// Package storage provides unified storage layer for MnemoNAS
// Combines workspace (native files) with versionstore (SQLite-based versioning)
package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/versionstore"
	"github.com/seanbao/mnemonas/internal/workspace"
	"github.com/zeebo/blake3"
)

// Common errors
var (
	ErrNotFound        = errors.New("not found")
	ErrIsDir           = errors.New("path is a directory")
	ErrNotDir          = errors.New("path is not a directory")
	ErrDirNotEmpty     = errors.New("directory not empty")
	ErrAlreadyExists   = errors.New("already exists")
	ErrFileLocked      = errors.New("file is locked")
	ErrVersionNotFound = errors.New("version not found")
)

// FileInfo represents file metadata
type FileInfo struct {
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	IsDir       bool      `json:"is_dir"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time"`
	ContentHash string    `json:"content_hash,omitempty"`
	Versioned   bool      `json:"versioned"` // Whether this file has version management
}

// VersionRef represents a version reference
type VersionRef struct {
	Hash      string    `json:"hash"`
	Size      int64     `json:"size"`
	Timestamp time.Time `json:"timestamp"`
	Comment   string    `json:"comment,omitempty"`
}

// TrashItem represents a deleted file
type TrashItem struct {
	ID           string    `json:"id"`
	OriginalPath string    `json:"original_path"`
	Size         int64     `json:"size"`
	DeletedAt    time.Time `json:"deleted_at"`
	IsDir        bool      `json:"is_dir"`
	HadVersions  bool      `json:"had_versions"`
}

// SearchResult represents a search result
type SearchResult struct {
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	IsDir       bool      `json:"is_dir"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time"`
	ContentHash string    `json:"hash,omitempty"`
}

// Config holds storage configuration
type Config struct {
	// FilesRoot is the root directory for user files
	FilesRoot string
	// InternalRoot is the root directory for .mnemonas internal data
	InternalRoot string
	// TrashRoot is the root directory for trash content
	TrashRoot string

	// Dataplane is the Rust dataplane client (required)
	Dataplane *dataplane.Client

	// Versioning policy configuration
	AutoVersionedExtensions []string
	AutoVersionedFilenames  []string
	MaxVersionedSize        int64

	// Retention policy
	MaxVersions        int
	MaxVersionAge      time.Duration
	TrashRetentionDays int
}

// FileSystem provides unified storage operations
type FileSystem struct {
	workspace *workspace.Workspace
	versions  *versionstore.Store
	policy    *versionstore.VersioningPolicy
	trashRoot string
	config    *Config
	mu        sync.RWMutex
}

