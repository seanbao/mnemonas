package maintenance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWriteHistoryFile_ReturnsDirectorySyncError(t *testing.T) {
	tmpDir := t.TempDir()
	historyPath := filepath.Join(tmpDir, "last_scrub.json")

	originalSyncHistoryFileRootDir := syncHistoryFileRootDir
	syncHistoryFileRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncHistoryFileRootDir = originalSyncHistoryFileRootDir
	}()

	err := writeHistoryFile(historyPath, []byte("{}"))
	if err == nil {
		t.Fatal("expected writeHistoryFile() to fail when directory sync fails")
	}
	if !isHistoryPersistenceWarning(err) {
		t.Fatalf("expected maintenance persistence warning, got %v", err)
	}
	if !strings.Contains(err.Error(), "failed to sync maintenance history directory") {
		t.Fatalf("expected directory sync error, got %v", err)
	}

	data, readErr := os.ReadFile(historyPath)
	if readErr != nil {
		t.Fatalf("expected history file to remain readable after sync failure, got %v", readErr)
	}
	if string(data) != "{}" {
		t.Fatalf("expected history content to be preserved, got %q", string(data))
	}
	info, statErr := os.Stat(historyPath)
	if statErr != nil {
		t.Fatalf("expected history file to exist after sync failure, got %v", statErr)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("expected history file permissions 0644, got %o", info.Mode().Perm())
	}
}

func TestNewHistoryStore(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	if store == nil {
		t.Fatal("expected non-nil store")
	}

	if store.dataDir != tmpDir {
		t.Errorf("expected dataDir=%s, got %s", tmpDir, store.dataDir)
	}
}

func TestNewHistoryStore_CreateDir(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "nested", "dir")

	store, err := NewHistoryStore(nestedDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	if store == nil {
		t.Fatal("expected non-nil store")
	}

	// Check directory was created
	info, err := os.Stat(nestedDir)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestNewHistoryStore_ReturnsErrorWhenDirectoryTreeSyncFails(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "nested", "dir")

	originalSyncHistoryFileDir := syncHistoryFileDir
	syncHistoryFileDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncHistoryFileDir = originalSyncHistoryFileDir
	}()

	if _, err := NewHistoryStore(nestedDir); err == nil {
		t.Fatal("expected NewHistoryStore() to fail when directory tree sync fails")
	} else if !strings.Contains(err.Error(), "failed to sync maintenance history directory tree") {
		t.Fatalf("expected maintenance history directory tree sync failure, got %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(nestedDir, "last_scrub.json")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no history file to be created, got %v", statErr)
	}
}

func TestEnsureHistoryDir_SyncsCreatedDirectoriesDeepestParentFirst(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "nested", "dir")

	originalSyncHistoryFileDir := syncHistoryFileDir
	var synced []string
	syncHistoryFileDir = func(dir string) error {
		synced = append(synced, dir)
		return nil
	}
	defer func() {
		syncHistoryFileDir = originalSyncHistoryFileDir
	}()

	if err := ensureHistoryDir(nestedDir, 0755); err != nil {
		t.Fatalf("ensureHistoryDir() error: %v", err)
	}

	expected := []string{filepath.Join(tmpDir, "nested"), tmpDir}
	if !reflect.DeepEqual(synced, expected) {
		t.Fatalf("syncHistoryFileDir() order = %#v, want %#v", synced, expected)
	}
}

func TestHistoryStore_ScrubOperations(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	// Initially no scrub result
	result := store.GetLastScrubResult()
	if result != nil {
		t.Error("expected nil result initially")
	}

	// Initially not running
	if store.ScrubIsRunning() {
		t.Error("expected ScrubIsRunning=false initially")
	}

	// Start scrub
	started, err := store.StartScrub()
	if started == nil {
		t.Fatal("StartScrub returned nil")
	}
	if err != nil {
		t.Fatalf("StartScrub failed: %v", err)
	}
	if started.Status != "running" {
		t.Errorf("expected status=running, got %s", started.Status)
	}
	if started.ID == "" {
		t.Error("expected non-empty ID")
	}

	// Now should be running
	if !store.ScrubIsRunning() {
		t.Error("expected ScrubIsRunning=true after start")
	}

	// Complete the scrub
	started.Status = "completed"
	started.EndTime = time.Now()
	started.TotalObjects = 100
	started.ValidObjects = 99
	started.CorruptedObjects = 1
	started.DurationMs = 1234

	err = store.SaveScrubResult(started)
	if err != nil {
		t.Fatalf("SaveScrubResult failed: %v", err)
	}

	// Should no longer be running
	if store.ScrubIsRunning() {
		t.Error("expected ScrubIsRunning=false after completion")
	}

	// Should have result
	saved := store.GetLastScrubResult()
	if saved == nil {
		t.Fatal("expected non-nil result after save")
	}
	if saved.TotalObjects != 100 {
		t.Errorf("expected TotalObjects=100, got %d", saved.TotalObjects)
	}
	if saved.ValidObjects != 99 {
		t.Errorf("expected ValidObjects=99, got %d", saved.ValidObjects)
	}
	if saved.CorruptedObjects != 1 {
		t.Errorf("expected CorruptedObjects=1, got %d", saved.CorruptedObjects)
	}
}

