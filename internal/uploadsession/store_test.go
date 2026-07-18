package uploadsession

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/zeebo/blake3"
)

func TestStoreUploadCommitLifecycleAndIdempotency(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, Options{})
	ctx := context.Background()
	condition := OriginalCondition{
		ExpectedExists:      true,
		DeleteIdentityToken: "original-target",
	}
	created, err := store.Create(ctx, CreateRequest{
		Owner:             "alice",
		ClientRequestID:   "client-request-1",
		Path:              "/home/alice/file.txt",
		TotalBytes:        10,
		OriginalCondition: condition,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !created.Created || !validID(created.Session.ID) {
		t.Fatalf("Create() = %+v, want a new random session", created)
	}
	if created.Session.State != StateUploading || created.Session.DurableOffset != 0 {
		t.Fatalf("Create() session = %+v, want empty uploading state", created.Session)
	}

	lookup, err := store.GetByClientRequestID(ctx, "alice", "client-request-1")
	if err != nil {
		t.Fatalf("GetByClientRequestID() error = %v", err)
	}
	if lookup.ID != created.Session.ID {
		t.Fatalf("GetByClientRequestID() ID = %q, want %q", lookup.ID, created.Session.ID)
	}
	if _, err := store.GetByClientRequestID(ctx, "bob", "client-request-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner GetByClientRequestID() error = %v, want ErrNotFound", err)
	}

	replayedCreate, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "client-request-1",
		Path:            "/home/alice/file.txt",
		TotalBytes:      10,
		OriginalCondition: OriginalCondition{
			ExpectedExists: false,
		},
	})
	if err != nil {
		t.Fatalf("idempotent Create() error = %v", err)
	}
	if replayedCreate.Created || replayedCreate.Session.ID != created.Session.ID {
		t.Fatalf("idempotent Create() = %+v, want original session", replayedCreate)
	}
	if replayedCreate.Session.OriginalCondition != condition {
		t.Fatalf(
			"idempotent Create() condition = %+v, want captured %+v",
			replayedCreate.Session.OriginalCondition,
			condition,
		)
	}
	_, err = store.Create(ctx, CreateRequest{
		Owner:             "alice",
		ClientRequestID:   "client-request-1",
		Path:              "/home/alice/other.txt",
		TotalBytes:        10,
		OriginalCondition: condition,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting Create() error = %v, want ErrConflict", err)
	}

	first := []byte("hello")
	firstResult, err := store.AppendChunk(ctx, appendRequest(
		"alice",
		created.Session.ID,
		0,
		first,
	))
	if err != nil {
		t.Fatalf("first AppendChunk() error = %v", err)
	}
	if firstResult.Replayed || firstResult.Session.DurableOffset != 5 ||
		firstResult.Session.State != StateUploading {
		t.Fatalf("first AppendChunk() = %+v", firstResult)
	}
	firstReplay, err := store.AppendChunk(ctx, appendRequest(
		"alice",
		created.Session.ID,
		0,
		first,
	))
	if err != nil {
		t.Fatalf("replayed first AppendChunk() error = %v", err)
	}
	if !firstReplay.Replayed || firstReplay.Session.Revision != firstResult.Session.Revision {
		t.Fatalf("replayed first AppendChunk() = %+v, want same revision", firstReplay)
	}
	_, err = store.AppendChunk(ctx, appendRequest(
		"alice",
		created.Session.ID,
		2,
		[]byte("x"),
	))
	var offsetErr *OffsetMismatchError
	if !errors.As(err, &offsetErr) || offsetErr.Expected != 5 {
		t.Fatalf("out-of-order AppendChunk() error = %v, want expected offset 5", err)
	}

	second := []byte("world")
	ready, err := store.AppendChunk(ctx, appendRequest(
		"alice",
		created.Session.ID,
		5,
		second,
	))
	if err != nil {
		t.Fatalf("final AppendChunk() error = %v", err)
	}
	wantPayload := append(append([]byte(nil), first...), second...)
	wantBLAKE3 := blake3Hex(wantPayload)
	if ready.Session.State != StateReady ||
		ready.Session.DurableOffset != int64(len(wantPayload)) ||
		ready.Session.ContentBLAKE3 != wantBLAKE3 {
		t.Fatalf("final AppendChunk() = %+v, want ready payload", ready)
	}
	finalReplay, err := store.AppendChunk(ctx, appendRequest(
		"alice",
		created.Session.ID,
		5,
		second,
	))
	if err != nil {
		t.Fatalf("replayed final AppendChunk() error = %v", err)
	}
	if !finalReplay.Replayed || finalReplay.Session.Revision != ready.Session.Revision {
		t.Fatalf("replayed final AppendChunk() = %+v, want same ready revision", finalReplay)
	}

	payload, payloadSession, err := store.OpenPayload(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("OpenPayload() error = %v", err)
	}
	gotPayload, readErr := io.ReadAll(payload)
	closeErr := payload.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read OpenPayload(): %v", errors.Join(readErr, closeErr))
	}
	if !bytes.Equal(gotPayload, wantPayload) || payloadSession.ContentBLAKE3 != wantBLAKE3 {
		t.Fatalf("OpenPayload() payload = %q, session = %+v", gotPayload, payloadSession)
	}

	committing, err := store.BeginCommit(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("BeginCommit() error = %v", err)
	}
	if committing.State != StateCommitting {
		t.Fatalf("BeginCommit() state = %q, want committing", committing.State)
	}
	replayedCommit, err := store.BeginCommit(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("replayed BeginCommit() error = %v", err)
	}
	if replayedCommit.Revision != committing.Revision {
		t.Fatalf("replayed BeginCommit() revision = %d, want %d", replayedCommit.Revision, committing.Revision)
	}
	committed, err := store.MarkCommitted(ctx, "alice", created.Session.ID, CommitResult{
		ContentBLAKE3:      wantBLAKE3,
		PersistenceWarning: true,
	})
	if err != nil {
		t.Fatalf("MarkCommitted() error = %v", err)
	}
	if committed.State != StateCommitted || !committed.PersistenceWarning {
		t.Fatalf("MarkCommitted() = %+v", committed)
	}
	repeatedCommitted, err := store.MarkCommitted(ctx, "alice", created.Session.ID, CommitResult{
		ContentBLAKE3:      wantBLAKE3,
		PersistenceWarning: true,
	})
	if err != nil {
		t.Fatalf("replayed MarkCommitted() error = %v", err)
	}
	if repeatedCommitted.Revision != committed.Revision {
		t.Fatalf("replayed MarkCommitted() revision = %d, want %d", repeatedCommitted.Revision, committed.Revision)
	}
	if _, err := store.Cancel(ctx, "alice", created.Session.ID); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Cancel(committed) error = %v, want ErrInvalidState", err)
	}
}

