// Package storage provides unified storage layer for MnemoNAS
// Combines workspace (native files) with versionstore (SQLite-based versioning)
package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/versionstore"
	"github.com/seanbao/mnemonas/internal/workspace"
	"github.com/zeebo/blake3"
)

// Common errors
var (
	ErrNotFound           = errors.New("not found")
	ErrIsDir              = errors.New("path is a directory")
	ErrNotDir             = errors.New("path is not a directory")
	ErrDirNotEmpty        = errors.New("directory not empty")
	ErrAlreadyExists      = errors.New("already exists")
	ErrFileLocked         = errors.New("file is locked")
	ErrFileTooLarge       = errors.New("file too large")
	ErrVersionNotFound    = errors.New("version not found")
	errStoragePathSymlink = errors.New("storage path contains symlink")
)

var syncStoragePathDir = syncStorageDir
var storageRandomRead = rand.Read
var walkStorageWorkspace = func(ctx context.Context, ws *workspace.Workspace, root string, fn workspace.WalkFunc) error {
	return ws.Walk(ctx, root, fn)
}

const defaultMaxWriteSize int64 = 10 * 1024 * 1024 * 1024 // 10GB

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
	MinFreeSpace       uint64
	TrashEnabled       *bool
	TrashRetentionDays int
	MaxTrashSize       int64
}

// FileSystem provides unified storage operations
type FileSystem struct {
	workspace            *workspace.Workspace
	versions             *versionstore.Store
	policy               *versionstore.VersioningPolicy
	trashRoot            string
	config               *Config
	onPathRenamed        func(ctx context.Context, oldPath, newPath string) error
	onPathDeleted        func(ctx context.Context, path string) (func() error, error)
	listReferencedHashes func(ctx context.Context) ([]string, error)
	getVersions          func(ctx context.Context, path string) ([]versionstore.Version, error)
	deleteFileIndex      func(ctx context.Context, path string) error
	updateFileIndex      func(ctx context.Context, path string, size int64, modTime time.Time, hash string) error
	hasVersionObject     func(ctx context.Context, hash string) (bool, error)
	getVersionObject     func(ctx context.Context, hash string) ([]byte, error)
	putVersionObject     func(ctx context.Context, data []byte) (string, error)
	addFileVersion       func(ctx context.Context, path, hash string, size int64, comment string) error
	deleteVersionObject  func(ctx context.Context, hash string) error
	addTrashMetadata     func(ctx context.Context, item *versionstore.TrashItem) error
	removeTrashMetadata  func(ctx context.Context, id string) error
	renameWorkspacePath  func(ctx context.Context, oldName, newName string) error
	renameMetadataPath   func(ctx context.Context, oldName, newName string) error
	removeTrashPath      func(path string) error
	gcMu                 sync.RWMutex
	mu                   sync.RWMutex
}

// UpdateTrashSettings applies trash settings to the running filesystem.
func (fs *FileSystem) UpdateTrashSettings(enabled bool, retentionDays int, maxSize int64) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if fs.config == nil {
		return
	}
	if fs.config.TrashEnabled == nil {
		fs.config.TrashEnabled = new(bool)
	}
	*fs.config.TrashEnabled = enabled
	fs.config.TrashRetentionDays = retentionDays
	fs.config.MaxTrashSize = maxSize
}

// UpdateRetentionSettings applies version retention settings to the running filesystem.
func (fs *FileSystem) UpdateRetentionSettings(maxVersions int, maxVersionAge time.Duration, minFreeSpace uint64) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if fs.config == nil {
		return
	}
	fs.config.MaxVersions = maxVersions
	fs.config.MaxVersionAge = maxVersionAge
	fs.config.MinFreeSpace = minFreeSpace
}

// UpdateVersioningSettings applies versioning policy settings to the running filesystem.
func (fs *FileSystem) UpdateVersioningSettings(extensions, filenames []string, maxVersionedSize int64) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if fs.config != nil {
		fs.config.AutoVersionedExtensions = append([]string(nil), extensions...)
		fs.config.AutoVersionedFilenames = append([]string(nil), filenames...)
		fs.config.MaxVersionedSize = maxVersionedSize
	}
	if fs.policy != nil {
		fs.policy.AutoVersionedExtensions = append([]string(nil), extensions...)
		fs.policy.AutoVersionedFilenames = append([]string(nil), filenames...)
		fs.policy.MaxVersionedSize = maxVersionedSize
	}
}

// SetDataplaneClient swaps the dataplane client used by version storage operations.
func (fs *FileSystem) SetDataplaneClient(client *dataplane.Client) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if fs.config != nil {
		fs.config.Dataplane = client
	}
	if fs.versions != nil {
		fs.versions.SetDataplaneClient(client)
	}
}

// SetPathChangeHooks registers callbacks for committed rename/delete operations.
func (fs *FileSystem) SetPathChangeHooks(onRename func(ctx context.Context, oldPath, newPath string) error, onDelete func(ctx context.Context, path string) (func() error, error)) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.onPathRenamed = onRename
	fs.onPathDeleted = onDelete
}

// RunRetentionSweep applies version retention rules across all versioned files.
func (fs *FileSystem) RunRetentionSweep(ctx context.Context) error {
	release := fs.beginMutation()
	defer release()

	return fs.runRetentionSweepLocked(ctx)
}

// New creates a new FileSystem
func New(cfg *Config) (*FileSystem, error) {
	if cfg.Dataplane == nil {
		return nil, errors.New("dataplane client is required")
	}

	if err := validateStoragePath(cfg.InternalRoot); err != nil {
		return nil, fmt.Errorf("failed to validate internal root: %w", err)
	}
	if err := validateStoragePath(cfg.TrashRoot); err != nil {
		return nil, fmt.Errorf("failed to validate trash root: %w", err)
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
	if err := ensureStorageDir(cfg.TrashRoot, 0700); err != nil {
		return nil, fmt.Errorf("failed to create trash directory: %w", err)
	}

	return &FileSystem{
		workspace:            ws,
		versions:             vs,
		policy:               policy,
		trashRoot:            cfg.TrashRoot,
		config:               cfg,
		listReferencedHashes: vs.GetAllVersionHashes,
		getVersions:          vs.GetVersions,
		deleteFileIndex:      vs.DeleteFileIndex,
		updateFileIndex:      vs.UpdateFileIndex,
		hasVersionObject:     vs.HasObject,
		getVersionObject:     vs.GetObject,
		putVersionObject:     vs.PutObject,
		addFileVersion:       vs.AddVersion,
		deleteVersionObject:  vs.DeleteObject,
		addTrashMetadata:     vs.AddToTrash,
		removeTrashMetadata:  vs.RemoveFromTrash,
		renameWorkspacePath:  ws.Rename,
		renameMetadataPath:   vs.RenamePath,
		removeTrashPath:      os.RemoveAll,
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
	var err error
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		return nil, err
	}

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
		if errors.Is(err, workspace.ErrNotDir) {
			return nil, ErrNotDir
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
		if contentHash, err := fs.hashWorkspaceFile(ctx, name); err == nil {
			fileInfo.ContentHash = contentHash
		}
	}

	return fileInfo, nil
}

// ReadDir reads directory contents
func (fs *FileSystem) ReadDir(ctx context.Context, name string) ([]*FileInfo, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	var err error
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		return nil, err
	}

	entries, err := fs.workspace.ReadDir(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return nil, ErrNotFound
		}
		if errors.Is(err, workspace.ErrNotDir) {
			return nil, ErrNotDir
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

	var err error
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		return nil, err
	}

	f, err := fs.workspace.OpenFile(ctx, name)
	if err != nil {
		return nil, mapWorkspaceReadablePathError(err)
	}

	return f, nil
}

