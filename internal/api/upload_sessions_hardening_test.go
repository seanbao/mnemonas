package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/seanbao/mnemonas/internal/activity"
	"github.com/seanbao/mnemonas/internal/backup"
	"github.com/seanbao/mnemonas/internal/config"
	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/storage"
	"github.com/seanbao/mnemonas/internal/uploadsession"
)

func TestUploadSessionConcurrentCommitsShareOneTerminalOutcome(t *testing.T) {
	server, _, _ := newUploadSessionHardeningHarness(t)
	payload := []byte("concurrent upload payload")
	ready := createReadyUploadSessionForHardening(
		t,
		server,
		"/uploads/concurrent.txt",
		"concurrent-commit",
		payload,
	)

	commitReached := make(chan struct{})
	allowCommit := make(chan struct{})
	var closeCommitReached sync.Once
	var closeAllowCommit sync.Once
	t.Cleanup(func() { closeAllowCommit.Do(func() { close(allowCommit) }) })
	var commitHookCalls atomic.Int32
	server.beforeQuotaMutationCommit = func(operation string) {
		if operation != "upload_session_commit" {
			return
		}
		commitHookCalls.Add(1)
		closeCommitReached.Do(func() { close(commitReached) })
		<-allowCommit
	}

	type commitResult struct {
		response *httptest.ResponseRecorder
	}
	results := make(chan commitResult, 2)
	startCommit := func() {
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/upload-sessions/"+ready.Data.ID+"/commit",
			nil,
		)
		response := httptest.NewRecorder()
		go func() {
			server.Router().ServeHTTP(response, request)
			results <- commitResult{response: response}
		}()
	}

	startCommit()
	select {
	case <-commitReached:
	case <-time.After(5 * time.Second):
		t.Fatal("first commit did not reach the publication boundary")
	}
	startCommit()
	select {
	case result := <-results:
		t.Fatalf(
			"overlapping commit completed before the active commit; status=%d body=%s",
			result.response.Code,
			result.response.Body.String(),
		)
	case <-time.After(100 * time.Millisecond):
	}
	closeAllowCommit.Do(func() { close(allowCommit) })

	var terminal []uploadSessionE2ESuccess
	for range 2 {
		select {
		case result := <-results:
			if result.response.Code != http.StatusOK {
				t.Fatalf(
					"concurrent commit status=%d, want %d; body=%s",
					result.response.Code,
					http.StatusOK,
					result.response.Body.String(),
				)
			}
			terminal = append(terminal, decodeUploadSessionE2ESuccess(t, result.response))
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent commit did not complete")
		}
	}
	if commitHookCalls.Load() != 1 {
		t.Fatalf("publication boundary calls=%d, want 1", commitHookCalls.Load())
	}
	for index, result := range terminal {
		if result.Data.State != uploadsession.StateCommitted {
			t.Fatalf("terminal[%d] state=%q, want committed", index, result.Data.State)
		}
		if result.Data.ContentBLAKE3 == nil {
			t.Fatalf("terminal[%d] has no content digest", index)
		}
	}
	if *terminal[0].Data.ContentBLAKE3 != *terminal[1].Data.ContentBLAKE3 {
		t.Fatalf(
			"concurrent terminal digests differ: %q and %q",
			*terminal[0].Data.ContentBLAKE3,
			*terminal[1].Data.ContentBLAKE3,
		)
	}
}

func TestUploadSessionKnownPrePublicationFailureReturnsToReady(t *testing.T) {
	server, _, _ := newUploadSessionHardeningHarness(t)
	payload := []byte("retryable upload payload")
	ready := createReadyUploadSessionForHardening(
		t,
		server,
		"/uploads/retryable.txt",
		"retryable-commit",
		payload,
	)

	const stagingSlots = 4
	readers := make([]*apiBlockingUploadReader, stagingSlots)
	responses := make(chan *httptest.ResponseRecorder, stagingSlots)
	for index := range readers {
		readers[index] = newAPIBlockingUploadReader()
		t.Cleanup(readers[index].Release)
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/files/uploads/blocker-"+strconv.Itoa(index)+".bin",
			readers[index],
		)
		response := httptest.NewRecorder()
		go func() {
			server.handleUploadFile(response, request)
			responses <- response
		}()
		select {
		case <-readers[index].started:
		case <-time.After(5 * time.Second):
			t.Fatal("blocking upload did not occupy a staging slot")
		}
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/upload-sessions/"+ready.Data.ID+"/commit",
		nil,
	)
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf(
			"staging-full commit status=%d, want %d; body=%s",
			response.Code,
			http.StatusTooManyRequests,
			response.Body.String(),
		)
	}
	session, err := server.uploadSessions.Get(t.Context(), "anonymous", ready.Data.ID)
	if err != nil {
		t.Fatalf("Get(upload session) error: %v", err)
	}
	if session.State != uploadsession.StateReady {
		t.Fatalf("session state after pre-publication failure=%q, want ready", session.State)
	}

	for _, reader := range readers {
		reader.Release()
	}
	for range readers {
		select {
		case result := <-responses:
			if result.Code != http.StatusCreated {
				t.Fatalf(
					"blocking upload status=%d, want %d; body=%s",
					result.Code,
					http.StatusCreated,
					result.Body.String(),
				)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("blocking upload did not finish")
		}
	}
}

