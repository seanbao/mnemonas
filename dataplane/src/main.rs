//! DataPlane main entry point
//! 
//! Provides HTTP API and gRPC API for CAS storage operations

use std::io::IsTerminal;
use std::net::SocketAddr;
use std::path::PathBuf;
use std::sync::Arc;

use anyhow::Result;
use axum::{
    extract::State,
    http::StatusCode,
    response::Json,
    routing::get,
    Router,
};
use clap::Parser;
use serde::Serialize;
use tonic::transport::Server;
use tower_http::trace::TraceLayer;
use tracing::{info, Level};
use tracing_subscriber::FmtSubscriber;

use dataplane::{CasStore, CasConfig, ChunkerConfig, DataPlaneService};

/// MnemoNAS DataPlane - High-performance Rust data plane
#[derive(Parser, Debug)]
#[command(name = "dataplane", version, about)]
struct Args {
    /// HTTP listen address
    #[arg(short, long, default_value = "127.0.0.1:9091")]
    listen: SocketAddr,
    
    /// gRPC listen address
    #[arg(short, long, default_value = "127.0.0.1:9090")]
    grpc: SocketAddr,
    
    /// CAS storage directory
    #[arg(short, long, default_value = "/tmp/mnemonas/cas")]
    data_dir: PathBuf,
    
    /// Log level
    #[arg(long, default_value = "info")]
    log_level: Level,
    
    /// CDC minimum chunk size (KB)
    #[arg(long, default_value = "256")]
    min_chunk_kb: u32,
    
    /// CDC average chunk size (KB)
    #[arg(long, default_value = "1024")]
    avg_chunk_kb: u32,
    
    /// CDC maximum chunk size (KB)
    #[arg(long, default_value = "4096")]
    max_chunk_kb: u32,
}

/// Application state shared across handlers
#[derive(Clone)]
struct AppState {
    cas: Arc<CasStore>,
}

/// Health check response
#[derive(Serialize)]
struct HealthResponse {
    status: &'static str,
    chunks: u64,
    size: u64,
    compressed_size: u64,
    hits: u64,
    misses: u64,
}

/// Stats response
#[derive(Serialize)]
struct StatsResponse {
    total_chunks: u64,
    total_size: u64,
    compressed_size: u64,
    compression_ratio: f64,
    dedup_ratio: f64,
}

/// Health check endpoint
async fn health_handler(State(state): State<AppState>) -> Json<HealthResponse> {
    let (chunks, size, compressed_size, hits, misses) = state.cas.stats();
    Json(HealthResponse {
        status: "healthy",
        chunks,
        size,
        compressed_size,
        hits,
        misses,
    })
}

/// Stats endpoint
async fn stats_handler(State(state): State<AppState>) -> Json<StatsResponse> {
    let (chunks, size, compressed_size, hits, misses) = state.cas.stats();
    let dedup_ratio = if hits + misses > 0 {
        hits as f64 / (hits + misses) as f64
    } else {
        0.0
    };
    let compression_ratio = if size > 0 {
        compressed_size as f64 / size as f64
    } else {
        1.0
    };
    Json(StatsResponse {
        total_chunks: chunks,
        total_size: size,
        compressed_size,
        compression_ratio,
        dedup_ratio,
    })
}

/// 404 fallback
async fn not_found() -> StatusCode {
    StatusCode::NOT_FOUND
}

#[tokio::main]
async fn main() -> Result<()> {
    let args = Args::parse();
    
    // Initialize logging
    let use_ansi = std::io::stderr().is_terminal();
    FmtSubscriber::builder()
        .with_max_level(args.log_level)
        .with_target(false)
        .with_thread_ids(false)
        .with_file(false)
        .with_line_number(false)
        .with_ansi(use_ansi)
        .init();
    
    info!(
        version = env!("CARGO_PKG_VERSION"),
        http_addr = %args.listen,
        grpc_addr = %args.grpc,
        data_dir = %args.data_dir.display(),
        "starting MnemoNAS DataPlane"
    );
    
    // Create CAS configuration
    let cas_config = CasConfig {
        root: args.data_dir.clone(),
        shard_levels: 2,
        shard_size: 2,
        ..Default::default()
    };
    
    // Create CDC configuration
    let chunker_config = ChunkerConfig {
        min_size: args.min_chunk_kb * 1024,
        avg_size: args.avg_chunk_kb * 1024,
        max_size: args.max_chunk_kb * 1024,
    };
    
    // Create shared CAS storage
    let cas = Arc::new(CasStore::new(cas_config).await?);
    
    // Start gRPC server with shared CAS
    let grpc_addr = args.grpc;
    let grpc_service = DataPlaneService::with_cas(Arc::clone(&cas), chunker_config);
    
    tokio::spawn(async move {
        info!(address = %grpc_addr, "gRPC service starting");
        if let Err(e) = Server::builder()
            .add_service(grpc_service.into_server())
            .serve(grpc_addr)
            .await
        {
            tracing::error!(error = %e, "gRPC server error");
        }
    });
    
    // Build HTTP router with axum
    let state = AppState { cas };
    let app = Router::new()
        .route("/health", get(health_handler))
        .route("/stats", get(stats_handler))
        .fallback(not_found)
        .layer(TraceLayer::new_for_http())
        .with_state(state);
    
    // Start HTTP server
    info!(address = %args.listen, "HTTP service ready");
    let listener = tokio::net::TcpListener::bind(args.listen).await?;
    axum::serve(listener, app).await?;
    
    Ok(())
}
