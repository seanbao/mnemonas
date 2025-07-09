use std::path::PathBuf;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let repo_root = std::fs::canonicalize(PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../.."))?;
    let proto_dir = repo_root.join("proto");
    let proto_file = proto_dir.join("dataplane.proto");
    let out_dir = repo_root.join("dataplane/src/proto");

    std::fs::create_dir_all(&out_dir)?;

    tonic_build::configure()
        .build_server(true)
        .build_client(true)
        .out_dir(out_dir)
        .compile_protos(&[proto_file], &[proto_dir])?;

    Ok(())
}
