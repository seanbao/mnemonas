package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/workspace"
)

var (
	afterDeleteWitnessOpen       = func(string) error { return nil }
	afterDeleteLeafRename        = func(string, string) error { return nil }
	afterDeleteStageCapture      = func(string, string) error { return nil }
	afterDeleteTrashCopy         = func(string, string) error { return nil }
	beforeTrashRollbackCapture   = func(string, string) error { return nil }
	afterTrashRollbackCapture    = func(string, string) error { return nil }
	beforeDeleteStageRemoval     = func(string, string) error { return nil }
	afterDeleteQuarantineCapture = func(string, string) error { return nil }
)

// DeleteStageResidualError reports that an exact staged object could not be
// restored without replacing a newer object at the original path.
type DeleteStageResidualError struct {
	Path      string
	StagePath string
	err       error
}

func (e *DeleteStageResidualError) Error() string {
	if e.StagePath != "" {
		return fmt.Sprintf("delete target retained in internal staging: %s (%s)", e.Path, e.StagePath)
	}
	return fmt.Sprintf("delete target retained in internal staging: %s", e.Path)
}

func (e *DeleteStageResidualError) Unwrap() error {
	return e.err
}

type stagedDeleteTarget struct {
	logicalName   string
	originalRel   string
	stageName     string
	stageRel      string
	stageAbs      string
	witness       *os.File
	witnessInfo   os.FileInfo
	stageInfo     os.FileInfo
	stageIdentity string
	expected      DeleteTargetSnapshot
	warning       error
}

func (target *stagedDeleteTarget) close() error {
	if target == nil || target.witness == nil {
		return nil
	}
	err := target.witness.Close()
	target.witness = nil
	return err
}

func (target *stagedDeleteTarget) residual(err error) *DeleteStageResidualError {
	if target == nil {
		return &DeleteStageResidualError{err: err}
	}
	return &DeleteStageResidualError{
		Path:      target.logicalName,
		StagePath: target.stageAbs,
		err:       err,
	}
}

func storageWorkspaceRelativeName(name string) string {
	return filepath.FromSlash(strings.TrimPrefix(name, "/"))
}

func storageWorkspaceName(rel string) string {
	return "/" + filepath.ToSlash(rel)
}

func newDeleteStageRelativeName(originalRel string) (string, error) {
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	name := ".mnemonas-delete-" + hex.EncodeToString(suffix[:]) + ".stage"
	parent := filepath.Dir(originalRel)
	if parent == "." {
		return name, nil
	}
	return filepath.Join(parent, name), nil
}

func deleteStageIdentity(info os.FileInfo) string {
	if info == nil {
		return ""
	}
	return workspace.DeleteIdentityTokenForFileInfo(info)
}

func sameDeleteStageEntry(expected os.FileInfo, expectedIdentity string, actual os.FileInfo) bool {
	return expected != nil && actual != nil &&
		os.SameFile(expected, actual) &&
		expectedIdentity != "" &&
		deleteStageIdentity(actual) == expectedIdentity
}

// renameDeleteLeafNoReplace verifies the source immediately before rename and
// the destination immediately afterwards. A post-rename mismatch is reported
// with moved=true; callers must not move the observed destination again because
// it may be an out-of-band replacement rather than the renamed source.
func renameDeleteLeafNoReplace(root *os.Root, sourceRel, targetRel string, expected os.FileInfo, expectedIdentity string) (os.FileInfo, bool, error) {
	before, err := root.Lstat(sourceRel)
	if err != nil {
		return nil, false, mapStorageRootPathError(err)
	}
	if !sameDeleteStageEntry(expected, expectedIdentity, before) {
		return nil, false, ErrDeleteTargetChanged
	}
	if err := rootio.RenameLeafNoReplace(root, sourceRel, targetRel); err != nil {
		return nil, false, mapStorageRootPathError(err)
	}
	if err := afterDeleteLeafRename(sourceRel, targetRel); err != nil {
		return nil, true, err
	}
	after, err := root.Lstat(targetRel)
	if err != nil {
		return nil, true, mapStorageRootPathError(err)
	}
	if !os.SameFile(before, after) || !os.SameFile(expected, after) {
		return after, true, ErrDeleteTargetChanged
	}
	return after, true, nil
}

