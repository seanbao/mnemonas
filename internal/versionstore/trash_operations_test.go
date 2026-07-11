package versionstore

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/dataplane"
)

func setupTrashOperationsStore(t *testing.T) *Store {
	t.Helper()
	client := dataplane.NewClient("unused")
	store, err := New(Config{
		DBPath:    filepath.Join(t.TempDir(), "trash-operations.db"),
		Dataplane: client,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close() error: %v", err)
		}
		client.Close()
	})
	return store
}

func testTrashOperationItem() *TrashItem {
	return &TrashItem{
		ID:           "trash-operation-item",
		OriginalPath: "/docs",
		Size:         3072,
		DeletedAt:    time.Unix(1_700_000_000, 0),
		ExpiresAt:    time.Unix(1_700_086_400, 0),
		IsDir:        true,
		HadVersions:  true,
		RestoreData:  []byte(`{"shares":["share-1"]}`),
	}
}

func testTrashOperation(kind, trashID, idDigit, hashDigit string) *TrashOperation {
	return &TrashOperation{
		ID:                 strings.Repeat(idDigit, 32),
		Kind:               kind,
		TrashID:            trashID,
		JournalHash:        strings.Repeat(hashDigit, 64),
		ParticipantPayload: []byte(`{"participant":"state"}`),
	}
}

func cloneTrashOperationForTest(operation *TrashOperation) *TrashOperation {
	cloned := *operation
	cloned.ParticipantPayload = append([]byte{}, operation.ParticipantPayload...)
	return &cloned
}

func TestFileIndexTreeExists(t *testing.T) {
	store := setupTrashOperationsStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_050, 0)
	for _, indexedPath := range []string{
		"/docs",
		"/docs/nested/report.txt",
		"/docs-archive/keep.txt",
		"/other.txt",
	} {
		if err := store.UpdateFileIndex(ctx, indexedPath, 1, now, "hash"); err != nil {
			t.Fatalf("UpdateFileIndex(%q) error: %v", indexedPath, err)
		}
	}

	for _, test := range []struct {
		name    string
		path    string
		want    bool
		wantErr error
	}{
		{name: "exact", path: "/docs", want: true},
		{name: "descendant", path: "/docs/nested", want: true},
		{name: "prefix collision excluded", path: "/doc", want: false},
		{name: "missing sibling", path: "/docs/missing", want: false},
		{name: "root", path: "/", want: true},
		{name: "invalid traversal", path: "/docs/../other", wantErr: errInvalidStorePath},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := store.FileIndexTreeExists(ctx, test.path)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("FileIndexTreeExists(%q) error = %v, want %v", test.path, err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("FileIndexTreeExists(%q) = %t, want %t", test.path, got, test.want)
			}
		})
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.FileIndexTreeExists(canceled, "/docs"); !errors.Is(err, context.Canceled) {
		t.Fatalf("FileIndexTreeExists(canceled) error = %v, want context.Canceled", err)
	}

	tree, err := store.ListFileIndexTree(ctx, "/docs")
	if err != nil {
		t.Fatalf("ListFileIndexTree(/docs) error: %v", err)
	}
	if len(tree) != 2 || tree[0].Path != "/docs" || tree[1].Path != "/docs/nested/report.txt" {
		t.Fatalf("ListFileIndexTree(/docs) = %+v", tree)
	}
	if tree, err := store.ListFileIndexTree(ctx, "/doc"); err != nil || len(tree) != 0 {
		t.Fatalf("ListFileIndexTree(/doc) = %+v, %v; want empty", tree, err)
	}
	if _, err := store.ListFileIndexTree(canceled, "/docs"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListFileIndexTree(canceled) error = %v, want context.Canceled", err)
	}
}

