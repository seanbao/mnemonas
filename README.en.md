# MnemoNAS

English | [简体中文](README.md)

[![CI](https://github.com/seanbao/mnemonas/actions/workflows/ci.yml/badge.svg)](https://github.com/seanbao/mnemonas/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/seanbao/mnemonas)](https://goreportcard.com/report/github.com/seanbao/mnemonas)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

> Private files. Local control. A self-hosted private cloud storage system.

MnemoNAS is an open-source self-hosted NAS system with a Web UI, WebDAV access, file versions, trash, scrub, and diagnostic bundles for daily file management. Data stays in the configured storage root, and moving that full root is enough to migrate the service.

The name comes from Mnemosyne, the Greek goddess of memory and mother of the nine Muses.

## Features

### Core Capabilities

- **Data ownership**: data stays on the configured disks; moving the full storage root is enough to migrate the service.
- **Usable Web UI**: desktop and mobile views are designed for clear daily use instead of dense admin-only panels.
- **Fast deployment**: Docker Compose and Linux/systemd deployment paths are provided.
- **Maintenance and diagnostics**: health checks, scrub, GC, and diagnostic bundles help discover and investigate data issues.
- **Web and WebDAV**: browser-based management and common WebDAV clients are both supported.

### Feature Matrix

| Area | Description |
| --- | --- |
| File management | List/grid views, drag-and-drop upload, batch actions, breadcrumbs, thumbnails |
| Version history | Policy-based file versions, version comparison, restore to a previous version |
| Trash | Soft delete, time-based browsing, restore, scheduled cleanup |
| Album mode | Image waterfall layout, thumbnails, immersive browsing |
| Search | Filename search with quick navigation |
| User management | Multiple users, roles, password policy, login history |
| Share links | Public/private links, password protection, expiration, access statistics |
| Activity log | Key operation history, statistics overview, high-risk summary with concentrated-window review scoped to the high-risk group, time-range, path, review-group, type, and user filters, administrator-only clear action, and home/small-team activity review |
| Settings | Server, storage, retention, and WebDAV configuration |
| Maintenance | Scrub, GC, object browsing, diagnostic bundle, system metrics |
| WebDAV | Core RFC 4918 read/write methods with Basic Auth and a maintained compatibility matrix |

## Architecture

```text
+---------------------------------------------------------+
|                      Web UI (React)                      |
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
- **CDC capability**: the Rust data plane exposes FastCDC file APIs; the current Go version-history path stores whole-object CAS snapshots, so edited versions only share storage when the full file content is identical.
- **Clear boundary**: reading files directly from `files/` is safe; writing around Web UI/WebDAV/API will not create versions or trash records.
- **Filesystem-neutral**: ext4, XFS, Btrfs, and ZFS are supported; ZFS mirror is recommended for stronger storage hygiene.
- **Migratable**: moving the full storage root keeps current files, history, trash, and metadata together.

See [Storage Internals and Best Practices](docs/storage-internals.en.md).

## Quick Start

### Linux / systemd

For a long-running Linux server, download a Linux release archive from [Releases](https://github.com/seanbao/mnemonas/releases) and install it as systemd services:

```bash
tar -xzf mnemonas-<version>-linux-amd64.tar.gz
cd mnemonas-<version>-linux-amd64

sudo ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

The default install path is `/usr/local/bin`, config is written to `/etc/mnemonas/config.toml`, data goes to `/srv/mnemonas`, and the Web UI listens on `http://<server-ip>:8080`. The first login password is stored at `/srv/mnemonas/.mnemonas/initial-password.txt`. For public-domain access, follow the [Public server quickstart](docs/public-server-quickstart.en.md) to restrict the backend port and configure an HTTPS reverse proxy.

See [Linux/systemd deployment](docs/linux-systemd-deployment.en.md).

### Docker Compose

Docker Engine and Compose v2 are required. Local source builds also require the Buildx plugin. Verify `docker compose version` first, and verify `docker buildx version` when building from source. On Ubuntu 24.04 systems where `docker` is available but `docker compose` is missing, the Ubuntu packages are usually `docker-compose-v2` and `docker-buildx`; Docker's official apt repository usually uses `docker-compose-plugin` and `docker-buildx-plugin`.

```bash
git clone https://github.com/seanbao/mnemonas.git
cd mnemonas

./scripts/docker-quickstart.sh --start

# Default Web UI:
# http://localhost:8080
```

The bundled `docker-compose.yml` builds `mnemonas:local` from source by default. The host does not need Go, Rust, or Node.js, but it must be able to pull Docker base images. The quickstart script creates or updates `.env`, writes the current host UID/GID, creates `MNEMONAS_DATA_DIR`, runs Docker preflight checks, and selects the start mode from `MNEMONAS_IMAGE`: local images use a source build, while release image tags use `docker compose up -d --pull missing --no-build`. Binary archives from GitHub Releases include `docker-compose.yml` and `.env.example`, and the packaged template presets `MNEMONAS_IMAGE` to the GHCR image for the same release tag.

If port 8080 is already used:

```bash
./scripts/docker-quickstart.sh --port 8888 --start
```

On first startup, MnemoNAS creates persistent config in the data directory. The Web login initial password is stored at `<MNEMONAS_DATA_DIR>/.mnemonas/initial-password.txt`. After the first administrator login, the dashboard shows a first-deployment checklist and requires explicit confirmation of initial credential handling, administrator redundancy, backup planning, and public-entry safety before the prompt can be closed. Release image usage is documented in the [Docker deployment guide](docs/docker-deployment.en.md).

### Manual Binary Run

This is useful for development and debugging. For long-running deployments, prefer systemd.

```bash
tar -xzf mnemonas-<version>-linux-amd64.tar.gz
cd mnemonas-<version>-linux-amd64

mkdir -p ~/.mnemonas
chmod 750 ~/.mnemonas
cp mnemonas.example.toml ~/.mnemonas/config.toml

./dataplane &
./nasd
```

### WebDAV Clients

MnemoNAS exposes WebDAV for common desktop, mobile, and CLI clients:

| Platform | Recommended Client | URL |
| --- | --- | --- |
| macOS | Finder | `http://localhost:8080/dav` |
| Windows | File Explorer | `http://localhost:8080/dav` |
| Linux | GNOME Files / davfs2 | `http://localhost:8080/dav` |
| iOS | Files / Documents | `http://<server-ip>:8080/dav` |
| Android | Solid Explorer | `http://<server-ip>:8080/dav` |
| CLI | rclone | `webdav:` remote |

WebDAV Basic Auth is enabled by default. Use the current WebDAV username and password from configuration or the admin settings API.

See [Mounting Guide](docs/mounting-guide.en.md).

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
├── proto/              # gRPC protocol definitions
├── docs/               # Documentation
└── docker-compose.yml
```

## Development

### Requirements

- Go 1.25.10+
- Rust 1.92+
- Node.js `^20.19.0` or `>=22.12.0` (Node 22 from `.nvmrc` is recommended)
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
make build
make dev
make test
make test-torture
make coverage
make e2e
make bench
make lint
make fmt
make deps
make clean
make help
```

`make lint` and `make check` require `golangci-lint`. If it is not in `PATH`, specify it explicitly:

```bash
GOLANGCI_LINT=/path/to/golangci-lint make lint
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
| [Linux/systemd Deployment](docs/linux-systemd-deployment.en.md) | systemd deployment for Linux servers |
| [Public Server Quickstart](docs/public-server-quickstart.en.md) | Recommended public-domain HTTPS entry path |
| [Docker Deployment](docs/docker-deployment.en.md) | Docker deployment guide |
| [Mounting Guide](docs/mounting-guide.en.md) | WebDAV client setup |
| [WebDAV Compatibility](docs/webdav-compatibility.en.md) | Client compatibility and protocol coverage |
| [Reverse Proxy Setup](docs/reverse-proxy-setup.en.md) | HTTPS and public entry setup |
| [Storage Internals](docs/storage-internals.en.md) | CAS, filesystem choices, and tuning |
| [Backup Guide](docs/backup-guide.en.md) | Backup and restore strategy |
| [FAQ](docs/faq.en.md) | Frequently asked questions |
| [Architecture](docs/architecture.en.md) | System design and technology choices |
| [Roadmap](docs/roadmap.en.md) | Priorities from private file cloud to home and small-team NAS |
| [Security Guide](docs/security.en.md) | Auth and network security |
| [Support](SUPPORT.en.md) | Support channels and support boundary |

## Script Tools

| Script | Description |
| --- | --- |
| [scripts/dev.sh](scripts/dev.sh) | Development environment launcher |
| [scripts/install-systemd.sh](scripts/install-systemd.sh) | systemd installer for release archives |
| [scripts/uninstall-systemd.sh](scripts/uninstall-systemd.sh) | systemd uninstaller |
| [scripts/mnemonas-doctor.sh](scripts/mnemonas-doctor.sh) | Deployment diagnostics |
| [scripts/mnemonas-docker-preflight.sh](scripts/mnemonas-docker-preflight.sh) | Docker Compose preflight checks |
| [scripts/run-e2e-isolated.sh](scripts/run-e2e-isolated.sh) | Isolated E2E runner used by `make e2e` |
| [scripts/e2e-test.sh](scripts/e2e-test.sh) | E2E checks against an explicit running service |
| [scripts/torture-test.sh](scripts/torture-test.sh) | Non-destructive deep test matrix |
| [scripts/run-benchmark-isolated.sh](scripts/run-benchmark-isolated.sh) | Isolated benchmark runner used by `make bench` |
| [scripts/benchmark.sh](scripts/benchmark.sh) | Benchmark an explicit service and storage root |
| [scripts/fault-injection-test.sh](scripts/fault-injection-test.sh) | Destructive fault-injection test runner |
| [scripts/setup-reverse-proxy.sh](scripts/setup-reverse-proxy.sh) | Public HTTPS reverse proxy setup and MnemoNAS backend hardening |

## License

MIT License. See [LICENSE](LICENSE).

*MnemoNAS - self-hosted file management and version history.*
