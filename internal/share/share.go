// Package share provides file sharing functionality for MnemoNAS
package share

import (
	"crypto/rand"
	"encoding/hex"
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

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrShareNotFound     = errors.New("share not found")
	ErrShareExpired      = errors.New("share has expired")
	ErrShareAccessLimit  = errors.New("share access limit reached")
	ErrInvalidPassword   = errors.New("invalid password")
	ErrShareDisabled     = errors.New("share is disabled")
	errInvalidSharePath  = errors.New("invalid share path")
	errInvalidMaxAccess  = errors.New("invalid max access")
	errShareStoreSymlink = errors.New("share store path must not be a symlink")
)

// ShareType represents the type of shared resource
type ShareType string

const (
	ShareTypeFile   ShareType = "file"
	ShareTypeFolder ShareType = "folder"
)

// Permission represents sharing permissions
type Permission string

const (
	PermissionRead      Permission = "read"
	PermissionReadWrite Permission = "read_write"
)

func normalizePermission(permission Permission) Permission {
	if permission != PermissionRead {
		return PermissionRead
	}
	return permission
}

// Share represents a shared file or folder
type Share struct {
	ID           string     `json:"id"`
	Path         string     `json:"path"`
	Type         ShareType  `json:"type"`
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
	PasswordHash string     `json:"password_hash"`
	Permission   Permission `json:"permission"`
	Enabled      bool       `json:"enabled"`
	AccessCount  int64      `json:"access_count"`
	MaxAccess    int64      `json:"max_access"`
	LastAccess   *time.Time `json:"last_access"`
	Description  string     `json:"description"`
}

// IsExpired checks if the share has expired
func (s *Share) IsExpired() bool {
	if s.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*s.ExpiresAt)
}

// HasPassword checks if the share requires a password
func (s *Share) HasPassword() bool {
	return s.PasswordHash != ""
}

// CheckPassword verifies the provided password
func (s *Share) CheckPassword(password string) bool {
	if !s.HasPassword() {
		return true
	}
	err := bcrypt.CompareHashAndPassword([]byte(s.PasswordHash), []byte(password))
	return err == nil
}

// IsAccessLimitReached checks if max access count is reached
func (s *Share) IsAccessLimitReached() bool {
	if s.MaxAccess == 0 {
		return false
	}
	return s.AccessCount >= s.MaxAccess
}

// CanAccess checks if the share can be accessed
func (s *Share) CanAccess() error {
	if !s.Enabled {
		return ErrShareDisabled
	}
	if s.IsExpired() {
		return ErrShareExpired
	}
	if s.IsAccessLimitReached() {
		return ErrShareAccessLimit
	}
	return nil
}

// ShareStore manages share persistence
type ShareStore struct {
	mu       sync.RWMutex
	writeMu  sync.Mutex
	shares   map[string]*Share
	pathIdx  map[string][]string
	filePath string
	version  uint64
}

var shareStoreWriter = writeShareStoreFile
var syncShareStoreDir = syncShareDir
var syncShareStoreRootDir = syncShareRootDir
var afterValidateShareStorePath = func() {}

var shareStoreDirRootsMu sync.RWMutex
var shareStoreDirRoots = map[string]*os.Root{}

const shareStoreRootEscapeError = "path escapes from parent"

type shareStoreSnapshot struct {
	shares   map[string]*Share
	pathIdx  map[string][]string
	filePath string
	version  uint64
}

type authorizedAccessReservation struct {
	id                 string
	currentLastAccess  time.Time
	previousLastAccess *time.Time
}

// NewShareStore creates a new share store
func NewShareStore(filePath string) (*ShareStore, error) {
	normalizedPath, err := ensureShareStoreDirRoot(filePath, false)
	if err != nil {
		return nil, err
	}

	store := &ShareStore{
		shares:   make(map[string]*Share),
		pathIdx:  make(map[string][]string),
		filePath: normalizedPath,
	}

	if err := store.load(); err != nil && !os.IsNotExist(err) {
		if recoverErr := store.recoverCorruptShares(err); recoverErr != nil {
			return nil, errors.Join(
				fmt.Errorf("failed to load shares: %w", err),
				fmt.Errorf("recover corrupt shares: %w", recoverErr),
			)
		}
	}

	return store, nil
}

