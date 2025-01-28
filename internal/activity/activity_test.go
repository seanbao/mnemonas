package activity

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewStore(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if store == nil {
		t.Fatal("NewStore() returned nil")
	}

	if store.Count() != 0 {
		t.Errorf("Expected 0 entries, got %d", store.Count())
	}
}

func TestLogAndList(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	// Log entries
	err := store.Log(ActionUpload, "/file1.txt", "user1", "192.168.1.1", nil)
	if err != nil {
		t.Fatalf("Log() error: %v", err)
	}

	err = store.Log(ActionDelete, "/file2.txt", "user2", "192.168.1.2", map[string]string{"reason": "cleanup"})
	if err != nil {
		t.Fatalf("Log() error: %v", err)
	}

	// List entries
	entries, total := store.List(10, 0, "", "")
	if total != 2 {
		t.Errorf("Expected total 2, got %d", total)
	}
	if len(entries) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(entries))
	}

	// First entry should be most recent (delete)
	if entries[0].Action != ActionDelete {
		t.Errorf("Expected first entry to be delete, got %s", entries[0].Action)
	}
	if entries[0].User != "user2" {
		t.Errorf("Expected user2, got %s", entries[0].User)
	}
}

func TestLogRollsBackWhenSaveFails(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	if err := store.Log(ActionUpload, "/original.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("Initial Log() error: %v", err)
	}

	originalRoot := store.root
	store.root = filepath.Join(tmpDir, "missing", "nested")

	err := store.Log(ActionDelete, "/should-not-persist.txt", "user", "127.0.0.1", nil)
	if err == nil {
		t.Fatal("Expected Log() to fail when save path is invalid")
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("Expected original entries to remain after failed log, got total=%d len=%d", total, len(entries))
	}
	if entries[0].Path != "/original.txt" {
		t.Fatalf("Expected original log entry to remain after rollback, got %s", entries[0].Path)
	}

	store.root = originalRoot
	reloaded, reloadErr := NewStore(tmpDir)
	if reloadErr != nil {
		t.Fatalf("NewStore() reload error: %v", reloadErr)
	}
	reloadedEntries, reloadedTotal := reloaded.List(10, 0, "", "")
	if reloadedTotal != 1 || len(reloadedEntries) != 1 {
		t.Fatalf("Expected persisted entries to remain unchanged after failed log, got total=%d len=%d", reloadedTotal, len(reloadedEntries))
	}
	if reloadedEntries[0].Path != "/original.txt" {
		t.Fatalf("Expected persisted original entry after failed log, got %s", reloadedEntries[0].Path)
	}
}

func TestListWithFilters(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	// Log various entries
	store.Log(ActionUpload, "/file1.txt", "admin", "192.168.1.1", nil)
	store.Log(ActionUpload, "/file2.txt", "user1", "192.168.1.2", nil)
	store.Log(ActionDelete, "/file3.txt", "admin", "192.168.1.1", nil)
	store.Log(ActionLogin, "", "user1", "192.168.1.2", nil)

	// Filter by action
	entries, total := store.List(10, 0, ActionUpload, "")
	if total != 2 {
		t.Errorf("Expected 2 upload entries, got %d", total)
	}

	// Filter by user
	entries, total = store.List(10, 0, "", "admin")
	if total != 2 {
		t.Errorf("Expected 2 admin entries, got %d", total)
	}

	// Filter by both
	entries, total = store.List(10, 0, ActionUpload, "admin")
	if total != 1 {
		t.Errorf("Expected 1 admin upload entry, got %d", total)
	}
	if entries[0].Path != "/file1.txt" {
		t.Errorf("Expected /file1.txt, got %s", entries[0].Path)
	}
}

func TestListPagination(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	// Log 5 entries
	for i := 0; i < 5; i++ {
		store.Log(ActionUpload, "/file"+string(rune('0'+i))+".txt", "user", "127.0.0.1", nil)
	}

	// Get page 1 (limit 2)
	entries, total := store.List(2, 0, "", "")
	if total != 5 {
		t.Errorf("Expected total 5, got %d", total)
	}
	if len(entries) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(entries))
	}

	// Get page 2
	entries, _ = store.List(2, 2, "", "")
	if len(entries) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(entries))
	}

	// Get page 3
	entries, _ = store.List(2, 4, "", "")
	if len(entries) != 1 {
		t.Errorf("Expected 1 entry, got %d", len(entries))
	}
}

func TestClear(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	store.Log(ActionUpload, "/test.txt", "user", "127.0.0.1", nil)
	if store.Count() != 1 {
		t.Errorf("Expected 1 entry, got %d", store.Count())
	}

	err := store.Clear()
	if err != nil {
		t.Fatalf("Clear() error: %v", err)
	}

	if store.Count() != 0 {
		t.Errorf("Expected 0 entries after clear, got %d", store.Count())
	}
}