// WriteFile writes a file, creating versions if needed
func (fs *FileSystem) WriteFile(ctx context.Context, name string, r io.Reader) error {
	release := fs.beginMutation()
	defer release()

	var err error
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		return err
	}
	previousData, hadPreviousFile, err := fs.readExistingFileForRollback(ctx, name)
	if err != nil {
		return err
	}

	var rollbackVersionHash string
	var rollbackVersionRecorded bool
	var rollbackVersionObjectCreated bool

	fullPath := fs.workspace.FullPath(name)
	if err := validateStoragePath(fullPath); err != nil {
		if errors.Is(err, errStoragePathSymlink) {
			return ErrNotFound
		}
		if isPathNotDirError(err) {
			return ErrNotDir
		}
		return fmt.Errorf("failed to validate path: %w", err)
	}
	if err := ensureStorageDir(filepath.Dir(fullPath), 0755); err != nil {
		if isPathNotDirError(err) {
			return ErrNotDir
		}
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	f, err := os.CreateTemp(filepath.Dir(fullPath), ".storage-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := f.Name()
	if err := f.Chmod(0644); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to set temp file permissions: %w", err)
	}

	hasher := blake3.New()
	limited := &io.LimitedReader{R: r, N: defaultMaxWriteSize + 1}
	written, copyErr := io.Copy(io.MultiWriter(f, hasher), limited)
	syncErr := f.Sync()
	closeErr := f.Close()

	if copyErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write file: %w", copyErr)
	}
	if syncErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to sync file: %w", syncErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close file: %w", closeErr)
	}
	if written > defaultMaxWriteSize {
		os.Remove(tmpPath)
		return fmt.Errorf("%w (max: %d bytes)", ErrFileTooLarge, defaultMaxWriteSize)
	}

	// Check if versioning is needed
	shouldVersion := fs.policy.ShouldVersion(ctx, name, written)

	// If versioning enabled and file exists, save old version first
	if shouldVersion && hadPreviousFile {
		oldData := previousData
		candidateHash := computeHash(oldData)
		rollbackVersionHash = candidateHash
		hasObject, err := fs.hasVersionObject(ctx, candidateHash)
		if err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("failed to check existing version object: %w", err)
		}
		rollbackVersionObjectCreated = !hasObject

		_, versionErr := fs.versions.GetVersion(ctx, name, candidateHash)
		versionAlreadyRecorded := versionErr == nil
		if versionErr != nil && !errors.Is(versionErr, versionstore.ErrNotFound) {
			os.Remove(tmpPath)
			return fmt.Errorf("failed to check existing version: %w", versionErr)
		}

		oldHash, err := fs.putVersionObject(ctx, oldData)
		if err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("failed to store version: %w", err)
		}
		rollbackVersionHash = oldHash

		if !versionAlreadyRecorded {
			if err := fs.addFileVersion(ctx, name, oldHash, int64(len(oldData)), ""); err != nil {
				os.Remove(tmpPath)
				if rollbackVersionObjectCreated {
					if deleteErr := fs.deleteVersionObject(ctx, oldHash); deleteErr != nil {
						return errors.Join(
							fmt.Errorf("failed to record version: %w", err),
							fmt.Errorf("failed to cleanup version object during rollback: %w", deleteErr),
						)
					}
				}
				return fmt.Errorf("failed to record version: %w", err)
			}
			rollbackVersionRecorded = true
		}
	}

	if err := os.Rename(tmpPath, fullPath); err != nil {
		os.Remove(tmpPath)
		if rollbackErr := fs.rollbackWriteVersion(ctx, name, rollbackVersionHash, rollbackVersionRecorded, rollbackVersionObjectCreated); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to replace file: %w", err),
				fmt.Errorf("failed to rollback version metadata: %w", rollbackErr),
			)
		}
		return fmt.Errorf("failed to replace file: %w", err)
	}
	if err := syncStoragePathDir(filepath.Dir(fullPath)); err != nil {
		versionRollbackErr := fs.rollbackWriteVersion(ctx, name, rollbackVersionHash, rollbackVersionRecorded, rollbackVersionObjectCreated)
		if rollbackErr := fs.restoreFileAfterIndexFailure(ctx, name, hadPreviousFile, previousData); rollbackErr != nil {
			joinedErr := errors.Join(
				fmt.Errorf("failed to sync parent directory: %w", err),
				fmt.Errorf("failed to rollback file content: %w", rollbackErr),
			)
			if versionRollbackErr != nil {
				joinedErr = errors.Join(joinedErr, fmt.Errorf("failed to rollback version metadata: %w", versionRollbackErr))
			}
			return joinedErr
		}
		if versionRollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to sync parent directory: %w", err),
				fmt.Errorf("failed to rollback version metadata: %w", versionRollbackErr),
			)
		}
		return fmt.Errorf("failed to sync parent directory: %w", err)
	}

	newHash := fmt.Sprintf("%x", hasher.Sum(nil))
	if err := fs.updateFileIndex(ctx, name, written, time.Now(), newHash); err != nil {
		versionRollbackErr := fs.rollbackWriteVersion(ctx, name, rollbackVersionHash, rollbackVersionRecorded, rollbackVersionObjectCreated)
		if rollbackErr := fs.restoreFileAfterIndexFailure(ctx, name, hadPreviousFile, previousData); rollbackErr != nil {
			joinedErr := errors.Join(
				fmt.Errorf("failed to update file index: %w", err),
				fmt.Errorf("failed to rollback file content: %w", rollbackErr),
			)
			if versionRollbackErr != nil {
				joinedErr = errors.Join(joinedErr, fmt.Errorf("failed to rollback version metadata: %w", versionRollbackErr))
			}
			return joinedErr
		}
		if versionRollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to update file index: %w", err),
				fmt.Errorf("failed to rollback version metadata: %w", versionRollbackErr),
			)
		}
		return fmt.Errorf("failed to update file index: %w", err)
	}

	if shouldVersion && (fs.config.MaxVersions > 0 || fs.config.MaxVersionAge > 0) {
		if err := fs.cleanupVersions(ctx, name); err != nil {
			// The new content, index, and current-version metadata are already
			// committed here. Retention cleanup failures should leave extra history
			// behind, not turn the caller's successful write into a false-negative.
			return nil
		}
	}

	if fs.shouldForceRetentionSweepLocked() {
		if err := fs.runRetentionSweepLocked(ctx); err != nil {
			// The new content and index are already committed at this point, so
			// retention enforcement failures must not turn a successful write into
			// a false-negative for callers.
			return nil
		}
	}

	return nil
}

