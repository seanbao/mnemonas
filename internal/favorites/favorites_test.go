package favorites

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWriteFavoritesStoreFile_ReturnsDirectorySyncError(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	originalSyncFavoritesStoreDir := syncFavoritesStoreDir
	syncFavoritesStoreDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncFavoritesStoreDir = originalSyncFavoritesStoreDir
	}()

	err := writeFavoritesStoreFile(storePath, []byte("[]"))
	if err == nil {
		t.Fatal("expected writeFavoritesStoreFile() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync favorites directory") {
		t.Fatalf("expected directory sync error, got %v", err)
	}

	data, readErr := os.ReadFile(storePath)
	if readErr != nil {
		t.Fatalf("expected favorites file to remain readable after sync failure, got %v", readErr)
	}
	if string(data) != "[]" {
		t.Fatalf("expected favorites content to be preserved, got %q", string(data))
	}
	info, statErr := os.Stat(storePath)
	if statErr != nil {
		t.Fatalf("expected favorites file to exist after sync failure, got %v", statErr)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected favorites file permissions 0600, got %o", info.Mode().Perm())
	}
}

func TestWriteFavoritesStoreFile_ReturnsDirectoryTreeSyncError(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "nested", "state", "favorites.json")

	originalSyncFavoritesStoreDir := syncFavoritesStoreDir
	syncFavoritesStoreDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncFavoritesStoreDir = originalSyncFavoritesStoreDir
	}()

	err := writeFavoritesStoreFile(storePath, []byte("[]"))
	if err == nil {
		t.Fatal("expected writeFavoritesStoreFile() to fail when directory tree sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync favorites directory tree") {
		t.Fatalf("expected directory tree sync error, got %v", err)
	}
	if _, statErr := os.Stat(storePath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no favorites file to be created, got %v", statErr)
	}
}

func TestNewStore_RecoversFromCorruptFavoritesFile(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")
	if err := os.WriteFile(storePath, []byte("{invalid json"), 0600); err != nil {
		t.Fatalf("WriteFile(favorites.json) error: %v", err)
	}

	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if count := store.Count("user1"); count != 0 {
		t.Fatalf("expected recovered store to start empty, got %d favorites", count)
	}

	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}
	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "favorites.json.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt favorites backup to be created")
	}

	if _, err := store.Add("user1", "/docs/file.txt", "restored"); err != nil {
		t.Fatalf("Add() after recovery error: %v", err)
	}

	reloaded, reloadErr := NewStore(storePath)
	if reloadErr != nil {
		t.Fatalf("NewStore() reload error: %v", reloadErr)
	}
	if count := reloaded.Count("user1"); count != 1 {
		t.Fatalf("expected recovered store to persist new favorites, got %d", count)
	}
}

func TestNewStore_LoadNormalizesAndDropsInvalidPaths(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	legacy := []Favorite{
		{Path: `docs\\report.pdf`, UserID: "user1", CreatedAt: time.Now(), Note: "normalized"},
		{Path: "../escape.txt", UserID: "user1", CreatedAt: time.Now(), Note: "invalid"},
		{Path: "   ", UserID: "user1", CreatedAt: time.Now(), Note: "blank"},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("Marshal(legacy favorites) error: %v", err)
	}
	if err := os.WriteFile(storePath, data, 0600); err != nil {
		t.Fatalf("WriteFile(favorites.json) error: %v", err)
	}

	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if !store.IsFavorite("user1", "/docs/report.pdf") {
		t.Fatal("expected legacy favorite path to be normalized on load")
	}
	if store.IsFavorite("user1", "/escape.txt") {
		t.Fatal("expected invalid traversal favorite to be dropped on load")
	}
	if count := store.Count("user1"); count != 1 {
		t.Fatalf("expected only normalized valid favorite to remain, got %d", count)
	}
	listed := store.List("user1")
	if len(listed) != 1 || listed[0].Path != "/docs/report.pdf" {
		t.Fatalf("expected normalized favorite path in list output, got %+v", listed)
	}

	data, err = os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile(favorites.json) error: %v", err)
	}
	var persisted []Favorite
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal(persisted favorites) error: %v", err)
	}
	if len(persisted) != 1 {
		t.Fatalf("expected normalized favorites file to contain one entry, got %d", len(persisted))
	}
	if persisted[0].Path != "/docs/report.pdf" {
		t.Fatalf("expected normalized favorite path to be persisted, got %q", persisted[0].Path)
	}
}

