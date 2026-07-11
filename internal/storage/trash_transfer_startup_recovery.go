package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/versionstore"
)

var (
	beforeTrashTransferJournalTempRemoval  = func(string) error { return nil }
	syncTrashTransferJournalTempCleanupDir = func(dir *os.File) error { return dir.Sync() }
)

// TrashTransferRecoveryReport summarizes recovery of live delete-to-Trash and
// restore-from-Trash transfers.
type TrashTransferRecoveryReport struct {
	RolledBack     int
	RolledForward  int
	Completed      int
	Blocked        []string
	UntrackedPaths []string
}

type trashTransferRecoveryRecords struct {
	prepared  *trashTransferJournalRecord
	copying   *trashTransferJournalRecord
	ready     *trashTransferJournalRecord
	committed *trashTransferJournalRecord
	completed *trashTransferJournalRecord
	stageRel  string
}

type trashTransferRecoveryAction struct {
	record       *trashTransferJournalRecord
	rollback     bool
	wasCompleted bool
	operation    *versionstore.TrashOperation
}

// RecoverTrashTransfers reconciles durable live Trash transfer journals with
// their SQLite commit markers before ordinary storage mutations are allowed.
func (fs *FileSystem) RecoverTrashTransfers(ctx context.Context) (TrashTransferRecoveryReport, error) {
	var report TrashTransferRecoveryReport
	release := fs.beginRecoveryMutation()
	defer release()
	if err := ctx.Err(); err != nil {
		return report, err
	}

	operations, err := fs.versions.ListTrashOperations(ctx)
	if err != nil {
		return report, fs.blockTrashTransferRecovery(&report, "versionstore", err)
	}
	records, untracked, scanErr := fs.scanTrashTransferRecoveryRecords(ctx)
	report.UntrackedPaths = append(report.UntrackedPaths, untracked...)
	if scanErr != nil {
		return report, fs.blockTrashTransferRecovery(&report, "journal-scan", scanErr)
	}
	if len(records) == 0 && len(operations) == 0 {
		if fs.trashMutationBlocked != nil {
			return report, fs.trashMutationBlocked
		}
		return report, nil
	}
	if fs.versions.RecoveredFromCorruption() {
		return report, fs.blockTrashTransferRecovery(&report, "versionstore", errors.New("versionstore recovery evidence was rebuilt after corruption"))
	}

	actions, classifyErr := fs.classifyTrashTransferRecovery(records, operations)
	if classifyErr != nil {
		return report, fs.blockTrashTransferRecovery(&report, "classification", classifyErr)
	}
	if err := fs.validateTrashTransferParticipantRecovery(actions); err != nil {
		return report, fs.blockTrashTransferRecovery(&report, "participants", err)
	}

	for _, action := range actions {
		if !action.rollback {
			continue
		}
		var err error
		if action.record.Kind == trashTransferDeleteToTrash {
			err = fs.preflightPreparedDeleteTrashTransferRollback(ctx, action.record)
		} else {
			err = fs.preflightPreparedRestoreTrashTransferRollback(ctx, action.record)
		}
		if err != nil {
			return report, fs.blockTrashTransferRecovery(&report, action.record.OperationID, err)
		}
	}
	for _, action := range actions {
		if !action.rollback {
			continue
		}
		var err error
		if action.record.Kind == trashTransferDeleteToTrash {
			err = fs.rollbackPreparedDeleteTrashTransfer(ctx, action.record)
		} else {
			err = fs.rollbackPreparedRestoreTrashTransfer(ctx, action.record)
		}
		if err != nil {
			return report, fs.blockTrashTransferRecovery(&report, action.record.OperationID, err)
		}
		report.RolledBack++
	}
	for _, action := range actions {
		if action.rollback {
			continue
		}
		var err error
		if action.record.Kind == trashTransferDeleteToTrash {
			err = fs.rollForwardDeleteTrashTransfer(ctx, action)
		} else {
			err = fs.rollForwardRestoreTrashTransfer(ctx, action)
		}
		if err != nil {
			return report, fs.blockTrashTransferRecovery(&report, action.record.OperationID, err)
		}
		if action.wasCompleted {
			report.Completed++
		} else {
			report.RolledForward++
		}
	}

	fs.trashMutationBlocked = nil
	report.Blocked = nil
	report.UntrackedPaths = uniqueSortedStrings(report.UntrackedPaths)
	return report, nil
}