// Mkdir creates a directory
func (fs *FileSystem) Mkdir(ctx context.Context, name string) error {
	release := fs.beginMutation()
	defer release()

	var err error
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		return err
	}

	err = fs.workspace.Mkdir(ctx, name)
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
	if fs.config != nil && fs.config.TrashEnabled != nil && !*fs.config.TrashEnabled {
		return fs.PermanentDelete(ctx, name)
	}

	release := fs.beginMutation()
	defer release()

	var err error
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		return err
	}

	// Get file info
	info, err := fs.workspace.Stat(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return ErrNotFound
		}
		if errors.Is(err, workspace.ErrNotDir) {
			return ErrNotDir
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

	if err := fs.ensureTrashCapacityLocked(ctx, info.Size); err != nil {
		return err
	}
	rollbackInfo := &FileInfo{
		IsDir:   info.IsDir,
		Size:    info.Size,
		ModTime: info.ModTime,
	}

	// Generate trash ID
	id, err := generateID()
	if err != nil {
		return fmt.Errorf("generate trash ID: %w", err)
	}

	// Check if file had versioning
	hadVersions := false
	if !info.IsDir {
		contentHash, hashErr := fs.hashWorkspaceFile(ctx, name)
		if hashErr != nil {
			return hashErr
		}
		rollbackInfo.ContentHash = contentHash
		versions, err := fs.versions.GetVersions(ctx, name)
		if err != nil {
			return fmt.Errorf("failed to read version metadata: %w", err)
		}
		hadVersions = len(versions) > 0
	}

	// Move file to trash
	trashContentPath := path.Join(fs.trashRoot, id, "content")
	if err := ensureStorageDir(path.Dir(trashContentPath), 0700); err != nil {
		return fmt.Errorf("failed to create trash directory: %w", err)
	}

	fullPath := fs.workspace.FullPath(name)
	if err := movePath(fullPath, trashContentPath); err != nil {
		return fmt.Errorf("failed to move to trash: %w", err)
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
		if rollbackErr := movePath(trashContentPath, fullPath); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to add to trash: %w", err),
				fmt.Errorf("failed to rollback trash move: %w", rollbackErr),
			)
		}
		return fmt.Errorf("failed to add to trash: %w", err)
	}

	// Remove from file index
	if err := fs.deleteFileIndex(ctx, name); err != nil {
		if rollbackErr := fs.rollbackSoftDelete(ctx, name, rollbackInfo, id, trashContentPath, false); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to delete file index: %w", err),
				rollbackErr,
			)
		}
		return fmt.Errorf("failed to delete file index: %w", err)
	}
	if _, err := fs.notifyPathDeleted(ctx, name); err != nil {
		if rollbackErr := fs.rollbackSoftDelete(ctx, name, rollbackInfo, id, trashContentPath, true); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to sync delete hooks: %w", err),
				rollbackErr,
			)
		}
		return fmt.Errorf("failed to sync delete hooks: %w", err)
	}

	return nil
}

func (fs *FileSystem) ensureTrashCapacityLocked(ctx context.Context, incomingSize int64) error {
	if fs.config == nil || fs.config.MaxTrashSize <= 0 {
		return nil
	}

	_, totalSize, err := fs.versions.GetTrashStats(ctx)
	if err != nil {
		return fmt.Errorf("failed to read trash stats: %w", err)
	}
	if totalSize+incomingSize <= fs.config.MaxTrashSize {
		return nil
	}

	items, err := fs.versions.ListTrash(ctx)
	if err != nil {
		return fmt.Errorf("failed to list trash items: %w", err)
	}

	for i := len(items) - 1; i >= 0 && totalSize+incomingSize > fs.config.MaxTrashSize; i-- {
		item := items[i]
		if err := fs.deleteTrashItem(ctx, &item); err != nil {
			return fmt.Errorf("failed to evict trash item %s: %w", item.ID, err)
		}
		totalSize -= item.Size
	}

	return nil
}

// PermanentDelete permanently deletes a file (bypasses trash)
func (fs *FileSystem) PermanentDelete(ctx context.Context, name string) error {
	release := fs.beginMutation()
	defer release()

	var err error
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		return err
	}
	previousData, hadPreviousFile, err := fs.readExistingFileForRollback(ctx, name)
	if err != nil && !errors.Is(err, ErrIsDir) {
		return err
	}
	hadPreviousDir := false

	// Get file info
	info, err := fs.workspace.Stat(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	if info.IsDir {
		hadPreviousDir = true
		entries, err := fs.workspace.ReadDir(ctx, name)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return ErrDirNotEmpty
		}
	}

	var versionHashes []string
	if !info.IsDir {
		versions, versionsErr := fs.versions.GetVersions(ctx, name)
		if versionsErr != nil {
			return versionsErr
		}
		versionHashes = make([]string, 0, len(versions))
		for _, version := range versions {
			versionHashes = append(versionHashes, version.Hash)
		}
	}

	// Delete file
	if err := fs.workspace.Delete(ctx, name); err != nil {
		return err
	}

	// Remove from file index
	if err := fs.deleteFileIndex(ctx, name); err != nil {
		if rollbackErr := fs.rollbackDeletedPath(ctx, name, hadPreviousFile, previousData, hadPreviousDir); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to delete file index: %w", err),
				fmt.Errorf("failed to rollback deleted path: %w", rollbackErr),
			)
		}
		return fmt.Errorf("failed to delete file index: %w", err)
	}
	rollbackDeleteHook, err := fs.notifyPathDeleted(ctx, name)
	if err != nil {
		if rollbackErr := fs.rollbackDeletedPath(ctx, name, hadPreviousFile, previousData, hadPreviousDir); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to sync delete hooks: %w", err),
				fmt.Errorf("failed to rollback deleted path: %w", rollbackErr),
			)
		}
		if !info.IsDir {
			if restoreIndexErr := fs.updateFileIndex(ctx, name, info.Size, info.ModTime, computeHash(previousData)); restoreIndexErr != nil {
				return errors.Join(
					fmt.Errorf("failed to sync delete hooks: %w", err),
					fmt.Errorf("failed to restore file index after rollback: %w", restoreIndexErr),
				)
			}
		}
		return fmt.Errorf("failed to sync delete hooks: %w", err)
	}

	if !info.IsDir {
		if err := fs.versions.DeleteVersions(ctx, name); err != nil {
			var rollbackErr error
			if rollbackDeleteHook != nil {
				rollbackErr = rollbackDeleteHook()
			}
			if pathRollbackErr := fs.rollbackDeletedPath(ctx, name, hadPreviousFile, previousData, hadPreviousDir); pathRollbackErr != nil {
				return errors.Join(
					fmt.Errorf("failed to delete version metadata: %w", err),
					wrapStorageStepError("rollback deleted-path hooks", rollbackErr),
					fmt.Errorf("failed to rollback deleted path: %w", pathRollbackErr),
				)
			}
			if restoreIndexErr := fs.updateFileIndex(ctx, name, info.Size, info.ModTime, computeHash(previousData)); restoreIndexErr != nil {
				return errors.Join(
					fmt.Errorf("failed to delete version metadata: %w", err),
					wrapStorageStepError("rollback deleted-path hooks", rollbackErr),
					fmt.Errorf("failed to restore file index after rollback: %w", restoreIndexErr),
				)
			}
			if rollbackErr != nil {
				return errors.Join(
					fmt.Errorf("failed to delete version metadata: %w", err),
					wrapStorageStepError("rollback deleted-path hooks", rollbackErr),
				)
			}
			return fmt.Errorf("failed to delete version metadata: %w", err)
		}

		objectDeleteErr := fs.deleteUnreferencedVersionObjects(ctx, versionHashes)
		if objectDeleteErr != nil {
			return fmt.Errorf("failed to delete version objects: %w", objectDeleteErr)
		}
	}

	return nil
}

