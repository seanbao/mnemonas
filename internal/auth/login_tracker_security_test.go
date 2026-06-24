package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoginAttemptTracker_EvictsOldestUnlockedFailureAndPreservesLocks(t *testing.T) {
	tracker := newLoginAttemptTracker()
	tracker.maxEntries = 3
	tracker.maxEntriesPerIP = 3
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }
	clientIP := "203.0.113.40"
	lockedKey := loginAttemptKey{usernameDigest: sha256.Sum256([]byte("locked-user")), clientIP: clientIP}
	oldestUnlockedKey := loginAttemptKey{usernameDigest: sha256.Sum256([]byte("oldest-unlocked")), clientIP: clientIP}
	newerUnlockedKey := loginAttemptKey{usernameDigest: sha256.Sum256([]byte("newer-unlocked")), clientIP: clientIP}
	replacementKey := loginAttemptKey{usernameDigest: sha256.Sum256([]byte("replacement")), clientIP: clientIP}

	if !tracker.recordFailure(lockedKey, 1, time.Minute, time.Minute) {
		t.Fatal("expected locked key to enter its lock window")
	}
	now = now.Add(time.Second)
	if tracker.recordFailure(oldestUnlockedKey, 10, time.Minute, time.Minute) {
		t.Fatal("oldest unlocked key was unexpectedly locked")
	}
	now = now.Add(time.Second)
	if tracker.recordFailure(newerUnlockedKey, 10, time.Minute, time.Minute) {
		t.Fatal("newer unlocked key was unexpectedly locked")
	}
	now = now.Add(time.Second)
	if tracker.recordFailure(replacementKey, 10, time.Minute, time.Minute) {
		t.Fatal("replacement key was unexpectedly locked")
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if _, ok := tracker.attempts[lockedKey]; !ok {
		t.Fatal("locked key was evicted")
	}
	if _, ok := tracker.attempts[oldestUnlockedKey]; ok {
		t.Fatal("oldest unlocked key was not evicted")
	}
	if _, ok := tracker.attempts[newerUnlockedKey]; !ok {
		t.Fatal("newer unlocked key was evicted instead")
	}
	if _, ok := tracker.attempts[replacementKey]; !ok {
		t.Fatal("replacement key was not tracked")
	}
}

func TestLoginAttemptTracker_AllLockedCapacityDoesNotLockUnknownKey(t *testing.T) {
	tracker := newLoginAttemptTracker()
	tracker.maxEntries = 2
	tracker.maxEntriesPerIP = 2
	now := time.Date(2026, 7, 14, 8, 30, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }
	clientIP := "203.0.113.41"

	for index := 0; index < 2; index++ {
		key := loginAttemptKey{usernameDigest: sha256.Sum256([]byte(fmt.Sprintf("locked-%d", index))), clientIP: clientIP}
		if !tracker.recordFailure(key, 1, time.Minute, time.Minute) {
			t.Fatalf("key %d did not enter its lock window", index)
		}
	}
	unknownKey := loginAttemptKey{usernameDigest: sha256.Sum256([]byte("unknown")), clientIP: clientIP}
	if tracker.isLocked(unknownKey, time.Minute) {
		t.Fatal("unknown key inherited a lock solely because the tracker was full")
	}
	if tracker.recordFailure(unknownKey, 5, time.Minute, time.Minute) {
		t.Fatal("untracked failure was reported as a lock solely because the tracker was full")
	}
	if got := len(tracker.attempts); got != 2 {
		t.Fatalf("tracked failures = %d, want 2", got)
	}
}

func TestLoginAttemptTracker_CredentialCheckWindowIsConcurrentAndBounded(t *testing.T) {
	tracker := newLoginAttemptTracker()
	now := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }
	const (
		limit      = 7
		goroutines = 128
	)
	start := make(chan struct{})
	var allowed atomic.Int32
	var waitGroup sync.WaitGroup
	for index := 0; index < goroutines; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			if tracker.allowCredentialCheck("203.0.113.42", limit, 10*time.Second) {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	waitGroup.Wait()

	if got := allowed.Load(); got != limit {
		t.Fatalf("allowed credential checks = %d, want %d", got, limit)
	}
	tracker.mu.Lock()
	state := tracker.credentialChecksByIP["203.0.113.42"]
	tracker.mu.Unlock()
	if state.checks != limit {
		t.Fatalf("recorded credential checks = %d, want %d", state.checks, limit)
	}

	now = now.Add(10 * time.Second)
	if !tracker.allowCredentialCheck("203.0.113.42", limit, 10*time.Second) {
		t.Fatal("credential check window did not reopen after the short interval")
	}
}