func (fs *FileSystem) blockTrashTransferRecovery(report *TrashTransferRecoveryReport, operationID string, cause error) error {
	if operationID != "" {
		report.Blocked = uniqueSortedStrings(append(report.Blocked, operationID))
	}
	recoveryErr := errors.Join(ErrTrashRecoveryRequired, cause)
	fs.trashMutationBlocked = errors.Join(fs.trashMutationBlocked, recoveryErr)
	report.UntrackedPaths = uniqueSortedStrings(report.UntrackedPaths)
	return recoveryErr
}

func (fs *FileSystem) scanTrashTransferRecoveryRecords(ctx context.Context) (map[string]*trashTransferRecoveryRecords, []string, error) {
	return fs.scanTrashTransferRecoveryRecordsAfterTempCleanup(ctx, 0)
}

func (fs *FileSystem) scanTrashTransferRecoveryRecordsAfterTempCleanup(ctx context.Context, cleanedTemps int) (map[string]*trashTransferRecoveryRecords, []string, error) {
	records := make(map[string]*trashTransferRecoveryRecords)
	if err := fs.checkTrashRootPathIdentity(); err != nil {
		return records, nil, err
	}
	dirInfo, err := fs.trashRootHandle.Lstat(trashTransferJournalDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return records, nil, nil
		}
		return records, nil, mapStorageRootPathError(err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return records, []string{filepath.Join(fs.trashRoot, trashTransferJournalDir)}, ErrNotRegular
	}
	dirIdentity := deleteStageIdentity(dirInfo)
	if dirIdentity == "" {
		return records, nil, ErrDeleteIdentityUnavailable
	}
	dir, err := rootio.OpenDirNoFollow(fs.trashRootHandle, trashTransferJournalDir)
	if err != nil {
		return records, nil, mapStorageRootPathError(err)
	}
	opened, statErr := dir.Stat()
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if statErr != nil || readErr != nil || closeErr != nil || !sameDeleteStageEntry(dirInfo, dirIdentity, opened) {
		return records, nil, errors.Join(ErrDeleteTargetChanged, statErr, readErr, closeErr)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	untracked := make([]string, 0)
	var scanErr error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return records, untracked, err
		}
		name, err := safeStorageReadDirFallbackChildName(entry.Name())
		if err != nil {
			untracked = append(untracked, filepath.Join(fs.trashRoot, trashTransferJournalDir, entry.Name()))
			scanErr = errors.Join(scanErr, err)
			continue
		}
		if operationID, decision, ok := parseTrashTransferJournalName(name); ok {
			rel := trashTransferJournalRel(operationID, decision)
			record, err := fs.readTrashTransferJournalRecord(rel, decision)
			if err != nil || record.OperationID != operationID {
				scanErr = errors.Join(scanErr, fmt.Errorf("read Trash transfer journal %s: %w", name, errors.Join(err, ErrDeleteTargetChanged)))
				continue
			}
			group := records[operationID]
			if group == nil {
				group = &trashTransferRecoveryRecords{}
				records[operationID] = group
			}
			slot := trashTransferRecoveryRecordSlot(group, decision)
			if slot == nil || *slot != nil {
				scanErr = errors.Join(scanErr, fmt.Errorf("duplicate Trash transfer checkpoint %s", name))
				continue
			}
			*slot = record
			continue
		}
		if operationID, ok := parseTrashTransferItemStageName(name); ok {
			group := records[operationID]
			if group == nil {
				group = &trashTransferRecoveryRecords{}
				records[operationID] = group
			}
			if group.stageRel != "" {
				scanErr = errors.Join(scanErr, fmt.Errorf("duplicate Trash transfer stage %s", name))
				continue
			}
			group.stageRel = filepath.ToSlash(filepath.Join(trashTransferJournalDir, name))
			continue
		}
		if isTrashTransferJournalPublishTempName(name) {
			if cleanedTemps >= 128 {
				untracked = append(untracked, filepath.Join(fs.trashRoot, trashTransferJournalDir, name))
				return records, uniqueSortedStrings(untracked), errors.Join(scanErr, errors.New("too many Trash transfer journal publish temps require cleanup"))
			}
			if err := fs.removeTrashTransferJournalPublishTemp(name, dirInfo, dirIdentity); err != nil {
				untracked = append(untracked, filepath.Join(fs.trashRoot, trashTransferJournalDir, name))
				return records, uniqueSortedStrings(untracked), errors.Join(scanErr, fmt.Errorf("clean Trash transfer journal publish temp %s: %w", name, err))
			}
			return fs.scanTrashTransferRecoveryRecordsAfterTempCleanup(ctx, cleanedTemps+1)
		}
		untracked = append(untracked, filepath.Join(fs.trashRoot, trashTransferJournalDir, name))
		scanErr = errors.Join(scanErr, fmt.Errorf("untracked Trash transfer path %s", name))
	}
	current, err := fs.trashRootHandle.Lstat(trashTransferJournalDir)
	if err != nil || !sameDeleteStageEntry(dirInfo, dirIdentity, current) {
		scanErr = errors.Join(scanErr, ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	return records, uniqueSortedStrings(untracked), scanErr
}

func trashTransferRecoveryRecordSlot(group *trashTransferRecoveryRecords, decision string) **trashTransferJournalRecord {
	switch decision {
	case trashTransferPrepared:
		return &group.prepared
	case trashTransferCopying:
		return &group.copying
	case trashTransferReady:
		return &group.ready
	case trashTransferCommitted:
		return &group.committed
	case trashTransferCompleted:
		return &group.completed
	default:
		return nil
	}
}

func parseTrashTransferItemStageName(name string) (string, bool) {
	if strings.ContainsAny(name, `/\`) || !strings.HasPrefix(name, "transfer-") || !strings.HasSuffix(name, ".item") {
		return "", false
	}
	operationID := strings.TrimSuffix(strings.TrimPrefix(name, "transfer-"), ".item")
	return operationID, validTrashPurgeOperationID(operationID)
}

func isTrashTransferJournalPublishTempName(name string) bool {
	const prefix = ".trash-transfer-journal-"
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

func (fs *FileSystem) classifyTrashTransferRecovery(records map[string]*trashTransferRecoveryRecords, operations []versionstore.TrashOperation) ([]trashTransferRecoveryAction, error) {
	operationsByID := make(map[string]*versionstore.TrashOperation, len(operations))
	operationsByTrashID := make(map[string]*versionstore.TrashOperation, len(operations))
	for index := range operations {
		operation := &operations[index]
		if operationsByID[operation.ID] != nil || operationsByTrashID[operation.TrashID] != nil {
			return nil, versionstore.ErrTrashOperationConflict
		}
		operationsByID[operation.ID] = operation
		operationsByTrashID[operation.TrashID] = operation
	}

	operationIDs := make([]string, 0, len(records))
	for operationID := range records {
		operationIDs = append(operationIDs, operationID)
	}
	sort.Strings(operationIDs)
	actions := make([]trashTransferRecoveryAction, 0, len(operationIDs))
	claimedOperations := make(map[string]struct{}, len(operationIDs))
	claimedTrashIDs := make(map[string]string, len(operationIDs))
	claimedPaths := make(map[string]string, len(operationIDs))
	for _, operationID := range operationIDs {
		group := records[operationID]
		record, wasCompleted, err := validateTrashTransferRecoveryChain(operationID, group)
		if err != nil {
			return nil, err
		}
		if err := fs.verifyTrashTransferRootIdentities(record); err != nil {
			return nil, fmt.Errorf("verify Trash transfer %s roots: %w", operationID, err)
		}
		if owner := claimedTrashIDs[record.Item.ID]; owner != "" {
			return nil, fmt.Errorf("Trash ID %s is claimed by transfers %s and %s", record.Item.ID, owner, operationID)
		}
		operationPaths := []string{record.Item.OriginalPath}
		if record.Kind == trashTransferRestoreFromTrash && record.DestinationPath != record.Item.OriginalPath {
			operationPaths = append(operationPaths, record.DestinationPath)
		}
		for _, operationPath := range operationPaths {
			for claimedPath, owner := range claimedPaths {
				if pathMatchesOrDescendant(claimedPath, operationPath) || pathMatchesOrDescendant(operationPath, claimedPath) {
					return nil, fmt.Errorf("overlapping Trash transfer paths %s and %s are claimed by %s and %s", claimedPath, operationPath, owner, operationID)
				}
			}
			claimedPaths[operationPath] = operationID
		}
		claimedTrashIDs[record.Item.ID] = operationID

		operation := operationsByID[operationID]
		trashOperation := operationsByTrashID[record.Item.ID]
		if operation != nil && operation != trashOperation {
			return nil, versionstore.ErrTrashOperationConflict
		}
		if operation != nil {
			if !trashTransferOperationMatches(record, operation) {
				return nil, versionstore.ErrTrashOperationConflict
			}
			claimedOperations[operation.ID] = struct{}{}
		}

		action := trashTransferRecoveryAction{record: record, operation: operation, wasCompleted: wasCompleted}
		switch {
		case wasCompleted:
			action.rollback = false
		case group.committed != nil:
			if operation == nil {
				return nil, fmt.Errorf("committed Trash transfer %s has no database marker", operationID)
			}
		case group.ready != nil && operation != nil:
			action.rollback = false
		case group.ready != nil || group.copying != nil || group.prepared != nil:
			if operation != nil {
				return nil, versionstore.ErrTrashOperationConflict
			}
			action.rollback = true
		default:
			return nil, fmt.Errorf("Trash transfer %s has no recoverable checkpoint", operationID)
		}
		actions = append(actions, action)
	}
	for operationID := range operationsByID {
		if _, ok := claimedOperations[operationID]; !ok {
			return nil, fmt.Errorf("Trash operation %s has no sidecar journal", operationID)
		}
	}
	return actions, nil
}

func validateTrashTransferRecoveryChain(operationID string, group *trashTransferRecoveryRecords) (*trashTransferJournalRecord, bool, error) {
	if group == nil {
		return nil, false, errors.New("Trash transfer recovery group is missing")
	}
	if group.stageRel != "" && group.prepared == nil && group.copying == nil && group.ready == nil && group.committed == nil && group.completed == nil {
		return nil, false, fmt.Errorf("Trash transfer stage %s has no journal", operationID)
	}
	var expectedStagePath string
	for _, record := range []*trashTransferJournalRecord{group.prepared, group.copying, group.ready, group.committed, group.completed} {
		if record == nil {
			continue
		}
		if expectedStagePath == "" {
			expectedStagePath = record.TrashStagePath
		}
		if record.TrashStagePath != expectedStagePath {
			return nil, false, fmt.Errorf("Trash transfer %s has divergent stage paths", operationID)
		}
	}
	if group.stageRel != "" && group.stageRel != expectedStagePath {
		return nil, false, fmt.Errorf("Trash transfer %s stage does not match its journal", operationID)
	}
	if group.stageRel != "" && (group.committed != nil || group.completed != nil) {
		return nil, false, fmt.Errorf("committed Trash transfer %s retains a private stage", operationID)
	}
	if group.completed != nil {
		if group.completed.OperationID != operationID {
			return nil, false, ErrDeleteTargetChanged
		}
		if group.ready != nil && group.committed == nil || group.copying != nil && (group.ready == nil || group.committed == nil) || group.prepared != nil && (group.copying == nil || group.ready == nil || group.committed == nil) {
			return nil, false, fmt.Errorf("Trash transfer %s has an invalid partially cleaned completed checkpoint chain", operationID)
		}
		if group.committed != nil && !trashTransferCheckpointBodiesEqual(group.completed, group.committed) {
			return nil, false, fmt.Errorf("Trash transfer %s completed checkpoint diverges from committed", operationID)
		}
		if group.ready != nil && !trashTransferCheckpointBodiesEqual(group.completed, group.ready) {
			return nil, false, fmt.Errorf("Trash transfer %s completed checkpoint diverges from ready", operationID)
		}
		if group.copying != nil {
			if group.ready == nil || !trashTransferCopyingPlanMatchesReady(group.copying, group.ready) {
				return nil, false, fmt.Errorf("Trash transfer %s copying checkpoint diverges from ready", operationID)
			}
		}
		if group.prepared != nil {
			if group.copying == nil || !trashTransferPreparedPlanMatchesCopying(group.prepared, group.copying) {
				return nil, false, fmt.Errorf("Trash transfer %s prepared checkpoint diverges from copying", operationID)
			}
		}
		return group.completed, true, nil
	}
	if group.committed != nil {
		if group.prepared == nil || group.copying == nil || group.ready == nil ||
			!trashTransferPreparedPlanMatchesCopying(group.prepared, group.copying) ||
			!trashTransferCopyingPlanMatchesReady(group.copying, group.ready) ||
			!trashTransferCheckpointBodiesEqual(group.ready, group.committed) {
			return nil, false, fmt.Errorf("Trash transfer %s has an invalid committed checkpoint chain", operationID)
		}
		return group.committed, false, nil
	}
	if group.ready != nil {
		if group.prepared == nil || group.copying == nil ||
			!trashTransferPreparedPlanMatchesCopying(group.prepared, group.copying) ||
			!trashTransferCopyingPlanMatchesReady(group.copying, group.ready) {
			return nil, false, fmt.Errorf("Trash transfer %s has an invalid ready checkpoint chain", operationID)
		}
		return group.ready, false, nil
	}
	if group.copying != nil {
		if group.prepared == nil || !trashTransferPreparedPlanMatchesCopying(group.prepared, group.copying) {
			return nil, false, fmt.Errorf("Trash transfer %s has an invalid copying checkpoint chain", operationID)
		}
		return group.copying, false, nil
	}
	if group.prepared != nil {
		return group.prepared, false, nil
	}
	return nil, false, fmt.Errorf("Trash transfer %s has no journal", operationID)
}

func trashTransferPreparedPlanMatchesCopying(prepared, copying *trashTransferJournalRecord) bool {
	if prepared == nil || copying == nil {
		return false
	}
	projected := *copying
	projected.Decision = trashTransferPrepared
	if prepared.TrashStageIdentity == "" {
		projected.TrashStageIdentity = ""
	}
	if prepared.WorkspaceStageIdentity == "" {
		projected.WorkspaceStageIdentity = ""
	}
	projected.WorkspaceParentDirs = append([]trashTransferWorkspaceParentDir(nil), copying.WorkspaceParentDirs...)
	for index := range projected.WorkspaceParentDirs {
		if index >= len(prepared.WorkspaceParentDirs) || prepared.WorkspaceParentDirs[index].Identity == "" {
			projected.WorkspaceParentDirs[index].Identity = ""
		}
	}
	return reflect.DeepEqual(prepared, &projected)
}

func trashTransferCopyingPlanMatchesReady(copying, ready *trashTransferJournalRecord) bool {
	if copying == nil || ready == nil {
		return false
	}
	projected := *ready
	projected.Decision = trashTransferCopying
	projected.ReplicaManifest = nil
	return reflect.DeepEqual(copying, &projected)
}

func trashTransferCheckpointBodiesEqual(left, right *trashTransferJournalRecord) bool {
	leftHash, leftErr := trashTransferJournalHash(left)
	rightHash, rightErr := trashTransferJournalHash(right)
	return leftErr == nil && rightErr == nil && leftHash == rightHash
}

func (fs *FileSystem) validateTrashTransferParticipantRecovery(actions []trashTransferRecoveryAction) error {
	requiresParticipant := false
	hooks := fs.trashParticipantHooksSnapshot()
	for _, action := range actions {
		if len(action.record.ParticipantPayload) == 0 {
			continue
		}
		requiresParticipant = true
		switch action.record.Kind {
		case trashTransferDeleteToTrash:
			if (action.rollback && hooks.RollbackDelete == nil) ||
				(!action.rollback && (hooks.CompleteDelete == nil || (!action.wasCompleted && hooks.ApplyDelete == nil))) {
				return errors.New("delete-to-Trash participant recovery callbacks are unavailable")
			}
		case trashTransferRestoreFromTrash:
			if !action.rollback && (hooks.CompleteRestore == nil || (!action.wasCompleted && hooks.ApplyRestore == nil)) {
				return errors.New("restore-from-Trash participant recovery callbacks are unavailable")
			}
		}
	}
	if !requiresParticipant {
		return nil
	}
	if hooks.RecoveryStateReliable == nil {
		return errors.New("Trash participant recovery evidence is unavailable")
	}
	return hooks.RecoveryStateReliable()
}

func (fs *FileSystem) rollForwardDeleteTrashTransfer(ctx context.Context, action trashTransferRecoveryAction) error {
	record := action.record
	if record == nil || record.Kind != trashTransferDeleteToTrash {
		return ErrDeleteTargetChanged
	}
	if err := fs.verifyTrashTransferRootIdentities(record); err != nil {
		return err
	}
	stored, err := fs.versions.GetTrashItem(ctx, record.Item.ID)
	if err != nil {
		return fmt.Errorf("read committed Trash item: %w", err)
	}
	if !sameTrashPurgeItem(record.Item, trashPurgeJournalItemFromStore(stored)) {
		return errors.New("committed Trash item does not match transfer journal")
	}
	indexed, err := fs.versions.FileIndexTreeExists(ctx, record.Item.OriginalPath)
	if err != nil {
		return err
	}
	if indexed {
		return errors.New("committed delete-to-Trash source remains in the file index")
	}
	if _, _, err := fs.scanDeleteTrashTransferItem(ctx, filepath.FromSlash(record.Item.ID), record.TrashStageIdentity, record.ReplicaManifest, false); err != nil {
		return fmt.Errorf("verify committed Trash replica: %w", err)
	}
	if action.operation != nil && !trashTransferOperationMatches(record, action.operation) {
		return versionstore.ErrTrashOperationConflict
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
		for _, rel := range []string{
			storageWorkspaceRelativeName(record.Item.OriginalPath),
			storageWorkspaceRelativeName(record.WorkspaceStagePath),
		} {
			if _, err := fs.filesRootHandle.Lstat(rel); !errors.Is(err, os.ErrNotExist) {
				if err == nil {
					return errors.New("completed delete-to-Trash transfer retains its source")
				}
				return mapStorageRootPathError(err)
			}
		}
	} else {
		_, participantErr := deliverTrashParticipantWithDurabilityRetry(func() error {
			return fs.applyTrashDeleteParticipant(ctx, record.OperationID, record.Item.OriginalPath, record.ParticipantPayload, true)
		})
		if participantErr != nil {
			return participantErr
		}
		if err := fs.removeCommittedDeleteTransferSource(ctx, record); err != nil {
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
		return fs.completeTrashDeleteParticipant(ctx, record.OperationID, record.Item.OriginalPath, record.ParticipantPayload)
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
		return errors.New("delete-to-Trash operation marker disappeared before completion")
	}
	return fs.completeTrashTransferJournal(record)
}
