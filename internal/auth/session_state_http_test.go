package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func decodeAuthHTTPErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope ResponseEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode auth response: %v", err)
	}
	if envelope.Error == nil {
		t.Fatalf("response omitted structured error: %s", response.Body.String())
	}
	return envelope.Error.Code
}

func newSessionStateHTTPUser(t *testing.T, username string) (*UserStore, *User) {
	t.Helper()
	store, _, err := NewUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	user, err := store.Create(username, "password123", "", RoleUser)
	if err != nil {
		t.Fatalf("Create(%q) error: %v", username, err)
	}
	return store, user
}

func TestAuthHTTPMapsActiveSessionCapacityWithoutPublishingCookies(t *testing.T) {
	store, user := newSessionStateHTTPUser(t, "capacity-user")
	manager := NewTokenManager(sessionRegistryTestSecret, 15*time.Minute, 24*time.Hour)
	manager.refreshSessionLimit = 1
	manager.refreshUserSessionLimit = 1
	if _, err := manager.GenerateTokenPair(user); err != nil {
		t.Fatalf("GenerateTokenPair(first) error: %v", err)
	}

	body, err := json.Marshal(LoginRequest{Username: user.Username, Password: "password123"})
	if err != nil {
		t.Fatalf("marshal login request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	response := httptest.NewRecorder()
	NewHandler(store, manager).HandleLogin(response, request)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("login status = %d, want %d: %s", response.Code, http.StatusTooManyRequests, response.Body.String())
	}
	if got := decodeAuthHTTPErrorCode(t, response); got != "REFRESH_SESSION_LIMIT" {
		t.Fatalf("error code = %q, want REFRESH_SESSION_LIMIT", got)
	}
	if cookies := response.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("capacity-limited login published cookies: %+v", cookies)
	}
}