// Rename renames/moves a file or directory
func (fs *FileSystem) Rename(ctx context.Context, oldName, newName string) error {
	release := fs.beginMutation()
	defer release()

	var err error
	oldName, err = normalizeStorageWorkspacePath(oldName)
	if err != nil {
		return err
	}
	newName, err = normalizeStorageWorkspacePath(newName)
	if err != nil {
		return err
	}

	err = fs.renameWorkspacePath(ctx, oldName, newName)
	if err != nil {
		if errors.Is(err, workspace.ErrAlreadyExists) {
			return ErrAlreadyExists
		}
		if errors.Is(err, workspace.ErrNotDir) {
			return ErrNotDir
		}
		if errors.Is(err, workspace.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	if err := fs.renameMetadataPath(ctx, oldName, newName); err != nil {
		if rollbackErr := fs.renameWorkspacePath(ctx, newName, oldName); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to rename metadata: %w", err),
				fmt.Errorf("failed to rollback workspace rename: %w", rollbackErr),
			)
		}
		return fmt.Errorf("failed to rename metadata: %w", err)
	}
	if err := fs.notifyPathRenamed(ctx, oldName, newName); err != nil {
		rollbackWorkspaceErr := fs.renameWorkspacePath(ctx, newName, oldName)
		rollbackMetadataErr := fs.renameMetadataPath(ctx, newName, oldName)
		if rollbackWorkspaceErr != nil || rollbackMetadataErr != nil {
			errs := []error{fmt.Errorf("failed to sync rename hooks: %w", err)}
			if rollbackWorkspaceErr != nil {
				errs = append(errs, fmt.Errorf("failed to rollback workspace rename after hook failure: %w", rollbackWorkspaceErr))
			}
			if rollbackMetadataErr != nil {
				errs = append(errs, fmt.Errorf("failed to rollback metadata rename after hook failure: %w", rollbackMetadataErr))
			}
			return errors.Join(errs...)
		}
		return fmt.Errorf("failed to sync rename hooks: %w", err)
	}

	return nil
}

func (fs *FileSystem) notifyPathDeleted(ctx context.Context, name string) (func() error, error) {
	fs.mu.RLock()
	hook := fs.onPathDeleted
	fs.mu.RUnlock()
	if hook != nil {
		return hook(ctx, name)
	}
	return nil, nil
}

func (fs *FileSystem) notifyPathRenamed(ctx context.Context, oldName, newName string) error {
	fs.mu.RLock()
	hook := fs.onPathRenamed
	fs.mu.RUnlock()
	if hook != nil {
		return hook(ctx, oldName, newName)
	}
	return nil
}

// ============================================================================
// Version Operations
// ============================================================================

// ListVersions returns all versions of a file (including current)
func (fs *FileSystem) ListVersions(ctx context.Context, name string) ([]VersionRef, error) {
	var err error
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		return nil, err
	}

	// Get current file info
	info, err := fs.workspace.Stat(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return nil, ErrNotFound
		}
		if errors.Is(err, workspace.ErrNotDir) {
			return nil, ErrNotDir
		}
		return nil, err
	}

	if info.IsDir {
		return nil, ErrIsDir
	}

	// Current version
	var currentHash string
	if contentHash, err := fs.hashWorkspaceFile(ctx, name); err == nil {
		currentHash = contentHash
	} else if errors.Is(err, ErrNotFound) || errors.Is(err, ErrNotDir) || errors.Is(err, ErrIsDir) {
		return nil, err
	}

	result := []VersionRef{{
		Hash:      currentHash,
		Size:      info.Size,
		Timestamp: info.ModTime,
		Comment:   "(current)",
	}}

	// Historical versions
	versions, err := fs.getVersions(ctx, name)
	if err != nil {
		return nil, err
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

func mapWorkspaceReadablePathError(err error) error {
	if errors.Is(err, workspace.ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, workspace.ErrNotDir) {
		return ErrNotDir
	}
	if errors.Is(err, workspace.ErrIsDir) {
		return ErrIsDir
	}
	return err
}

func mapWorkspaceOpenFileError(err error) error {
	return mapWorkspaceReadablePathError(err)
}

// GetVersion reads a specific version of a file
func (fs *FileSystem) GetVersion(ctx context.Context, name, hash string) (io.ReadCloser, error) {
	var err error
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		return nil, err
	}
	info, err := fs.workspace.Stat(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return nil, ErrNotFound
		}
		if errors.Is(err, workspace.ErrNotDir) {
			return nil, ErrNotDir
		}
		return nil, err
	}
	if info.IsDir {
		return nil, ErrIsDir
	}

	// Check if it's the current version
	if currentHash, err := fs.hashWorkspaceFile(ctx, name); err == nil {
		if currentHash == hash {
			f, err := fs.workspace.OpenFile(ctx, name)
			if err != nil {
				return nil, mapWorkspaceReadablePathError(err)
			}
			return f, nil
		}
	}

	if _, err := fs.versions.GetVersion(ctx, name, hash); err != nil {
		if errors.Is(err, versionstore.ErrNotFound) {
			return nil, ErrVersionNotFound
		}
		return nil, err
	}

	// Get from version store
	data, err := fs.getVersionObject(ctx, hash)
	if err != nil {
		if errors.Is(err, versionstore.ErrNotFound) {
			return nil, ErrVersionNotFound
		}
		return nil, err
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (fs *FileSystem) hashWorkspaceFile(ctx context.Context, name string) (string, error) {
	reader, err := fs.workspace.OpenFile(ctx, name)
	if err != nil {
		return "", mapWorkspaceReadablePathError(err)
	}
	defer reader.Close()

	hasher := blake3.New()
	if _, err := io.Copy(hasher, reader); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

// RestoreVersion restores a file to a specific version
func (fs *FileSystem) RestoreVersion(ctx context.Context, name, hash string) error {
	release := fs.beginMutation()
	defer release()

	var err error
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		return err
	}
	previousData, hadPreviousFile, err := fs.readExistingFileForRollback(ctx, name)
	if err != nil {
		return err
	}
	currentHashMatches := hadPreviousFile && computeHash(previousData) == hash
	if !currentHashMatches {
		if _, err := fs.versions.GetVersion(ctx, name, hash); err != nil {
			if errors.Is(err, versionstore.ErrNotFound) {
				return ErrVersionNotFound
			}
			return err
		}
	}

	var data []byte
	if currentHashMatches {
		data = previousData
	} else {
		// Get version data
		data, err = fs.getVersionObject(ctx, hash)
		if err != nil {
			if errors.Is(err, versionstore.ErrNotFound) {
				return ErrVersionNotFound
			}
			return err
		}
	}

	rollbackVersionHash := ""
	rollbackVersionRecorded := false
	rollbackVersionObjectCreated := false

	// Save current as a version first
	if hadPreviousFile {
		currentHash := computeHash(previousData)
		if currentHash != hash {
			hasObject, err := fs.hasVersionObject(ctx, currentHash)
			if err != nil {
				return fmt.Errorf("failed to check current version object before restore: %w", err)
			}
			rollbackObjectCreated := !hasObject
			storedHash, err := fs.putVersionObject(ctx, previousData)
			if err != nil {
				return fmt.Errorf("failed to store current version before restore: %w", err)
			}
			rollbackVersionHash = storedHash
			rollbackVersionObjectCreated = rollbackObjectCreated
			if err := fs.addFileVersion(ctx, name, storedHash, int64(len(previousData)), "before restore"); err != nil {
				if rollbackErr := fs.rollbackWriteVersion(ctx, name, storedHash, false, rollbackObjectCreated); rollbackErr != nil {
					return errors.Join(
						fmt.Errorf("failed to record current version before restore: %w", err),
						fmt.Errorf("failed to cleanup current snapshot version during rollback: %w", rollbackErr),
					)
				}
				return fmt.Errorf("failed to record current version before restore: %w", err)
			}
			rollbackVersionRecorded = true
		}
	}

	// Write restored version
	if err := fs.workspace.WriteFile(ctx, name, data); err != nil {
		if rollbackErr := fs.rollbackWriteVersion(ctx, name, rollbackVersionHash, rollbackVersionRecorded, rollbackVersionObjectCreated); rollbackErr != nil {
			return errors.Join(
				err,
				fmt.Errorf("failed to rollback current snapshot version: %w", rollbackErr),
			)
		}
		return err
	}

	if err := fs.updateFileIndex(ctx, name, int64(len(data)), time.Now(), computeHash(data)); err != nil {
		rollbackErr := fs.restoreFileAfterIndexFailure(ctx, name, hadPreviousFile, previousData)
		versionRollbackErr := fs.rollbackWriteVersion(ctx, name, rollbackVersionHash, rollbackVersionRecorded, rollbackVersionObjectCreated)
		if rollbackErr != nil && versionRollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to update file index: %w", err),
				fmt.Errorf("failed to rollback restored version: %w", rollbackErr),
				fmt.Errorf("failed to rollback current snapshot version: %w", versionRollbackErr),
			)
		}
		if rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to update file index: %w", err),
				fmt.Errorf("failed to rollback restored version: %w", rollbackErr),
			)
		}
		if versionRollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to update file index: %w", err),
				fmt.Errorf("failed to rollback current snapshot version: %w", versionRollbackErr),
			)
		}
		return fmt.Errorf("failed to update file index: %w", err)
	}
	return nil
}

