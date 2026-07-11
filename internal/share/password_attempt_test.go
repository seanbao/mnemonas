package share

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newPasswordAttemptTestHandler(t *testing.T) (*Share, *Handler) {
	t.Helper()
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "owner-1",
		Password:  "correct-password",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	handler := NewHandler(store, nil)
	handler.passwordFailureDelay = 0
	return share, handler
}

func passwordAttemptRequest(shareID, password string) *http.Request {
	return passwordAttemptRequestFrom(shareID, password, "203.0.113.5:1234")
}

func passwordAttemptRequestFrom(shareID, password, remoteAddr string) *http.Request {
	request := newRouteRequest(http.MethodPost, "/s/"+shareID, shareID, []byte(`{"password":"`+password+`"}`))
	request.RemoteAddr = remoteAddr
	return request
}

func TestAccessShareWithPassword_OnlyOneBcryptPerShareClient(t *testing.T) {
	share, handler := newPasswordAttemptTestHandler(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var checks atomic.Int32
	handler.beforePasswordCheck = func(string) {
		if checks.Add(1) == 1 {
			close(entered)
			<-release
		}
	}

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		handler.AccessShareWithPassword(recorder, passwordAttemptRequest(share.ID, "wrong"))
		firstDone <- recorder
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first password verification did not start")
	}

	concurrent := httptest.NewRecorder()
	handler.AccessShareWithPassword(concurrent, passwordAttemptRequest(share.ID, "wrong"))
	if concurrent.Code != http.StatusTooManyRequests {
		t.Fatalf("concurrent status = %d, want 429", concurrent.Code)
	}
	assertRetryAfterSeconds(t, concurrent)
	if checks.Load() != 1 {
		t.Fatalf("bcrypt checks = %d, want 1", checks.Load())
	}

	close(release)
	select {
	case first := <-firstDone:
		if first.Code != http.StatusUnauthorized {
			t.Fatalf("first status = %d, want 401", first.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first password verification did not finish")
	}
}

func TestAccessShareWithPassword_ParallelRequestsCannotBypassFailureLimit(t *testing.T) {
	share, handler := newPasswordAttemptTestHandler(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var checks atomic.Int32
	handler.beforePasswordCheck = func(string) {
		if checks.Add(1) == 1 {
			close(entered)
			<-release
		}
	}

	const parallel = 12
	start := make(chan struct{})
	statuses := make(chan int, parallel)
	var wait sync.WaitGroup
	for index := 0; index < parallel; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			recorder := httptest.NewRecorder()
			handler.AccessShareWithPassword(recorder, passwordAttemptRequest(share.ID, "wrong"))
			statuses <- recorder.Code
		}()
	}
	close(start)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("password verification did not start")
	}
	close(release)
	wait.Wait()
	close(statuses)
	unauthorized := 0
	limited := 0
	for status := range statuses {
		switch status {
		case http.StatusUnauthorized:
			unauthorized++
		case http.StatusTooManyRequests:
			limited++
		default:
			t.Fatalf("unexpected parallel status %d", status)
		}
	}
	if unauthorized != 1 || limited != parallel-1 || checks.Load() != 1 {
		t.Fatalf("parallel results unauthorized=%d limited=%d checks=%d", unauthorized, limited, checks.Load())
	}

	handler.beforePasswordCheck = nil
	for attempt := 2; attempt < handler.passwordFailureLimit; attempt++ {
		recorder := httptest.NewRecorder()
		handler.AccessShareWithPassword(recorder, passwordAttemptRequest(share.ID, "wrong"))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", attempt, recorder.Code)
		}
	}
	lockRecorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(lockRecorder, passwordAttemptRequest(share.ID, "wrong"))
	if lockRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("limit attempt status = %d, want 429", lockRecorder.Code)
	}
	assertRetryAfterSeconds(t, lockRecorder)

	validRecorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(validRecorder, passwordAttemptRequest(share.ID, "correct-password"))
	if validRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("valid password during lock status = %d, want 429", validRecorder.Code)
	}
	assertRetryAfterSeconds(t, validRecorder)
}

