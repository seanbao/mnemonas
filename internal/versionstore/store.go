// Package versionstore provides SQLite-based version management for MnemoNAS
package versionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/seanbao/mnemonas/internal/dataplane"
)

// Common errors
var (
	ErrNotFound    = errors.New("not found")
	ErrFileLocked  = errors.New("file is locked")
	ErrLockExpired = errors.New("lock has expired")
)

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
	db      *sql.DB
	objects *ObjectStore
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

	// Ensure database directory exists
	dbDir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dbDir, 0700); err != nil {
		return nil, err
	}

	// Open database
	db, err := sql.Open("sqlite3", cfg.DBPath+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, err
	}

	// Create tables
	if err := createTables(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{
		db:      db,
		objects: NewObjectStore(cfg.Dataplane),
	}, nil
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
		had_versions BOOLEAN NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_trash_expires ON trash(expires_at);

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
	return err
}

// Close closes the store
func (s *Store) Close() error {
	return s.db.Close()
}

// ============================================================================
// Version Operations
// ============================================================================

// AddVersion adds a new version record
func (s *Store) AddVersion(ctx context.Context, path, hash string, size int64, comment string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO versions (path, hash, size, created_at, comment) VALUES (?, ?, ?, ?, ?)`,
		path, hash, size, time.Now().Unix(), comment)
	return err
}

// GetVersions returns all versions of a file, newest first
func (s *Store) GetVersions(ctx context.Context, path string) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, path, hash, size, created_at, COALESCE(comment, '') 
		 FROM versions WHERE path = ? ORDER BY created_at DESC`,
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
	var v Version
	var createdAt int64

	err := s.db.QueryRowContext(ctx,
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
	_, err := s.db.ExecContext(ctx, `DELETE FROM versions WHERE path = ?`, path)
	return err
}

// DeleteOldVersions deletes versions older than maxAge or exceeding maxCount
func (s *Store) DeleteOldVersions(ctx context.Context, path string, maxCount int, maxAge time.Duration) ([]string, error) {
	cutoff := time.Now().Add(-maxAge).Unix()

	// Get hashes to delete
	rows, err := s.db.QueryContext(ctx,
		`SELECT hash FROM versions WHERE path = ? AND (
			created_at < ? OR 
			id NOT IN (SELECT id FROM versions WHERE path = ? ORDER BY created_at DESC LIMIT ?)
		)`,
		path, cutoff, path, maxCount)
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

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Delete the records
	_, err = s.db.ExecContext(ctx,
		`DELETE FROM versions WHERE path = ? AND (
			created_at < ? OR 
			id NOT IN (SELECT id FROM versions WHERE path = ? ORDER BY created_at DESC LIMIT ?)
		)`,
		path, cutoff, path, maxCount)

	return hashes, err
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

// ============================================================================
// Versioning Override Operations
// ============================================================================

// SetVersioningOverride sets a user override for versioning on a path
func (s *Store) SetVersioningOverride(ctx context.Context, path string, enabled bool) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO versioning_overrides (path, enabled, created_at) VALUES (?, ?, ?)`,
		path, enabled, time.Now().Unix())
	return err
}

// GetVersioningOverride gets the user override for a path
// Returns (enabled, exists)
func (s *Store) GetVersioningOverride(ctx context.Context, path string) (bool, bool) {
	var enabled bool
	err := s.db.QueryRowContext(ctx,
		`SELECT enabled FROM versioning_overrides WHERE path = ?`,
		path).Scan(&enabled)
	if err != nil {
		return false, false
	}
	return enabled, true
}

// DeleteVersioningOverride removes the user override for a path
func (s *Store) DeleteVersioningOverride(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM versioning_overrides WHERE path = ?`, path)
	return err
}

