# Backup Guide

English | [简体中文](backup-guide.md)

Data safety is a core MnemoNAS concern. This guide explains what must be backed up, how to take consistent backups, and how to restore data.

## Backup Strategy: 3-2-1

Follow the industry-standard 3-2-1 backup rule:

| Rule | Meaning | MnemoNAS Practice |
| --- | --- | --- |
| 3 copies | Production data plus two copies | Main data + external disk + cloud or another machine |
| 2 media types | Different storage media | SSD + HDD, or local disk + cloud |
| 1 offsite/offline copy | One copy outside the primary machine | Cloud storage or offline physical disk |

RAID, ZFS mirror, Btrfs RAID1, and mdadm reduce disk-failure risk. They are not backups.

## What to Back Up

Default `storage.root` is `~/.mnemonas`. Replace paths below if you changed it.

Systemd deployments commonly use `/srv/mnemonas` for data and `/etc/mnemonas/config.toml` for config. The config is outside the data directory and should be backed up separately.

```text
storage.root/
├── files/                  # user files
├── .mnemonas/              # internal data
│   ├── objects/            # CAS objects
│   ├── index.db            # SQLite metadata
│   ├── trash/              # trash contents
│   ├── thumbnails/         # thumbnail cache
│   ├── maintenance/        # scrub/GC state
│   └── activity/           # activity log
├── secrets.json            # generated JWT/WebDAV secrets
└── config.toml             # direct-run or Docker config, if stored here
```

| Path | Importance | Notes |
| --- | --- | --- |
| `files/` | Critical | Current user data |
| `.mnemonas/` | Critical | Metadata, versions, trash, maintenance state |
| `secrets.json` | Medium | Generated WebDAV/JWT secrets |
| `config.toml` | Medium | Direct-run and Docker config; systemd usually stores it under `/etc/mnemonas` |

## Consistency First

Do not copy an active storage root while MnemoNAS is writing to it, especially not `files/` and `.mnemonas/` separately.

Use one of these approaches:

1. Back up from a ZFS, Btrfs, or LVM snapshot.
2. Stop `mnemonas` and `mnemonas-dataplane`, back up, then start them again.

The examples below assume `SOURCE_DIR` is consistent: either a snapshot mount or a stopped-service `storage.root`.

Systemd cold backup window:

```bash
sudo systemctl stop mnemonas mnemonas-dataplane
# run rclone/restic/rsync backup here
sudo install -D -m 0600 /etc/mnemonas/config.toml /backup/mnemonas-config.toml
sudo systemctl start mnemonas-dataplane mnemonas
```

Docker cold backup window:

```bash
docker compose stop
# run rclone/restic/rsync backup here
docker compose start
```

## Method 0: Built-in Backup Jobs

MnemoNAS has a built-in backup job entry point that can run from the Maintenance page or API, report health, and run restore drills or remote consistency checks. Supported job types:

- `local`: copy a source directory into a local snapshot with `manifest.json`.
- `restic`: invoke the system `restic` executable and back up the source directory into a restic repository.
- `rclone`: invoke the system `rclone` executable and sync the source directory to an rclone remote.

Limits:

- `local.destination` must be an absolute path outside `storage.root`, otherwise the backup can recurse into itself.
- The default source is `storage.root`; for production data, prefer a ZFS, Btrfs, or LVM snapshot mount as `source`.
- Symlinks inside the source directory abort the job so the backup cannot escape the intended source tree.
- `restic` and `rclone` jobs do not build shell command strings; `command` must be a bare executable name or absolute path, and `extra_args` are appended as argv entries.
- `password_file` and `config_file` must be regular files outside `source` and `storage.root` so backup credentials are not included in the data being backed up.
- `schedule_interval` is a lightweight in-process scheduler for fixed intervals. For complex windows, bandwidth limits, network wake-up, and multi-stage recovery, continue to use systemd timers or external orchestration.

Example config:

```toml
[backup]

[[backup.jobs]]
id = "external-disk"
name = "External disk backup"
type = "local"
source = ""                                # empty means storage.root
destination = "/mnt/backup-drive/mnemonas" # must be outside storage.root
disabled = false
schedule_interval = "24h"                  # run every 24 hours; zero or omitted means manual only
schedule_window_start = "02:00"            # optional; automatic runs only start in this local-time window
schedule_window_end = "05:00"              # may cross midnight, for example 22:00 to 06:00
stale_after = "72h"                        # mark the job stale after 72 hours without a successful backup
max_snapshots = 7                          # retain up to 7 snapshots
max_age = "720h"                           # retain snapshots for up to 30 days
include_config = true
verify_after_backup = true
exclude = [".mnemonas/thumbnails"]
```

Restic example:

```toml
[[backup.jobs]]
id = "restic-remote"
name = "Restic encrypted backup"
type = "restic"
source = "/mnt/snapshots/mnemonas-latest"
repository = "rest:http://backup.example:8000/mnemonas"
command = "restic"
password_file = "/etc/mnemonas/restic.pass"
schedule_interval = "24h"
schedule_window_start = "02:00"
schedule_window_end = "05:00"
stale_after = "72h"
verify_after_backup = true
exclude = [".mnemonas/thumbnails"]
extra_args = ["--compression", "max"]
```

Rclone example:

```toml
[[backup.jobs]]
id = "rclone-cloud"
name = "Rclone cloud sync"
type = "rclone"
source = "/mnt/snapshots/mnemonas-latest"
remote = "cloud:mnemonas/current"
command = "rclone"
config_file = "/etc/mnemonas/rclone.conf"
schedule_interval = "24h"
schedule_window_start = "02:00"
schedule_window_end = "05:00"
stale_after = "72h"
verify_after_backup = true
exclude = [".mnemonas/thumbnails"]
extra_args = ["--fast-list"]
```

`schedule_window_start` and `schedule_window_end` only restrict automatic scheduling; manual run-now operations are unaffected. The window uses the server local time in `HH:MM` format and may cross midnight. Local retention runs after a successful backup and always keeps the current snapshot. `max_snapshots = 0` and `max_age = "0"` disable the corresponding pruning dimension. Restic and rclone retention is managed by the external tool, such as `restic forget --prune` or lifecycle rules on the remote. The Maintenance page shows job health, next scheduled run, schedule window, latest backup, latest restore drill, and how many old snapshots the latest local run pruned.

After restarting the service:

```bash
# List jobs
curl -b cookies.txt http://localhost:8080/api/v1/maintenance/backups

# Run now
curl -X POST -b cookies.txt http://localhost:8080/api/v1/maintenance/backups/external-disk/run

# Restore-drill or remote consistency-check; local temporary restores are deleted by default
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore-drill \
  -H 'Content-Type: application/json' \
  -d '{"keep_artifact":false}'
```

For `local`, the restore drill copies the latest snapshot into a temporary directory and verifies every file size and SHA-256 from the manifest. Set `keep_artifact = true` to retain the restored directory for manual inspection. For `restic`, the drill currently runs `restic check`; for `rclone`, it runs `rclone check --one-way` to verify remote consistency.

When you need to retrieve data from a `local` or `rclone` job, restore it into an explicit independent directory:

```bash
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas","include_config":true}'

# rclone job example: copy from remote and verify with rclone check before install
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/rclone-cloud/restore \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas-rclone","include_config":false}'
```

`target_path` must be an absolute server-side path outside the current `storage.root`, backup source, and any local backup destination. Its parent must exist, and the target must not exist or must be empty. Local restore copies snapshot `data/` contents into the target root and verifies them immediately. With `include_config = true`, the config file is restored to `target_path/.mnemonas-restore/config.toml`. Rclone restore runs `rclone copy <remote> <target>` and `rclone check <remote> <target> --one-way`. Inspect the restored directory first, then stop services and migrate paths or update `storage.root` when you are ready to switch over.

## Method 1: rclone

Install:

```bash
sudo apt install rclone
brew install rclone
```

Configure a remote:

```bash
rclone config
```

Example backup script:

```bash
#!/bin/bash
set -euo pipefail

SOURCE_DIR="${SOURCE_DIR:-$HOME/.mnemonas}"
REMOTE="remote:mnemonas-backup"
DATE=$(date +%Y%m%d)

echo "=== MnemoNAS backup started $(date) ==="
rclone sync "$SOURCE_DIR" "$REMOTE/current" \
  --progress \
  --transfers 4 \
  --checkers 8 \
  --backup-dir "$REMOTE/history/$DATE"
echo "=== MnemoNAS backup completed $(date) ==="
```

Schedule with cron:

```bash
crontab -e
```

```cron
0 3 * * * /path/to/backup.sh >> /var/log/mnemonas-backup.log 2>&1
```

For production machines, avoid running unreviewed pipe-to-shell install commands. Prefer distribution packages or reviewed release artifacts.

## Method 2: restic

Install:

```bash
sudo apt install restic
brew install restic
```

Initialize:

```bash
restic init --repo /backup/mnemonas-restic

export AWS_ACCESS_KEY_ID=<key>
export AWS_SECRET_ACCESS_KEY=<secret>
restic init --repo s3:s3.amazonaws.com/bucket/mnemonas
```

Back up and inspect snapshots:

```bash
SOURCE_DIR="${SOURCE_DIR:-$HOME/.mnemonas}"
restic backup "$SOURCE_DIR" \
  --repo /backup/mnemonas-restic \
  --tag mnemonas

restic snapshots --repo /backup/mnemonas-restic
```

