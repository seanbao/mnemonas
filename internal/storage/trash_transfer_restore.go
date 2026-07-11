package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/versionstore"
	"github.com/seanbao/mnemonas/internal/workspace"
)

func (fs *FileSystem) restoreTrashTransferLocked(ctx context.Context, id, destinationPath string) error {
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
	if err := fs.validateTrashRestoreDestinationLocked(ctx, item, destinationPath); err != nil {
		return err
	}

	operationID, err := fs.allocateTrashTransferRestoreOperationID(ctx, item.ID, destinationPath)
	if err != nil {
		return err
	}
	trashItemIdentity, sourceManifest, err := fs.captureRestoreTrashTransferSource(ctx, item)
	if err != nil {
		return err
	}
	filesRootIdentity, trashRootIdentity, err := fs.captureTrashTransferRootIdentities()
	if err != nil {
		return err
	}
	workspaceParentDirs, err := fs.planTrashTransferWorkspaceParentDirs(destinationPath)
	if err != nil {
		return err
	}
	record := &trashTransferJournalRecord{
		Version:             trashTransferJournalVersion,
		Decision:            trashTransferPrepared,
		Kind:                trashTransferRestoreFromTrash,
		OperationID:         operationID,
		FilesRootIdentity:   filesRootIdentity,
		TrashRootIdentity:   trashRootIdentity,
		Item:                trashPurgeJournalItemFromStore(item),
		DestinationPath:     destinationPath,
		WorkspaceStagePath:  trashTransferWorkspaceStagePath(path.Dir(destinationPath), operationID),
		WorkspaceParentDirs: workspaceParentDirs,
		TrashItemIdentity:   trashItemIdentity,
		SourceManifest:      sourceManifest,
		ParticipantPayload:  append([]byte(nil), item.RestoreData...),
	}
	if err := validateTrashTransferJournalRecord(record, trashTransferPrepared); err != nil {
		return err
	}
	published, err := fs.publishTrashTransferJournalRecord(record)
	if err != nil {
		if published {
			return fs.blockTrashTransferLocked(record, fmt.Errorf("sync prepared restore-from-Trash journal: %w", err))
		}
		return fmt.Errorf("persist prepared restore-from-Trash journal: %w", err)
	}

	rollbackBeforeCopying := func(cause error) error {
		recoveryCtx := context.WithoutCancel(ctx)
		rollbackErr := fs.rollbackPreparedRestoreTrashTransfer(recoveryCtx, record)
		if rollbackErr == nil {
			return cause
		}
		if isTrashTransferTerminalCleanupWarning(rollbackErr) {
			return fs.blockTrashTransferLocked(record, errors.Join(cause, rollbackErr))
		}
		return fs.blockTrashTransferLocked(record, errors.Join(cause, rollbackErr))
	}

	createdParentDirs, err := fs.createPreparedTrashTransferWorkspaceParentDirs(record)
	if err != nil {
		return rollbackBeforeCopying(fmt.Errorf("create restore-from-Trash parent directories: %w", err))
	}
	copying := *record
	copying.Decision = trashTransferCopying
	copying.WorkspaceParentDirs = createdParentDirs
	workspaceRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
	stageIdentity, _, err := fs.createPreparedTrashTransferOwnedContainer(
		workspaceRoot,
		stageRel,
		record,
		trashTransferOwnershipRoleWorkspaceContainer,
	)
	copying.WorkspaceStageIdentity = stageIdentity
	if err != nil {
		return rollbackBeforeCopying(fmt.Errorf("create restore-from-Trash owned container: %w", err))
	}
	if err := validateTrashTransferJournalRecord(&copying, trashTransferCopying); err != nil {
		return rollbackBeforeCopying(err)
	}
	published, err = fs.publishTrashTransferJournalRecord(&copying)
	if err != nil {
		if published {
			return fs.blockTrashTransferLocked(&copying, fmt.Errorf("sync copying restore-from-Trash journal: %w", err))
		}
		return rollbackBeforeCopying(fmt.Errorf("persist copying restore-from-Trash journal: %w", err))
	}
	record = &copying
	if err := fs.removeTrashTransferOwnershipMarker(
		workspaceRoot,
		stageRel,
		record,
		trashTransferOwnershipRoleWorkspaceContainer,
		record.WorkspaceStageIdentity,
		false,
	); err != nil {
		return fs.blockTrashTransferLocked(record, fmt.Errorf("remove restore-from-Trash container ownership marker: %w", err))
	}
	if err := fs.removeTrashTransferWorkspaceParentOwnershipMarkers(record, record.WorkspaceParentDirs, false); err != nil {
		return fs.blockTrashTransferLocked(record, fmt.Errorf("remove restore-from-Trash parent ownership markers: %w", err))
	}

	rollback := func(cause error, rollbackRecord *trashTransferJournalRecord) error {
		rollbackErr := fs.rollbackPreparedRestoreTrashTransfer(context.WithoutCancel(ctx), rollbackRecord)
		if rollbackErr != nil {
			if isTrashTransferTerminalCleanupWarning(rollbackErr) {
				return fs.blockTrashTransferLocked(rollbackRecord, errors.Join(cause, rollbackErr))
			}
			return fs.blockTrashTransferLocked(rollbackRecord, errors.Join(cause, rollbackErr))
		}
		return cause
	}

	if err := fs.copyRestoreTrashTransferReplica(ctx, record); err != nil {
		return rollback(fmt.Errorf("copy restore-from-Trash source to workspace stage: %w", err), record)
	}
	contentRel := trashTransferOwnedContentRel(stageRel)
	replicaManifest, _, err := fs.scanTrashTransferTree(ctx, workspaceRoot, contentRel, nil, false)
	if err != nil {
		return rollback(fmt.Errorf("capture restore-from-Trash replica manifest: %w", err), record)
	}
	ready := *record
	ready.Decision = trashTransferReady
	ready.ReplicaManifest = replicaManifest
	if err := validateTrashTransferJournalRecord(&ready, trashTransferReady); err != nil {
		return rollback(err, &ready)
	}
	published, err = fs.publishTrashTransferJournalRecord(&ready)
	if err != nil {
		if published {
			return fs.blockTrashTransferLocked(&ready, fmt.Errorf("sync ready restore-from-Trash journal: %w", err))
		}
		return rollback(fmt.Errorf("persist ready restore-from-Trash journal: %w", err), &ready)
	}
	record = &ready

	if err := fs.publishRestoreTrashTransferDestination(ctx, record); err != nil {
		return rollback(fmt.Errorf("publish restore-from-Trash destination: %w", err), record)
	}
	fileIndex, err := trashRestoreFileIndexEntries(record)
	if err != nil {
		return rollback(err, record)
	}
	operation, err := trashTransferOperationForRecord(record)
	if err != nil {
		return rollback(err, record)
	}
	commitTrashRestore := fs.commitTrashRestore
	if commitTrashRestore == nil {
		commitTrashRestore = fs.versions.CommitTrashRestore
	}
	commitErr := commitTrashRestore(ctx, item, destinationPath, fileIndex, item.HadVersions && destinationPath != item.OriginalPath, operation)
	committed, resolveErr := fs.resolveTrashRestoreCommit(context.WithoutCancel(ctx), record, commitErr)
	if resolveErr != nil {
		return fs.blockTrashTransferLocked(record, resolveErr)
	}
	if !committed {
		return rollback(fmt.Errorf("commit restore-from-Trash metadata: %w", commitErr), record)
	}

	committedRecord := *record
	committedRecord.Decision = trashTransferCommitted
	published, err = fs.publishTrashTransferJournalRecord(&committedRecord)
	if err != nil {
		return fs.blockTrashTransferLocked(&committedRecord, fmt.Errorf("persist committed restore-from-Trash journal: %w", err))
	}
	record = &committedRecord
	recoveryCtx := context.WithoutCancel(ctx)
	var persistenceWarning error
	participantWarning, participantErr := deliverTrashParticipantWithDurabilityRetry(func() error {
		return fs.applyTrashRestoreParticipant(recoveryCtx, operationID, item.OriginalPath, destinationPath, item.RestoreData)
	})
	if participantErr != nil {
		return fs.blockTrashTransferLocked(record, fmt.Errorf("deliver committed restore-from-Trash participant: %w", participantErr))
	}
	if participantWarning != nil {
		persistenceWarning = fmt.Errorf("deliver committed restore-from-Trash participant: %w", participantWarning)
	}
	if err := fs.removeCommittedRestoreTrashSource(recoveryCtx, record, true); err != nil {
		return fs.blockTrashTransferLocked(record, fmt.Errorf("remove committed restore-from-Trash source: %w", err))
	}

	completedRecord := *record
	completedRecord.Decision = trashTransferCompleted
	published, err = fs.publishTrashTransferJournalRecord(&completedRecord)
	if err != nil {
		return fs.blockTrashTransferLocked(&completedRecord, fmt.Errorf("persist completed restore-from-Trash journal: %w", err))
	}
	record = &completedRecord
	participantWarning, participantErr = deliverTrashParticipantWithDurabilityRetry(func() error {
		return fs.completeTrashRestoreParticipant(recoveryCtx, operationID, item.OriginalPath, destinationPath, item.RestoreData)
	})
	if participantErr != nil {
		return fs.blockTrashTransferLocked(record, fmt.Errorf("complete restore-from-Trash participant: %w", participantErr))
	}
	if participantWarning != nil {
		persistenceWarning = errors.Join(persistenceWarning, fmt.Errorf("complete restore-from-Trash participant: %w", participantWarning))
	}
	hash, err := trashTransferJournalHash(record)
	if err != nil {
		return fs.blockTrashTransferLocked(record, err)
	}
	if err := fs.versions.CompleteTrashOperation(recoveryCtx, operationID, hash); err != nil {
		return fs.blockTrashTransferLocked(record, fmt.Errorf("acknowledge restore-from-Trash participant: %w", err))
	}
	if err := fs.completeTrashTransferJournal(record); err != nil {
		cleanupErr := fmt.Errorf("cleanup restore-from-Trash journal: %w", err)
		recoveryErr := fs.blockTrashTransferLocked(record, cleanupErr)
		if isTrashTransferTerminalCleanupWarning(err) {
			return errors.Join(
				wrapVisibleMutationWarning(errors.Join(persistenceWarning, cleanupErr)),
				recoveryErr,
			)
		}
		return recoveryErr
	}
	return wrapVisibleMutationWarning(persistenceWarning)
}

