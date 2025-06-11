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
	errInvalidOverwriteHeader             = errors.New("invalid Overwrite header")
	errLockTokenMatchesRequestURI         = errors.New("lock token does not match request URI")
	errLockRefreshRequiresToken           = errors.New("LOCK refresh requires a matching lock token")
	errPreconditionFailed                 = errors.New("precondition failed")
	errFileAlreadyExists                  = errors.New("file already exists")
	errOverwriteDisabled                  = errors.New("destination exists and overwrite is disabled")
	errDestinationInsideSourceDirectory   = errors.New("destination cannot be inside source directory")
	errDirectoryCopyOverwriteNotSupported = errors.New("overwriting an existing destination with a directory copy is not supported")
)

const webdavLockTimeout = time.Hour
const webdavLockCleanupInterval = 5 * time.Minute
const webdavLockTokenBytes = 16
const maxWebDAVLockTokenAttempts = 8
const maxPropfindTraversalDepth = 64
const webdavLockDepthZero = "0"
const webdavLockDepthInfinity = "infinity"

var webdavRandomRead = rand.Read

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
	// REM-1 fix: Authentication
	authType string
	username string
	password string
}

// Config holds WebDAV handler configuration
type Config struct {
	FileSystem *storage.FileSystem
	Prefix     string // URL prefix, e.g., "/dav"
	ReadOnly   bool
	// REM-1 fix: Authentication configuration
	AuthType string // "none", "basic"
	Username string
	Password string
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
		authType:            strings.ToLower(cfg.AuthType),
		username:            cfg.Username,
		password:            cfg.Password,
	}
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
	// REM-1 fix: Check authentication
	if !h.checkAuth(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="MnemoNAS WebDAV"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Remove prefix to get file path
	filePath := strings.TrimPrefix(r.URL.Path, h.prefix)
	if filePath == "" {
		filePath = "/"
	}
	if hasTraversalSegment(filePath) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// Validate path to prevent path traversal attacks (C1 fix)
	filePath = path.Clean(filePath)
	if !path.IsAbs(filePath) {
		filePath = "/" + filePath
	}

	ctx := r.Context()

	// Check read-only mode
	if h.readOnly && !isReadMethod(r.Method) {
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
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			return
		}

		// Return directory listing (simple HTML)
		h.serveDirectory(ctx, w, r, filePath, info)
		return
	}

	etag := fmt.Sprintf(`"%s"`, info.ContentHash)

	// Check If-Match (precondition) before cache validators.
	if im := r.Header.Get("If-Match"); im != "" {
		if !h.matchETag(im, etag) {
			http.Error(w, errPreconditionFailed.Error(), http.StatusPreconditionFailed)
			return
		}
	}

	if ius := r.Header.Get("If-Unmodified-Since"); ius != "" {
		if unmodifiedSince, err := http.ParseTime(ius); err == nil && isHTTPTimeAfter(info.ModTime, unmodifiedSince) {
			http.Error(w, errPreconditionFailed.Error(), http.StatusPreconditionFailed)
			return
		}
	}

	// Check If-None-Match (conditional GET)
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		if h.matchETag(inm, etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	} else if ims := r.Header.Get("If-Modified-Since"); ims != "" {
		if modifiedSince, err := http.ParseTime(ims); err == nil && !isHTTPTimeAfter(info.ModTime, modifiedSince) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	// Read file
	reader, err := h.fs.OpenFile(ctx, filePath)
	if err != nil {
		h.handleError(w, err)
		return
	}
	defer reader.Close()

	// Set ETag header for caching
	w.Header().Set("ETag", etag)
	w.Header().Set("Accept-Ranges", "bytes")
	if contentType := fileContentType(filePath); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}

	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
		w.Header().Set("Last-Modified", info.ModTime.UTC().Format(http.TimeFormat))
		return
	}

	// Use http.ServeContent to handle Range requests automatically
	// Pass filename from path for Content-Disposition
	http.ServeContent(w, r, path.Base(filePath), info.ModTime, reader)
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

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Index of %s</title></head>
<body>
<h1>Index of %s</h1>
<hr>
<pre>
`, html.EscapeString(filePath), html.EscapeString(filePath))

	if filePath != "/" {
		parentHref := escapeDirectoryHref(path.Join(h.prefix, path.Dir(filePath)))
		fmt.Fprintf(w, `<a href="%s">..</a>