func TestStoreCancelPersistsTombstoneAndCleanupRemovesIt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	root := filepath.Join(t.TempDir(), "sessions")
	options := Options{Now: func() time.Time { return now }}
	store, err := Open(root, options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ctx := context.Background()
	created, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "cancel-1",
		Path:            "/home/alice/partial.bin",
		TotalBytes:      8,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appended, err := store.AppendChunk(
		ctx,
		appendRequest("alice", created.Session.ID, 0, []byte("part")),
	)
	if err != nil {
		t.Fatalf("AppendChunk() error = %v", err)
	}
	cancelled, err := store.Cancel(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if cancelled.State != StateCancelled ||
		cancelled.DurableOffset != appended.Session.DurableOffset ||
		cancelled.Revision != appended.Session.Revision+1 {
		t.Fatalf("Cancel() = %+v, want durable cancelled tombstone", cancelled)
	}
	payloadPath := filepath.Join(root, created.Session.ID, payloadFileName)
	if _, err := os.Lstat(payloadPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled payload Lstat() error = %v, want not exist", err)
	}
	repeated, err := store.Cancel(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("replayed Cancel() error = %v", err)
	}
	if repeated.State != StateCancelled || repeated.Revision != cancelled.Revision {
		t.Fatalf("replayed Cancel() = %+v, want same tombstone", repeated)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	store, err = Open(root, options)
	if err != nil {
		t.Fatalf("reopen cancelled store: %v", err)
	}
	defer store.Close()
	recovered, err := store.GetByClientRequestID(ctx, "alice", "cancel-1")
	if err != nil {
		t.Fatalf("GetByClientRequestID() after reopen error = %v", err)
	}
	if recovered.State != StateCancelled || recovered.Revision != cancelled.Revision {
		t.Fatalf("recovered tombstone = %+v, want cancelled revision %d", recovered, cancelled.Revision)
	}

	now = cancelled.ExpiresAt.Add(time.Nanosecond)
	cleaned, err := store.CleanupExpired(ctx, now)
	if err != nil {
		t.Fatalf("CleanupExpired() error = %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("CleanupExpired() cleaned = %d, want 1", cleaned)
	}
	if _, err := store.Get(ctx, "alice", created.Session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after cleanup error = %v, want ErrNotFound", err)
	}
	if _, err := store.GetByClientRequestID(ctx, "alice", "cancel-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByClientRequestID() after cleanup error = %v, want ErrNotFound", err)
	}
}

func TestStoreRecoveryTruncatesOnlyBytesBeyondDurableOffset(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "sessions")
	store, err := Open(root, Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	created, err := store.Create(context.Background(), CreateRequest{
		Owner:           "alice",
		ClientRequestID: "recover-1",
		Path:            "/home/alice/recover.bin",
		TotalBytes:      6,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.AppendChunk(
		context.Background(),
		appendRequest("alice", created.Session.ID, 0, []byte("abc")),
	); err != nil {
		t.Fatalf("AppendChunk() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	payloadPath := filepath.Join(root, created.Session.ID, payloadFileName)
	payload, err := os.OpenFile(payloadPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("open payload for crash-tail simulation: %v", err)
	}
	if _, err := payload.Write([]byte("unpublished")); err != nil {
		_ = payload.Close()
		t.Fatalf("append unpublished payload tail: %v", err)
	}
	if err := payload.Sync(); err != nil {
		_ = payload.Close()
		t.Fatalf("sync unpublished payload tail: %v", err)
	}
	if err := payload.Close(); err != nil {
		t.Fatalf("close unpublished payload tail: %v", err)
	}

	store, err = Open(root, Options{})
	if err != nil {
		t.Fatalf("Open() with oversized recoverable payload error = %v", err)
	}
	info, err := os.Stat(payloadPath)
	if err != nil {
		t.Fatalf("Stat() recovered payload error = %v", err)
	}
	if info.Size() != 3 {
		t.Fatalf("recovered payload size = %d, want durable offset 3", info.Size())
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() recovered store error = %v", err)
	}

	if err := os.Truncate(payloadPath, 2); err != nil {
		t.Fatalf("truncate payload below durable offset: %v", err)
	}
	if _, err := Open(root, Options{}); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("Open() with undersized payload error = %v, want ErrRecoveryRequired", err)
	}
}

func TestStoreCreateIsConcurrentAndOwnerScoped(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, Options{})
	const workers = 12
	results := make(chan CreateResult, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := store.Create(context.Background(), CreateRequest{
				Owner:           "alice",
				ClientRequestID: "concurrent-1",
				Path:            "/home/alice/concurrent.bin",
				TotalBytes:      123,
			})
			results <- result
			errs <- err
		}()
	}
	group.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Create() error = %v", err)
		}
	}
	var (
		id           string
		createdCount int
	)
	for result := range results {
		if id == "" {
			id = result.Session.ID
		}
		if result.Session.ID != id {
			t.Fatalf("concurrent Create() ID = %q, want %q", result.Session.ID, id)
		}
		if result.Created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("concurrent Create() created count = %d, want 1", createdCount)
	}

	otherOwner, err := store.Create(context.Background(), CreateRequest{
		Owner:           "bob",
		ClientRequestID: "concurrent-1",
		Path:            "/home/bob/concurrent.bin",
		TotalBytes:      123,
	})
	if err != nil {
		t.Fatalf("same request ID for another owner Create() error = %v", err)
	}
	if otherOwner.Session.ID == id {
		t.Fatal("owner-scoped request IDs unexpectedly shared a session")
	}
}

func TestStoreBoundsAndExclusiveLock(t *testing.T) {
	t.Parallel()

	for _, root := range []string{"", " \t\n"} {
		if _, err := Open(root, Options{}); err == nil {
			t.Fatalf("Open(%q) unexpectedly accepted an empty store root", root)
		}
	}

	root := filepath.Join(t.TempDir(), "sessions")
	store, err := Open(root, Options{
		MaxSessionsPerOwner: 1,
		MaxSessions:         2,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	if _, err := Open(root, Options{}); err == nil {
		t.Fatal("second Open() unexpectedly acquired an exclusive store lock")
	}
	first, err := store.Create(context.Background(), CreateRequest{
		Owner:           "alice",
		ClientRequestID: "limit-1",
		Path:            "/home/alice/one.bin",
		TotalBytes:      1,
	})
	if err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	_, err = store.Create(context.Background(), CreateRequest{
		Owner:           "alice",
		ClientRequestID: "limit-2",
		Path:            "/home/alice/two.bin",
		TotalBytes:      1,
	})
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("per-owner limited Create() error = %v, want ErrLimitExceeded", err)
	}
	oversized := AppendChunkRequest{
		Owner:       "alice",
		ID:          first.Session.ID,
		Offset:      0,
		Length:      MaxChunkBytes() + 1,
		ChunkSHA256: sha256Hex(nil),
		Body:        bytes.NewReader(nil),
	}
	if _, err := store.AppendChunk(context.Background(), oversized); !errors.Is(err, ErrChunkTooLarge) {
		t.Fatalf("oversized AppendChunk() error = %v, want ErrChunkTooLarge", err)
	}
}

func TestStoreStateChainCompactsAndRecoversConsecutiveTail(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "sessions")
	store, err := Open(root, Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ctx := context.Background()
	created, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "compact-1",
		Path:            "/home/alice/compact.bin",
		TotalBytes:      5,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for offset, value := range []byte{'a', 'b'} {
		if _, err := store.AppendChunk(
			ctx,
			appendRequest("alice", created.Session.ID, int64(offset), []byte{value}),
		); err != nil {
			t.Fatalf("AppendChunk(offset=%d) error = %v", offset, err)
		}
	}
	sessionDir := filepath.Join(root, created.Session.ID)
	stateTwoPath := filepath.Join(sessionDir, stateFileName(2))
	stateTwo, err := os.ReadFile(stateTwoPath)
	if err != nil {
		t.Fatalf("ReadFile(state revision 2) error = %v", err)
	}
	if _, err := store.AppendChunk(
		ctx,
		appendRequest("alice", created.Session.ID, 2, []byte{'c'}),
	); err != nil {
		t.Fatalf("third AppendChunk() error = %v", err)
	}
	assertStateRevisions(t, sessionDir, []uint64{3, 4})
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Simulate a crash before an older, already superseded revision was
	// compacted. Recovery must accept and compact the consecutive tail.
	if err := os.WriteFile(stateTwoPath, stateTwo, 0o600); err != nil {
		t.Fatalf("restore stale state revision 2: %v", err)
	}
	if err := os.Chmod(stateTwoPath, 0o600); err != nil {
		t.Fatalf("chmod stale state revision 2: %v", err)
	}
	store, err = Open(root, Options{})
	if err != nil {
		t.Fatalf("Open() consecutive state tail error = %v", err)
	}
	assertStateRevisions(t, sessionDir, []uint64{3, 4})
	recovered, err := store.Get(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("Get() recovered state error = %v", err)
	}
	if recovered.Revision != 4 || recovered.DurableOffset != 3 {
		t.Fatalf("recovered session = %+v, want revision 4 offset 3", recovered)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() recovered store error = %v", err)
	}

	// Simulate a crash after a fully synced pending state was created but
	// before its final rename.
	finalState := filepath.Join(sessionDir, stateFileName(4))
	pendingState := filepath.Join(
		sessionDir,
		pendingStateFileName(4, "00000000000000000000000000000000"),
	)
	if err := os.Rename(finalState, pendingState); err != nil {
		t.Fatalf("rename final state to pending crash artifact: %v", err)
	}
	store, err = Open(root, Options{})
	if err != nil {
		t.Fatalf("Open() pending state recovery error = %v", err)
	}
	defer store.Close()
	assertStateRevisions(t, sessionDir, []uint64{3, 4})
	if _, err := os.Lstat(pendingState); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending state Lstat() error = %v, want not exist", err)
	}
	continued, err := store.AppendChunk(
		ctx,
		appendRequest("alice", created.Session.ID, 3, []byte{'d'}),
	)
	if err != nil {
		t.Fatalf("AppendChunk() after tail recovery error = %v", err)
	}
	if continued.Session.Revision != 5 || continued.Session.DurableOffset != 4 {
		t.Fatalf("continued session = %+v, want revision 5 offset 4", continued.Session)
	}
	assertStateRevisions(t, sessionDir, []uint64{4, 5})
}

func TestStoreChunkIDIsDurableReplayIdentity(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "sessions")
	store, err := Open(root, Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ctx := context.Background()
	created, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "chunk-id-1",
		Path:            "/home/alice/chunk-id.bin",
		TotalBytes:      2,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	request := appendRequest("alice", created.Session.ID, 0, []byte{'a'})
	appended, err := store.AppendChunk(ctx, request)
	if err != nil {
		t.Fatalf("AppendChunk() error = %v", err)
	}
	if appended.Session.LastChunkID != request.ChunkID {
		t.Fatalf(
			"AppendChunk() LastChunkID = %q, want %q",
			appended.Session.LastChunkID,
			request.ChunkID,
		)
	}

	conflicting := appendRequest("alice", created.Session.ID, 0, []byte{'b'})
	conflicting.ChunkID = request.ChunkID
	if _, err := store.AppendChunk(ctx, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused ChunkID error = %v, want ErrConflict", err)
	}
	differentID := appendRequest("alice", created.Session.ID, 0, []byte{'a'})
	differentID.ChunkID = "different-retry-id"
	var offsetErr *OffsetMismatchError
	if _, err := store.AppendChunk(ctx, differentID); !errors.As(err, &offsetErr) {
		t.Fatalf("different ChunkID replay error = %v, want OffsetMismatchError", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	store, err = Open(root, Options{})
	if err != nil {
		t.Fatalf("Open() after ChunkID persistence error = %v", err)
	}
	defer store.Close()
	request = appendRequest("alice", created.Session.ID, 0, []byte{'a'})
	replayed, err := store.AppendChunk(ctx, request)
	if err != nil {
		t.Fatalf("AppendChunk() durable replay error = %v", err)
	}
	if !replayed.Replayed || replayed.Session.LastChunkID != request.ChunkID {
		t.Fatalf("durable replay = %+v, want matching ChunkID replay", replayed)
	}
}

func TestStorePublicationStartedPersistsAndMarkReadyClearsIt(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "sessions")
	store, err := Open(root, Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ctx := context.Background()
	created, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "publication-started-1",
		Path:            "/home/alice/publication.bin",
		TotalBytes:      0,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	committing, err := store.BeginCommit(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("BeginCommit() error = %v", err)
	}
	if committing.PublicationStarted {
		t.Fatalf("BeginCommit() = %+v, want pre-publication evidence", committing)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(pre-publication) error = %v", err)
	}

	store, err = Open(root, Options{})
	if err != nil {
		t.Fatalf("Open(pre-publication) error = %v", err)
	}
	recovered, err := store.Get(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("Get(pre-publication) error = %v", err)
	}
	if recovered.State != StateCommitting || recovered.PublicationStarted {
		t.Fatalf("recovered pre-publication session = %+v", recovered)
	}
	started, err := store.MarkPublicationStarted(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("MarkPublicationStarted() error = %v", err)
	}
	if !started.PublicationStarted || started.Revision != recovered.Revision+1 {
		t.Fatalf("MarkPublicationStarted() = %+v, want durable new revision", started)
	}
	replayed, err := store.MarkPublicationStarted(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("replayed MarkPublicationStarted() error = %v", err)
	}
	if replayed.Revision != started.Revision || !replayed.PublicationStarted {
		t.Fatalf("replayed MarkPublicationStarted() = %+v, want same revision", replayed)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(publication-started) error = %v", err)
	}

	store, err = Open(root, Options{})
	if err != nil {
		t.Fatalf("Open(publication-started) error = %v", err)
	}
	defer store.Close()
	recovered, err = store.Get(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("Get(publication-started) error = %v", err)
	}
	if recovered.State != StateCommitting || !recovered.PublicationStarted ||
		recovered.Revision != started.Revision {
		t.Fatalf("recovered publication-started session = %+v", recovered)
	}
	ready, err := store.MarkReady(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("MarkReady() error = %v", err)
	}
	if ready.State != StateReady || ready.PublicationStarted {
		t.Fatalf("MarkReady() = %+v, want ready without publication evidence", ready)
	}
	if _, err := store.MarkPublicationStarted(
		ctx,
		"alice",
		created.Session.ID,
	); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("MarkPublicationStarted(ready) error = %v, want ErrInvalidState", err)
	}
}

func TestStoreTerminalStatesRemovePayloadAndRecoverWithoutIt(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "sessions")
	store, err := Open(root, Options{
		MaxStagedBytesPerOwner: 1,
		MaxStagedBytes:         1,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ctx := context.Background()

	committedCreate, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "terminal-commit",
		Path:            "/home/alice/committed.bin",
		TotalBytes:      1,
	})
	if err != nil {
		t.Fatalf("Create(committed) error = %v", err)
	}
	committedReady, err := store.AppendChunk(
		ctx,
		appendRequest("alice", committedCreate.Session.ID, 0, []byte{'a'}),
	)
	if err != nil {
		t.Fatalf("AppendChunk(committed) error = %v", err)
	}
	if _, err := store.BeginCommit(ctx, "alice", committedCreate.Session.ID); err != nil {
		t.Fatalf("BeginCommit() error = %v", err)
	}
	committed, err := store.MarkCommitted(
		ctx,
		"alice",
		committedCreate.Session.ID,
		CommitResult{ContentBLAKE3: committedReady.Session.ContentBLAKE3},
	)
	if err != nil {
		t.Fatalf("MarkCommitted() error = %v", err)
	}
	assertPayloadMissing(t, root, committed.ID)

	conflictCreate, err := store.Create(ctx, CreateRequest{
		Owner:           "bob",
		ClientRequestID: "terminal-conflict",
		Path:            "/home/bob/conflict.bin",
		TotalBytes:      1,
	})
	if err != nil {
		t.Fatalf("Create(conflict) error = %v", err)
	}
	if _, err := store.AppendChunk(
		ctx,
		appendRequest("bob", conflictCreate.Session.ID, 0, []byte{'b'}),
	); err != nil {
		t.Fatalf("AppendChunk(conflict) error = %v", err)
	}
	conflicted, err := store.MarkConflict(ctx, "bob", conflictCreate.Session.ID, "target changed")
	if err != nil {
		t.Fatalf("MarkConflict() error = %v", err)
	}
	assertPayloadMissing(t, root, conflicted.ID)
	store.stagingMu.Lock()
	stagedBytes := store.stagedBytes
	store.stagingMu.Unlock()
	if stagedBytes != 0 {
		t.Fatalf("staged bytes after terminal cleanup = %d, want 0", stagedBytes)
	}

	crashCreate, err := store.Create(ctx, CreateRequest{
		Owner:           "carol",
		ClientRequestID: "terminal-crash",
		Path:            "/home/carol/crash.bin",
		TotalBytes:      1,
	})
	if err != nil {
		t.Fatalf("Create(crash terminal) error = %v", err)
	}
	if _, err := store.AppendChunk(
		ctx,
		appendRequest("carol", crashCreate.Session.ID, 0, []byte{'c'}),
	); err != nil {
		t.Fatalf("AppendChunk(crash terminal) error = %v", err)
	}
	// Simulate a process crash after the terminal state was synced but before
	// the payload cleanup phase ran.
	crashLock := store.sessionLock(crashCreate.Session.ID)
	crashLock.Lock()
	loaded, err := store.loadOwnedSession("carol", crashCreate.Session.ID)
	if err != nil {
		crashLock.Unlock()
		t.Fatalf("load crash terminal session: %v", err)
	}
	crashState := loaded.state
	crashState.State = StateConflict
	crashState.ConflictReason = "simulated post-state crash"
	crashState.UpdatedAt = store.now()
	crashState.ExpiresAt = crashState.UpdatedAt.Add(store.opts.TTL)
	crashPersisted, err := store.writeNextState(loaded, crashState)
	if err == nil {
		store.publishSnapshot(crashPersisted.state)
	}
	crashLock.Unlock()
	if err != nil {
		t.Fatalf("persist simulated crash terminal state: %v", err)
	}
	crashedConflict := publicSession(crashPersisted.state)
	if _, err := os.Stat(filepath.Join(
		root,
		crashCreate.Session.ID,
		payloadFileName,
	)); err != nil {
		t.Fatalf("crash terminal payload should still exist: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	store, err = Open(root, Options{
		MaxStagedBytesPerOwner: 1,
		MaxStagedBytes:         1,
	})
	if err != nil {
		t.Fatalf("Open() terminal states without payload error = %v", err)
	}
	defer store.Close()
	for owner, session := range map[string]Session{
		"alice": committed,
		"bob":   conflicted,
		"carol": crashedConflict,
	} {
		recovered, err := store.Get(ctx, owner, session.ID)
		if err != nil {
			t.Fatalf("Get(%s terminal) error = %v", owner, err)
		}
		if recovered.State != session.State || recovered.Revision != session.Revision {
			t.Fatalf("Get(%s terminal) = %+v, want %+v", owner, recovered, session)
		}
		assertPayloadMissing(t, root, session.ID)
	}
	store.stagingMu.Lock()
	stagedBytes = store.stagedBytes
	store.stagingMu.Unlock()
	if stagedBytes != 0 {
		t.Fatalf("recovered staged bytes after terminal cleanup = %d, want 0", stagedBytes)
	}
}

func TestStoreIdempotentTerminalRetryRepeatsFailedPayloadCleanup(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "sessions")
	store, err := Open(root, Options{
		MaxStagedBytesPerOwner: 2,
		MaxStagedBytes:         2,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	conflictCreate, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "retry-conflict-cleanup",
		Path:            "/home/alice/conflict.bin",
		TotalBytes:      1,
	})
	if err != nil {
		t.Fatalf("Create(conflict) error = %v", err)
	}
	if _, err := store.AppendChunk(
		ctx,
		appendRequest("alice", conflictCreate.Session.ID, 0, []byte{'a'}),
	); err != nil {
		t.Fatalf("AppendChunk(conflict) error = %v", err)
	}
	conflictPayload := filepath.Join(root, conflictCreate.Session.ID, payloadFileName)
	if err := os.Chmod(conflictPayload, 0o640); err != nil {
		t.Fatalf("chmod conflict payload failure injection: %v", err)
	}
	if _, err := store.MarkConflict(
		ctx,
		"alice",
		conflictCreate.Session.ID,
		"target changed",
	); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("MarkConflict() cleanup failure error = %v, want ErrRecoveryRequired", err)
	}
	store.mu.RLock()
	conflictSnapshot := store.sessions[conflictCreate.Session.ID]
	store.mu.RUnlock()
	if conflictSnapshot.State != StateConflict {
		t.Fatalf("persisted conflict snapshot = %+v, want conflict", conflictSnapshot)
	}
	if err := os.Chmod(conflictPayload, 0o600); err != nil {
		t.Fatalf("restore conflict payload permissions: %v", err)
	}
	retriedConflict, err := store.MarkConflict(
		ctx,
		"alice",
		conflictCreate.Session.ID,
		"target changed",
	)
	if err != nil {
		t.Fatalf("idempotent MarkConflict() cleanup retry error = %v", err)
	}
	if retriedConflict.State != StateConflict ||
		retriedConflict.Revision != conflictSnapshot.Revision {
		t.Fatalf(
			"idempotent MarkConflict() = %+v, want unchanged conflict revision",
			retriedConflict,
		)
	}
	assertPayloadMissing(t, root, conflictCreate.Session.ID)

	cancelCreate, err := store.Create(ctx, CreateRequest{
		Owner:           "bob",
		ClientRequestID: "retry-cancel-cleanup",
		Path:            "/home/bob/cancel.bin",
		TotalBytes:      2,
	})
	if err != nil {
		t.Fatalf("Create(cancel) error = %v", err)
	}
	if _, err := store.AppendChunk(
		ctx,
		appendRequest("bob", cancelCreate.Session.ID, 0, []byte{'b'}),
	); err != nil {
		t.Fatalf("AppendChunk(cancel) error = %v", err)
	}
	cancelPayload := filepath.Join(root, cancelCreate.Session.ID, payloadFileName)
	if err := os.Chmod(cancelPayload, 0o640); err != nil {
		t.Fatalf("chmod cancel payload failure injection: %v", err)
	}
	if _, err := store.Cancel(
		ctx,
		"bob",
		cancelCreate.Session.ID,
	); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("Cancel() cleanup failure error = %v, want ErrRecoveryRequired", err)
	}
	store.mu.RLock()
	cancelSnapshot := store.sessions[cancelCreate.Session.ID]
	store.mu.RUnlock()
	if cancelSnapshot.State != StateCancelled {
		t.Fatalf("persisted cancel snapshot = %+v, want cancelled", cancelSnapshot)
	}
	if err := os.Chmod(cancelPayload, 0o600); err != nil {
		t.Fatalf("restore cancel payload permissions: %v", err)
	}
	retriedCancel, err := store.Cancel(ctx, "bob", cancelCreate.Session.ID)
	if err != nil {
		t.Fatalf("idempotent Cancel() cleanup retry error = %v", err)
	}
	if retriedCancel.State != StateCancelled ||
		retriedCancel.Revision != cancelSnapshot.Revision {
		t.Fatalf(
			"idempotent Cancel() = %+v, want unchanged cancelled revision",
			retriedCancel,
		)
	}
	assertPayloadMissing(t, root, cancelCreate.Session.ID)

	store.stagingMu.Lock()
	stagedBytes := store.stagedBytes
	store.stagingMu.Unlock()
	if stagedBytes != 0 {
		t.Fatalf("staged bytes after idempotent terminal retries = %d, want 0", stagedBytes)
	}
}

