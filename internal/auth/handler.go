package auth

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/seanbao/mnemonas/internal/requestip"
)

// Handler provides HTTP handlers for authentication
type Handler struct {
	userStore                  *UserStore
	tokenManager               *TokenManager
	loginAttempts              *loginAttemptTracker
	loginFailureLimit          int
	loginFailureWindow         time.Duration
	loginLockDuration          time.Duration
	loginCredentialCheckLimit  int
	loginCredentialCheckWindow time.Duration
	usageResolver              UserUsageResolver
}

type UserUsageResolver func(context.Context, *User) (int64, error)

type LoginRateLimitPolicy struct {
	Enabled               bool
	FailureLimit          int
	FailureWindow         time.Duration
	LockDuration          time.Duration
	CredentialCheckLimit  int
	CredentialCheckWindow time.Duration
}

type loginAttemptTracker struct {
	mu                           sync.Mutex
	attempts                     map[loginAttemptKey]loginAttemptState
	entriesByIP                  map[string]int
	credentialChecksByIP         map[string]loginCredentialCheckState
	credentialCheckOrder         *list.List
	unlockedFailures             *list.List
	unlockedFailuresByIP         map[string]*list.List
	maxEntries                   int
	maxEntriesPerIP              int
	maxCredentialCheckIPs        int
	lastPrunedAt                 time.Time
	lastCredentialChecksPrunedAt time.Time
	now                          func() time.Time
}

type loginAttemptKey struct {
	usernameDigest [sha256.Size]byte
	clientIP       string
}

type loginAttemptState struct {
	failures           int
	lastFailure        time.Time
	lockedUntil        time.Time
	globalOrderElement *list.Element
	ipOrderElement     *list.Element
}

type loginFailureOrderEntry struct {
	key loginAttemptKey
}

type loginCredentialCheckState struct {
	windowStartedAt time.Time
	checks          int
	orderElement    *list.Element
}

type loginCredentialCheckOrderEntry struct {
	clientIP string
}

const (
	defaultLoginFailureLimit           = 5
	defaultLoginFailureWindow          = 15 * time.Minute
	defaultLoginLockDuration           = 5 * time.Minute
	defaultLoginRateLimitMessage       = "too many login attempts, try later"
	defaultLoginAttemptMaxEntries      = 4096
	defaultLoginAttemptMaxEntriesPerIP = 64
	defaultLoginAttemptPruneInterval   = time.Minute
	defaultLoginCredentialCheckLimit   = 12
	defaultLoginCredentialCheckWindow  = 10 * time.Second
	defaultLoginCredentialCheckMaxIPs  = 4096
	defaultJSONRequestBodyLimit        = 1 * 1024 * 1024
	authPersistenceWarningHeader       = `199 MnemoNAS "auth state persistence incomplete"`
	sessionModeHeader                  = "X-MnemoNAS-Session-Mode"
	sessionModeCookie                  = "cookie"
	sessionCookiePath                  = "/api/v1"
	refreshSessionCookiePath           = "/api/v1/auth"
	downloadSessionCookiePath          = "/api/v1"
	downloadSessionSameSite            = http.SameSiteStrictMode
)

func newLoginAttemptTracker() *loginAttemptTracker {
	return &loginAttemptTracker{
		attempts:              make(map[loginAttemptKey]loginAttemptState),
		entriesByIP:           make(map[string]int),
		credentialChecksByIP:  make(map[string]loginCredentialCheckState),
		credentialCheckOrder:  list.New(),
		unlockedFailures:      list.New(),
		unlockedFailuresByIP:  make(map[string]*list.List),
		maxEntries:            defaultLoginAttemptMaxEntries,
		maxEntriesPerIP:       defaultLoginAttemptMaxEntriesPerIP,
		maxCredentialCheckIPs: defaultLoginCredentialCheckMaxIPs,
		now:                   time.Now,
	}
}

func (t *loginAttemptTracker) isLocked(key loginAttemptKey, failureWindow time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	t.pruneExpiredIfDueLocked(now, failureWindow)
	state, ok := t.attempts[key]
	if !ok {
		return false
	}
	if state.lockedUntil.After(now) {
		return true
	}
	if !state.lockedUntil.IsZero() || loginAttemptFailureExpired(state, now, failureWindow) {
		t.deleteEntryLocked(key)
		return false
	}
	return false
}

func (t *loginAttemptTracker) recordFailure(key loginAttemptKey, limit int, failureWindow, lockDuration time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	t.pruneExpiredIfDueLocked(now, failureWindow)
	state, exists := t.attempts[key]
	if exists && state.lockedUntil.After(now) {
		return true
	}
	if exists && (!state.lockedUntil.IsZero() && !state.lockedUntil.After(now) || loginAttemptFailureExpired(state, now, failureWindow)) {
		t.deleteEntryLocked(key)
		exists = false
		state = loginAttemptState{}
	}
	if !exists {
		if !t.ensureFailureCapacityLocked(key.clientIP) {
			return false
		}
		t.entriesByIP[key.clientIP]++
	}
	state.failures++
	state.lastFailure = now
	if state.failures >= limit {
		state.lockedUntil = now.Add(lockDuration)
		t.removeUnlockedFailureOrderLocked(&state, key.clientIP)
	} else {
		t.touchUnlockedFailureOrderLocked(key, &state)
	}
	t.attempts[key] = state

	return !state.lockedUntil.IsZero()
}

func (t *loginAttemptTracker) pruneExpiredIfDueLocked(now time.Time, failureWindow time.Duration) {
	pruneInterval := defaultLoginAttemptPruneInterval
	if failureWindow > 0 && failureWindow < pruneInterval {
		pruneInterval = failureWindow
	}
	if !t.lastPrunedAt.IsZero() && !now.Before(t.lastPrunedAt) && now.Sub(t.lastPrunedAt) < pruneInterval {
		return
	}

	for key, state := range t.attempts {
		if state.lockedUntil.After(now) {
			continue
		}
		if !state.lockedUntil.IsZero() && !state.lockedUntil.After(now) {
			t.deleteEntryLocked(key)
			continue
		}
		if loginAttemptFailureExpired(state, now, failureWindow) {
			t.deleteEntryLocked(key)
		}
	}
	t.lastPrunedAt = now
}

func loginAttemptFailureExpired(state loginAttemptState, now time.Time, failureWindow time.Duration) bool {
	return failureWindow > 0 && !state.lastFailure.IsZero() && now.Sub(state.lastFailure) > failureWindow
}

func (t *loginAttemptTracker) ensureFailureCapacityLocked(clientIP string) bool {
	if t.maxEntriesPerIP > 0 && t.entriesByIP[clientIP] >= t.maxEntriesPerIP {
		if !t.evictOldestUnlockedFailureLocked(clientIP) {
			return false
		}
	}
	if t.maxEntries > 0 && len(t.attempts) >= t.maxEntries {
		if !t.evictOldestUnlockedFailureLocked("") {
			return false
		}
	}
	return true
}

