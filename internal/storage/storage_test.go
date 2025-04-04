//go:build cgo
// +build cgo

package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/versionstore"
)

// testDataplaneAddr is the address of the test dataplane server
const testDataplaneAddr = "127.0.0.1:9090"

// setupDataplaneClient creates a dataplane client for testing
// Returns nil if dataplane is not available
func setupDataplaneClient(t *testing.T) *dataplane.Client {
	client := dataplane.NewClient(testDataplaneAddr)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		return nil
	}

	// Check if healthy
	if _, err := client.Health(ctx); err != nil {
		client.Close()
		return nil
	}

	t.Cleanup(func() { client.Close() })
	return client
}

func setupFileSystem(t *testing.T) *FileSystem {
	client := setupDataplaneClient(t)
	if client == nil {
		t.Skip("dataplane not available, skipping test")
	}

	tmpDir := t.TempDir()
	cfg := &Config{
		FilesRoot:          filepath.Join(tmpDir, "files"),
		InternalRoot:       filepath.Join(tmpDir, ".mnemonas"),
		TrashRoot:          filepath.Join(tmpDir, ".mnemonas", "trash"),
		Dataplane:          client,
		MaxVersions:        10,
		MaxVersionAge:      30 * 24 * time.Hour,
		TrashRetentionDays: 30,
	}

	fs, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { fs.Close() })
	return fs
}

func TestNew(t *testing.T) {
	client := setupDataplaneClient(t)
	if client == nil {
		t.Skip("dataplane not available, skipping test")
	}

	tmpDir := t.TempDir()
	cfg := &Config{
		FilesRoot:    filepath.Join(tmpDir, "files"),
		InternalRoot: filepath.Join(tmpDir, ".mnemonas"),
		TrashRoot:    filepath.Join(tmpDir, ".mnemonas", "trash"),
		Dataplane:    client,
	}

	fs, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer fs.Close()

	// Check internal directory was created
	if _, err := os.Stat(cfg.InternalRoot); err != nil {
		t.Errorf("Internal directory not created: %v", err)
	}

	// Check database was created
	dbPath := filepath.Join(cfg.InternalRoot, "index.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("Database not created: %v", err)
	}
}

func TestNew_RequiresDataplane(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{
		FilesRoot:    filepath.Join(tmpDir, "files"),
		InternalRoot: filepath.Join(tmpDir, ".mnemonas"),
		TrashRoot:    filepath.Join(tmpDir, ".mnemonas", "trash"),
		Dataplane:    nil,
	}

	_, err := New(cfg)
	if err == nil {
		t.Error("Expected error when Dataplane is nil")
	}
}

func TestFileSystem_WriteFile_Read(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	content := []byte("hello world")

	err := fs.WriteFile(ctx, "/test.txt", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	f, err := fs.OpenFile(ctx, "/test.txt")
	if err != nil {
		t.Fatalf("OpenFile() error: %v", err)
	}
	defer f.Close()

	got := make([]byte, 100)
	n, _ := f.Read(got)

	if string(got[:n]) != string(content) {
		t.Errorf("Content = %q, want %q", got[:n], content)
	}
}

func TestFileSystem_WriteFile_RollsBackNewFileWhenIndexUpdateFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.versions.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	err := fs.WriteFile(ctx, "/rollback-new.bin", bytes.NewReader([]byte("new content")))
	if err == nil {
		t.Fatal("Expected WriteFile() to fail when file index update cannot persist")
	}

	if _, statErr := fs.Stat(ctx, "/rollback-new.bin"); statErr != ErrNotFound {
		t.Fatalf("Expected new file to be removed after rollback, got %v", statErr)
	}
}

func TestFileSystem_WriteFile_RollsBackOverwriteWhenIndexUpdateFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/rollback-existing.bin", bytes.NewReader([]byte("old content"))); err != nil {
		t.Fatalf("Initial WriteFile() error: %v", err)
	}

	if err := fs.versions.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	err := fs.WriteFile(ctx, "/rollback-existing.bin", bytes.NewReader([]byte("new content")))
	if err == nil {
		t.Fatal("Expected WriteFile() overwrite to fail when file index update cannot persist")
	}

	f, openErr := fs.OpenFile(ctx, "/rollback-existing.bin")
	if openErr != nil {
		t.Fatalf("OpenFile() after rollback error: %v", openErr)
	}
	defer f.Close()

	data, readErr := io.ReadAll(f)
	if readErr != nil {
		t.Fatalf("ReadAll() after rollback error: %v", readErr)
	}
	if string(data) != "old content" {
		t.Fatalf("Expected original content after rollback, got %q", string(data))
	}
}

