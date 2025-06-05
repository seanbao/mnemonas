# MnemoNAS Roadmap

English | [简体中文](roadmap.md)

This document tracks the product path from a self-hosted file cloud toward a practical home and small-team NAS. It guides development priorities, but it is not a release commitment. Scope may change based on data-safety requirements, maintenance cost, and user feedback.

## Current Assessment

As of 2026-05-09, MnemoNAS already includes Web file management, WebDAV, version history, trash, multi-user auth, sharing, activity logs, health checks, Scrub, GC, diagnostics export, Docker deployment, and systemd deployment. It is usable as a local private file cloud for personal or small-team evaluation, especially when the data is not the only copy.

The public-access track now has a first baseline: reverse-proxy scripts, `mnemonas-public-setup`, `mnemonas-doctor --public-domain`, the Web public-access wizard, the security self-check API, the Web settings flow, and E2E regression coverage. The backup track now has local jobs, command-backed restic/rclone remote targets, restore drills or remote checks, lightweight scheduling, automatic backup windows, retention, safe-directory local and rclone restores, Web maintenance status, and webhook failure notifications. It is not yet a complete NAS appliance. The main missing areas are SMB, full one-click restore workflows, disk health alerts, quotas, folder-level permissions, multi-channel notifications, and fuller storage-pool visibility. Until external backups and the public-access security loop are mature, MnemoNAS should not be treated as the only long-term copy of important data or exposed directly without an HTTPS reverse proxy.

## Product Positioning

MnemoNAS does not aim to clone all features of TrueNAS, Synology DSM, or Unraid. Its priority is to provide an auditable, migratable, native-file-first self-hosted storage entry point.

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
| L3 | Small-team NAS | Collaboration and auditability | Folder permissions, groups, share governance, audit reports, disaster recovery flow |

The project is currently close to L1 and has the first baseline for the L1+ public-access entry point. The next stage is to harden that public-access security loop before continuing the data-protection and LAN-compatibility work needed for L2.

## Priority Roadmap

### P0: Reliable Private File Cloud

Goal: make MnemoNAS safe enough for cautious real-world personal use, assuming the user already has external backups.

| Capability | Current state | Next step | Acceptance criteria |
| --- | --- | --- | --- |
| Backup and restore | Backup guide, local backup/restore-drill API, safe-directory local snapshot restore, safe-directory rclone restore, command-backed restic/rclone remote targets, Web maintenance view, lightweight scheduling, automatic backup windows, local snapshot retention, and webhook failure notifications exist; restic one-click restore orchestration is still missing | Add remote-retention guidance and restic restore workflows | Local directory, external drive, or rclone/restic targets can be configured; each job shows latest backup, restore verification, and alert state |
| Deployment reliability | Docker and systemd paths exist | Formalize upgrade, rollback, and config migration | Release install, upgrade, uninstall, and data retention are covered by automated tests |
| HTTPS and security wizard | Reverse-proxy scripts, Traefik/Cloudflare Tunnel templates, Web wizard, security self-check API, public/certificate/HTTP-redirect doctor checks, cloud-firewall checklist, and desktop/mobile E2E regression coverage exist | Add renewal guidance and stronger failure guidance | Public domain, LAN self-signed, reverse-proxy headers, Secure/SameSite cookies, CSRF, download sessions, and internal dataplane ports are covered by automated tests and docs |
| Data integrity | Scrub, GC, diagnostics exist | Add schedules and failure notifications | Scrub failures are visible in UI, activity logs, and notifications |
| Secure defaults | Web session uses HttpOnly cookies | Add login rate limiting, session revocation, admin security reminders, and dangerous-configuration warnings | Weak passwords, default passwords, cross-site requests, suspicious logins, and public-share misconfiguration are blocked or warned clearly |
| WebDAV compatibility | Basic matrix exists | Expand Windows, macOS, and rclone regression coverage | Critical clients cover read, write, rename, delete, and recovery behavior |

### P1: Home NAS Baseline

Goal: cover the basic expectations for a LAN home NAS.

| Capability | Gap | Suggested implementation | Acceptance criteria |
| --- | --- | --- | --- |
| SMB/Samba | Current access is mainly Web and WebDAV | Provide an official Samba integration path, starting with generated systemd/Docker configuration | Windows and macOS can mount it directly; permission and path mapping do not bypass security boundaries |
| Disk health | Storage stats exist; SMART is missing | Add SMART collection, temperature, bad disk, missing disk, and lifetime status | UI shows disk health; anomalies are recorded in activity logs and notifications |
| Notifications | Webhook covers disk-space and backup-failure/warning events; channel coverage is still limited | Support email, Telegram, WeCom-style channels, and more event sources | Disk-full, backup-failed, Scrub-failed, and suspicious-login events can notify users |
| User quotas | User isolation does not enforce capacity | Add user and directory quota models | Upload, WebDAV write, copy, and restore all honor quotas |

### P2: Small-Team Collaboration

Goal: make multi-user collaboration more controllable and auditable.

| Capability | Gap | Suggested implementation | Acceptance criteria |
| --- | --- | --- | --- |
| Folder-level permissions | Current model is role and home_dir oriented | Add directory ACLs, groups, and inheritance | Web, API, WebDAV, and shares use the same permission decision |
| Share governance | Share links exist | Add share policies, default expiry, download limits, and audit reports | Admins can inspect, search, and disable all shares |
| Audit reports | Activity logs exist | Add reports by user, directory, share, and suspicious action | Audit data can be exported and filtered by time range and action type |
| Admin recovery tools | Versions and trash exist | Add cross-directory restore and batch restore preflight | Restore preview shows conflicts, overwrite impact, and permission impact |

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
3. **Disk health and notifications**: surface failures before data loss.
4. **SMB/Samba integration**: cover the most common LAN file-sharing workflow.
5. **Quotas and permissions**: prevent long-running multi-user deployments from interfering with each other.
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
- Implementing a custom SMB or NFS protocol stack.
- Claiming long-term single-copy data safety without external backups.
- Recommending direct public-internet deployment that bypasses the security preflight, doctor checks, and HTTPS reverse proxy.
- Weakening default security boundaries for public anonymous sharing.

## Maintenance Rules

- Completed items should move to CHANGELOG or feature documentation rather than staying in the roadmap.
- If the implementation strategy changes, update acceptance criteria rather than only renaming the feature.
- Data-safety items should keep explicit failure modes and rollback strategies.