func TestStoreSessionLimitIncludesTerminalTombstones(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	store := openTestStore(t, Options{
		MaxSessionsPerOwner: 1,
		MaxSessions:         1,
		Now:                 func() time.Time { return now },
	})
	ctx := context.Background()
	first, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "terminal-limit-1",
		Path:            "/home/alice/one.bin",
		TotalBytes:      1,
	})
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	cancelled, err := store.Cancel(ctx, "alice", first.Session.ID)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	_, err = store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "terminal-limit-2",
		Path:            "/home/alice/two.bin",
		TotalBytes:      1,
	})
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Create() behind terminal tombstone error = %v, want ErrLimitExceeded", err)
	}
	now = cancelled.ExpiresAt
	cleaned, err := store.CleanupExpired(ctx, now)
	if err != nil || cleaned != 1 {
		t.Fatalf("CleanupExpired() = (%d, %v), want (1, nil)", cleaned, err)
	}
	if _, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "terminal-limit-2",
		Path:            "/home/alice/two.bin",
		TotalBytes:      1,
	}); err != nil {
		t.Fatalf("Create() after tombstone cleanup error = %v", err)
	}
}

func TestStoreExpiryIsHardBoundaryExceptWhileCommitting(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	store := openTestStore(t, Options{
		TTL: time.Hour,
		Now: func() time.Time { return now },
	})
	ctx := context.Background()
	expiring, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "expires-1",
		Path:            "/home/alice/expires.bin",
		TotalBytes:      1,
	})
	if err != nil {
		t.Fatalf("Create(expiring) error = %v", err)
	}
	committingCreate, err := store.Create(ctx, CreateRequest{
		Owner:           "bob",
		ClientRequestID: "expires-committing",
		Path:            "/home/bob/committing.bin",
		TotalBytes:      0,
	})
	if err != nil {
		t.Fatalf("Create(committing) error = %v", err)
	}
	committing, err := store.BeginCommit(ctx, "bob", committingCreate.Session.ID)
	if err != nil {
		t.Fatalf("BeginCommit() error = %v", err)
	}
	now = expiring.Session.ExpiresAt

	if _, err := store.Get(ctx, "alice", expiring.Session.ID); !errors.Is(err, ErrExpired) {
		t.Fatalf("Get(expired) error = %v, want ErrExpired", err)
	}
	if _, err := store.AppendChunk(
		ctx,
		appendRequest("alice", expiring.Session.ID, 0, []byte{'a'}),
	); !errors.Is(err, ErrExpired) {
		t.Fatalf("AppendChunk(expired) error = %v, want ErrExpired", err)
	}
	if _, err := store.BeginCommit(ctx, "alice", expiring.Session.ID); !errors.Is(err, ErrExpired) {
		t.Fatalf("BeginCommit(expired) error = %v, want ErrExpired", err)
	}
	if _, err := store.Cancel(ctx, "alice", expiring.Session.ID); !errors.Is(err, ErrExpired) {
		t.Fatalf("Cancel(expired) error = %v, want ErrExpired", err)
	}
	if _, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "expires-1",
		Path:            "/home/alice/expires.bin",
		TotalBytes:      1,
	}); !errors.Is(err, ErrExpired) {
		t.Fatalf("Create(expired replay) error = %v, want ErrExpired", err)
	}

	recoveredCommitting, err := store.Get(ctx, "bob", committing.ID)
	if err != nil || recoveredCommitting.State != StateCommitting {
		t.Fatalf("Get(expired committing) = (%+v, %v)", recoveredCommitting, err)
	}
	payload, _, err := store.OpenPayload(ctx, "bob", committing.ID)
	if err != nil {
		t.Fatalf("OpenPayload(expired committing) error = %v", err)
	}
	if err := payload.Close(); err != nil {
		t.Fatalf("close expired committing payload: %v", err)
	}
	listed, err := store.ListCommitting(ctx)
	if err != nil {
		t.Fatalf("ListCommitting() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != committing.ID || listed[0].Owner != "bob" {
		t.Fatalf("ListCommitting() = %+v, want bob/%s", listed, committing.ID)
	}
	if _, err := store.MarkCommitted(ctx, "bob", committing.ID, CommitResult{
		ContentBLAKE3: emptyBLAKE3(),
	}); err != nil {
		t.Fatalf("MarkCommitted(expired committing) error = %v", err)
	}
	listed, err = store.ListCommitting(ctx)
	if err != nil || len(listed) != 0 {
		t.Fatalf("ListCommitting() after terminal = (%+v, %v), want empty", listed, err)
	}
}