// SetVersioning sets the versioning override for a file
func (fs *FileSystem) SetVersioning(ctx context.Context, name string, enabled bool) error {
	var err error
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		return err
	}
	return fs.versions.SetVersioningOverride(ctx, name, enabled)
}

// GetVersioningStatus returns the versioning status for a file
func (fs *FileSystem) GetVersioningStatus(ctx context.Context, name string) (enabled bool, reason string, err error) {
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		return false, "", err
	}

	info, err := fs.workspace.Stat(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return false, "", ErrNotFound
		}
		if errors.Is(err, workspace.ErrNotDir) {
			return false, "", ErrNotDir
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
	release := fs.beginMutation()
	defer release()

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
	if err := ensureStorageDir(path.Dir(destPath), 0755); err != nil {
		if isPathNotDirError(err) {
			return ErrNotDir
		}
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	if err := movePath(trashContentPath, destPath); err != nil {
		return fmt.Errorf("failed to restore from trash: %w", err)
	}

	// Remove from trash database
	if err := fs.versions.RemoveFromTrash(ctx, id); err != nil {
		if rollbackErr := movePath(destPath, trashContentPath); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to remove trash metadata: %w", err),
				fmt.Errorf("failed to rollback restored content: %w", rollbackErr),
			)
		}
		return fmt.Errorf("failed to remove trash metadata: %w", err)
	}
	os.RemoveAll(path.Join(fs.trashRoot, id))
	if !item.IsDir {
		if err := fs.syncFileIndexFromWorkspace(ctx, item.OriginalPath); err != nil {
			rollbackErr := movePath(destPath, trashContentPath)
			metadataErr := fs.versions.AddToTrash(ctx, item)
			if rollbackErr != nil && metadataErr != nil {
				return errors.Join(
					fmt.Errorf("failed to update file index: %w", err),
					fmt.Errorf("failed to rollback restored content: %w", rollbackErr),
					fmt.Errorf("failed to restore trash metadata: %w", metadataErr),
				)
			}
			if rollbackErr != nil {
				return errors.Join(
					fmt.Errorf("failed to update file index: %w", err),
					fmt.Errorf("failed to rollback restored content: %w", rollbackErr),
				)
			}
			if metadataErr != nil {
				return errors.Join(
					fmt.Errorf("failed to update file index: %w", err),
					fmt.Errorf("failed to restore trash metadata: %w", metadataErr),
				)
			}
			return fmt.Errorf("failed to update file index: %w", err)
		}
	}

	return nil
}

// RestoreFromTrashTo restores a file from trash to a custom location
func (fs *FileSystem) RestoreFromTrashTo(ctx context.Context, id, newPath string) error {
	release := fs.beginMutation()
	defer release()

	var err error
	newPath, err = normalizeStorageWorkspacePath(newPath)
	if err != nil {
		return err
	}

	// Verify trash item exists
	item, err := fs.versions.GetTrashItem(ctx, id)
	if err != nil {
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

	if err := ensureStorageDir(path.Dir(destPath), 0755); err != nil {
		if isPathNotDirError(err) {
			return ErrNotDir
		}
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	if err := movePath(trashContentPath, destPath); err != nil {
		return fmt.Errorf("failed to restore from trash: %w", err)
	}

	metadataRenamed := false
	if item.HadVersions {
		if err := fs.renameMetadataPath(ctx, item.OriginalPath, newPath); err != nil {
			if rollbackErr := movePath(destPath, trashContentPath); rollbackErr != nil {
				return errors.Join(
					fmt.Errorf("failed to update version metadata: %w", err),
					fmt.Errorf("failed to rollback restored content: %w", rollbackErr),
				)
			}
			return fmt.Errorf("failed to update version metadata: %w", err)
		}
		metadataRenamed = true
	}

	if err := fs.removeTrashMetadata(ctx, id); err != nil {
		var rollbackErrs []error
		if metadataRenamed {
			if revertErr := fs.renameMetadataPath(ctx, newPath, item.OriginalPath); revertErr != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to rollback version metadata: %w", revertErr))
			}
		}
		if rollbackErr := movePath(destPath, trashContentPath); rollbackErr != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to rollback restored content: %w", rollbackErr))
		}
		if len(rollbackErrs) > 0 {
			return errors.Join(append([]error{fmt.Errorf("failed to remove trash metadata: %w", err)}, rollbackErrs...)...)
		}
		return fmt.Errorf("failed to remove trash metadata: %w", err)
	}

	os.RemoveAll(path.Join(fs.trashRoot, id))
	if !item.IsDir {
		if err := fs.syncFileIndexFromWorkspace(ctx, newPath); err != nil {
			var rollbackErrs []error
			if metadataRenamed {
				if revertErr := fs.renameMetadataPath(ctx, newPath, item.OriginalPath); revertErr != nil {
					rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to rollback version metadata: %w", revertErr))
				}
			}
			rollbackErr := movePath(destPath, trashContentPath)
			if rollbackErr != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to rollback restored content: %w", rollbackErr))
			}
			if metadataErr := fs.addTrashMetadata(ctx, item); metadataErr != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to restore trash metadata: %w", metadataErr))
			}
			if len(rollbackErrs) > 0 {
				return errors.Join(append([]error{fmt.Errorf("failed to update file index: %w", err)}, rollbackErrs...)...)
			}
			return fmt.Errorf("failed to update file index: %w", err)
		}
	}

	return nil
}

