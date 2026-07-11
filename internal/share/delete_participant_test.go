package share

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShareStore_SnapshotDeleteExactReturnsEnabledCanonicalDeepCopies(t *testing.T) {
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}

	expiresIn := time.Hour
	cShare, err := store.Create(CreateShareOptions{
		Path:        "/docs/c.txt",
		Type:        ShareTypeFile,
		CreatedBy:   "user-b",
		ExpiresIn:   &expiresIn,
		Description: "stored-c",
	})
	if err != nil {
		t.Fatalf("Create(/docs/c.txt) error: %v", err)
	}
	aShare, err := store.Create(CreateShareOptions{
		Path:        "/docs/a.txt",
		Type:        ShareTypeFile,
		CreatedBy:   "user-a",
		Description: "stored-a",
	})
	if err != nil {
		t.Fatalf("Create(/docs/a.txt) error: %v", err)
	}
	disabledShare, err := store.Create(CreateShareOptions{
		Path:      "/docs/b.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user-a",
	})
	if err != nil {
		t.Fatalf("Create(/docs/b.txt) error: %v", err)
	}
	if err := store.Update(disabledShare.ID, func(updated *Share) error {
		updated.Enabled = false
		return nil
	}); err != nil {
		t.Fatalf("Update(disable /docs/b.txt) error: %v", err)
	}
	if _, err := store.Create(CreateShareOptions{
		Path:      "/other.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user-a",
	}); err != nil {
		t.Fatalf("Create(/other.txt) error: %v", err)
	}

	snapshot, err := store.SnapshotDeleteExact(`docs`)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact() error: %v", err)
	}
	if len(snapshot) != 2 {
		t.Fatalf("snapshot count = %d, want 2", len(snapshot))
	}
	if snapshot[0].ID != aShare.ID || snapshot[1].ID != cShare.ID {
		t.Fatalf("snapshot order = [%s %s], want [%s %s]", snapshot[0].Path, snapshot[1].Path, aShare.Path, cShare.Path)
	}

	snapshot[0].Description = "mutated-a"
	snapshot[1].Description = "mutated-c"
	if snapshot[1].ExpiresAt == nil {
		t.Fatal("expected expiration in snapshot")
	}
	*snapshot[1].ExpiresAt = snapshot[1].ExpiresAt.Add(24 * time.Hour)

	storedA, err := store.Get(aShare.ID)
	if err != nil {
		t.Fatalf("Get(aShare) error: %v", err)
	}
	if storedA.Description != "stored-a" || !storedA.Enabled {
		t.Fatalf("stored a share changed through snapshot: %+v", storedA)
	}
	storedC, err := store.Get(cShare.ID)
	if err != nil {
		t.Fatalf("Get(cShare) error: %v", err)
	}
	if storedC.Description != "stored-c" || storedC.ExpiresAt == nil || cShare.ExpiresAt == nil || !storedC.ExpiresAt.Equal(*cShare.ExpiresAt) {
		t.Fatalf("stored c share changed through snapshot: %+v", storedC)
	}
}

