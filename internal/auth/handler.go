package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/seanbao/mnemonas/internal/requestip"
)

// Handler provides HTTP handlers for authentication
type Handler struct {
	userStore          *UserStore
	tokenManager       *TokenManager
	loginAttempts      *loginAttemptTracker
	loginFailureLimit  int
	loginFailureWindow time.Duration
	loginLockDuration  time.Duration
	usageResolver      UserUsageResolver
}

type UserUsageResolver func(context.Context, *User) (int64, error)

type LoginRateLimitPolicy struct {
	Enabled       bool
	FailureLimit  int
	FailureWindow time.Duration
	LockDuration  time.Duration
}

type loginAttemptTracker struct {
	mu       sync.Mutex
	attempts map[string]loginAttemptState
	now      func() time.Time
}

type loginAttemptState struct {
	failures    int
	lastFailure time.Time
	lockedUntil time.Time
}

const (
	defaultLoginFailureLimit     = 5
	defaultLoginFailureWindow    = 15 * time.Minute
	defaultLoginLockDuration     = 5 * time.Minute
	defaultLoginRateLimitMessage = "too many login attempts, try later"
	defaultJSONRequestBodyLimit  = 1 * 1024 * 1024
	authPersistenceWarningHeader = `199 MnemoNAS "auth state persistence incomplete"`
	sessionModeHeader            = "X-MnemoNAS-Session-Mode"
	sessionModeCookie            = "cookie"
	sessionCookiePath            = "/api/v1"
	refreshSessionCookiePath     = "/api/v1/auth/refresh"
	downloadSessionCookiePath    = "/api/v1"
	downloadSessionSameSite      = http.SameSiteStrictMode
)

func newLoginAttemptTracker() *loginAttemptTracker {
	return &loginAttemptTracker{
		attempts: make(map[string]loginAttemptState),
		now:      time.Now,
	}
}

func (t *loginAttemptTracker) isLocked(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
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

func (t *loginAttemptTracker) recordFailure(key string, limit int, failureWindow, lockDuration time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	t.pruneExpiredLocked(now, failureWindow)
	state := t.attempts[key]
	if failureWindow > 0 && !state.lastFailure.IsZero() && now.Sub(state.lastFailure) > failureWindow {
		state = loginAttemptState{}
	}
	state.failures++
	state.lastFailure = now
	if state.failures >= limit {
		state.lockedUntil = now.Add(lockDuration)
	}
	t.attempts[key] = state

	return !state.lockedUntil.IsZero()
}

func (t *loginAttemptTracker) pruneExpiredLocked(now time.Time, failureWindow time.Duration) {
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

func (t *loginAttemptTracker) reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.attempts, key)
}

// NewHandler creates a new auth handler
func NewHandler(us *UserStore, tm *TokenManager) *Handler {
	return &Handler{
		userStore:          us,
		tokenManager:       tm,
		loginAttempts:      newLoginAttemptTracker(),
		loginFailureLimit:  defaultLoginFailureLimit,
		loginFailureWindow: defaultLoginFailureWindow,
		loginLockDuration:  defaultLoginLockDuration,
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
		FailureLimit:  h.loginFailureLimit,
		FailureWindow: h.loginFailureWindow,
		LockDuration:  h.loginLockDuration,
	}
	policy.Enabled = h.loginAttempts != nil &&
		policy.FailureLimit > 0 &&
		policy.FailureWindow > 0 &&
		policy.LockDuration > 0
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
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Email    string   `json:"email,omitempty"`
	Role     Role     `json:"role"`
	Groups   []string `json:"groups,omitempty"`
	HomeDir  string   `json:"home_dir"`
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
		"id":          user.ID,
		"username":    user.Username,
		"email":       user.Email,
		"role":        user.Role,
		"groups":      append([]string(nil), user.Groups...),
		"disabled":    user.Disabled,
		"home_dir":    user.HomeDir,
		"created_at":  user.CreatedAt,
		"updated_at":  user.UpdatedAt,
		"quota_bytes": user.QuotaBytes,
		"used_bytes":  user.UsedBytes,
	}
	if user.LastLoginAt != nil {
		info["last_login_at"] = user.LastLoginAt
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
			ID:       user.ID,
			Username: user.Username,
			Email:    user.Email,
			Role:     user.Role,
			Groups:   append([]string(nil), user.Groups...),
			HomeDir:  user.HomeDir,
		},
	}
	if includeTokens {
		resp.AccessToken = tokenPair.AccessToken
		resp.RefreshToken = tokenPair.RefreshToken
	}
	return resp
}

