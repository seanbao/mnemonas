package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestWriteAuthFileAtomically_ReturnsDirectorySyncError(t *testing.T) {
	tmpDir := t.TempDir()
	usersPath := filepath.Join(tmpDir, "users.json")

	originalSyncAuthFileDir := syncAuthFileDir
	syncAuthFileDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncAuthFileDir = originalSyncAuthFileDir
	}()

	err := writeAuthFileAtomically(usersPath, []byte("[]"), errUserStoreSymlink, ".users-*.tmp", "users")
	if err == nil {
		t.Fatal("expected writeAuthFileAtomically() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync users directory") {
		t.Fatalf("expected directory sync error, got %v", err)
	}

	data, readErr := os.ReadFile(usersPath)
	if readErr != nil {
		t.Fatalf("expected users file to remain readable after sync failure, got %v", readErr)
	}
	if string(data) != "[]" {
		t.Fatalf("expected users content to be preserved, got %q", string(data))
	}
	info, statErr := os.Stat(usersPath)
	if statErr != nil {
		t.Fatalf("expected users file to exist after sync failure, got %v", statErr)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected users file permissions 0600, got %o", info.Mode().Perm())
	}
}

func TestUserStore(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")

	t.Run("create and authenticate", func(t *testing.T) {
		store, _, err := NewUserStore(usersFile)
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
		store, _, _ := NewUserStore(filepath.Join(dir, "users2.json"))
		store.Create("duplicate", "password123", "", RoleUser)

		_, err := store.Create("duplicate", "password456", "", RoleUser)
		if err != ErrUserExists {
			t.Errorf("expected ErrUserExists, got %v", err)
		}
	})

	t.Run("short password", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users3.json"))
		_, err := store.Create("shortpass", "short", "", RoleUser)
		if err != ErrPasswordTooShort {
			t.Errorf("expected ErrPasswordTooShort, got %v", err)
		}
	})

	t.Run("change password", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users4.json"))
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
		store, _, _ := NewUserStore(filepath.Join(dir, "users5.json"))
		user, _ := store.Create("disabled", "password123", "", RoleUser)

		user.Disabled = true
		store.Update(user)

		_, err := store.Authenticate("disabled", "password123")
		if err != ErrUserDisabled {
			t.Errorf("expected ErrUserDisabled, got %v", err)
		}
	})

	t.Run("returned users are detached copies", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users5-copy.json"))
		user, _ := store.Create("copyuser", "password123", "copy@test.com", RoleUser)

		fetched, err := store.GetByID(user.ID)
		if err != nil {
			t.Fatalf("failed to get user: %v", err)
		}
		fetched.Disabled = true
		fetched.Email = "mutated@test.com"

		fresh, err := store.GetByID(user.ID)
		if err != nil {
			t.Fatalf("failed to get fresh user: %v", err)
		}
		if fresh.Disabled {
			t.Fatal("expected detached copy mutation to leave store state unchanged")
		}
		if fresh.Email != "copy@test.com" {
			t.Fatalf("expected stored email copy@test.com, got %s", fresh.Email)
		}
	})

	t.Run("failed mutations roll back in-memory state", func(t *testing.T) {
		storeDir := t.TempDir()
		store, _, err := NewUserStore(filepath.Join(storeDir, "users.json"))
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}

		changeUser, err := store.Create("rollback-change", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create change user: %v", err)
		}
		deleteUser, err := store.Create("rollback-delete", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create delete user: %v", err)
		}
		resetUser, err := store.Create("rollback-reset", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create reset user: %v", err)
		}
		updateUser, err := store.Create("rollback-update", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create update user: %v", err)
		}

		store.filePath = storeDir

		updateUser.Disabled = true
		if err := store.Update(updateUser); err == nil {
			t.Fatal("expected update save failure")
		}
		freshUpdated, err := store.GetByID(updateUser.ID)
		if err != nil {
			t.Fatalf("failed to reload updated user: %v", err)
		}
		if freshUpdated.Disabled {
			t.Fatal("expected failed update to roll back disabled flag")
		}

		if err := store.ChangePassword(changeUser.ID, "password123", "newpassword456"); err == nil {
			t.Fatal("expected change password save failure")
		}
		if _, err := store.Authenticate("rollback-change", "newpassword456"); err == nil {
			t.Fatal("expected failed password change to leave new password unusable")
		}
		if _, err := store.Authenticate("rollback-change", "password123"); err != nil {
			t.Fatalf("expected old password to remain valid after rollback, got %v", err)
		}

		if err := store.ResetPassword(resetUser.ID, "resetpass456"); err == nil {
			t.Fatal("expected reset password save failure")
		}
		if _, err := store.Authenticate("rollback-reset", "resetpass456"); err == nil {
			t.Fatal("expected failed reset password to leave new password unusable")
		}
		if _, err := store.Authenticate("rollback-reset", "password123"); err != nil {
			t.Fatalf("expected original password to remain valid after failed reset, got %v", err)
		}

		if err := store.Delete(deleteUser.ID); err == nil {
			t.Fatal("expected delete save failure")
		}
		if _, err := store.GetByID(deleteUser.ID); err != nil {
			t.Fatalf("expected failed delete to keep user in store, got %v", err)
		}
	})

	t.Run("delete user", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users6.json"))
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
		store, _, _ := NewUserStore(filepath.Join(dir, "users7.json"))
		// NewUserStore creates default admin, so we get existing admin
		admin, _ := store.GetByUsername("admin")

		err := store.Delete(admin.ID)
		if err != ErrLastAdmin {
			t.Errorf("expected ErrLastAdmin, got %v", err)
		}
	})

	t.Run("persistence", func(t *testing.T) {
		usersFile := filepath.Join(dir, "users8.json")
		store1, _, _ := NewUserStore(usersFile)
		store1.Create("persistent", "password123", "p@example.com", RoleUser)

		store2, _, err := NewUserStore(usersFile)
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

	t.Run("get by username stays responsive during login persistence", func(t *testing.T) {
		storeDir := t.TempDir()
		store, _, err := NewUserStore(filepath.Join(storeDir, "users.json"))
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}

		user, err := store.Create("slowlogin", "password123", "slow@login.test", RoleUser)
		if err != nil {
			t.Fatalf("failed to create user: %v", err)
		}

		originalWriter := userStoreWriter
		writerStarted := make(chan struct{})
		writerRelease := make(chan struct{})
		var startOnce sync.Once
		var releaseOnce sync.Once
		userStoreWriter = func(path string, data []byte) error {
			startOnce.Do(func() {
				close(writerStarted)
			})
			<-writerRelease
			return originalWriter(path, data)
		}
		t.Cleanup(func() {
			userStoreWriter = originalWriter
			releaseOnce.Do(func() {
				close(writerRelease)
			})
		})

		authDone := make(chan struct {
			user *User
			err  error
		}, 1)
		go func() {
			authUser, authErr := store.Authenticate("slowlogin", "password123")
			authDone <- struct {
				user *User
				err  error
			}{user: authUser, err: authErr}
		}()

		select {
		case <-writerStarted:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for user store write to start")
		}

		lookupDone := make(chan struct{})
		go func() {
			loaded, lookupErr := store.GetByUsername("slowlogin")
			if lookupErr != nil {
				t.Errorf("GetByUsername() error during pending login persist: %v", lookupErr)
			} else if loaded.LastLoginAt != nil {
				t.Errorf("expected reads during pending login persist to observe committed LastLoginAt=nil, got %v", loaded.LastLoginAt)
			}
			close(lookupDone)
		}()

		select {
		case <-lookupDone:
		case <-time.After(time.Second):
			t.Fatal("GetByUsername() blocked on an in-flight Authenticate() save")
		}

		releaseOnce.Do(func() {
			close(writerRelease)
		})

		select {
		case result := <-authDone:
			if result.err != nil {
				t.Fatalf("Authenticate() error: %v", result.err)
			}
			if result.user == nil || result.user.ID != user.ID {
				t.Fatalf("expected Authenticate() to return user %s, got %+v", user.ID, result.user)
			}
			if result.user.LastLoginAt == nil {
				t.Fatal("expected Authenticate() result to include last_login_at after successful save")
			}
		case <-time.After(time.Second):
			t.Fatal("Authenticate() did not finish after releasing writer")
		}

		loaded, err := store.GetByUsername("slowlogin")
		if err != nil {
			t.Fatalf("failed to reload user after authenticate: %v", err)
		}
		if loaded.LastLoginAt == nil {
			t.Fatal("expected persisted last_login_at after authenticate")
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

		_, err = tm.ValidateRefreshToken(tokenPair1.RefreshToken)
		if err != ErrTokenRevoked {
			t.Errorf("refresh token: expected ErrTokenRevoked, got %v", err)
		}
	})

	t.Run("revoke by user does not revoke newly issued tokens in same second", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		user := &User{
			ID:       "user-same-second",
			Username: "same-second",
			Role:     RoleUser,
		}

		oldPair, _ := tm.GenerateTokenPair(user)
		tm.RevokeByUser(user.ID)
		newPair, _ := tm.GenerateTokenPair(user)

		_, err := tm.ValidateAccessToken(oldPair.AccessToken)
		if err != ErrTokenRevoked {
			t.Fatalf("old access token: expected ErrTokenRevoked, got %v", err)
		}

		if _, err := tm.ValidateAccessToken(newPair.AccessToken); err != nil {
			t.Fatalf("new access token should remain valid, got %v", err)
		}

		if _, err := tm.ValidateRefreshToken(newPair.RefreshToken); err != nil {
			t.Fatalf("new refresh token should remain valid, got %v", err)
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

func TestTokenManager_CleanupRevokedTokens_RemovesExpiredEntries(t *testing.T) {
	tm := NewTokenManager("cleanup-secret", time.Minute, time.Minute)

	tm.mu.Lock()
	tm.revokedTokens["expired-token"] = time.Now().Add(-time.Minute)
	tm.userRevokedAt["expired-user"] = time.Now().Add(-2 * time.Minute)
	tm.mu.Unlock()

	tm.CleanupRevokedTokens()

	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if _, ok := tm.revokedTokens["expired-token"]; ok {
		t.Fatal("expected expired token entry to be removed")
	}
	if _, ok := tm.userRevokedAt["expired-user"]; ok {
		t.Fatal("expected expired user revocation entry to be removed")
	}
}

func TestMiddleware(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "middleware_users.json")
	store, _, _ := NewUserStore(usersFile)
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
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"MISSING_AUTH_HEADER"`)) {
			t.Fatalf("expected MISSING_AUTH_HEADER payload, got %s", rec.Body.String())
		}
	})

	t.Run("require auth - disabled user", func(t *testing.T) {
		disabledUser, err := store.Create("middleware-disabled", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create disabled user: %v", err)
		}
		disabledUser.Disabled = true
		if err := store.Update(disabledUser); err != nil {
			t.Fatalf("failed to disable user: %v", err)
		}

		disabledToken, err := tm.GenerateTokenPair(disabledUser)
		if err != nil {
			t.Fatalf("failed to generate token: %v", err)
		}

		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer "+disabledToken.AccessToken)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d", rec.Code)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"USER_DISABLED"`)) {
			t.Fatalf("expected USER_DISABLED payload, got %s", rec.Body.String())
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
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"INVALID_TOKEN"`)) {
			t.Fatalf("expected INVALID_TOKEN payload, got %s", rec.Body.String())
		}
	})

	t.Run("require auth - query token rejected for download", func(t *testing.T) {
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}))

		req := httptest.NewRequest("GET", "/api/v1/download/test.txt?auth="+userToken.AccessToken, nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rec.Code)
		}
	})

	t.Run("require auth - download session cookie allowed for download", func(t *testing.T) {
		called := false
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))

		req := httptest.NewRequest("GET", "/api/v1/download/test.txt", nil)
		req.AddCookie(&http.Cookie{Name: DownloadSessionCookieName, Value: userToken.AccessToken})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("handler was not called")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("require auth - download session cookie allowed for thumbnails", func(t *testing.T) {
		called := false
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))

		req := httptest.NewRequest("GET", "/api/v1/thumbnails/test.jpg?size=medium", nil)
		req.AddCookie(&http.Cookie{Name: DownloadSessionCookieName, Value: userToken.AccessToken})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("handler was not called")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("require auth - query token rejected for non-download", func(t *testing.T) {
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}))

		req := httptest.NewRequest("GET", "/api/v1/files/test.txt?auth="+userToken.AccessToken, nil)
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
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"INSUFFICIENT_PERMISSIONS"`)) {
			t.Fatalf("expected INSUFFICIENT_PERMISSIONS payload, got %s", rec.Body.String())
		}
	})

	t.Run("require role - missing auth context uses structured unauthorized error", func(t *testing.T) {
		handler := mw.RequireRole(RoleAdmin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}))

		req := httptest.NewRequest("GET", "/admin", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", rec.Code)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"NOT_AUTHENTICATED"`)) {
			t.Fatalf("expected NOT_AUTHENTICATED payload, got %s", rec.Body.String())
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

	t.Run("path boundary matching", func(t *testing.T) {
		mwWithExclude := NewMiddlewareWithExclude(store, tm, []string{"/health", "/api/v1/version", "/public/"})

		handler := mwWithExclude.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		for _, allowedPath := range []string{"/health", "/health/ready", "/api/v1/version", "/public/files/test.txt"} {
			req := httptest.NewRequest(http.MethodGet, allowedPath, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected excluded path %s to bypass auth, got status %d", allowedPath, rec.Code)
			}
		}

		for _, blockedPath := range []string{"/healthz", "/api/v1/version-extra", "/publicity"} {
			req := httptest.NewRequest(http.MethodGet, blockedPath, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected same-prefix path %s to require auth, got status %d", blockedPath, rec.Code)
			}
		}
	})

	t.Run("download session cookie path boundaries", func(t *testing.T) {
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		for _, allowedPath := range []string{"/api/v1/download/test.txt", "/api/v1/thumbnails/test.jpg", "/api/v1/download"} {
			req := httptest.NewRequest(http.MethodGet, allowedPath, nil)
			req.AddCookie(&http.Cookie{Name: DownloadSessionCookieName, Value: userToken.AccessToken})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected download cookie path %s to be allowed, got status %d", allowedPath, rec.Code)
			}
		}

		for _, blockedPath := range []string{"/api/v1/download-anything", "/api/v1/thumbnails-extra"} {
			req := httptest.NewRequest(http.MethodGet, blockedPath, nil)
			req.AddCookie(&http.Cookie{Name: DownloadSessionCookieName, Value: userToken.AccessToken})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected same-prefix cookie path %s to require bearer auth, got status %d", blockedPath, rec.Code)
			}
		}
	})
}

