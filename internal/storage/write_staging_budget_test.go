package storage

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/versionstore"
)

func TestWriteStagingBudgetSharesProcessCapacityAndReleases(t *testing.T) {
	budget := newWriteStagingBudget(
		8,
		0,
		func() (uint64, error) { return 1024, nil },
		func() uint64 { return 0 },
	)
	first := budget.newReservation()
	if err := first.reserve(6); err != nil {
		t.Fatalf("reserve(first) error: %v", err)
	}
	if err := first.consume(6); err != nil {
		t.Fatalf("consume(first) error: %v", err)
	}
	second := budget.newReservation()
	if err := second.reserve(3); !errors.Is(err, ErrWriteBusy) {
		t.Fatalf("reserve(over process budget) error = %v, want ErrWriteBusy", err)
	}
	first.release()
	if err := second.reserve(3); err != nil {
		t.Fatalf("reserve(after release) error: %v", err)
	}
	second.release()
	if budget.used != 0 || budget.pending != 0 {
		t.Fatalf("budget after release = used:%d pending:%d, want zero", budget.used, budget.pending)
	}
}

func TestWriteStagingBudgetPendingReservationsShareFreeSpaceBoundary(t *testing.T) {
	budget := newWriteStagingBudget(
		100,
		0,
		func() (uint64, error) { return 10, nil },
		func() uint64 { return 4 },
	)
	first := budget.newReservation()
	if err := first.reserve(4); err != nil {
		t.Fatalf("reserve(first) error: %v", err)
	}
	second := budget.newReservation()
	if err := second.reserve(3); !errors.Is(err, ErrInsufficientStorage) {
		t.Fatalf("reserve(over shared free-space boundary) error = %v, want ErrInsufficientStorage", err)
	}
	first.release()
	if err := second.reserve(3); err != nil {
		t.Fatalf("reserve(after pending release) error: %v", err)
	}
	second.release()
}

func TestWriteStagingBudgetFullProcessReservationBlocksUntilRelease(t *testing.T) {
	budget := newWriteStagingBudget(
		10,
		0,
		func() (uint64, error) { return 100, nil },
		func() uint64 { return 0 },
	)
	full := budget.newReservation()
	if err := full.reserve(10); err != nil {
		t.Fatalf("reserve(full process budget) error: %v", err)
	}
	if err := full.consume(10); err != nil {
		t.Fatalf("consume(full process budget) error: %v", err)
	}
	waiting := budget.newReservation()
	if err := waiting.reserve(1); !errors.Is(err, ErrWriteBusy) {
		t.Fatalf("reserve(while full) error = %v, want ErrWriteBusy", err)
	}
	full.release()
	if err := waiting.reserve(1); err != nil {
		t.Fatalf("reserve(after full release) error: %v", err)
	}
	waiting.release()
}

func TestWriteStagingBudgetHonorsFreeSpaceAndFailsClosed(t *testing.T) {
	available := uint64(10)
	budget := newWriteStagingBudget(
		100,
		0,
		func() (uint64, error) { return available, nil },
		func() uint64 { return 8 },
	)
	reservation := budget.newReservation()
	if err := reservation.reserve(3); !errors.Is(err, ErrInsufficientStorage) {
		t.Fatalf("reserve(below minimum free) error = %v, want ErrInsufficientStorage", err)
	}
	if err := reservation.reserve(2); err != nil {
		t.Fatalf("reserve(at minimum free boundary) error: %v", err)
	}
	reservation.release()

	budget.availableBytes = func() (uint64, error) {
		return 0, errors.New("statfs unavailable")
	}
	if err := budget.newReservation().reserve(1); !errors.Is(err, ErrInsufficientStorage) {
		t.Fatalf("reserve(statfs failure) error = %v, want ErrInsufficientStorage", err)
	}
}

func TestWriteStagingBudgetSamplesMinFreeOutsideBudgetLockAndAvailableInside(t *testing.T) {
	var budget *writeStagingBudget
	var minFreeSawUnlocked atomic.Bool
	var availableSawLocked atomic.Bool
	budget = newWriteStagingBudget(
		100,
		0,
		func() (uint64, error) {
			if budget.mu.TryLock() {
				budget.mu.Unlock()
			} else {
				availableSawLocked.Store(true)
			}
			return 100, nil
		},
		func() uint64 {
			if budget.mu.TryLock() {
				minFreeSawUnlocked.Store(true)
				budget.mu.Unlock()
			}
			return 0
		},
	)
	reservation := budget.newReservation()
	if err := reservation.reserve(1); err != nil {
		t.Fatalf("reserve() error: %v", err)
	}
	reservation.release()
	if !minFreeSawUnlocked.Load() {
		t.Fatal("minimum-free-space callback ran while budget mutex was held")
	}
	if !availableSawLocked.Load() {
		t.Fatal("available-capacity callback ran outside budget mutex")
	}
}