func TestUploadSessionCoordinatorResolvesInterruptedCommittingStates(t *testing.T) {
	server, fs, _ := newUploadSessionHardeningHarness(t)

	unchangedPayload := []byte("unchanged payload")
	unchanged := createReadyUploadSessionForHardening(
		t,
		server,
		"/uploads/unchanged.txt",
		"crash-before-publish",
		unchangedPayload,
	)
	if _, err := server.uploadSessions.BeginCommit(
		t.Context(),
		"anonymous",
		unchanged.Data.ID,
	); err != nil {
		t.Fatalf("BeginCommit(unchanged) error: %v", err)
	}

	publishedPayload := []byte("published payload")
	published := createReadyUploadSessionForHardening(
		t,
		server,
		"/uploads/published.txt",
		"crash-after-publish",
		publishedPayload,
	)
	if _, err := server.uploadSessions.BeginCommit(
		t.Context(),
		"anonymous",
		published.Data.ID,
	); err != nil {
		t.Fatalf("BeginCommit(published) error: %v", err)
	}
	if _, err := server.uploadSessions.MarkPublicationStarted(
		t.Context(),
		"anonymous",
		published.Data.ID,
	); err != nil {
		t.Fatalf("MarkPublicationStarted(published) error: %v", err)
	}
	if err := fs.WriteFile(
		t.Context(),
		published.Data.Path,
		bytes.NewReader(publishedPayload),
	); err != nil {
		t.Fatalf("WriteFile(published target) error: %v", err)
	}

	conflictPayload := []byte("session payload")
	conflicted := createReadyUploadSessionForHardening(
		t,
		server,
		"/uploads/conflicted.txt",
		"crash-with-target-change",
		conflictPayload,
	)
	if _, err := server.uploadSessions.BeginCommit(
		t.Context(),
		"anonymous",
		conflicted.Data.ID,
	); err != nil {
		t.Fatalf("BeginCommit(conflicted) error: %v", err)
	}
	if _, err := server.uploadSessions.MarkPublicationStarted(
		t.Context(),
		"anonymous",
		conflicted.Data.ID,
	); err != nil {
		t.Fatalf("MarkPublicationStarted(conflicted) error: %v", err)
	}
	if err := fs.WriteFile(
		t.Context(),
		conflicted.Data.Path,
		strings.NewReader("external payload"),
	); err != nil {
		t.Fatalf("WriteFile(conflicting target) error: %v", err)
	}

	reconciled, err := server.reconcileInterruptedUploadSessions(t.Context())
	if err != nil {
		t.Fatalf("reconcileInterruptedUploadSessions() error: %v", err)
	}
	if reconciled != 3 {
		t.Fatalf("reconciled sessions=%d, want 3", reconciled)
	}
	waitForUploadSessionStoreState(
		t,
		server,
		unchanged.Data.ID,
		uploadsession.StateReady,
	)
	waitForUploadSessionStoreState(
		t,
		server,
		published.Data.ID,
		uploadsession.StateCommitted,
	)
	waitForUploadSessionStoreState(
		t,
		server,
		conflicted.Data.ID,
		uploadsession.StateConflict,
	)
	if err := server.ensureUploadSessionActivity(uploadsession.Session{
		ID:    published.Data.ID,
		Owner: "anonymous",
		Path:  published.Data.Path,
	}); err != nil {
		t.Fatalf("ensureUploadSessionActivity(idempotent retry) error: %v", err)
	}
	entries, total := server.activity.List(10, 0, activity.ActionUpload, "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("recovered upload activity total=%d entries=%+v, want one", total, entries)
	}
	if entries[0].ID != "upload-session-"+published.Data.ID ||
		entries[0].User != "anonymous" ||
		entries[0].IP != "" ||
		entries[0].Path != published.Data.Path ||
		entries[0].Details["upload_session_id"] != published.Data.ID ||
		len(entries[0].Details) != 1 {
		t.Fatalf("unexpected recovered upload activity: %+v", entries[0])
	}
}

func TestUploadSessionRecoveryDistinguishesPublicationWindowFromMatchingContent(
	t *testing.T,
) {
	server, fs, _ := newUploadSessionHardeningHarness(t)

	externalPayload := []byte("matching external payload")
	external := createReadyUploadSessionForHardening(
		t,
		server,
		"/uploads/external-match.txt",
		"external-match-before-publication",
		externalPayload,
	)
	if _, err := server.uploadSessions.BeginCommit(
		t.Context(),
		"anonymous",
		external.Data.ID,
	); err != nil {
		t.Fatalf("BeginCommit(external match) error: %v", err)
	}
	if err := fs.WriteFile(
		t.Context(),
		external.Data.Path,
		bytes.NewReader(externalPayload),
	); err != nil {
		t.Fatalf("WriteFile(external matching target) error: %v", err)
	}

	originalPayload := []byte("already matching original payload")
	if err := fs.WriteFile(
		t.Context(),
		"/uploads/original-match.txt",
		bytes.NewReader(originalPayload),
	); err != nil {
		t.Fatalf("WriteFile(original matching target) error: %v", err)
	}
	original := createReadyUploadSessionForHardening(
		t,
		server,
		"/uploads/original-match.txt",
		"original-match-publication-started",
		originalPayload,
	)
	if _, err := server.uploadSessions.BeginCommit(
		t.Context(),
		"anonymous",
		original.Data.ID,
	); err != nil {
		t.Fatalf("BeginCommit(original match) error: %v", err)
	}
	if _, err := server.uploadSessions.MarkPublicationStarted(
		t.Context(),
		"anonymous",
		original.Data.ID,
	); err != nil {
		t.Fatalf("MarkPublicationStarted(original match) error: %v", err)
	}

	reconciled, err := server.reconcileInterruptedUploadSessions(t.Context())
	if err != nil {
		t.Fatalf("reconcileInterruptedUploadSessions() error: %v", err)
	}
	if reconciled != 2 {
		t.Fatalf("reconciled sessions=%d, want 2", reconciled)
	}
	waitForUploadSessionStoreState(
		t,
		server,
		external.Data.ID,
		uploadsession.StateConflict,
	)
	waitForUploadSessionStoreState(
		t,
		server,
		original.Data.ID,
		uploadsession.StateReady,
	)
	_, total := server.activity.List(10, 0, activity.ActionUpload, "")
	if total != 0 {
		t.Fatalf("matching pre-publication targets produced %d upload activities, want 0", total)
	}
}

func TestUploadSessionCommitHoldsMutationLeaseThroughCommittedTransition(t *testing.T) {
	server, _, _ := newUploadSessionHardeningHarness(t)
	ready := createReadyUploadSessionForHardening(
		t,
		server,
		"/uploads/live-lease.txt",
		"live-lease",
		[]byte("live lease payload"),
	)

	transitionReached := make(chan struct{})
	allowTransition := make(chan struct{})
	var reachOnce sync.Once
	var allowOnce sync.Once
	t.Cleanup(func() { allowOnce.Do(func() { close(allowTransition) }) })
	server.beforeUploadSessionTransition = func(state uploadsession.State) {
		if state != uploadsession.StateCommitted {
			return
		}
		reachOnce.Do(func() { close(transitionReached) })
		<-allowTransition
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/upload-sessions/"+ready.Data.ID+"/commit",
		nil,
	)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.Router().ServeHTTP(response, request)
		close(done)
	}()
	select {
	case <-transitionReached:
	case <-time.After(5 * time.Second):
		t.Fatal("commit did not reach the committed transition")
	}
	assertUploadSessionMutationLeaseHeld(t, server)
	allowOnce.Do(func() { close(allowTransition) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("commit did not finish after terminal transition resumed")
	}
	if response.Code != http.StatusOK {
		t.Fatalf(
			"commit status=%d, want %d; body=%s",
			response.Code,
			http.StatusOK,
			response.Body.String(),
		)
	}
}

