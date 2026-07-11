package favorites

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	trashDeleteOperationA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	trashDeleteOperationB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestStore_SnapshotDeleteExactReturnsCanonicalDeepCopies(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if _, err := store.Add("user-b", "/docs/c.txt", "stored-c"); err != nil {
		t.Fatalf("Add(/docs/c.txt) error: %v", err)
	}
	if _, err := store.Add("user-a", "/docs/b.txt", "stored-b"); err != nil {
		t.Fatalf("Add(/docs/b.txt) error: %v", err)
	}
	if _, err := store.Add("user-a", "/docs/a.txt", "stored-a"); err != nil {
		t.Fatalf("Add(/docs/a.txt) error: %v", err)
	}
	if _, err := store.Add("user-a", "/other.txt", "other"); err != nil {
		t.Fatalf("Add(/other.txt) error: %v", err)
	}

	snapshot, err := store.SnapshotDeleteExact(`docs`)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact() error: %v", err)
	}
	want := []struct {
		userID string
		path   string
	}{
		{userID: "user-a", path: "/docs/a.txt"},
		{userID: "user-a", path: "/docs/b.txt"},
		{userID: "user-b", path: "/docs/c.txt"},
	}
	if len(snapshot) != len(want) {
		t.Fatalf("snapshot count = %d, want %d", len(snapshot), len(want))
	}
	for index, expected := range want {
		if snapshot[index].UserID != expected.userID || snapshot[index].Path != expected.path {
			t.Fatalf("snapshot[%d] = %s:%s, want %s:%s", index, snapshot[index].UserID, snapshot[index].Path, expected.userID, expected.path)
		}
	}

	snapshot[0].Note = "mutated"
	snapshot[0].Path = "/mutated.txt"
	stored := store.List("user-a")
	if len(stored) != 3 {
		t.Fatalf("stored favorite count = %d, want 3", len(stored))
	}
	foundOriginal := false
	for _, favorite := range stored {
		if favorite.Path == "/docs/a.txt" {
			foundOriginal = favorite.Note == "stored-a"
		}
		if favorite.Path == "/mutated.txt" {
			t.Fatal("snapshot mutation changed stored favorite path")
		}
	}
	if !foundOriginal {
		t.Fatal("snapshot mutation changed stored favorite note")
	}
}

func TestStore_ApplyDeleteExactIsScopedAndIdempotent(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "favorites.json")
	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	matching, err := store.Add("user-a", "/docs/matching.txt", "original")
	if err != nil {
		t.Fatalf("Add(matching) error: %v", err)
	}
	recreatedOriginal, err := store.Add("user-a", "/docs/recreated.txt", "original")
	if err != nil {
		t.Fatalf("Add(recreated original) error: %v", err)
	}
	if _, err := store.Add("user-a", "/other.txt", "other"); err != nil {
		t.Fatalf("Add(other) error: %v", err)
	}

	snapshot, err := store.SnapshotDeleteExact("/docs")
	if err != nil {
		t.Fatalf("SnapshotDeleteExact() error: %v", err)
	}

	if err := store.UpdateNote("user-a", matching.Path, "newer note"); err != nil {
		t.Fatalf("UpdateNote(matching) error: %v", err)
	}
	if err := store.Remove("user-a", recreatedOriginal.Path); err != nil {
		t.Fatalf("Remove(recreated original) error: %v", err)
	}
	time.Sleep(time.Millisecond)
	recreated, err := store.Add("user-a", recreatedOriginal.Path, "recreated")
	if err != nil {
		t.Fatalf("Add(recreated) error: %v", err)
	}
	if recreated.CreatedAt.Equal(recreatedOriginal.CreatedAt) {
		t.Fatal("recreated favorite must have a distinct identity timestamp")
	}
	createdAfterSnapshot, err := store.Add("user-a", "/docs/new.txt", "new")
	if err != nil {
		t.Fatalf("Add(created after snapshot) error: %v", err)
	}

	if err := store.ApplyDeleteExact(snapshot); err != nil {
		t.Fatalf("ApplyDeleteExact(first) error: %v", err)
	}
	if err := store.ApplyDeleteExact(snapshot); err != nil {
		t.Fatalf("ApplyDeleteExact(second) error: %v", err)
	}

	if store.IsFavorite("user-a", matching.Path) {
		t.Fatal("matching favorite should be removed even after a note update")
	}
	if !store.IsFavorite("user-a", recreated.Path) {
		t.Fatal("recreated favorite should not be removed by the old snapshot")
	}
	if !store.IsFavorite("user-a", createdAfterSnapshot.Path) {
		t.Fatal("favorite created after the snapshot should remain")
	}
	if !store.IsFavorite("user-a", "/other.txt") {
		t.Fatal("unrelated favorite should remain")
	}

	loaded := store.List("user-a")
	for _, favorite := range loaded {
		if favorite.Path == recreated.Path && favorite.Note != "recreated" {
			t.Fatalf("recreated favorite changed: %+v", favorite)
		}
	}

	reloaded, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(reload) error: %v", err)
	}
	if reloaded.IsFavorite("user-a", matching.Path) {
		t.Fatal("matching favorite removal was not persisted")
	}
	if !reloaded.IsFavorite("user-a", recreated.Path) || !reloaded.IsFavorite("user-a", createdAfterSnapshot.Path) {
		t.Fatal("favorites outside the exact snapshot identity were not preserved")
	}
}

