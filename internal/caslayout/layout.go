// Package caslayout provides directory layout for content-addressable storage
// This is a reusable package, planned for standalone open source release
package caslayout

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var errCASPathSymlink = errors.New("CAS storage path must not traverse a symlink")

var syncDir = syncCASDirectory

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
	root   string
	layout Layout
}

// NewStore creates a CAS store
func NewStore(root string, layout Layout) (*Store, error) {
	if layout == nil {
		layout = NewShardedLayout(2, 2) // default 2 levels, 2 chars each
	}

	if err := validateCASPath(root, root); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	return &Store{
		root:   root,
		layout: layout,
	}, nil
}

// Put stores data, returns path
func (s *Store) Put(hash string, data []byte) error {
	path := s.layout.FullPath(s.root, hash)
	dir := filepath.Dir(path)
	if err := validateCASPath(s.root, path); err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	if err := validateCASPath(s.root, dir); err != nil {
		return err
	}

	// I1 fix: Atomic write with proper fsync
	// Step 1: Write to temp file
	f, err := os.CreateTemp(dir, ".cas-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := f.Name()

	_, writeErr := f.Write(data)
	syncErr := f.Sync() // fsync data before rename
	closeErr := f.Close()

	if writeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write temp file: %w", writeErr)
	}
	if syncErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to sync temp file: %w", syncErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", closeErr)
	}

	// Step 2: Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename file: %w", err)
	}

	// Step 3: fsync directory to ensure rename is persisted
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("failed to sync directory: %w", err)
	}

	return nil
}

func syncCASDirectory(dir string) error {
	parentDir, err := os.Open(dir)
	if err != nil {
		return err
	}

	if err := parentDir.Sync(); err != nil {
		_ = parentDir.Close()
		return err
	}

	return parentDir.Close()
}

// Get reads data
func (s *Store) Get(hash string) ([]byte, error) {
	path := s.layout.FullPath(s.root, hash)
	if err := validateCASPath(s.root, path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return data, nil
}

// Has checks if data exists
func (s *Store) Has(hash string) bool {
	path := s.layout.FullPath(s.root, hash)
	if err := validateCASPath(s.root, path); err != nil {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// Delete removes data
func (s *Store) Delete(hash string) error {
	path := s.layout.FullPath(s.root, hash)
	if err := validateCASPath(s.root, path); err != nil {
		return err
	}
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	return nil
}

// Size gets data size
func (s *Store) Size(hash string) (int64, error) {
	path := s.layout.FullPath(s.root, hash)
	if err := validateCASPath(s.root, path); err != nil {
		return 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("failed to get file info: %w", err)
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
	path := s.layout.FullPath(s.root, hash)
	if err := validateCASPath(s.root, path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
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

// Walk iterates over all stored hashes
func (s *Store) Walk(fn func(hash string) error) error {
	return filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
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

	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasSuffix(path, ".tmp") {
			return nil
		}

		if !s.matchesLayoutPath(path) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil // ignore files that cannot be read
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
	err = filepath.WalkDir(s.root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".tmp") {
			return nil
		}

		// Get file size before deletion
		info, infoErr := d.Info()
		if infoErr == nil {
			size += info.Size()
		}

		// Remove staging file
		if removeErr := os.Remove(path); removeErr == nil {
			count++
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
