// Package versionstore provides SQLite-based version management for MnemoNAS
package versionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/rootio"
)

// Common errors
var (
	ErrNotFound            = errors.New("not found")
	ErrAlreadyExists       = errors.New("already exists")
	ErrFileLocked          = errors.New("file is locked")
	ErrLockExpired         = errors.New("lock has expired")
	ErrUnavailable         = errors.New("version object store unavailable")
	errInvalidStorePath    = errors.New("invalid path")
	errInvalidStoreID      = errors.New("invalid ID")
	errVersionStoreSymlink = errors.New("version store path must not traverse a symlink")
)

const sqliteDriverName = "sqlite"

var syncVersionStoreDir = syncVersionStoreDirectory
var afterValidateVersionStorePath = func() {}

// LockType defines the type of lock
type LockType int

const (
	ReadLock  LockType = iota // Shared lock, allows concurrent reads
	WriteLock                 // Exclusive lock, blocks all other access
)

// FileLock represents a file lock
type FileLock struct {
	Path      string    `json:"path"`
	Holder    string    `json:"holder"`
	LockType  LockType  `json:"lock_type"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// TrashItem represents a deleted file in trash
type TrashItem struct {
	ID           string    `json:"id"`
	OriginalPath string    `json:"original_path"`
	Size         int64     `json:"size"`
	DeletedAt    time.Time `json:"deleted_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	IsDir        bool      `json:"is_dir"`
	HadVersions  bool      `json:"had_versions"`
	RestoreData  []byte    `json:"-"`
}