func (t *loginAttemptTracker) evictOldestUnlockedFailureLocked(clientIP string) bool {
	failureOrder := t.unlockedFailures
	if clientIP != "" {
		failureOrder = t.unlockedFailuresByIP[clientIP]
	}
	if failureOrder == nil || failureOrder.Len() == 0 {
		return false
	}
	entry, ok := failureOrder.Front().Value.(loginFailureOrderEntry)
	if !ok {
		return false
	}
	t.deleteEntryLocked(entry.key)
	return true
}

func (t *loginAttemptTracker) touchUnlockedFailureOrderLocked(key loginAttemptKey, state *loginAttemptState) {
	if state.globalOrderElement == nil {
		state.globalOrderElement = t.unlockedFailures.PushBack(loginFailureOrderEntry{key: key})
	} else {
		t.unlockedFailures.MoveToBack(state.globalOrderElement)
	}
	perIPOrder := t.unlockedFailuresByIP[key.clientIP]
	if perIPOrder == nil {
		perIPOrder = list.New()
		t.unlockedFailuresByIP[key.clientIP] = perIPOrder
	}
	if state.ipOrderElement == nil {
		state.ipOrderElement = perIPOrder.PushBack(loginFailureOrderEntry{key: key})
	} else {
		perIPOrder.MoveToBack(state.ipOrderElement)
	}
}

func (t *loginAttemptTracker) removeUnlockedFailureOrderLocked(state *loginAttemptState, clientIP string) {
	if state.globalOrderElement != nil {
		t.unlockedFailures.Remove(state.globalOrderElement)
		state.globalOrderElement = nil
	}
	if state.ipOrderElement == nil {
		return
	}
	if perIPOrder := t.unlockedFailuresByIP[clientIP]; perIPOrder != nil {
		perIPOrder.Remove(state.ipOrderElement)
		if perIPOrder.Len() == 0 {
			delete(t.unlockedFailuresByIP, clientIP)
		}
	}
	state.ipOrderElement = nil
}

func (t *loginAttemptTracker) allowCredentialCheck(clientIP string, limit int, window time.Duration) bool {
	if limit <= 0 || window <= 0 {
		return true
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	t.pruneCredentialChecksIfDueLocked(now, window)
	state, exists := t.credentialChecksByIP[clientIP]
	if exists && (now.Before(state.windowStartedAt) || now.Sub(state.windowStartedAt) >= window) {
		t.deleteCredentialCheckLocked(clientIP)
		state = loginCredentialCheckState{}
		exists = false
	}
	if !exists {
		t.ensureCredentialCheckCapacityLocked()
		state.windowStartedAt = now
		state.orderElement = t.credentialCheckOrder.PushBack(loginCredentialCheckOrderEntry{clientIP: clientIP})
	} else if state.orderElement != nil {
		t.credentialCheckOrder.MoveToBack(state.orderElement)
	}
	if state.checks >= limit {
		t.credentialChecksByIP[clientIP] = state
		return false
	}
	state.checks++
	t.credentialChecksByIP[clientIP] = state
	return true
}

func (t *loginAttemptTracker) pruneCredentialChecksIfDueLocked(now time.Time, window time.Duration) {
	pruneInterval := window
	if pruneInterval > defaultLoginAttemptPruneInterval {
		pruneInterval = defaultLoginAttemptPruneInterval
	}
	if !t.lastCredentialChecksPrunedAt.IsZero() && !now.Before(t.lastCredentialChecksPrunedAt) && now.Sub(t.lastCredentialChecksPrunedAt) < pruneInterval {
		return
	}
	for clientIP, state := range t.credentialChecksByIP {
		if now.Before(state.windowStartedAt) || now.Sub(state.windowStartedAt) >= window {
			t.deleteCredentialCheckLocked(clientIP)
		}
	}
	t.lastCredentialChecksPrunedAt = now
}

func (t *loginAttemptTracker) ensureCredentialCheckCapacityLocked() {
	if t.maxCredentialCheckIPs <= 0 || len(t.credentialChecksByIP) < t.maxCredentialCheckIPs {
		return
	}
	oldest := t.credentialCheckOrder.Front()
	if oldest == nil {
		return
	}
	entry, ok := oldest.Value.(loginCredentialCheckOrderEntry)
	if ok {
		t.deleteCredentialCheckLocked(entry.clientIP)
	}
}

func (t *loginAttemptTracker) deleteCredentialCheckLocked(clientIP string) {
	state, ok := t.credentialChecksByIP[clientIP]
	if !ok {
		return
	}
	if state.orderElement != nil {
		t.credentialCheckOrder.Remove(state.orderElement)
	}
	delete(t.credentialChecksByIP, clientIP)
}

func (t *loginAttemptTracker) deleteEntryLocked(key loginAttemptKey) {
	state, ok := t.attempts[key]
	if !ok {
		return
	}
	t.removeUnlockedFailureOrderLocked(&state, key.clientIP)
	delete(t.attempts, key)
	if t.entriesByIP[key.clientIP] <= 1 {
		delete(t.entriesByIP, key.clientIP)
		return
	}
	t.entriesByIP[key.clientIP]--
}

func (t *loginAttemptTracker) reset(key loginAttemptKey) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.deleteEntryLocked(key)
}

// NewHandler creates a new auth handler
func NewHandler(us *UserStore, tm *TokenManager) *Handler {
	return &Handler{
		userStore:                  us,
		tokenManager:               tm,
		loginAttempts:              newLoginAttemptTracker(),
		loginFailureLimit:          defaultLoginFailureLimit,
		loginFailureWindow:         defaultLoginFailureWindow,
		loginLockDuration:          defaultLoginLockDuration,
		loginCredentialCheckLimit:  defaultLoginCredentialCheckLimit,
		loginCredentialCheckWindow: defaultLoginCredentialCheckWindow,
	}
}

func (h *Handler) SetUserUsageResolver(resolver UserUsageResolver) {
	h.usageResolver = resolver
}

func (h *Handler) LoginRateLimitPolicy() LoginRateLimitPolicy {
	if h == nil {
		return LoginRateLimitPolicy{}
	}
	policy := LoginRateLimitPolicy{
		FailureLimit:          h.loginFailureLimit,
		FailureWindow:         h.loginFailureWindow,
		LockDuration:          h.loginLockDuration,
		CredentialCheckLimit:  h.loginCredentialCheckLimit,
		CredentialCheckWindow: h.loginCredentialCheckWindow,
	}
	policy.Enabled = h.loginAttempts != nil &&
		policy.FailureLimit > 0 &&
		policy.FailureWindow > 0 &&
		policy.LockDuration > 0
	policy.Enabled = policy.Enabled &&
		policy.CredentialCheckLimit > 0 &&
		policy.CredentialCheckWindow > 0
	return policy
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

func writeJSONBodyError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large", "PAYLOAD_TOO_LARGE")
		return
	}

	writeError(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
}