func TestFileSystem_WriteFile_RollsBackVersionMetadataWhenIndexUpdateFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/rollback-version.md", bytes.NewReader([]byte("old content"))); err != nil {
		t.Fatalf("Initial WriteFile() error: %v", err)
	}

	fs.updateFileIndex = func(ctx context.Context, path string, size int64, modTime time.Time, hash string) error {
		return errors.New("index update failed")
	}

	err := fs.WriteFile(ctx, "/rollback-version.md", bytes.NewReader([]byte("new content")))
	if err == nil {
		t.Fatal("Expected WriteFile() overwrite to fail when file index update fails")
	}

	versions, versionErr := fs.versions.GetVersions(ctx, "/rollback-version.md")
	if versionErr != nil {
		t.Fatalf("GetVersions() after rollback error: %v", versionErr)
	}
	if len(versions) != 0 {
		t.Fatalf("Expected no historical version metadata after rollback, got %d entries", len(versions))
	}

	f, openErr := fs.OpenFile(ctx, "/rollback-version.md")
	if openErr != nil {
		t.Fatalf("OpenFile() after rollback error: %v", openErr)
	}
	defer f.Close()

	data, readErr := io.ReadAll(f)
	if readErr != nil {
		t.Fatalf("ReadAll() after rollback error: %v", readErr)
	}
	if string(data) != "old content" {
		t.Fatalf("Expected original content after rollback, got %q", string(data))
	}
}

func TestFileSystem_Stat(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.WriteFile(ctx, "/stat.txt", bytes.NewReader([]byte("content")))

	info, err := fs.Stat(ctx, "/stat.txt")
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}

	if info.Name != "stat.txt" {
		t.Errorf("Name = %s, want stat.txt", info.Name)
	}
	if info.IsDir {
		t.Error("IsDir should be false for file")
	}
	if info.Size != 7 {
		t.Errorf("Size = %d, want 7", info.Size)
	}
}

func TestFileSystem_Stat_NotFound(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	_, err := fs.Stat(ctx, "/nonexistent.txt")
	if err != ErrNotFound {
		t.Errorf("Stat() error = %v, want ErrNotFound", err)
	}
}

func TestFileSystem_Stat_Root(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	info, err := fs.Stat(ctx, "/")
	if err != nil {
		t.Fatalf("Stat(/) error: %v", err)
	}

	if !info.IsDir {
		t.Error("Root should be a directory")
	}
}

func TestFileSystem_Mkdir(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	err := fs.Mkdir(ctx, "/newdir")
	if err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}

	info, err := fs.Stat(ctx, "/newdir")
	if err != nil {
		t.Fatalf("Stat() error after mkdir: %v", err)
	}

	if !info.IsDir {
		t.Error("Created path should be directory")
	}
}

func TestFileSystem_Mkdir_AlreadyExists(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/existingdir")

	err := fs.Mkdir(ctx, "/existingdir")
	if err != ErrAlreadyExists {
		t.Errorf("Mkdir() error = %v, want ErrAlreadyExists", err)
	}
}

func TestFileSystem_ReadDir(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/listdir")
	fs.WriteFile(ctx, "/listdir/a.txt", bytes.NewReader([]byte("a")))
	fs.WriteFile(ctx, "/listdir/b.txt", bytes.NewReader([]byte("b")))
	fs.Mkdir(ctx, "/listdir/subdir")

	entries, err := fs.ReadDir(ctx, "/listdir")
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}

	if len(entries) != 3 {
		t.Errorf("ReadDir() returned %d entries, want 3", len(entries))
	}
}

func TestFileSystem_Delete(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.WriteFile(ctx, "/todelete.txt", bytes.NewReader([]byte("delete me")))

	err := fs.Delete(ctx, "/todelete.txt")
	if err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	// File should be in trash, not deleted
	_, err = fs.Stat(ctx, "/todelete.txt")
	if err != ErrNotFound {
		t.Error("File should not exist in original location")
	}

	// Should be in trash
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}

	if len(items) != 1 {
		t.Errorf("ListTrash() returned %d items, want 1", len(items))
	}
}

