// Package storage provides unified storage layer for MnemoNAS
// Combines workspace (native files) with versionstore (SQLite-based versioning)
package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/zeebo/blake3"

	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/versionstore"
	"github.com/seanbao/mnemonas/internal/workspace"
)

// Common errors
var (
	ErrNotFound                  = errors.New("not found")
	ErrIsDir                     = errors.New("path is a directory")
	ErrNotDir                    = errors.New("path is not a directory")
	ErrNotRegular                = errors.New("path is not a regular file")
	ErrDirNotEmpty               = errors.New("directory not empty")
	ErrAlreadyExists             = errors.New("already exists")
	ErrFileLocked                = errors.New("file is locked")
	ErrFileTooLarge              = errors.New("file too large")
	ErrVersionNotFound           = errors.New("version not found")
	ErrDeletePolicyChanged       = errors.New("delete policy changed")
	ErrDeleteTargetChanged       = errors.New("delete target changed")
	ErrInvalidDeleteIntent       = errors.New("invalid delete intent")
	ErrInvalidTrashSelection     = errors.New("invalid trash selection")
	ErrDeleteIdentityUnavailable = errors.New("delete identity verification unavailable")
	errStoragePathSymlink        = errors.New("storage path contains symlink")
	errEmptyDeleteTargetToken    = errors.New("delete target token is empty")
)

// MaxDeleteIntentTargets bounds one atomic delete-intent snapshot request.
const MaxDeleteIntentTargets = 1000

// MaxTrashSelectionIDs bounds one exact trash deletion request.
const MaxTrashSelectionIDs = 1000

// MaxTrashSelectionIDLength bounds one immutable trash identifier in bytes.
const MaxTrashSelectionIDLength = 128

// DeleteMode identifies how a live path is deleted.
type DeleteMode string

const (
	DeleteModeTrash     DeleteMode = "trash"
	DeleteModePermanent DeleteMode = "permanent"
)

// Valid reports whether the delete mode is supported.
func (m DeleteMode) Valid() bool {
	return m == DeleteModeTrash || m == DeleteModePermanent
}

// DeletePolicy is an atomic snapshot of runtime deletion settings.
type DeletePolicy struct {
	Mode                    DeleteMode
	TrashRetentionDays      int
	TrashAutoCleanupEnabled bool
	RetentionSweepInterval  time.Duration
	MaxTrashSize            int64
	Token                   string
}

// DeletePolicyExpectation identifies the deletion policy confirmed by a caller.
type DeletePolicyExpectation struct {
	Mode  DeleteMode
	Token string
}

// DeletePolicyChangedError reports that the caller's confirmed deletion policy
// no longer matches the runtime policy.
type DeletePolicyChangedError struct {
	Expected DeletePolicyExpectation
	Actual   DeletePolicy
}

func (e *DeletePolicyChangedError) Error() string {
	return fmt.Sprintf("%s: expected mode %q, actual mode %q", ErrDeletePolicyChanged, e.Expected.Mode, e.Actual.Mode)
}

func (e *DeletePolicyChangedError) Unwrap() error {
	return ErrDeletePolicyChanged
}

// DeletePathAuthorizer validates one path in a deletion tree. Delete-intent
// retries may invoke it more than once without holding the filesystem lock, so
// implementations must be idempotent and side-effect free. Implementations
// must not call FileSystem methods because deletion and fallback scans invoke
// the authorizer while holding the filesystem lock.
type DeletePathAuthorizer func(path string) error

// DeleteTargetSnapshot describes the live root selected for deletion while the
// filesystem mutation lock is held.
type DeleteTargetSnapshot struct {
	Root    FileInfo
	Entries []FileInfo
}

// DeleteTargetSnapshotOptions controls the cost and scope of a target snapshot.
type DeleteTargetSnapshotOptions struct {
	IncludeDescendants bool
	IncludeContentHash bool
}

// DeleteTargetValidator validates the current deletion target while the
// filesystem mutation lock is held. Implementations must not call FileSystem
// methods because the filesystem mutation lock is held.
type DeleteTargetValidator func(DeleteTargetSnapshot) error

// PreparedDeleteTarget contains root response metadata and the opaque token
// produced for one target during atomic delete-intent preparation.
type PreparedDeleteTarget struct {
	Path                string
	Name                string
	IsDir               bool
	Size                int64
	ModTime             time.Time
	DeleteIdentityToken string
	Token               string
}

// ObservedDeleteTarget binds a requested path to the object identity returned
// by a prior directory listing or stat operation.
type ObservedDeleteTarget struct {
	Path                  string
	ObservedIdentityToken string
}

// DeleteIntentSnapshot contains one policy snapshot and the requested targets
// captured from one validated filesystem mutation epoch.
type DeleteIntentSnapshot struct {
	Policy  DeletePolicy
	Targets []PreparedDeleteTarget
}

// DeleteTargetChangedError reports that the live target tree no longer matches
// the target token confirmed by a caller.
type DeleteTargetChangedError struct {
	Path          string
	ExpectedToken string
	ActualToken   string
}

func (e *DeleteTargetChangedError) Error() string {
	return fmt.Sprintf("%s: %s", ErrDeleteTargetChanged, e.Path)
}

func (e *DeleteTargetChangedError) Unwrap() error {
	return ErrDeleteTargetChanged
}

// DeleteIdentityChangedError reports that a selected path no longer names the
// same filesystem object observed by the caller. Tokens are intentionally not
// retained on the error so they cannot be disclosed through API details.
type DeleteIdentityChangedError struct {
	Path string
}

func (e *DeleteIdentityChangedError) Error() string {
	return fmt.Sprintf("%s: %s", ErrDeleteTargetChanged, e.Path)
}

func (e *DeleteIdentityChangedError) Unwrap() error {
	return ErrDeleteTargetChanged
}

// VisibleMutationWarningError reports that a storage mutation is already
// externally visible, but the final durability step did not complete.
type VisibleMutationWarningError struct {
	err error
}

func (e *VisibleMutationWarningError) Error() string {
	return e.err.Error()
}

func (e *VisibleMutationWarningError) Unwrap() error {
	return e.err
}

func wrapVisibleMutationWarning(err error) error {
	if err == nil {
		return nil
	}
	var warningErr *VisibleMutationWarningError
	if errors.As(err, &warningErr) {
		return err
	}
	return &VisibleMutationWarningError{err: err}
}

func isVisibleMutationWarning(err error) bool {
	var warningErr *VisibleMutationWarningError
	return errors.As(err, &warningErr)
}

var syncStoragePathDir = syncStorageDir
var syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
	dirHandle, err := rootio.OpenDirNoFollow(root, relName)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}
var storageRandomRead = rand.Read
var movePathRename = func(root *os.Root, oldRel, newRel, oldAbs, newAbs string) error {
	return rootio.RenameNoFollow(root, oldRel, newRel)
}
var movePathRemove = removeCopiedMoveSource
var movePathRemoveAll = func(root *os.Root, rel, abs string) error {
	return rootio.RemoveAllNoFollow(root, rel)
}
var beforeStorageWorkspaceWrite = func() error { return nil }
var afterValidateStoragePaths = func() error { return nil }
var afterStorageCopySourceStat = func(string) error { return nil }
var beforeCopiedFileDestinationCleanup = func(string) error { return nil }
var beforeCopiedFileSourceIsolation = func(string, string) error { return nil }
var afterCopiedFileSourceIsolation = func(string, string, string) error { return nil }
var afterCopiedFilePublish = func(string, string) error { return nil }
var createStorageCopyTempFile = createStorageTempFile
var walkStorageWorkspace = func(ctx context.Context, ws *workspace.Workspace, root string, fn workspace.WalkFunc) error {
	return ws.Walk(ctx, root, fn)
}
var walkStorageDeleteTree = func(ctx context.Context, ws *workspace.Workspace, root string, fn workspace.WalkFunc) error {
	return ws.WalkStrict(ctx, root, fn)
}
var readMountInfo = func() ([]byte, error) {
	return os.ReadFile("/proc/self/mountinfo")
}

var errDeleteSnapshotRootComplete = errors.New("delete snapshot root complete")

const defaultMaxWriteSize int64 = 10 * 1024 * 1024 * 1024 // 10GB

// FileInfo represents file metadata
type FileInfo struct {
	Path                string      `json:"path"`
	Name                string      `json:"name"`
	IsDir               bool        `json:"is_dir"`
	Mode                os.FileMode `json:"-"`
	Size                int64       `json:"size"`
	ModTime             time.Time   `json:"mod_time"`
	DeleteIdentityToken string      `json:"delete_identity_token,omitempty"`
	ContentHash         string      `json:"content_hash,omitempty"`
	Versioned           bool        `json:"versioned"` // Whether this file has version management
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
	ExpiresAt    time.Time `json:"expires_at"`
	IsDir        bool      `json:"is_dir"`
	HadVersions  bool      `json:"had_versions"`
	RestoreData  []byte    `json:"-"`
}

// TrashSelectionResult partitions every requested trash ID by its outcome.
type TrashSelectionResult struct {
	DeletedIDs   []string
	RemainingIDs []string
	SkippedIDs   []string
}

// TrashRootAuthorizationError reports that authorization failed for a trash
// item's root restore path rather than one of its descendants.
type TrashRootAuthorizationError struct {
	Path string
	err  error
}

func (e *TrashRootAuthorizationError) Error() string {
	if e == nil || e.err == nil {
		return "trash root authorization failed"
	}
	return e.err.Error()
}

