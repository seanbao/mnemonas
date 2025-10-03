// Package webdav provides WebDAV protocol HTTP handler
// Implements RFC 4918 WebDAV standard
package webdav

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/seanbao/mnemonas/internal/storage"
)

var (
	errInvalidDepthHeader                 = errors.New("invalid Depth header")
	errPropfindDepthLimitExceeded         = errors.New("Depth header exceeds server recursion limit")
	errInvalidProppatchBody               = errors.New("invalid PROPPATCH request body")
	errInvalidOverwriteHeader             = errors.New("invalid Overwrite header")
	errLockTokenMatchesRequestURI         = errors.New("lock token does not match request URI")
	errLockRefreshRequiresToken           = errors.New("LOCK refresh requires a matching lock token")
	errPreconditionFailed                 = errors.New("precondition failed")
	errOverwriteDisabled                  = errors.New("destination exists and overwrite is disabled")
	errDestinationInsideSourceDirectory   = errors.New("destination cannot be inside source directory")
	errDirectoryCopyOverwriteNotSupported = errors.New("overwriting an existing destination with a directory copy is not supported")
	errWebDAVQuotaExceeded                = errors.New("user quota exceeded")
	errWebDAVPathAccessDenied             = errors.New("path access denied")
)

const webdavLockTimeout = time.Hour
const webdavLockCleanupInterval = 5 * time.Minute
const webdavLockTokenBytes = 16
const maxWebDAVLockTokenAttempts = 8
const webdavDeleteCleanupWarningHeader = `199 MnemoNAS "delete cleanup incomplete"`
const webdavTrashDeleteCleanupWarningHeader = `199 MnemoNAS "trash delete cleanup incomplete"`
const webdavWorkspaceMutationWarningHeader = `199 MnemoNAS "workspace mutation persistence incomplete"`
const maxPropfindTraversalDepth = 64
const webdavLockDepthZero = "0"
const webdavLockDepthInfinity = "infinity"
const maxWebDAVXMLRequestBody = 1 << 20
const untrustedWebDAVContentSecurityPolicy = "sandbox; default-src 'none'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; img-src 'self' data: blob:; media-src 'self' data: blob:; style-src 'unsafe-inline'"
const webDAVAuthTypeUsers = "users"

const (
	webDAVRoleAdmin = "admin"
	webDAVRoleUser  = "user"
	webDAVRoleGuest = "guest"
)

var webdavRandomRead = rand.Read

type webDAVScopeContextKey struct{}

type requestScope struct {
	username    string
	role        string
	groups      []string
	homeDir     string
	scoped      bool
	readOnly    bool
	quotaBytes  int64
	accessRules []DirectoryAccessRule
}

// DirectoryQuota limits logical current-file bytes under a MnemoNAS path.
type DirectoryQuota struct {
	Path       string
	QuotaBytes int64
}

// DirectoryAccessRule grants read/write access under a logical MnemoNAS path.
type DirectoryAccessRule struct {
	Path        string
	ReadUsers   []string
	WriteUsers  []string
	ReadGroups  []string
	WriteGroups []string
	ReadRoles   []string
	WriteRoles  []string
}

type accessMode string

const (
	accessRead  accessMode = "read"
	accessWrite accessMode = "write"
)

func scopeFromContext(ctx context.Context) requestScope {
	if ctx == nil {
		return requestScope{}
	}
	scope, _ := ctx.Value(webDAVScopeContextKey{}).(requestScope)
	return scope
}

func contextWithScope(ctx context.Context, scope requestScope) context.Context {
	return context.WithValue(ctx, webDAVScopeContextKey{}, scope)
}

func (s requestScope) storagePath(clientPath string) (string, bool) {
	cleanPath := path.Clean(clientPath)
	if !path.IsAbs(cleanPath) {
		cleanPath = "/" + cleanPath
	}
	if !s.scoped {
		return cleanPath, true
	}

	if strings.TrimSpace(s.homeDir) == "" {
		return "", false
	}
	if s.hasAccessRules() {
		if _, ok := s.matchAccessRule(cleanPath); ok {
			return cleanPath, true
		}
	}
	if cleanPath == "/" {
		return s.homeDir, true
	}
	relative := strings.TrimPrefix(cleanPath, "/")
	if relative == "" {
		return s.homeDir, true
	}
	return path.Clean(path.Join(s.homeDir, relative)), true
}

func (s requestScope) clientPath(storagePath string) (string, bool) {
	cleanPath := path.Clean(storagePath)
	if !path.IsAbs(cleanPath) {
		cleanPath = "/" + cleanPath
	}
	if !s.scoped {
		return cleanPath, true
	}

	homeDir := path.Clean(s.homeDir)
	if !path.IsAbs(homeDir) {
		homeDir = "/" + homeDir
	}
	if homeDir == "/" {
		return cleanPath, true
	}
	if cleanPath == homeDir {
		return "/", true
	}
	if strings.HasPrefix(cleanPath, homeDir+"/") {
		clientPath := strings.TrimPrefix(cleanPath, homeDir)
		if clientPath == "" {
			return "/", true
		}
		return clientPath, true
	}
	if s.hasAccessRules() {
		if _, ok := s.matchAccessRule(cleanPath); ok && s.canAccess(cleanPath, accessRead) {
			return cleanPath, true
		}
	}
	return "", false
}

func (s requestScope) isClientRoot(storagePath string) bool {
	clientPath, ok := s.clientPath(storagePath)
	return ok && clientPath == "/"
}

func (s requestScope) hasAccessRules() bool {
	return len(s.accessRules) > 0
}

func (s requestScope) canAccess(targetPath string, mode accessMode) bool {
	if !s.scoped {
		return true
	}
	targetPath = path.Clean(targetPath)
	if rule, ok := s.matchAccessRule(targetPath); ok {
		return accessRuleAllowsScope(rule, s, mode)
	}
	return strings.TrimSpace(s.homeDir) != "" && pathMatchesOrDescendant(path.Clean(s.homeDir), targetPath)
}

func (s requestScope) matchAccessRule(targetPath string) (DirectoryAccessRule, bool) {
	targetPath = path.Clean(targetPath)
	bestIndex := -1
	bestLength := -1
	for i, rule := range s.accessRules {
		if strings.TrimSpace(rule.Path) == "" || !pathMatchesOrDescendant(rule.Path, targetPath) {
			continue
		}
		if len(rule.Path) > bestLength {
			bestIndex = i
			bestLength = len(rule.Path)
		}
	}
	if bestIndex < 0 {
		return DirectoryAccessRule{}, false
	}
	return s.accessRules[bestIndex], true
}

func accessRuleAllowsScope(rule DirectoryAccessRule, scope requestScope, mode accessMode) bool {
	username := strings.ToLower(strings.TrimSpace(scope.username))
	role := strings.ToLower(strings.TrimSpace(scope.role))
	groups := make([]string, 0, len(scope.groups))
	for _, group := range scope.groups {
		group = strings.ToLower(strings.TrimSpace(group))
		if group != "" {
			groups = append(groups, group)
		}
	}

	users := rule.ReadUsers
	roles := rule.ReadRoles
	groupsAllowed := rule.ReadGroups
	if mode == accessWrite {
		users = rule.WriteUsers
		roles = rule.WriteRoles
		groupsAllowed = rule.WriteGroups
	} else {
		users = append(append([]string(nil), rule.ReadUsers...), rule.WriteUsers...)
		roles = append(append([]string(nil), rule.ReadRoles...), rule.WriteRoles...)
		groupsAllowed = append(append([]string(nil), rule.ReadGroups...), rule.WriteGroups...)
	}

	if slices.Contains(users, username) || slices.Contains(roles, role) {
		return true
	}
	for _, group := range groups {
		if slices.Contains(groupsAllowed, group) {
			return true
		}
	}
	return false
}

func normalizeScopedHomeDir(homeDir string) (string, bool) {
	trimmed := strings.TrimSpace(homeDir)
	if trimmed == "" || hasTraversalSegment(trimmed) {
		return "", false
	}
	normalized := path.Clean(strings.ReplaceAll(trimmed, "\\", "/"))
	if !path.IsAbs(normalized) {
		normalized = "/" + normalized
	}
	return normalized, true
}

type UserIdentity struct {
	Username   string
	Role       string
	Groups     []string
	HomeDir    string
	QuotaBytes int64
}

type UserAuthenticator func(ctx context.Context, username, password string) (*UserIdentity, error)

type webdavLock struct {
	token     string
	depth     string
	expiresAt time.Time
}

func (h *Handler) invalidatePropCache(paths ...string) {
	seen := make(map[string]struct{}, len(paths))
	for _, cachePath := range paths {
		if cachePath == "" {
			continue
		}
		if _, ok := seen[cachePath]; ok {
			continue
		}
		seen[cachePath] = struct{}{}
		h.propCache.Invalidate(cachePath)
	}
}

// Handler is the WebDAV request handler
type Handler struct {
	fs                   *storage.FileSystem
	prefix               string
	readOnly             bool
	pathLock             *PathLock
	propCache            *PropfindCache
	locksMu              sync.Mutex
	locks                map[string]webdavLock
	lockCleanupInterval  time.Duration
	lockCleanupStartOnce sync.Once
	lockCleanupStopOnce  sync.Once
	lockCleanupStarted   chan struct{}
	lockCleanupStop      chan struct{}
	lockCleanupDone      chan struct{}
	newLockToken         func() (string, error)
	authType          string
	username          string
	password          string
	userAuthenticator UserAuthenticator
	quotaMu           sync.Mutex
	directoryQuotas   []DirectoryQuota
	directoryAccess   []DirectoryAccessRule
	beforeCopyFile    func(srcPath, dstPath string) error
}

// Config holds WebDAV handler configuration
type Config struct {
	FileSystem *storage.FileSystem
	Prefix     string // URL prefix, e.g., "/dav"
	ReadOnly   bool
	AuthType          string // "none", "basic", "users"
	Username          string
	Password          string
	UserAuthenticator UserAuthenticator
	DirectoryQuotas   []DirectoryQuota
	DirectoryAccess   []DirectoryAccessRule
}

// NewHandler creates a WebDAV handler
func NewHandler(cfg Config) *Handler {
	return &Handler{
		fs:                  cfg.FileSystem,
		prefix:              strings.TrimSuffix(cfg.Prefix, "/"),
		readOnly:            cfg.ReadOnly,
		pathLock:            NewPathLock(),
		propCache:           NewPropfindCache(30*time.Second, 1000),
		locks:               make(map[string]webdavLock),
		lockCleanupInterval: webdavLockCleanupInterval,
		lockCleanupStarted:  make(chan struct{}),
		lockCleanupStop:     make(chan struct{}),
		lockCleanupDone:     make(chan struct{}),
		newLockToken:        generateOpaqueLockToken,
		authType:            strings.ToLower(strings.TrimSpace(cfg.AuthType)),
		username:            cfg.Username,
		password:            cfg.Password,
		userAuthenticator:   cfg.UserAuthenticator,
		directoryQuotas:     cloneWebDAVDirectoryQuotas(cfg.DirectoryQuotas),
		directoryAccess:     cloneWebDAVDirectoryAccessRules(cfg.DirectoryAccess),
	}
}

func cloneWebDAVDirectoryQuotas(quotas []DirectoryQuota) []DirectoryQuota {
	if len(quotas) == 0 {
		return nil
	}
	return append([]DirectoryQuota(nil), quotas...)
}