func TestShareStore_ApplyDeleteExactIsScopedAndIdempotent(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "shares.json")
	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}

	matching, err := store.Create(CreateShareOptions{
		Path:        "/docs/matching.txt",
		Type:        ShareTypeFile,
		CreatedBy:   "user-a",
		Description: "original",
	})
	if err != nil {
		t.Fatalf("Create(matching) error: %v", err)
	}
	moved, err := store.Create(CreateShareOptions{
		Path:      "/docs/moved.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user-a",
	})
	if err != nil {
		t.Fatalf("Create(moved) error: %v", err)
	}
	alreadyDisabled, err := store.Create(CreateShareOptions{
		Path:      "/docs/already-disabled.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user-a",
	})
	if err != nil {
		t.Fatalf("Create(alreadyDisabled) error: %v", err)
	}

	snapshot, err := store.SnapshotDeleteExact("/docs")
	if err != nil {
		t.Fatalf("SnapshotDeleteExact() error: %v", err)
	}

	if err := store.Update(matching.ID, func(updated *Share) error {
		updated.Description = "newer"
		updated.AccessCount = 7
		return nil
	}); err != nil {
		t.Fatalf("Update(matching metadata) error: %v", err)
	}
	if err := store.Update(moved.ID, func(updated *Share) error {
		updated.Path = "/archive/moved.txt"
		return nil
	}); err != nil {
		t.Fatalf("Update(moved path) error: %v", err)
	}
	if err := store.Update(alreadyDisabled.ID, func(updated *Share) error {
		updated.Enabled = false
		return nil
	}); err != nil {
		t.Fatalf("Update(alreadyDisabled) error: %v", err)
	}
	createdAfterSnapshot, err := store.Create(CreateShareOptions{
		Path:      "/docs/new.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user-a",
	})
	if err != nil {
		t.Fatalf("Create(createdAfterSnapshot) error: %v", err)
	}

	if err := store.ApplyDeleteExact(snapshot); err != nil {
		t.Fatalf("ApplyDeleteExact(first) error: %v", err)
	}
	if err := store.ApplyDeleteExact(snapshot); err != nil {
		t.Fatalf("ApplyDeleteExact(second) error: %v", err)
	}

	assertShare := func(id string, wantPath string, wantEnabled bool) *Share {
		t.Helper()
		share, err := store.Get(id)
		if err != nil {
			t.Fatalf("Get(%s) error: %v", id, err)
		}
		if share.Path != wantPath || share.Enabled != wantEnabled {
			t.Fatalf("share %s = {Path:%q Enabled:%t}, want {Path:%q Enabled:%t}", id, share.Path, share.Enabled, wantPath, wantEnabled)
		}
		return share
	}

	deleted := assertShare(matching.ID, "/docs/matching.txt", false)
	if deleted.Description != "newer" || deleted.AccessCount != 7 {
		t.Fatalf("matching share lost newer metadata: %+v", deleted)
	}
	assertShare(moved.ID, "/archive/moved.txt", true)
	assertShare(alreadyDisabled.ID, "/docs/already-disabled.txt", false)
	assertShare(createdAfterSnapshot.ID, "/docs/new.txt", true)

	reloaded, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reload) error: %v", err)
	}
	reloadedDeleted, err := reloaded.Get(matching.ID)
	if err != nil {
		t.Fatalf("Get(reloaded matching) error: %v", err)
	}
	if reloadedDeleted.Enabled || reloadedDeleted.Description != "newer" || reloadedDeleted.AccessCount != 7 {
		t.Fatalf("persisted matching share = %+v", reloadedDeleted)
	}
	reloadedMoved, err := reloaded.Get(moved.ID)
	if err != nil {
		t.Fatalf("Get(reloaded moved) error: %v", err)
	}
	if reloadedMoved.Path != "/archive/moved.txt" || !reloadedMoved.Enabled {
		t.Fatalf("persisted moved share = %+v", reloadedMoved)
	}
}

func TestShareStore_DeleteExactSnapshotSupportsPreservingCurrentRestore(t *testing.T) {
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}

	created, err := store.Create(CreateShareOptions{
		Path:        "/docs/report.txt",
		Type:        ShareTypeFile,
		CreatedBy:   "user-a",
		Description: "original",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	snapshot, err := store.SnapshotDeleteExact(created.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact() error: %v", err)
	}
	if err := store.ApplyDeleteExact(snapshot); err != nil {
		t.Fatalf("ApplyDeleteExact() error: %v", err)
	}
	if err := store.Update(created.ID, func(updated *Share) error {
		updated.Description = "newer"
		return nil
	}); err != nil {
		t.Fatalf("Update(newer metadata) error: %v", err)
	}

	if err := store.RestoreSharesPreservingCurrent(snapshot); err != nil {
		t.Fatalf("RestoreSharesPreservingCurrent() error: %v", err)
	}
	restored, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get(restored) error: %v", err)
	}
	if !restored.Enabled || restored.Description != "newer" {
		t.Fatalf("restored share = %+v", restored)
	}
}

