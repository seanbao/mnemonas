package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func assertAccessRejectedByMiddleware(t *testing.T, store *UserStore, manager *TokenManager, accessToken string, wantStatus int) {
	t.Helper()

	called := false
	handler := NewMiddleware(store, manager).RequireAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/files", nil)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if called {
		t.Fatal("authenticated handler ran with invalidated account state")
	}
	if response.Code != wantStatus {
		t.Fatalf("middleware status = %d, want %d: %s", response.Code, wantStatus, response.Body.String())
	}
}

func assertRefreshRejectedByHandler(t *testing.T, handler *Handler, refreshToken string, wantStatus int) {
	t.Helper()

	body, err := json.Marshal(RefreshRequest{RefreshToken: refreshToken})
	if err != nil {
		t.Fatalf("Marshal(refresh request) error: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.HandleRefresh(response, request)
	if response.Code != wantStatus {
		t.Fatalf("refresh status = %d, want %d: %s", response.Code, wantStatus, response.Body.String())
	}
}

func TestTokenManagerSessionFamilyRevocationPersists(t *testing.T) {
	const secret = "session-family-persistence-secret-32-bytes"
	revocationPath := filepath.Join(t.TempDir(), "auth-sessions.json")
	manager := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
	if err := manager.EnableSessionPersistence(revocationPath); err != nil {
		t.Fatalf("EnableSessionPersistence() error: %v", err)
	}

	user := &User{
		ID:                "session-family-user",
		Username:          "session-family-user",
		Role:              RoleUser,
		CredentialVersion: 1,
	}
	first, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair(first) error: %v", err)
	}
	firstClaims, err := manager.ValidateAccessToken(first.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken(first) error: %v", err)
	}
	rotated, err := manager.generateTokenPairForSession(user, firstClaims.SessionID, firstClaims.SessionExpiresAt.Time)
	if err != nil {
		t.Fatalf("generateTokenPairForSession() error: %v", err)
	}
	rotatedClaims, err := manager.ValidateAccessToken(rotated.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken(rotated) error: %v", err)
	}
	if rotatedClaims.SessionID != firstClaims.SessionID {
		t.Fatalf("rotated session ID = %q, want %q", rotatedClaims.SessionID, firstClaims.SessionID)
	}

	independent, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair(independent) error: %v", err)
	}
	independentClaims, err := manager.ValidateAccessToken(independent.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken(independent) error: %v", err)
	}
	if independentClaims.SessionID == firstClaims.SessionID {
		t.Fatal("independent login reused the existing session ID")
	}

	if err := manager.RevokeSession(firstClaims.SessionID, firstClaims.SessionExpiresAt.Time); err != nil {
		t.Fatalf("RevokeSession() error: %v", err)
	}
	assertTokenPairRevoked(t, manager, first)
	assertTokenPairRevoked(t, manager, rotated)
	assertTokenPairValid(t, manager, independent)

	restarted := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
	if err := restarted.EnableSessionPersistence(revocationPath); err != nil {
		t.Fatalf("EnableSessionPersistence() after restart error: %v", err)
	}
	assertTokenPairRevoked(t, restarted, first)
	assertTokenPairRevoked(t, restarted, rotated)
	assertTokenPairValid(t, restarted, independent)
}

