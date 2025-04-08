// Package webdav provides WebDAV protocol HTTP handler
// Implements RFC 4918 WebDAV standard
package webdav

import (
	"context"
	"crypto/subtle"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/seanbao/mnemonas/internal/storage"
)

var (
	errInvalidDepthHeader                 = errors.New("invalid Depth header")
	errInvalidOverwriteHeader             = errors.New("invalid Overwrite header")
	errPreconditionFailed                 = errors.New("precondition failed")
	errFileAlreadyExists                  = errors.New("file already exists")
	errOverwriteDisabled                  = errors.New("destination exists and overwrite is disabled")
	errDestinationInsideSourceDirectory   = errors.New("destination cannot be inside source directory")
	errDirectoryCopyOverwriteNotSupported = errors.New("overwriting an existing destination with a directory copy is not supported")
)

const webdavLockTimeout = time.Hour

type webdavLock struct {
	token     string
	expiresAt time.Time
}

// Handler is the WebDAV request handler
type Handler struct {
	fs        *storage.FileSystem
	prefix    string
	readOnly  bool
	pathLock  *PathLock
	propCache *PropfindCache
	locksMu   sync.Mutex
	locks     map[string]webdavLock
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
		fs:        cfg.FileSystem,
		prefix:    strings.TrimSuffix(cfg.Prefix, "/"),
		readOnly:  cfg.ReadOnly,
		pathLock:  NewPathLock(),
		propCache: NewPropfindCache(30*time.Second, 1000),
		locks:     make(map[string]webdavLock),
		authType:  strings.ToLower(cfg.AuthType),
		username:  cfg.Username,
		password:  cfg.Password,
	}
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
	// Acquire read lock for concurrent read protection
	h.pathLock.RLock(filePath)
	defer h.pathLock.RUnlock(filePath)

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
		parentHref := path.Join(h.prefix, path.Dir(filePath))
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
			html.EscapeString(path.Join(h.prefix, child.Path)),
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

