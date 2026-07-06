# Release Notes Draft

English | [简体中文](release-notes.md)

This document is the release-notes draft for the next public release. The final release notes should use the corresponding tag, CI result, Release artifacts, and deployment validation result as the source of truth.

## Summary

This release candidate focuses on improving MnemoNAS stability, public-access safety boundaries, deployment verifiability, and documentation maintainability as a self-hosted NAS. The current hardening branch is split into reviewable commits by risk area and has passed full branch-range validation.

## Major Changes

- Strengthened path, archive-download, WebDAV, public-share, workspace, CAS, and backup-restore boundaries, covering symlinks, traversal, percent-encoded dot segments, encoded query or fragment markers, percent-encoded sensitive parameter names, control characters, and rollback error paths.
- Upgraded `golang.org/x/image` to `v0.43.0` to resolve TIFF/WebP dependency-security findings reachable from thumbnail decoding, with the indirect `golang.org/x/text` version refreshed as part of the Go module update.
- Expanded backend and frontend coverage for authentication, users, home directories, directory quotas, directory access rules, share policies, and secure session defaults.
- Added Account Security for every authenticated user, reachable from the user menu, with the administrator Settings page linking to the same entry point. Voluntary and required password changes share validation and error handling, sign the account out on every device after success, and require a new sign-in. When the client can observe an unconfirmed request outcome, it clears local authentication state so the browser does not continue using a potentially revoked session. Requests to `POST /api/v1/auth/password` must now include an `expected_user_id` that matches the authenticated context; existing API callers that omit it receive `400 MISSING_EXPECTED_USER_ID`.
- The Files page now distinguishes moving an item to Trash from permanent deletion under the current policy, and disables only deletion when that policy is unknown. File lists return an object-identity token for each real item; delete confirmation submits the selected items' observed identities through `POST /api/v1/files-delete-intents` and atomically captures the complete policy and target-tree tokens. Replacing a selected path with a new file or directory invalidates the old identity even when its type, size, and modification time match. Target tokens now use a v3 hierarchical SHA-256 Merkle representation. The final token binds the canonical root path and complete path hierarchy. Each node digest binds a file-or-directory domain separator, the full permission mode, size, nanosecond modification time, object identity, and the snapshot content-hash field; for a regular file, that field carries the actual content digest. Directory nodes sort children by name and combine each child name and digest in that order. Delete confirmation samples the complete policy and mutation epoch while briefly holding the storage read lock, then releases the lock and performs a strict traversal of the entire requested target batch outside the lock, applying per-entry access, mount-boundary, and type checks, hashing regular files, and streaming node digests into the v3 target token in name-sorted depth-first order. After the scan, it validates the mutation epoch again. If it changed, the server discards the entire batch result and performs bounded retries, using a read-locked fallback scan when necessary. Prepare retains only the root metadata required for the REST response and the target token; it does not retain a complete target-tree manifest. The final value remains a 64-character lowercase hexadecimal string that callers treat as opaque. Without contention, directory-scan and regular-file hash counts remain unchanged; contention can add scans and hashes from discarded attempts. The mutation epoch is not included in the v3 target token or exposed through REST requests or responses; the existing REST shapes remain unchanged. `DELETE /api/v1/files/{path}` requires the mode, policy token, and target token. A complete-policy or target change detected before atomic capture returns `409 DELETE_POLICY_CHANGED` or `409 DELETE_TARGET_CHANGED` without committing business state. After capture begins, deletion uses a source-local no-replace stage, a complete Trash-copy manifest, and a handle-anchored quarantine with permissions no broader than mode `0700`. An uncommitted result that cannot be rolled back safely returns `500` and preserves recovery evidence; a committed result with incomplete physical cleanup returns success with cleanup warnings. The MnemoNAS storage lock and mutation epoch cover only in-platform operations. Direct filesystem writes by another process with the same UID and concurrent mounts by a privileged process do not advance the epoch; existing object-identity, mount-boundary, staged-object verification, and recovery-safety checks continue to handle such changes. Process crashes and power loss are likewise outside these mechanisms. The background retention sweep now removes both expired versions and expired Trash items. The Trash page uses each item's persisted expiry and states that capacity limits can remove items earlier.
- Emptying Trash now submits the exact confirmed ID set through `POST /api/v1/trash/empty`; the former `DELETE /api/v1/trash` endpoint has been removed. The confirmation dialog freezes items, IDs, count, and size when opened. Selections above 1000 items are submitted from that frozen set in batches, followed by one refresh after all batches finish. Under one storage write lock, the server loads current items, preflights access rules for every selected item that still exists, and then permanently deletes in request order. Unselected, newly added, or already missing items cannot be deleted accidentally. The response completely partitions requested IDs into deleted, remaining, and skipped arrays and reports partial results separately from post-commit physical-cleanup warnings.
- Hardened email alert egress. Message headers and the SMTP envelope sanitize control characters, reducing header-injection risk if an internal caller or later extension bypasses config validation.
- Improved visible Web quality. Core pages, public entry points, mobile layouts, baseline accessibility, runtime errors, failed requests, and broken visible text are covered by Playwright scans. The Dashboard first-run checklist and login page surface setup safety hints for auth-disabled, share-enabled-without-auth, anonymous WebDAV, and enabled `allow_unsafe_no_auth` states.
- Hardened systemd, Docker, reverse proxy, public-access templates, doctor, public-domain readiness validation, release package, and release artifact verification paths. Docker preflight rejects empty `MNEMONAS_IMAGE` values, values that start with `-`, contain whitespace or control characters, look like URLs, use invalid `sha256` digests, or carry Docker-incompatible tags before Compose validation, and URL-shaped diagnostics do not echo credentials, query strings, or fragments. Docker quickstart, preflight, and the container entrypoint also reject configured `auth.users_file` container paths with parent directory segments or control characters, preventing `/data/../...` values from being mapped to host-side initial-password paths outside the data directory. Docker smoke rejects image references that start with `-` or contain whitespace or control characters before starting the container. Invalid container healthcheck target diagnostics only print a redacted URL shape and do not write embedded credentials, original query strings, or fragments to container logs. The reverse-proxy setup script reports only the host-format constraint for invalid `MNEMONAS_UPSTREAM_HOST` values and does not echo raw host values or credentials, query strings, or fragments from pasted URLs. `mnemonas-doctor --public-domain` prints only a redacted URL shape for invalid `share.base_url` diagnostics and does not echo credentials, query strings, or fragments from misconfigured values. The public go-live smoke and doctor reject `localhost`, IP addresses, and four-part all-numeric hostnames, manual port-review commands include both connection and total request timeouts, and blank custom backend target lists or ambiguous target paths are rejected so port-exposure checks cannot be skipped or produce unclear backend probe URLs. The public go-live smoke only prints redacted target shapes for invalid custom backend targets and bad HTTP redirects, avoiding query strings, fragments, userinfo, and control-character path content in failure logs. The Release workflow checks archives, checksums, the required target set, unexpected downloaded artifact entries, archive entry types, duplicate entries, control-character paths, whitespace paths, archive-member control-character paths, archive-member whitespace paths, backslash paths, ambiguous paths, GHCR repository names, and the pushed container image tag before creating the GitHub Release. The release artifact verifier supports passing dash-prefixed local artifact directories through `--` and uses shell-safe diagnostics for control-character paths in downloaded artifact directories, checksum manifests, and archive members, preventing post-publish verification paths from being interpreted as shell-builtin options or writing raw control characters to verification logs. The post-publish maintainer entry point normalizes dash-prefixed explicit artifact directories as local paths and rejects invalid repository names before download.
- The systemd install and uninstall scripts use shell-safe diagnostics when rejecting path, address, port, or account parameters that contain control characters, preventing failure logs from writing raw control characters or creating injected log lines.
- Benchmark, E2E, fault-injection scripts, the reverse-proxy setup summary, and bilingual reverse-proxy WebDAV PROPFIND examples now pass WebDAV Basic Auth credentials through temporary curl config files, keeping passwords out of `curl` command arguments. Development and reverse-proxy documentation no longer include manual examples that place WebDAV passwords in `curl -u`, and script tests plus documentation contracts cover the boundary.
- The public go-live smoke auto-selects GNU timeout-compatible commands in `timeout`, then `gtimeout`, order for TCP probes, and supports `TIMEOUT_BIN` for compatible overrides.
- Release tags are checked before artifact builds and must use `vMAJOR.MINOR.PATCH` or a SemVer prerelease form such as `v1.2.3-rc.1`, with the Docker image tag after the leading `v` capped at 128 characters. The post-publish artifact verifier reuses the same version-validation logic for explicit or archive-inferred versions.
- Added rerunnable WebDAV curl protocol smoke checks for validating basic read/write, URL-encoded space paths, copy, move, and delete operations against a running service. The script rejects `WEBDAV_URL` values with whitespace, query strings, fragments, embedded credentials, backslashes, encoded slashes, encoded backslashes, or `.`/`..` path segments, and rejects non-`0/1` `CURL_INSECURE` values, with coverage in the script gate.
- Added a rerunnable backup restore-drill smoke entry point for exercising a running service by explicit backup job ID, covering job listing, single-job retrieval, immediate backup, retention check, restore drill, and restore-report download. The script does not create or delete backup jobs and rejects API URLs with whitespace, query strings, fragments, embedded credentials, backslashes, encoded slashes or backslashes, empty path segments, or dot segments before issuing requests.
- Added the post-publication go-live entry point `scripts/release-go-live-check.sh`. It validates the release tag, repository name, public domain, backup-drill API URL, job ID, and optional cookie file before starting any helper, normalizes uppercase or single trailing-dot domains for public checks, rejects repeated trailing-dot domains, then runs the release-readiness summary, GitHub Release and GHCR artifact verification, public `mnemonas-doctor --public-domain`, external-network go-live smoke, and backup restore-drill smoke in order. The restore drill requires an explicit API URL and job ID, or an explicit skip recorded in the release evidence.
- Added a WebDAV compatibility report form for collecting validation results or client-specific failures from Finder, Windows File Explorer, mobile file managers, media players, and CLI clients.
- The Maintenance completed-restore dialog can copy a restore cutover record with the target path, read-only verification result, cutover steps, pre-cutover confirmation, and rollback checklist. Restore reports now use raw restore-target matching to add explicit findings when the latest restore is complete but lacks matching read-only verification, when read-only verification predates restore completion, when it belongs to a different restore target, or when the read-only verification status cannot serve as current-target evidence, avoiding stale, cross-target, or unusable verification being read as current evidence. Batch restore results list cross-directory cutover candidates and conflict-disposition records, and include job names, backup targets, retention-policy status, candidate paths, read-only verification conclusions, verification error details, disposition guidance, and config-file retention requirements in the copyable result record for ticket or duty-log handoff.
- The Settings directory-access user matrix and unsaved-rule preview can copy a review record with the path, user read/write decisions, matched rules, and related-share impact, and keep backend-persisted recent review history, falling back to current-browser records when server history is unavailable.
- Share path policies can restrict which authenticated users, groups, or roles may create and maintain share links under a path, while administrators retain management access for repairing existing shares.
- Key disposition entry points in Shares, version history, Trash, and Maintenance write activity review records for share disable, deletion, re-enable, policy update, version restore, Trash restore, and backup restore execution results. Activity review history now immediately shows newly matching records after disposition when they match the current filter, making accidental sharing, deletion, and restore follow-up traceable.
- Tightened the release readiness summary: after the recorded full-validation target, `release-readiness` fails by default on committed or uncommitted non-release-documentation changes and requires refreshed full validation or an explicit draft override. Draft overrides for non-release-documentation changes now print `validation-warning` so they are not mistaken for final release readiness.
- `release-readiness` treats the bilingual Docker deployment guide as release documentation after the full-validation target, allowing final publication updates for the actual tag, Release workflow result, and artifact names while still rejecting ordinary documentation or code changes.
- `release-readiness` now requires all four hardening evidence documents to exist and record the same full-validation target, preventing missing evidence from being skipped before release.
- `release-readiness` also requires the bilingual hardening progress ledgers to record the same full-validation target on their `make release-readiness` rows, preventing the readiness summary from staying on an older target after full-validation evidence is refreshed.
- `release-readiness` also checks that both release-notes drafts record the current full-validation target, so stale validation snapshots fail before release.
- `release-readiness` requires the bilingual release-notes post-publish download and artifact-verifier examples to use `<tag>` placeholders, preventing fixed version numbers from entering copyable commands before the first release.
- `release-readiness` requires the `CHANGELOG.md` and `CHANGELOG.en.md` release checklists to include documentation, dependency-security, Docker build/smoke, selected release tag validation, and release script regression commands, and to retain the L1/L1+ release-candidate positioning, non-primary-copy, and external-backup boundaries, preventing final release verification from omitting key local gates or data-safety limits.
- `release-readiness` requires the Dependabot configuration to cover Go, Rust dataplane, Rust proto generator, Web npm, GitHub Actions, and Docker dependency update entry points, preventing the release branch from losing its dependency-maintenance baseline.
- `release-readiness` requires `.github/workflows/ci.yml` and `.github/workflows/release.yml` to retain key CI, E2E, Docker smoke, release-tag validation, release-artifact upload and download, checksum generation and publication, version- and repository-bound release-artifact verification, pre-publish image verification, release-job dependencies, and publication-permission baselines, preventing core automation paths from being lost before release.
- `release-readiness` requires `Makefile` to retain core local gate targets such as `check`, `verify-changed`, `release-readiness`, `quick-check`, `security-check`, `docker-check`, and `test-torture`, preventing CI, release-checklist, and maintainer-documentation entry points from being lost before release.
- `release-readiness` requires `.github/workflows/torture.yml` to retain manual and scheduled triggers, read-only permissions, the `RUN_LIVE_FAULTS: '0'` non-destructive guard, and the `make test-torture` entry point, preventing the long-running regression workflow from being lost before release.
- `release-readiness` requires blank Issues to stay disabled and checks that the bug report, usage question, feature request, and WebDAV compatibility Issue Forms keep sensitive-data redaction, diagnostic, and security-impact guidance, preventing public collaboration entry points from bypassing safety prompts.
- `release-readiness` checks that the security policy and support guide retain private vulnerability reporting, public-disclosure warnings, dataplane port exposure boundaries, dependency-security checks, and direct-public-exposure limitations.
- `release-readiness` requires the release checklist and bilingual release notes to retain the `mnemonas-doctor --public-domain`, `scripts/public-go-live-smoke.sh`, `scripts/backup-restore-drill-smoke.sh`, `scripts/release-go-live-check.sh`, and `cloud-firewall-checklist` entry points, preventing public-deployment environment review, post-publication go-live verification, and the restore-drill entry point from being omitted during final release preparation.
- `release-readiness` rejects a base ref that is not an ancestor of the current HEAD, preventing misleading release-readiness summaries from sibling branch ranges.
- Go test entry points now keep a 20-minute package timeout so heavy race packages are not interrupted by Go's default 10-minute timeout during full branch validation.
- Documentation checks reject copyable raw `?path=/...` path queries in API examples, requiring restore and favorite-check `path` query examples to use `%2F...` encoding.
- Documentation checks require the bilingual release-notes pre-release validation list to keep its Playwright E2E counts, frontend-unit-test counts, Docker image, and Docker smoke port aligned with the latest full-validation evidence in the hardening review summary, preventing stale local evidence after validation evidence refreshes.
- Documentation checks require the bilingual Docker deployment guide to retain the post-publish `verify-published-release.sh` command, version and repository arguments, optional artifact directory, image-manifest retry settings, `--skip-image-check`, `--keep-artifacts`, `--keep-published-artifacts`, empty-directory requirements, dash-prefixed artifact directories, and pre-download repository validation guidance, preventing post-publish verification guidance from regressing.
- Documentation checks require the security hardening guide's public-deployment checklist to retain the initial-password, WebDAV authentication, doctor, public firewall, anonymous WebDAV, direct-backend, and dataplane exposure review items.
- Documentation checks require the backup guide to retain restore-drill commands, 30-day drill reminders, failure categories, retained drill artifacts, restore-summary export, and the guidance that backups are not proven until restored, preventing recovery-usability documentation from regressing.
- Storage and configuration documentation clarify that the FastCDC API is a Rust dataplane capability, while current version history still uses whole-object CAS snapshots and does not reference-count CDC chunks; documentation checks reject overclaims that imply block-level version deduplication is enabled.
- Streamlined and synchronized Chinese and English documentation, including deployment, configuration, FAQ, roadmap, security, hardening progress, and pre-release review entry points.

