// Package caslayout provides directory layout for content-addressable storage
// This is a reusable package, planned for standalone open source release
package caslayout

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode"

	"github.com/seanbao/mnemonas/internal/rootio"
)

var errCASPathSymlink = errors.New("CAS storage path must not traverse a symlink")

var syncDir = syncCASDirectory
var syncRootDir = syncCASRootDirectory
var casRandomRead = rand.Read
var afterValidateCASPath = func() {}

const casRootEscapeError = "path escapes from parent"

// Layout defines the directory layout strategy for CAS storage
type Layout interface {
	// HashToPath converts hash to storage path (relative path)
	HashToPath(hash string) string

	// PathToHash extracts hash from path
	PathToHash(path string) (string, error)

	// FullPath returns the full filesystem path
	FullPath(root, hash string) string
}

// FlatLayout is a flat layout - all files in the same directory
// Suitable for small-scale storage (<100k files)
type FlatLayout struct{}

func (l FlatLayout) HashToPath(hash string) string {
	return hash
}

func (l FlatLayout) PathToHash(path string) (string, error) {
	return filepath.Base(path), nil
}

func (l FlatLayout) FullPath(root, hash string) string {
	return filepath.Join(root, hash)
}

// ShardedLayout is a sharded layout - directories based on hash prefix
// Uses first N characters as directory levels
type ShardedLayout struct {
	Levels    int // number of directory levels
	ShardSize int // characters per level
}

// NewShardedLayout creates a sharded layout
// levels=2, shardSize=2 produces paths like ab/cd/abcd1234...
func NewShardedLayout(levels, shardSize int) *ShardedLayout {
	if levels < 1 {
		levels = 2
	}
	if shardSize < 1 {
		shardSize = 2
	}
	return &ShardedLayout{
		Levels:    levels,
		ShardSize: shardSize,
	}
}

func (l *ShardedLayout) HashToPath(hash string) string {
	if len(hash) < l.Levels*l.ShardSize {
		return hash
	}

	parts := make([]string, 0, l.Levels+1)
	for i := range l.Levels {
		start := i * l.ShardSize
		end := start + l.ShardSize
		parts = append(parts, hash[start:end])
	}
	parts = append(parts, hash)

	return filepath.Join(parts...)
}

func (l *ShardedLayout) PathToHash(path string) (string, error) {
	return filepath.Base(path), nil
}

func (l *ShardedLayout) FullPath(root, hash string) string {
	return filepath.Join(root, l.HashToPath(hash))
}

// Store is the CAS storage implementation
type Store struct {
	root       string
	layout     Layout
	rootHandle *os.Root
}

type casWalkFunc func(relPath string, info os.FileInfo) error

// NewStore creates a CAS store
func NewStore(root string, layout Layout) (*Store, error) {
	if layout == nil {
		layout = NewShardedLayout(2, 2) // default 2 levels, 2 chars each
	}

	normalizedRoot, rootHandle, err := ensureCASRoot(root)
	if err != nil {
		return nil, err
	}

	return &Store{
		root:       normalizedRoot,
		layout:     layout,
		rootHandle: rootHandle,
	}, nil
}

// Put stores data, returns path
func (s *Store) Put(hash string, data []byte) error {
	relPath := filepath.Clean(s.layout.HashToPath(hash))
	path := filepath.Join(s.root, relPath)
	relDir := filepath.Dir(relPath)
	if err := validateCASPath(s.root, path); err != nil {
		return err
	}
	createdDirs, err := ensureCASDirWithRoot(s.rootHandle, relDir, 0755)
	if err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	if err := validateCASPath(s.root, filepath.Join(s.root, relDir)); err != nil {
		return err
	}
	afterValidateCASPath()

	// Write to a temporary file, fsync it, then atomically rename it into place.
	f, tmpPath, err := createCASTempFile(s.rootHandle, relDir)
	if err != nil {
		cleanupCreatedCASDirsWithRoot(s.rootHandle, createdDirs)
		if errors.Is(err, os.ErrPermission) || rootio.IsSymlinkError(err) || isCASRootEscapeError(err) {
			return errCASPathSymlink
		}
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	_, writeErr := f.Write(data)
	syncErr := f.Sync() // fsync data before rename
	closeErr := f.Close()

	if writeErr != nil {
		cleanupCASTempPath(s.rootHandle, tmpPath)
		cleanupCreatedCASDirsWithRoot(s.rootHandle, createdDirs)
		return fmt.Errorf("failed to write temp file: %w", writeErr)
	}
	if syncErr != nil {
		cleanupCASTempPath(s.rootHandle, tmpPath)
		cleanupCreatedCASDirsWithRoot(s.rootHandle, createdDirs)
		return fmt.Errorf("failed to sync temp file: %w", syncErr)
	}
	if closeErr != nil {
		cleanupCASTempPath(s.rootHandle, tmpPath)
		cleanupCreatedCASDirsWithRoot(s.rootHandle, createdDirs)
		return fmt.Errorf("failed to close temp file: %w", closeErr)
	}

	// Step 2: Atomic rename
	if err := s.rootHandle.Rename(tmpPath, relPath); err != nil {
		cleanupCASTempPath(s.rootHandle, tmpPath)
		cleanupCreatedCASDirsWithRoot(s.rootHandle, createdDirs)
		if errors.Is(err, os.ErrPermission) || isCASRootEscapeError(err) {
			return errCASPathSymlink
		}
		return fmt.Errorf("failed to rename file: %w", err)
	}

	// Step 3: fsync directory to ensure rename is persisted
	if err := syncRootDir(s.rootHandle, relDir); err != nil {
		return fmt.Errorf("failed to sync directory: %w", err)
	}

	return nil
}

func syncCreatedCASDirs(createdDirs []string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncDir(filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync directory tree: %w", err)
		}
	}
	return nil
}