func TestPasswordAttemptTracker_ActiveFailuresSurviveShareCapacityPressure(t *testing.T) {
	tracker := newPasswordAttemptTracker()
	tracker.capacity = 2
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }

	for failure := 1; failure <= 4; failure++ {
		finish, _, admitted := tracker.begin("share-a", "client-a", 5, time.Hour, time.Minute)
		if !admitted {
			t.Fatalf("client-a failure %d was not admitted", failure)
		}
		if retryAfter := finish(passwordAttemptFailed); retryAfter != 0 {
			t.Fatalf("client-a failure %d retry_after = %v, want 0", failure, retryAfter)
		}
	}
	finishSecond, _, admitted := tracker.begin("share-a", "client-b", 5, time.Hour, time.Minute)
	if !admitted {
		t.Fatal("client-b was not admitted")
	}
	finishSecond(passwordAttemptFailed)
	if _, retryAfter, admitted := tracker.begin("share-a", "client-c", 5, time.Hour, time.Minute); admitted || retryAfter < time.Second {
		t.Fatalf("capacity admission=%v retry_after=%v, want fail closed", admitted, retryAfter)
	}
	finishFifth, _, admitted := tracker.begin("share-a", "client-a", 5, time.Hour, time.Minute)
	if !admitted {
		t.Fatal("existing client-a was rejected under capacity pressure")
	}
	if retryAfter := finishFifth(passwordAttemptFailed); retryAfter != time.Minute {
		t.Fatalf("fifth failure retry_after = %v, want %v", retryAfter, time.Minute)
	}
	state := tracker.partitions["share-a"].attempts["client-a"]
	if state.failures != 5 || !state.lockedUntil.After(now) {
		t.Fatalf("client-a state = %#v, want five failures and lock", state)
	}
}

func TestPasswordAttemptTracker_ShareCapacityDoesNotAffectAnotherShare(t *testing.T) {
	tracker := newPasswordAttemptTracker()
	tracker.capacity = 1
	finishA, _, admitted := tracker.begin("share-a", "client-a", 5, time.Hour, time.Minute)
	if !admitted {
		t.Fatal("share-a client was not admitted")
	}
	finishA(passwordAttemptFailed)
	if _, _, admitted := tracker.begin("share-a", "client-b", 5, time.Hour, time.Minute); admitted {
		t.Fatal("second share-a client bypassed the per-share capacity")
	}
	finishB, _, admitted := tracker.begin("share-b", "client-b", 5, time.Hour, time.Minute)
	if !admitted {
		t.Fatal("share-a saturation affected share-b")
	}
	finishB(passwordAttemptSucceeded)
	if tracker.total != 1 || len(tracker.partitions) != 1 {
		t.Fatalf("tracker total/partitions = %d/%d, want 1/1", tracker.total, len(tracker.partitions))
	}
}

func TestPasswordAttemptTracker_GlobalCapacityFailsClosed(t *testing.T) {
	tracker := newPasswordAttemptTracker()
	for index := 0; index < defaultPasswordGlobalAttemptCapacity; index++ {
		shareID := "share-" + strconv.Itoa(index/defaultPasswordAttemptCapacity)
		clientID := "client-" + strconv.Itoa(index%defaultPasswordAttemptCapacity)
		finish, _, admitted := tracker.begin(shareID, clientID, 5, time.Hour, time.Minute)
		if !admitted {
			t.Fatalf("entry %d was not admitted", index)
		}
		finish(passwordAttemptFailed)
	}
	if _, retryAfter, admitted := tracker.begin("share-overflow", "client", 5, time.Hour, time.Minute); admitted || retryAfter < time.Second {
		t.Fatalf("global admission=%v retry_after=%v, want fail closed", admitted, retryAfter)
	}
	if tracker.total != defaultPasswordGlobalAttemptCapacity || tracker.total != tracker.globalCapacity {
		t.Fatalf("tracker total = %d, want global capacity %d", tracker.total, tracker.globalCapacity)
	}
}