func (fs *FileSystem) validateTrashRestoreDestinationLocked(ctx context.Context, item *versionstore.TrashItem, destinationPath string) error {
	if item == nil || destinationPath == "" {
		return ErrNotFound
	}
	exists, err := fs.workspacePathExists(ctx, destinationPath)
	if err != nil {
		return err
	}
	if exists {
		return ErrAlreadyExists
	}
	if !item.HadVersions || destinationPath == item.OriginalPath {
		return nil
	}
	originalExists, err := fs.workspacePathExists(ctx, item.OriginalPath)
	if err != nil {
		return err
	}
	if originalExists {
		return fmt.Errorf("cannot restore %s to a custom path while its original path still has version metadata: %w", item.OriginalPath, ErrAlreadyExists)
	}
	sharedMetadata, err := fs.hasOtherTrashItemReferencingRestoredMetadata(ctx, item.OriginalPath, item.IsDir, item.ID)
	if err != nil {
		return err
	}
	if sharedMetadata {
		return fmt.Errorf("cannot restore %s to a custom path while another trash item still references its version metadata: %w", item.OriginalPath, ErrAlreadyExists)
	}
	targetMetadata, err := fs.hasOtherTrashItemReferencingRestoredMetadata(ctx, destinationPath, item.IsDir, item.ID)
	if err != nil {
		return err
	}
	if targetMetadata {
		return fmt.Errorf("cannot restore %s to custom path %s while the target path still has version metadata: %w", item.OriginalPath, destinationPath, ErrAlreadyExists)
	}
	targetVersionMetadata, err := fs.versionMetadataPathExists(ctx, destinationPath, item.IsDir)
	if err != nil {
		return err
	}
	if targetVersionMetadata {
		return fmt.Errorf("cannot restore %s to custom path %s while the target path has version metadata: %w", item.OriginalPath, destinationPath, ErrAlreadyExists)
	}
	return nil
}

