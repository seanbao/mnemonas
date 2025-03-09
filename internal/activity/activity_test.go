package activity

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWriteActivityLogFile_ReturnsDirectorySyncError(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "activity.json")

	originalSyncActivityLogDir := syncActivityLogDir
	syncActivityLogDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncActivityLogDir = originalSyncActivityLogDir
	}()

	err := writeActivityLogFile(logPath, []byte("[]"))
	if err == nil {
		t.Fatal("expected writeActivityLogFile() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync activity log directory") {
		t.Fatalf("expected directory sync error, got %v", err)
	}

	data, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("expected activity log to remain readable after sync failure, got %v", readErr)
	}
	if string(data) != "[]" {
		t.Fatalf("expected activity log content to be preserved, got %q", string(data))
	}
	info, statErr := os.Stat(logPath)
	if statErr != nil {
		t.Fatalf("expected activity log file to exist after sync failure, got %v", statErr)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("expected activity log permissions 0640, got %o", info.Mode().Perm())
	}
}

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

func TestNewStore_ReturnsErrorWhenActivityDirectorySyncFails(t *testing.T) {
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, "nested", "activity")

	originalSyncActivityLogDir := syncActivityLogDir
	syncActivityLogDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncActivityLogDir = originalSyncActivityLogDir
	}()

	if _, err := NewStore(root); err == nil {
		t.Fatal("expected NewStore() to fail when activity directory tree sync fails")
	} else if !strings.Contains(err.Error(), "failed to sync activity directory tree") {
		t.Fatalf("expected activity directory tree sync failure, got %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(root, "activity.json")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no activity log file to be created, got %v", statErr)
	}
}

func TestNewStore_RecoversFromCorruptLogFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "activity.json")
	if err := os.WriteFile(logPath, []byte("{invalid json"), 0640); err != nil {
		t.Fatalf("WriteFile(activity.json) error: %v", err)
	}

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if store.Count() != 0 {
		t.Fatalf("Expected recovered store to start empty, got %d entries", store.Count())
	}

	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}

	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "activity.json.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt activity log backup to be created")
	}

	if err := store.Log(ActionUpload, "/recovered.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log() after recovery error: %v", err)
	}

	reloaded, reloadErr := NewStore(tmpDir)
	if reloadErr != nil {
		t.Fatalf("NewStore() reload error: %v", reloadErr)
	}
	if reloaded.Count() != 1 {
		t.Fatalf("Expected recovered store to persist new entries, got %d", reloaded.Count())
	}
}

func TestNewStore_ReturnsErrorWhenCorruptLogBackupDirectorySyncFails(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "activity.json")
	if err := os.WriteFile(logPath, []byte("{invalid json"), 0640); err != nil {
		t.Fatalf("WriteFile(activity.json) error: %v", err)
	}

	originalSyncActivityLogDir := syncActivityLogDir
	syncFailed := false
	syncActivityLogDir = func(dir string) error {
		if !syncFailed {
			syncFailed = true
			return errors.New("sync dir failed")
		}
		return nil
	}
	t.Cleanup(func() {
		syncActivityLogDir = originalSyncActivityLogDir
	})

	if _, err := NewStore(tmpDir); err == nil {
		t.Fatal("expected NewStore() to fail when corrupt backup directory sync fails")
	} else if !strings.Contains(err.Error(), "sync corrupt activity log directory") {
		t.Fatalf("expected directory sync failure in error, got %v", err)
	}

	if _, statErr := os.Stat(logPath); statErr != nil {
		t.Fatalf("expected original corrupt log to remain after rollback, got %v", statErr)
	}

	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "activity.json.corrupt.") {
			t.Fatalf("expected no corrupt backup after rollback, found %s", entry.Name())
		}
	}
}

