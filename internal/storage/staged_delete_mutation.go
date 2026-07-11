package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/versionstore"
	"github.com/seanbao/mnemonas/internal/workspace"
)

func (fs *FileSystem) deletePreparedWithPolicyLocked(ctx context.Context, name string, policy DeletePolicy, expected DeleteTargetSnapshot) error {
	if policy.Mode == DeleteModeTrash {
		return fs.commitJournaledTrashDeleteLocked(ctx, name, policy, expected)
	}
	if policy.Mode == DeleteModePermanent && expected.Root.IsDir && len(expected.Entries) > 1 {
		return ErrDirNotEmpty
	}
	target, err := fs.stageDeleteTargetLocked(ctx, name, expected, nil)
	if err != nil {
		return err
	}
	defer target.close()

	verified, err := fs.snapshotStagedDeleteLocked(ctx, target)
	if err != nil {
		return fs.rollbackStagedDeleteLocked(target, err)
	}
	target.expected = verified
	return fs.commitStagedPermanentDeleteLocked(ctx, target, verified)
}

func deleteSnapshotTargetSize(snapshot DeleteTargetSnapshot) int64 {
	var size int64
	for _, entry := range snapshot.Entries {
		if !entry.IsDir {
			size += entry.Size
		}
	}
	return size
}

func (fs *FileSystem) deleteSnapshotHadVersions(ctx context.Context, snapshot DeleteTargetSnapshot) (bool, error) {
	if !snapshot.Root.IsDir {
		versions, err := fs.getVersions(ctx, snapshot.Root.Path)
		if err != nil {
			return false, fmt.Errorf("failed to read version metadata: %w", err)
		}
		return len(versions) > 0, nil
	}
	versionPaths, err := fs.listVersionPaths(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to list version metadata paths: %w", err)
	}
	for _, versionPath := range versionPaths {
		if pathMatchesOrDescendant(snapshot.Root.Path, versionPath) {
			return true, nil
		}
	}
	return false, nil
}

type stagedTrashContentEntry struct {
	info     os.FileInfo
	identity string
	logical  string
	hash     string
}

type stagedTrashContent struct {
	rel         string
	abs         string
	source      DeleteTargetSnapshot
	sourceModes map[string]os.FileMode
	entries     map[string]stagedTrashContentEntry
}

func (fs *FileSystem) copyStagedDeleteToTrashLocked(ctx context.Context, target *stagedDeleteTarget, trashContentPath string) (*stagedTrashContent, error) {
	srcRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	dstRoot := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
	dstRel, ok := storageRelativePath(fs.trashRoot, filepath.Clean(trashContentPath))
	if !ok || dstRel == "." {
		return nil, errStoragePathSymlink
	}
	dstAbs := storageAbsolutePath(dstRoot, dstRel)
	if err := fs.checkStorageCopyMountBoundaries(srcRoot, target.stageAbs, dstRoot, dstAbs); err != nil {
		return nil, err
	}
	if target.expected.Root.IsDir {
		if err := fs.copyDirBetweenRoots(srcRoot, target.stageRel, target.stageAbs, dstRoot, dstRel, dstAbs); err != nil {
			return nil, fs.wrapStagedTrashCopyFailure(target, dstRel, dstAbs, err)
		}
	} else {
		if err := fs.copyFileBetweenRoots(srcRoot, target.stageRel, target.stageAbs, dstRoot, dstRel, dstAbs); err != nil {
			return nil, fs.wrapStagedTrashCopyFailure(target, dstRel, dstAbs, err)
		}
	}
	if err := afterDeleteTrashCopy(target.logicalName, dstAbs); err != nil {
		return nil, &DeleteStageResidualError{Path: target.logicalName, StagePath: dstAbs, err: err}
	}
	if err := fs.checkStorageCopyMountBoundaries(srcRoot, target.stageAbs, dstRoot, dstAbs); err != nil {
		return nil, &DeleteStageResidualError{Path: target.logicalName, StagePath: dstAbs, err: err}
	}
	source, err := fs.snapshotStagedDeleteLockedWithHashes(ctx, target, true)
	if err != nil {
		return nil, &DeleteStageResidualError{Path: target.logicalName, StagePath: dstAbs, err: err}
	}
	if err := fs.checkStorageCopyMountBoundaries(srcRoot, target.stageAbs, dstRoot, dstAbs); err != nil {
		return nil, &DeleteStageResidualError{Path: target.logicalName, StagePath: dstAbs, err: err}
	}
	sourceModes, err := fs.captureStagedDeleteModesLocked(target)
	if err != nil {
		return nil, &DeleteStageResidualError{Path: target.logicalName, StagePath: dstAbs, err: err}
	}
	content := &stagedTrashContent{rel: dstRel, abs: dstAbs, source: source, sourceModes: sourceModes}
	entries, err := fs.scanStagedTrashContentLocked(ctx, content, nil)
	if err != nil {
		return nil, &DeleteStageResidualError{Path: target.logicalName, StagePath: dstAbs, err: err}
	}
	content.entries = entries
	if err := fs.checkStorageCopyMountBoundaries(srcRoot, target.stageAbs, dstRoot, dstAbs); err != nil {
		return nil, &DeleteStageResidualError{Path: target.logicalName, StagePath: dstAbs, err: err}
	}
	if err := afterDeleteTrashContentHash(target.logicalName, target.stageAbs, dstAbs); err != nil {
		return nil, &DeleteStageResidualError{Path: target.logicalName, StagePath: dstAbs, err: err}
	}
	if err := fs.verifyStagedDeleteMetadataLocked(ctx, target, source); err != nil {
		return nil, &DeleteStageResidualError{Path: target.logicalName, StagePath: dstAbs, err: err}
	}
	if err := fs.verifyStagedTrashContentMetadataLocked(ctx, content); err != nil {
		return nil, &DeleteStageResidualError{Path: target.logicalName, StagePath: dstAbs, err: err}
	}
	if err := fs.checkStorageCopyMountBoundaries(srcRoot, target.stageAbs, dstRoot, dstAbs); err != nil {
		return nil, &DeleteStageResidualError{Path: target.logicalName, StagePath: dstAbs, err: err}
	}
	return content, nil
}

