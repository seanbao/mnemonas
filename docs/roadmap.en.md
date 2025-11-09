# MnemoNAS Roadmap

English | [简体中文](roadmap.md)

This document tracks the product path from a self-hosted file cloud toward a practical home and small-team NAS. It guides development priorities, but it is not a release commitment. Scope may change based on data-safety requirements, maintenance cost, and user feedback.

## Current Assessment

As of 2026-06-14, MnemoNAS already includes Web file management, WebDAV, version history, trash, multi-user auth, user groups, user-level quotas, directory access rules, sharing, activity logs, health checks, Scrub, GC, diagnostics export, Docker deployment, systemd deployment, and post-build Docker container smoke tests. It is usable as a local private file cloud for personal or small-team evaluation, especially when the data is not the only copy.

The current capability set can be summarized by track:

- Public access has a first baseline: reverse-proxy scripts, `mnemonas-public-setup`, `mnemonas-doctor --public-domain`, the Web public-access wizard, the security self-check API, certificate-renewal guidance, certificate-failure triage guidance, the Web settings flow, and E2E regression coverage. Public diagnostics check broad UFW allow rules for backend ports and consistently expand `~` in storage and WebDAV user-file paths.
- Backup and restore include local jobs, command-backed restic/rclone remote targets, lightweight scheduling, automatic backup windows, retention checks, restore drills or remote checks, restore history, restore summaries, single-job restore progress steps, batch restore progress steps, post-restore read-only verification, post-restore cutover and rollback checklists, copyable restore cutover records, batch restore attention reasons, Dashboard risk summaries, Web maintenance status, and Webhook/Telegram/WeCom/DingTalk/SMTP notifications for backup, explicit restore, restore verification, restore drill, and retention-check events.
- Alerts and disk health include a saved-config `alert_test` entry point, directory-access and share-policy change events, soon-expiring share aggregate events, and `smartctl`-based SMART, temperature, missing-device, and serial-drift checks. These states are wired into the health page, diagnostic summaries, activity logs, and notifications.
- Quotas and permissions include user quotas, directory quotas, dynamic usage, account-attention summaries, filtering, sorting, review-summary copy, current-list export, quota-and-permission joint review, pre-save directory-quota review, and quota enforcement for Web/API/WebDAV write and restore paths. User groups and directory access rules share one permission decision across Web/API, WebDAV users mode, search, shares, favorites, trash, activity filtering, and Settings API effective-access checks. Share path policies can also restrict share creation and maintenance by user, group, or role.
- Storage visibility includes filesystem capacity, filesystem type, mount point, device/dataset source, redacted mount options, native data-checksum hints, and an administrator storage-health summary. SMB has preview config, credentials, gateway authorization, and diagnostics, but this build still does not start a mountable SMB/Samba runtime; that track is deferred until `smboxide` is mature enough to integrate.

It is not yet a complete NAS appliance. The main missing areas are a mountable SMB runtime, more complete guided restore workflows for cross-directory scenarios, permission inheritance/share-rule cleanup, and fuller storage-pool visibility. Until external backups and the public-access security check flow are mature, MnemoNAS should not be treated as the only long-term copy of important data or exposed directly without an HTTPS reverse proxy.

The Dashboard first-run checklist now includes authentication, sharing, and WebDAV status highlights, and warns before public deployment when authentication is disabled or WebDAV anonymous access is enabled. This summary supplements first-run onboarding and does not replace the security self-check API, `mnemonas-doctor`, or cloud firewall review.

## Product Positioning

MnemoNAS does not aim to clone all features of TrueNAS, Synology DSM, or Unraid. Its priority is to provide a traceable, migratable, native-file-first self-hosted storage entry point.

Priorities:

1. Data safety and recoverability before feature count.
2. Web UI and WebDAV experience before complex storage-pool management.
3. Deployability, diagnostics, and backups before full automation.
4. Features that clearly support home and small-team workflows first.

## Availability Levels

| Level | Goal | Scope | Required baseline |
| --- | --- | --- | --- |
| L0 | Development preview | Local debugging and demos | Basic file read/write, startup scripts, unit tests |
| L1 | Private file cloud | Personal or small-team non-primary copies; LAN or controlled private network by default | Web/WebDAV, auth, versions, trash, deployment docs, external backup guide |
| L1+ | Secure remote access | Public access through a domain or reverse proxy | HTTPS, reverse-proxy preflight, security wizard, cookie/CSRF boundary, login protection, exposed-port checks |
| L2 | Home NAS baseline | Long-running LAN deployment | SMB, backup jobs, SMART/disk alerts, notifications, quotas, restore drills |
| L3 | Small-team NAS | Collaboration with clear sharing history | Folder permissions, groups, share-rule cleanup, activity review, disaster recovery flow |

