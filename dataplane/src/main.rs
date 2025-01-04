//! DataPlane main entry point
//! 
//! Provides HTTP API and gRPC API for CAS storage operations

use std::io::IsTerminal;
use std::net::SocketAddr;
use std::path::PathBuf;
use std::sync::Arc;

use anyhow::Result;
use clap::Parser;
use tokio::io::AsyncReadExt;
use tonic::transport::Server;
use tracing::{info, Level};
use tracing_subscriber::FmtSubscriber;

use dataplane::{CasStore, CasConfig, Chunker, ChunkerConfig, DataPlaneService};

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

#[tokio::main]
async fn main() -> Result<()> {
    let args = Args::parse();
    
    // Initialize logging
    // Disable ANSI colors when not writing to a terminal (e.g., redirected to file)
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
    };
    
    // Create CDC configuration
    let chunker_config = ChunkerConfig {
        min_size: args.min_chunk_kb * 1024,
        avg_size: args.avg_chunk_kb * 1024,
        max_size: args.max_chunk_kb * 1024,
    };
    
    // Create storage for HTTP server
    let cas = Arc::new(CasStore::new(cas_config.clone()).await?);
    let _chunker = Arc::new(Chunker::new(chunker_config.clone()));
    
    // Start gRPC server
    let grpc_addr = args.grpc;
    let grpc_service = DataPlaneService::new(cas_config, chunker_config).await?;
    
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
    
    info!(address = %args.listen, "HTTP service ready");
    
    // HTTP server for health checks
    let listener = tokio::net::TcpListener::bind(args.listen).await?;
    
    loop {
        let (mut socket, addr) = listener.accept().await?;
        let cas = Arc::clone(&cas);
        
        tokio::spawn(async move {
            let mut buf = vec![0u8; 65536];
            
            match socket.read(&mut buf).await {
                Ok(n) if n > 0 => {
                    let request = String::from_utf8_lossy(&buf[..n]);
                    
                    // Simple HTTP parsing
                    if request.starts_with("GET /health") {
                        let (chunks, size, hits, misses) = cas.stats();
                        let response = format!(
                            "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n\
                            {{\"status\":\"healthy\",\"chunks\":{},\"size\":{},\"hits\":{},\"misses\":{}}}",
                            chunks, size, hits, misses
                        );
                        let _ = tokio::io::AsyncWriteExt::write_all(&mut socket, response.as_bytes()).await;
                    } else if request.starts_with("GET /stats") {
                        let (chunks, size, hits, misses) = cas.stats();
                        let dedup_ratio = if hits + misses > 0 {
                            hits as f64 / (hits + misses) as f64
                        } else {
                            0.0
                        };
                        let response = format!(
                            "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n\
                            {{\"total_chunks\":{},\"total_size\":{},\"dedup_ratio\":{:.2}}}",
                            chunks, size, dedup_ratio
                        );
                        let _ = tokio::io::AsyncWriteExt::write_all(&mut socket, response.as_bytes()).await;
                    } else {
                        let response = "HTTP/1.1 404 Not Found\r\n\r\nNot Found";
                        let _ = tokio::io::AsyncWriteExt::write_all(&mut socket, response.as_bytes()).await;
                    }
                }
                _ => {}
            }
            
            info!(client = %addr, "connection handling complete");
        });
    }
}
