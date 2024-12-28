// Package maintenance provides system maintenance tasks and history
package maintenance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// gcRunning tracks whether GC is currently running (atomic for thread-safety)
var gcRunning atomic.Bool

// ScrubResult represents the result of a scrub operation
type ScrubResult struct {
	ID               string       `json:"id"`
	StartTime        time.Time    `json:"start_time"`
	EndTime          time.Time    `json:"end_time"`
	Status           string       `json:"status"` // "running", "completed", "failed"
	TotalObjects     uint64       `json:"total_objects"`
	ValidObjects     uint64       `json:"valid_objects"`
	CorruptedObjects uint64       `json:"corrupted_objects"`
	MissingObjects   uint64       `json:"missing_objects"`
	TotalSize        uint64       `json:"total_size"`
	DurationMs       uint64       `json:"duration_ms"`
	Errors           []ScrubError `json:"errors,omitempty"`
	ErrorMessage     string       `json:"error_message,omitempty"`
}

// ScrubError represents a single error found during scrub
type ScrubError struct {
	Hash      string `json:"hash"`
	ErrorType string `json:"error_type"`
	Message   string `json:"message"`
}

// HistoryStore stores maintenance task history
type HistoryStore struct {
	dataDir     string
	mu          sync.RWMutex
	scrubResult *ScrubResult // Most recent scrub result
}

// NewHistoryStore creates a new history store
func NewHistoryStore(dataDir string) (*HistoryStore, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	store := &HistoryStore{
		dataDir: dataDir,
	}
	// Load last scrub result if exists
	_ = store.loadLastScrubResult()
	return store, nil
}

// SaveScrubResult saves a scrub result
func (s *HistoryStore) SaveScrubResult(result *ScrubResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.scrubResult = result
	// Persist to disk atomically
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dataDir, "last_scrub.json")
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// GetLastScrubResult returns the most recent scrub result
func (s *HistoryStore) GetLastScrubResult() *ScrubResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scrubResult
}

// ScrubIsRunning checks if a scrub is currently in progress
func (s *HistoryStore) ScrubIsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.scrubResult == nil {
		return false
	}
	return s.scrubResult.Status == "running"
}

// StartScrub marks a scrub as started
func (s *HistoryStore) StartScrub() *ScrubResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := &ScrubResult{
		ID:        time.Now().Format("20060102-150405"),
		StartTime: time.Now(),
		Status:    "running",
	}
	s.scrubResult = result
	// Persist
	data, _ := json.MarshalIndent(result, "", "  ")
	path := filepath.Join(s.dataDir, "last_scrub.json")
	os.WriteFile(path, data, 0644)

	return result
}

func (s *HistoryStore) loadLastScrubResult() error {
	path := filepath.Join(s.dataDir, "last_scrub.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var result ScrubResult
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}
	s.scrubResult = &result
	return nil
}

// GCIsRunning checks if GC is currently in progress
func GCIsRunning() bool {
	return gcRunning.Load()
}

// StartGC attempts to start GC, returns false if already running
func StartGC() bool {
	return gcRunning.CompareAndSwap(false, true)
}

// FinishGC marks GC as finished
func FinishGC() {
	gcRunning.Store(false)
}
