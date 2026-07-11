package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/versionstore"
	"github.com/seanbao/mnemonas/internal/workspace"
)

func (fs *FileSystem) commitJournaledTrashDeleteLocked(ctx context.Context, name string, policy DeletePolicy, expected DeleteTargetSnapshot) error {
	if err := fs.checkTrashMutationAllowedLocked(); err != nil {
		return err
	}

	operationID, trashID, err := fs.allocateTrashTransferDeleteIDs(ctx, name)
	if err != nil {
		return err
	}
	verified, sourceManifest, err := fs.captureTrashTransferDeleteSource(ctx, name, expected)
	if err != nil {
		return err
	}
	hadVersions, err := fs.deleteSnapshotHadVersions(ctx, verified)
	if err != nil {
		return err
	}
	participantPayload, err := fs.prepareTrashDeleteParticipant(ctx, operationID, name)
	if err != nil {
		return fmt.Errorf("prepare delete-to-Trash participant: %w", err)
	}

	deletedAt := time.Now().Truncate(time.Second)
	trashItem := &versionstore.TrashItem{
		ID:           trashID,
		OriginalPath: name,
		Size:         deleteSnapshotTargetSize(verified),
		DeletedAt:    deletedAt,
		ExpiresAt:    deletedAt.AddDate(0, 0, policy.TrashRetentionDays),
		IsDir:        verified.Root.IsDir,
		HadVersions:  hadVersions,
		RestoreData:  append([]byte(nil), participantPayload...),
	}
	filesRootIdentity, trashRootIdentity, err := fs.captureTrashTransferRootIdentities()
	if err != nil {
		return err
	}
	record := &trashTransferJournalRecord{
		Version:            trashTransferJournalVersion,
		Decision:           trashTransferPrepared,
		Kind:               trashTransferDeleteToTrash,
		OperationID:        operationID,
		FilesRootIdentity:  filesRootIdentity,
		TrashRootIdentity:  trashRootIdentity,
		Item:               trashPurgeJournalItemFromStore(trashItem),
		WorkspaceStagePath: trashTransferWorkspaceStagePath(filepath.ToSlash(filepath.Dir(name)), operationID),
		TrashStagePath:     trashTransferItemStageRel(operationID),
		SourceManifest:     sourceManifest,
		ParticipantPayload: append([]byte(nil), participantPayload...),
	}
	if err := validateTrashTransferJournalRecord(record, trashTransferPrepared); err != nil {
		return err
	}
	published, err := fs.publishTrashTransferJournalRecord(record)
	if err != nil {
		if published {
			return fs.blockTrashTransferLocked(record, fmt.Errorf("sync prepared delete-to-Trash journal: %w", err))
		}
		return fmt.Errorf("persist prepared delete-to-Trash journal: %w", err)
	}

	trashRoot := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
	trashStageRel := filepath.FromSlash(record.TrashStagePath)
	trashStageIdentity, _, err := fs.createPreparedTrashTransferOwnedContainer(
		trashRoot,
		trashStageRel,
		record,
		trashTransferOwnershipRoleTrashContainer,
	)
	if err != nil {
		return fs.rollbackUnpublishedPreparedDeleteTrashTransfer(ctx, record, fmt.Errorf("create delete-to-Trash owned container: %w", err))
	}
	copying := *record
	copying.Decision = trashTransferCopying
	copying.TrashStageIdentity = trashStageIdentity
	if err := validateTrashTransferJournalRecord(&copying, trashTransferCopying); err != nil {
		return fs.rollbackUnpublishedPreparedDeleteTrashTransfer(ctx, record, err)
	}
	published, err = fs.publishTrashTransferJournalRecord(&copying)
	if err != nil {
		if published {
			return fs.blockTrashTransferLocked(&copying, fmt.Errorf("sync copying delete-to-Trash journal: %w", err))
		}
		return fs.rollbackUnpublishedPreparedDeleteTrashTransfer(ctx, record, fmt.Errorf("persist copying delete-to-Trash journal: %w", err))
	}
	record = &copying
	if err := fs.removeTrashTransferOwnershipMarker(
		trashRoot,
		trashStageRel,
		record,
		trashTransferOwnershipRoleTrashContainer,
		record.TrashStageIdentity,
		false,
	); err != nil {
		return fs.blockTrashTransferLocked(record, fmt.Errorf("remove delete-to-Trash ownership marker: %w", err))
	}

	var target *stagedDeleteTarget
	rollback := func(cause error, rollbackRecord *trashTransferJournalRecord) error {
		rollbackErr := fs.rollbackPreparedDeleteTrashTransfer(context.WithoutCancel(ctx), rollbackRecord)
		if rollbackErr != nil {
			if isTrashTransferTerminalCleanupWarning(rollbackErr) {
				return fs.blockTrashTransferLocked(rollbackRecord, errors.Join(cause, rollbackErr))
			}
			rollbackErr = fs.preserveJournaledTrashDeleteWitnessLocked(target, rollbackErr)
			return fs.blockTrashTransferLocked(rollbackRecord, errors.Join(cause, rollbackErr))
		}
		return cause
	}

	plannedStageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
	target, err = fs.stageDeleteTargetAtLocked(ctx, name, verified, func() error {
		return afterStorageCopySourceStat(fs.workspace.FullPath(name))
	}, plannedStageRel)
	if err != nil {
		return rollback(err, record)
	}
	defer target.close()
	target.expected = verified

	workspaceRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	if _, _, err := fs.scanTrashTransferTree(ctx, workspaceRoot, target.stageRel, record.SourceManifest, false); err != nil {
		return rollback(fmt.Errorf("verify staged delete-to-Trash source: %w", err), record)
	}

	trashContentRel := filepath.Join(filepath.FromSlash(record.TrashStagePath), "content")
	trashContentPath := storageAbsolutePath(trashRoot, trashContentRel)
	copied, err := fs.copyStagedDeleteToTrashLocked(ctx, target, trashContentPath)
	if err != nil {
		return rollback(fmt.Errorf("copy staged target to private Trash stage: %w", err), record)
	}
	replicaManifest, _, err := fs.scanTrashTransferTree(ctx, trashRoot, trashContentRel, nil, false)
	if err != nil {
		return rollback(fmt.Errorf("capture delete-to-Trash replica manifest: %w", err), record)
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
			return fs.blockTrashTransferLocked(&ready, fmt.Errorf("sync ready delete-to-Trash journal: %w", err))
		}
		return rollback(fmt.Errorf("persist ready delete-to-Trash journal: %w", err), &ready)
	}
	record = &ready

	if err := fs.publishDeleteTrashTransferItem(ctx, record); err != nil {
		return rollback(fmt.Errorf("publish delete-to-Trash item: %w", err), record)
	}
	copied.rel = filepath.Join(filepath.FromSlash(record.Item.ID), "content")
	copied.abs = storageAbsolutePath(trashRoot, copied.rel)

	persistenceWarning := target.warning
	if err := fs.applyTrashDeleteParticipant(ctx, operationID, name, participantPayload, false); err != nil {
		if workspace.IsVisibleMutationWarning(err) || isVisibleMutationWarning(err) {
			persistenceWarning = errors.Join(persistenceWarning, fmt.Errorf("apply delete-to-Trash participant: %w", err))
		} else {
			return rollback(fmt.Errorf("apply delete-to-Trash participant: %w", err), record)
		}
	}

	operation, err := trashTransferOperationForRecord(record)
	if err != nil {
		return rollback(err, record)
	}
	commitTrashDelete := fs.commitTrashDelete
	if commitTrashDelete == nil {
		commitTrashDelete = fs.versions.CommitTrashDelete
	}
	commitErr := commitTrashDelete(ctx, trashItem, operation)
	committed, resolveErr := fs.resolveTrashDeleteCommit(context.WithoutCancel(ctx), record, trashItem, commitErr)
	if resolveErr != nil {
		return fs.blockTrashTransferLocked(record, resolveErr)
	}
	if !committed {
		return rollback(fmt.Errorf("commit delete-to-Trash metadata: %w", commitErr), record)
	}

	committedRecord := *record
	committedRecord.Decision = trashTransferCommitted
	published, err = fs.publishTrashTransferJournalRecord(&committedRecord)
	if err != nil {
		return fs.blockTrashTransferLocked(&committedRecord, fmt.Errorf("persist committed delete-to-Trash journal: %w", err))
	}
	record = &committedRecord
	recoveryCtx := context.WithoutCancel(ctx)
	participantWarning, participantErr := deliverTrashParticipantWithDurabilityRetry(func() error {
		return fs.applyTrashDeleteParticipant(recoveryCtx, operationID, name, participantPayload, true)
	})
	if participantErr != nil {
		return fs.blockTrashTransferLocked(record, fmt.Errorf("deliver committed delete-to-Trash participant: %w", participantErr))
	}
	if participantWarning != nil {
		persistenceWarning = errors.Join(persistenceWarning, fmt.Errorf("deliver committed delete-to-Trash participant: %w", participantWarning))
	}
	if err := fs.removeCommittedDeleteTransferSource(recoveryCtx, record); err != nil {
		return fs.blockTrashTransferLocked(record, fmt.Errorf("remove committed delete-to-Trash source: %w", err))
	}

	completedRecord := *record
	completedRecord.Decision = trashTransferCompleted
	published, err = fs.publishTrashTransferJournalRecord(&completedRecord)
	if err != nil {
		return fs.blockTrashTransferLocked(&completedRecord, fmt.Errorf("persist completed delete-to-Trash journal: %w", err))
	}
	record = &completedRecord
	participantWarning, participantErr = deliverTrashParticipantWithDurabilityRetry(func() error {
		return fs.completeTrashDeleteParticipant(recoveryCtx, operationID, name, participantPayload)
	})
	if participantErr != nil {
		return fs.blockTrashTransferLocked(record, fmt.Errorf("complete delete-to-Trash participant: %w", participantErr))
	}
	if participantWarning != nil {
		persistenceWarning = errors.Join(persistenceWarning, fmt.Errorf("complete delete-to-Trash participant: %w", participantWarning))
	}
	hash, err := trashTransferJournalHash(record)
	if err != nil {
		return fs.blockTrashTransferLocked(record, err)
	}
	if err := fs.versions.CompleteTrashOperation(recoveryCtx, operationID, hash); err != nil {
		return fs.blockTrashTransferLocked(record, fmt.Errorf("acknowledge delete-to-Trash participant: %w", err))
	}
	if err := fs.completeTrashTransferJournal(record); err != nil {
		cleanupErr := fmt.Errorf("cleanup delete-to-Trash journal: %w", err)
		recoveryErr := fs.blockTrashTransferLocked(record, cleanupErr)
		if isTrashTransferTerminalCleanupWarning(err) {
			return errors.Join(
				wrapVisibleMutationWarning(errors.Join(persistenceWarning, cleanupErr)),
				recoveryErr,
			)
		}
		return recoveryErr
	}

	var cleanupWarning error
	if err := fs.ensureTrashCapacityLocked(recoveryCtx, 0, trashID); err != nil {
		cleanupWarning = wrapTrashDeleteWarning(fmt.Errorf("enforce Trash capacity: %w", err))
	}
	if persistenceWarning != nil || cleanupWarning != nil {
		return errors.Join(wrapVisibleMutationWarning(persistenceWarning), cleanupWarning)
	}
	return nil
}