func TestNewStore_RejectsSymlinkLogFile(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "real-activity.json")
	logPath := filepath.Join(tmpDir, "activity.json")

	if err := os.WriteFile(targetPath, []byte("[]"), 0640); err != nil {
		t.Fatalf("WriteFile(real-activity.json) error: %v", err)
	}
	if err := os.Symlink(targetPath, logPath); err != nil {
		t.Fatalf("Symlink(activity.json) error: %v", err)
	}

	_, err := NewStore(tmpDir)
	if !errors.Is(err, errActivityLogSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestNewStore_RejectsSymlinkParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	realRoot := filepath.Join(tmpDir, "real-activity-root")
	if err := os.MkdirAll(realRoot, 0755); err != nil {
		t.Fatalf("MkdirAll(real-root) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realRoot, "activity.json"), []byte("[]"), 0640); err != nil {
		t.Fatalf("WriteFile(activity.json) error: %v", err)
	}
	linkedRoot := filepath.Join(tmpDir, "linked-activity-root")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatalf("Symlink(linked-root) error: %v", err)
	}

	_, err := NewStore(linkedRoot)
	if !errors.Is(err, errActivityLogSymlink) {
		t.Fatalf("expected parent-directory symlink rejection, got %v", err)
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
	blockedPath := filepath.Join(tmpDir, "blocked-root")
	if err := os.WriteFile(blockedPath, []byte("blocked"), 0644); err != nil {
		t.Fatalf("WriteFile(blocked-root) error: %v", err)
	}
	store.root = filepath.Join(blockedPath, "nested")

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

func TestLogRollsBackWhenLogPathIsSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if err := store.Log(ActionUpload, "/original.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("Initial Log() error: %v", err)
	}

	symlinkRoot := filepath.Join(tmpDir, "symlink-root")
	if err := os.MkdirAll(symlinkRoot, 0755); err != nil {
		t.Fatalf("MkdirAll(symlink-root) error: %v", err)
	}
	targetPath := filepath.Join(tmpDir, "real-activity.json")
	if err := os.WriteFile(targetPath, []byte("[]"), 0640); err != nil {
		t.Fatalf("WriteFile(real-activity.json) error: %v", err)
	}
	if err := os.Symlink(targetPath, filepath.Join(symlinkRoot, "activity.json")); err != nil {
		t.Fatalf("Symlink(activity.json) error: %v", err)
	}

	originalRoot := store.root
	store.root = symlinkRoot

	err = store.Log(ActionDelete, "/should-not-persist.txt", "user", "127.0.0.1", nil)
	if !errors.Is(err, errActivityLogSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("expected original entries after rollback, got total=%d len=%d", total, len(entries))
	}
	if entries[0].Path != "/original.txt" {
		t.Fatalf("expected original entry after rollback, got %s", entries[0].Path)
	}

	store.root = originalRoot
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
	blockedPath := filepath.Join(tmpDir, "blocked-root")
	if err := os.WriteFile(blockedPath, []byte("blocked"), 0644); err != nil {
		t.Fatalf("WriteFile(blocked-root) error: %v", err)
	}
	store.root = filepath.Join(blockedPath, "nested")

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

func TestListDoesNotBlockWhileLogPersists(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	originalWriter := activityLogWriter
	writerStarted := make(chan struct{})
	writerRelease := make(chan struct{})
	var once sync.Once
	var releaseOnce sync.Once
	activityLogWriter = func(path string, data []byte) error {
		once.Do(func() {
			close(writerStarted)
		})
		<-writerRelease
		return originalWriter(path, data)
	}
	t.Cleanup(func() {
		activityLogWriter = originalWriter
		releaseOnce.Do(func() {
			close(writerRelease)
		})
	})

	logDone := make(chan error, 1)
	go func() {
		logDone <- store.Log(ActionUpload, "/slow.txt", "user", "127.0.0.1", nil)
	}()

	select {
	case <-writerStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for activity log write to start")
	}

	listDone := make(chan struct{})
	go func() {
		entries, total := store.List(10, 0, "", "")
		if total != 0 || len(entries) != 0 {
			t.Errorf("expected reads during pending persist to observe committed state only, got total=%d len=%d", total, len(entries))
		}
		close(listDone)
	}()

	select {
	case <-listDone:
	case <-time.After(time.Second):
		t.Fatal("List() blocked on an in-flight activity log save")
	}

	releaseOnce.Do(func() {
		close(writerRelease)
	})

	select {
	case err := <-logDone:
		if err != nil {
			t.Fatalf("Log() error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Log() did not finish after releasing writer")
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("expected committed entry after save, got total=%d len=%d", total, len(entries))
	}
	if entries[0].Path != "/slow.txt" {
		t.Fatalf("expected /slow.txt after save, got %s", entries[0].Path)
	}
}

func TestLogSerializesConcurrentPersists(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	originalWriter := activityLogWriter
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	secondStarted := make(chan struct{})
	var onceFirst sync.Once
	var onceRelease sync.Once
	var onceSecond sync.Once
	var callCount int32
	activityLogWriter = func(path string, data []byte) error {
		call := atomic.AddInt32(&callCount, 1)
		switch call {
		case 1:
			onceFirst.Do(func() {
				close(firstStarted)
			})
			<-firstRelease
		case 2:
			onceSecond.Do(func() {
				close(secondStarted)
			})
		}
		return originalWriter(path, data)
	}
	t.Cleanup(func() {
		activityLogWriter = originalWriter
		onceFirst.Do(func() {
			close(firstStarted)
		})
		onceSecond.Do(func() {
			close(secondStarted)
		})
		onceRelease.Do(func() {
			close(firstRelease)
		})
	})

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- store.Log(ActionUpload, "/first.txt", "user", "127.0.0.1", nil)
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first activity log persist to start")
	}

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- store.Log(ActionUpload, "/second.txt", "user", "127.0.0.1", nil)
	}()

	select {
	case <-secondStarted:
		t.Fatal("second activity persist started before first persist completed")
	case <-time.After(100 * time.Millisecond):
	}

	onceRelease.Do(func() {
		close(firstRelease)
	})

	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first Log() error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first Log() did not finish after releasing first persist")
	}

	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second activity persist to start")
	}

	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second Log() error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Log() did not finish")
	}

	entries, total := store.List(10, 0, "", "")
	if total != 2 || len(entries) != 2 {
		t.Fatalf("expected two committed activity entries, got total=%d len=%d", total, len(entries))
	}

	reloaded, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() reload error: %v", err)
	}
	reloadedEntries, reloadedTotal := reloaded.List(10, 0, "", "")
	if reloadedTotal != 2 || len(reloadedEntries) != 2 {
		t.Fatalf("expected two persisted activity entries, got total=%d len=%d", reloadedTotal, len(reloadedEntries))
	}
	if reloadedEntries[0].Path != "/second.txt" || reloadedEntries[1].Path != "/first.txt" {
		t.Fatalf("expected persisted entries [/second.txt /first.txt], got [%s %s]", reloadedEntries[0].Path, reloadedEntries[1].Path)
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

func TestLogCopiesDetailsMap(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	details := map[string]string{"reason": "original"}
	if err := store.Log(ActionDelete, "/file.txt", "user", "127.0.0.1", details); err != nil {
		t.Fatalf("Log() error: %v", err)
	}
	details["reason"] = "mutated"

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("Expected 1 entry, got total=%d len=%d", total, len(entries))
	}
	if entries[0].Details["reason"] != "original" {
		t.Fatalf("Expected stored details to remain original, got %q", entries[0].Details["reason"])
	}
}

func TestListAndGetByIDReturnDetachedDetails(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	if err := store.Log(ActionDelete, "/file.txt", "user", "127.0.0.1", map[string]string{"reason": "original"}); err != nil {
		t.Fatalf("Log() error: %v", err)
	}

	entries, _ := store.List(10, 0, "", "")
	entries[0].Details["reason"] = "mutated-via-list"

	reloadedEntries, _ := store.List(10, 0, "", "")
	if reloadedEntries[0].Details["reason"] != "original" {
		t.Fatalf("Expected list mutation to stay detached, got %q", reloadedEntries[0].Details["reason"])
	}

	entry, err := store.GetByID(reloadedEntries[0].ID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	entry.Details["reason"] = "mutated-via-get"

	finalEntries, _ := store.List(10, 0, "", "")
	if finalEntries[0].Details["reason"] != "original" {
		t.Fatalf("Expected GetByID mutation to stay detached, got %q", finalEntries[0].Details["reason"])
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