func (e *TrashRootAuthorizationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// DiskStats describes capacity for the filesystem hosting the workspace.
type DiskStats struct {
	TotalBytes                uint64
	FreeBytes                 uint64
	AvailableBytes            uint64
	UsedBytes                 uint64
	UsageRatio                float64
	FileSystemType            string
	MountPoint                string
	MountSource               string
	MountOptions              string
	NativeDataChecksumSupport bool
}

var (
	errDiskStatsInvalidBlockSize = errors.New("filesystem reported invalid block size")
	errDiskStatsCapacityOverflow = errors.New("filesystem capacity exceeds uint64")
)

func diskStatsFromStatfsBlocks(blocks, freeBlocks, availableBlocks uint64, blockSize int64, mountDetails diskMountDetails) (DiskStats, error) {
	if blockSize <= 0 {
		return DiskStats{}, errDiskStatsInvalidBlockSize
	}

	totalBytes, err := diskStatsBytes(blocks, uint64(blockSize))
	if err != nil {
		return DiskStats{}, err
	}
	freeBytes, err := diskStatsBytes(freeBlocks, uint64(blockSize))
	if err != nil {
		return DiskStats{}, err
	}
	availableBytes, err := diskStatsBytes(availableBlocks, uint64(blockSize))
	if err != nil {
		return DiskStats{}, err
	}

	return diskStatsFromUsage(totalBytes, freeBytes, availableBytes, mountDetails), nil
}

func diskStatsBytes(blocks, blockSize uint64) (uint64, error) {
	hi, lo := bits.Mul64(blocks, blockSize)
	if hi != 0 {
		return 0, errDiskStatsCapacityOverflow
	}
	return lo, nil
}

func diskStatsFromUsage(totalBytes, freeBytes, availableBytes uint64, mountDetails diskMountDetails) DiskStats {
	if freeBytes > totalBytes {
		freeBytes = totalBytes
	}
	if availableBytes > totalBytes {
		availableBytes = totalBytes
	}
	usedBytes := totalBytes - freeBytes
	usageRatio := 0.0
	if totalBytes > 0 {
		usageRatio = float64(usedBytes) / float64(totalBytes)
	}
	return DiskStats{
		TotalBytes:                totalBytes,
		FreeBytes:                 freeBytes,
		AvailableBytes:            availableBytes,
		UsedBytes:                 usedBytes,
		UsageRatio:                usageRatio,
		FileSystemType:            mountDetails.FileSystemType,
		MountPoint:                mountDetails.MountPoint,
		MountSource:               mountDetails.MountSource,
		MountOptions:              mountDetails.MountOptions,
		NativeDataChecksumSupport: filesystemHasNativeDataChecksumSupport(mountDetails.FileSystemType),
	}
}

type PathDeleteHookResult struct {
	Rollback    func() error
	RestoreData []byte
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

// SearchFilter decides whether a search result should be included.
type SearchFilter func(*SearchResult) (bool, error)

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
	MaxVersions            int
	MaxVersionAge          time.Duration
	MinFreeSpace           uint64
	RetentionSweepInterval time.Duration
	TrashEnabled           *bool
	TrashRetentionDays     int
	MaxTrashSize           int64
}

// RuntimePolicySettings contains retention and deletion settings that must be
// published as one runtime snapshot.
type RuntimePolicySettings struct {
	MaxVersions        int
	MaxVersionAge      time.Duration
	MinFreeSpace       uint64
	SweepInterval      time.Duration
	TrashEnabled       bool
	TrashRetentionDays int
	MaxTrashSize       int64
}

// FileSystem provides unified storage operations
type FileSystem struct {
	workspace                 *workspace.Workspace
	filesRootHandle           *os.Root
	trashRootHandle           *os.Root
	versions                  *versionstore.Store
	policy                    *versionstore.VersioningPolicy
	trashRoot                 string
	config                    *Config
	onPathRenamed             func(ctx context.Context, oldPath, newPath string) error
	onPathDeleted             func(ctx context.Context, path string) (*PathDeleteHookResult, error)
	listReferencedHashes      func(ctx context.Context) ([]string, error)
	listVersionPaths          func(ctx context.Context) ([]string, error)
	getVersions               func(ctx context.Context, path string) ([]versionstore.Version, error)
	deleteFileIndex           func(ctx context.Context, path string) error
	deleteFileIndexPrefix     func(ctx context.Context, path string) error
	updateFileIndex           func(ctx context.Context, path string, size int64, modTime time.Time, hash string) error
	hasVersionObject          func(ctx context.Context, hash string) (bool, error)
	getVersionObject          func(ctx context.Context, hash string) ([]byte, error)
	putVersionObject          func(ctx context.Context, data []byte) (string, error)
	addFileVersion            func(ctx context.Context, path, hash string, size int64, comment string) error
	deleteFileVersion         func(ctx context.Context, path, hash string) error
	deleteVersionObject       func(ctx context.Context, hash string) error
	hashDeleteTargetFile      func(ctx context.Context, path string) (string, error)
	readDeleteMountPoints     func() ([]string, error)
	addTrashMetadata          func(ctx context.Context, item *versionstore.TrashItem) error
	updateTrashRestoreData    func(ctx context.Context, id string, restoreData []byte) error
	removeTrashMetadata       func(ctx context.Context, id string) error
	writeWorkspacePath        func(ctx context.Context, name string, data []byte) error
	mkdirWorkspacePath        func(ctx context.Context, name string) error
	copyWorkspacePath         func(ctx context.Context, oldName, newName string) error
	deleteStagedWorkspacePath func(ctx context.Context, logicalName string, remove func() error) error
	renameWorkspacePath       func(ctx context.Context, oldName, newName string) error
	renameMetadataPath        func(ctx context.Context, oldName, newName string) error
	renameHistoryMetadataPath func(ctx context.Context, oldName, newName string) error
	removeTrashPath           func(path string) error
	trashMutationBlocked      error
	hookMu                    sync.RWMutex
	gcMu                      sync.RWMutex
	mu                        sync.RWMutex
	mutationEpoch             uint64 // guarded by mu

	hashStagedDeleteSourceFile    func(ctx context.Context, file *os.File) (string, error)
	hashStagedTrashContentFile    func(ctx context.Context, file *os.File) (string, error)
	hashStorageCopiedFile         func(file *os.File) (string, error)
	hashDeleteWitnessRecoveryFile func(file *os.File) (string, error)
}

// UpdateTrashSettings applies trash settings to the running filesystem.
func (fs *FileSystem) UpdateTrashSettings(enabled bool, retentionDays int, maxSize int64) {
	fs.lockMutation()
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
func (fs *FileSystem) UpdateRetentionSettings(maxVersions int, maxVersionAge time.Duration, minFreeSpace uint64, sweepInterval time.Duration) {
	fs.lockMutation()
	defer fs.mu.Unlock()

	if fs.config == nil {
		return
	}
	fs.config.MaxVersions = maxVersions
	fs.config.MaxVersionAge = maxVersionAge
	fs.config.MinFreeSpace = minFreeSpace
	fs.config.RetentionSweepInterval = sweepInterval
}

// UpdateRuntimePolicySettings publishes retention and deletion settings atomically.
func (fs *FileSystem) UpdateRuntimePolicySettings(settings RuntimePolicySettings) {
	fs.lockMutation()
	defer fs.mu.Unlock()

	if fs.config == nil {
		return
	}
	if fs.config.TrashEnabled == nil {
		fs.config.TrashEnabled = new(bool)
	}
	fs.config.MaxVersions = settings.MaxVersions
	fs.config.MaxVersionAge = settings.MaxVersionAge
	fs.config.MinFreeSpace = settings.MinFreeSpace
	fs.config.RetentionSweepInterval = settings.SweepInterval
	*fs.config.TrashEnabled = settings.TrashEnabled
	fs.config.TrashRetentionDays = settings.TrashRetentionDays
	fs.config.MaxTrashSize = settings.MaxTrashSize
}

// CurrentDeletePolicy returns a consistent runtime deletion-policy snapshot.
func (fs *FileSystem) CurrentDeletePolicy() DeletePolicy {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	return fs.currentDeletePolicyLocked()
}

func (fs *FileSystem) currentDeletePolicyLocked() DeletePolicy {
	policy := DeletePolicy{Mode: DeleteModeTrash}
	if fs.config == nil {
		policy.Token = deletePolicyToken(policy)
		return policy
	}
	if fs.config.TrashEnabled != nil && !*fs.config.TrashEnabled {
		policy.Mode = DeleteModePermanent
	}
	policy.TrashRetentionDays = fs.config.TrashRetentionDays
	policy.TrashAutoCleanupEnabled = fs.config.RetentionSweepInterval > 0
	policy.RetentionSweepInterval = fs.config.RetentionSweepInterval
	policy.MaxTrashSize = fs.config.MaxTrashSize
	policy.Token = deletePolicyToken(policy)
	return policy
}

func deletePolicyToken(policy DeletePolicy) string {
	hasher := sha256.New()
	_, _ = fmt.Fprintf(hasher,
		"version=1\nmode=%s\ntrash_retention_days=%d\ntrash_auto_cleanup_enabled=%t\nretention_sweep_interval_ns=%d\nmax_trash_size=%d\n",
		policy.Mode,
		policy.TrashRetentionDays,
		policy.TrashAutoCleanupEnabled,
		policy.RetentionSweepInterval.Nanoseconds(),
		policy.MaxTrashSize,
	)
	return hex.EncodeToString(hasher.Sum(nil))
}

// UpdateVersioningSettings applies versioning policy settings to the running filesystem.
func (fs *FileSystem) UpdateVersioningSettings(extensions, filenames []string, maxVersionedSize int64) {
	fs.lockMutation()
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

func (fs *FileSystem) currentVersioningPolicy() *versionstore.VersioningPolicy {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	if fs.policy == nil {
		return nil
	}

	policy := *fs.policy
	policy.AutoVersionedExtensions = append([]string(nil), fs.policy.AutoVersionedExtensions...)
	policy.AutoVersionedFilenames = append([]string(nil), fs.policy.AutoVersionedFilenames...)
	return &policy
}

// SetDataplaneClient swaps the dataplane client used by version storage operations.
func (fs *FileSystem) SetDataplaneClient(client *dataplane.Client) {
	fs.lockMutation()
	defer fs.mu.Unlock()

	if fs.config != nil {
		fs.config.Dataplane = client
	}
	if fs.versions != nil {
		fs.versions.SetDataplaneClient(client)
	}
}

// SetPathChangeHooks registers callbacks for committed rename/delete operations.
func (fs *FileSystem) SetPathChangeHooks(onRename func(ctx context.Context, oldPath, newPath string) error, onDelete func(ctx context.Context, path string) (*PathDeleteHookResult, error)) {
	fs.hookMu.Lock()
	defer fs.hookMu.Unlock()
	fs.onPathRenamed = onRename
	fs.onPathDeleted = onDelete
}

// RunRetentionSweep applies version and trash retention rules.
func (fs *FileSystem) RunRetentionSweep(ctx context.Context) error {
	release := fs.beginMutation()
	defer release()

	versionErr := fs.runRetentionSweepLocked(ctx)
	_, trashErr := fs.cleanupExpiredTrashLocked(ctx)
	return errors.Join(versionErr, trashErr)
}

// New creates a new FileSystem
func New(cfg *Config) (*FileSystem, error) {
	if cfg.Dataplane == nil {
		return nil, errors.New("dataplane client is required")
	}
	trashRoot, err := normalizeStorageHostPath(cfg.TrashRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize trash root: %w", err)
	}

	if err := validateStoragePath(cfg.InternalRoot); err != nil {
		return nil, fmt.Errorf("failed to validate internal root: %w", err)
	}
	if err := validateStoragePath(trashRoot); err != nil {
		return nil, fmt.Errorf("failed to validate trash root: %w", err)
	}

	// Create workspace for native file operations
	ws, err := workspace.New(cfg.FilesRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}
	cleanupWorkspace := true
	defer func() {
		if cleanupWorkspace {
			_ = ws.Close()
		}
	}()

	// Create version store
	vs, err := versionstore.New(versionstore.Config{
		DBPath:    path.Join(cfg.InternalRoot, "index.db"),
		Dataplane: cfg.Dataplane,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create version store: %w", err)
	}
	cleanupVersionStore := true
	defer func() {
		if cleanupVersionStore {
			_ = vs.Close()
		}
	}()

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
	if err := ensureStorageDir(trashRoot, 0700); err != nil {
		return nil, fmt.Errorf("failed to create trash directory: %w", err)
	}

	filesRootHandle, err := os.OpenRoot(ws.Root())
	if err != nil {
		return nil, fmt.Errorf("failed to open files root: %w", err)
	}
	cleanupFilesRoot := true
	defer func() {
		if cleanupFilesRoot {
			_ = filesRootHandle.Close()
		}
	}()
	trashRootHandle, err := os.OpenRoot(trashRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to open trash root: %w", err)
	}
	cleanupTrashRoot := true
	defer func() {
		if cleanupTrashRoot {
			_ = trashRootHandle.Close()
		}
	}()
	cleanupWorkspace = false
	cleanupVersionStore = false
	cleanupFilesRoot = false
	cleanupTrashRoot = false

	fs := &FileSystem{
		workspace:                 ws,
		filesRootHandle:           filesRootHandle,
		trashRootHandle:           trashRootHandle,
		versions:                  vs,
		policy:                    policy,
		trashRoot:                 trashRoot,
		config:                    cfg,
		listReferencedHashes:      vs.GetAllVersionHashes,
		listVersionPaths:          vs.ListVersionPaths,
		getVersions:               vs.GetVersions,
		deleteFileIndex:           vs.DeleteFileIndex,
		deleteFileIndexPrefix:     vs.DeleteFileIndexPrefix,
		updateFileIndex:           vs.UpdateFileIndex,
		hasVersionObject:          vs.HasObject,
		getVersionObject:          vs.GetObject,
		putVersionObject:          vs.PutObject,
		addFileVersion:            vs.AddVersion,
		deleteFileVersion:         vs.DeleteVersion,
		deleteVersionObject:       vs.DeleteObject,
		addTrashMetadata:          vs.AddToTrash,
		updateTrashRestoreData:    vs.UpdateTrashRestoreData,
		removeTrashMetadata:       vs.RemoveFromTrash,
		writeWorkspacePath:        nil,
		mkdirWorkspacePath:        ws.Mkdir,
		copyWorkspacePath:         ws.Copy,
		deleteStagedWorkspacePath: func(_ context.Context, _ string, remove func() error) error { return remove() },
		renameWorkspacePath:       ws.Rename,
		renameMetadataPath:        vs.RenamePath,
		renameHistoryMetadataPath: vs.RenamePathHistory,
	}
	fs.removeTrashPath = fs.removeCommittedTrashPurgeStage
	return fs, nil
}

// Close closes the filesystem
func (fs *FileSystem) Close() error {
	var err error
	if fs.versions != nil {
		err = errors.Join(err, fs.versions.Close())
	}
	if fs.workspace != nil {
		err = errors.Join(err, fs.workspace.Close())
	}
	if fs.filesRootHandle != nil {
		err = errors.Join(err, fs.filesRootHandle.Close())
		fs.filesRootHandle = nil
	}
	if fs.trashRootHandle != nil {
		err = errors.Join(err, fs.trashRootHandle.Close())
		fs.trashRootHandle = nil
	}
	return err
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
			Mode:    os.ModeDir,
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
		Path:                info.Path,
		Name:                info.Name,
		IsDir:               info.IsDir,
		Mode:                info.Mode,
		Size:                info.Size,
		ModTime:             info.ModTime,
		DeleteIdentityToken: info.DeleteIdentityToken,
	}
	policy := fs.currentVersioningPolicy()

	// Check if file has versioning
	if !info.IsDir && policy != nil {
		fileInfo.Versioned = policy.ShouldVersion(ctx, name, info.Size)
		if contentHash, err := fs.hashWorkspaceFile(ctx, name); err == nil {
			fileInfo.ContentHash = contentHash
		}
	}

	return fileInfo, nil
}

// ReadDir reads directory contents
func (fs *FileSystem) ReadDir(ctx context.Context, name string) ([]*FileInfo, error) {
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
	policy := fs.currentVersioningPolicy()
	for _, e := range entries {
		childPath, childName, err := storageReadDirChildPath(name, e)
		if err != nil {
			return nil, err
		}
		info := &FileInfo{
			Path:                childPath,
			Name:                childName,
			IsDir:               e.IsDir,
			Mode:                e.Mode,
			Size:                e.Size,
			ModTime:             e.ModTime,
			DeleteIdentityToken: e.DeleteIdentityToken,
		}
		if !e.IsDir && policy != nil {
			info.Versioned = policy.ShouldVersion(ctx, childPath, e.Size)
		}
		result = append(result, info)
	}

	return result, nil
}

func storageReadDirChildPath(parentPath string, child *workspace.FileInfo) (string, string, error) {
	if child == nil {
		return "", "", ErrNotFound
	}
	cleanParent := path.Clean(parentPath)
	childPath := child.Path
	if childPath == "" {
		childName, err := safeStorageReadDirFallbackChildName(child.Name)
		if err != nil {
			return "", "", err
		}
		childPath = path.Join(cleanParent, childName)
	}
	if strings.Contains(childPath, "\\") {
		return "", "", ErrNotFound
	}
	cleanChild, err := normalizeStorageWorkspacePath(childPath)
	if err != nil {
		return "", "", err
	}
	if cleanChild == cleanParent || path.Dir(cleanChild) != cleanParent {
		return "", "", ErrNotFound
	}
	return cleanChild, path.Base(cleanChild), nil
}

func safeStorageReadDirFallbackChildName(name string) (string, error) {
	childName := strings.ReplaceAll(name, "\\", "/")
	if childName == "" || strings.Contains(childName, "/") {
		return "", ErrNotFound
	}
	if _, err := normalizeStorageWorkspacePath(childName); err != nil {
		return "", err
	}
	return childName, nil
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

// OpenFileSnapshot opens a file for reading and returns metadata derived from the
// same open file handle so callers can serve a consistent snapshot.
func (fs *FileSystem) OpenFileSnapshot(ctx context.Context, name string) (*os.File, *FileInfo, error) {
	fs.mu.RLock()

	var err error
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		fs.mu.RUnlock()
		return nil, nil, err
	}

	f, err := fs.workspace.OpenFile(ctx, name)
	fs.mu.RUnlock()
	if err != nil {
		return nil, nil, mapWorkspaceReadablePathError(err)
	}

	info, err := fs.snapshotFileInfo(ctx, name, f, fs.currentVersioningPolicy())
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}

	return f, info, nil
}

func (fs *FileSystem) snapshotFileInfo(ctx context.Context, name string, file *os.File, policy *versionstore.VersioningPolicy) (*FileInfo, error) {
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	fileInfo := &FileInfo{
		Path:    name,
		Name:    path.Base(name),
		IsDir:   false,
		Mode:    stat.Mode(),
		Size:    stat.Size(),
		ModTime: stat.ModTime(),
	}
	if policy != nil {
		fileInfo.Versioned = policy.ShouldVersion(ctx, name, stat.Size())
	}
	if contentHash, err := hashOpenWorkspaceFile(file); err == nil {
		fileInfo.ContentHash = contentHash
	}

	return fileInfo, nil
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
	if err := rejectStorageRootMutation(name); err != nil {
		return err
	}
	previousData, hadPreviousFile, err := fs.readExistingFileForRollback(ctx, name)
	if err != nil {
		return err
	}
	createdWorkspaceDirs := []string(nil)

	var rollbackVersionHash string
	var rollbackVersionRecorded bool
	var rollbackVersionObjectCreated bool
	rollbackMutation := func(cause error) error {
		versionRollbackErr := fs.rollbackWriteVersion(ctx, name, rollbackVersionHash, rollbackVersionRecorded, rollbackVersionObjectCreated)
		fileRollbackErr := fs.restoreFileAfterIndexFailure(ctx, name, hadPreviousFile, previousData, createdWorkspaceDirs)
		if fileRollbackErr != nil && versionRollbackErr != nil {
			return errors.Join(
				cause,
				fmt.Errorf("failed to rollback file content: %w", fileRollbackErr),
				fmt.Errorf("failed to rollback version metadata: %w", versionRollbackErr),
			)
		}
		if fileRollbackErr != nil {
			return errors.Join(cause, fmt.Errorf("failed to rollback file content: %w", fileRollbackErr))
		}
		if versionRollbackErr != nil {
			return errors.Join(cause, fmt.Errorf("failed to rollback version metadata: %w", versionRollbackErr))
		}
		return cause
	}

	hasher := blake3.New()

	if err := fs.validateWorkspaceParentForWrite(name); err != nil {
		return err
	}
	if err := beforeStorageWorkspaceWrite(); err != nil {
		return rollbackMutation(err)
	}

	written, err := fs.workspace.WriteFileFromReaderWithOptions(ctx, name, io.TeeReader(r, hasher), workspace.WriteFileOptions{
		MaxBytes:    defaultMaxWriteSize,
		SyncParent:  syncStoragePathDir,
		CreatedDirs: &createdWorkspaceDirs,
	})
	if err != nil {
		mappedErr := mapWorkspaceWritablePathError(unwrapWorkspaceVisibleMutationError(err))
		if workspace.IsVisibleMutationWarning(err) {
			return rollbackMutation(mappedErr)
		}
		return mappedErr
	}

	shouldVersion := fs.policy.ShouldVersion(ctx, name, written)
	if shouldVersion && hadPreviousFile {
		oldData := previousData
		candidateHash := computeHash(oldData)
		rollbackVersionHash = candidateHash
		hasObject, err := fs.hasVersionObject(ctx, candidateHash)
		if err != nil {
			return rollbackMutation(fmt.Errorf("failed to check existing version object: %w", err))
		}
		rollbackVersionObjectCreated = !hasObject

		_, versionErr := fs.versions.GetVersion(ctx, name, candidateHash)
		versionAlreadyRecorded := versionErr == nil
		if versionErr != nil && !errors.Is(versionErr, versionstore.ErrNotFound) {
			return rollbackMutation(fmt.Errorf("failed to check existing version: %w", versionErr))
		}

		oldHash, err := fs.putVersionObject(ctx, oldData)
		if err != nil {
			return rollbackMutation(fmt.Errorf("failed to store version: %w", err))
		}
		rollbackVersionHash = oldHash

		if !versionAlreadyRecorded {
			if err := fs.addFileVersion(ctx, name, oldHash, int64(len(oldData)), ""); err != nil {
				return rollbackMutation(fmt.Errorf("failed to record version: %w", err))
			}
			rollbackVersionRecorded = true
		}
	}

	newHash := fmt.Sprintf("%x", hasher.Sum(nil))
	if err := fs.updateFileIndex(ctx, name, written, time.Now(), newHash); err != nil {
		return rollbackMutation(fmt.Errorf("failed to update file index: %w", err))
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

	mkdirWorkspacePath := fs.mkdirWorkspacePath
	if mkdirWorkspacePath == nil {
		mkdirWorkspacePath = fs.workspace.Mkdir
	}
	err = mkdirWorkspacePath(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrAlreadyExists) {
			return ErrAlreadyExists
		}
		if errors.Is(err, workspace.ErrNotDir) {
			return ErrNotDir
		}
		if workspace.IsVisibleMutationWarning(err) {
			return wrapVisibleMutationWarning(err)
		}
		return err
	}

	return nil
}

// Delete deletes a file or directory using the current runtime policy.
func (fs *FileSystem) Delete(ctx context.Context, name string) error {
	return fs.delete(ctx, name, nil, DeleteTargetSnapshotOptions{}, nil, nil)
}

// DeleteWithPathAuthorizer deletes a path under the current runtime policy
// after authorizing its complete live tree under the mutation lock.
func (fs *FileSystem) DeleteWithPathAuthorizer(ctx context.Context, name string, authorize DeletePathAuthorizer) error {
	return fs.delete(ctx, name, nil, DeleteTargetSnapshotOptions{}, nil, authorize)
}

// DeleteWithTargetValidator deletes a path under the current runtime policy
// after validating its current root snapshot and complete live tree under the
// same mutation lock.
func (fs *FileSystem) DeleteWithTargetValidator(ctx context.Context, name string, validate DeleteTargetValidator, authorize DeletePathAuthorizer) error {
	return fs.DeleteWithTargetValidatorOptions(ctx, name, DeleteTargetSnapshotOptions{IncludeContentHash: true}, validate, authorize)
}

// DeleteWithTargetValidatorOptions deletes a path after capturing the requested
// root snapshot fields under the filesystem mutation lock.
func (fs *FileSystem) DeleteWithTargetValidatorOptions(ctx context.Context, name string, options DeleteTargetSnapshotOptions, validate DeleteTargetValidator, authorize DeletePathAuthorizer) error {
	return fs.delete(ctx, name, nil, options, validate, authorize)
}

// DeleteWithExpectedPolicy deletes a path only when the runtime policy still
// matches the caller's confirmed policy and every live tree path is authorized.
func (fs *FileSystem) DeleteWithExpectedPolicy(ctx context.Context, name string, expected DeletePolicyExpectation, authorize DeletePathAuthorizer) error {
	return fs.delete(ctx, name, &expected, DeleteTargetSnapshotOptions{}, nil, authorize)
}

// PrepareDeleteIntents captures one deletion policy and every requested live
// target tree from one validated filesystem mutation epoch.
func (fs *FileSystem) PrepareDeleteIntents(ctx context.Context, names []string, authorize DeletePathAuthorizer) (DeleteIntentSnapshot, error) {
	normalized, err := normalizeDeleteIntentPaths(names)
	if err != nil {
		return DeleteIntentSnapshot{}, err
	}
	targets := make([]ObservedDeleteTarget, 0, len(normalized))
	for _, name := range normalized {
		targets = append(targets, ObservedDeleteTarget{Path: name})
	}
	return fs.prepareDeleteIntents(ctx, targets, authorize)
}

// PrepareObservedDeleteIntents captures delete intents only when every path
// still names the same filesystem object returned by a prior listing or stat.
func (fs *FileSystem) PrepareObservedDeleteIntents(ctx context.Context, targets []ObservedDeleteTarget, authorize DeletePathAuthorizer) (DeleteIntentSnapshot, error) {
	normalized, err := normalizeObservedDeleteIntentTargets(targets)
	if err != nil {
		return DeleteIntentSnapshot{}, err
	}
	return fs.prepareDeleteIntents(ctx, normalized, authorize)
}

const maxOptimisticDeleteIntentAttempts = 2

func (fs *FileSystem) prepareDeleteIntents(ctx context.Context, targets []ObservedDeleteTarget, authorize DeletePathAuthorizer) (DeleteIntentSnapshot, error) {
	for attempt := 0; attempt < maxOptimisticDeleteIntentAttempts; attempt++ {
		fs.mu.RLock()
		if err := ctx.Err(); err != nil {
			fs.mu.RUnlock()
			return DeleteIntentSnapshot{}, err
		}
		epoch := fs.mutationEpoch
		policy := fs.currentDeletePolicyLocked()
		fs.mu.RUnlock()

		preparedTargets, scanErr := fs.prepareDeleteIntentTargets(ctx, targets, authorize)
		if scanErr != nil && (errors.Is(scanErr, context.Canceled) || errors.Is(scanErr, context.DeadlineExceeded)) {
			return DeleteIntentSnapshot{}, scanErr
		}
		if err := ctx.Err(); err != nil {
			return DeleteIntentSnapshot{}, err
		}

		fs.mu.RLock()
		validationErr := ctx.Err()
		stable := fs.mutationEpoch == epoch
		fs.mu.RUnlock()
		if validationErr != nil {
			return DeleteIntentSnapshot{}, validationErr
		}
		if !stable {
			continue
		}
		if scanErr != nil {
			return DeleteIntentSnapshot{}, scanErr
		}

		return DeleteIntentSnapshot{Policy: policy, Targets: preparedTargets}, nil
	}

	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return DeleteIntentSnapshot{}, err
	}

	policy := fs.currentDeletePolicyLocked()
	preparedTargets, err := fs.prepareDeleteIntentTargets(ctx, targets, authorize)
	if err != nil {
		return DeleteIntentSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return DeleteIntentSnapshot{}, err
	}
	return DeleteIntentSnapshot{Policy: policy, Targets: preparedTargets}, nil
}

func (fs *FileSystem) prepareDeleteIntentTargets(ctx context.Context, targets []ObservedDeleteTarget, authorize DeletePathAuthorizer) ([]PreparedDeleteTarget, error) {
	result := make([]PreparedDeleteTarget, 0, len(targets))
	options := DeleteTargetSnapshotOptions{IncludeDescendants: true, IncludeContentHash: true}
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		stream, err := newDeleteTreeMerkleStreamV3(target.Path)
		if err != nil {
			return nil, err
		}
		var root FileInfo
		rootSeen := false
		err = fs.walkDeleteTargetEntriesWithObservedIdentity(ctx, target.Path, options, target.ObservedIdentityToken, authorize, nil, func(entry FileInfo) error {
			if entry.Path == target.Path {
				root = entry
				rootSeen = true
			}
			return stream.add(entry)
		})
		if err != nil {
			return nil, err
		}
		if !rootSeen {
			return nil, ErrNotFound
		}
		token, err := stream.finish()
		if err != nil {
			return nil, err
		}
		if token == "" {
			return nil, errEmptyDeleteTargetToken
		}
		result = append(result, PreparedDeleteTarget{
			Path:                root.Path,
			Name:                root.Name,
			IsDir:               root.IsDir,
			Size:                root.Size,
			ModTime:             root.ModTime,
			DeleteIdentityToken: root.DeleteIdentityToken,
			Token:               token,
		})
	}
	return result, nil
}