func TestCommitTrashDelete_AtomicOutboxAndIdempotency(t *testing.T) {
	store := setupTrashOperationsStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_100, 0)

	for path, hash := range map[string]string{
		"/docs/readme.md":         "readme-hash",
		"/docs/nested/guide.md":   "guide-hash",
		"/docs-archive/retain.md": "retain-hash",
	} {
		if err := store.UpdateFileIndex(ctx, path, 100, now, hash); err != nil {
			t.Fatalf("UpdateFileIndex(%q) error: %v", path, err)
		}
	}

	item := testTrashOperationItem()
	operation := testTrashOperation(TrashOperationKindDeleteToTrash, item.ID, "a", "b")
	wantOperation := cloneTrashOperationForTest(operation)
	if err := store.CommitTrashDelete(ctx, item, operation); err != nil {
		t.Fatalf("CommitTrashDelete() error: %v", err)
	}

	storedItem, err := store.GetTrashItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetTrashItem() error: %v", err)
	}
	if storedItem.OriginalPath != item.OriginalPath || storedItem.Size != item.Size ||
		storedItem.DeletedAt.Unix() != item.DeletedAt.Unix() || storedItem.ExpiresAt.Unix() != item.ExpiresAt.Unix() ||
		storedItem.IsDir != item.IsDir || storedItem.HadVersions != item.HadVersions ||
		string(storedItem.RestoreData) != string(item.RestoreData) {
		t.Fatalf("GetTrashItem() = %+v, want %+v", storedItem, item)
	}
	for _, deletedPath := range []string{"/docs/readme.md", "/docs/nested/guide.md"} {
		if _, _, _, err := store.GetFileIndex(ctx, deletedPath); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetFileIndex(%q) error = %v, want ErrNotFound", deletedPath, err)
		}
	}
	if _, _, hash, err := store.GetFileIndex(ctx, "/docs-archive/retain.md"); err != nil || hash != "retain-hash" {
		t.Fatalf("retained sibling index = (%q, %v), want (retain-hash, nil)", hash, err)
	}

	storedOperation, err := store.GetTrashOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("GetTrashOperation() error: %v", err)
	}
	if !trashOperationsEqual(*storedOperation, *wantOperation) {
		t.Fatalf("GetTrashOperation() = %+v, want %+v", storedOperation, wantOperation)
	}
	if storedOperation.ParticipantPayload == nil {
		t.Fatal("GetTrashOperation() returned a nil participant payload")
	}

	if err := store.CommitTrashDelete(ctx, item, wantOperation); err != nil {
		t.Fatalf("idempotent CommitTrashDelete() error: %v", err)
	}
	conflictingOperation := cloneTrashOperationForTest(wantOperation)
	conflictingOperation.ParticipantPayload = []byte("different")
	if err := store.CommitTrashDelete(ctx, item, conflictingOperation); !errors.Is(err, ErrTrashOperationConflict) {
		t.Fatalf("conflicting CommitTrashDelete() error = %v, want ErrTrashOperationConflict", err)
	}
	sameTrashDifferentOperation := cloneTrashOperationForTest(wantOperation)
	sameTrashDifferentOperation.ID = strings.Repeat("c", 32)
	if err := store.CommitTrashDelete(ctx, item, sameTrashDifferentOperation); !errors.Is(err, ErrTrashOperationConflict) {
		t.Fatalf("same-Trash CommitTrashDelete() error = %v, want ErrTrashOperationConflict", err)
	}

	operations, err := store.ListTrashOperations(ctx)
	if err != nil {
		t.Fatalf("ListTrashOperations() error: %v", err)
	}
	if len(operations) != 1 || !trashOperationsEqual(operations[0], *wantOperation) {
		t.Fatalf("ListTrashOperations() = %+v, want [%+v]", operations, *wantOperation)
	}
	operations[0].ParticipantPayload[0] = 'X'
	storedOperation, err = store.GetTrashOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("GetTrashOperation() after caller mutation error: %v", err)
	}
	if !trashOperationsEqual(*storedOperation, *wantOperation) {
		t.Fatalf("stored operation changed through returned payload: %+v", storedOperation)
	}

	wrongHash := strings.Repeat("c", 64)
	if err := store.CompleteTrashOperation(ctx, operation.ID, wrongHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CompleteTrashOperation(wrong hash) error = %v, want ErrNotFound", err)
	}
	if _, err := store.GetTrashOperation(ctx, operation.ID); err != nil {
		t.Fatalf("operation removed by wrong hash: %v", err)
	}
	if err := store.CompleteTrashOperation(ctx, operation.ID, operation.JournalHash); err != nil {
		t.Fatalf("CompleteTrashOperation() error: %v", err)
	}
	if _, err := store.GetTrashOperation(ctx, operation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTrashOperation() after completion error = %v, want ErrNotFound", err)
	}
}