// LoginRequest is the login request body
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse is the login response body
type LoginResponse struct {
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	TokenType    string    `json:"token_type"`
	User         UserInfo  `json:"user"`
}

// UserInfo is public user information
type UserInfo struct {
	ID                 string   `json:"id"`
	Username           string   `json:"username"`
	Email              string   `json:"email,omitempty"`
	Role               Role     `json:"role"`
	Groups             []string `json:"groups,omitempty"`
	HomeDir            string   `json:"home_dir"`
	MustChangePassword bool     `json:"must_change_password"`
}

// RefreshRequest is the refresh token request body
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type ResponseEnvelope struct {
	Success bool         `json:"success"`
	Data    interface{}  `json:"data,omitempty"`
	Message string       `json:"message,omitempty"`
	Error   *ErrorDetail `json:"error,omitempty"`
}

type ErrorDetail struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

func fullUserResponse(user *User) map[string]interface{} {
	info := map[string]interface{}{
		"id":                   user.ID,
		"username":             user.Username,
		"email":                user.Email,
		"role":                 user.Role,
		"groups":               append([]string(nil), user.Groups...),
		"disabled":             user.Disabled,
		"home_dir":             user.HomeDir,
		"created_at":           user.CreatedAt,
		"updated_at":           user.UpdatedAt,
		"quota_bytes":          user.QuotaBytes,
		"used_bytes":           user.UsedBytes,
		"must_change_password": user.MustChangePassword,
	}
	if user.LastLoginAt != nil {
		info["last_login_at"] = user.LastLoginAt
	}
	if user.PasswordChangedAt != nil {
		info["password_changed_at"] = user.PasswordChangedAt
	}
	return info
}

func (h *Handler) fullUserResponse(ctx context.Context, user *User) map[string]interface{} {
	info := fullUserResponse(user)
	if h != nil && h.usageResolver != nil {
		if usedBytes, err := h.usageResolver(ctx, user); err == nil {
			info["used_bytes"] = usedBytes
		}
	}
	return info
}

func requestWantsCookieSession(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get(sessionModeHeader)), sessionModeCookie)
}

func loginResponseFromTokenPair(tokenPair *TokenPair, user *User, includeTokens bool) LoginResponse {
	resp := LoginResponse{
		ExpiresAt: tokenPair.ExpiresAt,
		TokenType: tokenPair.TokenType,
		User: UserInfo{
			ID:                 user.ID,
			Username:           user.Username,
			Email:              user.Email,
			Role:               user.Role,
			Groups:             append([]string(nil), user.Groups...),
			HomeDir:            user.HomeDir,
			MustChangePassword: user.MustChangePassword,
		},
	}
	if includeTokens {
		resp.AccessToken = tokenPair.AccessToken
		resp.RefreshToken = tokenPair.RefreshToken
	}
	return resp
}

func setSessionCookies(w http.ResponseWriter, r *http.Request, tokenPair *TokenPair) {
	secure := requestIsHTTPS(r)
	setAuthCookie(w, AccessSessionCookieName, tokenPair.AccessToken, sessionCookiePath, tokenPair.ExpiresAt, secure)
	setAuthCookie(w, RefreshSessionCookieName, tokenPair.RefreshToken, refreshSessionCookiePath, tokenPair.RefreshExpiresAt, secure)
}

func setAuthCookie(w http.ResponseWriter, name, value, path string, expires time.Time, secure bool) {
	name = cookieNameForHTTPSMode(name, secure)
	if secure {
		path = "/"
	}
	maxAge := int(time.Until(expires).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		Expires:  expires.UTC(),
		MaxAge:   maxAge,
	})
}

func clearSessionCookiesForMode(w http.ResponseWriter, secure bool) {
	clearAuthCookie(w, AccessSessionCookieName, sessionCookiePath, secure)
	clearAuthCookie(w, RefreshSessionCookieName, refreshSessionCookiePath, secure)
}

func clearBrowserSessionCookies(w http.ResponseWriter, r *http.Request) {
	secure := requestIsHTTPS(r)
	clearSessionCookiesForMode(w, secure)
	clearDownloadSessionCookieForMode(w, secure)
}

func clearAuthCookie(w http.ResponseWriter, name, path string, secure bool) {
	name = cookieNameForHTTPSMode(name, secure)
	if secure {
		path = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
	})
}