func (fs *FileSystem) openDeleteWitness(name string, root FileInfo) (*os.File, os.FileInfo, error) {
	rel := storageWorkspaceRelativeName(name)
	var (
		file *os.File
		err  error
	)
	if root.IsDir {
		file, err = rootio.OpenDirNoFollow(fs.filesRootHandle, rel)
	} else {
		file, err = rootio.OpenRegularFileNoFollow(fs.filesRootHandle, rel)
	}
	if err != nil {
		return nil, nil, mapWorkspaceReadablePathError(mapStorageRootPathError(err))
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	identity := workspace.DeleteIdentityTokenForFileInfo(info)
	if root.DeleteIdentityToken == "" || identity == "" {
		_ = file.Close()
		return nil, nil, ErrDeleteIdentityUnavailable
	}
	if identity != root.DeleteIdentityToken || info.IsDir() != root.IsDir || info.Size() != root.Size || !info.ModTime().Equal(root.ModTime) {
		_ = file.Close()
		return nil, nil, &DeleteTargetChangedError{Path: name}
	}
	return file, info, nil
}

func (fs *FileSystem) stageDeleteTargetLocked(ctx context.Context, name string, expected DeleteTargetSnapshot, beforeCapture func() error) (*stagedDeleteTarget, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if expected.Root.Path != name || len(expected.Entries) == 0 {
		return nil, &DeleteTargetChangedError{Path: name}
	}
	witness, witnessInfo, err := fs.openDeleteWitness(name, expected.Root)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrNotDir) {
			return nil, &DeleteTargetChangedError{Path: name}
		}
		return nil, err
	}
	target := &stagedDeleteTarget{
		logicalName: name,
		originalRel: storageWorkspaceRelativeName(name),
		witness:     witness,
		witnessInfo: witnessInfo,
		expected:    expected,
	}
	abort := func(cause error) (*stagedDeleteTarget, error) {
		_ = target.close()
		return nil, cause
	}
	if err := afterDeleteWitnessOpen(name); err != nil {
		return abort(err)
	}
	if beforeCapture != nil {
		if err := beforeCapture(); err != nil {
			return abort(err)
		}
	}

	if err := fs.captureDeleteMountBoundary(fs.workspace.Root()).checkHostTree(fs.workspace.FullPath(name)); err != nil {
		return abort(err)
	}
	root := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	witnessIdentity := deleteStageIdentity(witnessInfo)
	for range 32 {
		stageRel, err := newDeleteStageRelativeName(target.originalRel)
		if err != nil {
			return abort(err)
		}
		stageAbs := storageAbsolutePath(root, stageRel)
		if err := fs.captureDeleteMountBoundary(fs.workspace.Root()).checkHostTree(fs.workspace.FullPath(name)); err != nil {
			return abort(err)
		}
		stageInfo, moved, err := renameDeleteLeafNoReplace(fs.filesRootHandle, target.originalRel, stageRel, witnessInfo, witnessIdentity)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			if moved {
				target.stageRel = stageRel
				target.stageName = storageWorkspaceName(stageRel)
				target.stageAbs = stageAbs
				target.stageInfo = stageInfo
				target.stageIdentity = deleteStageIdentity(stageInfo)
				closeErr := target.close()
				return nil, errors.Join(
					&DeleteTargetChangedError{Path: name},
					&DeleteStageResidualError{Path: name, StagePath: stageAbs, err: errors.Join(err, closeErr)},
				)
			}
			if errors.Is(err, ErrDeleteTargetChanged) {
				if current, statErr := fs.filesRootHandle.Lstat(target.originalRel); statErr == nil && !current.IsDir() && !current.Mode().IsRegular() {
					return abort(ErrNotRegular)
				}
				return abort(&DeleteTargetChangedError{Path: name})
			}
			return abort(err)
		}
		target.stageRel = stageRel
		target.stageName = storageWorkspaceName(stageRel)
		target.stageAbs = stageAbs
		target.stageInfo = stageInfo
		target.stageIdentity = deleteStageIdentity(stageInfo)
		break
	}
	if target.stageRel == "" {
		return abort(errors.New("failed to allocate unique delete stage path"))
	}

	rollbackCapture := func(cause error) (*stagedDeleteTarget, error) {
		return nil, fs.rollbackStagedDeleteLocked(target, cause)
	}
	if target.stageIdentity == "" {
		return rollbackCapture(ErrDeleteIdentityUnavailable)
	}
	if err := syncStorageManagedRenameDirs(root, target.originalRel, root, target.stageRel); err != nil {
		target.warning = workspace.WrapVisibleMutationWarning(fmt.Errorf("failed to sync delete staging rename: %w", err))
	}
	if err := afterDeleteStageCapture(name, target.stageAbs); err != nil {
		return rollbackCapture(err)
	}
	stageInfo, err := fs.filesRootHandle.Lstat(target.stageRel)
	if err != nil {
		return rollbackCapture(mapStorageRootPathError(err))
	}
	if !sameDeleteStageEntry(target.stageInfo, target.stageIdentity, stageInfo) || !os.SameFile(target.witnessInfo, stageInfo) {
		if !stageInfo.IsDir() && !stageInfo.Mode().IsRegular() {
			return rollbackCapture(ErrNotRegular)
		}
		return rollbackCapture(&DeleteTargetChangedError{Path: name})
	}
	if err := fs.captureDeleteMountBoundary(fs.workspace.Root()).checkHostTree(target.stageAbs); err != nil {
		return rollbackCapture(err)
	}
	return target, nil
}