func TestCommitTrashDelete_MapsTrashIDConflictAndRollsBack(t *testing.T) {
	store := setupTrashOperationsStore(t)
	ctx := context.Background()
	item := testTrashOperationItem()
	item.ID = "trash-delete-rollback"
	item.OriginalPath = "/atomic.txt"
	item.IsDir = false
	operation := testTrashOperation(TrashOperationKindDeleteToTrash, item.ID, "1", "2")

	if err := store.UpdateFileIndex(ctx, item.OriginalPath, 10, time.Unix(1_700_000_200, 0), "original-hash"); err != nil {
		t.Fatalf("UpdateFileIndex() error: %v", err)
	}
	competingOperationID := strings.Repeat("c", 32)
	if _, err := store.db.ExecContext(ctx, `
		CREATE TRIGGER inject_competing_delete_operation
		AFTER INSERT ON trash
		BEGIN
			INSERT INTO trash_operations (
				operation_id, kind, trash_id, journal_hash, participant_payload
			) VALUES (
				'cccccccccccccccccccccccccccccccc',
				'delete_to_trash',
				NEW.id,
				'dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd',
				x''
			);
		END;
	`); err != nil {
		t.Fatalf("create failure trigger error: %v", err)
	}

	if err := store.CommitTrashDelete(ctx, item, operation); !errors.Is(err, ErrTrashOperationConflict) {
		t.Fatalf("CommitTrashDelete() error = %v, want ErrTrashOperationConflict", err)
	}
	if _, err := store.GetTrashItem(ctx, item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTrashItem() after rollback error = %v, want ErrNotFound", err)
	}
	if _, _, hash, err := store.GetFileIndex(ctx, item.OriginalPath); err != nil || hash != "original-hash" {
		t.Fatalf("source index after rollback = (%q, %v), want (original-hash, nil)", hash, err)
	}
	if _, err := store.GetTrashOperation(ctx, operation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTrashOperation() after rollback error = %v, want ErrNotFound", err)
	}
	if _, err := store.GetTrashOperation(ctx, competingOperationID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("competing operation survived rollback: %v", err)
	}
}

func TestCommitTrashRestore_ReplacesIndexAndRenamesHistory(t *testing.T) {
	store := setupTrashOperationsStore(t)
	ctx := context.Background()
	item := testTrashOperationItem()
	item.ID = "trash-restore-item"
	if err := store.AddToTrash(ctx, item); err != nil {
		t.Fatalf("AddToTrash() error: %v", err)
	}

	if err := store.AddVersion(ctx, "/docs/readme.md", "version-hash", 100, ""); err != nil {
		t.Fatalf("AddVersion() error: %v", err)
	}
	if err := store.SetVersioningOverride(ctx, "/docs/readme.md", true); err != nil {
		t.Fatalf("SetVersioningOverride() error: %v", err)
	}
	if err := store.AcquireLock(ctx, "/docs/readme.md", "writer", WriteLock, time.Hour); err != nil {
		t.Fatalf("AcquireLock() error: %v", err)
	}
	now := time.Unix(1_700_000_300, 0)
	if err := store.UpdateFileIndex(ctx, "/restored/stale.txt", 1, now, "stale-hash"); err != nil {
		t.Fatalf("UpdateFileIndex(stale) error: %v", err)
	}
	if err := store.UpdateFileIndex(ctx, "/restored-sibling/keep.txt", 2, now, "keep-hash"); err != nil {
		t.Fatalf("UpdateFileIndex(sibling) error: %v", err)
	}

	index := []FileIndexEntry{
		{Path: "/restored/readme.md", Size: 100, ModTime: now, ContentHash: "restored-readme"},
		{Path: "/restored/nested/guide.md", Size: 200, ModTime: now.Add(time.Second), ContentHash: "restored-guide"},
	}
	operation := testTrashOperation(TrashOperationKindRestoreFromTrash, item.ID, "3", "4")
	if err := store.CommitTrashRestore(ctx, item, "/restored", index, true, operation); err != nil {
		t.Fatalf("CommitTrashRestore() error: %v", err)
	}
	if _, err := store.GetTrashItem(ctx, item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTrashItem() after restore error = %v, want ErrNotFound", err)
	}
	if _, _, _, err := store.GetFileIndex(ctx, "/restored/stale.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale destination index error = %v, want ErrNotFound", err)
	}
	for _, entry := range index {
		size, modTime, hash, err := store.GetFileIndex(ctx, entry.Path)
		if err != nil || size != entry.Size || !modTime.Equal(entry.ModTime) || hash != entry.ContentHash {
			t.Fatalf("GetFileIndex(%q) = (%d, %v, %q, %v), want (%d, %v, %q, nil)",
				entry.Path, size, modTime, hash, err, entry.Size, entry.ModTime, entry.ContentHash)
		}
	}
	if _, _, hash, err := store.GetFileIndex(ctx, "/restored-sibling/keep.txt"); err != nil || hash != "keep-hash" {
		t.Fatalf("sibling index = (%q, %v), want (keep-hash, nil)", hash, err)
	}

	versions, err := store.GetVersions(ctx, "/restored/readme.md")
	if err != nil || len(versions) != 1 || versions[0].Hash != "version-hash" {
		t.Fatalf("restored versions = (%+v, %v), want version-hash", versions, err)
	}
	if versions, err := store.GetVersions(ctx, "/docs/readme.md"); err != nil || len(versions) != 0 {
		t.Fatalf("source versions after rename = (%+v, %v), want empty", versions, err)
	}
	if enabled, exists := store.GetVersioningOverride(ctx, "/restored/readme.md"); !exists || !enabled {
		t.Fatalf("restored override = (%v, %v), want (true, true)", enabled, exists)
	}
	if lock, err := store.GetLock(ctx, "/restored/readme.md"); err != nil || lock.Holder != "writer" {
		t.Fatalf("restored lock = (%+v, %v), want writer", lock, err)
	}

	storedOperation, err := store.GetTrashOperation(ctx, operation.ID)
	if err != nil || !trashOperationsEqual(*storedOperation, *operation) {
		t.Fatalf("restore outbox = (%+v, %v), want %+v", storedOperation, err, operation)
	}
	if err := store.CommitTrashRestore(ctx, item, "/restored", index, true, operation); err != nil {
		t.Fatalf("idempotent CommitTrashRestore() error: %v", err)
	}
}