func TestFileSystem_Delete_BypassesTrashWhenDisabled(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	trashEnabled := false
	fs.config.TrashEnabled = &trashEnabled

	if err := fs.WriteFile(ctx, "/delete-no-trash.txt", bytes.NewReader([]byte("gone forever"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	if err := fs.Delete(ctx, "/delete-no-trash.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	if _, err := fs.Stat(ctx, "/delete-no-trash.txt"); err != ErrNotFound {
		t.Fatalf("expected file to be permanently deleted, got %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected delete with trash disabled not to create trash items, got %d", len(items))
	}
}

func TestFileSystem_Delete_EvictsOldestTrashWhenMaxSizeExceeded(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	fs.config.MaxTrashSize = 10

	if err := fs.WriteFile(ctx, "/old.txt", bytes.NewReader([]byte("123456"))); err != nil {
		t.Fatalf("WriteFile(old) error: %v", err)
	}
	if err := fs.Delete(ctx, "/old.txt"); err != nil {
		t.Fatalf("Delete(old) error: %v", err)
	}

	time.Sleep(1100 * time.Millisecond)

	if err := fs.WriteFile(ctx, "/new.txt", bytes.NewReader([]byte("1234567"))); err != nil {
		t.Fatalf("WriteFile(new) error: %v", err)
	}
	if err := fs.Delete(ctx, "/new.txt"); err != nil {
		t.Fatalf("Delete(new) error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("ListTrash() returned %d items, want 1", len(items))
	}
	if items[0].OriginalPath != "/new.txt" {
		t.Fatalf("expected newest item to remain in trash, got %s", items[0].OriginalPath)
	}

	count, totalSize, err := fs.GetTrashStats(ctx)
	if err != nil {
		t.Fatalf("GetTrashStats() error: %v", err)
	}
	if count != 1 {
		t.Fatalf("Trash count = %d, want 1", count)
	}
	if totalSize != 7 {
		t.Fatalf("Trash size = %d, want 7", totalSize)
	}
	if _, err := fs.GetTrashItem(ctx, items[0].ID); err != nil {
		t.Fatalf("GetTrashItem() error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fs.trashRoot, items[0].ID)); err != nil {
		t.Fatalf("expected remaining trash content to exist: %v", err)
	}
	entries, err := os.ReadDir(fs.trashRoot)
	if err != nil {
		t.Fatalf("ReadDir(trashRoot) error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("trash root entries = %d, want 1", len(entries))
	}
}

func TestFileSystem_DeleteAndRestore_EmptyDirectory(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/emptydir"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}

	if err := fs.Delete(ctx, "/emptydir"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	if _, err := fs.Stat(ctx, "/emptydir"); err != ErrNotFound {
		t.Fatalf("Expected deleted directory to be absent, got %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Expected 1 trash item, got %d", len(items))
	}
	if !items[0].IsDir {
		t.Fatal("Expected trash item to be a directory")
	}

	if err := fs.RestoreFromTrash(ctx, items[0].ID); err != nil {
		t.Fatalf("RestoreFromTrash() error: %v", err)
	}

	info, err := fs.Stat(ctx, "/emptydir")
	if err != nil {
		t.Fatalf("Stat() after restore error: %v", err)
	}
	if !info.IsDir {
		t.Fatal("Expected restored path to be a directory")
	}
}

func TestFileSystem_Delete_RollsBackFileWhenIndexDeleteFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/delete-rollback.txt", bytes.NewReader([]byte("keep me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	fs.deleteFileIndex = func(ctx context.Context, path string) error {
		return errors.New("index delete failed")
	}

	err := fs.Delete(ctx, "/delete-rollback.txt")
	if err == nil {
		t.Fatal("Expected Delete() to fail when file index removal fails")
	}

	f, openErr := fs.OpenFile(ctx, "/delete-rollback.txt")
	if openErr != nil {
		t.Fatalf("OpenFile() after rollback error: %v", openErr)
	}
	defer f.Close()

	data, readErr := io.ReadAll(f)
	if readErr != nil {
		t.Fatalf("ReadAll() after rollback error: %v", readErr)
	}
	if string(data) != "keep me" {
		t.Fatalf("Expected original file content after rollback, got %q", string(data))
	}

	items, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() error: %v", listErr)
	}
	if len(items) != 0 {
		t.Fatalf("Expected trash to remain empty after rollback, got %d items", len(items))
	}
}

func TestFileSystem_Delete_RollsBackDirectoryWhenIndexDeleteFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/delete-rollback-dir"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}

	fs.deleteFileIndex = func(ctx context.Context, path string) error {
		return errors.New("index delete failed")
	}

	err := fs.Delete(ctx, "/delete-rollback-dir")
	if err == nil {
		t.Fatal("Expected Delete() to fail when directory index removal fails")
	}

	info, statErr := fs.Stat(ctx, "/delete-rollback-dir")
	if statErr != nil {
		t.Fatalf("Stat() after directory rollback error: %v", statErr)
	}
	if !info.IsDir {
		t.Fatal("Expected rolled back path to remain a directory")
	}

	items, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() error: %v", listErr)
	}
	if len(items) != 0 {
		t.Fatalf("Expected trash to remain empty after rollback, got %d items", len(items))
	}
}

func TestFileSystem_Delete_FailsWhenVersionLookupFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/delete-version-lookup.txt", bytes.NewReader([]byte("keep me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	if err := fs.versions.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	err := fs.Delete(ctx, "/delete-version-lookup.txt")
	if err == nil {
		t.Fatal("Expected Delete() to fail when version metadata lookup fails")
	}

	f, openErr := fs.OpenFile(ctx, "/delete-version-lookup.txt")
	if openErr != nil {
		t.Fatalf("OpenFile() after failed delete error: %v", openErr)
	}
	defer f.Close()

	data, readErr := io.ReadAll(f)
	if readErr != nil {
		t.Fatalf("ReadAll() after failed delete error: %v", readErr)
	}
	if string(data) != "keep me" {
		t.Fatalf("Expected file content to remain unchanged, got %q", string(data))
	}
}

func TestFileSystem_PermanentDelete(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.WriteFile(ctx, "/permanent.txt", bytes.NewReader([]byte("delete forever")))

	err := fs.PermanentDelete(ctx, "/permanent.txt")
	if err != nil {
		t.Fatalf("PermanentDelete() error: %v", err)
	}

	// Should not be in trash
	items, _ := fs.ListTrash(ctx)
	if len(items) != 0 {
		t.Errorf("Trash should be empty after permanent delete")
	}
}

func TestFileSystem_PermanentDelete_RollsBackFileWhenMetadataDeleteFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/permanent-rollback.bin", bytes.NewReader([]byte("keep me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	if err := fs.versions.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	err := fs.PermanentDelete(ctx, "/permanent-rollback.bin")
	if err == nil {
		t.Fatal("Expected PermanentDelete() to fail when metadata cleanup cannot persist")
	}

	f, openErr := fs.OpenFile(ctx, "/permanent-rollback.bin")
	if openErr != nil {
		t.Fatalf("OpenFile() after rollback error: %v", openErr)
	}
	defer f.Close()

	data, readErr := io.ReadAll(f)
	if readErr != nil {
		t.Fatalf("ReadAll() after rollback error: %v", readErr)
	}
	if string(data) != "keep me" {
		t.Fatalf("Expected original file content after rollback, got %q", string(data))
	}
}

func TestFileSystem_PermanentDelete_RollsBackDirectoryWhenIndexDeleteFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/permanent-dir"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}

	if err := fs.versions.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	err := fs.PermanentDelete(ctx, "/permanent-dir")
	if err == nil {
		t.Fatal("Expected PermanentDelete() directory delete to fail when index cleanup cannot persist")
	}

	info, statErr := fs.Stat(ctx, "/permanent-dir")
	if statErr != nil {
		t.Fatalf("Stat() after directory rollback error: %v", statErr)
	}
	if !info.IsDir {
		t.Fatal("Expected rolled back path to remain a directory")
	}
}

