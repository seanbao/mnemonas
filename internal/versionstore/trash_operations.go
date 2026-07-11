package versionstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	sqlite3 "modernc.org/sqlite/lib"
)

const (
	// TrashOperationKindDeleteToTrash identifies a committed delete-to-Trash mutation.
	TrashOperationKindDeleteToTrash = "delete_to_trash"
	// TrashOperationKindRestoreFromTrash identifies a committed restore-from-Trash mutation.
	TrashOperationKindRestoreFromTrash = "restore_from_trash"
)

var (
	// ErrTrashOperationConflict reports conflicting reuse of an operation or Trash ID.
	ErrTrashOperationConflict = errors.New("trash operation conflict")
	// ErrTrashItemMismatch reports that a restore request does not match the persisted Trash item.
	ErrTrashItemMismatch     = errors.New("trash item mismatch")
	errInvalidTrashOperation = errors.New("invalid trash operation")
)

// TrashOperation is a durable participant outbox entry for a Trash transaction.
type TrashOperation struct {
	ID                 string `json:"id"`
	Kind               string `json:"kind"`
	TrashID            string `json:"trash_id"`
	JournalHash        string `json:"journal_hash"`
	ParticipantPayload []byte `json:"participant_payload"`
}

// FileIndexEntry contains one complete file index row restored by a Trash transaction.
type FileIndexEntry struct {
	Path        string    `json:"path"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time"`
	ContentHash string    `json:"content_hash"`
}

// CommitTrashDelete atomically records a complete Trash item, removes the source
// file index tree, and publishes its participant outbox operation.
func (s *Store) CommitTrashDelete(ctx context.Context, item *TrashItem, operation *TrashOperation) error {
	normalizedItem, err := cloneAndNormalizeTrashItem(item)
	if err != nil {
		return err
	}
	normalizedOperation, err := cloneAndValidateTrashOperation(operation, TrashOperationKindDeleteToTrash, normalizedItem.ID)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	idempotent, err := checkTrashOperationIdempotency(ctx, tx, normalizedOperation)
	if err != nil {
		return err
	}
	if idempotent {
		return nil
	}

	var trashItemExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trash WHERE id = ?)`, normalizedItem.ID).Scan(&trashItemExists); err != nil {
		return err
	}
	if trashItemExists {
		return ErrAlreadyExists
	}

	if err := insertTrashItemTx(ctx, tx, normalizedItem); err != nil {
		return err
	}
	if err := deleteFileIndexTreeTx(ctx, tx, normalizedItem.OriginalPath); err != nil {
		return err
	}
	if err := insertTrashOperationTx(ctx, tx, normalizedOperation); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// CommitTrashRestore atomically removes an exactly matching Trash item, replaces
// the destination file index tree, optionally relocates historical path metadata,
// and publishes its participant outbox operation.
func (s *Store) CommitTrashRestore(
	ctx context.Context,
	item *TrashItem,
	destinationPath string,
	fileIndex []FileIndexEntry,
	renameHistory bool,
	operation *TrashOperation,
) error {
	normalizedItem, err := cloneAndNormalizeTrashItem(item)
	if err != nil {
		return err
	}
	destinationPath, err = normalizeVersionStorePath(destinationPath)
	if err != nil {
		return err
	}
	normalizedIndex, err := cloneAndNormalizeFileIndex(fileIndex, destinationPath)
	if err != nil {
		return err
	}
	normalizedOperation, err := cloneAndValidateTrashOperation(operation, TrashOperationKindRestoreFromTrash, normalizedItem.ID)
	if err != nil {
		return err
	}

	s.fileLocksMu.Lock()
	defer s.fileLocksMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	idempotent, err := checkTrashOperationIdempotency(ctx, tx, normalizedOperation)
	if err != nil {
		return err
	}
	if idempotent {
		return nil
	}

	if renameHistory && normalizedItem.OriginalPath != destinationPath {
		conflict, err := hasTrashRestoreHistoryConflict(ctx, tx, normalizedItem.OriginalPath, destinationPath)
		if err != nil {
			return err
		}
		if conflict {
			return ErrAlreadyExists
		}
	}

	matched, err := deleteMatchingTrashItemTx(ctx, tx, normalizedItem)
	if err != nil {
		return err
	}
	if !matched {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trash WHERE id = ?)`, normalizedItem.ID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrTrashItemMismatch
		}
		return ErrNotFound
	}

	if err := replaceFileIndexTreeTx(ctx, tx, destinationPath, normalizedIndex); err != nil {
		return err
	}
	if renameHistory && normalizedItem.OriginalPath != destinationPath {
		for _, table := range []string{"versions", "versioning_overrides", "file_locks"} {
			if err := renameTrashRestorePathInTable(ctx, tx, table, normalizedItem.OriginalPath, destinationPath); err != nil {
				return err
			}
		}
	}
	if err := insertTrashOperationTx(ctx, tx, normalizedOperation); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// ListTrashOperations returns all pending Trash participant outbox entries.
