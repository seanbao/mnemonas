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
	ReadDirLimit(ctx context.Context, filePath string, limit int) ([]*storage.FileInfo, error)
}

type MutationLease interface {
	Stat(ctx context.Context, filePath string) (*storage.FileInfo, error)
	Release()
}

type MutationLeaseProvider interface {
	AcquireMutationLease(ctx context.Context) (MutationLease, error)
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
	downloadTicketKeyMu   sync.RWMutex
	downloadTicketKey     []byte
	downloadTicketRandom  io.Reader
	downloadTicketNow     func() time.Time
	passwordAttempts      *passwordAttemptTracker
	passwordFailureLimit  int
	passwordFailureWindow time.Duration
	passwordFailureDelay  time.Duration
	passwordLockDuration  time.Duration
	passwordCheckGate     chan struct{}
	beforePasswordCheck   func(id string)
	beforeMutateShare     func(id string) error
	pathAccessAuthorizer  PathAccessAuthorizer
	publicArchiveGate     chan struct{}
	downloadTicketGate    chan struct{}
}

type shareOwnerStore interface {
	GetByID(id string) (*auth.User, error)
	GetByUsername(username string) (*auth.User, error)
}

type passwordAttemptTracker struct {
	mu             sync.Mutex
	partitions     map[string]*passwordAttemptPartition
	now            func() time.Time
	capacity       int
	globalCapacity int
	total          int
}

type passwordAttemptPartition struct {
	attempts map[string]passwordAttemptState
}

type passwordAttemptState struct {
	failures    int
	lastFailure time.Time
	lockedUntil time.Time
	lastSeen    time.Time
	inFlight    bool
}

type passwordAttemptResult uint8

const (
	passwordAttemptCancelled passwordAttemptResult = iota
	passwordAttemptSucceeded
	passwordAttemptFailed
)

type PublicShareAccessPolicy struct {
	CookieNamePrefix              string
	CookiePaths                   []string
	CookieHTTPOnly                bool
	CookieSameSite                http.SameSite
	MetadataCacheControl          string
	MetadataVaryCookie            bool
	MetadataNosniff               bool
	MetadataReferrerPolicy        string
	PasswordFailureLimit          int
	PasswordFailureWindow         time.Duration
	PasswordLockDuration          time.Duration
	PasswordAttemptCapacity       int
	PasswordGlobalAttemptCapacity int
	PasswordConcurrentLimit       int
	PasswordBcryptConcurrency     int
	DownloadTicketCookiePrefix    string
	DownloadTicketSecurePrefix    string
	DownloadTicketCookiePath      string
	DownloadTicketTTL             time.Duration
	DownloadTicketCookieLimit     int
	DownloadTicketConcurrency     int
	PublicArchiveConcurrency      int
}

const (
	shareAccessCookiePrefix              = "mnemonas_share_"
	sharePersistenceWarningHeader        = `199 MnemoNAS "share persistence incomplete"`
	defaultPasswordFailureLimit          = 5
	defaultPasswordFailureWindow         = 15 * time.Minute
	defaultPasswordFailureDelay          = 200 * time.Millisecond
	defaultPasswordLockDuration          = 5 * time.Minute
	defaultPasswordAttemptCapacity       = 128
	defaultPasswordGlobalAttemptCapacity = 4096
	defaultPasswordBcryptConcurrent      = 8
	defaultRateLimitErrorMessage         = "too many attempts, try later"
	defaultJSONRequestBodyLimit          = 1 * 1024 * 1024
	defaultPublicArchiveConcurrent       = 4
	defaultDownloadTicketConcurrent      = 4
	maxShareArchiveEntries               = 10000
	maxDurationDays                      = int64((1<<63 - 1) / int64(24*time.Hour))
	untrustedAttachmentCSP               = "sandbox allow-downloads; default-src 'none'; base-uri 'none'; object-src 'none'; frame-ancestors 'self'; img-src 'self' data: blob:; media-src 'self' data: blob:; style-src 'unsafe-inline'"
	untrustedEmbeddedErrorCSP            = "sandbox; default-src 'none'; base-uri 'none'; object-src 'none'; frame-ancestors 'self'"
	shareAccessSameSite                  = http.SameSiteStrictMode
)

var (
	maxShareArchiveBytes           = int64(20 * 1024 * 1024 * 1024)
	errInvalidSharePermission      = errors.New("invalid permission")
	errUnsupportedShareArchive     = errors.New("unsupported archive format")
	errInvalidShareArchivePath     = errors.New("invalid archive path")
	errShareArchiveTooManyEntries  = errors.New("archive contains too many entries")
	errShareArchiveContentTooLarge = errors.New("archive content is too large")
	errShareArchiveDuplicateEntry  = errors.New("archive contains duplicate entry names")
	errShareArchiveMissingMetadata = errors.New("archive entry missing metadata")
	errShareArchiveSnapshotChanged = errors.New("archive entry snapshot changed")
)

