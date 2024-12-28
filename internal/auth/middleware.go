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

		// Get token from header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
			return
		}

		// Parse Bearer token
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			http.Error(w, `{"error":"invalid authorization header format"}`, http.StatusUnauthorized)
			return
		}

		tokenString := parts[1]

		// Validate token
		claims, err := m.tokenManager.ValidateAccessToken(tokenString)
		if err != nil {
			switch err {
			case ErrTokenExpired:
				http.Error(w, `{"error":"token expired"}`, http.StatusUnauthorized)
			case ErrTokenRevoked:
				http.Error(w, `{"error":"token revoked"}`, http.StatusUnauthorized)
			default:
				http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			}
			return
		}

		// Get user from store
		user, err := m.userStore.GetByID(claims.UserID)
		if err != nil {
			http.Error(w, `{"error":"user not found"}`, http.StatusUnauthorized)
			return
		}

		if user.Disabled {
			http.Error(w, `{"error":"user is disabled"}`, http.StatusForbidden)
			return
		}

		// Add user and claims to context
		ctx := context.WithValue(r.Context(), ContextKeyUser, user)
		ctx = context.WithValue(ctx, ContextKeyClaims, claims)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
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

// IsAdmin checks if the user in context is admin
func IsAdmin(ctx context.Context) bool {
	user := GetUserFromContext(ctx)
	return user != nil && user.Role == RoleAdmin
}
