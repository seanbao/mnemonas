package share

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShareStore_TrashRestoreReceiptProtectsLaterUpdatesAcrossReopen(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "shares.json")
	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	created := createTrashLifecycleShare(t, store, "/docs/report.txt")
	original := snapshotTrashLifecycleShares(t, store, created.Path)
	deleteOperationID := strings.Repeat("a", 32)
	restoreOperationID := strings.Repeat("b", 32)
	completeTrashLifecycleDelete(t, store, deleteOperationID, original)
	relocated := cloneShareSlice(original)

	if err := store.ApplyTrashRestoreOperation(restoreOperationID, deleteOperationID, original, relocated); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation(first) error: %v", err)
	}
	restored, err := store.Get(created.ID)
	if err != nil || !restored.Enabled || restored.Path != created.Path {
		t.Fatalf("first restored share = (%+v, %v)", restored, err)
	}
	wantReceipt := &shareTrashRestoreOperation{
		DeleteOperationID: deleteOperationID,
		Original:          original,
		Relocated:         relocated,
		Restored:          []*Share{copyShare(restored)},
	}
	receipt := readShareStoreFileForParticipantTest(t, storePath).TrashRestoreOperations[restoreOperationID]
	if !shareTrashRestoreOperationsEqual(receipt, wantReceipt) {
		t.Fatalf("persisted restore receipt = %+v, want %+v", receipt, wantReceipt)
	}

	if err := store.Update(created.ID, func(updated *Share) error {
		updated.Path = "/archive/user-moved.txt"
		updated.Enabled = false
		updated.Description = "newest-after-restore"
		updated.AccessCount = 9
		return nil
	}); err != nil {
		t.Fatalf("Update(after restore) error: %v", err)
	}
	if err := store.ApplyTrashRestoreOperation(restoreOperationID, deleteOperationID, original, relocated); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation(replay) error: %v", err)
	}
	assertTrashRestoreReplayPreservedShare(t, store, created.ID)

	reopened, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reopen) error: %v", err)
	}
	if err := reopened.ApplyTrashRestoreOperation(restoreOperationID, deleteOperationID, original, relocated); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation(reopen replay) error: %v", err)
	}
	assertTrashRestoreReplayPreservedShare(t, reopened, created.ID)

	mismatched := cloneShareSlice(relocated)
	mismatched[0].Path = "/different-target.txt"
	if err := reopened.ApplyTrashRestoreOperation(restoreOperationID, deleteOperationID, original, mismatched); !errors.Is(err, ErrTrashRestoreOperationConflict) {
		t.Fatalf("ApplyTrashRestoreOperation(mismatch) error = %v, want conflict", err)
	}
	assertTrashRestoreReplayPreservedShare(t, reopened, created.ID)

	if err := reopened.CompleteTrashRestoreOperation(restoreOperationID); err != nil {
		t.Fatalf("CompleteTrashRestoreOperation() error: %v", err)
	}
	if err := reopened.CompleteTrashRestoreOperation(restoreOperationID); err != nil {
		t.Fatalf("CompleteTrashRestoreOperation(idempotent) error: %v", err)
	}
	state := readShareStoreFileForParticipantTest(t, storePath)
	if _, exists := state.TrashRestoreOperations[restoreOperationID]; exists {
		t.Fatal("restore receipt remained after completion")
	}
	if _, exists := state.TrashDeleteOperations[deleteOperationID]; exists {
		t.Fatal("completed delete ownership remained after restore completion")
	}
}

func TestShareStore_TrashRestoreFirstApplyDoesNotRecreateUserDeletedShare(t *testing.T) {
	store := newTrashLifecycleShareStore(t)
	created := createTrashLifecycleShare(t, store, "/docs/missing.txt")
	original := snapshotTrashLifecycleShares(t, store, created.Path)
	completeTrashLifecycleDelete(t, store, shareTrashDeleteOperationA, original)
	if err := store.Delete(created.ID); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	if err := store.ApplyTrashRestoreOperation(
		shareTrashRestoreOperationA,
		shareTrashDeleteOperationA,
		original,
		original,
	); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation() error: %v", err)
	}
	if _, err := store.Get(created.ID); !errors.Is(err, ErrShareNotFound) {
		t.Fatalf("Get(user-deleted share) error = %v, want ErrShareNotFound", err)
	}
	receipt := store.trashRestoreOperations[shareTrashRestoreOperationA]
	if receipt == nil || len(receipt.Restored) != 0 {
		t.Fatalf("restore receipt = %+v, want empty restored set", receipt)
	}
}