func decodeOptionalRefreshRequest(r *http.Request, req *RefreshRequest) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, defaultJSONRequestBodyLimit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > defaultJSONRequestBodyLimit {
		return &http.MaxBytesError{Limit: defaultJSONRequestBodyLimit}
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(req); err != nil {
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

// HandleLogin handles POST /api/v1/auth/login
func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	var req LoginRequest
	if err := decodeJSONBodyStrict(r, &req); err != nil {
		writeJSONBodyError(w, err)
		return
	}

	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required", "MISSING_CREDENTIALS")
		return
	}
	cleanUsername, err := normalizeLoginUsername(req.Username)
	if err != nil {
		attemptKey := invalidLoginAttemptKey(r)
		if h.loginAttempts != nil {
			if h.loginAttempts.isLocked(attemptKey, h.loginFailureWindow) ||
				h.loginAttempts.recordFailure(attemptKey, h.loginFailureLimit, h.loginFailureWindow, h.loginLockDuration) {
				writeError(w, http.StatusTooManyRequests, defaultLoginRateLimitMessage, "LOGIN_RATE_LIMITED")
				return
			}
		}
		writeError(w, http.StatusUnauthorized, "invalid username or password", "INVALID_CREDENTIALS")
		return
	}
	req.Username = cleanUsername

	attemptKey := newLoginAttemptKey(r, req.Username)
	if h.loginAttempts != nil && h.loginAttempts.isLocked(attemptKey, h.loginFailureWindow) {
		writeError(w, http.StatusTooManyRequests, defaultLoginRateLimitMessage, "LOGIN_RATE_LIMITED")
		return
	}
	if h.loginAttempts != nil && !h.loginAttempts.allowCredentialCheck(
		attemptKey.clientIP,
		h.loginCredentialCheckLimit,
		h.loginCredentialCheckWindow,
	) {
		retryAfter := int((h.loginCredentialCheckWindow + time.Second - 1) / time.Second)
		if retryAfter < 1 {
			retryAfter = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		writeError(w, http.StatusTooManyRequests, defaultLoginRateLimitMessage, "LOGIN_RATE_LIMITED")
		return
	}

	user, err := h.userStore.Authenticate(req.Username, req.Password)
	loginWarning := false
	if err != nil {
		if isAuthPersistenceWarning(err) && user != nil {
			markAuthPersistenceWarningHeaders(w)
			loginWarning = true
		} else {
			switch err {
			case ErrInvalidCredentials:
				if h.loginAttempts != nil {
					if locked := h.loginAttempts.recordFailure(attemptKey, h.loginFailureLimit, h.loginFailureWindow, h.loginLockDuration); locked {
						writeError(w, http.StatusTooManyRequests, defaultLoginRateLimitMessage, "LOGIN_RATE_LIMITED")
						return
					}
				}
				writeError(w, http.StatusUnauthorized, "invalid username or password", "INVALID_CREDENTIALS")
			case ErrUserDisabled:
				if h.loginAttempts != nil {
					if locked := h.loginAttempts.recordFailure(attemptKey, h.loginFailureLimit, h.loginFailureWindow, h.loginLockDuration); locked {
						writeError(w, http.StatusTooManyRequests, defaultLoginRateLimitMessage, "LOGIN_RATE_LIMITED")
						return
					}
				}
				writeError(w, http.StatusUnauthorized, "invalid username or password", "INVALID_CREDENTIALS")
			default:
				writeError(w, http.StatusInternalServerError, "internal server error", "AUTH_ERROR")
			}
			return
		}
	}
	if h.loginAttempts != nil {
		h.loginAttempts.reset(attemptKey)
	}

	tokenPair, err := h.tokenManager.GenerateTokenPair(user)
	if err != nil {
		if isAuthPersistenceWarning(err) && tokenPair != nil {
			markAuthPersistenceWarningHeaders(w)
			loginWarning = true
		} else {
			switch {
			case errors.Is(err, ErrRefreshSessionLimit):
				writeError(w, http.StatusTooManyRequests, "active session limit reached", "REFRESH_SESSION_LIMIT")
			case errors.Is(err, ErrTokenStateUnavailable):
				writeError(w, http.StatusServiceUnavailable, "token session state unavailable", "TOKEN_STATE_UNAVAILABLE")
			default:
				writeError(w, http.StatusInternalServerError, "internal server error", "TOKEN_ERROR")
			}
			return
		}
	}

	setSessionCookies(w, r, tokenPair)
	resp := loginResponseFromTokenPair(tokenPair, user, !requestWantsCookieSession(r))

	message := ""
	if loginWarning {
		message = "login succeeded with persistence warning"
	}
	writeSuccess(w, http.StatusOK, resp, message)
}

func newLoginAttemptKey(r *http.Request, username string) loginAttemptKey {
	return loginAttemptKey{
		usernameDigest: sha256.Sum256([]byte(normalizeUsername(strings.TrimSpace(username)))),
		clientIP:       requestip.ClientIP(r),
	}
}

func invalidLoginAttemptKey(r *http.Request) loginAttemptKey {
	return loginAttemptKey{
		usernameDigest: sha256.Sum256([]byte("invalid-login-username")),
		clientIP:       requestip.ClientIP(r),
	}
}

func normalizeLoginUsername(username string) (string, error) {
	if len(username) > maxUsernameRuneCount*utf8.UTFMax {
		return "", ErrInvalidUsername
	}
	return normalizeNewUsername(username)
}

// HandleRefresh handles POST /api/v1/auth/refresh
func (h *Handler) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	var req RefreshRequest
	if err := decodeOptionalRefreshRequest(r, &req); err != nil {
		writeJSONBodyError(w, err)
		return
	}

	refreshTokens := make([]string, 0, 1)
	refreshToken := strings.TrimSpace(req.RefreshToken)
	refreshFromCookie := false
	if refreshToken != "" {
		refreshTokens = append(refreshTokens, refreshToken)
	} else {
		var cookieErr error
		refreshTokens, cookieErr = cookieValuesFromRequest(r, RefreshSessionCookieName)
		if cookieErr != nil {
			clearBrowserSessionCookies(w, r)
			writeRefreshTokenError(w, ErrInvalidToken)
			return
		}
		refreshFromCookie = len(refreshTokens) > 0
	}

	if len(refreshTokens) == 0 {
		clearBrowserSessionCookies(w, r)
		writeError(w, http.StatusBadRequest, "refresh token required", "MISSING_TOKEN")
		return
	}

	claims, err := h.validateRefreshTokenCandidates(refreshTokens)
	if err != nil {
		if errors.Is(err, errRefreshTokenReused) && claims != nil {
			h.handleRefreshTokenReuse(w, r, claims)
			return
		}
		if refreshFromCookie && shouldClearSessionCookiesAfterRefreshFailure(err) {
			clearBrowserSessionCookies(w, r)
		}
		writeRefreshTokenError(w, err)
		return
	}
	userID := claims.Subject

	user, err := h.userStore.GetByID(userID)
	if err != nil {
		if refreshFromCookie {
			clearBrowserSessionCookies(w, r)
		}
		writeError(w, http.StatusUnauthorized, "user not found", "USER_NOT_FOUND")
		return
	}

	if user.Disabled {
		if refreshFromCookie {
			clearBrowserSessionCookies(w, r)
		}
		writeError(w, http.StatusForbidden, "user account is disabled", "USER_DISABLED")
		return
	}
	if claims.CredentialVersion != user.CredentialVersion {
		if refreshFromCookie {
			clearBrowserSessionCookies(w, r)
		}
		writeRefreshTokenError(w, ErrTokenRevoked)
		return
	}

	tokenPair, err := h.tokenManager.generateTokenPairForSession(user, claims.SessionID, claims.ExpiresAt.Time, claims.Generation+1)
	if err != nil {
		h.handleRefreshPairGenerationError(w, r, refreshTokens, refreshFromCookie, err)
		return
	}

	revocationWarning := false
	if err := h.tokenManager.consumeRefreshTokenClaims(claims); err != nil {
		if isAuthPersistenceWarning(err) {
			revocationWarning = true
		} else if errors.Is(err, errRefreshTokenReused) {
			h.handleRefreshTokenReuse(w, r, claims)
			return
		} else if errors.Is(err, ErrRefreshRateLimited) {
			w.Header().Set("Retry-After", "30")
			writeError(w, http.StatusTooManyRequests, "refresh requests are temporarily limited", "REFRESH_RATE_LIMITED")
			return
		} else if errors.Is(err, ErrRefreshSessionLimit) {
			writeError(w, http.StatusTooManyRequests, "active session limit reached", "REFRESH_SESSION_LIMIT")
			return
		} else if isRefreshTokenError(err) {
			if refreshFromCookie && shouldClearSessionCookiesAfterRefreshFailure(err) {
				clearBrowserSessionCookies(w, r)
			}
			writeRefreshTokenError(w, err)
			return
		} else {
			writeError(w, http.StatusInternalServerError, "internal server error", "TOKEN_ERROR")
			return
		}
	}

	setSessionCookies(w, r, tokenPair)
	resp := loginResponseFromTokenPair(tokenPair, user, !refreshFromCookie && !requestWantsCookieSession(r))

	message := ""
	if revocationWarning {
		markAuthPersistenceWarningHeaders(w)
		message = "refresh token rotated with persistence warning"
	}
	writeSuccess(w, http.StatusOK, resp, message)
}

