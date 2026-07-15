package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/versionstore"
	"github.com/seanbao/mnemonas/internal/workspace"
)

const (
	writeTransactionJournalDir         = "write-transaction-journal"
	writeTransactionJournalSchema      = 1
	writeTransactionJournalMaxFileSize = 1 << 20
	writeTransactionJournalFilePrefix  = "write-"
	writeTransactionJournalPending     = "pending-"
)

var (
	// ErrWriteTransactionJournalCorrupt reports journal evidence that cannot be
	// reconciled without making an unsafe write decision.
	ErrWriteTransactionJournalCorrupt = errors.New("write transaction journal is corrupt")
	// ErrWriteTransactionJournalConflict reports an immutable checkpoint or
	// operation state that conflicts with the requested transition.
	ErrWriteTransactionJournalConflict = errors.New("write transaction journal state conflicts with the request")

	errWriteTransactionJournalCrashInjected = errors.New("write transaction journal crash injected")
)

// writeTransactionJournalFaultHook is replaced only by package tests.
var writeTransactionJournalFaultHook = func(string) error { return nil }

// writeTransactionJournalDirectorySync is replaced only by package tests that
// need to model a persistent filesystem sync failure.
var writeTransactionJournalDirectorySync = func(dir *os.File) error {
	return dir.Sync()
}

// WriteTransactionCheckpoint is one immutable transaction checkpoint.
type WriteTransactionCheckpoint string

const (
	WriteTransactionCheckpointPrepared  WriteTransactionCheckpoint = "prepared"
	WriteTransactionCheckpointPublished WriteTransactionCheckpoint = "published"
	WriteTransactionCheckpointCommitted WriteTransactionCheckpoint = "committed"
)

// WriteTransactionKind identifies whether publication creates or replaces the
// canonical target.
type WriteTransactionKind string

const (
	WriteTransactionKindCreate    WriteTransactionKind = "create"
	WriteTransactionKindOverwrite WriteTransactionKind = "overwrite"
)

// WriteTransactionDecision is the only recovery direction authorized by the
// durable checkpoint set.
type WriteTransactionDecision string

const (
	WriteTransactionDecisionRollback    WriteTransactionDecision = "rollback"
	WriteTransactionDecisionRollforward WriteTransactionDecision = "rollforward"
)

// WriteTransactionState classifies both normal checkpoint chains and the two
// suffix states produced by durable roll-forward journal cleanup.
type WriteTransactionState string

const (
	WriteTransactionStatePrepared                   WriteTransactionState = "prepared"
	WriteTransactionStatePublished                  WriteTransactionState = "published"
	WriteTransactionStateCommitted                  WriteTransactionState = "committed"
	WriteTransactionStateRollforwardWithoutPrepared WriteTransactionState = "rollforward_without_prepared"
	WriteTransactionStateRollforwardCommittedOnly   WriteTransactionState = "rollforward_committed_only"
)

// WriteTransactionRootBinding binds one already-opened storage directory.
type WriteTransactionRootBinding struct {
	PersistentIdentity string `json:"persistent_identity"`
	Mode               uint32 `json:"mode"`
}

// WriteTransactionRootBindings records every directory identity needed by
// physical and metadata recovery.
type WriteTransactionRootBindings struct {
	Files    WriteTransactionRootBinding `json:"files"`
	Internal WriteTransactionRootBinding `json:"internal"`
	Staging  WriteTransactionRootBinding `json:"staging"`
	Journal  WriteTransactionRootBinding `json:"journal"`
}

// WriteTransactionObjectEvidence binds one regular filesystem object.
type WriteTransactionObjectEvidence struct {
	RelativePath       string `json:"relative_path"`
	PersistentIdentity string `json:"persistent_identity"`
	DeleteIdentity     string `json:"delete_identity"`
	Mode               uint32 `json:"mode"`
	Size               int64  `json:"size"`
	ModTimeUnixNano    int64  `json:"mod_time_unix_nano"`
	BLAKE3             string `json:"blake3"`
}

// WriteTransactionObjectExpectation binds an object across an intentional
// namespace mutation. DeleteIdentity is deliberately absent because rename and
// exchange may change ctime; PersistentIdentity plus content and stable file
// metadata are the cross-namespace authority.
type WriteTransactionObjectExpectation struct {
	RelativePath       string `json:"relative_path"`
	PersistentIdentity string `json:"persistent_identity"`
	Mode               uint32 `json:"mode"`
	Size               int64  `json:"size"`
	ModTimeUnixNano    int64  `json:"mod_time_unix_nano"`
	BLAKE3             string `json:"blake3"`
}

// WriteTransactionTargetEvidence binds the canonical target and its parent.
type WriteTransactionTargetEvidence struct {
	Path                     string                            `json:"path"`
	RelativePath             string                            `json:"relative_path"`
	ParentRelativePath       string                            `json:"parent_relative_path"`
	ParentPersistentIdentity string                            `json:"parent_persistent_identity"`
	ParentMode               uint32                            `json:"parent_mode"`
	Before                   *WriteTransactionObjectEvidence   `json:"before,omitempty"`
	After                    WriteTransactionObjectExpectation `json:"after"`
}

// WriteTransactionStagePlan lists every reserved stage or residue path that a
// physical recovery implementation may need to inspect.
type WriteTransactionStagePlan struct {
	Source    string `json:"source"`
	Exchange  string `json:"exchange,omitempty"`
	Captured  string `json:"captured,omitempty"`
	Published string `json:"published,omitempty"`
	Committed string `json:"committed,omitempty"`
	Recovery  string `json:"recovery,omitempty"`
}

// WriteTransactionCASPlan is the immutable content-addressed object intent
// captured before physical publication.
type WriteTransactionCASPlan struct {
	Enabled       bool   `json:"enabled"`
	Hash          string `json:"hash,omitempty"`
	Size          int64  `json:"size,omitempty"`
	ExistedBefore bool   `json:"existed_before,omitempty"`
	PutRequired   bool   `json:"put_required,omitempty"`
}

// WriteTransactionCASOutcome records the post-publication CAS result.
// VerifiedHash and VerifiedSize are mandatory when CAS is enabled, including
// when the object existed before this operation. They are semantic evidence
// obtained by reading and hashing the CAS object, not filesystem identity.
type WriteTransactionCASOutcome struct {
	Enabled            bool   `json:"enabled"`
	VerifiedHash       string `json:"verified_hash,omitempty"`
	VerifiedSize       int64  `json:"verified_size,omitempty"`
	VerifiedBefore     bool   `json:"verified_before,omitempty"`
	PutAttempted       bool   `json:"put_attempted,omitempty"`
	PutObserved        bool   `json:"put_observed,omitempty"`
	Deduplicated       bool   `json:"deduplicated,omitempty"`
	CreatedByOperation bool   `json:"created_by_operation,omitempty"`
}

// WriteTransactionPublishedOutcome is immutable publication evidence. It is
// absent from prepared, first appears in published, and is copied unchanged
// into committed.
type WriteTransactionPublishedOutcome struct {
	Target WriteTransactionObjectEvidence `json:"target"`
	CAS    WriteTransactionCASOutcome     `json:"cas"`
}

// WriteTransactionCreatedDirectory records exact ownership evidence for a
// target parent created by this operation. Entries are deepest-first.
type WriteTransactionCreatedDirectory struct {
	Path               string `json:"path"`
	RelativePath       string `json:"relative_path"`
	PreAbsent          bool   `json:"pre_absent"`
	PersistentIdentity string `json:"persistent_identity"`
	Mode               uint32 `json:"mode"`
}

// WriteTransactionCreatedDirectoryBase binds the first non-owned ancestor
// above a contiguous created-directory chain.
type WriteTransactionCreatedDirectoryBase struct {
	RelativePath       string `json:"relative_path"`
	PersistentIdentity string `json:"persistent_identity"`
	Mode               uint32 `json:"mode"`
}

// WriteTransactionPlan is the full immutable recovery plan repeated in every
// checkpoint.
type WriteTransactionPlan struct {
	Kind                 WriteTransactionKind                  `json:"kind"`
	Roots                WriteTransactionRootBindings          `json:"roots"`
	Target               WriteTransactionTargetEvidence        `json:"target"`
	Source               WriteTransactionObjectEvidence        `json:"source"`
	OldTarget            *WriteTransactionObjectExpectation    `json:"old_target,omitempty"`
	Stages               WriteTransactionStagePlan             `json:"stages"`
	Metadata             versionstore.WriteMetadataPlan        `json:"metadata"`
	CAS                  WriteTransactionCASPlan               `json:"cas"`
	CreatedDirectories   []WriteTransactionCreatedDirectory    `json:"created_directories,omitempty"`
	CreatedDirectoryBase *WriteTransactionCreatedDirectoryBase `json:"created_directory_base,omitempty"`
}

// WriteTransactionRecord is one immutable, self-digested checkpoint. The
// record digest covers every field except RecordDigest itself.
type WriteTransactionRecord struct {
	Schema            int                               `json:"schema"`
	Checkpoint        WriteTransactionCheckpoint        `json:"checkpoint"`
	OperationID       string                            `json:"operation_id"`
	PlanDigest        string                            `json:"plan_digest"`
	OutcomeDigest     string                            `json:"outcome_digest,omitempty"`
	PreparedDigest    string                            `json:"prepared_digest,omitempty"`
	PublishedDigest   string                            `json:"published_digest,omitempty"`
	PredecessorDigest string                            `json:"predecessor_digest,omitempty"`
	Plan              WriteTransactionPlan              `json:"plan"`
	Outcome           *WriteTransactionPublishedOutcome `json:"outcome,omitempty"`
	RecordDigest      string                            `json:"record_digest"`
}

// WriteTransactionFinalObservation distinguishes a verified absence from an
// indeterminate read after a failed publication attempt.
type WriteTransactionFinalObservation string

const (
	WriteTransactionFinalAbsent        WriteTransactionFinalObservation = "absent"
	WriteTransactionFinalObservedValid WriteTransactionFinalObservation = "observed_valid"
	WriteTransactionFinalUnknown       WriteTransactionFinalObservation = "unknown"
)

// WriteTransactionPublishResult always exposes whether the immutable final
// checkpoint was observed, fully verified, and covered by a successful journal
// directory sync. For committed, FinalObserved makes the decision irreversible
// even when Err reports an earlier publication warning.
type WriteTransactionPublishResult struct {
	Record           WriteTransactionRecord           `json:"record"`
	FinalObserved    bool                             `json:"final_observed"`
	FinalObservation WriteTransactionFinalObservation `json:"final_observation"`
}

// WriteTransactionOperation is one deterministic scan result.
type WriteTransactionOperation struct {
	OperationID string                   `json:"operation_id"`
	State       WriteTransactionState    `json:"state"`
	Decision    WriteTransactionDecision `json:"decision"`
	Record      WriteTransactionRecord   `json:"record"`
	Prepared    *WriteTransactionRecord  `json:"prepared,omitempty"`
	Published   *WriteTransactionRecord  `json:"published,omitempty"`
	Committed   *WriteTransactionRecord  `json:"committed,omitempty"`
}

