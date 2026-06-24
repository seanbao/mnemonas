package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const sessionRegistryTestSecret = "session-registry-test-secret-at-least-32-bytes"

func newSessionRegistryTestUser(id string) *User {
	return &User{
		ID:                id,
		Username:          id,
		Role:              RoleUser,
		CredentialVersion: 1,
	}
}

func newSessionRegistryTestManager(current *time.Time, monotonic *time.Time, accessTTL, refreshTTL time.Duration) *TokenManager {
	manager := NewTokenManager(sessionRegistryTestSecret, accessTTL, refreshTTL)
	manager.now = func() time.Time { return *current }
	manager.monotonicNow = func() time.Time { return *monotonic }
	return manager
}

func sessionRegistryTestClaims(t *testing.T, manager *TokenManager, pair *TokenPair) *refreshTokenClaims {
	t.Helper()
	claims, err := manager.parseRefreshTokenClaims(pair.RefreshToken)
	if err != nil {
		t.Fatalf("parseRefreshTokenClaims() error: %v", err)
	}
	return claims
}

func sessionRegistryTestCount(manager *TokenManager) int {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return len(manager.sessionRegistry)
}

func replacePersistTokenSessionStateForRegistryTest(t *testing.T, replacement func(*TokenManager) error) func() {
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

func TestSessionRegistryInitialIssuancePersistsAcrossRestart(t *testing.T) {
	current := time.Date(2026, time.July, 14, 8, 0, 0, 0, time.UTC)
	monotonic := current
	path := filepath.Join(t.TempDir(), "token-sessions.json")
	manager := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
	if err := manager.EnableSessionPersistence(path); err != nil {
		t.Fatalf("EnableSessionPersistence() error: %v", err)
	}

	user := newSessionRegistryTestUser("initial-registry-user")
	pair, err := manager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}
	claims := sessionRegistryTestClaims(t, manager, pair)

	manager.mu.RLock()
	record, tracked := manager.sessionRegistry[claims.SessionID]
	registrySize := len(manager.sessionRegistry)
	manager.mu.RUnlock()
	if !tracked {
		t.Fatalf("new session %q was not registered before token publication", claims.SessionID)
	}
	if registrySize != 1 {
		t.Fatalf("session registry size = %d, want 1", registrySize)
	}
	if record.UserID != user.ID {
		t.Fatalf("session user = %q, want %q", record.UserID, user.ID)
	}
	if !record.ExpiresAt.Equal(claims.ExpiresAt.Time) {
		t.Fatalf("session expiry = %s, want %s", record.ExpiresAt, claims.ExpiresAt.Time)
	}
	if record.NextRefreshGeneration != 0 {
		t.Fatalf("initial next refresh generation = %d, want 0", record.NextRefreshGeneration)
	}
	wantLastRotatedAt := jwtTimestamp(current).Add(-minimumRefreshRotationInterval)
	if !record.LastRotatedAt.Equal(wantLastRotatedAt) {
		t.Fatalf("initial last rotation = %s, want %s", record.LastRotatedAt, wantLastRotatedAt)
	}

	state, _, err := loadTokenSessionState(path)
	if err != nil {
		t.Fatalf("loadTokenSessionState() error: %v", err)
	}
	persisted, ok := state.Sessions[claims.SessionID]
	if !ok {
		t.Fatalf("persisted state omitted issued session %q", claims.SessionID)
	}
	assertPersistedSessionRegistryRecord(t, persisted, record)

	restarted := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
	if err := restarted.EnableSessionPersistence(path); err != nil {
		t.Fatalf("EnableSessionPersistence() after restart error: %v", err)
	}
	if _, err := restarted.ValidateAccessToken(pair.AccessToken); err != nil {
		t.Fatalf("ValidateAccessToken() after restart error: %v", err)
	}
	if _, err := restarted.ValidateRefreshToken(pair.RefreshToken); err != nil {
		t.Fatalf("ValidateRefreshToken() after restart error: %v", err)
	}
}