// DeleteFromTrash permanently deletes an item from trash
func (fs *FileSystem) DeleteFromTrash(ctx context.Context, id string) error {
	release := fs.beginMutation()
	defer release()

	item, err := fs.versions.GetTrashItem(ctx, id)
	if err != nil {
		if errors.Is(err, versionstore.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	_, err = fs.permanentlyDeleteTrashItem(ctx, item)
	return err
}

// EmptyTrash permanently deletes all items from trash
func (fs *FileSystem) EmptyTrash(ctx context.Context) (int, error) {
	release := fs.beginMutation()
	defer release()
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	// Get all trash items
	items, err := fs.versions.ListTrash(ctx)
	if err != nil {
		return 0, err
	}

	deleted := 0
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		visibleDeleted, err := fs.permanentlyDeleteTrashItem(ctx, &item)
		if err != nil {
			if visibleDeleted {
				deleted++
			}
			return deleted, err
		}
		deleted++
	}

	return deleted, nil
}

// CleanupExpiredTrash removes expired trash items
func (fs *FileSystem) CleanupExpiredTrash(ctx context.Context) (int, error) {
	release := fs.beginMutation()
	defer release()
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	// Get expired items first so metadata is removed only after content deletion succeeds.
	items, err := fs.versions.ListTrash(ctx)
	if err != nil {
		return 0, err
	}

	now := time.Now()
	deleted := 0
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		if !item.ExpiresAt.Before(now) {
			continue
		}
		visibleDeleted, err := fs.permanentlyDeleteTrashItem(ctx, &item)
		if err != nil {
			if visibleDeleted {
				deleted++
			}
			return deleted, err
		}
		deleted++
	}

	return deleted, nil
}

func (fs *FileSystem) permanentlyDeleteTrashItem(ctx context.Context, item *versionstore.TrashItem) (bool, error) {
	err := fs.deleteTrashItem(ctx, item)
	visibleDeleted := err == nil
	if !item.HadVersions {
		var durabilityErr *trashDeleteDurabilityError
		if errors.As(err, &durabilityErr) {
			visibleDeleted = true
		}
		return visibleDeleted, err
	}

	var durabilityErr *trashDeleteDurabilityError
	if err != nil && !errors.As(err, &durabilityErr) {
		return false, err
	}
	if durabilityErr != nil {
		visibleDeleted = true
	}

	cleanupErr := fs.cleanupDeletedTrashVersions(ctx, item)
	if cleanupErr != nil {
		return visibleDeleted, errors.Join(err, cleanupErr)
	}

	return visibleDeleted, err
}

func (fs *FileSystem) cleanupDeletedTrashVersions(ctx context.Context, item *versionstore.TrashItem) error {
	versions, err := fs.versions.GetVersions(ctx, item.OriginalPath)
	if err != nil {
		return fmt.Errorf("failed to read version metadata for trash item: %w", err)
	}

	versionHashes := make([]string, 0, len(versions))
	for _, version := range versions {
		versionHashes = append(versionHashes, version.Hash)
	}

	if err := fs.versions.DeleteVersions(ctx, item.OriginalPath); err != nil {
		return fmt.Errorf("failed to delete version metadata for trash item: %w", err)
	}
	if err := fs.deleteUnreferencedVersionObjects(ctx, versionHashes); err != nil {
		return fmt.Errorf("failed to delete version objects for trash item: %w", err)
	}

	return nil
}

func (fs *FileSystem) deleteTrashItem(ctx context.Context, item *versionstore.TrashItem) error {
	trashItemPath := path.Join(fs.trashRoot, item.ID)
	stageID, err := generateID()
	if err != nil {
		return fmt.Errorf("generate trash staging ID for %s: %w", item.ID, err)
	}
	stagedTrashPath := path.Join(fs.trashRoot, ".deleting", item.ID+"-"+stageID)

	// Stage the trash entry first so metadata deletion can be rolled back safely.
	if err := movePath(trashItemPath, stagedTrashPath); err != nil {
		return fmt.Errorf("failed to stage trash content for %s: %w", item.ID, err)
	}

	if err := fs.removeTrashMetadata(ctx, item.ID); err != nil {
		if rollbackErr := movePath(stagedTrashPath, trashItemPath); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to remove trash metadata for %s: %w", item.ID, err),
				fmt.Errorf("failed to rollback trash content for %s: %w", item.ID, rollbackErr),
			)
		}
		fs.cleanupTrashStagingDir(stagedTrashPath)
		return fmt.Errorf("failed to remove trash metadata for %s: %w", item.ID, err)
	}

	removedContent, err := fs.removeTrashPathDurably(stagedTrashPath)
	if err != nil {
		if removedContent {
			return &trashDeleteDurabilityError{err: fmt.Errorf("failed to sync deleted trash content for %s: %w", item.ID, err)}
		}
		rollbackErr := movePath(stagedTrashPath, trashItemPath)
		metadataErr := fs.addTrashMetadata(ctx, item)
		fs.cleanupTrashStagingDir(stagedTrashPath)
		if rollbackErr != nil && metadataErr != nil {
			return errors.Join(
				fmt.Errorf("failed to delete trash content for %s: %w", item.ID, err),
				fmt.Errorf("failed to rollback trash content for %s: %w", item.ID, rollbackErr),
				fmt.Errorf("failed to restore trash metadata for %s: %w", item.ID, metadataErr),
			)
		}
		if rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to delete trash content for %s: %w", item.ID, err),
				fmt.Errorf("failed to rollback trash content for %s: %w", item.ID, rollbackErr),
			)
		}
		if metadataErr != nil {
			return errors.Join(
				fmt.Errorf("failed to delete trash content for %s: %w", item.ID, err),
				fmt.Errorf("failed to restore trash metadata for %s: %w", item.ID, metadataErr),
			)
		}
		return fmt.Errorf("failed to delete trash content for %s: %w", item.ID, err)
	}

	fs.cleanupTrashStagingDir(stagedTrashPath)

	return nil
}

type trashDeleteDurabilityError struct {
	err error
}

func (e *trashDeleteDurabilityError) Error() string {
	return e.err.Error()
}

func (e *trashDeleteDurabilityError) Unwrap() error {
	return e.err
}

// GetTrashStats returns trash statistics
func (fs *FileSystem) GetTrashStats(ctx context.Context) (count int, totalSize int64, err error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	return fs.versions.GetTrashStats(ctx)
}

// GetFileCount returns the number of indexed files
func (fs *FileSystem) GetFileCount(ctx context.Context) (int, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	return fs.versions.CountFiles(ctx)
}

// ============================================================================
// Search Operations
// ============================================================================