// DeleteWithExpectedPolicyAndTarget deletes a path only when both the complete
// runtime policy and complete live target tree still match the caller's intent.
func (fs *FileSystem) DeleteWithExpectedPolicyAndTarget(ctx context.Context, name string, expectedPolicy DeletePolicyExpectation, expectedTargetToken string, authorize DeletePathAuthorizer) error {
	release := fs.beginMutation()
	defer release()

	var err error
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		return err
	}
	if err := rejectStorageRootMutation(name); err != nil {
		return err
	}

	policy := fs.currentDeletePolicyLocked()
	if expectedPolicy.Mode != policy.Mode || expectedPolicy.Token != policy.Token {
		return &DeletePolicyChangedError{Expected: expectedPolicy, Actual: policy}
	}

	snapshot, err := fs.deleteTargetSnapshotLocked(ctx, name, DeleteTargetSnapshotOptions{
		IncludeDescendants: true,
		IncludeContentHash: true,
	}, authorize)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrNotDir) {
			return &DeleteTargetChangedError{
				Path:          name,
				ExpectedToken: expectedTargetToken,
			}
		}
		return err
	}
	actualTargetToken, err := deleteTreeTokenV3(snapshot)
	if err != nil {
		return err
	}
	if actualTargetToken == "" {
		return errEmptyDeleteTargetToken
	}
	if expectedTargetToken != actualTargetToken {
		return &DeleteTargetChangedError{
			Path:          name,
			ExpectedToken: expectedTargetToken,
			ActualToken:   actualTargetToken,
		}
	}

	return fs.deleteWithPolicyLocked(ctx, name, policy, snapshot)
}

func (fs *FileSystem) delete(ctx context.Context, name string, expected *DeletePolicyExpectation, snapshotOptions DeleteTargetSnapshotOptions, validate DeleteTargetValidator, authorize DeletePathAuthorizer) error {
	release := fs.beginMutation()
	defer release()

	var err error
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		return err
	}
	if err := rejectStorageRootMutation(name); err != nil {
		return err
	}
	policy := fs.currentDeletePolicyLocked()
	if expected != nil && (expected.Mode != policy.Mode || expected.Token != policy.Token) {
		return &DeletePolicyChangedError{Expected: *expected, Actual: policy}
	}
	if err := fs.authorizeDeleteTreeLocked(ctx, name, authorize); err != nil {
		return err
	}
	guardSnapshot, err := fs.deleteTargetSnapshotLocked(ctx, name, DeleteTargetSnapshotOptions{
		IncludeDescendants: true,
		IncludeContentHash: snapshotOptions.IncludeContentHash,
	}, nil)
	if err != nil {
		return err
	}
	if validate != nil {
		if err := validate(projectDeleteTargetSnapshot(guardSnapshot, snapshotOptions)); err != nil {
			return err
		}
	}
	return fs.deleteWithPolicyLocked(ctx, name, policy, guardSnapshot)
}

func (fs *FileSystem) deleteWithPolicyLocked(ctx context.Context, name string, policy DeletePolicy, snapshot DeleteTargetSnapshot) error {
	return fs.deletePreparedWithPolicyLocked(ctx, name, policy, snapshot)
}

func (fs *FileSystem) deleteTargetSnapshotLocked(ctx context.Context, name string, options DeleteTargetSnapshotOptions, authorize DeletePathAuthorizer) (DeleteTargetSnapshot, error) {
	return fs.deleteTargetSnapshotWithObservedIdentityLocked(ctx, name, options, "", authorize)
}

func (fs *FileSystem) deleteTargetSnapshotWithObservedIdentityLocked(ctx context.Context, name string, options DeleteTargetSnapshotOptions, observedIdentityToken string, authorize DeletePathAuthorizer) (DeleteTargetSnapshot, error) {
	entries := make([]FileInfo, 0, 1)
	err := fs.walkDeleteTargetEntriesWithObservedIdentityLocked(ctx, name, options, observedIdentityToken, authorize, func(entry FileInfo) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return DeleteTargetSnapshot{}, err
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	for _, entry := range entries {
		if entry.Path == name {
			return DeleteTargetSnapshot{Root: entry, Entries: entries}, nil
		}
	}
	return DeleteTargetSnapshot{}, ErrNotFound
}

func (fs *FileSystem) walkDeleteTargetEntriesWithObservedIdentityLocked(ctx context.Context, name string, options DeleteTargetSnapshotOptions, observedIdentityToken string, authorize DeletePathAuthorizer, visit func(FileInfo) error) error {
	return fs.walkDeleteTargetEntriesWithObservedIdentity(ctx, name, options, observedIdentityToken, authorize, fs.policy, visit)
}

func (fs *FileSystem) walkDeleteTargetEntriesWithObservedIdentity(ctx context.Context, name string, options DeleteTargetSnapshotOptions, observedIdentityToken string, authorize DeletePathAuthorizer, policy *versionstore.VersioningPolicy, visit func(FileInfo) error) error {
	mountBoundary := fs.captureDeleteMountBoundary(fs.workspace.Root())
	visitWorkspaceEntry := func(filePath string, info *workspace.FileInfo) error {
		if info == nil {
			return ErrNotFound
		}
		if authorize != nil {
			if err := authorize(filePath); err != nil {
				return err
			}
		}
		if filePath == name && observedIdentityToken != "" && info.DeleteIdentityToken == "" {
			return ErrDeleteIdentityUnavailable
		}
		if err := mountBoundary.checkWorkspacePath(filePath); err != nil {
			return err
		}
		if !info.IsDir && !info.Mode.IsRegular() {
			return workspace.ErrNotRegular
		}
		if filePath == name && observedIdentityToken != "" {
			if info.DeleteIdentityToken != observedIdentityToken {
				return &DeleteIdentityChangedError{Path: name}
			}
		}
		entry := FileInfo{
			Path:                filePath,
			Name:                info.Name,
			IsDir:               info.IsDir,
			Mode:                info.Mode,
			Size:                info.Size,
			ModTime:             info.ModTime,
			DeleteIdentityToken: info.DeleteIdentityToken,
		}
		if !info.IsDir {
			if policy != nil {
				entry.Versioned = policy.ShouldVersion(ctx, filePath, info.Size)
			}
			if options.IncludeContentHash {
				contentHash, err := fs.deleteTargetFileHash(ctx, filePath)
				if err != nil {
					return err
				}
				entry.ContentHash = contentHash
			}
		}
		return visit(entry)
	}

	if options.IncludeDescendants {
		if err := walkStorageDeleteTree(ctx, fs.workspace, name, visitWorkspaceEntry); err != nil {
			return mapWorkspaceReadablePathError(err)
		}
	} else {
		err := walkStorageDeleteTree(ctx, fs.workspace, name, func(filePath string, info *workspace.FileInfo) error {
			if err := visitWorkspaceEntry(filePath, info); err != nil {
				return err
			}
			return errDeleteSnapshotRootComplete
		})
		if err != nil && !errors.Is(err, errDeleteSnapshotRootComplete) {
			return mapWorkspaceReadablePathError(err)
		}
	}
	return nil
}

func (fs *FileSystem) deleteTargetFileHash(ctx context.Context, name string) (string, error) {
	if fs.hashDeleteTargetFile != nil {
		return fs.hashDeleteTargetFile(ctx, name)
	}
	return fs.hashWorkspaceFile(ctx, name)
}

func normalizeDeleteIntentPaths(names []string) ([]string, error) {
	if len(names) == 0 || len(names) > MaxDeleteIntentTargets {
		return nil, ErrInvalidDeleteIntent
	}
	normalized := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" {
			return nil, ErrInvalidDeleteIntent
		}
		cleanName, err := normalizeStorageWorkspacePath(name)
		if err != nil || rejectStorageRootMutation(cleanName) != nil {
			return nil, ErrInvalidDeleteIntent
		}
		if _, ok := seen[cleanName]; ok {
			return nil, ErrInvalidDeleteIntent
		}
		for _, existing := range normalized {
			if pathMatchesOrDescendant(existing, cleanName) || pathMatchesOrDescendant(cleanName, existing) {
				return nil, ErrInvalidDeleteIntent
			}
		}
		seen[cleanName] = struct{}{}
		normalized = append(normalized, cleanName)
	}
	return normalized, nil
}

func normalizeObservedDeleteIntentTargets(targets []ObservedDeleteTarget) ([]ObservedDeleteTarget, error) {
	if len(targets) == 0 || len(targets) > MaxDeleteIntentTargets {
		return nil, ErrInvalidDeleteIntent
	}
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		if !validDeleteIdentityToken(target.ObservedIdentityToken) {
			return nil, ErrInvalidDeleteIntent
		}
		names = append(names, target.Path)
	}
	normalizedNames, err := normalizeDeleteIntentPaths(names)
	if err != nil {
		return nil, err
	}
	normalized := make([]ObservedDeleteTarget, 0, len(targets))
	for i, target := range targets {
		target.Path = normalizedNames[i]
		normalized = append(normalized, target)
	}
	return normalized, nil
}