func (h *Handler) validateRefreshTokenCandidates(candidates []string) (*refreshTokenClaims, error) {
	var firstErr error
	var firstClaims *refreshTokenClaims
	for _, refreshToken := range candidates {
		claims, err := h.tokenManager.validateRefreshTokenClaims(refreshToken)
		if err == nil {
			return claims, nil
		}
		if errors.Is(err, errRefreshTokenReused) && claims != nil {
			return claims, err
		}
		if firstErr == nil {
			firstErr = err
			firstClaims = claims
		}
	}
	if firstErr == nil {
		firstErr = ErrInvalidToken
	}
	return firstClaims, firstErr
}

func shouldClearSessionCookiesAfterRefreshFailure(err error) bool {
	return !errors.Is(err, ErrTokenStateUnavailable)
}

func (h *Handler) handleRefreshPairGenerationError(w http.ResponseWriter, r *http.Request, refreshTokens []string, refreshFromCookie bool, generationErr error) {
	if errors.Is(generationErr, ErrTokenStateUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "token session state unavailable", "TOKEN_STATE_UNAVAILABLE")
		return
	}
	if errors.Is(generationErr, ErrInvalidToken) {
		latestClaims, latestErr := h.validateRefreshTokenCandidates(refreshTokens)
		if errors.Is(latestErr, errRefreshTokenReused) && latestClaims != nil {
			h.handleRefreshTokenReuse(w, r, latestClaims)
			return
		}
	}
	if isRefreshTokenError(generationErr) {
		if refreshFromCookie && shouldClearSessionCookiesAfterRefreshFailure(generationErr) {
			clearBrowserSessionCookies(w, r)
		}
		writeRefreshTokenError(w, generationErr)
		return
	}
	writeError(w, http.StatusInternalServerError, "internal server error", "TOKEN_ERROR")
}

func (h *Handler) handleRefreshTokenReuse(w http.ResponseWriter, r *http.Request, claims *refreshTokenClaims) {
	if claims == nil || claims.SessionID == "" || claims.ExpiresAt == nil {
		writeRefreshTokenError(w, ErrTokenRevoked)
		return
	}
	if err := h.tokenManager.RevokeSession(claims.SessionID, claims.ExpiresAt.Time); err != nil {
		if !isAuthPersistenceWarning(err) {
			writeError(w, http.StatusInternalServerError, "internal server error", "TOKEN_ERROR")
			return
		}
		markAuthPersistenceWarningHeaders(w)
	}
	clearBrowserSessionCookies(w, r)
	writeRefreshTokenError(w, ErrTokenRevoked)
}

func isRefreshTokenError(err error) bool {
	return errors.Is(err, ErrInvalidToken) ||
		errors.Is(err, ErrTokenExpired) ||
		errors.Is(err, ErrTokenRevoked)
}

func writeRefreshTokenError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrTokenStateUnavailable):
		writeError(w, http.StatusServiceUnavailable, "token session state unavailable", "TOKEN_STATE_UNAVAILABLE")
	case errors.Is(err, ErrTokenExpired):
		writeError(w, http.StatusUnauthorized, "refresh token expired", "TOKEN_EXPIRED")
	case errors.Is(err, ErrTokenRevoked):
		writeError(w, http.StatusUnauthorized, "token has been revoked", "TOKEN_REVOKED")
	default:
		writeError(w, http.StatusUnauthorized, "invalid refresh token", "INVALID_TOKEN")
	}
}

// HandleLogout handles POST /api/v1/auth/logout
func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	var req RefreshRequest
	if err := decodeOptionalRefreshRequest(r, &req); err != nil {
		writeJSONBodyError(w, err)
		return
	}

	sessions := make(map[string]time.Time, 2)
	if claims := GetClaimsFromContext(r.Context()); claims != nil && claims.SessionID != "" && claims.SessionExpiresAt != nil {
		sessions[claims.SessionID] = claims.SessionExpiresAt.Time
	}

	refreshTokens := make([]string, 0, 1)
	if refreshToken := strings.TrimSpace(req.RefreshToken); refreshToken != "" {
		refreshTokens = append(refreshTokens, refreshToken)
	} else {
		var cookieErr error
		refreshTokens, cookieErr = cookieValuesFromRequest(r, RefreshSessionCookieName)
		if cookieErr != nil {
			clearBrowserSessionCookies(w, r)
			writeRefreshTokenError(w, ErrInvalidToken)
			return
		}
	}
	if len(refreshTokens) > 0 {
		refreshClaims, parseErr := h.parseLogoutRefreshTokenCandidates(refreshTokens)
		if errors.Is(parseErr, ErrTokenStateUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "token session state unavailable", "TOKEN_STATE_UNAVAILABLE")
			return
		}
		if refreshClaims != nil && refreshClaims.ExpiresAt != nil {
			if currentExpiry, ok := sessions[refreshClaims.SessionID]; !ok || refreshClaims.ExpiresAt.Time.After(currentExpiry) {
				sessions[refreshClaims.SessionID] = refreshClaims.ExpiresAt.Time
			}
		}
	}

	if err := h.tokenManager.RevokeSessions(sessions); err != nil {
		if isAuthPersistenceWarning(err) {
			clearBrowserSessionCookies(w, r)
			markAuthPersistenceWarningHeaders(w)
			writeSuccess(w, http.StatusOK, nil, "logged out with persistence warning")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error", "TOKEN_ERROR")
		return
	}
	clearBrowserSessionCookies(w, r)

	writeSuccess(w, http.StatusOK, nil, "logged out successfully")
}

func (h *Handler) parseLogoutRefreshTokenCandidates(candidates []string) (*refreshTokenClaims, error) {
	var stateErr error
	for _, refreshToken := range candidates {
		claims, err := h.tokenManager.parseRefreshTokenClaims(refreshToken)
		if err == nil {
			return claims, nil
		}
		if errors.Is(err, ErrTokenStateUnavailable) {
			stateErr = err
		}
	}
	return nil, stateErr
}

// HandleCreateDownloadSession handles POST /api/v1/auth/download-session.
func (h *Handler) HandleCreateDownloadSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	if user := GetUserFromContext(r.Context()); user != nil && user.Disabled {
		writeDisabledUserError(w)
		return
	}

	claims := GetClaimsFromContext(r.Context())
	if claims == nil || claims.ExpiresAt == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", "NOT_AUTHENTICATED")
		return
	}
	accessToken := GetAccessTokenFromContext(r.Context())
	if strings.TrimSpace(accessToken) == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated", "NOT_AUTHENTICATED")
		return
	}

	maxAge := int(time.Until(claims.ExpiresAt.Time).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}

	secure := requestIsHTTPS(r)
	cookieName := cookieNameForHTTPSMode(DownloadSessionCookieName, secure)
	cookiePath := downloadSessionCookiePath
	if secure {
		cookiePath = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    accessToken,
		Path:     cookiePath,
		HttpOnly: true,
		SameSite: downloadSessionSameSite,
		Secure:   secure,
		Expires:  claims.ExpiresAt.Time.UTC(),
		MaxAge:   maxAge,
	})

	writeSuccess(w, http.StatusOK, nil, "")
}