func trashTransferOwnershipMarkerConflictsWithRestoreParent(destinationPath, operationID string) (bool, error) {
	markerName, err := trashTransferOwnershipMarkerName(operationID)
	if err != nil {
		return false, err
	}
	normalized, err := normalizeStorageWorkspacePath(destinationPath)
	if err != nil || normalized != destinationPath || normalized == "/" {
		return false, errors.Join(ErrDeleteTargetChanged, err)
	}
	parentPath := strings.TrimPrefix(path.Dir(normalized), "/")
	if parentPath == "" || parentPath == "." {
		return false, nil
	}
	for _, component := range strings.Split(parentPath, "/") {
		if component == markerName {
			return true, nil
		}
	}
	return false, nil
}

func (fs *FileSystem) allocateTrashTransferRestoreOperationID(ctx context.Context, trashID, destinationPath string) (string, error) {
	return fs.allocateTrashTransferRestoreOperationIDWithGenerator(ctx, trashID, destinationPath, newTrashPurgeOperationID)
}

func (fs *FileSystem) allocateTrashTransferRestoreOperationIDWithGenerator(
	ctx context.Context,
	trashID string,
	destinationPath string,
	generateOperationID func() (string, error),
) (string, error) {
	if generateOperationID == nil {
		return "", errors.New("restore-from-Trash operation ID generator is unavailable")
	}
	operations, err := fs.versions.ListTrashOperations(ctx)
	if err != nil {
		return "", err
	}
	operationIDs := make(map[string]struct{}, len(operations))
	for _, operation := range operations {
		operationIDs[operation.ID] = struct{}{}
		if operation.TrashID == trashID {
			return "", versionstore.ErrTrashOperationConflict
		}
	}
	for range 32 {
		operationID, err := generateOperationID()
		if err != nil {
			return "", fmt.Errorf("generate restore-from-Trash operation ID: %w", err)
		}
		if _, exists := operationIDs[operationID]; exists {
			continue
		}
		markerCollision, err := trashTransferOwnershipMarkerConflictsWithRestoreParent(destinationPath, operationID)
		if err != nil {
			return "", fmt.Errorf("validate restore-from-Trash ownership marker path: %w", err)
		}
		if markerCollision {
			continue
		}
		stageRel := storageWorkspaceRelativeName(trashTransferWorkspaceStagePath(path.Dir(destinationPath), operationID))
		if _, err := fs.filesRootHandle.Lstat(stageRel); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", mapStorageRootPathError(err)
		}
		collision := false
		for _, rel := range []string{trashTransferItemStageRel(operationID)} {
			if _, err := fs.trashRootHandle.Lstat(filepath.FromSlash(rel)); err == nil {
				collision = true
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				return "", mapStorageRootPathError(err)
			}
		}
		for _, decision := range []string{trashTransferPrepared, trashTransferCopying, trashTransferReady, trashTransferCommitted, trashTransferCompleted} {
			if _, err := fs.trashRootHandle.Lstat(filepath.FromSlash(trashTransferJournalRel(operationID, decision))); err == nil {
				collision = true
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				return "", mapStorageRootPathError(err)
			}
		}
		if !collision {
			return operationID, nil
		}
	}
	return "", errors.New("failed to allocate restore-from-Trash transaction identifier")
}