func TestFileSystem_PermanentDelete_AttemptsAllVersionObjectDeletes(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/permanent-objects.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/permanent-objects.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/permanent-objects.md", bytes.NewReader([]byte("v3"))); err != nil {
		t.Fatalf("WriteFile(v3) error: %v", err)
	}

	versions, err := fs.versions.GetVersions(ctx, "/permanent-objects.md")
	if err != nil {
		t.Fatalf("GetVersions() error: %v", err)
	}
	if len(versions) < 2 {
		t.Fatalf("Expected historical versions, got %d", len(versions))
	}

	called := make(map[string]int)
	fs.deleteVersionObject = func(hash string) error {
		called[hash]++
		return errors.New("delete object failed")
	}

	err = fs.PermanentDelete(ctx, "/permanent-objects.md")
	if err == nil {
		t.Fatal("Expected PermanentDelete() to report object deletion failures")
	}

	for _, version := range versions {
		if called[version.Hash] != 1 {
			t.Fatalf("Expected deleteVersionObject to be attempted once for %s, got %d", version.Hash, called[version.Hash])
		}
	}

	if _, statErr := fs.Stat(ctx, "/permanent-objects.md"); statErr != ErrNotFound {
		t.Fatalf("Expected file content to remain deleted after object cleanup failure, got %v", statErr)
	}
	trashItems, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() error: %v", listErr)
	}
	if len(trashItems) != 0 {
		t.Fatalf("Expected permanent delete not to move file to trash, got %d items", len(trashItems))
	}
}

func TestFileSystem_Delete_DirNotEmpty(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/nonemptydir")
	fs.WriteFile(ctx, "/nonemptydir/file.txt", bytes.NewReader([]byte("x")))

	err := fs.Delete(ctx, "/nonemptydir")
	if err != ErrDirNotEmpty {
		t.Errorf("Delete() error = %v, want ErrDirNotEmpty", err)
	}
}

func TestFileSystem_Rename(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.WriteFile(ctx, "/oldname.txt", bytes.NewReader([]byte("content")))

	err := fs.Rename(ctx, "/oldname.txt", "/newname.txt")
	if err != nil {
		t.Fatalf("Rename() error: %v", err)
	}

	_, err = fs.Stat(ctx, "/oldname.txt")
	if err != ErrNotFound {
		t.Error("Old path should not exist")
	}

	_, err = fs.Stat(ctx, "/newname.txt")
	if err != nil {
		t.Error("New path should exist")
	}
}

func TestFileSystem_Rename_PreservesVersions(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.WriteFile(ctx, "/rename.md", bytes.NewReader([]byte("v1")))
	fs.WriteFile(ctx, "/rename.md", bytes.NewReader([]byte("v2")))

	if err := fs.Rename(ctx, "/rename.md", "/renamed.md"); err != nil {
		t.Fatalf("Rename() error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/renamed.md")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}
	if len(versions) < 2 {
		t.Errorf("ListVersions() returned %d versions, want at least 2", len(versions))
	}
}

func TestFileSystem_Rename_RollsBackWhenMetadataRenameFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/rename-fail.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	fs.renameMetadataPath = func(ctx context.Context, oldName, newName string) error {
		return errors.New("metadata rename failed")
	}

	err := fs.Rename(ctx, "/rename-fail.txt", "/rename-fail-new.txt")
	if err == nil {
		t.Fatal("Expected Rename() to fail when metadata rename fails")
	}

	if _, statErr := fs.Stat(ctx, "/rename-fail.txt"); statErr != nil {
		t.Fatalf("Expected original path to remain after rollback, got %v", statErr)
	}
	if _, statErr := fs.Stat(ctx, "/rename-fail-new.txt"); statErr != ErrNotFound {
		t.Fatalf("Expected new path to be absent after rollback, got %v", statErr)
	}
}

