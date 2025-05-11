// Package caslayout provides directory layout for content-addressable storage
package caslayout

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanupCASTempPath_IgnoresRemoveError(t *testing.T) {
	tmpDir := t.TempDir()
	busyDir := filepath.Join(tmpDir, "busy")
	if err := os.Mkdir(busyDir, 0700); err != nil {
		t.Fatalf("failed to create busy temp dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(busyDir, "child"), []byte("data"), 0600); err != nil {
		t.Fatalf("failed to create busy temp child: %v", err)
	}

	root, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatalf("failed to open root: %v", err)
	}
	defer root.Close()

	cleanupCASTempPath(root, "busy")
	if _, err := os.Stat(busyDir); err != nil {
		t.Fatalf("expected busy temp path to remain after ignored cleanup error: %v", err)
	}
}

func TestShardedLayout_HashToPath(t *testing.T) {
	tests := []struct {
		name      string
		levels    int
		shardSize int
		hash      string
		want      string
	}{
		{
			name:      "standard 2 levels 2 chars",
			levels:    2,
			shardSize: 2,
			hash:      "abcdef1234567890",
			want:      filepath.Join("ab", "cd", "abcdef1234567890"),
		},
		{
			name:      "3 levels 2 chars",
			levels:    3,
			shardSize: 2,
			hash:      "abcdef1234567890",
			want:      filepath.Join("ab", "cd", "ef", "abcdef1234567890"),
		},
		{
			name:      "2 levels 3 chars",
			levels:    2,
			shardSize: 3,
			hash:      "abcdef1234567890",
			want:      filepath.Join("abc", "def", "abcdef1234567890"),
		},
		{
			name:      "short hash fallback",
			levels:    2,
			shardSize: 2,
			hash:      "abc",
			want:      "abc", // too short, return as-is
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewShardedLayout(tt.levels, tt.shardSize)
			got := l.HashToPath(tt.hash)
			if got != tt.want {
				t.Errorf("HashToPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShardedLayout_PathToHash(t *testing.T) {
	l := NewShardedLayout(2, 2)

	tests := []struct {
		path string
		want string
	}{
		{filepath.Join("ab", "cd", "abcdef1234"), "abcdef1234"},
		{"simple", "simple"},
		{filepath.Join("deep", "nested", "path", "hash"), "hash"},
	}

	for _, tt := range tests {
		got, err := l.PathToHash(tt.path)
		if err != nil {
			t.Errorf("PathToHash(%q) error: %v", tt.path, err)
			continue
		}
		if got != tt.want {
			t.Errorf("PathToHash(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestFlatLayout(t *testing.T) {
	l := FlatLayout{}

	t.Run("HashToPath", func(t *testing.T) {
		hash := "abcdef1234567890"
		got := l.HashToPath(hash)
		if got != hash {
			t.Errorf("HashToPath() = %q, want %q", got, hash)
		}
	})

	t.Run("PathToHash", func(t *testing.T) {
		path := "abcdef1234567890"
		got, err := l.PathToHash(path)
		if err != nil {
			t.Errorf("PathToHash() error: %v", err)
		}
		if got != path {
			t.Errorf("PathToHash() = %q, want %q", got, path)
		}
	})

	t.Run("FullPath", func(t *testing.T) {
		root := "/data"
		hash := "abc123"
		got := l.FullPath(root, hash)
		want := filepath.Join(root, hash)
		if got != want {
			t.Errorf("FullPath() = %q, want %q", got, want)
		}
	})
}

func TestEnsureCASDir_SyncsCreatedDirectoriesDeepestParentFirst(t *testing.T) {
	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "ab", "cd", "ef")

	originalSyncDir := syncDir
	var synced []string
	syncDir = func(dir string) error {
		synced = append(synced, dir)
		return nil
	}
	defer func() {
		syncDir = originalSyncDir
	}()

	if err := ensureCASDir(targetDir, 0755); err != nil {
		t.Fatalf("ensureCASDir() error: %v", err)
	}

	want := []string{
		filepath.Join(tmpDir, "ab", "cd"),
		filepath.Join(tmpDir, "ab"),
		tmpDir,
	}
	if strings.Join(synced, "|") != strings.Join(want, "|") {
		t.Fatalf("synced directories = %v, want %v", synced, want)
	}
}

func TestStore_PutGetDelete(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	hash := "abcdef1234567890abcdef1234567890"
	data := []byte("test content")

	// Test Put
	t.Run("Put", func(t *testing.T) {
		if err := store.Put(hash, data); err != nil {
			t.Fatalf("Put() error: %v", err)
		}

		// Verify file exists
		path := store.layout.FullPath(store.root, hash)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Error("Put() did not create file")
		}
	})

	// Test Has
	t.Run("Has", func(t *testing.T) {
		if !store.Has(hash) {
			t.Error("Has() = false, want true")
		}
		if store.Has("nonexistent") {
			t.Error("Has(nonexistent) = true, want false")
		}
	})

	// Test Get
	t.Run("Get", func(t *testing.T) {
		got, err := store.Get(hash)
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}
		if string(got) != string(data) {
			t.Errorf("Get() = %q, want %q", got, data)
		}
	})

	// Test Get non-existent
	t.Run("Get_NotFound", func(t *testing.T) {
		_, err := store.Get("nonexistent")
		if err != ErrNotFound {
			t.Errorf("Get(nonexistent) error = %v, want ErrNotFound", err)
		}
	})

	// Test Size
	t.Run("Size", func(t *testing.T) {
		size, err := store.Size(hash)
		if err != nil {
			t.Fatalf("Size() error: %v", err)
		}
		if size != int64(len(data)) {
			t.Errorf("Size() = %d, want %d", size, len(data))
		}
	})

	t.Run("Size_NotFound", func(t *testing.T) {
		_, err := store.Size("nonexistent")
		if err != ErrNotFound {
			t.Errorf("Size(nonexistent) error = %v, want ErrNotFound", err)
		}
	})

	// Test Reader
	t.Run("Reader", func(t *testing.T) {
		reader, err := store.Reader(hash)
		if err != nil {
			t.Fatalf("Reader() error: %v", err)
		}
		defer reader.Close()

		buf := make([]byte, len(data))
		n, err := reader.Read(buf)
		if err != nil {
			t.Fatalf("Read() error: %v", err)
		}
		if string(buf[:n]) != string(data) {
			t.Errorf("Reader content = %q, want %q", buf[:n], data)
		}
		if _, err := reader.Seek(0, 0); err != nil {
			t.Fatalf("Seek(0) error: %v", err)
		}
		secondRead := make([]byte, 4)
		n, err = reader.Read(secondRead)
		if err != nil {
			t.Fatalf("Read() after seek error: %v", err)
		}
		if string(secondRead[:n]) != "test" {
			t.Errorf("Reader after seek = %q, want test", secondRead[:n])
		}
	})

	t.Run("Reader_NotFound", func(t *testing.T) {
		reader, err := store.Reader("nonexistent")
		if err != ErrNotFound {
			t.Errorf("Reader(nonexistent) error = %v, want ErrNotFound", err)
		}
		if reader != nil {
			_ = reader.Close()
			t.Fatal("Reader(nonexistent) returned non-nil reader")
		}
	})

	// Test Delete
	t.Run("Delete", func(t *testing.T) {
		if err := store.Delete(hash); err != nil {
			t.Fatalf("Delete() error: %v", err)
		}
		if store.Has(hash) {
			t.Error("Delete() did not remove file")
		}
	})

	// Test Delete non-existent (should not error)
	t.Run("Delete_NotFound", func(t *testing.T) {
		if err := store.Delete("nonexistent"); err != nil {
			t.Errorf("Delete(nonexistent) error: %v", err)
		}
	})
}

func TestStore_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	hash := "test1234567890test1234567890test"
	data := []byte("atomic test content")

	// Put should use atomic write (.tmp -> rename)
	if err := store.Put(hash, data); err != nil {
		t.Fatalf("Put() error: %v", err)
	}

	// Verify no .tmp files remain
	found := false
	filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && filepath.Ext(path) == ".tmp" {
			found = true
		}
		return nil
	})

	if found {
		t.Error("Found .tmp file after Put() - atomic write may have failed")
	}
}

func TestNewStore_RejectsSymlinkRoot(t *testing.T) {
	tmpDir := t.TempDir()
	realRoot := filepath.Join(tmpDir, "real-root")
	if err := os.MkdirAll(realRoot, 0755); err != nil {
		t.Fatalf("MkdirAll(real-root) error: %v", err)
	}
	rootLink := filepath.Join(tmpDir, "root-link")
	if err := os.Symlink(realRoot, rootLink); err != nil {
		t.Fatalf("Symlink(root-link) error: %v", err)
	}

	_, err := NewStore(rootLink, nil)
	if !errors.Is(err, errCASPathSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestNewStore_RejectsSymlinkParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	realParent := filepath.Join(tmpDir, "real-parent")
	if err := os.MkdirAll(realParent, 0755); err != nil {
		t.Fatalf("MkdirAll(real-parent) error: %v", err)
	}
	linkedParent := filepath.Join(tmpDir, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("Symlink(linked-parent) error: %v", err)
	}

	_, err := NewStore(filepath.Join(linkedParent, "cas"), nil)
	if !errors.Is(err, errCASPathSymlink) {
		t.Fatalf("expected symlink parent rejection, got %v", err)
	}
}

func TestNewStore_DoesNotCreateRootThroughSymlinkParent(t *testing.T) {
	tmpDir := t.TempDir()
	realParent := filepath.Join(tmpDir, "real-parent")
	if err := os.MkdirAll(realParent, 0755); err != nil {
		t.Fatalf("MkdirAll(real-parent) error: %v", err)
	}
	linkedParent := filepath.Join(tmpDir, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("Symlink(linked-parent) error: %v", err)
	}

	root := filepath.Join(linkedParent, "cas")
	if _, err := NewStore(root, nil); !errors.Is(err, errCASPathSymlink) {
		t.Fatalf("expected symlink parent rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(realParent, "cas")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("CAS root created through symlink parent, stat error = %v", err)
	}
}

func TestNewStore_ReturnsDirectoryTreeSyncError(t *testing.T) {
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, "nested", "cas")

	originalSyncDir := syncDir
	syncDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncDir = originalSyncDir
	}()

	if _, err := NewStore(root, nil); err == nil {
		t.Fatal("expected NewStore() to fail when directory tree sync fails")
	} else if !strings.Contains(err.Error(), "failed to sync directory tree") {
		t.Fatalf("expected directory tree sync error, got %v", err)
	}
}

func TestStore_PutRejectsSymlinkObjectPath(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	hash := "abcdef1234567890abcdef1234567890"
	path := store.layout.FullPath(store.root, hash)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(object dir) error: %v", err)
	}
	targetPath := filepath.Join(tmpDir, "real-object")
	if err := os.WriteFile(targetPath, []byte("original"), 0644); err != nil {
		t.Fatalf("WriteFile(real-object) error: %v", err)
	}
	if err := os.Symlink(targetPath, path); err != nil {
		t.Fatalf("Symlink(object path) error: %v", err)
	}

	err = store.Put(hash, []byte("updated"))
	if !errors.Is(err, errCASPathSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile(real-object) error: %v", err)
	}
	if string(data) != "original" {
		t.Fatalf("expected symlink target unchanged, got %q", string(data))
	}
}