func TestNewStore_RejectsNullFavoriteEntry(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")
	if err := os.WriteFile(storePath, []byte("[null]"), 0600); err != nil {
		t.Fatalf("WriteFile(favorites.json) error: %v", err)
	}

	if _, err := NewStore(storePath); err == nil {
		t.Fatal("expected NewStore() to reject null favorite entries")
	} else if !strings.Contains(err.Error(), "null entry") {
		t.Fatalf("expected null entry error, got %v", err)
	}
}

func TestNewStore_ReturnsErrorWhenCorruptFavoritesBackupSyncFails(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")
	if err := os.WriteFile(storePath, []byte("{invalid json"), 0600); err != nil {
		t.Fatalf("WriteFile(favorites.json) error: %v", err)
	}

	originalSyncFavoritesStoreDir := syncFavoritesStoreDir
	syncFailed := false
	syncFavoritesStoreDir = func(dir string) error {
		if !syncFailed {
			syncFailed = true
			return errors.New("directory fsync failed")
		}
		return nil
	}
	defer func() {
		syncFavoritesStoreDir = originalSyncFavoritesStoreDir
	}()

	if _, err := NewStore(storePath); err == nil {
		t.Fatal("expected NewStore() to fail when corrupt favorites backup sync fails")
	} else if !strings.Contains(err.Error(), "sync corrupt favorites directory") {
		t.Fatalf("expected corrupt favorites sync failure in error, got %v", err)
	}

	if _, statErr := os.Stat(storePath); statErr != nil {
		t.Fatalf("expected original corrupt favorites file to remain after rollback, got %v", statErr)
	}
	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "favorites.json.corrupt.") {
			t.Fatalf("expected no corrupt backup after rollback, found %s", entry.Name())
		}
	}
}

func TestStore(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	// Create store
	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	userID := "user1"
	path1 := "/documents/file1.txt"
	path2 := "/photos/vacation.jpg"

	// Test Add
	fav1, err := store.Add(userID, path1, "important file")
	if err != nil {
		t.Fatalf("failed to add favorite: %v", err)
	}
	if fav1.Path != path1 {
		t.Errorf("expected path %s, got %s", path1, fav1.Path)
	}
	if fav1.Note != "important file" {
		t.Errorf("expected note 'important file', got %s", fav1.Note)
	}

	// Test duplicate Add
	_, err = store.Add(userID, path1, "")
	if err != ErrAlreadyFavorited {
		t.Errorf("expected ErrAlreadyFavorited, got %v", err)
	}

	// Add another favorite
	_, err = store.Add(userID, path2, "")
	if err != nil {
		t.Fatalf("failed to add second favorite: %v", err)
	}

	// Test List
	favorites := store.List(userID)
	if len(favorites) != 2 {
		t.Errorf("expected 2 favorites, got %d", len(favorites))
	}

	// Test IsFavorite
	if !store.IsFavorite(userID, path1) {
		t.Error("expected path1 to be favorite")
	}
	if store.IsFavorite(userID, "/nonexistent") {
		t.Error("expected /nonexistent to not be favorite")
	}

	// Test CheckPaths
	result := store.CheckPaths(userID, []string{path1, path2, "/nonexistent"})
	if !result[path1] {
		t.Error("expected path1 to be true")
	}
	if !result[path2] {
		t.Error("expected path2 to be true")
	}
	if result["/nonexistent"] {
		t.Error("expected /nonexistent to be false")
	}

	// Test Count
	if count := store.Count(userID); count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}

	// Test UpdateNote
	if err := store.UpdateNote(userID, path1, "updated note"); err != nil {
		t.Fatalf("failed to update note: %v", err)
	}
	favorites = store.List(userID)
	for _, f := range favorites {
		if f.Path == path1 && f.Note != "updated note" {
			t.Errorf("expected note 'updated note', got %s", f.Note)
		}
	}

	// Test Remove
	if err := store.Remove(userID, path1); err != nil {
		t.Fatalf("failed to remove favorite: %v", err)
	}
	if store.IsFavorite(userID, path1) {
		t.Error("expected path1 to not be favorite after remove")
	}

	// Test Remove nonexistent
	if err := store.Remove(userID, "/nonexistent"); err != ErrFavoriteNotFound {
		t.Errorf("expected ErrFavoriteNotFound, got %v", err)
	}

	// Test persistence - reload store
	store2, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to reload store: %v", err)
	}
	if !store2.IsFavorite(userID, path2) {
		t.Error("expected path2 to be favorite after reload")
	}
	if store2.IsFavorite(userID, path1) {
		t.Error("expected path1 to not be favorite after reload")
	}
}

