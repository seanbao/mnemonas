//! CAS (Content-Addressable Storage) implementation
//! Uses BLAKE3 as hash algorithm (10x faster than SHA256)
//! Supports optional zstd compression for storage efficiency

use std::path::{Component, Path, PathBuf};
#[cfg(test)]
use std::sync::atomic::{AtomicBool, AtomicUsize};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use dashmap::DashMap;
use thiserror::Error;
use tokio::fs;
use tokio::io::AsyncWriteExt;
use tokio::sync::Mutex;
use tracing::{debug, info, instrument, warn};

/// CAS storage error
#[derive(Error, Debug)]
pub enum CasError {
    #[error("IO error: {0}")]
    Io(#[from] std::io::Error),

    #[error("Object not found: {0}")]
    NotFound(String),

    #[error("Hash mismatch: expected={expected}, actual={actual}")]
    HashMismatch { expected: String, actual: String },

    #[error("invalid object hash")]
    InvalidHash,

    #[error("invalid CAS configuration: {0}")]
    InvalidConfig(String),

    #[error("Compression error: {0}")]
    Compression(String),
}

pub type Result<T> = std::result::Result<T, CasError>;

/// CAS storage configuration
#[derive(Debug, Clone)]
pub struct CasConfig {
    /// Storage root directory
    pub root: PathBuf,
    /// Number of shard levels
    pub shard_levels: usize,
    /// Characters per shard level
    pub shard_size: usize,
    /// Enable zstd compression
    pub compression_enabled: bool,
    /// Compression level (1-22, default 3)
    pub compression_level: i32,
    /// Minimum size to compress (bytes, smaller files stored uncompressed)
    pub min_compress_size: usize,
}

impl Default for CasConfig {
    fn default() -> Self {
        let default_root = std::env::var("HOME")
            .map(PathBuf::from)
            .unwrap_or_else(|_| PathBuf::from("./data"))
            .join(".mnemonas")
            .join(".mnemonas")
            .join("objects");
        Self {
            root: default_root,
            shard_levels: 2,
            shard_size: 2,
            compression_enabled: true,
            compression_level: 3,
            min_compress_size: 1024, // 1KB
        }
    }
}

impl CasConfig {
    /// Validate filesystem and sharding parameters before the store touches disk.
    pub fn validate(&self) -> Result<()> {
        if self.root.as_os_str().is_empty() {
            return Err(CasError::InvalidConfig(
                "root directory cannot be empty".to_string(),
            ));
        }
        if path_contains_parent_segment(&self.root) {
            return Err(CasError::InvalidConfig(
                "root directory cannot contain parent directory segments".to_string(),
            ));
        }
        if is_protected_system_directory(&self.root) {
            return Err(CasError::InvalidConfig(
                "root directory cannot be a protected system directory".to_string(),
            ));
        }
        if self.shard_levels > 0 && self.shard_size == 0 {
            return Err(CasError::InvalidConfig(
                "shard_size must be positive when shard_levels is non-zero".to_string(),
            ));
        }
        let sharded_chars = self
            .shard_levels
            .checked_mul(self.shard_size)
            .ok_or_else(|| {
                CasError::InvalidConfig("shard configuration is too large".to_string())
            })?;
        if sharded_chars > blake3::OUT_LEN * 2 {
            return Err(CasError::InvalidConfig(
                "shard configuration exceeds hash length".to_string(),
            ));
        }
        if !(1..=22).contains(&self.compression_level) {
            return Err(CasError::InvalidConfig(
                "compression_level must be between 1 and 22".to_string(),
            ));
        }
        validate_no_symlink_ancestors(&self.root)
    }
}

/// CAS storage
pub struct CasStore {
    config: CasConfig,
    /// Memory index for fast existence check (hash -> original_size)
    index: DashMap<String, u64>,
    /// Per-object write/delete locks. These avoid holding DashMap shard locks across async I/O.
    write_locks: DashMap<String, Arc<Mutex<()>>>,
    /// Statistics
    stats: CasStats,
    temp_nonce: AtomicU64,
    #[cfg(test)]
    fail_put_after: AtomicUsize,
    #[cfg(test)]
    fail_parent_sync: AtomicBool,
    #[cfg(test)]
    before_object_file_open: std::sync::Mutex<Option<Box<dyn FnOnce() + Send>>>,
    #[cfg(test)]
    before_temp_file_create: std::sync::Mutex<Option<Box<dyn FnOnce() + Send>>>,
    #[cfg(test)]
    before_object_rename: std::sync::Mutex<Option<Box<dyn FnOnce() + Send>>>,
    #[cfg(test)]
    before_object_delete: std::sync::Mutex<Option<Box<dyn FnOnce() + Send>>>,
}

pub struct PutResult {
    pub hash: String,
    pub deduplicated: bool,
}

/// Storage statistics
#[derive(Default)]
pub struct CasStats {
    pub total_chunks: AtomicU64,
    pub logical_size: AtomicU64,
    pub total_size: AtomicU64,
    pub compressed_size: AtomicU64,
    pub hit_count: AtomicU64,
    pub miss_count: AtomicU64,
}

/// Paginated object listing entry: (hash, size, created_at_unix)
pub type ObjectListEntry = (String, u64, Option<i64>);

/// Paginated object listing page: (entries, next_cursor)
pub type ObjectListPage = (Vec<ObjectListEntry>, Option<String>);

/// Scrub result for a single object
#[derive(Debug, Clone)]
pub struct ScrubResult {
    pub hash: String,
    pub size: u64,
    pub valid: bool,
    pub error: Option<String>,
}

/// Scrub summary
#[derive(Debug, Clone, Default)]
pub struct ScrubSummary {
    pub total_objects: u64,
    pub valid_objects: u64,
    pub corrupted_objects: u64,
    pub missing_objects: u64,
    pub total_size: u64,
    pub duration_ms: u64,
    /// (hash, error_type, message)
    pub errors: Vec<(String, String, String)>,
}

impl CasStore {
    /// Create CAS storage
    pub async fn new(config: CasConfig) -> Result<Self> {
        config.validate()?;

        // Create root directory
        fs::create_dir_all(&config.root).await?;
        validate_no_symlink_ancestors(&config.root)?;

        info!(
            root = %config.root.display(),
            compression = config.compression_enabled,
            compression_level = config.compression_level,
            "initializing CAS storage"
        );

        let store = Self {
            config,
            index: DashMap::new(),
            write_locks: DashMap::new(),
            stats: CasStats::default(),
            temp_nonce: AtomicU64::new(0),
            #[cfg(test)]
            fail_put_after: AtomicUsize::new(usize::MAX),
            #[cfg(test)]
            fail_parent_sync: AtomicBool::new(false),
            #[cfg(test)]
            before_object_file_open: std::sync::Mutex::new(None),
            #[cfg(test)]
            before_temp_file_create: std::sync::Mutex::new(None),
            #[cfg(test)]
            before_object_rename: std::sync::Mutex::new(None),
            #[cfg(test)]
            before_object_delete: std::sync::Mutex::new(None),
        };

        // Rebuild index (background task)
        store.rebuild_index().await?;

        Ok(store)
    }

    /// Store data chunk, returns hash
    #[instrument(skip(self, data), fields(size = data.len()))]
    pub async fn put(&self, data: &[u8]) -> Result<String> {
        self.put_with_status(data).await.map(|result| result.hash)
    }

    /// Store data chunk, returning both the hash and whether it was deduplicated.
    #[instrument(skip(self, data), fields(size = data.len()))]
    pub async fn put_with_status(&self, data: &[u8]) -> Result<PutResult> {
        let hash = compute_hash(data);
        let original_size = data.len() as u64;
        let write_lock = self.lock_for_hash(&hash);

        let result = {
            let _write_guard = write_lock.lock().await;
            self.put_with_status_locked(hash.clone(), data, original_size)
                .await
        };

        self.remove_hash_lock_if_idle(&hash, &write_lock);
        result
    }

