// Package maintenance provides system maintenance tasks and history
package maintenance

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/seanbao/mnemonas/internal/rootio"
)

var errHistoryFileSymlink = errors.New("maintenance history file path must not be a symlink")
var ErrScrubAlreadyRunning = errors.New("scrub already running")

const interruptedScrubErrorMessage = "scrub interrupted before completion"

var syncHistoryFileDir = syncHistoryDir
var syncHistoryFileRootDir = syncHistoryRootDir
var writeHistoryStoreFile = writeHistoryFile
var afterValidateHistoryFilePath = func() {}

var historyFileDirRootsMu sync.RWMutex
var historyFileDirRoots = map[string]*os.Root{}

const historyRootEscapeError = "path escapes from parent"
const maxHistoryTempAttempts = 32

type historyPersistenceWarningError struct {
	err error
}

func (e *historyPersistenceWarningError) Error() string {
	return e.err.Error()
}

func (e *historyPersistenceWarningError) Unwrap() error {
	return e.err
}

func wrapHistoryPersistenceWarning(err error) error {
	if err == nil {
		return nil
	}
	var warning *historyPersistenceWarningError
	if errors.As(err, &warning) {
		return err
	}
	return &historyPersistenceWarningError{err: err}
}

func isHistoryPersistenceWarning(err error) bool {
	var warning *historyPersistenceWarningError
	return errors.As(err, &warning)
}

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
	normalizedHistoryPath, err := ensureHistoryDirRoot(filepath.Join(dataDir, "last_scrub.json"), true)
	if err != nil {
		return nil, err
	}
	store := &HistoryStore{
		dataDir: filepath.Dir(normalizedHistoryPath),
	}
	// Load last scrub result if exists
	if err := store.loadLastScrubResult(); err != nil && !os.IsNotExist(err) {
		if recoverErr := store.recoverCorruptLastScrubResult(err); recoverErr != nil {
			return nil, errors.Join(
				fmt.Errorf("load last scrub result: %w", err),
				fmt.Errorf("recover corrupt scrub history: %w", recoverErr),
			)
		}
		if recoverErr := store.restoreTerminalFallbackAfterCorruptRecovery(); recoverErr != nil {
			return nil, errors.Join(
				fmt.Errorf("load last scrub result: %w", err),
				fmt.Errorf("restore terminal scrub fallback after corrupt recovery: %w", recoverErr),
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
		if isHistoryPersistenceWarning(err) {
			return err
		}
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
		if isHistoryPersistenceWarning(err) {
			return cloneScrubResult(result), err
		}
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
			if recoverErr := s.recoverCorruptScrubTerminalFallback(fallbackErr); recoverErr != nil {
				return errors.Join(
					fmt.Errorf("load scrub terminal fallback: %w", fallbackErr),
					fmt.Errorf("recover corrupt scrub terminal fallback: %w", recoverErr),
				)
			}
		}
	} else if fallback != nil {
		result = fallback
		if err := s.persistScrubResult(result); err != nil && !isHistoryPersistenceWarning(err) {
			return fmt.Errorf("persist restored scrub fallback: %w", err)
		}
	}

	if recoverInterruptedScrubResult(result, time.Now()) {
		if err := s.persistScrubResult(result); err != nil {
			if isHistoryPersistenceWarning(err) {
				s.scrubResult = cloneScrubResult(result)
				return nil
			}
			return fmt.Errorf("persist recovered scrub result: %w", err)
		}
	}
	s.scrubResult = cloneScrubResult(result)
	return nil
}

func (s *HistoryStore) recoverCorruptLastScrubResult(loadErr error) error {
	if err := recoverCorruptHistoryFile(s.lastScrubPath(), loadErr); err != nil {
		return err
	}
	s.scrubResult = nil
	return nil
}

func (s *HistoryStore) recoverCorruptScrubTerminalFallback(loadErr error) error {
	return recoverCorruptHistoryFile(s.lastScrubTerminalFallbackPath(), loadErr)
}

func (s *HistoryStore) restoreTerminalFallbackAfterCorruptRecovery() error {
	fallback, err := loadScrubResultFile(s.lastScrubTerminalFallbackPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("load scrub terminal fallback: %w", err)
	}
	if fallback == nil || fallback.Status == "running" {
		return nil
	}

	if err := s.persistScrubResult(fallback); err != nil {
		if isHistoryPersistenceWarning(err) {
			s.scrubResult = cloneScrubResult(fallback)
			return nil
		}
		return fmt.Errorf("persist scrub terminal fallback: %w", err)
	}
	s.scrubResult = cloneScrubResult(fallback)
	return nil
}

