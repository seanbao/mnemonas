# Storage Internals and Operations Guidance

English | [简体中文](storage-internals.md)

This document explains MnemoNAS storage architecture, how it differs from traditional NAS systems, and what filesystem setups are recommended.

## Overview

MnemoNAS uses a hybrid layout:

- Current user files are stored as normal files.
- Historical versions are stored as content-addressed objects.
- SQLite stores metadata for versions, trash, locks, and indexes.

```text
+---------------------------------------------------------+
|                  MnemoNAS application                   |
| WebDAV/API -> storage layer -> versions -> SQLite       |
+---------------------------------------------------------+
|                     Storage root                        |
| files/      current user files                          |
| .mnemonas/  metadata, CAS objects, trash                |
+---------------------------------------------------------+
|                Underlying filesystem                    |
| ext4 / XFS / Btrfs / ZFS / APFS / NTFS                  |
+---------------------------------------------------------+
|                    Physical media                       |
| single disk / mirror / RAID / remote backup             |
+---------------------------------------------------------+
```

Design goals:

- Keep current files readable without special software.
- Keep version history separate from current files.
- Use transactional metadata where consistency matters.
- Make full-root backup and migration straightforward.

## Directory Layout

Default storage root:

```text
~/.mnemonas/
├── files/
│   ├── documents/
│   │   └── report.docx
│   └── photos/
│       └── vacation.jpg
└── .mnemonas/
    ├── index.db
    ├── objects/
    │   └── ab/
    │       └── cd/
    │           └── abcd1234...
    └── trash/
        ├── .deleting/
        │   ├── purge-{operation-id}.prepared.json
        │   ├── purge-{operation-id}.committed.json
        │   └── purge-{operation-id}.item
        ├── .transactions/
        │   ├── transfer-{operation-id}.prepared.json
        │   ├── transfer-{operation-id}.copying.json
        │   ├── transfer-{operation-id}.ready.json
        │   ├── transfer-{operation-id}.committed.json
        │   ├── transfer-{operation-id}.completed.json
        │   └── transfer-{operation-id}.item
        └── {trash-id}/
            └── content
```

## Native Current Files

Current files are ordinary files under `files/`.

Benefits:

- Users with OS-level directory access can read current files directly.
- Offline migration and backup are easier to reason about.
- The current version is not locked inside a proprietary object layout.

Important boundary:

- Reading under `files/` is safe.
- Writing or deleting under `files/` while bypassing MnemoNAS does not create versions, trash records, activity logs, or metadata updates.
- Full restore of versions, trash, and indexes requires `.mnemonas/` as well as `files/`.

## CAS Objects

Historical content is stored in a content-addressed store:

```text
objects/
├── ab/
│   └── cd/
│       └── abcd1234567890...
```

Properties:

- BLAKE3 hash addressing.
- Identical content can reuse the same object.
- Reads verify hash integrity.
- Writes use temporary files, `fsync`, and rename for crash consistency.
- zstd compression is supported for object payloads when it reduces size.

## SQLite Metadata

`index.db` stores metadata such as:

| Table | Purpose |
| --- | --- |
| `files` | File index data |
| `versions` | Version history |
| `versioning_overrides` | Per-file version policy overrides |
| `trash` | Trash metadata |
| `trash_operations` | Durable Trash transfer outbox |
| `file_locks` | WebDAV lock state |

SQLite gives MnemoNAS ACID transactions, indexes, and a portable metadata file.

## Trash

When Trash is enabled, deleted files are moved into `.mnemonas/trash/` with metadata in SQLite. The metadata records original path, deletion time, persisted expiry, and content location. When Trash is disabled, deletion is immediately permanent.

Trash expiry and size limits are configured under `[storage.trash]`. `[storage.retention].gc_interval` drives one background sweep for both expired file versions and expired Trash items. A value of `0` disables both periodic cleanup paths without disabling capacity cleanup, explicit permanent deletion, or empty-Trash operations.

