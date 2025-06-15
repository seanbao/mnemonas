# MnemoNAS Roadmap

English | [简体中文](roadmap.md)

This document tracks the product path from a self-hosted file cloud toward a practical home and small-team NAS. It guides development priorities, but it is not a release commitment. Scope may change based on data-safety requirements, maintenance cost, and user feedback.

## Current Assessment

As of 2026-05-19, MnemoNAS already includes Web file management, WebDAV, version history, trash, multi-user auth, user groups, user-level quotas, directory access rules, sharing, activity logs, health checks, Scrub, GC, diagnostics export, Docker deployment, and systemd deployment. It is usable as a local private file cloud for personal or small-team evaluation, especially when the data is not the only copy.

The public-access track now has a first baseline: reverse-proxy scripts, `mnemonas-public-setup`, `mnemonas-doctor --public-domain`, the Web public-access wizard, the security self-check API, certificate-renewal guidance, certificate-failure triage guidance, the Web settings flow, and E2E regression coverage. The backup track now has local jobs, command-backed restic/rclone remote targets, restore drills or remote checks, restore-drill history, success-rate summaries, failure categorization, lightweight scheduling, automatic backup windows, retention checks, safe-directory local/restic/rclone restores, batch restore preview and execution, post-restore read-only verification, post-restore cutover checklists, restore history, stale restore-drill reminders, Web maintenance status, and Webhook/Telegram/SMTP failure notifications. Disk health now has `smartctl`-based SMART, temperature, missing-device, and serial-drift checks wired into the health page, diagnostic summaries, activity logs, and Webhook/Telegram/SMTP events. Quotas now have an admin user-quota UI/API, dynamic `used_bytes`, directory quota configuration, directory quota usage summaries, server-side hard limits for non-admin Web/API uploads, copies, trash restores, and WebDAV users-mode PUT/COPY writes, plus directory quota enforcement for matching Web/API and WebDAV upload, copy, move, and restore-style writes. Permissions now have user groups, directory access rules shared by Web/API, WebDAV users mode, search, shares, favorites, trash, and activity filtering, plus effective access checks in the Settings API and Web settings page. Storage visibility now includes filesystem capacity, filesystem type, mount point, and device/dataset source on the storage page. SMB now has preview config, credentials, gateway authorization, and diagnostics, but this build still does not start a mountable SMB/Samba runtime; this track is deferred until `smboxide` is mature enough to integrate. It is not yet a complete NAS appliance. The main missing areas are a mountable SMB runtime, full one-click restore workflows, policy inheritance/share governance, more notification event sources, richer quota/permission admin views, and fuller storage-pool visibility. Until external backups and the public-access security loop are mature, MnemoNAS should not be treated as the only long-term copy of important data or exposed directly without an HTTPS reverse proxy.

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
| L3 | Small-team NAS | Collaboration with traceable sharing | Folder permissions, groups, share governance, activity review, disaster recovery flow |

The project is currently close to L1 and has the first baseline for the L1+ public-access entry point. The next stage is to harden that public-access security loop before continuing the data-protection and LAN-compatibility work needed for L2.

## Priority Roadmap

### P0: Reliable Private File Cloud

Goal: make MnemoNAS safe enough for cautious real-world personal use, assuming the user already has external backups.

| Capability | Current state | Next step | Acceptance criteria |
| --- | --- | --- | --- |
| Backup and restore | Backup guide, local backup/restore-drill API, safe-directory local snapshot restore, safe-directory restic/rclone restore previews and restores, batch restore preview and execution, restore preflight, failed-preflight blocking, post-restore read-only verification, post-restore cutover/rollback checklists, restore summary export, restore-drill history, success-rate summaries, failure categorization, restore result history, remote-retention auto-detection, scheduled restore-drill status and stale notifications, command-backed restic/rclone remote targets, Web maintenance view, lightweight scheduling, automatic backup windows, local snapshot retention, and Webhook/Telegram/SMTP failure notifications exist | Add broader fault-injection coverage and continue polishing the batch restore Web entry point | Local directory, external drive, or rclone/restic targets can be configured; each job shows latest backup, retention check, restore verification, restore-drill history, restore history, and alert state |
| Deployment reliability | Docker and systemd paths exist | Formalize upgrade, rollback, and config migration | Release install, upgrade, uninstall, and data retention are covered by automated tests |
| HTTPS and security wizard | Reverse-proxy scripts, Traefik/Cloudflare Tunnel templates, Web wizard, security self-check API, public/certificate/HTTP-redirect doctor checks, renewal guidance, certificate-failure triage guidance, cloud-firewall checklist, and desktop/mobile E2E regression coverage exist | Expand more failure cases and mobile wizard coverage | Public domain, LAN self-signed, reverse-proxy headers, Secure/SameSite cookies, CSRF, download sessions, and internal dataplane ports are covered by automated tests and docs |
| Data integrity | Scrub, GC, and diagnostics exist; manual and scheduled Scrub runs write activity entries; failures, object anomalies, or incomplete result persistence send Webhook/Telegram/SMTP notifications; scheduled Scrub has bounded failure retries, can be hot-updated from Web settings, and surfaces schedule state in health/diagnostics | Add native ZFS/Btrfs scrub coordination and deeper failure remediation guidance | Scrub failures are visible in UI, activity logs, and notifications |
| Secure defaults | Web session uses HttpOnly cookies, with login rate limiting and a user-session revocation action available | Expand admin security reminders and dangerous-configuration warnings | Weak passwords, default passwords, cross-site requests, suspicious logins, and public-share misconfiguration are blocked or warned clearly |
| WebDAV compatibility | Basic matrix exists | Expand Windows, macOS, and rclone regression coverage | Critical clients cover read, write, rename, delete, and recovery behavior |