func TestStore_DeleteExactSnapshotSupportsRestoreIfMissing(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	created, err := store.Add("user-a", "/docs/report.txt", "original")
	if err != nil {
		t.Fatalf("Add() error: %v", err)
	}
	snapshot, err := store.SnapshotDeleteExact(created.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact() error: %v", err)
	}
	if err := store.ApplyDeleteExact(snapshot); err != nil {
		t.Fatalf("ApplyDeleteExact() error: %v", err)
	}
	if err := store.RestoreFavoritesIfMissing(snapshot); err != nil {
		t.Fatalf("RestoreFavoritesIfMissing() error: %v", err)
	}

	restored := store.List("user-a")
	if len(restored) != 1 || restored[0].Path != created.Path || restored[0].Note != "original" || !restored[0].CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("restored favorite = %+v", restored)
	}
}

func TestStore_TrashDeleteOperationReopensAndRollsBackActualRemovedFavorite(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "favorites.json")
	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	original, err := store.Add("user-a", "/docs/report.txt", "snapshot note")
	if err != nil {
		t.Fatalf("Add() error: %v", err)
	}
	planned, err := store.SnapshotDeleteExact(original.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact() error: %v", err)
	}
	if err := store.UpdateNote(original.UserID, original.Path, "latest note"); err != nil {
		t.Fatalf("UpdateNote() error: %v", err)
	}

	if err := store.RollbackTrashDeleteOperation(trashDeleteOperationA); err != nil {
		t.Fatalf("RollbackTrashDeleteOperation(before apply) error: %v", err)
	}
	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(precommit) error: %v", err)
	}
	if store.IsFavorite(original.UserID, original.Path) {
		t.Fatal("precommit apply did not remove the planned favorite")
	}
	operation := store.trashDeleteOperations[trashDeleteOperationA]
	if operation == nil || len(operation.Removed) != 1 || operation.Removed[0].Note != "latest note" {
		t.Fatalf("durable removed state = %+v, want the complete object removed at apply time", operation)
	}

	reopened, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(reopen before rollback) error: %v", err)
	}
	if reopened.IsFavorite(original.UserID, original.Path) || reopened.trashDeleteOperations[trashDeleteOperationA] == nil {
		t.Fatal("reopen did not preserve the precommit deletion and rollback marker")
	}
	if err := reopened.RollbackTrashDeleteOperation(trashDeleteOperationA); err != nil {
		t.Fatalf("RollbackTrashDeleteOperation() error: %v", err)
	}
	restored := reopened.List(original.UserID)
	if len(restored) != 1 || restored[0].Path != original.Path || restored[0].Note != "latest note" || !restored[0].CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("restored favorite = %+v, want the complete actually removed object", restored)
	}
	if reopened.trashDeleteOperations[trashDeleteOperationA] != nil {
		t.Fatal("rollback marker remains after successful rollback")
	}

	reopenedAgain, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(reopen after rollback) error: %v", err)
	}
	if !reopenedAgain.IsFavorite(original.UserID, original.Path) || reopenedAgain.trashDeleteOperations[trashDeleteOperationA] != nil {
		t.Fatal("rolled-back state was not persisted")
	}
	if err := reopenedAgain.RollbackTrashDeleteOperation(trashDeleteOperationA); err != nil {
		t.Fatalf("idempotent RollbackTrashDeleteOperation() error: %v", err)
	}
}

