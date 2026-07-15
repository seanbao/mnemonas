// Package workspace provides native file system operations for MnemoNAS
package workspace

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
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/seanbao/mnemonas/internal/rootio"
)

var readDirEntryInfo = func(root *os.Root, name string, entry os.DirEntry) (os.FileInfo, error) {
	return root.Lstat(name)
}

var copyWorkspaceData = copyWithContext
var finalizeWorkspaceCopyTemp = func(root *os.Root, tmpPath string) error {
	return root.Remove(tmpPath)
}
var syncWorkspaceDir = syncWorkspaceParentDir
var afterValidateWorkspacePaths = func() error { return nil }
var beforeWorkspaceRename = func() error { return nil }
var afterWorkspaceFilePublish = func() error { return nil }
var afterWorkspaceCreatedDirTempOpen = func(string, string) error { return nil }
var afterWorkspaceCreatedDirPublish = func(string) error { return nil }
var beforeWorkspaceCreatedDirRemoval = func(string) error { return nil }

var errWorkspaceTempOwnershipChanged = errors.New("workspace temp file ownership changed")
var errWorkspaceCreatedDirOwnershipChanged = errors.New("workspace created directory ownership changed")

// IsCreatedDirOwnershipChanged reports whether a write observed a created
// directory that can no longer be safely treated as workspace-owned.
func IsCreatedDirOwnershipChanged(err error) bool {
	return errors.Is(err, errWorkspaceCreatedDirOwnershipChanged)
}

type ownedWorkspaceFileSnapshot struct {
	info               os.FileInfo
	deleteIdentity     string
	persistentIdentity string
}

// CreatedDir records the identity of a workspace directory created for one
// write. The private evidence fields prevent callers from manufacturing
// ownership proof for an unrelated directory.
type CreatedDir struct {
	Path               string
	relativePath       string
	info               os.FileInfo
	persistentIdentity string
	mode               os.FileMode
	handle             *os.File
}

// CreatedDirEvidence is a serializable snapshot of one directory that the
// current write created and still owns.
type CreatedDirEvidence struct {
	Path               string
	RelativePath       string
	PersistentIdentity string
	DeleteIdentity     string
	Mode               os.FileMode
}

const workspaceRootEscapeError = "path escapes from parent"

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buffer := make([]byte, 32*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}

		readBytes, readErr := src.Read(buffer)
		if readBytes > 0 {
			if err := ctx.Err(); err != nil {
				return written, err
			}
			writeBytes, writeErr := dst.Write(buffer[:readBytes])
			written += int64(writeBytes)
			if writeErr != nil {
				return written, writeErr
			}
			if writeBytes != readBytes {
				return written, io.ErrShortWrite
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return written, nil
			}
			return written, readErr
		}
	}
}

func cleanupTempPath(tmpPath string, operationErr error) error {
	if removeErr := os.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return errors.Join(operationErr, fmt.Errorf("cleanup temp file %s: %w", tmpPath, removeErr))
	}
	return operationErr
}

func cleanupWorkspaceTempPath(root *os.Root, tmpPath string, operationErr error) error {
	if removeErr := removeWorkspaceTempPath(root, tmpPath); removeErr != nil {
		return errors.Join(operationErr, fmt.Errorf("cleanup temp file %s: %w", tmpPath, removeErr))
	}
	return operationErr
}

func removeWorkspaceTempPath(root *os.Root, tmpPath string) error {
	if removeErr := root.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	return nil
}

func newOwnedWorkspaceFileSnapshot(info os.FileInfo) ownedWorkspaceFileSnapshot {
	return ownedWorkspaceFileSnapshot{
		info:               info,
		deleteIdentity:     DeleteIdentityTokenForFileInfo(info),
		persistentIdentity: PersistentIdentityTokenForFileInfo(info),
	}
}

func readOwnedWorkspaceFileSnapshot(root *os.Root, name string, expected ownedWorkspaceFileSnapshot, verifyDeleteIdentity bool) (ownedWorkspaceFileSnapshot, error) {
	if expected.info == nil || !expected.info.Mode().IsRegular() {
		return ownedWorkspaceFileSnapshot{}, errWorkspaceTempOwnershipChanged
	}

	file, err := rootio.OpenRegularFileNoFollow(root, name)
	if err != nil {
		return ownedWorkspaceFileSnapshot{}, errors.Join(errWorkspaceTempOwnershipChanged, err)
	}
	defer file.Close()

	current, err := file.Stat()
	if err != nil {
		return ownedWorkspaceFileSnapshot{}, errors.Join(errWorkspaceTempOwnershipChanged, err)
	}
	if !current.Mode().IsRegular() || !os.SameFile(expected.info, current) ||
		current.Mode() != expected.info.Mode() || current.Size() != expected.info.Size() ||
		!current.ModTime().Equal(expected.info.ModTime()) {
		return ownedWorkspaceFileSnapshot{}, errWorkspaceTempOwnershipChanged
	}
	if expected.persistentIdentity != "" &&
		PersistentIdentityTokenForFileInfo(current) != expected.persistentIdentity {
		return ownedWorkspaceFileSnapshot{}, errWorkspaceTempOwnershipChanged
	}
	if verifyDeleteIdentity && expected.deleteIdentity != "" &&
		DeleteIdentityTokenForFileInfo(current) != expected.deleteIdentity {
		return ownedWorkspaceFileSnapshot{}, errWorkspaceTempOwnershipChanged
	}
	return newOwnedWorkspaceFileSnapshot(current), nil
}

func verifyOwnedWorkspaceFile(root *os.Root, name string, expected ownedWorkspaceFileSnapshot, verifyDeleteIdentity bool) error {
	_, err := readOwnedWorkspaceFileSnapshot(root, name, expected, verifyDeleteIdentity)
	return err
}

func removeOwnedWorkspaceTempPath(root *os.Root, tmpPath string, expected os.FileInfo) error {
	if expected == nil || !expected.Mode().IsRegular() {
		return errWorkspaceTempOwnershipChanged
	}

	parentName := path.Dir(tmpPath)
	parent, err := rootio.OpenDirNoFollow(root, parentName)
	if err != nil {
		return err
	}
	defer parent.Close()

	return rootio.RemoveAllFromDirNoFollowChecked(parent, path.Base(tmpPath), func(_ string, current os.FileInfo) error {
		if !current.Mode().IsRegular() || !os.SameFile(expected, current) {
			return errWorkspaceTempOwnershipChanged
		}
		return nil
	})
}

func cleanupOwnedWorkspaceTempPath(root *os.Root, tmpPath string, expected os.FileInfo, operationErr error) error {
	if removeErr := removeOwnedWorkspaceTempPath(root, tmpPath, expected); removeErr != nil {
		return errors.Join(operationErr, fmt.Errorf("cleanup owned temp file %s: %w", tmpPath, removeErr))
	}
	return operationErr
}

func workspaceCreatedDirMatches(expected CreatedDir, current os.FileInfo) bool {
	if expected.info == nil || current == nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(expected.info, current) || current.Mode() != expected.mode {
		return false
	}
	return expected.persistentIdentity == "" ||
		PersistentIdentityTokenForFileInfo(current) == expected.persistentIdentity
}

