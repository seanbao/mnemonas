//go:build windows

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStateLockPreventsWindowsPathReplacement(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "auth")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("Mkdir(auth directory) error: %v", err)
	}
	usersPath := filepath.Join(dir, "users.json")
	stateLock, err := AcquireStateLock(usersPath)
	if err != nil {
		t.Fatalf("AcquireStateLock() error: %v", err)
	}
	if contender, err := AcquireStateLock(usersPath); contender != nil || !errors.Is(err, ErrAuthStateLockHeld) {
		if contender != nil {
			_ = contender.Close()
		}
		_ = stateLock.Close()
		t.Fatalf("AcquireStateLock(contender) lock=%#v error=%v, want ErrAuthStateLockHeld", contender, err)
	}

	if err := os.Rename(stateLock.Path(), stateLock.Path()+".moved"); err == nil {
		_ = stateLock.Close()
		t.Fatal("Rename(lock file) succeeded while state lock was held")
	}
	if err := os.Remove(stateLock.Path()); err == nil {
		_ = stateLock.Close()
		t.Fatal("Remove(lock file) succeeded while state lock was held")
	}
	if err := os.Rename(dir, dir+".moved"); err == nil {
		_ = stateLock.Close()
		t.Fatal("Rename(authentication state directory) succeeded while state lock was held")
	}
	if err := os.Rename(parent, parent+".moved"); err == nil {
		_ = stateLock.Close()
		t.Fatal("Rename(authentication state ancestor) succeeded while state lock was held")
	}

	if err := stateLock.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if err := os.Rename(stateLock.Path(), stateLock.Path()+".moved"); err != nil {
		t.Fatalf("Rename(lock file) after Close error: %v", err)
	}
	movedParent := parent + ".moved"
	if err := os.Rename(parent, movedParent); err != nil {
		t.Fatalf("Rename(authentication state ancestor) after Close error: %v", err)
	}
	if err := os.Rename(movedParent, parent); err != nil {
		t.Fatalf("Restore(authentication state ancestor) error: %v", err)
	}
}