## Release Artifacts

The Release workflow is expected to produce:

- Linux x86_64 / ARM64 binary archives.
- macOS Intel / Apple Silicon manual-run archives.
- `checksums.txt`.
- GHCR container image tags.

Archives should include a top-level directory, `nasd`, `dataplane`, Web UI static assets, systemd install/uninstall scripts, doctor, Docker Compose templates, `.env.example`, deployment templates, and Chinese/English documentation. The packaged `.env.example` should preset the GHCR image for the same release tag.

## Pre-Release Validation

The current hardening branch has the following validation evidence. Final publication should use the latest tag, Release workflow result, and required environment validation as the source of truth:

Latest local full-validation snapshot: validation target `2a40b9624170`; `GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master` passed, covering diff whitespace, secret-leak scanning, workflow/YAML/script gates, the restore-report increment that uses raw restore-target matching to add explicit findings when the latest restore is complete but lacks matching read-only verification, when read-only verification predates restore completion, when it belongs to a different restore target, or when the read-only verification status cannot serve as current-target evidence, the Dashboard first-run checklist and login-page setup-safety hint increment for auth-disabled, share-enabled-without-auth, anonymous WebDAV, and enabled `allow_unsafe_no_auth` states, the Activity review cache update that immediately inserts updated records matching the current history filter, the reverse-proxy WebDAV verification documentation contract gate, the hardening progress overall status boundary documentation contract gate, the post-publish verifier regression for dash-prefixed explicit artifact directories and invalid repository failures before download, the `--keep-artifacts` retained temporary download-directory increment, the `--keep-published-artifacts` published-artifact retention pass-through increment, the Docker deployment guide post-publish verification documentation contract extension, the post-publication go-live entry point, backup restore-drill pre-helper input validation, repeated trailing-dot rejection, and simulated regression, the release-readiness checklist summary-scope gate, the release-readiness bilingual Docker deployment guide release-documentation classification gate, the release-readiness gate that keeps the CHANGELOG known limitations aligned with the L1/L1+ release-candidate positioning, non-primary-copy, and external-backup boundaries, Release workflow structural gates, the hardening progress `make release-readiness` row-level validation-target gate, the `make release-readiness` entry-point baseline, the config compatibility regression that verifies a legacy minimal config backfills current defaults, the share-creation execution-result record increment, the share policy-update execution-result record increment, `make check`, toolchain consistency, Go/Rust/frontend dependency security scans, example config validation, public-access templates, protobuf regeneration stability, Rust fmt/test/clippy, proto-gen fmt/test/clippy, frontend lint/typecheck/unit/build, 379 Playwright E2E cases, Docker build, Docker image `sha256:2627520ecc18f3bc7f9a5847b4d73050251150a9d4b0169a30b8f463823bfe3f`, and Docker smoke. The Docker smoke used the Docker-assigned loopback port `http://127.0.0.1:32813`.