func recoverCorruptHistoryFile(path string, loadErr error) error {
	if !isRecoverableHistoryLoadError(loadErr) {
		return loadErr
	}

	corruptPath := fmt.Sprintf("%s.corrupt.%d", path, time.Now().UnixNano())
	if err := renameRegisteredHistoryFile(path, corruptPath); err != nil {
		return fmt.Errorf("backup corrupt scrub history: %w", err)
	}
	if err := syncRegisteredHistoryDir(path); err != nil {
		if rollbackErr := renameRegisteredHistoryFile(corruptPath, path); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt scrub history directory: %w", err),
				fmt.Errorf("rollback corrupt scrub history backup: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncRegisteredHistoryDir(path); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt scrub history directory: %w", err),
				fmt.Errorf("sync corrupt scrub history rollback: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("sync corrupt scrub history directory: %w", err)
	}

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
	data, err := readRegisteredHistoryFile(path)
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

func normalizeHistoryFilePath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve maintenance history file path: %w", err)
	}
	return absPath, nil
}

func validateHistoryFilePath(path string) error {
	cleaned, err := normalizeHistoryFilePath(path)
	if err != nil {
		return err
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

func ensureHistoryDirRoot(path string, create bool) (string, error) {
	normalizedPath, _, err := ensureHistoryDirRootWithState(path, create)
	return normalizedPath, err
}

func ensureHistoryDirRootWithState(path string, create bool) (string, *os.Root, error) {
	normalizedPath, err := normalizeHistoryFilePath(path)
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Dir(normalizedPath)

	historyFileDirRootsMu.RLock()
	root := historyFileDirRoots[dir]
	historyFileDirRootsMu.RUnlock()
	if root != nil {
		return normalizedPath, nil, nil
	}

	if err := validateHistoryFilePath(normalizedPath); err != nil {
		return "", nil, err
	}

	if create {
		if err := ensureHistoryDir(dir, 0755); err != nil {
			return "", nil, err
		}
	} else if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return normalizedPath, nil, nil
		}
		return "", nil, err
	}

	root, err = os.OpenRoot(dir)
	if err != nil {
		return "", nil, mapHistoryRootPathError(err)
	}

	historyFileDirRootsMu.Lock()
	if existing := historyFileDirRoots[dir]; existing != nil {
		historyFileDirRootsMu.Unlock()
		_ = root.Close()
		return normalizedPath, nil, nil
	}
	historyFileDirRoots[dir] = root
	historyFileDirRootsMu.Unlock()

	return normalizedPath, root, nil
}

func registeredHistoryDirRoot(path string) (*os.Root, string, bool, error) {
	normalizedPath, err := normalizeHistoryFilePath(path)
	if err != nil {
		return nil, "", false, err
	}
	dir := filepath.Dir(normalizedPath)
	historyFileDirRootsMu.RLock()
	root := historyFileDirRoots[dir]
	historyFileDirRootsMu.RUnlock()
	return root, normalizedPath, root != nil, nil
}

func readRegisteredHistoryFile(path string) ([]byte, error) {
	root, normalizedPath, ok, err := registeredHistoryDirRoot(path)
	if err != nil {
		return nil, err
	}
	if !ok {
		normalizedPath, _, err = ensureHistoryDirRootWithState(normalizedPath, false)
		if err != nil {
			return nil, err
		}
		root, normalizedPath, ok, err = registeredHistoryDirRoot(normalizedPath)
		if err != nil {
			return nil, err
		}
		if !ok {
			if err := validateHistoryFilePath(normalizedPath); err != nil {
				return nil, err
			}
			return nil, &os.PathError{Op: "open", Path: normalizedPath, Err: os.ErrNotExist}
		}
	}
	return readHistoryFileWithRoot(root, normalizedPath)
}

func writeRegisteredHistoryFileAtomically(path string, data []byte) error {
	root, normalizedPath, ok, err := registeredHistoryDirRoot(path)
	if err != nil {
		return err
	}
	if !ok {
		normalizedPath, _, err = ensureHistoryDirRootWithState(normalizedPath, true)
		if err != nil {
			return err
		}
		return writeRegisteredHistoryFileAtomically(normalizedPath, data)
	}
	return writeHistoryFileAtomicallyWithRoot(root, normalizedPath, data)
}

func renameRegisteredHistoryFile(oldPath, newPath string) error {
	root, normalizedOldPath, ok, err := registeredHistoryDirRoot(oldPath)
	if err != nil {
		return err
	}
	normalizedNewPath, err := normalizeHistoryFilePath(newPath)
	if err != nil {
		return err
	}
	if filepath.Dir(normalizedOldPath) != filepath.Dir(normalizedNewPath) {
		return fmt.Errorf("maintenance history rename requires same parent directory")
	}
	if ok {
		afterValidateHistoryFilePath()
		if err := root.Rename(filepath.Base(normalizedOldPath), filepath.Base(normalizedNewPath)); err != nil {
			return mapHistoryRootPathError(err)
		}
		return nil
	}
	if err := validateHistoryFilePath(normalizedOldPath); err != nil {
		return err
	}
	if err := validateHistoryFilePath(normalizedNewPath); err != nil {
		return err
	}
	normalizedOldPath, _, err = ensureHistoryDirRootWithState(normalizedOldPath, false)
	if err != nil {
		return err
	}
	root, normalizedOldPath, ok, err = registeredHistoryDirRoot(normalizedOldPath)
	if err != nil {
		return err
	}
	if !ok {
		return &os.PathError{Op: "rename", Path: normalizedOldPath, Err: os.ErrNotExist}
	}
	afterValidateHistoryFilePath()
	if err := root.Rename(filepath.Base(normalizedOldPath), filepath.Base(normalizedNewPath)); err != nil {
		return mapHistoryRootPathError(err)
	}
	return nil
}