func TestCommitTrashRestore_MapsTrashIDConflictAndRollsBackAllTables(t *testing.T) {
	store := setupTrashOperationsStore(t)
	ctx := context.Background()
	item := testTrashOperationItem()
	item.ID = "trash-restore-rollback"
	if err := store.AddToTrash(ctx, item); err != nil {
		t.Fatalf("AddToTrash() error: %v", err)
	}
	if err := store.AddVersion(ctx, "/docs/readme.md", "original-version", 100, ""); err != nil {
		t.Fatalf("AddVersion() error: %v", err)
	}
	if err := store.SetVersioningOverride(ctx, "/docs/readme.md", true); err != nil {
		t.Fatalf("SetVersioningOverride() error: %v", err)
	}
	if err := store.AcquireLock(ctx, "/docs/readme.md", "writer", WriteLock, time.Hour); err != nil {
		t.Fatalf("AcquireLock() error: %v", err)
	}
	now := time.Unix(1_700_000_400, 0)
	if err := store.UpdateFileIndex(ctx, "/target/stale.txt", 1, now, "stale-hash"); err != nil {
		t.Fatalf("UpdateFileIndex() error: %v", err)
	}
	competingOperationID := strings.Repeat("e", 32)
	if _, err := store.db.ExecContext(ctx, `
		CREATE TRIGGER inject_competing_restore_operation
		AFTER DELETE ON trash
		BEGIN
			INSERT INTO trash_operations (
				operation_id, kind, trash_id, journal_hash, participant_payload
			) VALUES (
				'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee',
				'restore_from_trash',
				OLD.id,
				'ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff',
				x''
			);
		END;
	`); err != nil {
		t.Fatalf("create failure trigger error: %v", err)
	}

	operation := testTrashOperation(TrashOperationKindRestoreFromTrash, item.ID, "5", "6")
	index := []FileIndexEntry{{Path: "/target/readme.md", Size: 100, ModTime: now, ContentHash: "new-hash"}}
	if err := store.CommitTrashRestore(ctx, item, "/target", index, true, operation); !errors.Is(err, ErrTrashOperationConflict) {
		t.Fatalf("CommitTrashRestore() error = %v, want ErrTrashOperationConflict", err)
	}
	if _, err := store.GetTrashItem(ctx, item.ID); err != nil {
		t.Fatalf("Trash item was not rolled back: %v", err)
	}
	if _, _, hash, err := store.GetFileIndex(ctx, "/target/stale.txt"); err != nil || hash != "stale-hash" {
		t.Fatalf("stale target index after rollback = (%q, %v), want (stale-hash, nil)", hash, err)
	}
	if _, _, _, err := store.GetFileIndex(ctx, "/target/readme.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("new target index after rollback error = %v, want ErrNotFound", err)
	}
	if versions, err := store.GetVersions(ctx, "/docs/readme.md"); err != nil || len(versions) != 1 {
		t.Fatalf("source versions after rollback = (%+v, %v), want one", versions, err)
	}
	if versions, err := store.GetVersions(ctx, "/target/readme.md"); err != nil || len(versions) != 0 {
		t.Fatalf("target versions after rollback = (%+v, %v), want empty", versions, err)
	}
	if enabled, exists := store.GetVersioningOverride(ctx, "/docs/readme.md"); !exists || !enabled {
		t.Fatalf("source override after rollback = (%v, %v), want (true, true)", enabled, exists)
	}
	if lock, err := store.GetLock(ctx, "/docs/readme.md"); err != nil || lock.Holder != "writer" {
		t.Fatalf("source lock after rollback = (%+v, %v), want writer", lock, err)
	}
	if _, err := store.GetTrashOperation(ctx, operation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTrashOperation() after rollback error = %v, want ErrNotFound", err)
	}
	if _, err := store.GetTrashOperation(ctx, competingOperationID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("competing operation survived rollback: %v", err)
	}
}

