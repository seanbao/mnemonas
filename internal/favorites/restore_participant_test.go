package favorites

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStore_TrashRestoreOperationPersistsReceiptAndProtectsUserChanges(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "favorites.json")
	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	current, err := store.Add("user-a", "/docs/current.txt", "current note")
	if err != nil {
		t.Fatalf("Add(current) error: %v", err)
	}
	planned := []*Favorite{
		{
			UserID:    "user-b",
			Path:      "/docs/restored.txt",
			CreatedAt: time.Unix(20, 0).UTC(),
			Note:      "restored note",
		},
		{
			UserID:    current.UserID,
			Path:      current.Path,
			CreatedAt: time.Unix(10, 0).UTC(),
			Note:      "stale planned note",
		},
	}
	if err := store.RestoreFavoritesIfMissing(planned); err != nil {
		t.Fatalf("RestoreFavoritesIfMissing(setup) error: %v", err)
	}
	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(setup) error: %v", err)
	}
	if err := store.CompleteTrashDeleteOperation(trashDeleteOperationA); err != nil {
		t.Fatalf("CompleteTrashDeleteOperation(setup) error: %v", err)
	}

	if err := store.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationA, planned, planned); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation(first) error: %v", err)
	}
	receipt := cloneTrashRestoreOperation(store.trashRestoreOperations[trashDeleteOperationB])
	if receipt == nil || receipt.DeleteOperationID != trashDeleteOperationA || len(receipt.Original) != 2 || len(receipt.Relocated) != 2 || len(receipt.Restored) != 1 || receipt.Restored[0].UserID != "user-b" {
		t.Fatalf("restore receipt = %+v, want canonical plan", receipt)
	}
	currentFavorites := store.List(current.UserID)
	if len(currentFavorites) != 1 || currentFavorites[0].Note != "current note" || !currentFavorites[0].CreatedAt.Equal(current.CreatedAt) {
		t.Fatalf("existing favorite was replaced by restore: %+v", currentFavorites)
	}
	if !store.IsFavorite("user-b", "/docs/restored.txt") {
		t.Fatal("missing planned favorite was not restored")
	}

	reopened, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(reopen with receipt) error: %v", err)
	}
	reopenedReceipt := reopened.trashRestoreOperations[trashDeleteOperationB]
	if reopenedReceipt == nil || reopenedReceipt.DeleteOperationID != receipt.DeleteOperationID ||
		!favoriteSlicesEqual(reopenedReceipt.Original, receipt.Original) ||
		!favoriteSlicesEqual(reopenedReceipt.Relocated, receipt.Relocated) ||
		!favoriteSlicesEqual(reopenedReceipt.Restored, receipt.Restored) {
		t.Fatalf("reopened receipt = %+v, want %+v", reopenedReceipt, receipt)
	}
	if err := reopened.Remove("user-b", "/docs/restored.txt"); err != nil {
		t.Fatalf("Remove(restored favorite) error: %v", err)
	}
	if err := reopened.UpdateNote(current.UserID, current.Path, "user updated note"); err != nil {
		t.Fatalf("UpdateNote(current favorite) error: %v", err)
	}
	if err := reopened.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationA, planned, planned); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation(replay) error: %v", err)
	}
	if reopened.IsFavorite("user-b", "/docs/restored.txt") {
		t.Fatal("restore replay resurrected a favorite deleted after the first apply")
	}
	updated := reopened.List(current.UserID)
	if len(updated) != 1 || updated[0].Note != "user updated note" {
		t.Fatalf("restore replay changed a user-updated favorite: %+v", updated)
	}

	mismatched := cloneFavoriteSlice(planned)
	mismatched[0].Note = "different plan"
	if err := reopened.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationA, mismatched, mismatched); !errors.Is(err, ErrTrashRestoreOperationConflict) {
		t.Fatalf("ApplyTrashRestoreOperation(mismatch) error = %v, want conflict", err)
	}
	if err := reopened.CompleteTrashRestoreOperation(trashDeleteOperationB); err != nil {
		t.Fatalf("CompleteTrashRestoreOperation() error: %v", err)
	}
	if _, exists := reopened.trashRestoreOperations[trashDeleteOperationB]; exists {
		t.Fatal("restore receipt remains after completion")
	}
	if _, exists := reopened.trashDeleteOperations[trashDeleteOperationA]; exists {
		t.Fatal("delete ownership remains after restore completion")
	}
	if err := reopened.CompleteTrashRestoreOperation(trashDeleteOperationB); err != nil {
		t.Fatalf("CompleteTrashRestoreOperation(idempotent) error: %v", err)
	}

	reopenedAgain, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(reopen after completion) error: %v", err)
	}
	if _, exists := reopenedAgain.trashRestoreOperations[trashDeleteOperationB]; exists {
		t.Fatal("completed restore receipt survived reopen")
	}
	if reopenedAgain.IsFavorite("user-b", "/docs/restored.txt") {
		t.Fatal("user deletion after restore did not survive reopen")
	}
}

