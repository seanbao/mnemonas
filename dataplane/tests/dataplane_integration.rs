use dataplane::cas::{compute_hash, CasConfig};
use dataplane::cdc::ChunkerConfig;
use dataplane::service::proto::data_plane_client::DataPlaneClient;
use dataplane::service::proto::put_file_request::Payload;
use dataplane::service::proto::{
    DeleteChunkRequest, FileMetadata, GetChunkRequest, GetFileRequest, HasChunkRequest,
    PutChunkRequest, PutFileRequest,
};
use dataplane::DataPlaneService;
use tempfile::tempdir;
use tokio::net::TcpListener;
use tokio::sync::oneshot;
use tokio_stream::iter;
use tokio_stream::wrappers::TcpListenerStream;
use tonic::transport::{Channel, Server};
use tonic::Code;

async fn setup_client() -> (DataPlaneClient<Channel>, oneshot::Sender<()>) {
    let temp = tempdir().expect("tempdir");
    let cas_config = CasConfig {
        root: temp.path().join("cas"),
        compression_enabled: false,
        ..Default::default()
    };
    let chunker_config = ChunkerConfig {
        min_size: 1024,
        avg_size: 4096,
        max_size: 16384,
    };

    let service = DataPlaneService::new(cas_config, chunker_config)
        .await
        .expect("service init");

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
async fn test_put_get_chunk_and_dedup() {
    let (mut client, shutdown_tx) = setup_client().await;

    let data = b"hello dataplane".to_vec();
    let expected_hash = compute_hash(&data);

    let first = client
        .put_chunk(PutChunkRequest {
            data: data.clone(),
            expected_hash: Some(expected_hash.clone()),
        })
        .await
        .expect("put chunk")
        .into_inner();

    assert_eq!(first.size, data.len() as u64);
    assert_eq!(first.hash, expected_hash);
    assert!(!first.deduplicated);

    let second = client
        .put_chunk(PutChunkRequest {
            data: data.clone(),
            expected_hash: None,
        })
        .await
        .expect("put chunk again")
        .into_inner();

    assert!(second.deduplicated);

    let has = client
        .has_chunk(HasChunkRequest {
            hash: first.hash.clone(),
        })
        .await
        .expect("has chunk")
        .into_inner();

    assert!(has.exists);
    assert_eq!(has.size, Some(data.len() as u64));

    let got = client
        .get_chunk(GetChunkRequest {
            hash: first.hash.clone(),
        })
        .await
        .expect("get chunk")
        .into_inner();

    assert_eq!(got.data, data);

    let deleted = client
        .delete_chunk(DeleteChunkRequest { hash: first.hash })
        .await
        .expect("delete chunk")
        .into_inner();

    assert!(deleted.deleted);

    let _ = shutdown_tx.send(());
}

#[tokio::test]
async fn test_put_chunk_hash_mismatch() {
    let (mut client, shutdown_tx) = setup_client().await;

    let data = b"hash mismatch".to_vec();
    let err = client
        .put_chunk(PutChunkRequest {
            data,
            expected_hash: Some("bad-hash".to_string()),
        })
        .await
        .expect_err("hash mismatch should fail");

    assert_eq!(err.code(), Code::InvalidArgument);

    let _ = shutdown_tx.send(());
}

#[tokio::test]
async fn test_get_chunk_not_found() {
    let (mut client, shutdown_tx) = setup_client().await;

    let err = client
        .get_chunk(GetChunkRequest {
            hash: "missing".to_string(),
        })
        .await
        .expect_err("missing chunk");

    assert_eq!(err.code(), Code::NotFound);

    let _ = shutdown_tx.send(());
}

#[tokio::test]
async fn test_put_get_file_streaming() {
    let (mut client, shutdown_tx) = setup_client().await;

    let data = b"streaming file data".repeat(4096);

    let requests = vec![
        PutFileRequest {
            payload: Some(Payload::Metadata(FileMetadata {
                path: "/docs/file.txt".to_string(),
                content_type: Some("text/plain".to_string()),
            })),
        },
        PutFileRequest {
            payload: Some(Payload::Chunk(data.clone())),
        },
    ];

    let response = client
        .put_file(iter(requests))
        .await
        .expect("put file")
        .into_inner();

    assert_eq!(response.total_size, data.len() as u64);
    assert!(response.chunk_count > 0);

    let mut stream = client
        .get_file(GetFileRequest {
            manifest_hash: response.manifest_hash,
        })
        .await
        .expect("get file")
        .into_inner();

    let mut reconstructed = Vec::new();
    while let Some(msg) = stream.message().await.expect("stream message") {
        reconstructed.extend_from_slice(&msg.data);
    }

    assert_eq!(reconstructed, data);

    let _ = shutdown_tx.send(());
}

#[tokio::test]
async fn test_put_file_without_data() {
    let (mut client, shutdown_tx) = setup_client().await;

    let requests = vec![PutFileRequest {
        payload: Some(Payload::Metadata(FileMetadata {
            path: "/empty".to_string(),
            content_type: None,
        })),
    }];

    let err = client
        .put_file(iter(requests))
        .await
        .expect_err("empty file should fail");

    assert_eq!(err.code(), Code::InvalidArgument);

    let _ = shutdown_tx.send(());
}

#[tokio::test]
async fn test_put_file_requires_metadata_before_chunks() {
    let (mut client, shutdown_tx) = setup_client().await;

    let requests = vec![PutFileRequest {
        payload: Some(Payload::Chunk(b"data without metadata".to_vec())),
    }];

    let err = client
        .put_file(iter(requests))
        .await
        .expect_err("put_file without metadata should fail");

    assert_eq!(err.code(), Code::InvalidArgument);

    let _ = shutdown_tx.send(());
}

#[tokio::test]
async fn test_put_file_rejects_duplicate_metadata() {
    let (mut client, shutdown_tx) = setup_client().await;

    let requests = vec![
        PutFileRequest {
            payload: Some(Payload::Metadata(FileMetadata {
                path: "/docs/file.txt".to_string(),
                content_type: None,
            })),
        },
        PutFileRequest {
            payload: Some(Payload::Metadata(FileMetadata {
                path: "/docs/other.txt".to_string(),
                content_type: None,
            })),
        },
        PutFileRequest {
            payload: Some(Payload::Chunk(b"data".to_vec())),
        },
    ];

    let err = client
        .put_file(iter(requests))
        .await
        .expect_err("put_file with duplicate metadata should fail");

    assert_eq!(err.code(), Code::InvalidArgument);

    let _ = shutdown_tx.send(());
}

#[tokio::test]
async fn test_get_file_reports_missing_chunk() {
    let (mut client, shutdown_tx) = setup_client().await;

    let data = b"streaming file data".repeat(4096);
    let requests = vec![
        PutFileRequest {
            payload: Some(Payload::Metadata(FileMetadata {
                path: "/docs/file.txt".to_string(),
                content_type: Some("text/plain".to_string()),
            })),
        },
        PutFileRequest {
            payload: Some(Payload::Chunk(data)),
        },
    ];

    let response = client
        .put_file(iter(requests))
        .await
        .expect("put file")
        .into_inner();

    client
        .delete_chunk(DeleteChunkRequest {
            hash: response.chunk_hashes[0].clone(),
        })
        .await
        .expect("delete chunk");

    let mut stream = client
        .get_file(GetFileRequest {
            manifest_hash: response.manifest_hash,
        })
        .await
        .expect("get file stream")
        .into_inner();

    let err = stream
        .message()
        .await
        .expect_err("missing chunk should surface as stream error");

    assert_eq!(err.code(), Code::DataLoss);

    let _ = shutdown_tx.send(());
}