func (s *ShareStore) load() error {
	data, err := readRegisteredShareStoreFile(s.filePath)
	if err != nil {
		return err
	}

	var shares []*Share
	if err := json.Unmarshal(data, &shares); err != nil {
		return fmt.Errorf("failed to parse shares file: %w", err)
	}

	s.shares = make(map[string]*Share)
	s.pathIdx = make(map[string][]string)
	needsRewrite := false

	for i, share := range shares {
		if share == nil {
			return fmt.Errorf("shares file contains null entry at index %d", i)
		}
		normalized := copyShare(share)
		originalPath := normalized.Path
		originalPermission := normalized.Permission
		normalized.Permission = normalizePermission(normalized.Permission)
		if err := validateShareInvariants(normalized); err != nil {
			needsRewrite = true
			continue
		}
		if normalized.Path != originalPath || normalized.Permission != originalPermission {
			needsRewrite = true
		}
		if _, exists := s.shares[normalized.ID]; exists {
			needsRewrite = true
		}
		s.shares[normalized.ID] = normalized
		s.pathIdx[normalized.Path] = append(s.pathIdx[normalized.Path], normalized.ID)
	}

	if needsRewrite {
		if err := saveShareState(s.filePath, s.shares); err != nil {
			return fmt.Errorf("persist normalized shares: %w", err)
		}
	}

	return nil
}

func (s *ShareStore) recoverCorruptShares(loadErr error) error {
	if !isRecoverableShareLoadError(loadErr) {
		return loadErr
	}

	corruptPath := fmt.Sprintf("%s.corrupt.%d", s.filePath, time.Now().UnixNano())
	if err := renameRegisteredShareStoreFile(s.filePath, corruptPath); err != nil {
		return fmt.Errorf("backup corrupt shares file: %w", err)
	}
	if err := syncRegisteredShareStoreDir(s.filePath); err != nil {
		if rollbackErr := renameRegisteredShareStoreFile(corruptPath, s.filePath); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt shares directory: %w", err),
				fmt.Errorf("rollback corrupt shares backup: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncRegisteredShareStoreDir(s.filePath); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt shares directory: %w", err),
				fmt.Errorf("sync corrupt shares rollback: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("sync corrupt shares directory: %w", err)
	}

	s.shares = make(map[string]*Share)
	s.pathIdx = make(map[string][]string)
	return nil
}

func isRecoverableShareLoadError(err error) bool {
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

func (s *ShareStore) save() error {
	return saveShareState(s.filePath, s.shares)
}

func saveShareState(filePath string, shares map[string]*Share) error {
	serializedShares := make([]*Share, 0, len(shares))
	for _, share := range shares {
		serializedShares = append(serializedShares, copyShare(share))
	}

	data, err := json.MarshalIndent(serializedShares, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize shares: %w", err)
	}

	if err := shareStoreWriter(filePath, data); err != nil {
		return err
	}

	return nil
}

func cloneShareMap(shares map[string]*Share) map[string]*Share {
	cloned := make(map[string]*Share, len(shares))
	for id, share := range shares {
		cloned[id] = copyShare(share)
	}
	return cloned
}

func clonePathIndex(pathIdx map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(pathIdx))
	for path, ids := range pathIdx {
		cloned[path] = append([]string(nil), ids...)
	}
	return cloned
}

func normalizeStoredSharePath(rawPath string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(rawPath), "\\", "/")
	if normalized == "" {
		return "", errInvalidSharePath
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return "", errInvalidSharePath
		}
	}
	return path.Clean("/" + normalized), nil
}

func validateShareInvariants(share *Share) error {
	cleanPath, err := normalizeStoredSharePath(share.Path)
	if err != nil {
		return err
	}
	share.Path = cleanPath

	if share.MaxAccess < 0 {
		return errInvalidMaxAccess
	}

	switch share.Permission {
	case "", PermissionRead:
		share.Permission = PermissionRead
		return nil
	default:
		return errInvalidSharePermission
	}
}

func removeShareID(ids []string, id string) []string {
	for i, currentID := range ids {
		if currentID == id {
			return append(ids[:i], ids[i+1:]...)
		}
	}
	return ids
}

func moveSharePathIndex(pathIdx map[string][]string, oldPath, newPath, id string) {
	ids := removeShareID(pathIdx[oldPath], id)
	if len(ids) == 0 {
		delete(pathIdx, oldPath)
	} else {
		pathIdx[oldPath] = ids
	}
	pathIdx[newPath] = append(pathIdx[newPath], id)
}

