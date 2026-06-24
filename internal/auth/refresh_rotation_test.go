package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testRefreshUser(id string) *User {
	return &User{
		ID:                id,
		Username:          id,
		Role:              RoleUser,
		CredentialVersion: 1,
	}
}

func TestRefreshRotationUsesBoundedPersistedSessionState(t *testing.T) {
	const rotations = 32
	dir := t.TempDir()
	path := filepath.Join(dir, "auth-sessions.json")
	manager := NewTokenManager("bounded-refresh-state-secret-32-bytes", 15*time.Minute, 24*time.Hour)
	manager.refreshRotationInterval = 0
	if err := manager.EnableSessionPersistence(path); err != nil {
		t.Fatalf("EnableSessionPersistence() error: %v", err)
	}
	user := testRefreshUser("bounded-refresh-user")
	pair, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}

	var firstSize int64
	for generation := uint64(0); generation < rotations; generation++ {
		claims, err := manager.validateRefreshTokenClaims(pair.RefreshToken)
		if err != nil {
			t.Fatalf("validate generation %d: %v", generation, err)
		}
		if claims.Generation != generation {
			t.Fatalf("refresh generation = %d, want %d", claims.Generation, generation)
		}
		child, err := manager.generateTokenPairForSession(user, claims.SessionID, claims.ExpiresAt.Time, claims.Generation+1)
		if err != nil {
			t.Fatalf("generate child generation %d: %v", generation+1, err)
		}
		if err := manager.consumeRefreshTokenClaims(claims); err != nil {
			t.Fatalf("consume generation %d: %v", generation, err)
		}
		pair = child

		manager.mu.RLock()
		trackedSessions := len(manager.sessionRegistry)
		manager.mu.RUnlock()
		if trackedSessions != 1 {
			t.Fatalf("tracked refresh sessions = %d, want 1", trackedSessions)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(revocation state) error: %v", err)
		}
		if generation == 0 {
			firstSize = info.Size()
		} else if info.Size() > firstSize+128 {
			t.Fatalf("revocation state grew from %d to %d bytes across bounded rotation", firstSize, info.Size())
		}
	}

	restarted := NewTokenManager("bounded-refresh-state-secret-32-bytes", 15*time.Minute, 24*time.Hour)
	if err := restarted.EnableSessionPersistence(path); err != nil {
		t.Fatalf("EnableSessionPersistence() after restart error: %v", err)
	}
	if _, err := restarted.ValidateRefreshToken(pair.RefreshToken); err != nil {
		t.Fatalf("latest refresh token after restart error: %v", err)
	}
}

func TestRefreshRotationRateAndSessionLimits(t *testing.T) {
	t.Run("same session is rate limited", func(t *testing.T) {
		manager := NewTokenManager("refresh-rate-limit-secret-32-bytes", 15*time.Minute, 24*time.Hour)
		base := time.Now()
		manager.now = func() time.Time { return base }
		user := testRefreshUser("refresh-rate-user")
		pair, err := manager.GenerateTokenPair(user)
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}
		claims, err := manager.validateRefreshTokenClaims(pair.RefreshToken)
		if err != nil {
			t.Fatalf("validate initial refresh: %v", err)
		}
		child, err := manager.generateTokenPairForSession(user, claims.SessionID, claims.ExpiresAt.Time, 1)
		if err != nil {
			t.Fatalf("generateTokenPairForSession() error: %v", err)
		}
		if err := manager.consumeRefreshTokenClaims(claims); err != nil {
			t.Fatalf("consume initial refresh: %v", err)
		}
		childClaims, err := manager.validateRefreshTokenClaims(child.RefreshToken)
		if err != nil {
			t.Fatalf("validate child refresh: %v", err)
		}
		if err := manager.consumeRefreshTokenClaims(childClaims); !errors.Is(err, ErrRefreshRateLimited) {
			t.Fatalf("immediate child consume error = %v, want %v", err, ErrRefreshRateLimited)
		}
		base = base.Add(minimumRefreshRotationInterval + time.Second)
		if err := manager.consumeRefreshTokenClaims(childClaims); err != nil {
			t.Fatalf("consume after interval error: %v", err)
		}
	})

	t.Run("per user and global tracked session limits", func(t *testing.T) {
		manager := NewTokenManager("refresh-session-limit-secret-32-bytes", 15*time.Minute, 24*time.Hour)
		manager.refreshRotationInterval = 0
		manager.refreshUserSessionLimit = 2
		manager.refreshSessionLimit = 3
		userA := testRefreshUser("session-limit-user-a")
		for index := 0; index < 2; index++ {
			pair, err := manager.GenerateTokenPair(userA)
			if err != nil {
				t.Fatalf("GenerateTokenPair(user A %d) error: %v", index, err)
			}
			if _, err := manager.ValidateRefreshToken(pair.RefreshToken); err != nil {
				t.Fatalf("ValidateRefreshToken(user A %d) error: %v", index, err)
			}
		}
		if extraA, err := manager.GenerateTokenPair(userA); !errors.Is(err, ErrRefreshSessionLimit) || extraA != nil {
			t.Fatalf("per-user overflow pair/error = %#v/%v, want nil/%v", extraA, err, ErrRefreshSessionLimit)
		}

		userB := testRefreshUser("session-limit-user-b")
		pairB, err := manager.GenerateTokenPair(userB)
		if err != nil {
			t.Fatalf("GenerateTokenPair(user B) error: %v", err)
		}
		if _, err := manager.ValidateRefreshToken(pairB.RefreshToken); err != nil {
			t.Fatalf("ValidateRefreshToken(user B) error: %v", err)
		}

		userC := testRefreshUser("session-limit-user-c")
		pairC, err := manager.GenerateTokenPair(userC)
		if !errors.Is(err, ErrRefreshSessionLimit) || pairC != nil {
			t.Fatalf("global overflow pair/error = %#v/%v, want nil/%v", pairC, err, ErrRefreshSessionLimit)
		}
	})
}

