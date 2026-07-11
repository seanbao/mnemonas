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
| `file_locks` | WebDAV lock state |

SQLite gives MnemoNAS ACID transactions, indexes, and a portable metadata file.

## Trash

When Trash is enabled, deleted files are moved into `.mnemonas/trash/` with metadata in SQLite. The metadata records original path, deletion time, persisted expiry, and content location. When Trash is disabled, deletion is immediately permanent.

Trash expiry and size limits are configured under `[storage.trash]`. `[storage.retention].gc_interval` drives one background sweep for both expired file versions and expired Trash items. A value of `0` disables both periodic cleanup paths without disabling capacity cleanup, explicit permanent deletion, or empty-Trash operations.

Each Trash item receives a random ID when created, and that ID remains immutable for the item's lifetime. Exact emptying loads the current Trash state once under one storage write lock and preflights access rules for the restored logical paths of every selected item that still exists, including descendants, before any deletion occurs. A preflight failure leaves every selected item unchanged. After preflight succeeds, items are deleted in request-ID order. Unselected items, including items added after the operation begins, remain unchanged, and selected IDs that no longer exist are skipped. If a hard execution failure occurs, selected items that still exist and have not been processed remain unchanged. The deleted, remaining, and skipped result sets form a complete, non-overlapping partition of the original request and each preserves request order.

### Permanent Trash-Item Deletion Recovery Journal

Explicit permanent deletion, exact emptying, expiry cleanup, and capacity cleanup share a durable decision journal under `.mnemonas/trash/.deleting/` when they permanently remove an item that is already in Trash. Before the first item move, the server writes and syncs `purge-{operation-id}.prepared.json`. The record contains the complete Trash metadata, a path-sorted content manifest, and the version-object hashes that may require cleanup. A no-replace rename then moves the content to `purge-{operation-id}.item` in the same directory. After removing the SQLite Trash metadata, the server writes and syncs a matching `purge-{operation-id}.committed.json` before recursively removing the stage, version metadata, and unreferenced version objects.

Startup recovery runs before the retention sweeper, workspace-stage cleanup, and network listener start. An operation with only a `prepared` decision rolls back after validating the complete manifest: it restores the content to the canonical Trash path and restores missing SQLite metadata. Recovery completes every `prepared` rollback found in the scan before starting any `committed` roll-forward. A failed rollback or canceled context blocks the entire committed phase so shared version-ownership evidence is restored before version cleanup. An operation with a `committed` decision only rolls forward: it removes any remaining Trash metadata and staged content, completes version cleanup, and finally removes the decision journals. Manifest entries that are already absent are treated as completed parts of a roll-forward; extra entries, same-name replacements, and identity changes are not removed. A `committed` decision is never rolled back into a restorable Trash item.

If journal filenames, operation IDs, decision pairs, manifests, staged content, and SQLite metadata cannot be verified safely against one another, recovery fails closed and prevents writable startup while preserving the relevant paths for manual inspection. A runtime rollback failure also blocks later Trash mutations until recovery succeeds. A recognized orphan `.item` without a decision journal blocks startup recovery. Unrecognized legacy residue, temporary files, and other untracked paths are reported and preserved; a filename pattern alone never authorizes automatic deletion.

This journal covers only permanent deletion of an item that is already in Trash. It does not cover a live delete-to-Trash operation or Trash restore. Coordinating cross-filesystem stages with business state on those two paths still requires a separate durable participant/outbox protocol. Unverifiable residue from those paths remains subject to the manual-review boundary described later in this section.

Journal recovery uses a persistent identity composed of device number, inode, and object type, and validates permissions, size, and file content hashes separately. Linux and macOS provide this identity across a rename within one filesystem and support the handle-relative per-entry removal used by recovery. Other platforms reject the operation before publishing a purge record, moving a Trash item, or changing its metadata. A remount, snapshot restore, or volume transfer can change the device number. In that case, recovery rejects the evidence as the original object even if the inode and content still match, and manual inspection is required.

Safe recovery also requires one `nasd` writer per storage root at any time. The MnemoNAS storage lock is process-local and does not provide cross-process single-writer arbitration. Running multiple `nasd` instances against the same storage root concurrently is unsupported.