func (fs *FileSystem) rollbackStagedDeleteLocked(target *stagedDeleteTarget, cause error) error {
	if target == nil || target.stageRel == "" {
		return cause
	}
	root := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	stageRel := target.stageRel
	boundary := fs.captureDeleteMountBoundary(fs.workspace.Root())
	if err := errors.Join(
		boundary.checkHostTree(target.stageAbs),
		boundary.checkHostTree(fs.workspace.FullPath(target.logicalName)),
	); err != nil {
		residual := target.residual(err)
		return errors.Join(cause, residual)
	}
	stageInfo, err := fs.filesRootHandle.Lstat(stageRel)
	if err != nil || target.stageInfo == nil || stageInfo == nil || !os.SameFile(target.stageInfo, stageInfo) {
		if err == nil {
			err = ErrDeleteTargetChanged
		}
		residual := target.residual(mapStorageRootPathError(err))
		return errors.Join(cause, residual)
	}
	rollbackIdentity := deleteStageIdentity(stageInfo)
	if rollbackIdentity == "" {
		return errors.Join(cause, target.residual(ErrDeleteIdentityUnavailable))
	}
	restoredInfo, moved, err := renameDeleteLeafNoReplace(fs.filesRootHandle, stageRel, target.originalRel, target.stageInfo, rollbackIdentity)
	if err != nil {
		if moved {
			return errors.Join(cause, &DeleteStageResidualError{
				Path:      target.logicalName,
				StagePath: fs.workspace.FullPath(target.logicalName),
				err:       err,
			})
		}
		residual := target.residual(mapStorageRootPathError(err))
		return errors.Join(cause, residual)
	}
	if !os.SameFile(target.stageInfo, restoredInfo) {
		residual := target.residual(ErrDeleteTargetChanged)
		return errors.Join(cause, residual)
	}
	if err := fs.captureDeleteMountBoundary(fs.workspace.Root()).checkHostTree(fs.workspace.FullPath(target.logicalName)); err != nil {
		target.stageRel = ""
		target.stageName = ""
		target.stageAbs = ""
		return errors.Join(cause, err)
	}
	target.stageRel = ""
	target.stageName = ""
	target.stageAbs = ""
	if err := syncStorageManagedRenameDirs(root, stageRel, root, target.originalRel); err != nil {
		return errors.Join(cause, fmt.Errorf("failed to sync restored delete target: %w", err))
	}
	return cause
}

func logicalDeleteStagePath(stageRoot, logicalRoot, stagePath string) (string, bool) {
	if stagePath == stageRoot {
		return logicalRoot, true
	}
	prefix := strings.TrimSuffix(stageRoot, "/") + "/"
	if !strings.HasPrefix(stagePath, prefix) {
		return "", false
	}
	return strings.TrimSuffix(logicalRoot, "/") + "/" + strings.TrimPrefix(stagePath, prefix), true
}

func deleteSnapshotEntryMap(snapshot DeleteTargetSnapshot) map[string]FileInfo {
	entries := make(map[string]FileInfo, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		entries[entry.Path] = entry
	}
	return entries
}

