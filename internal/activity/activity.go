// Package activity provides activity logging and audit trail functionality
package activity

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
	"time"
)

var errActivityLogSymlink = errors.New("activity log path must not be a symlink")

const maxActivityIDAttempts = 4

// ActionType represents the type of activity
type ActionType string

const (
	ActionUpload       ActionType = "upload"
	ActionDownload     ActionType = "download"
	ActionDelete       ActionType = "delete"
	ActionRename       ActionType = "rename"
	ActionMove         ActionType = "move"
	ActionCopy         ActionType = "copy"
	ActionCreate       ActionType = "create"
	ActionRestore      ActionType = "restore"
	ActionShare        ActionType = "share"
	ActionUnshare      ActionType = "unshare"
	ActionLogin        ActionType = "login"
	ActionLogout       ActionType = "logout"
	ActionTrashRestore ActionType = "trash_restore"
	ActionTrashDelete  ActionType = "trash_delete"
	ActionTrashEmpty   ActionType = "trash_empty"
)

// Entry represents a single activity log entry
type Entry struct {
	ID        string            `json:"id"`
	Timestamp time.Time         `json:"timestamp"`
	Action    ActionType        `json:"action"`
	Path      string            `json:"path,omitempty"`
	User      string            `json:"user,omitempty"`
	IP        string            `json:"ip,omitempty"`
	Details   map[string]string `json:"details,omitempty"`
}

// Store manages activity log storage
type Store struct {
	root    string
	entries []Entry
	mu      sync.RWMutex
	writeMu sync.Mutex
	maxSize int // Maximum number of entries to keep in memory
}

var activityLogWriter = writeActivityLogFile
var syncActivityLogDir = syncActivityDir
var activityRandomRead = rand.Read
var activityIDGenerator = generateID
var activityTimeNow = time.Now

func copyDetails(details map[string]string) map[string]string {
	if details == nil {
		return nil
	}
	clone := make(map[string]string, len(details))
	for key, value := range details {
		clone[key] = value
	}
	return clone
}

func copyEntry(entry Entry) Entry {
	clone := entry
	clone.Details = copyDetails(entry.Details)
	return clone
}

// NewStore creates a new activity store
func NewStore(root string) (*Store, error) {
	if err := ensureActivityDir(root, 0750); err != nil {
		return nil, fmt.Errorf("create activity dir: %w", err)
	}

	s := &Store{
		root:    root,
		entries: make([]Entry, 0),
		maxSize: 10000, // Keep last 10000 entries in memory
	}

	// Load existing entries
	if err := s.load(); err != nil {
		if recoverErr := s.recoverCorruptLog(err); recoverErr != nil {
			return nil, errors.Join(
				fmt.Errorf("load activity log: %w", err),
				fmt.Errorf("recover corrupt activity log: %w", recoverErr),
			)
		}
	}

	return s, nil
}

// logFilePath returns the path to the current log file
func (s *Store) logFilePath() string {
	return filepath.Join(s.root, "activity.json")
}

// load reads entries from disk
func (s *Store) load() error {
	if err := validateActivityLogPath(s.logFilePath()); err != nil {
		return err
	}
	data, err := os.ReadFile(s.logFilePath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}

	s.entries = entries
	return nil
}

func (s *Store) recoverCorruptLog(loadErr error) error {
	if !isRecoverableActivityLogError(loadErr) {
		return loadErr
	}

	dir := filepath.Dir(s.logFilePath())
	corruptPath := fmt.Sprintf("%s.corrupt.%d", s.logFilePath(), time.Now().UnixNano())
	if err := os.Rename(s.logFilePath(), corruptPath); err != nil {
		return fmt.Errorf("backup corrupt activity log: %w", err)
	}
	if err := syncActivityLogDir(dir); err != nil {
		if rollbackErr := os.Rename(corruptPath, s.logFilePath()); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt activity log directory: %w", err),
				fmt.Errorf("rollback corrupt activity log backup: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncActivityLogDir(dir); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt activity log directory: %w", err),
				fmt.Errorf("sync corrupt activity log rollback: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("sync corrupt activity log directory: %w", err)
	}

	s.entries = make([]Entry, 0)
	return nil
}

func isRecoverableActivityLogError(err error) bool {
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

// save writes entries to disk
func saveEntries(path string, entries []Entry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return activityLogWriter(path, data)
}

func cloneEntries(entries []Entry) []Entry {
	return append([]Entry(nil), entries...)
}

func (s *Store) updateEntries(mutator func([]Entry) ([]Entry, error)) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.RLock()
	currentEntries := cloneEntries(s.entries)
	logPath := s.logFilePath()
	s.mu.RUnlock()

	nextEntries, err := mutator(currentEntries)
	if err != nil {
		return err
	}
	if err := saveEntries(logPath, nextEntries); err != nil {
		return err
	}

	s.mu.Lock()
	s.entries = nextEntries
	s.mu.Unlock()
	return nil
}

func validateActivityLogPath(path string) error {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		absPath, err := filepath.Abs(cleaned)
		if err != nil {
			return fmt.Errorf("failed to resolve activity log path: %w", err)
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
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errActivityLogSymlink
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
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errActivityLogSymlink
		}
	}
	return nil
}

