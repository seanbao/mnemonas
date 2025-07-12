# Backup Guide

English | [简体中文](backup-guide.md)

Data safety is a core MnemoNAS concern. This guide explains what must be backed up, how to take consistent backups, and how to restore after common failure modes.

## Backup Strategy: 3-2-1

Follow the industry-standard 3-2-1 backup rule:

| Rule | Meaning | MnemoNAS Practice |
| --- | --- | --- |
| 3 copies | Production data plus two copies | Main data + external disk + cloud or another machine |
| 2 media types | Different storage media | SSD + HDD, or local disk + cloud |
| 1 offsite/offline copy | One copy outside the primary machine | Cloud storage or offline physical disk |

RAID, ZFS mirror, Btrfs RAID1, and mdadm reduce disk-failure risk. They are not backups.

## What to Back Up

Default `storage.root` is `~/.mnemonas`. For deployments with a different storage root, replace the paths below.

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

- `local.destination` must be an absolute path outside `storage.root` and must not be the filesystem root or a protected system directory; existing destination path components must not be symlinks, otherwise the backup can recurse into itself or write through a symlink target. Local restore previews, restores, and restore drills recheck the destination before reading snapshot manifests or creating drill artifacts.
- The default source is `storage.root`; for production data, prefer a ZFS, Btrfs, or LVM snapshot mount as `source`.
- Symlinks inside the source directory abort backup jobs so the backup cannot escape the intended source tree. `rclone` restore drills also reject current source-tree symlinks before remote verification.
- `restic` and `rclone` jobs do not build shell command strings; `command` must be a bare executable name or absolute path without whitespace or control characters, and `extra_args`, `exclude`, and `retention_policy` must not contain control characters. `extra_args` are appended to backup commands as argv entries. Restore commands do not reuse backup-specific extra args.
- `password_file` and `config_file` must be regular files outside `source` and `storage.root`, and their existing path components must not be symlinks, so backup credentials are not included in the data being backed up or reached through a symlink alias.
- Job views, run results, restore or preview results, restore reports, and batch restore results redact embedded userinfo, tokens, passwords, secrets, and key parameters in target paths, remote target fields, and API-visible backup error, warning, or restore-report findings text.
- Backup alert events do not include sources, destinations, restore target paths, snapshot or manifest paths, or raw warning/error text. They retain only summary fields such as status, trigger, counts, timestamps, failure category, and markers for omitted location or error details.
- Restic/rclone commands still use the original configured `repository` or `remote` values. Clients that call `restore-verify` after a restore should reuse the original request `target_path`, not the redacted response `target_path` intended for display.
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
schedule_interval = "24h"                  # run every 24 hours; zero, empty, or omitted means manual only
schedule_window_start = "02:00"            # optional; automatic runs only start in this local-time window
schedule_window_end = "05:00"              # may cross midnight, for example 22:00 to 06:00
stale_after = "72h"                        # mark the job stale after 72 hours without a successful backup
restore_drill_stale_after = "720h"         # remind after 30 days without a successful restore drill
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
restore_drill_stale_after = "720h"
retention_policy = "external: restic forget --keep-daily 7 --keep-weekly 4 --prune"
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
restore_drill_stale_after = "720h"
retention_policy = "external: cloud lifecycle keeps 30 daily versions"
verify_after_backup = true
exclude = [".mnemonas/thumbnails"]
extra_args = ["--fast-list"]
```

`schedule_window_start` and `schedule_window_end` only restrict automatic scheduling; manual run-now operations are unaffected. The window uses the server local time in `HH:MM` format and may cross midnight.

Local retention runs after a successful backup and always keeps the current snapshot. `max_snapshots = 0` and `max_age = "0"` disable the corresponding pruning dimension.

Restic and rclone retention is managed by the external tool, such as `restic forget --prune`, a systemd timer, or lifecycle rules on the remote. Set `retention_policy` to mark that external policy as confirmed in the Maintenance page; otherwise the task shows a retention warning.

After each successful backup MnemoNAS also runs a retention check, and the Maintenance page exposes a manual "Check retention" action:

- `local` validates each local snapshot `manifest.json` and snapshot layout before counting the local snapshot range.
- `restic` runs `restic snapshots --json --tag mnemonas --tag job:<id>`.
- `rclone` runs `rclone lsjson <remote> --recursive --files-only`.

A local snapshot directory with a non-canonical run ID, a non-snapshot entry in the `snapshots/` root (only `.partial` directories are skipped as incomplete snapshots), a missing manifest, a manifest path that contains a symlink, or a manifest that is not a regular file fails the retention check and persists a failed result.

A manifest also fails validation when it has a mismatched job ID, run ID, or `created_at`, unsafe archive path, duplicate path, negative file size, invalid file mode, invalid SHA-256, inconsistent summary fields, missing `data/` directory, extra files missing from the manifest, or unexpected top-level directories, so damaged snapshots are not treated as usable snapshots.

Local restore previews, restores, and restore drills also require the persisted snapshot path and manifest path for the latest run to match the current `local.destination/<job-id>/snapshots/<run-id>/manifest.json` location. The latest completed snapshot must not be missing and must have a manifest, the `snapshots/` root must not contain non-snapshot entries or non-canonical snapshot directories, the manifest path must not contain symlinks, and the manifest must be a regular file; mismatches are rejected so snapshots outside the configured backup target are not used.

Results are persisted as `last_retention_check` and warn when snapshots are missing, the remote is empty, `retention_policy` is not set, or the external command fails. `restore_drill_stale_after` controls periodic restore-drill reminders and defaults to 30 days when empty or omitted.

The Dashboard summarizes the number of backup tasks needing attention, their main reasons, and suggested next steps using the same criteria. The Maintenance page shows job health, retention status, restore-drill status, restore-drill history and success-rate summary, next scheduled run, schedule window, latest backup, latest restore target, recent restore history, and how many old snapshots the latest local run pruned.

When a task has a backup failure, retention issue, restore-drill attention state, latest-backup or latest-restore warning, pending restore verification, or failed/warning restore check, the job row also summarizes the attention reasons and suggested next steps.

Backup, restore, restore-drill, read-only verification, and retention-check operations persist a `running` record before execution. During service startup, `running` records left by a previous process exit are marked failed and written back to the state file.

Restore-drill history and explicit restore history both keep the latest 20 entries by default, including failed attempts and their error messages. Failed drills also record a stable `failure_category` for common causes such as missing snapshots, integrity-check failures, external-command failures, and I/O errors.

After restarting the service:

```bash
# List jobs
curl -b cookies.txt http://localhost:8080/api/v1/maintenance/backups