func newPasswordAttemptTracker() *passwordAttemptTracker {
	return &passwordAttemptTracker{
		partitions:     make(map[string]*passwordAttemptPartition),
		now:            time.Now,
		capacity:       defaultPasswordAttemptCapacity,
		globalCapacity: defaultPasswordGlobalAttemptCapacity,
	}
}

func PublicShareAccessPolicySnapshot() PublicShareAccessPolicy {
	return PublicShareAccessPolicy{
		CookieNamePrefix:              shareAccessCookiePrefix,
		CookiePaths:                   []string{"/s/{share_id}", "/api/v1/public/shares/{share_id}"},
		CookieHTTPOnly:                true,
		CookieSameSite:                shareAccessSameSite,
		MetadataCacheControl:          "private, no-cache",
		MetadataVaryCookie:            true,
		MetadataNosniff:               true,
		MetadataReferrerPolicy:        "no-referrer",
		PasswordFailureLimit:          defaultPasswordFailureLimit,
		PasswordFailureWindow:         defaultPasswordFailureWindow,
		PasswordLockDuration:          defaultPasswordLockDuration,
		PasswordAttemptCapacity:       defaultPasswordAttemptCapacity,
		PasswordGlobalAttemptCapacity: defaultPasswordGlobalAttemptCapacity,
		PasswordConcurrentLimit:       1,
		PasswordBcryptConcurrency:     defaultPasswordBcryptConcurrent,
		DownloadTicketCookiePrefix:    downloadTicketCookiePrefix,
		DownloadTicketSecurePrefix:    downloadTicketSecureCookiePrefix,
		DownloadTicketCookiePath:      downloadTicketCookiePath,
		DownloadTicketTTL:             defaultDownloadTicketTTL,
		DownloadTicketCookieLimit:     maxDownloadTicketCookies,
		DownloadTicketConcurrency:     defaultDownloadTicketConcurrent,
		PublicArchiveConcurrency:      defaultPublicArchiveConcurrent,
	}
}

// begin atomically admits at most one password verification for a share/client
// pair. The returned completion function must be called exactly once.
func (t *passwordAttemptTracker) begin(shareID, clientID string, limit int, failureWindow, lockDuration time.Duration) (func(passwordAttemptResult) time.Duration, time.Duration, bool) {
	t.mu.Lock()
	now := t.now()
	partition := t.partitions[shareID]
	if partition != nil {
		t.prunePartitionLocked(shareID, partition, now, failureWindow)
		partition = t.partitions[shareID]
	}
	state, ok := passwordAttemptState{}, false
	if partition != nil {
		state, ok = partition.attempts[clientID]
	}
	if ok && state.lockedUntil.After(now) {
		retryAfter := state.lockedUntil.Sub(now)
		t.mu.Unlock()
		return nil, retryAfter, false
	}
	if ok && state.inFlight {
		t.mu.Unlock()
		return nil, time.Second, false
	}
	if !ok {
		if partition != nil && !t.makeClientRoomLocked(shareID, partition, now) {
			t.mu.Unlock()
			return nil, time.Second, false
		}
		if !t.makeGlobalRoomLocked(now, failureWindow) {
			t.mu.Unlock()
			return nil, time.Second, false
		}
		partition = t.partitions[shareID]
		if partition == nil {
			partition = &passwordAttemptPartition{attempts: make(map[string]passwordAttemptState)}
			t.partitions[shareID] = partition
		}
		t.total++
	}
	if failureWindow > 0 && !state.lastFailure.IsZero() && now.Sub(state.lastFailure) > failureWindow {
		state = passwordAttemptState{}
	}
	state.inFlight = true
	state.lastSeen = now
	partition.attempts[clientID] = state
	t.mu.Unlock()

	var once sync.Once
	var retryAfter time.Duration
	return func(result passwordAttemptResult) time.Duration {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()

			partition := t.partitions[shareID]
			if partition == nil {
				return
			}
			current, exists := partition.attempts[clientID]
			if !exists {
				return
			}
			if result == passwordAttemptSucceeded {
				delete(partition.attempts, clientID)
				t.total--
				if len(partition.attempts) == 0 {
					delete(t.partitions, shareID)
				}
				return
			}

			now := t.now()
			current.inFlight = false
			if result == passwordAttemptCancelled {
				if current.failures == 0 && !current.lockedUntil.After(now) {
					delete(partition.attempts, clientID)
					t.total--
				} else {
					current.lastSeen = now
					partition.attempts[clientID] = current
				}
				if len(partition.attempts) == 0 {
					delete(t.partitions, shareID)
				}
				return
			}
			current.failures++
			current.lastFailure = now
			current.lastSeen = now
			if limit <= 0 || current.failures >= limit {
				current.lockedUntil = now.Add(lockDuration)
				retryAfter = lockDuration
			}
			partition.attempts[clientID] = current
		})
		return retryAfter
	}, 0, true
}