func (fs *FileSystem) rollbackUnpublishedPreparedDeleteTrashTransfer(ctx context.Context, record *trashTransferJournalRecord, cause error) error {
	rollbackErr := fs.rollbackPreparedDeleteTrashTransfer(context.WithoutCancel(ctx), record)
	if rollbackErr == nil {
		return cause
	}
	if isTrashTransferTerminalCleanupWarning(rollbackErr) {
		return fs.blockTrashTransferLocked(record, errors.Join(cause, rollbackErr))
	}
	return fs.blockTrashTransferLocked(record, errors.Join(cause, rollbackErr))
}

func (fs *FileSystem) allocateTrashTransferDeleteIDs(ctx context.Context, sourcePath string) (string, string, error) {
	operations, err := fs.versions.ListTrashOperations(ctx)
	if err != nil {
		return "", "", err
	}
	operationIDs := make(map[string]struct{}, len(operations))
	trashIDs := make(map[string]struct{}, len(operations))
	for _, operation := range operations {
		operationIDs[operation.ID] = struct{}{}
		trashIDs[operation.TrashID] = struct{}{}
	}
	for range 32 {
		operationID, err := newTrashPurgeOperationID()
		if err != nil {
			return "", "", fmt.Errorf("generate delete-to-Trash operation ID: %w", err)
		}
		trashID, err := generateID()
		if err != nil {
			return "", "", fmt.Errorf("generate Trash item ID: %w", err)
		}
		if _, exists := operationIDs[operationID]; exists {
			continue
		}
		if _, exists := trashIDs[trashID]; exists {
			continue
		}
		if _, err := fs.versions.GetTrashItem(ctx, trashID); err == nil {
			continue
		} else if !errors.Is(err, versionstore.ErrNotFound) {
			return "", "", err
		}
		workspaceStageRel := storageWorkspaceRelativeName(trashTransferWorkspaceStagePath(filepath.ToSlash(filepath.Dir(sourcePath)), operationID))
		if _, err := fs.filesRootHandle.Lstat(workspaceStageRel); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", "", mapStorageRootPathError(err)
		}
		collision := false
		for _, rel := range []string{trashTransferItemStageRel(operationID), trashID} {
			if _, err := fs.trashRootHandle.Lstat(filepath.FromSlash(rel)); err == nil {
				collision = true
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				return "", "", mapStorageRootPathError(err)
			}
		}
		if collision {
			continue
		}
		for _, decision := range []string{trashTransferPrepared, trashTransferCopying, trashTransferReady, trashTransferCommitted, trashTransferCompleted} {
			if _, err := fs.trashRootHandle.Lstat(filepath.FromSlash(trashTransferJournalRel(operationID, decision))); err == nil {
				collision = true
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				return "", "", mapStorageRootPathError(err)
			}
		}
		if !collision {
			return operationID, trashID, nil
		}
	}
	return "", "", errors.New("failed to allocate delete-to-Trash transaction identifiers")
}