func TestHistoryStore_PersistAndLoad(t *testing.T) {
	tmpDir := t.TempDir()

	// Create store and save result
	store1, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	result := &ScrubResult{
		ID:               "test-id",
		StartTime:        time.Now().Add(-time.Minute),
		EndTime:          time.Now(),
		Status:           "completed",
		TotalObjects:     500,
		ValidObjects:     495,
		CorruptedObjects: 3,
		MissingObjects:   2,
		TotalSize:        1024 * 1024,
		DurationMs:       60000,
		Errors: []ScrubError{
			{Hash: "abc123", ErrorType: "corrupted", Message: "hash mismatch"},
		},
	}

	err = store1.SaveScrubResult(result)
	if err != nil {
		t.Fatalf("SaveScrubResult failed: %v", err)
	}

	// Create new store and verify it loads persisted data
	store2, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore (reload) failed: %v", err)
	}

	loaded := store2.GetLastScrubResult()
	if loaded == nil {
		t.Fatal("expected loaded result")
	}
	if loaded.ID != "test-id" {
		t.Errorf("expected ID=test-id, got %s", loaded.ID)
	}
	if loaded.TotalObjects != 500 {
		t.Errorf("expected TotalObjects=500, got %d", loaded.TotalObjects)
	}
	if len(loaded.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(loaded.Errors))
	}
	if loaded.Errors[0].Hash != "abc123" {
		t.Errorf("expected error hash=abc123, got %s", loaded.Errors[0].Hash)
	}
}

func TestNewHistoryStore_RecoversFromCorruptLastScrubFile(t *testing.T) {
	tmpDir := t.TempDir()
	historyPath := filepath.Join(tmpDir, "last_scrub.json")
	if err := os.WriteFile(historyPath, []byte("{invalid json"), 0644); err != nil {
		t.Fatalf("WriteFile(last_scrub.json) error: %v", err)
	}

	store, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore() error: %v", err)
	}
	if store.GetLastScrubResult() != nil {
		t.Fatal("expected recovered history store to start empty")
	}

	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}
	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "last_scrub.json.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt history backup to be created")
	}

	result := &ScrubResult{ID: "after-recovery", Status: "completed", StartTime: time.Now(), EndTime: time.Now()}
	if err := store.SaveScrubResult(result); err != nil {
		t.Fatalf("SaveScrubResult() after recovery error: %v", err)
	}

	reloaded, reloadErr := NewHistoryStore(tmpDir)
	if reloadErr != nil {
		t.Fatalf("NewHistoryStore() reload error: %v", reloadErr)
	}
	loaded := reloaded.GetLastScrubResult()
	if loaded == nil || loaded.ID != "after-recovery" {
		t.Fatalf("expected recovered history to persist new result, got %+v", loaded)
	}
}

func TestNewHistoryStore_ReturnsErrorWhenCorruptBackupDirectorySyncFails(t *testing.T) {
	tmpDir := t.TempDir()
	historyPath := filepath.Join(tmpDir, "last_scrub.json")
	if err := os.WriteFile(historyPath, []byte("{invalid json"), 0644); err != nil {
		t.Fatalf("WriteFile(last_scrub.json) error: %v", err)
	}

	originalSyncHistoryFileRootDir := syncHistoryFileRootDir
	syncFailed := false
	syncHistoryFileRootDir = func(root *os.Root) error {
		if !syncFailed {
			syncFailed = true
			return errors.New("directory fsync failed")
		}
		return nil
	}
	defer func() {
		syncHistoryFileRootDir = originalSyncHistoryFileRootDir
	}()

	if _, err := NewHistoryStore(tmpDir); err == nil {
		t.Fatal("expected NewHistoryStore() to fail when corrupt backup sync fails")
	} else if !strings.Contains(err.Error(), "sync corrupt scrub history directory") {
		t.Fatalf("expected scrub history sync failure in error, got %v", err)
	}

	if _, statErr := os.Stat(historyPath); statErr != nil {
		t.Fatalf("expected original corrupt history file to remain after rollback, got %v", statErr)
	}
	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "last_scrub.json.corrupt.") {
			t.Fatalf("expected no corrupt backup after rollback, found %s", entry.Name())
		}
	}
}