// Version represents a file version
type Version struct {
	ID        int64     `json:"id"`
	Path      string    `json:"path"`
	Hash      string    `json:"hash"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	Comment   string    `json:"comment,omitempty"`
}

// Store is the SQLite-based version store
type Store struct {
	db                      *sql.DB
	objects                 *ObjectStore
	dbDirHandle             *os.File
	fileLocksMu             sync.Mutex
	recoveredFromCorruption bool

	getChunkRefSizeFn func(ctx context.Context, hash string) (int64, error)
	deleteObjectFn    func(ctx context.Context, hash string) error
	deleteChunkRefFn  func(ctx context.Context, chunkHash string) error
}

// Config holds store configuration
type Config struct {
	DBPath    string            // Path to SQLite database file
	Dataplane *dataplane.Client // Rust dataplane client (required)
}

// New creates a new version store
func New(cfg Config) (*Store, error) {
	if cfg.Dataplane == nil {
		return nil, errors.New("dataplane client is required")
	}

	normalizedPath, err := normalizeVersionStoreFilePath(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := validateVersionStoreFilePath(normalizedPath); err != nil {
		return nil, err
	}

	// Ensure database directory exists
	dbDir := filepath.Dir(normalizedPath)
	if err := ensureVersionStoreDir(dbDir, 0700); err != nil {
		return nil, err
	}

	anchoredDBPath, dirHandle, err := prepareAnchoredVersionStorePath(normalizedPath)
	if err != nil {
		return nil, err
	}

	// Open database
	db, err := sql.Open(sqliteDriverName, versionStoreSQLiteDSN(anchoredDBPath))
	if err != nil {
		_ = dirHandle.Close()
		return nil, err
	}

	// Create tables
	recoveredFromCorruption := false
	if err := createTables(db); err != nil {
		_ = db.Close()
		_ = dirHandle.Close()
		if recoverErr := recoverCorruptVersionStoreDatabase(normalizedPath, err); recoverErr != nil {
			return nil, errors.Join(
				fmt.Errorf("initialize version store schema: %w", err),
				fmt.Errorf("recover corrupt version store database: %w", recoverErr),
			)
		}

		anchoredDBPath, dirHandle, err = prepareAnchoredVersionStorePath(normalizedPath)
		if err != nil {
			return nil, err
		}
		db, err = sql.Open(sqliteDriverName, versionStoreSQLiteDSN(anchoredDBPath))
		if err != nil {
			_ = dirHandle.Close()
			return nil, err
		}
		if err := createTables(db); err != nil {
			_ = db.Close()
			_ = dirHandle.Close()
			return nil, err
		}
		recoveredFromCorruption = true
	}

	store := &Store{
		db:                      db,
		objects:                 NewObjectStore(cfg.Dataplane),
		dbDirHandle:             dirHandle,
		recoveredFromCorruption: recoveredFromCorruption,
	}
	store.getChunkRefSizeFn = func(ctx context.Context, hash string) (int64, error) {
		var size int64
		if err := store.db.QueryRowContext(ctx, `SELECT size FROM chunk_refs WHERE hash = ?`, hash).Scan(&size); err != nil {
			return 0, err
		}
		return size, nil
	}
	store.deleteObjectFn = func(ctx context.Context, hash string) error {
		return store.DeleteObject(ctx, hash)
	}
	store.deleteChunkRefFn = func(ctx context.Context, chunkHash string) error {
		return store.DeleteChunkRef(ctx, chunkHash)
	}

	return store, nil
}

// RecoveredFromCorruption reports whether this store instance replaced a
// corrupt database during construction.
func (s *Store) RecoveredFromCorruption() bool {
	return s != nil && s.recoveredFromCorruption
}

func recoverCorruptVersionStoreDatabase(dbPath string, initErr error) error {
	if !isRecoverableVersionStoreInitError(initErr) {
		return initErr
	}

	root, err := os.OpenRoot(filepath.Dir(dbPath))
	if err != nil {
		return fmt.Errorf("open version store directory root: %w", err)
	}
	defer root.Close()

	base := filepath.Base(dbPath)
	backupBase := fmt.Sprintf("%s.corrupt.%d", base, time.Now().UnixNano())
	renamed := make([][2]string, 0, 3)

	if err := root.Rename(base, backupBase); err != nil {
		return fmt.Errorf("backup corrupt version store database: %w", err)
	}
	renamed = append(renamed, [2]string{base, backupBase})

	for _, sidecar := range []string{"-wal", "-shm"} {
		oldName := base + sidecar
		newName := backupBase + sidecar
		if err := root.Rename(oldName, newName); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if rollbackErr := rollbackRenamedVersionStoreFiles(root, renamed); rollbackErr != nil {
				return errors.Join(
					fmt.Errorf("backup corrupt version store sidecar %s: %w", oldName, err),
					fmt.Errorf("rollback corrupt version store backup: %w", rollbackErr),
				)
			}
			if rollbackSyncErr := syncVersionStoreDir(filepath.Dir(dbPath)); rollbackSyncErr != nil {
				return errors.Join(
					fmt.Errorf("backup corrupt version store sidecar %s: %w", oldName, err),
					fmt.Errorf("sync corrupt version store rollback: %w", rollbackSyncErr),
				)
			}
			return fmt.Errorf("backup corrupt version store sidecar %s: %w", oldName, err)
		}
		renamed = append(renamed, [2]string{oldName, newName})
	}

	if err := syncVersionStoreDir(filepath.Dir(dbPath)); err != nil {
		if rollbackErr := rollbackRenamedVersionStoreFiles(root, renamed); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt version store directory: %w", err),
				fmt.Errorf("rollback corrupt version store backup: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncVersionStoreDir(filepath.Dir(dbPath)); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt version store directory: %w", err),
				fmt.Errorf("sync corrupt version store rollback: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("sync corrupt version store directory: %w", err)
	}

	return nil
}

func rollbackRenamedVersionStoreFiles(root *os.Root, renamed [][2]string) error {
	var rollbackErr error
	for i := len(renamed) - 1; i >= 0; i-- {
		if err := root.Rename(renamed[i][1], renamed[i][0]); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	return rollbackErr
}

func isRecoverableVersionStoreSQLiteError(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database disk image is malformed") ||
		strings.Contains(message, "file is not a database") ||
		strings.Contains(message, "not a database")
}

func isRecoverableVersionStoreInitError(err error) bool {
	return isRecoverableVersionStoreSQLiteError(err)
}

func normalizeVersionStoreFilePath(dbPath string) (string, error) {
	cleaned := filepath.Clean(dbPath)
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve version store path: %w", err)
	}
	return absPath, nil
}

func validateVersionStoreFilePath(path string) error {
	root := filepath.VolumeName(path) + string(filepath.Separator)
	current := root
	trimmed := strings.TrimPrefix(path, root)
	if trimmed == "" {
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("failed to stat version store path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errVersionStoreSymlink
		}
		return nil
	}

	for _, part := range strings.Split(trimmed, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("failed to stat version store path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errVersionStoreSymlink
		}
	}

	return nil
}

func prepareAnchoredVersionStorePath(dbPath string) (string, *os.File, error) {
	dbDir := filepath.Dir(dbPath)
	root, err := os.OpenRoot(dbDir)
	if err != nil {
		return "", nil, fmt.Errorf("failed to open version store directory root: %w", err)
	}
	defer root.Close()

	dirHandle, err := root.Open(".")
	if err != nil {
		return "", nil, fmt.Errorf("failed to anchor version store directory: %w", err)
	}

	afterValidateVersionStorePath()
	if err := rejectAnchoredVersionStoreFileSymlink(root, filepath.Base(dbPath)); err != nil {
		_ = dirHandle.Close()
		return "", nil, err
	}
	return anchoredFDPath(dirHandle.Fd(), filepath.Base(dbPath)), dirHandle, nil
}

func rejectAnchoredVersionStoreFileSymlink(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to stat anchored version store path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errVersionStoreSymlink
	}
	if info.IsDir() {
		return errInvalidStorePath
	}
	return nil
}

func versionStoreSQLiteDSN(path string) string {
	return path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
}

func anchoredFDPath(fd uintptr, name string) string {
	fdRoot := "/proc/self/fd"
	if runtime.GOOS == "darwin" {
		fdRoot = "/dev/fd"
	}
	return fmt.Sprintf("%s/%d/%s", fdRoot, fd, name)
}

func syncCreatedVersionStoreDirs(createdDirs []string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncVersionStoreDir(filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync version store directory tree: %w", err)
		}
	}
	return nil
}

func ensureVersionStoreDir(dir string, perm os.FileMode) error {
	createdDirs, err := mkdirAllVersionStoreDirNoFollowTracked(dir, perm)
	if err != nil {
		return err
	}
	return syncCreatedVersionStoreDirs(createdDirs)
}

func mkdirAllVersionStoreDirNoFollowTracked(dir string, perm os.FileMode) ([]string, error) {
	createdDirs, err := rootio.MkdirAllPathNoFollowTracked(dir, perm)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return createdDirs, errVersionStoreSymlink
		}
		return createdDirs, err
	}
	return createdDirs, nil
}

func syncVersionStoreDirectory(dir string) error {
	dirHandle, err := rootio.OpenDirPathNoFollow(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func normalizeVersionStorePath(rawPath string) (string, error) {
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	if strings.IndexFunc(normalized, unicode.IsControl) >= 0 {
		return "", errInvalidStorePath
	}
	if normalized == "" {
		return "", errInvalidStorePath
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "." || segment == ".." {
			return "", errInvalidStorePath
		}
	}
	return path.Clean("/" + normalized), nil
}

func isValidStoreID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		b := id[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == '_' {
			continue
		}
		return false
	}
	return true
}

func normalizeTrashItemFromDB(item *TrashItem) error {
	if item == nil || !isValidStoreID(item.ID) {
		return errInvalidStoreID
	}
	cleanOriginalPath, err := normalizeVersionStorePath(item.OriginalPath)
	if err != nil {
		return err
	}
	item.OriginalPath = cleanOriginalPath
	item.RestoreData = normalizeTrashRestoreData(item.RestoreData)
	return nil
}

// SetDataplaneClient swaps the dataplane client used by the backing object store.
func (s *Store) SetDataplaneClient(client *dataplane.Client) {
	if s == nil || s.objects == nil {
		return
	}
	s.objects.SetClient(client)
}

func createTables(db *sql.DB) error {
	schema := `
	-- File index for fast lookups
	CREATE TABLE IF NOT EXISTS files (
		path TEXT PRIMARY KEY,
		size INTEGER NOT NULL,
		mod_time INTEGER NOT NULL,
		content_hash TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_files_hash ON files(content_hash);

	-- Versioning override settings
	CREATE TABLE IF NOT EXISTS versioning_overrides (
		path TEXT PRIMARY KEY,
		enabled BOOLEAN NOT NULL,
		created_at INTEGER NOT NULL
	);

	-- Version history
	CREATE TABLE IF NOT EXISTS versions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		path TEXT NOT NULL,
		hash TEXT NOT NULL,
		size INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		comment TEXT,
		UNIQUE(path, hash)
	);
	CREATE INDEX IF NOT EXISTS idx_versions_path ON versions(path);
	CREATE INDEX IF NOT EXISTS idx_versions_created ON versions(created_at);

	-- Chunk reference counting for GC
	-- Each version's manifest_hash is stored in versions.hash
	-- The actual chunk hashes are stored in this table
	CREATE TABLE IF NOT EXISTS chunk_refs (
		hash TEXT PRIMARY KEY,
		ref_count INTEGER NOT NULL DEFAULT 1,
		size INTEGER NOT NULL,
		created_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_chunk_refs_count ON chunk_refs(ref_count);

	-- Mapping from version to its chunks (for GC reference counting)
	CREATE TABLE IF NOT EXISTS version_chunks (
		version_id INTEGER NOT NULL,
		chunk_hash TEXT NOT NULL,
		PRIMARY KEY (version_id, chunk_hash),
		FOREIGN KEY (version_id) REFERENCES versions(id) ON DELETE CASCADE,
		FOREIGN KEY (chunk_hash) REFERENCES chunk_refs(hash)
	);

	-- Trash
	CREATE TABLE IF NOT EXISTS trash (
		id TEXT PRIMARY KEY,
		original_path TEXT NOT NULL,
		size INTEGER NOT NULL,
		deleted_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		is_dir BOOLEAN NOT NULL,
		had_versions BOOLEAN NOT NULL,
		restore_data BLOB NOT NULL DEFAULT x''
	);
	CREATE INDEX IF NOT EXISTS idx_trash_expires ON trash(expires_at);

	-- Durable outbox for trash transaction participants
	CREATE TABLE IF NOT EXISTS trash_operations (
		operation_id TEXT PRIMARY KEY
			CHECK(length(operation_id) = 32 AND operation_id NOT GLOB '*[^0-9A-Fa-f]*'),
		kind TEXT NOT NULL
			CHECK(kind IN ('delete_to_trash', 'restore_from_trash')),
		trash_id TEXT NOT NULL UNIQUE,
		journal_hash TEXT NOT NULL
			CHECK(length(journal_hash) = 64 AND journal_hash NOT GLOB '*[^0-9A-Fa-f]*'),
		participant_payload BLOB NOT NULL
	);
	-- File locks
	CREATE TABLE IF NOT EXISTS file_locks (
		path TEXT PRIMARY KEY,
		holder TEXT NOT NULL,
		lock_type INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		created_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_locks_expires ON file_locks(expires_at);
	`

	_, err := db.Exec(schema)
	if err != nil {
		return err
	}
	if err := ensureTrashTableSchema(db); err != nil {
		return err
	}
	return ensureFileLocksTableSchema(db)
}

func ensureTrashTableSchema(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(trash)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	hasRestoreData := false
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == "restore_data" {
			hasRestoreData = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasRestoreData {
		return nil
	}

	_, err = db.Exec(`ALTER TABLE trash ADD COLUMN restore_data BLOB NOT NULL DEFAULT x''`)
	return err
}

func ensureFileLocksTableSchema(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(file_locks)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	hasPathPK := false
	hasHolderPK := false
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == "path" && pk > 0 {
			hasPathPK = true
		}
		if name == "holder" && pk > 0 {
			hasHolderPK = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasPathPK && hasHolderPK {
		_, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_locks_path ON file_locks(path)`)
		return err
	}

	_, err = db.Exec(`
		DROP TABLE IF EXISTS file_locks;
		CREATE TABLE file_locks (
			path TEXT NOT NULL,
			holder TEXT NOT NULL,
			lock_type INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (path, holder)
		);
		CREATE INDEX IF NOT EXISTS idx_locks_expires ON file_locks(expires_at);
		CREATE INDEX IF NOT EXISTS idx_locks_path ON file_locks(path);
	`)
	return err
}