func (fs *FileSystem) wrapStagedTrashCopyFailure(target *stagedDeleteTarget, dstRel, dstAbs string, cause error) error {
	_, err := fs.trashRootHandle.Lstat(dstRel)
	if errors.Is(err, os.ErrNotExist) {
		return cause
	}
	if err != nil {
		cause = errors.Join(cause, mapStorageRootPathError(err))
	}
	return &DeleteStageResidualError{Path: target.logicalName, StagePath: dstAbs, err: cause}
}

func (fs *FileSystem) captureStagedDeleteModesLocked(target *stagedDeleteTarget) (map[string]os.FileMode, error) {
	modes := make(map[string]os.FileMode, len(target.expected.Entries))
	for _, expected := range target.expected.Entries {
		stagePath := target.stageName
		if expected.Path != target.logicalName {
			suffix := strings.TrimPrefix(expected.Path, strings.TrimSuffix(target.logicalName, "/")+"/")
			if suffix == expected.Path || suffix == "" {
				return nil, ErrDeleteTargetChanged
			}
			stagePath = strings.TrimSuffix(target.stageName, "/") + "/" + suffix
		}
		info, err := fs.filesRootHandle.Lstat(storageWorkspaceRelativeName(stagePath))
		if err != nil {
			return nil, mapStorageRootPathError(err)
		}
		if expected.Path == target.logicalName {
			if !sameDeleteStageEntry(target.stageInfo, target.stageIdentity, info) {
				return nil, ErrDeleteTargetChanged
			}
		} else if deleteStageIdentity(info) != expected.DeleteIdentityToken {
			return nil, ErrDeleteTargetChanged
		}
		modes[expected.Path] = info.Mode()
	}
	return modes, nil
}

func sameStagedTrashContentMetadata(expected stagedTrashContentEntry, logical string, actual os.FileInfo) bool {
	return expected.info != nil &&
		expected.logical == logical &&
		sameDeleteStageEntry(expected.info, expected.identity, actual) &&
		expected.info.IsDir() == actual.IsDir() &&
		expected.info.Mode() == actual.Mode() &&
		expected.info.Size() == actual.Size() &&
		expected.info.ModTime().Equal(actual.ModTime())
}

func (fs *FileSystem) scanStagedTrashContentLocked(ctx context.Context, content *stagedTrashContent, expectedManifest map[string]stagedTrashContentEntry) (map[string]stagedTrashContentEntry, error) {
	return fs.scanStagedTrashContentWithHashesLocked(ctx, content, expectedManifest, true)
}

func (fs *FileSystem) verifyStagedTrashContentMetadataLocked(ctx context.Context, content *stagedTrashContent) error {
	if content == nil || content.entries == nil {
		return ErrDeleteTargetChanged
	}
	_, err := fs.scanStagedTrashContentWithHashesLocked(ctx, content, content.entries, false)
	return err
}

func (fs *FileSystem) scanStagedTrashContentWithHashesLocked(ctx context.Context, content *stagedTrashContent, expectedManifest map[string]stagedTrashContentEntry, includeContentHashes bool) (map[string]stagedTrashContentEntry, error) {
	if content == nil || content.rel == "" || content.abs == "" {
		return nil, ErrDeleteTargetChanged
	}
	if err := fs.captureDeleteMountBoundary(fs.trashRoot).checkHostTree(content.abs); err != nil {
		return nil, err
	}
	expectedByLogical := deleteSnapshotEntryMap(content.source)
	seen := make(map[string]struct{}, len(expectedByLogical))
	entries := make(map[string]stagedTrashContentEntry, len(expectedByLogical))

	var scan func(string, string) error
	scan = func(rel, suffix string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := fs.trashRootHandle.Lstat(rel)
		if err != nil {
			return mapStorageRootPathError(err)
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return ErrNotRegular
		}
		logical := content.source.Root.Path
		if suffix != "." {
			logical = strings.TrimSuffix(content.source.Root.Path, "/") + "/" + filepath.ToSlash(suffix)
		}
		expected, ok := expectedByLogical[logical]
		sourceMode, modeOK := content.sourceModes[logical]
		if !ok || !modeOK || info.IsDir() != expected.IsDir || storagePreservedMode(info.Mode()) != storagePreservedMode(sourceMode) || (!info.IsDir() && info.Size() != expected.Size) {
			return ErrDeleteTargetChanged
		}
		identity := deleteStageIdentity(info)
		if identity == "" {
			return ErrDeleteIdentityUnavailable
		}
		entry := stagedTrashContentEntry{info: info, identity: identity, logical: logical}
		if expectedEntry, ok := expectedManifest[suffix]; expectedManifest != nil {
			if !ok || !sameStagedTrashContentMetadata(expectedEntry, logical, info) {
				return ErrDeleteTargetChanged
			}
		}
		if info.IsDir() {
			dir, err := rootio.OpenDirNoFollow(fs.trashRootHandle, rel)
			if err != nil {
				return mapStorageRootPathError(err)
			}
			opened, err := dir.Stat()
			if err != nil || !os.SameFile(info, opened) {
				_ = dir.Close()
				return errors.Join(ErrDeleteTargetChanged, err)
			}
			children, err := dir.ReadDir(-1)
			if closeErr := dir.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return err
			}
			entries[suffix] = entry
			seen[logical] = struct{}{}
			for _, child := range children {
				childName, err := safeStorageReadDirFallbackChildName(child.Name())
				if err != nil {
					return err
				}
				childSuffix := childName
				if suffix != "." {
					childSuffix = filepath.Join(suffix, childName)
				}
				if err := scan(filepath.Join(rel, childName), childSuffix); err != nil {
					return err
				}
			}
			current, err := fs.trashRootHandle.Lstat(rel)
			if err != nil || !sameDeleteStageEntry(info, identity, current) {
				return errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
			}
			return nil
		}

		file, err := rootio.OpenRegularFileNoFollow(fs.trashRootHandle, rel)
		if err != nil {
			return mapStorageRootPathError(err)
		}
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			_ = file.Close()
			return errors.Join(ErrDeleteTargetChanged, err)
		}
		hash := ""
		if includeContentHashes {
			hashFile := fs.hashStagedTrashContentFile
			if hashFile == nil {
				hashFile = hashOpenWorkspaceFileContext
			}
			hash, err = hashFile(ctx, file)
			if err != nil {
				_ = file.Close()
				return err
			}
		}
		after, statErr := file.Stat()
		closeErr := file.Close()
		if statErr != nil || closeErr != nil || !sameDeleteStageEntry(info, identity, after) {
			return errors.Join(ErrDeleteTargetChanged, statErr, closeErr)
		}
		current, err := fs.trashRootHandle.Lstat(rel)
		if err != nil || !sameDeleteStageEntry(info, identity, current) || (includeContentHashes && hash != expected.ContentHash) {
			return errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
		}
		entry.hash = hash
		if expectedEntry, ok := expectedManifest[suffix]; expectedManifest != nil && includeContentHashes {
			if !ok || expectedEntry.hash != hash {
				return ErrDeleteTargetChanged
			}
		}
		entries[suffix] = entry
		seen[logical] = struct{}{}
		return nil
	}

	if err := scan(content.rel, "."); err != nil {
		return nil, err
	}
	if len(seen) != len(expectedByLogical) || (expectedManifest != nil && len(entries) != len(expectedManifest)) {
		return nil, ErrDeleteTargetChanged
	}
	if err := fs.captureDeleteMountBoundary(fs.trashRoot).checkHostTree(content.abs); err != nil {
		return nil, err
	}
	return entries, nil
}

