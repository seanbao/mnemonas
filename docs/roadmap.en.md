# MnemoNAS Roadmap

English | [简体中文](roadmap.md)

This document tracks the product path from a self-hosted file cloud toward a practical home and small-team NAS. It guides development priorities, but it is not a release commitment. Scope may change based on data-safety requirements, maintenance cost, and user feedback.

## Current Assessment

As of 2026-06-03, MnemoNAS already includes Web file management, WebDAV, version history, trash, multi-user auth, user groups, user-level quotas, directory access rules, sharing, activity logs, health checks, Scrub, GC, diagnostics export, Docker deployment, and systemd deployment. It is usable as a local private file cloud for personal or small-team evaluation, especially when the data is not the only copy.

The current capability set can be summarized by track:

- Public access has a first baseline: reverse-proxy scripts, `mnemonas-public-setup`, `mnemonas-doctor --public-domain`, the Web public-access wizard, the security self-check API, certificate-renewal guidance, certificate-failure triage guidance, the Web settings flow, and E2E regression coverage.
- Backup and restore include local jobs, command-backed restic/rclone remote targets, lightweight scheduling, automatic backup windows, retention checks, restore drills or remote checks, restore history, restore summaries, post-restore read-only verification, post-restore cutover and rollback checklists, batch restore attention reasons, Dashboard risk summaries, Web maintenance status, and Webhook/Telegram/WeCom/DingTalk/SMTP notifications for backup, explicit restore, restore verification, restore drill, and retention-check events.
- Alerts and disk health include a saved-config `alert_test` entry point, directory-access and share-policy change events, soon-expiring share aggregate events, and `smartctl`-based SMART, temperature, missing-device, and serial-drift checks. These states are wired into the health page, diagnostic summaries, activity logs, and notifications.
- Quotas and permissions include user quotas, directory quotas, dynamic usage, account-attention summaries, filtering, sorting, review-summary copy, current-list export, pre-save directory-quota review, and quota enforcement for Web/API/WebDAV write and restore paths. User groups and directory access rules share one permission decision across Web/API, WebDAV users mode, search, shares, favorites, trash, activity filtering, and Settings API effective-access checks.
- Storage visibility includes filesystem capacity, filesystem type, mount point, device/dataset source, redacted mount options, native data-checksum hints, and an administrator storage-health summary. SMB has preview config, credentials, gateway authorization, and diagnostics, but this build still does not start a mountable SMB/Samba runtime; that track is deferred until `smboxide` is mature enough to integrate.

It is not yet a complete NAS appliance. The main missing areas are a mountable SMB runtime, full one-click restore workflows, permission inheritance/share-rule cleanup, richer quota/permission admin views, and fuller storage-pool visibility. Until external backups and the public-access security check flow are mature, MnemoNAS should not be treated as the only long-term copy of important data or exposed directly without an HTTPS reverse proxy.

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

The project is currently close to L1 and has the first baseline for the L1+ public-access entry point. The next stage is to harden that public-access security check flow before continuing the data-protection and LAN-compatibility work needed for L2.

## Priority Roadmap

### P0: Reliable Private File Cloud

Goal: make MnemoNAS safe enough for cautious real-world personal use, assuming the user already has external backups.

| Capability | Current state | Next step | Acceptance criteria |
| --- | --- | --- | --- |
| Backup and restore | Backup guide, local backup/restore-drill API, safe-directory local snapshot restore, safe-directory restic/rclone restore previews and restores, batch restore attention reasons, suggested next steps, selection, preview, pre-submit review, and execution, restore preflight, failed-preflight blocking, post-restore read-only verification, latest-restore recheck, post-restore cutover/rollback checklists, restore summary export and findings preview, restore-drill history, success-rate summaries, failure categorization, restore result history, matched restore-verification association, remote-retention auto-detection, scheduled restore-drill status and stale notifications, command-backed restic/rclone remote targets, Dashboard risk summaries, Web maintenance view, lightweight scheduling, automatic backup windows, local snapshot retention, and Webhook/Telegram/WeCom/DingTalk/SMTP backup/restore/restore-verification/restore-drill/retention-check notifications exist | Add broader fault-injection coverage and continue polishing the batch restore Web entry point | Local directory, external drive, or rclone/restic targets can be configured; the Dashboard and each job show latest backup, retention check, attention reasons, suggested next steps, restore verification, restore-summary findings, restore-drill history, restore-history list, and alert state |
| Deployment reliability | Docker and systemd paths exist | Formalize upgrade, rollback, and config migration | Release install, upgrade, uninstall, and data retention are covered by automated tests |
| HTTPS and security wizard | Reverse-proxy scripts, Traefik/Cloudflare Tunnel templates, Web wizard, security self-check API, public/certificate/HTTP-redirect doctor checks, renewal guidance, certificate-failure triage guidance, cloud-firewall checklist, and desktop/mobile E2E regression coverage exist | Expand more failure cases and mobile wizard coverage | Public domain, LAN self-signed, reverse-proxy headers, Secure/SameSite cookies, CSRF, download sessions, and internal dataplane ports are covered by automated tests and docs |
| Data integrity | Scrub, GC, and diagnostics exist; manual and scheduled Scrub runs write activity entries; failures, object anomalies, or incomplete result persistence send Webhook/Telegram/WeCom/DingTalk/SMTP notifications; scheduled Scrub has bounded failure retries, can be hot-updated from Web settings, and surfaces schedule state in health/diagnostics | Add native ZFS/Btrfs scrub coordination and deeper failure remediation guidance | Scrub failures are visible in UI, activity logs, and notifications |
| Secure defaults | Web session uses HttpOnly cookies, with login rate limiting and a user-session revocation action available | Expand admin security reminders and dangerous-configuration warnings | Weak passwords, default passwords, cross-site requests, suspicious logins, and public-share misconfiguration are blocked or warned clearly |
| WebDAV compatibility | Basic matrix exists | Expand Windows, macOS, and rclone regression coverage | Critical clients cover read, write, rename, delete, and recovery behavior |

