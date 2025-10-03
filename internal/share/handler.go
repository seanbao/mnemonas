package share

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/requestip"
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

type FileSnapshotOpener interface {
	OpenFileSnapshot(ctx context.Context, filePath string) (*os.File, *storage.FileInfo, error)
}

type PathAccessAuthorizer func(ctx context.Context, share *Share, targetPath string) error

type responseEnvelope struct {
	Success bool         `json:"success"`
	Data    interface{}  `json:"data,omitempty"`
	Warning bool         `json:"warning,omitempty"`
	Message string       `json:"message,omitempty"`
	Error   *errorDetail `json:"error,omitempty"`
}

type errorDetail struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// Handler provides HTTP handlers for share operations
type Handler struct {
	store                 *ShareStore
	fs                    FileOpener
	userStore             shareOwnerStore
	baseURL               string
	passwordAttempts      *passwordAttemptTracker
	passwordFailureLimit  int
	passwordFailureWindow time.Duration
	passwordFailureDelay  time.Duration
	passwordLockDuration  time.Duration
	beforeMutateShare     func(id string) error
	pathAccessAuthorizer  PathAccessAuthorizer
}

type shareOwnerStore interface {
	GetByID(id string) (*auth.User, error)
	GetByUsername(username string) (*auth.User, error)
}

type passwordAttemptTracker struct {
	mu       sync.Mutex
	attempts map[string]passwordAttemptState
	now      func() time.Time
}

type passwordAttemptState struct {
	failures    int
	lastFailure time.Time
	lockedUntil time.Time
}

const (
	shareAccessCookiePrefix       = "mnemonas_share_"
	sharePersistenceWarningHeader = `199 MnemoNAS "share persistence incomplete"`
	defaultPasswordFailureLimit   = 5
	defaultPasswordFailureWindow  = 15 * time.Minute
	defaultPasswordFailureDelay   = 200 * time.Millisecond
	defaultPasswordLockDuration   = 5 * time.Minute
	defaultRateLimitErrorMessage  = "too many attempts, try later"
	defaultJSONRequestBodyLimit   = 1 * 1024 * 1024
	maxShareArchiveEntries        = 10000
	maxDurationDays               = int64((1<<63 - 1) / int64(24*time.Hour))
	untrustedDownloadCSP          = "sandbox; default-src 'none'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; img-src 'self' data: blob:; media-src 'self' data: blob:; style-src 'unsafe-inline'"
	shareAccessSameSite           = http.SameSiteStrictMode
)

var (
	maxShareArchiveBytes           = int64(20 * 1024 * 1024 * 1024)
	errInvalidSharePermission      = errors.New("invalid permission")
	errUnsupportedShareArchive     = errors.New("unsupported archive format")
	errInvalidShareArchivePath     = errors.New("invalid archive path")
	errShareArchiveTooManyEntries  = errors.New("archive contains too many entries")
	errShareArchiveContentTooLarge = errors.New("archive content is too large")
)

func newPasswordAttemptTracker() *passwordAttemptTracker {
	return &passwordAttemptTracker{
		attempts: make(map[string]passwordAttemptState),
		now:      time.Now,
	}
}

func (t *passwordAttemptTracker) isLocked(key string, failureWindow time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	t.pruneExpiredLocked(now, failureWindow)
	state, ok := t.attempts[key]
	if !ok {
		return false
	}
	if state.lockedUntil.After(now) {
		return true
	}
	if !state.lockedUntil.IsZero() {
		delete(t.attempts, key)
	}
	return false
}

func (t *passwordAttemptTracker) recordFailure(key string, limit int, failureWindow, lockDuration time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	t.pruneExpiredLocked(now, failureWindow)
	state := t.attempts[key]
	if failureWindow > 0 && !state.lastFailure.IsZero() && now.Sub(state.lastFailure) > failureWindow {
		state = passwordAttemptState{}
	}
	state.failures++
	state.lastFailure = now
	if state.failures >= limit {
		state.lockedUntil = now.Add(lockDuration)
	}
	t.attempts[key] = state

	return !state.lockedUntil.IsZero()
}

func (t *passwordAttemptTracker) pruneExpiredLocked(now time.Time, failureWindow time.Duration) {
	for key, state := range t.attempts {
		if !state.lockedUntil.IsZero() && !state.lockedUntil.After(now) {
			delete(t.attempts, key)
			continue
		}
		if failureWindow > 0 && !state.lastFailure.IsZero() && now.Sub(state.lastFailure) > failureWindow {
			delete(t.attempts, key)
		}
	}
}

func (t *passwordAttemptTracker) reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.attempts, key)
}

// NewHandler creates a new share handler
// fs can be nil if file download is not needed
func NewHandler(store *ShareStore, fs FileOpener) *Handler {
	return &Handler{
		store:                 store,
		fs:                    fs,
		passwordAttempts:      newPasswordAttemptTracker(),
		passwordFailureLimit:  defaultPasswordFailureLimit,
		passwordFailureWindow: defaultPasswordFailureWindow,
		passwordFailureDelay:  defaultPasswordFailureDelay,
		passwordLockDuration:  defaultPasswordLockDuration,
	}
}

// SetBaseURL sets the base URL for share links
func (h *Handler) SetBaseURL(baseURL string) {
	baseURL = strings.TrimSpace(baseURL)
	h.baseURL = strings.TrimRight(baseURL, "/")
}

func (h *Handler) SetUserStore(store shareOwnerStore) {
	h.userStore = store
}

func (h *Handler) SetPathAccessAuthorizer(authorizer PathAccessAuthorizer) {
	h.pathAccessAuthorizer = authorizer
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
	MaxAccess   *int64 `json:"max_access,omitempty"`
	Description string `json:"description,omitempty"`
}

func normalizeShareAbsolutePath(rawPath string) (string, error) {
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	if strings.ContainsRune(normalized, '\x00') {
		return "", errors.New("invalid path")
	}
	if strings.TrimSpace(normalized) == "" {
		return "", errors.New("invalid path")
	}
	if hasShareDotSegment(normalized) {
		return "", errors.New("invalid path")
	}
	return path.Clean("/" + normalized), nil
}

func normalizeShareRelativePath(rawPath string) (string, error) {
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	if strings.ContainsRune(normalized, '\x00') {
		return "", errors.New("invalid path")
	}
	normalized = strings.TrimLeft(normalized, "/")
	if hasShareDotSegment(normalized) {
		return "", errors.New("invalid path")
	}
	cleaned := path.Clean(normalized)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("invalid path")
	}
	if cleaned == "." {
		return "", nil
	}
	return cleaned, nil
}

func shareListPathFromRequest(r *http.Request) (string, error) {
	values, ok := r.URL.Query()["path"]
	if !ok {
		return "", nil
	}
	if len(values) != 1 {
		return "", errors.New("ambiguous path parameter")
	}
	return normalizeShareRelativePath(values[0])
}

func shareDownloadPathFromRequest(r *http.Request, id string) (string, error) {
	prefixes := []string{
		"/s/" + url.PathEscape(id) + "/download",
		"/api/v1/public/shares/" + url.PathEscape(id) + "/download",
	}
	escapedPath := r.URL.EscapedPath()
	for _, prefix := range prefixes {
		if !strings.HasPrefix(escapedPath, prefix) {
			continue
		}
		encodedPath := strings.TrimPrefix(escapedPath, prefix)
		if encodedPath == "" || encodedPath == "/" {
			return "", nil
		}
		if !strings.HasPrefix(encodedPath, "/") {
			return "", errors.New("invalid path")
		}
		decodedPath, err := url.PathUnescape(encodedPath)
		if err != nil {
			return "", errors.New("invalid path")
		}
		return decodedPath, nil
	}

	filePath := chi.URLParam(r, "*")
	if filePath == "" {
		return "", nil
	}
	return "/" + filePath, nil
}

func decodeJSONBodyStrict(r *http.Request, dst any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, defaultJSONRequestBodyLimit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > defaultJSONRequestBodyLimit {
		return &http.MaxBytesError{Limit: defaultJSONRequestBodyLimit}
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}

	var extra struct{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing data")
		}
		return err
	}

	return nil
}