func TestHistoryStore_FailedScrub(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	// Start and fail scrub
	started, err := store.StartScrub()
	if err != nil {
		t.Fatalf("StartScrub failed: %v", err)
	}
	started.Status = "failed"
	started.EndTime = time.Now()
	started.ErrorMessage = "connection refused"

	err = store.SaveScrubResult(started)
	if err != nil {
		t.Fatalf("SaveScrubResult failed: %v", err)
	}

	// Should not be running (failed is not running)
	if store.ScrubIsRunning() {
		t.Error("expected ScrubIsRunning=false for failed scrub")
	}

	// Should have error message
	saved := store.GetLastScrubResult()
	if saved.ErrorMessage != "connection refused" {
		t.Errorf("expected ErrorMessage='connection refused', got '%s'", saved.ErrorMessage)
	}
}

func TestHistoryStore_StartScrub_ReturnedResultDoesNotMutateStoreBeforeSave(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	started, err := store.StartScrub()
	if err != nil {
		t.Fatalf("StartScrub failed: %v", err)
	}

	started.Status = "completed"
	started.ErrorMessage = "should not leak"

	if !store.ScrubIsRunning() {
		t.Fatal("expected scrub to remain running until SaveScrubResult")
	}

	saved := store.GetLastScrubResult()
	if saved == nil {
		t.Fatal("expected current scrub result")
	}
	if saved.Status != "running" {
		t.Fatalf("stored status = %q, want running", saved.Status)
	}
	if saved.ErrorMessage != "" {
		t.Fatalf("stored error message = %q, want empty", saved.ErrorMessage)
	}
}

func TestHistoryStore_GetLastScrubResult_ReturnsClone(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	if err := store.SaveScrubResult(&ScrubResult{
		ID:           "clone-test",
		StartTime:    time.Now().Add(-time.Minute),
		EndTime:      time.Now(),
		Status:       "completed",
		ErrorMessage: "original",
		Errors:       []ScrubError{{Hash: "abc", ErrorType: "corrupted", Message: "mismatch"}},
	}); err != nil {
		t.Fatalf("SaveScrubResult failed: %v", err)
	}

	loaded := store.GetLastScrubResult()
	if loaded == nil {
		t.Fatal("expected loaded scrub result")
	}
	loaded.Status = "failed"
	loaded.ErrorMessage = "mutated"
	loaded.Errors[0].Message = "changed"

	again := store.GetLastScrubResult()
	if again == nil {
		t.Fatal("expected scrub result on second load")
	}
	if again.Status != "completed" {
		t.Fatalf("stored status = %q, want completed", again.Status)
	}
	if again.ErrorMessage != "original" {
		t.Fatalf("stored error message = %q, want original", again.ErrorMessage)
	}
	if again.Errors[0].Message != "mismatch" {
		t.Fatalf("stored error message detail = %q, want mismatch", again.Errors[0].Message)
	}
}

func TestNewHistoryStore_RecoverInterruptedRunningScrub(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	started, err := store.StartScrub()
	if err != nil {
		t.Fatalf("StartScrub failed: %v", err)
	}
	if started == nil {
		t.Fatal("expected non-nil started scrub result")
	}

	reloaded, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore(reload) failed: %v", err)
	}

	if reloaded.ScrubIsRunning() {
		t.Fatal("expected interrupted persisted scrub not to remain running after reload")
	}

	result := reloaded.GetLastScrubResult()
	if result == nil {
		t.Fatal("expected recovered scrub result")
	}
	if result.Status != "failed" {
		t.Fatalf("expected recovered scrub status failed, got %q", result.Status)
	}
	if result.ErrorMessage != interruptedScrubErrorMessage {
		t.Fatalf("expected recovered scrub error message %q, got %q", interruptedScrubErrorMessage, result.ErrorMessage)
	}
	if result.EndTime.IsZero() {
		t.Fatal("expected recovered scrub to have an end time")
	}
	if !result.StartTime.IsZero() && result.EndTime.Before(result.StartTime) {
		t.Fatalf("expected recovered scrub end time %v to be >= start time %v", result.EndTime, result.StartTime)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "last_scrub.json"))
	if err != nil {
		t.Fatalf("ReadFile(last_scrub.json) error: %v", err)
	}
	var persisted ScrubResult
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal(last_scrub.json) error: %v", err)
	}
	if persisted.Status != "failed" {
		t.Fatalf("expected recovered scrub status to be persisted as failed, got %q", persisted.Status)
	}
	if persisted.ErrorMessage != interruptedScrubErrorMessage {
		t.Fatalf("expected recovered scrub error message to persist as %q, got %q", interruptedScrubErrorMessage, persisted.ErrorMessage)
	}
}

