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
- Activity log with filters, details, statistics, and disk-health system events.
- Storage page shows filesystem type, mount point, and backing device/dataset source.
- User management with create/edit/delete, home directory and quota editing, password reset, and enable/disable flows.
- Share management with link creation, password protection, expiration, access statistics, public share access, risk filtering, soon-expiry reminders, policy presets, and direct disable actions for high-risk links.
- Settings for server, storage, retention, WebDAV, CDC parameters, scheduled Scrub, and data-plane connection status.
- Public access wizard and security self-check entry point for HTTPS reverse proxy, trusted proxy hops, and share-domain configuration.
- Desktop and mobile E2E coverage for the public access wizard.
- Health and maintenance views for uptime, storage health, disk SMART/temperature/media-wear/missing-device status, scheduled Scrub status/retries, SMB preview runtime state, scrub, GC, object browsing, backup job health/schedules/retention/restore drills, and diagnostic bundle export.

#### Backend API
- Authentication APIs for JWT login, logout, refresh, password changes, and current-user lookup.
- User management APIs, including user-level quotas. Non-admin Web/API uploads, copies, and trash restores return `QUOTA_EXCEEDED` when they exceed the configured quota and can emit `quota_exceeded` Webhook/Telegram/SMTP alert events.
- `storage.directory_quotas` directory hard limits and storage-page directory quota usage summaries. Web/API uploads, copies, moves, trash restores, version restores, and WebDAV PUT/COPY/MOVE operations check matching directory quotas before writing.
- User groups and `storage.directory_access_rules` for shared-directory read/write grants by user, group, or role. Web/API, WebDAV users mode, search, shares, favorites, trash, and activity filtering use the same path authorization decision.
- Effective access checks, unsaved-rule previews, per-path user matrix views, and related-share impact checks in the Settings API and Web settings page so admins can inspect why users can read or write a path.
- WebDAV supports `auth_type = "users"` so clients can mount with MnemoNAS user accounts; non-admin mounts are rooted at the user's `home_dir`, guest accounts are read-only, and PUT/COPY writes honor user quotas.
- Share-link APIs including public access, password checks, default expiry/access-limit policy, and share risk markers.
- Activity log APIs, including scrub system events.
- Runtime settings APIs, including public-access security self-check, certificate renewal and failure-triage guidance, scheduled Scrub updates, and hot updates for Webhook/Telegram/SMTP alert notifications.
- Configured local backup jobs and command-backed restic/rclone remote targets with run-now execution, lightweight scheduling, automatic backup windows, local snapshot retention, automatic/manual retention checks, job health status, manifest-based local restore drills, non-destructive restore previews, batch restore preview/execution for up to 20 items with per-item restore and read-only verification results, restore preflight for target isolation/state/capacity/content/config handling, failed-preflight blocking, safe-directory local/restic/rclone restore, persisted post-restore verification reports, post-restore cutover and rollback checklists, restore summary export, scheduled restore-drill reminders, rate-limited stale/missing restore-drill alerts, restore-drill history, success-rate summary, failure categorization, restore result history, remote consistency checks, and Webhook/Telegram/SMTP events for backup failures, retention-check failures, and warnings.
- Disk health API and runtime settings for `smartctl --json` SMART checks, temperature thresholds, NVMe/ATA media-wear signals, missing-device detection, serial-drift detection, `disk_health` activity-log records, and Webhook/Telegram/SMTP events.
- `[maintenance.scrub]` supports background scheduled Scrub with bounded failure retries; the Settings API and Web settings page can hot-update its scheduler, and diagnostics report schedule settings, latest Scrub state, and retry counts. Manual and scheduled scrub completion writes activity entries; scrub failures, object anomalies, and incomplete result persistence send `scrub_run` events through Webhook/Telegram/SMTP notifications.
- Login rate limits send throttled `login_rate_limited` warning events through configured alert channels, containing only username and client address, never passwords or tokens.
- SMB preview diagnostics return sanitized runtime state and share counts, and `nasd --check-config` warns when `smb.enabled=true` because current builds do not start an SMB/Samba listener.