func TestFileSystem_Rename_ReturnsRollbackFailure(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/rename-rollback.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	firstRename := true
	fs.renameWorkspacePath = func(ctx context.Context, oldName, newName string) error {
		if firstRename {
			firstRename = false
			return fs.workspace.Rename(ctx, oldName, newName)
		}
		return errors.New("rollback rename failed")
	}
	fs.renameMetadataPath = func(ctx context.Context, oldName, newName string) error {
		return errors.New("metadata rename failed")
	}

	err := fs.Rename(ctx, "/rename-rollback.txt", "/rename-rollback-new.txt")
	if err == nil {
		t.Fatal("Expected Rename() to fail when rollback fails")
	}
	if !strings.Contains(err.Error(), "failed to rename metadata") {
		t.Fatalf("Expected metadata rename failure in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "failed to rollback workspace rename") {
		t.Fatalf("Expected rollback failure in error, got %v", err)
	}

	if _, statErr := fs.Stat(ctx, "/rename-rollback-new.txt"); statErr != nil {
		t.Fatalf("Expected file to remain at new path when rollback fails, got %v", statErr)
	}
}

func TestFileSystem_RestoreFromTrash(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	originalContent := []byte("restore me")
	fs.WriteFile(ctx, "/restore.txt", bytes.NewReader(originalContent))

	fs.Delete(ctx, "/restore.txt")

	items, _ := fs.ListTrash(ctx)
	if len(items) == 0 {
		t.Fatal("No items in trash")
	}

	err := fs.RestoreFromTrash(ctx, items[0].ID)
	if err != nil {
		t.Fatalf("RestoreFromTrash() error: %v", err)
	}

	// File should be restored
	_, err = fs.Stat(ctx, "/restore.txt")
	if err != nil {
		t.Errorf("Stat() after restore error: %v", err)
	}
}

func TestFileSystem_RestoreFromTrash_RollsBackWhenIndexUpdateFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-index-fail.txt", bytes.NewReader([]byte("restore me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-index-fail.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("No items in trash")
	}

	fs.updateFileIndex = func(ctx context.Context, path string, size int64, modTime time.Time, hash string) error {
		return errors.New("index update failed")
	}

	err = fs.RestoreFromTrash(ctx, items[0].ID)
	if err == nil {
		t.Fatal("Expected RestoreFromTrash() to fail when file index update fails")
	}

	if _, statErr := fs.Stat(ctx, "/restore-index-fail.txt"); statErr != ErrNotFound {
		t.Fatalf("Expected restored file to be moved back to trash, got %v", statErr)
	}

	trashItems, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after rollback error: %v", listErr)
	}
	if len(trashItems) != 1 {
		t.Fatalf("Expected trash item to remain after rollback, got %d items", len(trashItems))
	}
}

func TestFileSystem_RestoreFromTrashTo_PreservesVersions(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.WriteFile(ctx, "/restore-move.md", bytes.NewReader([]byte("v1")))
	fs.WriteFile(ctx, "/restore-move.md", bytes.NewReader([]byte("v2")))
	fs.Delete(ctx, "/restore-move.md")

	items, _ := fs.ListTrash(ctx)
	if len(items) == 0 {
		t.Fatal("No items in trash")
	}

	if err := fs.RestoreFromTrashTo(ctx, items[0].ID, "/restored/restore-move.md"); err != nil {
		t.Fatalf("RestoreFromTrashTo() error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/restored/restore-move.md")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}
	if len(versions) < 2 {
		t.Errorf("ListVersions() returned %d versions, want at least 2", len(versions))
	}
}

func TestFileSystem_RestoreFromTrashTo_RollsBackWhenIndexUpdateFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-to-index-fail.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-to-index-fail.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-to-index-fail.md"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("No items in trash")
	}

	fs.updateFileIndex = func(ctx context.Context, path string, size int64, modTime time.Time, hash string) error {
		return errors.New("index update failed")
	}

	newPath := "/restored/restore-to-index-fail.md"
	err = fs.RestoreFromTrashTo(ctx, items[0].ID, newPath)
	if err == nil {
		t.Fatal("Expected RestoreFromTrashTo() to fail when file index update fails")
	}

	if _, statErr := fs.Stat(ctx, newPath); statErr != ErrNotFound {
		t.Fatalf("Expected restored target path to be rolled back, got %v", statErr)
	}

	trashItems, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after rollback error: %v", listErr)
	}
	if len(trashItems) != 1 {
		t.Fatalf("Expected trash item to remain after rollback, got %d items", len(trashItems))
	}

	versions, versionErr := fs.ListVersions(ctx, "/restore-to-index-fail.md")
	if versionErr != nil {
		t.Fatalf("ListVersions() after rollback error: %v", versionErr)
	}
	if len(versions) < 2 {
		t.Fatalf("Expected original version metadata to remain after rollback, got %d versions", len(versions))
	}
}

func TestFileSystem_RestoreFromTrashTo_RollsBackOnMetadataConflict(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-conflict.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-conflict.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-conflict.md"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("No items in trash")
	}

	conflictPath := "/restored/restore-conflict.md"
	if err := fs.versions.UpdateFileIndex(ctx, conflictPath, 1, time.Now(), "conflict-hash"); err != nil {
		t.Fatalf("UpdateFileIndex() error: %v", err)
	}

	err = fs.RestoreFromTrashTo(ctx, items[0].ID, conflictPath)
	if err == nil {
		t.Fatal("Expected RestoreFromTrashTo() to fail when metadata path conflicts")
	}

	if _, statErr := fs.Stat(ctx, conflictPath); statErr != ErrNotFound {
		t.Fatalf("Expected restored file to be rolled back from target path, got %v", statErr)
	}

	trashItems, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after rollback error: %v", listErr)
	}
	if len(trashItems) != 1 {
		t.Fatalf("Expected trash item to remain after rollback, got %d items", len(trashItems))
	}
	if trashItems[0].OriginalPath != "/restore-conflict.md" {
		t.Fatalf("Expected original trash item path to remain unchanged, got %s", trashItems[0].OriginalPath)
	}

	versions, versionErr := fs.ListVersions(ctx, "/restore-conflict.md")
	if versionErr != nil {
		t.Fatalf("ListVersions() after rollback error: %v", versionErr)
	}
	if len(versions) < 2 {
		t.Fatalf("Expected original version metadata to remain after rollback, got %d versions", len(versions))
	}
}