func normalizeCASRootPath(root string) (string, error) {
	cleaned := filepath.Clean(root)
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve CAS root path: %w", err)
	}
	return absPath, nil
}

func rejectCASRootPathSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to stat CAS path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errCASPathSymlink
	}
	return nil
}

func validateCASRootPath(root string) error {
	current := filepath.VolumeName(root) + string(filepath.Separator)
	trimmed := strings.TrimPrefix(root, current)
	if trimmed == "" {
		return rejectCASRootPathSymlink(root)
	}

	if err := rejectCASRootPathSymlink(current); err != nil {
		return err
	}
	for _, part := range strings.Split(trimmed, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if err := rejectCASRootPathSymlink(current); err != nil {
			return err
		}
	}
	return nil
}

func ensureCASDir(dir string, perm os.FileMode) error {
	createdDirs, err := rootio.MkdirAllPathNoFollowTracked(dir, perm)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return errCASPathSymlink
		}
		return err
	}
	return syncCreatedCASDirs(createdDirs)
}

func ensureCASRoot(root string) (string, *os.Root, error) {
	normalizedRoot, err := normalizeCASRootPath(root)
	if err != nil {
		return "", nil, err
	}
	if err := validateCASRootPath(normalizedRoot); err != nil {
		return "", nil, err
	}
	if err := ensureCASDir(normalizedRoot, 0755); err != nil {
		return "", nil, fmt.Errorf("failed to create storage directory: %w", err)
	}
	if err := validateCASRootPath(normalizedRoot); err != nil {
		return "", nil, err
	}

	rootHandle, err := os.OpenRoot(normalizedRoot)
	if err != nil {
		return "", nil, fmt.Errorf("failed to open storage root: %w", err)
	}
	return normalizedRoot, rootHandle, nil
}

func syncCreatedCASDirsWithRoot(root *os.Root, createdDirs []string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncRootDir(root, filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync directory tree: %w", err)
		}
	}
	return nil
}

func ensureCASDirWithRoot(root *os.Root, dir string, perm os.FileMode) ([]string, error) {
	if dir == "." {
		return nil, nil
	}

	createdDirs, err := rootio.MkdirAllNoFollowTracked(root, dir, perm)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return createdDirs, errCASPathSymlink
		}
		return createdDirs, err
	}
	return createdDirs, syncCreatedCASDirsWithRoot(root, createdDirs)
}

func syncCASDirectory(dir string) error {
	parentDir, err := rootio.OpenDirPathNoFollow(dir)
	if err != nil {
		return err
	}

	if err := parentDir.Sync(); err != nil {
		_ = parentDir.Close()
		return err
	}

	return parentDir.Close()
}

func syncCASRootDirectory(root *os.Root, dir string) error {
	parentDir, err := rootio.OpenDirNoFollow(root, dir)
	if err != nil {
		return err
	}

	if err := parentDir.Sync(); err != nil {
		_ = parentDir.Close()
		return err
	}

	return parentDir.Close()
}

func readCASFileWithRoot(root *os.Root, path string) ([]byte, error) {
	file, err := rootio.OpenFileNoFollow(root, path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return io.ReadAll(file)
}

func createCASTempFile(root *os.Root, dir string) (*os.File, string, error) {
	tmpName, err := newCASTempName()
	if err != nil {
		return nil, "", err
	}

	tmpPath := filepath.Join(dir, tmpName)
	file, err := rootio.OpenFileNoFollow(root, tmpPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, "", err
	}

	return file, tmpPath, nil
}

func newCASTempName() (string, error) {
	random := make([]byte, 8)
	if _, err := casRandomRead(random); err != nil {
		return "", err
	}
	return ".cas-" + hex.EncodeToString(random) + ".tmp", nil
}

func cleanupCASTempPath(root *os.Root, path string) {
	_ = root.Remove(path)
}

func cleanupCreatedCASDirsWithRoot(root *os.Root, createdDirs []string) {
	for _, dir := range createdDirs {
		if dir == "." {
			continue
		}
		if err := root.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			break
		}
	}
}