func TestStore_PutDoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	root := filepath.Join(baseDir, "cas")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("MkdirAll(outsideDir) error: %v", err)
	}

	store, err := NewStore(root, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	hash := "abcdef1234567890abcdef1234567890"
	objectPath := store.layout.FullPath(store.root, hash)
	shardDir := filepath.Dir(objectPath)
	if err := os.MkdirAll(shardDir, 0755); err != nil {
		t.Fatalf("MkdirAll(shardDir) error: %v", err)
	}
	outsideObjectPath := filepath.Join(outsideDir, filepath.Base(objectPath))
	if err := os.WriteFile(outsideObjectPath, []byte("outside-original"), 0644); err != nil {
		t.Fatalf("WriteFile(outsideObject) error: %v", err)
	}
	backupDir := filepath.Join(root, "backup-shard")

	originalHook := afterValidateCASPath
	var hookErr error
	afterValidateCASPath = func() {
		if hookErr != nil {
			return
		}
		if err := os.Rename(shardDir, backupDir); err != nil {
			hookErr = err
			return
		}
		if err := os.Symlink(outsideDir, shardDir); err != nil {
			hookErr = err
		}
	}
	defer func() {
		afterValidateCASPath = originalHook
	}()

	err = store.Put(hash, []byte("updated"))
	if hookErr != nil {
		t.Fatalf("afterValidateCASPath hook error: %v", hookErr)
	}
	if !errors.Is(err, errCASPathSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}

	data, err := os.ReadFile(outsideObjectPath)
	if err != nil {
		t.Fatalf("ReadFile(outsideObject) error: %v", err)
	}
	if string(data) != "outside-original" {
		t.Fatalf("expected outside object unchanged, got %q", string(data))
	}
}