func TestShareStore_TrashRestoreFirstApplyPreservesUserChangedPathAndEnabledState(t *testing.T) {
	store := newTrashLifecycleShareStore(t)
	created := createTrashLifecycleShare(t, store, "/docs/moved.txt")
	original := snapshotTrashLifecycleShares(t, store, created.Path)
	completeTrashLifecycleDelete(t, store, shareTrashDeleteOperationA, original)
	if err := store.Update(created.ID, func(updated *Share) error {
		updated.Path = "/archive/user-moved.txt"
		updated.Enabled = false
		updated.Description = "user changed while item was in Trash"
		return nil
	}); err != nil {
		t.Fatalf("Update(user state) error: %v", err)
	}

	if err := store.ApplyTrashRestoreOperation(
		shareTrashRestoreOperationA,
		shareTrashDeleteOperationA,
		original,
		original,
	); err != nil {
		t.Fatalf("ApplyTrashRestoreOperation() error: %v", err)
	}
	current, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if current.Path != "/archive/user-moved.txt" || current.Enabled ||
		current.Description != "user changed while item was in Trash" {
		t.Fatalf("user state changed by first restore Apply: %+v", current)
	}
}

func TestShareStore_TrashRestoreValidationAndStrictLoading(t *testing.T) {
	store := newTrashLifecycleShareStore(t)
	if err := store.ApplyTrashRestoreOperation(strings.Repeat("A", 32), shareTrashDeleteOperationA, nil, nil); !errors.Is(err, errInvalidTrashRestoreOperation) {
		t.Fatalf("ApplyTrashRestoreOperation(uppercase restore ID) error = %v, want invalid operation", err)
	}
	if err := store.ApplyTrashRestoreOperation(shareTrashRestoreOperationA, strings.Repeat("A", 32), nil, nil); !errors.Is(err, errInvalidTrashRestoreOperation) {
		t.Fatalf("ApplyTrashRestoreOperation(uppercase delete ID) error = %v, want invalid operation", err)
	}
	if err := store.CompleteTrashRestoreOperation(strings.Repeat("a", 31)); !errors.Is(err, errInvalidTrashRestoreOperation) {
		t.Fatalf("CompleteTrashRestoreOperation(short ID) error = %v, want invalid operation", err)
	}

	validShare := &Share{
		ID:         "share-a",
		Path:       "/docs/a.txt",
		Type:       ShareTypeFile,
		CreatedBy:  "user-a",
		CreatedAt:  time.Unix(1_700_000_000, 0),
		Permission: PermissionRead,
		Enabled:    true,
	}
	validDelete := &shareTrashDeleteOperation{
		Planned:        []*Share{copyShare(validShare)},
		Changed:        []*Share{copyShare(validShare)},
		RestoreBlocked: []string{},
		Committed:      true,
		Completed:      true,
	}
	validRestore := &shareTrashRestoreOperation{
		DeleteOperationID: shareTrashDeleteOperationA,
		Original:          []*Share{copyShare(validShare)},
		Relocated:         []*Share{copyShare(validShare)},
		Restored:          []*Share{},
	}
	tests := []struct {
		name       string
		operations map[string]*shareTrashRestoreOperation
	}{
		{
			name: "invalid operation ID",
			operations: map[string]*shareTrashRestoreOperation{
				strings.Repeat("A", 32): cloneTrashRestoreOperation(validRestore),
			},
		},
		{
			name: "null operation",
			operations: map[string]*shareTrashRestoreOperation{
				shareTrashRestoreOperationA: nil,
			},
		},
		{
			name: "unknown delete ownership",
			operations: map[string]*shareTrashRestoreOperation{
				shareTrashRestoreOperationA: {
					DeleteOperationID: strings.Repeat("3", 32),
					Original:          []*Share{copyShare(validShare)},
					Relocated:         []*Share{copyShare(validShare)},
					Restored:          []*Share{},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storePath := filepath.Join(t.TempDir(), "shares.json")
			data := marshalShareStoreFileForParticipantTest(t, shareStoreFile{
				Version: shareStoreFormatVersion,
				Shares:  []*Share{},
				TrashDeleteOperations: map[string]*shareTrashDeleteOperation{
					shareTrashDeleteOperationA: cloneTrashDeleteOperations(map[string]*shareTrashDeleteOperation{
						shareTrashDeleteOperationA: validDelete,
					})[shareTrashDeleteOperationA],
				},
				TrashRestoreOperations: test.operations,
			})
			if err := os.WriteFile(storePath, data, 0600); err != nil {
				t.Fatalf("WriteFile() error: %v", err)
			}
			if _, err := NewShareStore(storePath); err == nil {
				t.Fatal("NewShareStore() unexpectedly accepted invalid restore receipt data")
			}
		})
	}
}

func assertTrashRestoreReplayPreservedShare(t *testing.T, store *ShareStore, shareID string) {
	t.Helper()
	current, err := store.Get(shareID)
	if err != nil {
		t.Fatalf("Get(%s) error: %v", shareID, err)
	}
	if current.Path != "/archive/user-moved.txt" || current.Enabled ||
		current.Description != "newest-after-restore" || current.AccessCount != 9 {
		t.Fatalf("share changed by restore replay: %+v", current)
	}
}