// WriteTransactionJournal owns an exclusive lock on one journal directory.
type WriteTransactionJournal struct {
	mu           sync.Mutex
	internalRoot *os.Root
	internalDir  *os.File
	journalDir   *os.File
	internal     WriteTransactionRootBinding
	journal      WriteTransactionRootBinding
	locked       bool
	closed       bool
}

type writeTransactionJournalFileEvidence struct {
	persistentIdentity string
	deleteIdentity     string
	mode               os.FileMode
	size               int64
	modTimeUnixNano    int64
	hash               [sha256.Size]byte
}

type writeTransactionOperationFiles struct {
	final   map[WriteTransactionCheckpoint]string
	pending map[WriteTransactionCheckpoint]string
}

// OpenWriteTransactionJournal opens, validates, syncs, and exclusively locks
// InternalRoot/write-transaction-journal.
func OpenWriteTransactionJournal(internalRoot *os.Root) (*WriteTransactionJournal, error) {
	if internalRoot == nil {
		return nil, errors.New("write transaction internal root is unavailable")
	}
	internalDir, err := rootio.OpenDirNoFollow(internalRoot, ".")
	if err != nil {
		return nil, fmt.Errorf("open write transaction internal root: %w", err)
	}
	if err := rootio.MkdirNoFollow(internalRoot, writeTransactionJournalDir, 0o700); err != nil &&
		!errors.Is(err, os.ErrExist) {
		_ = internalDir.Close()
		return nil, fmt.Errorf("create write transaction journal: %w", err)
	}
	journalDir, err := rootio.OpenDirNoFollow(internalRoot, writeTransactionJournalDir)
	if err != nil {
		_ = internalDir.Close()
		return nil, fmt.Errorf("open write transaction journal: %w", err)
	}
	locked := false
	closeOnError := func(resultErr error) (*WriteTransactionJournal, error) {
		if locked {
			_ = unlockAtomicWriteRenameProbeJournal(journalDir)
			locked = false
		}
		_ = internalDir.Close()
		_ = journalDir.Close()
		return nil, resultErr
	}
	journalLstat, err := internalRoot.Lstat(writeTransactionJournalDir)
	if err != nil {
		return closeOnError(fmt.Errorf("inspect write transaction journal: %w", err))
	}
	journalInfo, err := journalDir.Stat()
	if err != nil {
		return closeOnError(fmt.Errorf("inspect opened write transaction journal: %w", err))
	}
	if !journalLstat.IsDir() || !os.SameFile(journalLstat, journalInfo) ||
		journalLstat.Mode() != os.ModeDir|0o700 ||
		journalInfo.Mode() != os.ModeDir|0o700 {
		return closeOnError(errors.New("write transaction journal directory identity or mode is invalid"))
	}
	if err := tryLockAtomicWriteRenameProbeJournal(journalDir); err != nil {
		return closeOnError(fmt.Errorf("lock write transaction journal: %w", err))
	}
	locked = true
	internalBinding, err := writeTransactionRootBindingFromFile(internalDir)
	if err != nil {
		return closeOnError(fmt.Errorf("bind write transaction internal root: %w", err))
	}
	journalBinding, err := writeTransactionRootBindingFromFile(journalDir)
	if err != nil {
		return closeOnError(fmt.Errorf("bind write transaction journal root: %w", err))
	}
	if err := journalDir.Sync(); err != nil {
		return closeOnError(fmt.Errorf("sync write transaction journal on open: %w", err))
	}
	if err := callWriteTransactionJournalFaultHook("open:journal_directory_synced"); err != nil {
		return closeOnError(err)
	}
	if err := internalDir.Sync(); err != nil {
		return closeOnError(fmt.Errorf("sync internal root for write transaction journal: %w", err))
	}
	if err := callWriteTransactionJournalFaultHook("open:internal_directory_synced"); err != nil {
		return closeOnError(err)
	}
	return &WriteTransactionJournal{
		internalRoot: internalRoot,
		internalDir:  internalDir,
		journalDir:   journalDir,
		internal:     internalBinding,
		journal:      journalBinding,
		locked:       true,
	}, nil
}

// Close releases the journal lock and held directory handles.
func (journal *WriteTransactionJournal) Close() error {
	if journal == nil {
		return nil
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return nil
	}
	journal.closed = true
	var unlockErr error
	if journal.locked {
		unlockErr = unlockAtomicWriteRenameProbeJournal(journal.journalDir)
		journal.locked = false
	}
	return errors.Join(unlockErr, journal.internalDir.Close(), journal.journalDir.Close())
}

// InternalRootBinding returns the identity captured by the journal handle.
func (journal *WriteTransactionJournal) InternalRootBinding() WriteTransactionRootBinding {
	if journal == nil {
		return WriteTransactionRootBinding{}
	}
	return journal.internal
}

// JournalRootBinding returns the identity captured by the journal handle.
func (journal *WriteTransactionJournal) JournalRootBinding() WriteTransactionRootBinding {
	if journal == nil {
		return WriteTransactionRootBinding{}
	}
	return journal.journal
}

// CaptureWriteTransactionRootBinding captures a serializable identity from an
// already-opened directory handle.
func CaptureWriteTransactionRootBinding(dir *os.File) (WriteTransactionRootBinding, error) {
	return writeTransactionRootBindingFromFile(dir)
}

// PublishPrepared writes the first immutable checkpoint. Repeating the same
// request returns the existing record.
func (journal *WriteTransactionJournal) PublishPrepared(
	operationID string,
	plan WriteTransactionPlan,
) (WriteTransactionPublishResult, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.ensureOpenLocked(); err != nil {
		return unknownWriteTransactionPublishResult(), err
	}
	if err := validateWriteTransactionOperationID(operationID); err != nil {
		return unknownWriteTransactionPublishResult(), err
	}
	if err := journal.validatePlanLocked(plan); err != nil {
		return unknownWriteTransactionPublishResult(), err
	}
	operations, err := journal.scanLocked(true)
	if err != nil {
		return unknownWriteTransactionPublishResult(), err
	}
	if existing := findWriteTransactionOperation(operations, operationID); existing != nil {
		if existing.Prepared != nil && reflect.DeepEqual(existing.Prepared.Plan, plan) {
			return observedWriteTransactionPublishResult(*existing.Prepared), nil
		}
		return unknownWriteTransactionPublishResult(), ErrWriteTransactionJournalConflict
	}
	record, err := newWriteTransactionRecord(
		operationID,
		WriteTransactionCheckpointPrepared,
		plan,
		nil,
		nil,
		nil,
	)
	if err != nil {
		return unknownWriteTransactionPublishResult(), err
	}
	return journal.writeRecordLocked(record)
}

// PublishPublished writes the visible-publication checkpoint and binds the
// immutable prepared intent to separately captured physical/CAS outcome.
func (journal *WriteTransactionJournal) PublishPublished(
	operationID string,
	outcome WriteTransactionPublishedOutcome,
) (WriteTransactionPublishResult, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.ensureOpenLocked(); err != nil {
		return unknownWriteTransactionPublishResult(), err
	}
	operations, err := journal.scanLocked(true)
	if err != nil {
		return unknownWriteTransactionPublishResult(), err
	}
	existing := findWriteTransactionOperation(operations, operationID)
	if existing == nil || existing.Prepared == nil {
		return absentWriteTransactionPublishResult(), ErrWriteTransactionJournalConflict
	}
	if err := validateWriteTransactionPublishedOutcome(existing.Prepared.Plan, outcome); err != nil {
		return unknownWriteTransactionPublishResult(), err
	}
	if existing.Published != nil {
		if existing.Published.Outcome != nil && reflect.DeepEqual(*existing.Published.Outcome, outcome) {
			return observedWriteTransactionPublishResult(*existing.Published), nil
		}
		return unknownWriteTransactionPublishResult(), ErrWriteTransactionJournalConflict
	}
	record, err := newWriteTransactionRecord(
		operationID,
		WriteTransactionCheckpointPublished,
		existing.Prepared.Plan,
		&outcome,
		existing.Prepared,
		nil,
	)
	if err != nil {
		return unknownWriteTransactionPublishResult(), err
	}
	return journal.writeRecordLocked(record)
}

// PublishCommitted writes the only irreversible commit decision.
//
// If the result has FinalObserved=true, callers must roll forward or block even
// when err is non-nil: the final committed decision was read back and matched.
func (journal *WriteTransactionJournal) PublishCommitted(
	operationID string,
) (WriteTransactionPublishResult, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.ensureOpenLocked(); err != nil {
		return unknownWriteTransactionPublishResult(), err
	}
	operations, err := journal.scanLocked(true)
	if err != nil {
		return unknownWriteTransactionPublishResult(), err
	}
	existing := findWriteTransactionOperation(operations, operationID)
	if existing == nil || existing.Prepared == nil || existing.Published == nil {
		return absentWriteTransactionPublishResult(), ErrWriteTransactionJournalConflict
	}
	if existing.Committed != nil {
		return observedWriteTransactionPublishResult(*existing.Committed), nil
	}
	record, err := newWriteTransactionRecord(
		operationID,
		WriteTransactionCheckpointCommitted,
		existing.Published.Plan,
		existing.Published.Outcome,
		existing.Prepared,
		existing.Published,
	)
	if err != nil {
		return unknownWriteTransactionPublishResult(), err
	}
	return journal.writeRecordLocked(record)
}

// Scan removes only validly placed pending files, then returns operations sorted
// by operation ID. A final committed file always selects roll-forward.
func (journal *WriteTransactionJournal) Scan() ([]WriteTransactionOperation, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.ensureOpenLocked(); err != nil {
		return nil, err
	}
	return journal.scanLocked(true)
}

// CleanupRollback durably removes published before prepared. It rejects every
// operation that already has a final committed checkpoint.
func (journal *WriteTransactionJournal) CleanupRollback(operationID string) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.ensureOpenLocked(); err != nil {
		return err
	}
	operations, err := journal.scanLocked(true)
	if err != nil {
		return err
	}
	operation := findWriteTransactionOperation(operations, operationID)
	if operation == nil {
		return nil
	}
	if operation.Committed != nil {
		return ErrWriteTransactionJournalConflict
	}
	for _, checkpoint := range []WriteTransactionCheckpoint{
		WriteTransactionCheckpointPublished,
		WriteTransactionCheckpointPrepared,
	} {
		record := writeTransactionOperationRecord(operation, checkpoint)
		if record == nil {
			continue
		}
		if err := journal.removeRecordLocked(*record, "rollback"); err != nil {
			return err
		}
	}
	return nil
}