func workspaceCreatedDirHandleMatches(expected CreatedDir, current os.FileInfo) bool {
	if expected.handle == nil || !workspaceCreatedDirMatches(expected, current) {
		return false
	}
	held, err := expected.handle.Stat()
	return err == nil && workspaceCreatedDirMatches(expected, held) &&
		os.SameFile(held, current) && held.Mode() == current.Mode()
}

// ReleaseCreatedDirs releases identity handles held by directory evidence.
// It is safe to call after CleanupCreatedDirs or more than once.
func ReleaseCreatedDirs(createdDirs []CreatedDir) error {
	var releaseErr error
	for i := range createdDirs {
		if createdDirs[i].handle == nil {
			continue
		}
		if err := createdDirs[i].handle.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			releaseErr = errors.Join(releaseErr, fmt.Errorf("close created directory evidence %s: %w", createdDirs[i].Path, err))
		}
	}
	return releaseErr
}

// SnapshotCreatedDirs revalidates each created directory against its retained
// handle and returns deepest-first durable ownership evidence.
func (w *Workspace) SnapshotCreatedDirs(createdDirs []CreatedDir) ([]CreatedDirEvidence, error) {
	if w == nil || w.rootHandle == nil {
		return nil, errWorkspaceCreatedDirOwnershipChanged
	}
	evidence := make([]CreatedDirEvidence, 0, len(createdDirs))
	for _, created := range createdDirs {
		expectedPath := filepath.Join(w.root, filepath.FromSlash(created.relativePath))
		if created.Path == "" ||
			filepath.Clean(created.Path) != filepath.Clean(expectedPath) {
			return nil, errWorkspaceCreatedDirOwnershipChanged
		}
		info, err := readWorkspaceCreatedDirEntry(w.rootHandle, created)
		if err != nil {
			return nil, err
		}
		persistentIdentity := PersistentIdentityTokenForFileInfo(info)
		deleteIdentity := DeleteIdentityTokenForFileInfo(info)
		if persistentIdentity == "" ||
			deleteIdentity == "" ||
			persistentIdentity != created.persistentIdentity ||
			info.Mode() != created.mode {
			return nil, errWorkspaceCreatedDirOwnershipChanged
		}
		evidence = append(evidence, CreatedDirEvidence{
			Path:               created.Path,
			RelativePath:       created.relativePath,
			PersistentIdentity: persistentIdentity,
			DeleteIdentity:     deleteIdentity,
			Mode:               info.Mode(),
		})
	}
	return evidence, nil
}

func removeOwnedWorkspaceCreatedDir(root *os.Root, created CreatedDir) error {
	if root == nil || created.relativePath == "" || created.relativePath == "." ||
		created.info == nil || created.handle == nil {
		return errWorkspaceCreatedDirOwnershipChanged
	}
	directory, err := rootio.OpenDirNoFollow(root, created.relativePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.Join(errWorkspaceCreatedDirOwnershipChanged, err)
	}
	defer directory.Close()

	finalInfo, err := directory.Stat()
	if err != nil || !workspaceCreatedDirHandleMatches(created, finalInfo) {
		return errors.Join(errWorkspaceCreatedDirOwnershipChanged, err)
	}
	entries, readErr := directory.ReadDir(1)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return errors.Join(errWorkspaceCreatedDirOwnershipChanged, readErr)
	}
	if len(entries) != 0 {
		return errWorkspaceCreatedDirOwnershipChanged
	}
	finalDeleteIdentity := DeleteIdentityTokenForFileInfo(finalInfo)
	if finalDeleteIdentity == "" {
		return errWorkspaceCreatedDirOwnershipChanged
	}
	if err := beforeWorkspaceCreatedDirRemoval(created.Path); err != nil {
		return err
	}

	parentName := path.Dir(created.relativePath)
	parent, err := rootio.OpenDirNoFollow(root, parentName)
	if err != nil {
		return errors.Join(errWorkspaceCreatedDirOwnershipChanged, err)
	}
	defer parent.Close()

	base := path.Base(created.relativePath)
	removeErr := rootio.RemoveAllFromDirNoFollowChecked(parent, base, func(name string, current os.FileInfo) error {
		if path.Clean(strings.ReplaceAll(name, "\\", "/")) != base ||
			!workspaceCreatedDirHandleMatches(created, current) || !os.SameFile(finalInfo, current) ||
			DeleteIdentityTokenForFileInfo(current) != finalDeleteIdentity {
			return errWorkspaceCreatedDirOwnershipChanged
		}
		return nil
	})
	if removeErr != nil {
		return errors.Join(errWorkspaceCreatedDirOwnershipChanged, removeErr)
	}
	return nil
}

func cleanupWorkspaceCreatedDirsWithRetention(
	rootPath string,
	root *os.Root,
	createdDirs []CreatedDir,
	operationErr error,
	retainOnOwnershipChange bool,
) (rollbackErr error) {
	rollbackErr = operationErr
	defer func() {
		if retainOnOwnershipChange && IsCreatedDirOwnershipChanged(rollbackErr) {
			return
		}
		if releaseErr := ReleaseCreatedDirs(createdDirs); releaseErr != nil {
			rollbackErr = errors.Join(rollbackErr, releaseErr)
		}
	}()
	for _, dir := range createdDirs {
		expectedPath := filepath.Join(rootPath, filepath.FromSlash(dir.relativePath))
		if dir.Path == "" || filepath.Clean(dir.Path) != filepath.Clean(expectedPath) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("cleanup created directory %s: %w", dir.Path, errWorkspaceCreatedDirOwnershipChanged))
			break
		}
		if removeErr := removeOwnedWorkspaceCreatedDir(root, dir); removeErr != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("cleanup created directory %s: %w", dir.Path, removeErr))
			break
		}
	}
	return rollbackErr
}

func cleanupWorkspaceCreatedDirs(rootPath string, root *os.Root, createdDirs []CreatedDir, operationErr error) error {
	return cleanupWorkspaceCreatedDirsWithRetention(rootPath, root, createdDirs, operationErr, false)
}

// CleanupCreatedDirs removes only empty directories whose identity still
// matches evidence returned by a prior write in this workspace.
func (w *Workspace) CleanupCreatedDirs(ctx context.Context, createdDirs []CreatedDir) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		if releaseErr := ReleaseCreatedDirs(createdDirs); releaseErr != nil {
			return errors.Join(err, releaseErr)
		}
		return err
	}
	return cleanupWorkspaceCreatedDirs(w.root, w.rootHandle, createdDirs, nil)
}

// PrepareWriteParent creates missing parent directories for a later
// descriptor-relative publish and returns identity evidence for rollback.
// The caller must release the evidence and remove the directories when the
// publish does not commit.
func (w *Workspace) PrepareWriteParent(
	ctx context.Context,
	name string,
	syncParent func(string) error,
) ([]CreatedDir, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateWorkspaceMutationName(name); err != nil {
		return nil, err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return nil, err
	}

	createdDirs, err := ensureWorkspaceDirsTracked(
		w.root,
		w.rootHandle,
		workspaceParentRelativeName(name),
		0o755,
	)
	if err != nil {
		return createdDirs, w.mapWorkspaceCreatePathError(filepath.Dir(fullPath), err)
	}
	if syncParent == nil {
		syncParent = syncWorkspaceDir
	}
	if err := syncCreatedWorkspaceDirEvidenceWith(createdDirs, syncParent); err != nil {
		return createdDirs, fmt.Errorf("sync created directory tree: %w", err)
	}
	return createdDirs, nil
}

