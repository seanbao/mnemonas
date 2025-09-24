//! CDC (Content-Defined Chunking)
//! Efficient data deduplication using FastCDC algorithm

use std::time::{SystemTime, SystemTimeError, UNIX_EPOCH};

use fastcdc::v2020::FastCDC;
use tracing::{debug, warn};

pub(crate) fn unix_timestamp_secs(now: SystemTime) -> Result<u64, SystemTimeError> {
    now.duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_secs())
}

pub(crate) fn unix_timestamp_secs_or_zero(now: SystemTime) -> u64 {
    unix_timestamp_secs(now).unwrap_or_else(|err| {
        warn!(error = %err, "system clock is before unix epoch, falling back to timestamp 0");
        0
    })
}

pub(crate) fn current_unix_timestamp_secs_or_zero() -> u64 {
    unix_timestamp_secs_or_zero(SystemTime::now())
}

/// Chunking configuration
#[derive(Debug, Clone)]
pub struct ChunkerConfig {
    /// Minimum chunk size (default 256KB)
    pub min_size: u32,
    /// Average chunk size (default 1MB)  
    pub avg_size: u32,
    /// Maximum chunk size (default 4MB)
    pub max_size: u32,
}

impl Default for ChunkerConfig {
    fn default() -> Self {
        Self {
            min_size: 256 * 1024,      // 256KB
            avg_size: 1024 * 1024,     // 1MB
            max_size: 4 * 1024 * 1024, // 4MB
        }
    }
}

impl ChunkerConfig {
    /// Buffer threshold for streaming CDC (2x max_size)
    pub fn buffer_threshold(&self) -> usize {
        self.max_size as usize * 2
    }
}

/// Data chunk
#[derive(Debug, Clone)]
pub struct Chunk {
    /// Offset in original data
    pub offset: u64,
    /// Chunk length
    pub length: u32,
    /// Chunk data
    pub data: Vec<u8>,
    /// Chunk hash (BLAKE3)
    pub hash: String,
}

/// CDC chunker (batch mode)
pub struct Chunker {
    config: ChunkerConfig,
}

impl Chunker {
    /// Create chunker
    pub fn new(config: ChunkerConfig) -> Self {
        Self { config }
    }

    /// Get config reference
    pub fn config(&self) -> &ChunkerConfig {
        &self.config
    }

    /// Chunk data (batch mode - loads all data into memory)
    pub fn chunk(&self, data: &[u8]) -> Vec<Chunk> {
        if data.is_empty() {
            return vec![];
        }

        let chunker = FastCDC::new(
            data,
            self.config.min_size,
            self.config.avg_size,
            self.config.max_size,
        );

        let mut chunks = Vec::new();

        for entry in chunker {
            let chunk_data = &data[entry.offset..entry.offset + entry.length];
            let hash = crate::cas::compute_hash(chunk_data);

            debug!(
                offset = entry.offset,
                length = entry.length,
                hash = %hash,
                "chunk generated"
            );

            chunks.push(Chunk {
                offset: entry.offset as u64,
                length: entry.length as u32,
                data: chunk_data.to_vec(),
                hash,
            });
        }

        debug!(
            total_size = data.len(),
            chunk_count = chunks.len(),
            avg_chunk_size = data.len() / chunks.len().max(1),
            "chunking complete"
        );

        chunks
    }

    /// Estimate chunk count (without actual chunking)
    pub fn estimate_chunks(&self, size: u64) -> u64 {
        (size / self.config.avg_size as u64).max(1)
    }
}

/// Streaming CDC chunker - processes data incrementally with bounded memory
pub struct StreamingChunker {
    config: ChunkerConfig,
    /// Internal buffer for accumulating data
    buffer: Vec<u8>,
    /// Total bytes processed (for offset calculation)
    total_offset: u64,
    /// Buffer threshold - process when buffer exceeds this
    threshold: usize,
}

impl StreamingChunker {
    /// Create a new streaming chunker
    pub fn new(config: ChunkerConfig) -> Self {
        let threshold = config.buffer_threshold();
        Self {
            config,
            buffer: Vec::with_capacity(threshold),
            total_offset: 0,
            threshold,
        }
    }

    /// Feed data and get completed chunks
    /// Returns chunks that are fully determined (not including trailing partial data)
    pub fn feed(&mut self, data: &[u8]) -> Vec<Chunk> {
        self.buffer.extend_from_slice(data);

        // Only process when buffer exceeds threshold
        if self.buffer.len() < self.threshold {
            return vec![];
        }

        self.process_buffer(false)
    }

    /// Finish processing and return all remaining chunks
    pub fn finish(mut self) -> Vec<Chunk> {
        self.process_buffer(true)
    }

    /// Current buffer size (for monitoring)
    pub fn buffer_size(&self) -> usize {
        self.buffer.len()
    }