# Run now
curl -X POST -b cookies.txt http://localhost:8080/api/v1/maintenance/backups/external-disk/run

# Check retention policy and visible remote/local contents
curl -X POST -b cookies.txt http://localhost:8080/api/v1/maintenance/backups/external-disk/retention-check

# Restore-drill or remote consistency-check; local temporary restores are deleted by default
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore-drill \
  -H 'Content-Type: application/json' \
  -d '{"keep_artifact":false}'
```

For `local`, the restore drill first confirms that the snapshot does not contain files missing from the manifest, then copies the latest snapshot into a temporary directory and verifies every file size, permission mode, and SHA-256 from the manifest. Set `keep_artifact = true` to retain the restored directory for manual inspection. For `restic`, the drill currently runs `restic check`; for `rclone`, it runs `rclone check --one-way` to verify remote consistency.

To retrieve data from a `local`, `restic`, or `rclone` job, restore into an explicit independent directory:

```bash
# Preview first: validate target safety and confirm estimated files, bytes, and sample paths
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore-preview \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas","include_config":true}'

curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas","include_config":true}'

# Post-restore check: read-only target scan and storage-root layout detection
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore-verify \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas"}'

# rclone job example: copy from remote and verify with rclone check before install
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/rclone-cloud/restore \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas-rclone","include_config":false}'