func TestStore_TrashDeleteOperationPrecommitIsIdempotentAndRejectsPlanMismatch(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "favorites.json")
	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	original, err := store.Add("user-a", "/docs/report.txt", "original")
	if err != nil {
		t.Fatalf("Add() error: %v", err)
	}
	planned, err := store.SnapshotDeleteExact(original.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact() error: %v", err)
	}

	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(first) error: %v", err)
	}
	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(idempotent) error: %v", err)
	}
	if got := len(store.trashDeleteOperations[trashDeleteOperationA].Removed); got != 1 {
		t.Fatalf("removed snapshot count after replay = %d, want 1", got)
	}

	mismatched := cloneFavoriteSlice(planned)
	mismatched[0].Note = "different plan"
	err = store.ApplyTrashDeleteOperation(trashDeleteOperationA, mismatched, false)
	if !errors.Is(err, ErrTrashDeleteOperationConflict) {
		t.Fatalf("ApplyTrashDeleteOperation(mismatch) error = %v, want conflict", err)
	}
	if err := store.CompleteTrashDeleteOperation(trashDeleteOperationA); !errors.Is(err, ErrTrashDeleteOperationConflict) {
		t.Fatalf("CompleteTrashDeleteOperation(precommit) error = %v, want conflict", err)
	}
	if store.IsFavorite(original.UserID, original.Path) || store.trashDeleteOperations[trashDeleteOperationA] == nil {
		t.Fatal("plan conflict changed the pending operation state")
	}
}

