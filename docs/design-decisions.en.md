# Design Decisions and Technology Choices

English | [简体中文](design-decisions.md)

This document records the main design choices behind MnemoNAS and the practical goals used to judge whether the project is ready for a public open-source release.

## Positioning

MnemoNAS is a private NAS for self-hosted deployments. It prioritizes data ownership, recoverability, and a polished daily-use Web experience over a large enterprise feature checklist.

Why MnemoNAS:

- Data ownership: no hard dependency on a cloud vendor.
- Portable storage: move the full storage root to migrate current files, versions, trash, and metadata.
- Usable Web UI: daily file work should feel like a focused product, not an operations dashboard.
- Recoverability: trash and version history are built into the storage path.
- Standard access: Web UI for humans, WebDAV for common clients, REST API for automation.
- Backup-friendly: the full storage root can be backed up with snapshots, restic, borg, rclone, or cold-copy workflows.

## Technology Choices

### Backend

| Component | Choice | Reason |
| --- | --- | --- |
| Control plane | Go 1.25.9+ | Simple deployment, mature HTTP ecosystem, strong concurrency support |
| Data plane | Rust 1.92+ | High performance and memory safety for storage-heavy logic |
| HTTP router | chi | Lightweight, standard-library friendly, predictable |
| WebDAV | `golang.org/x/net/webdav` plus local behavior | Mature base implementation with project-specific compatibility work |
| RPC | gRPC with grpc-go and tonic | Typed streaming interface between Go and Rust |
| Hashing | BLAKE3 | Fast content addressing and verification |
| Chunking | FastCDC in dataplane file APIs | Practical content-defined chunking is available in Rust; Go version storage currently uses whole-object CAS snapshots |
| Logging | zerolog and tracing | Structured logs with low overhead |

### Frontend

| Component | Choice | Reason |
| --- | --- | --- |
| Framework | React 19 + TypeScript | Familiar ecosystem and strong type support |
| Build tool | Vite | Fast development loop and simple production builds |
| Components | HeroUI | Accessible component base with theme support |
| Styling | Tailwind CSS 4 | Consistent utility-first styling without runtime cost |
| State | Zustand + TanStack Query | Lightweight local state plus server-state caching |
| Virtualization | TanStack Virtual | Large directory performance |
| Animation | Framer Motion | Controlled interaction polish |

The interface target is a calm file-management tool: compact, readable, and suitable for repeated use. It is not intended to look like a marketing page or a dense admin console.

## Architecture Decisions

### Native Current Files Plus CAS Versions

MnemoNAS stores current files as normal files under `files/` and stores historical versions in a content-addressed object store.

This avoids locking the user into a proprietary object-only layout for current data while still making versions whole-object deduplicated and verifiable.

### Loose Objects Before Packfiles

The current CAS layout stores one object per file path under a sharded directory tree:

```text
objects/
├── ab/
│   └── cd/
│       └── abcd1234567890...
```

Reasons:

- Simple crash-consistent writes using temporary files, `fsync`, and rename.
- Easy backup and inspection.
- Low implementation risk for an early release.

Packfiles may be introduced later if object count or small-object overhead becomes a real problem.

### WebDAV First

| Protocol | Decision | Rationale |
| --- | --- | --- |
| WebDAV | First supported mount protocol | Cross-platform and feasible to implement well enough for common clients |
| S3 | Future extension | Useful for tools, less natural as a mounted filesystem |
| SMB/Samba | Preview config only | The gateway contract exists, but this build does not start an SMB runtime |
| NFS | Not in current scope | Permission and deployment boundaries are high for a small project |

## Release Quality Targets

| Area | Target |
| --- | --- |
| Installation | A new user can reach the Web UI and WebDAV in 10-15 minutes |
| Mounting | Common macOS, Windows, Linux, mobile, and rclone clients have a documented path |
| Large files | Streaming download, Range requests, and large upload paths are covered |
| Data trust | Scrub reports missing or corrupted objects |
| Recovery | Trash and version restore work from the Web UI/API |
| Diagnostics | A diagnostic bundle can be exported for troubleshooting |
| Crash consistency | Kill/restart tests do not leave half-written current files |
| Authentication | Web UI users, roles, passwords, and login history are implemented |
| Sharing | Passwords, expiration, and public access flow are implemented |
| Activity | Important user operations are traceable |

## Scope Boundaries

Current non-goals:

- SMB/NFS protocol stacks. SMB gateway config, credentials, and authorization are preview scaffolding only and are not mountable in this build.
- RAID, filesystem creation, disk formatting, or volume management.
- Multi-node consistency.
- Treating deduplication as the core user-facing feature.

Data safety promises:

- Application crashes should not silently corrupt current files.
- Scrub should be able to report missing or corrupted CAS objects.
- Common accidental deletion and modification should be recoverable through trash or versions.

Not promised:

- Redundancy against physical disk failure on a single disk.
- Automatic disk partitioning or formatting.
- Replacement for a 3-2-1 backup strategy.

## Competitive Framing

MnemoNAS is not trying to out-feature full NAS operating systems such as TrueNAS or OpenMediaVault. The target is a smaller, more focused self-hosted storage product that is easy to deploy, pleasant to use, and explicit about data recovery paths.

Compared with backup tools such as restic or borg, MnemoNAS is interactive file management first. Compared with object stores such as MinIO or Ceph, it is a private NAS first.

## Related Documents

- [Architecture](architecture.en.md)
- [Storage internals](storage-internals.en.md)
- [Backup guide](backup-guide.en.md)
- [Extension points](extension-points.en.md)
