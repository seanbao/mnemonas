//! MnemoNAS DataPlane - High-performance Rust data plane
//!
//! Responsibilities:
//! - CAS (Content-Addressable Storage)
//! - CDC (Content-Defined Chunking)
//! - Data deduplication
//! - Data integrity verification

pub mod cas;
pub mod cdc;
pub mod service;

pub use cas::{CasConfig, CasError, CasStore};
pub use cdc::{Chunk, ChunkRef, Chunker, ChunkerConfig, FileManifest, StreamingChunker};
pub use service::DataPlaneService;