func TestStore_NormalizesDirectPathInputs(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	fav, err := store.Add("user1", `docs\\report.pdf`, "note")
	if err != nil {
		t.Fatalf("Add() error: %v", err)
	}
	if fav.Path != "/docs/report.pdf" {
		t.Fatalf("expected normalized favorite path, got %q", fav.Path)
	}
	if !store.IsFavorite("user1", "docs/report.pdf") {
		t.Fatal("expected IsFavorite() to normalize direct relative path input")
	}

	if err := store.UpdateNote("user1", `docs\\report.pdf`, "updated"); err != nil {
		t.Fatalf("UpdateNote() error: %v", err)
	}
	listed := store.List("user1")
	if len(listed) != 1 || listed[0].Note != "updated" {
		t.Fatalf("expected normalized UpdateNote() to update stored favorite, got %+v", listed)
	}

	if err := store.Remove("user1", `docs\\report.pdf`); err != nil {
		t.Fatalf("Remove() error: %v", err)
	}
	if store.IsFavorite("user1", "/docs/report.pdf") {
		t.Fatal("expected normalized Remove() to delete the stored favorite")
	}
}

func TestStore_AddRejectsTraversalLikePath(t *testing.T) {
	testCases := []string{
		"../escape.txt",
		`..\\escape.txt`,
		"   ",
	}

	for _, rawPath := range testCases {
		t.Run(rawPath, func(t *testing.T) {
			tmpDir := t.TempDir()
			storePath := filepath.Join(tmpDir, "favorites.json")

			store, err := NewStore(storePath)
			if err != nil {
				t.Fatalf("failed to create store: %v", err)
			}

			fav, err := store.Add("user1", rawPath, "note")
			if !errors.Is(err, errInvalidFavoritePath) {
				t.Fatalf("Add() error = %v, want %v", err, errInvalidFavoritePath)
			}
			if fav != nil {
				t.Fatalf("expected failed add to return nil favorite, got %+v", fav)
			}
			if got := store.Count("user1"); got != 0 {
				t.Fatalf("expected no persisted favorites after failed add, got %d", got)
			}
			if store.IsFavorite("user1", "/escape.txt") {
				t.Fatal("expected traversal-like add not to create a normalized alias favorite")
			}
		})
	}
}

func TestStoreMultipleUsers(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	user1 := "user1"
	user2 := "user2"
	path := "/shared/file.txt"

	// Both users can favorite the same path
	_, err = store.Add(user1, path, "user1 note")
	if err != nil {
		t.Fatalf("user1 failed to add favorite: %v", err)
	}

	_, err = store.Add(user2, path, "user2 note")
	if err != nil {
		t.Fatalf("user2 failed to add favorite: %v", err)
	}

	// Each user sees their own favorites
	if store.Count(user1) != 1 {
		t.Error("expected user1 to have 1 favorite")
	}
	if store.Count(user2) != 1 {
		t.Error("expected user2 to have 1 favorite")
	}

	// User1 removing doesn't affect user2
	if err := store.Remove(user1, path); err != nil {
		t.Fatalf("user1 failed to remove: %v", err)
	}
	if store.IsFavorite(user1, path) {
		t.Error("expected user1 to not have favorite after remove")
	}
	if !store.IsFavorite(user2, path) {
		t.Error("expected user2 to still have favorite")
	}
}

func TestStoreEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	// Write empty JSON array
	if err := os.WriteFile(storePath, []byte("[]"), 0644); err != nil {
		t.Fatalf("failed to write empty file: %v", err)
	}

	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store with empty file: %v", err)
	}

	if store.Count("anyuser") != 0 {
		t.Error("expected 0 favorites for new user")
	}
}

func TestStore_RejectsSymlinkPathOnLoad(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "real-favorites.json")
	symlinkPath := filepath.Join(tmpDir, "favorites.json")

	if err := os.WriteFile(targetPath, []byte("[]"), 0600); err != nil {
		t.Fatalf("failed to write target store: %v", err)
	}
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	_, err := NewStore(symlinkPath)
	if !errors.Is(err, errFavoritesStoreSymlink) {
		t.Fatalf("expected symlink error, got %v", err)
	}
}

func TestStore_RejectsSymlinkParentDirectoryOnLoad(t *testing.T) {
	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "real-favorites")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatalf("failed to create real favorites dir: %v", err)
	}
	targetPath := filepath.Join(realDir, "favorites.json")
	if err := os.WriteFile(targetPath, []byte("[]"), 0600); err != nil {
		t.Fatalf("failed to seed favorites store: %v", err)
	}
	linkedDir := filepath.Join(tmpDir, "linked-favorites")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Fatalf("failed to create favorites dir symlink: %v", err)
	}

	_, err := NewStore(filepath.Join(linkedDir, "favorites.json"))
	if !errors.Is(err, errFavoritesStoreSymlink) {
		t.Fatalf("expected parent-directory symlink error, got %v", err)
	}
}

func TestStore_ReturnedFavoritesAreDetachedCopies(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	if _, err := store.Add("user1", "/docs/file.txt", "original note"); err != nil {
		t.Fatalf("failed to add favorite: %v", err)
	}

	listed := store.List("user1")
	if len(listed) != 1 {
		t.Fatalf("expected 1 favorite, got %d", len(listed))
	}
	listed[0].Note = "mutated note"

	fresh := store.List("user1")
	if fresh[0].Note != "original note" {
		t.Fatalf("expected stored note to remain original, got %q", fresh[0].Note)
	}
}

func TestStore_RollsBackFailedMutations(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	if _, err := store.Add("user1", "/docs/file.txt", "original note"); err != nil {
		t.Fatalf("failed to add favorite: %v", err)
	}

	store.filePath = tmpDir

	if err := store.UpdateNote("user1", "/docs/file.txt", "updated note"); err == nil {
		t.Fatal("expected update note save failure")
	}
	listed := store.List("user1")
	if len(listed) != 1 {
		t.Fatalf("expected 1 favorite after failed note update, got %d", len(listed))
	}
	if listed[0].Note != "original note" {
		t.Fatalf("expected note rollback to keep original note, got %q", listed[0].Note)
	}

	if err := store.Remove("user1", "/docs/file.txt"); err == nil {
		t.Fatal("expected remove save failure")
	}
	if !store.IsFavorite("user1", "/docs/file.txt") {
		t.Fatal("expected failed remove to keep favorite in store")
	}
}

func TestStore_AddRejectsSymlinkPath(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	targetPath := filepath.Join(tmpDir, "real-favorites.json")
	if err := os.WriteFile(targetPath, []byte("[]"), 0600); err != nil {
		t.Fatalf("failed to write target store: %v", err)
	}
	symlinkPath := filepath.Join(tmpDir, "favorites-link.json")
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}
	store.filePath = symlinkPath

	fav, err := store.Add("user1", "/docs/file.txt", "note")
	if !errors.Is(err, errFavoritesStoreSymlink) {
		t.Fatalf("expected symlink error, got %v", err)
	}
	if fav != nil {
		t.Fatal("expected failed add to return nil favorite")
	}
	if store.Count("user1") != 0 {
		t.Fatalf("expected failed add to roll back store state, got %d favorites", store.Count("user1"))
	}
}

