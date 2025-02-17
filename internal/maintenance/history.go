// Package maintenance provides system maintenance tasks and history
package maintenance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

var errHistoryFileSymlink = errors.New("maintenance history file path must not be a symlink")

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
	if err := store.loadLastScrubResult(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return store, nil
}

// SaveScrubResult saves a scrub result
func (s *HistoryStore) SaveScrubResult(result *ScrubResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	previous := s.scrubResult
	s.scrubResult = result
	if err := s.persistScrubResult(result); err != nil {
		s.scrubResult = previous
		return err
	}
	return nil
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

// StartScrub marks a scrub as started.
func (s *HistoryStore) StartScrub() (*ScrubResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	previous := s.scrubResult
	result := &ScrubResult{
		ID:        time.Now().Format("20060102-150405"),
		StartTime: time.Now(),
		Status:    "running",
	}
	s.scrubResult = result
	if err := s.persistScrubResult(result); err != nil {
		s.scrubResult = previous
		return nil, err
	}

	return result, nil
}

func (s *HistoryStore) loadLastScrubResult() error {
	path := s.lastScrubPath()
	if err := validateHistoryFilePath(path); err != nil {
		return err
	}
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

func (s *HistoryStore) lastScrubPath() string {
	return filepath.Join(s.dataDir, "last_scrub.json")
}

func (s *HistoryStore) persistScrubResult(result *ScrubResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return writeHistoryFile(s.lastScrubPath(), data)
}

func validateHistoryFilePath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to stat maintenance history file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errHistoryFileSymlink
	}
	return nil
}

func writeHistoryFile(path string, data []byte) error {
	if err := validateHistoryFilePath(path); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create maintenance history directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".last-scrub-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp maintenance history file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmpFile.Chmod(0644); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to set maintenance history file permissions: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write maintenance history file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to sync maintenance history file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp maintenance history file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to replace maintenance history file: %w", err)
	}
	cleanup = false
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
