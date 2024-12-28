fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Generate proto files on every build
    println!("cargo:rerun-if-changed=../proto/dataplane.proto");
    
    // Ensure output directory exists
    std::fs::create_dir_all("src/proto")?;
    
    // Compile protobuf
    tonic_build::configure()
        .build_server(true)
        .build_client(true)
        .out_dir("src/proto")
        .compile_protos(&["../proto/dataplane.proto"], &["../proto"])?;
    
    Ok(())
}
