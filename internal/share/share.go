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
	"syscall"
	"time"

	"github.com/seanbao/mnemonas/internal/rootio"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrShareNotFound     = errors.New("share not found")
	ErrShareExpired      = errors.New("share has expired")
	ErrShareAccessLimit  = errors.New("share access limit reached")
	ErrInvalidPassword   = errors.New("invalid password")
	ErrShareDisabled     = errors.New("share is disabled")
	errInvalidSharePath  = errors.New("invalid share path")
	errInvalidShareID    = errors.New("invalid share ID")
	errShareIDImmutable  = errors.New("share ID cannot be changed")
	errInvalidShareType  = errors.New("invalid share type")
	errInvalidMaxAccess  = errors.New("invalid max access")
	errSharePasswordLong = errors.New("share password must be at most 72 bytes")
	errShareStoreSymlink = errors.New("share store path must not be a symlink")
)

const maxSharePasswordBytes = 72

// PersistenceWarningError reports that the shares mutation is already visible
// on disk, but the final directory fsync did not complete.
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

// ShareRiskLevel describes the current safety risk of an enabled share.
type ShareRiskLevel string

const (
	ShareRiskLevelNone   ShareRiskLevel = "none"
	ShareRiskLevelLow    ShareRiskLevel = "low"
	ShareRiskLevelMedium ShareRiskLevel = "medium"
	ShareRiskLevelHigh   ShareRiskLevel = "high"
)

// ShareRiskReason explains one concrete share-safety concern.
type ShareRiskReason struct {
	Code     string         `json:"code"`
	Level    ShareRiskLevel `json:"level"`
	Message  string         `json:"message"`
	Resolved bool           `json:"resolved,omitempty"`
}