    async fn put_with_status_locked(
        &self,
        hash: String,
        data: &[u8],
        original_size: u64,
    ) -> Result<PutResult> {
        if let Some(existing_size) = self.index.get(&hash).map(|r| *r) {
            if self.object_exists(&hash).await? {
                debug!(hash = %hash, "dedup hit");
                self.record_dedup_hit(original_size);
                return Ok(PutResult {
                    hash,
                    deduplicated: true,
                });
            }

            let stale_disk_size = self.prepare_write_target(&hash, data)?.0.len() as u64;
            if self.index.remove(&hash).is_some() {
                warn!(hash = %hash, "stale CAS index entry detected; recreating missing object");
                self.stats.total_chunks.fetch_sub(1, Ordering::Relaxed);
                self.stats
                    .total_size
                    .fetch_sub(existing_size, Ordering::Relaxed);
                self.stats
                    .compressed_size
                    .fetch_sub(stale_disk_size, Ordering::Relaxed);
            }
        }

        #[cfg(test)]
        self.maybe_fail_put()?;

        let (write_data, path) = self.prepare_write_target(&hash, data)?;

        let disk_size = write_data.len() as u64;

        // Create directory
        if let Some(parent) = path.parent() {
            validate_no_symlink_ancestors(parent)?;
            fs::create_dir_all(parent).await?;
            validate_no_symlink_ancestors(parent)?;
        }

        // Atomic write
        let (mut file, tmp_path) = self.create_temp_file(&path).await?;
        if let Err(err) = file.write_all(&write_data).await {
            let _ = remove_file_no_follow(&tmp_path).await;
            return Err(CasError::Io(err));
        }
        if let Err(err) = file.sync_all().await {
            let _ = remove_file_no_follow(&tmp_path).await;
            return Err(CasError::Io(err));
        }
        drop(file);
        #[cfg(test)]
        if let Some(hook) = self.before_object_rename.lock().unwrap().take() {
            hook();
        }
        if let Err(err) = rename_file_no_follow(&tmp_path, &path).await {
            let _ = remove_file_no_follow(&tmp_path).await;
            return Err(CasError::Io(err));
        }
        self.sync_parent_dir(&path).await?;

        // Update index and stats
        self.index.insert(hash.clone(), original_size);
        self.stats
            .logical_size
            .fetch_add(original_size, Ordering::Relaxed);
        self.stats.total_chunks.fetch_add(1, Ordering::Relaxed);
        self.stats
            .total_size
            .fetch_add(original_size, Ordering::Relaxed);
        self.stats
            .compressed_size
            .fetch_add(disk_size, Ordering::Relaxed);

        debug!(
            hash = %hash,
            original_size,
            disk_size,
            compressed = path.extension().map(|e| e == "zst").unwrap_or(false),
            "stored successfully"
        );
        Ok(PutResult {
            hash,
            deduplicated: false,
        })
    }

    fn record_dedup_hit(&self, original_size: u64) {
        self.stats
            .logical_size
            .fetch_add(original_size, Ordering::Relaxed);
        self.stats.hit_count.fetch_add(1, Ordering::Relaxed);
    }

    fn lock_for_hash(&self, hash: &str) -> Arc<Mutex<()>> {
        self.write_locks
            .entry(hash.to_string())
            .or_insert_with(|| Arc::new(Mutex::new(())))
            .clone()
    }

    fn remove_hash_lock_if_idle(&self, hash: &str, lock: &Arc<Mutex<()>>) {
        if Arc::strong_count(lock) != 2 {
            return;
        }

        self.write_locks.remove_if(hash, |_, existing| {
            Arc::ptr_eq(existing, lock) && Arc::strong_count(existing) == 2
        });
    }

    fn prepare_write_target(&self, hash: &str, data: &[u8]) -> Result<(Vec<u8>, PathBuf)> {
        let should_compress =
            self.config.compression_enabled && data.len() >= self.config.min_compress_size;

        if should_compress {
            let compressed =
                zstd::encode_all(std::io::Cursor::new(data), self.config.compression_level)
                    .map_err(|e| CasError::Compression(e.to_string()))?;

            if compressed.len() < data.len() {
                let path = self.hash_to_path(hash).with_extension("zst");
                Ok((compressed, path))
            } else {
                Ok((data.to_vec(), self.hash_to_path(hash)))
            }
        } else {
            Ok((data.to_vec(), self.hash_to_path(hash)))
        }
    }

    fn next_temp_path(&self, path: &Path) -> PathBuf {
        let nonce = self.temp_nonce.fetch_add(1, Ordering::Relaxed);
        let timestamp = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or(Duration::ZERO)
            .as_nanos();
        let file_name = path
            .file_name()
            .and_then(|name| name.to_str())
            .unwrap_or("object");
        let temp_name = format!(
            ".{file_name}.{}.{}.tmp",
            std::process::id(),
            timestamp + nonce as u128
        );

        path.parent()
            .unwrap_or_else(|| Path::new("."))
            .join(temp_name)
    }

    async fn create_temp_file(&self, path: &Path) -> Result<(fs::File, PathBuf)> {
        validate_object_parent(path)?;

        for _ in 0..8 {
            let tmp_path = self.next_temp_path(path);
            #[cfg(test)]
            if let Some(hook) = self.before_temp_file_create.lock().unwrap().take() {
                hook();
            }
            match create_new_file_no_follow(&tmp_path).await {
                Ok(file) => return Ok((file, tmp_path)),
                Err(err) if err.kind() == std::io::ErrorKind::AlreadyExists => continue,
                Err(err) => return Err(CasError::Io(err)),
            }
        }

        Err(CasError::Io(std::io::Error::new(
            std::io::ErrorKind::AlreadyExists,
            "failed to allocate unique temp object path",
        )))
    }

    async fn object_exists(&self, hash: &str) -> Result<bool> {
        let base_path = self.hash_to_path(hash);
        let compressed_path = base_path.with_extension("zst");

        if self.path_exists(&compressed_path).await? {
            return Ok(true);
        }

        self.path_exists(&base_path).await
    }

    async fn path_exists(&self, path: &Path) -> Result<bool> {
        if validate_object_parent(path).is_err() {
            return Ok(false);
        }

        match fs::symlink_metadata(path).await {
            Ok(metadata) => Ok(metadata.is_file() && !metadata.file_type().is_symlink()),
            Err(err) if err.kind() == std::io::ErrorKind::NotFound => Ok(false),
            Err(err) => Err(CasError::Io(err)),
        }
    }

    async fn read_object_file(&self, path: &Path) -> Result<Option<Vec<u8>>> {
        if validate_object_parent(path).is_err() {
            return Ok(None);
        }

        let metadata = match fs::symlink_metadata(path).await {
            Ok(metadata) => metadata,
            Err(err) if err.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            Err(err) => return Err(CasError::Io(err)),
        };

        if !metadata.is_file() || metadata.file_type().is_symlink() {
            return Ok(None);
        }

        #[cfg(test)]
        if let Some(hook) = self.before_object_file_open.lock().unwrap().take() {
            hook();
        }

        read_regular_file_no_follow(path).await
    }

    #[cfg(test)]
    pub(crate) fn set_fail_put_after(&self, remaining_successes: usize) {
        self.fail_put_after
            .store(remaining_successes, Ordering::Relaxed);
    }

    #[cfg(test)]
    pub(crate) fn fail_next_parent_sync(&self) {
        self.fail_parent_sync.store(true, Ordering::Relaxed);
    }

    #[cfg(test)]
    pub(crate) fn set_before_object_file_open<F>(&self, hook: F)
    where
        F: FnOnce() + Send + 'static,
    {
        *self.before_object_file_open.lock().unwrap() = Some(Box::new(hook));
    }

    #[cfg(test)]
    pub(crate) fn set_before_temp_file_create<F>(&self, hook: F)
    where
        F: FnOnce() + Send + 'static,
    {
        *self.before_temp_file_create.lock().unwrap() = Some(Box::new(hook));
    }

    #[cfg(test)]
    pub(crate) fn set_before_object_rename<F>(&self, hook: F)
    where
        F: FnOnce() + Send + 'static,
    {
        *self.before_object_rename.lock().unwrap() = Some(Box::new(hook));
    }

    #[cfg(test)]
    pub(crate) fn set_before_object_delete<F>(&self, hook: F)
    where
        F: FnOnce() + Send + 'static,
    {
        *self.before_object_delete.lock().unwrap() = Some(Box::new(hook));
    }

    pub(crate) fn rollback_logical_size(&self, bytes: u64) {
        if bytes == 0 {
            return;
        }
        self.stats.logical_size.fetch_sub(bytes, Ordering::Relaxed);
    }

    #[cfg(test)]
    fn maybe_fail_put(&self) -> Result<()> {
        let remaining = self.fail_put_after.load(Ordering::Relaxed);
        if remaining == usize::MAX {
            return Ok(());
        }
        if remaining == 0 {
            return Err(CasError::Io(std::io::Error::other("test put failure")));
        }
        self.fail_put_after.store(remaining - 1, Ordering::Relaxed);
        Ok(())
    }

    async fn sync_parent_dir(&self, path: &Path) -> Result<()> {
        #[cfg(test)]
        if self.fail_parent_sync.swap(false, Ordering::Relaxed) {
            return Err(CasError::Io(std::io::Error::other(
                "test parent sync failure",
            )));
        }

        let parent = path.parent().ok_or_else(|| {
            CasError::Io(std::io::Error::other("object path has no parent directory"))
        })?;
        validate_no_symlink_ancestors(parent)?;
        sync_dir_no_follow(parent).await?;
        Ok(())
    }