func TestShareStore_TrashDeletePrecommitReopensAndRollsBackExactChanges(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "shares.json")
	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}

	matching, err := store.Create(CreateShareOptions{
		Path:        "/docs/matching.txt",
		Type:        ShareTypeFile,
		CreatedBy:   "user-a",
		Description: "original",
	})
	if err != nil {
		t.Fatalf("Create(matching) error: %v", err)
	}
	moved, err := store.Create(CreateShareOptions{
		Path:      "/docs/moved.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user-a",
	})
	if err != nil {
		t.Fatalf("Create(moved) error: %v", err)
	}
	planned, err := store.SnapshotDeleteExact("/docs")
	if err != nil {
		t.Fatalf("SnapshotDeleteExact() error: %v", err)
	}

	if err := store.Update(matching.ID, func(updated *Share) error {
		updated.Description = "newer-before-precommit"
		updated.AccessCount = 4
		return nil
	}); err != nil {
		t.Fatalf("Update(matching metadata) error: %v", err)
	}
	if err := store.Update(moved.ID, func(updated *Share) error {
		updated.Path = "/archive/moved.txt"
		return nil
	}); err != nil {
		t.Fatalf("Update(moved path) error: %v", err)
	}
	createdAfterPlan, err := store.Create(CreateShareOptions{
		Path:      "/docs/new.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user-a",
	})
	if err != nil {
		t.Fatalf("Create(after plan) error: %v", err)
	}

	operationID := strings.Repeat("a", 32)
	if err := store.ApplyTrashDeleteOperation(operationID, planned, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(precommit) error: %v", err)
	}
	current, err := store.Get(matching.ID)
	if err != nil {
		t.Fatalf("Get(matching) error: %v", err)
	}
	if current.Enabled || current.Description != "newer-before-precommit" || current.AccessCount != 4 {
		t.Fatalf("matching after precommit = %+v", current)
	}
	if current, err := store.Get(moved.ID); err != nil || current.Path != "/archive/moved.txt" || !current.Enabled {
		t.Fatalf("moved after precommit = (%+v, %v)", current, err)
	}
	if current, err := store.Get(createdAfterPlan.ID); err != nil || !current.Enabled {
		t.Fatalf("post-plan share after precommit = (%+v, %v)", current, err)
	}

	persisted := readShareStoreFileForParticipantTest(t, storePath)
	marker := persisted.TrashDeleteOperations[operationID]
	if marker == nil || len(marker.Planned) != 2 || len(marker.Changed) != 1 {
		t.Fatalf("persisted marker = %+v, want two planned and one changed", marker)
	}
	if marker.Planned[0].Path != "/docs/matching.txt" || marker.Planned[1].Path != "/docs/moved.txt" {
		t.Fatalf("persisted plan is not canonical: %+v", marker.Planned)
	}
	if marker.Changed[0].ID != matching.ID || !marker.Changed[0].Enabled || marker.Changed[0].Description != "newer-before-precommit" {
		t.Fatalf("persisted changed snapshot = %+v", marker.Changed)
	}

	reopened, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reopen) error: %v", err)
	}
	if err := reopened.Update(matching.ID, func(updated *Share) error {
		updated.Description = "newest-after-reopen"
		updated.AccessCount = 9
		return nil
	}); err != nil {
		t.Fatalf("Update(after reopen) error: %v", err)
	}
	if err := reopened.RollbackTrashDeleteOperation(operationID); err != nil {
		t.Fatalf("RollbackTrashDeleteOperation() error: %v", err)
	}
	restored, err := reopened.Get(matching.ID)
	if err != nil {
		t.Fatalf("Get(restored) error: %v", err)
	}
	if restored.Enabled || restored.Description != "newest-after-reopen" || restored.AccessCount != 9 {
		t.Fatalf("restored matching share = %+v", restored)
	}
	if _, exists := readShareStoreFileForParticipantTest(t, storePath).TrashDeleteOperations[operationID]; exists {
		t.Fatal("rollback did not remove the durable marker")
	}

	reopenedAgain, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(second reopen) error: %v", err)
	}
	if err := reopenedAgain.RollbackTrashDeleteOperation(operationID); err != nil {
		t.Fatalf("idempotent RollbackTrashDeleteOperation() error: %v", err)
	}
}

