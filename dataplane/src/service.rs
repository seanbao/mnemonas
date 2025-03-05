//! gRPC service implementation

use std::pin::Pin;
use std::sync::Arc;
use std::time::Instant;

use tokio::sync::mpsc;
use tokio_stream::{wrappers::ReceiverStream, Stream, StreamExt};
use tonic::{Request, Response, Status, Streaming};
use tracing::{info, instrument};

use crate::cas::{CasStore, CasConfig};
use crate::cdc::{Chunker, ChunkerConfig, FileManifest, ChunkRef, StreamingChunker};

// Include generated protobuf code
pub mod proto {
    include!("proto/mnemonas.dataplane.v1.rs");
}

use proto::data_plane_server::{DataPlane, DataPlaneServer};
use proto::*;

/// DataPlane service implementation
pub struct DataPlaneService {
    cas: Arc<CasStore>,
    chunker: Arc<Chunker>,
    start_time: Instant,
}

impl DataPlaneService {
    /// Create a new DataPlane service
    pub async fn new(cas_config: CasConfig, chunker_config: ChunkerConfig) -> anyhow::Result<Self> {
        let cas = CasStore::new(cas_config).await?;
        let chunker = Chunker::new(chunker_config);
        
        Ok(Self {
            cas: Arc::new(cas),
            chunker: Arc::new(chunker),
            start_time: Instant::now(),
        })
    }
    
    /// Create service with existing CAS store (shared instance)
    pub fn with_cas(cas: Arc<CasStore>, chunker_config: ChunkerConfig) -> Self {
        let chunker = Chunker::new(chunker_config);
        Self {
            cas,
            chunker: Arc::new(chunker),
            start_time: Instant::now(),
        }
    }
    
    /// Get gRPC server
    pub fn into_server(self) -> DataPlaneServer<Self> {
        DataPlaneServer::new(self)
    }
}

#[tonic::async_trait]
impl DataPlane for DataPlaneService {
    /// Store data chunk
    #[instrument(skip(self, request))]
    async fn put_chunk(
        &self,
        request: Request<PutChunkRequest>,
    ) -> Result<Response<PutChunkResponse>, Status> {
        let req = request.into_inner();
        let data = &req.data;
        
        // If expected hash provided, verify first
        if let Some(expected) = &req.expected_hash {
            let actual = crate::cas::compute_hash(data);
            if &actual != expected {
                return Err(Status::invalid_argument(format!(
                    "Hash mismatch: expected={}, actual={}",
                    expected, actual
                )));
            }
        }
        
        // Check if deduplication will happen
        let hash = crate::cas::compute_hash(data);
        let deduplicated = self.cas.has(&hash);
        
        // Store
        let hash = self.cas.put(data).await
            .map_err(|e| Status::internal(e.to_string()))?;
        
        Ok(Response::new(PutChunkResponse {
            hash,
            size: data.len() as u64,
            deduplicated,
        }))
    }
    
    /// Get data chunk
    #[instrument(skip(self))]
    async fn get_chunk(
        &self,
        request: Request<GetChunkRequest>,
    ) -> Result<Response<GetChunkResponse>, Status> {
        let hash = &request.into_inner().hash;
        
        let data = self.cas.get(hash).await
            .map_err(|e| match e {
                crate::cas::CasError::NotFound(_) => Status::not_found("Object not found"),
                _ => Status::internal(e.to_string()),
            })?;
        
        Ok(Response::new(GetChunkResponse { data }))
    }
    
    /// Check if data chunk exists
    async fn has_chunk(
        &self,
        request: Request<HasChunkRequest>,
    ) -> Result<Response<HasChunkResponse>, Status> {
        let hash = &request.into_inner().hash;
        let exists = self.cas.has(hash);
        let size = self.cas.size(hash);
        
        Ok(Response::new(HasChunkResponse { exists, size }))
    }
    
    /// Delete data chunk
    #[instrument(skip(self))]
    async fn delete_chunk(
        &self,
        request: Request<DeleteChunkRequest>,
    ) -> Result<Response<DeleteChunkResponse>, Status> {
        let hash = &request.into_inner().hash;
        
        let deleted = self.cas.delete(hash).await
            .map_err(|e| Status::internal(e.to_string()))?;
        
        Ok(Response::new(DeleteChunkResponse { deleted }))
    }
    
