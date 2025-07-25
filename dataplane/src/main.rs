//! DataPlane main entry point
//!
//! Provides HTTP API and gRPC API for CAS storage operations

use std::io::IsTerminal;
use std::net::SocketAddr;
use std::path::PathBuf;
use std::sync::Arc;

use anyhow::{bail, Result};
use axum::{extract::State, http::StatusCode, response::Json, routing::get, Router};
use clap::Parser;
use serde::Serialize;
use tonic::transport::Server;
use tower_http::trace::TraceLayer;
use tracing::{info, Level};
use tracing_subscriber::FmtSubscriber;

use dataplane::{CasConfig, CasStore, ChunkerConfig, DataPlaneService};

const MIN_CDC_CHUNK_SIZE: u32 = 64 * 1024;
const MAX_CDC_CHUNK_SIZE: u32 = 64 * 1024 * 1024;

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
    #[arg(short, long)]
    data_dir: Option<PathBuf>,

    /// Log level
    #[arg(long, default_value = "info")]
    log_level: Level,

    /// CDC minimum chunk size (bytes)
    #[arg(long, default_value_t = 262_144)]
    min_chunk_size: u32,

    /// CDC average chunk size (bytes)
    #[arg(long, default_value_t = 1_048_576)]
    avg_chunk_size: u32,

    /// CDC maximum chunk size (bytes)
    #[arg(long, default_value_t = 4_194_304)]
    max_chunk_size: u32,

    /// Legacy CDC minimum chunk size (KiB)
    #[arg(long, hide = true)]
    min_chunk_kb: Option<u32>,

    /// Legacy CDC average chunk size (KiB)
    #[arg(long, hide = true)]
    avg_chunk_kb: Option<u32>,

    /// Legacy CDC maximum chunk size (KiB)
    #[arg(long, hide = true)]
    max_chunk_kb: Option<u32>,
}

fn default_data_dir() -> PathBuf {
    if let Ok(home) = std::env::var("HOME") {
        return PathBuf::from(home)
            .join(".mnemonas")
            .join(".mnemonas")
            .join("objects");
    }
    PathBuf::from("./data/.mnemonas/objects")
}

fn chunk_size_from_args(bytes: u32, kib: Option<u32>, label: &str) -> Result<u32> {
    if let Some(kib) = kib {
        return kib
            .checked_mul(1024)
            .ok_or_else(|| anyhow::anyhow!("{label} is too large"));
    }
    Ok(bytes)
}

fn chunker_config_from_args(args: &Args) -> Result<ChunkerConfig> {
    let min_size = chunk_size_from_args(args.min_chunk_size, args.min_chunk_kb, "min chunk size")?;
    let avg_size = chunk_size_from_args(args.avg_chunk_size, args.avg_chunk_kb, "avg chunk size")?;
    let max_size = chunk_size_from_args(args.max_chunk_size, args.max_chunk_kb, "max chunk size")?;

    if min_size < MIN_CDC_CHUNK_SIZE {
        bail!("min chunk size must be greater than or equal to {MIN_CDC_CHUNK_SIZE} bytes");
    }
    if min_size >= avg_size {
        bail!("min chunk size must be less than avg chunk size");
    }
    if avg_size >= max_size {
        bail!("avg chunk size must be less than max chunk size");
    }
    if max_size > MAX_CDC_CHUNK_SIZE {
        bail!("max chunk size must be less than or equal to {MAX_CDC_CHUNK_SIZE} bytes");
    }

    Ok(ChunkerConfig {
        min_size,
        avg_size,
        max_size,
    })
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
    let (chunks, _logical_size, unique_size, compressed_size, hits, misses) = state.cas.stats();
    Json(HealthResponse {
        status: "healthy",
        chunks,
        size: unique_size,
        compressed_size,
        hits,
        misses,
    })
}

