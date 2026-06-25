//go:build unix

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStateLockRecoverAdminPasswordRequiresMatchingHeldLock(t *testing.T) {
	dir := privateAuthStateTestDir(t)
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod(auth directory) error: %v", err)
	}
	usersPath, _ := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 3)

	otherDir := privateAuthStateTestDir(t)
	if err := os.Chmod(otherDir, 0o700); err != nil {
		t.Fatalf("Chmod(other auth directory) error: %v", err)
	}
	otherUsersPath := filepath.Join(otherDir, "users.json")

	stateLock, err := AcquireStateLock(usersPath)
	if err != nil {
		t.Fatalf("AcquireStateLock() error: %v", err)
	}
	if _, err := stateLock.RecoverAdminPassword(otherUsersPath, "admin"); !errors.Is(err, ErrAuthStateLockRequired) {
		t.Fatalf("RecoverAdminPassword(other users file) error = %v, want ErrAuthStateLockRequired", err)
	}
	result, err := stateLock.RecoverAdminPassword(usersPath, "admin")
	if err != nil || result == nil {
		t.Fatalf("RecoverAdminPassword() result=%#v error=%v", result, err)
	}
	if err := stateLock.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if _, err := stateLock.RecoverAdminPassword(usersPath, "admin"); !errors.Is(err, ErrAuthStateLockRequired) {
		t.Fatalf("RecoverAdminPassword(closed lock) error = %v, want ErrAuthStateLockRequired", err)
	}
}

func TestCopiedStateLockSharesRecoveryLifecycle(t *testing.T) {
	dir := privateAuthStateTestDir(t)
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod(auth directory) error: %v", err)
	}
	usersPath, _ := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 3)

	stateLock, err := AcquireStateLock(usersPath)
	if err != nil {
		t.Fatalf("AcquireStateLock() error: %v", err)
	}
	copiedLock := *stateLock
	if err := copiedLock.Close(); err != nil {
		t.Fatalf("Close(copied lock) error: %v", err)
	}
	if _, err := stateLock.RecoverAdminPassword(usersPath, "admin"); !errors.Is(err, ErrAuthStateLockRequired) {
		t.Fatalf("RecoverAdminPassword(original after copied close) error = %v, want ErrAuthStateLockRequired", err)
	}

	reacquired, err := AcquireStateLock(usersPath)
	if err != nil {
		t.Fatalf("AcquireStateLock() after copied close error: %v", err)
	}
	if err := reacquired.Close(); err != nil {
		t.Fatalf("Close(reacquired lock) error: %v", err)
	}
}