`, html.EscapeString(parentHref))
	}

	for _, child := range children {
		name := path.Base(child.Path)
		if child.IsDir {
			name += "/"
		}
		// V3-2 fix: Escape HTML to prevent XSS
		fmt.Fprintf(w, `<a href="%s">%s</a>    %s    %d
`,
			html.EscapeString(escapeDirectoryHref(path.Join(h.prefix, child.Path))),
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

func (h *Handler) webdavHref(filePath string, isDir bool) string {
	rawHref := path.Join(h.prefix, filePath)
	if isDir && !strings.HasSuffix(rawHref, "/") {
		rawHref += "/"
	}
	return escapeDirectoryHref(rawHref)
}

func (h *Handler) handlePut(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	releaseLocks := h.acquireHierarchyLocks(hierarchyLockSpec{path: filePath, write: true})
	defer releaseLocks()

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

	err := h.fs.WriteFile(ctx, filePath, r.Body)
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

	etag := fmt.Sprintf(`"%s"`, info.ContentHash)
	if im := r.Header.Get("If-Match"); im != "" {
		if !h.matchETag(im, etag) {
			http.Error(w, errPreconditionFailed.Error(), http.StatusPreconditionFailed)
			return
		}
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		if h.matchETag(inm, etag) {
			http.Error(w, errPreconditionFailed.Error(), http.StatusPreconditionFailed)
			return
		}
	}

	affectedPaths := []string{path.Dir(filePath), filePath}
	h.invalidatePropCache(affectedPaths...)

	if err := h.fs.Delete(ctx, filePath); err != nil {
		h.handleError(w, err)
		return
	}
	h.clearLocksUnderPath(filePath)

	h.invalidatePropCache(affectedPaths...)

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleMkcol(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	// MKCOL does not allow request body
	if requestHasBody(r) {
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
		h.handleError(w, err)
		return
	}

	h.invalidatePropCache(affectedPaths...)

	w.WriteHeader(http.StatusCreated)
}

func requestHasBody(r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return false
	}
	if r.ContentLength > 0 {
		return true
	}
	if r.ContentLength == 0 {
		return false
	}

	var probe [1]byte
	n, err := r.Body.Read(probe[:])
	if n > 0 {
		r.Body = prependReadCloser(bytes.NewReader(probe[:n]), r.Body)
		return true
	}
	return err != io.EOF
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

	affectedPaths := []string{path.Dir(dst), dst}
	h.invalidatePropCache(affectedPaths...)

	if err := h.copyResource(ctx, srcPath, dst, copyDepth); err != nil {
		if h.writeParentNotDirectoryConflict(w, err) {
			return
		}
		h.handleError(w, err)
		return
	}

	h.invalidatePropCache(affectedPaths...)

	if dstExists {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) copyResource(ctx context.Context, srcPath, dstPath, depth string) error {
	info, err := h.fs.Stat(ctx, srcPath)
	if err != nil {
		return err
	}

	if info.IsDir {
		if err := h.fs.Mkdir(ctx, dstPath); err != nil {
			return err
		}
		if depth == "0" {
			return nil
		}

		children, err := h.fs.ReadDir(ctx, srcPath)
		if err != nil {
			return h.rollbackCopiedDirectory(dstPath, err)
		}

		for _, child := range children {
			childName := path.Base(child.Path)
			if err := h.copyResource(ctx, child.Path, path.Join(dstPath, childName), "infinity"); err != nil {
				return h.rollbackCopiedDirectory(dstPath, err)
			}
		}
		return nil
	}

	reader, err := h.fs.OpenFile(ctx, srcPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	return h.fs.WriteFile(ctx, dstPath, reader)
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
		h.handleError(w, err)
		return
	}
	if srcInfo.IsDir {
		if err := h.validateInfinityOnlyDepth(r.Header.Get("Depth")); err != nil {
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
	releaseLocks := h.acquireHierarchyLocks(hierarchyLockSpec{path: filePath, write: false})
	defer releaseLocks()

	depth, err := h.parsePropfindDepth(r.Header.Get("Depth"))
	if err != nil {
		writeKnownWebDAVError(w, errInvalidDepthHeader, http.StatusBadRequest)
		return
	}

	// Check cache first
	if responses, ok := h.propCache.Get(filePath, depth); ok {
		h.writePropfindResponse(w, responses)
		return
	}
	generation := h.propCache.SnapshotGeneration()

	info, err := h.fs.Stat(ctx, filePath)
	if err != nil {
		h.handleError(w, err)
		return
	}

	var responses []propfindResponse

	// Add current resource
	responses = append(responses, h.propResponse(filePath, info))

	if err := h.appendPropfindChildren(ctx, filePath, info, depth, 0, &responses); err != nil {
		if errors.Is(err, errPropfindDepthLimitExceeded) {
			writeKnownWebDAVError(w, errPropfindDepthLimitExceeded, http.StatusForbidden)
			return
		}
		h.handleError(w, err)
		return
	}

	// Cache the result for large directories
	if len(responses) > 10 {
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
		*responses = append(*responses, h.propResponse(child.Path, child))
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

	switch strings.ToLower(depth) {
	case "0", "1", "infinity":
		return strings.ToLower(depth), nil
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

func (h *Handler) propResponse(filePath string, info *storage.FileInfo) propfindResponse {
	href := h.webdavHref(filePath, info.IsDir)

	props := propstat{
		Status: "HTTP/1.1 200 OK",
		Prop: prop{
			DisplayName:     path.Base(filePath),
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

	// Simple implementation: ignore property modification requests
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	fmt.Fprint(w, xml.Header)
	escapedHref := h.webdavHref(filePath, info.IsDir)
	fmt.Fprintf(w, `<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>%s</D:href>
    <D:propstat>
      <D:status>HTTP/1.1 200 OK</D:status>
    </D:propstat>
  </D:response>
