package versionstore

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrWriteMetadataConflict reports that the current version/index pair is
	// neither the exact before-state nor the exact after-state in a plan.
	ErrWriteMetadataConflict = errors.New("write metadata state conflicts with its transaction plan")
	// ErrInvalidWriteMetadataPlan reports a malformed or internally
	// inconsistent write metadata plan.
	ErrInvalidWriteMetadataPlan = errors.New("invalid write metadata transaction plan")
	// ErrWriteMetadataOutcomeUnknown reports that SQLite may have committed a
	// metadata transaction even though its commit or cleanup returned an error.
	// A durable decision journal must reconcile the recorded plan with a
	// detached recovery context before it releases its mutation lease.
	ErrWriteMetadataOutcomeUnknown = errors.New("write metadata transaction outcome is unknown")
)

// FileIndexRecord is the exact SQLite representation of one files row.
// ModTimeUnix intentionally uses the database's current whole-second precision.
type FileIndexRecord struct {
	Path              string `json:"path"`
	Size              int64  `json:"size"`
	ModTimeUnix       int64  `json:"mod_time_unix"`
	ContentHash       string `json:"content_hash"`
	ContentHashIsNull bool   `json:"content_hash_is_null"`
}

// VersionRecord is the exact SQLite representation of one versions row. An ID
// of zero is permitted only for a planned insert whose autoincrement ID is not
// known before commit.
type VersionRecord struct {
	ID            int64  `json:"id"`
	Path          string `json:"path"`
	Hash          string `json:"hash"`
	Size          int64  `json:"size"`
	CreatedAtUnix int64  `json:"created_at_unix"`
	Comment       string `json:"comment"`
	CommentIsNull bool   `json:"comment_is_null"`
}

// WriteMetadataPlan binds the exact file-index and optional version row states
// on both sides of one streamed-write decision. The upper storage layer must
// hold one mutation lease from CaptureWriteMetadataPlan until the durable
// decision has been committed or rolled back. Direct SQLite changes that do
// not participate in that lease are outside this plan's recovery guarantee.
type WriteMetadataPlan struct {
	IndexBefore   *FileIndexRecord `json:"index_before,omitempty"`
	IndexAfter    FileIndexRecord  `json:"index_after"`
	VersionBefore *VersionRecord   `json:"version_before,omitempty"`
	VersionAfter  *VersionRecord   `json:"version_after,omitempty"`
}

// ValidateWriteMetadataPlan validates a plan decoded from a durable journal
// without reading or changing SQLite state.
func ValidateWriteMetadataPlan(plan WriteMetadataPlan) error {
	return validateWriteMetadataPlan(plan)
}

// WriteMetadataState classifies the current database state against a plan.
type WriteMetadataState string

const (
	WriteMetadataStateBefore   WriteMetadataState = "before"
	WriteMetadataStateAfter    WriteMetadataState = "after"
	WriteMetadataStateBoth     WriteMetadataState = "both"
	WriteMetadataStateConflict WriteMetadataState = "conflict"
)

var (
	// writeMetadataTestHook is nil outside package tests. It injects failures
	// only after SQL mutations, while the immediate transaction is still open.
	writeMetadataTestHook func(stage string) error
	// The transaction hooks always use the real sql.Tx outside tests. A test
	// replacement for the commit hook must still call tx.Commit before
	// returning so it can model an ambiguous result without leaking a tx.
	writeMetadataCommitTransaction   = commitWriteMetadataTransaction
	writeMetadataRollbackTransaction = rollbackWriteMetadataTransaction
)

type writeMetadataConnection interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type writeMetadataSnapshot struct {
	index   *FileIndexRecord
	version *VersionRecord
}