func writeShareJSONBodyError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		writeShareError(w, http.StatusRequestEntityTooLarge, "request body too large", "PAYLOAD_TOO_LARGE")
		return
	}

	writeShareError(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
}

// CreateShare creates a new share link
func (h *Handler) CreateShare(w http.ResponseWriter, r *http.Request) {
	var req CreateShareRequest
	if err := decodeJSONBodyStrict(r, &req); err != nil {
		writeShareJSONBodyError(w, err)
		return
	}

	cleanPath := strings.TrimSpace(req.Path)
	if cleanPath == "" {
		writeShareError(w, http.StatusBadRequest, "path is required", "MISSING_PATH")
		return
	}
	cleanPath, err := normalizeShareAbsolutePath(req.Path)
	if err != nil {
		writeShareError(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
		return
	}
	if req.MaxAccess != nil && *req.MaxAccess < 0 {
		writeShareError(w, http.StatusBadRequest, "invalid max_access", "INVALID_MAX_ACCESS")
		return
	}

	userID := getUserIDFromContext(r.Context())

	opts := CreateShareOptions{
		Path:        cleanPath,
		CreatedBy:   userID,
		Password:    req.Password,
		Description: req.Description,
	}
	if req.MaxAccess != nil {
		opts.MaxAccess = *req.MaxAccess
	}

	switch req.Type {
	case "", "file":
		opts.Type = ShareTypeFile
	case "folder":
		opts.Type = ShareTypeFolder
	default:
		writeShareError(w, http.StatusBadRequest, "invalid share type", "INVALID_SHARE_TYPE")
		return
	}

	if req.ExpiresIn != "" {
		duration, err := parseDuration(req.ExpiresIn)
		if err != nil {
			writeShareError(w, http.StatusBadRequest, "invalid expires_in format", "INVALID_EXPIRES_IN")
			return
		}
		opts.ExpiresIn = &duration
	}

	switch req.Permission {
	case "":
		opts.Permission = PermissionRead
	case "read":
		opts.Permission = PermissionRead
	default:
		writeShareError(w, http.StatusBadRequest, "invalid permission", "INVALID_PERMISSION")
		return
	}

	statProvider, ok := h.fs.(FileStatProvider)
	if !ok {
		writeShareError(w, http.StatusServiceUnavailable, "filesystem not available", "FILESYSTEM_UNAVAILABLE")
		return
	}

	info, err := statProvider.Stat(r.Context(), cleanPath)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeShareError(w, http.StatusNotFound, "file not found", "FILE_NOT_FOUND")
			return
		}
		if errors.Is(err, storage.ErrNotDir) {
			writeShareError(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
			return
		}
		writeShareError(w, http.StatusInternalServerError, "internal server error", "CREATE_SHARE_FAILED")
		return
	}
	if opts.Type == ShareTypeFile && info.IsDir {
		writeShareError(w, http.StatusBadRequest, "path is not a file", "INVALID_SHARE_TYPE")
		return
	}
	if opts.Type == ShareTypeFolder && !info.IsDir {
		writeShareError(w, http.StatusBadRequest, "path is not a folder", "INVALID_SHARE_TYPE")
		return
	}

	share, err := h.store.Create(opts)
	if err != nil {
		if errors.Is(err, errSharePasswordLong) {
			writeShareError(w, http.StatusBadRequest, "share password must be at most 72 bytes", "PASSWORD_TOO_LONG")
			return
		}
		if IsPersistenceWarning(err) {
			shareInfo := share.ToInfo()
			shareInfo.URL = h.buildShareURL(share.ID)
			writeShareSuccessWithWarning(w, http.StatusCreated, shareInfo, "share created with persistence warning")
			return
		}
		writeShareError(w, http.StatusInternalServerError, "internal server error", "CREATE_SHARE_FAILED")
		return
	}

	shareInfo := share.ToInfo()
	shareInfo.URL = h.buildShareURL(share.ID)

	writeShareSuccess(w, http.StatusCreated, shareInfo, "")
}

// ListShares lists shares for the current user
func (h *Handler) ListShares(w http.ResponseWriter, r *http.Request) {
	isAdmin := getIsAdminFromContext(r.Context())
	listAll, err := shareListAllFromRequest(r)
	if err != nil {
		writeShareError(w, http.StatusBadRequest, "invalid request", "INVALID_REQUEST")
		return
	}

	var shares []*Share
	if isAdmin && listAll {
		shares = h.store.ListAll()
	} else {
		shares = h.store.ListByUser(getShareOwnerIdentifiersFromContext(r.Context())...)
	}

	infos := make([]*ShareInfo, len(shares))
	for i, s := range shares {
		infos[i] = s.ToInfo()
		infos[i].URL = h.buildShareURL(s.ID)
	}

	writeShareSuccess(w, http.StatusOK, infos, "")
}

func shareListAllFromRequest(r *http.Request) (bool, error) {
	values, ok := r.URL.Query()["all"]
	if !ok {
		return false, nil
	}
	if len(values) != 1 {
		return false, errors.New("ambiguous all parameter")
	}
	return values[0] == "true", nil
}

// GetShare gets a share by ID
func (h *Handler) GetShare(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	share, err := h.store.Get(id)
	if err != nil {
		if errors.Is(err, ErrShareNotFound) {
			writeShareError(w, http.StatusNotFound, "share not found", "SHARE_NOT_FOUND")
			return
		}
		writeShareError(w, http.StatusInternalServerError, "internal server error", "GET_SHARE_FAILED")
		return
	}

	if !shareOwnedByRequester(r.Context(), share) {
		writeShareError(w, http.StatusForbidden, "forbidden", "FORBIDDEN")
		return
	}

	info := share.ToInfo()
	info.URL = h.buildShareURL(share.ID)

	writeShareSuccess(w, http.StatusOK, info, "")
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
	if err := decodeJSONBodyStrict(r, &req); err != nil {
		writeShareJSONBodyError(w, err)
		return
	}

	var expiresAt *time.Time
	if req.ExpiresIn != nil {
		if *req.ExpiresIn == "" {
			expiresAt = nil
		} else {
			duration, err := parseDuration(*req.ExpiresIn)
			if err != nil {
				writeShareError(w, http.StatusBadRequest, "invalid expires_in format", "INVALID_EXPIRES_IN")
				return
			}
			exp := time.Now().Add(duration)
			expiresAt = &exp
		}
	}
	if req.MaxAccess != nil && *req.MaxAccess < 0 {
		writeShareError(w, http.StatusBadRequest, "invalid max_access", "INVALID_MAX_ACCESS")
		return
	}

	share, err := h.store.Get(id)
	if err != nil {
		if errors.Is(err, ErrShareNotFound) {
			writeShareError(w, http.StatusNotFound, "share not found", "SHARE_NOT_FOUND")
			return
		}
		writeShareError(w, http.StatusInternalServerError, "internal server error", "GET_SHARE_FAILED")
		return
	}

	if !shareOwnedByRequester(r.Context(), share) {
		writeShareError(w, http.StatusForbidden, "forbidden", "FORBIDDEN")
		return
	}
	if h.beforeMutateShare != nil {
		if err := h.beforeMutateShare(id); err != nil {
			writeShareError(w, http.StatusInternalServerError, "internal server error", "UPDATE_SHARE_FAILED")
			return
		}
	}

	var updatedShare *Share
	err = h.store.Update(id, func(s *Share) error {
		if req.Enabled != nil {
			s.Enabled = *req.Enabled
		}
		if req.ExpiresIn != nil {
			s.ExpiresAt = expiresAt
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
			case "":
				s.Permission = PermissionRead
			case "read":
				s.Permission = PermissionRead
			default:
				return errInvalidSharePermission
			}
		}
		if req.MaxAccess != nil {
			s.MaxAccess = *req.MaxAccess
		}
		if req.Description != nil {
			s.Description = *req.Description
		}
		updatedShare = copyShare(s)
		return nil
	})

	if err != nil {
		if errors.Is(err, errInvalidSharePermission) {
			writeShareError(w, http.StatusBadRequest, "invalid permission", "INVALID_PERMISSION")
			return
		}
		if errors.Is(err, errSharePasswordLong) {
			writeShareError(w, http.StatusBadRequest, "share password must be at most 72 bytes", "PASSWORD_TOO_LONG")
			return
		}
		if errors.Is(err, ErrShareNotFound) {
			writeShareError(w, http.StatusNotFound, "share not found", "SHARE_NOT_FOUND")
			return
		}
		if IsPersistenceWarning(err) && updatedShare != nil {
			info := updatedShare.ToInfo()
			info.URL = h.buildShareURL(updatedShare.ID)
			writeShareSuccessWithWarning(w, http.StatusOK, info, "share updated with persistence warning")
			return
		}
		writeShareError(w, http.StatusInternalServerError, "internal server error", "UPDATE_SHARE_FAILED")
		return
	}

	if updatedShare == nil {
		writeShareError(w, http.StatusInternalServerError, "internal server error", "UPDATE_SHARE_FAILED")
		return
	}
	info := updatedShare.ToInfo()
	info.URL = h.buildShareURL(updatedShare.ID)

	writeShareSuccess(w, http.StatusOK, info, "")
}