func validDeleteIdentityToken(token string) bool {
	if len(token) != sha256.Size*2 {
		return false
	}
	for _, character := range token {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (fs *FileSystem) authorizeDeleteTreeLocked(ctx context.Context, name string, authorize DeletePathAuthorizer) error {
	mountBoundary := fs.captureDeleteMountBoundary(fs.workspace.Root())
	err := walkStorageDeleteTree(ctx, fs.workspace, name, func(filePath string, _ *workspace.FileInfo) error {
		if authorize != nil {
			if err := authorize(filePath); err != nil {
				return err
			}
		}
		return mountBoundary.checkWorkspacePath(filePath)
	})
	return mapWorkspaceReadablePathError(err)
}

func (fs *FileSystem) ensureTrashCapacityLocked(ctx context.Context, incomingSize int64, protectedIDs ...string) error {
	if err := fs.checkTrashMutationAllowedLocked(); err != nil {
		return err
	}
	if fs.config == nil || fs.config.MaxTrashSize <= 0 {
		return nil
	}
	protected := make(map[string]struct{}, len(protectedIDs))
	for _, protectedID := range protectedIDs {
		if protectedID == "" {
			continue
		}
		protected[protectedID] = struct{}{}
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
		if _, skip := protected[item.ID]; skip {
			continue
		}
		if _, err := fs.permanentlyDeleteTrashItem(ctx, &item); err != nil {
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
	if err := rejectStorageRootMutation(name); err != nil {
		return err
	}
	snapshot, err := fs.deleteTargetSnapshotLocked(ctx, name, DeleteTargetSnapshotOptions{
		IncludeDescendants: true,
		IncludeContentHash: true,
	}, nil)
	if err != nil {
		return err
	}
	return fs.deletePreparedWithPolicyLocked(ctx, name, DeletePolicy{Mode: DeleteModePermanent}, snapshot)
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
	if err := rejectStorageRootMutation(oldName); err != nil {
		return err
	}
	newName, err = normalizeStorageWorkspacePath(newName)
	if err != nil {
		return err
	}
	if err := rejectStorageRootMutation(newName); err != nil {
		return err
	}
	sourceInfo, err := fs.workspace.Stat(ctx, oldName)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return ErrNotFound
		}
		if errors.Is(err, workspace.ErrNotDir) {
			return ErrNotDir
		}
		return err
	}
	targetVersionMetadata, err := fs.versionMetadataPathExists(ctx, newName, sourceInfo.IsDir)
	if err != nil {
		return err
	}
	if targetVersionMetadata {
		return ErrAlreadyExists
	}

	renameWorkspacePath := fs.renameWorkspacePath
	if renameWorkspacePath == nil {
		renameWorkspacePath = fs.workspace.Rename
	}
	var renameWarning error
	err = renameWorkspacePath(ctx, oldName, newName)
	if err != nil {
		if workspace.IsVisibleMutationWarning(err) || isVisibleMutationWarning(err) {
			renameWarning = err
		} else {
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
	}

	if err := fs.renameMetadataPath(ctx, oldName, newName); err != nil {
		if rollbackErr := renameWorkspacePath(ctx, newName, oldName); rollbackErr != nil {
			errs := []error{fmt.Errorf("failed to rename metadata: %w", err)}
			if renameWarning != nil {
				errs = append(errs, fmt.Errorf("workspace rename warning: %w", renameWarning))
			}
			errs = append(errs, fmt.Errorf("failed to rollback workspace rename: %w", rollbackErr))
			return errors.Join(errs...)
		}
		return fmt.Errorf("failed to rename metadata: %w", err)
	}
	if err := fs.notifyPathRenamed(ctx, oldName, newName); err != nil {
		if workspace.IsVisibleMutationWarning(err) || isVisibleMutationWarning(err) {
			renameWarning = errors.Join(renameWarning, fmt.Errorf("failed to sync rename hooks: %w", err))
		} else {
			rollbackWorkspaceErr := renameWorkspacePath(ctx, newName, oldName)
			rollbackMetadataErr := fs.renameMetadataPath(ctx, newName, oldName)
			if rollbackWorkspaceErr != nil || rollbackMetadataErr != nil {
				errs := []error{fmt.Errorf("failed to sync rename hooks: %w", err)}
				if renameWarning != nil {
					errs = append(errs, fmt.Errorf("workspace rename warning: %w", renameWarning))
				}
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
	}
	if renameWarning != nil {
		return wrapVisibleMutationWarning(renameWarning)
	}

	return nil
}

// Copy copies a file without overwriting an existing destination.
func (fs *FileSystem) Copy(ctx context.Context, srcName, dstName string) error {
	release := fs.beginMutation()
	defer release()

	var err error
	srcName, err = normalizeStorageWorkspacePath(srcName)
	if err != nil {
		return err
	}
	if err := rejectStorageRootMutation(srcName); err != nil {
		return err
	}
	dstName, err = normalizeStorageWorkspacePath(dstName)
	if err != nil {
		return err
	}
	if err := rejectStorageRootMutation(dstName); err != nil {
		return err
	}

	copyWorkspacePath := fs.copyWorkspacePath
	if copyWorkspacePath == nil {
		copyWorkspacePath = fs.workspace.Copy
	}
	var copyWarning error
	if err := copyWorkspacePath(ctx, srcName, dstName); err != nil {
		if workspace.IsVisibleMutationWarning(err) {
			copyWarning = err
		} else {
			if errors.Is(err, workspace.ErrAlreadyExists) {
				return ErrAlreadyExists
			}
			if errors.Is(err, workspace.ErrNotDir) {
				return ErrNotDir
			}
			if errors.Is(err, workspace.ErrNotFound) {
				return ErrNotFound
			}
			if errors.Is(err, workspace.ErrIsDir) {
				return ErrIsDir
			}
			return err
		}
	}

	if err := fs.syncFileIndexFromWorkspace(ctx, dstName); err != nil {
		rollbackDeleteErr := fs.workspace.Delete(ctx, dstName)
		rollbackIndexErr := fs.deleteFileIndex(ctx, dstName)
		if errors.Is(rollbackIndexErr, versionstore.ErrNotFound) {
			rollbackIndexErr = nil
		}
		if rollbackDeleteErr != nil || rollbackIndexErr != nil {
			errList := []error{fmt.Errorf("failed to update file index: %w", err)}
			if copyWarning != nil {
				errList = append(errList, fmt.Errorf("workspace copy warning: %w", copyWarning))
			}
			if rollbackDeleteErr != nil {
				errList = append(errList, fmt.Errorf("failed to rollback copied file: %w", rollbackDeleteErr))
			}
			if rollbackIndexErr != nil {
				errList = append(errList, fmt.Errorf("failed to rollback copied file index: %w", rollbackIndexErr))
			}
			return errors.Join(errList...)
		}
		return fmt.Errorf("failed to update file index: %w", err)
	}
	if copyWarning != nil {
		return wrapVisibleMutationWarning(copyWarning)
	}

	return nil
}

func (fs *FileSystem) notifyPathDeleted(ctx context.Context, name string) (*PathDeleteHookResult, error) {
	fs.hookMu.RLock()
	hook := fs.onPathDeleted
	fs.hookMu.RUnlock()
	if hook != nil {
		return hook(ctx, name)
	}
	return nil, nil
}

func (fs *FileSystem) notifyPathRenamed(ctx context.Context, oldName, newName string) error {
	fs.hookMu.RLock()
	hook := fs.onPathRenamed
	fs.hookMu.RUnlock()
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
	currentHash, err := fs.hashWorkspaceFile(ctx, name)
	if err != nil {
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
	if errors.Is(err, workspace.ErrNotRegular) {
		return ErrNotRegular
	}
	return err
}

func mapWorkspaceWritablePathError(err error) error {
	if errors.Is(err, workspace.ErrFileTooLarge) {
		return fmt.Errorf("%w (max: %d bytes)", ErrFileTooLarge, defaultMaxWriteSize)
	}
	return mapWorkspaceReadablePathError(err)
}

func unwrapWorkspaceVisibleMutationError(err error) error {
	var warningErr *workspace.VisibleMutationWarningError
	if errors.As(err, &warningErr) {
		return warningErr.Unwrap()
	}
	return err
}

type readSeekNopCloser struct {
	*bytes.Reader
}

func (r readSeekNopCloser) Close() error {
	return nil
}

// GetVersion reads a specific version of a file
func (fs *FileSystem) GetVersion(ctx context.Context, name, hash string) (io.ReadSeekCloser, error) {
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
	currentHash, err := fs.hashWorkspaceFile(ctx, name)
	if err != nil {
		return nil, err
	}
	if currentHash == hash {
		f, err := fs.workspace.OpenFile(ctx, name)
		if err != nil {
			return nil, mapWorkspaceReadablePathError(err)
		}
		return f, nil
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

	return readSeekNopCloser{Reader: bytes.NewReader(data)}, nil
}

func (fs *FileSystem) hashWorkspaceFile(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	reader, err := fs.workspace.OpenRegularFile(ctx, name)
	if err != nil {
		return "", mapWorkspaceReadablePathError(err)
	}
	defer reader.Close()

	return hashOpenWorkspaceFileContext(ctx, reader)
}

func hashOpenWorkspaceFile(reader *os.File) (string, error) {
	return hashOpenWorkspaceFileContext(context.Background(), reader)
}

func hashOpenWorkspaceFileContext(ctx context.Context, reader *os.File) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return "", err
	}

	hash, err := hashReaderWithContext(ctx, reader)
	if err != nil {
		return "", err
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return "", err
	}

	return hash, nil
}

func hashReaderWithContext(ctx context.Context, reader io.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	hasher := blake3.New()
	if _, err := io.Copy(hasher, contextCheckingReader{ctx: ctx, reader: reader}); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

type contextCheckingReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextCheckingReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
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
	if err := rejectStorageRootMutation(name); err != nil {
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
	var restoreWriteWarning error

	// Save current as a version first
	if hadPreviousFile {
		currentHash := computeHash(previousData)
		if currentHash != hash {
			hasObject, err := fs.hasVersionObject(ctx, currentHash)
			if err != nil {
				return fmt.Errorf("failed to check current version object before restore: %w", err)
			}
			rollbackObjectCreated := !hasObject
			_, versionErr := fs.versions.GetVersion(ctx, name, currentHash)
			versionAlreadyRecorded := versionErr == nil
			if versionErr != nil && !errors.Is(versionErr, versionstore.ErrNotFound) {
				return fmt.Errorf("failed to check current version before restore: %w", versionErr)
			}
			storedHash, err := fs.putVersionObject(ctx, previousData)
			if err != nil {
				return fmt.Errorf("failed to store current version before restore: %w", err)
			}
			rollbackVersionHash = storedHash
			rollbackVersionObjectCreated = rollbackObjectCreated
			if !versionAlreadyRecorded {
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
	}

	// Write restored version
	writeWorkspacePath := fs.writeWorkspacePath
	if writeWorkspacePath == nil {
		writeWorkspacePath = fs.writeWorkspaceFile
	}
	if err := writeWorkspacePath(ctx, name, data); err != nil {
		if workspace.IsVisibleMutationWarning(err) || isVisibleMutationWarning(err) {
			restoreWriteWarning = wrapVisibleMutationWarning(err)
		} else {
			if rollbackErr := fs.rollbackWriteVersion(ctx, name, rollbackVersionHash, rollbackVersionRecorded, rollbackVersionObjectCreated); rollbackErr != nil {
				return errors.Join(
					err,
					fmt.Errorf("failed to rollback current snapshot version: %w", rollbackErr),
				)
			}
			return err
		}
	}

	if err := fs.updateFileIndex(ctx, name, int64(len(data)), time.Now(), computeHash(data)); err != nil {
		rollbackErr := fs.restoreFileAfterIndexFailure(ctx, name, hadPreviousFile, previousData, nil)
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
	if restoreWriteWarning != nil {
		return restoreWriteWarning
	}
	return nil
}

// SetVersioning sets the versioning override for a file
func (fs *FileSystem) SetVersioning(ctx context.Context, name string, enabled bool) error {
	release := fs.beginMutation()
	defer release()

	var err error
	name, err = normalizeStorageWorkspacePath(name)
	if err != nil {
		return err
	}
	if err := rejectStorageRootMutation(name); err != nil {
		return err
	}
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
	if info.IsDir {
		return ErrIsDir
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

	policy := fs.currentVersioningPolicy()
	if policy == nil {
		return false, "not_versioned_type", nil
	}
	enabled, reason = policy.GetVersioningStatus(ctx, name, info.Size)
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
			ExpiresAt:    item.ExpiresAt,
			IsDir:        item.IsDir,
			HadVersions:  item.HadVersions,
			RestoreData:  item.RestoreData,
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
		ExpiresAt:    item.ExpiresAt,
		IsDir:        item.IsDir,
		HadVersions:  item.HadVersions,
		RestoreData:  item.RestoreData,
	}, nil
}

// WalkTrashItemRestorePaths walks the workspace paths that a trash item would
// restore to at its original location.
func (fs *FileSystem) WalkTrashItemRestorePaths(ctx context.Context, id string, fn func(restoredPath string, isDir bool, size int64) error) error {
	if fn == nil {
		return nil
	}

	fs.mu.RLock()
	defer fs.mu.RUnlock()

	item, err := fs.versions.GetTrashItem(ctx, id)
	if err != nil {
		if errors.Is(err, versionstore.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return fs.walkTrashItemRestorePathsLocked(ctx, item, fn)
}

func (fs *FileSystem) walkTrashItemRestorePathsLocked(ctx context.Context, item *versionstore.TrashItem, fn func(restoredPath string, isDir bool, size int64) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	contentRel := filepath.Join(item.ID, "content")
	info, err := fs.trashRootHandle.Lstat(contentRel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return mapStorageRootPathError(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errStoragePathSymlink
	}

	mountBoundary := fs.captureDeleteMountBoundary(fs.trashRoot)
	originalRoot := path.Clean(item.OriginalPath)
	if err := fn(originalRoot, info.IsDir(), info.Size()); err != nil {
		return err
	}
	if err := mountBoundary.checkHostPath(filepath.Join(fs.trashRoot, contentRel)); err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}

	return fs.walkTrashContentRestorePaths(ctx, mountBoundary, contentRel, contentRel, originalRoot, fn)
}

func (fs *FileSystem) walkTrashContentRestorePaths(ctx context.Context, mountBoundary deleteMountBoundary, contentRootRel, currentRel, originalRoot string, fn func(restoredPath string, isDir bool, size int64) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	dirHandle, err := rootio.OpenDirNoFollow(fs.trashRootHandle, currentRel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return mapStorageRootPathError(err)
	}
	defer dirHandle.Close()

	entries, err := dirHandle.ReadDir(-1)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		entryName, err := safeStorageReadDirFallbackChildName(entry.Name())
		if err != nil {
			return err
		}
		childRel := filepath.Join(currentRel, entryName)
		info, err := fs.trashRootHandle.Lstat(childRel)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ErrNotFound
			}
			return mapStorageRootPathError(err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errStoragePathSymlink
		}

		relativePath, err := filepath.Rel(contentRootRel, childRel)
		if err != nil {
			return err
		}
		restoredPath := path.Clean(path.Join(originalRoot, filepath.ToSlash(relativePath)))
		if err := fn(restoredPath, info.IsDir(), info.Size()); err != nil {
			return err
		}
		if err := mountBoundary.checkHostPath(filepath.Join(fs.trashRoot, childRel)); err != nil {
			return err
		}
		if info.IsDir() {
			if err := fs.walkTrashContentRestorePaths(ctx, mountBoundary, contentRootRel, childRel, originalRoot, fn); err != nil {
				return err
			}
		}
	}

	return nil
}

// RestoreFromTrash restores a file from trash
func (fs *FileSystem) RestoreFromTrash(ctx context.Context, id string) error {
	release := fs.beginMutation()
	defer release()
	if err := fs.checkTrashMutationAllowedLocked(); err != nil {
		return err
	}

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
	trashItemInfo, err := fs.trashRootHandle.Lstat(filepath.FromSlash(id))
	if err != nil || !trashItemInfo.IsDir() {
		return fmt.Errorf("failed to identify trash item directory: %w", errors.Join(mapStorageRootPathError(err), ErrDeleteTargetChanged))
	}

	var restoreWarning error
	if err := fs.movePath(trashContentPath, destPath); err != nil {
		if isVisibleMutationWarning(err) {
			restoreWarning = errors.Join(restoreWarning, fmt.Errorf("restore trash content: %w", err))
		} else {
			if isPathNotDirError(err) {
				return ErrNotDir
			}
			return fmt.Errorf("failed to restore from trash: %w", err)
		}
	}

	// Remove from trash database
	if err := fs.versions.RemoveFromTrash(ctx, id); err != nil {
		if rollbackErr := fs.movePath(destPath, trashContentPath); rollbackErr != nil {
			errs := []error{fmt.Errorf("failed to remove trash metadata: %w", err)}
			if restoreWarning != nil {
				errs = append(errs, fmt.Errorf("restore warning: %w", restoreWarning))
			}
			errs = append(errs, fmt.Errorf("failed to rollback restored content: %w", rollbackErr))
			return errors.Join(errs...)
		}
		return fmt.Errorf("failed to remove trash metadata: %w", err)
	}
	fs.cleanupTrashItemDir(path.Join(fs.trashRoot, id), trashItemInfo)
	if err := fs.syncRestoredIndexEntries(ctx, item.OriginalPath, item.IsDir); err != nil {
		rollbackErr := fs.movePath(destPath, trashContentPath)
		metadataErr := fs.versions.AddToTrash(ctx, item)
		indexRollbackErr := fs.deleteFileIndexPrefix(ctx, item.OriginalPath)
		var errs []error
		errs = append(errs, fmt.Errorf("failed to update file index: %w", err))
		if restoreWarning != nil && (rollbackErr != nil || metadataErr != nil || indexRollbackErr != nil) {
			errs = append(errs, fmt.Errorf("restore warning: %w", restoreWarning))
		}
		if rollbackErr != nil {
			errs = append(errs, fmt.Errorf("failed to rollback restored content: %w", rollbackErr))
		}
		if metadataErr != nil {
			errs = append(errs, fmt.Errorf("failed to restore trash metadata: %w", metadataErr))
		}
		if indexRollbackErr != nil {
			errs = append(errs, fmt.Errorf("failed to rollback restored file index: %w", indexRollbackErr))
		}
		return errors.Join(errs...)
	}
	if restoreWarning != nil {
		return wrapVisibleMutationWarning(restoreWarning)
	}

	return nil
}

// RestoreFromTrashTo restores a file from trash to a custom location
func (fs *FileSystem) RestoreFromTrashTo(ctx context.Context, id, newPath string) error {
	release := fs.beginMutation()
	defer release()
	if err := fs.checkTrashMutationAllowedLocked(); err != nil {
		return err
	}

	var err error
	newPath, err = normalizeStorageWorkspacePath(newPath)
	if err != nil {
		return err
	}
	if err := rejectStorageRootMutation(newPath); err != nil {
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
	if item.HadVersions && newPath != item.OriginalPath {
		originalExists, err := fs.workspacePathExists(ctx, item.OriginalPath)
		if err != nil {
			return err
		}
		if originalExists {
			return fmt.Errorf("cannot restore %s to a custom path while its original path still has version metadata: %w", item.OriginalPath, ErrAlreadyExists)
		}
		sharedMetadata, err := fs.hasOtherTrashItemReferencingRestoredMetadata(ctx, item.OriginalPath, item.IsDir, id)
		if err != nil {
			return err
		}
		if sharedMetadata {
			return fmt.Errorf("cannot restore %s to a custom path while another trash item still references its version metadata: %w", item.OriginalPath, ErrAlreadyExists)
		}
		targetMetadata, err := fs.hasOtherTrashItemReferencingRestoredMetadata(ctx, newPath, item.IsDir, id)
		if err != nil {
			return err
		}
		if targetMetadata {
			return fmt.Errorf("cannot restore %s to custom path %s while the target path still has version metadata: %w", item.OriginalPath, newPath, ErrAlreadyExists)
		}
		targetVersionMetadata, err := fs.versionMetadataPathExists(ctx, newPath, item.IsDir)
		if err != nil {
			return err
		}
		if targetVersionMetadata {
			return fmt.Errorf("cannot restore %s to custom path %s while the target path has version metadata: %w", item.OriginalPath, newPath, ErrAlreadyExists)
		}
	}

	// Check if target path already exists
	if _, err := fs.workspace.Stat(ctx, newPath); err == nil {
		return ErrAlreadyExists
	}

	// Move from trash
	trashContentPath := path.Join(fs.trashRoot, id, "content")
	destPath := fs.workspace.FullPath(newPath)
	trashItemInfo, err := fs.trashRootHandle.Lstat(filepath.FromSlash(id))
	if err != nil || !trashItemInfo.IsDir() {
		return fmt.Errorf("failed to identify trash item directory: %w", errors.Join(mapStorageRootPathError(err), ErrDeleteTargetChanged))
	}

	var restoreWarning error
	if err := fs.movePath(trashContentPath, destPath); err != nil {
		if isVisibleMutationWarning(err) {
			restoreWarning = errors.Join(restoreWarning, fmt.Errorf("restore trash content: %w", err))
		} else {
			if isPathNotDirError(err) {
				return ErrNotDir
			}
			return fmt.Errorf("failed to restore from trash: %w", err)
		}
	}

	if err := fs.removeTrashMetadata(ctx, id); err != nil {
		var rollbackErrs []error
		if rollbackErr := fs.movePath(destPath, trashContentPath); rollbackErr != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to rollback restored content: %w", rollbackErr))
		}
		if len(rollbackErrs) > 0 {
			errs := []error{fmt.Errorf("failed to remove trash metadata: %w", err)}
			if restoreWarning != nil {
				errs = append(errs, fmt.Errorf("restore warning: %w", restoreWarning))
			}
			return errors.Join(append(errs, rollbackErrs...)...)
		}
		return fmt.Errorf("failed to remove trash metadata: %w", err)
	}

	fs.cleanupTrashItemDir(path.Join(fs.trashRoot, id), trashItemInfo)
	if err := fs.syncRestoredIndexEntries(ctx, newPath, item.IsDir); err != nil {
		var rollbackErrs []error
		if rollbackErr := fs.movePath(destPath, trashContentPath); rollbackErr != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to rollback restored content: %w", rollbackErr))
		}
		if metadataErr := fs.addTrashMetadata(ctx, item); metadataErr != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to restore trash metadata: %w", metadataErr))
		}
		if cleanupErr := fs.deleteFileIndexPrefix(ctx, newPath); cleanupErr != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to rollback restored file index: %w", cleanupErr))
		}
		if len(rollbackErrs) > 0 {
			errs := []error{fmt.Errorf("failed to update file index: %w", err)}
			if restoreWarning != nil {
				errs = append(errs, fmt.Errorf("restore warning: %w", restoreWarning))
			}
			return errors.Join(append(errs, rollbackErrs...)...)
		}
		return fmt.Errorf("failed to update file index: %w", err)
	}

	if item.HadVersions && newPath != item.OriginalPath {
		if err := fs.renameHistoryMetadataPath(ctx, item.OriginalPath, newPath); err != nil {
			var rollbackErrs []error
			if rollbackErr := fs.movePath(destPath, trashContentPath); rollbackErr != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to rollback restored content: %w", rollbackErr))
			}
			if metadataErr := fs.addTrashMetadata(ctx, item); metadataErr != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to restore trash metadata: %w", metadataErr))
			}
			if cleanupErr := fs.deleteFileIndexPrefix(ctx, newPath); cleanupErr != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to rollback restored file index: %w", cleanupErr))
			}
			if len(rollbackErrs) > 0 {
				errs := []error{fmt.Errorf("failed to update version metadata: %w", err)}
				if restoreWarning != nil {
					errs = append(errs, fmt.Errorf("restore warning: %w", restoreWarning))
				}
				return errors.Join(append(errs, rollbackErrs...)...)
			}
			return fmt.Errorf("failed to update version metadata: %w", err)
		}
	}
	if restoreWarning != nil {
		return wrapVisibleMutationWarning(restoreWarning)
	}

	return nil
}

func (fs *FileSystem) hasOtherTrashItemWithOriginalPath(ctx context.Context, originalPath, excludedID string) (bool, error) {
	items, err := fs.versions.ListTrash(ctx)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if item.ID == excludedID {
			continue
		}
		if !item.HadVersions {
			continue
		}
		if item.OriginalPath == originalPath {
			return true, nil
		}
	}
	return false, nil
}

func (fs *FileSystem) hasOtherTrashItemReferencingRestoredMetadata(ctx context.Context, originalPath string, isDir bool, excludedID string) (bool, error) {
	items, err := fs.versions.ListTrash(ctx)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if item.ID == excludedID {
			continue
		}
		if !item.HadVersions {
			continue
		}
		if item.OriginalPath == originalPath {
			return true, nil
		}
		if isDir && pathMatchesOrDescendant(originalPath, item.OriginalPath) {
			return true, nil
		}
	}
	return false, nil
}

// HasVersionMetadataPath reports whether historical version metadata exists at
// the path, optionally including descendants for directory operations.
func (fs *FileSystem) HasVersionMetadataPath(ctx context.Context, name string, includeDescendants bool) (bool, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.versionMetadataPathExists(ctx, name, includeDescendants)
}

func (fs *FileSystem) versionMetadataPathExists(ctx context.Context, name string, includeDescendants bool) (bool, error) {
	name, err := normalizeStorageWorkspacePath(name)
	if err != nil {
		return false, err
	}
	paths, err := fs.listVersionPaths(ctx)
	if err != nil {
		return false, err
	}
	for _, versionPath := range paths {
		if versionPath == name {
			return true, nil
		}
		if includeDescendants && pathMatchesOrDescendant(name, versionPath) {
			return true, nil
		}
	}
	return false, nil
}

func (fs *FileSystem) versionPathReferencedOutsideTrashItem(ctx context.Context, versionPath, excludedID string) (bool, error) {
	livePathExists, err := fs.workspacePathExists(ctx, versionPath)
	if err != nil {
		return false, err
	}
	if livePathExists {
		return true, nil
	}

	items, err := fs.versions.ListTrash(ctx)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if item.ID == excludedID {
			continue
		}
		if !item.HadVersions {
			continue
		}
		if pathMatchesOrDescendant(item.OriginalPath, versionPath) {
			return true, nil
		}
	}
	return false, nil
}