func TestHistoryStore_SaveScrubResult_DoesNotFollowSymlinkPath(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	started, err := store.StartScrub()
	if err != nil {
		t.Fatalf("StartScrub failed: %v", err)
	}

	historyPath := filepath.Join(tmpDir, "last_scrub.json")
	backupPath := filepath.Join(tmpDir, "last_scrub.backup.json")
	if err := os.Rename(historyPath, backupPath); err != nil {
		t.Fatalf("failed to rename history file: %v", err)
	}
	if err := os.Symlink(backupPath, historyPath); err != nil {
		t.Fatalf("failed to replace history file with symlink: %v", err)
	}

	started.Status = "completed"
	started.EndTime = time.Now()
	started.TotalObjects = 5

	err = store.SaveScrubResult(started)
	if err != nil {
		t.Fatalf("expected rooted final save to replace symlink path safely, got %v", err)
	}
	if store.ScrubIsRunning() {
		t.Fatal("expected failed final persistence not to leave scrub running in memory")
	}

	result := store.GetLastScrubResult()
	if result == nil {
		t.Fatal("expected in-memory scrub result after failed final persistence")
	}
	if result.Status != "completed" {
		t.Fatalf("expected in-memory scrub status completed, got %q", result.Status)
	}
	if result.TotalObjects != 5 {
		t.Fatalf("expected in-memory scrub total_objects 5, got %d", result.TotalObjects)
	}
	if result.EndTime.IsZero() {
		t.Fatal("expected in-memory scrub result to retain end time")
	}

	targetData, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("ReadFile(last_scrub.backup.json) error: %v", err)
	}
	var targetPersisted ScrubResult
	if err := json.Unmarshal(targetData, &targetPersisted); err != nil {
		t.Fatalf("Unmarshal(last_scrub.backup.json) error: %v", err)
	}
	if targetPersisted.Status != "running" {
		t.Fatalf("expected symlink target scrub status to remain running, got %q", targetPersisted.Status)
	}

	info, err := os.Lstat(historyPath)
	if err != nil {
		t.Fatalf("Lstat(last_scrub.json) error: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("expected last_scrub.json symlink to be replaced with a regular file")
	}

	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("ReadFile(last_scrub.json) error: %v", err)
	}
	var persisted ScrubResult
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal(last_scrub.json) error: %v", err)
	}
	if persisted.Status != "completed" {
		t.Fatalf("expected canonical scrub status completed, got %q", persisted.Status)
	}
	if persisted.TotalObjects != 5 {
		t.Fatalf("expected canonical scrub total_objects 5, got %d", persisted.TotalObjects)
	}
}