    /// Read data chunk
    #[instrument(skip(self))]
    pub async fn get(&self, hash: &str) -> Result<Vec<u8>> {
        validate_hash(hash)?;

        let base_path = self.hash_to_path(hash);
        let compressed_path = base_path.with_extension("zst");

        // Try compressed file first, then uncompressed
        let (raw_data, is_compressed) =
            if let Some(data) = self.read_object_file(&compressed_path).await? {
                (data, true)
            } else if let Some(data) = self.read_object_file(&base_path).await? {
                (data, false)
            } else {
                self.stats.miss_count.fetch_add(1, Ordering::Relaxed);
                return Err(CasError::NotFound(hash.to_string()));
            };

        // Decompress if needed
        let data = if is_compressed {
            zstd::decode_all(std::io::Cursor::new(&raw_data))
                .map_err(|e| CasError::Compression(e.to_string()))?
        } else {
            raw_data
        };

        // Verify hash
        let actual_hash = compute_hash(&data);
        if actual_hash != hash {
            return Err(CasError::HashMismatch {
                expected: hash.to_string(),
                actual: actual_hash,
            });
        }

        Ok(data)
    }

    /// Check if data chunk exists
    pub fn has(&self, hash: &str) -> bool {
        if !is_valid_hash(hash) {
            return false;
        }
        self.index.contains_key(hash)
    }

    /// Get data chunk size (original uncompressed size)
    pub fn size(&self, hash: &str) -> Option<u64> {
        if !is_valid_hash(hash) {
            return None;
        }
        self.index.get(hash).map(|r| *r)
    }

    /// Delete data chunk
    #[instrument(skip(self))]
    pub async fn delete(&self, hash: &str) -> Result<bool> {
        validate_hash(hash)?;
        let write_lock = self.lock_for_hash(hash);

        let result = {
            let _write_guard = write_lock.lock().await;
            self.delete_locked(hash).await
        };

        self.remove_hash_lock_if_idle(hash, &write_lock);
        result
    }

    async fn delete_locked(&self, hash: &str) -> Result<bool> {
        let base_path = self.hash_to_path(hash);
        let compressed_path = base_path.with_extension("zst");
        if validate_object_parent(&base_path).is_err() {
            return Ok(false);
        }

        if let Some(original_size) = self.index.get(hash).map(|r| *r) {
            #[cfg(test)]
            if let Some(hook) = self.before_object_delete.lock().unwrap().take() {
                hook();
            }

            // Try to delete both possible files
            let deleted_compressed = remove_file_no_follow(&compressed_path).await.is_ok();
            let deleted_plain = remove_file_no_follow(&base_path).await.is_ok();

            if deleted_compressed || deleted_plain {
                self.index.remove(hash);
                self.stats.total_chunks.fetch_sub(1, Ordering::Relaxed);
                self.stats
                    .total_size
                    .fetch_sub(original_size, Ordering::Relaxed);
                // Note: compressed_size tracking is approximate after delete
                let deleted_path = if deleted_compressed {
                    &compressed_path
                } else {
                    &base_path
                };
                self.sync_parent_dir(deleted_path).await?;
                Ok(true)
            } else {
                Ok(false)
            }
        } else {
            Ok(false)
        }
    }

    /// Get statistics (chunks, logical_size, unique_size, compressed_size, hits, misses)
    pub fn stats(&self) -> (u64, u64, u64, u64, u64, u64) {
        (
            self.stats.total_chunks.load(Ordering::Relaxed),
            self.stats.logical_size.load(Ordering::Relaxed),
            self.stats.total_size.load(Ordering::Relaxed),
            self.stats.compressed_size.load(Ordering::Relaxed),
            self.stats.hit_count.load(Ordering::Relaxed),
            self.stats.miss_count.load(Ordering::Relaxed),
        )
    }

    /// Get compression ratio (0.0 - 1.0, lower is better)
    pub fn compression_ratio(&self) -> f64 {
        let original = self.stats.total_size.load(Ordering::Relaxed);
        let compressed = self.stats.compressed_size.load(Ordering::Relaxed);
        if original == 0 {
            return 1.0;
        }
        compressed as f64 / original as f64
    }

    /// Run scrub to verify objects integrity
    /// If hashes is Some, only verify those specific hashes; otherwise verify all
    #[instrument(skip(self, hashes))]
    pub async fn scrub(&self, hashes: Option<&[String]>) -> Result<ScrubSummary> {
        use std::time::Instant;

        if let Some(hashes) = hashes {
            for hash in hashes {
                validate_hash(hash)?;
            }
        }

        info!("starting scrub...");
        let start = Instant::now();

        let mut summary = ScrubSummary::default();

        // Collect hashes to check
        let hashes_to_check: Vec<(String, u64)> = match hashes {
            Some(h) => h
                .iter()
                .filter_map(|hash| self.index.get(hash).map(|e| (hash.clone(), *e)))
                .collect(),
            None => self
                .index
                .iter()
                .map(|e| (e.key().clone(), *e.value()))
                .collect(),
        };

        // Iterate objects
        for (hash, expected_size) in hashes_to_check {
            summary.total_objects += 1;

            // Use CAS read path to handle compressed objects transparently
            match self.get(&hash).await {
                Ok(data) => {
                    let actual_size = data.len() as u64;

                    if actual_size == expected_size {
                        summary.valid_objects += 1;
                        summary.total_size += actual_size;
                    } else {
                        summary.corrupted_objects += 1;
                        summary.errors.push((
                            hash.clone(),
                            "corrupted".to_string(),
                            format!(
                                "size mismatch: expected={}, actual={}",
                                expected_size, actual_size
                            ),
                        ));
                    }
                }
                Err(CasError::NotFound(_)) => {
                    summary.missing_objects += 1;
                    summary.errors.push((
                        hash.clone(),
                        "missing".to_string(),
                        "file not found".to_string(),
                    ));
                }
                Err(CasError::HashMismatch { expected, actual }) => {
                    summary.corrupted_objects += 1;
                    summary.errors.push((
                        hash.clone(),
                        "corrupted".to_string(),
                        format!("hash mismatch: expected={}, actual={}", expected, actual),
                    ));
                }
                Err(e) => {
                    summary.corrupted_objects += 1;
                    summary.errors.push((
                        hash.clone(),
                        "io_error".to_string(),
                        format!("read error: {}", e),
                    ));
                }
            }
        }

        summary.duration_ms = start.elapsed().as_millis() as u64;

        info!(
            total = summary.total_objects,
            valid = summary.valid_objects,
            corrupted = summary.corrupted_objects,
            missing = summary.missing_objects,
            duration_ms = summary.duration_ms,
            "scrub complete"
        );

        Ok(summary)
    }

    /// List objects with pagination (for GC)
    /// Returns (objects, next_cursor) where objects is (hash, size, created_at_unix)
    pub fn list_objects(&self, cursor: Option<&str>, limit: usize) -> ObjectListPage {
        let mut objects: Vec<(String, u64)> = self
            .index
            .iter()
            .map(|e| (e.key().clone(), *e.value()))
            .collect();

        // Sort for consistent pagination
        objects.sort_by(|a, b| a.0.cmp(&b.0));

        // Find start position based on cursor
        let start = match cursor {
            Some(c) => objects
                .iter()
                .position(|(h, _)| h.as_str() > c)
                .unwrap_or(objects.len()),
            None => 0,
        };

        let end = (start + limit).min(objects.len());

        // Get creation times from file metadata
        let result: Vec<ObjectListEntry> = objects[start..end]
            .iter()
            .map(|(hash, size)| {
                let created_at = self.get_created_at(hash);
                (hash.clone(), *size, created_at)
            })
            .collect();

        let next_cursor = if end < objects.len() {
            result.last().map(|(h, _, _)| h.clone())
        } else {
            None
        };

        (result, next_cursor)
    }

    /// Get file creation time as Unix timestamp
    fn get_created_at(&self, hash: &str) -> Option<i64> {
        let base_path = self.hash_to_path(hash);
        let compressed_path = base_path.with_extension("zst");

        if validate_object_parent(&base_path).is_err() {
            return None;
        }

        let metadata = std::fs::symlink_metadata(&compressed_path)
            .or_else(|_| std::fs::symlink_metadata(&base_path))
            .ok()?;
        if !metadata.is_file() || metadata.file_type().is_symlink() {
            return None;
        }
        metadata_timestamp_to_unix(metadata.created().ok(), metadata.modified().ok())
    }

    /// List all object hashes (for GC reference counting)
    pub fn list_hashes(&self) -> Vec<String> {
        self.index.iter().map(|e| e.key().clone()).collect()
    }

    /// Get root path
    pub fn root(&self) -> &PathBuf {
        &self.config.root
    }

