# Changelog

English | [简体中文](CHANGELOG.md)

All notable changes are recorded in this file.

This project follows [Semantic Versioning 2.0.0](https://semver.org/).

## [Unreleased]

### Added

#### Web UI
- Dashboard with storage usage, file/version counts, recent activity, quick actions, and data-plane health.
- File manager with breadcrumbs, list/grid views, context actions, drag-and-drop upload, batch operations, upload queue, and thumbnails.
- Album mode with image waterfall layout, generated thumbnails, and focused image browsing.
- Version history with version metadata, size comparison, and restore.
- Trash with time-based listing, restore, batch restore, and empty-trash flow.
- Filename search with highlighted results and quick navigation.
- Activity log with filters, details, and statistics.
- User management with create/edit/delete, password reset, and enable/disable flows.
- Share management with link creation, password protection, expiration, access statistics, and public share access.
- Settings for server, storage, retention, WebDAV, CDC parameters, and data-plane connection status.
- Health and maintenance views for uptime, storage health, scrub, GC, object browsing, and diagnostic bundle export.

#### Backend API
- Authentication APIs for JWT login, logout, refresh, password changes, and current-user lookup.
- User management APIs.
- Share-link APIs including public access and password checks.
- Activity log APIs.
- Runtime settings APIs.

#### Project Tooling
- GitHub Actions CI/CD for Go, Rust, frontend checks, Docker builds, and release packaging.
- Release workflow for multi-platform binaries and container images.
- Linux/systemd install and uninstall scripts.
- `mnemonas-doctor` deployment diagnostics.
- Docker Compose preflight checks for Compose v2, Buildx, ports, permissions, disk space, and existing config.
- Container healthcheck binary so runtime images do not depend on `curl`.
- `tools/proto-gen` Rust protobuf generator so normal dataplane and Docker builds do not require system `protoc`.
- Script simulation tests and CI script checks.
- Toolchain hints through `.go-version`, `.nvmrc`, Go `toolchain`, and Rust `rust-version`.
- `.gitattributes`, security policy, support policy, pre-commit config, golangci-lint config, and tightened `.gitignore`.

#### Documentation
- Linux/systemd deployment guide.
- Docker deployment guide covering Compose v2, non-root UID/GID, configurable HTTP port, weak-network build strategies, and dataplane port boundaries.
- Backup guide, API reference, storage internals, WebDAV compatibility, mounting guide, reverse proxy setup, security guide, and FAQ.
- Bilingual README, documentation index, main topic docs, support policy, and security policy.

### Changed
- Release archives include a top-level directory, Web UI assets, install/uninstall scripts, diagnostic scripts, and docs.
- The default `docker-compose.yml` builds `mnemonas:local` from source; public release images can be selected with explicit version tags after they are available.
- Docker Compose host HTTP port is configured through `MNEMONAS_HTTP_PORT`.
- CI pins protobuf generator versions and `protoc 3.20.1`, then verifies generated files do not drift after `make proto`.
- Rust checks cover dataplane all-targets and `tools/proto-gen`.
- `make go-packages` centralizes Go package discovery for CI, docs examples, and security scans.
- `make workflows-check` runs actionlint against GitHub Actions workflows.
- README, development docs, and testing docs use the Node.js engine range from `web/package.json`.
- CI and release workflows use narrower permissions, concurrency controls, and job timeouts.
- Security docs distinguish the Web UI initial admin password from generated WebDAV Basic Auth credentials.
- Security docs and doctor checks warn that dataplane ports `9090/9091` should not be exposed to untrusted networks.
- Backup docs describe consistency windows and snapshot recommendations for live data.

### Fixed
- Prevented systemd installation and static-file discovery from treating Vite source directories as built Web UI output.
- Fixed broad `.gitignore` / `.dockerignore` rules for `nasd`.
- Removed runtime `apt-get` dependency from Docker health checks.
- Removed the Docker/Rust build dependency on system `protoc`.
- Removed tracked local build/runtime artifacts.
- Fixed frontend syntax, hook dependency, lint, and unused-symbol issues.

---

## [0.1.0] - Unreleased

First public release target.

### Added

#### Core
- Content-addressed storage with BLAKE3 hashes.
- FastCDC chunking for version storage.
- Policy-based version history and restore.
- Soft delete with asynchronous cleanup.

#### WebDAV
- RFC 4918 core read/write methods: `PROPFIND`, `GET`, `PUT`, `DELETE`, `MKCOL`, `COPY`, and `MOVE`.
- Virtual `LOCK` / `UNLOCK` behavior.
- Basic Auth.
- Compatibility matrix for common clients, with real-client regression to be expanded around releases.

#### Operations
- Health endpoint.
- Scrub data integrity checks.
- GC.
- Diagnostic bundle export.

#### Deployment
- Docker / Docker Compose.
- Linux and macOS binary archives.
- TOML configuration file.

### Known Limitations
- `LOCK` / `UNLOCK` are virtual; clients editing the same file concurrently should account for that.
- Windows WebClient requires registry changes for HTTP connections; HTTPS is preferred.
- Users, roles, and per-user root boundaries are supported, but fine-grained per-file ACLs are not.
- Direct public exposure without HTTPS reverse proxy or VPN is not recommended.

### Compatibility
- Go: 1.25.9+
- Rust: 1.92+
- Node.js: `^20.19.0` or `>=22.12.0`
- Docker: 20.10+ with Compose v2 plugin
- Platforms: Linux x86_64/ARM64 for long-running deployments; macOS Intel/Apple Silicon for development, local runs, and manual binaries

---

## Release Checklist

- [ ] `make quick-check`
- [ ] `make scripts-check`
- [ ] `make test`
- [ ] `make security-check`
- [ ] Update `CHANGELOG.md` and `CHANGELOG.en.md`
- [ ] Update README version references when present
- [ ] Create and push a Git tag
- [ ] Verify GitHub Release artifacts, checksums, release notes, and container image tags

---

## Versioning

- MAJOR (`X.0.0`): incompatible API changes
- MINOR (`0.X.0`): backward-compatible feature additions
- PATCH (`0.0.X`): backward-compatible fixes

Pre-release examples:

- `0.1.0-alpha.1`: alpha, incomplete feature set
- `0.1.0-beta.1`: beta, feature-complete but may contain bugs
- `0.1.0-rc.1`: release candidate

[Unreleased]: https://github.com/seanbao/mnemonas/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/seanbao/mnemonas/releases/tag/v0.1.0