The project currently meets the L1 private-file-cloud baseline and has the first baseline for the L1+ public-access entry point. The next stage is to make the L1+ public-access security check flow and restore loop suitable for long-running operation before continuing the data-protection and LAN-compatibility work needed for L2.

## Priority Roadmap

### P0: Reliable Private File Cloud

Goal: make MnemoNAS safe enough for cautious real-world personal use, assuming the user already has external backups.

| Capability | Current state | Next step | Acceptance criteria |
| --- | --- | --- | --- |
| Backup and restore | Local/remote backup, restore preview, restore execution, post-restore verification, state summaries, and notifications exist | Add broader fault-injection coverage and continue polishing the cross-directory restore Web entry point | Local directory, external drive, or rclone/restic targets can be configured; the Dashboard and each job show backup, restore, verification, drill, findings, and alert state |
| Deployment reliability | Docker and systemd paths, a systemd successful-upgrade test that preserves existing config and data, a systemd rollback test that reruns the previous release installer to restore runtime assets while preserving config and data, post-build loopback Docker container smoke tests, release install/uninstall safety tests, and diff-aware validation exist | Continue formalizing config-migration, cross-version drills, and real-environment rollback records | Release install, upgrade, rollback, uninstall, data retention, and Docker image health startup are covered by automated tests |
| HTTPS and security wizard | Reverse-proxy scripts, Traefik/Cloudflare Tunnel templates, Web wizard, security self-check API, public/certificate/HTTP-redirect doctor checks, renewal guidance, certificate-failure triage guidance, cloud-firewall checklist, and desktop/mobile E2E regression coverage exist | Expand more failure cases and mobile wizard coverage | Public domain, LAN self-signed, reverse-proxy headers, Secure/SameSite cookies, CSRF, download sessions, and internal dataplane ports are covered by automated tests and docs |
| Data integrity | Scrub, GC, and diagnostics exist; manual and scheduled Scrub runs write activity entries; failures, object anomalies, or incomplete result persistence send Webhook/Telegram/WeCom/DingTalk/SMTP notifications; scheduled Scrub has bounded failure retries, can be hot-updated from Web settings, and surfaces schedule state in health/diagnostics | Add native ZFS/Btrfs scrub coordination and deeper failure remediation guidance | Scrub failures are visible in UI, activity logs, and notifications |
| Secure defaults | Web session uses HttpOnly cookies, with login rate limiting and a user-session revocation action available | Expand admin security reminders and dangerous-configuration warnings | Weak passwords, default passwords, cross-site requests, suspicious logins, and public-share misconfiguration are blocked or warned clearly |
| WebDAV compatibility | Basic matrix exists | Expand Windows, macOS, and rclone regression coverage | Critical clients cover read, write, rename, delete, and recovery behavior |

Current backup and restore coverage:

- Backup targets: local jobs, local snapshot retention, command-backed restic/rclone remote targets, lightweight scheduling, and automatic backup windows.
- Restore flow: safe-directory local snapshot restore, safe-directory restic/rclone remote preview and restore, single-job restore progress steps, restore preflight, failed-preflight blocking, post-restore read-only verification, latest-restore recheck, cutover/rollback checklists, copyable restore cutover records, and restore-summary export.
- Batch entry point: batch restore attention reasons, suggested next steps, progress steps, selection, preview, pre-submit review, and execution.
- Visibility: restore-summary findings preview, restore-drill history, success-rate summaries, failure categorization, restore result history, matched restore-verification association, remote-retention auto-detection, scheduled restore-drill status and stale notifications, Dashboard risk summaries, and Web maintenance view.
- Notifications: Webhook, Telegram, WeCom, DingTalk, and SMTP cover backup, restore, restore-verification, restore-drill, and retention-check events.

### P1: Home NAS Baseline

Goal: cover the basic expectations for a LAN home NAS.