    /// Convert hash to file path
    fn hash_to_path(&self, hash: &str) -> PathBuf {
        debug_assert!(is_valid_hash(hash), "invalid CAS hash");

        let mut path = self.config.root.clone();

        // Shard directories
        let mut offset = 0;
        for _ in 0..self.config.shard_levels {
            let end = offset + self.config.shard_size;
            if end <= hash.len() {
                path.push(&hash[offset..end]);
                offset = end;
            }
        }

        path.push(hash);
        path
    }

    fn parse_index_hash_from_path(&self, path: &Path) -> Option<(String, bool)> {
        let relative = path.strip_prefix(&self.config.root).ok()?;
        let components: Vec<&str> = relative
            .iter()
            .map(|component| component.to_str())
            .collect::<Option<Vec<_>>>()?;

        if components.len() != self.config.shard_levels + 1 {
            return None;
        }

        let file_name = *components.last()?;
        let (hash, compressed) = match file_name.strip_suffix(".zst") {
            Some(hash) => (hash, true),
            None => (file_name, false),
        };

        if !is_valid_hash(hash) {
            return None;
        }

        for (index, shard) in components[..self.config.shard_levels].iter().enumerate() {
            if shard.len() != self.config.shard_size {
                return None;
            }
            let start = index * self.config.shard_size;
            let end = start + self.config.shard_size;
            if end > hash.len() {
                return None;
            }
            if &hash[start..end] != *shard {
                return None;
            }
        }

        Some((hash.to_string(), compressed))
    }

    /// Rebuild memory index
    async fn rebuild_index(&self) -> Result<()> {
        info!("rebuilding CAS index...");

        self.index.clear();

        let mut count = 0u64;
        let mut original_size = 0u64;
        let mut disk_size = 0u64;
        let mut compressed_count = 0u64;

        let mut stack = vec![self.config.root.clone()];

        while let Some(dir) = stack.pop() {
            let mut entries = match fs::read_dir(&dir).await {
                Ok(e) => e,
                Err(_) => continue,
            };

            while let Some(entry) = entries.next_entry().await? {
                let path = entry.path();
                let file_type = entry.file_type().await?;

                if file_type.is_symlink() {
                    continue;
                }

                if file_type.is_dir() {
                    stack.push(path);
                } else if file_type.is_file() {
                    let file_name = path
                        .file_name()
                        .and_then(|n| n.to_str())
                        .unwrap_or_default();

                    // Skip temp files
                    if file_name.ends_with(".tmp") {
                        continue;
                    }

                    let Some((hash, compressed)) = self.parse_index_hash_from_path(&path) else {
                        tracing::warn!(
                            path = %path.display(),
                            "ignoring non-object file during index rebuild"
                        );
                        continue;
                    };

                    let metadata = entry.metadata().await?;

                    let file_size = metadata.len();
                    disk_size += file_size;

                    // Handle compressed vs uncompressed files
                    if compressed {
                        // Try to get original size from zstd frame header
                        match std::fs::read(&path) {
                            Ok(data) => {
                                // zstd::decode_all gives us the original data
                                match zstd::decode_all(std::io::Cursor::new(&data)) {
                                    Ok(decompressed) => {
                                        let orig_size = decompressed.len() as u64;
                                        self.index.insert(hash.to_string(), orig_size);
                                        original_size += orig_size;
                                        compressed_count += 1;
                                    }
                                    Err(e) => {
                                        tracing::warn!(
                                            path = %path.display(),
                                            error = %e,
                                            "failed to decompress file during index rebuild"
                                        );
                                        // Store file size as fallback
                                        self.index.insert(hash.clone(), file_size);
                                        original_size += file_size;
                                    }
                                }
                            }
                            Err(e) => {
                                tracing::warn!(
                                    path = %path.display(),
                                    error = %e,
                                    "failed to read file during index rebuild"
                                );
                                continue;
                            }
                        }
                        count += 1;
                    } else {
                        // Uncompressed file
                        self.index.insert(hash, file_size);
                        original_size += file_size;
                        count += 1;
                    }
                }
            }
        }

        self.stats.total_chunks.store(count, Ordering::Relaxed);
        self.stats
            .logical_size
            .store(original_size, Ordering::Relaxed);
        self.stats
            .total_size
            .store(original_size, Ordering::Relaxed);
        self.stats
            .compressed_size
            .store(disk_size, Ordering::Relaxed);

        let ratio = if original_size > 0 {
            disk_size as f64 / original_size as f64
        } else {
            1.0
        };

        info!(
            chunks = count,
            original_size,
            disk_size,
            compressed_count,
            compression_ratio = format!("{:.1}%", ratio * 100.0),
            "index rebuild complete"
        );
        Ok(())
    }
}

/// Compute BLAKE3 hash
pub fn compute_hash(data: &[u8]) -> String {
    blake3::hash(data).to_hex().to_string()
}

/// Validate a BLAKE3 hex object hash used by external CAS callers.
pub fn validate_hash(hash: &str) -> Result<()> {
    if is_valid_hash(hash) {
        Ok(())
    } else {
        Err(CasError::InvalidHash)
    }
}

/// Returns true when hash is a fixed-width hexadecimal BLAKE3 digest.
pub fn is_valid_hash(hash: &str) -> bool {
    hash.len() == blake3::OUT_LEN * 2 && hash.bytes().all(|byte| byte.is_ascii_hexdigit())
}

fn path_contains_parent_segment(path: &Path) -> bool {
    path.components()
        .any(|component| matches!(component, Component::ParentDir))
}

fn is_protected_system_directory(path: &Path) -> bool {
    #[cfg(unix)]
    {
        let normalized = normalize_path_for_compare(path);
        [
            Path::new("/"),
            Path::new("/bin"),
            Path::new("/boot"),
            Path::new("/dev"),
            Path::new("/etc"),
            Path::new("/home"),
            Path::new("/lib"),
            Path::new("/lib64"),
            Path::new("/media"),
            Path::new("/mnt"),
            Path::new("/opt"),
            Path::new("/proc"),
            Path::new("/root"),
            Path::new("/run"),
            Path::new("/sbin"),
            Path::new("/srv"),
            Path::new("/sys"),
            Path::new("/tmp"),
            Path::new("/usr"),
            Path::new("/usr/local"),
            Path::new("/usr/local/bin"),
            Path::new("/usr/local/share"),
            Path::new("/var"),
        ]
        .iter()
        .any(|protected| normalized == *protected)
    }
    #[cfg(not(unix))]
    {
        let _ = path;
        false
    }
}

fn normalize_path_for_compare(path: &Path) -> PathBuf {
    let mut normalized = PathBuf::new();
    for component in path.components() {
        match component {
            Component::CurDir => {}
            Component::ParentDir => normalized.push(component.as_os_str()),
            Component::Prefix(_) | Component::RootDir | Component::Normal(_) => {
                normalized.push(component.as_os_str());
            }
        }
    }
    if normalized.as_os_str().is_empty() {
        PathBuf::from(".")
    } else {
        normalized
    }
}

fn validate_no_symlink_ancestors(path: &Path) -> Result<()> {
    let mut current = PathBuf::new();

    for component in path.components() {
        match component {
            Component::CurDir => continue,
            Component::ParentDir => {
                return Err(CasError::InvalidConfig(
                    "root directory cannot contain parent directory segments".to_string(),
                ));
            }
            Component::Prefix(_) | Component::RootDir => {
                current.push(component.as_os_str());
                continue;
            }
            Component::Normal(_) => {
                current.push(component.as_os_str());
            }
        }

        match std::fs::symlink_metadata(&current) {
            Ok(metadata) => {
                if metadata.file_type().is_symlink() {
                    return Err(CasError::InvalidConfig(format!(
                        "root directory component is a symlink: {}",
                        current.display()
                    )));
                }
            }
            Err(err) if err.kind() == std::io::ErrorKind::NotFound => return Ok(()),
            Err(err) => return Err(CasError::Io(err)),
        }
    }

    Ok(())
}

fn validate_object_parent(path: &Path) -> Result<()> {
    let parent = path.parent().ok_or_else(|| {
        CasError::Io(std::io::Error::other("object path has no parent directory"))
    })?;
    validate_no_symlink_ancestors(parent)
}

async fn create_new_file_no_follow(path: &Path) -> std::io::Result<fs::File> {
    #[cfg(unix)]
    {
        create_new_file_no_follow_unix(path).map(fs::File::from_std)
    }
    #[cfg(not(unix))]
    {
        let metadata = match std::fs::symlink_metadata(path) {
            Ok(metadata) => Some(metadata),
            Err(err) if err.kind() == std::io::ErrorKind::NotFound => None,
            Err(err) => return Err(err),
        };
        if let Some(metadata) = metadata {
            if metadata.file_type().is_symlink() {
                return Err(std::io::Error::new(
                    std::io::ErrorKind::PermissionDenied,
                    "object path resolves through a symlink",
                ));
            }
        }
        fs::OpenOptions::new()
            .create_new(true)
            .write(true)
            .open(path)
            .await
    }
}

async fn rename_file_no_follow(src: &Path, dst: &Path) -> std::io::Result<()> {
    #[cfg(unix)]
    {
        rename_file_no_follow_unix(src, dst)
    }
    #[cfg(not(unix))]
    {
        fs::rename(src, dst).await
    }
}

async fn remove_file_no_follow(path: &Path) -> std::io::Result<()> {
    #[cfg(unix)]
    {
        unlink_file_no_follow_unix(path)
    }
    #[cfg(not(unix))]
    {
        fs::remove_file(path).await
    }
}

async fn sync_dir_no_follow(path: &Path) -> std::io::Result<()> {
    #[cfg(unix)]
    {
        let path = path.to_path_buf();
        tokio::task::spawn_blocking(move || {
            let dir = open_dir_no_follow_unix(&path)?;
            dir.sync_all()
        })
        .await
        .map_err(|err| std::io::Error::other(format!("directory sync task failed: {err}")))?
    }
    #[cfg(not(unix))]
    {
        let dir = fs::File::open(path).await?;
        dir.sync_all().await
    }
}

#[cfg(unix)]
fn path_component_cstring(
    component: &std::ffi::OsStr,
    context: &'static str,
) -> std::io::Result<std::ffi::CString> {
    use std::os::unix::ffi::OsStrExt;

    std::ffi::CString::new(component.as_bytes())
        .map_err(|_| std::io::Error::new(std::io::ErrorKind::InvalidInput, context))
}

#[cfg(unix)]
fn open_dir_no_follow_unix(path: &Path) -> std::io::Result<std::fs::File> {
    use std::os::fd::{AsRawFd, FromRawFd};
    use std::os::unix::fs::OpenOptionsExt;

    let start = if path.is_absolute() {
        Path::new("/")
    } else {
        Path::new(".")
    };
    let mut dir = std::fs::OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_CLOEXEC | libc::O_DIRECTORY | libc::O_NOFOLLOW)
        .open(start)?;

