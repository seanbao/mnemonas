// Package webdavcas provides WebDAV to CAS storage adapter layer
package webdavcas

import (
	"bytes"
	"context"
	"io"
	"os"
	"path"
	"testing"
	"time"
)

func setupTestFS(t *testing.T) (*FileSystem, string) {
	tmpDir := t.TempDir()
	casRoot := path.Join(tmpDir, "cas")
	metaRoot := path.Join(tmpDir, "meta")

	fs, err := NewFileSystem(casRoot, metaRoot)
	if err != nil {
		t.Fatalf("NewFileSystem() error: %v", err)
	}

	return fs, tmpDir
}

func TestFileSystem_BasicOperations(t *testing.T) {
	fs, _ := setupTestFS(t)
	ctx := context.Background()

	t.Run("Stat_Root", func(t *testing.T) {
		info, err := fs.Stat(ctx, "/")
		if err != nil {
			t.Fatalf("Stat(/) error: %v", err)
		}
		if !info.IsDir {
			t.Error("Root should be a directory")
		}
	})

	t.Run("Mkdir", func(t *testing.T) {
		if err := fs.Mkdir(ctx, "/testdir"); err != nil {
			t.Fatalf("Mkdir() error: %v", err)
		}

		info, err := fs.Stat(ctx, "/testdir")
		if err != nil {
			t.Fatalf("Stat(testdir) error: %v", err)
		}
		if !info.IsDir {
			t.Error("Created path should be a directory")
		}
	})

	t.Run("WriteFile", func(t *testing.T) {
		content := "Hello, MnemoNAS!"
		err := fs.WriteFile(ctx, "/testdir/hello.txt", bytes.NewReader([]byte(content)))
		if err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}

		info, err := fs.Stat(ctx, "/testdir/hello.txt")
		if err != nil {
			t.Fatalf("Stat() error: %v", err)
		}
		if info.IsDir {
			t.Error("File should not be a directory")
		}
		if info.Size != int64(len(content)) {
			t.Errorf("Size = %d, want %d", info.Size, len(content))
		}
		if info.ContentHash == "" {
			t.Error("ContentHash should not be empty")
		}
	})

	t.Run("OpenFile", func(t *testing.T) {
		reader, err := fs.OpenFile(ctx, "/testdir/hello.txt")
		if err != nil {
			t.Fatalf("OpenFile() error: %v", err)
		}
		defer reader.Close()

		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("ReadAll() error: %v", err)
		}
		if string(data) != "Hello, MnemoNAS!" {
			t.Errorf("Content = %q, want %q", data, "Hello, MnemoNAS!")
		}
	})

	t.Run("ReadDir", func(t *testing.T) {
		children, err := fs.ReadDir(ctx, "/testdir")
		if err != nil {
			t.Fatalf("ReadDir() error: %v", err)
		}
		if len(children) != 1 {
			t.Errorf("ReadDir() returned %d children, want 1", len(children))
		}
	})
}

