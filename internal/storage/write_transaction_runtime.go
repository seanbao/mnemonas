package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/versionstore"
	"github.com/seanbao/mnemonas/internal/workspace"
)

// writeTransactionRuntimeStore is the exact metadata/CAS surface used by the
// live coordinator and its locked recovery path.
type writeTransactionRuntimeStore interface {
	writeTransactionRecoveryStore
	CaptureWriteMetadataPlan(
		context.Context,
		versionstore.FileIndexRecord,
		*versionstore.VersionRecord,
	) (versionstore.WriteMetadataPlan, error)
}

var (
	writeTransactionRuntimeFaultHook = func(string) error { return nil }
	writeTransactionRuntimeNow       = time.Now
	writeTransactionRuntimeRandom    = rand.Read
)

var errWriteTransactionRuntimeFaultInjected = errors.New("write transaction runtime fault injected")

type writeTransactionRuntimePrepared struct {
	operationID string
	name        string
	targetRel   string
	source      *stagedWriteFile
	createdDirs []workspace.CreatedDir
	oldData     []byte
	plan        WriteTransactionPlan
}

// runWriteTransactionRuntimeLocked coordinates one already-staged source while
// the caller holds closeMu.RLock and writeStagingMu.RLock. The caller must
// capture targetSnapshot before consuming the request body. This function
// acquires only the mutation lease and must not be called with one already held.
func (fs *FileSystem) runWriteTransactionRuntimeLocked(
	ctx context.Context,
	name string,
	source *stagedWriteFile,
	options writeFileTransactionOptions,
	targetSnapshot writeTargetSnapshot,
) error {
	if fs == nil {
		return errors.New("write transaction runtime store is unavailable")
	}
	store := fs.writeTransactionStore
	if store == nil {
		store = fs.versions
	}
	if store == nil {
		return errors.New("write transaction runtime store is unavailable")
	}
	return fs.runWriteTransactionRuntimeLockedWithStore(
		ctx,
		name,
		source,
		options,
		targetSnapshot,
		store,
	)
}