func TestAuthHandler(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "handler_users.json")
	store, _, _ := NewUserStore(usersFile)
	tm := NewTokenManager("test-secret", 15*time.Minute, 24*time.Hour)

	store.Create("handleruser", "password123", "handler@test.com", RoleUser)
	store.Create("handleradmin", "adminpass123", "admin@test.com", RoleAdmin)

	h := NewHandler(store, tm)

	type authEnvelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
		Error   *ErrorDetail    `json:"error"`
	}

	t.Run("login success", func(t *testing.T) {
		body := `{"username":"handleruser","password":"password123"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		h.HandleLogin(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		var resp LoginResponse
		if err := json.Unmarshal(envelope.Data, &resp); err != nil {
			t.Fatalf("unmarshal login payload error: %v", err)
		}

		if !envelope.Success {
			t.Error("expected success true")
		}
		if resp.AccessToken == "" {
			t.Error("expected access token")
		}
		if resp.User.Username != "handleruser" {
			t.Errorf("expected username handleruser, got %s", resp.User.Username)
		}
	})

	t.Run("login trims surrounding username whitespace", func(t *testing.T) {
		body := `{"username":"  handleruser  ","password":"password123"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		h.HandleLogin(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("login rejects unknown fields", func(t *testing.T) {
		body := `{"username":"handleruser","password":"password123","unexpected":true}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		h.HandleLogin(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_REQUEST" {
			t.Fatalf("expected INVALID_REQUEST error, got %+v", envelope.Error)
		}
	})

	t.Run("login rejects oversized bodies", func(t *testing.T) {
		body := bytes.Repeat([]byte{'x'}, defaultJSONRequestBodyLimit+1)
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
		rec := httptest.NewRecorder()

		h.HandleLogin(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected status 413, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "PAYLOAD_TOO_LARGE" {
			t.Fatalf("expected PAYLOAD_TOO_LARGE error, got %+v", envelope.Error)
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

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_CREDENTIALS" {
			t.Fatalf("expected INVALID_CREDENTIALS error, got %+v", envelope.Error)
		}
	})

	t.Run("disabled user login does not reveal account state", func(t *testing.T) {
		disabledUser, err := store.Create("disabled-login", "password123", "disabled-login@test.com", RoleUser)
		if err != nil {
			t.Fatalf("create disabled user error: %v", err)
		}
		disabledUser.Disabled = true
		if err := store.Update(disabledUser); err != nil {
			t.Fatalf("disable user error: %v", err)
		}

		body := `{"username":"disabled-login","password":"password123"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		h.HandleLogin(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", rec.Code)
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_CREDENTIALS" {
			t.Fatalf("expected INVALID_CREDENTIALS error, got %+v", envelope.Error)
		}
	})

	t.Run("login rate limited after repeated failures", func(t *testing.T) {
		h.loginFailureLimit = 3
		h.loginFailureWindow = time.Minute
		h.loginLockDuration = time.Minute
		h.loginAttempts = newLoginAttemptTracker()
		now := time.Date(2026, 3, 19, 11, 0, 0, 0, time.UTC)
		h.loginAttempts.now = func() time.Time { return now }

		body := `{"username":"handleruser","password":"wrongpassword"}`
		for attempt := 1; attempt < h.loginFailureLimit; attempt++ {
			req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
			req.RemoteAddr = "203.0.113.5:1234"
			rec := httptest.NewRecorder()

			h.HandleLogin(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("attempt %d: expected status 401, got %d", attempt, rec.Code)
			}
		}

		lockedReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		lockedReq.RemoteAddr = "203.0.113.5:1234"
		lockedRec := httptest.NewRecorder()
		h.HandleLogin(lockedRec, lockedReq)

		if lockedRec.Code != http.StatusTooManyRequests {
			t.Fatalf("expected status 429, got %d", lockedRec.Code)
		}
		var envelope authEnvelope
		if err := json.Unmarshal(lockedRec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "LOGIN_RATE_LIMITED" {
			t.Fatalf("expected LOGIN_RATE_LIMITED error, got %+v", envelope.Error)
		}

		now = now.Add(h.loginLockDuration + time.Second)
		successReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(`{"username":"handleruser","password":"password123"}`))
		successReq.RemoteAddr = "203.0.113.5:1234"
		successRec := httptest.NewRecorder()
		h.HandleLogin(successRec, successReq)

		if successRec.Code != http.StatusOK {
			t.Fatalf("expected status 200 after lock expiry, got %d", successRec.Code)
		}

		postSuccessReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		postSuccessReq.RemoteAddr = "203.0.113.5:1234"
		postSuccessRec := httptest.NewRecorder()
		h.HandleLogin(postSuccessRec, postSuccessReq)

		if postSuccessRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected failures to reset after successful login, got %d", postSuccessRec.Code)
		}
	})

	t.Run("login rate limiting normalizes username casing", func(t *testing.T) {
		h.loginFailureLimit = 2
		h.loginFailureWindow = time.Minute
		h.loginLockDuration = time.Minute
		h.loginAttempts = newLoginAttemptTracker()
		now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)
		h.loginAttempts.now = func() time.Time { return now }

		firstReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(`{"username":"HANDLERUSER","password":"wrongpassword"}`))
		firstReq.RemoteAddr = "203.0.113.6:1234"
		firstRec := httptest.NewRecorder()
		h.HandleLogin(firstRec, firstReq)

		if firstRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected first mixed-case attempt to return 401, got %d", firstRec.Code)
		}

		secondReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(`{"username":"handleruser","password":"wrongpassword"}`))
		secondReq.RemoteAddr = "203.0.113.6:1234"
		secondRec := httptest.NewRecorder()
		h.HandleLogin(secondRec, secondReq)

		if secondRec.Code != http.StatusTooManyRequests {
			t.Fatalf("expected second differently-cased attempt to return 429, got %d", secondRec.Code)
		}

		var envelope authEnvelope
		if err := json.Unmarshal(secondRec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "LOGIN_RATE_LIMITED" {
			t.Fatalf("expected LOGIN_RATE_LIMITED error, got %+v", envelope.Error)
		}
	})

	t.Run("login rate limiting ignores spoofed leading forwarded for entries", func(t *testing.T) {
		h.loginFailureLimit = 2
		h.loginFailureWindow = time.Minute
		h.loginLockDuration = time.Minute
		h.loginAttempts = newLoginAttemptTracker()
		now := time.Date(2026, 3, 19, 12, 30, 0, 0, time.UTC)
		h.loginAttempts.now = func() time.Time { return now }

		firstReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(`{"username":"handleruser","password":"wrongpassword"}`))
		firstReq.RemoteAddr = "127.0.0.1:8080"
		firstReq.Header.Set("X-Forwarded-For", "203.0.113.10, 198.51.100.20")
		firstRec := httptest.NewRecorder()
		h.HandleLogin(firstRec, firstReq)

		if firstRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected first spoofed forwarded attempt to return 401, got %d", firstRec.Code)
		}

		secondReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(`{"username":"handleruser","password":"wrongpassword"}`))
		secondReq.RemoteAddr = "127.0.0.1:8080"
		secondReq.Header.Set("X-Forwarded-For", "203.0.113.11, 198.51.100.20")
		secondRec := httptest.NewRecorder()
		h.HandleLogin(secondRec, secondReq)

		if secondRec.Code != http.StatusTooManyRequests {
			t.Fatalf("expected repeated spoofed forwarded attempts to return 429, got %d", secondRec.Code)
		}

		var envelope authEnvelope
		if err := json.Unmarshal(secondRec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "LOGIN_RATE_LIMITED" {
			t.Fatalf("expected LOGIN_RATE_LIMITED error, got %+v", envelope.Error)
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

		var loginEnvelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &loginEnvelope); err != nil {
			t.Fatalf("unmarshal login envelope error: %v", err)
		}
		var loginResp LoginResponse
		if err := json.Unmarshal(loginEnvelope.Data, &loginResp); err != nil {
			t.Fatalf("unmarshal login payload error: %v", err)
		}

		refreshBody, _ := json.Marshal(RefreshRequest{RefreshToken: loginResp.RefreshToken})
		req = httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewBuffer(refreshBody))
		rec = httptest.NewRecorder()
		h.HandleRefresh(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var refreshEnvelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &refreshEnvelope); err != nil {
			t.Fatalf("unmarshal refresh envelope error: %v", err)
		}
		var refreshResp LoginResponse
		if err := json.Unmarshal(refreshEnvelope.Data, &refreshResp); err != nil {
			t.Fatalf("unmarshal refresh payload error: %v", err)
		}

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

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal me envelope error: %v", err)
		}
		if !envelope.Success {
			t.Fatal("expected me success")
		}
	})

	t.Run("download session endpoint", func(t *testing.T) {
		user, _ := store.GetByUsername("handleruser")
		pair, _ := tm.GenerateTokenPair(user)

		req := httptest.NewRequest("POST", "/api/v1/auth/download-session", nil)
		req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
		ctx := context.WithValue(req.Context(), ContextKeyUser, user)
		ctx = context.WithValue(ctx, ContextKeyClaims, &TokenClaims{
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute))},
			UserID:           user.ID,
			Username:         user.Username,
			Role:             user.Role,
		})
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		h.HandleCreateDownloadSession(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		cookies := rec.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("expected one cookie, got %d", len(cookies))
		}
		if cookies[0].Name != DownloadSessionCookieName {
			t.Fatalf("expected cookie %q, got %q", DownloadSessionCookieName, cookies[0].Name)
		}
		if cookies[0].Path != "/api/v1" {
			t.Fatalf("expected download cookie path, got %q", cookies[0].Path)
		}
	})

	t.Run("download session cookie ignores spoofed forwarded proto from untrusted source", func(t *testing.T) {
		user, _ := store.GetByUsername("handleruser")
		pair, _ := tm.GenerateTokenPair(user)

		req := httptest.NewRequest("POST", "/api/v1/auth/download-session", nil)
		req.RemoteAddr = "203.0.113.5:1234"
		req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
		req.Header.Set("X-Forwarded-Proto", "https")
		ctx := context.WithValue(req.Context(), ContextKeyUser, user)
		ctx = context.WithValue(ctx, ContextKeyClaims, &TokenClaims{
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute))},
			UserID:           user.ID,
			Username:         user.Username,
			Role:             user.Role,
		})
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		h.HandleCreateDownloadSession(rec, req)

		cookies := rec.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("expected one cookie, got %d", len(cookies))
		}
		if cookies[0].Secure {
			t.Fatal("expected spoofed forwarded proto to leave cookie insecure")
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

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal users envelope error: %v", err)
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(envelope.Data, &resp); err != nil {
			t.Fatalf("unmarshal users payload error: %v", err)
		}

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

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal create user envelope error: %v", err)
		}
		if !envelope.Success {
			t.Fatal("expected create user success")
		}
	})

	t.Run("admin create user rejects unknown fields", func(t *testing.T) {
		body := `{"username":"unknownfielduser","password":"newpass123","email":"new@test.com","role":"user","unexpected":true}`
		req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(body))
		admin, _ := store.GetByUsername("handleradmin")
		ctx := context.WithValue(req.Context(), ContextKeyUser, admin)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		h.HandleCreateUser(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal create user envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_REQUEST" {
			t.Fatalf("expected INVALID_REQUEST error, got %+v", envelope.Error)
		}
		if _, err := store.GetByUsername("unknownfielduser"); err == nil {
			t.Fatal("expected user creation to be rejected before persistence")
		}
	})

	t.Run("admin create user rejects whitespace-only username", func(t *testing.T) {
		body := `{"username":"   ","password":"newpass123","email":"new@test.com","role":"user"}`
		req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(body))
		admin, _ := store.GetByUsername("handleradmin")
		ctx := context.WithValue(req.Context(), ContextKeyUser, admin)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		h.HandleCreateUser(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal create user envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "MISSING_FIELDS" {
			t.Fatalf("expected MISSING_FIELDS error, got %+v", envelope.Error)
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

	t.Run("admin mutation internal errors are hidden", func(t *testing.T) {
		assertInternalError := func(t *testing.T, rec *httptest.ResponseRecorder, wantCode string) {
			t.Helper()

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("expected status 500, got %d: %s", rec.Code, rec.Body.String())
			}

			var envelope authEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("unmarshal envelope error: %v", err)
			}
			if envelope.Error == nil {
				t.Fatalf("expected error payload, got %s", rec.Body.String())
			}
			if envelope.Error.Code != wantCode {
				t.Fatalf("expected error code %s, got %s", wantCode, envelope.Error.Code)
			}
			if envelope.Error.Message != "internal server error" {
				t.Fatalf("expected generic internal error message, got %q", envelope.Error.Message)
			}
		}

		brokenStoreDir := t.TempDir()
		brokenStore, _, err := NewUserStore(filepath.Join(brokenStoreDir, "users.json"))
		if err != nil {
			t.Fatalf("failed to create broken store fixture: %v", err)
		}
		admin, err := brokenStore.GetByUsername("admin")
		if err != nil {
			t.Fatalf("failed to get admin user: %v", err)
		}
		changeUser, err := brokenStore.Create("managed-change", "password123", "managed-change@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create change user: %v", err)
		}
		deleteUser, err := brokenStore.Create("managed-delete", "password123", "managed-delete@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create delete user: %v", err)
		}
		resetUser, err := brokenStore.Create("managed-reset", "password123", "managed-reset@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create reset user: %v", err)
		}
		toggleUser, err := brokenStore.Create("managed-toggle", "password123", "managed-toggle@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create toggle user: %v", err)
		}
		brokenStore.filePath = brokenStoreDir
		brokenHandler := NewHandler(brokenStore, tm)

		createReq := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(`{"username":"brokencreate","password":"password123","email":"broken@test.com","role":"user"}`))
		createReq = createReq.WithContext(context.WithValue(createReq.Context(), ContextKeyUser, admin))
		createRec := httptest.NewRecorder()
		brokenHandler.HandleCreateUser(createRec, createReq)
		assertInternalError(t, createRec, "CREATE_ERROR")

		changeReq := httptest.NewRequest("POST", "/api/v1/auth/password", bytes.NewBufferString(`{"old_password":"password123","new_password":"newpassword456"}`))
		changeReq = changeReq.WithContext(context.WithValue(changeReq.Context(), ContextKeyUser, changeUser))
		changeRec := httptest.NewRecorder()
		brokenHandler.HandleChangePassword(changeRec, changeReq)
		assertInternalError(t, changeRec, "PASSWORD_ERROR")

		deleteReq := httptest.NewRequest("DELETE", "/api/v1/admin/users/"+deleteUser.ID, nil)
		deleteReq = deleteReq.WithContext(context.WithValue(deleteReq.Context(), ContextKeyUser, admin))
		deleteRec := httptest.NewRecorder()
		brokenHandler.HandleDeleteUser(deleteRec, deleteReq, deleteUser.ID)
		assertInternalError(t, deleteRec, "DELETE_ERROR")

		resetReq := httptest.NewRequest("POST", "/api/v1/admin/users/"+resetUser.ID+"/reset-password", bytes.NewBufferString(`{"new_password":"resetpass123"}`))
		resetReq = resetReq.WithContext(context.WithValue(resetReq.Context(), ContextKeyUser, admin))
		resetRec := httptest.NewRecorder()
		brokenHandler.HandleResetUserPassword(resetRec, resetReq, resetUser.ID)
		assertInternalError(t, resetRec, "RESET_ERROR")

		toggleReq := httptest.NewRequest("PUT", "/api/v1/admin/users/"+toggleUser.ID+"/status", bytes.NewBufferString(`{"disabled":true}`))
		toggleReq = toggleReq.WithContext(context.WithValue(toggleReq.Context(), ContextKeyUser, admin))
		toggleRec := httptest.NewRecorder()
		brokenHandler.HandleToggleUserStatus(toggleRec, toggleReq, toggleUser.ID)
		assertInternalError(t, toggleRec, "UPDATE_ERROR")
	})

	t.Run("toggle user status requires disabled field", func(t *testing.T) {
		toggleUser, err := store.Create("toggle-missing-field", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create toggle user: %v", err)
		}
		toggleUser.Disabled = true
		if err := store.Update(toggleUser); err != nil {
			t.Fatalf("failed to disable toggle user: %v", err)
		}

		admin, _ := store.GetByUsername("handleradmin")
		toggleReq := httptest.NewRequest("PUT", "/api/v1/admin/users/"+toggleUser.ID+"/status", bytes.NewBufferString(`{}`))
		toggleReq = toggleReq.WithContext(context.WithValue(toggleReq.Context(), ContextKeyUser, admin))
		toggleRec := httptest.NewRecorder()
		h.HandleToggleUserStatus(toggleRec, toggleReq, toggleUser.ID)

		if toggleRec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", toggleRec.Code, toggleRec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(toggleRec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal toggle envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "MISSING_DISABLED" {
			t.Fatalf("expected MISSING_DISABLED error, got %+v", envelope.Error)
		}

		refreshed, err := store.GetByID(toggleUser.ID)
		if err != nil {
			t.Fatalf("failed to reload toggle user: %v", err)
		}
		if !refreshed.Disabled {
			t.Fatal("expected missing disabled field to leave user disabled")
		}
	})
}

func TestWriteSuccess_InvalidPayloadReturnsInternalServerError(t *testing.T) {
	rec := httptest.NewRecorder()

	writeSuccess(rec, http.StatusOK, map[string]interface{}{
		"bad": make(chan int),
	}, "ok")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}
	if rec.Body.String() != "Internal Server Error\n" {
		t.Fatalf("expected internal server error body, got %q", rec.Body.String())
	}
}

func TestLoginAttemptTracker_PrunesStaleEntriesOnNewFailure(t *testing.T) {
	tracker := newLoginAttemptTracker()
	now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }
	tracker.attempts["stale"] = loginAttemptState{failures: 1, lastFailure: now.Add(-2 * time.Minute)}

	tracker.recordFailure("fresh", 5, time.Minute, time.Minute)

	if _, ok := tracker.attempts["stale"]; ok {
		t.Fatal("expected stale tracker entry to be pruned")
	}
	if _, ok := tracker.attempts["fresh"]; !ok {
		t.Fatal("expected fresh tracker entry to remain")
	}
}

func TestDefaultAdminCreation(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "new_users.json")

	store, _, err := NewUserStore(usersFile)
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
	store, _, _ := NewUserStore(usersFile)

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
