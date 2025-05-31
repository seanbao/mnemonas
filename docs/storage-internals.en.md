# Storage Internals and Best Practices

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

Deleted files are moved into `.mnemonas/trash/` with metadata in SQLite. The metadata records original path, deletion time, expiration, and content location.

Trash retention and size limits are configured under `[storage.trash]`.

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

```bash
curl -H "Authorization: Bearer <access-token>" \
  http://localhost:8080/api/v1/versions/documents/report.docx

curl -X POST \
  -H "Authorization: Bearer <access-token>" \
  "http://localhost:8080/api/v1/versions/abc123.../restore?path=/documents/report.docx"
```

## Comparison with Traditional NAS

| Area | MnemoNAS | Traditional NAS | Pure CAS System |
| --- | --- | --- | --- |
| Current files | Native files | Native files | CAS objects |
| Version storage | CAS objects | Filesystem snapshots | CAS objects |
| Current-file readability | Directly readable | Directly readable | Needs special software |
| Deduplication | BLAKE3 whole-object versions; CDC file APIs available in dataplane | Filesystem dependent | Core feature |
| Metadata | SQLite | Filesystem and app metadata | JSON/DB |
| Complexity | Medium | Low for simple file sharing | High |

The hybrid approach trades some purity for recoverability and user inspection.

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
| Can it meet serious NAS safety expectations? | Yes, when combined with mirror/RAID and backups. |
| Can data migrate to another machine? | Yes, move the full storage root. Current files remain readable; versions require MnemoNAS metadata. |

Key principle: MnemoNAS adds application-level versioning, deduplication, verification, and recovery. Filesystem redundancy and independent backups remain the user's responsibility.

## Related Documents

- [Architecture](architecture.en.md)
- [Backup guide](backup-guide.en.md)
- [Docker deployment](docker-deployment.en.md)
- [FAQ](faq.en.md)