func syncWorkspaceParentDir(dir string) error {
	dirHandle, err := rootio.OpenDirPathNoFollow(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func syncWorkspaceRenameDirs(oldPath, newPath string) error {
	oldDir := filepath.Dir(oldPath)
	newDir := filepath.Dir(newPath)
	if oldDir == newDir {
		return syncWorkspaceDir(oldDir)
	}
	if err := syncWorkspaceDir(newDir); err != nil {
		return err
	}
	return syncWorkspaceDir(oldDir)
}

func syncCreatedWorkspaceDirs(createdDirs []string) error {
	return syncCreatedWorkspaceDirsWith(createdDirs, syncWorkspaceDir)
}

func syncCreatedWorkspaceDirsWith(createdDirs []string, syncDir func(string) error) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncDir(filepath.Dir(createdDirs[i])); err != nil {
			return err
		}
	}
	return nil
}

func syncCreatedWorkspaceDirEvidenceWith(createdDirs []CreatedDir, syncDir func(string) error) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncDir(filepath.Dir(createdDirs[i].Path)); err != nil {
			return err
		}
	}
	return nil
}

func newWorkspaceCreatedDirTempName(parentName string) (string, error) {
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	tempName := ".workspace-dir-" + hex.EncodeToString(suffix[:]) + ".tmp"
	if parentName == "." {
		return tempName, nil
	}
	return path.Join(parentName, tempName), nil
}

func readWorkspaceCreatedDirEntry(root *os.Root, created CreatedDir) (os.FileInfo, error) {
	if root == nil || created.relativePath == "" || created.relativePath == "." ||
		created.info == nil || created.handle == nil {
		return nil, errWorkspaceCreatedDirOwnershipChanged
	}
	current, err := rootio.OpenDirNoFollow(root, created.relativePath)
	if err != nil {
		return nil, errors.Join(errWorkspaceCreatedDirOwnershipChanged, err)
	}
	defer current.Close()

	info, err := current.Stat()
	if err != nil || !workspaceCreatedDirHandleMatches(created, info) {
		return nil, errors.Join(errWorkspaceCreatedDirOwnershipChanged, err)
	}
	return info, nil
}

func cleanupUnpublishedWorkspaceCreatedDir(root *os.Root, created CreatedDir, operationErr error) error {
	removeErr := removeOwnedWorkspaceCreatedDir(root, created)
	releaseErr := ReleaseCreatedDirs([]CreatedDir{created})
	if removeErr != nil {
		removeErr = fmt.Errorf("cleanup unpublished directory %s: %w", created.Path, removeErr)
	}
	return errors.Join(operationErr, removeErr, releaseErr)
}

func publishWorkspaceCreatedDir(rootPath string, root *os.Root, currentRel string, perm os.FileMode) (CreatedDir, bool, error) {
	parentRel := path.Dir(currentRel)
	for range 32 {
		tempRel, err := newWorkspaceCreatedDirTempName(parentRel)
		if err != nil {
			return CreatedDir{}, false, err
		}
		if err := rootio.MkdirNoFollow(root, tempRel, perm); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return CreatedDir{}, false, mapWorkspaceRootPathError(err)
		}

		tempInfo, statErr := root.Lstat(tempRel)
		if statErr != nil || tempInfo == nil || !tempInfo.IsDir() || tempInfo.Mode()&os.ModeSymlink != 0 {
			return CreatedDir{}, false, errors.Join(errWorkspaceCreatedDirOwnershipChanged, statErr)
		}
		handle, openErr := rootio.OpenDirNoFollow(root, tempRel)
		if openErr != nil {
			// No identity handle is available, so the uncertain path must be
			// preserved instead of deleting an object that may be a replacement.
			return CreatedDir{}, false, errors.Join(errWorkspaceCreatedDirOwnershipChanged, openErr)
		}
		heldInfo, heldStatErr := handle.Stat()
		if heldStatErr != nil || !heldInfo.IsDir() || heldInfo.Mode()&os.ModeSymlink != 0 ||
			!os.SameFile(tempInfo, heldInfo) || tempInfo.Mode() != heldInfo.Mode() {
			_ = handle.Close()
			return CreatedDir{}, false, errors.Join(errWorkspaceCreatedDirOwnershipChanged, heldStatErr)
		}

		created := CreatedDir{
			Path:               filepath.Join(rootPath, filepath.FromSlash(tempRel)),
			relativePath:       tempRel,
			info:               heldInfo,
			persistentIdentity: PersistentIdentityTokenForFileInfo(heldInfo),
			mode:               heldInfo.Mode(),
			handle:             handle,
		}
		targetPath := filepath.Join(rootPath, filepath.FromSlash(currentRel))
		if err := afterWorkspaceCreatedDirTempOpen(created.Path, targetPath); err != nil {
			return CreatedDir{}, false, cleanupUnpublishedWorkspaceCreatedDir(root, created, err)
		}
		if _, err := readWorkspaceCreatedDirEntry(root, created); err != nil {
			return CreatedDir{}, false, cleanupUnpublishedWorkspaceCreatedDir(root, created, err)
		}

		renameErr := rootio.RenameLeafNoReplace(root, tempRel, currentRel)
		if renameErr != nil {
			cleanupErr := cleanupUnpublishedWorkspaceCreatedDir(root, created, nil)
			if errors.Is(renameErr, os.ErrExist) && cleanupErr == nil {
				info, statErr := root.Lstat(currentRel)
				if statErr == nil && info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
					return CreatedDir{}, false, nil
				}
				if statErr == nil {
					if info.Mode()&os.ModeSymlink != 0 {
						return CreatedDir{}, false, ErrNotFound
					}
					return CreatedDir{}, false, ErrNotDir
				}
				return CreatedDir{}, false, mapWorkspaceRootPathError(statErr)
			}
			return CreatedDir{}, false, errors.Join(mapWorkspaceRootPathError(renameErr), cleanupErr)
		}

		created.Path = targetPath
		created.relativePath = currentRel
		hookErr := afterWorkspaceCreatedDirPublish(targetPath)
		publishedInfo, verifyErr := readWorkspaceCreatedDirEntry(root, created)
		if verifyErr != nil {
			// The target no longer names the held object. Preserve both the
			// current target and the displaced object, and return the held
			// evidence so the transaction owner can classify the failure.
			return created, true, errors.Join(hookErr, verifyErr)
		}
		created.info = publishedInfo
		created.persistentIdentity = PersistentIdentityTokenForFileInfo(publishedInfo)
		created.mode = publishedInfo.Mode()
		if hookErr != nil {
			return created, true, hookErr
		}
		return created, true, nil
	}

	return CreatedDir{}, false, errors.New("failed to allocate unique workspace directory")
}