func TestUploadSessionRecoveryHoldsMutationLeaseThroughCommittedTransition(
	t *testing.T,
) {
	server, fs, _ := newUploadSessionHardeningHarness(t)
	payload := []byte("recovery lease payload")
	ready := createReadyUploadSessionForHardening(
		t,
		server,
		"/uploads/recovery-lease.txt",
		"recovery-lease",
		payload,
	)
	if _, err := server.uploadSessions.BeginCommit(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	); err != nil {
		t.Fatalf("BeginCommit() error: %v", err)
	}
	if _, err := server.uploadSessions.MarkPublicationStarted(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	); err != nil {
		t.Fatalf("MarkPublicationStarted() error: %v", err)
	}
	if err := fs.WriteFile(t.Context(), ready.Data.Path, bytes.NewReader(payload)); err != nil {
		t.Fatalf("WriteFile(published target) error: %v", err)
	}

	transitionReached := make(chan struct{})
	allowTransition := make(chan struct{})
	var reachOnce sync.Once
	var allowOnce sync.Once
	t.Cleanup(func() { allowOnce.Do(func() { close(allowTransition) }) })
	server.beforeUploadSessionTransition = func(state uploadsession.State) {
		if state != uploadsession.StateCommitted {
			return
		}
		reachOnce.Do(func() { close(transitionReached) })
		<-allowTransition
	}
	result := make(chan error, 1)
	go func() {
		_, err := server.reconcileInterruptedUploadSessions(t.Context())
		result <- err
	}()
	select {
	case <-transitionReached:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not reach the committed transition")
	}
	assertUploadSessionMutationLeaseHeld(t, server)
	assertStorageMutationLeaseHeld(t, fs)
	allowOnce.Do(func() { close(allowTransition) })
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("reconcileInterruptedUploadSessions() error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not finish after terminal transition resumed")
	}
	waitForUploadSessionStoreState(
		t,
		server,
		ready.Data.ID,
		uploadsession.StateCommitted,
	)
}

func TestUploadSessionRecoveryWaitsForStorageRecoveryGate(t *testing.T) {
	server, fs, root := newUploadSessionHardeningHarness(t)
	payload := []byte("published while storage recovery remains pending")
	ready := createReadyUploadSessionForHardening(
		t,
		server,
		"/uploads/storage-recovery-gate.txt",
		"storage-recovery-gate",
		payload,
	)
	if _, err := server.uploadSessions.BeginCommit(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	); err != nil {
		t.Fatalf("BeginCommit() error: %v", err)
	}
	if _, err := server.uploadSessions.MarkPublicationStarted(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	); err != nil {
		t.Fatalf("MarkPublicationStarted() error: %v", err)
	}
	if err := fs.WriteFile(
		t.Context(),
		ready.Data.Path,
		bytes.NewReader(payload),
	); err != nil {
		t.Fatalf("WriteFile(published target) error: %v", err)
	}

	internalRoot := filepath.Join(root, ".mnemonas")
	recoveryEvidence := filepath.Join(
		internalRoot,
		"write-staging",
		".mnemonas-write-rollback-upload-session-test.tmp",
	)
	if err := os.WriteFile(recoveryEvidence, []byte("retained old target"), 0o600); err != nil {
		t.Fatalf("WriteFile(recovery evidence) error: %v", err)
	}
	if _, _, err := fs.CleanupStaging(t.Context()); !errors.Is(
		err,
		storage.ErrWriteRecoveryRequired,
	) {
		t.Fatalf("CleanupStaging() error=%v, want ErrWriteRecoveryRequired", err)
	}

	for _, request := range []*http.Request{
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/upload-sessions/"+ready.Data.ID,
			nil,
		),
		httptest.NewRequest(
			http.MethodPost,
			"/api/v1/upload-sessions/"+ready.Data.ID+"/commit",
			nil,
		),
	} {
		response := httptest.NewRecorder()
		server.Router().ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf(
				"%s %s status=%d, want 503; body=%s",
				request.Method,
				request.URL.Path,
				response.Code,
				response.Body.String(),
			)
		}
	}
	if reconciled, err := server.reconcileInterruptedUploadSessions(
		t.Context(),
	); reconciled != 0 || !errors.Is(err, storage.ErrWriteRecoveryRequired) {
		t.Fatalf(
			"reconcileInterruptedUploadSessions()=(%d, %v), want (0, ErrWriteRecoveryRequired)",
			reconciled,
			err,
		)
	}

	pending, err := server.uploadSessions.Get(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	)
	if err != nil {
		t.Fatalf("Get(pending session) error: %v", err)
	}
	if pending.State != uploadsession.StateCommitting || !pending.PublicationStarted {
		t.Fatalf("pending session changed while storage gate was active: %+v", pending)
	}
	payloadReader, _, err := server.uploadSessions.OpenPayload(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	)
	if err != nil {
		t.Fatalf("OpenPayload(pending session) error: %v", err)
	}
	retainedPayload, readErr := io.ReadAll(payloadReader)
	closeErr := payloadReader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read retained payload errors: read=%v close=%v", readErr, closeErr)
	}
	if !bytes.Equal(retainedPayload, payload) {
		t.Fatalf("retained payload=%q, want %q", retainedPayload, payload)
	}
	if _, total := server.activity.List(10, 0, activity.ActionUpload, ""); total != 0 {
		t.Fatalf("storage-gated recovery wrote %d upload activities, want 0", total)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("server Close() error: %v", err)
	}
	if err := fs.Close(); err != nil {
		t.Fatalf("filesystem Close() error: %v", err)
	}
	if err := os.Remove(recoveryEvidence); err != nil {
		t.Fatalf("Remove(recovery evidence) error: %v", err)
	}

	restartedClient := dataplane.NewClient("127.0.0.1:1")
	t.Cleanup(func() {
		if err := restartedClient.Close(); err != nil {
			t.Errorf("restarted dataplane client Close() error: %v", err)
		}
	})
	restartedFS, err := storage.New(&storage.Config{
		FilesRoot:          filepath.Join(root, "files"),
		InternalRoot:       internalRoot,
		TrashRoot:          filepath.Join(internalRoot, "trash"),
		TrashRetentionDays: 30,
		Dataplane:          restartedClient,
	})
	if err != nil {
		t.Fatalf("storage.New(restarted) error: %v", err)
	}
	t.Cleanup(func() {
		if err := restartedFS.Close(); err != nil {
			t.Errorf("restarted filesystem Close() error: %v", err)
		}
	})
	settings := config.Default()
	settings.Storage.Root = root
	restartedServer, err := NewServer(zerolog.Nop(), &ServerConfig{
		FileSystem:           restartedFS,
		ActivityRoot:         filepath.Join(internalRoot, "activity"),
		UploadSessionRoot:    filepath.Join(internalRoot, "upload-sessions"),
		Config:               settings,
		DeferBackgroundTasks: true,
	})
	if err != nil {
		t.Fatalf("NewServer(restarted) error: %v", err)
	}
	t.Cleanup(func() {
		if err := restartedServer.Close(); err != nil {
			t.Errorf("restarted server Close() error: %v", err)
		}
	})

	recovered, err := restartedServer.uploadSessions.Get(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	)
	if err != nil {
		t.Fatalf("Get(recovered session) error: %v", err)
	}
	if recovered.State != uploadsession.StateCommitted {
		t.Fatalf(
			"session state after storage recovery restart=%q, want committed",
			recovered.State,
		)
	}
	entries, total := restartedServer.activity.List(10, 0, activity.ActionUpload, "")
	if total != 1 || len(entries) != 1 ||
		entries[0].ID != "upload-session-"+ready.Data.ID {
		t.Fatalf("recovered upload activities total=%d entries=%+v, want one", total, entries)
	}
}