Each Trash item receives a random ID when created, and that ID remains immutable for the item's lifetime. Exact emptying loads the current Trash state once under one storage write lock and preflights access rules for the restored logical paths of every selected item that still exists, including descendants, before any deletion occurs. A preflight failure leaves every selected item unchanged. After preflight succeeds, items are deleted in request-ID order. Unselected items, including items added after the operation begins, remain unchanged, and selected IDs that no longer exist are skipped. If a hard execution failure occurs, selected items that still exist and have not been processed remain unchanged. The deleted, remaining, and skipped result sets form a complete, non-overlapping partition of the original request and each preserves request order.

### Permanent Trash-Item Deletion Recovery Journal

Explicit permanent deletion, exact emptying, expiry cleanup, and capacity cleanup share a durable decision journal under `.mnemonas/trash/.deleting/` when they permanently remove an item that is already in Trash. If a Trash item contains share or favorite restore data, the server validates the canonical payload, original path, participant stores, and recovery evidence without mutation before scanning content or publishing a journal. A validation failure leaves the content, SQLite metadata, and journal state unchanged. After preflight, the server writes and syncs `purge-{operation-id}.prepared.json`. The record contains the complete Trash metadata, a path-sorted content manifest, and the version-object hashes that may require cleanup. A no-replace rename then moves the content to `purge-{operation-id}.item` in the same directory. After removing the SQLite Trash metadata, the server writes and syncs a matching `purge-{operation-id}.committed.json` before recursively removing the stage, version metadata, and unreferenced version objects. After physical and version cleanup, the server uses the original delete operation ID in the restore payload to remove exact completed share and favorite ownership. It removes the purge journals only after that persistence barrier succeeds.

Startup recovery runs before the retention sweeper, workspace-stage cleanup, and network listener start. Before any recovery mutation, it globally preflights the participants for every `committed` operation with restore data; one failure blocks every `prepared` rollback and `committed` roll-forward in that pass. An operation with only a `prepared` decision rolls back after validating the complete manifest: it restores the content to the canonical Trash path and restores missing SQLite metadata. Recovery completes every `prepared` rollback found in the scan before starting any `committed` roll-forward. A failed rollback or canceled context blocks the entire committed phase so shared version-ownership evidence is restored before version cleanup. An operation with a `committed` decision only rolls forward: it removes any remaining Trash metadata and staged content, completes version cleanup, clears exact participant ownership through a persistence barrier, and finally removes the decision journals. The first participant persistence warning is retried once. A persistent warning or hard failure retains the `committed` journal and activates the recovery gate. Manifest entries that are already absent are treated as completed parts of a roll-forward; extra entries, same-name replacements, and identity changes are not removed. A `committed` decision is never rolled back into a restorable Trash item.

If journal filenames, operation IDs, decision pairs, manifests, staged content, and SQLite metadata cannot be verified safely against one another, recovery fails closed and prevents writable startup while preserving the relevant paths for manual inspection. A runtime rollback failure also blocks later storage mutations until recovery succeeds. A recognized orphan `.item` without a decision journal blocks startup recovery. Unrecognized legacy residue, temporary files, and other untracked paths are reported and preserved; a filename pattern alone never authorizes automatic deletion.

This journal covers only permanent deletion of an item that is already in Trash. Live delete-to-Trash and restore-from-Trash operations use the separate protocol below.

### Live Trash Transfer Recovery Journal

Live delete-to-Trash and restore-from-Trash operations use durable sidecar checkpoints under `.mnemonas/trash/.transactions/`. As execution progresses, an operation may publish `prepared`, `copying`, `ready`, `committed`, and `completed` records in that order and may retain a private `transfer-{operation-id}.item` replica or a source-local `.mnemonas-trash-transfer-{operation-id}.stage`. Before publishing `copying`, each new operation-owned container and each parent directory created for a restore contains a canonical mode-`0600` `.mnemonas-trash-transfer-owner-{operation-id}` marker. If the derived name matches any parent component of the restore target, the operation-ID allocator rejects that candidate and generates another. The marker binds the `prepared` journal hash, operation ID, role, exact relative path, and persistent identity. The `copying` checkpoint durably records the synced container and parent identities before payload copying. Payload copying starts only after the markers have been removed and their directories synced. Markers never enter the final Trash content or restore destination. The server may publish `ready` only after the replica is complete and its full manifest has been verified. The records contain the storage-root identities, logical paths, source and replica manifests, and the participant payload. Their SHA-256 journal hash excludes the decision field so matching checkpoint bodies bind to the same operation evidence.