func syncRegisteredHistoryDir(path string) error {
	root, normalizedPath, ok, err := registeredHistoryDirRoot(path)
	if err != nil {
		return err
	}
	if ok {
		return syncHistoryFileRootDir(root)
	}
	return syncHistoryFileDir(filepath.Dir(normalizedPath))
}

func readHistoryFileWithRoot(root *os.Root, path string) ([]byte, error) {
	afterValidateHistoryFilePath()

	file, err := rootio.OpenFileNoFollow(root, filepath.Base(path), os.O_RDONLY, 0)
	if err != nil {
		return nil, mapHistoryRootPathError(err)
	}
	defer file.Close()

	return io.ReadAll(file)
}

func writeHistoryFileAtomicallyWithRoot(root *os.Root, path string, data []byte) error {
	afterValidateHistoryFilePath()

	tmpFile, tmpName, err := createHistoryTempFile(root, ".last-scrub-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp maintenance history file: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = root.Remove(tmpName)
		}
	}()

	if err := tmpFile.Chmod(0644); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to set maintenance history file permissions: %w", cleanupHistoryTempPath(root, tmpName, err))
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write maintenance history file: %w", cleanupHistoryTempPath(root, tmpName, err))
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to sync maintenance history file: %w", cleanupHistoryTempPath(root, tmpName, err))
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp maintenance history file: %w", cleanupHistoryTempPath(root, tmpName, err))
	}
	if err := root.Rename(tmpName, filepath.Base(path)); err != nil {
		return fmt.Errorf("failed to replace maintenance history file: %w", cleanupHistoryTempPath(root, tmpName, mapHistoryRootPathError(err)))
	}
	cleanup = false
	if err := syncRegisteredHistoryDir(path); err != nil {
		return wrapHistoryPersistenceWarning(fmt.Errorf("failed to sync maintenance history directory: %w", err))
	}
	return nil
}

func newHistoryTempName(pattern string) (string, error) {
	randomPart := make([]byte, 8)
	if _, err := rand.Read(randomPart); err != nil {
		return "", err
	}
	name := hex.EncodeToString(randomPart)
	if strings.Contains(pattern, "*") {
		return strings.Replace(pattern, "*", name, 1), nil
	}
	return pattern + name, nil
}

func createHistoryTempFile(root *os.Root, pattern string) (*os.File, string, error) {
	for range maxHistoryTempAttempts {
		tmpName, err := newHistoryTempName(pattern)
		if err != nil {
			return nil, "", err
		}
		tmpFile, err := rootio.OpenFileNoFollow(root, tmpName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			return tmpFile, tmpName, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return nil, "", mapHistoryRootPathError(err)
	}

	return nil, "", errors.New("failed to allocate unique maintenance history temp file")
}

func cleanupHistoryTempPath(root *os.Root, tmpPath string, operationErr error) error {
	if removeErr := root.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return errors.Join(operationErr, fmt.Errorf("cleanup temp maintenance history file %s: %w", tmpPath, removeErr))
	}
	return operationErr
}

func syncHistoryRootDir(root *os.Root) error {
	dirHandle, err := root.Open(".")
	if err != nil {
		return mapHistoryRootPathError(err)
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func isHistoryRootEscapeError(err error) bool {
	return err != nil && strings.Contains(err.Error(), historyRootEscapeError)
}

func mapHistoryRootPathError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ELOOP) || rootio.IsSymlinkError(err) || isHistoryRootEscapeError(err) {
		return errHistoryFileSymlink
	}
	return err
}

func writeHistoryFile(path string, data []byte) error {
	normalizedPath, err := ensureHistoryDirRoot(path, true)
	if err != nil {
		return err
	}
	return writeRegisteredHistoryFileAtomically(normalizedPath, data)
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
	createdDirs, err := rootio.MkdirAllPathNoFollowTracked(dir, perm)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return errHistoryFileSymlink
		}
		return err
	}
	return syncCreatedHistoryDirs(createdDirs)
}

func syncHistoryDir(dir string) error {
	dirHandle, err := rootio.OpenDirPathNoFollow(dir)
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