// Close closes the store
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	err := s.db.Close()
	if s.dbDirHandle != nil {
		if closeErr := s.dbDirHandle.Close(); err == nil {
			err = closeErr
		}
		s.dbDirHandle = nil
	}
	return err
}

// ============================================================================
// Version Operations
// ============================================================================

// AddVersion adds a new version record
func (s *Store) AddVersion(ctx context.Context, path, hash string, size int64, comment string) error {
	path, err := normalizeVersionStorePath(path)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO versions (path, hash, size, created_at, comment) VALUES (?, ?, ?, ?, ?)`,
		path, hash, size, time.Now().Unix(), comment)
	return err
}

// GetVersions returns all versions of a file, newest first
func (s *Store) GetVersions(ctx context.Context, path string) ([]Version, error) {
	path, err := normalizeVersionStorePath(path)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, path, hash, size, created_at, COALESCE(comment, '') 
		 FROM versions WHERE path = ? ORDER BY created_at DESC, id DESC`,
		path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []Version
	for rows.Next() {
		var v Version
		var createdAt int64
		if err := rows.Scan(&v.ID, &v.Path, &v.Hash, &v.Size, &createdAt, &v.Comment); err != nil {
			return nil, err
		}
		v.CreatedAt = time.Unix(createdAt, 0)
		versions = append(versions, v)
	}

	return versions, rows.Err()
}

