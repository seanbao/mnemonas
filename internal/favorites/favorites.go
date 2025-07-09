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

// PersistenceWarningError reports that the favorites mutation is already
// visible on disk, but the final directory fsync did not complete.
type PersistenceWarningError struct {
	err error
}

func (e *PersistenceWarningError) Error() string {
	return e.err.Error()
}

func (e *PersistenceWarningError) Unwrap() error {
	return e.err
}

func WrapPersistenceWarning(err error) error {
	if err == nil {
		return nil
	}
	var warningErr *PersistenceWarningError
	if errors.As(err, &warningErr) {
		return err
	}
	return &PersistenceWarningError{err: err}
}

func IsPersistenceWarning(err error) bool {
	var warningErr *PersistenceWarningError
	return errors.As(err, &warningErr)
}

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
var syncFavoritesStoreRootDir = syncFavoritesRootDir
var afterValidateFavoritesStorePath = func() {}

var favoritesStoreDirRootsMu sync.RWMutex
var favoritesStoreDirRoots = map[string]*os.Root{}

const favoritesStoreRootEscapeError = "path escapes from parent"

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

func sortFavoritesCanonical(favorites []*Favorite) {
	sort.Slice(favorites, func(i, j int) bool {
		if favorites[i].UserID != favorites[j].UserID {
			return favorites[i].UserID < favorites[j].UserID
		}
		if favorites[i].Path != favorites[j].Path {
			return favorites[i].Path < favorites[j].Path
		}
		return favorites[i].CreatedAt.Before(favorites[j].CreatedAt)
	})
}

func normalizeStoredFavoritePath(rawPath string) (string, error) {
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	if strings.TrimSpace(normalized) == "" {
		return "", errInvalidFavoritePath
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return "", errInvalidFavoritePath
		}
	}
	return path.Clean("/" + normalized), nil
}

func normalizeRestoredFavorite(favorite *Favorite) (*Favorite, error) {
	normalized := copyFavorite(favorite)
	cleanPath, err := normalizeStoredFavoritePath(normalized.Path)
	if err != nil {
		return nil, err
	}
	normalized.Path = cleanPath
	return normalized, nil
}

// NewStore creates a new favorites store
func NewStore(filePath string) (*Store, error) {
	normalizedPath, err := ensureFavoritesStoreDirRoot(filePath, false)
	if err != nil {
		return nil, err
	}

	store := &Store{
		data:     make(map[string]map[string]*Favorite),
		filePath: normalizedPath,
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
	data, err := readRegisteredFavoritesStoreFile(s.filePath)
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
			if !IsPersistenceWarning(err) {
				return fmt.Errorf("persist normalized favorites: %w", err)
			}
		}
	}

	return nil
}

func (s *Store) recoverCorruptFavorites(loadErr error) error {
	if !isRecoverableFavoritesLoadError(loadErr) {
		return loadErr
	}

	corruptPath := fmt.Sprintf("%s.corrupt.%d", s.filePath, time.Now().UnixNano())
	if err := renameRegisteredFavoritesStoreFile(s.filePath, corruptPath); err != nil {
		return fmt.Errorf("backup corrupt favorites file: %w", err)
	}
	if err := syncRegisteredFavoritesStoreDir(s.filePath); err != nil {
		if rollbackErr := renameRegisteredFavoritesStoreFile(corruptPath, s.filePath); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt favorites directory: %w", err),
				fmt.Errorf("rollback corrupt favorites backup: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncRegisteredFavoritesStoreDir(s.filePath); rollbackSyncErr != nil {
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
	sortFavoritesCanonical(favorites)

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

func (s *Store) persistSnapshot(snapshot favoritesSnapshot) (bool, error) {
	err := saveFavoritesState(snapshot.filePath, snapshot.data)
	if err != nil && !IsPersistenceWarning(err) {
		return false, err
	}
	if !s.commitSnapshot(snapshot) {
		return false, nil
	}
	return true, err
}

func validateFavoritesStorePath(path string) error {
	cleaned, err := normalizeFavoritesStorePath(path)
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

func normalizeFavoritesStorePath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve favorites store path: %w", err)
	}
	return absPath, nil
}

func ensureFavoritesStoreDirRoot(path string, create bool) (string, error) {
	normalizedPath, _, err := ensureFavoritesStoreDirRootWithState(path, create)
	return normalizedPath, err
}

func ensureFavoritesStoreDirRootWithState(path string, create bool) (string, *os.Root, error) {
	normalizedPath, err := normalizeFavoritesStorePath(path)
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Dir(normalizedPath)

	favoritesStoreDirRootsMu.RLock()
	root := favoritesStoreDirRoots[dir]
	favoritesStoreDirRootsMu.RUnlock()
	if root != nil {
		return normalizedPath, nil, nil
	}

	if err := validateFavoritesStorePath(normalizedPath); err != nil {
		return "", nil, err
	}

	if create {
		if err := ensureFavoritesDir(dir, 0755); err != nil {
			return "", nil, fmt.Errorf("failed to create directory: %w", err)
		}
	} else if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return normalizedPath, nil, nil
		}
		return "", nil, fmt.Errorf("failed to stat favorites store directory: %w", err)
	}

	root, err = os.OpenRoot(dir)
	if err != nil {
		return "", nil, fmt.Errorf("failed to open favorites store directory root: %w", err)
	}

	favoritesStoreDirRootsMu.Lock()
	if existing := favoritesStoreDirRoots[dir]; existing != nil {
		favoritesStoreDirRootsMu.Unlock()
		_ = root.Close()
		return normalizedPath, nil, nil
	}
	favoritesStoreDirRoots[dir] = root
	favoritesStoreDirRootsMu.Unlock()

	return normalizedPath, root, nil
}

