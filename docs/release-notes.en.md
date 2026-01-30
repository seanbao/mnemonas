# Release Notes Draft

English | [简体中文](release-notes.md)

This document is the release-notes draft for the next public release. The final release notes should use the corresponding tag, CI result, Release artifacts, and deployment validation result as the source of truth.

## Summary

This release candidate focuses on improving MnemoNAS stability, public-access safety boundaries, deployment verifiability, and documentation maintainability as a self-hosted NAS. The current hardening branch is split into reviewable commits by risk area and has passed full branch-range validation.

## Major Changes

- Strengthened path, archive-download, WebDAV, public-share, workspace, CAS, and backup-restore boundaries, covering symlinks, traversal, percent-encoded dot segments, encoded query or fragment markers, percent-encoded sensitive parameter names, control characters, and rollback error paths.
- Expanded backend and frontend coverage for authentication, users, home directories, directory quotas, directory access rules, share policies, and secure session defaults.
- Hardened email alert egress. Message headers and the SMTP envelope sanitize control characters, reducing header-injection risk if an internal caller or later extension bypasses config validation.
- Improved visible Web quality. Core pages, public entry points, mobile layouts, baseline accessibility, runtime errors, failed requests, and broken visible text are covered by Playwright scans.
- Hardened systemd, Docker, reverse proxy, public-access templates, doctor, public-domain readiness validation, release package, and release artifact verification paths. Docker preflight rejects empty `MNEMONAS_IMAGE` values, values that start with `-`, contain whitespace or control characters, look like URLs, use invalid `sha256` digests, or carry Docker-incompatible tags before Compose validation, and URL-shaped diagnostics do not echo credentials, query strings, or fragments. Docker quickstart, preflight, and the container entrypoint also reject configured `auth.users_file` container paths with parent directory segments or control characters, preventing `/data/../...` values from being mapped to host-side initial-password paths outside the data directory. Docker smoke rejects image references that start with `-` or contain whitespace or control characters before starting the container. Invalid container healthcheck target diagnostics only print a redacted URL shape and do not write embedded credentials, original query strings, or fragments to container logs. The reverse-proxy setup script reports only the host-format constraint for invalid `MNEMONAS_UPSTREAM_HOST` values and does not echo raw host values or credentials, query strings, or fragments from pasted URLs. `mnemonas-doctor --public-domain` prints only a redacted URL shape for invalid `share.base_url` diagnostics and does not echo credentials, query strings, or fragments from misconfigured values. The public go-live smoke and doctor reject `localhost`, IP addresses, and four-part all-numeric hostnames, manual port-review commands include both connection and total request timeouts, and blank custom backend target lists or ambiguous target paths are rejected so port-exposure checks cannot be skipped or produce unclear backend probe URLs. The Release workflow checks archives, checksums, the required target set, unexpected downloaded artifact entries, archive entry types, duplicate entries, control-character paths, whitespace paths, archive-member control-character paths, archive-member whitespace paths, backslash paths, ambiguous paths, and GHCR repository names before creating the GitHub Release. The release artifact verifier supports passing dash-prefixed local artifact directories through `--`, preventing post-publish verification paths from being interpreted as shell-builtin options.
- Release tags are checked before artifact builds and must use `vMAJOR.MINOR.PATCH` or a SemVer prerelease form such as `v1.2.3-rc.1`, with the Docker image tag after the leading `v` capped at 128 characters. The post-publish artifact verifier reuses the same version-validation logic for explicit or archive-inferred versions.
- Added rerunnable WebDAV curl protocol smoke checks for validating basic read/write, URL-encoded space paths, copy, move, and delete operations against a running service. The script rejects `WEBDAV_URL` values with whitespace, query strings, fragments, embedded credentials, backslashes, encoded slashes, encoded backslashes, or `.`/`..` path segments, and rejects non-`0/1` `CURL_INSECURE` values, with coverage in the script gate.
- Added a WebDAV compatibility report form for collecting validation results or client-specific failures from Finder, Windows File Explorer, mobile file managers, media players, and CLI clients.
- The Maintenance completed-restore dialog can copy a restore cutover record with the target path, read-only verification result, cutover steps, pre-cutover confirmation, and rollback checklist. Batch restore results list cross-directory cutover candidates and conflict-disposition records, and include job names, backup targets, retention-policy status, candidate paths, read-only verification conclusions, verification error details, disposition guidance, and config-file retention requirements in the copyable result record for ticket or duty-log handoff.
- The Settings directory-access user matrix and unsaved-rule preview can copy a review record with the path, user read/write decisions, matched rules, and related-share impact, and keep backend-persisted recent review history, falling back to current-browser records when server history is unavailable.
- Share path policies can restrict which authenticated users, groups, or roles may create and maintain share links under a path, while administrators retain management access for repairing existing shares.
- Key disposition entry points in Shares, version history, Trash, and Maintenance write activity review records for share disable, deletion, re-enable, version restore, Trash restore, and backup restore execution results, making accidental sharing, deletion, and restore follow-up traceable.
- Tightened the release readiness summary: after the recorded full-validation target, `release-readiness` fails by default on non-release-documentation changes and requires refreshed full validation or an explicit draft override.
- `release-readiness` now requires all four hardening evidence documents to exist and record the same full-validation target, preventing missing evidence from being skipped before release.
- `release-readiness` also checks that both release-notes drafts record the current full-validation target, so stale validation snapshots fail before release.
- `release-readiness` rejects a base ref that is not an ancestor of the current HEAD, preventing misleading release-readiness summaries from sibling branch ranges.
- Documentation checks reject copyable raw `?path=/...` path queries in API examples, requiring restore and favorite-check `path` query examples to use `%2F...` encoding.
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