/// Stats endpoint
async fn stats_handler(State(state): State<AppState>) -> Json<StatsResponse> {
    let (chunks, logical_size, unique_size, compressed_size, _hits, _misses) = state.cas.stats();
    let dedup_ratio = if logical_size > 0 {
        1.0 - (unique_size as f64 / logical_size as f64)
    } else {
        0.0
    };
    let compression_ratio = if unique_size > 0 {
        compressed_size as f64 / unique_size as f64
    } else {
        1.0
    };
    Json(StatsResponse {
        total_chunks: chunks,
        total_size: logical_size,
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

    let data_dir = args.data_dir.clone().unwrap_or_else(default_data_dir);

    info!(
        version = env!("CARGO_PKG_VERSION"),
        http_addr = %args.listen,
        grpc_addr = %args.grpc,
        data_dir = %data_dir.display(),
        "starting MnemoNAS DataPlane"
    );

    // Create CAS configuration
    let cas_config = CasConfig {
        root: data_dir.clone(),
        shard_levels: 2,
        shard_size: 2,
        ..Default::default()
    };

    // Create CDC configuration
    let chunker_config = chunker_config_from_args(&args)?;

    // Create shared CAS storage
    let cas = Arc::new(CasStore::new(cas_config).await?);

    // Start gRPC and HTTP services together. If either listener fails to bind or
    // either server exits with an error, the process must fail instead of
    // leaving a misleading healthy endpoint behind.
    let grpc_addr = args.grpc;
    let grpc_service = DataPlaneService::with_cas(Arc::clone(&cas), chunker_config);
    let grpc_server = async move {
        info!(address = %grpc_addr, "gRPC service starting");
        Server::builder()
            .add_service(grpc_service.into_server())
            .serve(grpc_addr)
            .await?;
        Ok::<(), anyhow::Error>(())
    };

    // Build HTTP router with axum
    let state = AppState { cas };
    let app = Router::new()
        .route("/health", get(health_handler))
        .route("/stats", get(stats_handler))
        .fallback(not_found)
        .layer(TraceLayer::new_for_http())
        .with_state(state);

    let http_addr = args.listen;
    let http_server = async move {
        let listener = tokio::net::TcpListener::bind(http_addr).await?;
        info!(address = %http_addr, "HTTP service ready");
        axum::serve(listener, app).await?;
        Ok::<(), anyhow::Error>(())
    };

    tokio::try_join!(grpc_server, http_server)?;

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn args_with_chunk_sizes(min: u32, avg: u32, max: u32) -> Args {
        Args {
            listen: "127.0.0.1:9091".parse().expect("listen addr"),
            grpc: "127.0.0.1:9090".parse().expect("grpc addr"),
            data_dir: None,
            log_level: Level::INFO,
            min_chunk_size: min,
            avg_chunk_size: avg,
            max_chunk_size: max,
            min_chunk_kb: None,
            avg_chunk_kb: None,
            max_chunk_kb: None,
        }
    }

    #[test]
    fn chunker_config_uses_byte_sized_flags() {
        let args = args_with_chunk_sizes(65_536, 262_144, 1_048_576);

        let config = chunker_config_from_args(&args).expect("valid chunker config");

        assert_eq!(config.min_size, 65_536);
        assert_eq!(config.avg_size, 262_144);
        assert_eq!(config.max_size, 1_048_576);
    }

    #[test]
    fn chunker_config_accepts_legacy_kib_flags() {
        let mut args = args_with_chunk_sizes(262_144, 1_048_576, 4_194_304);
        args.min_chunk_kb = Some(128);
        args.avg_chunk_kb = Some(512);
        args.max_chunk_kb = Some(2048);

        let config = chunker_config_from_args(&args).expect("valid legacy chunker config");

        assert_eq!(config.min_size, 131_072);
        assert_eq!(config.avg_size, 524_288);
        assert_eq!(config.max_size, 2_097_152);
    }

    #[test]
    fn chunker_config_rejects_invalid_order() {
        let args = args_with_chunk_sizes(1_048_576, 262_144, 4_194_304);

        let err = chunker_config_from_args(&args).expect_err("invalid chunker config");

        assert!(err.to_string().contains("min chunk size"));
    }

    #[test]
    fn chunker_config_rejects_tiny_min_chunk() {
        let args = args_with_chunk_sizes(MIN_CDC_CHUNK_SIZE - 1, 262_144, 1_048_576);

        let err = chunker_config_from_args(&args).expect_err("undersized chunker config");

        assert!(err.to_string().contains(&MIN_CDC_CHUNK_SIZE.to_string()));
    }

    #[test]
    fn chunker_config_rejects_oversized_max_chunk() {
        let args =
            args_with_chunk_sizes(16 * 1024 * 1024, 32 * 1024 * 1024, MAX_CDC_CHUNK_SIZE + 1);

        let err = chunker_config_from_args(&args).expect_err("oversized chunker config");

        assert!(err.to_string().contains("max chunk size"));
    }
}