func (fs *FileSystem) verifyStagedTrashContentLocked(ctx context.Context, content *stagedTrashContent) error {
	_, err := fs.scanStagedTrashContentLocked(ctx, content, content.entries)
	return err
}

type stagedDeleteRemovalEntry struct {
	info     os.FileInfo
	identity string
}

func stagedDeleteRelativeEntryPath(target *stagedDeleteTarget, logicalPath string) (string, bool) {
	if target == nil || target.stageRel == "" || logicalPath == "" {
		return "", false
	}
	if logicalPath == target.logicalName {
		return target.stageRel, true
	}
	prefix := strings.TrimSuffix(target.logicalName, "/") + "/"
	if !strings.HasPrefix(logicalPath, prefix) {
		return "", false
	}
	suffix := strings.TrimPrefix(logicalPath, prefix)
	if suffix == "" {
		return "", false
	}
	return filepath.Join(target.stageRel, filepath.FromSlash(suffix)), true
}

func (fs *FileSystem) captureStagedDeleteRemovalManifestLocked(target *stagedDeleteTarget) (map[string]stagedDeleteRemovalEntry, error) {
	if target == nil || target.stageRel == "" {
		return nil, ErrDeleteTargetChanged
	}
	manifest := make(map[string]stagedDeleteRemovalEntry, len(target.expected.Entries))
	for _, expected := range target.expected.Entries {
		rel, ok := stagedDeleteRelativeEntryPath(target, expected.Path)
		if !ok {
			return nil, ErrDeleteTargetChanged
		}
		info, err := fs.filesRootHandle.Lstat(rel)
		if err != nil {
			return nil, mapStorageRootPathError(err)
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) || info.IsDir() != expected.IsDir || info.Size() != expected.Size || !info.ModTime().Equal(expected.ModTime) {
			return nil, ErrDeleteTargetChanged
		}
		identity := deleteStageIdentity(info)
		if expected.Path == target.logicalName {
			if !sameDeleteStageEntry(target.stageInfo, target.stageIdentity, info) {
				return nil, ErrDeleteTargetChanged
			}
		} else if identity == "" || identity != expected.DeleteIdentityToken {
			return nil, ErrDeleteTargetChanged
		}
		manifest[expected.Path] = stagedDeleteRemovalEntry{info: info, identity: identity}
	}
	if len(manifest) != len(target.expected.Entries) {
		return nil, ErrDeleteTargetChanged
	}
	return manifest, nil
}

func (fs *FileSystem) removeStagedDeleteTargetLocked(ctx context.Context, target *stagedDeleteTarget, useWorkspaceDelete bool, retainedCopy *stagedTrashContent) error {
	if target.stageRel == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return target.residual(err)
	}
	info, err := fs.filesRootHandle.Lstat(target.stageRel)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	if !sameDeleteStageEntry(target.stageInfo, target.stageIdentity, info) {
		return target.residual(ErrDeleteTargetChanged)
	}
	if err := fs.captureDeleteMountBoundary(fs.workspace.Root()).checkHostTree(target.stageAbs); err != nil {
		return target.residual(err)
	}
	if _, err := fs.snapshotStagedDeleteLocked(ctx, target); err != nil {
		return target.residual(err)
	}
	if err := beforeDeleteStageRemoval(target.logicalName, target.stageAbs); err != nil {
		return target.residual(err)
	}

	quarantine, err := fs.isolateStagedDeleteForRemovalLocked(ctx, target)
	if err != nil {
		var residual *DeleteStageResidualError
		if errors.As(err, &residual) {
			return err
		}
		return target.residual(err)
	}
	defer quarantine.close()
	if err := afterDeleteQuarantineCapture(target.logicalName, target.stageAbs); err != nil {
		return target.residual(err)
	}
	if err := fs.captureDeleteMountBoundary(fs.workspace.Root()).checkHostTree(target.stageAbs); err != nil {
		return target.residual(err)
	}
	manifest, err := fs.captureStagedDeleteRemovalManifestLocked(target)
	if err != nil {
		return target.residual(err)
	}
	if err := fs.captureDeleteMountBoundary(fs.workspace.Root()).checkHostTree(target.stageAbs); err != nil {
		return target.residual(err)
	}
	if retainedCopy != nil {
		if err := fs.verifyStagedTrashContentLocked(ctx, retainedCopy); err != nil {
			return target.residual(fmt.Errorf("trash copy changed before staged source removal: %w", err))
		}
	}

	verifiedIdentities := make(map[string]struct{}, len(manifest))
	remove := func() error {
		return rootio.RemoveAllFromDirNoFollowChecked(quarantine.dir, deleteQuarantineContentName, func(entryPath string, entryInfo os.FileInfo) error {
			return fs.verifyQuarantinedDeleteEntry(target, manifest, verifiedIdentities, entryPath, entryInfo)
		})
	}
	var removeErr error
	if useWorkspaceDelete && fs.deleteStagedWorkspacePath != nil {
		removeErr = fs.deleteStagedWorkspacePath(ctx, target.logicalName, remove)
	} else {
		removeErr = remove()
	}
	visibleWarning := error(nil)
	if removeErr != nil {
		if workspace.IsVisibleMutationWarning(removeErr) || isVisibleMutationWarning(removeErr) {
			if content, openErr := rootio.OpenDirEntryNoFollow(quarantine.dir, deleteQuarantineContentName); openErr == nil {
				_ = content.Close()
				return target.residual(removeErr)
			} else if !errors.Is(openErr, os.ErrNotExist) {
				return target.residual(errors.Join(removeErr, mapStorageRootPathError(openErr)))
			}
			visibleWarning = removeErr
		} else {
			return target.residual(mapStorageRootPathError(removeErr))
		}
	}

	target.stageRel = ""
	target.stageName = ""
	target.stageAbs = ""
	if err := fs.removeDeleteQuarantineLocked(quarantine); err != nil {
		if workspace.IsVisibleMutationWarning(err) || isVisibleMutationWarning(err) {
			return errors.Join(visibleWarning, err)
		}
		return errors.Join(visibleWarning, &DeleteStageResidualError{
			Path:      target.logicalName,
			StagePath: quarantine.abs,
			err:       err,
		})
	}
	return visibleWarning
}

