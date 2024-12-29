// Package favorites provides file favorites functionality for MnemoNAS
package favorites

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var (
	ErrFavoriteNotFound = errors.New("favorite not found")
	ErrAlreadyFavorited = errors.New("already favorited")
)

// Favorite represents a favorited file or folder
type Favorite struct {
	Path      string    `json:"path"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	Note      string    `json:"note,omitempty"`
}

// Store manages favorites persistence
type Store struct {
	mu sync.RWMutex
	// map[userID]map[path]*Favorite
	data     map[string]map[string]*Favorite
	filePath string
}

// NewStore creates a new favorites store
func NewStore(filePath string) (*Store, error) {
	store := &Store{
		data:     make(map[string]map[string]*Favorite),
		filePath: filePath,
	}

	if err := store.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load favorites: %w", err)
	}

	return store, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}

	var favorites []*Favorite
	if err := json.Unmarshal(data, &favorites); err != nil {
		return fmt.Errorf("failed to parse favorites file: %w", err)
	}

	s.data = make(map[string]map[string]*Favorite)
	for _, fav := range favorites {
		if s.data[fav.UserID] == nil {
			s.data[fav.UserID] = make(map[string]*Favorite)
		}
		s.data[fav.UserID][fav.Path] = fav
	}

	return nil
}

func (s *Store) save() error {
	var favorites []*Favorite
	for _, userFavs := range s.data {
		for _, fav := range userFavs {
			favorites = append(favorites, fav)
		}
	}

	data, err := json.MarshalIndent(favorites, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize favorites: %w", err)
	}

	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	tmpFile := s.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpFile, s.filePath); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("failed to rename file: %w", err)
	}

	return nil
}

// Add adds a path to favorites
func (s *Store) Add(userID, path, note string) (*Favorite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data[userID] == nil {
		s.data[userID] = make(map[string]*Favorite)
	}

	if _, exists := s.data[userID][path]; exists {
		return nil, ErrAlreadyFavorited
	}

	fav := &Favorite{
		Path:      path,
		UserID:    userID,
		CreatedAt: time.Now(),
		Note:      note,
	}

	s.data[userID][path] = fav

	if err := s.save(); err != nil {
		delete(s.data[userID], path)
		return nil, err
	}

	return fav, nil
}

// Remove removes a path from favorites
func (s *Store) Remove(userID, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data[userID] == nil {
		return ErrFavoriteNotFound
	}

	if _, exists := s.data[userID][path]; !exists {
		return ErrFavoriteNotFound
	}

	delete(s.data[userID], path)

	return s.save()
}

// List returns all favorites for a user, sorted by creation time (newest first)
func (s *Store) List(userID string) []*Favorite {
	s.mu.RLock()
	defer s.mu.RUnlock()

	userFavs := s.data[userID]
	if userFavs == nil {
		return []*Favorite{}
	}

	favorites := make([]*Favorite, 0, len(userFavs))
	for _, fav := range userFavs {
		favorites = append(favorites, fav)
	}

	// Sort by creation time, newest first
	sort.Slice(favorites, func(i, j int) bool {
		return favorites[i].CreatedAt.After(favorites[j].CreatedAt)
	})

	return favorites
}

// IsFavorite checks if a path is favorited by a user
func (s *Store) IsFavorite(userID, path string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data[userID] == nil {
		return false
	}
	_, exists := s.data[userID][path]
	return exists
}

// CheckPaths checks which paths are favorited from a list
func (s *Store) CheckPaths(userID string, paths []string) map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]bool, len(paths))
	userFavs := s.data[userID]

	for _, path := range paths {
		if userFavs != nil {
			_, exists := userFavs[path]
			result[path] = exists
		} else {
			result[path] = false
		}
	}

	return result
}

// UpdateNote updates the note for a favorite
func (s *Store) UpdateNote(userID, path, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data[userID] == nil {
		return ErrFavoriteNotFound
	}

	fav, exists := s.data[userID][path]
	if !exists {
		return ErrFavoriteNotFound
	}

	fav.Note = note

	return s.save()
}

// Count returns the number of favorites for a user
func (s *Store) Count(userID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data[userID] == nil {
		return 0
	}
	return len(s.data[userID])
}