func setSessionCookies(w http.ResponseWriter, r *http.Request, tokenPair *TokenPair) {
	setAuthCookie(w, r, AccessSessionCookieName, tokenPair.AccessToken, sessionCookiePath, tokenPair.ExpiresAt)
	setAuthCookie(w, r, RefreshSessionCookieName, tokenPair.RefreshToken, refreshSessionCookiePath, tokenPair.RefreshExpiresAt)
}

func setAuthCookie(w http.ResponseWriter, r *http.Request, name, value, path string, expires time.Time) {
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
		Secure:   requestIsHTTPS(r),
		Expires:  expires.UTC(),
		MaxAge:   maxAge,
	})
}

func clearSessionCookies(w http.ResponseWriter, r *http.Request) {
	clearAuthCookie(w, r, AccessSessionCookieName, sessionCookiePath)
	clearAuthCookie(w, r, RefreshSessionCookieName, refreshSessionCookiePath)
}

func clearAuthCookie(w http.ResponseWriter, r *http.Request, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
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
	req.Username = strings.TrimSpace(req.Username)

	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required", "MISSING_CREDENTIALS")
		return
	}

	attemptKey := loginAttemptKey(r, req.Username)
	if h.loginAttempts != nil && h.loginAttempts.isLocked(attemptKey) {
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
		writeError(w, http.StatusInternalServerError, "internal server error", "TOKEN_ERROR")
		return
	}

	setSessionCookies(w, r, tokenPair)
	resp := loginResponseFromTokenPair(tokenPair, user, !requestWantsCookieSession(r))

	message := ""
	if loginWarning {
		message = "login succeeded with persistence warning"
	}
	writeSuccess(w, http.StatusOK, resp, message)
}

