package maintenance

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
	started := store.StartScrub()
	if started == nil {
		t.Fatal("StartScrub returned nil")
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

func TestHistoryStore_FailedScrub(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewHistoryStore(tmpDir)
	if err != nil {
		t.Fatalf("NewHistoryStore failed: %v", err)
	}

	// Start and fail scrub
	started := store.StartScrub()
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
