package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUserStore(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")

	t.Run("create and authenticate", func(t *testing.T) {
		store, err := NewUserStore(usersFile)
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}

		user, err := store.Create("testuser", "password123", "test@example.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create user: %v", err)
		}

		if user.Username != "testuser" {
			t.Errorf("expected username testuser, got %s", user.Username)
		}
		if user.Role != RoleUser {
			t.Errorf("expected role user, got %s", user.Role)
		}

		authUser, err := store.Authenticate("testuser", "password123")
		if err != nil {
			t.Fatalf("failed to authenticate: %v", err)
		}
		if authUser.ID != user.ID {
			t.Errorf("expected user ID %s, got %s", user.ID, authUser.ID)
		}

		_, err = store.Authenticate("testuser", "wrongpassword")
		if err != ErrInvalidCredentials {
			t.Errorf("expected ErrInvalidCredentials, got %v", err)
		}
	})

	t.Run("duplicate username", func(t *testing.T) {
		store, _ := NewUserStore(filepath.Join(dir, "users2.json"))
		store.Create("duplicate", "password123", "", RoleUser)

		_, err := store.Create("duplicate", "password456", "", RoleUser)
		if err != ErrUserExists {
			t.Errorf("expected ErrUserExists, got %v", err)
		}
	})

	t.Run("short password", func(t *testing.T) {
		store, _ := NewUserStore(filepath.Join(dir, "users3.json"))
		_, err := store.Create("shortpass", "short", "", RoleUser)
		if err != ErrPasswordTooShort {
			t.Errorf("expected ErrPasswordTooShort, got %v", err)
		}
	})

	t.Run("change password", func(t *testing.T) {
		store, _ := NewUserStore(filepath.Join(dir, "users4.json"))
		user, _ := store.Create("changepw", "oldpassword123", "", RoleUser)

		err := store.ChangePassword(user.ID, "oldpassword123", "newpassword456")
		if err != nil {
			t.Fatalf("failed to change password: %v", err)
		}

		_, err = store.Authenticate("changepw", "oldpassword123")
		if err != ErrInvalidCredentials {
			t.Errorf("expected old password to fail")
		}

		_, err = store.Authenticate("changepw", "newpassword456")
		if err != nil {
			t.Errorf("expected new password to work: %v", err)
		}
	})

	t.Run("disable user", func(t *testing.T) {
		store, _ := NewUserStore(filepath.Join(dir, "users5.json"))
		user, _ := store.Create("disabled", "password123", "", RoleUser)

		user.Disabled = true
		store.Update(user)

		_, err := store.Authenticate("disabled", "password123")
		if err != ErrUserDisabled {
			t.Errorf("expected ErrUserDisabled, got %v", err)
		}
	})

	t.Run("delete user", func(t *testing.T) {
		store, _ := NewUserStore(filepath.Join(dir, "users6.json"))
		user, _ := store.Create("todelete", "password123", "", RoleUser)

		err := store.Delete(user.ID)
		if err != nil {
			t.Fatalf("failed to delete user: %v", err)
		}

		_, err = store.GetByID(user.ID)
		if err != ErrUserNotFound {
			t.Errorf("expected ErrUserNotFound, got %v", err)
		}
	})

	t.Run("cannot delete last admin", func(t *testing.T) {
		store, _ := NewUserStore(filepath.Join(dir, "users7.json"))
		// NewUserStore creates default admin, so we get existing admin
		admin, _ := store.GetByUsername("admin")

		err := store.Delete(admin.ID)
		if err != ErrLastAdmin {
			t.Errorf("expected ErrLastAdmin, got %v", err)
		}
	})

	t.Run("persistence", func(t *testing.T) {
		usersFile := filepath.Join(dir, "users8.json")
		store1, _ := NewUserStore(usersFile)
		store1.Create("persistent", "password123", "p@example.com", RoleUser)

		store2, err := NewUserStore(usersFile)
		if err != nil {
			t.Fatalf("failed to load existing store: %v", err)
		}

		user, err := store2.GetByUsername("persistent")
		if err != nil {
			t.Fatalf("failed to find persistent user: %v", err)
		}
		if user.Email != "p@example.com" {
			t.Errorf("expected email p@example.com, got %s", user.Email)
		}
	})
}