func TestShareStore_TrashDeleteRollbackPreservesMovedDeletedAndRebuiltShares(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "shares.json")
	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}

	create := func(path string) *Share {
		t.Helper()
		share, err := store.Create(CreateShareOptions{Path: path, Type: ShareTypeFile, CreatedBy: "user-a"})
		if err != nil {
			t.Fatalf("Create(%s) error: %v", path, err)
		}
		return share
	}
	moved := create("/docs/moved.txt")
	deleted := create("/docs/deleted.txt")
	rebuilt := create("/docs/rebuilt.txt")
	planned, err := store.SnapshotDeleteExact("/docs")
	if err != nil {
		t.Fatalf("SnapshotDeleteExact() error: %v", err)
	}
	operationID := strings.Repeat("b", 32)
	if err := store.ApplyTrashDeleteOperation(operationID, planned, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(precommit) error: %v", err)
	}

	if err := store.Update(moved.ID, func(updated *Share) error {
		updated.Path = "/archive/moved.txt"
		updated.Description = "moved-later"
		return nil
	}); err != nil {
		t.Fatalf("Update(moved) error: %v", err)
	}
	if err := store.Delete(deleted.ID); err != nil {
		t.Fatalf("Delete(deleted) error: %v", err)
	}
	if err := store.Delete(rebuilt.ID); err != nil {
		t.Fatalf("Delete(rebuilt) error: %v", err)
	}
	replacement := copyShare(rebuilt)
	replacement.CreatedAt = rebuilt.CreatedAt.Add(time.Hour)
	replacement.Description = "rebuilt-later"
	replacement.Enabled = false
	if err := store.RestoreShares([]*Share{replacement}); err != nil {
		t.Fatalf("RestoreShares(replacement) error: %v", err)
	}

	if err := store.RollbackTrashDeleteOperation(operationID); err != nil {
		t.Fatalf("RollbackTrashDeleteOperation() error: %v", err)
	}
	currentMoved, err := store.Get(moved.ID)
	if err != nil || currentMoved.Path != "/archive/moved.txt" || currentMoved.Enabled || currentMoved.Description != "moved-later" {
		t.Fatalf("moved share after rollback = (%+v, %v)", currentMoved, err)
	}
	if _, err := store.Get(deleted.ID); !errors.Is(err, ErrShareNotFound) {
		t.Fatalf("deleted share after rollback error = %v, want ErrShareNotFound", err)
	}
	currentRebuilt, err := store.Get(rebuilt.ID)
	if err != nil || currentRebuilt.Enabled || currentRebuilt.Description != "rebuilt-later" || !currentRebuilt.CreatedAt.Equal(replacement.CreatedAt) {
		t.Fatalf("rebuilt share after rollback = (%+v, %v)", currentRebuilt, err)
	}
	if _, exists := readShareStoreFileForParticipantTest(t, storePath).TrashDeleteOperations[operationID]; exists {
		t.Fatal("rollback did not remove marker after preserving newer objects")
	}
}