func loginAttemptKey(r *http.Request, username string) string {
	return normalizeUsername(strings.TrimSpace(username)) + "|" + requestip.ClientIP(r)
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
		refreshTokens = cookieValuesFromRequest(r, RefreshSessionCookieName)
		refreshFromCookie = len(refreshTokens) > 0
	}

	if len(refreshTokens) == 0 {
		clearSessionCookies(w, r)
		clearDownloadSessionCookie(w, r)
		writeError(w, http.StatusBadRequest, "refresh token required", "MISSING_TOKEN")
		return
	}

	claims, err := h.validateRefreshTokenCandidates(refreshTokens)
	if err != nil {
		if refreshFromCookie && shouldClearSessionCookiesAfterRefreshFailure(err) {
			clearSessionCookies(w, r)
			clearDownloadSessionCookie(w, r)
		}
		writeRefreshTokenError(w, err)
		return
	}
	userID := claims.Subject

	user, err := h.userStore.GetByID(userID)
	if err != nil {
		if refreshFromCookie {
			clearSessionCookies(w, r)
			clearDownloadSessionCookie(w, r)
		}
		writeError(w, http.StatusUnauthorized, "user not found", "USER_NOT_FOUND")
		return
	}

	if user.Disabled {
		if refreshFromCookie {
			clearSessionCookies(w, r)
			clearDownloadSessionCookie(w, r)
		}
		writeError(w, http.StatusForbidden, "user account is disabled", "USER_DISABLED")
		return
	}

	tokenPair, err := h.tokenManager.GenerateTokenPair(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error", "TOKEN_ERROR")
		return
	}

	revocationWarning := false
	if err := h.tokenManager.consumeRefreshTokenClaims(claims); err != nil {
		if isAuthPersistenceWarning(err) {
			revocationWarning = true
		} else {
			if refreshFromCookie && shouldClearSessionCookiesAfterRefreshFailure(err) {
				clearSessionCookies(w, r)
				clearDownloadSessionCookie(w, r)
			}
			writeRefreshTokenError(w, err)
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

func (h *Handler) validateRefreshTokenCandidates(candidates []string) (*jwt.RegisteredClaims, error) {
	var firstErr error
	for _, refreshToken := range candidates {
		claims, err := h.tokenManager.validateRefreshTokenClaims(refreshToken)
		if err == nil {
			return claims, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		firstErr = ErrInvalidToken
	}
	return nil, firstErr
}

func shouldClearSessionCookiesAfterRefreshFailure(err error) bool {
	return !errors.Is(err, errRefreshTokenReused)
}

func writeRefreshTokenError(w http.ResponseWriter, err error) {
	switch {
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

	claims := GetClaimsFromContext(r.Context())
	if claims != nil {
		if err := h.tokenManager.RevokeToken(claims.TokenID); err != nil {
			clearSessionCookies(w, r)
			clearDownloadSessionCookie(w, r)
			if isAuthPersistenceWarning(err) {
				markAuthPersistenceWarningHeaders(w)
				writeSuccess(w, http.StatusOK, nil, "logged out with persistence warning")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal server error", "TOKEN_ERROR")
			return
		}
	}
	clearSessionCookies(w, r)
	clearDownloadSessionCookie(w, r)

	writeSuccess(w, http.StatusOK, nil, "logged out successfully")
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

	http.SetCookie(w, &http.Cookie{
		Name:     string(DownloadSessionCookieName),
		Value:    accessToken,
		Path:     downloadSessionCookiePath,
		HttpOnly: true,
		SameSite: downloadSessionSameSite,
		Secure:   requestIsHTTPS(r),
		Expires:  claims.ExpiresAt.Time.UTC(),
		MaxAge:   maxAge,
	})

	writeSuccess(w, http.StatusOK, nil, "")
}

func clearDownloadSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     string(DownloadSessionCookieName),
		Value:    "",
		Path:     downloadSessionCookiePath,
		HttpOnly: true,
		SameSite: downloadSessionSameSite,
		Secure:   requestIsHTTPS(r),
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
			ID:       user.ID,
			Username: user.Username,
			Email:    user.Email,
			Role:     user.Role,
			Groups:   append([]string(nil), user.Groups...),
			HomeDir:  user.HomeDir,
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
			if revokeErr := h.tokenManager.RevokeByUser(user.ID); revokeErr != nil && !isAuthPersistenceWarning(revokeErr) {
				writeError(w, http.StatusInternalServerError, "internal server error", "PASSWORD_ERROR")
				return
			}
			markAuthPersistenceWarningHeaders(w)
			clearSessionCookies(w, r)
			clearDownloadSessionCookie(w, r)
			writeSuccess(w, http.StatusOK, map[string]interface{}{"warning": true}, "password changed with persistence warning")
			return
		}
		switch err {
		case ErrInvalidCredentials:
			writeError(w, http.StatusUnauthorized, "current password is incorrect", "INVALID_PASSWORD")
		case ErrPasswordTooShort:
			writeError(w, http.StatusBadRequest, "password must be at least 8 characters", "PASSWORD_TOO_SHORT")
		case ErrPasswordTooLong:
			writeError(w, http.StatusBadRequest, "password must be at most 72 bytes", "PASSWORD_TOO_LONG")
		default:
			writeError(w, http.StatusInternalServerError, "internal server error", "PASSWORD_ERROR")
		}
		return
	}

	// Revoke all tokens for this user
	if err := h.tokenManager.RevokeByUser(user.ID); err != nil {
		if isAuthPersistenceWarning(err) {
			markAuthPersistenceWarningHeaders(w)
			clearSessionCookies(w, r)
			clearDownloadSessionCookie(w, r)
			writeSuccess(w, http.StatusOK, map[string]interface{}{"warning": true}, "password changed with persistence warning")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error", "PASSWORD_ERROR")
		return
	}

	clearSessionCookies(w, r)
	clearDownloadSessionCookie(w, r)
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

	for _, u := range users {
		userInfos = append(userInfos, h.fullUserResponse(r.Context(), u))
	}

	writeSuccess(w, http.StatusOK, map[string]interface{}{
		"users": userInfos,
		"total": len(userInfos),
	}, "")
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
			writeError(w, http.StatusBadRequest, "password must be at least 8 characters", "PASSWORD_TOO_SHORT")
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

	user, err := h.userStore.GetByID(userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found", "USER_NOT_FOUND")
		return
	}

	if req.Email != nil {
		user.Email = strings.TrimSpace(*req.Email)
	}
	if req.Role != nil {
		role := Role(strings.ToLower(strings.TrimSpace(*req.Role)))
		if role != RoleAdmin && role != RoleUser && role != RoleGuest {
			writeError(w, http.StatusBadRequest, "invalid role, must be admin, user, or guest", "INVALID_ROLE")
			return
		}
		currentUser := GetUserFromContext(r.Context())
		if currentUser != nil && currentUser.ID == userID && role != RoleAdmin {
			writeError(w, http.StatusBadRequest, "cannot change your own admin role", "SELF_ROLE_CHANGE")
			return
		}
		if user.Role == RoleAdmin && !user.Disabled && role != RoleAdmin {
			activeAdmins := 0
			for _, u := range h.userStore.List() {
				if u.Role == RoleAdmin && !u.Disabled {
					activeAdmins++
				}
			}
			if activeAdmins <= 1 {
				writeError(w, http.StatusBadRequest, "cannot remove last admin user", "LAST_ADMIN")
				return
			}
		}
		user.Role = role
	}
	if req.HomeDir != nil {
		user.HomeDir = strings.TrimSpace(*req.HomeDir)
	}
	if req.Groups != nil {
		user.Groups = append([]string(nil), (*req.Groups)...)
	}
	if req.QuotaBytes != nil {
		if *req.QuotaBytes < 0 {
			writeError(w, http.StatusBadRequest, "quota_bytes must be greater than or equal to 0", "INVALID_QUOTA")
			return
		}
		user.QuotaBytes = *req.QuotaBytes
	}

	if err := h.userStore.Update(user); err != nil {
		if isAuthPersistenceWarning(err) {
			updatedUser := user
			if storedUser, getErr := h.userStore.GetByID(userID); getErr == nil {
				updatedUser = storedUser
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

	updatedUser := user
	if storedUser, getErr := h.userStore.GetByID(userID); getErr == nil {
		updatedUser = storedUser
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
			if revokeErr := h.tokenManager.RevokeByUser(userID); revokeErr != nil && !isAuthPersistenceWarning(revokeErr) {
				writeError(w, http.StatusInternalServerError, "internal server error", "DELETE_ERROR")
				return
			}
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
		if isAuthPersistenceWarning(err) {
			markAuthPersistenceWarningHeaders(w)
			writeSuccess(w, http.StatusOK, map[string]interface{}{"warning": true}, "user deleted with persistence warning")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error", "DELETE_ERROR")
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

	if err := h.userStore.ResetPassword(userID, req.NewPassword); err != nil {
		if isAuthPersistenceWarning(err) {
			if revokeErr := h.tokenManager.RevokeByUser(userID); revokeErr != nil && !isAuthPersistenceWarning(revokeErr) {
				writeError(w, http.StatusInternalServerError, "internal server error", "RESET_ERROR")
				return
			}
			markAuthPersistenceWarningHeaders(w)
			writeSuccess(w, http.StatusOK, map[string]interface{}{"warning": true}, "password reset with persistence warning")
			return
		}
		switch err {
		case ErrUserNotFound:
			writeError(w, http.StatusNotFound, "user not found", "USER_NOT_FOUND")
		case ErrPasswordTooShort:
			writeError(w, http.StatusBadRequest, "password must be at least 8 characters", "PASSWORD_TOO_SHORT")
		case ErrPasswordTooLong:
			writeError(w, http.StatusBadRequest, "password must be at most 72 bytes", "PASSWORD_TOO_LONG")
		default:
			writeError(w, http.StatusInternalServerError, "internal server error", "RESET_ERROR")
		}
		return
	}

	// Revoke all tokens for this user
	if err := h.tokenManager.RevokeByUser(userID); err != nil {
		if isAuthPersistenceWarning(err) {
			markAuthPersistenceWarningHeaders(w)
			writeSuccess(w, http.StatusOK, map[string]interface{}{"warning": true}, "password reset with persistence warning")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error", "RESET_ERROR")
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

	user, err := h.userStore.GetByID(userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found", "USER_NOT_FOUND")
		return
	}

	// Prevent disabling self
	currentUser := GetUserFromContext(r.Context())
	if currentUser != nil && currentUser.ID == userID && *req.Disabled {
		writeError(w, http.StatusBadRequest, "cannot disable your own account", "SELF_DISABLE")
		return
	}

	// Prevent disabling last admin
	if user.Role == RoleAdmin && *req.Disabled {
		// Count active admins
		activeAdmins := 0
		for _, u := range h.userStore.List() {
			if u.Role == RoleAdmin && !u.Disabled {
				activeAdmins++
			}
		}
		if activeAdmins <= 1 {
			writeError(w, http.StatusBadRequest, "cannot disable last admin user", "LAST_ADMIN")
			return
		}
	}

	user.Disabled = *req.Disabled
	if err := h.userStore.Update(user); err != nil {
		if isAuthPersistenceWarning(err) {
			if *req.Disabled {
				if revokeErr := h.tokenManager.RevokeByUser(userID); revokeErr != nil && !isAuthPersistenceWarning(revokeErr) {
					writeError(w, http.StatusInternalServerError, "internal server error", "UPDATE_ERROR")
					return
				}
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
		writeError(w, http.StatusInternalServerError, "internal server error", "UPDATE_ERROR")
		return
	}

	// If disabling, revoke all tokens
	if *req.Disabled {
		if err := h.tokenManager.RevokeByUser(userID); err != nil {
			if isAuthPersistenceWarning(err) {
				markAuthPersistenceWarningHeaders(w)
				writeSuccess(w, http.StatusOK, map[string]interface{}{
					"disabled": req.Disabled,
					"warning":  true,
				}, "user status updated with persistence warning")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal server error", "UPDATE_ERROR")
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
