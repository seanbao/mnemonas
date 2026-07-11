package share

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const shareReceiptDurabilityOperationID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const shareReceiptDurabilityDeleteOperationID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestShareStore_TrashDeleteIdempotentReplaysEstablishDurabilityBarrier(t *testing.T) {
	t.Run("pending apply", func(t *testing.T) {
		store := newReceiptDurabilityShareStore(t)

		assertShareReceiptDurabilityBarrier(t,
			func() error {
				return store.ApplyTrashDeleteOperation(shareReceiptDurabilityOperationID, nil, false)
			},
			func() error {
				return store.ApplyTrashDeleteOperation(shareReceiptDurabilityOperationID, nil, false)
			},
		)
	})

	t.Run("committed apply", func(t *testing.T) {
		store := newReceiptDurabilityShareStore(t)

		assertShareReceiptDurabilityBarrier(t,
			func() error {
				return store.ApplyTrashDeleteOperation(shareReceiptDurabilityOperationID, nil, true)
			},
			func() error {
				return store.ApplyTrashDeleteOperation(shareReceiptDurabilityOperationID, nil, true)
			},
		)
	})

	t.Run("complete missing receipt", func(t *testing.T) {
		store := newReceiptDurabilityShareStore(t)
		if err := store.ApplyTrashDeleteOperation(shareReceiptDurabilityOperationID, nil, true); err != nil {
			t.Fatalf("ApplyTrashDeleteOperation() error: %v", err)
		}

		assertShareReceiptDurabilityBarrier(t,
			func() error {
				return store.CompleteTrashDeleteOperation(shareReceiptDurabilityOperationID)
			},
			func() error {
				return store.CompleteTrashDeleteOperation(shareReceiptDurabilityOperationID)
			},
		)
	})

	t.Run("rollback missing marker", func(t *testing.T) {
		store := newReceiptDurabilityShareStore(t)
		if err := store.ApplyTrashDeleteOperation(shareReceiptDurabilityOperationID, nil, false); err != nil {
			t.Fatalf("ApplyTrashDeleteOperation() error: %v", err)
		}

		assertShareReceiptDurabilityBarrier(t,
			func() error {
				return store.RollbackTrashDeleteOperation(shareReceiptDurabilityOperationID)
			},
			func() error {
				return store.RollbackTrashDeleteOperation(shareReceiptDurabilityOperationID)
			},
		)
	})
}

func TestShareStore_TrashRestoreIdempotentReplaysEstablishDurabilityBarrier(t *testing.T) {
	t.Run("matching apply", func(t *testing.T) {
		store := newReceiptDurabilityShareStore(t)
		prepareCompletedEmptyShareDelete(t, store)

		assertShareReceiptDurabilityBarrier(t,
			func() error {
				return store.ApplyTrashRestoreOperation(
					shareReceiptDurabilityOperationID,
					shareReceiptDurabilityDeleteOperationID,
					[]*Share{},
					[]*Share{},
				)
			},
			func() error {
				return store.ApplyTrashRestoreOperation(
					shareReceiptDurabilityOperationID,
					shareReceiptDurabilityDeleteOperationID,
					[]*Share{},
					[]*Share{},
				)
			},
		)
	})

	t.Run("complete missing receipt", func(t *testing.T) {
		store := newReceiptDurabilityShareStore(t)
		prepareCompletedEmptyShareDelete(t, store)
		if err := store.ApplyTrashRestoreOperation(
			shareReceiptDurabilityOperationID,
			shareReceiptDurabilityDeleteOperationID,
			[]*Share{},
			[]*Share{},
		); err != nil {
			t.Fatalf("ApplyTrashRestoreOperation() error: %v", err)
		}

		assertShareReceiptDurabilityBarrier(t,
			func() error {
				return store.CompleteTrashRestoreOperation(shareReceiptDurabilityOperationID)
			},
			func() error {
				return store.CompleteTrashRestoreOperation(shareReceiptDurabilityOperationID)
			},
		)
	})
}

func prepareCompletedEmptyShareDelete(t *testing.T, store *ShareStore) {
	t.Helper()
	if err := store.ApplyTrashDeleteOperation(shareReceiptDurabilityDeleteOperationID, nil, true); err != nil {
		t.Fatalf("ApplyTrashDeleteOperation(setup) error: %v", err)
	}
	if err := store.CompleteTrashDeleteOperation(shareReceiptDurabilityDeleteOperationID); err != nil {
		t.Fatalf("CompleteTrashDeleteOperation(setup) error: %v", err)
	}
}

func newReceiptDurabilityShareStore(t *testing.T) *ShareStore {
	t.Helper()
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	return store
}

func assertShareReceiptDurabilityBarrier(t *testing.T, first, replay func() error) {
	t.Helper()

	originalWriter := shareStoreWriter
	writeCalls := 0
	shareStoreWriter = func(path string, data []byte) error {
		writeCalls++
		return originalWriter(path, data)
	}
	defer func() {
		shareStoreWriter = originalWriter
	}()

	syncFailure := errors.New("forced share receipt directory sync warning")
	syncCalls := 0
	restoreSync := SetSyncShareStoreRootDirForTest(func(root *os.Root) error {
		syncCalls++
		if syncCalls == 1 {
			return syncFailure
		}
		return nil
	})
	defer restoreSync()

	if err := first(); !IsPersistenceWarning(err) || !errors.Is(err, syncFailure) {
		t.Fatalf("first operation error = %v, want persistence warning wrapping %v", err, syncFailure)
	}
	if err := replay(); err != nil {
		t.Fatalf("idempotent replay error: %v", err)
	}
	if writeCalls != 2 {
		t.Fatalf("store rewrite calls = %d, want 2", writeCalls)
	}
	if syncCalls != 2 {
		t.Fatalf("store directory sync calls = %d, want 2", syncCalls)
	}
}