func (t *passwordAttemptTracker) makeClientRoomLocked(shareID string, partition *passwordAttemptPartition, now time.Time) bool {
	if t.capacity <= 0 {
		return false
	}
	if len(partition.attempts) < t.capacity {
		return true
	}

	var oldestKey string
	var oldestSeen time.Time
	for key, state := range partition.attempts {
		if state.inFlight || state.failures > 0 || state.lockedUntil.After(now) {
			continue
		}
		if oldestKey == "" || state.lastSeen.Before(oldestSeen) {
			oldestKey = key
			oldestSeen = state.lastSeen
		}
	}
	if oldestKey == "" {
		return false
	}
	delete(partition.attempts, oldestKey)
	t.total--
	if len(partition.attempts) == 0 {
		delete(t.partitions, shareID)
	}
	return true
}

func (t *passwordAttemptTracker) makeGlobalRoomLocked(now time.Time, failureWindow time.Duration) bool {
	if t.globalCapacity <= 0 {
		return false
	}
	if t.total < t.globalCapacity {
		return true
	}
	for shareID, partition := range t.partitions {
		t.prunePartitionLocked(shareID, partition, now, failureWindow)
	}
	if t.total < t.globalCapacity {
		return true
	}

	var oldestShareID string
	var oldestClientID string
	var oldestSeen time.Time
	for shareID, partition := range t.partitions {
		for clientID, state := range partition.attempts {
			if state.inFlight || state.failures > 0 || state.lockedUntil.After(now) {
				continue
			}
			if oldestClientID == "" || state.lastSeen.Before(oldestSeen) {
				oldestShareID = shareID
				oldestClientID = clientID
				oldestSeen = state.lastSeen
			}
		}
	}
	if oldestClientID == "" {
		return false
	}
	partition := t.partitions[oldestShareID]
	delete(partition.attempts, oldestClientID)
	t.total--
	if len(partition.attempts) == 0 {
		delete(t.partitions, oldestShareID)
	}
	return true
}

func (t *passwordAttemptTracker) prunePartitionLocked(shareID string, partition *passwordAttemptPartition, now time.Time, failureWindow time.Duration) {
	for clientID, state := range partition.attempts {
		if state.inFlight {
			continue
		}
		if !state.lockedUntil.IsZero() && !state.lockedUntil.After(now) {
			delete(partition.attempts, clientID)
			t.total--
			continue
		}
		if failureWindow > 0 && !state.lastFailure.IsZero() && now.Sub(state.lastFailure) > failureWindow {
			delete(partition.attempts, clientID)
			t.total--
		}
	}
	if len(partition.attempts) == 0 {
		delete(t.partitions, shareID)
	}
}

// NewHandler creates a new share handler
// fs can be nil if file download is not needed
func NewHandler(store *ShareStore, fs FileOpener) *Handler {
	h := &Handler{
		store:                 store,
		fs:                    fs,
		downloadTicketRandom:  defaultDownloadTicketRandomReader,
		downloadTicketNow:     time.Now,
		passwordAttempts:      newPasswordAttemptTracker(),
		passwordFailureLimit:  defaultPasswordFailureLimit,
		passwordFailureWindow: defaultPasswordFailureWindow,
		passwordFailureDelay:  defaultPasswordFailureDelay,
		passwordLockDuration:  defaultPasswordLockDuration,
		passwordCheckGate:     make(chan struct{}, defaultPasswordBcryptConcurrent),
		publicArchiveGate:     make(chan struct{}, defaultPublicArchiveConcurrent),
		downloadTicketGate:    make(chan struct{}, defaultDownloadTicketConcurrent),
	}
	h.downloadTicketKey = newEphemeralDownloadTicketKey()
	return h
}

func (h *Handler) acquirePasswordCheck() (func(), bool) {
	if h == nil || h.passwordCheckGate == nil {
		return nil, false
	}
	select {
	case h.passwordCheckGate <- struct{}{}:
		return func() { <-h.passwordCheckGate }, true
	default:
		return nil, false
	}
}