func TestStoreCloseWaitsForAdmittedOperationBeforeUnlock(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "sessions")
	store, err := Open(root, Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ctx := context.Background()
	created, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "close-gate-1",
		Path:            "/home/alice/close-gate.bin",
		TotalBytes:      1,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	reader := newBlockingChunkReader('x')
	appendDone := make(chan error, 1)
	go func() {
		_, appendErr := store.AppendChunk(ctx, AppendChunkRequest{
			Owner:       "alice",
			ID:          created.Session.ID,
			Offset:      0,
			Length:      1,
			ChunkID:     "close-gate-chunk",
			ChunkSHA256: sha256Hex([]byte{'x'}),
			Body:        reader,
		})
		appendDone <- appendErr
	}()
	select {
	case <-reader.started:
	case <-time.After(5 * time.Second):
		t.Fatal("AppendChunk() did not reach its blocking reader")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- store.Close()
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		store.gateMu.Lock()
		closing := store.closing
		store.gateMu.Unlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Close() did not close the operation entrance")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Close() returned before admitted AppendChunk(): %v", err)
	default:
	}
	if competing, err := Open(root, Options{}); err == nil {
		_ = competing.Close()
		t.Fatal("Open() acquired the store lock while Close() was waiting")
	}

	close(reader.release)
	if err := <-appendDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("admitted AppendChunk() error = %v, want context.Canceled", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	store, err = Open(root, Options{})
	if err != nil {
		t.Fatalf("Open() after Close() error = %v", err)
	}
	defer store.Close()
	recovered, err := store.Get(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("Get() after Close() error = %v", err)
	}
	if recovered.State != StateUploading || recovered.DurableOffset != 0 {
		t.Fatalf("Get() after Close() = %+v, want uploading offset 0", recovered)
	}
}