func assertPersistedSessionRegistryRecord(t *testing.T, persisted sessionRegistryRecord, record sessionRegistryRecord) {
	t.Helper()
	if persisted.UserID != record.UserID {
		t.Fatalf("persisted session user = %q, want %q", persisted.UserID, record.UserID)
	}
	if !persisted.ExpiresAt.Equal(record.ExpiresAt) {
		t.Fatalf("persisted session expiry = %s, want %s", persisted.ExpiresAt, record.ExpiresAt)
	}
	if persisted.NextRefreshGeneration != record.NextRefreshGeneration {
		t.Fatalf("persisted next refresh generation = %d, want %d", persisted.NextRefreshGeneration, record.NextRefreshGeneration)
	}
	if !persisted.LastRotatedAt.Equal(record.LastRotatedAt) {
		t.Fatalf("persisted last rotation = %s, want %s", persisted.LastRotatedAt, record.LastRotatedAt)
	}
}

func TestSessionRegistryRejectsUnknownSignedSessionAfterRestart(t *testing.T) {
	current := time.Date(2026, time.July, 14, 9, 0, 0, 0, time.UTC)
	monotonic := current
	issuer := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
	pair, err := issuer.GenerateTokenPair(newSessionRegistryTestUser("unknown-session-user"))
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}

	restarted := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
	if err := restarted.EnableSessionPersistence(filepath.Join(t.TempDir(), "token-sessions.json")); err != nil {
		t.Fatalf("EnableSessionPersistence() for empty restarted registry error: %v", err)
	}
	if _, err := restarted.ValidateAccessToken(pair.AccessToken); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("ValidateAccessToken(unknown session) error = %v, want %v", err, ErrTokenRevoked)
	}
	if _, err := restarted.ValidateRefreshToken(pair.RefreshToken); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("ValidateRefreshToken(unknown generation-zero session) error = %v, want %v", err, ErrTokenRevoked)
	}
}

func TestSessionRegistryConcurrentIssuanceHonorsCapacity(t *testing.T) {
	tests := []struct {
		name      string
		userFor   func(int) *User
		globalMax int
		userMax   int
	}{
		{
			name:      "global capacity",
			userFor:   func(index int) *User { return newSessionRegistryTestUser("global-cap-user-" + string(rune('a'+index))) },
			globalMax: 1,
			userMax:   2,
		},
		{
			name:      "per-user capacity",
			userFor:   func(int) *User { return newSessionRegistryTestUser("per-user-cap-user") },
			globalMax: 2,
			userMax:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := time.Date(2026, time.July, 14, 10, 0, 0, 0, time.UTC)
			monotonic := current
			manager := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
			manager.refreshSessionLimit = tt.globalMax
			manager.refreshUserSessionLimit = tt.userMax

			start := make(chan struct{})
			type result struct {
				pair *TokenPair
				err  error
			}
			results := make(chan result, 2)
			for index := 0; index < 2; index++ {
				user := tt.userFor(index)
				go func() {
					<-start
					pair, err := manager.GenerateTokenPair(user)
					results <- result{pair: pair, err: err}
				}()
			}
			close(start)

			successes := 0
			limited := 0
			for range 2 {
				result := <-results
				switch {
				case result.err == nil:
					successes++
					if result.pair == nil {
						t.Fatal("successful issuance returned a nil token pair")
					}
				case errors.Is(result.err, ErrRefreshSessionLimit):
					limited++
					if result.pair != nil {
						t.Fatal("capacity-limited issuance returned publishable tokens")
					}
				default:
					t.Fatalf("GenerateTokenPair() unexpected error: %v", result.err)
				}
			}
			if successes != 1 || limited != 1 {
				t.Fatalf("concurrent issuance results: successes=%d limited=%d, want 1/1", successes, limited)
			}
			if got := sessionRegistryTestCount(manager); got != 1 {
				t.Fatalf("session registry size = %d, want 1", got)
			}
		})
	}
}