func (fs *FileSystem) captureRestoreTrashTransferSource(ctx context.Context, item *versionstore.TrashItem) (string, []trashTransferManifestEntry, error) {
	if item == nil {
		return "", nil, ErrNotFound
	}
	itemRel := filepath.FromSlash(item.ID)
	identity, _, err := fs.scanDeleteTrashTransferItem(ctx, itemRel, "", nil, false)
	if err != nil {
		return "", nil, err
	}
	trashRoot := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
	manifest, _, err := fs.scanTrashTransferTree(ctx, trashRoot, filepath.Join(itemRel, "content"), nil, false)
	if err != nil {
		return "", nil, err
	}
	if _, _, err := fs.scanDeleteTrashTransferItem(ctx, itemRel, identity, manifest, false); err != nil {
		return "", nil, err
	}
	return identity, manifest, nil
}

func (fs *FileSystem) copyRestoreTrashTransferReplica(ctx context.Context, record *trashTransferJournalRecord) error {
	if record == nil || record.Kind != trashTransferRestoreFromTrash || record.Decision != trashTransferCopying {
		return ErrDeleteTargetChanged
	}
	sourceRoot := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
	destinationRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	sourceRel := filepath.Join(filepath.FromSlash(record.Item.ID), "content")
	stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
	if err := fs.verifyTrashTransferWorkspaceParentDirs(record.WorkspaceParentDirs); err != nil {
		return err
	}
	owned, err := fs.scanTrashTransferOwnedContainerPartial(ctx, destinationRoot, stageRel, record.WorkspaceStageIdentity, record.SourceManifest)
	if err != nil {
		return err
	}
	if len(owned) != 1 {
		return ErrDeleteTargetChanged
	}
	destinationRel := trashTransferOwnedContentRel(stageRel)
	sourceAbs := storageAbsolutePath(sourceRoot, sourceRel)
	destinationAbs := storageAbsolutePath(destinationRoot, destinationRel)
	if err := fs.checkStorageCopyMountBoundaries(sourceRoot, sourceAbs, destinationRoot, destinationAbs); err != nil {
		return err
	}
	if record.Item.IsDir {
		return fs.copyDirBetweenRoots(sourceRoot, sourceRel, sourceAbs, destinationRoot, destinationRel, destinationAbs)
	}
	return fs.copyFileBetweenRoots(sourceRoot, sourceRel, sourceAbs, destinationRoot, destinationRel, destinationAbs)
}

type trashTransferRestorePublishState uint8

const (
	trashTransferRestorePublishMissing trashTransferRestorePublishState = iota
	trashTransferRestorePublishStaged
	trashTransferRestorePublishRenamed
	trashTransferRestorePublishComplete
)

func (fs *FileSystem) inspectRestoreTrashTransferPublishState(
	ctx context.Context,
	record *trashTransferJournalRecord,
) (trashTransferRestorePublishState, os.FileInfo, error) {
	if record == nil || record.Kind != trashTransferRestoreFromTrash || record.Decision != trashTransferReady {
		return trashTransferRestorePublishMissing, nil, ErrDeleteTargetChanged
	}
	if err := fs.verifyTrashTransferRootIdentities(record); err != nil {
		return trashTransferRestorePublishMissing, nil, err
	}
	if err := fs.verifyTrashTransferWorkspaceParentDirs(record.WorkspaceParentDirs); err != nil {
		return trashTransferRestorePublishMissing, nil, err
	}
	workspaceRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
	contentRel := trashTransferOwnedContentRel(stageRel)
	destinationRel := storageWorkspaceRelativeName(record.DestinationPath)
	_, stageErr := fs.filesRootHandle.Lstat(stageRel)
	_, destinationErr := fs.filesRootHandle.Lstat(destinationRel)
	stageExists := stageErr == nil
	destinationExists := destinationErr == nil
	if stageErr != nil && !errors.Is(stageErr, os.ErrNotExist) {
		return trashTransferRestorePublishMissing, nil, mapStorageRootPathError(stageErr)
	}
	if destinationErr != nil && !errors.Is(destinationErr, os.ErrNotExist) {
		return trashTransferRestorePublishMissing, nil, mapStorageRootPathError(destinationErr)
	}

	var contentInfo os.FileInfo
	if stageExists {
		identities, err := fs.scanTrashTransferOwnedContainerPartial(ctx, workspaceRoot, stageRel, record.WorkspaceStageIdentity, record.SourceManifest)
		if err != nil {
			return trashTransferRestorePublishMissing, nil, err
		}
		if len(identities) == 1 {
			if !destinationExists {
				return trashTransferRestorePublishMissing, nil, errors.New("ready restore-from-Trash container is empty before destination publish")
			}
		} else {
			if destinationExists {
				return trashTransferRestorePublishMissing, nil, errors.Join(ErrAlreadyExists, errors.New("ready restore-from-Trash destination appeared before publish"))
			}
			if len(identities) != len(record.ReplicaManifest)+1 {
				return trashTransferRestorePublishMissing, nil, errors.New("ready restore-from-Trash container has an invalid publish state")
			}
			var ok bool
			contentInfo, ok = identities["content"]
			if !ok {
				return trashTransferRestorePublishMissing, nil, ErrDeleteTargetChanged
			}
			if _, _, err := fs.scanTrashTransferTree(ctx, workspaceRoot, contentRel, record.ReplicaManifest, false); err != nil {
				return trashTransferRestorePublishMissing, nil, err
			}
		}
	}
	if destinationExists {
		if _, _, err := fs.scanTrashTransferTree(ctx, workspaceRoot, destinationRel, record.ReplicaManifest, false); err != nil {
			return trashTransferRestorePublishMissing, nil, err
		}
	}
	switch {
	case stageExists && !destinationExists:
		return trashTransferRestorePublishStaged, contentInfo, nil
	case stageExists && destinationExists:
		return trashTransferRestorePublishRenamed, nil, nil
	case !stageExists && destinationExists:
		return trashTransferRestorePublishComplete, nil, nil
	default:
		return trashTransferRestorePublishMissing, nil, nil
	}
}