func TestStore_UpdatePathReferences_RenamesDescendantFavorites(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	if _, err := store.Add("user1", "/docs", "folder"); err != nil {
		t.Fatalf("Add(/docs) error: %v", err)
	}
	if _, err := store.Add("user1", "/docs/a.txt", "file"); err != nil {
		t.Fatalf("Add(/docs/a.txt) error: %v", err)
	}
	if _, err := store.Add("user1", "/other.txt", "other"); err != nil {
		t.Fatalf("Add(/other.txt) error: %v", err)
	}
	if _, err := store.Add("user2", "/docs/sub/b.txt", "nested"); err != nil {
		t.Fatalf("Add(/docs/sub/b.txt) error: %v", err)
	}

	if err := store.UpdatePathReferences("/docs", "/archive/docs"); err != nil {
		t.Fatalf("UpdatePathReferences() error: %v", err)
	}

	if store.IsFavorite("user1", "/docs") {
		t.Fatal("expected original folder favorite path to be removed")
	}
	if !store.IsFavorite("user1", "/archive/docs") {
		t.Fatal("expected folder favorite path to be updated")
	}
	if store.IsFavorite("user1", "/docs/a.txt") {
		t.Fatal("expected original file favorite path to be removed")
	}
	if !store.IsFavorite("user1", "/archive/docs/a.txt") {
		t.Fatal("expected file favorite path to be updated")
	}
	if !store.IsFavorite("user1", "/other.txt") {
		t.Fatal("expected unrelated favorite to remain")
	}
	if !store.IsFavorite("user2", "/archive/docs/sub/b.txt") {
		t.Fatal("expected descendant favorite for second user to be updated")
	}
}

func TestStore_UpdatePathReferences_PreservesAllDescendantsForSingleUser(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	paths := []string{
		"/docs",
		"/docs/a.txt",
		"/docs/b.txt",
		"/docs/sub/c.txt",
		"/docs/sub/deeper/d.txt",
	}
	for _, favoritePath := range paths {
		if _, err := store.Add("user1", favoritePath, favoritePath); err != nil {
			t.Fatalf("Add(%s) error: %v", favoritePath, err)
		}
	}

	if err := store.UpdatePathReferences("/docs", "/archive/docs"); err != nil {
		t.Fatalf("UpdatePathReferences() error: %v", err)
	}

	if store.Count("user1") != len(paths) {
		t.Fatalf("expected %d favorites after rename, got %d", len(paths), store.Count("user1"))
	}

	expected := []string{
		"/archive/docs",
		"/archive/docs/a.txt",
		"/archive/docs/b.txt",
		"/archive/docs/sub/c.txt",
		"/archive/docs/sub/deeper/d.txt",
	}
	for _, favoritePath := range expected {
		if !store.IsFavorite("user1", favoritePath) {
			t.Fatalf("expected favorite path %s to be updated", favoritePath)
		}
	}
	for _, oldPath := range paths {
		if store.IsFavorite("user1", oldPath) {
			t.Fatalf("expected old favorite path %s to be removed", oldPath)
		}
	}

	reloaded, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(reload) error: %v", err)
	}
	for _, favoritePath := range expected {
		if !reloaded.IsFavorite("user1", favoritePath) {
			t.Fatalf("expected reloaded favorite path %s to be updated", favoritePath)
		}
	}
}

