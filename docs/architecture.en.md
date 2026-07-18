# MnemoNAS Architecture

English | [简体中文](architecture.md)

This document describes the MnemoNAS system architecture, major design decisions, and implementation boundaries.

## Design Position

MnemoNAS is a self-hosted private cloud storage system for daily file management. It keeps the current file tree readable, adds version history and trash on top, and provides access through the Web UI, REST API, and WebDAV.

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
| Web UI / Flutter (Android-first) / WebDAV / API clients |
| Finder / Explorer / rclone / other mobile clients       |
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
- Share links with optional passwords, expiration, and logical download limits.
- Favorites and activity records scoped by per-user root directory.

SQLite is used for transactional metadata where ACID semantics matter. Some feature stores use JSON files when the data shape is small and local.

## Security Design

Security boundaries:

- Web UI/API authentication is JWT-backed and enabled by default; browser sessions store access and refresh tokens in same-origin `HttpOnly` cookies.
- The Flutter client uses `Authorization: Bearer` for REST API access and writes the access token and single-use rotating refresh token as one session generation in platform secure storage; it does not depend on browser cookies.
- User roles are `admin`, `user`, and `guest`.
- Non-admin users are scoped to their configured `home_dir`, with optional `storage.directory_access_rules` grants for shared directories.
- Directory access rules use the same most-specific path decision for files, search, shares, favorites, trash, activity logs, and WebDAV users mode.
- WebDAV can authenticate MnemoNAS users and apply role, group, `home_dir`, directory access-rule, home-scoped user-quota, and directory-quota boundaries; the legacy `basic` mode remains a separate global service credential.
- Share-link password validation uses short-lived HttpOnly cookies; downloads use signed, target-bound tickets with paired cookies.
- Download and preview flows use short-lived download-session cookies instead of long-lived tokens in URLs.

Deployment boundaries:

- Keep dataplane ports private.
- Use HTTPS through Caddy, Nginx, Traefik, or another trusted reverse proxy for public access.
- Set `server.trusted_proxy_hops` only when MnemoNAS is behind trusted proxies.
- Do not disable authentication outside a loopback-only development environment.

## Web Frontend Architecture

The Web UI lives under `web/` and uses React, TypeScript, Vite, HeroUI, Tailwind CSS, Zustand, and TanStack Query.

The UI is organized around repeated file-management workflows:

- File browser with list/grid views.
- Upload, download, rename, move, copy, delete, and batch operations.
- Version history and restore.
- Trash browsing and restore.
- Albums and thumbnails.
- Shares, favorites, activity, settings, and maintenance views.

The frontend talks to `/api/v1/*` and uses the same origin as `nasd` in production. In production, `nasd` serves the built static Web UI, keeps API, WebDAV, health, and direct share API routes ahead of the SPA fallback, and sends `Cache-Control: no-cache` for `index.html` so browser upgrades revalidate the application entry. During development, Vite serves the frontend on `5173` and proxies API calls to `8080`.

## Flutter Client Architecture

The Flutter client lives under `client/` and retains Android, Linux, and Windows runners. Android is the first usable-platform target. The Linux and Windows runners currently preserve the shared project boundary; builds and runtime validation on their native hosts have not been completed.

The client accesses the `nasd` REST API directly:

