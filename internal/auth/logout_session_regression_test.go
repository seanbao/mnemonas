package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogoutWithExpiredAccessRevokesRefreshCookieSession(t *testing.T) {
	dir := t.TempDir()
	store, _, err := NewUserStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	if _, err := store.Create("expired-access-logout", "password123", "", RoleUser); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	manager := NewTokenManager("expired-access-logout-secret-32-bytes", 25*time.Millisecond, time.Hour)
	currentTime := time.Now()
	manager.now = func() time.Time { return currentTime }
	handler := NewHandler(store, manager)
	middleware := NewMiddleware(store, manager)

	refreshCookieSeenOnLogout := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", handler.HandleLogin)
	mux.Handle("POST /api/v1/auth/logout", middleware.OptionalAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, cookieErr := r.Cookie(RefreshSessionCookieName)
		if cookieErr == nil {
			refreshCookieSeenOnLogout <- cookie.Value
		} else {
			refreshCookieSeenOnLogout <- ""
		}
		handler.HandleLogout(w, r)
	})))
	mux.HandleFunc("POST /api/v1/auth/refresh", handler.HandleRefresh)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error: %v", err)
	}
	client := &http.Client{Jar: jar}
	loginBody := bytes.NewBufferString(`{"username":"expired-access-logout","password":"password123"}`)
	loginRequest, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/login", loginBody)
	if err != nil {
		t.Fatalf("NewRequest(login) error: %v", err)
	}
	loginRequest.Header.Set("Content-Type", "application/json")
	loginRequest.Header.Set(sessionModeHeader, sessionModeCookie)
	loginResponse, err := client.Do(loginRequest)
	if err != nil {
		t.Fatalf("login request error: %v", err)
	}
	defer loginResponse.Body.Close()
	if loginResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loginResponse.Body)
		t.Fatalf("login status = %d, want %d: %s", loginResponse.StatusCode, http.StatusOK, body)
	}

	refreshURL := server.URL + "/api/v1/auth/refresh"
	refreshToken := cookieValueForURL(t, jar, refreshURL, RefreshSessionCookieName)
	accessToken := responseCookieValue(t, loginResponse.Cookies(), AccessSessionCookieName)
	currentTime = currentTime.Add(tokenValidationLeeway + time.Second)
	if _, validateErr := manager.ValidateAccessToken(accessToken); !errors.Is(validateErr, ErrTokenExpired) {
		t.Fatalf("ValidateAccessToken() error = %v, want %v after advancing time", validateErr, ErrTokenExpired)
	}

	logoutRequest, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/logout", nil)
	if err != nil {
		t.Fatalf("NewRequest(logout) error: %v", err)
	}
	logoutResponse, err := client.Do(logoutRequest)
	if err != nil {
		t.Fatalf("logout request error: %v", err)
	}
	logoutBody, readErr := io.ReadAll(logoutResponse.Body)
	logoutResponse.Body.Close()
	if readErr != nil {
		t.Fatalf("ReadAll(logout response) error: %v", readErr)
	}
	if logoutResponse.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d, want %d: %s", logoutResponse.StatusCode, http.StatusOK, logoutBody)
	}
	if seen := <-refreshCookieSeenOnLogout; seen != refreshToken {
		t.Fatalf("refresh cookie on logout = %q, want issued refresh token", seen)
	}

	refreshBody, err := json.Marshal(RefreshRequest{RefreshToken: refreshToken})
	if err != nil {
		t.Fatalf("Marshal(refresh request) error: %v", err)
	}
	refreshRequest, err := http.NewRequest(http.MethodPost, refreshURL, bytes.NewReader(refreshBody))
	if err != nil {
		t.Fatalf("NewRequest(refresh) error: %v", err)
	}
	refreshRequest.Header.Set("Content-Type", "application/json")
	refreshResponse, err := client.Do(refreshRequest)
	if err != nil {
		t.Fatalf("refresh request error: %v", err)
	}
	defer refreshResponse.Body.Close()
	if refreshResponse.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(refreshResponse.Body)
		t.Fatalf("refresh status = %d, want %d: %s", refreshResponse.StatusCode, http.StatusUnauthorized, body)
	}
	assertResponseErrorCode(t, refreshResponse.Body, "TOKEN_REVOKED")
}