func TestUploadSessionRecoveryRejectsGateActivatedAfterEvidenceCapture(
	t *testing.T,
) {
	server, fs, root := newUploadSessionHardeningHarness(t)
	payload := []byte("published before a concurrent recovery gate")
	ready := createReadyUploadSessionForHardening(
		t,
		server,
		"/uploads/concurrent-storage-recovery-gate.txt",
		"concurrent-storage-recovery-gate",
		payload,
	)
	if _, err := server.uploadSessions.BeginCommit(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	); err != nil {
		t.Fatalf("BeginCommit() error: %v", err)
	}
	if _, err := server.uploadSessions.MarkPublicationStarted(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	); err != nil {
		t.Fatalf("MarkPublicationStarted() error: %v", err)
	}
	if err := fs.WriteFile(
		t.Context(),
		ready.Data.Path,
		bytes.NewReader(payload),
	); err != nil {
		t.Fatalf("WriteFile(published target) error: %v", err)
	}

	evidenceCaptured := make(chan struct{})
	allowFinalLease := make(chan struct{})
	var evidenceOnce sync.Once
	var allowOnce sync.Once
	t.Cleanup(func() { allowOnce.Do(func() { close(allowFinalLease) }) })
	server.afterUploadSessionRecoveryEvidence = func() {
		evidenceOnce.Do(func() { close(evidenceCaptured) })
		<-allowFinalLease
	}
	type reconcileResult struct {
		count int
		err   error
	}
	result := make(chan reconcileResult, 1)
	go func() {
		count, err := server.reconcileInterruptedUploadSessions(t.Context())
		result <- reconcileResult{count: count, err: err}
	}()
	select {
	case <-evidenceCaptured:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not finish the unlocked evidence capture")
	}

	recoveryEvidence := filepath.Join(
		root,
		".mnemonas",
		"write-staging",
		".mnemonas-write-rollback-concurrent-upload-test.tmp",
	)
	if err := os.WriteFile(recoveryEvidence, []byte("retained old target"), 0o600); err != nil {
		t.Fatalf("WriteFile(recovery evidence) error: %v", err)
	}
	if _, _, err := fs.CleanupStaging(t.Context()); !errors.Is(
		err,
		storage.ErrWriteRecoveryRequired,
	) {
		t.Fatalf("CleanupStaging() error=%v, want ErrWriteRecoveryRequired", err)
	}
	allowOnce.Do(func() { close(allowFinalLease) })

	select {
	case got := <-result:
		if got.count != 0 || !errors.Is(got.err, storage.ErrWriteRecoveryRequired) {
			t.Fatalf(
				"reconcileInterruptedUploadSessions()=(%d, %v), want (0, ErrWriteRecoveryRequired)",
				got.count,
				got.err,
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not return after the concurrent gate activated")
	}
	assertUploadSessionRecoveryRemainsPending(
		t,
		server,
		ready.Data.ID,
		payload,
	)
}

func TestUploadSessionRecoveryRejectsTargetDriftAfterEvidenceCapture(
	t *testing.T,
) {
	server, fs, _ := newUploadSessionHardeningHarness(t)
	payload := []byte("published payload sampled by recovery")
	externalPayload := []byte("newer external target")
	ready := createReadyUploadSessionForHardening(
		t,
		server,
		"/uploads/recovery-evidence-drift.bin",
		"recovery-evidence-drift",
		payload,
	)
	if _, err := server.uploadSessions.BeginCommit(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	); err != nil {
		t.Fatalf("BeginCommit() error: %v", err)
	}
	if _, err := server.uploadSessions.MarkPublicationStarted(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	); err != nil {
		t.Fatalf("MarkPublicationStarted() error: %v", err)
	}
	if err := fs.WriteFile(
		t.Context(),
		ready.Data.Path,
		bytes.NewReader(payload),
	); err != nil {
		t.Fatalf("WriteFile(published target) error: %v", err)
	}

	evidenceCaptured := make(chan struct{})
	allowFinalLease := make(chan struct{})
	var evidenceOnce sync.Once
	var allowOnce sync.Once
	t.Cleanup(func() { allowOnce.Do(func() { close(allowFinalLease) }) })
	server.afterUploadSessionRecoveryEvidence = func() {
		evidenceOnce.Do(func() { close(evidenceCaptured) })
		<-allowFinalLease
	}
	type reconcileResult struct {
		count int
		err   error
	}
	result := make(chan reconcileResult, 1)
	go func() {
		count, err := server.reconcileInterruptedUploadSessions(t.Context())
		result <- reconcileResult{count: count, err: err}
	}()
	select {
	case <-evidenceCaptured:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not finish the unlocked evidence capture")
	}
	if err := fs.WriteFile(
		t.Context(),
		ready.Data.Path,
		bytes.NewReader(externalPayload),
	); err != nil {
		t.Fatalf("WriteFile(concurrent external target) error: %v", err)
	}
	allowOnce.Do(func() { close(allowFinalLease) })

	select {
	case got := <-result:
		if got.count != 0 ||
			!errors.Is(got.err, errUploadSessionRecoveryTargetDrift) {
			t.Fatalf(
				"reconcileInterruptedUploadSessions()=(%d, %v), want target-drift error",
				got.count,
				got.err,
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not return after target evidence drifted")
	}
	assertUploadSessionRecoveryRemainsPending(
		t,
		server,
		ready.Data.ID,
		payload,
	)

	server.afterUploadSessionRecoveryEvidence = nil
	if reconciled, err := server.reconcileInterruptedUploadSessions(
		t.Context(),
	); err != nil || reconciled != 1 {
		t.Fatalf(
			"reconcileInterruptedUploadSessions(stable retry)=(%d, %v), want (1, nil)",
			reconciled,
			err,
		)
	}
	conflicted, err := server.uploadSessions.Get(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	)
	if err != nil {
		t.Fatalf("Get(conflicted session) error: %v", err)
	}
	if conflicted.State != uploadsession.StateConflict {
		t.Fatalf("session state after stable retry=%q, want conflict", conflicted.State)
	}
	if got := uploadSessionE2EReadTarget(t, fs, ready.Data.Path); !bytes.Equal(
		got,
		externalPayload,
	) {
		t.Fatalf("target after stable recovery=%q, want %q", got, externalPayload)
	}
	if _, total := server.activity.List(10, 0, activity.ActionUpload, ""); total != 0 {
		t.Fatalf("conflicted recovery wrote %d upload activities, want 0", total)
	}
}

func TestUploadSessionRecoveryDoesNotWaitForConcurrentCapacityCheck(
	t *testing.T,
) {
	server, fs, _ := newUploadSessionHardeningHarness(t)
	recoveryPayload := []byte("published recovery payload")
	recoverySession := createReadyUploadSessionForHardening(
		t,
		server,
		"/uploads/recovery-append-lock-order.bin",
		"recovery-append-lock-order",
		recoveryPayload,
	)
	if _, err := server.uploadSessions.BeginCommit(
		t.Context(),
		"anonymous",
		recoverySession.Data.ID,
	); err != nil {
		t.Fatalf("BeginCommit() error: %v", err)
	}
	recoveryStoreSession, err := server.uploadSessions.MarkPublicationStarted(
		t.Context(),
		"anonymous",
		recoverySession.Data.ID,
	)
	if err != nil {
		t.Fatalf("MarkPublicationStarted() error: %v", err)
	}
	if err := fs.WriteFile(
		t.Context(),
		recoverySession.Data.Path,
		bytes.NewReader(recoveryPayload),
	); err != nil {
		t.Fatalf("WriteFile(published target) error: %v", err)
	}
	appendSession := uploadSessionE2ECreate(
		t,
		server,
		"/uploads/concurrent-append.bin",
		1,
		"concurrent-append",
		http.StatusCreated,
	)

	capacityCheckReached := make(chan struct{})
	allowCapacityCheck := make(chan struct{})
	var capacityOnce sync.Once
	var allowOnce sync.Once
	t.Cleanup(func() { allowOnce.Do(func() { close(allowCapacityCheck) }) })
	originalGetDiskStats := getDiskStats
	getDiskStats = func(targetFS *storage.FileSystem) (*storage.DiskStats, error) {
		capacityOnce.Do(func() { close(capacityCheckReached) })
		<-allowCapacityCheck
		return originalGetDiskStats(targetFS)
	}
	t.Cleanup(func() { getDiskStats = originalGetDiskStats })

	appendResponse := httptest.NewRecorder()
	appendDone := make(chan struct{})
	go func() {
		server.Router().ServeHTTP(
			appendResponse,
			uploadSessionE2EPatchRequest(
				appendSession.Data.ID,
				0,
				"concurrent-append-chunk",
				[]byte("x"),
			),
		)
		close(appendDone)
	}()
	select {
	case <-capacityCheckReached:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent append did not reach its capacity check")
	}

	evidenceCaptured := make(chan struct{})
	var evidenceOnce sync.Once
	server.afterUploadSessionRecoveryEvidence = func() {
		evidenceOnce.Do(func() { close(evidenceCaptured) })
	}
	recoveryResult := make(chan error, 1)
	go func() {
		_, err := server.reconcileCommittingUploadSession(
			t.Context(),
			"anonymous",
			recoveryStoreSession,
		)
		recoveryResult <- err
	}()
	select {
	case <-evidenceCaptured:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not capture target evidence")
	}

	select {
	case err := <-recoveryResult:
		if err != nil {
			t.Fatalf("reconcileCommittingUploadSession() error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery waited for the concurrent capacity check")
	}
	waitForUploadSessionStoreState(
		t,
		server,
		recoverySession.Data.ID,
		uploadsession.StateCommitted,
	)
	select {
	case <-appendDone:
		t.Fatal("concurrent append unexpectedly finished while capacity check was blocked")
	default:
	}

	allowOnce.Do(func() { close(allowCapacityCheck) })
	select {
	case <-appendDone:
		if appendResponse.Code != http.StatusOK {
			t.Fatalf(
				"concurrent append status=%d, want 200; body=%s",
				appendResponse.Code,
				appendResponse.Body.String(),
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent append did not finish after capacity check resumed")
	}
}

func TestUploadSessionRecoveryWaitsForTrashRecoveryGate(t *testing.T) {
	server, fs, root := newUploadSessionHardeningHarness(t)
	payload := []byte("published while Trash recovery remains pending")
	ready := createReadyUploadSessionForHardening(
		t,
		server,
		"/uploads/trash-recovery-gate.txt",
		"trash-recovery-gate",
		payload,
	)
	if _, err := server.uploadSessions.BeginCommit(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	); err != nil {
		t.Fatalf("BeginCommit() error: %v", err)
	}
	if _, err := server.uploadSessions.MarkPublicationStarted(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	); err != nil {
		t.Fatalf("MarkPublicationStarted() error: %v", err)
	}
	if err := fs.WriteFile(
		t.Context(),
		ready.Data.Path,
		bytes.NewReader(payload),
	); err != nil {
		t.Fatalf("WriteFile(published target) error: %v", err)
	}

	trashRoot := filepath.Join(root, ".mnemonas", "trash")
	originalTrashRoot := trashRoot + "-original"
	if err := os.Rename(trashRoot, originalTrashRoot); err != nil {
		t.Fatalf("Rename(Trash root) error: %v", err)
	}
	if err := os.Mkdir(trashRoot, 0o700); err != nil {
		_ = os.Rename(originalTrashRoot, trashRoot)
		t.Fatalf("Mkdir(replacement Trash root) error: %v", err)
	}
	restored := false
	restoreTrashRoot := func() error {
		if restored {
			return nil
		}
		if err := os.RemoveAll(trashRoot); err != nil {
			return err
		}
		if err := os.Rename(originalTrashRoot, trashRoot); err != nil {
			return err
		}
		restored = true
		return nil
	}
	t.Cleanup(func() {
		_ = server.Close()
		_ = fs.Close()
		if err := restoreTrashRoot(); err != nil {
			t.Errorf("restore Trash root error: %v", err)
		}
	})

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/upload-sessions/"+ready.Data.ID,
		nil,
	)
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf(
			"Trash-gated GET status=%d, want 503; body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	assertUploadSessionRecoveryRemainsPending(
		t,
		server,
		ready.Data.ID,
		payload,
	)
	if err := server.Close(); err != nil {
		t.Fatalf("server Close() error: %v", err)
	}
	if err := fs.Close(); err != nil {
		t.Fatalf("filesystem Close() error: %v", err)
	}
	if err := restoreTrashRoot(); err != nil {
		t.Fatalf("restore Trash root error: %v", err)
	}
}

func TestUploadSessionPatchAppliesIdleDeadlinesDuringSlowRead(t *testing.T) {
	server, _, _ := newUploadSessionHardeningHarness(t)
	settings := server.currentConfig()
	settings.Server.ReadTimeout = time.Minute
	settings.Server.WriteTimeout = 2 * time.Minute
	server.storeConfig(settings)
	created := uploadSessionE2ECreate(
		t,
		server,
		"/uploads/slow.txt",
		1,
		"slow-patch",
		http.StatusCreated,
	)

	reader := newAPIBlockingUploadReader()
	t.Cleanup(reader.Release)
	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/upload-sessions/"+created.Data.ID,
		reader,
	)
	request.ContentLength = 1
	digest := sha256.Sum256([]byte("x"))
	request.Header.Set(uploadOffsetHeader, "0")
	request.Header.Set(uploadChunkIDHeader, "slow-chunk")
	request.Header.Set(uploadChunkSHA256Header, hex.EncodeToString(digest[:]))
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", created.Data.ID)
	request = request.WithContext(
		context.WithValue(request.Context(), chi.RouteCtxKey, routeContext),
	)
	response := &apiDeadlineRecordingWriter{}
	done := make(chan struct{})
	go func() {
		server.handleAppendUploadSession(response, request)
		close(done)
	}()

	select {
	case <-reader.started:
	case <-time.After(5 * time.Second):
		t.Fatal("PATCH did not begin reading the request body")
	}
	if len(response.readDeadlines) == 0 {
		t.Fatal("PATCH did not refresh the read deadline before a blocking read")
	}
	now := time.Now()
	if got := response.readDeadlines[0]; got.Before(now.Add(50*time.Second)) ||
		got.After(now.Add(70*time.Second)) {
		t.Fatalf("read deadline=%v, want approximately one minute from now", got)
	}
	reader.Release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("PATCH did not finish after the reader resumed")
	}
	if response.statusCode != http.StatusOK {
		t.Fatalf(
			"slow PATCH status=%d, want %d; body=%s",
			response.statusCode,
			http.StatusOK,
			response.body.String(),
		)
	}
	if len(response.writeDeadlines) == 0 {
		t.Fatal("PATCH did not refresh the write deadline")
	}
}

func TestUploadSessionPatchRejectsSmallNonFinalChunk(t *testing.T) {
	server, _, _ := newUploadSessionHardeningHarness(t)
	created := uploadSessionE2ECreate(
		t,
		server,
		"/uploads/small-non-final.txt",
		uploadSessionMinNonFinalBytes+1,
		"small-non-final",
		http.StatusCreated,
	)
	request := uploadSessionE2EPatchRequest(
		created.Data.ID,
		0,
		"small-chunk",
		[]byte("x"),
	)
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf(
			"small non-final PATCH status=%d, want %d; body=%s",
			response.Code,
			http.StatusBadRequest,
			response.Body.String(),
		)
	}
	session, err := server.uploadSessions.Get(t.Context(), "anonymous", created.Data.ID)
	if err != nil {
		t.Fatalf("Get(upload session) error: %v", err)
	}
	if session.DurableOffset != 0 || session.LastChunkID != "" {
		t.Fatalf(
			"rejected chunk changed durable state: offset=%d chunk_id=%q",
			session.DurableOffset,
			session.LastChunkID,
		)
	}
}

func TestUploadSessionPatchMapsMaxBytesErrorToPayloadTooLarge(t *testing.T) {
	server, _, _ := newUploadSessionHardeningHarness(t)
	maxChunkBytes := uploadsession.MaxChunkBytes()
	created := uploadSessionE2ECreate(
		t,
		server,
		"/uploads/oversized-chunk.txt",
		maxChunkBytes+1,
		"oversized-chunk",
		http.StatusCreated,
	)
	payload := bytes.Repeat([]byte("x"), int(maxChunkBytes)+1)
	digest := sha256.Sum256(payload[:maxChunkBytes])
	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/upload-sessions/"+created.Data.ID,
		bytes.NewReader(payload),
	)
	request.ContentLength = maxChunkBytes
	request.Header.Set(uploadOffsetHeader, "0")
	request.Header.Set(uploadChunkIDHeader, "oversized-chunk-1")
	request.Header.Set(uploadChunkSHA256Header, hex.EncodeToString(digest[:]))
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"oversized PATCH status=%d, want %d; body=%s",
			response.Code,
			http.StatusRequestEntityTooLarge,
			response.Body.String(),
		)
	}
	session, err := server.uploadSessions.Get(t.Context(), "anonymous", created.Data.ID)
	if err != nil {
		t.Fatalf("Get(upload session) error: %v", err)
	}
	if session.DurableOffset != 0 || session.LastChunkID != "" {
		t.Fatalf(
			"oversized chunk changed durable state: offset=%d chunk_id=%q",
			session.DurableOffset,
			session.LastChunkID,
		)
	}
}

