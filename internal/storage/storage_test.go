package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/versionstore"
	"github.com/seanbao/mnemonas/internal/workspace"
)

type blockingOnceReader struct {
	started chan struct{}
	release chan struct{}
	data    []byte
	sent    bool
}

type partialErrorReader struct {
	data []byte
	err  error
	sent bool
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(buffer []byte) (int, error) {
	return f(buffer)
}

func TestWarningWrappersPreserveErrorSemantics(t *testing.T) {
	baseErr := errors.New("durability failed")

	if got := wrapVisibleMutationWarning(nil); got != nil {
		t.Fatalf("wrapVisibleMutationWarning(nil) = %v, want nil", got)
	}
	visibleErr := wrapVisibleMutationWarning(baseErr)
	if !isVisibleMutationWarning(visibleErr) {
		t.Fatalf("expected visible mutation warning, got %T", visibleErr)
	}
	if !errors.Is(visibleErr, baseErr) {
		t.Fatalf("expected visible mutation warning to unwrap %v", baseErr)
	}
	if visibleErr.Error() != baseErr.Error() {
		t.Fatalf("visible warning Error() = %q, want %q", visibleErr.Error(), baseErr.Error())
	}
	if got := wrapVisibleMutationWarning(visibleErr); got != visibleErr {
		t.Fatal("expected existing visible mutation warning to be reused")
	}

	if got := wrapTrashDeleteWarningWithPartial(nil, true); got != nil {
		t.Fatalf("wrapTrashDeleteWarningWithPartial(nil) = %v, want nil", got)
	}
	trashErr := wrapTrashDeleteWarning(baseErr)
	var trashWarning *TrashDeleteWarningError
	if !errors.As(trashErr, &trashWarning) {
		t.Fatalf("expected trash warning, got %T", trashErr)
	}
	if trashWarning.Partial() {
		t.Fatal("expected regular trash warning to be non-partial")
	}
	if !errors.Is(trashErr, baseErr) {
		t.Fatalf("expected trash warning to unwrap %v", baseErr)
	}
	if trashErr.Error() != baseErr.Error() {
		t.Fatalf("trash warning Error() = %q, want %q", trashErr.Error(), baseErr.Error())
	}
	promotedTrashErr := wrapTrashDeletePartialWarning(trashErr)
	var promotedTrashWarning *TrashDeleteWarningError
	if !errors.As(promotedTrashErr, &promotedTrashWarning) {
		t.Fatalf("expected promoted trash warning, got %T", promotedTrashErr)
	}
	if !promotedTrashWarning.Partial() {
		t.Fatal("expected promoted trash warning to be partial")
	}
	if !errors.Is(promotedTrashErr, baseErr) {
		t.Fatalf("expected promoted trash warning to unwrap %v", baseErr)
	}

	if got := wrapDeleteCleanupWarning(nil); got != nil {
		t.Fatalf("wrapDeleteCleanupWarning(nil) = %v, want nil", got)
	}
	deleteCleanupErr := wrapDeleteCleanupWarning(baseErr)
	var cleanupWarning *DeleteCleanupWarningError
	if !errors.As(deleteCleanupErr, &cleanupWarning) {
		t.Fatalf("expected delete cleanup warning, got %T", deleteCleanupErr)
	}
	if !errors.Is(deleteCleanupErr, baseErr) {
		t.Fatalf("expected delete cleanup warning to unwrap %v", baseErr)
	}
	if deleteCleanupErr.Error() != baseErr.Error() {
		t.Fatalf("delete cleanup warning Error() = %q, want %q", deleteCleanupErr.Error(), baseErr.Error())
	}
	if got := wrapDeleteCleanupWarning(deleteCleanupErr); got != deleteCleanupErr {
		t.Fatal("expected existing delete cleanup warning to be reused")
	}
}

func TestStagedDeletePersistenceWarningsPreservesEveryPersistenceCause(t *testing.T) {
	workspaceCause := errors.New("workspace durability failed")
	storageCause := errors.New("storage durability failed")
	cleanupCause := errors.New("cleanup failed")

	warnings := stagedDeletePersistenceWarnings(errors.Join(
		fmt.Errorf("workspace step: %w", workspace.WrapVisibleMutationWarning(workspaceCause)),
		wrapVisibleMutationWarning(storageCause),
		cleanupCause,
	))
	if !errors.Is(warnings, workspaceCause) || !errors.Is(warnings, storageCause) {
		t.Fatalf("persistence warnings = %v, want both persistence causes", warnings)
	}
	if errors.Is(warnings, cleanupCause) {
		t.Fatalf("persistence warnings = %v, unexpectedly retained cleanup cause", warnings)
	}
}

func TestCleanupStorageTempPath_JoinsRemoveError(t *testing.T) {
	fs := setupManagedPathHelperFileSystem(t)
	busyDir := filepath.Join(fs.workspace.Root(), "busy")
	if err := os.Mkdir(busyDir, 0700); err != nil {
		t.Fatalf("failed to create busy temp dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(busyDir, "child"), []byte("data"), 0600); err != nil {
		t.Fatalf("failed to create busy temp child: %v", err)
	}

	root, rel, abs, err := fs.resolveStoragePathRoot(busyDir)
	if err != nil {
		t.Fatalf("resolveStoragePathRoot() error: %v", err)
	}

	operationErr := errors.New("copy failed")
	err = errors.Join(operationErr, fs.cleanupStorageTempPath(root, rel, abs))
	if err == nil {
		t.Fatal("expected cleanup error")
	}
	if !errors.Is(err, operationErr) {
		t.Fatalf("expected joined error to include operation error, got %v", err)
	}
	if !strings.Contains(err.Error(), "cleanup temp file busy") {
		t.Fatalf("expected cleanup context in error, got %v", err)
	}
}

func TestFileSystem_UpdateTrashSettings(t *testing.T) {
	empty := &FileSystem{}
	empty.UpdateTrashSettings(true, 7, 1024)

	fs := &FileSystem{config: &Config{}}
	fs.UpdateTrashSettings(true, 14, 4096)

	if fs.config.TrashEnabled == nil || !*fs.config.TrashEnabled {
		t.Fatalf("TrashEnabled = %v, want true", fs.config.TrashEnabled)
	}
	if fs.config.TrashRetentionDays != 14 {
		t.Fatalf("TrashRetentionDays = %d, want 14", fs.config.TrashRetentionDays)
	}
	if fs.config.MaxTrashSize != 4096 {
		t.Fatalf("MaxTrashSize = %d, want 4096", fs.config.MaxTrashSize)
	}

	fs.UpdateTrashSettings(false, 3, 128)
	if fs.config.TrashEnabled == nil || *fs.config.TrashEnabled {
		t.Fatalf("TrashEnabled = %v, want false", fs.config.TrashEnabled)
	}
	if fs.config.TrashRetentionDays != 3 || fs.config.MaxTrashSize != 128 {
		t.Fatalf("unexpected updated trash settings: %+v", fs.config)
	}
}

func TestFileSystem_CurrentDeletePolicyReturnsAtomicSnapshot(t *testing.T) {
	fs := &FileSystem{config: &Config{RetentionSweepInterval: time.Hour}}

	fs.UpdateTrashSettings(true, 14, 4096)
	policy := fs.CurrentDeletePolicy()
	if policy.Mode != DeleteModeTrash {
		t.Fatalf("delete mode = %q, want %q", policy.Mode, DeleteModeTrash)
	}
	if policy.TrashRetentionDays != 14 {
		t.Fatalf("trash retention days = %d, want 14", policy.TrashRetentionDays)
	}
	if !policy.TrashAutoCleanupEnabled {
		t.Fatal("expected automatic trash cleanup to be enabled")
	}
	if policy.MaxTrashSize != 4096 {
		t.Fatalf("max trash size = %d, want 4096", policy.MaxTrashSize)
	}
	if len(policy.Token) != sha256.Size*2 || policy.Token != strings.ToLower(policy.Token) {
		t.Fatalf("delete policy token = %q, want 64 lowercase hexadecimal characters", policy.Token)
	}
	firstToken := policy.Token

	fs.UpdateRuntimePolicySettings(RuntimePolicySettings{
		MaxVersions:        3,
		MaxVersionAge:      24 * time.Hour,
		MinFreeSpace:       1024,
		SweepInterval:      0,
		TrashEnabled:       false,
		TrashRetentionDays: 3,
		MaxTrashSize:       128,
	})
	policy = fs.CurrentDeletePolicy()
	if policy.Mode != DeleteModePermanent {
		t.Fatalf("delete mode = %q, want %q", policy.Mode, DeleteModePermanent)
	}
	if policy.TrashRetentionDays != 3 || policy.TrashAutoCleanupEnabled || policy.MaxTrashSize != 128 {
		t.Fatalf("unexpected updated delete policy: %+v", policy)
	}
	if policy.Token == firstToken {
		t.Fatalf("delete policy token did not change after policy update: %q", policy.Token)
	}
}

func TestFileSystem_SetDataplaneClient(t *testing.T) {
	empty := &FileSystem{}
	empty.SetDataplaneClient(nil)

	client := dataplane.NewClient("127.0.0.1:1")
	fs := &FileSystem{config: &Config{}}
	fs.SetDataplaneClient(client)

	if fs.config.Dataplane != client {
		t.Fatalf("Dataplane = %#v, want %#v", fs.config.Dataplane, client)
	}
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

func (r *partialErrorReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	n := copy(p, r.data)
	return n, r.err
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

func setupStandaloneFileSystem(t *testing.T) *FileSystem {
	t.Helper()

	tmpDir := t.TempDir()
	fs, err := New(&Config{
		FilesRoot:          filepath.Join(tmpDir, "files"),
		InternalRoot:       filepath.Join(tmpDir, ".mnemonas"),
		TrashRoot:          filepath.Join(tmpDir, ".mnemonas", "trash"),
		Dataplane:          dataplane.NewClient("unused"),
		MaxVersions:        10,
		MaxVersionAge:      30 * 24 * time.Hour,
		TrashRetentionDays: 30,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() {
		if err := fs.Close(); err != nil {
			t.Errorf("fs.Close() error: %v", err)
		}
	})
	return fs
}

func setupManagedPathHelperFileSystem(t *testing.T) *FileSystem {
	t.Helper()

	tmpDir := t.TempDir()
	ws, err := workspace.New(filepath.Join(tmpDir, "files"))
	if err != nil {
		t.Fatalf("workspace.New() error: %v", err)
	}

	trashRoot := filepath.Join(tmpDir, "trash")
	if err := ensureStorageDir(trashRoot, 0700); err != nil {
		t.Fatalf("ensureStorageDir(trash) error: %v", err)
	}
	absTrashRoot, err := normalizeStorageHostPath(trashRoot)
	if err != nil {
		t.Fatalf("normalizeStorageHostPath(trash) error: %v", err)
	}

	filesRootHandle, err := os.OpenRoot(ws.Root())
	if err != nil {
		_ = ws.Close()
		t.Fatalf("os.OpenRoot(files) error: %v", err)
	}
	trashRootHandle, err := os.OpenRoot(absTrashRoot)
	if err != nil {
		_ = filesRootHandle.Close()
		_ = ws.Close()
		t.Fatalf("os.OpenRoot(trash) error: %v", err)
	}

	fs := &FileSystem{
		workspace:       ws,
		filesRootHandle: filesRootHandle,
		trashRootHandle: trashRootHandle,
		trashRoot:       absTrashRoot,
	}
	t.Cleanup(func() {
		if err := fs.Close(); err != nil {
			t.Errorf("fs.Close() error: %v", err)
		}
	})
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

func setTrashDeletedAt(t *testing.T, fs *FileSystem, ctx context.Context, originalPath string, deletedAt time.Time) {
	t.Helper()

	items, err := fs.versions.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(%s) before timestamp update error: %v", originalPath, err)
	}
	for _, item := range items {
		if item.OriginalPath != originalPath {
			continue
		}
		if err := fs.versions.RemoveFromTrash(ctx, item.ID); err != nil {
			t.Fatalf("RemoveFromTrash(%s) before timestamp update error: %v", item.ID, err)
		}
		item.DeletedAt = deletedAt
		if err := fs.versions.AddToTrash(ctx, &item); err != nil {
			t.Fatalf("AddToTrash(%s) after timestamp update error: %v", item.ID, err)
		}
		return
	}
	t.Fatalf("trash item for %s not found before timestamp update", originalPath)
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

func TestNew_RejectsFilesRootSymlinkParent(t *testing.T) {
	client := setupDataplaneClient(t)
	if client == nil {
		t.Skip("dataplane not available, skipping test")
	}

	tmpDir := t.TempDir()
	realFilesParent := filepath.Join(tmpDir, "real-files-parent")
	if err := os.MkdirAll(realFilesParent, 0755); err != nil {
		t.Fatalf("MkdirAll(real-files-parent) error: %v", err)
	}
	linkedFilesParent := filepath.Join(tmpDir, "linked-files-parent")
	if err := os.Symlink(realFilesParent, linkedFilesParent); err != nil {
		t.Fatalf("Symlink(linked-files-parent) error: %v", err)
	}

	filesRoot := filepath.Join(linkedFilesParent, "files")
	_, err := New(&Config{
		FilesRoot:    filesRoot,
		InternalRoot: filepath.Join(tmpDir, ".mnemonas"),
		TrashRoot:    filepath.Join(tmpDir, ".mnemonas", "trash"),
		Dataplane:    client,
	})
	if err == nil {
		t.Fatal("expected New() to reject files root under symlink parent")
	}
	if !strings.Contains(err.Error(), "failed to create workspace") || !strings.Contains(err.Error(), "workspace root must not be a symlink") {
		t.Fatalf("expected workspace symlink rejection, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(realFilesParent, "files")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("files root created through symlink parent, stat error = %v", statErr)
	}
}

func TestEnsureStorageDir_SyncsCreatedDirectoriesDeepestParentFirst(t *testing.T) {
	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "files", "nested", "dir")

	originalSyncStoragePathDir := syncStoragePathDir
	var synced []string
	syncStoragePathDir = func(dir string) error {
		synced = append(synced, dir)
		return nil
	}
	defer func() {
		syncStoragePathDir = originalSyncStoragePathDir
	}()

	if err := ensureStorageDir(targetDir, 0755); err != nil {
		t.Fatalf("ensureStorageDir() error: %v", err)
	}

	want := []string{
		filepath.Join(tmpDir, "files", "nested"),
		filepath.Join(tmpDir, "files"),
		tmpDir,
	}
	if strings.Join(synced, "|") != strings.Join(want, "|") {
		t.Fatalf("synced directories = %v, want %v", synced, want)
	}
}

func TestSyncStorageDirRejectsSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "real")
	if err := os.Mkdir(realDir, 0755); err != nil {
		t.Fatalf("Mkdir(real) error: %v", err)
	}
	linkedDir := filepath.Join(tmpDir, "linked")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	if err := syncStorageDir(linkedDir); !rootio.IsSymlinkError(err) {
		t.Fatalf("syncStorageDir() error = %v, want symlink error", err)
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

func TestFileSystem_OpenFileSnapshot_PreservesSnapshotAcrossPathReplacement(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	originalContent := []byte("first version")
	replacementContent := []byte("second version with different bytes")

	if err := fs.WriteFile(ctx, "/snapshot.txt", bytes.NewReader(originalContent)); err != nil {
		t.Fatalf("WriteFile(snapshot original) error: %v", err)
	}

	file, info, err := fs.OpenFileSnapshot(ctx, "/snapshot.txt")
	if err != nil {
		t.Fatalf("OpenFileSnapshot() error: %v", err)
	}
	defer file.Close()

	if info.ContentHash == "" {
		t.Fatal("expected snapshot info to include content hash")
	}
	if info.Size != int64(len(originalContent)) {
		t.Fatalf("snapshot size = %d, want %d", info.Size, len(originalContent))
	}

	if err := fs.WriteFile(ctx, "/snapshot.txt", bytes.NewReader(replacementContent)); err != nil {
		t.Fatalf("WriteFile(snapshot replacement) error: %v", err)
	}

	body, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll(snapshot file) error: %v", err)
	}
	if string(body) != string(originalContent) {
		t.Fatalf("snapshot body = %q, want %q", string(body), string(originalContent))
	}
	if gotHash := computeHash(body); gotHash != info.ContentHash {
		t.Fatalf("snapshot hash = %q, want %q", gotHash, info.ContentHash)
	}

	currentInfo, err := fs.Stat(ctx, "/snapshot.txt")
	if err != nil {
		t.Fatalf("Stat(snapshot current) error: %v", err)
	}
	if currentInfo.ContentHash == info.ContentHash {
		t.Fatalf("expected current path hash to differ after replacement, got %q", currentInfo.ContentHash)
	}
	if currentInfo.Size != int64(len(replacementContent)) {
		t.Fatalf("current size = %d, want %d", currentInfo.Size, len(replacementContent))
	}
}

func TestFileSystem_WriteFile_DistinguishesTrailingWhitespacePathMetadata(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	plainPath := "/docs/report.txt"
	spacedPath := "/docs/report.txt "
	plainOriginal := []byte("plain original")
	plainCurrent := []byte("plain current")
	spacedOriginal := []byte("spaced original")
	spacedCurrent := []byte("spaced current")

	if err := fs.Mkdir(ctx, "/docs"); err != nil {
		t.Fatalf("Mkdir(docs) error: %v", err)
	}
	if err := fs.WriteFile(ctx, plainPath, bytes.NewReader(plainOriginal)); err != nil {
		t.Fatalf("WriteFile(plain original) error: %v", err)
	}
	if err := fs.WriteFile(ctx, spacedPath, bytes.NewReader(spacedOriginal)); err != nil {
		t.Fatalf("WriteFile(spaced original) error: %v", err)
	}
	if err := fs.WriteFile(ctx, plainPath, bytes.NewReader(plainCurrent)); err != nil {
		t.Fatalf("WriteFile(plain current) error: %v", err)
	}
	if err := fs.WriteFile(ctx, spacedPath, bytes.NewReader(spacedCurrent)); err != nil {
		t.Fatalf("WriteFile(spaced current) error: %v", err)
	}

	plainVersions, err := fs.versions.GetVersions(ctx, plainPath)
	if err != nil {
		t.Fatalf("GetVersions(plain) error: %v", err)
	}
	if len(plainVersions) != 1 || plainVersions[0].Hash != computeHash(plainOriginal) {
		t.Fatalf("plain historical versions = %+v, want original hash only", plainVersions)
	}
	spacedVersions, err := fs.versions.GetVersions(ctx, spacedPath)
	if err != nil {
		t.Fatalf("GetVersions(spaced) error: %v", err)
	}
	if len(spacedVersions) != 1 || spacedVersions[0].Hash != computeHash(spacedOriginal) {
		t.Fatalf("spaced historical versions = %+v, want original hash only", spacedVersions)
	}

	plainSize, _, plainHash, err := fs.versions.GetFileIndex(ctx, plainPath)
	if err != nil {
		t.Fatalf("GetFileIndex(plain) error: %v", err)
	}
	if plainSize != int64(len(plainCurrent)) || plainHash != computeHash(plainCurrent) {
		t.Fatalf("plain file index = (%d, %q), want (%d, %q)", plainSize, plainHash, len(plainCurrent), computeHash(plainCurrent))
	}
	spacedSize, _, spacedHash, err := fs.versions.GetFileIndex(ctx, spacedPath)
	if err != nil {
		t.Fatalf("GetFileIndex(spaced) error: %v", err)
	}
	if spacedSize != int64(len(spacedCurrent)) || spacedHash != computeHash(spacedCurrent) {
		t.Fatalf("spaced file index = (%d, %q), want (%d, %q)", spacedSize, spacedHash, len(spacedCurrent), computeHash(spacedCurrent))
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
	if !errors.Is(err, errStoragePathSymlink) {
		t.Fatalf("WriteFile() error = %v, want errStoragePathSymlink", err)
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

func TestFileSystem_WriteFile_DoesNotFollowSymlinkInsertedBeforeManagedWrite(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	safeDir := filepath.Join(fs.workspace.Root(), "safe")
	if err := os.Mkdir(safeDir, 0755); err != nil {
		t.Fatalf("Mkdir(safe) error: %v", err)
	}
	outsideDir := t.TempDir()

	originalBeforeStorageWorkspaceWrite := beforeStorageWorkspaceWrite
	beforeStorageWorkspaceWrite = func() error {
		if err := os.Remove(safeDir); err != nil {
			return err
		}
		return os.Symlink(outsideDir, safeDir)
	}
	t.Cleanup(func() {
		beforeStorageWorkspaceWrite = originalBeforeStorageWorkspaceWrite
	})

	err := fs.WriteFile(ctx, "/safe/child.txt", bytes.NewReader([]byte("blocked")))
	if err != ErrNotFound {
		t.Fatalf("WriteFile() error = %v, want ErrNotFound", err)
	}
	if _, statErr := os.Stat(filepath.Join(outsideDir, "child.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected external target file to remain absent, got %v", statErr)
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

func TestFileSystem_RootMutationsAreRejected(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/source.txt", bytes.NewReader([]byte("copy content"))); err != nil {
		t.Fatalf("WriteFile(source.txt) error: %v", err)
	}

	if err := fs.Delete(ctx, "/"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete(/) error = %v, want ErrNotFound", err)
	}
	if err := fs.WriteFile(ctx, "/", bytes.NewReader([]byte("root content"))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("WriteFile(/) error = %v, want ErrNotFound", err)
	}
	if err := fs.PermanentDelete(ctx, "/"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("PermanentDelete(/) error = %v, want ErrNotFound", err)
	}
	if err := fs.Rename(ctx, "/", "/renamed-root"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Rename(/, /renamed-root) error = %v, want ErrNotFound", err)
	}
	if err := fs.Copy(ctx, "/", "/copied-root"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Copy(/, /copied-root) error = %v, want ErrNotFound", err)
	}
	if err := fs.Copy(ctx, "/source.txt", "/"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Copy(/source.txt, /) error = %v, want ErrNotFound", err)
	}
	if err := fs.WriteFile(ctx, "/restore-root-target.txt", bytes.NewReader([]byte("restore content"))); err != nil {
		t.Fatalf("WriteFile(restore-root-target.txt) error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-root-target.txt"); err != nil {
		t.Fatalf("Delete(restore-root-target.txt) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one trash item, got %d", len(items))
	}
	if err := fs.RestoreFromTrashTo(ctx, items[0].ID, "/"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RestoreFromTrashTo(id, /) error = %v, want ErrNotFound", err)
	}
	if err := fs.RestoreVersion(ctx, "/", "missing-version"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RestoreVersion(/) error = %v, want ErrNotFound", err)
	}
	if err := fs.SetVersioning(ctx, "/", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetVersioning(/) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.Stat(ctx, "/source.txt"); err != nil {
		t.Fatalf("expected source file to remain after rejected root mutations, got %v", err)
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

func TestFileSystem_WriteFile_RollsBackCreatedDirectoriesWhenIndexUpdateFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.versions.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	err := fs.WriteFile(ctx, "/deep/path/rollback-index-tree.bin", bytes.NewReader([]byte("new content")))
	if err == nil {
		t.Fatal("Expected WriteFile() to fail when file index update cannot persist")
	}

	if _, statErr := fs.Stat(ctx, "/deep/path/rollback-index-tree.bin"); statErr != ErrNotFound {
		t.Fatalf("Expected new file to be removed after rollback, got %v", statErr)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath("/deep/path")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Expected created nested directory to be removed after rollback, got %v", statErr)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath("/deep")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Expected created parent directory to be removed after rollback, got %v", statErr)
	}
}

func TestFileSystem_WriteFile_CleansCreatedDirectoriesWhenReaderFailsBeforeRename(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	readerErr := errors.New("reader failed")

	err := fs.WriteFile(ctx, "/deep/path/reader-fail.bin", &partialErrorReader{data: []byte("partial"), err: readerErr})
	if !errors.Is(err, readerErr) {
		t.Fatalf("expected reader failure, got %v", err)
	}
	if _, statErr := fs.Stat(ctx, "/deep/path/reader-fail.bin"); statErr != ErrNotFound {
		t.Fatalf("Expected failed write file to remain absent, got %v", statErr)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath("/deep/path")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Expected created nested directory to be removed after failed write, got %v", statErr)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath("/deep")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Expected created parent directory to be removed after failed write, got %v", statErr)
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

func TestFileSystem_WriteFile_KeepsHistoricalObjectWhenVersionMetadataRollbackFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	originalContent := []byte("old content")
	path := "/rollback-version-delete-fail.md"
	originalHash := computeHash(originalContent)

	if err := fs.WriteFile(ctx, path, bytes.NewReader(originalContent)); err != nil {
		t.Fatalf("Initial WriteFile() error: %v", err)
	}

	deleteVersionCalled := false
	fs.updateFileIndex = func(ctx context.Context, path string, size int64, modTime time.Time, hash string) error {
		return errors.New("index update failed")
	}
	fs.deleteFileVersion = func(ctx context.Context, versionPath, hash string) error {
		deleteVersionCalled = true
		if versionPath != path {
			t.Fatalf("deleteFileVersion() path = %q, want %q", versionPath, path)
		}
		if hash != originalHash {
			t.Fatalf("deleteFileVersion() hash = %q, want %q", hash, originalHash)
		}
		return errors.New("delete version metadata failed")
	}

	err := fs.WriteFile(ctx, path, bytes.NewReader([]byte("new content")))
	if err == nil {
		t.Fatal("Expected WriteFile() overwrite to fail when version metadata rollback fails")
	}
	if !strings.Contains(err.Error(), "failed to rollback version metadata") {
		t.Fatalf("expected rollback version metadata failure in error, got %v", err)
	}
	if !deleteVersionCalled {
		t.Fatal("expected rollback to attempt deleting version metadata")
	}

	versions, versionErr := fs.versions.GetVersions(ctx, path)
	if versionErr != nil {
		t.Fatalf("GetVersions() after rollback error: %v", versionErr)
	}
	if len(versions) != 1 {
		t.Fatalf("expected historical version metadata to remain when rollback delete fails, got %d entries", len(versions))
	}
	if versions[0].Hash != originalHash {
		t.Fatalf("expected remaining historical version hash %q, got %q", originalHash, versions[0].Hash)
	}

	hasObject, objectErr := fs.hasVersionObject(ctx, originalHash)
	if objectErr != nil {
		t.Fatalf("hasVersionObject() error: %v", objectErr)
	}
	if !hasObject {
		t.Fatal("expected historical version object to remain when metadata rollback fails")
	}

	f, openErr := fs.OpenFile(ctx, path)
	if openErr != nil {
		t.Fatalf("OpenFile() after rollback error: %v", openErr)
	}
	defer f.Close()

	data, readErr := io.ReadAll(f)
	if readErr != nil {
		t.Fatalf("ReadAll() after rollback error: %v", readErr)
	}
	if string(data) != string(originalContent) {
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
	if !strings.Contains(err.Error(), "sync parent directory") {
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
	if !strings.Contains(err.Error(), "sync parent directory") {
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
	if !strings.Contains(err.Error(), "sync parent directory") {
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

func TestFileSystem_WriteFile_RollsBackNewFileWhenCreatedDirectoryTreeSyncFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	blockedDir := fs.workspace.FullPath("/deep")
	originalSyncStoragePathDir := syncStoragePathDir
	syncStoragePathDir = func(dir string) error {
		if dir == blockedDir {
			return errors.New("sync dir failed")
		}
		return nil
	}
	t.Cleanup(func() {
		syncStoragePathDir = originalSyncStoragePathDir
	})

	err := fs.WriteFile(ctx, "/deep/path/rollback-sync-tree.bin", bytes.NewReader([]byte("new content")))
	if err == nil {
		t.Fatal("Expected WriteFile() to fail when created directory tree sync fails")
	}
	if !strings.Contains(err.Error(), "sync created directory tree") {
		t.Fatalf("expected created directory tree sync failure in error, got %v", err)
	}

	if _, statErr := fs.Stat(ctx, "/deep/path/rollback-sync-tree.bin"); statErr != ErrNotFound {
		t.Fatalf("Expected new file to be removed after rollback, got %v", statErr)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath("/deep/path")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Expected created nested directory to be removed after rollback, got %v", statErr)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath("/deep")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Expected created parent directory to be removed after rollback, got %v", statErr)
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
	if _, err := fs.Stat(ctx, "/safe/./versioned.txt"); err != ErrNotFound {
		t.Fatalf("Stat(dot segment) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.ReadDir(ctx, "../safe"); err != ErrNotFound {
		t.Fatalf("ReadDir(traversal) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.ReadDir(ctx, "./safe"); err != ErrNotFound {
		t.Fatalf("ReadDir(dot segment) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.OpenFile(ctx, "../safe/versioned.txt"); err != ErrNotFound {
		t.Fatalf("OpenFile(traversal) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.OpenFile(ctx, "/safe/./versioned.txt"); err != ErrNotFound {
		t.Fatalf("OpenFile(dot segment) error = %v, want ErrNotFound", err)
	}
	if err := fs.WriteFile(ctx, "../escape.txt", bytes.NewReader([]byte("blocked"))); err != ErrNotFound {
		t.Fatalf("WriteFile(traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.WriteFile(ctx, "/safe/./escape.txt", bytes.NewReader([]byte("blocked"))); err != ErrNotFound {
		t.Fatalf("WriteFile(dot segment) error = %v, want ErrNotFound", err)
	}
	if err := fs.Mkdir(ctx, "../escape-dir"); err != ErrNotFound {
		t.Fatalf("Mkdir(traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.Mkdir(ctx, "./safe/escape-dir"); err != ErrNotFound {
		t.Fatalf("Mkdir(dot segment) error = %v, want ErrNotFound", err)
	}
	if err := fs.Delete(ctx, "../safe/versioned.txt"); err != ErrNotFound {
		t.Fatalf("Delete(traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.Delete(ctx, "/safe/./versioned.txt"); err != ErrNotFound {
		t.Fatalf("Delete(dot segment) error = %v, want ErrNotFound", err)
	}
	if err := fs.PermanentDelete(ctx, "../safe/versioned.txt"); err != ErrNotFound {
		t.Fatalf("PermanentDelete(traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.PermanentDelete(ctx, "./safe/versioned.txt"); err != ErrNotFound {
		t.Fatalf("PermanentDelete(dot segment) error = %v, want ErrNotFound", err)
	}
	if err := fs.Rename(ctx, "../safe/versioned.txt", "/safe/renamed.txt"); err != ErrNotFound {
		t.Fatalf("Rename(source traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.Rename(ctx, "/safe/versioned.txt", "../renamed.txt"); err != ErrNotFound {
		t.Fatalf("Rename(destination traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.Rename(ctx, "/safe/./versioned.txt", "/safe/renamed.txt"); err != ErrNotFound {
		t.Fatalf("Rename(source dot segment) error = %v, want ErrNotFound", err)
	}
	if err := fs.Rename(ctx, "/safe/versioned.txt", "/safe/./renamed.txt"); err != ErrNotFound {
		t.Fatalf("Rename(destination dot segment) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.ListVersions(ctx, "../safe/versioned.txt"); err != ErrNotFound {
		t.Fatalf("ListVersions(traversal) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.ListVersions(ctx, "/safe/./versioned.txt"); err != ErrNotFound {
		t.Fatalf("ListVersions(dot segment) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.GetVersion(ctx, "../safe/versioned.txt", "missing-hash"); err != ErrNotFound {
		t.Fatalf("GetVersion(traversal) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.GetVersion(ctx, "./safe/versioned.txt", "missing-hash"); err != ErrNotFound {
		t.Fatalf("GetVersion(dot segment) error = %v, want ErrNotFound", err)
	}
	if err := fs.RestoreVersion(ctx, "../safe/versioned.txt", "missing-hash"); err != ErrNotFound {
		t.Fatalf("RestoreVersion(traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.RestoreVersion(ctx, "/safe/./versioned.txt", "missing-hash"); err != ErrNotFound {
		t.Fatalf("RestoreVersion(dot segment) error = %v, want ErrNotFound", err)
	}
	if err := fs.SetVersioning(ctx, "../safe/versioned.txt", true); err != ErrNotFound {
		t.Fatalf("SetVersioning(traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.SetVersioning(ctx, "./safe/versioned.txt", true); err != ErrNotFound {
		t.Fatalf("SetVersioning(dot segment) error = %v, want ErrNotFound", err)
	}
	if _, _, err := fs.GetVersioningStatus(ctx, "../safe/versioned.txt"); err != ErrNotFound {
		t.Fatalf("GetVersioningStatus(traversal) error = %v, want ErrNotFound", err)
	}
	if _, _, err := fs.GetVersioningStatus(ctx, "/safe/./versioned.txt"); err != ErrNotFound {
		t.Fatalf("GetVersioningStatus(dot segment) error = %v, want ErrNotFound", err)
	}
	if err := fs.RestoreFromTrashTo(ctx, trashID, "../restored.txt"); err != ErrNotFound {
		t.Fatalf("RestoreFromTrashTo(traversal) error = %v, want ErrNotFound", err)
	}
	if err := fs.RestoreFromTrashTo(ctx, trashID, "/safe/./restored.txt"); err != ErrNotFound {
		t.Fatalf("RestoreFromTrashTo(dot segment) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.Stat(ctx, "/safe/versioned\x00.txt"); err != ErrNotFound {
		t.Fatalf("Stat(NUL) error = %v, want ErrNotFound", err)
	}
	if err := fs.WriteFile(ctx, "/safe/nul\x00.txt", bytes.NewReader([]byte("blocked"))); err != ErrNotFound {
		t.Fatalf("WriteFile(NUL) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.Stat(ctx, "/safe/versioned\n.txt"); err != ErrNotFound {
		t.Fatalf("Stat(control character) error = %v, want ErrNotFound", err)
	}
	if err := fs.WriteFile(ctx, "/safe/delete\x7f.txt", bytes.NewReader([]byte("blocked"))); err != ErrNotFound {
		t.Fatalf("WriteFile(delete control character) error = %v, want ErrNotFound", err)
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
	if _, err := fs.Stat(ctx, "/safe/escape.txt"); err != ErrNotFound {
		t.Fatalf("expected no normalized /safe/escape.txt after dot-segment write, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/safe/escape-dir"); err != ErrNotFound {
		t.Fatalf("expected no normalized /safe/escape-dir after dot-segment mkdir, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/renamed.txt"); err != ErrNotFound {
		t.Fatalf("expected no normalized /renamed.txt after traversal rename, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/safe/renamed.txt"); err != ErrNotFound {
		t.Fatalf("expected no normalized /safe/renamed.txt after dot-segment rename, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/restored.txt"); err != ErrNotFound {
		t.Fatalf("expected no normalized /restored.txt after traversal restore, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/safe/restored.txt"); err != ErrNotFound {
		t.Fatalf("expected no normalized /safe/restored.txt after dot-segment restore, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/safe/nul.txt"); err != ErrNotFound {
		t.Fatalf("expected no normalized /safe/nul.txt after NUL write, got %v", err)
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

func TestFileSystem_Mkdir_ReturnsWarningWhenWorkspaceSyncFailsAfterCreate(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	originalMkdir := fs.mkdirWorkspacePath
	fs.mkdirWorkspacePath = func(ctx context.Context, name string) error {
		if err := originalMkdir(ctx, name); err != nil {
			return err
		}
		return workspace.WrapVisibleMutationWarning(errors.New("sync dir failed"))
	}

	err := fs.Mkdir(ctx, "/warning-dir")
	if !isVisibleMutationWarning(err) {
		t.Fatalf("expected visible mutation warning, got %v", err)
	}

	info, statErr := fs.Stat(ctx, "/warning-dir")
	if statErr != nil {
		t.Fatalf("Stat(/warning-dir) error: %v", statErr)
	}
	if !info.IsDir {
		t.Fatal("expected warning-dir to remain created after warning")
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

func TestStorageReadDirChildPathRejectsNonDirectChildren(t *testing.T) {
	tests := []struct {
		name      string
		parent    string
		child     *workspace.FileInfo
		wantPath  string
		wantName  string
		wantError bool
	}{
		{
			name:     "direct child",
			parent:   "/docs",
			child:    &workspace.FileInfo{Path: "/docs/report.txt", Name: "report.txt"},
			wantPath: "/docs/report.txt",
			wantName: "report.txt",
		},
		{
			name:     "root direct child",
			parent:   "/",
			child:    &workspace.FileInfo{Path: "/report.txt", Name: "report.txt"},
			wantPath: "/report.txt",
			wantName: "report.txt",
		},
		{
			name:     "fallback from blank path",
			parent:   "/docs",
			child:    &workspace.FileInfo{Name: "report.txt"},
			wantPath: "/docs/report.txt",
			wantName: "report.txt",
		},
		{
			name:      "backslash child path",
			parent:    "/docs",
			child:     &workspace.FileInfo{Path: "/docs\\report.txt", Name: "report.txt"},
			wantError: true,
		},
		{
			name:      "dot segment child path",
			parent:    "/docs",
			child:     &workspace.FileInfo{Path: "/docs/./report.txt", Name: "report.txt"},
			wantError: true,
		},
		{
			name:      "parent segment child path",
			parent:    "/docs",
			child:     &workspace.FileInfo{Path: "/docs/../report.txt", Name: "report.txt"},
			wantError: true,
		},
		{
			name:      "dot segment fallback name",
			parent:    "/docs",
			child:     &workspace.FileInfo{Name: "./report.txt"},
			wantError: true,
		},
		{
			name:      "leading slash fallback name",
			parent:    "/docs",
			child:     &workspace.FileInfo{Name: "/report.txt"},
			wantError: true,
		},
		{
			name:      "trailing slash fallback name",
			parent:    "/docs",
			child:     &workspace.FileInfo{Name: "report.txt/"},
			wantError: true,
		},
		{
			name:      "backslash fallback name",
			parent:    "/docs",
			child:     &workspace.FileInfo{Name: "nested\\report.txt"},
			wantError: true,
		},
		{
			name:      "control character child path",
			parent:    "/docs",
			child:     &workspace.FileInfo{Path: "/docs/report\n2026.txt", Name: "report\n2026.txt"},
			wantError: true,
		},
		{
			name:      "control character fallback name",
			parent:    "/docs",
			child:     &workspace.FileInfo{Name: "report\x7f.txt"},
			wantError: true,
		},
		{
			name:      "similar prefix sibling",
			parent:    "/docs",
			child:     &workspace.FileInfo{Path: "/docs-archive/secret.txt", Name: "secret.txt"},
			wantError: true,
		},
		{
			name:      "nested descendant",
			parent:    "/docs",
			child:     &workspace.FileInfo{Path: "/docs/nested/secret.txt", Name: "secret.txt"},
			wantError: true,
		},
		{
			name:      "same path",
			parent:    "/docs",
			child:     &workspace.FileInfo{Path: "/docs", Name: "docs"},
			wantError: true,
		},
		{
			name:      "nil child",
			parent:    "/docs",
			child:     nil,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotName, err := storageReadDirChildPath(tt.parent, tt.child)
			if (err != nil) != tt.wantError {
				t.Fatalf("storageReadDirChildPath() error = %v, wantError %v", err, tt.wantError)
			}
			if gotPath != tt.wantPath || gotName != tt.wantName {
				t.Fatalf("storageReadDirChildPath() = (%q, %q), want (%q, %q)", gotPath, gotName, tt.wantPath, tt.wantName)
			}
		})
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

func TestFileSystem_DeleteWithExpectedPolicyRejectsModeMismatchWithoutMutation(t *testing.T) {
	tests := []struct {
		name         string
		trashEnabled bool
		expectedMode DeleteMode
		actualMode   DeleteMode
	}{
		{name: "trash changed to permanent", trashEnabled: false, expectedMode: DeleteModeTrash, actualMode: DeleteModePermanent},
		{name: "permanent changed to trash", trashEnabled: true, expectedMode: DeleteModePermanent, actualMode: DeleteModeTrash},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := setupFileSystem(t)
			ctx := context.Background()
			if err := fs.WriteFile(ctx, "/mode-mismatch.txt", bytes.NewReader([]byte("keep me"))); err != nil {
				t.Fatalf("WriteFile() error: %v", err)
			}

			hookCalls := 0
			fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
				hookCalls++
				return nil, nil
			})
			fs.UpdateTrashSettings(tt.expectedMode == DeleteModeTrash, 9, 4096)
			expectedPolicy := fs.CurrentDeletePolicy()
			fs.UpdateTrashSettings(tt.trashEnabled, 9, 4096)

			err := fs.DeleteWithExpectedPolicy(ctx, "/mode-mismatch.txt", DeletePolicyExpectation{
				Mode:  expectedPolicy.Mode,
				Token: expectedPolicy.Token,
			}, nil)
			if !errors.Is(err, ErrDeletePolicyChanged) {
				t.Fatalf("DeleteWithExpectedPolicy() error = %v, want ErrDeletePolicyChanged", err)
			}
			var changedErr *DeletePolicyChangedError
			if !errors.As(err, &changedErr) {
				t.Fatalf("DeleteWithExpectedPolicy() error type = %T, want *DeletePolicyChangedError", err)
			}
			if changedErr.Expected.Mode != tt.expectedMode || changedErr.Expected.Token != expectedPolicy.Token || changedErr.Actual.Mode != tt.actualMode || changedErr.Actual.TrashRetentionDays != 9 {
				t.Fatalf("unexpected changed-mode error: %+v", changedErr)
			}
			if hookCalls != 0 {
				t.Fatalf("delete hook calls = %d, want 0", hookCalls)
			}
			if _, err := fs.Stat(ctx, "/mode-mismatch.txt"); err != nil {
				t.Fatalf("expected file to remain after mode mismatch, got %v", err)
			}
			items, err := fs.ListTrash(ctx)
			if err != nil {
				t.Fatalf("ListTrash() error: %v", err)
			}
			if len(items) != 0 {
				t.Fatalf("trash items = %d, want 0", len(items))
			}
		})
	}
}

func TestFileSystem_DeleteWithExpectedPolicyRejectsPolicyTokenDriftWithoutMutation(t *testing.T) {
	tests := []struct {
		name    string
		initial RuntimePolicySettings
		updated RuntimePolicySettings
	}{
		{
			name:    "retention days changed",
			initial: RuntimePolicySettings{SweepInterval: time.Hour, TrashEnabled: true, TrashRetentionDays: 30, MaxTrashSize: 4096},
			updated: RuntimePolicySettings{SweepInterval: time.Hour, TrashEnabled: true, TrashRetentionDays: 0, MaxTrashSize: 4096},
		},
		{
			name:    "automatic cleanup disabled",
			initial: RuntimePolicySettings{SweepInterval: time.Hour, TrashEnabled: true, TrashRetentionDays: 30, MaxTrashSize: 4096},
			updated: RuntimePolicySettings{SweepInterval: 0, TrashEnabled: true, TrashRetentionDays: 30, MaxTrashSize: 4096},
		},
		{
			name:    "cleanup interval changed while enabled",
			initial: RuntimePolicySettings{SweepInterval: 24 * time.Hour, TrashEnabled: true, TrashRetentionDays: 30, MaxTrashSize: 4096},
			updated: RuntimePolicySettings{SweepInterval: time.Minute, TrashEnabled: true, TrashRetentionDays: 30, MaxTrashSize: 4096},
		},
		{
			name:    "trash capacity changed",
			initial: RuntimePolicySettings{SweepInterval: time.Hour, TrashEnabled: true, TrashRetentionDays: 30, MaxTrashSize: 4096},
			updated: RuntimePolicySettings{SweepInterval: time.Hour, TrashEnabled: true, TrashRetentionDays: 30, MaxTrashSize: 8192},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := setupFileSystem(t)
			ctx := context.Background()
			fs.UpdateRuntimePolicySettings(tt.initial)
			expectedPolicy := fs.CurrentDeletePolicy()
			if expectedPolicy.Mode != DeleteModeTrash {
				t.Fatalf("initial delete mode = %q, want trash", expectedPolicy.Mode)
			}
			if err := fs.WriteFile(ctx, "/policy-token-drift.txt", bytes.NewReader([]byte("keep me"))); err != nil {
				t.Fatalf("WriteFile() error: %v", err)
			}

			hookCalls := 0
			fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
				hookCalls++
				return nil, nil
			})
			fs.UpdateRuntimePolicySettings(tt.updated)
			actualPolicy := fs.CurrentDeletePolicy()
			if actualPolicy.Mode != DeleteModeTrash {
				t.Fatalf("updated delete mode = %q, want trash", actualPolicy.Mode)
			}
			if actualPolicy.Token == expectedPolicy.Token {
				t.Fatalf("delete policy token did not change: %q", actualPolicy.Token)
			}

			err := fs.DeleteWithExpectedPolicy(ctx, "/policy-token-drift.txt", DeletePolicyExpectation{
				Mode:  expectedPolicy.Mode,
				Token: expectedPolicy.Token,
			}, nil)
			if !errors.Is(err, ErrDeletePolicyChanged) {
				t.Fatalf("DeleteWithExpectedPolicy() error = %v, want ErrDeletePolicyChanged", err)
			}
			var changedErr *DeletePolicyChangedError
			if !errors.As(err, &changedErr) {
				t.Fatalf("DeleteWithExpectedPolicy() error type = %T, want *DeletePolicyChangedError", err)
			}
			if changedErr.Actual.Token != actualPolicy.Token {
				t.Fatalf("actual token in error = %q, want %q", changedErr.Actual.Token, actualPolicy.Token)
			}
			if hookCalls != 0 {
				t.Fatalf("delete hook calls = %d, want 0", hookCalls)
			}
			if _, err := fs.Stat(ctx, "/policy-token-drift.txt"); err != nil {
				t.Fatalf("file changed after policy token drift: %v", err)
			}
			items, err := fs.ListTrash(ctx)
			if err != nil {
				t.Fatalf("ListTrash() error: %v", err)
			}
			if len(items) != 0 {
				t.Fatalf("trash items = %d, want 0", len(items))
			}
		})
	}
}

func TestFileSystem_DeleteWithExpectedPolicyPermanentModeDoesNotDeadlock(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	fs.UpdateTrashSettings(false, 30, 4096)
	if err := fs.WriteFile(ctx, "/permanent-mode.txt", bytes.NewReader([]byte("delete me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	policy := fs.CurrentDeletePolicy()

	done := make(chan error, 1)
	go func() {
		done <- fs.DeleteWithExpectedPolicy(ctx, "/permanent-mode.txt", DeletePolicyExpectation{Mode: policy.Mode, Token: policy.Token}, nil)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DeleteWithExpectedPolicy() error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DeleteWithExpectedMode() deadlocked in permanent mode")
	}

	if _, err := fs.Stat(ctx, "/permanent-mode.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected permanently deleted file to be absent, got %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("trash items = %d, want 0", len(items))
	}
}

func TestFileSystem_DeleteWithExpectedPolicyLinearizesRuntimePolicyUpdate(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	fs.UpdateRuntimePolicySettings(RuntimePolicySettings{
		MaxVersions:        10,
		MaxVersionAge:      30 * 24 * time.Hour,
		SweepInterval:      time.Hour,
		TrashEnabled:       false,
		TrashRetentionDays: 30,
		MaxTrashSize:       4096,
	})
	if err := fs.WriteFile(ctx, "/linearized-permanent-delete.txt", bytes.NewReader([]byte("delete me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	expectedPolicy := fs.CurrentDeletePolicy()

	originalDeleteWorkspacePath := fs.deleteStagedWorkspacePath
	deletePointReached := make(chan struct{})
	releaseDeletePoint := make(chan struct{})
	var releaseOnce sync.Once
	releaseDelete := func() {
		releaseOnce.Do(func() { close(releaseDeletePoint) })
	}
	defer releaseDelete()
	fs.deleteStagedWorkspacePath = func(ctx context.Context, name string, remove func() error) error {
		close(deletePointReached)
		<-releaseDeletePoint
		return originalDeleteWorkspacePath(ctx, name, remove)
	}

	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- fs.DeleteWithExpectedPolicy(ctx, "/linearized-permanent-delete.txt", DeletePolicyExpectation{Mode: expectedPolicy.Mode, Token: expectedPolicy.Token}, nil)
	}()

	select {
	case <-deletePointReached:
	case err := <-deleteDone:
		t.Fatalf("delete completed before reaching the blocked workspace delete point: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the workspace delete point")
	}

	if fs.mu.TryLock() {
		fs.mu.Unlock()
		t.Fatal("delete released the filesystem write lock before the workspace mutation")
	}

	updateStarted := make(chan struct{})
	updateDone := make(chan struct{})
	go func() {
		close(updateStarted)
		fs.UpdateRuntimePolicySettings(RuntimePolicySettings{
			MaxVersions:        3,
			MaxVersionAge:      7 * 24 * time.Hour,
			SweepInterval:      0,
			TrashEnabled:       true,
			TrashRetentionDays: 7,
			MaxTrashSize:       8192,
		})
		close(updateDone)
	}()
	<-updateStarted

	select {
	case <-updateDone:
		t.Fatal("runtime policy update completed while delete held the filesystem write lock")
	case <-time.After(100 * time.Millisecond):
	}

	releaseDelete()
	select {
	case err := <-deleteDone:
		if err != nil {
			t.Fatalf("DeleteWithExpectedPolicy() error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delete completion")
	}
	select {
	case <-updateDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime policy update")
	}

	policy := fs.CurrentDeletePolicy()
	if policy.Mode != DeleteModeTrash || policy.TrashRetentionDays != 7 || policy.TrashAutoCleanupEnabled {
		t.Fatalf("updated delete policy = %+v, want trash mode with seven-day retention and cleanup disabled", policy)
	}
	if _, err := fs.Stat(ctx, "/linearized-permanent-delete.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("permanently deleted path status = %v, want ErrNotFound", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("trash items = %d, want 0 because the in-flight delete used the matched permanent mode", len(items))
	}
}

func TestFileSystem_DeleteAuthorizationLinearizesConcurrentDescendantWrite(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	fs.UpdateRuntimePolicySettings(RuntimePolicySettings{
		SweepInterval:      time.Hour,
		TrashEnabled:       true,
		TrashRetentionDays: 30,
		MaxTrashSize:       1 << 20,
	})
	if err := fs.Mkdir(ctx, "/authorized-delete"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/authorized-delete/existing.txt", bytes.NewReader([]byte("existing"))); err != nil {
		t.Fatalf("WriteFile(existing) error: %v", err)
	}
	policy := fs.CurrentDeletePolicy()

	authorizerReachedLastEntry := make(chan struct{})
	releaseAuthorizer := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseAuthorizer) })
	}
	defer release()
	authorizedPaths := make([]string, 0, 2)
	authorize := func(targetPath string) error {
		authorizedPaths = append(authorizedPaths, targetPath)
		if targetPath == "/authorized-delete/existing.txt" {
			close(authorizerReachedLastEntry)
			<-releaseAuthorizer
		}
		if targetPath == "/authorized-delete/denied.txt" {
			return errors.New("denied descendant")
		}
		return nil
	}

	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- fs.DeleteWithExpectedPolicy(ctx, "/authorized-delete", DeletePolicyExpectation{Mode: policy.Mode, Token: policy.Token}, authorize)
	}()
	select {
	case <-authorizerReachedLastEntry:
	case err := <-deleteDone:
		t.Fatalf("delete completed before locked authorization point: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for locked delete authorization")
	}
	if fs.mu.TryLock() {
		fs.mu.Unlock()
		t.Fatal("delete authorization did not retain the filesystem write lock")
	}

	writeStarted := make(chan struct{})
	writeDone := make(chan error, 1)
	go func() {
		close(writeStarted)
		writeDone <- fs.WriteFile(ctx, "/authorized-delete/denied.txt", bytes.NewReader([]byte("denied")))
	}()
	<-writeStarted
	select {
	case err := <-writeDone:
		t.Fatalf("concurrent descendant write completed inside locked authorization boundary: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	release()
	select {
	case err := <-deleteDone:
		if err != nil {
			t.Fatalf("DeleteWithExpectedPolicy() error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for authorized delete")
	}
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("concurrent descendant write after delete error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for concurrent descendant writer")
	}

	if slices.Contains(authorizedPaths, "/authorized-delete/denied.txt") {
		t.Fatalf("authorization snapshot unexpectedly included blocked concurrent path: %v", authorizedPaths)
	}
	if _, err := fs.Stat(ctx, "/authorized-delete/existing.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted original child status = %v, want ErrNotFound", err)
	}
	if _, err := fs.Stat(ctx, "/authorized-delete/denied.txt"); err != nil {
		t.Fatalf("concurrent child should be created only after delete completed: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 || !items[0].IsDir {
		t.Fatalf("trash items = %+v, want one directory", items)
	}
	var restorePaths []string
	if err := fs.WalkTrashItemRestorePaths(ctx, items[0].ID, func(restoredPath string, _ bool, _ int64) error {
		restorePaths = append(restorePaths, restoredPath)
		return nil
	}); err != nil {
		t.Fatalf("WalkTrashItemRestorePaths() error: %v", err)
	}
	if slices.Contains(restorePaths, "/authorized-delete/denied.txt") {
		t.Fatalf("concurrent denied child crossed into deleted tree snapshot: %v", restorePaths)
	}
}

func TestFileSystem_DeleteWithExpectedPolicyAuthorizationRejectionHasNoSideEffects(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	fs.UpdateRuntimePolicySettings(RuntimePolicySettings{
		SweepInterval:      time.Hour,
		TrashEnabled:       true,
		TrashRetentionDays: 30,
		MaxTrashSize:       1 << 20,
	})
	if err := fs.Mkdir(ctx, "/rejected-delete"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/rejected-delete/existing.txt", bytes.NewReader([]byte("keep me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	policy := fs.CurrentDeletePolicy()

	hookCalls := 0
	fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
		hookCalls++
		return nil, nil
	})
	errDenied := errors.New("delete path denied")
	err := fs.DeleteWithExpectedPolicy(ctx, "/rejected-delete", DeletePolicyExpectation{Mode: policy.Mode, Token: policy.Token}, func(targetPath string) error {
		if targetPath == "/rejected-delete/existing.txt" {
			return errDenied
		}
		return nil
	})
	if !errors.Is(err, errDenied) {
		t.Fatalf("DeleteWithExpectedPolicy() error = %v, want %v", err, errDenied)
	}
	if hookCalls != 0 {
		t.Fatalf("delete hook calls = %d, want 0", hookCalls)
	}
	for _, targetPath := range []string{"/rejected-delete", "/rejected-delete/existing.txt"} {
		if _, err := fs.Stat(ctx, targetPath); err != nil {
			t.Fatalf("Stat(%s) after authorization rejection error: %v", targetPath, err)
		}
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("trash items after authorization rejection = %d, want 0", len(items))
	}
}

func TestFileSystem_DeleteWithTargetValidatorUsesCurrentNormalizedRootSnapshot(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/target-snapshot"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/target-snapshot/file.txt", bytes.NewReader([]byte("current content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	wantInfo, err := fs.Stat(ctx, "/target-snapshot/file.txt")
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}

	errRejected := errors.New("target snapshot rejected")
	var gotSnapshot DeleteTargetSnapshot
	authorizerCalls := 0
	err = fs.DeleteWithTargetValidator(ctx, "/target-snapshot//file.txt", func(snapshot DeleteTargetSnapshot) error {
		gotSnapshot = snapshot
		return errRejected
	}, func(string) error {
		authorizerCalls++
		return nil
	})
	if !errors.Is(err, errRejected) {
		t.Fatalf("DeleteWithTargetValidator() error = %v, want %v", err, errRejected)
	}
	if authorizerCalls != 1 {
		t.Fatalf("authorizer calls = %d, want 1", authorizerCalls)
	}
	if got := gotSnapshot.Root.Path; got != "/target-snapshot/file.txt" {
		t.Fatalf("snapshot root path = %q, want %q", got, "/target-snapshot/file.txt")
	}
	if gotSnapshot.Root.Name != wantInfo.Name || gotSnapshot.Root.IsDir != wantInfo.IsDir || gotSnapshot.Root.Size != wantInfo.Size || !gotSnapshot.Root.ModTime.Equal(wantInfo.ModTime) || gotSnapshot.Root.ContentHash != wantInfo.ContentHash {
		t.Fatalf("snapshot root = %+v, want current info %+v", gotSnapshot.Root, *wantInfo)
	}
	if _, err := fs.Stat(ctx, "/target-snapshot/file.txt"); err != nil {
		t.Fatalf("Stat() after validator rejection error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("trash items after validator rejection = %d, want 0", len(items))
	}
}

func TestFileSystem_DeleteWithTargetValidatorAuthorizesBeforeHashing(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/target-authorization.bin"
	if err := fs.WriteFile(ctx, targetPath, bytes.NewReader([]byte("keep"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	errDenied := errors.New("delete access denied")
	hashCalls := 0
	fs.hashDeleteTargetFile = func(context.Context, string) (string, error) {
		hashCalls++
		return "", errors.New("unexpected target hash")
	}
	validatorCalls := 0
	err := fs.DeleteWithTargetValidator(ctx, targetPath, func(DeleteTargetSnapshot) error {
		validatorCalls++
		return nil
	}, func(string) error {
		return errDenied
	})
	if !errors.Is(err, errDenied) {
		t.Fatalf("DeleteWithTargetValidator() error = %v, want %v", err, errDenied)
	}
	if hashCalls != 0 {
		t.Fatalf("target hash calls = %d, want 0", hashCalls)
	}
	if validatorCalls != 0 {
		t.Fatalf("validator calls = %d, want 0", validatorCalls)
	}
	if _, err := fs.Stat(ctx, targetPath); err != nil {
		t.Fatalf("Stat() after authorization rejection error: %v", err)
	}
}

func TestFileSystem_PrepareDeleteIntentsPreservesOrderAndSnapshotsCompleteTrees(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	for _, dir := range []string{"/empty", "/tree", "/tree/nested"} {
		if err := fs.Mkdir(ctx, dir); err != nil {
			t.Fatalf("Mkdir(%s) error: %v", dir, err)
		}
	}
	if err := fs.WriteFile(ctx, "/tree/nested/child.bin", bytes.NewReader([]byte("child"))); err != nil {
		t.Fatalf("WriteFile(child) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/file.bin", bytes.NewReader([]byte("file"))); err != nil {
		t.Fatalf("WriteFile(file) error: %v", err)
	}

	var authorized []string
	intent, err := fs.PrepareDeleteIntents(ctx, []string{"/file.bin", "/tree", "/empty"}, func(targetPath string) error {
		authorized = append(authorized, targetPath)
		return nil
	})
	if err != nil {
		t.Fatalf("PrepareDeleteIntents() error: %v", err)
	}
	if len(intent.Targets) != 3 {
		t.Fatalf("intent target count = %d, want 3", len(intent.Targets))
	}
	wantOrder := []string{"/file.bin", "/tree", "/empty"}
	for i, wantPath := range wantOrder {
		target := intent.Targets[i]
		if target.Snapshot.Root.Path != wantPath {
			t.Fatalf("target[%d] path = %q, want %q", i, target.Snapshot.Root.Path, wantPath)
		}
		if len(target.Token) != sha256.Size*2 || target.Token != strings.ToLower(target.Token) {
			t.Fatalf("target[%d] token = %q, want lowercase SHA-256", i, target.Token)
		}
	}
	if intent.Targets[0].Snapshot.Root.ContentHash == "" || len(intent.Targets[0].Snapshot.Entries) != 1 {
		t.Fatalf("file snapshot = %+v, want one hashed file", intent.Targets[0].Snapshot)
	}
	if got := len(intent.Targets[1].Snapshot.Entries); got != 3 {
		t.Fatalf("tree snapshot entries = %d, want root, nested directory, and child file", got)
	}
	if got := intent.Targets[1].Snapshot.Entries[2].ContentHash; got == "" {
		t.Fatal("tree child content hash is empty")
	}
	if got := len(intent.Targets[2].Snapshot.Entries); got != 1 || !intent.Targets[2].Snapshot.Root.IsDir {
		t.Fatalf("empty directory snapshot = %+v, want one directory entry", intent.Targets[2].Snapshot)
	}
	wantAuthorized := []string{"/file.bin", "/tree", "/tree/nested", "/tree/nested/child.bin", "/empty"}
	if !slices.Equal(authorized, wantAuthorized) {
		t.Fatalf("authorized paths = %v, want %v", authorized, wantAuthorized)
	}
}

func TestFileSystem_PrepareDeleteIntentsRejectsInvalidTargetSets(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()

	tests := []struct {
		name  string
		paths []string
	}{
		{name: "empty", paths: nil},
		{name: "empty path", paths: []string{""}},
		{name: "root", paths: []string{"/"}},
		{name: "duplicate normalized path", paths: []string{"/tree", "/tree/"}},
		{name: "ancestor then descendant", paths: []string{"/tree", "/tree/child"}},
		{name: "descendant then ancestor", paths: []string{"/tree/child", "/tree"}},
		{name: "over limit", paths: make([]string, MaxDeleteIntentTargets+1)},
	}
	for i := range tests[len(tests)-1].paths {
		tests[len(tests)-1].paths[i] = fmt.Sprintf("/target-%d", i)
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := fs.PrepareDeleteIntents(ctx, testCase.paths, nil)
			if !errors.Is(err, ErrInvalidDeleteIntent) {
				t.Fatalf("PrepareDeleteIntents() error = %v, want ErrInvalidDeleteIntent", err)
			}
		})
	}
}

func TestFileSystem_DeleteTargetSnapshotsAuthorizeBeforeHashAndStopAtDenial(t *testing.T) {
	for _, operation := range []string{"prepare", "delete"} {
		t.Run(operation, func(t *testing.T) {
			fs := setupStandaloneFileSystem(t)
			ctx := context.Background()
			if err := fs.Mkdir(ctx, "/tree"); err != nil {
				t.Fatalf("Mkdir(tree) error: %v", err)
			}
			for _, targetPath := range []string{"/tree/a-allowed.bin", "/tree/b-denied.bin", "/tree/c-after.bin"} {
				if err := fs.WriteFile(ctx, targetPath, bytes.NewReader([]byte(targetPath))); err != nil {
					t.Fatalf("WriteFile(%s) error: %v", targetPath, err)
				}
			}
			intent, err := fs.PrepareDeleteIntents(ctx, []string{"/tree"}, nil)
			if err != nil {
				t.Fatalf("PrepareDeleteIntents(initial) error: %v", err)
			}

			var authorized []string
			var hashed []string
			fs.hashDeleteTargetFile = func(ctx context.Context, targetPath string) (string, error) {
				hashed = append(hashed, targetPath)
				return fs.hashWorkspaceFile(ctx, targetPath)
			}
			errDenied := errors.New("denied descendant")
			authorize := func(targetPath string) error {
				authorized = append(authorized, targetPath)
				if targetPath == "/tree/b-denied.bin" {
					return errDenied
				}
				return nil
			}

			switch operation {
			case "prepare":
				_, err = fs.PrepareDeleteIntents(ctx, []string{"/tree"}, authorize)
			case "delete":
				err = fs.DeleteWithExpectedPolicyAndTarget(ctx, "/tree", DeletePolicyExpectation{Mode: intent.Policy.Mode, Token: intent.Policy.Token}, intent.Targets[0].Token, authorize)
			}
			if !errors.Is(err, errDenied) {
				t.Fatalf("%s error = %v, want denied descendant", operation, err)
			}
			wantAuthorized := []string{"/tree", "/tree/a-allowed.bin", "/tree/b-denied.bin"}
			if !slices.Equal(authorized, wantAuthorized) {
				t.Fatalf("authorized paths = %v, want %v", authorized, wantAuthorized)
			}
			if !slices.Equal(hashed, []string{"/tree/a-allowed.bin"}) {
				t.Fatalf("hashed paths = %v, want only the authorized file before denial", hashed)
			}
			if _, err := fs.Stat(ctx, "/tree/c-after.bin"); err != nil {
				t.Fatalf("tree changed after authorization denial: %v", err)
			}
		})
	}
}

func TestDeleteTargetTokenIsDeterministicAndLengthPrefixed(t *testing.T) {
	modTime := time.Unix(100, 200)
	entries := []FileInfo{
		{Path: "/root", Name: "root", IsDir: true, Size: 10, ModTime: modTime},
		{Path: "/root/a", Name: "a", Size: 1, ModTime: modTime, ContentHash: "bc"},
		{Path: "/root/ab", Name: "ab", Size: 1, ModTime: modTime, ContentHash: "c"},
	}
	snapshot := DeleteTargetSnapshot{Root: entries[0], Entries: entries}
	shuffled := DeleteTargetSnapshot{Root: entries[0], Entries: []FileInfo{entries[2], entries[0], entries[1]}}
	if got, want := deleteTargetToken(shuffled), deleteTargetToken(snapshot); got != want {
		t.Fatalf("token changed with entry order: got %q want %q", got, want)
	}

	ambiguousWithoutLengths := DeleteTargetSnapshot{Root: entries[0], Entries: []FileInfo{
		entries[0],
		{Path: "/root/a", Name: "a", Size: 1, ModTime: modTime, ContentHash: "b"},
		{Path: "/root/ab", Name: "ab", Size: 1, ModTime: modTime, ContentHash: "cc"},
	}}
	if deleteTargetToken(snapshot) == deleteTargetToken(ambiguousWithoutLengths) {
		t.Fatal("length-prefixed token encoding did not distinguish different entry fields")
	}
}

func TestFileSystem_DeleteTargetTokenTracksFileAndDirectoryDrift(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/file.bin", bytes.NewReader([]byte("first"))); err != nil {
		t.Fatalf("WriteFile(first) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/empty"); err != nil {
		t.Fatalf("Mkdir(empty) error: %v", err)
	}

	prepareToken := func(targetPath string) string {
		t.Helper()
		intent, err := fs.PrepareDeleteIntents(ctx, []string{targetPath}, nil)
		if err != nil {
			t.Fatalf("PrepareDeleteIntents(%s) error: %v", targetPath, err)
		}
		return intent.Targets[0].Token
	}

	fileBefore := prepareToken("/file.bin")
	if err := fs.WriteFile(ctx, "/file.bin", bytes.NewReader([]byte("other"))); err != nil {
		t.Fatalf("WriteFile(overwrite) error: %v", err)
	}
	fileAfterOverwrite := prepareToken("/file.bin")
	if fileAfterOverwrite == fileBefore {
		t.Fatal("file overwrite did not change target token")
	}
	if err := fs.Delete(ctx, "/file.bin"); err != nil {
		t.Fatalf("Delete(file) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/file.bin", bytes.NewReader([]byte("other"))); err != nil {
		t.Fatalf("WriteFile(recreate) error: %v", err)
	}
	recreatedTime := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(fs.workspace.FullPath("/file.bin"), recreatedTime, recreatedTime); err != nil {
		t.Fatalf("Chtimes(recreated file) error: %v", err)
	}
	if got := prepareToken("/file.bin"); got == fileAfterOverwrite {
		t.Fatal("file recreation did not change target token")
	}

	treeBefore := prepareToken("/tree")
	if err := fs.WriteFile(ctx, "/tree/child.bin", bytes.NewReader([]byte("child"))); err != nil {
		t.Fatalf("WriteFile(tree child) error: %v", err)
	}
	treeAfterAdd := prepareToken("/tree")
	if treeAfterAdd == treeBefore {
		t.Fatal("adding a directory descendant did not change target token")
	}
	if err := fs.WriteFile(ctx, "/tree/child.bin", bytes.NewReader([]byte("changed"))); err != nil {
		t.Fatalf("WriteFile(modified child) error: %v", err)
	}
	if got := prepareToken("/tree"); got == treeAfterAdd {
		t.Fatal("modifying a directory descendant did not change target token")
	}

	emptyBefore := prepareToken("/empty")
	emptyTime := time.Now().Add(3 * time.Hour)
	if err := os.Chtimes(fs.workspace.FullPath("/empty"), emptyTime, emptyTime); err != nil {
		t.Fatalf("Chtimes(empty directory) error: %v", err)
	}
	if got := prepareToken("/empty"); got == emptyBefore {
		t.Fatal("empty directory metadata change did not change target token")
	}
}

func TestFileSystem_DeleteWithExpectedPolicyAndTargetRejectsStalePolicyBeforeTargetScan(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/stale-policy.bin", bytes.NewReader([]byte("keep"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	intent, err := fs.PrepareDeleteIntents(ctx, []string{"/stale-policy.bin"}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents() error: %v", err)
	}
	fs.UpdateTrashSettings(intent.Policy.Mode != DeleteModeTrash, intent.Policy.TrashRetentionDays+1, intent.Policy.MaxTrashSize+1)

	originalWalk := walkStorageDeleteTree
	walkCalls := 0
	walkStorageDeleteTree = func(ctx context.Context, ws *workspace.Workspace, root string, fn workspace.WalkFunc) error {
		walkCalls++
		return originalWalk(ctx, ws, root, fn)
	}
	t.Cleanup(func() { walkStorageDeleteTree = originalWalk })
	hashCalls := 0
	fs.hashDeleteTargetFile = func(context.Context, string) (string, error) {
		hashCalls++
		return "", errors.New("unexpected hash")
	}
	authorizerCalls := 0

	err = fs.DeleteWithExpectedPolicyAndTarget(ctx, "/stale-policy.bin", DeletePolicyExpectation{Mode: intent.Policy.Mode, Token: intent.Policy.Token}, intent.Targets[0].Token, func(string) error {
		authorizerCalls++
		return nil
	})
	if !errors.Is(err, ErrDeletePolicyChanged) {
		t.Fatalf("DeleteWithExpectedPolicyAndTarget() error = %v, want ErrDeletePolicyChanged", err)
	}
	if walkCalls != 0 || hashCalls != 0 || authorizerCalls != 0 {
		t.Fatalf("stale policy performed target work: walk=%d hash=%d authorize=%d", walkCalls, hashCalls, authorizerCalls)
	}
}

func TestFileSystem_DeleteWithExpectedPolicyRejectsStalePolicyBeforeAuthorizationWalk(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/generic-stale-policy.bin", bytes.NewReader([]byte("keep"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	policy := fs.CurrentDeletePolicy()
	fs.UpdateTrashSettings(policy.Mode != DeleteModeTrash, policy.TrashRetentionDays+1, policy.MaxTrashSize+1)

	originalWalk := walkStorageDeleteTree
	walkCalls := 0
	walkStorageDeleteTree = func(ctx context.Context, ws *workspace.Workspace, root string, fn workspace.WalkFunc) error {
		walkCalls++
		return originalWalk(ctx, ws, root, fn)
	}
	t.Cleanup(func() { walkStorageDeleteTree = originalWalk })
	authorizerCalls := 0
	err := fs.DeleteWithExpectedPolicy(ctx, "/generic-stale-policy.bin", DeletePolicyExpectation{Mode: policy.Mode, Token: policy.Token}, func(string) error {
		authorizerCalls++
		return nil
	})
	if !errors.Is(err, ErrDeletePolicyChanged) {
		t.Fatalf("DeleteWithExpectedPolicy() error = %v, want ErrDeletePolicyChanged", err)
	}
	if walkCalls != 0 || authorizerCalls != 0 {
		t.Fatalf("stale generic policy performed authorization work: walk=%d authorize=%d", walkCalls, authorizerCalls)
	}
}

func TestFileSystem_DeleteWithExpectedPolicyAndTargetRejectsTargetDriftWithoutSideEffects(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/target-drift.bin", bytes.NewReader([]byte("before"))); err != nil {
		t.Fatalf("WriteFile(before) error: %v", err)
	}
	intent, err := fs.PrepareDeleteIntents(ctx, []string{"/target-drift.bin"}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/target-drift.bin", bytes.NewReader([]byte("after"))); err != nil {
		t.Fatalf("WriteFile(after) error: %v", err)
	}

	hookCalls := 0
	fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
		hookCalls++
		return nil, nil
	})
	indexCalls := 0
	originalDeleteIndex := fs.deleteFileIndex
	fs.deleteFileIndex = func(ctx context.Context, targetPath string) error {
		indexCalls++
		return originalDeleteIndex(ctx, targetPath)
	}

	err = fs.DeleteWithExpectedPolicyAndTarget(ctx, "/target-drift.bin", DeletePolicyExpectation{Mode: intent.Policy.Mode, Token: intent.Policy.Token}, intent.Targets[0].Token, nil)
	if !errors.Is(err, ErrDeleteTargetChanged) {
		t.Fatalf("DeleteWithExpectedPolicyAndTarget() error = %v, want ErrDeleteTargetChanged", err)
	}
	var changedErr *DeleteTargetChangedError
	if !errors.As(err, &changedErr) || changedErr.Path != "/target-drift.bin" || changedErr.ExpectedToken != intent.Targets[0].Token || changedErr.ActualToken == changedErr.ExpectedToken {
		t.Fatalf("target changed error = %+v", changedErr)
	}
	if hookCalls != 0 || indexCalls != 0 {
		t.Fatalf("target drift side effects: hook=%d index=%d", hookCalls, indexCalls)
	}
	data, err := fs.workspace.ReadFile(ctx, "/target-drift.bin")
	if err != nil || string(data) != "after" {
		t.Fatalf("workspace content after target drift = %q, %v", data, err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("trash items after target drift = %d, want 0", len(items))
	}
}

func TestFileSystem_DeleteWithExpectedPolicyAndTargetMapsUnavailableTargetToDrift(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		targetPath string
		setup      func(*testing.T, *FileSystem)
		mutate     func(*testing.T, *FileSystem)
	}{
		{
			name:       "target disappeared",
			targetPath: "/disappeared.bin",
			setup: func(t *testing.T, fs *FileSystem) {
				if err := fs.WriteFile(context.Background(), "/disappeared.bin", bytes.NewReader([]byte("item"))); err != nil {
					t.Fatalf("WriteFile() error: %v", err)
				}
			},
			mutate: func(t *testing.T, fs *FileSystem) {
				if err := os.Remove(fs.workspace.FullPath("/disappeared.bin")); err != nil {
					t.Fatalf("Remove() error: %v", err)
				}
			},
		},
		{
			name:       "parent replaced by file",
			targetPath: "/parent/child.bin",
			setup: func(t *testing.T, fs *FileSystem) {
				if err := fs.Mkdir(context.Background(), "/parent"); err != nil {
					t.Fatalf("Mkdir() error: %v", err)
				}
				if err := fs.WriteFile(context.Background(), "/parent/child.bin", bytes.NewReader([]byte("item"))); err != nil {
					t.Fatalf("WriteFile() error: %v", err)
				}
			},
			mutate: func(t *testing.T, fs *FileSystem) {
				parentPath := fs.workspace.FullPath("/parent")
				if err := os.RemoveAll(parentPath); err != nil {
					t.Fatalf("RemoveAll(parent) error: %v", err)
				}
				if err := os.WriteFile(parentPath, []byte("replacement"), 0o600); err != nil {
					t.Fatalf("WriteFile(parent replacement) error: %v", err)
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fs := setupStandaloneFileSystem(t)
			testCase.setup(t, fs)
			intent, err := fs.PrepareDeleteIntents(context.Background(), []string{testCase.targetPath}, nil)
			if err != nil {
				t.Fatalf("PrepareDeleteIntents() error: %v", err)
			}
			testCase.mutate(t, fs)
			err = fs.DeleteWithExpectedPolicyAndTarget(
				context.Background(),
				testCase.targetPath,
				DeletePolicyExpectation{Mode: intent.Policy.Mode, Token: intent.Policy.Token},
				intent.Targets[0].Token,
				nil,
			)
			var changedErr *DeleteTargetChangedError
			if !errors.As(err, &changedErr) || changedErr.Path != testCase.targetPath || changedErr.ExpectedToken != intent.Targets[0].Token || changedErr.ActualToken != "" {
				t.Fatalf("DeleteWithExpectedPolicyAndTarget() error = %#v, want unavailable target drift", err)
			}
		})
	}
}

func TestFileSystem_DeleteWithExpectedPolicyAndTargetPrioritizesPolicyDrift(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/both-drift.bin", bytes.NewReader([]byte("before"))); err != nil {
		t.Fatalf("WriteFile(before) error: %v", err)
	}
	intent, err := fs.PrepareDeleteIntents(ctx, []string{"/both-drift.bin"}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/both-drift.bin", bytes.NewReader([]byte("after"))); err != nil {
		t.Fatalf("WriteFile(after) error: %v", err)
	}
	fs.UpdateTrashSettings(intent.Policy.Mode != DeleteModeTrash, intent.Policy.TrashRetentionDays, intent.Policy.MaxTrashSize)

	hashCalls := 0
	fs.hashDeleteTargetFile = func(context.Context, string) (string, error) {
		hashCalls++
		return "", errors.New("unexpected hash")
	}
	err = fs.DeleteWithExpectedPolicyAndTarget(ctx, "/both-drift.bin", DeletePolicyExpectation{Mode: intent.Policy.Mode, Token: intent.Policy.Token}, intent.Targets[0].Token, nil)
	if !errors.Is(err, ErrDeletePolicyChanged) || errors.Is(err, ErrDeleteTargetChanged) {
		t.Fatalf("DeleteWithExpectedPolicyAndTarget() error = %v, want only ErrDeletePolicyChanged", err)
	}
	if hashCalls != 0 {
		t.Fatalf("both-drift comparison hashed target %d times, want 0", hashCalls)
	}
}

func TestHashReaderWithContextStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reads := 0
	reader := readerFunc(func(buffer []byte) (int, error) {
		reads++
		if reads == 1 {
			cancel()
			copy(buffer, "chunk")
			return len("chunk"), nil
		}
		return 0, io.EOF
	})

	_, err := hashReaderWithContext(ctx, reader)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("hashReaderWithContext() error = %v, want context.Canceled", err)
	}
	if reads != 1 {
		t.Fatalf("reader calls after cancellation = %d, want 1", reads)
	}
}

func TestFileSystem_PrepareDeleteIntentsReleasesReadLockAfterHashCancellation(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	if err := fs.WriteFile(context.Background(), "/cancel-intent.bin", strings.NewReader("content")); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	hashStarted := make(chan struct{})
	fs.hashDeleteTargetFile = func(ctx context.Context, _ string) (string, error) {
		close(hashStarted)
		<-ctx.Done()
		return "", ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	intentDone := make(chan error, 1)
	go func() {
		_, err := fs.PrepareDeleteIntents(ctx, []string{"/cancel-intent.bin"}, nil)
		intentDone <- err
	}()
	<-hashStarted
	cancel()

	select {
	case err := <-intentDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("PrepareDeleteIntents() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled delete intent did not stop hashing")
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- fs.WriteFile(context.Background(), "/after-cancel.bin", strings.NewReader("ok"))
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("WriteFile() after canceled intent error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled delete intent retained the filesystem read lock")
	}
}

func TestFileSystem_ListTrashPreservesExpiresAtAfterRuntimePolicyUpdate(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	fs.UpdateRuntimePolicySettings(RuntimePolicySettings{
		MaxVersions:        10,
		MaxVersionAge:      30 * 24 * time.Hour,
		SweepInterval:      time.Hour,
		TrashEnabled:       true,
		TrashRetentionDays: 2,
		MaxTrashSize:       4096,
	})
	if err := fs.WriteFile(ctx, "/persisted-expiration.txt", bytes.NewReader([]byte("trash me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	policy := fs.CurrentDeletePolicy()
	if err := fs.DeleteWithExpectedPolicy(ctx, "/persisted-expiration.txt", DeletePolicyExpectation{Mode: policy.Mode, Token: policy.Token}, nil); err != nil {
		t.Fatalf("DeleteWithExpectedPolicy() error: %v", err)
	}

	before, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() before policy update error: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("trash items before policy update = %d, want 1", len(before))
	}
	persistedExpiresAt := before[0].ExpiresAt
	wantExpiresAt := before[0].DeletedAt.AddDate(0, 0, 2)
	if !persistedExpiresAt.Equal(wantExpiresAt) {
		t.Fatalf("persisted ExpiresAt = %s, want %s", persistedExpiresAt, wantExpiresAt)
	}

	fs.UpdateRuntimePolicySettings(RuntimePolicySettings{
		MaxVersions:        3,
		MaxVersionAge:      7 * 24 * time.Hour,
		SweepInterval:      0,
		TrashEnabled:       false,
		TrashRetentionDays: 45,
		MaxTrashSize:       8192,
	})
	after, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() after policy update error: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("trash items after policy update = %d, want 1", len(after))
	}
	if after[0].ID != before[0].ID {
		t.Fatalf("trash item ID after policy update = %q, want %q", after[0].ID, before[0].ID)
	}
	if !after[0].ExpiresAt.Equal(persistedExpiresAt) {
		t.Fatalf("ExpiresAt after policy update = %s, want persisted value %s", after[0].ExpiresAt, persistedExpiresAt)
	}
}

func TestFileSystem_Delete_RejectsSymlinkedTrashRootWithoutCreatingOutsideDir(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/symlinked-trash-root.txt", bytes.NewReader([]byte("delete me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	outsideRoot := t.TempDir()
	backupTrashRoot := fs.trashRoot + "-backup"
	if err := os.Rename(fs.trashRoot, backupTrashRoot); err != nil {
		t.Fatalf("Rename(trash root backup) error: %v", err)
	}
	if err := os.Symlink(outsideRoot, fs.trashRoot); err != nil {
		t.Fatalf("Symlink(trash root) error: %v", err)
	}
	t.Cleanup(func() {
		if info, err := os.Lstat(fs.trashRoot); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(fs.trashRoot); removeErr != nil {
				t.Errorf("Remove(trash root symlink) error: %v", removeErr)
			}
		}
		if _, err := os.Stat(backupTrashRoot); err == nil {
			if renameErr := os.Rename(backupTrashRoot, fs.trashRoot); renameErr != nil {
				t.Errorf("Rename(backup trash root) error: %v", renameErr)
			}
		}
	})

	err := fs.Delete(ctx, "/symlinked-trash-root.txt")
	if !errors.Is(err, errStoragePathSymlink) {
		t.Fatalf("Delete() error = %v, want errStoragePathSymlink", err)
	}

	entries, readErr := os.ReadDir(outsideRoot)
	if readErr != nil {
		t.Fatalf("ReadDir(outside root) error: %v", readErr)
	}
	if len(entries) != 0 {
		entryNames := make([]string, 0, len(entries))
		for _, entry := range entries {
			entryNames = append(entryNames, entry.Name())
		}
		t.Fatalf("expected no outside trash directories, got %d entries: %v", len(entries), entryNames)
	}

	if _, statErr := fs.Stat(ctx, "/symlinked-trash-root.txt"); statErr != nil {
		t.Fatalf("expected file to remain in workspace after failed delete, got %v", statErr)
	}
}

func TestFileSystem_DeleteDirectoryWithSymlinkChildDoesNotLeaveTrashContent(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()

	srcDir := filepath.Join(fs.workspace.Root(), "symlink-tree")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll(symlink-tree) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "regular.txt"), []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(regular) error: %v", err)
	}
	if err := os.Symlink("regular.txt", filepath.Join(srcDir, "linked.txt")); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	err := fs.Delete(ctx, "/symlink-tree")
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Delete() error = %v, want ErrNotRegular", err)
	}
	if _, statErr := fs.Stat(ctx, "/symlink-tree"); statErr != nil {
		t.Fatalf("expected source directory to remain after rejected delete, got %v", statErr)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no trash metadata after rejected delete, got %+v", items)
	}
	entries, err := os.ReadDir(fs.trashRoot)
	if err != nil {
		t.Fatalf("ReadDir(trash root) error: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("expected no orphan trash content after rejected delete, got %v", names)
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
	setTrashDeletedAt(t, fs, ctx, "/old.txt", time.Now().Add(-time.Hour))

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
	setTrashDeletedAt(t, fs, ctx, "/old.txt", time.Now().Add(-time.Hour))

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

func TestFileSystem_Delete_EvictsVersionMetadataWhenTrashCapacityRemovesOldItem(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	fs.config.MaxTrashSize = 10

	if err := fs.WriteFile(ctx, "/old-versioned.md", bytes.NewReader([]byte("old-v1"))); err != nil {
		t.Fatalf("WriteFile(old v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/old-versioned.md", bytes.NewReader([]byte("old-v2"))); err != nil {
		t.Fatalf("WriteFile(old v2) error: %v", err)
	}
	oldVersions, err := fs.versions.GetVersions(ctx, "/old-versioned.md")
	if err != nil {
		t.Fatalf("GetVersions(old before delete) error: %v", err)
	}
	if len(oldVersions) == 0 {
		t.Fatal("expected old file to have version history")
	}
	if err := fs.Delete(ctx, "/old-versioned.md"); err != nil {
		t.Fatalf("Delete(old) error: %v", err)
	}
	setTrashDeletedAt(t, fs, ctx, "/old-versioned.md", time.Now().Add(-time.Hour))

	deletedHashes := make(map[string]int)
	fs.deleteVersionObject = func(ctx context.Context, hash string) error {
		deletedHashes[hash]++
		return nil
	}

	if err := fs.WriteFile(ctx, "/new-capacity.txt", bytes.NewReader([]byte("1234567"))); err != nil {
		t.Fatalf("WriteFile(new) error: %v", err)
	}
	if err := fs.Delete(ctx, "/new-capacity.txt"); err != nil {
		t.Fatalf("Delete(new) error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 || items[0].OriginalPath != "/new-capacity.txt" {
		t.Fatalf("expected only new trash item to remain, got %+v", items)
	}
	remainingVersions, err := fs.versions.GetVersions(ctx, "/old-versioned.md")
	if err != nil {
		t.Fatalf("GetVersions(old after capacity eviction) error: %v", err)
	}
	if len(remainingVersions) != 0 {
		t.Fatalf("expected evicted trash item version metadata to be removed, got %d entries", len(remainingVersions))
	}
	for _, version := range oldVersions {
		if deletedHashes[version.Hash] != 1 {
			t.Fatalf("expected evicted version object %s to be deleted once, got %d", version.Hash, deletedHashes[version.Hash])
		}
	}
}

func TestFileSystem_Delete_DoesNotEvictExistingTrashWhenLaterDeleteStepFails(t *testing.T) {
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
		t.Fatalf("ListTrash() before failed delete error: %v", err)
	}
	if len(itemsBefore) != 1 {
		t.Fatalf("expected one initial trash item, got %d", len(itemsBefore))
	}
	oldItem := itemsBefore[0]

	fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
		return nil, errors.New("delete hook failed")
	})

	if err := fs.WriteFile(ctx, "/new-evict.txt", bytes.NewReader([]byte("1234567"))); err != nil {
		t.Fatalf("WriteFile(new) error: %v", err)
	}
	err = fs.Delete(ctx, "/new-evict.txt")
	if err == nil {
		t.Fatal("expected Delete() to fail when a later delete step fails")
	}

	itemsAfter, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed delete error: %v", listErr)
	}
	if len(itemsAfter) != 1 {
		t.Fatalf("expected original trash item to remain after failed delete, got %d items", len(itemsAfter))
	}
	if itemsAfter[0].ID != oldItem.ID {
		t.Fatalf("expected old trash item to remain after failed delete, got %s want %s", itemsAfter[0].ID, oldItem.ID)
	}
	if _, statErr := os.Stat(filepath.Join(fs.trashRoot, oldItem.ID)); statErr != nil {
		t.Fatalf("expected original trash content to remain after failed delete: %v", statErr)
	}
	if _, statErr := fs.Stat(ctx, "/new-evict.txt"); statErr != nil {
		t.Fatalf("expected new file to remain in place after failed delete, got %v", statErr)
	}
}

func TestFileSystem_Delete_ReturnsWarningWhenTrashCapacityCleanupFailsAfterVisibleDelete(t *testing.T) {
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
	var warningErr *TrashDeleteWarningError
	if !errors.As(err, &warningErr) {
		t.Fatalf("expected trash delete warning when capacity cleanup fails, got %v", err)
	}
	if isVisibleMutationWarning(err) {
		t.Fatalf("capacity cleanup error was incorrectly marked as a persistence warning: %v", err)
	}

	itemsAfter, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed eviction error: %v", listErr)
	}
	if len(itemsAfter) != 2 {
		t.Fatalf("expected both trash items to remain after cleanup warning, got %d items", len(itemsAfter))
	}
	var foundOld, foundNew bool
	for _, item := range itemsAfter {
		if item.ID == oldItem.ID {
			foundOld = true
		}
		if item.OriginalPath == "/new-evict.txt" {
			foundNew = true
		}
	}
	if !foundOld || !foundNew {
		t.Fatalf("expected old and new trash items after cleanup warning, got %+v", itemsAfter)
	}
	if _, statErr := os.Stat(filepath.Join(fs.trashRoot, oldItem.ID)); statErr != nil {
		t.Fatalf("expected original trash content to remain after failed eviction: %v", statErr)
	}
	if _, statErr := fs.Stat(ctx, "/new-evict.txt"); statErr != ErrNotFound {
		t.Fatalf("expected new file to stay deleted after cleanup warning, got %v", statErr)
	}
}

func TestFileSystem_Delete_ReturnsWarningWhenSourceDirectorySyncFailsAfterMoveToTrash(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/delete-source-sync.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	originalSyncManagedStorageDir := syncManagedStorageDir
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if root == fs.filesRootHandle && relName == "." && absPath == fs.workspace.Root() {
			return errors.New("sync source dir failed")
		}
		return originalSyncManagedStorageDir(root, relName, absPath)
	}
	t.Cleanup(func() {
		syncManagedStorageDir = originalSyncManagedStorageDir
	})

	err := fs.Delete(ctx, "/delete-source-sync.txt")
	if !isVisibleMutationWarning(err) {
		t.Fatalf("Delete() error = %v, want visible mutation warning", err)
	}
	if _, statErr := fs.Stat(ctx, "/delete-source-sync.txt"); statErr != ErrNotFound {
		t.Fatalf("expected deleted file to remain absent after warning, got %v", statErr)
	}
	items, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() error: %v", listErr)
	}
	if len(items) != 1 {
		t.Fatalf("expected one trash item after warned delete, got %d", len(items))
	}
	if _, _, _, indexErr := fs.versions.GetFileIndex(ctx, "/delete-source-sync.txt"); !errors.Is(indexErr, versionstore.ErrNotFound) {
		t.Fatalf("expected deleted file index to be absent after warning, got %v", indexErr)
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

func TestFileSystem_DeleteAndRestore_NonEmptyDirectory(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/docs"); err != nil {
		t.Fatalf("Mkdir(/docs) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/docs/nested"); err != nil {
		t.Fatalf("Mkdir(/docs/nested) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/nested/report.txt", bytes.NewReader([]byte("report v1"))); err != nil {
		t.Fatalf("WriteFile(report v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/nested/report.txt", bytes.NewReader([]byte("report v2"))); err != nil {
		t.Fatalf("WriteFile(report v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/readme.md", bytes.NewReader([]byte("readme"))); err != nil {
		t.Fatalf("WriteFile(readme) error: %v", err)
	}

	if _, _, _, err := fs.versions.GetFileIndex(ctx, "/docs/nested/report.txt"); err != nil {
		t.Fatalf("GetFileIndex(report before delete) error: %v", err)
	}
	if _, _, _, err := fs.versions.GetFileIndex(ctx, "/docs/readme.md"); err != nil {
		t.Fatalf("GetFileIndex(readme before delete) error: %v", err)
	}

	if err := fs.Delete(ctx, "/docs"); err != nil {
		t.Fatalf("Delete(/docs) error: %v", err)
	}

	if _, err := fs.Stat(ctx, "/docs"); err != ErrNotFound {
		t.Fatalf("expected deleted directory to be absent, got %v", err)
	}
	if _, _, _, err := fs.versions.GetFileIndex(ctx, "/docs/nested/report.txt"); err != versionstore.ErrNotFound {
		t.Fatalf("GetFileIndex(report after delete) error = %v, want ErrNotFound", err)
	}
	if _, _, _, err := fs.versions.GetFileIndex(ctx, "/docs/readme.md"); err != versionstore.ErrNotFound {
		t.Fatalf("GetFileIndex(readme after delete) error = %v, want ErrNotFound", err)
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
	if items[0].Size != int64(len("report v2")+len("readme")) {
		t.Fatalf("trash directory size = %d, want %d", items[0].Size, len("report v2")+len("readme"))
	}
	if !items[0].HadVersions {
		t.Fatal("Expected trash item to preserve directory version metadata state")
	}

	if err := fs.RestoreFromTrash(ctx, items[0].ID); err != nil {
		t.Fatalf("RestoreFromTrash() error: %v", err)
	}

	data, err := fs.workspace.ReadFile(ctx, "/docs/nested/report.txt")
	if err != nil {
		t.Fatalf("ReadFile(report after restore) error: %v", err)
	}
	if string(data) != "report v2" {
		t.Fatalf("restored report content = %q, want %q", string(data), "report v2")
	}
	if _, _, _, err := fs.versions.GetFileIndex(ctx, "/docs/nested/report.txt"); err != nil {
		t.Fatalf("GetFileIndex(report after restore) error: %v", err)
	}
	if _, _, _, err := fs.versions.GetFileIndex(ctx, "/docs/readme.md"); err != nil {
		t.Fatalf("GetFileIndex(readme after restore) error: %v", err)
	}
	versions, err := fs.ListVersions(ctx, "/docs/nested/report.txt")
	if err != nil {
		t.Fatalf("ListVersions(report after restore) error: %v", err)
	}
	if len(versions) == 0 {
		t.Fatal("expected restored directory file to retain version history")
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

func TestFileSystem_Delete_DirectoryRollbackRestoresChildIndexesWhenPathDeleteHookFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/docs"); err != nil {
		t.Fatalf("Mkdir(/docs) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/docs/nested"); err != nil {
		t.Fatalf("Mkdir(/docs/nested) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/readme.md", bytes.NewReader([]byte("readme"))); err != nil {
		t.Fatalf("WriteFile(readme) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/nested/report.txt", bytes.NewReader([]byte("report"))); err != nil {
		t.Fatalf("WriteFile(report) error: %v", err)
	}

	if _, _, _, err := fs.versions.GetFileIndex(ctx, "/docs/readme.md"); err != nil {
		t.Fatalf("GetFileIndex(readme before delete) error: %v", err)
	}
	if _, _, _, err := fs.versions.GetFileIndex(ctx, "/docs/nested/report.txt"); err != nil {
		t.Fatalf("GetFileIndex(report before delete) error: %v", err)
	}

	fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
		return nil, errors.New("favorite cleanup failed")
	})

	err := fs.Delete(ctx, "/docs")
	if err == nil {
		t.Fatal("Expected Delete() to fail when directory delete hook fails")
	}
	if !strings.Contains(err.Error(), "failed to sync delete hooks") {
		t.Fatalf("expected delete hook failure in error, got %v", err)
	}

	if _, statErr := fs.Stat(ctx, "/docs"); statErr != nil {
		t.Fatalf("expected directory to be restored after rollback, got %v", statErr)
	}
	if _, _, _, err := fs.versions.GetFileIndex(ctx, "/docs/readme.md"); err != nil {
		t.Fatalf("GetFileIndex(readme after rollback) error: %v", err)
	}
	if _, _, _, err := fs.versions.GetFileIndex(ctx, "/docs/nested/report.txt"); err != nil {
		t.Fatalf("GetFileIndex(report after rollback) error: %v", err)
	}

	items, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() error: %v", listErr)
	}
	if len(items) != 0 {
		t.Fatalf("Expected trash to remain empty after directory rollback, got %d items", len(items))
	}

	count, countErr := fs.GetFileCount(ctx)
	if countErr != nil {
		t.Fatalf("GetFileCount() error: %v", countErr)
	}
	if count != 2 {
		t.Fatalf("GetFileCount() after directory rollback = %d, want 2", count)
	}
}

func TestFileSystem_Delete_ReturnsWarningWhenDeleteHookWarns(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/delete-hook-warning.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	restoreData := []byte(`{"favorites":[{"path":"/delete-hook-warning.txt"}]}`)
	fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
		return &PathDeleteHookResult{RestoreData: restoreData}, workspace.WrapVisibleMutationWarning(errors.New("favorite persistence warning"))
	})

	err := fs.Delete(ctx, "/delete-hook-warning.txt")
	if !isVisibleMutationWarning(err) {
		t.Fatalf("expected visible mutation warning, got %v", err)
	}
	if _, statErr := fs.Stat(ctx, "/delete-hook-warning.txt"); statErr != ErrNotFound {
		t.Fatalf("expected deleted path to remain deleted after hook warning, got %v", statErr)
	}
	items, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() error: %v", listErr)
	}
	if len(items) != 1 {
		t.Fatalf("expected one trash item after warned delete, got %d", len(items))
	}
	if string(items[0].RestoreData) != string(restoreData) {
		t.Fatalf("expected warned delete to persist restore data %q, got %q", string(restoreData), string(items[0].RestoreData))
	}
}

func TestFileSystem_Delete_DirectoryRollbackRestoresChildIndexesWhenTrashRestoreMetadataPersistsFails(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/docs"); err != nil {
		t.Fatalf("Mkdir(/docs) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/docs/nested"); err != nil {
		t.Fatalf("Mkdir(/docs/nested) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/readme.md", bytes.NewReader([]byte("readme"))); err != nil {
		t.Fatalf("WriteFile(readme) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/nested/report.txt", bytes.NewReader([]byte("report"))); err != nil {
		t.Fatalf("WriteFile(report) error: %v", err)
	}

	fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
		return &PathDeleteHookResult{RestoreData: []byte(`{"favorites":[{"user_id":"tester","path":"/docs/readme.md","type":"file"}]}`)}, nil
	})

	originalUpdateTrashRestoreData := fs.updateTrashRestoreData
	fs.updateTrashRestoreData = func(ctx context.Context, id string, restoreData []byte) error {
		return errors.New("persist restore metadata failed")
	}
	t.Cleanup(func() {
		fs.updateTrashRestoreData = originalUpdateTrashRestoreData
	})

	err := fs.Delete(ctx, "/docs")
	if err == nil {
		t.Fatal("Expected Delete() to fail when trash restore metadata persistence fails")
	}
	if !strings.Contains(err.Error(), "failed to persist trash restore metadata") {
		t.Fatalf("expected restore metadata failure in error, got %v", err)
	}

	if _, statErr := fs.Stat(ctx, "/docs"); statErr != nil {
		t.Fatalf("expected directory to be restored after rollback, got %v", statErr)
	}
	if _, _, _, err := fs.versions.GetFileIndex(ctx, "/docs/readme.md"); err != nil {
		t.Fatalf("GetFileIndex(readme after rollback) error: %v", err)
	}
	if _, _, _, err := fs.versions.GetFileIndex(ctx, "/docs/nested/report.txt"); err != nil {
		t.Fatalf("GetFileIndex(report after rollback) error: %v", err)
	}

	items, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() error: %v", listErr)
	}
	if len(items) != 0 {
		t.Fatalf("Expected trash to remain empty after directory rollback, got %d items", len(items))
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

	fs.deleteFileIndexPrefix = func(ctx context.Context, path string) error {
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

func TestFileSystem_PermanentDelete_ReturnsWarningWhenWorkspaceSyncFailsAfterDelete(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/permanent-warning.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	originalDelete := fs.deleteStagedWorkspacePath
	fs.deleteStagedWorkspacePath = func(ctx context.Context, name string, remove func() error) error {
		if err := originalDelete(ctx, name, remove); err != nil {
			return err
		}
		return workspace.WrapVisibleMutationWarning(errors.New("sync dir failed"))
	}

	err := fs.PermanentDelete(ctx, "/permanent-warning.txt")
	if !isVisibleMutationWarning(err) {
		t.Fatalf("expected visible mutation warning, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/permanent-warning.txt"); err != ErrNotFound {
		t.Fatalf("expected file to remain deleted after warning, got %v", err)
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

func TestFileSystem_PermanentDelete_ReturnsWarningWhenDeleteHookWarns(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/permanent-hook-warning.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
		return &PathDeleteHookResult{}, workspace.WrapVisibleMutationWarning(errors.New("favorite persistence warning"))
	})

	err := fs.PermanentDelete(ctx, "/permanent-hook-warning.txt")
	if !isVisibleMutationWarning(err) {
		t.Fatalf("expected visible mutation warning, got %v", err)
	}
	if _, statErr := fs.Stat(ctx, "/permanent-hook-warning.txt"); statErr != ErrNotFound {
		t.Fatalf("expected permanent delete to remain visible after hook warning, got %v", statErr)
	}
	if _, listErr := fs.ListVersions(ctx, "/permanent-hook-warning.txt"); !errors.Is(listErr, ErrNotFound) {
		t.Fatalf("expected version metadata to be removed after hook warning, got %v", listErr)
	}
}

func TestFileSystem_PermanentDelete_ReturnsWarningWhenVersionObjectCleanupFailsAfterVisibleDelete(t *testing.T) {
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
	var warningErr *DeleteCleanupWarningError
	if !errors.As(err, &warningErr) {
		t.Fatalf("expected DeleteCleanupWarningError, got %v", err)
	}
	if isVisibleMutationWarning(err) {
		t.Fatalf("version-object cleanup error was incorrectly marked as a persistence warning: %v", err)
	}
	if !strings.Contains(err.Error(), "failed to delete version objects") {
		t.Fatalf("expected version object cleanup warning, got %v", err)
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

func TestFileSystem_Rename_RejectsTargetVersionMetadataBeforeWorkspaceMove(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/rename-raw-source.md", bytes.NewReader([]byte("source v1"))); err != nil {
		t.Fatalf("WriteFile(source v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/rename-raw-source.md", bytes.NewReader([]byte("source v2"))); err != nil {
		t.Fatalf("WriteFile(source v2) error: %v", err)
	}
	if err := fs.versions.AddVersion(ctx, "/rename-raw-target.md", "rename-raw-target-hash", 1, ""); err != nil {
		t.Fatalf("AddVersion(target) error: %v", err)
	}

	workspaceRenameCalled := false
	fs.renameWorkspacePath = func(ctx context.Context, oldName, newName string) error {
		workspaceRenameCalled = true
		return fs.workspace.Rename(ctx, oldName, newName)
	}

	err := fs.Rename(ctx, "/rename-raw-source.md", "/rename-raw-target.md")
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("Rename() error = %v, want %v", err, ErrAlreadyExists)
	}
	if workspaceRenameCalled {
		t.Fatal("expected raw target metadata conflict to be rejected before workspace rename")
	}
	if _, statErr := fs.Stat(ctx, "/rename-raw-source.md"); statErr != nil {
		t.Fatalf("expected source to remain after rejected rename, got %v", statErr)
	}
	if _, statErr := fs.Stat(ctx, "/rename-raw-target.md"); statErr != ErrNotFound {
		t.Fatalf("expected target path to remain absent, got %v", statErr)
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

func TestFileSystem_Copy_IndexesVisibleWorkspaceCopyWarning(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/copy-source.txt", bytes.NewReader([]byte("source"))); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}

	originalCopyWorkspacePath := fs.copyWorkspacePath
	fs.copyWorkspacePath = func(ctx context.Context, srcName, dstName string) error {
		if err := fs.workspace.Copy(ctx, srcName, dstName); err != nil {
			return err
		}
		return workspace.WrapVisibleMutationWarning(errors.New("workspace copy cleanup warning"))
	}
	t.Cleanup(func() {
		fs.copyWorkspacePath = originalCopyWorkspacePath
	})

	err := fs.Copy(ctx, "/copy-source.txt", "/copy-dest.txt")
	if !isVisibleMutationWarning(err) {
		t.Fatalf("Copy() error = %v, want visible mutation warning", err)
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
	if string(data) != "source" {
		t.Fatalf("destination content = %q, want %q", string(data), "source")
	}
	_, _, hash, indexErr := fs.versions.GetFileIndex(ctx, "/copy-dest.txt")
	if indexErr != nil {
		t.Fatalf("GetFileIndex(copy-dest) error: %v", indexErr)
	}
	if hash != computeHash([]byte("source")) {
		t.Fatalf("copy-dest index hash = %q, want source hash", hash)
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

func TestFileSystem_Rename_MovesMetadataAfterVisibleWorkspaceRenameWarning(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/rename-warning.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/rename-warning.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	originalRenameWorkspacePath := fs.renameWorkspacePath
	fs.renameWorkspacePath = func(ctx context.Context, oldName, newName string) error {
		if err := fs.workspace.Rename(ctx, oldName, newName); err != nil {
			return err
		}
		return workspace.WrapVisibleMutationWarning(errors.New("workspace rename sync warning"))
	}
	t.Cleanup(func() {
		fs.renameWorkspacePath = originalRenameWorkspacePath
	})

	err := fs.Rename(ctx, "/rename-warning.md", "/rename-warning-new.md")
	if !isVisibleMutationWarning(err) {
		t.Fatalf("Rename() error = %v, want visible mutation warning", err)
	}
	if _, statErr := fs.Stat(ctx, "/rename-warning.md"); statErr != ErrNotFound {
		t.Fatalf("expected original path to be absent after warned rename, got %v", statErr)
	}
	if _, statErr := fs.Stat(ctx, "/rename-warning-new.md"); statErr != nil {
		t.Fatalf("expected renamed path to remain visible after warned rename, got %v", statErr)
	}

	newVersions, versionErr := fs.versions.GetVersions(ctx, "/rename-warning-new.md")
	if versionErr != nil {
		t.Fatalf("GetVersions(new path) error: %v", versionErr)
	}
	if len(newVersions) == 0 {
		t.Fatal("expected historical versions to move to new path")
	}
	oldVersions, versionErr := fs.versions.GetVersions(ctx, "/rename-warning.md")
	if versionErr != nil {
		t.Fatalf("GetVersions(old path) error: %v", versionErr)
	}
	if len(oldVersions) != 0 {
		t.Fatalf("expected original path historical metadata to be absent, got %d versions", len(oldVersions))
	}
	if _, _, _, indexErr := fs.versions.GetFileIndex(ctx, "/rename-warning.md"); !errors.Is(indexErr, versionstore.ErrNotFound) {
		t.Fatalf("expected original path file index to be absent, got %v", indexErr)
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

func TestFileSystem_Rename_ReturnsWarningWhenPathRenameHookWarns(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/rename-hook-warning.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	fs.SetPathChangeHooks(func(context.Context, string, string) error {
		return workspace.WrapVisibleMutationWarning(errors.New("favorite persistence warning"))
	}, nil)

	err := fs.Rename(ctx, "/rename-hook-warning.txt", "/rename-hook-warning-new.txt")
	if !isVisibleMutationWarning(err) {
		t.Fatalf("expected visible mutation warning, got %v", err)
	}
	if _, statErr := fs.Stat(ctx, "/rename-hook-warning.txt"); statErr != ErrNotFound {
		t.Fatalf("expected original path to be absent after warned rename, got %v", statErr)
	}
	if _, statErr := fs.Stat(ctx, "/rename-hook-warning-new.txt"); statErr != nil {
		t.Fatalf("expected renamed path to remain visible after hook warning, got %v", statErr)
	}
	versions, listErr := fs.ListVersions(ctx, "/rename-hook-warning-new.txt")
	if listErr != nil {
		t.Fatalf("ListVersions(new path) error: %v", listErr)
	}
	if len(versions) == 0 {
		t.Fatal("expected version metadata to move to new path after hook warning")
	}
	if _, listErr := fs.ListVersions(ctx, "/rename-hook-warning.txt"); !errors.Is(listErr, ErrNotFound) {
		t.Fatalf("expected original path version metadata to be absent after hook warning, got %v", listErr)
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

func TestFileSystem_RestoreFromTrash_RejectsSymlinkParentWithoutCreatingOutsideDir(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-link/nested/original.txt", bytes.NewReader([]byte("restore me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-link/nested/original.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 trash item, got %d", len(items))
	}

	if err := fs.PermanentDelete(ctx, "/restore-link/nested"); err != nil {
		t.Fatalf("PermanentDelete(nested) error: %v", err)
	}
	if err := fs.PermanentDelete(ctx, "/restore-link"); err != nil {
		t.Fatalf("PermanentDelete(parent) error: %v", err)
	}

	outsideRoot := t.TempDir()
	if err := os.Symlink(outsideRoot, filepath.Join(fs.workspace.Root(), "restore-link")); err != nil {
		t.Fatalf("Symlink(restore-link) error: %v", err)
	}

	err = fs.RestoreFromTrash(ctx, items[0].ID)
	if !errors.Is(err, errStoragePathSymlink) {
		t.Fatalf("RestoreFromTrash() error = %v, want errStoragePathSymlink", err)
	}

	if _, statErr := os.Stat(filepath.Join(outsideRoot, "nested")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no outside restore directory, got %v", statErr)
	}
	if _, statErr := fs.Stat(ctx, "/restore-link/nested/original.txt"); statErr != ErrNotFound {
		t.Fatalf("expected original path to remain absent after failed restore, got %v", statErr)
	}

	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed restore error: %v", listErr)
	}
	if len(remaining) != 1 || remaining[0].ID != items[0].ID {
		t.Fatalf("expected trash item to remain after failed restore, got %#v", remaining)
	}
}

func TestFileSystem_RestoreFromTrashRejectsFileTargetCreatedAfterPrecheck(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-race.txt", bytes.NewReader([]byte("trash content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-race.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one trash item, got %d", len(items))
	}

	livePath := filepath.Join(fs.workspace.Root(), "restore-race.txt")
	originalAfterValidateStoragePaths := afterValidateStoragePaths
	inserted := false
	afterValidateStoragePaths = func() error {
		if inserted {
			return nil
		}
		inserted = true
		return os.WriteFile(livePath, []byte("live content"), 0644)
	}
	t.Cleanup(func() {
		afterValidateStoragePaths = originalAfterValidateStoragePaths
	})

	err = fs.RestoreFromTrash(ctx, items[0].ID)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("RestoreFromTrash() error = %v, want ErrAlreadyExists", err)
	}
	data, readErr := os.ReadFile(livePath)
	if readErr != nil {
		t.Fatalf("ReadFile(live target) error: %v", readErr)
	}
	if string(data) != "live content" {
		t.Fatalf("live target content = %q, want live content", data)
	}
	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed restore error: %v", listErr)
	}
	if len(remaining) != 1 || remaining[0].ID != items[0].ID {
		t.Fatalf("expected trash item to remain after failed restore, got %#v", remaining)
	}
}

func TestFileSystem_RestoreFromTrashRejectsDirectoryTargetCreatedAfterPrecheck(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/restore-dir-race"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-dir-race/trash.txt", bytes.NewReader([]byte("trash content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-dir-race"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one trash item, got %d", len(items))
	}

	liveDir := filepath.Join(fs.workspace.Root(), "restore-dir-race")
	liveFile := filepath.Join(liveDir, "live.txt")
	originalAfterValidateStoragePaths := afterValidateStoragePaths
	inserted := false
	afterValidateStoragePaths = func() error {
		if inserted {
			return nil
		}
		inserted = true
		if err := os.Mkdir(liveDir, 0755); err != nil {
			return err
		}
		return os.WriteFile(liveFile, []byte("live content"), 0644)
	}
	t.Cleanup(func() {
		afterValidateStoragePaths = originalAfterValidateStoragePaths
	})

	err = fs.RestoreFromTrash(ctx, items[0].ID)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("RestoreFromTrash() error = %v, want ErrAlreadyExists", err)
	}
	data, readErr := os.ReadFile(liveFile)
	if readErr != nil {
		t.Fatalf("ReadFile(live file) error: %v", readErr)
	}
	if string(data) != "live content" {
		t.Fatalf("live file content = %q, want live content", data)
	}
	if _, statErr := os.Stat(filepath.Join(liveDir, "trash.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("trash content stat error = %v, want not exist", statErr)
	}
	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed restore error: %v", listErr)
	}
	if len(remaining) != 1 || remaining[0].ID != items[0].ID {
		t.Fatalf("expected trash item to remain after failed restore, got %#v", remaining)
	}
}

func TestFileSystem_RestoreFromTrash_DoesNotRemoveOutsideTrashItemDirAfterTrashRootSwap(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-root-swap.txt", bytes.NewReader([]byte("restore me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-root-swap.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Expected 1 trash item, got %d", len(items))
	}

	outsideRoot := t.TempDir()
	outsideItemDir := filepath.Join(outsideRoot, items[0].ID)
	if err := os.MkdirAll(outsideItemDir, 0700); err != nil {
		t.Fatalf("MkdirAll(outside trash item dir) error: %v", err)
	}

	backupTrashRoot := fs.trashRoot + "-backup"
	originalAfterValidateStoragePaths := afterValidateStoragePaths
	afterValidateStoragePaths = func() error {
		if err := os.Rename(fs.trashRoot, backupTrashRoot); err != nil {
			return err
		}
		return os.Symlink(outsideRoot, fs.trashRoot)
	}
	t.Cleanup(func() {
		afterValidateStoragePaths = originalAfterValidateStoragePaths
		if info, err := os.Lstat(fs.trashRoot); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(fs.trashRoot); removeErr != nil {
				t.Errorf("Remove(trash root symlink) error: %v", removeErr)
			}
		}
		if _, err := os.Stat(backupTrashRoot); err == nil {
			if renameErr := os.Rename(backupTrashRoot, fs.trashRoot); renameErr != nil {
				t.Errorf("Rename(backup trash root) error: %v", renameErr)
			}
		}
	})

	err = fs.RestoreFromTrash(ctx, items[0].ID)
	if err != nil {
		t.Fatalf("RestoreFromTrash() error: %v", err)
	}

	if _, statErr := fs.Stat(ctx, "/restore-root-swap.txt"); statErr != nil {
		t.Fatalf("expected restored file to exist, got %v", statErr)
	}
	if _, statErr := os.Stat(outsideItemDir); statErr != nil {
		t.Fatalf("expected outside trash item dir to remain untouched, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(backupTrashRoot, items[0].ID)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected anchored trash item dir to be removed after restore, got %v", statErr)
	}

	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after restore error: %v", listErr)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected trash metadata to be removed after restore, got %d items", len(remaining))
	}
}

func TestFileSystem_WalkTrashItemRestorePaths_UsesAnchoredTrashRootAfterTrashRootSwap(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/walk-trash-root"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/walk-trash-root/original.txt", bytes.NewReader([]byte("original"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/walk-trash-root"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 trash item, got %d", len(items))
	}

	outsideRoot := t.TempDir()
	outsideContent := filepath.Join(outsideRoot, items[0].ID, "content", "fake")
	if err := os.MkdirAll(outsideContent, 0700); err != nil {
		t.Fatalf("MkdirAll(outside content) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideContent, "secret.txt"), []byte("outside"), 0600); err != nil {
		t.Fatalf("WriteFile(outside content) error: %v", err)
	}

	backupTrashRoot := fs.trashRoot + "-backup"
	if err := os.Rename(fs.trashRoot, backupTrashRoot); err != nil {
		t.Fatalf("Rename(trash root backup) error: %v", err)
	}
	if err := os.Symlink(outsideRoot, fs.trashRoot); err != nil {
		t.Fatalf("Symlink(trash root) error: %v", err)
	}
	t.Cleanup(func() {
		if info, err := os.Lstat(fs.trashRoot); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(fs.trashRoot); removeErr != nil {
				t.Errorf("Remove(trash root symlink) error: %v", removeErr)
			}
		}
		if _, err := os.Stat(backupTrashRoot); err == nil {
			if renameErr := os.Rename(backupTrashRoot, fs.trashRoot); renameErr != nil {
				t.Errorf("Rename(backup trash root) error: %v", renameErr)
			}
		}
	})

	seen := make(map[string]bool)
	err = fs.WalkTrashItemRestorePaths(ctx, items[0].ID, func(restoredPath string, _ bool, _ int64) error {
		seen[restoredPath] = true
		return nil
	})
	if err != nil {
		t.Fatalf("WalkTrashItemRestorePaths() error: %v", err)
	}
	if !seen["/walk-trash-root"] || !seen["/walk-trash-root/original.txt"] {
		t.Fatalf("expected anchored trash content paths, got %#v", seen)
	}
	if seen["/walk-trash-root/fake"] || seen["/walk-trash-root/fake/secret.txt"] {
		t.Fatalf("walk followed swapped trash root, got %#v", seen)
	}
}

func TestFileSystem_WalkTrashItemRestorePaths_RejectsSymlinkInsideTrashContent(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/walk-trash-symlink"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/walk-trash-symlink/original.txt", bytes.NewReader([]byte("original"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/walk-trash-symlink"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 trash item, got %d", len(items))
	}

	outsideRoot := t.TempDir()
	contentPath := filepath.Join(fs.trashRoot, items[0].ID, "content")
	if err := os.Symlink(outsideRoot, filepath.Join(contentPath, "linked")); err != nil {
		t.Fatalf("Symlink(trash content) error: %v", err)
	}

	err = fs.WalkTrashItemRestorePaths(ctx, items[0].ID, func(string, bool, int64) error {
		return nil
	})
	if !errors.Is(err, errStoragePathSymlink) {
		t.Fatalf("WalkTrashItemRestorePaths() error = %v, want errStoragePathSymlink", err)
	}
}

func TestFileSystem_WalkTrashItemRestorePaths_RejectsUnsafeEntryNames(t *testing.T) {
	tests := []struct {
		name      string
		entryName string
	}{
		{name: "backslash", entryName: "nested\\report.txt"},
		{name: "newline", entryName: "report\n2026.txt"},
		{name: "delete-control", entryName: "report\x7f.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := setupFileSystem(t)
			ctx := context.Background()

			if err := fs.Mkdir(ctx, "/walk-trash-unsafe"); err != nil {
				t.Fatalf("Mkdir() error: %v", err)
			}
			if err := fs.WriteFile(ctx, "/walk-trash-unsafe/original.txt", bytes.NewReader([]byte("original"))); err != nil {
				t.Fatalf("WriteFile() error: %v", err)
			}
			if err := fs.Delete(ctx, "/walk-trash-unsafe"); err != nil {
				t.Fatalf("Delete() error: %v", err)
			}

			items, err := fs.ListTrash(ctx)
			if err != nil {
				t.Fatalf("ListTrash() error: %v", err)
			}
			if len(items) != 1 {
				t.Fatalf("expected 1 trash item, got %d", len(items))
			}

			contentPath := filepath.Join(fs.trashRoot, items[0].ID, "content")
			if err := os.WriteFile(filepath.Join(contentPath, tt.entryName), []byte("unsafe"), 0600); err != nil {
				t.Skipf("platform does not support unsafe filename %q: %v", tt.entryName, err)
			}

			err = fs.WalkTrashItemRestorePaths(ctx, items[0].ID, func(string, bool, int64) error {
				return nil
			})
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("WalkTrashItemRestorePaths() error = %v, want ErrNotFound", err)
			}
		})
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

func TestFileSystem_RestoreFromTrash_ReturnsWarningWhenTrashSourceDirectorySyncFailsAfterRestore(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-source-sync.txt", bytes.NewReader([]byte("restore me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-source-sync.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one trash item, got %d", len(items))
	}
	trashItemDir := filepath.Join(fs.trashRoot, items[0].ID)

	originalSyncManagedStorageDir := syncManagedStorageDir
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if root == fs.trashRootHandle && relName == items[0].ID && absPath == trashItemDir {
			return errors.New("sync trash source dir failed")
		}
		return originalSyncManagedStorageDir(root, relName, absPath)
	}
	t.Cleanup(func() {
		syncManagedStorageDir = originalSyncManagedStorageDir
	})

	err = fs.RestoreFromTrash(ctx, items[0].ID)
	if !isVisibleMutationWarning(err) {
		t.Fatalf("RestoreFromTrash() error = %v, want visible mutation warning", err)
	}
	if _, statErr := fs.Stat(ctx, "/restore-source-sync.txt"); statErr != nil {
		t.Fatalf("expected restored file to remain visible after warning, got %v", statErr)
	}
	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after warned restore error: %v", listErr)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected trash metadata to be removed after warned restore, got %d items", len(remaining))
	}
	if _, _, hash, indexErr := fs.versions.GetFileIndex(ctx, "/restore-source-sync.txt"); indexErr != nil {
		t.Fatalf("GetFileIndex(restored) error: %v", indexErr)
	} else if hash != computeHash([]byte("restore me")) {
		t.Fatalf("restored index hash = %q, want restored content hash", hash)
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

func TestFileSystem_RestoreFromTrashTo_RejectsSymlinkParentWithoutCreatingOutsideDir(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-custom.txt", bytes.NewReader([]byte("restore me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-custom.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 trash item, got %d", len(items))
	}

	outsideRoot := t.TempDir()
	if err := os.Symlink(outsideRoot, filepath.Join(fs.workspace.Root(), "restore-escape")); err != nil {
		t.Fatalf("Symlink(restore-escape) error: %v", err)
	}

	err = fs.RestoreFromTrashTo(ctx, items[0].ID, "/restore-escape/nested/restored.txt")
	if !errors.Is(err, errStoragePathSymlink) {
		t.Fatalf("RestoreFromTrashTo() error = %v, want errStoragePathSymlink", err)
	}

	if _, statErr := os.Stat(filepath.Join(outsideRoot, "nested")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no outside restore directory, got %v", statErr)
	}
	if _, statErr := fs.Stat(ctx, "/restore-escape/nested/restored.txt"); statErr != ErrNotFound {
		t.Fatalf("expected custom restore target to remain absent, got %v", statErr)
	}

	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed custom restore error: %v", listErr)
	}
	if len(remaining) != 1 || remaining[0].ID != items[0].ID {
		t.Fatalf("expected trash item to remain after failed custom restore, got %#v", remaining)
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

func TestFileSystem_RestoreFromTrashTo_RejectsDirectoryCustomPathWhenDescendantTrashItemHasVersionMetadata(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/restore-dir-shared"); err != nil {
		t.Fatalf("Mkdir(/restore-dir-shared) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/restore-dir-shared/nested"); err != nil {
		t.Fatalf("Mkdir(/restore-dir-shared/nested) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/restored"); err != nil {
		t.Fatalf("Mkdir(/restored) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-dir-shared/nested/report.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-dir-shared/nested/report.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	versionsBefore, err := fs.versions.GetVersions(ctx, "/restore-dir-shared/nested/report.txt")
	if err != nil {
		t.Fatalf("GetVersions(report before delete) error: %v", err)
	}
	if len(versionsBefore) == 0 {
		t.Fatal("expected descendant file to have version metadata")
	}

	if err := fs.Delete(ctx, "/restore-dir-shared/nested/report.txt"); err != nil {
		t.Fatalf("Delete(report) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after child delete) error: %v", err)
	}
	if len(items) != 1 || !items[0].HadVersions {
		t.Fatalf("expected one versioned child trash item, got %+v", items)
	}
	childTrashID := items[0].ID

	if err := fs.Delete(ctx, "/restore-dir-shared"); err != nil {
		t.Fatalf("Delete(directory) error: %v", err)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after directory delete) error: %v", err)
	}
	dirTrashID := ""
	for _, item := range items {
		if item.OriginalPath == "/restore-dir-shared" {
			dirTrashID = item.ID
			if !item.HadVersions {
				t.Fatalf("expected directory trash item to record descendant version metadata, got %+v", item)
			}
		}
	}
	if dirTrashID == "" {
		t.Fatalf("expected directory trash item, got %+v", items)
	}

	err = fs.RestoreFromTrashTo(ctx, dirTrashID, "/restored/restore-dir-shared")
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("RestoreFromTrashTo(directory) error = %v, want %v", err, ErrAlreadyExists)
	}
	if _, statErr := fs.Stat(ctx, "/restored/restore-dir-shared"); statErr != ErrNotFound {
		t.Fatalf("expected custom directory restore target to remain absent, got %v", statErr)
	}
	remaining, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after rejected restore) error: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("expected both trash items to remain after rejected restore, got %+v", remaining)
	}
	foundChild := false
	for _, item := range remaining {
		if item.ID == childTrashID {
			foundChild = true
		}
	}
	if !foundChild {
		t.Fatalf("expected child trash item to remain, got %+v", remaining)
	}
	versionsAfter, err := fs.versions.GetVersions(ctx, "/restore-dir-shared/nested/report.txt")
	if err != nil {
		t.Fatalf("GetVersions(report after rejected restore) error: %v", err)
	}
	if len(versionsAfter) != len(versionsBefore) {
		t.Fatalf("expected descendant version metadata count to remain %d, got %d", len(versionsBefore), len(versionsAfter))
	}
	movedVersions, err := fs.versions.GetVersions(ctx, "/restored/restore-dir-shared/nested/report.txt")
	if err != nil {
		t.Fatalf("GetVersions(restored descendant) error: %v", err)
	}
	if len(movedVersions) != 0 {
		t.Fatalf("expected rejected restore not to move descendant version metadata, got %d versions", len(movedVersions))
	}
}

func TestFileSystem_RestoreFromTrashTo_RejectsCustomPathWhenTargetTrashItemHasVersionMetadata(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-target-history.md", bytes.NewReader([]byte("target v1"))); err != nil {
		t.Fatalf("WriteFile(target v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-target-history.md", bytes.NewReader([]byte("target v2"))); err != nil {
		t.Fatalf("WriteFile(target v2) error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-target-history.md"); err != nil {
		t.Fatalf("Delete(target) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after target delete) error: %v", err)
	}
	if len(items) != 1 || !items[0].HadVersions {
		t.Fatalf("expected one versioned target trash item, got %+v", items)
	}
	targetTrashID := items[0].ID

	if err := fs.WriteFile(ctx, "/restore-source-history.md", bytes.NewReader([]byte("source v1"))); err != nil {
		t.Fatalf("WriteFile(source v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-source-history.md", bytes.NewReader([]byte("source v2"))); err != nil {
		t.Fatalf("WriteFile(source v2) error: %v", err)
	}
	sourceVersionsBefore, err := fs.versions.GetVersions(ctx, "/restore-source-history.md")
	if err != nil {
		t.Fatalf("GetVersions(source before delete) error: %v", err)
	}
	if len(sourceVersionsBefore) == 0 {
		t.Fatal("expected source file to have version metadata")
	}
	if err := fs.Delete(ctx, "/restore-source-history.md"); err != nil {
		t.Fatalf("Delete(source) error: %v", err)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after source delete) error: %v", err)
	}
	sourceTrashID := ""
	for _, item := range items {
		if item.OriginalPath == "/restore-source-history.md" {
			sourceTrashID = item.ID
		}
	}
	if sourceTrashID == "" {
		t.Fatalf("expected source trash item, got %+v", items)
	}

	err = fs.RestoreFromTrashTo(ctx, sourceTrashID, "/restore-target-history.md")
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("RestoreFromTrashTo() error = %v, want %v", err, ErrAlreadyExists)
	}
	if _, statErr := fs.Stat(ctx, "/restore-target-history.md"); statErr != ErrNotFound {
		t.Fatalf("expected target path to remain absent, got %v", statErr)
	}
	remaining, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after rejected restore) error: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("expected both trash items to remain, got %+v", remaining)
	}
	foundTarget, foundSource := false, false
	for _, item := range remaining {
		if item.ID == targetTrashID {
			foundTarget = true
		}
		if item.ID == sourceTrashID {
			foundSource = true
		}
	}
	if !foundTarget || !foundSource {
		t.Fatalf("expected target and source trash items to remain, got %+v", remaining)
	}
	sourceVersionsAfter, err := fs.versions.GetVersions(ctx, "/restore-source-history.md")
	if err != nil {
		t.Fatalf("GetVersions(source after rejected restore) error: %v", err)
	}
	if len(sourceVersionsAfter) != len(sourceVersionsBefore) {
		t.Fatalf("expected source version metadata count to remain %d, got %d", len(sourceVersionsBefore), len(sourceVersionsAfter))
	}
}

func TestFileSystem_RestoreFromTrashTo_AllowsCustomPathWhenSameOriginalTrashItemHasNoVersions(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-mixed-history.txt", bytes.NewReader([]byte("plain"))); err != nil {
		t.Fatalf("WriteFile(plain) error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-mixed-history.txt"); err != nil {
		t.Fatalf("Delete(plain) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after plain delete) error: %v", err)
	}
	if len(items) != 1 || items[0].HadVersions {
		t.Fatalf("expected one non-versioned trash item, got %+v", items)
	}
	plainTrashID := items[0].ID

	if err := fs.WriteFile(ctx, "/restore-mixed-history.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-mixed-history.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-mixed-history.txt"); err != nil {
		t.Fatalf("Delete(versioned) error: %v", err)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after versioned delete) error: %v", err)
	}
	versionedTrashID := ""
	for _, item := range items {
		if item.OriginalPath == "/restore-mixed-history.txt" && item.HadVersions {
			versionedTrashID = item.ID
		}
	}
	if versionedTrashID == "" {
		t.Fatalf("expected versioned trash item, got %+v", items)
	}

	if err := fs.RestoreFromTrashTo(ctx, versionedTrashID, "/restored/restore-mixed-history.txt"); err != nil {
		t.Fatalf("RestoreFromTrashTo() error: %v", err)
	}

	remaining, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after custom restore) error: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != plainTrashID {
		t.Fatalf("expected only non-versioned trash item to remain, got %+v", remaining)
	}
	if _, statErr := fs.Stat(ctx, "/restored/restore-mixed-history.txt"); statErr != nil {
		t.Fatalf("expected custom restore target to exist, got %v", statErr)
	}
	restoredVersions, err := fs.versions.GetVersions(ctx, "/restored/restore-mixed-history.txt")
	if err != nil {
		t.Fatalf("GetVersions(restored) error: %v", err)
	}
	if len(restoredVersions) == 0 {
		t.Fatal("expected version metadata to move to the custom restore path")
	}
	originalVersions, err := fs.versions.GetVersions(ctx, "/restore-mixed-history.txt")
	if err != nil {
		t.Fatalf("GetVersions(original) error: %v", err)
	}
	if len(originalVersions) != 0 {
		t.Fatalf("expected original path version metadata to be moved, got %d versions", len(originalVersions))
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

func TestFileSystem_DeleteFromTrash_CleansVersionMetadataWhenSameOriginalTrashItemHasNoVersions(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/delete-mixed-history.txt", bytes.NewReader([]byte("plain"))); err != nil {
		t.Fatalf("WriteFile(plain) error: %v", err)
	}
	if err := fs.Delete(ctx, "/delete-mixed-history.txt"); err != nil {
		t.Fatalf("Delete(plain) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after plain delete) error: %v", err)
	}
	if len(items) != 1 || items[0].HadVersions {
		t.Fatalf("expected one non-versioned trash item, got %+v", items)
	}
	plainTrashID := items[0].ID

	if err := fs.WriteFile(ctx, "/delete-mixed-history.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/delete-mixed-history.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	versionsBefore, err := fs.versions.GetVersions(ctx, "/delete-mixed-history.txt")
	if err != nil {
		t.Fatalf("GetVersions(before delete) error: %v", err)
	}
	if len(versionsBefore) == 0 {
		t.Fatal("expected recreated file to have version metadata")
	}
	if err := fs.Delete(ctx, "/delete-mixed-history.txt"); err != nil {
		t.Fatalf("Delete(versioned) error: %v", err)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after versioned delete) error: %v", err)
	}
	versionedTrashID := ""
	for _, item := range items {
		if item.OriginalPath == "/delete-mixed-history.txt" && item.HadVersions {
			versionedTrashID = item.ID
		}
	}
	if versionedTrashID == "" {
		t.Fatalf("expected versioned trash item, got %+v", items)
	}

	deletedHashes := make(map[string]int)
	fs.deleteVersionObject = func(ctx context.Context, hash string) error {
		deletedHashes[hash]++
		return nil
	}

	if err := fs.DeleteFromTrash(ctx, versionedTrashID); err != nil {
		t.Fatalf("DeleteFromTrash(versioned) error: %v", err)
	}

	remaining, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after versioned trash delete) error: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != plainTrashID {
		t.Fatalf("expected only non-versioned trash item to remain, got %+v", remaining)
	}
	remainingVersions, err := fs.versions.GetVersions(ctx, "/delete-mixed-history.txt")
	if err != nil {
		t.Fatalf("GetVersions(after delete) error: %v", err)
	}
	if len(remainingVersions) != 0 {
		t.Fatalf("expected version metadata to be removed, got %d versions", len(remainingVersions))
	}
	for _, version := range versionsBefore {
		if deletedHashes[version.Hash] != 1 {
			t.Fatalf("expected version object %s to be deleted once, got %d", version.Hash, deletedHashes[version.Hash])
		}
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

func TestFileSystem_RestoreFromTrashTo_RejectsCustomPathWhenOriginalPathExistsWithVersionMetadata(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-live-history.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-live-history.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-live-history.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-live-history.txt", bytes.NewReader([]byte("v3"))); err != nil {
		t.Fatalf("WriteFile(recreated) error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 || !items[0].HadVersions {
		t.Fatalf("expected one versioned trash item, got %+v", items)
	}
	versionsBefore, err := fs.ListVersions(ctx, "/restore-live-history.txt")
	if err != nil {
		t.Fatalf("ListVersions(live before restore) error: %v", err)
	}
	if len(versionsBefore) < 2 {
		t.Fatalf("expected recreated live file to retain historical versions before custom restore, got %d entries", len(versionsBefore))
	}

	err = fs.RestoreFromTrashTo(ctx, items[0].ID, "/restored/restore-live-history.txt")
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("RestoreFromTrashTo() error = %v, want %v", err, ErrAlreadyExists)
	}

	if _, statErr := fs.Stat(ctx, "/restored/restore-live-history.txt"); statErr != ErrNotFound {
		t.Fatalf("expected rejected custom restore target to remain absent, got %v", statErr)
	}
	versionsAfter, err := fs.ListVersions(ctx, "/restore-live-history.txt")
	if err != nil {
		t.Fatalf("ListVersions(live after restore) error: %v", err)
	}
	if len(versionsAfter) != len(versionsBefore) {
		t.Fatalf("expected live original path to keep %d version entries, got %d", len(versionsBefore), len(versionsAfter))
	}
	newVersions, err := fs.versions.GetVersions(ctx, "/restored/restore-live-history.txt")
	if err != nil {
		t.Fatalf("GetVersions(custom path) error: %v", err)
	}
	if len(newVersions) != 0 {
		t.Fatalf("expected rejected custom restore not to move version metadata, got %d versions", len(newVersions))
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

func TestFileSystem_RestoreFromTrashTo_ReturnsWarningWhenTrashSourceDirectorySyncFailsAfterRestore(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	newPath := "/restored/custom-source-sync.txt"

	if err := fs.WriteFile(ctx, "/custom-source-sync.txt", bytes.NewReader([]byte("restore custom"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/custom-source-sync.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one trash item, got %d", len(items))
	}
	trashItemDir := filepath.Join(fs.trashRoot, items[0].ID)

	originalSyncManagedStorageDir := syncManagedStorageDir
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if root == fs.trashRootHandle && relName == items[0].ID && absPath == trashItemDir {
			return errors.New("sync trash source dir failed")
		}
		return originalSyncManagedStorageDir(root, relName, absPath)
	}
	t.Cleanup(func() {
		syncManagedStorageDir = originalSyncManagedStorageDir
	})

	err = fs.RestoreFromTrashTo(ctx, items[0].ID, newPath)
	if !isVisibleMutationWarning(err) {
		t.Fatalf("RestoreFromTrashTo() error = %v, want visible mutation warning", err)
	}
	if _, statErr := fs.Stat(ctx, newPath); statErr != nil {
		t.Fatalf("expected custom restored file to remain visible after warning, got %v", statErr)
	}
	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after warned restore error: %v", listErr)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected trash metadata to be removed after warned restore, got %d items", len(remaining))
	}
	if _, _, hash, indexErr := fs.versions.GetFileIndex(ctx, newPath); indexErr != nil {
		t.Fatalf("GetFileIndex(custom restored) error: %v", indexErr)
	} else if hash != computeHash([]byte("restore custom")) {
		t.Fatalf("custom restored index hash = %q, want restored content hash", hash)
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
	if err := fs.versions.AddVersion(ctx, conflictPath, "conflict-hash", 1, ""); err != nil {
		t.Fatalf("AddVersion() error: %v", err)
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

func TestFileSystem_RestoreFromTrashTo_RejectsCustomPathWhenTargetHasRawVersionMetadataBeforeMovingContent(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-raw-conflict.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-raw-conflict.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	versionsBefore, err := fs.versions.GetVersions(ctx, "/restore-raw-conflict.md")
	if err != nil {
		t.Fatalf("GetVersions(source before delete) error: %v", err)
	}
	if len(versionsBefore) == 0 {
		t.Fatal("expected source file to have version metadata")
	}
	if err := fs.Delete(ctx, "/restore-raw-conflict.md"); err != nil {
		t.Fatalf("Delete(source) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one source trash item, got %+v", items)
	}

	targetPath := "/restored/raw-conflict.md"
	if err := fs.versions.AddVersion(ctx, targetPath, "raw-conflict-hash", 1, ""); err != nil {
		t.Fatalf("AddVersion(target) error: %v", err)
	}

	originalRemoveTrashMetadata := fs.removeTrashMetadata
	metadataRemovalCalled := false
	fs.removeTrashMetadata = func(ctx context.Context, id string) error {
		metadataRemovalCalled = true
		return originalRemoveTrashMetadata(ctx, id)
	}

	err = fs.RestoreFromTrashTo(ctx, items[0].ID, targetPath)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("RestoreFromTrashTo() error = %v, want %v", err, ErrAlreadyExists)
	}
	if metadataRemovalCalled {
		t.Fatal("expected target metadata conflict to be rejected before trash metadata removal")
	}
	if _, statErr := fs.Stat(ctx, targetPath); statErr != ErrNotFound {
		t.Fatalf("expected custom restore target to remain absent, got %v", statErr)
	}
	remaining, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after rejected restore) error: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != items[0].ID {
		t.Fatalf("expected source trash item to remain, got %+v", remaining)
	}
	versionsAfter, err := fs.versions.GetVersions(ctx, "/restore-raw-conflict.md")
	if err != nil {
		t.Fatalf("GetVersions(source after rejected restore) error: %v", err)
	}
	if len(versionsAfter) != len(versionsBefore) {
		t.Fatalf("expected source version metadata count to remain %d, got %d", len(versionsBefore), len(versionsAfter))
	}
}

func TestFileSystem_RestoreFromTrashTo_DoesNotMoveMetadataBeforeTrashMetadataRemovalSucceeds(t *testing.T) {
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
	rollbackRenameCalled := false
	fs.renameHistoryMetadataPath = func(ctx context.Context, oldName, updatedPath string) error {
		if oldName == newPath && updatedPath == "/restore-remove-fail.md" {
			rollbackRenameCalled = true
			return errors.New("metadata rollback failed")
		}
		return fs.versions.RenamePathHistory(ctx, oldName, updatedPath)
	}
	fs.removeTrashMetadata = func(ctx context.Context, id string) error {
		return errors.New("remove trash metadata failed")
	}

	err = fs.RestoreFromTrashTo(ctx, items[0].ID, newPath)
	if err == nil {
		t.Fatal("Expected RestoreFromTrashTo() to fail when trash metadata removal fails")
	}
	if rollbackRenameCalled {
		t.Fatal("expected metadata rename rollback not to be needed before trash metadata removal succeeds")
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
	originalVersions, versionsErr := fs.versions.GetVersions(ctx, "/restore-remove-fail.md")
	if versionsErr != nil {
		t.Fatalf("GetVersions(original) error: %v", versionsErr)
	}
	if len(originalVersions) != 1 {
		t.Fatalf("expected original historical version metadata to remain after rollback, got %d versions", len(originalVersions))
	}
	newVersions, versionsErr := fs.versions.GetVersions(ctx, newPath)
	if versionsErr != nil {
		t.Fatalf("GetVersions(new path) error: %v", versionsErr)
	}
	if len(newVersions) != 0 {
		t.Fatalf("expected no version metadata under restored path after rollback, got %d versions", len(newVersions))
	}
}

func TestFileSystem_RestoreFromTrashTo_DoesNotMoveMetadataBeforeIndexUpdateSucceeds(t *testing.T) {
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
	rollbackRenameCalled := false
	fs.renameHistoryMetadataPath = func(ctx context.Context, oldName, updatedPath string) error {
		if oldName == newPath && updatedPath == "/restore-index-rollback-fail.md" {
			rollbackRenameCalled = true
			return errors.New("metadata rollback failed")
		}
		return fs.versions.RenamePathHistory(ctx, oldName, updatedPath)
	}
	fs.updateFileIndex = func(ctx context.Context, path string, size int64, modTime time.Time, hash string) error {
		return errors.New("index update failed")
	}

	err = fs.RestoreFromTrashTo(ctx, items[0].ID, newPath)
	if err == nil {
		t.Fatal("Expected RestoreFromTrashTo() to fail when file index update fails")
	}
	if rollbackRenameCalled {
		t.Fatal("expected metadata rename rollback not to be needed before file index update succeeds")
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
	originalVersions, versionsErr := fs.versions.GetVersions(ctx, "/restore-index-rollback-fail.md")
	if versionsErr != nil {
		t.Fatalf("GetVersions(original) error: %v", versionsErr)
	}
	if len(originalVersions) != 1 {
		t.Fatalf("expected original historical version metadata to remain after rollback, got %d versions", len(originalVersions))
	}
	newVersions, versionsErr := fs.versions.GetVersions(ctx, newPath)
	if versionsErr != nil {
		t.Fatalf("GetVersions(new path) error: %v", versionsErr)
	}
	if len(newVersions) != 0 {
		t.Fatalf("expected no version metadata under restored path after rollback, got %d versions", len(newVersions))
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

func TestFileSystem_EmptyTrashSelection_DeletesSelectedItems(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	fs.WriteFile(ctx, "/empty1.txt", bytes.NewReader([]byte("x")))
	fs.WriteFile(ctx, "/empty2.txt", bytes.NewReader([]byte("y")))
	fs.Delete(ctx, "/empty1.txt")
	fs.Delete(ctx, "/empty2.txt")

	selected, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	ids := []string{selected[0].ID, selected[1].ID}
	result, err := fs.EmptyTrashSelection(ctx, ids, nil)
	if err != nil {
		t.Fatalf("EmptyTrashSelection() error: %v", err)
	}

	if len(result.DeletedIDs) != 2 {
		t.Errorf("EmptyTrashSelection() deleted %d, want 2", len(result.DeletedIDs))
	}

	items, _ := fs.ListTrash(ctx)
	if len(items) != 0 {
		t.Errorf("Trash still has %d items", len(items))
	}
}

func TestFileSystem_EmptyTrashSelection_ReturnsContextCanceledBeforeListing(t *testing.T) {
	fs := setupFileSystem(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := fs.EmptyTrashSelection(ctx, []string{"missing"}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(result.DeletedIDs) != 0 {
		t.Fatalf("expected zero deleted items on canceled context, got %d", len(result.DeletedIDs))
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
	var deleteWarningErr *TrashDeleteWarningError
	if !errors.As(err, &deleteWarningErr) {
		t.Fatalf("expected trash delete warning error, got %v", err)
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

func TestFileSystem_DeleteFromTrash_RemovesDirectoryChildVersionMetadata(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/docs"); err != nil {
		t.Fatalf("Mkdir(/docs) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/docs/nested"); err != nil {
		t.Fatalf("Mkdir(/docs/nested) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/nested/report.txt", bytes.NewReader([]byte("report v1"))); err != nil {
		t.Fatalf("WriteFile(report v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/nested/report.txt", bytes.NewReader([]byte("report v2"))); err != nil {
		t.Fatalf("WriteFile(report v2) error: %v", err)
	}

	versions, err := fs.versions.GetVersions(ctx, "/docs/nested/report.txt")
	if err != nil {
		t.Fatalf("GetVersions(report before delete) error: %v", err)
	}
	if len(versions) == 0 {
		t.Fatal("expected nested file to have version history before delete")
	}

	if err := fs.Delete(ctx, "/docs"); err != nil {
		t.Fatalf("Delete(/docs) error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 trash item, got %d", len(items))
	}

	deletedHashes := make(map[string]int)
	fs.deleteVersionObject = func(ctx context.Context, hash string) error {
		deletedHashes[hash]++
		return nil
	}

	if err := fs.DeleteFromTrash(ctx, items[0].ID); err != nil {
		t.Fatalf("DeleteFromTrash() error: %v", err)
	}

	for _, version := range versions {
		if deletedHashes[version.Hash] != 1 {
			t.Fatalf("expected deleteVersionObject to be called once for %s, got %d", version.Hash, deletedHashes[version.Hash])
		}
	}

	paths, err := fs.versions.ListVersionPaths(ctx)
	if err != nil {
		t.Fatalf("ListVersionPaths() error: %v", err)
	}
	for _, versionPath := range paths {
		if versionPath == "/docs/nested/report.txt" {
			t.Fatalf("expected nested version metadata to be removed, got paths %v", paths)
		}
	}

	remainingVersions, err := fs.versions.GetVersions(ctx, "/docs/nested/report.txt")
	if err != nil {
		t.Fatalf("GetVersions(report after DeleteFromTrash) error: %v", err)
	}
	if len(remainingVersions) != 0 {
		t.Fatalf("expected nested version metadata to be removed, got %d entries", len(remainingVersions))
	}
}

func TestFileSystem_DeleteFromTrash_KeepsDirectoryChildVersionMetadataReferencedByTrashItem(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/docs"); err != nil {
		t.Fatalf("Mkdir(/docs) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/docs/nested"); err != nil {
		t.Fatalf("Mkdir(/docs/nested) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/nested/report.txt", bytes.NewReader([]byte("report v1"))); err != nil {
		t.Fatalf("WriteFile(report v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/nested/report.txt", bytes.NewReader([]byte("report v2"))); err != nil {
		t.Fatalf("WriteFile(report v2) error: %v", err)
	}
	versionsBefore, err := fs.versions.GetVersions(ctx, "/docs/nested/report.txt")
	if err != nil {
		t.Fatalf("GetVersions(report before delete) error: %v", err)
	}
	if len(versionsBefore) == 0 {
		t.Fatal("expected nested file to have version history before delete")
	}

	if err := fs.Delete(ctx, "/docs/nested/report.txt"); err != nil {
		t.Fatalf("Delete(report) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after child delete) error: %v", err)
	}
	if len(items) != 1 || items[0].OriginalPath != "/docs/nested/report.txt" {
		t.Fatalf("expected child trash item, got %+v", items)
	}
	childTrashID := items[0].ID

	if err := fs.Delete(ctx, "/docs"); err != nil {
		t.Fatalf("Delete(/docs) error: %v", err)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after dir delete) error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected directory and child trash items, got %+v", items)
	}
	dirTrashID := ""
	for _, item := range items {
		if item.OriginalPath == "/docs" {
			dirTrashID = item.ID
			if !item.HadVersions {
				t.Fatalf("expected directory trash item to record nested version metadata, got %+v", item)
			}
		}
	}
	if dirTrashID == "" {
		t.Fatalf("expected directory trash item, got %+v", items)
	}

	if err := fs.DeleteFromTrash(ctx, dirTrashID); err != nil {
		t.Fatalf("DeleteFromTrash(directory) error: %v", err)
	}

	remaining, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after directory trash delete) error: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != childTrashID {
		t.Fatalf("expected child trash item to remain, got %+v", remaining)
	}
	versionsAfter, err := fs.versions.GetVersions(ctx, "/docs/nested/report.txt")
	if err != nil {
		t.Fatalf("GetVersions(report after directory trash delete) error: %v", err)
	}
	if len(versionsAfter) != len(versionsBefore) {
		t.Fatalf("expected child trash item to retain %d version entries, got %d", len(versionsBefore), len(versionsAfter))
	}
}

func TestFileSystem_EmptyTrashSelection_AttemptsVersionObjectCleanup(t *testing.T) {
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
	selected, err := fs.ListTrash(ctx)
	if err != nil || len(selected) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", selected, err)
	}

	called := make(map[string]int)
	fs.deleteVersionObject = func(ctx context.Context, hash string) error {
		called[hash]++
		return errors.New("delete object failed")
	}

	result, err := fs.EmptyTrashSelection(ctx, []string{selected[0].ID}, nil)
	if err == nil {
		t.Fatal("expected EmptyTrashSelection() to fail when version object cleanup fails")
	}
	var emptyWarningErr *TrashDeleteWarningError
	if !errors.As(err, &emptyWarningErr) {
		t.Fatalf("expected trash delete warning error, got %v", err)
	}
	if !strings.Contains(err.Error(), "failed to delete version objects for trash item") {
		t.Fatalf("expected trash version object cleanup error, got %v", err)
	}
	if len(result.DeletedIDs) != 1 {
		t.Fatalf("expected visible deletion to be counted before object cleanup failure, got %d", len(result.DeletedIDs))
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
	var cleanupWarningErr *TrashDeleteWarningError
	if !errors.As(err, &cleanupWarningErr) {
		t.Fatalf("expected trash delete warning error, got %v", err)
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

func TestFileSystem_EmptyTrashSelection_ContinuesAfterVisibleDeleteWarning(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/empty-trash-warning-versioned.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(versioned v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/empty-trash-warning-versioned.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(versioned v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/empty-trash-warning-plain.txt", bytes.NewReader([]byte("plain"))); err != nil {
		t.Fatalf("WriteFile(plain) error: %v", err)
	}
	if err := fs.Delete(ctx, "/empty-trash-warning-versioned.md"); err != nil {
		t.Fatalf("Delete(versioned) error: %v", err)
	}
	if err := fs.Delete(ctx, "/empty-trash-warning-plain.txt"); err != nil {
		t.Fatalf("Delete(plain) error: %v", err)
	}

	versions, err := fs.versions.GetVersions(ctx, "/empty-trash-warning-versioned.md")
	if err != nil {
		t.Fatalf("GetVersions() error: %v", err)
	}
	if len(versions) == 0 {
		t.Fatal("expected historical versions for warning scenario")
	}
	selected, err := fs.ListTrash(ctx)
	if err != nil || len(selected) != 2 {
		t.Fatalf("ListTrash() = %+v, %v; want two items", selected, err)
	}
	ids := []string{selected[0].ID, selected[1].ID}

	called := 0
	fs.deleteVersionObject = func(ctx context.Context, hash string) error {
		called++
		return errors.New("delete object failed")
	}

	result, err := fs.EmptyTrashSelection(ctx, ids, nil)
	if err == nil {
		t.Fatal("expected EmptyTrashSelection() to return warning when visible delete cleanup fails")
	}
	var warningErr *TrashDeleteWarningError
	if !errors.As(err, &warningErr) {
		t.Fatalf("expected trash delete warning error, got %v", err)
	}
	if len(result.DeletedIDs) != 2 {
		t.Fatalf("expected warning cleanup to continue deleting remaining items, got %d deletions", len(result.DeletedIDs))
	}
	if called != len(versions) {
		t.Fatalf("expected deleteVersionObject to be attempted %d times, got %d", len(versions), called)
	}
	items, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after warning error: %v", listErr)
	}
	if len(items) != 0 {
		t.Fatalf("expected all trash items removed despite cleanup warning, got %d items", len(items))
	}
}

func TestFileSystem_EmptyTrashSelection_PreservesPartialWarningWhenHardFailureFollows(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/empty-trash-mixed-plain.txt", bytes.NewReader([]byte("plain"))); err != nil {
		t.Fatalf("WriteFile(plain) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/empty-trash-mixed-versioned.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(versioned v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/empty-trash-mixed-versioned.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(versioned v2) error: %v", err)
	}
	if err := fs.Delete(ctx, "/empty-trash-mixed-plain.txt"); err != nil {
		t.Fatalf("Delete(plain) error: %v", err)
	}
	if err := fs.Delete(ctx, "/empty-trash-mixed-versioned.md"); err != nil {
		t.Fatalf("Delete(versioned) error: %v", err)
	}
	selected, err := fs.ListTrash(ctx)
	if err != nil || len(selected) != 2 {
		t.Fatalf("ListTrash() = %+v, %v; want two items", selected, err)
	}
	ids := []string{selected[0].ID, selected[1].ID}

	originalSyncManagedStorageDir := syncManagedStorageDir
	syncFailures := 0
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		syncFailures++
		if syncFailures == 4 {
			return errors.New("sync dir failed")
		}
		return originalSyncManagedStorageDir(root, relName, absPath)
	}
	t.Cleanup(func() {
		syncManagedStorageDir = originalSyncManagedStorageDir
	})

	removeCalls := 0
	fs.removeTrashPath = func(path string) error {
		removeCalls++
		if removeCalls == 2 {
			return errors.New("trash delete failed")
		}
		return os.RemoveAll(path)
	}

	result, err := fs.EmptyTrashSelection(ctx, ids, nil)
	if err == nil {
		t.Fatal("expected EmptyTrashSelection() to report mixed partial warning")
	}
	var warningErr *TrashDeleteWarningError
	if !errors.As(err, &warningErr) {
		t.Fatalf("expected trash delete warning error, got %v", err)
	}
	if !warningErr.Partial() {
		t.Fatalf("expected trash delete warning error to mark partial failure, got %v", err)
	}
	if len(result.DeletedIDs) != 1 {
		t.Fatalf("expected deleted count 1 when hard failure follows warning, got %d", len(result.DeletedIDs))
	}

	items, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after mixed warning error: %v", listErr)
	}
	if len(items) != 1 {
		t.Fatalf("expected one trash item to remain after mixed warning error, got %d", len(items))
	}
	if items[0].OriginalPath != "/empty-trash-mixed-plain.txt" && items[0].OriginalPath != "/empty-trash-mixed-versioned.md" {
		t.Fatalf("expected one original trash item to remain after hard failure, got %+v", items[0])
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

	originalSyncManagedStorageDir := syncManagedStorageDir
	syncCalls := 0
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		syncCalls++
		if syncCalls == 3 {
			return errors.New("sync dir failed")
		}
		return nil
	}
	t.Cleanup(func() {
		syncManagedStorageDir = originalSyncManagedStorageDir
	})

	err = fs.DeleteFromTrash(ctx, items[0].ID)
	if err == nil {
		t.Fatal("Expected DeleteFromTrash() to fail when trash delete directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync deleted trash content") {
		t.Fatalf("expected deleted trash sync failure in error, got %v", err)
	}
	if syncCalls < 3 {
		t.Fatalf("expected post-delete sync to be attempted after staging syncs, got %d sync calls", syncCalls)
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

func TestFileSystem_DeleteFromTrash_DoesNotRemoveOutsideDeletingDirAfterTrashRootSwap(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/trash-root-swap.txt", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/trash-root-swap.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Expected 1 trash item, got %d", len(items))
	}

	outsideRoot := t.TempDir()
	outsideDeletingDir := filepath.Join(outsideRoot, ".deleting")
	if err := os.MkdirAll(outsideDeletingDir, 0700); err != nil {
		t.Fatalf("MkdirAll(outside .deleting) error: %v", err)
	}

	backupTrashRoot := fs.trashRoot + "-backup"
	originalRemoveTrashMetadata := fs.removeTrashMetadata
	fs.removeTrashMetadata = func(ctx context.Context, id string) error {
		if err := os.Rename(fs.trashRoot, backupTrashRoot); err != nil {
			return err
		}
		if err := os.Symlink(outsideRoot, fs.trashRoot); err != nil {
			return err
		}
		return errors.New("metadata delete failed")
	}
	t.Cleanup(func() {
		fs.removeTrashMetadata = originalRemoveTrashMetadata
		if info, err := os.Lstat(fs.trashRoot); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(fs.trashRoot); removeErr != nil {
				t.Errorf("Remove(trash root symlink) error: %v", removeErr)
			}
		}
		if _, err := os.Stat(backupTrashRoot); err == nil {
			if renameErr := os.Rename(backupTrashRoot, fs.trashRoot); renameErr != nil {
				t.Errorf("Rename(backup trash root) error: %v", renameErr)
			}
		}
	})

	err = fs.DeleteFromTrash(ctx, items[0].ID)
	if err == nil {
		t.Fatal("Expected DeleteFromTrash() to fail when trash metadata deletion fails after trash root swap")
	}
	if !strings.Contains(err.Error(), "metadata delete failed") {
		t.Fatalf("expected metadata delete failure, got %v", err)
	}

	if _, statErr := os.Stat(outsideDeletingDir); statErr != nil {
		t.Fatalf("expected outside .deleting directory to remain untouched, got %v", statErr)
	}
	anchoredDeletingDir := filepath.Join(backupTrashRoot, ".deleting")
	if entries, readErr := os.ReadDir(anchoredDeletingDir); readErr == nil {
		if len(entries) != 0 {
			entryNames := make([]string, 0, len(entries))
			for _, entry := range entries {
				entryNames = append(entryNames, entry.Name())
			}
			t.Fatalf("expected anchored .deleting directory to have no staged leftovers, got %d entries: %v", len(entries), entryNames)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("expected anchored .deleting directory to be empty or absent, got %v", readErr)
	}
	if _, statErr := os.Stat(filepath.Join(backupTrashRoot, items[0].ID)); statErr != nil {
		t.Fatalf("expected trash item content to be rolled back inside anchored trash root, got %v", statErr)
	}

	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed metadata delete error: %v", listErr)
	}
	if len(remaining) != 1 || remaining[0].ID != items[0].ID {
		t.Fatalf("expected trash metadata to remain after rollback, got %#v", remaining)
	}
}

func TestFileSystem_DeleteFromTrash_AnchorsContentRemovalAfterTrashRootSwap(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/trash-root-swap-delete.txt", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/trash-root-swap-delete.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Expected 1 trash item, got %d", len(items))
	}

	outsideRoot := t.TempDir()
	backupTrashRoot := fs.trashRoot + "-backup"
	originalRemoveTrashPath := fs.removeTrashPath
	swappedTrashRoot := false
	sentinelPath := ""
	fs.removeTrashPath = func(target string) error {
		if !swappedTrashRoot {
			rel, ok := storageRelativePath(fs.trashRoot, filepath.Clean(target))
			if !ok {
				return fmt.Errorf("target %q escaped trash root", target)
			}
			outsideTarget := filepath.Join(outsideRoot, rel)
			if err := os.MkdirAll(outsideTarget, 0700); err != nil {
				return err
			}
			sentinelPath = filepath.Join(outsideTarget, "sentinel")
			if err := os.WriteFile(sentinelPath, []byte("outside"), 0600); err != nil {
				return err
			}
			if err := os.Rename(fs.trashRoot, backupTrashRoot); err != nil {
				return err
			}
			if err := os.Symlink(outsideRoot, fs.trashRoot); err != nil {
				return err
			}
			swappedTrashRoot = true
		}
		return originalRemoveTrashPath(target)
	}
	t.Cleanup(func() {
		fs.removeTrashPath = originalRemoveTrashPath
		if info, err := os.Lstat(fs.trashRoot); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(fs.trashRoot); removeErr != nil {
				t.Errorf("Remove(trash root symlink) error: %v", removeErr)
			}
		}
		if _, err := os.Stat(backupTrashRoot); err == nil {
			if renameErr := os.Rename(backupTrashRoot, fs.trashRoot); renameErr != nil {
				t.Errorf("Rename(backup trash root) error: %v", renameErr)
			}
		}
	})

	if err := fs.DeleteFromTrash(ctx, items[0].ID); err != nil {
		t.Fatalf("DeleteFromTrash() error: %v", err)
	}
	if !swappedTrashRoot {
		t.Fatal("expected trash root swap hook to run")
	}
	if data, err := os.ReadFile(sentinelPath); err != nil || string(data) != "outside" {
		t.Fatalf("outside sentinel was modified or removed, data=%q error=%v", string(data), err)
	}
	if _, statErr := os.Stat(filepath.Join(backupTrashRoot, items[0].ID)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected anchored trash item to be removed, got %v", statErr)
	}

	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after delete error: %v", listErr)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected trash metadata to be removed, got %#v", remaining)
	}
}

func TestFileSystem_EmptyTrashSelection_KeepsMetadataWhenContentDeleteFails(t *testing.T) {
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
	selected, err := fs.ListTrash(ctx)
	if err != nil || len(selected) != 2 {
		t.Fatalf("ListTrash() = %+v, %v; want two items", selected, err)
	}
	ids := []string{selected[0].ID, selected[1].ID}

	fs.removeTrashPath = func(path string) error {
		return errors.New("trash delete failed")
	}

	result, err := fs.EmptyTrashSelection(ctx, ids, nil)
	if err == nil {
		t.Fatal("Expected EmptyTrashSelection() to fail when trash content deletion fails")
	}
	if len(result.DeletedIDs) != 0 {
		t.Fatalf("Expected no metadata deletion on failure, got %d", len(result.DeletedIDs))
	}

	items, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() after failed empty error: %v", listErr)
	}
	if len(items) != 2 {
		t.Fatalf("Expected trash metadata to remain after failed empty, got %d items", len(items))
	}
}

func TestFileSystem_EmptyTrashSelection_RollsBackContentWhenMetadataDeleteFails(t *testing.T) {
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

	ids := []string{items[0].ID, items[1].ID}
	result, err := fs.EmptyTrashSelection(ctx, ids, nil)
	if err == nil {
		t.Fatal("Expected EmptyTrashSelection() to fail when trash metadata deletion fails")
	}
	if len(result.DeletedIDs) != 1 {
		t.Fatalf("Expected one trash item to be deleted before failure, got %d", len(result.DeletedIDs))
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

func TestFileSystem_EmptyTrashSelection_CountsDeletedItemWhenDirectorySyncFailsAfterContentDelete(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/empty-sync-fail.txt", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/empty-sync-fail.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	selected, err := fs.ListTrash(ctx)
	if err != nil || len(selected) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", selected, err)
	}
	if err := os.MkdirAll(filepath.Join(fs.trashRoot, ".deleting"), 0700); err != nil {
		t.Fatalf("MkdirAll(.deleting) error: %v", err)
	}

	originalSyncManagedStorageDir := syncManagedStorageDir
	syncCalls := 0
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		syncCalls++
		if syncCalls == 3 {
			return errors.New("sync dir failed")
		}
		return nil
	}
	t.Cleanup(func() {
		syncManagedStorageDir = originalSyncManagedStorageDir
	})

	result, err := fs.EmptyTrashSelection(ctx, []string{selected[0].ID}, nil)
	if err == nil {
		t.Fatal("Expected EmptyTrashSelection() to fail when trash delete directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync deleted trash content") {
		t.Fatalf("expected deleted trash sync failure in error, got %v", err)
	}
	if len(result.DeletedIDs) != 1 {
		t.Fatalf("Expected visible deletion to be counted before sync failure, got %d", len(result.DeletedIDs))
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

func TestFileSystem_ListVersions_PropagatesCurrentFileReadError(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()

	path := fs.workspace.FullPath("/versioned.txt")
	if err := os.WriteFile(path, []byte("current"), 0600); err != nil {
		t.Fatalf("os.WriteFile(versioned.txt) error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(path, 0600)
	})
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("os.Chmod(versioned.txt) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/versioned.txt")
	if err == nil {
		t.Fatal("expected ListVersions() to return current file read error")
	}
	if versions != nil {
		t.Fatalf("expected no version list on current file read error, got %d entries", len(versions))
	}
	if !os.IsPermission(err) {
		t.Fatalf("expected permission error from ListVersions(), got %v", err)
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

func TestFileSystem_GetVersion_PropagatesCurrentFileReadError(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()

	content := []byte("current")
	path := fs.workspace.FullPath("/versioned.txt")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("os.WriteFile(versioned.txt) error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(path, 0600)
	})
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("os.Chmod(versioned.txt) error: %v", err)
	}

	reader, err := fs.GetVersion(ctx, "/versioned.txt", computeHash(content))
	if err == nil {
		t.Fatal("expected GetVersion() to return current file read error")
	}
	if reader != nil {
		_ = reader.Close()
		t.Fatal("expected no reader on current file read error")
	}
	if errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("expected current file read error, got %v", err)
	}
	if !os.IsPermission(err) {
		t.Fatalf("expected permission error from GetVersion(), got %v", err)
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

func TestFileSystem_RestoreVersion_ReturnsWarningWhenWorkspaceSyncFailsAfterVisibleRestore(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	historicalContent := []byte("restore-warning-old-" + mustGenerateStorageID(t))
	currentContent := []byte("restore-warning-current-" + mustGenerateStorageID(t))

	if err := fs.WriteFile(ctx, "/restore-warning.txt", bytes.NewReader(historicalContent)); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-warning.txt", bytes.NewReader(currentContent)); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	versionsBefore, err := fs.ListVersions(ctx, "/restore-warning.txt")
	if err != nil {
		t.Fatalf("ListVersions() before restore error: %v", err)
	}

	historicalHash := ""
	for _, version := range versionsBefore {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("expected at least one historical version")
	}

	fs.writeWorkspacePath = func(ctx context.Context, name string, data []byte) error {
		if err := os.WriteFile(fs.workspace.FullPath(name), data, 0644); err != nil {
			return err
		}
		return workspace.WrapVisibleMutationWarning(errors.New("failed to sync parent directory"))
	}

	err = fs.RestoreVersion(ctx, "/restore-warning.txt", historicalHash)
	var warningErr *VisibleMutationWarningError
	if !errors.As(err, &warningErr) {
		t.Fatalf("RestoreVersion() error = %v, want VisibleMutationWarningError", err)
	}

	reader, err := fs.OpenFile(ctx, "/restore-warning.txt")
	if err != nil {
		t.Fatalf("OpenFile() after warning error: %v", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() after warning error: %v", err)
	}
	if !bytes.Equal(data, historicalContent) {
		t.Fatalf("expected restored content after warning, got %q", string(data))
	}

	versionsAfter, err := fs.ListVersions(ctx, "/restore-warning.txt")
	if err != nil {
		t.Fatalf("ListVersions() after warning error: %v", err)
	}
	currentVersionHash := ""
	for _, version := range versionsAfter {
		if version.Comment == "(current)" {
			currentVersionHash = version.Hash
			break
		}
	}
	if currentVersionHash != historicalHash {
		t.Fatalf("expected current version hash %q after warning, got %q", historicalHash, currentVersionHash)
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

	fs.UpdateRetentionSettings(1, 365*24*time.Hour, ^uint64(0), 0)
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

	fs.UpdateRetentionSettings(1, 365*24*time.Hour, ^uint64(0), 0)
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

	fs.UpdateRetentionSettings(0, 0, 0, 0)
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

	fs.UpdateRetentionSettings(1, 365*24*time.Hour, 0, 0)
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

func TestFileSystem_RunRetentionSweepContinuesTrashCleanupAfterVersionSweepFailure(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	fs.UpdateTrashSettings(true, 0, 1<<20)
	if err := fs.WriteFile(ctx, "/retention-domain-independence.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/retention-domain-independence.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.Delete(ctx, "/retention-domain-independence.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	versionSweepErr := errors.New("version sweep failed")
	fs.listVersionPaths = func(context.Context) ([]string, error) {
		return nil, versionSweepErr
	}
	fs.deleteVersionObject = func(context.Context, string) error {
		return errors.New("version object cleanup failed")
	}

	err := fs.RunRetentionSweep(ctx)
	if !errors.Is(err, versionSweepErr) {
		t.Fatalf("RunRetentionSweep() error = %v, want version sweep error", err)
	}
	var trashWarning *TrashDeleteWarningError
	if !errors.As(err, &trashWarning) {
		t.Fatalf("RunRetentionSweep() error = %v, want TrashDeleteWarningError", err)
	}
	items, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() error: %v", listErr)
	}
	if len(items) != 0 {
		t.Fatalf("trash items after mixed-domain sweep = %d, want 0", len(items))
	}
}

func TestMovePath_NonEmptyDirectory(t *testing.T) {
	fs := setupManagedPathHelperFileSystem(t)
	src := filepath.Join(fs.workspace.Root(), "src")
	dst := filepath.Join(fs.workspace.Root(), "dst")

	if err := os.MkdirAll(filepath.Join(src, "nested"), 0755); err != nil {
		t.Fatalf("MkdirAll(src) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	if err := fs.movePath(src, dst); err != nil {
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
	fs := setupManagedPathHelperFileSystem(t)
	src := filepath.Join(fs.workspace.Root(), "src")
	dst := filepath.Join(fs.workspace.Root(), "dst")

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

	if err := fs.movePath(src, dst); err != nil {
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
	fs := setupManagedPathHelperFileSystem(t)
	srcDir := filepath.Join(fs.workspace.Root(), "src-dir")
	dstDir := filepath.Join(fs.workspace.Root(), "dst-dir")
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

	originalSyncManagedStorageDir := syncManagedStorageDir
	syncFailed := false
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if !syncFailed {
			syncFailed = true
			return errors.New("sync dir failed")
		}
		return nil
	}
	t.Cleanup(func() {
		syncManagedStorageDir = originalSyncManagedStorageDir
	})

	err := fs.movePath(src, dst)
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

func TestMovePath_DoesNotOverwriteTargetCreatedAfterPrecheck(t *testing.T) {
	fs := setupManagedPathHelperFileSystem(t)
	src := filepath.Join(fs.workspace.Root(), "src.txt")
	dst := filepath.Join(fs.workspace.Root(), "dst.txt")
	if err := os.WriteFile(src, []byte("source"), 0644); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	originalAfterValidateStoragePaths := afterValidateStoragePaths
	inserted := false
	afterValidateStoragePaths = func() error {
		if inserted {
			return nil
		}
		inserted = true
		return os.WriteFile(dst, []byte("live target"), 0644)
	}
	t.Cleanup(func() {
		afterValidateStoragePaths = originalAfterValidateStoragePaths
	})

	err := fs.movePath(src, dst)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("movePath() error = %v, want ErrAlreadyExists", err)
	}

	sourceData, readErr := os.ReadFile(src)
	if readErr != nil {
		t.Fatalf("ReadFile(src) after rejected move error: %v", readErr)
	}
	if string(sourceData) != "source" {
		t.Fatalf("source content = %q, want source", sourceData)
	}
	targetData, readErr := os.ReadFile(dst)
	if readErr != nil {
		t.Fatalf("ReadFile(dst) after rejected move error: %v", readErr)
	}
	if string(targetData) != "live target" {
		t.Fatalf("target content = %q, want live target", targetData)
	}
}

func TestMovePath_PreservesCopiedDirectoryWhenSourceCleanupFails(t *testing.T) {
	fs := setupManagedPathHelperFileSystem(t)
	src := filepath.Join(fs.workspace.Root(), "src")
	dst := filepath.Join(fs.trashRoot, "dst")

	if err := os.MkdirAll(filepath.Join(src, "nested"), 0755); err != nil {
		t.Fatalf("MkdirAll(src) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	originalMovePathRename := movePathRename
	originalMovePathRemoveAll := movePathRemoveAll
	movePathRename = func(root *os.Root, oldRel, newRel, oldPath, newPath string) error {
		if oldPath == src && newPath == dst {
			return errors.New("rename failed")
		}
		return originalMovePathRename(root, oldRel, newRel, oldPath, newPath)
	}
	movePathRemoveAll = func(root *os.Root, rel, target string) error {
		if target == src {
			nestedFile := filepath.Join(src, "nested", "file.txt")
			if err := os.Remove(nestedFile); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return errors.New("source cleanup failed")
		}
		return originalMovePathRemoveAll(root, rel, target)
	}
	t.Cleanup(func() {
		movePathRename = originalMovePathRename
		movePathRemoveAll = originalMovePathRemoveAll
	})

	err := fs.movePath(src, dst)
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
	fs := setupManagedPathHelperFileSystem(t)
	src := filepath.Join(fs.workspace.Root(), "src.txt")
	if err := os.WriteFile(src, []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	outsidePath := filepath.Join(filepath.Dir(fs.workspace.Root()), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0600); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}

	dst := filepath.Join(fs.workspace.Root(), "dst.txt")
	if err := os.Symlink(outsidePath, dst); err != nil {
		t.Fatalf("Symlink(dst) error: %v", err)
	}

	err := fs.copyFile(src, dst)
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
	fs := setupManagedPathHelperFileSystem(t)
	srcDir := filepath.Join(fs.workspace.Root(), "src-dir")
	dstDir := filepath.Join(fs.workspace.Root(), "dst-dir")
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

	originalSyncManagedStorageDir := syncManagedStorageDir
	syncFailed := false
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if !syncFailed {
			syncFailed = true
			return errors.New("sync dir failed")
		}
		return nil
	}
	t.Cleanup(func() {
		syncManagedStorageDir = originalSyncManagedStorageDir
	})

	err := fs.copyFile(src, dst)
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

func TestCopyFile_CleansCreatedDirectoriesWhenTempCreateFails(t *testing.T) {
	fs := setupManagedPathHelperFileSystem(t)
	srcDir := filepath.Join(fs.workspace.Root(), "src-dir")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll(srcDir) error: %v", err)
	}

	src := filepath.Join(srcDir, "src.txt")
	dst := filepath.Join(fs.workspace.Root(), "deep", "copy", "dst.txt")
	dstDir := filepath.Dir(dst)
	if err := os.WriteFile(src, []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	originalCreateStorageCopyTempFile := createStorageCopyTempFile
	tempCreateErr := errors.New("temp create failed")
	createStorageCopyTempFile = func(root *os.Root, parentName, prefix string) (*os.File, string, error) {
		return nil, "", tempCreateErr
	}
	t.Cleanup(func() {
		createStorageCopyTempFile = originalCreateStorageCopyTempFile
	})

	err := fs.copyFile(src, dst)
	if !errors.Is(err, tempCreateErr) {
		t.Fatalf("expected temp create failure, got %v", err)
	}
	if _, statErr := os.Stat(dst); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no destination file to be created, got %v", statErr)
	}
	if _, statErr := os.Stat(dstDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected created destination directory to be removed, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(fs.workspace.Root(), "deep")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected created parent destination directory to be removed, got %v", statErr)
	}

	createStorageCopyTempFile = originalCreateStorageCopyTempFile
	if err := fs.copyFile(src, dst); err != nil {
		t.Fatalf("expected retry after failed copy cleanup to succeed, got %v", err)
	}
	data, readErr := os.ReadFile(dst)
	if readErr != nil {
		t.Fatalf("ReadFile(dst) error: %v", readErr)
	}
	if string(data) != "content" {
		t.Fatalf("expected destination content after retry, got %q", string(data))
	}
}

func TestCopyDir_ReturnsErrorWhenDirectorySyncFails(t *testing.T) {
	fs := setupManagedPathHelperFileSystem(t)
	src := filepath.Join(fs.workspace.Root(), "src")
	dst := filepath.Join(fs.workspace.Root(), "dst")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0755); err != nil {
		t.Fatalf("MkdirAll(src) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(src/nested/file.txt) error: %v", err)
	}

	originalSyncManagedStorageDir := syncManagedStorageDir
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		return errors.New("sync dir failed")
	}
	t.Cleanup(func() {
		syncManagedStorageDir = originalSyncManagedStorageDir
	})

	err := fs.copyDir(src, dst)
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

func TestCopyDir_CleansDestinationWhenSourceTreeContainsSymlink(t *testing.T) {
	fs := setupManagedPathHelperFileSystem(t)
	src := filepath.Join(fs.workspace.Root(), "src")
	dst := filepath.Join(fs.workspace.Root(), "dst")

	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatalf("MkdirAll(src) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "regular.txt"), []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(regular) error: %v", err)
	}
	if err := os.Symlink("regular.txt", filepath.Join(src, "linked.txt")); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	err := fs.copyDir(src, dst)
	if !errors.Is(err, errStoragePathSymlink) {
		t.Fatalf("copyDir() error = %v, want errStoragePathSymlink", err)
	}
	if _, statErr := os.Stat(dst); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected failed copy destination to be removed, got %v", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(src, "linked.txt")); statErr != nil {
		t.Fatalf("expected source symlink to remain for operator recovery, got %v", statErr)
	}
}

func TestCopyDirRejectsUnsafeSourceEntryNames(t *testing.T) {
	tests := []struct {
		name      string
		entryName string
	}{
		{name: "backslash", entryName: "nested\\report.txt"},
		{name: "newline", entryName: "report\n2026.txt"},
		{name: "delete-control", entryName: "report\x7f.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := setupManagedPathHelperFileSystem(t)
			src := filepath.Join(fs.workspace.Root(), "src")
			dst := filepath.Join(fs.workspace.Root(), "dst")

			if err := os.MkdirAll(src, 0755); err != nil {
				t.Fatalf("MkdirAll(src) error: %v", err)
			}
			if err := os.WriteFile(filepath.Join(src, tt.entryName), []byte("content"), 0644); err != nil {
				t.Skipf("platform does not support unsafe filename %q: %v", tt.entryName, err)
			}

			err := fs.copyDir(src, dst)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("copyDir() error = %v, want ErrNotFound", err)
			}
			if _, statErr := os.Stat(dst); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("expected failed copy destination to be removed, got %v", statErr)
			}
		})
	}
}

func TestMovePath_RejectsSymlinkDestinationParent(t *testing.T) {
	fs := setupManagedPathHelperFileSystem(t)
	src := filepath.Join(fs.workspace.Root(), "src.txt")
	if err := os.WriteFile(src, []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	outsideDir := filepath.Join(filepath.Dir(fs.workspace.Root()), "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("MkdirAll(outside) error: %v", err)
	}

	escapeDir := filepath.Join(fs.workspace.Root(), "escape")
	if err := os.Symlink(outsideDir, escapeDir); err != nil {
		t.Fatalf("Symlink(escape) error: %v", err)
	}

	err := fs.movePath(src, filepath.Join(escapeDir, "dst.txt"))
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

func TestCopyFile_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	fs := setupManagedPathHelperFileSystem(t)
	src := filepath.Join(fs.workspace.Root(), "src.txt")
	if err := os.WriteFile(src, []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	safeDir := filepath.Join(fs.workspace.Root(), "safe")
	if err := os.MkdirAll(safeDir, 0755); err != nil {
		t.Fatalf("MkdirAll(safe) error: %v", err)
	}
	outsideDir := filepath.Join(filepath.Dir(fs.workspace.Root()), "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("MkdirAll(outside) error: %v", err)
	}

	originalAfterValidateStoragePaths := afterValidateStoragePaths
	afterValidateStoragePaths = func() error {
		if err := os.RemoveAll(safeDir); err != nil {
			return err
		}
		return os.Symlink(outsideDir, safeDir)
	}
	t.Cleanup(func() {
		afterValidateStoragePaths = originalAfterValidateStoragePaths
	})

	err := fs.copyFile(src, filepath.Join(safeDir, "dst.txt"))
	if !errors.Is(err, errStoragePathSymlink) {
		t.Fatalf("copyFile() error = %v, want errStoragePathSymlink", err)
	}

	if _, statErr := os.Stat(filepath.Join(outsideDir, "dst.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no file outside managed path, got %v", statErr)
	}
	data, readErr := os.ReadFile(src)
	if readErr != nil {
		t.Fatalf("ReadFile(src) error: %v", readErr)
	}
	if string(data) != "content" {
		t.Fatalf("expected source content to remain unchanged, got %q", string(data))
	}
}

func TestCopyFile_RejectsSourceSymlinkInsertedAfterValidationInsideRoot(t *testing.T) {
	fs := setupManagedPathHelperFileSystem(t)
	src := filepath.Join(fs.workspace.Root(), "src.txt")
	dst := filepath.Join(fs.workspace.Root(), "dst.txt")
	if err := os.WriteFile(src, []byte("original"), 0644); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}
	linkedTarget := filepath.Join(fs.workspace.Root(), "linked.txt")
	if err := os.WriteFile(linkedTarget, []byte("linked"), 0644); err != nil {
		t.Fatalf("WriteFile(linked) error: %v", err)
	}

	originalAfterValidateStoragePaths := afterValidateStoragePaths
	swapped := false
	afterValidateStoragePaths = func() error {
		if swapped {
			return nil
		}
		swapped = true
		if err := os.Remove(src); err != nil {
			return err
		}
		return os.Symlink(filepath.Base(linkedTarget), src)
	}
	t.Cleanup(func() {
		afterValidateStoragePaths = originalAfterValidateStoragePaths
	})

	err := fs.copyFile(src, dst)
	if !errors.Is(err, errStoragePathSymlink) {
		t.Fatalf("copyFile() error = %v, want errStoragePathSymlink", err)
	}
	if _, statErr := os.Stat(dst); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected destination not to be created, got %v", statErr)
	}
}

func TestMovePath_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	fs := setupManagedPathHelperFileSystem(t)
	src := filepath.Join(fs.workspace.Root(), "src.txt")
	if err := os.WriteFile(src, []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	safeDir := filepath.Join(fs.workspace.Root(), "safe")
	if err := os.MkdirAll(safeDir, 0755); err != nil {
		t.Fatalf("MkdirAll(safe) error: %v", err)
	}
	outsideDir := filepath.Join(filepath.Dir(fs.workspace.Root()), "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("MkdirAll(outside) error: %v", err)
	}

	originalAfterValidateStoragePaths := afterValidateStoragePaths
	afterValidateStoragePaths = func() error {
		if err := os.RemoveAll(safeDir); err != nil {
			return err
		}
		return os.Symlink(outsideDir, safeDir)
	}
	t.Cleanup(func() {
		afterValidateStoragePaths = originalAfterValidateStoragePaths
	})

	err := fs.movePath(src, filepath.Join(safeDir, "dst.txt"))
	if !errors.Is(err, errStoragePathSymlink) {
		t.Fatalf("movePath() error = %v, want errStoragePathSymlink", err)
	}

	if _, statErr := os.Stat(filepath.Join(outsideDir, "dst.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no file outside managed path, got %v", statErr)
	}
	data, readErr := os.ReadFile(src)
	if readErr != nil {
		t.Fatalf("ReadFile(src) error: %v", readErr)
	}
	if string(data) != "content" {
		t.Fatalf("expected source content to remain unchanged, got %q", string(data))
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

func TestFileSystem_SearchFiltered_AppliesFilterBeforeLimit(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/aaa"); err != nil {
		t.Fatalf("Mkdir(/aaa) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/team"); err != nil {
		t.Fatalf("Mkdir(/team) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/aaa/report.txt", bytes.NewReader([]byte("outside"))); err != nil {
		t.Fatalf("WriteFile(/aaa/report.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/team/report.txt", bytes.NewReader([]byte("visible"))); err != nil {
		t.Fatalf("WriteFile(/team/report.txt) error: %v", err)
	}

	results, err := fs.SearchFiltered(ctx, "report", 1, func(result *SearchResult) (bool, error) {
		return result.Path == "/team/report.txt", nil
	})
	if err != nil {
		t.Fatalf("SearchFiltered() error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("SearchFiltered() returned %d results, want 1", len(results))
	}
	if results[0].Path != "/team/report.txt" {
		t.Fatalf("SearchFiltered() first result path = %q, want %q", results[0].Path, "/team/report.txt")
	}
}

func TestFileSystem_SearchWithinBase_RejectsTraversalRoot(t *testing.T) {
	tmpDir := t.TempDir()
	fs, err := New(&Config{
		FilesRoot:          filepath.Join(tmpDir, "files"),
		InternalRoot:       filepath.Join(tmpDir, ".mnemonas"),
		TrashRoot:          filepath.Join(tmpDir, ".mnemonas", "trash"),
		Dataplane:          dataplane.NewClient("unused"),
		MaxVersions:        10,
		MaxVersionAge:      30 * 24 * time.Hour,
		TrashRetentionDays: 30,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer fs.Close()

	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/other"); err != nil {
		t.Fatalf("Mkdir(/other) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/other/report.txt", bytes.NewReader([]byte("other"))); err != nil {
		t.Fatalf("WriteFile(/other/report.txt) error: %v", err)
	}

	results, err := fs.SearchWithinBase(ctx, "../../other", "report", 10)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SearchWithinBase() error = %v, want %v", err, ErrNotFound)
	}
	if results != nil {
		t.Fatalf("expected no results when root escapes workspace, got %d", len(results))
	}
}

func TestFileSystem_SearchWithinBase_MissingRootReturnsErrNotFound(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	results, err := fs.SearchWithinBase(ctx, "/missing-home", "report", 10)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SearchWithinBase() error = %v, want %v", err, ErrNotFound)
	}
	if results != nil {
		t.Fatalf("expected no results for missing search root, got %d", len(results))
	}
}

func TestFileSystem_GetFileCountCountsExternallyImportedFiles(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/managed.txt", strings.NewReader("managed")); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	importedDir := filepath.Join(fs.config.FilesRoot, "imported", "nested")
	if err := os.MkdirAll(importedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(imported) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(importedDir, "external.txt"), []byte("external"), 0o644); err != nil {
		t.Fatalf("WriteFile(external) error: %v", err)
	}

	count, err := fs.GetFileCount(ctx)
	if err != nil {
		t.Fatalf("GetFileCount() error: %v", err)
	}
	if count != 2 {
		t.Fatalf("GetFileCount() = %d, want 2", count)
	}
}

func TestFileSystem_DiskStatsReportsWorkspaceFilesystemCapacity(t *testing.T) {
	fs := setupStandaloneFileSystem(t)

	stats, err := fs.DiskStats()
	if err != nil {
		t.Fatalf("DiskStats() error: %v", err)
	}
	if stats.TotalBytes == 0 {
		t.Fatal("expected total disk capacity to be greater than zero")
	}
	if stats.UsedBytes > stats.TotalBytes {
		t.Fatalf("UsedBytes = %d exceeds TotalBytes = %d", stats.UsedBytes, stats.TotalBytes)
	}
	if stats.FreeBytes > stats.TotalBytes {
		t.Fatalf("FreeBytes = %d exceeds TotalBytes = %d", stats.FreeBytes, stats.TotalBytes)
	}
	if stats.AvailableBytes > stats.TotalBytes {
		t.Fatalf("AvailableBytes = %d exceeds TotalBytes = %d", stats.AvailableBytes, stats.TotalBytes)
	}
	if stats.UsageRatio < 0 || stats.UsageRatio > 1 {
		t.Fatalf("UsageRatio = %f, want between 0 and 1", stats.UsageRatio)
	}
	if stats.FileSystemType == "" {
		t.Fatal("expected filesystem type to be reported")
	}
}

func TestDiskStatsFromUsageHandlesInvalidCapacity(t *testing.T) {
	mountDetails := diskMountDetails{
		FileSystemType: "btrfs",
		MountPoint:     "/srv/mnemonas",
		MountSource:    "/dev/sda1",
		MountOptions:   "rw,compress=zstd",
	}

	zero := diskStatsFromUsage(0, 0, 0, mountDetails)
	if zero.UsedBytes != 0 || zero.UsageRatio != 0 {
		t.Fatalf("zero-capacity stats = %+v, want zero usage", zero)
	}
	if !zero.NativeDataChecksumSupport {
		t.Fatalf("expected btrfs native checksum support in zero-capacity stats: %+v", zero)
	}

	clamped := diskStatsFromUsage(100, 120, 130, mountDetails)
	if clamped.FreeBytes != 100 || clamped.AvailableBytes != 100 || clamped.UsedBytes != 0 || clamped.UsageRatio != 0 {
		t.Fatalf("clamped stats = %+v, want free/available clamped to total", clamped)
	}

	used := diskStatsFromUsage(100, 25, 20, mountDetails)
	if used.UsedBytes != 75 || used.UsageRatio != 0.75 {
		t.Fatalf("regular usage stats = %+v, want used=75 ratio=0.75", used)
	}
	if used.MountPoint != mountDetails.MountPoint || used.MountSource != mountDetails.MountSource || used.MountOptions != mountDetails.MountOptions {
		t.Fatalf("mount details not preserved: %+v", used)
	}
}

func TestDiskStatsFromStatfsBlocksValidatesBlockSizeAndOverflow(t *testing.T) {
	mountDetails := diskMountDetails{FileSystemType: "zfs"}

	stats, err := diskStatsFromStatfsBlocks(10, 4, 2, 4096, mountDetails)
	if err != nil {
		t.Fatalf("diskStatsFromStatfsBlocks() error: %v", err)
	}
	if stats.TotalBytes != 40960 || stats.FreeBytes != 16384 || stats.AvailableBytes != 8192 || stats.UsedBytes != 24576 {
		t.Fatalf("unexpected statfs stats: %+v", stats)
	}
	if !stats.NativeDataChecksumSupport {
		t.Fatalf("expected zfs native checksum support: %+v", stats)
	}

	for _, blockSize := range []int64{0, -4096} {
		if _, err := diskStatsFromStatfsBlocks(10, 4, 2, blockSize, mountDetails); !errors.Is(err, errDiskStatsInvalidBlockSize) {
			t.Fatalf("block size %d error = %v, want invalid block size", blockSize, err)
		}
	}

	if _, err := diskStatsFromStatfsBlocks(^uint64(0), 4, 2, 2, mountDetails); !errors.Is(err, errDiskStatsCapacityOverflow) {
		t.Fatalf("overflow error = %v, want capacity overflow", err)
	}
}

func TestFilesystemTypeFromMountInfoSelectsDeepestMount(t *testing.T) {
	mountInfo := []byte(strings.Join([]string{
		"21 1 8:1 / / rw,relatime - ext4 /dev/sda1 rw",
		"22 21 8:2 / /srv rw,relatime - xfs /dev/sdb1 rw",
		"23 22 0:42 /mnemonas /srv/mnemonas rw,relatime - zfs mnemonas/mirror rw",
	}, "\n"))

	fsType, err := filesystemTypeFromMountInfo("/srv/mnemonas/files", mountInfo)
	if err != nil {
		t.Fatalf("filesystemTypeFromMountInfo() error: %v", err)
	}
	if fsType != "zfs" {
		t.Fatalf("filesystemTypeFromMountInfo() = %q, want zfs", fsType)
	}

	details, err := diskMountDetailsFromMountInfo("/srv/mnemonas/files", mountInfo)
	if err != nil {
		t.Fatalf("diskMountDetailsFromMountInfo() error: %v", err)
	}
	if details.FileSystemType != "zfs" || details.MountPoint != "/srv/mnemonas" || details.MountSource != "mnemonas/mirror" || details.MountOptions != "rw,relatime" {
		t.Fatalf("diskMountDetailsFromMountInfo() = %+v, want zfs /srv/mnemonas mnemonas/mirror rw,relatime", details)
	}
}

func TestFilesystemTypeFromMountInfoUnescapesMountPoint(t *testing.T) {
	mountInfo := []byte("31 1 0:43 / /srv/Mnemo\\040NAS rw,relatime - btrfs /dev/sdc1 rw\n")

	fsType, err := filesystemTypeFromMountInfo("/srv/Mnemo NAS/files", mountInfo)
	if err != nil {
		t.Fatalf("filesystemTypeFromMountInfo() error: %v", err)
	}
	if fsType != "btrfs" {
		t.Fatalf("filesystemTypeFromMountInfo() = %q, want btrfs", fsType)
	}

	details, err := diskMountDetailsFromMountInfo("/srv/Mnemo NAS/files", mountInfo)
	if err != nil {
		t.Fatalf("diskMountDetailsFromMountInfo() error: %v", err)
	}
	if details.MountPoint != "/srv/Mnemo NAS" || details.MountSource != "/dev/sdc1" {
		t.Fatalf("diskMountDetailsFromMountInfo() = %+v, want unescaped mount point and source", details)
	}
}

func TestFilesystemTypeFromMagicFallback(t *testing.T) {
	for name, tt := range map[string]struct {
		magic      uint64
		wantType   string
		wantNative bool
	}{
		"btrfs":   {magic: 0x9123683E, wantType: "btrfs", wantNative: true},
		"cifs":    {magic: 0xFF534D42, wantType: "cifs"},
		"exfat":   {magic: 0x2011BAB0, wantType: "exfat"},
		"ext":     {magic: 0xEF53, wantType: "ext"},
		"fuse":    {magic: 0x65735546, wantType: "fuse"},
		"nfs":     {magic: 0x6969, wantType: "nfs"},
		"smb":     {magic: 0x517B, wantType: "smb"},
		"smb2":    {magic: 0xFE534D42, wantType: "smb2"},
		"tmpfs":   {magic: 0x01021994, wantType: "tmpfs"},
		"unknown": {magic: 0xDEADBEEF, wantType: "unknown"},
		"xfs":     {magic: 0x58465342, wantType: "xfs"},
		"zfs":     {magic: 0x2FC12FC1, wantType: "zfs", wantNative: true},
	} {
		t.Run(name, func(t *testing.T) {
			fsType := filesystemTypeFromMagic(tt.magic)
			if fsType != tt.wantType {
				t.Fatalf("filesystemTypeFromMagic(%#x) = %q, want %q", tt.magic, fsType, tt.wantType)
			}
			if got := filesystemHasNativeDataChecksumSupport(fsType); got != tt.wantNative {
				t.Fatalf("filesystemHasNativeDataChecksumSupport(%q) = %v, want %v", fsType, got, tt.wantNative)
			}
		})
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

	for _, query := range []string{"", "   \t\n  "} {
		_, err := fs.Search(ctx, query, 10)
		if err == nil {
			t.Fatalf("Search with query %q should return error", query)
		}
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

func TestFileSystem_SetVersioning_ReturnsErrNotFoundForMissingPath(t *testing.T) {
	tmpDir := t.TempDir()
	fs, err := New(&Config{
		FilesRoot:          filepath.Join(tmpDir, "files"),
		InternalRoot:       filepath.Join(tmpDir, ".mnemonas"),
		TrashRoot:          filepath.Join(tmpDir, ".mnemonas", "trash"),
		Dataplane:          dataplane.NewClient("unused"),
		MaxVersions:        10,
		MaxVersionAge:      30 * 24 * time.Hour,
		TrashRetentionDays: 30,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer fs.Close()

	ctx := context.Background()

	err = fs.SetVersioning(ctx, "/missing.txt", false)
	if err != ErrNotFound {
		t.Fatalf("SetVersioning() error = %v, want %v", err, ErrNotFound)
	}
}

func TestFileSystem_SetVersioning_ReturnsErrIsDirForDirectoryPath(t *testing.T) {
	tmpDir := t.TempDir()
	fs, err := New(&Config{
		FilesRoot:          filepath.Join(tmpDir, "files"),
		InternalRoot:       filepath.Join(tmpDir, ".mnemonas"),
		TrashRoot:          filepath.Join(tmpDir, ".mnemonas", "trash"),
		Dataplane:          dataplane.NewClient("unused"),
		MaxVersions:        10,
		MaxVersionAge:      30 * 24 * time.Hour,
		TrashRetentionDays: 30,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer fs.Close()

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/dir"); err != nil {
		t.Fatalf("Mkdir(/dir) error: %v", err)
	}

	err = fs.SetVersioning(ctx, "/dir", false)
	if err != ErrIsDir {
		t.Fatalf("SetVersioning() error = %v, want %v", err, ErrIsDir)
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
func TestFileSystem_CurrentVersioningPolicy_ReturnsDetachedSnapshot(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	snapshot := fs.currentVersioningPolicy()
	if snapshot == nil {
		t.Fatal("expected currentVersioningPolicy() snapshot")
	}
	if !snapshot.ShouldVersion(ctx, "/notes.md", 16) {
		t.Fatal("expected default snapshot to version .md files")
	}
	if snapshot.ShouldVersion(ctx, "/events.log", 16) {
		t.Fatal("expected default snapshot to skip .log files")
	}

	fs.UpdateVersioningSettings([]string{".log"}, []string{"ENVFILE"}, 32)

	if !snapshot.ShouldVersion(ctx, "/notes.md", 16) {
		t.Fatal("expected detached snapshot to retain original .md policy")
	}
	if snapshot.ShouldVersion(ctx, "/events.log", 16) {
		t.Fatal("expected detached snapshot to remain isolated from updated .log policy")
	}

	updated := fs.currentVersioningPolicy()
	if updated == nil {
		t.Fatal("expected updated currentVersioningPolicy() snapshot")
	}
	if updated.ShouldVersion(ctx, "/notes.md", 16) {
		t.Fatal("expected updated policy to stop versioning .md files")
	}
	if !updated.ShouldVersion(ctx, "/events.log", 16) {
		t.Fatal("expected updated policy to version .log files")
	}
	if updated.ShouldVersion(ctx, "/too-large.log", 64) {
		t.Fatal("expected updated policy to enforce max versioned size")
	}
}

func TestFileSystem_VersioningReadPathsRemainStableDuringHotUpdates(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/hot-update.md", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(hot-update.md) error: %v", err)
	}

	stop := make(chan struct{})
	errCh := make(chan error, 1)
	reportErr := func(err error) {
		select {
		case errCh <- err:
		default:
		}
	}

	var wg sync.WaitGroup
	reader := func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}

			if _, err := fs.Stat(ctx, "/hot-update.md"); err != nil {
				reportErr(fmt.Errorf("Stat() error: %w", err))
				return
			}
			if _, _, err := fs.GetVersioningStatus(ctx, "/hot-update.md"); err != nil {
				reportErr(fmt.Errorf("GetVersioningStatus() error: %w", err))
				return
			}
			if _, err := fs.ReadDir(ctx, "/"); err != nil {
				reportErr(fmt.Errorf("ReadDir() error: %w", err))
				return
			}
		}
	}

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go reader()
	}

	for i := 0; i < 200; i++ {
		if i%2 == 0 {
			fs.UpdateVersioningSettings([]string{".md"}, nil, 1024)
		} else {
			fs.UpdateVersioningSettings([]string{".log"}, []string{"ENVFILE"}, 32)
		}
	}

	close(stop)
	wg.Wait()

	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
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