func sharePathMatchesOrDescendant(basePath, candidatePath string) bool {
	basePath = path.Clean(basePath)
	candidatePath = path.Clean(candidatePath)
	if basePath == "/" {
		return strings.HasPrefix(candidatePath, "/")
	}
	return candidatePath == basePath || strings.HasPrefix(candidatePath, basePath+"/")
}

func relocateSharePath(currentPath, oldRoot, newRoot string) (string, bool) {
	currentPath = path.Clean(currentPath)
	oldRoot = path.Clean(oldRoot)
	newRoot = path.Clean(newRoot)
	if !sharePathMatchesOrDescendant(oldRoot, currentPath) {
		return "", false
	}
	if currentPath == oldRoot {
		return newRoot, true
	}
	return path.Clean(newRoot + strings.TrimPrefix(currentPath, oldRoot)), true
}

func (s *ShareStore) snapshotState() shareStoreSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return shareStoreSnapshot{
		shares:   cloneShareMap(s.shares),
		pathIdx:  clonePathIndex(s.pathIdx),
		filePath: s.filePath,
		version:  s.version,
	}
}

func (s *ShareStore) commitSnapshot(snapshot shareStoreSnapshot) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.version != snapshot.version {
		return false
	}

	s.shares = snapshot.shares
	s.pathIdx = snapshot.pathIdx
	s.version++
	return true
}

func validateShareStorePath(path string) error {
	cleaned, err := normalizeShareStoreFilePath(path)
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
			return fmt.Errorf("failed to stat share store: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errShareStoreSymlink
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
			return fmt.Errorf("failed to stat share store: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errShareStoreSymlink
		}
	}
	return nil
}

func normalizeShareStoreFilePath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve share store path: %w", err)
	}
	return absPath, nil
}

func ensureShareStoreDirRoot(path string, create bool) (string, error) {
	normalizedPath, _, err := ensureShareStoreDirRootWithState(path, create)
	return normalizedPath, err
}

func ensureShareStoreDirRootWithState(path string, create bool) (string, *os.Root, error) {
	normalizedPath, err := normalizeShareStoreFilePath(path)
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Dir(normalizedPath)

	shareStoreDirRootsMu.RLock()
	root := shareStoreDirRoots[dir]
	shareStoreDirRootsMu.RUnlock()
	if root != nil {
		return normalizedPath, nil, nil
	}

	if err := validateShareStorePath(normalizedPath); err != nil {
		return "", nil, err
	}

	if create {
		if err := ensureShareDir(dir, 0755); err != nil {
			return "", nil, fmt.Errorf("failed to create directory: %w", err)
		}
	} else if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return normalizedPath, nil, nil
		}
		return "", nil, fmt.Errorf("failed to stat share store directory: %w", err)
	}

	root, err = os.OpenRoot(dir)
	if err != nil {
		return "", nil, fmt.Errorf("failed to open share store directory root: %w", err)
	}

	shareStoreDirRootsMu.Lock()
	if existing := shareStoreDirRoots[dir]; existing != nil {
		shareStoreDirRootsMu.Unlock()
		_ = root.Close()
		return normalizedPath, nil, nil
	}
	shareStoreDirRoots[dir] = root
	shareStoreDirRootsMu.Unlock()

	return normalizedPath, root, nil
}

func releaseRegisteredShareStoreDirRoot(dir string, root *os.Root) {
	if root == nil {
		return
	}
	shareStoreDirRootsMu.Lock()
	if shareStoreDirRoots[dir] == root {
		delete(shareStoreDirRoots, dir)
	}
	shareStoreDirRootsMu.Unlock()
	_ = root.Close()
}

func registeredShareStoreDirRoot(path string) (*os.Root, string, bool, error) {
	normalizedPath, err := normalizeShareStoreFilePath(path)
	if err != nil {
		return nil, "", false, err
	}
	dir := filepath.Dir(normalizedPath)
	shareStoreDirRootsMu.RLock()
	root := shareStoreDirRoots[dir]
	shareStoreDirRootsMu.RUnlock()
	return root, normalizedPath, root != nil, nil
}

func readRegisteredShareStoreFile(path string) ([]byte, error) {
	root, normalizedPath, ok, err := registeredShareStoreDirRoot(path)
	if err != nil {
		return nil, err
	}
	if !ok {
		if err := validateShareStorePath(normalizedPath); err != nil {
			return nil, err
		}
		return os.ReadFile(normalizedPath)
	}
	return readShareStoreFileWithRoot(root, normalizedPath)
}

