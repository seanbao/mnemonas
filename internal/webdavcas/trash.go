// Package webdavcas provides WebDAV to CAS storage adapter layer
package webdavcas

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// TrashItem represents a deleted file/directory in the trash
type TrashItem struct {
	ID           string    `json:"id"`            // Unique ID for the trash item
	OriginalPath string    `json:"original_path"` // Original file path before deletion
	DeletedAt    time.Time `json:"deleted_at"`    // When the file was deleted
	FileInfo     FileInfo  `json:"file_info"`     // Complete file info (including versions)
}

// TrashStore manages the trash/recycle bin
type TrashStore struct {
	root string // Root directory for trash metadata
}

// TrashRetentionDays is the default retention period for trash items
const TrashRetentionDays = 30

// NewTrashStore creates a new trash store
func NewTrashStore(root string) (*TrashStore, error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, fmt.Errorf("failed to create trash directory: %w", err)
	}
	return &TrashStore{root: root}, nil
}

// generateID generates a unique ID for a trash item
func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (t *TrashStore) itemPath(id string) string {
	return path.Join(t.root, id+".json")
}

// Add adds a file to the trash
func (t *TrashStore) Add(originalPath string, info *FileInfo) (*TrashItem, error) {
	item := &TrashItem{
		ID:           generateID(),
		OriginalPath: originalPath,
		DeletedAt:    time.Now(),
		FileInfo:     *info,
	}

	data, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal trash item: %w", err)
	}

	// Atomic write
	itemPath := t.itemPath(item.ID)
	tmpPath := itemPath + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create trash file: %w", err)
	}

	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()

	if writeErr != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to write trash item: %w", writeErr)
	}
	if syncErr != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to sync trash item: %w", syncErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to close trash file: %w", closeErr)
	}

	if err := os.Rename(tmpPath, itemPath); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to rename trash file: %w", err)
	}

	return item, nil
}

// Get retrieves a trash item by ID
func (t *TrashStore) Get(id string) (*TrashItem, error) {
	data, err := os.ReadFile(t.itemPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("trash item not found: %s", id)
		}
		return nil, err
	}

	var item TrashItem
	if err := json.Unmarshal(data, &item); err != nil {
		return nil, fmt.Errorf("failed to parse trash item: %w", err)
	}

	return &item, nil
}

// List lists all items in the trash, sorted by deletion time (newest first)
func (t *TrashStore) List() ([]*TrashItem, error) {
	entries, err := os.ReadDir(t.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []*TrashItem{}, nil
		}
		return nil, err
	}

	var items []*TrashItem
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		// Skip temp files
		if strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".json")
		item, err := t.Get(id)
		if err != nil {
			continue // Skip corrupted items
		}
		items = append(items, item)
	}

	// Sort by deletion time (newest first)
	sort.Slice(items, func(i, j int) bool {
		return items[i].DeletedAt.After(items[j].DeletedAt)
	})

	return items, nil
}

// Remove permanently removes a trash item (metadata only, CAS cleanup via GC)
func (t *TrashStore) Remove(id string) error {
	itemPath := t.itemPath(id)
	if err := os.Remove(itemPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("trash item not found: %s", id)
		}
		return err
	}
	return nil
}

// Clear removes all items from the trash
func (t *TrashStore) Clear() (int, error) {
	items, err := t.List()
	if err != nil {
		return 0, err
	}

	count := 0
	for _, item := range items {
		if err := t.Remove(item.ID); err == nil {
			count++
		}
	}

	return count, nil
}

// CleanupExpired removes items older than the retention period
func (t *TrashStore) CleanupExpired(retentionDays int) (int, error) {
	items, err := t.List()
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	count := 0

	for _, item := range items {
		if item.DeletedAt.Before(cutoff) {
			if err := t.Remove(item.ID); err == nil {
				count++
			}
		}
	}

	return count, nil
}

// Count returns the number of items in the trash
func (t *TrashStore) Count() (int, error) {
	items, err := t.List()
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

// TotalSize calculates the total size of all items in the trash
func (t *TrashStore) TotalSize() (int64, error) {
	items, err := t.List()
	if err != nil {
		return 0, err
	}

	var total int64
	for _, item := range items {
		total += item.FileInfo.Size
	}
	return total, nil
}