func (fs *FileSystem) workspacePathExists(ctx context.Context, targetPath string) (bool, error) {
	_, err := fs.workspace.Stat(ctx, targetPath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrNotDir) {
		return false, nil
	}
	return false, err
}

// DeleteFromTrash permanently deletes an item from trash
func (fs *FileSystem) DeleteFromTrash(ctx context.Context, id string) error {
	release := fs.beginMutation()
	defer release()
	if err := fs.checkTrashMutationAllowedLocked(); err != nil {
		return err
	}

	item, err := fs.versions.GetTrashItem(ctx, id)
	if err != nil {
		if errors.Is(err, versionstore.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	visibleDeleted, err := fs.permanentlyDeleteTrashItem(ctx, item)
	if err != nil && visibleDeleted {
		return wrapTrashDeleteWarning(err)
	}
	return err
}

// EmptyTrashSelection permanently deletes exactly the requested trash items.
func (fs *FileSystem) EmptyTrashSelection(ctx context.Context, ids []string, authorize DeletePathAuthorizer) (TrashSelectionResult, error) {
	release := fs.beginMutation()
	defer release()

	var result TrashSelectionResult
	if err := fs.checkTrashMutationAllowedLocked(); err != nil {
		return result, err
	}
	if len(ids) == 0 || len(ids) > MaxTrashSelectionIDs {
		return result, ErrInvalidTrashSelection
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if !validTrashSelectionID(id) {
			return result, ErrInvalidTrashSelection
		}
		if _, exists := seen[id]; exists {
			return result, ErrInvalidTrashSelection
		}
		seen[id] = struct{}{}
	}

	items, err := fs.versions.ListTrash(ctx)
	if err != nil {
		return result, err
	}
	itemsByID := make(map[string]*versionstore.TrashItem, len(items))
	for i := range items {
		itemsByID[items[i].ID] = &items[i]
	}

	partitionUndeleted := func() TrashSelectionResult {
		partition := TrashSelectionResult{}
		for _, id := range ids {
			if _, exists := itemsByID[id]; exists {
				partition.RemainingIDs = append(partition.RemainingIDs, id)
			} else {
				partition.SkippedIDs = append(partition.SkippedIDs, id)
			}
		}
		return partition
	}

	for _, id := range ids {
		item, exists := itemsByID[id]
		if !exists {
			continue
		}
		if err := ctx.Err(); err != nil {
			return partitionUndeleted(), err
		}
		rootCallback := true
		if err := fs.walkTrashItemRestorePathsLocked(ctx, item, func(restoredPath string, _ bool, _ int64) error {
			if authorize == nil {
				return nil
			}
			err := authorize(restoredPath)
			if rootCallback {
				rootCallback = false
				if err != nil {
					return &TrashRootAuthorizationError{Path: restoredPath, err: err}
				}
			}
			return err
		}); err != nil {
			return partitionUndeleted(), err
		}
	}

	var warningErr error
	var hardFailure error
	for _, id := range ids {
		item, exists := itemsByID[id]
		if !exists {
			result.SkippedIDs = append(result.SkippedIDs, id)
			continue
		}
		if hardFailure != nil {
			result.RemainingIDs = append(result.RemainingIDs, id)
			continue
		}
		if err := ctx.Err(); err != nil {
			hardFailure = err
			result.RemainingIDs = append(result.RemainingIDs, id)
			continue
		}
		visibleDeleted, err := fs.permanentlyDeleteTrashItem(ctx, item)
		if err != nil {
			if visibleDeleted {
				result.DeletedIDs = append(result.DeletedIDs, id)
				warningErr = errors.Join(warningErr, err)
				continue
			}
			hardFailure = err
			result.RemainingIDs = append(result.RemainingIDs, id)
			continue
		}
		result.DeletedIDs = append(result.DeletedIDs, id)
	}

	if hardFailure != nil {
		if warningErr != nil {
			return result, wrapTrashDeletePartialWarning(errors.Join(warningErr, hardFailure))
		}
		return result, hardFailure
	}
	if warningErr != nil {
		return result, wrapTrashDeleteWarning(warningErr)
	}
	return result, nil
}

func validTrashSelectionID(id string) bool {
	if len(id) == 0 || len(id) > MaxTrashSelectionIDLength {
		return false
	}
	for i := 0; i < len(id); i++ {
		character := id[i]
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '_' && character != '-' {
			return false
		}
	}
	return true
}

// CleanupExpiredTrash removes expired trash items
func (fs *FileSystem) CleanupExpiredTrash(ctx context.Context) (int, error) {
	release := fs.beginMutation()
	defer release()

	return fs.cleanupExpiredTrashLocked(ctx)
}

func (fs *FileSystem) cleanupExpiredTrashLocked(ctx context.Context) (int, error) {
	if err := fs.checkTrashMutationAllowedLocked(); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	// Snapshot the expiration candidates before beginning journaled mutations.
	items, err := fs.versions.ListTrash(ctx)
	if err != nil {
		return 0, err
	}

	now := time.Now()
	deleted := 0
	var warningErr error
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			if warningErr != nil {
				return deleted, wrapTrashDeletePartialWarning(errors.Join(warningErr, err))
			}
			return deleted, err
		}
		if !item.ExpiresAt.Before(now) {
			continue
		}
		visibleDeleted, err := fs.permanentlyDeleteTrashItem(ctx, &item)
		if err != nil {
			if visibleDeleted {
				deleted++
				warningErr = errors.Join(warningErr, err)
				continue
			}
			if warningErr != nil {
				return deleted, wrapTrashDeletePartialWarning(errors.Join(warningErr, err))
			}
			return deleted, err
		}
		deleted++
	}

	if warningErr != nil {
		return deleted, wrapTrashDeleteWarning(warningErr)
	}
	return deleted, nil
}

func (fs *FileSystem) permanentlyDeleteTrashItem(ctx context.Context, item *versionstore.TrashItem) (bool, error) {
	if err := fs.checkTrashMutationAllowedLocked(); err != nil {
		return false, err
	}
	operation, err := fs.prepareTrashPurge(ctx, item)
	if err != nil {
		return false, err
	}
	committed, err := fs.executeTrashPurge(ctx, operation)
	if err != nil {
		return committed, err
	}
	if err := fs.finishTrashPurge(ctx, operation); err != nil {
		return true, err
	}
	return true, nil
}

func (fs *FileSystem) cleanupDeletedTrashVersionMetadata(ctx context.Context, item *versionstore.TrashItem) error {
	versionPaths := []string{item.OriginalPath}
	if item.IsDir {
		paths, err := fs.listVersionPaths(ctx)
		if err != nil {
			return fmt.Errorf("failed to list version metadata paths for trash item: %w", err)
		}
		versionPaths = versionPaths[:0]
		for _, versionPath := range paths {
			if pathMatchesOrDescendant(item.OriginalPath, versionPath) {
				versionPaths = append(versionPaths, versionPath)
			}
		}
	}

	retainedVersionPaths := make(map[string]struct{})
	for _, versionPath := range versionPaths {
		retained, err := fs.versionPathReferencedOutsideTrashItem(ctx, versionPath, item.ID)
		if err != nil {
			return fmt.Errorf("failed to check version metadata ownership for trash item: %w", err)
		}
		if retained {
			retainedVersionPaths[versionPath] = struct{}{}
			continue
		}
	}

	for _, versionPath := range versionPaths {
		if _, retained := retainedVersionPaths[versionPath]; retained {
			continue
		}
		if err := fs.versions.DeleteVersions(ctx, versionPath); err != nil {
			return fmt.Errorf("failed to delete version metadata for trash item: %w", err)
		}
	}
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

// TrashDeleteWarningError reports that a trash deletion became externally
// visible, but a follow-up cleanup step still failed.
type TrashDeleteWarningError struct {
	err     error
	partial bool
}

func (e *TrashDeleteWarningError) Error() string {
	return e.err.Error()
}

func (e *TrashDeleteWarningError) Unwrap() error {
	return e.err
}

func (e *TrashDeleteWarningError) Partial() bool {
	return e != nil && e.partial
}

func wrapTrashDeleteWarning(err error) error {
	return wrapTrashDeleteWarningWithPartial(err, false)
}

func wrapTrashDeletePartialWarning(err error) error {
	return wrapTrashDeleteWarningWithPartial(err, true)
}

func wrapTrashDeleteWarningWithPartial(err error, partial bool) error {
	if err == nil {
		return nil
	}
	var warningErr *TrashDeleteWarningError
	if errors.As(err, &warningErr) {
		if partial && !warningErr.partial {
			return &TrashDeleteWarningError{err: err, partial: true}
		}
		return err
	}
	return &TrashDeleteWarningError{err: err, partial: partial}
}

// DeleteCleanupWarningError reports that a delete operation already became
// externally visible, but follow-up cleanup still failed.
type DeleteCleanupWarningError struct {
	err error
}

func (e *DeleteCleanupWarningError) Error() string {
	return e.err.Error()
}

func (e *DeleteCleanupWarningError) Unwrap() error {
	return e.err
}

func wrapDeleteCleanupWarning(err error) error {
	if err == nil {
		return nil
	}
	var warningErr *DeleteCleanupWarningError
	if errors.As(err, &warningErr) {
		return err
	}
	return &DeleteCleanupWarningError{err: err}
}

// GetTrashStats returns trash statistics
func (fs *FileSystem) GetTrashStats(ctx context.Context) (count int, totalSize int64, err error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	return fs.versions.GetTrashStats(ctx)
}

// GetFileCount returns the number of current workspace files.
func (fs *FileSystem) GetFileCount(ctx context.Context) (int, error) {
	fs.mu.RLock()
	workspaceRef := fs.workspace
	fs.mu.RUnlock()

	count := 0
	err := walkStorageWorkspace(ctx, workspaceRef, "/", func(_ string, info *workspace.FileInfo) error {
		if info == nil || info.IsDir {
			return nil
		}
		count++
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// DiskStats returns capacity for the filesystem hosting the workspace.
func (fs *FileSystem) DiskStats() (*DiskStats, error) {
	fs.mu.RLock()
	workspaceRef := fs.workspace
	fs.mu.RUnlock()
	if workspaceRef == nil {
		return nil, errors.New("workspace not initialized")
	}

	return diskStatsForPath(workspaceRef.Root())
}

func diskStatsForPath(root string) (*DiskStats, error) {
	return diskStatsForHostPath(root)
}

type diskMountDetails struct {
	FileSystemType string
	MountPoint     string
	MountSource    string
	MountOptions   string
}

type mountInfoEntry struct {
	MountPoint     string
	FileSystemType string
	MountSource    string
	MountOptions   string
}

type deleteMountBoundary struct {
	root        string
	mountPoints map[string]struct{}
	err         error
}

func (fs *FileSystem) captureDeleteMountBoundary(root string) deleteMountBoundary {
	reader := fs.readDeleteMountPoints
	if reader == nil {
		reader = currentDeleteMountPoints
	}
	mountPoints, err := reader()
	if err != nil {
		return deleteMountBoundary{err: fmt.Errorf("read mount table: %w", err)}
	}
	if len(mountPoints) == 0 {
		return deleteMountBoundary{err: errors.New("mount table is empty")}
	}

	cleanRoot, err := normalizeStorageHostPath(root)
	if err != nil {
		return deleteMountBoundary{err: err}
	}
	boundary := deleteMountBoundary{
		root:        cleanRoot,
		mountPoints: make(map[string]struct{}),
	}
	for _, mountPoint := range mountPoints {
		if mountPoint == "" {
			return deleteMountBoundary{err: errors.New("mount table contains an empty mount point")}
		}
		if !filepath.IsAbs(mountPoint) || strings.IndexByte(mountPoint, 0) >= 0 {
			return deleteMountBoundary{err: fmt.Errorf("invalid mount point %q", mountPoint)}
		}
		cleanMountPoint := filepath.Clean(mountPoint)
		if cleanMountPoint == cleanRoot || !pathWithinMount(cleanMountPoint, cleanRoot) {
			continue
		}
		boundary.mountPoints[cleanMountPoint] = struct{}{}
	}
	return boundary
}

func (b deleteMountBoundary) checkWorkspacePath(workspacePath string) error {
	cleanWorkspacePath := workspace.CleanPath(workspacePath)
	hostPath := b.root
	if cleanWorkspacePath != "/" {
		hostPath = filepath.Join(b.root, filepath.FromSlash(strings.TrimPrefix(cleanWorkspacePath, "/")))
	}
	return b.checkHostPath(hostPath)
}

func (b deleteMountBoundary) checkHostPath(hostPath string) error {
	if b.err != nil {
		return fmt.Errorf("%w: deletion mount boundary could not be verified: %v", ErrNotRegular, b.err)
	}
	hostPath = filepath.Clean(hostPath)
	for mountPoint := range b.mountPoints {
		if pathWithinMount(hostPath, mountPoint) {
			return fmt.Errorf("%w: deletion target crosses mount point %s", ErrNotRegular, mountPoint)
		}
	}
	return nil
}

func (b deleteMountBoundary) checkHostTree(hostRoot string) error {
	if b.err != nil {
		return fmt.Errorf("%w: deletion mount boundary could not be verified: %v", ErrNotRegular, b.err)
	}
	hostRoot = filepath.Clean(hostRoot)
	for mountPoint := range b.mountPoints {
		if pathWithinMount(mountPoint, hostRoot) || pathWithinMount(hostRoot, mountPoint) {
			return fmt.Errorf("%w: deletion target crosses mount point %s", ErrNotRegular, mountPoint)
		}
	}
	return nil
}

func diskMountDetailsForPath(root string, magic uint64) diskMountDetails {
	mountInfo, err := readMountInfo()
	if err == nil {
		if details, err := diskMountDetailsFromMountInfo(root, mountInfo); err == nil && details.FileSystemType != "" {
			return details
		}
	}
	return diskMountDetails{FileSystemType: filesystemTypeFromMagic(magic)}
}

func filesystemTypeFromMountInfo(root string, mountInfo []byte) (string, error) {
	details, err := diskMountDetailsFromMountInfo(root, mountInfo)
	if err != nil {
		return "", err
	}
	return details.FileSystemType, nil
}

func diskMountDetailsFromMountInfo(root string, mountInfo []byte) (diskMountDetails, error) {
	target, err := normalizeStorageHostPath(root)
	if err != nil {
		return diskMountDetails{}, err
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	target = filepath.Clean(target)

	entries, err := parseMountInfo(mountInfo)
	if err != nil {
		return diskMountDetails{}, err
	}
	bestMountLen := -1
	bestDetails := diskMountDetails{}
	for _, entry := range entries {
		if !pathWithinMount(target, entry.MountPoint) {
			continue
		}
		if len(entry.MountPoint) > bestMountLen {
			bestMountLen = len(entry.MountPoint)
			bestDetails = diskMountDetails{
				FileSystemType: entry.FileSystemType,
				MountPoint:     entry.MountPoint,
				MountSource:    entry.MountSource,
				MountOptions:   entry.MountOptions,
			}
		}
	}
	if bestDetails.FileSystemType == "" {
		return diskMountDetails{}, fmt.Errorf("mount info did not contain path %s", target)
	}
	return bestDetails, nil
}

func mountPointsFromMountInfo(mountInfo []byte) ([]string, error) {
	entries, err := parseMountInfo(mountInfo)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("mountinfo contains no mount entries")
	}
	mountPoints := make([]string, 0, len(entries))
	for _, entry := range entries {
		mountPoints = append(mountPoints, entry.MountPoint)
	}
	return mountPoints, nil
}

func parseMountInfo(mountInfo []byte) ([]mountInfoEntry, error) {
	entries := make([]mountInfoEntry, 0)
	for lineNumber, line := range strings.Split(string(mountInfo), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		separator := strings.Index(line, " - ")
		if separator < 0 {
			return nil, fmt.Errorf("invalid mountinfo line %d: missing separator", lineNumber+1)
		}
		mountFields := strings.Fields(line[:separator])
		fsFields := strings.Fields(line[separator+3:])
		if len(mountFields) < 6 || len(fsFields) < 2 {
			return nil, fmt.Errorf("invalid mountinfo line %d: missing fields", lineNumber+1)
		}
		mountPoint, err := unescapeMountInfoPath(mountFields[4])
		if err != nil {
			return nil, fmt.Errorf("invalid mountinfo line %d mount point: %w", lineNumber+1, err)
		}
		mountSource, err := unescapeMountInfoPath(fsFields[1])
		if err != nil {
			return nil, fmt.Errorf("invalid mountinfo line %d mount source: %w", lineNumber+1, err)
		}
		entries = append(entries, mountInfoEntry{
			MountPoint:     filepath.Clean(mountPoint),
			FileSystemType: strings.ToLower(fsFields[0]),
			MountSource:    mountSource,
			MountOptions:   mountFields[5],
		})
	}
	return entries, nil
}

func pathWithinMount(target, mountPoint string) bool {
	rel, err := filepath.Rel(mountPoint, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func unescapeMountInfoPath(value string) (string, error) {
	var builder strings.Builder
	builder.Grow(len(value))
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			builder.WriteByte(value[i])
			continue
		}
		if i+3 >= len(value) {
			return "", fmt.Errorf("invalid mountinfo escape in %q", value)
		}
		decoded, ok := decodeMountInfoOctal(value[i+1 : i+4])
		if !ok {
			return "", fmt.Errorf("invalid mountinfo escape in %q", value)
		}
		builder.WriteByte(decoded)
		i += 3
	}
	return builder.String(), nil
}

func decodeMountInfoOctal(value string) (byte, bool) {
	if len(value) != 3 {
		return 0, false
	}
	var decoded byte
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '7' {
			return 0, false
		}
		decoded = decoded*8 + value[i] - '0'
	}
	return decoded, true
}

func filesystemTypeFromMagic(magic uint64) string {
	switch magic {
	case 0x01021994:
		return "tmpfs"
	case 0xEF53:
		return "ext"
	case 0x2FC12FC1:
		return "zfs"
	case 0x9123683E:
		return "btrfs"
	case 0x58465342:
		return "xfs"
	case 0x65735546:
		return "fuse"
	case 0x2011BAB0:
		return "exfat"
	case 0x6969:
		return "nfs"
	case 0x517B:
		return "smb"
	case 0xFF534D42:
		return "cifs"
	case 0xFE534D42:
		return "smb2"
	default:
		return "unknown"
	}
}

func filesystemHasNativeDataChecksumSupport(fsType string) bool {
	switch strings.ToLower(strings.TrimSpace(fsType)) {
	case "zfs", "btrfs":
		return true
	default:
		return false
	}
}

// ============================================================================
// Search Operations
// ============================================================================

// Search searches for files matching the query
func (fs *FileSystem) Search(ctx context.Context, query string, limit int) ([]*SearchResult, error) {
	return fs.search(ctx, "/", query, limit, nil)
}

// SearchWithinBase returns search results under a specific workspace root.
func (fs *FileSystem) SearchWithinBase(ctx context.Context, root, query string, limit int) ([]*SearchResult, error) {
	normalizedRoot, err := normalizeStorageWorkspacePath(root)
	if err != nil {
		return nil, err
	}
	return fs.search(ctx, normalizedRoot, query, limit, nil)
}

// SearchFiltered searches from the workspace root and applies filter before
// counting a result against limit.
func (fs *FileSystem) SearchFiltered(ctx context.Context, query string, limit int, filter SearchFilter) ([]*SearchResult, error) {
	return fs.search(ctx, "/", query, limit, filter)
}

func (fs *FileSystem) search(ctx context.Context, root, query string, limit int, filter SearchFilter) ([]*SearchResult, error) {
	query = strings.TrimSpace(query)
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
	err := walkStorageWorkspace(ctx, workspaceRef, root, func(filePath string, info *workspace.FileInfo) error {
		if len(results) >= limit {
			return io.EOF // Stop walking
		}

		name := strings.ToLower(info.Name)
		if strings.Contains(name, query) {
			result := &SearchResult{
				Path:    filePath,
				Name:    info.Name,
				IsDir:   info.IsDir,
				Size:    info.Size,
				ModTime: info.ModTime,
			}
			if filter != nil {
				include, err := filter(result)
				if err != nil {
					return err
				}
				if !include {
					return nil
				}
			}
			results = append(results, result)
			if len(results) >= limit {
				return io.EOF
			}
		}
		return nil
	})

	if err != nil && err != io.EOF {
		return nil, mapWorkspaceReadablePathError(err)
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
	fs.lockMutation()

	return func() {
		fs.mu.Unlock()
		fs.gcMu.RUnlock()
	}
}

func (fs *FileSystem) lockMutation() {
	fs.mu.Lock()
	fs.mutationEpoch++
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
		if err := fs.deleteVersionObject(ctx, hash); err != nil && !errors.Is(err, versionstore.ErrNotFound) {
			deleteErr = errors.Join(deleteErr, fmt.Errorf("delete version object %s: %w", hash, err))
		}
	}

	return deleteErr
}

func (fs *FileSystem) runRetentionSweepLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	paths, err := fs.listVersionPaths(ctx)
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

	stats, err := diskStatsForPath(fs.workspace.Root())
	if err != nil {
		return false
	}

	return stats.AvailableBytes < fs.config.MinFreeSpace
}

func (fs *FileSystem) deleteIndexEntriesForDeleteTarget(ctx context.Context, name string, isDir bool) error {
	if isDir {
		return fs.deleteFileIndexPrefix(ctx, name)
	}
	return fs.deleteFileIndex(ctx, name)
}

func (fs *FileSystem) syncRestoredIndexEntries(ctx context.Context, name string, isDir bool) error {
	if !isDir {
		return fs.syncFileIndexFromWorkspace(ctx, name)
	}

	return walkStorageWorkspace(ctx, fs.workspace, name, func(filePath string, entry *workspace.FileInfo) error {
		if entry == nil || entry.IsDir {
			return nil
		}
		return fs.syncFileIndexFromWorkspace(ctx, filePath)
	})
}

func pathMatchesOrDescendant(rootPath, candidatePath string) bool {
	if candidatePath == rootPath {
		return true
	}
	if rootPath == "/" {
		return strings.HasPrefix(candidatePath, "/")
	}
	return strings.HasPrefix(candidatePath, rootPath+"/")
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
	if strings.IndexFunc(normalized, unicode.IsControl) >= 0 {
		return "", ErrNotFound
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "." || segment == ".." {
			return "", ErrNotFound
		}
	}
	return workspace.CleanPath(name), nil
}

func rejectStorageRootMutation(name string) error {
	if workspace.CleanPath(name) == "/" {
		return ErrNotFound
	}
	return nil
}

const storageRootEscapeError = "path escapes from parent"

type storagePathRoot struct {
	absRoot string
	handle  *os.Root
}

func storagePreservedMode(mode os.FileMode) os.FileMode {
	return mode.Perm() | mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky)
}

func normalizeStorageHostPath(target string) (string, error) {
	cleaned := filepath.Clean(target)
	if !filepath.IsAbs(cleaned) {
		absPath, err := filepath.Abs(cleaned)
		if err != nil {
			return "", err
		}
		cleaned = absPath
	}
	return cleaned, nil
}

func storageRelativePath(rootAbs, targetAbs string) (string, bool) {
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", false
	}
	if rel == "." {
		return ".", true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func storageAbsolutePath(root *storagePathRoot, rel string) string {
	if rel == "." {
		return root.absRoot
	}
	return filepath.Join(root.absRoot, rel)
}

func isStorageRootEscapeError(err error) bool {
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		return false
	}
	return pathErr.Err != nil && pathErr.Err.Error() == storageRootEscapeError
}

func mapStorageRootPathError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, rootio.ErrEntryChanged) {
		return ErrDeleteTargetChanged
	}
	if isStorageRootEscapeError(err) || rootio.IsSymlinkError(err) {
		return errStoragePathSymlink
	}
	return err
}

