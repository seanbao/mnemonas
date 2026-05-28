# MnemoNAS Development Guide

English | [简体中文](development.md)

This guide explains how to set up a local MnemoNAS development environment, build each component, run tests, and debug the system.

## Requirements

| Tool | Minimum | Recommended | Purpose |
| --- | --- | --- | --- |
| Go | 1.25.11 | 1.25.11+ | Go control plane |
| Rust | 1.92 | 1.92.x | Rust data plane and protobuf generator |
| Node.js | `^20.19.0` or `>=22.12.0` | `.nvmrc` 22.x | Frontend |
| protoc | 3.20 | 3.20.1 for CI parity | Regenerate protobuf code |
| make | 3.x | 4.x | Build automation |

Optional:

| Tool | Purpose |
| --- | --- |
| Docker Engine + Compose v2 | Container build and deployment |
| golangci-lint | Required by `make lint` and `make check` unless explicitly skipped |
| Python 3 | Untracked text-file whitespace checks and local validation scripts in `make verify-changed` |
| PyYAML | YAML syntax and duplicate-key validation in `make verify-changed`, `make workflows-check`, and `make docs-check` |
| `timeout` or `gtimeout` | Bounds Docker image builds and container smoke tests selected by `make verify-changed`; on macOS, GNU coreutils can provide `gtimeout` |
| cargo-watch | Rust hot reload |
| nvm | Node.js version management |

The repository includes `.go-version` and `.nvmrc`. Rust versions are declared in `dataplane/Cargo.toml` and `tools/proto-gen/Cargo.toml`. Frontend commands should run after `nvm use`.

## Install Dependencies

### macOS

```bash
brew install go

curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs -o /tmp/rustup-init.sh
sed -n '1,120p' /tmp/rustup-init.sh
sh /tmp/rustup-init.sh
source ~/.cargo/env

brew install nvm
nvm install 22
nvm use 22

brew install protobuf

go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1

brew install golangci-lint python
python3 -m pip install --user PyYAML
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

curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs -o /tmp/rustup-init.sh
sed -n '1,120p' /tmp/rustup-init.sh
sh /tmp/rustup-init.sh
source ~/.cargo/env

curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh -o /tmp/nvm-install.sh
sed -n '1,120p' /tmp/nvm-install.sh
bash /tmp/nvm-install.sh
source ~/.nvm/nvm.sh
nvm install 22
nvm use 22

sudo apt install protobuf-compiler
protoc --version

sudo apt install python3-yaml

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
python3 --version
python3 -c 'import yaml'

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
./scripts/dev.sh --creds # shows WebDAV auth mode; hides Basic Auth plaintext by default
./scripts/dev.sh --frontend
./scripts/dev.sh --status
./scripts/dev.sh --kill
```

The script:

- `--creds` shows the initial password file and current WebDAV auth mode.
- `users` mode uses MnemoNAS accounts.
- `none` reports disabled WebDAV auth.
- `basic` hides the plaintext Basic Auth password by default.
- Set `MNEMONAS_DEV_SHOW_SECRETS=1 ./scripts/dev.sh --creds` only for deliberate local-terminal disclosure.

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
make coverage
make docker-check
```

`make verify-changed` selects checks from the changed files in the worktree, staged area, or a configured base ref.
It can select workflow, script, Go/Rust, frontend, E2E, Docker, documentation, dependency-security, toolchain configuration, quality configuration, example configuration, and public-access template checks.
When `go.mod`, `go.sum`, Cargo manifests or lockfiles, or Web npm manifests or lockfiles change, `verify-changed` adds dependency security checks; Web npm manifest or lockfile changes run npm audit with `NPM_AUDIT=1`.
YAML configuration validation rejects syntax errors and duplicate keys within the same mapping so local parsing cannot silently override configuration values.
Use `./scripts/verify-changed.sh --staged` to inspect staged content only, `./scripts/verify-changed.sh --base <ref>` to validate a branch range, and `--dry-run` to review selected commands without executing them.
Docker image build and container smoke checks are bounded by `VERIFY_CHANGED_DOCKER_TIMEOUT=45m` by default; override it for slower networks or build hosts. The script automatically uses `timeout` or GNU coreutils `gtimeout`.

Documentation-only changes run `make docs-check`.
That command validates local Markdown links against files and heading anchors in the repository.
It also validates JSON, YAML, and TOML code fences; JSON and YAML code fences reject duplicate keys within the same object or mapping.
It also confirms that README, CHANGELOG, SUPPORT, SECURITY, the Web README, public-access template README, and documents under `docs/` keep Chinese and English pairs.
Documents under `docs/` must also appear in both documentation indexes.

`make coverage` runs repository-wide Go coverage with the temporary dataplane wrapper, enforces the `GO_COVERAGE_MIN` threshold, runs frontend coverage, and writes ignored local reports to `coverage/go.html` and `web/coverage/`.

`make lint` and `make check` require `golangci-lint` unless `SKIP_GOLANGCI_LINT=1` is explicitly set for local troubleshooting.
Go linting inherits `GO_LINT_ENV` from `GO_CMD_ENV` by default, so local checks use `GOTOOLCHAIN=local`.
Override `GO_LINT_ENV` only when automatic toolchain download is required.

### Go

```bash
GO_PACKAGES=$(make --no-print-directory go-packages)
bash ./scripts/with-test-dataplane.sh go test -v $GO_PACKAGES

bash ./scripts/with-test-dataplane.sh go test -v ./internal/webdav/...

bash ./scripts/with-test-dataplane.sh go test -v -cover $GO_PACKAGES

make coverage
```

The temporary dataplane started by `with-test-dataplane.sh` auto-selects free `127.0.0.1` gRPC and HTTP ports by default.
When unset, `MNEMONAS_TEST_DATAPLANE_ADDR` and `MNEMONAS_TEST_DATAPLANE_HTTP_ADDR` are exported to the wrapped command with the selected addresses.

Overrides must:

- remain loopback: `localhost`, `ip6-localhost`, `::1`, or dotted-quad numeric `127.0.0.0/8`;
- use different ports;
- avoid whitespace and control characters.

These limits prevent the test service from listening on public or untrusted LAN interfaces.

After installing frontend dependencies, do not use `go test ./...` or `go list ./...` as the repository package set; Go will traverse third-party packages under `web/node_modules`. Use `make --no-print-directory go-packages` for repository-wide Go checks.

### Rust

```bash
cd dataplane
cargo test
cargo test test_cas_store
cargo test -- --nocapture
```

Coverage from the repository root:

```bash
cargo install cargo-llvm-cov --locked
make rust-coverage
```

### Frontend

```bash
cd web
npm run check:node
npm run test:run
npm run test
npm run test:coverage
npm run lint
npm run typecheck
npm run test:e2e
npm run test:e2e:ui
```

Playwright starts isolated backend and frontend servers by default.
Local runs use 4 workers unless `MNEMONAS_E2E_WORKERS` is set to a positive integer; CI uses 1 worker.
The default per-test Playwright timeout is 60 seconds, and the default assertion timeout is 10 seconds; override them with `MNEMONAS_E2E_TEST_TIMEOUT_MS` and `MNEMONAS_E2E_EXPECT_TIMEOUT_MS`.

The isolated backend uses a 2-hour access-token lifetime and a 168-hour refresh-token lifetime.
This prevents long parallel E2E runs from entering concurrent refresh-token rotation after a shared storageState expires.

To reuse existing services, set:

- `MNEMONAS_E2E_REUSE_EXISTING=1`;
- `MNEMONAS_E2E_BACKEND_URL`;
- `MNEMONAS_E2E_FRONTEND_URL`;
- `E2E_PASSWORD` or `E2E_PASSWORD_FILE`.

The default configuration writes the initial password file to `~/.mnemonas/.mnemonas/initial-password.txt`. If `auth.users_file` is stored at the `storage.root` top level, the initial password file is usually `~/.mnemonas/initial-password.txt`. Without `E2E_PASSWORD_FILE`, Playwright tries those two paths in that order. When `E2E_PASSWORD_FILE` is set explicitly, that file is authoritative; missing or empty files do not fall back to the defaults.

### WebDAV Smoke Test

```bash
# Run an independent curl protocol smoke against a running service; the script creates and removes a temporary collection.
WEBDAV_URL=http://localhost:8080/dav \
MNEMONAS_WEBDAV_USERNAME="<mnemonas-or-webdav-username>" \
MNEMONAS_WEBDAV_PASSWORD="<mnemonas-or-webdav-password>" \
./scripts/webdav-client-smoke.sh