func TestStoreCloseIsBoundedAndRetainsLockWhileOperationDrains(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "sessions")
	store, err := Open(root, Options{CloseTimeout: 25 * time.Millisecond})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ctx := context.Background()
	created, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "close-timeout-1",
		Path:            "/home/alice/close-timeout.bin",
		TotalBytes:      1,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	reader := newBlockingChunkReader('x')
	appendDone := make(chan error, 1)
	go func() {
		_, appendErr := store.AppendChunk(ctx, AppendChunkRequest{
			Owner:       "alice",
			ID:          created.Session.ID,
			Offset:      0,
			Length:      1,
			ChunkID:     "close-timeout-chunk",
			ChunkSHA256: sha256Hex([]byte{'x'}),
			Body:        reader,
		})
		appendDone <- appendErr
	}()
	select {
	case <-reader.started:
	case <-time.After(5 * time.Second):
		t.Fatal("AppendChunk() did not reach its blocking reader")
	}

	startedAt := time.Now()
	closeErr := store.Close()
	if !errors.Is(closeErr, ErrCloseTimeout) {
		t.Fatalf("Close() error = %v, want ErrCloseTimeout", closeErr)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("Close() elapsed = %s, want bounded return", elapsed)
	}
	if competing, err := Open(root, Options{}); err == nil {
		_ = competing.Close()
		t.Fatal("Open() acquired flock while timed-out Close() was still draining")
	}

	close(reader.release)
	if err := <-appendDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("drained AppendChunk() error = %v, want context.Canceled", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var reopened *Store
	for {
		reopened, err = Open(root, Options{})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Open() did not acquire flock after drain: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	defer reopened.Close()
	if err := store.Close(); err != nil {
		t.Fatalf("Close() after background drain error = %v", err)
	}
	recovered, err := reopened.Get(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("Get() after timed-out Close() error = %v", err)
	}
	if recovered.State != StateUploading || recovered.DurableOffset != 0 {
		t.Fatalf("recovered session = %+v, want uploading offset 0", recovered)
	}
}

func TestStoreCloseOwnsReturnedPayloadReaderLifetime(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "sessions")
	store, err := Open(root, Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ctx := context.Background()
	created, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "payload-lifetime-1",
		Path:            "/home/alice/payload-lifetime.bin",
		TotalBytes:      0,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	callerClosed, _, err := store.OpenPayload(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("OpenPayload() error = %v", err)
	}
	store.gateMu.Lock()
	trackedBeforeClose := len(store.openPayloads)
	store.gateMu.Unlock()
	if trackedBeforeClose != 1 {
		t.Fatalf("tracked payloads before caller Close() = %d, want 1", trackedBeforeClose)
	}
	if err := callerClosed.Close(); err != nil {
		t.Fatalf("caller payload Close() error = %v", err)
	}
	if err := callerClosed.Close(); err != nil {
		t.Fatalf("replayed caller payload Close() error = %v", err)
	}
	store.gateMu.Lock()
	trackedAfterClose := len(store.openPayloads)
	store.gateMu.Unlock()
	if trackedAfterClose != 0 {
		t.Fatalf("tracked payloads after caller Close() = %d, want 0", trackedAfterClose)
	}

	payload, _, err := store.OpenPayload(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("second OpenPayload() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	var buffer [1]byte
	if _, err := payload.Read(buffer[:]); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("payload Read() after Close() error = %v, want os.ErrClosed", err)
	}

	reopened, err := Open(root, Options{})
	if err != nil {
		t.Fatalf("Open() after tracked payload Close() error = %v", err)
	}
	defer reopened.Close()
	recovered, err := reopened.Get(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("Get() after tracked payload Close() error = %v", err)
	}
	if recovered.State != StateReady {
		t.Fatalf("recovered session = %+v, want ready", recovered)
	}
}

func TestStoreStagingLimitAdmissionIsAtomicAndReleasedByTerminalCleanup(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, Options{
		MaxStagedBytesPerOwner: 4,
		MaxStagedBytes:         4,
	})
	ctx := context.Background()
	type uploadCandidate struct {
		owner   string
		session Session
		body    []byte
	}
	candidates := make([]uploadCandidate, 0, 2)
	for _, input := range []struct {
		owner string
		body  []byte
	}{
		{owner: "alice", body: []byte("aaaa")},
		{owner: "bob", body: []byte("bbbb")},
	} {
		created, err := store.Create(ctx, CreateRequest{
			Owner:           input.owner,
			ClientRequestID: "staging-" + input.owner,
			Path:            "/home/" + input.owner + "/payload.bin",
			TotalBytes:      int64(len(input.body)),
		})
		if err != nil {
			t.Fatalf("Create(%s) error = %v", input.owner, err)
		}
		candidates = append(candidates, uploadCandidate{
			owner:   input.owner,
			session: created.Session,
			body:    input.body,
		})
	}

	start := make(chan struct{})
	type outcome struct {
		index  int
		result AppendResult
		err    error
	}
	outcomes := make(chan outcome, len(candidates))
	for index, candidate := range candidates {
		go func(index int, candidate uploadCandidate) {
			<-start
			result, err := store.AppendChunk(
				ctx,
				appendRequest(candidate.owner, candidate.session.ID, 0, candidate.body),
			)
			outcomes <- outcome{index: index, result: result, err: err}
		}(index, candidate)
	}
	close(start)

	winner := -1
	limited := -1
	for range candidates {
		outcome := <-outcomes
		switch {
		case outcome.err == nil:
			if winner != -1 {
				t.Fatalf("multiple staging admissions succeeded: %d and %d", winner, outcome.index)
			}
			winner = outcome.index
		case errors.Is(outcome.err, ErrStagingLimit):
			limited = outcome.index
		default:
			t.Fatalf("AppendChunk(%d) unexpected error = %v", outcome.index, outcome.err)
		}
	}
	if winner == -1 || limited == -1 {
		t.Fatalf("staging outcomes winner=%d limited=%d, want one each", winner, limited)
	}
	store.stagingMu.Lock()
	stagedBytes := store.stagedBytes
	store.stagingMu.Unlock()
	if stagedBytes != 4 {
		t.Fatalf("staged bytes after atomic admission = %d, want 4", stagedBytes)
	}

	winnerCandidate := candidates[winner]
	if _, err := store.MarkConflict(
		ctx,
		winnerCandidate.owner,
		winnerCandidate.session.ID,
		"release staging capacity",
	); err != nil {
		t.Fatalf("MarkConflict(winner) error = %v", err)
	}
	store.stagingMu.Lock()
	stagedBytes = store.stagedBytes
	store.stagingMu.Unlock()
	if stagedBytes != 0 {
		t.Fatalf("staged bytes after terminal cleanup = %d, want 0", stagedBytes)
	}

	limitedCandidate := candidates[limited]
	retried, err := store.AppendChunk(
		ctx,
		appendRequest(
			limitedCandidate.owner,
			limitedCandidate.session.ID,
			0,
			limitedCandidate.body,
		),
	)
	if err != nil {
		t.Fatalf("AppendChunk() after terminal capacity release error = %v", err)
	}
	if retried.Session.State != StateReady {
		t.Fatalf("retried AppendChunk() = %+v, want ready", retried)
	}
}

func TestStorePhysicalCapacityReservationIncludesConcurrentInflightChunks(t *testing.T) {
	t.Parallel()

	capacityErr := errors.New("physical reservation exceeds test capacity")
	capacityChecks := make(chan int64, 3)
	store := openTestStore(t, Options{
		MaxStagedBytesPerOwner: 4,
		MaxStagedBytes:         8,
		CheckStagingCapacity: func(_ context.Context, requiredBytes int64) error {
			capacityChecks <- requiredBytes
			if requiredBytes > 4 {
				return capacityErr
			}
			return nil
		},
	})
	ctx := context.Background()
	first, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "physical-reservation-1",
		Path:            "/home/alice/first.bin",
		TotalBytes:      4,
	})
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	second, err := store.Create(ctx, CreateRequest{
		Owner:           "bob",
		ClientRequestID: "physical-reservation-2",
		Path:            "/home/bob/second.bin",
		TotalBytes:      4,
	})
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}

	firstReader := newBlockingBytesReader([]byte("aaaa"))
	firstDone := make(chan error, 1)
	go func() {
		_, appendErr := store.AppendChunk(ctx, AppendChunkRequest{
			Owner:       "alice",
			ID:          first.Session.ID,
			Offset:      0,
			Length:      4,
			ChunkID:     "physical-first",
			ChunkSHA256: sha256Hex([]byte("aaaa")),
			Body:        firstReader,
		})
		firstDone <- appendErr
	}()
	select {
	case <-firstReader.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first AppendChunk() did not hold its physical reservation")
	}
	if got := <-capacityChecks; got != 4 {
		t.Fatalf("first capacity check = %d, want 4", got)
	}

	_, err = store.AppendChunk(
		ctx,
		appendRequest("bob", second.Session.ID, 0, []byte("bbbb")),
	)
	if !errors.Is(err, ErrStagingLimit) || !errors.Is(err, capacityErr) {
		t.Fatalf(
			"concurrent AppendChunk() error = %v, want capacity reservation failure",
			err,
		)
	}
	if got := <-capacityChecks; got != 8 {
		t.Fatalf("concurrent capacity check = %d, want aggregate 8", got)
	}
	store.stagingMu.Lock()
	physicalReserved := store.physicalReservedBytes
	store.stagingMu.Unlock()
	if physicalReserved != 4 {
		t.Fatalf("physical reservation after rejection = %d, want 4", physicalReserved)
	}

	close(firstReader.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first AppendChunk() error = %v", err)
	}
	store.stagingMu.Lock()
	physicalReserved = store.physicalReservedBytes
	store.stagingMu.Unlock()
	if physicalReserved != 0 {
		t.Fatalf("physical reservation after materialization = %d, want 0", physicalReserved)
	}

	if _, err := store.AppendChunk(
		ctx,
		appendRequest("bob", second.Session.ID, 0, []byte("bbbb")),
	); err != nil {
		t.Fatalf("AppendChunk() after reservation release error = %v", err)
	}
	if got := <-capacityChecks; got != 4 {
		t.Fatalf("capacity check after release = %d, want 4", got)
	}
	store.stagingMu.Lock()
	physicalReserved = store.physicalReservedBytes
	store.stagingMu.Unlock()
	if physicalReserved != 0 {
		t.Fatalf("final physical reservation = %d, want 0", physicalReserved)
	}
}