    for component in path.components() {
        match component {
            Component::RootDir | Component::CurDir => continue,
            Component::Normal(name) => {
                let name = path_component_cstring(name, "path component contains NUL")?;
                let fd = unsafe {
                    libc::openat(
                        dir.as_raw_fd(),
                        name.as_ptr(),
                        libc::O_RDONLY | libc::O_CLOEXEC | libc::O_DIRECTORY | libc::O_NOFOLLOW,
                    )
                };
                if fd < 0 {
                    return Err(std::io::Error::last_os_error());
                }
                dir = unsafe { std::fs::File::from_raw_fd(fd) };
            }
            Component::ParentDir | Component::Prefix(_) => {
                return Err(std::io::Error::new(
                    std::io::ErrorKind::InvalidInput,
                    "path escapes CAS root",
                ));
            }
        }
    }

    Ok(dir)
}

#[cfg(unix)]
fn open_parent_dir_no_follow_unix(
    path: &Path,
) -> std::io::Result<(std::fs::File, std::ffi::CString)> {
    let parent = path
        .parent()
        .ok_or_else(|| std::io::Error::other("object path has no parent directory"))?;
    let file_name = path.file_name().ok_or_else(|| {
        std::io::Error::new(
            std::io::ErrorKind::InvalidInput,
            "object path has no file name",
        )
    })?;
    let leaf = path_component_cstring(file_name, "file name contains NUL")?;
    let dir = open_dir_no_follow_unix(parent)?;
    Ok((dir, leaf))
}

#[cfg(unix)]
fn create_new_file_no_follow_unix(path: &Path) -> std::io::Result<std::fs::File> {
    use std::os::fd::{AsRawFd, FromRawFd};

    let (dir, leaf) = open_parent_dir_no_follow_unix(path)?;
    let fd = unsafe {
        libc::openat(
            dir.as_raw_fd(),
            leaf.as_ptr(),
            libc::O_WRONLY | libc::O_CREAT | libc::O_EXCL | libc::O_CLOEXEC | libc::O_NOFOLLOW,
            0o600,
        )
    };
    if fd < 0 {
        return Err(std::io::Error::last_os_error());
    }
    Ok(unsafe { std::fs::File::from_raw_fd(fd) })
}

#[cfg(unix)]
fn rename_file_no_follow_unix(src: &Path, dst: &Path) -> std::io::Result<()> {
    use std::os::fd::AsRawFd;

    if src.parent() != dst.parent() {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidInput,
            "CAS object rename requires same parent directory",
        ));
    }
    let (dir, src_leaf) = open_parent_dir_no_follow_unix(src)?;
    let dst_name = dst.file_name().ok_or_else(|| {
        std::io::Error::new(
            std::io::ErrorKind::InvalidInput,
            "object path has no file name",
        )
    })?;
    let dst_leaf = path_component_cstring(dst_name, "file name contains NUL")?;
    let result = unsafe {
        libc::renameat(
            dir.as_raw_fd(),
            src_leaf.as_ptr(),
            dir.as_raw_fd(),
            dst_leaf.as_ptr(),
        )
    };
    if result < 0 {
        return Err(std::io::Error::last_os_error());
    }
    Ok(())
}

#[cfg(unix)]
fn unlink_file_no_follow_unix(path: &Path) -> std::io::Result<()> {
    use std::os::fd::AsRawFd;

    let (dir, leaf) = open_parent_dir_no_follow_unix(path)?;
    let result = unsafe { libc::unlinkat(dir.as_raw_fd(), leaf.as_ptr(), 0) };
    if result < 0 {
        return Err(std::io::Error::last_os_error());
    }
    Ok(())
}

async fn read_regular_file_no_follow(path: &Path) -> Result<Option<Vec<u8>>> {
    let path = path.to_path_buf();
    tokio::task::spawn_blocking(move || read_regular_file_no_follow_blocking(&path))
        .await
        .map_err(|err| {
            CasError::Io(std::io::Error::other(format!(
                "object read task failed: {err}"
            )))
        })?
}

#[cfg(unix)]
fn read_regular_file_no_follow_blocking(path: &Path) -> Result<Option<Vec<u8>>> {
    use std::io::Read;
    use std::os::fd::{AsRawFd, FromRawFd};

    let (dir, leaf) = match open_parent_dir_no_follow_unix(path) {
        Ok(parent) => parent,
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(err) if is_unix_no_follow_rejection(&err) => return Ok(None),
        Err(err) => return Err(CasError::Io(err)),
    };
    let fd = unsafe {
        libc::openat(
            dir.as_raw_fd(),
            leaf.as_ptr(),
            libc::O_RDONLY | libc::O_CLOEXEC | libc::O_NOFOLLOW,
        )
    };
    let mut file = match (fd < 0).then(std::io::Error::last_os_error) {
        None => unsafe { std::fs::File::from_raw_fd(fd) },
        Some(err) if err.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Some(err) if is_unix_no_follow_rejection(&err) => return Ok(None),
        Some(err) => return Err(CasError::Io(err)),
    };

    let metadata = file.metadata().map_err(CasError::Io)?;
    if !metadata.is_file() {
        return Ok(None);
    }

    let mut data = Vec::new();
    file.read_to_end(&mut data).map_err(CasError::Io)?;
    Ok(Some(data))
}

#[cfg(unix)]
fn is_unix_no_follow_rejection(err: &std::io::Error) -> bool {
    matches!(err.raw_os_error(), Some(libc::ELOOP) | Some(libc::ENOTDIR))
}

#[cfg(not(unix))]
fn read_regular_file_no_follow_blocking(path: &Path) -> Result<Option<Vec<u8>>> {
    use std::io::Read;

    let metadata = match std::fs::symlink_metadata(path) {
        Ok(file) => file,
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(err) => return Err(CasError::Io(err)),
    };
    if !metadata.is_file() || metadata.file_type().is_symlink() {
        return Ok(None);
    }

    let mut file = std::fs::File::open(path).map_err(CasError::Io)?;
    let mut data = Vec::new();
    file.read_to_end(&mut data).map_err(CasError::Io)?;
    Ok(Some(data))
}

fn system_time_to_unix_timestamp(time: SystemTime) -> Option<i64> {
    let duration = time.duration_since(UNIX_EPOCH).ok()?;
    duration_to_unix_timestamp(duration)
}

fn metadata_timestamp_to_unix(
    created: Option<SystemTime>,
    modified: Option<SystemTime>,
) -> Option<i64> {
    created
        .and_then(system_time_to_unix_timestamp)
        .or_else(|| modified.and_then(system_time_to_unix_timestamp))
}

