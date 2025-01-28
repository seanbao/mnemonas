// Package activity provides activity logging and audit trail functionality
package activity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

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
		return nil, fmt.Errorf("load activity log: %w", err)
	}

	return s, nil
}

// logFilePath returns the path to the current log file
func (s *Store) logFilePath() string {
	return filepath.Join(s.root, "activity.json")
}

// load reads entries from disk
func (s *Store) load() error {
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

// save writes entries to disk
func (s *Store) save() error {
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := s.logFilePath() + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, s.logFilePath()); err != nil {
		os.Remove(tmpPath)
		return err
	}

	return nil
}

// generateID creates a unique ID for an entry
func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// Log records a new activity entry
func (s *Store) Log(action ActionType, path, user, ip string, details map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := append([]Entry(nil), s.entries...)

	entry := Entry{
		ID:        generateID(),
		Timestamp: time.Now(),
		Action:    action,
		Path:      path,
		User:      user,
		IP:        ip,
		Details:   details,
	}

	// Prepend entry (newest first)
	s.entries = append([]Entry{entry}, s.entries...)

	// Trim to max size
	if len(s.entries) > s.maxSize {
		s.entries = s.entries[:s.maxSize]
	}

	if err := s.save(); err != nil {
		s.entries = previous
		return err
	}
	return nil
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
		filtered = append(filtered, e)
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
	s.mu.Lock()
	defer s.mu.Unlock()

	previous := append([]Entry(nil), s.entries...)
	s.entries = make([]Entry, 0)
	if err := s.save(); err != nil {
		s.entries = previous
		return err
	}
	return nil
}

// GetByID returns a specific entry by ID
func (s *Store) GetByID(id string) (*Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, e := range s.entries {
		if e.ID == id {
			return &e, nil
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
