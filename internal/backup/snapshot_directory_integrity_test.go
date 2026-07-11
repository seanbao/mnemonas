package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/seanbao/mnemonas/internal/config"
)

func TestManager_RunRestoreRejectsSnapshotDirectoryTopologyTampering(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(t *testing.T, dataPath string)
	}{
		{
			name: "injected empty directory",
			tamper: func(t *testing.T, dataPath string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(dataPath, "injected-empty"), 0o700); err != nil {
					t.Fatalf("Mkdir(injected empty directory) error: %v", err)
				}
			},
		},
		{
			name: "removed original empty directory",
			tamper: func(t *testing.T, dataPath string) {
				t.Helper()
				if err := os.Remove(filepath.Join(dataPath, "original-empty")); err != nil {
					t.Fatalf("Remove(original empty directory) error: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, run, restoreTarget := newSnapshotDirectoryTamperFixture(t)
			tt.tamper(t, filepath.Join(run.SnapshotPath, "data"))

			_, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: restoreTarget})
			if !errors.Is(err, ErrUnsafePath) {
				t.Errorf("RunRestore() error = %v, want ErrUnsafePath", err)
			}
			if _, statErr := os.Lstat(restoreTarget); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("restore target stat error = %v, want not exist", statErr)
			}
		})
	}
}

func newSnapshotDirectoryTamperFixture(t *testing.T) (*Manager, *RunResult, string) {
	t.Helper()
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "restore")
	if err := os.Mkdir(filepath.Join(source, "original-empty"), 0o700); err != nil {
		t.Fatalf("Mkdir(original empty directory) error: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	return manager, run, filepath.Join(tmpDir, "restore-target")
}