func clearDownloadSessionCookieForMode(w http.ResponseWriter, secure bool) {
	cookieName := cookieNameForHTTPSMode(DownloadSessionCookieName, secure)
	cookiePath := downloadSessionCookiePath
	if secure {
		cookiePath = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     cookiePath,
		HttpOnly: true,
		SameSite: downloadSessionSameSite,
		Secure:   secure,
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
	})
}

func requestIsHTTPS(r *http.Request) bool {
	return requestip.RequestIsHTTPS(r)
}

func writeDisabledUserError(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "user account is disabled", "USER_DISABLED")
}

// HandleMe handles GET /api/v1/auth/me
func (h *Handler) HandleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	user := GetUserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", "NOT_AUTHENTICATED")
		return
	}
	if user.Disabled {
		writeDisabledUserError(w)
		return
	}

	resp := map[string]interface{}{
		"user": UserInfo{
			ID:                 user.ID,
			Username:           user.Username,
			Email:              user.Email,
			Role:               user.Role,
			Groups:             append([]string(nil), user.Groups...),
			HomeDir:            user.HomeDir,
			MustChangePassword: user.MustChangePassword,
		},
	}

	writeSuccess(w, http.StatusOK, resp, "")
}

// ChangePasswordRequest is the change password request body
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// HandleChangePassword handles POST /api/v1/auth/password
func (h *Handler) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	user := GetUserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", "NOT_AUTHENTICATED")
		return
	}
	if user.Disabled {
		writeDisabledUserError(w)
		return
	}

	var req ChangePasswordRequest
	if err := decodeJSONBodyStrict(r, &req); err != nil {
		writeJSONBodyError(w, err)
		return
	}

	if req.OldPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "old and new password required", "MISSING_PASSWORD")
		return
	}

	if err := h.userStore.ChangePassword(user.ID, req.OldPassword, req.NewPassword); err != nil {
		if isAuthPersistenceWarning(err) {
			_ = h.tokenManager.RevokeByUser(user.ID)
			markAuthPersistenceWarningHeaders(w)
			clearBrowserSessionCookies(w, r)
			writeSuccess(w, http.StatusOK, map[string]interface{}{"warning": true}, "password changed with persistence warning")
			return
		}
		switch err {
		case ErrInvalidCredentials:
			writeError(w, http.StatusUnauthorized, "current password is incorrect", "INVALID_PASSWORD")
		case ErrPasswordTooShort:
			writeError(w, http.StatusBadRequest, "password must contain at least 8 UTF-8 bytes and not be whitespace-only", "PASSWORD_TOO_SHORT")
		case ErrPasswordTooLong:
			writeError(w, http.StatusBadRequest, "password must be at most 72 bytes", "PASSWORD_TOO_LONG")
		case ErrPasswordUnchanged:
			writeError(w, http.StatusBadRequest, "new password must differ from current password", "PASSWORD_UNCHANGED")
		default:
			writeError(w, http.StatusInternalServerError, "internal server error", "PASSWORD_ERROR")
		}
		return
	}

	// Revoke all tokens for this user
	if err := h.tokenManager.RevokeByUser(user.ID); err != nil {
		markAuthPersistenceWarningHeaders(w)
		clearBrowserSessionCookies(w, r)
		writeSuccess(w, http.StatusOK, map[string]interface{}{"warning": true}, "password changed with persistence warning")
		return
	}

	clearBrowserSessionCookies(w, r)
	writeSuccess(w, http.StatusOK, nil, "password changed successfully")
}

// Admin endpoints

// CreateUserRequest is the create user request body
type CreateUserRequest struct {
	Username   string   `json:"username"`
	Password   string   `json:"password"`
	Email      string   `json:"email"`
	Role       string   `json:"role"`
	Groups     []string `json:"groups,omitempty"`
	HomeDir    *string  `json:"home_dir"`
	QuotaBytes *int64   `json:"quota_bytes"`
}

// UpdateUserRequest is the update user request body.
type UpdateUserRequest struct {
	Email      *string   `json:"email"`
	Role       *string   `json:"role"`
	Groups     *[]string `json:"groups,omitempty"`
	HomeDir    *string   `json:"home_dir"`
	QuotaBytes *int64    `json:"quota_bytes"`
}

// HandleListUsers handles GET /api/v1/admin/users
func (h *Handler) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	if !IsAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin access required", "FORBIDDEN")
		return
	}

	users := h.userStore.List()
	userInfos := make([]map[string]interface{}, 0, len(users))
	quotaSnapshotUsers := make([]*User, 0, len(users))

	for _, u := range users {
		userInfo := h.fullUserResponse(r.Context(), u)
		userInfos = append(userInfos, userInfo)
		quotaSnapshotUsers = append(quotaSnapshotUsers, userForQuotaTrendSnapshot(u, userInfo))
	}
	quotaHistory, quotaHistoryAvailable := h.recordUserQuotaTrendHistory(quotaSnapshotUsers)

	writeSuccess(w, http.StatusOK, map[string]interface{}{
		"users":                   userInfos,
		"total":                   len(userInfos),
		"quota_history":           quotaHistory,
		"quota_history_available": quotaHistoryAvailable,
	}, "")
}

func userForQuotaTrendSnapshot(user *User, userInfo map[string]interface{}) *User {
	snapshotUser := cloneUser(user)
	if snapshotUser == nil {
		return nil
	}
	if usedBytes, ok := userInfo["used_bytes"].(int64); ok {
		snapshotUser.UsedBytes = usedBytes
	}
	return snapshotUser
}

func (h *Handler) recordUserQuotaTrendHistory(users []*User) ([]UserQuotaTrendPoint, bool) {
	if h == nil || h.userStore == nil {
		return []UserQuotaTrendPoint{}, false
	}
	history, err := h.userStore.RecordUserQuotaTrendSnapshot(newUserQuotaTrendPoint(users, time.Now().UTC()))
	if err != nil {
		return []UserQuotaTrendPoint{}, false
	}
	return history, true
}