func TestCreateUploadSessionMapsOversizedJSONToPayloadTooLarge(t *testing.T) {
	server, _, _ := newUploadSessionHardeningHarness(t)
	body := bytes.Repeat([]byte("x"), int(DefaultJSONRequestBodyLimit)+1)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/upload-sessions",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"oversized create status=%d, want %d; body=%s",
			response.Code,
			http.StatusRequestEntityTooLarge,
			response.Body.String(),
		)
	}
}

func TestUploadSessionPatchRejectsInsufficientPhysicalCapacityWithoutAdvancingOffset(
	t *testing.T,
) {
	originalGetDiskStats := getDiskStats
	getDiskStats = func(_ *storage.FileSystem) (*storage.DiskStats, error) {
		return &storage.DiskStats{AvailableBytes: 1024}, nil
	}
	t.Cleanup(func() { getDiskStats = originalGetDiskStats })

	server, _, _ := newUploadSessionHardeningHarnessWithMinFreeSpace(t, 1024)
	created := uploadSessionE2ECreate(
		t,
		server,
		"/uploads/capacity.txt",
		1,
		"capacity-check",
		http.StatusCreated,
	)
	request := uploadSessionE2EPatchRequest(
		created.Data.ID,
		0,
		"capacity-chunk",
		[]byte("x"),
	)
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	if response.Code != http.StatusInsufficientStorage {
		t.Fatalf(
			"capacity-rejected PATCH status=%d, want %d; body=%s",
			response.Code,
			http.StatusInsufficientStorage,
			response.Body.String(),
		)
	}
	apiError := decodeUploadSessionE2EError(t, response)
	if apiError.Code != "UPLOAD_STAGING_CAPACITY_EXCEEDED" ||
		apiError.Message != "upload staging capacity is insufficient" {
		t.Fatalf("unexpected staging capacity error: %+v", apiError)
	}
	session, err := server.uploadSessions.Get(t.Context(), "anonymous", created.Data.ID)
	if err != nil {
		t.Fatalf("Get(upload session) error: %v", err)
	}
	if session.DurableOffset != 0 || session.LastChunkID != "" {
		t.Fatalf(
			"capacity rejection changed durable state: offset=%d chunk_id=%q",
			session.DurableOffset,
			session.LastChunkID,
		)
	}
}