func releaseRegisteredFavoritesStoreDirRoot(dir string, root *os.Root) {
	if root == nil {
		return
	}
	favoritesStoreDirRootsMu.Lock()
	if favoritesStoreDirRoots[dir] == root {
		delete(favoritesStoreDirRoots, dir)
	}
	favoritesStoreDirRootsMu.Unlock()
	_ = root.Close()
}

func registeredFavoritesStoreDirRoot(path string) (*os.Root, string, bool, error) {
	normalizedPath, err := normalizeFavoritesStorePath(path)
	if err != nil {
		return nil, "", false, err
	}
	dir := filepath.Dir(normalizedPath)
	favoritesStoreDirRootsMu.RLock()
	root := favoritesStoreDirRoots[dir]
	favoritesStoreDirRootsMu.RUnlock()
	return root, normalizedPath, root != nil, nil
}

func readRegisteredFavoritesStoreFile(path string) ([]byte, error) {
	root, normalizedPath, ok, err := registeredFavoritesStoreDirRoot(path)
	if err != nil {
		return nil, err
	}
	if !ok {
		if err := validateFavoritesStorePath(normalizedPath); err != nil {
			return nil, err
		}
		return os.ReadFile(normalizedPath)
	}
	return readFavoritesStoreFileWithRoot(root, normalizedPath)
}

func writeRegisteredFavoritesStoreFileAtomically(path string, data []byte) error {
	root, normalizedPath, ok, err := registeredFavoritesStoreDirRoot(path)
	if err != nil {
		return err
	}
	if !ok {
		return writeFavoritesStoreFileAtomically(normalizedPath, data)
	}
	return writeFavoritesStoreFileAtomicallyWithRoot(root, normalizedPath, data)
}

func renameRegisteredFavoritesStoreFile(oldPath, newPath string) error {
	root, normalizedOldPath, ok, err := registeredFavoritesStoreDirRoot(oldPath)
	if err != nil {
		return err
	}
	normalizedNewPath, err := normalizeFavoritesStorePath(newPath)
	if err != nil {
		return err
	}
	if ok && filepath.Dir(normalizedOldPath) == filepath.Dir(normalizedNewPath) {
		afterValidateFavoritesStorePath()
		if err := root.Rename(filepath.Base(normalizedOldPath), filepath.Base(normalizedNewPath)); err != nil {
			return mapFavoritesRootPathError(err)
		}
		return nil
	}
	if err := validateFavoritesStorePath(normalizedOldPath); err != nil {
		return err
	}
	if err := validateFavoritesStorePath(normalizedNewPath); err != nil {
		return err
	}
	afterValidateFavoritesStorePath()
	return os.Rename(normalizedOldPath, normalizedNewPath)
}