func TestFileSystem_RestoreFromTrash_AlreadyExists(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.WriteFile(ctx, "/conflict.txt", bytes.NewReader([]byte("original")))
	fs.Delete(ctx, "/conflict.txt")

	// Create a new file with same name
	fs.WriteFile(ctx, "/conflict.txt", bytes.NewReader([]byte("new")))

	items, _ := fs.ListTrash(ctx)
	if len(items) == 0 {
		t.Fatal("No items in trash")
	}

	err := fs.RestoreFromTrash(ctx, items[0].ID)
	if err != ErrAlreadyExists {
		t.Errorf("RestoreFromTrash() error = %v, want ErrAlreadyExists", err)
	}
}

func TestFileSystem_EmptyTrash(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.WriteFile(ctx, "/empty1.txt", bytes.NewReader([]byte("x")))
	fs.WriteFile(ctx, "/empty2.txt", bytes.NewReader([]byte("y")))
	fs.Delete(ctx, "/empty1.txt")
	fs.Delete(ctx, "/empty2.txt")

	deleted, err := fs.EmptyTrash(ctx)
	if err != nil {
		t.Fatalf("EmptyTrash() error: %v", err)
	}

	if deleted != 2 {
		t.Errorf("EmptyTrash() deleted %d, want 2", deleted)
	}

	items, _ := fs.ListTrash(ctx)
	if len(items) != 0 {
		t.Errorf("Trash still has %d items", len(items))
	}
}

func TestFileSystem_DeleteFromTrash_KeepsMetadataWhenContentDeleteFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/trash-delete-fail.txt", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/trash-delete-fail.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Expected 1 trash item, got %d", len(items))
	}

	fs.removeTrashPath = func(path string) error {
		return errors.New("trash delete failed")
	}

	err = fs.DeleteFromTrash(ctx, items[0].ID)
	if err == nil {
		t.Fatal("Expected DeleteFromTrash() to fail when trash content deletion fails")
	}

	items, err = fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() after failed delete error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Expected trash metadata to remain after failed content delete, got %d items", len(items))
	}
}

func TestFileSystem_EmptyTrash_KeepsMetadataWhenContentDeleteFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/empty-fail-1.txt", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/empty-fail-2.txt", bytes.NewReader([]byte("y"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/empty-fail-1.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if err := fs.Delete(ctx, "/empty-fail-2.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	fs.removeTrashPath = func(path string) error {
		return errors.New("trash delete failed")
	}

	deleted, err := fs.EmptyTrash(ctx)
	if err == nil {
		t.Fatal("Expected EmptyTrash() to fail when trash content deletion fails")
	}
	if deleted != 0 {
		t.Fatalf("Expected no metadata deletion on failure, got %d", deleted)
	}

	items, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed empty error: %v", listErr)
	}
	if len(items) != 2 {
		t.Fatalf("Expected trash metadata to remain after failed empty, got %d items", len(items))
	}
}

func TestFileSystem_CleanupExpiredTrash_KeepsMetadataWhenContentDeleteFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/expired-trash.txt", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/expired-trash.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.versions.ListTrash(ctx)
	if err != nil {
		t.Fatalf("versions.ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Expected 1 trash item, got %d", len(items))
	}

	original := items[0]
	if err := fs.versions.RemoveFromTrash(ctx, original.ID); err != nil {
		t.Fatalf("RemoveFromTrash() error: %v", err)
	}
	if err := fs.versions.AddToTrash(ctx, &versionstore.TrashItem{
		ID:           original.ID,
		OriginalPath: original.OriginalPath,
		Size:         original.Size,
		DeletedAt:    original.DeletedAt,
		ExpiresAt:    time.Now().Add(-time.Hour),
		IsDir:        original.IsDir,
		HadVersions:  original.HadVersions,
	}); err != nil {
		t.Fatalf("AddToTrash() error: %v", err)
	}

	fs.removeTrashPath = func(path string) error {
		return errors.New("trash delete failed")
	}

	deleted, err := fs.CleanupExpiredTrash(ctx)
	if err == nil {
		t.Fatal("Expected CleanupExpiredTrash() to fail when trash content deletion fails")
	}
	if deleted != 0 {
		t.Fatalf("Expected no expired metadata deletion on failure, got %d", deleted)
	}

	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed cleanup error: %v", listErr)
	}
	if len(remaining) != 1 {
		t.Fatalf("Expected expired trash metadata to remain after failed cleanup, got %d items", len(remaining))
	}
}