func cloneWebDAVDirectoryAccessRules(rules []DirectoryAccessRule) []DirectoryAccessRule {
	if len(rules) == 0 {
		return nil
	}
	cloned := make([]DirectoryAccessRule, len(rules))
	for i, rule := range rules {
		cloned[i] = rule
		cloned[i].ReadUsers = append([]string(nil), rule.ReadUsers...)
		cloned[i].WriteUsers = append([]string(nil), rule.WriteUsers...)
		cloned[i].ReadGroups = append([]string(nil), rule.ReadGroups...)
		cloned[i].WriteGroups = append([]string(nil), rule.WriteGroups...)
		cloned[i].ReadRoles = append([]string(nil), rule.ReadRoles...)
		cloned[i].WriteRoles = append([]string(nil), rule.WriteRoles...)
	}
	return cloned
}

// Close stops background resources owned by the handler.
func (h *Handler) Close() {
	select {
	case <-h.lockCleanupStarted:
		h.lockCleanupStopOnce.Do(func() {
			close(h.lockCleanupStop)
		})
		<-h.lockCleanupDone
	default:
	}
}

// OnPathRenamed invalidates cached listings and rebases locks after an external rename.
func (h *Handler) OnPathRenamed(oldPath, newPath string) {
	affectedPaths := []string{path.Dir(oldPath), oldPath, path.Dir(newPath), newPath}
	h.moveLocksUnderPath(oldPath, newPath)
	h.invalidatePropCache(affectedPaths...)
}

// OnPathDeleted invalidates cached listings and clears locks after an external delete.
func (h *Handler) OnPathDeleted(filePath string) *storage.PathDeleteHookResult {
	affectedPaths := []string{path.Dir(filePath), filePath}
	removedLocks := h.clearLocksUnderPathWithSnapshot(filePath)
	h.invalidatePropCache(affectedPaths...)
	if len(removedLocks) == 0 {
		return nil
	}

	return &storage.PathDeleteHookResult{
		Rollback: func() error {
			h.restoreLocks(removedLocks)
			return nil
		},
	}
}

func (h *Handler) startLockCleanupLoop() {
	if h.lockCleanupInterval <= 0 {
		return
	}

	h.lockCleanupStartOnce.Do(func() {
		close(h.lockCleanupStarted)

		go func() {
			ticker := time.NewTicker(h.lockCleanupInterval)
			defer ticker.Stop()
			defer close(h.lockCleanupDone)

			for {
				select {
				case <-ticker.C:
					h.locksMu.Lock()
					h.removeExpiredLocksLocked(time.Now())
					h.locksMu.Unlock()
				case <-h.lockCleanupStop:
					return
				}
			}
		}()
	})
}

// ServeHTTP handles WebDAV requests
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	identity, authenticated := h.authenticate(r)
	if !authenticated {
		w.Header().Set("WWW-Authenticate", `Basic realm="MnemoNAS WebDAV"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	scope, err := h.scopeForIdentity(identity)
	if err != nil {
		http.Error(w, "home directory is not available", http.StatusForbidden)
		return
	}

	requestPath := r.URL.Path
	if requestPath == "" {
		requestPath = "/"
	}
	if hasTraversalSegment(requestPath) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// Validate path to prevent path traversal attacks (C1 fix)
	cleanPath := path.Clean(requestPath)
	if !path.IsAbs(cleanPath) {
		cleanPath = "/" + cleanPath
	}
	clientPath, ok := trimWebDAVPrefix(cleanPath, h.prefix)
	if !ok {
		http.NotFound(w, r)
		return
	}
	filePath, ok := scope.storagePath(clientPath)
	if !ok {
		http.Error(w, "home directory is not available", http.StatusForbidden)
		return
	}

	ctx := contextWithScope(r.Context(), scope)
	r = r.WithContext(ctx)

	if mode, ok := accessModeForMethod(r.Method); ok && !authorizeScopePath(w, scope, filePath, mode) {
		return
	}

	// Check read-only mode
	if (h.readOnly || scope.readOnly) && !isReadMethod(r.Method) {
		http.Error(w, "read-only mode", http.StatusForbidden)
		return
	}

	switch r.Method {
	case "OPTIONS":
		h.handleOptions(w, r)
	case http.MethodGet, http.MethodHead:
		h.handleGet(ctx, w, r, filePath)
	case http.MethodPut:
		h.handlePut(ctx, w, r, filePath)
	case http.MethodDelete:
		h.handleDelete(ctx, w, r, filePath)
	case "MKCOL":
		h.handleMkcol(ctx, w, r, filePath)
	case "COPY":
		h.handleCopy(ctx, w, r, filePath)
	case "MOVE":
		h.handleMove(ctx, w, r, filePath)
	case "PROPFIND":
		h.handlePropfind(ctx, w, r, filePath)
	case "PROPPATCH":
		h.handleProppatch(ctx, w, r, filePath)
	case "LOCK":
		h.handleLock(ctx, w, r, filePath)
	case "UNLOCK":
		h.handleUnlock(ctx, w, r, filePath)
	default:
		http.Error(w, "method not supported", http.StatusMethodNotAllowed)
	}
}

func accessModeForMethod(method string) (accessMode, bool) {
	switch method {
	case "OPTIONS":
		return accessRead, false
	case http.MethodGet, http.MethodHead, "PROPFIND", "COPY":
		return accessRead, true
	default:
		return accessWrite, true
	}
}

func authorizeScopePath(w http.ResponseWriter, scope requestScope, targetPath string, mode accessMode) bool {
	if scope.canAccess(targetPath, mode) {
		return true
	}
	http.Error(w, "path access denied", http.StatusForbidden)
	return false
}

func (h *Handler) handleOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "OPTIONS, GET, HEAD, PUT, DELETE, MKCOL, COPY, MOVE, PROPFIND, PROPPATCH, LOCK, UNLOCK")
	w.Header().Set("DAV", "1, 2")
	w.Header().Set("MS-Author-Via", "DAV")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleGet(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	releaseLocks := h.acquireHierarchyLocks(hierarchyLockSpec{path: filePath, write: false})
	defer releaseLocks()

	info, err := h.fs.Stat(ctx, filePath)
	if err != nil {
		h.handleError(w, err)
		return
	}

	if info.IsDir {
		if r.Method == http.MethodHead {
			setUntrustedWebDAVContentHeaders(w.Header())
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			return
		}

		// Return directory listing (simple HTML)
		h.serveDirectory(ctx, w, r, filePath, info)
		return
	}

	reader, snapshotInfo, err := h.fs.OpenFileSnapshot(ctx, filePath)
	if err != nil {
		h.handleError(w, err)
		return
	}
	defer reader.Close()

	etag := fmt.Sprintf(`"%s"`, snapshotInfo.ContentHash)

	// Check If-Match (precondition) before cache validators.
	if im := r.Header.Get("If-Match"); im != "" {
		if !h.matchETag(im, etag) {
			http.Error(w, errPreconditionFailed.Error(), http.StatusPreconditionFailed)
			return
		}
	}

	if ius := r.Header.Get("If-Unmodified-Since"); ius != "" {
		if unmodifiedSince, err := http.ParseTime(ius); err == nil && isHTTPTimeAfter(snapshotInfo.ModTime, unmodifiedSince) {
			http.Error(w, errPreconditionFailed.Error(), http.StatusPreconditionFailed)
			return
		}
	}

	// Check If-None-Match (conditional GET)
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		if h.matchETag(inm, etag) {
			w.Header().Set("ETag", etag)
			w.Header().Set("Last-Modified", snapshotInfo.ModTime.UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusNotModified)
			return
		}
	} else if ims := r.Header.Get("If-Modified-Since"); ims != "" {
		if modifiedSince, err := http.ParseTime(ims); err == nil && !isHTTPTimeAfter(snapshotInfo.ModTime, modifiedSince) {
			w.Header().Set("ETag", etag)
			w.Header().Set("Last-Modified", snapshotInfo.ModTime.UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	// Set ETag header for caching
	w.Header().Set("ETag", etag)
	w.Header().Set("Accept-Ranges", "bytes")
	setUntrustedWebDAVContentHeaders(w.Header())
	if contentType := fileContentType(filePath); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}

	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.FormatInt(snapshotInfo.Size, 10))
		w.Header().Set("Last-Modified", snapshotInfo.ModTime.UTC().Format(http.TimeFormat))
		return
	}

	// Use http.ServeContent to handle Range requests automatically
	// Pass filename from path for Content-Disposition
	http.ServeContent(w, r, path.Base(filePath), snapshotInfo.ModTime, reader)
}

func fileContentType(filePath string) string {
	contentType := mime.TypeByExtension(path.Ext(filePath))
	if contentType == "" {
		return "application/octet-stream"
	}
	return contentType
}

func isHTTPTimeAfter(modTime, headerTime time.Time) bool {
	return modTime.UTC().Truncate(time.Second).After(headerTime.UTC().Truncate(time.Second))
}

func (h *Handler) serveDirectory(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string, info *storage.FileInfo) {
	children, err := h.fs.ReadDir(ctx, filePath)
	if err != nil {
		h.handleError(w, err)
		return
	}
	scope := scopeFromContext(ctx)
	displayPath, ok := scope.clientPath(filePath)
	if !ok {
		displayPath = filePath
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	setUntrustedWebDAVContentHeaders(w.Header())
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Index of %s</title></head>
<body>
<h1>Index of %s</h1>
<hr>
<pre>
`, html.EscapeString(displayPath), html.EscapeString(displayPath))

	if !scope.isClientRoot(filePath) {
		parentPath := path.Dir(filePath)
		parentClientPath, ok := scope.clientPath(parentPath)
		if !ok {
			parentClientPath = parentPath
		}
		parentHref := escapeDirectoryHref(path.Join(h.prefix, parentClientPath))
		fmt.Fprintf(w, `<a href="%s">..</a>
`, html.EscapeString(parentHref))
	}

	for _, child := range children {
		if !scope.canAccess(child.Path, accessRead) {
			continue
		}
		name := path.Base(child.Path)
		if child.IsDir {
			name += "/"
		}
		// Escape child names and HREFs before rendering the HTML fallback.
		fmt.Fprintf(w, `<a href="%s">%s</a>    %s    %d
`,
			html.EscapeString(h.webdavHref(ctx, child.Path, child.IsDir)),
			html.EscapeString(name),
			child.ModTime.Format(time.RFC3339),
			child.Size,
		)
	}

	fmt.Fprintf(w, `</pre>
<hr>
</body>
</html>`)
}

func escapeDirectoryHref(rawPath string) string {
	return (&url.URL{Path: rawPath}).EscapedPath()
}

func setUntrustedWebDAVContentHeaders(header http.Header) {
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", untrustedWebDAVContentSecurityPolicy)
}

func (h *Handler) webdavHref(ctx context.Context, filePath string, isDir bool) string {
	clientPath, ok := scopeFromContext(ctx).clientPath(filePath)
	if !ok {
		clientPath = filePath
	}
	rawHref := path.Join(h.prefix, clientPath)
	if isDir && !strings.HasSuffix(rawHref, "/") {
		rawHref += "/"
	}
	return escapeDirectoryHref(rawHref)
}