    /// Store file (streaming CDC - bounded memory usage)
    #[instrument(skip(self, request))]
    async fn put_file(
        &self,
        request: Request<Streaming<PutFileRequest>>,
    ) -> Result<Response<PutFileResponse>, Status> {
        let mut stream = request.into_inner();
        
        // Maximum file size limit (10GB) - prevents infinite streams
        const MAX_FILE_SIZE: u64 = 10 * 1024 * 1024 * 1024;
        
        // Create streaming chunker with bounded memory
        let mut streaming_chunker = StreamingChunker::new(self.chunker.config().clone());
        
        let mut metadata: Option<FileMetadata> = None;
        let mut total_size: u64 = 0;
        let mut chunk_refs: Vec<ChunkRef> = Vec::new();
        let mut chunk_hashes: Vec<String> = Vec::new();
        let mut unique_size: u64 = 0;
        
        // Process stream incrementally
        while let Some(req) = stream.next().await {
            let req = req?;
            match req.payload {
                Some(put_file_request::Payload::Metadata(m)) => {
                    metadata = Some(m);
                }
                Some(put_file_request::Payload::Chunk(data)) => {
                    // Check total size limit
                    total_size += data.len() as u64;
                    if total_size > MAX_FILE_SIZE {
                        return Err(Status::resource_exhausted(format!(
                            "File too large (max: {} bytes)",
                            MAX_FILE_SIZE
                        )));
                    }
                    
                    // Feed data to streaming chunker
                    let chunks = streaming_chunker.feed(&data);
                    
                    // Store completed chunks immediately
                    for chunk in chunks {
                        let deduplicated = self.cas.has(&chunk.hash);
                        
                        self.cas.put(&chunk.data).await
                            .map_err(|e| Status::internal(e.to_string()))?;
                        
                        chunk_refs.push(ChunkRef {
                            hash: chunk.hash.clone(),
                            size: chunk.length,
                            offset: chunk.offset,
                        });
                        chunk_hashes.push(chunk.hash);
                        
                        if !deduplicated {
                            unique_size += chunk.length as u64;
                        }
                    }
                }
                None => {}
            }
        }
        
        // Log metadata if provided
        if let Some(ref m) = metadata {
            info!(path = %m.path, total_size, "processing file upload");
        }
        
        if total_size == 0 {
            return Err(Status::invalid_argument("No data provided"));
        }
        
        // Process remaining data in buffer
        let final_chunks = streaming_chunker.finish();
        for chunk in final_chunks {
            let deduplicated = self.cas.has(&chunk.hash);
            
            self.cas.put(&chunk.data).await
                .map_err(|e| Status::internal(e.to_string()))?;
            
            chunk_refs.push(ChunkRef {
                hash: chunk.hash.clone(),
                size: chunk.length,
                offset: chunk.offset,
            });
            chunk_hashes.push(chunk.hash);
            
            if !deduplicated {
                unique_size += chunk.length as u64;
            }
        }
        
        // Create and store manifest
        let manifest = FileManifest {
            size: total_size,
            chunks: chunk_refs,
            created_at: std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_secs(),
        };
        
        let manifest_json = manifest.to_json()
            .map_err(|e| Status::internal(e.to_string()))?;
        
        let manifest_hash = self.cas.put(&manifest_json).await
            .map_err(|e| Status::internal(e.to_string()))?;
        
        let dedup_ratio = manifest.dedup_ratio(unique_size);
        
        info!(
            manifest_hash = %manifest_hash,
            total_size = manifest.size,
            unique_size,
            chunk_count = chunk_hashes.len(),
            dedup_ratio = format!("{:.1}%", dedup_ratio * 100.0),
            "file storage complete (streaming)"
        );
        
        Ok(Response::new(PutFileResponse {
            manifest_hash,
            chunk_hashes: chunk_hashes.clone(),
            total_size: manifest.size,
            chunk_count: chunk_hashes.len() as u32,
            dedup_ratio,
        }))
    }
    
    /// Stream output type
    type GetFileStream = Pin<Box<dyn Stream<Item = Result<GetFileResponse, Status>> + Send>>;
    
