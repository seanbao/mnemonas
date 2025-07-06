package versionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/dataplane"
)

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

func TestRollbackRenamedVersionStoreFiles_RestoresInReverseOrder(t *testing.T) {
	tmpDir := t.TempDir()
	root, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	if err := os.WriteFile(filepath.Join(tmpDir, "mnemonas.db.corrupt"), []byte("db"), 0600); err != nil {
		t.Fatalf("WriteFile(db backup) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "mnemonas.db.corrupt-wal"), []byte("wal"), 0600); err != nil {
		t.Fatalf("WriteFile(wal backup) error: %v", err)
	}

	err = rollbackRenamedVersionStoreFiles(root, [][2]string{
		{"mnemonas.db", "mnemonas.db.corrupt"},
		{"mnemonas.db-wal", "mnemonas.db.corrupt-wal"},
	})
	if err != nil {
		t.Fatalf("rollbackRenamedVersionStoreFiles() error: %v", err)
	}

	for name, want := range map[string]string{
		"mnemonas.db":     "db",
		"mnemonas.db-wal": "wal",
	} {
		data, err := os.ReadFile(filepath.Join(tmpDir, name))
		if err != nil {
			t.Fatalf("ReadFile(%s) error: %v", name, err)
		}
		if string(data) != want {
			t.Fatalf("%s contents = %q, want %q", name, string(data), want)
		}
	}
}

func TestRollbackRenamedVersionStoreFiles_ReturnsRenameErrors(t *testing.T) {
	tmpDir := t.TempDir()
	root, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	err = rollbackRenamedVersionStoreFiles(root, [][2]string{
		{"mnemonas.db", "missing-backup"},
	})
	if err == nil {
		t.Fatal("expected rollback error")
	}
	if !strings.Contains(err.Error(), "missing-backup") {
		t.Fatalf("expected missing backup path in rollback error, got %v", err)
	}
}

func TestRecoverCorruptVersionStoreDatabase_ReturnsUnrecoverableInitError(t *testing.T) {
	initErr := errors.New("permission denied")
	err := recoverCorruptVersionStoreDatabase(filepath.Join(t.TempDir(), "mnemonas.db"), initErr)
	if err != initErr {
		t.Fatalf("recoverCorruptVersionStoreDatabase() = %v, want original error", err)
	}
}

func setupStore(t *testing.T) *Store {
	client := setupDataplaneClient(t)
	if client == nil {
		t.Skip("dataplane not available, skipping test")
	}

	tmpDir := t.TempDir()
	s, err := New(Config{
		DBPath:    filepath.Join(tmpDir, "test.db"),
		Dataplane: client,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNew(t *testing.T) {
	client := setupDataplaneClient(t)
	if client == nil {
		t.Skip("dataplane not available, skipping test")
	}

	tmpDir := t.TempDir()
	s, err := New(Config{
		DBPath:    filepath.Join(tmpDir, "test.db"),
		Dataplane: client,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer s.Close()

	// Check database file was created
	if _, err := os.Stat(filepath.Join(tmpDir, "test.db")); err != nil {
		t.Errorf("Database file not created: %v", err)
	}
}

func TestNew_RecoversFromCorruptDatabaseFile(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0600); err != nil {
		t.Fatalf("WriteFile(corrupt db) error: %v", err)
	}

	store, err := New(Config{
		DBPath:    dbPath,
		Dataplane: dataplane.NewClient("unused"),
	})
	if err != nil {
		t.Fatalf("New() with corrupt database should recover, got %v", err)
	}
	defer store.Close()

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir(tmpDir) error: %v", err)
	}
	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "test.db.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt database backup to be created")
	}

	if _, err := store.ListTrash(context.Background()); err != nil {
		t.Fatalf("expected recovered database to be usable, got %v", err)
	}
}

func TestIsRecoverableVersionStoreSQLiteError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "corrupt message", err: errors.New("database disk image is malformed"), want: true},
		{name: "not a database message", err: errors.New("file is not a database"), want: true},
		{name: "sqlite composite message", err: errors.New("file is encrypted or is not a database"), want: true},
		{name: "unrelated", err: errors.New("permission denied"), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRecoverableVersionStoreSQLiteError(tc.err); got != tc.want {
				t.Fatalf("isRecoverableVersionStoreSQLiteError() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNew_RequiresDataplane(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := New(Config{
		DBPath:    filepath.Join(tmpDir, "test.db"),
		Dataplane: nil,
	})
	if err == nil {
		t.Error("Expected error when Dataplane is nil")
	}
}

func TestStore_SetDataplaneClient(t *testing.T) {
	first := dataplane.NewClient("first")
	second := dataplane.NewClient("second")
	store := &Store{objects: NewObjectStore(first)}

	store.SetDataplaneClient(second)
	if got := store.objects.getClient(); got != second {
		t.Fatalf("dataplane client = %#v, want second client", got)
	}

	(*Store)(nil).SetDataplaneClient(first)
	(&Store{}).SetDataplaneClient(first)
}

func TestNew_ReturnsDirectoryTreeSyncError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nested", "db", "test.db")

	originalSyncVersionStoreDir := syncVersionStoreDir
	syncVersionStoreDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncVersionStoreDir = originalSyncVersionStoreDir
	}()

	_, err := New(Config{
		DBPath:    dbPath,
		Dataplane: dataplane.NewClient("unused"),
	})
	if err == nil {
		t.Fatal("expected New() to fail when version store directory tree sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync version store directory tree") {
		t.Fatalf("expected directory tree sync error, got %v", err)
	}
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no database file to be created, got %v", statErr)
	}
}