- `GOTOOLCHAIN=local ./scripts/verify-changed.sh`
- `GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master`
- `make scripts-check`
- `make docs-check`
- `make security-check NPM_AUDIT=1`
- `make docker-check`
- `make release-readiness`
- `sudo mnemonas-doctor --public-domain <domain>`
- `./scripts/public-go-live-smoke.sh <domain>`
- `./scripts/backup-restore-drill-smoke.sh`
- `./scripts/release-go-live-check.sh`
- `docs/cloud-firewall-checklist.en.md`
- `./scripts/check-release-tag.sh <tag>`
- `./scripts/test-release-tag.sh`
- `./scripts/test-release-package.sh`
- `./scripts/test-release-artifacts.sh`
- Public go-live TCP reachability test: `scripts/test-public-go-live-smoke.sh`
- Backup restore-drill smoke safety test: `scripts/test-backup-restore-drill-smoke.sh`
- Release artifact dash-prefixed directory test: `scripts/test-release-artifacts.sh`
- Docker quickstart safety test: `scripts/test-docker-quickstart.sh`
- Docker preflight safety test: `scripts/test-docker-preflight.sh`
- Docker container startup safety test: `scripts/test-docker-start.sh`
- Docker smoke safety test: `scripts/test-docker-smoke.sh`
- WebDAV curl smoke safety test: `scripts/test-webdav-client-smoke.sh`
- Release workflow incremental validation: `make workflows-check`, `make scripts-check`, `./scripts/check-secret-leaks.sh`, `make toolchains-check`, `git diff --check`
- Playwright E2E: `379 passed`
- Frontend unit tests: `3124 passed`
- Docker build and `scripts/docker-smoke.sh`