func TestStoreCapacityChecksRunOutsideAccountingLockAndRollbackReservations(
	t *testing.T,
) {
	t.Parallel()

	firstCapacityErr := errors.New("first capacity check rejected")
	secondCapacityErr := errors.New("second capacity check rejected")
	firstCheckReached := make(chan struct{})
	secondCheckReached := make(chan struct{})
	allowFirstCheck := make(chan struct{})
	var releaseFirst sync.Once
	t.Cleanup(func() { releaseFirst.Do(func() { close(allowFirstCheck) }) })
	store := openTestStore(t, Options{
		MaxStagedBytesPerOwner: 4,
		MaxStagedBytes:         8,
		CheckStagingCapacity: func(_ context.Context, requiredBytes int64) error {
			switch requiredBytes {
			case 4:
				close(firstCheckReached)
				<-allowFirstCheck
				return firstCapacityErr
			case 8:
				close(secondCheckReached)
				return secondCapacityErr
			default:
				return fmt.Errorf("unexpected capacity reservation: %d", requiredBytes)
			}
		},
	})
	ctx := context.Background()
	first, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "capacity-outside-lock-1",
		Path:            "/home/alice/first.bin",
		TotalBytes:      4,
	})
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	second, err := store.Create(ctx, CreateRequest{
		Owner:           "bob",
		ClientRequestID: "capacity-outside-lock-2",
		Path:            "/home/bob/second.bin",
		TotalBytes:      4,
	})
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, appendErr := store.AppendChunk(
			ctx,
			appendRequest("alice", first.Session.ID, 0, []byte("aaaa")),
		)
		firstDone <- appendErr
	}()
	select {
	case <-firstCheckReached:
	case <-time.After(5 * time.Second):
		t.Fatal("first capacity check did not start")
	}

	secondDone := make(chan error, 1)
	go func() {
		_, appendErr := store.AppendChunk(
			ctx,
			appendRequest("bob", second.Session.ID, 0, []byte("bbbb")),
		)
		secondDone <- appendErr
	}()
	select {
	case <-secondCheckReached:
	case <-time.After(5 * time.Second):
		t.Fatal("second capacity check was blocked by the accounting lock")
	}
	if err := <-secondDone; !errors.Is(err, ErrStagingLimit) ||
		!errors.Is(err, secondCapacityErr) {
		t.Fatalf(
			"second AppendChunk() error = %v, want staging and capacity errors",
			err,
		)
	}

	store.stagingMu.Lock()
	stagedBytes := store.stagedBytes
	physicalReserved := store.physicalReservedBytes
	firstBytes := store.stagedBySession[first.Session.ID]
	secondBytes := store.stagedBySession[second.Session.ID]
	store.stagingMu.Unlock()
	if stagedBytes != 4 || physicalReserved != 4 ||
		firstBytes != 4 || secondBytes != 0 {
		t.Fatalf(
			"usage after second rollback = staged %d physical %d first %d second %d, want 4 4 4 0",
			stagedBytes,
			physicalReserved,
			firstBytes,
			secondBytes,
		)
	}

	releaseFirst.Do(func() { close(allowFirstCheck) })
	if err := <-firstDone; !errors.Is(err, ErrStagingLimit) ||
		!errors.Is(err, firstCapacityErr) {
		t.Fatalf(
			"first AppendChunk() error = %v, want staging and capacity errors",
			err,
		)
	}
	store.stagingMu.Lock()
	stagedBytes = store.stagedBytes
	physicalReserved = store.physicalReservedBytes
	firstBytes = store.stagedBySession[first.Session.ID]
	secondBytes = store.stagedBySession[second.Session.ID]
	store.stagingMu.Unlock()
	if stagedBytes != 0 || physicalReserved != 0 ||
		firstBytes != 0 || secondBytes != 0 {
		t.Fatalf(
			"usage after all rollbacks = staged %d physical %d first %d second %d, want zero",
			stagedBytes,
			physicalReserved,
			firstBytes,
			secondBytes,
		)
	}
}