// CleanupRollforward durably removes prepared, then published, and committed
// last. The two intermediate committed suffixes remain valid scan states.
func (journal *WriteTransactionJournal) CleanupRollforward(operationID string) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.ensureOpenLocked(); err != nil {
		return err
	}
	operations, err := journal.scanLocked(true)
	if err != nil {
		return err
	}
	operation := findWriteTransactionOperation(operations, operationID)
	if operation == nil {
		return nil
	}
	if operation.Committed == nil {
		return ErrWriteTransactionJournalConflict
	}
	for _, checkpoint := range []WriteTransactionCheckpoint{
		WriteTransactionCheckpointPrepared,
		WriteTransactionCheckpointPublished,
		WriteTransactionCheckpointCommitted,
	} {
		record := writeTransactionOperationRecord(operation, checkpoint)
		if record == nil {
			continue
		}
		if err := journal.removeRecordLocked(*record, "rollforward"); err != nil {
			return err
		}
	}
	return nil
}

func (journal *WriteTransactionJournal) ensureOpenLocked() error {
	if journal == nil || journal.closed || journal.internalRoot == nil ||
		journal.internalDir == nil || journal.journalDir == nil {
		return errors.New("write transaction journal is closed")
	}
	return journal.verifyDirectoryLocked()
}

func (journal *WriteTransactionJournal) validatePlanLocked(plan WriteTransactionPlan) error {
	if err := validateWriteTransactionPlan(plan); err != nil {
		return err
	}
	if plan.Roots.Internal != journal.internal || plan.Roots.Journal != journal.journal {
		return fmt.Errorf("%w: internal or journal root binding changed", ErrWriteTransactionJournalConflict)
	}
	stagingDir, err := rootio.OpenDirNoFollow(journal.internalRoot, writeStagingDir)
	if err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	defer stagingDir.Close()
	rootedBefore, err := journal.internalRoot.Lstat(writeStagingDir)
	if err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	held, err := stagingDir.Stat()
	if err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	rootedAfter, err := journal.internalRoot.Lstat(writeStagingDir)
	if err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	binding, err := writeTransactionRootBindingFromInfo(held)
	if err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	if !os.SameFile(rootedBefore, held) ||
		!os.SameFile(held, rootedAfter) ||
		rootedBefore.Mode() != os.ModeDir|0o700 ||
		held.Mode() != os.ModeDir|0o700 ||
		rootedAfter.Mode() != os.ModeDir|0o700 ||
		binding != plan.Roots.Staging {
		return fmt.Errorf("%w: write staging root binding changed", ErrWriteTransactionJournalConflict)
	}
	return journal.verifyDirectoryLocked()
}

func (journal *WriteTransactionJournal) verifyDirectoryLocked() error {
	rootedInternal, err := journal.internalRoot.Lstat(".")
	if err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	heldInternal, err := journal.internalDir.Stat()
	if err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	rootedInternalBinding, err := writeTransactionRootBindingFromInfo(rootedInternal)
	if err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	heldInternalBinding, err := writeTransactionRootBindingFromInfo(heldInternal)
	if err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	if !os.SameFile(rootedInternal, heldInternal) ||
		rootedInternalBinding != journal.internal ||
		heldInternalBinding != journal.internal {
		return ErrWriteTransactionJournalCorrupt
	}
	rooted, err := journal.internalRoot.Lstat(writeTransactionJournalDir)
	if err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	held, err := journal.journalDir.Stat()
	if err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	if !rooted.IsDir() || !held.IsDir() ||
		rooted.Mode() != os.ModeDir|0o700 ||
		held.Mode() != os.ModeDir|0o700 ||
		!os.SameFile(rooted, held) {
		return ErrWriteTransactionJournalCorrupt
	}
	rootedJournalBinding, err := writeTransactionRootBindingFromInfo(rooted)
	if err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	heldJournalBinding, err := writeTransactionRootBindingFromInfo(held)
	if err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	if rootedJournalBinding != journal.journal || heldJournalBinding != journal.journal {
		return ErrWriteTransactionJournalCorrupt
	}
	return nil
}

func (journal *WriteTransactionJournal) scanLocked(cleanPending bool) ([]WriteTransactionOperation, error) {
	if err := journal.verifyDirectoryLocked(); err != nil {
		return nil, err
	}
	if err := writeTransactionJournalDirectorySync(journal.journalDir); err != nil {
		return nil, fmt.Errorf("sync write transaction journal before scan: %w", err)
	}
	if err := journal.verifyDirectoryLocked(); err != nil {
		return nil, err
	}
	files, entries, err := journal.readEntriesLocked()
	if err != nil {
		return nil, err
	}
	records := make(map[string]map[WriteTransactionCheckpoint]WriteTransactionRecord)
	operationIDs := make([]string, 0, len(files))
	for operationID, operationFiles := range files {
		operationIDs = append(operationIDs, operationID)
		stageRecords := make(map[WriteTransactionCheckpoint]WriteTransactionRecord)
		for checkpoint, name := range operationFiles.final {
			record, err := journal.readRecordLocked(name)
			if err != nil {
				return nil, err
			}
			if err := journal.validatePlanLocked(record.Plan); err != nil {
				return nil, err
			}
			if record.OperationID != operationID || record.Checkpoint != checkpoint {
				return nil, fmt.Errorf("%w: checkpoint filename and record mismatch", ErrWriteTransactionJournalCorrupt)
			}
			stageRecords[checkpoint] = record
		}
		records[operationID] = stageRecords
	}
	sort.Strings(operationIDs)

	operations := make([]WriteTransactionOperation, 0, len(operationIDs))
	for _, operationID := range operationIDs {
		operation, err := classifyWriteTransactionOperation(operationID, records[operationID])
		if err != nil {
			return nil, err
		}
		if operation != nil {
			operations = append(operations, *operation)
		}
	}

	pendingNames := make([]string, 0)
	for _, operationID := range operationIDs {
		operationFiles := files[operationID]
		if len(operationFiles.pending) == 0 {
			continue
		}
		if len(operationFiles.pending) != 1 {
			return nil, fmt.Errorf("%w: operation %s has concurrent pending checkpoints", ErrWriteTransactionJournalCorrupt, operationID)
		}
		var checkpoint WriteTransactionCheckpoint
		var name string
		for currentCheckpoint, currentName := range operationFiles.pending {
			checkpoint, name = currentCheckpoint, currentName
		}
		if _, exists := operationFiles.final[checkpoint]; exists {
			return nil, fmt.Errorf("%w: operation %s has final and pending %s checkpoints", ErrWriteTransactionJournalCorrupt, operationID, checkpoint)
		}
		if err := validateWriteTransactionPendingPlacement(records[operationID], checkpoint); err != nil {
			return nil, err
		}
		if err := journal.validatePendingFileLocked(
			name,
			checkpoint,
			records[operationID],
			entries[name],
		); err != nil {
			return nil, err
		}
		pendingNames = append(pendingNames, name)
	}
	sort.Strings(pendingNames)
	if cleanPending && len(pendingNames) > 0 {
		for _, name := range pendingNames {
			if err := journal.removePendingLocked(name); err != nil {
				return nil, err
			}
		}
		return journal.scanLocked(false)
	}
	return operations, nil
}

func (journal *WriteTransactionJournal) readEntriesLocked() (
	map[string]*writeTransactionOperationFiles,
	map[string]os.DirEntry,
	error,
) {
	if err := journal.verifyDirectoryLocked(); err != nil {
		return nil, nil, err
	}
	if _, err := journal.journalDir.Seek(0, io.SeekStart); err != nil {
		return nil, nil, err
	}
	dirEntries, err := journal.journalDir.ReadDir(-1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, err
	}
	files := make(map[string]*writeTransactionOperationFiles)
	entries := make(map[string]os.DirEntry, len(dirEntries))
	for _, entry := range dirEntries {
		name := entry.Name()
		if _, exists := entries[name]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate directory entry %q", ErrWriteTransactionJournalCorrupt, name)
		}
		operationID, checkpoint, pending, ok := parseWriteTransactionJournalName(name)
		if !ok {
			return nil, nil, fmt.Errorf("%w: unknown entry %q", ErrWriteTransactionJournalCorrupt, name)
		}
		if entry.Type()&os.ModeType != 0 && !entry.Type().IsRegular() {
			return nil, nil, fmt.Errorf("%w: checkpoint %q is not a regular file", ErrWriteTransactionJournalCorrupt, name)
		}
		operationFiles := files[operationID]
		if operationFiles == nil {
			operationFiles = &writeTransactionOperationFiles{
				final:   make(map[WriteTransactionCheckpoint]string),
				pending: make(map[WriteTransactionCheckpoint]string),
			}
			files[operationID] = operationFiles
		}
		target := operationFiles.final
		if pending {
			target = operationFiles.pending
		}
		if _, exists := target[checkpoint]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate %s checkpoint for %s", ErrWriteTransactionJournalCorrupt, checkpoint, operationID)
		}
		target[checkpoint] = name
		entries[name] = entry
	}
	if err := journal.verifyDirectoryLocked(); err != nil {
		return nil, nil, err
	}
	return files, entries, nil
}

func (journal *WriteTransactionJournal) readRecordLocked(name string) (WriteTransactionRecord, error) {
	file, _, data, _, err := journal.openStableFileLocked(name)
	if err != nil {
		return WriteTransactionRecord{}, err
	}
	defer file.Close()
	var record WriteTransactionRecord
	if err := decodeStrictWriteTransactionRecord(data, &record); err != nil {
		return WriteTransactionRecord{}, err
	}
	if err := validateWriteTransactionRecord(record); err != nil {
		return WriteTransactionRecord{}, err
	}
	canonical, err := marshalWriteTransactionRecord(record)
	if err != nil {
		return WriteTransactionRecord{}, err
	}
	if !bytes.Equal(data, canonical) {
		return WriteTransactionRecord{}, fmt.Errorf("%w: checkpoint %q is not canonical", ErrWriteTransactionJournalCorrupt, name)
	}
	return record, nil
}