// GetVersion returns a specific version by hash
func (s *Store) GetVersion(ctx context.Context, path, hash string) (*Version, error) {
	path, err := normalizeVersionStorePath(path)
	if err != nil {
		return nil, err
	}
	var v Version
	var createdAt int64

	err = s.db.QueryRowContext(ctx,
		`SELECT id, path, hash, size, created_at, COALESCE(comment, '') 
		 FROM versions WHERE path = ? AND hash = ?`,
		path, hash).Scan(&v.ID, &v.Path, &v.Hash, &v.Size, &createdAt, &v.Comment)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	v.CreatedAt = time.Unix(createdAt, 0)
	return &v, nil
}

// DeleteVersions deletes all versions of a file
func (s *Store) DeleteVersions(ctx context.Context, path string) error {
	path, err := normalizeVersionStorePath(path)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM versions WHERE path = ?`, path)
	return err
}

// DeleteVersion deletes a specific version record for a file.
func (s *Store) DeleteVersion(ctx context.Context, path, hash string) error {
	path, err := normalizeVersionStorePath(path)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM versions WHERE path = ? AND hash = ?`, path, hash)
	return err
}

// DeleteOldVersions deletes versions older than maxAge or exceeding maxCount
func (s *Store) DeleteOldVersions(ctx context.Context, path string, maxCount int, maxAge time.Duration) ([]string, error) {
	versions, err := s.DeleteOldVersionsDetailed(ctx, path, maxCount, maxAge)
	if err != nil {
		return nil, err
	}

	hashes := make([]string, 0, len(versions))
	for _, version := range versions {
		hashes = append(hashes, version.Hash)
	}
	return hashes, nil
}