func TestStorePhysicalReservationIsReleasedAfterChunkFailure(t *testing.T) {
	t.Parallel()

	capacityChecks := make(chan int64, 2)
	store := openTestStore(t, Options{
		MaxStagedBytesPerOwner: 3,
		MaxStagedBytes:         3,
		CheckStagingCapacity: func(_ context.Context, requiredBytes int64) error {
			capacityChecks <- requiredBytes
			return nil
		},
	})
	ctx := context.Background()
	created, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "physical-failure-1",
		Path:            "/home/alice/failure.bin",
		TotalBytes:      3,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err = store.AppendChunk(ctx, AppendChunkRequest{
		Owner:       "alice",
		ID:          created.Session.ID,
		Offset:      0,
		Length:      3,
		ChunkID:     "physical-failure",
		ChunkSHA256: sha256Hex([]byte("abc")),
		Body:        bytes.NewReader([]byte("ab")),
	})
	if !errors.Is(err, ErrChunkLength) {
		t.Fatalf("AppendChunk(short body) error = %v, want ErrChunkLength", err)
	}
	if got := <-capacityChecks; got != 3 {
		t.Fatalf("failed chunk capacity check = %d, want 3", got)
	}
	store.stagingMu.Lock()
	physicalReserved := store.physicalReservedBytes
	stagedBytes := store.stagedBytes
	store.stagingMu.Unlock()
	if physicalReserved != 0 || stagedBytes != 0 {
		t.Fatalf(
			"usage after failed chunk = physical %d staged %d, want zero",
			physicalReserved,
			stagedBytes,
		)
	}

	if _, err := store.AppendChunk(
		ctx,
		appendRequest("alice", created.Session.ID, 0, []byte("abc")),
	); err != nil {
		t.Fatalf("AppendChunk() after failed reservation error = %v", err)
	}
	if got := <-capacityChecks; got != 3 {
		t.Fatalf("retried chunk capacity check = %d, want 3", got)
	}
	store.stagingMu.Lock()
	physicalReserved = store.physicalReservedBytes
	store.stagingMu.Unlock()
	if physicalReserved != 0 {
		t.Fatalf("physical reservation after retry = %d, want 0", physicalReserved)
	}
}