#### Project Tooling
- GitHub Actions CI/CD for Go, Rust, frontend checks, Docker builds, and release packaging.
- Release workflow for multi-platform binaries and container images.
- Linux/systemd install and uninstall scripts.
- `mnemonas-doctor` deployment diagnostics, including public HTTPS certificate checks, HTTP-to-HTTPS redirect checks, and manual cloud-firewall review guidance.
- `mnemonas-doctor --public-domain` detects broad UFW allow rules for backend control-plane and dataplane ports, and consistently expands `~` in storage and WebDAV user-file paths.
- `mnemonas-public-setup` public HTTPS reverse-proxy setup helper.
- Traefik and Cloudflare Tunnel public-access templates with script checks that prevent backend and dataplane port exposure.
- Docker Compose preflight checks for Compose v2, Buildx, ports, permissions, disk space, and existing config.
- Container healthcheck binary so runtime images do not depend on `curl`.
- `tools/proto-gen` Rust protobuf generator so normal dataplane and Docker builds do not require system `protoc`.
- Script simulation tests and CI script checks.
- Script simulation fixtures cover changed-file selection, WebDAV auth modes, public reverse-proxy exposure checks, benchmark paths, and Web Husky hooks.
- `scripts/webdav-client-smoke.sh` runs curl protocol smoke checks against a running service, covering basic WebDAV read/write, copy, move, and delete operations, with a dedicated safety test in `make scripts-check`.
- `scripts/release-readiness.sh` fails by default when non-release-documentation changes exist after the recorded full-validation target; draft summaries can opt in with `--allow-post-validation-changes`.
- WebDAV COPY/MOVE destination regression coverage for absolute path-reference destinations and rejection of bare relative destinations, including `dav/path`.
- `npm run typecheck` covers the frontend application, Playwright specs, and shared E2E helpers.
- Toolchain hints through `.go-version`, `.nvmrc`, Go `toolchain`, and Rust `rust-version`.
- `.gitattributes`, security policy, support policy, pre-commit config, golangci-lint config, and tightened `.gitignore`.

#### Documentation
- Linux/systemd deployment guide.
- Docker deployment guide covering Compose v2, non-root UID/GID, configurable HTTP port, weak-network build strategies, and dataplane port boundaries.
- Backup guide, including built-in local backup jobs and restore drills, API reference, storage internals, WebDAV compatibility, mounting guide, reverse proxy setup, security guide, and FAQ.
- Bilingual README, documentation index, main topic docs, support policy, and security policy.

### Changed
- Release archives include a top-level directory, Web UI assets, install/uninstall scripts, diagnostic scripts, docs, public-access deploy templates, and Docker Compose/env templates preset for the matching release image.
- The default `docker-compose.yml` builds `mnemonas:local` from source; public release images can be selected with explicit version tags after they are available.
- Docker Compose host HTTP port is configured through `MNEMONAS_HTTP_PORT`.
- CI pins protobuf generator versions and `protoc 3.20.1`, then verifies generated files do not drift after `make proto`.
- Rust checks cover dataplane all-targets and `tools/proto-gen`.
- `make go-packages` centralizes Go package discovery for CI, docs examples, and security scans.
- `make workflows-check` runs actionlint against GitHub Actions workflows.
- README, development docs, and testing docs use the Node.js engine range from `web/package.json`.
- CI and release workflows use narrower permissions, concurrency controls, and job timeouts.
- Release workflow verifies downloaded archives, checksums, and the required target set before creating the GitHub Release.
- Release artifact verifier rejects unsafe checksum paths, control-character paths, whitespace paths, symlinked archives, special archive entries, duplicate entries, backslash paths, and ambiguous path segments before running checksum validation.
- Security docs distinguish the Web UI initial admin password from generated WebDAV Basic Auth credentials.
- Security docs and doctor checks warn that dataplane ports `9090/9091` should not be exposed to untrusted networks.
- Added a public cloud firewall checklist covering common cloud security groups, VPC firewalls, IPv6, and port-forwarding mistakes.
- Backup docs describe consistency windows and snapshot recommendations for live data.
- Replaced the raw directory access rules textarea in Settings with a structured rule editor, per-path user matrix, related-share impact, and unsaved-rule preview entry point for user, group, and role grants.
- `make verify-changed` treats Web Husky hooks as script changes and runs frontend type checking for Web changes, including untracked E2E helper and config files.
- Root example config comments are standardized in English, and `make verify-changed` runs `nasd --check-config` when `mnemonas.example.toml` changes.
- `make verify-changed` runs Docker template script fixtures when `.env.example` or Compose templates change.
- `make verify-changed` runs the Docker build when `.dockerignore` changes so build-context rules do not drift silently.
- `make verify-changed` always runs `git diff --check` against the matching worktree, staged, or base range, and selects the relevant toolchain checks when `.go-version`, `.nvmrc`, or `.golangci.yml`/`.golangci.yaml` changes.
- `make verify-changed` runs the public-access template safety fixture when `deploy/public-access/` templates change.
- `make verify-changed` validates YAML syntax when `.github/dependabot.yml`, `.github/dependabot.yaml`, `codecov.yml`, or `codecov.yaml` changes.
- WebDAV setup scripts and development helpers report Basic, users, and no-auth modes explicitly so generated credentials and user-account mounts are not confused.