func TestWriteStagingBudgetRejectsStaleAvailableSampleAfterConcurrentConsume(t *testing.T) {
	var available atomic.Uint64
	available.Store(10)
	var minFreeCalls atomic.Int32
	waiterSampledMinFree := make(chan struct{})
	releaseWaiter := make(chan struct{})
	budget := newWriteStagingBudget(
		100,
		0,
		func() (uint64, error) { return available.Load(), nil },
		func() uint64 {
			if minFreeCalls.Add(1) == 1 {
				close(waiterSampledMinFree)
				<-releaseWaiter
			}
			return 0
		},
	)

	waiter := budget.newReservation()
	waiterResult := make(chan error, 1)
	go func() {
		waiterResult <- waiter.reserve(8)
	}()
	<-waiterSampledMinFree

	writer := budget.newReservation()
	if err := writer.reserve(8); err != nil {
		t.Fatalf("reserve(writer) error: %v", err)
	}
	available.Store(2)
	if err := writer.consume(8); err != nil {
		t.Fatalf("consume(writer) error: %v", err)
	}
	close(releaseWaiter)

	if err := <-waiterResult; !errors.Is(err, ErrInsufficientStorage) {
		t.Fatalf("reserve(waiter) error = %v, want ErrInsufficientStorage from refreshed capacity", err)
	}
	writer.release()
	waiter.release()
}

func TestWriteStagingReservationReleaseDoesNotDeadlockWithMinFreeSnapshot(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	budget := newWriteStagingBudget(
		100,
		0,
		func() (uint64, error) { return 100, nil },
		func() uint64 { return 0 },
	)
	first := budget.newReservation()
	if err := first.reserve(1); err != nil {
		t.Fatalf("reserve(first) error: %v", err)
	}
	if err := first.consume(1); err != nil {
		t.Fatalf("consume(first) error: %v", err)
	}

	minFreeEntered := make(chan struct{})
	budget.minFreeBytes = func() uint64 {
		close(minFreeEntered)
		fs.mu.RLock()
		defer fs.mu.RUnlock()
		return 0
	}
	fs.mu.Lock()
	secondResult := make(chan error, 1)
	go func() {
		second := budget.newReservation()
		err := second.reserve(1)
		if err == nil {
			second.release()
		}
		secondResult <- err
	}()
	<-minFreeEntered

	released := make(chan struct{})
	go func() {
		first.release()
		close(released)
	}()
	releaseBlocked := false
	select {
	case <-released:
	case <-time.After(time.Second):
		releaseBlocked = true
	}
	fs.mu.Unlock()
	if releaseBlocked {
		<-released
		t.Fatal("reservation release deadlocked behind a min-free callback waiting for filesystem mutation lock")
	}
	if err := <-secondResult; err != nil {
		t.Fatalf("reserve(second) error: %v", err)
	}
}

func TestWriteStagingBudgetRejectsOverflow(t *testing.T) {
	budget := newWriteStagingBudget(
		math.MaxUint64,
		math.MaxUint64,
		func() (uint64, error) { return math.MaxUint64, nil },
		func() uint64 { return 0 },
	)
	if err := budget.newReservation().reserve(1); !errors.Is(err, ErrWriteBusy) {
		t.Fatalf("reserve(overflow) error = %v, want ErrWriteBusy", err)
	}
}

type chunkedStagingReader struct {
	chunks [][]byte
}

func (r *chunkedStagingReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	return copy(p, chunk), nil
}

type failingStagingWriter struct {
	err error
}

func (w failingStagingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestWriteStagingBudgetWriterMapsDiskFullAndPreservesCause(t *testing.T) {
	budget := newWriteStagingBudget(
		100,
		0,
		func() (uint64, error) { return 100, nil },
		func() uint64 { return 0 },
	)
	reservation := budget.newReservation()
	writer := &writeStagingBudgetWriter{
		writer:      failingStagingWriter{err: syscall.ENOSPC},
		reservation: reservation,
		maxBytes:    100,
	}
	if _, err := writer.Write([]byte("data")); !errors.Is(err, ErrInsufficientStorage) ||
		!errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("Write() error = %v, want ErrInsufficientStorage and ENOSPC", err)
	}
	reservation.trimPending()
	reservation.release()
}