func TestNewHistoryStore_RestoresTerminalFallbackAfterFailedFinalPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	started, err := store.StartScrub()
	if err != nil {
		t.Fatalf("StartScrub failed: %v", err)
	}

	originalWriteHistoryStoreFile := writeHistoryStoreFile
	mainWriteCount := 0
	writeHistoryStoreFile = func(path string, data []byte) error {
		if filepath.Base(path) == "last_scrub.json" {
			mainWriteCount++
			if mainWriteCount == 1 {
				return errors.New("disk offline")
			}
		}
		return originalWriteHistoryStoreFile(path, data)
	}
	defer func() {
		writeHistoryStoreFile = originalWriteHistoryStoreFile
	}()

	started.Status = "completed"
	started.EndTime = time.Now()
	started.TotalObjects = 5

	err = store.SaveScrubResult(started)
	if err == nil || !strings.Contains(err.Error(), "disk offline") {
		t.Fatalf("expected disk offline error on final save, got %v", err)
	}

	current := store.GetLastScrubResult()
	if current == nil {
		t.Fatal("expected in-memory scrub result after failed final save")
	}
	if current.Status != "completed" {
		t.Fatalf("expected in-memory scrub status completed, got %q", current.Status)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "last_scrub.json"))
	if err != nil {
		t.Fatalf("ReadFile(last_scrub.json) error: %v", err)
	}
	var persisted ScrubResult
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal(last_scrub.json) error: %v", err)
	}
	if persisted.Status != "running" {
		t.Fatalf("expected canonical scrub status to remain running before reload, got %q", persisted.Status)
	}

	fallbackData, err := os.ReadFile(filepath.Join(tmpDir, "last_scrub.terminal.json"))
	if err != nil {
		t.Fatalf("ReadFile(last_scrub.terminal.json) error: %v", err)
	}
	var fallback ScrubResult
	if err := json.Unmarshal(fallbackData, &fallback); err != nil {
		t.Fatalf("Unmarshal(last_scrub.terminal.json) error: %v", err)
	}
	if fallback.Status != "completed" {
		t.Fatalf("expected terminal fallback status completed, got %q", fallback.Status)
	}
	if fallback.TotalObjects != 5 {
		t.Fatalf("expected terminal fallback total_objects 5, got %d", fallback.TotalObjects)
	}

	writeHistoryStoreFile = originalWriteHistoryStoreFile

	reloaded, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore(reload) failed: %v", err)
	}
	if reloaded.ScrubIsRunning() {
		t.Fatal("expected restored terminal fallback scrub not to remain running after reload")
	}

	restored := reloaded.GetLastScrubResult()
	if restored == nil {
		t.Fatal("expected restored scrub result after reload")
	}
	if restored.Status != "completed" {
		t.Fatalf("expected restored scrub status completed, got %q", restored.Status)
	}
	if restored.TotalObjects != 5 {
		t.Fatalf("expected restored scrub total_objects 5, got %d", restored.TotalObjects)
	}

	data, err = os.ReadFile(filepath.Join(tmpDir, "last_scrub.json"))
	if err != nil {
		t.Fatalf("ReadFile(last_scrub.json) after reload error: %v", err)
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal(last_scrub.json) after reload error: %v", err)
	}
	if persisted.Status != "completed" {
		t.Fatalf("expected canonical scrub status completed after reload repair, got %q", persisted.Status)
	}
}

func TestNewHistoryStore_RejectsSymlinkHistoryFile(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "real-history.json")
	historyPath := filepath.Join(tmpDir, "last_scrub.json")

	if err := os.WriteFile(targetPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to write target history file: %v", err)
	}
	if err := os.Symlink(targetPath, historyPath); err != nil {
		t.Fatalf("failed to create history symlink: %v", err)
	}

	_, err := NewHistoryStore(tmpDir)
	if !errors.Is(err, errHistoryFileSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestNewHistoryStore_RejectsSymlinkParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "real-history")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatalf("failed to create real history dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "last_scrub.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to seed history file: %v", err)
	}
	linkedDir := filepath.Join(tmpDir, "linked-history")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Fatalf("failed to create history dir symlink: %v", err)
	}

	_, err := NewHistoryStore(linkedDir)
	if !errors.Is(err, errHistoryFileSymlink) {
		t.Fatalf("expected parent-directory symlink rejection, got %v", err)
	}
}

func TestNewHistoryStore_DoesNotCreateDirThroughSymlinkParent(t *testing.T) {
	tmpDir := t.TempDir()
	realParent := filepath.Join(tmpDir, "real-parent")
	if err := os.MkdirAll(realParent, 0755); err != nil {
		t.Fatalf("MkdirAll(real-parent) error: %v", err)
	}
	linkedParent := filepath.Join(tmpDir, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("Symlink(linked-parent) error: %v", err)
	}

	historyRoot := filepath.Join(linkedParent, "history")
	if _, err := NewHistoryStore(historyRoot); !errors.Is(err, errHistoryFileSymlink) {
		t.Fatalf("expected symlink parent rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(realParent, "history")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("history root created through symlink parent, stat error = %v", err)
	}
}

func TestHistoryStore_StartScrub_DoesNotFollowSymlinkPath(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	targetPath := filepath.Join(tmpDir, "real-history.json")
	historyPath := filepath.Join(tmpDir, "last_scrub.json")
	if err := os.WriteFile(targetPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to write target history file: %v", err)
	}
	if err := os.Symlink(targetPath, historyPath); err != nil {
		t.Fatalf("failed to create history symlink: %v", err)
	}

	started, err := store.StartScrub()
	if err != nil {
		t.Fatalf("expected rooted start scrub to replace symlink path safely, got %v", err)
	}
	if started == nil || started.Status != "running" {
		t.Fatalf("expected running scrub result, got %#v", started)
	}
	result := store.GetLastScrubResult()
	if result == nil || result.Status != "running" {
		t.Fatalf("expected in-memory running scrub result, got %#v", result)
	}

	targetData, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile(real-history.json) error: %v", err)
	}
	if string(targetData) != "{}" {
		t.Fatalf("expected symlink target to remain unchanged, got %q", string(targetData))
	}

	info, err := os.Lstat(historyPath)
	if err != nil {
		t.Fatalf("Lstat(last_scrub.json) error: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("expected last_scrub.json symlink to be replaced with a regular file")
	}

	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("ReadFile(last_scrub.json) error: %v", err)
	}
	var persisted ScrubResult
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal(last_scrub.json) error: %v", err)
	}
	if persisted.Status != "running" {
		t.Fatalf("expected persisted scrub status running, got %q", persisted.Status)
	}
}