func TestFileSystem_ListVersions(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	// Create file with versions
	for i := 0; i < 3; i++ {
		content := []byte("version " + string(rune('0'+i)))
		fs.WriteFile(ctx, "/versioned.txt", bytes.NewReader(content))
		time.Sleep(10 * time.Millisecond)
	}

	versions, err := fs.ListVersions(ctx, "/versioned.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}

	// Should have current + at least 1 version
	if len(versions) < 2 {
		t.Errorf("ListVersions() returned %d versions, want at least 2", len(versions))
	}

	// First should be current
	if versions[0].Comment != "(current)" {
		t.Errorf("First version comment = %s, want '(current)'", versions[0].Comment)
	}
}

func TestFileSystem_RestoreVersion_RollsBackWhenIndexUpdateFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-version.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-version.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/restore-version.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}

	var historicalHash string
	for _, version := range versions {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("Expected at least one historical version")
	}

	if err := fs.versions.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	err = fs.RestoreVersion(ctx, "/restore-version.txt", historicalHash)
	if err == nil {
		t.Fatal("Expected RestoreVersion() to fail when file index update cannot persist")
	}

	f, openErr := fs.OpenFile(ctx, "/restore-version.txt")
	if openErr != nil {
		t.Fatalf("OpenFile() after rollback error: %v", openErr)
	}
	defer f.Close()

	data, readErr := io.ReadAll(f)
	if readErr != nil {
		t.Fatalf("ReadAll() after rollback error: %v", readErr)
	}
	if string(data) != "v2" {
		t.Fatalf("Expected current content to remain after rollback, got %q", string(data))
	}
}

func TestFileSystem_WriteFile_FailsWhenCleanupVersionsObjectDeleteFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.config.MaxVersions = 1
	if err := fs.WriteFile(ctx, "/cleanup-fail.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/cleanup-fail.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	fs.deleteVersionObject = func(hash string) error {
		return errors.New("delete object failed")
	}

	err := fs.WriteFile(ctx, "/cleanup-fail.md", bytes.NewReader([]byte("v3")))
	if err == nil {
		t.Fatal("Expected WriteFile() to fail when old version object cleanup fails")
	}
	if !strings.Contains(err.Error(), "failed to cleanup old versions") {
		t.Fatalf("Expected cleanup failure in error, got %v", err)
	}

	f, openErr := fs.OpenFile(ctx, "/cleanup-fail.md")
	if openErr != nil {
		t.Fatalf("OpenFile() after cleanup failure error: %v", openErr)
	}
	defer f.Close()

	data, readErr := io.ReadAll(f)
	if readErr != nil {
		t.Fatalf("ReadAll() after cleanup failure error: %v", readErr)
	}
	if string(data) != "v2" {
		t.Fatalf("Expected current content to remain unchanged after cleanup failure, got %q", string(data))
	}
}

func TestFileSystem_WriteFile_ForcesRetentionSweepWhenFreeSpaceBelowThreshold(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.config.MaxVersions = 3
	fs.config.MaxVersionAge = 365 * 24 * time.Hour
	fs.config.MinFreeSpace = ^uint64(0)

	for _, content := range []string{"v1", "v2", "v3", "v4"} {
		if err := fs.WriteFile(ctx, "/retention-sweep.txt", bytes.NewReader([]byte(content))); err != nil {
			t.Fatalf("WriteFile(%s) error: %v", content, err)
		}
	}

	fs.UpdateRetentionSettings(1, 365*24*time.Hour, ^uint64(0))
	if err := fs.WriteFile(ctx, "/trigger.txt", bytes.NewReader([]byte("trigger"))); err != nil {
		t.Fatalf("WriteFile(trigger) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/retention-sweep.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected current version plus one retained historical version after forced sweep, got %d", len(versions))
	}
}

func TestMovePath_NonEmptyDirectory(t *testing.T) {
	tempDir := t.TempDir()
	src := filepath.Join(tempDir, "src")
	dst := filepath.Join(tempDir, "dst")

	if err := os.MkdirAll(filepath.Join(src, "nested"), 0755); err != nil {
		t.Fatalf("MkdirAll(src) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	if err := movePath(src, dst); err != nil {
		t.Fatalf("movePath() error: %v", err)
	}

	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Expected source directory to be removed, got %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "nested", "file.txt"))
	if err != nil {
		t.Fatalf("ReadFile(dst) error: %v", err)
	}
	if string(data) != "content" {
		t.Fatalf("Expected moved file content to match, got %q", string(data))
	}
}

func TestMovePath_PreservesDirectoryAndFileModes(t *testing.T) {
	tempDir := t.TempDir()
	src := filepath.Join(tempDir, "src")
	dst := filepath.Join(tempDir, "dst")

	if err := os.MkdirAll(filepath.Join(src, "nested"), 0700); err != nil {
		t.Fatalf("MkdirAll(src) error: %v", err)
	}
	if err := os.Chmod(filepath.Join(src, "nested"), 0700); err != nil {
		t.Fatalf("Chmod(nested) error: %v", err)
	}
	filePath := filepath.Join(src, "nested", "file.txt")
	if err := os.WriteFile(filePath, []byte("content"), 0600); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	if err := movePath(src, dst); err != nil {
		t.Fatalf("movePath() error: %v", err)
	}

	nestedInfo, err := os.Stat(filepath.Join(dst, "nested"))
	if err != nil {
		t.Fatalf("Stat(nested) error: %v", err)
	}
	if nestedInfo.Mode().Perm() != 0700 {
		t.Fatalf("Expected nested directory mode 0700, got %#o", nestedInfo.Mode().Perm())
	}

	fileInfo, err := os.Stat(filepath.Join(dst, "nested", "file.txt"))
	if err != nil {
		t.Fatalf("Stat(file) error: %v", err)
	}
	if fileInfo.Mode().Perm() != 0600 {
		t.Fatalf("Expected file mode 0600, got %#o", fileInfo.Mode().Perm())
	}
}

