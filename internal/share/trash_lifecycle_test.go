package share

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

const (
	shareTrashDeleteOperationA  = "11111111111111111111111111111111"
	shareTrashRestoreOperationA = "22222222222222222222222222222222"
)

func TestShareStore_TrashRestoreDoesNotUndoDisableAfterPrepare(t *testing.T) {
	store := newTrashLifecycleShareStore(t)
	created := createTrashLifecycleShare(t, store, "/docs/disable-after-prepare.txt")
	planned := snapshotTrashLifecycleShares(t, store, created.Path)

	if err := store.Update(created.ID, func(current *Share) error {
		current.Enabled = false
		return nil
	}); err != nil {
		t.Fatalf("Update(disable after prepare) error: %v", err)
	}
	completeTrashLifecycleDelete(t, store, shareTrashDeleteOperationA, planned)

	if err := store.ApplyTrashRestoreOperation(
		shareTrashRestoreOperationA,
		shareTrashDeleteOperationA,
		planned,
		planned,
	); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation() error: %v", err)
	}
	current, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if current.Enabled {
		t.Fatalf("share explicitly disabled after prepare was restored: %+v", current)
	}
	receipt := store.trashRestoreOperations[shareTrashRestoreOperationA]
	if receipt == nil || len(receipt.Restored) != 0 {
		t.Fatalf("restore receipt = %+v, want no restored shares", receipt)
	}
}

func TestShareStore_TrashRestoreBlockSurvivesEnabledABAAndReopen(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "shares.json")
	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	created := createTrashLifecycleShare(t, store, "/docs/aba.txt")
	planned := snapshotTrashLifecycleShares(t, store, created.Path)

	if err := store.ApplyTrashDeleteOperation(shareTrashDeleteOperationA, planned, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(precommit) error: %v", err)
	}
	if err := store.Update(created.ID, func(current *Share) error {
		current.Enabled = true
		return nil
	}); err != nil {
		t.Fatalf("Update(enable) error: %v", err)
	}
	if err := store.Update(created.ID, func(current *Share) error {
		current.Enabled = false
		return nil
	}); err != nil {
		t.Fatalf("Update(disable) error: %v", err)
	}
	if err := store.ApplyTrashDeleteOperation(shareTrashDeleteOperationA, planned, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(committed) error: %v", err)
	}
	if err := store.CompleteTrashDeleteOperation(shareTrashDeleteOperationA); err != nil {
		t.Fatalf("CompleteTrashDeleteOperation() error: %v", err)
	}

	reopened, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reopen) error: %v", err)
	}
	relocated := cloneShareSlice(planned)
	relocated[0].Path = "/restored/custom.txt"
	if err := reopened.ApplyTrashRestoreOperation(
		shareTrashRestoreOperationA,
		shareTrashDeleteOperationA,
		planned,
		relocated,
	); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation() error: %v", err)
	}
	current, err := reopened.Get(created.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if current.Enabled || current.Path != created.Path {
		t.Fatalf("ABA-updated share changed by restore: %+v", current)
	}
	if err := reopened.CompleteTrashRestoreOperation(shareTrashRestoreOperationA); err != nil {
		t.Fatalf("CompleteTrashRestoreOperation() error: %v", err)
	}
	if _, exists := reopened.trashRestoreOperations[shareTrashRestoreOperationA]; exists {
		t.Fatal("restore receipt remained after completion")
	}
	if _, exists := reopened.trashDeleteOperations[shareTrashDeleteOperationA]; exists {
		t.Fatal("completed delete ownership remained after restore completion")
	}
}

func TestShareStore_TrashRestoreBlockSurvivesPathReferenceABA(t *testing.T) {
	store := newTrashLifecycleShareStore(t)
	created := createTrashLifecycleShare(t, store, "/docs/path-aba.txt")
	planned := snapshotTrashLifecycleShares(t, store, created.Path)
	completeTrashLifecycleDelete(t, store, shareTrashDeleteOperationA, planned)

	if err := store.UpdatePathReferences(created.Path, "/archive/path-aba.txt"); err != nil {
		t.Fatalf("UpdatePathReferences(move away) error: %v", err)
	}
	if err := store.UpdatePathReferences("/archive/path-aba.txt", created.Path); err != nil {
		t.Fatalf("UpdatePathReferences(move back) error: %v", err)
	}
	marker := store.trashDeleteOperations[shareTrashDeleteOperationA]
	if marker == nil || !stringSlicesEqual(marker.RestoreBlocked, []string{created.ID}) {
		t.Fatalf("delete ownership after path ABA = %+v, want durable restore guard", marker)
	}

	relocated := cloneShareSlice(planned)
	relocated[0].Path = "/restored/path-aba.txt"
	if err := store.ApplyTrashRestoreOperation(
		shareTrashRestoreOperationA,
		shareTrashDeleteOperationA,
		planned,
		relocated,
	); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation() error: %v", err)
	}
	current, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if current.Enabled || current.Path != created.Path {
		t.Fatalf("path-ABA share changed by restore: %+v", current)
	}
}