func (fs *FileSystem) preserveJournaledTrashDeleteWitnessLocked(target *stagedDeleteTarget, cause error) error {
	if target == nil || target.witness == nil || target.witnessInfo == nil || !target.witnessInfo.Mode().IsRegular() {
		return cause
	}
	for _, rel := range []string{target.originalRel, target.stageRel} {
		if rel == "" {
			continue
		}
		current, err := fs.filesRootHandle.Lstat(rel)
		if err == nil && os.SameFile(target.witnessInfo, current) {
			return cause
		}
	}

	recoveryPath, recoveryErr := fs.preserveDeleteWitnessRecoveryLocked(target)
	inspectionPaths := make([]string, 0, 2)
	if target.stageAbs != "" {
		inspectionPaths = append(inspectionPaths, target.stageAbs)
	}
	if recoveryPath != "" {
		inspectionPaths = append(inspectionPaths, recoveryPath)
	}
	var recoveryFailure *deleteWitnessRecoveryError
	if errors.As(recoveryErr, &recoveryFailure) {
		inspectionPaths = append(inspectionPaths, recoveryFailure.paths...)
	}
	return errors.Join(cause, &DeleteStageResidualError{
		Path:            target.logicalName,
		StagePath:       recoveryPath,
		InspectionPaths: uniqueSortedStrings(inspectionPaths),
		err: errors.Join(
			recoveryErr,
			errors.New("trusted delete source was no longer reachable by its journaled paths"),
		),
	})
}

