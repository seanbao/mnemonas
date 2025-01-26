package share

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/storage"
)

// FileReader combines read and close capabilities
type FileReader interface {
	io.Reader
	io.Closer
}

// FileOpener interface for opening files
type FileOpener interface {
	OpenFile(ctx context.Context, filePath string) (FileReader, error)
}

type FileStatProvider interface {
	Stat(ctx context.Context, filePath string) (*storage.FileInfo, error)
	ReadDir(ctx context.Context, filePath string) ([]*storage.FileInfo, error)
}

// Handler provides HTTP handlers for share operations
type Handler struct {
	store   *ShareStore
	fs      FileOpener
	baseURL string
}

const shareAccessCookiePrefix = "mnemonas_share_"

// NewHandler creates a new share handler
// fs can be nil if file download is not needed
func NewHandler(store *ShareStore, fs FileOpener) *Handler {
	return &Handler{
		store: store,
		fs:    fs,
	}
}

// SetBaseURL sets the base URL for share links
func (h *Handler) SetBaseURL(baseURL string) {
	baseURL = strings.TrimSpace(baseURL)
	h.baseURL = strings.TrimRight(baseURL, "/")
}

// Routes registers share routes (requires auth)
func (h *Handler) Routes(r chi.Router) {
	r.Get("/", h.ListShares)
	r.Post("/", h.CreateShare)
	r.Get("/{id}", h.GetShare)
	r.Put("/{id}", h.UpdateShare)
	r.Delete("/{id}", h.DeleteShare)
}

// PublicRoutes registers routes for public share access
func (h *Handler) PublicRoutes(r chi.Router) {
	r.Get("/{id}", h.AccessShare)
	r.Post("/{id}", h.AccessShareWithPassword)
	r.Get("/{id}/items", h.ListShareItems)
	r.Get("/{id}/download", h.DownloadShare)
	r.Get("/{id}/download/*", h.DownloadShareFile)
}

// CreateShareRequest represents a share creation request
type CreateShareRequest struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	ExpiresIn   string `json:"expires_in,omitempty"`
	Password    string `json:"password,omitempty"`
	Permission  string `json:"permission,omitempty"`
	MaxAccess   int64  `json:"max_access,omitempty"`
	Description string `json:"description,omitempty"`
}

