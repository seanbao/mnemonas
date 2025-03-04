//! CAS (Content-Addressable Storage) implementation
//! Uses BLAKE3 as hash algorithm (10x faster than SHA256)
//! Supports optional zstd compression for storage efficiency

use std::path::PathBuf;
use std::sync::atomic::{AtomicU64, Ordering};

use dashmap::DashMap;
use thiserror::Error;
use tokio::fs;
use tokio::io::AsyncWriteExt;
use tracing::{debug, info, instrument};

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
        Self {
            root: PathBuf::from("/var/lib/mnemonas/cas"),
            shard_levels: 2,
            shard_size: 2,
            compression_enabled: true,
            compression_level: 3,
            min_compress_size: 1024,  // 1KB
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
}

/// Storage statistics
#[derive(Default)]
pub struct CasStats {
    pub total_chunks: AtomicU64,
    pub total_size: AtomicU64,
    pub compressed_size: AtomicU64,
    pub hit_count: AtomicU64,
    pub miss_count: AtomicU64,
}

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
        };
        
        // Rebuild index (background task)
        store.rebuild_index().await?;
        
        Ok(store)
    }
    
    /// Store data chunk, returns hash
    #[instrument(skip(self, data), fields(size = data.len()))]
    pub async fn put(&self, data: &[u8]) -> Result<String> {
        let hash = compute_hash(data);
        let original_size = data.len() as u64;
        
        // Atomic check-and-insert using entry API (C3 fix - prevents TOCTOU race)
        use dashmap::mapref::entry::Entry;
        match self.index.entry(hash.clone()) {
            Entry::Occupied(_) => {
                // Already exists - deduplication hit
                debug!(hash = %hash, "dedup hit");
                self.stats.hit_count.fetch_add(1, Ordering::Relaxed);
                return Ok(hash);
            }
            Entry::Vacant(entry) => {
                // Determine if we should compress
                let should_compress = self.config.compression_enabled 
                    && data.len() >= self.config.min_compress_size;
                
                // Prepare data to write (possibly compressed)
                let (write_data, path) = if should_compress {
                    let compressed = zstd::encode_all(
                        std::io::Cursor::new(data),
                        self.config.compression_level,
                    ).map_err(|e| CasError::Compression(e.to_string()))?;
                    
                    // Only use compression if it actually saves space
                    if compressed.len() < data.len() {
                        let path = self.hash_to_path(&hash).with_extension("zst");
                        (compressed, path)
                    } else {
                        (data.to_vec(), self.hash_to_path(&hash))
                    }
                } else {
                    (data.to_vec(), self.hash_to_path(&hash))
                };
                
                let disk_size = write_data.len() as u64;
                
                // Create directory
                if let Some(parent) = path.parent() {
                    fs::create_dir_all(parent).await?;
                }
                
                // Atomic write
                let tmp_path = path.with_extension("tmp");
                let mut file = fs::File::create(&tmp_path).await?;
                file.write_all(&write_data).await?;
                file.sync_all().await?;
                fs::rename(&tmp_path, &path).await?;
                
                // Update index and stats
                entry.insert(original_size);
                self.stats.total_chunks.fetch_add(1, Ordering::Relaxed);
                self.stats.total_size.fetch_add(original_size, Ordering::Relaxed);
                self.stats.compressed_size.fetch_add(disk_size, Ordering::Relaxed);
                
                debug!(
                    hash = %hash,
                    original_size,
                    disk_size,
                    compressed = path.extension().map(|e| e == "zst").unwrap_or(false),
                    "stored successfully"
                );
                Ok(hash)
            }
        }
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
        
        if let Some((_, original_size)) = self.index.remove(hash) {
            // Try to delete both possible files
            let deleted_compressed = fs::remove_file(&compressed_path).await.is_ok();
            let deleted_plain = fs::remove_file(&base_path).await.is_ok();
            
            if deleted_compressed || deleted_plain {
                self.stats.total_chunks.fetch_sub(1, Ordering::Relaxed);
                self.stats.total_size.fetch_sub(original_size, Ordering::Relaxed);
                // Note: compressed_size tracking is approximate after delete
                Ok(true)
            } else {
                Ok(false)
            }
        } else {
            Ok(false)
        }
    }
    
    /// Get statistics (chunks, original_size, compressed_size, hits, misses)
    pub fn stats(&self) -> (u64, u64, u64, u64, u64) {
        (
            self.stats.total_chunks.load(Ordering::Relaxed),
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
            Some(h) => h.iter()
                .filter_map(|hash| self.index.get(hash).map(|e| (hash.clone(), *e)))
                .collect(),
            None => self.index.iter()
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
                            format!("size mismatch: expected={}, actual={}", expected_size, actual_size),
                        ));
                    }
                }
                Err(CasError::NotFound(_)) => {
                    summary.missing_objects += 1;
                    summary.errors.push((hash.clone(), "missing".to_string(), "file not found".to_string()));
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
                    summary.errors.push((hash.clone(), "io_error".to_string(), format!("read error: {}", e)));
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
    pub fn list_objects(&self, cursor: Option<&str>, limit: usize) -> (Vec<(String, u64, Option<i64>)>, Option<String>) {
        let mut objects: Vec<(String, u64)> = self.index.iter()
            .map(|e| (e.key().clone(), *e.value()))
            .collect();
        
        // Sort for consistent pagination
        objects.sort_by(|a, b| a.0.cmp(&b.0));
        
        // Find start position based on cursor
        let start = match cursor {
            Some(c) => objects.iter().position(|(h, _)| h.as_str() > c).unwrap_or(objects.len()),
            None => 0,
        };
        
        let end = (start + limit).min(objects.len());
        
        // Get creation times from file metadata
        let result: Vec<(String, u64, Option<i64>)> = objects[start..end].iter()
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

        let metadata = std::fs::metadata(&compressed_path).or_else(|_| std::fs::metadata(&base_path)).ok()?;
        metadata
            .created()
            .ok()
            .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
            .map(|d| d.as_secs() as i64)
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
    
    /// Rebuild memory index
    async fn rebuild_index(&self) -> Result<()> {
        info!("rebuilding CAS index...");
        
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
                let metadata = entry.metadata().await?;
                
                if metadata.is_dir() {
                    stack.push(path);
                } else if metadata.is_file() {
                    let file_name = path.file_name()
                        .and_then(|n| n.to_str())
                        .unwrap_or_default();
                    
                    // Skip temp files
                    if file_name.ends_with(".tmp") {
                        continue;
                    }
                    
                    let file_size = metadata.len();
                    disk_size += file_size;
                    
                    // Handle compressed vs uncompressed files
                    if file_name.ends_with(".zst") {
                        // Extract hash from filename (remove .zst extension)
                        let hash = file_name.strip_suffix(".zst").unwrap_or(file_name);
                        
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
                                        self.index.insert(hash.to_string(), file_size);
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
                        self.index.insert(file_name.to_string(), file_size);
                        original_size += file_size;
                        count += 1;
                    }
                }
            }
        }
        
        self.stats.total_chunks.store(count, Ordering::Relaxed);
        self.stats.total_size.store(original_size, Ordering::Relaxed);
        self.stats.compressed_size.store(disk_size, Ordering::Relaxed);
        
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

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;
    
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
        
        let (chunks, size, _compressed, hits, _) = store.stats();
        assert_eq!(chunks, 1);
        assert_eq!(size, data.len() as u64);
        assert_eq!(hits, 1); // Second put hits dedup
        
        // Delete
        assert!(store.delete(&hash).await.unwrap());
        assert!(!store.has(&hash));
    }
    
    #[tokio::test]
    async fn test_cas_compression() {
        let dir = tempdir().unwrap();
        let config = CasConfig {
            root: dir.path().to_path_buf(),
            compression_enabled: true,
            compression_level: 3,
            min_compress_size: 100,  // Lower threshold for testing
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
        let (chunks, original_size, compressed_size, _, _) = store.stats();
        assert_eq!(chunks, 1);
        assert_eq!(original_size, data.len() as u64);
        
        // Compressed size should be significantly smaller for repetitive data
        assert!(compressed_size < original_size, 
            "Expected compression: original={}, compressed={}", 
            original_size, compressed_size);
        
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
        let path = dir.path()
            .join(&hash[0..2])
            .join(&hash[2..4])
            .join(&hash);
        assert!(path.exists(), "Uncompressed file should exist at {:?}", path);
        
        // No .zst file should exist
        let compressed_path = path.with_extension("zst");
        assert!(!compressed_path.exists(), "No .zst file should exist");
        
        // Verify retrieval
        let retrieved = store.get(&hash).await.unwrap();
        assert_eq!(retrieved, data);
    }
}