// DeleteShare deletes a share
func (h *Handler) DeleteShare(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	share, err := h.store.Get(id)
	if err != nil {
		if errors.Is(err, ErrShareNotFound) {
			writeShareError(w, http.StatusNotFound, "share not found", "SHARE_NOT_FOUND")
			return
		}
		writeShareError(w, http.StatusInternalServerError, "internal server error", "GET_SHARE_FAILED")
		return
	}

	if !shareOwnedByRequester(r.Context(), share) {
		writeShareError(w, http.StatusForbidden, "forbidden", "FORBIDDEN")
		return
	}
	if h.beforeMutateShare != nil {
		if err := h.beforeMutateShare(id); err != nil {
			writeShareError(w, http.StatusInternalServerError, "internal server error", "DELETE_SHARE_FAILED")
			return
		}
	}

	if err := h.store.Delete(id); err != nil {
		if errors.Is(err, ErrShareNotFound) {
			writeShareError(w, http.StatusNotFound, "share not found", "SHARE_NOT_FOUND")
			return
		}
		if IsPersistenceWarning(err) {
			writeShareSuccessWithWarning(w, http.StatusOK, nil, "share deleted with persistence warning")
			return
		}
		writeShareError(w, http.StatusInternalServerError, "internal server error", "DELETE_SHARE_FAILED")
		return
	}

	writeShareSuccess(w, http.StatusOK, nil, "share deleted successfully")
}

// PublicShareInfo is the info returned for public share access
type PublicShareInfo struct {
	ID          string     `json:"id"`
	Type        ShareType  `json:"type"`
	HasPassword bool       `json:"has_password"`
	Permission  Permission `json:"permission"`
	Description string     `json:"description,omitempty"`
	FileName    string     `json:"file_name,omitempty"`
	FileSize    *int64     `json:"file_size,omitempty"`
	FolderItems *int       `json:"folder_items,omitempty"`
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

func writePublicShareAccessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrShareNotFound):
		writeShareError(w, http.StatusNotFound, "share not found", "SHARE_NOT_FOUND")
	case errors.Is(err, ErrInvalidPassword):
		writeShareError(w, http.StatusUnauthorized, "password required", "PASSWORD_REQUIRED")
	case errors.Is(err, ErrShareAccessLimit):
		writeShareError(w, http.StatusGone, "share access limit reached", "SHARE_ACCESS_LIMIT_REACHED")
	case errors.Is(err, ErrShareExpired):
		writeShareError(w, http.StatusGone, "share expired", "SHARE_EXPIRED")
	case errors.Is(err, ErrShareDisabled):
		writeShareError(w, http.StatusGone, "share disabled", "SHARE_DISABLED")
	default:
		writeShareError(w, http.StatusInternalServerError, "internal server error", "ACCESS_SHARE_FAILED")
	}
}

// AccessShare handles public share access
func (h *Handler) AccessShare(w http.ResponseWriter, r *http.Request) {
	setPublicShareJSONHeaders(w)
	id := chi.URLParam(r, "id")

	share, err := h.store.Get(id)
	if err != nil {
		writePublicShareAccessError(w, err)
		return
	}

	if share.HasPassword() {
		if err := share.CanAccess(); err != nil {
			writePublicShareAccessError(w, err)
			return
		}
		if err := h.ensureShareOwnerActive(share); err != nil {
			writePublicShareAccessError(w, err)
			return
		}

		if h.hasShareAccess(r, share) {
			info := &PublicShareInfo{
				ID:          share.ID,
				Type:        share.Type,
				HasPassword: share.HasPassword(),
				Permission:  share.Permission,
				Description: share.Description,
			}

			if err := h.enrichPublicShareInfo(r.Context(), info, share); err != nil {
				h.writePublicShareInfoError(w, share, err)
				return
			}
			if err := h.ensureShareOwnerActive(share); err != nil {
				writePublicShareAccessError(w, err)
				return
			}

			if !writePublicShareInfo(w, info) {
				return
			}
			return
		}

		info := &PublicShareInfo{
			ID:          share.ID,
			Type:        share.Type,
			HasPassword: share.HasPassword(),
			Permission:  share.Permission,
		}

		if !writePublicShareInfo(w, info) {
			return
		}
		return
	}

	if err := share.CanAccess(); err != nil {
		writePublicShareAccessError(w, err)
		return
	}
	if err := h.ensureShareOwnerActive(share); err != nil {
		writePublicShareAccessError(w, err)
		return
	}

	info := &PublicShareInfo{
		ID:          share.ID,
		Type:        share.Type,
		HasPassword: share.HasPassword(),
		Permission:  share.Permission,
		Description: share.Description,
	}

	if err := h.enrichPublicShareInfo(r.Context(), info, share); err != nil {
		h.writePublicShareInfoError(w, share, err)
		return
	}
	if err := h.ensureShareOwnerActive(share); err != nil {
		writePublicShareAccessError(w, err)
		return
	}

	if !writePublicShareInfo(w, info) {
		return
	}
}

// AccessShareRequest is the request for password-protected share access
type AccessShareRequest struct {
	Password string `json:"password"`
}

// AccessShareWithPassword validates password and returns share details
func (h *Handler) AccessShareWithPassword(w http.ResponseWriter, r *http.Request) {
	setPublicShareJSONHeaders(w)
	id := chi.URLParam(r, "id")

	var req AccessShareRequest
	if err := decodeJSONBodyStrict(r, &req); err != nil {
		writeShareJSONBodyError(w, err)
		return
	}

	share, err := h.store.Get(id)
	if err != nil {
		writePublicShareAccessError(w, err)
		return
	}

	if err := share.CanAccess(); err != nil {
		writePublicShareAccessError(w, err)
		return
	}
	if err := h.ensureShareOwnerActive(share); err != nil {
		writePublicShareAccessError(w, err)
		return
	}

	if share.HasPassword() {
		attemptKey := sharePasswordAttemptKey(id, r)
		if h.passwordAttempts.isLocked(attemptKey, h.passwordFailureWindow) {
			writeShareError(w, http.StatusTooManyRequests, defaultRateLimitErrorMessage, "SHARE_PASSWORD_RATE_LIMITED")
			return
		}

		if !share.CheckPassword(req.Password) {
			locked := h.passwordAttempts.recordFailure(attemptKey, h.passwordFailureLimit, h.passwordFailureWindow, h.passwordLockDuration)
			if h.passwordFailureDelay > 0 {
				time.Sleep(h.passwordFailureDelay)
			}
			if locked {
				writeShareError(w, http.StatusTooManyRequests, defaultRateLimitErrorMessage, "SHARE_PASSWORD_RATE_LIMITED")
				return
			}
			writeShareError(w, http.StatusUnauthorized, "invalid password", "INVALID_PASSWORD")
			return
		}

		h.passwordAttempts.reset(attemptKey)
	}

	info := &PublicShareInfo{
		ID:          share.ID,
		Type:        share.Type,
		HasPassword: share.HasPassword(),
		Permission:  share.Permission,
		Description: share.Description,
	}
	if err := h.enrichPublicShareInfo(r.Context(), info, share); err != nil {
		h.writePublicShareInfoError(w, share, err)
		return
	}
	if err := h.ensureShareOwnerActive(share); err != nil {
		writePublicShareAccessError(w, err)
		return
	}
	payload, err := marshalShareJSON(info)
	if err != nil {
		writeShareError(w, http.StatusInternalServerError, "internal server error", "GET_SHARE_FAILED")
		return
	}

	h.setShareAccessCookie(w, r, share)
	writeShareJSONPayload(w, http.StatusOK, payload)
}

