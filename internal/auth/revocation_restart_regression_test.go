package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type revocationRestartFixture struct {
	handler        *Handler
	tokenManager   *TokenManager
	tokenPair      *TokenPair
	revocationFile string
	secret         string
}

func newRevocationRestartFixture(t *testing.T) *revocationRestartFixture {
	t.Helper()

	dir := t.TempDir()
	store, _, err := NewUserStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	user, err := store.Create("revocation-restart-user", "password123", "", RoleUser)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	const secret = "revocation-restart-secret-at-least-32-bytes"
	revocationFile := filepath.Join(dir, "auth-sessions.json")
	tokenManager := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
	if err := tokenManager.EnableSessionPersistence(revocationFile); err != nil {
		t.Fatalf("EnableSessionPersistence() error: %v", err)
	}
	tokenPair, err := tokenManager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}

	return &revocationRestartFixture{
		handler:        NewHandler(store, tokenManager),
		tokenManager:   tokenManager,
		tokenPair:      tokenPair,
		revocationFile: revocationFile,
		secret:         secret,
	}
}

func (fixture *revocationRestartFixture) restartTokenManager(t *testing.T) *TokenManager {
	t.Helper()

	restarted := NewTokenManager(fixture.secret, 15*time.Minute, 24*time.Hour)
	if err := restarted.EnableSessionPersistence(fixture.revocationFile); err != nil {
		t.Fatalf("EnableSessionPersistence() after restart error: %v", err)
	}
	return restarted
}

func replacePersistTokenSessionState(t *testing.T, replacement func(*TokenManager) error) func() {
	t.Helper()

	original := persistTokenSessionState
	persistTokenSessionState = replacement
	restored := false
	restore := func() {
		if restored {
			return
		}
		persistTokenSessionState = original
		restored = true
	}
	t.Cleanup(restore)
	return restore
}

