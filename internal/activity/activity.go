// Package activity provides activity logging and audit trail functionality
package activity

import (
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
	maxSize int // Maximum number of entries to keep in memory
	version uint64
}

var activityLogWriter = writeActivityLogFile

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
	if err := os.MkdirAll(root, 0750); err != nil {
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
			return nil, fmt.Errorf("load activity log: %w", err)
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

	corruptPath := fmt.Sprintf("%s.corrupt.%d", s.logFilePath(), time.Now().UnixNano())
	if err := os.Rename(s.logFilePath(), corruptPath); err != nil {
		return fmt.Errorf("backup corrupt activity log: %w", err)
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

func (s *Store) updateEntries(mutator func([]Entry) []Entry) error {
	for {
		s.mu.RLock()
		baseVersion := s.version
		currentEntries := cloneEntries(s.entries)
		logPath := s.logFilePath()
		s.mu.RUnlock()

		nextEntries := mutator(currentEntries)
		if err := saveEntries(logPath, nextEntries); err != nil {
			return err
		}

		s.mu.Lock()
		if s.version != baseVersion {
			s.mu.Unlock()
			continue
		}
		s.entries = nextEntries
		s.version++
		s.mu.Unlock()
		return nil
	}
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
	if err := os.MkdirAll(dir, 0750); err != nil {
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
	return nil
}

// generateID creates a unique ID for an entry
func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// Log records a new activity entry
func (s *Store) Log(action ActionType, path, user, ip string, details map[string]string) error {
	entry := Entry{
		ID:        generateID(),
		Timestamp: time.Now(),
		Action:    action,
		Path:      path,
		User:      user,
		IP:        ip,
		Details:   copyDetails(details),
	}

	return s.updateEntries(func(entries []Entry) []Entry {
		nextEntries := make([]Entry, 0, len(entries)+1)
		nextEntries = append(nextEntries, entry)
		nextEntries = append(nextEntries, entries...)
		if len(nextEntries) > s.maxSize {
			nextEntries = nextEntries[:s.maxSize]
		}
		return nextEntries
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
	return s.updateEntries(func([]Entry) []Entry {
		return make([]Entry, 0)
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
	today := time.Now().Truncate(24 * time.Hour)
	todayCount := 0
	for _, e := range s.entries {
		if e.Timestamp.After(today) {
			todayCount++
		}
	}
	stats["today"] = todayCount

	return stats
}