### P1: Home NAS Baseline

Goal: cover the basic expectations for a LAN home NAS.

| Capability | Gap | Suggested implementation | Acceptance criteria |
| --- | --- | --- | --- |
| SMB/Samba | Preview config, credentials, gateway authorization, and diagnostics exist; current builds are not mountable | Defer runtime integration until `smboxide` is mature, or provide an official Samba sidecar while preserving permission and versioning boundaries | Windows and macOS can mount it directly; permission and path mapping do not bypass security boundaries; health and doctor output report runtime state accurately |
| Disk health | SMART JSON collection, temperature thresholds, lifetime/media-wear fields, missing-device detection, serial mismatch detection, health UI, activity logs, and Webhook/Telegram/SMTP alerts exist | Add more USB/RAID bridge compatibility notes | UI shows disk health; anomalies are recorded in activity logs and notifications |
| Notifications | Webhook, Telegram, and SMTP email cover disk-space, backup-failure/warning, restore-drill reminders, disk-health anomalies, Scrub anomalies, login rate limits, and Web/API quota denials; event-source coverage is still limited | Support WeCom-style channels and more event sources such as permission changes | Disk-full, backup-failed, Scrub-failed, login-rate-limited, and quota-exceeded events can notify users |
| User quotas | User-level quotas, directory quotas, dynamic usage, directory quota usage summaries, Web/API upload/copy/move/trash-restore/version-restore enforcement, WebDAV PUT/COPY/MOVE enforcement, and Web/API quota-denial alerts exist | Add richer admin views, trends, and permission-aware quota views | Upload, copy, move, restore, and WebDAV writes honor user or directory quotas with clear errors and notifications |

### P2: Small-Team Collaboration

Goal: make home or small-team collaboration more controllable and easier to trace when something goes wrong.

| Capability | Gap | Suggested implementation | Acceptance criteria |
| --- | --- | --- | --- |
| Folder-level permissions | Basic directory ACLs, groups, single-user effective access checks, unsaved-rule previews, per-path user matrix views, and related-share impact checks exist; policy presets and friendlier activity review are still missing | Add family/small-team sharing presets and friendlier activity review | Web, API, WebDAV, and shares use the same permission decision, and admins can review effective access |
| Share safety | Newly-created shares support default expiry, default access limits, family/temporary policy presets, directory-path policy enforcement for required passwords, max expiry, and max access, risk hints, soon-expiry reminders, review filters, and one-click disable for high-risk links; per-member policy enforcement and richer review history are still missing | Add member policy constraints, expiry notifications, and richer review history | Admins can inspect, search, and disable all shares, and can find long-lived, soon-expiring, or over-broad public links |
| Activity review | Activity logs exist | Add home/small-team views by user, directory, share, and unusual action | Key activity can be reviewed by time range and action type to trace accidental deletes, moves, or shares |
| Admin recovery tools | Versions, trash, and backup batch restore preflight exist | Add cross-directory restore, permission-impact preview, and clearer conflict descriptions | Restore preview shows conflicts, overwrite impact, and permission impact |

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
- Shipping a custom SMB or NFS protocol stack before permission, version-history, trash, and activity-history boundaries are closed.
- Claiming long-term single-copy data safety without external backups.
- Recommending direct public-internet deployment that bypasses the security preflight, doctor checks, and HTTPS reverse proxy.
- Weakening default security boundaries for public anonymous sharing.

## Maintenance Rules

- Completed items should move to CHANGELOG or feature documentation rather than staying in the roadmap.
- If the implementation strategy changes, update acceptance criteria rather than only renaming the feature.
- Data-safety items should keep explicit failure modes and rollback strategies.