func TestStore_RemoveFavoritesUnderPath_RemovesExactAndDescendantFavorites(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	if _, err := store.Add("user1", "/docs", "folder"); err != nil {
		t.Fatalf("Add(/docs) error: %v", err)
	}
	if _, err := store.Add("user1", "/docs/a.txt", "file"); err != nil {
		t.Fatalf("Add(/docs/a.txt) error: %v", err)
	}
	if _, err := store.Add("user1", "/other.txt", "other"); err != nil {
		t.Fatalf("Add(/other.txt) error: %v", err)
	}
	if _, err := store.Add("user2", "/docs/sub/b.txt", "nested"); err != nil {
		t.Fatalf("Add(/docs/sub/b.txt) error: %v", err)
	}

	if err := store.RemoveFavoritesUnderPath("/docs"); err != nil {
		t.Fatalf("RemoveFavoritesUnderPath() error: %v", err)
	}

	if store.IsFavorite("user1", "/docs") {
		t.Fatal("expected exact deleted-path favorite to be removed")
	}
	if store.IsFavorite("user1", "/docs/a.txt") {
		t.Fatal("expected descendant deleted-path favorite to be removed")
	}
	if !store.IsFavorite("user1", "/other.txt") {
		t.Fatal("expected unrelated favorite to remain")
	}
	if store.IsFavorite("user2", "/docs/sub/b.txt") {
		t.Fatal("expected second user descendant favorite to be removed")
	}
}

func TestStore_RemoveFavoritesUnderPathWithRestore_RestoresRemovedFavorites(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	if _, err := store.Add("user1", "/docs", "folder"); err != nil {
		t.Fatalf("Add(/docs) error: %v", err)
	}
	if _, err := store.Add("user1", "/docs/a.txt", "file"); err != nil {
		t.Fatalf("Add(/docs/a.txt) error: %v", err)
	}

	removed, err := store.RemoveFavoritesUnderPathWithRestore("/docs")
	if err != nil {
		t.Fatalf("RemoveFavoritesUnderPathWithRestore() error: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("expected two removed favorites in rollback state, got %d", len(removed))
	}

	if err := store.RestoreFavorites(removed); err != nil {
		t.Fatalf("RestoreFavorites() error: %v", err)
	}
	if !store.IsFavorite("user1", "/docs") {
		t.Fatal("expected exact favorite to be restored")
	}
	if !store.IsFavorite("user1", "/docs/a.txt") {
		t.Fatal("expected descendant favorite to be restored")
	}

	reloaded, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(reload) error: %v", err)
	}
	if !reloaded.IsFavorite("user1", "/docs") || !reloaded.IsFavorite("user1", "/docs/a.txt") {
		t.Fatal("expected restored favorites to persist after reload")
	}
}

func TestStore_RestoreFavoritesIfMissing_PreservesRecreatedFavorite(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	if _, err := store.Add("user1", "/docs/a.txt", "original"); err != nil {
		t.Fatalf("Add(original) error: %v", err)
	}

	removed, err := store.RemoveFavoritesUnderPathWithRestore("/docs/a.txt")
	if err != nil {
		t.Fatalf("RemoveFavoritesUnderPathWithRestore() error: %v", err)
	}
	if len(removed) != 1 {
		t.Fatalf("expected one removed favorite in rollback state, got %d", len(removed))
	}

	if _, err := store.Add("user1", "/docs/a.txt", "newer"); err != nil {
		t.Fatalf("Add(newer) error: %v", err)
	}

	if err := store.RestoreFavoritesIfMissing(removed); err != nil {
		t.Fatalf("RestoreFavoritesIfMissing() error: %v", err)
	}

	loaded := store.List("user1")
	if len(loaded) != 1 {
		t.Fatalf("expected one favorite after rollback restore, got %d", len(loaded))
	}
	if loaded[0].Note != "newer" {
		t.Fatalf("expected newer favorite note to be preserved, got %q", loaded[0].Note)
	}

	reloaded, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore(reload) error: %v", err)
	}
	reloadedFavorites := reloaded.List("user1")
	if len(reloadedFavorites) != 1 {
		t.Fatalf("expected one persisted favorite after reload, got %d", len(reloadedFavorites))
	}
	if reloadedFavorites[0].Note != "newer" {
		t.Fatalf("expected persisted newer favorite note to be preserved, got %q", reloadedFavorites[0].Note)
	}
}