// RenamePath updates all metadata paths after a file or directory rename.
func (s *Store) RenamePath(ctx context.Context, oldPath, newPath string) error {
	if oldPath == newPath {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

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

	return tx.Commit()
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
	likePattern := prefix + "%"
	prefixStart := len(oldPath) + 1

	_, err := tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET path = ? || substr(path, ?) WHERE path LIKE ?`, table),
		newPath, prefixStart, likePattern)
	return err
}

// ============================================================================
// Trash Operations
// ============================================================================

// AddToTrash adds a file to trash
func (s *Store) AddToTrash(ctx context.Context, item *TrashItem) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO trash (id, original_path, size, deleted_at, expires_at, is_dir, had_versions) 
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.OriginalPath, item.Size, item.DeletedAt.Unix(),
		item.ExpiresAt.Unix(), item.IsDir, item.HadVersions)
	return err
}

// GetTrashItem returns a trash item by ID
func (s *Store) GetTrashItem(ctx context.Context, id string) (*TrashItem, error) {
	var item TrashItem
	var deletedAt, expiresAt int64

	err := s.db.QueryRowContext(ctx,
		`SELECT id, original_path, size, deleted_at, expires_at, is_dir, had_versions 
		 FROM trash WHERE id = ?`, id).Scan(
		&item.ID, &item.OriginalPath, &item.Size, &deletedAt, &expiresAt,
		&item.IsDir, &item.HadVersions)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	item.DeletedAt = time.Unix(deletedAt, 0)
	item.ExpiresAt = time.Unix(expiresAt, 0)
	return &item, nil
}

// ListTrash returns all trash items, newest first
func (s *Store) ListTrash(ctx context.Context) ([]TrashItem, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, original_path, size, deleted_at, expires_at, is_dir, had_versions 
		 FROM trash ORDER BY deleted_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []TrashItem
	for rows.Next() {
		var item TrashItem
		var deletedAt, expiresAt int64
		if err := rows.Scan(&item.ID, &item.OriginalPath, &item.Size, &deletedAt,
			&expiresAt, &item.IsDir, &item.HadVersions); err != nil {
			return nil, err
		}
		item.DeletedAt = time.Unix(deletedAt, 0)
		item.ExpiresAt = time.Unix(expiresAt, 0)
		items = append(items, item)
	}

	return items, rows.Err()
}

// RemoveFromTrash removes a trash item
func (s *Store) RemoveFromTrash(ctx context.Context, id string) error {
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
	now := time.Now()
	expiresAt := now.Add(duration)

	// Clean up expired locks first
	s.db.ExecContext(ctx, `DELETE FROM file_locks WHERE expires_at < ?`, now.Unix())

	// Check for existing lock
	var existingHolder string
	var existingType int
	var existingExpires int64

	err := s.db.QueryRowContext(ctx,
		`SELECT holder, lock_type, expires_at FROM file_locks WHERE path = ?`,
		path).Scan(&existingHolder, &existingType, &existingExpires)

	// Lock exists
	if err == nil {
		// Lock still valid
		if time.Unix(existingExpires, 0).After(now) {
			// Conflict: either we want write lock or existing is write lock
			if lockType == WriteLock || LockType(existingType) == WriteLock {
				if existingHolder != holder {
					return ErrFileLocked
				}
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
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
	var lock FileLock
	var lockType int
	var expiresAt, createdAt int64

	err := s.db.QueryRowContext(ctx,
		`SELECT path, holder, lock_type, expires_at, created_at FROM file_locks WHERE path = ?`,
		path).Scan(&lock.Path, &lock.Holder, &lockType, &expiresAt, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	lock.LockType = LockType(lockType)
	lock.ExpiresAt = time.Unix(expiresAt, 0)
	lock.CreatedAt = time.Unix(createdAt, 0)

	// Check if expired
	if lock.ExpiresAt.Before(time.Now()) {
		// Clean up expired lock
		s.db.ExecContext(ctx, `DELETE FROM file_locks WHERE path = ?`, path)
		return nil, ErrLockExpired
	}

	return &lock, nil
}

// CleanupExpiredLocks removes expired locks
func (s *Store) CleanupExpiredLocks(ctx context.Context) (int, error) {
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
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO files (path, size, mod_time, content_hash) VALUES (?, ?, ?, ?)`,
		path, size, modTime.Unix(), hash)
	return err
}

// GetFileIndex returns file index entry
func (s *Store) GetFileIndex(ctx context.Context, path string) (size int64, modTime time.Time, hash string, err error) {
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

// DeleteFileIndex removes a file from the index
func (s *Store) DeleteFileIndex(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE path = ?`, path)
	return err
}

// SearchFiles searches the file index
func (s *Store) SearchFiles(ctx context.Context, query string, limit int) ([]string, error) {
	query = "%" + strings.ToLower(query) + "%"
	rows, err := s.db.QueryContext(ctx,
		`SELECT path FROM files WHERE LOWER(path) LIKE ? LIMIT ?`,
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

// ============================================================================
// Object Storage Operations (delegated to ObjectStore backend)
// ============================================================================

// PutObject stores version content and returns its hash
func (s *Store) PutObject(data []byte) (string, error) {
	return s.objects.Put(context.Background(), data)
}

// GetObject retrieves version content by hash
func (s *Store) GetObject(hash string) ([]byte, error) {
	return s.objects.Get(context.Background(), hash)
}

// HasObject checks if an object exists
func (s *Store) HasObject(hash string) bool {
	return s.objects.Has(context.Background(), hash)
}

// DeleteObject removes an object
func (s *Store) DeleteObject(hash string) error {
	return s.objects.Delete(context.Background(), hash)
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
	defer tx.Rollback()

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

	return tx.Commit()
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

	for _, hash := range orphans {
		// Get size before deleting
		var size int64
		_ = s.db.QueryRowContext(ctx, `SELECT size FROM chunk_refs WHERE hash = ?`, hash).Scan(&size)

		// Delete from CAS
		if err := s.DeleteObject(hash); err != nil {
			// Log but continue
			continue
		}

		// Delete reference record
		if err := s.DeleteChunkRef(ctx, hash); err != nil {
			continue
		}

		deleted++
		freedBytes += size
	}

	return deleted, freedBytes, nil
}