func TestShareStore_TrashRestoreUsesOnlyChangedOwnershipAtCustomPath(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "shares.json")
	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	created := createTrashLifecycleShare(t, store, "/docs/custom.txt")
	planned := snapshotTrashLifecycleShares(t, store, created.Path)
	completeTrashLifecycleDelete(t, store, shareTrashDeleteOperationA, planned)

	reopened, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reopen) error: %v", err)
	}
	relocated := cloneShareSlice(planned)
	relocated[0].Path = "/restored/custom.txt"
	if err := reopened.ApplyTrashRestoreOperation(
		shareTrashRestoreOperationA,
		shareTrashDeleteOperationA,
		planned,
		relocated,
	); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation() error: %v", err)
	}
	current, err := reopened.Get(created.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !current.Enabled || current.Path != relocated[0].Path {
		t.Fatalf("restored share = %+v, want enabled custom path", current)
	}
	receipt := reopened.trashRestoreOperations[shareTrashRestoreOperationA]
	if receipt == nil || receipt.DeleteOperationID != shareTrashDeleteOperationA ||
		len(receipt.Restored) != 1 || receipt.Restored[0].Path != relocated[0].Path {
		t.Fatalf("restore receipt = %+v", receipt)
	}
}

func TestShareStore_PurgeCompletedTrashDeleteOperation(t *testing.T) {
	store := newTrashLifecycleShareStore(t)
	created := createTrashLifecycleShare(t, store, "/docs/purge.txt")
	planned := snapshotTrashLifecycleShares(t, store, created.Path)

	if err := store.ApplyTrashDeleteOperation(shareTrashDeleteOperationA, planned, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(precommit) error: %v", err)
	}
	if err := store.PurgeCompletedTrashDeleteOperation(shareTrashDeleteOperationA); !errors.Is(err, ErrTrashDeleteOperationConflict) {
		t.Fatalf("PurgeCompletedTrashDeleteOperation(pending) error = %v, want conflict", err)
	}
	if err := store.ApplyTrashDeleteOperation(shareTrashDeleteOperationA, planned, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(committed) error: %v", err)
	}
	if err := store.PurgeCompletedTrashDeleteOperation(shareTrashDeleteOperationA); !errors.Is(err, ErrTrashDeleteOperationConflict) {
		t.Fatalf("PurgeCompletedTrashDeleteOperation(committed) error = %v, want conflict", err)
	}
	if err := store.CompleteTrashDeleteOperation(shareTrashDeleteOperationA); err != nil {
		t.Fatalf("CompleteTrashDeleteOperation() error: %v", err)
	}
	if err := store.PurgeCompletedTrashDeleteOperation(shareTrashDeleteOperationA); err != nil {
		t.Fatalf("PurgeCompletedTrashDeleteOperation(completed) error: %v", err)
	}
	if err := store.PurgeCompletedTrashDeleteOperation(shareTrashDeleteOperationA); err != nil {
		t.Fatalf("PurgeCompletedTrashDeleteOperation(missing) error: %v", err)
	}
	if _, exists := store.trashDeleteOperations[shareTrashDeleteOperationA]; exists {
		t.Fatal("purged delete ownership remained")
	}
	if err := store.ApplyTrashRestoreOperation(
		shareTrashRestoreOperationA,
		shareTrashDeleteOperationA,
		planned,
		planned,
	); !errors.Is(err, ErrTrashRestoreOperationConflict) {
		t.Fatalf("ApplyTrashRestoreOperation(after purge) error = %v, want conflict", err)
	}
}

func newTrashLifecycleShareStore(t *testing.T) *ShareStore {
	t.Helper()
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	return store
}

func createTrashLifecycleShare(t *testing.T, store *ShareStore, sharePath string) *Share {
	t.Helper()
	created, err := store.Create(CreateShareOptions{
		Path:        sharePath,
		Type:        ShareTypeFile,
		CreatedBy:   "user-a",
		Description: strings.TrimPrefix(sharePath, "/"),
	})
	if err != nil {
		t.Fatalf("Create(%s) error: %v", sharePath, err)
	}
	return created
}

func snapshotTrashLifecycleShares(t *testing.T, store *ShareStore, targetPath string) []*Share {
	t.Helper()
	planned, err := store.SnapshotDeleteExact(targetPath)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact(%s) error: %v", targetPath, err)
	}
	return planned
}

func completeTrashLifecycleDelete(t *testing.T, store *ShareStore, operationID string, planned []*Share) {
	t.Helper()
	if err := store.ApplyTrashDeleteOperation(operationID, planned, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(precommit) error: %v", err)
	}
	if err := store.ApplyTrashDeleteOperation(operationID, planned, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(committed) error: %v", err)
	}
	if err := store.CompleteTrashDeleteOperation(operationID); err != nil {
		t.Fatalf("CompleteTrashDeleteOperation() error: %v", err)
	}
}
