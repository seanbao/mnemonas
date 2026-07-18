# MnemoNAS

English | [简体中文](README.md)

[![CI](https://github.com/seanbao/mnemonas/actions/workflows/ci.yml/badge.svg)](https://github.com/seanbao/mnemonas/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/seanbao/mnemonas)](https://goreportcard.com/report/github.com/seanbao/mnemonas)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

> Private files and local control for self-hosted storage.

> [!WARNING]
> MnemoNAS is still under development and has not published any usable release. The current source tree is for development and validation only; it must not hold real data or be used for production deployment. Defects, usage problems, and feature suggestions may be submitted through [GitHub Issues](https://github.com/seanbao/mnemonas/issues).

MnemoNAS is an open-source self-hosted NAS system for daily file management.
It provides a Web UI, WebDAV access, file versions, trash, scrub, and diagnostic bundles.
Data stays in the configured storage root, and moving that full root is enough to migrate the service.

The name comes from Mnemosyne, the Greek goddess of memory and mother of the nine Muses.

## Capability Overview

### Core Capabilities

- **Data ownership**: data stays in the configured local storage root; capacity is determined by the underlying disks, and moving the full storage root is enough to migrate the service.
- **Web interface**: desktop and mobile views are designed for clear daily use instead of dense admin-only panels.
- **Deployment paths**: Docker Compose and Linux/systemd deployment methods are provided.
- **Maintenance and diagnostics**: health checks, scrub, GC, and diagnostic bundles help discover and investigate data issues.
- **Web and WebDAV coverage**: browser-based management and WebDAV protocol access cover the main access paths, with client compatibility tracked in the matrix.

### Web Management Feature Matrix

| Area | Description |
| --- | --- |
| File management | List/grid views, drag-and-drop upload, batch actions, breadcrumbs, thumbnails |
| Version history | Policy-based file versions, version comparison, restore to a previous version, pre-restore impact review, and restore-result activity details |
| Trash | Soft delete, time-based browsing, restore, scheduled cleanup |
| Album mode | Image waterfall layout, thumbnails, immersive browsing |
| Search | Filename search with quick navigation |
| User management | Multiple users, roles, password policy, login history |
| Share links | Public/private links, password protection, expiration, access statistics |
| Activity log | Operation history, statistics, high-risk review, filters, disposition records, export, and administrator-only clear action |
| Settings | Server, storage, retention, and WebDAV configuration |
| Backup and maintenance | Backup jobs, restore drills, scrub, GC, object listing, diagnostic bundle, system metrics |
| WebDAV | Core RFC 4918 read/write methods with MnemoNAS user authentication or Basic Auth and a maintained compatibility matrix |

Activity review includes:

- high-risk summaries;
- concentrated-window review;
- current-page and current-filter cross-page review;
- structured bulk-disposition summaries;
- follow-up review status.

Activity rows also link to related versions, trash entries, shares, and review records.

### Flutter Client (In Development)

[`client/`](client/README.en.md) contains the Flutter project for Android, Linux, and Windows, with Android as the first usable-platform target. The current source covers server connection, Bearer sessions and refresh-token rotation isolated by revision/CAS, file browsing, bounded filename search, recoverable upload and download, rename, move, copy, two-phase safe deletion, trash restore and exact permanent deletion, account management, and issue feedback.

No usable client version has been published. Full-text and photo indexing, search pagination, Android native background transfer, cross-process task leases, native desktop validation, physical Android-device acceptance, and release signing remain incomplete. The Linux and Windows runners currently preserve only the cross-platform project boundary.

## Architecture

```text
+---------------------------------------------------------+
|       Flutter client / Web UI / WebDAV clients          |
+---------------------------------------------------------+
|                   Go control plane (nasd)                |
|  +---------+  +---------+  +---------+  +---------+      |
|  | WebDAV  |  |  API    |  | Config  |  |  Auth   |      |
|  +---------+  +---------+  +---------+  +---------+      |
+---------------------------------------------------------+
|                         gRPC                            |
+---------------------------------------------------------+
|                 Rust data plane (dataplane)              |
|  +---------+  +---------+  +---------+  +---------+      |
|  |   CAS   |  |  CDC    |  |  Scrub  |  |   GC    |      |
|  +---------+  +---------+  +---------+  +---------+      |
+---------------------------------------------------------+
|                      Filesystem                          |
+---------------------------------------------------------+
```

### Storage Model

MnemoNAS uses a hybrid layout: current files are stored as native files under `files/`, while historical versions and deduplicated objects are stored in an internal CAS.

- **Readable current files**: the current version lives in a normal directory and can be migrated or backed up offline by users with OS-level access.
- **Content-addressed versions**: historical content is addressed by BLAKE3 hashes.
- **CDC capability**: the Rust data plane exposes FastCDC file APIs; the FastCDC API is a dataplane capability, and current version history still stores whole-object CAS snapshots, so edited versions only share storage when the full file content is identical.
- **Clear boundary**: reading files directly from `files/` is safe; writing around Web UI/WebDAV/API will not create versions or trash records.
- **Filesystem-neutral**: ext4, XFS, Btrfs, and ZFS are supported; ZFS mirror is recommended for stronger storage hygiene.
- **Migratable**: moving the full storage root keeps current files, history, trash, and metadata together.

See [Storage Internals and Operations Guidance](docs/storage-internals.en.md).

## Development Preview

The following steps are for local development and functional validation only. The repository does not currently provide a downloadable release archive or supported container image.

### Docker Compose Source Build

Docker Engine and Compose v2 are required.
Local source builds also require the Buildx plugin.
Verify `docker compose version` first, and verify `docker buildx version` when building from source.

On Ubuntu 24.04 systems where `docker` is available but `docker compose` is missing, the Ubuntu packages are usually `docker-compose-v2` and `docker-buildx`.
Docker's official apt repository usually uses `docker-compose-plugin` and `docker-buildx-plugin`.

```bash
git clone https://github.com/seanbao/mnemonas.git
cd mnemonas

./scripts/docker-quickstart.sh --start

# Default Web UI:
# http://localhost:8080
```

The bundled `docker-compose.yml` builds `mnemonas:local` from source by default.
The host does not need Go, Rust, or Node.js, but it must be able to pull Docker base images.

The quickstart script:

- creates or updates `.env`;
- writes the current host UID/GID;
- creates `MNEMONAS_DATA_DIR`;
- runs Docker preflight checks;
- selects the start mode from `MNEMONAS_IMAGE`.

Only local source-built development images are currently supported.
After `--start`, the script waits for the local `/health` endpoint, then prints Web UI, health check, initial-password read, WebDAV, Compose status, and log commands.
Use `--skip-health-check` only when the host cannot reach the Docker-published port locally.

If port 8080 is already used:

```bash
./scripts/docker-quickstart.sh --port 8888 --start
```

On first startup, MnemoNAS creates persistent config in the data directory.
By default, the Web login initial password is stored at `<MNEMONAS_DATA_DIR>/.mnemonas/initial-password.txt`.
If `auth.users_file` is customized, `initial-password.txt` is stored next to that users file.

After the first administrator login, the dashboard shows a first-deployment checklist.
The prompt closes only after explicit confirmation of initial credential handling, administrator redundancy, backup planning, and public-entry safety.
Source-build details and validation of future container release paths are documented in the [Docker deployment guide](docs/docker-deployment.en.md).

### WebDAV Client Validation

MnemoNAS exposes WebDAV for common desktop, mobile, and CLI clients. The table below is a connection-entry summary; compatibility status is tracked in the [WebDAV Compatibility](docs/webdav-compatibility.en.md) matrix. `rclone` has optional real-client E2E coverage, while Finder, Windows File Explorer, and mobile clients remain tracked by the matrix.

| Platform | Common Client | URL |
| --- | --- | --- |
| macOS | Finder | `http://localhost:8080/dav` |
| Windows | File Explorer | `http://localhost:8080/dav` |
| Linux | GNOME Files / davfs2 | `http://localhost:8080/dav` |
| iOS | Files / Documents | `http://<server-ip>:8080/dav` |
| Android | Solid Explorer | `http://<server-ip>:8080/dav` |
| CLI | rclone | `webdav:` remote |

For development validation, `auth_type = "users"` is preferred so clients use MnemoNAS usernames and passwords and follow the same `home_dir`, directory-access, and quota boundaries.
The root example config keeps `basic` as a compatibility baseline. User-boundary validation should switch to `users` unless legacy clients or dedicated service credentials require a global WebDAV username and password.

The running Web UI exposes the mount URL, Basic username, and readable generated password on the Settings -> WebDAV tab.
Custom Basic passwords are not echoed back and should come from the config file or password manager.
Generated Basic Auth passwords are also stored in `<storage.root>/secrets.json`.

Mounting steps are documented in the [Mounting Guide](docs/mounting-guide.en.md).

## Repository Layout

```text
mnemonas/
├── cmd/nasd/           # Go control plane entrypoint
├── internal/           # Go internal packages
│   ├── webdav/         # WebDAV implementation
│   ├── api/            # REST/gRPC API
│   ├── config/         # Config management
│   ├── caslayout/      # CAS layout
│   └── storage/        # Filesystem, versions, trash, CAS orchestration
├── dataplane/          # Rust data plane
├── web/                # React frontend
├── client/             # Flutter Android/Linux/Windows client
├── proto/              # gRPC protocol definitions
├── docs/               # Documentation
└── docker-compose.yml
```

## Development

### Requirements

- Go 1.25.12+
- Rust 1.92+
- Node.js `^20.19.0` or `>=22.12.0` (Node 22 from `.nvmrc` is recommended)
- Flutter 3.44.4, a complete JDK 17, and an Android SDK with NDK `28.2.13676358` for Android client builds
- Docker Engine + Compose v2
- protoc 3.20+ when regenerating protobuf or running `make proto` / `make build`

### Dev Script

```bash
source "$HOME/.nvm/nvm.sh"
nvm use

./scripts/dev.sh
./scripts/dev.sh --backend
./scripts/dev.sh --creds
./scripts/dev.sh --frontend
./scripts/dev.sh --status
./scripts/dev.sh --kill
```

The script builds and starts the Go control plane, Rust data plane, and frontend dev server, writes logs under `logs/`, checks service readiness, and enforces the Node.js version before starting frontend tooling.

### Make Targets

```bash
# Full build: protobuf, Web UI, Go control plane, and Rust data plane
make build

# Fast debug build for local development
make dev

# Change-aware validation; run this first before committing local changes
make verify-changed

# Flutter formatting, analysis, tests, Android policy, and debug APK gates
make client-check

# Pre-release readiness summary; run before tagging
make release-readiness

# Fast local checks
make quick-check

# Full project check: workflows, scripts, toolchains, docs, lint, and tests
make check

# All tests
make test

# Deep race/fuzz/property/browser torture matrix
make test-torture

# Coverage reports
make coverage

# Documentation, script, and workflow checks
make docs-check
make scripts-check
make workflows-check
make toolchains-check

# Dependency vulnerability checks
make security-check

# Isolated E2E acceptance tests
make e2e

# Isolated destructive fault-injection tests
make fault-injection

# Isolated performance benchmarks
make bench

# Docker image build and container smoke test
make docker-check

# Linting and formatting
make lint
make fmt

# Dependency installation, cleanup, and help
make deps
make clean
make help
```

`make lint` and `make check` require `golangci-lint`. If it is not in `PATH`, specify it explicitly:

```bash
GOLANGCI_LINT=/path/to/golangci-lint make lint
```

Go linting inherits the local Go toolchain environment by default. Override it only when automatic toolchain download is required:

```bash
GO_LINT_ENV="GOSUMDB=sum.golang.org GOTOOLCHAIN=auto" make lint
```

Use `SKIP_GOLANGCI_LINT=1` only for temporary local troubleshooting; do not skip Go static analysis before committing.

### Ports

| Service | Port | Description |
| --- | --- | --- |
| Go control plane (nasd) | 8080 | REST API + WebDAV |
| Rust data plane HTTP | 9091 | Health + stats |
| Rust data plane gRPC | 9090 | CAS storage service |
| Frontend dev server | 5173 | Vite dev server |

Docker and systemd deployments expose only `8080` by default. Data plane ports `9090/9091` are internal and should stay inside the container or on `127.0.0.1`. Custom Web or dataplane backend ports should remain private as well.

## Documentation

| Document | Description |
| --- | --- |
| [Documentation Index](docs/README.en.md) | English entry point for project docs |
| [中文文档索引](docs/README.md) | Chinese documentation index |
| [Development Guide](docs/development.en.md) | Local development setup and debugging |
| [Linux/systemd Deployment](docs/linux-systemd-deployment.en.md) | Pre-release validation path for future systemd archives |
| [Public Server Quickstart](docs/public-server-quickstart.en.md) | Domain, HTTPS, reverse-proxy, and security validation before a future public release |
| [Docker Deployment](docs/docker-deployment.en.md) | Source builds and validation of future container release paths |
| [Mounting Guide](docs/mounting-guide.en.md) | WebDAV client setup |
| [WebDAV Compatibility](docs/webdav-compatibility.en.md) | Client compatibility and protocol coverage |
| [Reverse Proxy Setup](docs/reverse-proxy-setup.en.md) | HTTPS and public entry setup |
| [Storage Internals and Operations Guidance](docs/storage-internals.en.md) | CAS, filesystem choices, and tuning |
| [Backup Guide](docs/backup-guide.en.md) | Backup and restore strategy |
| [FAQ](docs/faq.en.md) | Frequently asked questions |
| [Architecture](docs/architecture.en.md) | System design and technology choices |
| [Roadmap](docs/roadmap.en.md) | Priorities from private file cloud to home and small-team NAS |
| [Security Hardening Guide](docs/security.en.md) | Auth and network security |
| [Feedback](SUPPORT.en.md) | Issue reporting channels, required context, and handling boundaries |
| [Code of Conduct](CODE_OF_CONDUCT.md) | Conduct requirements and enforcement scope for feedback channels |

## Script Tools

| Script | Description |
| --- | --- |
| [scripts/dev.sh](scripts/dev.sh) | Development environment launcher |
| [scripts/install-systemd.sh](scripts/install-systemd.sh) | systemd installation validator for future release archives |
| [scripts/uninstall-systemd.sh](scripts/uninstall-systemd.sh) | systemd uninstaller |
| [scripts/mnemonas-doctor.sh](scripts/mnemonas-doctor.sh) | Deployment diagnostics |
| [scripts/docker-quickstart.sh](scripts/docker-quickstart.sh) | Docker Compose quickstart script |
| [scripts/mnemonas-docker-preflight.sh](scripts/mnemonas-docker-preflight.sh) | Docker Compose preflight checks |
| [scripts/docker-smoke.sh](scripts/docker-smoke.sh) | Loopback container smoke test for a built image |
| [scripts/run-e2e-isolated.sh](scripts/run-e2e-isolated.sh) | Isolated E2E runner used by `make e2e` |
| [scripts/e2e-test.sh](scripts/e2e-test.sh) | E2E checks against an explicit running service |
| [scripts/torture-test.sh](scripts/torture-test.sh) | Non-destructive deep test matrix |
| [scripts/run-benchmark-isolated.sh](scripts/run-benchmark-isolated.sh) | Isolated benchmark runner used by `make bench` |
| [scripts/benchmark.sh](scripts/benchmark.sh) | Benchmark an explicit service and storage root |
| [scripts/run-fault-injection-isolated.sh](scripts/run-fault-injection-isolated.sh) | Isolated destructive fault-injection runner used by `make fault-injection` |
| [scripts/fault-injection-test.sh](scripts/fault-injection-test.sh) | Low-level destructive fault-injection runner for explicit targets |
| [scripts/setup-reverse-proxy.sh](scripts/setup-reverse-proxy.sh) | Public HTTPS reverse proxy setup and MnemoNAS backend hardening |

## License

MIT License. See [LICENSE](LICENSE).

*MnemoNAS - self-hosted file management and version history.*
