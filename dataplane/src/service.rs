//! gRPC service implementation

use std::pin::Pin;
use std::sync::Arc;
use std::time::Instant;

use tokio::sync::mpsc;
use tokio_stream::{wrappers::ReceiverStream, Stream, StreamExt};
use tonic::{Request, Response, Status, Streaming};
use tracing::{info, instrument};

use crate::cas::{CasConfig, CasStore};
use crate::cdc::{Chunk, ChunkRef, Chunker, ChunkerConfig, FileManifest, StreamingChunker};

// Include generated protobuf code
pub mod proto {
    include!("proto/mnemonas.dataplane.v1.rs");
}

use proto::data_plane_server::{DataPlane, DataPlaneServer};
use proto::*;

const DEFAULT_LIST_OBJECTS_LIMIT: u32 = 1000;
const MAX_LIST_OBJECTS_LIMIT: u32 = 1000;
const MAX_GRPC_MESSAGE_SIZE: usize = 128 * 1024 * 1024;

fn invalid_hash_status(err: crate::cas::CasError) -> Status {
    Status::invalid_argument(err.to_string())
}

fn cas_read_status(err: crate::cas::CasError, not_found_message: &'static str) -> Status {
    match err {
        crate::cas::CasError::NotFound(_) => Status::not_found(not_found_message),
        crate::cas::CasError::InvalidHash => Status::invalid_argument("invalid object hash"),
        _ => Status::internal(err.to_string()),
    }
}

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
            .max_decoding_message_size(MAX_GRPC_MESSAGE_SIZE)
            .max_encoding_message_size(MAX_GRPC_MESSAGE_SIZE)
    }

    async fn store_uploaded_chunk(
        &self,
        chunk: Chunk,
        chunk_refs: &mut Vec<ChunkRef>,
        chunk_hashes: &mut Vec<String>,
        created_hashes: &mut Vec<String>,
        logical_size_added: &mut u64,
        unique_size: &mut u64,
    ) -> Result<(), Status> {
        let put_result = self
            .cas
            .put_with_status(&chunk.data)
            .await
            .map_err(|e| Status::internal(e.to_string()))?;

        *logical_size_added += chunk.length as u64;

        if !put_result.deduplicated {
            created_hashes.push(put_result.hash.clone());
            *unique_size += chunk.length as u64;
        }

        chunk_refs.push(ChunkRef {
            hash: put_result.hash.clone(),
            size: chunk.length,
            offset: chunk.offset,
        });
        chunk_hashes.push(put_result.hash);

        Ok(())
    }

    async fn rollback_put_file_failure(
        &self,
        created_hashes: &[String],
        logical_size_added: u64,
        status: Status,
    ) -> Status {
        self.cas.rollback_logical_size(logical_size_added);

        if created_hashes.is_empty() {
            return status;
        }

        let mut cleanup_errors = Vec::new();
        for hash in created_hashes.iter().rev() {
            if let Err(err) = self.cas.delete(hash).await {
                cleanup_errors.push(format!("delete {}: {}", hash, err));
            }
        }

        if cleanup_errors.is_empty() {
            return status;
        }

        Status::new(
            status.code(),
            format!(
                "{}; rollback failed: {}",
                status.message(),
                cleanup_errors.join("; ")
            ),
        )
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
            crate::cas::validate_hash(expected).map_err(invalid_hash_status)?;
            let actual = crate::cas::compute_hash(data);
            if &actual != expected {
                return Err(Status::invalid_argument(format!(
                    "Hash mismatch: expected={}, actual={}",
                    expected, actual
                )));
            }
        }

        let put_result = self
            .cas
            .put_with_status(data)
            .await
            .map_err(|e| Status::internal(e.to_string()))?;

        Ok(Response::new(PutChunkResponse {
            hash: put_result.hash,
            size: data.len() as u64,
            deduplicated: put_result.deduplicated,
        }))
    }

    /// Get data chunk
    #[instrument(skip(self))]
    async fn get_chunk(
        &self,
        request: Request<GetChunkRequest>,
    ) -> Result<Response<GetChunkResponse>, Status> {
        let hash = &request.into_inner().hash;

        let data = self
            .cas
            .get(hash)
            .await
            .map_err(|e| cas_read_status(e, "Object not found"))?;

        Ok(Response::new(GetChunkResponse { data }))
    }

    /// Check if data chunk exists
    async fn has_chunk(
        &self,
        request: Request<HasChunkRequest>,
    ) -> Result<Response<HasChunkResponse>, Status> {
        let hash = &request.into_inner().hash;
        crate::cas::validate_hash(hash).map_err(invalid_hash_status)?;
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

        let deleted = self.cas.delete(hash).await.map_err(|e| match e {
            crate::cas::CasError::InvalidHash => Status::invalid_argument("invalid object hash"),
            _ => Status::internal(e.to_string()),
        })?;

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
        let mut created_hashes: Vec<String> = Vec::new();
        let mut logical_size_added: u64 = 0;
        let mut unique_size: u64 = 0;

        // Process stream incrementally
        while let Some(req) = stream.next().await {
            let req = match req {
                Ok(req) => req,
                Err(status) => {
                    return Err(self
                        .rollback_put_file_failure(&created_hashes, logical_size_added, status)
                        .await);
                }
            };
            match req.payload {
                Some(put_file_request::Payload::Metadata(m)) => {
                    if metadata.is_some() {
                        return Err(self
                            .rollback_put_file_failure(
                                &created_hashes,
                                logical_size_added,
                                Status::invalid_argument(
                                    "File metadata must be provided exactly once",
                                ),
                            )
                            .await);
                    }
                    if total_size > 0 {
                        return Err(self
                            .rollback_put_file_failure(
                                &created_hashes,
                                logical_size_added,
                                Status::invalid_argument(
                                    "File metadata must be sent before file chunks",
                                ),
                            )
                            .await);
                    }
                    metadata = Some(m);
                }
                Some(put_file_request::Payload::Chunk(data)) => {
                    if metadata.is_none() {
                        return Err(self
                            .rollback_put_file_failure(
                                &created_hashes,
                                logical_size_added,
                                Status::invalid_argument(
                                    "File metadata must be sent before file chunks",
                                ),
                            )
                            .await);
                    }

                    // Check total size limit
                    total_size += data.len() as u64;
                    if total_size > MAX_FILE_SIZE {
                        return Err(self
                            .rollback_put_file_failure(
                                &created_hashes,
                                logical_size_added,
                                Status::resource_exhausted(format!(
                                    "File too large (max: {} bytes)",
                                    MAX_FILE_SIZE
                                )),
                            )
                            .await);
                    }

                    // Feed data to streaming chunker
                    let chunks = streaming_chunker.feed(&data);

                    // Store completed chunks immediately
                    for chunk in chunks {
                        if let Err(status) = self
                            .store_uploaded_chunk(
                                chunk,
                                &mut chunk_refs,
                                &mut chunk_hashes,
                                &mut created_hashes,
                                &mut logical_size_added,
                                &mut unique_size,
                            )
                            .await
                        {
                            return Err(self
                                .rollback_put_file_failure(
                                    &created_hashes,
                                    logical_size_added,
                                    status,
                                )
                                .await);
                        }
                    }
                }
                None => {}
            }
        }

        // Log metadata if provided
        if let Some(ref m) = metadata {
            info!(path = %m.path, total_size, "processing file upload");
        } else {
            return Err(self
                .rollback_put_file_failure(
                    &created_hashes,
                    logical_size_added,
                    Status::invalid_argument("File metadata is required"),
                )
                .await);
        }

        if total_size == 0 {
            return Err(self
                .rollback_put_file_failure(
                    &created_hashes,
                    logical_size_added,
                    Status::invalid_argument("No data provided"),
                )
                .await);
        }

        // Process remaining data in buffer
        let final_chunks = streaming_chunker.finish();
        for chunk in final_chunks {
            if let Err(status) = self
                .store_uploaded_chunk(
                    chunk,
                    &mut chunk_refs,
                    &mut chunk_hashes,
                    &mut created_hashes,
                    &mut logical_size_added,
                    &mut unique_size,
                )
                .await
            {
                return Err(self
                    .rollback_put_file_failure(&created_hashes, logical_size_added, status)
                    .await);
            }
        }

        // Create and store manifest
        let manifest = FileManifest {
            size: total_size,
            chunks: chunk_refs,
            created_at: crate::cdc::current_unix_timestamp_secs_or_zero(),
        };

        let manifest_json = manifest
            .to_json()
            .map_err(|e| Status::internal(e.to_string()));

        let manifest_json = match manifest_json {
            Ok(manifest_json) => manifest_json,
            Err(status) => {
                return Err(self
                    .rollback_put_file_failure(&created_hashes, logical_size_added, status)
                    .await);
            }
        };

        let manifest_result = self
            .cas
            .put_with_status(&manifest_json)
            .await
            .map_err(|e| Status::internal(e.to_string()));

        let manifest_hash = match manifest_result {
            Ok(result) => result.hash,
            Err(status) => {
                return Err(self
                    .rollback_put_file_failure(&created_hashes, logical_size_added, status)
                    .await);
            }
        };

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
        crate::cas::validate_hash(&manifest_hash).map_err(invalid_hash_status)?;

        // Get manifest
        let manifest_data = self
            .cas
            .get(&manifest_hash)
            .await
            .map_err(|e| cas_read_status(e, "File not found"))?;

        let manifest = FileManifest::from_json(&manifest_data)
            .map_err(|e| Status::data_loss(format!("Failed to parse manifest: {}", e)))?;
        manifest
            .validate()
            .map_err(|e| Status::data_loss(format!("Invalid file manifest: {}", e)))?;

        // Create streaming response
        let (tx, rx) = mpsc::channel(4);
        let cas = Arc::clone(&self.cas);

        tokio::spawn(async move {
            for chunk_ref in manifest.chunks {
                match cas.get(&chunk_ref.hash).await {
                    Ok(data) => {
                        if data.len() as u64 != chunk_ref.size as u64 {
                            let _ = tx
                                .send(Err(Status::data_loss(
                                    "File manifest chunk size does not match CAS object",
                                )))
                                .await;
                            break;
                        }
                        if tx.send(Ok(GetFileResponse { data })).await.is_err() {
                            break;
                        }
                    }
                    Err(e) => {
                        let status = match e {
                            crate::cas::CasError::NotFound(_) => {
                                Status::data_loss("File chunk missing from CAS")
                            }
                            crate::cas::CasError::InvalidHash => {
                                Status::data_loss("File manifest contains invalid chunk hash")
                            }
                            _ => Status::internal(e.to_string()),
                        };
                        let _ = tx.send(Err(status)).await;
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
        let (total_chunks, logical_size, unique_size, _compressed_size, _hits, _misses) =
            self.cas.stats();

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
            for hash in &req.hashes {
                crate::cas::validate_hash(hash).map_err(invalid_hash_status)?;
            }
            Some(req.hashes)
        };

        let summary = self
            .cas
            .scrub(hashes.as_deref())
            .await
            .map_err(|e| match e {
                crate::cas::CasError::InvalidHash => {
                    Status::invalid_argument("invalid object hash")
                }
                _ => Status::internal(e.to_string()),
            })?;

        // Convert errors to proto format
        let errors: Vec<ScrubError> = summary
            .errors
            .into_iter()
            .map(|(hash, error_type, message)| ScrubError {
                hash,
                error_type,
                message,
            })
            .collect();

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
        let requested_limit = req.limit.unwrap_or(DEFAULT_LIST_OBJECTS_LIMIT);
        if requested_limit == 0 || requested_limit > MAX_LIST_OBJECTS_LIMIT {
            return Err(Status::invalid_argument(format!(
                "limit must be between 1 and {}",
                MAX_LIST_OBJECTS_LIMIT
            )));
        }
        let limit = requested_limit as usize;
        let cursor = req.cursor.as_deref();
        if let Some(cursor) = cursor {
            crate::cas::validate_hash(cursor).map_err(invalid_hash_status)?;
        }

        let (objects, next_cursor) = self.cas.list_objects(cursor, limit);

        let objects: Vec<ObjectInfo> = objects
            .into_iter()
            .map(|(hash, size, created_at)| ObjectInfo {
                hash,
                size,
                created_at_unix: created_at,
            })
            .collect();

        Ok(Response::new(ListObjectsResponse {
            objects,
            next_cursor,
        }))
    }
}

#[cfg(test)]
mod tests {
    use super::proto::data_plane_client::DataPlaneClient;
    use super::proto::put_file_request::Payload;
    use super::proto::{FileMetadata, PutFileRequest};
    use super::*;
    use tempfile::tempdir;
    use tokio::net::TcpListener;
    use tokio::sync::oneshot;
    use tokio_stream::iter;
    use tokio_stream::wrappers::TcpListenerStream;
    use tonic::transport::{Channel, Server};
    use tonic::Code;

    #[test]
    fn test_grpc_message_limit_has_version_object_headroom() {
        let max_grpc_message_size = MAX_GRPC_MESSAGE_SIZE;
        let default_max_version_object_size = 100 * 1024 * 1024;
        assert!(max_grpc_message_size > default_max_version_object_size);
    }

    async fn setup_test_client(
        service: DataPlaneService,
    ) -> (DataPlaneClient<Channel>, oneshot::Sender<()>) {
        let listener = TcpListener::bind("127.0.0.1:0").await.expect("bind");
        let addr = listener.local_addr().expect("local addr");
        let (shutdown_tx, shutdown_rx) = oneshot::channel();

        tokio::spawn(async move {
            let incoming = TcpListenerStream::new(listener);
            Server::builder()
                .add_service(service.into_server())
                .serve_with_incoming_shutdown(incoming, async {
                    let _ = shutdown_rx.await;
                })
                .await
                .expect("server run");
        });

        let endpoint = format!("http://{}", addr);
        let client = DataPlaneClient::connect(endpoint)
            .await
            .expect("connect client");

        (client, shutdown_tx)
    }

    #[tokio::test]
    async fn test_invalid_hash_requests_return_invalid_argument() {
        let temp = tempdir().expect("tempdir");
        let cas = Arc::new(
            CasStore::new(CasConfig {
                root: temp.path().join("cas"),
                compression_enabled: false,
                ..Default::default()
            })
            .await
            .expect("cas init"),
        );
        let service = DataPlaneService::with_cas(cas, ChunkerConfig::default());
        let (mut client, shutdown_tx) = setup_test_client(service).await;

        let invalid_hash = "/etc/passwd".to_string();

        let err = client
            .get_chunk(GetChunkRequest {
                hash: invalid_hash.clone(),
            })
            .await
            .expect_err("get_chunk should reject invalid hash");
        assert_eq!(err.code(), Code::InvalidArgument);

        let err = client
            .has_chunk(HasChunkRequest {
                hash: invalid_hash.clone(),
            })
            .await
            .expect_err("has_chunk should reject invalid hash");
        assert_eq!(err.code(), Code::InvalidArgument);

        let err = client
            .delete_chunk(DeleteChunkRequest {
                hash: invalid_hash.clone(),
            })
            .await
            .expect_err("delete_chunk should reject invalid hash");
        assert_eq!(err.code(), Code::InvalidArgument);

        let err = client
            .get_file(GetFileRequest {
                manifest_hash: invalid_hash.clone(),
            })
            .await
            .expect_err("get_file should reject invalid manifest hash");
        assert_eq!(err.code(), Code::InvalidArgument);

        let err = client
            .scrub(ScrubRequest {
                hashes: vec![invalid_hash.clone()],
            })
            .await
            .expect_err("scrub should reject invalid hash filters");
        assert_eq!(err.code(), Code::InvalidArgument);

        let err = client
            .list_objects(ListObjectsRequest {
                cursor: Some(invalid_hash),
                limit: Some(10),
            })
            .await
            .expect_err("list_objects should reject invalid cursors");
        assert_eq!(err.code(), Code::InvalidArgument);

        let _ = shutdown_tx.send(());
    }

    #[tokio::test]
    async fn test_list_objects_rejects_invalid_limits() {
        let temp = tempdir().expect("tempdir");
        let cas = Arc::new(
            CasStore::new(CasConfig {
                root: temp.path().join("cas"),
                compression_enabled: false,
                ..Default::default()
            })
            .await
            .expect("cas init"),
        );
        let service = DataPlaneService::with_cas(cas, ChunkerConfig::default());
        let (mut client, shutdown_tx) = setup_test_client(service).await;

        for limit in [0, MAX_LIST_OBJECTS_LIMIT + 1] {
            let err = client
                .list_objects(ListObjectsRequest {
                    cursor: None,
                    limit: Some(limit),
                })
                .await
                .expect_err("list_objects should reject invalid limits");
            assert_eq!(err.code(), Code::InvalidArgument);
        }

        client
            .list_objects(ListObjectsRequest {
                cursor: None,
                limit: None,
            })
            .await
            .expect("list_objects should accept the default limit");

        let _ = shutdown_tx.send(());
    }

    #[tokio::test]
    async fn test_put_file_rolls_back_created_chunks_on_store_failure() {
        let temp = tempdir().expect("tempdir");
        let cas = Arc::new(
            CasStore::new(CasConfig {
                root: temp.path().join("cas"),
                compression_enabled: false,
                ..Default::default()
            })
            .await
            .expect("cas init"),
        );
        cas.set_fail_put_after(2);

        let service = DataPlaneService::with_cas(
            Arc::clone(&cas),
            ChunkerConfig {
                min_size: 1024,
                avg_size: 2048,
                max_size: 4096,
            },
        );
        let (mut client, shutdown_tx) = setup_test_client(service).await;

        let data: Vec<u8> = (0..(16 * 1024)).map(|i| (i % 251) as u8).collect();
        let requests = vec![
            PutFileRequest {
                payload: Some(Payload::Metadata(FileMetadata {
                    path: "/docs/fail.bin".to_string(),
                    content_type: None,
                })),
            },
            PutFileRequest {
                payload: Some(Payload::Chunk(data)),
            },
        ];

        let err = client
            .put_file(iter(requests))
            .await
            .expect_err("put_file should fail after the injected CAS write error");

        assert_eq!(err.code(), Code::Internal);
        assert!(
            cas.list_hashes().is_empty(),
            "expected failed put_file upload to leave no orphaned chunks"
        );
        let (chunks, logical_size, unique_size, _, _, _) = cas.stats();
        assert_eq!(
            chunks, 0,
            "expected failed upload not to leave chunk stats behind"
        );
        assert_eq!(
            logical_size, 0,
            "expected failed upload not to inflate logical size"
        );
        assert_eq!(
            unique_size, 0,
            "expected failed upload not to inflate unique size"
        );

        let _ = shutdown_tx.send(());
    }
}