func syncRegisteredFavoritesStoreDir(path string) error {
	root, normalizedPath, ok, err := registeredFavoritesStoreDirRoot(path)
	if err != nil {
		return err
	}
	if ok {
		return syncFavoritesStoreRootDir(root)
	}
	return syncFavoritesStoreDir(filepath.Dir(normalizedPath))
}

func readFavoritesStoreFileWithRoot(root *os.Root, path string) ([]byte, error) {
	afterValidateFavoritesStorePath()

	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return nil, mapFavoritesRootPathError(err)
	}
	defer file.Close()

	return io.ReadAll(file)
}

func writeFavoritesStoreFileAtomicallyWithRoot(root *os.Root, path string, data []byte) error {
	afterValidateFavoritesStorePath()

	tmpFile, tmpName, err := createFavoritesTempFile(root, ".favorites-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp favorites file: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = root.Remove(tmpName)
		}
	}()

	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		return cleanupFavoritesTempPath(root, tmpName, fmt.Errorf("failed to set temp favorites permissions: %w", err))
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return cleanupFavoritesTempPath(root, tmpName, fmt.Errorf("failed to write favorites file: %w", err))
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return cleanupFavoritesTempPath(root, tmpName, fmt.Errorf("failed to sync favorites file: %w", err))
	}
	if err := tmpFile.Close(); err != nil {
		return cleanupFavoritesTempPath(root, tmpName, fmt.Errorf("failed to close temp favorites file: %w", err))
	}
	if err := root.Rename(tmpName, filepath.Base(path)); err != nil {
		return cleanupFavoritesTempPath(root, tmpName, fmt.Errorf("failed to replace favorites file: %w", mapFavoritesRootPathError(err)))
	}
	cleanup = false
	if err := syncRegisteredFavoritesStoreDir(path); err != nil {
		return WrapPersistenceWarning(fmt.Errorf("failed to sync favorites directory: %w", err))
	}

	return nil
}

func newFavoritesTempName(pattern string) (string, error) {
	randomPart := fmt.Sprintf("%d", time.Now().UnixNano())
	name := strings.Replace(pattern, "*", randomPart, 1)
	if strings.Contains(pattern, "*") {
		return name, nil
	}
	return pattern + randomPart, nil
}

func createFavoritesTempFile(root *os.Root, pattern string) (*os.File, string, error) {
	for range 32 {
		tmpName, err := newFavoritesTempName(pattern)
		if err != nil {
			return nil, "", err
		}
		tmpFile, err := root.OpenFile(tmpName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			return tmpFile, tmpName, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return nil, "", mapFavoritesRootPathError(err)
	}

	return nil, "", errors.New("failed to allocate unique temp favorites file")
}

func cleanupFavoritesTempPath(root *os.Root, tmpPath string, operationErr error) error {
	if removeErr := root.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return errors.Join(operationErr, fmt.Errorf("cleanup temp favorites file %s: %w", tmpPath, removeErr))
	}
	return operationErr
}

func cleanupCreatedFavoritesDirs(createdDirs []string, operationErr error) error {
	rollbackErr := operationErr
	for _, dir := range createdDirs {
		if removeErr := os.Remove(dir); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("cleanup created favorites directory %s: %w", dir, removeErr))
			break
		}
	}
	return rollbackErr
}

func syncFavoritesRootDir(root *os.Root) error {
	dirHandle, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func isFavoritesRootEscapeError(err error) bool {
	return err != nil && strings.Contains(err.Error(), favoritesStoreRootEscapeError)
}

func mapFavoritesRootPathError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrPermission) || isFavoritesRootEscapeError(err) {
		return errFavoritesStoreSymlink
	}
	return err
}

func writeFavoritesStoreFileAtomically(path string, data []byte) error {
	if err := validateFavoritesStorePath(path); err != nil {
		return err
	}
	afterValidateFavoritesStorePath()

	dir := filepath.Dir(path)
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
		return WrapPersistenceWarning(fmt.Errorf("failed to sync favorites directory: %w", err))
	}

	return nil
}

