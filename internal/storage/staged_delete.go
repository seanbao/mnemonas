package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/workspace"
)

var (
	afterDeleteWitnessOpen       = func(string) error { return nil }
	afterDeleteLeafRename        = func(string, string) error { return nil }
	afterDeleteStageCapture      = func(string, string) error { return nil }
	afterDeleteTrashCopy         = func(string, string) error { return nil }
	afterDeleteTrashContentHash  = func(string, string, string) error { return nil }
	beforeDeleteStageRemoval     = func(string, string) error { return nil }
	afterDeleteQuarantineCapture = func(string, string) error { return nil }
	syncDeleteWitnessRecoveryDir = func(root *os.Root, relName, absPath string) error {
		return syncManagedStorageDir(root, relName, absPath)
	}
)

// DeleteStageResidualError reports that an exact staged object could not be
// restored without replacing a newer object at the original path.
type DeleteStageResidualError struct {
	Path            string
	StagePath       string
	InspectionPaths []string
	err             error
}

func (e *DeleteStageResidualError) Error() string {
	message := ""
	if e.StagePath != "" {
		message = fmt.Sprintf("delete target retained in internal staging: %s (%s)", e.Path, e.StagePath)
	} else {
		message = fmt.Sprintf("delete target recovery requires manual inspection: %s", e.Path)
	}
	if len(e.InspectionPaths) > 0 {
		message += "; inspect: " + strings.Join(e.InspectionPaths, ", ")
	}
	return message
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

func newDeleteRecoveryRelativeName(originalRel string) (string, error) {
	rel, err := newDeleteStageRelativeName(originalRel)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(rel, ".stage") + ".recovery", nil
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
	return fs.stageDeleteTargetAtLocked(ctx, name, expected, beforeCapture, "")
}

func (fs *FileSystem) stageDeleteTargetAtLocked(ctx context.Context, name string, expected DeleteTargetSnapshot, beforeCapture func() error, plannedStageRel string) (*stagedDeleteTarget, error) {
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
	if err := fs.captureDeleteWitnessContentHashLocked(ctx, target); err != nil {
		return abort(err)
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
	attempts := 32
	if plannedStageRel != "" {
		plannedStageRel = filepath.Clean(plannedStageRel)
		if filepath.IsAbs(plannedStageRel) || plannedStageRel == "." || filepath.Dir(plannedStageRel) != filepath.Dir(target.originalRel) {
			return abort(ErrDeleteTargetChanged)
		}
		attempts = 1
	}
	for range attempts {
		stageRel := plannedStageRel
		if stageRel == "" {
			var err error
			stageRel, err = newDeleteStageRelativeName(target.originalRel)
			if err != nil {
				return abort(err)
			}
		}
		stageAbs := storageAbsolutePath(root, stageRel)
		if err := fs.captureDeleteMountBoundary(fs.workspace.Root()).checkHostTree(fs.workspace.FullPath(name)); err != nil {
			return abort(err)
		}
		stageInfo, moved, err := renameDeleteLeafNoReplace(fs.filesRootHandle, target.originalRel, stageRel, witnessInfo, witnessIdentity)
		if err != nil {
			if moved {
				target.stageRel = stageRel
				target.stageName = storageWorkspaceName(stageRel)
				target.stageAbs = stageAbs
				target.stageInfo = stageInfo
				target.stageIdentity = deleteStageIdentity(stageInfo)
				var recoveryPath string
				var recoveryErr error
				if target.witnessInfo.Mode().IsRegular() {
					recoveryPath, recoveryErr = fs.preserveDeleteWitnessRecoveryLocked(target)
				} else {
					recoveryErr = errors.New("post-rename directory witness has no verified recovery path")
				}
				inspectionPaths := []string{stageAbs}
				var recoveryFailure *deleteWitnessRecoveryError
				if errors.As(recoveryErr, &recoveryFailure) {
					inspectionPaths = append(inspectionPaths, recoveryFailure.paths...)
				}
				closeErr := target.close()
				return nil, errors.Join(
					&DeleteTargetChangedError{Path: name},
					&DeleteStageResidualError{Path: name, StagePath: recoveryPath, InspectionPaths: inspectionPaths, err: errors.Join(
						err,
						recoveryErr,
						closeErr,
						fmt.Errorf("unknown post-rename entry retained at %s", stageAbs),
					)},
				)
			}
			if errors.Is(err, os.ErrExist) {
				if plannedStageRel != "" {
					return abort(ErrAlreadyExists)
				}
				continue
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

func (fs *FileSystem) captureDeleteWitnessContentHashLocked(ctx context.Context, target *stagedDeleteTarget) error {
	if target == nil || target.witness == nil || target.witnessInfo == nil || !target.witnessInfo.Mode().IsRegular() || target.expected.Root.ContentHash != "" {
		return nil
	}
	before, err := target.witness.Stat()
	if err != nil || !sameStorageCopySource(target.witnessInfo, before) {
		return errors.Join(ErrDeleteTargetChanged, err)
	}
	current, err := fs.filesRootHandle.Lstat(target.originalRel)
	if err != nil || !sameStorageCopySource(before, current) {
		return errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	hash, err := hashOpenWorkspaceFileContext(ctx, target.witness)
	if err != nil {
		return err
	}
	after, statErr := target.witness.Stat()
	current, pathErr := fs.filesRootHandle.Lstat(target.originalRel)
	if statErr != nil || pathErr != nil || !sameStorageCopySource(before, after) || !sameStorageCopySource(after, current) {
		return errors.Join(ErrDeleteTargetChanged, statErr, mapStorageRootPathError(pathErr))
	}
	target.expected.Root.ContentHash = hash
	rootUpdated := false
	for i := range target.expected.Entries {
		if target.expected.Entries[i].Path == target.logicalName {
			target.expected.Entries[i].ContentHash = hash
			rootUpdated = true
			break
		}
	}
	if !rootUpdated {
		return ErrDeleteTargetChanged
	}
	return nil
}

type deleteWitnessRecoveryError struct {
	paths []string
	err   error
}

func (e *deleteWitnessRecoveryError) Error() string {
	return "delete witness recovery requires inspection"
}

func (e *deleteWitnessRecoveryError) Unwrap() error {
	return e.err
}

func (fs *FileSystem) preserveDeleteWitnessRecoveryLocked(target *stagedDeleteTarget) (string, error) {
	if target == nil || target.witness == nil || target.witnessInfo == nil || !target.witnessInfo.Mode().IsRegular() {
		return "", ErrDeleteTargetChanged
	}
	if target.expected.Root.ContentHash == "" {
		return "", errors.New("delete witness recovery requires a captured content hash")
	}
	root := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	for range 32 {
		recoveryRel, err := newDeleteRecoveryRelativeName(target.originalRel)
		if err != nil {
			return "", err
		}
		recoveryAbs := storageAbsolutePath(root, recoveryRel)
		recovery, err := rootio.OpenFileNoFollow(fs.filesRootHandle, recoveryRel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", mapStorageRootPathError(err)
		}
		failRecovery := func(cause error) (string, error) {
			closeErr := recovery.Close()
			return "", &deleteWitnessRecoveryError{paths: []string{recoveryAbs}, err: errors.Join(cause, closeErr)}
		}
		before, err := target.witness.Stat()
		if err != nil || !os.SameFile(target.witnessInfo, before) || target.witnessInfo.Mode() != before.Mode() || target.witnessInfo.Size() != before.Size() || !target.witnessInfo.ModTime().Equal(before.ModTime()) {
			return failRecovery(errors.Join(ErrDeleteTargetChanged, err))
		}
		if _, err := target.witness.Seek(0, io.SeekStart); err != nil {
			return failRecovery(err)
		}
		if err := recovery.Chmod(storagePreservedMode(before.Mode())); err != nil {
			return failRecovery(err)
		}
		copied, copyErr := io.Copy(recovery, target.witness)
		syncErr := recovery.Sync()
		after, statErr := target.witness.Stat()
		recoveryInfo, recoveryStatErr := recovery.Stat()
		if copyErr != nil || syncErr != nil || statErr != nil || recoveryStatErr != nil || copied != before.Size() || !sameStorageCopySource(before, after) || recoveryInfo.Size() != copied {
			return failRecovery(errors.Join(ErrDeleteTargetChanged, copyErr, syncErr, statErr, recoveryStatErr))
		}
		if closeErr := recovery.Close(); closeErr != nil {
			return "", &deleteWitnessRecoveryError{paths: []string{recoveryAbs}, err: closeErr}
		}
		current, err := fs.filesRootHandle.Lstat(recoveryRel)
		if err != nil || !sameStorageCopySource(recoveryInfo, current) {
			return "", &deleteWitnessRecoveryError{paths: []string{recoveryAbs}, err: errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))}
		}
		verification, err := rootio.OpenRegularFileNoFollow(fs.filesRootHandle, recoveryRel)
		if err != nil {
			return "", &deleteWitnessRecoveryError{paths: []string{recoveryAbs}, err: errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))}
		}
		openedInfo, statErr := verification.Stat()
		hashRecoveryFile := fs.hashDeleteWitnessRecoveryFile
		if hashRecoveryFile == nil {
			hashRecoveryFile = hashOpenWorkspaceFile
		}
		hash, hashErr := hashRecoveryFile(verification)
		afterHashInfo, afterHashStatErr := verification.Stat()
		closeErr := verification.Close()
		current, currentErr := fs.filesRootHandle.Lstat(recoveryRel)
		if statErr != nil || hashErr != nil || afterHashStatErr != nil || closeErr != nil || currentErr != nil ||
			!sameStorageCopySource(recoveryInfo, openedInfo) || !sameStorageCopySource(openedInfo, afterHashInfo) ||
			!sameStorageCopySource(afterHashInfo, current) || hash != target.expected.Root.ContentHash {
			return "", &deleteWitnessRecoveryError{paths: []string{recoveryAbs}, err: errors.Join(ErrDeleteTargetChanged, statErr, hashErr, afterHashStatErr, closeErr, mapStorageRootPathError(currentErr))}
		}
		if err := syncDeleteWitnessRecoveryDir(fs.filesRootHandle, filepath.Dir(recoveryRel), storageAbsolutePath(root, filepath.Dir(recoveryRel))); err != nil {
			return "", &deleteWitnessRecoveryError{
				paths: []string{recoveryAbs},
				err:   fmt.Errorf("failed to sync delete witness recovery file: %w", err),
			}
		}
		current, err = fs.filesRootHandle.Lstat(recoveryRel)
		if err != nil || !sameStorageCopySource(recoveryInfo, current) {
			return "", &deleteWitnessRecoveryError{paths: []string{recoveryAbs}, err: errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))}
		}
		return recoveryAbs, nil
	}
	return "", errors.New("failed to allocate delete witness recovery path")
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
	return actual != nil && actual.IsDir == expected.IsDir && actual.Mode == expected.Mode && actual.Size == expected.Size && actual.ModTime.Equal(expected.ModTime)
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
		hashFile := fs.hashStagedDeleteSourceFile
		if hashFile == nil {
			hashFile = hashOpenWorkspaceFileContext
		}
		hash, err = hashFile(ctx, file)
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

func (fs *FileSystem) verifyStagedDeleteMetadataLocked(ctx context.Context, target *stagedDeleteTarget, expected DeleteTargetSnapshot) error {
	metadataTarget := *target
	metadataTarget.expected = projectDeleteTargetSnapshot(expected, DeleteTargetSnapshotOptions{IncludeDescendants: true})
	_, err := fs.snapshotStagedDeleteLocked(ctx, &metadataTarget)
	return err
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
			Mode:                info.Mode,
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
			actualToken, actualErr := deleteTreeTokenV3(comparison)
			expectedToken, expectedErr := deleteTreeTokenV3(target.expected)
			if actualErr != nil || expectedErr != nil || actualToken == "" || expectedToken == "" || actualToken != expectedToken {
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
