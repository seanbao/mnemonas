package favorites

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

const (
	trashDeleteOperationC = "cccccccccccccccccccccccccccccccc"
	trashDeleteOperationD = "dddddddddddddddddddddddddddddddd"
)

func TestStore_CompletedTrashDeleteOwnershipSurvivesReopen(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "favorites.json")
	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	original, err := store.Add("user-a", "/docs/report.txt", "original")
	if err != nil {
		t.Fatalf("Add() error: %v", err)
	}
	planned := []*Favorite{copyFavorite(original)}

	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation() error: %v", err)
	}
	if err := store.CompleteTrashDeleteOperation(trashDeleteOperationA); err != nil {
		t.Fatalf("CompleteTrashDeleteOperation() error: %v", err)
	}
	completed := store.trashDeleteOperations[trashDeleteOperationA]
	if completed == nil || !completed.Completed {
		t.Fatalf("completed delete ownership = %+v, want retained completed marker", completed)
	}

	reopened, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(reopen) error: %v", err)
	}
	completed = reopened.trashDeleteOperations[trashDeleteOperationA]
	if completed == nil || !completed.Completed {
		t.Fatalf("reopened delete ownership = %+v, want completed marker", completed)
	}
}

func TestStore_TrashRestoreDoesNotResurrectAfterUserAddThenRemove(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	original, err := store.Add("user-a", "/docs/report.txt", "original")
	if err != nil {
		t.Fatalf("Add(original) error: %v", err)
	}
	planned := []*Favorite{copyFavorite(original)}

	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation() error: %v", err)
	}
	if err := store.CompleteTrashDeleteOperation(trashDeleteOperationA); err != nil {
		t.Fatalf("CompleteTrashDeleteOperation() error: %v", err)
	}
	if _, err := store.Add(original.UserID, original.Path, "later user favorite"); err != nil {
		t.Fatalf("Add(later) error: %v", err)
	}
	if err := store.Remove(original.UserID, original.Path); err != nil {
		t.Fatalf("Remove(later) error: %v", err)
	}

	reopened, err := NewStore(store.filePath)
	if err != nil {
		t.Fatalf("NewStore(reopen) error: %v", err)
	}
	if err := reopened.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationA, planned, planned); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation() error: %v", err)
	}
	if reopened.IsFavorite(original.UserID, original.Path) {
		t.Fatal("restore resurrected a favorite after a later user add and remove")
	}
}

func TestStore_UserMutationsBlockCompletedTrashDeleteOwnership(t *testing.T) {
	for _, test := range []struct {
		name        string
		seedCurrent bool
		mutate      func(*Store, *Favorite) error
	}{
		{
			name: "add",
			mutate: func(store *Store, current *Favorite) error {
				_, err := store.Add(current.UserID, current.Path, "added later")
				return err
			},
		},
		{
			name:        "remove",
			seedCurrent: true,
			mutate: func(store *Store, current *Favorite) error {
				return store.Remove(current.UserID, current.Path)
			},
		},
		{
			name:        "update note",
			seedCurrent: true,
			mutate: func(store *Store, current *Favorite) error {
				return store.UpdateNote(current.UserID, current.Path, "updated later")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			storePath := filepath.Join(t.TempDir(), "favorites.json")
			original := &Favorite{
				UserID:    "user-a",
				Path:      "/docs/report.txt",
				CreatedAt: time.Unix(1, 0).UTC(),
				Note:      "removed original",
			}
			current := &Favorite{
				UserID:    original.UserID,
				Path:      original.Path,
				CreatedAt: time.Unix(2, 0).UTC(),
				Note:      "current favorite",
			}
			data := map[string]map[string]*Favorite{}
			if test.seedCurrent {
				data[current.UserID] = map[string]*Favorite{current.Path: copyFavorite(current)}
			}
			if err := saveFavoritesState(
				storePath,
				data,
				map[string]*trashDeleteOperation{
					trashDeleteOperationA: {
						Planned:        []*Favorite{copyFavorite(original)},
						Removed:        []*Favorite{copyFavorite(original)},
						RestoreBlocked: []favoritePathIdentity{},
						Committed:      true,
						Completed:      true,
					},
				},
				map[string]*trashRestoreOperation{},
			); err != nil {
				t.Fatalf("saveFavoritesState() error: %v", err)
			}
			store, err := NewStore(storePath)
			if err != nil {
				t.Fatalf("NewStore() error: %v", err)
			}
			if err := test.mutate(store, current); err != nil {
				t.Fatalf("mutation error: %v", err)
			}
			operation := store.trashDeleteOperations[trashDeleteOperationA]
			wantBlock := favoritePathIdentity{UserID: original.UserID, Path: original.Path}
			if operation == nil || !operation.Completed || len(operation.RestoreBlocked) != 1 || operation.RestoreBlocked[0] != wantBlock {
				t.Fatalf("completed ownership after mutation = %+v, want block %+v", operation, wantBlock)
			}
			reopened, err := NewStore(storePath)
			if err != nil {
				t.Fatalf("NewStore(reopen) error: %v", err)
			}
			operation = reopened.trashDeleteOperations[trashDeleteOperationA]
			if operation == nil || len(operation.RestoreBlocked) != 1 || operation.RestoreBlocked[0] != wantBlock {
				t.Fatalf("reopened completed ownership = %+v, want durable block %+v", operation, wantBlock)
			}
		})
	}
}