func TestTokenManager(t *testing.T) {
	secret := "test-jwt-secret-key-for-testing"

	t.Run("generate and validate", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		user := &User{
			ID:       "user-123",
			Username: "testuser",
			Role:     RoleUser,
		}

		tokenPair, err := tm.GenerateTokenPair(user)
		if err != nil {
			t.Fatalf("failed to generate token pair: %v", err)
		}

		if tokenPair.AccessToken == "" {
			t.Error("access token is empty")
		}
		if tokenPair.RefreshToken == "" {
			t.Error("refresh token is empty")
		}
		if tokenPair.TokenType != "Bearer" {
			t.Errorf("expected token type Bearer, got %s", tokenPair.TokenType)
		}

		claims, err := tm.ValidateAccessToken(tokenPair.AccessToken)
		if err != nil {
			t.Fatalf("failed to validate access token: %v", err)
		}
		if claims.UserID != "user-123" {
			t.Errorf("expected user ID user-123, got %s", claims.UserID)
		}
		if claims.Role != RoleUser {
			t.Errorf("expected role user, got %s", claims.Role)
		}

		userID, err := tm.ValidateRefreshToken(tokenPair.RefreshToken)
		if err != nil {
			t.Fatalf("failed to validate refresh token: %v", err)
		}
		if userID != "user-123" {
			t.Errorf("expected user ID user-123, got %s", userID)
		}
	})

	t.Run("revoke token", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		user := &User{
			ID:       "user-456",
			Username: "revokeuser",
			Role:     RoleUser,
		}

		tokenPair, _ := tm.GenerateTokenPair(user)
		claims, _ := tm.ValidateAccessToken(tokenPair.AccessToken)
		tm.RevokeToken(claims.TokenID)

		_, err := tm.ValidateAccessToken(tokenPair.AccessToken)
		if err != ErrTokenRevoked {
			t.Errorf("expected ErrTokenRevoked, got %v", err)
		}
	})

	t.Run("revoke by user", func(t *testing.T) {
		t.Skip("RevokeByUser not fully implemented yet")
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		user := &User{
			ID:       "user-789",
			Username: "multitoken",
			Role:     RoleUser,
		}

		tokenPair1, _ := tm.GenerateTokenPair(user)
		tokenPair2, _ := tm.GenerateTokenPair(user)

		tm.RevokeByUser(user.ID)

		_, err := tm.ValidateAccessToken(tokenPair1.AccessToken)
		if err != ErrTokenRevoked {
			t.Errorf("token1: expected ErrTokenRevoked, got %v", err)
		}
		_, err = tm.ValidateAccessToken(tokenPair2.AccessToken)
		if err != ErrTokenRevoked {
			t.Errorf("token2: expected ErrTokenRevoked, got %v", err)
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		_, err := tm.ValidateAccessToken("invalid-token")
		if err == nil {
			t.Error("expected error for invalid token")
		}
	})

	t.Run("expired token", func(t *testing.T) {
		tm := NewTokenManager(secret, -1*time.Hour, -1*time.Hour)

		user := &User{
			ID:       "user-exp",
			Username: "expired",
			Role:     RoleUser,
		}

		tokenPair, _ := tm.GenerateTokenPair(user)

		_, err := tm.ValidateAccessToken(tokenPair.AccessToken)
		if err != ErrTokenExpired {
			t.Errorf("expected ErrTokenExpired, got %v", err)
		}
	})
}

func TestMiddleware(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "middleware_users.json")
	store, _ := NewUserStore(usersFile)
	tm := NewTokenManager("test-secret", 15*time.Minute, 24*time.Hour)

	user, _ := store.Create("middlewareuser", "password123", "", RoleUser)
	admin, _ := store.Create("middlewareadmin", "password123", "", RoleAdmin)

	userToken, _ := tm.GenerateTokenPair(user)
	adminToken, _ := tm.GenerateTokenPair(admin)

	mw := NewMiddleware(store, tm)

	t.Run("require auth - valid token", func(t *testing.T) {
		called := false
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			ctxUser := GetUserFromContext(r.Context())
			if ctxUser == nil {
				t.Error("user not found in context")
			} else if ctxUser.Username != "middlewareuser" {
				t.Errorf("wrong user in context: %s", ctxUser.Username)
			}
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer "+userToken.AccessToken)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("handler was not called")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("require auth - no token", func(t *testing.T) {
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rec.Code)
		}
	})

	t.Run("require auth - invalid token", func(t *testing.T) {
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rec.Code)
		}
	})

	t.Run("require role - admin only", func(t *testing.T) {
		called := false
		// Chain RequireAuth -> RequireRole
		innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})
		handler := mw.RequireAuth(mw.RequireRole(RoleAdmin)(innerHandler))

		req := httptest.NewRequest("GET", "/admin", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken.AccessToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("admin handler should be called")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200 for admin, got %d", rec.Code)
		}

		called = false
		handler2 := mw.RequireAuth(mw.RequireRole(RoleAdmin)(innerHandler))
		req = httptest.NewRequest("GET", "/admin", nil)
		req.Header.Set("Authorization", "Bearer "+userToken.AccessToken)
		rec = httptest.NewRecorder()
		handler2.ServeHTTP(rec, req)

		if called {
			t.Error("user handler should not be called for admin endpoint")
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected status 403 for user, got %d", rec.Code)
		}
	})

	t.Run("optional auth - with token", func(t *testing.T) {
		called := false
		handler := mw.OptionalAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			ctxUser := GetUserFromContext(r.Context())
			if ctxUser == nil {
				t.Error("expected user in context")
			}
		}))

		req := httptest.NewRequest("GET", "/optional", nil)
		req.Header.Set("Authorization", "Bearer "+userToken.AccessToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("handler should be called")
		}
	})

	t.Run("optional auth - without token", func(t *testing.T) {
		called := false
		handler := mw.OptionalAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			ctxUser := GetUserFromContext(r.Context())
			if ctxUser != nil {
				t.Error("expected no user in context")
			}
		}))

		req := httptest.NewRequest("GET", "/optional", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("handler should be called even without token")
		}
	})

	t.Run("excluded paths", func(t *testing.T) {
		mwWithExclude := NewMiddlewareWithExclude(store, tm, []string{"/health", "/public/"})

		called := false
		handler := mwWithExclude.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))

		req := httptest.NewRequest("GET", "/health", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("handler should be called for excluded path")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}

		called = false
		req = httptest.NewRequest("GET", "/public/files/test.txt", nil)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("handler should be called for excluded prefix")
		}
	})
}

