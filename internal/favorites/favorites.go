// Package favorites provides file favorites functionality for MnemoNAS
package favorites

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrFavoriteNotFound      = errors.New("favorite not found")
	ErrAlreadyFavorited      = errors.New("already favorited")
	errInvalidFavoritePath   = errors.New("invalid favorite path")
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
	mu      sync.RWMutex
	writeMu sync.Mutex
	// map[userID]map[path]*Favorite
	data     map[string]map[string]*Favorite
	filePath string
	version  uint64
}

var favoritesStoreWriter = writeFavoritesStoreFile
var syncFavoritesStoreDir = syncFavoritesDir

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

func normalizeStoredFavoritePath(rawPath string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(rawPath), "\\", "/")
	if normalized == "" {
		return "", errInvalidFavoritePath
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return "", errInvalidFavoritePath
		}
	}
	return path.Clean("/" + normalized), nil
}

// NewStore creates a new favorites store
func NewStore(filePath string) (*Store, error) {
	store := &Store{
		data:     make(map[string]map[string]*Favorite),
		filePath: filePath,
	}

	if err := store.load(); err != nil && !os.IsNotExist(err) {
		if recoverErr := store.recoverCorruptFavorites(err); recoverErr != nil {
			return nil, errors.Join(
				fmt.Errorf("failed to load favorites: %w", err),
				fmt.Errorf("recover corrupt favorites: %w", recoverErr),
			)
		}
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
	needsRewrite := false
	for i, fav := range favorites {
		if fav == nil {
			return fmt.Errorf("favorites file contains null entry at index %d", i)
		}
		cleanPath, err := normalizeStoredFavoritePath(fav.Path)
		if err != nil {
			needsRewrite = true
			continue
		}

		normalized := copyFavorite(fav)
		if normalized.Path != cleanPath {
			needsRewrite = true
		}
		normalized.Path = cleanPath
		if s.data[normalized.UserID] == nil {
			s.data[normalized.UserID] = make(map[string]*Favorite)
		}
		if _, exists := s.data[normalized.UserID][normalized.Path]; exists {
			needsRewrite = true
		}
		s.data[normalized.UserID][normalized.Path] = normalized
	}

	if needsRewrite {
		if err := saveFavoritesState(s.filePath, s.data); err != nil {
			return fmt.Errorf("persist normalized favorites: %w", err)
		}
	}

	return nil
}

func (s *Store) recoverCorruptFavorites(loadErr error) error {
	if !isRecoverableFavoritesLoadError(loadErr) {
		return loadErr
	}

	dir := filepath.Dir(s.filePath)
	corruptPath := fmt.Sprintf("%s.corrupt.%d", s.filePath, time.Now().UnixNano())
	if err := os.Rename(s.filePath, corruptPath); err != nil {
		return fmt.Errorf("backup corrupt favorites file: %w", err)
	}
	if err := syncFavoritesStoreDir(dir); err != nil {
		if rollbackErr := os.Rename(corruptPath, s.filePath); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt favorites directory: %w", err),
				fmt.Errorf("rollback corrupt favorites backup: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncFavoritesStoreDir(dir); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt favorites directory: %w", err),
				fmt.Errorf("sync corrupt favorites rollback: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("sync corrupt favorites directory: %w", err)
	}

	s.data = make(map[string]map[string]*Favorite)
	return nil
}

func isRecoverableFavoritesLoadError(err error) bool {
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
	if err := ensureFavoritesDir(dir, 0755); err != nil {
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
	if err := syncFavoritesStoreDir(dir); err != nil {
		return fmt.Errorf("failed to sync favorites directory: %w", err)
	}

	return nil
}

func collectMissingFavoritesDirs(dir string) ([]string, error) {
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

func syncCreatedFavoritesDirs(createdDirs []string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncFavoritesStoreDir(filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync favorites directory tree: %w", err)
		}
	}
	return nil
}

func ensureFavoritesDir(dir string, perm os.FileMode) error {
	createdDirs, err := collectMissingFavoritesDirs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	return syncCreatedFavoritesDirs(createdDirs)
}

func syncFavoritesDir(dir string) error {
	dirHandle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

// Add adds a path to favorites
func (s *Store) Add(userID, path, note string) (*Favorite, error) {
	cleanPath, err := normalizeStoredFavoritePath(path)
	if err != nil {
		return nil, err
	}

	fav := &Favorite{
		Path:      cleanPath,
		UserID:    userID,
		CreatedAt: time.Now(),
		Note:      note,
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		if snapshot.data[userID] == nil {
			snapshot.data[userID] = make(map[string]*Favorite)
		}
		if _, exists := snapshot.data[userID][cleanPath]; exists {
			return nil, ErrAlreadyFavorited
		}

		snapshot.data[userID][cleanPath] = copyFavorite(fav)

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
	cleanPath, err := normalizeStoredFavoritePath(path)
	if err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		if snapshot.data[userID] == nil {
			return ErrFavoriteNotFound
		}
		if _, exists := snapshot.data[userID][cleanPath]; !exists {
			return ErrFavoriteNotFound
		}

		delete(snapshot.data[userID], cleanPath)
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
	cleanPath, err := normalizeStoredFavoritePath(path)
	if err != nil {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data[userID] == nil {
		return false
	}
	_, exists := s.data[userID][cleanPath]
	return exists
}

// CheckPaths checks which paths are favorited from a list
func (s *Store) CheckPaths(userID string, paths []string) map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]bool, len(paths))
	userFavs := s.data[userID]

	for _, rawPath := range paths {
		cleanPath, err := normalizeStoredFavoritePath(rawPath)
		if err != nil {
			result[rawPath] = false
			continue
		}
		if userFavs != nil {
			_, exists := userFavs[cleanPath]
			result[rawPath] = exists
		} else {
			result[rawPath] = false
		}
	}

	return result
}

// UpdateNote updates the note for a favorite
func (s *Store) UpdateNote(userID, path, note string) error {
	cleanPath, err := normalizeStoredFavoritePath(path)
	if err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		if snapshot.data[userID] == nil {
			return ErrFavoriteNotFound
		}

		fav, exists := snapshot.data[userID][cleanPath]
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

func favoritePathMatchesOrDescendant(basePath, candidatePath string) bool {
	basePath = path.Clean(basePath)
	candidatePath = path.Clean(candidatePath)
	if basePath == "/" {
		return strings.HasPrefix(candidatePath, "/")
	}
	return candidatePath == basePath || strings.HasPrefix(candidatePath, basePath+"/")
}

func relocateFavoritePath(currentPath, oldRoot, newRoot string) (string, bool) {
	currentPath = path.Clean(currentPath)
	oldRoot = path.Clean(oldRoot)
	newRoot = path.Clean(newRoot)
	if !favoritePathMatchesOrDescendant(oldRoot, currentPath) {
		return "", false
	}
	if currentPath == oldRoot {
		return newRoot, true
	}
	return path.Clean(newRoot + strings.TrimPrefix(currentPath, oldRoot)), true
}

// UpdatePathReferences rewrites favorite paths when a filesystem path is renamed.
func (s *Store) UpdatePathReferences(oldPath, newPath string) error {
	oldPath = path.Clean(oldPath)
	newPath = path.Clean(newPath)
	if oldPath == newPath {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		changed := false
		type pendingFavoriteRewrite struct {
			userID      string
			currentPath string
			updated     *Favorite
		}
		pendingRewrites := make([]pendingFavoriteRewrite, 0)

		for userID, userFavs := range snapshot.data {
			for currentPath, fav := range userFavs {
				updatedPath, ok := relocateFavoritePath(currentPath, oldPath, newPath)
				if !ok || updatedPath == currentPath {
					continue
				}

				updated := copyFavorite(fav)
				updated.Path = updatedPath
				pendingRewrites = append(pendingRewrites, pendingFavoriteRewrite{
					userID:      userID,
					currentPath: currentPath,
					updated:     updated,
				})
				changed = true
			}
		}

		for _, rewrite := range pendingRewrites {
			delete(snapshot.data[rewrite.userID], rewrite.currentPath)
			snapshot.data[rewrite.userID][rewrite.updated.Path] = rewrite.updated
		}

		if !changed {
			return nil
		}
		if err := saveFavoritesState(snapshot.filePath, snapshot.data); err != nil {
			return err
		}
		if s.commitSnapshot(snapshot) {
			return nil
		}
	}
}

// RemoveFavoritesUnderPath removes favorites that reference a deleted path.
func (s *Store) RemoveFavoritesUnderPath(targetPath string) error {
	_, err := s.RemoveFavoritesUnderPathWithRestore(targetPath)
	return err
}

// RemoveFavoritesUnderPathWithRestore removes favorites under a deleted path and
// returns the removed favorites for rollback if a later step fails.
func (s *Store) RemoveFavoritesUnderPathWithRestore(targetPath string) ([]*Favorite, error) {
	targetPath = path.Clean(targetPath)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		changed := false
		var removed []*Favorite

		for userID, userFavs := range snapshot.data {
			for currentPath, fav := range userFavs {
				if !favoritePathMatchesOrDescendant(targetPath, currentPath) {
					continue
				}

				removed = append(removed, copyFavorite(fav))
				delete(snapshot.data[userID], currentPath)
				changed = true
			}
			if len(snapshot.data[userID]) == 0 {
				delete(snapshot.data, userID)
			}
		}

		if !changed {
			return nil, nil
		}
		if err := saveFavoritesState(snapshot.filePath, snapshot.data); err != nil {
			return nil, err
		}
		if s.commitSnapshot(snapshot) {
			return removed, nil
		}
	}
}

// RestoreFavorites restores favorites that were removed by a failed operation.
func (s *Store) RestoreFavorites(favorites []*Favorite) error {
	if len(favorites) == 0 {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		changed := false

		for _, favorite := range favorites {
			if snapshot.data[favorite.UserID] == nil {
				snapshot.data[favorite.UserID] = make(map[string]*Favorite)
			}

			current, ok := snapshot.data[favorite.UserID][favorite.Path]
			if ok && current.Note == favorite.Note && current.CreatedAt.Equal(favorite.CreatedAt) {
				continue
			}

			snapshot.data[favorite.UserID][favorite.Path] = copyFavorite(favorite)
			changed = true
		}

		if !changed {
			return nil
		}
		if err := saveFavoritesState(snapshot.filePath, snapshot.data); err != nil {
			return err
		}
		if s.commitSnapshot(snapshot) {
			return nil
		}
	}
}