On Linux and macOS, workspace metadata reads derive an opaque object-identity token from the device, inode, ctime, type and permission bits, size, and nanosecond modification time. REST delete confirmation requires the identity token from the current list. The server samples the complete delete policy and mutation epoch while briefly holding the storage read lock, then releases the lock and scans and hashes the entire requested target batch outside the lock. After the scan, the server validates the mutation epoch again. If it changed, the server discards the entire batch result and performs bounded retries, using a read-locked fallback scan when necessary. The root callback checks write access, the nested-mount boundary below the workspace root, file type, and observed identity in that order; an identity mismatch does not read content or traverse a directory. After the root passes, the traversal applies the same access, mount-boundary, and type checks to later entries, hashes regular files through a no-follow read path, and streams node digests into the v3 target token in name-sorted depth-first order. Nested mount points, symlinks, FIFOs, Unix sockets, and other special files in a target tree are rejected immediately, preventing an incomplete target token and avoiding blocking during open. The boundary comes from the host mount table, so it also detects bind mounts on the same device. Both a tree containing a mount point and a target located inside a mount point are rejected. The v3 target token uses a hierarchical SHA-256 Merkle representation. The final token binds the canonical root path and complete path hierarchy. Each node digest binds a file-or-directory domain separator, the full permission mode, size, nanosecond modification time, object identity, and the snapshot content-hash field; for a regular file, that field carries the actual content digest. A directory node sorts children by name and combines each child name and digest in that order. The final target token remains a 64-character lowercase hexadecimal string that callers must treat as opaque. Directories and empty directories also participate. Prepare retains only the root metadata required for the REST response and the target token; it does not retain a complete target-tree manifest. One confirmation request accepts at most 1000 non-nested targets. When a platform cannot provide the required object identity, the list returns `null` and the server rejects observed delete-intent creation. Without contention, each target is still scanned once and each regular file is still hashed once. Contention can add scans and hashes from discarded attempts. The mutation epoch is not included in the v3 target token or exposed through REST requests or responses; the existing REST shapes remain unchanged.

During deletion, complete-policy comparison, per-entry write-access revalidation, target-token comparison, and mutation execute under one storage write lock. Policy comparison precedes target-tree traversal, so a stale policy cannot trigger a full-tree read. A delete-mode, retention-period, sweep-interval, capacity-limit, or target-tree change detected before object capture does not commit workspace, index, version, share, favorite, Trash, or activity changes. A confirmed target that disappeared or whose parent is no longer a directory is also treated as target drift. After object capture begins, a failed path may perform a no-replace rollback and may change object ctime or parent-directory timestamps. WebDAV conditional deletion first revalidates write access for the complete target tree under the same write lock, then reads the relevant target attributes and evaluates the condition; only ETag-dependent conditions calculate a content hash.

After access checks, target-token comparison, or WebDAV conditions pass, deletion does not resolve the object again from its original logical path. The server first opens the root through a no-follow handle as a witness, compares the handle identity with the root identity in the snapshot verified by the current request, and captures a content hash for a regular-file witness when the snapshot does not already contain one. It then uses an atomic no-replace rename to capture the current leaf at a random stage under the same parent on the source filesystem. The staged object must identify the same object as the witness. If an unknown same-name object replaces the staged path before revalidation, the server records an independent recovery copy in the residual path only when the still-open regular-file witness can be copied, both the file and parent directory can be synced, and the copy can be revalidated against both path identity and that hash. The unknown object remains in place. A directory witness or a regular-file recovery that cannot be synced and verified does not label the unknown stage path or unconfirmed copy as recovery evidence. `StagePath` remains empty, while `InspectionPaths` and the error log identify the actual unknown locations for manual inspection. The complete staged tree is mapped back to the original logical paths and rechecked for entry set, type, size, modification time, descendant identity, and file content. File hashing also verifies before and after the read that the open handle and staged path still identify the same object. Because rename can change the root object's ctime, the pre-rename root identity is retained only after `os.SameFile` succeeds; descendant identities are still compared from their staged state.

After each regular-file copy is published, the copy path keeps the destination file handle open, checks the copied byte count, and hashes the published destination completely against the digest accumulated during the copy read. The handle and destination path must retain the same identity before and after that hash. After the complete Trash copy is successfully published, the server performs one further complete content-hash pass over the source stage and one over the destination copy, producing a complete copy proof that covers paths, types, sizes, permissions, object identities, and hashes. The immediately following complete source and destination traversals do not read file content. They revalidate the entry sets, path mappings, types, sizes, permissions, modification times, and object identities, then perform a final mount-boundary check on both sides before allowing business-state commit. Delete confirmation, target-token revalidation at commit, stage capture, the initial source and destination copy proof, and the content proofs before and after moving the source stage into quarantine retain their existing behavior. After business-state commit and before recursive removal of the quarantined source, the server performs one final complete hash pass over the Trash copy. If that copy has drifted, the operation returns a committed cleanup warning and retains the quarantined source for recovery. Final physical removal moves the verified source stage into a random quarantine under the same parent with permissions no broader than mode `0700`, then removes entries relative to a server-held directory handle. The first member of a hard-link group is checked with the complete identity token; later members must still match the same manifest inode, allowing only the ctime change caused by removing an earlier link. An unknown entry, same-name replacement, identity change, or newly observed mount stops recursive removal and retains the quarantine content.