The SQLite `trash_operations` outbox row is written atomically with Trash metadata and file-index changes. It binds the operation ID, Trash ID, operation kind, participant payload, and journal hash. Share and favorite participants persist operation-scoped receipts and original delete ownership in their configured stores. Replaying the same operation is idempotent. Explicit share updates or deletion and favorite mutations after delete completion block an older restore from overwriting the newer intent. Reaching `completed` finishes only the transfer receipt; delete ownership remains until the corresponding Trash item is restored or permanently purged. A purge removes ownership only by the exact original delete operation ID in its journal payload.

A preflight-valid `prepared`, `copying`, or `ready` chain without a matching outbox row rolls back. `prepared` recovery removes newly created containers and parent directories only when a complete matching owner marker proves ownership. If a crash occurs after private-container creation but before marker creation, recovery automatically reclaims only an empty mode-`0700` container at the exact operation path. Restore parent directories without markers are retained while the journal rolls back. A partially written, corrupt, non-canonical, or mismatched marker, or a container with an unknown entry, cannot prove ownership; recovery preserves the evidence and requires manual reconciliation. Recovery of `copying` removes only a partial replica whose identity and manifest prove that it belongs to the operation. A `ready` chain with a matching row, and every preflight-valid `committed` chain, rolls forward. A `completed` chain never replays participant Apply; recovery verifies the filesystem postcondition and retries only receipt completion, outbox acknowledgement, and journal cleanup. Invalid checkpoint chains, missing or mismatched outbox rows, unreliable participant evidence, untracked owned stages, and identity or manifest drift fail closed and preserve the evidence for inspection.

Startup first initializes the share and favorite participant stores and hooks used by the API. It then recovers permanent deletions under `.deleting`, followed by live transfers under `.transactions`. Both recovery passes finish before workspace-stage cleanup, background tasks, and the network listener start. A recovery failure prevents writable service startup. Runtime failures that require recovery block subsequent storage mutations until recovery succeeds. If a terminal journal was removed but its parent-directory sync failed, the server makes a best-effort attempt to repersist the `completed` journal; the equivalent rollback uncertainty repersists a canonical `prepared` journal. Both cases activate the recovery gate. When the visible mutation was already committed, the current request may still return a persistence warning, but capacity cleanup and new storage mutations remain blocked until recovery confirms the terminal state and removes the journal.

When a corrupt share or favorite persistence file is isolated, the server writes a `<store-file>.recovery-required` marker next to that store. The marker preserves the unreliable-participant-evidence state across process restarts; a missing or regenerated main store file does not clear it automatically. An operator may remove the marker and rerun startup recovery only after reconciling the isolated copy, backups, and pending Trash journals.

Journal recovery uses a persistent identity composed of device number, inode, and object type, and validates permissions, size, and file content hashes separately. Linux and macOS provide this identity across a rename within one filesystem and support the handle-relative per-entry removal used by recovery. Other platforms reject the operation before publishing a purge record, moving a Trash item, or changing its metadata. A remount, snapshot restore, or volume transfer can change the device number. In that case, recovery rejects the evidence as the original object even if the inode and content still match, and manual inspection is required.

Safe recovery also requires one `nasd` writer per storage root at any time. The MnemoNAS storage lock is process-local and does not provide cross-process single-writer arbitration. Running multiple `nasd` instances against the same storage root concurrently is unsupported.