func TestStore_ListDoesNotBlockWhileAddPersists(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	originalWriter := favoritesStoreWriter
	writerStarted := make(chan struct{})
	writerRelease := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	favoritesStoreWriter = func(path string, data []byte) error {
		startOnce.Do(func() {
			close(writerStarted)
		})
		<-writerRelease
		return originalWriter(path, data)
	}
	t.Cleanup(func() {
		favoritesStoreWriter = originalWriter
		releaseOnce.Do(func() {
			close(writerRelease)
		})
	})

	addDone := make(chan struct {
		fav *Favorite
		err error
	}, 1)
	go func() {
		fav, addErr := store.Add("user1", "/docs/slow.txt", "slow")
		addDone <- struct {
			fav *Favorite
			err error
		}{fav: fav, err: addErr}
	}()

	select {
	case <-writerStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for favorites store write to start")
	}

	listDone := make(chan struct{})
	go func() {
		listed := store.List("user1")
		if len(listed) != 0 {
			t.Errorf("expected reads during pending persist to observe committed empty favorites, got %d", len(listed))
		}
		close(listDone)
	}()

	select {
	case <-listDone:
	case <-time.After(time.Second):
		t.Fatal("List() blocked on an in-flight favorites save")
	}

	releaseOnce.Do(func() {
		close(writerRelease)
	})

	select {
	case result := <-addDone:
		if result.err != nil {
			t.Fatalf("Add() error: %v", result.err)
		}
		if result.fav == nil || result.fav.Path != "/docs/slow.txt" {
			t.Fatalf("expected added favorite for /docs/slow.txt, got %+v", result.fav)
		}
	case <-time.After(time.Second):
		t.Fatal("Add() did not finish after releasing writer")
	}

	listed := store.List("user1")
	if len(listed) != 1 {
		t.Fatalf("expected 1 favorite after save, got %d", len(listed))
	}
	if listed[0].Path != "/docs/slow.txt" {
		t.Fatalf("expected /docs/slow.txt after save, got %s", listed[0].Path)
	}
}

func TestStore_ConcurrentWritesSerializePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "favorites.json")

	store, err := NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	originalWriter := favoritesStoreWriter
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	secondStarted := make(chan struct{})
	var startFirstOnce sync.Once
	var releaseFirstOnce sync.Once
	var startSecondOnce sync.Once
	var callCount int32
	favoritesStoreWriter = func(path string, data []byte) error {
		call := atomic.AddInt32(&callCount, 1)
		switch call {
		case 1:
			startFirstOnce.Do(func() {
				close(firstStarted)
			})
			<-firstRelease
		case 2:
			startSecondOnce.Do(func() {
				close(secondStarted)
			})
		}
		return originalWriter(path, data)
	}
	t.Cleanup(func() {
		favoritesStoreWriter = originalWriter
		startFirstOnce.Do(func() {
			close(firstStarted)
		})
		startSecondOnce.Do(func() {
			close(secondStarted)
		})
		releaseFirstOnce.Do(func() {
			close(firstRelease)
		})
	})

	firstDone := make(chan error, 1)
	go func() {
		_, addErr := store.Add("user1", "/docs/first.txt", "first")
		firstDone <- addErr
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first favorites persist to start")
	}

	secondDone := make(chan error, 1)
	go func() {
		_, addErr := store.Add("user1", "/docs/second.txt", "second")
		secondDone <- addErr
	}()

	select {
	case <-secondStarted:
		t.Fatal("second favorites persist started before first persist completed")
	case <-time.After(100 * time.Millisecond):
	}

	releaseFirstOnce.Do(func() {
		close(firstRelease)
	})

	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first Add() error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first Add() did not finish after releasing writer")
	}

	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second favorites persist to start")
	}

	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second Add() error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Add() did not finish")
	}

	listed := store.List("user1")
	if len(listed) != 2 {
		t.Fatalf("expected 2 favorites after serialized persists, got %d", len(listed))
	}
}