func ensureWorkspaceDirsTracked(rootPath string, root *os.Root, relDir string, perm os.FileMode) ([]CreatedDir, error) {
	cleanRel := path.Clean(strings.ReplaceAll(relDir, "\\", "/"))
	if cleanRel == "." || cleanRel == "/" {
		return nil, nil
	}
	if strings.HasPrefix(cleanRel, "../") || cleanRel == ".." {
		return nil, ErrNotFound
	}

	createdDirs := make([]CreatedDir, 0)
	currentRel := "."
	for _, part := range strings.Split(cleanRel, "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return createdDirs, ErrNotFound
		}
		if currentRel == "." {
			currentRel = part
		} else {
			currentRel = path.Join(currentRel, part)
		}

		info, err := root.Lstat(currentRel)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return createdDirs, ErrNotFound
			}
			if !info.IsDir() {
				return createdDirs, ErrNotDir
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			mappedErr := mapWorkspaceRootPathError(err)
			return createdDirs, mappedErr
		}

		created, published, err := publishWorkspaceCreatedDir(rootPath, root, currentRel, perm)
		if published {
			createdDirs = append([]CreatedDir{created}, createdDirs...)
		}
		if err != nil {
			return createdDirs, err
		}
	}

	return createdDirs, nil
}

func normalizeWorkspaceRootPath(root string) (string, error) {
	cleaned := filepath.Clean(root)
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}

	absRoot, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	return absRoot, nil
}

func validateWorkspaceRootComponent(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errWorkspaceRootSymlink
	}
	if !info.IsDir() {
		return ErrNotDir
	}
	return nil
}

func validateWorkspaceRootPath(root string) error {
	current := filepath.VolumeName(root) + string(filepath.Separator)
	trimmed := strings.TrimPrefix(root, current)
	if trimmed == "" {
		return validateWorkspaceRootComponent(root)
	}

	if err := validateWorkspaceRootComponent(current); err != nil {
		return err
	}
	for _, part := range strings.Split(trimmed, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if err := validateWorkspaceRootComponent(current); err != nil {
			return err
		}
	}
	return nil
}

func ensureWorkspaceRoot(root string) (string, *os.Root, error) {
	normalizedRoot, err := normalizeWorkspaceRootPath(root)
	if err != nil {
		return "", nil, err
	}
	if err := validateWorkspaceRootPath(normalizedRoot); err != nil {
		return "", nil, err
	}

	createdDirs, err := rootio.MkdirAllPathNoFollowTracked(normalizedRoot, 0755)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return "", nil, errWorkspaceRootSymlink
		}
		return "", nil, err
	}
	if err := syncCreatedWorkspaceDirs(createdDirs); err != nil {
		return "", nil, fmt.Errorf("failed to sync directory: %w", err)
	}
	if err := validateWorkspaceRootPath(normalizedRoot); err != nil {
		return "", nil, err
	}

	rootHandle, err := os.OpenRoot(normalizedRoot)
	if err != nil {
		return "", nil, err
	}
	return normalizedRoot, rootHandle, nil
}

// Common errors
var (
	ErrNotFound             = errors.New("not found")
	ErrAlreadyExists        = errors.New("already exists")
	ErrNotDir               = errors.New("not a directory")
	ErrIsDir                = errors.New("is a directory")
	ErrNotRegular           = errors.New("not a regular file")
	ErrFileTooLarge         = errors.New("file too large")
	errWorkspaceRootSymlink = errors.New("workspace root must not be a symlink")
)

type WriteFileOptions struct {
	MaxBytes          int64
	SyncParent        func(string) error
	CreatedDirs       *[]CreatedDir
	Mode              *os.FileMode
	PublishNoReplace  bool
	BeforePublish     func() error
	PublishedIdentity *string
}

// VisibleMutationWarningError reports that a workspace mutation is already
// externally visible, but the final directory fsync did not complete.
type VisibleMutationWarningError struct {
	err error
}

func (e *VisibleMutationWarningError) Error() string {
	return e.err.Error()
}

func (e *VisibleMutationWarningError) Unwrap() error {
	return e.err
}

func WrapVisibleMutationWarning(err error) error {
	if err == nil {
		return nil
	}
	var warningErr *VisibleMutationWarningError
	if errors.As(err, &warningErr) {
		return err
	}
	return &VisibleMutationWarningError{err: err}
}

func IsVisibleMutationWarning(err error) bool {
	var warningErr *VisibleMutationWarningError
	return errors.As(err, &warningErr)
}

// FileInfo represents file metadata
type FileInfo struct {
	Path                string
	Name                string
	IsDir               bool
	Mode                os.FileMode
	Size                int64
	ModTime             time.Time
	DeleteIdentityToken string
}

// Workspace provides native file operations on a root directory
type Workspace struct {
	root       string
	rootHandle *os.Root
}

// New creates a new Workspace with the given root directory
func New(root string) (*Workspace, error) {
	absRoot, rootHandle, err := ensureWorkspaceRoot(root)
	if err != nil {
		return nil, err
	}

	return &Workspace{root: absRoot, rootHandle: rootHandle}, nil
}

func (w *Workspace) validatePath(fullPath string) error {
	cleanRoot := filepath.Clean(w.root)
	cleanPath := filepath.Clean(fullPath)
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return ErrNotFound
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return ErrNotFound
	}

	current := cleanRoot
	if err := validateWorkspaceComponent(current); err != nil {
		return err
	}
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if err := validateWorkspaceComponent(current); err != nil {
			return err
		}
	}
	return nil
}

func validateWorkspaceComponent(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrNotFound
	}
	return nil
}

func workspaceRootRelativeName(name string) string {
	cleanName := strings.TrimPrefix(CleanPath(name), "/")
	if cleanName == "" {
		return "."
	}
	return cleanName
}

func workspaceParentRelativeName(name string) string {
	return workspaceRootRelativeName(path.Dir(CleanPath(name)))
}

func childWorkspaceRootName(parent, child string) string {
	if parent == "." {
		return child
	}
	return path.Join(parent, child)
}

func workspaceDirEntryName(entry os.DirEntry) (string, error) {
	if entry == nil {
		return "", ErrNotFound
	}
	name := entry.Name()
	normalized := strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.Contains(normalized, "/") {
		return "", ErrNotFound
	}
	if err := validateWorkspaceName(name); err != nil {
		return "", err
	}
	return name, nil
}

func isWorkspaceRootEscapeError(err error) bool {
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		return false
	}
	return pathErr.Err != nil && pathErr.Err.Error() == workspaceRootEscapeError
}

func mapWorkspaceRootPathError(err error) error {
	if err == nil {
		return nil
	}
	if IsCreatedDirOwnershipChanged(err) {
		return err
	}
	if isWorkspaceRootEscapeError(err) || rootio.IsSymlinkError(err) || errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if errors.Is(err, syscall.ENOTDIR) {
		return ErrNotDir
	}
	return err
}

func (w *Workspace) mapWorkspaceCreatePathError(fullPath string, err error) error {
	if mappedErr := mapWorkspaceRootPathError(err); mappedErr != err {
		return mappedErr
	}
	if errors.Is(err, os.ErrExist) {
		if validateErr := w.validatePath(fullPath); validateErr != nil {
			return validateErr
		}
	}
	return err
}

func newWorkspaceTempName(parentName string) (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	tempName := ".workspace-" + hex.EncodeToString(suffix[:]) + ".tmp"
	if parentName == "." {
		return tempName, nil
	}
	return path.Join(parentName, tempName), nil
}