On Linux and macOS, workspace metadata reads derive an opaque object-identity token from the device, inode, ctime, type and permission bits, size, and nanosecond modification time. REST delete confirmation requires the identity token from the current list. The server samples the complete delete policy and mutation epoch while briefly holding the storage read lock, then releases the lock and scans and hashes the entire requested target batch outside the lock. After the scan, the server validates the mutation epoch again. If it changed, the server discards the entire batch result and performs bounded retries, using a read-locked fallback scan when necessary. The root callback checks write access, the nested-mount boundary below the workspace root, file type, and observed identity in that order; an identity mismatch does not read content or traverse a directory. After the root passes, the traversal applies the same access, mount-boundary, and type checks to later entries, hashes regular files through a no-follow read path, and streams node digests into the v3 target token in name-sorted depth-first order. Nested mount points, symlinks, FIFOs, Unix sockets, and other special files in a target tree are rejected immediately, preventing an incomplete target token and avoiding blocking during open. The boundary comes from the host mount table, so it also detects bind mounts on the same device. Both a tree containing a mount point and a target located inside a mount point are rejected. The v3 target token uses a hierarchical SHA-256 Merkle representation. The final token binds the canonical root path and complete path hierarchy. Each node digest binds a file-or-directory domain separator, the full permission mode, size, nanosecond modification time, object identity, and the snapshot content-hash field; for a regular file, that field carries the actual content digest. A directory node sorts children by name and combines each child name and digest in that order. The final target token remains a 64-character lowercase hexadecimal string that callers must treat as opaque. Directories and empty directories also participate. Prepare retains only the root metadata required for the REST response and the target token; it does not retain a complete target-tree manifest. One confirmation request accepts at most 1000 non-nested targets. When a platform cannot provide the required object identity, the list returns `null` and the server rejects observed delete-intent creation. Without contention, each target is still scanned once and each regular file is still hashed once. Contention can add scans and hashes from discarded attempts. The mutation epoch is not included in the v3 target token or exposed through REST requests or responses; the existing REST shapes remain unchanged.

During deletion, complete-policy comparison, per-entry write-access revalidation, target-token comparison, and mutation execute under one storage write lock. Policy comparison precedes target-tree traversal, so a stale policy cannot trigger a full-tree read. A delete-mode, retention-period, sweep-interval, capacity-limit, or target-tree change detected before object capture does not commit workspace, index, version, share, favorite, Trash, or activity changes. A confirmed target that disappeared or whose parent is no longer a directory is also treated as target drift. After object capture begins, a failed path may perform a no-replace rollback and may change object ctime or parent-directory timestamps. WebDAV conditional deletion first revalidates write access for the complete target tree under the same write lock, then reads the relevant target attributes and evaluates the condition; only ETag-dependent conditions calculate a content hash.

After access checks, target-token comparison, or WebDAV conditions pass, deletion does not resolve the object again from its original logical path. The server first opens the root through a no-follow handle as a witness, compares the handle identity with the root identity in the snapshot verified by the current request, and captures a content hash for a regular-file witness when the snapshot does not already contain one. It then uses an atomic no-replace rename to capture the current leaf at a random stage under the same parent on the source filesystem. The staged object must identify the same object as the witness. If an unknown same-name object replaces the staged path before revalidation, the server records an independent recovery copy in the residual path only when the still-open regular-file witness can be copied, both the file and parent directory can be synced, and the copy can be revalidated against both path identity and that hash. The unknown object remains in place. A directory witness or a regular-file recovery that cannot be synced and verified does not label the unknown stage path or unconfirmed copy as recovery evidence. `StagePath` remains empty, while `InspectionPaths` and the error log identify the actual unknown locations for manual inspection. The complete staged tree is mapped back to the original logical paths and rechecked for entry set, type, size, modification time, descendant identity, and file content. File hashing also verifies before and after the read that the open handle and staged path still identify the same object. Because rename can change the root object's ctime, the pre-rename root identity is retained only after `os.SameFile` succeeds; descendant identities are still compared from their staged state.

After each regular-file copy is published, the copy path keeps the destination file handle open, checks the copied byte count, and hashes the published destination completely against the digest accumulated during the copy read. The handle and destination path must retain the same identity before and after that hash. A complete copy proof covers paths, types, sizes, permissions, object identities, and hashes, and metadata-only traversals revalidate both trees and their mount boundaries before the durable business-state commit.