func TestStore_TrashDeleteOperationCommittedReceiptProtectsReplayWithAndWithoutPrecommit(t *testing.T) {
	for _, test := range []struct {
		name        string
		precommit   bool
		reintroduce bool
	}{
		{name: "without marker"},
		{name: "with marker", precommit: true, reintroduce: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			storePath := filepath.Join(t.TempDir(), "favorites.json")
			store, err := NewStore(storePath)
			if err != nil {
				t.Fatalf("NewStore() error: %v", err)
			}
			original, err := store.Add("user-a", "/docs/report.txt", "original")
			if err != nil {
				t.Fatalf("Add() error: %v", err)
			}
			planned, err := store.SnapshotDeleteExact(original.Path)
			if err != nil {
				t.Fatalf("SnapshotDeleteExact() error: %v", err)
			}
			if test.precommit {
				if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, false); err != nil {
					t.Fatalf("ApplyTrashDeleteOperation(precommit) error: %v", err)
				}
			}
			if test.reintroduce {
				if err := store.RestoreFavorites(planned); err != nil {
					t.Fatalf("RestoreFavorites(exact identity) error: %v", err)
				}
			}

			if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, true); err != nil {
				t.Fatalf("ApplyTrashDeleteOperation(committed) error: %v", err)
			}
			marker := store.trashDeleteOperations[trashDeleteOperationA]
			if store.IsFavorite(original.UserID, original.Path) || marker == nil || !marker.Committed {
				t.Fatal("committed apply did not atomically remove the exact favorite and record its receipt")
			}

			reopened, err := NewStore(storePath)
			if err != nil {
				t.Fatalf("NewStore(reopen) error: %v", err)
			}
			marker = reopened.trashDeleteOperations[trashDeleteOperationA]
			if reopened.IsFavorite(original.UserID, original.Path) || marker == nil || !marker.Committed {
				t.Fatal("committed apply receipt was not persisted")
			}
			if err := reopened.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, true); err != nil {
				t.Fatalf("ApplyTrashDeleteOperation(committed replay) error: %v", err)
			}
			time.Sleep(time.Millisecond)
			recreated, err := reopened.Add(original.UserID, original.Path, "recreated")
			if err != nil {
				t.Fatalf("Add(recreated) error: %v", err)
			}
			if recreated.CreatedAt.Equal(original.CreatedAt) {
				t.Fatal("recreated favorite identity did not change")
			}
			if err := reopened.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, true); err != nil {
				t.Fatalf("ApplyTrashDeleteOperation(after recreate) error: %v", err)
			}
			if !reopened.IsFavorite(recreated.UserID, recreated.Path) {
				t.Fatal("committed replay removed a later favorite identity")
			}
			mismatched := cloneFavoriteSlice(planned)
			mismatched[0].Note = "different plan"
			if err := reopened.ApplyTrashDeleteOperation(trashDeleteOperationA, mismatched, true); !errors.Is(err, ErrTrashDeleteOperationConflict) {
				t.Fatalf("ApplyTrashDeleteOperation(committed mismatch) error = %v, want conflict", err)
			}
			if !reopened.IsFavorite(recreated.UserID, recreated.Path) {
				t.Fatal("committed plan conflict changed the recreated favorite")
			}
			if err := reopened.RollbackTrashDeleteOperation(trashDeleteOperationA); !errors.Is(err, ErrTrashDeleteOperationConflict) {
				t.Fatalf("RollbackTrashDeleteOperation(committed) error = %v, want conflict", err)
			}
			if err := reopened.CompleteTrashDeleteOperation(trashDeleteOperationA); err != nil {
				t.Fatalf("CompleteTrashDeleteOperation() error: %v", err)
			}
			marker = reopened.trashDeleteOperations[trashDeleteOperationA]
			if marker == nil || !marker.Completed || !reopened.IsFavorite(recreated.UserID, recreated.Path) {
				t.Fatal("completion did not retain completed ownership while preserving the recreated favorite")
			}
			if err := reopened.CompleteTrashDeleteOperation(trashDeleteOperationA); err != nil {
				t.Fatalf("CompleteTrashDeleteOperation(idempotent) error: %v", err)
			}
		})
	}
}