func TestNewHistoryStore_Load_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "history")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("MkdirAll(history) error: %v", err)
	}
	original := &ScrubResult{ID: "original", Status: "completed", TotalObjects: 5, StartTime: time.Unix(1, 0), EndTime: time.Unix(2, 0)}
	originalData, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal(original) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "last_scrub.json"), originalData, 0644); err != nil {
		t.Fatalf("WriteFile(last_scrub.json) error: %v", err)
	}
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("MkdirAll(outside) error: %v", err)
	}
	outside := &ScrubResult{ID: "outside", Status: "completed", TotalObjects: 99, StartTime: time.Unix(3, 0), EndTime: time.Unix(4, 0)}
	outsideData, err := json.Marshal(outside)
	if err != nil {
		t.Fatalf("Marshal(outside) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "last_scrub.json"), outsideData, 0644); err != nil {
		t.Fatalf("WriteFile(outside last_scrub.json) error: %v", err)
	}
	backupDir := dataDir + "-backup"

	originalAfterValidateHistoryFilePath := afterValidateHistoryFilePath
	afterValidateHistoryFilePath = func() {
		if err := os.Rename(dataDir, backupDir); err != nil {
			t.Fatalf("Rename(history) failed: %v", err)
		}
		if err := os.Symlink(outsideDir, dataDir); err != nil {
			t.Fatalf("Symlink(history) failed: %v", err)
		}
	}
	defer func() {
		afterValidateHistoryFilePath = originalAfterValidateHistoryFilePath
	}()

	store, err := NewHistoryStore(dataDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	loaded := store.GetLastScrubResult()
	if loaded == nil || loaded.ID != "original" {
		t.Fatalf("expected original scrub result after validation race, got %#v", loaded)
	}
	if loaded.TotalObjects != 5 {
		t.Fatalf("expected original total_objects 5, got %d", loaded.TotalObjects)
	}

	data, err := os.ReadFile(filepath.Join(outsideDir, "last_scrub.json"))
	if err != nil {
		t.Fatalf("ReadFile(outside last_scrub.json) error: %v", err)
	}
	var persisted ScrubResult
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal(outside last_scrub.json) error: %v", err)
	}
	if persisted.ID != "outside" {
		t.Fatalf("expected outside scrub result unchanged, got %q", persisted.ID)
	}
}

func TestHistoryStore_StartScrub_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "history")
	store, err := NewHistoryStore(dataDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("MkdirAll(outside) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "last_scrub.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("WriteFile(outside last_scrub.json) error: %v", err)
	}
	backupDir := dataDir + "-backup"
	swapped := false

	originalAfterValidateHistoryFilePath := afterValidateHistoryFilePath
	afterValidateHistoryFilePath = func() {
		if swapped {
			return
		}
		swapped = true
		if err := os.Rename(dataDir, backupDir); err != nil {
			t.Fatalf("Rename(history) failed: %v", err)
		}
		if err := os.Symlink(outsideDir, dataDir); err != nil {
			t.Fatalf("Symlink(history) failed: %v", err)
		}
	}
	defer func() {
		afterValidateHistoryFilePath = originalAfterValidateHistoryFilePath
	}()

	started, err := store.StartScrub()
	if err != nil {
		t.Fatalf("expected rooted start scrub to stay on original directory, got %v", err)
	}
	if started == nil || started.Status != "running" {
		t.Fatalf("expected running scrub result, got %#v", started)
	}

	data, err := os.ReadFile(filepath.Join(outsideDir, "last_scrub.json"))
	if err != nil {
		t.Fatalf("ReadFile(outside last_scrub.json) error: %v", err)
	}
	if string(data) != "{}" {
		t.Fatalf("expected outside history file unchanged, got %q", string(data))
	}

	data, err = os.ReadFile(filepath.Join(backupDir, "last_scrub.json"))
	if err != nil {
		t.Fatalf("ReadFile(backup last_scrub.json) error: %v", err)
	}
	var persisted ScrubResult
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal(backup last_scrub.json) error: %v", err)
	}
	if persisted.Status != "running" {
		t.Fatalf("expected persisted scrub status running in original directory, got %q", persisted.Status)
	}
}