func TestNew_RejectsSymlinkDBDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "real-db")
	if err := os.MkdirAll(realDir, 0700); err != nil {
		t.Fatalf("MkdirAll(realDir) error: %v", err)
	}
	linkDir := filepath.Join(tmpDir, "db-link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatalf("Symlink(linkDir) error: %v", err)
	}

	_, err := New(Config{
		DBPath:    filepath.Join(linkDir, "test.db"),
		Dataplane: dataplane.NewClient("unused"),
	})
	if !errors.Is(err, errVersionStoreSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestEnsureVersionStoreDir_RejectsSymlinkParent(t *testing.T) {
	tmpDir := t.TempDir()
	realParent := filepath.Join(tmpDir, "real-parent")
	if err := os.MkdirAll(realParent, 0700); err != nil {
		t.Fatalf("MkdirAll(realParent) error: %v", err)
	}
	linkedParent := filepath.Join(tmpDir, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("Symlink(linkedParent) error: %v", err)
	}

	err := ensureVersionStoreDir(filepath.Join(linkedParent, "db"), 0700)
	if !errors.Is(err, errVersionStoreSymlink) {
		t.Fatalf("ensureVersionStoreDir() error = %v, want errVersionStoreSymlink", err)
	}
	if _, statErr := os.Stat(filepath.Join(realParent, "db")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no directory to be created through symlink parent, got %v", statErr)
	}
}

func TestNew_DoesNotFollowDBDirectorySymlinkInsertedAfterValidation(t *testing.T) {
	tmpDir := t.TempDir()
	dbDir := filepath.Join(tmpDir, "db")
	if err := os.MkdirAll(dbDir, 0700); err != nil {
		t.Fatalf("MkdirAll(dbDir) error: %v", err)
	}
	dbPath := filepath.Join(dbDir, "test.db")

	outsideDir := t.TempDir()
	backupDir := dbDir + "-backup"
	originalHook := afterValidateVersionStorePath
	afterValidateVersionStorePath = func() {
		if err := os.Rename(dbDir, backupDir); err != nil {
			t.Fatalf("Rename(dbDir) error: %v", err)
		}
		if err := os.Symlink(outsideDir, dbDir); err != nil {
			t.Fatalf("Symlink(dbDir) error: %v", err)
		}
	}
	t.Cleanup(func() {
		afterValidateVersionStorePath = originalHook
		if info, err := os.Lstat(dbDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(dbDir); removeErr != nil {
				t.Errorf("Remove(dbDir symlink) error: %v", removeErr)
			}
		}
		if _, err := os.Stat(backupDir); err == nil {
			if renameErr := os.Rename(backupDir, dbDir); renameErr != nil {
				t.Errorf("Rename(backupDir) error: %v", renameErr)
			}
		}
	})

	store, err := New(Config{
		DBPath:    dbPath,
		Dataplane: dataplane.NewClient("unused"),
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer store.Close()

	anchoredDBPath := filepath.Join(backupDir, "test.db")
	if _, err := os.Stat(anchoredDBPath); err != nil {
		t.Fatalf("expected anchored database file, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "test.db")); !os.IsNotExist(err) {
		t.Fatalf("expected no outside database file, got %v", err)
	}
}

func TestNew_RejectsDBFileSymlinkInsertedAfterValidation(t *testing.T) {
	tmpDir := t.TempDir()
	dbDir := filepath.Join(tmpDir, "db")
	if err := os.MkdirAll(dbDir, 0700); err != nil {
		t.Fatalf("MkdirAll(dbDir) error: %v", err)
	}
	dbPath := filepath.Join(dbDir, "test.db")
	if err := os.WriteFile(dbPath, []byte("placeholder"), 0600); err != nil {
		t.Fatalf("WriteFile(test.db) error: %v", err)
	}
	linkedPath := filepath.Join(dbDir, "linked.db")
	if err := os.WriteFile(linkedPath, []byte("linked"), 0600); err != nil {
		t.Fatalf("WriteFile(linked.db) error: %v", err)
	}

	originalHook := afterValidateVersionStorePath
	var hookErr error
	swapped := false
	afterValidateVersionStorePath = func() {
		if hookErr != nil || swapped {
			return
		}
		swapped = true
		if err := os.Remove(dbPath); err != nil {
			hookErr = err
			return
		}
		hookErr = os.Symlink(filepath.Base(linkedPath), dbPath)
	}
	t.Cleanup(func() {
		afterValidateVersionStorePath = originalHook
	})

	_, err := New(Config{
		DBPath:    dbPath,
		Dataplane: dataplane.NewClient("unused"),
	})
	if hookErr != nil {
		t.Fatalf("afterValidateVersionStorePath hook error: %v", hookErr)
	}
	if !errors.Is(err, errVersionStoreSymlink) {
		t.Fatalf("New() error = %v, want errVersionStoreSymlink", err)
	}
}

func TestEnsureVersionStoreDir_SyncsCreatedDirectoriesDeepestParentFirst(t *testing.T) {
	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "nested", "db", "store")

	originalSyncVersionStoreDir := syncVersionStoreDir
	var synced []string
	syncVersionStoreDir = func(dir string) error {
		synced = append(synced, dir)
		return nil
	}
	defer func() {
		syncVersionStoreDir = originalSyncVersionStoreDir
	}()

	if err := ensureVersionStoreDir(targetDir, 0700); err != nil {
		t.Fatalf("ensureVersionStoreDir() error: %v", err)
	}

	want := []string{
		filepath.Join(tmpDir, "nested", "db"),
		filepath.Join(tmpDir, "nested"),
		tmpDir,
	}
	if strings.Join(synced, "|") != strings.Join(want, "|") {
		t.Fatalf("synced directories = %v, want %v", synced, want)
	}
}

func TestStore_AddVersion(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	err := s.AddVersion(ctx, "/test.txt", "abc123def456", 100, "test version")
	if err != nil {
		t.Fatalf("AddVersion() error: %v", err)
	}

	versions, err := s.GetVersions(ctx, "/test.txt")
	if err != nil {
		t.Fatalf("GetVersions() error: %v", err)
	}

	if len(versions) != 1 {
		t.Fatalf("GetVersions() returned %d versions, want 1", len(versions))
	}

	v := versions[0]
	if v.Path != "/test.txt" {
		t.Errorf("Path = %s, want /test.txt", v.Path)
	}
	if v.Hash != "abc123def456" {
		t.Errorf("Hash = %s, want abc123def456", v.Hash)
	}
	if v.Size != 100 {
		t.Errorf("Size = %d, want 100", v.Size)
	}
	if v.Comment != "test version" {
		t.Errorf("Comment = %s, want 'test version'", v.Comment)
	}
}

func TestStore_GetVersion(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	s.AddVersion(ctx, "/test.txt", "hash123", 50, "")

	v, err := s.GetVersion(ctx, "/test.txt", "hash123")
	if err != nil {
		t.Fatalf("GetVersion() error: %v", err)
	}

	if v.Hash != "hash123" {
		t.Errorf("Hash = %s, want hash123", v.Hash)
	}
}

func TestStore_GetVersion_NotFound(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	_, err := s.GetVersion(ctx, "/test.txt", "nonexistent")
	if err != ErrNotFound {
		t.Errorf("GetVersion() error = %v, want ErrNotFound", err)
	}
}

func TestStore_DeleteVersions(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	s.AddVersion(ctx, "/test.txt", "hash1", 100, "")
	s.AddVersion(ctx, "/test.txt", "hash2", 200, "")

	err := s.DeleteVersions(ctx, "/test.txt")
	if err != nil {
		t.Fatalf("DeleteVersions() error: %v", err)
	}

	versions, _ := s.GetVersions(ctx, "/test.txt")
	if len(versions) != 0 {
		t.Errorf("Versions still exist after delete: %d", len(versions))
	}
}

func TestStore_DeleteVersionRestoreVersionsAndReferenceChecks(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	if err := s.AddVersion(ctx, "/docs/report.txt", "hash-keep", 100, "keep"); err != nil {
		t.Fatalf("AddVersion(keep) error: %v", err)
	}
	if err := s.AddVersion(ctx, "/docs/report.txt", "hash-delete", 200, "delete"); err != nil {
		t.Fatalf("AddVersion(delete) error: %v", err)
	}
	toRestore, err := s.GetVersion(ctx, "/docs/report.txt", "hash-delete")
	if err != nil {
		t.Fatalf("GetVersion(delete) error: %v", err)
	}

	hasRef, err := s.HasVersionReference(ctx, "hash-delete")
	if err != nil {
		t.Fatalf("HasVersionReference(before) error: %v", err)
	}
	if !hasRef {
		t.Fatal("expected hash-delete to be referenced before deletion")
	}

	if err := s.DeleteVersion(ctx, "/docs/report.txt", "hash-delete"); err != nil {
		t.Fatalf("DeleteVersion() error: %v", err)
	}
	if _, err := s.GetVersion(ctx, "/docs/report.txt", "hash-delete"); err != ErrNotFound {
		t.Fatalf("GetVersion(deleted) error = %v, want ErrNotFound", err)
	}
	hasRef, err = s.HasVersionReference(ctx, "hash-delete")
	if err != nil {
		t.Fatalf("HasVersionReference(after delete) error: %v", err)
	}
	if hasRef {
		t.Fatal("expected hash-delete reference to be absent after DeleteVersion")
	}

	if err := s.RestoreVersions(ctx, []Version{*toRestore}); err != nil {
		t.Fatalf("RestoreVersions() error: %v", err)
	}
	restored, err := s.GetVersion(ctx, "/docs/report.txt", "hash-delete")
	if err != nil {
		t.Fatalf("GetVersion(restored) error: %v", err)
	}
	if restored.ID != toRestore.ID || restored.Comment != "delete" {
		t.Fatalf("restored version = %#v, want original %#v", restored, toRestore)
	}

	if err := s.RestoreVersions(ctx, nil); err != nil {
		t.Fatalf("RestoreVersions(nil) error: %v", err)
	}
	if err := s.RestoreVersions(ctx, []Version{{Path: "../escape", Hash: "bad"}}); !errors.Is(err, errInvalidStorePath) {
		t.Fatalf("RestoreVersions(invalid path) error = %v, want %v", err, errInvalidStorePath)
	}
	if err := s.RestoreVersions(ctx, []Version{{Path: "/docs/./report.txt", Hash: "bad"}}); !errors.Is(err, errInvalidStorePath) {
		t.Fatalf("RestoreVersions(dot-segment path) error = %v, want %v", err, errInvalidStorePath)
	}
	if err := s.RestoreVersions(ctx, []Version{{Path: "/docs/report\x00.txt", Hash: "bad"}}); !errors.Is(err, errInvalidStorePath) {
		t.Fatalf("RestoreVersions(NUL path) error = %v, want %v", err, errInvalidStorePath)
	}
	if err := s.RestoreVersions(ctx, []Version{{Path: "/docs/report\n.txt", Hash: "bad"}}); !errors.Is(err, errInvalidStorePath) {
		t.Fatalf("RestoreVersions(control character path) error = %v, want %v", err, errInvalidStorePath)
	}
	if err := s.RestoreVersions(ctx, []Version{{Path: "/docs/report\x7f.txt", Hash: "bad"}}); !errors.Is(err, errInvalidStorePath) {
		t.Fatalf("RestoreVersions(delete control character path) error = %v, want %v", err, errInvalidStorePath)
	}
}

func TestStore_DeleteOldVersions(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	// Add multiple versions with different hashes
	for i := 0; i < 10; i++ {
		hash := "hash" + string(rune('a'+i))
		s.AddVersion(ctx, "/test.txt", hash, int64(i*100), "")
	}

	// Keep only 3 versions
	hashes, err := s.DeleteOldVersions(ctx, "/test.txt", 3, 24*time.Hour)
	if err != nil {
		t.Fatalf("DeleteOldVersions() error: %v", err)
	}

	if len(hashes) != 7 {
		t.Errorf("DeleteOldVersions() deleted %d hashes, want 7", len(hashes))
	}

	versions, _ := s.GetVersions(ctx, "/test.txt")
	if len(versions) != 3 {
		t.Errorf("After cleanup: %d versions, want 3", len(versions))
	}
}

func TestStore_DeleteOldVersions_ZeroLimitsKeepAllVersions(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	for i := 0; i < 55; i++ {
		hash := fmt.Sprintf("hash-%02d", i)
		if err := s.AddVersion(ctx, "/unlimited.txt", hash, int64(i+1), ""); err != nil {
			t.Fatalf("AddVersion(%s) error: %v", hash, err)
		}
	}

	hashes, err := s.DeleteOldVersions(ctx, "/unlimited.txt", 0, 0)
	if err != nil {
		t.Fatalf("DeleteOldVersions() error: %v", err)
	}
	if len(hashes) != 0 {
		t.Fatalf("DeleteOldVersions() deleted %d hashes, want 0", len(hashes))
	}

	versions, err := s.GetVersions(ctx, "/unlimited.txt")
	if err != nil {
		t.Fatalf("GetVersions() error: %v", err)
	}
	if len(versions) != 55 {
		t.Fatalf("GetVersions() returned %d versions, want 55", len(versions))
	}
}

func TestStore_GetVersions_UsesIDTieBreakerWhenCreatedAtMatches(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	for _, hash := range []string{"hash1", "hash2", "hash3"} {
		if err := s.AddVersion(ctx, "/same-second.txt", hash, 100, ""); err != nil {
			t.Fatalf("AddVersion(%s) error: %v", hash, err)
		}
	}

	versions, err := s.GetVersions(ctx, "/same-second.txt")
	if err != nil {
		t.Fatalf("GetVersions() error: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("GetVersions() returned %d versions, want 3", len(versions))
	}
	if versions[0].Hash != "hash3" || versions[1].Hash != "hash2" || versions[2].Hash != "hash1" {
		t.Fatalf("unexpected same-second order: [%s %s %s]", versions[0].Hash, versions[1].Hash, versions[2].Hash)
	}
}

func TestStore_DeleteOldVersions_UsesIDTieBreakerWhenCreatedAtMatches(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	for _, hash := range []string{"hash1", "hash2", "hash3"} {
		if err := s.AddVersion(ctx, "/same-second-retention.txt", hash, 100, ""); err != nil {
			t.Fatalf("AddVersion(%s) error: %v", hash, err)
		}
	}

	hashes, err := s.DeleteOldVersions(ctx, "/same-second-retention.txt", 1, 24*time.Hour)
	if err != nil {
		t.Fatalf("DeleteOldVersions() error: %v", err)
	}
	if len(hashes) != 2 {
		t.Fatalf("DeleteOldVersions() deleted %d hashes, want 2", len(hashes))
	}

	versions, err := s.GetVersions(ctx, "/same-second-retention.txt")
	if err != nil {
		t.Fatalf("GetVersions() after cleanup error: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 retained version after cleanup, got %d", len(versions))
	}
	if versions[0].Hash != "hash3" {
		t.Fatalf("expected newest inserted hash3 to be retained, got %s", versions[0].Hash)
	}
}

func TestStore_GetAllVersionHashes(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	s.AddVersion(ctx, "/file1.txt", "hash1", 100, "")
	s.AddVersion(ctx, "/file2.txt", "hash2", 200, "")
	s.AddVersion(ctx, "/file1.txt", "hash3", 150, "")

	hashes, err := s.GetAllVersionHashes(ctx)
	if err != nil {
		t.Fatalf("GetAllVersionHashes() error: %v", err)
	}

	if len(hashes) != 3 {
		t.Errorf("GetAllVersionHashes() returned %d hashes, want 3", len(hashes))
	}
}

func TestStore_ListVersionPaths(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	s.AddVersion(ctx, "/b.txt", "hash1", 100, "")
	s.AddVersion(ctx, "/a.txt", "hash2", 200, "")
	s.AddVersion(ctx, "/b.txt", "hash3", 150, "")

	paths, err := s.ListVersionPaths(ctx)
	if err != nil {
		t.Fatalf("ListVersionPaths() error: %v", err)
	}

	if len(paths) != 2 {
		t.Fatalf("ListVersionPaths() returned %d paths, want 2", len(paths))
	}
	if paths[0] != "/a.txt" || paths[1] != "/b.txt" {
		t.Fatalf("unexpected version paths: %#v", paths)
	}
}

func TestStore_VersioningOverride(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	// Initially no override
	_, exists := s.GetVersioningOverride(ctx, "/test.txt")
	if exists {
		t.Error("Override should not exist initially")
	}

	// Set override
	err := s.SetVersioningOverride(ctx, "/test.txt", false)
	if err != nil {
		t.Fatalf("SetVersioningOverride() error: %v", err)
	}

	enabled, exists := s.GetVersioningOverride(ctx, "/test.txt")
	if !exists {
		t.Error("Override should exist after set")
	}
	if enabled {
		t.Error("Override should be false")
	}

	// Delete override
	err = s.DeleteVersioningOverride(ctx, "/test.txt")
	if err != nil {
		t.Fatalf("DeleteVersioningOverride() error: %v", err)
	}

	_, exists = s.GetVersioningOverride(ctx, "/test.txt")
	if exists {
		t.Error("Override should not exist after delete")
	}
}

func TestStore_Trash(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	item := &TrashItem{
		ID:           "trash123",
		OriginalPath: "/deleted.txt",
		Size:         500,
		DeletedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(30 * 24 * time.Hour),
		IsDir:        false,
		HadVersions:  true,
		RestoreData:  []byte(`{"shares":[{"id":"share-1"}]}`),
	}

	// Add to trash
	err := s.AddToTrash(ctx, item)
	if err != nil {
		t.Fatalf("AddToTrash() error: %v", err)
	}

	// Get trash item
	got, err := s.GetTrashItem(ctx, "trash123")
	if err != nil {
		t.Fatalf("GetTrashItem() error: %v", err)
	}

	if got.OriginalPath != "/deleted.txt" {
		t.Errorf("OriginalPath = %s, want /deleted.txt", got.OriginalPath)
	}
	if got.Size != 500 {
		t.Errorf("Size = %d, want 500", got.Size)
	}
	if !got.HadVersions {
		t.Error("HadVersions should be true")
	}
	if string(got.RestoreData) != string(item.RestoreData) {
		t.Fatalf("RestoreData = %q, want %q", string(got.RestoreData), string(item.RestoreData))
	}

	updatedRestoreData := []byte(`{"favorites":[{"path":"/deleted.txt"}]}`)
	if err := s.UpdateTrashRestoreData(ctx, item.ID, updatedRestoreData); err != nil {
		t.Fatalf("UpdateTrashRestoreData() error: %v", err)
	}
	updatedItem, err := s.GetTrashItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetTrashItem() after update error: %v", err)
	}
	if string(updatedItem.RestoreData) != string(updatedRestoreData) {
		t.Fatalf("updated RestoreData = %q, want %q", string(updatedItem.RestoreData), string(updatedRestoreData))
	}

	// List trash
	items, err := s.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("ListTrash() returned %d items, want 1", len(items))
	}

	// Get stats
	count, size, err := s.GetTrashStats(ctx)
	if err != nil {
		t.Fatalf("GetTrashStats() error: %v", err)
	}
	if count != 1 || size != 500 {
		t.Errorf("GetTrashStats() = (%d, %d), want (1, 500)", count, size)
	}

	// Remove from trash
	err = s.RemoveFromTrash(ctx, "trash123")
	if err != nil {
		t.Fatalf("RemoveFromTrash() error: %v", err)
	}

	_, err = s.GetTrashItem(ctx, "trash123")
	if err != ErrNotFound {
		t.Errorf("GetTrashItem() after remove = %v, want ErrNotFound", err)
	}
}

func TestStore_TrashRejectsAndSkipsInvalidIDsAndPaths(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	now := time.Now()

	if err := s.AddToTrash(ctx, &TrashItem{
		ID:           "../escape",
		OriginalPath: "/deleted.txt",
		DeletedAt:    now,
		ExpiresAt:    now.Add(time.Hour),
	}); !errors.Is(err, errInvalidStoreID) {
		t.Fatalf("AddToTrash() error = %v, want %v", err, errInvalidStoreID)
	}

	if err := s.AddToTrash(ctx, &TrashItem{
		ID:           "valid-trash",
		OriginalPath: "/valid.txt",
		Size:         10,
		DeletedAt:    now,
		ExpiresAt:    now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("AddToTrash(valid) error: %v", err)
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO trash (id, original_path, size, deleted_at, expires_at, is_dir, had_versions, restore_data)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"../escape", "/evil.txt", 1, now.Unix(), now.Add(time.Hour).Unix(), false, false, []byte{}); err != nil {
		t.Fatalf("insert invalid trash ID fixture: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO trash (id, original_path, size, deleted_at, expires_at, is_dir, had_versions, restore_data)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"invalid-path", "../evil.txt", 1, now.Unix(), now.Add(time.Hour).Unix(), false, false, []byte{}); err != nil {
		t.Fatalf("insert invalid trash path fixture: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO trash (id, original_path, size, deleted_at, expires_at, is_dir, had_versions, restore_data)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"invalid-dot-path", "/docs/./evil.txt", 1, now.Unix(), now.Add(time.Hour).Unix(), false, false, []byte{}); err != nil {
		t.Fatalf("insert invalid dot-segment trash path fixture: %v", err)
	}

	items, err := s.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 || items[0].ID != "valid-trash" {
		t.Fatalf("ListTrash() = %+v, want only valid-trash", items)
	}

	if _, err := s.GetTrashItem(ctx, "../escape"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTrashItem(invalid ID) error = %v, want %v", err, ErrNotFound)
	}
	if _, err := s.GetTrashItem(ctx, "invalid-path"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTrashItem(invalid path row) error = %v, want %v", err, ErrNotFound)
	}
	if _, err := s.GetTrashItem(ctx, "invalid-dot-path"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTrashItem(invalid dot-segment path row) error = %v, want %v", err, ErrNotFound)
	}
	if err := s.UpdateTrashRestoreData(ctx, "../escape", []byte("{}")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateTrashRestoreData(invalid ID) error = %v, want %v", err, ErrNotFound)
	}
	if err := s.RemoveFromTrash(ctx, "../escape"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RemoveFromTrash(invalid ID) error = %v, want %v", err, ErrNotFound)
	}
}

func TestStore_ClearTrash(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	// Add multiple items
	for i := 0; i < 5; i++ {
		if err := s.AddToTrash(ctx, &TrashItem{
			ID:           "trash" + string(rune('0'+i)),
			OriginalPath: "/file" + string(rune('0'+i)) + ".txt",
			Size:         100,
			DeletedAt:    time.Now(),
			ExpiresAt:    time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("AddToTrash() error: %v", err)
		}
	}

	count, err := s.ClearTrash(ctx)
	if err != nil {
		t.Fatalf("ClearTrash() error: %v", err)
	}

	if count != 5 {
		t.Errorf("ClearTrash() deleted %d, want 5", count)
	}

	items, _ := s.ListTrash(ctx)
	if len(items) != 0 {
		t.Errorf("Trash not empty after clear: %d items", len(items))
	}
}

func TestStore_CleanupExpiredTrash(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	// Add expired item
	if err := s.AddToTrash(ctx, &TrashItem{
		ID:           "expired",
		OriginalPath: "/expired.txt",
		Size:         100,
		DeletedAt:    time.Now().Add(-48 * time.Hour),
		ExpiresAt:    time.Now().Add(-24 * time.Hour), // Already expired
	}); err != nil {
		t.Fatalf("AddToTrash() error: %v", err)
	}

	// Add non-expired item
	if err := s.AddToTrash(ctx, &TrashItem{
		ID:           "valid",
		OriginalPath: "/valid.txt",
		Size:         100,
		DeletedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("AddToTrash() error: %v", err)
	}

	ids, err := s.CleanupExpiredTrash(ctx)
	if err != nil {
		t.Fatalf("CleanupExpiredTrash() error: %v", err)
	}

	if len(ids) != 1 || ids[0] != "expired" {
		t.Errorf("CleanupExpiredTrash() returned %v, want [expired]", ids)
	}

	items, _ := s.ListTrash(ctx)
	if len(items) != 1 {
		t.Errorf("After cleanup: %d items, want 1", len(items))
	}
}

func TestStore_AddToTrash_DefaultsNilRestoreDataToEmptyBlob(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	item := &TrashItem{
		ID:           "trash-empty-restore-data",
		OriginalPath: "/empty.txt",
		Size:         42,
		DeletedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(time.Hour),
	}

	if err := s.AddToTrash(ctx, item); err != nil {
		t.Fatalf("AddToTrash() error: %v", err)
	}

	if item.RestoreData == nil {
		t.Fatal("RestoreData should be normalized to an empty blob")
	}

	stored, err := s.GetTrashItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetTrashItem() error: %v", err)
	}

	if len(stored.RestoreData) != 0 {
		t.Fatalf("RestoreData length = %d, want 0", len(stored.RestoreData))
	}
	if stored.RestoreData == nil {
		t.Fatal("stored RestoreData should not be nil")
	}
}

func TestStore_ListTrash_OrdersItemsDeterministicallyWithinSameSecond(t *testing.T) {
	tmpDir := t.TempDir()
	s, err := New(Config{
		DBPath:    filepath.Join(tmpDir, "test.db"),
		Dataplane: dataplane.NewClient("unused"),
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	deletedAt := time.Unix(1700000000, 0)
	olderInsert := &TrashItem{
		ID:           "trash-older-insert",
		OriginalPath: "/older.txt",
		DeletedAt:    deletedAt,
		ExpiresAt:    deletedAt.Add(time.Hour),
	}
	newerInsert := &TrashItem{
		ID:           "trash-newer-insert",
		OriginalPath: "/newer.txt",
		DeletedAt:    deletedAt,
		ExpiresAt:    deletedAt.Add(time.Hour),
	}

	if err := s.AddToTrash(ctx, olderInsert); err != nil {
		t.Fatalf("AddToTrash(olderInsert) error: %v", err)
	}
	if err := s.AddToTrash(ctx, newerInsert); err != nil {
		t.Fatalf("AddToTrash(newerInsert) error: %v", err)
	}

	items, err := s.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected two trash items, got %d", len(items))
	}
	if items[0].ID != newerInsert.ID {
		t.Fatalf("expected most recently inserted trash item %q first when deleted_at ties, got %q", newerInsert.ID, items[0].ID)
	}
}

func TestStore_FileLock(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	// Acquire lock
	err := s.AcquireLock(ctx, "/locked.txt", "user1", WriteLock, time.Hour)
	if err != nil {
		t.Fatalf("AcquireLock() error: %v", err)
	}

	// Get lock
	lock, err := s.GetLock(ctx, "/locked.txt")
	if err != nil {
		t.Fatalf("GetLock() error: %v", err)
	}

	if lock.Holder != "user1" {
		t.Errorf("Holder = %s, want user1", lock.Holder)
	}
	if lock.LockType != WriteLock {
		t.Errorf("LockType = %v, want WriteLock", lock.LockType)
	}

	// Try to acquire conflicting lock
	err = s.AcquireLock(ctx, "/locked.txt", "user2", WriteLock, time.Hour)
	if err != ErrFileLocked {
		t.Errorf("AcquireLock() error = %v, want ErrFileLocked", err)
	}

	// Release lock
	err = s.ReleaseLock(ctx, "/locked.txt", "user1")
	if err != nil {
		t.Fatalf("ReleaseLock() error: %v", err)
	}

	// Now another user can lock
	err = s.AcquireLock(ctx, "/locked.txt", "user2", WriteLock, time.Hour)
	if err != nil {
		t.Errorf("AcquireLock() after release error: %v", err)
	}
}

func TestStore_FileLock_AllowsConcurrentReadersAndBlocksWriterUntilReleased(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	if err := s.AcquireLock(ctx, "/shared.txt", "reader1", ReadLock, time.Hour); err != nil {
		t.Fatalf("AcquireLock(reader1) error: %v", err)
	}
	if err := s.AcquireLock(ctx, "/shared.txt", "reader2", ReadLock, time.Hour); err != nil {
		t.Fatalf("AcquireLock(reader2) error: %v", err)
	}
	if err := s.AcquireLock(ctx, "/shared.txt", "writer", WriteLock, time.Hour); err != ErrFileLocked {
		t.Fatalf("AcquireLock(writer with active readers) error = %v, want %v", err, ErrFileLocked)
	}
	if err := s.ReleaseLock(ctx, "/shared.txt", "reader1"); err != nil {
		t.Fatalf("ReleaseLock(reader1) error: %v", err)
	}
	if err := s.AcquireLock(ctx, "/shared.txt", "writer", WriteLock, time.Hour); err != ErrFileLocked {
		t.Fatalf("AcquireLock(writer with remaining reader) error = %v, want %v", err, ErrFileLocked)
	}
	if err := s.ReleaseLock(ctx, "/shared.txt", "reader2"); err != nil {
		t.Fatalf("ReleaseLock(reader2) error: %v", err)
	}
	if err := s.AcquireLock(ctx, "/shared.txt", "writer", WriteLock, time.Hour); err != nil {
		t.Fatalf("AcquireLock(writer after readers released) error: %v", err)
	}
	lock, err := s.GetLock(ctx, "/shared.txt")
	if err != nil {
		t.Fatalf("GetLock() error: %v", err)
	}
	if lock.Holder != "writer" || lock.LockType != WriteLock {
		t.Fatalf("GetLock() = (%q, %v), want (%q, %v)", lock.Holder, lock.LockType, "writer", WriteLock)
	}
}

func TestNew_UpgradesLegacyFileLocksSchema(t *testing.T) {
	client := setupDataplaneClient(t)
	if client == nil {
		t.Skip("dataplane not available, skipping test")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "legacy-locks.db")
	db, err := sql.Open(sqliteDriverName, versionStoreSQLiteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE file_locks (
			path TEXT PRIMARY KEY,
			holder TEXT NOT NULL,
			lock_type INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		);
	`); err != nil {
		db.Close()
		t.Fatalf("create legacy file_locks schema error: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("legacy db close error: %v", err)
	}

	s, err := New(Config{DBPath: dbPath, Dataplane: client})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.AcquireLock(ctx, "/shared.txt", "reader1", ReadLock, time.Hour); err != nil {
		t.Fatalf("AcquireLock(reader1) error: %v", err)
	}
	if err := s.AcquireLock(ctx, "/shared.txt", "reader2", ReadLock, time.Hour); err != nil {
		t.Fatalf("AcquireLock(reader2) error after schema upgrade: %v", err)
	}
}

func TestStore_CleanupExpiredLocks(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	// Acquire lock with very short duration that will expire immediately
	s.AcquireLock(ctx, "/expiring.txt", "user1", WriteLock, -time.Second)

	count, err := s.CleanupExpiredLocks(ctx)
	if err != nil {
		t.Fatalf("CleanupExpiredLocks() error: %v", err)
	}

	if count != 1 {
		t.Errorf("CleanupExpiredLocks() = %d, want 1", count)
	}
}

func TestStore_FileIndex(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)

	// Update index
	err := s.UpdateFileIndex(ctx, "/indexed.txt", 1024, now, "hash123")
	if err != nil {
		t.Fatalf("UpdateFileIndex() error: %v", err)
	}

	// Get index
	size, modTime, hash, err := s.GetFileIndex(ctx, "/indexed.txt")
	if err != nil {
		t.Fatalf("GetFileIndex() error: %v", err)
	}

	if size != 1024 {
		t.Errorf("Size = %d, want 1024", size)
	}
	if !modTime.Equal(now) {
		t.Errorf("ModTime = %v, want %v", modTime, now)
	}
	if hash != "hash123" {
		t.Errorf("Hash = %s, want hash123", hash)
	}

	// Delete index
	err = s.DeleteFileIndex(ctx, "/indexed.txt")
	if err != nil {
		t.Fatalf("DeleteFileIndex() error: %v", err)
	}

	_, _, _, err = s.GetFileIndex(ctx, "/indexed.txt")
	if err != ErrNotFound {
		t.Errorf("GetFileIndex() after delete = %v, want ErrNotFound", err)
	}
}

func TestStore_CountFiles(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	s.UpdateFileIndex(ctx, "/docs/readme.md", 100, time.Now(), "h1")
	s.UpdateFileIndex(ctx, "/docs/guide.md", 200, time.Now(), "h2")

	count, err := s.CountFiles(ctx)
	if err != nil {
		t.Fatalf("CountFiles() error: %v", err)
	}
	if count != 2 {
		t.Errorf("CountFiles() = %d, want 2", count)
	}
}

func TestStore_DeleteFileIndexPrefix(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	if err := s.UpdateFileIndex(ctx, "/docs/readme.md", 100, now, "hash1"); err != nil {
		t.Fatalf("UpdateFileIndex(readme) error: %v", err)
	}
	if err := s.UpdateFileIndex(ctx, "/docs/nested/guide.md", 200, now, "hash2"); err != nil {
		t.Fatalf("UpdateFileIndex(guide) error: %v", err)
	}
	if err := s.UpdateFileIndex(ctx, "/docs-archive/keep.md", 300, now, "hash3"); err != nil {
		t.Fatalf("UpdateFileIndex(keep) error: %v", err)
	}

	if err := s.DeleteFileIndexPrefix(ctx, "/docs"); err != nil {
		t.Fatalf("DeleteFileIndexPrefix() error: %v", err)
	}

	if _, _, _, err := s.GetFileIndex(ctx, "/docs/readme.md"); err != ErrNotFound {
		t.Fatalf("GetFileIndex(/docs/readme.md) error = %v, want ErrNotFound", err)
	}
	if _, _, _, err := s.GetFileIndex(ctx, "/docs/nested/guide.md"); err != ErrNotFound {
		t.Fatalf("GetFileIndex(/docs/nested/guide.md) error = %v, want ErrNotFound", err)
	}
	if _, _, _, err := s.GetFileIndex(ctx, "/docs-archive/keep.md"); err != nil {
		t.Fatalf("GetFileIndex(/docs-archive/keep.md) error: %v", err)
	}

	count, err := s.CountFiles(ctx)
	if err != nil {
		t.Fatalf("CountFiles() error: %v", err)
	}
	if count != 1 {
		t.Fatalf("CountFiles() = %d, want 1", count)
	}
}

func TestStore_RenamePath(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	if err := s.AddVersion(ctx, "/docs/readme.md", "hash1", 100, ""); err != nil {
		t.Fatalf("AddVersion() error: %v", err)
	}
	if err := s.AddVersion(ctx, "/docs/readme.md", "hash2", 200, ""); err != nil {
		t.Fatalf("AddVersion() error: %v", err)
	}
	if err := s.UpdateFileIndex(ctx, "/docs/readme.md", 200, now, "hash2"); err != nil {
		t.Fatalf("UpdateFileIndex() error: %v", err)
	}
	if err := s.SetVersioningOverride(ctx, "/docs/readme.md", true); err != nil {
		t.Fatalf("SetVersioningOverride() error: %v", err)
	}

	if err := s.RenamePath(ctx, "/docs", "/notes"); err != nil {
		t.Fatalf("RenamePath() error: %v", err)
	}

	versions, err := s.GetVersions(ctx, "/notes/readme.md")
	if err != nil {
		t.Fatalf("GetVersions() error: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("GetVersions() returned %d versions, want 2", len(versions))
	}

	oldVersions, err := s.GetVersions(ctx, "/docs/readme.md")
	if err != nil {
		t.Fatalf("GetVersions() error: %v", err)
	}
	if len(oldVersions) != 0 {
		t.Fatalf("GetVersions() returned %d versions for old path, want 0", len(oldVersions))
	}

	size, _, hash, err := s.GetFileIndex(ctx, "/notes/readme.md")
	if err != nil {
		t.Fatalf("GetFileIndex() error: %v", err)
	}
	if size != 200 {
		t.Errorf("Size = %d, want 200", size)
	}
	if hash != "hash2" {
		t.Errorf("Hash = %s, want hash2", hash)
	}

	_, _, _, err = s.GetFileIndex(ctx, "/docs/readme.md")
	if err != ErrNotFound {
		t.Errorf("GetFileIndex() old path error = %v, want ErrNotFound", err)
	}

	enabled, exists := s.GetVersioningOverride(ctx, "/notes/readme.md")
	if !exists || !enabled {
		t.Errorf("GetVersioningOverride() = (%v, %v), want (true, true)", enabled, exists)
	}
	_, exists = s.GetVersioningOverride(ctx, "/docs/readme.md")
	if exists {
		t.Error("GetVersioningOverride() old path should not exist")
	}
}

func TestStore_RenamePathHistory_PreservesTargetFileIndex(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	if err := s.AddVersion(ctx, "/docs/readme.md", "hash1", 100, ""); err != nil {
		t.Fatalf("AddVersion() error: %v", err)
	}
	if err := s.AddVersion(ctx, "/docs/readme.md", "hash2", 200, ""); err != nil {
		t.Fatalf("AddVersion() error: %v", err)
	}
	if err := s.SetVersioningOverride(ctx, "/docs/readme.md", true); err != nil {
		t.Fatalf("SetVersioningOverride() error: %v", err)
	}
	if err := s.AcquireLock(ctx, "/docs/readme.md", "writer", WriteLock, time.Hour); err != nil {
		t.Fatalf("AcquireLock() error: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	if err := s.UpdateFileIndex(ctx, "/notes/readme.md", 300, now, "restored-hash"); err != nil {
		t.Fatalf("UpdateFileIndex(target) error: %v", err)
	}

	if err := s.RenamePathHistory(ctx, "/docs", "/notes"); err != nil {
		t.Fatalf("RenamePathHistory() error: %v", err)
	}

	versions, err := s.GetVersions(ctx, "/notes/readme.md")
	if err != nil {
		t.Fatalf("GetVersions(target) error: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("GetVersions(target) returned %d versions, want 2", len(versions))
	}

	oldVersions, err := s.GetVersions(ctx, "/docs/readme.md")
	if err != nil {
		t.Fatalf("GetVersions(source) error: %v", err)
	}
	if len(oldVersions) != 0 {
		t.Fatalf("GetVersions(source) returned %d versions, want 0", len(oldVersions))
	}

	size, modTime, hash, err := s.GetFileIndex(ctx, "/notes/readme.md")
	if err != nil {
		t.Fatalf("GetFileIndex(target) error: %v", err)
	}
	if size != 300 {
		t.Fatalf("GetFileIndex(target) size = %d, want 300", size)
	}
	if !modTime.Equal(now) {
		t.Fatalf("GetFileIndex(target) modTime = %v, want %v", modTime, now)
	}
	if hash != "restored-hash" {
		t.Fatalf("GetFileIndex(target) hash = %s, want restored-hash", hash)
	}

	enabled, exists := s.GetVersioningOverride(ctx, "/notes/readme.md")
	if !exists || !enabled {
		t.Fatalf("GetVersioningOverride(target) = (%v, %v), want (true, true)", enabled, exists)
	}
	_, exists = s.GetVersioningOverride(ctx, "/docs/readme.md")
	if exists {
		t.Fatal("GetVersioningOverride(source) should not exist")
	}

	lock, err := s.GetLock(ctx, "/notes/readme.md")
	if err != nil {
		t.Fatalf("GetLock(target) error: %v", err)
	}
	if lock.Holder != "writer" {
		t.Fatalf("GetLock(target) holder = %q, want %q", lock.Holder, "writer")
	}
	if _, err := s.GetLock(ctx, "/docs/readme.md"); err != ErrNotFound {
		t.Fatalf("GetLock(source) error = %v, want %v", err, ErrNotFound)
	}
}

func TestStore_RenamePath_TargetAlreadyExists(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	if err := s.AddVersion(ctx, "/docs/readme.md", "hash1", 100, ""); err != nil {
		t.Fatalf("AddVersion(source) error: %v", err)
	}
	if err := s.UpdateFileIndex(ctx, "/docs/readme.md", 100, time.Now(), "hash1"); err != nil {
		t.Fatalf("UpdateFileIndex(source) error: %v", err)
	}

	if err := s.AddVersion(ctx, "/notes/existing.md", "hash2", 200, ""); err != nil {
		t.Fatalf("AddVersion(target) error: %v", err)
	}
	if err := s.UpdateFileIndex(ctx, "/notes/existing.md", 200, time.Now(), "hash2"); err != nil {
		t.Fatalf("UpdateFileIndex(target) error: %v", err)
	}

	err := s.RenamePath(ctx, "/docs", "/notes")
	if err != ErrAlreadyExists {
		t.Fatalf("RenamePath() error = %v, want ErrAlreadyExists", err)
	}

	versions, err := s.GetVersions(ctx, "/docs/readme.md")
	if err != nil {
		t.Fatalf("GetVersions(source) error: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected source metadata to remain unchanged, got %d versions", len(versions))
	}

	_, _, _, err = s.GetFileIndex(ctx, "/notes/existing.md")
	if err != nil {
		t.Fatalf("GetFileIndex(target) error after failed rename: %v", err)
	}
}

func TestStore_RenamePath_DoesNotTouchCaseDistinctSiblings(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	if err := s.AddVersion(ctx, "/Docs/readme.md", "hash1", 100, ""); err != nil {
		t.Fatalf("AddVersion(source) error: %v", err)
	}
	if err := s.UpdateFileIndex(ctx, "/Docs/readme.md", 100, now, "hash1"); err != nil {
		t.Fatalf("UpdateFileIndex(source) error: %v", err)
	}
	if err := s.AddVersion(ctx, "/docs/keep.md", "hash2", 200, ""); err != nil {
		t.Fatalf("AddVersion(sibling) error: %v", err)
	}
	if err := s.UpdateFileIndex(ctx, "/docs/keep.md", 200, now, "hash2"); err != nil {
		t.Fatalf("UpdateFileIndex(sibling) error: %v", err)
	}

	if err := s.RenamePath(ctx, "/Docs", "/Notes"); err != nil {
		t.Fatalf("RenamePath() error: %v", err)
	}

	if _, _, _, err := s.GetFileIndex(ctx, "/Notes/readme.md"); err != nil {
		t.Fatalf("GetFileIndex(renamed) error: %v", err)
	}
	if _, _, _, err := s.GetFileIndex(ctx, "/docs/keep.md"); err != nil {
		t.Fatalf("GetFileIndex(case-distinct sibling) error: %v", err)
	}
	if _, _, _, err := s.GetFileIndex(ctx, "/Notes/keep.md"); err != ErrNotFound {
		t.Fatalf("GetFileIndex(unexpectedly renamed sibling) error = %v, want ErrNotFound", err)
	}

	versions, err := s.GetVersions(ctx, "/docs/keep.md")
	if err != nil {
		t.Fatalf("GetVersions(case-distinct sibling) error: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("GetVersions(case-distinct sibling) returned %d versions, want 1", len(versions))
	}
}

func TestStore_RenamePath_TargetCaseDistinctPathDoesNotConflict(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	if err := s.AddVersion(ctx, "/docs/readme.md", "hash1", 100, ""); err != nil {
		t.Fatalf("AddVersion(source) error: %v", err)
	}
	if err := s.UpdateFileIndex(ctx, "/docs/readme.md", 100, now, "hash1"); err != nil {
		t.Fatalf("UpdateFileIndex(source) error: %v", err)
	}
	if err := s.AddVersion(ctx, "/notes/existing.md", "hash2", 200, ""); err != nil {
		t.Fatalf("AddVersion(case-distinct target) error: %v", err)
	}
	if err := s.UpdateFileIndex(ctx, "/notes/existing.md", 200, now, "hash2"); err != nil {
		t.Fatalf("UpdateFileIndex(case-distinct target) error: %v", err)
	}

	if err := s.RenamePath(ctx, "/docs", "/Notes"); err != nil {
		t.Fatalf("RenamePath() error: %v", err)
	}

	if _, _, _, err := s.GetFileIndex(ctx, "/Notes/readme.md"); err != nil {
		t.Fatalf("GetFileIndex(renamed) error: %v", err)
	}
	if _, _, _, err := s.GetFileIndex(ctx, "/notes/existing.md"); err != nil {
		t.Fatalf("GetFileIndex(case-distinct target) error: %v", err)
	}
}

func TestStore_RenamePath_DoesNotTreatPercentAsWildcard(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	if err := s.AddVersion(ctx, "/docs%2024/readme.md", "hash1", 100, ""); err != nil {
		t.Fatalf("AddVersion(source) error: %v", err)
	}
	if err := s.UpdateFileIndex(ctx, "/docs%2024/readme.md", 100, now, "hash1"); err != nil {
		t.Fatalf("UpdateFileIndex(source) error: %v", err)
	}
	if err := s.AddVersion(ctx, "/docsX2024/keep.md", "hash2", 200, ""); err != nil {
		t.Fatalf("AddVersion(sibling) error: %v", err)
	}
	if err := s.UpdateFileIndex(ctx, "/docsX2024/keep.md", 200, now, "hash2"); err != nil {
		t.Fatalf("UpdateFileIndex(sibling) error: %v", err)
	}

	if err := s.RenamePath(ctx, "/docs%2024", "/notes%2024"); err != nil {
		t.Fatalf("RenamePath() error: %v", err)
	}

	if _, _, _, err := s.GetFileIndex(ctx, "/notes%2024/readme.md"); err != nil {
		t.Fatalf("GetFileIndex(renamed) error: %v", err)
	}
	if _, _, _, err := s.GetFileIndex(ctx, "/docsX2024/keep.md"); err != nil {
		t.Fatalf("GetFileIndex(percent sibling) error: %v", err)
	}
	if _, _, _, err := s.GetFileIndex(ctx, "/notes%2024/keep.md"); err != ErrNotFound {
		t.Fatalf("GetFileIndex(unexpectedly renamed percent sibling) error = %v, want ErrNotFound", err)
	}
}

func TestStore_OperationsRejectTraversalLikePaths(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	testCases := []string{
		"../escape.txt",
		`..\\escape.txt`,
		"/docs/./report.txt",
		"./docs/report.txt",
		".",
	}

	for _, rawPath := range testCases {
		t.Run(rawPath, func(t *testing.T) {
			if err := s.AddVersion(ctx, rawPath, "hash1", 1, ""); !errors.Is(err, errInvalidStorePath) {
				t.Fatalf("AddVersion() error = %v, want %v", err, errInvalidStorePath)
			}
			if _, err := s.GetVersions(ctx, rawPath); !errors.Is(err, errInvalidStorePath) {
				t.Fatalf("GetVersions() error = %v, want %v", err, errInvalidStorePath)
			}
			if err := s.SetVersioningOverride(ctx, rawPath, true); !errors.Is(err, errInvalidStorePath) {
				t.Fatalf("SetVersioningOverride() error = %v, want %v", err, errInvalidStorePath)
			}
			if err := s.UpdateFileIndex(ctx, rawPath, 1, time.Now(), "hash1"); !errors.Is(err, errInvalidStorePath) {
				t.Fatalf("UpdateFileIndex() error = %v, want %v", err, errInvalidStorePath)
			}
			if err := s.AcquireLock(ctx, rawPath, "tester", WriteLock, time.Minute); !errors.Is(err, errInvalidStorePath) {
				t.Fatalf("AcquireLock() error = %v, want %v", err, errInvalidStorePath)
			}
			if err := s.AddToTrash(ctx, &TrashItem{ID: "trash-" + strings.ReplaceAll(rawPath, " ", "_"), OriginalPath: rawPath, DeletedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}); !errors.Is(err, errInvalidStorePath) {
				t.Fatalf("AddToTrash() error = %v, want %v", err, errInvalidStorePath)
			}
			if err := s.RenamePath(ctx, rawPath, "/dest"); !errors.Is(err, errInvalidStorePath) {
				t.Fatalf("RenamePath(source) error = %v, want %v", err, errInvalidStorePath)
			}
			if err := s.RenamePath(ctx, "/source", rawPath); !errors.Is(err, errInvalidStorePath) {
				t.Fatalf("RenamePath(destination) error = %v, want %v", err, errInvalidStorePath)
			}
		})
	}
}

func TestStore_NormalizesDirectPathInputs(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	rawPath := `docs\\report.txt`

	if err := s.AddVersion(ctx, rawPath, "hash1", 100, ""); err != nil {
		t.Fatalf("AddVersion() error: %v", err)
	}
	versions, err := s.GetVersions(ctx, "/docs/report.txt")
	if err != nil {
		t.Fatalf("GetVersions(normalized) error: %v", err)
	}
	if len(versions) != 1 || versions[0].Path != "/docs/report.txt" {
		t.Fatalf("expected normalized version path, got %+v", versions)
	}

	if err := s.UpdateFileIndex(ctx, rawPath, 100, now, "hash1"); err != nil {
		t.Fatalf("UpdateFileIndex() error: %v", err)
	}
	if _, _, hash, err := s.GetFileIndex(ctx, "/docs/report.txt"); err != nil || hash != "hash1" {
		t.Fatalf("GetFileIndex(normalized) = (%q, %v), want (hash1, nil)", hash, err)
	}

	if err := s.SetVersioningOverride(ctx, rawPath, true); err != nil {
		t.Fatalf("SetVersioningOverride() error: %v", err)
	}
	if enabled, exists := s.GetVersioningOverride(ctx, "/docs/report.txt"); !exists || !enabled {
		t.Fatalf("GetVersioningOverride(normalized) = (%v, %v), want (true, true)", enabled, exists)
	}

	if err := s.AcquireLock(ctx, rawPath, "tester", WriteLock, time.Minute); err != nil {
		t.Fatalf("AcquireLock() error: %v", err)
	}
	lock, err := s.GetLock(ctx, "/docs/report.txt")
	if err != nil {
		t.Fatalf("GetLock(normalized) error: %v", err)
	}
	if lock.Path != "/docs/report.txt" {
		t.Fatalf("expected normalized lock path, got %q", lock.Path)
	}

	if err := s.AddToTrash(ctx, &TrashItem{ID: "trash-normalized", OriginalPath: rawPath, DeletedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("AddToTrash() error: %v", err)
	}
	item, err := s.GetTrashItem(ctx, "trash-normalized")
	if err != nil {
		t.Fatalf("GetTrashItem() error: %v", err)
	}
	if item.OriginalPath != "/docs/report.txt" {
		t.Fatalf("expected normalized trash original path, got %q", item.OriginalPath)
	}

	if err := s.RenamePath(ctx, `docs`, `archive\\docs`); err != nil {
		t.Fatalf("RenamePath() error: %v", err)
	}
	renamedVersions, err := s.GetVersions(ctx, "/archive/docs/report.txt")
	if err != nil {
		t.Fatalf("GetVersions(renamed normalized path) error: %v", err)
	}
	if len(renamedVersions) != 1 {
		t.Fatalf("expected one renamed normalized version entry, got %d", len(renamedVersions))
	}
	if _, _, _, err := s.GetFileIndex(ctx, "/archive/docs/report.txt"); err != nil {
		t.Fatalf("GetFileIndex(renamed normalized path) error: %v", err)
	}
	if _, _, _, err := s.GetFileIndex(ctx, "/docs/report.txt"); err != ErrNotFound {
		t.Fatalf("expected old normalized path index to be absent, got %v", err)
	}
}

func TestStore_PreservesWhitespaceInPaths(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	plainPath := "/docs/report.txt"
	spacedPath := "/docs/report.txt "

	if err := s.AddVersion(ctx, plainPath, "hash-plain", 100, ""); err != nil {
		t.Fatalf("AddVersion(plain) error: %v", err)
	}
	if err := s.AddVersion(ctx, spacedPath, "hash-spaced", 200, ""); err != nil {
		t.Fatalf("AddVersion(spaced) error: %v", err)
	}

	plainVersions, err := s.GetVersions(ctx, plainPath)
	if err != nil {
		t.Fatalf("GetVersions(plain) error: %v", err)
	}
	if len(plainVersions) != 1 || plainVersions[0].Path != plainPath || plainVersions[0].Hash != "hash-plain" {
		t.Fatalf("plain versions = %+v, want only %q", plainVersions, plainPath)
	}
	spacedVersions, err := s.GetVersions(ctx, spacedPath)
	if err != nil {
		t.Fatalf("GetVersions(spaced) error: %v", err)
	}
	if len(spacedVersions) != 1 || spacedVersions[0].Path != spacedPath || spacedVersions[0].Hash != "hash-spaced" {
		t.Fatalf("spaced versions = %+v, want only %q", spacedVersions, spacedPath)
	}

	if err := s.UpdateFileIndex(ctx, plainPath, 100, now, "index-plain"); err != nil {
		t.Fatalf("UpdateFileIndex(plain) error: %v", err)
	}
	if err := s.UpdateFileIndex(ctx, spacedPath, 200, now, "index-spaced"); err != nil {
		t.Fatalf("UpdateFileIndex(spaced) error: %v", err)
	}
	if plainSize, _, plainHash, err := s.GetFileIndex(ctx, plainPath); err != nil || plainSize != 100 || plainHash != "index-plain" {
		t.Fatalf("GetFileIndex(plain) = (%d, %q, %v), want (100, index-plain, nil)", plainSize, plainHash, err)
	}
	if spacedSize, _, spacedHash, err := s.GetFileIndex(ctx, spacedPath); err != nil || spacedSize != 200 || spacedHash != "index-spaced" {
		t.Fatalf("GetFileIndex(spaced) = (%d, %q, %v), want (200, index-spaced, nil)", spacedSize, spacedHash, err)
	}

	if err := s.SetVersioningOverride(ctx, plainPath, false); err != nil {
		t.Fatalf("SetVersioningOverride(plain) error: %v", err)
	}
	if err := s.SetVersioningOverride(ctx, spacedPath, true); err != nil {
		t.Fatalf("SetVersioningOverride(spaced) error: %v", err)
	}
	if enabled, exists := s.GetVersioningOverride(ctx, plainPath); !exists || enabled {
		t.Fatalf("GetVersioningOverride(plain) = (%v, %v), want (false, true)", enabled, exists)
	}
	if enabled, exists := s.GetVersioningOverride(ctx, spacedPath); !exists || !enabled {
		t.Fatalf("GetVersioningOverride(spaced) = (%v, %v), want (true, true)", enabled, exists)
	}

	if err := s.AcquireLock(ctx, plainPath, "plain-holder", WriteLock, time.Minute); err != nil {
		t.Fatalf("AcquireLock(plain) error: %v", err)
	}
	if err := s.AcquireLock(ctx, spacedPath, "spaced-holder", WriteLock, time.Minute); err != nil {
		t.Fatalf("AcquireLock(spaced) error: %v", err)
	}
	if lock, err := s.GetLock(ctx, plainPath); err != nil || lock.Holder != "plain-holder" {
		t.Fatalf("GetLock(plain) = (%+v, %v), want holder plain-holder", lock, err)
	}
	if lock, err := s.GetLock(ctx, spacedPath); err != nil || lock.Holder != "spaced-holder" {
		t.Fatalf("GetLock(spaced) = (%+v, %v), want holder spaced-holder", lock, err)
	}

	if err := s.AddToTrash(ctx, &TrashItem{ID: "trash-plain", OriginalPath: plainPath, DeletedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("AddToTrash(plain) error: %v", err)
	}
	if err := s.AddToTrash(ctx, &TrashItem{ID: "trash-spaced", OriginalPath: spacedPath, DeletedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("AddToTrash(spaced) error: %v", err)
	}
	plainTrash, err := s.GetTrashItem(ctx, "trash-plain")
	if err != nil {
		t.Fatalf("GetTrashItem(plain) error: %v", err)
	}
	if plainTrash.OriginalPath != plainPath {
		t.Fatalf("plain trash path = %q, want %q", plainTrash.OriginalPath, plainPath)
	}
	spacedTrash, err := s.GetTrashItem(ctx, "trash-spaced")
	if err != nil {
		t.Fatalf("GetTrashItem(spaced) error: %v", err)
	}
	if spacedTrash.OriginalPath != spacedPath {
		t.Fatalf("spaced trash path = %q, want %q", spacedTrash.OriginalPath, spacedPath)
	}
}

func TestStore_SearchFiles(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	// Add some files to index
	s.UpdateFileIndex(ctx, "/docs/readme.md", 100, time.Now(), "h1")
	s.UpdateFileIndex(ctx, "/docs/guide.md", 200, time.Now(), "h2")
	s.UpdateFileIndex(ctx, "/src/main.go", 300, time.Now(), "h3")

	// Search
	results, err := s.SearchFiles(ctx, "docs", 10)
	if err != nil {
		t.Fatalf("SearchFiles() error: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("SearchFiles(docs) returned %d results, want 2", len(results))
	}

	results, err = s.SearchFiles(ctx, "readme", 10)
	if err != nil {
		t.Fatalf("SearchFiles() error: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("SearchFiles(readme) returned %d results, want 1", len(results))
	}
}

func TestStore_SearchFiles_TreatsLikeWildcardsAsLiterals(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	now := time.Now()

	for _, indexedPath := range []string{
		"/docs/literal%name.txt",
		"/docs/literal_name.txt",
		"/docs/literalXname.txt",
		"/docs/other.txt",
	} {
		if err := s.UpdateFileIndex(ctx, indexedPath, 100, now, "hash-"+indexedPath); err != nil {
			t.Fatalf("UpdateFileIndex(%s) error: %v", indexedPath, err)
		}
	}

	percentResults, err := s.SearchFiles(ctx, "%", 10)
	if err != nil {
		t.Fatalf("SearchFiles(%%) error: %v", err)
	}
	if !reflect.DeepEqual(percentResults, []string{"/docs/literal%name.txt"}) {
		t.Fatalf("SearchFiles(%%) = %v, want literal percent path only", percentResults)
	}

	underscoreResults, err := s.SearchFiles(ctx, "_", 10)
	if err != nil {
		t.Fatalf("SearchFiles(_) error: %v", err)
	}
	if !reflect.DeepEqual(underscoreResults, []string{"/docs/literal_name.txt"}) {
		t.Fatalf("SearchFiles(_) = %v, want literal underscore path only", underscoreResults)
	}
}

func TestStore_Objects(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	data := []byte("version content")

	// Put object (hash is computed by dataplane)
	hash, err := s.PutObject(ctx, data)
	if err != nil {
		t.Fatalf("PutObject() error: %v", err)
	}

	// Hash should be non-empty
	if hash == "" {
		t.Error("PutObject() returned empty hash")
	}

	// Check exists
	exists, err := s.HasObject(ctx, hash)
	if err != nil {
		t.Fatalf("HasObject() error: %v", err)
	}
	if !exists {
		t.Error("HasObject() returned false for existing object")
	}

	// Get object
	got, err := s.GetObject(ctx, hash)
	if err != nil {
		t.Fatalf("GetObject() error: %v", err)
	}

	if string(got) != string(data) {
		t.Errorf("GetObject() = %q, want %q", got, data)
	}

	// Delete object
	err = s.DeleteObject(ctx, hash)
	if err != nil {
		t.Fatalf("DeleteObject() error: %v", err)
	}

	exists, err = s.HasObject(ctx, hash)
	if err != nil {
		t.Fatalf("HasObject() after delete error: %v", err)
	}
	if exists {
		t.Error("HasObject() returned true after delete")
	}
}

func TestStore_ChunkReferenceLifecycle(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	if err := s.AddChunkRef(ctx, "chunk-a", 128); err != nil {
		t.Fatalf("AddChunkRef(first) error: %v", err)
	}
	if err := s.AddChunkRef(ctx, "chunk-a", 128); err != nil {
		t.Fatalf("AddChunkRef(second) error: %v", err)
	}

	shouldGC, err := s.RemoveChunkRef(ctx, "missing-chunk")
	if err != nil {
		t.Fatalf("RemoveChunkRef(missing) error: %v", err)
	}
	if shouldGC {
		t.Fatal("missing chunk must not be reported as ready for GC")
	}

	shouldGC, err = s.RemoveChunkRef(ctx, "chunk-a")
	if err != nil {
		t.Fatalf("RemoveChunkRef(first) error: %v", err)
	}
	if shouldGC {
		t.Fatal("chunk-a should still have one reference after first removal")
	}

	shouldGC, err = s.RemoveChunkRef(ctx, "chunk-a")
	if err != nil {
		t.Fatalf("RemoveChunkRef(second) error: %v", err)
	}
	if !shouldGC {
		t.Fatal("chunk-a should be ready for GC after reference count reaches zero")
	}

	orphans, err := s.GetOrphanedChunks(ctx, 10)
	if err != nil {
		t.Fatalf("GetOrphanedChunks() error: %v", err)
	}
	if len(orphans) != 1 || orphans[0] != "chunk-a" {
		t.Fatalf("orphaned chunks = %v, want [chunk-a]", orphans)
	}
	if err := s.DeleteChunkRef(ctx, "chunk-a"); err != nil {
		t.Fatalf("DeleteChunkRef() error: %v", err)
	}
	orphans, err = s.GetOrphanedChunks(ctx, 10)
	if err != nil {
		t.Fatalf("GetOrphanedChunks(after delete) error: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphaned chunks after delete = %v, want empty", orphans)
	}
}

func TestStore_LinkVersionChunks(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	if err := s.AddVersion(ctx, "/docs/report.txt", "manifest-hash", 256, ""); err != nil {
		t.Fatalf("AddVersion() error: %v", err)
	}
	version, err := s.GetVersion(ctx, "/docs/report.txt", "manifest-hash")
	if err != nil {
		t.Fatalf("GetVersion() error: %v", err)
	}
	for _, hash := range []string{"chunk-1", "chunk-2"} {
		if err := s.AddChunkRef(ctx, hash, 64); err != nil {
			t.Fatalf("AddChunkRef(%s) error: %v", hash, err)
		}
	}

	if err := s.LinkVersionChunks(ctx, version.ID, []string{"chunk-1", "chunk-2", "chunk-1"}); err != nil {
		t.Fatalf("LinkVersionChunks() error: %v", err)
	}

	var linked int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM version_chunks WHERE version_id = ?`, version.ID).Scan(&linked); err != nil {
		t.Fatalf("count version_chunks error: %v", err)
	}
	if linked != 2 {
		t.Fatalf("linked chunk rows = %d, want 2 unique chunks", linked)
	}
}

func TestStore_RunGC_ReturnsDeleteErrorsAndContinues(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	if _, err := s.db.ExecContext(ctx, `INSERT INTO chunk_refs (hash, ref_count, size, created_at) VALUES (?, 0, ?, ?)`, "orphan-fail", 10, time.Now().Unix()); err != nil {
		t.Fatalf("insert orphan-fail error: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO chunk_refs (hash, ref_count, size, created_at) VALUES (?, 0, ?, ?)`, "orphan-ok", 20, time.Now().Unix()); err != nil {
		t.Fatalf("insert orphan-ok error: %v", err)
	}

	called := make(map[string]int)
	s.deleteObjectFn = func(ctx context.Context, hash string) error {
		called[hash]++
		if hash == "orphan-fail" {
			return errors.New("delete object failed")
		}
		return nil
	}

	deleted, freed, err := s.RunGC(ctx, 10)
	if err == nil {
		t.Fatal("expected RunGC() to return aggregated delete error")
	}
	if !strings.Contains(err.Error(), "orphan-fail") {
		t.Fatalf("expected error to mention failed hash, got %v", err)
	}
	if called["orphan-fail"] != 1 || called["orphan-ok"] != 1 {
		t.Fatalf("expected both orphan deletes to be attempted once, got %+v", called)
	}
	if deleted != 1 {
		t.Fatalf("expected one successful deletion, got %d", deleted)
	}
	if freed != 20 {
		t.Fatalf("expected freed bytes 20, got %d", freed)
	}

	remaining, getErr := s.GetOrphanedChunks(ctx, 10)
	if getErr != nil {
		t.Fatalf("GetOrphanedChunks() error: %v", getErr)
	}
	if len(remaining) != 1 || remaining[0] != "orphan-fail" {
		t.Fatalf("expected failed orphan to remain referenced, got %v", remaining)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunk_refs WHERE hash = ?`, "orphan-ok").Scan(&count); err != nil {
		t.Fatalf("chunk ref lookup error: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected successful orphan ref to be removed, count=%d", count)
	}
}

func TestStore_RunGC_ReturnsChunkRefSizeLookupErrorsAndSkipsDelete(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	if _, err := s.db.ExecContext(ctx, `INSERT INTO chunk_refs (hash, ref_count, size, created_at) VALUES (?, 0, ?, ?)`, "orphan-size-fail", 10, time.Now().Unix()); err != nil {
		t.Fatalf("insert orphan-size-fail error: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO chunk_refs (hash, ref_count, size, created_at) VALUES (?, 0, ?, ?)`, "orphan-ok", 20, time.Now().Unix()); err != nil {
		t.Fatalf("insert orphan-ok error: %v", err)
	}

	originalGetChunkRefSize := s.getChunkRefSizeFn
	s.getChunkRefSizeFn = func(ctx context.Context, hash string) (int64, error) {
		if hash == "orphan-size-fail" {
			return 0, errors.New("size lookup failed")
		}
		return originalGetChunkRefSize(ctx, hash)
	}

	called := make(map[string]int)
	s.deleteObjectFn = func(ctx context.Context, hash string) error {
		called[hash]++
		return nil
	}

	deleted, freed, err := s.RunGC(ctx, 10)
	if err == nil {
		t.Fatal("expected RunGC() to return chunk ref size lookup error")
	}
	if !strings.Contains(err.Error(), "orphan-size-fail") {
		t.Fatalf("expected error to mention failed hash, got %v", err)
	}
	if called["orphan-size-fail"] != 0 {
		t.Fatalf("expected failing orphan to skip object delete, got %+v", called)
	}
	if called["orphan-ok"] != 1 {
		t.Fatalf("expected healthy orphan to be deleted once, got %+v", called)
	}
	if deleted != 1 {
		t.Fatalf("expected one successful deletion, got %d", deleted)
	}
	if freed != 20 {
		t.Fatalf("expected freed bytes 20, got %d", freed)
	}

	remaining, getErr := s.GetOrphanedChunks(ctx, 10)
	if getErr != nil {
		t.Fatalf("GetOrphanedChunks() error: %v", getErr)
	}
	if len(remaining) != 1 || remaining[0] != "orphan-size-fail" {
		t.Fatalf("expected size lookup failure candidate to remain for retry, got %v", remaining)
	}
}

func TestStore_RunGC_ReturnsChunkRefDeleteErrors(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	if _, err := s.db.ExecContext(ctx, `INSERT INTO chunk_refs (hash, ref_count, size, created_at) VALUES (?, 0, ?, ?)`, "orphan-ref-fail", 30, time.Now().Unix()); err != nil {
		t.Fatalf("insert orphan-ref-fail error: %v", err)
	}

	s.deleteObjectFn = func(ctx context.Context, hash string) error { return nil }
	s.deleteChunkRefFn = func(ctx context.Context, chunkHash string) error {
		return errors.New("delete chunk ref failed")
	}

	deleted, freed, err := s.RunGC(ctx, 10)
	if err == nil {
		t.Fatal("expected RunGC() to return chunk ref delete error")
	}
	if !strings.Contains(err.Error(), "orphan-ref-fail") {
		t.Fatalf("expected error to mention failed chunk hash, got %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected zero successful deletions, got %d", deleted)
	}
	if freed != 0 {
		t.Fatalf("expected freed bytes 0, got %d", freed)
	}

	remaining, getErr := s.GetOrphanedChunks(ctx, 10)
	if getErr != nil {
		t.Fatalf("GetOrphanedChunks() error: %v", getErr)
	}
	if len(remaining) != 1 || remaining[0] != "orphan-ref-fail" {
		t.Fatalf("expected orphan to remain when ref deletion fails, got %v", remaining)
	}
}

func TestStore_RunGC_CleansChunkRefWhenObjectAlreadyMissing(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	if _, err := s.db.ExecContext(ctx, `INSERT INTO chunk_refs (hash, ref_count, size, created_at) VALUES (?, 0, ?, ?)`, "orphan-missing", 30, time.Now().Unix()); err != nil {
		t.Fatalf("insert orphan-missing error: %v", err)
	}

	s.deleteObjectFn = func(ctx context.Context, hash string) error {
		if hash != "orphan-missing" {
			t.Fatalf("unexpected hash %q", hash)
		}
		return ErrNotFound
	}

	deleted, freed, err := s.RunGC(ctx, 10)
	if err != nil {
		t.Fatalf("RunGC() error = %v, want nil", err)
	}
	if deleted != 0 {
		t.Fatalf("expected zero object deletions when CAS object is already missing, got %d", deleted)
	}
	if freed != 0 {
		t.Fatalf("expected zero freed bytes when CAS object is already missing, got %d", freed)
	}

	remaining, getErr := s.GetOrphanedChunks(ctx, 10)
	if getErr != nil {
		t.Fatalf("GetOrphanedChunks() error: %v", getErr)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected orphan ref to be cleaned up, got %v", remaining)
	}

	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunk_refs WHERE hash = ?`, "orphan-missing").Scan(&count); err != nil {
		t.Fatalf("chunk ref lookup error: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected orphan chunk ref to be removed, count=%d", count)
	}
}

func TestObjectStore_ReturnsErrUnavailableWhenDisconnected(t *testing.T) {
	store := NewObjectStore(&dataplane.Client{})
	ctx := context.Background()

	if _, err := store.Get(ctx, "abc"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Get() error = %v, want ErrUnavailable", err)
	}
	if _, err := store.Put(ctx, []byte("data")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Put() error = %v, want ErrUnavailable", err)
	}
	if _, err := store.Has(ctx, "abc"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Has() error = %v, want ErrUnavailable", err)
	}
	if err := store.Delete(ctx, "abc"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Delete() error = %v, want ErrUnavailable", err)
	}
}