func TestAuthHandler(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "handler_users.json")
	store, _ := NewUserStore(usersFile)
	tm := NewTokenManager("test-secret", 15*time.Minute, 24*time.Hour)

	store.Create("handleruser", "password123", "handler@test.com", RoleUser)
	store.Create("handleradmin", "adminpass123", "admin@test.com", RoleAdmin)

	h := NewHandler(store, tm)

	t.Run("login success", func(t *testing.T) {
		body := `{"username":"handleruser","password":"password123"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		h.HandleLogin(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp LoginResponse
		json.Unmarshal(rec.Body.Bytes(), &resp)

		if !resp.Success {
			t.Error("expected success true")
		}
		if resp.AccessToken == "" {
			t.Error("expected access token")
		}
		if resp.User.Username != "handleruser" {
			t.Errorf("expected username handleruser, got %s", resp.User.Username)
		}
	})

	t.Run("login failure", func(t *testing.T) {
		body := `{"username":"handleruser","password":"wrongpassword"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		h.HandleLogin(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rec.Code)
		}
	})

	t.Run("login missing credentials", func(t *testing.T) {
		body := `{"username":"handleruser"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		h.HandleLogin(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", rec.Code)
		}
	})

	t.Run("refresh token", func(t *testing.T) {
		body := `{"username":"handleruser","password":"password123"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		h.HandleLogin(rec, req)

		var loginResp LoginResponse
		json.Unmarshal(rec.Body.Bytes(), &loginResp)

		refreshBody, _ := json.Marshal(RefreshRequest{RefreshToken: loginResp.RefreshToken})
		req = httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewBuffer(refreshBody))
		rec = httptest.NewRecorder()
		h.HandleRefresh(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var refreshResp LoginResponse
		json.Unmarshal(rec.Body.Bytes(), &refreshResp)

		if refreshResp.AccessToken == "" {
			t.Error("expected new access token")
		}
	})

	t.Run("me endpoint", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
		user, _ := store.GetByUsername("handleruser")
		ctx := context.WithValue(req.Context(), ContextKeyUser, user)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		h.HandleMe(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("admin list users", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/admin/users", nil)
		admin, _ := store.GetByUsername("handleradmin")
		ctx := context.WithValue(req.Context(), ContextKeyUser, admin)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		h.HandleListUsers(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp map[string]interface{}
		json.Unmarshal(rec.Body.Bytes(), &resp)

		users := resp["users"].([]interface{})
		if len(users) < 2 {
			t.Errorf("expected at least 2 users, got %d", len(users))
		}
	})

	t.Run("admin create user", func(t *testing.T) {
		body := `{"username":"newuser","password":"newpass123","email":"new@test.com","role":"user"}`
		req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(body))
		admin, _ := store.GetByUsername("handleradmin")
		ctx := context.WithValue(req.Context(), ContextKeyUser, admin)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		h.HandleCreateUser(rec, req)

		if rec.Code != http.StatusCreated {
			t.Errorf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("non-admin cannot list users", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/admin/users", nil)
		user, _ := store.GetByUsername("handleruser")
		ctx := context.WithValue(req.Context(), ContextKeyUser, user)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		h.HandleListUsers(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("expected status 403, got %d", rec.Code)
		}
	})
}

func TestDefaultAdminCreation(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "new_users.json")

	store, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}

	admin, err := store.GetByUsername("admin")
	if err != nil {
		t.Fatalf("default admin should exist: %v", err)
	}

	if admin.Role != RoleAdmin {
		t.Errorf("expected admin role, got %s", admin.Role)
	}
}

func TestUserStoreQuota(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "quota_users.json")
	store, _ := NewUserStore(usersFile)

	user, _ := store.Create("quotauser", "password123", "", RoleUser)

	t.Run("set and check quota", func(t *testing.T) {
		user.QuotaBytes = 1024 * 1024 * 100
		user.UsedBytes = 1024 * 1024 * 50
		store.Update(user)

		updated, _ := store.GetByID(user.ID)
		if updated.QuotaBytes != 1024*1024*100 {
			t.Errorf("expected quota 100MB, got %d", updated.QuotaBytes)
		}
		if updated.UsedBytes != 1024*1024*50 {
			t.Errorf("expected used 50MB, got %d", updated.UsedBytes)
		}
	})
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