func writeRegisteredShareStoreFileAtomically(path string, data []byte) error {
	root, normalizedPath, ok, err := registeredShareStoreDirRoot(path)
	if err != nil {
		return err
	}
	if !ok {
		return writeShareStoreFileAtomically(normalizedPath, data)
	}
	return writeShareStoreFileAtomicallyWithRoot(root, normalizedPath, data)
}

func renameRegisteredShareStoreFile(oldPath, newPath string) error {
	root, normalizedOldPath, ok, err := registeredShareStoreDirRoot(oldPath)
	if err != nil {
		return err
	}
	normalizedNewPath, err := normalizeShareStoreFilePath(newPath)
	if err != nil {
		return err
	}
	if ok && filepath.Dir(normalizedOldPath) == filepath.Dir(normalizedNewPath) {
		afterValidateShareStorePath()
		if err := root.Rename(filepath.Base(normalizedOldPath), filepath.Base(normalizedNewPath)); err != nil {
			return mapShareRootPathError(err)
		}
		return nil
	}
	if err := validateShareStorePath(normalizedOldPath); err != nil {
		return err
	}
	if err := validateShareStorePath(normalizedNewPath); err != nil {
		return err
	}
	afterValidateShareStorePath()
	return os.Rename(normalizedOldPath, normalizedNewPath)
}

func syncRegisteredShareStoreDir(path string) error {
	root, normalizedPath, ok, err := registeredShareStoreDirRoot(path)
	if err != nil {
		return err
	}
	if ok {
		return syncShareStoreRootDir(root)
	}
	return syncShareStoreDir(filepath.Dir(normalizedPath))
}

func readShareStoreFileWithRoot(root *os.Root, path string) ([]byte, error) {
	afterValidateShareStorePath()

	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return nil, mapShareRootPathError(err)
	}
	defer file.Close()

	return io.ReadAll(file)
}

func writeShareStoreFileAtomicallyWithRoot(root *os.Root, path string, data []byte) error {
	afterValidateShareStorePath()

	tmpFile, tmpName, err := createShareTempFile(root, ".shares-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp shares file: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = root.Remove(tmpName)
		}
	}()

	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		return cleanupShareTempPath(root, tmpName, fmt.Errorf("failed to set temp shares permissions: %w", err))
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return cleanupShareTempPath(root, tmpName, fmt.Errorf("failed to write shares file: %w", err))
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return cleanupShareTempPath(root, tmpName, fmt.Errorf("failed to sync shares file: %w", err))
	}
	if err := tmpFile.Close(); err != nil {
		return cleanupShareTempPath(root, tmpName, fmt.Errorf("failed to close temp shares file: %w", err))
	}
	if err := root.Rename(tmpName, filepath.Base(path)); err != nil {
		return cleanupShareTempPath(root, tmpName, fmt.Errorf("failed to replace shares file: %w", mapShareRootPathError(err)))
	}
	cleanup = false
	if err := syncRegisteredShareStoreDir(path); err != nil {
		return fmt.Errorf("failed to sync shares directory: %w", err)
	}

	return nil
}

func newShareTempName(pattern string) (string, error) {
	randomPart, err := generateShareID()
	if err != nil {
		return "", err
	}
	if strings.Contains(pattern, "*") {
		return strings.Replace(pattern, "*", randomPart, 1), nil
	}
	return pattern + randomPart, nil
}

func createShareTempFile(root *os.Root, pattern string) (*os.File, string, error) {
	for range 32 {
		tmpName, err := newShareTempName(pattern)
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
		return nil, "", mapShareRootPathError(err)
	}

	return nil, "", errors.New("failed to allocate unique temp shares file")
}

func cleanupShareTempPath(root *os.Root, tmpPath string, operationErr error) error {
	if removeErr := root.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return errors.Join(operationErr, fmt.Errorf("cleanup temp shares file %s: %w", tmpPath, removeErr))
	}
	return operationErr
}

func cleanupCreatedShareDirs(createdDirs []string, operationErr error) error {
	rollbackErr := operationErr
	for _, dir := range createdDirs {
		if removeErr := os.Remove(dir); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("cleanup created shares directory %s: %w", dir, removeErr))
			break
		}
	}
	return rollbackErr
}

func syncShareRootDir(root *os.Root) error {
	dirHandle, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func isShareRootEscapeError(err error) bool {
	return err != nil && strings.Contains(err.Error(), shareStoreRootEscapeError)
}

func mapShareRootPathError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrPermission) || isShareRootEscapeError(err) {
		return errShareStoreSymlink
	}
	return err
}