func TestStore_TrashRestoreUsesCompletedDeleteOwnership(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "favorites.json")
	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	original, err := store.Add("user-a", "/docs/report.txt", "planned note")
	if err != nil {
		t.Fatalf("Add() error: %v", err)
	}
	planned := []*Favorite{copyFavorite(original)}
	if err := store.UpdateNote(original.UserID, original.Path, "latest note at delete"); err != nil {
		t.Fatalf("UpdateNote() error: %v", err)
	}
	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation() error: %v", err)
	}
	if err := store.CompleteTrashDeleteOperation(trashDeleteOperationA); err != nil {
		t.Fatalf("CompleteTrashDeleteOperation() error: %v", err)
	}

	if err := store.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationA, planned, planned); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation() error: %v", err)
	}
	wantRestored := copyFavorite(original)
	wantRestored.Note = "latest note at delete"
	restored := store.List(original.UserID)
	if len(restored) != 1 || !favoriteFullEqual(restored[0], wantRestored) {
		t.Fatalf("restored favorites = %+v, want %+v", restored, wantRestored)
	}
	receipt := store.trashRestoreOperations[trashDeleteOperationB]
	if receipt == nil || receipt.DeleteOperationID != trashDeleteOperationA ||
		!favoriteSlicesEqual(receipt.Original, planned) || !favoriteSlicesEqual(receipt.Relocated, planned) ||
		!favoriteSlicesEqual(receipt.Restored, []*Favorite{wantRestored}) {
		t.Fatalf("restore receipt = %+v", receipt)
	}

	if err := store.CompleteTrashRestoreOperation(trashDeleteOperationB); err != nil {
		t.Fatalf("CompleteTrashRestoreOperation() error: %v", err)
	}
	if store.trashRestoreOperations[trashDeleteOperationB] != nil || store.trashDeleteOperations[trashDeleteOperationA] != nil {
		t.Fatal("restore completion did not atomically remove both ownership records")
	}
	reopened, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(reopen) error: %v", err)
	}
	if !reopened.IsFavorite(original.UserID, original.Path) {
		t.Fatal("restored favorite did not survive reopen")
	}
}

func TestStore_TrashRestoreRelocatesOwnedFavoriteToCustomRoot(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	original, err := store.Add("user-a", "/old-root/docs/report.txt", "latest note at delete")
	if err != nil {
		t.Fatalf("Add() error: %v", err)
	}
	originalPlan := []*Favorite{copyFavorite(original)}
	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, originalPlan, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation() error: %v", err)
	}
	if err := store.CompleteTrashDeleteOperation(trashDeleteOperationA); err != nil {
		t.Fatalf("CompleteTrashDeleteOperation() error: %v", err)
	}
	relocated := cloneFavoriteSlice(originalPlan)
	relocated[0].Path = "/custom-root/docs/report.txt"

	if err := store.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationA, originalPlan, relocated); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation() error: %v", err)
	}
	if store.IsFavorite(original.UserID, original.Path) {
		t.Fatal("restore recreated the favorite at its original path")
	}
	restored := store.List(original.UserID)
	if len(restored) != 1 || restored[0].Path != relocated[0].Path || restored[0].Note != original.Note || !restored[0].CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("relocated favorite = %+v, want target path with removed metadata", restored)
	}
	receipt := store.trashRestoreOperations[trashDeleteOperationB]
	if receipt == nil || !favoriteSlicesEqual(receipt.Original, originalPlan) ||
		!favoriteSlicesEqual(receipt.Relocated, relocated) || !favoriteSlicesEqual(receipt.Restored, restored) {
		t.Fatalf("custom-root restore receipt = %+v", receipt)
	}
}

func TestStore_TrashRestoreRejectsWrongDeleteOperationID(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	original, err := store.Add("user-a", "/docs/report.txt", "original")
	if err != nil {
		t.Fatalf("Add() error: %v", err)
	}
	planned := []*Favorite{copyFavorite(original)}
	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation() error: %v", err)
	}
	if err := store.CompleteTrashDeleteOperation(trashDeleteOperationA); err != nil {
		t.Fatalf("CompleteTrashDeleteOperation() error: %v", err)
	}

	if err := store.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationC, planned, planned); !errors.Is(err, ErrTrashRestoreOperationConflict) {
		t.Fatalf("ApplyTrashRestoreOperation(wrong delete ID) error = %v, want conflict", err)
	}
	if err := store.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationA, planned, planned); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation(correct) error: %v", err)
	}
	if err := store.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationC, planned, planned); !errors.Is(err, ErrTrashRestoreOperationConflict) {
		t.Fatalf("ApplyTrashRestoreOperation(replay wrong delete ID) error = %v, want conflict", err)
	}
}

