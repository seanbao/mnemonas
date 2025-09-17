// Package webdav provides WebDAV protocol HTTP handler
package webdav

import (
	"sync"
	"sync/atomic"
)

// PathLock provides per-path locking for concurrent write protection.
// Entries are reference-counted and removed when no holder remains.
type PathLock struct {
	mu    sync.Mutex
	locks map[string]*lockEntry
}

// lockEntry holds a lock with reference count
type lockEntry struct {
	lock     *sync.RWMutex
	refCount int32
}

// NewPathLock creates a new path lock manager
func NewPathLock() *PathLock {
	return &PathLock{
		locks: make(map[string]*lockEntry),
	}
}

// acquire returns or creates a lock for the given path, incrementing ref count
func (pl *PathLock) acquire(path string) *sync.RWMutex {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	entry, ok := pl.locks[path]
	if !ok {
		entry = &lockEntry{
			lock:     &sync.RWMutex{},
			refCount: 0,
		}
		pl.locks[path] = entry
	}
	atomic.AddInt32(&entry.refCount, 1)
	return entry.lock
}

// release decrements ref count and removes entry if zero
func (pl *PathLock) release(path string) {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	if entry, ok := pl.locks[path]; ok {
		if atomic.AddInt32(&entry.refCount, -1) <= 0 {
			delete(pl.locks, path)
		}
	}
}

// RLock acquires a read lock for the given path
func (pl *PathLock) RLock(path string) {
	pl.acquire(path).RLock()
}

// RUnlock releases a read lock for the given path.
func (pl *PathLock) RUnlock(path string) {
	pl.mu.Lock()
	entry, ok := pl.locks[path]
	if !ok {
		pl.mu.Unlock()
		return
	}
	entry.lock.RUnlock()
	newCount := atomic.AddInt32(&entry.refCount, -1)
	if newCount <= 0 {
		delete(pl.locks, path)
	}
	pl.mu.Unlock()
}

// Lock acquires a write lock for the given path
func (pl *PathLock) Lock(path string) {
	pl.acquire(path).Lock()
}

// Unlock releases a write lock for the given path.
func (pl *PathLock) Unlock(path string) {
	pl.mu.Lock()
	entry, ok := pl.locks[path]
	if !ok {
		pl.mu.Unlock()
		return
	}
	entry.lock.Unlock()
	newCount := atomic.AddInt32(&entry.refCount, -1)
	if newCount <= 0 {
		delete(pl.locks, path)
	}
	pl.mu.Unlock()
}

// TryLock attempts to acquire a write lock, returns false if already locked
func (pl *PathLock) TryLock(path string) bool {
	lock := pl.acquire(path)
	if lock.TryLock() {
		return true
	}
	pl.release(path)
	return false
}

// TryRLock attempts to acquire a read lock, returns false if write-locked
func (pl *PathLock) TryRLock(path string) bool {
	lock := pl.acquire(path)
	if lock.TryRLock() {
		return true
	}
	pl.release(path)
	return false
}

// Size returns the number of active locks (for monitoring)
func (pl *PathLock) Size() int {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return len(pl.locks)
}
