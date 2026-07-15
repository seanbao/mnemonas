package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func requireFilesystemMutationGatesReleased(t *testing.T, fs *FileSystem) {
	t.Helper()

	if !fs.gcMu.TryLock() {
		t.Fatal("filesystem GC gate remained held after rejected mutation")
	}
	defer fs.gcMu.Unlock()

	if !fs.mu.TryLock() {
		t.Fatal("filesystem mutation lock remained held after rejected mutation")
	}
	fs.mu.Unlock()
}

func TestMutationLeaseStatSkipsVersionPolicyAndContentHash(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	const filePath = "/large-share.bin"
	if err := fs.WriteFile(ctx, filePath, bytes.NewReader([]byte("seed"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	const largeSize = int64(1 << 30)
	if err := os.Truncate(fs.workspace.FullPath(filePath), largeSize); err != nil {
		t.Fatalf("Truncate() error: %v", err)
	}

	hashCalled := false
	fs.hashStatWorkspaceFile = func(context.Context, string) (string, error) {
		hashCalled = true
		return "unexpected", nil
	}
	lease, err := fs.AcquireMutationLease(ctx)
	if err != nil {
		t.Fatalf("AcquireMutationLease() error: %v", err)
	}
	info, err := lease.Stat(ctx, filePath)
	lease.Release()
	if err != nil {
		t.Fatalf("MutationLease.Stat() error: %v", err)
	}
	if hashCalled {
		t.Fatal("MutationLease.Stat() invoked the full Stat content hash hook")
	}
	if info.Size != largeSize || info.IsDir || info.ContentHash != "" || info.Versioned {
		t.Fatalf("MutationLease.Stat() info = %+v", info)
	}
}

func TestAcquireMutationLeaseCancellationWhileWaiting(t *testing.T) {
	fs := setupFileSystem(t)
	first, err := fs.AcquireMutationLease(context.Background())
	if err != nil {
		t.Fatalf("AcquireMutationLease(first) error: %v", err)
	}
	defer first.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	second, err := fs.AcquireMutationLease(ctx)
	if second != nil {
		second.Release()
		t.Fatal("AcquireMutationLease(canceled) returned a lease")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AcquireMutationLease(canceled) error = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("AcquireMutationLease(canceled) returned after %s", elapsed)
	}
}

func TestAcquireMutationLeaseAcceptsNilContext(t *testing.T) {
	fs := setupFileSystem(t)
	lease, err := fs.AcquireMutationLease(nil)
	if err != nil {
		t.Fatalf("AcquireMutationLease(nil) error: %v", err)
	}
	lease.Release()
}

func TestFilesystemMutationCancellationWhileWaitingForGCGate(t *testing.T) {
	fs := setupFileSystem(t)
	fs.gcMu.Lock()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	err := fs.Mkdir(ctx, "/canceled-admission")
	fs.gcMu.Unlock()

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Mkdir(canceled admission) error = %v, want DeadlineExceeded", err)
	}
	if _, err := fs.Stat(context.Background(), "/canceled-admission"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat(canceled admission path) error = %v, want ErrNotFound", err)
	}
}

func TestMutationLeaseStatRejectsUseAfterRelease(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/released.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	lease, err := fs.AcquireMutationLease(ctx)
	if err != nil {
		t.Fatalf("AcquireMutationLease() error: %v", err)
	}
	lease.Release()
	lease.Release()
	if _, err := lease.Stat(ctx, "/released.txt"); !errors.Is(err, ErrMutationLeaseReleased) {
		t.Fatalf("MutationLease.Stat(after release) error = %v, want ErrMutationLeaseReleased", err)
	}
}