const deleteQuarantineContentName = "content"

type stagedDeleteQuarantine struct {
	rel  string
	abs  string
	dir  *os.File
	info os.FileInfo
}

func (quarantine *stagedDeleteQuarantine) close() error {
	if quarantine == nil || quarantine.dir == nil {
		return nil
	}
	err := quarantine.dir.Close()
	quarantine.dir = nil
	return err
}

func deleteQuarantineRelativeName(originalRel string) (string, error) {
	rel, err := newDeleteStageRelativeName(originalRel)
	if err != nil {
		return "", err
	}
	return rel[:len(rel)-len(".stage")] + ".quarantine", nil
}

func (fs *FileSystem) removeDeleteQuarantineLocked(quarantine *stagedDeleteQuarantine) error {
	if quarantine == nil || quarantine.dir == nil {
		return nil
	}
	if err := fs.captureDeleteMountBoundary(fs.workspace.Root()).checkHostTree(quarantine.abs); err != nil {
		return err
	}
	current, err := fs.filesRootHandle.Lstat(quarantine.rel)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	opened, err := quarantine.dir.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(quarantine.info, current) || !os.SameFile(opened, current) {
		return ErrDeleteTargetChanged
	}
	if err := quarantine.close(); err != nil {
		return err
	}
	parentRel := filepath.Dir(quarantine.rel)
	parent, err := rootio.OpenDirNoFollow(fs.filesRootHandle, parentRel)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	defer parent.Close()
	base := filepath.Base(quarantine.rel)
	if err := rootio.RemoveAllFromDirNoFollowChecked(parent, base, func(entryPath string, info os.FileInfo) error {
		if entryPath != base || info == nil || !info.IsDir() || !os.SameFile(quarantine.info, info) {
			return rootio.ErrEntryChanged
		}
		return nil
	}); err != nil {
		return mapStorageRootPathError(err)
	}
	if err := syncManagedStorageDir(fs.filesRootHandle, parentRel, storageAbsolutePath(&storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}, parentRel)); err != nil {
		return workspace.WrapVisibleMutationWarning(fmt.Errorf("failed to sync staged delete cleanup: %w", err))
	}
	return nil
}

func (fs *FileSystem) isolateStagedDeleteForRemovalLocked(ctx context.Context, target *stagedDeleteTarget) (*stagedDeleteQuarantine, error) {
	root := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	var quarantine *stagedDeleteQuarantine
	for range 32 {
		rel, err := deleteQuarantineRelativeName(target.originalRel)
		if err != nil {
			return nil, err
		}
		if err := rootio.MkdirNoFollow(fs.filesRootHandle, rel, 0o700); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return nil, mapStorageRootPathError(err)
		}
		dir, err := rootio.OpenDirNoFollow(fs.filesRootHandle, rel)
		if err != nil {
			return nil, &DeleteStageResidualError{
				Path:      target.logicalName,
				StagePath: storageAbsolutePath(root, rel),
				err:       mapStorageRootPathError(err),
			}
		}
		info, err := dir.Stat()
		if err != nil {
			_ = dir.Close()
			return nil, err
		}
		quarantine = &stagedDeleteQuarantine{
			rel:  rel,
			abs:  storageAbsolutePath(root, rel),
			dir:  dir,
			info: info,
		}
		break
	}
	if quarantine == nil {
		return nil, errors.New("failed to allocate delete quarantine")
	}
	abortEmpty := func(cause error) (*stagedDeleteQuarantine, error) {
		cleanupErr := fs.removeDeleteQuarantineLocked(quarantine)
		return nil, errors.Join(cause, wrapStorageStepError("cleanup empty delete quarantine", cleanupErr))
	}
	retain := func(cause error) (*stagedDeleteQuarantine, error) {
		closeErr := quarantine.close()
		return nil, target.residual(errors.Join(cause, wrapStorageStepError("close retained delete quarantine", closeErr)))
	}
	if err := ctx.Err(); err != nil {
		return abortEmpty(err)
	}
	if err := fs.captureDeleteMountBoundary(fs.workspace.Root()).checkHostTree(quarantine.abs); err != nil {
		return abortEmpty(err)
	}
	current, err := fs.filesRootHandle.Lstat(target.stageRel)
	if err != nil || !sameDeleteStageEntry(target.stageInfo, target.stageIdentity, current) {
		if err == nil {
			err = ErrDeleteTargetChanged
		}
		return abortEmpty(target.residual(mapStorageRootPathError(err)))
	}
	if err := rootio.RenameLeafIntoDirNoReplace(fs.filesRootHandle, target.stageRel, quarantine.dir, deleteQuarantineContentName); err != nil {
		return abortEmpty(mapStorageRootPathError(err))
	}
	content, err := rootio.OpenDirEntryNoFollow(quarantine.dir, deleteQuarantineContentName)
	if err != nil {
		target.stageAbs = filepath.Join(quarantine.abs, deleteQuarantineContentName)
		return retain(mapStorageRootPathError(err))
	}
	contentInfo, statErr := content.Stat()
	closeErr := content.Close()
	if statErr != nil || closeErr != nil {
		target.stageAbs = filepath.Join(quarantine.abs, deleteQuarantineContentName)
		return retain(errors.Join(statErr, closeErr))
	}
	if !os.SameFile(target.stageInfo, contentInfo) {
		target.stageAbs = filepath.Join(quarantine.abs, deleteQuarantineContentName)
		return retain(ErrDeleteTargetChanged)
	}

	oldRel := target.stageRel
	target.stageRel = filepath.Join(quarantine.rel, deleteQuarantineContentName)
	target.stageName = storageWorkspaceName(target.stageRel)
	target.stageAbs = filepath.Join(quarantine.abs, deleteQuarantineContentName)
	target.stageInfo = contentInfo
	target.stageIdentity = deleteStageIdentity(contentInfo)
	if target.stageIdentity == "" {
		return retain(ErrDeleteIdentityUnavailable)
	}
	if err := syncStorageManagedRenameDirs(root, oldRel, root, target.stageRel); err != nil {
		return retain(workspace.WrapVisibleMutationWarning(fmt.Errorf("failed to sync delete quarantine rename: %w", err)))
	}
	if err := fs.captureDeleteMountBoundary(fs.workspace.Root()).checkHostTree(target.stageAbs); err != nil {
		return retain(err)
	}
	if _, err := fs.snapshotStagedDeleteLocked(ctx, target); err != nil {
		return retain(err)
	}
	return quarantine, nil
}