func (h *Handler) handlePut(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	if !h.authorizeWriteLock(w, r, filePath) {
		return
	}

	// Acquire write lock for exclusive access
	h.pathLock.Lock(filePath)
	defer h.pathLock.Unlock(filePath)

	// Check if parent directory exists
	parent := path.Dir(filePath)
	if parent != "/" {
		parentInfo, err := h.fs.Stat(ctx, parent)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				http.Error(w, "parent directory not found", http.StatusConflict)
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

	// Check conditional headers for existing file
	existingInfo, statErr := h.fs.Stat(ctx, filePath)
	isCreate := false
	if statErr != nil {
		if errors.Is(statErr, storage.ErrNotFound) {
			isCreate = true
		} else {
			http.Error(w, "internal server error", http.StatusInternalServerError)
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

	err := h.fs.WriteFile(ctx, filePath, r.Body)
	if err != nil {
		h.handleError(w, err)
		return
	}

	// Invalidate cache for parent directory
	h.propCache.Invalidate(parent)

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
	if !h.authorizeWriteLock(w, r, filePath) {
		return
	}

	// Acquire write lock for exclusive access
	h.pathLock.Lock(filePath)
	defer h.pathLock.Unlock(filePath)

	if err := h.fs.Delete(ctx, filePath); err != nil {
		h.handleError(w, err)
		return
	}

	// Invalidate cache for parent directory
	h.propCache.Invalidate(path.Dir(filePath))

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleMkcol(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	// MKCOL does not allow request body
	if requestHasBody(r) {
		http.Error(w, "MKCOL does not allow request body", http.StatusUnsupportedMediaType)
		return
	}
	if !h.authorizeWriteLock(w, r, filePath) {
		return
	}

	if err := h.fs.Mkdir(ctx, filePath); err != nil {
		h.handleError(w, err)
		return
	}

	// Invalidate cache for parent directory
	h.propCache.Invalidate(path.Dir(filePath))

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
		return true
	}
	return err != io.EOF
}

func (h *Handler) handleCopy(ctx context.Context, w http.ResponseWriter, r *http.Request, srcPath string) {
	dst := h.getDestination(r)
	if dst == "" {
		http.Error(w, "missing Destination header", http.StatusBadRequest)
		return
	}
	if srcPath == dst {
		http.Error(w, "source and destination must differ", http.StatusConflict)
		return
	}
	if !h.authorizeWriteLock(w, r, srcPath, dst) {
		return
	}

	dstExists := h.destinationExists(ctx, dst)

	if err := h.checkOverwriteHeader(ctx, r, dst); err != nil {
		if h.writeExpectedWebDAVError(w, err, http.StatusPreconditionFailed, errInvalidOverwriteHeader, errOverwriteDisabled) {
			return
		}
		h.handleError(w, err)
		return
	}

	// NEW-3 fix: Acquire locks in deterministic order to avoid deadlock (same as MOVE)
	first, second := srcPath, dst
	if first > second {
		first, second = second, first
	}

	// First path gets read lock if it's source, write lock if destination
	if first == srcPath {
		h.pathLock.RLock(first)
		defer h.pathLock.RUnlock(first)
	} else {
		h.pathLock.Lock(first)
		defer h.pathLock.Unlock(first)
	}

	if first != second {
		if second == srcPath {
			h.pathLock.RLock(second)
			defer h.pathLock.RUnlock(second)
		} else {
			h.pathLock.Lock(second)
			defer h.pathLock.Unlock(second)
		}
	}

	if err := h.rejectDirectoryDescendantDestination(ctx, srcPath, dst); err != nil {
		if h.writeExpectedWebDAVError(w, err, http.StatusConflict, errDestinationInsideSourceDirectory) {
			return
		}
		h.handleError(w, err)
		return
	}
	if err := h.rejectDirectoryCopyOverwrite(ctx, srcPath, dst, dstExists); err != nil {
		if h.writeExpectedWebDAVError(w, err, http.StatusConflict, errDirectoryCopyOverwriteNotSupported) {
			return
		}
		h.handleError(w, err)
		return
	}

	if err := h.copyResource(ctx, srcPath, dst); err != nil {
		h.handleError(w, err)
		return
	}

	// Invalidate cache for destination parent and destination path.
	h.propCache.Invalidate(path.Dir(dst))
	h.propCache.Invalidate(dst)

	if dstExists {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) copyResource(ctx context.Context, srcPath, dstPath string) error {
	info, err := h.fs.Stat(ctx, srcPath)
	if err != nil {
		return err
	}

	if info.IsDir {
		if err := h.fs.Mkdir(ctx, dstPath); err != nil {
			return err
		}

		children, err := h.fs.ReadDir(ctx, srcPath)
		if err != nil {
			return h.rollbackCopiedDirectory(dstPath, err)
		}

		for _, child := range children {
			childName := path.Base(child.Path)
			if err := h.copyResource(ctx, child.Path, path.Join(dstPath, childName)); err != nil {
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
		http.Error(w, "source and destination must differ", http.StatusConflict)
		return
	}
	if !h.authorizeWriteLock(w, r, srcPath, dst) {
		return
	}

	dstExists := h.destinationExists(ctx, dst)

	if err := h.checkOverwriteHeader(ctx, r, dst); err != nil {
		if h.writeExpectedWebDAVError(w, err, http.StatusPreconditionFailed, errInvalidOverwriteHeader, errOverwriteDisabled) {
			return
		}
		h.handleError(w, err)
		return
	}

	// Acquire locks for both paths (in deterministic order to avoid deadlock)
	first, second := srcPath, dst
	if first > second {
		first, second = second, first
	}
	h.pathLock.Lock(first)
	defer h.pathLock.Unlock(first)
	if first != second {
		h.pathLock.Lock(second)
		defer h.pathLock.Unlock(second)
	}

	if err := h.rejectDirectoryDescendantDestination(ctx, srcPath, dst); err != nil {
		if h.writeExpectedWebDAVError(w, err, http.StatusConflict, errDestinationInsideSourceDirectory) {
			return
		}
		h.handleError(w, err)
		return
	}

	if dstExists {
		backupPath, err := h.allocateMoveBackupPath(ctx, dst)
		if err != nil {
			h.handleError(w, err)
			return
		}
		if err := h.fs.Rename(ctx, dst, backupPath); err != nil {
			h.handleError(w, err)
			return
		}

		if err := h.fs.Rename(ctx, srcPath, dst); err != nil {
			if restoreErr := h.fs.Rename(ctx, backupPath, dst); restoreErr != nil {
				h.handleError(w, errors.Join(err, fmt.Errorf("failed to restore overwritten destination: %w", restoreErr)))
				return
			}
			h.handleError(w, err)
			return
		}

		if err := h.fs.PermanentDelete(ctx, backupPath); err != nil {
			h.handleError(w, err)
			return
		}
	} else {
		if err := h.fs.Rename(ctx, srcPath, dst); err != nil {
			h.handleError(w, err)
			return
		}
	}

	// Invalidate cache for both source and destination parents
	h.propCache.Invalidate(path.Dir(srcPath))
	h.propCache.Invalidate(path.Dir(dst))

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

	// Check cache first
	if responses, ok := h.propCache.Get(filePath, depth); ok {
		h.writePropfindResponse(w, responses)
		return
	}

	info, err := h.fs.Stat(ctx, filePath)
	if err != nil {
		h.handleError(w, err)
		return
	}

	var responses []propfindResponse

	// Add current resource
	responses = append(responses, h.propResponse(filePath, info))

	if err := h.appendPropfindChildren(ctx, filePath, info, depth, &responses); err != nil {
		h.handleError(w, err)
		return
	}

	// Cache the result for large directories
	if len(responses) > 10 {
		h.propCache.Set(filePath, depth, responses)
	}

	h.writePropfindResponse(w, responses)
}

func (h *Handler) appendPropfindChildren(ctx context.Context, filePath string, info *storage.FileInfo, depth string, responses *[]propfindResponse) error {
	if !info.IsDir || depth == "0" {
		return nil
	}

	children, err := h.fs.ReadDir(ctx, filePath)
	if err != nil {
		return err
	}

	for _, child := range children {
		*responses = append(*responses, h.propResponse(child.Path, child))
		if depth == "infinity" {
			if err := h.appendPropfindChildren(ctx, child.Path, child, depth, responses); err != nil {
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
	href := path.Join(h.prefix, filePath)
	if info.IsDir && !strings.HasSuffix(href, "/") {
		href += "/"
	}

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
	if !h.authorizeWriteLock(w, r, filePath) {
		return
	}

	if _, err := h.fs.Stat(ctx, filePath); err != nil {
		h.handleError(w, err)
		return
	}

	// Simple implementation: ignore property modification requests
	w.WriteHeader(http.StatusMultiStatus)
	fmt.Fprint(w, xml.Header)
	fmt.Fprintf(w, `<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>%s</D:href>
    <D:propstat>
      <D:status>HTTP/1.1 200 OK</D:status>
    </D:propstat>
  </D:response>
</D:multistatus>`, path.Join(h.prefix, filePath))
}

func (h *Handler) handleLock(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	if _, err := h.fs.Stat(ctx, filePath); err != nil {
		h.handleError(w, err)
		return
	}

	h.locksMu.Lock()
	h.removeExpiredLocksLocked(time.Now())
	if existing, exists := h.locks[filePath]; exists {
		if hasMatchingLockToken(extractLockTokens(r), existing.token) {
			existing.expiresAt = time.Now().Add(webdavLockTimeout)
			h.locks[filePath] = existing
			h.locksMu.Unlock()
			h.writeLockResponse(w, existing.token)
			return
		}
		h.locksMu.Unlock()
		http.Error(w, "resource is locked", http.StatusLocked)
		return
	}

	// Simple implementation: return virtual lock
	token := fmt.Sprintf("opaquelocktoken:%d", time.Now().UnixNano())
	h.locks[filePath] = webdavLock{
		token:     token,
		expiresAt: time.Now().Add(webdavLockTimeout),
	}
	h.locksMu.Unlock()

	h.writeLockResponse(w, token)
}

func (h *Handler) writeLockResponse(w http.ResponseWriter, token string) {
	w.Header().Set("Lock-Token", "<"+token+">")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	fmt.Fprint(w, xml.Header)
	fmt.Fprintf(w, `<D:prop xmlns:D="DAV:">
  <D:lockdiscovery>
    <D:activelock>
      <D:locktype><D:write/></D:locktype>
      <D:lockscope><D:exclusive/></D:lockscope>
      <D:depth>infinity</D:depth>
      <D:timeout>Second-3600</D:timeout>
      <D:locktoken><D:href>%s</D:href></D:locktoken>
    </D:activelock>
  </D:lockdiscovery>
</D:prop>`, token)
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

func extractLockTokens(r *http.Request) []string {
	seen := make(map[string]struct{})
	tokens := make([]string, 0, 2)

	appendToken := func(token string) {
		token = normalizeLockToken(token)
		if token == "" {
			return
		}
		if _, exists := seen[token]; exists {
			return
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}

	appendToken(r.Header.Get("Lock-Token"))

	ifHeader := r.Header.Get("If")
	for {
		start := strings.IndexByte(ifHeader, '<')
		if start == -1 {
			break
		}
		ifHeader = ifHeader[start+1:]

		end := strings.IndexByte(ifHeader, '>')
		if end == -1 {
			break
		}

		appendToken(ifHeader[:end])
		ifHeader = ifHeader[end+1:]
	}

	return tokens
}

func hasMatchingLockToken(providedTokens []string, expected string) bool {
	for _, token := range providedTokens {
		if token == expected {
			return true
		}
	}
	return false
}

func (h *Handler) authorizeWriteLock(w http.ResponseWriter, r *http.Request, paths ...string) bool {
	providedTokens := extractLockTokens(r)

	h.locksMu.Lock()
	defer h.locksMu.Unlock()
	h.removeExpiredLocksLocked(time.Now())

	for _, filePath := range lockCheckPaths(paths...) {
		lockInfo, locked := h.locks[filePath]
		if !locked {
			continue
		}
		if !hasMatchingLockToken(providedTokens, lockInfo.token) {
			http.Error(w, "resource is locked", http.StatusLocked)
			return false
		}
	}

	return true
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
	if hasTraversalSegment(u.Path) {
		return ""
	}

	// Get path and clean it
	dstPath := path.Clean(u.Path)
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
	for _, segment := range strings.Split(rawPath, "/") {
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