func TestStoreCapacityHookRejectsBeforePayloadMutation(t *testing.T) {
	t.Parallel()

	capacityErr := errors.New("physical staging capacity unavailable")
	var hookCalls int
	store := openTestStore(t, Options{
		MaxStagedBytesPerOwner: 8,
		MaxStagedBytes:         8,
		CheckStagingCapacity: func(ctx context.Context, additionalBytes int64) error {
			if ctx == nil {
				t.Fatal("CheckStagingCapacity() received a nil context")
			}
			hookCalls++
			if additionalBytes != 3 {
				t.Fatalf(
					"CheckStagingCapacity() additionalBytes = %d, want 3",
					additionalBytes,
				)
			}
			return capacityErr
		},
	})
	ctx := context.Background()
	created, err := store.Create(ctx, CreateRequest{
		Owner:           "alice",
		ClientRequestID: "capacity-hook-1",
		Path:            "/home/alice/capacity.bin",
		TotalBytes:      3,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err = store.AppendChunk(
		ctx,
		appendRequest("alice", created.Session.ID, 0, []byte("abc")),
	)
	if !errors.Is(err, ErrStagingLimit) || !errors.Is(err, capacityErr) {
		t.Fatalf(
			"AppendChunk() error = %v, want ErrStagingLimit and callback error",
			err,
		)
	}
	if hookCalls != 1 {
		t.Fatalf("CheckStagingCapacity() calls = %d, want 1", hookCalls)
	}
	recovered, err := store.Get(ctx, "alice", created.Session.ID)
	if err != nil {
		t.Fatalf("Get() after capacity rejection error = %v", err)
	}
	if recovered.DurableOffset != 0 || recovered.Revision != created.Session.Revision {
		t.Fatalf("session after capacity rejection = %+v, want unchanged", recovered)
	}
	payloadInfo, err := os.Stat(filepath.Join(
		store.rootPath,
		created.Session.ID,
		payloadFileName,
	))
	if err != nil {
		t.Fatalf("Stat(payload) after capacity rejection error = %v", err)
	}
	if payloadInfo.Size() != 0 {
		t.Fatalf("payload size after capacity rejection = %d, want 0", payloadInfo.Size())
	}
	store.stagingMu.Lock()
	stagedBytes := store.stagedBytes
	sessionBytes := store.stagedBySession[created.Session.ID]
	store.stagingMu.Unlock()
	if stagedBytes != 0 || sessionBytes != 0 {
		t.Fatalf(
			"staging usage after capacity rejection = global %d session %d, want zero",
			stagedBytes,
			sessionBytes,
		)
	}
}

func openTestStore(t *testing.T, options Options) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "sessions"), options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func assertStateRevisions(t *testing.T, sessionDir string, want []uint64) {
	t.Helper()
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", sessionDir, err)
	}
	got := make([]uint64, 0)
	for _, entry := range entries {
		if revision, ok := parseStateFileName(entry.Name()); ok {
			got = append(got, revision)
		}
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	if len(got) != len(want) {
		t.Fatalf("state revisions = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("state revisions = %v, want %v", got, want)
		}
	}
}

func assertPayloadMissing(t *testing.T, root, id string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, id, payloadFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("terminal payload Lstat() error = %v, want not exist", err)
	}
}

type blockingChunkReader struct {
	value   byte
	started chan struct{}
	release chan struct{}
	once    sync.Once
	read    bool
}

func newBlockingChunkReader(value byte) *blockingChunkReader {
	return &blockingChunkReader{
		value:   value,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *blockingChunkReader) Read(buffer []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.once.Do(func() {
		close(r.started)
	})
	<-r.release
	r.read = true
	buffer[0] = r.value
	return 1, nil
}

type blockingBytesReader struct {
	data    []byte
	started chan struct{}
	release chan struct{}
	once    sync.Once
	offset  int
}

func newBlockingBytesReader(data []byte) *blockingBytesReader {
	return &blockingBytesReader{
		data:    append([]byte(nil), data...),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *blockingBytesReader) Read(buffer []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	r.once.Do(func() {
		close(r.started)
		<-r.release
	})
	n := copy(buffer, r.data[r.offset:])
	r.offset += n
	return n, nil
}

func appendRequest(owner, id string, offset int64, body []byte) AppendChunkRequest {
	return AppendChunkRequest{
		Owner:       owner,
		ID:          id,
		Offset:      offset,
		Length:      int64(len(body)),
		ChunkID:     fmt.Sprintf("chunk-%d-%s", offset, sha256Hex(body)[:16]),
		ChunkSHA256: sha256Hex(body),
		Body:        bytes.NewReader(body),
	}
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func blake3Hex(value []byte) string {
	sum := blake3.Sum256(value)
	return hex.EncodeToString(sum[:])
}