For live delete-to-Trash, the private replica is published at its canonical Trash path only after the `ready` checkpoint. The SQLite commit then updates Trash metadata and indexes and inserts the outbox row atomically. After the committed participant state is applied, the server revalidates the canonical Trash replica immediately before checked, handle-relative removal of the source stage. A hard failure preserves the journal and outbox evidence for roll-forward recovery and blocks later storage mutations; it is not converted into a cleanup warning. Permanent deletion continues to move a verified source stage into a random quarantine with permissions no broader than mode `0700` and remove entries relative to a server-held directory handle. An unknown entry, same-name replacement, identity change, or newly observed mount stops permanent cleanup and retains the quarantine content.

For one successful regular-file deletion without contention, the live Trash path across delete confirmation and commit performs twelve complete content-hash passes plus one copy read: two over the live file, four over the source stage, and six over the Trash destination. The permanent-delete path performs five complete content-hash passes: two over the live file and three over the source stage. Contention, failure handling, or rollback can add file reads. Only the witness-recovery branch for a replaced post-rename stage restricts `StagePath` to a verified recovery copy. When that copy cannot be confirmed, `StagePath` remains empty and `InspectionPaths` lists the actual unknown stage and unconfirmed copy locations. At other failure stages, `StagePath` identifies a retained internal residue location that still requires manual review against the error cause and filesystem evidence.

Generic cross-root regular-file moves used by other internal migration paths check the source path's initial identity, open-handle identity, copied byte count, post-copy handle state, and current path identity. They use `.mnemonas-move-*.tmp` and `.mnemonas-remove-*` isolation paths and remove only objects that still match the copy proof. Trash restore instead creates and syncs a dedicated workspace transfer container, writes `copying`, and then copies the canonical Trash source into that container's payload path. After full replica verification it writes `ready`, publishes the destination without replacement, and commits the Trash, index, version, override, and lock changes with the outbox row. Immediately before removing the canonical Trash source, it revalidates the complete destination manifest. Destination drift blocks recovery and preserves the Trash source when it still exists.

Trash deletion copies only from the verified stage and retains that source stage through the durable business-state commit. The source is removed only after the committed participant state is applied and the canonical Trash replica is revalidated. The same flow applies on one filesystem and across `EXDEV`. Before commit, rollback uses a no-replace rename from the verified stage to the original path; after commit, recovery only rolls forward. Permanent deletion also removes only its stage and never deletes by resolving the original logical path again. A new object created at the original path during deletion is not copied, overwritten, or removed. If rollback cannot be verified, the new object remains unchanged and REST returns `500 Internal Server Error` without recording delete activity.

In permanent mode, a physical-cleanup failure after logical commit returns `200 OK` for REST or `204 No Content` for WebDAV with `delete cleanup incomplete`; the logical path remains deleted. In live Trash mode, only explicitly classified persistence-only participant or receipt failures return success with `workspace mutation persistence incomplete`. Capacity cleanup runs after the transfer reaches `completed`; its failure returns success with `trash delete cleanup incomplete`. A hard journal, participant, receipt, outbox, source, or destination failure returns `500 Internal Server Error`, may occur after durable commit, preserves recovery evidence, and activates the storage mutation gate. A completed operation is recovered by retrying completion only, without replaying participant Apply.

This atomic boundary and the mutation epoch cover only operations serialized through the MnemoNAS storage lock. Direct filesystem writes by another process with the same UID and concurrent mounts by a privileged process neither advance the epoch nor obey the storage lock. Object-identity, mount-boundary, staged-object, and recovery-safety checks reject observed changes, but a change between the last verification and a filesystem call remains outside the application-level transaction boundary. The `.deleting` journal covers process crashes and power loss during permanent deletion of an item already in Trash; `.transactions`, `trash_operations`, and participant receipts cover those failures during live delete-to-Trash and restore-from-Trash when their evidence remains valid. The server does not move or delete an unknown replacement or automatically remove unverified `.mnemonas-delete-*`, `.mnemonas-trash-transfer-*`, `.mnemonas-move-*`, `.mnemonas-remove-*`, or internal Trash residue. `InspectionPaths` and the server log identify known locations. Residue outside a verified recovery chain requires manual review using those paths and filesystem evidence.

