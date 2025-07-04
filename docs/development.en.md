# MnemoNAS Development Guide

English | [简体中文](development.md)

This guide explains how to set up a local MnemoNAS development environment, build each component, run tests, and debug the system.

## Requirements

| Tool | Minimum | Recommended | Purpose |
| --- | --- | --- | --- |
| Go | 1.25.11 | 1.25.11+ | Go control plane |
| Rust | 1.92 | 1.92.x | Rust data plane |
| Node.js | `^20.19.0` or `>=22.12.0` | `.nvmrc` 22.x | Frontend |
| protoc | 3.20 | 3.20.1 for CI parity | Regenerate protobuf code |
| make | 3.x | 4.x | Build automation |

Optional:

| Tool | Purpose |
| --- | --- |
| Docker Engine + Compose v2 | Container build and deployment |
| golangci-lint | Required by `make lint` and `make check` unless explicitly skipped |
| cargo-watch | Rust hot reload |
| nvm | Node.js version management |

The repository includes `.go-version` and `.nvmrc`. Rust version is declared in `dataplane/Cargo.toml`. Frontend commands should run after `nvm use`.

## Install Dependencies

### macOS

```bash
brew install go

curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
source ~/.cargo/env

brew install nvm
nvm install 22
nvm use 22

brew install protobuf

go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1

brew install golangci-lint
cargo install cargo-watch --version 8.5.3
```

### Ubuntu / Debian

```bash
sudo apt update

GO_VERSION=1.25.11
wget "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
sudo tar -C /usr/local -xzf "go${GO_VERSION}.linux-amd64.tar.gz"
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc
source ~/.bashrc

curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
source ~/.cargo/env

curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash
source ~/.nvm/nvm.sh
nvm install 22
nvm use 22

sudo apt install protobuf-compiler
protoc --version

go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1

echo fs.inotify.max_user_watches=524288 | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

If the distribution `protoc` is older than 3.20, use a backport or official prebuilt package. CI uses 3.20.1 to keep committed generated Go headers stable.

### Windows

WSL2 Ubuntu is recommended. For native Windows, install Go, Rust, Node.js, and protobuf through winget or scoop.

## Verify Tooling

```bash
go version
rustc --version
node --version
npm --version
protoc --version

which protoc-gen-go
which protoc-gen-go-grpc

source ~/.nvm/nvm.sh
nvm use
```

## Repository Layout

```text
mnemonas/
├── cmd/nasd/              # Go control-plane entrypoint
├── internal/              # Go internal packages
│   ├── api/               # REST API
│   ├── auth/              # users, JWTs, passwords
│   ├── config/            # TOML config
│   ├── storage/           # storage orchestration
│   ├── versionstore/      # versions, trash, metadata
│   ├── webdav/            # WebDAV implementation
│   └── workspace/         # native file operations
├── dataplane/             # Rust data plane
├── web/                   # React frontend
├── proto/                 # gRPC protocol definitions
├── scripts/               # dev, test, deployment helpers
├── docs/                  # documentation
├── docker-compose.yml
├── Dockerfile
├── Makefile
└── mnemonas.example.toml
```

## Build

Full build:

```bash
git clone https://github.com/seanbao/mnemonas.git
cd mnemonas

make deps
make build
```

Artifacts:

```text
bin/nasd
bin/dataplane
web/dist/
```

Step-by-step:

```bash
make proto

CGO_ENABLED=0 go build -o bin/nasd ./cmd/nasd

cd dataplane && cargo build --release --locked
cp target/release/dataplane ../bin/

cd web && npm run build
```

Normal Rust and Docker builds use committed generated Rust protobuf code and do not need `protoc` unless protobuf files are regenerated.

Fast debug build:

```bash
make dev
```

## Local Development

Use the helper script for most work:

```bash
source ~/.nvm/nvm.sh
nvm use

./scripts/dev.sh
./scripts/dev.sh --backend
./scripts/dev.sh --creds # hides the WebDAV plaintext password by default
./scripts/dev.sh --frontend
./scripts/dev.sh --status
./scripts/dev.sh --kill
```

The script:

- `--creds` shows the initial password file and WebDAV credential location without printing the WebDAV plaintext password. Set `MNEMONAS_DEV_SHOW_SECRETS=1 ./scripts/dev.sh --creds` only for deliberate local-terminal disclosure.

- Builds Go and Rust components.
- Starts dataplane, `nasd`, and optionally Vite.
- Checks ports and service readiness.
- Writes logs under `logs/`.
- Tracks process IDs under `.pids/`.
- Enforces the Node.js engine before frontend startup.

### Manual Component Startup

Terminal 1:

```bash
cd dataplane
cargo run -- --data-dir ~/.mnemonas/.mnemonas/objects --grpc 127.0.0.1:9090 --listen 127.0.0.1:9091
```

Terminal 2:

```bash
go run ./cmd/nasd
# or
./bin/nasd
```

Terminal 3:

```bash
source ~/.nvm/nvm.sh
nvm use