func TestRevocationExpiryUsesIssuedSessionDeadlineAfterTTLReduction(t *testing.T) {
	const longRefreshTTL = 24 * time.Hour
	manager := NewTokenManager("revocation-deadline-secret-32-bytes", 15*time.Minute, longRefreshTTL)
	user := &User{
		ID:                "revocation-deadline-user",
		Username:          "revocation-deadline-user",
		Role:              RoleUser,
		CredentialVersion: 1,
	}

	refreshPair, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair(refresh) error: %v", err)
	}
	refreshClaims, err := manager.validateRefreshTokenClaims(refreshPair.RefreshToken)
	if err != nil {
		t.Fatalf("validateRefreshTokenClaims(refresh) error: %v", err)
	}
	sessionPair, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair(session) error: %v", err)
	}
	sessionClaims, err := manager.ValidateAccessToken(sessionPair.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken(session) error: %v", err)
	}

	manager.UpdateExpiries(15*time.Minute, 100*time.Millisecond)
	if err := manager.consumeRefreshTokenClaims(refreshClaims); err != nil {
		t.Fatalf("consumeRefreshTokenClaims() error: %v", err)
	}
	if err := manager.RevokeSession(sessionClaims.SessionID, sessionClaims.SessionExpiresAt.Time); err != nil {
		t.Fatalf("RevokeSession() error: %v", err)
	}

	manager.mu.Lock()
	refreshState := manager.sessionRegistry[refreshClaims.SessionID]
	cleanupAt := time.Now().Add(time.Hour)
	manager.cleanupExpiredSessionsLocked(cleanupAt)
	_, refreshStillTracked := manager.sessionRegistry[refreshClaims.SessionID]
	_, sessionStillTracked := manager.sessionRegistry[sessionClaims.SessionID]
	manager.mu.Unlock()

	if !refreshState.ExpiresAt.Equal(refreshClaims.ExpiresAt.Time) {
		t.Fatalf("refresh session expiry = %s, want issued expiry %s", refreshState.ExpiresAt, refreshClaims.ExpiresAt.Time)
	}
	if !refreshStillTracked {
		t.Fatalf("refresh session state was removed at %s before issued expiry %s", cleanupAt, refreshClaims.ExpiresAt.Time)
	}
	if sessionStillTracked {
		t.Fatalf("revoked session remained in the authoritative registry until %s", sessionClaims.SessionExpiresAt.Time)
	}
	if _, err := manager.ValidateRefreshToken(refreshPair.RefreshToken); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("ValidateRefreshToken(consumed) error = %v, want %v", err, ErrTokenRevoked)
	}
	if _, err := manager.ValidateRefreshToken(sessionPair.RefreshToken); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("ValidateRefreshToken(session) error = %v, want %v", err, ErrTokenRevoked)
	}
}

func TestLogoutHardPersistenceFailureKeepsCookiesForRetry(t *testing.T) {
	dir := t.TempDir()
	store, _, err := NewUserStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	user, err := store.Create("logout-persistence-retry", "password123", "", RoleUser)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	manager := NewTokenManager("logout-persistence-retry-secret-32-bytes", 15*time.Minute, time.Hour)
	if err := manager.EnableSessionPersistence(filepath.Join(dir, "auth-sessions.json")); err != nil {
		t.Fatalf("EnableSessionPersistence() error: %v", err)
	}
	pair, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}
	handler := NewHandler(store, manager)
	logout := NewMiddleware(store, manager).OptionalAuth(http.HandlerFunc(handler.HandleLogout))

	restore := replacePersistTokenSessionState(t, func(*TokenManager) error {
		return errors.New("pre-rename write failed")
	})
	failedResponse := httptest.NewRecorder()
	logout.ServeHTTP(failedResponse, newCookieLogoutRequest(pair))
	if failedResponse.Code != http.StatusInternalServerError {
		t.Fatalf("hard-failed logout status = %d, want %d: %s", failedResponse.Code, http.StatusInternalServerError, failedResponse.Body.String())
	}
	assertCookiesNotCleared(t, failedResponse.Result().Cookies())
	assertTokenPairValid(t, manager, pair)

	restore()
	retryResponse := httptest.NewRecorder()
	logout.ServeHTTP(retryResponse, newCookieLogoutRequest(pair))
	if retryResponse.Code != http.StatusOK {
		t.Fatalf("retried logout status = %d, want %d: %s", retryResponse.Code, http.StatusOK, retryResponse.Body.String())
	}
	assertSessionCookiesCleared(t, retryResponse.Result().Cookies())
	assertTokenPairRevoked(t, manager, pair)
}

