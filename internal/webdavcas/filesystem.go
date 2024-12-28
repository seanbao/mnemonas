// Package webdavcas provides WebDAV to CAS storage adapter layer
// Converts WebDAV file operations to CAS content-addressable operations
package webdavcas

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/seanbao/mnemonas/internal/caslayout"
	"github.com/zeebo/blake3"
)

// FileSystem implements a WebDAV filesystem with version history support
type FileSystem struct {
	cas      *caslayout.Store
	metadata *MetadataStore
	trash    *TrashStore
	mu       sync.RWMutex
}

// NewFileSystem creates a CAS-backed filesystem
func NewFileSystem(casRoot, metadataRoot string) (*FileSystem, error) {
	cas, err := caslayout.NewStore(casRoot, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create CAS store: %w", err)
	}

	metadata, err := NewMetadataStore(metadataRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to create metadata store: %w", err)
	}

	// Initialize trash store in a subdirectory of metadata
	trashRoot := path.Join(metadataRoot, ".trash")
	trash, err := NewTrashStore(trashRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to create trash store: %w", err)
	}

	return &FileSystem{
		cas:      cas,
		metadata: metadata,
		trash:    trash,
	}, nil
}

// CleanupStaging removes incomplete staging files from CAS and metadata stores
// Should be called on startup to clean up after crashes
// Returns total files cleaned and bytes freed
func (fs *FileSystem) CleanupStaging(ctx context.Context) (files int, bytes int64, err error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Cleanup CAS staging files
	casFiles, casBytes, casErr := fs.cas.CleanupStaging()
	if casErr != nil {
		return 0, 0, fmt.Errorf("failed to cleanup CAS staging: %w", casErr)
	}
	files += casFiles
	bytes += casBytes

	// Cleanup metadata staging files
	metaFiles, metaBytes, metaErr := fs.metadata.CleanupStaging()
	if metaErr != nil {
		return files, bytes, fmt.Errorf("failed to cleanup metadata staging: %w", metaErr)
	}
	files += metaFiles
	bytes += metaBytes

	return files, bytes, nil
}

// MaxVersions is the maximum number of versions to retain per file (REM-8)
const MaxVersions = 100

// MaxFileSize is the maximum file size for Go-side processing (V3-1)
// Larger files should use streaming through Rust data plane
const MaxFileSize = 100 * 1024 * 1024 // 100MB

// FileInfo holds file metadata
type FileInfo struct {
	Path        string       `json:"path"`
	IsDir       bool         `json:"is_dir"`
	Size        int64        `json:"size"`
	ModTime     time.Time    `json:"mod_time"`
	ContentHash string       `json:"content_hash,omitempty"` // CAS address
	Versions    []VersionRef `json:"versions,omitempty"`     // version history
}

// VersionRef holds a version reference
type VersionRef struct {
	Hash      string    `json:"hash"`
	Size      int64     `json:"size"`
	Timestamp time.Time `json:"timestamp"`
	Comment   string    `json:"comment,omitempty"`
}

// Stat gets file info
func (fs *FileSystem) Stat(ctx context.Context, name string) (*FileInfo, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	name = cleanPath(name)

	// Return virtual root directory info
	if name == "/" {
		return &FileInfo{
			Path:    "/",
			IsDir:   true,
			ModTime: time.Now(),
		}, nil
	}

	return fs.metadata.Get(name)
}

// ReadDir reads directory contents
func (fs *FileSystem) ReadDir(ctx context.Context, name string) ([]*FileInfo, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	name = cleanPath(name)
	return fs.metadata.List(name)
}

// OpenFile opens a file for reading with seek support for Range requests
func (fs *FileSystem) OpenFile(ctx context.Context, name string) (caslayout.ReadSeekCloser, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	name = cleanPath(name)
	info, err := fs.metadata.Get(name)
	if err != nil {
		return nil, err
	}

	if info.IsDir {
		return nil, errors.New("cannot open directory")
	}

	return fs.cas.Reader(info.ContentHash)
}

