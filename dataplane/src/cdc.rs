//! CDC (Content-Defined Chunking)
//! Efficient data deduplication using FastCDC algorithm

use fastcdc::v2020::FastCDC;
use tracing::debug;

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

/// CDC chunker
pub struct Chunker {
    config: ChunkerConfig,
}

impl Chunker {
    /// Create chunker
    pub fn new(config: ChunkerConfig) -> Self {
        Self { config }
    }
    
    /// Chunk data
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

/// 文件清单 - 记录文件由哪些chunk组成
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct FileManifest {
    /// 原始文件大小
    pub size: u64,
    /// 组成文件的chunk hash列表（按顺序）
    pub chunks: Vec<ChunkRef>,
    /// 创建时间
    pub created_at: u64,
}

/// Chunk引用
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct ChunkRef {
    /// Chunk的hash
    pub hash: String,
    /// Chunk大小
    pub size: u32,
    /// 在原始文件中的偏移
    pub offset: u64,
}

impl FileManifest {
    /// 从chunk列表创建清单
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
            created_at: std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_secs(),
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
            min_size: 1024,      // 1KB
            avg_size: 4096,      // 4KB
            max_size: 16384,     // 16KB
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
                hash: "hash1".to_string(),
            },
            Chunk {
                offset: 1000,
                length: 2000,
                data: vec![0; 2000],
                hash: "hash2".to_string(),
            },
        ];
        
        let manifest = FileManifest::from_chunks(&chunks);
        
        assert_eq!(manifest.size, 3000);
        assert_eq!(manifest.chunks.len(), 2);
        
        // Test serialization roundtrip
        let json = manifest.to_json().unwrap();
        let restored = FileManifest::from_json(&json).unwrap();
        
        assert_eq!(restored.size, manifest.size);
        assert_eq!(restored.chunks.len(), manifest.chunks.len());
    }
}