func writeShareStoreFileAtomically(path string, data []byte) error {
	if err := validateShareStorePath(path); err != nil {
		return err
	}
	afterValidateShareStorePath()

	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, ".shares-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp shares file: %w", err)
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
		return fmt.Errorf("failed to set temp shares permissions: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write shares file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to sync shares file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp shares file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to replace shares file: %w", err)
	}
	cleanup = false
	if err := syncShareStoreDir(dir); err != nil {
		return fmt.Errorf("failed to sync shares directory: %w", err)
	}

	return nil
}

func writeShareStoreFile(path string, data []byte) error {
	_, normalizedPath, ok, err := registeredShareStoreDirRoot(path)
	if err != nil {
		return err
	}
	if ok {
		return writeRegisteredShareStoreFileAtomically(normalizedPath, data)
	}
	if err := validateShareStorePath(normalizedPath); err != nil {
		return err
	}
	createdDirs, err := collectMissingShareDirs(filepath.Dir(normalizedPath))
	if err != nil {
		return err
	}
	registeredRoot := (*os.Root)(nil)
	normalizedPath, registeredRoot, err = ensureShareStoreDirRootWithState(normalizedPath, true)
	if err != nil {
		releaseRegisteredShareStoreDirRoot(filepath.Dir(normalizedPath), registeredRoot)
		return cleanupCreatedShareDirs(createdDirs, fmt.Errorf("failed to create directory: %w", err))
	}
	if err := writeRegisteredShareStoreFileAtomically(normalizedPath, data); err != nil {
		releaseRegisteredShareStoreDirRoot(filepath.Dir(normalizedPath), registeredRoot)
		return cleanupCreatedShareDirs(createdDirs, err)
	}
	return nil
}

func collectMissingShareDirs(dir string) ([]string, error) {
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

func syncCreatedShareDirs(createdDirs []string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncShareStoreDir(filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync shares directory tree: %w", err)
		}
	}
	return nil
}

func ensureShareDir(dir string, perm os.FileMode) error {
	createdDirs, err := collectMissingShareDirs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	return syncCreatedShareDirs(createdDirs)
}

func syncShareDir(dir string) error {
	dirHandle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

// CreateShareOptions contains options for creating a share
type CreateShareOptions struct {
	Path        string
	Type        ShareType
	CreatedBy   string
	ExpiresIn   *time.Duration
	Password    string
	Permission  Permission
	MaxAccess   int64
	Description string
}

// Create creates a new share
func (s *ShareStore) Create(opts CreateShareOptions) (*Share, error) {
	id, err := generateShareID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate share ID: %w", err)
	}

	now := time.Now()
	share := &Share{
		ID:          id,
		Path:        opts.Path,
		Type:        opts.Type,
		CreatedBy:   opts.CreatedBy,
		CreatedAt:   now,
		Permission:  opts.Permission,
		Enabled:     true,
		MaxAccess:   opts.MaxAccess,
		Description: opts.Description,
	}

	if opts.ExpiresIn != nil {
		exp := now.Add(*opts.ExpiresIn)
		share.ExpiresAt = &exp
	}

	if opts.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(opts.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("failed to hash password: %w", err)
		}
		share.PasswordHash = string(hash)
	}

	if err := validateShareInvariants(share); err != nil {
		return nil, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		snapshot.shares[id] = copyShare(share)
		snapshot.pathIdx[share.Path] = append(snapshot.pathIdx[share.Path], id)

		if err := saveShareState(snapshot.filePath, snapshot.shares); err != nil {
			return nil, err
		}
		if s.commitSnapshot(snapshot) {
			return copyShare(share), nil
		}
	}
}

// Get retrieves a share by ID
func (s *ShareStore) Get(id string) (*Share, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	share, ok := s.shares[id]
	if !ok {
		return nil, ErrShareNotFound
	}

	return copyShare(share), nil
}