func (journal *WriteTransactionJournal) validatePendingFileLocked(
	name string,
	checkpoint WriteTransactionCheckpoint,
	records map[WriteTransactionCheckpoint]WriteTransactionRecord,
	entry os.DirEntry,
) error {
	if entry == nil {
		return fmt.Errorf("%w: pending checkpoint %q disappeared", ErrWriteTransactionJournalCorrupt, name)
	}
	file, info, data, _, err := journal.openStableFileLocked(name)
	if err != nil {
		return err
	}
	closeErr := file.Close()
	if closeErr != nil {
		return closeErr
	}
	if info.Mode() != 0o600 || len(data) > writeTransactionJournalMaxFileSize {
		return fmt.Errorf("%w: pending checkpoint %q metadata is invalid", ErrWriteTransactionJournalCorrupt, name)
	}
	if len(data) == 0 {
		return nil
	}
	var complete WriteTransactionRecord
	if err := decodeStrictWriteTransactionRecord(data, &complete); err == nil {
		if err := validateWriteTransactionRecord(complete); err != nil {
			return err
		}
		if complete.OperationID != operationIDFromPendingName(name) ||
			complete.Checkpoint != checkpoint {
			return fmt.Errorf("%w: pending checkpoint identity mismatch", ErrWriteTransactionJournalCorrupt)
		}
		if err := journal.validatePlanLocked(complete.Plan); err != nil {
			return err
		}
		if err := validateCompleteWriteTransactionPendingRecord(complete, records); err != nil {
			return err
		}
		canonical, err := marshalWriteTransactionRecord(complete)
		if err != nil {
			return err
		}
		if len(data) > len(canonical) || !bytes.Equal(data, canonical[:len(data)]) {
			return fmt.Errorf("%w: pending checkpoint %q is not canonical", ErrWriteTransactionJournalCorrupt, name)
		}
		return nil
	} else if !isWriteTransactionUnexpectedJSONEnd(data) {
		return fmt.Errorf("%w: pending checkpoint %q is not a truncated JSON record", ErrWriteTransactionJournalCorrupt, name)
	}
	if err := validateWriteTransactionPendingPrefix(
		data,
		operationIDFromPendingName(name),
		checkpoint,
		records,
	); err != nil {
		return fmt.Errorf("%w: pending checkpoint %q is not a valid record prefix: %v", ErrWriteTransactionJournalCorrupt, name, err)
	}
	return nil
}

func validateCompleteWriteTransactionPendingRecord(
	record WriteTransactionRecord,
	records map[WriteTransactionCheckpoint]WriteTransactionRecord,
) error {
	switch record.Checkpoint {
	case WriteTransactionCheckpointPrepared:
		if len(records) != 0 {
			return ErrWriteTransactionJournalCorrupt
		}
	case WriteTransactionCheckpointPublished:
		prepared, ok := records[WriteTransactionCheckpointPrepared]
		if !ok || len(records) != 1 {
			return ErrWriteTransactionJournalCorrupt
		}
		if err := validateWriteTransactionRecordPair(prepared, record); err != nil {
			return err
		}
	case WriteTransactionCheckpointCommitted:
		prepared, preparedOK := records[WriteTransactionCheckpointPrepared]
		published, publishedOK := records[WriteTransactionCheckpointPublished]
		if !preparedOK || !publishedOK || len(records) != 2 {
			return ErrWriteTransactionJournalCorrupt
		}
		expected, err := newWriteTransactionRecord(
			record.OperationID,
			WriteTransactionCheckpointCommitted,
			published.Plan,
			published.Outcome,
			&prepared,
			&published,
		)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(record, expected) {
			return ErrWriteTransactionJournalCorrupt
		}
	default:
		return ErrWriteTransactionJournalCorrupt
	}
	return nil
}

type writeTransactionPendingPrefixPiece struct {
	literal string
	hex     int
}

func validateWriteTransactionPendingPrefix(
	data []byte,
	operationID string,
	checkpoint WriteTransactionCheckpoint,
	records map[WriteTransactionCheckpoint]WriteTransactionRecord,
) error {
	base := fmt.Sprintf(
		`{"schema":%d,"checkpoint":%q,"operation_id":%q,"plan_digest":"`,
		writeTransactionJournalSchema,
		checkpoint,
		operationID,
	)
	var pieces []writeTransactionPendingPrefixPiece
	switch checkpoint {
	case WriteTransactionCheckpointPrepared:
		pieces = []writeTransactionPendingPrefixPiece{
			{literal: base},
			{hex: sha256.Size * 2},
			{literal: `","plan":`},
		}
	case WriteTransactionCheckpointPublished:
		prepared, ok := records[WriteTransactionCheckpointPrepared]
		if !ok {
			return errors.New("prepared predecessor is unavailable")
		}
		planJSON, err := json.Marshal(prepared.Plan)
		if err != nil {
			return err
		}
		pieces = []writeTransactionPendingPrefixPiece{
			{literal: base + prepared.PlanDigest + `","outcome_digest":"`},
			{hex: sha256.Size * 2},
			{literal: `","prepared_digest":"` + prepared.RecordDigest +
				`","predecessor_digest":"` + prepared.RecordDigest +
				`","plan":` + string(planJSON) + `,"outcome":`},
		}
	case WriteTransactionCheckpointCommitted:
		prepared, preparedOK := records[WriteTransactionCheckpointPrepared]
		published, publishedOK := records[WriteTransactionCheckpointPublished]
		if !preparedOK || !publishedOK {
			return errors.New("committed predecessors are unavailable")
		}
		expected, err := newWriteTransactionRecord(
			operationID,
			WriteTransactionCheckpointCommitted,
			published.Plan,
			published.Outcome,
			&prepared,
			&published,
		)
		if err != nil {
			return err
		}
		expectedData, err := marshalWriteTransactionRecord(expected)
		if err != nil {
			return err
		}
		if len(data) > len(expectedData) || !bytes.Equal(data, expectedData[:len(data)]) {
			return errors.New("committed bytes differ from the expected record")
		}
		return nil
	default:
		return errors.New("checkpoint is invalid")
	}
	if err := matchWriteTransactionPendingPrefixPieces(data, pieces); err != nil {
		return err
	}
	return validateWriteTransactionJSONPrefixKeys(data)
}

func matchWriteTransactionPendingPrefixPieces(
	data []byte,
	pieces []writeTransactionPendingPrefixPiece,
) error {
	offset := 0
	for _, piece := range pieces {
		if piece.literal != "" {
			remaining := len(data) - offset
			if remaining <= 0 {
				return nil
			}
			compare := len(piece.literal)
			if remaining < compare {
				compare = remaining
			}
			if !bytes.Equal(data[offset:offset+compare], []byte(piece.literal[:compare])) {
				return errors.New("literal header mismatch")
			}
			offset += compare
			if compare < len(piece.literal) {
				return nil
			}
			continue
		}
		remaining := len(data) - offset
		if remaining <= 0 {
			return nil
		}
		compare := piece.hex
		if remaining < compare {
			compare = remaining
		}
		for _, value := range data[offset : offset+compare] {
			if !((value >= '0' && value <= '9') || (value >= 'a' && value <= 'f')) {
				return errors.New("digest prefix is not lowercase hexadecimal")
			}
		}
		offset += compare
		if compare < piece.hex {
			return nil
		}
	}
	return nil
}

func isWriteTransactionUnexpectedJSONEnd(data []byte) bool {
	var value any
	err := json.Unmarshal(data, &value)
	var syntaxErr *json.SyntaxError
	return errors.As(err, &syntaxErr) &&
		syntaxErr.Offset >= int64(len(data)) &&
		strings.Contains(syntaxErr.Error(), "unexpected end of JSON input")
}

func validateWriteTransactionJSONPrefixKeys(data []byte) error {
stringScan:
	for index := 0; index < len(data); index++ {
		if data[index] <= ' ' {
			return errors.New("canonical JSON prefix contains whitespace")
		}
		if data[index] != '"' {
			continue
		}
		start := index
		index++
		escaped := false
		for index < len(data) {
			switch {
			case escaped:
				escaped = false
			case data[index] == '\\':
				escaped = true
			case data[index] == '"':
				raw := data[start+1 : index]
				if index+1 < len(data) && data[index+1] == ':' {
					if bytes.IndexByte(raw, '\\') >= 0 ||
						!writeTransactionJSONKeyAllowed(string(raw)) {
						return fmt.Errorf("unknown JSON field %q", raw)
					}
				}
				continue stringScan
			}
			index++
		}
		previous := byte(0)
		if start > 0 {
			previous = data[start-1]
		}
		if previous == '{' || previous == ',' {
			raw := data[start+1:]
			if bytes.IndexByte(raw, '\\') >= 0 ||
				!writeTransactionJSONKeyPrefixAllowed(string(raw)) {
				return fmt.Errorf("unknown partial JSON field %q", raw)
			}
		}
		return nil
	}
	return nil
}

func writeTransactionJSONKeyAllowed(key string) bool {
	_, ok := writeTransactionJSONKeys[key]
	return ok
}