func (fs *FileSystem) verifyQuarantinedDeleteEntry(target *stagedDeleteTarget, manifest map[string]stagedDeleteRemovalEntry, verifiedIdentities map[string]struct{}, entryPath string, entryInfo os.FileInfo) error {
	logicalPath, ok := logicalDeleteStagePath("/"+filepath.ToSlash(deleteQuarantineContentName), target.logicalName, "/"+filepath.ToSlash(entryPath))
	if !ok {
		return ErrDeleteTargetChanged
	}
	expected, ok := manifest[logicalPath]
	if !ok || expected.info == nil || !os.SameFile(expected.info, entryInfo) || entryInfo.IsDir() != expected.info.IsDir() || entryInfo.Size() != expected.info.Size() || !entryInfo.ModTime().Equal(expected.info.ModTime()) || storagePreservedMode(entryInfo.Mode()) != storagePreservedMode(expected.info.Mode()) {
		return ErrDeleteTargetChanged
	}
	identity := deleteStageIdentity(entryInfo)
	if identity == "" {
		return ErrDeleteIdentityUnavailable
	}
	if identity != expected.identity {
		if entryInfo.IsDir() {
			return ErrDeleteTargetChanged
		}
		if _, verified := verifiedIdentities[expected.identity]; !verified {
			return ErrDeleteTargetChanged
		}
	}
	if expected.identity == "" {
		return ErrDeleteTargetChanged
	}
	verifiedIdentities[expected.identity] = struct{}{}
	return nil
}

func (fs *FileSystem) stageTrashRollbackContentLocked(ctx context.Context, id, trashContentPath string, content *stagedTrashContent) (*stagedTrashContent, error) {
	if content == nil {
		return nil, ErrDeleteTargetChanged
	}
	if err := beforeTrashRollbackCapture(content.source.Root.Path, content.abs); err != nil {
		return nil, err
	}
	if err := fs.verifyStagedTrashContentLocked(ctx, content); err != nil {
		return nil, &DeleteStageResidualError{Path: content.source.Root.Path, StagePath: content.abs, err: err}
	}
	stageID, err := generateID()
	if err != nil {
		return nil, err
	}
	holdRel := filepath.FromSlash(path.Join(id, ".rollback-"+stageID+".stage"))
	root := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
	holdAbs := storageAbsolutePath(root, holdRel)
	if err := fs.captureDeleteMountBoundary(fs.trashRoot).checkHostTree(trashContentPath); err != nil {
		return nil, err
	}
	rootEntry := content.entries["."]
	holdInfo, moved, err := renameDeleteLeafNoReplace(fs.trashRootHandle, content.rel, holdRel, rootEntry.info, rootEntry.identity)
	if err != nil {
		if moved {
			return nil, &DeleteStageResidualError{Path: content.source.Root.Path, StagePath: holdAbs, err: err}
		}
		return nil, &DeleteStageResidualError{Path: content.source.Root.Path, StagePath: content.abs, err: err}
	}
	oldRel := content.rel
	content.rel = holdRel
	content.abs = holdAbs
	rootEntry.info = holdInfo
	rootEntry.identity = deleteStageIdentity(holdInfo)
	content.entries["."] = rootEntry
	if rootEntry.identity == "" {
		return content, ErrDeleteIdentityUnavailable
	}
	if err := afterTrashRollbackCapture(content.source.Root.Path, content.abs); err != nil {
		return content, err
	}
	if err := fs.verifyStagedTrashContentLocked(ctx, content); err != nil {
		return content, &DeleteStageResidualError{Path: content.source.Root.Path, StagePath: content.abs, err: err}
	}
	if err := syncStorageManagedRenameDirs(root, oldRel, root, holdRel); err != nil {
		return content, workspace.WrapVisibleMutationWarning(fmt.Errorf("failed to sync trash rollback staging: %w", err))
	}
	return content, nil
}