func mapStorageCreateTargetError(err error) error {
	mappedErr := mapStorageRootPathError(err)
	if errors.Is(mappedErr, os.ErrExist) {
		return ErrAlreadyExists
	}
	return mappedErr
}

func newStorageTempName(parentName, prefix string) (string, error) {
	var suffix [8]byte
	if _, err := storageRandomRead(suffix[:]); err != nil {
		return "", err
	}
	tempName := prefix + hex.EncodeToString(suffix[:]) + ".tmp"
	if parentName == "." {
		return tempName, nil
	}
	return filepath.Join(parentName, tempName), nil
}

func createStorageTempFile(root *os.Root, parentName, prefix string) (*os.File, string, error) {
	for range 32 {
		tempName, err := newStorageTempName(parentName, prefix)
		if err != nil {
			return nil, "", err
		}
		tempFile, err := rootio.OpenFileNoFollow(root, tempName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			return tempFile, tempName, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return nil, "", mapStorageRootPathError(err)
	}

	return nil, "", errors.New("failed to allocate unique temp file")
}

func validateStoragePath(target string) error {
	cleaned, err := normalizeStorageHostPath(target)
	if err != nil {
		return err
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

func (fs *FileSystem) resolveStoragePathRoot(target string) (*storagePathRoot, string, string, error) {
	absPath, err := normalizeStorageHostPath(target)
	if err != nil {
		return nil, "", "", err
	}

	if rel, ok := storageRelativePath(fs.workspace.Root(), absPath); ok {
		return &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}, rel, absPath, nil
	}
	if rel, ok := storageRelativePath(fs.trashRoot, absPath); ok {
		return &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}, rel, absPath, nil
	}

	return nil, "", "", fmt.Errorf("managed path %q is outside storage roots", absPath)
}

func syncCreatedStorageManagedDirs(root *storagePathRoot, createdDirs []string) error {
	for i := 0; i < len(createdDirs); i++ {
		parent := filepath.Dir(createdDirs[i])
		if err := syncManagedStorageDir(root.handle, parent, storageAbsolutePath(root, parent)); err != nil {
			return err
		}
	}
	return nil
}

func (fs *FileSystem) cleanupCreatedStorageManagedTrees(root *storagePathRoot, createdDirs []string) error {
	if len(createdDirs) == 0 {
		return nil
	}
	paths := make([]string, 0, len(createdDirs))
	for _, dir := range createdDirs {
		if dir != "." {
			paths = append(paths, storageAbsolutePath(root, dir))
		}
	}
	if len(paths) == 0 {
		return nil
	}
	// Mkdir does not return an identity-bearing handle. A directory captured
	// after creation could already be an external replacement, so failed
	// operations retain created parent directories instead of deleting by path.
	return fmt.Errorf("created directories retained after failed operation: %s", strings.Join(paths, ", "))
}

func (fs *FileSystem) cleanupStorageTempPath(root *storagePathRoot, tmpRel, tmpAbs string, expected os.FileInfo) error {
	if err := fs.captureDeleteMountBoundary(root.absRoot).checkHostTree(tmpAbs); err != nil {
		return fmt.Errorf("cleanup temp file %s: %w", tmpRel, err)
	}
	if expected == nil {
		return fmt.Errorf("cleanup temp file %s: identity unavailable; retained at %s", tmpRel, tmpAbs)
	}
	if err := removeCopiedMoveSource(root.handle, tmpRel, tmpAbs, expected); err != nil {
		return fmt.Errorf("cleanup temp file %s: %w", tmpRel, mapStorageRootPathError(err))
	}
	parentRel := filepath.Dir(tmpRel)
	if err := syncManagedStorageDir(root.handle, parentRel, storageAbsolutePath(root, parentRel)); err != nil {
		return fmt.Errorf("sync cleaned temp file %s: %w", tmpRel, err)
	}
	return nil
}

func (fs *FileSystem) checkStorageCopyMountBoundaries(srcRoot *storagePathRoot, srcAbs string, dstRoot *storagePathRoot, dstAbs string) error {
	return errors.Join(
		fs.captureDeleteMountBoundary(srcRoot.absRoot).checkHostTree(srcAbs),
		fs.captureDeleteMountBoundary(dstRoot.absRoot).checkHostTree(dstAbs),
	)
}

func (fs *FileSystem) ensureStorageManagedDirTracked(root *storagePathRoot, absDir string, perm os.FileMode) ([]string, error) {
	relDir, ok := storageRelativePath(root.absRoot, absDir)
	if !ok {
		return nil, fmt.Errorf("managed directory %q is outside storage root %q", absDir, root.absRoot)
	}
	if relDir == "." {
		return nil, nil
	}

	createdDirs, err := rootio.MkdirAllNoFollowTracked(root.handle, relDir, perm)
	if err != nil {
		cleanupErr := fs.cleanupCreatedStorageManagedTrees(root, createdDirs)
		return nil, errors.Join(mapStorageRootPathError(err), cleanupErr)
	}
	if err := syncCreatedStorageManagedDirs(root, createdDirs); err != nil {
		cleanupErr := fs.cleanupCreatedStorageManagedTrees(root, createdDirs)
		return nil, errors.Join(err, cleanupErr)
	}
	return createdDirs, nil
}

func ensureStorageManagedDir(root *storagePathRoot, absDir string, perm os.FileMode) error {
	relDir, ok := storageRelativePath(root.absRoot, absDir)
	if !ok {
		return fmt.Errorf("managed directory %q is outside storage root %q", absDir, root.absRoot)
	}

	var createdDirs []string
	if relDir != "." {
		var err error
		createdDirs, err = rootio.MkdirAllNoFollowTracked(root.handle, relDir, perm)
		if err != nil {
			return mapStorageRootPathError(err)
		}
	}
	return syncCreatedStorageManagedDirs(root, createdDirs)
}

func syncStorageManagedRenameDirs(oldRoot *storagePathRoot, oldRel string, newRoot *storagePathRoot, newRel string) error {
	oldParent := filepath.Dir(oldRel)
	newParent := filepath.Dir(newRel)
	if oldRoot.handle == newRoot.handle && oldParent == newParent {
		return syncManagedStorageDir(oldRoot.handle, oldParent, storageAbsolutePath(oldRoot, oldParent))
	}
	if err := syncManagedStorageDir(newRoot.handle, newParent, storageAbsolutePath(newRoot, newParent)); err != nil {
		return err
	}
	return syncManagedStorageDir(oldRoot.handle, oldParent, storageAbsolutePath(oldRoot, oldParent))
}

func syncStorageDir(dir string) error {
	dirHandle, err := rootio.OpenDirPathNoFollow(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func syncCreatedStorageDirs(createdDirs []string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncStoragePathDir(filepath.Dir(createdDirs[i])); err != nil {
			return err
		}
	}
	return nil
}

func ensureStorageDir(dir string, perm os.FileMode) error {
	createdDirs, err := rootio.MkdirAllPathNoFollowTracked(dir, perm)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return errStoragePathSymlink
		}
		return err
	}
	return syncCreatedStorageDirs(createdDirs)
}

func (fs *FileSystem) copyFile(src, dst string) error {
	if err := validateStoragePath(src); err != nil {
		return err
	}
	if err := validateStoragePath(dst); err != nil {
		return err
	}
	if err := afterValidateStoragePaths(); err != nil {
		return err
	}

	srcRoot, srcRel, srcAbs, err := fs.resolveStoragePathRoot(src)
	if err != nil {
		return err
	}
	dstRoot, dstRel, dstAbs, err := fs.resolveStoragePathRoot(dst)
	if err != nil {
		return err
	}

	checkCopyMountBoundaries := func() error {
		return fs.checkStorageCopyMountBoundaries(srcRoot, srcAbs, dstRoot, dstAbs)
	}
	if err := checkCopyMountBoundaries(); err != nil {
		return err
	}
	proof, err := fs.copyFileBetweenRootsWithIdentity(srcRoot, srcRel, srcAbs, dstRoot, dstRel, dstAbs)
	if err != nil {
		return err
	}
	if err := checkCopyMountBoundaries(); err != nil {
		cleanupErr := fs.removeCopiedFileDestination(dstRoot, dstRel, dstAbs, proof)
		return errors.Join(err, cleanupErr)
	}
	return nil
}

func (fs *FileSystem) copyFileBetweenRoots(srcRoot *storagePathRoot, srcRel, srcAbs string, dstRoot *storagePathRoot, dstRel, dstAbs string) error {
	_, err := fs.copyFileBetweenRootsWithIdentity(srcRoot, srcRel, srcAbs, dstRoot, dstRel, dstAbs)
	return err
}

type copiedFileProof struct {
	source      os.FileInfo
	destination os.FileInfo
	contentHash string
}

type copiedDestinationResidualError struct {
	path string
	err  error
}

func (e *copiedDestinationResidualError) Error() string {
	return fmt.Sprintf("copied destination retained after uncommitted operation: %s", e.path)
}

func (e *copiedDestinationResidualError) Unwrap() error {
	return e.err
}

type copiedDestinationCleanupDurabilityError struct {
	err error
}

func (e *copiedDestinationCleanupDurabilityError) Error() string {
	return e.err.Error()
}

func (e *copiedDestinationCleanupDurabilityError) Unwrap() error {
	return e.err
}

func copiedDestinationRetentionError(root *storagePathRoot, rel, abs string, cause error) error {
	if root == nil || root.handle == nil {
		return &copiedDestinationResidualError{path: abs, err: cause}
	}
	if _, err := root.handle.Lstat(rel); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cause
		}
		cause = errors.Join(cause, mapStorageRootPathError(err))
	}
	return &copiedDestinationResidualError{path: abs, err: cause}
}