func TestStore_PruneCompletedTrashDeleteOperationsIsExplicit(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	completeEmptyTrashDeleteOperation(t, store, trashDeleteOperationA)
	completeEmptyTrashDeleteOperation(t, store, trashDeleteOperationB)
	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationC, nil, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(pending) error: %v", err)
	}
	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationD, nil, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(committed) error: %v", err)
	}

	if err := store.PruneCompletedTrashDeleteOperations([]string{trashDeleteOperationA}); err != nil {
		t.Fatalf("PruneCompletedTrashDeleteOperations() error: %v", err)
	}
	if operation := store.trashDeleteOperations[trashDeleteOperationA]; operation == nil || !operation.Completed {
		t.Fatalf("active completed operation = %+v, want retained", operation)
	}
	if operation := store.trashDeleteOperations[trashDeleteOperationB]; operation != nil {
		t.Fatalf("inactive completed operation = %+v, want pruned", operation)
	}
	if operation := store.trashDeleteOperations[trashDeleteOperationC]; operation == nil || operation.Committed {
		t.Fatalf("pending operation = %+v, want retained", operation)
	}
	if operation := store.trashDeleteOperations[trashDeleteOperationD]; operation == nil || !operation.Committed || operation.Completed {
		t.Fatalf("committed operation = %+v, want retained and incomplete", operation)
	}
	reopened, err := NewStore(store.filePath)
	if err != nil {
		t.Fatalf("NewStore(reopen) error: %v", err)
	}
	if reopened.trashDeleteOperations[trashDeleteOperationB] != nil ||
		reopened.trashDeleteOperations[trashDeleteOperationA] == nil ||
		reopened.trashDeleteOperations[trashDeleteOperationC] == nil ||
		reopened.trashDeleteOperations[trashDeleteOperationD] == nil {
		t.Fatalf("reopened pruned operations = %+v", reopened.trashDeleteOperations)
	}
}

func TestStore_PurgeCompletedTrashDeleteOperation(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	completeEmptyTrashDeleteOperation(t, store, trashDeleteOperationA)
	if err := store.PurgeCompletedTrashDeleteOperation(trashDeleteOperationA); err != nil {
		t.Fatalf("PurgeCompletedTrashDeleteOperation() error: %v", err)
	}
	if store.trashDeleteOperations[trashDeleteOperationA] != nil {
		t.Fatal("purged completed operation remains")
	}
	if err := store.PurgeCompletedTrashDeleteOperation(trashDeleteOperationA); err != nil {
		t.Fatalf("PurgeCompletedTrashDeleteOperation(idempotent) error: %v", err)
	}

	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationB, nil, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(pending) error: %v", err)
	}
	if err := store.PurgeCompletedTrashDeleteOperation(trashDeleteOperationB); !errors.Is(err, ErrTrashDeleteOperationConflict) {
		t.Fatalf("PurgeCompletedTrashDeleteOperation(pending) error = %v, want conflict", err)
	}
	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationC, nil, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(committed) error: %v", err)
	}
	if err := store.PurgeCompletedTrashDeleteOperation(trashDeleteOperationC); !errors.Is(err, ErrTrashDeleteOperationConflict) {
		t.Fatalf("PurgeCompletedTrashDeleteOperation(committed) error = %v, want conflict", err)
	}
	if err := store.CompleteTrashDeleteOperation(trashDeleteOperationC); err != nil {
		t.Fatalf("CompleteTrashDeleteOperation() error: %v", err)
	}
	if err := store.ApplyTrashRestoreOperation(trashDeleteOperationD, trashDeleteOperationC, nil, nil); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation() error: %v", err)
	}
	if err := store.PurgeCompletedTrashDeleteOperation(trashDeleteOperationC); !errors.Is(err, ErrTrashDeleteOperationConflict) {
		t.Fatalf("PurgeCompletedTrashDeleteOperation(restore-owned) error = %v, want conflict", err)
	}
}

func completeEmptyTrashDeleteOperation(t *testing.T, store *Store, operationID string) {
	t.Helper()
	if err := store.ApplyTrashDeleteOperation(operationID, nil, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(%s) error: %v", operationID, err)
	}
	if err := store.CompleteTrashDeleteOperation(operationID); err != nil {
		t.Fatalf("CompleteTrashDeleteOperation(%s) error: %v", operationID, err)
	}
}