func (fs *FileSystem) restoreStagedTrashRollbackContentLocked(ctx context.Context, id string, staged *stagedTrashContent, trashContentPath string) error {
	if staged == nil {
		return nil
	}
	preVerifyErr := fs.verifyStagedTrashContentLocked(ctx, staged)
	if err := errors.Join(
		fs.captureDeleteMountBoundary(fs.trashRoot).checkHostTree(staged.abs),
		fs.captureDeleteMountBoundary(fs.trashRoot).checkHostTree(trashContentPath),
	); err != nil {
		return err
	}
	contentRel := filepath.FromSlash(path.Join(id, "content"))
	root := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
	rootEntry := staged.entries["."]
	current, err := fs.trashRootHandle.Lstat(staged.rel)
	if err != nil || rootEntry.info == nil || current == nil || !os.SameFile(rootEntry.info, current) {
		return &DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: staged.abs, err: errors.Join(preVerifyErr, mapStorageRootPathError(err), ErrDeleteTargetChanged)}
	}
	currentIdentity := deleteStageIdentity(current)
	if currentIdentity == "" {
		return &DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: staged.abs, err: errors.Join(preVerifyErr, ErrDeleteIdentityUnavailable)}
	}
	restoredInfo, moved, err := renameDeleteLeafNoReplace(fs.trashRootHandle, staged.rel, contentRel, rootEntry.info, currentIdentity)
	if err != nil {
		if moved {
			return &DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: trashContentPath, err: err}
		}
		return &DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: staged.abs, err: err}
	}
	oldRel := staged.rel
	staged.rel = contentRel
	staged.abs = storageAbsolutePath(root, contentRel)
	rootEntry.info = restoredInfo
	rootEntry.identity = deleteStageIdentity(restoredInfo)
	staged.entries["."] = rootEntry
	if rootEntry.identity == "" {
		return &DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: staged.abs, err: ErrDeleteIdentityUnavailable}
	}
	postVerifyErr := fs.verifyStagedTrashContentLocked(ctx, staged)
	syncErr := syncStorageManagedRenameDirs(root, oldRel, root, contentRel)
	if syncErr != nil {
		syncErr = workspace.WrapVisibleMutationWarning(fmt.Errorf("failed to sync restored trash rollback content: %w", syncErr))
	}
	return errors.Join(preVerifyErr, postVerifyErr, syncErr)
}

func (fs *FileSystem) discardStagedTrashRollbackContentLocked(ctx context.Context, staged *stagedTrashContent) error {
	if staged == nil {
		return nil
	}
	if err := fs.verifyStagedTrashContentLocked(ctx, staged); err != nil {
		return &DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: staged.abs, err: err}
	}
	if err := fs.captureDeleteMountBoundary(fs.trashRoot).checkHostTree(staged.abs); err != nil {
		return err
	}
	parentRel := filepath.Dir(staged.rel)
	stageID, err := generateID()
	if err != nil {
		return err
	}
	quarantineRel := filepath.Join(parentRel, ".discard-"+stageID+".quarantine")
	quarantineAbs := storageAbsolutePath(&storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}, quarantineRel)
	if err := rootio.MkdirNoFollow(fs.trashRootHandle, quarantineRel, 0o700); err != nil {
		return mapStorageRootPathError(err)
	}
	quarantineDir, err := rootio.OpenDirNoFollow(fs.trashRootHandle, quarantineRel)
	if err != nil {
		return &DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: quarantineAbs, err: mapStorageRootPathError(err)}
	}
	defer func() {
		if quarantineDir != nil {
			_ = quarantineDir.Close()
		}
	}()
	quarantineInfo, err := quarantineDir.Stat()
	if err != nil {
		return &DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: quarantineAbs, err: err}
	}
	removeEmptyQuarantine := func() error {
		current, err := fs.trashRootHandle.Lstat(quarantineRel)
		if err != nil {
			return mapStorageRootPathError(err)
		}
		opened, err := quarantineDir.Stat()
		if err != nil || !os.SameFile(quarantineInfo, current) || !os.SameFile(opened, current) {
			return errors.Join(ErrDeleteTargetChanged, err)
		}
		if err := quarantineDir.Close(); err != nil {
			return err
		}
		quarantineDir = nil
		if err := fs.trashRootHandle.Remove(quarantineRel); err != nil {
			return mapStorageRootPathError(err)
		}
		if err := syncManagedStorageDir(fs.trashRootHandle, parentRel, storageAbsolutePath(&storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}, parentRel)); err != nil {
			return workspace.WrapVisibleMutationWarning(fmt.Errorf("failed to sync trash discard quarantine cleanup: %w", err))
		}
		return nil
	}
	if err := fs.captureDeleteMountBoundary(fs.trashRoot).checkHostTree(quarantineAbs); err != nil {
		cleanupErr := removeEmptyQuarantine()
		return errors.Join(err, wrapStorageStepError("cleanup empty trash discard quarantine", cleanupErr))
	}
	rootEntry := staged.entries["."]
	current, err := fs.trashRootHandle.Lstat(staged.rel)
	if err != nil || !sameDeleteStageEntry(rootEntry.info, rootEntry.identity, current) {
		if err == nil {
			err = ErrDeleteTargetChanged
		}
		cleanupErr := removeEmptyQuarantine()
		return errors.Join(&DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: staged.abs, err: err}, wrapStorageStepError("cleanup empty trash discard quarantine", cleanupErr))
	}
	if err := rootio.RenameLeafIntoDirNoReplace(fs.trashRootHandle, staged.rel, quarantineDir, deleteQuarantineContentName); err != nil {
		cleanupErr := removeEmptyQuarantine()
		return errors.Join(mapStorageRootPathError(err), wrapStorageStepError("cleanup empty trash discard quarantine", cleanupErr))
	}
	content, err := rootio.OpenDirEntryNoFollow(quarantineDir, deleteQuarantineContentName)
	if err != nil {
		return &DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: filepath.Join(quarantineAbs, deleteQuarantineContentName), err: mapStorageRootPathError(err)}
	}
	contentInfo, statErr := content.Stat()
	closeErr := content.Close()
	if statErr != nil || closeErr != nil || !os.SameFile(rootEntry.info, contentInfo) {
		return &DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: filepath.Join(quarantineAbs, deleteQuarantineContentName), err: errors.Join(ErrDeleteTargetChanged, statErr, closeErr)}
	}
	staged.rel = filepath.Join(quarantineRel, deleteQuarantineContentName)
	staged.abs = filepath.Join(quarantineAbs, deleteQuarantineContentName)
	rootEntry.info = contentInfo
	rootEntry.identity = deleteStageIdentity(contentInfo)
	staged.entries["."] = rootEntry
	if rootEntry.identity == "" {
		return &DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: staged.abs, err: ErrDeleteIdentityUnavailable}
	}
	if err := fs.verifyStagedTrashContentLocked(ctx, staged); err != nil {
		return &DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: staged.abs, err: err}
	}
	if err := fs.captureDeleteMountBoundary(fs.trashRoot).checkHostTree(staged.abs); err != nil {
		return &DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: staged.abs, err: err}
	}
	if err := rootio.RemoveAllFromDirNoFollowChecked(quarantineDir, deleteQuarantineContentName, func(entryPath string, info os.FileInfo) error {
		suffix := "."
		if entryPath != deleteQuarantineContentName {
			suffix = strings.TrimPrefix(entryPath, deleteQuarantineContentName+string(filepath.Separator))
		}
		expected, ok := staged.entries[suffix]
		if !ok || !sameDeleteStageEntry(expected.info, expected.identity, info) {
			return ErrDeleteTargetChanged
		}
		return nil
	}); err != nil {
		return &DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: staged.abs, err: mapStorageRootPathError(err)}
	}
	staged.rel = ""
	staged.abs = ""
	if err := removeEmptyQuarantine(); err != nil {
		if workspace.IsVisibleMutationWarning(err) || isVisibleMutationWarning(err) {
			return err
		}
		return &DeleteStageResidualError{Path: staged.source.Root.Path, StagePath: quarantineAbs, err: err}
	}
	return nil
}