// New creates a new FileSystem
func New(cfg *Config) (*FileSystem, error) {
	if cfg.Dataplane == nil {
		return nil, errors.New("dataplane client is required")
	}

	// Create workspace for native file operations
	ws, err := workspace.New(cfg.FilesRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}

	// Create version store
	vs, err := versionstore.New(versionstore.Config{
		DBPath:    path.Join(cfg.InternalRoot, "index.db"),
		Dataplane: cfg.Dataplane,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create version store: %w", err)
	}

	// Create versioning policy
	policy := versionstore.DefaultVersioningPolicy(vs)
	if len(cfg.AutoVersionedExtensions) > 0 {
		policy.AutoVersionedExtensions = cfg.AutoVersionedExtensions
	}
	if len(cfg.AutoVersionedFilenames) > 0 {
		policy.AutoVersionedFilenames = cfg.AutoVersionedFilenames
	}
	if cfg.MaxVersionedSize > 0 {
		policy.MaxVersionedSize = cfg.MaxVersionedSize
	}

	// Ensure trash directory exists
	if err := os.MkdirAll(cfg.TrashRoot, 0700); err != nil {
		return nil, fmt.Errorf("failed to create trash directory: %w", err)
	}

	return &FileSystem{
		workspace: ws,
		versions:  vs,
		policy:    policy,
		trashRoot: cfg.TrashRoot,
		config:    cfg,
	}, nil
}

// Close closes the filesystem
func (fs *FileSystem) Close() error {
	return fs.versions.Close()
}

// ============================================================================
// File Operations
// ============================================================================

// Stat returns file info
func (fs *FileSystem) Stat(ctx context.Context, name string) (*FileInfo, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	name = workspace.CleanPath(name)

	// Handle root directory
	if name == "/" {
		return &FileInfo{
			Path:    "/",
			Name:    "/",
			IsDir:   true,
			ModTime: time.Now(),
		}, nil
	}

	info, err := fs.workspace.Stat(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	fileInfo := &FileInfo{
		Path:    info.Path,
		Name:    info.Name,
		IsDir:   info.IsDir,
		Size:    info.Size,
		ModTime: info.ModTime,
	}

	// Check if file has versioning
	if !info.IsDir {
		fileInfo.Versioned = fs.policy.ShouldVersion(ctx, name, info.Size)
		// Compute hash for non-directory files
		if data, err := fs.workspace.ReadFile(ctx, name); err == nil {
			fileInfo.ContentHash = computeHash(data)
		}
	}

	return fileInfo, nil
}

// ReadDir reads directory contents
func (fs *FileSystem) ReadDir(ctx context.Context, name string) ([]*FileInfo, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	name = workspace.CleanPath(name)

	entries, err := fs.workspace.ReadDir(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	result := make([]*FileInfo, 0, len(entries))
	for _, e := range entries {
		info := &FileInfo{
			Path:    e.Path,
			Name:    e.Name,
			IsDir:   e.IsDir,
			Size:    e.Size,
			ModTime: e.ModTime,
		}
		if !e.IsDir {
			info.Versioned = fs.policy.ShouldVersion(ctx, e.Path, e.Size)
		}
		result = append(result, info)
	}

	return result, nil
}

// OpenFile opens a file for reading
func (fs *FileSystem) OpenFile(ctx context.Context, name string) (*os.File, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	name = workspace.CleanPath(name)

	f, err := fs.workspace.OpenFile(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return f, nil
}

// WriteFile writes a file, creating versions if needed
func (fs *FileSystem) WriteFile(ctx context.Context, name string, r io.Reader) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	name = workspace.CleanPath(name)

	// Read data (with size limit for safety)
	data, err := io.ReadAll(io.LimitReader(r, 500*1024*1024)) // 500MB limit
	if err != nil {
		return fmt.Errorf("failed to read data: %w", err)
	}

	// Check if versioning is needed
	shouldVersion := fs.policy.ShouldVersion(ctx, name, int64(len(data)))

	// If versioning enabled and file exists, save old version first
	if shouldVersion {
		if oldData, err := fs.workspace.ReadFile(ctx, name); err == nil {
			// Store old content to version objects (hash is computed by object store)
			oldHash, err := fs.versions.PutObject(oldData)
			if err != nil {
				return fmt.Errorf("failed to store version: %w", err)
			}
			// Record version in database
			if err := fs.versions.AddVersion(ctx, name, oldHash, int64(len(oldData)), ""); err != nil {
				return fmt.Errorf("failed to record version: %w", err)
			}
			// Cleanup old versions based on retention policy
			if fs.config.MaxVersions > 0 || fs.config.MaxVersionAge > 0 {
				fs.cleanupVersions(ctx, name)
			}
		}
	}

	// Write new file
	if err := fs.workspace.WriteFile(ctx, name, data); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Update file index
	newHash := computeHash(data)
	fs.versions.UpdateFileIndex(ctx, name, int64(len(data)), time.Now(), newHash)

	return nil
}

// Mkdir creates a directory
func (fs *FileSystem) Mkdir(ctx context.Context, name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	name = workspace.CleanPath(name)

	err := fs.workspace.Mkdir(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrAlreadyExists) {
			return ErrAlreadyExists
		}
		if errors.Is(err, workspace.ErrNotDir) {
			return ErrNotDir
		}
		return err
	}

	return nil
}

// Delete deletes a file or directory (soft delete to trash)
func (fs *FileSystem) Delete(ctx context.Context, name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	name = workspace.CleanPath(name)

	// Get file info
	info, err := fs.workspace.Stat(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	// Check if directory is empty
	if info.IsDir {
		entries, err := fs.workspace.ReadDir(ctx, name)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return ErrDirNotEmpty
		}
	}

	// Generate trash ID
	id := generateID()

	// Check if file had versioning
	hadVersions := false
	if !info.IsDir {
		versions, _ := fs.versions.GetVersions(ctx, name)
		hadVersions = len(versions) > 0
	}

	// Move file to trash
	trashContentPath := path.Join(fs.trashRoot, id, "content")
	if err := os.MkdirAll(path.Dir(trashContentPath), 0700); err != nil {
		return fmt.Errorf("failed to create trash directory: %w", err)
	}

	fullPath := fs.workspace.FullPath(name)
	if err := os.Rename(fullPath, trashContentPath); err != nil {
		// If rename fails (cross-device), copy and delete
		if err := copyFile(fullPath, trashContentPath); err != nil {
			return fmt.Errorf("failed to move to trash: %w", err)
		}
		os.Remove(fullPath)
	}

	// Add to trash database
	trashItem := &versionstore.TrashItem{
		ID:           id,
		OriginalPath: name,
		Size:         info.Size,
		DeletedAt:    time.Now(),
		ExpiresAt:    time.Now().AddDate(0, 0, fs.config.TrashRetentionDays),
		IsDir:        info.IsDir,
		HadVersions:  hadVersions,
	}
	if err := fs.versions.AddToTrash(ctx, trashItem); err != nil {
		// Rollback: move back from trash
		os.Rename(trashContentPath, fullPath)
		return fmt.Errorf("failed to add to trash: %w", err)
	}

	// Remove from file index
	fs.versions.DeleteFileIndex(ctx, name)

	return nil
}

// PermanentDelete permanently deletes a file (bypasses trash)
func (fs *FileSystem) PermanentDelete(ctx context.Context, name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	name = workspace.CleanPath(name)

	// Get file info
	info, err := fs.workspace.Stat(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	if info.IsDir {
		entries, err := fs.workspace.ReadDir(ctx, name)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return ErrDirNotEmpty
		}
	}

	// Delete versions
	if !info.IsDir {
		versions, _ := fs.versions.GetVersions(ctx, name)
		for _, v := range versions {
			fs.versions.DeleteObject(v.Hash)
		}
		fs.versions.DeleteVersions(ctx, name)
	}

	// Delete file
	if err := fs.workspace.Delete(ctx, name); err != nil {
		return err
	}

	// Remove from file index
	fs.versions.DeleteFileIndex(ctx, name)

	return nil
}

// Rename renames/moves a file or directory
func (fs *FileSystem) Rename(ctx context.Context, oldName, newName string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	oldName = workspace.CleanPath(oldName)
	newName = workspace.CleanPath(newName)

	err := fs.workspace.Rename(ctx, oldName, newName)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	// Update file index
	fs.versions.DeleteFileIndex(ctx, oldName)

	return nil
}

// ============================================================================
// Version Operations
// ============================================================================

// ListVersions returns all versions of a file (including current)
func (fs *FileSystem) ListVersions(ctx context.Context, name string) ([]VersionRef, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	name = workspace.CleanPath(name)

	// Get current file info
	info, err := fs.workspace.Stat(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if info.IsDir {
		return nil, ErrIsDir
	}

	// Current version
	var currentHash string
	if data, err := fs.workspace.ReadFile(ctx, name); err == nil {
		currentHash = computeHash(data)
	}

	result := []VersionRef{{
		Hash:      currentHash,
		Size:      info.Size,
		Timestamp: info.ModTime,
		Comment:   "(current)",
	}}

	// Historical versions
	versions, err := fs.versions.GetVersions(ctx, name)
	if err != nil {
		return result, nil // Return at least current version
	}

	for _, v := range versions {
		result = append(result, VersionRef{
			Hash:      v.Hash,
			Size:      v.Size,
			Timestamp: v.CreatedAt,
			Comment:   v.Comment,
		})
	}

	return result, nil
}

// GetVersion reads a specific version of a file
func (fs *FileSystem) GetVersion(ctx context.Context, name, hash string) (io.ReadCloser, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	name = workspace.CleanPath(name)

	// Check if it's the current version
	if data, err := fs.workspace.ReadFile(ctx, name); err == nil {
		if computeHash(data) == hash {
			f, err := fs.workspace.OpenFile(ctx, name)
			if err != nil {
				return nil, err
			}
			return f, nil
		}
	}

	// Get from version store
	data, err := fs.versions.GetObject(hash)
	if err != nil {
		if errors.Is(err, versionstore.ErrNotFound) {
			return nil, ErrVersionNotFound
		}
		return nil, err
	}

	return io.NopCloser(strings.NewReader(string(data))), nil
}

// RestoreVersion restores a file to a specific version
func (fs *FileSystem) RestoreVersion(ctx context.Context, name, hash string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	name = workspace.CleanPath(name)

	// Get version data
	data, err := fs.versions.GetObject(hash)
	if err != nil {
		if errors.Is(err, versionstore.ErrNotFound) {
			return ErrVersionNotFound
		}
		return err
	}

	// Save current as a version first
	if currentData, err := fs.workspace.ReadFile(ctx, name); err == nil {
		currentHash := computeHash(currentData)
		if currentHash != hash {
			fs.versions.PutObject(currentData)
			fs.versions.AddVersion(ctx, name, currentHash, int64(len(currentData)), "before restore")
		}
	}

	// Write restored version
	return fs.workspace.WriteFile(ctx, name, data)
}

// SetVersioning sets the versioning override for a file
func (fs *FileSystem) SetVersioning(ctx context.Context, name string, enabled bool) error {
	name = workspace.CleanPath(name)
	return fs.versions.SetVersioningOverride(ctx, name, enabled)
}

// GetVersioningStatus returns the versioning status for a file
func (fs *FileSystem) GetVersioningStatus(ctx context.Context, name string) (enabled bool, reason string, err error) {
	name = workspace.CleanPath(name)

	info, err := fs.workspace.Stat(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return false, "", ErrNotFound
		}
		return false, "", err
	}

	enabled, reason = fs.policy.GetVersioningStatus(ctx, name, info.Size)
	return enabled, reason, nil
}

// ============================================================================
// Trash Operations
// ============================================================================

// ListTrash returns all items in trash
func (fs *FileSystem) ListTrash(ctx context.Context) ([]*TrashItem, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	items, err := fs.versions.ListTrash(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]*TrashItem, len(items))
	for i, item := range items {
		result[i] = &TrashItem{
			ID:           item.ID,
			OriginalPath: item.OriginalPath,
			Size:         item.Size,
			DeletedAt:    item.DeletedAt,
			IsDir:        item.IsDir,
			HadVersions:  item.HadVersions,
		}
	}

	return result, nil
}

// GetTrashItem returns a trash item
func (fs *FileSystem) GetTrashItem(ctx context.Context, id string) (*TrashItem, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	item, err := fs.versions.GetTrashItem(ctx, id)
	if err != nil {
		if errors.Is(err, versionstore.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return &TrashItem{
		ID:           item.ID,
		OriginalPath: item.OriginalPath,
		Size:         item.Size,
		DeletedAt:    item.DeletedAt,
		IsDir:        item.IsDir,
		HadVersions:  item.HadVersions,
	}, nil
}

// RestoreFromTrash restores a file from trash
func (fs *FileSystem) RestoreFromTrash(ctx context.Context, id string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	item, err := fs.versions.GetTrashItem(ctx, id)
	if err != nil {
		if errors.Is(err, versionstore.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	// Check if original path already exists
	if _, err := fs.workspace.Stat(ctx, item.OriginalPath); err == nil {
		return ErrAlreadyExists
	}

	// Move back from trash
	trashContentPath := path.Join(fs.trashRoot, id, "content")
	destPath := fs.workspace.FullPath(item.OriginalPath)

	// Ensure parent directory exists
	if err := os.MkdirAll(path.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	if err := os.Rename(trashContentPath, destPath); err != nil {
		if err := copyFile(trashContentPath, destPath); err != nil {
			return fmt.Errorf("failed to restore from trash: %w", err)
		}
		os.RemoveAll(path.Join(fs.trashRoot, id))
	} else {
		os.RemoveAll(path.Join(fs.trashRoot, id))
	}

	// Remove from trash database
	fs.versions.RemoveFromTrash(ctx, id)

	return nil
}

// RestoreFromTrashTo restores a file from trash to a custom location
func (fs *FileSystem) RestoreFromTrashTo(ctx context.Context, id, newPath string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	newPath = workspace.CleanPath(newPath)

	// Verify trash item exists
	if _, err := fs.versions.GetTrashItem(ctx, id); err != nil {
		if errors.Is(err, versionstore.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	// Check if target path already exists
	if _, err := fs.workspace.Stat(ctx, newPath); err == nil {
		return ErrAlreadyExists
	}

	// Move from trash
	trashContentPath := path.Join(fs.trashRoot, id, "content")
	destPath := fs.workspace.FullPath(newPath)

	if err := os.MkdirAll(path.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	if err := os.Rename(trashContentPath, destPath); err != nil {
		if err := copyFile(trashContentPath, destPath); err != nil {
			return fmt.Errorf("failed to restore from trash: %w", err)
		}
		os.RemoveAll(path.Join(fs.trashRoot, id))
	} else {
		os.RemoveAll(path.Join(fs.trashRoot, id))
	}

	fs.versions.RemoveFromTrash(ctx, id)

	return nil
}

// DeleteFromTrash permanently deletes an item from trash
func (fs *FileSystem) DeleteFromTrash(ctx context.Context, id string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	item, err := fs.versions.GetTrashItem(ctx, id)
	if err != nil {
		if errors.Is(err, versionstore.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	// Delete version objects if file had versions
	if item.HadVersions {
		// Note: We could store version hashes in trash metadata for complete cleanup
		// For now, orphan objects will be cleaned up by GC
	}

	// Delete trash content
	os.RemoveAll(path.Join(fs.trashRoot, id))

	// Remove from database
	return fs.versions.RemoveFromTrash(ctx, id)
}

// EmptyTrash permanently deletes all items from trash
func (fs *FileSystem) EmptyTrash(ctx context.Context) (int, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Get all trash items
	items, err := fs.versions.ListTrash(ctx)
	if err != nil {
		return 0, err
	}

	// Delete all trash content directories
	for _, item := range items {
		os.RemoveAll(path.Join(fs.trashRoot, item.ID))
	}

	// Clear database
	return fs.versions.ClearTrash(ctx)
}

// CleanupExpiredTrash removes expired trash items
func (fs *FileSystem) CleanupExpiredTrash(ctx context.Context) (int, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Get expired items
	ids, err := fs.versions.CleanupExpiredTrash(ctx)
	if err != nil {
		return 0, err
	}

	// Delete trash content
	for _, id := range ids {
		os.RemoveAll(path.Join(fs.trashRoot, id))
	}

	return len(ids), nil
}

// GetTrashStats returns trash statistics
func (fs *FileSystem) GetTrashStats(ctx context.Context) (count int, totalSize int64, err error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	return fs.versions.GetTrashStats(ctx)
}

// ============================================================================
// Search Operations
// ============================================================================

// Search searches for files matching the query
func (fs *FileSystem) Search(ctx context.Context, query string, limit int) ([]*SearchResult, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	if query == "" {
		return nil, errors.New("search query cannot be empty")
	}

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	query = strings.ToLower(query)
	var results []*SearchResult

	// Walk through workspace
	err := fs.workspace.Walk(ctx, "/", func(filePath string, info *workspace.FileInfo) error {
		if len(results) >= limit {
			return io.EOF // Stop walking
		}

		name := strings.ToLower(info.Name)
		if strings.Contains(name, query) {
			results = append(results, &SearchResult{
				Path:    filePath,
				Name:    info.Name,
				IsDir:   info.IsDir,
				Size:    info.Size,
				ModTime: info.ModTime,
			})
		}

		return nil
	})

	if err != nil && err != io.EOF {
		return nil, err
	}

	return results, nil
}

// ============================================================================
// Cleanup Operations
// ============================================================================

// CleanupStaging removes incomplete staging files
func (fs *FileSystem) CleanupStaging(ctx context.Context) (files int, bytes int64, err error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	return fs.workspace.CleanupStaging(ctx)
}

// cleanupVersions removes old versions based on retention policy
func (fs *FileSystem) cleanupVersions(ctx context.Context, name string) {
	maxCount := fs.config.MaxVersions
	if maxCount <= 0 {
		maxCount = 50
	}

	maxAge := fs.config.MaxVersionAge
	if maxAge <= 0 {
		maxAge = 90 * 24 * time.Hour
	}

	hashes, err := fs.versions.DeleteOldVersions(ctx, name, maxCount, maxAge)
	if err != nil {
		return
	}

	// Delete orphaned objects
	for _, hash := range hashes {
		fs.versions.DeleteObject(hash)
	}
}

// GetAllReferencedHashes returns all hashes currently referenced by version store
// This is used for garbage collection
func (fs *FileSystem) GetAllReferencedHashes(ctx context.Context) ([]string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	// In the new architecture, versions are managed by versionstore
	// Return version hashes from the database
	return fs.versions.GetAllVersionHashes(ctx)
}

// ============================================================================
// Helper Functions
// ============================================================================

func computeHash(data []byte) string {
	h := blake3.Sum256(data)
	return fmt.Sprintf("%x", h[:])
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	if err := os.MkdirAll(path.Dir(dst), 0755); err != nil {
		return err
	}

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return err
	}

	return dstFile.Sync()
}