func TestShareStore_TrashDeleteCommitIsIdempotentAndRejectsPlanMismatch(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "shares.json")
	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	created, err := store.Create(CreateShareOptions{
		Path:        "/docs/report.txt",
		Type:        ShareTypeFile,
		CreatedBy:   "user-a",
		Description: "original",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	planned, err := store.SnapshotDeleteExact(created.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact() error: %v", err)
	}
	operationID := strings.Repeat("c", 32)
	if err := store.ApplyTrashDeleteOperation(operationID, planned, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(precommit) error: %v", err)
	}
	if err := store.Update(created.ID, func(updated *Share) error {
		updated.Enabled = true
		updated.Description = "reenabled-later"
		return nil
	}); err != nil {
		t.Fatalf("Update(reenable) error: %v", err)
	}
	if err := store.ApplyTrashDeleteOperation(operationID, planned, false); err != nil {
		t.Fatalf("idempotent precommit error: %v", err)
	}
	if current, err := store.Get(created.ID); err != nil || !current.Enabled {
		t.Fatalf("repeated precommit overwrote newer enabled state: (%+v, %v)", current, err)
	}

	mismatched := cloneShareSlice(planned)
	mismatched[0].Description = "different-plan"
	if err := store.ApplyTrashDeleteOperation(operationID, mismatched, false); !errors.Is(err, ErrTrashDeleteOperationConflict) {
		t.Fatalf("mismatched precommit error = %v, want ErrTrashDeleteOperationConflict", err)
	}
	if err := store.ApplyTrashDeleteOperation(operationID, mismatched, true); !errors.Is(err, ErrTrashDeleteOperationConflict) {
		t.Fatalf("mismatched commit error = %v, want ErrTrashDeleteOperationConflict", err)
	}

	if err := store.ApplyTrashDeleteOperation(operationID, planned, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(commit) error: %v", err)
	}
	committed, err := store.Get(created.ID)
	if err != nil || committed.Enabled || committed.Description != "reenabled-later" {
		t.Fatalf("committed share = (%+v, %v)", committed, err)
	}
	committedMarker := readShareStoreFileForParticipantTest(t, storePath).TrashDeleteOperations[operationID]
	if committedMarker == nil || !committedMarker.Committed {
		t.Fatalf("committed marker = %+v, want durable committed receipt", committedMarker)
	}
	if err := store.Update(created.ID, func(updated *Share) error {
		updated.Path = "/archive/user-moved.txt"
		updated.Enabled = true
		updated.Description = "updated-after-commit"
		return nil
	}); err != nil {
		t.Fatalf("Update(after commit) error: %v", err)
	}
	if err := store.ApplyTrashDeleteOperation(operationID, planned, true); err != nil {
		t.Fatalf("idempotent commit error: %v", err)
	}
	currentAfterReplay, err := store.Get(created.ID)
	if err != nil || currentAfterReplay.Path != "/archive/user-moved.txt" || !currentAfterReplay.Enabled || currentAfterReplay.Description != "updated-after-commit" {
		t.Fatalf("share after committed replay = (%+v, %v)", currentAfterReplay, err)
	}

	reopenedCommitted, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reopen committed receipt) error: %v", err)
	}
	if err := reopenedCommitted.ApplyTrashDeleteOperation(operationID, planned, true); err != nil {
		t.Fatalf("committed replay after reopen error: %v", err)
	}
	currentAfterReopenReplay, err := reopenedCommitted.Get(created.ID)
	if err != nil || currentAfterReopenReplay.Path != "/archive/user-moved.txt" || !currentAfterReopenReplay.Enabled {
		t.Fatalf("share after reopened committed replay = (%+v, %v)", currentAfterReopenReplay, err)
	}
	if err := reopenedCommitted.RollbackTrashDeleteOperation(operationID); !errors.Is(err, ErrTrashDeleteOperationConflict) {
		t.Fatalf("RollbackTrashDeleteOperation(committed) error = %v, want conflict", err)
	}
	if err := reopenedCommitted.CompleteTrashDeleteOperation(operationID); err != nil {
		t.Fatalf("CompleteTrashDeleteOperation() error: %v", err)
	}
	if err := reopenedCommitted.CompleteTrashDeleteOperation(operationID); err != nil {
		t.Fatalf("idempotent CompleteTrashDeleteOperation() error: %v", err)
	}
	completedMarker := readShareStoreFileForParticipantTest(t, storePath).TrashDeleteOperations[operationID]
	if completedMarker == nil || !completedMarker.Completed {
		t.Fatalf("complete marker = %+v, want retained completed ownership", completedMarker)
	}

	direct, err := reopenedCommitted.Create(CreateShareOptions{Path: "/docs/direct.txt", Type: ShareTypeFile, CreatedBy: "user-a"})
	if err != nil {
		t.Fatalf("Create(direct) error: %v", err)
	}
	directPlan, err := reopenedCommitted.SnapshotDeleteExact(direct.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact(direct) error: %v", err)
	}
	directOperationID := strings.Repeat("d", 32)
	directRecovery, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(direct committed recovery) error: %v", err)
	}
	if err := directRecovery.ApplyTrashDeleteOperation(directOperationID, directPlan, true); err != nil {
		t.Fatalf("direct committed ApplyTrashDeleteOperation() error: %v", err)
	}
	if current, err := directRecovery.Get(direct.ID); err != nil || current.Enabled {
		t.Fatalf("direct committed share = (%+v, %v)", current, err)
	}
	directMarker := readShareStoreFileForParticipantTest(t, storePath).TrashDeleteOperations[directOperationID]
	if directMarker == nil || !directMarker.Committed {
		t.Fatalf("direct committed marker = %+v, want committed receipt", directMarker)
	}
}