func TestTokenRevocationPersistenceRejectsUntrustedSchema(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "corrupt JSON", data: "{"},
		{name: "missing schema version", data: `{"revoked_tokens":{},"revoked_sessions":{},"user_revoked_at":{}}`},
		{name: "unknown schema version", data: `{"schema_version":999,"revoked_tokens":{},"revoked_sessions":{},"user_revoked_at":{}}`},
		{name: "obsolete schema version", data: `{"schema_version":1,"revoked_tokens":{},"revoked_sessions":{},"user_revoked_at":{}}`},
		{name: "missing time high water", data: `{"schema_version":2,"revoked_tokens":{},"revoked_sessions":{},"refresh_sessions":{},"user_session_generations":{}}`},
		{name: "missing refresh sessions", data: `{"schema_version":2,"time_high_water":"2026-07-13T00:00:00Z","revoked_tokens":{},"revoked_sessions":{},"user_session_generations":{}}`},
		{name: "invalid refresh session", data: `{"schema_version":2,"time_high_water":"2026-07-13T00:00:00Z","revoked_tokens":{},"revoked_sessions":{},"refresh_sessions":{"session":{"user_id":"","next_generation":1,"expires_at":"2026-07-14T00:00:00Z","last_rotated_at":"2026-07-13T00:00:00Z"}},"user_session_generations":{}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth-sessions.json")
			if err := os.WriteFile(path, []byte(tt.data), 0o600); err != nil {
				t.Fatalf("WriteFile() error: %v", err)
			}
			manager := NewTokenManager("revocation-schema-secret-32-bytes", 15*time.Minute, time.Hour)
			if err := manager.EnableSessionPersistence(path); err == nil {
				t.Fatal("EnableSessionPersistence() error = nil, want fail-closed schema rejection")
			}
		})
	}
}

func cookieValueForURL(t *testing.T, jar http.CookieJar, rawURL, name string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("NewRequest(cookie lookup) error: %v", err)
	}
	for _, cookie := range jar.Cookies(request.URL) {
		if cookie.Name == name {
			if strings.TrimSpace(cookie.Value) == "" {
				t.Fatalf("cookie %s is empty", name)
			}
			return cookie.Value
		}
	}
	t.Fatalf("cookie %s not found for %s", name, rawURL)
	return ""
}

func responseCookieValue(t *testing.T, cookies []*http.Cookie, name string) string {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			if strings.TrimSpace(cookie.Value) == "" {
				t.Fatalf("cookie %s is empty", name)
			}
			return cookie.Value
		}
	}
	t.Fatalf("cookie %s not found in response", name)
	return ""
}

func assertResponseErrorCode(t *testing.T, body io.Reader, want string) {
	t.Helper()
	var envelope ResponseEnvelope
	if err := json.NewDecoder(body).Decode(&envelope); err != nil {
		t.Fatalf("Decode(response) error: %v", err)
	}
	if envelope.Error == nil || envelope.Error.Code != want {
		t.Fatalf("response error = %+v, want code %q", envelope.Error, want)
	}
}

func newCookieLogoutRequest(pair *TokenPair) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: AccessSessionCookieName, Value: pair.AccessToken})
	request.AddCookie(&http.Cookie{Name: RefreshSessionCookieName, Value: pair.RefreshToken})
	return request
}

func assertCookiesNotCleared(t *testing.T, cookies []*http.Cookie) {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name != AccessSessionCookieName && cookie.Name != RefreshSessionCookieName && cookie.Name != DownloadSessionCookieName {
			continue
		}
		if cookie.MaxAge < 0 || cookie.Value == "" {
			t.Fatalf("hard-failed logout cleared session cookie %s", cookie.Name)
		}
	}
}

func assertSessionCookiesCleared(t *testing.T, cookies []*http.Cookie) {
	t.Helper()
	cleared := make(map[string]bool)
	for _, cookie := range cookies {
		if cookie.MaxAge < 0 && cookie.Value == "" {
			cleared[cookie.Name] = true
		}
	}
	for _, name := range []string{AccessSessionCookieName, RefreshSessionCookieName, DownloadSessionCookieName} {
		if !cleared[name] {
			t.Fatalf("cookie %s was not cleared after successful logout: %+v", name, cookies)
		}
	}
}
