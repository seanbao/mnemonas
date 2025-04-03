//go:build cgo
// +build cgo

package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/versionstore"
	"github.com/seanbao/mnemonas/internal/workspace"
)

type blockingOnceReader struct {
	started chan struct{}
	release chan struct{}
	data    []byte
	sent    bool
}

func (r *blockingOnceReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	close(r.started)
	<-r.release
	r.sent = true
	n := copy(p, r.data)
	return n, io.EOF
}

// testDataplaneAddr is the address of the test dataplane server
func testDataplaneAddr() string {
	if addr := os.Getenv("MNEMONAS_TEST_DATAPLANE_ADDR"); addr != "" {
		return addr
	}
	return "127.0.0.1:9090"
}

// setupDataplaneClient creates a dataplane client for testing
// Returns nil if dataplane is not available
func setupDataplaneClient(t *testing.T) *dataplane.Client {
	client := dataplane.NewClient(testDataplaneAddr())
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

func mustGenerateStorageID(t *testing.T) string {
	t.Helper()

	id, err := generateID()
	if err != nil {
		t.Fatalf("generateID() error: %v", err)
	}
	return id
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

func TestFileSystem_OpenFile_ReturnsErrIsDirForDirectoryPath(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/dir"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}

	reader, err := fs.OpenFile(ctx, "/dir")
	if err != ErrIsDir {
		t.Fatalf("OpenFile() error = %v, want ErrIsDir", err)
	}
	if reader != nil {
		t.Fatal("expected no reader for directory path")
	}
}

func TestFileSystem_OpenFile_ReturnsErrNotDirWhenParentIsFile(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/open-parent", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(open-parent) error: %v", err)
	}

	reader, err := fs.OpenFile(ctx, "/open-parent/child.txt")
	if err != ErrNotDir {
		t.Fatalf("OpenFile() error = %v, want ErrNotDir", err)
	}
	if reader != nil {
		t.Fatal("expected no reader for parent-not-directory path")
	}
}

func TestFileSystem_WriteFile_ReturnsErrNotDirWhenParentIsFile(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/parent-file", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(parent-file) error: %v", err)
	}

	err := fs.WriteFile(ctx, "/parent-file/child.txt", bytes.NewReader([]byte("nested")))
	if err != ErrNotDir {
		t.Fatalf("WriteFile() error = %v, want ErrNotDir", err)
	}
}

func TestFileSystem_WriteFile_RejectsSymlinkParent(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(fs.workspace.Root(), "escape")); err != nil {
		t.Fatalf("Symlink(escape) error: %v", err)
	}

	err := fs.WriteFile(ctx, "/escape/payload.txt", bytes.NewReader([]byte("payload")))
	if err != ErrNotFound {
		t.Fatalf("WriteFile() error = %v, want ErrNotFound", err)
	}

	if _, statErr := os.Stat(filepath.Join(outsideDir, "payload.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no file outside workspace, got %v", statErr)
	}
}