// DeleteOldVersionsDetailed deletes versions older than maxAge or exceeding maxCount
// and returns the full deleted version records for callers that may need rollback.
func (s *Store) DeleteOldVersionsDetailed(ctx context.Context, path string, maxCount int, maxAge time.Duration) ([]Version, error) {
	path, err := normalizeVersionStorePath(path)
	if err != nil {
		return nil, err
	}
	conditions := make([]string, 0, 2)
	args := []any{path}

	if maxAge > 0 {
		conditions = append(conditions, "created_at < ?")
		args = append(args, time.Now().Add(-maxAge).Unix())
	}
	if maxCount > 0 {
		conditions = append(conditions, "id NOT IN (SELECT id FROM versions WHERE path = ? ORDER BY created_at DESC, id DESC LIMIT ?)")
		args = append(args, path, maxCount)
	}
	if len(conditions) == 0 {
		return nil, nil
	}

	whereClause := strings.Join(conditions, " OR ")

	// Get version rows to delete so callers can restore metadata if a later cleanup step fails.
	selectQuery := fmt.Sprintf(`SELECT id, path, hash, size, created_at, COALESCE(comment, '') FROM versions WHERE path = ? AND (%s)`, whereClause)
	rows, err := s.db.QueryContext(ctx, selectQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []Version
	for rows.Next() {
		var version Version
		var createdAt int64
		if err := rows.Scan(&version.ID, &version.Path, &version.Hash, &version.Size, &createdAt, &version.Comment); err != nil {
			return nil, err
		}
		version.CreatedAt = time.Unix(createdAt, 0)
		versions = append(versions, version)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Delete the records
	deleteQuery := fmt.Sprintf(`DELETE FROM versions WHERE path = ? AND (%s)`, whereClause)
	_, err = s.db.ExecContext(ctx, deleteQuery, args...)

	return versions, err
}

// RestoreVersions restores previously deleted version metadata rows.
func (s *Store) RestoreVersions(ctx context.Context, versions []Version) error {
	if len(versions) == 0 {
		return nil
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

	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO versions (id, path, hash, size, created_at, comment) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, version := range versions {
		cleanPath, err := normalizeVersionStorePath(version.Path)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, version.ID, cleanPath, version.Hash, version.Size, version.CreatedAt.Unix(), version.Comment); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// GetAllVersionHashes returns all version hashes in the database
func (s *Store) GetAllVersionHashes(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT hash FROM versions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hashes []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		hashes = append(hashes, hash)
	}

	return hashes, rows.Err()
}

// HasVersionReference reports whether any version metadata still references the hash.
func (s *Store) HasVersionReference(ctx context.Context, hash string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM versions WHERE hash = ? LIMIT 1`, hash).Scan(&exists)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ListVersionPaths returns all file paths that have historical versions.
func (s *Store) ListVersionPaths(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT path FROM versions ORDER BY path ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}

	return paths, rows.Err()
}

// ============================================================================
// Versioning Override Operations
// ============================================================================

// SetVersioningOverride sets a user override for versioning on a path
func (s *Store) SetVersioningOverride(ctx context.Context, path string, enabled bool) error {
	path, err := normalizeVersionStorePath(path)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO versioning_overrides (path, enabled, created_at) VALUES (?, ?, ?)`,
		path, enabled, time.Now().Unix())
	return err
}

// GetVersioningOverride gets the user override for a path
// Returns (enabled, exists)
func (s *Store) GetVersioningOverride(ctx context.Context, path string) (bool, bool) {
	path, err := normalizeVersionStorePath(path)
	if err != nil {
		return false, false
	}
	var enabled bool
	err = s.db.QueryRowContext(ctx,
		`SELECT enabled FROM versioning_overrides WHERE path = ?`,
		path).Scan(&enabled)
	if err != nil {
		return false, false
	}
	return enabled, true
}

// DeleteVersioningOverride removes the user override for a path
func (s *Store) DeleteVersioningOverride(ctx context.Context, path string) error {
	path, err := normalizeVersionStorePath(path)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM versioning_overrides WHERE path = ?`, path)
	return err
}

// RenamePath updates all metadata paths after a file or directory rename.
func (s *Store) RenamePath(ctx context.Context, oldPath, newPath string) error {
	s.fileLocksMu.Lock()
	defer s.fileLocksMu.Unlock()

	oldPath, err := normalizeVersionStorePath(oldPath)
	if err != nil {
		return err
	}
	newPath, err = normalizeVersionStorePath(newPath)
	if err != nil {
		return err
	}
	if oldPath == newPath {
		return nil
	}

	conflict, err := s.hasPathConflictInTables(ctx, newPath, []string{"versions", "files", "versioning_overrides", "file_locks"})
	if err != nil {
		return err
	}
	if conflict {
		return ErrAlreadyExists
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

	if err := renamePathInTable(ctx, tx, "versions", oldPath, newPath); err != nil {
		return err
	}
	if err := renamePathInTable(ctx, tx, "files", oldPath, newPath); err != nil {
		return err
	}
	if err := renamePathInTable(ctx, tx, "versioning_overrides", oldPath, newPath); err != nil {
		return err
	}
	if err := renamePathInTable(ctx, tx, "file_locks", oldPath, newPath); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// RenamePathHistory updates historical metadata after restore flows that already
// recreated the current file index at the destination path.
func (s *Store) RenamePathHistory(ctx context.Context, oldPath, newPath string) error {
	s.fileLocksMu.Lock()
	defer s.fileLocksMu.Unlock()

	oldPath, err := normalizeVersionStorePath(oldPath)
	if err != nil {
		return err
	}
	newPath, err = normalizeVersionStorePath(newPath)
	if err != nil {
		return err
	}
	if oldPath == newPath {
		return nil
	}

	conflict, err := s.hasPathConflictInTables(ctx, newPath, []string{"versions", "versioning_overrides", "file_locks"})
	if err != nil {
		return err
	}
	if conflict {
		return ErrAlreadyExists
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

	if err := renamePathInTable(ctx, tx, "versions", oldPath, newPath); err != nil {
		return err
	}
	if err := renamePathInTable(ctx, tx, "versioning_overrides", oldPath, newPath); err != nil {
		return err
	}
	if err := renamePathInTable(ctx, tx, "file_locks", oldPath, newPath); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) hasPathConflictInTables(ctx context.Context, targetPath string, tables []string) (bool, error) {
	if targetPath == "/" {
		return false, nil
	}

	prefix := targetPath + "/"
	prefixLen := len(prefix)
	for _, table := range tables {
		query := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE path = ? OR substr(path, 1, ?) = ? LIMIT 1)`, table)
		var exists bool
		if err := s.db.QueryRowContext(ctx, query, targetPath, prefixLen, prefix).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}

	return false, nil
}