func TestRefreshReplayRevocationPersistenceIsTransactional(t *testing.T) {
	dir := t.TempDir()
	store, _, err := NewUserStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	user, err := store.Create("refresh-replay-user", "password123", "", RoleUser)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	manager := NewTokenManager("refresh-replay-transaction-secret", 15*time.Minute, 24*time.Hour)
	manager.refreshRotationInterval = 0
	if err := manager.EnableSessionPersistence(filepath.Join(dir, "auth-sessions.json")); err != nil {
		t.Fatalf("EnableSessionPersistence() error: %v", err)
	}
	handler := NewHandler(store, manager)
	initial, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}
	refreshBody, err := json.Marshal(RefreshRequest{RefreshToken: initial.RefreshToken})
	if err != nil {
		t.Fatalf("Marshal(refresh request) error: %v", err)
	}
	firstResponse := httptest.NewRecorder()
	handler.HandleRefresh(firstResponse, httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(refreshBody)))
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("first refresh status = %d: %s", firstResponse.Code, firstResponse.Body.String())
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(firstResponse.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("Unmarshal(first refresh envelope) error: %v", err)
	}
	var rotated LoginResponse
	if err := json.Unmarshal(envelope.Data, &rotated); err != nil {
		t.Fatalf("Unmarshal(rotated tokens) error: %v", err)
	}

	restore := replacePersistTokenSessionState(t, func(*TokenManager) error {
		return errors.New("pre-rename write failed")
	})
	failedResponse := httptest.NewRecorder()
	failedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(refreshBody))
	failedRequest.AddCookie(&http.Cookie{Name: AccessSessionCookieName, Value: rotated.AccessToken})
	failedRequest.AddCookie(&http.Cookie{Name: RefreshSessionCookieName, Value: rotated.RefreshToken})
	handler.HandleRefresh(failedResponse, failedRequest)
	if failedResponse.Code != http.StatusInternalServerError {
		t.Fatalf("hard-failed replay status = %d, want 500: %s", failedResponse.Code, failedResponse.Body.String())
	}
	assertCookiesNotCleared(t, failedResponse.Result().Cookies())
	if _, err := manager.ValidateAccessToken(rotated.AccessToken); err != nil {
		t.Fatalf("rotated access token after rollback error: %v", err)
	}

	restore()
	retryResponse := httptest.NewRecorder()
	retryRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(refreshBody))
	handler.HandleRefresh(retryResponse, retryRequest)
	if retryResponse.Code != http.StatusUnauthorized {
		t.Fatalf("retried replay status = %d, want 401: %s", retryResponse.Code, retryResponse.Body.String())
	}
	assertSessionCookiesCleared(t, retryResponse.Result().Cookies())
	if _, err := manager.ValidateAccessToken(rotated.AccessToken); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("rotated access token after replay error = %v, want %v", err, ErrTokenRevoked)
	}
}