cd web
npm run dev
```

Frontend dev server: `http://localhost:5173`; API proxy target: `http://localhost:8080`.

To have `nasd` serve the static Web UI directly, build the frontend first or set `MNEMONAS_WEB_DIR=web/dist`.

## Ports

| Service | Port | Description |
| --- | --- | --- |
| `nasd` | 8080 | Web UI, REST API, WebDAV |
| dataplane HTTP | 9091 | Health and stats |
| dataplane gRPC | 9090 | CAS storage service |
| Vite | 5173 | Frontend dev server |

## Development Config

`~/.mnemonas/config.toml`:

```toml
[server]
host = "127.0.0.1"
port = 8080

[storage]
root = "~/.mnemonas"

[dataplane]
grpc_address = "127.0.0.1:9090"

[webdav]
enabled = true
prefix = "/dav"

[log]
level = "debug"
format = "console"
```

## Tests

Main entry points:

```bash
make verify-changed
make test
make test-torture
make e2e
make bench
make lint
make check
make docs-check
```

`make verify-changed` selects workflow, script, Go/Rust, frontend, E2E, Docker, and documentation checks from the changed files in the worktree, staged area, or a configured base ref. Documentation-only changes run `make docs-check`, which validates local Markdown links against files and heading anchors in the repository and confirms that README, CHANGELOG, SUPPORT, SECURITY, the Web README, and documents under `docs/` keep Chinese and English pairs; documents under `docs/` must also appear in both documentation indexes.

`make lint` and `make check` require `golangci-lint` unless `SKIP_GOLANGCI_LINT=1` is explicitly set for local troubleshooting.

### Go

```bash
GO_PACKAGES=$(make --no-print-directory go-packages)
bash ./scripts/with-test-dataplane.sh go test -v $GO_PACKAGES

bash ./scripts/with-test-dataplane.sh go test -v ./internal/webdav/...

bash ./scripts/with-test-dataplane.sh go test -v -cover $GO_PACKAGES

bash ./scripts/with-test-dataplane.sh go test -coverprofile=coverage.out $GO_PACKAGES
go tool cover -html=coverage.out
```

The temporary dataplane started by `with-test-dataplane.sh` accepts only loopback gRPC/HTTP addresses: `localhost`, `ip6-localhost`, `::1`, or dotted-quad numeric `127.0.0.0/8` addresses. Overrides through `MNEMONAS_TEST_DATAPLANE_ADDR` or `MNEMONAS_TEST_DATAPLANE_HTTP_ADDR` must remain loopback and must not contain whitespace or control characters, so the test service is not exposed on public or untrusted LAN interfaces.

After installing frontend dependencies, do not use `go test ./...` or `go list ./...` as the repository package set; Go will traverse third-party packages under `web/node_modules`. Use `make --no-print-directory go-packages` for repository-wide Go checks.

### Rust

```bash
cd dataplane
cargo test
cargo test test_cas_store
cargo test -- --nocapture
```

Coverage:

```bash
cargo install cargo-tarpaulin
cargo tarpaulin --out Html
```

### Frontend

```bash
cd web
npm run check:node
npm run test:run
npm run test
npm run test:coverage
npm run lint
npx tsc --noEmit
npm run test:e2e
npm run test:e2e:ui
```

Playwright starts isolated backend and frontend servers by default. Local runs use 4 workers unless `MNEMONAS_E2E_WORKERS` is set to a positive integer; CI uses 1 worker. The isolated backend uses a 2-hour access-token lifetime and a 168-hour refresh-token lifetime so long parallel E2E runs do not enter concurrent refresh-token rotation after a shared storageState expires. To reuse existing services, set `MNEMONAS_E2E_REUSE_EXISTING=1`, `MNEMONAS_E2E_BACKEND_URL`, `MNEMONAS_E2E_FRONTEND_URL`, and `E2E_PASSWORD`.

### WebDAV Smoke Test

```bash
WEBDAV_USER="<webdav-username>"
WEBDAV_PASS="<webdav-password>"

curl -u "$WEBDAV_USER:$WEBDAV_PASS" -X PROPFIND http://localhost:8080/dav/ -H "Depth: 1"
curl -u "$WEBDAV_USER:$WEBDAV_PASS" -X PUT http://localhost:8080/dav/test.txt -d "Hello World"
curl -u "$WEBDAV_USER:$WEBDAV_PASS" http://localhost:8080/dav/test.txt

curl http://localhost:8080/health
curl http://localhost:9091/health
curl http://localhost:9091/stats
```

`9091` should remain local/private.

### E2E

```bash
make e2e
./scripts/run-e2e-isolated.sh --quick
```