func TestAccessShareWithPassword_BcryptGatePreservesPriorFailures(t *testing.T) {
	share, handler := newPasswordAttemptTestHandler(t)
	handler.passwordFailureLimit = 2
	first := httptest.NewRecorder()
	handler.AccessShareWithPassword(first, passwordAttemptRequest(share.ID, "wrong"))
	if first.Code != http.StatusUnauthorized {
		t.Fatalf("first status = %d, want 401", first.Code)
	}
	for index := 0; index < cap(handler.passwordCheckGate); index++ {
		handler.passwordCheckGate <- struct{}{}
	}
	limited := httptest.NewRecorder()
	handler.AccessShareWithPassword(limited, passwordAttemptRequest(share.ID, "wrong"))
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("bcrypt saturation status = %d, want 429", limited.Code)
	}
	assertRetryAfterSeconds(t, limited)
	for len(handler.passwordCheckGate) > 0 {
		<-handler.passwordCheckGate
	}
	state := handler.passwordAttempts.partitions[share.ID].attempts["203.0.113.5"]
	if state.failures != 1 || state.inFlight {
		t.Fatalf("state after bcrypt saturation = %#v, want one retained failure", state)
	}
	locked := httptest.NewRecorder()
	handler.AccessShareWithPassword(locked, passwordAttemptRequest(share.ID, "wrong"))
	if locked.Code != http.StatusTooManyRequests {
		t.Fatalf("post-saturation second failure status = %d, want 429", locked.Code)
	}
}

func TestAccessShareWithPassword_OverlongPasswordDoesNotConsumeAttemptCapacity(t *testing.T) {
	share, handler := newPasswordAttemptTestHandler(t)
	handler.passwordAttempts.capacity = 1
	overlong := httptest.NewRecorder()
	handler.AccessShareWithPassword(overlong, passwordAttemptRequestFrom(
		share.ID,
		strings.Repeat("a", maxSharePasswordBytes+1),
		"203.0.113.10:1234",
	))
	if overlong.Code != http.StatusBadRequest || responseErrorCode(t, overlong) != "PASSWORD_TOO_LONG" {
		t.Fatalf("overlong status/body = %d/%s", overlong.Code, overlong.Body.String())
	}
	if handler.passwordAttempts.total != 0 || len(handler.passwordAttempts.partitions) != 0 {
		t.Fatalf("overlong attempt consumed tracker state: total=%d partitions=%d", handler.passwordAttempts.total, len(handler.passwordAttempts.partitions))
	}
	validLength := httptest.NewRecorder()
	handler.AccessShareWithPassword(validLength, passwordAttemptRequestFrom(share.ID, "wrong", "203.0.113.11:1234"))
	if validLength.Code != http.StatusUnauthorized {
		t.Fatalf("valid-length client status = %d, want 401", validLength.Code)
	}
}

func TestPublicShareAccessPolicySnapshot_IncludesTicketAndPasswordConcurrencyLimits(t *testing.T) {
	policy := PublicShareAccessPolicySnapshot()
	if policy.PasswordAttemptCapacity != 128 ||
		policy.PasswordGlobalAttemptCapacity != 4096 ||
		policy.PasswordConcurrentLimit != 1 ||
		policy.PasswordBcryptConcurrency != 8 {
		t.Fatalf("password policy = %#v", policy)
	}
	if policy.DownloadTicketCookiePrefix != downloadTicketCookiePrefix ||
		policy.DownloadTicketSecurePrefix != downloadTicketSecureCookiePrefix ||
		policy.DownloadTicketCookiePath != "/" {
		t.Fatalf("ticket binder cookie policy = %#v", policy)
	}
	if policy.DownloadTicketTTL != 24*time.Hour ||
		policy.DownloadTicketCookieLimit != 32 ||
		policy.DownloadTicketConcurrency != 4 ||
		policy.PublicArchiveConcurrency != 4 {
		t.Fatalf("ticket policy = %#v", policy)
	}
}

func assertRetryAfterSeconds(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	value := recorder.Header().Get("Retry-After")
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 1 {
		t.Fatalf("Retry-After = %q, want positive integer seconds", value)
	}
}