### P1: Home NAS Baseline

Goal: cover the basic expectations for a LAN home NAS.

| Capability | Gap | Suggested implementation | Acceptance criteria |
| --- | --- | --- | --- |
| SMB/Samba | Preview config, credentials, gateway authorization, and diagnostics exist; current builds are not mountable | Defer runtime integration until `smboxide` is mature, or provide an official Samba sidecar while preserving permission and versioning boundaries | Windows and macOS can mount it directly; permission and path mapping do not bypass security boundaries; health and doctor output report runtime state accurately |
| Disk health | SMART JSON collection, temperature thresholds, lifetime/media-wear fields, missing-device detection, serial mismatch detection, health UI, activity logs, and Webhook/Telegram/WeCom/DingTalk/SMTP alerts exist | Add more USB/RAID bridge compatibility notes | UI shows disk health; anomalies are recorded in activity logs and notifications |
| Notifications | Webhook, Telegram, WeCom, DingTalk, and SMTP email cover disk-space, backup failures or warnings, explicit restore failures or warnings, post-restore read-only verification failures or warnings, restore-drill reminders, retention-check failures or warnings, disk-health anomalies, Scrub anomalies, login rate limits, Web/API quota denials, directory access or share policy changes, and enabled shares that are expiring soon; Web/API provides a saved-config `alert_test` test entry point | Support more operational or security event sources and additional notification channels as needed | Disk-full, backup-failed, restore-failed or warning, Scrub-failed, login-rate-limited, quota-exceeded, critical policy-change, and expiring-share events can notify users; administrators can send a test alert to verify saved channels |
| User quotas | User-level quotas, directory quotas, dynamic usage, user-management account-attention count breakdown, filtering, reason filtering, and copyable account-review summaries, quota attention stats, filtering, attention-first ordering, contact-, permission-, login-, and balance-aware summary copy, card states, user-card access-scope and review hints, copyable user-access review summaries, review-hint stats and filtering, user-list search, sorting, role/status filters, stat-card quick focus, context-rich current-list export, result summaries, and filter clearing, directory quota usage and status summaries, directory-quota attention list, filtering, and attention-first summary copy with remaining capacity, pre-save directory-quota change review, Web/API upload/copy/move/trash-restore user-quota enforcement for writes into `home_dir`, WebDAV PUT/COPY/MOVE user-quota enforcement for writes into `home_dir`, directory-quota enforcement for version restores, and Web/API quota-denial alerts exist | Add richer trend and permission-aware quota views | Uploads, copies, moves, trash restores, version restores, and WebDAV writes honor the applicable user or directory quotas with clear errors and notifications |

### P2: Small-Team Collaboration

Goal: make home or small-team collaboration more controllable and easier to trace when something goes wrong.

| Capability | Gap | Suggested implementation | Acceptance criteria |
| --- | --- | --- | --- |
| Folder-level permissions | Basic directory ACLs, groups, single-user effective access checks, unsaved-rule previews, per-path user matrix views, family/small-team policy presets, pre-save rule-change review, directory-access coverage summary, and related-share impact checks exist; more detailed bulk-application drilldown and review history are still missing | Add more detailed bulk-application drilldown and review history | Web, API, WebDAV, and shares use the same permission decision, and admins can review effective access |
| Share safety | Newly-created shares support default expiry, default access limits, family/temporary policy presets, pre-submit effective-policy review, directory-path policy enforcement for required passwords, max expiry, and max access, risk hints, pre-save share-policy change review, a share-policy coverage summary, soon-expiry reminders and notifications, review summaries, review filters, and one-click disable for high-risk links; per-member policy enforcement and richer review history are still missing | Add member policy constraints and richer review history | Admins can inspect, search, and disable all shares, and can find long-lived, soon-expiring, or over-broad public links |
| Activity review | Activity list, statistics overview, high-risk summary, concentrated-window review scoped to the high-risk group, current-page review details, current-page review disposition checklist, current-filter cross-page review records, persisted review records with disposition status, action counts, path samples, and user samples, batch follow-up view for review records, follow-up disposition status and note write-back, review-record CSV export, review-history filtering by reviewer/time/disposition status/linked activity, quick focus for follow-up reviews, trace entry points from individual activity rows and review records to related path, share, or high-risk activity, disposition entry points to version history, trash restore pages, and path-scoped share-disposition views, time-range, path/directory, share/high-risk group, action-type, and user filtering, a share-list handoff to matching path-scoped share activity, and administrator-only clear action exist | Add more direct disposition actions and result records | Key activity can be reviewed by time range, path/directory, user, and action type to trace accidental deletes, moves, or shares |
| Admin recovery tools | Versions, trash, version-restore pre-submit impact review and restore-result activity details, backup batch restore preflight, single-job pre-submit restore review, and single-job/batch restore impact summaries exist | Add cross-directory restore and richer conflict disposition records | Restore preview shows conflicts, overwrite impact, and permission impact |

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