func TestShareStore_CompleteTrashDeleteRejectsPendingMarker(t *testing.T) {
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	created, err := store.Create(CreateShareOptions{Path: "/docs/pending.txt", Type: ShareTypeFile, CreatedBy: "user-a"})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	planned, err := store.SnapshotDeleteExact(created.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact() error: %v", err)
	}
	operationID := strings.Repeat("1", 32)
	if err := store.ApplyTrashDeleteOperation(operationID, planned, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(precommit) error: %v", err)
	}
	if err := store.CompleteTrashDeleteOperation(operationID); !errors.Is(err, ErrTrashDeleteOperationConflict) {
		t.Fatalf("CompleteTrashDeleteOperation(pending) error = %v, want conflict", err)
	}
	if marker := store.trashDeleteOperations[operationID]; marker == nil || marker.Committed {
		t.Fatalf("pending marker changed after rejected complete: %+v", marker)
	}
}

func TestShareStore_CommittedTrashDeleteReceiptRetainsRestoreGuardAcrossLaterOwnership(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "shares.json")
	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	created, err := store.Create(CreateShareOptions{
		Path:        "/docs/receipt.txt",
		Type:        ShareTypeFile,
		CreatedBy:   "user-a",
		Description: "original",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	committedPlan, err := store.SnapshotDeleteExact(created.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact(committed) error: %v", err)
	}
	committedOperationID := strings.Repeat("2", 32)
	if err := store.ApplyTrashDeleteOperation(committedOperationID, committedPlan, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(committed) error: %v", err)
	}
	committedReceipt := cloneTrashDeleteOperations(store.trashDeleteOperations)[committedOperationID]
	if committedReceipt == nil || !committedReceipt.Committed || len(committedReceipt.Changed) != 1 {
		t.Fatalf("committed receipt = %+v, want one changed share", committedReceipt)
	}

	if err := store.Update(created.ID, func(updated *Share) error {
		updated.Enabled = true
		updated.Description = "updated-after-commit"
		return nil
	}); err != nil {
		t.Fatalf("Update(after committed receipt) error: %v", err)
	}
	committedReceipt = cloneTrashDeleteOperations(store.trashDeleteOperations)[committedOperationID]
	if committedReceipt == nil || !stringSlicesEqual(committedReceipt.RestoreBlocked, []string{created.ID}) {
		t.Fatalf("committed receipt after explicit update = %+v, want restore guard", committedReceipt)
	}
	pendingPlan, err := store.SnapshotDeleteExact(created.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact(pending rollback) error: %v", err)
	}
	pendingOperationID := strings.Repeat("3", 32)
	if err := store.ApplyTrashDeleteOperation(pendingOperationID, pendingPlan, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(pending rollback) error: %v", err)
	}
	if err := store.RollbackTrashDeleteOperation(pendingOperationID); err != nil {
		t.Fatalf("RollbackTrashDeleteOperation(pending) error: %v", err)
	}
	rolledBack, err := store.Get(created.ID)
	if err != nil || !rolledBack.Enabled || rolledBack.Description != "updated-after-commit" {
		t.Fatalf("share after pending rollback = (%+v, %v)", rolledBack, err)
	}
	assertTrashDeleteReceiptEqual(t, store.trashDeleteOperations[committedOperationID], committedReceipt)

	ownedPlan, err := store.SnapshotDeleteExact(created.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact(pending ownership) error: %v", err)
	}
	ownedOperationID := strings.Repeat("4", 32)
	if err := store.ApplyTrashDeleteOperation(ownedOperationID, ownedPlan, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(pending ownership) error: %v", err)
	}
	if err := store.Update(created.ID, func(updated *Share) error {
		updated.Enabled = true
		return nil
	}); err != nil {
		t.Fatalf("Update(reenable before later commit) error: %v", err)
	}
	laterPlan, err := store.SnapshotDeleteExact(created.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact(later commit) error: %v", err)
	}
	laterOperationID := strings.Repeat("5", 32)
	if err := store.ApplyTrashDeleteOperation(laterOperationID, laterPlan, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(later commit) error: %v", err)
	}
	assertTrashDeleteReceiptEqual(t, store.trashDeleteOperations[committedOperationID], committedReceipt)
	ownedMarker := store.trashDeleteOperations[ownedOperationID]
	if ownedMarker == nil || ownedMarker.Committed || len(ownedMarker.Changed) != 0 {
		t.Fatalf("pending ownership marker after later commit = %+v, want empty active ownership", ownedMarker)
	}

	reopened, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reopen) error: %v", err)
	}
	assertTrashDeleteReceiptEqual(t, reopened.trashDeleteOperations[committedOperationID], committedReceipt)
}

