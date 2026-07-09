package backup

import (
	"os"
	"testing"
)

func secureBackupTestTempDir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod(test temp dir) error: %v", err)
	}
	return dir
}

func newBackupTestManager(t testing.TB, cfg ManagerConfig) (*Manager, error) {
	t.Helper()
	manager, err := NewManager(cfg)
	if err == nil {
		t.Cleanup(func() {
			if closeErr := manager.Close(); closeErr != nil {
				t.Errorf("Close(test backup manager) error: %v", closeErr)
			}
		})
	}
	return manager, err
}