func (fs *FileSystem) runWriteTransactionRuntimeLockedWithStore(
	ctx context.Context,
	name string,
	source *stagedWriteFile,
	options writeFileTransactionOptions,
	targetSnapshot writeTargetSnapshot,
	store writeTransactionRuntimeStore,
) (resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if fs == nil || store == nil {
		return errors.New("write transaction runtime is unavailable")
	}
	if fs.writeTransactionJournal == nil {
		return errors.New("write transaction journal is unavailable")
	}
	if source == nil || source.file == nil || source.rel == "" {
		return errors.New("staged write source is unavailable")
	}
	normalizedName, err := normalizeStorageWorkspacePath(name)
	if err != nil {
		return err
	}
	if err := rejectStorageRootMutation(normalizedName); err != nil {
		return err
	}

	releaseMutation, err := fs.beginMutation(ctx)
	if err != nil {
		return err
	}
	defer releaseMutation()

	prepared, err := fs.prepareWriteTransactionRuntime(
		ctx,
		normalizedName,
		source,
		options,
		targetSnapshot,
		store,
	)
	if err != nil {
		var createdDirs []workspace.CreatedDir
		if prepared != nil {
			createdDirs = prepared.createdDirs
		}
		return fs.abortWriteTransactionBeforeJournal(ctx, source, createdDirs, err)
	}
	defer func() {
		releaseErr := workspace.ReleaseCreatedDirs(prepared.createdDirs)
		if releaseErr != nil {
			resultErr = errors.Join(resultErr, releaseErr)
		}
	}()

	if err := callWriteTransactionRuntimeFaultHook("before-prepared"); err != nil {
		return fs.abortWriteTransactionBeforeJournal(
			ctx,
			source,
			prepared.createdDirs,
			err,
		)
	}
	preparedResult, preparedErr := fs.writeTransactionJournal.PublishPrepared(
		prepared.operationID,
		prepared.plan,
	)
	// From the first immutable publication attempt onward, legacy staged-file
	// defers must never perform path cleanup.
	source.retained = true
	if preparedErr != nil {
		return fs.finishWriteTransactionRuntime(ctx, store, prepared, preparedErr)
	}
	if !preparedResult.FinalObserved {
		return fs.finishWriteTransactionRuntime(
			ctx,
			store,
			prepared,
			ErrWriteTransactionJournalCorrupt,
		)
	}
	if err := callWriteTransactionRuntimeFaultHook("after-prepared"); err != nil {
		return fs.finishWriteTransactionRuntime(ctx, store, prepared, err)
	}
	if err := ctx.Err(); err != nil {
		return fs.finishWriteTransactionRuntime(ctx, store, prepared, err)
	}

	commitCtx, cancelCommit := context.WithTimeout(
		context.WithoutCancel(ctx),
		writeCommitTimeout,
	)
	defer cancelCommit()
	if err := callWriteTransactionRuntimeFaultHook("before-visible-publish"); err != nil {
		return fs.finishWriteTransactionRuntime(ctx, store, prepared, err)
	}
	publishErr := mapWriteStorageCapacityError(
		fs.publishWriteTransactionRuntime(commitCtx, prepared),
	)
	source.rel = ""
	source.trusted = false
	if closeErr := closeWriteTransactionRuntimeSource(source); closeErr != nil {
		publishErr = errors.Join(publishErr, closeErr)
	}
	if publishErr != nil {
		return fs.finishWriteTransactionRuntime(ctx, store, prepared, publishErr)
	}
	if err := callWriteTransactionRuntimeFaultHook("after-visible-publish"); err != nil {
		return fs.finishWriteTransactionRuntime(ctx, store, prepared, err)
	}

	casOutcome, err := publishWriteTransactionRuntimeCAS(
		commitCtx,
		store,
		prepared.plan.CAS,
		prepared.oldData,
	)
	if err != nil {
		return fs.finishWriteTransactionRuntime(ctx, store, prepared, err)
	}
	if err := callWriteTransactionRuntimeFaultHook("after-cas"); err != nil {
		return fs.finishWriteTransactionRuntime(ctx, store, prepared, err)
	}
	target, err := inspectWriteTransactionObject(
		commitCtx,
		fs.filesRootHandle,
		prepared.targetRel,
	)
	if err != nil {
		return fs.finishWriteTransactionRuntime(ctx, store, prepared, err)
	}
	outcome := WriteTransactionPublishedOutcome{
		Target: writeTransactionObjectEvidenceFromObserved(target, prepared.targetRel),
		CAS:    casOutcome,
	}
	publishedResult, publishedErr := fs.writeTransactionJournal.PublishPublished(
		prepared.operationID,
		outcome,
	)
	if publishedErr != nil {
		return fs.finishWriteTransactionRuntime(ctx, store, prepared, publishedErr)
	}
	if !publishedResult.FinalObserved {
		return fs.finishWriteTransactionRuntime(
			ctx,
			store,
			prepared,
			ErrWriteTransactionJournalCorrupt,
		)
	}
	if err := callWriteTransactionRuntimeFaultHook("after-published"); err != nil {
		return fs.finishWriteTransactionRuntime(ctx, store, prepared, err)
	}

	metadataWarning, err := ensureWriteTransactionRuntimeMetadata(
		commitCtx,
		store,
		prepared.plan.Metadata,
	)
	if err != nil {
		return fs.finishWriteTransactionRuntime(ctx, store, prepared, err)
	}
	if err := callWriteTransactionRuntimeFaultHook("after-metadata"); err != nil {
		return fs.finishWriteTransactionRuntime(ctx, store, prepared, err)
	}
	committedResult, committedErr := fs.writeTransactionJournal.PublishCommitted(
		prepared.operationID,
	)
	if committedErr != nil {
		return fs.finishWriteTransactionRuntime(
			ctx,
			store,
			prepared,
			errors.Join(metadataWarning, committedErr),
		)
	}
	if !committedResult.FinalObserved {
		return fs.finishWriteTransactionRuntime(
			ctx,
			store,
			prepared,
			errors.Join(metadataWarning, ErrWriteTransactionJournalCorrupt),
		)
	}
	if err := callWriteTransactionRuntimeFaultHook("after-committed"); err != nil {
		return fs.finishWriteTransactionRuntime(
			ctx,
			store,
			prepared,
			errors.Join(metadataWarning, err),
		)
	}
	return fs.finishWriteTransactionRuntime(ctx, store, prepared, metadataWarning)
}

