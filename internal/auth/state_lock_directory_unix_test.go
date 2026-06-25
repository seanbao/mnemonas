//go:build unix

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireStateLockRejectsLocallyWritableDirectory(t *testing.T) {
	for _, mode := range []os.FileMode{0o770, 0o777} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := filepath.Join(privateAuthStateTestDir(t), "auth")
			if err := os.Mkdir(dir, mode); err != nil {
				t.Fatalf("Mkdir(auth) error: %v", err)
			}
			if err := os.Chmod(dir, mode); err != nil {
				t.Fatalf("Chmod(auth) error: %v", err)
			}

			lock, err := AcquireStateLock(filepath.Join(dir, "users.json"))
			if lock != nil {
				_ = lock.Close()
				t.Fatal("AcquireStateLock() returned a lock in a locally writable directory")
			}
			if !errors.Is(err, ErrAuthStateLockUnsafeDirectory) {
				t.Fatalf("AcquireStateLock() error = %v, want ErrAuthStateLockUnsafeDirectory", err)
			}
			if _, err := os.Lstat(filepath.Join(dir, authStateLockFileName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected lock acquisition created a lock file: %v", err)
			}
		})
	}
}

func TestAcquireStateLockAllowsNonWritableSharedDirectory(t *testing.T) {
	dir := filepath.Join(privateAuthStateTestDir(t), "auth")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("Mkdir(auth) error: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("Chmod(auth) error: %v", err)
	}

	lock, err := AcquireStateLock(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("AcquireStateLock() error: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
}

func TestAcquireStateLockRejectsReplaceableWritableAncestor(t *testing.T) {
	parent := privateAuthStateTestDir(t)
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatalf("Chmod(parent) error: %v", err)
	}
	authDir := filepath.Join(parent, "auth")
	if err := os.Mkdir(authDir, 0o700); err != nil {
		t.Fatalf("Mkdir(auth) error: %v", err)
	}

	lock, err := AcquireStateLock(filepath.Join(authDir, "users.json"))
	if lock != nil {
		_ = lock.Close()
		t.Fatal("AcquireStateLock() returned a lock below a replaceable writable ancestor")
	}
	if !errors.Is(err, ErrAuthStateLockUnsafeAncestor) {
		t.Fatalf("AcquireStateLock() error = %v, want ErrAuthStateLockUnsafeAncestor", err)
	}
	if _, err := os.Lstat(filepath.Join(authDir, authStateLockFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected lock acquisition created a lock file: %v", err)
	}
}

func TestAcquireStateLockAllowsProtectedStickyAncestor(t *testing.T) {
	parent := privateAuthStateTestDir(t)
	if err := os.Chmod(parent, 0o777|os.ModeSticky); err != nil {
		t.Fatalf("Chmod(parent) error: %v", err)
	}
	authDir := filepath.Join(parent, "auth")
	if err := os.Mkdir(authDir, 0o700); err != nil {
		t.Fatalf("Mkdir(auth) error: %v", err)
	}

	lock, err := AcquireStateLock(filepath.Join(authDir, "users.json"))
	if err != nil {
		t.Fatalf("AcquireStateLock() error: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
}

func TestAcquireStateLockReopensDirectoryAfterRejectedAncestorIsReplaced(t *testing.T) {
	parent := privateAuthStateTestDir(t)
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatalf("Chmod(parent unsafe) error: %v", err)
	}
	authDir := filepath.Join(parent, "auth")
	if err := os.Mkdir(authDir, 0o700); err != nil {
		t.Fatalf("Mkdir(auth) error: %v", err)
	}
	usersPath := filepath.Join(authDir, "users.json")
	if lock, err := AcquireStateLock(usersPath); lock != nil || !errors.Is(err, ErrAuthStateLockUnsafeAncestor) {
		if lock != nil {
			_ = lock.Close()
		}
		t.Fatalf("AcquireStateLock(unsafe ancestor) lock=%#v error=%v", lock, err)
	}

	oldAuthDir := filepath.Join(parent, "old-auth")
	if err := os.Rename(authDir, oldAuthDir); err != nil {
		t.Fatalf("Rename(auth) error: %v", err)
	}
	if err := os.Mkdir(authDir, 0o700); err != nil {
		t.Fatalf("Mkdir(replacement auth) error: %v", err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatalf("Chmod(parent safe) error: %v", err)
	}

	lock, err := AcquireStateLock(usersPath)
	if err != nil {
		t.Fatalf("AcquireStateLock(retry) error: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(authDir, authStateLockFileName)); err != nil {
		t.Fatalf("replacement auth directory does not contain lock file: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(oldAuthDir, authStateLockFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected directory unexpectedly contains lock file: %v", err)
	}
}