// WriteFile writes a file (creates new version)
func (fs *FileSystem) WriteFile(ctx context.Context, name string, r io.Reader) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	name = cleanPath(name)

	// V3-1 fix: Limit file size to prevent OOM
	limitedReader := io.LimitReader(r, MaxFileSize+1)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return fmt.Errorf("failed to read data: %w", err)
	}
	if len(data) > MaxFileSize {
		return fmt.Errorf("file too large: max %d bytes", MaxFileSize)
	}

	// Compute hash (simple method for now, will integrate with Rust data plane later)
	hash := computeHash(data)

	// Store to CAS
	if err := fs.cas.Put(hash, data); err != nil {
		return fmt.Errorf("failed to store data: %w", err)
	}

	// Update metadata
	now := time.Now()
	existing, err := fs.metadata.Get(name)

	var versions []VersionRef
	if err == nil && existing != nil {
		// Keep old versions
		versions = existing.Versions
		if existing.ContentHash != "" {
			versions = append([]VersionRef{{
				Hash:      existing.ContentHash,
				Size:      existing.Size,
				Timestamp: existing.ModTime,
			}}, versions...)
		}
		// REM-8 fix: Limit version history to prevent unbounded growth
		if len(versions) > MaxVersions {
			versions = versions[:MaxVersions]
		}
	}

	info := &FileInfo{
		Path:        name,
		IsDir:       false,
		Size:        int64(len(data)),
		ModTime:     now,
		ContentHash: hash,
		Versions:    versions,
	}

	return fs.metadata.Put(name, info)
}

// Mkdir creates a directory
func (fs *FileSystem) Mkdir(ctx context.Context, name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	name = cleanPath(name)

	// Check parent directory exists
	parent := path.Dir(name)
	if parent != "/" && parent != "." {
		parentInfo, err := fs.metadata.Get(parent)
		if err != nil {
			return fmt.Errorf("parent directory not found: %s", parent)
		}
		if !parentInfo.IsDir {
			return fmt.Errorf("parent path is not a directory: %s", parent)
		}
	}

	info := &FileInfo{
		Path:    name,
		IsDir:   true,
		ModTime: time.Now(),
	}

	return fs.metadata.Put(name, info)
}

// Delete deletes a file or directory (soft delete to trash)
func (fs *FileSystem) Delete(ctx context.Context, name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	name = cleanPath(name)

	info, err := fs.metadata.Get(name)
	if err != nil {
		return err
	}

	if info.IsDir {
		// Check if directory is empty
		children, err := fs.metadata.List(name)
		if err != nil {
			return err
		}
		if len(children) > 0 {
			return errors.New("directory not empty")
		}
	}

	// Soft delete: move to trash instead of permanent deletion
	if _, err := fs.trash.Add(name, info); err != nil {
		return fmt.Errorf("failed to move to trash: %w", err)
	}

	// Remove from active metadata
	return fs.metadata.Delete(name)
}

// PermanentDelete permanently deletes a file (bypasses trash)
func (fs *FileSystem) PermanentDelete(ctx context.Context, name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	name = cleanPath(name)

	info, err := fs.metadata.Get(name)
	if err != nil {
		return err
	}

	if info.IsDir {
		// Check if directory is empty
		children, err := fs.metadata.List(name)
		if err != nil {
			return err
		}
		if len(children) > 0 {
			return errors.New("directory not empty")
		}
	}

	// Direct deletion without trash
	return fs.metadata.Delete(name)
}

// === Trash Operations ===

// ListTrash returns all items in the trash
func (fs *FileSystem) ListTrash(ctx context.Context) ([]*TrashItem, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.trash.List()
}

// GetTrashItem returns a specific trash item
func (fs *FileSystem) GetTrashItem(ctx context.Context, id string) (*TrashItem, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.trash.Get(id)
}

// RestoreFromTrash restores a file from the trash to its original location
func (fs *FileSystem) RestoreFromTrash(ctx context.Context, id string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	item, err := fs.trash.Get(id)
	if err != nil {
		return err
	}

	// Check if original path already exists
	if _, err := fs.metadata.Get(item.OriginalPath); err == nil {
		return fmt.Errorf("cannot restore: path already exists: %s", item.OriginalPath)
	}

	// Check if parent directory exists
	parent := path.Dir(item.OriginalPath)
	if parent != "/" && parent != "." {
		if _, err := fs.metadata.Get(parent); err != nil {
			return fmt.Errorf("cannot restore: parent directory does not exist: %s", parent)
		}
	}

	// Restore metadata
	if err := fs.metadata.Put(item.OriginalPath, &item.FileInfo); err != nil {
		return fmt.Errorf("failed to restore metadata: %w", err)
	}

	// Remove from trash
	if err := fs.trash.Remove(id); err != nil {
		// Rollback: delete restored metadata
		_ = fs.metadata.Delete(item.OriginalPath)
		return fmt.Errorf("failed to remove from trash: %w", err)
	}

	return nil
}