func TestAuthHTTPPublishesCommittedSessionWithPersistenceWarning(t *testing.T) {
	store, user := newSessionStateHTTPUser(t, "warning-session-user")
	current := jwtTimestamp(time.Now().UTC())
	monotonic := current
	statePath := filepath.Join(t.TempDir(), "auth-sessions.json")
	manager := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
	if err := manager.EnableSessionPersistence(statePath); err != nil {
		t.Fatalf("EnableSessionPersistence() error: %v", err)
	}

	restore := replacePersistTokenSessionStateForRegistryTest(t, func(manager *TokenManager) error {
		if err := manager.persistSessionStateLocked(); err != nil {
			return err
		}
		return wrapAuthPersistenceWarning(errors.New("directory sync result is uncertain"))
	})
	body, err := json.Marshal(LoginRequest{Username: user.Username, Password: "password123"})
	if err != nil {
		t.Fatalf("marshal login request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	response := httptest.NewRecorder()
	NewHandler(store, manager).HandleLogin(response, request)
	restore()

	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if got := response.Header().Get("Warning"); got != authPersistenceWarningHeader {
		t.Fatalf("Warning = %q, want %q", got, authPersistenceWarningHeader)
	}
	if len(response.Result().Cookies()) != 2 {
		t.Fatalf("warning login cookies = %+v, want access and refresh cookies", response.Result().Cookies())
	}
	var envelope struct {
		Data    LoginResponse `json:"data"`
		Message string        `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode warning login response: %v", err)
	}
	if envelope.Message != "login succeeded with persistence warning" {
		t.Fatalf("message = %q, want persistence warning", envelope.Message)
	}
	if envelope.Data.AccessToken == "" || envelope.Data.RefreshToken == "" {
		t.Fatalf("warning login omitted bearer tokens: %+v", envelope.Data)
	}

	restarted := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
	if err := restarted.EnableSessionPersistence(statePath); err != nil {
		t.Fatalf("EnableSessionPersistence(restart) error: %v", err)
	}
	if _, err := restarted.ValidateAccessToken(envelope.Data.AccessToken); err != nil {
		t.Fatalf("ValidateAccessToken(committed warning token) error: %v", err)
	}
}

func TestAuthHTTPReturnsServiceUnavailableWhenTimeLeaseIsExhausted(t *testing.T) {
	store, user := newSessionStateHTTPUser(t, "lease-http-user")
	current := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	monotonic := current
	manager := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
	if err := manager.EnableSessionPersistence(filepath.Join(t.TempDir(), "auth-sessions.json")); err != nil {
		t.Fatalf("EnableSessionPersistence() error: %v", err)
	}
	pair, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}
	manager.mu.Lock()
	manager.restartTimeFloor = current
	manager.mu.Unlock()
	replacePersistTokenSessionStateForRegistryTest(t, func(*TokenManager) error {
		return errors.New("time lease persistence unavailable")
	})

	assertUnavailable := func(t *testing.T, response *httptest.ResponseRecorder) {
		t.Helper()
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
		}
		if got := decodeAuthHTTPErrorCode(t, response); got != "TOKEN_STATE_UNAVAILABLE" {
			t.Fatalf("error code = %q, want TOKEN_STATE_UNAVAILABLE", got)
		}
		if cookies := response.Result().Cookies(); len(cookies) != 0 {
			t.Fatalf("state-unavailable response mutated cookies: %+v", cookies)
		}
	}

	middleware := NewMiddleware(store, manager)
	handler := NewHandler(store, manager)
	downstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("state-unavailable request reached downstream handler")
		w.WriteHeader(http.StatusNoContent)
	})

	t.Run("required access authentication", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		request.Header.Set("Authorization", "Bearer "+pair.AccessToken)
		response := httptest.NewRecorder()
		middleware.RequireAuth(downstream).ServeHTTP(response, request)
		assertUnavailable(t, response)
	})

	t.Run("optional access authentication", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
		request.Header.Set("Authorization", "Bearer "+pair.AccessToken)
		response := httptest.NewRecorder()
		middleware.OptionalAuth(downstream).ServeHTTP(response, request)
		assertUnavailable(t, response)
	})

	t.Run("refresh cookie", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "https://nas.example.test/api/v1/auth/refresh", nil)
		request.AddCookie(&http.Cookie{Name: HTTPSRefreshSessionCookieName, Value: pair.RefreshToken})
		response := httptest.NewRecorder()
		handler.HandleRefresh(response, request)
		assertUnavailable(t, response)
	})

	t.Run("logout refresh cookie", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "https://nas.example.test/api/v1/auth/logout", nil)
		request.AddCookie(&http.Cookie{Name: HTTPSRefreshSessionCookieName, Value: pair.RefreshToken})
		response := httptest.NewRecorder()
		handler.HandleLogout(response, request)
		assertUnavailable(t, response)
	})
}

func TestAuthHTTPDelayedConcurrentRefreshIsHandledAsReplay(t *testing.T) {
	store, user := newSessionStateHTTPUser(t, "delayed-refresh-user")
	manager := NewTokenManager(sessionRegistryTestSecret, 15*time.Minute, 24*time.Hour)
	handler := NewHandler(store, manager)
	pair, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}
	firstClaims, err := manager.validateRefreshTokenClaims(pair.RefreshToken)
	if err != nil {
		t.Fatalf("validate first refresh request: %v", err)
	}
	delayedClaims, err := manager.validateRefreshTokenClaims(pair.RefreshToken)
	if err != nil {
		t.Fatalf("validate delayed refresh request: %v", err)
	}
	childPair, err := manager.generateTokenPairForSession(user, firstClaims.SessionID, firstClaims.ExpiresAt.Time, firstClaims.Generation+1)
	if err != nil {
		t.Fatalf("generate first child pair: %v", err)
	}
	if err := manager.consumeRefreshTokenClaims(firstClaims); err != nil {
		t.Fatalf("consume first refresh token: %v", err)
	}
	if _, err := manager.ValidateAccessToken(childPair.AccessToken); err != nil {
		t.Fatalf("first child access token was not active before replay: %v", err)
	}

	_, delayedErr := manager.generateTokenPairForSession(user, delayedClaims.SessionID, delayedClaims.ExpiresAt.Time, delayedClaims.Generation+1)
	if !errors.Is(delayedErr, ErrInvalidToken) {
		t.Fatalf("delayed generation error = %v, want %v", delayedErr, ErrInvalidToken)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	response := httptest.NewRecorder()
	handler.handleRefreshPairGenerationError(response, request, []string{pair.RefreshToken}, false, delayedErr)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("delayed replay status = %d, want %d: %s", response.Code, http.StatusUnauthorized, response.Body.String())
	}
	if got := decodeAuthHTTPErrorCode(t, response); got != "TOKEN_REVOKED" {
		t.Fatalf("delayed replay error code = %q, want TOKEN_REVOKED", got)
	}
	if _, err := manager.ValidateAccessToken(childPair.AccessToken); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("child access validation after delayed replay = %v, want %v", err, ErrTokenRevoked)
	}
}

func TestTokenValidationRequiresHS256AndExpectedIssuer(t *testing.T) {
	_, user := newSessionStateHTTPUser(t, "jwt-contract-user")
	manager := NewTokenManager(sessionRegistryTestSecret, 15*time.Minute, 24*time.Hour)
	pair, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}
	accessClaims, err := manager.ValidateAccessToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken(original) error: %v", err)
	}
	refreshClaims, err := manager.parseRefreshTokenClaims(pair.RefreshToken)
	if err != nil {
		t.Fatalf("parseRefreshTokenClaims(original) error: %v", err)
	}

	sign := func(t *testing.T, method jwt.SigningMethod, claims jwt.Claims) string {
		t.Helper()
		token, err := jwt.NewWithClaims(method, claims).SignedString(manager.secretKey)
		if err != nil {
			t.Fatalf("sign test token: %v", err)
		}
		return token
	}
	wrongIssuerAccess := *accessClaims
	wrongIssuerAccess.Issuer = "other-service"
	missingIssuerAccess := *accessClaims
	missingIssuerAccess.Issuer = ""
	wrongIssuerRefresh := *refreshClaims
	wrongIssuerRefresh.Issuer = "other-service"
	missingIssuerRefresh := *refreshClaims
	missingIssuerRefresh.Issuer = ""

	tests := []struct {
		name     string
		token    string
		validate func(string) error
	}{
		{
			name:  "access rejects HS384",
			token: sign(t, jwt.SigningMethodHS384, accessClaims),
			validate: func(token string) error {
				_, err := manager.ValidateAccessToken(token)
				return err
			},
		},
		{
			name:  "access rejects wrong issuer",
			token: sign(t, jwt.SigningMethodHS256, &wrongIssuerAccess),
			validate: func(token string) error {
				_, err := manager.ValidateAccessToken(token)
				return err
			},
		},
		{
			name:  "access rejects missing issuer",
			token: sign(t, jwt.SigningMethodHS256, &missingIssuerAccess),
			validate: func(token string) error {
				_, err := manager.ValidateAccessToken(token)
				return err
			},
		},
		{
			name:  "refresh rejects HS384",
			token: sign(t, jwt.SigningMethodHS384, refreshClaims),
			validate: func(token string) error {
				_, err := manager.ValidateRefreshToken(token)
				return err
			},
		},
		{
			name:  "refresh rejects wrong issuer",
			token: sign(t, jwt.SigningMethodHS256, &wrongIssuerRefresh),
			validate: func(token string) error {
				_, err := manager.ValidateRefreshToken(token)
				return err
			},
		},
		{
			name:  "refresh rejects missing issuer",
			token: sign(t, jwt.SigningMethodHS256, &missingIssuerRefresh),
			validate: func(token string) error {
				_, err := manager.ValidateRefreshToken(token)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.validate(test.token); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("validation error = %v, want %v", err, ErrInvalidToken)
			}
		})
	}
}
