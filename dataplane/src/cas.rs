//! CAS (Content-Addressable Storage) implementation
//! Uses BLAKE3 as hash algorithm (10x faster than SHA256)

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
}

impl Default for CasConfig {
    fn default() -> Self {
        Self {
            root: PathBuf::from("/var/lib/mnemonas/cas"),
            shard_levels: 2,
            shard_size: 2,
        }
    }
}

/// CAS storage
pub struct CasStore {
    config: CasConfig,
    /// Memory index for fast existence check
    index: DashMap<String, u64>,
    /// Statistics
    stats: CasStats,
}

/// Storage statistics
#[derive(Default)]
pub struct CasStats {
    pub total_chunks: AtomicU64,
    pub total_size: AtomicU64,
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
        
        info!(root = %config.root.display(), "initializing CAS storage");
        
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
                // Write file first before inserting to index
                let path = self.hash_to_path(&hash);
                
                // Create directory
                if let Some(parent) = path.parent() {
                    fs::create_dir_all(parent).await?;
                }
                
                // Atomic write
                let tmp_path = path.with_extension("tmp");
                let mut file = fs::File::create(&tmp_path).await?;
                file.write_all(data).await?;
                file.sync_all().await?;
                fs::rename(&tmp_path, &path).await?;
                
                // Now insert to index
                let size = data.len() as u64;
                entry.insert(size);
                self.stats.total_chunks.fetch_add(1, Ordering::Relaxed);
                self.stats.total_size.fetch_add(size, Ordering::Relaxed);
                
                debug!(hash = %hash, size, "stored successfully");
                Ok(hash)
            }
        }
    }
    
    /// Read data chunk
    #[instrument(skip(self))]
    pub async fn get(&self, hash: &str) -> Result<Vec<u8>> {
        let path = self.hash_to_path(hash);
        
        match fs::read(&path).await {
            Ok(data) => {
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
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                self.stats.miss_count.fetch_add(1, Ordering::Relaxed);
                Err(CasError::NotFound(hash.to_string()))
            }
            Err(e) => Err(e.into()),
        }
    }
    
    /// Check if data chunk exists
    pub fn has(&self, hash: &str) -> bool {
        self.index.contains_key(hash)
    }
    
    /// Get data chunk size
    pub fn size(&self, hash: &str) -> Option<u64> {
        self.index.get(hash).map(|r| *r)
    }
    
    /// Delete data chunk
    #[instrument(skip(self))]
    pub async fn delete(&self, hash: &str) -> Result<bool> {
        let path = self.hash_to_path(hash);
        
        if let Some((_, size)) = self.index.remove(hash) {
            match fs::remove_file(&path).await {
                Ok(()) => {
                    self.stats.total_chunks.fetch_sub(1, Ordering::Relaxed);
                    self.stats.total_size.fetch_sub(size, Ordering::Relaxed);
                    Ok(true)
                }
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(false),
                Err(e) => Err(e.into()),
            }
        } else {
            Ok(false)
        }
    }
    
    /// Get statistics
    pub fn stats(&self) -> (u64, u64, u64, u64) {
        (
            self.stats.total_chunks.load(Ordering::Relaxed),
            self.stats.total_size.load(Ordering::Relaxed),
            self.stats.hit_count.load(Ordering::Relaxed),
            self.stats.miss_count.load(Ordering::Relaxed),
        )
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
            
            let path = self.hash_to_path(&hash);
            
            // Check file exists and read it
            match fs::read(&path).await {
                Ok(data) => {
                    let actual_hash = compute_hash(&data);
                    let actual_size = data.len() as u64;
                    
                    if actual_hash == hash && actual_size == expected_size {
                        summary.valid_objects += 1;
                        summary.total_size += actual_size;
                    } else {
                        summary.corrupted_objects += 1;
                        let (error_type, message) = if actual_hash != hash {
                            ("corrupted".to_string(), format!("hash mismatch: expected={}, actual={}", hash, actual_hash))
                        } else {
                            ("corrupted".to_string(), format!("size mismatch: expected={}, actual={}", expected_size, actual_size))
                        };
                        summary.errors.push((hash.clone(), error_type, message));
                    }
                }
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                    summary.missing_objects += 1;
                    summary.errors.push((hash.clone(), "missing".to_string(), "file not found".to_string()));
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
        let path = self.hash_to_path(hash);
        std::fs::metadata(&path)
            .ok()
            .and_then(|m| m.created().ok())
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
        let mut size = 0u64;
        
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
                    if !file_name.ends_with(".tmp") {
                        let file_size = metadata.len();
                        self.index.insert(file_name.to_string(), file_size);
                        count += 1;
                        size += file_size;
                    }
                }
            }
        }
        
        self.stats.total_chunks.store(count, Ordering::Relaxed);
        self.stats.total_size.store(size, Ordering::Relaxed);
        
        info!(chunks = count, size, "index rebuild complete");
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
        
        // Store
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
        
        let (chunks, size, hits, _) = store.stats();
        assert_eq!(chunks, 1);
        assert_eq!(size, data.len() as u64);
        assert_eq!(hits, 1); // Second put hits dedup
        
        // Delete
        assert!(store.delete(&hash).await.unwrap());
        assert!(!store.has(&hash));
    }
}