func (fs *FileSystem) copyFileBetweenRootsWithIdentity(srcRoot *storagePathRoot, srcRel, srcAbs string, dstRoot *storagePathRoot, dstRel, dstAbs string) (*copiedFileProof, error) {
	if err := fs.checkStorageCopyMountBoundaries(srcRoot, srcAbs, dstRoot, dstAbs); err != nil {
		return nil, err
	}
	srcInfo, err := srcRoot.handle.Lstat(srcRel)
	if err != nil {
		return nil, mapStorageRootPathError(err)
	}
	if srcInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errStoragePathSymlink
	}
	if !srcInfo.Mode().IsRegular() {
		return nil, ErrNotRegular
	}
	if err := afterStorageCopySourceStat(srcAbs); err != nil {
		return nil, err
	}
	if err := fs.checkStorageCopyMountBoundaries(srcRoot, srcAbs, dstRoot, dstAbs); err != nil {
		return nil, err
	}

	srcFile, err := rootio.OpenRegularFileNoFollow(srcRoot.handle, srcRel)
	if err != nil {
		mappedErr := mapStorageRootPathError(err)
		if mappedErr != err {
			return nil, mappedErr
		}
		currentInfo, statErr := srcRoot.handle.Lstat(srcRel)
		if statErr == nil && currentInfo.Mode()&os.ModeSymlink == 0 && !currentInfo.Mode().IsRegular() {
			return nil, ErrNotRegular
		}
		if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENXIO) {
			return nil, ErrNotRegular
		}
		return nil, err
	}
	defer srcFile.Close()
	openedInfo, err := srcFile.Stat()
	if err != nil {
		return nil, err
	}
	if !sameStorageCopySource(srcInfo, openedInfo) {
		return nil, ErrDeleteTargetChanged
	}
	dstParentRel := filepath.Dir(dstRel)
	createdDirs, err := fs.ensureStorageManagedDirTracked(dstRoot, filepath.Dir(dstAbs), 0755)
	if err != nil {
		return nil, err
	}
	if err := fs.checkStorageCopyMountBoundaries(srcRoot, srcAbs, dstRoot, dstAbs); err != nil {
		cleanupErr := fs.cleanupCreatedStorageManagedTrees(dstRoot, createdDirs)
		return nil, errors.Join(err, cleanupErr)
	}

	dstFile, tmpRel, err := createStorageCopyTempFile(dstRoot.handle, dstParentRel, ".storage-copy-")
	if err != nil {
		cleanupErr := fs.cleanupCreatedStorageManagedTrees(dstRoot, createdDirs)
		return nil, errors.Join(err, cleanupErr)
	}
	tmpAbs := storageAbsolutePath(dstRoot, tmpRel)
	abortTempCopy := func(cause error) error {
		tempInfo, statErr := dstFile.Stat()
		closeErr := dstFile.Close()
		tempCleanupErr := fs.cleanupStorageTempPath(dstRoot, tmpRel, tmpAbs, tempInfo)
		createdDirsCleanupErr := fs.cleanupCreatedStorageManagedTrees(dstRoot, createdDirs)
		return errors.Join(cause, statErr, closeErr, tempCleanupErr, createdDirsCleanupErr)
	}
	if err := dstFile.Chmod(storagePreservedMode(srcInfo.Mode())); err != nil {
		return nil, abortTempCopy(err)
	}

	copyHasher := blake3.New()
	copied, err := io.Copy(io.MultiWriter(dstFile, copyHasher), srcFile)
	if err != nil || copied != openedInfo.Size() {
		return nil, abortTempCopy(errors.Join(err, func() error {
			if copied != openedInfo.Size() {
				return fmt.Errorf("copied %d bytes from %d-byte source", copied, openedInfo.Size())
			}
			return nil
		}()))
	}
	contentHash := fmt.Sprintf("%x", copyHasher.Sum(nil))
	afterCopyInfo, sourceStatErr := srcFile.Stat()
	currentSourceInfo, sourcePathErr := srcRoot.handle.Lstat(srcRel)
	if sourceStatErr != nil || sourcePathErr != nil || !sameStorageCopySource(openedInfo, afterCopyInfo) || !sameStorageCopySource(openedInfo, currentSourceInfo) {
		return nil, abortTempCopy(errors.Join(ErrDeleteTargetChanged, sourceStatErr, mapStorageRootPathError(sourcePathErr)))
	}
	if err := dstFile.Sync(); err != nil {
		return nil, abortTempCopy(err)
	}
	tempInfo, err := dstFile.Stat()
	if err != nil {
		return nil, abortTempCopy(err)
	}

	if err := rootio.RenamePathIntoDirNoFollow(tmpAbs, filepath.Dir(dstAbs), filepath.Base(dstRel)); err != nil {
		return nil, abortTempCopy(mapStorageCreateTargetError(err))
	}
	if err := afterCopiedFilePublish(srcAbs, dstAbs); err != nil {
		closeErr := dstFile.Close()
		return nil, errors.Join(err, closeErr, &copiedDestinationResidualError{path: dstAbs, err: errors.New("published destination hook failed")})
	}
	publishedInfo, publishedStatErr := dstFile.Stat()
	if publishedStatErr != nil || !sameStorageFileObject(tempInfo, publishedInfo) {
		closeErr := dstFile.Close()
		return nil, errors.Join(
			ErrDeleteTargetChanged,
			publishedStatErr,
			closeErr,
			&copiedDestinationResidualError{path: dstAbs, err: errors.New("published destination identity could not be established")},
		)
	}
	proof := &copiedFileProof{source: afterCopyInfo, destination: publishedInfo, contentHash: contentHash}
	publishedFailure := func(cause error) (*copiedFileProof, error) {
		cleanupErr := fs.removeCopiedFileDestination(dstRoot, dstRel, dstAbs, proof)
		createdDirsCleanupErr := fs.cleanupCreatedStorageManagedTrees(dstRoot, createdDirs)
		if cleanupErr != nil {
			var durabilityErr *copiedDestinationCleanupDurabilityError
			if !errors.As(cleanupErr, &durabilityErr) {
				residualPath := dstAbs
				var isolationResidual *copiedSourceResidualError
				if errors.As(cleanupErr, &isolationResidual) && isolationResidual.path != "" {
					residualPath = isolationResidual.path
				}
				cleanupErr = &copiedDestinationResidualError{path: residualPath, err: cleanupErr}
			}
		}
		return nil, errors.Join(cause, cleanupErr, createdDirsCleanupErr)
	}
	currentDestination, err := dstRoot.handle.Lstat(dstRel)
	if err != nil || !sameStorageCopySource(publishedInfo, currentDestination) {
		closeErr := dstFile.Close()
		return publishedFailure(errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err), closeErr))
	}
	hashCopiedFile := fs.hashStorageCopiedFile
	if hashCopiedFile == nil {
		hashCopiedFile = hashOpenWorkspaceFile
	}
	destinationHash, hashErr := hashCopiedFile(dstFile)
	afterHashInfo, afterHashStatErr := dstFile.Stat()
	currentDestination, currentErr := dstRoot.handle.Lstat(dstRel)
	closeErr := dstFile.Close()
	if hashErr != nil || afterHashStatErr != nil || currentErr != nil || closeErr != nil || destinationHash != contentHash ||
		!sameStorageCopySource(publishedInfo, afterHashInfo) || !sameStorageCopySource(afterHashInfo, currentDestination) {
		proof.destination = afterHashInfo
		return publishedFailure(errors.Join(ErrDeleteTargetChanged, hashErr, afterHashStatErr, mapStorageRootPathError(currentErr), closeErr))
	}
	proof.destination = afterHashInfo
	if err := syncManagedStorageDir(dstRoot.handle, dstParentRel, filepath.Dir(dstAbs)); err != nil {
		return publishedFailure(fmt.Errorf("failed to sync copied file: %w", err))
	}
	if err := fs.captureDeleteMountBoundary(dstRoot.absRoot).checkHostTree(dstAbs); err != nil {
		return publishedFailure(err)
	}
	currentDestination, err = dstRoot.handle.Lstat(dstRel)
	if err != nil || !sameStorageCopySource(proof.destination, currentDestination) {
		return publishedFailure(errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err)))
	}

	return proof, nil
}

func sameStorageCopySource(expected, actual os.FileInfo) bool {
	if !sameStorageFileObject(expected, actual) {
		return false
	}
	expectedIdentity := deleteStageIdentity(expected)
	actualIdentity := deleteStageIdentity(actual)
	return expectedIdentity == "" && actualIdentity == "" || expectedIdentity != "" && expectedIdentity == actualIdentity
}

func sameStorageFileObject(expected, actual os.FileInfo) bool {
	return expected != nil && actual != nil && os.SameFile(expected, actual) && expected.IsDir() == actual.IsDir() && expected.Mode() == actual.Mode() && expected.Size() == actual.Size() && expected.ModTime().Equal(actual.ModTime())
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

	reader, err := fs.workspace.OpenRegularFile(ctx, name)
	if err != nil {
		mappedErr := mapWorkspaceReadablePathError(err)
		if errors.Is(mappedErr, ErrNotDir) || isPathNotDirError(err) {
			return nil, false, ErrNotDir
		}
		return nil, false, mappedErr
	}
	defer reader.Close()

	data, err := io.ReadAll(contextCheckingReader{ctx: ctx, reader: reader})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (fs *FileSystem) writeWorkspaceFile(ctx context.Context, name string, data []byte) error {
	_, err := fs.workspace.WriteFileFromReaderWithOptions(ctx, name, bytes.NewReader(data), workspace.WriteFileOptions{
		SyncParent: syncStoragePathDir,
	})
	if err == nil {
		return nil
	}
	mappedErr := mapWorkspaceWritablePathError(unwrapWorkspaceVisibleMutationError(err))
	if workspace.IsVisibleMutationWarning(err) {
		return wrapVisibleMutationWarning(mappedErr)
	}
	return mappedErr
}

func (fs *FileSystem) validateWorkspaceParentForWrite(name string) error {
	parentAbsPath := filepath.Dir(fs.workspace.FullPath(name))
	if fs.filesRootHandle != nil {
		relDir, ok := storageRelativePath(fs.workspace.Root(), parentAbsPath)
		if !ok {
			return ErrNotFound
		}
		if relDir == "." {
			return nil
		}
		dirHandle, err := rootio.OpenDirNoFollow(fs.filesRootHandle, relDir)
		if err == nil {
			return dirHandle.Close()
		}
		if rootio.IsSymlinkError(err) {
			return errStoragePathSymlink
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}

	dirHandle, err := rootio.OpenDirPathNoFollow(parentAbsPath)
	if err == nil {
		return dirHandle.Close()
	}
	if rootio.IsSymlinkError(err) {
		return errStoragePathSymlink
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if errors.Is(err, syscall.ENOTDIR) {
		return ErrNotDir
	}
	return err
}

func (fs *FileSystem) rollbackCreatedWorkspaceDirs(ctx context.Context, createdDirs []string) error {
	var rollbackErr error
	for _, dir := range createdDirs {
		if filepath.IsAbs(dir) {
			relDir, ok := storageRelativePath(fs.workspace.Root(), dir)
			if !ok {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("created directory %s is outside workspace root", dir))
				break
			}
			dir = relDir
		}
		if dir == "." {
			continue
		}
		name := "/" + filepath.ToSlash(dir)
		if err := fs.workspace.Delete(ctx, name); err != nil {
			if errors.Is(err, workspace.ErrNotFound) {
				continue
			}
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove created directory %s: %w", name, err))
			break
		}
	}
	return rollbackErr
}

func (fs *FileSystem) restoreFileAfterIndexFailure(ctx context.Context, name string, hadPreviousFile bool, previousData []byte, createdDirs []string) error {
	if hadPreviousFile {
		return fs.workspace.WriteFile(ctx, name, previousData)
	}
	if err := fs.workspace.Delete(ctx, name); err != nil {
		return err
	}
	return fs.rollbackCreatedWorkspaceDirs(ctx, createdDirs)
}

func (fs *FileSystem) rollbackWriteVersion(ctx context.Context, name, hash string, versionRecorded, objectCreated bool) error {
	if !versionRecorded && !objectCreated {
		return nil
	}

	deleteFileVersion := fs.deleteFileVersion
	if deleteFileVersion == nil {
		deleteFileVersion = fs.versions.DeleteVersion
	}

	var rollbackErr error
	metadataRolledBack := !versionRecorded
	if versionRecorded {
		if err := deleteFileVersion(ctx, name, hash); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("delete version metadata %s: %w", hash, err))
		} else {
			metadataRolledBack = true
		}
	}
	if objectCreated && metadataRolledBack {
		if err := fs.deleteVersionObject(ctx, hash); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("failed to cleanup version object during rollback %s: %w", hash, err))
		}
	}

	return rollbackErr
}

