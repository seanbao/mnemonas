//go:build !linux && !darwin

package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStorageRootLifecycleLockFailsClosedWhenUnsupported(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatalf("Mkdir(root) error: %v", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot(root) error: %v", err)
	}
	defer root.Close()

	lock, err := acquireStorageRootLifecycleLock(storageRootLifecycleLockSpec{
		label: "files",
		path:  rootPath,
		root:  root,
	})
	if lock != nil || !errors.Is(err, ErrStorageRootLockUnsupported) {
		t.Fatalf("acquire result lock=%#v error=%v, want ErrStorageRootLockUnsupported", lock, err)
	}
}