func sameDeleteSnapshotMetadata(actual *workspace.FileInfo, expected FileInfo) bool {
	return actual != nil && actual.IsDir == expected.IsDir && actual.Size == expected.Size && actual.ModTime.Equal(expected.ModTime)
}

func (fs *FileSystem) hashStableStagedFile(ctx context.Context, target *stagedDeleteTarget, stagePath, logicalPath string, expected FileInfo, forceHash bool) (string, error) {
	rel := storageWorkspaceRelativeName(stagePath)
	var (
		file *os.File
		err  error
	)
	if logicalPath == target.logicalName && !expected.IsDir {
		file = target.witness
	} else {
		file, err = rootio.OpenRegularFileNoFollow(fs.filesRootHandle, rel)
		if err != nil {
			return "", mapWorkspaceReadablePathError(mapStorageRootPathError(err))
		}
		defer file.Close()
	}
	before, err := file.Stat()
	if err != nil {
		return "", err
	}
	pathInfo, err := fs.filesRootHandle.Lstat(rel)
	if err != nil || !os.SameFile(before, pathInfo) {
		return "", &DeleteTargetChangedError{Path: target.logicalName}
	}
	if logicalPath == target.logicalName {
		if !os.SameFile(target.witnessInfo, before) {
			return "", &DeleteTargetChangedError{Path: target.logicalName}
		}
	} else if workspace.DeleteIdentityTokenForFileInfo(before) != expected.DeleteIdentityToken {
		return "", &DeleteTargetChangedError{Path: target.logicalName}
	}
	beforeToken := workspace.DeleteIdentityTokenForFileInfo(before)
	hash := ""
	if expected.ContentHash != "" || forceHash {
		hash, err = hashOpenWorkspaceFileContext(ctx, file)
		if err != nil {
			return "", err
		}
	}
	after, err := file.Stat()
	if err != nil {
		return "", err
	}
	pathInfo, err = fs.filesRootHandle.Lstat(rel)
	if err != nil || !os.SameFile(after, pathInfo) || !os.SameFile(before, after) || beforeToken == "" || beforeToken != workspace.DeleteIdentityTokenForFileInfo(after) {
		return "", &DeleteTargetChangedError{Path: target.logicalName}
	}
	if after.Size() != expected.Size || !after.ModTime().Equal(expected.ModTime) || (expected.ContentHash != "" && hash != expected.ContentHash) {
		return "", &DeleteTargetChangedError{Path: target.logicalName}
	}
	return hash, nil
}

func (fs *FileSystem) snapshotStagedDeleteLocked(ctx context.Context, target *stagedDeleteTarget) (DeleteTargetSnapshot, error) {
	return fs.snapshotStagedDeleteLockedWithHashes(ctx, target, false)
}