func (fs *FileSystem) publishRestoreTrashTransferDestination(ctx context.Context, record *trashTransferJournalRecord) error {
	state, stagedInfo, err := fs.inspectRestoreTrashTransferPublishState(ctx, record)
	if err != nil {
		return err
	}
	workspaceRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
	contentRel := trashTransferOwnedContentRel(stageRel)
	destinationRel := storageWorkspaceRelativeName(record.DestinationPath)
	if state == trashTransferRestorePublishStaged {
		if err := rootio.RenameLeafNoReplace(fs.filesRootHandle, contentRel, destinationRel); err != nil {
			if errors.Is(err, os.ErrExist) {
				return ErrAlreadyExists
			}
			return mapStorageRootPathError(err)
		}
		destinationInfo, err := fs.filesRootHandle.Lstat(destinationRel)
		if err != nil || !os.SameFile(stagedInfo, destinationInfo) {
			return errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
		}
		if err := syncStorageManagedRenameDirs(workspaceRoot, contentRel, workspaceRoot, destinationRel); err != nil {
			return fmt.Errorf("sync published restore-from-Trash destination: %w", err)
		}
		state, _, err = fs.inspectRestoreTrashTransferPublishState(ctx, record)
		if err != nil {
			return err
		}
	}
	if state == trashTransferRestorePublishRenamed {
		if err := fs.removeTrashTransferOwnedContainerPartial(ctx, workspaceRoot, stageRel, record.WorkspaceStageIdentity, record.SourceManifest); err != nil {
			return fmt.Errorf("remove published restore-from-Trash container: %w", err)
		}
		state, _, err = fs.inspectRestoreTrashTransferPublishState(ctx, record)
		if err != nil {
			return err
		}
	}
	if state != trashTransferRestorePublishComplete {
		return errors.New("ready restore-from-Trash replica is missing")
	}
	return nil
}