func TestFileSystemWritePublishParentSyncMapsCapacityErrorAndPreservesCause(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	if err := fs.Mkdir(t.Context(), "/capacity-parent"); err != nil {
		t.Fatalf("Mkdir(capacity-parent) error: %v", err)
	}
	originalRecoveryHook := writeTransactionRecoveryFaultHook
	var injected atomic.Bool
	writeTransactionRecoveryFaultHook = func(point string) error {
		if point == "namespace:before-target-parent-sync" &&
			injected.CompareAndSwap(false, true) {
			return syscall.ENOSPC
		}
		return originalRecoveryHook(point)
	}
	t.Cleanup(func() { writeTransactionRecoveryFaultHook = originalRecoveryHook })

	err := fs.WriteFile(t.Context(), "/capacity-parent/target.bin", strings.NewReader("data"))
	if !errors.Is(err, ErrInsufficientStorage) || !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("WriteFile() error = %v, want ErrInsufficientStorage and ENOSPC", err)
	}
	if errors.Is(err, ErrWriteRecoveryRequired) {
		t.Fatalf("WriteFile() error = %v, successful rollback must not require recovery", err)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath("/capacity-parent/target.bin")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target after rejected parent preparation stat error = %v, want not exist", statErr)
	}
}

func TestFileSystemWriteStagingBudgetChecksIncrementalGrowth(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	availableCalls := 0
	fs.writeStagingAvailable = func() (uint64, error) {
		availableCalls++
		if availableCalls == 1 {
			return 5, nil
		}
		return 2, nil
	}
	fs.writeStagingBudget = newWriteStagingBudget(
		100,
		0,
		func() (uint64, error) { return fs.writeStagingAvailable() },
		func() uint64 { return 0 },
	)

	err := fs.WriteFile(t.Context(), "/incremental-budget.bin", &chunkedStagingReader{
		chunks: [][]byte{[]byte("abc"), []byte("def")},
	})
	if !errors.Is(err, ErrInsufficientStorage) {
		t.Fatalf("WriteFile() error = %v, want ErrInsufficientStorage", err)
	}
	if availableCalls < 2 {
		t.Fatalf("available capacity checks = %d, want incremental recheck", availableCalls)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath("/incremental-budget.bin")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target after rejected growth stat error = %v, want not exist", statErr)
	}
	fs.writeStagingBudget.mu.Lock()
	defer fs.writeStagingBudget.mu.Unlock()
	if fs.writeStagingBudget.used != 0 || fs.writeStagingBudget.pending != 0 {
		t.Fatalf(
			"budget after rejected growth cleanup = used:%d pending:%d, want zero",
			fs.writeStagingBudget.used,
			fs.writeStagingBudget.pending,
		)
	}
}

func TestFileSystemSuccessfulWritesReleaseStagingBudget(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	assertBudgetEmpty := func(stage string) {
		t.Helper()
		fs.writeStagingBudget.mu.Lock()
		defer fs.writeStagingBudget.mu.Unlock()
		if fs.writeStagingBudget.used != 0 || fs.writeStagingBudget.pending != 0 {
			t.Fatalf(
				"%s budget = used:%d pending:%d, want zero",
				stage,
				fs.writeStagingBudget.used,
				fs.writeStagingBudget.pending,
			)
		}
	}
	if err := fs.WriteFile(t.Context(), "/budget-release.bin", strings.NewReader("new target")); err != nil {
		t.Fatalf("WriteFile(new) error: %v", err)
	}
	assertBudgetEmpty("new write")
	if err := fs.WriteFile(t.Context(), "/budget-release.bin", strings.NewReader("replacement")); err != nil {
		t.Fatalf("WriteFile(overwrite) error: %v", err)
	}
	assertBudgetEmpty("overwrite")
}

func TestNewWriteStagingBudgetMinFreeCallbackBalancesFileSystemReadLock(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	if fs.writeStagingBudget == nil || fs.writeStagingBudget.minFreeBytes == nil {
		t.Fatal("New() did not configure the write staging minimum-free-space callback")
	}

	for range 2 {
		if got := fs.writeStagingBudget.minFreeBytes(); got != fs.config.MinFreeSpace {
			t.Fatalf("minFreeBytes() = %d, want %d", got, fs.config.MinFreeSpace)
		}
	}

	fs.mu.Lock()
	fs.mu.Unlock()
}