func TestClearRollsBackWhenSaveFails(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	if err := store.Log(ActionUpload, "/test.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log() error: %v", err)
	}

	originalRoot := store.root
	store.root = filepath.Join(tmpDir, "missing", "nested")

	err := store.Clear()
	if err == nil {
		t.Fatal("Expected Clear() to fail when save path is invalid")
	}

	if store.Count() != 1 {
		t.Fatalf("Expected in-memory entries to be restored after clear failure, got %d", store.Count())
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("Expected 1 entry after rollback, got total=%d len=%d", total, len(entries))
	}
	if entries[0].Path != "/test.txt" {
		t.Fatalf("Expected rolled back entry path /test.txt, got %s", entries[0].Path)
	}

	store.root = originalRoot
	reloaded, reloadErr := NewStore(tmpDir)
	if reloadErr != nil {
		t.Fatalf("NewStore() reload error: %v", reloadErr)
	}
	if reloaded.Count() != 1 {
		t.Fatalf("Expected persisted activity log to remain unchanged after failed clear, got %d", reloaded.Count())
	}
}

func TestGetByID(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	store.Log(ActionUpload, "/test.txt", "user", "127.0.0.1", nil)

	entries, _ := store.List(1, 0, "", "")
	if len(entries) != 1 {
		t.Fatal("Expected 1 entry")
	}

	entry, err := store.GetByID(entries[0].ID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if entry.Path != "/test.txt" {
		t.Errorf("Expected /test.txt, got %s", entry.Path)
	}

	// Test not found
	_, err = store.GetByID("nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent ID")
	}
}

func TestStatistics(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	store.Log(ActionUpload, "/file1.txt", "admin", "127.0.0.1", nil)
	store.Log(ActionUpload, "/file2.txt", "user1", "127.0.0.1", nil)
	store.Log(ActionDelete, "/file3.txt", "admin", "127.0.0.1", nil)

	stats := store.Statistics()

	total, ok := stats["total"].(int)
	if !ok || total != 3 {
		t.Errorf("Expected total 3, got %v", stats["total"])
	}

	today, ok := stats["today"].(int)
	if !ok || today != 3 {
		t.Errorf("Expected today 3, got %v", stats["today"])
	}

	byAction, ok := stats["by_action"].(map[ActionType]int)
	if !ok {
		t.Fatal("by_action type assertion failed")
	}
	if byAction[ActionUpload] != 2 {
		t.Errorf("Expected 2 uploads, got %d", byAction[ActionUpload])
	}
	if byAction[ActionDelete] != 1 {
		t.Errorf("Expected 1 delete, got %d", byAction[ActionDelete])
	}

	byUser, ok := stats["by_user"].(map[string]int)
	if !ok {
		t.Fatal("by_user type assertion failed")
	}
	if byUser["admin"] != 2 {
		t.Errorf("Expected 2 for admin, got %d", byUser["admin"])
	}
}

func TestPersistence(t *testing.T) {
	tmpDir := t.TempDir()

	// Create store and log entry
	store1, _ := NewStore(tmpDir)
	store1.Log(ActionUpload, "/persistent.txt", "user", "127.0.0.1", nil)

	// Create new store instance - should load existing data
	store2, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if store2.Count() != 1 {
		t.Errorf("Expected 1 entry after reload, got %d", store2.Count())
	}

	entries, _ := store2.List(1, 0, "", "")
	if entries[0].Path != "/persistent.txt" {
		t.Errorf("Expected /persistent.txt, got %s", entries[0].Path)
	}
}

func TestMaxSize(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)
	store.maxSize = 5 // Set small max size for test

	// Log more entries than max size
	for i := 0; i < 10; i++ {
		store.Log(ActionUpload, "/file"+string(rune('0'+i))+".txt", "user", "127.0.0.1", nil)
		time.Sleep(time.Millisecond) // Ensure unique timestamps
	}

	if store.Count() != 5 {
		t.Errorf("Expected 5 entries (max size), got %d", store.Count())
	}

	// Verify most recent entries are kept
	entries, _ := store.List(10, 0, "", "")
	// Most recent entry should be file9.txt
	if entries[0].Path != "/file9.txt" {
		t.Errorf("Expected most recent /file9.txt, got %s", entries[0].Path)
	}
}

func TestLogFilePath(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	expectedPath := filepath.Join(tmpDir, "activity.json")
	if store.logFilePath() != expectedPath {
		t.Errorf("Expected %s, got %s", expectedPath, store.logFilePath())
	}
}

func TestEmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	activityDir := filepath.Join(tmpDir, "activity")

	// Ensure directory doesn't exist before test
	os.RemoveAll(activityDir)

	store, err := NewStore(activityDir)
	if err != nil {
		t.Fatalf("NewStore() should create directory: %v", err)
	}

	// Directory should be created
	if _, err := os.Stat(activityDir); os.IsNotExist(err) {
		t.Error("Expected activity directory to be created")
	}

	if store.Count() != 0 {
		t.Errorf("Expected 0 entries in new store, got %d", store.Count())
	}
}