If code, scripts, configuration, documentation, or workflow files change again before release, rerun the matching validation.

## Post-Publish Verification

After the release tag is published, prefer the unified go-live entry point:

```bash
./scripts/release-go-live-check.sh \
  --version <tag> \
  --domain nas.example.com \
  --repository seanbao/mnemonas \
  --artifact-dir dist/release-check \
  --backup-api-url https://nas.example.com/api/v1 \
  --backup-job-id external-disk \
  --cookie-file cookies.txt
```

To make the unified go-live check retain temporary downloaded artifacts for failure investigation, omit `--artifact-dir` and pass `--keep-published-artifacts`; explicit `--artifact-dir` values are maintainer-selected and already retained, so they cannot be combined with this option.
When the backup restore drill cannot be run for the release, pass `--skip-backup-restore-drill` explicitly and record that the release does not have complete restore evidence.
To verify only the GitHub Release artifacts, run:

```bash
mkdir -p dist/release-check
./scripts/verify-published-release.sh \
  --version <tag> \
  --repository seanbao/mnemonas \
  --artifact-dir dist/release-check
```

Then complete at least one archive-install smoke test, one Docker release-image startup smoke test, public documentation link checks, and deployment-environment review covering `mnemonas-doctor --public-domain`, external-network `public-go-live-smoke.sh`, DNS, firewall, TLS, and cloud security groups.
Explicit `--artifact-dir` values may use dash-prefixed relative paths, and repository names are validated as GHCR-compatible lowercase `owner/repo` values before download.
To retain temporary downloaded artifacts while investigating a failure, omit `--artifact-dir` and pass `--keep-artifacts`; the script prints the retained directory.