func TestSessionRegistryLogoutChurnDoesNotGrowAndExpiryReleasesCapacity(t *testing.T) {
	t.Run("logout churn", func(t *testing.T) {
		current := time.Date(2026, time.July, 14, 11, 0, 0, 0, time.UTC)
		monotonic := current
		path := filepath.Join(t.TempDir(), "token-sessions.json")
		manager := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
		manager.refreshSessionLimit = 1
		manager.refreshUserSessionLimit = 1
		if err := manager.EnableSessionPersistence(path); err != nil {
			t.Fatalf("EnableSessionPersistence() error: %v", err)
		}
		user := newSessionRegistryTestUser("logout-churn-user")

		for index := 0; index < 256; index++ {
			pair, err := manager.GenerateTokenPair(user)
			if err != nil {
				t.Fatalf("GenerateTokenPair(%d) error: %v", index, err)
			}
			claims := sessionRegistryTestClaims(t, manager, pair)
			if err := manager.RevokeSession(claims.SessionID, claims.ExpiresAt.Time); err != nil {
				t.Fatalf("RevokeSession(%d) error: %v", index, err)
			}
			for duplicate := 0; duplicate < 3; duplicate++ {
				if err := manager.RevokeSession(claims.SessionID, claims.ExpiresAt.Time); err != nil {
					t.Fatalf("duplicate RevokeSession(%d, %d) error: %v", index, duplicate, err)
				}
			}
			if got := sessionRegistryTestCount(manager); got != 0 {
				t.Fatalf("session registry size after logout %d = %d, want 0", index, got)
			}
		}

		state, _, err := loadTokenSessionState(path)
		if err != nil {
			t.Fatalf("loadTokenSessionState() error: %v", err)
		}
		if got := len(state.Sessions); got != 0 {
			t.Fatalf("persisted sessions after logout churn = %d, want 0", got)
		}
	})

	t.Run("expired session releases capacity", func(t *testing.T) {
		current := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
		monotonic := current
		manager := newSessionRegistryTestManager(&current, &monotonic, 30*time.Second, time.Minute)
		manager.refreshSessionLimit = 1
		manager.refreshUserSessionLimit = 1
		user := newSessionRegistryTestUser("expiry-capacity-user")
		first, err := manager.GenerateTokenPair(user)
		if err != nil {
			t.Fatalf("GenerateTokenPair(first) error: %v", err)
		}

		current = current.Add(2 * time.Minute)
		monotonic = monotonic.Add(2 * time.Minute)
		second, err := manager.GenerateTokenPair(user)
		if err != nil {
			t.Fatalf("GenerateTokenPair(after expiry) error: %v", err)
		}
		if second == nil {
			t.Fatal("GenerateTokenPair(after expiry) returned nil pair")
		}
		if got := sessionRegistryTestCount(manager); got != 1 {
			t.Fatalf("session registry size after expiry replacement = %d, want 1", got)
		}
		if _, err := manager.ValidateRefreshToken(first.RefreshToken); !errors.Is(err, ErrTokenExpired) {
			t.Fatalf("ValidateRefreshToken(expired session) error = %v, want %v", err, ErrTokenExpired)
		}
	})
}

