package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/workspace"
)

const (
	trashTransferOwnershipMarkerPrefix  = ".mnemonas-trash-transfer-owner-"
	trashTransferOwnershipMarkerVersion = 1
	trashTransferOwnershipMarkerMaxSize = 4096

	trashTransferOwnershipRoleTrashContainer     = "trash-container"
	trashTransferOwnershipRoleWorkspaceContainer = "workspace-container"
	trashTransferOwnershipRoleWorkspaceParent    = "workspace-parent"
)

var (
	beforeTrashTransferOwnedContainerRemoval   = func(string) error { return nil }
	syncTrashTransferOwnershipMarkerCleanupDir = func(dir *os.File, _ string) error { return dir.Sync() }
)

type trashTransferOwnershipMarker struct {
	Version             int    `json:"version"`
	OperationID         string `json:"operation_id"`
	Role                string `json:"role"`
	RelativePath        string `json:"relative_path"`
	PersistentIdentity  string `json:"persistent_identity"`
	PreparedJournalHash string `json:"prepared_journal_hash"`
}

func trashTransferOwnershipMarkerName(operationID string) (string, error) {
	if !validTrashPurgeOperationID(operationID) {
		return "", ErrDeleteTargetChanged
	}
	name := trashTransferOwnershipMarkerPrefix + operationID
	if filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return "", ErrDeleteTargetChanged
	}
	return name, nil
}

func trashTransferPreparedOwnershipRecord(record *trashTransferJournalRecord) (*trashTransferJournalRecord, error) {
	if record == nil || (record.Decision != trashTransferPrepared && record.Decision != trashTransferCopying && record.Decision != trashTransferReady) {
		return nil, ErrDeleteTargetChanged
	}
	prepared := *record
	prepared.Decision = trashTransferPrepared
	prepared.ReplicaManifest = nil
	prepared.TrashStageIdentity = ""
	prepared.WorkspaceStageIdentity = ""
	prepared.WorkspaceParentDirs = append([]trashTransferWorkspaceParentDir(nil), record.WorkspaceParentDirs...)
	for index := range prepared.WorkspaceParentDirs {
		prepared.WorkspaceParentDirs[index].Identity = ""
	}
	if err := validateTrashTransferJournalRecord(&prepared, trashTransferPrepared); err != nil {
		return nil, err
	}
	return &prepared, nil
}

func trashTransferPreparedOwnershipHash(record *trashTransferJournalRecord) (string, error) {
	prepared, err := trashTransferPreparedOwnershipRecord(record)
	if err != nil {
		return "", err
	}
	return trashTransferJournalHash(prepared)
}

func trashTransferOwnedContentRel(containerRel string) string {
	return filepath.Join(containerRel, "content")
}

func expectedTrashTransferOwnershipMarker(
	record *trashTransferJournalRecord,
	role string,
	rel string,
	identity string,
) (trashTransferOwnershipMarker, error) {
	rel = filepath.Clean(rel)
	if rel == "." || filepath.IsAbs(rel) || !validTrashPurgeContentHash(identity) {
		return trashTransferOwnershipMarker{}, ErrDeleteTargetChanged
	}
	switch role {
	case trashTransferOwnershipRoleTrashContainer,
		trashTransferOwnershipRoleWorkspaceContainer,
		trashTransferOwnershipRoleWorkspaceParent:
	default:
		return trashTransferOwnershipMarker{}, ErrDeleteTargetChanged
	}
	preparedHash, err := trashTransferPreparedOwnershipHash(record)
	if err != nil {
		return trashTransferOwnershipMarker{}, err
	}
	return trashTransferOwnershipMarker{
		Version:             trashTransferOwnershipMarkerVersion,
		OperationID:         record.OperationID,
		Role:                role,
		RelativePath:        filepath.ToSlash(rel),
		PersistentIdentity:  identity,
		PreparedJournalHash: preparedHash,
	}, nil
}

func canonicalTrashTransferOwnershipMarker(marker trashTransferOwnershipMarker) ([]byte, error) {
	data, err := json.Marshal(marker)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if len(data) == 0 || len(data) > trashTransferOwnershipMarkerMaxSize {
		return nil, errors.New("Trash transfer ownership marker exceeds size limit")
	}
	return data, nil
}

func (fs *FileSystem) writeTrashTransferOwnershipMarker(
	root *storagePathRoot,
	containerRel string,
	record *trashTransferJournalRecord,
	role string,
	expectedIdentity string,
) error {
	containerRel = filepath.Clean(containerRel)
	if root == nil || root.handle == nil || record == nil {
		return ErrDeleteTargetChanged
	}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return err
	}
	containerInfo, err := root.handle.Lstat(containerRel)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	if !containerInfo.IsDir() || containerInfo.Mode()&os.ModeSymlink != 0 || workspace.PersistentIdentityTokenForFileInfo(containerInfo) != expectedIdentity {
		return ErrDeleteTargetChanged
	}
	marker, err := expectedTrashTransferOwnershipMarker(record, role, containerRel, expectedIdentity)
	if err != nil {
		return err
	}
	data, err := canonicalTrashTransferOwnershipMarker(marker)
	if err != nil {
		return err
	}
	markerName, err := trashTransferOwnershipMarkerName(record.OperationID)
	if err != nil {
		return err
	}
	markerRel := filepath.Join(containerRel, markerName)
	file, err := rootio.OpenFileNoFollow(root.handle, markerRel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return mapStorageCreateTargetError(mapStorageRootPathError(err))
	}
	written, err := file.Write(data)
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("write Trash transfer ownership marker: %w", err)
	}
	if written != len(data) {
		_ = file.Close()
		return fmt.Errorf("write Trash transfer ownership marker: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync Trash transfer ownership marker: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Trash transfer ownership marker: %w", err)
	}
	if err := syncManagedStorageDir(root.handle, containerRel, storageAbsolutePath(root, containerRel)); err != nil {
		return fmt.Errorf("sync Trash transfer ownership marker directory: %w", err)
	}
	current, err := root.handle.Lstat(containerRel)
	if err != nil || !os.SameFile(containerInfo, current) || workspace.PersistentIdentityTokenForFileInfo(current) != expectedIdentity {
		return errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	if _, _, err := fs.readTrashTransferOwnershipMarker(root, containerRel, record, role, expectedIdentity); err != nil {
		return fmt.Errorf("verify Trash transfer ownership marker: %w", err)
	}
	return fs.checkTrashTransferRootIdentity(root)
}