func TestNewHistoryStore_RecoverCorruptScrub_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "history")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("MkdirAll(history) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "last_scrub.json"), []byte("{invalid json"), 0644); err != nil {
		t.Fatalf("WriteFile(last_scrub.json) error: %v", err)
	}
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("MkdirAll(outside) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "last_scrub.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("WriteFile(outside last_scrub.json) error: %v", err)
	}
	backupDir := dataDir + "-backup"
	swapped := false

	originalAfterValidateHistoryFilePath := afterValidateHistoryFilePath
	afterValidateHistoryFilePath = func() {
		if swapped {
			return
		}
		swapped = true
		if err := os.Rename(dataDir, backupDir); err != nil {
			t.Fatalf("Rename(history) failed: %v", err)
		}
		if err := os.Symlink(outsideDir, dataDir); err != nil {
			t.Fatalf("Symlink(history) failed: %v", err)
		}
	}
	defer func() {
		afterValidateHistoryFilePath = originalAfterValidateHistoryFilePath
	}()

	store, err := NewHistoryStore(dataDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}
	if store.GetLastScrubResult() != nil {
		t.Fatal("expected corrupt history recovery to clear in-memory scrub result")
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("ReadDir(backup history) error: %v", err)
	}
	foundCorruptBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "last_scrub.json.corrupt.") {
			foundCorruptBackup = true
			break
		}
	}
	if !foundCorruptBackup {
		t.Fatal("expected corrupt history backup to remain in original directory")
	}

	data, err := os.ReadFile(filepath.Join(outsideDir, "last_scrub.json"))
	if err != nil {
		t.Fatalf("ReadFile(outside last_scrub.json) error: %v", err)
	}
	if string(data) != "{}" {
		t.Fatalf("expected outside history file unchanged, got %q", string(data))
	}
	outsideEntries, err := os.ReadDir(outsideDir)
	if err != nil {
		t.Fatalf("ReadDir(outside) error: %v", err)
	}
	for _, entry := range outsideEntries {
		if strings.HasPrefix(entry.Name(), "last_scrub.json.corrupt.") {
			t.Fatalf("expected no corrupt backup outside original directory, found %s", entry.Name())
		}
	}
}

func TestHistoryStore_StartScrubDirectorySyncWarningKeepsRunningState(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	originalSyncHistoryFileRootDir := syncHistoryFileRootDir
	syncHistoryFileRootDir = func(*os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncHistoryFileRootDir = originalSyncHistoryFileRootDir
	}()

	started, err := store.StartScrub()
	if err == nil {
		t.Fatal("expected StartScrub to report directory sync warning")
	}
	if !isHistoryPersistenceWarning(err) {
		t.Fatalf("expected maintenance persistence warning, got %v", err)
	}
	if started == nil || started.Status != "running" {
		t.Fatalf("expected running scrub result despite warning, got %#v", started)
	}
	if !store.ScrubIsRunning() {
		t.Fatal("expected scrub to remain running in memory after directory sync warning")
	}

	result := store.GetLastScrubResult()
	if result == nil || result.Status != "running" {
		t.Fatalf("expected running in-memory scrub result, got %#v", result)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "last_scrub.json"))
	if err != nil {
		t.Fatalf("ReadFile(last_scrub.json) error: %v", err)
	}
	var persisted ScrubResult
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal(last_scrub.json) error: %v", err)
	}
	if persisted.Status != "running" {
		t.Fatalf("expected persisted scrub status running, got %q", persisted.Status)
	}
}