// GetByPath retrieves all shares for a path
func (s *ShareStore) GetByPath(path string) []*Share {
	normalizedPath, err := normalizeStoredSharePath(path)
	if err != nil {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := s.pathIdx[normalizedPath]
	shares := make([]*Share, 0, len(ids))

	for _, id := range ids {
		if share, ok := s.shares[id]; ok {
			shares = append(shares, copyShare(share))
		}
	}

	sortSharesForOutput(shares)

	return shares
}

// ListByUser lists all shares created by a user
func (s *ShareStore) ListByUser(userID string) []*Share {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var shares []*Share
	for _, share := range s.shares {
		if share.CreatedBy == userID {
			shares = append(shares, copyShare(share))
		}
	}

	sortSharesForOutput(shares)

	return shares
}

// ListAll lists all shares
func (s *ShareStore) ListAll() []*Share {
	s.mu.RLock()
	defer s.mu.RUnlock()

	shares := make([]*Share, 0, len(s.shares))
	for _, share := range s.shares {
		shares = append(shares, copyShare(share))
	}

	sortSharesForOutput(shares)

	return shares
}

// Update updates a share
func (s *ShareStore) Update(id string, fn func(*Share) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		share, ok := snapshot.shares[id]
		if !ok {
			return ErrShareNotFound
		}

		updated := copyShare(share)
		if err := fn(updated); err != nil {
			return err
		}
		if err := validateShareInvariants(updated); err != nil {
			return err
		}

		oldPath := share.Path
		newPath := updated.Path
		snapshot.shares[id] = updated
		if oldPath != newPath {
			ids := removeShareID(snapshot.pathIdx[oldPath], id)
			if len(ids) == 0 {
				delete(snapshot.pathIdx, oldPath)
			} else {
				snapshot.pathIdx[oldPath] = ids
			}
			snapshot.pathIdx[newPath] = append(snapshot.pathIdx[newPath], id)
		}

		if err := saveShareState(snapshot.filePath, snapshot.shares); err != nil {
			return err
		}
		if s.commitSnapshot(snapshot) {
			return nil
		}
	}
}

// Delete deletes a share
func (s *ShareStore) Delete(id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		share, ok := snapshot.shares[id]
		if !ok {
			return ErrShareNotFound
		}

		ids := removeShareID(snapshot.pathIdx[share.Path], id)
		if len(ids) == 0 {
			delete(snapshot.pathIdx, share.Path)
		} else {
			snapshot.pathIdx[share.Path] = ids
		}
		delete(snapshot.shares, id)

		if err := saveShareState(snapshot.filePath, snapshot.shares); err != nil {
			return err
		}
		if s.commitSnapshot(snapshot) {
			return nil
		}
	}
}

// UpdatePathReferences rewrites share paths when a filesystem path is renamed.
func (s *ShareStore) UpdatePathReferences(oldPath, newPath string) error {
	_, err := s.UpdatePathReferencesWithRestore(oldPath, newPath)
	return err
}

// UpdatePathReferencesWithRestore rewrites share paths when a filesystem path
// is renamed and returns the original share states needed to restore the
// change if a later step fails.
func (s *ShareStore) UpdatePathReferencesWithRestore(oldPath, newPath string) ([]*Share, error) {
	var err error
	oldPath, err = normalizeStoredSharePath(oldPath)
	if err != nil {
		return nil, err
	}
	newPath, err = normalizeStoredSharePath(newPath)
	if err != nil {
		return nil, err
	}
	if oldPath == newPath {
		return nil, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		changed := false
		var renamed []*Share

		for id, share := range snapshot.shares {
			updatedPath, ok := relocateSharePath(share.Path, oldPath, newPath)
			if !ok || updatedPath == share.Path {
				continue
			}

			renamed = append(renamed, copyShare(share))
			updated := copyShare(share)
			updated.Path = updatedPath
			snapshot.shares[id] = updated
			moveSharePathIndex(snapshot.pathIdx, share.Path, updatedPath, id)
			changed = true
		}

		if !changed {
			return nil, nil
		}
		if err := saveShareState(snapshot.filePath, snapshot.shares); err != nil {
			return nil, err
		}
		if s.commitSnapshot(snapshot) {
			return renamed, nil
		}
	}
}

// DisableSharesUnderPath disables shares that reference a deleted path.
func (s *ShareStore) DisableSharesUnderPath(targetPath string) error {
	_, err := s.DisableSharesUnderPathWithRestore(targetPath)
	return err
}