    /// Get file (streaming)
    #[instrument(skip(self))]
    async fn get_file(
        &self,
        request: Request<GetFileRequest>,
    ) -> Result<Response<Self::GetFileStream>, Status> {
        let manifest_hash = request.into_inner().manifest_hash;
        
        // Get manifest
        let manifest_data = self.cas.get(&manifest_hash).await
            .map_err(|e| match e {
                crate::cas::CasError::NotFound(_) => Status::not_found("File not found"),
                _ => Status::internal(e.to_string()),
            })?;
        
        let manifest = FileManifest::from_json(&manifest_data)
            .map_err(|e| Status::internal(format!("Failed to parse manifest: {}", e)))?;
        
        // Create streaming response
        let (tx, rx) = mpsc::channel(4);
        let cas = Arc::clone(&self.cas);
        
        tokio::spawn(async move {
            for chunk_ref in manifest.chunks {
                match cas.get(&chunk_ref.hash).await {
                    Ok(data) => {
                        if tx.send(Ok(GetFileResponse { data })).await.is_err() {
                            break;
                        }
                    }
                    Err(e) => {
                        let _ = tx.send(Err(Status::internal(e.to_string()))).await;
                        break;
                    }
                }
            }
        });
        
        Ok(Response::new(Box::pin(ReceiverStream::new(rx))))
    }
    
    /// Health check
    async fn health(
        &self,
        _request: Request<HealthRequest>,
    ) -> Result<Response<HealthResponse>, Status> {
        Ok(Response::new(HealthResponse {
            healthy: true,
            version: env!("CARGO_PKG_VERSION").to_string(),
            uptime_secs: self.start_time.elapsed().as_secs(),
        }))
    }
    
    /// Get statistics
    async fn stats(
        &self,
        _request: Request<StatsRequest>,
    ) -> Result<Response<StatsResponse>, Status> {
        let (total_chunks, logical_size, unique_size, _compressed_size, _hits, _misses) = self.cas.stats();

        let dedup_ratio = if logical_size > 0 {
            1.0 - (unique_size as f64 / logical_size as f64)
        } else {
            0.0
        };
        
        Ok(Response::new(StatsResponse {
            total_chunks,
            total_size: logical_size,
            unique_size,
            dedup_ratio,
        }))
    }
    
    /// Run scrub to verify data integrity
    #[instrument(skip(self))]
    async fn scrub(
        &self,
        request: Request<ScrubRequest>,
    ) -> Result<Response<ScrubResponse>, Status> {
        let req = request.into_inner();
        
        // If specific hashes provided, scrub only those
        let hashes = if req.hashes.is_empty() {
            None
        } else {
            Some(req.hashes)
        };
        
        let summary = self.cas.scrub(hashes.as_deref()).await
            .map_err(|e| Status::internal(e.to_string()))?;
        
        // Convert errors to proto format
        let errors: Vec<ScrubError> = summary.errors.into_iter().map(|(hash, error_type, message)| {
            ScrubError {
                hash,
                error_type,
                message,
            }
        }).collect();
        
        info!(
            total = summary.total_objects,
            valid = summary.valid_objects,
            corrupted = summary.corrupted_objects,
            missing = summary.missing_objects,
            duration_ms = summary.duration_ms,
            "scrub complete"
        );
        
        Ok(Response::new(ScrubResponse {
            total_objects: summary.total_objects,
            valid_objects: summary.valid_objects,
            corrupted_objects: summary.corrupted_objects,
            missing_objects: summary.missing_objects,
            total_size: summary.total_size,
            duration_ms: summary.duration_ms,
            errors,
        }))
    }
    
    /// List all objects (for GC)
    async fn list_objects(
        &self,
        request: Request<ListObjectsRequest>,
    ) -> Result<Response<ListObjectsResponse>, Status> {
        let req = request.into_inner();
        let limit = req.limit.unwrap_or(1000) as usize;
        let cursor = req.cursor.as_deref();
        
        let (objects, next_cursor) = self.cas.list_objects(cursor, limit);
        
        let objects: Vec<ObjectInfo> = objects.into_iter().map(|(hash, size, created_at)| {
            ObjectInfo { hash, size, created_at_unix: created_at }
        }).collect();
        
        Ok(Response::new(ListObjectsResponse {
            objects,
            next_cursor,
        }))
    }
}