func (h *Handler) acquireDownloadTicketIssuance(w http.ResponseWriter) (func(), bool) {
	if h == nil || h.downloadTicketGate == nil {
		writeShareError(w, http.StatusServiceUnavailable, "download tickets unavailable", "DOWNLOAD_TICKET_UNAVAILABLE")
		return nil, false
	}
	select {
	case h.downloadTicketGate <- struct{}{}:
		return func() { <-h.downloadTicketGate }, true
	default:
		w.Header().Set("Retry-After", "1")
		writeShareError(w, http.StatusTooManyRequests, "too many download ticket requests, try later", "DOWNLOAD_TICKET_RATE_LIMITED")
		return nil, false
	}
}

func (h *Handler) acquirePublicArchive(w http.ResponseWriter) (func(), bool) {
	if h == nil || h.publicArchiveGate == nil {
		writeShareError(w, http.StatusServiceUnavailable, "archive downloads unavailable", "DOWNLOAD_TICKET_UNAVAILABLE")
		return nil, false
	}
	select {
	case h.publicArchiveGate <- struct{}{}:
		return func() { <-h.publicArchiveGate }, true
	default:
		w.Header().Set("Retry-After", "1")
		writeShareError(w, http.StatusTooManyRequests, "too many archive downloads, try later", "DOWNLOAD_TICKET_RATE_LIMITED")
		return nil, false
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
	r.Post("/{id}/download-ticket", h.CreateDownloadTicket)
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
	if containsSharePathControlCharacter(normalized) {
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
	if containsSharePathControlCharacter(normalized) {
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
	prepared, err := h.store.PrepareCreate(opts)
	if err != nil {
		if errors.Is(err, errSharePasswordLong) {
			writeShareError(w, http.StatusBadRequest, "share password must be at most 72 bytes", "PASSWORD_TOO_LONG")
			return
		}
		writeShareError(w, http.StatusInternalServerError, "internal server error", "CREATE_SHARE_FAILED")
		return
	}

	_, ok := h.fs.(FileStatProvider)
	if !ok {
		writeShareError(w, http.StatusServiceUnavailable, "filesystem not available", "FILESYSTEM_UNAVAILABLE")
		return
	}
	leaseProvider, ok := h.fs.(MutationLeaseProvider)
	if !ok {
		writeShareError(w, http.StatusServiceUnavailable, "filesystem mutation lease unavailable", "FILESYSTEM_UNAVAILABLE")
		return
	}
	mutationLease, err := leaseProvider.AcquireMutationLease(r.Context())
	if err != nil {
		writeShareError(w, http.StatusInternalServerError, "internal server error", "CREATE_SHARE_FAILED")
		return
	}
	defer mutationLease.Release()

	info, err := mutationLease.Stat(r.Context(), cleanPath)
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
	if err := r.Context().Err(); err != nil {
		writeShareError(w, http.StatusInternalServerError, "internal server error", "CREATE_SHARE_FAILED")
		return
	}

	share, err := h.store.CommitPreparedCreate(prepared)
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
	if rejectNonGETPublicShareRead(w, r) {
		return
	}

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
	if rejectNonPOSTPublicShareAccess(w, r) {
		return
	}

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
		if err := validateSharePassword(req.Password); err != nil {
			if errors.Is(err, errSharePasswordLong) {
				writeShareError(w, http.StatusBadRequest, "share password must be at most 72 bytes", "PASSWORD_TOO_LONG")
				return
			}
			writeShareError(w, http.StatusBadRequest, "invalid password", "INVALID_PASSWORD")
			return
		}
		finishAttempt, retryAfter, admitted := h.passwordAttempts.begin(
			id,
			clientIdentifier(r),
			h.passwordFailureLimit,
			h.passwordFailureWindow,
			h.passwordLockDuration,
		)
		if !admitted {
			writeSharePasswordRateLimit(w, retryAfter)
			return
		}
		releasePasswordCheck, acquired := h.acquirePasswordCheck()
		if !acquired {
			finishAttempt(passwordAttemptCancelled)
			writeSharePasswordRateLimit(w, time.Second)
			return
		}

		var passwordValid bool
		func() {
			result := passwordAttemptCancelled
			defer func() {
				releasePasswordCheck()
				retryAfter = finishAttempt(result)
			}()
			if h.beforePasswordCheck != nil {
				h.beforePasswordCheck(id)
			}
			passwordValid = share.CheckPassword(req.Password)
			if passwordValid {
				result = passwordAttemptSucceeded
			} else {
				result = passwordAttemptFailed
			}
		}()
		if !passwordValid {
			if h.passwordFailureDelay > 0 {
				time.Sleep(h.passwordFailureDelay)
			}
			if retryAfter > 0 {
				writeSharePasswordRateLimit(w, retryAfter)
				return
			}
			writeShareError(w, http.StatusUnauthorized, "invalid password", "INVALID_PASSWORD")
			return
		}
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
func clientIdentifier(r *http.Request) string {
	return requestip.ClientIP(r)
}

func writeSharePasswordRateLimit(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int64((retryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	writeShareError(w, http.StatusTooManyRequests, defaultRateLimitErrorMessage, "SHARE_PASSWORD_RATE_LIMITED")
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
	if rejectNonGETPublicShareRead(w, r) {
		return
	}

	id := chi.URLParam(r, "id")
	archiveFormat, err := shareArchiveFormatFromRequest(r)
	if err != nil {
		writeShareError(w, http.StatusBadRequest, "unsupported archive format", "INVALID_ARCHIVE_FORMAT")
		return
	}
	grant, err := h.readDownloadTicketGrant(r)
	if err != nil {
		writeDownloadTicketError(w, err)
		return
	}

	share, err := h.loadShareForDownloadTicket(id)
	if err != nil {
		writePublicShareAccessError(w, err)
		return
	}

	if archiveFormat == "zip" {
		if h.fs == nil {
			writeShareError(w, http.StatusServiceUnavailable, "filesystem not available", "FILESYSTEM_UNAVAILABLE")
			return
		}
		if err := h.validateDownloadTicketTarget(grant, share, share.Path, archiveFormat); err != nil {
			writeDownloadTicketValidationError(w, err)
			return
		}
		if err := h.authorizeSharePath(r.Context(), share, share.Path); err != nil {
			writePublicSharePathError(w, err, "DOWNLOAD_SHARE_FAILED")
			return
		}
		h.serveAuthorizedShareArchive(w, r, share, share.Path, grant)
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

	if err := h.validateDownloadTicketTarget(grant, share, share.Path, archiveFormat); err != nil {
		writeDownloadTicketValidationError(w, err)
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
		if errors.Is(err, storage.ErrNotRegular) {
			writeShareError(w, http.StatusConflict, "download target is not a regular file", "FILE_NOT_REGULAR")
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
	share, err = h.loadShareForDownloadTicket(id)
	if err != nil {
		writePublicShareAccessError(w, err)
		return
	}
	if err := h.validateDownloadTicketTarget(grant, share, share.Path, archiveFormat); err != nil {
		writeDownloadTicketValidationError(w, err)
		return
	}
	if err := h.authorizeSharePath(r.Context(), share, share.Path); err != nil {
		writePublicSharePathError(w, err, "DOWNLOAD_SHARE_FAILED")
		return
	}

	h.serveAuthorizedShareDownload(w, r, share, reader, share.Path)
}

// DownloadShareFile handles file download from shared folder
func (h *Handler) DownloadShareFile(w http.ResponseWriter, r *http.Request) {
	setPublicShareJSONHeaders(w)
	if rejectNonGETPublicShareRead(w, r) {
		return
	}

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
	grant, err := h.readDownloadTicketGrant(r)
	if err != nil {
		writeDownloadTicketError(w, err)
		return
	}

	share, err := h.loadShareForDownloadTicket(id)
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

	if err := h.validateDownloadTicketTarget(grant, share, fullPath, archiveFormat); err != nil {
		writeDownloadTicketValidationError(w, err)
		return
	}
	if err := h.authorizeSharePath(r.Context(), share, fullPath); err != nil {
		writePublicSharePathError(w, err, "DOWNLOAD_SHARE_FAILED")
		return
	}

	if archiveFormat == "zip" {
		h.serveAuthorizedShareArchive(w, r, share, fullPath, grant)
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
		if errors.Is(err, storage.ErrNotRegular) {
			writeShareError(w, http.StatusConflict, "download target is not a regular file", "FILE_NOT_REGULAR")
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
	share, err = h.loadShareForDownloadTicket(id)
	if err != nil {
		writePublicShareAccessError(w, err)
		return
	}
	fullPath = path.Join(share.Path, filePath)
	if err := h.validateDownloadTicketTarget(grant, share, fullPath, archiveFormat); err != nil {
		writeDownloadTicketValidationError(w, err)
		return
	}
	if err := h.authorizeSharePath(r.Context(), share, fullPath); err != nil {
		writePublicSharePathError(w, err, "DOWNLOAD_SHARE_FAILED")
		return
	}

	h.serveAuthorizedShareDownload(w, r, share, reader, fullPath)
}

func contentDispositionAttachment(filename string) string {
	value := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	if value == "" {
		return "attachment"
	}
	return value
}

func rejectNonGETPublicShareRead(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		return false
	}
	w.Header().Set("Allow", http.MethodGet)
	writeShareError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
	return true
}

func rejectNonPOSTPublicShareAccess(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodPost {
		return false
	}
	w.Header().Set("Allow", http.MethodPost)
	writeShareError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
	return true
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
	setShareDownloadHeaders(w, filePath)
	if err := streamDownload(w, reader, firstChunk, exhausted); err != nil {
		if streamResponseStarted(err) {
			return
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

type shareArchiveInternalError struct {
	operation string
	err       error
}

func (e *shareArchiveInternalError) Error() string {
	if strings.TrimSpace(e.operation) == "" {
		return "share archive operation failed"
	}
	return e.operation + " failed"
}

func (e *shareArchiveInternalError) Unwrap() error {
	return e.err
}

func newShareArchiveInternalError(operation string, err error) error {
	if err == nil {
		return &shareArchiveInternalError{operation: operation}
	}
	return &shareArchiveInternalError{operation: operation, err: err}
}

type shareArchiveCollector struct {
	handler      *Handler
	share        *Share
	statProvider FileStatProvider
	ctx          context.Context
	entries      []shareArchiveEntry
	totalBytes   int64
	discovered   int
}

func (h *Handler) serveAuthorizedShareArchive(w http.ResponseWriter, r *http.Request, share *Share, rootPath string, grants ...*downloadTicketGrant) {
	release, acquired := h.acquirePublicArchive(w)
	if !acquired {
		return
	}
	defer release()

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
	if err := validateShareArchiveEntries(entries); err != nil {
		writePublicShareArchiveError(w, err)
		return
	}
	if len(grants) > 0 && grants[0] != nil {
		currentShare, err := h.loadShareForDownloadTicket(share.ID)
		if err != nil {
			writePublicShareAccessError(w, err)
			return
		}
		if err := h.validateDownloadTicketTarget(grants[0], currentShare, rootPath, "zip"); err != nil {
			writeDownloadTicketValidationError(w, err)
			return
		}
		if err := h.authorizeSharePath(r.Context(), currentShare, rootPath); err != nil {
			writePublicSharePathError(w, err, "DOWNLOAD_SHARE_FAILED")
			return
		}
	} else if err := h.ensureShareOwnerActive(share); err != nil {
		writePublicShareAccessError(w, err)
		return
	}

	setShareArchiveDownloadHeaders(w, rootPath)
	trackingWriter := &responseStartTrackingWriter{ResponseWriter: w}
	zipWriter := zip.NewWriter(trackingWriter)
	if err := h.writeShareArchive(r.Context(), zipWriter, entries); err != nil {
		if trackingWriter.started {
			return
		}
		h.writeShareArchiveBeforeResponseError(w, err, "DOWNLOAD_SHARE_ARCHIVE_FAILED")
		return
	}
	if err := zipWriter.Close(); err != nil {
		if trackingWriter.started {
			return
		}
		h.writeShareArchiveBeforeResponseError(w, err, "DOWNLOAD_SHARE_ARCHIVE_FAILED")
		return
	}
}

func (h *Handler) writeShareArchiveBeforeResponseError(w http.ResponseWriter, err error, fallbackCode string) {
	clearShareArchiveDownloadHeaders(w)
	if isPublicShareArchiveResponseError(err) {
		writePublicShareArchiveError(w, err)
		return
	}
	writeShareError(w, http.StatusInternalServerError, "internal server error", fallbackCode)
}

func clearShareArchiveDownloadHeaders(w http.ResponseWriter) {
	header := w.Header()
	header.Del("Content-Disposition")
	header.Del("Content-Length")
	header.Del("Content-Range")
	header.Del("Accept-Ranges")
	header.Set("X-Frame-Options", "SAMEORIGIN")
	header.Set("Content-Security-Policy", untrustedEmbeddedErrorCSP)
	setPublicShareJSONHeaders(w)
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
		discovered:   1,
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

	remaining := maxShareArchiveEntries - c.discovered
	children, err := c.statProvider.ReadDirLimit(c.ctx, sourcePath, remaining+1)
	if err != nil {
		return err
	}
	if len(children) > remaining {
		return errShareArchiveTooManyEntries
	}
	c.discovered += len(children)
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
	if info == nil {
		return errShareArchiveMissingMetadata
	}
	if !info.Mode.IsRegular() {
		return storage.ErrNotRegular
	}
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
	seenNames := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.info == nil {
			return errShareArchiveMissingMetadata
		}
		zipName, err := safeShareArchiveHeaderName(entry.zipName, entry.info.IsDir)
		if err != nil {
			return err
		}
		if err := rememberShareArchiveHeaderName(seenNames, zipName); err != nil {
			return err
		}
		if entry.info.IsDir {
			header := &zip.FileHeader{
				Name:   zipName,
				Method: zip.Store,
			}
			header.SetModTime(entry.info.ModTime)
			header.SetMode(os.ModeDir | 0o755)
			if _, err := zipWriter.CreateHeader(header); err != nil {
				return newShareArchiveInternalError("create share archive directory", err)
			}
			continue
		}
		if !entry.info.Mode.IsRegular() {
			return storage.ErrNotRegular
		}

		reader, archiveInfo, err := h.openShareArchiveFile(ctx, entry)
		if err != nil {
			return newShareArchiveInternalError("open share archive file", err)
		}
		if reader == nil {
			return storage.ErrNotFound
		}
		if archiveInfo == nil {
			archiveInfo = entry.info
		}
		if archiveInfo.IsDir {
			_ = reader.Close()
			return errShareArchiveSnapshotChanged
		}
		if !archiveInfo.Mode.IsRegular() {
			_ = reader.Close()
			return storage.ErrNotRegular
		}
		if archiveInfo.Size < 0 || totalBytes > maxShareArchiveBytes-archiveInfo.Size {
			_ = reader.Close()
			return errShareArchiveContentTooLarge
		}

		header := &zip.FileHeader{
			Name:               zipName,
			Method:             zip.Deflate,
			UncompressedSize64: uint64(archiveInfo.Size),
		}
		header.SetModTime(archiveInfo.ModTime)
		header.SetMode(0o644)
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			_ = reader.Close()
			return newShareArchiveInternalError("create share archive file", err)
		}
		remaining := maxShareArchiveBytes - totalBytes
		written, copyErr := io.Copy(writer, io.LimitReader(reader, remaining+1))
		closeErr := reader.Close()
		if copyErr != nil {
			return newShareArchiveInternalError("write share archive file", copyErr)
		}
		if closeErr != nil {
			return newShareArchiveInternalError("close share archive file", closeErr)
		}
		if written > remaining {
			return errShareArchiveContentTooLarge
		}
		if written != archiveInfo.Size {
			return errShareArchiveSnapshotChanged
		}
		totalBytes += written
	}
	return nil
}

func validateShareArchiveEntries(entries []shareArchiveEntry) error {
	seenNames := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.info == nil {
			return errShareArchiveMissingMetadata
		}
		if !entry.info.IsDir && !entry.info.Mode.IsRegular() {
			return storage.ErrNotRegular
		}
		zipName, err := safeShareArchiveHeaderName(entry.zipName, entry.info.IsDir)
		if err != nil {
			return err
		}
		if err := rememberShareArchiveHeaderName(seenNames, zipName); err != nil {
			return err
		}
	}
	return nil
}

func rememberShareArchiveHeaderName(seenNames map[string]struct{}, zipName string) error {
	if _, ok := seenNames[zipName]; ok {
		return errShareArchiveDuplicateEntry
	}
	seenNames[zipName] = struct{}{}
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
		errors.Is(err, errShareArchiveDuplicateEntry) ||
		errors.Is(err, errShareArchiveSnapshotChanged) ||
		errors.Is(err, errInvalidShareArchivePath) ||
		errors.Is(err, storage.ErrNotRegular) ||
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
	case errors.Is(err, errShareArchiveDuplicateEntry):
		writeShareError(w, http.StatusConflict, "archive contains duplicate entries", "ARCHIVE_DUPLICATE_ENTRY")
	case errors.Is(err, errShareArchiveSnapshotChanged):
		writeShareError(w, http.StatusConflict, "archive entry changed during download", "ARCHIVE_ENTRY_CHANGED")
	case errors.Is(err, storage.ErrNotRegular):
		writeShareError(w, http.StatusConflict, "archive entry is not a regular file", "ARCHIVE_ENTRY_NOT_REGULAR")
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
			if suffixLength > 0 && size > 0 {
				hasRange = true
			}
			continue
		}

		start, err := strconv.ParseInt(startText, 10, 64)
		if err != nil || start < 0 {
			return false
		}
		if start >= size {
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
	return hasRange
}

func requestHasRangeHeader(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("Range")) != ""
}

func setShareArchiveDownloadHeaders(w http.ResponseWriter, rootPath string) {
	setUntrustedAttachmentDownloadHeaders(w)
	w.Header().Set("Content-Disposition", contentDispositionAttachment(shareArchiveFilename(rootPath)))
	w.Header().Set("Content-Type", "application/zip")
}

func setShareDownloadHeaders(w http.ResponseWriter, filePath string) {
	filename := path.Base(filePath)
	setUntrustedAttachmentDownloadHeaders(w)
	w.Header().Set("Content-Disposition", contentDispositionAttachment(filename))
	w.Header().Set("Content-Type", shareDownloadContentType(filePath))
}

func safeShareArchiveEntryName(name string) (string, error) {
	if strings.Contains(name, "\\") {
		return "", errInvalidShareArchivePath
	}
	normalized := name
	if normalized == "" || containsSharePathControlCharacter(normalized) || strings.HasPrefix(normalized, "/") || strings.Contains(normalized, ":") || hasShareDotSegment(normalized) {
		return "", errInvalidShareArchivePath
	}
	cleaned := path.Clean(normalized)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errInvalidShareArchivePath
	}
	return cleaned, nil
}

func safeShareArchiveHeaderName(name string, isDir bool) (string, error) {
	if strings.Contains(name, "\\") {
		return "", errInvalidShareArchivePath
	}
	normalized := name
	if isDir {
		trimmed := strings.TrimRight(normalized, "/")
		cleaned, err := safeShareArchiveEntryName(trimmed)
		if err != nil {
			return "", err
		}
		return cleaned + "/", nil
	}
	if strings.HasSuffix(normalized, "/") {
		return "", errInvalidShareArchivePath
	}
	return safeShareArchiveEntryName(normalized)
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

func setUntrustedAttachmentDownloadHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", untrustedAttachmentCSP)
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
	// net/http can commit the response headers before the underlying write
	// reports a zero-byte transport error, so treat every Write attempt as a
	// committed response.
	w.started = true
	return w.ResponseWriter.Write(p)
}

// ListShareItems lists items within a shared folder.
func (h *Handler) ListShareItems(w http.ResponseWriter, r *http.Request) {
	setPublicShareJSONHeaders(w)
	if rejectNonGETPublicShareRead(w, r) {
		return
	}

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
	if err := h.ensureShareOwnerActive(share); err != nil {
		writePublicShareAccessError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	trackingWriter := &responseStartTrackingWriter{ResponseWriter: w}
	if err := json.NewEncoder(trackingWriter).Encode(resp); err != nil {
		if trackingWriter.started {
			return
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

	cleanTarget, ok := cleanShareRuntimePath(targetPath)
	if !ok {
		return ErrShareNotFound
	}
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
		childName, err := safeShareFallbackChildName(child.Name)
		if err != nil {
			return "", "", err
		}
		childPath = path.Join(cleanParent, childName)
	}
	if strings.Contains(childPath, "\\") || containsSharePathControlCharacter(childPath) || hasShareDotSegment(childPath) {
		return "", "", ErrShareNotFound
	}
	cleanChild := path.Clean(childPath)
	if cleanChild == cleanParent || path.Dir(cleanChild) != cleanParent {
		return "", "", ErrShareNotFound
	}
	return cleanChild, path.Base(cleanChild), nil
}

func safeShareFallbackChildName(name string) (string, error) {
	childName := strings.ReplaceAll(name, "\\", "/")
	if childName == "" || strings.Contains(childName, "/") || containsSharePathControlCharacter(childName) || hasShareDotSegment(childName) {
		return "", ErrShareNotFound
	}
	return childName, nil
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
	return requestip.RequestIsHTTPS(r)
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
	cleanBase, ok := cleanShareRuntimePath(basePath)
	if !ok {
		return "", ErrShareNotFound
	}
	cleanEntry, ok := cleanShareRuntimePath(entryPath)
	if !ok {
		return "", ErrShareNotFound
	}

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
	var ok bool
	basePath, ok = cleanShareRuntimePath(basePath)
	if !ok {
		return false
	}
	targetPath, ok = cleanShareRuntimePath(targetPath)
	if !ok {
		return false
	}
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

func cleanShareRuntimePath(filePath string) (string, bool) {
	if filePath == "" || !strings.HasPrefix(filePath, "/") {
		return "", false
	}
	if strings.Contains(filePath, "\\") || containsSharePathControlCharacter(filePath) || hasShareDotSegment(filePath) {
		return "", false
	}
	cleaned := path.Clean(filePath)
	if cleaned == "." || !strings.HasPrefix(cleaned, "/") {
		return "", false
	}
	return cleaned, true
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
	if user := auth.GetUserFromContext(ctx); user != nil && user.Disabled {
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

	if claims := auth.GetClaimsFromContext(ctx); claims != nil {
		appendUnique(claims.UserID)
		appendUnique(claims.Username)
		return identifiers
	}
	if user := auth.GetUserFromContext(ctx); user != nil && !user.Disabled {
		appendUnique(user.ID)
		appendUnique(user.Username)
	}
	return identifiers
}

func getUserIDFromContext(ctx context.Context) string {
	if user := auth.GetUserFromContext(ctx); user != nil && user.Disabled {
		return ""
	}
	if claims := auth.GetClaimsFromContext(ctx); claims != nil && strings.TrimSpace(claims.UserID) != "" {
		return strings.TrimSpace(claims.UserID)
	}
	if user := auth.GetUserFromContext(ctx); user != nil && !user.Disabled && strings.TrimSpace(user.ID) != "" {
		return strings.TrimSpace(user.ID)
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