fn duration_to_unix_timestamp(duration: Duration) -> Option<i64> {
    match i64::try_from(duration.as_secs()) {
        Ok(timestamp) => Some(timestamp),
        Err(_) => {
            warn!(
                seconds = duration.as_secs(),
                "file creation time exceeds i64 unix timestamp range; omitting created_at"
            );
            None
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[cfg(unix)]
    use std::os::unix::fs::PermissionsExt;
    use tempfile::tempdir;

    fn list_tmp_files(dir: &Path) -> Vec<PathBuf> {
        std::fs::read_dir(dir)
            .unwrap()
            .filter_map(|entry| entry.ok().map(|entry| entry.path()))
            .filter(|path| {
                path.file_name()
                    .and_then(|name| name.to_str())
                    .map(|name| name.ends_with(".tmp"))
                    .unwrap_or(false)
            })
            .collect()
    }

    #[test]
    fn test_cas_config_rejects_empty_root_parent_segments_and_bad_sharding() {
        let dir = tempdir().unwrap();

        let err = CasConfig {
            root: PathBuf::new(),
            ..Default::default()
        }
        .validate()
        .expect_err("empty root should be rejected");
        assert!(matches!(err, CasError::InvalidConfig(_)));

        let err = CasConfig {
            root: PathBuf::from("data/../objects"),
            ..Default::default()
        }
        .validate()
        .expect_err("parent segments should be rejected");
        assert!(matches!(err, CasError::InvalidConfig(_)));

        let err = CasConfig {
            root: dir.path().join("cas"),
            shard_levels: 1,
            shard_size: 0,
            ..Default::default()
        }
        .validate()
        .expect_err("zero shard size should be rejected");
        assert!(matches!(err, CasError::InvalidConfig(_)));

        let err = CasConfig {
            root: dir.path().join("cas"),
            shard_levels: blake3::OUT_LEN * 2 + 1,
            shard_size: 1,
            ..Default::default()
        }
        .validate()
        .expect_err("oversized sharding should be rejected");
        assert!(matches!(err, CasError::InvalidConfig(_)));

        let err = CasConfig {
            root: dir.path().join("cas"),
            compression_level: 23,
            ..Default::default()
        }
        .validate()
        .expect_err("invalid compression level should be rejected");
        assert!(matches!(err, CasError::InvalidConfig(_)));
    }

    #[cfg(unix)]
    #[test]
    fn test_cas_config_rejects_protected_system_roots() {
        for root in ["/", "/tmp", "/usr/local/bin"] {
            let err = CasConfig {
                root: PathBuf::from(root),
                ..Default::default()
            }
            .validate()
            .expect_err("protected root should be rejected");

            assert!(
                matches!(err, CasError::InvalidConfig(_)),
                "unexpected error for {root}: {err:?}"
            );
        }
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn test_cas_new_rejects_symlink_root_ancestor() {
        let dir = tempdir().unwrap();
        let real_parent = dir.path().join("real-parent");
        let linked_parent = dir.path().join("linked-parent");
        std::fs::create_dir(&real_parent).unwrap();
        std::os::unix::fs::symlink(&real_parent, &linked_parent).unwrap();

        let err = match CasStore::new(CasConfig {
            root: linked_parent.join("cas"),
            compression_enabled: false,
            ..Default::default()
        })
        .await
        {
            Ok(_) => panic!("symlink ancestor should be rejected"),
            Err(err) => err,
        };

        assert!(matches!(err, CasError::InvalidConfig(_)));
        assert!(
            !real_parent.join("cas").exists(),
            "CAS root should not be created through a symlink ancestor"
        );
    }

    #[test]
    fn test_hash_validation_rejects_path_like_values() {
        assert!(is_valid_hash(&"a".repeat(blake3::OUT_LEN * 2)));
        assert!(is_valid_hash(&"A".repeat(blake3::OUT_LEN * 2)));

        for hash in [
            "",
            "abc",
            "/etc/passwd",
            "../../outside",
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaag",
        ] {
            assert!(
                !is_valid_hash(hash),
                "expected path-like or malformed hash to be rejected: {hash:?}"
            );
            assert!(matches!(validate_hash(hash), Err(CasError::InvalidHash)));
        }
    }

    #[tokio::test]
    async fn test_cas_public_methods_reject_invalid_hash_paths() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().join("cas"),
            compression_enabled: false,
            ..Default::default()
        };
        let store = CasStore::new(config).await.unwrap();
        let outside_path = dir.path().join("outside.txt");
        std::fs::write(&outside_path, b"outside").unwrap();
        let invalid_hash = outside_path.to_string_lossy().to_string();

        assert!(matches!(
            store.get(&invalid_hash).await,
            Err(CasError::InvalidHash)
        ));
        assert!(matches!(
            store.delete(&invalid_hash).await,
            Err(CasError::InvalidHash)
        ));
        assert!(!store.has(&invalid_hash));
        assert_eq!(store.size(&invalid_hash), None);

        let hashes = vec![invalid_hash];
        assert!(matches!(
            store.scrub(Some(&hashes)).await,
            Err(CasError::InvalidHash)
        ));
    }

    #[tokio::test]
    async fn test_cas_basic() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().to_path_buf(),
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();

        // Store (data is small, won't be compressed by default)
        let data = b"Hello, MnemoNAS!";
        let hash = store.put(data).await.unwrap();

        assert!(store.has(&hash));
        assert_eq!(store.size(&hash), Some(data.len() as u64));

        // Read
        let retrieved = store.get(&hash).await.unwrap();
        assert_eq!(retrieved, data);

        // Dedup test
        let hash2 = store.put(data).await.unwrap();
        assert_eq!(hash, hash2);

        let (chunks, logical_size, unique_size, _compressed, hits, _) = store.stats();
        assert_eq!(chunks, 1);
        assert_eq!(logical_size, data.len() as u64 * 2);
        assert_eq!(unique_size, data.len() as u64);
        assert_eq!(hits, 1); // Second put hits dedup

        // Delete
        assert!(store.delete(&hash).await.unwrap());
        assert!(!store.has(&hash));
    }

    #[tokio::test]
    async fn test_put_recreates_missing_object_instead_of_returning_false_dedup() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().to_path_buf(),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        let data = b"stale index should be repaired";
        let hash = store.put(data).await.unwrap();
        let path = store.hash_to_path(&hash);

        tokio::fs::remove_file(&path).await.unwrap();

        let result = store.put_with_status(data).await.unwrap();

        assert!(
            !result.deduplicated,
            "missing object should be recreated, not treated as a dedup hit"
        );
        assert_eq!(store.get(&hash).await.unwrap(), data);

        let (chunks, logical_size, unique_size, compressed_size, hits, misses) = store.stats();
        assert_eq!(chunks, 1);
        assert_eq!(logical_size, data.len() as u64 * 2);
        assert_eq!(unique_size, data.len() as u64);
        assert_eq!(compressed_size, data.len() as u64);
        assert_eq!(hits, 0);
        assert_eq!(misses, 0);
    }

    #[tokio::test]
    async fn test_failed_put_does_not_inflate_logical_size() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().to_path_buf(),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        store.set_fail_put_after(0);

        let err = store
            .put(b"failed put should not count")
            .await
            .expect_err("expected injected put failure");

        assert!(matches!(err, CasError::Io(_)));

        let (chunks, logical_size, unique_size, compressed_size, hits, misses) = store.stats();
        assert_eq!(chunks, 0);
        assert_eq!(logical_size, 0);
        assert_eq!(unique_size, 0);
        assert_eq!(compressed_size, 0);
        assert_eq!(hits, 0);
        assert_eq!(misses, 0);
    }

    #[tokio::test]
    async fn test_put_surfaces_parent_sync_failure_after_rename() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().to_path_buf(),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        let data = b"parent sync failure";
        let hash = compute_hash(data);
        let path = store.hash_to_path(&hash);
        let parent = path.parent().unwrap().to_path_buf();

        store.fail_next_parent_sync();

        let err = store
            .put(data)
            .await
            .expect_err("expected injected parent sync failure");

        assert!(matches!(err, CasError::Io(_)));
        assert!(
            path.exists(),
            "renamed object should remain visible on disk"
        );
        assert!(
            list_tmp_files(&parent).is_empty(),
            "temporary files should not remain after rename"
        );
        assert!(
            !store.has(&hash),
            "index should not claim success after failed parent sync"
        );

        let (chunks, logical_size, unique_size, compressed_size, hits, misses) = store.stats();
        assert_eq!(chunks, 0);
        assert_eq!(logical_size, 0);
        assert_eq!(unique_size, 0);
        assert_eq!(compressed_size, 0);
        assert_eq!(hits, 0);
        assert_eq!(misses, 0);

        store.rebuild_index().await.unwrap();

        assert!(
            store.has(&hash),
            "rebuild should recover the on-disk object"
        );
        assert_eq!(store.get(&hash).await.unwrap(), data);

        let (chunks, logical_size, unique_size, compressed_size, hits, misses) = store.stats();
        assert_eq!(chunks, 1);
        assert_eq!(logical_size, data.len() as u64);
        assert_eq!(unique_size, data.len() as u64);
        assert_eq!(compressed_size, data.len() as u64);
        assert_eq!(hits, 0);
        assert_eq!(misses, 0);
    }

    #[tokio::test]
    async fn test_put_uses_unique_temp_paths_when_fixed_tmp_name_exists() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().to_path_buf(),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        let data = b"stale tmp collision";
        let hash = compute_hash(data);
        let path = store.hash_to_path(&hash);
        let parent = path.parent().unwrap();
        std::fs::create_dir_all(parent).unwrap();

        let fixed_tmp_path = path.with_extension("tmp");
        std::fs::write(&fixed_tmp_path, b"stale temp").unwrap();

        let stored_hash = store.put(data).await.unwrap();

        assert_eq!(stored_hash, hash);
        assert_eq!(store.get(&hash).await.unwrap(), data);
        assert!(
            fixed_tmp_path.exists(),
            "existing fixed temp path should remain untouched"
        );

        let (chunks, logical_size, unique_size, compressed_size, hits, misses) = store.stats();
        assert_eq!(chunks, 1);
        assert_eq!(logical_size, data.len() as u64);
        assert_eq!(unique_size, data.len() as u64);
        assert_eq!(compressed_size, data.len() as u64);
        assert_eq!(hits, 0);
        assert_eq!(misses, 0);
    }

    #[tokio::test]
    async fn test_rebuild_index_replaces_stale_entries_and_ignores_non_object_files() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().to_path_buf(),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        let data = b"valid object";
        let hash = store.put(data).await.unwrap();

        let stale_hash = compute_hash(b"stale object");
        store.index.insert(stale_hash, 123);

        std::fs::write(dir.path().join("notes.txt"), b"not an object").unwrap();

        let wrong_shard_path = dir.path().join("ff").join("ee").join(&hash);
        std::fs::create_dir_all(wrong_shard_path.parent().unwrap()).unwrap();
        std::fs::write(&wrong_shard_path, b"wrong shard object").unwrap();

        store.rebuild_index().await.unwrap();

        let mut hashes = store.list_hashes();
        hashes.sort();
        assert_eq!(hashes, vec![hash.clone()]);
        assert_eq!(store.get(&hash).await.unwrap(), data);

        let (chunks, logical_size, unique_size, compressed_size, hits, misses) = store.stats();
        assert_eq!(chunks, 1);
        assert_eq!(logical_size, data.len() as u64);
        assert_eq!(unique_size, data.len() as u64);
        assert_eq!(compressed_size, data.len() as u64);
        assert_eq!(hits, 0);
        assert_eq!(misses, 0);
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn test_rebuild_index_ignores_symlinked_objects() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().to_path_buf(),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        let data = b"external symlinked object";
        let hash = compute_hash(data);
        let external_path = dir.path().join("external-object");
        std::fs::write(&external_path, data).unwrap();

        let symlink_path = store.hash_to_path(&hash);
        std::fs::create_dir_all(symlink_path.parent().unwrap()).unwrap();
        std::os::unix::fs::symlink(&external_path, &symlink_path).unwrap();

        store.rebuild_index().await.unwrap();

        assert!(!store.has(&hash));
        let (chunks, logical_size, unique_size, compressed_size, hits, misses) = store.stats();
        assert_eq!(chunks, 0);
        assert_eq!(logical_size, 0);
        assert_eq!(unique_size, 0);
        assert_eq!(compressed_size, 0);
        assert_eq!(hits, 0);
        assert_eq!(misses, 0);
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn test_get_ignores_symlinked_object_paths() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().to_path_buf(),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        let data = b"external object data";
        let hash = store.put(data).await.unwrap();
        let object_path = store.hash_to_path(&hash);
        let external_path = dir.path().join("external-object");

        tokio::fs::remove_file(&object_path).await.unwrap();
        std::fs::write(&external_path, data).unwrap();
        std::os::unix::fs::symlink(&external_path, &object_path).unwrap();

        assert!(matches!(store.get(&hash).await, Err(CasError::NotFound(_))));
        assert!(!store.object_exists(&hash).await.unwrap());
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn test_read_object_file_does_not_follow_symlink_inserted_after_metadata() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().to_path_buf(),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        let data = b"external object swapped after metadata";
        let hash = compute_hash(data);
        let object_path = store.hash_to_path(&hash);
        std::fs::create_dir_all(object_path.parent().unwrap()).unwrap();
        std::fs::write(&object_path, b"placeholder before race").unwrap();

        let external_path = dir.path().join("external-object-after-metadata");
        std::fs::write(&external_path, data).unwrap();
        let hook_object_path = object_path.clone();
        store.set_before_object_file_open(move || {
            std::fs::remove_file(&hook_object_path).unwrap();
            std::os::unix::fs::symlink(&external_path, &hook_object_path).unwrap();
        });

        let read = store.read_object_file(&object_path).await.unwrap();
        assert!(
            read.is_none(),
            "CAS object reads must not follow a symlink inserted after metadata validation"
        );
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn test_get_does_not_read_through_parent_symlink_inserted_after_metadata() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().join("cas"),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        let data = b"do not read through swapped symlink parent";
        let hash = store.put(data).await.unwrap();
        let object_path = store.hash_to_path(&hash);
        let object_parent = object_path.parent().unwrap().to_path_buf();
        let backup_parent = dir.path().join("object-parent-read-backup");
        let outside_parent = dir.path().join("outside-parent-read");

        store.set_before_object_file_open({
            let object_parent = object_parent.clone();
            let backup_parent = backup_parent.clone();
            let outside_parent = outside_parent.clone();
            let hash = hash.clone();
            move || {
                std::fs::rename(&object_parent, &backup_parent).unwrap();
                std::fs::create_dir(&outside_parent).unwrap();
                std::fs::write(outside_parent.join(&hash), data).unwrap();
                std::os::unix::fs::symlink(&outside_parent, &object_parent).unwrap();
            }
        });

        assert!(matches!(store.get(&hash).await, Err(CasError::NotFound(_))));
        assert!(
            outside_parent.join(&hash).exists(),
            "read path should not alter the outside object"
        );
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn test_get_ignores_symlinked_object_parent() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().join("cas"),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        let data = b"external parent object";
        let hash = store.put(data).await.unwrap();
        let object_path = store.hash_to_path(&hash);
        let object_parent = object_path.parent().unwrap().to_path_buf();
        let outside_parent = dir.path().join("outside-parent");

        tokio::fs::remove_dir_all(&object_parent).await.unwrap();
        std::fs::create_dir(&outside_parent).unwrap();
        std::fs::write(outside_parent.join(&hash), data).unwrap();
        std::os::unix::fs::symlink(&outside_parent, &object_parent).unwrap();

        assert!(matches!(store.get(&hash).await, Err(CasError::NotFound(_))));
        assert!(!store.object_exists(&hash).await.unwrap());
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn test_put_rejects_symlinked_object_parent() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().join("cas"),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        let data = b"do not write through symlinked parent";
        let hash = compute_hash(data);
        let object_path = store.hash_to_path(&hash);
        let object_parent = object_path.parent().unwrap().to_path_buf();
        let shard_parent = object_parent.parent().unwrap();
        let outside_parent = dir.path().join("outside-parent");

        std::fs::create_dir_all(shard_parent).unwrap();
        std::fs::create_dir(&outside_parent).unwrap();
        std::os::unix::fs::symlink(&outside_parent, &object_parent).unwrap();

        let err = store
            .put(data)
            .await
            .expect_err("symlinked parent should fail");

        assert!(matches!(err, CasError::InvalidConfig(_)));
        assert!(
            !outside_parent.join(&hash).exists(),
            "CAS put should not write through a symlinked object parent"
        );
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn test_put_does_not_follow_parent_symlink_inserted_before_temp_create() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().join("cas"),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        let data = b"do not create temp through swapped symlink parent";
        let hash = compute_hash(data);
        let object_path = store.hash_to_path(&hash);
        let object_parent = object_path.parent().unwrap().to_path_buf();
        let outside_parent = dir.path().join("outside-parent-create");

        store.set_before_temp_file_create({
            let object_parent = object_parent.clone();
            let outside_parent = outside_parent.clone();
            move || {
                std::fs::remove_dir_all(&object_parent).unwrap();
                std::fs::create_dir(&outside_parent).unwrap();
                std::os::unix::fs::symlink(&outside_parent, &object_parent).unwrap();
            }
        });

        let err = store
            .put(data)
            .await
            .expect_err("symlinked parent inserted before temp create should fail");

        assert!(matches!(err, CasError::Io(_) | CasError::InvalidConfig(_)));
        assert!(
            !outside_parent.join(&hash).exists(),
            "CAS put should not create the final object through a swapped symlink parent"
        );
        let outside_entries = std::fs::read_dir(&outside_parent).unwrap().count();
        assert_eq!(outside_entries, 0, "outside parent should remain empty");
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn test_put_does_not_rename_through_parent_symlink_inserted_after_temp_create() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().join("cas"),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        let data = b"do not rename through swapped symlink parent";
        let hash = compute_hash(data);
        let object_path = store.hash_to_path(&hash);
        let object_parent = object_path.parent().unwrap().to_path_buf();
        let backup_parent = dir.path().join("object-parent-backup");
        let outside_parent = dir.path().join("outside-parent-rename");

        store.set_before_object_rename({
            let object_parent = object_parent.clone();
            let backup_parent = backup_parent.clone();
            let outside_parent = outside_parent.clone();
            move || {
                std::fs::rename(&object_parent, &backup_parent).unwrap();
                std::fs::create_dir(&outside_parent).unwrap();
                std::os::unix::fs::symlink(&outside_parent, &object_parent).unwrap();
            }
        });

        let err = store
            .put(data)
            .await
            .expect_err("symlinked parent inserted before rename should fail");

        assert!(matches!(err, CasError::Io(_) | CasError::InvalidConfig(_)));
        assert!(
            !outside_parent.join(&hash).exists(),
            "CAS put should not rename the object through a swapped symlink parent"
        );
        let outside_entries = std::fs::read_dir(&outside_parent).unwrap().count();
        assert_eq!(outside_entries, 0, "outside parent should remain empty");
        assert!(
            !store.has(&hash),
            "index should not claim success after swapped-parent rename failure"
        );
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn test_delete_does_not_unlink_through_parent_symlink_inserted_after_validation() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().join("cas"),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        let data = b"do not delete through swapped symlink parent";
        let hash = store.put(data).await.unwrap();
        let object_path = store.hash_to_path(&hash);
        let object_parent = object_path.parent().unwrap().to_path_buf();
        let backup_parent = dir.path().join("object-parent-delete-backup");
        let outside_parent = dir.path().join("outside-parent-delete");

        store.set_before_object_delete({
            let object_parent = object_parent.clone();
            let backup_parent = backup_parent.clone();
            let outside_parent = outside_parent.clone();
            let hash = hash.clone();
            move || {
                std::fs::rename(&object_parent, &backup_parent).unwrap();
                std::fs::create_dir(&outside_parent).unwrap();
                std::fs::write(outside_parent.join(&hash), data).unwrap();
                std::os::unix::fs::symlink(&outside_parent, &object_parent).unwrap();
            }
        });

        assert!(
            !store.delete(&hash).await.unwrap(),
            "delete should fail closed when the parent is swapped to a symlink"
        );
        assert!(
            outside_parent.join(&hash).exists(),
            "delete must not unlink the outside object"
        );
        assert!(
            store.has(&hash),
            "index should remain intact when filesystem delete is rejected"
        );
    }

    #[tokio::test]
    async fn test_delete_surfaces_parent_sync_failure_after_remove() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().to_path_buf(),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        let data = b"delete parent sync failure";
        let hash = store.put(data).await.unwrap();
        let path = store.hash_to_path(&hash);

        store.fail_next_parent_sync();

        let err = store
            .delete(&hash)
            .await
            .expect_err("expected injected parent sync failure");

        assert!(matches!(err, CasError::Io(_)));
        assert!(
            !path.exists(),
            "deleted object should remain absent after sync failure"
        );
        assert!(
            !store.has(&hash),
            "index should reflect the visible deletion"
        );

        let (chunks, logical_size, unique_size, _, _, _) = store.stats();
        assert_eq!(chunks, 0);
        assert_eq!(logical_size, data.len() as u64);
        assert_eq!(unique_size, 0);

        assert!(matches!(store.get(&hash).await, Err(CasError::NotFound(_))));
        assert!(
            !store.delete(&hash).await.unwrap(),
            "retry should observe already-deleted object"
        );
    }

    #[tokio::test]
    async fn test_cas_compression() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().to_path_buf(),
            compression_enabled: true,
            compression_level: 3,
            min_compress_size: 100, // Lower threshold for testing
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();

        // Create compressible data (repeating pattern)
        let mut data = Vec::new();
        for _ in 0..1000 {
            data.extend_from_slice(b"This is a repeating pattern for compression testing. ");
        }

        let hash = store.put(&data).await.unwrap();

        // Verify data can be retrieved correctly
        let retrieved = store.get(&hash).await.unwrap();
        assert_eq!(retrieved, data);

        // Check compression stats
        let (chunks, _logical_size, original_size, compressed_size, _, _) = store.stats();
        assert_eq!(chunks, 1);
        assert_eq!(original_size, data.len() as u64);

        // Compressed size should be significantly smaller for repetitive data
        assert!(
            compressed_size < original_size,
            "Expected compression: original={}, compressed={}",
            original_size,
            compressed_size
        );

        println!(
            "Compression test: original={}, compressed={}, ratio={:.1}%",
            original_size,
            compressed_size,
            (compressed_size as f64 / original_size as f64) * 100.0
        );
    }

    #[tokio::test]
    async fn test_cas_no_compression() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().to_path_buf(),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();

        // Store data
        let data = b"Test data without compression";
        let hash = store.put(data).await.unwrap();

        // Verify file is stored uncompressed
        let path = dir.path().join(&hash[0..2]).join(&hash[2..4]).join(&hash);
        assert!(
            path.exists(),
            "Uncompressed file should exist at {:?}",
            path
        );

        // No .zst file should exist
        let compressed_path = path.with_extension("zst");
        assert!(!compressed_path.exists(), "No .zst file should exist");

        // Verify retrieval
        let retrieved = store.get(&hash).await.unwrap();
        assert_eq!(retrieved, data);
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn test_delete_keeps_index_when_filesystem_removal_fails() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().to_path_buf(),
            compression_enabled: false,
            ..Default::default()
        };

        let store = CasStore::new(config).await.unwrap();
        let data = b"delete consistency";
        let hash = store.put(data).await.unwrap();
        let object_path = store.hash_to_path(&hash);
        let parent = object_path.parent().unwrap();

        let original_permissions = std::fs::metadata(parent).unwrap().permissions();
        let mut read_only_permissions = original_permissions.clone();
        read_only_permissions.set_mode(0o500);
        std::fs::set_permissions(parent, read_only_permissions).unwrap();

        let delete_result = store.delete(&hash).await.unwrap();

        std::fs::set_permissions(parent, original_permissions).unwrap();

        assert!(!delete_result);
        assert!(store.has(&hash));
        assert_eq!(store.size(&hash), Some(data.len() as u64));

        let (chunks, logical_size, unique_size, _, _, _) = store.stats();
        assert_eq!(chunks, 1);
        assert_eq!(logical_size, data.len() as u64);
        assert_eq!(unique_size, data.len() as u64);

        assert!(store.delete(&hash).await.unwrap());
        assert!(!store.has(&hash));
    }

    #[test]
    fn test_system_time_to_unix_timestamp_valid() {
        let timestamp = system_time_to_unix_timestamp(UNIX_EPOCH + Duration::from_secs(42));

        assert_eq!(timestamp, Some(42));
    }

    #[test]
    fn test_system_time_to_unix_timestamp_before_epoch() {
        let timestamp = system_time_to_unix_timestamp(UNIX_EPOCH - Duration::from_secs(1));

        assert_eq!(timestamp, None);
    }

    #[test]
    fn test_system_time_to_unix_timestamp_out_of_range() {
        let overflow_seconds = (i64::MAX as u64).saturating_add(1);
        let timestamp = duration_to_unix_timestamp(Duration::from_secs(overflow_seconds));

        assert_eq!(timestamp, None);
    }

    #[test]
    fn test_metadata_timestamp_to_unix_falls_back_to_modified() {
        let modified = UNIX_EPOCH + Duration::from_secs(123);

        let timestamp = metadata_timestamp_to_unix(None, Some(modified));

        assert_eq!(timestamp, Some(123));
    }

    #[test]
    fn test_metadata_timestamp_to_unix_prefers_created() {
        let created = UNIX_EPOCH + Duration::from_secs(42);
        let modified = UNIX_EPOCH + Duration::from_secs(123);

        let timestamp = metadata_timestamp_to_unix(Some(created), Some(modified));

        assert_eq!(timestamp, Some(42));
    }
}