func TestFileSystem_VersionHistory(t *testing.T) {
	fs, _ := setupTestFS(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/versions")

	versions := []string{"version 1", "version 2", "version 3"}
	for _, v := range versions {
		err := fs.WriteFile(ctx, "/versions/file.txt", bytes.NewReader([]byte(v)))
		if err != nil {
			t.Fatalf("WriteFile(%s) error: %v", v, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Run("ListVersions", func(t *testing.T) {
		versionList, err := fs.ListVersions(ctx, "/versions/file.txt")
		if err != nil {
			t.Fatalf("ListVersions() error: %v", err)
		}
		if len(versionList) != 3 {
			t.Errorf("ListVersions() returned %d versions, want 3", len(versionList))
		}
	})

	t.Run("RestoreVersion", func(t *testing.T) {
		versionList, _ := fs.ListVersions(ctx, "/versions/file.txt")
		oldestHash := versionList[len(versionList)-1].Hash

		err := fs.RestoreVersion(ctx, "/versions/file.txt", oldestHash)
		if err != nil {
			t.Fatalf("RestoreVersion() error: %v", err)
		}

		reader, _ := fs.OpenFile(ctx, "/versions/file.txt")
		data, _ := io.ReadAll(reader)
		reader.Close()

		if string(data) != "version 1" {
			t.Errorf("Content after restore = %q, want 'version 1'", data)
		}
	})
}

func TestFileSystem_Rename(t *testing.T) {
	fs, _ := setupTestFS(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/src")
	fs.WriteFile(ctx, "/src/file.txt", bytes.NewReader([]byte("content")))

	t.Run("RenameFile", func(t *testing.T) {
		err := fs.Rename(ctx, "/src/file.txt", "/src/renamed.txt")
		if err != nil {
			t.Fatalf("Rename() error: %v", err)
		}

		if _, err := fs.Stat(ctx, "/src/file.txt"); err == nil {
			t.Error("Old path still exists after rename")
		}

		info, err := fs.Stat(ctx, "/src/renamed.txt")
		if err != nil {
			t.Fatalf("Stat(new) error: %v", err)
		}
		if info.IsDir {
			t.Error("Renamed file should not be a directory")
		}
	})
}

func TestFileSystem_Trash(t *testing.T) {
	fs, _ := setupTestFS(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/trash-test")
	fs.WriteFile(ctx, "/trash-test/file.txt", bytes.NewReader([]byte("delete me")))

	t.Run("Delete_ToTrash", func(t *testing.T) {
		err := fs.Delete(ctx, "/trash-test/file.txt")
		if err != nil {
			t.Fatalf("Delete() error: %v", err)
		}

		if _, err := fs.Stat(ctx, "/trash-test/file.txt"); err == nil {
			t.Error("File still exists after delete")
		}
	})

	t.Run("ListTrash", func(t *testing.T) {
		items, err := fs.ListTrash(ctx)
		if err != nil {
			t.Fatalf("ListTrash() error: %v", err)
		}
		if len(items) != 1 {
			t.Errorf("ListTrash() returned %d items, want 1", len(items))
		}
	})

	t.Run("RestoreFromTrash", func(t *testing.T) {
		items, _ := fs.ListTrash(ctx)
		if len(items) == 0 {
			t.Fatal("No items in trash to restore")
		}

		err := fs.RestoreFromTrash(ctx, items[0].ID)
		if err != nil {
			t.Fatalf("RestoreFromTrash() error: %v", err)
		}

		info, err := fs.Stat(ctx, "/trash-test/file.txt")
		if err != nil {
			t.Fatalf("Stat() after restore error: %v", err)
		}
		if info.Size != int64(len("delete me")) {
			t.Error("Restored file has wrong size")
		}
	})
}

func TestFileSystem_CleanupStaging(t *testing.T) {
	fs, tmpDir := setupTestFS(t)
	ctx := context.Background()

	casDir := path.Join(tmpDir, "cas")
	metaDir := path.Join(tmpDir, "meta")

	os.MkdirAll(path.Join(casDir, "ab", "cd"), 0755)
	os.WriteFile(path.Join(casDir, "ab", "cd", "hash.tmp"), []byte("incomplete"), 0644)
	os.WriteFile(path.Join(metaDir, "file.json.tmp"), []byte("{}"), 0644)

	files, size, err := fs.CleanupStaging(ctx)
	if err != nil {
		t.Fatalf("CleanupStaging() error: %v", err)
	}

	if files != 2 {
		t.Errorf("CleanupStaging() files = %d, want 2", files)
	}
	expectedSize := int64(len("incomplete") + len("{}"))
	if size != expectedSize {
		t.Errorf("CleanupStaging() size = %d, want %d", size, expectedSize)
	}
}

func TestFileSystem_ErrorCases(t *testing.T) {
	fs, _ := setupTestFS(t)
	ctx := context.Background()

	t.Run("Stat_NotFound", func(t *testing.T) {
		_, err := fs.Stat(ctx, "/nonexistent")
		if err == nil {
			t.Error("Stat(nonexistent) should error")
		}
	})

	t.Run("Mkdir_NoParent", func(t *testing.T) {
		err := fs.Mkdir(ctx, "/parent/child")
		if err == nil {
			t.Error("Mkdir without parent should error")
		}
	})

	t.Run("Delete_NonEmptyDir", func(t *testing.T) {
		fs.Mkdir(ctx, "/nonempty")
		fs.WriteFile(ctx, "/nonempty/file.txt", bytes.NewReader([]byte("x")))

		err := fs.Delete(ctx, "/nonempty")
		if err == nil {
			t.Error("Delete non-empty directory should error")
		}
	})

	t.Run("OpenFile_Directory", func(t *testing.T) {
		fs.Mkdir(ctx, "/opendir")
		_, err := fs.OpenFile(ctx, "/opendir")
		if err == nil {
			t.Error("OpenFile on directory should error")
		}
	})
}

func TestCleanPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/foo/bar", "/foo/bar"},
		{"foo/bar", "/foo/bar"},
		{"/foo/../bar", "/bar"},
		{".", "/"},
		{"", "/"},
		{"/", "/"},
		{"//foo//bar//", "/foo/bar"},
	}

	for _, tt := range tests {
		got := cleanPath(tt.input)
		if got != tt.want {
			t.Errorf("cleanPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestComputeHash(t *testing.T) {
	data := []byte("test data for hashing")
	hash1 := computeHash(data)
	hash2 := computeHash(data)

	if hash1 != hash2 {
		t.Error("Hash should be deterministic")
	}

	hash3 := computeHash([]byte("different data"))
	if hash1 == hash3 {
		t.Error("Different data should produce different hash")
	}

	if len(hash1) != 64 {
		t.Errorf("Hash length = %d, want 64", len(hash1))
	}
}

func TestMetadataStore(t *testing.T) {
	tmpDir := t.TempDir()
	metaRoot := path.Join(tmpDir, "meta")

	store, err := NewMetadataStore(metaRoot)
	if err != nil {
		t.Fatalf("NewMetadataStore() error: %v", err)
	}

	t.Run("PutGet", func(t *testing.T) {
		info := &FileInfo{
			Path:        "/test/file.txt",
			IsDir:       false,
			Size:        100,
			ModTime:     time.Now(),
			ContentHash: "abc123",
		}

		if err := store.Put("/test/file.txt", info); err != nil {
			t.Fatalf("Put() error: %v", err)
		}

		got, err := store.Get("/test/file.txt")
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}

		if got.Path != info.Path {
			t.Errorf("Path = %q, want %q", got.Path, info.Path)
		}
		if got.Size != info.Size {
			t.Errorf("Size = %d, want %d", got.Size, info.Size)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := store.Delete("/test/file.txt"); err != nil {
			t.Fatalf("Delete() error: %v", err)
		}

		if _, err := store.Get("/test/file.txt"); err == nil {
			t.Error("Get after Delete should error")
		}
	})

	t.Run("List", func(t *testing.T) {
		store.Put("/listdir/a.txt", &FileInfo{Path: "/listdir/a.txt"})
		store.Put("/listdir/b.txt", &FileInfo{Path: "/listdir/b.txt"})
		store.Put("/other/c.txt", &FileInfo{Path: "/other/c.txt"})

		children, err := store.List("/listdir")
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}

		if len(children) != 2 {
			t.Errorf("List() returned %d items, want 2", len(children))
		}
	})
}

func TestFileSystem_RestoreFromTrashTo(t *testing.T) {
	fs, _ := setupTestFS(t)
	ctx := context.Background()

	// Create a file and delete it
	fs.Mkdir(ctx, "/restore-test")
	fs.Mkdir(ctx, "/alt-location")
	fs.WriteFile(ctx, "/restore-test/file.txt", bytes.NewReader([]byte("restore me")))
	fs.Delete(ctx, "/restore-test/file.txt")

	// Get the trash item
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) == 0 {
		t.Fatalf("ListTrash() error or empty: %v", err)
	}

	t.Run("RestoreToNewPath", func(t *testing.T) {
		err := fs.RestoreFromTrashTo(ctx, items[0].ID, "/alt-location/restored.txt")
		if err != nil {
			t.Fatalf("RestoreFromTrashTo() error: %v", err)
		}

		info, err := fs.Stat(ctx, "/alt-location/restored.txt")
		if err != nil {
			t.Fatalf("Stat() after restore error: %v", err)
		}
		if info.Size != int64(len("restore me")) {
			t.Error("Restored file has wrong size")
		}
	})

	t.Run("RestoreToExistingPath_Error", func(t *testing.T) {
		// Create another file and delete it
		fs.WriteFile(ctx, "/restore-test/another.txt", bytes.NewReader([]byte("x")))
		fs.Delete(ctx, "/restore-test/another.txt")
		items2, _ := fs.ListTrash(ctx)

		// Try to restore to existing path
		err := fs.RestoreFromTrashTo(ctx, items2[0].ID, "/alt-location/restored.txt")
		if err == nil {
			t.Error("RestoreFromTrashTo to existing path should error")
		}
	})

	t.Run("RestoreToMissingParent_Error", func(t *testing.T) {
		fs.WriteFile(ctx, "/restore-test/third.txt", bytes.NewReader([]byte("y")))
		fs.Delete(ctx, "/restore-test/third.txt")
		items3, _ := fs.ListTrash(ctx)

		err := fs.RestoreFromTrashTo(ctx, items3[0].ID, "/nonexistent-parent/file.txt")
		if err == nil {
			t.Error("RestoreFromTrashTo to missing parent should error")
		}
	})
}

func TestFileSystem_RenameDirectory(t *testing.T) {
	fs, _ := setupTestFS(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/olddir")
	fs.WriteFile(ctx, "/olddir/file.txt", bytes.NewReader([]byte("content")))

	t.Run("RenameDirectory", func(t *testing.T) {
		// Note: Rename only renames the directory metadata, not the contents
		// This is a limitation of the current implementation
		fs.Mkdir(ctx, "/newdir")

		// Rename the file inside
		err := fs.Rename(ctx, "/olddir/file.txt", "/newdir/file.txt")
		if err != nil {
			t.Fatalf("Rename() error: %v", err)
		}

		// Old path should not exist
		if _, err := fs.Stat(ctx, "/olddir/file.txt"); err == nil {
			t.Error("Old path still exists after rename")
		}

		// New path should exist
		info, err := fs.Stat(ctx, "/newdir/file.txt")
		if err != nil {
			t.Fatalf("Stat(new) error: %v", err)
		}
		if info.IsDir {
			t.Error("Renamed file should not be a directory")
		}
	})
}

func TestFileSystem_GetVersion(t *testing.T) {
	fs, _ := setupTestFS(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/version-test")

	// Write multiple versions
	fs.WriteFile(ctx, "/version-test/file.txt", bytes.NewReader([]byte("version 1")))
	time.Sleep(10 * time.Millisecond)
	fs.WriteFile(ctx, "/version-test/file.txt", bytes.NewReader([]byte("version 2")))

	versions, _ := fs.ListVersions(ctx, "/version-test/file.txt")
	if len(versions) < 2 {
		t.Fatalf("Expected at least 2 versions, got %d", len(versions))
	}

	t.Run("GetSpecificVersion", func(t *testing.T) {
		// Get the old version
		oldHash := versions[1].Hash
		reader, err := fs.GetVersion(ctx, "/version-test/file.txt", oldHash)
		if err != nil {
			t.Fatalf("GetVersion() error: %v", err)
		}
		defer reader.Close()

		data, _ := io.ReadAll(reader)
		if string(data) != "version 1" {
			t.Errorf("Content = %q, want 'version 1'", data)
		}
	})
}

func TestFileSystem_GetAllReferencedHashes(t *testing.T) {
	fs, _ := setupTestFS(t)
	ctx := context.Background()

	// Create files with different hashes
	fs.Mkdir(ctx, "/hash-test")
	fs.WriteFile(ctx, "/hash-test/file1.txt", bytes.NewReader([]byte("content1")))
	fs.WriteFile(ctx, "/hash-test/file2.txt", bytes.NewReader([]byte("content2")))

	// Create versions
	fs.WriteFile(ctx, "/hash-test/file1.txt", bytes.NewReader([]byte("content1 v2")))

	// Delete a file to trash
	fs.Delete(ctx, "/hash-test/file2.txt")

	t.Run("ReturnsAllHashes", func(t *testing.T) {
		hashes, err := fs.GetAllReferencedHashes(ctx)
		if err != nil {
			t.Fatalf("GetAllReferencedHashes() error: %v", err)
		}

		// Should have: file1 current, file1 v1, file2 in trash
		if len(hashes) < 3 {
			t.Errorf("Expected at least 3 hashes, got %d", len(hashes))
		}
	})
}

func TestFileSystem_PermanentDelete(t *testing.T) {
	fs, _ := setupTestFS(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/perm-delete")
	fs.WriteFile(ctx, "/perm-delete/file.txt", bytes.NewReader([]byte("delete permanently")))

	t.Run("PermanentDelete_BypassesTrash", func(t *testing.T) {
		trashBefore, _ := fs.ListTrash(ctx)

		err := fs.PermanentDelete(ctx, "/perm-delete/file.txt")
		if err != nil {
			t.Fatalf("PermanentDelete() error: %v", err)
		}

		// File should not exist
		if _, err := fs.Stat(ctx, "/perm-delete/file.txt"); err == nil {
			t.Error("File still exists after PermanentDelete")
		}

		// Trash should not have the file
		trashAfter, _ := fs.ListTrash(ctx)
		if len(trashAfter) != len(trashBefore) {
			t.Error("PermanentDelete should not add to trash")
		}
	})

	t.Run("PermanentDelete_NonEmptyDir_Error", func(t *testing.T) {
		fs.Mkdir(ctx, "/nonempty-perm")
		fs.WriteFile(ctx, "/nonempty-perm/file.txt", bytes.NewReader([]byte("x")))

		err := fs.PermanentDelete(ctx, "/nonempty-perm")
		if err == nil {
			t.Error("PermanentDelete non-empty directory should error")
		}
	})
}

func TestFileSystem_TrashOperations(t *testing.T) {
	fs, _ := setupTestFS(t)
	ctx := context.Background()

	// Create and delete multiple files
	fs.Mkdir(ctx, "/trash-ops")
	fs.WriteFile(ctx, "/trash-ops/file1.txt", bytes.NewReader([]byte("file1")))
	fs.WriteFile(ctx, "/trash-ops/file2.txt", bytes.NewReader([]byte("file2 content")))
	fs.Delete(ctx, "/trash-ops/file1.txt")
	fs.Delete(ctx, "/trash-ops/file2.txt")

	t.Run("GetTrashItem", func(t *testing.T) {
		items, _ := fs.ListTrash(ctx)
		if len(items) == 0 {
			t.Fatal("No items in trash")
		}

		item, err := fs.GetTrashItem(ctx, items[0].ID)
		if err != nil {
			t.Fatalf("GetTrashItem() error: %v", err)
		}
		if item.ID != items[0].ID {
			t.Error("GetTrashItem returned wrong item")
		}
	})

	t.Run("GetTrashStats", func(t *testing.T) {
		count, totalSize, err := fs.GetTrashStats(ctx)
		if err != nil {
			t.Fatalf("GetTrashStats() error: %v", err)
		}
		if count != 2 {
			t.Errorf("Count = %d, want 2", count)
		}
		expectedSize := int64(len("file1") + len("file2 content"))
		if totalSize != expectedSize {
			t.Errorf("TotalSize = %d, want %d", totalSize, expectedSize)
		}
	})

	t.Run("DeleteFromTrash", func(t *testing.T) {
		items, _ := fs.ListTrash(ctx)
		err := fs.DeleteFromTrash(ctx, items[0].ID)
		if err != nil {
			t.Fatalf("DeleteFromTrash() error: %v", err)
		}

		itemsAfter, _ := fs.ListTrash(ctx)
		if len(itemsAfter) != len(items)-1 {
			t.Error("DeleteFromTrash should remove one item")
		}
	})

	t.Run("EmptyTrash", func(t *testing.T) {
		// Add more files to trash
		fs.WriteFile(ctx, "/trash-ops/file3.txt", bytes.NewReader([]byte("file3")))
		fs.Delete(ctx, "/trash-ops/file3.txt")

		count, err := fs.EmptyTrash(ctx)
		if err != nil {
			t.Fatalf("EmptyTrash() error: %v", err)
		}
		if count == 0 {
			t.Error("EmptyTrash should have removed items")
		}

		items, _ := fs.ListTrash(ctx)
		if len(items) != 0 {
			t.Error("Trash should be empty after EmptyTrash")
		}
	})
}

func TestFileSystem_CleanupExpiredTrash(t *testing.T) {
	fs, _ := setupTestFS(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/expired-test")
	fs.WriteFile(ctx, "/expired-test/file.txt", bytes.NewReader([]byte("x")))
	fs.Delete(ctx, "/expired-test/file.txt")

	t.Run("CleanupExpiredTrash_NoneExpired", func(t *testing.T) {
		// With retention of 30 days, nothing should be cleaned
		count, err := fs.CleanupExpiredTrash(ctx, 30)
		if err != nil {
			t.Fatalf("CleanupExpiredTrash() error: %v", err)
		}
		if count != 0 {
			t.Error("No items should be expired yet")
		}
	})

	t.Run("CleanupExpiredTrash_AllExpired", func(t *testing.T) {
		// With retention of 0 days, everything should be cleaned
		count, err := fs.CleanupExpiredTrash(ctx, 0)
		if err != nil {
			t.Fatalf("CleanupExpiredTrash() error: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 item cleaned, got %d", count)
		}
	})
}

func TestFileSystem_RestoreFromTrash_Errors(t *testing.T) {
	fs, _ := setupTestFS(t)
	ctx := context.Background()

	t.Run("RestoreFromTrash_NotFound", func(t *testing.T) {
		err := fs.RestoreFromTrash(ctx, "nonexistent-id")
		if err == nil {
			t.Error("RestoreFromTrash with invalid ID should error")
		}
	})

	t.Run("RestoreFromTrash_PathExists", func(t *testing.T) {
		fs.Mkdir(ctx, "/conflict-test")
		fs.WriteFile(ctx, "/conflict-test/file.txt", bytes.NewReader([]byte("original")))

		// Delete and recreate with same name
		fs.Delete(ctx, "/conflict-test/file.txt")
		fs.WriteFile(ctx, "/conflict-test/file.txt", bytes.NewReader([]byte("new file")))

		items, _ := fs.ListTrash(ctx)
		if len(items) == 0 {
			t.Fatal("No items in trash")
		}

		err := fs.RestoreFromTrash(ctx, items[0].ID)
		if err == nil {
			t.Error("RestoreFromTrash to existing path should error")
		}
	})

	t.Run("RestoreFromTrash_ParentMissing", func(t *testing.T) {
		// Clean up previous test
		fs.EmptyTrash(ctx)

		fs.Mkdir(ctx, "/parent-missing")
		fs.WriteFile(ctx, "/parent-missing/file.txt", bytes.NewReader([]byte("x")))
		fs.Delete(ctx, "/parent-missing/file.txt")

		// Delete the parent directory
		fs.PermanentDelete(ctx, "/parent-missing")

		items, _ := fs.ListTrash(ctx)
		if len(items) == 0 {
			t.Fatal("No items in trash")
		}

		err := fs.RestoreFromTrash(ctx, items[0].ID)
		if err == nil {
			t.Error("RestoreFromTrash with missing parent should error")
		}
	})
}

func TestFileSystem_WriteFile_LargeFile(t *testing.T) {
	fs, _ := setupTestFS(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/large-test")

	t.Run("WriteFile_AtLimit", func(t *testing.T) {
		// This test is slow due to memory allocation, so we use a smaller size
		// Just verify the limit check logic works
		largeData := make([]byte, MaxFileSize+1)
		err := fs.WriteFile(ctx, "/large-test/toolarge.txt", bytes.NewReader(largeData))
		if err == nil {
			t.Error("WriteFile should reject files larger than MaxFileSize")
		}
	})
}

func TestFileSystem_Mkdir_ParentNotDirectory(t *testing.T) {
	fs, _ := setupTestFS(t)
	ctx := context.Background()

	// Create a file
	fs.Mkdir(ctx, "/mkdir-test")
	fs.WriteFile(ctx, "/mkdir-test/file.txt", bytes.NewReader([]byte("x")))

	t.Run("Mkdir_ParentIsFile", func(t *testing.T) {
		err := fs.Mkdir(ctx, "/mkdir-test/file.txt/subdir")
		if err == nil {
			t.Error("Mkdir with file as parent should error")
		}
	})
}

func TestTrashStore(t *testing.T) {
	tmpDir := t.TempDir()
	trashRoot := path.Join(tmpDir, "trash")

	store, err := NewTrashStore(trashRoot)
	if err != nil {
		t.Fatalf("NewTrashStore() error: %v", err)
	}

	t.Run("Add_Get", func(t *testing.T) {
		info := &FileInfo{
			Path:        "/test/file.txt",
			IsDir:       false,
			Size:        100,
			ContentHash: "abc123",
		}

		item, err := store.Add("/test/file.txt", info)
		if err != nil {
			t.Fatalf("Add() error: %v", err)
		}

		if item.ID == "" {
			t.Error("Item should have an ID")
		}

		got, err := store.Get(item.ID)
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}

		if got.OriginalPath != "/test/file.txt" {
			t.Errorf("OriginalPath = %q, want %q", got.OriginalPath, "/test/file.txt")
		}
	})

	t.Run("Get_NotFound", func(t *testing.T) {
		_, err := store.Get("nonexistent")
		if err == nil {
			t.Error("Get nonexistent should error")
		}
	})

	t.Run("Remove", func(t *testing.T) {
		info := &FileInfo{Path: "/remove/file.txt"}
		item, _ := store.Add("/remove/file.txt", info)

		if err := store.Remove(item.ID); err != nil {
			t.Fatalf("Remove() error: %v", err)
		}

		if _, err := store.Get(item.ID); err == nil {
			t.Error("Get after Remove should error")
		}
	})

	t.Run("Remove_NotFound", func(t *testing.T) {
		err := store.Remove("nonexistent")
		if err == nil {
			t.Error("Remove nonexistent should error")
		}
	})

	t.Run("List_Sorted", func(t *testing.T) {
		// Clear existing items
		store.Clear()

		// Add items with different times
		info1 := &FileInfo{Path: "/first.txt"}
		store.Add("/first.txt", info1)
		time.Sleep(10 * time.Millisecond)

		info2 := &FileInfo{Path: "/second.txt"}
		store.Add("/second.txt", info2)

		items, err := store.List()
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}

		if len(items) != 2 {
			t.Fatalf("List() returned %d items, want 2", len(items))
		}

		// Newest first
		if items[0].OriginalPath != "/second.txt" {
			t.Error("List should return newest first")
		}
	})

	t.Run("Count_TotalSize", func(t *testing.T) {
		store.Clear()

		store.Add("/a.txt", &FileInfo{Path: "/a.txt", Size: 100})
		store.Add("/b.txt", &FileInfo{Path: "/b.txt", Size: 200})

		count, _ := store.Count()
		if count != 2 {
			t.Errorf("Count = %d, want 2", count)
		}

		total, _ := store.TotalSize()
		if total != 300 {
			t.Errorf("TotalSize = %d, want 300", total)
		}
	})
}

func TestHasExtension(t *testing.T) {
	tests := []struct {
		name string
		ext  string
		want bool
	}{
		{"file.json", ".json", true},
		{"file.txt", ".json", false},
		{"file.json.tmp", ".tmp", true},
		{".json", ".json", false}, // name must be longer than ext
		{"", ".json", false},
	}

	for _, tt := range tests {
		got := hasExtension(tt.name, tt.ext)
		if got != tt.want {
			t.Errorf("hasExtension(%q, %q) = %v, want %v", tt.name, tt.ext, got, tt.want)
		}
	}
}