func trashRestoreFileIndexEntries(record *trashTransferJournalRecord) ([]versionstore.FileIndexEntry, error) {
	if record == nil || record.Kind != trashTransferRestoreFromTrash || len(record.ReplicaManifest) == 0 {
		return nil, ErrDeleteTargetChanged
	}
	entries := make([]versionstore.FileIndexEntry, 0, len(record.ReplicaManifest))
	for _, manifestEntry := range record.ReplicaManifest {
		if manifestEntry.Kind == "dir" {
			continue
		}
		entryPath := record.DestinationPath
		if manifestEntry.Path != "." {
			entryPath = path.Join(record.DestinationPath, manifestEntry.Path)
		}
		entries = append(entries, versionstore.FileIndexEntry{
			Path:        entryPath,
			Size:        manifestEntry.Size,
			ModTime:     time.Unix(0, manifestEntry.ModTimeUnixNano),
			ContentHash: manifestEntry.ContentHash,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

func (fs *FileSystem) resolveTrashRestoreCommit(ctx context.Context, record *trashTransferJournalRecord, commitErr error) (bool, error) {
	operation, operationErr := fs.versions.GetTrashOperation(ctx, record.OperationID)
	if operationErr == nil {
		if trashTransferOperationMatches(record, operation) {
			return true, nil
		}
		return false, versionstore.ErrTrashOperationConflict
	}
	if !errors.Is(operationErr, versionstore.ErrNotFound) {
		return false, errors.Join(commitErr, operationErr)
	}
	if commitErr == nil {
		return false, errors.New("restore-from-Trash commit returned success without an outbox marker")
	}
	persisted, itemErr := fs.versions.GetTrashItem(ctx, record.Item.ID)
	if itemErr == nil {
		if !sameTrashPurgeItem(record.Item, trashPurgeJournalItemFromStore(persisted)) {
			return false, versionstore.ErrTrashItemMismatch
		}
		return false, nil
	}
	if !errors.Is(itemErr, versionstore.ErrNotFound) {
		return false, errors.Join(commitErr, itemErr)
	}
	return false, errors.Join(commitErr, errors.New("restore-from-Trash item disappeared without its operation marker"))
}

func (fs *FileSystem) preflightPreparedRestoreTrashTransferRollback(ctx context.Context, record *trashTransferJournalRecord) error {
	if record == nil || record.Kind != trashTransferRestoreFromTrash || record.Decision == trashTransferCommitted || record.Decision == trashTransferCompleted {
		return ErrDeleteTargetChanged
	}
	if err := fs.verifyTrashTransferRootIdentities(record); err != nil {
		return err
	}
	stored, err := fs.versions.GetTrashItem(ctx, record.Item.ID)
	if err != nil {
		return err
	}
	if !sameTrashPurgeItem(record.Item, trashPurgeJournalItemFromStore(stored)) {
		return versionstore.ErrTrashItemMismatch
	}
	if _, _, err := fs.scanDeleteTrashTransferItem(ctx, filepath.FromSlash(record.Item.ID), record.TrashItemIdentity, record.SourceManifest, false); err != nil {
		return err
	}
	workspaceRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
	destinationRel := storageWorkspaceRelativeName(record.DestinationPath)
	_, stageErr := fs.filesRootHandle.Lstat(stageRel)
	_, destinationErr := fs.filesRootHandle.Lstat(destinationRel)
	stageExists := stageErr == nil
	destinationExists := destinationErr == nil
	if stageErr != nil && !errors.Is(stageErr, os.ErrNotExist) {
		return mapStorageRootPathError(stageErr)
	}
	if destinationErr != nil && !errors.Is(destinationErr, os.ErrNotExist) {
		return mapStorageRootPathError(destinationErr)
	}
	switch record.Decision {
	case trashTransferPrepared:
		if destinationExists {
			return errors.New("prepared restore-from-Trash transfer has a published destination")
		}
		if _, _, err := fs.inspectPreparedTrashTransferWorkspaceParentDirs(record); err != nil {
			return err
		}
		if stageExists {
			if _, _, err := fs.inspectPreparedTrashTransferOwnedContainer(
				workspaceRoot,
				stageRel,
				record,
				trashTransferOwnershipRoleWorkspaceContainer,
				record.WorkspaceStageIdentity,
			); err != nil {
				return err
			}
		}
		return nil
	case trashTransferCopying:
		if destinationExists {
			return errors.New("copying restore-from-Trash transfer has a published destination")
		}
		if _, _, err := fs.inspectPreparedTrashTransferWorkspaceParentDirs(record); err != nil {
			return err
		}
		if stageExists {
			_, err := fs.scanTrashTransferOwnedContainerPartialWithOwnership(
				ctx,
				workspaceRoot,
				stageRel,
				record.WorkspaceStageIdentity,
				record.SourceManifest,
				record,
				trashTransferOwnershipRoleWorkspaceContainer,
			)
			return err
		}
		return nil
	case trashTransferReady:
		if !stageExists && !destinationExists {
			return fs.verifyTrashTransferWorkspaceParentDirsForRollback(record.WorkspaceParentDirs)
		}
		if err := fs.verifyTrashTransferWorkspaceParentDirsForRollback(record.WorkspaceParentDirs); err != nil {
			return err
		}
		if stageExists {
			identities, err := fs.scanTrashTransferOwnedContainerPartial(ctx, workspaceRoot, stageRel, record.WorkspaceStageIdentity, record.SourceManifest)
			if err != nil {
				return err
			}
			if destinationExists && len(identities) != 1 {
				return errors.New("ready restore-from-Trash has both staged payload and destination")
			}
		}
		if destinationExists {
			if _, _, err := fs.scanTrashTransferTree(ctx, workspaceRoot, destinationRel, record.ReplicaManifest, true); err != nil {
				return err
			}
		}
		return nil
	default:
		return ErrDeleteTargetChanged
	}
}

func (fs *FileSystem) rollbackPreparedRestoreTrashTransfer(ctx context.Context, record *trashTransferJournalRecord) error {
	if record == nil || record.Kind != trashTransferRestoreFromTrash || record.Decision == trashTransferCommitted || record.Decision == trashTransferCompleted {
		return ErrDeleteTargetChanged
	}
	operations, err := fs.versions.ListTrashOperations(ctx)
	if err != nil {
		return err
	}
	for index := range operations {
		operation := &operations[index]
		if operation.ID != record.OperationID && operation.TrashID != record.Item.ID {
			continue
		}
		if trashTransferOperationMatches(record, operation) {
			return errors.New("committed restore-from-Trash operation cannot be rolled back")
		}
		return versionstore.ErrTrashOperationConflict
	}
	if err := fs.preflightPreparedRestoreTrashTransferRollback(ctx, record); err != nil {
		return err
	}
	workspaceRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
	destinationRel := storageWorkspaceRelativeName(record.DestinationPath)
	var createdParentDirs []trashTransferWorkspaceParentDir
	retainParentDirs := false
	if record.Decision == trashTransferPrepared {
		createdParentDirs, retainParentDirs, err = fs.inspectPreparedTrashTransferWorkspaceParentDirs(record)
		if err != nil {
			return err
		}
		stageIdentity := record.WorkspaceStageIdentity
		if _, err := fs.filesRootHandle.Lstat(stageRel); err == nil {
			stageIdentity, _, err = fs.inspectPreparedTrashTransferOwnedContainer(
				workspaceRoot,
				stageRel,
				record,
				trashTransferOwnershipRoleWorkspaceContainer,
				record.WorkspaceStageIdentity,
			)
			if err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return mapStorageRootPathError(err)
		}
		if stageIdentity != "" || (!retainParentDirs && len(createdParentDirs) != 0) {
			owned, err := trashTransferRecordWithPreparedOwnership(record, stageIdentity, createdParentDirs, retainParentDirs)
			if err != nil {
				return err
			}
			*record = *owned
		}
	} else if record.Decision == trashTransferCopying {
		createdParentDirs, _, err = fs.inspectPreparedTrashTransferWorkspaceParentDirs(record)
		if err != nil {
			return err
		}
	}
	switch record.Decision {
	case trashTransferPrepared:
		if _, err := fs.filesRootHandle.Lstat(stageRel); err == nil {
			if err := fs.removeTrashTransferOwnedContainerPartialWithOwnership(
				ctx,
				workspaceRoot,
				stageRel,
				record.WorkspaceStageIdentity,
				record.SourceManifest,
				record,
				trashTransferOwnershipRoleWorkspaceContainer,
			); err != nil {
				return fmt.Errorf("remove prepared restore-from-Trash container: %w", err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return mapStorageRootPathError(err)
		}
	case trashTransferCopying:
		if _, err := fs.filesRootHandle.Lstat(stageRel); err == nil {
			if err := fs.removeTrashTransferOwnedContainerPartialWithOwnership(
				ctx,
				workspaceRoot,
				stageRel,
				record.WorkspaceStageIdentity,
				record.SourceManifest,
				record,
				trashTransferOwnershipRoleWorkspaceContainer,
			); err != nil {
				return fmt.Errorf("remove copying restore-from-Trash container: %w", err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return mapStorageRootPathError(err)
		}
	case trashTransferReady:
		_, stageErr := fs.filesRootHandle.Lstat(stageRel)
		_, destinationErr := fs.filesRootHandle.Lstat(destinationRel)
		if stageErr != nil && !errors.Is(stageErr, os.ErrNotExist) {
			return mapStorageRootPathError(stageErr)
		}
		if destinationErr != nil && !errors.Is(destinationErr, os.ErrNotExist) {
			return mapStorageRootPathError(destinationErr)
		}
		if destinationErr == nil {
			if err := fs.removeWorkspaceTrashTransferReplica(ctx, destinationRel, record.ReplicaManifest); err != nil {
				return fmt.Errorf("remove published restore-from-Trash replica: %w", err)
			}
		}
		if stageErr == nil {
			if err := fs.removeTrashTransferOwnedContainerPartial(ctx, workspaceRoot, stageRel, record.WorkspaceStageIdentity, record.SourceManifest); err != nil {
				return fmt.Errorf("remove ready restore-from-Trash container: %w", err)
			}
		}
	default:
		return ErrDeleteTargetChanged
	}
	parentDirsToRemove := record.WorkspaceParentDirs
	if record.Decision == trashTransferPrepared || record.Decision == trashTransferCopying {
		if err := fs.removeTrashTransferWorkspaceParentOwnershipMarkers(record, createdParentDirs, true); err != nil {
			return fmt.Errorf("remove restore-from-Trash parent ownership markers: %w", err)
		}
		parentDirsToRemove = createdParentDirs
		if record.Decision == trashTransferPrepared && retainParentDirs {
			parentDirsToRemove = nil
		}
	}
	if err := fs.removeTrashTransferWorkspaceParentDirs(parentDirsToRemove); err != nil {
		return fmt.Errorf("remove restore-from-Trash parent directories: %w", err)
	}
	return fs.rollbackTrashTransferJournal(record)
}

func (fs *FileSystem) removeWorkspaceTrashTransferReplica(ctx context.Context, rel string, expected []trashTransferManifestEntry) error {
	workspaceRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	_, identities, err := fs.scanTrashTransferTree(ctx, workspaceRoot, rel, expected, true)
	if err != nil {
		return err
	}
	parentRel := filepath.Dir(rel)
	parent, err := rootio.OpenDirNoFollow(fs.filesRootHandle, parentRel)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	defer parent.Close()
	base := filepath.Base(rel)
	if err := rootio.RemoveAllFromDirNoFollowCheckedInPlace(parent, base, func(entryPath string, info os.FileInfo) error {
		suffix := "."
		if entryPath != base {
			suffix = filepath.ToSlash(entryPath[len(base)+1:])
		}
		expectedInfo, ok := identities[suffix]
		if !ok || !os.SameFile(expectedInfo, info) || workspace.PersistentIdentityTokenForFileInfo(expectedInfo) != workspace.PersistentIdentityTokenForFileInfo(info) {
			return rootio.ErrEntryChanged
		}
		return nil
	}); err != nil {
		return mapStorageRootPathError(err)
	}
	return syncManagedStorageDir(fs.filesRootHandle, parentRel, storageAbsolutePath(workspaceRoot, parentRel))
}

func (fs *FileSystem) removeCommittedRestoreTrashSource(ctx context.Context, record *trashTransferJournalRecord, allowMissing bool) error {
	if record == nil || record.Kind != trashTransferRestoreFromTrash || (record.Decision != trashTransferCommitted && record.Decision != trashTransferCompleted) {
		return ErrDeleteTargetChanged
	}
	if err := fs.verifyTrashTransferRootIdentities(record); err != nil {
		return err
	}
	if err := fs.verifyTrashTransferWorkspaceParentDirs(record.WorkspaceParentDirs); err != nil {
		return err
	}
	stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
	if _, err := fs.filesRootHandle.Lstat(stageRel); err == nil {
		return errors.New("committed restore-from-Trash transfer retains its owned container")
	} else if !errors.Is(err, os.ErrNotExist) {
		return mapStorageRootPathError(err)
	}
	workspaceRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	destinationRel := storageWorkspaceRelativeName(record.DestinationPath)
	return fs.removeDeleteTrashTransferItemAfterPreflight(
		ctx,
		filepath.FromSlash(record.Item.ID),
		record.TrashItemIdentity,
		record.SourceManifest,
		allowMissing,
		func() error {
			if _, _, err := fs.scanTrashTransferTree(ctx, workspaceRoot, destinationRel, record.ReplicaManifest, false); err != nil {
				return fmt.Errorf("verify committed restore-from-Trash destination before source removal: %w", err)
			}
			return nil
		},
	)
}

func trashRestoreFileIndexEqual(left, right []versionstore.FileIndexEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Path != right[index].Path || left[index].Size != right[index].Size ||
			left[index].ContentHash != right[index].ContentHash || left[index].ModTime.Unix() != right[index].ModTime.Unix() {
			return false
		}
	}
	return true
}

func (fs *FileSystem) rollForwardRestoreTrashTransfer(ctx context.Context, action trashTransferRecoveryAction) error {
	record := action.record
	if record == nil || record.Kind != trashTransferRestoreFromTrash {
		return ErrDeleteTargetChanged
	}
	if err := fs.verifyTrashTransferRootIdentities(record); err != nil {
		return err
	}
	if action.operation != nil && !trashTransferOperationMatches(record, action.operation) {
		return versionstore.ErrTrashOperationConflict
	}
	if _, err := fs.versions.GetTrashItem(ctx, record.Item.ID); !errors.Is(err, versionstore.ErrNotFound) {
		if err == nil {
			return errors.New("committed restore-from-Trash item remains in metadata")
		}
		return err
	}
	if record.Decision == trashTransferReady {
		if err := fs.publishRestoreTrashTransferDestination(ctx, record); err != nil {
			return fmt.Errorf("resume ready restore-from-Trash destination publish: %w", err)
		}
	}
	if err := fs.verifyTrashTransferWorkspaceParentDirs(record.WorkspaceParentDirs); err != nil {
		return err
	}
	workspaceRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
	if _, err := fs.filesRootHandle.Lstat(stageRel); err == nil {
		return errors.New("committed restore-from-Trash transfer retains a private workspace stage")
	} else if !errors.Is(err, os.ErrNotExist) {
		return mapStorageRootPathError(err)
	}
	destinationRel := storageWorkspaceRelativeName(record.DestinationPath)
	if _, _, err := fs.scanTrashTransferTree(ctx, workspaceRoot, destinationRel, record.ReplicaManifest, false); err != nil {
		return fmt.Errorf("verify committed restore-from-Trash destination: %w", err)
	}
	wantIndex, err := trashRestoreFileIndexEntries(record)
	if err != nil {
		return err
	}
	gotIndex, err := fs.versions.ListFileIndexTree(ctx, record.DestinationPath)
	if err != nil {
		return err
	}
	if !trashRestoreFileIndexEqual(gotIndex, wantIndex) {
		return errors.New("committed restore-from-Trash file index does not match its journal")
	}
	if record.Decision == trashTransferReady {
		committed := *record
		committed.Decision = trashTransferCommitted
		if _, err := fs.publishTrashTransferJournalRecord(&committed); err != nil {
			return err
		}
		record = &committed
	}
	if action.wasCompleted {
		if _, err := fs.trashRootHandle.Lstat(filepath.FromSlash(record.Item.ID)); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return errors.New("completed restore-from-Trash transfer retains its source item")
			}
			return mapStorageRootPathError(err)
		}
	} else {
		_, participantErr := deliverTrashParticipantWithDurabilityRetry(func() error {
			return fs.applyTrashRestoreParticipant(ctx, record.OperationID, record.Item.OriginalPath, record.DestinationPath, record.ParticipantPayload)
		})
		if participantErr != nil {
			return participantErr
		}
		if err := fs.removeCommittedRestoreTrashSource(ctx, record, true); err != nil {
			return err
		}
	}
	if record.Decision != trashTransferCompleted {
		completed := *record
		completed.Decision = trashTransferCompleted
		if _, err := fs.publishTrashTransferJournalRecord(&completed); err != nil {
			return err
		}
		record = &completed
	}
	_, participantErr := deliverTrashParticipantWithDurabilityRetry(func() error {
		return fs.completeTrashRestoreParticipant(ctx, record.OperationID, record.Item.OriginalPath, record.DestinationPath, record.ParticipantPayload)
	})
	if participantErr != nil {
		return participantErr
	}
	hash, err := trashTransferJournalHash(record)
	if err != nil {
		return err
	}
	if action.operation != nil {
		if err := fs.versions.CompleteTrashOperation(ctx, record.OperationID, hash); err != nil {
			return err
		}
	} else if !action.wasCompleted {
		return errors.New("restore-from-Trash operation marker disappeared before completion")
	}
	return fs.completeTrashTransferJournal(record)
}
