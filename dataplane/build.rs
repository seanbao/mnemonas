fn main() {
    // The Rust protobuf code is checked in so normal builds do not need protoc.
    println!("cargo:rerun-if-changed=../proto/dataplane.proto");
    println!("cargo:rerun-if-changed=src/proto/mnemonas.dataplane.v1.rs");
}
