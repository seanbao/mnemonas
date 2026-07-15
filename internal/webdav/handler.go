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
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/seanbao/mnemonas/internal/httpstream"
	quotareservation "github.com/seanbao/mnemonas/internal/quota"
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
	errWebDAVQuotaExceeded                = errors.New("quota exceeded")
	errWebDAVUserQuotaExceeded            = &webDAVQuotaExceededError{message: "user quota exceeded"}
	errWebDAVDirectoryQuotaExceeded       = &webDAVQuotaExceededError{message: "directory quota exceeded"}
	errWebDAVPathAccessDenied             = errors.New("path access denied")
)

type webDAVQuotaExceededError struct {
	message string
}

func (e *webDAVQuotaExceededError) Error() string {
	if e == nil {
		return errWebDAVQuotaExceeded.Error()
	}
	return e.message
}

func (e *webDAVQuotaExceededError) Is(target error) bool {
	return target == errWebDAVQuotaExceeded
}

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
const maxWebDAVWriteSize int64 = 10 * 1024 * 1024 * 1024
const webDAVQuotaGrowthChunk int64 = 64 * 1024 * 1024
const webDAVWriteRetryAfterSeconds = 1
const untrustedWebDAVContentSecurityPolicy = "sandbox; default-src 'none'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; img-src 'self' data: blob:; media-src 'self' data: blob:; style-src 'unsafe-inline'"
const webDAVAuthTypeUsers = "users"
const webDAVAllowedMethods = "OPTIONS, GET, HEAD, PUT, DELETE, MKCOL, COPY, MOVE, PROPFIND, PROPPATCH, LOCK, UNLOCK"
const webDAVReadOnlyAllowedMethods = "OPTIONS, GET, HEAD, PROPFIND"

const (
	webDAVRoleAdmin    = "admin"
	webDAVRoleUser     = "user"
	webDAVRoleGuest    = "guest"
	quotaTypeUser      = "user"
	quotaTypeDirectory = "directory"
)

var webdavRandomRead = rand.Read
var onWebDAVLockCleanupComplete = func() {}
var onWebDAVPropfindCacheMiss = func(*Handler, string, string) {}
var beforeWebDAVDeleteStorage = func(*Handler, string) {}
var webDAVHandlerLifecycleSequence atomic.Uint64

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