Latest local full-validation snapshot: validation target `16fefe99bb29`; `GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master` passed, covering the WebDAV README overview, README client summary, and mounting-guide compatibility-note alignment with the compatibility matrix, `make check`, dependency security scans, example config validation, public-access templates, protobuf regeneration stability, Rust fmt/test/clippy, proto-gen fmt/test/clippy, frontend lint/typecheck/unit/build, 375 Playwright E2E cases, Docker build, and Docker smoke. The Docker smoke used the Docker-assigned loopback port `http://127.0.0.1:32868`.

- `GOTOOLCHAIN=local ./scripts/verify-changed.sh`
- `GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master`
- `make scripts-check`
- `make docs-check`
- `./scripts/test-release-tag.sh`
- `./scripts/test-release-package.sh`
- `./scripts/test-release-artifacts.sh`
- Release artifact dash-prefixed directory test: `scripts/test-release-artifacts.sh`
- Docker quickstart safety test: `scripts/test-docker-quickstart.sh`
- Docker preflight safety test: `scripts/test-docker-preflight.sh`
- Docker container startup safety test: `scripts/test-docker-start.sh`
- Docker smoke safety test: `scripts/test-docker-smoke.sh`
- WebDAV curl smoke safety test: `scripts/test-webdav-client-smoke.sh`
- Release workflow incremental validation: `make workflows-check`, `make scripts-check`, `./scripts/check-secret-leaks.sh`, `make toolchains-check`, `git diff --check`
- Playwright E2E: `375 passed`
- Frontend unit tests: `3108 passed`
- Docker build and `scripts/docker-smoke.sh`

If code, scripts, configuration, documentation, or workflow files change again before release, rerun the matching validation.

## Post-Publish Verification

After the release tag is published, download the GitHub Release artifacts and run:

```bash
mkdir -p dist/release-check
gh release download v0.1.0 \
  --repo seanbao/mnemonas \
  --dir dist/release-check

./scripts/verify-release-artifacts.sh \
  --version v0.1.0 \
  --repository seanbao/mnemonas \
  --require-targets \
  --check-image \
  dist/release-check
```

Then complete at least one archive-install smoke test, one Docker release-image startup smoke test, public documentation link checks, and deployment-environment review for DNS, firewall, TLS, and cloud security groups.

## Known Limitations

- The mountable SMB/Samba runtime is still not enabled. The current build only keeps configuration, diagnostics, and runtime-state notices.
- `LOCK` / `UNLOCK` are virtual WebDAV compatibility behavior. Concurrent multi-client edits of the same file still require conflict control in the client or upper workflow.
- Real public deployments depend on the specific DNS, firewall, TLS, reverse-proxy, and cloud security-group configuration. Templates and doctor checks do not replace environment-level review.
- If a future version introduces irreversible data migration, rollback should follow the matching release note or backup-restore procedure.

## Maintainer Release Checklist

- Confirm `CHANGELOG.md` and `CHANGELOG.en.md` cover this release.
- Confirm this draft is updated with the final tag, validation results, and artifact names.
- Confirm `git status --short --branch` is clean.
- Confirm `./scripts/plan-hardening-commits.sh --fail-on-manual` reports no paths left to group.
- Run `./scripts/release-readiness.sh` and confirm commit subjects, temporary `fixup!` / `squash!` commits, hardening validation evidence, release-documentation commands, and community health files pass.
- After creating and pushing the tag, confirm the Release workflow succeeds.
- After publication, run the release artifact verifier and record the result.
