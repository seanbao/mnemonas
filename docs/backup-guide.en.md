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
sudo rclone sync remote:mnemonas-backup/current /srv/mnemonas-restored

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

Add an exit trap to backup scripts:

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