func (s *Store) ListTrashOperations(ctx context.Context) ([]TrashOperation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT operation_id, kind, trash_id, journal_hash, participant_payload
		 FROM trash_operations ORDER BY rowid ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	operations := make([]TrashOperation, 0)
	for rows.Next() {
		operation, err := scanTrashOperation(rows)
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return operations, nil
}

// GetTrashOperation returns one pending Trash participant outbox entry.
func (s *Store) GetTrashOperation(ctx context.Context, id string) (*TrashOperation, error) {
	if !isFixedHex(id, 32) {
		return nil, fmt.Errorf("%w: invalid operation ID", errInvalidTrashOperation)
	}
	operation, err := getTrashOperation(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	return &operation, nil
}

// CompleteTrashOperation removes an outbox entry only when both its operation ID
// and journal hash match.
func (s *Store) CompleteTrashOperation(ctx context.Context, id, journalHash string) error {
	if !isFixedHex(id, 32) {
		return fmt.Errorf("%w: invalid operation ID", errInvalidTrashOperation)
	}
	if !isFixedHex(journalHash, 64) {
		return fmt.Errorf("%w: invalid journal hash", errInvalidTrashOperation)
	}
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM trash_operations WHERE operation_id = ? AND journal_hash = ?`,
		id, journalHash)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func cloneAndNormalizeTrashItem(item *TrashItem) (TrashItem, error) {
	if item == nil {
		return TrashItem{}, errors.New("trash item is required")
	}
	cloned := *item
	if err := normalizeTrashItemFromDB(&cloned); err != nil {
		return TrashItem{}, err
	}
	cloned.RestoreData = cloneNonNilBytes(cloned.RestoreData)
	return cloned, nil
}

func cloneAndValidateTrashOperation(operation *TrashOperation, expectedKind, trashID string) (TrashOperation, error) {
	if operation == nil {
		return TrashOperation{}, fmt.Errorf("%w: operation is required", errInvalidTrashOperation)
	}
	cloned := *operation
	if cloned.ParticipantPayload == nil {
		return TrashOperation{}, fmt.Errorf("%w: participant payload is required", errInvalidTrashOperation)
	}
	cloned.ParticipantPayload = cloneNonNilBytes(cloned.ParticipantPayload)
	if err := validateTrashOperation(cloned); err != nil {
		return TrashOperation{}, err
	}
	if cloned.Kind != expectedKind {
		return TrashOperation{}, fmt.Errorf("%w: unexpected operation kind %q", errInvalidTrashOperation, cloned.Kind)
	}
	if cloned.TrashID != trashID {
		return TrashOperation{}, fmt.Errorf("%w: operation Trash ID does not match item", errInvalidTrashOperation)
	}
	return cloned, nil
}

func validateTrashOperation(operation TrashOperation) error {
	if !isFixedHex(operation.ID, 32) {
		return fmt.Errorf("%w: invalid operation ID", errInvalidTrashOperation)
	}
	if operation.Kind != TrashOperationKindDeleteToTrash && operation.Kind != TrashOperationKindRestoreFromTrash {
		return fmt.Errorf("%w: invalid kind %q", errInvalidTrashOperation, operation.Kind)
	}
	if !isValidStoreID(operation.TrashID) {
		return fmt.Errorf("%w: invalid Trash ID", errInvalidTrashOperation)
	}
	if !isFixedHex(operation.JournalHash, 64) {
		return fmt.Errorf("%w: invalid journal hash", errInvalidTrashOperation)
	}
	if operation.ParticipantPayload == nil {
		return fmt.Errorf("%w: participant payload is required", errInvalidTrashOperation)
	}
	return nil
}

func isFixedHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}

func cloneNonNilBytes(value []byte) []byte {
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}

func trashOperationsEqual(left, right TrashOperation) bool {
	return left.ID == right.ID &&
		left.Kind == right.Kind &&
		left.TrashID == right.TrashID &&
		left.JournalHash == right.JournalHash &&
		bytes.Equal(left.ParticipantPayload, right.ParticipantPayload)
}

func checkTrashOperationIdempotency(ctx context.Context, tx *sql.Tx, operation TrashOperation) (bool, error) {
	existing, err := getTrashOperation(ctx, tx, operation.ID)
	if err == nil {
		if !trashOperationsEqual(existing, operation) {
			return false, fmt.Errorf("%w: operation ID %q has different fields", ErrTrashOperationConflict, operation.ID)
		}
		return true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return false, err
	}

	existing, err = getTrashOperationByTrashID(ctx, tx, operation.TrashID)
	if err == nil {
		return false, fmt.Errorf(
			"%w: Trash ID %q already belongs to operation %q",
			ErrTrashOperationConflict,
			operation.TrashID,
			existing.ID,
		)
	}
	if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	return false, nil
}

type trashOperationQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type trashOperationScanner interface {
	Scan(...any) error
}

func getTrashOperation(ctx context.Context, queryer trashOperationQueryRower, id string) (TrashOperation, error) {
	return scanTrashOperation(queryer.QueryRowContext(ctx,
		`SELECT operation_id, kind, trash_id, journal_hash, participant_payload
		 FROM trash_operations WHERE operation_id = ?`, id))
}

func getTrashOperationByTrashID(ctx context.Context, queryer trashOperationQueryRower, trashID string) (TrashOperation, error) {
	return scanTrashOperation(queryer.QueryRowContext(ctx,
		`SELECT operation_id, kind, trash_id, journal_hash, participant_payload
		 FROM trash_operations WHERE trash_id = ?`, trashID))
}

func scanTrashOperation(scanner trashOperationScanner) (TrashOperation, error) {
	var operation TrashOperation
	if err := scanner.Scan(
		&operation.ID,
		&operation.Kind,
		&operation.TrashID,
		&operation.JournalHash,
		&operation.ParticipantPayload,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TrashOperation{}, ErrNotFound
		}
		return TrashOperation{}, err
	}
	operation.ParticipantPayload = cloneNonNilBytes(operation.ParticipantPayload)
	if err := validateTrashOperation(operation); err != nil {
		return TrashOperation{}, err
	}
	return operation, nil
}

func insertTrashOperationTx(ctx context.Context, tx *sql.Tx, operation TrashOperation) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO trash_operations (operation_id, kind, trash_id, journal_hash, participant_payload)
		 VALUES (?, ?, ?, ?, ?)`,
		operation.ID,
		operation.Kind,
		operation.TrashID,
		operation.JournalHash,
		operation.ParticipantPayload,
	)
	if isSQLiteUniqueConstraintError(err) {
		return fmt.Errorf("%w: %v", ErrTrashOperationConflict, err)
	}
	return err
}

type sqliteErrorCoder interface {
	Code() int
}

func isSQLiteUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr sqliteErrorCoder
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY ||
		sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

func insertTrashItemTx(ctx context.Context, tx *sql.Tx, item TrashItem) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO trash (id, original_path, size, deleted_at, expires_at, is_dir, had_versions, restore_data)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		item.OriginalPath,
		item.Size,
		item.DeletedAt.Unix(),
		item.ExpiresAt.Unix(),
		item.IsDir,
		item.HadVersions,
		item.RestoreData,
	)
	return err
}