func TestSessionRegistryInitialIssuancePersistenceTransaction(t *testing.T) {
	t.Run("hard failure rolls back and publishes no token", func(t *testing.T) {
		current := time.Date(2026, time.July, 14, 13, 0, 0, 0, time.UTC)
		monotonic := current
		path := filepath.Join(t.TempDir(), "token-sessions.json")
		manager := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
		if err := manager.EnableSessionPersistence(path); err != nil {
			t.Fatalf("EnableSessionPersistence() error: %v", err)
		}

		restore := replacePersistTokenSessionStateForRegistryTest(t, func(*TokenManager) error {
			return errors.New("pre-rename session registry write failed")
		})
		pair, err := manager.GenerateTokenPair(newSessionRegistryTestUser("hard-issuance-user"))
		if err == nil || isAuthPersistenceWarning(err) {
			t.Fatalf("GenerateTokenPair() error = %v, want hard persistence failure", err)
		}
		if pair != nil {
			t.Fatal("hard-failed issuance returned publishable tokens")
		}
		if got := sessionRegistryTestCount(manager); got != 0 {
			t.Fatalf("session registry size after hard failure = %d, want 0", got)
		}
		restore()

		restarted := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
		if err := restarted.EnableSessionPersistence(path); err != nil {
			t.Fatalf("EnableSessionPersistence() after failed issuance error: %v", err)
		}
		if got := sessionRegistryTestCount(restarted); got != 0 {
			t.Fatalf("restarted session registry size = %d, want 0", got)
		}
	})

	t.Run("committed warning keeps token and survives restart", func(t *testing.T) {
		current := time.Date(2026, time.July, 14, 14, 0, 0, 0, time.UTC)
		monotonic := current
		path := filepath.Join(t.TempDir(), "token-sessions.json")
		manager := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
		if err := manager.EnableSessionPersistence(path); err != nil {
			t.Fatalf("EnableSessionPersistence() error: %v", err)
		}

		restore := replacePersistTokenSessionStateForRegistryTest(t, func(manager *TokenManager) error {
			if err := manager.persistSessionStateLocked(); err != nil {
				return err
			}
			return wrapAuthPersistenceWarning(errors.New("post-rename directory sync failed"))
		})
		pair, err := manager.GenerateTokenPair(newSessionRegistryTestUser("warning-issuance-user"))
		if !isAuthPersistenceWarning(err) {
			t.Fatalf("GenerateTokenPair() error = %v, want persistence warning", err)
		}
		if pair == nil || pair.AccessToken == "" || pair.RefreshToken == "" {
			t.Fatalf("warning issuance returned unusable token pair: %#v", pair)
		}
		if got := sessionRegistryTestCount(manager); got != 1 {
			t.Fatalf("session registry size after warning = %d, want 1", got)
		}
		restore()

		restarted := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
		if err := restarted.EnableSessionPersistence(path); err != nil {
			t.Fatalf("EnableSessionPersistence() after warning error: %v", err)
		}
		if _, err := restarted.ValidateAccessToken(pair.AccessToken); err != nil {
			t.Fatalf("ValidateAccessToken() after warning restart error: %v", err)
		}
		if _, err := restarted.ValidateRefreshToken(pair.RefreshToken); err != nil {
			t.Fatalf("ValidateRefreshToken() after warning restart error: %v", err)
		}
	})
}

func TestSessionRegistryTenThousandValidationsUseFinitePersistenceWrites(t *testing.T) {
	current := time.Date(2026, time.July, 14, 15, 0, 0, 0, time.UTC)
	monotonic := current
	path := filepath.Join(t.TempDir(), "token-sessions.json")
	manager := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
	manager.timeLeaseDuration = time.Hour
	manager.timeLeaseRenewalLead = 10 * time.Minute
	if err := manager.EnableSessionPersistence(path); err != nil {
		t.Fatalf("EnableSessionPersistence() error: %v", err)
	}

	var writes atomic.Int64
	restore := replacePersistTokenSessionStateForRegistryTest(t, func(manager *TokenManager) error {
		writes.Add(1)
		return manager.persistSessionStateLocked()
	})
	pair, err := manager.GenerateTokenPair(newSessionRegistryTestUser("validation-write-user"))
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}
	baselineWrites := writes.Load()

	manager.mu.Lock()
	manager.restartTimeFloor = current.Add(30 * time.Minute)
	manager.mu.Unlock()
	for index := 0; index < 5_000; index++ {
		if _, err := manager.ValidateAccessToken(pair.AccessToken); err != nil {
			t.Fatalf("ValidateAccessToken(%d) error: %v", index, err)
		}
		if _, err := manager.ValidateRefreshToken(pair.RefreshToken); err != nil {
			t.Fatalf("ValidateRefreshToken(%d) error: %v", index, err)
		}
	}
	restore()

	if additional := writes.Load() - baselineWrites; additional > 1 {
		t.Fatalf("10,000 validations caused %d additional persistence writes, want at most 1", additional)
	}
}

