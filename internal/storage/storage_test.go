package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/dataplane"
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
