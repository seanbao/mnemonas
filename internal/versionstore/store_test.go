package versionstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupStore(t *testing.T) *Store {
	tmpDir := t.TempDir()
	s, err := New(Config{
		DBPath:     filepath.Join(tmpDir, "test.db"),
		ObjectRoot: filepath.Join(tmpDir, "objects"),
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNew(t *testing.T) {
	tmpDir := t.TempDir()
	s, err := New(Config{
		DBPath:     filepath.Join(tmpDir, "test.db"),
		ObjectRoot: filepath.Join(tmpDir, "objects"),
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer s.Close()

	// Check database file was created
	if _, err := os.Stat(filepath.Join(tmpDir, "test.db")); err != nil {
		t.Errorf("Database file not created: %v", err)
	}

	// Check objects directory was created
	if _, err := os.Stat(filepath.Join(tmpDir, "objects")); err != nil {
		t.Errorf("Objects directory not created: %v", err)
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

func TestStore_DeleteOldVersions(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	// Add multiple versions with different hashes
	for i := 0; i < 10; i++ {
		hash := "hash" + string(rune('a'+i))
		s.AddVersion(ctx, "/test.txt", hash, int64(i*100), "")
		time.Sleep(10 * time.Millisecond)
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

func TestStore_ClearTrash(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	// Add multiple items
	for i := 0; i < 5; i++ {
		s.AddToTrash(ctx, &TrashItem{
			ID:           "trash" + string(rune('0'+i)),
			OriginalPath: "/file" + string(rune('0'+i)) + ".txt",
			Size:         100,
			DeletedAt:    time.Now(),
			ExpiresAt:    time.Now().Add(time.Hour),
		})
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
	s.AddToTrash(ctx, &TrashItem{
		ID:           "expired",
		OriginalPath: "/expired.txt",
		Size:         100,
		DeletedAt:    time.Now().Add(-48 * time.Hour),
		ExpiresAt:    time.Now().Add(-24 * time.Hour), // Already expired
	})

	// Add non-expired item
	s.AddToTrash(ctx, &TrashItem{
		ID:           "valid",
		OriginalPath: "/valid.txt",
		Size:         100,
		DeletedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	})

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

func TestStore_Objects(t *testing.T) {
	s := setupStore(t)

	hash := "abcd1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"
	data := []byte("version content")

	// Put object
	err := s.PutObject(hash, data)
	if err != nil {
		t.Fatalf("PutObject() error: %v", err)
	}

	// Check exists
	if !s.HasObject(hash) {
		t.Error("HasObject() returned false for existing object")
	}

	// Get object
	got, err := s.GetObject(hash)
	if err != nil {
		t.Fatalf("GetObject() error: %v", err)
	}

	if string(got) != string(data) {
		t.Errorf("GetObject() = %q, want %q", got, data)
	}

	// Delete object
	err = s.DeleteObject(hash)
	if err != nil {
		t.Fatalf("DeleteObject() error: %v", err)
	}

	if s.HasObject(hash) {
		t.Error("HasObject() returned true after delete")
	}
}

func TestStore_ObjectPath(t *testing.T) {
	s := setupStore(t)

	hash := "abcd1234"
	got := s.ObjectPath(hash)

	if !filepath.IsAbs(got) {
		t.Errorf("ObjectPath(%s) should be absolute", hash)
	}

	// Should contain sharded path
	if !containsSubpath(got, "ab/cd/abcd1234") {
		t.Errorf("ObjectPath(%s) = %s, should contain sharded path", hash, got)
	}
}

func containsSubpath(fullPath, subpath string) bool {
	return len(fullPath) >= len(subpath) && fullPath[len(fullPath)-len(subpath):] == subpath
}
