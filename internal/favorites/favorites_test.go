package favorites

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

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