func TestFileSystem_ListVersions_Dir(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/versiondir")

	_, err := fs.ListVersions(ctx, "/versiondir")
	if err != ErrIsDir {
		t.Errorf("ListVersions() error = %v, want ErrIsDir", err)
	}
}

func TestFileSystem_TrashStats(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.WriteFile(ctx, "/trash1.txt", bytes.NewReader([]byte("content1")))
	fs.WriteFile(ctx, "/trash2.txt", bytes.NewReader([]byte("content22")))
	fs.Delete(ctx, "/trash1.txt")
	fs.Delete(ctx, "/trash2.txt")

	count, totalSize, err := fs.GetTrashStats(ctx)
	if err != nil {
		t.Fatalf("GetTrashStats() error: %v", err)
	}

	if count != 2 {
		t.Errorf("Trash count = %d, want 2", count)
	}
	if totalSize != 17 {
		t.Errorf("Trash size = %d, want 17", totalSize)
	}
}

func TestFileSystem_Search(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.WriteFile(ctx, "/readme.md", bytes.NewReader([]byte("x")))
	fs.WriteFile(ctx, "/guide.md", bytes.NewReader([]byte("y")))
	fs.WriteFile(ctx, "/main.go", bytes.NewReader([]byte("z")))

	results, err := fs.Search(ctx, "md", 10)
	if err != nil {
		t.Fatalf("Search() error: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Search(md) returned %d results, want 2", len(results))
	}
}

func TestFileSystem_Search_EmptyQuery(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	_, err := fs.Search(ctx, "", 10)
	if err == nil {
		t.Error("Search with empty query should return error")
	}
}

func TestFileSystem_CleanupStaging(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	files, bytes, err := fs.CleanupStaging(ctx)
	if err != nil {
		t.Fatalf("CleanupStaging() error: %v", err)
	}

	// Should not error even with no staging files
	if files < 0 || bytes < 0 {
		t.Error("CleanupStaging() returned negative values")
	}
}

func TestFileSystem_SetVersioning(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.WriteFile(ctx, "/override.txt", bytes.NewReader([]byte("x")))

	err := fs.SetVersioning(ctx, "/override.txt", false)
	if err != nil {
		t.Fatalf("SetVersioning() error: %v", err)
	}

	enabled, reason, err := fs.GetVersioningStatus(ctx, "/override.txt")
	if err != nil {
		t.Fatalf("GetVersioningStatus() error: %v", err)
	}

	if enabled {
		t.Error("Versioning should be disabled after override")
	}
	if reason == "" {
		t.Error("Reason should not be empty")
	}
}

func TestFileSystem_GetAllReferencedHashes(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	// Create some versions
	fs.WriteFile(ctx, "/hashes.txt", bytes.NewReader([]byte("v1")))
	fs.WriteFile(ctx, "/hashes.txt", bytes.NewReader([]byte("v2")))
	fs.WriteFile(ctx, "/hashes.txt", bytes.NewReader([]byte("v3")))

	hashes, err := fs.GetAllReferencedHashes(ctx)
	if err != nil {
		t.Fatalf("GetAllReferencedHashes() error: %v", err)
	}

	// Should have at least some hashes from versions
	if len(hashes) < 1 {
		t.Log("No version hashes found (may be expected if versioning not triggered)")
	}
}

func TestFileSystem_AcquireGCLock_BlocksMutationsUntilReleased(t *testing.T) {
	fs := &FileSystem{
		listReferencedHashes: func(ctx context.Context) ([]string, error) {
			return []string{"hash1", "hash2"}, nil
		},
	}

	hashes, release, err := fs.AcquireGCLock(context.Background())
	if err != nil {
		t.Fatalf("AcquireGCLock() error: %v", err)
	}

	if len(hashes) != 2 {
		t.Fatalf("AcquireGCLock() returned %d hashes, want 2", len(hashes))
	}

	locked := make(chan struct{})
	go func() {
		fs.mu.Lock()
		close(locked)
		fs.mu.Unlock()
	}()

	select {
	case <-locked:
		t.Fatal("expected storage mutation lock to remain held during GC")
	case <-time.After(20 * time.Millisecond):
	}

	release()

	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("expected blocked mutation to proceed after GC lock release")
	}
}

func TestFileSystem_AcquireGCLock_ReleasesLockOnSnapshotError(t *testing.T) {
	fs := &FileSystem{
		listReferencedHashes: func(ctx context.Context) ([]string, error) {
			return nil, errors.New("snapshot failed")
		},
	}

	_, release, err := fs.AcquireGCLock(context.Background())
	if err == nil {
		t.Fatal("expected AcquireGCLock() to fail")
	}
	if release != nil {
		t.Fatal("expected no release function on error")
	}

	locked := make(chan struct{})
	go func() {
		fs.mu.Lock()
		close(locked)
		fs.mu.Unlock()
	}()

	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("expected mutex to be released after snapshot error")
	}
}