// Search searches for files matching the query
func (fs *FileSystem) Search(ctx context.Context, query string, limit int) ([]*SearchResult, error) {
	if query == "" {
		return nil, errors.New("search query cannot be empty")
	}

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	fs.mu.RLock()
	workspaceRef := fs.workspace
	fs.mu.RUnlock()

	query = strings.ToLower(query)
	var results []*SearchResult

	// Walk through workspace
	err := walkStorageWorkspace(ctx, workspaceRef, "/", func(filePath string, info *workspace.FileInfo) error {
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
	release := fs.beginMutation()
	defer release()

	return fs.workspace.CleanupStaging(ctx)
}

func (fs *FileSystem) beginMutation() func() {
	fs.gcMu.RLock()
	fs.mu.Lock()

	return func() {
		fs.mu.Unlock()
		fs.gcMu.RUnlock()
	}
}

// cleanupVersions removes old versions based on retention policy
func (fs *FileSystem) cleanupVersions(ctx context.Context, name string) error {
	maxCount := fs.config.MaxVersions
	maxAge := fs.config.MaxVersionAge

	versions, err := fs.versions.DeleteOldVersionsDetailed(ctx, name, maxCount, maxAge)
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		return nil
	}

	// Delete orphaned objects. If deletion fails for a hash, restore the
	// corresponding version metadata so maintenance leaves extra history behind.
	deleteErr, versionsToRestore := fs.deleteRetainedVersionObjects(ctx, versions)
	if deleteErr != nil {
		if restoreErr := fs.versions.RestoreVersions(ctx, versionsToRestore); restoreErr != nil {
			return errors.Join(
				fmt.Errorf("failed to cleanup one or more version objects: %w", deleteErr),
				fmt.Errorf("failed to restore retained version metadata: %w", restoreErr),
			)
		}
		return fmt.Errorf("failed to cleanup one or more version objects: %w", deleteErr)
	}

	return nil
}

func (fs *FileSystem) deleteRetainedVersionObjects(ctx context.Context, versions []versionstore.Version) (error, []versionstore.Version) {
	versionsByHash := make(map[string][]versionstore.Version)
	for _, version := range versions {
		versionsByHash[version.Hash] = append(versionsByHash[version.Hash], version)
	}

	var deleteErr error
	var versionsToRestore []versionstore.Version
	for hash, groupedVersions := range versionsByHash {
		if err := ctx.Err(); err != nil {
			return errors.Join(deleteErr, err), append(versionsToRestore, groupedVersions...)
		}

		referenced, err := fs.versions.HasVersionReference(ctx, hash)
		if err != nil {
			deleteErr = errors.Join(deleteErr, fmt.Errorf("check version references %s: %w", hash, err))
			versionsToRestore = append(versionsToRestore, groupedVersions...)
			continue
		}
		if referenced {
			continue
		}
		if err := fs.deleteVersionObject(ctx, hash); err != nil {
			deleteErr = errors.Join(deleteErr, fmt.Errorf("delete version object %s: %w", hash, err))
			versionsToRestore = append(versionsToRestore, groupedVersions...)
		}
	}

	return deleteErr, versionsToRestore
}

func (fs *FileSystem) deleteUnreferencedVersionObjects(ctx context.Context, hashes []string) error {
	seen := make(map[string]struct{}, len(hashes))
	var deleteErr error
	for _, hash := range hashes {
		if err := ctx.Err(); err != nil {
			if deleteErr != nil {
				return errors.Join(deleteErr, err)
			}
			return err
		}
		if _, ok := seen[hash]; ok {
			continue
		}
		seen[hash] = struct{}{}

		referenced, err := fs.versions.HasVersionReference(ctx, hash)
		if err != nil {
			deleteErr = errors.Join(deleteErr, fmt.Errorf("check version references %s: %w", hash, err))
			continue
		}
		if referenced {
			continue
		}
		if err := fs.deleteVersionObject(ctx, hash); err != nil {
			deleteErr = errors.Join(deleteErr, fmt.Errorf("delete version object %s: %w", hash, err))
		}
	}

	return deleteErr
}

func (fs *FileSystem) runRetentionSweepLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	paths, err := fs.versions.ListVersionPaths(ctx)
	if err != nil {
		return fmt.Errorf("list version paths: %w", err)
	}

	for _, name := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fs.cleanupVersions(ctx, name); err != nil {
			return fmt.Errorf("cleanup versions for %s: %w", name, err)
		}
	}

	return nil
}

func (fs *FileSystem) shouldForceRetentionSweepLocked() bool {
	if fs.config == nil || fs.config.MinFreeSpace == 0 {
		return false
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(fs.workspace.Root(), &stat); err != nil {
		return false
	}

	freeBytes := stat.Bavail * uint64(stat.Bsize)
	return freeBytes < fs.config.MinFreeSpace
}

func (fs *FileSystem) refreshFileIndex(ctx context.Context, name string) {
	_ = fs.syncFileIndexFromWorkspace(ctx, name)
}

func (fs *FileSystem) syncFileIndexFromWorkspace(ctx context.Context, name string) error {
	info, err := fs.workspace.Stat(ctx, name)
	if err != nil || info.IsDir {
		return err
	}

	data, err := fs.workspace.ReadFile(ctx, name)
	if err != nil {
		return mapWorkspaceReadablePathError(err)
	}

	return fs.updateFileIndex(ctx, name, info.Size, info.ModTime, computeHash(data))
}

// GetAllReferencedHashes returns all hashes currently referenced by version store
// This is used for garbage collection
func (fs *FileSystem) GetAllReferencedHashes(ctx context.Context) ([]string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	// In the new architecture, versions are managed by versionstore
	// Return version hashes from the database
	return fs.listReferencedHashes(ctx)
}

// AcquireGCLock blocks storage mutations for the duration of a GC pass and returns the current referenced hashes.
func (fs *FileSystem) AcquireGCLock(ctx context.Context) ([]string, func(), error) {
	fs.gcMu.Lock()
	hashes, err := fs.listReferencedHashes(ctx)
	if err != nil {
		fs.gcMu.Unlock()
		return nil, nil, err
	}

	return hashes, func() {
		fs.gcMu.Unlock()
	}, nil
}

// ============================================================================
// Helper Functions
// ============================================================================

func computeHash(data []byte) string {
	h := blake3.Sum256(data)
	return fmt.Sprintf("%x", h[:])
}

func generateID() (string, error) {
	b := make([]byte, 8)
	if _, err := storageRandomRead(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func normalizeStorageWorkspacePath(name string) (string, error) {
	normalized := strings.ReplaceAll(name, "\\", "/")
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return "", ErrNotFound
		}
	}
	return workspace.CleanPath(name), nil
}

func validateStoragePath(target string) error {
	cleaned := filepath.Clean(target)
	if !filepath.IsAbs(cleaned) {
		absPath, err := filepath.Abs(cleaned)
		if err != nil {
			return err
		}
		cleaned = absPath
	}

	root := filepath.VolumeName(cleaned) + string(filepath.Separator)
	current := root
	trimmed := strings.TrimPrefix(cleaned, root)
	if trimmed == "" {
		info, err := os.Lstat(cleaned)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errStoragePathSymlink
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
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errStoragePathSymlink
		}
	}

	return nil
}