The isolated runner avoids writing into a real user storage root. `scripts/e2e-test.sh` can target an explicit running service, but it requires explicit `BASE_URL`, `STORAGE_ROOT`, `CONFIG_FILE`, `SECRETS_FILE`, and `INITIAL_PASSWORD_FILE`. `STORAGE_ROOT` must not contain control characters, `..`, or symlink path components.

The isolated E2E runner and Playwright backend accept only loopback Web and dataplane addresses: `localhost`, `ip6-localhost`, `::1`, or dotted-quad numeric `127.0.0.0/8` addresses. This prevents test backends from unintentionally listening on public or untrusted LAN interfaces.

### Fault Injection

Fault injection kills and restarts `nasd` and can corrupt test objects. It is disabled by default and must target an isolated instance:

```bash
MNEMONAS_LIVE_FAULTS=1 \
BASE_URL=http://127.0.0.1:18080 \
STORAGE_ROOT=/tmp/mnemonas-fault-target \
NASD_BIN="$PWD/bin/nasd" \
FAULT_INJECTION_ASSUME_YES=1 \
RUN_CORRUPTION_TESTS=0 \
./scripts/fault-injection-test.sh
```

Safety checks are covered by `scripts/test-fault-injection-safety.sh` and `make scripts-check`. `BASE_URL`, `STORAGE_ROOT`, and `NASD_BIN` must be explicit. Real storage paths require `ALLOW_REAL_STORAGE=1`, must still be absolute, must not contain control characters, `..`, or symlink path components, and must not point at protected system directories such as `/`, `/tmp`, or `/var`. `OBJECTS_DIR` and `INDEX_DB`, which may be read or modified by the destructive checks, must be under `STORAGE_ROOT`.

### Benchmarks

```bash
make bench
./scripts/run-benchmark-isolated.sh
```

Manual benchmark against an explicit service:

```bash
MNEMONAS_STORAGE_ROOT=/tmp/mnemonas-bench-target \
./scripts/benchmark.sh http://127.0.0.1:18080
```

Manual benchmark targets create and remove `storage.root/files/benchmark-test`. Real storage paths require `ALLOW_REAL_STORAGE=1`, must still be absolute, must not contain control characters, `..`, or symlink path components, and must not point at protected system directories.

The isolated benchmark runner uses the same loopback-only rule for Web and dataplane addresses. To benchmark a remote or shared-network instance, run `scripts/benchmark.sh` directly with an explicit isolated `MNEMONAS_STORAGE_ROOT`.

## Debugging

### Go

```bash
go install "github.com/go-delve/delve/cmd/dlv@v1.26.3"
dlv debug ./cmd/nasd
```

### Rust

```bash
cd dataplane
cargo build
rust-lldb target/debug/dataplane
```

### Logs

```bash
LOG_LEVEL=debug ./bin/nasd
RUST_LOG=debug ./bin/dataplane
```

Or configure:

```toml
[log]
level = "debug"
```

### Network

```bash
grpcurl -plaintext localhost:9090 list
grpcurl -plaintext localhost:9090 describe
sudo tcpdump -i lo port 8080 -w debug.pcap
```

## Common Issues

### `protoc-gen-go: program not found`

```bash
export PATH=$PATH:$(go env GOPATH)/bin
```

### Go Toolchain Download Fails

The repo pins `toolchain go1.25.11`. When local network access blocks toolchain download but a compatible local Go 1.25.x is available:

```bash
packages=$(GOTOOLCHAIN=local make --no-print-directory go-packages)
GOTOOLCHAIN=local go test $packages
GOTOOLCHAIN=local make build
```

Release builds and vulnerability scans should use Go 1.25.11 or a newer 1.25.x patch version.

If `GOSUMDB=off` breaks toolchain verification:

```bash
GOSUMDB=sum.golang.org go version
GOSUMDB=sum.golang.org govulncheck ./...
```

### Frontend Watcher Limit

```bash
echo fs.inotify.max_user_watches=524288 | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

### Reset Development Data

```bash
DEFAULT_DATA_DIR="$HOME/.mnemonas"
DATA_DIR="${MNEMONAS_DATA_DIR:-$DEFAULT_DATA_DIR}"
[ "$DATA_DIR" = "$DEFAULT_DATA_DIR" ] || { echo "refusing non-default DATA_DIR; inspect and delete manually: $DATA_DIR"; exit 1; }
[ ! -L "$DATA_DIR" ] || { echo "refusing symlink DATA_DIR: $DATA_DIR"; exit 1; }
rm -rf -- "$DATA_DIR"
```

## Code Style

Go:

```bash
go fmt ./...
```

Rust:

```bash
cd dataplane && cargo fmt
```

Frontend:

```bash
cd web
npm run lint
npm run build
```

Commits follow Conventional Commits:

```text
feat(webdav): add ETag support for conditional requests
fix(dataplane): fix memory leak in CDC chunking
docs(readme): update installation instructions
```