Deletion-specific traversal does not change ordinary directory traversal, search, or file-count behavior. The deletion path rereads the mount table for the intent snapshot, access revalidation, Trash target description, cross-root copying, and checked removal. A read failure or invalid mount path rejects continued processing as `ErrNotRegular`. Before capture, REST and WebDAV map it to `409 Conflict`. In permanent mode, a captured object that cannot be rolled back is a recovery residual, while a failure after logical commit is a cleanup warning. In live Trash mode, a hard failure after checkpoint publication retains the durable evidence, returns `500`, and requires recovery before later storage mutations. A copied destination is removed only while its boundary remains verifiable; otherwise the internal copy is preserved to avoid crossing a new mount. Permanent Trash-item staging and recursive removal follow the same boundary rule.

Each Trash item receives its expiry when it is deleted; later configuration changes do not recalculate it. Capacity pressure can still remove older items before expiry, so the expiry is not a guaranteed minimum retention period.

## Versioning Policy

MnemoNAS automatically versions files where history is usually valuable:

| File Type | Default | Reason |
| --- | --- | --- |
| Text, Markdown, office documents | Versioned | Frequently edited |
| Config and source files | Versioned | Changes should be traceable |
| Images | Not versioned by default | Large and usually append-only |
| Videos | Not versioned by default | Very large |
| Files over default size limit | Not versioned by default | High storage cost |

Retention example:

```toml
[storage.retention]
max_versions = 50
max_age = "2160h"
```

Version APIs:

The restore URL `path` query value should be encoded in copyable examples. For example, `/documents/report.docx` is sent as `%2Fdocuments%2Freport.docx`.

```bash
MNEMONAS_ACCESS_TOKEN="<access-token>"
curl_auth_config="$(mktemp)"
trap 'rm -f "$curl_auth_config"' EXIT
chmod 600 "$curl_auth_config"
printf 'header = "Authorization: Bearer %s"\n' "$MNEMONAS_ACCESS_TOKEN" > "$curl_auth_config"

curl --config "$curl_auth_config" \
  http://localhost:8080/api/v1/versions/documents/report.docx

curl -X POST \
  --config "$curl_auth_config" \
  "http://localhost:8080/api/v1/versions/abc123.../restore?path=%2Fdocuments%2Freport.docx"
```

## Comparison with Traditional NAS

| Area | MnemoNAS | Traditional NAS | Pure CAS System |
| --- | --- | --- | --- |
| Current files | Native files | Native files | CAS objects |
| Version storage | CAS objects | Filesystem snapshots | CAS objects |
| Current-file readability | Directly readable | Directly readable | Needs special software |
| Deduplication | BLAKE3 whole-object versions; CDC file APIs are available in dataplane, but current version history does not reference-count CDC chunks | Filesystem dependent | Core feature |
| Metadata | SQLite | Filesystem and app metadata | JSON/DB |
| Complexity | Medium | Low for simple file sharing | High |

The hybrid approach trades some purity for recoverability and user inspection.
Current version history uses whole-object CAS snapshots; the FastCDC API is a dataplane capability and does not mean chunk-level version deduplication is enabled.

## Filesystem Compatibility

MnemoNAS does not require a specific filesystem.

| Filesystem | Compatibility | Recommendation | Notes |
| --- | --- | --- | --- |
| ext4 | Supported | Good | Stable Linux default |
| XFS | Supported | Good | Strong for large files and concurrency |
| Btrfs | Supported | Very good | Snapshots and scrub add a protection layer |
| ZFS | Supported | Best | Mirror, scrub, compression, strong operational model |
| NTFS | Supported | Limited | Works in Windows contexts |
| APFS | Supported | Good | Works in macOS contexts |
| exFAT | Not recommended | Poor | Weak atomicity expectations |
| NFS mount | Supported with caution | Limited | Watch latency and consistency behavior |

## Recommended Configurations