# For a read-only routing check, a direct PROPFIND is enough.
curl -u "<mnemonas-or-webdav-username>:<mnemonas-or-webdav-password>" \
  -X PROPFIND http://localhost:8080/dav/ -H "Depth: 1"

curl http://localhost:8080/health
curl http://localhost:9091/health
curl http://localhost:9091/stats
```

`scripts/webdav-client-smoke.sh` covers `OPTIONS`, `MKCOL`, `PUT`, `PROPFIND`, `GET`, `HEAD`, `COPY`, `MOVE`, `DELETE`, content validation after COPY/MOVE, and read/write checks for URL-encoded space paths. `WEBDAV_URL` must be an HTTP(S) WebDAV root URL without whitespace, query strings, fragments, embedded credentials, backslashes, encoded slashes, encoded backslashes, or `.`/`..` path segments; pass credentials through environment variables. For `webdav.auth_type = "basic"`, use `./scripts/dev.sh --creds` to find credential locations. For `webdav.auth_type = "users"`, use a MnemoNAS username and password. Each curl request uses `CURL_CONNECT_TIMEOUT=10` and `CURL_MAX_TIME=30` by default; increase these environment variables on high-latency links.
The script passes authentication through a temporary curl config file so plaintext passwords are not printed in command arguments.

`9091` should remain local/private.

### Backup Restore-Drill Smoke Test

```bash
# Run a maintenance API smoke against a running service; the script does not create or delete backup jobs.
MNEMONAS_API_URL=http://localhost:8080/api/v1 \
MNEMONAS_BACKUP_JOB_ID=external-disk \
MNEMONAS_COOKIE_FILE=cookies.txt \
./scripts/backup-restore-drill-smoke.sh
```

`scripts/backup-restore-drill-smoke.sh` reads the backup job list and single-job detail by explicit job ID, triggers an immediate backup, runs the retention check, performs a restore drill, and downloads the restore report. `MNEMONAS_API_URL` must be an HTTP(S) API root URL without whitespace, query strings, fragments, embedded credentials, backslashes, encoded slashes, encoded backslashes, empty path segments, or `.`/`..` path segments; `MNEMONAS_BACKUP_JOB_ID` must be a safe job ID. Pass authentication through `MNEMONAS_COOKIE_FILE` when required. Each curl request uses `CURL_CONNECT_TIMEOUT=10` and `CURL_MAX_TIME=600` by default; increase `CURL_MAX_TIME` for high-latency backup targets. Set `MNEMONAS_BACKUP_KEEP_ARTIFACT=1` to retain local drill artifacts for manual inspection.

### E2E

```bash
make e2e
./scripts/run-e2e-isolated.sh --quick
RUN_RCLONE_WEBDAV=1 ./scripts/run-e2e-isolated.sh --quick
```

The isolated runner avoids writing into a real user storage root.
`scripts/e2e-test.sh` can target an explicit running service, but it requires:

- `BASE_URL`;
- `STORAGE_ROOT`;
- `CONFIG_FILE`;
- `SECRETS_FILE`;
- `INITIAL_PASSWORD_FILE`.

`STORAGE_ROOT` must not contain control characters, `..`, or symlink path components.
`BASE_URL` must be an HTTP(S) URL with a host; it must not contain whitespace, control characters, embedded credentials, query strings, fragments, backslashes, encoded slashes or backslashes, encoded query or fragment markers, empty path segments, or `.`/`..` path segments. Trailing slashes are normalized after validation.
For WebDAV `auth_type = "basic"`, the script can read Basic Auth credentials from config or `secrets.json`.
For WebDAV `auth_type = "users"`, set `MNEMONAS_WEBDAV_USERNAME` and `MNEMONAS_WEBDAV_PASSWORD` explicitly.
Set `RUN_RCLONE_WEBDAV=1` to make the isolated runner and `scripts/e2e-test.sh` run an additional WebDAV client smoke when `rclone` is installed. The smoke covers upload, download, move/rename, list, and cleanup operations.

The isolated E2E runner and Playwright backend accept only loopback Web and dataplane addresses: `localhost`, `ip6-localhost`, `::1`, or dotted-quad numeric `127.0.0.0/8` addresses. This prevents test backends from unintentionally listening on public or untrusted LAN interfaces.

### Fault Injection

Fault injection kills and restarts `nasd` and can corrupt test objects. The default project entry point starts an isolated backend under `/tmp` and passes the explicit target information to the destructive runner:

```bash
make fault-injection
./scripts/run-fault-injection-isolated.sh
```

The low-level runner remains available when an already running isolated target must be tested:

```bash
MNEMONAS_LIVE_FAULTS=1 \
BASE_URL=http://127.0.0.1:18080 \
STORAGE_ROOT=/tmp/mnemonas-fault-target \
NASD_BIN="$PWD/bin/nasd" \
FAULT_INJECTION_ASSUME_YES=1 \
RUN_CORRUPTION_TESTS=0 \
./scripts/fault-injection-test.sh
```

Safety checks are covered by `scripts/test-fault-injection-safety.sh` and `make scripts-check`.
The isolated runner accepts only `/tmp` or checkout-local roots and loopback Web and dataplane addresses.

The low-level runner requires explicit `BASE_URL`, `STORAGE_ROOT`, and `NASD_BIN`.
`BASE_URL` follows the same HTTP(S) URL safety rules as the E2E runner.
When WebDAV uses `auth_type = "users"`, it also requires explicit `MNEMONAS_WEBDAV_USERNAME` and `MNEMONAS_WEBDAV_PASSWORD`.

Real storage paths require `ALLOW_REAL_STORAGE=1`.
They must still be absolute.
They must not contain control characters, `..`, or symlink path components.
They must not point at protected system directories such as `/`, `/tmp`, or `/var`.

`OBJECTS_DIR`, `INDEX_DB`, and optional `NASD_PID_FILE` paths that may be read or modified by destructive checks must be under `STORAGE_ROOT`.

### Benchmarks

```bash
make bench
./scripts/run-benchmark-isolated.sh
```

Manual benchmark against an explicit service:

```bash
MNEMONAS_STORAGE_ROOT=/tmp/mnemonas-bench-target \
./scripts/benchmark.sh http://127.0.0.1:18080

# When explicit WebDAV credentials or protected metrics are required:
MNEMONAS_WEBDAV_USERNAME="<mnemonas-or-webdav-username>" \
MNEMONAS_WEBDAV_PASSWORD="<mnemonas-or-webdav-password>" \
MNEMONAS_ACCESS_TOKEN="<access-token>" \
MNEMONAS_STORAGE_ROOT=/tmp/mnemonas-bench-target \
./scripts/benchmark.sh http://127.0.0.1:18080
```

Manual benchmark targets create and remove `storage.root/files/benchmark-test`.
The benchmark target URL follows the same HTTP(S) URL safety rules as the E2E runner.
Real storage paths require `ALLOW_REAL_STORAGE=1`.
They must still be absolute, must not contain control characters, `..`, or symlink path components, and must not point at protected system directories.

For WebDAV `auth_type = "basic"`, the manual benchmark reads Basic Auth credentials from `config.toml` or `secrets.json` when environment credentials are not provided.
For WebDAV `auth_type = "users"`, set `MNEMONAS_WEBDAV_USERNAME` and `MNEMONAS_WEBDAV_PASSWORD` explicitly.
`[webdav].username/password` are not treated as MnemoNAS user credentials.

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