# restic job example: restore latest + job tag and install source contents at the target root
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/restic-cloud/restore \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas-restic","include_config":false}'
```

`restore-preview` does not write target data and does not write restore history. It reuses the same restore-target safety checks and returns estimated file count, bytes, up to 10 sample paths, `preflight_checks`, `warnings`, `cutover_checklist`, and `rollback_checklist`. Local restore previews first check the snapshot layout and reject extra files missing from the manifest or unexpected top-level directories.

The Maintenance page requires a successful preview that still matches the current target and config option and has no failed preflight checks before enabling restore. Before execution it also shows a review summary covering the target path, write boundary, restore content, config handling, preflight counts, and post-restore read-only verification plan, plus an impact summary for target state, conflict or overwrite risk, config and permission impact, restore scope, and post-restore verification.

Preflight checks cover target isolation, `target_state`, backup content, target filesystem capacity, and config handling. `target_state` reports whether the target directory does not exist or the target directory already exists and is empty; both states are accepted. Missing targets use the parent directory for the capacity probe, while existing empty target directories use the target directory's filesystem.

`preflight_checks[].status` can be `passed`, `warning`, or `failed`. `status = "warning"` means restore can continue after review, while `status = "failed"` prevents the Maintenance page from starting restore and is rejected by server-side preflight before `restore` writes data. `warnings` aggregates warning and failed preflight details for the Maintenance page, batch previews, and restore history.

`target_path` must satisfy these rules:

- It is an absolute server-side POSIX path that starts with `/`.
- It contains no control characters, backslashes, or `.`/`..` path segments.
- It is not the filesystem root or a protected system directory.
- It is outside the current `storage.root`, backup source, and any local backup destination or repository.

Windows and UNC paths are not valid server restore targets. Its parent must exist, the target must not exist or must be empty, and existing target path components must not be symlinks. Invalid restore `target_path` values and invalid batch restore entries return `400 Bad Request`; unsafe paths caused by backup job configuration, backup source contents, or external commands are task execution failures.

`restore` reruns the same server-side preflight before writing and first confirms that a local snapshot does not contain files missing from the manifest; failed preflight or snapshot verification rejects the restore and is persisted with the failed restore record. Local restore copies snapshot `data/` contents into the target root, preserves empty directories and directory permissions, and verifies files immediately. With `include_config = true`, the config file is restored to `target_path/.mnemonas-restore/config.toml`.

Restic preview uses `restic ls --json`, while restore runs `restic restore latest --tag mnemonas --tag job:<id> --path <source>` and installs the restored source directory contents at the target root.

Rclone preview uses `rclone lsjson`, while restore runs `rclone copy <remote> <staging>` and `rclone check <remote> <staging> --one-way` against a staging directory. Restic preview and rclone preview or retention listings reject unsafe output file paths, including empty paths, control characters, backslashes, Windows/UNC syntax, `.`/`..` path segments, or absolute paths outside the configured source boundary.

Restic and rclone restores reject restored symlinks and special files before installing the target directory; rclone installs the staging directory into the target path.

After restore, `restore-verify` scans the target read-only and reports file count, bytes, config presence, whether `files/` plus `.mnemonas/` look like a full storage root, and warnings for symlinks, special files, or incomplete layout.

For `local` jobs it first compares against the snapshot recorded for the latest successful restore to the same target, falling back to the latest local snapshot manifest and directory layout only when no matching restore record exists, and returns the comparison `snapshot_path` and `manifest_path`.

It warns about missing files, checksum/size/mode mismatches, extra regular files or directories, missing directories, directory permission drift, or mismatched restored `.mnemonas-restore/config.toml` contents when that file exists. The latest report is persisted as `last_restore_verify` so it remains visible after refreshing the Maintenance page. The Maintenance page enters the post-restore cutover checklist automatically after a successful restore and also displays the rollback checklist for that restore.

Requests with invalid `target_path` syntax return `400 Bad Request` and do not update the latest restore or latest restore-verification status. Restore attempts that pass syntax validation but fail on boundaries, target state, backup content, or external commands are still recorded as failed results.

`restore-verify` reuses the same `target_path` validation before checking whether the target directory exists, including control-character, backslash, `.`/`..` segment, Windows/UNC syntax, protected-directory, boundary-directory, and symlink path-component checks.

The Maintenance page and restore report associate `last_restore_verify` with the latest restore only when the latest restore completed successfully, the target path matches, and the verification start time is not earlier than the restore completion time. Running, completed, and failed matching verifications are copied into `last_matching_restore_verify`.

While verification is running, the job view shows the check in progress and the restore report states that the target should not be switched before completion; otherwise they report that the latest restore still needs a matching read-only verification. The Maintenance page can rerun read-only verification directly from the backup-job list for the latest successful restore target, so interrupted, refreshed, or manually investigated pending-verification states can be completed without reopening the restore flow.

When the latest restore is still running, restore reports state that the restore has not completed and do not attach older read-only verification results.

For multiple independent jobs or targets, preview the batch first and then run the batch restore:

```bash
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/batch-restore-preview \
  -H 'Content-Type: application/json' \
  -d '{"items":[{"job_id":"external-disk","target_path":"/mnt/restore/a","include_config":true},{"job_id":"rclone-cloud","target_path":"/mnt/restore/b","include_config":false}]}'

curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/batch-restore \
  -H 'Content-Type: application/json' \
  -d '{"items":[{"job_id":"external-disk","target_path":"/mnt/restore/a","include_config":true},{"job_id":"rclone-cloud","target_path":"/mnt/restore/b","include_config":false}]}'