    /// Process buffer and extract completed chunks
    fn process_buffer(&mut self, is_final: bool) -> Vec<Chunk> {
        if self.buffer.is_empty() {
            return vec![];
        }

        let chunker = FastCDC::new(
            &self.buffer,
            self.config.min_size,
            self.config.avg_size,
            self.config.max_size,
        );

        let mut chunks = Vec::new();
        let mut last_end = 0usize;

        for entry in chunker {
            let chunk_end = entry.offset + entry.length;

            // If not final, keep the last chunk in buffer (might be incomplete)
            if !is_final && chunk_end >= self.buffer.len() {
                break;
            }

            let chunk_data = &self.buffer[entry.offset..chunk_end];
            let hash = crate::cas::compute_hash(chunk_data);

            chunks.push(Chunk {
                offset: self.total_offset + entry.offset as u64,
                length: entry.length as u32,
                data: chunk_data.to_vec(),
                hash,
            });

            last_end = chunk_end;
        }

        // Update state: remove processed data, keep remainder
        if last_end > 0 {
            self.total_offset += last_end as u64;
            self.buffer.drain(..last_end);
        }

        debug!(
            chunks_produced = chunks.len(),
            remaining_buffer = self.buffer.len(),
            total_offset = self.total_offset,
            "streaming chunk batch"
        );

        chunks
    }
}

/// File manifest recording the chunks that make up a file.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct FileManifest {
    /// Original file size.
    pub size: u64,
    /// Ordered chunk hash references that make up the file.
    pub chunks: Vec<ChunkRef>,
    /// Creation time as a Unix timestamp in seconds.
    pub created_at: u64,
}

/// Chunk reference.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct ChunkRef {
    /// Chunk hash.
    pub hash: String,
    /// Chunk size.
    pub size: u32,
    /// Offset in the original file.
    pub offset: u64,
}

impl FileManifest {
    /// Create a manifest from a chunk list.
    pub fn from_chunks(chunks: &[Chunk]) -> Self {
        let size = chunks.iter().map(|c| c.length as u64).sum();
        let chunk_refs = chunks
            .iter()
            .map(|c| ChunkRef {
                hash: c.hash.clone(),
                size: c.length,
                offset: c.offset,
            })
            .collect();

        Self {
            size,
            chunks: chunk_refs,
            created_at: current_unix_timestamp_secs_or_zero(),
        }
    }

    /// Serialize to JSON
    pub fn to_json(&self) -> serde_json::Result<Vec<u8>> {
        serde_json::to_vec(self)
    }

    /// Deserialize from JSON
    pub fn from_json(data: &[u8]) -> serde_json::Result<Self> {
        serde_json::from_slice(data)
    }

    /// Validate manifest structure before reconstructing a file.
    pub fn validate(&self) -> Result<(), String> {
        let mut expected_offset = 0u64;

        for (index, chunk) in self.chunks.iter().enumerate() {
            crate::cas::validate_hash(&chunk.hash)
                .map_err(|_| format!("chunk {index} has invalid hash"))?;
            if chunk.size == 0 {
                return Err(format!("chunk {index} has zero size"));
            }
            if chunk.offset != expected_offset {
                return Err(format!(
                    "chunk {index} offset is {}, expected {expected_offset}",
                    chunk.offset
                ));
            }
            expected_offset = expected_offset
                .checked_add(chunk.size as u64)
                .ok_or_else(|| format!("chunk {index} size overflows manifest"))?;
        }

        if expected_offset != self.size {
            return Err(format!(
                "chunk sizes sum to {expected_offset}, expected {}",
                self.size
            ));
        }

        Ok(())
    }