func TestStore_CommittedTrashDeleteReceiptTracksLaterRestoreBlocking(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	original, err := store.Add("user-a", "/docs/report.txt", "original")
	if err != nil {
		t.Fatalf("Add(original) error: %v", err)
	}
	originalPlan, err := store.SnapshotDeleteExact(original.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact(original) error: %v", err)
	}
	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationB, originalPlan, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(committed) error: %v", err)
	}
	committedBefore := cloneTrashDeleteOperation(store.trashDeleteOperations[trashDeleteOperationB])

	time.Sleep(time.Millisecond)
	rebuilt, err := store.Add(original.UserID, original.Path, "rebuilt")
	if err != nil {
		t.Fatalf("Add(rebuilt) error: %v", err)
	}
	assertTrashDeleteOperationBlocked(t, store.trashDeleteOperations[trashDeleteOperationB], committedBefore, original.UserID, original.Path)

	rebuiltPlan, err := store.SnapshotDeleteExact(rebuilt.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact(rebuilt) error: %v", err)
	}
	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, rebuiltPlan, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(pending) error: %v", err)
	}
	if err := store.RestoreFavoritesIfMissing(rebuiltPlan); err != nil {
		t.Fatalf("RestoreFavoritesIfMissing(pending plan) error: %v", err)
	}
	assertTrashDeleteOperationBlocked(t, store.trashDeleteOperations[trashDeleteOperationB], committedBefore, original.UserID, original.Path)
	pending := store.trashDeleteOperations[trashDeleteOperationA]
	if pending == nil || pending.Committed || len(pending.RestoreBlocked) != 1 {
		t.Fatalf("pending marker did not retain active restore blocking: %+v", pending)
	}

	if err := store.Remove(rebuilt.UserID, rebuilt.Path); err != nil {
		t.Fatalf("Remove(rebuilt) error: %v", err)
	}
	if err := store.RollbackTrashDeleteOperation(trashDeleteOperationA); err != nil {
		t.Fatalf("RollbackTrashDeleteOperation(pending) error: %v", err)
	}
	if store.IsFavorite(rebuilt.UserID, rebuilt.Path) {
		t.Fatal("pending rollback restored a favorite after a later restore and removal")
	}
	assertTrashDeleteOperationBlocked(t, store.trashDeleteOperations[trashDeleteOperationB], committedBefore, original.UserID, original.Path)
}

func assertTrashDeleteOperationBlocked(t *testing.T, got, want *trashDeleteOperation, userID, favoritePath string) {
	t.Helper()
	if got == nil || want == nil || got.Committed != want.Committed || got.Completed != want.Completed ||
		!favoriteSlicesEqual(got.Planned, want.Planned) ||
		!favoriteSlicesEqual(got.Removed, want.Removed) ||
		len(got.RestoreBlocked) != 1 || got.RestoreBlocked[0] != (favoritePathIdentity{UserID: userID, Path: favoritePath}) {
		t.Fatalf("committed receipt blocking = %+v, want one block for %s:%s", got, userID, favoritePath)
	}
}

