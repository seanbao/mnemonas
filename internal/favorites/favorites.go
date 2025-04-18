// Package favorites provides file favorites functionality for MnemoNAS
package favorites

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrFavoriteNotFound      = errors.New("favorite not found")
	ErrAlreadyFavorited      = errors.New("already favorited")
	errFavoritesStoreSymlink = errors.New("favorites store path must not be a symlink")
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
	version  uint64
}

var favoritesStoreWriter = writeFavoritesStoreFile

type favoritesSnapshot struct {
	data     map[string]map[string]*Favorite
	filePath string
	version  uint64
}

func copyFavorite(fav *Favorite) *Favorite {
	if fav == nil {
		return nil
	}
	clone := *fav
	return &clone
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
	if err := validateFavoritesStorePath(s.filePath); err != nil {
		return err
	}
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
	return saveFavoritesState(s.filePath, s.data)
}

func saveFavoritesState(filePath string, dataByUser map[string]map[string]*Favorite) error {
	var favorites []*Favorite
	for _, userFavs := range dataByUser {
		for _, fav := range userFavs {
			favorites = append(favorites, copyFavorite(fav))
		}
	}

	data, err := json.MarshalIndent(favorites, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize favorites: %w", err)
	}

	if err := favoritesStoreWriter(filePath, data); err != nil {
		return err
	}

	return nil
}

func cloneFavoritesData(data map[string]map[string]*Favorite) map[string]map[string]*Favorite {
	cloned := make(map[string]map[string]*Favorite, len(data))
	for userID, userFavs := range data {
		cloned[userID] = make(map[string]*Favorite, len(userFavs))
		for path, fav := range userFavs {
			cloned[userID][path] = copyFavorite(fav)
		}
	}
	return cloned
}

func (s *Store) snapshotState() favoritesSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return favoritesSnapshot{
		data:     cloneFavoritesData(s.data),
		filePath: s.filePath,
		version:  s.version,
	}
}

func (s *Store) commitSnapshot(snapshot favoritesSnapshot) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.version != snapshot.version {
		return false
	}

	s.data = snapshot.data
	s.version++
	return true
}

func validateFavoritesStorePath(path string) error {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		absPath, err := filepath.Abs(cleaned)
		if err != nil {
			return fmt.Errorf("failed to resolve favorites store path: %w", err)
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
			return fmt.Errorf("failed to stat favorites store: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errFavoritesStoreSymlink
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
			return fmt.Errorf("failed to stat favorites store: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errFavoritesStoreSymlink
		}
	}
	return nil
}

func writeFavoritesStoreFile(path string, data []byte) error {
	if err := validateFavoritesStorePath(path); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".favorites-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp favorites file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to set temp favorites permissions: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write favorites file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to sync favorites file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp favorites file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to replace favorites file: %w", err)
	}
	cleanup = false

	return nil
}

// Add adds a path to favorites
func (s *Store) Add(userID, path, note string) (*Favorite, error) {
	fav := &Favorite{
		Path:      path,
		UserID:    userID,
		CreatedAt: time.Now(),
		Note:      note,
	}

	for {
		snapshot := s.snapshotState()
		if snapshot.data[userID] == nil {
			snapshot.data[userID] = make(map[string]*Favorite)
		}
		if _, exists := snapshot.data[userID][path]; exists {
			return nil, ErrAlreadyFavorited
		}

		snapshot.data[userID][path] = copyFavorite(fav)

		if err := saveFavoritesState(snapshot.filePath, snapshot.data); err != nil {
			return nil, err
		}
		if s.commitSnapshot(snapshot) {
			return copyFavorite(fav), nil
		}
	}
}

// Remove removes a path from favorites
func (s *Store) Remove(userID, path string) error {
	for {
		snapshot := s.snapshotState()
		if snapshot.data[userID] == nil {
			return ErrFavoriteNotFound
		}
		if _, exists := snapshot.data[userID][path]; !exists {
			return ErrFavoriteNotFound
		}

		delete(snapshot.data[userID], path)
		if len(snapshot.data[userID]) == 0 {
			delete(snapshot.data, userID)
		}

		if err := saveFavoritesState(snapshot.filePath, snapshot.data); err != nil {
			return err
		}
		if s.commitSnapshot(snapshot) {
			return nil
		}
	}
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
		favorites = append(favorites, copyFavorite(fav))
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
	for {
		snapshot := s.snapshotState()
		if snapshot.data[userID] == nil {
			return ErrFavoriteNotFound
		}

		fav, exists := snapshot.data[userID][path]
		if !exists {
			return ErrFavoriteNotFound
		}

		fav.Note = note
		if err := saveFavoritesState(snapshot.filePath, snapshot.data); err != nil {
			return err
		}
		if s.commitSnapshot(snapshot) {
			return nil
		}
	}
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