// DisableSharesUnderPathWithRestore disables shares under a path and returns
// the prior share states needed to restore the change if a later step fails.
func (s *ShareStore) DisableSharesUnderPathWithRestore(targetPath string) ([]*Share, error) {
	var err error
	targetPath, err = normalizeStoredSharePath(targetPath)
	if err != nil {
		return nil, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		changed := false
		var disabled []*Share

		for id, share := range snapshot.shares {
			if !sharePathMatchesOrDescendant(targetPath, share.Path) || !share.Enabled {
				continue
			}

			disabled = append(disabled, copyShare(share))
			updated := copyShare(share)
			updated.Enabled = false
			snapshot.shares[id] = updated
			changed = true
		}

		if !changed {
			return nil, nil
		}
		if err := saveShareState(snapshot.filePath, snapshot.shares); err != nil {
			return nil, err
		}
		if s.commitSnapshot(snapshot) {
			return disabled, nil
		}
	}
}

// RestoreShares restores previously changed share states after a failed operation.
func (s *ShareStore) RestoreShares(shares []*Share) error {
	if len(shares) == 0 {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		changed := false

		for _, original := range shares {
			current, ok := snapshot.shares[original.ID]
			if ok && sharesEqual(current, original) {
				continue
			}
			if ok {
				if current.Path != original.Path {
					moveSharePathIndex(snapshot.pathIdx, current.Path, original.Path, original.ID)
				}
			} else {
				snapshot.pathIdx[original.Path] = append(snapshot.pathIdx[original.Path], original.ID)
			}

			snapshot.shares[original.ID] = copyShare(original)
			changed = true
		}

		if !changed {
			return nil
		}
		if err := saveShareState(snapshot.filePath, snapshot.shares); err != nil {
			return err
		}
		if s.commitSnapshot(snapshot) {
			return nil
		}
	}
}

// RestoreDisabledSharesPreservingCurrent restores share availability after a
// failed delete rollback while keeping newer share metadata changes.
func (s *ShareStore) RestoreDisabledSharesPreservingCurrent(shares []*Share) error {
	if len(shares) == 0 {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		changed := false

		for _, original := range shares {
			current, ok := snapshot.shares[original.ID]
			if !ok {
				continue
			}

			updated := copyShare(current)
			if updated.Path != original.Path {
				moveSharePathIndex(snapshot.pathIdx, updated.Path, original.Path, original.ID)
				updated.Path = original.Path
			}
			updated.Enabled = original.Enabled

			if sharesEqual(updated, current) {
				continue
			}

			snapshot.shares[original.ID] = updated
			changed = true
		}

		if !changed {
			return nil
		}
		if err := saveShareState(snapshot.filePath, snapshot.shares); err != nil {
			return err
		}
		if s.commitSnapshot(snapshot) {
			return nil
		}
	}
}

// RestoreMovedSharesPreservingCurrent restores share paths after a failed rename
// rollback while keeping newer share metadata changes.
func (s *ShareStore) RestoreMovedSharesPreservingCurrent(shares []*Share) error {
	if len(shares) == 0 {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		changed := false

		for _, original := range shares {
			current, ok := snapshot.shares[original.ID]
			if !ok {
				continue
			}

			updated := copyShare(current)
			if updated.Path != original.Path {
				moveSharePathIndex(snapshot.pathIdx, updated.Path, original.Path, original.ID)
				updated.Path = original.Path
			}

			if sharesEqual(updated, current) {
				continue
			}

			snapshot.shares[original.ID] = updated
			changed = true
		}

		if !changed {
			return nil
		}
		if err := saveShareState(snapshot.filePath, snapshot.shares); err != nil {
			return err
		}
		if s.commitSnapshot(snapshot) {
			return nil
		}
	}
}

func sharesEqual(a, b *Share) bool {
	if a == nil || b == nil {
		return a == b
	}

	return a.ID == b.ID &&
		a.Path == b.Path &&
		a.Type == b.Type &&
		a.PasswordHash == b.PasswordHash &&
		timePtrEqual(a.ExpiresAt, b.ExpiresAt) &&
		a.CreatedAt.Equal(b.CreatedAt) &&
		a.CreatedBy == b.CreatedBy &&
		a.Permission == b.Permission &&
		a.Enabled == b.Enabled &&
		a.AccessCount == b.AccessCount &&
		a.MaxAccess == b.MaxAccess &&
		a.Description == b.Description &&
		timePtrEqual(a.LastAccess, b.LastAccess)
}

func timePtrEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// RecordAccess records an access to the share
func (s *ShareStore) RecordAccess(id string) error {
	_, err := s.RecordAuthorizedAccess(id)
	return err
}