func TestStore_TrashDeleteOperationApplyPersistenceBoundaries(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "favorites.json")
	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	original, err := store.Add("user-a", "/docs/report.txt", "original")
	if err != nil {
		t.Fatalf("Add() error: %v", err)
	}
	planned, err := store.SnapshotDeleteExact(original.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact() error: %v", err)
	}

	originalWriter := favoritesStoreWriter
	hardFailure := errors.New("write failed before publish")
	favoritesStoreWriter = func(string, []byte) error { return hardFailure }
	err = store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, false)
	favoritesStoreWriter = originalWriter
	if !errors.Is(err, hardFailure) {
		t.Fatalf("ApplyTrashDeleteOperation(hard failure) error = %v, want %v", err, hardFailure)
	}
	if !store.IsFavorite(original.UserID, original.Path) || store.trashDeleteOperations[trashDeleteOperationA] != nil {
		t.Fatal("hard persistence failure changed committed memory state")
	}
	reopened, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(after hard failure) error: %v", err)
	}
	if !reopened.IsFavorite(original.UserID, original.Path) || reopened.trashDeleteOperations[trashDeleteOperationA] != nil {
		t.Fatal("hard persistence failure changed durable state")
	}

	originalSync := syncFavoritesStoreRootDir
	syncFailure := errors.New("directory sync failed after publish")
	syncFavoritesStoreRootDir = func(*os.Root) error { return syncFailure }
	err = store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, false)
	syncFavoritesStoreRootDir = originalSync
	if !IsPersistenceWarning(err) || !errors.Is(err, syncFailure) {
		t.Fatalf("ApplyTrashDeleteOperation(sync warning) error = %v, want persistence warning", err)
	}
	if store.IsFavorite(original.UserID, original.Path) || store.trashDeleteOperations[trashDeleteOperationA] == nil {
		t.Fatal("published persistence warning was not committed in memory")
	}
	reopened, err = NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(after warning) error: %v", err)
	}
	if reopened.IsFavorite(original.UserID, original.Path) || reopened.trashDeleteOperations[trashDeleteOperationA] == nil {
		t.Fatal("published persistence warning was not durable")
	}

	syncFavoritesStoreRootDir = func(*os.Root) error { return syncFailure }
	err = reopened.RollbackTrashDeleteOperation(trashDeleteOperationA)
	syncFavoritesStoreRootDir = originalSync
	if !IsPersistenceWarning(err) || !errors.Is(err, syncFailure) {
		t.Fatalf("RollbackTrashDeleteOperation(sync warning) error = %v, want persistence warning", err)
	}
	if !reopened.IsFavorite(original.UserID, original.Path) || reopened.trashDeleteOperations[trashDeleteOperationA] != nil {
		t.Fatal("rollback warning did not publish restoration and marker cleanup")
	}
	reopenedAfterRollback, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(after rollback warning) error: %v", err)
	}
	if !reopenedAfterRollback.IsFavorite(original.UserID, original.Path) || reopenedAfterRollback.trashDeleteOperations[trashDeleteOperationA] != nil {
		t.Fatal("rollback warning state did not survive reopen")
	}
	syncFavoritesStoreRootDir = func(*os.Root) error { return syncFailure }
	err = reopenedAfterRollback.ApplyTrashDeleteOperation(trashDeleteOperationB, planned, true)
	syncFavoritesStoreRootDir = originalSync
	if !IsPersistenceWarning(err) || !errors.Is(err, syncFailure) {
		t.Fatalf("ApplyTrashDeleteOperation(committed warning) error = %v, want persistence warning", err)
	}
	marker := reopenedAfterRollback.trashDeleteOperations[trashDeleteOperationB]
	if reopenedAfterRollback.IsFavorite(original.UserID, original.Path) || marker == nil || !marker.Committed {
		t.Fatal("committed warning did not publish deletion with its receipt")
	}
	reopenedAfterCommit, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(after committed warning) error: %v", err)
	}
	marker = reopenedAfterCommit.trashDeleteOperations[trashDeleteOperationB]
	if reopenedAfterCommit.IsFavorite(original.UserID, original.Path) || marker == nil || !marker.Committed {
		t.Fatal("committed warning receipt did not survive reopen")
	}

	t.Cleanup(func() {
		favoritesStoreWriter = originalWriter
		syncFavoritesStoreRootDir = originalSync
	})
}

func TestStore_TrashDeleteOperationConcurrentRecreateThenDeleteSuppressesRollback(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "favorites.json")
	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	original, err := store.Add("user-a", "/docs/report.txt", "original")
	if err != nil {
		t.Fatalf("Add() error: %v", err)
	}
	planned, err := store.SnapshotDeleteExact(original.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact() error: %v", err)
	}
	time.Sleep(time.Millisecond)

	originalWriter := favoritesStoreWriter
	applyStarted := make(chan struct{})
	applyRelease := make(chan struct{})
	var once sync.Once
	favoritesStoreWriter = func(path string, data []byte) error {
		once.Do(func() {
			close(applyStarted)
			<-applyRelease
		})
		return originalWriter(path, data)
	}
	t.Cleanup(func() {
		favoritesStoreWriter = originalWriter
		select {
		case <-applyRelease:
		default:
			close(applyRelease)
		}
	})

	applyDone := make(chan error, 1)
	go func() {
		applyDone <- store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, false)
	}()
	select {
	case <-applyStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for precommit persistence")
	}

	type addResult struct {
		favorite *Favorite
		err      error
	}
	addDone := make(chan addResult, 1)
	go func() {
		favorite, addErr := store.Add(original.UserID, original.Path, "recreated")
		addDone <- addResult{favorite: favorite, err: addErr}
	}()
	select {
	case result := <-addDone:
		t.Fatalf("concurrent rebuild completed before precommit publish: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}
	close(applyRelease)
	if err := <-applyDone; err != nil {
		t.Fatalf("ApplyTrashDeleteOperation() error: %v", err)
	}
	result := <-addDone
	if result.err != nil || result.favorite == nil {
		t.Fatalf("concurrent Add() = %+v", result)
	}
	if result.favorite.CreatedAt.Equal(original.CreatedAt) {
		t.Fatal("concurrent rebuild reused the deleted favorite identity")
	}
	if err := store.Remove(result.favorite.UserID, result.favorite.Path); err != nil {
		t.Fatalf("Remove(recreated) error: %v", err)
	}
	if err := store.RollbackTrashDeleteOperation(trashDeleteOperationA); err != nil {
		t.Fatalf("RollbackTrashDeleteOperation() error: %v", err)
	}
	if store.IsFavorite(original.UserID, original.Path) {
		t.Fatal("rollback restored the old favorite after a later rebuild and delete")
	}

	reopened, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(reopen) error: %v", err)
	}
	if reopened.IsFavorite(original.UserID, original.Path) || reopened.trashDeleteOperations[trashDeleteOperationA] != nil {
		t.Fatal("later rebuild/delete choice or marker cleanup was not durable")
	}
}