func TestRefreshHandlerReturnsRetryContractForRapidRotation(t *testing.T) {
	dir := t.TempDir()
	store, _, err := NewUserStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	user, err := store.Create("rapid-refresh-user", "password123", "", RoleUser)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	manager := NewTokenManager("rapid-refresh-contract-secret-32-bytes", 15*time.Minute, 24*time.Hour)
	handler := NewHandler(store, manager)
	initial, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}

	requestRefresh := func(token string) *httptest.ResponseRecorder {
		body, marshalErr := json.Marshal(RefreshRequest{RefreshToken: token})
		if marshalErr != nil {
			t.Fatalf("Marshal(refresh request) error: %v", marshalErr)
		}
		response := httptest.NewRecorder()
		handler.HandleRefresh(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(body)))
		return response
	}
	firstResponse := requestRefresh(initial.RefreshToken)
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("first refresh status = %d: %s", firstResponse.Code, firstResponse.Body.String())
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(firstResponse.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("Unmarshal(first refresh envelope) error: %v", err)
	}
	var rotated LoginResponse
	if err := json.Unmarshal(envelope.Data, &rotated); err != nil {
		t.Fatalf("Unmarshal(rotated tokens) error: %v", err)
	}

	limitedResponse := requestRefresh(rotated.RefreshToken)
	if limitedResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("rapid refresh status = %d, want 429: %s", limitedResponse.Code, limitedResponse.Body.String())
	}
	if retryAfter := limitedResponse.Header().Get("Retry-After"); retryAfter != "30" {
		t.Fatalf("Retry-After = %q, want 30", retryAfter)
	}
	assertResponseErrorCode(t, limitedResponse.Body, "REFRESH_RATE_LIMITED")
	if _, err := manager.ValidateAccessToken(rotated.AccessToken); err != nil {
		t.Fatalf("rate-limited rotation revoked current access token: %v", err)
	}
}

func TestSessionRegistryRevocationSurvivesWallClockRollback(t *testing.T) {
	manager := NewTokenManager("session-generation-clock-secret", 15*time.Minute, 24*time.Hour)
	user := testRefreshUser("clock-rollback-user")
	oldPair, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair(old) error: %v", err)
	}

	manager.now = func() time.Time { return time.Now().Add(5 * time.Minute) }
	if err := manager.RevokeByUser(user.ID); err != nil {
		t.Fatalf("RevokeByUser() error: %v", err)
	}
	manager.now = func() time.Time { return time.Now().Add(-time.Minute) }
	newPair, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair(new) after clock rollback error: %v", err)
	}

	if _, err := manager.ValidateAccessToken(oldPair.AccessToken); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("old access token error = %v, want %v", err, ErrTokenRevoked)
	}
	if _, err := manager.ValidateRefreshToken(oldPair.RefreshToken); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("old refresh token error = %v, want %v", err, ErrTokenRevoked)
	}
	if _, err := manager.ValidateAccessToken(newPair.AccessToken); err != nil {
		t.Fatalf("new access token after clock rollback error: %v", err)
	}
	if _, err := manager.ValidateRefreshToken(newPair.RefreshToken); err != nil {
		t.Fatalf("new refresh token after clock rollback error: %v", err)
	}
}

func TestRevocationCleanupHighWaterPreventsClockRollbackRevival(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth-sessions.json")
	const secret = "revocation-high-water-secret-32-bytes"
	manager := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
	if err := manager.EnableSessionPersistence(path); err != nil {
		t.Fatalf("EnableSessionPersistence() error: %v", err)
	}
	user := testRefreshUser("revocation-high-water-user")
	pair, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}
	claims, err := manager.ValidateAccessToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken() error: %v", err)
	}
	if err := manager.RevokeSession(claims.SessionID, claims.SessionExpiresAt.Time); err != nil {
		t.Fatalf("RevokeSession() error: %v", err)
	}

	manager.now = func() time.Time { return claims.SessionExpiresAt.Time.Add(time.Minute) }
	if err := manager.CleanupExpiredSessions(); err != nil {
		t.Fatalf("CleanupExpiredSessions() error: %v", err)
	}
	manager.now = time.Now
	if _, err := manager.ValidateAccessToken(pair.AccessToken); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("access token after clock rollback error = %v, want %v", err, ErrTokenExpired)
	}

	restarted := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
	if err := restarted.EnableSessionPersistence(path); err != nil {
		t.Fatalf("EnableSessionPersistence() after cleanup error: %v", err)
	}
	if _, err := restarted.ValidateAccessToken(pair.AccessToken); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("access token after restart and rollback error = %v, want %v", err, ErrTokenExpired)
	}
}

func TestTokenManagerEffectiveTimeAdvancesDuringWallClockRollback(t *testing.T) {
	manager := NewTokenManager("effective-time-clock-secret-32-bytes", 15*time.Minute, 24*time.Hour)
	wallNow := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	monotonicNow := time.Now()
	manager.now = func() time.Time { return wallNow }
	manager.monotonicNow = func() time.Time { return monotonicNow }

	first := manager.currentTime()
	wallNow = wallNow.Add(-time.Hour)
	monotonicNow = monotonicNow.Add(5 * time.Minute)
	second := manager.currentTime()
	if elapsed := second.Sub(first); elapsed != 5*time.Minute {
		t.Fatalf("effective time advanced by %s, want %s", elapsed, 5*time.Minute)
	}
}