func (fs *FileSystem) captureTrashTransferDeleteSource(ctx context.Context, name string, expected DeleteTargetSnapshot) (DeleteTargetSnapshot, []trashTransferManifestEntry, error) {
	root := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	manifest, identities, err := fs.scanTrashTransferTree(ctx, root, storageWorkspaceRelativeName(name), nil, false)
	if err != nil {
		return DeleteTargetSnapshot{}, nil, err
	}
	expectedByPath := deleteSnapshotEntryMap(expected)
	if len(manifest) != len(expectedByPath) {
		return DeleteTargetSnapshot{}, nil, &DeleteTargetChangedError{Path: name}
	}
	verified := expected
	verified.Entries = append([]FileInfo(nil), expected.Entries...)
	verifiedByPath := make(map[string]*FileInfo, len(verified.Entries))
	for index := range verified.Entries {
		verifiedByPath[verified.Entries[index].Path] = &verified.Entries[index]
	}
	for _, entry := range manifest {
		logicalPath := name
		if entry.Path != "." {
			logicalPath = name + "/" + entry.Path
		}
		want, ok := expectedByPath[logicalPath]
		info := identities[entry.Path]
		actual, actualOK := verifiedByPath[logicalPath]
		if !ok || !actualOK || info == nil ||
			want.IsDir != (entry.Kind == "dir") ||
			want.Size != info.Size() ||
			want.ModTime.UnixNano() != entry.ModTimeUnixNano ||
			uint32(storagePreservedMode(want.Mode)) != entry.Mode ||
			want.DeleteIdentityToken == "" ||
			want.DeleteIdentityToken != workspace.DeleteIdentityTokenForFileInfo(info) ||
			(want.ContentHash != "" && want.ContentHash != entry.ContentHash) {
			return DeleteTargetSnapshot{}, nil, &DeleteTargetChangedError{Path: name}
		}
		if entry.Kind == "file" {
			actual.ContentHash = entry.ContentHash
		}
	}
	rootEntry, ok := verifiedByPath[name]
	if !ok {
		return DeleteTargetSnapshot{}, nil, &DeleteTargetChangedError{Path: name}
	}
	verified.Root = *rootEntry
	return verified, manifest, nil
}