| Capability | Gap | Suggested implementation | Acceptance criteria |
| --- | --- | --- | --- |
| SMB/Samba | Preview config, credentials, gateway authorization, and diagnostics exist; current builds are not mountable | Defer runtime integration until `smboxide` is mature, or provide an official Samba sidecar while preserving permission and versioning boundaries | Windows and macOS can mount it directly; permission and path mapping do not bypass security boundaries; health and doctor output report runtime state accurately |
| Disk health | SMART JSON collection, temperature thresholds, lifetime/media-wear fields, missing-device detection, serial mismatch detection, health UI, activity logs, and Webhook/Telegram/WeCom/DingTalk/SMTP alerts exist | Add more USB/RAID bridge compatibility notes | UI shows disk health; anomalies are recorded in activity logs and notifications |
| Notifications | Webhook, Telegram, WeCom, DingTalk, and SMTP email cover the main capacity, backup, restore, disk-health, Scrub, security, and sharing events | Support more operational or security event sources and additional notification channels as needed | Disk-full, backup-failed, restore-failed or warning, Scrub-failed, login-rate-limited, quota-exceeded, critical policy-change, and expiring-share events can notify users; administrators can send a test alert to verify saved channels |
| User quotas | User quotas, directory quotas, usage stats, review summaries, filtering, sorting, export, server-side tiered long-term trend history, current-browser fallback snapshots, quota-and-permission joint review, and write hard-limits exist | Add finer permission-aware quota history | Uploads, copies, moves, trash restores, version restores, and WebDAV writes honor the applicable user or directory quotas with clear errors and notifications |

Current notification coverage:

- Channels: Webhook, Telegram, WeCom, DingTalk, and SMTP email.
- Backup and restore: backup failures or warnings, explicit restore failures or warnings, post-restore read-only verification failures or warnings, restore-drill reminders, and retention-check failures or warnings.
- Runtime and security: disk-space, disk-health anomalies, Scrub anomalies, login rate limits, Web/API quota denials, and directory access or share policy changes.
- Sharing: aggregate reminders for enabled shares that are expiring soon.
- Test entry point: Web/API provides a saved-config `alert_test` test entry point.

Current user-quota coverage:

- Quota model: user-level quotas, directory quotas, dynamic usage, and directory-quota usage/status summaries.
- User management: account-attention count breakdown, filtering, reason filtering, attention-first ordering, user-list search, sorting, role/status filters, stat-card quick focus, limited-user aggregate quota/headroom overview, server-side tiered long-term quota trend history, current-browser fallback trend snapshots, quota-and-permission joint review, result summaries, and filter clearing.
- Review output: copyable account-review summaries, contact-, permission-, login-, and balance-aware summary copy, card states, user-card access-scope and review hints, copyable user-access review summaries, review-hint stats and filtering, and context-rich current-list export.
- Directory quotas: directory-quota attention list, filtering, attention-first summary copy with remaining capacity, and pre-save directory-quota change review.
- Enforcement: Web/API upload/copy/move/trash-restore user-quota enforcement for writes into `home_dir`, WebDAV PUT/COPY/MOVE user-quota enforcement for writes into `home_dir`, directory-quota enforcement for version restores, and Web/API quota-denial alerts.

### P2: Small-Team Collaboration

Goal: make home or small-team collaboration more controllable and easier to trace when something goes wrong.

| Capability | Gap | Suggested implementation | Acceptance criteria |
| --- | --- | --- | --- |
| Folder-level permissions | Basic directory ACLs, groups, single-user effective access checks, unsaved-rule previews, per-path user matrix views, family/small-team policy presets, pre-save rule-change review, directory-access coverage summary, related-share impact checks, copyable directory-access review records, and backend-persisted recent review history exist; more detailed bulk-application drilldown is still missing | Add more detailed bulk-application drilldown | Web, API, WebDAV, and shares use the same permission decision, and admins can review effective access |
| Share safety | Newly-created shares support default expiry, default access limits, family/temporary policy presets, pre-submit effective-policy review, directory-path policy enforcement for required passwords, max expiry, max access, and creator/maintainer user, group, or role scope, risk hints, pre-save share-policy change review, a share-policy coverage summary, rule-cleanup suggestions, soon-expiry reminders and notifications, review summaries, copyable review summaries, review filters, current-scope review-record creation, current-scope review-history handoff, share-type review-record filtering, expandable review details with share-disposition clues, and direct disable actions for high-risk links; more execution-result records can still be added | Add more share execution-result records | Admins can inspect, search, and disable all shares, and can find long-lived, soon-expiring, or over-broad public links |
| Activity review | Recent activity, statistics, high-risk review, persisted review records, filtering, export, and disposition entry points exist | Add more direct disposition actions and result records | Key activity can be reviewed by time range, path/directory, user, and action type to trace accidental deletes, moves, or shares |
| Admin recovery tools | Versions, trash, version-restore pre-submit impact review, Trash cross-directory batch-restore review, restore-result activity details, backup batch restore preflight, single-job restore progress steps, batch restore progress steps, single-job pre-submit restore review, and single-job/batch restore impact summaries exist | Continue completing cross-directory restore guidance and richer conflict disposition records | Restore preview shows conflicts, overwrite impact, and permission impact |