func createWorkspaceTempFile(root *os.Root, parentName string) (*os.File, string, error) {
	for range 32 {
		tempName, err := newWorkspaceTempName(parentName)
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
		return nil, "", mapWorkspaceRootPathError(err)
	}

	return nil, "", errors.New("failed to allocate unique temp file")
}

// Close releases the workspace root handle.
func (w *Workspace) Close() error {
	if w == nil || w.rootHandle == nil {
		return nil
	}
	if err := w.rootHandle.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return err
	}
	return nil
}

// Root returns the workspace root directory
func (w *Workspace) Root() string {
	return w.root
}

// FullPath returns the full filesystem path for a workspace path
func (w *Workspace) FullPath(name string) string {
	name = CleanPath(name)
	if name == "/" {
		return w.root
	}
	return filepath.Join(w.root, name)
}

// CleanPath normalizes a path for use within the workspace
func CleanPath(name string) string {
	// Normalize path separators
	name = strings.ReplaceAll(name, "\\", "/")

	// Keep paths rooted inside the workspace while preserving valid names like foo..txt.
	return path.Clean("/" + name)
}

func validateWorkspaceName(name string) error {
	normalized := strings.ReplaceAll(name, "\\", "/")
	if strings.IndexFunc(normalized, unicode.IsControl) >= 0 {
		return ErrNotFound
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "." || segment == ".." {
			return ErrNotFound
		}
	}
	return nil
}

func validateWorkspaceMutationName(name string) error {
	if err := validateWorkspaceName(name); err != nil {
		return err
	}
	if CleanPath(name) == "/" {
		return ErrNotFound
	}
	return nil
}

// Stat returns file information
func (w *Workspace) Stat(ctx context.Context, name string) (*FileInfo, error) {
	if err := validateWorkspaceName(name); err != nil {
		return nil, err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return nil, err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return nil, err
	}

	info, err := w.rootHandle.Lstat(workspaceRootRelativeName(name))
	if err != nil {
		return nil, mapWorkspaceRootPathError(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrNotFound
	}

	return &FileInfo{
		Path:                CleanPath(name),
		Name:                info.Name(),
		IsDir:               info.IsDir(),
		Mode:                info.Mode(),
		Size:                info.Size(),
		ModTime:             info.ModTime(),
		DeleteIdentityToken: deleteIdentityToken(info),
	}, nil
}

// ReadDir lists directory contents
func (w *Workspace) ReadDir(ctx context.Context, name string) ([]*FileInfo, error) {
	return w.readDir(ctx, name, -1)
}

// ReadDirLimit lists at most limit visible directory entries without loading
// the complete directory into memory. A positive limit is required.
func (w *Workspace) ReadDirLimit(ctx context.Context, name string, limit int) ([]*FileInfo, error) {
	if limit <= 0 {
		return nil, errors.New("directory read limit must be positive")
	}
	return w.readDir(ctx, name, limit)
}

func (w *Workspace) readDir(ctx context.Context, name string, limit int) ([]*FileInfo, error) {
	if err := validateWorkspaceName(name); err != nil {
		return nil, err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return nil, err
	}

	dirHandle, err := rootio.OpenDirNoFollow(w.rootHandle, workspaceRootRelativeName(name))
	if err != nil {
		return nil, mapWorkspaceRootPathError(err)
	}
	defer dirHandle.Close()

	dirInfo, err := dirHandle.Stat()
	if err != nil {
		return nil, err
	}
	if !dirInfo.IsDir() {
		return nil, ErrNotDir
	}

	capacity := 0
	if limit > 0 {
		capacity = limit
	}
	result := make([]*FileInfo, 0, capacity)
	for limit < 0 || len(result) < limit {
		readCount := -1
		if limit > 0 {
			readCount = limit - len(result)
		}
		entries, readErr := dirHandle.ReadDir(readCount)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, mapWorkspaceRootPathError(readErr)
		}
		for _, e := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			entryName, err := workspaceDirEntryName(e)
			if err != nil {
				return nil, err
			}
			childRootName := childWorkspaceRootName(workspaceRootRelativeName(name), entryName)
			info, err := readDirEntryInfo(w.rootHandle, childRootName, e)
			if err != nil {
				return nil, mapWorkspaceRootPathError(err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}

			childPath := path.Join(CleanPath(name), entryName)
			result = append(result, &FileInfo{
				Path:                childPath,
				Name:                entryName,
				IsDir:               info.IsDir(),
				Mode:                info.Mode(),
				Size:                info.Size(),
				ModTime:             info.ModTime(),
				DeleteIdentityToken: deleteIdentityToken(info),
			})
		}
		if errors.Is(readErr, io.EOF) || len(entries) == 0 || readCount < 0 {
			break
		}
	}

	return result, nil
}

// OpenFile opens a file for reading
func (w *Workspace) OpenFile(ctx context.Context, name string) (*os.File, error) {
	if err := validateWorkspaceName(name); err != nil {
		return nil, err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return nil, err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return nil, err
	}

	fileHandle, err := rootio.OpenFileNoFollow(w.rootHandle, workspaceRootRelativeName(name), os.O_RDONLY, 0)
	if err != nil {
		return nil, mapWorkspaceRootPathError(err)
	}

	info, err := fileHandle.Stat()
	if err != nil {
		_ = fileHandle.Close()
		return nil, err
	}
	if info.IsDir() {
		_ = fileHandle.Close()
		return nil, ErrIsDir
	}

	return fileHandle, nil
}

// OpenRegularFile opens a regular file for reading without blocking on
// special files such as FIFOs.
func (w *Workspace) OpenRegularFile(ctx context.Context, name string) (*os.File, error) {
	if err := validateWorkspaceName(name); err != nil {
		return nil, err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return nil, err
	}

	fileHandle, err := rootio.OpenRegularFileNoFollow(w.rootHandle, workspaceRootRelativeName(name))
	if err != nil {
		mappedErr := mapWorkspaceRootPathError(err)
		if mappedErr != err {
			return nil, mappedErr
		}
		info, statErr := w.rootHandle.Lstat(workspaceRootRelativeName(name))
		if statErr == nil && info.Mode()&os.ModeSymlink == 0 {
			if info.IsDir() {
				return nil, ErrIsDir
			}
			if !info.Mode().IsRegular() {
				return nil, ErrNotRegular
			}
		}
		if errors.Is(err, syscall.EINVAL) {
			return nil, ErrNotRegular
		}
		return nil, err
	}
	return fileHandle, nil
}

// ReadFile reads entire file content
func (w *Workspace) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := validateWorkspaceName(name); err != nil {
		return nil, err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return nil, err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return nil, err
	}

	fileHandle, err := rootio.OpenFileNoFollow(w.rootHandle, workspaceRootRelativeName(name), os.O_RDONLY, 0)
	if err != nil {
		return nil, mapWorkspaceRootPathError(err)
	}
	defer fileHandle.Close()

	info, err := fileHandle.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, ErrIsDir
	}

	return io.ReadAll(fileHandle)
}

// WriteFile writes data to a file, creating parent directories as needed
func (w *Workspace) WriteFile(ctx context.Context, name string, data []byte) error {
	_, err := w.WriteFileFromReaderWithOptions(ctx, name, bytes.NewReader(data), WriteFileOptions{})
	return err
}