func isCASRootEscapeError(err error) bool {
	return err != nil && strings.Contains(err.Error(), casRootEscapeError)
}

// Get reads data
func (s *Store) Get(hash string) ([]byte, error) {
	relPath := filepath.Clean(s.layout.HashToPath(hash))
	path := filepath.Join(s.root, relPath)
	if err := validateCASPath(s.root, path); err != nil {
		return nil, err
	}
	afterValidateCASPath()
	data, err := readCASFileWithRoot(s.rootHandle, relPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ELOOP) || rootio.IsSymlinkError(err) || isCASRootEscapeError(err) {
			return nil, errCASPathSymlink
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return data, nil
}

// Has checks if data exists
func (s *Store) Has(hash string) bool {
	relPath := filepath.Clean(s.layout.HashToPath(hash))
	path := filepath.Join(s.root, relPath)
	if err := validateCASPath(s.root, path); err != nil {
		return false
	}
	afterValidateCASPath()
	info, err := s.rootHandle.Lstat(relPath)
	return err == nil && info.Mode()&os.ModeSymlink == 0
}

// Delete removes data
func (s *Store) Delete(hash string) error {
	relPath := filepath.Clean(s.layout.HashToPath(hash))
	path := filepath.Join(s.root, relPath)
	if err := validateCASPath(s.root, path); err != nil {
		return err
	}
	afterValidateCASPath()
	relDir := filepath.Dir(relPath)
	err := s.rootHandle.Remove(relPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		if errors.Is(err, os.ErrPermission) || isCASRootEscapeError(err) {
			return errCASPathSymlink
		}
		return fmt.Errorf("failed to delete file: %w", err)
	}
	if err == nil {
		if err := syncRootDir(s.rootHandle, relDir); err != nil {
			return fmt.Errorf("failed to sync directory: %w", err)
		}
	}
	return nil
}

// Size gets data size
func (s *Store) Size(hash string) (int64, error) {
	relPath := filepath.Clean(s.layout.HashToPath(hash))
	path := filepath.Join(s.root, relPath)
	if err := validateCASPath(s.root, path); err != nil {
		return 0, err
	}
	afterValidateCASPath()
	info, err := s.rootHandle.Lstat(relPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, ErrNotFound
		}
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ELOOP) || rootio.IsSymlinkError(err) || isCASRootEscapeError(err) {
			return 0, errCASPathSymlink
		}
		return 0, fmt.Errorf("failed to get file info: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, errCASPathSymlink
	}
	return info.Size(), nil
}

// ReadSeekCloser combines io.ReadSeeker with io.Closer
type ReadSeekCloser interface {
	io.ReadSeeker
	io.Closer
}

// Reader returns a data reader with seek support for Range requests
func (s *Store) Reader(hash string) (ReadSeekCloser, error) {
	relPath := filepath.Clean(s.layout.HashToPath(hash))
	path := filepath.Join(s.root, relPath)
	if err := validateCASPath(s.root, path); err != nil {
		return nil, err
	}
	afterValidateCASPath()
	f, err := rootio.OpenFileNoFollow(s.rootHandle, relPath, os.O_RDONLY, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ELOOP) || rootio.IsSymlinkError(err) || isCASRootEscapeError(err) {
			return nil, errCASPathSymlink
		}
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	return f, nil
}

func validateCASPath(root, path string) error {
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(path)
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return fmt.Errorf("failed to resolve CAS path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("CAS path escapes storage root: %s", cleanPath)
	}

	current := cleanRoot
	if err := rejectCASPathSymlink(current); err != nil {
		return err
	}
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if err := rejectCASPathSymlink(current); err != nil {
			return err
		}
	}
	return nil
}

func rejectCASPathSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to stat CAS path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errCASPathSymlink
	}
	return nil
}

func casChildRelativePath(parent, child string) string {
	if parent == "." {
		return child
	}
	return filepath.Join(parent, child)
}

func safeCASDirEntryName(name string) (string, error) {
	normalized := strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.Contains(normalized, "/") || strings.IndexFunc(normalized, unicode.IsControl) >= 0 {
		return "", errCASPathSymlink
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "." || segment == ".." {
			return "", errCASPathSymlink
		}
	}
	return name, nil
}