## Known Limitations

- This release candidate is positioned as a fully locally validated L1 private file cloud with an initial L1+ public-access safety baseline, not as the only long-term copy of important data. Production use should keep external backups and continue collecting real-media restore, remote-restore disposition, cross-version upgrade, and rollback records.
- The mountable SMB/Samba runtime is still not enabled. The current build only keeps configuration, diagnostics, and runtime-state notices.
- `LOCK` / `UNLOCK` are virtual WebDAV compatibility behavior. Concurrent multi-client edits of the same file still require conflict control in the client or upper workflow.
- Real public deployments depend on the specific DNS, firewall, TLS, reverse-proxy, and cloud security-group configuration. Templates and doctor checks do not replace environment-level review.
- If a future version introduces irreversible data migration, rollback should follow the matching release note or backup-restore procedure.

## Maintainer Release Checklist

- Confirm `CHANGELOG.md` and `CHANGELOG.en.md` cover this release.
- Confirm this draft is updated with the final tag, validation results, and artifact names.
- Confirm `git status --short --branch` is clean.
- Confirm `./scripts/plan-hardening-commits.sh --fail-on-manual` reports no paths left to group.
- Run `make release-readiness` and confirm commit subjects, temporary `fixup!` / `squash!` commits, hardening validation evidence, release-documentation commands, public-deployment review commands, security policy, Dependabot baseline, CI/Release workflow baseline, Makefile core local gate target baseline, torture workflow baseline, blank-Issue disablement and Issue Form safety guidance, and community health files pass.
- After creating and pushing the tag, confirm the Release workflow succeeds.
- After publication, run `./scripts/release-go-live-check.sh` and record the artifact verification, public smoke, and backup restore-drill results.
