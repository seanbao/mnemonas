package storage

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/seanbao/mnemonas/internal/rootio"
)

const maxWriteStagingBytes uint64 = uint64(defaultMaxWriteSize)

type writeStagingBudget struct {
	mu             sync.Mutex
	limit          uint64
	used           uint64
	pending        uint64
	availableBytes func() (uint64, error)
	minFreeBytes   func() uint64
}

func mapWriteStorageCapacityError(err error) error {
	if err == nil || !isWriteStorageCapacityError(err) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrInsufficientStorage, err)
}

type writeStagingReservation struct {
	budget   *writeStagingBudget
	reserved uint64
	pending  uint64
	released bool
}

func newWriteStagingBudget(
	limit uint64,
	initialUsed uint64,
	availableBytes func() (uint64, error),
	minFreeBytes func() uint64,
) *writeStagingBudget {
	if initialUsed > limit {
		initialUsed = limit
	}
	return &writeStagingBudget{
		limit:          limit,
		used:           initialUsed,
		availableBytes: availableBytes,
		minFreeBytes:   minFreeBytes,
	}
}

func scanInitialWriteStagingBytes(root *os.Root) (uint64, error) {
	if root == nil {
		return 0, errors.New("write staging root is unavailable")
	}
	dir, err := rootio.OpenDirNoFollow(root, writeStagingDir)
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return 0, err
	}
	var used uint64
	for _, entry := range entries {
		info, err := root.Lstat(filepath.Join(writeStagingDir, entry.Name()))
		if err != nil {
			return used, err
		}
		if !info.Mode().IsRegular() || info.Size() < 0 {
			return used, fmt.Errorf("write staging entry %s is not a regular file", entry.Name())
		}
		size := uint64(info.Size())
		if size > math.MaxUint64-used {
			return math.MaxUint64, errors.New("write staging usage overflow")
		}
		used += size
	}
	return used, nil
}

func (b *writeStagingBudget) newReservation() *writeStagingReservation {
	return &writeStagingReservation{budget: b}
}

func (r *writeStagingReservation) reserve(bytes uint64) error {
	if bytes == 0 {
		return nil
	}
	if r == nil || r.budget == nil {
		return errors.New("write staging budget is unavailable")
	}
	b := r.budget
	if b.availableBytes == nil {
		return fmt.Errorf("%w: available capacity cannot be determined", ErrInsufficientStorage)
	}
	// The minimum-free-space callback may acquire FileSystem.mu. Sample it
	// before budget.mu so publish and cleanup paths can release reservations
	// while holding the filesystem mutation lock without a lock-order cycle.
	minFree := uint64(0)
	if b.minFreeBytes != nil {
		minFree = b.minFreeBytes()
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if r.released {
		return errors.New("write staging reservation is already released")
	}
	// Serialize the statfs sample with reservation accounting. Otherwise a
	// waiter can reuse a stale available-byte sample after another writer has
	// consumed its pending reservation and allocated those bytes on disk.
	available, err := b.availableBytes()
	if err != nil {
		return fmt.Errorf("%w: inspect staging filesystem capacity: %w", ErrInsufficientStorage, err)
	}
	if bytes > math.MaxUint64-b.used || bytes > math.MaxUint64-r.reserved ||
		bytes > math.MaxUint64-b.pending || bytes > math.MaxUint64-r.pending {
		return ErrWriteBusy
	}
	if b.used+bytes > b.limit {
		return ErrWriteBusy
	}
	requiredPending := b.pending + bytes
	if minFree > math.MaxUint64-requiredPending || available < minFree+requiredPending {
		return fmt.Errorf(
			"%w: available=%d minimum_free=%d pending=%d requested=%d",
			ErrInsufficientStorage,
			available,
			minFree,
			b.pending,
			bytes,
		)
	}
	b.used += bytes
	b.pending += bytes
	r.reserved += bytes
	r.pending += bytes
	return nil
}

func (r *writeStagingReservation) consume(bytes uint64) error {
	if bytes == 0 {
		return nil
	}
	if r == nil || r.budget == nil {
		return errors.New("write staging budget is unavailable")
	}
	b := r.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.released || bytes > r.pending || bytes > b.pending {
		return errors.New("write staging reservation accounting is inconsistent")
	}
	r.pending -= bytes
	b.pending -= bytes
	return nil
}

func (r *writeStagingReservation) trimPending() {
	if r == nil || r.budget == nil {
		return
	}
	b := r.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.released || r.pending == 0 {
		return
	}
	unused := r.pending
	r.pending = 0
	r.reserved -= unused
	b.pending -= unused
	b.used -= unused
}

func (r *writeStagingReservation) release() {
	if r == nil || r.budget == nil {
		return
	}
	b := r.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.released {
		return
	}
	b.used -= r.reserved
	b.pending -= r.pending
	r.reserved = 0
	r.pending = 0
	r.released = true
}

func (b *writeStagingBudget) resetAfterVerifiedCleanup() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.used = 0
	b.pending = 0
	b.mu.Unlock()
}

type writeStagingBudgetWriter struct {
	writer      io.Writer
	reservation *writeStagingReservation
	maxBytes    uint64
	written     uint64
}

func (w *writeStagingBudgetWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if w == nil || w.writer == nil || w.reservation == nil {
		return 0, errors.New("write staging budget writer is unavailable")
	}
	request := uint64(len(p))
	if w.maxBytes > 0 {
		if w.written >= w.maxBytes {
			return 0, ErrFileTooLarge
		}
		remaining := w.maxBytes - w.written
		if request > remaining {
			request = remaining
		}
	}
	if request == 0 {
		return 0, ErrFileTooLarge
	}
	if w.reservation.pending < request {
		grow := request - w.reservation.pending
		if w.maxBytes > 0 {
			remainingReservation := w.maxBytes - w.written - w.reservation.pending
			if grow > remainingReservation {
				grow = remainingReservation
			}
		}
		if err := w.reservation.reserve(grow); err != nil {
			return 0, err
		}
	}
	n, err := w.writer.Write(p[:request])
	err = mapWriteStorageCapacityError(err)
	if n > 0 {
		if consumeErr := w.reservation.consume(uint64(n)); consumeErr != nil {
			return n, errors.Join(err, consumeErr)
		}
		w.written += uint64(n)
	}
	if err == nil && n < len(p) {
		if uint64(n) == request && request < uint64(len(p)) {
			err = ErrFileTooLarge
		} else {
			err = io.ErrShortWrite
		}
	}
	return n, err
}