func writeTransactionJSONKeyPrefixAllowed(prefix string) bool {
	for key := range writeTransactionJSONKeys {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

var writeTransactionJSONKeys = map[string]struct{}{
	"after": {}, "before": {}, "blake3": {}, "captured": {},
	"cas": {}, "checkpoint": {}, "comment": {}, "comment_is_null": {},
	"committed": {}, "content_hash": {}, "content_hash_is_null": {},
	"created_at_unix": {}, "created_by_operation": {}, "created_directories": {},
	"created_directory_base": {},
	"deduplicated":           {}, "delete_identity": {}, "enabled": {}, "exchange": {},
	"existed_before": {}, "files": {}, "hash": {}, "id": {}, "index_after": {},
	"index_before": {}, "internal": {}, "journal": {}, "kind": {}, "metadata": {},
	"mod_time_unix": {}, "mod_time_unix_nano": {}, "mode": {}, "old_target": {},
	"operation_id": {}, "outcome": {}, "outcome_digest": {}, "parent_mode": {},
	"parent_persistent_identity": {}, "parent_relative_path": {}, "path": {},
	"persistent_identity": {}, "plan": {}, "plan_digest": {}, "pre_absent": {},
	"predecessor_digest": {}, "prepared_digest": {}, "published": {},
	"published_digest": {}, "put_attempted": {}, "put_observed": {},
	"put_required": {}, "record_digest": {}, "recovery": {}, "relative_path": {},
	"roots": {}, "schema": {}, "size": {}, "source": {}, "stages": {},
	"staging": {}, "target": {}, "verified_before": {}, "verified_hash": {},
	"verified_size": {}, "version_after": {}, "version_before": {},
}

func operationIDFromPendingName(name string) string {
	operationID, _, pending, ok := parseWriteTransactionJournalName(name)
	if !ok || !pending {
		return ""
	}
	return operationID
}

func (journal *WriteTransactionJournal) removePendingLocked(name string) error {
	file, info, _, evidence, err := journal.openStableFileLocked(name)
	if err != nil {
		return err
	}
	defer file.Close()
	if info.Mode() != 0o600 {
		return fmt.Errorf("%w: pending checkpoint %q mode is invalid", ErrWriteTransactionJournalCorrupt, name)
	}
	if err := journal.removeLeafCheckedLocked(
		name,
		file,
		info,
		evidence,
		"checkpoint:pending_cleanup:directory_sync",
	); err != nil {
		return err
	}
	return callWriteTransactionJournalFaultHook("checkpoint:pending_removed:" + name)
}

func (journal *WriteTransactionJournal) writeRecordLocked(
	record WriteTransactionRecord,
) (WriteTransactionPublishResult, error) {
	if err := journal.verifyDirectoryLocked(); err != nil {
		return publishResultWithRecord(record, WriteTransactionFinalUnknown), err
	}
	data, err := marshalWriteTransactionRecord(record)
	if err != nil {
		return publishResultWithRecord(record, WriteTransactionFinalUnknown), err
	}
	if len(data) > writeTransactionJournalMaxFileSize {
		return publishResultWithRecord(record, WriteTransactionFinalAbsent),
			errors.New("write transaction checkpoint is too large")
	}
	finalName := writeTransactionJournalName(record.OperationID, record.Checkpoint, false)
	pendingName := writeTransactionJournalName(record.OperationID, record.Checkpoint, true)
	finalRel := filepath.Join(writeTransactionJournalDir, finalName)
	pendingRel := filepath.Join(writeTransactionJournalDir, pendingName)
	if _, err := journal.internalRoot.Lstat(finalRel); err == nil {
		return journal.finishWriteTransactionPublicationLocked(
			record,
			ErrWriteTransactionJournalConflict,
		)
	} else if !errors.Is(err, os.ErrNotExist) {
		return publishResultWithRecord(record, WriteTransactionFinalUnknown), err
	}
	file, err := rootio.OpenFileNoFollow(
		journal.internalRoot,
		pendingRel,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600,
	)
	if err != nil {
		return journal.finishWriteTransactionPublicationLocked(record, err)
	}
	if err := callWriteTransactionJournalFaultHook(
		"checkpoint:" + string(record.Checkpoint) + ":pending_created",
	); err != nil {
		_ = file.Close()
		return journal.finishWriteTransactionPublicationLocked(record, err)
	}
	split := len(data) / 2
	firstWritten, writeErr := file.Write(data[:split])
	if writeErr == nil && firstWritten != split {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		if err := callWriteTransactionJournalFaultHook(
			"checkpoint:" + string(record.Checkpoint) + ":pending_partial",
		); err != nil {
			_ = file.Close()
			return journal.finishWriteTransactionPublicationLocked(record, err)
		}
	}
	if writeErr == nil {
		var secondWritten int
		secondWritten, writeErr = file.Write(data[split:])
		if writeErr == nil && secondWritten != len(data)-split {
			writeErr = io.ErrShortWrite
		}
	}
	syncErr := callWriteTransactionJournalFaultHook(
		"checkpoint:" + string(record.Checkpoint) + ":pending_file_sync",
	)
	if syncErr == nil {
		syncErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return journal.finishWriteTransactionPublicationLocked(record, err)
	}
	if err := callWriteTransactionJournalFaultHook(
		"checkpoint:" + string(record.Checkpoint) + ":pending_file_synced",
	); err != nil {
		return journal.finishWriteTransactionPublicationLocked(record, err)
	}
	if err := rootio.RenameLeafNoReplace(journal.internalRoot, pendingRel, finalRel); err != nil {
		return journal.finishWriteTransactionPublicationLocked(record, err)
	}
	if err := callWriteTransactionJournalFaultHook(
		"checkpoint:" + string(record.Checkpoint) + ":final_renamed",
	); err != nil {
		return journal.finishWriteTransactionPublicationLocked(record, err)
	}
	syncErr = callWriteTransactionJournalFaultHook(
		"checkpoint:" + string(record.Checkpoint) + ":directory_sync",
	)
	if syncErr == nil {
		syncErr = writeTransactionJournalDirectorySync(journal.journalDir)
	}
	if syncErr != nil {
		return journal.finishWriteTransactionPublicationLocked(record, syncErr)
	}
	if err := callWriteTransactionJournalFaultHook(
		"checkpoint:" + string(record.Checkpoint) + ":directory_synced",
	); err != nil {
		return journal.finishWriteTransactionPublicationLocked(record, err)
	}
	return journal.finishWriteTransactionPublicationLocked(record, nil)
}

func (journal *WriteTransactionJournal) finishWriteTransactionPublicationLocked(
	record WriteTransactionRecord,
	publicationErr error,
) (WriteTransactionPublishResult, error) {
	finalName := writeTransactionJournalName(record.OperationID, record.Checkpoint, false)
	current, err := journal.readRecordLocked(finalName)
	if errors.Is(err, os.ErrNotExist) {
		if publicationErr == nil {
			publicationErr = ErrWriteTransactionJournalCorrupt
		}
		return publishResultWithRecord(record, WriteTransactionFinalAbsent), publicationErr
	}
	if err != nil {
		return publishResultWithRecord(record, WriteTransactionFinalUnknown),
			errors.Join(publicationErr, err)
	}
	if !reflect.DeepEqual(current, record) {
		return publishResultWithRecord(record, WriteTransactionFinalUnknown),
			errors.Join(publicationErr, ErrWriteTransactionJournalCorrupt)
	}
	if err := writeTransactionJournalDirectorySync(journal.journalDir); err != nil {
		return publishResultWithRecord(record, WriteTransactionFinalUnknown),
			errors.Join(publicationErr, fmt.Errorf(
				"sync observed write transaction checkpoint: %w",
				err,
			))
	}
	return observedWriteTransactionPublishResult(record), publicationErr
}

func publishResultWithRecord(
	record WriteTransactionRecord,
	observation WriteTransactionFinalObservation,
) WriteTransactionPublishResult {
	return WriteTransactionPublishResult{
		Record:           record,
		FinalObserved:    observation == WriteTransactionFinalObservedValid,
		FinalObservation: observation,
	}
}

func observedWriteTransactionPublishResult(
	record WriteTransactionRecord,
) WriteTransactionPublishResult {
	return publishResultWithRecord(record, WriteTransactionFinalObservedValid)
}

func absentWriteTransactionPublishResult() WriteTransactionPublishResult {
	return publishResultWithRecord(WriteTransactionRecord{}, WriteTransactionFinalAbsent)
}

func unknownWriteTransactionPublishResult() WriteTransactionPublishResult {
	return publishResultWithRecord(WriteTransactionRecord{}, WriteTransactionFinalUnknown)
}

func (journal *WriteTransactionJournal) removeRecordLocked(
	record WriteTransactionRecord,
	direction string,
) error {
	name := writeTransactionJournalName(record.OperationID, record.Checkpoint, false)
	file, info, data, evidence, err := journal.openStableFileLocked(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	expected, err := marshalWriteTransactionRecord(record)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, expected) {
		return ErrWriteTransactionJournalCorrupt
	}
	if err := journal.removeLeafCheckedLocked(
		name,
		file,
		info,
		evidence,
		"checkpoint:cleanup:"+direction+":"+string(record.Checkpoint)+"_directory_sync",
	); err != nil {
		return err
	}
	return callWriteTransactionJournalFaultHook(
		"checkpoint:cleanup:" + direction + ":" + string(record.Checkpoint) + "_removed",
	)
}

func (journal *WriteTransactionJournal) removeLeafCheckedLocked(
	name string,
	opened *os.File,
	info os.FileInfo,
	evidence writeTransactionJournalFileEvidence,
	directorySyncFaultPoint string,
) error {
	if err := journal.verifyDirectoryLocked(); err != nil {
		return err
	}
	verify := func(_ string, current os.FileInfo) error {
		actual, err := newWriteTransactionJournalFileEvidence(current, nil)
		if err != nil {
			return err
		}
		if actual.persistentIdentity != evidence.persistentIdentity ||
			actual.deleteIdentity != evidence.deleteIdentity ||
			actual.mode != evidence.mode ||
			actual.size != evidence.size ||
			actual.modTimeUnixNano != evidence.modTimeUnixNano {
			return ErrWriteTransactionJournalCorrupt
		}
		return nil
	}
	if err := rootio.RemoveAllFromDirNoFollowCheckedInPlace(
		journal.journalDir,
		name,
		verify,
	); err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	syncErr := callWriteTransactionJournalFaultHook(directorySyncFaultPoint)
	if syncErr == nil {
		syncErr = journal.journalDir.Sync()
	}
	if syncErr != nil {
		return syncErr
	}
	after, err := opened.Stat()
	if err != nil || !os.SameFile(info, after) {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	return nil
}

func (journal *WriteTransactionJournal) openStableFileLocked(
	name string,
) (*os.File, os.FileInfo, []byte, writeTransactionJournalFileEvidence, error) {
	if err := journal.verifyDirectoryLocked(); err != nil {
		return nil, nil, nil, writeTransactionJournalFileEvidence{}, err
	}
	rel := filepath.Join(writeTransactionJournalDir, name)
	file, err := rootio.OpenRegularFileNoFollow(journal.internalRoot, rel)
	if err != nil {
		return nil, nil, nil, writeTransactionJournalFileEvidence{}, err
	}
	fail := func(resultErr error) (*os.File, os.FileInfo, []byte, writeTransactionJournalFileEvidence, error) {
		_ = file.Close()
		return nil, nil, nil, writeTransactionJournalFileEvidence{}, resultErr
	}
	rootedBefore, err := journal.internalRoot.Lstat(rel)
	if err != nil {
		return fail(err)
	}
	before, err := file.Stat()
	if err != nil || !os.SameFile(rootedBefore, before) {
		return fail(errors.Join(ErrWriteTransactionJournalCorrupt, err))
	}
	if before.Mode() != 0o600 || before.Size() < 0 ||
		before.Size() > writeTransactionJournalMaxFileSize {
		return fail(ErrWriteTransactionJournalCorrupt)
	}
	data, err := io.ReadAll(io.LimitReader(file, writeTransactionJournalMaxFileSize+1))
	if err != nil || int64(len(data)) != before.Size() {
		return fail(errors.Join(ErrWriteTransactionJournalCorrupt, err))
	}
	after, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	rootedAfter, err := journal.internalRoot.Lstat(rel)
	if err != nil {
		return fail(err)
	}
	beforeEvidence, err := newWriteTransactionJournalFileEvidence(before, data)
	if err != nil {
		return fail(err)
	}
	afterEvidence, err := newWriteTransactionJournalFileEvidence(after, data)
	if err != nil {
		return fail(err)
	}
	rootedEvidence, err := newWriteTransactionJournalFileEvidence(rootedAfter, data)
	if err != nil {
		return fail(err)
	}
	if !os.SameFile(before, after) || !os.SameFile(after, rootedAfter) ||
		beforeEvidence != afterEvidence || afterEvidence != rootedEvidence {
		return fail(ErrWriteTransactionJournalCorrupt)
	}
	if err := journal.verifyDirectoryLocked(); err != nil {
		return fail(err)
	}
	return file, after, data, afterEvidence, nil
}

func newWriteTransactionJournalFileEvidence(
	info os.FileInfo,
	data []byte,
) (writeTransactionJournalFileEvidence, error) {
	if info == nil || !info.Mode().IsRegular() {
		return writeTransactionJournalFileEvidence{}, ErrWriteTransactionJournalCorrupt
	}
	persistentIdentity := workspace.PersistentIdentityTokenForFileInfo(info)
	deleteIdentity := workspace.DeleteIdentityTokenForFileInfo(info)
	if persistentIdentity == "" || deleteIdentity == "" {
		return writeTransactionJournalFileEvidence{}, ErrWriteTransactionJournalCorrupt
	}
	return writeTransactionJournalFileEvidence{
		persistentIdentity: persistentIdentity,
		deleteIdentity:     deleteIdentity,
		mode:               info.Mode(),
		size:               info.Size(),
		modTimeUnixNano:    info.ModTime().UnixNano(),
		hash:               sha256.Sum256(data),
	}, nil
}

func newWriteTransactionRecord(
	operationID string,
	checkpoint WriteTransactionCheckpoint,
	plan WriteTransactionPlan,
	outcome *WriteTransactionPublishedOutcome,
	prepared *WriteTransactionRecord,
	published *WriteTransactionRecord,
) (WriteTransactionRecord, error) {
	if err := validateWriteTransactionOperationID(operationID); err != nil {
		return WriteTransactionRecord{}, err
	}
	planData, err := json.Marshal(plan)
	if err != nil {
		return WriteTransactionRecord{}, err
	}
	planDigest := sha256.Sum256(planData)
	record := WriteTransactionRecord{
		Schema:      writeTransactionJournalSchema,
		Checkpoint:  checkpoint,
		OperationID: operationID,
		PlanDigest:  hex.EncodeToString(planDigest[:]),
		Plan:        plan,
	}
	switch checkpoint {
	case WriteTransactionCheckpointPrepared:
		if outcome != nil || prepared != nil || published != nil {
			return WriteTransactionRecord{}, ErrWriteTransactionJournalConflict
		}
	case WriteTransactionCheckpointPublished:
		if outcome == nil || prepared == nil || published != nil {
			return WriteTransactionRecord{}, ErrWriteTransactionJournalConflict
		}
		if err := validateWriteTransactionPublishedOutcome(plan, *outcome); err != nil {
			return WriteTransactionRecord{}, err
		}
		outcomeDigest, err := writeTransactionOutcomeDigest(*outcome)
		if err != nil {
			return WriteTransactionRecord{}, err
		}
		outcomeCopy := *outcome
		record.Outcome = &outcomeCopy
		record.OutcomeDigest = outcomeDigest
		record.PreparedDigest = prepared.RecordDigest
		record.PredecessorDigest = prepared.RecordDigest
	case WriteTransactionCheckpointCommitted:
		if outcome == nil || prepared == nil || published == nil ||
			published.Outcome == nil ||
			!reflect.DeepEqual(*published.Outcome, *outcome) {
			return WriteTransactionRecord{}, ErrWriteTransactionJournalConflict
		}
		outcomeDigest, err := writeTransactionOutcomeDigest(*outcome)
		if err != nil {
			return WriteTransactionRecord{}, err
		}
		if published.OutcomeDigest != outcomeDigest {
			return WriteTransactionRecord{}, ErrWriteTransactionJournalConflict
		}
		outcomeCopy := *outcome
		record.Outcome = &outcomeCopy
		record.OutcomeDigest = outcomeDigest
		record.PreparedDigest = prepared.RecordDigest
		record.PublishedDigest = published.RecordDigest
		record.PredecessorDigest = published.RecordDigest
	default:
		return WriteTransactionRecord{}, ErrWriteTransactionJournalConflict
	}
	digest, err := writeTransactionRecordDigest(record)
	if err != nil {
		return WriteTransactionRecord{}, err
	}
	record.RecordDigest = digest
	return record, validateWriteTransactionRecord(record)
}

func validateWriteTransactionRecord(record WriteTransactionRecord) error {
	if record.Schema != writeTransactionJournalSchema {
		return fmt.Errorf("%w: unsupported schema", ErrWriteTransactionJournalCorrupt)
	}
	if err := validateWriteTransactionOperationID(record.OperationID); err != nil {
		return err
	}
	if err := validateWriteTransactionPlan(record.Plan); err != nil {
		return err
	}
	planData, err := json.Marshal(record.Plan)
	if err != nil {
		return err
	}
	planHash := sha256.Sum256(planData)
	if record.PlanDigest != hex.EncodeToString(planHash[:]) {
		return fmt.Errorf("%w: plan digest mismatch", ErrWriteTransactionJournalCorrupt)
	}
	switch record.Checkpoint {
	case WriteTransactionCheckpointPrepared:
		if record.Outcome != nil || record.OutcomeDigest != "" ||
			record.PreparedDigest != "" || record.PublishedDigest != "" ||
			record.PredecessorDigest != "" {
			return fmt.Errorf("%w: prepared digest chain is invalid", ErrWriteTransactionJournalCorrupt)
		}
	case WriteTransactionCheckpointPublished:
		if record.Outcome == nil ||
			!validWriteTransactionDigest(record.OutcomeDigest) ||
			!validWriteTransactionDigest(record.PreparedDigest) ||
			record.PublishedDigest != "" ||
			record.PredecessorDigest != record.PreparedDigest {
			return fmt.Errorf("%w: published digest chain is invalid", ErrWriteTransactionJournalCorrupt)
		}
	case WriteTransactionCheckpointCommitted:
		if record.Outcome == nil ||
			!validWriteTransactionDigest(record.OutcomeDigest) ||
			!validWriteTransactionDigest(record.PreparedDigest) ||
			!validWriteTransactionDigest(record.PublishedDigest) ||
			record.PredecessorDigest != record.PublishedDigest {
			return fmt.Errorf("%w: committed digest chain is invalid", ErrWriteTransactionJournalCorrupt)
		}
	default:
		return fmt.Errorf("%w: checkpoint is invalid", ErrWriteTransactionJournalCorrupt)
	}
	if record.Outcome != nil {
		if err := validateWriteTransactionPublishedOutcome(record.Plan, *record.Outcome); err != nil {
			return err
		}
		outcomeDigest, err := writeTransactionOutcomeDigest(*record.Outcome)
		if err != nil {
			return err
		}
		if record.OutcomeDigest != outcomeDigest {
			return fmt.Errorf("%w: publication outcome digest mismatch", ErrWriteTransactionJournalCorrupt)
		}
	}
	if !validWriteTransactionDigest(record.RecordDigest) {
		return fmt.Errorf("%w: record digest is invalid", ErrWriteTransactionJournalCorrupt)
	}
	expected, err := writeTransactionRecordDigest(record)
	if err != nil {
		return err
	}
	if record.RecordDigest != expected {
		return fmt.Errorf("%w: record digest mismatch", ErrWriteTransactionJournalCorrupt)
	}
	return nil
}

func writeTransactionOutcomeDigest(outcome WriteTransactionPublishedOutcome) (string, error) {
	data, err := json.Marshal(outcome)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func writeTransactionRecordDigest(record WriteTransactionRecord) (string, error) {
	record.RecordDigest = ""
	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func marshalWriteTransactionRecord(record WriteTransactionRecord) ([]byte, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func decodeStrictWriteTransactionRecord(data []byte, record *WriteTransactionRecord) error {
	if len(data) == 0 || len(data) > writeTransactionJournalMaxFileSize {
		return ErrWriteTransactionJournalCorrupt
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(record); err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing JSON value")
		}
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	return nil
}

func classifyWriteTransactionOperation(
	operationID string,
	records map[WriteTransactionCheckpoint]WriteTransactionRecord,
) (*WriteTransactionOperation, error) {
	prepared, hasPrepared := records[WriteTransactionCheckpointPrepared]
	published, hasPublished := records[WriteTransactionCheckpointPublished]
	committed, hasCommitted := records[WriteTransactionCheckpointCommitted]
	if !hasPrepared && !hasPublished && !hasCommitted {
		return nil, nil
	}
	if hasPublished && !hasPrepared && !hasCommitted {
		return nil, fmt.Errorf("%w: published checkpoint lacks prepared predecessor", ErrWriteTransactionJournalCorrupt)
	}
	if hasCommitted && hasPrepared && !hasPublished {
		return nil, fmt.Errorf("%w: committed checkpoint lacks published predecessor", ErrWriteTransactionJournalCorrupt)
	}
	if hasPrepared && hasPublished {
		if err := validateWriteTransactionRecordPair(prepared, published); err != nil {
			return nil, err
		}
	}
	if hasPublished && hasCommitted {
		if err := validateWriteTransactionRecordPair(published, committed); err != nil {
			return nil, err
		}
		if published.PreparedDigest != committed.PreparedDigest {
			return nil, fmt.Errorf("%w: committed prepared digest changed", ErrWriteTransactionJournalCorrupt)
		}
	}
	if hasPrepared && hasCommitted && prepared.RecordDigest != committed.PreparedDigest {
		return nil, fmt.Errorf("%w: committed prepared digest mismatch", ErrWriteTransactionJournalCorrupt)
	}

	result := &WriteTransactionOperation{
		OperationID: operationID,
		Decision:    WriteTransactionDecisionRollback,
	}
	if hasPrepared {
		copy := prepared
		result.Prepared = &copy
		result.Record = copy
		result.State = WriteTransactionStatePrepared
	}
	if hasPublished {
		copy := published
		result.Published = &copy
		result.Record = copy
		result.State = WriteTransactionStatePublished
	}
	if hasCommitted {
		copy := committed
		result.Committed = &copy
		result.Record = copy
		result.Decision = WriteTransactionDecisionRollforward
		result.State = WriteTransactionStateCommitted
		if !hasPrepared && hasPublished {
			result.State = WriteTransactionStateRollforwardWithoutPrepared
		}
		if !hasPrepared && !hasPublished {
			result.State = WriteTransactionStateRollforwardCommittedOnly
		}
	}
	return result, nil
}

func validateWriteTransactionRecordPair(
	predecessor WriteTransactionRecord,
	successor WriteTransactionRecord,
) error {
	if predecessor.OperationID != successor.OperationID ||
		predecessor.PlanDigest != successor.PlanDigest ||
		!reflect.DeepEqual(predecessor.Plan, successor.Plan) ||
		successor.PredecessorDigest != predecessor.RecordDigest {
		return fmt.Errorf("%w: checkpoint predecessor mismatch", ErrWriteTransactionJournalCorrupt)
	}
	if predecessor.Checkpoint == WriteTransactionCheckpointPublished &&
		(successor.OutcomeDigest != predecessor.OutcomeDigest ||
			!reflect.DeepEqual(successor.Outcome, predecessor.Outcome)) {
		return fmt.Errorf("%w: committed publication outcome changed", ErrWriteTransactionJournalCorrupt)
	}
	return nil
}

func validateWriteTransactionPendingPlacement(
	records map[WriteTransactionCheckpoint]WriteTransactionRecord,
	checkpoint WriteTransactionCheckpoint,
) error {
	_, prepared := records[WriteTransactionCheckpointPrepared]
	_, published := records[WriteTransactionCheckpointPublished]
	_, committed := records[WriteTransactionCheckpointCommitted]
	valid := false
	switch checkpoint {
	case WriteTransactionCheckpointPrepared:
		valid = !prepared && !published && !committed
	case WriteTransactionCheckpointPublished:
		valid = prepared && !published && !committed
	case WriteTransactionCheckpointCommitted:
		valid = prepared && published && !committed
	}
	if !valid {
		return fmt.Errorf("%w: pending %s checkpoint is out of order", ErrWriteTransactionJournalCorrupt, checkpoint)
	}
	return nil
}

func validateWriteTransactionPlan(plan WriteTransactionPlan) error {
	if plan.Kind != WriteTransactionKindCreate && plan.Kind != WriteTransactionKindOverwrite {
		return fmt.Errorf("%w: write kind is invalid", ErrWriteTransactionJournalCorrupt)
	}
	for label, binding := range map[string]WriteTransactionRootBinding{
		"files":    plan.Roots.Files,
		"internal": plan.Roots.Internal,
		"staging":  plan.Roots.Staging,
		"journal":  plan.Roots.Journal,
	} {
		if !validWriteTransactionDigest(binding.PersistentIdentity) ||
			os.FileMode(binding.Mode)&os.ModeDir == 0 {
			return fmt.Errorf("%w: %s root binding is invalid", ErrWriteTransactionJournalCorrupt, label)
		}
	}
	if plan.Roots.Staging.Mode != uint32(os.ModeDir|0o700) ||
		plan.Roots.Journal.Mode != uint32(os.ModeDir|0o700) {
		return fmt.Errorf("%w: private root mode is invalid", ErrWriteTransactionJournalCorrupt)
	}
	if plan.Target.Path == "" || strings.IndexByte(plan.Target.Path, 0) >= 0 {
		return fmt.Errorf("%w: target path is invalid", ErrWriteTransactionJournalCorrupt)
	}
	if err := validateWriteTransactionRelativePath(plan.Target.RelativePath, false); err != nil {
		return err
	}
	if err := validateWriteTransactionRelativePath(plan.Target.ParentRelativePath, true); err != nil {
		return err
	}
	if storageWorkspaceName(plan.Target.RelativePath) != plan.Target.Path ||
		storageWorkspaceRelativeName(plan.Target.Path) != plan.Target.RelativePath ||
		plan.Target.After.RelativePath != plan.Target.RelativePath ||
		plan.Target.ParentRelativePath != filepath.Dir(plan.Target.RelativePath) {
		return fmt.Errorf("%w: target path relationships are inconsistent", ErrWriteTransactionJournalCorrupt)
	}
	if !validWriteTransactionDigest(plan.Target.ParentPersistentIdentity) ||
		os.FileMode(plan.Target.ParentMode)&os.ModeDir == 0 {
		return fmt.Errorf("%w: target parent evidence is invalid", ErrWriteTransactionJournalCorrupt)
	}
	if err := validateWriteTransactionObjectEvidence(plan.Source); err != nil {
		return fmt.Errorf("validate source evidence: %w", err)
	}
	if err := validateWriteTransactionObjectExpectation(plan.Target.After); err != nil {
		return fmt.Errorf("validate target after expectation: %w", err)
	}
	if !writeTransactionEvidenceMatchesExpectation(plan.Source, plan.Target.After, false) {
		return fmt.Errorf("%w: source and target-after stable evidence differ", ErrWriteTransactionJournalCorrupt)
	}
	if plan.Kind == WriteTransactionKindOverwrite {
		if plan.OldTarget == nil || plan.Target.Before == nil {
			return fmt.Errorf("%w: overwrite lacks old target evidence", ErrWriteTransactionJournalCorrupt)
		}
		if err := validateWriteTransactionObjectExpectation(*plan.OldTarget); err != nil {
			return fmt.Errorf("validate old target expectation: %w", err)
		}
		if err := validateWriteTransactionObjectEvidence(*plan.Target.Before); err != nil {
			return fmt.Errorf("validate target before evidence: %w", err)
		}
		if plan.Target.Before.RelativePath != plan.Target.RelativePath ||
			plan.OldTarget.RelativePath != plan.Stages.Source ||
			!writeTransactionEvidenceMatchesExpectation(*plan.Target.Before, *plan.OldTarget, false) {
			return fmt.Errorf("%w: old target and target-before evidence differ", ErrWriteTransactionJournalCorrupt)
		}
	} else if plan.OldTarget != nil || plan.Target.Before != nil {
		return fmt.Errorf("%w: create unexpectedly has old target evidence", ErrWriteTransactionJournalCorrupt)
	}
	if err := validateWriteTransactionStagePlan(plan); err != nil {
		return err
	}
	if plan.Source.RelativePath != plan.Stages.Source {
		return fmt.Errorf("%w: source evidence does not name the prepared stage", ErrWriteTransactionJournalCorrupt)
	}
	if err := validateWriteTransactionCASPlan(plan.CAS); err != nil {
		return err
	}
	if err := versionstore.ValidateWriteMetadataPlan(plan.Metadata); err != nil {
		return errors.Join(ErrWriteTransactionJournalCorrupt, err)
	}
	if plan.Metadata.IndexAfter.Path != plan.Target.Path {
		return fmt.Errorf("%w: metadata path does not match target", ErrWriteTransactionJournalCorrupt)
	}
	if plan.Metadata.IndexAfter.Size != plan.Target.After.Size ||
		plan.Metadata.IndexAfter.ContentHashIsNull ||
		plan.Metadata.IndexAfter.ContentHash != plan.Target.After.BLAKE3 ||
		plan.Metadata.IndexAfter.ModTimeUnix !=
			time.Unix(0, plan.Target.After.ModTimeUnixNano).Unix() {
		return fmt.Errorf("%w: metadata after-state does not match target content", ErrWriteTransactionJournalCorrupt)
	}
	if plan.CAS.Enabled {
		if plan.Kind != WriteTransactionKindOverwrite ||
			plan.OldTarget == nil ||
			plan.Metadata.VersionAfter == nil ||
			plan.CAS.Hash != plan.OldTarget.BLAKE3 ||
			plan.CAS.Size != plan.OldTarget.Size ||
			plan.Metadata.VersionAfter.Path != plan.Target.Path ||
			plan.Metadata.VersionAfter.Hash != plan.CAS.Hash ||
			plan.Metadata.VersionAfter.Size != plan.CAS.Size ||
			(plan.Metadata.VersionBefore != nil && !plan.CAS.ExistedBefore) {
			return fmt.Errorf("%w: CAS and version metadata do not match old content", ErrWriteTransactionJournalCorrupt)
		}
	} else if plan.Metadata.VersionBefore != nil || plan.Metadata.VersionAfter != nil {
		return fmt.Errorf("%w: version metadata lacks a CAS plan", ErrWriteTransactionJournalCorrupt)
	}
	if plan.Kind == WriteTransactionKindOverwrite &&
		(len(plan.CreatedDirectories) != 0 || plan.CreatedDirectoryBase != nil) {
		return fmt.Errorf("%w: overwrite cannot own target directories", ErrWriteTransactionJournalCorrupt)
	}
	if len(plan.CreatedDirectories) == 0 && plan.CreatedDirectoryBase != nil {
		return fmt.Errorf("%w: created-directory base lacks an owned chain", ErrWriteTransactionJournalCorrupt)
	}
	if len(plan.CreatedDirectories) != 0 && plan.CreatedDirectoryBase == nil {
		return fmt.Errorf("%w: created-directory chain lacks its base binding", ErrWriteTransactionJournalCorrupt)
	}
	seenDirs := make(map[string]struct{}, len(plan.CreatedDirectories))
	lastDepth := int(^uint(0) >> 1)
	lastAtDepth := ""
	for index, directory := range plan.CreatedDirectories {
		if directory.Path == "" ||
			!directory.PreAbsent ||
			!validWriteTransactionDigest(directory.PersistentIdentity) ||
			os.FileMode(directory.Mode)&os.ModeDir == 0 {
			return fmt.Errorf("%w: created directory evidence is invalid", ErrWriteTransactionJournalCorrupt)
		}
		if err := validateWriteTransactionRelativePath(directory.RelativePath, false); err != nil {
			return err
		}
		if storageWorkspaceName(directory.RelativePath) != directory.Path ||
			storageWorkspaceRelativeName(directory.Path) != directory.RelativePath ||
			(index == 0 &&
				(directory.RelativePath != plan.Target.ParentRelativePath ||
					directory.PersistentIdentity != plan.Target.ParentPersistentIdentity ||
					directory.Mode != plan.Target.ParentMode)) ||
			(index > 0 &&
				filepath.Dir(plan.CreatedDirectories[index-1].RelativePath) != directory.RelativePath) {
			return fmt.Errorf("%w: created directory ancestry is invalid", ErrWriteTransactionJournalCorrupt)
		}
		if _, exists := seenDirs[directory.RelativePath]; exists {
			return fmt.Errorf("%w: duplicate created directory", ErrWriteTransactionJournalCorrupt)
		}
		seenDirs[directory.RelativePath] = struct{}{}
		depth := writeTransactionPathDepth(directory.RelativePath)
		if depth > lastDepth || (depth == lastDepth && lastAtDepth > directory.RelativePath) {
			return fmt.Errorf("%w: created directories are not deepest-first", ErrWriteTransactionJournalCorrupt)
		}
		lastDepth = depth
		lastAtDepth = directory.RelativePath
	}
	if base := plan.CreatedDirectoryBase; base != nil {
		if err := validateWriteTransactionRelativePath(base.RelativePath, true); err != nil {
			return err
		}
		if !validWriteTransactionDigest(base.PersistentIdentity) ||
			os.FileMode(base.Mode)&os.ModeDir == 0 ||
			base.RelativePath != filepath.Dir(
				plan.CreatedDirectories[len(plan.CreatedDirectories)-1].RelativePath,
			) {
			return fmt.Errorf("%w: created-directory base binding is invalid", ErrWriteTransactionJournalCorrupt)
		}
		if _, exists := seenDirs[base.RelativePath]; exists {
			return fmt.Errorf("%w: created-directory base is owned by the operation", ErrWriteTransactionJournalCorrupt)
		}
		if base.RelativePath == "." &&
			(base.PersistentIdentity != plan.Roots.Files.PersistentIdentity ||
				base.Mode != plan.Roots.Files.Mode) {
			return fmt.Errorf("%w: created-directory root base differs from the files root", ErrWriteTransactionJournalCorrupt)
		}
	}
	return nil
}

func validateWriteTransactionObjectEvidence(evidence WriteTransactionObjectEvidence) error {
	if err := validateWriteTransactionRelativePath(evidence.RelativePath, false); err != nil {
		return err
	}
	if !validWriteTransactionDigest(evidence.PersistentIdentity) ||
		!validWriteTransactionDigest(evidence.DeleteIdentity) ||
		!validWriteTransactionDigest(evidence.BLAKE3) ||
		!os.FileMode(evidence.Mode).IsRegular() ||
		evidence.Size < 0 ||
		evidence.ModTimeUnixNano <= 0 {
		return ErrWriteTransactionJournalCorrupt
	}
	return nil
}

func validateWriteTransactionObjectExpectation(
	expectation WriteTransactionObjectExpectation,
) error {
	if err := validateWriteTransactionRelativePath(expectation.RelativePath, false); err != nil {
		return err
	}
	if !validWriteTransactionDigest(expectation.PersistentIdentity) ||
		!validWriteTransactionDigest(expectation.BLAKE3) ||
		!os.FileMode(expectation.Mode).IsRegular() ||
		expectation.Size < 0 ||
		expectation.ModTimeUnixNano <= 0 {
		return ErrWriteTransactionJournalCorrupt
	}
	return nil
}

func writeTransactionEvidenceMatchesExpectation(
	evidence WriteTransactionObjectEvidence,
	expectation WriteTransactionObjectExpectation,
	matchRelativePath bool,
) bool {
	return (!matchRelativePath || evidence.RelativePath == expectation.RelativePath) &&
		evidence.PersistentIdentity == expectation.PersistentIdentity &&
		evidence.Mode == expectation.Mode &&
		evidence.Size == expectation.Size &&
		evidence.ModTimeUnixNano == expectation.ModTimeUnixNano &&
		evidence.BLAKE3 == expectation.BLAKE3
}

func validateWriteTransactionStagePlan(plan WriteTransactionPlan) error {
	sourceIdentity := plan.Source.PersistentIdentity
	if err := validateWriteTransactionStagePath(
		plan.Stages.Source,
		writeSourceStagePrefix,
		".tmp",
		sourceIdentity,
		false,
	); err != nil {
		return fmt.Errorf("validate source stage: %w", err)
	}
	if err := validateWriteTransactionStagePath(
		plan.Stages.Exchange,
		writeExchangeStagePrefix,
		".tmp",
		sourceIdentity,
		true,
	); err != nil {
		return fmt.Errorf("validate exchange stage: %w", err)
	}
	if plan.Stages.Exchange != "" {
		return fmt.Errorf("%w: pre-prepared exchange stages are forbidden", ErrWriteTransactionJournalCorrupt)
	}
	oldIdentity := ""
	if plan.OldTarget != nil {
		oldIdentity = plan.OldTarget.PersistentIdentity
	}
	for _, stage := range []struct {
		path           string
		prefix         string
		extension      string
		expected       string
		alternate      string
		allowEmptyPath bool
	}{
		{plan.Stages.Captured, writeCapturedStagePrefix, ".tmp", oldIdentity, "", true},
		{plan.Stages.Published, writePublishedStagePrefix, ".tmp", sourceIdentity, "", true},
		{plan.Stages.Committed, writeCommittedStagePrefix, ".tmp", oldIdentity, "", true},
		{plan.Stages.Recovery, writeRecoveryStagePrefix, ".stage", sourceIdentity, oldIdentity, true},
	} {
		if err := validateWriteTransactionStagePath(
			stage.path,
			stage.prefix,
			stage.extension,
			stage.expected,
			stage.allowEmptyPath,
			stage.alternate,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateWriteTransactionStagePath(
	path string,
	prefix string,
	extension string,
	expectedIdentity string,
	allowEmpty bool,
	alternateIdentity ...string,
) error {
	if path == "" {
		if allowEmpty {
			return nil
		}
		return fmt.Errorf("%w: required stage path is missing", ErrWriteTransactionJournalCorrupt)
	}
	if err := validateWriteTransactionRelativePath(path, false); err != nil {
		return err
	}
	if filepath.Dir(path) != writeStagingDir {
		return fmt.Errorf("%w: stage path escapes write staging", ErrWriteTransactionJournalCorrupt)
	}
	identity, ok := parseWriteStagePersistentIdentity(filepath.Base(path), prefix, extension)
	if !ok {
		return fmt.Errorf("%w: stage name is invalid", ErrWriteTransactionJournalCorrupt)
	}
	if identity == expectedIdentity {
		return nil
	}
	for _, alternate := range alternateIdentity {
		if alternate != "" && identity == alternate {
			return nil
		}
	}
	return fmt.Errorf("%w: stage name identity mismatch", ErrWriteTransactionJournalCorrupt)
}

func validateWriteTransactionCASPlan(cas WriteTransactionCASPlan) error {
	if !cas.Enabled {
		if cas.Hash != "" || cas.Size != 0 || cas.ExistedBefore || cas.PutRequired {
			return fmt.Errorf("%w: disabled CAS plan has state", ErrWriteTransactionJournalCorrupt)
		}
		return nil
	}
	if !validWriteTransactionDigest(cas.Hash) || cas.Size < 0 {
		return fmt.Errorf("%w: CAS plan is invalid", ErrWriteTransactionJournalCorrupt)
	}
	if cas.PutRequired == cas.ExistedBefore {
		return fmt.Errorf("%w: CAS decision is inconsistent", ErrWriteTransactionJournalCorrupt)
	}
	return nil
}

func validateWriteTransactionPublishedOutcome(
	plan WriteTransactionPlan,
	outcome WriteTransactionPublishedOutcome,
) error {
	if err := validateWriteTransactionObjectEvidence(outcome.Target); err != nil {
		return fmt.Errorf("validate published target evidence: %w", err)
	}
	if !writeTransactionEvidenceMatchesExpectation(outcome.Target, plan.Target.After, true) {
		return fmt.Errorf("%w: published target does not match prepared expectation", ErrWriteTransactionJournalCorrupt)
	}
	cas := outcome.CAS
	if !plan.CAS.Enabled {
		if cas.Enabled || cas.VerifiedHash != "" || cas.VerifiedSize != 0 ||
			cas.VerifiedBefore || cas.PutAttempted || cas.PutObserved ||
			cas.Deduplicated || cas.CreatedByOperation {
			return fmt.Errorf("%w: disabled CAS outcome has state", ErrWriteTransactionJournalCorrupt)
		}
		return nil
	}
	if !cas.Enabled ||
		cas.VerifiedHash != plan.CAS.Hash ||
		cas.VerifiedSize != plan.CAS.Size {
		return fmt.Errorf("%w: CAS content verification mismatch", ErrWriteTransactionJournalCorrupt)
	}
	if plan.CAS.ExistedBefore {
		if !cas.VerifiedBefore || cas.PutAttempted || cas.PutObserved ||
			cas.Deduplicated || cas.CreatedByOperation {
			return fmt.Errorf("%w: existing CAS outcome is inconsistent", ErrWriteTransactionJournalCorrupt)
		}
		return nil
	}
	if cas.VerifiedBefore || !cas.PutAttempted || !cas.PutObserved ||
		cas.Deduplicated == cas.CreatedByOperation {
		return fmt.Errorf("%w: CAS put outcome is inconsistent", ErrWriteTransactionJournalCorrupt)
	}
	return nil
}

func validateWriteTransactionRelativePath(path string, allowDot bool) error {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path ||
		strings.IndexByte(path, 0) >= 0 {
		return fmt.Errorf("%w: relative path is invalid", ErrWriteTransactionJournalCorrupt)
	}
	if path == "." {
		if allowDot {
			return nil
		}
		return fmt.Errorf("%w: relative path is invalid", ErrWriteTransactionJournalCorrupt)
	}
	if path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: relative path escapes its root", ErrWriteTransactionJournalCorrupt)
	}
	return nil
}

func writeTransactionRootBindingFromFile(file *os.File) (WriteTransactionRootBinding, error) {
	if file == nil {
		return WriteTransactionRootBinding{}, errors.New("directory handle is unavailable")
	}
	info, err := file.Stat()
	if err != nil {
		return WriteTransactionRootBinding{}, err
	}
	return writeTransactionRootBindingFromInfo(info)
}

func writeTransactionRootBindingFromInfo(info os.FileInfo) (WriteTransactionRootBinding, error) {
	if info == nil {
		return WriteTransactionRootBinding{}, errors.New("directory identity is unavailable")
	}
	if !info.IsDir() {
		return WriteTransactionRootBinding{}, errors.New("root binding is not a directory")
	}
	identity := workspace.PersistentIdentityTokenForFileInfo(info)
	if identity == "" {
		return WriteTransactionRootBinding{}, errors.New("root persistent identity is unavailable")
	}
	return WriteTransactionRootBinding{
		PersistentIdentity: identity,
		Mode:               uint32(info.Mode()),
	}, nil
}

func validateWriteTransactionOperationID(operationID string) error {
	decoded, err := hex.DecodeString(operationID)
	if err != nil || len(decoded) != 16 || operationID != strings.ToLower(operationID) {
		return fmt.Errorf("%w: operation ID is invalid", ErrWriteTransactionJournalCorrupt)
	}
	return nil
}

func validWriteTransactionDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func writeTransactionJournalName(
	operationID string,
	checkpoint WriteTransactionCheckpoint,
	pending bool,
) string {
	name := writeTransactionJournalFilePrefix + operationID + "." + string(checkpoint) + ".json"
	if pending {
		return writeTransactionJournalPending + name
	}
	return name
}

func parseWriteTransactionJournalName(
	name string,
) (string, WriteTransactionCheckpoint, bool, bool) {
	pending := strings.HasPrefix(name, writeTransactionJournalPending)
	if pending {
		name = strings.TrimPrefix(name, writeTransactionJournalPending)
	}
	if !strings.HasPrefix(name, writeTransactionJournalFilePrefix) ||
		!strings.HasSuffix(name, ".json") {
		return "", "", false, false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(name, writeTransactionJournalFilePrefix), ".json")
	separator := strings.LastIndexByte(body, '.')
	if separator <= 0 {
		return "", "", false, false
	}
	operationID := body[:separator]
	checkpoint := WriteTransactionCheckpoint(body[separator+1:])
	if err := validateWriteTransactionOperationID(operationID); err != nil {
		return "", "", false, false
	}
	switch checkpoint {
	case WriteTransactionCheckpointPrepared,
		WriteTransactionCheckpointPublished,
		WriteTransactionCheckpointCommitted:
		return operationID, checkpoint, pending, true
	default:
		return "", "", false, false
	}
}

func findWriteTransactionOperation(
	operations []WriteTransactionOperation,
	operationID string,
) *WriteTransactionOperation {
	index := sort.Search(len(operations), func(index int) bool {
		return operations[index].OperationID >= operationID
	})
	if index >= len(operations) || operations[index].OperationID != operationID {
		return nil
	}
	return &operations[index]
}

func writeTransactionOperationRecord(
	operation *WriteTransactionOperation,
	checkpoint WriteTransactionCheckpoint,
) *WriteTransactionRecord {
	if operation == nil {
		return nil
	}
	switch checkpoint {
	case WriteTransactionCheckpointPrepared:
		return operation.Prepared
	case WriteTransactionCheckpointPublished:
		return operation.Published
	case WriteTransactionCheckpointCommitted:
		return operation.Committed
	default:
		return nil
	}
}

func writeTransactionPathDepth(path string) int {
	if path == "." {
		return 0
	}
	return len(strings.Split(filepath.Clean(path), string(filepath.Separator)))
}

func callWriteTransactionJournalFaultHook(point string) error {
	if err := writeTransactionJournalFaultHook(point); err != nil {
		return errors.Join(errWriteTransactionJournalCrashInjected, err)
	}
	return nil
}