// RestoreFromTrashTo restores a file from the trash to a custom location
func (fs *FileSystem) RestoreFromTrashTo(ctx context.Context, id string, newPath string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	newPath = cleanPath(newPath)

	item, err := fs.trash.Get(id)
	if err != nil {
		return err
	}

	// Check if target path already exists
	if _, err := fs.metadata.Get(newPath); err == nil {
		return fmt.Errorf("cannot restore: path already exists: %s", newPath)
	}

	// Check if parent directory exists
	parent := path.Dir(newPath)
	if parent != "/" && parent != "." {
		if _, err := fs.metadata.Get(parent); err != nil {
			return fmt.Errorf("cannot restore: parent directory does not exist: %s", parent)
		}
	}

	// Update path and restore
	restoredInfo := item.FileInfo
	restoredInfo.Path = newPath

	if err := fs.metadata.Put(newPath, &restoredInfo); err != nil {
		return fmt.Errorf("failed to restore metadata: %w", err)
	}

	// Remove from trash
	if err := fs.trash.Remove(id); err != nil {
		// Rollback
		_ = fs.metadata.Delete(newPath)
		return fmt.Errorf("failed to remove from trash: %w", err)
	}

	return nil
}

// DeleteFromTrash permanently deletes a single item from the trash
func (fs *FileSystem) DeleteFromTrash(ctx context.Context, id string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.trash.Remove(id)
}

// EmptyTrash permanently deletes all items from the trash
func (fs *FileSystem) EmptyTrash(ctx context.Context) (int, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.trash.Clear()
}

// CleanupExpiredTrash removes trash items older than the retention period
func (fs *FileSystem) CleanupExpiredTrash(ctx context.Context, retentionDays int) (int, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.trash.CleanupExpired(retentionDays)
}

// GetTrashStats returns trash statistics
func (fs *FileSystem) GetTrashStats(ctx context.Context) (count int, totalSize int64, err error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	count, err = fs.trash.Count()
	if err != nil {
		return
	}

	totalSize, err = fs.trash.TotalSize()
	return
}

// Rename renames/moves a file or directory
func (fs *FileSystem) Rename(ctx context.Context, oldName, newName string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	oldName = cleanPath(oldName)
	newName = cleanPath(newName)

	info, err := fs.metadata.Get(oldName)
	if err != nil {
		return err
	}

	// Update path
	info.Path = newName
	info.ModTime = time.Now()

	// C5 fix: Atomic rename using write-ahead approach
	// Step 1: Write new metadata first (if crash here, old still exists, no data loss)
	if err := fs.metadata.Put(newName, info); err != nil {
		return err
	}

	// Step 2: Delete old metadata
	// If crash here, file exists at both paths - acceptable, can be cleaned up
	if err := fs.metadata.Delete(oldName); err != nil {
		// Rollback: try to delete the new entry
		if rollbackErr := fs.metadata.Delete(newName); rollbackErr != nil {
			log.Printf("webdavcas: rename rollback failed for %s: %v", newName, rollbackErr)
		}
		return fmt.Errorf("rename failed during delete: %w", err)
	}

	return nil
}

// GetVersion gets a specific version of a file
func (fs *FileSystem) GetVersion(ctx context.Context, name string, versionHash string) (io.ReadCloser, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	return fs.cas.Reader(versionHash)
}

// ListVersions lists all versions of a file
func (fs *FileSystem) ListVersions(ctx context.Context, name string) ([]VersionRef, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	name = cleanPath(name)
	info, err := fs.metadata.Get(name)
	if err != nil {
		return nil, err
	}

	// Current version + historical versions
	all := []VersionRef{{
		Hash:      info.ContentHash,
		Size:      info.Size,
		Timestamp: info.ModTime,
	}}
	all = append(all, info.Versions...)

	return all, nil
}

// RestoreVersion restores a file to a specific version
func (fs *FileSystem) RestoreVersion(ctx context.Context, name string, versionHash string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	name = cleanPath(name)
	info, err := fs.metadata.Get(name)
	if err != nil {
		return err
	}

	// Check if version exists
	if !fs.cas.Has(versionHash) {
		return fmt.Errorf("version not found: %s", versionHash)
	}

	size, err := fs.cas.Size(versionHash)
	if err != nil {
		return err
	}

	// Add current version to history
	if info.ContentHash != "" && info.ContentHash != versionHash {
		info.Versions = append([]VersionRef{{
			Hash:      info.ContentHash,
			Size:      info.Size,
			Timestamp: info.ModTime,
		}}, info.Versions...)
	}

	// Update to target version
	info.ContentHash = versionHash
	info.Size = size
	info.ModTime = time.Now()

	return fs.metadata.Put(name, info)
}