// HandleCreateUser handles POST /api/v1/admin/users
func (h *Handler) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	if !IsAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin access required", "FORBIDDEN")
		return
	}

	var req CreateUserRequest
	if err := decodeJSONBodyStrict(r, &req); err != nil {
		writeJSONBodyError(w, err)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)
	req.Role = strings.TrimSpace(req.Role)

	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required", "MISSING_FIELDS")
		return
	}
	cleanUsername, err := normalizeNewUsername(req.Username)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid username", "INVALID_USERNAME")
		return
	}
	req.Username = cleanUsername

	role := Role(strings.ToLower(req.Role))
	if role == "" {
		role = RoleUser
	}
	if role != RoleAdmin && role != RoleUser && role != RoleGuest {
		writeError(w, http.StatusBadRequest, "invalid role, must be admin, user, or guest", "INVALID_ROLE")
		return
	}
	var homeDir *string
	if req.HomeDir != nil {
		trimmedHomeDir := strings.TrimSpace(*req.HomeDir)
		homeDir = &trimmedHomeDir
	}
	quotaBytes := int64(0)
	if req.QuotaBytes != nil {
		if *req.QuotaBytes < 0 {
			writeError(w, http.StatusBadRequest, "quota_bytes must be greater than or equal to 0", "INVALID_QUOTA")
			return
		}
		quotaBytes = *req.QuotaBytes
	}

	user, err := h.userStore.CreateWithOptions(req.Username, req.Password, req.Email, role, CreateUserOptions{
		Groups:     req.Groups,
		HomeDir:    homeDir,
		QuotaBytes: quotaBytes,
	})
	if err != nil {
		if isAuthPersistenceWarning(err) && user != nil {
			markAuthPersistenceWarningHeaders(w)
			writeSuccess(w, http.StatusCreated, map[string]interface{}{
				"user":    h.fullUserResponse(r.Context(), user),
				"warning": true,
			}, "user created with persistence warning")
			return
		}
		switch err {
		case ErrUserExists:
			writeError(w, http.StatusConflict, "user already exists", "USER_EXISTS")
		case ErrPasswordTooShort:
			writeError(w, http.StatusBadRequest, "password must contain at least 8 UTF-8 bytes and not be whitespace-only", "PASSWORD_TOO_SHORT")
		case ErrPasswordTooLong:
			writeError(w, http.StatusBadRequest, "password must be at most 72 bytes", "PASSWORD_TOO_LONG")
		case errInvalidUserGroups:
			writeError(w, http.StatusBadRequest, "invalid groups", "INVALID_GROUPS")
		case errInvalidUserHomeDir:
			writeError(w, http.StatusBadRequest, "invalid home_dir", "INVALID_HOME_DIR")
		case errInvalidQuotaBytes:
			writeError(w, http.StatusBadRequest, "quota_bytes must be greater than or equal to 0", "INVALID_QUOTA")
		default:
			writeError(w, http.StatusInternalServerError, "internal server error", "CREATE_ERROR")
		}
		return
	}

	writeSuccess(w, http.StatusCreated, map[string]interface{}{
		"user": h.fullUserResponse(r.Context(), user),
	}, "")
}

// HandleUpdateUser handles PUT /api/v1/admin/users/{id}
func (h *Handler) HandleUpdateUser(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	if !IsAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin access required", "FORBIDDEN")
		return
	}

	var req UpdateUserRequest
	if err := decodeJSONBodyStrict(r, &req); err != nil {
		writeJSONBodyError(w, err)
		return
	}
	if req.Email == nil && req.Role == nil && req.Groups == nil && req.HomeDir == nil && req.QuotaBytes == nil {
		writeError(w, http.StatusBadRequest, "at least one field is required", "MISSING_FIELDS")
		return
	}

	var role *Role
	if req.Role != nil {
		normalizedRole := Role(strings.ToLower(strings.TrimSpace(*req.Role)))
		if normalizedRole != RoleAdmin && normalizedRole != RoleUser && normalizedRole != RoleGuest {
			writeError(w, http.StatusBadRequest, "invalid role, must be admin, user, or guest", "INVALID_ROLE")
			return
		}
		currentUser := GetUserFromContext(r.Context())
		if currentUser != nil && currentUser.ID == userID && normalizedRole != RoleAdmin {
			writeError(w, http.StatusBadRequest, "cannot change your own admin role", "SELF_ROLE_CHANGE")
			return
		}
		role = &normalizedRole
	}
	if req.QuotaBytes != nil {
		if *req.QuotaBytes < 0 {
			writeError(w, http.StatusBadRequest, "quota_bytes must be greater than or equal to 0", "INVALID_QUOTA")
			return
		}
	}

	updatedUser, err := h.userStore.Patch(userID, UserPatch{
		Email:      req.Email,
		Role:       role,
		Groups:     req.Groups,
		HomeDir:    req.HomeDir,
		QuotaBytes: req.QuotaBytes,
	})
	if err != nil {
		if isAuthPersistenceWarning(err) {
			if updatedUser == nil {
				writeError(w, http.StatusInternalServerError, "internal server error", "UPDATE_ERROR")
				return
			}
			markAuthPersistenceWarningHeaders(w)
			writeSuccess(w, http.StatusOK, map[string]interface{}{
				"user":    h.fullUserResponse(r.Context(), updatedUser),
				"warning": true,
			}, "user updated with persistence warning")
			return
		}
		switch {
		case errors.Is(err, ErrUserNotFound):
			writeError(w, http.StatusNotFound, "user not found", "USER_NOT_FOUND")
		case errors.Is(err, ErrUserExists):
			writeError(w, http.StatusConflict, "user already exists", "USER_EXISTS")
		case errors.Is(err, ErrInvalidRole):
			writeError(w, http.StatusBadRequest, "invalid role, must be admin, user, or guest", "INVALID_ROLE")
		case errors.Is(err, errInvalidUserHomeDir):
			writeError(w, http.StatusBadRequest, "invalid home_dir", "INVALID_HOME_DIR")
		case errors.Is(err, errInvalidUserGroups):
			writeError(w, http.StatusBadRequest, "invalid groups", "INVALID_GROUPS")
		case errors.Is(err, ErrLastAdmin):
			writeError(w, http.StatusBadRequest, "cannot remove last admin user", "LAST_ADMIN")
		case errors.Is(err, errInvalidQuotaBytes):
			writeError(w, http.StatusBadRequest, "quota_bytes must be greater than or equal to 0", "INVALID_QUOTA")
		default:
			writeError(w, http.StatusInternalServerError, "internal server error", "UPDATE_ERROR")
		}
		return
	}

	writeSuccess(w, http.StatusOK, map[string]interface{}{
		"user": h.fullUserResponse(r.Context(), updatedUser),
	}, "user updated successfully")
}

