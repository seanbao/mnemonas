# Changelog

English | [简体中文](CHANGELOG.md)

All notable changes are recorded in this file.

This project follows [Semantic Versioning 2.0.0](https://semver.org/).

## [Unreleased]

MnemoNAS is still under development and has not published any usable release. The current source tree is for development and validation only; it must not hold real data or be used for production deployment. Problems and suggestions may be submitted through GitHub Issues.

### Added

#### Flutter Client (Development)
- Added a Flutter client project for Android, Linux, and Windows, with Android as the first usable-platform target. The current source implements server validation, Bearer sessions with single-use refresh-token rotation, secure session storage, file browsing and core file operations, streaming upload and download, Android Storage Access Framework export, account flows, and an issue-feedback entry. Linux and Windows native build and runtime validation are incomplete, and no usable client version has been published.

#### Web UI
- Dashboard with storage usage, file/version counts, recent activity, quick actions, and data-plane health. The desktop sidebar now includes a Home entry consistent with mobile navigation.
- File manager with breadcrumbs, list/grid views, context actions, drag-and-drop upload, batch operations, upload queue, and thumbnails.
- Album mode with image waterfall layout, generated thumbnails, and focused image browsing.
- Version history with version metadata, size comparison, and restore.
- Trash with time-based listing, restore, batch restore, and empty-trash flow.
- Filename search with highlighted results and quick navigation.
- Activity log with filters, details, statistics, and disk-health system events.
- Storage page shows filesystem type, mount point, and backing device/dataset source.
- User management with create/edit/delete, home directory and quota editing, password reset, enable/disable flows, and a dedicated **Directory & Access** view for directory quotas, access rules, effective-access review, and review history.
- Share management with link creation, password protection, expiration, access statistics, public share access, risk filtering, soon-expiry reminders, policy presets, and direct disable actions for high-risk links.
- The Favorites page provides administrators with an independently loaded, reset, and saved feature switch whose request contains only `favorites`.
- Settings opens with task-oriented entries for account and remote access, data protection and permissions, sharing and collaboration, device mounting, and device status and notifications. The device-status entry opens Health. Mobile uses a single category selector, while expert network parameters are collapsed by default. CDC, data-plane connections, and related deployment parameters are no longer exposed in the Web UI and remain managed through configuration and diagnostics.
- Settings for server, storage, retention, and WebDAV.
- Scheduled Scrub configuration now lives on Maintenance and saves through an isolated request containing only `maintenance.scrub`.
- Health owns disk-health and notification configuration. Each form loads, validates, resets, and saves independently through requests containing only `disk_health` or `alerts`; saved notification channels can send a test alert.
- Public access wizard and security self-check entry point for HTTPS reverse proxy, trusted proxy hops, and share-domain configuration.
- Desktop and mobile E2E coverage for the public access wizard.
- Health and maintenance views for uptime, storage health, disk SMART/temperature/media-wear/missing-device status, scheduled Scrub status/retries, SMB preview runtime state, scrub, GC, object browsing, backup job health/schedules/retention/restore drills, and diagnostic bundle export.

#### Backend API
- Authentication APIs for JWT login, logout, refresh, password changes, and current-user lookup.
- User management APIs, including user-level quotas. Non-admin Web/API uploads, copies, moves, and trash restores, plus administrator-initiated version restores into a limited user's home directory, return `QUOTA_EXCEEDED` when they exceed the configured quota and can emit `quota_exceeded` Webhook/Telegram/WeCom/DingTalk/SMTP alert events.
- `storage.directory_quotas` directory hard limits and storage-page directory quota usage summaries. Web/API uploads, copies, moves, trash restores, version restores, and WebDAV PUT/COPY/MOVE operations check matching directory quotas before writing.
- User groups and `storage.directory_access_rules` for shared-directory read/write grants by user, group, or role. Web/API, WebDAV users mode, search, shares, favorites, trash, and activity filtering use the same path authorization decision.
- Effective access checks, unsaved-rule previews, per-path user matrix views, related-share impact checks, copyable directory-access review records, and backend-persisted recent review history in the Settings API and the **Users > Directory & Access** view. Directory-policy saves use an isolated payload and do not overwrite configuration maintained by the Settings page. Server history falls back to current-browser records when unavailable.
- WebDAV supports `auth_type = "users"` so clients can mount with MnemoNAS user accounts; non-admin mounts are rooted at the user's `home_dir`, guest accounts are read-only, and PUT/COPY writes honor user quotas.
- Share-link APIs including public access, password checks, default expiry/access-limit policy, and share risk markers.
- Activity log APIs, including scrub system events.
- Runtime settings APIs, including public-access security self-check, certificate renewal and failure-triage guidance, scheduled Scrub updates, and hot updates for Webhook/Telegram/WeCom/DingTalk/SMTP alert notifications.
- Configured local backup jobs and command-backed restic/rclone remote targets with run-now execution, lightweight scheduling, automatic backup windows, local snapshot retention, automatic/manual retention checks, job health status, manifest-based local restore drills, non-destructive restore previews, batch restore preview/execution for up to 20 items with per-item restore and read-only verification results, restore preflight for target isolation/state/capacity/content/config handling, failed-preflight blocking, safe-directory local/restic/rclone restore, persisted post-restore verification reports, post-restore cutover and rollback checklists, copyable restore cutover records, restore summary export, scheduled restore-drill reminders, rate-limited stale/missing restore-drill alerts, restore-drill history, success-rate summary, failure categorization, restore result history, remote consistency checks, and Webhook/Telegram/WeCom/DingTalk/SMTP events for backup failures, retention-check failures, and warnings.
- Disk health API and runtime settings for `smartctl --json` SMART checks, temperature thresholds, NVMe/ATA media-wear signals, missing-device detection, serial-drift detection, `disk_health` activity-log records, and Webhook/Telegram/WeCom/DingTalk/SMTP events.
- `[maintenance.scrub]` supports background scheduled Scrub with bounded failure retries; the Settings API and Web Maintenance page can hot-update its scheduler, and diagnostics report schedule settings, latest Scrub state, and retry counts. Manual and scheduled scrub completion writes activity entries; scrub failures, object anomalies, and incomplete result persistence send `scrub_run` events through Webhook/Telegram/WeCom/DingTalk/SMTP notifications.
- Login rate limits send throttled `login_rate_limited` warning events through configured alert channels, containing only username and client address, never passwords or tokens.
- SMB preview diagnostics return sanitized runtime state and share counts, and `nasd --check-config` warns when `smb.enabled=true` because current builds do not start an SMB/Samba listener.

#### Project Tooling
- Desktop and mobile visual regression for the consumer settings overview with fixed dynamic data and a `0.005` screenshot-difference limit.
- GitHub Actions CI/CD for Go, Rust, frontend checks, Docker builds, and release packaging.
- Release workflow for multi-platform binaries and container images.
- Linux/systemd install and uninstall scripts.
- systemd install and uninstall scripts use shell-safe failure diagnostics for path, address, port, and account parameters with control characters, preventing deployment logs from writing raw control characters or injected log lines.
- `mnemonas-doctor` deployment diagnostics, including public HTTPS certificate checks, HTTP-to-HTTPS redirect checks, and manual cloud-firewall review guidance.
- `mnemonas-doctor --public-domain` detects broad UFW allow rules for backend control-plane and dataplane ports, and consistently expands `~` in storage and WebDAV user-file paths.
- `mnemonas-public-setup` public HTTPS reverse-proxy setup helper.
- Traefik and Cloudflare Tunnel public-access templates with script checks that prevent backend and dataplane port exposure.
- Docker Compose preflight checks for Compose v2, Buildx, ports, permissions, disk space, and existing config.
- Container healthcheck binary so runtime images do not depend on `curl`.
- `tools/proto-gen` Rust protobuf generator so normal dataplane and Docker builds do not require system `protoc`.
- Script simulation tests and CI script checks.
- Script simulation fixtures cover changed-file selection, WebDAV auth modes, public reverse-proxy exposure checks, benchmark paths, and Web Husky hooks.
- `scripts/webdav-client-smoke.sh` runs curl protocol smoke checks against a running service, covering basic WebDAV read/write, URL-encoded space paths, copy, move, and delete operations, while rejecting `WEBDAV_URL` values with whitespace, query strings, fragments, embedded credentials, and non-`0/1` `CURL_INSECURE` values through a dedicated safety test in `make scripts-check`.
- WebDAV compatibility report form for submitting validation results or client-specific failures from common desktop, mobile, media-player, and CLI clients.
- `scripts/check-release-tag.sh` validates release tags as `vMAJOR.MINOR.PATCH` or SemVer prerelease tags before release artifacts are built, and caps the Docker image tag after the leading `v` at 128 characters.
- `scripts/verify-release-artifacts.sh` uses shell-safe diagnostics for control-character paths in downloaded artifact directories, checksum manifests, and archive members, preventing raw control characters from entering post-publish verification logs.
- `scripts/verify-published-release.sh` wraps GitHub Release downloads and the artifact verifier, downloading into an empty or temporary directory by default and checking archives, checksums, required targets, and the GHCR image tag. Explicit dash-prefixed `--artifact-dir` values are normalized as local paths, and invalid repository names fail before download, so post-publish checks are less likely to omit flags, mix in stale artifacts, or use the wrong repository.
- `scripts/release-readiness.sh` fails by default when non-release-documentation changes exist after the recorded full-validation target; draft summaries can opt in with `--allow-post-validation-changes`.
- `scripts/release-readiness.sh` treats the bilingual Docker deployment guide as release documentation after the full-validation target, allowing final publication updates for the actual tag, Release workflow result, and artifact names while still rejecting ordinary documentation or code changes.
- `scripts/release-readiness.sh` requires all four hardening evidence documents to exist and record the same full-validation target, preventing missing evidence from being skipped before release.
- `scripts/release-readiness.sh` checks that both release-notes drafts record the current full-validation target, preventing stale validation snapshots in release notes.
- `scripts/release-readiness.sh` requires the bilingual release-notes post-publish download and artifact-verifier examples to use `<tag>` placeholders, preventing fixed version numbers from entering copyable commands before the first release.
- `scripts/release-readiness.sh` requires the `CHANGELOG.md` and `CHANGELOG.en.md` release checklists to include documentation, dependency-security, Docker build/smoke, selected release tag validation, and release script regression commands, preventing key local gates from being omitted from final release verification.
- `scripts/release-readiness.sh` requires the Dependabot configuration to cover Go, Rust dataplane, Rust proto generator, Web npm, GitHub Actions, and Docker dependency update entry points, preventing the release branch from losing its dependency-maintenance baseline.
- `scripts/release-readiness.sh` requires `.github/workflows/ci.yml` and `.github/workflows/release.yml` to retain key CI, E2E, Docker smoke, release-tag validation, release-artifact upload and download, checksum generation and publication, version- and repository-bound release-artifact verification, pre-publish image verification, release-job dependencies, and publication-permission baselines, preventing core automation paths from being lost before release.
- `scripts/release-readiness.sh` requires `Makefile` to retain core local gate targets such as `check`, `verify-changed`, `release-readiness`, `quick-check`, `security-check`, `docker-check`, and `test-torture`, preventing CI, release-checklist, and maintainer-documentation entry points from being lost before release.
- `scripts/release-readiness.sh` requires `.github/workflows/torture.yml` to retain manual and scheduled triggers, read-only permissions, the `RUN_LIVE_FAULTS: '0'` non-destructive guard, and the `make test-torture` entry point, preventing the long-running regression workflow from being lost before release.
- `scripts/release-readiness.sh` requires blank Issues to stay disabled and checks that the bug report, usage question, feature request, and WebDAV compatibility Issue Forms retain sensitive-data redaction, diagnostic, and security-impact guidance, preventing public feedback entry points from bypassing safety prompts.
- `scripts/release-readiness.sh` checks that the security policy and support guide retain private vulnerability reporting, public-disclosure warnings, dataplane port exposure boundaries, dependency-security checks, and direct-public-exposure limitations.
- `scripts/release-readiness.sh` requires the release checklist and bilingual release notes to retain the `mnemonas-doctor --public-domain`, `scripts/public-go-live-smoke.sh`, `scripts/backup-restore-drill-smoke.sh`, `scripts/release-go-live-check.sh`, and `cloud-firewall-checklist` entry points, preventing public-deployment environment review, post-publication go-live verification, and the restore-drill entry point from being omitted during final release preparation.
- `scripts/release-readiness.sh` rejects a base ref that is not an ancestor of the current HEAD, preventing misleading release-readiness summaries from sibling branch ranges.
- `scripts/release-readiness.sh` checks that local commit subjects on the current release branch follow Conventional Commits and rejects leftover `fixup!` / `squash!` temporary commits.
- Added `make release-readiness` as a Makefile entry point for the release readiness summary, and the Makefile baseline gate retains that entry point so pre-release checks do not depend on remembering the script path.
- `scripts/public-go-live-smoke.sh` checks backend-port TCP reachability before HTTP status checks, so `8080/9090/9091` or custom backend ports fail when an external network can establish a TCP connection even if no HTTP status is returned.
- `scripts/public-go-live-smoke.sh` only prints redacted target shapes for invalid custom backend targets and bad HTTP redirects, avoiding query strings, fragments, userinfo, and control-character path content in failure logs.
- `scripts/public-go-live-smoke.sh` auto-selects GNU timeout-compatible commands in `timeout`, then `gtimeout`, order for TCP probes and supports `TIMEOUT_BIN` for compatible overrides.
- `make test`, `make quick-check`, `make coverage`, torture tests, and hardening group-planning commands use a 30-minute Go package timeout. Full race gates limit package concurrency to three, and CI reserves 60 minutes for the Go job so resource contention and verbose logging do not cause false timeouts in heavy packages.
- `scripts/check-doc-links.sh` requires the backup guide to retain restore-drill commands, 30-day drill reminders, failure categories, retained drill artifacts, restore-summary export, and the guidance that backups are not proven until restored, preventing recovery-usability documentation from regressing.
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
- Streamed uploads and version restores now use durable `prepared`, `published`, and `committed` decisions that coordinate namespace state, CAS version objects, and SQLite metadata. Startup rolls forward only committed transactions, rolls back all others, and retains evidence while blocking writes when reconciliation is unsafe. REST and WebDAV writes now require an existing direct parent instead of creating intermediate directories implicitly.
- `share.base_url` validation now rejects encoded query or fragment markers in the path, preventing public-share base URLs from becoming ambiguous after proxy or browser decoding.
- `scripts/docker-smoke.sh` now rejects empty image references, option-like image references that start with `-`, and image references with whitespace or control characters before starting the container, preventing Docker smoke image names from being interpreted as Docker run options. HTTP probes now use connection and total request timeouts with the more widely supported curl timeout argument form and treat successful `/health` HTTP responses as service readiness, so half-open connections or empty response bodies cannot stall the container smoke.
- Container healthcheck `MNEMONAS_HEALTHCHECK_URL` overrides now reject embedded credentials and fragments while preserving valid query parameters for probes.
- Release archives include a top-level directory, Web UI assets, install/uninstall scripts, diagnostic scripts, docs, public-access deploy templates, and Docker Compose/env templates preset for the matching release image.
- The default `docker-compose.yml` builds `mnemonas:local` from source; public release images can be selected with explicit version tags after they are available.
- Docker Compose host HTTP port is configured through `MNEMONAS_HTTP_PORT`.
- CI pins protobuf generator versions and `protoc 3.20.1`, then verifies generated files do not drift after `make proto`.
- Rust checks cover dataplane all-targets and `tools/proto-gen`.
- `make go-packages` centralizes Go package discovery for CI, docs examples, and security scans.
- `make workflows-check` runs actionlint against GitHub Actions workflows.
- README, development docs, and testing docs use the Node.js engine range from `web/package.json`.
- CI and release workflows use narrower permissions, concurrency controls, and job timeouts.
- Release workflow verifies downloaded archives, checksums, the required target set, and the pushed container image tag before creating the GitHub Release.
- Release workflow rejects non-SemVer release tags before building archives or container images.
- Release artifact verifier rejects unsafe checksum paths, control-character paths, whitespace paths, symlinked archives, unexpected downloaded artifact entries, special archive entries, duplicate entries, archive-member control-character paths, archive-member whitespace paths, backslash paths, and ambiguous path segments before running checksum validation, validates explicit or archive-inferred release versions against Docker/GHCR image tag constraints, supports passing dash-prefixed artifact directories through `--` for post-publication local checks, and retries briefly unavailable remote image manifests according to configuration.
- Release artifact verifier reports the verified target set on success so post-publication checks can confirm platform archive coverage.
- `scripts/verify-published-release.sh` reports the retained temporary download directory on successful or failed exit when `--keep-artifacts` is used without an explicit artifact directory, keeping failed post-publication artifact checks diagnosable.
- `scripts/release-go-live-check.sh` chains the release-readiness summary, published artifact verification, public doctor, external-network go-live smoke, and backup restore-drill smoke into one post-publication go-live entry point. It validates the release tag, repository name, public domain, backup-drill API URL, job ID, and optional cookie file before starting any helper, normalizes uppercase or single trailing-dot domains for public checks, rejects repeated trailing-dot domains, passes `--keep-published-artifacts` through to the published release verifier to retain temporary downloads, rejects combining that option with an explicit `--artifact-dir`, and requires backup API/job arguments unless the restore drill is explicitly skipped.
- Security docs distinguish the Web UI initial admin password from generated WebDAV Basic Auth credentials.
- Security docs and doctor checks warn that dataplane ports `9090/9091` should not be exposed to untrusted networks.
- Added a public cloud firewall checklist covering common cloud security groups, VPC firewalls, IPv6, and port-forwarding mistakes.
- Backup docs describe consistency windows and snapshot recommendations for live data.
- Moved directory access from Settings to **Users > Directory & Access**, with a structured rule editor, per-path user matrix, related-share impact, unsaved-rule preview, and review-record copy entry point for user, group, and role grants.
- `make verify-changed` treats Web Husky hooks as script changes and runs frontend type checking for Web changes, including untracked E2E helper and config files.
- Root example config comments are standardized in English, and `make verify-changed` runs `nasd --check-config` when `mnemonas.example.toml` changes.
- `make verify-changed` runs Docker template script fixtures when `.env.example` or Compose templates change.
- `make verify-changed` runs the Docker build when `.dockerignore` changes so build-context rules do not drift silently.
- `make verify-changed` always runs `git diff --check` against the matching worktree, staged, or base range, and selects the relevant toolchain checks when `.go-version`, `.nvmrc`, or `.golangci.yml`/`.golangci.yaml` changes.
- `make verify-changed` runs the public-access template safety fixture when `deploy/public-access/` templates change.
- `make verify-changed` validates YAML syntax when `.github/dependabot.yml`, `.github/dependabot.yaml`, `codecov.yml`, or `codecov.yaml` changes.
- WebDAV setup scripts and development helpers report Basic, users, and no-auth modes explicitly so generated credentials and user-account mounts are not confused.

### Fixed
- Fixed the isolated fault-injection runner creating group-writable directories under collaborative `umask` values, which caused the authentication state lock to reject startup before scenarios ran. Fault injection now completes the bootstrap administrator password change and requires an explicit corrupted-object count, an actual version-restore result, and SQLite quarantine evidence instead of treating generic error envelopes or unexecuted scenarios as passes.
- Upgraded the Rust data plane and protobuf generator lockfiles from `anyhow 1.0.102` to `1.0.103`, resolving the `RUSTSEC-2026-0190` memory-safety advisory for `Error::downcast_mut()`.
- Fixed Trash-copy drift after business commit still removing the correct staged source, a stage-path replacement being misidentified as original recovery evidence, and cross-root restore copying or deleting a same-name replacement. Physical source cleanup now performs a final complete Trash-copy proof. A regular-file witness receives a content hash before staging, and the recovery-copy path is written to `StagePath` only after the copy has synced both its file and parent directory and passed identity and content revalidation; otherwise `InspectionPaths` exposes the unknown stage replacement and unconfirmed copy locations for review. Cross-root file copying now checks the copied byte count and published destination content, atomically isolates the source and any cleanup target under random names, and on Unix randomly captures and rechecks each checked-remove leaf before the final unlink or rmdir so unknown replacements detected before the final filesystem call are not removed.
- Fixed permanent Trash-item deletion leaving content, SQLite metadata, or version references in an indeterminate state after a process crash or power loss. Explicit permanent deletion, exact emptying, expiry cleanup, and capacity cleanup now persist `prepared` and `committed` decisions under `.mnemonas/trash/.deleting/`. Startup recovery rolls back every uncommitted operation before rolling any committed operation forward. An unverifiable recognized journal or orphan stage fails closed and prevents writable startup, while legacy and untracked residue is never removed from its filename alone. The journal currently covers only permanent deletion of an item already in Trash, not live delete-to-Trash or Trash restore. The recovery path is available on Linux and macOS and requires one `nasd` writer per storage root.
- Fixed races that allowed multiple backup managers to mutate the same state root or local job directory concurrently. Backup state now uses a lifetime single-writer lock and directory-identity checks. Moving or replacing the state root quarantines the manager; an already committed `completed` result returns a warning to inspect the directory and restart, and later API requests return `503`. `local` backup, restore, and verification operations use a per-job target lock, and an unconfirmed lock release returns `500`; unsafe Unix directory ownership or mode fails closed. Restic jobs continue to rely on native repository locking, while shared rclone remotes require external serialization.
- Fixed first automatic backups being skipped by repeated clock reads, and shutdown advancing markers for schedules that never started or reminders that were not delivered. Scheduled trigger time now commits atomically with `running` state; restore-drill reminders record cooldown only after successful delivery and retry failures.
- Fixed missing cancellation and deadline signaling for custom backup notification calls. `NotifyBackupEvent` now receives a 10-second deadline and manager-shutdown cancellation, which custom implementations must observe. The built-in SMTP transport also uses a 30-second default timeout and honors an earlier upstream deadline.
- Fixed local backup retention cleanup running before successful state was durably committed. Backup state, snapshot publication, and cleanup now follow a durable order, while committed runs with incomplete later sync or cleanup return success with structured warnings and an HTTP `Warning` header.
- Fixed `golang.org/x/image` TIFF/WebP dependency-security findings reachable from thumbnail decoding by upgrading `golang.org/x/image` to `v0.43.0` and refreshing the indirect `golang.org/x/text` version.
- Fixed backup and frontend diagnostic redaction for percent-encoded sensitive parameter names such as `access%5Fkey` and `secret%2Dkey`, preventing credential values from leaking in error text.
- Fixed `server.trusted_proxy_hops` updates through the settings API not immediately updating runtime client-IP and HTTPS forwarded-header interpretation.
- Fixed the public go-live smoke and `mnemonas-doctor --public-domain` accepting four-part numeric inputs outside the IPv4 range as DNS hostnames. Manual public-port checks now include total request timeouts so half-open connections cannot stall the review.
- Fixed the Web Husky pre-commit hook so it resolves the repository root, runs from `web/`, and uses the frontend lint-staged configuration.
- Fixed frontend authentication setup so reused-server E2E runs can opt into auth-state skips, while isolated E2E runs fail instead of silently saving an empty auth state.
- Fixed long backup-configuration example paths being clipped in the Maintenance page on mobile layouts.
- Prevented systemd installation and static-file discovery from treating Vite source directories as built Web UI output.
- Fixed broad `.gitignore` / `.dockerignore` rules for `nasd`.
- Removed runtime `apt-get` dependency from Docker health checks.
- Removed the Docker/Rust build dependency on system `protoc`.
- Removed tracked local build/runtime artifacts.
- Fixed frontend syntax, hook dependency, lint, and unused-symbol issues.

---

## 0.1.0 - Unreleased

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
- There is currently no release candidate and no usable release has been published. The development source tree must not hold real data or be used for production deployment.

### Compatibility
- Go: 1.25.12+
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
- [ ] If public access is planned, run on the server: `sudo mnemonas-doctor --public-domain <domain>`, and review the [Public cloud firewall checklist](docs/cloud-firewall-checklist.en.md) for DNS, firewall, TLS, and cloud security groups
- [ ] If public access is planned, run from an external network: `./scripts/public-go-live-smoke.sh <domain>` to confirm HTTPS, same-domain redirects, and private backend ports
- [ ] If this release includes the backup and restore path, run `./scripts/backup-restore-drill-smoke.sh` against at least one configured backup job and confirm that immediate backup, retention review, restore drill, and restore report download can be repeated
- [ ] Run the post-publication go-live check: `./scripts/release-go-live-check.sh --version <tag> --domain <domain>`; provide `--backup-api-url` and `--backup-job-id` for the restore drill, or explicitly record `--skip-backup-restore-drill`
- [ ] Confirm `./scripts/plan-hardening-commits.sh --fail-on-manual` reports no unclassified paths
- [ ] Run release readiness summary: `make release-readiness`
- [ ] Update `CHANGELOG.md`, `CHANGELOG.en.md`, README version references, and [release notes draft](docs/release-notes.en.md)
- [ ] Validate the selected release tag: `./scripts/check-release-tag.sh <tag>`
- [ ] Run release script regressions: `./scripts/test-release-tag.sh`, `./scripts/test-release-package.sh`, and `./scripts/test-release-artifacts.sh`
- [ ] Create and push a Git tag, for example `git tag -a <tag> -m "Release <tag>"` followed by `git push origin <tag>`
- [ ] After publication, run `./scripts/verify-published-release.sh --version <tag> --repository seanbao/mnemonas` to download and verify release artifacts, checksums, and container image tags
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

[Unreleased]: https://github.com/seanbao/mnemonas/commits/HEAD
