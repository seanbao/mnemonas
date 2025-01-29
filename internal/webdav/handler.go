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
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/seanbao/mnemonas/internal/storage"
)

// Handler is the WebDAV request handler
type Handler struct {
	fs        *storage.FileSystem
	prefix    string
	readOnly  bool
	pathLock  *PathLock
	propCache *PropfindCache
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

	// Validate path to prevent path traversal attacks (C1 fix)
	filePath = path.Clean(filePath)
	if !path.IsAbs(filePath) {
		filePath = "/" + filePath
	}
	// Reject any path containing .. after cleaning
	if strings.Contains(filePath, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
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
		// Return directory listing (simple HTML)
		h.serveDirectory(ctx, w, r, filePath, info)
		return
	}

	etag := fmt.Sprintf(`"%s"`, info.ContentHash)

	// Check If-None-Match (conditional GET)
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		if h.matchETag(inm, etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	// Check If-Match (precondition)
	if im := r.Header.Get("If-Match"); im != "" {
		if !h.matchETag(im, etag) {
			http.Error(w, "precondition failed", http.StatusPreconditionFailed)
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

	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
		w.Header().Set("Last-Modified", info.ModTime.UTC().Format(http.TimeFormat))
		return
	}

	// Use http.ServeContent to handle Range requests automatically
	// Pass filename from path for Content-Disposition
	http.ServeContent(w, r, path.Base(filePath), info.ModTime, reader)
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
		fmt.Fprintf(w, `<a href="%s">..</a>
`, html.EscapeString(path.Dir(filePath)))
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
	// Acquire write lock for exclusive access
	h.pathLock.Lock(filePath)
	defer h.pathLock.Unlock(filePath)

	// Check if parent directory exists
	parent := path.Dir(filePath)
	if parent != "/" {
		parentInfo, err := h.fs.Stat(ctx, parent)
		if err != nil {
			http.Error(w, "parent directory not found", http.StatusConflict)
			return
		}
		if !parentInfo.IsDir {
			http.Error(w, "parent path is not a directory", http.StatusConflict)
			return
		}
	}

	// Check conditional headers for existing file
	existingInfo, statErr := h.fs.Stat(ctx, filePath)
	isCreate := statErr != nil

	if !isCreate {
		etag := fmt.Sprintf(`"%s"`, existingInfo.ContentHash)

		// If-Match: only update if ETag matches (prevent overwrite conflicts)
		if im := r.Header.Get("If-Match"); im != "" {
			if !h.matchETag(im, etag) {
				http.Error(w, "precondition failed", http.StatusPreconditionFailed)
				return
			}
		}

		// If-None-Match: prevent update if file exists (for create-only)
		if inm := r.Header.Get("If-None-Match"); inm == "*" {
			http.Error(w, "file already exists", http.StatusPreconditionFailed)
			return
		}
	} else {
		// If-Match with non-existent file should fail
		if im := r.Header.Get("If-Match"); im != "" && im != "*" {
			http.Error(w, "precondition failed", http.StatusPreconditionFailed)
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
	if r.ContentLength > 0 {
		http.Error(w, "MKCOL does not allow request body", http.StatusUnsupportedMediaType)
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

func (h *Handler) handleCopy(ctx context.Context, w http.ResponseWriter, r *http.Request, srcPath string) {
	dst := h.getDestination(r)
	if dst == "" {
		http.Error(w, "missing Destination header", http.StatusBadRequest)
		return
	}

	if err := h.checkOverwriteHeader(ctx, r, dst); err != nil {
		http.Error(w, err.Error(), http.StatusPreconditionFailed)
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

	// Simple implementation: read source file, write to destination
	reader, err := h.fs.OpenFile(ctx, srcPath)
	if err != nil {
		h.handleError(w, err)
		return
	}
	defer reader.Close()

	if err := h.fs.WriteFile(ctx, dst, reader); err != nil {
		h.handleError(w, err)
		return
	}

	// Invalidate cache for destination parent
	h.propCache.Invalidate(path.Dir(dst))

	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) handleMove(ctx context.Context, w http.ResponseWriter, r *http.Request, srcPath string) {
	dst := h.getDestination(r)
	if dst == "" {
		http.Error(w, "missing Destination header", http.StatusBadRequest)
		return
	}

	if err := h.checkOverwriteHeader(ctx, r, dst); err != nil {
		http.Error(w, err.Error(), http.StatusPreconditionFailed)
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

	if err := h.fs.Rename(ctx, srcPath, dst); err != nil {
		h.handleError(w, err)
		return
	}

	// Invalidate cache for both source and destination parents
	h.propCache.Invalidate(path.Dir(srcPath))
	h.propCache.Invalidate(path.Dir(dst))

	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) checkOverwriteHeader(ctx context.Context, r *http.Request, dst string) error {
	overwrite := r.Header.Get("Overwrite")
	if overwrite == "" || strings.EqualFold(overwrite, "T") {
		return nil
	}
	if !strings.EqualFold(overwrite, "F") {
		return errors.New("invalid Overwrite header")
	}

	if _, err := h.fs.Stat(ctx, dst); err == nil {
		return errors.New("destination exists and overwrite is disabled")
	} else if !errors.Is(err, storage.ErrNotFound) {
		return err
	}

	return nil
}

func (h *Handler) handlePropfind(ctx context.Context, w http.ResponseWriter, r *http.Request, filePath string) {
	depth := r.Header.Get("Depth")
	if depth == "" {
		depth = "infinity"
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

	// If directory and depth is not 0, add child resources
	if info.IsDir && depth != "0" {
		children, err := h.fs.ReadDir(ctx, filePath)
		if err == nil {
			for _, child := range children {
				responses = append(responses, h.propResponse(child.Path, child))
			}
		}
	}

	// Cache the result for large directories
	if len(responses) > 10 {
		h.propCache.Set(filePath, depth, responses)
	}

	h.writePropfindResponse(w, responses)
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

	// Simple implementation: return virtual lock
	token := fmt.Sprintf("opaquelocktoken:%d", time.Now().UnixNano())

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

	if r.Header.Get("Lock-Token") == "" {
		http.Error(w, "missing lock token", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
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

	// Get path and clean it
	dstPath := path.Clean(u.Path)
	if !path.IsAbs(dstPath) {
		dstPath = "/" + dstPath
	}

	// Validate - reject path traversal
	if strings.Contains(dstPath, "..") {
		return ""
	}
	if h.prefix != "" && dstPath != h.prefix && !strings.HasPrefix(dstPath, h.prefix+"/") {
		return ""
	}

	return strings.TrimPrefix(dstPath, h.prefix)
}

func (h *Handler) handleError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrNotFound) {
		http.Error(w, "resource not found", http.StatusNotFound)
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