func TestStore_PutReturnsDirectoryTreeSyncError(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	originalSyncRootDir := syncRootDir
	syncRootDir = func(root *os.Root, dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncRootDir = originalSyncRootDir
	}()

	hash := "abcdef1234567890abcdef1234567890"
	err = store.Put(hash, []byte("payload"))
	if err == nil {
		t.Fatal("expected Put() to fail when shard directory tree sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync directory tree") {
		t.Fatalf("expected directory tree sync error, got %v", err)
	}

	objectPath := store.layout.FullPath(store.root, hash)
	if _, statErr := os.Stat(objectPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no object to be created after directory tree sync failure, got %v", statErr)
	}
}

func TestStore_PutCleansCreatedDirectoriesWhenTempCreateFails(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	hash := "abcdef1234567890abcdef1234567890"
	objectPath := store.layout.FullPath(store.root, hash)
	objectDir := filepath.Dir(objectPath)

	originalHook := afterValidateCASPath
	var hookErr error
	hookApplied := false
	afterValidateCASPath = func() {
		if hookApplied || hookErr != nil {
			return
		}
		hookApplied = true
		hookErr = os.Chmod(objectDir, 0500)
	}
	defer func() {
		afterValidateCASPath = originalHook
		_ = os.Chmod(objectDir, 0755)
	}()

	err = store.Put(hash, []byte("payload"))
	if hookErr != nil {
		t.Fatalf("afterValidateCASPath hook error: %v", hookErr)
	}
	if err == nil {
		t.Fatal("expected Put() to fail when temp file creation fails")
	}
	if !errors.Is(err, errCASPathSymlink) {
		t.Fatalf("expected rooted temp create failure to stay mapped as symlink rejection, got %v", err)
	}
	if _, statErr := os.Stat(objectPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no object to be created, got %v", statErr)
	}
	if _, statErr := os.Stat(objectDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected created shard directory to be removed, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Dir(objectDir)); !os.IsNotExist(statErr) {
		t.Fatalf("expected created parent shard directory to be removed, got %v", statErr)
	}

	afterValidateCASPath = originalHook
	if err := store.Put(hash, []byte("payload")); err != nil {
		t.Fatalf("expected retry after failed Put() cleanup to succeed, got %v", err)
	}
}

func TestStore_PutReturnsDirectorySyncError(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	hash := "fedcba0987654321fedcba0987654321"
	objectPath := store.layout.FullPath(store.root, hash)
	if err := os.MkdirAll(filepath.Dir(objectPath), 0755); err != nil {
		t.Fatalf("MkdirAll(object dir) error: %v", err)
	}

	originalSyncRootDir := syncRootDir
	syncRootDir = func(root *os.Root, dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncRootDir = originalSyncRootDir
	}()

	err = store.Put(hash, []byte("payload"))
	if err == nil {
		t.Fatal("expected Put() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync directory") {
		t.Fatalf("expected directory sync error, got %v", err)
	}

	data, readErr := os.ReadFile(objectPath)
	if readErr != nil {
		t.Fatalf("expected renamed object to remain readable after sync failure, got %v", readErr)
	}
	if string(data) != "payload" {
		t.Fatalf("expected object payload to be preserved, got %q", string(data))
	}
	if matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(objectPath), ".cas-*.tmp")); globErr != nil {
		t.Fatalf("Glob(.cas-*.tmp) error: %v", globErr)
	} else if len(matches) != 0 {
		t.Fatalf("expected no leftover temp files, got %v", matches)
	}
}