func sharePasswordAttemptKey(id string, r *http.Request) string {
	return id + "|" + clientIdentifier(r)
}

func clientIdentifier(r *http.Request) string {
	return requestip.ClientIP(r)
}

func shareDownloadContentType(filePath string) string {
	contentType := mime.TypeByExtension(strings.ToLower(path.Ext(filePath)))
	if contentType == "" {
		return "application/octet-stream"
	}
	return contentType
}

// DownloadShare handles file download for shares
func (h *Handler) DownloadShare(w http.ResponseWriter, r *http.Request) {
	setPublicShareJSONHeaders(w)
	id := chi.URLParam(r, "id")
	archiveFormat, err := shareArchiveFormatFromRequest(r)
	if err != nil {
		writeShareError(w, http.StatusBadRequest, "unsupported archive format", "INVALID_ARCHIVE_FORMAT")
		return
	}

	share, err := h.authorizeShare(r, id)
	if err != nil {
		writePublicShareAccessError(w, err)
		return
	}

	if archiveFormat == "zip" {
		if h.fs == nil {
			writeShareError(w, http.StatusServiceUnavailable, "filesystem not available", "FILESYSTEM_UNAVAILABLE")
			return
		}
		if err := h.authorizeSharePath(r.Context(), share, share.Path); err != nil {
			writePublicSharePathError(w, err, "DOWNLOAD_SHARE_FAILED")
			return
		}
		h.serveAuthorizedShareArchive(w, r, share, share.Path)
		return
	}

	if share.Type != ShareTypeFile {
		writeShareError(w, http.StatusBadRequest, "use /download/{path} for folders", "INVALID_SHARE_TYPE")
		return
	}

	if h.fs == nil {
		writeShareError(w, http.StatusServiceUnavailable, "filesystem not available", "FILESYSTEM_UNAVAILABLE")
		return
	}

	if err := h.authorizeSharePath(r.Context(), share, share.Path); err != nil {
		writePublicSharePathError(w, err, "DOWNLOAD_SHARE_FAILED")
		return
	}

	reader, err := h.fs.OpenFile(r.Context(), share.Path)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeShareError(w, http.StatusNotFound, "file not found", "FILE_NOT_FOUND")
			return
		}
		if errors.Is(err, storage.ErrNotDir) {
			writeShareError(w, http.StatusNotFound, "file not found", "FILE_NOT_FOUND")
			return
		}
		if errors.Is(err, storage.ErrIsDir) {
			writeShareError(w, http.StatusBadRequest, "shared resource is a directory", "INVALID_SHARE_TYPE")
			return
		}
		writeShareError(w, http.StatusInternalServerError, "internal server error", "DOWNLOAD_SHARE_FAILED")
		return
	}
	if reader == nil {
		writeShareError(w, http.StatusNotFound, "file not found", "FILE_NOT_FOUND")
		return
	}
	defer reader.Close()

	h.serveAuthorizedShareDownload(w, r, share, reader, share.Path)
}