| Scenario | Setup | Data Safety | Cost |
| --- | --- | --- | --- |
| Budget | Single ext4 disk + cloud backup | Basic | Low |
| Recommended mirror | ZFS mirror, 2 disks | Strong | Medium |
| Advanced Linux | Btrfs RAID1 | Strong | Medium |
| Compatibility first | mdadm RAID1 + ext4 | Strong | Medium |
| Large capacity | ZFS RAIDZ1 or RAIDZ2 | Stronger | Higher |

### ZFS Mirror

```bash
sudo zpool create mnemonas mirror /dev/sda /dev/sdb
sudo zfs set mountpoint=/srv/mnemonas mnemonas
sudo zfs set compression=lz4 mnemonas
sudo zfs set recordsize=1M mnemonas
```

Schedule scrub:

```cron
0 2 * * 0 /sbin/zpool scrub mnemonas
```

Config:

```toml
[storage]
root = "/srv/mnemonas"
```

### Btrfs RAID1

```bash
sudo mkfs.btrfs -m raid1 -d raid1 /dev/sda /dev/sdb
sudo mkdir -p /srv/mnemonas
sudo mount /dev/sda /srv/mnemonas
```

fstab example:

```bash
echo "UUID=$(blkid -s UUID -o value /dev/sda) /srv/mnemonas btrfs defaults,compress=zstd 0 0" | sudo tee -a /etc/fstab
```

Scrub:

```cron
0 2 * * 0 /sbin/btrfs scrub start /srv/mnemonas
```

### mdadm RAID1 + ext4

```bash
sudo mdadm --create /dev/md0 --level=1 --raid-devices=2 /dev/sda /dev/sdb
sudo mkfs.ext4 /dev/md0
sudo mkdir -p /srv/mnemonas
sudo mount /dev/md0 /srv/mnemonas
sudo mdadm --detail --scan | sudo tee /etc/mdadm/mdadm.conf
sudo update-initramfs -u
```

### Single Disk + Cloud Backup

```bash
sudo mkdir -p /srv/mnemonas
```

Use snapshots or cold backup windows before syncing with rclone, restic, or borg. Single-disk deployments must have off-machine backups.

## Data Safety Layers

```text
Layer 1: MnemoNAS application protection
  - BLAKE3 verification
  - atomic writes
  - versions and trash
  - scrub

Layer 2: filesystem protection
  - ZFS/Btrfs scrub
  - copy-on-write
  - snapshots

Layer 3: hardware redundancy
  - mirror or RAID
  - spare disks

Layer 4: independent backup
  - cloud backup
  - external offline disk
```

## Performance Notes

| Operation | Main Factor |
| --- | --- |
| Sequential write | Disk I/O |
| Sequential read | Disk I/O |
| Small-file write | fsync frequency and metadata I/O |
| Directory listing | Metadata I/O |
| Dedup hit | In-memory/object index behavior |
| Scrub | Sequential read throughput |

ZFS tuning:

```bash
echo "options zfs zfs_arc_max=8589934592" | sudo tee /etc/modprobe.d/zfs.conf
sudo zpool add mnemonas cache /dev/nvme0n1
```

MnemoNAS tuning:

```toml
[dataplane.cdc]
avg_chunk_size = 2097152

[storage.retention]
max_versions = 20
max_age = "2160h"
```

## Summary

| Question | Answer |
| --- | --- |
| Is a specific filesystem required? | No. ext4 is enough to run. |
| What is recommended? | ZFS mirror for stronger reliability. |
| Can it meet common NAS reliability expectations? | Yes, when combined with mirror/RAID and backups. |
| Can data migrate to another machine? | Yes, move the full storage root. Current files remain readable; versions require MnemoNAS metadata. |

Key principle: MnemoNAS adds application-level versioning, whole-object version deduplication, verification, and recovery. Filesystem redundancy and independent backups remain the user's responsibility.

## Related Documents

- [Architecture](architecture.en.md)
- [Backup guide](backup-guide.en.md)
- [Docker deployment](docker-deployment.en.md)
- [FAQ](faq.en.md)