Current activity-review coverage:

- Base views: activity list, statistics overview, high-risk summary, and concentrated-window review scoped to the high-risk group.
- Review records: current-page review details, current-page disposition checklist, current-filter cross-page review records, and persisted review records with disposition status, action counts, path samples, user samples, linked activities, and share-disposition detail clues.
- Follow-up flow: batch follow-up view for review records, follow-up disposition status and note write-back, review-record CSV export, review-history filtering by reviewer/time/disposition status/linked activity/action group, and quick focus for follow-up and share reviews.
- Trace entry points: individual activity rows and review records link to related paths, shares, or high-risk activity; disposition entry points link to version history, trash restore pages, and path-scoped share-disposition views.
- Filters: time-range, path/directory, share/high-risk group, action-type, and user filtering, plus a share-list handoff to matching path-scoped share activity and an administrator-only clear action.

### P3: Advanced Capabilities

Goal: improve long-term usability after the L2/L3 baseline is stable.

| Capability | Direction |
| --- | --- |
| Full-text and media indexing | Extend filename search to document text, EXIF, video metadata, and duplicate detection |
| Sync clients | Provide desktop/mobile sync or official rclone profiles for offline sync workflows |
| Storage-pool visibility | Visualize ZFS/Btrfs/mdraid status by integrating system tools rather than reimplementing storage stacks |
| Object-level encryption | User keys, recovery keys, key rotation, backup and restore process |
| Plugins and automation | Build on extension points for task runners, webhooks, and media processing |

## Recommended Implementation Order

1. **Improve HTTPS and security guidance**: if public access is planned, continue reducing certificate, reverse-proxy, cookie, CSRF, exposed-port, cloud-firewall, and default-account misconfiguration.
2. **Backup jobs and restore drills**: verify recoverability before expanding real-world usage.
3. **Disk health and notifications**: expand USB/RAID bridge compatibility and broaden notification channels.
4. **Quotas and permissions**: continue quota admin views, trends, ACLs, and groups so long-running multi-user deployments do not interfere with each other.
5. **SMB/Samba integration**: integrate a mountable runtime after `smboxide` matures, covering the most common LAN file-sharing workflow.
6. **Full-text indexing, sync clients, and plugins**: proceed after data protection and permissions stabilize.

## Quality Gates

Every P0/P1 feature should meet at least:

- Unit tests for core boundary conditions.
- API/storage integration tests for success, failure, and rollback.
- Web E2E coverage for desktop and mobile main flows.
- WebDAV or SMB features must update the client compatibility record.
- Public-access features must cover HTTPS certificate status, reverse-proxy headers, Secure/SameSite cookies, CSRF, login rate limiting, and internal-port exposure checks.
- Data deletion, restore, backup, quota, and permission features must include fault-injection or recovery tests.
- Documentation must explain boundaries, failure modes, and recovery steps.

## Non-Goals

The near-term roadmap does not prioritize:

- Replacing full NAS distributions for disk-array management.
- Shipping a custom SMB or NFS protocol stack before permission, version-history, trash, and activity-history boundaries are fully verified.
- Claiming long-term single-copy data safety without external backups.
- Recommending direct public-internet deployment that bypasses the security preflight, doctor checks, and HTTPS reverse proxy.
- Weakening default security boundaries for public anonymous sharing.

## Maintenance Rules

- Completed items should move to CHANGELOG or feature documentation rather than staying in the roadmap.
- If the implementation strategy changes, update acceptance criteria rather than only renaming the feature.
- Data-safety items should keep explicit failure modes and rollback strategies.