type webDAVDirectoryQuotaCheck struct {
	path          string
	quotaBytes    int64
	requiredBytes int64
}

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
	if !s.scoped {
		cleanPath := path.Clean(clientPath)
		if !path.IsAbs(cleanPath) {
			cleanPath = "/" + cleanPath
		}
		return cleanPath, true
	}

	cleanPath := cleanWebDAVRuntimeClientPath(clientPath)
	if cleanPath == "" {
		return "", false
	}

	if strings.TrimSpace(s.homeDir) == "" {
		return "", false
	}
	if s.hasAccessRules() {
		if _, ok := s.matchAccessRule(cleanPath); ok {
			return cleanPath, true
		}
		if s.hasReadableSharedRootPath(cleanPath) {
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
	if !s.scoped {
		cleanPath := path.Clean(storagePath)
		if !path.IsAbs(cleanPath) {
			cleanPath = "/" + cleanPath
		}
		return cleanPath, true
	}

	cleanPath := cleanWebDAVRuntimeTargetPath(storagePath)
	if cleanPath == "" {
		return "", false
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
		if s.hasReadableSharedRootPath(cleanPath) {
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
	targetPath = cleanWebDAVRuntimeTargetPath(targetPath)
	if targetPath == "" {
		return false
	}
	if rule, ok := s.matchAccessRule(targetPath); ok {
		return accessRuleAllowsScope(rule, s, mode)
	}
	return strings.TrimSpace(s.homeDir) != "" && pathMatchesOrDescendant(path.Clean(s.homeDir), targetPath)
}

func (s requestScope) hasReadableSharedRootPath(targetPath string) bool {
	if !s.scoped || !s.hasAccessRules() {
		return false
	}
	targetPath = cleanWebDAVRuntimeTargetPath(targetPath)
	if targetPath == "" {
		return false
	}
	if targetPath == "/" {
		return false
	}
	targetRoot := topLevelWebDAVPath(targetPath)
	if targetRoot == "" {
		return false
	}

	for _, rule := range s.accessRules {
		rulePath := cleanWebDAVAccessRulePath(rule.Path)
		if rulePath == "" || rulePath == "/" || topLevelWebDAVPath(rulePath) != targetRoot {
			continue
		}
		if accessRuleAllowsScope(rule, s, accessRead) {
			return true
		}
	}
	return false
}

func (s requestScope) readableDescendantRulePaths(targetPath string) []string {
	if !s.scoped || !s.hasAccessRules() {
		return nil
	}
	targetPath = cleanWebDAVRuntimeTargetPath(targetPath)
	if targetPath == "" {
		return nil
	}
	if targetPath == "/" {
		return nil
	}

	paths := make([]string, 0)
	for _, rule := range s.accessRules {
		rulePath := cleanWebDAVAccessRulePath(rule.Path)
		if rulePath == "" || rulePath == "/" || rulePath == targetPath {
			continue
		}
		if !pathMatchesOrDescendant(targetPath, rulePath) {
			continue
		}
		if !accessRuleAllowsScope(rule, s, accessRead) {
			continue
		}
		paths = append(paths, rulePath)
	}
	return paths
}

func (s requestScope) matchAccessRule(targetPath string) (DirectoryAccessRule, bool) {
	targetPath = cleanWebDAVRuntimeTargetPath(targetPath)
	if targetPath == "" {
		return DirectoryAccessRule{}, false
	}
	bestIndex := -1
	bestLength := -1
	for i, rule := range s.accessRules {
		rulePath := cleanWebDAVAccessRulePath(rule.Path)
		if rulePath == "" || !pathMatchesOrDescendant(rulePath, targetPath) {
			continue
		}
		if len(rulePath) > bestLength {
			bestIndex = i
			bestLength = len(rulePath)
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

	var users []string
	var roles []string
	var groupsAllowed []string
	if mode == accessWrite {
		users = normalizeAccessRuleRuntimeValues(rule.WriteUsers)
		roles = normalizeAccessRuleRuntimeValues(rule.WriteRoles)
		groupsAllowed = normalizeAccessRuleRuntimeValues(rule.WriteGroups)
	} else {
		users = normalizeAccessRuleRuntimeValues(append(append([]string(nil), rule.ReadUsers...), rule.WriteUsers...))
		roles = normalizeAccessRuleRuntimeValues(append(append([]string(nil), rule.ReadRoles...), rule.WriteRoles...))
		groupsAllowed = normalizeAccessRuleRuntimeValues(append(append([]string(nil), rule.ReadGroups...), rule.WriteGroups...))
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

func normalizeAccessRuleRuntimeValues(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			normalized = append(normalized, value)
		}
	}
	return normalized
}

func normalizeScopedHomeDir(homeDir string) (string, bool) {
	trimmed := strings.TrimSpace(homeDir)
	if trimmed == "" || strings.IndexFunc(trimmed, unicode.IsControl) >= 0 || hasDotSegment(trimmed) {
		return "", false
	}
	normalized := path.Clean(strings.ReplaceAll(trimmed, "\\", "/"))
	if !path.IsAbs(normalized) {
		normalized = "/" + normalized
	}
	return normalized, true
}

func hasDotSegment(rawPath string) bool {
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	if strings.ContainsRune(normalized, '\x00') {
		return true
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}

	return false
}

func cleanDecodedWebDAVURLPath(rawPath string) (string, bool) {
	if containsWebDAVPathControlCharacter(rawPath) || hasDotSegment(rawPath) {
		return "", false
	}
	cleanPath := path.Clean(strings.ReplaceAll(rawPath, "\\", "/"))
	if !path.IsAbs(cleanPath) {
		cleanPath = "/" + cleanPath
	}
	return cleanPath, true
}

func cleanDecodedAbsoluteWebDAVURLPath(rawPath string) (string, bool) {
	if !strings.HasPrefix(rawPath, "/") {
		return "", false
	}
	return cleanDecodedWebDAVURLPath(rawPath)
}

func containsWebDAVPathControlCharacter(rawPath string) bool {
	return strings.IndexFunc(rawPath, unicode.IsControl) >= 0
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

// RuntimeLockState owns path serialization and DAV locks across handler rebuilds.
// A single state must be reused while runtime configuration is hot-updated.
type RuntimeLockState struct {
	pathLock *PathLock

	locksMu sync.Mutex
	locks   map[string]webdavLock

	lifecycleMu     sync.Mutex
	cleanupStarted  bool
	closed          bool
	lockCleanupStop chan struct{}
	lockCleanupDone chan struct{}
}

// NewRuntimeLockState creates lock state that can be shared by successive handlers.
func NewRuntimeLockState() *RuntimeLockState {
	return &RuntimeLockState{
		pathLock:        NewPathLock(),
		locks:           make(map[string]webdavLock),
		lockCleanupStop: make(chan struct{}),
		lockCleanupDone: make(chan struct{}),
	}
}

// Close stops the background DAV lock cleanup loop.
func (s *RuntimeLockState) Close() {
	if s == nil {
		return
	}

	s.lifecycleMu.Lock()
	if !s.closed {
		s.closed = true
		if s.cleanupStarted {
			close(s.lockCleanupStop)
		}
	}
	started := s.cleanupStarted
	done := s.lockCleanupDone
	s.lifecycleMu.Unlock()

	if started {
		<-done
	}
}

func (s *RuntimeLockState) startCleanupLoop(interval time.Duration) {
	if s == nil || interval <= 0 {
		return
	}

	s.lifecycleMu.Lock()
	if s.closed || s.cleanupStarted {
		s.lifecycleMu.Unlock()
		return
	}
	s.cleanupStarted = true
	stop := s.lockCleanupStop
	done := s.lockCleanupDone
	s.lifecycleMu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		defer close(done)

		for {
			select {
			case <-ticker.C:
				s.locksMu.Lock()
				removeExpiredWebDAVLocks(s.locks, time.Now())
				s.locksMu.Unlock()
				onWebDAVLockCleanupComplete()
			case <-stop:
				return
			}
		}
	}()
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
	lifecycleID               uint64
	fs                        *storage.FileSystem
	prefix                    string
	readOnly                  bool
	pathLock                  *PathLock
	propCache                 *PropfindCache
	locksMu                   *sync.Mutex
	locks                     map[string]webdavLock
	lockCleanupInterval       time.Duration
	runtimeLockState          *RuntimeLockState
	ownsRuntimeLockState      bool
	newLockToken              func() (string, error)
	authType                  string
	username                  string
	password                  string
	userAuthenticator         UserAuthenticator
	quotaCoordinator          *quotareservation.Coordinator
	directoryQuotas           []DirectoryQuota
	directoryAccess           []DirectoryAccessRule
	beforeCopyFile            func(srcPath, dstPath string) error
	beforePutWrite            func(filePath string) error
	beforeQuotaMutationCommit func(operation string)
	readTimeout               time.Duration
	writeTimeout              time.Duration
	pathChangesObserved       bool
}

// Config holds WebDAV handler configuration
type Config struct {
	FileSystem        *storage.FileSystem
	Prefix            string // URL prefix, e.g., "/dav"
	ReadOnly          bool
	AuthType          string // "none", "basic", "users"
	Username          string
	Password          string
	UserAuthenticator UserAuthenticator
	DirectoryQuotas   []DirectoryQuota
	DirectoryAccess   []DirectoryAccessRule
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	QuotaCoordinator  *quotareservation.Coordinator
	RuntimeLockState  *RuntimeLockState
	// PathChangesObservedExternally indicates that storage rename/delete hooks
	// dispatch committed namespace changes back to this handler.
	PathChangesObservedExternally bool
}

// NewHandler creates a WebDAV handler
func NewHandler(cfg Config) *Handler {
	authType := normalizeAuthType(cfg.AuthType)
	username := cfg.Username
	if authType == "basic" && strings.TrimSpace(username) == "" {
		username = "admin"
	}
	quotaCoordinator := cfg.QuotaCoordinator
	if quotaCoordinator == nil {
		quotaCoordinator = quotareservation.NewCoordinator()
	}
	runtimeLockState := cfg.RuntimeLockState
	ownsRuntimeLockState := runtimeLockState == nil
	if runtimeLockState == nil {
		runtimeLockState = NewRuntimeLockState()
	}

	return &Handler{
		lifecycleID:          nextWebDAVHandlerLifecycleID(),
		fs:                   cfg.FileSystem,
		prefix:               strings.TrimSuffix(cfg.Prefix, "/"),
		readOnly:             cfg.ReadOnly,
		pathLock:             runtimeLockState.pathLock,
		propCache:            NewPropfindCache(30*time.Second, 1000),
		locksMu:              &runtimeLockState.locksMu,
		locks:                runtimeLockState.locks,
		lockCleanupInterval:  webdavLockCleanupInterval,
		runtimeLockState:     runtimeLockState,
		ownsRuntimeLockState: ownsRuntimeLockState,
		newLockToken:         generateOpaqueLockToken,
		authType:             authType,
		username:             username,
		password:             cfg.Password,
		userAuthenticator:    cfg.UserAuthenticator,
		quotaCoordinator:     quotaCoordinator,
		directoryQuotas:      cloneWebDAVDirectoryQuotas(cfg.DirectoryQuotas),
		directoryAccess:      cloneWebDAVDirectoryAccessRules(cfg.DirectoryAccess),
		readTimeout:          cfg.ReadTimeout,
		writeTimeout:         cfg.WriteTimeout,
		pathChangesObserved:  cfg.PathChangesObservedExternally,
	}
}

func nextWebDAVHandlerLifecycleID() uint64 {
	for {
		current := webDAVHandlerLifecycleSequence.Load()
		if current == ^uint64(0) {
			panic("WebDAV handler lifecycle ID space exhausted")
		}
		if webDAVHandlerLifecycleSequence.CompareAndSwap(current, current+1) {
			return current + 1
		}
	}
}

func normalizeAuthType(authType string) string {
	normalized := strings.ToLower(strings.TrimSpace(authType))
	if normalized == "" {
		return "basic"
	}
	return normalized
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
	if h == nil || !h.ownsRuntimeLockState {
		return
	}
	h.runtimeLockState.Close()
}

// WebDAVLifecycleID returns the process-unique immutable handler identity.
func (h *Handler) WebDAVLifecycleID() uint64 {
	if h == nil {
		return 0
	}
	return h.lifecycleID
}

// OnPathRenamed invalidates cached listings and rebases locks after an external rename.
func (h *Handler) OnPathRenamed(oldPath, newPath string) {
	h.moveLocksUnderPath(oldPath, newPath)
	h.InvalidateRenamedPathCache(oldPath, newPath)
}

// OnPathDeleted invalidates cached listings and clears locks after an external delete.
func (h *Handler) OnPathDeleted(filePath string) *storage.PathDeleteHookResult {
	removedLocks := h.clearLocksUnderPathWithSnapshot(filePath)
	h.InvalidateDeletedPathCache(filePath)
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

// PathChangeRuntimeState identifies the shared lock state used by path callbacks.
func (h *Handler) PathChangeRuntimeState() *RuntimeLockState {
	if h == nil {
		return nil
	}
	return h.runtimeLockState
}

// InvalidateRenamedPathCache invalidates generation-local cache entries after a rename.
func (h *Handler) InvalidateRenamedPathCache(oldPath, newPath string) {
	if h == nil {
		return
	}
	affectedPaths := []string{path.Dir(oldPath), oldPath, path.Dir(newPath), newPath}
	h.invalidatePropCache(affectedPaths...)
}

// InvalidateDeletedPathCache invalidates generation-local cache entries after a delete.
func (h *Handler) InvalidateDeletedPathCache(filePath string) {
	if h == nil {
		return
	}
	h.invalidatePropCache(path.Dir(filePath), filePath)
}

func (h *Handler) startLockCleanupLoop() {
	if h == nil || h.runtimeLockState == nil {
		return
	}
	h.runtimeLockState.startCleanupLoop(h.lockCleanupInterval)
}

// ServeHTTP handles WebDAV requests
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w = h.withTransferIdleDeadlines(w, r)

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
	cleanPath, ok := cleanDecodedAbsoluteWebDAVURLPath(requestPath)
	if !ok {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
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

	if !isSupportedWebDAVMethod(r.Method) {
		h.writeMethodNotAllowed(w, scope, "method not supported")
		return
	}

	if mode, ok := accessModeForMethod(r.Method); ok && !h.authorizeScopePath(ctx, w, scope, filePath, mode) {
		return
	}

	// Check read-only mode
	if (h.readOnly || scope.readOnly) && !isReadMethod(r.Method) {
		w.Header().Set("Allow", h.allowedMethodsForScope(scope))
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
		h.writeMethodNotAllowed(w, scope, "method not supported")
	}
}

func (h *Handler) withTransferIdleDeadlines(w http.ResponseWriter, r *http.Request) http.ResponseWriter {
	w = httpstream.NewWriteIdleDeadlineResponseWriter(w, h.writeTimeout)
	if r != nil && r.Method == http.MethodPut {
		r.Body = httpstream.NewReadIdleDeadlineBody(r.Body, w, h.readTimeout)
	}
	return w
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

func isSupportedWebDAVMethod(method string) bool {
	switch method {
	case "OPTIONS", http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete,
		"MKCOL", "COPY", "MOVE", "PROPFIND", "PROPPATCH", "LOCK", "UNLOCK":
		return true
	}
	return false
}

func authorizeScopePath(w http.ResponseWriter, scope requestScope, targetPath string, mode accessMode) bool {
	if scope.canAccess(targetPath, mode) {
		return true
	}
	http.Error(w, "path access denied", http.StatusForbidden)
	return false
}

func (h *Handler) authorizeScopePath(ctx context.Context, w http.ResponseWriter, scope requestScope, targetPath string, mode accessMode) bool {
	if scope.canAccess(targetPath, mode) {
		return true
	}
	if mode == accessRead {
		ok, err := h.hasExistingReadableDescendantRule(ctx, scope, targetPath)
		if err != nil {
			h.handleError(w, err)
			return false
		}
		if ok {
			return true
		}
	}
	http.Error(w, "path access denied", http.StatusForbidden)
	return false
}

func (h *Handler) scopeCanReadOrNavigate(ctx context.Context, scope requestScope, targetPath string) (bool, error) {
	if scope.canAccess(targetPath, accessRead) {
		return true, nil
	}
	return h.hasExistingReadableDescendantRule(ctx, scope, targetPath)
}

func (h *Handler) handleOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", h.allowedMethodsForScope(scopeFromContext(r.Context())))
	w.Header().Set("DAV", "1, 2")
	w.Header().Set("MS-Author-Via", "DAV")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) writeMethodNotAllowed(w http.ResponseWriter, scope requestScope, message string) {
	w.Header().Set("Allow", h.allowedMethodsForScope(scope))
	http.Error(w, message, http.StatusMethodNotAllowed)
}

func (h *Handler) allowedMethodsForScope(scope requestScope) string {
	if h.readOnly || scope.readOnly {
		return webDAVReadOnlyAllowedMethods
	}
	return webDAVAllowedMethods
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
			w.WriteHeader(http.StatusOK)
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
		if !matchStrongETag(im, etag) {
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
		if matchWeakETag(inm, etag) {
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
		w.WriteHeader(http.StatusOK)
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
	children, err := h.visibleDirectoryChildren(ctx, filePath)
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

type putCommitGuardReader struct {
	reader       io.Reader
	finalize     func() error
	finalizeOnce sync.Once
	finalizeErr  error
	release      func()
	releaseOnce  sync.Once
}

func (r *putCommitGuardReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if !errors.Is(err, io.EOF) {
		return n, err
	}
	r.finalizeOnce.Do(func() {
		if r.finalize != nil {
			r.finalizeErr = r.finalize()
		}
	})
	if r.finalizeErr != nil {
		return n, r.finalizeErr
	}
	return n, err
}

func (r *putCommitGuardReader) Release() {
	r.releaseOnce.Do(func() {
		if r.release != nil {
			r.release()
		}
	})
}

func (h *Handler) newPutCommitGuardReader(
	ctx context.Context,
	r *http.Request,
	reader io.Reader,
	filePath string,
	clientPath string,
	resourcePaths []string,
	namespacePaths []string,
) *putCommitGuardReader {
	guard := &putCommitGuardReader{reader: reader}
	guard.finalize = func() error {
		releaseHierarchy := h.acquireHierarchyLocks(hierarchyLockSpec{path: filePath, write: true})
		guard.release = releaseHierarchy
		// The admission-time reservation and quota-rule snapshot remain authoritative.
		// Fresh authentication below rechecks access without retroactively changing quota policy.
		mutation, err := h.acquireQuotaMutationForCommit(ctx, "put")
		if err != nil {
			return err
		}
		guard.release = func() {
			mutation.Release()
			releaseHierarchy()
		}
		if err := h.validatePutCommitBoundary(ctx, r, filePath, clientPath, resourcePaths, namespacePaths); err != nil {
			return err
		}
		return nil
	}
	return guard
}

func (h *Handler) validatePutCommitBoundary(
	ctx context.Context,
	r *http.Request,
	filePath string,
	clientPath string,
	resourcePaths []string,
	namespacePaths []string,
) error {
	identity, authenticated := h.authenticate(r)
	if !authenticated {
		return errWebDAVPathAccessDenied
	}
	freshScope, err := h.scopeForIdentity(identity)
	if err != nil || h.readOnly || freshScope.readOnly {
		return errWebDAVPathAccessDenied
	}
	mappedPath, ok := freshScope.storagePath(clientPath)
	if !ok || mappedPath != filePath || !freshScope.canAccess(filePath, accessWrite) {
		return errWebDAVPathAccessDenied
	}
	freshRequest := r.WithContext(contextWithScope(r.Context(), freshScope))

	parent := path.Dir(filePath)
	if parent != "/" {
		parentInfo, err := h.fs.Stat(ctx, parent)
		if err != nil {
			return err
		}
		if !parentInfo.IsDir {
			return storage.ErrNotDir
		}
	}
	if !h.writeLockAuthorized(
		freshRequest,
		filePath,
		resourcePaths,
		namespacePaths,
		nil,
	) {
		return storage.ErrFileLocked
	}
	return nil
}

func (h *Handler) handlePut(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	releaseInitialLocks := h.acquireHierarchyLocks(hierarchyLockSpec{path: filePath, write: true})
	initialLocksHeld := true
	defer func() {
		if initialLocksHeld {
			releaseInitialLocks()
		}
	}()

	if strings.TrimSpace(r.Header.Get("Content-Range")) != "" {
		http.Error(w, "Content-Range is not supported for PUT", http.StatusBadRequest)
		return
	}
	if r.ContentLength > maxWebDAVWriteSize {
		h.handleError(w, storage.ErrFileTooLarge)
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
			if !matchStrongETag(im, etag) {
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
			if matchWeakETag(inm, etag) {
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

	condition := storage.WriteFileCondition{ExpectedExists: !isCreate}
	replacedBytes := int64(0)
	if !isCreate {
		condition.DeleteIdentityToken = existingInfo.DeleteIdentityToken
		replacedBytes = nonNegativeSize(existingInfo.Size)
	}
	reader, releaseQuota, err := h.quotaCheckedUploadReader(
		ctx,
		filePath,
		r.Body,
		r.ContentLength,
		condition,
		replacedBytes,
	)
	if err != nil {
		h.handleError(w, err)
		return
	}
	defer releaseQuota()

	clientPath, ok := scopeFromContext(ctx).clientPath(filePath)
	if !ok {
		h.handleError(w, errWebDAVPathAccessDenied)
		return
	}
	commitGuard := h.newPutCommitGuardReader(ctx, r, reader, filePath, clientPath, resourcePaths, namespacePaths)
	defer commitGuard.Release()

	releaseInitialLocks()
	initialLocksHeld = false
	if h.beforePutWrite != nil {
		if err := h.beforePutWrite(filePath); err != nil {
			h.handleError(w, err)
			return
		}
	}

	err = h.fs.WriteFileIfUnchanged(ctx, filePath, commitGuard, condition)
	releaseQuota()
	commitGuard.Release()
	if err != nil {
		if isOnlyVisibleMutationWarning(err) {
			markVisibleMutationWarningHeader(w)
		} else {
			if errors.Is(err, storage.ErrWriteConflict) && hasWebDAVWritePrecondition(r.Header) {
				http.Error(w, errPreconditionFailed.Error(), http.StatusPreconditionFailed)
				return
			}
			h.handleError(w, err)
			return
		}
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

func hasWebDAVWritePrecondition(header http.Header) bool {
	return strings.TrimSpace(header.Get("If-Match")) != "" ||
		strings.TrimSpace(header.Get("If-None-Match")) != "" ||
		strings.TrimSpace(header.Get("If-Unmodified-Since")) != ""
}

func (h *Handler) handleDelete(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	releaseLocks := h.acquireHierarchyLocks(hierarchyLockSpec{path: filePath, write: true})
	defer releaseLocks()

	if !h.authorizeWriteLockWithScope(w, r, filePath, []string{filePath}, namespacePathsForResources(filePath), []string{filePath}) {
		return
	}

	affectedPaths := []string{path.Dir(filePath), filePath}
	h.invalidatePropCache(affectedPaths...)

	scope := scopeFromContext(ctx)
	var authorize storage.DeletePathAuthorizer
	if scope.scoped {
		authorize = func(targetPath string) error {
			if !scope.canAccess(targetPath, accessWrite) {
				return errWebDAVPathAccessDenied
			}
			return nil
		}
	}
	beforeWebDAVDeleteStorage(h, filePath)
	depth := r.Header.Get("Depth")
	ifMatch := r.Header.Get("If-Match")
	ifNoneMatch := r.Header.Get("If-None-Match")
	ifUnmodifiedSince := r.Header.Get("If-Unmodified-Since")
	needsTargetValidator := depth != "" || ifMatch != "" || ifNoneMatch != "" || ifUnmodifiedSince != ""
	var err error
	if needsTargetValidator {
		validate := func(snapshot storage.DeleteTargetSnapshot) error {
			info := snapshot.Root
			if info.IsDir {
				if err := h.validateInfinityOnlyDepth(depth); err != nil {
					return err
				}
			}
			etag := fmt.Sprintf(`"%s"`, info.ContentHash)
			return h.mutationPreconditionError(r, etag, info.ModTime)
		}
		err = h.fs.DeleteWithTargetValidatorOptions(ctx, filePath, storage.DeleteTargetSnapshotOptions{
			IncludeContentHash: ifMatch != "" || ifNoneMatch != "",
		}, validate, authorize)
	} else {
		err = h.fs.DeleteWithPathAuthorizer(ctx, filePath, authorize)
	}
	if err != nil {
		if errors.Is(err, errInvalidDepthHeader) {
			writeKnownWebDAVError(w, errInvalidDepthHeader, http.StatusBadRequest)
			return
		}
		if errors.Is(err, errPreconditionFailed) {
			http.Error(w, errPreconditionFailed.Error(), http.StatusPreconditionFailed)
			return
		}
		if !markDeleteWarningHeaders(w, err) {
			h.handleError(w, err)
			return
		}
	}
	if !h.pathChangesObserved {
		h.clearLocksUnderPath(filePath)
	}

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
		h.writeMethodNotAllowed(w, scopeFromContext(ctx), "resource already exists")
		return
	} else if !errors.Is(err, storage.ErrNotFound) && !errors.Is(err, storage.ErrNotDir) {
		h.handleError(w, err)
		return
	}

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

	affectedPaths := []string{path.Dir(filePath), filePath}
	h.invalidatePropCache(affectedPaths...)

	if err := h.fs.Mkdir(ctx, filePath); err != nil {
		if errors.Is(err, storage.ErrAlreadyExists) {
			h.writeMethodNotAllowed(w, scopeFromContext(ctx), "resource already exists")
			return
		}
		if errors.Is(err, storage.ErrNotDir) {
			http.Error(w, "parent path is not a directory", http.StatusConflict)
			return
		}
		if isOnlyVisibleMutationWarning(err) {
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
	if err := h.ensureDestinationParent(ctx, dst); err != nil {
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
	if copyErr != nil && !isOnlyVisibleMutationWarning(copyErr) {
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
	userQuotaApplies := scope.scoped && scope.quotaBytes > 0 && strings.TrimSpace(scope.homeDir) != "" && pathMatchesOrDescendant(scope.homeDir, dstPath)
	hasDirectoryQuotas := h.hasDirectoryQuotaRules()
	prepare := func(ctx context.Context, _ quotareservation.View) ([]webDAVQuotaClaim, error) {
		claims := make([]webDAVQuotaClaim, 0, 1+len(h.directoryQuotas))
		if userQuotaApplies {
			requiredBytes, err := h.copyRequiredBytes(ctx, srcPath, dstPath, depth, allowOverwrite)
			if err != nil {
				return nil, err
			}
			usedBytes, err := h.pathLogicalSizeIfExists(ctx, scope.homeDir)
			if err != nil {
				return nil, err
			}
			claims = append(claims, webDAVQuotaClaim{
				kind:          quotaTypeUser,
				path:          scope.homeDir,
				usedBytes:     usedBytes,
				quotaBytes:    scope.quotaBytes,
				requiredBytes: requiredBytes,
				exceededErr:   errWebDAVUserQuotaExceeded,
			})
		}
		if hasDirectoryQuotas {
			checks, err := h.copyDirectoryQuotaChecks(ctx, srcPath, dstPath, depth, allowOverwrite)
			if err != nil {
				return nil, err
			}
			for _, check := range checks {
				usedBytes, err := h.pathLogicalSizeIfExists(ctx, check.path)
				if err != nil {
					return nil, err
				}
				claims = append(claims, webDAVQuotaClaim{
					kind:          quotaTypeDirectory,
					path:          check.path,
					usedBytes:     usedBytes,
					quotaBytes:    check.quotaBytes,
					requiredBytes: check.requiredBytes,
					exceededErr:   errWebDAVDirectoryQuotaExceeded,
				})
			}
		}
		return claims, nil
	}

	var reservation *quotareservation.Reservation
	var err error
	if userQuotaApplies || hasDirectoryQuotas {
		reservation, err = h.reserveQuotaClaims(ctx, prepare)
		if err != nil {
			return err
		}
	}

	mutation, err := h.acquireQuotaMutationForCommit(ctx, "copy")
	if err != nil {
		if reservation != nil {
			reservation.Release()
		}
		return err
	}
	defer func() {
		if reservation != nil {
			reservation.Release()
		}
		mutation.Release()
	}()
	if reservation != nil {
		if err := h.refreshQuotaClaims(ctx, mutation, reservation, prepare); err != nil {
			return err
		}
	}

	err = h.copyResource(ctx, srcPath, dstPath, depth, allowOverwrite)
	if reservation != nil {
		reservation.Release()
	}
	mutation.Release()
	return err
}

func (h *Handler) copyDirectoryQuotaChecks(ctx context.Context, srcPath, dstPath, depth string, allowOverwrite bool) ([]webDAVDirectoryQuotaCheck, error) {
	if len(h.directoryQuotas) == 0 {
		return nil, nil
	}

	checks := make([]webDAVDirectoryQuotaCheck, 0, len(h.directoryQuotas))
	for _, rule := range h.directoryQuotas {
		rulePath := cleanWebDAVQuotaPath(rule.Path)
		if rulePath == "" || rule.QuotaBytes <= 0 {
			continue
		}

		requiredBytes, err := h.copyDirectoryQuotaRequiredBytes(ctx, srcPath, dstPath, depth, allowOverwrite, rulePath)
		if err != nil {
			return nil, err
		}
		if requiredBytes <= 0 {
			continue
		}
		checks = append(checks, webDAVDirectoryQuotaCheck{
			path:          rulePath,
			quotaBytes:    rule.QuotaBytes,
			requiredBytes: requiredBytes,
		})
	}
	return checks, nil
}

func (h *Handler) copyDirectoryQuotaRequiredBytes(ctx context.Context, srcPath, dstPath, depth string, allowOverwrite bool, quotaPath string) (int64, error) {
	if pathMatchesOrDescendant(quotaPath, dstPath) {
		return h.copyRequiredBytes(ctx, srcPath, dstPath, depth, allowOverwrite)
	}

	sourceInfo, err := h.fs.Stat(ctx, srcPath)
	if err != nil {
		return 0, err
	}
	if sourceInfo.IsDir && depth == "0" {
		return 0, nil
	}

	mappedSourcePath, ok := mappedTreePathForQuota(srcPath, dstPath, quotaPath)
	if !ok {
		return 0, nil
	}
	return h.copySourcePathLogicalSizeIfExists(ctx, mappedSourcePath)
}

func (h *Handler) copySourcePathLogicalSizeIfExists(ctx context.Context, targetPath string) (int64, error) {
	info, err := h.fs.Stat(ctx, targetPath)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrNotDir) {
			return 0, nil
		}
		return 0, err
	}
	return h.copySourceLogicalSize(ctx, info)
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
		if requiredBytes <= replacedBytes {
			return 0, nil
		}
		requiredBytes -= replacedBytes
	}
	return requiredBytes, nil
}

func (h *Handler) copySourceLogicalSize(ctx context.Context, info *storage.FileInfo) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if info == nil {
		return 0, nil
	}
	scope := scopeFromContext(ctx)
	if scope.scoped && !scope.canAccess(info.Path, accessRead) {
		ok, err := h.hasExistingReadableDescendantRule(ctx, scope, info.Path)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, nil
		}
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
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if child == nil {
			continue
		}
		childPath, _, err := webDAVReadDirChildPath(info.Path, child)
		if err != nil {
			return 0, err
		}
		childInfo := *child
		childInfo.Path = childPath
		size, err := h.copySourceLogicalSize(ctx, &childInfo)
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

func webDAVReadDirChildPath(parentPath string, child *storage.FileInfo) (string, string, error) {
	if child == nil {
		return "", "", storage.ErrNotFound
	}
	cleanParent := path.Clean(parentPath)
	childPath := child.Path
	if childPath == "" {
		childName, err := safeWebDAVFallbackChildName(child.Name)
		if err != nil {
			return "", "", err
		}
		childPath = path.Join(cleanParent, childName)
	}
	if strings.Contains(childPath, "\\") || containsWebDAVPathControlCharacter(childPath) || hasDotSegment(childPath) {
		return "", "", errWebDAVPathAccessDenied
	}
	cleanChild := path.Clean(childPath)
	if cleanChild == cleanParent || path.Dir(cleanChild) != cleanParent {
		return "", "", errWebDAVPathAccessDenied
	}
	return cleanChild, path.Base(cleanChild), nil
}

func safeWebDAVFallbackChildName(name string) (string, error) {
	childName := strings.ReplaceAll(name, "\\", "/")
	if childName == "" || strings.Contains(childName, "/") || containsWebDAVPathControlCharacter(childName) || hasDotSegment(childName) {
		return "", errWebDAVPathAccessDenied
	}
	return childName, nil
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
			if isOnlyVisibleMutationWarning(err) {
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
			if child == nil {
				continue
			}
			childPath, childName, err := webDAVReadDirChildPath(srcPath, child)
			if err != nil {
				return h.rollbackCopiedDirectory(dstPath, err)
			}
			visible, err := h.scopeCanReadOrNavigate(ctx, scope, childPath)
			if err != nil {
				return h.rollbackCopiedDirectory(dstPath, err)
			}
			if !visible {
				continue
			}
			if err := h.copyResource(ctx, childPath, path.Join(dstPath, childName), "infinity", false); err != nil {
				if isOnlyVisibleMutationWarning(err) {
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

func (h *Handler) quotaCheckedUploadReader(
	ctx context.Context,
	targetPath string,
	reader io.Reader,
	contentLength int64,
	condition storage.WriteFileCondition,
	replacedBytes int64,
) (io.Reader, func(), error) {
	scope := scopeFromContext(ctx)
	directoryRules := h.directoryQuotaRulesForTarget(targetPath)
	userQuotaApplies := scope.scoped && scope.quotaBytes > 0 && strings.TrimSpace(scope.homeDir) != "" && pathMatchesOrDescendant(scope.homeDir, targetPath)
	if !userQuotaApplies && len(directoryRules) == 0 {
		return reader, func() {}, nil
	}

	requiredBytes := int64(0)
	if contentLength >= 0 && contentLength > replacedBytes {
		requiredBytes = contentLength - replacedBytes
	}
	var initialClaims []webDAVQuotaClaim
	reservation, err := h.reserveQuotaClaims(ctx, func(ctx context.Context, _ quotareservation.View) ([]webDAVQuotaClaim, error) {
		if err := h.validateUploadTargetSnapshot(ctx, targetPath, condition); err != nil {
			return nil, err
		}
		var claimErr error
		initialClaims, claimErr = h.currentUploadQuotaClaims(
			ctx,
			scope,
			userQuotaApplies,
			directoryRules,
			requiredBytes,
		)
		return initialClaims, claimErr
	})
	if err != nil {
		return nil, nil, err
	}
	reservations := []*quotareservation.Reservation{reservation}
	releaseReservations := func() {
		for index := len(reservations) - 1; index >= 0; index-- {
			reservations[index].Release()
		}
	}

	if contentLength >= 0 {
		return &quotaLimitedReader{
			reader:    reader,
			remaining: contentLength,
			err:       webDAVUploadQuotaLimitError(initialClaims),
		}, releaseReservations, nil
	}

	allowedBytes := nonNegativeSize(replacedBytes)
	if allowedBytes > maxWebDAVWriteSize {
		allowedBytes = maxWebDAVWriteSize
	}
	grow := func() (int64, error) {
		if allowedBytes >= maxWebDAVWriteSize {
			return 0, &webDAVQuotaGrowthLimitError{err: storage.ErrFileTooLarge}
		}
		requestedBytes := webDAVQuotaGrowthChunk
		if remaining := maxWebDAVWriteSize - allowedBytes; requestedBytes > remaining {
			requestedBytes = remaining
		}

		grantedBytes := requestedBytes
		var limitingClaim *webDAVQuotaClaim
		nextReservation, reserveErr := h.reserveQuotaClaims(ctx, func(ctx context.Context, view quotareservation.View) ([]webDAVQuotaClaim, error) {
			if err := h.validateUploadTargetSnapshot(ctx, targetPath, condition); err != nil {
				return nil, err
			}
			claims, err := h.currentUploadQuotaClaims(ctx, scope, userQuotaApplies, directoryRules, 0)
			if err != nil {
				return nil, err
			}
			for index := range claims {
				availableBytes := webDAVQuotaClaimAvailable(claims[index], view)
				if availableBytes < grantedBytes {
					grantedBytes = availableBytes
					limitingClaim = &claims[index]
				}
			}
			if grantedBytes <= 0 {
				if limitingClaim == nil && len(claims) > 0 {
					limitingClaim = &claims[0]
				}
				if limitingClaim != nil && limitingClaim.exceededErr != nil {
					return nil, limitingClaim.exceededErr
				}
				return nil, errWebDAVQuotaExceeded
			}
			for index := range claims {
				claims[index].requiredBytes = grantedBytes
			}
			return claims, nil
		})
		if reserveErr != nil {
			if errors.Is(reserveErr, errWebDAVQuotaExceeded) {
				return 0, &webDAVQuotaGrowthLimitError{err: reserveErr}
			}
			return 0, reserveErr
		}
		reservations = append(reservations, nextReservation)
		allowedBytes += grantedBytes
		return grantedBytes, nil
	}

	return &quotaLimitedReader{
		reader:    reader,
		remaining: allowedBytes,
		err:       webDAVUploadQuotaLimitError(initialClaims),
		grow:      grow,
	}, releaseReservations, nil
}

func (h *Handler) validateUploadTargetSnapshot(ctx context.Context, targetPath string, condition storage.WriteFileCondition) error {
	info, err := h.fs.Stat(ctx, targetPath)
	if err != nil {
		if (errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrNotDir)) && !condition.ExpectedExists {
			return nil
		}
		if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrNotDir) {
			return storage.ErrWriteConflict
		}
		return err
	}
	if !condition.ExpectedExists || info.IsDir || info.DeleteIdentityToken != condition.DeleteIdentityToken {
		return storage.ErrWriteConflict
	}
	return nil
}

func (h *Handler) currentUploadQuotaClaims(
	ctx context.Context,
	scope requestScope,
	userQuotaApplies bool,
	directoryRules []DirectoryQuota,
	requiredBytes int64,
) ([]webDAVQuotaClaim, error) {
	claims := make([]webDAVQuotaClaim, 0, 1+len(directoryRules))
	if userQuotaApplies {
		usedBytes, err := h.pathLogicalSizeIfExists(ctx, scope.homeDir)
		if err != nil {
			return nil, err
		}
		claims = append(claims, webDAVQuotaClaim{
			kind:          quotaTypeUser,
			path:          scope.homeDir,
			usedBytes:     usedBytes,
			quotaBytes:    scope.quotaBytes,
			requiredBytes: requiredBytes,
			exceededErr:   errWebDAVUserQuotaExceeded,
		})
	}
	for _, rule := range directoryRules {
		usedBytes, err := h.pathLogicalSizeIfExists(ctx, rule.Path)
		if err != nil {
			return nil, err
		}
		claims = append(claims, webDAVQuotaClaim{
			kind:          quotaTypeDirectory,
			path:          rule.Path,
			usedBytes:     usedBytes,
			quotaBytes:    rule.QuotaBytes,
			requiredBytes: requiredBytes,
			exceededErr:   errWebDAVDirectoryQuotaExceeded,
		})
	}
	return claims, nil
}

func webDAVUploadQuotaLimitError(claims []webDAVQuotaClaim) error {
	if len(claims) == 0 || claims[0].exceededErr == nil {
		return errWebDAVQuotaExceeded
	}
	return claims[0].exceededErr
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
	if err := ctx.Err(); err != nil {
		return 0, err
	}
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
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		childPath, _, err := webDAVReadDirChildPath(info.Path, child)
		if err != nil {
			return 0, err
		}
		childInfo := *child
		childInfo.Path = childPath
		size, err := h.fileInfoLogicalSize(ctx, &childInfo)
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

func (h *Handler) directoryQuotaRulesForTarget(targetPath string) []DirectoryQuota {
	if len(h.directoryQuotas) == 0 {
		return nil
	}
	matched := make([]DirectoryQuota, 0, len(h.directoryQuotas))
	for _, rule := range h.directoryQuotas {
		rule.Path = cleanWebDAVQuotaPath(rule.Path)
		if rule.Path == "" {
			continue
		}
		if rule.QuotaBytes <= 0 {
			continue
		}
		if pathMatchesOrDescendant(rule.Path, targetPath) {
			matched = append(matched, rule)
		}
	}
	return matched
}

func (h *Handler) hasDirectoryQuotaRules() bool {
	for _, rule := range h.directoryQuotas {
		if cleanWebDAVQuotaPath(rule.Path) != "" && rule.QuotaBytes > 0 {
			return true
		}
	}
	return false
}

func (h *Handler) reserveMoveQuota(
	ctx context.Context,
	srcPath string,
	dstPath string,
) (*quotareservation.Reservation, func(context.Context, quotareservation.View) ([]webDAVQuotaClaim, error), error) {
	scope := scopeFromContext(ctx)
	userQuotaApplies := scope.scoped && scope.quotaBytes > 0 && strings.TrimSpace(scope.homeDir) != "" && pathMatchesOrDescendant(scope.homeDir, dstPath) && !pathMatchesOrDescendant(scope.homeDir, srcPath)
	hasDirectoryQuotas := h.hasDirectoryQuotaRules()
	prepare := func(ctx context.Context, _ quotareservation.View) ([]webDAVQuotaClaim, error) {
		if !userQuotaApplies && !hasDirectoryQuotas {
			return nil, nil
		}
		claims := make([]webDAVQuotaClaim, 0, 1+len(h.directoryQuotas))
		if userQuotaApplies {
			requiredBytes, err := h.pathLogicalSize(ctx, srcPath)
			if err != nil {
				return nil, err
			}
			usedBytes, err := h.pathLogicalSizeIfExists(ctx, scope.homeDir)
			if err != nil {
				return nil, err
			}
			claims = append(claims, webDAVQuotaClaim{
				kind:          quotaTypeUser,
				path:          scope.homeDir,
				usedBytes:     usedBytes,
				quotaBytes:    scope.quotaBytes,
				requiredBytes: requiredBytes,
				exceededErr:   errWebDAVUserQuotaExceeded,
			})
		}

		if hasDirectoryQuotas {
			checks, err := h.moveDirectoryQuotaChecks(ctx, srcPath, dstPath)
			if err != nil {
				return nil, err
			}
			for _, check := range checks {
				usedBytes, err := h.pathLogicalSizeIfExists(ctx, check.path)
				if err != nil {
					return nil, err
				}
				claims = append(claims, webDAVQuotaClaim{
					kind:          quotaTypeDirectory,
					path:          check.path,
					usedBytes:     usedBytes,
					quotaBytes:    check.quotaBytes,
					requiredBytes: check.requiredBytes,
					exceededErr:   errWebDAVDirectoryQuotaExceeded,
				})
			}
		}
		return claims, nil
	}
	reservation, err := h.reserveQuotaClaims(ctx, prepare)
	return reservation, prepare, err
}

func (h *Handler) moveDirectoryQuotaChecks(ctx context.Context, srcPath, dstPath string) ([]webDAVDirectoryQuotaCheck, error) {
	if len(h.directoryQuotas) == 0 {
		return nil, nil
	}

	checks := make([]webDAVDirectoryQuotaCheck, 0, len(h.directoryQuotas))
	for _, rule := range h.directoryQuotas {
		rulePath := cleanWebDAVQuotaPath(rule.Path)
		if rulePath == "" || rule.QuotaBytes <= 0 {
			continue
		}

		destinationBytes, err := h.mappedTreeLogicalSizeIfExists(ctx, srcPath, dstPath, rulePath)
		if err != nil {
			return nil, err
		}
		sourceBytes, err := h.mappedTreeLogicalSizeIfExists(ctx, srcPath, srcPath, rulePath)
		if err != nil {
			return nil, err
		}
		if destinationBytes <= sourceBytes {
			continue
		}
		requiredBytes := destinationBytes - sourceBytes
		checks = append(checks, webDAVDirectoryQuotaCheck{
			path:          rulePath,
			quotaBytes:    rule.QuotaBytes,
			requiredBytes: requiredBytes,
		})
	}
	return checks, nil
}

func (h *Handler) mappedTreeLogicalSizeIfExists(ctx context.Context, treeRoot, mappedRoot, quotaPath string) (int64, error) {
	sourcePath, ok := mappedTreePathForQuota(treeRoot, mappedRoot, quotaPath)
	if !ok {
		return 0, nil
	}
	return h.pathLogicalSizeIfExists(ctx, sourcePath)
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

type webDAVQuotaClaim struct {
	kind          string
	path          string
	usedBytes     int64
	quotaBytes    int64
	requiredBytes int64
	exceededErr   error
}

func (h *Handler) acquireQuotaMutationForCommit(ctx context.Context, operation string) (*quotareservation.MutationLease, error) {
	if h.beforeQuotaMutationCommit != nil {
		h.beforeQuotaMutationCommit(operation)
	}
	return h.quotaCoordinator.AcquireMutation(ctx)
}

func (c webDAVQuotaClaim) scopeKey() string {
	return quotareservation.ScopeKey(c.kind, c.path)
}

func (h *Handler) reserveQuotaClaims(
	ctx context.Context,
	prepare func(context.Context, quotareservation.View) ([]webDAVQuotaClaim, error),
) (*quotareservation.Reservation, error) {
	var checks []webDAVQuotaClaim
	reservation, err := h.quotaCoordinator.Reserve(ctx, func(ctx context.Context, view quotareservation.View) ([]quotareservation.Claim, error) {
		var prepareErr error
		checks, prepareErr = prepare(ctx, view)
		if prepareErr != nil {
			return nil, prepareErr
		}
		claims := make([]quotareservation.Claim, 0, len(checks))
		for _, check := range checks {
			claims = append(claims, quotareservation.Claim{
				Key:           check.scopeKey(),
				UsedBytes:     check.usedBytes,
				LimitBytes:    check.quotaBytes,
				RequiredBytes: check.requiredBytes,
			})
		}
		return claims, nil
	})
	if err == nil {
		return reservation, nil
	}
	return nil, webDAVQuotaReservationError(checks, err)
}

func (h *Handler) refreshQuotaClaims(
	ctx context.Context,
	mutation *quotareservation.MutationLease,
	reservation *quotareservation.Reservation,
	prepare func(context.Context, quotareservation.View) ([]webDAVQuotaClaim, error),
) error {
	var checks []webDAVQuotaClaim
	err := mutation.Refresh(ctx, reservation, func(ctx context.Context, view quotareservation.View) ([]quotareservation.Claim, error) {
		var prepareErr error
		checks, prepareErr = prepare(ctx, view)
		if prepareErr != nil {
			return nil, prepareErr
		}
		claims := make([]quotareservation.Claim, 0, len(checks))
		for _, check := range checks {
			claims = append(claims, quotareservation.Claim{
				Key:           check.scopeKey(),
				UsedBytes:     check.usedBytes,
				LimitBytes:    check.quotaBytes,
				RequiredBytes: check.requiredBytes,
			})
		}
		return claims, nil
	})
	return webDAVQuotaReservationError(checks, err)
}

func webDAVQuotaReservationError(checks []webDAVQuotaClaim, err error) error {
	if err == nil {
		return nil
	}
	var exceeded *quotareservation.ExceededError
	if !errors.As(err, &exceeded) || exceeded.ClaimIndex < 0 || exceeded.ClaimIndex >= len(checks) {
		return err
	}
	if checks[exceeded.ClaimIndex].exceededErr != nil {
		return checks[exceeded.ClaimIndex].exceededErr
	}
	return errWebDAVQuotaExceeded
}

func webDAVQuotaClaimAvailable(check webDAVQuotaClaim, view quotareservation.View) int64 {
	usedBytes := nonNegativeSize(check.usedBytes)
	quotaBytes := nonNegativeSize(check.quotaBytes)
	reservedBytes := view.ReservedBytes(check.scopeKey())
	if usedBytes >= quotaBytes {
		return 0
	}
	availableBytes := quotaBytes - usedBytes
	if reservedBytes >= availableBytes {
		return 0
	}
	return availableBytes - reservedBytes
}

type quotaLimitedReader struct {
	reader    io.Reader
	remaining int64
	err       error
	grow      func() (int64, error)
}

type webDAVQuotaGrowthLimitError struct {
	err error
}

func (e *webDAVQuotaGrowthLimitError) Error() string {
	if e == nil || e.err == nil {
		return "upload limit reached"
	}
	return e.err.Error()
}

func (e *webDAVQuotaGrowthLimitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (r *quotaLimitedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining <= 0 {
		if r.grow != nil {
			grownBytes, err := r.grow()
			if err != nil {
				var limitErr *webDAVQuotaGrowthLimitError
				if !errors.As(err, &limitErr) {
					return 0, err
				}
				r.err = limitErr.err
			}
			if grownBytes > 0 {
				r.remaining = grownBytes
			} else if err == nil {
				return 0, io.ErrNoProgress
			}
		}
	}
	if r.remaining <= 0 {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			return 0, r.err
		}
		if err != nil {
			return 0, err
		}
		return 0, io.ErrNoProgress
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

func isOnlyVisibleMutationWarning(err error) bool {
	if isHardWriteStateError(err) {
		return false
	}
	return errorTreeAll(err, func(candidate error) bool {
		_, ok := candidate.(*storage.VisibleMutationWarningError)
		return ok
	})
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
	if !isOnlyDeleteMutationWarning(err) {
		return false
	}
	if isTrashDeleteWarning(err) {
		markTrashDeleteCleanupWarningHeader(w)
	}
	if isDeleteCleanupWarning(err) {
		markDeleteCleanupWarningHeader(w)
	}
	if isVisibleMutationWarning(err) {
		markVisibleMutationWarningHeader(w)
	}
	return true
}

func isOnlyDeleteMutationWarning(err error) bool {
	if isHardWriteStateError(err) {
		return false
	}
	return errorTreeAll(err, func(candidate error) bool {
		switch candidate.(type) {
		case *storage.TrashDeleteWarningError, *storage.DeleteCleanupWarningError, *storage.VisibleMutationWarningError:
			return true
		default:
			return false
		}
	})
}

func isHardWriteStateError(err error) bool {
	return errors.Is(err, storage.ErrWriteRecoveryRequired) ||
		errors.Is(err, storage.ErrWriteAtomicRenameUnsupported) ||
		errors.Is(err, storage.ErrInsufficientStorage) ||
		errors.Is(err, storage.ErrWriteBusy) ||
		errors.Is(err, storage.ErrWriteConflict)
}

func errorTreeAll(err error, match func(error) bool) bool {
	if err == nil {
		return false
	}
	if match(err) {
		return true
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		children := multi.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !errorTreeAll(child, match) {
				return false
			}
		}
		return true
	}
	if single, ok := err.(interface{ Unwrap() error }); ok {
		return errorTreeAll(single.Unwrap(), match)
	}
	return false
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

	var cleanupWarning error
	if info.IsDir {
		children, err := h.fs.ReadDir(ctx, targetPath)
		if err != nil {
			return err
		}
		for _, child := range children {
			childPath, _, err := webDAVReadDirChildPath(targetPath, child)
			if err != nil {
				return err
			}
			if err := h.removeCopiedTree(ctx, childPath); err != nil {
				if isOnlyDeleteMutationWarning(err) {
					cleanupWarning = errors.Join(cleanupWarning, err)
					continue
				}
				return err
			}
		}
	}

	if err := h.fs.PermanentDelete(ctx, targetPath); err != nil {
		if isOnlyDeleteMutationWarning(err) {
			return errors.Join(cleanupWarning, err)
		}
		return err
	}
	return cleanupWarning
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
	if err := h.ensureDestinationParent(ctx, dst); err != nil {
		if h.writeParentNotDirectoryConflict(w, err) {
			return
		}
		h.handleError(w, err)
		return
	}
	if !dstExists {
		targetMetadata, err := h.fs.HasVersionMetadataPath(ctx, dst, srcInfo.IsDir)
		if err != nil {
			h.handleError(w, err)
			return
		}
		if targetMetadata {
			h.handleError(w, storage.ErrAlreadyExists)
			return
		}
	}

	quotaReservation, prepareQuota, err := h.reserveMoveQuota(ctx, srcPath, dst)
	if err != nil {
		h.handleError(w, err)
		return
	}
	mutation, err := h.acquireQuotaMutationForCommit(ctx, "move")
	if err != nil {
		quotaReservation.Release()
		h.handleError(w, err)
		return
	}
	defer func() {
		quotaReservation.Release()
		mutation.Release()
	}()
	if err := h.refreshQuotaClaims(ctx, mutation, quotaReservation, prepareQuota); err != nil {
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

		if err := h.removeCopiedTree(ctx, backupPath); err != nil {
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
	if !h.pathChangesObserved {
		h.moveLocksUnderPath(srcPath, dst)
	}
	quotaReservation.Release()
	mutation.Release()

	h.invalidatePropCache(affectedPaths...)

	if dstExists {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) writeFailedMutationPrecondition(w http.ResponseWriter, r *http.Request, etag string, modTime time.Time) bool {
	if err := h.mutationPreconditionError(r, etag, modTime); err != nil {
		http.Error(w, err.Error(), http.StatusPreconditionFailed)
		return true
	}
	return false
}

func (h *Handler) mutationPreconditionError(r *http.Request, etag string, modTime time.Time) error {
	if im := r.Header.Get("If-Match"); im != "" {
		if !matchStrongETag(im, etag) {
			return errPreconditionFailed
		}
	}
	if ius := r.Header.Get("If-Unmodified-Since"); ius != "" {
		if unmodifiedSince, err := http.ParseTime(ius); err == nil && isHTTPTimeAfter(modTime, unmodifiedSince) {
			return errPreconditionFailed
		}
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		if matchWeakETag(inm, etag) {
			return errPreconditionFailed
		}
	}
	return nil
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
		childPath, _, err := webDAVReadDirChildPath(rootPath, child)
		if err != nil {
			return err
		}
		if err := h.authorizeTreeAccess(ctx, childPath, mode); err != nil {
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
		childPath, _, err := webDAVReadDirChildPath(sourceRoot, child)
		if err != nil {
			return err
		}
		childDestination, ok := mapWebDAVDescendantPath(sourceRoot, destinationRoot, childPath)
		if !ok {
			return errWebDAVPathAccessDenied
		}
		if !scope.canAccess(childDestination, mode) {
			return errWebDAVPathAccessDenied
		}
		if child.IsDir {
			if err := h.authorizeMappedTreeAccess(ctx, childPath, childDestination, mode); err != nil {
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
		if child == nil {
			continue
		}
		childPath, _, err := webDAVReadDirChildPath(sourceRoot, child)
		if err != nil {
			return err
		}
		visible, err := h.scopeCanReadOrNavigate(ctx, scope, childPath)
		if err != nil {
			return err
		}
		if !visible {
			continue
		}
		childDestination, ok := mapWebDAVDescendantPath(sourceRoot, destinationRoot, childPath)
		if !ok {
			return errWebDAVPathAccessDenied
		}
		if !scope.canAccess(childDestination, accessWrite) {
			return errWebDAVPathAccessDenied
		}
		if child.IsDir {
			if err := h.authorizeCopyDestinationTreeAccess(ctx, childPath, childDestination); err != nil {
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

func mapWebDAVDescendantPath(sourceRoot, destinationRoot, currentPath string) (string, bool) {
	sourceRoot = cleanWebDAVRuntimePolicyPath(sourceRoot)
	destinationRoot = cleanWebDAVRuntimePolicyPath(destinationRoot)
	currentPath = cleanWebDAVRuntimePolicyPath(currentPath)
	if sourceRoot == "" || destinationRoot == "" || currentPath == "" {
		return "", false
	}

	relativePath := ""
	if sourceRoot == "/" {
		if currentPath == "/" || !strings.HasPrefix(currentPath, "/") {
			return "", false
		}
		relativePath = strings.TrimPrefix(currentPath, "/")
	} else {
		if currentPath != sourceRoot && !isDescendantPath(sourceRoot, currentPath) {
			return "", false
		}
		relativePath = strings.TrimPrefix(currentPath, sourceRoot)
		relativePath = strings.TrimPrefix(relativePath, "/")
	}
	if relativePath == "" {
		return destinationRoot, true
	}
	return path.Clean(path.Join(destinationRoot, relativePath)), true
}

func mappedTreePathForQuota(treeRoot, mappedRoot, quotaPath string) (string, bool) {
	treeRoot = cleanWebDAVRuntimePolicyPath(treeRoot)
	mappedRoot = cleanWebDAVRuntimePolicyPath(mappedRoot)
	quotaPath = cleanWebDAVRuntimePolicyPath(quotaPath)
	if treeRoot == "" || mappedRoot == "" || quotaPath == "" {
		return "", false
	}

	if pathMatchesOrDescendant(quotaPath, mappedRoot) {
		return treeRoot, true
	}
	if !pathMatchesOrDescendant(mappedRoot, quotaPath) {
		return "", false
	}

	relativePath := ""
	if mappedRoot == "/" {
		relativePath = strings.TrimPrefix(quotaPath, "/")
	} else {
		relativePath = strings.TrimPrefix(quotaPath, mappedRoot)
		relativePath = strings.TrimPrefix(relativePath, "/")
	}
	if relativePath == "" {
		return treeRoot, true
	}
	return path.Clean(path.Join(treeRoot, relativePath)), true
}

func cleanAbsoluteWebDAVPath(filePath string) string {
	cleanPath := path.Clean(filePath)
	if !path.IsAbs(cleanPath) {
		cleanPath = "/" + cleanPath
	}
	return cleanPath
}

func cleanWebDAVQuotaPath(quotaPath string) string {
	return cleanWebDAVRuntimePolicyPath(quotaPath)
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

func (h *Handler) ensureDestinationParent(ctx context.Context, dst string) error {
	parentPath := path.Dir(dst)
	info, err := h.fs.Stat(ctx, parentPath)
	if err != nil {
		return err
	}
	if !info.IsDir {
		return storage.ErrNotDir
	}
	return nil
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
		onWebDAVPropfindCacheMiss(h, filePath, depth)
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

	children, err := h.visibleDirectoryChildren(ctx, filePath)
	if err != nil {
		return err
	}

	for _, child := range children {
		*responses = append(*responses, h.propResponse(ctx, child.Path, child))
		if depth == "infinity" {
			if err := h.appendPropfindChildren(ctx, child.Path, child, depth, currentDepth+1, responses); err != nil {
				return err
			}
		}
	}

	return nil
}

func (h *Handler) visibleDirectoryChildren(ctx context.Context, filePath string) ([]*storage.FileInfo, error) {
	children, err := h.fs.ReadDir(ctx, filePath)
	if err != nil {
		return nil, err
	}

	scope := scopeFromContext(ctx)
	sharedChildren, err := h.visibleSharedRootChildren(ctx, filePath)
	if err != nil {
		return nil, err
	}

	filtered := make([]*storage.FileInfo, 0, len(children)+len(sharedChildren))
	seenClientPaths := make(map[string]struct{}, len(children))
	for _, child := range sharedChildren {
		clientPath, ok := scope.clientPath(child.Path)
		if !ok {
			continue
		}
		seenClientPaths[clientPath] = struct{}{}
		filtered = append(filtered, child)
	}

	for _, child := range children {
		if child == nil {
			continue
		}
		childPath, _, err := webDAVReadDirChildPath(filePath, child)
		if err != nil {
			return nil, err
		}
		visible, err := h.scopeCanReadOrNavigate(ctx, scope, childPath)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		clientPath, ok := scope.clientPath(childPath)
		if !ok {
			continue
		}
		if _, exists := seenClientPaths[clientPath]; exists {
			continue
		}
		seenClientPaths[clientPath] = struct{}{}
		childInfo := *child
		childInfo.Path = childPath
		filtered = append(filtered, &childInfo)
	}

	return filtered, nil
}

func (h *Handler) visibleSharedRootChildren(ctx context.Context, filePath string) ([]*storage.FileInfo, error) {
	scope := scopeFromContext(ctx)
	if !scope.scoped || !scope.hasAccessRules() || !scope.isClientRoot(filePath) {
		return nil, nil
	}

	seen := make(map[string]struct{})
	children := make([]*storage.FileInfo, 0)
	for _, rule := range scope.accessRules {
		rulePath := cleanWebDAVAccessRulePath(rule.Path)
		if rulePath == "" || rulePath == "/" {
			continue
		}
		if !accessRuleAllowsScope(rule, scope, accessRead) {
			continue
		}
		ruleTargetExists, err := h.directoryExists(ctx, rulePath)
		if err != nil {
			return nil, err
		}
		if !ruleTargetExists {
			continue
		}

		rootPath := topLevelWebDAVPath(rulePath)
		if rootPath == "" {
			continue
		}
		if _, exists := seen[rootPath]; exists {
			continue
		}
		info, err := h.fs.Stat(ctx, rootPath)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrNotDir) {
				continue
			}
			return nil, err
		}
		if !info.IsDir {
			continue
		}
		seen[rootPath] = struct{}{}
		children = append(children, info)
	}

	return children, nil
}

func (h *Handler) hasExistingReadableDescendantRule(ctx context.Context, scope requestScope, targetPath string) (bool, error) {
	for _, rulePath := range scope.readableDescendantRulePaths(targetPath) {
		ok, err := h.directoryExists(ctx, rulePath)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func (h *Handler) directoryExists(ctx context.Context, filePath string) (bool, error) {
	info, err := h.fs.Stat(ctx, filePath)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrNotDir) {
			return false, nil
		}
		return false, err
	}
	return info != nil && info.IsDir, nil
}

func cleanWebDAVAccessRulePath(rulePath string) string {
	return cleanWebDAVRuntimePolicyPath(rulePath)
}

func cleanWebDAVRuntimeClientPath(filePath string) string {
	if filePath == "" {
		filePath = "/"
	}
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}
	return cleanWebDAVRuntimeTargetPath(filePath)
}

func cleanWebDAVRuntimeTargetPath(filePath string) string {
	if filePath == "" || !strings.HasPrefix(filePath, "/") {
		return ""
	}
	if strings.Contains(filePath, "\\") || containsWebDAVPathControlCharacter(filePath) || hasDotSegment(filePath) {
		return ""
	}
	return cleanAbsoluteWebDAVPath(filePath)
}

func cleanWebDAVRuntimePolicyPath(filePath string) string {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return ""
	}
	if !strings.HasPrefix(filePath, "/") || strings.ContainsAny(filePath, "\\?#") || hasDotSegment(filePath) {
		return ""
	}
	if containsWebDAVPathControlCharacter(filePath) {
		return ""
	}
	return cleanAbsoluteWebDAVPath(filePath)
}

func topLevelWebDAVPath(filePath string) string {
	trimmed := strings.Trim(filePath, "/")
	if trimmed == "" {
		return ""
	}
	first, _, _ := strings.Cut(trimmed, "/")
	if first == "" {
		return ""
	}
	return "/" + first
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

	setUntrustedWebDAVContentHeaders(w.Header())
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
	href := h.webdavHref(ctx, filePath, isDir)
	setUntrustedWebDAVContentHeaders(w.Header())
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	fmt.Fprint(w, xml.Header)
	fmt.Fprint(w, `<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>`)
	_ = xml.EscapeText(w, []byte(href))
	fmt.Fprint(w, `</D:href>
    <D:propstat>
      <D:status>HTTP/1.1 200 OK</D:status>
    </D:propstat>
  </D:response>
</D:multistatus>`)
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

	setUntrustedWebDAVContentHeaders(w.Header())
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
	setUntrustedWebDAVContentHeaders(w.Header())
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
	if u.Host != "" && !webDAVHostMatches(u.Host, expectedHost, u.Scheme) {
		return "", false
	}
	escapedPath := u.EscapedPath()
	if !strings.HasPrefix(escapedPath, "/") {
		return "", false
	}
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", false
	}
	resolvedPath, ok := cleanDecodedAbsoluteWebDAVURLPath(decodedPath)
	if !ok {
		return "", false
	}
	resolvedPath, ok = trimWebDAVPrefix(resolvedPath, prefix)
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
	if h.writeLockAuthorized(r, requestPath, resourcePaths, namespacePaths, descendantRoots) {
		return true
	}
	http.Error(w, "resource is locked", http.StatusLocked)
	return false
}

func (h *Handler) writeLockAuthorized(r *http.Request, requestPath string, resourcePaths, namespacePaths, descendantRoots []string) bool {
	providedTokens := extractLockTokens(r, requestPath, h.prefix, scopePathMapper(r.Context()))

	h.locksMu.Lock()
	defer h.locksMu.Unlock()
	h.removeExpiredLocksLocked(time.Now())

	for lockPath, lockInfo := range h.locks {
		if !lockAffectsRequest(lockPath, lockInfo, resourcePaths, namespacePaths, descendantRoots) {
			continue
		}
		if !hasMatchingLockToken(providedTokens, lockInfo.token, lockPath, lockInfo) {
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
	removeExpiredWebDAVLocks(h.locks, now)
}

func removeExpiredWebDAVLocks(locks map[string]webdavLock, now time.Time) {
	for filePath, lockInfo := range locks {
		if !lockInfo.expiresAt.After(now) {
			delete(locks, filePath)
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
	if u.Host != "" && !webDAVHostMatches(u.Host, requestHost(r), u.Scheme) {
		return ""
	}
	escapedPath := u.EscapedPath()
	if !strings.HasPrefix(escapedPath, "/") {
		return ""
	}
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return ""
	}
	dstPath, ok := cleanDecodedAbsoluteWebDAVURLPath(decodedPath)
	if !ok {
		return ""
	}

	dstPath, ok = trimWebDAVPrefix(dstPath, h.prefix)
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

func webDAVHostMatches(uriHost, requestHost, uriScheme string) bool {
	uriHost = strings.TrimSpace(uriHost)
	requestHost = strings.TrimSpace(requestHost)
	if uriHost == "" || requestHost == "" {
		return false
	}

	uriName, uriPort, uriOK := splitWebDAVHostPort(uriHost)
	requestName, requestPort, requestOK := splitWebDAVHostPort(requestHost)
	if !uriOK || !requestOK || !strings.EqualFold(uriName, requestName) {
		return false
	}
	if uriPort != "" && requestPort != "" {
		return uriPort == requestPort
	}
	if strings.TrimSpace(uriScheme) == "" {
		return uriPort == "" && requestPort == ""
	}
	defaultPort := defaultWebDAVPort(uriScheme)
	if defaultPort == "" {
		return false
	}
	if uriPort != "" {
		return uriPort == defaultPort
	}
	if requestPort != "" {
		return requestPort == defaultPort
	}
	return true
}

func splitWebDAVHostPort(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	if host, port, err := net.SplitHostPort(value); err == nil {
		host, ok := normalizeWebDAVHostName(strings.Trim(host, "[]"))
		if !ok {
			return "", "", false
		}
		return host, port, true
	}
	if strings.HasPrefix(value, "[") {
		closing := strings.LastIndex(value, "]")
		if closing == len(value)-1 && closing > 0 {
			host, ok := normalizeWebDAVHostName(value[1:closing])
			if !ok {
				return "", "", false
			}
			return host, "", true
		}
		return "", "", false
	}
	if strings.Count(value, ":") == 0 {
		host, ok := normalizeWebDAVHostName(value)
		if !ok {
			return "", "", false
		}
		return host, "", true
	}
	if ip := net.ParseIP(value); ip != nil {
		return value, "", true
	}
	return "", "", false
}

func normalizeWebDAVHostName(host string) (string, bool) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", false
	}
	if strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
		if host == "" || strings.HasSuffix(host, ".") {
			return "", false
		}
	}
	return host, true
}

func defaultWebDAVPort(scheme string) string {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func (h *Handler) handleError(w http.ResponseWriter, err error) {
	var residualErr *storage.DeleteStageResidualError
	if errors.As(err, &residualErr) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if errors.Is(err, storage.ErrWriteRecoveryRequired) {
		http.Error(w, "storage write recovery is required", http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, storage.ErrWriteAtomicRenameUnsupported) {
		http.Error(w, "storage layout does not support atomic write for this target", http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, storage.ErrInsufficientStorage) {
		http.Error(w, "insufficient storage capacity for this write", http.StatusInsufficientStorage)
		return
	}
	if errors.Is(err, storage.ErrWriteBusy) {
		w.Header().Set("Retry-After", strconv.Itoa(webDAVWriteRetryAfterSeconds))
		http.Error(w, "write capacity is busy, retry later", http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, storage.ErrWriteConflict) {
		http.Error(w, "resource changed during write", http.StatusConflict)
		return
	}
	if errors.Is(err, errWebDAVPathAccessDenied) {
		http.Error(w, "path access denied", http.StatusForbidden)
		return
	}
	if errors.Is(err, errWebDAVQuotaExceeded) {
		http.Error(w, webDAVQuotaExceededMessage(err), http.StatusInsufficientStorage)
		return
	}
	if errors.Is(err, storage.ErrNotFound) {
		http.Error(w, "resource not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, storage.ErrIsDir) || errors.Is(err, storage.ErrNotDir) || errors.Is(err, storage.ErrNotRegular) {
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

func webDAVQuotaExceededMessage(err error) string {
	if errors.Is(err, errWebDAVDirectoryQuotaExceeded) {
		return errWebDAVDirectoryQuotaExceeded.Error()
	}
	if errors.Is(err, errWebDAVUserQuotaExceeded) {
		return errWebDAVUserQuotaExceeded.Error()
	}
	return errWebDAVQuotaExceeded.Error()
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
		if h.password == "" {
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
	case "none":
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

	username := strings.TrimSpace(identity.Username)
	if username == "" {
		return requestScope{}, errors.New("invalid user identity")
	}

	role := strings.ToLower(strings.TrimSpace(identity.Role))
	switch role {
	case webDAVRoleAdmin:
		return requestScope{
			username:   username,
			role:       role,
			groups:     append([]string(nil), identity.Groups...),
			quotaBytes: identity.QuotaBytes,
		}, nil
	case webDAVRoleUser, webDAVRoleGuest:
	default:
		return requestScope{}, errors.New("invalid user identity")
	}

	homeDir, ok := normalizeScopedHomeDir(identity.HomeDir)
	if !ok {
		return requestScope{}, errors.New("invalid user home directory")
	}
	return requestScope{
		username:    username,
		role:        role,
		groups:      append([]string(nil), identity.Groups...),
		homeDir:     homeDir,
		scoped:      true,
		readOnly:    role == webDAVRoleGuest,
		quotaBytes:  identity.QuotaBytes,
		accessRules: cloneWebDAVDirectoryAccessRules(h.directoryAccess),
	}, nil
}

// matchStrongETag applies the strong comparison required by If-Match.
func matchStrongETag(condition, current string) bool {
	return matchETagCondition(condition, current, func(candidate, current string) bool {
		return candidate == current && candidate[0] == '"'
	})
}

// matchWeakETag applies the weak comparison required by If-None-Match.
func matchWeakETag(condition, current string) bool {
	return matchETagCondition(condition, current, func(candidate, current string) bool {
		return strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(current, "W/")
	})
}

func matchETagCondition(condition, current string, equal func(candidate, current string) bool) bool {
	condition = textproto.TrimString(condition)
	if condition == "*" {
		return true
	}

	current, remain := scanEntityTag(current)
	if current == "" || textproto.TrimString(remain) != "" {
		return false
	}
	candidates, ok := parseEntityTagList(condition)
	if !ok {
		return false
	}
	for _, candidate := range candidates {
		if equal(candidate, current) {
			return true
		}
	}
	return false
}

func parseEntityTagList(value string) ([]string, bool) {
	tags := make([]string, 0, 1)
	value = textproto.TrimString(value)
	if value == "" {
		return nil, false
	}
	for {
		tag, remain := scanEntityTag(value)
		if tag == "" {
			return nil, false
		}
		tags = append(tags, tag)
		remain = textproto.TrimString(remain)
		if remain == "" {
			return tags, true
		}
		if remain[0] != ',' {
			return nil, false
		}
		value = textproto.TrimString(remain[1:])
		if value == "" || value[0] == ',' {
			return nil, false
		}
	}
}

// scanEntityTag consumes one RFC entity-tag and returns the remaining input.
func scanEntityTag(value string) (tag, remain string) {
	value = textproto.TrimString(value)
	start := 0
	if strings.HasPrefix(value, "W/") {
		start = 2
	}
	if len(value[start:]) < 2 || value[start] != '"' {
		return "", ""
	}
	for i := start + 1; i < len(value); i++ {
		character := value[i]
		switch {
		case character == 0x21 || character >= 0x23 && character <= 0x7e || character >= 0x80:
		case character == '"':
			return value[:i+1], value[i+1:]
		default:
			return "", ""
		}
	}
	return "", ""
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