func TestCommitTrashRestore_RejectsMismatchedTrashItemWithoutMutation(t *testing.T) {
	store := setupTrashOperationsStore(t)
	ctx := context.Background()
	item := testTrashOperationItem()
	item.ID = "trash-restore-mismatch"
	if err := store.AddToTrash(ctx, item); err != nil {
		t.Fatalf("AddToTrash() error: %v", err)
	}
	if err := store.UpdateFileIndex(ctx, "/target/existing.txt", 10, time.Unix(1_700_000_500, 0), "existing-hash"); err != nil {
		t.Fatalf("UpdateFileIndex() error: %v", err)
	}

	mismatched := *item
	mismatched.RestoreData = []byte("different")
	operation := testTrashOperation(TrashOperationKindRestoreFromTrash, item.ID, "7", "8")
	err := store.CommitTrashRestore(ctx, &mismatched, "/target", []FileIndexEntry{
		{Path: "/target/new.txt", Size: 1, ModTime: time.Unix(1_700_000_501, 0), ContentHash: "new-hash"},
	}, false, operation)
	if !errors.Is(err, ErrTrashItemMismatch) {
		t.Fatalf("CommitTrashRestore() error = %v, want ErrTrashItemMismatch", err)
	}
	if _, err := store.GetTrashItem(ctx, item.ID); err != nil {
		t.Fatalf("Trash item changed after mismatch: %v", err)
	}
	if _, _, hash, err := store.GetFileIndex(ctx, "/target/existing.txt"); err != nil || hash != "existing-hash" {
		t.Fatalf("existing target index = (%q, %v), want (existing-hash, nil)", hash, err)
	}
	if _, _, _, err := store.GetFileIndex(ctx, "/target/new.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("new target index after mismatch error = %v, want ErrNotFound", err)
	}
	if _, err := store.GetTrashOperation(ctx, operation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTrashOperation() after mismatch error = %v, want ErrNotFound", err)
	}
}

func TestCommitTrashDelete_ValidatesOperationFields(t *testing.T) {
	store := setupTrashOperationsStore(t)
	ctx := context.Background()
	item := testTrashOperationItem()
	item.ID = "trash-operation-validation"
	base := testTrashOperation(TrashOperationKindDeleteToTrash, item.ID, "a", "b")

	tests := []struct {
		name   string
		mutate func(*TrashOperation)
	}{
		{name: "short ID", mutate: func(operation *TrashOperation) { operation.ID = strings.Repeat("a", 31) }},
		{name: "non-hex ID", mutate: func(operation *TrashOperation) { operation.ID = strings.Repeat("g", 32) }},
		{name: "invalid kind", mutate: func(operation *TrashOperation) { operation.Kind = "purge" }},
		{name: "mismatched Trash ID", mutate: func(operation *TrashOperation) { operation.TrashID = "different-trash-item" }},
		{name: "short hash", mutate: func(operation *TrashOperation) { operation.JournalHash = strings.Repeat("b", 63) }},
		{name: "non-hex hash", mutate: func(operation *TrashOperation) { operation.JournalHash = strings.Repeat("z", 64) }},
		{name: "nil payload", mutate: func(operation *TrashOperation) { operation.ParticipantPayload = nil }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operation := cloneTrashOperationForTest(base)
			test.mutate(operation)
			if err := store.CommitTrashDelete(ctx, item, operation); err == nil {
				t.Fatal("CommitTrashDelete() unexpectedly succeeded")
			}
			if _, err := store.GetTrashItem(ctx, item.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("GetTrashItem() after validation failure error = %v, want ErrNotFound", err)
			}
		})
	}

	emptyPayloadOperation := cloneTrashOperationForTest(base)
	emptyPayloadOperation.ParticipantPayload = []byte{}
	if err := store.CommitTrashDelete(ctx, item, emptyPayloadOperation); err != nil {
		t.Fatalf("CommitTrashDelete(empty non-nil payload) error: %v", err)
	}
	stored, err := store.GetTrashOperation(ctx, emptyPayloadOperation.ID)
	if err != nil {
		t.Fatalf("GetTrashOperation() error: %v", err)
	}
	if stored.ParticipantPayload == nil || len(stored.ParticipantPayload) != 0 {
		t.Fatalf("stored empty payload = %#v, want non-nil empty slice", stored.ParticipantPayload)
	}
}