func assertTrashDeleteReceiptEqual(t *testing.T, current, want *shareTrashDeleteOperation) {
	t.Helper()
	if current == nil || want == nil || current.Committed != want.Committed || current.Completed != want.Completed ||
		!stringSlicesEqual(current.RestoreBlocked, want.RestoreBlocked) ||
		!shareSlicesEqual(current.Planned, want.Planned) || !shareSlicesEqual(current.Changed, want.Changed) {
		t.Fatalf("Trash delete receipt = %+v, want %+v", current, want)
	}
}

func TestShareStore_TrashDeletePersistenceWarningsRemainRecoverable(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "shares.json")
	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	created, err := store.Create(CreateShareOptions{Path: "/docs/warning.txt", Type: ShareTypeFile, CreatedBy: "user-a"})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	planned, err := store.SnapshotDeleteExact(created.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact() error: %v", err)
	}
	operationID := strings.Repeat("e", 32)

	originalSync := syncShareStoreRootDir
	defer func() { syncShareStoreRootDir = originalSync }()
	syncShareStoreRootDir = func(root *os.Root) error {
		return errors.New("forced directory sync warning")
	}
	if err := store.ApplyTrashDeleteOperation(operationID, planned, false); !IsPersistenceWarning(err) {
		t.Fatalf("precommit error = %v, want PersistenceWarningError", err)
	}
	syncShareStoreRootDir = originalSync

	reopened, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reopen after warning) error: %v", err)
	}
	if current, err := reopened.Get(created.ID); err != nil || current.Enabled {
		t.Fatalf("share after warning reopen = (%+v, %v)", current, err)
	}
	if _, exists := readShareStoreFileForParticipantTest(t, storePath).TrashDeleteOperations[operationID]; !exists {
		t.Fatal("precommit marker was not durable after warning")
	}

	syncShareStoreRootDir = func(root *os.Root) error {
		return errors.New("forced rollback directory sync warning")
	}
	if err := reopened.RollbackTrashDeleteOperation(operationID); !IsPersistenceWarning(err) {
		t.Fatalf("rollback error = %v, want PersistenceWarningError", err)
	}
	syncShareStoreRootDir = originalSync

	reopenedAgain, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reopen after rollback warning) error: %v", err)
	}
	if current, err := reopenedAgain.Get(created.ID); err != nil || !current.Enabled {
		t.Fatalf("share after rollback warning reopen = (%+v, %v)", current, err)
	}
	if _, exists := readShareStoreFileForParticipantTest(t, storePath).TrashDeleteOperations[operationID]; exists {
		t.Fatal("rollback marker remained after persistence warning")
	}

	commitShare, err := reopenedAgain.Create(CreateShareOptions{
		Path:      "/docs/commit-warning.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user-a",
	})
	if err != nil {
		t.Fatalf("Create(commit warning) error: %v", err)
	}
	commitPlan, err := reopenedAgain.SnapshotDeleteExact(commitShare.Path)
	if err != nil {
		t.Fatalf("SnapshotDeleteExact(commit warning) error: %v", err)
	}
	commitOperationID := strings.Repeat("f", 32)
	if err := reopenedAgain.ApplyTrashDeleteOperation(commitOperationID, commitPlan, false); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(commit warning precommit) error: %v", err)
	}
	syncShareStoreRootDir = func(root *os.Root) error {
		return errors.New("forced commit directory sync warning")
	}
	if err := reopenedAgain.ApplyTrashDeleteOperation(commitOperationID, commitPlan, true); !IsPersistenceWarning(err) {
		t.Fatalf("commit error = %v, want PersistenceWarningError", err)
	}
	syncShareStoreRootDir = originalSync

	afterCommitWarning, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reopen after commit warning) error: %v", err)
	}
	if current, err := afterCommitWarning.Get(commitShare.ID); err != nil || current.Enabled {
		t.Fatalf("share after commit warning reopen = (%+v, %v)", current, err)
	}
	commitMarker := readShareStoreFileForParticipantTest(t, storePath).TrashDeleteOperations[commitOperationID]
	if commitMarker == nil || !commitMarker.Committed {
		t.Fatalf("commit receipt after persistence warning = %+v, want committed", commitMarker)
	}
}