For one successful regular-file deletion without contention, the Trash path across delete confirmation and the subsequent commit call performs nine complete content-hash passes plus one copy read. One hash proves that the published destination matches the copy-stream digest, and the final pass revalidates the committed Trash copy before physical source cleanup. The permanent-delete path performs five complete content-hash passes. Contention, failure handling, or rollback can add file reads. This change does not alter REST requests or responses, v3 target tokens, or external status-code mappings. Only the witness-recovery branch for a replaced post-rename stage restricts `StagePath` to a verified recovery copy. When that copy cannot be confirmed, `StagePath` remains empty and `InspectionPaths` lists the actual unknown stage and unconfirmed copy locations. At other failure stages, `StagePath` identifies a retained internal residue location that still requires manual review against the error cause and filesystem evidence.

Cross-root regular-file moves support Trash restore and related internal migration. The copy path checks the source path's initial identity, open-handle identity, actual copied byte count, post-copy handle state, and current path identity. After publication, it keeps the destination handle open, hashes the published content completely against the copy-stream digest, and rechecks handle-to-path identity. A no-replace rename then moves the source to a random `.mnemonas-move-*.tmp` isolation path under the same parent. The isolated source is removed only after its identity and content proof, the destination's final content proof, and both mount boundaries remain valid. On Unix, checked removal also atomically captures each verified leaf under a random `.mnemonas-remove-*` name and rechecks identity before the final unlink or rmdir. Any observed source or destination drift rejects the move. Rollback restores only a verified isolated source and cleans only a destination that still matches this copy proof; unknown replacements and unconfirmed isolated entries detected before the final filesystem call remain in place.

Trash deletion copies only from the verified stage and retains the source stage until Trash metadata, indexes, and delete hooks complete. The same flow applies on a single filesystem and across `EXDEV`. Permanent deletion also removes only the stage and does not delete the original logical path. A new object created at the original path during deletion is not copied, overwritten, or removed. Before logical commit, rollback is limited to a no-replace rename from the stage to the original path. If the original path is occupied, a mount boundary changes, or stage identity cannot be verified, the new object remains unchanged and the operation returns a recovery-required residual. Trash metadata and its copy remain paired when they cannot be safely rolled back. REST returns this uncommitted outcome as `500 Internal Server Error` and does not record delete activity.

After the applicable index, delete-hook, Trash, or version metadata has committed, a physical-cleanup failure does not report an effective deletion as failed. REST returns `200 OK` with cleanup warnings and marks the corresponding cleanup warning in delete activity. The server error log records a recovery path only when a stage or quarantine residue remains. WebDAV returns `204 No Content` with cleanup warnings. The logical path remains deleted in both cases. Request cancellation does not interrupt quarantine cleanup after commit. If content was removed and only the parent-directory sync failed, the result carries only a persistence warning and does not invent a residual path.

This atomic boundary and the mutation epoch cover only operations serialized through the MnemoNAS storage lock. Direct filesystem writes by another process with the same UID and concurrent mounts by a privileged process neither advance the epoch nor are serialized by the storage lock; object-identity, mount-boundary, staged-object verification, and recovery-safety checks reject each change they observe. External writers are not governed by the storage lock, so a change between the last verification and the filesystem call remains outside the application-level transaction boundary. Process crashes and power loss remain outside these in-process mechanisms; the `.deleting` journal described above provides startup recovery only for permanent deletion of an item already in Trash. The server does not move back or delete an unknown replacement, and it does not automatically remove `.mnemonas-delete-*.stage`, `.mnemonas-delete-*.recovery`, `.mnemonas-delete-*.quarantine`, `.mnemonas-move-*.tmp`, `.mnemonas-remove-*`, or untracked internal Trash residue whose ownership cannot be verified. `InspectionPaths` in an error and the server log identify the actual locations when known. Residue outside the journaled recovery scope requires manual review using those paths and filesystem evidence.

Deletion-specific traversal does not change ordinary directory traversal, search, or file-count behavior. The deletion path rereads the mount table for the intent snapshot, access revalidation, Trash target description, before and after cross-root copying, before and after moving a source stage into quarantine, and immediately before recursive removal. A read failure or invalid mount path rejects continued processing as `ErrNotRegular`. Before capture, REST and WebDAV map it to `409 Conflict`; a failure that cannot be safely rolled back becomes a recovery residual, while the same condition after logical commit becomes a cleanup warning. A copied destination is removed only while its boundary remains verifiable. If that boundary cannot be verified, recursive cleanup stops and preserves internal copied residue to avoid crossing a new mount. Trash-item staging and recursive removal follow the same rule.

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