func TestStore_TrashDeleteOperationValidatesIDsAndPersistedMarkers(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "favorites.json")
	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	original, err := store.Add("user-b", "/docs/b.txt", "b")
	if err != nil {
		t.Fatalf("Add(user-b) error: %v", err)
	}
	second, err := store.Add("user-a", "/docs/a.txt", "a")
	if err != nil {
		t.Fatalf("Add(user-a) error: %v", err)
	}
	planned := []*Favorite{original, second}
	for _, invalidID := range []string{"", strings.Repeat("a", 31), strings.Repeat("A", 32), strings.Repeat("g", 32)} {
		if err := store.ApplyTrashDeleteOperation(invalidID, planned, false); !errors.Is(err, errInvalidTrashDeleteOperation) {
			t.Fatalf("ApplyTrashDeleteOperation(%q) error = %v, want invalid operation", invalidID, err)
		}
		if err := store.RollbackTrashDeleteOperation(invalidID); !errors.Is(err, errInvalidTrashDeleteOperation) {
			t.Fatalf("RollbackTrashDeleteOperation(%q) error = %v, want invalid operation", invalidID, err)
		}
		if err := store.CompleteTrashDeleteOperation(invalidID); !errors.Is(err, errInvalidTrashDeleteOperation) {
			t.Fatalf("CompleteTrashDeleteOperation(%q) error = %v, want invalid operation", invalidID, err)
		}
	}

	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation() error: %v", err)
	}
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile(store) error: %v", err)
	}
	var state favoritesStoreState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("Unmarshal(store) error: %v", err)
	}
	operation := state.TrashDeleteOperations[trashDeleteOperationA]
	if state.Version != favoritesStoreVersion || operation == nil || len(operation.Planned) != 2 {
		t.Fatalf("persisted state = %+v", state)
	}
	if operation.Planned[0].UserID != "user-a" || operation.Planned[1].UserID != "user-b" {
		t.Fatalf("persisted plan order = %+v, want canonical user/path order", operation.Planned)
	}

	operation.Removed = append(operation.Removed, &Favorite{
		UserID:    "user-z",
		Path:      "/outside.txt",
		CreatedAt: time.Unix(1, 0),
	})
	malformed, err := json.Marshal(&state)
	if err != nil {
		t.Fatalf("Marshal(malformed store) error: %v", err)
	}
	if err := os.WriteFile(storePath, malformed, 0o600); err != nil {
		t.Fatalf("WriteFile(malformed store) error: %v", err)
	}
	if _, err := NewStore(storePath); !errors.Is(err, errInvalidTrashDeleteOperation) {
		t.Fatalf("NewStore(malformed marker) error = %v, want invalid operation", err)
	}
}