func TestSessionRegistryRestartTimeLeaseBoundary(t *testing.T) {
	t.Run("hard renewal failure exhausts the existing lease before failing closed", func(t *testing.T) {
		current := time.Date(2026, time.July, 14, 16, 0, 0, 0, time.UTC)
		monotonic := current
		path := filepath.Join(t.TempDir(), "token-sessions.json")
		manager := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
		manager.timeLeaseDuration = time.Minute
		manager.timeLeaseRenewalLead = 15 * time.Second
		if err := manager.EnableSessionPersistence(path); err != nil {
			t.Fatalf("EnableSessionPersistence() error: %v", err)
		}
		pair, err := manager.GenerateTokenPair(newSessionRegistryTestUser("lease-hard-failure-user"))
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}

		manager.mu.Lock()
		oldFloor := current.Add(10 * time.Second)
		manager.restartTimeFloor = oldFloor
		manager.mu.Unlock()
		restore := replacePersistTokenSessionStateForRegistryTest(t, func(*TokenManager) error {
			return errors.New("restart-time lease renewal failed")
		})
		if _, err := manager.ValidateAccessToken(pair.AccessToken); err != nil {
			t.Fatalf("ValidateAccessToken() before lease exhaustion error: %v", err)
		}
		manager.mu.RLock()
		floorAfterFailure := manager.restartTimeFloor
		manager.mu.RUnlock()
		if !floorAfterFailure.Equal(oldFloor) {
			t.Fatalf("restart time floor after failed renewal = %s, want %s", floorAfterFailure, oldFloor)
		}

		current = oldFloor
		monotonic = monotonic.Add(10 * time.Second)
		if _, err := manager.ValidateAccessToken(pair.AccessToken); !errors.Is(err, ErrTokenStateUnavailable) {
			t.Fatalf("ValidateAccessToken() at exhausted lease error = %v, want %v", err, ErrTokenStateUnavailable)
		}
		restore()

		if _, err := manager.ValidateAccessToken(pair.AccessToken); err != nil {
			t.Fatalf("ValidateAccessToken() after persistence recovery error: %v", err)
		}
		manager.mu.RLock()
		renewedFloor := manager.restartTimeFloor
		manager.mu.RUnlock()
		if !renewedFloor.After(oldFloor) {
			t.Fatalf("restart time floor after recovery = %s, want after %s", renewedFloor, oldFloor)
		}
	})

	t.Run("warning renewal is treated as committed", func(t *testing.T) {
		current := time.Date(2026, time.July, 14, 16, 30, 0, 0, time.UTC)
		monotonic := current
		path := filepath.Join(t.TempDir(), "token-sessions.json")
		manager := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
		manager.timeLeaseDuration = time.Minute
		manager.timeLeaseRenewalLead = 15 * time.Second
		if err := manager.EnableSessionPersistence(path); err != nil {
			t.Fatalf("EnableSessionPersistence() error: %v", err)
		}
		pair, err := manager.GenerateTokenPair(newSessionRegistryTestUser("lease-warning-user"))
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}

		manager.mu.Lock()
		oldFloor := current.Add(10 * time.Second)
		manager.restartTimeFloor = oldFloor
		manager.mu.Unlock()
		restore := replacePersistTokenSessionStateForRegistryTest(t, func(manager *TokenManager) error {
			if err := manager.persistSessionStateLocked(); err != nil {
				return err
			}
			return wrapAuthPersistenceWarning(errors.New("lease directory sync failed"))
		})
		if _, err := manager.ValidateAccessToken(pair.AccessToken); err != nil {
			t.Fatalf("ValidateAccessToken() after committed lease warning error: %v", err)
		}
		manager.mu.RLock()
		renewedFloor := manager.restartTimeFloor
		manager.mu.RUnlock()
		if !renewedFloor.After(oldFloor) {
			t.Fatalf("restart time floor after warning = %s, want after %s", renewedFloor, oldFloor)
		}
		restore()

		restarted := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
		restarted.timeLeaseDuration = time.Minute
		restarted.timeLeaseRenewalLead = 15 * time.Second
		if err := restarted.EnableSessionPersistence(path); err != nil {
			t.Fatalf("EnableSessionPersistence() after lease warning error: %v", err)
		}
		if _, err := restarted.ValidateAccessToken(pair.AccessToken); err != nil {
			t.Fatalf("ValidateAccessToken() after lease-warning restart error: %v", err)
		}
	})

	t.Run("concurrent boundary validations perform one renewal", func(t *testing.T) {
		current := time.Date(2026, time.July, 14, 17, 0, 0, 0, time.UTC)
		monotonic := current
		path := filepath.Join(t.TempDir(), "token-sessions.json")
		manager := newSessionRegistryTestManager(&current, &monotonic, 15*time.Minute, 24*time.Hour)
		manager.timeLeaseDuration = time.Minute
		manager.timeLeaseRenewalLead = 15 * time.Second
		if err := manager.EnableSessionPersistence(path); err != nil {
			t.Fatalf("EnableSessionPersistence() error: %v", err)
		}
		pair, err := manager.GenerateTokenPair(newSessionRegistryTestUser("lease-race-user"))
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}
		manager.mu.Lock()
		manager.restartTimeFloor = current.Add(10 * time.Second)
		manager.mu.Unlock()

		var writes atomic.Int64
		enteredWrite := make(chan struct{})
		releaseWrite := make(chan struct{})
		var enteredOnce sync.Once
		restore := replacePersistTokenSessionStateForRegistryTest(t, func(manager *TokenManager) error {
			writes.Add(1)
			enteredOnce.Do(func() { close(enteredWrite) })
			<-releaseWrite
			return manager.persistSessionStateLocked()
		})

		const validators = 32
		start := make(chan struct{})
		results := make(chan error, validators)
		for index := 0; index < validators; index++ {
			go func(index int) {
				<-start
				if index%2 == 0 {
					_, err := manager.ValidateAccessToken(pair.AccessToken)
					results <- err
					return
				}
				_, err := manager.ValidateRefreshToken(pair.RefreshToken)
				results <- err
			}(index)
		}
		close(start)
		select {
		case <-enteredWrite:
		case <-time.After(2 * time.Second):
			close(releaseWrite)
			t.Fatal("timed out waiting for lease renewal persistence")
		}
		close(releaseWrite)
		for range validators {
			if err := <-results; err != nil {
				t.Fatalf("concurrent validation error: %v", err)
			}
		}
		restore()
		if got := writes.Load(); got != 1 {
			t.Fatalf("concurrent lease boundary writes = %d, want 1", got)
		}
	})
}