func (w *Workspace) WriteFileFromReaderWithOptions(ctx context.Context, name string, r io.Reader, options WriteFileOptions) (written int64, resultErr error) {
	if options.CreatedDirs != nil {
		if err := ReleaseCreatedDirs(*options.CreatedDirs); err != nil {
			return 0, err
		}
		*options.CreatedDirs = (*options.CreatedDirs)[:0]
	}
	if err := validateWorkspaceMutationName(name); err != nil {
		return 0, err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return 0, err
	}

	rootName := workspaceRootRelativeName(name)
	parentName := workspaceParentRelativeName(name)

	// Ensure parent directory exists
	createdDirs, err := ensureWorkspaceDirsTracked(w.root, w.rootHandle, parentName, 0755)
	if options.CreatedDirs != nil {
		*options.CreatedDirs = append((*options.CreatedDirs)[:0], createdDirs...)
	}
	cleanupCreatedDirs := func(operationErr error) error {
		return cleanupWorkspaceCreatedDirsWithRetention(
			w.root,
			w.rootHandle,
			createdDirs,
			operationErr,
			options.CreatedDirs != nil,
		)
	}
	if err != nil {
		return 0, w.mapWorkspaceCreatePathError(filepath.Dir(fullPath), cleanupCreatedDirs(err))
	}
	mutationVisible := false
	if options.CreatedDirs == nil {
		defer func() {
			releaseErr := ReleaseCreatedDirs(createdDirs)
			if releaseErr == nil {
				return
			}
			if mutationVisible {
				releaseErr = WrapVisibleMutationWarning(releaseErr)
			}
			resultErr = errors.Join(resultErr, releaseErr)
		}()
	}

	// Atomic write: write to temp file then rename
	tmpFile, tmpPath, err := createWorkspaceTempFile(w.rootHandle, parentName)
	if err != nil {
		return 0, cleanupCreatedDirs(err)
	}
	ownedTempInfo, err := tmpFile.Stat()
	if err != nil {
		closeErr := tmpFile.Close()
		return 0, cleanupCreatedDirs(errors.Join(err, closeErr))
	}
	mode := os.FileMode(0644)
	if options.Mode != nil {
		mode = *options.Mode
	}
	if err := tmpFile.Chmod(mode); err != nil {
		_ = tmpFile.Close()
		return 0, cleanupCreatedDirs(cleanupOwnedWorkspaceTempPath(w.rootHandle, tmpPath, ownedTempInfo, err))
	}

	copyReader := r
	if options.MaxBytes > 0 {
		copyReader = &io.LimitedReader{R: r, N: options.MaxBytes + 1}
	}

	written, writeErr := copyWorkspaceData(ctx, tmpFile, copyReader)
	syncErr := tmpFile.Sync()
	publishedIdentity := ""
	var publishSnapshot ownedWorkspaceFileSnapshot
	var tempStatErr error
	if writeErr == nil && syncErr == nil {
		var finalTempInfo os.FileInfo
		finalTempInfo, tempStatErr = tmpFile.Stat()
		if tempStatErr == nil {
			ownedTempInfo = finalTempInfo
			publishSnapshot = newOwnedWorkspaceFileSnapshot(finalTempInfo)
		}
		if tempStatErr == nil && options.PublishedIdentity != nil {
			publishedIdentity = publishSnapshot.persistentIdentity
			if publishedIdentity == "" {
				tempStatErr = errors.New("published file identity is unavailable")
			}
		}
	}
	closeErr := tmpFile.Close()

	if writeErr != nil {
		return written, cleanupCreatedDirs(cleanupOwnedWorkspaceTempPath(w.rootHandle, tmpPath, ownedTempInfo, writeErr))
	}
	if syncErr != nil {
		return written, cleanupCreatedDirs(cleanupOwnedWorkspaceTempPath(w.rootHandle, tmpPath, ownedTempInfo, syncErr))
	}
	if tempStatErr != nil {
		return written, cleanupCreatedDirs(cleanupOwnedWorkspaceTempPath(w.rootHandle, tmpPath, ownedTempInfo, tempStatErr))
	}
	if closeErr != nil {
		return written, cleanupCreatedDirs(cleanupOwnedWorkspaceTempPath(w.rootHandle, tmpPath, ownedTempInfo, closeErr))
	}
	if options.MaxBytes > 0 && written > options.MaxBytes {
		return written, cleanupCreatedDirs(cleanupOwnedWorkspaceTempPath(w.rootHandle, tmpPath, ownedTempInfo, ErrFileTooLarge))
	}
	if err := ctx.Err(); err != nil {
		return written, cleanupCreatedDirs(cleanupOwnedWorkspaceTempPath(w.rootHandle, tmpPath, ownedTempInfo, err))
	}
	if options.BeforePublish != nil {
		if err := options.BeforePublish(); err != nil {
			return written, cleanupCreatedDirs(cleanupOwnedWorkspaceTempPath(w.rootHandle, tmpPath, ownedTempInfo, err))
		}
		if err := ctx.Err(); err != nil {
			return written, cleanupCreatedDirs(cleanupOwnedWorkspaceTempPath(w.rootHandle, tmpPath, ownedTempInfo, err))
		}
	}
	if err := verifyOwnedWorkspaceFile(w.rootHandle, tmpPath, publishSnapshot, true); err != nil {
		verifyErr := fmt.Errorf("verify temp file ownership before publish: %w", err)
		return written, cleanupCreatedDirs(cleanupOwnedWorkspaceTempPath(w.rootHandle, tmpPath, ownedTempInfo, verifyErr))
	}

	var renameErr error
	if options.PublishNoReplace {
		renameErr = rootio.RenameLeafNoReplace(w.rootHandle, tmpPath, rootName)
	} else {
		renameErr = w.rootHandle.Rename(tmpPath, rootName)
	}
	if renameErr != nil {
		if options.PublishNoReplace && errors.Is(renameErr, os.ErrExist) {
			return written, cleanupCreatedDirs(cleanupOwnedWorkspaceTempPath(w.rootHandle, tmpPath, ownedTempInfo, ErrAlreadyExists))
		}
		if mappedErr := mapWorkspaceRootPathError(renameErr); mappedErr != renameErr {
			return written, cleanupCreatedDirs(cleanupOwnedWorkspaceTempPath(w.rootHandle, tmpPath, ownedTempInfo, mappedErr))
		}
		return written, cleanupCreatedDirs(cleanupOwnedWorkspaceTempPath(w.rootHandle, tmpPath, ownedTempInfo, renameErr))
	}
	mutationVisible = true
	if options.PublishedIdentity != nil {
		*options.PublishedIdentity = publishedIdentity
	}
	// Renaming may update ctime. Establish a new deletion-identity baseline
	// immediately after the namespace mutation, before any later work can
	// modify the published object in place.
	postRenameSnapshot, err := readOwnedWorkspaceFileSnapshot(w.rootHandle, rootName, publishSnapshot, false)
	if err != nil {
		return written, WrapVisibleMutationWarning(fmt.Errorf("verify published file ownership after rename: %w", err))
	}
	hookErr := afterWorkspaceFilePublish()
	verifyErr := verifyOwnedWorkspaceFile(w.rootHandle, rootName, postRenameSnapshot, true)
	if hookErr != nil || verifyErr != nil {
		var hookWarning error
		if hookErr != nil {
			hookWarning = fmt.Errorf("after publishing file: %w", hookErr)
		}
		var verifyWarning error
		if verifyErr != nil {
			verifyWarning = fmt.Errorf("verify published file ownership: %w", verifyErr)
		}
		return written, WrapVisibleMutationWarning(errors.Join(hookWarning, verifyWarning))
	}
	syncParent := options.SyncParent
	if syncParent == nil {
		syncParent = syncWorkspaceDir
	}
	if err := syncParent(filepath.Dir(fullPath)); err != nil {
		return written, WrapVisibleMutationWarning(fmt.Errorf("sync parent directory: %w", err))
	}
	if err := syncCreatedWorkspaceDirEvidenceWith(createdDirs, syncParent); err != nil {
		return written, WrapVisibleMutationWarning(fmt.Errorf("sync created directory tree: %w", err))
	}

	return written, nil
}