func writeActivityLogFile(path string, data []byte) error {
	if err := validateActivityLogPath(path); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := ensureActivityDir(dir, 0750); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, ".activity-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := f.Chmod(0640); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	if err := syncActivityLogDir(dir); err != nil {
		return fmt.Errorf("failed to sync activity log directory: %w", err)
	}
	return nil
}

func collectMissingActivityDirs(dir string) ([]string, error) {
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

func syncCreatedActivityDirs(createdDirs []string) error {
	for i := len(createdDirs) - 1; i >= 0; i-- {
		if err := syncActivityLogDir(filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync activity directory tree: %w", err)
		}
	}
	return nil
}

func ensureActivityDir(dir string, perm os.FileMode) error {
	createdDirs, err := collectMissingActivityDirs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	return syncCreatedActivityDirs(createdDirs)
}

func syncActivityDir(dir string) error {
	dirHandle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := activityRandomRead(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func generateUniqueActivityID(entries []Entry) (string, error) {
	existing := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		existing[entry.ID] = struct{}{}
	}

	for attempt := 0; attempt < maxActivityIDAttempts; attempt++ {
		id, err := activityIDGenerator()
		if err != nil {
			return "", fmt.Errorf("generate activity ID: %w", err)
		}
		if _, ok := existing[id]; !ok {
			return id, nil
		}
	}

	return "", errors.New("generate unique activity ID: collision limit exceeded")
}

// Log records a new activity entry
func (s *Store) Log(action ActionType, path, user, ip string, details map[string]string) error {
	return s.updateEntries(func(entries []Entry) ([]Entry, error) {
		id, err := generateUniqueActivityID(entries)
		if err != nil {
			return nil, err
		}
		entry := Entry{
			ID:        id,
			Timestamp: time.Now(),
			Action:    action,
			Path:      path,
			User:      user,
			IP:        ip,
			Details:   copyDetails(details),
		}

		nextEntries := make([]Entry, 0, len(entries)+1)
		nextEntries = append(nextEntries, entry)
		nextEntries = append(nextEntries, entries...)
		if len(nextEntries) > s.maxSize {
			nextEntries = nextEntries[:s.maxSize]
		}
		return nextEntries, nil
	})
}

// List returns recent activity entries
func (s *Store) List(limit, offset int, actionFilter ActionType, userFilter string) ([]Entry, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Filter entries
	var filtered []Entry
	for _, e := range s.entries {
		if actionFilter != "" && e.Action != actionFilter {
			continue
		}
		if userFilter != "" && e.User != userFilter {
			continue
		}
		filtered = append(filtered, copyEntry(e))
	}

	total := len(filtered)

	// Apply pagination
	if offset >= len(filtered) {
		return []Entry{}, total
	}

	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}

	return filtered[offset:end], total
}

// Count returns the total number of entries
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Clear removes all entries
func (s *Store) Clear() error {
	return s.updateEntries(func([]Entry) ([]Entry, error) {
		return make([]Entry, 0), nil
	})
}

// GetByID returns a specific entry by ID
func (s *Store) GetByID(id string) (*Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, e := range s.entries {
		if e.ID == id {
			entry := copyEntry(e)
			return &entry, nil
		}
	}
	return nil, fmt.Errorf("entry not found: %s", id)
}

// Statistics returns activity statistics
func (s *Store) Statistics() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[string]interface{})
	stats["total"] = len(s.entries)

	// Count by action type
	actionCounts := make(map[ActionType]int)
	userCounts := make(map[string]int)

	for _, e := range s.entries {
		actionCounts[e.Action]++
		if e.User != "" {
			userCounts[e.User]++
		}
	}

	stats["by_action"] = actionCounts
	stats["by_user"] = userCounts

	// Today's activity
	today := startOfLocalDay(activityTimeNow())
	todayCount := 0
	for _, e := range s.entries {
		if !e.Timestamp.Before(today) {
			todayCount++
		}
	}
	stats["today"] = todayCount

	return stats
}

func startOfLocalDay(now time.Time) time.Time {
	year, month, day := now.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, now.Location())
}