func (fs *FileSystem) resolveTrashDeleteCommit(ctx context.Context, record *trashTransferJournalRecord, item *versionstore.TrashItem, commitErr error) (bool, error) {
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
		return false, errors.New("delete-to-Trash commit returned success without an outbox marker")
	}
	persisted, itemErr := fs.versions.GetTrashItem(ctx, item.ID)
	if itemErr == nil {
		return false, fmt.Errorf("delete-to-Trash item %s exists without its operation marker: %+v", item.ID, persisted)
	}
	if !errors.Is(itemErr, versionstore.ErrNotFound) {
		return false, errors.Join(commitErr, itemErr)
	}
	operations, listErr := fs.versions.ListTrashOperations(ctx)
	if listErr != nil {
		return false, errors.Join(commitErr, listErr)
	}
	for index := range operations {
		if operations[index].TrashID == item.ID {
			return false, versionstore.ErrTrashOperationConflict
		}
	}
	return false, nil
}

func (fs *FileSystem) removeCommittedDeleteTransferSource(ctx context.Context, record *trashTransferJournalRecord) error {
	if record == nil || record.Kind != trashTransferDeleteToTrash || (record.Decision != trashTransferCommitted && record.Decision != trashTransferCompleted) {
		return ErrDeleteTargetChanged
	}
	if err := fs.verifyTrashTransferRootIdentities(record); err != nil {
		return err
	}
	originalRel := storageWorkspaceRelativeName(record.Item.OriginalPath)
	if _, err := fs.filesRootHandle.Lstat(originalRel); err == nil {
		return errors.New("committed delete-to-Trash source is visible at its original path")
	} else if !errors.Is(err, os.ErrNotExist) {
		return mapStorageRootPathError(err)
	}
	if _, _, err := fs.scanDeleteTrashTransferItem(ctx, filepath.FromSlash(record.Item.ID), record.TrashStageIdentity, record.ReplicaManifest, false); err != nil {
		return fmt.Errorf("verify committed Trash replica: %w", err)
	}
	stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
	if _, err := fs.filesRootHandle.Lstat(stageRel); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return mapStorageRootPathError(err)
	}
	workspaceRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	_, identities, err := fs.scanTrashTransferTree(ctx, workspaceRoot, stageRel, record.SourceManifest, true)
	if err != nil {
		return err
	}
	parentRel := filepath.Dir(stageRel)
	parent, err := rootio.OpenDirNoFollow(fs.filesRootHandle, parentRel)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	defer parent.Close()
	base := filepath.Base(stageRel)
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