func TestStore_DeleteReturnsDirectorySyncError(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	hash := "0123456789abcdef0123456789abcdef"
	if err := store.Put(hash, []byte("payload")); err != nil {
		t.Fatalf("Put() error: %v", err)
	}

	originalSyncRootDir := syncRootDir
	syncRootDir = func(root *os.Root, dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncRootDir = originalSyncRootDir
	}()

	err = store.Delete(hash)
	if err == nil {
		t.Fatal("expected Delete() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync directory") {
		t.Fatalf("expected directory sync error, got %v", err)
	}
	if store.Has(hash) {
		t.Fatal("expected object to remain deleted after directory sync failure")
	}
}

func TestStore_GetRejectsSymlinkObjectPath(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	hash := "1234567890abcdef1234567890abcdef"
	path := store.layout.FullPath(store.root, hash)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(object dir) error: %v", err)
	}
	targetPath := filepath.Join(tmpDir, "real-object")
	if err := os.WriteFile(targetPath, []byte("payload"), 0644); err != nil {
		t.Fatalf("WriteFile(real-object) error: %v", err)
	}
	if err := os.Symlink(targetPath, path); err != nil {
		t.Fatalf("Symlink(object path) error: %v", err)
	}

	_, err = store.Get(hash)
	if !errors.Is(err, errCASPathSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestStore_GetDoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	root := filepath.Join(baseDir, "cas")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("MkdirAll(outsideDir) error: %v", err)
	}

	store, err := NewStore(root, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	hash := "1234567890abcdef1234567890abcdef"
	objectPath := store.layout.FullPath(store.root, hash)
	shardDir := filepath.Dir(objectPath)
	if err := os.MkdirAll(shardDir, 0755); err != nil {
		t.Fatalf("MkdirAll(shardDir) error: %v", err)
	}
	if err := os.WriteFile(objectPath, []byte("root-payload"), 0644); err != nil {
		t.Fatalf("WriteFile(objectPath) error: %v", err)
	}
	outsideObjectPath := filepath.Join(outsideDir, filepath.Base(objectPath))
	if err := os.WriteFile(outsideObjectPath, []byte("outside-payload"), 0644); err != nil {
		t.Fatalf("WriteFile(outsideObject) error: %v", err)
	}
	backupDir := filepath.Join(root, "backup-shard")

	originalHook := afterValidateCASPath
	var hookErr error
	afterValidateCASPath = func() {
		if hookErr != nil {
			return
		}
		if err := os.Rename(shardDir, backupDir); err != nil {
			hookErr = err
			return
		}
		if err := os.Symlink(outsideDir, shardDir); err != nil {
			hookErr = err
		}
	}
	defer func() {
		afterValidateCASPath = originalHook
	}()

	_, err = store.Get(hash)
	if hookErr != nil {
		t.Fatalf("afterValidateCASPath hook error: %v", hookErr)
	}
	if !errors.Is(err, errCASPathSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestStore_SizeAndReaderDoNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	for _, operation := range []string{"size", "reader"} {
		t.Run(operation, func(t *testing.T) {
			baseDir := t.TempDir()
			root := filepath.Join(baseDir, "cas")
			outsideDir := filepath.Join(baseDir, "outside")
			if err := os.MkdirAll(outsideDir, 0755); err != nil {
				t.Fatalf("MkdirAll(outsideDir) error: %v", err)
			}

			store, err := NewStore(root, nil)
			if err != nil {
				t.Fatalf("NewStore() error: %v", err)
			}

			hash := "2234567890abcdef1234567890abcdef"
			objectPath := store.layout.FullPath(store.root, hash)
			shardDir := filepath.Dir(objectPath)
			if err := os.MkdirAll(shardDir, 0755); err != nil {
				t.Fatalf("MkdirAll(shardDir) error: %v", err)
			}
			if err := os.WriteFile(objectPath, []byte("root-payload"), 0644); err != nil {
				t.Fatalf("WriteFile(objectPath) error: %v", err)
			}
			outsideObjectPath := filepath.Join(outsideDir, filepath.Base(objectPath))
			if err := os.WriteFile(outsideObjectPath, []byte("outside-payload"), 0644); err != nil {
				t.Fatalf("WriteFile(outsideObject) error: %v", err)
			}
			backupDir := filepath.Join(root, "backup-shard")

			originalHook := afterValidateCASPath
			var hookErr error
			afterValidateCASPath = func() {
				if hookErr != nil {
					return
				}
				if err := os.Rename(shardDir, backupDir); err != nil {
					hookErr = err
					return
				}
				if err := os.Symlink(outsideDir, shardDir); err != nil {
					hookErr = err
				}
			}
			defer func() {
				afterValidateCASPath = originalHook
			}()

			var opErr error
			if operation == "size" {
				_, opErr = store.Size(hash)
			} else {
				var reader ReadSeekCloser
				reader, opErr = store.Reader(hash)
				if reader != nil {
					_ = reader.Close()
				}
			}
			if hookErr != nil {
				t.Fatalf("afterValidateCASPath hook error: %v", hookErr)
			}
			if !errors.Is(opErr, errCASPathSymlink) {
				t.Fatalf("expected symlink rejection, got %v", opErr)
			}

			data, err := os.ReadFile(outsideObjectPath)
			if err != nil {
				t.Fatalf("ReadFile(outsideObject) error: %v", err)
			}
			if string(data) != "outside-payload" {
				t.Fatalf("expected outside object unchanged, got %q", string(data))
			}
		})
	}
}

func TestStore_Walk(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	// Add some objects
	hashes := []string{
		"aaaa1234567890aaaa1234567890aaaa",
		"bbbb1234567890bbbb1234567890bbbb",
		"cccc1234567890cccc1234567890cccc",
	}

	for _, hash := range hashes {
		if err := store.Put(hash, []byte("data-"+hash)); err != nil {
			t.Fatalf("Put(%s) error: %v", hash, err)
		}
	}

	// Create a .tmp file that should be skipped
	tmpFile := filepath.Join(tmpDir, "test.tmp")
	os.WriteFile(tmpFile, []byte("tmp"), 0644)

	// Walk and collect
	var collected []string
	err = store.Walk(func(hash string) error {
		collected = append(collected, hash)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() error: %v", err)
	}

	// Should have exactly 3 objects (tmp file skipped)
	if len(collected) != 3 {
		t.Errorf("Walk() collected %d hashes, want 3", len(collected))
	}
}

func TestStore_WalkPropagatesCallbackError(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	hash := "dddd1234567890dddd1234567890dddd"
	if err := store.Put(hash, []byte("payload")); err != nil {
		t.Fatalf("Put(hash) error: %v", err)
	}

	expectedErr := errors.New("stop walk")
	err = store.Walk(func(gotHash string) error {
		if gotHash != hash {
			t.Fatalf("Walk() hash = %s, want %s", gotHash, hash)
		}
		return expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("Walk() error = %v, want %v", err, expectedErr)
	}
}

func TestStore_Walk_DoesNotFollowRootSymlinkInsertedAfterValidation(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	originalHash := "hash4444444444444444444444444444"
	if err := store.Put(originalHash, []byte("original-data")); err != nil {
		t.Fatalf("Put(originalHash) error: %v", err)
	}

	outsideRoot := t.TempDir()
	outsideHash := "hash5555555555555555555555555555"
	outsidePath := store.layout.FullPath(outsideRoot, outsideHash)
	if err := os.MkdirAll(filepath.Dir(outsidePath), 0755); err != nil {
		t.Fatalf("MkdirAll(outside shard dir) error: %v", err)
	}
	if err := os.WriteFile(outsidePath, []byte("outside-data"), 0644); err != nil {
		t.Fatalf("WriteFile(outside object) error: %v", err)
	}

	backupRoot := tmpDir + "-backup"
	originalHook := afterValidateCASPath
	afterValidateCASPath = func() {
		if err := os.Rename(tmpDir, backupRoot); err != nil {
			t.Fatalf("Rename(CAS root) error: %v", err)
		}
		if err := os.Symlink(outsideRoot, tmpDir); err != nil {
			t.Fatalf("Symlink(CAS root) error: %v", err)
		}
	}
	t.Cleanup(func() {
		afterValidateCASPath = originalHook
		if info, err := os.Lstat(tmpDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(tmpDir); removeErr != nil {
				t.Errorf("Remove(CAS root symlink) error: %v", removeErr)
			}
		}
		if _, err := os.Stat(backupRoot); err == nil {
			if renameErr := os.Rename(backupRoot, tmpDir); renameErr != nil {
				t.Errorf("Rename(backup root) error: %v", renameErr)
			}
		}
	})

	var walked []string
	if err := store.Walk(func(hash string) error {
		walked = append(walked, hash)
		return nil
	}); err != nil {
		t.Fatalf("Walk() error: %v", err)
	}
	if len(walked) != 1 || walked[0] != originalHash {
		t.Fatalf("Walk() returned %v, want [%s]", walked, originalHash)
	}
}

func TestStore_Stats(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	// Empty stats
	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats() error: %v", err)
	}
	if stats.TotalObjects != 0 {
		t.Errorf("Empty Stats.TotalObjects = %d, want 0", stats.TotalObjects)
	}

	// Add objects
	data1 := []byte("data1")
	data2 := []byte("data2longer")
	store.Put("hash1111111111111111111111111111", data1)
	store.Put("hash2222222222222222222222222222", data2)

	stats, err = store.Stats()
	if err != nil {
		t.Fatalf("Stats() error: %v", err)
	}

	if stats.TotalObjects != 2 {
		t.Errorf("Stats.TotalObjects = %d, want 2", stats.TotalObjects)
	}
	expectedSize := int64(len(data1) + len(data2))
	if stats.TotalSize != expectedSize {
		t.Errorf("Stats.TotalSize = %d, want %d", stats.TotalSize, expectedSize)
	}
}

func TestStore_Stats_DoesNotFollowRootSymlinkInsertedAfterValidation(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	originalData := []byte("original-data")
	if err := store.Put("hash6666666666666666666666666666", originalData); err != nil {
		t.Fatalf("Put(original hash) error: %v", err)
	}

	outsideRoot := t.TempDir()
	outsidePath := store.layout.FullPath(outsideRoot, "hash7777777777777777777777777777")
	if err := os.MkdirAll(filepath.Dir(outsidePath), 0755); err != nil {
		t.Fatalf("MkdirAll(outside shard dir) error: %v", err)
	}
	if err := os.WriteFile(outsidePath, []byte("outside-data"), 0644); err != nil {
		t.Fatalf("WriteFile(outside object) error: %v", err)
	}

	backupRoot := tmpDir + "-backup"
	originalHook := afterValidateCASPath
	afterValidateCASPath = func() {
		if err := os.Rename(tmpDir, backupRoot); err != nil {
			t.Fatalf("Rename(CAS root) error: %v", err)
		}
		if err := os.Symlink(outsideRoot, tmpDir); err != nil {
			t.Fatalf("Symlink(CAS root) error: %v", err)
		}
	}
	t.Cleanup(func() {
		afterValidateCASPath = originalHook
		if info, err := os.Lstat(tmpDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(tmpDir); removeErr != nil {
				t.Errorf("Remove(CAS root symlink) error: %v", removeErr)
			}
		}
		if _, err := os.Stat(backupRoot); err == nil {
			if renameErr := os.Rename(backupRoot, tmpDir); renameErr != nil {
				t.Errorf("Rename(backup root) error: %v", renameErr)
			}
		}
	})

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats() error: %v", err)
	}
	if stats.TotalObjects != 1 {
		t.Fatalf("Stats.TotalObjects = %d, want 1", stats.TotalObjects)
	}
	if stats.TotalSize != int64(len(originalData)) {
		t.Fatalf("Stats.TotalSize = %d, want %d", stats.TotalSize, len(originalData))
	}
}

func TestStore_Stats_IgnoresInvalidObjectPaths(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	validHash := "hash3333333333333333333333333333"
	validData := []byte("valid-data")
	if err := store.Put(validHash, validData); err != nil {
		t.Fatalf("Put(validHash) error: %v", err)
	}

	strayPath := filepath.Join(tmpDir, "unexpected.bin")
	if err := os.WriteFile(strayPath, []byte("stray-data"), 0644); err != nil {
		t.Fatalf("WriteFile(strayPath) error: %v", err)
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats() error: %v", err)
	}

	if stats.TotalObjects != 1 {
		t.Fatalf("Stats.TotalObjects = %d, want 1", stats.TotalObjects)
	}
	if stats.TotalSize != int64(len(validData)) {
		t.Fatalf("Stats.TotalSize = %d, want %d", stats.TotalSize, len(validData))
	}
	var walked []string
	if walkErr := store.Walk(func(hash string) error {
		walked = append(walked, hash)
		if hash != validHash {
			t.Fatalf("unexpected hash from Walk(): %s", hash)
		}
		return nil
	}); walkErr != nil {
		t.Fatalf("Walk() error: %v", walkErr)
	}
	if len(walked) != 1 {
		t.Fatalf("Walk() returned %d hashes, want 1", len(walked))
	}
}

func TestStore_CleanupStaging(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	// Create some .tmp files simulating incomplete writes
	layout := NewShardedLayout(2, 2)
	tmpPath1 := layout.FullPath(tmpDir, "ab12staging1") + ".tmp"
	tmpPath2 := layout.FullPath(tmpDir, "cd34staging2") + ".tmp"

	os.MkdirAll(filepath.Dir(tmpPath1), 0755)
	os.MkdirAll(filepath.Dir(tmpPath2), 0755)
	os.WriteFile(tmpPath1, []byte("incomplete1"), 0644)
	os.WriteFile(tmpPath2, []byte("incomplete22"), 0644)

	// Run cleanup
	count, size, err := store.CleanupStaging()
	if err != nil {
		t.Fatalf("CleanupStaging() error: %v", err)
	}

	if count != 2 {
		t.Errorf("CleanupStaging() count = %d, want 2", count)
	}
	if size != int64(len("incomplete1")+len("incomplete22")) {
		t.Errorf("CleanupStaging() size = %d, want %d", size, len("incomplete1")+len("incomplete22"))
	}

	// Verify files are gone
	if _, err := os.Stat(tmpPath1); !os.IsNotExist(err) {
		t.Error("tmp file 1 still exists after cleanup")
	}
	if _, err := os.Stat(tmpPath2); !os.IsNotExist(err) {
		t.Error("tmp file 2 still exists after cleanup")
	}
}

func TestStore_CleanupStaging_DoesNotFollowRootSymlinkInsertedAfterValidation(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	originalTmpPath := store.layout.FullPath(tmpDir, "ab12staging3") + ".tmp"
	if err := os.MkdirAll(filepath.Dir(originalTmpPath), 0755); err != nil {
		t.Fatalf("MkdirAll(original tmp dir) error: %v", err)
	}
	if err := os.WriteFile(originalTmpPath, []byte("inside-staging"), 0644); err != nil {
		t.Fatalf("WriteFile(original tmp) error: %v", err)
	}

	outsideRoot := t.TempDir()
	outsideTmpPath := store.layout.FullPath(outsideRoot, "cd34staging4") + ".tmp"
	if err := os.MkdirAll(filepath.Dir(outsideTmpPath), 0755); err != nil {
		t.Fatalf("MkdirAll(outside tmp dir) error: %v", err)
	}
	if err := os.WriteFile(outsideTmpPath, []byte("outside-staging"), 0644); err != nil {
		t.Fatalf("WriteFile(outside tmp) error: %v", err)
	}

	backupRoot := tmpDir + "-backup"
	originalHook := afterValidateCASPath
	afterValidateCASPath = func() {
		if err := os.Rename(tmpDir, backupRoot); err != nil {
			t.Fatalf("Rename(CAS root) error: %v", err)
		}
		if err := os.Symlink(outsideRoot, tmpDir); err != nil {
			t.Fatalf("Symlink(CAS root) error: %v", err)
		}
	}
	t.Cleanup(func() {
		afterValidateCASPath = originalHook
		if info, err := os.Lstat(tmpDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(tmpDir); removeErr != nil {
				t.Errorf("Remove(CAS root symlink) error: %v", removeErr)
			}
		}
		if _, err := os.Stat(backupRoot); err == nil {
			if renameErr := os.Rename(backupRoot, tmpDir); renameErr != nil {
				t.Errorf("Rename(backup root) error: %v", renameErr)
			}
		}
	})

	count, size, err := store.CleanupStaging()
	if err != nil {
		t.Fatalf("CleanupStaging() error: %v", err)
	}
	if count != 1 {
		t.Fatalf("CleanupStaging() count = %d, want 1", count)
	}
	if size != int64(len("inside-staging")) {
		t.Fatalf("CleanupStaging() size = %d, want %d", size, len("inside-staging"))
	}
	originalRelPath, err := filepath.Rel(tmpDir, originalTmpPath)
	if err != nil {
		t.Fatalf("filepath.Rel(originalTmpPath) error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backupRoot, originalRelPath)); !os.IsNotExist(err) {
		t.Fatalf("expected anchored tmp file removed, got %v", err)
	}
	data, err := os.ReadFile(outsideTmpPath)
	if err != nil {
		t.Fatalf("ReadFile(outside tmp) error: %v", err)
	}
	if string(data) != "outside-staging" {
		t.Fatalf("expected outside tmp file unchanged, got %q", string(data))
	}
}

func TestStore_CleanupStaging_DoesNotCountBytesWhenRemoveFails(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	tmpPath := store.layout.FullPath(tmpDir, "ef56staging5") + ".tmp"
	shardDir := filepath.Dir(tmpPath)
	if err := os.MkdirAll(shardDir, 0755); err != nil {
		t.Fatalf("MkdirAll(shard dir) error: %v", err)
	}
	if err := os.WriteFile(tmpPath, []byte("not-removed"), 0644); err != nil {
		t.Fatalf("WriteFile(tmp) error: %v", err)
	}
	if err := os.Chmod(shardDir, 0555); err != nil {
		t.Fatalf("Chmod(shard dir) error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(shardDir, 0755)
	})

	count, size, err := store.CleanupStaging()
	if err == nil {
		if _, statErr := os.Stat(tmpPath); os.IsNotExist(statErr) {
			t.Skip("filesystem permits tmp removal from read-only directory")
		}
		t.Fatal("CleanupStaging() expected error when tmp removal fails")
	}
	if count != 0 {
		t.Fatalf("CleanupStaging() count = %d, want 0", count)
	}
	if size != 0 {
		t.Fatalf("CleanupStaging() size = %d, want 0", size)
	}
	if _, statErr := os.Stat(tmpPath); statErr != nil {
		t.Fatalf("expected tmp file to remain after failed cleanup, got %v", statErr)
	}
	if !errors.Is(err, errCASPathSymlink) {
		t.Fatalf("CleanupStaging() error = %v, want %v", err, errCASPathSymlink)
	}
}

func TestNewShardedLayout_Defaults(t *testing.T) {
	// Test invalid parameters get corrected
	l := NewShardedLayout(0, 0)
	if l.Levels != 2 {
		t.Errorf("Levels = %d, want 2 (default)", l.Levels)
	}
	if l.ShardSize != 2 {
		t.Errorf("ShardSize = %d, want 2 (default)", l.ShardSize)
	}
}

func TestNewStore_CreateDir(t *testing.T) {
	tmpDir := t.TempDir()
	newPath := filepath.Join(tmpDir, "nested", "dir", "store")

	store, err := NewStore(newPath, nil)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if _, err := os.Stat(store.Root()); os.IsNotExist(err) {
		t.Error("NewStore() did not create root directory")
	}
}