func TestStore_TrashRestoreOperationPersistsEmptyPlanReceipt(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "favorites.json")
	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	completeEmptyTrashDeleteOperation(t, store, trashDeleteOperationA)
	if err := store.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationA, nil, nil); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation(empty) error: %v", err)
	}
	receipt, exists := store.trashRestoreOperations[trashDeleteOperationB]
	if !exists || receipt == nil || receipt.DeleteOperationID != trashDeleteOperationA || len(receipt.Original) != 0 || len(receipt.Relocated) != 0 || len(receipt.Restored) != 0 {
		t.Fatalf("empty restore receipt = %#v, exists=%v", receipt, exists)
	}

	mismatched := []*Favorite{{
		UserID:    "user-a",
		Path:      "/docs/later.txt",
		CreatedAt: time.Unix(30, 0).UTC(),
	}}
	if err := store.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationA, mismatched, mismatched); !errors.Is(err, ErrTrashRestoreOperationConflict) {
		t.Fatalf("ApplyTrashRestoreOperation(reused empty receipt) error = %v, want conflict", err)
	}
}

func TestStore_TrashRestoreOperationPersistenceBoundaries(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "favorites.json")
	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	planned := []*Favorite{{
		UserID:    "user-a",
		Path:      "/docs/restored.txt",
		CreatedAt: time.Unix(40, 0).UTC(),
		Note:      "planned",
	}}
	if err := store.RestoreFavorites(planned); err != nil {
		t.Fatalf("RestoreFavorites(setup) error: %v", err)
	}
	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(setup) error: %v", err)
	}
	if err := store.CompleteTrashDeleteOperation(trashDeleteOperationA); err != nil {
		t.Fatalf("CompleteTrashDeleteOperation(setup) error: %v", err)
	}

	originalWriter := favoritesStoreWriter
	originalSync := syncFavoritesStoreRootDir
	t.Cleanup(func() {
		favoritesStoreWriter = originalWriter
		syncFavoritesStoreRootDir = originalSync
	})

	hardFailure := errors.New("write failed before publish")
	favoritesStoreWriter = func(string, []byte) error { return hardFailure }
	err = store.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationA, planned, planned)
	favoritesStoreWriter = originalWriter
	if !errors.Is(err, hardFailure) {
		t.Fatalf("ApplyTrashRestoreOperation(hard failure) error = %v, want %v", err, hardFailure)
	}
	if store.IsFavorite("user-a", "/docs/restored.txt") {
		t.Fatal("hard persistence failure published the restored favorite")
	}
	if _, exists := store.trashRestoreOperations[trashDeleteOperationB]; exists {
		t.Fatal("hard persistence failure published a restore receipt")
	}

	syncFailure := errors.New("directory sync failed after publish")
	syncFavoritesStoreRootDir = func(*os.Root) error { return syncFailure }
	err = store.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationA, planned, planned)
	syncFavoritesStoreRootDir = originalSync
	if !IsPersistenceWarning(err) || !errors.Is(err, syncFailure) {
		t.Fatalf("ApplyTrashRestoreOperation(sync warning) error = %v, want persistence warning", err)
	}
	if !store.IsFavorite("user-a", "/docs/restored.txt") {
		t.Fatal("published persistence warning did not commit the restored favorite")
	}
	if _, exists := store.trashRestoreOperations[trashDeleteOperationB]; !exists {
		t.Fatal("published persistence warning did not commit the restore receipt")
	}

	reopened, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(after warning) error: %v", err)
	}
	if !reopened.IsFavorite("user-a", "/docs/restored.txt") {
		t.Fatal("restored favorite did not survive reopen after persistence warning")
	}
	if _, exists := reopened.trashRestoreOperations[trashDeleteOperationB]; !exists {
		t.Fatal("restore receipt did not survive reopen after persistence warning")
	}
}

