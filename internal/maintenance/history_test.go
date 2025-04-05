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

	originalSyncHistoryFileDir := syncHistoryFileDir
	syncHistoryFileDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncHistoryFileDir = originalSyncHistoryFileDir
	}()

	err := writeHistoryFile(historyPath, []byte("{}"))
	if err == nil {
		t.Fatal("expected writeHistoryFile() to fail when directory sync fails")
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

	originalSyncHistoryFileDir := syncHistoryFileDir
	syncFailed := false
	syncHistoryFileDir = func(dir string) error {
		if !syncFailed {
			syncFailed = true
			return errors.New("directory fsync failed")
		}
		return nil
	}
	defer func() {
		syncHistoryFileDir = originalSyncHistoryFileDir
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

func TestHistoryStore_SaveScrubResultFailureKeepsTerminalStateInMemory(t *testing.T) {
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
	if !errors.Is(err, errHistoryFileSymlink) {
		t.Fatalf("expected symlink rejection on final save, got %v", err)
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

func TestHistoryStore_StartScrubRejectsSymlinkHistoryFile(t *testing.T) {
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
	if !errors.Is(err, errHistoryFileSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
	if started != nil {
		t.Fatal("expected nil result when start scrub persistence fails")
	}
	if store.GetLastScrubResult() != nil {
		t.Fatal("expected in-memory scrub result rollback after failed start")
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
