# MnemoNAS Architecture

English | [简体中文](architecture.md)

This document describes the MnemoNAS system architecture, major design decisions, and implementation boundaries.

## Design Position

MnemoNAS is a self-hosted private cloud storage system for daily file management. It keeps the current file tree readable, adds version history and trash on top, and exposes both a Web UI and WebDAV.

Core principles:

- Data ownership: data lives on the user's own disks, and moving the full storage root is enough to migrate the service.
- Usable interface: desktop and mobile views should be clear and efficient for daily file work.
- Crash consistency: write paths recover to either the previous complete version or the new complete version.
- End-to-end verification: BLAKE3 hashes are used to detect missing or corrupted objects.
- Recoverability: version history and trash are first-class features.

Current non-goals:

- Mountable SMB/NFS runtimes. SMB currently has preview gateway config only; protocol compatibility and security boundaries are not complete.
- RAID or volume management inside MnemoNAS.
- Multi-node cluster consistency.

## High-Level Architecture

```text
+---------------------------------------------------------+
|                      Clients                            |
|  Web UI / Finder / Explorer / rclone / mobile clients   |
+-------------------------+-------------------------------+
                          |
+-------------------------v-------------------------------+
|                 Go control plane (nasd)                 |
|  WebDAV handler / REST API / static Web UI / auth       |
|  config / users / shares / activity / storage facade    |
+-------------------------+-------------------------------+
                          | gRPC
+-------------------------v-------------------------------+
|                Rust data plane (dataplane)              |
|  CAS object storage / CDC chunking / scrub / GC         |
+-------------------------+-------------------------------+
                          |
+-------------------------v-------------------------------+
|                      Filesystem                         |
|  storage.root/files        current user files           |
|  storage.root/.mnemonas    metadata, objects, trash     |
+---------------------------------------------------------+
```

The Go process owns user-facing protocols and policy. The Rust process owns high-throughput content-addressed storage work.

## Go Control Plane

The control plane is implemented by `cmd/nasd` and packages under `internal/`.

Main responsibilities:

- HTTP server and static Web UI serving.
- REST API for files, users, shares, settings, maintenance, and diagnostics.
- WebDAV RFC 4918 core methods.
- Authentication, JWT refresh tokens, per-user root directory boundaries, and admin-only endpoints.
- Storage orchestration: workspace files, version store, trash, activity log, and maintenance tasks.
- Configuration loading, validation, and runtime settings updates.

Important modules:

| Module | Responsibility |
| --- | --- |
| `internal/storage` | Unified file operations, versioning, trash, and metadata orchestration |
| `internal/workspace` | Native file operations under `storage.root/files` |
| `internal/versionstore` | SQLite-backed version metadata and object-store abstraction |
| `internal/webdav` | WebDAV request handling and client compatibility behavior |
| `internal/api` | REST handlers and response contracts |
| `internal/config` | TOML config loading and validation |
| `internal/auth` | Users, groups, roles, passwords, JWTs, login limits, and download sessions |

Current files are written to the native workspace first. When a file is eligible for versioning, historical content is committed to the CAS-backed version store.

## Rust Data Plane

The data plane lives under `dataplane/`.

Main responsibilities:

- Store and retrieve content-addressed objects.
- Chunk large content using FastCDC for dataplane file APIs.
- Hash content with BLAKE3.
- Optionally compress object payloads with zstd.
- Run scrub and object listing operations.
- Serve gRPC to `nasd` and an internal health/statistics HTTP endpoint.

The Go version-history path currently stores historical snapshots as whole BLAKE3 CAS objects. The dataplane `PutFile` / `GetFile` RPCs provide FastCDC chunking, but chunk-level version reference tracking is not yet wired into the Go control plane.

The data plane is intentionally not exposed to end users. In normal deployments, gRPC `9090` and HTTP `9091` stay on loopback or inside the container.

## Communication

`nasd` talks to `dataplane` through gRPC. This keeps the process boundary simple and avoids CGO/FFI complexity while retaining a strong typed interface.

NAS workloads are usually dominated by disk I/O and network I/O rather than Go-to-Rust serialization overhead. gRPC is therefore a pragmatic default for the current architecture.

## Storage Model

MnemoNAS uses a hybrid layout:

```text
storage.root/
├── files/                # current user files, stored as normal files
└── .mnemonas/
    ├── index.db          # SQLite metadata
    ├── objects/          # CAS objects for versions
    ├── trash/            # soft-deleted content
    ├── thumbnails/       # generated thumbnail cache
    ├── maintenance/      # scrub/GC state
    └── users.json        # user data when auth uses the default file
```

This gives users a readable current file tree while keeping version history content-addressed, whole-object deduplicated, and verifiable.

Directly reading files under `files/` is safe for users with OS-level access. Directly writing or deleting files there while MnemoNAS is running bypasses version history, trash, activity logging, and metadata reconciliation.

## Data Model

The main logical entities are:

- Current files and directories under `files/`.
- Version records keyed by path and content hash.
- CAS objects addressed by BLAKE3 hash.
- Trash records with original path, deletion time, and content reference.
- Users with role, groups, and `home_dir`.
- Share links with optional password, expiration, and access limits.
- Favorites and activity records scoped by per-user root directory.

SQLite is used for transactional metadata where ACID semantics matter. Some feature stores use JSON files when the data shape is small and local.

## Security Design

Security boundaries:

- Web UI/API authentication is JWT-backed and enabled by default; browser sessions store access and refresh tokens in same-origin `HttpOnly` cookies.
- User roles are `admin`, `user`, and `guest`.
- Non-admin users are scoped to their configured `home_dir`, with optional `storage.directory_access_rules` grants for shared directories.
- Directory access rules use the same most-specific path decision for files, search, shares, favorites, trash, activity logs, and WebDAV users mode.
- WebDAV can authenticate MnemoNAS users and apply role, group, `home_dir`, directory access-rule, home-scoped user-quota, and directory-quota boundaries; the legacy `basic` mode remains a separate global service credential.
- Share-link password validation uses short-lived HttpOnly cookies.
- Download and preview flows use short-lived download-session cookies instead of long-lived tokens in URLs.

Deployment boundaries:

- Keep dataplane ports private.
- Use HTTPS through Caddy, Nginx, Traefik, or another trusted reverse proxy for public access.
- Set `server.trusted_proxy_hops` only when MnemoNAS is behind trusted proxies.
- Do not disable authentication outside a loopback-only development environment.

## Frontend Architecture

The Web UI lives under `web/` and uses React, TypeScript, Vite, HeroUI, Tailwind CSS, Zustand, and TanStack Query.

The UI is organized around repeated file-management workflows:

- File browser with list/grid views.
- Upload, download, rename, move, copy, delete, and batch operations.
- Version history and restore.
- Trash browsing and restore.
- Albums and thumbnails.
- Shares, favorites, activity, settings, and maintenance views.

The frontend talks to `/api/v1/*` and uses the same origin as `nasd` in production. During development, Vite serves the frontend on `5173` and proxies API calls to `8080`.

## Related Documents

- [Storage internals](storage-internals.en.md)
- [Configuration reference](configuration.en.md)
- [Security hardening](security.en.md)
- [API reference](api-reference.en.md)
- [Development guide](development.en.md)
