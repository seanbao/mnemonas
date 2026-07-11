package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// Context keys for user information
type contextKey string

const (
	ContextKeyUser        contextKey = "user"
	ContextKeyClaims      contextKey = "claims"
	ContextKeyAccessToken contextKey = "access_token"
)

const (
	AccessSessionCookieName        = "mnemonas_access"
	RefreshSessionCookieName       = "mnemonas_refresh"
	DownloadSessionCookieName      = "mnemonas_download_access"
	HTTPSAccessSessionCookieName   = "__Host-mnemonas_access"
	HTTPSRefreshSessionCookieName  = "__Host-mnemonas_refresh"
	HTTPSDownloadSessionCookieName = "__Host-mnemonas_download_access"
)

var errAmbiguousSessionCookies = errors.New("ambiguous session cookies")

// Middleware provides authentication middleware
type Middleware struct {
	tokenManager *TokenManager
	userStore    *UserStore
	excludePaths []string // Paths that don't require auth
}

// NewMiddleware creates a new auth middleware
func NewMiddleware(us *UserStore, tm *TokenManager) *Middleware {
	return &Middleware{
		tokenManager: tm,
		userStore:    us,
		excludePaths: []string{
			"/health",
			"/api/v1/auth/login",
			"/api/v1/auth/refresh",
			"/api/v1/version",
		},
	}
}

// NewMiddlewareWithExclude creates middleware with custom excluded paths
func NewMiddlewareWithExclude(us *UserStore, tm *TokenManager, excludePaths []string) *Middleware {
	return &Middleware{
		tokenManager: tm,
		userStore:    us,
		excludePaths: excludePaths,
	}
}

// SetExcludePaths sets paths that don't require authentication
func (m *Middleware) SetExcludePaths(paths []string) {
	m.excludePaths = paths
}

// RequireAuth middleware that requires valid JWT token
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if path is excluded
		for _, path := range m.excludePaths {
			if pathMatches(r.URL.Path, path) {
				next.ServeHTTP(w, r)
				return
			}
		}

		tokenCandidates, candidateErr := tokenCandidatesFromRequest(r, true)
		if candidateErr != nil {
			if errors.Is(candidateErr, errAmbiguousSessionCookies) {
				writeError(w, http.StatusUnauthorized, "ambiguous session cookies", "INVALID_TOKEN")
				return
			}
			writeError(w, http.StatusUnauthorized, "invalid authorization header format", "INVALID_AUTH_HEADER")
			return
		}
		if len(tokenCandidates) == 0 {
			writeError(w, http.StatusUnauthorized, "missing authorization header", "MISSING_AUTH_HEADER")
			return
		}

		// Validate token
		tokenString, claims, tokenErr := m.validateAccessTokenCandidates(tokenCandidates)
		if tokenErr != nil {
			switch {
			case errors.Is(tokenErr, ErrTokenStateUnavailable):
				writeError(w, http.StatusServiceUnavailable, "token session state unavailable", "TOKEN_STATE_UNAVAILABLE")
			case errors.Is(tokenErr, ErrTokenExpired):
				writeError(w, http.StatusUnauthorized, "token expired", "TOKEN_EXPIRED")
			case errors.Is(tokenErr, ErrTokenRevoked):
				writeError(w, http.StatusUnauthorized, "token has been revoked", "TOKEN_REVOKED")
			default:
				writeError(w, http.StatusUnauthorized, "invalid token", "INVALID_TOKEN")
			}
			return
		}

		// Get user from store
		user, err := m.userStore.GetByID(claims.UserID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "user not found", "USER_NOT_FOUND")
			return
		}

		if user.Disabled {
			writeError(w, http.StatusForbidden, "user account is disabled", "USER_DISABLED")
			return
		}
		if claims.CredentialVersion != user.CredentialVersion {
			writeError(w, http.StatusUnauthorized, "token has been revoked", "TOKEN_REVOKED")
			return
		}
		if user.MustChangePassword && !allowsRequiredPasswordChange(r) {
			writeError(w, http.StatusForbidden, "password change required", "PASSWORD_CHANGE_REQUIRED")
			return
		}

		// Add user and claims to context
		ctx := context.WithValue(r.Context(), ContextKeyUser, user)
		ctx = context.WithValue(ctx, ContextKeyClaims, claims)
		ctx = context.WithValue(ctx, ContextKeyAccessToken, tokenString)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func allowsRequiredPasswordChange(r *http.Request) bool {
	if r == nil {
		return false
	}
	switch r.URL.Path {
	case "/api/v1/auth/me":
		return r.Method == http.MethodGet
	case "/api/v1/auth/password":
		return r.Method == http.MethodPost
	default:
		return false
	}
}