// WriteFileFromReader writes data from a reader to a file
func (w *Workspace) WriteFileFromReader(ctx context.Context, name string, r io.Reader) error {
	_, err := w.WriteFileFromReaderWithOptions(ctx, name, r, WriteFileOptions{})
	return err
}

// Mkdir creates a directory
func (w *Workspace) Mkdir(ctx context.Context, name string) error {
	if err := validateWorkspaceMutationName(name); err != nil {
		return err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return err
	}

	rootName := workspaceRootRelativeName(name)

	// Check if already exists
	info, err := w.rootHandle.Lstat(rootName)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrNotFound
		}
		if info.IsDir() {
			return ErrAlreadyExists
		}
		return ErrNotDir
	}
	if errors.Is(err, syscall.ENOTDIR) {
		return ErrNotDir
	}
	if isWorkspaceRootEscapeError(err) {
		return ErrNotFound
	}

	createdRelDirs, err := rootio.MkdirAllNoFollowTracked(w.rootHandle, rootName, 0755)
	if err != nil {
		return w.mapWorkspaceCreatePathError(fullPath, err)
	}
	createdDirs := make([]string, len(createdRelDirs))
	for i, relDir := range createdRelDirs {
		createdDirs[i] = filepath.Join(w.root, relDir)
	}
	if err := syncCreatedWorkspaceDirs(createdDirs); err != nil {
		return WrapVisibleMutationWarning(fmt.Errorf("failed to sync directory: %w", err))
	}

	return nil
}

// Delete removes a file or empty directory
func (w *Workspace) Delete(ctx context.Context, name string) error {
	if err := validateWorkspaceMutationName(name); err != nil {
		return err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return err
	}

	rootName := workspaceRootRelativeName(name)

	info, err := w.rootHandle.Lstat(rootName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrNotFound
	}

	if info.IsDir() {
		if err := w.rootHandle.Remove(rootName); err != nil {
			return mapWorkspaceRootPathError(err)
		}
		if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
			return WrapVisibleMutationWarning(fmt.Errorf("failed to sync directory: %w", err))
		}
		return nil
	}

	if err := w.rootHandle.Remove(rootName); err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
		return WrapVisibleMutationWarning(fmt.Errorf("failed to sync directory: %w", err))
	}
	return nil
}

// DeleteAll removes a file or directory recursively
func (w *Workspace) DeleteAll(ctx context.Context, name string) error {
	if err := validateWorkspaceMutationName(name); err != nil {
		return err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return err
	}

	rootName := workspaceRootRelativeName(name)

	info, err := w.rootHandle.Lstat(rootName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrNotFound
	}

	if err := rootio.RemoveAllNoFollow(w.rootHandle, rootName); err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
		return WrapVisibleMutationWarning(fmt.Errorf("failed to sync directory: %w", err))
	}
	return nil
}

// Rename moves or renames a file or directory
func (w *Workspace) Rename(ctx context.Context, oldName, newName string) error {
	if err := validateWorkspaceMutationName(oldName); err != nil {
		return err
	}
	if err := validateWorkspaceMutationName(newName); err != nil {
		return err
	}
	oldPath := w.FullPath(oldName)
	newPath := w.FullPath(newName)
	if err := w.validatePath(oldPath); err != nil {
		return err
	}
	if err := w.validatePath(newPath); err != nil {
		return err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return err
	}

	oldRootName := workspaceRootRelativeName(oldName)
	newRootName := workspaceRootRelativeName(newName)

	// Check source exists
	oldInfo, err := w.rootHandle.Lstat(oldRootName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if oldInfo.Mode()&os.ModeSymlink != 0 {
		return ErrNotFound
	}

	if newInfo, err := w.rootHandle.Lstat(newRootName); err == nil {
		if newInfo.Mode()&os.ModeSymlink != 0 {
			return ErrNotFound
		}
		return ErrAlreadyExists
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) && !isWorkspaceRootEscapeError(err) {
		return err
	}

	parentInfo, err := w.rootHandle.Lstat(workspaceParentRelativeName(newName))
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 {
		return ErrNotFound
	}
	if !parentInfo.IsDir() {
		return ErrNotDir
	}
	if err := beforeWorkspaceRename(); err != nil {
		return err
	}

	if err := rootio.RenamePathIntoDirNoFollow(oldPath, filepath.Dir(newPath), filepath.Base(newPath)); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrAlreadyExists
		}
		return mapWorkspaceRootPathError(err)
	}
	if err := syncWorkspaceRenameDirs(oldPath, newPath); err != nil {
		if rollbackErr := rootio.RenamePathIntoDirNoFollow(newPath, filepath.Dir(oldPath), filepath.Base(oldPath)); rollbackErr != nil {
			return WrapVisibleMutationWarning(errors.Join(
				fmt.Errorf("failed to sync directory: %w", err),
				fmt.Errorf("failed to rollback renamed path: %w", rollbackErr),
			))
		}
		if rollbackSyncErr := syncWorkspaceRenameDirs(newPath, oldPath); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("failed to sync directory: %w", err),
				fmt.Errorf("failed to sync rollback directory: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("failed to sync directory: %w", err)
	}

	return nil
}