func TestLoginAttemptTracker_CredentialCheckBucketsUseBoundedLRU(t *testing.T) {
	tracker := newLoginAttemptTracker()
	tracker.maxCredentialCheckIPs = 2
	now := time.Date(2026, 7, 14, 9, 15, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }
	const window = time.Minute

	if !tracker.allowCredentialCheck("203.0.113.50", 10, window) {
		t.Fatal("first IP was unexpectedly limited")
	}
	if !tracker.allowCredentialCheck("203.0.113.51", 10, window) {
		t.Fatal("second IP was unexpectedly limited")
	}
	if !tracker.allowCredentialCheck("203.0.113.50", 10, window) {
		t.Fatal("recently used IP was unexpectedly limited")
	}
	if !tracker.allowCredentialCheck("203.0.113.52", 10, window) {
		t.Fatal("replacement IP was unexpectedly limited")
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if got := len(tracker.credentialChecksByIP); got != tracker.maxCredentialCheckIPs {
		t.Fatalf("credential check buckets = %d, want %d", got, tracker.maxCredentialCheckIPs)
	}
	if _, ok := tracker.credentialChecksByIP["203.0.113.51"]; ok {
		t.Fatal("least recently used credential check bucket was not evicted")
	}
	if _, ok := tracker.credentialChecksByIP["203.0.113.50"]; !ok {
		t.Fatal("recently used credential check bucket was evicted")
	}
	if _, ok := tracker.credentialChecksByIP["203.0.113.52"]; !ok {
		t.Fatal("replacement credential check bucket was not stored")
	}
	if got := tracker.credentialCheckOrder.Len(); got != tracker.maxCredentialCheckIPs {
		t.Fatalf("credential check LRU entries = %d, want %d", got, tracker.maxCredentialCheckIPs)
	}
}

func TestAuthHandler_SharedNATCapacityDoesNotBlockCorrectCredentials(t *testing.T) {
	dir := t.TempDir()
	store, _, err := NewUserStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("create user store: %v", err)
	}
	if _, err := store.Create("nat-user", "password123", "", RoleUser); err != nil {
		t.Fatalf("create NAT user: %v", err)
	}
	handler := NewHandler(store, NewTokenManager("shared-nat-capacity-secret", 15*time.Minute, 24*time.Hour))
	handler.loginAttempts.maxEntries = 8
	handler.loginAttempts.maxEntriesPerIP = 8
	now := time.Date(2026, 7, 14, 9, 30, 0, 0, time.UTC)
	handler.loginAttempts.now = func() time.Time { return now }
	clientIP := "203.0.113.43"

	for index := 0; index < handler.loginAttempts.maxEntriesPerIP; index++ {
		key := loginAttemptKey{
			usernameDigest: sha256.Sum256([]byte(fmt.Sprintf("random-user-%d", index))),
			clientIP:       clientIP,
		}
		if handler.loginAttempts.recordFailure(key, 100, time.Minute, time.Minute) {
			t.Fatalf("random key %d was unexpectedly locked", index)
		}
		now = now.Add(time.Millisecond)
	}

	body, err := json.Marshal(LoginRequest{Username: "nat-user", Password: "password123"})
	if err != nil {
		t.Fatalf("marshal login request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.RemoteAddr = clientIP + ":1234"
	rec := httptest.NewRecorder()
	handler.HandleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("correct credentials behind a full shared-NAT tracker returned %d: %s", rec.Code, rec.Body.String())
	}
	handler.loginAttempts.mu.Lock()
	defer handler.loginAttempts.mu.Unlock()
	if got := len(handler.loginAttempts.attempts); got > handler.loginAttempts.maxEntries {
		t.Fatalf("tracked failures = %d, exceeds maximum %d", got, handler.loginAttempts.maxEntries)
	}
	serialized := fmt.Sprintf("%#v", handler.loginAttempts.attempts)
	if strings.Contains(serialized, "random-user") || strings.Contains(serialized, "nat-user") {
		t.Fatalf("attempt tracker retained a plaintext username: %s", serialized)
	}
}

func TestAuthHandler_SharedNATChurnOnlyAppliesShortCredentialWindow(t *testing.T) {
	dir := t.TempDir()
	store, _, err := NewUserStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("create user store: %v", err)
	}
	if _, err := store.Create("nat-window-user", "password123", "", RoleUser); err != nil {
		t.Fatalf("create NAT window user: %v", err)
	}
	handler := NewHandler(store, NewTokenManager("shared-nat-window-secret", 15*time.Minute, 24*time.Hour))
	handler.loginAttempts.maxEntries = 4
	handler.loginAttempts.maxEntriesPerIP = 4
	handler.loginFailureLimit = 100
	handler.loginCredentialCheckLimit = 4
	handler.loginCredentialCheckWindow = 10 * time.Second
	now := time.Date(2026, 7, 14, 9, 45, 0, 0, time.UTC)
	handler.loginAttempts.now = func() time.Time { return now }
	clientIP := "203.0.113.44"

	requestLogin := func(username, password string) *httptest.ResponseRecorder {
		t.Helper()
		body, marshalErr := json.Marshal(LoginRequest{Username: username, Password: password})
		if marshalErr != nil {
			t.Fatalf("marshal login request: %v", marshalErr)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		req.RemoteAddr = clientIP + ":1234"
		rec := httptest.NewRecorder()
		handler.HandleLogin(rec, req)
		return rec
	}

	for index := 0; index < handler.loginAttempts.maxEntriesPerIP; index++ {
		rec := requestLogin(fmt.Sprintf("random-nat-user-%d", index), "wrong-password")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("churn request %d status = %d, want 401: %s", index, rec.Code, rec.Body.String())
		}
	}
	limited := requestLogin("nat-window-user", "password123")
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("immediate correct login status = %d, want short-window 429: %s", limited.Code, limited.Body.String())
	}
	if got := limited.Header().Get("Retry-After"); got != "10" {
		t.Fatalf("Retry-After = %q, want 10", got)
	}

	now = now.Add(handler.loginCredentialCheckWindow)
	success := requestLogin("nat-window-user", "password123")
	if success.Code != http.StatusOK {
		t.Fatalf("correct login after short window status = %d, want 200: %s", success.Code, success.Body.String())
	}
	handler.loginAttempts.mu.Lock()
	defer handler.loginAttempts.mu.Unlock()
	if got := len(handler.loginAttempts.attempts); got != handler.loginAttempts.maxEntries {
		t.Fatalf("failure tracker entries = %d, want bounded capacity %d", got, handler.loginAttempts.maxEntries)
	}
}
