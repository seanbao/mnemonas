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

pub use cas::{CasStore, CasConfig, CasError};
pub use cdc::{Chunker, ChunkerConfig, Chunk, FileManifest};
pub use service::DataPlaneService;