func (s *Store) casFullPath(relPath string) string {
	if relPath == "." {
		return s.root
	}
	return filepath.Join(s.root, relPath)
}

func (s *Store) walkWithRoot(fn casWalkFunc) error {
	afterValidateCASPath()
	rootInfo, err := s.rootHandle.Lstat(".")
	if err != nil {
		if errors.Is(err, os.ErrPermission) || isCASRootEscapeError(err) {
			return errCASPathSymlink
		}
		return err
	}
	return s.walkRootEntry(".", rootInfo, fn)
}

func (s *Store) walkRootEntry(relPath string, info os.FileInfo, fn casWalkFunc) error {
	if err := fn(relPath, info); err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}

	dirHandle, err := rootio.OpenDirNoFollow(s.rootHandle, relPath)
	if err != nil {
		if errors.Is(err, os.ErrPermission) || isCASRootEscapeError(err) {
			return errCASPathSymlink
		}
		return err
	}
	defer dirHandle.Close()

	entries, err := dirHandle.ReadDir(-1)
	if err != nil {
		if errors.Is(err, os.ErrPermission) || isCASRootEscapeError(err) {
			return errCASPathSymlink
		}
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		entryName, err := safeCASDirEntryName(entry.Name())
		if err != nil {
			return err
		}
		childRelPath := casChildRelativePath(relPath, entryName)
		childInfo, err := s.rootHandle.Lstat(childRelPath)
		if err != nil {
			if errors.Is(err, os.ErrPermission) || rootio.IsSymlinkError(err) || isCASRootEscapeError(err) {
				return errCASPathSymlink
			}
			return err
		}
		if err := s.walkRootEntry(childRelPath, childInfo, fn); err != nil {
			return err
		}
	}

	return nil
}

// Walk iterates over all stored hashes
func (s *Store) Walk(fn func(hash string) error) error {
	return s.walkWithRoot(func(relPath string, info os.FileInfo) error {
		if info.IsDir() {
			return nil
		}
		path := s.casFullPath(relPath)
		// Skip temp files
		if strings.HasSuffix(path, ".tmp") {
			return nil
		}
		if !s.matchesLayoutPath(path) {
			return nil
		}

		hash, err := s.layout.PathToHash(path)
		if err != nil {
			return nil // skip files that cannot be parsed
		}

		return fn(hash)
	})
}

// Stats holds storage statistics
type Stats struct {
	TotalObjects int64
	TotalSize    int64
}

// Stats returns storage statistics
func (s *Store) Stats() (*Stats, error) {
	stats := &Stats{}

	err := s.walkWithRoot(func(relPath string, info os.FileInfo) error {
		path := s.casFullPath(relPath)
		if info.IsDir() || strings.HasSuffix(path, ".tmp") {
			return nil
		}

		if !s.matchesLayoutPath(path) {
			return nil
		}

		stats.TotalObjects++
		stats.TotalSize += info.Size()
		return nil
	})

	return stats, err
}

func (s *Store) matchesLayoutPath(path string) bool {
	sharded, ok := s.layout.(*ShardedLayout)
	if !ok {
		return true
	}

	relPath, err := filepath.Rel(s.root, path)
	if err != nil {
		return false
	}
	if relPath == "." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) || relPath == ".." {
		return false
	}

	hash := filepath.Base(relPath)
	if len(hash) < sharded.Levels*sharded.ShardSize {
		return filepath.Dir(relPath) == "."
	}

	current := filepath.Dir(relPath)
	for level := sharded.Levels - 1; level >= 0; level-- {
		expected := hash[level*sharded.ShardSize : (level+1)*sharded.ShardSize]
		if filepath.Base(current) != expected {
			return false
		}
		current = filepath.Dir(current)
	}

	return true
}

// CleanupStaging removes incomplete staging files (.tmp files)
// Returns the number of files cleaned and total size freed
func (s *Store) CleanupStaging() (count int, size int64, err error) {
	err = s.walkWithRoot(func(relPath string, info os.FileInfo) error {
		if info.IsDir() {
			return nil
		}
		path := s.casFullPath(relPath)
		if !strings.HasSuffix(path, ".tmp") {
			return nil
		}

		// Remove staging file
		if removeErr := s.rootHandle.Remove(relPath); removeErr == nil {
			count++
			size += info.Size()
		} else if errors.Is(removeErr, os.ErrPermission) || isCASRootEscapeError(removeErr) {
			return errCASPathSymlink
		}
		return nil
	})
	return
}

// Root returns the storage root directory
func (s *Store) Root() string {
	return s.root
}

// Error definitions
var (
	ErrNotFound = errors.New("object not found")
)