func (m *Middleware) validateAccessTokenCandidates(candidates []string) (string, *TokenClaims, error) {
	var firstErr error
	var selectedToken string
	var selectedClaims *TokenClaims
	for _, tokenString := range candidates {
		claims, err := m.tokenManager.ValidateAccessToken(tokenString)
		if err == nil {
			if selectedClaims != nil && selectedClaims.UserID != claims.UserID {
				return "", nil, errAmbiguousSessionCookies
			}
			if selectedClaims == nil {
				selectedToken = tokenString
				selectedClaims = claims
			}
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if selectedClaims != nil {
		return selectedToken, selectedClaims, nil
	}
	if firstErr == nil {
		firstErr = ErrInvalidToken
	}
	return "", nil, firstErr
}

func tokenCandidatesFromRequest(r *http.Request, allowScopedDownloadCookie bool) ([]string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			return nil, errors.New("invalid authorization header")
		}
		return []string{parts[1]}, nil
	}

	candidates, err := cookieValuesFromRequest(r, AccessSessionCookieName)
	if err != nil {
		return nil, err
	}
	if allowScopedDownloadCookie && allowDownloadSessionCookie(r) {
		downloadCandidates, err := cookieValuesFromRequest(r, DownloadSessionCookieName)
		if err != nil {
			return nil, err
		}
		if len(candidates) > 0 && len(downloadCandidates) > 0 && candidates[0] != downloadCandidates[0] {
			return nil, errAmbiguousSessionCookies
		}
		for _, candidate := range downloadCandidates {
			if !containsCookieValue(candidates, candidate) {
				candidates = append(candidates, candidate)
			}
		}
	}
	return candidates, nil
}

func cookieValuesFromRequest(r *http.Request, name string) ([]string, error) {
	name = cookieNameForRequest(r, name)
	values := make([]string, 0, 1)
	for _, cookie := range r.Cookies() {
		if cookie.Name != name {
			continue
		}
		value := strings.TrimSpace(cookie.Value)
		if value == "" {
			continue
		}
		if containsCookieValue(values, value) {
			continue
		}
		values = append(values, value)
		if len(values) > 1 {
			return nil, errAmbiguousSessionCookies
		}
	}
	return values, nil
}

func containsCookieValue(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func cookieNameForRequest(r *http.Request, name string) string {
	return cookieNameForHTTPSMode(name, requestIsHTTPS(r))
}

func cookieNameForHTTPSMode(name string, secure bool) string {
	if !secure {
		return name
	}
	switch name {
	case AccessSessionCookieName:
		return HTTPSAccessSessionCookieName
	case RefreshSessionCookieName:
		return HTTPSRefreshSessionCookieName
	case DownloadSessionCookieName:
		return HTTPSDownloadSessionCookieName
	default:
		return name
	}
}

func allowDownloadSessionCookie(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	path := r.URL.Path
	return pathMatches(path, "/api/v1/download") || pathMatches(path, "/api/v1/thumbnails")
}

func pathMatches(requestPath, prefix string) bool {
	if prefix == "" {
		return false
	}
	if requestPath == prefix {
		return true
	}
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(requestPath, prefix)
	}
	return strings.HasPrefix(requestPath, prefix+"/")
}

// RequireRole middleware that requires a specific role
func (m *Middleware) RequireRole(roles ...Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := GetUserFromContext(r.Context())
			if user == nil {
				writeError(w, http.StatusUnauthorized, "not authenticated", "NOT_AUTHENTICATED")
				return
			}
			if user.Disabled {
				writeError(w, http.StatusForbidden, "user account is disabled", "USER_DISABLED")
				return
			}

			// Check if user has required role
			hasRole := false
			for _, role := range roles {
				if user.Role == role {
					hasRole = true
					break
				}
			}

			if !hasRole {
				writeError(w, http.StatusForbidden, "insufficient permissions", "INSUFFICIENT_PERMISSIONS")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// OptionalAuth middleware that adds user to context if token is present
func (m *Middleware) OptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCandidates, candidateErr := tokenCandidatesFromRequest(r, false)
		if candidateErr != nil || len(tokenCandidates) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		tokenString, claims, err := m.validateAccessTokenCandidates(tokenCandidates)
		if err != nil {
			if errors.Is(err, ErrTokenStateUnavailable) {
				writeError(w, http.StatusServiceUnavailable, "token session state unavailable", "TOKEN_STATE_UNAVAILABLE")
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		user, err := m.userStore.GetByID(claims.UserID)
		if err != nil || user.Disabled || claims.CredentialVersion != user.CredentialVersion {
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), ContextKeyUser, user)
		ctx = context.WithValue(ctx, ContextKeyClaims, claims)
		ctx = context.WithValue(ctx, ContextKeyAccessToken, tokenString)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetUserFromContext retrieves user from request context
func GetUserFromContext(ctx context.Context) *User {
	user, _ := ctx.Value(ContextKeyUser).(*User)
	return user
}

// GetClaimsFromContext retrieves claims from request context
func GetClaimsFromContext(ctx context.Context) *TokenClaims {
	claims, _ := ctx.Value(ContextKeyClaims).(*TokenClaims)
	return claims
}

// GetAccessTokenFromContext retrieves the bearer token used for request authentication.
func GetAccessTokenFromContext(ctx context.Context) string {
	token, _ := ctx.Value(ContextKeyAccessToken).(string)
	return token
}

// WithClaimsContext adds claims to the context for testing or internal use.
func WithClaimsContext(ctx context.Context, claims *TokenClaims) context.Context {
	if claims == nil {
		return ctx
	}
	return context.WithValue(ctx, ContextKeyClaims, claims)
}

// IsAdmin checks if the context user is an enabled admin.
func IsAdmin(ctx context.Context) bool {
	user := GetUserFromContext(ctx)
	return user != nil && user.Role == RoleAdmin && !user.Disabled
}