func renamePathInTable(ctx context.Context, tx *sql.Tx, table, oldPath, newPath string) error {
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET path = ? WHERE path = ?`, table),
		newPath, oldPath); err != nil {
		return err
	}

	if oldPath == "/" {
		return nil
	}

	prefix := oldPath + "/"
	prefixLen := len(prefix)

	_, err := tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET path = ? || substr(path, ?) WHERE substr(path, 1, ?) = ?`, table),
		newPath, prefixLen, prefixLen, prefix)
	return err
}

// ============================================================================
// Trash Operations
// ============================================================================

// AddToTrash adds a file to trash
func (s *Store) AddToTrash(ctx context.Context, item *TrashItem) error {
	cleanOriginalPath, err := normalizeVersionStorePath(item.OriginalPath)
	if err != nil {
		return err
	}
	if !isValidStoreID(item.ID) {
		return errInvalidStoreID
	}
	item.OriginalPath = cleanOriginalPath
	item.RestoreData = normalizeTrashRestoreData(item.RestoreData)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO trash (id, original_path, size, deleted_at, expires_at, is_dir, had_versions, restore_data) 
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.OriginalPath, item.Size, item.DeletedAt.Unix(),
		item.ExpiresAt.Unix(), item.IsDir, item.HadVersions, item.RestoreData)
	return err
}