</D:multistatus>`, escapedHref)
}

func (h *Handler) handleLock(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	releaseLocks := h.acquireHierarchyLocks(hierarchyLockSpec{path: filePath, write: false})
	defer releaseLocks()

	info, err := h.fs.Stat(ctx, filePath)
	if err != nil {
		h.handleError(w, err)
		return
	}
	hasBody := requestHasBody(r)
	providedTokens := extractLockTokens(r, filePath, h.prefix)
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
	releaseLocks := h.acquireHierarchyLocks(hierarchyLockSpec{path: filePath, write: false})
	defer releaseLocks()

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
	token string
	path  string
}

func extractLockTokens(r *http.Request, requestPath, prefix string) []providedLockToken {
	seen := make(map[string]struct{})
	tokens := make([]providedLockToken, 0, 2)

	appendToken := func(token, tokenPath string) {
		token = normalizeLockToken(token)
		if token == "" || tokenPath == "" {
			return
		}
		key := tokenPath + "\x00" + token
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		tokens = append(tokens, providedLockToken{token: token, path: tokenPath})
	}

	appendToken(r.Header.Get("Lock-Token"), requestPath)

	ifHeader := r.Header.Get("If")
	currentPath := requestPath
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
			if ok {
				currentPath = resolvedPath
			} else {
				currentPath = ""
			}
			ifHeader = ifHeader[uriEnd+1:]
		case '(':
			stateEnd := strings.IndexByte(ifHeader, ')')
			if stateEnd == -1 {
				return tokens
			}
			extractStateListLockTokens(ifHeader[1:stateEnd], currentPath, appendToken)
			ifHeader = ifHeader[stateEnd+1:]
		default:
			ifHeader = ifHeader[1:]
		}
	}

	return tokens
}

func extractStateListLockTokens(stateList, tokenPath string, appendToken func(token, tokenPath string)) {
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
				appendToken(stateList[1:tokenEnd], tokenPath)
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
	if prefix != "" {
		if resolvedPath != prefix && !strings.HasPrefix(resolvedPath, prefix+"/") {
			return "", false
		}
		resolvedPath = strings.TrimPrefix(resolvedPath, prefix)
		if resolvedPath == "" {
			resolvedPath = "/"
		}
	}
	return resolvedPath, true
}

func hasMatchingLockToken(providedTokens []providedLockToken, expected, lockPath string, lockInfo webdavLock) bool {
	for _, token := range providedTokens {
		if token.token != expected {
			continue
		}
		if lockScopeContainsPath(lockPath, lockInfo, token.path) {
			return true
		}
	}
	return false
}

func lockScopeContainsPath(lockPath string, lockInfo webdavLock, candidatePath string) bool {
	if candidatePath == lockPath {
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

func (h *Handler) authorizeWriteLockWithDescendants(w http.ResponseWriter, r *http.Request, requestPath string, descendantRoots []string, paths ...string) bool {
	return h.authorizeWriteLockWithScope(w, r, requestPath, paths, nil, descendantRoots)
}

func (h *Handler) authorizeWriteLockWithScope(w http.ResponseWriter, r *http.Request, requestPath string, resourcePaths, namespacePaths, descendantRoots []string) bool {
	providedTokens := extractLockTokens(r, requestPath, h.prefix)

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
	immediateParent := true
	for {
		lockInfo, exists := h.locks[current]
		if exists && hasMatchingLockToken(providedTokens, lockInfo.token, current, lockInfo) {
			if lockInfo.depth == webdavLockDepthInfinity || immediateParent {
				return current, lockInfo, true
			}
		}
		if current == "/" {
			break
		}
		current = path.Dir(current)
		immediateParent = false
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

func (h *Handler) lockCheckPathsWithDescendantsLocked(paths, descendantRoots []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(paths)*2)

	for _, filePath := range lockCheckPaths(paths...) {
		if _, ok := seen[filePath]; ok {
			continue
		}
		seen[filePath] = struct{}{}
		result = append(result, filePath)
	}

	for _, root := range descendantRoots {
		for lockPath := range h.locks {
			if !pathMatchesOrDescendant(root, lockPath) {
				continue
			}
			if _, ok := seen[lockPath]; ok {
				continue
			}
			seen[lockPath] = struct{}{}
			result = append(result, lockPath)
		}
	}

	return result
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
	h.locksMu.Lock()
	defer h.locksMu.Unlock()
	h.removeExpiredLocksLocked(time.Now())

	for lockPath := range h.locks {
		if pathMatchesOrDescendant(rootPath, lockPath) {
			delete(h.locks, lockPath)
		}
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

func lockCheckPaths(paths ...string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(paths)*2)

	for _, filePath := range paths {
		current := filePath
		for {
			if _, ok := seen[current]; !ok {
				seen[current] = struct{}{}
				result = append(result, current)
			}
			if current == "/" {
				break
			}
			current = path.Dir(current)
		}
	}

	return result
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

	// Parse URL properly (I6 fix)
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

	if h.prefix != "" && dstPath != h.prefix && !strings.HasPrefix(dstPath, h.prefix+"/") {
		return ""
	}

	return strings.TrimPrefix(dstPath, h.prefix)
}

func requestHost(r *http.Request) string {
	if r.Host != "" {
		return r.Host
	}
	return r.URL.Host
}

func hasTraversalSegment(rawPath string) bool {
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return true
		}
	}

	return false
}

func (h *Handler) handleError(w http.ResponseWriter, err error) {
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

// checkAuth verifies authentication based on configured auth type (REM-1 fix)
func (h *Handler) checkAuth(r *http.Request) bool {
	switch h.authType {
	case "basic":
		user, pass, ok := r.BasicAuth()
		if !ok {
			return false
		}
		// Constant-time comparison to prevent timing attacks
		userMatch := subtle.ConstantTimeCompare([]byte(user), []byte(h.username)) == 1
		passMatch := subtle.ConstantTimeCompare([]byte(pass), []byte(h.password)) == 1
		return userMatch && passMatch
	case "none", "":
		return true
	default:
		// Unknown auth type, deny by default
		return false
	}
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
