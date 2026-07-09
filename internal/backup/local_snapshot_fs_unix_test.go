//go:build unix

package backup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSyncOpenedBackupSnapshotDirectoryTreeStaysOnHeldInode(t *testing.T) {
	parent := secureBackupTestTempDir(t)
	snapshotPath := filepath.Join(parent, "snapshot.partial")
	mustWriteFile(t, filepath.Join(snapshotPath, "nested", "note.txt"), "backup")
	dir, err := os.Open(snapshotPath)
	if err != nil {
		t.Fatalf("Open(snapshot) error: %v", err)
	}
	defer dir.Close()

	movedPath := snapshotPath + ".moved"
	if err := os.Rename(snapshotPath, movedPath); err != nil {
		t.Fatalf("Rename(snapshot) error: %v", err)
	}
	if err := os.Mkdir(snapshotPath, 0o700); err != nil {
		t.Fatalf("Mkdir(replacement) error: %v", err)
	}
	if err := unix.Mkfifo(filepath.Join(snapshotPath, "replacement.fifo"), 0o600); err != nil {
		t.Fatalf("Mkfifo(replacement) error: %v", err)
	}

	if err := syncOpenedBackupSnapshotDirectoryTree(dir, snapshotPath); err != nil {
		t.Fatalf("syncOpenedBackupSnapshotDirectoryTree() error: %v", err)
	}
	assertFileContent(t, filepath.Join(movedPath, "nested", "note.txt"), "backup")
}

func TestSyncBackupSnapshotDirectoryTreeRejectsSpecialFileWithoutBlocking(t *testing.T) {
	snapshotPath := filepath.Join(secureBackupTestTempDir(t), "snapshot.partial")
	if err := os.Mkdir(snapshotPath, 0o700); err != nil {
		t.Fatalf("Mkdir(snapshot) error: %v", err)
	}
	if err := unix.Mkfifo(filepath.Join(snapshotPath, "unexpected.fifo"), 0o600); err != nil {
		t.Fatalf("Mkfifo() error: %v", err)
	}

	err := syncBackupSnapshotDirectoryTree(snapshotPath)
	if !errors.Is(err, ErrUnsupportedFileType) {
		t.Fatalf("syncBackupSnapshotDirectoryTree() error = %v, want %v", err, ErrUnsupportedFileType)
	}
}

func TestSyncBackupSnapshotDirectoryTreeRejectsSymlink(t *testing.T) {
	snapshotPath := filepath.Join(secureBackupTestTempDir(t), "snapshot.partial")
	if err := os.Mkdir(snapshotPath, 0o700); err != nil {
		t.Fatalf("Mkdir(snapshot) error: %v", err)
	}
	if err := os.Symlink("outside", filepath.Join(snapshotPath, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	err := syncBackupSnapshotDirectoryTree(snapshotPath)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("syncBackupSnapshotDirectoryTree() error = %v, want %v", err, ErrUnsafePath)
	}
}