// HandleDeleteUser handles DELETE /api/v1/admin/users/{id}
func (h *Handler) HandleDeleteUser(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	if !IsAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin access required", "FORBIDDEN")
		return
	}

	// Prevent self-deletion
	currentUser := GetUserFromContext(r.Context())
	if currentUser != nil && currentUser.ID == userID {
		writeError(w, http.StatusBadRequest, "cannot delete your own account", "SELF_DELETE")
		return
	}

	if err := h.userStore.Delete(userID); err != nil {
		if isAuthPersistenceWarning(err) {
			_ = h.tokenManager.RevokeByUser(userID)
			markAuthPersistenceWarningHeaders(w)
			writeSuccess(w, http.StatusOK, map[string]interface{}{"warning": true}, "user deleted with persistence warning")
			return
		}
		switch err {
		case ErrUserNotFound:
			writeError(w, http.StatusNotFound, "user not found", "USER_NOT_FOUND")
		case ErrLastAdmin:
			writeError(w, http.StatusBadRequest, "cannot delete last admin user", "LAST_ADMIN")
		default:
			writeError(w, http.StatusInternalServerError, "internal server error", "DELETE_ERROR")
		}
		return
	}

	if err := h.tokenManager.RevokeByUser(userID); err != nil {
		markAuthPersistenceWarningHeaders(w)
		writeSuccess(w, http.StatusOK, map[string]interface{}{"warning": true}, "user deleted with persistence warning")
		return
	}

	writeSuccess(w, http.StatusOK, nil, "user deleted successfully")
}

// HandleResetUserPassword handles POST /api/v1/admin/users/{id}/reset-password
func (h *Handler) HandleResetUserPassword(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	if !IsAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin access required", "FORBIDDEN")
		return
	}

	var req struct {
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSONBodyStrict(r, &req); err != nil {
		writeJSONBodyError(w, err)
		return
	}

	if req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "new password required", "MISSING_PASSWORD")
		return
	}

	actor := GetUserFromContext(r.Context())
	var resetErr error
	if actor != nil && actor.ID == userID {
		resetErr = h.userStore.ResetOwnPassword(userID, req.NewPassword)
	} else {
		resetErr = h.userStore.ResetPassword(userID, req.NewPassword)
	}
	if resetErr != nil {
		if isAuthPersistenceWarning(resetErr) {
			_ = h.tokenManager.RevokeByUser(userID)
			markAuthPersistenceWarningHeaders(w)
			writeSuccess(w, http.StatusOK, map[string]interface{}{"warning": true}, "password reset with persistence warning")
			return
		}
		switch resetErr {
		case ErrUserNotFound:
			writeError(w, http.StatusNotFound, "user not found", "USER_NOT_FOUND")
		case ErrPasswordTooShort:
			writeError(w, http.StatusBadRequest, "password must contain at least 8 UTF-8 bytes and not be whitespace-only", "PASSWORD_TOO_SHORT")
		case ErrPasswordTooLong:
			writeError(w, http.StatusBadRequest, "password must be at most 72 bytes", "PASSWORD_TOO_LONG")
		default:
			writeError(w, http.StatusInternalServerError, "internal server error", "RESET_ERROR")
		}
		return
	}

	// Revoke all tokens for this user
	if err := h.tokenManager.RevokeByUser(userID); err != nil {
		markAuthPersistenceWarningHeaders(w)
		writeSuccess(w, http.StatusOK, map[string]interface{}{"warning": true}, "password reset with persistence warning")
		return
	}

	writeSuccess(w, http.StatusOK, nil, "password reset successfully")
}

// HandleRevokeUserSessions handles POST /api/v1/admin/users/{id}/revoke-sessions
func (h *Handler) HandleRevokeUserSessions(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	if !IsAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin access required", "FORBIDDEN")
		return
	}

	if _, err := h.userStore.GetByID(userID); err != nil {
		writeError(w, http.StatusNotFound, "user not found", "USER_NOT_FOUND")
		return
	}

	if err := h.tokenManager.RevokeByUser(userID); err != nil {
		if isAuthPersistenceWarning(err) {
			markAuthPersistenceWarningHeaders(w)
			writeSuccess(w, http.StatusOK, map[string]interface{}{
				"revoked": true,
				"warning": true,
			}, "user sessions revoked with persistence warning")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error", "REVOKE_SESSIONS_ERROR")
		return
	}

	writeSuccess(w, http.StatusOK, map[string]interface{}{"revoked": true}, "user sessions revoked successfully")
}

// HandleToggleUserStatus handles PUT /api/v1/admin/users/{id}/status
func (h *Handler) HandleToggleUserStatus(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	if !IsAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin access required", "FORBIDDEN")
		return
	}

	var req struct {
		Disabled *bool `json:"disabled"`
	}
	if err := decodeJSONBodyStrict(r, &req); err != nil {
		writeJSONBodyError(w, err)
		return
	}
	if req.Disabled == nil {
		writeError(w, http.StatusBadRequest, "disabled field is required", "MISSING_DISABLED")
		return
	}

	// Prevent disabling self
	currentUser := GetUserFromContext(r.Context())
	if currentUser != nil && currentUser.ID == userID && *req.Disabled {
		writeError(w, http.StatusBadRequest, "cannot disable your own account", "SELF_DISABLE")
		return
	}

	if _, err := h.userStore.Patch(userID, UserPatch{Disabled: req.Disabled}); err != nil {
		if isAuthPersistenceWarning(err) {
			if *req.Disabled {
				_ = h.tokenManager.RevokeByUser(userID)
			}
			markAuthPersistenceWarningHeaders(w)
			writeSuccess(w, http.StatusOK, map[string]interface{}{
				"disabled": req.Disabled,
				"warning":  true,
			}, "user status updated with persistence warning")
			return
		}
		if errors.Is(err, ErrLastAdmin) {
			writeError(w, http.StatusBadRequest, "cannot disable last admin user", "LAST_ADMIN")
			return
		}
		if errors.Is(err, ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found", "USER_NOT_FOUND")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error", "UPDATE_ERROR")
		return
	}

	// If disabling, revoke all tokens
	if *req.Disabled {
		if err := h.tokenManager.RevokeByUser(userID); err != nil {
			markAuthPersistenceWarningHeaders(w)
			writeSuccess(w, http.StatusOK, map[string]interface{}{
				"disabled": req.Disabled,
				"warning":  true,
			}, "user status updated with persistence warning")
			return
		}
	}

	writeSuccess(w, http.StatusOK, map[string]interface{}{
		"disabled": req.Disabled,
	}, "user status updated successfully")
}

func writeError(w http.ResponseWriter, status int, message, code string) {
	writeEnvelope(w, status, ResponseEnvelope{
		Success: false,
		Error: &ErrorDetail{
			Code:    code,
			Message: message,
		},
	})
}

func writeSuccess(w http.ResponseWriter, status int, data interface{}, message string) {
	if data == nil {
		data = json.RawMessage("null")
	}
	writeEnvelope(w, status, ResponseEnvelope{
		Success: true,
		Data:    data,
		Message: message,
	})
}

func markAuthPersistenceWarningHeaders(w http.ResponseWriter) {
	if w == nil {
		return
	}
	headers := w.Header()
	for _, warningValue := range headers.Values("Warning") {
		if warningValue == authPersistenceWarningHeader {
			return
		}
	}
	headers.Add("Warning", authPersistenceWarningHeader)
}

func writeEnvelope(w http.ResponseWriter, status int, envelope ResponseEnvelope) {
	body, err := json.Marshal(envelope)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"success":false,"error":{"code":"INTERNAL_ERROR","message":"internal server error"}}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