    /// Calculate dedup ratio
    pub fn dedup_ratio(&self, unique_size: u64) -> f64 {
        if self.size == 0 {
            return 1.0;
        }
        1.0 - (unique_size as f64 / self.size as f64)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;

    #[test]
    fn test_chunking() {
        // Create test data (repeating pattern for dedup testing)
        let mut data = Vec::new();
        let pattern = b"Hello, this is a test pattern for CDC chunking! ";
        for _ in 0..10000 {
            data.extend_from_slice(pattern);
        }

        // Use small chunk sizes for testing
        let config = ChunkerConfig {
            min_size: 1024,  // 1KB
            avg_size: 4096,  // 4KB
            max_size: 16384, // 16KB
        };

        let chunker = Chunker::new(config);
        let chunks = chunker.chunk(&data);

        assert!(!chunks.is_empty());

        // Verify chunks can reconstruct original data
        let mut reconstructed = Vec::new();
        for chunk in &chunks {
            reconstructed.extend_from_slice(&chunk.data);
        }
        assert_eq!(data, reconstructed);

        // Verify hash uniqueness
        let mut unique_hashes = std::collections::HashSet::new();
        for chunk in &chunks {
            unique_hashes.insert(chunk.hash.clone());
        }

        println!(
            "Total chunks: {}, Unique chunks: {}, Dedup: {:.1}%",
            chunks.len(),
            unique_hashes.len(),
            (1.0 - unique_hashes.len() as f64 / chunks.len() as f64) * 100.0
        );
    }

    #[test]
    fn test_manifest() {
        let chunks = vec![
            Chunk {
                offset: 0,
                length: 1000,
                data: vec![0; 1000],
                hash: crate::cas::compute_hash(&vec![0; 1000]),
            },
            Chunk {
                offset: 1000,
                length: 2000,
                data: vec![0; 2000],
                hash: crate::cas::compute_hash(&vec![0; 2000]),
            },
        ];

        let manifest = FileManifest::from_chunks(&chunks);
        manifest.validate().expect("valid manifest");

        assert_eq!(manifest.size, 3000);
        assert_eq!(manifest.chunks.len(), 2);

        // Test serialization roundtrip
        let json = manifest.to_json().unwrap();
        let restored = FileManifest::from_json(&json).unwrap();

        assert_eq!(restored.size, manifest.size);
        assert_eq!(restored.chunks.len(), manifest.chunks.len());
    }

    #[test]
    fn test_manifest_validate_rejects_invalid_shape() {
        let manifest = FileManifest {
            size: 10,
            chunks: vec![ChunkRef {
                hash: "0".repeat(64),
                size: 10,
                offset: 1,
            }],
            created_at: 0,
        };

        let err = manifest.validate().expect_err("invalid manifest shape");

        assert!(err.contains("offset"));
    }

    #[test]
    fn test_manifest_validate_rejects_invalid_hash() {
        let manifest = FileManifest {
            size: 10,
            chunks: vec![ChunkRef {
                hash: "../bad".to_string(),
                size: 10,
                offset: 0,
            }],
            created_at: 0,
        };

        let err = manifest.validate().expect_err("invalid manifest hash");

        assert!(err.contains("invalid hash"));
    }

    #[test]
    fn test_unix_timestamp_secs() {
        let timestamp = unix_timestamp_secs(UNIX_EPOCH + Duration::from_secs(42)).unwrap();

        assert_eq!(timestamp, 42);
    }

    #[test]
    fn test_unix_timestamp_secs_before_epoch() {
        let result = unix_timestamp_secs(UNIX_EPOCH - Duration::from_secs(1));

        assert!(result.is_err());
    }

    #[test]
    fn test_unix_timestamp_secs_or_zero_before_epoch() {
        let timestamp = unix_timestamp_secs_or_zero(UNIX_EPOCH - Duration::from_secs(1));

        assert_eq!(timestamp, 0);
    }

    #[test]
    fn test_streaming_chunker() {
        // Create test data
        let mut data = Vec::new();
        let pattern = b"Hello, this is a test pattern for streaming CDC! ";
        for _ in 0..10000 {
            data.extend_from_slice(pattern);
        }

        // Use small chunk sizes for testing
        let config = ChunkerConfig {
            min_size: 1024,  // 1KB
            avg_size: 4096,  // 4KB
            max_size: 16384, // 16KB
        };

        // Batch chunking for comparison
        let batch_chunker = Chunker::new(config.clone());
        let batch_chunks = batch_chunker.chunk(&data);

        // Streaming chunking - simulate receiving data in small pieces
        let mut streaming_chunker = StreamingChunker::new(config);
        let mut streaming_chunks = Vec::new();

        // Feed data in 1KB pieces
        for chunk in data.chunks(1024) {
            streaming_chunks.extend(streaming_chunker.feed(chunk));
        }
        streaming_chunks.extend(streaming_chunker.finish());

        // Verify both methods produce same total size
        let batch_total: u64 = batch_chunks.iter().map(|c| c.length as u64).sum();
        let streaming_total: u64 = streaming_chunks.iter().map(|c| c.length as u64).sum();
        assert_eq!(batch_total, streaming_total);
        assert_eq!(batch_total, data.len() as u64);

        // Verify streaming chunks can reconstruct original data
        let mut reconstructed = Vec::new();
        for chunk in &streaming_chunks {
            reconstructed.extend_from_slice(&chunk.data);
        }
        assert_eq!(data, reconstructed);

        println!(
            "Batch chunks: {}, Streaming chunks: {}",
            batch_chunks.len(),
            streaming_chunks.len()
        );
    }

    #[test]
    fn test_streaming_chunker_small_data() {
        // Test with data smaller than threshold
        let data = b"Small data that fits in buffer";

        let config = ChunkerConfig {
            min_size: 64,
            avg_size: 256,
            max_size: 1024,
        };

        let mut streaming_chunker = StreamingChunker::new(config);

        // Feed small data - should not produce chunks yet
        let chunks = streaming_chunker.feed(data);
        assert!(chunks.is_empty());
        assert_eq!(streaming_chunker.buffer_size(), data.len());

        // Finish should produce the chunk
        let final_chunks = streaming_chunker.finish();
        assert!(!final_chunks.is_empty());

        // Verify data
        let mut reconstructed = Vec::new();
        for chunk in &final_chunks {
            reconstructed.extend_from_slice(&chunk.data);
        }
        assert_eq!(&reconstructed[..], data);
    }
}