func TestFileSystemWriteStagingBudgetUsesUpdatedMinFreeSpace(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	fs.writeStagingAvailable = func() (uint64, error) { return 10, nil }
	fs.writeStagingBudget = newWriteStagingBudget(
		100,
		0,
		func() (uint64, error) { return fs.writeStagingAvailable() },
		func() uint64 {
			fs.mu.RLock()
			defer fs.mu.RUnlock()
			return fs.config.MinFreeSpace
		},
	)

	fs.UpdateRetentionSettings(fs.config.MaxVersions, fs.config.MaxVersionAge, 8, fs.config.RetentionSweepInterval)
	if err := fs.WriteFile(t.Context(), "/min-free-blocked.bin", strings.NewReader("abc")); !errors.Is(err, ErrInsufficientStorage) {
		t.Fatalf("WriteFile(high min free) error = %v, want ErrInsufficientStorage", err)
	}
	fs.UpdateRetentionSettings(fs.config.MaxVersions, fs.config.MaxVersionAge, 0, fs.config.RetentionSweepInterval)
	if err := fs.WriteFile(t.Context(), "/min-free-allowed.bin", strings.NewReader("abc")); err != nil {
		t.Fatalf("WriteFile(updated min free) error: %v", err)
	}
}

func TestFileSystemWriteStagingCleanupResidueRetainsBudgetAndBlocksWrites(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	cleanupErr := errors.New("remove source staging failed")
	originalRemoveStagedWriteFile := removeStagedWriteFile
	removeStagedWriteFile = func(root *os.Root, rel string) error {
		if strings.HasPrefix(filepath.Base(rel), writeSourceStagePrefix) {
			return cleanupErr
		}
		return originalRemoveStagedWriteFile(root, rel)
	}
	t.Cleanup(func() { removeStagedWriteFile = originalRemoveStagedWriteFile })

	readerErr := errors.New("request body failed")
	err := fs.WriteFile(context.Background(), "/residue.bin", &partialErrorReader{
		data: []byte("retained staged bytes"),
		err:  readerErr,
	})
	if !errors.Is(err, readerErr) || !errors.Is(err, cleanupErr) ||
		!errors.Is(err, ErrWriteRecoveryRequired) {
		t.Fatalf("WriteFile() error = %v, want reader, cleanup, and recovery errors", err)
	}
	fs.writeStagingBudget.mu.Lock()
	retainedBytes := fs.writeStagingBudget.used
	fs.writeStagingBudget.mu.Unlock()
	if retainedBytes == 0 {
		t.Fatal("cleanup residue released its staging reservation")
	}
	if err := fs.WriteFile(t.Context(), "/blocked-after-residue.bin", strings.NewReader("blocked")); !errors.Is(err, ErrWriteRecoveryRequired) {
		t.Fatalf("WriteFile(after residue) error = %v, want ErrWriteRecoveryRequired", err)
	}
}

func TestFileSystemRolledBackPublishedResidueRetainsBudgetAndBlocksWrites(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	indexErr := errors.New("index update failed")
	store := newWriteTransactionRuntimeTestStore(fs)
	fs.writeTransactionStore = store
	store.ensureWriteMetadataFn = func(context.Context, versionstore.WriteMetadataPlan) error {
		return indexErr
	}
	cleanupErr := errors.New("remove rolled-back published stage failed")
	originalRecoveryHook := writeTransactionRecoveryFaultHook
	writeTransactionRecoveryFaultHook = func(point string) error {
		if strings.HasPrefix(point, "remove-stage:") &&
			!strings.HasPrefix(point, "remove-stage:after-unlink:") {
			return cleanupErr
		}
		return originalRecoveryHook(point)
	}
	t.Cleanup(func() { writeTransactionRecoveryFaultHook = originalRecoveryHook })

	err := fs.WriteFile(t.Context(), "/rolled-back-residue.bin", strings.NewReader("published then rolled back"))
	if !errors.Is(err, indexErr) || !errors.Is(err, cleanupErr) ||
		!errors.Is(err, ErrWriteRecoveryRequired) {
		t.Fatalf("WriteFile() error = %v, want index, cleanup, and recovery errors", err)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath("/rolled-back-residue.bin")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rolled-back target stat error = %v, want not exist", statErr)
	}
	fs.writeStagingBudget.mu.Lock()
	retainedBytes := fs.writeStagingBudget.used
	fs.writeStagingBudget.mu.Unlock()
	if retainedBytes == 0 {
		t.Fatal("rolled-back published residue released its staging reservation")
	}
	if err := fs.WriteFile(t.Context(), "/blocked-after-rolled-back-residue.bin", strings.NewReader("blocked")); !errors.Is(err, ErrWriteRecoveryRequired) {
		t.Fatalf("WriteFile(after rolled-back residue) error = %v, want ErrWriteRecoveryRequired", err)
	}
}