// ShareRisk summarizes share-safety concerns for list/detail responses.
type ShareRisk struct {
	Level   ShareRiskLevel    `json:"level"`
	Reasons []ShareRiskReason `json:"reasons,omitempty"`
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

func (s *Share) IsActive(now time.Time) bool {
	if s == nil || !s.Enabled {
		return false
	}
	if s.ExpiresAt != nil && now.After(*s.ExpiresAt) {
		return false
	}
	if s.MaxAccess > 0 && s.AccessCount >= s.MaxAccess {
		return false
	}
	return true
}

// Risk summarizes safety concerns for currently active shares.
func (s *Share) Risk(now time.Time) ShareRisk {
	if !s.IsActive(now) {
		return ShareRisk{Level: ShareRiskLevelNone}
	}

	reasons := make([]ShareRiskReason, 0, 5)
	addReason := func(code string, level ShareRiskLevel, message string) {
		reasons = append(reasons, ShareRiskReason{
			Code:    code,
			Level:   level,
			Message: message,
		})
	}

	if s.Type == ShareTypeFolder && path.Clean(s.Path) == "/" {
		addReason("root_folder", ShareRiskLevelHigh, "分享根目录会公开整个文件空间")
	} else if s.Type == ShareTypeFolder && sharePathDepth(s.Path) <= 1 {
		addReason("broad_folder", ShareRiskLevelMedium, "分享顶层文件夹可能覆盖较多内容")
	}
	if !s.HasPassword() {
		addReason("no_password", ShareRiskLevelHigh, "未设置密码，拿到链接的人都能访问")
	}
	if s.ExpiresAt == nil {
		addReason("no_expiration", ShareRiskLevelMedium, "未设置过期时间，链接会长期有效")
	} else if expiresIn := s.ExpiresAt.Sub(now); expiresIn > 0 && expiresIn <= 72*time.Hour {
		addReason("expiring_soon", ShareRiskLevelLow, "分享即将到期，建议确认是否需要延长或关闭")
	}
	if s.MaxAccess == 0 {
		addReason("unlimited_access", ShareRiskLevelMedium, "未设置访问次数上限")
	}
	if s.LastAccess == nil && !s.CreatedAt.IsZero() && now.Sub(s.CreatedAt) >= 30*24*time.Hour {
		addReason("unused_enabled", ShareRiskLevelLow, "该分享长期未被访问但仍处于启用状态")
	} else if s.LastAccess != nil && now.Sub(*s.LastAccess) >= 90*24*time.Hour {
		addReason("stale_enabled", ShareRiskLevelLow, "该分享最近访问时间较久，建议确认是否仍需保留")
	}

	return ShareRisk{
		Level:   highestShareRiskLevel(reasons),
		Reasons: reasons,
	}
}

func sharePathDepth(rawPath string) int {
	cleaned := path.Clean(rawPath)
	if cleaned == "/" {
		return 0
	}
	return len(strings.Split(strings.Trim(cleaned, "/"), "/"))
}

func highestShareRiskLevel(reasons []ShareRiskReason) ShareRiskLevel {
	level := ShareRiskLevelNone
	for _, reason := range reasons {
		if shareRiskLevelRank(reason.Level) > shareRiskLevelRank(level) {
			level = reason.Level
		}
	}
	return level
}

func shareRiskLevelRank(level ShareRiskLevel) int {
	switch level {
	case ShareRiskLevelHigh:
		return 3
	case ShareRiskLevelMedium:
		return 2
	case ShareRiskLevelLow:
		return 1
	default:
		return 0
	}
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
		if err := normalizeLegacyShareInvariants(normalized); err != nil {
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
	}
	s.pathIdx = rebuildPathIndex(s.shares)

	if needsRewrite {
		if err := saveShareState(s.filePath, s.shares); err != nil {
			if !IsPersistenceWarning(err) {
				return fmt.Errorf("persist normalized shares: %w", err)
			}
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

func saveShareState(filePath string, shares map[string]*Share) error {
	serializedShares := make([]*Share, 0, len(shares))
	for _, share := range shares {
		serializedShares = append(serializedShares, copyShare(share))
	}
	sortSharesCanonical(serializedShares)

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

func rebuildPathIndex(shares map[string]*Share) map[string][]string {
	pathIdx := make(map[string][]string, len(shares))
	for id, share := range shares {
		if share == nil {
			continue
		}
		pathIdx[share.Path] = append(pathIdx[share.Path], id)
	}
	return pathIdx
}

func normalizeStoredSharePath(rawPath string) (string, error) {
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	if strings.ContainsRune(normalized, '\x00') {
		return "", errInvalidSharePath
	}
	if strings.TrimSpace(normalized) == "" {
		return "", errInvalidSharePath
	}
	if hasShareDotSegment(normalized) {
		return "", errInvalidSharePath
	}
	return path.Clean("/" + normalized), nil
}

func normalizeLegacyStoredSharePath(rawPath string) (string, error) {
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	if strings.ContainsRune(normalized, '\x00') {
		return "", errInvalidSharePath
	}
	if strings.TrimSpace(normalized) == "" {
		return "", errInvalidSharePath
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return "", errInvalidSharePath
		}
	}
	return path.Clean("/" + normalized), nil
}

func hasShareDotSegment(filePath string) bool {
	for _, segment := range strings.Split(filePath, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func validateShareInvariants(share *Share) error {
	return validateShareInvariantsWithPathNormalizer(share, normalizeStoredSharePath)
}

func validateShareInvariantsWithPathNormalizer(share *Share, normalizePath func(string) (string, error)) error {
	if !isValidShareID(share.ID) {
		return errInvalidShareID
	}

	cleanPath, err := normalizePath(share.Path)
	if err != nil {
		return err
	}
	share.Path = cleanPath

	switch share.Type {
	case ShareTypeFile, ShareTypeFolder:
	default:
		return errInvalidShareType
	}

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

func normalizeLegacyShareInvariants(share *Share) error {
	share.Permission = normalizePermission(share.Permission)
	return validateShareInvariantsWithPathNormalizer(share, normalizeLegacyStoredSharePath)
}

func isValidShareID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		b := id[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == '_' {
			continue
		}
		return false
	}
	return true
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

func (s *ShareStore) persistSnapshot(snapshot shareStoreSnapshot) (bool, error) {
	err := saveShareState(snapshot.filePath, snapshot.shares)
	if err != nil && !IsPersistenceWarning(err) {
		return false, err
	}
	if !s.commitSnapshot(snapshot) {
		return false, nil
	}
	return true, err
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
	normalizedPath, _, _, err := ensureShareStoreDirRootWithState(path, create)
	return normalizedPath, err
}

func ensureShareStoreDirRootWithState(path string, create bool) (string, *os.Root, []string, error) {
	normalizedPath, err := normalizeShareStoreFilePath(path)
	if err != nil {
		return "", nil, nil, err
	}
	dir := filepath.Dir(normalizedPath)

	shareStoreDirRootsMu.RLock()
	root := shareStoreDirRoots[dir]
	shareStoreDirRootsMu.RUnlock()
	if root != nil {
		return normalizedPath, nil, nil, nil
	}

	if err := validateShareStorePath(normalizedPath); err != nil {
		return "", nil, nil, err
	}

	createdDirs := []string(nil)
	if create {
		var err error
		createdDirs, err = ensureShareDir(dir, 0755)
		if err != nil {
			return "", nil, createdDirs, fmt.Errorf("failed to create directory: %w", err)
		}
	} else if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return normalizedPath, nil, nil, nil
		}
		return "", nil, nil, fmt.Errorf("failed to stat share store directory: %w", err)
	}

	root, err = os.OpenRoot(dir)
	if err != nil {
		return "", nil, createdDirs, fmt.Errorf("failed to open share store directory root: %w", err)
	}

	shareStoreDirRootsMu.Lock()
	if existing := shareStoreDirRoots[dir]; existing != nil {
		shareStoreDirRootsMu.Unlock()
		_ = root.Close()
		return normalizedPath, nil, createdDirs, nil
	}
	shareStoreDirRoots[dir] = root
	shareStoreDirRootsMu.Unlock()

	return normalizedPath, root, createdDirs, nil
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
		normalizedPath, _, _, err = ensureShareStoreDirRootWithState(normalizedPath, false)
		if err != nil {
			return nil, err
		}
		root, normalizedPath, ok, err = registeredShareStoreDirRoot(normalizedPath)
		if err != nil {
			return nil, err
		}
		if !ok {
			if err := validateShareStorePath(normalizedPath); err != nil {
				return nil, err
			}
			return nil, &os.PathError{Op: "open", Path: normalizedPath, Err: os.ErrNotExist}
		}
	}
	return readShareStoreFileWithRoot(root, normalizedPath)
}

func writeRegisteredShareStoreFileAtomically(path string, data []byte) error {
	root, normalizedPath, ok, err := registeredShareStoreDirRoot(path)
	if err != nil {
		return err
	}
	if !ok {
		normalizedPath, _, _, err = ensureShareStoreDirRootWithState(normalizedPath, true)
		if err != nil {
			return err
		}
		return writeRegisteredShareStoreFileAtomically(normalizedPath, data)
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
	if filepath.Dir(normalizedOldPath) != filepath.Dir(normalizedNewPath) {
		return fmt.Errorf("share store rename requires same parent directory")
	}
	if ok {
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
	normalizedOldPath, _, _, err = ensureShareStoreDirRootWithState(normalizedOldPath, false)
	if err != nil {
		return err
	}
	root, normalizedOldPath, ok, err = registeredShareStoreDirRoot(normalizedOldPath)
	if err != nil {
		return err
	}
	if !ok {
		return &os.PathError{Op: "rename", Path: normalizedOldPath, Err: os.ErrNotExist}
	}
	afterValidateShareStorePath()
	if err := root.Rename(filepath.Base(normalizedOldPath), filepath.Base(normalizedNewPath)); err != nil {
		return mapShareRootPathError(err)
	}
	return nil
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

	file, err := rootio.OpenFileNoFollow(root, filepath.Base(path), os.O_RDONLY, 0)
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
		return WrapPersistenceWarning(fmt.Errorf("failed to sync shares directory: %w", err))
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
		tmpFile, err := rootio.OpenFileNoFollow(root, tmpName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
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
	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ELOOP) || rootio.IsSymlinkError(err) || isShareRootEscapeError(err) {
		return errShareStoreSymlink
	}
	return err
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
	registeredRoot := (*os.Root)(nil)
	createdDirs := []string(nil)
	normalizedPath, registeredRoot, createdDirs, err = ensureShareStoreDirRootWithState(normalizedPath, true)
	if err != nil {
		releaseRegisteredShareStoreDirRoot(filepath.Dir(normalizedPath), registeredRoot)
		return cleanupCreatedShareDirs(createdDirs, fmt.Errorf("failed to create directory: %w", err))
	}
	releaseRootOnError := registeredRoot != nil
	if err := writeRegisteredShareStoreFileAtomically(normalizedPath, data); err != nil {
		if releaseRootOnError {
			releaseRegisteredShareStoreDirRoot(filepath.Dir(normalizedPath), registeredRoot)
			return cleanupCreatedShareDirs(createdDirs, err)
		}
		return err
	}
	return nil
}

func syncCreatedShareDirs(createdDirs []string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncShareStoreDir(filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync shares directory tree: %w", err)
		}
	}
	return nil
}

func ensureShareDir(dir string, perm os.FileMode) ([]string, error) {
	createdDirs, err := rootio.MkdirAllPathNoFollowTracked(dir, perm)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return createdDirs, errShareStoreSymlink
		}
		return createdDirs, err
	}
	return createdDirs, syncCreatedShareDirs(createdDirs)
}

func syncShareDir(dir string) error {
	dirHandle, err := rootio.OpenDirPathNoFollow(dir)
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
	if err := validateSharePassword(opts.Password); err != nil {
		return nil, err
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
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return copyShare(share), err
		}
		if err != nil {
			return nil, err
		}
	}
}

func validateSharePassword(password string) error {
	if len([]byte(password)) > maxSharePasswordBytes {
		return errSharePasswordLong
	}
	return nil
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

// ListByUser lists all shares created by a user identifier.
// Legacy share metadata may persist the owner's username instead of the user ID,
// so callers may pass multiple accepted identifiers.
func (s *ShareStore) ListByUser(ownerIdentifiers ...string) []*Share {
	s.mu.RLock()
	defer s.mu.RUnlock()

	acceptedOwners := make(map[string]struct{}, len(ownerIdentifiers))
	for _, ownerIdentifier := range ownerIdentifiers {
		if strings.TrimSpace(ownerIdentifier) == "" {
			continue
		}
		acceptedOwners[ownerIdentifier] = struct{}{}
	}
	if len(acceptedOwners) == 0 {
		return nil
	}

	var shares []*Share
	for _, share := range s.shares {
		if _, ok := acceptedOwners[share.CreatedBy]; ok {
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
		if updated.ID != id {
			return errShareIDImmutable
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
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
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
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
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
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return renamed, err
		}
		if err != nil {
			return nil, err
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
		sortSharesCanonical(disabled)
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return disabled, err
		}
		if err != nil {
			return nil, err
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
			if original == nil {
				continue
			}
			normalized := copyShare(original)
			if err := normalizeLegacyShareInvariants(normalized); err != nil {
				return err
			}

			current, ok := snapshot.shares[normalized.ID]
			if ok && sharesEqual(current, normalized) {
				continue
			}
			if ok {
				if current.Path != normalized.Path {
					moveSharePathIndex(snapshot.pathIdx, current.Path, normalized.Path, normalized.ID)
				}
			} else {
				snapshot.pathIdx[normalized.Path] = append(snapshot.pathIdx[normalized.Path], normalized.ID)
			}

			snapshot.shares[normalized.ID] = normalized
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
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
		}
	}
}

// RestoreSharesPreservingCurrent restores share path/enabled state while keeping
// newer mutable metadata on existing shares. Missing shares are recreated from
// the supplied restore state.
func (s *ShareStore) RestoreSharesPreservingCurrent(shares []*Share) error {
	if len(shares) == 0 {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		changed := false

		for _, original := range shares {
			normalized := copyShare(original)
			if err := normalizeLegacyShareInvariants(normalized); err != nil {
				return err
			}

			current, ok := snapshot.shares[normalized.ID]
			if !ok {
				snapshot.shares[normalized.ID] = normalized
				snapshot.pathIdx[normalized.Path] = append(snapshot.pathIdx[normalized.Path], normalized.ID)
				changed = true
				continue
			}

			updated := copyShare(current)
			if updated.Path != normalized.Path {
				moveSharePathIndex(snapshot.pathIdx, updated.Path, normalized.Path, normalized.ID)
				updated.Path = normalized.Path
			}
			updated.Enabled = normalized.Enabled

			if sharesEqual(updated, current) {
				continue
			}

			snapshot.shares[normalized.ID] = updated
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
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
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
		reservation := &authorizedAccessReservation{
			id:                 id,
			currentLastAccess:  now,
			previousLastAccess: prevLastAccess,
		}
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return copyShare(updated), reservation, err
		}
		if err != nil {
			return nil, nil, err
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
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
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
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return copyShare(updated), err
		}
		if err != nil {
			return nil, err
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

func sortSharesCanonical(shares []*Share) {
	sort.Slice(shares, func(i, j int) bool {
		if shares[i].Path != shares[j].Path {
			return shares[i].Path < shares[j].Path
		}
		if shares[i].CreatedBy != shares[j].CreatedBy {
			return shares[i].CreatedBy < shares[j].CreatedBy
		}
		if !shares[i].CreatedAt.Equal(shares[j].CreatedAt) {
			return shares[i].CreatedAt.Before(shares[j].CreatedAt)
		}
		return shares[i].ID < shares[j].ID
	})
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
	Risk        ShareRisk  `json:"risk"`
}

// ToInfo converts a Share to ShareInfo
func (s *Share) ToInfo() *ShareInfo {
	now := time.Now()
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
		Risk:        s.Risk(now),
	}
}