func (fs *FileSystem) prepareWriteTransactionRuntime(
	ctx context.Context,
	name string,
	source *stagedWriteFile,
	options writeFileTransactionOptions,
	targetSnapshot writeTargetSnapshot,
	store writeTransactionRuntimeStore,
) (*writeTransactionRuntimePrepared, error) {
	prepared := &writeTransactionRuntimePrepared{
		name:   name,
		source: source,
	}
	operationID, err := newWriteTransactionRuntimeOperationID()
	if err != nil {
		return prepared, err
	}
	prepared.operationID = operationID
	if err := callWriteTransactionRuntimeFaultHook("before-plan"); err != nil {
		return prepared, err
	}
	if err := fs.validateAtomicWriteTargetMount(name); err != nil {
		return prepared, err
	}
	if err := validateWriteFileCondition(targetSnapshot, options.condition); err != nil {
		return prepared, err
	}
	if options.forceVersion && targetSnapshot.exists &&
		targetSnapshot.size > versionstore.MaxVersionObjectSize {
		return prepared, fmt.Errorf(
			"%w: current file exceeds the %d-byte version safety snapshot limit",
			ErrFileTooLarge,
			versionstore.MaxVersionObjectSize,
		)
	}
	targetRel, ok := storageRelativePath(fs.workspace.Root(), fs.workspace.FullPath(name))
	if !ok || targetRel == "." {
		return prepared, ErrWriteConflict
	}
	prepared.targetRel = targetRel
	targetParentRel := filepath.Dir(targetRel)
	if _, err := inspectWriteTransactionRootBinding(
		fs.filesRootHandle,
		targetParentRel,
		false,
	); err != nil {
		if rootio.IsSymlinkError(err) {
			return prepared, errStoragePathSymlink
		}
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return prepared, ErrNotDir
		}
		return prepared, err
	}

	publishMode := os.FileMode(0o644)
	if targetSnapshot.exists {
		publishMode = targetSnapshot.mode.Perm()
	}
	if err := prepareWriteTransactionRuntimeSource(ctx, source, publishMode); err != nil {
		return prepared, err
	}
	sourcePath := source.rel
	sourceObserved, err := inspectWriteTransactionObject(
		ctx,
		fs.internalRootHandle,
		sourcePath,
	)
	if err != nil {
		return prepared, err
	}
	if err := fs.validateWriteTarget(ctx, name, targetSnapshot); err != nil {
		return prepared, err
	}

	var targetBefore *writeTransactionObservedObject
	if targetSnapshot.exists {
		targetBefore, err = inspectWriteTransactionObject(ctx, fs.filesRootHandle, targetRel)
		if err != nil {
			return prepared, err
		}
		if targetBefore.deleteIdentity != targetSnapshot.deleteIdentityToken {
			return prepared, ErrWriteConflict
		}
	} else if _, err := inspectWriteTransactionObject(
		ctx,
		fs.filesRootHandle,
		targetRel,
	); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}
		return prepared, errors.Join(ErrWriteConflict, err)
	}

	shouldVersion := targetBefore != nil &&
		targetBefore.hash != sourceObserved.hash &&
		(options.forceVersion ||
			(fs.policy != nil && fs.policy.ShouldVersion(ctx, name, targetBefore.size)))
	var versionCandidate *versionstore.VersionRecord
	casPlan := WriteTransactionCASPlan{}
	if shouldVersion {
		if targetBefore.size > versionstore.MaxVersionObjectSize {
			return prepared, fmt.Errorf(
				"%w: current file exceeds the %d-byte version safety snapshot limit",
				ErrFileTooLarge,
				versionstore.MaxVersionObjectSize,
			)
		}
		oldExpectation := writeTransactionObjectExpectationFromObserved(
			targetBefore,
			sourcePath,
		)
		prepared.oldData, err = readWriteTransactionObject(
			ctx,
			fs.filesRootHandle,
			targetRel,
			oldExpectation,
		)
		if err != nil {
			return prepared, err
		}
		existedBefore, err := inspectWriteTransactionRuntimeCASBefore(
			ctx,
			store,
			targetBefore.hash,
			targetBefore.size,
		)
		if err != nil {
			return prepared, err
		}
		casPlan = WriteTransactionCASPlan{
			Enabled:       true,
			Hash:          targetBefore.hash,
			Size:          targetBefore.size,
			ExistedBefore: existedBefore,
			PutRequired:   !existedBefore,
		}
		versionCandidate = &versionstore.VersionRecord{
			Path:          name,
			Hash:          targetBefore.hash,
			Size:          targetBefore.size,
			CreatedAtUnix: writeTransactionRuntimeNow().Unix(),
			Comment:       options.versionComment,
		}
	}

	indexAfter := versionstore.FileIndexRecord{
		Path:        name,
		Size:        sourceObserved.size,
		ModTimeUnix: time.Unix(0, sourceObserved.modTimeUnixNano).Unix(),
		ContentHash: sourceObserved.hash,
	}
	metadataPlan, err := store.CaptureWriteMetadataPlan(
		ctx,
		indexAfter,
		versionCandidate,
	)
	if err != nil {
		return prepared, err
	}
	rootBindings, parentBinding, err := fs.captureWriteTransactionRuntimeBindings(
		targetParentRel,
	)
	if err != nil {
		return prepared, err
	}
	sourceEvidence := writeTransactionObjectEvidenceFromObserved(
		sourceObserved,
		sourcePath,
	)
	afterExpectation := writeTransactionObjectExpectationFromObserved(
		sourceObserved,
		targetRel,
	)
	plan := WriteTransactionPlan{
		Kind:  WriteTransactionKindCreate,
		Roots: rootBindings,
		Target: WriteTransactionTargetEvidence{
			Path:                     name,
			RelativePath:             targetRel,
			ParentRelativePath:       targetParentRel,
			ParentPersistentIdentity: parentBinding.PersistentIdentity,
			ParentMode:               parentBinding.Mode,
			After:                    afterExpectation,
		},
		Source: sourceEvidence,
		Stages: WriteTransactionStagePlan{
			Source: sourcePath,
		},
		Metadata: metadataPlan,
		CAS:      casPlan,
	}
	if targetBefore != nil {
		beforeEvidence := writeTransactionObjectEvidenceFromObserved(targetBefore, targetRel)
		oldExpectation := writeTransactionObjectExpectationFromObserved(
			targetBefore,
			sourcePath,
		)
		plan.Kind = WriteTransactionKindOverwrite
		plan.Target.Before = &beforeEvidence
		plan.OldTarget = &oldExpectation
	}
	if err := validateWriteTransactionPlan(plan); err != nil {
		return prepared, err
	}
	prepared.plan = plan
	if err := callWriteTransactionRuntimeFaultHook("after-plan"); err != nil {
		return prepared, err
	}
	return prepared, nil
}