Restore:

```bash
restic restore latest \
  --repo /backup/mnemonas-restic \
  --target /restore/mnemonas
```

Retention:

```bash
restic forget \
  --repo /backup/mnemonas-restic \
  --keep-daily 7 \
  --keep-weekly 4 \
  --keep-monthly 12 \
  --prune
```

## Method 3: Local rsync

Use this for an external disk or another local mount:

```bash
#!/bin/bash
set -euo pipefail

SOURCE_DIR="${SOURCE_DIR:-$HOME/.mnemonas}"
BACKUP_DIR="/mnt/backup-drive/mnemonas"

rsync -aHAX --delete \
  --exclude='*.tmp' \
  "$SOURCE_DIR/" \
  "$BACKUP_DIR/"
```

`SOURCE_DIR` must be a snapshot directory or a stopped-service storage root.

## Method 4: Docker Directory Backup

For the default Docker setup where host `~/.mnemonas` maps to `/data`:

```bash
docker compose stop
tar czf mnemonas-data.tar.gz -C ~/.mnemonas .
docker compose start
```

## Restore Data

Stop MnemoNAS before restore. Restore to a temporary path first, validate it, then replace the old storage root.

### Restore from rclone

```bash
sudo systemctl stop mnemonas mnemonas-dataplane
sudo mkdir -p /srv/mnemonas-restored
sudo rclone copy remote:mnemonas-backup/current /srv/mnemonas-restored
sudo rclone check remote:mnemonas-backup/current /srv/mnemonas-restored --one-way

sudo mv /srv/mnemonas /srv/mnemonas-old
sudo mv /srv/mnemonas-restored /srv/mnemonas
sudo chown -R mnemonas:mnemonas /srv/mnemonas
sudo chmod 0750 /srv/mnemonas /srv/mnemonas/files
sudo chmod 0700 /srv/mnemonas/.mnemonas
```

### Restore from restic

```bash
restic snapshots --repo /backup/mnemonas-restic

sudo systemctl stop mnemonas mnemonas-dataplane
restic restore <snapshot-id> \
  --repo /backup/mnemonas-restic \
  --target /restore/mnemonas
```

For Docker, replace the systemd commands with `docker compose stop` and `docker compose start`, and ensure restored ownership matches `MNEMONAS_UID` and `MNEMONAS_GID`.

## Validate Restore

```bash
sudo systemctl start mnemonas-dataplane mnemonas
sudo mnemonas-doctor

curl http://localhost:8080/health

curl -X POST \
  -H "Authorization: Bearer <access-token>" \
  http://localhost:8080/api/v1/maintenance/scrub
```

## Verify Backups

Run verification on a schedule:

```bash
restic check --repo /backup/mnemonas-restic

SOURCE_DIR="${SOURCE_DIR:-$HOME/.mnemonas}"
rclone check "$SOURCE_DIR" remote:mnemonas-backup/current
```

Also perform restore drills. A backup that has never been restored is an assumption, not a proven recovery path.

## Encrypt Offsite Backups

restic encrypts backups by default. For rclone, use a `crypt` remote wrapping your storage remote:

```bash
rclone config
```

Store repository passwords and cloud credentials in a password manager or secret store.

## Backup Failure Alerts

Built-in `[[backup.jobs]]` reuse the `[alerts]` webhook. MnemoNAS sends `backup_run` or `backup_restore_drill` events when a backup fails, a restore drill fails, or a successful backup has retention warnings:

```toml
[alerts]
enabled = true
webhook_url = "https://your-webhook.example/alert"
webhook_method = "POST"
```

`POST` sends a JSON body with `type`, `level`, `message`, `timestamp`, `hostname`, and `details`. The details include the job ID, job name, run ID, status, error message, and snapshot path when available. `GET` mode encodes the same base fields into the query string and sends `details` as a JSON string.

For external restic/rclone scripts, keep an exit trap:

```bash
notify_failure() {
  local status=$?
  if [ "$status" -ne 0 ]; then
    curl -fsS -X POST "https://your-webhook.example/alert" \
      -d "message=MnemoNAS backup failed" || true
  fi
  exit "$status"
}
trap notify_failure EXIT
```

## Example Strategies

| Scenario | Strategy |
| --- | --- |
| Minimal deployment | Weekly cold backup or snapshot backup to cloud; monthly external-disk copy |
| Advanced deployment | Daily restic to local NAS/external disk; weekly rclone to S3/OSS; monthly offline disk |
| Production-like setup | Daily filesystem snapshot, restic from snapshot, offsite copy, quarterly restore drill |

## Related Resources

- [rclone documentation](https://rclone.org/docs/)
- [restic documentation](https://restic.readthedocs.io/)
- [FAQ](faq.en.md)
- [Configuration reference](configuration.en.md)