// DownloadShareFile handles file download from shared folder
func (h *Handler) DownloadShareFile(w http.ResponseWriter, r *http.Request) {
	setPublicShareJSONHeaders(w)
	id := chi.URLParam(r, "id")
	filePath, err := shareDownloadPathFromRequest(r, id)
	if err != nil {
		writeShareError(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
		return
	}
	filePath, err = normalizeShareRelativePath(filePath)
	if err != nil {
		writeShareError(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
		return
	}
	archiveFormat, err := shareArchiveFormatFromRequest(r)
	if err != nil {
		writeShareError(w, http.StatusBadRequest, "unsupported archive format", "INVALID_ARCHIVE_FORMAT")
		return
	}

	share, err := h.authorizeShare(r, id)
	if err != nil {
		writePublicShareAccessError(w, err)
		return
	}

	if share.Type != ShareTypeFolder {
		writeShareError(w, http.StatusBadRequest, "share is not a folder", "INVALID_SHARE_TYPE")
		return
	}

	if filePath == "" || filePath == "." {
		writeShareError(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
		return
	}

	fullPath := path.Join(share.Path, filePath)
	if !isWithinSharePath(share.Path, fullPath) {
		writeShareError(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
		return
	}

	if h.fs == nil {
		writeShareError(w, http.StatusServiceUnavailable, "filesystem not available", "FILESYSTEM_UNAVAILABLE")
		return
	}

	if err := h.authorizeSharePath(r.Context(), share, fullPath); err != nil {
		writePublicSharePathError(w, err, "DOWNLOAD_SHARE_FAILED")
		return
	}

	if archiveFormat == "zip" {
		h.serveAuthorizedShareArchive(w, r, share, fullPath)
		return
	}

	reader, err := h.fs.OpenFile(r.Context(), fullPath)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeShareError(w, http.StatusNotFound, "file not found", "FILE_NOT_FOUND")
			return
		}
		if errors.Is(err, storage.ErrNotDir) {
			writeShareError(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
			return
		}
		if errors.Is(err, storage.ErrIsDir) {
			writeShareError(w, http.StatusBadRequest, "path is a directory", "INVALID_PATH")
			return
		}
		writeShareError(w, http.StatusInternalServerError, "internal server error", "DOWNLOAD_SHARE_FAILED")
		return
	}
	if reader == nil {
		writeShareError(w, http.StatusNotFound, "file not found", "FILE_NOT_FOUND")
		return
	}
	defer reader.Close()

	h.serveAuthorizedShareDownload(w, r, share, reader, fullPath)
}

func contentDispositionAttachment(filename string) string {
	value := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	if value == "" {
		return "attachment"
	}
	return value
}

func (h *Handler) serveAuthorizedShareDownload(w http.ResponseWriter, r *http.Request, share *Share, reader FileReader, filePath string) {
	if requestHasRangeHeader(r) {
		if seeker, ok := reader.(io.ReadSeeker); ok {
			h.serveSeekableShareDownload(w, r, share, seeker, filePath)
			return
		}
	}

	firstChunk, exhausted, err := prefetchDownloadChunk(reader)
	if err != nil {
		writeShareError(w, http.StatusInternalServerError, "internal server error", "DOWNLOAD_SHARE_FAILED")
		return
	}
	accessReservation, err := h.reserveAuthorizedAccessForShare(share)
	if err != nil {
		if IsPersistenceWarning(err) {
			markSharePersistenceWarningHeaders(w)
		} else {
			writePublicShareAccessError(w, err)
			return
		}
	}

	setShareDownloadHeaders(w, filePath)
	if err := streamDownload(w, reader, firstChunk, exhausted); err != nil {
		if streamResponseStarted(err) {
			return
		}
		if rollbackErr := h.store.rollbackAuthorizedAccess(accessReservation); rollbackErr != nil {
			if IsPersistenceWarning(rollbackErr) {
				markSharePersistenceWarningHeaders(w)
			} else {
				writeShareError(w, http.StatusInternalServerError, "internal server error", "DOWNLOAD_SHARE_ROLLBACK_FAILED")
				return
			}
		}
		writeShareError(w, http.StatusInternalServerError, "internal server error", "DOWNLOAD_SHARE_FAILED")
		return
	}
}

func shareArchiveFormatFromRequest(r *http.Request) (string, error) {
	values, ok := r.URL.Query()["archive"]
	if !ok {
		return "", nil
	}
	if len(values) != 1 {
		return "", errUnsupportedShareArchive
	}
	archiveFormat := strings.TrimSpace(values[0])
	if archiveFormat == "" {
		return "", nil
	}
	if archiveFormat != "zip" {
		return "", errUnsupportedShareArchive
	}
	return archiveFormat, nil
}

type shareArchiveEntry struct {
	sourcePath string
	zipName    string
	info       *storage.FileInfo
}

type shareArchiveCollector struct {
	handler      *Handler
	share        *Share
	statProvider FileStatProvider
	ctx          context.Context
	entries      []shareArchiveEntry
	totalBytes   int64
}

func (h *Handler) serveAuthorizedShareArchive(w http.ResponseWriter, r *http.Request, share *Share, rootPath string) {
	statProvider, ok := h.fs.(FileStatProvider)
	if !ok {
		writeShareError(w, http.StatusServiceUnavailable, "filesystem not available", "FILESYSTEM_UNAVAILABLE")
		return
	}

	entries, err := h.collectShareArchiveEntries(r.Context(), share, statProvider, rootPath)
	if err != nil {
		writePublicShareArchiveError(w, err)
		return
	}

	accessReservation, err := h.reserveAuthorizedAccessForShare(share)
	if err != nil {
		if IsPersistenceWarning(err) {
			markSharePersistenceWarningHeaders(w)
		} else {
			writePublicShareAccessError(w, err)
			return
		}
	}

	setShareArchiveDownloadHeaders(w, rootPath)
	trackingWriter := &responseStartTrackingWriter{ResponseWriter: w}
	zipWriter := zip.NewWriter(trackingWriter)
	if err := h.writeShareArchive(r.Context(), zipWriter, entries); err != nil {
		if trackingWriter.started {
			_ = zipWriter.Close()
			return
		}
		h.rollbackShareArchiveAccessOrWriteError(w, accessReservation, err, "DOWNLOAD_SHARE_ARCHIVE_FAILED")
		return
	}
	if err := zipWriter.Close(); err != nil {
		if trackingWriter.started {
			return
		}
		h.rollbackShareArchiveAccessOrWriteError(w, accessReservation, err, "DOWNLOAD_SHARE_ARCHIVE_FAILED")
		return
	}
}

func (h *Handler) rollbackShareArchiveAccessOrWriteError(w http.ResponseWriter, reservation *authorizedAccessReservation, err error, fallbackCode string) {
	if rollbackErr := h.store.rollbackAuthorizedAccess(reservation); rollbackErr != nil {
		if IsPersistenceWarning(rollbackErr) {
			markSharePersistenceWarningHeaders(w)
		} else {
			writeShareError(w, http.StatusInternalServerError, "internal server error", "DOWNLOAD_SHARE_ARCHIVE_ROLLBACK_FAILED")
			return
		}
	}
	if isPublicShareArchiveResponseError(err) {
		writePublicShareArchiveError(w, err)
		return
	}
	writeShareError(w, http.StatusInternalServerError, "internal server error", fallbackCode)
}

func (h *Handler) collectShareArchiveEntries(ctx context.Context, share *Share, statProvider FileStatProvider, rootPath string) ([]shareArchiveEntry, error) {
	info, err := statProvider.Stat(ctx, rootPath)
	if err != nil {
		return nil, err
	}

	rootName, err := safeShareArchiveEntryName(shareArchiveRootName(rootPath))
	if err != nil {
		return nil, err
	}

	collector := &shareArchiveCollector{
		handler:      h,
		share:        share,
		statProvider: statProvider,
		ctx:          ctx,
	}
	if info.IsDir {
		if err := collector.walkDirectory(rootPath, rootName, info); err != nil {
			return nil, err
		}
		return collector.entries, nil
	}

	if err := collector.addFile(rootPath, rootName, info); err != nil {
		return nil, err
	}
	return collector.entries, nil
}

func (c *shareArchiveCollector) walkDirectory(sourcePath, zipName string, info *storage.FileInfo) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if err := c.addDirectory(sourcePath, zipName, info); err != nil {
		return err
	}

	children, err := c.statProvider.ReadDir(c.ctx, sourcePath)
	if err != nil {
		return err
	}
	for _, child := range children {
		if child == nil {
			continue
		}
		if err := c.ctx.Err(); err != nil {
			return err
		}
		childPath, childName, err := shareReadDirChildPath(sourcePath, child)
		if err != nil {
			if errors.Is(err, ErrShareNotFound) {
				continue
			}
			return err
		}
		if err := c.handler.authorizeSharePath(c.ctx, c.share, childPath); err != nil {
			if errors.Is(err, ErrShareNotFound) {
				continue
			}
			return err
		}

		zipChildName, err := safeShareArchiveEntryName(path.Join(zipName, childName))
		if err != nil {
			return err
		}
		if child.IsDir {
			if err := c.walkDirectory(childPath, zipChildName, child); err != nil {
				return err
			}
			continue
		}
		if err := c.addFile(childPath, zipChildName, child); err != nil {
			return err
		}
	}
	return nil
}

func (c *shareArchiveCollector) addDirectory(sourcePath, zipName string, info *storage.FileInfo) error {
	if len(c.entries)+1 > maxShareArchiveEntries {
		return errShareArchiveTooManyEntries
	}
	c.entries = append(c.entries, shareArchiveEntry{
		sourcePath: sourcePath,
		zipName:    strings.TrimRight(zipName, "/") + "/",
		info:       info,
	})
	return nil
}

func (c *shareArchiveCollector) addFile(sourcePath, zipName string, info *storage.FileInfo) error {
	if len(c.entries)+1 > maxShareArchiveEntries {
		return errShareArchiveTooManyEntries
	}
	if info.Size < 0 || c.totalBytes > maxShareArchiveBytes-info.Size {
		return errShareArchiveContentTooLarge
	}
	c.totalBytes += info.Size
	c.entries = append(c.entries, shareArchiveEntry{
		sourcePath: sourcePath,
		zipName:    zipName,
		info:       info,
	})
	return nil
}

func (h *Handler) writeShareArchive(ctx context.Context, zipWriter *zip.Writer, entries []shareArchiveEntry) error {
	var totalBytes int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.info == nil {
			return fmt.Errorf("share archive entry %s missing metadata", entry.sourcePath)
		}
		if entry.info.IsDir {
			header := &zip.FileHeader{
				Name:   entry.zipName,
				Method: zip.Store,
			}
			header.SetModTime(entry.info.ModTime)
			header.SetMode(os.ModeDir | 0o755)
			if _, err := zipWriter.CreateHeader(header); err != nil {
				return fmt.Errorf("create share archive directory %s: %w", entry.zipName, err)
			}
			continue
		}

		reader, archiveInfo, err := h.openShareArchiveFile(ctx, entry)
		if err != nil {
			return fmt.Errorf("open share archive file %s: %w", entry.sourcePath, err)
		}
		if reader == nil {
			return storage.ErrNotFound
		}
		if archiveInfo == nil {
			archiveInfo = entry.info
		}
		if archiveInfo.IsDir {
			_ = reader.Close()
			return storage.ErrIsDir
		}
		if archiveInfo.Size < 0 || totalBytes > maxShareArchiveBytes-archiveInfo.Size {
			_ = reader.Close()
			return errShareArchiveContentTooLarge
		}

		header := &zip.FileHeader{
			Name:               entry.zipName,
			Method:             zip.Deflate,
			UncompressedSize64: uint64(archiveInfo.Size),
		}
		header.SetModTime(archiveInfo.ModTime)
		header.SetMode(0o644)
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			_ = reader.Close()
			return fmt.Errorf("create share archive file %s: %w", entry.zipName, err)
		}
		remaining := maxShareArchiveBytes - totalBytes
		written, copyErr := io.Copy(writer, io.LimitReader(reader, remaining+1))
		closeErr := reader.Close()
		if copyErr != nil {
			return fmt.Errorf("write share archive file %s: %w", entry.zipName, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close share archive file %s: %w", entry.sourcePath, closeErr)
		}
		if written > remaining {
			return errShareArchiveContentTooLarge
		}
		totalBytes += written
	}
	return nil
}

func (h *Handler) openShareArchiveFile(ctx context.Context, entry shareArchiveEntry) (FileReader, *storage.FileInfo, error) {
	if snapshotOpener, ok := h.fs.(FileSnapshotOpener); ok {
		return snapshotOpener.OpenFileSnapshot(ctx, entry.sourcePath)
	}
	reader, err := h.fs.OpenFile(ctx, entry.sourcePath)
	return reader, entry.info, err
}

func isPublicShareArchiveResponseError(err error) bool {
	return errors.Is(err, errShareArchiveTooManyEntries) ||
		errors.Is(err, errShareArchiveContentTooLarge) ||
		errors.Is(err, errInvalidShareArchivePath) ||
		errors.Is(err, storage.ErrNotFound) ||
		errors.Is(err, ErrShareNotFound) ||
		errors.Is(err, storage.ErrNotDir) ||
		errors.Is(err, storage.ErrIsDir)
}

func writePublicShareArchiveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errShareArchiveTooManyEntries):
		writeShareError(w, http.StatusRequestEntityTooLarge, "archive contains too many entries", "ARCHIVE_TOO_MANY_ENTRIES")
	case errors.Is(err, errShareArchiveContentTooLarge):
		writeShareError(w, http.StatusRequestEntityTooLarge, "archive content is too large", "ARCHIVE_TOO_LARGE")
	case errors.Is(err, errInvalidShareArchivePath):
		writeShareError(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
	case errors.Is(err, storage.ErrNotFound), errors.Is(err, ErrShareNotFound):
		writeShareError(w, http.StatusNotFound, "file not found", "FILE_NOT_FOUND")
	case errors.Is(err, storage.ErrNotDir), errors.Is(err, storage.ErrIsDir):
		writeShareError(w, http.StatusBadRequest, "path is not a directory", "INVALID_PATH")
	default:
		writeShareError(w, http.StatusInternalServerError, "internal server error", "DOWNLOAD_SHARE_ARCHIVE_FAILED")
	}
}

