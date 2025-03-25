package auth

import (
	"context"
	"net/http"
	"strings"
)

// Context keys for user information
type contextKey string

const (
	ContextKeyUser   contextKey = "user"
	ContextKeyClaims contextKey = "claims"
)

const DownloadSessionCookieName = "mnemonas_download_access"

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
			if strings.HasPrefix(r.URL.Path, path) {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Get token from header or download-session cookie.
		tokenString := ""
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				writeError(w, http.StatusUnauthorized, "invalid authorization header format", "INVALID_AUTH_HEADER")
				return
			}
			tokenString = parts[1]
		} else if allowDownloadSessionCookie(r) {
			if cookie, err := r.Cookie(DownloadSessionCookieName); err == nil {
				tokenString = cookie.Value
			}
		}
		if tokenString == "" {
			writeError(w, http.StatusUnauthorized, "missing authorization header", "MISSING_AUTH_HEADER")
			return
		}

		// Validate token
		claims, err := m.tokenManager.ValidateAccessToken(tokenString)
		if err != nil {
			switch err {
			case ErrTokenExpired:
				writeError(w, http.StatusUnauthorized, "token expired", "TOKEN_EXPIRED")
			case ErrTokenRevoked:
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

		// Add user and claims to context
		ctx := context.WithValue(r.Context(), ContextKeyUser, user)
		ctx = context.WithValue(ctx, ContextKeyClaims, claims)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func allowDownloadSessionCookie(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	path := r.URL.Path
	return strings.HasPrefix(path, "/api/v1/download") || strings.HasPrefix(path, "/api/v1/thumbnails")
}

// RequireRole middleware that requires a specific role
func (m *Middleware) RequireRole(roles ...Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := GetUserFromContext(r.Context())
			if user == nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
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
				http.Error(w, `{"error":"insufficient permissions"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// OptionalAuth middleware that adds user to context if token is present
func (m *Middleware) OptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			next.ServeHTTP(w, r)
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			next.ServeHTTP(w, r)
			return
		}

		claims, err := m.tokenManager.ValidateAccessToken(parts[1])
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		user, err := m.userStore.GetByID(claims.UserID)
		if err != nil || user.Disabled {
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), ContextKeyUser, user)
		ctx = context.WithValue(ctx, ContextKeyClaims, claims)
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

// WithClaimsContext adds claims to the context for testing or internal use.
func WithClaimsContext(ctx context.Context, claims *TokenClaims) context.Context {
	if claims == nil {
		return ctx
	}
	return context.WithValue(ctx, ContextKeyClaims, claims)
}

// IsAdmin checks if the user in context is admin
func IsAdmin(ctx context.Context) bool {
	user := GetUserFromContext(ctx)
	return user != nil && user.Role == RoleAdmin
}