func (fs *FileSystem) rollbackStagedTrashDeleteLocked(ctx context.Context, target *stagedDeleteTarget, copied *stagedTrashContent, info *FileInfo, id, trashContentPath string, restoreIndex bool, cause error) error {
	contentErr := fs.rollbackStagedDeleteLocked(target, nil)
	if contentErr != nil {
		return errors.Join(
			cause,
			wrapStorageStepError("rollback staged deleted content", contentErr),
		)
	}
	stagedTrash, trashStageErr := fs.stageTrashRollbackContentLocked(ctx, id, trashContentPath, copied)
	var metadataErr error
	if trashStageErr == nil && stagedTrash != nil {
		metadataErr = fs.removeTrashMetadata(ctx, id)
	}
	var trashFinalizeErr error
	if stagedTrash == nil {
		// Keep metadata and content paired when content could not be staged.
	} else if trashStageErr != nil {
		trashFinalizeErr = fs.restoreStagedTrashRollbackContentLocked(ctx, id, stagedTrash, trashContentPath)
	} else if metadataErr != nil {
		trashFinalizeErr = fs.restoreStagedTrashRollbackContentLocked(ctx, id, stagedTrash, trashContentPath)
	} else {
		trashFinalizeErr = fs.discardStagedTrashRollbackContentLocked(ctx, stagedTrash)
	}
	// This rollback path does not retain an identity-bearing handle for the
	// item directory. Leave an empty directory instead of deleting by path.
	var restoreIndexErr error
	if restoreIndex && trashStageErr == nil && metadataErr == nil && trashFinalizeErr == nil {
		restoreIndexErr = fs.restoreDeletedIndexEntries(ctx, target.logicalName, info)
	}
	return errors.Join(
		cause,
		wrapStorageStepError("rollback staged deleted content", contentErr),
		wrapStorageStepError("stage copied trash content for rollback", trashStageErr),
		wrapStorageStepError("rollback trash metadata", metadataErr),
		wrapStorageStepError("finalize copied trash rollback content", trashFinalizeErr),
		wrapStorageStepError("restore file index", restoreIndexErr),
	)
}

func (fs *FileSystem) commitStagedTrashDeleteLocked(ctx context.Context, target *stagedDeleteTarget, snapshot DeleteTargetSnapshot, policy DeletePolicy) error {
	targetSize := deleteSnapshotTargetSize(snapshot)
	hadVersions, err := fs.deleteSnapshotHadVersions(ctx, snapshot)
	if err != nil {
		return fs.rollbackStagedDeleteLocked(target, err)
	}
	id, err := generateID()
	if err != nil {
		return fs.rollbackStagedDeleteLocked(target, fmt.Errorf("generate trash ID: %w", err))
	}
	trashContentPath := path.Join(fs.trashRoot, id, "content")
	copied, err := fs.copyStagedDeleteToTrashLocked(ctx, target, trashContentPath)
	if err != nil {
		return fs.rollbackStagedDeleteLocked(target, fmt.Errorf("failed to copy staged target to trash: %w", err))
	}

	deletedAt := time.Now()
	trashItem := &versionstore.TrashItem{
		ID:           id,
		OriginalPath: target.logicalName,
		Size:         targetSize,
		DeletedAt:    deletedAt,
		ExpiresAt:    deletedAt.AddDate(0, 0, policy.TrashRetentionDays),
		IsDir:        snapshot.Root.IsDir,
		HadVersions:  hadVersions,
	}
	rollbackInfo := snapshot.Root
	if err := fs.addTrashMetadata(ctx, trashItem); err != nil {
		rollbackErr := fs.rollbackStagedDeleteLocked(target, nil)
		var cleanupErr error
		if rollbackErr == nil {
			cleanupErr = fs.discardStagedTrashRollbackContentLocked(ctx, copied)
		}
		return errors.Join(
			fmt.Errorf("failed to add to trash: %w", err),
			wrapStorageStepError("rollback staged deleted content", rollbackErr),
			wrapStorageStepError("cleanup copied trash content", cleanupErr),
		)
	}
	if err := fs.deleteIndexEntriesForDeleteTarget(ctx, target.logicalName, snapshot.Root.IsDir); err != nil {
		return fs.rollbackStagedTrashDeleteLocked(ctx, target, copied, &rollbackInfo, id, trashContentPath, false, fmt.Errorf("failed to delete file index: %w", err))
	}

	persistenceWarning := target.warning
	var cleanupWarning error
	hookResult, err := fs.notifyPathDeleted(ctx, target.logicalName)
	if err != nil {
		if workspace.IsVisibleMutationWarning(err) || isVisibleMutationWarning(err) {
			persistenceWarning = errors.Join(persistenceWarning, fmt.Errorf("failed to sync delete hooks: %w", err))
		} else {
			return fs.rollbackStagedTrashDeleteLocked(ctx, target, copied, &rollbackInfo, id, trashContentPath, true, fmt.Errorf("failed to sync delete hooks: %w", err))
		}
	}
	if hookResult != nil && len(hookResult.RestoreData) > 0 {
		if err := fs.updateTrashRestoreData(ctx, id, hookResult.RestoreData); err != nil {
			rollbackErr := fs.rollbackStagedTrashDeleteLocked(ctx, target, copied, &rollbackInfo, id, trashContentPath, true, fmt.Errorf("failed to persist trash restore metadata: %w", err))
			if hookResult.Rollback != nil {
				rollbackErr = errors.Join(rollbackErr, wrapStorageStepError("rollback delete hooks", hookResult.Rollback()))
			}
			return rollbackErr
		}
	}

	if err := fs.removeStagedDeleteTargetLocked(context.WithoutCancel(ctx), target, false, copied); err != nil {
		var residual *DeleteStageResidualError
		persistenceErr := stagedDeletePersistenceWarnings(err)
		if errors.As(err, &residual) {
			cleanupWarning = errors.Join(cleanupWarning, wrapDeleteCleanupWarning(fmt.Errorf("failed to cleanup staged delete source: %w", err)))
		} else if persistenceErr == nil {
			cleanupWarning = errors.Join(cleanupWarning, wrapDeleteCleanupWarning(fmt.Errorf("failed to cleanup staged delete source: %w", err)))
		}
		if persistenceErr != nil {
			persistenceWarning = errors.Join(persistenceWarning, persistenceErr)
		}
	}
	if err := fs.ensureTrashCapacityLocked(ctx, 0, id); err != nil {
		cleanupWarning = errors.Join(cleanupWarning, wrapTrashDeleteWarning(fmt.Errorf("failed to enforce trash capacity: %w", err)))
	}
	if persistenceWarning != nil || cleanupWarning != nil {
		return errors.Join(wrapVisibleMutationWarning(persistenceWarning), cleanupWarning)
	}
	return nil
}