func TestFileSystemCleanupStagingResetsRecoveredBudgetUsage(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	staged, err := fs.stageWriteReader(t.Context(), strings.NewReader("orphaned source"), defaultMaxWriteSize, writeSourceStagePrefix)
	if err != nil {
		t.Fatalf("stageWriteReader() error: %v", err)
	}
	if err := staged.file.Close(); err != nil {
		t.Fatalf("Close(staged) error: %v", err)
	}
	staged.file = nil

	files, bytes, err := fs.CleanupStaging(t.Context())
	if err != nil {
		t.Fatalf("CleanupStaging() error: %v", err)
	}
	if files != 1 || bytes != int64(len("orphaned source")) {
		t.Fatalf("CleanupStaging() = (%d, %d), want (1, %d)", files, bytes, len("orphaned source"))
	}
	fs.writeStagingBudget.mu.Lock()
	defer fs.writeStagingBudget.mu.Unlock()
	if fs.writeStagingBudget.used != 0 || fs.writeStagingBudget.pending != 0 {
		t.Fatalf(
			"budget after verified cleanup = used:%d pending:%d, want zero",
			fs.writeStagingBudget.used,
			fs.writeStagingBudget.pending,
		)
	}
}

func TestFileSystemCleanupStagingSyncFailureRetainsBudgetAndBlocksWrites(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	staged, err := fs.stageWriteReader(t.Context(), strings.NewReader("cleanup sync residue"), defaultMaxWriteSize, writeSourceStagePrefix)
	if err != nil {
		t.Fatalf("stageWriteReader() error: %v", err)
	}
	if err := staged.file.Close(); err != nil {
		t.Fatalf("Close(staged) error: %v", err)
	}
	staged.file = nil

	syncErr := errors.New("cleanup staging sync failed")
	originalSyncWriteStagingDirectory := syncWriteStagingDirectory
	syncWriteStagingDirectory = func(*os.Root) error {
		return syncErr
	}
	t.Cleanup(func() {
		syncWriteStagingDirectory = originalSyncWriteStagingDirectory
	})

	files, _, err := fs.CleanupStaging(t.Context())
	if files != 1 || !errors.Is(err, syncErr) || !errors.Is(err, ErrWriteRecoveryRequired) {
		t.Fatalf("CleanupStaging() = files:%d error:%v, want one removal plus sync and recovery errors", files, err)
	}
	fs.writeStagingBudget.mu.Lock()
	retainedBytes := fs.writeStagingBudget.used
	fs.writeStagingBudget.mu.Unlock()
	if retainedBytes == 0 {
		t.Fatal("failed cleanup durability barrier released the staging budget")
	}
	if err := fs.WriteFile(t.Context(), "/blocked-after-cleanup-sync.bin", strings.NewReader("blocked")); !errors.Is(err, ErrWriteRecoveryRequired) {
		t.Fatalf("WriteFile(after cleanup sync failure) error = %v, want ErrWriteRecoveryRequired", err)
	}
}

func TestFileSystemRestartInventoriesWriteStagingUsage(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	staged, err := fs.stageWriteReader(t.Context(), strings.NewReader("restart residue"), defaultMaxWriteSize, writeSourceStagePrefix)
	if err != nil {
		t.Fatalf("stageWriteReader() error: %v", err)
	}
	if err := staged.file.Close(); err != nil {
		t.Fatalf("Close(staged) error: %v", err)
	}
	staged.file = nil
	cfg := *fs.config
	if err := fs.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	restarted, err := New(&cfg)
	if err != nil {
		t.Fatalf("New(restart) error: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	restarted.writeStagingBudget.mu.Lock()
	inventoried := restarted.writeStagingBudget.used
	restarted.writeStagingBudget.mu.Unlock()
	if inventoried != uint64(len("restart residue")) {
		t.Fatalf("inventoried staging bytes = %d, want %d", inventoried, len("restart residue"))
	}
	if _, _, err := restarted.CleanupStaging(t.Context()); err != nil {
		t.Fatalf("CleanupStaging(restart) error: %v", err)
	}
	restarted.writeStagingBudget.mu.Lock()
	defer restarted.writeStagingBudget.mu.Unlock()
	if restarted.writeStagingBudget.used != 0 {
		t.Fatalf("staging bytes after restart cleanup = %d, want zero", restarted.writeStagingBudget.used)
	}
}