// GetAllReferencedHashes returns all CAS hashes referenced by current metadata
// Used for GC to identify unreferenced objects
// Includes both active files and trash items to prevent premature cleanup
func (fs *FileSystem) GetAllReferencedHashes(ctx context.Context) ([]string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	hashSet := make(map[string]struct{})

	// Collect hashes from active metadata
	entries, err := os.ReadDir(fs.metadata.root)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !hasExtension(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(path.Join(fs.metadata.root, entry.Name()))
		if err != nil {
			continue
		}

		var info FileInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}

		// Collect current content hash
		if info.ContentHash != "" {
			hashSet[info.ContentHash] = struct{}{}
		}

		// Collect version hashes
		for _, v := range info.Versions {
			if v.Hash != "" {
				hashSet[v.Hash] = struct{}{}
			}
		}
	}

	// Collect hashes from trash items (prevent GC from deleting recoverable files)
	trashItems, err := fs.trash.List()
	if err == nil {
		for _, item := range trashItems {
			if item.FileInfo.ContentHash != "" {
				hashSet[item.FileInfo.ContentHash] = struct{}{}
			}
			for _, v := range item.FileInfo.Versions {
				if v.Hash != "" {
					hashSet[v.Hash] = struct{}{}
				}
			}
		}
	}

	hashes := make([]string, 0, len(hashSet))
	for h := range hashSet {
		hashes = append(hashes, h)
	}

	return hashes, nil
}

// MetadataStore is the metadata storage
type MetadataStore struct {
	root string
}

// NewMetadataStore creates a metadata store
func NewMetadataStore(root string) (*MetadataStore, error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	return &MetadataStore{root: root}, nil
}

func (m *MetadataStore) metaPath(name string) string {
	// V3-3 fix: Convert path to safe filename using URL encoding
	// This ensures paths like "/foo/bar" don't create subdirectories
	safeName := path.Clean(name)
	if safeName == "/" || safeName == "." {
		safeName = "_root_"
	} else {
		// Replace / with encoded form to keep flat structure
		safeName = strings.ReplaceAll(safeName, "/", "%2F")
	}
	return path.Join(m.root, safeName+".json")
}

// Get retrieves metadata
func (m *MetadataStore) Get(name string) (*FileInfo, error) {
	data, err := os.ReadFile(m.metaPath(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, caslayout.ErrNotFound
		}
		return nil, err
	}

	var info FileInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}

	return &info, nil
}

// Put stores metadata with atomic write (REM-6 fix)
func (m *MetadataStore) Put(name string, info *FileInfo) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}

	metaPath := m.metaPath(name)
	if err := os.MkdirAll(path.Dir(metaPath), 0755); err != nil {
		return err
	}

	// REM-6 fix: Atomic write with fsync (same pattern as CAS)
	tmpPath := metaPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp metadata file: %w", err)
	}

	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()

	if writeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write metadata: %w", writeErr)
	}
	if syncErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to sync metadata: %w", syncErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close metadata file: %w", closeErr)
	}

	if err := os.Rename(tmpPath, metaPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename metadata file: %w", err)
	}

	return nil
}

// Delete removes metadata
func (m *MetadataStore) Delete(name string) error {
	return os.Remove(m.metaPath(name))
}

// List lists directory contents
func (m *MetadataStore) List(dir string) ([]*FileInfo, error) {
	var results []*FileInfo

	entries, err := os.ReadDir(m.root)
	if err != nil {
		return nil, err
	}

	dir = cleanPath(dir)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Read metadata
		name := entry.Name()
		if !hasExtension(name, ".json") {
			continue
		}

		data, err := os.ReadFile(path.Join(m.root, name))
		if err != nil {
			continue
		}

		var info FileInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}

		// Check if in specified directory
		parent := path.Dir(info.Path)
		if parent == dir || (dir == "/" && parent == ".") {
			results = append(results, &info)
		}
	}

	return results, nil
}

// CleanupStaging removes incomplete staging files (.tmp files)
// Returns the number of files cleaned and total size freed
func (m *MetadataStore) CleanupStaging() (count int, size int64, err error) {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return 0, 0, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".tmp") {
			continue
		}

		fullPath := path.Join(m.root, name)
		info, infoErr := entry.Info()
		if infoErr == nil {
			size += info.Size()
		}

		if removeErr := os.Remove(fullPath); removeErr == nil {
			count++
		}
	}

	return count, size, nil
}

// Helper functions

func cleanPath(p string) string {
	p = path.Clean(p)
	if p == "." {
		return "/"
	}
	if !path.IsAbs(p) {
		p = "/" + p
	}
	return p
}

func hasExtension(name, ext string) bool {
	return len(name) > len(ext) && name[len(name)-len(ext):] == ext
}

// computeHash generates BLAKE3 hash (REM-2 fix: consistent with Rust data plane)
func computeHash(data []byte) string {
	h := blake3.Sum256(data)
	return hex.EncodeToString(h[:])
}
