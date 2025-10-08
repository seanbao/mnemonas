// Package webdav provides WebDAV protocol HTTP handler
package webdav

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPathLock_BasicLockUnlock(t *testing.T) {
	pl := NewPathLock()

	pl.Lock("/test")
	if pl.Size() != 1 {
		t.Errorf("Size() = %d, want 1", pl.Size())
	}

	pl.Unlock("/test")
	if pl.Size() != 0 {
		t.Errorf("Size() after unlock = %d, want 0", pl.Size())
	}
}

func TestPathLock_ReadLock(t *testing.T) {
	pl := NewPathLock()

	// Multiple readers should be allowed
	pl.RLock("/test")
	pl.RLock("/test")

	if pl.Size() != 1 {
		t.Errorf("Size() = %d, want 1 (same path)", pl.Size())
	}

	pl.RUnlock("/test")
	pl.RUnlock("/test")

	if pl.Size() != 0 {
		t.Errorf("Size() after unlocks = %d, want 0", pl.Size())
	}
}

func TestPathLock_TryLock(t *testing.T) {
	pl := NewPathLock()

	// First TryLock should succeed
	if !pl.TryLock("/test") {
		t.Error("First TryLock should succeed")
	}

	// Second TryLock on same path should fail
	if pl.TryLock("/test") {
		t.Error("Second TryLock should fail")
	}

	pl.Unlock("/test")

	// TryLock should succeed after unlock
	if !pl.TryLock("/test") {
		t.Error("TryLock after unlock should succeed")
	}
	pl.Unlock("/test")
}

func TestPathLock_TryRLock(t *testing.T) {
	pl := NewPathLock()

	// Multiple TryRLock should succeed
	if !pl.TryRLock("/test") {
		t.Error("First TryRLock should succeed")
	}
	if !pl.TryRLock("/test") {
		t.Error("Second TryRLock should succeed")
	}

	pl.RUnlock("/test")
	pl.RUnlock("/test")
}

func TestPathLock_TryRLock_BlockedByWrite(t *testing.T) {
	pl := NewPathLock()

	pl.Lock("/test")

	// TryRLock should fail when write locked
	if pl.TryRLock("/test") {
		t.Error("TryRLock should fail when write locked")
	}

	pl.Unlock("/test")
}

func TestPathLock_DifferentPaths(t *testing.T) {
	pl := NewPathLock()

	pl.Lock("/path1")
	pl.Lock("/path2")

	if pl.Size() != 2 {
		t.Errorf("Size() = %d, want 2", pl.Size())
	}

	pl.Unlock("/path1")
	pl.Unlock("/path2")

	if pl.Size() != 0 {
		t.Errorf("Size() = %d, want 0", pl.Size())
	}
}

func TestPathLock_ConcurrentReaders(t *testing.T) {
	pl := NewPathLock()
	var counter int32
	var wg sync.WaitGroup

	numReaders := 100
	entered := make(chan struct{}, numReaders)
	release := make(chan struct{})

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pl.RLock("/shared")
			atomic.AddInt32(&counter, 1)
			entered <- struct{}{}
			<-release
			atomic.AddInt32(&counter, -1)
			pl.RUnlock("/shared")
		}()
	}

	for i := 0; i < numReaders; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			close(release)
			t.Fatalf("timed out waiting for reader %d to enter", i+1)
		}
	}
	if got := atomic.LoadInt32(&counter); got != int32(numReaders) {
		close(release)
		t.Fatalf("concurrent readers = %d, want %d", got, numReaders)
	}
	close(release)
	wg.Wait()

	if pl.Size() != 0 {
		t.Errorf("Size() after all readers = %d, want 0", pl.Size())
	}
}

func TestPathLock_WriterBlocksReaders(t *testing.T) {
	pl := NewPathLock()
	var order []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Writer acquires lock first
	pl.Lock("/test")

	// Start reader goroutine (will be blocked)
	readerReady := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(readerReady)
		pl.RLock("/test")
		mu.Lock()
		order = append(order, "reader")
		mu.Unlock()
		pl.RUnlock("/test")
	}()

	<-readerReady

	mu.Lock()
	order = append(order, "writer")
	mu.Unlock()

	// Release writer lock
	pl.Unlock("/test")

	wg.Wait()

	if len(order) != 2 || order[0] != "writer" || order[1] != "reader" {
		t.Errorf("Order = %v, want [writer reader]", order)
	}
}

func TestPathLock_RefCountCleanup(t *testing.T) {
	pl := NewPathLock()

	// Acquire multiple locks
	pl.RLock("/test")
	pl.RLock("/test")
	pl.RLock("/test")

	if pl.Size() != 1 {
		t.Errorf("Size() = %d, want 1", pl.Size())
	}

	// Release all locks
	pl.RUnlock("/test")
	pl.RUnlock("/test")
	pl.RUnlock("/test")

	if pl.Size() != 0 {
		t.Errorf("Size() after all unlocks = %d, want 0", pl.Size())
	}
}

func TestPathLock_UnlockNonexistent(t *testing.T) {
	pl := NewPathLock()

	// Should not panic
	pl.Unlock("/nonexistent")
	pl.RUnlock("/nonexistent")
}

func TestPathLock_ConcurrentWriters(t *testing.T) {
	pl := NewPathLock()
	var counter int32
	var maxConcurrent int32
	var wg sync.WaitGroup

	numWriters := 50
	entered := make(chan struct{}, numWriters)
	release := make(chan struct{})

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pl.Lock("/shared")

			current := atomic.AddInt32(&counter, 1)
			if current > atomic.LoadInt32(&maxConcurrent) {
				atomic.StoreInt32(&maxConcurrent, current)
			}

			entered <- struct{}{}
			<-release
			atomic.AddInt32(&counter, -1)

			pl.Unlock("/shared")
		}()
	}

	for i := 0; i < numWriters; i++ {
		select {
		case <-entered:
			if got := atomic.LoadInt32(&counter); got != 1 {
				close(release)
				t.Fatalf("concurrent writers while writer %d held lock = %d, want 1", i+1, got)
			}
			release <- struct{}{}
		case <-time.After(time.Second):
			close(release)
			t.Fatalf("timed out waiting for writer %d to enter", i+1)
		}
	}
	wg.Wait()

	// Only one writer should have held the lock at any time
	if maxConcurrent != 1 {
		t.Errorf("Max concurrent writers = %d, want 1", maxConcurrent)
	}

	if pl.Size() != 0 {
		t.Errorf("Size() = %d, want 0", pl.Size())
	}
}

func TestPathLock_RapidReacquireDoesNotPermitOverlap(t *testing.T) {
	pl := NewPathLock()
	var concurrent int32
	var overlapped atomic.Bool
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		for i := 0; i < 5000; i++ {
			pl.Lock("/shared")
			if atomic.AddInt32(&concurrent, 1) > 1 {
				overlapped.Store(true)
			}
			runtime.Gosched()
			atomic.AddInt32(&concurrent, -1)
			pl.Unlock("/shared")
			runtime.Gosched()
		}
	}

	wg.Add(2)
	go worker()
	go worker()
	wg.Wait()

	if overlapped.Load() {
		t.Fatal("expected rapid reacquire sequence to preserve exclusive access")
	}
	if pl.Size() != 0 {
		t.Fatalf("Size() = %d, want 0", pl.Size())
	}
}