func TestNewServerUploadSessionStartupFailureClosesBackupManager(t *testing.T) {
	root := secureBackupAPITestTempDir(t)
	backupRoot := filepath.Join(root, "backup-state")
	storageRoot := filepath.Join(root, "storage")
	if err := os.MkdirAll(storageRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll(storage root) error: %v", err)
	}
	invalidUploadRoot := filepath.Join(root, "upload-sessions")
	if err := os.WriteFile(invalidUploadRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile(invalid upload root) error: %v", err)
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		BackupRoot:           backupRoot,
		StorageRoot:          storageRoot,
		UploadSessionRoot:    invalidUploadRoot,
		DeferBackgroundTasks: true,
	})
	if server != nil {
		t.Cleanup(func() { _ = server.Close() })
		t.Fatal("NewServer() returned a server for an invalid upload session root")
	}
	if err == nil || !strings.Contains(err.Error(), "failed to initialize upload session store") {
		t.Fatalf("NewServer() error=%v, want upload session initialization failure", err)
	}

	manager, err := backup.NewManager(backup.ManagerConfig{
		Root:        backupRoot,
		StorageRoot: storageRoot,
	})
	if err != nil {
		t.Fatalf("backup manager lock remained held after NewServer failure: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("backup manager Close() error: %v", err)
	}
}