func (fs *FileSystem) rollbackStagedPermanentDeleteLocked(ctx context.Context, target *stagedDeleteTarget, snapshot DeleteTargetSnapshot, restoreIndex bool, rollbackHook *PathDeleteHookResult, cause error) error {
	pathErr := fs.rollbackStagedDeleteLocked(target, nil)
	var hookErr error
	if rollbackHook != nil && rollbackHook.Rollback != nil {
		hookErr = rollbackHook.Rollback()
	}
	var indexErr error
	if restoreIndex && pathErr == nil {
		info := snapshot.Root
		indexErr = fs.restoreDeletedIndexEntries(ctx, target.logicalName, &info)
	}
	return errors.Join(
		cause,
		wrapStorageStepError("rollback staged deleted path", pathErr),
		wrapStorageStepError("rollback deleted-path hooks", hookErr),
		wrapStorageStepError("restore file index", indexErr),
	)
}

func (fs *FileSystem) commitStagedPermanentDeleteLocked(ctx context.Context, target *stagedDeleteTarget, snapshot DeleteTargetSnapshot) error {
	var versionHashes []string
	if !snapshot.Root.IsDir {
		versions, err := fs.versions.GetVersions(ctx, target.logicalName)
		if err != nil {
			return fs.rollbackStagedDeleteLocked(target, err)
		}
		versionHashes = make([]string, 0, len(versions))
		for _, version := range versions {
			versionHashes = append(versionHashes, version.Hash)
		}
	}

	if err := fs.deleteFileIndex(ctx, target.logicalName); err != nil {
		return fs.rollbackStagedPermanentDeleteLocked(ctx, target, snapshot, false, nil, fmt.Errorf("failed to delete file index: %w", err))
	}
	rollbackHook, err := fs.notifyPathDeleted(ctx, target.logicalName)
	persistenceWarning := target.warning
	var cleanupWarning error
	if err != nil {
		if workspace.IsVisibleMutationWarning(err) || isVisibleMutationWarning(err) {
			persistenceWarning = errors.Join(persistenceWarning, fmt.Errorf("failed to sync delete hooks: %w", err))
		} else {
			return fs.rollbackStagedPermanentDeleteLocked(ctx, target, snapshot, true, nil, fmt.Errorf("failed to sync delete hooks: %w", err))
		}
	}
	if !snapshot.Root.IsDir {
		if err := fs.versions.DeleteVersions(ctx, target.logicalName); err != nil {
			return fs.rollbackStagedPermanentDeleteLocked(ctx, target, snapshot, true, rollbackHook, fmt.Errorf("failed to delete version metadata: %w", err))
		}
	}

	if err := fs.removeStagedDeleteTargetLocked(context.WithoutCancel(ctx), target, true, nil); err != nil {
		var residual *DeleteStageResidualError
		persistenceErr := stagedDeletePersistenceWarnings(err)
		if errors.As(err, &residual) {
			cleanupWarning = errors.Join(cleanupWarning, wrapDeleteCleanupWarning(fmt.Errorf("failed to cleanup permanently deleted stage: %w", err)))
		} else if persistenceErr == nil {
			cleanupWarning = errors.Join(cleanupWarning, wrapDeleteCleanupWarning(fmt.Errorf("failed to cleanup permanently deleted stage: %w", err)))
		}
		if persistenceErr != nil {
			persistenceWarning = errors.Join(persistenceWarning, persistenceErr)
		}
	}
	if !snapshot.Root.IsDir {
		if err := fs.deleteUnreferencedVersionObjects(ctx, versionHashes); err != nil {
			cleanupWarning = errors.Join(cleanupWarning, wrapDeleteCleanupWarning(fmt.Errorf("failed to delete version objects: %w", err)))
		}
	}
	if persistenceWarning != nil || cleanupWarning != nil {
		return errors.Join(wrapVisibleMutationWarning(persistenceWarning), cleanupWarning)
	}
	return nil
}

func stagedDeletePersistenceWarnings(err error) error {
	if err == nil {
		return nil
	}
	switch warning := err.(type) {
	case *VisibleMutationWarningError:
		return warning
	case *workspace.VisibleMutationWarningError:
		return warning
	case interface{ Unwrap() []error }:
		children := warning.Unwrap()
		persistenceWarnings := make([]error, 0, len(children))
		for _, child := range children {
			if persistenceWarning := stagedDeletePersistenceWarnings(child); persistenceWarning != nil {
				persistenceWarnings = append(persistenceWarnings, persistenceWarning)
			}
		}
		return errors.Join(persistenceWarnings...)
	case interface{ Unwrap() error }:
		return stagedDeletePersistenceWarnings(warning.Unwrap())
	default:
		return nil
	}
}
