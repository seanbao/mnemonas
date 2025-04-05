// Package maintenance provides system maintenance tasks and history
package maintenance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var errHistoryFileSymlink = errors.New("maintenance history file path must not be a symlink")
var ErrScrubAlreadyRunning = errors.New("scrub already running")

const interruptedScrubErrorMessage = "scrub interrupted before completion"

var syncHistoryFileDir = syncHistoryDir
var writeHistoryStoreFile = writeHistoryFile

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

func cloneScrubResult(result *ScrubResult) *ScrubResult {
	if result == nil {
		return nil
	}
	clone := *result
	if len(result.Errors) > 0 {
		clone.Errors = append([]ScrubError(nil), result.Errors...)
	}
	return &clone
}

// NewHistoryStore creates a new history store
func NewHistoryStore(dataDir string) (*HistoryStore, error) {
	if err := ensureHistoryDir(dataDir, 0755); err != nil {
		return nil, err
	}
	store := &HistoryStore{
		dataDir: dataDir,
	}
	// Load last scrub result if exists
	if err := store.loadLastScrubResult(); err != nil && !os.IsNotExist(err) {
		if recoverErr := store.recoverCorruptLastScrubResult(err); recoverErr != nil {
			return nil, errors.Join(
				fmt.Errorf("load last scrub result: %w", err),
				fmt.Errorf("recover corrupt scrub history: %w", recoverErr),
			)
		}
	}
	return store, nil
}

// SaveScrubResult saves a scrub result
func (s *HistoryStore) SaveScrubResult(result *ScrubResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	previous := cloneScrubResult(s.scrubResult)
	stored := cloneScrubResult(result)
	s.scrubResult = stored
	if err := s.persistScrubResult(stored); err != nil {
		if shouldPreserveTerminalScrubState(previous, stored) {
			if fallbackErr := s.persistScrubTerminalFallback(stored); fallbackErr != nil {
				return errors.Join(err, fmt.Errorf("persist scrub terminal fallback: %w", fallbackErr))
			}
		} else {
			s.scrubResult = previous
		}
		return err
	}
	return nil
}

func shouldPreserveTerminalScrubState(previous, current *ScrubResult) bool {
	if previous == nil || current == nil {
		return false
	}
	if previous.ID == "" || previous.ID != current.ID {
		return false
	}
	return previous.Status == "running" && current.Status != "running"
}

// GetLastScrubResult returns the most recent scrub result
func (s *HistoryStore) GetLastScrubResult() *ScrubResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneScrubResult(s.scrubResult)
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
	if s.scrubResult != nil && s.scrubResult.Status == "running" {
		return nil, ErrScrubAlreadyRunning
	}

	previous := s.scrubResult
	result := &ScrubResult{
		ID:        time.Now().Format("20060102-150405"),
		StartTime: time.Now(),
		Status:    "running",
	}
	s.scrubResult = cloneScrubResult(result)
	if err := s.persistScrubResult(result); err != nil {
		s.scrubResult = previous
		return nil, err
	}

	return cloneScrubResult(result), nil
}

func (s *HistoryStore) loadLastScrubResult() error {
	result, err := loadScrubResultFile(s.lastScrubPath())
	if err != nil {
		return err
	}

	if fallback, fallbackErr := s.loadScrubTerminalFallback(result); fallbackErr != nil {
		if !os.IsNotExist(fallbackErr) {
			return fmt.Errorf("load scrub terminal fallback: %w", fallbackErr)
		}
	} else if fallback != nil {
		result = fallback
		_ = s.persistScrubResult(result)
	}

	if recoverInterruptedScrubResult(result, time.Now()) {
		if err := s.persistScrubResult(result); err != nil {
			return fmt.Errorf("persist recovered scrub result: %w", err)
		}
	}
	s.scrubResult = cloneScrubResult(result)
	return nil
}

func (s *HistoryStore) recoverCorruptLastScrubResult(loadErr error) error {
	if !isRecoverableHistoryLoadError(loadErr) {
		return loadErr
	}

	path := s.lastScrubPath()
	dir := filepath.Dir(path)
	corruptPath := fmt.Sprintf("%s.corrupt.%d", path, time.Now().UnixNano())
	if err := os.Rename(path, corruptPath); err != nil {
		return fmt.Errorf("backup corrupt scrub history: %w", err)
	}
	if err := syncHistoryFileDir(dir); err != nil {
		if rollbackErr := os.Rename(corruptPath, path); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt scrub history directory: %w", err),
				fmt.Errorf("rollback corrupt scrub history backup: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncHistoryFileDir(dir); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt scrub history directory: %w", err),
				fmt.Errorf("sync corrupt scrub history rollback: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("sync corrupt scrub history directory: %w", err)
	}

	s.scrubResult = nil
	return nil
}

func isRecoverableHistoryLoadError(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}

	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return true
	}

	var typeErr *json.UnmarshalTypeError
	return errors.As(err, &typeErr)
}