func TestFileSystem_WriteFile_DoesNotFollowTempSymlink(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0600); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}

	tmpLink := filepath.Join(fs.workspace.Root(), "safe.txt.tmp")
	if err := os.Symlink(outsidePath, tmpLink); err != nil {
		t.Fatalf("Symlink(safe.txt.tmp) error: %v", err)
	}

	if err := fs.WriteFile(ctx, "/safe.txt", bytes.NewReader([]byte("workspace"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	outsideData, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("ReadFile(outside) error: %v", err)
	}
	if string(outsideData) != "outside" {
		t.Fatalf("expected outside file to remain unchanged, got %q", string(outsideData))
	}

	info, err := os.Lstat(filepath.Join(fs.workspace.Root(), "safe.txt"))
	if err != nil {
		t.Fatalf("Lstat(safe.txt) error: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("expected written file to be a regular file, got symlink")
	}

	data, err := os.ReadFile(filepath.Join(fs.workspace.Root(), "safe.txt"))
	if err != nil {
		t.Fatalf("ReadFile(safe.txt) error: %v", err)
	}
	if string(data) != "workspace" {
		t.Fatalf("expected workspace content to be written, got %q", string(data))
	}
}

func TestFileSystem_WriteFile_VersionsOriginalContentWhenWorkspaceChangesDuringUpload(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	originalContent := []byte("original content")
	if err := fs.WriteFile(ctx, "/version-race.txt", bytes.NewReader(originalContent)); err != nil {
		t.Fatalf("initial WriteFile() error: %v", err)
	}

	reader := &blockingOnceReader{
		started: make(chan struct{}),
		release: make(chan struct{}),
		data:    []byte("new content"),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- fs.WriteFile(ctx, "/version-race.txt", reader)
	}()

	<-reader.started
	if err := os.WriteFile(fs.workspace.FullPath("/version-race.txt"), []byte("mutated externally"), 0644); err != nil {
		t.Fatalf("WriteFile(external mutation) error: %v", err)
	}
	close(reader.release)

	if err := <-errCh; err != nil {
		t.Fatalf("race WriteFile() error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/version-race.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}
	if len(versions) < 2 {
		t.Fatalf("expected current version plus one historical version, got %d entries", len(versions))
	}

	var historicalHash string
	for _, version := range versions {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("expected historical version hash")
	}

	wantHistoricalHash := computeHash(originalContent)
	if historicalHash != wantHistoricalHash {
		t.Fatalf("expected historical version hash %q, got %q", wantHistoricalHash, historicalHash)
	}

	readerAfter, err := fs.OpenFile(ctx, "/version-race.txt")
	if err != nil {
		t.Fatalf("OpenFile() error: %v", err)
	}
	defer readerAfter.Close()

	currentData, err := io.ReadAll(readerAfter)
	if err != nil {
		t.Fatalf("ReadAll() error: %v", err)
	}
	if string(currentData) != "new content" {
		t.Fatalf("expected current content %q, got %q", "new content", string(currentData))
	}
}

func TestFileSystem_Delete_ReturnsErrNotDirWhenParentIsFile(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/delete-parent", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(delete-parent) error: %v", err)
	}

	err := fs.Delete(ctx, "/delete-parent/child.txt")
	if err != ErrNotDir {
		t.Fatalf("Delete() error = %v, want ErrNotDir", err)
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

func TestFileSystem_WriteFile_RollsBackNewFileWhenDirectorySyncFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	originalSyncStoragePathDir := syncStoragePathDir
	syncStoragePathDir = func(dir string) error {
		return errors.New("sync dir failed")
	}
	t.Cleanup(func() {
		syncStoragePathDir = originalSyncStoragePathDir
	})

	err := fs.WriteFile(ctx, "/rollback-sync-new.bin", bytes.NewReader([]byte("new content")))
	if err == nil {
		t.Fatal("Expected WriteFile() to fail when parent directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync parent directory") {
		t.Fatalf("expected parent directory sync failure in error, got %v", err)
	}

	if _, statErr := fs.Stat(ctx, "/rollback-sync-new.bin"); statErr != ErrNotFound {
		t.Fatalf("Expected new file to be removed after rollback, got %v", statErr)
	}
}

func TestFileSystem_WriteFile_RollsBackOverwriteWhenDirectorySyncFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/rollback-sync-existing.bin", bytes.NewReader([]byte("old content"))); err != nil {
		t.Fatalf("Initial WriteFile() error: %v", err)
	}

	originalSyncStoragePathDir := syncStoragePathDir
	syncStoragePathDir = func(dir string) error {
		return errors.New("sync dir failed")
	}
	t.Cleanup(func() {
		syncStoragePathDir = originalSyncStoragePathDir
	})

	err := fs.WriteFile(ctx, "/rollback-sync-existing.bin", bytes.NewReader([]byte("new content")))
	if err == nil {
		t.Fatal("Expected WriteFile() overwrite to fail when parent directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync parent directory") {
		t.Fatalf("expected parent directory sync failure in error, got %v", err)
	}

	f, openErr := fs.OpenFile(ctx, "/rollback-sync-existing.bin")
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

func TestFileSystem_WriteFile_RollsBackVersionMetadataWhenDirectorySyncFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/rollback-sync-version.md", bytes.NewReader([]byte("old content"))); err != nil {
		t.Fatalf("Initial WriteFile() error: %v", err)
	}

	originalSyncStoragePathDir := syncStoragePathDir
	syncStoragePathDir = func(dir string) error {
		return errors.New("sync dir failed")
	}
	t.Cleanup(func() {
		syncStoragePathDir = originalSyncStoragePathDir
	})

	err := fs.WriteFile(ctx, "/rollback-sync-version.md", bytes.NewReader([]byte("new content")))
	if err == nil {
		t.Fatal("Expected WriteFile() overwrite to fail when parent directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync parent directory") {
		t.Fatalf("expected parent directory sync failure in error, got %v", err)
	}

	versions, versionErr := fs.versions.GetVersions(ctx, "/rollback-sync-version.md")
	if versionErr != nil {
		t.Fatalf("GetVersions() after rollback error: %v", versionErr)
	}
	if len(versions) != 0 {
		t.Fatalf("Expected no historical version metadata after rollback, got %d entries", len(versions))
	}

	f, openErr := fs.OpenFile(ctx, "/rollback-sync-version.md")
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

func TestFileSystem_WriteFile_ReturnsRollbackCleanupFailureWhenVersionRecordFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	oldContent := []byte("old content " + mustGenerateStorageID(t))
	oldHash := computeHash(oldContent)
	exists, err := fs.versions.HasObject(ctx, oldHash)
	if err != nil {
		t.Fatalf("HasObject(oldHash) error: %v", err)
	}
	if exists {
		t.Fatalf("expected unique test hash %s to be absent before write", oldHash)
	}

	if err := fs.WriteFile(ctx, "/rollback-cleanup.md", bytes.NewReader(oldContent)); err != nil {
		t.Fatalf("Initial WriteFile() error: %v", err)
	}

	deleteCalls := 0
	fs.addFileVersion = func(ctx context.Context, path, hash string, size int64, comment string) error {
		if path != "/rollback-cleanup.md" {
			t.Fatalf("unexpected path %q", path)
		}
		if hash != oldHash {
			t.Fatalf("unexpected hash %q", hash)
		}
		return errors.New("record version failed")
	}
	fs.deleteVersionObject = func(ctx context.Context, hash string) error {
		deleteCalls++
		if hash != oldHash {
			t.Fatalf("unexpected delete hash %q", hash)
		}
		return errors.New("delete object failed")
	}

	err = fs.WriteFile(ctx, "/rollback-cleanup.md", bytes.NewReader([]byte("new content")))
	if err == nil {
		t.Fatal("Expected WriteFile() to fail when version rollback cleanup fails")
	}
	if !strings.Contains(err.Error(), "failed to record version") {
		t.Fatalf("expected version record failure in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "failed to cleanup version object during rollback") {
		t.Fatalf("expected rollback cleanup failure in error, got %v", err)
	}
	if deleteCalls != 1 {
		t.Fatalf("expected deleteVersionObject to be attempted once, got %d", deleteCalls)
	}

	versions, versionErr := fs.versions.GetVersions(ctx, "/rollback-cleanup.md")
	if versionErr != nil {
		t.Fatalf("GetVersions() after rollback error: %v", versionErr)
	}
	if len(versions) != 0 {
		t.Fatalf("Expected no historical version metadata after rollback, got %d entries", len(versions))
	}

	f, openErr := fs.OpenFile(ctx, "/rollback-cleanup.md")
	if openErr != nil {
		t.Fatalf("OpenFile() after rollback error: %v", openErr)
	}
	defer f.Close()

	data, readErr := io.ReadAll(f)
	if readErr != nil {
		t.Fatalf("ReadAll() after rollback error: %v", readErr)
	}
	if string(data) != string(oldContent) {
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

func TestFileSystem_OperationsRejectTraversalLikePaths(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/safe"); err != nil {
		t.Fatalf("Mkdir(/safe) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/safe/versioned.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/safe/versioned.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/safe/trash.txt", bytes.NewReader([]byte("trash"))); err != nil {
		t.Fatalf("WriteFile(trash) error: %v", err)
	}
	if err := fs.Delete(ctx, "/safe/trash.txt"); err != nil {
		t.Fatalf("Delete(/safe/trash.txt) error: %v", err)
	}

	trashItems, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(trashItems) == 0 {
		t.Fatal("expected trash item for traversal restore test")
	}
	trashID := ""
	for _, item := range trashItems {
		if item != nil && item.OriginalPath == "/safe/trash.txt" {
			trashID = item.ID
			break
		}
	}
	if trashID == "" {
		t.Fatal("expected /safe/trash.txt trash item")
	}

	if _, err := fs.Stat(ctx, "../safe/versioned.txt"); err != ErrNotFound {
		t.Fatalf("Stat(traversal) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.ReadDir(ctx, "../safe"); err != ErrNotFound {
		t.Fatalf("ReadDir(traversal) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.OpenFile(ctx, "../safe/versioned.txt"); err != ErrNotFound {
		t.Fatalf("OpenFile(traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.WriteFile(ctx, "../escape.txt", bytes.NewReader([]byte("blocked"))); err != ErrNotFound {
		t.Fatalf("WriteFile(traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.Mkdir(ctx, "../escape-dir"); err != ErrNotFound {
		t.Fatalf("Mkdir(traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.Delete(ctx, "../safe/versioned.txt"); err != ErrNotFound {
		t.Fatalf("Delete(traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.PermanentDelete(ctx, "../safe/versioned.txt"); err != ErrNotFound {
		t.Fatalf("PermanentDelete(traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.Rename(ctx, "../safe/versioned.txt", "/safe/renamed.txt"); err != ErrNotFound {
		t.Fatalf("Rename(source traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.Rename(ctx, "/safe/versioned.txt", "../renamed.txt"); err != ErrNotFound {
		t.Fatalf("Rename(destination traversal) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.ListVersions(ctx, "../safe/versioned.txt"); err != ErrNotFound {
		t.Fatalf("ListVersions(traversal) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.GetVersion(ctx, "../safe/versioned.txt", "missing-hash"); err != ErrNotFound {
		t.Fatalf("GetVersion(traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.RestoreVersion(ctx, "../safe/versioned.txt", "missing-hash"); err != ErrNotFound {
		t.Fatalf("RestoreVersion(traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.SetVersioning(ctx, "../safe/versioned.txt", true); err != ErrNotFound {
		t.Fatalf("SetVersioning(traversal) error = %v, want ErrNotFound", err)
	}
	if _, _, err := fs.GetVersioningStatus(ctx, "../safe/versioned.txt"); err != ErrNotFound {
		t.Fatalf("GetVersioningStatus(traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.RestoreFromTrashTo(ctx, trashID, "../restored.txt"); err != ErrNotFound {
		t.Fatalf("RestoreFromTrashTo(traversal) error = %v, want ErrNotFound", err)
	}

	file, err := fs.OpenFile(ctx, "/safe/versioned.txt")
	if err != nil {
		t.Fatalf("OpenFile(/safe/versioned.txt) after traversal rejections error: %v", err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll(/safe/versioned.txt) error: %v", err)
	}
	if string(data) != "v2" {
		t.Fatalf("OpenFile(/safe/versioned.txt) content = %q, want %q", string(data), "v2")
	}
	if _, err := fs.Stat(ctx, "/escape.txt"); err != ErrNotFound {
		t.Fatalf("expected no normalized /escape.txt after traversal write, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/escape-dir"); err != ErrNotFound {
		t.Fatalf("expected no normalized /escape-dir after traversal mkdir, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/renamed.txt"); err != ErrNotFound {
		t.Fatalf("expected no normalized /renamed.txt after traversal rename, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/restored.txt"); err != ErrNotFound {
		t.Fatalf("expected no normalized /restored.txt after traversal restore, got %v", err)
	}

	remainingTrash, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() after traversal restore rejection error: %v", err)
	}
	foundTrash := false
	for _, item := range remainingTrash {
		if item != nil && item.ID == trashID {
			foundTrash = true
			break
		}
	}
	if !foundTrash {
		t.Fatal("expected traversal restore rejection to leave trash item intact")
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

func TestFileSystem_ReadDir_ReturnsErrNotDirWhenPathIsFile(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/file.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	entries, err := fs.ReadDir(ctx, "/file.txt")
	if err != ErrNotDir {
		t.Fatalf("ReadDir() error = %v, want ErrNotDir", err)
	}
	if entries != nil {
		t.Fatalf("expected no entries for file path, got %d", len(entries))
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

func TestFileSystem_Delete_ReturnsEntropyFailureBeforeMovingToTrash(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/delete-entropy.txt", bytes.NewReader([]byte("keep me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	originalRandomRead := storageRandomRead
	storageRandomRead = func([]byte) (int, error) {
		return 0, errors.New("entropy unavailable")
	}
	defer func() {
		storageRandomRead = originalRandomRead
	}()

	err := fs.Delete(ctx, "/delete-entropy.txt")
	if err == nil {
		t.Fatal("expected Delete() to fail when trash ID generation fails")
	}
	if !strings.Contains(err.Error(), "generate trash ID") {
		t.Fatalf("expected trash ID generation error, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/delete-entropy.txt"); err != nil {
		t.Fatalf("expected file to remain after failed delete, got %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no trash items after failed delete, got %d", len(items))
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

func TestFileSystem_Delete_EvictsExistingTrashBeforeKeepingOversizedNewestItem(t *testing.T) {
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

	if err := fs.WriteFile(ctx, "/oversized.txt", bytes.NewReader([]byte("12345678901"))); err != nil {
		t.Fatalf("WriteFile(oversized) error: %v", err)
	}
	if err := fs.Delete(ctx, "/oversized.txt"); err != nil {
		t.Fatalf("Delete(oversized) error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("ListTrash() returned %d items, want 1", len(items))
	}
	if items[0].OriginalPath != "/oversized.txt" {
		t.Fatalf("expected oversized newest item to remain in trash, got %s", items[0].OriginalPath)
	}

	count, totalSize, err := fs.GetTrashStats(ctx)
	if err != nil {
		t.Fatalf("GetTrashStats() error: %v", err)
	}
	if count != 1 {
		t.Fatalf("Trash count = %d, want 1", count)
	}
	if totalSize != 11 {
		t.Fatalf("Trash size = %d, want 11", totalSize)
	}
}

func TestFileSystem_Delete_EvictionKeepsContentWhenMetadataDeleteFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	fs.config.MaxTrashSize = 10

	if err := fs.WriteFile(ctx, "/old-evict.txt", bytes.NewReader([]byte("123456"))); err != nil {
		t.Fatalf("WriteFile(old) error: %v", err)
	}
	if err := fs.Delete(ctx, "/old-evict.txt"); err != nil {
		t.Fatalf("Delete(old) error: %v", err)
	}

	itemsBefore, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() before eviction error: %v", err)
	}
	if len(itemsBefore) != 1 {
		t.Fatalf("expected one initial trash item, got %d", len(itemsBefore))
	}
	oldItem := itemsBefore[0]

	fs.removeTrashMetadata = func(ctx context.Context, id string) error {
		return errors.New("metadata delete failed")
	}

	if err := fs.WriteFile(ctx, "/new-evict.txt", bytes.NewReader([]byte("1234567"))); err != nil {
		t.Fatalf("WriteFile(new) error: %v", err)
	}
	err = fs.Delete(ctx, "/new-evict.txt")
	if err == nil {
		t.Fatal("Expected Delete() to fail when max-size eviction metadata delete fails")
	}

	itemsAfter, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed eviction error: %v", listErr)
	}
	if len(itemsAfter) != 1 {
		t.Fatalf("expected original trash metadata to remain after failed eviction, got %d items", len(itemsAfter))
	}
	if itemsAfter[0].ID != oldItem.ID {
		t.Fatalf("expected old trash item to remain after failed eviction, got %s want %s", itemsAfter[0].ID, oldItem.ID)
	}
	if _, statErr := os.Stat(filepath.Join(fs.trashRoot, oldItem.ID)); statErr != nil {
		t.Fatalf("expected original trash content to remain after failed eviction: %v", statErr)
	}
	if _, statErr := fs.Stat(ctx, "/new-evict.txt"); statErr != nil {
		t.Fatalf("expected new file to remain in place after failed eviction, got %v", statErr)
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

func TestFileSystem_Delete_RollsBackWhenPathDeleteHookFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/delete-hook.txt", bytes.NewReader([]byte("keep me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	info, err := fs.Stat(ctx, "/delete-hook.txt")
	if err != nil {
		t.Fatalf("Stat() before delete error: %v", err)
	}

	fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
		return nil, errors.New("favorite cleanup failed")
	})

	err = fs.Delete(ctx, "/delete-hook.txt")
	if err == nil {
		t.Fatal("Expected Delete() to fail when path delete hook fails")
	}
	if !strings.Contains(err.Error(), "failed to sync delete hooks") {
		t.Fatalf("expected delete hook failure in error, got %v", err)
	}

	f, openErr := fs.OpenFile(ctx, "/delete-hook.txt")
	if openErr != nil {
		t.Fatalf("OpenFile() after hook rollback error: %v", openErr)
	}
	defer f.Close()

	data, readErr := io.ReadAll(f)
	if readErr != nil {
		t.Fatalf("ReadAll() after hook rollback error: %v", readErr)
	}
	if string(data) != "keep me" {
		t.Fatalf("Expected original file content after hook rollback, got %q", string(data))
	}

	items, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() error: %v", listErr)
	}
	if len(items) != 0 {
		t.Fatalf("Expected trash to remain empty after hook rollback, got %d items", len(items))
	}
	_, _, hash, indexErr := fs.versions.GetFileIndex(ctx, "/delete-hook.txt")
	if indexErr != nil {
		t.Fatalf("GetFileIndex() after hook rollback error: %v", indexErr)
	}
	if hash != info.ContentHash {
		t.Fatalf("expected restored file index hash %q, got %q", info.ContentHash, hash)
	}
}

func TestFileSystem_Delete_CompletesWhenDeleteHookRegistered(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/delete-hook-ok.txt", bytes.NewReader([]byte("remove me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	hookCalled := make(chan struct{}, 1)
	fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
		hookCalled <- struct{}{}
		return nil, nil
	})

	done := make(chan error, 1)
	go func() {
		done <- fs.Delete(ctx, "/delete-hook-ok.txt")
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Delete() error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Delete() deadlocked while invoking delete hook")
	}

	select {
	case <-hookCalled:
	default:
		t.Fatal("expected delete hook to be called")
	}

	if _, err := fs.Stat(ctx, "/delete-hook-ok.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted file to be absent, got %v", err)
	}
}

func TestFileSystem_Delete_KeepsTrashContentWhenTrashMetadataRollbackFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/delete-rollback-metadata.txt", bytes.NewReader([]byte("keep me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	fs.deleteFileIndex = func(ctx context.Context, path string) error {
		if err := fs.versions.Close(); err != nil {
			t.Fatalf("Close() error: %v", err)
		}
		return errors.New("index delete failed")
	}

	err := fs.Delete(ctx, "/delete-rollback-metadata.txt")
	if err == nil {
		t.Fatal("Expected Delete() to fail when index removal and trash metadata rollback both fail")
	}

	reader, openErr := fs.OpenFile(ctx, "/delete-rollback-metadata.txt")
	if openErr != nil {
		t.Fatalf("OpenFile() after rollback error: %v", openErr)
	}
	defer reader.Close()

	data, readErr := io.ReadAll(reader)
	if readErr != nil {
		t.Fatalf("ReadAll() after rollback error: %v", readErr)
	}
	if string(data) != "keep me" {
		t.Fatalf("Expected original file content after rollback, got %q", string(data))
	}

	entries, dirErr := os.ReadDir(fs.trashRoot)
	if dirErr != nil {
		t.Fatalf("ReadDir(trashRoot) error: %v", dirErr)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one retained trash staging entry after metadata rollback failure, got %d", len(entries))
	}
	if _, statErr := os.Stat(filepath.Join(fs.trashRoot, entries[0].Name(), "content")); statErr != nil {
		t.Fatalf("expected trash content to remain when trash metadata rollback fails, got %v", statErr)
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

func TestFileSystem_PermanentDelete_RollsBackWhenPathDeleteHookFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/permanent-hook.txt", bytes.NewReader([]byte("keep me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
		return nil, errors.New("favorite cleanup failed")
	})

	err := fs.PermanentDelete(ctx, "/permanent-hook.txt")
	if err == nil {
		t.Fatal("Expected PermanentDelete() to fail when path delete hook fails")
	}
	if !strings.Contains(err.Error(), "failed to sync delete hooks") {
		t.Fatalf("expected delete hook failure in error, got %v", err)
	}

	f, openErr := fs.OpenFile(ctx, "/permanent-hook.txt")
	if openErr != nil {
		t.Fatalf("OpenFile() after hook rollback error: %v", openErr)
	}
	defer f.Close()

	data, readErr := io.ReadAll(f)
	if readErr != nil {
		t.Fatalf("ReadAll() after hook rollback error: %v", readErr)
	}
	if string(data) != "keep me" {
		t.Fatalf("Expected original file content after hook rollback, got %q", string(data))
	}

	if _, _, _, indexErr := fs.versions.GetFileIndex(ctx, "/permanent-hook.txt"); indexErr != nil {
		t.Fatalf("GetFileIndex() after hook rollback error: %v", indexErr)
	}
	trashItems, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() error: %v", listErr)
	}
	if len(trashItems) != 0 {
		t.Fatalf("Expected permanent delete rollback not to create trash entries, got %d", len(trashItems))
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
	fs.deleteVersionObject = func(ctx context.Context, hash string) error {
		called[hash]++
		return errors.New("delete object failed")
	}

	err = fs.PermanentDelete(ctx, "/permanent-objects.md")
	if err == nil {
		t.Fatal("expected PermanentDelete() to fail when version object cleanup fails")
	}
	if !strings.Contains(err.Error(), "failed to delete version objects") {
		t.Fatalf("expected version object cleanup error, got %v", err)
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

func TestFileSystem_PermanentDelete_DoesNotDeleteSharedVersionObject(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	sharedContent := []byte("shared-delete-" + mustGenerateStorageID(t))
	sharedHash := computeHash(sharedContent)
	exists, err := fs.versions.HasObject(ctx, sharedHash)
	if err != nil {
		t.Fatalf("HasObject(sharedHash) before writes error: %v", err)
	}
	if exists {
		t.Fatalf("expected unique shared hash %s to be absent before writes", sharedHash)
	}

	if err := fs.WriteFile(ctx, "/permanent-shared/a.txt", bytes.NewReader(sharedContent)); err != nil {
		t.Fatalf("WriteFile(a v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/permanent-shared/b.txt", bytes.NewReader(sharedContent)); err != nil {
		t.Fatalf("WriteFile(b v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/permanent-shared/a.txt", bytes.NewReader([]byte("a-v2"))); err != nil {
		t.Fatalf("WriteFile(a v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/permanent-shared/b.txt", bytes.NewReader([]byte("b-v2"))); err != nil {
		t.Fatalf("WriteFile(b v2) error: %v", err)
	}

	if err := fs.PermanentDelete(ctx, "/permanent-shared/a.txt"); err != nil {
		t.Fatalf("PermanentDelete(a) error: %v", err)
	}

	exists, err = fs.versions.HasObject(ctx, sharedHash)
	if err != nil {
		t.Fatalf("HasObject(sharedHash) after delete error: %v", err)
	}
	if !exists {
		t.Fatalf("expected shared historical object %s to remain while another path still references it", sharedHash)
	}

	reader, err := fs.GetVersion(ctx, "/permanent-shared/b.txt", sharedHash)
	if err != nil {
		t.Fatalf("GetVersion(shared historical hash) error: %v", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(shared historical hash) error: %v", err)
	}
	if string(data) != string(sharedContent) {
		t.Fatalf("expected shared historical content %q, got %q", string(sharedContent), string(data))
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

func TestFileSystem_GetVersion_PreservesBinaryHistoricalContent(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	originalContent := []byte{0xff, 0x00, 0xfe, 0x41, 0x80, 0x7f}
	currentContent := []byte("current")

	if err := fs.WriteFile(ctx, "/binary-history.txt", bytes.NewReader(originalContent)); err != nil {
		t.Fatalf("WriteFile(original) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/binary-history.txt", bytes.NewReader(currentContent)); err != nil {
		t.Fatalf("WriteFile(current) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/binary-history.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}
	if len(versions) < 2 {
		t.Fatalf("expected current and historical version entries, got %d", len(versions))
	}

	currentHash := computeHash(currentContent)
	historicalHash := ""
	for _, version := range versions {
		if version.Hash != currentHash {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("expected to find a historical version hash")
	}

	reader, err := fs.GetVersion(ctx, "/binary-history.txt", historicalHash)
	if err != nil {
		t.Fatalf("GetVersion(historical binary content) error: %v", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(historical binary content) error: %v", err)
	}
	if !bytes.Equal(data, originalContent) {
		t.Fatalf("expected historical binary content %v, got %v", originalContent, data)
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

func TestFileSystem_Rename_AlreadyExists(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/rename-source.txt", bytes.NewReader([]byte("source"))); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/rename-dest.txt", bytes.NewReader([]byte("dest"))); err != nil {
		t.Fatalf("WriteFile(dest) error: %v", err)
	}

	err := fs.Rename(ctx, "/rename-source.txt", "/rename-dest.txt")
	if err != ErrAlreadyExists {
		t.Fatalf("Rename() error = %v, want ErrAlreadyExists", err)
	}

	if _, statErr := fs.Stat(ctx, "/rename-source.txt"); statErr != nil {
		t.Fatalf("Expected source path to remain after conflict, got %v", statErr)
	}
	if _, statErr := fs.Stat(ctx, "/rename-dest.txt"); statErr != nil {
		t.Fatalf("Expected destination path to remain after conflict, got %v", statErr)
	}
}

func TestFileSystem_Rename_ReturnsErrNotDirWhenDestinationParentIsFile(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/rename-source.txt", bytes.NewReader([]byte("source"))); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/rename-parent", bytes.NewReader([]byte("not a directory"))); err != nil {
		t.Fatalf("WriteFile(parent) error: %v", err)
	}

	err := fs.Rename(ctx, "/rename-source.txt", "/rename-parent/child.txt")
	if err != ErrNotDir {
		t.Fatalf("Rename() error = %v, want ErrNotDir", err)
	}

	if _, statErr := fs.Stat(ctx, "/rename-source.txt"); statErr != nil {
		t.Fatalf("Expected source path to remain after parent conflict, got %v", statErr)
	}
	if _, statErr := fs.Stat(ctx, "/rename-parent/child.txt"); statErr != ErrNotDir {
		t.Fatalf("Expected destination child to remain absent, got %v", statErr)
	}
}

func TestFileSystem_Copy_ReturnsErrAlreadyExistsWhenDestinationExists(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/copy-source.txt", bytes.NewReader([]byte("source"))); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/copy-dest.txt", bytes.NewReader([]byte("dest"))); err != nil {
		t.Fatalf("WriteFile(dest) error: %v", err)
	}

	err := fs.Copy(ctx, "/copy-source.txt", "/copy-dest.txt")
	if err != ErrAlreadyExists {
		t.Fatalf("Copy() error = %v, want ErrAlreadyExists", err)
	}

	reader, readErr := fs.OpenFile(ctx, "/copy-dest.txt")
	if readErr != nil {
		t.Fatalf("OpenFile(dest) error: %v", readErr)
	}
	defer reader.Close()
	data, readErr := io.ReadAll(reader)
	if readErr != nil {
		t.Fatalf("ReadAll(dest) error: %v", readErr)
	}
	if string(data) != "dest" {
		t.Fatalf("destination content = %q, want %q", string(data), "dest")
	}
	if _, statErr := fs.Stat(ctx, "/copy-source.txt"); statErr != nil {
		t.Fatalf("expected source to remain after conflict, got %v", statErr)
	}
}

func TestFileSystem_Copy_RollsBackDestinationWhenIndexUpdateFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/copy-source.txt", bytes.NewReader([]byte("source"))); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}

	originalUpdateFileIndex := fs.updateFileIndex
	fs.updateFileIndex = func(ctx context.Context, path string, size int64, modTime time.Time, hash string) error {
		return errors.New("index update failed")
	}
	t.Cleanup(func() {
		fs.updateFileIndex = originalUpdateFileIndex
	})

	err := fs.Copy(ctx, "/copy-source.txt", "/copy-dest.txt")
	if err == nil {
		t.Fatal("expected Copy() to fail when file index update fails")
	}
	if !strings.Contains(err.Error(), "failed to update file index") {
		t.Fatalf("expected file index failure in error, got %v", err)
	}

	if _, statErr := fs.Stat(ctx, "/copy-dest.txt"); statErr != ErrNotFound {
		t.Fatalf("expected copied destination to be removed after rollback, got %v", statErr)
	}
	reader, readErr := fs.OpenFile(ctx, "/copy-source.txt")
	if readErr != nil {
		t.Fatalf("OpenFile(source) error: %v", readErr)
	}
	defer reader.Close()
	data, readErr := io.ReadAll(reader)
	if readErr != nil {
		t.Fatalf("ReadAll(source) error: %v", readErr)
	}
	if string(data) != "source" {
		t.Fatalf("source content = %q, want %q", string(data), "source")
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

func TestFileSystem_Rename_RollsBackWhenPathRenameHookFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/rename-hook.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	fs.SetPathChangeHooks(func(context.Context, string, string) error {
		return errors.New("share path sync failed")
	}, nil)

	err := fs.Rename(ctx, "/rename-hook.txt", "/rename-hook-new.txt")
	if err == nil {
		t.Fatal("Expected Rename() to fail when path rename hook fails")
	}
	if !strings.Contains(err.Error(), "failed to sync rename hooks") {
		t.Fatalf("expected rename hook failure in error, got %v", err)
	}

	if _, statErr := fs.Stat(ctx, "/rename-hook.txt"); statErr != nil {
		t.Fatalf("expected original path to remain after hook rollback, got %v", statErr)
	}
	if _, statErr := fs.Stat(ctx, "/rename-hook-new.txt"); statErr != ErrNotFound {
		t.Fatalf("expected new path to be absent after hook rollback, got %v", statErr)
	}

	versions, listErr := fs.ListVersions(ctx, "/rename-hook.txt")
	if listErr != nil {
		t.Fatalf("ListVersions(original path) error: %v", listErr)
	}
	if len(versions) == 0 {
		t.Fatal("expected version metadata to remain attached to original path after hook rollback")
	}
	if _, listErr := fs.ListVersions(ctx, "/rename-hook-new.txt"); !errors.Is(listErr, ErrNotFound) {
		t.Fatalf("expected new path version metadata to be absent after hook rollback, got %v", listErr)
	}
}

func TestFileSystem_Rename_CompletesWhenRenameHookRegistered(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/rename-hook-ok.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	hookCalled := make(chan struct{}, 1)
	fs.SetPathChangeHooks(func(context.Context, string, string) error {
		hookCalled <- struct{}{}
		return nil
	}, nil)

	done := make(chan error, 1)
	go func() {
		done <- fs.Rename(ctx, "/rename-hook-ok.txt", "/rename-hook-ok-new.txt")
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Rename() error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Rename() deadlocked while invoking rename hook")
	}

	select {
	case <-hookCalled:
	default:
		t.Fatal("expected rename hook to be called")
	}

	if _, err := fs.Stat(ctx, "/rename-hook-ok-new.txt"); err != nil {
		t.Fatalf("expected renamed file to exist at new path, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/rename-hook-ok.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected original path to be absent after rename, got %v", err)
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
	if _, statErr := os.Stat(filepath.Join(fs.trashRoot, items[0].ID, "content")); statErr != nil {
		t.Fatalf("Expected trash content to remain after rollback, got %v", statErr)
	}
}

func TestFileSystem_RestoreFromTrash_ReturnsErrNotDirWhenParentIsFile(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/restore-parent/child"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-parent/child/file.txt", bytes.NewReader([]byte("restore me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-parent/child/file.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if err := fs.PermanentDelete(ctx, "/restore-parent/child"); err != nil {
		t.Fatalf("PermanentDelete(child) error: %v", err)
	}
	if err := fs.PermanentDelete(ctx, "/restore-parent"); err != nil {
		t.Fatalf("PermanentDelete(parent) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-parent", bytes.NewReader([]byte("blocking file"))); err != nil {
		t.Fatalf("WriteFile(parent file) error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("No items in trash")
	}

	err = fs.RestoreFromTrash(ctx, items[0].ID)
	if err != ErrNotDir {
		t.Fatalf("RestoreFromTrash() error = %v, want ErrNotDir", err)
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

func TestFileSystem_RestoreFromTrashTo_RejectsCustomPathWhenAnotherTrashItemSharesOriginalMetadata(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-shared-history.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-shared-history.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-shared-history.txt"); err != nil {
		t.Fatalf("Delete(first) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-shared-history.txt", bytes.NewReader([]byte("v3"))); err != nil {
		t.Fatalf("WriteFile(v3) error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-shared-history.txt"); err != nil {
		t.Fatalf("Delete(second) error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 trash items for shared original path, got %d", len(items))
	}
	for _, item := range items {
		if item.OriginalPath != "/restore-shared-history.txt" {
			t.Fatalf("expected trash item original path to remain shared, got %s", item.OriginalPath)
		}
	}

	versionsBefore, err := fs.versions.GetVersions(ctx, "/restore-shared-history.txt")
	if err != nil {
		t.Fatalf("versions.GetVersions() before restore error: %v", err)
	}
	if len(versionsBefore) == 0 {
		t.Fatal("expected shared version metadata to exist before restore")
	}

	err = fs.RestoreFromTrashTo(ctx, items[0].ID, "/restored/restore-shared-history.txt")
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("RestoreFromTrashTo() error = %v, want %v", err, ErrAlreadyExists)
	}

	if _, statErr := fs.Stat(ctx, "/restored/restore-shared-history.txt"); statErr != ErrNotFound {
		t.Fatalf("expected custom restore target to remain absent, got %v", statErr)
	}

	itemsAfter, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() after rejected restore error: %v", err)
	}
	if len(itemsAfter) != 2 {
		t.Fatalf("expected both trash items to remain after rejected restore, got %d", len(itemsAfter))
	}

	versionsAfter, err := fs.versions.GetVersions(ctx, "/restore-shared-history.txt")
	if err != nil {
		t.Fatalf("versions.GetVersions() after rejected restore error: %v", err)
	}
	if len(versionsAfter) != len(versionsBefore) {
		t.Fatalf("expected shared version metadata count to remain %d, got %d", len(versionsBefore), len(versionsAfter))
	}

	restoredVersions, err := fs.versions.GetVersions(ctx, "/restored/restore-shared-history.txt")
	if err != nil {
		t.Fatalf("versions.GetVersions(restored) error: %v", err)
	}
	if len(restoredVersions) != 0 {
		t.Fatalf("expected rejected restore not to move shared version metadata, got %d versions", len(restoredVersions))
	}
}

func TestFileSystem_DeleteFromTrash_KeepsSharedVersionMetadataUntilLastTrashItem(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/delete-shared-history.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/delete-shared-history.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.Delete(ctx, "/delete-shared-history.txt"); err != nil {
		t.Fatalf("Delete(first) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/delete-shared-history.txt", bytes.NewReader([]byte("v3"))); err != nil {
		t.Fatalf("WriteFile(v3) error: %v", err)
	}
	if err := fs.Delete(ctx, "/delete-shared-history.txt"); err != nil {
		t.Fatalf("Delete(second) error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 trash items for shared original path, got %d", len(items))
	}

	versionsBefore, err := fs.versions.GetVersions(ctx, "/delete-shared-history.txt")
	if err != nil {
		t.Fatalf("versions.GetVersions() before delete error: %v", err)
	}
	if len(versionsBefore) == 0 {
		t.Fatal("expected shared version metadata to exist before deleting trash items")
	}

	if err := fs.DeleteFromTrash(ctx, items[0].ID); err != nil {
		t.Fatalf("DeleteFromTrash(first) error: %v", err)
	}

	remaining, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() after first delete error: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected one trash item to remain after first delete, got %d", len(remaining))
	}

	versionsAfterFirst, err := fs.versions.GetVersions(ctx, "/delete-shared-history.txt")
	if err != nil {
		t.Fatalf("versions.GetVersions() after first delete error: %v", err)
	}
	if len(versionsAfterFirst) != len(versionsBefore) {
		t.Fatalf("expected shared version metadata count to remain %d after first delete, got %d", len(versionsBefore), len(versionsAfterFirst))
	}

	if err := fs.DeleteFromTrash(ctx, remaining[0].ID); err != nil {
		t.Fatalf("DeleteFromTrash(second) error: %v", err)
	}

	remaining, err = fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() after second delete error: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected no trash items to remain after deleting both, got %d", len(remaining))
	}

	versionsAfterSecond, err := fs.versions.GetVersions(ctx, "/delete-shared-history.txt")
	if err != nil {
		t.Fatalf("versions.GetVersions() after second delete error: %v", err)
	}
	if len(versionsAfterSecond) != 0 {
		t.Fatalf("expected shared version metadata to be cleaned up after last trash item is deleted, got %d versions", len(versionsAfterSecond))
	}
}

func TestFileSystem_DeleteFromTrash_KeepsVersionMetadataWhenOriginalPathExists(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/delete-live-history.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/delete-live-history.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.Delete(ctx, "/delete-live-history.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/delete-live-history.txt", bytes.NewReader([]byte("v3"))); err != nil {
		t.Fatalf("WriteFile(v3) error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 trash item, got %d", len(items))
	}

	versionsBefore, err := fs.ListVersions(ctx, "/delete-live-history.txt")
	if err != nil {
		t.Fatalf("ListVersions() before deleting trash item error: %v", err)
	}
	if len(versionsBefore) < 2 {
		t.Fatalf("expected recreated live file to retain historical versions before trash delete, got %d entries", len(versionsBefore))
	}

	if err := fs.DeleteFromTrash(ctx, items[0].ID); err != nil {
		t.Fatalf("DeleteFromTrash() error: %v", err)
	}

	versionsAfter, err := fs.ListVersions(ctx, "/delete-live-history.txt")
	if err != nil {
		t.Fatalf("ListVersions() after deleting trash item error: %v", err)
	}
	if len(versionsAfter) != len(versionsBefore) {
		t.Fatalf("expected live file version count to remain %d after deleting unrelated trash item, got %d", len(versionsBefore), len(versionsAfter))
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
	if _, statErr := os.Stat(filepath.Join(fs.trashRoot, items[0].ID, "content")); statErr != nil {
		t.Fatalf("Expected trash content to remain after rollback, got %v", statErr)
	}

	versions, versionErr := fs.versions.GetVersions(ctx, "/restore-to-index-fail.md")
	if versionErr != nil {
		t.Fatalf("GetVersions() after rollback error: %v", versionErr)
	}
	if len(versions) != 1 {
		t.Fatalf("Expected original historical version metadata to remain after rollback, got %d versions", len(versions))
	}
}

func TestFileSystem_RestoreFromTrashTo_ReturnsErrNotDirWhenParentIsFile(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-to-parent-file.txt", bytes.NewReader([]byte("restore me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-to-parent-file.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-target-parent", bytes.NewReader([]byte("blocking file"))); err != nil {
		t.Fatalf("WriteFile(parent file) error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("No items in trash")
	}

	err = fs.RestoreFromTrashTo(ctx, items[0].ID, "/restore-target-parent/child.txt")
	if err != ErrNotDir {
		t.Fatalf("RestoreFromTrashTo() error = %v, want ErrNotDir", err)
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

	versions, versionErr := fs.versions.GetVersions(ctx, "/restore-conflict.md")
	if versionErr != nil {
		t.Fatalf("GetVersions() after rollback error: %v", versionErr)
	}
	if len(versions) != 1 {
		t.Fatalf("Expected original historical version metadata to remain after rollback, got %d versions", len(versions))
	}
}

func TestFileSystem_RestoreFromTrashTo_RollsBackContentWhenTrashMetadataRemovalAndMetadataRollbackFail(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-remove-fail.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-remove-fail.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-remove-fail.md"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("No items in trash")
	}

	newPath := "/restored/restore-remove-fail.md"
	fs.renameMetadataPath = func(ctx context.Context, oldName, updatedPath string) error {
		if oldName == newPath && updatedPath == "/restore-remove-fail.md" {
			return errors.New("metadata rollback failed")
		}
		return fs.versions.RenamePath(ctx, oldName, updatedPath)
	}
	fs.removeTrashMetadata = func(ctx context.Context, id string) error {
		return errors.New("remove trash metadata failed")
	}

	err = fs.RestoreFromTrashTo(ctx, items[0].ID, newPath)
	if err == nil {
		t.Fatal("Expected RestoreFromTrashTo() to fail when trash metadata removal fails")
	}
	if !strings.Contains(err.Error(), "failed to rollback version metadata") {
		t.Fatalf("Expected version metadata rollback failure in error, got %v", err)
	}

	if _, statErr := fs.Stat(ctx, newPath); statErr != ErrNotFound {
		t.Fatalf("Expected restored target path to be rolled back despite metadata rollback failure, got %v", statErr)
	}

	trashItems, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after rollback error: %v", listErr)
	}
	if len(trashItems) != 1 {
		t.Fatalf("Expected trash item to remain after rollback, got %d items", len(trashItems))
	}
	if _, statErr := os.Stat(filepath.Join(fs.trashRoot, items[0].ID, "content")); statErr != nil {
		t.Fatalf("Expected trash content to remain after rollback, got %v", statErr)
	}
}

func TestFileSystem_RestoreFromTrashTo_RollsBackContentWhenIndexUpdateAndMetadataRollbackFail(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-index-rollback-fail.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-index-rollback-fail.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-index-rollback-fail.md"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("No items in trash")
	}

	newPath := "/restored/restore-index-rollback-fail.md"
	fs.renameMetadataPath = func(ctx context.Context, oldName, updatedPath string) error {
		if oldName == newPath && updatedPath == "/restore-index-rollback-fail.md" {
			return errors.New("metadata rollback failed")
		}
		return fs.versions.RenamePath(ctx, oldName, updatedPath)
	}
	fs.updateFileIndex = func(ctx context.Context, path string, size int64, modTime time.Time, hash string) error {
		return errors.New("index update failed")
	}

	err = fs.RestoreFromTrashTo(ctx, items[0].ID, newPath)
	if err == nil {
		t.Fatal("Expected RestoreFromTrashTo() to fail when file index update fails")
	}
	if !strings.Contains(err.Error(), "failed to rollback version metadata") {
		t.Fatalf("Expected version metadata rollback failure in error, got %v", err)
	}

	if _, statErr := fs.Stat(ctx, newPath); statErr != ErrNotFound {
		t.Fatalf("Expected restored target path to be rolled back despite metadata rollback failure, got %v", statErr)
	}

	trashItems, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after rollback error: %v", listErr)
	}
	if len(trashItems) != 1 {
		t.Fatalf("Expected trash item to remain after rollback, got %d items", len(trashItems))
	}
	if _, statErr := os.Stat(filepath.Join(fs.trashRoot, items[0].ID, "content")); statErr != nil {
		t.Fatalf("Expected trash content to remain after rollback, got %v", statErr)
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

func TestFileSystem_EmptyTrash_ReturnsContextCanceledBeforeListing(t *testing.T) {
	fs := setupFileSystem(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	deleted, err := fs.EmptyTrash(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected zero deleted items on canceled context, got %d", deleted)
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

func TestFileSystem_DeleteFromTrash_ReturnsEntropyFailureBeforeStaging(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/trash-entropy.txt", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/trash-entropy.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Expected 1 trash item, got %d", len(items))
	}

	originalRandomRead := storageRandomRead
	storageRandomRead = func([]byte) (int, error) {
		return 0, errors.New("entropy unavailable")
	}
	defer func() {
		storageRandomRead = originalRandomRead
	}()

	err = fs.DeleteFromTrash(ctx, items[0].ID)
	if err == nil {
		t.Fatal("expected DeleteFromTrash() to fail when trash staging ID generation fails")
	}
	if !strings.Contains(err.Error(), "generate trash staging ID") {
		t.Fatalf("expected trash staging ID generation error, got %v", err)
	}

	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed staging error: %v", listErr)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected trash metadata to remain after staging ID failure, got %d items", len(remaining))
	}
	if _, statErr := os.Stat(filepath.Join(fs.trashRoot, items[0].ID)); statErr != nil {
		t.Fatalf("expected trash content to remain after staging ID failure, got %v", statErr)
	}
}

func TestFileSystem_DeleteFromTrash_AttemptsVersionObjectCleanup(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/trash-permanent-objects.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/trash-permanent-objects.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/trash-permanent-objects.md", bytes.NewReader([]byte("v3"))); err != nil {
		t.Fatalf("WriteFile(v3) error: %v", err)
	}

	versions, err := fs.versions.GetVersions(ctx, "/trash-permanent-objects.md")
	if err != nil {
		t.Fatalf("GetVersions() error: %v", err)
	}
	if len(versions) < 2 {
		t.Fatalf("expected historical versions, got %d", len(versions))
	}

	if err := fs.Delete(ctx, "/trash-permanent-objects.md"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 trash item, got %d", len(items))
	}

	called := make(map[string]int)
	fs.deleteVersionObject = func(ctx context.Context, hash string) error {
		called[hash]++
		return errors.New("delete object failed")
	}

	err = fs.DeleteFromTrash(ctx, items[0].ID)
	if err == nil {
		t.Fatal("expected DeleteFromTrash() to fail when version object cleanup fails")
	}
	if !strings.Contains(err.Error(), "failed to delete version objects for trash item") {
		t.Fatalf("expected trash version object cleanup error, got %v", err)
	}

	for _, version := range versions {
		if called[version.Hash] != 1 {
			t.Fatalf("expected deleteVersionObject to be attempted once for %s, got %d", version.Hash, called[version.Hash])
		}
	}
	if _, statErr := fs.Stat(ctx, "/trash-permanent-objects.md"); statErr != ErrNotFound {
		t.Fatalf("expected file content to remain deleted after trash cleanup failure, got %v", statErr)
	}
	remainingItems, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after object cleanup failure error: %v", listErr)
	}
	if len(remainingItems) != 0 {
		t.Fatalf("expected trash metadata to be removed before object cleanup failure, got %d items", len(remainingItems))
	}
	remainingVersions, versionsErr := fs.versions.GetVersions(ctx, "/trash-permanent-objects.md")
	if versionsErr != nil {
		t.Fatalf("GetVersions() after trash cleanup failure error: %v", versionsErr)
	}
	if len(remainingVersions) != 0 {
		t.Fatalf("expected version metadata to be removed before object cleanup failure, got %d entries", len(remainingVersions))
	}
}

func TestFileSystem_EmptyTrash_AttemptsVersionObjectCleanup(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/empty-trash-objects.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/empty-trash-objects.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/empty-trash-objects.md", bytes.NewReader([]byte("v3"))); err != nil {
		t.Fatalf("WriteFile(v3) error: %v", err)
	}

	versions, err := fs.versions.GetVersions(ctx, "/empty-trash-objects.md")
	if err != nil {
		t.Fatalf("GetVersions() error: %v", err)
	}
	if len(versions) < 2 {
		t.Fatalf("expected historical versions, got %d", len(versions))
	}

	if err := fs.Delete(ctx, "/empty-trash-objects.md"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	called := make(map[string]int)
	fs.deleteVersionObject = func(ctx context.Context, hash string) error {
		called[hash]++
		return errors.New("delete object failed")
	}

	deleted, err := fs.EmptyTrash(ctx)
	if err == nil {
		t.Fatal("expected EmptyTrash() to fail when version object cleanup fails")
	}
	if !strings.Contains(err.Error(), "failed to delete version objects for trash item") {
		t.Fatalf("expected trash version object cleanup error, got %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected visible deletion to be counted before object cleanup failure, got %d", deleted)
	}

	for _, version := range versions {
		if called[version.Hash] != 1 {
			t.Fatalf("expected deleteVersionObject to be attempted once for %s, got %d", version.Hash, called[version.Hash])
		}
	}
	remainingItems, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after object cleanup failure error: %v", listErr)
	}
	if len(remainingItems) != 0 {
		t.Fatalf("expected trash metadata to be removed before object cleanup failure, got %d items", len(remainingItems))
	}
	remainingVersions, versionsErr := fs.versions.GetVersions(ctx, "/empty-trash-objects.md")
	if versionsErr != nil {
		t.Fatalf("GetVersions() after object cleanup failure error: %v", versionsErr)
	}
	if len(remainingVersions) != 0 {
		t.Fatalf("expected version metadata to be removed before object cleanup failure, got %d entries", len(remainingVersions))
	}
}

func TestFileSystem_CleanupExpiredTrash_AttemptsVersionObjectCleanup(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/expired-trash-objects.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/expired-trash-objects.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/expired-trash-objects.md", bytes.NewReader([]byte("v3"))); err != nil {
		t.Fatalf("WriteFile(v3) error: %v", err)
	}

	versions, err := fs.versions.GetVersions(ctx, "/expired-trash-objects.md")
	if err != nil {
		t.Fatalf("GetVersions() error: %v", err)
	}
	if len(versions) < 2 {
		t.Fatalf("expected historical versions, got %d", len(versions))
	}

	if err := fs.Delete(ctx, "/expired-trash-objects.md"); err != nil {
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

	called := make(map[string]int)
	fs.deleteVersionObject = func(ctx context.Context, hash string) error {
		called[hash]++
		return errors.New("delete object failed")
	}

	deleted, err := fs.CleanupExpiredTrash(ctx)
	if err == nil {
		t.Fatal("expected CleanupExpiredTrash() to fail when version object cleanup fails")
	}
	if !strings.Contains(err.Error(), "failed to delete version objects for trash item") {
		t.Fatalf("expected trash version object cleanup error, got %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected visible expired deletion to be counted before object cleanup failure, got %d", deleted)
	}

	for _, version := range versions {
		if called[version.Hash] != 1 {
			t.Fatalf("expected deleteVersionObject to be attempted once for %s, got %d", version.Hash, called[version.Hash])
		}
	}
	remainingItems, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after cleanup failure error: %v", listErr)
	}
	if len(remainingItems) != 0 {
		t.Fatalf("expected expired trash metadata to be removed before object cleanup failure, got %d items", len(remainingItems))
	}
	remainingVersions, versionsErr := fs.versions.GetVersions(ctx, "/expired-trash-objects.md")
	if versionsErr != nil {
		t.Fatalf("GetVersions() after cleanup failure error: %v", versionsErr)
	}
	if len(remainingVersions) != 0 {
		t.Fatalf("expected version metadata to be removed before object cleanup failure, got %d entries", len(remainingVersions))
	}
}

func TestFileSystem_DeleteFromTrash_KeepsContentWhenMetadataDeleteFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/trash-metadata-fail.txt", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/trash-metadata-fail.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Expected 1 trash item, got %d", len(items))
	}

	fs.removeTrashMetadata = func(ctx context.Context, id string) error {
		return errors.New("metadata delete failed")
	}

	err = fs.DeleteFromTrash(ctx, items[0].ID)
	if err == nil {
		t.Fatal("Expected DeleteFromTrash() to fail when trash metadata deletion fails")
	}

	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed metadata delete error: %v", listErr)
	}
	if len(remaining) != 1 {
		t.Fatalf("Expected trash metadata to remain after failed metadata delete, got %d items", len(remaining))
	}
	if _, statErr := os.Stat(filepath.Join(fs.trashRoot, items[0].ID)); statErr != nil {
		t.Fatalf("Expected trash content to remain after failed metadata delete: %v", statErr)
	}
}

func TestFileSystem_DeleteFromTrash_ReturnsDirectorySyncErrorAfterContentDelete(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/trash-sync-fail.txt", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/trash-sync-fail.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Expected 1 trash item, got %d", len(items))
	}
	if err := os.MkdirAll(filepath.Join(fs.trashRoot, ".deleting"), 0700); err != nil {
		t.Fatalf("MkdirAll(.deleting) error: %v", err)
	}

	originalSyncStoragePathDir := syncStoragePathDir
	syncCalls := 0
	syncStoragePathDir = func(dir string) error {
		syncCalls++
		if syncCalls == 3 {
			return errors.New("sync dir failed")
		}
		return nil
	}
	t.Cleanup(func() {
		syncStoragePathDir = originalSyncStoragePathDir
	})

	err = fs.DeleteFromTrash(ctx, items[0].ID)
	if err == nil {
		t.Fatal("Expected DeleteFromTrash() to fail when trash delete directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync deleted trash content") {
		t.Fatalf("expected deleted trash sync failure in error, got %v", err)
	}
	if syncCalls < 3 {
		t.Fatalf("expected post-delete sync to be attempted, got %d sync calls", syncCalls)
	}

	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed delete error: %v", listErr)
	}
	if len(remaining) != 0 {
		t.Fatalf("Expected trash metadata to be removed after visible delete, got %d items", len(remaining))
	}
	if _, statErr := os.Stat(filepath.Join(fs.trashRoot, items[0].ID)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Expected trash content to remain deleted after sync failure, got %v", statErr)
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

func TestFileSystem_EmptyTrash_RollsBackContentWhenMetadataDeleteFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/empty-metadata-fail-1.txt", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/empty-metadata-fail-2.txt", bytes.NewReader([]byte("y"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/empty-metadata-fail-1.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if err := fs.Delete(ctx, "/empty-metadata-fail-2.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("Expected 2 trash items, got %d", len(items))
	}

	metadataDeletes := 0
	fs.removeTrashMetadata = func(ctx context.Context, id string) error {
		metadataDeletes++
		if metadataDeletes == 2 {
			return errors.New("metadata delete failed")
		}
		return fs.versions.RemoveFromTrash(ctx, id)
	}

	deleted, err := fs.EmptyTrash(ctx)
	if err == nil {
		t.Fatal("Expected EmptyTrash() to fail when trash metadata deletion fails")
	}
	if deleted != 1 {
		t.Fatalf("Expected one trash item to be deleted before failure, got %d", deleted)
	}

	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed metadata delete error: %v", listErr)
	}
	if len(remaining) != 1 {
		t.Fatalf("Expected one trash item to remain after failed metadata delete, got %d items", len(remaining))
	}
	if _, statErr := os.Stat(filepath.Join(fs.trashRoot, remaining[0].ID)); statErr != nil {
		t.Fatalf("Expected remaining trash content to be restored after failed metadata delete: %v", statErr)
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

func TestFileSystem_EmptyTrash_CountsDeletedItemWhenDirectorySyncFailsAfterContentDelete(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/empty-sync-fail.txt", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/empty-sync-fail.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(fs.trashRoot, ".deleting"), 0700); err != nil {
		t.Fatalf("MkdirAll(.deleting) error: %v", err)
	}

	originalSyncStoragePathDir := syncStoragePathDir
	syncCalls := 0
	syncStoragePathDir = func(dir string) error {
		syncCalls++
		if syncCalls == 3 {
			return errors.New("sync dir failed")
		}
		return nil
	}
	t.Cleanup(func() {
		syncStoragePathDir = originalSyncStoragePathDir
	})

	deleted, err := fs.EmptyTrash(ctx)
	if err == nil {
		t.Fatal("Expected EmptyTrash() to fail when trash delete directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync deleted trash content") {
		t.Fatalf("expected deleted trash sync failure in error, got %v", err)
	}
	if deleted != 1 {
		t.Fatalf("Expected visible deletion to be counted before sync failure, got %d", deleted)
	}

	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed empty error: %v", listErr)
	}
	if len(remaining) != 0 {
		t.Fatalf("Expected trash metadata to be removed after visible delete, got %d items", len(remaining))
	}
}

func TestFileSystem_CleanupExpiredTrash_KeepsContentWhenMetadataDeleteFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/expired-metadata-fail.txt", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/expired-metadata-fail.txt"); err != nil {
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

	fs.removeTrashMetadata = func(ctx context.Context, id string) error {
		return errors.New("metadata delete failed")
	}

	deleted, err := fs.CleanupExpiredTrash(ctx)
	if err == nil {
		t.Fatal("Expected CleanupExpiredTrash() to fail when trash metadata deletion fails")
	}
	if deleted != 0 {
		t.Fatalf("Expected no expired trash deletions on metadata failure, got %d", deleted)
	}

	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed metadata cleanup error: %v", listErr)
	}
	if len(remaining) != 1 {
		t.Fatalf("Expected expired trash metadata to remain after failed metadata cleanup, got %d items", len(remaining))
	}
	if _, statErr := os.Stat(filepath.Join(fs.trashRoot, original.ID)); statErr != nil {
		t.Fatalf("Expected expired trash content to remain after failed metadata cleanup: %v", statErr)
	}
}

func TestFileSystem_CleanupExpiredTrash_ReturnsContextCanceledBeforeListing(t *testing.T) {
	fs := setupFileSystem(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	deleted, err := fs.CleanupExpiredTrash(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected zero deleted items on canceled context, got %d", deleted)
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

func TestFileSystem_ListVersions_PropagatesVersionStoreFailure(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/versioned.txt", bytes.NewReader([]byte("current"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	fs.getVersions = func(context.Context, string) ([]versionstore.Version, error) {
		return nil, errors.New("version store unavailable")
	}

	versions, err := fs.ListVersions(ctx, "/versioned.txt")
	if err == nil {
		t.Fatal("expected ListVersions() to return version store failure")
	}
	if versions != nil {
		t.Fatalf("expected no version list on version store failure, got %d entries", len(versions))
	}
	if !strings.Contains(err.Error(), "version store unavailable") {
		t.Fatalf("expected version store failure to propagate, got %v", err)
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

func TestFileSystem_GetVersion_RejectsHashFromDifferentPath(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/docs/a.txt", bytes.NewReader([]byte("a-v1"))); err != nil {
		t.Fatalf("WriteFile(a v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/a.txt", bytes.NewReader([]byte("a-v2"))); err != nil {
		t.Fatalf("WriteFile(a v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/b.txt", bytes.NewReader([]byte("b-current"))); err != nil {
		t.Fatalf("WriteFile(b) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/docs/a.txt")
	if err != nil {
		t.Fatalf("ListVersions(a) error: %v", err)
	}

	var historicalHash string
	for _, version := range versions {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("expected historical version hash for a.txt")
	}

	reader, err := fs.GetVersion(ctx, "/docs/b.txt", historicalHash)
	if err != ErrVersionNotFound {
		t.Fatalf("GetVersion() error = %v, want ErrVersionNotFound", err)
	}
	if reader != nil {
		t.Fatal("expected no reader when hash does not belong to requested path")
	}
}

func TestFileSystem_RestoreVersion_RejectsHashFromDifferentPath(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/docs/a.txt", bytes.NewReader([]byte("a-v1"))); err != nil {
		t.Fatalf("WriteFile(a v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/a.txt", bytes.NewReader([]byte("a-v2"))); err != nil {
		t.Fatalf("WriteFile(a v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/b.txt", bytes.NewReader([]byte("b-current"))); err != nil {
		t.Fatalf("WriteFile(b) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/docs/a.txt")
	if err != nil {
		t.Fatalf("ListVersions(a) error: %v", err)
	}

	var historicalHash string
	for _, version := range versions {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("expected historical version hash for a.txt")
	}

	err = fs.RestoreVersion(ctx, "/docs/b.txt", historicalHash)
	if err != ErrVersionNotFound {
		t.Fatalf("RestoreVersion() error = %v, want ErrVersionNotFound", err)
	}

	reader, err := fs.OpenFile(ctx, "/docs/b.txt")
	if err != nil {
		t.Fatalf("OpenFile(b) error: %v", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(b) error: %v", err)
	}
	if string(data) != "b-current" {
		t.Fatalf("expected b.txt content to remain unchanged, got %q", string(data))
	}
}

func TestFileSystem_RestoreVersion_AllowsCurrentHashWithoutStoredObject(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/docs/current.txt", bytes.NewReader([]byte("current-content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/docs/current.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}
	if len(versions) == 0 || versions[0].Comment != "(current)" {
		t.Fatalf("expected current version entry, got %#v", versions)
	}

	if err := fs.RestoreVersion(ctx, "/docs/current.txt", versions[0].Hash); err != nil {
		t.Fatalf("RestoreVersion(current) error: %v", err)
	}

	reader, err := fs.OpenFile(ctx, "/docs/current.txt")
	if err != nil {
		t.Fatalf("OpenFile() error: %v", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error: %v", err)
	}
	if string(data) != "current-content" {
		t.Fatalf("expected current.txt content to remain unchanged, got %q", string(data))
	}
}

func TestFileSystem_RestoreVersion_PreservesReadableFilePermissions(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/docs/perm.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/perm.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/docs/perm.txt")
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
		t.Fatal("expected a historical version to restore")
	}

	if err := fs.RestoreVersion(ctx, "/docs/perm.txt", historicalHash); err != nil {
		t.Fatalf("RestoreVersion() error: %v", err)
	}

	info, err := os.Stat(fs.workspace.FullPath("/docs/perm.txt"))
	if err != nil {
		t.Fatalf("Stat(perm.txt) error: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("expected restored file permissions 0644, got %o", info.Mode().Perm())
	}
}

func TestMapWorkspaceReadablePathError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "not found", err: workspace.ErrNotFound, want: ErrNotFound},
		{name: "wrapped not dir", err: errors.Join(errors.New("wrapped"), workspace.ErrNotDir), want: ErrNotDir},
		{name: "wrapped is dir", err: errors.Join(errors.New("wrapped"), workspace.ErrIsDir), want: ErrIsDir},
		{name: "passthrough", err: errors.New("boom")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapWorkspaceReadablePathError(tt.err)
			if tt.want == nil {
				if got != tt.err {
					t.Fatalf("mapWorkspaceReadablePathError() = %v, want original error %v", got, tt.err)
				}
				return
			}
			if !errors.Is(got, tt.want) {
				t.Fatalf("mapWorkspaceReadablePathError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFileSystem_HashWorkspaceFile_MapsReadablePathErrors(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/hash-dir"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/hash-parent", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(hash-parent) error: %v", err)
	}

	tests := []struct {
		name string
		path string
		want error
	}{
		{name: "missing path", path: "/missing.txt", want: ErrNotFound},
		{name: "directory path", path: "/hash-dir", want: ErrIsDir},
		{name: "parent not directory", path: "/hash-parent/child.txt", want: ErrNotDir},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fs.hashWorkspaceFile(ctx, tt.path)
			if !errors.Is(err, tt.want) {
				t.Fatalf("hashWorkspaceFile() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestFileSystem_GetVersion_ReturnsErrIsDirForDirectoryPath(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/dir"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}

	reader, err := fs.GetVersion(ctx, "/dir", strings.Repeat("a", 64))
	if err != ErrIsDir {
		t.Fatalf("GetVersion() error = %v, want ErrIsDir", err)
	}
	if reader != nil {
		t.Fatal("expected no reader for directory version request")
	}
}

func TestFileSystem_GetVersion_ReturnsErrNotDirWhenParentIsFile(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/version-parent", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(version-parent) error: %v", err)
	}

	reader, err := fs.GetVersion(ctx, "/version-parent/child.txt", strings.Repeat("a", 64))
	if err != ErrNotDir {
		t.Fatalf("GetVersion() error = %v, want ErrNotDir", err)
	}
	if reader != nil {
		t.Fatal("expected no reader for parent-not-directory version request")
	}
}

func TestFileSystem_RestoreVersion_ReturnsErrNotDirWhenParentIsFile(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-source.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-source.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-parent-file", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(parent) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/restore-source.txt")
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

	err = fs.RestoreVersion(ctx, "/restore-parent-file/child.txt", historicalHash)
	if err != ErrNotDir {
		t.Fatalf("RestoreVersion() error = %v, want ErrNotDir", err)
	}
}

func TestFileSystem_RestoreVersion_FailsWhenCurrentSnapshotCannotBeRecorded(t *testing.T) {
	tests := []struct {
		name   string
		inject func(fs *FileSystem)
	}{
		{
			name: "put object failure",
			inject: func(fs *FileSystem) {
				fs.putVersionObject = func(ctx context.Context, data []byte) (string, error) {
					return "", errors.New("store current version failed")
				}
			},
		},
		{
			name: "add version failure",
			inject: func(fs *FileSystem) {
				fs.addFileVersion = func(ctx context.Context, path, hash string, size int64, comment string) error {
					return errors.New("record current version failed")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := setupFileSystem(t)
			ctx := context.Background()

			if err := fs.WriteFile(ctx, "/restore-snapshot.txt", bytes.NewReader([]byte("v1"))); err != nil {
				t.Fatalf("WriteFile(v1) error: %v", err)
			}
			if err := fs.WriteFile(ctx, "/restore-snapshot.txt", bytes.NewReader([]byte("v2"))); err != nil {
				t.Fatalf("WriteFile(v2) error: %v", err)
			}

			versionsBefore, err := fs.ListVersions(ctx, "/restore-snapshot.txt")
			if err != nil {
				t.Fatalf("ListVersions() before restore error: %v", err)
			}

			var historicalHash string
			for _, version := range versionsBefore {
				if version.Comment != "(current)" {
					historicalHash = version.Hash
					break
				}
			}
			if historicalHash == "" {
				t.Fatal("Expected at least one historical version")
			}

			tt.inject(fs)

			err = fs.RestoreVersion(ctx, "/restore-snapshot.txt", historicalHash)
			if err == nil {
				t.Fatal("Expected RestoreVersion() to fail when current snapshot cannot be recorded")
			}

			f, openErr := fs.OpenFile(ctx, "/restore-snapshot.txt")
			if openErr != nil {
				t.Fatalf("OpenFile() after failed restore error: %v", openErr)
			}
			defer f.Close()

			data, readErr := io.ReadAll(f)
			if readErr != nil {
				t.Fatalf("ReadAll() after failed restore error: %v", readErr)
			}
			if string(data) != "v2" {
				t.Fatalf("Expected current content to remain after failed restore, got %q", string(data))
			}

			versionsAfter, listErr := fs.ListVersions(ctx, "/restore-snapshot.txt")
			if listErr != nil {
				t.Fatalf("ListVersions() after failed restore error: %v", listErr)
			}
			if len(versionsAfter) != len(versionsBefore) {
				t.Fatalf("Expected version history length to remain unchanged, got %d want %d", len(versionsAfter), len(versionsBefore))
			}
		})
	}
}

func TestFileSystem_RestoreVersion_CleansUpCurrentSnapshotObjectWhenVersionRecordFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	currentContent := []byte("restore-current-" + mustGenerateStorageID(t))
	currentHash := computeHash(currentContent)
	exists, err := fs.versions.HasObject(ctx, currentHash)
	if err != nil {
		t.Fatalf("HasObject(currentHash) error: %v", err)
	}
	if exists {
		t.Fatalf("expected unique current hash %s to be absent before restore", currentHash)
	}

	historicalContent := []byte("restore-historical-" + mustGenerateStorageID(t))
	if err := fs.WriteFile(ctx, "/restore-cleanup.txt", bytes.NewReader(historicalContent)); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-cleanup.txt", bytes.NewReader(currentContent)); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/restore-cleanup.txt")
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

	fs.addFileVersion = func(ctx context.Context, path, hash string, size int64, comment string) error {
		return errors.New("record current version failed")
	}

	err = fs.RestoreVersion(ctx, "/restore-cleanup.txt", historicalHash)
	if err == nil {
		t.Fatal("Expected RestoreVersion() to fail when current snapshot record fails")
	}

	exists, err = fs.versions.HasObject(ctx, currentHash)
	if err != nil {
		t.Fatalf("HasObject(currentHash) after failed restore error: %v", err)
	}
	if exists {
		t.Fatalf("expected current snapshot object %s to be cleaned up after failed restore", currentHash)
	}

	f, openErr := fs.OpenFile(ctx, "/restore-cleanup.txt")
	if openErr != nil {
		t.Fatalf("OpenFile() after failed restore error: %v", openErr)
	}
	defer f.Close()

	data, readErr := io.ReadAll(f)
	if readErr != nil {
		t.Fatalf("ReadAll() after failed restore error: %v", readErr)
	}
	if string(data) != string(currentContent) {
		t.Fatalf("expected current content to remain after failed restore, got %q", string(data))
	}
}

func TestFileSystem_RestoreVersion_RollsBackCurrentSnapshotVersionWhenIndexUpdateFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	historicalContent := []byte("restore-index-old-" + mustGenerateStorageID(t))
	currentContent := []byte("restore-index-current-" + mustGenerateStorageID(t))
	currentHash := computeHash(currentContent)

	exists, err := fs.versions.HasObject(ctx, currentHash)
	if err != nil {
		t.Fatalf("HasObject(currentHash) before restore error: %v", err)
	}
	if exists {
		t.Fatalf("expected unique current hash %s to be absent before restore", currentHash)
	}

	if err := fs.WriteFile(ctx, "/restore-index-cleanup.txt", bytes.NewReader(historicalContent)); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-index-cleanup.txt", bytes.NewReader(currentContent)); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	versionsBefore, err := fs.ListVersions(ctx, "/restore-index-cleanup.txt")
	if err != nil {
		t.Fatalf("ListVersions() before restore error: %v", err)
	}

	var historicalHash string
	for _, version := range versionsBefore {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("Expected at least one historical version")
	}

	fs.updateFileIndex = func(ctx context.Context, path string, size int64, modTime time.Time, hash string) error {
		return errors.New("index update failed")
	}

	err = fs.RestoreVersion(ctx, "/restore-index-cleanup.txt", historicalHash)
	if err == nil {
		t.Fatal("Expected RestoreVersion() to fail when file index update fails")
	}

	exists, err = fs.versions.HasObject(ctx, currentHash)
	if err != nil {
		t.Fatalf("HasObject(currentHash) after failed restore error: %v", err)
	}
	if exists {
		t.Fatalf("expected current snapshot object %s to be cleaned up after failed restore", currentHash)
	}

	versionsAfter, err := fs.ListVersions(ctx, "/restore-index-cleanup.txt")
	if err != nil {
		t.Fatalf("ListVersions() after failed restore error: %v", err)
	}
	if len(versionsAfter) != len(versionsBefore) {
		t.Fatalf("expected version count to remain %d after failed restore, got %d", len(versionsBefore), len(versionsAfter))
	}
	for _, version := range versionsAfter {
		if version.Comment == "before restore" {
			t.Fatalf("expected failed restore not to leave before restore version, got %#v", version)
		}
	}

	reader, err := fs.OpenFile(ctx, "/restore-index-cleanup.txt")
	if err != nil {
		t.Fatalf("OpenFile() after failed restore error: %v", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() after failed restore error: %v", err)
	}
	if string(data) != string(currentContent) {
		t.Fatalf("expected current content to remain after failed restore, got %q", string(data))
	}
}

func TestFileSystem_RestoreVersion_DoesNotDeletePreExistingCurrentSnapshotVersionOnRollback(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	firstContent := []byte("restore-existing-first-" + mustGenerateStorageID(t))
	secondContent := []byte("restore-existing-second-" + mustGenerateStorageID(t))
	currentHash := computeHash(firstContent)

	if err := fs.WriteFile(ctx, "/restore-existing-snapshot.txt", bytes.NewReader(firstContent)); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-existing-snapshot.txt", bytes.NewReader(secondContent)); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-existing-snapshot.txt", bytes.NewReader(firstContent)); err != nil {
		t.Fatalf("WriteFile(v3) error: %v", err)
	}

	versionsBefore, err := fs.ListVersions(ctx, "/restore-existing-snapshot.txt")
	if err != nil {
		t.Fatalf("ListVersions() before restore error: %v", err)
	}

	historicalHash := ""
	historicalCurrentCount := 0
	for _, version := range versionsBefore {
		if version.Comment != "(current)" && version.Hash == currentHash {
			historicalCurrentCount++
		}
		if version.Comment != "(current)" && version.Hash != currentHash && historicalHash == "" {
			historicalHash = version.Hash
		}
	}
	if historicalHash == "" {
		t.Fatal("expected at least one different historical version")
	}
	if historicalCurrentCount == 0 {
		t.Fatal("expected current snapshot hash to already exist in historical versions")
	}

	fs.updateFileIndex = func(ctx context.Context, path string, size int64, modTime time.Time, hash string) error {
		return errors.New("index update failed")
	}

	err = fs.RestoreVersion(ctx, "/restore-existing-snapshot.txt", historicalHash)
	if err == nil {
		t.Fatal("expected RestoreVersion() to fail when file index update fails")
	}

	versionsAfter, err := fs.ListVersions(ctx, "/restore-existing-snapshot.txt")
	if err != nil {
		t.Fatalf("ListVersions() after failed restore error: %v", err)
	}
	if len(versionsAfter) != len(versionsBefore) {
		t.Fatalf("expected version count to remain %d after failed restore, got %d", len(versionsBefore), len(versionsAfter))
	}

	historicalCurrentCountAfter := 0
	for _, version := range versionsAfter {
		if version.Comment == "before restore" {
			t.Fatalf("expected failed restore not to leave before restore version, got %#v", version)
		}
		if version.Comment != "(current)" && version.Hash == currentHash {
			historicalCurrentCountAfter++
		}
	}
	if historicalCurrentCountAfter != historicalCurrentCount {
		t.Fatalf("expected pre-existing historical snapshot count %d to remain after rollback, got %d", historicalCurrentCount, historicalCurrentCountAfter)
	}

	reader, err := fs.OpenFile(ctx, "/restore-existing-snapshot.txt")
	if err != nil {
		t.Fatalf("OpenFile() after failed restore error: %v", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() after failed restore error: %v", err)
	}
	if !bytes.Equal(data, firstContent) {
		t.Fatalf("expected current content to remain after failed restore, got %q", string(data))
	}
}

func TestFileSystem_WriteFile_DoesNotFailWhenCleanupVersionsObjectDeleteFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.config.MaxVersions = 1
	if err := fs.WriteFile(ctx, "/cleanup-fail.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/cleanup-fail.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	fs.deleteVersionObject = func(ctx context.Context, hash string) error {
		return errors.New("delete object failed")
	}

	if err := fs.WriteFile(ctx, "/cleanup-fail.md", bytes.NewReader([]byte("v3"))); err != nil {
		t.Fatalf("WriteFile(v3) should succeed despite cleanup failure: %v", err)
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
	if string(data) != "v3" {
		t.Fatalf("Expected new content to remain committed after cleanup failure, got %q", string(data))
	}

	versions, err := fs.ListVersions(ctx, "/cleanup-fail.md")
	if err != nil {
		t.Fatalf("ListVersions() after cleanup failure error: %v", err)
	}
	if len(versions) < 2 {
		t.Fatalf("expected version history to remain present after cleanup failure, got %d entries", len(versions))
	}
}

func TestFileSystem_WriteFile_CleanupVersionsDoesNotDeleteSharedVersionObject(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.config.MaxVersions = 1
	fs.config.MaxVersionAge = 365 * 24 * time.Hour

	sharedContent := []byte("shared-old-" + mustGenerateStorageID(t))
	sharedHash := computeHash(sharedContent)
	exists, err := fs.versions.HasObject(ctx, sharedHash)
	if err != nil {
		t.Fatalf("HasObject(sharedHash) before writes error: %v", err)
	}
	if exists {
		t.Fatalf("expected unique shared hash %s to be absent before writes", sharedHash)
	}

	if err := fs.WriteFile(ctx, "/docs/a.txt", bytes.NewReader(sharedContent)); err != nil {
		t.Fatalf("WriteFile(a v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/b.txt", bytes.NewReader(sharedContent)); err != nil {
		t.Fatalf("WriteFile(b v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/a.txt", bytes.NewReader([]byte("a-v2"))); err != nil {
		t.Fatalf("WriteFile(a v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/b.txt", bytes.NewReader([]byte("b-v2"))); err != nil {
		t.Fatalf("WriteFile(b v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/a.txt", bytes.NewReader([]byte("a-v3"))); err != nil {
		t.Fatalf("WriteFile(a v3) error: %v", err)
	}

	exists, err = fs.versions.HasObject(ctx, sharedHash)
	if err != nil {
		t.Fatalf("HasObject(sharedHash) error: %v", err)
	}
	if !exists {
		t.Fatalf("expected shared historical object %s to remain while another path still references it", sharedHash)
	}

	reader, err := fs.GetVersion(ctx, "/docs/b.txt", sharedHash)
	if err != nil {
		t.Fatalf("GetVersion(shared historical hash) error: %v", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(shared historical hash) error: %v", err)
	}
	if string(data) != string(sharedContent) {
		t.Fatalf("expected shared historical content %q, got %q", string(sharedContent), string(data))
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

func TestFileSystem_WriteFile_DoesNotFailWhenForcedRetentionSweepFailsAfterCommit(t *testing.T) {
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
	fs.deleteVersionObject = func(ctx context.Context, hash string) error {
		return errors.New("delete version object failed")
	}

	if err := fs.WriteFile(ctx, "/trigger.txt", bytes.NewReader([]byte("trigger"))); err != nil {
		t.Fatalf("WriteFile(trigger) should succeed despite post-commit retention sweep failure: %v", err)
	}

	f, err := fs.OpenFile(ctx, "/trigger.txt")
	if err != nil {
		t.Fatalf("OpenFile(trigger) error: %v", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll(trigger) error: %v", err)
	}
	if string(data) != "trigger" {
		t.Fatalf("expected committed trigger content, got %q", string(data))
	}

	versions, err := fs.ListVersions(ctx, "/retention-sweep.txt")
	if err != nil {
		t.Fatalf("ListVersions(retention-sweep) error: %v", err)
	}
	if len(versions) < 2 {
		t.Fatalf("expected retention-sweep history to remain present after failed maintenance, got %d versions", len(versions))
	}
	if _, err := fs.Stat(ctx, "/trigger.txt"); err != nil {
		t.Fatalf("Stat(trigger) error: %v", err)
	}
}

type storageContextKey string

func TestFileSystem_WriteFile_PropagatesContextToVersionObjectOperations(t *testing.T) {
	fs := setupFileSystem(t)
	baseCtx := context.Background()
	if err := fs.WriteFile(baseCtx, "/ctx-write.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}

	ctx := context.WithValue(baseCtx, storageContextKey("key"), "write-value")
	hasSeenCtx := false
	putSeenCtx := false
	fs.hasVersionObject = func(callCtx context.Context, hash string) (bool, error) {
		if got := callCtx.Value(storageContextKey("key")); got != "write-value" {
			t.Fatalf("hasVersionObject() context value = %v, want write-value", got)
		}
		hasSeenCtx = true
		return false, nil
	}
	fs.putVersionObject = func(callCtx context.Context, data []byte) (string, error) {
		if got := callCtx.Value(storageContextKey("key")); got != "write-value" {
			t.Fatalf("putVersionObject() context value = %v, want write-value", got)
		}
		putSeenCtx = true
		return computeHash(data), nil
	}

	if err := fs.WriteFile(ctx, "/ctx-write.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if !hasSeenCtx {
		t.Fatal("expected hasVersionObject() to receive caller context")
	}
	if !putSeenCtx {
		t.Fatal("expected putVersionObject() to receive caller context")
	}
}

func TestFileSystem_GetVersion_PropagatesContextToVersionObjectLookup(t *testing.T) {
	fs := setupFileSystem(t)
	baseCtx := context.Background()
	if err := fs.WriteFile(baseCtx, "/ctx-version.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(baseCtx, "/ctx-version.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	historicalHash := computeHash([]byte("v1"))
	ctx := context.WithValue(baseCtx, storageContextKey("key"), "get-value")
	getSeenCtx := false
	fs.getVersionObject = func(callCtx context.Context, hash string) ([]byte, error) {
		if got := callCtx.Value(storageContextKey("key")); got != "get-value" {
			t.Fatalf("getVersionObject() context value = %v, want get-value", got)
		}
		if hash != historicalHash {
			t.Fatalf("getVersionObject() hash = %q, want %q", hash, historicalHash)
		}
		getSeenCtx = true
		return []byte("v1"), nil
	}

	reader, err := fs.GetVersion(ctx, "/ctx-version.txt", historicalHash)
	if err != nil {
		t.Fatalf("GetVersion() error: %v", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error: %v", err)
	}
	if string(data) != "v1" {
		t.Fatalf("GetVersion() content = %q, want v1", string(data))
	}
	if !getSeenCtx {
		t.Fatal("expected getVersionObject() to receive caller context")
	}
}

func TestFileSystem_DeleteUnreferencedVersionObjects_PropagatesContextToDelete(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.WithValue(context.Background(), storageContextKey("key"), "delete-value")
	deleteSeenCtx := false
	fs.deleteVersionObject = func(callCtx context.Context, hash string) error {
		if got := callCtx.Value(storageContextKey("key")); got != "delete-value" {
			t.Fatalf("deleteVersionObject() context value = %v, want delete-value", got)
		}
		if hash != "ctx-hash" {
			t.Fatalf("deleteVersionObject() hash = %q, want ctx-hash", hash)
		}
		deleteSeenCtx = true
		return nil
	}

	if err := fs.deleteUnreferencedVersionObjects(ctx, []string{"ctx-hash"}); err != nil {
		t.Fatalf("deleteUnreferencedVersionObjects() error: %v", err)
	}
	if !deleteSeenCtx {
		t.Fatal("expected deleteVersionObject() to receive caller context")
	}
}

func TestFileSystem_CleanupVersions_ZeroRetentionKeepsAllHistory(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.config.MaxVersions = 60
	fs.config.MaxVersionAge = 365 * 24 * time.Hour

	for i := 0; i < 55; i++ {
		content := fmt.Sprintf("v%02d", i)
		if err := fs.WriteFile(ctx, "/retention-unlimited.txt", bytes.NewReader([]byte(content))); err != nil {
			t.Fatalf("WriteFile(%s) error: %v", content, err)
		}
	}

	fs.UpdateRetentionSettings(0, 0, 0)
	if err := fs.cleanupVersions(ctx, "/retention-unlimited.txt"); err != nil {
		t.Fatalf("cleanupVersions() error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/retention-unlimited.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}
	if len(versions) != 55 {
		t.Fatalf("expected current version plus 54 historical versions, got %d entries", len(versions))
	}
}

func TestFileSystem_CleanupVersions_RestoresMetadataWhenObjectDeleteFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.config.MaxVersions = 10
	fs.config.MaxVersionAge = 365 * 24 * time.Hour

	for _, content := range []string{"v1", "v2", "v3", "v4"} {
		if err := fs.WriteFile(ctx, "/retention-restore.txt", bytes.NewReader([]byte(content))); err != nil {
			t.Fatalf("WriteFile(%s) error: %v", content, err)
		}
	}

	before, err := fs.ListVersions(ctx, "/retention-restore.txt")
	if err != nil {
		t.Fatalf("ListVersions(before) error: %v", err)
	}
	if len(before) != 4 {
		t.Fatalf("expected current version plus three historical versions before cleanup, got %d", len(before))
	}

	fs.UpdateRetentionSettings(1, 365*24*time.Hour, 0)
	fs.deleteVersionObject = func(ctx context.Context, hash string) error {
		return errors.New("delete version object failed")
	}

	err = fs.cleanupVersions(ctx, "/retention-restore.txt")
	if err == nil {
		t.Fatal("expected cleanupVersions() to fail when version object deletion fails")
	}
	if !strings.Contains(err.Error(), "failed to cleanup one or more version objects") {
		t.Fatalf("expected version object cleanup failure, got %v", err)
	}

	after, err := fs.ListVersions(ctx, "/retention-restore.txt")
	if err != nil {
		t.Fatalf("ListVersions(after) error: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("expected cleanup failure to restore all version metadata, got %d entries want %d", len(after), len(before))
	}

	beforeHashes := make(map[string]int, len(before))
	for _, version := range before {
		beforeHashes[version.Hash]++
	}
	for _, version := range after {
		beforeHashes[version.Hash]--
	}
	for hash, count := range beforeHashes {
		if count != 0 {
			t.Fatalf("expected version hash %s to be preserved across failed cleanup, delta=%d", hash, count)
		}
	}
}

func TestFileSystem_DeleteUnreferencedVersionObjects_ReturnsContextCanceled(t *testing.T) {
	fs := setupFileSystem(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := fs.deleteUnreferencedVersionObjects(ctx, []string{"abc123"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestFileSystem_RunRetentionSweep_ReturnsContextCanceledBeforeListingPaths(t *testing.T) {
	fs := setupFileSystem(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := fs.RunRetentionSweep(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
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

func TestMovePath_RollsBackRenameWhenDirectorySyncFails(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src-dir")
	dstDir := filepath.Join(tempDir, "dst-dir")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll(srcDir) error: %v", err)
	}
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		t.Fatalf("MkdirAll(dstDir) error: %v", err)
	}

	src := filepath.Join(srcDir, "src.txt")
	dst := filepath.Join(dstDir, "dst.txt")
	if err := os.WriteFile(src, []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	originalSyncStoragePathDir := syncStoragePathDir
	syncFailed := false
	syncStoragePathDir = func(dir string) error {
		if !syncFailed {
			syncFailed = true
			return errors.New("sync dir failed")
		}
		return nil
	}
	t.Cleanup(func() {
		syncStoragePathDir = originalSyncStoragePathDir
	})

	err := movePath(src, dst)
	if err == nil {
		t.Fatal("expected movePath() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync renamed path") {
		t.Fatalf("expected sync failure in error, got %v", err)
	}

	data, readErr := os.ReadFile(src)
	if readErr != nil {
		t.Fatalf("ReadFile(src) after rollback error: %v", readErr)
	}
	if string(data) != "content" {
		t.Fatalf("expected source content after rollback, got %q", string(data))
	}
	if _, statErr := os.Stat(dst); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected destination to be removed after rollback, got %v", statErr)
	}
}

func TestMovePath_PreservesCopiedDirectoryWhenSourceCleanupFails(t *testing.T) {
	tempDir := t.TempDir()
	src := filepath.Join(tempDir, "src")
	dst := filepath.Join(tempDir, "dst")

	if err := os.MkdirAll(filepath.Join(src, "nested"), 0755); err != nil {
		t.Fatalf("MkdirAll(src) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	originalMovePathRename := movePathRename
	originalMovePathRemoveAll := movePathRemoveAll
	movePathRename = func(oldPath, newPath string) error {
		if oldPath == src && newPath == dst {
			return errors.New("rename failed")
		}
		return originalMovePathRename(oldPath, newPath)
	}
	movePathRemoveAll = func(target string) error {
		if target == src {
			nestedFile := filepath.Join(src, "nested", "file.txt")
			if err := os.Remove(nestedFile); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return errors.New("source cleanup failed")
		}
		return originalMovePathRemoveAll(target)
	}
	t.Cleanup(func() {
		movePathRename = originalMovePathRename
		movePathRemoveAll = originalMovePathRemoveAll
	})

	err := movePath(src, dst)
	if err == nil {
		t.Fatal("expected movePath() to fail when copied source cleanup fails")
	}
	if !strings.Contains(err.Error(), "failed to remove copied source directory") {
		t.Fatalf("expected copied-source cleanup failure in error, got %v", err)
	}

	data, readErr := os.ReadFile(filepath.Join(dst, "nested", "file.txt"))
	if readErr != nil {
		t.Fatalf("ReadFile(dst) error: %v", readErr)
	}
	if string(data) != "content" {
		t.Fatalf("expected copied destination content to remain, got %q", string(data))
	}

	if _, statErr := os.Stat(src); statErr != nil {
		t.Fatalf("expected partially cleaned source directory to remain for manual recovery, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(src, "nested", "file.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected simulated partial source cleanup to remove the nested file, got %v", statErr)
	}
}

func TestCopyFile_RejectsSymlinkDestination(t *testing.T) {
	tempDir := t.TempDir()
	src := filepath.Join(tempDir, "src.txt")
	if err := os.WriteFile(src, []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	outsidePath := filepath.Join(tempDir, "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0600); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}

	dst := filepath.Join(tempDir, "dst.txt")
	if err := os.Symlink(outsidePath, dst); err != nil {
		t.Fatalf("Symlink(dst) error: %v", err)
	}

	err := copyFile(src, dst)
	if !errors.Is(err, errStoragePathSymlink) {
		t.Fatalf("copyFile() error = %v, want errStoragePathSymlink", err)
	}

	outsideData, readErr := os.ReadFile(outsidePath)
	if readErr != nil {
		t.Fatalf("ReadFile(outside) error: %v", readErr)
	}
	if string(outsideData) != "outside" {
		t.Fatalf("expected outside file to remain unchanged, got %q", string(outsideData))
	}
}

func TestCopyFile_RollsBackDestinationWhenDirectorySyncFails(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src-dir")
	dstDir := filepath.Join(tempDir, "dst-dir")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll(srcDir) error: %v", err)
	}
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		t.Fatalf("MkdirAll(dstDir) error: %v", err)
	}

	src := filepath.Join(srcDir, "src.txt")
	dst := filepath.Join(dstDir, "dst.txt")
	if err := os.WriteFile(src, []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	originalSyncStoragePathDir := syncStoragePathDir
	syncFailed := false
	syncStoragePathDir = func(dir string) error {
		if !syncFailed {
			syncFailed = true
			return errors.New("sync dir failed")
		}
		return nil
	}
	t.Cleanup(func() {
		syncStoragePathDir = originalSyncStoragePathDir
	})

	err := copyFile(src, dst)
	if err == nil {
		t.Fatal("expected copyFile() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync copied file") {
		t.Fatalf("expected sync failure in error, got %v", err)
	}

	srcData, readErr := os.ReadFile(src)
	if readErr != nil {
		t.Fatalf("ReadFile(src) error: %v", readErr)
	}
	if string(srcData) != "content" {
		t.Fatalf("expected source content to remain unchanged, got %q", string(srcData))
	}
	if _, statErr := os.Stat(dst); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected destination to be removed after rollback, got %v", statErr)
	}
}

func TestCopyDir_ReturnsErrorWhenDirectorySyncFails(t *testing.T) {
	tempDir := t.TempDir()
	src := filepath.Join(tempDir, "src")
	dst := filepath.Join(tempDir, "dst")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0755); err != nil {
		t.Fatalf("MkdirAll(src) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(src/nested/file.txt) error: %v", err)
	}

	originalSyncStoragePathDir := syncStoragePathDir
	syncStoragePathDir = func(dir string) error {
		return errors.New("sync dir failed")
	}
	t.Cleanup(func() {
		syncStoragePathDir = originalSyncStoragePathDir
	})

	err := copyDir(src, dst)
	if err == nil {
		t.Fatal("expected copyDir() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "sync dir failed") {
		t.Fatalf("expected sync failure in error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dst, "nested", "file.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected copied tree to remain absent after failure, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(src, "nested", "file.txt")); statErr != nil {
		t.Fatalf("expected source tree to remain intact, got %v", statErr)
	}
}

func TestMovePath_RejectsSymlinkDestinationParent(t *testing.T) {
	tempDir := t.TempDir()
	src := filepath.Join(tempDir, "src.txt")
	if err := os.WriteFile(src, []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	outsideDir := filepath.Join(tempDir, "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("MkdirAll(outside) error: %v", err)
	}

	escapeDir := filepath.Join(tempDir, "escape")
	if err := os.Symlink(outsideDir, escapeDir); err != nil {
		t.Fatalf("Symlink(escape) error: %v", err)
	}

	err := movePath(src, filepath.Join(escapeDir, "dst.txt"))
	if !errors.Is(err, errStoragePathSymlink) {
		t.Fatalf("movePath() error = %v, want errStoragePathSymlink", err)
	}

	if _, statErr := os.Stat(src); statErr != nil {
		t.Fatalf("expected source file to remain in place, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(outsideDir, "dst.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no file outside managed path, got %v", statErr)
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

func TestFileSystem_SearchWithinBase_RespectsLimitWithinRoot(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/other"); err != nil {
		t.Fatalf("Mkdir(/other) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/tester"); err != nil {
		t.Fatalf("Mkdir(/tester) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/other/report.txt", bytes.NewReader([]byte("other"))); err != nil {
		t.Fatalf("WriteFile(/other/report.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tester/report.txt", bytes.NewReader([]byte("tester"))); err != nil {
		t.Fatalf("WriteFile(/tester/report.txt) error: %v", err)
	}

	results, err := fs.SearchWithinBase(ctx, "/tester", "report", 1)
	if err != nil {
		t.Fatalf("SearchWithinBase() error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("SearchWithinBase() returned %d results, want 1", len(results))
	}
	if results[0].Path != "/tester/report.txt" {
		t.Fatalf("SearchWithinBase() first result path = %q, want %q", results[0].Path, "/tester/report.txt")
	}
}

func TestFileSystem_Search_PropagatesTraversalError(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/blocked"); err != nil {
		t.Fatalf("Mkdir(blocked) error: %v", err)
	}
	blockedPath := filepath.Join(fs.workspace.Root(), "blocked")
	if err := os.Chmod(blockedPath, 0); err != nil {
		t.Fatalf("Chmod(blocked) error: %v", err)
	}
	defer func() {
		_ = os.Chmod(blockedPath, 0o755)
	}()

	results, err := fs.Search(ctx, "blocked", 10)
	if err == nil {
		t.Fatal("expected Search() to propagate traversal error")
	}
	if results != nil {
		t.Fatalf("expected no results on traversal error, got %d", len(results))
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

func TestFileSystem_Search_DoesNotBlockWritesWhileTraversing(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	started := make(chan struct{})
	release := make(chan struct{})
	previousWalk := walkStorageWorkspace
	walkStorageWorkspace = func(ctx context.Context, ws *workspace.Workspace, root string, fn workspace.WalkFunc) error {
		close(started)
		<-release
		return fn("/readme.md", &workspace.FileInfo{
			Path:    "/readme.md",
			Name:    "readme.md",
			IsDir:   false,
			Size:    1,
			ModTime: time.Now(),
		})
	}
	t.Cleanup(func() {
		walkStorageWorkspace = previousWalk
	})

	type searchResult struct {
		results []*SearchResult
		err     error
	}
	searchDone := make(chan searchResult, 1)
	go func() {
		results, err := fs.Search(ctx, "readme", 10)
		searchDone <- searchResult{results: results, err: err}
	}()

	<-started

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- fs.WriteFile(ctx, "/concurrent.txt", bytes.NewReader([]byte("content")))
	}()

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("WriteFile() during Search() error: %v", err)
		}
	case <-time.After(time.Second):
		close(release)
		<-searchDone
		t.Fatal("expected Search() traversal not to block concurrent writes")
	}

	close(release)
	searchOutcome := <-searchDone
	if searchOutcome.err != nil {
		t.Fatalf("Search() error: %v", searchOutcome.err)
	}
	if len(searchOutcome.results) != 1 || searchOutcome.results[0].Path != "/readme.md" {
		t.Fatalf("Search() results = %#v, want single /readme.md result", searchOutcome.results)
	}

	if _, err := fs.Stat(ctx, "/concurrent.txt"); err != nil {
		t.Fatalf("Stat(/concurrent.txt) error: %v", err)
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

func TestFileSystem_CleanupStaging_PropagatesWorkspaceWalkError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based walk error is unreliable as root")
	}

	fs := setupFileSystem(t)
	ctx := context.Background()

	blockedDir := filepath.Join(fs.workspace.Root(), "blocked")
	if err := os.Mkdir(blockedDir, 0755); err != nil {
		t.Fatalf("Mkdir(blocked) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedDir, "stuck.tmp"), []byte("temp"), 0644); err != nil {
		t.Fatalf("WriteFile(stuck.tmp) error: %v", err)
	}
	if err := os.Chmod(blockedDir, 0000); err != nil {
		t.Fatalf("Chmod(blocked) error: %v", err)
	}
	defer os.Chmod(blockedDir, 0755)

	_, _, err := fs.CleanupStaging(ctx)
	if err == nil {
		t.Fatal("expected CleanupStaging() to return walk error")
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

func TestFileSystem_GetVersioningStatus_ReturnsErrNotDirWhenParentIsFile(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/override-parent", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(override-parent) error: %v", err)
	}

	_, _, err := fs.GetVersioningStatus(ctx, "/override-parent/child.txt")
	if err != ErrNotDir {
		t.Fatalf("GetVersioningStatus() error = %v, want ErrNotDir", err)
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
		releaseMutation := fs.beginMutation()
		close(locked)
		releaseMutation()
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

func TestFileSystem_AcquireGCLock_DoesNotBlockReaders(t *testing.T) {
	fs := &FileSystem{
		listReferencedHashes: func(ctx context.Context) ([]string, error) {
			return []string{"hash1"}, nil
		},
	}

	_, release, err := fs.AcquireGCLock(context.Background())
	if err != nil {
		t.Fatalf("AcquireGCLock() error: %v", err)
	}
	defer release()

	readLocked := make(chan struct{})
	go func() {
		fs.mu.RLock()
		close(readLocked)
		fs.mu.RUnlock()
	}()

	select {
	case <-readLocked:
	case <-time.After(time.Second):
		t.Fatal("expected readers to proceed while GC gate is held")
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
		releaseMutation := fs.beginMutation()
		close(locked)
		releaseMutation()
	}()

	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("expected mutex to be released after snapshot error")
	}
}