func TestNewServerFailsClosedWhenUploadSessionRecoveryCannotLogActivity(
	t *testing.T,
) {
	server, fs, root := newUploadSessionHardeningHarness(t)
	payload := []byte("published before restart")
	ready := createReadyUploadSessionForHardening(
		t,
		server,
		"/uploads/startup-recovery.txt",
		"startup-recovery",
		payload,
	)
	if _, err := server.uploadSessions.BeginCommit(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	); err != nil {
		t.Fatalf("BeginCommit() error: %v", err)
	}
	if _, err := server.uploadSessions.MarkPublicationStarted(
		t.Context(),
		"anonymous",
		ready.Data.ID,
	); err != nil {
		t.Fatalf("MarkPublicationStarted() error: %v", err)
	}
	if err := fs.WriteFile(
		t.Context(),
		ready.Data.Path,
		bytes.NewReader(payload),
	); err != nil {
		t.Fatalf("WriteFile(published target) error: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("server Close() error: %v", err)
	}

	internalRoot := filepath.Join(root, ".mnemonas")
	uploadSessionRoot := filepath.Join(internalRoot, "upload-sessions")
	unavailableActivityRoot := filepath.Join(internalRoot, "unavailable-activity")
	if err := os.WriteFile(
		unavailableActivityRoot,
		[]byte("not a directory"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile(unavailable activity root) error: %v", err)
	}
	settings := config.Default()
	settings.Storage.Root = root
	reopened, err := NewServer(zerolog.Nop(), &ServerConfig{
		FileSystem:           fs,
		ActivityRoot:         unavailableActivityRoot,
		UploadSessionRoot:    uploadSessionRoot,
		Config:               settings,
		DeferBackgroundTasks: true,
	})
	if reopened != nil {
		t.Cleanup(func() { _ = reopened.Close() })
		t.Fatal("NewServer() returned a server after startup recovery failed")
	}
	if err == nil ||
		!strings.Contains(err.Error(), "reconcile interrupted upload sessions before startup") ||
		!strings.Contains(err.Error(), "activity log is configured but unavailable") {
		t.Fatalf("NewServer() error=%v, want fail-closed startup reconciliation error", err)
	}

	// The failed constructor must close the upload-session store it opened.
	store, err := uploadsession.Open(uploadSessionRoot, uploadsession.Options{})
	if err != nil {
		t.Fatalf("upload session store lock remained held after NewServer failure: %v", err)
	}
	session, getErr := store.Get(t.Context(), "anonymous", ready.Data.ID)
	closeErr := store.Close()
	if getErr != nil {
		t.Fatalf("Get(interrupted upload session) error: %v", getErr)
	}
	if closeErr != nil {
		t.Fatalf("upload session store Close() error: %v", closeErr)
	}
	if session.State != uploadsession.StateCommitting {
		t.Fatalf(
			"session state after failed startup recovery=%q, want %q",
			session.State,
			uploadsession.StateCommitting,
		)
	}
}

func newUploadSessionHardeningHarness(
	t *testing.T,
) (*Server, *storage.FileSystem, string) {
	t.Helper()
	return newUploadSessionHardeningHarnessWithMinFreeSpace(t, 0)
}

func newUploadSessionHardeningHarnessWithMinFreeSpace(
	t *testing.T,
	minFreeSpace uint64,
) (*Server, *storage.FileSystem, string) {
	t.Helper()
	root := t.TempDir()
	internalRoot := filepath.Join(root, ".mnemonas")
	client := dataplane.NewClient("127.0.0.1:1")
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("dataplane client Close() error: %v", err)
		}
	})
	fs, err := storage.New(&storage.Config{
		FilesRoot:          filepath.Join(root, "files"),
		InternalRoot:       internalRoot,
		TrashRoot:          filepath.Join(internalRoot, "trash"),
		TrashRetentionDays: 30,
		Dataplane:          client,
	})
	if err != nil {
		t.Fatalf("storage.New() error: %v", err)
	}
	t.Cleanup(func() {
		if err := fs.Close(); err != nil {
			t.Errorf("filesystem Close() error: %v", err)
		}
	})

	settings := config.Default()
	settings.Storage.Root = root
	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		FileSystem:                fs,
		ActivityRoot:              filepath.Join(internalRoot, "activity"),
		UploadSessionRoot:         filepath.Join(internalRoot, "upload-sessions"),
		UploadSessionMinFreeSpace: minFreeSpace,
		Config:                    settings,
		DeferBackgroundTasks:      true,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("server Close() error: %v", err)
		}
	})
	if err := fs.Mkdir(t.Context(), "/uploads"); err != nil {
		t.Fatalf("Mkdir(/uploads) error: %v", err)
	}
	return server, fs, root
}