func (fs *FileSystem) readTrashTransferOwnershipMarker(
	root *storagePathRoot,
	containerRel string,
	record *trashTransferJournalRecord,
	role string,
	expectedIdentity string,
) (os.FileInfo, os.FileInfo, error) {
	containerRel = filepath.Clean(containerRel)
	if root == nil || root.handle == nil || record == nil || containerRel == "." || filepath.IsAbs(containerRel) {
		return nil, nil, ErrDeleteTargetChanged
	}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return nil, nil, err
	}
	containerInfo, err := root.handle.Lstat(containerRel)
	if err != nil {
		return nil, nil, mapStorageRootPathError(err)
	}
	identity := workspace.PersistentIdentityTokenForFileInfo(containerInfo)
	if !containerInfo.IsDir() || containerInfo.Mode()&os.ModeSymlink != 0 || !validTrashPurgeContentHash(identity) || (expectedIdentity != "" && identity != expectedIdentity) {
		return nil, nil, ErrDeleteTargetChanged
	}
	expected, err := expectedTrashTransferOwnershipMarker(record, role, containerRel, identity)
	if err != nil {
		return nil, nil, err
	}
	markerName, err := trashTransferOwnershipMarkerName(record.OperationID)
	if err != nil {
		return nil, nil, err
	}
	markerRel := filepath.Join(containerRel, markerName)
	markerInfo, err := root.handle.Lstat(markerRel)
	if err != nil {
		return nil, nil, mapStorageRootPathError(err)
	}
	if !markerInfo.Mode().IsRegular() || markerInfo.Mode()&os.ModeSymlink != 0 || markerInfo.Mode().Perm() != 0o600 || markerInfo.Size() <= 0 || markerInfo.Size() > trashTransferOwnershipMarkerMaxSize {
		return nil, nil, ErrNotRegular
	}
	markerIdentity := deleteStageIdentity(markerInfo)
	if markerIdentity == "" {
		return nil, nil, ErrDeleteIdentityUnavailable
	}
	file, err := rootio.OpenRegularFileNoFollow(root.handle, markerRel)
	if err != nil {
		return nil, nil, mapStorageRootPathError(err)
	}
	opened, statErr := file.Stat()
	data, readErr := io.ReadAll(io.LimitReader(file, trashTransferOwnershipMarkerMaxSize+1))
	after, afterErr := file.Stat()
	closeErr := file.Close()
	currentMarker, pathErr := root.handle.Lstat(markerRel)
	if statErr != nil || readErr != nil || afterErr != nil || closeErr != nil || pathErr != nil ||
		!sameDeleteStageEntry(markerInfo, markerIdentity, opened) || !sameStorageFileObject(markerInfo, opened) ||
		!sameDeleteStageEntry(markerInfo, markerIdentity, after) || !sameStorageFileObject(markerInfo, after) ||
		!sameDeleteStageEntry(markerInfo, markerIdentity, currentMarker) || !sameStorageFileObject(markerInfo, currentMarker) {
		return nil, nil, errors.Join(ErrDeleteTargetChanged, statErr, readErr, afterErr, closeErr, mapStorageRootPathError(pathErr))
	}
	if len(data) == 0 || len(data) > trashTransferOwnershipMarkerMaxSize {
		return nil, nil, errors.New("Trash transfer ownership marker exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var marker trashTransferOwnershipMarker
	if err := decoder.Decode(&marker); err != nil {
		return nil, nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, nil, errors.New("Trash transfer ownership marker contains trailing data")
	}
	canonical, err := canonicalTrashTransferOwnershipMarker(marker)
	if err != nil || !bytes.Equal(data, canonical) || marker != expected {
		return nil, nil, errors.Join(ErrDeleteTargetChanged, err, errors.New("Trash transfer ownership marker is not canonical or does not match its prepared journal"))
	}
	currentContainer, err := root.handle.Lstat(containerRel)
	if err != nil || !os.SameFile(containerInfo, currentContainer) || workspace.PersistentIdentityTokenForFileInfo(currentContainer) != identity {
		return nil, nil, errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return nil, nil, err
	}
	return markerInfo, containerInfo, nil
}

func (fs *FileSystem) removeTrashTransferOwnershipMarker(
	root *storagePathRoot,
	containerRel string,
	record *trashTransferJournalRecord,
	role string,
	expectedIdentity string,
	allowMissing bool,
) error {
	containerRel = filepath.Clean(containerRel)
	if root == nil || root.handle == nil || record == nil || containerRel == "." || filepath.IsAbs(containerRel) {
		return ErrDeleteTargetChanged
	}
	markerName, err := trashTransferOwnershipMarkerName(record.OperationID)
	if err != nil {
		return err
	}
	markerRel := filepath.Join(containerRel, markerName)
	if _, err := root.handle.Lstat(markerRel); err != nil {
		if allowMissing && errors.Is(err, os.ErrNotExist) {
			containerInfo, containerErr := root.handle.Lstat(containerRel)
			if containerErr != nil || !containerInfo.IsDir() || containerInfo.Mode()&os.ModeSymlink != 0 || workspace.PersistentIdentityTokenForFileInfo(containerInfo) != expectedIdentity {
				return errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(containerErr))
			}
			return nil
		}
		return mapStorageRootPathError(err)
	}
	markerInfo, containerInfo, err := fs.readTrashTransferOwnershipMarker(root, containerRel, record, role, expectedIdentity)
	if err != nil {
		return err
	}
	markerIdentity := deleteStageIdentity(markerInfo)
	dir, err := rootio.OpenDirNoFollow(root.handle, containerRel)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	defer dir.Close()
	opened, err := dir.Stat()
	if err != nil || !os.SameFile(containerInfo, opened) || workspace.PersistentIdentityTokenForFileInfo(opened) != expectedIdentity {
		return errors.Join(ErrDeleteTargetChanged, err)
	}
	if err := rootio.RemoveAllFromDirNoFollowCheckedInPlace(dir, markerName, func(entryPath string, current os.FileInfo) error {
		if entryPath != markerName || !sameDeleteStageEntry(markerInfo, markerIdentity, current) || !sameStorageFileObject(markerInfo, current) {
			return rootio.ErrEntryChanged
		}
		return nil
	}); err != nil {
		return mapStorageRootPathError(err)
	}
	if err := syncTrashTransferOwnershipMarkerCleanupDir(dir, role); err != nil {
		return fmt.Errorf("sync Trash transfer ownership marker removal: %w", err)
	}
	current, err := root.handle.Lstat(containerRel)
	if err != nil || !os.SameFile(containerInfo, current) || workspace.PersistentIdentityTokenForFileInfo(current) != expectedIdentity {
		return errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	return fs.checkTrashTransferRootIdentity(root)
}

func isStorageCopyPublishTempName(name string) bool {
	const prefix = ".storage-copy-"
	const suffix = ".tmp"
	if strings.ContainsAny(name, `/\`) || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	hexSuffix := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	if len(hexSuffix) != 16 {
		return false
	}
	for _, character := range hexSuffix {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func (fs *FileSystem) createTrashTransferOwnedContainer(root *storagePathRoot, rel string) (string, os.FileInfo, error) {
	rel = filepath.Clean(rel)
	if root == nil || root.handle == nil || rel == "." || filepath.IsAbs(rel) {
		return "", nil, ErrDeleteTargetChanged
	}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return "", nil, err
	}
	abs := storageAbsolutePath(root, rel)
	if err := fs.captureDeleteMountBoundary(root.absRoot).checkHostTree(filepath.Dir(abs)); err != nil {
		return "", nil, err
	}
	if err := rootio.MkdirNoFollow(root.handle, rel, 0o700); err != nil {
		return "", nil, mapStorageCreateTargetError(err)
	}
	info, err := root.handle.Lstat(rel)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", info, errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	identity := workspace.PersistentIdentityTokenForFileInfo(info)
	if identity == "" {
		return "", info, ErrDeleteIdentityUnavailable
	}
	parentRel := filepath.Dir(rel)
	if err := syncManagedStorageDir(root.handle, parentRel, storageAbsolutePath(root, parentRel)); err != nil {
		return identity, info, fmt.Errorf("sync Trash transfer owned container: %w", err)
	}
	current, err := root.handle.Lstat(rel)
	if err != nil || !os.SameFile(info, current) || workspace.PersistentIdentityTokenForFileInfo(current) != identity {
		return identity, info, errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	if err := fs.captureDeleteMountBoundary(root.absRoot).checkHostTree(abs); err != nil {
		return identity, info, err
	}
	return identity, info, nil
}

func (fs *FileSystem) createPreparedTrashTransferOwnedContainer(
	root *storagePathRoot,
	rel string,
	record *trashTransferJournalRecord,
	role string,
) (string, os.FileInfo, error) {
	identity, info, err := fs.createTrashTransferOwnedContainer(root, rel)
	if err != nil {
		return identity, info, err
	}
	if err := fs.writeTrashTransferOwnershipMarker(root, rel, record, role, identity); err != nil {
		return identity, info, err
	}
	return identity, info, nil
}

func (fs *FileSystem) inspectPreparedTrashTransferOwnedContainer(
	root *storagePathRoot,
	containerRel string,
	record *trashTransferJournalRecord,
	role string,
	expectedIdentity string,
) (string, bool, error) {
	containerRel = filepath.Clean(containerRel)
	if root == nil || root.handle == nil || record == nil || containerRel == "." || filepath.IsAbs(containerRel) {
		return "", false, ErrDeleteTargetChanged
	}
	markerName, err := trashTransferOwnershipMarkerName(record.OperationID)
	if err != nil {
		return "", false, err
	}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return "", false, err
	}
	containerInfo, err := root.handle.Lstat(containerRel)
	if err != nil {
		return "", false, mapStorageRootPathError(err)
	}
	identity := workspace.PersistentIdentityTokenForFileInfo(containerInfo)
	if !containerInfo.IsDir() || containerInfo.Mode()&os.ModeSymlink != 0 || !validTrashPurgeContentHash(identity) || (expectedIdentity != "" && identity != expectedIdentity) {
		return "", false, ErrDeleteTargetChanged
	}
	deleteIdentity := deleteStageIdentity(containerInfo)
	if deleteIdentity == "" {
		return "", false, ErrDeleteIdentityUnavailable
	}
	dir, err := rootio.OpenDirNoFollow(root.handle, containerRel)
	if err != nil {
		return "", false, mapStorageRootPathError(err)
	}
	opened, statErr := dir.Stat()
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if statErr != nil || readErr != nil || closeErr != nil || !sameDeleteStageEntry(containerInfo, deleteIdentity, opened) {
		return "", false, errors.Join(ErrDeleteTargetChanged, statErr, readErr, closeErr)
	}
	if len(entries) == 0 {
		// A crash may occur after mkdir+parent fsync but before the owner
		// marker is durable. Only an empty, private 0700 stage can be safely
		// reclaimed without an identity receipt.
		if containerInfo.Mode().Perm() != 0o700 {
			return "", false, ErrDeleteTargetChanged
		}
		current, err := root.handle.Lstat(containerRel)
		if err != nil || !sameDeleteStageEntry(containerInfo, deleteIdentity, current) {
			return "", false, errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
		}
		return identity, false, nil
	}
	if len(entries) != 1 || entries[0].Name() != markerName {
		return "", false, errors.Join(ErrDeleteTargetChanged, errors.New("unverified partial prepared Trash transfer container contents"))
	}
	if _, _, err := fs.readTrashTransferOwnershipMarker(root, containerRel, record, role, expectedIdentity); err != nil {
		return "", false, err
	}
	current, err := root.handle.Lstat(containerRel)
	if err != nil || !sameDeleteStageEntry(containerInfo, deleteIdentity, current) {
		return "", false, errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	return identity, true, nil
}

func (fs *FileSystem) scanTrashTransferOwnedContainerPartial(
	ctx context.Context,
	root *storagePathRoot,
	containerRel string,
	expectedIdentity string,
	source []trashTransferManifestEntry,
) (map[string]os.FileInfo, error) {
	return fs.scanTrashTransferOwnedContainerPartialWithOwnership(ctx, root, containerRel, expectedIdentity, source, nil, "")
}

func (fs *FileSystem) scanTrashTransferOwnedContainerPartialWithOwnership(
	ctx context.Context,
	root *storagePathRoot,
	containerRel string,
	expectedIdentity string,
	source []trashTransferManifestEntry,
	record *trashTransferJournalRecord,
	role string,
) (map[string]os.FileInfo, error) {
	containerRel = filepath.Clean(containerRel)
	if root == nil || root.handle == nil || containerRel == "." || filepath.IsAbs(containerRel) || !validTrashPurgeContentHash(expectedIdentity) {
		return nil, ErrDeleteTargetChanged
	}
	if len(source) == 0 {
		return nil, errors.New("Trash transfer source manifest is missing")
	}
	if err := validateTrashTransferManifest(source, trashTransferManifestSize(source), source[0].Kind == "dir"); err != nil {
		return nil, err
	}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return nil, err
	}
	containerAbs := storageAbsolutePath(root, containerRel)
	if err := fs.captureDeleteMountBoundary(root.absRoot).checkHostTree(containerAbs); err != nil {
		return nil, err
	}

	expectedByPath := make(map[string]trashTransferManifestEntry, len(source))
	for _, entry := range source {
		expectedByPath[entry.Path] = entry
	}
	identities := make(map[string]os.FileInfo, len(source)+2)
	tempCount := 0
	markerName := ""
	if record != nil {
		resolvedMarkerName, err := trashTransferOwnershipMarkerName(record.OperationID)
		if err != nil {
			return nil, err
		}
		markerName = resolvedMarkerName
	}

	containerInfo, err := root.handle.Lstat(containerRel)
	if err != nil {
		return nil, mapStorageRootPathError(err)
	}
	if !containerInfo.IsDir() || containerInfo.Mode()&os.ModeSymlink != 0 || workspace.PersistentIdentityTokenForFileInfo(containerInfo) != expectedIdentity {
		return nil, ErrDeleteTargetChanged
	}
	containerDeleteIdentity := deleteStageIdentity(containerInfo)
	if containerDeleteIdentity == "" {
		return nil, ErrDeleteIdentityUnavailable
	}
	identities["."] = containerInfo

	var scanDir func(rel, containerSuffix, sourceSuffix string, expectedDir *trashTransferManifestEntry) error
	scanDir = func(rel, containerSuffix, sourceSuffix string, expectedDir *trashTransferManifestEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := root.handle.Lstat(rel)
		if err != nil {
			return mapStorageRootPathError(err)
		}
		identity := deleteStageIdentity(info)
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || identity == "" {
			return ErrNotRegular
		}
		if expectedDir != nil && !trashTransferPartialDirectoryModeAllowed(*expectedDir, info) {
			return ErrDeleteTargetChanged
		}

		dir, err := rootio.OpenDirNoFollow(root.handle, rel)
		if err != nil {
			return mapStorageRootPathError(err)
		}
		opened, statErr := dir.Stat()
		children, readErr := dir.ReadDir(-1)
		closeErr := dir.Close()
		if statErr != nil || readErr != nil || closeErr != nil || !sameDeleteStageEntry(info, identity, opened) {
			return errors.Join(ErrDeleteTargetChanged, statErr, readErr, closeErr)
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		childNames := make(map[string]struct{}, len(children))
		for _, child := range children {
			childNames[child.Name()] = struct{}{}
		}

		for _, child := range children {
			childName, err := safeStorageReadDirFallbackChildName(child.Name())
			if err != nil {
				return err
			}
			childRel := filepath.Join(rel, childName)
			childContainerSuffix := filepath.ToSlash(childName)
			if containerSuffix != "." {
				childContainerSuffix = path.Join(containerSuffix, filepath.ToSlash(childName))
			}

			if containerSuffix == "." && markerName != "" && childName == markerName {
				if record == nil {
					return ErrDeleteTargetChanged
				}
				markerInfo, _, err := fs.readTrashTransferOwnershipMarker(root, containerRel, record, role, expectedIdentity)
				if err != nil {
					return err
				}
				identities[childContainerSuffix] = markerInfo
				continue
			}

			if isStorageCopyPublishTempName(childName) {
				tempCount++
				if tempCount > 1 || !trashTransferPartialTempHasCandidate(source, sourceSuffix, childNames) {
					return ErrDeleteTargetChanged
				}
				tempInfo, err := root.handle.Lstat(childRel)
				if err != nil {
					return mapStorageRootPathError(err)
				}
				if !tempInfo.Mode().IsRegular() || tempInfo.Mode()&os.ModeSymlink != 0 || tempInfo.Size() < 0 || !trashTransferPartialTempSizeAllowed(source, sourceSuffix, childNames, tempInfo.Size()) {
					return ErrNotRegular
				}
				tempIdentity := deleteStageIdentity(tempInfo)
				if tempIdentity == "" {
					return ErrDeleteIdentityUnavailable
				}
				file, err := rootio.OpenRegularFileNoFollow(root.handle, childRel)
				if err != nil {
					return mapStorageRootPathError(err)
				}
				opened, statErr := file.Stat()
				closeErr := file.Close()
				current, pathErr := root.handle.Lstat(childRel)
				if statErr != nil || closeErr != nil || pathErr != nil || !sameDeleteStageEntry(tempInfo, tempIdentity, opened) || !sameDeleteStageEntry(tempInfo, tempIdentity, current) {
					return errors.Join(ErrDeleteTargetChanged, statErr, closeErr, mapStorageRootPathError(pathErr))
				}
				identities[childContainerSuffix] = tempInfo
				continue
			}

			childSourceSuffix := filepath.ToSlash(childName)
			if sourceSuffix != "." {
				childSourceSuffix = path.Join(sourceSuffix, filepath.ToSlash(childName))
			}
			if containerSuffix == "." {
				if childName != "content" {
					return ErrDeleteTargetChanged
				}
				childSourceSuffix = "."
			}
			want, ok := expectedByPath[childSourceSuffix]
			if !ok {
				return ErrDeleteTargetChanged
			}
			childInfo, err := root.handle.Lstat(childRel)
			if err != nil {
				return mapStorageRootPathError(err)
			}
			childIdentity := deleteStageIdentity(childInfo)
			if childIdentity == "" {
				return ErrDeleteIdentityUnavailable
			}
			if childInfo.Mode()&os.ModeSymlink != 0 || (!childInfo.IsDir() && !childInfo.Mode().IsRegular()) {
				return ErrNotRegular
			}
			if childInfo.IsDir() {
				if want.Kind != "dir" {
					return ErrDeleteTargetChanged
				}
				identities[childContainerSuffix] = childInfo
				if err := scanDir(childRel, childContainerSuffix, childSourceSuffix, &want); err != nil {
					return err
				}
				continue
			}
			if want.Kind != "file" || uint32(storagePreservedMode(childInfo.Mode())) != want.Mode || childInfo.Size() != want.Size {
				return ErrDeleteTargetChanged
			}
			file, err := rootio.OpenRegularFileNoFollow(root.handle, childRel)
			if err != nil {
				return mapStorageRootPathError(err)
			}
			opened, statErr := file.Stat()
			if statErr != nil || !sameDeleteStageEntry(childInfo, childIdentity, opened) {
				_ = file.Close()
				return errors.Join(ErrDeleteTargetChanged, statErr)
			}
			hashFile := fs.hashTrashTransferFile
			if hashFile == nil {
				hashFile = func(ctx context.Context, _ *storagePathRoot, _ string, file *os.File) (string, error) {
					return hashOpenWorkspaceFileContext(ctx, file)
				}
			}
			hash, hashErr := hashFile(ctx, root, childRel, file)
			after, afterErr := file.Stat()
			closeErr := file.Close()
			current, pathErr := root.handle.Lstat(childRel)
			if hashErr != nil || afterErr != nil || closeErr != nil || pathErr != nil || hash != want.ContentHash ||
				!sameDeleteStageEntry(childInfo, childIdentity, after) || !sameDeleteStageEntry(childInfo, childIdentity, current) {
				return errors.Join(ErrDeleteTargetChanged, hashErr, afterErr, closeErr, mapStorageRootPathError(pathErr))
			}
			identities[childContainerSuffix] = childInfo
		}

		current, err := root.handle.Lstat(rel)
		if err != nil || !sameDeleteStageEntry(info, identity, current) {
			return errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
		}
		return nil
	}

	if err := scanDir(containerRel, ".", ".", nil); err != nil {
		return nil, err
	}
	current, err := root.handle.Lstat(containerRel)
	if err != nil || !sameDeleteStageEntry(containerInfo, containerDeleteIdentity, current) || workspace.PersistentIdentityTokenForFileInfo(current) != expectedIdentity {
		return nil, errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	if err := fs.captureDeleteMountBoundary(root.absRoot).checkHostTree(containerAbs); err != nil {
		return nil, err
	}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return nil, err
	}
	return identities, nil
}

func trashTransferManifestSize(manifest []trashTransferManifestEntry) int64 {
	var size int64
	for _, entry := range manifest {
		if entry.Kind == "file" {
			size += entry.Size
		}
	}
	return size
}

func trashTransferPartialDirectoryModeAllowed(expected trashTransferManifestEntry, actual os.FileInfo) bool {
	if expected.Kind != "dir" || actual == nil || !actual.IsDir() {
		return false
	}
	want := os.FileMode(expected.Mode)
	got := storagePreservedMode(actual.Mode())
	return got == want || got&^want == 0
}

func trashTransferPartialTempHasCandidate(source []trashTransferManifestEntry, parent string, childNames map[string]struct{}) bool {
	return trashTransferPartialTempSizeAllowed(source, parent, childNames, 0)
}

func trashTransferPartialTempSizeAllowed(source []trashTransferManifestEntry, parent string, childNames map[string]struct{}, size int64) bool {
	for _, entry := range source {
		if entry.Kind != "file" || path.Dir(entry.Path) != parent || entry.Size < size {
			continue
		}
		name := path.Base(entry.Path)
		if entry.Path == "." {
			name = "content"
		}
		if _, exists := childNames[name]; !exists {
			return true
		}
	}
	return false
}

func (fs *FileSystem) removeTrashTransferOwnedContainerPartial(
	ctx context.Context,
	root *storagePathRoot,
	containerRel string,
	expectedIdentity string,
	source []trashTransferManifestEntry,
) error {
	return fs.removeTrashTransferOwnedContainerPartialWithOwnership(ctx, root, containerRel, expectedIdentity, source, nil, "")
}

func (fs *FileSystem) removeTrashTransferOwnedContainerPartialWithOwnership(
	ctx context.Context,
	root *storagePathRoot,
	containerRel string,
	expectedIdentity string,
	source []trashTransferManifestEntry,
	record *trashTransferJournalRecord,
	role string,
) error {
	if _, err := root.handle.Lstat(containerRel); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return mapStorageRootPathError(err)
	}
	identities, err := fs.scanTrashTransferOwnedContainerPartialWithOwnership(ctx, root, containerRel, expectedIdentity, source, record, role)
	if err != nil {
		return err
	}
	parentRel := filepath.Dir(containerRel)
	parent, err := rootio.OpenDirNoFollow(root.handle, parentRel)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	defer parent.Close()
	if err := beforeTrashTransferOwnedContainerRemoval(storageAbsolutePath(root, containerRel)); err != nil {
		return err
	}
	base := filepath.Base(containerRel)
	if err := rootio.RemoveAllFromDirNoFollowCheckedInPlace(parent, base, func(entryPath string, info os.FileInfo) error {
		suffix := "."
		if entryPath != base {
			suffix = filepath.ToSlash(strings.TrimPrefix(entryPath, base+string(filepath.Separator)))
		}
		expected, ok := identities[suffix]
		if !ok || !os.SameFile(expected, info) || workspace.PersistentIdentityTokenForFileInfo(expected) != workspace.PersistentIdentityTokenForFileInfo(info) {
			return rootio.ErrEntryChanged
		}
		return nil
	}); err != nil {
		return mapStorageRootPathError(err)
	}
	return syncManagedStorageDir(root.handle, parentRel, storageAbsolutePath(root, parentRel))
}

func (fs *FileSystem) planTrashTransferWorkspaceParentDirs(destinationPath string) ([]trashTransferWorkspaceParentDir, error) {
	parentPath := path.Dir(destinationPath)
	if parentPath == "/" {
		return nil, nil
	}
	root := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(parentPath, "/"), "/")
	current := ""
	missing := false
	plannedShallow := make([]trashTransferWorkspaceParentDir, 0, len(parts))
	for _, part := range parts {
		current = path.Join(current, part)
		rel := filepath.FromSlash(current)
		if missing {
			plannedShallow = append(plannedShallow, trashTransferWorkspaceParentDir{Path: "/" + current})
			continue
		}
		info, err := fs.filesRootHandle.Lstat(rel)
		if errors.Is(err, os.ErrNotExist) {
			missing = true
			plannedShallow = append(plannedShallow, trashTransferWorkspaceParentDir{Path: "/" + current})
			continue
		}
		if err != nil {
			return nil, mapStorageRootPathError(err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrNotRegular
		}
		dir, err := rootio.OpenDirNoFollow(fs.filesRootHandle, rel)
		if err != nil {
			return nil, mapStorageRootPathError(err)
		}
		if err := dir.Close(); err != nil {
			return nil, err
		}
	}
	planned := make([]trashTransferWorkspaceParentDir, len(plannedShallow))
	for index := range plannedShallow {
		planned[len(plannedShallow)-1-index] = plannedShallow[index]
	}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return nil, err
	}
	return planned, nil
}

func (fs *FileSystem) createTrashTransferWorkspaceParentDirs(planned []trashTransferWorkspaceParentDir) ([]trashTransferWorkspaceParentDir, error) {
	return fs.createTrashTransferWorkspaceParentDirsForRecord(planned, nil)
}

func (fs *FileSystem) createPreparedTrashTransferWorkspaceParentDirs(record *trashTransferJournalRecord) ([]trashTransferWorkspaceParentDir, error) {
	if record == nil || record.Kind != trashTransferRestoreFromTrash || record.Decision != trashTransferPrepared {
		return nil, ErrDeleteTargetChanged
	}
	return fs.createTrashTransferWorkspaceParentDirsForRecord(record.WorkspaceParentDirs, record)
}

func (fs *FileSystem) createTrashTransferWorkspaceParentDirsForRecord(
	planned []trashTransferWorkspaceParentDir,
	record *trashTransferJournalRecord,
) ([]trashTransferWorkspaceParentDir, error) {
	created := make([]trashTransferWorkspaceParentDir, len(planned))
	copy(created, planned)
	root := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return nil, err
	}
	for index := len(planned) - 1; index >= 0; index-- {
		rel := storageWorkspaceRelativeName(planned[index].Path)
		if err := rootio.MkdirNoFollow(fs.filesRootHandle, rel, 0o755); err != nil {
			return created[index+1:], mapStorageCreateTargetError(err)
		}
		info, err := fs.filesRootHandle.Lstat(rel)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return created[index:], errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
		}
		identity := workspace.PersistentIdentityTokenForFileInfo(info)
		if identity == "" {
			return created[index:], ErrDeleteIdentityUnavailable
		}
		created[index].Identity = identity
		parentRel := filepath.Dir(rel)
		if err := syncManagedStorageDir(fs.filesRootHandle, parentRel, storageAbsolutePath(root, parentRel)); err != nil {
			return created[index:], err
		}
		current, err := fs.filesRootHandle.Lstat(rel)
		if err != nil || !os.SameFile(info, current) || workspace.PersistentIdentityTokenForFileInfo(current) != identity {
			return created[index:], errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
		}
		if record != nil {
			if err := fs.writeTrashTransferOwnershipMarker(root, rel, record, trashTransferOwnershipRoleWorkspaceParent, identity); err != nil {
				return created[index:], err
			}
		}
	}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return created, err
	}
	return created, nil
}

func (fs *FileSystem) inspectPreparedTrashTransferWorkspaceParentDirs(
	record *trashTransferJournalRecord,
) ([]trashTransferWorkspaceParentDir, bool, error) {
	if record == nil || record.Kind != trashTransferRestoreFromTrash || (record.Decision != trashTransferPrepared && record.Decision != trashTransferCopying) {
		return nil, false, ErrDeleteTargetChanged
	}
	root := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return nil, false, err
	}
	markerName, err := trashTransferOwnershipMarkerName(record.OperationID)
	if err != nil {
		return nil, false, err
	}
	createdByIndex := make(map[int]trashTransferWorkspaceParentDir, len(record.WorkspaceParentDirs))
	retainedUnmarked := false
	missingDeeper := false
	for index := len(record.WorkspaceParentDirs) - 1; index >= 0; index-- {
		dirRecord := record.WorkspaceParentDirs[index]
		rel := storageWorkspaceRelativeName(dirRecord.Path)
		info, err := fs.filesRootHandle.Lstat(rel)
		if errors.Is(err, os.ErrNotExist) {
			missingDeeper = true
			continue
		}
		if err != nil {
			return nil, false, mapStorageRootPathError(err)
		}
		if missingDeeper {
			return nil, false, ErrDeleteTargetChanged
		}
		identity := workspace.PersistentIdentityTokenForFileInfo(info)
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !validTrashPurgeContentHash(identity) || (dirRecord.Identity != "" && identity != dirRecord.Identity) {
			return nil, false, ErrDeleteTargetChanged
		}
		deleteIdentity := deleteStageIdentity(info)
		if deleteIdentity == "" {
			return nil, false, ErrDeleteIdentityUnavailable
		}
		dir, err := rootio.OpenDirNoFollow(fs.filesRootHandle, rel)
		if err != nil {
			return nil, false, mapStorageRootPathError(err)
		}
		opened, statErr := dir.Stat()
		entries, readErr := dir.ReadDir(-1)
		closeErr := dir.Close()
		if statErr != nil || readErr != nil || closeErr != nil || !sameDeleteStageEntry(info, deleteIdentity, opened) {
			return nil, false, errors.Join(ErrDeleteTargetChanged, statErr, readErr, closeErr)
		}
		allowedChild := ""
		if index > 0 {
			childRel := storageWorkspaceRelativeName(record.WorkspaceParentDirs[index-1].Path)
			if _, childErr := fs.filesRootHandle.Lstat(childRel); childErr == nil {
				allowedChild = filepath.Base(childRel)
			} else if !errors.Is(childErr, os.ErrNotExist) {
				return nil, false, mapStorageRootPathError(childErr)
			}
		} else {
			stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
			if _, stageErr := fs.filesRootHandle.Lstat(stageRel); stageErr == nil {
				allowedChild = filepath.Base(stageRel)
			} else if !errors.Is(stageErr, os.ErrNotExist) {
				return nil, false, mapStorageRootPathError(stageErr)
			}
		}
		markerPresent := false
		for _, entry := range entries {
			name, err := safeStorageReadDirFallbackChildName(entry.Name())
			if err != nil {
				return nil, false, err
			}
			switch name {
			case markerName:
				if markerPresent {
					return nil, false, ErrDeleteTargetChanged
				}
				markerPresent = true
			case allowedChild:
				if allowedChild == "" {
					return nil, false, ErrDeleteTargetChanged
				}
			default:
				return nil, false, ErrDeleteTargetChanged
			}
		}
		if markerPresent {
			if _, _, err := fs.readTrashTransferOwnershipMarker(root, rel, record, trashTransferOwnershipRoleWorkspaceParent, dirRecord.Identity); err != nil {
				return nil, false, err
			}
		} else if dirRecord.Identity == "" {
			// Unmarked restore parents are retained. They may have been made
			// durable immediately before a crash, but there is no identity
			// receipt proving exclusive ownership.
			if info.Mode().Perm() != 0o755 {
				return nil, false, ErrDeleteTargetChanged
			}
			retainedUnmarked = true
		} else if info.Mode().Perm() != 0o755 {
			return nil, false, ErrDeleteTargetChanged
		}
		createdByIndex[index] = trashTransferWorkspaceParentDir{Path: dirRecord.Path, Identity: identity}
	}
	created := make([]trashTransferWorkspaceParentDir, 0, len(createdByIndex))
	for index := range record.WorkspaceParentDirs {
		if dirRecord, exists := createdByIndex[index]; exists {
			created = append(created, dirRecord)
		}
	}
	return created, retainedUnmarked, fs.checkTrashTransferRootIdentity(root)
}

func trashTransferRecordWithPreparedOwnership(
	record *trashTransferJournalRecord,
	stageIdentity string,
	createdParentDirs []trashTransferWorkspaceParentDir,
	retainParentDirs bool,
) (*trashTransferJournalRecord, error) {
	if record == nil || record.Decision != trashTransferPrepared {
		return nil, ErrDeleteTargetChanged
	}
	owned := *record
	owned.WorkspaceParentDirs = append([]trashTransferWorkspaceParentDir(nil), record.WorkspaceParentDirs...)
	switch record.Kind {
	case trashTransferDeleteToTrash:
		owned.TrashStageIdentity = stageIdentity
	case trashTransferRestoreFromTrash:
		owned.WorkspaceStageIdentity = stageIdentity
		if !retainParentDirs {
			identitiesByPath := make(map[string]string, len(createdParentDirs))
			for _, dir := range createdParentDirs {
				identitiesByPath[dir.Path] = dir.Identity
			}
			for index := range owned.WorkspaceParentDirs {
				owned.WorkspaceParentDirs[index].Identity = identitiesByPath[owned.WorkspaceParentDirs[index].Path]
			}
		}
	default:
		return nil, ErrDeleteTargetChanged
	}
	if err := validateTrashTransferJournalRecord(&owned, trashTransferPrepared); err != nil {
		return nil, err
	}
	return &owned, nil
}

func (fs *FileSystem) removeTrashTransferWorkspaceParentOwnershipMarkers(
	record *trashTransferJournalRecord,
	dirs []trashTransferWorkspaceParentDir,
	allowMissing bool,
) error {
	if record == nil {
		return ErrDeleteTargetChanged
	}
	root := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	markerName, err := trashTransferOwnershipMarkerName(record.OperationID)
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		rel := storageWorkspaceRelativeName(dir.Path)
		markerRel := filepath.Join(rel, markerName)
		if _, err := fs.filesRootHandle.Lstat(markerRel); err != nil {
			if allowMissing && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return mapStorageRootPathError(err)
		}
		if err := fs.removeTrashTransferOwnershipMarker(root, rel, record, trashTransferOwnershipRoleWorkspaceParent, dir.Identity, allowMissing); err != nil {
			return err
		}
	}
	return nil
}

func (fs *FileSystem) verifyTrashTransferWorkspaceParentDirs(dirs []trashTransferWorkspaceParentDir) error {
	root := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return err
	}
	for _, dir := range dirs {
		rel := storageWorkspaceRelativeName(dir.Path)
		info, err := fs.filesRootHandle.Lstat(rel)
		if err != nil {
			return mapStorageRootPathError(err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || workspace.PersistentIdentityTokenForFileInfo(info) != dir.Identity {
			return ErrDeleteTargetChanged
		}
	}
	return fs.checkTrashTransferRootIdentity(root)
}

func (fs *FileSystem) verifyTrashTransferWorkspaceParentDirsForRollback(dirs []trashTransferWorkspaceParentDir) error {
	root := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return err
	}
	foundExisting := false
	for _, dir := range dirs {
		rel := storageWorkspaceRelativeName(dir.Path)
		info, err := fs.filesRootHandle.Lstat(rel)
		if errors.Is(err, os.ErrNotExist) {
			if foundExisting {
				return ErrDeleteTargetChanged
			}
			continue
		}
		if err != nil {
			return mapStorageRootPathError(err)
		}
		foundExisting = true
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || workspace.PersistentIdentityTokenForFileInfo(info) != dir.Identity {
			return ErrDeleteTargetChanged
		}
	}
	return fs.checkTrashTransferRootIdentity(root)
}

func (fs *FileSystem) removeTrashTransferWorkspaceParentDirs(dirs []trashTransferWorkspaceParentDir) error {
	root := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return err
	}
	for _, dirRecord := range dirs {
		rel := storageWorkspaceRelativeName(dirRecord.Path)
		info, err := fs.filesRootHandle.Lstat(rel)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return mapStorageRootPathError(err)
		}
		identity := deleteStageIdentity(info)
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || identity == "" || workspace.PersistentIdentityTokenForFileInfo(info) != dirRecord.Identity {
			return ErrDeleteTargetChanged
		}
		dir, err := rootio.OpenDirNoFollow(fs.filesRootHandle, rel)
		if err != nil {
			return mapStorageRootPathError(err)
		}
		opened, statErr := dir.Stat()
		entries, readErr := dir.ReadDir(-1)
		closeErr := dir.Close()
		if statErr != nil || readErr != nil || closeErr != nil || !sameDeleteStageEntry(info, identity, opened) || len(entries) != 0 {
			return errors.Join(ErrDeleteTargetChanged, statErr, readErr, closeErr)
		}
		parentRel := filepath.Dir(rel)
		parent, err := rootio.OpenDirNoFollow(fs.filesRootHandle, parentRel)
		if err != nil {
			return mapStorageRootPathError(err)
		}
		base := filepath.Base(rel)
		removeErr := rootio.RemoveAllFromDirNoFollowCheckedInPlace(parent, base, func(entryPath string, current os.FileInfo) error {
			if entryPath != base || !sameDeleteStageEntry(info, identity, current) {
				return rootio.ErrEntryChanged
			}
			return nil
		})
		closeErr = parent.Close()
		if removeErr != nil || closeErr != nil {
			return errors.Join(mapStorageRootPathError(removeErr), closeErr)
		}
		if err := syncManagedStorageDir(fs.filesRootHandle, parentRel, storageAbsolutePath(root, parentRel)); err != nil {
			return err
		}
	}
	return fs.checkTrashTransferRootIdentity(root)
}
