//go:build unix

package backup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/seanbao/mnemonas/internal/config"
)

func TestBackupTargetLockRejectsLocallyWritableJobDirectory(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	job := backupTargetLockTestJob(t, tmpDir, filepath.Join(tmpDir, "backups"))
	jobRoot := filepath.Join(job.Destination, job.ID)
	if err := os.MkdirAll(jobRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll(job root) error: %v", err)
	}
	if err := os.Chmod(jobRoot, 0o777); err != nil {
		t.Fatalf("Chmod(job root) error: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: job.Source,
		Jobs:        []config.BackupJobConfig{job},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	lock, err := manager.acquireJobTargetLock(job)
	if lock != nil {
		_ = lock.Close()
		t.Fatal("acquireJobTargetLock() accepted a locally writable job directory")
	}
	if !errors.Is(err, ErrBackupTargetLockUnsafeDirectory) {
		t.Fatalf("acquireJobTargetLock() error = %v, want %v", err, ErrBackupTargetLockUnsafeDirectory)
	}
}

func TestBackupTargetLockRejectsReplaceableWritableAncestor(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	shared := filepath.Join(tmpDir, "shared")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatalf("Mkdir(shared) error: %v", err)
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatalf("Chmod(shared) error: %v", err)
	}
	job := backupTargetLockTestJob(t, tmpDir, filepath.Join(shared, "backups"))

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: job.Source,
		Jobs:        []config.BackupJobConfig{job},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	lock, err := manager.acquireJobTargetLock(job)
	if lock != nil {
		_ = lock.Close()
		t.Fatal("acquireJobTargetLock() accepted a replaceable writable ancestor")
	}
	if !errors.Is(err, ErrBackupTargetLockUnsafeAncestor) {
		t.Fatalf("acquireJobTargetLock() error = %v, want %v", err, ErrBackupTargetLockUnsafeAncestor)
	}
}

func TestBackupTargetLockAllowsProtectedStickyAncestor(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	sticky := filepath.Join(tmpDir, "sticky")
	if err := os.Mkdir(sticky, 0o700); err != nil {
		t.Fatalf("Mkdir(sticky) error: %v", err)
	}
	if err := os.Chmod(sticky, 0o777|os.ModeSticky); err != nil {
		t.Fatalf("Chmod(sticky) error: %v", err)
	}
	job := backupTargetLockTestJob(t, tmpDir, sticky)

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: job.Source,
		Jobs:        []config.BackupJobConfig{job},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	lock, err := manager.acquireJobTargetLock(job)
	if err != nil {
		t.Fatalf("acquireJobTargetLock() error: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
}

func backupTargetLockTestJob(t *testing.T, tmpDir, destination string) config.BackupJobConfig {
	t.Helper()
	source := filepath.Join(tmpDir, "source")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	return config.BackupJobConfig{
		ID:          "home",
		Name:        "Home backup",
		Type:        JobTypeLocal,
		Source:      source,
		Destination: destination,
	}
}