func deleteMatchingTrashItemTx(ctx context.Context, tx *sql.Tx, item TrashItem) (bool, error) {
	result, err := tx.ExecContext(ctx,
		`DELETE FROM trash
		 WHERE id = ? AND original_path = ? AND size = ? AND deleted_at = ? AND expires_at = ?
		   AND is_dir = ? AND had_versions = ? AND restore_data = ?`,
		item.ID,
		item.OriginalPath,
		item.Size,
		item.DeletedAt.Unix(),
		item.ExpiresAt.Unix(),
		item.IsDir,
		item.HadVersions,
		item.RestoreData,
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

func cloneAndNormalizeFileIndex(entries []FileIndexEntry, destinationPath string) ([]FileIndexEntry, error) {
	normalized := make([]FileIndexEntry, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for index, entry := range entries {
		cleanPath, err := normalizeVersionStorePath(entry.Path)
		if err != nil {
			return nil, err
		}
		if !pathAtOrBelow(cleanPath, destinationPath) {
			return nil, fmt.Errorf("file index path %q is outside destination %q", cleanPath, destinationPath)
		}
		if _, exists := seen[cleanPath]; exists {
			return nil, fmt.Errorf("duplicate file index path %q", cleanPath)
		}
		seen[cleanPath] = struct{}{}
		entry.Path = cleanPath
		normalized[index] = entry
	}
	return normalized, nil
}

func pathAtOrBelow(candidate, root string) bool {
	if root == "/" {
		return true
	}
	return candidate == root || strings.HasPrefix(candidate, root+"/")
}

func deleteFileIndexTreeTx(ctx context.Context, tx *sql.Tx, root string) error {
	if root == "/" {
		_, err := tx.ExecContext(ctx, `DELETE FROM files`)
		return err
	}
	prefix := root + "/"
	_, err := tx.ExecContext(ctx,
		`DELETE FROM files WHERE path = ? OR (path >= ? AND path < ?)`,
		root,
		prefix,
		prefix+"\uffff",
	)
	return err
}

func replaceFileIndexTreeTx(ctx context.Context, tx *sql.Tx, destinationPath string, entries []FileIndexEntry) error {
	if err := deleteFileIndexTreeTx(ctx, tx, destinationPath); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	statement, err := tx.PrepareContext(ctx,
		`INSERT INTO files (path, size, mod_time, content_hash) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer statement.Close()
	for _, entry := range entries {
		if _, err := statement.ExecContext(ctx, entry.Path, entry.Size, entry.ModTime.Unix(), entry.ContentHash); err != nil {
			return err
		}
	}
	return nil
}

func hasTrashRestoreHistoryConflict(ctx context.Context, tx *sql.Tx, sourcePath, destinationPath string) (bool, error) {
	for _, table := range []string{"versions", "versioning_overrides", "file_locks"} {
		targetCondition, targetArgs := pathTreeSQLCondition(destinationPath)
		sourceCondition, sourceArgs := pathTreeSQLCondition(sourcePath)
		query := fmt.Sprintf(
			`SELECT EXISTS(SELECT 1 FROM %s WHERE (%s) AND NOT (%s) LIMIT 1)`,
			table,
			targetCondition,
			sourceCondition,
		)
		args := append(targetArgs, sourceArgs...)
		var exists bool
		if err := tx.QueryRowContext(ctx, query, args...).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}

func pathTreeSQLCondition(root string) (string, []any) {
	if root == "/" {
		return `1 = 1`, nil
	}
	prefix := root + "/"
	return `path = ? OR substr(path, 1, ?) = ?`, []any{root, len(prefix), prefix}
}

func renameTrashRestorePathInTable(ctx context.Context, tx *sql.Tx, table, oldPath, newPath string) error {
	if oldPath == newPath {
		return nil
	}
	if oldPath == "/" {
		_, err := tx.ExecContext(ctx,
			fmt.Sprintf(`UPDATE %s SET path = CASE WHEN path = '/' THEN ? ELSE ? || path END`, table),
			newPath,
			newPath,
		)
		return err
	}

	prefix := oldPath + "/"
	prefixLen := len(prefix)
	newDescendantBase := newPath
	if newPath == "/" {
		newDescendantBase = ""
	}
	_, err := tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s
		 SET path = CASE WHEN path = ? THEN ? ELSE ? || substr(path, ?) END
		 WHERE path = ? OR substr(path, 1, ?) = ?`, table),
		oldPath,
		newPath,
		newDescendantBase,
		prefixLen,
		oldPath,
		prefixLen,
		prefix,
	)
	return err
}