func writeFavoritesStoreFile(path string, data []byte) error {
	_, normalizedPath, ok, err := registeredFavoritesStoreDirRoot(path)
	if err != nil {
		return err
	}
	if ok {
		return writeRegisteredFavoritesStoreFileAtomically(normalizedPath, data)
	}
	if err := validateFavoritesStorePath(normalizedPath); err != nil {
		return err
	}
	createdDirs, err := collectMissingFavoritesDirs(filepath.Dir(normalizedPath))
	if err != nil {
		return err
	}
	registeredRoot := (*os.Root)(nil)
	normalizedPath, registeredRoot, err = ensureFavoritesStoreDirRootWithState(normalizedPath, true)
	if err != nil {
		releaseRegisteredFavoritesStoreDirRoot(filepath.Dir(normalizedPath), registeredRoot)
		return cleanupCreatedFavoritesDirs(createdDirs, err)
	}
	if err := writeRegisteredFavoritesStoreFileAtomically(normalizedPath, data); err != nil {
		releaseRegisteredFavoritesStoreDirRoot(filepath.Dir(normalizedPath), registeredRoot)
		return cleanupCreatedFavoritesDirs(createdDirs, err)
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

func favoriteUserIdentifiers(userID string, extraUserIDs ...string) []string {
	identifiers := make([]string, 0, 1+len(extraUserIDs))
	appendUnique := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		for _, existing := range identifiers {
			if existing == trimmed {
				return
			}
		}
		identifiers = append(identifiers, trimmed)
	}

	appendUnique(userID)
	for _, extraUserID := range extraUserIDs {
		appendUnique(extraUserID)
	}
	return identifiers
}

func findFavoriteOwner(data map[string]map[string]*Favorite, cleanPath string, identifiers []string) (string, *Favorite) {
	for _, identifier := range identifiers {
		userFavs := data[identifier]
		if userFavs == nil {
			continue
		}
		if favorite, ok := userFavs[cleanPath]; ok {
			return identifier, favorite
		}
	}
	return "", nil
}

// Add adds a path to favorites
func (s *Store) Add(userID, path, note string, extraUserIDs ...string) (*Favorite, error) {
	cleanPath, err := normalizeStoredFavoritePath(path)
	if err != nil {
		return nil, err
	}
	identifiers := favoriteUserIdentifiers(userID, extraUserIDs...)
	primaryUserID := ""
	if len(identifiers) > 0 {
		primaryUserID = identifiers[0]
	}

	fav := &Favorite{
		Path:      cleanPath,
		UserID:    primaryUserID,
		CreatedAt: time.Now(),
		Note:      note,
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		if primaryUserID == "" {
			return nil, ErrAlreadyFavorited
		}
		if _, existing := findFavoriteOwner(snapshot.data, cleanPath, identifiers); existing != nil {
			return nil, ErrAlreadyFavorited
		}
		if snapshot.data[primaryUserID] == nil {
			snapshot.data[primaryUserID] = make(map[string]*Favorite)
		}

		snapshot.data[primaryUserID][cleanPath] = copyFavorite(fav)
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return copyFavorite(fav), err
		}
		if err != nil {
			return nil, err
		}
	}
}

// Remove removes a path from favorites
func (s *Store) Remove(userID, path string, extraUserIDs ...string) error {
	cleanPath, err := normalizeStoredFavoritePath(path)
	if err != nil {
		return err
	}
	identifiers := favoriteUserIdentifiers(userID, extraUserIDs...)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		ownerID, _ := findFavoriteOwner(snapshot.data, cleanPath, identifiers)
		if ownerID == "" {
			return ErrFavoriteNotFound
		}

		delete(snapshot.data[ownerID], cleanPath)
		if len(snapshot.data[ownerID]) == 0 {
			delete(snapshot.data, ownerID)
		}
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
		}
	}
}

// List returns all favorites for a user, sorted by creation time (newest first)
func (s *Store) List(userID string, extraUserIDs ...string) []*Favorite {
	s.mu.RLock()
	defer s.mu.RUnlock()
	identifiers := favoriteUserIdentifiers(userID, extraUserIDs...)
	if len(identifiers) == 0 {
		return []*Favorite{}
	}
	primaryUserID := identifiers[0]

	favoritesByPath := make(map[string]*Favorite)
	for _, identifier := range identifiers {
		for _, fav := range s.data[identifier] {
			if _, exists := favoritesByPath[fav.Path]; exists {
				continue
			}
			cloned := copyFavorite(fav)
			if primaryUserID != "" {
				cloned.UserID = primaryUserID
			}
			favoritesByPath[cloned.Path] = cloned
		}
	}

	favorites := make([]*Favorite, 0, len(favoritesByPath))
	for _, fav := range favoritesByPath {
		favorites = append(favorites, fav)
	}

	// Sort by creation time, newest first
	sort.Slice(favorites, func(i, j int) bool {
		if favorites[i].CreatedAt.Equal(favorites[j].CreatedAt) {
			return favorites[i].Path < favorites[j].Path
		}
		return favorites[i].CreatedAt.After(favorites[j].CreatedAt)
	})

	return favorites
}

