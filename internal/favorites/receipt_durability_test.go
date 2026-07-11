package favorites

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStore_TrashDeleteIdempotentReplaysEstablishDurabilityBarrier(t *testing.T) {
	t.Run("pending apply", func(t *testing.T) {
		store := newReceiptDurabilityFavoritesStore(t)

		assertFavoritesReceiptDurabilityBarrier(t,
			func() error {
				return store.ApplyTrashDeleteOperation(trashDeleteOperationA, nil, false)
			},
			func() error {
				return store.ApplyTrashDeleteOperation(trashDeleteOperationA, nil, false)
			},
		)
	})

	t.Run("committed apply", func(t *testing.T) {
		store := newReceiptDurabilityFavoritesStore(t)

		assertFavoritesReceiptDurabilityBarrier(t,
			func() error {
				return store.ApplyTrashDeleteOperation(trashDeleteOperationA, nil, true)
			},
			func() error {
				return store.ApplyTrashDeleteOperation(trashDeleteOperationA, nil, true)
			},
		)
	})

	t.Run("completed replay", func(t *testing.T) {
		store := newReceiptDurabilityFavoritesStore(t)
		if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, nil, true); err != nil {
			t.Fatalf("ApplyTrashDeleteOperation() error: %v", err)
		}

		assertFavoritesReceiptDurabilityBarrier(t,
			func() error {
				return store.CompleteTrashDeleteOperation(trashDeleteOperationA)
			},
			func() error {
				return store.CompleteTrashDeleteOperation(trashDeleteOperationA)
			},
		)
	})

	t.Run("purge missing marker", func(t *testing.T) {
		store := newReceiptDurabilityFavoritesStore(t)
		completeEmptyTrashDeleteOperation(t, store, trashDeleteOperationA)

		assertFavoritesReceiptDurabilityBarrier(t,
			func() error {
				return store.PurgeCompletedTrashDeleteOperation(trashDeleteOperationA)
			},
			func() error {
				return store.PurgeCompletedTrashDeleteOperation(trashDeleteOperationA)
			},
		)
	})

	t.Run("rollback missing marker", func(t *testing.T) {
		store := newReceiptDurabilityFavoritesStore(t)
		if err := store.ApplyTrashDeleteOperation(trashDeleteOperationA, nil, false); err != nil {
			t.Fatalf("ApplyTrashDeleteOperation() error: %v", err)
		}

		assertFavoritesReceiptDurabilityBarrier(t,
			func() error {
				return store.RollbackTrashDeleteOperation(trashDeleteOperationA)
			},
			func() error {
				return store.RollbackTrashDeleteOperation(trashDeleteOperationA)
			},
		)
	})
}

func TestStore_TrashRestoreIdempotentReplaysEstablishDurabilityBarrier(t *testing.T) {
	t.Run("matching apply", func(t *testing.T) {
		store := newReceiptDurabilityFavoritesStore(t)
		completeEmptyTrashDeleteOperation(t, store, trashDeleteOperationA)

		assertFavoritesReceiptDurabilityBarrier(t,
			func() error {
				return store.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationA, nil, nil)
			},
			func() error {
				return store.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationA, nil, nil)
			},
		)
	})

	t.Run("complete missing receipt", func(t *testing.T) {
		store := newReceiptDurabilityFavoritesStore(t)
		completeEmptyTrashDeleteOperation(t, store, trashDeleteOperationA)
		if err := store.ApplyTrashRestoreOperation(trashDeleteOperationB, trashDeleteOperationA, nil, nil); err != nil {
			t.Fatalf("ApplyTrashRestoreOperation() error: %v", err)
		}

		assertFavoritesReceiptDurabilityBarrier(t,
			func() error {
				return store.CompleteTrashRestoreOperation(trashDeleteOperationB)
			},
			func() error {
				return store.CompleteTrashRestoreOperation(trashDeleteOperationB)
			},
		)
	})
}

func newReceiptDurabilityFavoritesStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	return store
}

func assertFavoritesReceiptDurabilityBarrier(t *testing.T, first, replay func() error) {
	t.Helper()

	originalWriter := favoritesStoreWriter
	writeCalls := 0
	favoritesStoreWriter = func(path string, data []byte) error {
		writeCalls++
		return originalWriter(path, data)
	}
	defer func() {
		favoritesStoreWriter = originalWriter
	}()

	syncFailure := errors.New("forced favorites receipt directory sync warning")
	syncCalls := 0
	restoreSync := SetSyncFavoritesStoreRootDirForTest(func(root *os.Root) error {
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