func (h *Handler) serveSeekableShareDownload(w http.ResponseWriter, r *http.Request, share *Share, reader io.ReadSeeker, filePath string) {
	servesContent, err := seekableRangeRequestServesContent(r, reader)
	if err != nil {
		writeShareError(w, http.StatusInternalServerError, "internal server error", "DOWNLOAD_SHARE_FAILED")
		return
	}
	if !servesContent {
		setShareDownloadHeaders(w, filePath)
		http.ServeContent(w, r, path.Base(filePath), time.Time{}, reader)
		return
	}

	_, err = h.reserveAuthorizedAccessForShare(share)
	if err != nil {
		if IsPersistenceWarning(err) {
			markSharePersistenceWarningHeaders(w)
		} else {
			writePublicShareAccessError(w, err)
			return
		}
	}

	setShareDownloadHeaders(w, filePath)
	http.ServeContent(w, r, path.Base(filePath), time.Time{}, reader)
}

func seekableRangeRequestServesContent(r *http.Request, reader io.Seeker) (bool, error) {
	if !requestHasRangeHeader(r) {
		return true, nil
	}
	if strings.TrimSpace(r.Header.Get("If-Range")) != "" {
		return true, nil
	}
	size, err := seekableContentSize(reader)
	if err != nil {
		return false, err
	}
	return shareRangeHeaderServesContent(r.Header.Get("Range"), size), nil
}

func seekableContentSize(reader io.Seeker) (int64, error) {
	current, err := reader.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	size, sizeErr := reader.Seek(0, io.SeekEnd)
	_, restoreErr := reader.Seek(current, io.SeekStart)
	if sizeErr != nil {
		return 0, sizeErr
	}
	if restoreErr != nil {
		return 0, restoreErr
	}
	return size, nil
}

func shareRangeHeaderServesContent(rangeHeader string, size int64) bool {
	const prefix = "bytes="
	if !strings.HasPrefix(rangeHeader, prefix) {
		return false
	}

	hasRange := false
	noOverlap := false
	for _, rawRange := range strings.Split(rangeHeader[len(prefix):], ",") {
		rawRange = strings.TrimSpace(rawRange)
		if rawRange == "" {
			continue
		}
		startText, endText, ok := strings.Cut(rawRange, "-")
		if !ok {
			return false
		}
		startText = strings.TrimSpace(startText)
		endText = strings.TrimSpace(endText)
		if startText == "" {
			if endText == "" || strings.HasPrefix(endText, "-") {
				return false
			}
			suffixLength, err := strconv.ParseInt(endText, 10, 64)
			if err != nil || suffixLength < 0 {
				return false
			}
			hasRange = true
			continue
		}

		start, err := strconv.ParseInt(startText, 10, 64)
		if err != nil || start < 0 {
			return false
		}
		if start >= size {
			noOverlap = true
			continue
		}
		if endText != "" {
			end, err := strconv.ParseInt(endText, 10, 64)
			if err != nil || start > end {
				return false
			}
		}
		hasRange = true
	}
	if noOverlap && !hasRange {
		return size == 0
	}
	return true
}

func requestHasRangeHeader(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("Range")) != ""
}

func setShareArchiveDownloadHeaders(w http.ResponseWriter, rootPath string) {
	setUntrustedDownloadHeaders(w)
	w.Header().Set("Content-Disposition", contentDispositionAttachment(shareArchiveFilename(rootPath)))
	w.Header().Set("Content-Type", "application/zip")
}

func setShareDownloadHeaders(w http.ResponseWriter, filePath string) {
	filename := path.Base(filePath)
	setUntrustedDownloadHeaders(w)
	w.Header().Set("Content-Disposition", contentDispositionAttachment(filename))
	w.Header().Set("Content-Type", shareDownloadContentType(filePath))
}

func safeShareArchiveEntryName(name string) (string, error) {
	normalized := strings.ReplaceAll(name, "\\", "/")
	if normalized == "" || strings.ContainsRune(normalized, '\x00') || strings.HasPrefix(normalized, "/") || hasShareDotSegment(normalized) {
		return "", errInvalidShareArchivePath
	}
	cleaned := path.Clean(normalized)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errInvalidShareArchivePath
	}
	return cleaned, nil
}

func shareArchiveRootName(rootPath string) string {
	cleaned := path.Clean(rootPath)
	if cleaned == "/" || cleaned == "." {
		return "mnemonas-share"
	}
	return path.Base(cleaned)
}

func shareArchiveFilename(rootPath string) string {
	rootName := shareArchiveRootName(rootPath)
	if strings.HasSuffix(strings.ToLower(rootName), ".zip") {
		return rootName
	}
	return rootName + ".zip"
}

func setUntrustedDownloadHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", untrustedDownloadCSP)
}

func setPublicShareJSONHeaders(w http.ResponseWriter) {
	header := w.Header()
	header.Set("Cache-Control", "private, no-cache")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	appendVaryHeader(header, "Cookie")
}

func appendVaryHeader(header http.Header, token string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	for _, value := range header.Values("Vary") {
		for _, existing := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(existing), token) {
				return
			}
		}
	}
	header.Add("Vary", token)
}

func prefetchDownloadChunk(reader io.Reader) ([]byte, bool, error) {
	buf := make([]byte, 32*1024)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if err != nil && !errors.Is(err, io.EOF) {
				return nil, false, err
			}
			return buf[:n], errors.Is(err, io.EOF), nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, true, nil
			}
			return nil, false, err
		}
	}
}

func streamDownload(w http.ResponseWriter, reader io.Reader, firstChunk []byte, exhausted bool) error {
	trackingWriter := &responseStartTrackingWriter{ResponseWriter: w}
	if len(firstChunk) > 0 {
		if _, err := trackingWriter.Write(firstChunk); err != nil {
			return &streamResponseError{err: err, responseStarted: trackingWriter.started}
		}
	}
	if exhausted {
		return nil
	}
	_, err := io.Copy(trackingWriter, reader)
	if err != nil {
		return &streamResponseError{err: err, responseStarted: trackingWriter.started}
	}
	return nil
}

type streamResponseError struct {
	err             error
	responseStarted bool
}

func (e *streamResponseError) Error() string {
	return e.err.Error()
}

func (e *streamResponseError) Unwrap() error {
	return e.err
}

func streamResponseStarted(err error) bool {
	var streamErr *streamResponseError
	return errors.As(err, &streamErr) && streamErr.responseStarted
}

type responseStartTrackingWriter struct {
	http.ResponseWriter
	started bool
}

func (w *responseStartTrackingWriter) WriteHeader(statusCode int) {
	w.started = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseStartTrackingWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if err == nil || n > 0 {
		w.started = true
	}
	return n, err
}