func (fixture *revocationRestartFixture) refresh(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(RefreshRequest{RefreshToken: fixture.tokenPair.RefreshToken})
	if err != nil {
		t.Fatalf("Marshal(refresh request) error: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	fixture.handler.HandleRefresh(response, request)
	return response
}

func (fixture *revocationRestartFixture) logout(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()

	claims, err := fixture.tokenManager.ValidateAccessToken(fixture.tokenPair.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken() before logout error: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request = request.WithContext(WithClaimsContext(request.Context(), claims))
	response := httptest.NewRecorder()
	fixture.handler.HandleLogout(response, request)
	return response
}

func assertTokenPairValid(t *testing.T, manager *TokenManager, pair *TokenPair) {
	t.Helper()

	if _, err := manager.ValidateAccessToken(pair.AccessToken); err != nil {
		t.Fatalf("ValidateAccessToken() error = %v, want valid token", err)
	}
	if _, err := manager.ValidateRefreshToken(pair.RefreshToken); err != nil {
		t.Fatalf("ValidateRefreshToken() error = %v, want valid token", err)
	}
}

func assertTokenPairRevoked(t *testing.T, manager *TokenManager, pair *TokenPair) {
	t.Helper()

	if _, err := manager.ValidateAccessToken(pair.AccessToken); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("ValidateAccessToken() error = %v, want %v", err, ErrTokenRevoked)
	}
	if _, err := manager.ValidateRefreshToken(pair.RefreshToken); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("ValidateRefreshToken() error = %v, want %v", err, ErrTokenRevoked)
	}
}

func TestRefreshRevocationPersistenceTransactionAcrossRestart(t *testing.T) {
	t.Run("hard failure rolls back and does not issue a replacement session", func(t *testing.T) {
		fixture := newRevocationRestartFixture(t)
		restore := replacePersistTokenSessionState(t, func(*TokenManager) error {
			return errors.New("pre-rename write failed")
		})

		response := fixture.refresh(t)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("refresh status = %d, want %d: %s", response.Code, http.StatusInternalServerError, response.Body.String())
		}
		if body := response.Body.String(); strings.Contains(body, "access_token") || strings.Contains(body, "refresh_token") {
			t.Fatalf("hard-failed refresh issued replacement token data: %s", body)
		}
		for _, cookie := range response.Result().Cookies() {
			if (cookie.Name == AccessSessionCookieName || cookie.Name == RefreshSessionCookieName) && cookie.Value != "" {
				t.Fatalf("hard-failed refresh issued non-empty %s cookie", cookie.Name)
			}
		}

		restore()
		assertTokenPairValid(t, fixture.tokenManager, fixture.tokenPair)
		assertTokenPairValid(t, fixture.restartTokenManager(t), fixture.tokenPair)
	})

	t.Run("committed warning remains revoked after restart", func(t *testing.T) {
		fixture := newRevocationRestartFixture(t)
		restore := replacePersistTokenSessionState(t, func(manager *TokenManager) error {
			if err := manager.persistSessionStateLocked(); err != nil {
				return err
			}
			return wrapAuthPersistenceWarning(errors.New("post-rename directory sync failed"))
		})

		response := fixture.refresh(t)
		if response.Code != http.StatusOK {
			t.Fatalf("refresh status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
		}
		if got := response.Header().Get("Warning"); got != authPersistenceWarningHeader {
			t.Fatalf("refresh Warning header = %q, want %q", got, authPersistenceWarningHeader)
		}

		restore()
		if _, err := fixture.tokenManager.ValidateRefreshToken(fixture.tokenPair.RefreshToken); !errors.Is(err, ErrTokenRevoked) {
			t.Fatalf("ValidateRefreshToken() after warning error = %v, want %v", err, ErrTokenRevoked)
		}
		if _, err := fixture.restartTokenManager(t).ValidateRefreshToken(fixture.tokenPair.RefreshToken); !errors.Is(err, ErrTokenRevoked) {
			t.Fatalf("ValidateRefreshToken() after warning restart error = %v, want %v", err, ErrTokenRevoked)
		}
	})
}

func TestLogoutRevocationPersistenceTransactionAcrossRestart(t *testing.T) {
	t.Run("hard failure rolls back and reports failure", func(t *testing.T) {
		fixture := newRevocationRestartFixture(t)
		restore := replacePersistTokenSessionState(t, func(*TokenManager) error {
			return errors.New("pre-rename write failed")
		})

		response := fixture.logout(t)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("logout status = %d, want %d: %s", response.Code, http.StatusInternalServerError, response.Body.String())
		}

		restore()
		assertTokenPairValid(t, fixture.tokenManager, fixture.tokenPair)
		assertTokenPairValid(t, fixture.restartTokenManager(t), fixture.tokenPair)
	})

	t.Run("committed warning remains revoked after restart", func(t *testing.T) {
		fixture := newRevocationRestartFixture(t)
		restore := replacePersistTokenSessionState(t, func(manager *TokenManager) error {
			if err := manager.persistSessionStateLocked(); err != nil {
				return err
			}
			return wrapAuthPersistenceWarning(errors.New("post-rename directory sync failed"))
		})

		response := fixture.logout(t)
		if response.Code != http.StatusOK {
			t.Fatalf("logout status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
		}
		if got := response.Header().Get("Warning"); got != authPersistenceWarningHeader {
			t.Fatalf("logout Warning header = %q, want %q", got, authPersistenceWarningHeader)
		}

		restore()
		assertTokenPairRevoked(t, fixture.tokenManager, fixture.tokenPair)
		assertTokenPairRevoked(t, fixture.restartTokenManager(t), fixture.tokenPair)
	})
}

func TestRevocationDeadlinesSurviveTTLReductionAndRestart(t *testing.T) {
	const (
		secret         = "revocation-ttl-restart-secret-32-bytes"
		originalTTL    = 24 * time.Hour
		reducedTTL     = 100 * time.Millisecond
		advancePastTTL = 5 * time.Minute
		accessTokenTTL = 15 * time.Minute
	)

	currentTime := jwtTimestamp(time.Now())
	monotonicTime := currentTime
	newManager := func() *TokenManager {
		manager := NewTokenManager(secret, accessTokenTTL, originalTTL)
		manager.now = func() time.Time { return currentTime }
		manager.monotonicNow = func() time.Time { return monotonicTime }
		return manager
	}

	revocationFile := filepath.Join(t.TempDir(), "auth-sessions.json")
	manager := newManager()
	if err := manager.EnableSessionPersistence(revocationFile); err != nil {
		t.Fatalf("EnableSessionPersistence() error: %v", err)
	}
	user := &User{
		ID:                "revocation-ttl-restart-user",
		Username:          "revocation-ttl-restart-user",
		Role:              RoleUser,
		CredentialVersion: 1,
	}

	consumedPair, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair(consumed) error: %v", err)
	}
	consumedClaims, err := manager.validateRefreshTokenClaims(consumedPair.RefreshToken)
	if err != nil {
		t.Fatalf("validateRefreshTokenClaims(consumed) error: %v", err)
	}
	logoutPair, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair(logout) error: %v", err)
	}
	logoutClaims, err := manager.ValidateAccessToken(logoutPair.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken(logout) error: %v", err)
	}

	manager.UpdateExpiries(accessTokenTTL, reducedTTL)
	if err := manager.consumeRefreshTokenClaims(consumedClaims); err != nil {
		t.Fatalf("consumeRefreshTokenClaims() error: %v", err)
	}
	if err := manager.RevokeSession(logoutClaims.SessionID, logoutClaims.SessionExpiresAt.Time); err != nil {
		t.Fatalf("RevokeSession() error: %v", err)
	}

	currentTime = currentTime.Add(advancePastTTL)
	monotonicTime = monotonicTime.Add(advancePastTTL)
	if err := manager.CleanupExpiredSessions(); err != nil {
		t.Fatalf("CleanupExpiredSessions() error: %v", err)
	}

	restarted := newManager()
	if err := restarted.EnableSessionPersistence(revocationFile); err != nil {
		t.Fatalf("EnableSessionPersistence() after TTL reduction error: %v", err)
	}
	if _, err := restarted.ValidateRefreshToken(consumedPair.RefreshToken); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("ValidateRefreshToken(consumed) after restart error = %v, want %v", err, ErrTokenRevoked)
	}
	assertTokenPairRevoked(t, restarted, logoutPair)
}