func createReadyUploadSessionForHardening(
	t *testing.T,
	server *Server,
	targetPath string,
	clientRequestID string,
	payload []byte,
) uploadSessionE2ESuccess {
	t.Helper()
	created := uploadSessionE2ECreate(
		t,
		server,
		targetPath,
		int64(len(payload)),
		clientRequestID,
		http.StatusCreated,
	)
	ready := uploadSessionE2EPatch(
		t,
		server,
		created.Data.ID,
		0,
		clientRequestID+"-chunk",
		payload,
		http.StatusOK,
	)
	if ready.Data.State != uploadsession.StateReady {
		t.Fatalf("upload session state=%q, want ready", ready.Data.State)
	}
	session, err := server.uploadSessions.Get(t.Context(), "anonymous", created.Data.ID)
	if err != nil {
		t.Fatalf("Get(upload session) error: %v", err)
	}
	if session.LastChunkID != clientRequestID+"-chunk" {
		t.Fatalf(
			"stored chunk ID=%q, want %q",
			session.LastChunkID,
			clientRequestID+"-chunk",
		)
	}
	return ready
}

func waitForUploadSessionStoreState(
	t *testing.T,
	server *Server,
	id string,
	want uploadsession.State,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		session, err := server.uploadSessions.Get(t.Context(), "anonymous", id)
		if err != nil {
			t.Fatalf("Get(upload session %s) error: %v", id, err)
		}
		if session.State == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("upload session %s state=%q, want %q", id, session.State, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertUploadSessionRecoveryRemainsPending(
	t *testing.T,
	server *Server,
	id string,
	wantPayload []byte,
) {
	t.Helper()
	session, err := server.uploadSessions.Get(t.Context(), "anonymous", id)
	if err != nil {
		t.Fatalf("Get(pending upload session) error: %v", err)
	}
	if session.State != uploadsession.StateCommitting ||
		!session.PublicationStarted {
		t.Fatalf("pending upload session changed during recovery gate: %+v", session)
	}
	payloadReader, _, err := server.uploadSessions.OpenPayload(
		t.Context(),
		"anonymous",
		id,
	)
	if err != nil {
		t.Fatalf("OpenPayload(pending upload session) error: %v", err)
	}
	payload, readErr := io.ReadAll(payloadReader)
	closeErr := payloadReader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read pending upload payload errors: read=%v close=%v", readErr, closeErr)
	}
	if !bytes.Equal(payload, wantPayload) {
		t.Fatalf("pending upload payload=%q, want %q", payload, wantPayload)
	}
	if _, total := server.activity.List(10, 0, activity.ActionUpload, ""); total != 0 {
		t.Fatalf("recovery-gated upload wrote %d activities, want 0", total)
	}
}

func assertUploadSessionMutationLeaseHeld(t *testing.T, server *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	mutation, err := server.quotaReservations().AcquireMutation(ctx)
	if mutation != nil {
		mutation.Release()
		t.Fatal("acquired a mutation lease while upload session transition was blocked")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AcquireMutation() error=%v, want context deadline exceeded", err)
	}
}

func assertStorageMutationLeaseHeld(t *testing.T, fs *storage.FileSystem) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	lease, err := fs.AcquireMutationLease(ctx)
	if lease != nil {
		lease.Release()
		t.Fatal("acquired a storage mutation lease during upload recovery transition")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf(
			"storage AcquireMutationLease() error=%v, want context deadline exceeded",
			err,
		)
	}
}