// CreateShare creates a new share link
func (h *Handler) CreateShare(w http.ResponseWriter, r *http.Request) {
	var req CreateShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	userID := getUserIDFromContext(r.Context())

	opts := CreateShareOptions{
		Path:        req.Path,
		CreatedBy:   userID,
		Password:    req.Password,
		MaxAccess:   req.MaxAccess,
		Description: req.Description,
	}

	switch req.Type {
	case "folder":
		opts.Type = ShareTypeFolder
	default:
		opts.Type = ShareTypeFile
	}

	if req.ExpiresIn != "" {
		duration, err := parseDuration(req.ExpiresIn)
		if err != nil {
			http.Error(w, "invalid expires_in format", http.StatusBadRequest)
			return
		}
		opts.ExpiresIn = &duration
	}

	switch req.Permission {
	case "read_write":
		opts.Permission = PermissionReadWrite
	default:
		opts.Permission = PermissionRead
	}

	share, err := h.store.Create(opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	info := share.ToInfo()
	info.URL = h.buildShareURL(share.ID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(info)
}

// ListShares lists shares for the current user
func (h *Handler) ListShares(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	isAdmin := getIsAdminFromContext(r.Context())

	var shares []*Share
	if isAdmin && r.URL.Query().Get("all") == "true" {
		shares = h.store.ListAll()
	} else {
		shares = h.store.ListByUser(userID)
	}

	infos := make([]*ShareInfo, len(shares))
	for i, s := range shares {
		infos[i] = s.ToInfo()
		infos[i].URL = h.buildShareURL(s.ID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(infos)
}

// GetShare gets a share by ID
func (h *Handler) GetShare(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	share, err := h.store.Get(id)
	if err != nil {
		if errors.Is(err, ErrShareNotFound) {
			http.Error(w, "share not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID := getUserIDFromContext(r.Context())
	isAdmin := getIsAdminFromContext(r.Context())
	if share.CreatedBy != userID && !isAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	info := share.ToInfo()
	info.URL = h.buildShareURL(share.ID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// UpdateShareRequest represents a share update request
type UpdateShareRequest struct {
	Enabled     *bool   `json:"enabled,omitempty"`
	ExpiresIn   *string `json:"expires_in,omitempty"`
	Password    *string `json:"password,omitempty"`
	Permission  *string `json:"permission,omitempty"`
	MaxAccess   *int64  `json:"max_access,omitempty"`
	Description *string `json:"description,omitempty"`
}

// UpdateShare updates a share
func (h *Handler) UpdateShare(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req UpdateShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	share, err := h.store.Get(id)
	if err != nil {
		if errors.Is(err, ErrShareNotFound) {
			http.Error(w, "share not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID := getUserIDFromContext(r.Context())
	isAdmin := getIsAdminFromContext(r.Context())
	if share.CreatedBy != userID && !isAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	err = h.store.Update(id, func(s *Share) error {
		if req.Enabled != nil {
			s.Enabled = *req.Enabled
		}
		if req.ExpiresIn != nil {
			if *req.ExpiresIn == "" {
				s.ExpiresAt = nil
			} else {
				duration, err := parseDuration(*req.ExpiresIn)
				if err != nil {
					return err
				}
				exp := time.Now().Add(duration)
				s.ExpiresAt = &exp
			}
		}
		if req.Password != nil {
			if *req.Password == "" {
				s.PasswordHash = ""
			} else {
				hash, err := hashPassword(*req.Password)
				if err != nil {
					return err
				}
				s.PasswordHash = hash
			}
		}
		if req.Permission != nil {
			switch *req.Permission {
			case "read_write":
				s.Permission = PermissionReadWrite
			default:
				s.Permission = PermissionRead
			}
		}
		if req.MaxAccess != nil {
			s.MaxAccess = *req.MaxAccess
		}
		if req.Description != nil {
			s.Description = *req.Description
		}
		return nil
	})

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	share, _ = h.store.Get(id)
	info := share.ToInfo()
	info.URL = h.buildShareURL(share.ID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// DeleteShare deletes a share
func (h *Handler) DeleteShare(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	share, err := h.store.Get(id)
	if err != nil {
		if errors.Is(err, ErrShareNotFound) {
			http.Error(w, "share not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID := getUserIDFromContext(r.Context())
	isAdmin := getIsAdminFromContext(r.Context())
	if share.CreatedBy != userID && !isAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := h.store.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// PublicShareInfo is the info returned for public share access
type PublicShareInfo struct {
	ID          string     `json:"id"`
	Type        ShareType  `json:"type"`
	HasPassword bool       `json:"has_password"`
	Permission  Permission `json:"permission"`
	Description string     `json:"description,omitempty"`
	FileName    string     `json:"file_name,omitempty"`
	FileSize    int64      `json:"file_size,omitempty"`
	FolderItems int        `json:"folder_items,omitempty"`
}

// PublicShareItem represents a single file item in a shared folder.
type PublicShareItem struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// PublicShareListResponse represents the folder listing for a shared folder.
type PublicShareListResponse struct {
	Path  string             `json:"path"`
	Items []*PublicShareItem `json:"items"`
}

// AccessShare handles public share access
func (h *Handler) AccessShare(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	share, err := h.store.Get(id)
	if err != nil {
		if errors.Is(err, ErrShareNotFound) {
			http.Error(w, "share not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if share.HasPassword() {
		if h.hasShareAccess(r, share) {
			info := &PublicShareInfo{
				ID:          share.ID,
				Type:        share.Type,
				HasPassword: share.HasPassword(),
				Permission:  share.Permission,
				Description: share.Description,
			}

			h.enrichPublicShareInfo(r.Context(), info, share)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(info)
			return
		}

		if err := share.CanAccess(); err != nil {
			switch {
			case errors.Is(err, ErrShareAccessLimit):
				http.Error(w, "share access limit reached", http.StatusGone)
			case errors.Is(err, ErrShareExpired), errors.Is(err, ErrShareDisabled):
				http.Error(w, err.Error(), http.StatusGone)
			default:
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}

		info := &PublicShareInfo{
			ID:          share.ID,
			Type:        share.Type,
			HasPassword: share.HasPassword(),
			Permission:  share.Permission,
			Description: share.Description,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(info)
		return
	}

	if err := share.CanAccess(); err != nil {
		switch {
		case errors.Is(err, ErrShareAccessLimit):
			http.Error(w, "share access limit reached", http.StatusGone)
		case errors.Is(err, ErrShareExpired), errors.Is(err, ErrShareDisabled):
			http.Error(w, err.Error(), http.StatusGone)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	info := &PublicShareInfo{
		ID:          share.ID,
		Type:        share.Type,
		HasPassword: share.HasPassword(),
		Permission:  share.Permission,
		Description: share.Description,
	}

	h.enrichPublicShareInfo(r.Context(), info, share)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// AccessShareRequest is the request for password-protected share access
type AccessShareRequest struct {
	Password string `json:"password"`
}

// AccessShareWithPassword validates password and returns share details
func (h *Handler) AccessShareWithPassword(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req AccessShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	share, err := h.store.Get(id)
	if err != nil {
		if errors.Is(err, ErrShareNotFound) {
			http.Error(w, "share not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := share.CanAccess(); err != nil {
		switch {
		case errors.Is(err, ErrShareAccessLimit):
			http.Error(w, "share access limit reached", http.StatusGone)
		case errors.Is(err, ErrShareExpired), errors.Is(err, ErrShareDisabled):
			http.Error(w, err.Error(), http.StatusGone)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if share.HasPassword() && !share.CheckPassword(req.Password) {
		http.Error(w, "invalid password", http.StatusUnauthorized)
		return
	}

	h.setShareAccessCookie(w, r, share)

	info := &PublicShareInfo{
		ID:          share.ID,
		Type:        share.Type,
		HasPassword: share.HasPassword(),
		Permission:  share.Permission,
		Description: share.Description,
	}
	h.enrichPublicShareInfo(r.Context(), info, share)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// DownloadShare handles file download for shares
func (h *Handler) DownloadShare(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	share, err := h.accessAuthorizedShare(r, id)
	if err != nil {
		switch {
		case errors.Is(err, ErrShareNotFound):
			http.Error(w, "share not found", http.StatusNotFound)
		case errors.Is(err, ErrInvalidPassword):
			http.Error(w, "password required", http.StatusUnauthorized)
		case errors.Is(err, ErrShareAccessLimit):
			http.Error(w, "share access limit reached", http.StatusGone)
		case errors.Is(err, ErrShareExpired), errors.Is(err, ErrShareDisabled):
			http.Error(w, err.Error(), http.StatusGone)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if share.Type != ShareTypeFile {
		http.Error(w, "use /download/{path} for folders", http.StatusBadRequest)
		return
	}

	if h.fs == nil {
		http.Error(w, "filesystem not available", http.StatusServiceUnavailable)
		return
	}

	reader, err := h.fs.OpenFile(r.Context(), share.Path)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if reader == nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	defer reader.Close()

	filename := path.Base(share.Path)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("Content-Type", "application/octet-stream")
	io.Copy(w, reader)
}

// DownloadShareFile handles file download from shared folder
func (h *Handler) DownloadShareFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	filePath := chi.URLParam(r, "*")
	if filePath == "" {
		prefix := "/s/" + id + "/download/"
		if strings.HasPrefix(r.URL.Path, prefix) {
			filePath = strings.TrimPrefix(r.URL.Path, prefix)
		}
	}
	filePath = strings.TrimPrefix(filePath, "/")

	share, err := h.accessAuthorizedShare(r, id)
	if err != nil {
		switch {
		case errors.Is(err, ErrShareNotFound):
			http.Error(w, "share not found", http.StatusNotFound)
		case errors.Is(err, ErrInvalidPassword):
			http.Error(w, "password required", http.StatusUnauthorized)
		case errors.Is(err, ErrShareAccessLimit):
			http.Error(w, "share access limit reached", http.StatusGone)
		case errors.Is(err, ErrShareExpired), errors.Is(err, ErrShareDisabled):
			http.Error(w, err.Error(), http.StatusGone)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if share.Type != ShareTypeFolder {
		http.Error(w, "share is not a folder", http.StatusBadRequest)
		return
	}

	if filePath == "" || filePath == "." {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	fullPath := path.Join(share.Path, filePath)
	if !isWithinSharePath(share.Path, fullPath) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	if h.fs == nil {
		http.Error(w, "filesystem not available", http.StatusServiceUnavailable)
		return
	}

	reader, err := h.fs.OpenFile(r.Context(), fullPath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if reader == nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	defer reader.Close()

	filename := path.Base(fullPath)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("Content-Type", "application/octet-stream")
	io.Copy(w, reader)
}

// ListShareItems lists items within a shared folder.
func (h *Handler) ListShareItems(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	relPath := r.URL.Query().Get("path")
	relPath = strings.TrimPrefix(relPath, "/")
	if relPath == "." {
		relPath = ""
	}

	share, err := h.accessAuthorizedShare(r, id)
	if err != nil {
		switch {
		case errors.Is(err, ErrShareNotFound):
			http.Error(w, "share not found", http.StatusNotFound)
		case errors.Is(err, ErrInvalidPassword):
			http.Error(w, "password required", http.StatusUnauthorized)
		case errors.Is(err, ErrShareAccessLimit):
			http.Error(w, "share access limit reached", http.StatusGone)
		case errors.Is(err, ErrShareExpired), errors.Is(err, ErrShareDisabled):
			http.Error(w, err.Error(), http.StatusGone)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if share.Type != ShareTypeFolder {
		http.Error(w, "share is not a folder", http.StatusBadRequest)
		return
	}

	statProvider, ok := h.fs.(FileStatProvider)
	if !ok {
		http.Error(w, "filesystem not available", http.StatusServiceUnavailable)
		return
	}

	fullPath := share.Path
	if relPath != "" {
		fullPath = path.Join(share.Path, relPath)
	}
	if !isWithinSharePath(share.Path, fullPath) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	entries, err := statProvider.ReadDir(r.Context(), fullPath)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]*PublicShareItem, 0, len(entries))
	for _, entry := range entries {
		relItemPath, relErr := shareRelativePath(share.Path, entry.Path)
		if relErr != nil || relItemPath == "." {
			relItemPath = entry.Name
		}
		items = append(items, &PublicShareItem{
			Name:    entry.Name,
			Path:    relItemPath,
			IsDir:   entry.IsDir,
			Size:    entry.Size,
			ModTime: entry.ModTime,
		})
	}

	resp := &PublicShareListResponse{
		Path:  relPath,
		Items: items,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) enrichPublicShareInfo(ctx context.Context, info *PublicShareInfo, share *Share) {
	info.FileName = path.Base(share.Path)

	statProvider, ok := h.fs.(FileStatProvider)
	if !ok {
		return
	}

	switch share.Type {
	case ShareTypeFile:
		if fileInfo, err := statProvider.Stat(ctx, share.Path); err == nil && !fileInfo.IsDir {
			info.FileSize = fileInfo.Size
		}
	case ShareTypeFolder:
		if entries, err := statProvider.ReadDir(ctx, share.Path); err == nil {
			info.FolderItems = len(entries)
		}
	}
}

func (h *Handler) buildShareURL(id string) string {
	if h.baseURL != "" {
		return h.baseURL + "/s/" + id
	}
	return "/s/" + id
}

func (h *Handler) accessAuthorizedShare(r *http.Request, id string) (*Share, error) {
	share, err := h.store.Get(id)
	if err != nil {
		return nil, err
	}

	if err := share.CanAccess(); err != nil {
		return nil, err
	}

	if share.HasPassword() && !h.hasShareAccess(r, share) {
		return nil, ErrInvalidPassword
	}

	if err := h.store.RecordAccess(id); err != nil {
		return nil, err
	}

	return h.store.Get(id)
}

func (h *Handler) hasShareAccess(r *http.Request, share *Share) bool {
	if share == nil || !share.HasPassword() {
		return true
	}

	cookie, err := r.Cookie(shareAccessCookieName(share.ID))
	if err != nil {
		return false
	}

	expected := h.shareAccessToken(share)
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(expected)) == 1
}

func (h *Handler) setShareAccessCookie(w http.ResponseWriter, r *http.Request, share *Share) {
	if share == nil || !share.HasPassword() {
		return
	}

	cookie := &http.Cookie{
		Name:     shareAccessCookieName(share.ID),
		Value:    h.shareAccessToken(share),
		Path:     "/s/" + share.ID,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
	}

	if share.ExpiresAt != nil {
		cookie.Expires = share.ExpiresAt.UTC()
		cookie.MaxAge = int(time.Until(*share.ExpiresAt).Seconds())
		if cookie.MaxAge < 0 {
			cookie.MaxAge = 0
		}
	} else {
		cookie.MaxAge = int((24 * time.Hour).Seconds())
	}

	http.SetCookie(w, cookie)
}

func (h *Handler) shareAccessToken(share *Share) string {
	sum := sha256.Sum256([]byte(share.ID + ":" + share.PasswordHash))
	return hex.EncodeToString(sum[:])
}

func shareAccessCookieName(id string) string {
	return shareAccessCookiePrefix + id
}

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func shareRelativePath(basePath, entryPath string) (string, error) {
	cleanBase := path.Clean(basePath)
	cleanEntry := path.Clean(entryPath)

	if cleanEntry == cleanBase {
		return ".", nil
	}

	prefix := cleanBase
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	if !strings.HasPrefix(cleanEntry, prefix) {
		return "", errors.New("entry outside share path")
	}

	return strings.TrimPrefix(cleanEntry, prefix), nil
}

func isWithinSharePath(basePath, targetPath string) bool {
	basePath = path.Clean(basePath)
	targetPath = path.Clean(targetPath)
	if basePath == "/" {
		return strings.HasPrefix(targetPath, "/")
	}
	if targetPath == basePath {
		return true
	}
	if strings.HasPrefix(targetPath, basePath) {
		return len(targetPath) > len(basePath) && targetPath[len(basePath)] == '/'
	}
	return false
}

func getUserIDFromContext(ctx context.Context) string {
	if claims := auth.GetClaimsFromContext(ctx); claims != nil {
		return claims.UserID
	}
	return ""
}

func getIsAdminFromContext(ctx context.Context) bool {
	return auth.IsAdmin(ctx)
}

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	return time.ParseDuration(s)
}