func TestSessionRegistryStateFileRejectsUntrustedInput(t *testing.T) {
	floor := time.Date(2026, time.July, 14, 18, 0, 0, 0, time.UTC)
	validSessionID := "00000000000000000000000000000001"
	validRecord := fmt.Sprintf(`{"user_id":"user","expires_at":"%s","next_refresh_generation":0,"last_rotated_at":"%s"}`, floor.Add(time.Hour).Format(time.RFC3339Nano), floor.Format(time.RFC3339Nano))
	validState := fmt.Sprintf(`{"schema_version":%d,"restart_time_floor":"%s","sessions":{"%s":%s}}`, tokenSessionSchemaVersion, floor.Format(time.RFC3339Nano), validSessionID, validRecord)
	tests := []struct {
		name string
		data []byte
	}{
		{name: "unknown top-level field", data: []byte(validState[:len(validState)-1] + `,"unknown":true}`)},
		{name: "unknown record field", data: []byte(fmt.Sprintf(`{"schema_version":%d,"restart_time_floor":"%s","sessions":{"%s":{"user_id":"user","expires_at":"%s","next_refresh_generation":0,"last_rotated_at":"%s","unknown":true}}}`, tokenSessionSchemaVersion, floor.Format(time.RFC3339Nano), validSessionID, floor.Add(time.Hour).Format(time.RFC3339Nano), floor.Format(time.RFC3339Nano)))},
		{name: "missing schema", data: []byte(fmt.Sprintf(`{"restart_time_floor":"%s","sessions":{}}`, floor.Format(time.RFC3339Nano)))},
		{name: "missing restart floor", data: []byte(fmt.Sprintf(`{"schema_version":%d,"sessions":{}}`, tokenSessionSchemaVersion))},
		{name: "missing sessions", data: []byte(fmt.Sprintf(`{"schema_version":%d,"restart_time_floor":"%s"}`, tokenSessionSchemaVersion, floor.Format(time.RFC3339Nano)))},
		{name: "missing generation", data: []byte(fmt.Sprintf(`{"schema_version":%d,"restart_time_floor":"%s","sessions":{"%s":{"user_id":"user","expires_at":"%s","last_rotated_at":"%s"}}}`, tokenSessionSchemaVersion, floor.Format(time.RFC3339Nano), validSessionID, floor.Add(time.Hour).Format(time.RFC3339Nano), floor.Format(time.RFC3339Nano)))},
		{name: "duplicate field", data: []byte(fmt.Sprintf(`{"schema_version":%d,"schema_version":%d,"restart_time_floor":"%s","sessions":{}}`, tokenSessionSchemaVersion, tokenSessionSchemaVersion, floor.Format(time.RFC3339Nano)))},
		{name: "trailing value", data: []byte(validState + `{}`)},
		{name: "invalid session id", data: []byte(fmt.Sprintf(`{"schema_version":%d,"restart_time_floor":"%s","sessions":{"not-random":%s}}`, tokenSessionSchemaVersion, floor.Format(time.RFC3339Nano), validRecord))},
		{name: "oversized file", data: bytes.Repeat([]byte{' '}, maxTokenSessionStateFileBytes+1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth-sessions.json")
			if err := os.WriteFile(path, tt.data, 0o600); err != nil {
				t.Fatalf("WriteFile() error: %v", err)
			}
			manager := NewTokenManager(sessionRegistryTestSecret, 15*time.Minute, time.Hour)
			if err := manager.EnableSessionPersistence(path); err == nil {
				t.Fatal("EnableSessionPersistence() error = nil, want fail-closed rejection")
			}
		})
	}

	t.Run("session map count", func(t *testing.T) {
		state := tokenSessionState{
			SchemaVersion:    tokenSessionSchemaVersion,
			RestartTimeFloor: floor,
			Sessions:         make(map[string]sessionRegistryRecord, maxTrackedRefreshSessions+1),
		}
		for index := 1; index <= maxTrackedRefreshSessions+1; index++ {
			state.Sessions[fmt.Sprintf("%032x", index)] = sessionRegistryRecord{
				UserID:                "user",
				ExpiresAt:             floor.Add(time.Hour),
				NextRefreshGeneration: 0,
				LastRotatedAt:         floor,
			}
		}
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("Marshal() error: %v", err)
		}
		path := filepath.Join(t.TempDir(), "auth-sessions.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}
		manager := NewTokenManager(sessionRegistryTestSecret, 15*time.Minute, time.Hour)
		if err := manager.EnableSessionPersistence(path); err == nil {
			t.Fatal("EnableSessionPersistence() accepted an oversized session map")
		}
	})
}