// RecordAuthorizedAccess validates access constraints and records an access atomically.
func (s *ShareStore) RecordAuthorizedAccess(id string) (*Share, error) {
	share, _, err := s.reserveAuthorizedAccess(id)
	if err != nil {
		return nil, err
	}
	return share, nil
}

func (s *ShareStore) reserveAuthorizedAccess(id string) (*Share, *authorizedAccessReservation, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		share, ok := snapshot.shares[id]
		if !ok {
			return nil, nil, ErrShareNotFound
		}

		if err := share.CanAccess(); err != nil {
			return nil, nil, err
		}

		prevLastAccess := cloneTimePtr(share.LastAccess)
		updated := copyShare(share)
		now := time.Now()
		updated.AccessCount++
		updated.LastAccess = &now
		snapshot.shares[id] = updated

		if err := saveShareState(snapshot.filePath, snapshot.shares); err != nil {
			return nil, nil, err
		}
		if s.commitSnapshot(snapshot) {
			return copyShare(updated), &authorizedAccessReservation{
				id:                 id,
				currentLastAccess:  now,
				previousLastAccess: prevLastAccess,
			}, nil
		}
	}
}

func (s *ShareStore) rollbackAuthorizedAccess(reservation *authorizedAccessReservation) error {
	if reservation == nil {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		share, ok := snapshot.shares[reservation.id]
		if !ok {
			return ErrShareNotFound
		}
		if share.AccessCount == 0 {
			return nil
		}

		updated := copyShare(share)
		updated.AccessCount--
		if updated.LastAccess != nil && updated.LastAccess.Equal(reservation.currentLastAccess) {
			updated.LastAccess = cloneTimePtr(reservation.previousLastAccess)
		}
		snapshot.shares[reservation.id] = updated

		if err := saveShareState(snapshot.filePath, snapshot.shares); err != nil {
			return err
		}
		if s.commitSnapshot(snapshot) {
			return nil
		}
	}
}

// Access validates and records access to a share
func (s *ShareStore) Access(id string, password string) (*Share, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		share, ok := snapshot.shares[id]
		if !ok {
			return nil, ErrShareNotFound
		}

		if err := share.CanAccess(); err != nil {
			return nil, err
		}

		if share.HasPassword() && !share.CheckPassword(password) {
			return nil, ErrInvalidPassword
		}

		updated := copyShare(share)
		now := time.Now()
		updated.AccessCount++
		updated.LastAccess = &now
		snapshot.shares[id] = updated

		if err := saveShareState(snapshot.filePath, snapshot.shares); err != nil {
			return nil, err
		}
		if s.commitSnapshot(snapshot) {
			return copyShare(updated), nil
		}
	}
}

func copyShare(share *Share) *Share {
	if share == nil {
		return nil
	}

	copy := *share
	if share.ExpiresAt != nil {
		expiresAt := *share.ExpiresAt
		copy.ExpiresAt = &expiresAt
	}
	if share.LastAccess != nil {
		lastAccess := *share.LastAccess
		copy.LastAccess = &lastAccess
	}

	return &copy
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func sortSharesForOutput(shares []*Share) {
	sort.Slice(shares, func(i, j int) bool {
		left := shares[i]
		right := shares[j]
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.After(right.CreatedAt)
		}
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return left.Path < right.Path
	})
}

func generateShareID() (string, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// ShareInfo is a safe representation of a share for API responses
type ShareInfo struct {
	ID          string     `json:"id"`
	Path        string     `json:"path"`
	Type        ShareType  `json:"type"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	HasPassword bool       `json:"has_password"`
	Permission  Permission `json:"permission"`
	Enabled     bool       `json:"enabled"`
	AccessCount int64      `json:"access_count"`
	MaxAccess   int64      `json:"max_access"`
	LastAccess  *time.Time `json:"last_access"`
	Description string     `json:"description"`
	URL         string     `json:"url,omitempty"`
}

// ToInfo converts a Share to ShareInfo
func (s *Share) ToInfo() *ShareInfo {
	return &ShareInfo{
		ID:          s.ID,
		Path:        s.Path,
		Type:        s.Type,
		CreatedBy:   s.CreatedBy,
		CreatedAt:   s.CreatedAt,
		ExpiresAt:   s.ExpiresAt,
		HasPassword: s.HasPassword(),
		Permission:  normalizePermission(s.Permission),
		Enabled:     s.Enabled,
		AccessCount: s.AccessCount,
		MaxAccess:   s.MaxAccess,
		LastAccess:  s.LastAccess,
		Description: s.Description,
	}
}