func (s *Store) UpdateTrashRestoreData(ctx context.Context, id string, restoreData []byte) error {
	if !isValidStoreID(id) {
		return ErrNotFound
	}
	restoreData = normalizeTrashRestoreData(restoreData)
	result, err := s.db.ExecContext(ctx, `UPDATE trash SET restore_data = ? WHERE id = ?`, restoreData, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetTrashItem returns a trash item by ID
func (s *Store) GetTrashItem(ctx context.Context, id string) (*TrashItem, error) {
	if !isValidStoreID(id) {
		return nil, ErrNotFound
	}

	var item TrashItem
	var deletedAt, expiresAt int64

	err := s.db.QueryRowContext(ctx,
		`SELECT id, original_path, size, deleted_at, expires_at, is_dir, had_versions, restore_data 
		 FROM trash WHERE id = ?`, id).Scan(
		&item.ID, &item.OriginalPath, &item.Size, &deletedAt, &expiresAt,
		&item.IsDir, &item.HadVersions, &item.RestoreData)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	item.DeletedAt = time.Unix(deletedAt, 0)
	item.ExpiresAt = time.Unix(expiresAt, 0)
	if err := normalizeTrashItemFromDB(&item); err != nil {
		return nil, ErrNotFound
	}
	return &item, nil
}

// ListTrash returns all trash items, newest first
func (s *Store) ListTrash(ctx context.Context) ([]TrashItem, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, original_path, size, deleted_at, expires_at, is_dir, had_versions, restore_data 
		 FROM trash ORDER BY deleted_at DESC, rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []TrashItem
	for rows.Next() {
		var item TrashItem
		var deletedAt, expiresAt int64
		if err := rows.Scan(&item.ID, &item.OriginalPath, &item.Size, &deletedAt,
			&expiresAt, &item.IsDir, &item.HadVersions, &item.RestoreData); err != nil {
			return nil, err
		}
		item.DeletedAt = time.Unix(deletedAt, 0)
		item.ExpiresAt = time.Unix(expiresAt, 0)
		if err := normalizeTrashItemFromDB(&item); err != nil {
			continue
		}
		items = append(items, item)
	}

	return items, rows.Err()
}

func normalizeTrashRestoreData(data []byte) []byte {
	if data == nil {
		return []byte{}
	}
	return data
}

// RemoveFromTrash removes a trash item
func (s *Store) RemoveFromTrash(ctx context.Context, id string) error {
	if !isValidStoreID(id) {
		return ErrNotFound
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM trash WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearTrash removes all trash items
func (s *Store) ClearTrash(ctx context.Context) (int, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM trash`)
	if err != nil {
		return 0, err
	}
	affected, _ := result.RowsAffected()
	return int(affected), nil
}

// CleanupExpiredTrash removes expired trash items
// Returns deleted item IDs for file cleanup
func (s *Store) CleanupExpiredTrash(ctx context.Context) ([]string, error) {
	now := time.Now().Unix()

	// Get expired items
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM trash WHERE expires_at < ?`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Delete expired items
	_, err = s.db.ExecContext(ctx, `DELETE FROM trash WHERE expires_at < ?`, now)
	return ids, err
}

// GetTrashStats returns trash statistics
func (s *Store) GetTrashStats(ctx context.Context) (count int, totalSize int64, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM trash`).Scan(&count, &totalSize)
	return
}

// ============================================================================
// File Lock Operations
// ============================================================================

// AcquireLock tries to acquire a lock on a path
func (s *Store) AcquireLock(ctx context.Context, path, holder string, lockType LockType, duration time.Duration) error {
	s.fileLocksMu.Lock()
	defer s.fileLocksMu.Unlock()

	path, err := normalizeVersionStorePath(path)
	if err != nil {
		return err
	}
	now := time.Now()
	expiresAt := now.Add(duration)

	// Clean up expired locks first
	s.db.ExecContext(ctx, `DELETE FROM file_locks WHERE expires_at < ?`, now.Unix())

	rows, err := s.db.QueryContext(ctx,
		`SELECT holder, lock_type FROM file_locks WHERE path = ? AND expires_at >= ?`,
		path, now.Unix())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var existingHolder string
		var existingType int
		if err := rows.Scan(&existingHolder, &existingType); err != nil {
			return err
		}
		if existingHolder == holder {
			continue
		}
		if lockType == WriteLock || LockType(existingType) == WriteLock {
			return ErrFileLocked
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Insert or update lock
	_, err = s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO file_locks (path, holder, lock_type, expires_at, created_at) 
		 VALUES (?, ?, ?, ?, ?)`,
		path, holder, int(lockType), expiresAt.Unix(), now.Unix())

	return err
}

// ReleaseLock releases a lock
func (s *Store) ReleaseLock(ctx context.Context, path, holder string) error {
	s.fileLocksMu.Lock()
	defer s.fileLocksMu.Unlock()

	path, err := normalizeVersionStorePath(path)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM file_locks WHERE path = ? AND holder = ?`, path, holder)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetLock returns the lock info for a path
func (s *Store) GetLock(ctx context.Context, path string) (*FileLock, error) {
	s.fileLocksMu.Lock()
	defer s.fileLocksMu.Unlock()

	path, err := normalizeVersionStorePath(path)
	if err != nil {
		return nil, err
	}
	var lock FileLock
	var lockType int
	var expiresAt, createdAt int64
	now := time.Now().Unix()

	err = s.db.QueryRowContext(ctx,
		`SELECT path, holder, lock_type, expires_at, created_at
		 FROM file_locks
		 WHERE path = ? AND expires_at >= ?
		 ORDER BY lock_type DESC, created_at ASC
		 LIMIT 1`,
		path, now).Scan(&lock.Path, &lock.Holder, &lockType, &expiresAt, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			result, cleanupErr := s.db.ExecContext(ctx, `DELETE FROM file_locks WHERE path = ? AND expires_at < ?`, path, now)
			if cleanupErr != nil {
				return nil, cleanupErr
			}
			affected, _ := result.RowsAffected()
			if affected > 0 {
				return nil, ErrLockExpired
			}
			return nil, ErrNotFound
		}
		return nil, err
	}

	lock.LockType = LockType(lockType)
	lock.ExpiresAt = time.Unix(expiresAt, 0)
	lock.CreatedAt = time.Unix(createdAt, 0)

	return &lock, nil
}

// CleanupExpiredLocks removes expired locks
func (s *Store) CleanupExpiredLocks(ctx context.Context) (int, error) {
	s.fileLocksMu.Lock()
	defer s.fileLocksMu.Unlock()

	result, err := s.db.ExecContext(ctx,
		`DELETE FROM file_locks WHERE expires_at < ?`, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	affected, _ := result.RowsAffected()
	return int(affected), nil
}

// ============================================================================
// File Index Operations
// ============================================================================

// UpdateFileIndex updates the file index
func (s *Store) UpdateFileIndex(ctx context.Context, path string, size int64, modTime time.Time, hash string) error {
	path, err := normalizeVersionStorePath(path)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO files (path, size, mod_time, content_hash) VALUES (?, ?, ?, ?)`,
		path, size, modTime.Unix(), hash)
	return err
}

// GetFileIndex returns file index entry
func (s *Store) GetFileIndex(ctx context.Context, path string) (size int64, modTime time.Time, hash string, err error) {
	path, err = normalizeVersionStorePath(path)
	if err != nil {
		return 0, time.Time{}, "", err
	}
	var modTimeUnix int64
	err = s.db.QueryRowContext(ctx,
		`SELECT size, mod_time, content_hash FROM files WHERE path = ?`,
		path).Scan(&size, &modTimeUnix, &hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrNotFound
		}
		return
	}
	modTime = time.Unix(modTimeUnix, 0)
	return
}

// FileIndexTreeExists reports whether any file index row exists at or below path.
func (s *Store) FileIndexTreeExists(ctx context.Context, filePath string) (bool, error) {
	filePath, err := normalizeVersionStorePath(filePath)
	if err != nil {
		return false, err
	}
	condition, args := pathTreeSQLCondition(filePath)
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM files WHERE `+condition+` LIMIT 1)`, args...).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// ListFileIndexTree returns every file index row at or below path in path order.
func (s *Store) ListFileIndexTree(ctx context.Context, filePath string) ([]FileIndexEntry, error) {
	filePath, err := normalizeVersionStorePath(filePath)
	if err != nil {
		return nil, err
	}
	condition, args := pathTreeSQLCondition(filePath)
	rows, err := s.db.QueryContext(ctx,
		`SELECT path, size, mod_time, content_hash FROM files WHERE `+condition+` ORDER BY path ASC`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]FileIndexEntry, 0)
	for rows.Next() {
		var entry FileIndexEntry
		var modTimeUnix int64
		if err := rows.Scan(&entry.Path, &entry.Size, &modTimeUnix, &entry.ContentHash); err != nil {
			return nil, err
		}
		entry.ModTime = time.Unix(modTimeUnix, 0)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// DeleteFileIndex removes a file from the index
func (s *Store) DeleteFileIndex(ctx context.Context, path string) error {
	path, err := normalizeVersionStorePath(path)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM files WHERE path = ?`, path)
	return err
}

