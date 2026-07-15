package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/versionstore"
	"github.com/seanbao/mnemonas/internal/workspace"
)

// WriteTransactionRecoveryReport summarizes startup reconciliation of durable
// streamed-write transactions.
type WriteTransactionRecoveryReport struct {
	RolledBack      int
	RolledForward   int
	Blocked         []string
	InspectionPaths []string
}

type writeTransactionRecoveryStore interface {
	InspectWriteMetadata(context.Context, versionstore.WriteMetadataPlan) (versionstore.WriteMetadataState, error)
	RollbackWriteMetadata(context.Context, versionstore.WriteMetadataPlan) error
	EnsureWriteMetadataCommitted(context.Context, versionstore.WriteMetadataPlan) error
	GetObject(context.Context, string) ([]byte, error)
	PutObjectExpected(context.Context, []byte, string) (versionstore.ObjectPutResult, error)
	HasVersionReference(context.Context, string) (bool, error)
	DeleteObject(context.Context, string) error
}

// writeTransactionRecoveryFaultHook is replaced only by package tests.
var writeTransactionRecoveryFaultHook = func(string) error { return nil }

type writeTransactionRecoveryBlockedError struct {
	operationID     string
	inspectionPaths []string
	err             error
}

func (e *writeTransactionRecoveryBlockedError) Error() string {
	if e == nil {
		return ErrWriteRecoveryRequired.Error()
	}
	message := ErrWriteRecoveryRequired.Error()
	if e.operationID != "" {
		message += ": operation " + e.operationID
	}
	if len(e.inspectionPaths) != 0 {
		message += "; inspect " + strings.Join(e.inspectionPaths, ", ")
	}
	if e.err != nil {
		message += "; cause: " + e.err.Error()
	}
	return message
}

func (e *writeTransactionRecoveryBlockedError) Unwrap() error {
	if e == nil || e.err == nil {
		return ErrWriteRecoveryRequired
	}
	return errors.Join(ErrWriteRecoveryRequired, e.err)
}

type writeTransactionObservedObject struct {
	path               string
	info               os.FileInfo
	persistentIdentity string
	deleteIdentity     string
	mode               os.FileMode
	size               int64
	modTimeUnixNano    int64
	hash               string
}

type writeTransactionObjectClass uint8

const (
	writeTransactionObjectAbsent writeTransactionObjectClass = iota
	writeTransactionObjectBefore
	writeTransactionObjectAfter
)

type writeTransactionPhysicalLayout struct {
	targetClass writeTransactionObjectClass
	target      *writeTransactionObservedObject
	stageClass  writeTransactionObjectClass
	stage       *writeTransactionObservedObject
}

type writeTransactionNamespaceSyncPriority uint8

const (
	writeTransactionSyncSourceFirst writeTransactionNamespaceSyncPriority = iota
	writeTransactionSyncTargetFirst
)

func (fs *FileSystem) recoverWriteTransactions(
	ctx context.Context,
	journal *WriteTransactionJournal,
) (WriteTransactionRecoveryReport, error) {
	if fs == nil || fs.versions == nil {
		return WriteTransactionRecoveryReport{}, errors.New("write transaction metadata store is unavailable")
	}
	return fs.recoverWriteTransactionsWithStore(ctx, journal, fs.versions)
}