func TestStore_TrashRestoreOperationValidatesIDsAndPersistedReceipts(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "favorites.json")
	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	planned := []*Favorite{{
		UserID:    "user-a",
		Path:      "/docs/a.txt",
		CreatedAt: time.Unix(1, 0).UTC(),
	}}
	for _, invalidID := range []string{"", strings.Repeat("a", 31), strings.Repeat("A", 32), strings.Repeat("g", 32)} {
		if err := store.ApplyTrashRestoreOperation(invalidID, trashDeleteOperationA, planned, planned); !errors.Is(err, errInvalidTrashRestoreOperation) {
			t.Fatalf("ApplyTrashRestoreOperation(%q) error = %v, want invalid operation", invalidID, err)
		}
		if err := store.ApplyTrashRestoreOperation(trashDeleteOperationB, invalidID, planned, planned); !errors.Is(err, errInvalidTrashRestoreOperation) {
			t.Fatalf("ApplyTrashRestoreOperation(delete %q) error = %v, want invalid operation", invalidID, err)
		}
		if err := store.CompleteTrashRestoreOperation(invalidID); !errors.Is(err, errInvalidTrashRestoreOperation) {
			t.Fatalf("CompleteTrashRestoreOperation(%q) error = %v, want invalid operation", invalidID, err)
		}
	}

	if err := store.RestoreFavorites(planned); err != nil {
		t.Fatalf("RestoreFavorites(setup) error: %v", err)
	}
	if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, planned, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(setup) error: %v", err)
	}
	if err := store.CompleteTrashDeleteOperation(trashDeleteOperationA); err != nil {
		t.Fatalf("CompleteTrashDeleteOperation(setup) error: %v", err)
	}
	if err := store.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationA, planned, planned); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation() error: %v", err)
	}
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile(store) error: %v", err)
	}
	var state favoritesStoreState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("Unmarshal(store) error: %v", err)
	}
	receipt := state.TrashRestoreOperations[trashDeleteOperationB]
	if receipt == nil || receipt.DeleteOperationID != trashDeleteOperationA ||
		!favoriteSlicesEqual(receipt.Original, planned) || !favoriteSlicesEqual(receipt.Relocated, planned) || !favoriteSlicesEqual(receipt.Restored, planned) {
		t.Fatalf("persisted restore receipt = %+v", receipt)
	}

	receipt.Restored = append(receipt.Restored, copyFavorite(receipt.Restored[0]))
	malformed, err := json.Marshal(&state)
	if err != nil {
		t.Fatalf("Marshal(malformed store) error: %v", err)
	}
	if err := os.WriteFile(storePath, malformed, 0o600); err != nil {
		t.Fatalf("WriteFile(malformed store) error: %v", err)
	}
	if _, err := NewStore(storePath); !errors.Is(err, errInvalidTrashRestoreOperation) {
		t.Fatalf("NewStore(malformed restore receipt) error = %v, want invalid operation", err)
	}
}