func TestRefreshCannotPublishChildAfterConcurrentLogout(t *testing.T) {
	dir := t.TempDir()
	store, _, err := NewUserStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	user, err := store.Create("session-race-user", "password123", "", RoleUser)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	manager := NewTokenManager("session-race-secret-at-least-32-bytes", 15*time.Minute, 24*time.Hour)
	pair, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}
	claims, err := manager.ValidateAccessToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken() error: %v", err)
	}
	handler := NewHandler(store, manager)

	refreshBody, err := json.Marshal(RefreshRequest{RefreshToken: pair.RefreshToken})
	if err != nil {
		t.Fatalf("Marshal(refresh request) error: %v", err)
	}
	refreshRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(refreshBody))
	refreshResponse := httptest.NewRecorder()

	enteredGeneration := make(chan struct{})
	releaseGeneration := make(chan struct{})
	originalRandomRead := tokenRandomRead
	var enteredOnce sync.Once
	tokenRandomRead = func(buffer []byte) (int, error) {
		enteredOnce.Do(func() { close(enteredGeneration) })
		<-releaseGeneration
		return originalRandomRead(buffer)
	}
	t.Cleanup(func() { tokenRandomRead = originalRandomRead })

	refreshDone := make(chan struct{})
	go func() {
		handler.HandleRefresh(refreshResponse, refreshRequest)
		close(refreshDone)
	}()

	select {
	case <-enteredGeneration:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for refresh token generation")
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutRequest = logoutRequest.WithContext(context.WithValue(logoutRequest.Context(), ContextKeyClaims, claims))
	logoutResponse := httptest.NewRecorder()
	handler.HandleLogout(logoutResponse, logoutRequest)
	if logoutResponse.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want %d: %s", logoutResponse.Code, http.StatusOK, logoutResponse.Body.String())
	}

	close(releaseGeneration)
	select {
	case <-refreshDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for refresh response")
	}
	if refreshResponse.Code != http.StatusUnauthorized {
		t.Fatalf("refresh status = %d, want %d: %s", refreshResponse.Code, http.StatusUnauthorized, refreshResponse.Body.String())
	}
	if !bytes.Contains(refreshResponse.Body.Bytes(), []byte(`"code":"TOKEN_REVOKED"`)) {
		t.Fatalf("refresh response = %s, want TOKEN_REVOKED", refreshResponse.Body.String())
	}
	if bytes.Contains(refreshResponse.Body.Bytes(), []byte("access_token")) || bytes.Contains(refreshResponse.Body.Bytes(), []byte("refresh_token")) {
		t.Fatalf("revoked refresh published replacement token data: %s", refreshResponse.Body.String())
	}
	for _, cookie := range refreshResponse.Result().Cookies() {
		if (cookie.Name == AccessSessionCookieName || cookie.Name == RefreshSessionCookieName) && cookie.Value != "" {
			t.Fatalf("revoked refresh published non-empty %s cookie", cookie.Name)
		}
	}
	if _, err := manager.ValidateAccessToken(pair.AccessToken); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("ValidateAccessToken() after logout error = %v, want %v", err, ErrTokenRevoked)
	}
}

func TestLogoutWithConsumedRefreshTokenRevokesRotatedChild(t *testing.T) {
	dir := t.TempDir()
	store, _, err := NewUserStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	user, err := store.Create("consumed-logout-user", "password123", "", RoleUser)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	manager := NewTokenManager("consumed-logout-secret-at-least-32-bytes", 15*time.Minute, 24*time.Hour)
	handler := NewHandler(store, manager)
	initial, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}

	refreshBody, err := json.Marshal(RefreshRequest{RefreshToken: initial.RefreshToken})
	if err != nil {
		t.Fatalf("Marshal(refresh request) error: %v", err)
	}
	refreshRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(refreshBody))
	refreshResponse := httptest.NewRecorder()
	handler.HandleRefresh(refreshResponse, refreshRequest)
	if refreshResponse.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200: %s", refreshResponse.Code, refreshResponse.Body.String())
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(refreshResponse.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("Unmarshal(refresh envelope) error: %v", err)
	}
	var rotated LoginResponse
	if err := json.Unmarshal(envelope.Data, &rotated); err != nil {
		t.Fatalf("Unmarshal(refresh data) error: %v", err)
	}
	if rotated.AccessToken == "" || rotated.RefreshToken == "" {
		t.Fatalf("refresh response omitted rotated tokens: %s", refreshResponse.Body.String())
	}

	logoutBody, err := json.Marshal(RefreshRequest{RefreshToken: initial.RefreshToken})
	if err != nil {
		t.Fatalf("Marshal(logout request) error: %v", err)
	}
	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", bytes.NewReader(logoutBody))
	logoutResponse := httptest.NewRecorder()
	handler.HandleLogout(logoutResponse, logoutRequest)
	if logoutResponse.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200: %s", logoutResponse.Code, logoutResponse.Body.String())
	}
	if _, err := manager.ValidateAccessToken(rotated.AccessToken); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("ValidateAccessToken(rotated) error = %v, want %v", err, ErrTokenRevoked)
	}
	if _, err := manager.ValidateRefreshToken(rotated.RefreshToken); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("ValidateRefreshToken(rotated) error = %v, want %v", err, ErrTokenRevoked)
	}
}
