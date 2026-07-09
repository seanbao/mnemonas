//go:build unix

package backup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestManagerStateLockRejectsLocallyWritableDirectory(t *testing.T) {
	root := filepath.Join(secureBackupTestTempDir(t), "state")
	if err := os.Mkdir(root, 0o777); err != nil {
		t.Fatalf("Mkdir(state) error: %v", err)
	}
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatalf("Chmod(state) error: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{Root: root})
	if manager != nil {
		_ = manager.Close()
		t.Fatal("NewManager() accepted a locally writable state root")
	}
	if !errors.Is(err, ErrBackupStateLockUnsafeDirectory) {
		t.Fatalf("NewManager() error = %v, want %v", err, ErrBackupStateLockUnsafeDirectory)
	}
}

func TestManagerStateLockRejectsReplaceableWritableAncestor(t *testing.T) {
	parent := secureBackupTestTempDir(t)
	unsafeAncestor := filepath.Join(parent, "shared")
	if err := os.Mkdir(unsafeAncestor, 0o777); err != nil {
		t.Fatalf("Mkdir(shared) error: %v", err)
	}
	if err := os.Chmod(unsafeAncestor, 0o777); err != nil {
		t.Fatalf("Chmod(shared) error: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{Root: filepath.Join(unsafeAncestor, "state")})
	if manager != nil {
		_ = manager.Close()
		t.Fatal("NewManager() accepted a replaceable state-root ancestor")
	}
	if !errors.Is(err, ErrBackupStateLockUnsafeAncestor) {
		t.Fatalf("NewManager() error = %v, want %v", err, ErrBackupStateLockUnsafeAncestor)
	}
}

func TestManagerStateLockAllowsProtectedStickyAncestor(t *testing.T) {
	parent := secureBackupTestTempDir(t)
	stickyAncestor := filepath.Join(parent, "sticky")
	if err := os.Mkdir(stickyAncestor, 0o777); err != nil {
		t.Fatalf("Mkdir(sticky) error: %v", err)
	}
	if err := os.Chmod(stickyAncestor, 0o777|os.ModeSticky); err != nil {
		t.Fatalf("Chmod(sticky) error: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{Root: filepath.Join(stickyAncestor, "state")})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
}

func TestManagerStateLockRejectsSpecialFile(t *testing.T) {
	root := filepath.Join(secureBackupTestTempDir(t), "state")
	if err := ensureBackupStateRoot(root); err != nil {
		t.Fatalf("ensureBackupStateRoot() error: %v", err)
	}
	if err := unix.Mkfifo(filepath.Join(root, backupStateLockFileName), 0o600); err != nil {
		t.Fatalf("Mkfifo(lock) error: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{Root: root})
	if manager != nil {
		_ = manager.Close()
		t.Fatal("NewManager() accepted a special-file state lock")
	}
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("NewManager() error = %v, want %v", err, ErrUnsafePath)
	}
}