// IsFavorite checks if a path is favorited by a user
func (s *Store) IsFavorite(userID, path string, extraUserIDs ...string) bool {
	cleanPath, err := normalizeStoredFavoritePath(path)
	if err != nil {
		return false
	}
	identifiers := favoriteUserIdentifiers(userID, extraUserIDs...)

	s.mu.RLock()
	defer s.mu.RUnlock()

	ownerID, _ := findFavoriteOwner(s.data, cleanPath, identifiers)
	return ownerID != ""
}

// CheckPaths checks which paths are favorited from a list
func (s *Store) CheckPaths(userID string, paths []string, extraUserIDs ...string) map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	identifiers := favoriteUserIdentifiers(userID, extraUserIDs...)

	result := make(map[string]bool, len(paths))

	for _, rawPath := range paths {
		cleanPath, err := normalizeStoredFavoritePath(rawPath)
		if err != nil {
			result[rawPath] = false
			continue
		}
		ownerID, _ := findFavoriteOwner(s.data, cleanPath, identifiers)
		result[rawPath] = ownerID != ""
	}

	return result
}

// UpdateNote updates the note for a favorite
func (s *Store) UpdateNote(userID, path, note string, extraUserIDs ...string) error {
	cleanPath, err := normalizeStoredFavoritePath(path)
	if err != nil {
		return err
	}
	identifiers := favoriteUserIdentifiers(userID, extraUserIDs...)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		_, fav := findFavoriteOwner(snapshot.data, cleanPath, identifiers)
		if fav == nil {
			return ErrFavoriteNotFound
		}

		fav.Note = note
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
		}
	}
}

// Count returns the number of favorites for a user
func (s *Store) Count(userID string, extraUserIDs ...string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	identifiers := favoriteUserIdentifiers(userID, extraUserIDs...)
	if len(identifiers) == 0 {
		return 0
	}

	seenPaths := make(map[string]struct{})
	for _, identifier := range identifiers {
		for path := range s.data[identifier] {
			seenPaths[path] = struct{}{}
		}
	}
	return len(seenPaths)
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
	var err error
	oldPath, err = normalizeStoredFavoritePath(oldPath)
	if err != nil {
		return err
	}
	newPath, err = normalizeStoredFavoritePath(newPath)
	if err != nil {
		return err
	}
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
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
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
	var err error
	targetPath, err = normalizeStoredFavoritePath(targetPath)
	if err != nil {
		return nil, err
	}

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
		sortFavoritesCanonical(removed)
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return removed, err
		}
		if err != nil {
			return nil, err
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
			normalized, err := normalizeRestoredFavorite(favorite)
			if err != nil {
				return err
			}

			if snapshot.data[normalized.UserID] == nil {
				snapshot.data[normalized.UserID] = make(map[string]*Favorite)
			}

			current, ok := snapshot.data[normalized.UserID][normalized.Path]
			if ok && current.Note == normalized.Note && current.CreatedAt.Equal(normalized.CreatedAt) {
				continue
			}

			snapshot.data[normalized.UserID][normalized.Path] = normalized
			changed = true
		}

		if !changed {
			return nil
		}
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
		}
	}
}

// RestoreFavoritesIfMissing restores removed favorites only when the same path
// has not been recreated during rollback.
func (s *Store) RestoreFavoritesIfMissing(favorites []*Favorite) error {
	if len(favorites) == 0 {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		changed := false

		for _, favorite := range favorites {
			normalized, err := normalizeRestoredFavorite(favorite)
			if err != nil {
				return err
			}

			userFavs := snapshot.data[normalized.UserID]
			if userFavs != nil {
				if _, ok := userFavs[normalized.Path]; ok {
					continue
				}
			} else {
				userFavs = make(map[string]*Favorite)
				snapshot.data[normalized.UserID] = userFavs
			}

			userFavs[normalized.Path] = normalized
			changed = true
		}

		if !changed {
			return nil
		}
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
		}
	}
}