func TestSessionRegistryExpiredValidationAdvancesRestartFloor(t *testing.T) {
	issuedAt := time.Date(2026, time.July, 14, 19, 0, 0, 0, time.UTC)
	current := issuedAt
	monotonic := issuedAt
	path := filepath.Join(t.TempDir(), "auth-sessions.json")
	manager := newSessionRegistryTestManager(&current, &monotonic, 30*time.Second, 5*time.Minute)
	if err := manager.EnableSessionPersistence(path); err != nil {
		t.Fatalf("EnableSessionPersistence() error: %v", err)
	}
	pair, err := manager.GenerateTokenPair(newSessionRegistryTestUser("expired-validation-user"))
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}

	current = issuedAt.Add(2 * time.Minute)
	monotonic = monotonic.Add(2 * time.Minute)
	if _, err := manager.ValidateAccessToken(pair.AccessToken); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("ValidateAccessToken() error = %v, want %v", err, ErrTokenExpired)
	}

	rolledBackWall := issuedAt
	restartMonotonic := issuedAt
	restarted := newSessionRegistryTestManager(&rolledBackWall, &restartMonotonic, 30*time.Second, 5*time.Minute)
	if err := restarted.EnableSessionPersistence(path); err != nil {
		t.Fatalf("EnableSessionPersistence() after rollback error: %v", err)
	}
	if _, err := restarted.ValidateAccessToken(pair.AccessToken); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("ValidateAccessToken() after restart rollback error = %v, want %v", err, ErrTokenExpired)
	}
}