// DeleteFileIndexPrefix removes all indexed files at or under a path.
func (s *Store) DeleteFileIndexPrefix(ctx context.Context, path string) error {
	path, err := normalizeVersionStorePath(path)
	if err != nil {
		return err
	}

	prefix := path + "/"
	_, err = s.db.ExecContext(ctx,
		`DELETE FROM files WHERE path = ? OR (path >= ? AND path < ?)`,
		path,
		prefix,
		prefix+"\uffff",
	)
	return err
}

// CountFiles returns the number of indexed files
func (s *Store) CountFiles(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// SearchFiles searches the file index
func (s *Store) SearchFiles(ctx context.Context, query string, limit int) ([]string, error) {
	query = "%" + escapeSQLiteLikeLiteral(strings.ToLower(query)) + "%"
	rows, err := s.db.QueryContext(ctx,
		`SELECT path FROM files WHERE LOWER(path) LIKE ? ESCAPE '\' ORDER BY path ASC LIMIT ?`,
		query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}

	return paths, rows.Err()
}

func escapeSQLiteLikeLiteral(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

// ============================================================================
// Object Storage Operations (delegated to ObjectStore backend)
// ============================================================================

// PutObject stores version content and returns its hash.
func (s *Store) PutObject(ctx context.Context, data []byte) (string, error) {
	return s.objects.Put(ctx, data)
}

// GetObject retrieves version content by hash.
func (s *Store) GetObject(ctx context.Context, hash string) ([]byte, error) {
	return s.objects.Get(ctx, hash)
}

// HasObject checks if an object exists.
func (s *Store) HasObject(ctx context.Context, hash string) (bool, error) {
	return s.objects.Has(ctx, hash)
}

// DeleteObject removes an object.
func (s *Store) DeleteObject(ctx context.Context, hash string) error {
	return s.objects.Delete(ctx, hash)
}

// ============================================================================
// Chunk Reference Counting (for GC)
// ============================================================================

// AddChunkRef increments reference count for a chunk
// If the chunk doesn't exist in the reference table, creates it with count 1
func (s *Store) AddChunkRef(ctx context.Context, chunkHash string, size int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chunk_refs (hash, ref_count, size, created_at)
		VALUES (?, 1, ?, ?)
		ON CONFLICT(hash) DO UPDATE SET ref_count = ref_count + 1
	`, chunkHash, size, time.Now().Unix())
	return err
}

// RemoveChunkRef decrements reference count for a chunk
// Returns true if the chunk reference count reaches 0 (can be garbage collected)
func (s *Store) RemoveChunkRef(ctx context.Context, chunkHash string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE chunk_refs SET ref_count = ref_count - 1 WHERE hash = ?
	`, chunkHash)
	if err != nil {
		return false, err
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return false, nil // Chunk not found
	}

	// Check if ref_count is now 0
	var refCount int
	err = s.db.QueryRowContext(ctx, `SELECT ref_count FROM chunk_refs WHERE hash = ?`, chunkHash).Scan(&refCount)
	if err != nil {
		return false, err
	}

	return refCount <= 0, nil
}

// LinkVersionChunks records chunk references for a version
func (s *Store) LinkVersionChunks(ctx context.Context, versionID int64, chunkHashes []string) error {
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

	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO version_chunks (version_id, chunk_hash) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, hash := range chunkHashes {
		if _, err := stmt.ExecContext(ctx, versionID, hash); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// GetOrphanedChunks returns chunks with ref_count <= 0
func (s *Store) GetOrphanedChunks(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT hash FROM chunk_refs WHERE ref_count <= 0 LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hashes []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		hashes = append(hashes, hash)
	}

	return hashes, rows.Err()
}

// DeleteChunkRef removes a chunk reference record (after GC)
func (s *Store) DeleteChunkRef(ctx context.Context, chunkHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chunk_refs WHERE hash = ?`, chunkHash)
	return err
}

// RunGC performs garbage collection of orphaned chunks
// Returns the number of chunks deleted and total bytes freed
func (s *Store) RunGC(ctx context.Context, batchSize int) (int, int64, error) {
	orphans, err := s.GetOrphanedChunks(ctx, batchSize)
	if err != nil {
		return 0, 0, err
	}

	var deleted int
	var freedBytes int64
	var gcErr error

	for _, hash := range orphans {
		// If chunk metadata cannot be loaded reliably, leave the candidate for a later GC pass.
		size, err := s.getChunkRefSizeFn(ctx, hash)
		if err != nil {
			gcErr = errors.Join(gcErr, fmt.Errorf("get chunk ref size %s: %w", hash, err))
			continue
		}

		// Delete from CAS
		objectDeleted := true
		if err := s.deleteObjectFn(ctx, hash); err != nil {
			if errors.Is(err, ErrNotFound) {
				objectDeleted = false
			} else {
				gcErr = errors.Join(gcErr, fmt.Errorf("delete object %s: %w", hash, err))
				continue
			}
		}

		// Delete reference record
		if err := s.deleteChunkRefFn(ctx, hash); err != nil {
			gcErr = errors.Join(gcErr, fmt.Errorf("delete chunk ref %s: %w", hash, err))
			continue
		}

		if objectDeleted {
			deleted++
			freedBytes += size
		}
	}

	return deleted, freedBytes, gcErr
}