- Authenticated requests use a Bearer access token. The access token, refresh token, server address, and session timing data are stored as one record through `flutter_secure_storage`, preventing readers from observing tokens from different rotation generations.
- A server refresh token can succeed only once. The client coalesces concurrent refresh attempts only for `401 TOKEN_EXPIRED`, saves the new token pair returned by the server, and then retries the original request. Streaming uploads check session validity before sending and disable request-body replay.
- Public HTTP endpoints are rejected before connection. Loopback and local-network HTTP endpoints remain available for development validation, with an unencrypted-transport warning in the interface. The HTTP client does not follow redirects automatically.
- Android upload selection uses the Storage Access Framework `ACTION_OPEN_DOCUMENT` flow. The native layer returns only the `content` URI, display name, MIME type, and optional size to Dart; it does not return complete file bytes or retain a persisted read grant. A known size above 10 GiB is rejected before local copying begins. When metadata omits the size, the native copy loop still enforces the same 10 GiB hard limit and stops writing and removes the partial file when the limit is reached. Allowed sources are copied one at a time into an app-private import directory with a fixed-size buffer and `fsync`; the native layer reports preparation progress and accepts cancellation, avoiding complete-file or multi-selection Java heap residency. The Dart coordinator then copies the content into a stable task-owned private payload, computes SHA-256 in the same pass, and releases the temporary import after the ledger is durable. Desktop platforms create the same task-owned payload from a regular file path.
- Foreground upload tasks use the same app-private generation ledger as downloads. Records include the server address, user, target path, private-payload SHA-256, upload-session ID, creation-attempt state, server-authoritative offset, expiry, and phase, but never authentication tokens. The client durably records the attempt before the first creation request. If that response is lost before a session ID is obtained, the client only looks up an existing session by client request ID and does not repeat creation. A server session accepts sequential chunks of at most 8 MiB, validates each chunk with SHA-256, and computes BLAKE3 when the complete payload reaches `ready`. Before each run, the client validates the private-payload size and SHA-256. After another lost response or client restart, it first queries the session and continues from the server offset. If a session is missing or expired during lookup, chunk append, or commit, the task becomes an unconfirmed result instead of creating a new target snapshot implicitly. The server reconciles an unconfirmed commit through the durable publication window, target identity, size, and BLAKE3, and synchronously recovers interrupted states before exposing routes. While the storage recovery gate remains active, it preserves `committing` and the staged payload instead of confirming publication from the visible target alone. For a successful upload, the client deletes the private payload only after observing `committed`; startup also retries payload cleanup for confirmed completed or cancelled tasks.
- Foreground download tasks are stored in an app-private generation ledger. Records include the server address, user, remote path, destination, download identity, durable offset, and phase, but never an access or refresh token. Downloads continue through a stable partial file with `Range` and `X-MnemoNAS-If-Download-Identity`; bytes are appended only when the response also has `206`, the exact `Content-Range`, the expected total size, and the same identity. After client restart, the ledger first reconciles against the partial file's actual length. Incomplete tasks become paused, complete Android private payloads await a newly selected destination, and a possibly published but unconfirmed desktop result becomes `resultUnconfirmed`.
- Android download export completes an app-private payload before opening the Storage Access Framework `ACTION_CREATE_DOCUMENT` flow, preventing network failure from leaving an empty document. The selected `content` URI uses only the current foreground flow's temporary write grant. The platform channel copies with truncation semantics and marks the task complete only after the destination write succeeds. Export failure, cancellation, or process interruption retains the private payload in an awaiting-destination task; after client restart, the stale URI is cleared and another location must be selected.
- The transfer center groups tasks by runtime state and treats pause, retry, destination selection, cancel-and-delete, and history clearing as distinct actions. A local payload or partial file for an incomplete task is removed only after explicit confirmation, and an unconfirmed-result record also directs the operator to check the target location first.

This section describes the current source architecture only. Both durable upload and download ledgers are driven by the foreground Dart coordinator. An Android native background executor, notification actions, and cross-process task leases remain incomplete. The target background boundary uses user-initiated data transfer (UIDT) jobs on Android 14 and later, a WorkManager long-running fallback on Android 7 through 13, and one application-scoped FlutterEngine for Dart coordination. Before that boundary can be connected, the task ledger must become a transactional store with revision/CAS, durable task commands, and fencing tokens. A SAF document provider may retain an empty or partial document after native-copy failure; the client does not automatically delete an external document whose ownership cannot be confirmed. Desktop destination publication also lacks a cross-process no-replace primitive, durable publication journal, and directory synchronization. The Flutter client remains under development and no usable version has been published. Physical Android-device acceptance, upgrade validation, and independent release signing are incomplete, and Linux and Windows do not yet have validated distributable builds.

## Related Documents

- [Storage internals](storage-internals.en.md)
- [Configuration reference](configuration.en.md)
- [Security hardening](security.en.md)
- [API reference](api-reference.en.md)
- [Development guide](development.en.md)
- [Flutter client](../client/README.en.md)