func TestHistoryStore_SaveScrubResultDirectorySyncWarningKeepsCanonicalState(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	started, err := store.StartScrub()
	if err != nil {
		t.Fatalf("StartScrub failed: %v", err)
	}

	originalSyncHistoryFileRootDir := syncHistoryFileRootDir
	syncHistoryFileRootDir = func(*os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncHistoryFileRootDir = originalSyncHistoryFileRootDir
	}()

	started.Status = "completed"
	started.EndTime = time.Now()
	started.TotalObjects = 7

	err = store.SaveScrubResult(started)
	if err == nil {
		t.Fatal("expected SaveScrubResult to report directory sync warning")
	}
	if !isHistoryPersistenceWarning(err) {
		t.Fatalf("expected maintenance persistence warning, got %v", err)
	}
	if store.ScrubIsRunning() {
		t.Fatal("expected scrub to stop running in memory after completed save warning")
	}

	result := store.GetLastScrubResult()
	if result == nil || result.Status != "completed" {
		t.Fatalf("expected completed in-memory scrub result, got %#v", result)
	}
	if result.TotalObjects != 7 {
		t.Fatalf("expected in-memory scrub total_objects 7, got %d", result.TotalObjects)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "last_scrub.json"))
	if err != nil {
		t.Fatalf("ReadFile(last_scrub.json) error: %v", err)
	}
	var persisted ScrubResult
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal(last_scrub.json) error: %v", err)
	}
	if persisted.Status != "completed" {
		t.Fatalf("expected persisted scrub status completed, got %q", persisted.Status)
	}
	if persisted.TotalObjects != 7 {
		t.Fatalf("expected persisted scrub total_objects 7, got %d", persisted.TotalObjects)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "last_scrub.terminal.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no terminal fallback file for visible canonical save, got %v", err)
	}
}

func TestNewHistoryStore_RecoverInterruptedScrubDirectorySyncWarningStillLoads(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	started, err := store.StartScrub()
	if err != nil {
		t.Fatalf("StartScrub failed: %v", err)
	}
	if started == nil || started.Status != "running" {
		t.Fatalf("expected running scrub result, got %#v", started)
	}

	originalSyncHistoryFileRootDir := syncHistoryFileRootDir
	syncHistoryFileRootDir = func(*os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncHistoryFileRootDir = originalSyncHistoryFileRootDir
	}()

	reloaded, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore(reload) failed: %v", err)
	}
	if reloaded.ScrubIsRunning() {
		t.Fatal("expected recovered interrupted scrub not to remain running")
	}

	result := reloaded.GetLastScrubResult()
	if result == nil {
		t.Fatal("expected recovered scrub result after reload")
	}
	if result.Status != "failed" {
		t.Fatalf("expected recovered scrub status failed, got %q", result.Status)
	}
	if result.ErrorMessage != interruptedScrubErrorMessage {
		t.Fatalf("expected recovered scrub error message %q, got %q", interruptedScrubErrorMessage, result.ErrorMessage)
	}
}

func TestHistoryStore_StartScrub_RejectsAlreadyRunning(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	started, err := store.StartScrub()
	if err != nil {
		t.Fatalf("first StartScrub failed: %v", err)
	}
	if started == nil || started.Status != "running" {
		t.Fatalf("expected first scrub start to return running result, got %#v", started)
	}

	startedAgain, err := store.StartScrub()
	if !errors.Is(err, ErrScrubAlreadyRunning) {
		t.Fatalf("expected ErrScrubAlreadyRunning, got %v", err)
	}
	if startedAgain != nil {
		t.Fatalf("expected nil result when scrub already running, got %#v", startedAgain)
	}
	if !store.ScrubIsRunning() {
		t.Fatal("expected scrub to remain running after duplicate start rejection")
	}

	current := store.GetLastScrubResult()
	if current == nil || current.ID != started.ID {
		t.Fatalf("expected running scrub record to remain unchanged, got %#v", current)
	}
}

func TestGCRunningState(t *testing.T) {
	// Ensure clean state first
	for GCIsRunning() {
		FinishGC()
	}

	// Initially not running
	if GCIsRunning() {
		t.Error("expected GCIsRunning=false initially")
	}

	// Start GC
	if !StartGC() {
		t.Error("expected StartGC to return true")
	}

	// Now running
	if !GCIsRunning() {
		t.Error("expected GCIsRunning=true after start")
	}

	// Cannot start again
	if StartGC() {
		t.Error("expected StartGC to return false when already running")
	}

	// Finish GC
	FinishGC()

	// No longer running
	if GCIsRunning() {
		t.Error("expected GCIsRunning=false after finish")
	}

	// Can start again
	if !StartGC() {
		t.Error("expected StartGC to return true after finish")
	}
	FinishGC() // cleanup
}

func TestGCConcurrentStart(t *testing.T) {
	// Ensure clean state
	for GCIsRunning() {
		FinishGC()
	}

	started := make(chan bool, 10)

	// Try to start GC from multiple goroutines
	for i := 0; i < 10; i++ {
		go func() {
			started <- StartGC()
		}()
	}

	// Count how many succeeded
	successCount := 0
	for i := 0; i < 10; i++ {
		if <-started {
			successCount++
		}
	}

	// Exactly one should have succeeded
	if successCount != 1 {
		t.Errorf("expected exactly 1 successful start, got %d", successCount)
	}

	FinishGC() // cleanup
}