func (h *Handler) handlePut(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	releaseLocks := h.acquireHierarchyLocks(hierarchyLockSpec{path: filePath, write: true})
	defer releaseLocks()

	if strings.TrimSpace(r.Header.Get("Content-Range")) != "" {
		http.Error(w, "Content-Range is not supported for PUT", http.StatusBadRequest)
		return
	}

	existingInfo, statErr := h.fs.Stat(ctx, filePath)
	isCreate := false
	if statErr != nil {
		if errors.Is(statErr, storage.ErrNotFound) || errors.Is(statErr, storage.ErrNotDir) {
			isCreate = true
		} else {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	resourcePaths := []string{filePath}
	namespacePaths := []string(nil)
	if isCreate {
		resourcePaths = nil
		namespacePaths = namespacePathsForResources(filePath)
	}
	if !h.authorizeWriteLockWithScope(w, r, filePath, resourcePaths, namespacePaths, nil) {
		return
	}

	// Check if parent directory exists
	parent := path.Dir(filePath)
	if parent != "/" {
		parentInfo, err := h.fs.Stat(ctx, parent)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				http.Error(w, "parent directory not found", http.StatusConflict)
				return
			}
			if errors.Is(err, storage.ErrNotDir) {
				http.Error(w, "parent path is not a directory", http.StatusConflict)
				return
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if !parentInfo.IsDir {
			http.Error(w, "parent path is not a directory", http.StatusConflict)
			return
		}
	}

	if !isCreate {
		etag := fmt.Sprintf(`"%s"`, existingInfo.ContentHash)

		// If-Match: only update if ETag matches (prevent overwrite conflicts)
		if im := r.Header.Get("If-Match"); im != "" {
			if !h.matchETag(im, etag) {
				http.Error(w, errPreconditionFailed.Error(), http.StatusPreconditionFailed)
				return
			}
		}

		if ius := r.Header.Get("If-Unmodified-Since"); ius != "" {
			if unmodifiedSince, err := http.ParseTime(ius); err == nil && isHTTPTimeAfter(existingInfo.ModTime, unmodifiedSince) {
				http.Error(w, errPreconditionFailed.Error(), http.StatusPreconditionFailed)
				return
			}
		}

		// If-None-Match: prevent update when any provided validator matches the current representation.
		if inm := r.Header.Get("If-None-Match"); inm != "" {
			if h.matchETag(inm, etag) {
				http.Error(w, errPreconditionFailed.Error(), http.StatusPreconditionFailed)
				return
			}
		}
	} else {
		// If-Match with non-existent file should fail
		if im := r.Header.Get("If-Match"); im != "" {
			http.Error(w, errPreconditionFailed.Error(), http.StatusPreconditionFailed)
			return
		}
	}

	affectedPaths := []string{parent, filePath}
	h.invalidatePropCache(affectedPaths...)

	reader, releaseQuota, err := h.quotaCheckedUploadReader(ctx, filePath, r.Body, r.ContentLength)
	if err != nil {
		h.handleError(w, err)
		return
	}
	defer releaseQuota()

	err = h.fs.WriteFile(ctx, filePath, reader)
	if err != nil {
		h.handleError(w, err)
		return
	}

	h.invalidatePropCache(affectedPaths...)

	// Return new ETag
	newInfo, _ := h.fs.Stat(ctx, filePath)
	if newInfo != nil {
		w.Header().Set("ETag", fmt.Sprintf(`"%s"`, newInfo.ContentHash))
	}

	if isCreate {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) handleDelete(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	releaseLocks := h.acquireHierarchyLocks(hierarchyLockSpec{path: filePath, write: true})
	defer releaseLocks()

	if !h.authorizeWriteLockWithScope(w, r, filePath, []string{filePath}, namespacePathsForResources(filePath), []string{filePath}) {
		return
	}

	info, err := h.fs.Stat(ctx, filePath)
	if err != nil {
		h.handleError(w, err)
		return
	}
	if info.IsDir {
		if err := h.validateInfinityOnlyDepth(r.Header.Get("Depth")); err != nil {
			writeKnownWebDAVError(w, errInvalidDepthHeader, http.StatusBadRequest)
			return
		}
	}
	if err := h.authorizeTreeAccess(ctx, filePath, accessWrite); err != nil {
		h.handleError(w, err)
		return
	}

	etag := fmt.Sprintf(`"%s"`, info.ContentHash)
	if h.writeFailedMutationPrecondition(w, r, etag, info.ModTime) {
		return
	}

	affectedPaths := []string{path.Dir(filePath), filePath}
	h.invalidatePropCache(affectedPaths...)

	if err := h.fs.Delete(ctx, filePath); err != nil {
		if !markDeleteWarningHeaders(w, err) {
			h.handleError(w, err)
			return
		}
	}
	h.clearLocksUnderPath(filePath)

	h.invalidatePropCache(affectedPaths...)

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleMkcol(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	// MKCOL does not allow request body
	hasBody, err := requestHasBody(r)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if hasBody {
		http.Error(w, "MKCOL does not allow request body", http.StatusUnsupportedMediaType)
		return
	}
	releaseLocks := h.acquireHierarchyLocks(hierarchyLockSpec{path: filePath, write: true})
	defer releaseLocks()

	if !h.authorizeWriteLockWithScope(w, r, filePath, []string{filePath}, namespacePathsForResources(filePath), nil) {
		return
	}

	if _, err := h.fs.Stat(ctx, filePath); err == nil {
		http.Error(w, "resource already exists", http.StatusMethodNotAllowed)
		return
	} else if !errors.Is(err, storage.ErrNotFound) && !errors.Is(err, storage.ErrNotDir) {
		h.handleError(w, err)
		return
	}

	affectedPaths := []string{path.Dir(filePath), filePath}
	h.invalidatePropCache(affectedPaths...)

	if err := h.fs.Mkdir(ctx, filePath); err != nil {
		if errors.Is(err, storage.ErrAlreadyExists) {
			http.Error(w, "resource already exists", http.StatusMethodNotAllowed)
			return
		}
		if errors.Is(err, storage.ErrNotDir) {
			http.Error(w, "parent path is not a directory", http.StatusConflict)
			return
		}
		if isVisibleMutationWarning(err) {
			markVisibleMutationWarningHeader(w)
		} else {
			h.handleError(w, err)
			return
		}
	}

	h.invalidatePropCache(affectedPaths...)

	w.WriteHeader(http.StatusCreated)
}

func requestHasBody(r *http.Request) (bool, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return false, nil
	}
	if r.ContentLength > 0 {
		return true, nil
	}
	if r.ContentLength == 0 {
		return false, nil
	}

	var probe [1]byte
	n, err := r.Body.Read(probe[:])
	if n > 0 {
		r.Body = prependReadCloser(bytes.NewReader(probe[:n]), r.Body)
		if err != nil && err != io.EOF {
			return true, err
		}
		return true, nil
	}
	if err == io.EOF {
		return false, nil
	}
	return false, err
}

func prependReadCloser(prefix io.Reader, body io.ReadCloser) io.ReadCloser {
	return struct {
		io.Reader
		io.Closer
	}{
		Reader: io.MultiReader(prefix, body),
		Closer: body,
	}
}

func (h *Handler) handleCopy(ctx context.Context, w http.ResponseWriter, r *http.Request, srcPath string) {
	dst := h.getDestination(r)
	if dst == "" {
		http.Error(w, "missing Destination header", http.StatusBadRequest)
		return
	}
	if srcPath == dst {
		http.Error(w, "source and destination must differ", http.StatusForbidden)
		return
	}
	if !authorizeScopePath(w, scopeFromContext(ctx), dst, accessWrite) {
		return
	}

	releaseLocks := h.acquireHierarchyLocks(
		hierarchyLockSpec{path: srcPath, write: false},
		hierarchyLockSpec{path: dst, write: true},
	)
	defer releaseLocks()

	if !h.authorizeWriteLockWithScope(w, r, srcPath, []string{dst}, namespacePathsForResources(dst), nil) {
		return
	}

	srcInfo, err := h.fs.Stat(ctx, srcPath)
	if err != nil {
		if h.writeParentNotDirectoryConflict(w, err) {
			return
		}
		h.handleError(w, err)
		return
	}

	copyDepth := "infinity"
	if srcInfo.IsDir {
		copyDepth, err = h.parseCopyDepth(r.Header.Get("Depth"))
		if err != nil {
			writeKnownWebDAVError(w, errInvalidDepthHeader, http.StatusBadRequest)
			return
		}
	}

	dstExists := h.destinationExists(ctx, dst)

	if err := h.checkOverwriteHeader(ctx, r, dst); err != nil {
		if h.writeExpectedWebDAVError(w, err, http.StatusPreconditionFailed, errInvalidOverwriteHeader, errOverwriteDisabled) {
			return
		}
		if h.writeParentNotDirectoryConflict(w, err) {
			return
		}
		h.handleError(w, err)
		return
	}

	if err := h.rejectDirectoryDescendantDestination(ctx, srcPath, dst); err != nil {
		if h.writeExpectedWebDAVError(w, err, http.StatusConflict, errDestinationInsideSourceDirectory) {
			return
		}
		if h.writeParentNotDirectoryConflict(w, err) {
			return
		}
		h.handleError(w, err)
		return
	}
	if err := h.rejectDirectoryCopyOverwrite(ctx, srcPath, dst, dstExists); err != nil {
		if h.writeExpectedWebDAVError(w, err, http.StatusConflict, errDirectoryCopyOverwriteNotSupported) {
			return
		}
		if h.writeParentNotDirectoryConflict(w, err) {
			return
		}
		h.handleError(w, err)
		return
	}
	if srcInfo.IsDir && copyDepth != "0" {
		if err := h.authorizeCopyDestinationTreeAccess(ctx, srcPath, dst); err != nil {
			h.handleError(w, err)
			return
		}
	}

	affectedPaths := []string{path.Dir(dst), dst}
	h.invalidatePropCache(affectedPaths...)

	allowOverwrite := dstExists && !srcInfo.IsDir
	copyErr := h.copyResourceWithQuota(ctx, srcPath, dst, copyDepth, allowOverwrite)
	if copyErr != nil && !isVisibleMutationWarning(copyErr) {
		if strings.EqualFold(r.Header.Get("Overwrite"), "F") && errors.Is(copyErr, storage.ErrAlreadyExists) {
			writeKnownWebDAVError(w, errOverwriteDisabled, http.StatusPreconditionFailed)
			return
		}
		if h.writeParentNotDirectoryConflict(w, copyErr) {
			return
		}
		h.handleError(w, copyErr)
		return
	}
	if copyErr != nil {
		markVisibleMutationWarningHeader(w)
	}

	h.invalidatePropCache(affectedPaths...)

	if dstExists {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) copyResourceWithQuota(ctx context.Context, srcPath, dstPath, depth string, allowOverwrite bool) error {
	scope := scopeFromContext(ctx)
	directoryRules := h.directoryQuotaRulesForTarget(dstPath)
	userQuotaApplies := scope.scoped && scope.quotaBytes > 0 && strings.TrimSpace(scope.homeDir) != "" && pathMatchesOrDescendant(scope.homeDir, dstPath)
	if !userQuotaApplies && len(directoryRules) == 0 {
		return h.copyResource(ctx, srcPath, dstPath, depth, allowOverwrite)
	}

	h.quotaMu.Lock()
	defer h.quotaMu.Unlock()

	requiredBytes, err := h.copyRequiredBytes(ctx, srcPath, dstPath, depth, allowOverwrite)
	if err != nil {
		return err
	}
	if userQuotaApplies {
		usedBytes, err := h.pathLogicalSizeIfExists(ctx, scope.homeDir)
		if err != nil {
			return err
		}
		if err := ensureWebDAVQuotaAvailable(usedBytes, scope.quotaBytes, requiredBytes); err != nil {
			return err
		}
	}
	for _, rule := range directoryRules {
		usedBytes, err := h.pathLogicalSizeIfExists(ctx, rule.Path)
		if err != nil {
			return err
		}
		if err := ensureWebDAVQuotaAvailable(usedBytes, rule.QuotaBytes, requiredBytes); err != nil {
			return err
		}
	}

	return h.copyResource(ctx, srcPath, dstPath, depth, allowOverwrite)
}

func (h *Handler) copyRequiredBytes(ctx context.Context, srcPath, dstPath, depth string, allowOverwrite bool) (int64, error) {
	info, err := h.fs.Stat(ctx, srcPath)
	if err != nil {
		return 0, err
	}
	if info.IsDir && depth == "0" {
		return 0, nil
	}

	requiredBytes, err := h.copySourceLogicalSize(ctx, info)
	if err != nil {
		return 0, err
	}
	if allowOverwrite && !info.IsDir {
		replacedBytes, err := h.existingUploadTargetSize(ctx, dstPath)
		if err != nil {
			return 0, err
		}
		requiredBytes -= replacedBytes
		if requiredBytes < 0 {
			requiredBytes = 0
		}
	}
	return requiredBytes, nil
}

func (h *Handler) copySourceLogicalSize(ctx context.Context, info *storage.FileInfo) (int64, error) {
	if info == nil {
		return 0, nil
	}
	scope := scopeFromContext(ctx)
	if scope.scoped && !scope.canAccess(info.Path, accessRead) {
		return 0, nil
	}
	if !info.IsDir {
		return nonNegativeSize(info.Size), nil
	}

	children, err := h.fs.ReadDir(ctx, info.Path)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, child := range children {
		if child == nil {
			continue
		}
		size, err := h.copySourceLogicalSize(ctx, child)
		if err != nil {
			return 0, err
		}
		total, err = addWebDAVQuotaSize(total, size)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func (h *Handler) copyResource(ctx context.Context, srcPath, dstPath, depth string, allowOverwrite bool) error {
	if !scopeFromContext(ctx).canAccess(dstPath, accessWrite) {
		return errWebDAVPathAccessDenied
	}

	info, err := h.fs.Stat(ctx, srcPath)
	if err != nil {
		return err
	}

	if info.IsDir {
		var copyWarning error
		if err := h.fs.Mkdir(ctx, dstPath); err != nil {
			if isVisibleMutationWarning(err) {
				copyWarning = mergeVisibleMutationWarning(copyWarning, err)
			} else {
				return err
			}
		}
		if depth == "0" {
			return copyWarning
		}

		children, err := h.fs.ReadDir(ctx, srcPath)
		if err != nil {
			return h.rollbackCopiedDirectory(dstPath, err)
		}

		scope := scopeFromContext(ctx)
		for _, child := range children {
			if !scope.canAccess(child.Path, accessRead) {
				continue
			}
			childName := path.Base(child.Path)
			if err := h.copyResource(ctx, child.Path, path.Join(dstPath, childName), "infinity", false); err != nil {
				if isVisibleMutationWarning(err) {
					copyWarning = mergeVisibleMutationWarning(copyWarning, err)
					continue
				}
				return h.rollbackCopiedDirectory(dstPath, err)
			}
		}
		return copyWarning
	}

	if h.beforeCopyFile != nil {
		if err := h.beforeCopyFile(srcPath, dstPath); err != nil {
			return err
		}
	}

	if !allowOverwrite {
		return h.fs.Copy(ctx, srcPath, dstPath)
	}

	reader, err := h.fs.OpenFile(ctx, srcPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	return h.fs.WriteFile(ctx, dstPath, reader)
}

func (h *Handler) quotaCheckedUploadReader(ctx context.Context, targetPath string, reader io.Reader, contentLength int64) (io.Reader, func(), error) {
	scope := scopeFromContext(ctx)
	directoryRules := h.directoryQuotaRulesForTarget(targetPath)
	userQuotaApplies := scope.scoped && scope.quotaBytes > 0 && strings.TrimSpace(scope.homeDir) != "" && pathMatchesOrDescendant(scope.homeDir, targetPath)
	if !userQuotaApplies && len(directoryRules) == 0 {
		return reader, func() {}, nil
	}

	h.quotaMu.Lock()
	unlock := func() {
		h.quotaMu.Unlock()
	}

	replacedBytes, err := h.existingUploadTargetSize(ctx, targetPath)
	if err != nil {
		unlock()
		return nil, nil, err
	}

	availableBytes := int64(^uint64(0) >> 1)
	if userQuotaApplies {
		usedBytes, err := h.pathLogicalSizeIfExists(ctx, scope.homeDir)
		if err != nil {
			unlock()
			return nil, nil, err
		}
		candidateAvailable := webDAVQuotaAvailableBytes(usedBytes-replacedBytes, scope.quotaBytes)
		if contentLength >= 0 && contentLength > candidateAvailable {
			unlock()
			return nil, nil, errWebDAVQuotaExceeded
		}
		if candidateAvailable < availableBytes {
			availableBytes = candidateAvailable
		}
	}
	for _, rule := range directoryRules {
		usedBytes, err := h.pathLogicalSizeIfExists(ctx, rule.Path)
		if err != nil {
			unlock()
			return nil, nil, err
		}
		candidateAvailable := webDAVQuotaAvailableBytes(usedBytes-replacedBytes, rule.QuotaBytes)
		if contentLength >= 0 && contentLength > candidateAvailable {
			unlock()
			return nil, nil, errWebDAVQuotaExceeded
		}
		if candidateAvailable < availableBytes {
			availableBytes = candidateAvailable
		}
	}

	return &quotaLimitedReader{
		reader:    reader,
		remaining: availableBytes,
		err:       errWebDAVQuotaExceeded,
	}, unlock, nil
}

func (h *Handler) existingUploadTargetSize(ctx context.Context, targetPath string) (int64, error) {
	info, err := h.fs.Stat(ctx, targetPath)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrNotDir) {
			return 0, nil
		}
		return 0, err
	}
	if info.IsDir {
		return 0, storage.ErrIsDir
	}
	return nonNegativeSize(info.Size), nil
}

func (h *Handler) pathLogicalSizeIfExists(ctx context.Context, targetPath string) (int64, error) {
	size, err := h.pathLogicalSize(ctx, targetPath)
	if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrNotDir) {
		return 0, nil
	}
	return size, err
}

func (h *Handler) pathLogicalSize(ctx context.Context, targetPath string) (int64, error) {
	info, err := h.fs.Stat(ctx, targetPath)
	if err != nil {
		return 0, err
	}
	return h.fileInfoLogicalSize(ctx, info)
}

func (h *Handler) fileInfoLogicalSize(ctx context.Context, info *storage.FileInfo) (int64, error) {
	if info == nil {
		return 0, nil
	}
	if !info.IsDir {
		return nonNegativeSize(info.Size), nil
	}

	children, err := h.fs.ReadDir(ctx, info.Path)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, child := range children {
		size, err := h.fileInfoLogicalSize(ctx, child)
		if err != nil {
			return 0, err
		}
		total, err = addWebDAVQuotaSize(total, size)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func ensureWebDAVQuotaAvailable(usedBytes, quotaBytes, requiredBytes int64) error {
	requiredBytes = nonNegativeSize(requiredBytes)
	if requiredBytes > webDAVQuotaAvailableBytes(usedBytes, quotaBytes) {
		return errWebDAVQuotaExceeded
	}
	return nil
}

func webDAVQuotaAvailableBytes(usedBytes, quotaBytes int64) int64 {
	if usedBytes < 0 {
		usedBytes = 0
	}
	if quotaBytes < 0 {
		quotaBytes = 0
	}
	availableBytes := quotaBytes - usedBytes
	if availableBytes < 0 {
		return 0
	}
	return availableBytes
}

func (h *Handler) directoryQuotaRulesForTarget(targetPath string) []DirectoryQuota {
	if len(h.directoryQuotas) == 0 {
		return nil
	}
	matched := make([]DirectoryQuota, 0, len(h.directoryQuotas))
	for _, rule := range h.directoryQuotas {
		if rule.QuotaBytes <= 0 {
			continue
		}
		if pathMatchesOrDescendant(rule.Path, targetPath) {
			matched = append(matched, rule)
		}
	}
	return matched
}

func (h *Handler) ensureMoveQuota(ctx context.Context, srcPath, dstPath string, dstExists bool) error {
	scope := scopeFromContext(ctx)
	directoryRules := h.directoryQuotaRulesForTarget(dstPath)
	userQuotaApplies := scope.scoped && scope.quotaBytes > 0 && strings.TrimSpace(scope.homeDir) != "" && pathMatchesOrDescendant(scope.homeDir, dstPath) && !pathMatchesOrDescendant(scope.homeDir, srcPath)
	if !userQuotaApplies && len(directoryRules) == 0 {
		return nil
	}

	requiredBytes, err := h.pathLogicalSize(ctx, srcPath)
	if err != nil {
		return err
	}

	var replacedBytes int64
	if dstExists {
		replacedBytes, err = h.pathLogicalSizeIfExists(ctx, dstPath)
		if err != nil {
			return err
		}
	}

	if userQuotaApplies {
		usedBytes, err := h.pathLogicalSizeIfExists(ctx, scope.homeDir)
		if err != nil {
			return err
		}
		deltaBytes := requiredBytes - replacedBytes
		if deltaBytes < 0 {
			deltaBytes = 0
		}
		if err := ensureWebDAVQuotaAvailable(usedBytes, scope.quotaBytes, deltaBytes); err != nil {
			return err
		}
	}

	for _, rule := range directoryRules {
		if pathMatchesOrDescendant(rule.Path, srcPath) {
			continue
		}
		usedBytes, err := h.pathLogicalSizeIfExists(ctx, rule.Path)
		if err != nil {
			return err
		}
		deltaBytes := requiredBytes - replacedBytes
		if deltaBytes < 0 {
			deltaBytes = 0
		}
		if err := ensureWebDAVQuotaAvailable(usedBytes, rule.QuotaBytes, deltaBytes); err != nil {
			return err
		}
	}
	return nil
}

func addWebDAVQuotaSize(left, right int64) (int64, error) {
	if right <= 0 {
		return left, nil
	}
	const maxInt64 = int64(^uint64(0) >> 1)
	if left > maxInt64-right {
		return 0, errors.New("logical file size overflow")
	}
	return left + right, nil
}

func nonNegativeSize(size int64) int64 {
	if size < 0 {
		return 0
	}
	return size
}

type quotaLimitedReader struct {
	reader    io.Reader
	remaining int64
	err       error
}

func (r *quotaLimitedReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			return 0, r.err
		}
		if err != nil {
			return 0, err
		}
		return 0, nil
	}

	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

func isVisibleMutationWarning(err error) bool {
	var warningErr *storage.VisibleMutationWarningError
	return errors.As(err, &warningErr)
}

func isTrashDeleteWarning(err error) bool {
	var warningErr *storage.TrashDeleteWarningError
	return errors.As(err, &warningErr)
}

func isDeleteCleanupWarning(err error) bool {
	var warningErr *storage.DeleteCleanupWarningError
	return errors.As(err, &warningErr)
}

func markDeleteWarningHeaders(w http.ResponseWriter, err error) bool {
	if isTrashDeleteWarning(err) {
		markTrashDeleteCleanupWarningHeader(w)
	}
	if isDeleteCleanupWarning(err) {
		markDeleteCleanupWarningHeader(w)
	}
	if isVisibleMutationWarning(err) {
		markVisibleMutationWarningHeader(w)
	}
	return isTrashDeleteWarning(err) || isDeleteCleanupWarning(err) || isVisibleMutationWarning(err)
}

func mergeVisibleMutationWarning(existing, err error) error {
	if err == nil {
		return existing
	}
	if existing == nil {
		return err
	}
	return errors.Join(existing, err)
}

func markVisibleMutationWarningHeader(w http.ResponseWriter) {
	if w == nil {
		return
	}
	headers := w.Header()
	for _, warningValue := range headers.Values("Warning") {
		if warningValue == webdavWorkspaceMutationWarningHeader {
			return
		}
	}
	headers.Add("Warning", webdavWorkspaceMutationWarningHeader)
}

func markTrashDeleteCleanupWarningHeader(w http.ResponseWriter) {
	if w == nil {
		return
	}
	headers := w.Header()
	for _, warningValue := range headers.Values("Warning") {
		if warningValue == webdavTrashDeleteCleanupWarningHeader {
			return
		}
	}
	headers.Add("Warning", webdavTrashDeleteCleanupWarningHeader)
}

func markDeleteCleanupWarningHeader(w http.ResponseWriter) {
	if w == nil {
		return
	}
	headers := w.Header()
	for _, warningValue := range headers.Values("Warning") {
		if warningValue == webdavDeleteCleanupWarningHeader {
			return
		}
	}
	headers.Add("Warning", webdavDeleteCleanupWarningHeader)
}

func (h *Handler) rollbackCopiedDirectory(dstPath string, copyErr error) error {
	if rollbackErr := h.removeCopiedTree(context.Background(), dstPath); rollbackErr != nil && !errors.Is(rollbackErr, storage.ErrNotFound) {
		return errors.Join(copyErr, fmt.Errorf("rollback copied directory %s: %w", dstPath, rollbackErr))
	}
	return copyErr
}

func (h *Handler) removeCopiedTree(ctx context.Context, targetPath string) error {
	info, err := h.fs.Stat(ctx, targetPath)
	if err != nil {
		return err
	}

	if info.IsDir {
		children, err := h.fs.ReadDir(ctx, targetPath)
		if err != nil {
			return err
		}
		for _, child := range children {
			if err := h.removeCopiedTree(ctx, child.Path); err != nil {
				return err
			}
		}
	}

	return h.fs.PermanentDelete(ctx, targetPath)
}

func (h *Handler) handleMove(ctx context.Context, w http.ResponseWriter, r *http.Request, srcPath string) {
	dst := h.getDestination(r)
	if dst == "" {
		http.Error(w, "missing Destination header", http.StatusBadRequest)
		return
	}
	if srcPath == dst {
		http.Error(w, "source and destination must differ", http.StatusForbidden)
		return
	}
	if !authorizeScopePath(w, scopeFromContext(ctx), dst, accessWrite) {
		return
	}

	releaseLocks := h.acquireHierarchyLocks(
		hierarchyLockSpec{path: srcPath, write: true},
		hierarchyLockSpec{path: dst, write: true},
	)
	defer releaseLocks()

	if !h.authorizeWriteLockWithScope(w, r, srcPath, []string{srcPath, dst}, namespacePathsForResources(srcPath, dst), []string{srcPath, dst}) {
		return
	}

	srcInfo, err := h.fs.Stat(ctx, srcPath)
	if err != nil {
		if h.writeParentNotDirectoryConflict(w, err) {
			return
		}
		h.handleError(w, err)
		return
	}
	if srcInfo.IsDir {
		if err := h.validateInfinityOnlyDepth(r.Header.Get("Depth")); err != nil {
			writeKnownWebDAVError(w, errInvalidDepthHeader, http.StatusBadRequest)
			return
		}
	}
	srcETag := fmt.Sprintf(`"%s"`, srcInfo.ContentHash)
	if h.writeFailedMutationPrecondition(w, r, srcETag, srcInfo.ModTime) {
		return
	}

	dstExists := h.destinationExists(ctx, dst)
	if dstExists {
		dstInfo, err := h.fs.Stat(ctx, dst)
		if err != nil {
			if h.writeParentNotDirectoryConflict(w, err) {
				return
			}
			h.handleError(w, err)
			return
		}
		if srcInfo.IsDir != dstInfo.IsDir {
			http.Error(w, "resource type conflict", http.StatusConflict)
			return
		}
	}

	if err := h.checkOverwriteHeader(ctx, r, dst); err != nil {
		if h.writeExpectedWebDAVError(w, err, http.StatusPreconditionFailed, errInvalidOverwriteHeader, errOverwriteDisabled) {
			return
		}
		if h.writeParentNotDirectoryConflict(w, err) {
			return
		}
		h.handleError(w, err)
		return
	}

	if err := h.rejectDirectoryDescendantDestination(ctx, srcPath, dst); err != nil {
		if h.writeExpectedWebDAVError(w, err, http.StatusConflict, errDestinationInsideSourceDirectory) {
			return
		}
		if h.writeParentNotDirectoryConflict(w, err) {
			return
		}
		h.handleError(w, err)
		return
	}
	if err := h.authorizeMoveTreeAccess(ctx, srcPath, dst, dstExists); err != nil {
		h.handleError(w, err)
		return
	}

	h.quotaMu.Lock()
	defer h.quotaMu.Unlock()
	if err := h.ensureMoveQuota(ctx, srcPath, dst, dstExists); err != nil {
		h.handleError(w, err)
		return
	}

	affectedPaths := []string{path.Dir(srcPath), srcPath, path.Dir(dst), dst}
	h.invalidatePropCache(affectedPaths...)

	if dstExists {
		backupPath, err := h.allocateMoveBackupPath(ctx, dst)
		if err != nil {
			h.handleError(w, err)
			return
		}
		if err := h.fs.Rename(ctx, dst, backupPath); err != nil {
			if h.writeParentNotDirectoryConflict(w, err) {
				return
			}
			h.handleError(w, err)
			return
		}

		if err := h.fs.Rename(ctx, srcPath, dst); err != nil {
			if restoreErr := h.fs.Rename(ctx, backupPath, dst); restoreErr != nil {
				h.handleError(w, errors.Join(err, fmt.Errorf("failed to restore overwritten destination: %w", restoreErr)))
				return
			}
			if h.writeParentNotDirectoryConflict(w, err) {
				return
			}
			h.handleError(w, err)
			return
		}

		if err := h.fs.PermanentDelete(ctx, backupPath); err != nil {
			// The namespace move is already committed at this point. Cleanup failure
			// should leave behind maintenance work, not turn a successful MOVE into
			// a false failure for clients.
			if !markDeleteWarningHeaders(w, err) {
				h.handleError(w, err)
				return
			}
		}
	} else {
		if err := h.fs.Rename(ctx, srcPath, dst); err != nil {
			if h.writeParentNotDirectoryConflict(w, err) {
				return
			}
			h.handleError(w, err)
			return
		}
	}
	h.moveLocksUnderPath(srcPath, dst)

	h.invalidatePropCache(affectedPaths...)

	if dstExists {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) writeFailedMutationPrecondition(w http.ResponseWriter, r *http.Request, etag string, modTime time.Time) bool {
	if im := r.Header.Get("If-Match"); im != "" {
		if !h.matchETag(im, etag) {
			http.Error(w, errPreconditionFailed.Error(), http.StatusPreconditionFailed)
			return true
		}
	}
	if ius := r.Header.Get("If-Unmodified-Since"); ius != "" {
		if unmodifiedSince, err := http.ParseTime(ius); err == nil && isHTTPTimeAfter(modTime, unmodifiedSince) {
			http.Error(w, errPreconditionFailed.Error(), http.StatusPreconditionFailed)
			return true
		}
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		if h.matchETag(inm, etag) {
			http.Error(w, errPreconditionFailed.Error(), http.StatusPreconditionFailed)
			return true
		}
	}
	return false
}

func (h *Handler) allocateMoveBackupPath(ctx context.Context, dst string) (string, error) {
	dir := path.Dir(dst)
	base := path.Base(dst)
	for attempt := 0; attempt < 16; attempt++ {
		candidate := path.Join(dir, fmt.Sprintf(".%s.webdav-move-backup-%d", base, time.Now().UnixNano()+int64(attempt)))
		if _, err := h.fs.Stat(ctx, candidate); errors.Is(err, storage.ErrNotFound) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", storage.ErrAlreadyExists
}

func (h *Handler) checkOverwriteHeader(ctx context.Context, r *http.Request, dst string) error {
	overwrite := r.Header.Get("Overwrite")
	if overwrite == "" || strings.EqualFold(overwrite, "T") {
		return nil
	}
	if !strings.EqualFold(overwrite, "F") {
		return errInvalidOverwriteHeader
	}

	if _, err := h.fs.Stat(ctx, dst); err == nil {
		return errOverwriteDisabled
	} else if !errors.Is(err, storage.ErrNotFound) {
		return err
	}

	return nil
}

func (h *Handler) parseCopyDepth(depth string) (string, error) {
	if depth == "" {
		return "infinity", nil
	}

	switch strings.ToLower(strings.TrimSpace(depth)) {
	case "0", "infinity":
		return strings.ToLower(strings.TrimSpace(depth)), nil
	default:
		return "", errInvalidDepthHeader
	}
}

func (h *Handler) validateInfinityOnlyDepth(depth string) error {
	if depth == "" || strings.EqualFold(strings.TrimSpace(depth), webdavLockDepthInfinity) {
		return nil
	}
	return errInvalidDepthHeader
}

func (h *Handler) parseLockDepth(depth string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(depth)) {
	case "":
		return webdavLockDepthInfinity, nil
	case webdavLockDepthZero:
		return webdavLockDepthZero, nil
	case webdavLockDepthInfinity:
		return webdavLockDepthInfinity, nil
	default:
		return "", errInvalidDepthHeader
	}
}

func (h *Handler) rejectDirectoryDescendantDestination(ctx context.Context, srcPath, dstPath string) error {
	info, err := h.fs.Stat(ctx, srcPath)
	if err != nil {
		return err
	}
	if !info.IsDir {
		return nil
	}
	if isDescendantPath(srcPath, dstPath) {
		return errDestinationInsideSourceDirectory
	}
	return nil
}

func (h *Handler) rejectDirectoryCopyOverwrite(ctx context.Context, srcPath, dstPath string, dstExists bool) error {
	if !dstExists {
		return nil
	}

	info, err := h.fs.Stat(ctx, srcPath)
	if err != nil {
		return err
	}
	if info.IsDir {
		return errDirectoryCopyOverwriteNotSupported
	}
	return nil
}

func (h *Handler) authorizeTreeAccess(ctx context.Context, rootPath string, mode accessMode) error {
	scope := scopeFromContext(ctx)
	if !scope.scoped {
		return nil
	}
	if !scope.canAccess(rootPath, mode) {
		return errWebDAVPathAccessDenied
	}
	if !scope.hasAccessRules() {
		return nil
	}

	info, err := h.fs.Stat(ctx, rootPath)
	if err != nil {
		return err
	}
	if !info.IsDir {
		return nil
	}

	children, err := h.fs.ReadDir(ctx, rootPath)
	if err != nil {
		return err
	}
	for _, child := range children {
		if child == nil {
			continue
		}
		if err := h.authorizeTreeAccess(ctx, child.Path, mode); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) authorizeMappedTreeAccess(ctx context.Context, sourceRoot, destinationRoot string, mode accessMode) error {
	scope := scopeFromContext(ctx)
	if !scope.scoped {
		return nil
	}
	if !scope.canAccess(destinationRoot, mode) {
		return errWebDAVPathAccessDenied
	}
	if !scope.hasAccessRules() {
		return nil
	}

	info, err := h.fs.Stat(ctx, sourceRoot)
	if err != nil {
		return err
	}
	if !info.IsDir {
		return nil
	}

	children, err := h.fs.ReadDir(ctx, sourceRoot)
	if err != nil {
		return err
	}
	for _, child := range children {
		if child == nil {
			continue
		}
		childDestination := mapWebDAVDescendantPath(sourceRoot, destinationRoot, child.Path)
		if !scope.canAccess(childDestination, mode) {
			return errWebDAVPathAccessDenied
		}
		if child.IsDir {
			if err := h.authorizeMappedTreeAccess(ctx, child.Path, childDestination, mode); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *Handler) authorizeCopyDestinationTreeAccess(ctx context.Context, sourceRoot, destinationRoot string) error {
	scope := scopeFromContext(ctx)
	if !scope.scoped {
		return nil
	}
	if !scope.canAccess(destinationRoot, accessWrite) {
		return errWebDAVPathAccessDenied
	}
	if !scope.hasAccessRules() {
		return nil
	}

	info, err := h.fs.Stat(ctx, sourceRoot)
	if err != nil {
		return err
	}
	if !info.IsDir {
		return nil
	}

	children, err := h.fs.ReadDir(ctx, sourceRoot)
	if err != nil {
		return err
	}
	for _, child := range children {
		if child == nil || !scope.canAccess(child.Path, accessRead) {
			continue
		}
		childDestination := mapWebDAVDescendantPath(sourceRoot, destinationRoot, child.Path)
		if !scope.canAccess(childDestination, accessWrite) {
			return errWebDAVPathAccessDenied
		}
		if child.IsDir {
			if err := h.authorizeCopyDestinationTreeAccess(ctx, child.Path, childDestination); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *Handler) authorizeMoveTreeAccess(ctx context.Context, sourcePath, destinationPath string, destinationExists bool) error {
	if err := h.authorizeTreeAccess(ctx, sourcePath, accessWrite); err != nil {
		return err
	}
	if err := h.authorizeMappedTreeAccess(ctx, sourcePath, destinationPath, accessWrite); err != nil {
		return err
	}
	if destinationExists {
		if err := h.authorizeTreeAccess(ctx, destinationPath, accessWrite); err != nil {
			return err
		}
	}
	return nil
}

func mapWebDAVDescendantPath(sourceRoot, destinationRoot, currentPath string) string {
	sourceRoot = path.Clean(sourceRoot)
	destinationRoot = path.Clean(destinationRoot)
	currentPath = path.Clean(currentPath)

	relativePath := ""
	if sourceRoot == "/" {
		relativePath = strings.TrimPrefix(currentPath, "/")
	} else {
		relativePath = strings.TrimPrefix(currentPath, sourceRoot)
		relativePath = strings.TrimPrefix(relativePath, "/")
	}
	if relativePath == "" {
		return destinationRoot
	}
	return path.Clean(path.Join(destinationRoot, relativePath))
}

func isDescendantPath(parentPath, childPath string) bool {
	if parentPath == "/" {
		return childPath != "/" && strings.HasPrefix(childPath, "/")
	}
	return strings.HasPrefix(childPath, parentPath+"/")
}

func (h *Handler) destinationExists(ctx context.Context, dst string) bool {
	_, err := h.fs.Stat(ctx, dst)
	return err == nil
}

func writeKnownWebDAVError(w http.ResponseWriter, known error, status int) {
	http.Error(w, known.Error(), status)
}

func (h *Handler) writeParentNotDirectoryConflict(w http.ResponseWriter, err error) bool {
	if errors.Is(err, storage.ErrNotDir) {
		http.Error(w, "parent path is not a directory", http.StatusConflict)
		return true
	}
	return false
}

func (h *Handler) writeExpectedWebDAVError(w http.ResponseWriter, err error, status int, expected ...error) bool {
	for _, candidate := range expected {
		if errors.Is(err, candidate) {
			writeKnownWebDAVError(w, candidate, status)
			return true
		}
	}

	return false
}

func (h *Handler) handlePropfind(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	depth, err := h.parsePropfindDepth(r.Header.Get("Depth"))
	if err != nil {
		writeKnownWebDAVError(w, errInvalidDepthHeader, http.StatusBadRequest)
		return
	}

	cacheable := !scopeFromContext(ctx).scoped
	// Check cache first
	if cacheable {
		if responses, ok := h.propCache.Get(filePath, depth); ok {
			h.writePropfindResponse(w, responses)
			return
		}
	}
	generation := h.propCache.SnapshotGeneration()

	info, err := h.fs.Stat(ctx, filePath)
	if err != nil {
		h.handleError(w, err)
		return
	}

	var responses []propfindResponse

	// Add current resource
	responses = append(responses, h.propResponse(ctx, filePath, info))

	if err := h.appendPropfindChildren(ctx, filePath, info, depth, 0, &responses); err != nil {
		if errors.Is(err, errPropfindDepthLimitExceeded) {
			writeKnownWebDAVError(w, errPropfindDepthLimitExceeded, http.StatusForbidden)
			return
		}
		h.handleError(w, err)
		return
	}

	// Cache the result for large directories
	if cacheable && len(responses) > 10 {
		h.propCache.SetIfUnchanged(filePath, depth, responses, generation)
	}

	h.writePropfindResponse(w, responses)
}

func (h *Handler) appendPropfindChildren(ctx context.Context, filePath string, info *storage.FileInfo, depth string, currentDepth int, responses *[]propfindResponse) error {
	if !info.IsDir || depth == "0" {
		return nil
	}
	if depth == "infinity" && currentDepth >= maxPropfindTraversalDepth {
		return errPropfindDepthLimitExceeded
	}

	children, err := h.fs.ReadDir(ctx, filePath)
	if err != nil {
		return err
	}

	for _, child := range children {
		if !scopeFromContext(ctx).canAccess(child.Path, accessRead) {
			continue
		}
		*responses = append(*responses, h.propResponse(ctx, child.Path, child))
		if depth == "infinity" {
			if err := h.appendPropfindChildren(ctx, child.Path, child, depth, currentDepth+1, responses); err != nil {
				return err
			}
		}
	}

	return nil
}

func (h *Handler) parsePropfindDepth(depth string) (string, error) {
	if depth == "" {
		return "infinity", nil
	}

	normalized := strings.ToLower(strings.TrimSpace(depth))
	switch normalized {
	case "0", "1", "infinity":
		return normalized, nil
	default:
		return "", errInvalidDepthHeader
	}
}

func (h *Handler) writePropfindResponse(w http.ResponseWriter, responses []propfindResponse) {
	ms := multistatus{
		Responses: responses,
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)

	fmt.Fprint(w, xml.Header)
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	enc.Encode(ms)
}

func (h *Handler) propResponse(ctx context.Context, filePath string, info *storage.FileInfo) propfindResponse {
	href := h.webdavHref(ctx, filePath, info.IsDir)
	clientPath, ok := scopeFromContext(ctx).clientPath(filePath)
	if !ok {
		clientPath = filePath
	}
	displayName := path.Base(clientPath)
	if clientPath == "/" {
		displayName = "/"
	}

	props := propstat{
		Status: "HTTP/1.1 200 OK",
		Prop: prop{
			DisplayName:     displayName,
			GetLastModified: info.ModTime.UTC().Format(http.TimeFormat),
		},
	}

	if info.IsDir {
		props.Prop.ResourceType = &resourceType{Collection: &struct{}{}}
	} else {
		props.Prop.GetContentLength = info.Size
		props.Prop.GetETag = fmt.Sprintf(`"%s"`, info.ContentHash)
	}

	return propfindResponse{
		Href:     href,
		Propstat: props,
	}
}

func (h *Handler) handleProppatch(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	releaseLocks := h.acquireHierarchyLocks(hierarchyLockSpec{path: filePath, write: true})
	defer releaseLocks()
	if !h.authorizeWriteLock(w, r, filePath, filePath) {
		return
	}

	info, err := h.fs.Stat(ctx, filePath)
	if err != nil {
		h.handleError(w, err)
		return
	}

	requestedProperties, err := parseProppatchProperties(http.MaxBytesReader(w, r.Body, maxWebDAVXMLRequestBody))
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "PROPPATCH request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		writeKnownWebDAVError(w, errInvalidProppatchBody, http.StatusBadRequest)
		return
	}
	if len(requestedProperties) == 0 {
		h.writeProppatchNoOpResponse(ctx, w, filePath, info.IsDir)
		return
	}

	h.writeProppatchUnsupportedResponse(ctx, w, filePath, info.IsDir, requestedProperties)
}

func parseProppatchProperties(body io.Reader) ([]webdavPropertyElement, error) {
	decoder := xml.NewDecoder(body)
	var requestedProperties []webdavPropertyElement
	seenRoot := false
	inProp := false
	propDepth := 0

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch token := token.(type) {
		case xml.StartElement:
			if !seenRoot {
				if token.Name.Local != "propertyupdate" {
					return nil, errInvalidProppatchBody
				}
				seenRoot = true
				continue
			}

			if token.Name.Local == "prop" {
				inProp = true
				propDepth = 0
				continue
			}

			if inProp {
				if propDepth == 0 {
					requestedProperties = append(requestedProperties, webdavPropertyElement{XMLName: token.Name})
				}
				propDepth++
			}
		case xml.EndElement:
			if !inProp {
				continue
			}
			if token.Name.Local == "prop" && propDepth == 0 {
				inProp = false
				continue
			}
			if propDepth > 0 {
				propDepth--
			}
		}
	}

	if !seenRoot {
		return nil, errInvalidProppatchBody
	}

	return requestedProperties, nil
}

func (h *Handler) writeProppatchNoOpResponse(ctx context.Context, w http.ResponseWriter, filePath string, isDir bool) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	fmt.Fprint(w, xml.Header)
	fmt.Fprintf(w, `<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>%s</D:href>
    <D:propstat>
      <D:status>HTTP/1.1 200 OK</D:status>
    </D:propstat>
  </D:response>
</D:multistatus>`, h.webdavHref(ctx, filePath, isDir))
}

func (h *Handler) writeProppatchUnsupportedResponse(ctx context.Context, w http.ResponseWriter, filePath string, isDir bool, requestedProperties []webdavPropertyElement) {
	namespacePrefixes, orderedNamespaces := buildProppatchNamespacePrefixes(requestedProperties)
	var response strings.Builder
	response.WriteString(`<D:multistatus xmlns:D="DAV:"`)
	for _, namespace := range orderedNamespaces {
		response.WriteString(` xmlns:`)
		response.WriteString(namespacePrefixes[namespace])
		response.WriteString(`="`)
		_ = xml.EscapeText(&response, []byte(namespace))
		response.WriteString(`"`)
	}
	response.WriteString(`>`)
	response.WriteString(`<D:response><D:href>`)
	_ = xml.EscapeText(&response, []byte(h.webdavHref(ctx, filePath, isDir)))
	response.WriteString(`</D:href><D:propstat><D:prop>`)
	for _, property := range requestedProperties {
		writeProppatchPropertyElement(&response, namespacePrefixes, property)
	}
	response.WriteString(`</D:prop><D:status>HTTP/1.1 403 Forbidden</D:status><D:error><D:cannot-modify-protected-property/></D:error><D:responsedescription>`)
	_ = xml.EscapeText(&response, []byte("property updates are not supported"))
	response.WriteString(`</D:responsedescription></D:propstat></D:response></D:multistatus>`)

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	fmt.Fprint(w, xml.Header)
	fmt.Fprint(w, response.String())
}

func buildProppatchNamespacePrefixes(requestedProperties []webdavPropertyElement) (map[string]string, []string) {
	namespacePrefixes := map[string]string{"DAV:": "D"}
	orderedNamespaces := make([]string, 0)
	for _, property := range requestedProperties {
		namespace := property.XMLName.Space
		if namespace == "" || namespace == "DAV:" {
			continue
		}
		if _, exists := namespacePrefixes[namespace]; exists {
			continue
		}
		namespacePrefixes[namespace] = fmt.Sprintf("P%d", len(orderedNamespaces)+1)
		orderedNamespaces = append(orderedNamespaces, namespace)
	}
	return namespacePrefixes, orderedNamespaces
}

func writeProppatchPropertyElement(response *strings.Builder, namespacePrefixes map[string]string, property webdavPropertyElement) {
	response.WriteByte('<')
	if prefix, ok := namespacePrefixes[property.XMLName.Space]; ok && prefix != "" {
		response.WriteString(prefix)
		response.WriteByte(':')
	}
	response.WriteString(property.XMLName.Local)
	response.WriteString("/>")
}

func (h *Handler) handleLock(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	info, err := h.fs.Stat(ctx, filePath)
	if err != nil {
		h.handleError(w, err)
		return
	}
	hasBody, err := requestHasBody(r)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	providedTokens := extractLockTokens(r, filePath, h.prefix, scopePathMapper(ctx))
	hasRefreshCondition := len(providedTokens) > 0 || r.Header.Get("If") != "" || r.Header.Get("Lock-Token") != ""

	h.startLockCleanupLoop()
	now := time.Now()

	h.locksMu.Lock()
	h.removeExpiredLocksLocked(now)
	if !hasBody {
		if !hasRefreshCondition {
			h.locksMu.Unlock()
			writeKnownWebDAVError(w, errLockRefreshRequiresToken, http.StatusBadRequest)
			return
		}

		lockPath, existing, exists := h.findRefreshLockLocked(filePath, providedTokens)
		if !exists {
			h.locksMu.Unlock()
			writeKnownWebDAVError(w, errLockTokenMatchesRequestURI, http.StatusPreconditionFailed)
			return
		}

		existing.expiresAt = now.Add(webdavLockTimeout)
		h.locks[lockPath] = existing
		h.locksMu.Unlock()
		h.writeLockResponse(w, existing.token, existing.depth, false)
		return
	}

	depth, err := h.parseLockDepth(r.Header.Get("Depth"))
	if err != nil {
		h.locksMu.Unlock()
		writeKnownWebDAVError(w, errInvalidDepthHeader, http.StatusBadRequest)
		return
	}
	if h.lockConflictsWithRequestLocked(filePath, info.IsDir, depth) {
		h.locksMu.Unlock()
		http.Error(w, "resource is locked", http.StatusLocked)
		return
	}

	token, err := h.generateUniqueLockTokenLocked()
	if err != nil {
		h.locksMu.Unlock()
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.locks[filePath] = webdavLock{
		token:     token,
		depth:     depth,
		expiresAt: now.Add(webdavLockTimeout),
	}
	h.locksMu.Unlock()

	h.writeLockResponse(w, token, depth, true)
}

func (h *Handler) writeLockResponse(w http.ResponseWriter, token, depth string, includeLockToken bool) {
	if includeLockToken {
		w.Header().Set("Lock-Token", "<"+token+">")
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	fmt.Fprint(w, xml.Header)
	fmt.Fprintf(w, `<D:prop xmlns:D="DAV:">
  <D:lockdiscovery>
    <D:activelock>
      <D:locktype><D:write/></D:locktype>
      <D:lockscope><D:exclusive/></D:lockscope>
      <D:depth>%s</D:depth>
      <D:timeout>Second-3600</D:timeout>
      <D:locktoken><D:href>%s</D:href></D:locktoken>
    </D:activelock>
  </D:lockdiscovery>
</D:prop>`, depth, token)
}

func (h *Handler) handleUnlock(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	if _, err := h.fs.Stat(ctx, filePath); err != nil {
		h.handleError(w, err)
		return
	}

	lockToken := normalizeLockToken(r.Header.Get("Lock-Token"))
	if lockToken == "" {
		http.Error(w, "missing lock token", http.StatusBadRequest)
		return
	}

	h.locksMu.Lock()
	h.removeExpiredLocksLocked(time.Now())
	lockInfo, exists := h.locks[filePath]
	if !exists {
		h.locksMu.Unlock()
		http.Error(w, "resource is not locked", http.StatusConflict)
		return
	}
	if lockInfo.token != lockToken {
		h.locksMu.Unlock()
		http.Error(w, "lock token does not match", http.StatusConflict)
		return
	}
	delete(h.locks, filePath)
	h.locksMu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func normalizeLockToken(token string) string {
	token = strings.TrimSpace(token)
	token = strings.TrimPrefix(token, "<")
	token = strings.TrimSuffix(token, ">")
	return token
}

type providedLockToken struct {
	token  string
	path   string
	tagged bool
}

func scopePathMapper(ctx context.Context) func(string) (string, bool) {
	scope := scopeFromContext(ctx)
	if !scope.scoped {
		return nil
	}
	return scope.storagePath
}

func extractLockTokens(r *http.Request, requestPath, prefix string, mapPath func(string) (string, bool)) []providedLockToken {
	seen := make(map[string]struct{})
	tokens := make([]providedLockToken, 0, 2)

	appendToken := func(token, tokenPath string, tagged bool) {
		token = normalizeLockToken(token)
		if token == "" || tokenPath == "" {
			return
		}
		key := tokenPath + "\x00" + strconv.FormatBool(tagged) + "\x00" + token
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		tokens = append(tokens, providedLockToken{token: token, path: tokenPath, tagged: tagged})
	}

	appendToken(r.Header.Get("Lock-Token"), requestPath, false)

	ifHeader := r.Header.Get("If")
	currentPath := requestPath
	currentTagged := false
	for {
		ifHeader = strings.TrimSpace(ifHeader)
		if ifHeader == "" {
			break
		}

		switch ifHeader[0] {
		case '<':
			uriEnd := strings.IndexByte(ifHeader, '>')
			if uriEnd == -1 {
				return tokens
			}
			resolvedPath, ok := resolveIfHeaderPath(ifHeader[1:uriEnd], requestHost(r), prefix)
			if ok && mapPath != nil {
				resolvedPath, ok = mapPath(resolvedPath)
			}
			if ok {
				currentPath = resolvedPath
			} else {
				currentPath = ""
			}
			currentTagged = true
			ifHeader = ifHeader[uriEnd+1:]
		case '(':
			stateEnd := strings.IndexByte(ifHeader, ')')
			if stateEnd == -1 {
				return tokens
			}
			extractStateListLockTokens(ifHeader[1:stateEnd], currentPath, currentTagged, appendToken)
			ifHeader = ifHeader[stateEnd+1:]
		default:
			ifHeader = ifHeader[1:]
		}
	}

	return tokens
}

func extractStateListLockTokens(stateList, tokenPath string, tagged bool, appendToken func(token, tokenPath string, tagged bool)) {
	for {
		stateList = strings.TrimSpace(stateList)
		if stateList == "" {
			return
		}

		negated := false
		if len(stateList) >= 3 && strings.EqualFold(stateList[:3], "Not") {
			remainder := strings.TrimSpace(stateList[3:])
			if remainder != stateList[3:] || len(stateList) == 3 {
				negated = true
				stateList = remainder
			}
		}
		if stateList == "" {
			return
		}

		switch stateList[0] {
		case '<':
			tokenEnd := strings.IndexByte(stateList, '>')
			if tokenEnd == -1 {
				return
			}
			if !negated {
				appendToken(stateList[1:tokenEnd], tokenPath, tagged)
			}
			stateList = stateList[tokenEnd+1:]
		case '[':
			etagEnd := strings.IndexByte(stateList, ']')
			if etagEnd == -1 {
				return
			}
			stateList = stateList[etagEnd+1:]
		default:
			stateList = stateList[1:]
		}
	}
}

func resolveIfHeaderPath(rawPath, expectedHost, prefix string) (string, bool) {
	u, err := url.Parse(rawPath)
	if err != nil {
		return "", false
	}
	if u.Host != "" && !strings.EqualFold(u.Host, expectedHost) {
		return "", false
	}
	decodedPath, err := url.PathUnescape(u.EscapedPath())
	if err != nil || hasTraversalSegment(decodedPath) {
		return "", false
	}
	resolvedPath := path.Clean(decodedPath)
	if !path.IsAbs(resolvedPath) {
		resolvedPath = "/" + resolvedPath
	}
	resolvedPath, ok := trimWebDAVPrefix(resolvedPath, prefix)
	if !ok {
		return "", false
	}
	return resolvedPath, true
}

func hasMatchingLockToken(providedTokens []providedLockToken, expected, lockPath string, lockInfo webdavLock) bool {
	for _, token := range providedTokens {
		if token.token != expected {
			continue
		}
		if token.tagged {
			if taggedLockScopeContainsPath(lockPath, lockInfo, token.path) {
				return true
			}
			continue
		}
		if lockScopeContainsPath(lockPath, lockInfo, token.path) {
			return true
		}
	}
	return false
}

func taggedLockScopeContainsPath(lockPath string, lockInfo webdavLock, candidatePath string) bool {
	if candidatePath == lockPath {
		return true
	}
	if lockInfo.depth == webdavLockDepthInfinity {
		return isDescendantPath(lockPath, candidatePath)
	}
	return path.Dir(candidatePath) == lockPath
}

func lockScopeContainsPath(lockPath string, lockInfo webdavLock, candidatePath string) bool {
	if candidatePath == lockPath {
		return true
	}
	if pathMatchesOrDescendant(candidatePath, lockPath) {
		return true
	}
	if lockInfo.depth == webdavLockDepthInfinity {
		return isDescendantPath(lockPath, candidatePath)
	}
	return path.Dir(candidatePath) == lockPath
}

func (h *Handler) authorizeWriteLock(w http.ResponseWriter, r *http.Request, requestPath string, paths ...string) bool {
	return h.authorizeWriteLockWithScope(w, r, requestPath, paths, nil, nil)
}

func (h *Handler) authorizeWriteLockWithScope(w http.ResponseWriter, r *http.Request, requestPath string, resourcePaths, namespacePaths, descendantRoots []string) bool {
	providedTokens := extractLockTokens(r, requestPath, h.prefix, scopePathMapper(r.Context()))

	h.locksMu.Lock()
	defer h.locksMu.Unlock()
	h.removeExpiredLocksLocked(time.Now())

	for lockPath, lockInfo := range h.locks {
		if !lockAffectsRequest(lockPath, lockInfo, resourcePaths, namespacePaths, descendantRoots) {
			continue
		}
		if !hasMatchingLockToken(providedTokens, lockInfo.token, lockPath, lockInfo) {
			http.Error(w, "resource is locked", http.StatusLocked)
			return false
		}
	}

	return true
}

func lockAffectsRequest(lockPath string, lockInfo webdavLock, resourcePaths, namespacePaths, descendantRoots []string) bool {
	for _, resourcePath := range resourcePaths {
		if lockCoversResourcePath(lockPath, lockInfo, resourcePath) {
			return true
		}
	}
	for _, namespacePath := range namespacePaths {
		if lockCoversNamespacePath(lockPath, lockInfo, namespacePath) {
			return true
		}
	}
	for _, root := range descendantRoots {
		if pathMatchesOrDescendant(root, lockPath) {
			return true
		}
	}
	return false
}

func lockCoversResourcePath(lockPath string, lockInfo webdavLock, targetPath string) bool {
	if lockPath == targetPath {
		return true
	}
	return lockInfo.depth == webdavLockDepthInfinity && isDescendantPath(lockPath, targetPath)
}

func lockCoversNamespacePath(lockPath string, lockInfo webdavLock, namespacePath string) bool {
	if lockPath == namespacePath {
		return true
	}
	return lockInfo.depth == webdavLockDepthInfinity && isDescendantPath(lockPath, namespacePath)
}

func namespacePathsForResources(paths ...string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(paths))

	for _, filePath := range paths {
		if filePath == "/" {
			continue
		}
		parent := path.Dir(filePath)
		if _, exists := seen[parent]; exists {
			continue
		}
		seen[parent] = struct{}{}
		result = append(result, parent)
	}

	return result
}

type hierarchyLockSpec struct {
	path  string
	write bool
}

func (h *Handler) acquireHierarchyLocks(specs ...hierarchyLockSpec) func() {
	paths := make(map[string]bool)
	for _, spec := range specs {
		for _, lockPath := range lockHierarchy(spec.path) {
			if currentWrite, exists := paths[lockPath]; exists && currentWrite {
				continue
			}
			paths[lockPath] = spec.write
		}
	}

	ordered := make([]string, 0, len(paths))
	for lockPath := range paths {
		ordered = append(ordered, lockPath)
	}
	slices.Sort(ordered)

	for _, lockPath := range ordered {
		if paths[lockPath] {
			h.pathLock.Lock(lockPath)
			continue
		}
		h.pathLock.RLock(lockPath)
	}

	return func() {
		for i := len(ordered) - 1; i >= 0; i-- {
			lockPath := ordered[i]
			if paths[lockPath] {
				h.pathLock.Unlock(lockPath)
				continue
			}
			h.pathLock.RUnlock(lockPath)
		}
	}
}

func lockHierarchy(filePath string) []string {
	cleanPath := path.Clean(filePath)
	if cleanPath == "/" {
		return []string{"/"}
	}

	segments := strings.Split(strings.TrimPrefix(cleanPath, "/"), "/")
	current := ""
	hierarchy := make([]string, 0, len(segments)+1)
	hierarchy = append(hierarchy, "/")
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		current += "/" + segment
		hierarchy = append(hierarchy, current)
	}

	return hierarchy
}

func (h *Handler) findRefreshLockLocked(filePath string, providedTokens []providedLockToken) (string, webdavLock, bool) {
	if lockInfo, exists := h.locks[filePath]; exists && hasMatchingLockToken(providedTokens, lockInfo.token, filePath, lockInfo) {
		return filePath, lockInfo, true
	}

	current := path.Dir(filePath)
	for {
		lockInfo, exists := h.locks[current]
		if exists && hasMatchingLockToken(providedTokens, lockInfo.token, current, lockInfo) {
			if lockInfo.depth == webdavLockDepthInfinity {
				return current, lockInfo, true
			}
		}
		if current == "/" {
			break
		}
		current = path.Dir(current)
	}

	return "", webdavLock{}, false
}

func (h *Handler) lockConflictsWithRequestLocked(filePath string, isDir bool, depth string) bool {
	for lockPath, lockInfo := range h.locks {
		if lockCoversResourcePath(lockPath, lockInfo, filePath) {
			return true
		}
		if isDir && depth == webdavLockDepthInfinity && pathMatchesOrDescendant(filePath, lockPath) {
			return true
		}
	}

	return false
}

func pathMatchesOrDescendant(rootPath, candidatePath string) bool {
	return rootPath == candidatePath || isDescendantPath(rootPath, candidatePath)
}

func rebaseLockedPath(rootPath, newRootPath, lockPath string) string {
	if lockPath == rootPath {
		return newRootPath
	}
	relative := strings.TrimPrefix(lockPath, rootPath)
	return path.Clean(newRootPath + relative)
}

func (h *Handler) clearLocksUnderPath(rootPath string) {
	_ = h.clearLocksUnderPathWithSnapshot(rootPath)
}

func (h *Handler) clearLocksUnderPathWithSnapshot(rootPath string) map[string]webdavLock {
	h.locksMu.Lock()
	defer h.locksMu.Unlock()
	h.removeExpiredLocksLocked(time.Now())

	removed := make(map[string]webdavLock)
	for lockPath := range h.locks {
		if pathMatchesOrDescendant(rootPath, lockPath) {
			removed[lockPath] = h.locks[lockPath]
			delete(h.locks, lockPath)
		}
	}
	if len(removed) == 0 {
		return nil
	}
	return removed
}

func (h *Handler) restoreLocks(restored map[string]webdavLock) {
	if len(restored) == 0 {
		return
	}

	h.locksMu.Lock()
	defer h.locksMu.Unlock()
	h.removeExpiredLocksLocked(time.Now())

	now := time.Now()
	for lockPath, lockInfo := range restored {
		if !lockInfo.expiresAt.After(now) {
			continue
		}
		if _, exists := h.locks[lockPath]; exists {
			continue
		}
		h.locks[lockPath] = lockInfo
	}
}

func (h *Handler) moveLocksUnderPath(rootPath, newRootPath string) {
	h.locksMu.Lock()
	defer h.locksMu.Unlock()
	h.removeExpiredLocksLocked(time.Now())

	moved := make(map[string]webdavLock)
	for lockPath, lockInfo := range h.locks {
		if pathMatchesOrDescendant(newRootPath, lockPath) {
			delete(h.locks, lockPath)
			continue
		}
		if !pathMatchesOrDescendant(rootPath, lockPath) {
			continue
		}

		delete(h.locks, lockPath)
		moved[rebaseLockedPath(rootPath, newRootPath, lockPath)] = lockInfo
	}

	for lockPath, lockInfo := range moved {
		h.locks[lockPath] = lockInfo
	}
}

func (h *Handler) removeExpiredLocksLocked(now time.Time) {
	for filePath, lockInfo := range h.locks {
		if !lockInfo.expiresAt.After(now) {
			delete(h.locks, filePath)
		}
	}
}

func (h *Handler) generateUniqueLockTokenLocked() (string, error) {
	for attempt := 0; attempt < maxWebDAVLockTokenAttempts; attempt++ {
		token, err := h.newLockToken()
		if err != nil {
			return "", fmt.Errorf("generate lock token: %w", err)
		}
		if !h.lockTokenExistsLocked(token) {
			return token, nil
		}
	}

	return "", errors.New("generate lock token: exhausted unique token attempts")
}

func (h *Handler) lockTokenExistsLocked(expected string) bool {
	for _, lockInfo := range h.locks {
		if lockInfo.token == expected {
			return true
		}
	}
	return false
}

func generateOpaqueLockToken() (string, error) {
	b := make([]byte, webdavLockTokenBytes)
	if _, err := webdavRandomRead(b); err != nil {
		return "", err
	}
	return "opaquelocktoken:" + hex.EncodeToString(b), nil
}

func (h *Handler) getDestination(r *http.Request) string {
	dst := r.Header.Get("Destination")
	if dst == "" {
		return ""
	}

	u, err := url.Parse(dst)
	if err != nil {
		return ""
	}
	if u.Host != "" && !strings.EqualFold(u.Host, requestHost(r)) {
		return ""
	}
	decodedPath, err := url.PathUnescape(u.EscapedPath())
	if err != nil {
		return ""
	}
	if hasTraversalSegment(decodedPath) {
		return ""
	}

	// Get path and clean it
	dstPath := path.Clean(decodedPath)
	if !path.IsAbs(dstPath) {
		dstPath = "/" + dstPath
	}

	dstPath, ok := trimWebDAVPrefix(dstPath, h.prefix)
	if !ok {
		return ""
	}
	if mappedPath, ok := scopeFromContext(r.Context()).storagePath(dstPath); ok {
		return mappedPath
	}

	return ""
}

func trimWebDAVPrefix(cleanPath, prefix string) (string, bool) {
	if prefix == "" || prefix == "/" {
		return cleanPath, true
	}
	if cleanPath != prefix && !strings.HasPrefix(cleanPath, prefix+"/") {
		return "", false
	}

	trimmed := strings.TrimPrefix(cleanPath, prefix)
	if trimmed == "" {
		return "/", true
	}
	if !path.IsAbs(trimmed) {
		trimmed = "/" + trimmed
	}
	return trimmed, true
}

func requestHost(r *http.Request) string {
	if r.Host != "" {
		return r.Host
	}
	return r.URL.Host
}

func hasTraversalSegment(rawPath string) bool {
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	if strings.ContainsRune(normalized, '\x00') {
		return true
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return true
		}
	}

	return false
}

func (h *Handler) handleError(w http.ResponseWriter, err error) {
	if errors.Is(err, errWebDAVQuotaExceeded) {
		http.Error(w, "user quota exceeded", http.StatusInsufficientStorage)
		return
	}
	if errors.Is(err, errWebDAVPathAccessDenied) {
		http.Error(w, "path access denied", http.StatusForbidden)
		return
	}
	if errors.Is(err, storage.ErrNotFound) {
		http.Error(w, "resource not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, storage.ErrIsDir) || errors.Is(err, storage.ErrNotDir) {
		http.Error(w, "resource type conflict", http.StatusConflict)
		return
	}
	if errors.Is(err, storage.ErrDirNotEmpty) {
		http.Error(w, "directory not empty", http.StatusConflict)
		return
	}
	if errors.Is(err, storage.ErrAlreadyExists) {
		http.Error(w, "resource already exists", http.StatusConflict)
		return
	}
	if errors.Is(err, storage.ErrFileTooLarge) {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}
	if errors.Is(err, storage.ErrFileLocked) {
		http.Error(w, "resource is locked", http.StatusLocked)
		return
	}
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func isReadMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, "OPTIONS", "PROPFIND":
		return true
	}
	return false
}

// authenticate verifies authentication based on the configured auth type.
func (h *Handler) authenticate(r *http.Request) (*UserIdentity, bool) {
	switch h.authType {
	case "basic":
		user, pass, ok := r.BasicAuth()
		if !ok {
			return nil, false
		}
		// Constant-time comparison to prevent timing attacks
		userMatch := subtle.ConstantTimeCompare([]byte(user), []byte(h.username)) == 1
		passMatch := subtle.ConstantTimeCompare([]byte(pass), []byte(h.password)) == 1
		if !userMatch || !passMatch {
			return nil, false
		}
		return nil, true
	case webDAVAuthTypeUsers:
		if h.userAuthenticator == nil {
			return nil, false
		}
		user, pass, ok := r.BasicAuth()
		if !ok {
			return nil, false
		}
		identity, err := h.userAuthenticator(r.Context(), user, pass)
		if err != nil || identity == nil {
			return nil, false
		}
		return identity, true
	case "none", "":
		return nil, true
	default:
		// Unknown auth type, deny by default
		return nil, false
	}
}

func (h *Handler) scopeForIdentity(identity *UserIdentity) (requestScope, error) {
	if h.authType != webDAVAuthTypeUsers || identity == nil {
		return requestScope{}, nil
	}

	role := strings.ToLower(strings.TrimSpace(identity.Role))
	if role == webDAVRoleAdmin {
		return requestScope{
			username:   identity.Username,
			role:       role,
			groups:     append([]string(nil), identity.Groups...),
			quotaBytes: identity.QuotaBytes,
		}, nil
	}

	homeDir, ok := normalizeScopedHomeDir(identity.HomeDir)
	if !ok {
		return requestScope{}, errors.New("invalid user home directory")
	}
	return requestScope{
		username:    identity.Username,
		role:        role,
		groups:      append([]string(nil), identity.Groups...),
		homeDir:     homeDir,
		scoped:      true,
		readOnly:    role == webDAVRoleGuest,
		quotaBytes:  identity.QuotaBytes,
		accessRules: cloneWebDAVDirectoryAccessRules(h.directoryAccess),
	}, nil
}

// matchETag checks if the given ETag matches the condition value
// Supports multiple ETags separated by comma, and weak ETag prefix "W/"
func (h *Handler) matchETag(condition, etag string) bool {
	if condition == "*" {
		return true
	}

	// Handle multiple ETags
	for _, candidate := range strings.Split(condition, ",") {
		candidate = strings.TrimSpace(candidate)
		// Remove weak ETag prefix for comparison
		candidate = strings.TrimPrefix(candidate, "W/")
		etag = strings.TrimPrefix(etag, "W/")
		if candidate == etag {
			return true
		}
	}
	return false
}

// XML structure definitions

type multistatus struct {
	XMLName   xml.Name           `xml:"DAV: multistatus"`
	Responses []propfindResponse `xml:"response"`
}

type propfindResponse struct {
	Href     string   `xml:"href"`
	Propstat propstat `xml:"propstat"`
}

type propstat struct {
	Prop   prop   `xml:"prop"`
	Status string `xml:"status"`
}

type prop struct {
	DisplayName      string        `xml:"displayname,omitempty"`
	GetLastModified  string        `xml:"getlastmodified,omitempty"`
	GetContentLength int64         `xml:"getcontentlength,omitempty"`
	GetETag          string        `xml:"getetag,omitempty"`
	ResourceType     *resourceType `xml:"resourcetype,omitempty"`
}

type resourceType struct {
	Collection *struct{} `xml:"collection,omitempty"`
}

type webdavPropertyElement struct {
	XMLName xml.Name
	Inner   string `xml:",innerxml,omitempty"`
}