func prepareWriteTransactionRuntimeSource(
	ctx context.Context,
	source *stagedWriteFile,
	publishMode os.FileMode,
) error {
	if source == nil || source.file == nil || source.rel == "" ||
		!source.trusted || source.stageInfo == nil {
		return errors.New("staged write source is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := source.file.Chmod(publishMode); err != nil {
		return fmt.Errorf("set staged write mode: %w", mapWriteStorageCapacityError(err))
	}
	if err := source.file.Sync(); err != nil {
		return fmt.Errorf("sync staged write mode: %w", mapWriteStorageCapacityError(err))
	}
	if err := source.refreshStageInfoFromOpenFile(); err != nil {
		source.retained = true
		return errors.Join(errWriteStageChanged, err)
	}
	if source.stageInfo.Size() != source.size {
		source.retained = true
		return errWriteStageChanged
	}
	if err := source.verifyStageContent(ctx); err != nil {
		source.retained = true
		return err
	}
	return nil
}

func (fs *FileSystem) captureWriteTransactionRuntimeBindings(
	targetParentRel string,
) (WriteTransactionRootBindings, WriteTransactionRootBinding, error) {
	files, err := inspectWriteTransactionRootBinding(fs.filesRootHandle, ".", false)
	if err != nil {
		return WriteTransactionRootBindings{}, WriteTransactionRootBinding{}, err
	}
	internal, err := inspectWriteTransactionRootBinding(fs.internalRootHandle, ".", false)
	if err != nil {
		return WriteTransactionRootBindings{}, WriteTransactionRootBinding{}, err
	}
	staging, err := inspectWriteTransactionRootBinding(
		fs.internalRootHandle,
		writeStagingDir,
		true,
	)
	if err != nil {
		return WriteTransactionRootBindings{}, WriteTransactionRootBinding{}, err
	}
	journalBinding, err := inspectWriteTransactionRootBinding(
		fs.internalRootHandle,
		writeTransactionJournalDir,
		true,
	)
	if err != nil {
		return WriteTransactionRootBindings{}, WriteTransactionRootBinding{}, err
	}
	parent, err := inspectWriteTransactionRootBinding(
		fs.filesRootHandle,
		targetParentRel,
		false,
	)
	if err != nil {
		return WriteTransactionRootBindings{}, WriteTransactionRootBinding{}, err
	}
	bindings := WriteTransactionRootBindings{
		Files:    files,
		Internal: internal,
		Staging:  staging,
		Journal:  journalBinding,
	}
	if internal != fs.writeTransactionJournal.InternalRootBinding() ||
		journalBinding != fs.writeTransactionJournal.JournalRootBinding() {
		return WriteTransactionRootBindings{}, WriteTransactionRootBinding{},
			ErrWriteTransactionJournalConflict
	}
	return bindings, parent, nil
}

func (fs *FileSystem) publishWriteTransactionRuntime(
	ctx context.Context,
	prepared *writeTransactionRuntimePrepared,
) error {
	if prepared == nil {
		return errors.New("prepared write transaction is unavailable")
	}
	switch prepared.plan.Kind {
	case WriteTransactionKindCreate:
		return fs.renameWriteTransactionObjectNoReplace(
			ctx,
			fs.writeTransactionJournal,
			prepared.plan,
			fs.internalRootHandle,
			prepared.plan.Stages.Source,
			fs.filesRootHandle,
			prepared.targetRel,
			prepared.plan.Target.After,
		)
	case WriteTransactionKindOverwrite:
		if prepared.plan.OldTarget == nil {
			return ErrWriteTransactionJournalCorrupt
		}
		return fs.exchangeWriteTransactionObjects(
			ctx,
			fs.writeTransactionJournal,
			prepared.plan,
			prepared.plan.Stages.Source,
			prepared.targetRel,
			prepared.plan.Target.After,
			*prepared.plan.OldTarget,
		)
	default:
		return ErrWriteTransactionJournalCorrupt
	}
}

func inspectWriteTransactionRuntimeCASBefore(
	ctx context.Context,
	store writeTransactionRuntimeStore,
	hash string,
	size int64,
) (bool, error) {
	data, err := store.GetObject(ctx, hash)
	if errors.Is(err, versionstore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect existing CAS object: %w", err)
	}
	if int64(len(data)) != size || computeHash(data) != hash {
		return false, fmt.Errorf(
			"%w: existing CAS object content does not match old content",
			ErrWriteTransactionJournalCorrupt,
		)
	}
	return true, nil
}

func publishWriteTransactionRuntimeCAS(
	ctx context.Context,
	store writeTransactionRuntimeStore,
	plan WriteTransactionCASPlan,
	oldData []byte,
) (WriteTransactionCASOutcome, error) {
	if !plan.Enabled {
		return WriteTransactionCASOutcome{}, nil
	}
	if int64(len(oldData)) != plan.Size || computeHash(oldData) != plan.Hash {
		return WriteTransactionCASOutcome{}, fmt.Errorf(
			"%w: old content does not match CAS plan",
			ErrWriteTransactionJournalCorrupt,
		)
	}
	outcome := WriteTransactionCASOutcome{
		Enabled:      true,
		VerifiedHash: plan.Hash,
		VerifiedSize: plan.Size,
	}
	if plan.ExistedBefore {
		data, err := store.GetObject(ctx, plan.Hash)
		if err != nil {
			return WriteTransactionCASOutcome{}, fmt.Errorf(
				"read existing CAS object: %w",
				err,
			)
		}
		if int64(len(data)) != plan.Size || computeHash(data) != plan.Hash {
			return WriteTransactionCASOutcome{}, fmt.Errorf(
				"%w: existing CAS object changed",
				ErrWriteTransactionJournalCorrupt,
			)
		}
		outcome.VerifiedBefore = true
		return outcome, nil
	}
	put, err := store.PutObjectExpected(ctx, oldData, plan.Hash)
	if err != nil {
		return WriteTransactionCASOutcome{}, fmt.Errorf("store version CAS object: %w", err)
	}
	if put.Hash != plan.Hash || put.Size != plan.Size {
		return WriteTransactionCASOutcome{}, fmt.Errorf(
			"%w: CAS put result does not match plan",
			ErrWriteTransactionJournalCorrupt,
		)
	}
	outcome.PutAttempted = true
	outcome.PutObserved = true
	outcome.Deduplicated = put.Deduplicated
	outcome.CreatedByOperation = !put.Deduplicated
	data, err := store.GetObject(ctx, plan.Hash)
	if err != nil {
		return WriteTransactionCASOutcome{}, fmt.Errorf("verify stored CAS object: %w", err)
	}
	if int64(len(data)) != plan.Size || computeHash(data) != plan.Hash {
		return WriteTransactionCASOutcome{}, fmt.Errorf(
			"%w: stored CAS object content does not match plan",
			ErrWriteTransactionJournalCorrupt,
		)
	}
	return outcome, nil
}

func ensureWriteTransactionRuntimeMetadata(
	ctx context.Context,
	store writeTransactionRuntimeStore,
	plan versionstore.WriteMetadataPlan,
) (warning error, resultErr error) {
	applyErr := store.EnsureWriteMetadataCommitted(ctx, plan)
	state, inspectErr := store.InspectWriteMetadata(ctx, plan)
	if inspectErr != nil {
		return nil, errors.Join(applyErr, fmt.Errorf("inspect write metadata outcome: %w", inspectErr))
	}
	switch state {
	case versionstore.WriteMetadataStateAfter, versionstore.WriteMetadataStateBoth:
		return applyErr, nil
	case versionstore.WriteMetadataStateBefore:
		if applyErr == nil {
			applyErr = versionstore.ErrWriteMetadataOutcomeUnknown
		}
		return nil, applyErr
	case versionstore.WriteMetadataStateConflict:
		return nil, versionstore.ErrWriteMetadataConflict
	default:
		return nil, fmt.Errorf(
			"%w: unknown write metadata state %q",
			versionstore.ErrWriteMetadataConflict,
			state,
		)
	}
}

func (fs *FileSystem) finishWriteTransactionRuntime(
	requestCtx context.Context,
	store writeTransactionRuntimeStore,
	prepared *writeTransactionRuntimePrepared,
	cause error,
) error {
	if prepared == nil {
		return errors.Join(cause, ErrWriteTransactionJournalCorrupt)
	}
	operations, scanErr := fs.writeTransactionJournal.Scan()
	if scanErr != nil {
		_ = closeWriteTransactionRuntimeSource(prepared.source)
		return fs.blockWriteTransactionRuntimeLocked(prepared, cause, scanErr, false)
	}
	operation := findWriteTransactionOperation(operations, prepared.operationID)
	if operation == nil {
		if cause == nil {
			_ = closeWriteTransactionRuntimeSource(prepared.source)
			return fs.blockWriteTransactionRuntimeLocked(
				prepared,
				ErrWriteTransactionJournalCorrupt,
				nil,
				false,
			)
		}
		prepared.source.retained = false
		cleanupErr := prepared.source.discard()
		rollbackCtx, cancelRollback := context.WithTimeout(
			context.WithoutCancel(requestCtx),
			writeRollbackTimeout,
		)
		defer cancelRollback()
		cleanupErr = errors.Join(
			cleanupErr,
			fs.workspace.CleanupCreatedDirs(rollbackCtx, prepared.createdDirs),
		)
		return errors.Join(cause, cleanupErr)
	}
	closeErr := closeWriteTransactionRuntimeSource(prepared.source)
	recoveryCtx, cancelRecovery := context.WithTimeout(
		context.WithoutCancel(requestCtx),
		writeRollbackTimeout,
	)
	defer cancelRecovery()
	recoveryErr := fs.recoverOneWriteTransaction(
		recoveryCtx,
		fs.writeTransactionJournal,
		store,
		*operation,
	)
	if recoveryErr != nil {
		return fs.blockWriteTransactionRuntimeLocked(
			prepared,
			errors.Join(cause, closeErr),
			recoveryErr,
			operation.Decision == WriteTransactionDecisionRollforward,
		)
	}
	if operation.Decision == WriteTransactionDecisionRollforward {
		fs.runWriteTransactionRuntimeRetentionLocked(recoveryCtx, prepared)
	}
	prepared.source.rel = ""
	prepared.source.trusted = false
	prepared.source.retained = false
	prepared.source.releaseReservation()
	resultErr := errors.Join(cause, closeErr)
	if resultErr != nil && operation.Decision == WriteTransactionDecisionRollforward {
		return wrapVisibleMutationWarning(resultErr)
	}
	return resultErr
}

func (fs *FileSystem) runWriteTransactionRuntimeRetentionLocked(
	ctx context.Context,
	prepared *writeTransactionRuntimePrepared,
) {
	if fs == nil || prepared == nil || fs.config == nil {
		return
	}
	if prepared.plan.CAS.Enabled &&
		(fs.config.MaxVersions > 0 || fs.config.MaxVersionAge > 0) {
		if err := fs.cleanupVersions(ctx, prepared.name); err != nil {
			return
		}
	}
	if fs.shouldForceRetentionSweepLocked() {
		_ = fs.runRetentionSweepLocked(ctx)
	}
}

func (fs *FileSystem) blockWriteTransactionRuntimeLocked(
	prepared *writeTransactionRuntimePrepared,
	cause error,
	recoveryErr error,
	visible bool,
) error {
	if prepared != nil && prepared.source != nil {
		prepared.source.retained = true
		prepared.source.trusted = false
	}
	operationID := ""
	var inspectionPaths []string
	if prepared != nil {
		operationID = prepared.operationID
		inspectionPaths = fs.writeTransactionInspectionPaths(prepared.plan)
	}
	blocked := &writeTransactionRecoveryBlockedError{
		operationID:     operationID,
		inspectionPaths: inspectionPaths,
		err:             errors.Join(cause, recoveryErr),
	}
	fs.writeMutationBlocked = errors.Join(fs.writeMutationBlocked, blocked)
	if visible {
		return wrapVisibleMutationWarning(blocked)
	}
	return blocked
}

func (fs *FileSystem) abortWriteTransactionBeforeJournal(
	requestCtx context.Context,
	source *stagedWriteFile,
	createdDirs []workspace.CreatedDir,
	cause error,
) error {
	rollbackCtx, cancelRollback := context.WithTimeout(
		context.WithoutCancel(requestCtx),
		writeRollbackTimeout,
	)
	defer cancelRollback()
	var cleanupErr error
	if source != nil {
		cleanupErr = source.discard()
	}
	if fs != nil && fs.workspace != nil {
		cleanupErr = errors.Join(
			cleanupErr,
			fs.workspace.CleanupCreatedDirs(rollbackCtx, createdDirs),
		)
	}
	cleanupErr = errors.Join(cleanupErr, workspace.ReleaseCreatedDirs(createdDirs))
	return errors.Join(cause, cleanupErr)
}

func closeWriteTransactionRuntimeSource(source *stagedWriteFile) error {
	if source == nil || source.file == nil {
		return nil
	}
	err := source.file.Close()
	source.file = nil
	return err
}

func writeTransactionObjectEvidenceFromObserved(
	observed *writeTransactionObservedObject,
	relativePath string,
) WriteTransactionObjectEvidence {
	if observed == nil {
		return WriteTransactionObjectEvidence{}
	}
	return WriteTransactionObjectEvidence{
		RelativePath:       relativePath,
		PersistentIdentity: observed.persistentIdentity,
		DeleteIdentity:     observed.deleteIdentity,
		Mode:               uint32(observed.mode),
		Size:               observed.size,
		ModTimeUnixNano:    observed.modTimeUnixNano,
		BLAKE3:             observed.hash,
	}
}

func writeTransactionObjectExpectationFromObserved(
	observed *writeTransactionObservedObject,
	relativePath string,
) WriteTransactionObjectExpectation {
	if observed == nil {
		return WriteTransactionObjectExpectation{}
	}
	return WriteTransactionObjectExpectation{
		RelativePath:       relativePath,
		PersistentIdentity: observed.persistentIdentity,
		Mode:               uint32(observed.mode),
		Size:               observed.size,
		ModTimeUnixNano:    observed.modTimeUnixNano,
		BLAKE3:             observed.hash,
	}
}

// snapshotWriteTransactionCreatedDirectories converts Workspace's absolute
// host Path into the journal's normalized logical Path while retaining the
// descriptor-relative path as the filesystem authority.
func (fs *FileSystem) snapshotWriteTransactionCreatedDirectories(
	createdDirs []workspace.CreatedDir,
) (
	[]WriteTransactionCreatedDirectory,
	*WriteTransactionCreatedDirectoryBase,
	error,
) {
	if fs == nil || fs.workspace == nil {
		return nil, nil, errors.New("workspace is unavailable")
	}
	evidence, err := fs.workspace.SnapshotCreatedDirs(createdDirs)
	if err != nil {
		return nil, nil, err
	}
	result := make([]WriteTransactionCreatedDirectory, 0, len(evidence))
	for _, current := range evidence {
		logicalPath := storageWorkspaceName(current.RelativePath)
		expectedAbsolute := fs.workspace.FullPath(logicalPath)
		if filepath.Clean(current.Path) != filepath.Clean(expectedAbsolute) {
			return nil, nil, ErrWriteTransactionJournalCorrupt
		}
		result = append(result, WriteTransactionCreatedDirectory{
			Path:               logicalPath,
			RelativePath:       current.RelativePath,
			PreAbsent:          true,
			PersistentIdentity: current.PersistentIdentity,
			Mode:               uint32(current.Mode),
		})
	}
	if len(result) == 0 {
		return result, nil, nil
	}
	baseRelativePath := filepath.Dir(result[len(result)-1].RelativePath)
	baseBinding, err := inspectWriteTransactionRootBinding(
		fs.filesRootHandle,
		baseRelativePath,
		false,
	)
	if err != nil {
		return nil, nil, err
	}
	return result, &WriteTransactionCreatedDirectoryBase{
		RelativePath:       baseRelativePath,
		PersistentIdentity: baseBinding.PersistentIdentity,
		Mode:               baseBinding.Mode,
	}, nil
}

func newWriteTransactionRuntimeOperationID() (string, error) {
	var operationID [16]byte
	if _, err := writeTransactionRuntimeRandom(operationID[:]); err != nil {
		return "", fmt.Errorf("generate write transaction operation ID: %w", err)
	}
	return hex.EncodeToString(operationID[:]), nil
}

func callWriteTransactionRuntimeFaultHook(point string) error {
	if err := writeTransactionRuntimeFaultHook(point); err != nil {
		return errors.Join(errWriteTransactionRuntimeFaultInjected, err)
	}
	return nil
}