func (fs *FileSystem) snapshotStagedDeleteLockedWithHashes(ctx context.Context, target *stagedDeleteTarget, forceHash bool) (DeleteTargetSnapshot, error) {
	expectedByPath := deleteSnapshotEntryMap(target.expected)
	entries := make([]FileInfo, 0, len(expectedByPath))
	seen := make(map[string]struct{}, len(expectedByPath))
	type stableDir struct {
		rel      string
		logical  string
		identity string
	}
	directories := make([]stableDir, 0)
	mountBoundary := fs.captureDeleteMountBoundary(fs.workspace.Root())

	err := walkStorageDeleteTree(ctx, fs.workspace, target.stageName, func(stagePath string, info *workspace.FileInfo) error {
		logicalPath, ok := logicalDeleteStagePath(target.stageName, target.logicalName, stagePath)
		if !ok {
			return &DeleteTargetChangedError{Path: target.logicalName}
		}
		expected, ok := expectedByPath[logicalPath]
		if !ok || !sameDeleteSnapshotMetadata(info, expected) {
			return &DeleteTargetChangedError{Path: target.logicalName}
		}
		if _, duplicate := seen[logicalPath]; duplicate {
			return &DeleteTargetChangedError{Path: target.logicalName}
		}
		seen[logicalPath] = struct{}{}
		if err := mountBoundary.checkWorkspacePath(stagePath); err != nil {
			return err
		}
		if info == nil || (!info.IsDir && !info.Mode.IsRegular()) {
			return workspace.ErrNotRegular
		}

		rel := storageWorkspaceRelativeName(stagePath)
		osInfo, err := fs.filesRootHandle.Lstat(rel)
		if err != nil {
			return mapStorageRootPathError(err)
		}
		identity := workspace.DeleteIdentityTokenForFileInfo(osInfo)
		if logicalPath == target.logicalName {
			if !os.SameFile(target.witnessInfo, osInfo) || !sameDeleteStageEntry(target.stageInfo, target.stageIdentity, osInfo) {
				return &DeleteTargetChangedError{Path: target.logicalName}
			}
		} else if identity == "" || identity != expected.DeleteIdentityToken {
			return &DeleteTargetChangedError{Path: target.logicalName}
		}

		entry := FileInfo{
			Path:                logicalPath,
			Name:                expected.Name,
			IsDir:               info.IsDir,
			Size:                info.Size,
			ModTime:             info.ModTime,
			DeleteIdentityToken: expected.DeleteIdentityToken,
			Versioned:           expected.Versioned,
		}
		if info.IsDir {
			directories = append(directories, stableDir{rel: rel, logical: logicalPath, identity: identity})
		} else {
			hash, err := fs.hashStableStagedFile(ctx, target, stagePath, logicalPath, expected, forceHash)
			if err != nil {
				return err
			}
			entry.ContentHash = hash
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return DeleteTargetSnapshot{}, mapWorkspaceReadablePathError(err)
	}
	if len(seen) != len(expectedByPath) {
		return DeleteTargetSnapshot{}, &DeleteTargetChangedError{Path: target.logicalName}
	}
	if err := fs.captureDeleteMountBoundary(fs.workspace.Root()).checkHostTree(target.stageAbs); err != nil {
		return DeleteTargetSnapshot{}, err
	}
	for _, directory := range directories {
		info, err := fs.filesRootHandle.Lstat(directory.rel)
		if err != nil {
			return DeleteTargetSnapshot{}, &DeleteTargetChangedError{Path: target.logicalName}
		}
		if directory.logical == target.logicalName {
			if !os.SameFile(target.witnessInfo, info) || !sameDeleteStageEntry(target.stageInfo, target.stageIdentity, info) || workspace.DeleteIdentityTokenForFileInfo(info) != directory.identity {
				return DeleteTargetSnapshot{}, &DeleteTargetChangedError{Path: target.logicalName}
			}
		} else if workspace.DeleteIdentityTokenForFileInfo(info) != directory.identity {
			return DeleteTargetSnapshot{}, &DeleteTargetChangedError{Path: target.logicalName}
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	for _, entry := range entries {
		if entry.Path == target.logicalName {
			snapshot := DeleteTargetSnapshot{Root: entry, Entries: entries}
			comparison := snapshot
			if forceHash && !deleteSnapshotIncludesContentHashes(target.expected) {
				comparison = projectDeleteTargetSnapshot(snapshot, DeleteTargetSnapshotOptions{IncludeDescendants: true})
			}
			if deleteTargetToken(comparison) != deleteTargetToken(target.expected) {
				return DeleteTargetSnapshot{}, &DeleteTargetChangedError{Path: target.logicalName}
			}
			return snapshot, nil
		}
	}
	return DeleteTargetSnapshot{}, &DeleteTargetChangedError{Path: target.logicalName}
}

func deleteSnapshotIncludesContentHashes(snapshot DeleteTargetSnapshot) bool {
	for _, entry := range snapshot.Entries {
		if !entry.IsDir && entry.ContentHash != "" {
			return true
		}
	}
	return false
}

func projectDeleteTargetSnapshot(snapshot DeleteTargetSnapshot, options DeleteTargetSnapshotOptions) DeleteTargetSnapshot {
	projected := DeleteTargetSnapshot{
		Root:    snapshot.Root,
		Entries: append([]FileInfo(nil), snapshot.Entries...),
	}
	if !options.IncludeDescendants {
		projected.Entries = []FileInfo{projected.Root}
	}
	if !options.IncludeContentHash {
		projected.Root.ContentHash = ""
		for i := range projected.Entries {
			projected.Entries[i].ContentHash = ""
		}
	}
	return projected
}

func isCrossDeviceError(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}