### Fixed
- Fixed `server.trusted_proxy_hops` updates through the settings API not immediately updating runtime client-IP and HTTPS forwarded-header interpretation.
- Fixed the Web Husky pre-commit hook so it resolves the repository root, runs from `web/`, and uses the frontend lint-staged configuration.
- Fixed frontend authentication setup so reused-server E2E runs can opt into auth-state skips, while isolated E2E runs fail instead of silently saving an empty auth state.
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

#### Performance Optimization
- PROPFIND response cache with a 30-second TTL.
- Request metrics collection and statistics.
- Streaming file transfers; practical file size limits are determined mainly by disk, client, and reverse-proxy constraints.

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
- Users, roles, groups, per-user root boundaries, and directory access rules are supported, but inherited ACL policies and per-file ACLs are not.
- Direct public exposure without HTTPS reverse proxy or VPN is not recommended.

### Compatibility
- Go: 1.25.11+
- Rust: 1.92+
- Node.js: `^20.19.0` or `>=22.12.0`
- Docker: 20.10+ with Compose v2 plugin
- Platforms: Linux x86_64/ARM64 for long-running deployments; macOS Intel/Apple Silicon for development, local runs, and manual binaries

---

## Release Checklist

- [ ] Record the baseline and keep the worktree clean: `git status --short --branch`
- [ ] Run full change-aware validation: `GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master`
- [ ] Run documentation checks: `make docs-check`
- [ ] Run script checks: `make scripts-check`
- [ ] Run dependency security checks: `make security-check NPM_AUDIT=1`
- [ ] Run Docker build and smoke checks: `make docker-check`
- [ ] Confirm `./scripts/plan-hardening-commits.sh --fail-on-manual` reports no unclassified paths
- [ ] Run release readiness summary: `./scripts/release-readiness.sh`
- [ ] Update `CHANGELOG.md`, `CHANGELOG.en.md`, README version references, and [release notes draft](docs/release-notes.en.md)
- [ ] Create and push a Git tag
- [ ] After publication, download the GitHub Release artifacts and run `./scripts/verify-release-artifacts.sh --version <tag> --repository seanbao/mnemonas --require-targets --check-image <artifact-dir>` to verify release artifacts, checksums, and container image tags
- [ ] After publication, verify release archive installation, Docker release image startup, and public documentation links

---

## Versioning

- MAJOR (`X.0.0`): incompatible API changes
- MINOR (`0.X.0`): backward-compatible feature additions
- PATCH (`0.0.X`): backward-compatible fixes

### Pre-release Versions

- `0.1.0-alpha.1`: alpha, incomplete feature set
- `0.1.0-beta.1`: beta, feature-complete but may contain bugs
- `0.1.0-rc.1`: release candidate

[Unreleased]: https://github.com/seanbao/mnemonas/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/seanbao/mnemonas/releases/tag/v0.1.0