func syncStorageDir(dir string) error {
	dirHandle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func syncStorageRenameDirs(src, dst string) error {
	srcDir := filepath.Dir(src)
	dstDir := filepath.Dir(dst)
	if srcDir == dstDir {
		return syncStoragePathDir(srcDir)
	}
	if err := syncStoragePathDir(dstDir); err != nil {
		return err
	}
	return syncStoragePathDir(srcDir)
}

func collectMissingStorageDirs(dir string) ([]string, error) {
	missing := make([]string, 0)
	current := filepath.Clean(dir)
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return nil, syscall.ENOTDIR
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			if errors.Is(err, syscall.ENOTDIR) {
				return nil, err
			}
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

func syncCreatedStorageDirs(createdDirs []string) error {
	for i := len(createdDirs) - 1; i >= 0; i-- {
		if err := syncStoragePathDir(filepath.Dir(createdDirs[i])); err != nil {
			return err
		}
	}
	return nil
}

func ensureStorageDir(dir string, perm os.FileMode) error {
	createdDirs, err := collectMissingStorageDirs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	return syncCreatedStorageDirs(createdDirs)
}

func copyFile(src, dst string) error {
	if err := validateStoragePath(src); err != nil {
		return err
	}
	if err := validateStoragePath(dst); err != nil {
		return err
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	if err := ensureStorageDir(path.Dir(dst), 0755); err != nil {
		return err
	}

	dstFile, err := os.CreateTemp(path.Dir(dst), ".storage-copy-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := dstFile.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if err := dstFile.Chmod(srcInfo.Mode().Perm()); err != nil {
		dstFile.Close()
		return err
	}

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		dstFile.Close()
		return err
	}
	if err := dstFile.Sync(); err != nil {
		dstFile.Close()
		return err
	}
	if err := dstFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		return err
	}
	if err := syncStoragePathDir(path.Dir(dst)); err != nil {
		if rollbackErr := os.Remove(dst); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to sync copied file: %w", err),
				fmt.Errorf("failed to rollback copied file: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncStoragePathDir(path.Dir(dst)); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("failed to sync copied file: %w", err),
				fmt.Errorf("failed to sync copy rollback: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("failed to sync copied file: %w", err)
	}

	return nil
}

func (fs *FileSystem) readExistingFileForRollback(ctx context.Context, name string) ([]byte, bool, error) {
	info, err := fs.workspace.Stat(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return nil, false, nil
		}
		if errors.Is(err, workspace.ErrNotDir) || isPathNotDirError(err) {
			return nil, false, ErrNotDir
		}
		return nil, false, err
	}
	if info.IsDir {
		return nil, false, ErrIsDir
	}

	data, err := fs.workspace.ReadFile(ctx, name)
	if err != nil {
		mappedErr := mapWorkspaceReadablePathError(err)
		if errors.Is(mappedErr, ErrNotDir) || isPathNotDirError(err) {
			return nil, false, ErrNotDir
		}
		return nil, false, mappedErr
	}
	return data, true, nil
}

func (fs *FileSystem) restoreFileAfterIndexFailure(ctx context.Context, name string, hadPreviousFile bool, previousData []byte) error {
	if hadPreviousFile {
		return fs.workspace.WriteFile(ctx, name, previousData)
	}
	return fs.workspace.Delete(ctx, name)
}

func (fs *FileSystem) rollbackWriteVersion(ctx context.Context, name, hash string, versionRecorded, objectCreated bool) error {
	if !versionRecorded && !objectCreated {
		return nil
	}

	var rollbackErr error
	if versionRecorded {
		if err := fs.versions.DeleteVersion(ctx, name, hash); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("delete version metadata %s: %w", hash, err))
		}
	}
	if objectCreated {
		if err := fs.deleteVersionObject(ctx, hash); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("delete version object %s: %w", hash, err))
		}
	}

	return rollbackErr
}

func (fs *FileSystem) rollbackDeletedPath(ctx context.Context, name string, hadPreviousFile bool, previousData []byte, hadPreviousDir bool) error {
	if hadPreviousFile {
		return fs.workspace.WriteFile(ctx, name, previousData)
	}
	if hadPreviousDir {
		return fs.workspace.Mkdir(ctx, name)
	}
	return nil
}

func (fs *FileSystem) restoreDeletedFileIndex(ctx context.Context, name string, info *FileInfo) error {
	if info == nil || info.IsDir {
		return nil
	}

	hash := info.ContentHash
	if hash == "" {
		computedHash, err := fs.hashWorkspaceFile(ctx, name)
		if err != nil {
			return fmt.Errorf("rehash restored file index: %w", err)
		}
		hash = computedHash
	}

	return fs.updateFileIndex(ctx, name, info.Size, info.ModTime, hash)
}

func (fs *FileSystem) rollbackSoftDelete(ctx context.Context, name string, info *FileInfo, id, trashContentPath string, restoreIndex bool) error {
	fullPath := fs.workspace.FullPath(name)
	rollbackErr := movePath(trashContentPath, fullPath)
	metadataErr := fs.versions.RemoveFromTrash(ctx, id)
	var restoreTrashContentErr error
	if rollbackErr == nil && metadataErr != nil {
		restoreTrashContentErr = restoreTrashContent(fullPath, trashContentPath, info.IsDir)
	}
	if rollbackErr == nil && metadataErr == nil {
		os.RemoveAll(path.Join(fs.trashRoot, id))
	}

	var restoreIndexErr error
	if restoreIndex && rollbackErr == nil && metadataErr == nil {
		restoreIndexErr = fs.restoreDeletedFileIndex(ctx, name, info)
	}

	return errors.Join(
		wrapStorageStepError("rollback deleted content", rollbackErr),
		wrapStorageStepError("rollback trash metadata", metadataErr),
		wrapStorageStepError("restore trash content", restoreTrashContentErr),
		wrapStorageStepError("restore file index", restoreIndexErr),
	)
}

func wrapStorageStepError(step string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("failed to %s: %w", step, err)
}

func restoreTrashContent(src, dst string, isDir bool) error {
	if isDir {
		return copyDir(src, dst)
	}
	return copyFile(src, dst)
}

func (fs *FileSystem) removeTrashPathDurably(trashPath string) (bool, error) {
	if err := fs.removeTrashPath(trashPath); err != nil {
		return false, err
	}
	if err := syncStoragePathDir(path.Dir(trashPath)); err != nil {
		return true, fmt.Errorf("failed to sync trash delete directory: %w", err)
	}
	return true, nil
}

func (fs *FileSystem) cleanupTrashStagingDir(stagedTrashPath string) {
	_ = os.Remove(path.Dir(stagedTrashPath))
}

func movePath(src, dst string) error {
	if err := validateStoragePath(src); err != nil {
		return err
	}
	if err := validateStoragePath(dst); err != nil {
		return err
	}

	if err := ensureStorageDir(path.Dir(dst), 0755); err != nil {
		return err
	}

	if err := os.Rename(src, dst); err == nil {
		if syncErr := syncStorageRenameDirs(src, dst); syncErr != nil {
			if rollbackErr := os.Rename(dst, src); rollbackErr != nil {
				return errors.Join(
					fmt.Errorf("failed to sync renamed path: %w", syncErr),
					fmt.Errorf("failed to rollback renamed path: %w", rollbackErr),
				)
			}
			if rollbackSyncErr := syncStorageRenameDirs(dst, src); rollbackSyncErr != nil {
				return errors.Join(
					fmt.Errorf("failed to sync renamed path: %w", syncErr),
					fmt.Errorf("failed to sync rollback path: %w", rollbackSyncErr),
				)
			}
			return fmt.Errorf("failed to sync renamed path: %w", syncErr)
		}
		return nil
	}

	info, statErr := os.Stat(src)
	if statErr != nil {
		return statErr
	}

	if info.IsDir() {
		if err := copyDir(src, dst); err != nil {
			return err
		}
		if err := os.RemoveAll(src); err != nil {
			_ = os.RemoveAll(dst)
			return err
		}
		return nil
	}

	if err := copyFile(src, dst); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

func isPathNotDirError(err error) bool {
	return errors.Is(err, syscall.ENOTDIR)
}

func copyDir(src, dst string) error {
	if err := validateStoragePath(src); err != nil {
		return err
	}
	if err := validateStoragePath(dst); err != nil {
		return err
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := ensureStorageDir(dst, srcInfo.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chmod(dst, srcInfo.Mode().Perm()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := path.Join(src, entry.Name())
		dstPath := path.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}

		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
	}

	return nil
}