// Copy copies a file
func (w *Workspace) Copy(ctx context.Context, srcName, dstName string) error {
	if err := validateWorkspaceMutationName(srcName); err != nil {
		return err
	}
	if err := validateWorkspaceMutationName(dstName); err != nil {
		return err
	}
	srcPath := w.FullPath(srcName)
	dstPath := w.FullPath(dstName)
	if err := w.validatePath(srcPath); err != nil {
		return err
	}
	if err := w.validatePath(dstPath); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return err
	}

	srcRootName := workspaceRootRelativeName(srcName)
	dstRootName := workspaceRootRelativeName(dstName)
	dstParentName := workspaceParentRelativeName(dstName)

	// Check source exists and is a file
	srcFile, err := rootio.OpenFileNoFollow(w.rootHandle, srcRootName, os.O_RDONLY, 0)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}
	if srcInfo.IsDir() {
		return ErrIsDir
	}

	parentInfo, err := w.rootHandle.Lstat(dstParentName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 {
		return ErrNotFound
	}
	if !parentInfo.IsDir() {
		return ErrNotDir
	}

	// Copy file
	dstFile, tmpPath, err := createWorkspaceTempFile(w.rootHandle, dstParentName)
	if err != nil {
		return err
	}
	if err := dstFile.Chmod(0644); err != nil {
		_ = dstFile.Close()
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, err)
	}

	_, copyErr := copyWorkspaceData(ctx, dstFile, srcFile)
	syncErr := dstFile.Sync()
	closeErr := dstFile.Close()

	if copyErr != nil {
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, copyErr)
	}
	if syncErr != nil {
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, syncErr)
	}
	if closeErr != nil {
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, closeErr)
	}

	if err := w.rootHandle.Link(tmpPath, dstRootName); err != nil {
		if errors.Is(err, os.ErrExist) {
			return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, ErrAlreadyExists)
		}
		if mappedErr := mapWorkspaceRootPathError(err); mappedErr != err {
			return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, mappedErr)
		}
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, err)
	}

	var copyWarning error
	if err := finalizeWorkspaceCopyTemp(w.rootHandle, tmpPath); err != nil {
		if cleanupErr := removeWorkspaceTempPath(w.rootHandle, tmpPath); cleanupErr != nil {
			copyWarning = fmt.Errorf("failed to finalize copied file: %w", errors.Join(err, fmt.Errorf("cleanup temp file %s: %w", tmpPath, cleanupErr)))
		}
	}
	if err := syncWorkspaceDir(filepath.Dir(dstPath)); err != nil {
		if copyWarning != nil {
			return WrapVisibleMutationWarning(errors.Join(copyWarning, fmt.Errorf("sync parent directory: %w", err)))
		}
		if rollbackErr := w.rootHandle.Remove(dstRootName); rollbackErr != nil {
			return WrapVisibleMutationWarning(errors.Join(
				fmt.Errorf("sync parent directory: %w", err),
				fmt.Errorf("failed to rollback copied file: %w", rollbackErr),
			))
		}
		if rollbackSyncErr := syncWorkspaceDir(filepath.Dir(dstPath)); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("sync parent directory: %w", err),
				fmt.Errorf("failed to sync rollback copy removal: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("sync parent directory: %w", err)
	}
	if copyWarning != nil {
		return WrapVisibleMutationWarning(copyWarning)
	}

	return nil
}

// WalkFunc is the type of function called by Walk
type WalkFunc func(path string, info *FileInfo) error

type workspaceWalkInternalFunc func(rootName, cleanPath string, info os.FileInfo) error

func (w *Workspace) walkWithRootHandle(ctx context.Context, rootName, cleanPath string, rejectNonRegular bool, fn workspaceWalkInternalFunc) error {
	entryInfo, err := w.rootHandle.Lstat(rootName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if entryInfo.Mode()&os.ModeSymlink != 0 {
		if !rejectNonRegular {
			return ErrNotFound
		}
	}

	return w.walkWorkspaceEntry(ctx, rootName, cleanPath, entryInfo, rejectNonRegular, fn)
}

func (w *Workspace) walkWorkspaceEntry(ctx context.Context, rootName, cleanPath string, info os.FileInfo, rejectNonRegular bool, fn workspaceWalkInternalFunc) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := fn(rootName, cleanPath, info); err != nil {
		return err
	}
	if rejectNonRegular && !info.IsDir() && !info.Mode().IsRegular() {
		return ErrNotRegular
	}
	if !info.IsDir() {
		return nil
	}

	dirHandle, err := rootio.OpenDirNoFollow(w.rootHandle, rootName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	defer dirHandle.Close()

	entries, err := dirHandle.ReadDir(-1)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}

		entryName, err := workspaceDirEntryName(entry)
		if err != nil {
			return err
		}
		childRootName := childWorkspaceRootName(rootName, entryName)
		entryInfo, err := readDirEntryInfo(w.rootHandle, childRootName, entry)
		if err != nil {
			return mapWorkspaceRootPathError(err)
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 && !rejectNonRegular {
			continue
		}

		childCleanPath := path.Join(cleanPath, entryName)
		if err := w.walkWorkspaceEntry(ctx, childRootName, childCleanPath, entryInfo, rejectNonRegular, fn); err != nil {
			return err
		}
	}

	return nil
}

func (w *Workspace) walk(ctx context.Context, root string, rejectNonRegular bool, fn WalkFunc) error {
	if err := validateWorkspaceName(root); err != nil {
		return err
	}
	rootPath := w.FullPath(root)
	rootClean := CleanPath(root)
	validatePath := rootPath
	if rejectNonRegular {
		validatePath = w.FullPath(path.Dir(rootClean))
	}
	if err := w.validatePath(validatePath); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return err
	}

	return w.walkWithRootHandle(ctx, workspaceRootRelativeName(root), rootClean, rejectNonRegular, func(_ string, cleanPath string, info os.FileInfo) error {
		return fn(cleanPath, &FileInfo{
			Path:                cleanPath,
			Name:                info.Name(),
			IsDir:               info.IsDir(),
			Mode:                info.Mode(),
			Size:                info.Size(),
			ModTime:             info.ModTime(),
			DeleteIdentityToken: deleteIdentityToken(info),
		})
	})
}

// Walk walks the file tree rooted at root, calling fn for each non-symlink
// file or directory.
func (w *Workspace) Walk(ctx context.Context, root string, fn WalkFunc) error {
	return w.walk(ctx, root, false, fn)
}

// WalkStrict visits every entry without following symlinks and rejects
// symlinks or other non-regular, non-directory entries after invoking fn.
func (w *Workspace) WalkStrict(ctx context.Context, root string, fn WalkFunc) error {
	return w.walk(ctx, root, true, fn)
}

// CleanupStaging removes incomplete workspace staging files.
func (w *Workspace) CleanupStaging(ctx context.Context) (files int, bytes int64, err error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return 0, 0, err
	}

	type stagingFile struct {
		rootName string
		size     int64
	}
	stagingFiles := make([]stagingFile, 0)
	err = w.walkWithRootHandle(ctx, ".", "/", false, func(rootName, _ string, info os.FileInfo) error {
		if info.IsDir() || !isWorkspaceStagingFile(info.Name()) {
			return nil
		}
		stagingFiles = append(stagingFiles, stagingFile{rootName: rootName, size: info.Size()})
		return nil
	})
	if err != nil {
		return 0, 0, err
	}

	for _, stagingFile := range stagingFiles {
		if err := ctx.Err(); err != nil {
			return files, bytes, err
		}
		if rmErr := w.rootHandle.Remove(stagingFile.rootName); rmErr == nil {
			files++
			bytes += stagingFile.size
		}
	}

	return files, bytes, err
}

func isWorkspaceStagingFile(name string) bool {
	return strings.HasPrefix(name, ".workspace-") && strings.HasSuffix(name, ".tmp")
}

// Exists checks if a path exists
func (w *Workspace) Exists(ctx context.Context, name string) bool {
	if err := validateWorkspaceName(name); err != nil {
		return false
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return false
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return false
	}
	info, err := w.rootHandle.Lstat(workspaceRootRelativeName(name))
	return err == nil && info.Mode()&os.ModeSymlink == 0
}

// IsDir checks if a path is a directory
func (w *Workspace) IsDir(ctx context.Context, name string) bool {
	if err := validateWorkspaceName(name); err != nil {
		return false
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return false
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return false
	}
	info, err := w.rootHandle.Lstat(workspaceRootRelativeName(name))
	return err == nil && info.Mode()&os.ModeSymlink == 0 && info.IsDir()
}