// ListShareItems lists items within a shared folder.
func (h *Handler) ListShareItems(w http.ResponseWriter, r *http.Request) {
	setPublicShareJSONHeaders(w)
	id := chi.URLParam(r, "id")
	relPath, err := shareListPathFromRequest(r)
	if err != nil {
		writeShareError(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
		return
	}

	share, err := h.authorizeShare(r, id)
	if err != nil {
		writePublicShareAccessError(w, err)
		return
	}

	if share.Type != ShareTypeFolder {
		writeShareError(w, http.StatusBadRequest, "share is not a folder", "INVALID_SHARE_TYPE")
		return
	}

	statProvider, ok := h.fs.(FileStatProvider)
	if !ok {
		writeShareError(w, http.StatusServiceUnavailable, "filesystem not available", "FILESYSTEM_UNAVAILABLE")
		return
	}

	fullPath := share.Path
	if relPath != "" {
		fullPath = path.Join(share.Path, relPath)
	}
	if !isWithinSharePath(share.Path, fullPath) {
		writeShareError(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
		return
	}

	if err := h.authorizeSharePath(r.Context(), share, fullPath); err != nil {
		writePublicSharePathError(w, err, "LIST_SHARE_ITEMS_FAILED")
		return
	}

	entries, err := statProvider.ReadDir(r.Context(), fullPath)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeShareError(w, http.StatusNotFound, "file not found", "FILE_NOT_FOUND")
			return
		}
		if errors.Is(err, storage.ErrNotDir) {
			writeShareError(w, http.StatusBadRequest, "path is not a directory", "INVALID_PATH")
			return
		}
		writeShareError(w, http.StatusInternalServerError, "internal server error", "LIST_SHARE_ITEMS_FAILED")
		return
	}

	items := make([]*PublicShareItem, 0, len(entries))
	for _, entry := range entries {
		entryPath, entryName, err := shareReadDirChildPath(fullPath, entry)
		if err != nil {
			if errors.Is(err, ErrShareNotFound) {
				continue
			}
			writeShareError(w, http.StatusInternalServerError, "internal server error", "LIST_SHARE_ITEMS_FAILED")
			return
		}
		relItemPath, relErr := shareRelativePath(share.Path, entryPath)
		if relErr != nil {
			if errors.Is(relErr, ErrShareNotFound) {
				continue
			}
			writeShareError(w, http.StatusInternalServerError, "internal server error", "LIST_SHARE_ITEMS_FAILED")
			return
		}
		if err := h.authorizeSharePath(r.Context(), share, entryPath); err != nil {
			if errors.Is(err, ErrShareNotFound) {
				continue
			}
			writeShareError(w, http.StatusInternalServerError, "internal server error", "LIST_SHARE_ITEMS_FAILED")
			return
		}
		if relItemPath == "." {
			relItemPath = entryName
		}
		items = append(items, &PublicShareItem{
			Name:    entryName,
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

	accessReservation, err := h.reserveAuthorizedAccessForShare(share)
	if err != nil {
		if IsPersistenceWarning(err) {
			markSharePersistenceWarningHeaders(w)
		} else {
			writePublicShareAccessError(w, err)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	trackingWriter := &responseStartTrackingWriter{ResponseWriter: w}
	if err := json.NewEncoder(trackingWriter).Encode(resp); err != nil {
		if trackingWriter.started {
			return
		}
		if rollbackErr := h.store.rollbackAuthorizedAccess(accessReservation); rollbackErr != nil {
			if IsPersistenceWarning(rollbackErr) {
				markSharePersistenceWarningHeaders(w)
			} else {
				writeShareError(w, http.StatusInternalServerError, "internal server error", "LIST_SHARE_ITEMS_ROLLBACK_FAILED")
				return
			}
		}
		writeShareError(w, http.StatusInternalServerError, "internal server error", "LIST_SHARE_ITEMS_FAILED")
		return
	}
}

func (h *Handler) enrichPublicShareInfo(ctx context.Context, info *PublicShareInfo, share *Share) error {
	info.FileName = shareArchiveRootName(share.Path)

	statProvider, ok := h.fs.(FileStatProvider)
	if !ok {
		return nil
	}

	switch share.Type {
	case ShareTypeFile:
		if err := h.authorizeSharePath(ctx, share, share.Path); err != nil {
			return err
		}
		fileInfo, err := statProvider.Stat(ctx, share.Path)
		if err != nil {
			return err
		}
		if fileInfo.IsDir {
			return storage.ErrIsDir
		}
		fileSize := fileInfo.Size
		info.FileSize = &fileSize
	case ShareTypeFolder:
		if err := h.authorizeSharePath(ctx, share, share.Path); err != nil {
			return err
		}
		entries, err := statProvider.ReadDir(ctx, share.Path)
		if err != nil {
			return err
		}
		authorizedEntries, err := h.filterAuthorizedShareEntries(ctx, share, share.Path, entries)
		if err != nil {
			return err
		}
		folderItems := len(authorizedEntries)
		info.FolderItems = &folderItems
	}

	return nil
}

func (h *Handler) authorizeSharePath(ctx context.Context, share *Share, targetPath string) error {
	if share == nil {
		return ErrShareNotFound
	}

	cleanTarget := path.Clean(targetPath)
	if !isWithinSharePath(share.Path, cleanTarget) {
		return ErrShareNotFound
	}
	if h.pathAccessAuthorizer == nil {
		return nil
	}
	return h.pathAccessAuthorizer(ctx, share, cleanTarget)
}

func (h *Handler) filterAuthorizedShareEntries(ctx context.Context, share *Share, parentPath string, entries []*storage.FileInfo) ([]*storage.FileInfo, error) {
	filtered := make([]*storage.FileInfo, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		entryPath, entryName, err := shareReadDirChildPath(parentPath, entry)
		if err != nil {
			if errors.Is(err, ErrShareNotFound) {
				continue
			}
			return nil, err
		}
		if err := h.authorizeSharePath(ctx, share, entryPath); err != nil {
			if errors.Is(err, ErrShareNotFound) {
				continue
			}
			return nil, err
		}
		entryInfo := *entry
		entryInfo.Path = entryPath
		entryInfo.Name = entryName
		filtered = append(filtered, &entryInfo)
	}
	return filtered, nil
}

func shareReadDirChildPath(parentPath string, child *storage.FileInfo) (string, string, error) {
	if child == nil {
		return "", "", ErrShareNotFound
	}
	cleanParent := path.Clean(parentPath)
	childPath := child.Path
	if strings.TrimSpace(childPath) == "" {
		childName := strings.ReplaceAll(child.Name, "\\", "/")
		if strings.ContainsRune(childName, '\x00') || hasShareDotSegment(childName) {
			return "", "", ErrShareNotFound
		}
		childPath = path.Join(cleanParent, childName)
	}
	normalizedChildPath := strings.ReplaceAll(childPath, "\\", "/")
	if strings.ContainsRune(normalizedChildPath, '\x00') || hasShareDotSegment(normalizedChildPath) {
		return "", "", ErrShareNotFound
	}
	cleanChild := path.Clean(normalizedChildPath)
	if cleanChild == cleanParent || path.Dir(cleanChild) != cleanParent {
		return "", "", ErrShareNotFound
	}
	return cleanChild, path.Base(cleanChild), nil
}

func writePublicSharePathError(w http.ResponseWriter, err error, fallbackCode string) {
	if errors.Is(err, ErrShareNotFound) {
		writeShareError(w, http.StatusNotFound, "file not found", "FILE_NOT_FOUND")
		return
	}
	writeShareError(w, http.StatusInternalServerError, "internal server error", fallbackCode)
}

func (h *Handler) writePublicShareInfoError(w http.ResponseWriter, share *Share, err error) {
	if errors.Is(err, ErrShareNotFound) {
		writeShareError(w, http.StatusNotFound, "file not found", "FILE_NOT_FOUND")
		return
	}
	if errors.Is(err, storage.ErrNotFound) {
		writeShareError(w, http.StatusNotFound, "file not found", "FILE_NOT_FOUND")
		return
	}
	if share.Type == ShareTypeFile && errors.Is(err, storage.ErrNotDir) {
		writeShareError(w, http.StatusNotFound, "file not found", "FILE_NOT_FOUND")
		return
	}

	if share.Type == ShareTypeFile && errors.Is(err, storage.ErrIsDir) {
		writeShareError(w, http.StatusBadRequest, "shared resource is a directory", "INVALID_SHARE_TYPE")
		return
	}
	if share.Type == ShareTypeFolder && errors.Is(err, storage.ErrNotDir) {
		writeShareError(w, http.StatusBadRequest, "shared resource is not a folder", "INVALID_SHARE_TYPE")
		return
	}

	writeShareError(w, http.StatusInternalServerError, "internal server error", "GET_SHARE_FAILED")
}

func (h *Handler) buildShareURL(id string) string {
	if h.baseURL != "" {
		return h.baseURL + "/s/" + id
	}
	return "/s/" + id
}

func (h *Handler) authorizeShare(r *http.Request, id string) (*Share, error) {
	share, err := h.store.Get(id)
	if err != nil {
		return nil, err
	}

	if err := share.CanAccess(); err != nil {
		return nil, err
	}
	if err := h.ensureShareOwnerActive(share); err != nil {
		return nil, err
	}

	if share.HasPassword() && !h.hasShareAccess(r, share) {
		return nil, ErrInvalidPassword
	}

	return share, nil
}

func (h *Handler) ensureShareOwnerActive(share *Share) error {
	if h.userStore == nil || share == nil || share.CreatedBy == "" {
		return nil
	}

	owner, err := resolveShareOwner(h.userStore, share.CreatedBy)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return ErrShareDisabled
		}
		return err
	}
	if owner.Disabled {
		return ErrShareDisabled
	}
	return nil
}

func (h *Handler) reserveAuthorizedAccessForShare(share *Share) (*authorizedAccessReservation, error) {
	if share == nil {
		return nil, ErrShareNotFound
	}

	_, reservation, err := h.store.reserveAuthorizedAccess(share.ID)
	if err != nil && !IsPersistenceWarning(err) {
		return nil, err
	}
	if err := h.ensureShareOwnerActive(share); err != nil {
		if rollbackErr := h.store.rollbackAuthorizedAccess(reservation); rollbackErr != nil && !IsPersistenceWarning(rollbackErr) {
			return nil, errors.Join(err, rollbackErr)
		}
		return nil, err
	}

	return reservation, err
}

func (h *Handler) hasShareAccess(r *http.Request, share *Share) bool {
	if share == nil || !share.HasPassword() {
		return true
	}

	cookieName := shareAccessCookieName(share.ID)
	expected := h.shareAccessToken(share)
	for _, cookie := range r.Cookies() {
		if cookie.Name != cookieName {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(expected)) == 1 {
			return true
		}
	}
	return false
}

func (h *Handler) setShareAccessCookie(w http.ResponseWriter, r *http.Request, share *Share) {
	if share == nil || !share.HasPassword() {
		return
	}

	for _, cookiePath := range shareAccessCookiePaths(share.ID) {
		cookie := &http.Cookie{
			Name:     shareAccessCookieName(share.ID),
			Value:    h.shareAccessToken(share),
			Path:     cookiePath,
			HttpOnly: true,
			SameSite: shareAccessSameSite,
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
}

func (h *Handler) shareAccessToken(share *Share) string {
	sum := sha256.Sum256([]byte(share.ID + ":" + share.PasswordHash))
	return hex.EncodeToString(sum[:])
}

func shareAccessCookiePaths(id string) []string {
	return []string{"/s/" + id, "/api/v1/public/shares/" + id}
}

func shareAccessCookieName(id string) string {
	return shareAccessCookiePrefix + id
}

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !requestip.IsTrustedForwardedSource(requestip.RemoteIP(r.RemoteAddr)) {
		return false
	}
	if requestip.TrustedProxyHops() <= 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

var marshalShareJSON = func(data any) ([]byte, error) {
	return json.Marshal(data)
}

func writeShareJSONPayload(w http.ResponseWriter, status int, payload []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func writeShareJSON(w http.ResponseWriter, status int, data any) bool {
	payload, err := marshalShareJSON(data)
	if err != nil {
		writeShareJSONPayload(w, http.StatusInternalServerError, []byte(`{"success":false,"error":{"code":"INTERNAL_ERROR","message":"internal server error"}}`))
		return false
	}

	writeShareJSONPayload(w, status, payload)
	return true
}

func writePublicShareInfo(w http.ResponseWriter, info *PublicShareInfo) bool {
	payload, err := marshalShareJSON(info)
	if err != nil {
		writeShareError(w, http.StatusInternalServerError, "internal server error", "GET_SHARE_FAILED")
		return false
	}

	writeShareJSONPayload(w, http.StatusOK, payload)
	return true
}

func writeShareSuccess(w http.ResponseWriter, status int, data interface{}, message string) {
	writeShareSuccessEnvelope(w, status, data, message, false)
}

func writeShareSuccessWithWarning(w http.ResponseWriter, status int, data interface{}, message string) {
	markSharePersistenceWarningHeaders(w)
	writeShareSuccessEnvelope(w, status, data, message, true)
}

func markSharePersistenceWarningHeaders(w http.ResponseWriter) {
	headers := w.Header()
	for _, warningValue := range headers.Values("Warning") {
		if warningValue == sharePersistenceWarningHeader {
			return
		}
	}
	headers.Add("Warning", sharePersistenceWarningHeader)
}

func writeShareSuccessEnvelope(w http.ResponseWriter, status int, data interface{}, message string, warning bool) {
	if data == nil {
		data = json.RawMessage("null")
	}
	writeShareJSON(w, status, responseEnvelope{
		Success: true,
		Data:    data,
		Warning: warning,
		Message: message,
	})
}

func writeShareError(w http.ResponseWriter, status int, message, code string) {
	writeShareJSON(w, status, responseEnvelope{
		Success: false,
		Error: &errorDetail{
			Code:    code,
			Message: message,
		},
	})
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
		return "", ErrShareNotFound
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

func resolveShareOwner(store shareOwnerStore, ownerRef string) (*auth.User, error) {
	if store == nil || strings.TrimSpace(ownerRef) == "" {
		return nil, auth.ErrUserNotFound
	}

	owner, err := store.GetByID(ownerRef)
	if err == nil {
		return owner, nil
	}
	if !errors.Is(err, auth.ErrUserNotFound) {
		return nil, err
	}

	owner, err = store.GetByUsername(ownerRef)
	if err != nil {
		return nil, err
	}
	return owner, nil
}

func shareOwnedByRequester(ctx context.Context, share *Share) bool {
	if share == nil {
		return false
	}
	if getIsAdminFromContext(ctx) {
		return true
	}
	for _, ownerIdentifier := range getShareOwnerIdentifiersFromContext(ctx) {
		if share.CreatedBy == ownerIdentifier {
			return true
		}
	}
	return false
}

func getShareOwnerIdentifiersFromContext(ctx context.Context) []string {
	claims := auth.GetClaimsFromContext(ctx)
	if claims == nil {
		return nil
	}

	identifiers := make([]string, 0, 2)
	appendUnique := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		for _, existing := range identifiers {
			if existing == trimmed {
				return
			}
		}
		identifiers = append(identifiers, trimmed)
	}

	appendUnique(claims.UserID)
	appendUnique(claims.Username)
	return identifiers
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
	if err := validateSharePassword(password); err != nil {
		return "", err
	}
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

	validatePositiveDuration := func(duration time.Duration) (time.Duration, error) {
		if duration <= 0 {
			return 0, errors.New("duration must be greater than zero")
		}
		return duration, nil
	}

	if strings.HasSuffix(s, "d") {
		days, err := strconv.ParseInt(strings.TrimSuffix(s, "d"), 10, 64)
		if err != nil {
			return 0, err
		}
		if days <= 0 {
			return 0, errors.New("duration must be greater than zero")
		}
		if days > maxDurationDays {
			return 0, errors.New("duration too large")
		}
		return validatePositiveDuration(time.Duration(days) * 24 * time.Hour)
	}

	duration, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	return validatePositiveDuration(duration)
}