// CaptureWriteMetadataPlan captures the exact current rows and prepares their
// desired after-state. plannedVersion is inserted only when its (path, hash)
// row does not already exist; an existing row is preserved byte-for-byte.
func (s *Store) CaptureWriteMetadataPlan(
	ctx context.Context,
	indexAfter FileIndexRecord,
	plannedVersion *VersionRecord,
) (WriteMetadataPlan, error) {
	if s == nil || s.db == nil {
		return WriteMetadataPlan{}, errors.New("version store is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	normalizedIndex, err := normalizeWriteMetadataIndex(indexAfter, false)
	if err != nil {
		return WriteMetadataPlan{}, err
	}
	var normalizedCandidate *VersionRecord
	if plannedVersion != nil {
		candidate, normalizeErr := normalizeWriteMetadataVersion(*plannedVersion, true, false)
		if normalizeErr != nil {
			return WriteMetadataPlan{}, normalizeErr
		}
		if candidate.Path != normalizedIndex.Path {
			return WriteMetadataPlan{}, fmt.Errorf(
				"%w: version path does not match index path",
				ErrInvalidWriteMetadataPlan,
			)
		}
		normalizedCandidate = &candidate
	}

	var result WriteMetadataPlan
	err = s.withImmediateWriteMetadataTransaction(ctx, func(conn writeMetadataConnection) error {
		indexBefore, queryErr := queryWriteMetadataIndex(ctx, conn, normalizedIndex.Path)
		if queryErr != nil {
			return queryErr
		}
		result = WriteMetadataPlan{
			IndexBefore: cloneFileIndexRecord(indexBefore),
			IndexAfter:  normalizedIndex,
		}
		if normalizedCandidate == nil {
			return nil
		}
		versionBefore, queryErr := queryWriteMetadataVersion(
			ctx,
			conn,
			normalizedCandidate.Path,
			normalizedCandidate.Hash,
		)
		if queryErr != nil {
			return queryErr
		}
		if versionBefore != nil {
			if versionBefore.Size != normalizedCandidate.Size {
				return fmt.Errorf(
					"%w: existing version size %d does not match planned size %d",
					ErrWriteMetadataConflict,
					versionBefore.Size,
					normalizedCandidate.Size,
				)
			}
			result.VersionBefore = cloneVersionRecord(versionBefore)
			result.VersionAfter = cloneVersionRecord(versionBefore)
			return nil
		}
		result.VersionAfter = cloneVersionRecord(normalizedCandidate)
		return nil
	})
	if err != nil {
		return WriteMetadataPlan{}, err
	}
	if err := validateWriteMetadataPlan(result); err != nil {
		return WriteMetadataPlan{}, err
	}
	return result, nil
}

// InspectWriteMetadata classifies the current rows in one immediate SQLite
// transaction so an observer never sees a version/index pair from two
// different commits.
func (s *Store) InspectWriteMetadata(
	ctx context.Context,
	plan WriteMetadataPlan,
) (WriteMetadataState, error) {
	if s == nil || s.db == nil {
		return WriteMetadataStateConflict, errors.New("version store is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateWriteMetadataPlan(plan); err != nil {
		return WriteMetadataStateConflict, err
	}
	state := WriteMetadataStateConflict
	err := s.withImmediateWriteMetadataTransaction(ctx, func(conn writeMetadataConnection) error {
		current, inspectErr := inspectWriteMetadataSnapshot(ctx, conn, plan)
		if inspectErr != nil {
			return inspectErr
		}
		state = classifyWriteMetadataSnapshot(plan, current)
		return nil
	})
	return state, err
}

// CommitWriteMetadata atomically applies the after-state. It is idempotent
// when the complete after-state is already present. An error matching
// ErrWriteMetadataOutcomeUnknown requires journal-directed reconciliation;
// callers must not infer that SQLite remained in the before-state.
func (s *Store) CommitWriteMetadata(ctx context.Context, plan WriteMetadataPlan) error {
	return s.reconcileWriteMetadata(ctx, plan, WriteMetadataStateAfter)
}

// EnsureWriteMetadataCommitted atomically restores the after-state during
// committed crash recovery. An error matching ErrWriteMetadataOutcomeUnknown
// requires another detached, journal-directed reconciliation attempt.
func (s *Store) EnsureWriteMetadataCommitted(ctx context.Context, plan WriteMetadataPlan) error {
	return s.reconcileWriteMetadata(ctx, plan, WriteMetadataStateAfter)
}

// RollbackWriteMetadata atomically restores the exact before-state. It is
// idempotent when that complete state is already present. An error matching
// ErrWriteMetadataOutcomeUnknown requires another detached rollback attempt
// before the mutation lease may be released.
func (s *Store) RollbackWriteMetadata(ctx context.Context, plan WriteMetadataPlan) error {
	return s.reconcileWriteMetadata(ctx, plan, WriteMetadataStateBefore)
}

func (s *Store) reconcileWriteMetadata(
	ctx context.Context,
	plan WriteMetadataPlan,
	desired WriteMetadataState,
) error {
	if s == nil || s.db == nil {
		return errors.New("version store is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if desired != WriteMetadataStateBefore && desired != WriteMetadataStateAfter {
		return fmt.Errorf("%w: unsupported desired state %q", ErrInvalidWriteMetadataPlan, desired)
	}
	if err := validateWriteMetadataPlan(plan); err != nil {
		return err
	}

	return s.withImmediateWriteMetadataTransaction(ctx, func(conn writeMetadataConnection) error {
		current, err := inspectWriteMetadataSnapshot(ctx, conn, plan)
		if err != nil {
			return err
		}
		state := classifyWriteMetadataSnapshot(plan, current)
		if state == WriteMetadataStateConflict {
			return ErrWriteMetadataConflict
		}
		if state == WriteMetadataStateBoth || state == desired {
			return nil
		}

		if desired == WriteMetadataStateAfter {
			if state != WriteMetadataStateBefore {
				return ErrWriteMetadataConflict
			}
			if err := applyWriteMetadataVersionAfter(ctx, conn, plan); err != nil {
				return err
			}
			if err := callWriteMetadataTestHook("after-version-commit-mutation"); err != nil {
				return err
			}
			if err := upsertWriteMetadataIndex(ctx, conn, plan.IndexAfter); err != nil {
				return err
			}
			if err := callWriteMetadataTestHook("after-index-commit-mutation"); err != nil {
				return err
			}
		} else {
			if state != WriteMetadataStateAfter {
				return ErrWriteMetadataConflict
			}
			if err := applyWriteMetadataVersionBefore(ctx, conn, plan); err != nil {
				return err
			}
			if err := callWriteMetadataTestHook("after-version-rollback-mutation"); err != nil {
				return err
			}
			if err := restoreWriteMetadataIndex(ctx, conn, plan); err != nil {
				return err
			}
			if err := callWriteMetadataTestHook("after-index-rollback-mutation"); err != nil {
				return err
			}
		}

		verified, err := inspectWriteMetadataSnapshot(ctx, conn, plan)
		if err != nil {
			return err
		}
		verifiedState := classifyWriteMetadataSnapshot(plan, verified)
		if verifiedState != desired && verifiedState != WriteMetadataStateBoth {
			return ErrWriteMetadataConflict
		}
		return callWriteMetadataTestHook("before-metadata-transaction-commit")
	})
}

func (s *Store) withImmediateWriteMetadataTransaction(
	ctx context.Context,
	action func(writeMetadataConnection) error,
) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The store DSN configures _txlock=immediate. Using sql.Tx preserves the
	// driver's commit cleanup and connection-pool invariants.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackErr := writeMetadataRollbackTransaction(tx)
		finished = true
		// Before this function invokes Commit, sql.ErrTxDone means the
		// transaction context already completed its automatic rollback.
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			resultErr = errors.Join(
				resultErr,
				ErrWriteMetadataOutcomeUnknown,
				fmt.Errorf("rollback write metadata transaction: %w", rollbackErr),
			)
		}
	}()
	if err := action(tx); err != nil {
		return err
	}
	if err := writeMetadataCommitTransaction(tx); err != nil {
		finished = true
		return errors.Join(
			ErrWriteMetadataOutcomeUnknown,
			fmt.Errorf("commit write metadata transaction: %w", err),
		)
	}
	finished = true
	return nil
}

func commitWriteMetadataTransaction(tx *sql.Tx) error {
	return tx.Commit()
}

func rollbackWriteMetadataTransaction(tx *sql.Tx) error {
	return tx.Rollback()
}

func inspectWriteMetadataSnapshot(
	ctx context.Context,
	conn writeMetadataConnection,
	plan WriteMetadataPlan,
) (writeMetadataSnapshot, error) {
	index, err := queryWriteMetadataIndex(ctx, conn, plan.IndexAfter.Path)
	if err != nil {
		return writeMetadataSnapshot{}, err
	}
	var version *VersionRecord
	if key := writeMetadataVersionKey(plan); key != nil {
		version, err = queryWriteMetadataVersion(ctx, conn, key.Path, key.Hash)
		if err != nil {
			return writeMetadataSnapshot{}, err
		}
	}
	return writeMetadataSnapshot{index: index, version: version}, nil
}

func classifyWriteMetadataSnapshot(
	plan WriteMetadataPlan,
	current writeMetadataSnapshot,
) WriteMetadataState {
	before := equalFileIndexRecord(plan.IndexBefore, current.index) &&
		equalVersionRecord(plan.VersionBefore, current.version)
	after := equalFileIndexRecord(&plan.IndexAfter, current.index) &&
		equalVersionRecord(plan.VersionAfter, current.version)
	switch {
	case before && after:
		return WriteMetadataStateBoth
	case before:
		return WriteMetadataStateBefore
	case after:
		return WriteMetadataStateAfter
	default:
		return WriteMetadataStateConflict
	}
}

func queryWriteMetadataIndex(
	ctx context.Context,
	conn writeMetadataConnection,
	path string,
) (*FileIndexRecord, error) {
	var record FileIndexRecord
	var contentHash sql.NullString
	err := conn.QueryRowContext(
		ctx,
		`SELECT path, size, mod_time, content_hash FROM files WHERE path = ?`,
		path,
	).Scan(&record.Path, &record.Size, &record.ModTimeUnix, &contentHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	record.ContentHash = contentHash.String
	record.ContentHashIsNull = !contentHash.Valid
	return &record, nil
}

func queryWriteMetadataVersion(
	ctx context.Context,
	conn writeMetadataConnection,
	path string,
	hash string,
) (*VersionRecord, error) {
	var record VersionRecord
	var comment sql.NullString
	err := conn.QueryRowContext(
		ctx,
		`SELECT id, path, hash, size, created_at, comment
		 FROM versions WHERE path = ? AND hash = ?`,
		path,
		hash,
	).Scan(
		&record.ID,
		&record.Path,
		&record.Hash,
		&record.Size,
		&record.CreatedAtUnix,
		&comment,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	record.Comment = comment.String
	record.CommentIsNull = !comment.Valid
	return &record, nil
}

func applyWriteMetadataVersionAfter(
	ctx context.Context,
	conn writeMetadataConnection,
	plan WriteMetadataPlan,
) error {
	if plan.VersionAfter == nil || plan.VersionBefore != nil {
		return nil
	}
	after := plan.VersionAfter
	var comment any = after.Comment
	if after.CommentIsNull {
		comment = nil
	}
	_, err := conn.ExecContext(
		ctx,
		`INSERT INTO versions (path, hash, size, created_at, comment)
		 VALUES (?, ?, ?, ?, ?)`,
		after.Path,
		after.Hash,
		after.Size,
		after.CreatedAtUnix,
		comment,
	)
	return err
}

func applyWriteMetadataVersionBefore(
	ctx context.Context,
	conn writeMetadataConnection,
	plan WriteMetadataPlan,
) error {
	if plan.VersionAfter == nil || plan.VersionBefore != nil {
		return nil
	}
	after := plan.VersionAfter
	query := `DELETE FROM versions
		 WHERE path = ? AND hash = ? AND size = ? AND created_at = ?
		   AND comment = ?`
	args := []any{
		after.Path,
		after.Hash,
		after.Size,
		after.CreatedAtUnix,
		after.Comment,
	}
	if after.CommentIsNull {
		query = `DELETE FROM versions
		 WHERE path = ? AND hash = ? AND size = ? AND created_at = ?
		   AND comment IS NULL`
		args = args[:4]
	}
	result, err := conn.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrWriteMetadataConflict
	}
	return nil
}

func upsertWriteMetadataIndex(
	ctx context.Context,
	conn writeMetadataConnection,
	record FileIndexRecord,
) error {
	var contentHash any = record.ContentHash
	if record.ContentHashIsNull {
		contentHash = nil
	}
	_, err := conn.ExecContext(
		ctx,
		`INSERT INTO files (path, size, mod_time, content_hash)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET
		   size = excluded.size,
		   mod_time = excluded.mod_time,
		   content_hash = excluded.content_hash`,
		record.Path,
		record.Size,
		record.ModTimeUnix,
		contentHash,
	)
	return err
}

func restoreWriteMetadataIndex(
	ctx context.Context,
	conn writeMetadataConnection,
	plan WriteMetadataPlan,
) error {
	if plan.IndexBefore != nil {
		return upsertWriteMetadataIndex(ctx, conn, *plan.IndexBefore)
	}
	after := plan.IndexAfter
	result, err := conn.ExecContext(
		ctx,
		`DELETE FROM files
		 WHERE path = ? AND size = ? AND mod_time = ? AND content_hash = ?`,
		after.Path,
		after.Size,
		after.ModTimeUnix,
		after.ContentHash,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrWriteMetadataConflict
	}
	return nil
}

func validateWriteMetadataPlan(plan WriteMetadataPlan) error {
	indexAfter, err := normalizeWriteMetadataIndex(plan.IndexAfter, false)
	if err != nil || indexAfter != plan.IndexAfter {
		if err == nil {
			err = errors.New("index after path is not normalized")
		}
		return errors.Join(ErrInvalidWriteMetadataPlan, err)
	}
	if plan.IndexBefore != nil {
		before, normalizeErr := normalizeWriteMetadataIndex(*plan.IndexBefore, true)
		if normalizeErr != nil || before != *plan.IndexBefore ||
			before.Path != plan.IndexAfter.Path {
			if normalizeErr == nil {
				normalizeErr = errors.New("index before does not match the normalized target path")
			}
			return errors.Join(ErrInvalidWriteMetadataPlan, normalizeErr)
		}
	}
	switch {
	case plan.VersionBefore == nil && plan.VersionAfter == nil:
		return nil
	case plan.VersionBefore == nil && plan.VersionAfter != nil:
		after, normalizeErr := normalizeWriteMetadataVersion(*plan.VersionAfter, true, false)
		if normalizeErr != nil || after != *plan.VersionAfter ||
			after.Path != plan.IndexAfter.Path || after.ID != 0 {
			if normalizeErr == nil {
				normalizeErr = errors.New("planned version insert is inconsistent")
			}
			return errors.Join(ErrInvalidWriteMetadataPlan, normalizeErr)
		}
		return nil
	case plan.VersionBefore != nil && plan.VersionAfter != nil:
		before, beforeErr := normalizeWriteMetadataVersion(*plan.VersionBefore, false, true)
		after, afterErr := normalizeWriteMetadataVersion(*plan.VersionAfter, false, true)
		if beforeErr != nil || afterErr != nil ||
			before != *plan.VersionBefore || after != *plan.VersionAfter ||
			before != after || before.Path != plan.IndexAfter.Path {
			return errors.Join(
				ErrInvalidWriteMetadataPlan,
				beforeErr,
				afterErr,
				errors.New("preserved version row is inconsistent"),
			)
		}
		return nil
	default:
		return errors.Join(
			ErrInvalidWriteMetadataPlan,
			errors.New("write metadata plan cannot remove or replace an existing version row"),
		)
	}
}

func normalizeWriteMetadataIndex(
	record FileIndexRecord,
	allowNullHash bool,
) (FileIndexRecord, error) {
	path, err := normalizeVersionStorePath(record.Path)
	if err != nil {
		return FileIndexRecord{}, errors.Join(ErrInvalidWriteMetadataPlan, err)
	}
	record.Path = path
	validHash := validWriteMetadataHash(record.ContentHash)
	if record.ContentHashIsNull {
		validHash = allowNullHash && record.ContentHash == ""
	}
	if record.Size < 0 || record.ModTimeUnix <= 0 || !validHash {
		return FileIndexRecord{}, fmt.Errorf(
			"%w: invalid file index size, timestamp, or content hash",
			ErrInvalidWriteMetadataPlan,
		)
	}
	return record, nil
}

func normalizeWriteMetadataVersion(
	record VersionRecord,
	allowUnknownID bool,
	allowNullComment bool,
) (VersionRecord, error) {
	path, err := normalizeVersionStorePath(record.Path)
	if err != nil {
		return VersionRecord{}, errors.Join(ErrInvalidWriteMetadataPlan, err)
	}
	record.Path = path
	if record.CommentIsNull && (!allowNullComment || record.Comment != "") {
		return VersionRecord{}, fmt.Errorf(
			"%w: invalid null version comment",
			ErrInvalidWriteMetadataPlan,
		)
	}
	if record.ID < 0 || (!allowUnknownID && record.ID == 0) ||
		record.Size < 0 || record.CreatedAtUnix <= 0 ||
		!validWriteMetadataHash(record.Hash) {
		return VersionRecord{}, fmt.Errorf(
			"%w: invalid version identity, size, timestamp, or hash",
			ErrInvalidWriteMetadataPlan,
		)
	}
	return record, nil
}

func validWriteMetadataHash(hash string) bool {
	if len(hash) != 64 || hash != strings.ToLower(hash) {
		return false
	}
	decoded, err := hex.DecodeString(hash)
	return err == nil && len(decoded) == 32
}

func writeMetadataVersionKey(plan WriteMetadataPlan) *VersionRecord {
	if plan.VersionAfter != nil {
		return plan.VersionAfter
	}
	return plan.VersionBefore
}

func equalFileIndexRecord(expected, actual *FileIndexRecord) bool {
	if expected == nil || actual == nil {
		return expected == nil && actual == nil
	}
	return *expected == *actual
}

func equalVersionRecord(expected, actual *VersionRecord) bool {
	if expected == nil || actual == nil {
		return expected == nil && actual == nil
	}
	if expected.Path != actual.Path ||
		expected.Hash != actual.Hash ||
		expected.Size != actual.Size ||
		expected.CreatedAtUnix != actual.CreatedAtUnix ||
		expected.Comment != actual.Comment ||
		expected.CommentIsNull != actual.CommentIsNull {
		return false
	}
	if expected.ID == 0 {
		// A prepared journal cannot know SQLite's autoincrement result. Any
		// positive ID is equivalent while the upper mutation lease excludes
		// uncoordinated metadata writers.
		return actual.ID > 0
	}
	return expected.ID == actual.ID
}

func cloneFileIndexRecord(record *FileIndexRecord) *FileIndexRecord {
	if record == nil {
		return nil
	}
	cloned := *record
	return &cloned
}

func cloneVersionRecord(record *VersionRecord) *VersionRecord {
	if record == nil {
		return nil
	}
	cloned := *record
	return &cloned
}

func callWriteMetadataTestHook(stage string) error {
	if writeMetadataTestHook == nil {
		return nil
	}
	return writeMetadataTestHook(stage)
}