func recoverInterruptedScrubResult(result *ScrubResult, now time.Time) bool {
	if result == nil || result.Status != "running" {
		return false
	}

	result.Status = "failed"
	if result.EndTime.IsZero() || (!result.StartTime.IsZero() && result.EndTime.Before(result.StartTime)) {
		if !result.StartTime.IsZero() && now.Before(result.StartTime) {
			result.EndTime = result.StartTime
		} else {
			result.EndTime = now
		}
	}
	if result.ErrorMessage == "" {
		result.ErrorMessage = interruptedScrubErrorMessage
	}
	if result.DurationMs == 0 && !result.StartTime.IsZero() && !result.EndTime.Before(result.StartTime) {
		result.DurationMs = uint64(result.EndTime.Sub(result.StartTime).Milliseconds())
	}
	return true
}

func (s *HistoryStore) lastScrubPath() string {
	return filepath.Join(s.dataDir, "last_scrub.json")
}

func (s *HistoryStore) lastScrubTerminalFallbackPath() string {
	return filepath.Join(s.dataDir, "last_scrub.terminal.json")
}

func (s *HistoryStore) persistScrubResult(result *ScrubResult) error {
	return s.persistScrubResultToPath(s.lastScrubPath(), result)
}

func (s *HistoryStore) persistScrubTerminalFallback(result *ScrubResult) error {
	return s.persistScrubResultToPath(s.lastScrubTerminalFallbackPath(), result)
}

func (s *HistoryStore) persistScrubResultToPath(path string, result *ScrubResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return writeHistoryStoreFile(path, data)
}

func loadScrubResultFile(path string) (*ScrubResult, error) {
	if err := validateHistoryFilePath(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result ScrubResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *HistoryStore) loadScrubTerminalFallback(current *ScrubResult) (*ScrubResult, error) {
	if current == nil || current.Status != "running" {
		return nil, nil
	}

	fallback, err := loadScrubResultFile(s.lastScrubTerminalFallbackPath())
	if err != nil {
		return nil, err
	}
	if !sameScrubExecution(current, fallback) || fallback.Status == "running" {
		return nil, nil
	}
	return fallback, nil
}

func sameScrubExecution(current, fallback *ScrubResult) bool {
	if current == nil || fallback == nil {
		return false
	}
	if current.ID == "" || current.ID != fallback.ID {
		return false
	}
	return current.StartTime.Equal(fallback.StartTime)
}

func validateHistoryFilePath(path string) error {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		absPath, err := filepath.Abs(cleaned)
		if err != nil {
			return fmt.Errorf("failed to resolve maintenance history file path: %w", err)
		}
		cleaned = absPath
	}

	root := filepath.VolumeName(cleaned) + string(filepath.Separator)
	current := root
	trimmed := strings.TrimPrefix(cleaned, root)
	if trimmed == "" {
		info, err := os.Lstat(cleaned)
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

	for _, part := range strings.Split(trimmed, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("failed to stat maintenance history file: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errHistoryFileSymlink
		}
	}
	return nil
}

func writeHistoryFile(path string, data []byte) error {
	if err := validateHistoryFilePath(path); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := ensureHistoryDir(dir, 0755); err != nil {
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
	if err := syncHistoryFileDir(dir); err != nil {
		return fmt.Errorf("failed to sync maintenance history directory: %w", err)
	}
	return nil
}

func collectMissingHistoryDirs(dir string) ([]string, error) {
	missing := make([]string, 0)
	current := filepath.Clean(dir)
	for {
		if _, err := os.Stat(current); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}

		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return missing, nil
}

func syncCreatedHistoryDirs(createdDirs []string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncHistoryFileDir(filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync maintenance history directory tree: %w", err)
		}
	}
	return nil
}

func ensureHistoryDir(dir string, perm os.FileMode) error {
	createdDirs, err := collectMissingHistoryDirs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	return syncCreatedHistoryDirs(createdDirs)
}

func syncHistoryDir(dir string) error {
	dirHandle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
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