func (fs *FileSystem) recoverWriteTransactionsWithStore(
	ctx context.Context,
	journal *WriteTransactionJournal,
	store writeTransactionRecoveryStore,
) (WriteTransactionRecoveryReport, error) {
	var report WriteTransactionRecoveryReport
	if fs == nil {
		return report, errors.New("write transaction filesystem is unavailable")
	}
	if journal == nil {
		return report, errors.New("write transaction journal is unavailable")
	}
	if store == nil {
		return report, errors.New("write transaction metadata store is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}

	fs.writeStagingMu.Lock()
	defer fs.writeStagingMu.Unlock()
	release := fs.beginRecoveryMutation()
	defer release()
	if err := fs.ensureOpenLocked(); err != nil {
		return report, err
	}
	if err := fs.validateWriteTransactionRecoveryInfrastructure(journal); err != nil {
		return fs.blockWriteTransactionRecovery(report, "journal", nil, err)
	}

	operations, err := journal.Scan()
	if err != nil {
		return fs.blockWriteTransactionRecovery(
			report,
			"journal",
			fs.writeTransactionJournalInspectionPaths(),
			err,
		)
	}
	if err := fs.validateWriteTransactionRecoveryInfrastructure(journal); err != nil {
		return fs.blockWriteTransactionRecovery(report, "journal", nil, err)
	}

	var blockedErrors []error
	for _, operation := range operations {
		inspectionPaths := fs.writeTransactionInspectionPaths(operation.Record.Plan)
		report.InspectionPaths = uniqueSortedStrings(append(report.InspectionPaths, inspectionPaths...))
		if err := fs.recoverOneWriteTransaction(ctx, journal, store, operation); err != nil {
			blockedReport, blockedErr := fs.blockWriteTransactionRecovery(
				report,
				operation.OperationID,
				inspectionPaths,
				err,
			)
			report = blockedReport
			blockedErrors = append(blockedErrors, blockedErr)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return report, errors.Join(append(blockedErrors, ctxErr)...)
			}
			if infrastructureErr := fs.validateWriteTransactionRecoveryInfrastructure(journal); infrastructureErr != nil {
				return report, errors.Join(append(blockedErrors, infrastructureErr)...)
			}
			continue
		}
		switch operation.Decision {
		case WriteTransactionDecisionRollback:
			report.RolledBack++
		case WriteTransactionDecisionRollforward:
			report.RolledForward++
		default:
			blockedReport, blockedErr := fs.blockWriteTransactionRecovery(
				report,
				operation.OperationID,
				inspectionPaths,
				ErrWriteTransactionJournalCorrupt,
			)
			report = blockedReport
			blockedErrors = append(blockedErrors, blockedErr)
		}
	}
	if err := fs.validateWriteTransactionRecoveryInfrastructure(journal); err != nil {
		return fs.blockWriteTransactionRecovery(report, "journal", nil, err)
	}
	if len(blockedErrors) != 0 {
		return report, errors.Join(blockedErrors...)
	}
	return report, nil
}

func (fs *FileSystem) blockWriteTransactionRecovery(
	report WriteTransactionRecoveryReport,
	operationID string,
	inspectionPaths []string,
	cause error,
) (WriteTransactionRecoveryReport, error) {
	report.Blocked = uniqueSortedStrings(append(report.Blocked, operationID))
	report.InspectionPaths = uniqueSortedStrings(append(report.InspectionPaths, inspectionPaths...))
	recoveryErr := &writeTransactionRecoveryBlockedError{
		operationID:     operationID,
		inspectionPaths: uniqueSortedStrings(inspectionPaths),
		err:             cause,
	}
	return report, recoveryErr
}

func (fs *FileSystem) recoverOneWriteTransaction(
	ctx context.Context,
	journal *WriteTransactionJournal,
	store writeTransactionRecoveryStore,
	operation WriteTransactionOperation,
) error {
	plan := operation.Record.Plan
	if operation.OperationID == "" || operation.Record.OperationID != operation.OperationID {
		return ErrWriteTransactionJournalCorrupt
	}
	if operation.Decision == WriteTransactionDecisionRollforward {
		if operation.Committed == nil {
			return fmt.Errorf("%w: roll-forward lacks committed checkpoint", ErrWriteTransactionJournalCorrupt)
		}
	} else if operation.Decision == WriteTransactionDecisionRollback {
		if operation.Committed != nil {
			return fmt.Errorf("%w: rollback has committed checkpoint", ErrWriteTransactionJournalCorrupt)
		}
	} else {
		return fmt.Errorf("%w: unknown recovery decision", ErrWriteTransactionJournalCorrupt)
	}
	if err := callWriteTransactionRecoveryFaultHook(operation.OperationID + ":before"); err != nil {
		return err
	}
	if err := fs.validateWriteTransactionRecoveryRoots(journal, plan); err != nil {
		return err
	}

	metadataState, err := store.InspectWriteMetadata(ctx, plan.Metadata)
	if err != nil {
		return fmt.Errorf("inspect write metadata: %w", err)
	}
	switch metadataState {
	case versionstore.WriteMetadataStateBefore,
		versionstore.WriteMetadataStateAfter,
		versionstore.WriteMetadataStateBoth:
	case versionstore.WriteMetadataStateConflict:
		return versionstore.ErrWriteMetadataConflict
	default:
		return fmt.Errorf(
			"%w: unknown write metadata state %q",
			versionstore.ErrWriteMetadataConflict,
			metadataState,
		)
	}
	layout, err := fs.inspectWriteTransactionPhysicalLayout(ctx, plan)
	if err != nil {
		return err
	}
	if err := fs.validateWriteTransactionTargetParentForRecovery(
		plan,
		operation.Decision,
		layout,
	); err != nil {
		return err
	}
	if err := fs.validateWriteTransactionCreatedDirectoryBase(plan); err != nil {
		return err
	}

	switch operation.Decision {
	case WriteTransactionDecisionRollback:
		return fs.rollbackWriteTransaction(
			ctx,
			journal,
			store,
			operation,
			metadataState,
			layout,
		)
	case WriteTransactionDecisionRollforward:
		return fs.rollforwardWriteTransaction(
			ctx,
			journal,
			store,
			operation,
			metadataState,
			layout,
		)
	default:
		return ErrWriteTransactionJournalCorrupt
	}
}

func (fs *FileSystem) rollbackWriteTransaction(
	ctx context.Context,
	journal *WriteTransactionJournal,
	store writeTransactionRecoveryStore,
	operation WriteTransactionOperation,
	metadataState versionstore.WriteMetadataState,
	layout writeTransactionPhysicalLayout,
) error {
	plan := operation.Record.Plan
	layout, err := fs.ensureWriteTransactionPhysicalBefore(ctx, journal, plan, layout)
	if err != nil {
		return err
	}
	if err := callWriteTransactionRecoveryFaultHook(operation.OperationID + ":after-physical"); err != nil {
		return err
	}
	if err := store.RollbackWriteMetadata(ctx, plan.Metadata); err != nil {
		return fmt.Errorf("rollback write metadata from %s: %w", metadataState, err)
	}
	metadataState, err = store.InspectWriteMetadata(ctx, plan.Metadata)
	if err != nil {
		return fmt.Errorf("verify rolled-back write metadata: %w", err)
	}
	if metadataState != versionstore.WriteMetadataStateBefore &&
		metadataState != versionstore.WriteMetadataStateBoth {
		return versionstore.ErrWriteMetadataConflict
	}
	if err := callWriteTransactionRecoveryFaultHook(operation.OperationID + ":after-metadata"); err != nil {
		return err
	}
	if err := fs.rollbackWriteTransactionCAS(ctx, store, operation, metadataState); err != nil {
		return err
	}
	if err := fs.validateWriteTransactionCreatedDirectoryBase(plan); err != nil {
		return err
	}
	if err := callWriteTransactionRecoveryFaultHook(operation.OperationID + ":before-stage-cleanup"); err != nil {
		return err
	}
	if layout.stage != nil {
		if layout.stageClass != writeTransactionObjectAfter {
			return fmt.Errorf("%w: rollback residue is not the new object", ErrWriteTransactionJournalCorrupt)
		}
		if err := fs.removeWriteTransactionStage(ctx, journal, plan, *layout.stage, plan.Target.After); err != nil {
			return err
		}
	}
	if plan.Kind == WriteTransactionKindCreate {
		if err := fs.cleanupWriteTransactionCreatedDirectories(journal, plan); err != nil {
			return err
		}
	}
	if err := callWriteTransactionRecoveryFaultHook(operation.OperationID + ":before-journal-cleanup"); err != nil {
		return err
	}
	if err := fs.validateWriteTransactionFinalState(
		ctx,
		journal,
		store,
		operation,
		WriteTransactionDecisionRollback,
	); err != nil {
		return err
	}
	if err := journal.CleanupRollback(operation.OperationID); err != nil {
		return fmt.Errorf("cleanup rolled-back write transaction journal: %w", err)
	}
	return fs.validateWriteTransactionRecoveryInfrastructure(journal)
}

func (fs *FileSystem) rollforwardWriteTransaction(
	ctx context.Context,
	journal *WriteTransactionJournal,
	store writeTransactionRecoveryStore,
	operation WriteTransactionOperation,
	metadataState versionstore.WriteMetadataState,
	layout writeTransactionPhysicalLayout,
) error {
	plan := operation.Record.Plan
	layout, err := fs.ensureWriteTransactionPhysicalAfter(ctx, journal, plan, layout)
	if err != nil {
		return err
	}
	if err := callWriteTransactionRecoveryFaultHook(operation.OperationID + ":after-physical"); err != nil {
		return err
	}
	if err := fs.ensureWriteTransactionCAS(ctx, store, operation, layout); err != nil {
		return err
	}
	if err := store.EnsureWriteMetadataCommitted(ctx, plan.Metadata); err != nil {
		return fmt.Errorf("commit write metadata from %s: %w", metadataState, err)
	}
	metadataState, err = store.InspectWriteMetadata(ctx, plan.Metadata)
	if err != nil {
		return fmt.Errorf("verify committed write metadata: %w", err)
	}
	if metadataState != versionstore.WriteMetadataStateAfter &&
		metadataState != versionstore.WriteMetadataStateBoth {
		return versionstore.ErrWriteMetadataConflict
	}
	if err := callWriteTransactionRecoveryFaultHook(operation.OperationID + ":after-metadata"); err != nil {
		return err
	}
	if err := callWriteTransactionRecoveryFaultHook(operation.OperationID + ":before-stage-cleanup"); err != nil {
		return err
	}
	if layout.stage != nil {
		if layout.stageClass != writeTransactionObjectBefore {
			return fmt.Errorf("%w: roll-forward residue is not the old object", ErrWriteTransactionJournalCorrupt)
		}
		if plan.OldTarget == nil {
			return ErrWriteTransactionJournalCorrupt
		}
		if err := fs.removeWriteTransactionStage(ctx, journal, plan, *layout.stage, *plan.OldTarget); err != nil {
			return err
		}
	}
	if err := callWriteTransactionRecoveryFaultHook(operation.OperationID + ":before-journal-cleanup"); err != nil {
		return err
	}
	if err := fs.validateWriteTransactionFinalState(
		ctx,
		journal,
		store,
		operation,
		WriteTransactionDecisionRollforward,
	); err != nil {
		return err
	}
	if err := journal.CleanupRollforward(operation.OperationID); err != nil {
		return fmt.Errorf("cleanup committed write transaction journal: %w", err)
	}
	return fs.validateWriteTransactionRecoveryInfrastructure(journal)
}

func (fs *FileSystem) inspectWriteTransactionPhysicalLayout(
	ctx context.Context,
	plan WriteTransactionPlan,
) (writeTransactionPhysicalLayout, error) {
	var layout writeTransactionPhysicalLayout
	target, targetErr := inspectWriteTransactionObject(ctx, fs.filesRootHandle, plan.Target.RelativePath)
	switch {
	case errors.Is(targetErr, os.ErrNotExist):
		layout.targetClass = writeTransactionObjectAbsent
	case targetErr != nil:
		return layout, fmt.Errorf("inspect write target: %w", targetErr)
	case target.matchesExpectation(plan.Target.After):
		layout.targetClass = writeTransactionObjectAfter
		layout.target = target
	case plan.Target.Before != nil && target.matchesEvidenceStable(*plan.Target.Before):
		layout.targetClass = writeTransactionObjectBefore
		layout.target = target
	default:
		return layout, fmt.Errorf("%w: canonical target matches neither durable state", ErrWriteTransactionJournalCorrupt)
	}

	for _, stagePath := range writeTransactionStagePaths(plan.Stages) {
		stage, err := inspectWriteTransactionObject(ctx, fs.internalRootHandle, stagePath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return layout, fmt.Errorf("inspect write stage %s: %w", stagePath, err)
		}
		stageClass := writeTransactionObjectAbsent
		if stage.matchesExpectation(plan.Target.After) {
			stageClass = writeTransactionObjectAfter
		}
		if plan.OldTarget != nil && stage.matchesExpectation(*plan.OldTarget) {
			if stageClass != writeTransactionObjectAbsent {
				return layout, fmt.Errorf("%w: stage matches both old and new content", ErrWriteTransactionJournalCorrupt)
			}
			stageClass = writeTransactionObjectBefore
		}
		if stageClass == writeTransactionObjectAbsent {
			return layout, fmt.Errorf("%w: stage %s contains an unknown object", ErrWriteTransactionJournalCorrupt, stagePath)
		}
		if !writeTransactionStageAllowsObject(plan, stagePath, stageClass) {
			return layout, fmt.Errorf(
				"%w: stage %s contains an object in an invalid role",
				ErrWriteTransactionJournalCorrupt,
				stagePath,
			)
		}
		if layout.stage != nil {
			return layout, fmt.Errorf("%w: multiple write transaction stages are populated", ErrWriteTransactionJournalCorrupt)
		}
		layout.stage = stage
		layout.stageClass = stageClass
	}

	switch plan.Kind {
	case WriteTransactionKindCreate:
		if layout.stageClass == writeTransactionObjectBefore ||
			(layout.targetClass == writeTransactionObjectAfter && layout.stage != nil) ||
			(layout.targetClass == writeTransactionObjectBefore) {
			return layout, fmt.Errorf("%w: unknown create layout", ErrWriteTransactionJournalCorrupt)
		}
	case WriteTransactionKindOverwrite:
		switch layout.targetClass {
		case writeTransactionObjectBefore:
			if layout.stage != nil && layout.stageClass != writeTransactionObjectAfter {
				return layout, fmt.Errorf("%w: overwrite before-layout has an old stage", ErrWriteTransactionJournalCorrupt)
			}
		case writeTransactionObjectAfter:
			if layout.stage != nil && layout.stageClass != writeTransactionObjectBefore {
				return layout, fmt.Errorf("%w: overwrite after-layout has a new stage", ErrWriteTransactionJournalCorrupt)
			}
		default:
			return layout, fmt.Errorf("%w: overwrite target is absent", ErrWriteTransactionJournalCorrupt)
		}
	default:
		return layout, ErrWriteTransactionJournalCorrupt
	}
	return layout, nil
}

func writeTransactionStageAllowsObject(
	plan WriteTransactionPlan,
	stagePath string,
	class writeTransactionObjectClass,
) bool {
	switch plan.Kind {
	case WriteTransactionKindCreate:
		return class == writeTransactionObjectAfter &&
			(stagePath == plan.Stages.Source ||
				stagePath == plan.Stages.Published ||
				stagePath == plan.Stages.Recovery)
	case WriteTransactionKindOverwrite:
		switch class {
		case writeTransactionObjectAfter:
			return stagePath == plan.Stages.Source ||
				stagePath == plan.Stages.Published ||
				stagePath == plan.Stages.Recovery
		case writeTransactionObjectBefore:
			return stagePath == plan.Stages.Source ||
				stagePath == plan.Stages.Captured ||
				stagePath == plan.Stages.Committed ||
				stagePath == plan.Stages.Recovery
		}
	}
	return false
}

func (fs *FileSystem) ensureWriteTransactionPhysicalBefore(
	ctx context.Context,
	journal *WriteTransactionJournal,
	plan WriteTransactionPlan,
	layout writeTransactionPhysicalLayout,
) (writeTransactionPhysicalLayout, error) {
	switch plan.Kind {
	case WriteTransactionKindCreate:
		if layout.targetClass == writeTransactionObjectAfter {
			if layout.stage != nil {
				return layout, ErrWriteTransactionJournalCorrupt
			}
			stagePath, err := selectWriteTransactionRollbackStage(plan)
			if err != nil {
				return layout, err
			}
			if err := fs.renameWriteTransactionObjectNoReplaceWithPriority(
				ctx,
				journal,
				plan,
				fs.filesRootHandle,
				plan.Target.RelativePath,
				fs.internalRootHandle,
				stagePath,
				plan.Target.After,
				writeTransactionSyncSourceFirst,
			); err != nil {
				return layout, err
			}
		}
	case WriteTransactionKindOverwrite:
		if layout.targetClass == writeTransactionObjectAfter {
			if layout.stage == nil || layout.stageClass != writeTransactionObjectBefore ||
				plan.OldTarget == nil {
				return layout, fmt.Errorf("%w: old content stage is unavailable", ErrWriteTransactionJournalCorrupt)
			}
			if err := fs.exchangeWriteTransactionObjectsWithPriority(
				ctx,
				journal,
				plan,
				layout.stage.path,
				plan.Target.RelativePath,
				*plan.OldTarget,
				plan.Target.After,
				writeTransactionSyncTargetFirst,
			); err != nil {
				return layout, err
			}
		}
	default:
		return layout, ErrWriteTransactionJournalCorrupt
	}
	updated, err := fs.inspectWriteTransactionPhysicalLayout(ctx, plan)
	if err != nil {
		return updated, err
	}
	if updated.targetClass != writeTransactionObjectAbsent &&
		updated.targetClass != writeTransactionObjectBefore {
		return updated, fmt.Errorf("%w: physical rollback did not restore before-state", ErrWriteTransactionJournalCorrupt)
	}
	return fs.reaffirmWriteTransactionPhysicalLayout(
		ctx,
		journal,
		plan,
		updated,
		plan.Kind == WriteTransactionKindCreate,
	)
}

func (fs *FileSystem) ensureWriteTransactionPhysicalAfter(
	ctx context.Context,
	journal *WriteTransactionJournal,
	plan WriteTransactionPlan,
	layout writeTransactionPhysicalLayout,
) (writeTransactionPhysicalLayout, error) {
	switch plan.Kind {
	case WriteTransactionKindCreate:
		if layout.targetClass == writeTransactionObjectAbsent {
			if layout.stage == nil || layout.stageClass != writeTransactionObjectAfter {
				return layout, fmt.Errorf("%w: new content stage is unavailable", ErrWriteTransactionJournalCorrupt)
			}
			if err := fs.renameWriteTransactionObjectNoReplace(
				ctx,
				journal,
				plan,
				fs.internalRootHandle,
				layout.stage.path,
				fs.filesRootHandle,
				plan.Target.RelativePath,
				plan.Target.After,
			); err != nil {
				return layout, err
			}
		}
	case WriteTransactionKindOverwrite:
		if layout.targetClass == writeTransactionObjectBefore {
			if layout.stage == nil || layout.stageClass != writeTransactionObjectAfter ||
				plan.OldTarget == nil {
				return layout, fmt.Errorf("%w: prepared exchange stage is unavailable", ErrWriteTransactionJournalCorrupt)
			}
			if err := fs.exchangeWriteTransactionObjectsWithPriority(
				ctx,
				journal,
				plan,
				layout.stage.path,
				plan.Target.RelativePath,
				plan.Target.After,
				*plan.OldTarget,
				writeTransactionSyncTargetFirst,
			); err != nil {
				return layout, err
			}
		}
	default:
		return layout, ErrWriteTransactionJournalCorrupt
	}
	updated, err := fs.inspectWriteTransactionPhysicalLayout(ctx, plan)
	if err != nil {
		return updated, err
	}
	if updated.targetClass != writeTransactionObjectAfter {
		return updated, fmt.Errorf("%w: physical roll-forward did not restore after-state", ErrWriteTransactionJournalCorrupt)
	}
	return fs.reaffirmWriteTransactionPhysicalLayout(ctx, journal, plan, updated, false)
}

func (fs *FileSystem) reaffirmWriteTransactionPhysicalLayout(
	ctx context.Context,
	journal *WriteTransactionJournal,
	plan WriteTransactionPlan,
	layout writeTransactionPhysicalLayout,
	stagingFirst bool,
) (writeTransactionPhysicalLayout, error) {
	if err := fs.validateWriteTransactionRecoveryRoots(journal, plan); err != nil {
		return layout, err
	}
	syncStaging := func() error {
		if err := syncWriteTransactionDirectory(fs.internalRootHandle, writeStagingDir); err != nil {
			return fmt.Errorf("sync write transaction staging directory: %w", err)
		}
		return nil
	}
	if stagingFirst {
		if err := syncStaging(); err != nil {
			return layout, err
		}
		if err := fs.syncWriteTransactionTargetNamespace(plan); err != nil {
			return layout, err
		}
	} else {
		if err := fs.syncWriteTransactionTargetNamespace(plan); err != nil {
			return layout, err
		}
		if err := syncStaging(); err != nil {
			return layout, err
		}
	}
	if err := fs.validateWriteTransactionRecoveryRoots(journal, plan); err != nil {
		return layout, err
	}
	verified, err := fs.inspectWriteTransactionPhysicalLayout(ctx, plan)
	if err != nil {
		return verified, err
	}
	if verified.targetClass != layout.targetClass ||
		verified.stageClass != layout.stageClass ||
		(verified.stage == nil) != (layout.stage == nil) {
		return verified, ErrWriteTransactionJournalCorrupt
	}
	return verified, nil
}

func (fs *FileSystem) renameWriteTransactionObjectNoReplace(
	ctx context.Context,
	journal *WriteTransactionJournal,
	plan WriteTransactionPlan,
	sourceRoot *os.Root,
	sourcePath string,
	targetRoot *os.Root,
	targetPath string,
	expectation WriteTransactionObjectExpectation,
) error {
	return fs.renameWriteTransactionObjectNoReplaceWithPriority(
		ctx,
		journal,
		plan,
		sourceRoot,
		sourcePath,
		targetRoot,
		targetPath,
		expectation,
		writeTransactionSyncTargetFirst,
	)
}

func (fs *FileSystem) renameWriteTransactionObjectNoReplaceWithPriority(
	ctx context.Context,
	journal *WriteTransactionJournal,
	plan WriteTransactionPlan,
	sourceRoot *os.Root,
	sourcePath string,
	targetRoot *os.Root,
	targetPath string,
	expectation WriteTransactionObjectExpectation,
	priority writeTransactionNamespaceSyncPriority,
) error {
	if priority != writeTransactionSyncSourceFirst &&
		priority != writeTransactionSyncTargetFirst {
		return ErrWriteTransactionJournalCorrupt
	}
	if err := fs.validateWriteTransactionRecoveryRoots(journal, plan); err != nil {
		return err
	}
	if sourceRoot == fs.filesRootHandle || targetRoot == fs.filesRootHandle {
		if err := fs.validateWriteTransactionTargetParent(plan); err != nil {
			return err
		}
	}
	source, err := inspectWriteTransactionObject(ctx, sourceRoot, sourcePath)
	if err != nil || !source.matchesExpectation(expectation) {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	if _, err := inspectWriteTransactionObject(ctx, targetRoot, targetPath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}
		return errors.Join(ErrWriteTransactionJournalConflict, err)
	}
	if err := callWriteTransactionRecoveryFaultHook("namespace:before-rename"); err != nil {
		return err
	}
	if err := rootio.RenameLeafBetweenRootsNoReplace(sourceRoot, sourcePath, targetRoot, targetPath); err != nil {
		return fmt.Errorf("rename write transaction object: %w", err)
	}
	if err := callWriteTransactionRecoveryFaultHook("namespace:after-rename"); err != nil {
		return err
	}
	if err := fs.syncWriteTransactionRenameParents(
		sourceRoot,
		sourcePath,
		targetRoot,
		targetPath,
		priority,
	); err != nil {
		return err
	}
	if err := fs.validateWriteTransactionRecoveryRoots(journal, plan); err != nil {
		return err
	}
	target, err := inspectWriteTransactionObject(ctx, targetRoot, targetPath)
	if err != nil || !target.matchesExpectation(expectation) {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	if _, err := inspectWriteTransactionObject(ctx, sourceRoot, sourcePath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	return nil
}

func (fs *FileSystem) exchangeWriteTransactionObjects(
	ctx context.Context,
	journal *WriteTransactionJournal,
	plan WriteTransactionPlan,
	stagePath string,
	targetPath string,
	stageExpectation WriteTransactionObjectExpectation,
	targetExpectation WriteTransactionObjectExpectation,
) error {
	return fs.exchangeWriteTransactionObjectsWithPriority(
		ctx,
		journal,
		plan,
		stagePath,
		targetPath,
		stageExpectation,
		targetExpectation,
		writeTransactionSyncSourceFirst,
	)
}

func (fs *FileSystem) exchangeWriteTransactionObjectsWithPriority(
	ctx context.Context,
	journal *WriteTransactionJournal,
	plan WriteTransactionPlan,
	stagePath string,
	targetPath string,
	stageExpectation WriteTransactionObjectExpectation,
	targetExpectation WriteTransactionObjectExpectation,
	priority writeTransactionNamespaceSyncPriority,
) error {
	if priority != writeTransactionSyncSourceFirst &&
		priority != writeTransactionSyncTargetFirst {
		return ErrWriteTransactionJournalCorrupt
	}
	if err := fs.validateWriteTransactionRecoveryRoots(journal, plan); err != nil {
		return err
	}
	if err := fs.validateWriteTransactionTargetParent(plan); err != nil {
		return err
	}
	stage, stageErr := inspectWriteTransactionObject(ctx, fs.internalRootHandle, stagePath)
	target, targetErr := inspectWriteTransactionObject(ctx, fs.filesRootHandle, targetPath)
	if stageErr != nil || targetErr != nil ||
		!stage.matchesExpectation(stageExpectation) ||
		!target.matchesExpectation(targetExpectation) {
		return errors.Join(ErrWriteTransactionJournalCorrupt, stageErr, targetErr)
	}
	if err := callWriteTransactionRecoveryFaultHook("namespace:before-exchange"); err != nil {
		return err
	}
	if err := rootio.ExchangeLeavesBetweenRoots(
		fs.internalRootHandle,
		stagePath,
		fs.filesRootHandle,
		targetPath,
	); err != nil {
		return fmt.Errorf("exchange write transaction objects: %w", err)
	}
	if err := callWriteTransactionRecoveryFaultHook("namespace:after-exchange"); err != nil {
		return err
	}
	if err := fs.syncWriteTransactionRenameParents(
		fs.internalRootHandle,
		stagePath,
		fs.filesRootHandle,
		targetPath,
		priority,
	); err != nil {
		return err
	}
	if err := fs.validateWriteTransactionRecoveryRoots(journal, plan); err != nil {
		return err
	}
	stage, stageErr = inspectWriteTransactionObject(ctx, fs.internalRootHandle, stagePath)
	target, targetErr = inspectWriteTransactionObject(ctx, fs.filesRootHandle, targetPath)
	if stageErr != nil || targetErr != nil ||
		!stage.matchesExpectation(targetExpectation) ||
		!target.matchesExpectation(stageExpectation) {
		return errors.Join(ErrWriteTransactionJournalCorrupt, stageErr, targetErr)
	}
	return nil
}

func (fs *FileSystem) ensureWriteTransactionCAS(
	ctx context.Context,
	store writeTransactionRecoveryStore,
	operation WriteTransactionOperation,
	layout writeTransactionPhysicalLayout,
) error {
	plan := operation.Record.Plan
	if !plan.CAS.Enabled {
		return nil
	}
	if operation.Record.Outcome == nil || !operation.Record.Outcome.CAS.Enabled {
		return fmt.Errorf("%w: committed CAS outcome is unavailable", ErrWriteTransactionJournalCorrupt)
	}
	data, err := store.GetObject(ctx, plan.CAS.Hash)
	if err == nil {
		return verifyWriteTransactionCASData(data, plan.CAS)
	}
	if !errors.Is(err, versionstore.ErrNotFound) {
		return fmt.Errorf("read committed CAS object: %w", err)
	}
	if layout.stage == nil || layout.stageClass != writeTransactionObjectBefore ||
		plan.OldTarget == nil {
		return fmt.Errorf("%w: missing CAS object has no verified old-content stage", ErrWriteTransactionJournalCorrupt)
	}
	data, err = readWriteTransactionObject(
		ctx,
		fs.internalRootHandle,
		layout.stage.path,
		*plan.OldTarget,
	)
	if err != nil {
		return fmt.Errorf("read old content for CAS recovery: %w", err)
	}
	if err := verifyWriteTransactionCASData(data, plan.CAS); err != nil {
		return err
	}
	put, err := store.PutObjectExpected(ctx, data, plan.CAS.Hash)
	if err != nil {
		return fmt.Errorf("restore committed CAS object: %w", err)
	}
	if put.Hash != plan.CAS.Hash || put.Size != plan.CAS.Size {
		return fmt.Errorf("%w: restored CAS result does not match plan", ErrWriteTransactionJournalCorrupt)
	}
	data, err = store.GetObject(ctx, plan.CAS.Hash)
	if err != nil {
		return fmt.Errorf("verify restored CAS object: %w", err)
	}
	return verifyWriteTransactionCASData(data, plan.CAS)
}

func (fs *FileSystem) rollbackWriteTransactionCAS(
	ctx context.Context,
	store writeTransactionRecoveryStore,
	operation WriteTransactionOperation,
	metadataState versionstore.WriteMetadataState,
) error {
	plan := operation.Record.Plan
	if !plan.CAS.Enabled || operation.Record.Outcome == nil ||
		!operation.Record.Outcome.CAS.CreatedByOperation {
		return nil
	}
	if metadataState != versionstore.WriteMetadataStateBefore &&
		metadataState != versionstore.WriteMetadataStateBoth {
		return versionstore.ErrWriteMetadataConflict
	}
	referenced, err := store.HasVersionReference(ctx, plan.CAS.Hash)
	if err != nil {
		return fmt.Errorf("inspect CAS references before rollback cleanup: %w", err)
	}
	if referenced {
		return nil
	}
	if err := store.DeleteObject(ctx, plan.CAS.Hash); err != nil &&
		!errors.Is(err, versionstore.ErrNotFound) {
		return fmt.Errorf("delete operation-created CAS object: %w", err)
	}
	return nil
}

func verifyWriteTransactionCASData(data []byte, plan WriteTransactionCASPlan) error {
	if int64(len(data)) != plan.Size || computeHash(data) != plan.Hash {
		return fmt.Errorf("%w: CAS object content does not match plan", ErrWriteTransactionJournalCorrupt)
	}
	return nil
}

func (fs *FileSystem) removeWriteTransactionStage(
	ctx context.Context,
	journal *WriteTransactionJournal,
	plan WriteTransactionPlan,
	stage writeTransactionObservedObject,
	expectation WriteTransactionObjectExpectation,
) error {
	if err := fs.validateWriteTransactionRecoveryRoots(journal, plan); err != nil {
		return err
	}
	current, err := inspectWriteTransactionObject(ctx, fs.internalRootHandle, stage.path)
	if err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	if !current.matchesExpectation(expectation) {
		return ErrWriteTransactionJournalCorrupt
	}
	if err := callWriteTransactionRecoveryFaultHook("remove-stage:" + stage.path); err != nil {
		return err
	}
	parent, err := rootio.OpenDirNoFollow(fs.internalRootHandle, filepath.Dir(stage.path))
	if err != nil {
		return err
	}
	verifyEntry := func(path string, actual os.FileInfo) error {
		if filepath.Clean(path) != filepath.Base(stage.path) ||
			!sameDeleteStageEntry(current.info, current.deleteIdentity, actual) {
			return errWriteStageChanged
		}
		return nil
	}
	verifyFile := func(_ string, file *os.File, info os.FileInfo) error {
		if info == nil || info.Size() != current.size {
			return errWriteStageChanged
		}
		before, err := file.Stat()
		if err != nil || !sameStableWriteTargetHandle(info, before) {
			return errors.Join(errWriteStageChanged, err)
		}
		actualHash, hashErr := hashOpenWorkspaceFileContext(ctx, file)
		after, statErr := file.Stat()
		if hashErr != nil || statErr != nil ||
			!sameStableWriteTargetHandle(before, after) ||
			actualHash != current.hash {
			return errors.Join(errWriteStageChanged, hashErr, statErr)
		}
		return nil
	}
	removeErr := rootio.RemoveAllFromDirNoFollowCheckedInPlaceWithRegularFile(
		parent,
		filepath.Base(stage.path),
		verifyEntry,
		verifyFile,
	)
	var faultErr error
	if removeErr == nil {
		faultErr = callWriteTransactionRecoveryFaultHook("remove-stage:after-unlink:" + stage.path)
	}
	var syncErr error
	if faultErr == nil {
		syncErr = parent.Sync()
	}
	closeErr := parent.Close()
	if removeErr != nil || faultErr != nil || syncErr != nil || closeErr != nil {
		return fmt.Errorf(
			"remove verified write transaction stage: %w",
			errors.Join(removeErr, faultErr, syncErr, closeErr),
		)
	}
	if err := fs.validateWriteTransactionRecoveryRoots(journal, plan); err != nil {
		return err
	}
	if _, err := inspectWriteTransactionObject(ctx, fs.internalRootHandle, stage.path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	return nil
}

func (fs *FileSystem) cleanupWriteTransactionCreatedDirectories(
	journal *WriteTransactionJournal,
	plan WriteTransactionPlan,
) error {
	for _, directory := range plan.CreatedDirectories {
		if err := fs.validateWriteTransactionCreatedDirectoryBase(plan); err != nil {
			return err
		}
		if err := fs.validateWriteTransactionRecoveryRoots(journal, plan); err != nil {
			return err
		}
		rooted, err := fs.filesRootHandle.Lstat(directory.RelativePath)
		if errors.Is(err, os.ErrNotExist) {
			if err := fs.syncWriteTransactionCreatedDirectoryAbsence(
				plan,
				fs.filesRootHandle,
				directory.RelativePath,
			); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		dir, err := rootio.OpenDirNoFollow(fs.filesRootHandle, directory.RelativePath)
		if err != nil {
			return err
		}
		held, statErr := dir.Stat()
		entries, readErr := dir.ReadDir(1)
		closeErr := dir.Close()
		if statErr != nil || readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil ||
			!rooted.IsDir() || !held.IsDir() || !os.SameFile(rooted, held) ||
			workspace.PersistentIdentityTokenForFileInfo(held) != directory.PersistentIdentity ||
			uint32(held.Mode()) != directory.Mode ||
			len(entries) != 0 {
			return errors.Join(ErrWriteTransactionJournalCorrupt, statErr, readErr, closeErr)
		}

		parentRel := filepath.Dir(directory.RelativePath)
		parent, err := rootio.OpenDirNoFollow(fs.filesRootHandle, parentRel)
		if err != nil {
			return err
		}
		base := filepath.Base(directory.RelativePath)
		verify := func(path string, current os.FileInfo) error {
			if filepath.Clean(path) != base ||
				current == nil || !current.IsDir() ||
				workspace.PersistentIdentityTokenForFileInfo(current) != directory.PersistentIdentity ||
				uint32(current.Mode()) != directory.Mode {
				return ErrWriteTransactionJournalCorrupt
			}
			return nil
		}
		removeErr := rootio.RemoveEmptyDirNoFollowCheckedInPlace(parent, base, verify)
		var faultErr error
		if removeErr == nil {
			faultErr = callWriteTransactionRecoveryFaultHook(
				"remove-created-directory:after-unlink:" + directory.RelativePath,
			)
		}
		var syncErr error
		if faultErr == nil {
			syncErr = parent.Sync()
		}
		closeErr = parent.Close()
		if removeErr != nil || faultErr != nil || syncErr != nil || closeErr != nil {
			return errors.Join(removeErr, faultErr, syncErr, closeErr)
		}
		if err := fs.validateWriteTransactionRecoveryRoots(journal, plan); err != nil {
			return err
		}
		if _, err := fs.filesRootHandle.Lstat(directory.RelativePath); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				err = os.ErrExist
			}
			return errors.Join(ErrWriteTransactionJournalCorrupt, err)
		}
	}
	return nil
}

func (fs *FileSystem) validateWriteTransactionRecoveryInfrastructure(
	journal *WriteTransactionJournal,
) error {
	if fs == nil || journal == nil || fs.filesRootHandle == nil || fs.internalRootHandle == nil {
		return errors.New("write transaction recovery roots are unavailable")
	}
	if fs.rootLifecycleLock != nil {
		if err := fs.rootLifecycleLock.Validate(); err != nil {
			return err
		}
	}
	internal, err := inspectWriteTransactionRootBinding(fs.internalRootHandle, ".", false)
	if err != nil {
		return err
	}
	staging, err := inspectWriteTransactionRootBinding(fs.internalRootHandle, writeStagingDir, true)
	if err != nil {
		return err
	}
	journalRoot, err := inspectWriteTransactionRootBinding(
		fs.internalRootHandle,
		writeTransactionJournalDir,
		true,
	)
	if err != nil {
		return err
	}
	if internal != journal.InternalRootBinding() ||
		journalRoot != journal.JournalRootBinding() ||
		staging.Mode != uint32(os.ModeDir|0o700) {
		return ErrWriteTransactionJournalCorrupt
	}
	return nil
}

func (fs *FileSystem) validateWriteTransactionRecoveryRoots(
	journal *WriteTransactionJournal,
	plan WriteTransactionPlan,
) error {
	if err := fs.validateWriteTransactionRecoveryInfrastructure(journal); err != nil {
		return err
	}
	files, err := inspectWriteTransactionRootBinding(fs.filesRootHandle, ".", false)
	if err != nil {
		return err
	}
	internal, err := inspectWriteTransactionRootBinding(fs.internalRootHandle, ".", false)
	if err != nil {
		return err
	}
	staging, err := inspectWriteTransactionRootBinding(fs.internalRootHandle, writeStagingDir, true)
	if err != nil {
		return err
	}
	journalRoot, err := inspectWriteTransactionRootBinding(
		fs.internalRootHandle,
		writeTransactionJournalDir,
		true,
	)
	if err != nil {
		return err
	}
	if files != plan.Roots.Files ||
		internal != plan.Roots.Internal ||
		staging != plan.Roots.Staging ||
		journalRoot != plan.Roots.Journal {
		return fmt.Errorf("%w: write transaction root binding changed", ErrWriteTransactionJournalCorrupt)
	}
	return nil
}

func inspectWriteTransactionRootBinding(
	root *os.Root,
	relativePath string,
	private bool,
) (WriteTransactionRootBinding, error) {
	if root == nil {
		return WriteTransactionRootBinding{}, errors.New("write transaction root is unavailable")
	}
	rooted, err := root.Lstat(relativePath)
	if err != nil {
		return WriteTransactionRootBinding{}, err
	}
	dir, err := rootio.OpenDirNoFollow(root, relativePath)
	if err != nil {
		return WriteTransactionRootBinding{}, err
	}
	held, statErr := dir.Stat()
	rootedAfter, rootedAfterErr := root.Lstat(relativePath)
	closeErr := dir.Close()
	if statErr != nil || rootedAfterErr != nil || closeErr != nil ||
		!rooted.IsDir() || !held.IsDir() || !rootedAfter.IsDir() ||
		!os.SameFile(rooted, held) || !os.SameFile(held, rootedAfter) {
		return WriteTransactionRootBinding{}, errors.Join(
			ErrWriteTransactionJournalCorrupt,
			statErr,
			rootedAfterErr,
			closeErr,
		)
	}
	if private && (rooted.Mode() != os.ModeDir|0o700 ||
		held.Mode() != os.ModeDir|0o700 ||
		rootedAfter.Mode() != os.ModeDir|0o700) {
		return WriteTransactionRootBinding{}, ErrWriteTransactionJournalCorrupt
	}
	rootedBinding, err := writeTransactionRootBindingFromInfo(rooted)
	if err != nil {
		return WriteTransactionRootBinding{}, err
	}
	heldBinding, err := writeTransactionRootBindingFromInfo(held)
	if err != nil {
		return WriteTransactionRootBinding{}, err
	}
	if rootedBinding != heldBinding {
		return WriteTransactionRootBinding{}, ErrWriteTransactionJournalCorrupt
	}
	rootedAfterBinding, err := writeTransactionRootBindingFromInfo(rootedAfter)
	if err != nil {
		return WriteTransactionRootBinding{}, err
	}
	if heldBinding != rootedAfterBinding {
		return WriteTransactionRootBinding{}, ErrWriteTransactionJournalCorrupt
	}
	return heldBinding, nil
}

func (fs *FileSystem) validateWriteTransactionTargetParent(plan WriteTransactionPlan) error {
	parent, err := rootio.OpenDirNoFollow(fs.filesRootHandle, plan.Target.ParentRelativePath)
	if err != nil {
		return err
	}
	held, statErr := parent.Stat()
	rooted, rootedErr := fs.filesRootHandle.Lstat(plan.Target.ParentRelativePath)
	closeErr := parent.Close()
	if statErr != nil || rootedErr != nil || closeErr != nil ||
		!held.IsDir() || !rooted.IsDir() || !os.SameFile(held, rooted) ||
		workspace.PersistentIdentityTokenForFileInfo(held) != plan.Target.ParentPersistentIdentity ||
		uint32(held.Mode()) != plan.Target.ParentMode {
		return errors.Join(
			ErrWriteTransactionJournalCorrupt,
			statErr,
			rootedErr,
			closeErr,
		)
	}
	return nil
}

func (fs *FileSystem) validateWriteTransactionTargetParentForRecovery(
	plan WriteTransactionPlan,
	decision WriteTransactionDecision,
	layout writeTransactionPhysicalLayout,
) error {
	err := fs.validateWriteTransactionTargetParent(plan)
	if err == nil {
		return nil
	}
	if decision == WriteTransactionDecisionRollback &&
		plan.Kind == WriteTransactionKindCreate &&
		layout.targetClass == writeTransactionObjectAbsent &&
		layout.stage == nil &&
		writeTransactionPlanOwnsTargetParent(plan) &&
		errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func writeTransactionPlanOwnsTargetParent(plan WriteTransactionPlan) bool {
	return len(plan.CreatedDirectories) != 0 &&
		plan.CreatedDirectories[0].RelativePath == plan.Target.ParentRelativePath &&
		plan.CreatedDirectories[0].PersistentIdentity == plan.Target.ParentPersistentIdentity
}

func (fs *FileSystem) validateWriteTransactionCreatedDirectoryBase(
	plan WriteTransactionPlan,
) error {
	if len(plan.CreatedDirectories) == 0 {
		if plan.CreatedDirectoryBase != nil {
			return ErrWriteTransactionJournalCorrupt
		}
		return nil
	}
	base := plan.CreatedDirectoryBase
	if base == nil {
		return ErrWriteTransactionJournalCorrupt
	}
	binding, err := inspectWriteTransactionRootBinding(
		fs.filesRootHandle,
		base.RelativePath,
		false,
	)
	if err != nil {
		return err
	}
	if binding.PersistentIdentity != base.PersistentIdentity ||
		binding.Mode != base.Mode {
		return fmt.Errorf(
			"%w: created-directory base binding changed",
			ErrWriteTransactionJournalCorrupt,
		)
	}
	return nil
}

func (fs *FileSystem) syncWriteTransactionTargetNamespace(plan WriteTransactionPlan) error {
	if err := fs.validateWriteTransactionTargetParent(plan); err == nil {
		if err := syncWriteTransactionDirectory(
			fs.filesRootHandle,
			plan.Target.ParentRelativePath,
		); err != nil {
			return fmt.Errorf("sync write transaction target parent: %w", err)
		}
		return nil
	} else if !writeTransactionPlanOwnsTargetParent(plan) || !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := fs.syncWriteTransactionCreatedDirectoryAbsence(
		plan,
		fs.filesRootHandle,
		plan.Target.ParentRelativePath,
	); err != nil {
		return fmt.Errorf("sync absent write transaction target parent: %w", err)
	}
	return nil
}

func (fs *FileSystem) syncWriteTransactionCreatedDirectoryAbsence(
	plan WriteTransactionPlan,
	root *os.Root,
	relativePath string,
) error {
	if root == nil {
		return errors.New("write transaction root is unavailable")
	}
	owned := make(
		map[string]WriteTransactionCreatedDirectory,
		len(plan.CreatedDirectories),
	)
	for _, directory := range plan.CreatedDirectories {
		owned[directory.RelativePath] = directory
	}
	for current := filepath.Clean(relativePath); ; current = filepath.Dir(current) {
		dir, err := rootio.OpenDirNoFollow(root, current)
		if err == nil {
			expectedIdentity := ""
			var expectedMode uint32
			if directory, ok := owned[current]; ok {
				expectedIdentity = directory.PersistentIdentity
				expectedMode = directory.Mode
			} else if base := plan.CreatedDirectoryBase; base != nil &&
				current == base.RelativePath {
				expectedIdentity = base.PersistentIdentity
				expectedMode = base.Mode
			} else {
				_ = dir.Close()
				return fmt.Errorf(
					"%w: write transaction ancestor %s lacks exact authority",
					ErrWriteTransactionJournalCorrupt,
					current,
				)
			}
			rootedBefore, rootedBeforeErr := root.Lstat(current)
			held, statErr := dir.Stat()
			syncErr := dir.Sync()
			rootedAfter, rootedAfterErr := root.Lstat(current)
			closeErr := dir.Close()
			if rootedBeforeErr != nil || statErr != nil || syncErr != nil ||
				rootedAfterErr != nil || closeErr != nil ||
				!rootedBefore.IsDir() || !held.IsDir() || !rootedAfter.IsDir() ||
				!os.SameFile(rootedBefore, held) || !os.SameFile(held, rootedAfter) ||
				workspace.PersistentIdentityTokenForFileInfo(held) != expectedIdentity ||
				uint32(held.Mode()) != expectedMode {
				return errors.Join(
					ErrWriteTransactionJournalCorrupt,
					rootedBeforeErr,
					statErr,
					syncErr,
					rootedAfterErr,
					closeErr,
				)
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, ok := owned[current]; !ok {
			return errors.Join(
				ErrWriteTransactionJournalCorrupt,
				fmt.Errorf("unowned write transaction ancestor %s is absent: %w", current, err),
			)
		}
	}
}

func (fs *FileSystem) validateWriteTransactionFinalState(
	ctx context.Context,
	journal *WriteTransactionJournal,
	store writeTransactionRecoveryStore,
	operation WriteTransactionOperation,
	decision WriteTransactionDecision,
) error {
	plan := operation.Record.Plan
	if err := fs.validateWriteTransactionRecoveryRoots(journal, plan); err != nil {
		return err
	}
	if err := fs.syncWriteTransactionTargetNamespace(plan); err != nil {
		return err
	}
	if err := syncWriteTransactionDirectory(fs.internalRootHandle, writeStagingDir); err != nil {
		return fmt.Errorf("sync write transaction staging before journal cleanup: %w", err)
	}

	layout, err := fs.inspectWriteTransactionPhysicalLayout(ctx, plan)
	if err != nil {
		return err
	}
	if layout.stage != nil {
		return fmt.Errorf("%w: write transaction stage remains before journal cleanup", ErrWriteTransactionJournalCorrupt)
	}
	switch decision {
	case WriteTransactionDecisionRollback:
		if plan.Kind == WriteTransactionKindCreate {
			if layout.targetClass != writeTransactionObjectAbsent {
				return fmt.Errorf("%w: create target remains after rollback", ErrWriteTransactionJournalCorrupt)
			}
			for _, directory := range plan.CreatedDirectories {
				if _, err := fs.filesRootHandle.Lstat(directory.RelativePath); !errors.Is(err, os.ErrNotExist) {
					if err == nil {
						err = os.ErrExist
					}
					return errors.Join(ErrWriteTransactionJournalCorrupt, err)
				}
			}
		} else if layout.targetClass != writeTransactionObjectBefore {
			return fmt.Errorf("%w: overwrite target is not in before-state", ErrWriteTransactionJournalCorrupt)
		}
	case WriteTransactionDecisionRollforward:
		if layout.targetClass != writeTransactionObjectAfter {
			return fmt.Errorf("%w: target is not in after-state", ErrWriteTransactionJournalCorrupt)
		}
	default:
		return ErrWriteTransactionJournalCorrupt
	}

	metadataState, err := store.InspectWriteMetadata(ctx, plan.Metadata)
	if err != nil {
		return fmt.Errorf("inspect final write metadata: %w", err)
	}
	expectedMetadata := versionstore.WriteMetadataStateBefore
	if decision == WriteTransactionDecisionRollforward {
		expectedMetadata = versionstore.WriteMetadataStateAfter
	}
	if metadataState != expectedMetadata && metadataState != versionstore.WriteMetadataStateBoth {
		return versionstore.ErrWriteMetadataConflict
	}
	if decision == WriteTransactionDecisionRollforward && plan.CAS.Enabled {
		data, err := store.GetObject(ctx, plan.CAS.Hash)
		if err != nil {
			return fmt.Errorf("inspect final write CAS object: %w", err)
		}
		if err := verifyWriteTransactionCASData(data, plan.CAS); err != nil {
			return err
		}
	}
	return fs.validateWriteTransactionRecoveryRoots(journal, plan)
}

func inspectWriteTransactionObject(
	ctx context.Context,
	root *os.Root,
	relativePath string,
) (*writeTransactionObservedObject, error) {
	if root == nil {
		return nil, errors.New("write transaction object root is unavailable")
	}
	if _, err := root.Lstat(relativePath); err != nil {
		return nil, err
	}
	file, err := rootio.OpenRegularFileNoFollow(root, relativePath)
	if err != nil {
		return nil, err
	}
	before, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	persistentIdentity := workspace.PersistentIdentityTokenForFileInfo(before)
	deleteIdentity := workspace.DeleteIdentityTokenForFileInfo(before)
	if persistentIdentity == "" || deleteIdentity == "" {
		_ = file.Close()
		return nil, ErrWriteTransactionJournalCorrupt
	}
	hash, hashErr := hashOpenWorkspaceFileContext(ctx, file)
	after, statErr := file.Stat()
	closeErr := file.Close()
	current, currentErr := rootio.OpenRegularFileNoFollow(root, relativePath)
	var currentInfo os.FileInfo
	var currentCloseErr error
	if currentErr == nil {
		currentInfo, currentErr = current.Stat()
		currentCloseErr = current.Close()
	}
	if hashErr != nil || statErr != nil || closeErr != nil || currentErr != nil ||
		currentCloseErr != nil ||
		!sameStableWriteTargetHandle(before, after) ||
		!sameStableWriteTargetHandle(after, currentInfo) {
		return nil, errors.Join(
			ErrWriteTransactionJournalCorrupt,
			hashErr,
			statErr,
			closeErr,
			currentErr,
			currentCloseErr,
		)
	}
	return &writeTransactionObservedObject{
		path:               relativePath,
		info:               currentInfo,
		persistentIdentity: persistentIdentity,
		deleteIdentity:     deleteIdentity,
		mode:               currentInfo.Mode(),
		size:               currentInfo.Size(),
		modTimeUnixNano:    currentInfo.ModTime().UnixNano(),
		hash:               hash,
	}, nil
}

func readWriteTransactionObject(
	ctx context.Context,
	root *os.Root,
	relativePath string,
	expectation WriteTransactionObjectExpectation,
) ([]byte, error) {
	observed, err := inspectWriteTransactionObject(ctx, root, relativePath)
	if err != nil || !observed.matchesExpectation(expectation) {
		return nil, errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	file, err := rootio.OpenRegularFileNoFollow(root, relativePath)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(contextCheckingReader{ctx: ctx, reader: file})
	after, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil ||
		!sameStableWriteTargetHandle(observed.info, after) ||
		int64(len(data)) != expectation.Size ||
		computeHash(data) != expectation.BLAKE3 {
		return nil, errors.Join(
			ErrWriteTransactionJournalCorrupt,
			readErr,
			statErr,
			closeErr,
		)
	}
	return data, nil
}

func (observed *writeTransactionObservedObject) matchesExpectation(
	expectation WriteTransactionObjectExpectation,
) bool {
	return observed != nil &&
		observed.persistentIdentity == expectation.PersistentIdentity &&
		uint32(observed.mode) == expectation.Mode &&
		observed.size == expectation.Size &&
		observed.modTimeUnixNano == expectation.ModTimeUnixNano &&
		observed.hash == expectation.BLAKE3
}

func (observed *writeTransactionObservedObject) matchesEvidenceStable(
	evidence WriteTransactionObjectEvidence,
) bool {
	return observed != nil &&
		observed.persistentIdentity == evidence.PersistentIdentity &&
		uint32(observed.mode) == evidence.Mode &&
		observed.size == evidence.Size &&
		observed.modTimeUnixNano == evidence.ModTimeUnixNano &&
		observed.hash == evidence.BLAKE3
}

func writeTransactionStagePaths(stages WriteTransactionStagePlan) []string {
	paths := []string{
		stages.Source,
		stages.Exchange,
		stages.Captured,
		stages.Published,
		stages.Committed,
		stages.Recovery,
	}
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func selectWriteTransactionRollbackStage(plan WriteTransactionPlan) (string, error) {
	for _, candidate := range []string{
		plan.Stages.Published,
		plan.Stages.Recovery,
		plan.Stages.Source,
	} {
		if candidate != "" {
			return candidate, nil
		}
	}
	return "", ErrWriteTransactionJournalCorrupt
}

func (fs *FileSystem) syncWriteTransactionRenameParents(
	sourceRoot *os.Root,
	sourcePath string,
	targetRoot *os.Root,
	targetPath string,
	priority writeTransactionNamespaceSyncPriority,
) error {
	if sourceRoot == targetRoot && filepath.Dir(sourcePath) == filepath.Dir(targetPath) {
		return syncWriteTransactionDirectory(sourceRoot, filepath.Dir(sourcePath))
	}
	type namespaceParent struct {
		label string
		root  *os.Root
		path  string
	}
	source := namespaceParent{
		label: "source",
		root:  sourceRoot,
		path:  filepath.Dir(sourcePath),
	}
	target := namespaceParent{
		label: "target",
		root:  targetRoot,
		path:  filepath.Dir(targetPath),
	}
	first, second := source, target
	if priority == writeTransactionSyncTargetFirst {
		first, second = target, source
	} else if priority != writeTransactionSyncSourceFirst {
		return ErrWriteTransactionJournalCorrupt
	}
	syncParent := func(parent namespaceParent) error {
		if err := callWriteTransactionRecoveryFaultHook(
			"namespace:before-" + parent.label + "-parent-sync",
		); err != nil {
			return err
		}
		if err := syncWriteTransactionDirectory(parent.root, parent.path); err != nil {
			return err
		}
		return callWriteTransactionRecoveryFaultHook(
			"namespace:after-" + parent.label + "-parent-sync",
		)
	}
	if err := syncParent(first); err != nil {
		return err
	}
	return syncParent(second)
}

func syncWriteTransactionDirectory(root *os.Root, relativePath string) error {
	dir, err := rootio.OpenDirNoFollow(root, relativePath)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	return errors.Join(syncErr, closeErr)
}

func (fs *FileSystem) writeTransactionInspectionPaths(plan WriteTransactionPlan) []string {
	paths := []string{plan.Target.Path}
	internalRoot := ""
	if fs != nil && fs.config != nil {
		internalRoot = fs.config.InternalRoot
	}
	for _, stage := range writeTransactionStagePaths(plan.Stages) {
		if internalRoot == "" {
			paths = append(paths, stage)
		} else {
			paths = append(paths, filepath.Join(internalRoot, stage))
		}
	}
	for _, directory := range plan.CreatedDirectories {
		paths = append(paths, directory.Path)
	}
	return uniqueSortedStrings(paths)
}

func (fs *FileSystem) writeTransactionJournalInspectionPaths() []string {
	if fs != nil && fs.config != nil && fs.config.InternalRoot != "" {
		return []string{filepath.Join(fs.config.InternalRoot, writeTransactionJournalDir)}
	}
	return []string{writeTransactionJournalDir}
}

func callWriteTransactionRecoveryFaultHook(point string) error {
	if err := writeTransactionRecoveryFaultHook(point); err != nil {
		return fmt.Errorf("write transaction recovery fault at %s: %w", point, err)
	}
	return nil
}