func (fs *FileSystem) restoreDeletedIndexEntries(ctx context.Context, name string, info *FileInfo) error {
	if info == nil {
		return nil
	}
	if info.IsDir {
		return fs.syncRestoredIndexEntries(ctx, name, true)
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

func wrapStorageStepError(step string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("failed to %s: %w", step, err)
}

func (fs *FileSystem) removeTrashPathDurably(trashPath string) (bool, error) {
	if err := fs.checkTrashRootPathIdentity(); err != nil {
		return false, err
	}
	if err := fs.captureDeleteMountBoundary(fs.trashRoot).checkHostTree(trashPath); err != nil {
		return false, err
	}
	if err := fs.checkTrashRootPathIdentity(); err != nil {
		return false, err
	}
	root, relPath, _, err := fs.resolveStoragePathRoot(trashPath)
	if err != nil {
		return false, err
	}
	if relPath == "." {
		return false, errStoragePathSymlink
	}

	if err := fs.removeTrashPath(trashPath); err != nil {
		return false, err
	}
	parentRel := filepath.Dir(relPath)
	if err := syncManagedStorageDir(root.handle, parentRel, storageAbsolutePath(root, parentRel)); err != nil {
		return true, fmt.Errorf("failed to sync trash delete directory: %w", err)
	}
	return true, nil
}

func (fs *FileSystem) cleanupTrashItemDir(trashItemPath string, expected os.FileInfo) {
	if fs.captureDeleteMountBoundary(fs.trashRoot).checkHostTree(trashItemPath) != nil {
		return
	}
	root, relPath, _, err := fs.resolveStoragePathRoot(trashItemPath)
	if err != nil {
		return
	}
	if relPath == "." {
		return
	}
	parent, err := rootio.OpenDirNoFollow(root.handle, filepath.Dir(relPath))
	if err != nil {
		return
	}
	defer parent.Close()
	base := filepath.Base(relPath)
	if err := rootio.RemoveAllFromDirNoFollowChecked(parent, base, func(entryPath string, info os.FileInfo) error {
		if entryPath != base || expected == nil || info == nil || !info.IsDir() || !os.SameFile(expected, info) {
			return rootio.ErrEntryChanged
		}
		return nil
	}); err != nil {
		return
	}
	_ = syncManagedStorageDir(root.handle, filepath.Dir(relPath), storageAbsolutePath(root, filepath.Dir(relPath)))
}

func (fs *FileSystem) movePath(src, dst string) error {
	if err := validateStoragePath(src); err != nil {
		return err
	}
	if err := validateStoragePath(dst); err != nil {
		return err
	}
	if err := afterValidateStoragePaths(); err != nil {
		return err
	}

	srcRoot, srcRel, srcAbs, err := fs.resolveStoragePathRoot(src)
	if err != nil {
		return err
	}
	dstRoot, dstRel, dstAbs, err := fs.resolveStoragePathRoot(dst)
	if err != nil {
		return err
	}
	checkMoveMountBoundaries := func() error {
		return fs.checkStorageCopyMountBoundaries(srcRoot, srcAbs, dstRoot, dstAbs)
	}
	if err := checkMoveMountBoundaries(); err != nil {
		return err
	}

	createdParentDirs, err := fs.ensureStorageManagedDirTracked(dstRoot, filepath.Dir(dstAbs), 0755)
	if err != nil {
		return err
	}
	abortMove := func(cause error) error {
		if len(createdParentDirs) == 0 {
			return cause
		}
		if cleanupErr := fs.cleanupCreatedStorageManagedTrees(dstRoot, createdParentDirs); cleanupErr != nil {
			return errors.Join(cause, cleanupErr)
		}
		return cause
	}
	if err := checkMoveMountBoundaries(); err != nil {
		return abortMove(err)
	}

	if srcRoot.handle == dstRoot.handle {
		if err := movePathRename(srcRoot.handle, srcRel, dstRel, srcAbs, dstAbs); err == nil {
			if mountErr := checkMoveMountBoundaries(); mountErr != nil {
				if rollbackErr := movePathRename(srcRoot.handle, dstRel, srcRel, dstAbs, srcAbs); rollbackErr != nil {
					return abortMove(errors.Join(
						mountErr,
						fmt.Errorf("failed to rollback renamed path after mount boundary changed: %w", mapStorageRootPathError(rollbackErr)),
					))
				}
				if rollbackSyncErr := syncStorageManagedRenameDirs(dstRoot, dstRel, srcRoot, srcRel); rollbackSyncErr != nil {
					return abortMove(errors.Join(
						mountErr,
						fmt.Errorf("failed to sync mount-boundary rollback path: %w", rollbackSyncErr),
					))
				}
				return abortMove(mountErr)
			}
			if syncErr := syncStorageManagedRenameDirs(srcRoot, srcRel, dstRoot, dstRel); syncErr != nil {
				if rollbackErr := movePathRename(srcRoot.handle, dstRel, srcRel, dstAbs, srcAbs); rollbackErr != nil {
					return errors.Join(
						fmt.Errorf("failed to sync renamed path: %w", syncErr),
						fmt.Errorf("failed to rollback renamed path: %w", mapStorageRootPathError(rollbackErr)),
					)
				}
				if rollbackSyncErr := syncStorageManagedRenameDirs(dstRoot, dstRel, srcRoot, srcRel); rollbackSyncErr != nil {
					return errors.Join(
						fmt.Errorf("failed to sync renamed path: %w", syncErr),
						fmt.Errorf("failed to sync rollback path: %w", rollbackSyncErr),
					)
				}
				return abortMove(fmt.Errorf("failed to sync renamed path: %w", syncErr))
			}
			return nil
		} else if mappedErr := mapStorageCreateTargetError(err); mappedErr != err {
			return abortMove(mappedErr)
		}
	}
	if err := checkMoveMountBoundaries(); err != nil {
		return abortMove(err)
	}
	info, statErr := srcRoot.handle.Lstat(srcRel)
	if statErr != nil {
		return abortMove(mapStorageRootPathError(statErr))
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return abortMove(errStoragePathSymlink)
	}

	if info.IsDir() {
		if err := fs.copyDirBetweenRoots(srcRoot, srcRel, srcAbs, dstRoot, dstRel, dstAbs); err != nil {
			return abortMove(err)
		}
		if err := checkMoveMountBoundaries(); err != nil {
			cleanupErr := fs.removeCopiedMoveDestination(dstRoot, dstRel, dstAbs)
			return abortMove(errors.Join(err, cleanupErr))
		}
		if err := movePathRemoveAll(srcRoot.handle, srcRel, srcAbs); err != nil {
			return fmt.Errorf("failed to remove copied source directory: %w", mapStorageRootPathError(err))
		}
		return syncRemovedStorageSourceDir(srcRoot, srcRel)
	}

	proof, err := fs.copyFileBetweenRootsWithIdentity(srcRoot, srcRel, srcAbs, dstRoot, dstRel, dstAbs)
	if err != nil {
		return abortMove(err)
	}
	if err := checkMoveMountBoundaries(); err != nil {
		cleanupErr := fs.removeCopiedFileDestination(dstRoot, dstRel, dstAbs, proof)
		return abortMove(errors.Join(err, cleanupErr))
	}
	if err := beforeCopiedFileSourceIsolation(srcAbs, dstAbs); err != nil {
		cleanupErr := fs.removeCopiedFileDestination(dstRoot, dstRel, dstAbs, proof)
		return abortMove(errors.Join(err, cleanupErr))
	}
	isolatedSource, err := fs.isolateCopiedFile(srcRoot, srcRel, srcAbs, proof.source, proof.contentHash)
	if err != nil {
		var residual *copiedSourceResidualError
		if errors.As(err, &residual) {
			return abortMove(errors.Join(err, copiedDestinationRetentionError(
				dstRoot,
				dstRel,
				dstAbs,
				errors.New("published destination retained because copied source isolation could not be restored"),
			)))
		}
		cleanupErr := fs.removeCopiedFileDestination(dstRoot, dstRel, dstAbs, proof)
		return abortMove(errors.Join(err, cleanupErr))
	}
	abortIsolatedMove := func(cause error) error {
		restored, rollbackErr := fs.restoreIsolatedCopiedFile(isolatedSource)
		if !restored || rollbackErr != nil {
			residualPath := isolatedSource.isolatedAbs
			if restored {
				residualPath = isolatedSource.originalAbs
			}
			return abortMove(errors.Join(
				cause,
				&copiedSourceResidualError{path: residualPath, err: rollbackErr},
				copiedDestinationRetentionError(
					dstRoot,
					dstRel,
					dstAbs,
					errors.New("published destination retained because isolated copied source could not be restored"),
				),
			))
		}
		cleanupErr := fs.removeCopiedFileDestination(dstRoot, dstRel, dstAbs, proof)
		return abortMove(errors.Join(cause, rollbackErr, cleanupErr))
	}
	if err := afterCopiedFileSourceIsolation(srcAbs, isolatedSource.isolatedAbs, dstAbs); err != nil {
		return abortIsolatedMove(err)
	}
	if err := fs.verifyCopiedFileDestination(dstRoot, dstRel, dstAbs, proof); err != nil {
		return abortIsolatedMove(err)
	}
	if err := movePathRemove(srcRoot.handle, isolatedSource.isolatedRel, isolatedSource.isolatedAbs, isolatedSource.info); err != nil {
		return abortIsolatedMove(mapStorageRootPathError(err))
	}
	return syncRemovedStorageSourceDir(srcRoot, isolatedSource.isolatedRel)
}

func (fs *FileSystem) removeCopiedMoveDestination(root *storagePathRoot, rel, abs string) error {
	if err := fs.captureDeleteMountBoundary(root.absRoot).checkHostTree(abs); err != nil {
		return fmt.Errorf("failed to verify copied destination cleanup: %w", err)
	}
	if err := rootio.RemoveAllNoFollow(root.handle, rel); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to cleanup copied destination: %w", mapStorageRootPathError(err))
	}
	parentRel := filepath.Dir(rel)
	if err := syncManagedStorageDir(root.handle, parentRel, storageAbsolutePath(root, parentRel)); err != nil {
		return fmt.Errorf("failed to sync copied destination cleanup: %w", err)
	}
	return nil
}

type isolatedCopiedFile struct {
	root        *storagePathRoot
	originalRel string
	originalAbs string
	isolatedRel string
	isolatedAbs string
	info        os.FileInfo
	contentHash string
}

type copiedSourceResidualError struct {
	path string
	err  error
}

func (e *copiedSourceResidualError) Error() string {
	return fmt.Sprintf("copied source retained in internal isolation: %s", e.path)
}

func (e *copiedSourceResidualError) Unwrap() error {
	return e.err
}

func (fs *FileSystem) isolateCopiedFile(root *storagePathRoot, rel, abs string, expected os.FileInfo, expectedHash string) (*isolatedCopiedFile, error) {
	if err := fs.captureDeleteMountBoundary(root.absRoot).checkHostTree(abs); err != nil {
		return nil, err
	}
	current, err := root.handle.Lstat(rel)
	if err != nil || !sameStorageCopySource(expected, current) {
		return nil, errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	for range 32 {
		isolatedRel, err := newStorageTempName(filepath.Dir(rel), ".mnemonas-move-")
		if err != nil {
			return nil, err
		}
		isolatedAbs := storageAbsolutePath(root, isolatedRel)
		if err := rootio.RenameNoFollow(root.handle, rel, isolatedRel); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return nil, mapStorageRootPathError(err)
		}
		isolatedInfo, statErr := root.handle.Lstat(isolatedRel)
		if statErr != nil || !sameStorageFileObject(expected, isolatedInfo) {
			rollbackErr := rootio.RenameNoFollow(root.handle, isolatedRel, rel)
			syncErr := syncStorageManagedRenameDirs(root, isolatedRel, root, rel)
			if rollbackErr != nil {
				return nil, errors.Join(ErrDeleteTargetChanged, statErr, &copiedSourceResidualError{path: isolatedAbs, err: mapStorageRootPathError(rollbackErr)}, syncErr)
			}
			if syncErr != nil {
				return nil, errors.Join(ErrDeleteTargetChanged, statErr, &copiedSourceResidualError{path: abs, err: syncErr})
			}
			return nil, errors.Join(ErrDeleteTargetChanged, statErr, syncErr)
		}
		isolated := &isolatedCopiedFile{
			root:        root,
			originalRel: rel,
			originalAbs: abs,
			isolatedRel: isolatedRel,
			isolatedAbs: isolatedAbs,
			info:        isolatedInfo,
			contentHash: expectedHash,
		}
		if err := fs.verifyStorageFileProof(root, isolatedRel, isolatedAbs, isolatedInfo, expectedHash); err != nil {
			restored, rollbackErr := fs.restoreCapturedIsolatedFile(isolated)
			if !restored {
				return nil, errors.Join(err, &copiedSourceResidualError{path: isolatedAbs, err: rollbackErr})
			}
			if rollbackErr != nil {
				return nil, errors.Join(err, &copiedSourceResidualError{path: abs, err: rollbackErr})
			}
			return nil, errors.Join(err, rollbackErr)
		}
		return isolated, nil
	}
	return nil, errors.New("failed to allocate copied source isolation path")
}

func (fs *FileSystem) restoreCapturedIsolatedFile(isolated *isolatedCopiedFile) (bool, error) {
	if isolated == nil || isolated.root == nil {
		return false, ErrDeleteTargetChanged
	}
	if err := rootio.RenameNoFollow(isolated.root.handle, isolated.isolatedRel, isolated.originalRel); err != nil {
		return false, mapStorageRootPathError(err)
	}
	restoredInfo, err := isolated.root.handle.Lstat(isolated.originalRel)
	if err != nil || !sameStorageFileObject(isolated.info, restoredInfo) {
		return true, errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	return true, syncStorageManagedRenameDirs(isolated.root, isolated.isolatedRel, isolated.root, isolated.originalRel)
}

func (fs *FileSystem) restoreIsolatedCopiedFile(isolated *isolatedCopiedFile) (bool, error) {
	if isolated == nil || isolated.root == nil {
		return false, ErrDeleteTargetChanged
	}
	if err := fs.verifyStorageFileProof(isolated.root, isolated.isolatedRel, isolated.isolatedAbs, isolated.info, isolated.contentHash); err != nil {
		return false, err
	}
	if err := rootio.RenameNoFollow(isolated.root.handle, isolated.isolatedRel, isolated.originalRel); err != nil {
		return false, mapStorageRootPathError(err)
	}
	restoredInfo, err := isolated.root.handle.Lstat(isolated.originalRel)
	if err != nil || !sameStorageFileObject(isolated.info, restoredInfo) {
		return true, errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	if err := fs.verifyStorageFileProof(isolated.root, isolated.originalRel, isolated.originalAbs, restoredInfo, isolated.contentHash); err != nil {
		return true, err
	}
	return true, syncStorageManagedRenameDirs(isolated.root, isolated.isolatedRel, isolated.root, isolated.originalRel)
}

func (fs *FileSystem) removeCopiedFileDestination(root *storagePathRoot, rel, abs string, proof *copiedFileProof) error {
	if proof == nil {
		return ErrDeleteTargetChanged
	}
	if err := beforeCopiedFileDestinationCleanup(abs); err != nil {
		return err
	}
	isolated, err := fs.isolateCopiedFile(root, rel, abs, proof.destination, proof.contentHash)
	if err != nil {
		return fmt.Errorf("failed to isolate copied file cleanup: %w", err)
	}
	if err := movePathRemove(root.handle, isolated.isolatedRel, isolated.isolatedAbs, isolated.info); err != nil {
		restored, rollbackErr := fs.restoreIsolatedCopiedFile(isolated)
		if !restored || rollbackErr != nil {
			residualPath := isolated.isolatedAbs
			if restored {
				residualPath = isolated.originalAbs
			}
			return errors.Join(fmt.Errorf("failed to cleanup copied file: %w", mapStorageRootPathError(err)), &copiedSourceResidualError{path: residualPath, err: rollbackErr})
		}
		return errors.Join(fmt.Errorf("failed to cleanup copied file: %w", mapStorageRootPathError(err)), rollbackErr)
	}
	parentRel := filepath.Dir(rel)
	if err := syncManagedStorageDir(root.handle, parentRel, storageAbsolutePath(root, parentRel)); err != nil {
		return &copiedDestinationCleanupDurabilityError{err: fmt.Errorf("failed to sync copied file cleanup: %w", err)}
	}
	return nil
}

func (fs *FileSystem) verifyCopiedFileDestination(root *storagePathRoot, rel, abs string, proof *copiedFileProof) error {
	if proof == nil {
		return ErrDeleteTargetChanged
	}
	return fs.verifyStorageFileProof(root, rel, abs, proof.destination, proof.contentHash)
}

func (fs *FileSystem) verifyStorageFileProof(root *storagePathRoot, rel, abs string, expected os.FileInfo, expectedHash string) error {
	if err := fs.captureDeleteMountBoundary(root.absRoot).checkHostTree(abs); err != nil {
		return err
	}
	file, err := rootio.OpenRegularFileNoFollow(root.handle, rel)
	if err != nil {
		return errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	before, statErr := file.Stat()
	current, pathErr := root.handle.Lstat(rel)
	if statErr != nil || pathErr != nil || !sameStorageCopySource(expected, before) || !sameStorageCopySource(before, current) {
		closeErr := file.Close()
		return errors.Join(ErrDeleteTargetChanged, statErr, mapStorageRootPathError(pathErr), closeErr)
	}
	hash, hashErr := hashOpenWorkspaceFile(file)
	after, afterStatErr := file.Stat()
	current, pathErr = root.handle.Lstat(rel)
	closeErr := file.Close()
	if hashErr != nil || afterStatErr != nil || pathErr != nil || closeErr != nil || hash != expectedHash ||
		!sameStorageCopySource(before, after) || !sameStorageCopySource(after, current) {
		return errors.Join(ErrDeleteTargetChanged, hashErr, afterStatErr, mapStorageRootPathError(pathErr), closeErr)
	}
	return nil
}

func syncRemovedStorageSourceDir(root *storagePathRoot, rel string) error {
	parentRel := filepath.Dir(rel)
	if err := syncManagedStorageDir(root.handle, parentRel, storageAbsolutePath(root, parentRel)); err != nil {
		return wrapVisibleMutationWarning(fmt.Errorf("failed to sync moved source directory: %w", err))
	}
	return nil
}

func isPathNotDirError(err error) bool {
	return errors.Is(err, syscall.ENOTDIR)
}

func (fs *FileSystem) copyDir(src, dst string) error {
	if err := validateStoragePath(src); err != nil {
		return err
	}
	if err := validateStoragePath(dst); err != nil {
		return err
	}
	if err := afterValidateStoragePaths(); err != nil {
		return err
	}

	srcRoot, srcRel, srcAbs, err := fs.resolveStoragePathRoot(src)
	if err != nil {
		return err
	}
	dstRoot, dstRel, dstAbs, err := fs.resolveStoragePathRoot(dst)
	if err != nil {
		return err
	}

	checkCopyMountBoundaries := func() error {
		return fs.checkStorageCopyMountBoundaries(srcRoot, srcAbs, dstRoot, dstAbs)
	}
	if err := checkCopyMountBoundaries(); err != nil {
		return err
	}
	if err := fs.copyDirBetweenRoots(srcRoot, srcRel, srcAbs, dstRoot, dstRel, dstAbs); err != nil {
		return err
	}
	if err := checkCopyMountBoundaries(); err != nil {
		cleanupErr := fs.removeCopiedMoveDestination(dstRoot, dstRel, dstAbs)
		return errors.Join(err, cleanupErr)
	}
	return nil
}

func (fs *FileSystem) copyDirBetweenRoots(srcRoot *storagePathRoot, srcRel, srcAbs string, dstRoot *storagePathRoot, dstRel, dstAbs string) error {
	if err := fs.checkStorageCopyMountBoundaries(srcRoot, srcAbs, dstRoot, dstAbs); err != nil {
		return err
	}
	srcInfo, err := srcRoot.handle.Lstat(srcRel)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	if srcInfo.Mode()&os.ModeSymlink != 0 {
		return errStoragePathSymlink
	}

	createdDirs, err := fs.ensureStorageManagedDirTracked(dstRoot, filepath.Dir(dstAbs), 0755)
	if err != nil {
		return err
	}
	if err := fs.checkStorageCopyMountBoundaries(srcRoot, srcAbs, dstRoot, dstAbs); err != nil {
		cleanupErr := fs.cleanupCreatedStorageManagedTrees(dstRoot, createdDirs)
		return errors.Join(err, cleanupErr)
	}
	if err := rootio.MkdirNoFollow(dstRoot.handle, dstRel, srcInfo.Mode().Perm()); err != nil {
		cleanupErr := fs.cleanupCreatedStorageManagedTrees(dstRoot, createdDirs)
		return errors.Join(mapStorageCreateTargetError(err), cleanupErr)
	}
	createdDirs = append([]string{dstRel}, createdDirs...)
	abortCopy := func(cause error) error {
		if len(createdDirs) == 0 {
			return cause
		}
		if cleanupErr := fs.cleanupCreatedStorageManagedTrees(dstRoot, createdDirs); cleanupErr != nil {
			cause = errors.Join(cause, cleanupErr)
		}
		return cause
	}
	if err := fs.checkStorageCopyMountBoundaries(srcRoot, srcAbs, dstRoot, dstAbs); err != nil {
		return abortCopy(err)
	}
	if err := syncManagedStorageDir(dstRoot.handle, filepath.Dir(dstRel), filepath.Dir(dstAbs)); err != nil {
		return abortCopy(fmt.Errorf("failed to sync copied directory: %w", err))
	}

	dstDir, err := rootio.OpenDirNoFollow(dstRoot.handle, dstRel)
	if err != nil {
		return abortCopy(mapStorageRootPathError(err))
	}
	if err := dstDir.Chmod(storagePreservedMode(srcInfo.Mode())); err != nil {
		_ = dstDir.Close()
		return abortCopy(mapStorageRootPathError(err))
	}
	if err := dstDir.Close(); err != nil {
		return abortCopy(err)
	}

	srcDir, err := rootio.OpenDirNoFollow(srcRoot.handle, srcRel)
	if err != nil {
		return abortCopy(mapStorageRootPathError(err))
	}
	defer srcDir.Close()

	entries, err := srcDir.ReadDir(-1)
	if err != nil {
		return abortCopy(err)
	}

	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return abortCopy(errStoragePathSymlink)
		}

		entryName, err := safeStorageReadDirFallbackChildName(entry.Name())
		if err != nil {
			return abortCopy(err)
		}
		srcPathRel := filepath.Join(srcRel, entryName)
		dstPathRel := filepath.Join(dstRel, entryName)
		srcPathAbs := storageAbsolutePath(srcRoot, srcPathRel)
		dstPathAbs := storageAbsolutePath(dstRoot, dstPathRel)

		if entry.IsDir() {
			if err := fs.copyDirBetweenRoots(srcRoot, srcPathRel, srcPathAbs, dstRoot, dstPathRel, dstPathAbs); err != nil {
				return abortCopy(err)
			}
			continue
		}

		if err := fs.copyFileBetweenRoots(srcRoot, srcPathRel, srcPathAbs, dstRoot, dstPathRel, dstPathAbs); err != nil {
			return abortCopy(err)
		}
	}

	return nil
}
