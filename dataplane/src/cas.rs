//! CAS (Content-Addressable Storage) implementation
//! Uses BLAKE3 as hash algorithm (10x faster than SHA256)
//! Supports optional zstd compression for storage efficiency

use std::path::{Path, PathBuf};
#[cfg(test)]
use std::sync::atomic::{AtomicBool, AtomicUsize};
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use dashmap::DashMap;
use thiserror::Error;
use tokio::fs;
use tokio::io::AsyncWriteExt;
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

/// CAS storage
pub struct CasStore {
    config: CasConfig,
    /// Memory index for fast existence check (hash -> original_size)
    index: DashMap<String, u64>,
    /// Statistics
    stats: CasStats,
    temp_nonce: AtomicU64,
    #[cfg(test)]
    fail_put_after: AtomicUsize,
    #[cfg(test)]
    fail_parent_sync: AtomicBool,
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
        // Create root directory
        fs::create_dir_all(&config.root).await?;

        info!(
            root = %config.root.display(),
            compression = config.compression_enabled,
            compression_level = config.compression_level,
            "initializing CAS storage"
        );

        let store = Self {
            config,
            index: DashMap::new(),
            stats: CasStats::default(),
            temp_nonce: AtomicU64::new(0),
            #[cfg(test)]
            fail_put_after: AtomicUsize::new(usize::MAX),
            #[cfg(test)]
            fail_parent_sync: AtomicBool::new(false),
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

        // Atomic check-and-insert using entry API (C3 fix - prevents TOCTOU race)
        use dashmap::mapref::entry::Entry;
        match self.index.entry(hash.clone()) {
            Entry::Occupied(_) => {
                debug!(hash = %hash, "dedup hit");
                self.record_dedup_hit(original_size);
                return Ok(PutResult {
                    hash,
                    deduplicated: true,
                });
            }
            Entry::Vacant(entry) => {
                #[cfg(test)]
                self.maybe_fail_put()?;

                let (write_data, path) = self.prepare_write_target(&hash, data)?;

                let disk_size = write_data.len() as u64;

                // Create directory
                if let Some(parent) = path.parent() {
                    fs::create_dir_all(parent).await?;
                }

                // Atomic write
                let (mut file, tmp_path) = self.create_temp_file(&path).await?;
                if let Err(err) = file.write_all(&write_data).await {
                    let _ = fs::remove_file(&tmp_path).await;
                    return Err(CasError::Io(err));
                }
                if let Err(err) = file.sync_all().await {
                    let _ = fs::remove_file(&tmp_path).await;
                    return Err(CasError::Io(err));
                }
                drop(file);
                if let Err(err) = fs::rename(&tmp_path, &path).await {
                    let _ = fs::remove_file(&tmp_path).await;
                    return Err(CasError::Io(err));
                }
                self.sync_parent_dir(&path).await?;

                // Update index and stats
                entry.insert(original_size);
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
        }
    }

    fn record_dedup_hit(&self, original_size: u64) {
        self.stats
            .logical_size
            .fetch_add(original_size, Ordering::Relaxed);
        self.stats.hit_count.fetch_add(1, Ordering::Relaxed);
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
        for _ in 0..8 {
            let tmp_path = self.next_temp_path(path);
            match fs::OpenOptions::new()
                .create_new(true)
                .write(true)
                .open(&tmp_path)
                .await
            {
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
        match fs::metadata(path).await {
            Ok(metadata) => Ok(metadata.is_file()),
            Err(err) if err.kind() == std::io::ErrorKind::NotFound => Ok(false),
            Err(err) => Err(CasError::Io(err)),
        }
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
        let dir = fs::File::open(parent).await?;
        dir.sync_all().await?;
        Ok(())
    }

    /// Read data chunk
    #[instrument(skip(self))]
    pub async fn get(&self, hash: &str) -> Result<Vec<u8>> {
        let base_path = self.hash_to_path(hash);
        let compressed_path = base_path.with_extension("zst");

        // Try compressed file first, then uncompressed
        let (raw_data, is_compressed) = if compressed_path.exists() {
            (fs::read(&compressed_path).await?, true)
        } else if base_path.exists() {
            (fs::read(&base_path).await?, false)
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
        self.index.contains_key(hash)
    }

    /// Get data chunk size (original uncompressed size)
    pub fn size(&self, hash: &str) -> Option<u64> {
        self.index.get(hash).map(|r| *r)
    }

    /// Delete data chunk
    #[instrument(skip(self))]
    pub async fn delete(&self, hash: &str) -> Result<bool> {
        let base_path = self.hash_to_path(hash);
        let compressed_path = base_path.with_extension("zst");

        if let Some(original_size) = self.index.get(hash).map(|r| *r) {
            // Try to delete both possible files
            let deleted_compressed = fs::remove_file(&compressed_path).await.is_ok();
            let deleted_plain = fs::remove_file(&base_path).await.is_ok();

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

        let metadata = std::fs::metadata(&compressed_path)
            .or_else(|_| std::fs::metadata(&base_path))
            .ok()?;
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

        if hash.len() != blake3::OUT_LEN * 2 || !hash.bytes().all(|byte| byte.is_ascii_hexdigit()) {
            return None;
        }

        for (index, shard) in components[..self.config.shard_levels].iter().enumerate() {
            if shard.len() != self.config.shard_size {
                return None;
            }
            let start = index * self.config.shard_size;
            let end = start + self.config.shard_size;
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