```

A batch can contain up to 20 items and rejects duplicate or nested targets within the same request. Batch preview does not write data.

The Maintenance page shows a batch-restore readiness summary in the restore dialog, covering selected item count, target completion, config-restore item count, and preview state.

The Maintenance page also provides **Select attention**, **Select all**, and **Clear selection** controls. **Select attention** chooses currently restorable jobs with failed, stale, pending-verification, latest-backup or latest-restore warning, or warning restore-check states; **Select all** chooses the currently restorable jobs. Both actions fill suggested targets for items without a target.

After batch preview, the Maintenance page shows a pre-submit review covering item count, target independence, estimated restore content, config-restore item count, preflight counts, and post-restore read-only verification, plus a batch impact summary for target conflicts, overwrite risk, config and permission impact, failed or warning preflight checks, and post-restore verification.

The batch restore API reruns preflight for the full batch before writing any target directory. If any item has a target conflict, a failed preflight, or cannot produce a preview, the batch restore fails with per-item errors and writes no target data. After the full-batch preflight passes, batch restore runs items sequentially, and every successful item is followed by `restore-verify`.

Top-level `total_files` and `verified_bytes` aggregate the read-only verification results from completed items. Runtime failures after a passed preflight can still produce partial failures and set the overall `warning` flag, so clients should inspect each `items[]` entry for status, error, and verification output.

The Maintenance page shows restore-summary findings in the job summary, and the **Export summary** action downloads the JSON restore summary for the backup job. The JSON includes latest backup, retention check, restore drill, restore-drill history, explicit restore, read-only verification, restore history, and findings. Download responses use `Cache-Control: no-store`, `Pragma: no-cache`, `X-Content-Type-Options: nosniff`, and `Referrer-Policy: no-referrer`. A restore summary should be retained before switching `storage.root` and kept with diagnostics after a failed restore.

Cutover checklist:

1. Confirm `restore-verify` has no unexplained warnings. For a full MnemoNAS storage-root restore, expect both `files/` and `.mnemonas/`.
2. Keep the current config file and current `storage.root` as rollback points.
3. Stop `mnemonas` and `mnemonas-dataplane`, then either point `storage.root` to the restored directory or move the restored directory into the production mount point.
4. Start services and check health, login, file listing, upload, download, and version history.
5. Keep the old directory until the restored instance is verified. To roll back, restore the old config and point `storage.root` back to the old directory.

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

When restoring a restic job through the Maintenance page or `/api/v1/maintenance/backups/{id}/restore`, MnemoNAS automatically selects the latest snapshot with the `mnemonas` and `job:<id>` tags and moves the restored source contents to the target root, avoiding manual movement of nested paths such as `/restore/.../srv/mnemonas`.

For Docker, replace the systemd commands with `docker compose stop` and `docker compose start`, and ensure restored ownership matches `MNEMONAS_UID` and `MNEMONAS_GID`.

## Validate Restore

```bash
sudo systemctl start mnemonas-dataplane mnemonas
sudo mnemonas-doctor
```

The Web UI security self-check checks enabled local backup job destinations and reports targets that are inside `storage.root` or the backup source, pass through symlink components, are not directories, are missing, or appear non-writable. For systemd deployments, `mnemonas-doctor` checks whether `BACKUP_ROOT` is inside `storage.root` and reports a risk when the backup target is a symlink, is not a directory, shares the same filesystem source as the main storage, or is not writable. Long-running backup targets should use a separate disk, dataset, or remote storage.

```bash
# Docker start; for release images, use docker compose up -d --no-build instead.
docker compose up -d

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

restic encrypts backups by default. For rclone, use a `crypt` remote wrapping the storage remote:

```bash
rclone config
```

Store repository passwords and cloud credentials in a password manager or secret store.

## Backup Failure Alerts

Built-in `[[backup.jobs]]` reuse `[alerts]` notification channels. MnemoNAS sends `backup_run`, `backup_restore`, `backup_restore_verify`, `backup_restore_drill`, or `backup_retention_check` events when a backup fails, an explicit restore fails, an explicit restore completes with warnings, a post-restore read-only verification fails or reports warnings, a restore drill fails, a restore drill is missing or stale beyond `restore_drill_stale_after`, a successful backup has retention-check warnings, or a manual retention check fails or reports warnings.

The event `message` is a fixed public summary and does not include job names, paths, or raw error text. Event details contain only job ID, run ID, job type, trigger, status, timestamps, file/byte/snapshot counts, warning count, error-message presence, failure category, and whether location details were omitted. They do not include job names, sources, backup targets, restore target paths, snapshot paths, manifest paths, raw warnings, or raw error text.

Restore-drill reminders are rate-limited and recorded as `last_restore_drill_reminder_at` in the job view. Webhook, Telegram, WeCom, DingTalk, and SMTP email channels can receive these events. A Webhook channel can be enabled with:

```toml
[alerts]
enabled = true
webhook_url = "https://webhook.example.com/alert"
webhook_method = "POST"
```

`POST` sends a JSON body with `type`, `level`, `message`, `timestamp`, `hostname`, and `details`. `details` uses only the redacted summary fields listed above. `GET` mode encodes the same base fields into the query string and sends `details` as a JSON string. Neither mode sends job names, sources, backup targets, restore target paths, snapshot paths, manifest paths, raw warnings, or raw error text.

For external restic/rclone scripts, keep an exit trap:

```bash
notify_failure() {
  local status=$?
  if [ "$status" -ne 0 ]; then
    curl -fsS -X POST "https://webhook.example.com/alert" \
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
