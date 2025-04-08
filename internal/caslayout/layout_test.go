// Package caslayout provides directory layout for content-addressable storage
package caslayout

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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
