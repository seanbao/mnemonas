//go:build unix

package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManager_RunRestoreRejectsSnapshotDirectoryModeTampering(t *testing.T) {
	manager, run, restoreTarget := newSnapshotDirectoryTamperFixture(t)
	directoryPath := filepath.Join(run.SnapshotPath, "data", "docs")
	assertPathMode(t, directoryPath, 0o700)
	if err := os.Chmod(directoryPath, 0o755); err != nil {
		t.Fatalf("Chmod(snapshot directory) error: %v", err)
	}
	assertPathMode(t, directoryPath, 0o755)

	_, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: restoreTarget})
	if !errors.Is(err, ErrUnsafePath) {
		t.Errorf("RunRestore() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Lstat(restoreTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("restore target stat error = %v, want not exist", statErr)
	}
}