func TestShareStore_TrashDeleteOperationValidationAndStrictLoading(t *testing.T) {
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	if err := store.ApplyTrashDeleteOperation(strings.Repeat("a", 31), nil, false); err == nil {
		t.Fatal("ApplyTrashDeleteOperation() accepted a short operation ID")
	}
	if err := store.RollbackTrashDeleteOperation(strings.Repeat("g", 32)); err == nil {
		t.Fatal("RollbackTrashDeleteOperation() accepted a non-hex operation ID")
	}
	if err := store.CompleteTrashDeleteOperation(strings.Repeat("A", 32)); err == nil {
		t.Fatal("CompleteTrashDeleteOperation() accepted an uppercase operation ID")
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
	validMarker := &shareTrashDeleteOperation{
		Planned:        []*Share{copyShare(validShare)},
		Changed:        []*Share{},
		RestoreBlocked: []string{},
	}
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "array root",
			data: []byte(`[]`),
		},
		{
			name: "invalid operation ID",
			data: marshalShareStoreFileForParticipantTest(t, shareStoreFile{
				Version:                shareStoreFormatVersion,
				Shares:                 []*Share{},
				TrashRestoreOperations: map[string]*shareTrashRestoreOperation{},
				TrashDeleteOperations: map[string]*shareTrashDeleteOperation{
					strings.Repeat("g", 32): validMarker,
				},
			}),
		},
		{
			name: "null marker",
			data: marshalShareStoreFileForParticipantTest(t, shareStoreFile{
				Version:                shareStoreFormatVersion,
				Shares:                 []*Share{},
				TrashRestoreOperations: map[string]*shareTrashRestoreOperation{},
				TrashDeleteOperations: map[string]*shareTrashDeleteOperation{
					strings.Repeat("f", 32): nil,
				},
			}),
		},
		{
			name: "noncanonical marker order",
			data: marshalShareStoreFileForParticipantTest(t, shareStoreFile{
				Version:                shareStoreFormatVersion,
				Shares:                 []*Share{},
				TrashRestoreOperations: map[string]*shareTrashRestoreOperation{},
				TrashDeleteOperations: map[string]*shareTrashDeleteOperation{
					strings.Repeat("f", 32): {
						Planned: []*Share{
							{ID: "share-b", Path: "/docs/b.txt", Type: ShareTypeFile, CreatedBy: "user-a", CreatedAt: validShare.CreatedAt, Permission: PermissionRead, Enabled: true},
							copyShare(validShare),
						},
						Changed:        []*Share{},
						RestoreBlocked: []string{},
					},
				},
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storePath := filepath.Join(t.TempDir(), "shares.json")
			if err := os.WriteFile(storePath, test.data, 0600); err != nil {
				t.Fatalf("WriteFile() error: %v", err)
			}
			if _, err := NewShareStore(storePath); err == nil {
				t.Fatal("NewShareStore() unexpectedly accepted invalid store data")
			}
		})
	}
}

func readShareStoreFileForParticipantTest(t *testing.T, storePath string) shareStoreFile {
	t.Helper()
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", storePath, err)
	}
	stored, err := decodeShareStoreFile(data)
	if err != nil {
		t.Fatalf("decodeShareStoreFile() error: %v", err)
	}
	return stored
}

func marshalShareStoreFileForParticipantTest(t *testing.T, stored shareStoreFile) []byte {
	t.Helper()
	data, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	return data
}
