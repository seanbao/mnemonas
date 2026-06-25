# Linux/systemd Deployment Guide

English | [简体中文](linux-systemd-deployment.md)

This guide targets long-running Linux server deployments.
It keeps setup steps limited, enables systemd auto-start, preserves useful logs, uses a stable data directory, and supports fast diagnostics when something fails.

## Good Fit

- Ubuntu 22.04/24.04 LTS, Debian, or a similar systemd-based Linux distribution.
- Single-machine file service, document/media archive, LAN WebDAV, or WebDAV behind a reverse proxy.
- Filesystem-managed physical reliability, with MnemoNAS providing Web UI, WebDAV, versions, trash, checksums, and scrub.

MnemoNAS does not implement RAID. For multi-disk reliability, use ZFS mirror, Btrfs RAID1, or mdadm, then point MnemoNAS at the mounted directory.

## Recommended Paths

| Path | Purpose |
| --- | --- |
| `/srv/mnemonas` | Main MnemoNAS data directory |
| `/etc/mnemonas/config.toml` | Service configuration |
| `/usr/local/bin/nasd` | Go control plane and Web UI service |
| `/usr/local/bin/dataplane` | Rust data plane service |
| `/usr/local/share/mnemonas/web` | Static Web UI assets |
| `/backup/mnemonas` | Local or external backup target |

## Storage Preparation

The recommended high-reliability setup is two SSDs in a ZFS mirror, plus an independent disk or remote storage for scheduled backups.

The following commands destroy data on the selected disks. Check model and serial numbers first:

```bash
ls -l /dev/disk/by-id/
```

Then create a pool:

```bash
sudo apt update
sudo apt install -y zfsutils-linux

sudo zpool create \
  -o ashift=12 \
  -o autotrim=on \
  -O compression=lz4 \
  -O atime=off \
  -O xattr=sa \
  -O acltype=posixacl \
  mnemonas mirror /dev/disk/by-id/<disk-a> /dev/disk/by-id/<disk-b>
sudo zfs create -o mountpoint=/srv/mnemonas -o recordsize=1M mnemonas/data
sudo mkdir -p /srv/mnemonas
```

Single-disk ext4, XFS, or Btrfs is also usable, but it does not protect against disk failure. Keep an independent backup.

If the same server also runs Docker, downloaders, transcoding, model caches, or other heavy services, do not put their data under `/srv/mnemonas`.
Use a separate scratch path such as `/srv/fast-scratch`.

## Install MnemoNAS

Download a Linux release archive from GitHub Releases:

```bash
tar -xzf mnemonas-<version>-linux-amd64.tar.gz
cd mnemonas-<version>-linux-amd64

sudo ./scripts/install-systemd.sh
```

Default install behavior:

- Creates a `mnemonas` system user.
- Creates `/srv/mnemonas/files` and `/srv/mnemonas/.mnemonas`.
- Installs `mnemonas-dataplane.service` and `mnemonas.service`.
- Listens on `0.0.0.0:8080`.
- Enables and starts both services.

Custom data directory or port:

```bash
sudo env STORAGE_ROOT=/srv/mnemonas SERVER_PORT=8080 ./scripts/install-systemd.sh
```

The systemd installer and uninstaller require absolute paths for these locations:

- `BIN_DIR`
- `SHARE_DIR`
- `CONFIG_DIR`
- `CONFIG_PATH`
- `SYSTEMD_DIR`
- `STORAGE_ROOT`
- The Web UI directory

Those paths must not contain control characters or symbolic-link components.
`CONFIG_PATH` must stay under `CONFIG_DIR`.

Except for the Web UI directory living under `SHARE_DIR`, binary, shared-resource, config, systemd unit, and data paths must not overlap each other.
Before creating directories or changing permissions, the installer also checks `STORAGE_ROOT/files` and `STORAGE_ROOT/.mnemonas/objects`.
Those managed subdirectories must not point elsewhere through symlinks.

To place data on a separate disk, mount the real filesystem at the target directory before running the installer.
Do not point `STORAGE_ROOT` at a symlink.

The installer only fixes ownership of the managed top-level directories by default. After manual data copy, recursive ownership repair can be requested with:

```bash
sudo env FIX_STORAGE_OWNERSHIP=1 ./scripts/install-systemd.sh
```

After a successful installation, the script prints directly runnable next steps.
These include:

- The Web UI URL.
- The `sudo cat .../initial-password.txt` command derived from the current `auth.users_file`.
- The `mnemonas-doctor` diagnostic command.
- Log inspection commands.

If the installer fails while reloading, enabling, or starting the systemd services, it prints the failed stage and the relevant diagnostic commands.
Those commands can include `systemctl cat`, `systemctl status`, or `journalctl`.
Keep the config and data directories in place, inspect the reported unit or service logs, then rerun the installer after fixing the issue.

If no release archive is available, build from source:

```bash
git clone https://github.com/seanbao/mnemonas.git
cd mnemonas

make deps
make build
sudo env RELEASE_DIR="$PWD" ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

Source builds require Go, Rust, Node.js, and protoc. See [development](development.en.md).

## First Login

Run diagnostics:

```bash
sudo mnemonas-doctor
```

Read the default initial password:

```bash
sudo cat /srv/mnemonas/.mnemonas/initial-password.txt
```

Open it on a LAN, on the server itself, or through an SSH tunnel:

```text
http://<server-ip>:8080
```

For public-domain access, do not expose `8080` directly. Follow the [Public server quickstart](public-server-quickstart.en.md) to configure an HTTPS reverse proxy.

Change the administrator password after the first login. `mnemonas-doctor` reports if the initial-password file still exists.

## Administrator Password Recovery

When an existing enabled administrator loses the password, stop the service and run recovery locally on the server. This example recovers the `admin` account:

```bash
sudo systemctl stop mnemonas
sudo -u mnemonas /usr/local/bin/nasd \
  --config /etc/mnemonas/config.toml \
  --recover-admin admin
sudo cat /srv/mnemonas/.mnemonas/initial-password.txt
sudo systemctl start mnemonas
```

The recovery command does not accept a password and does not print the generated temporary password. It prints only the administrator username, credential-file path, and non-sensitive status information. The random temporary password is stored in `initial-password.txt` with mode `0600`. If `auth.users_file` is customized, the credential file is stored next to that users file; use the path reported by the command.

The configuration must keep `auth.enabled = true`. The target must be an existing enabled account with the `admin` role. Recovery revokes all existing sessions for that account. The temporary password requires an immediate password change after login, and the credential file is removed after the password change succeeds.

The `nasd` service and recovery command both exclusively acquire `auth-state.lock` in the authentication-state directory. Root or the `mnemonas` service account must own the authentication-state path, that directory must not be writable by group or other users, and no ancestor may be replaceable by another local account. Recovery is rejected when the service is still running, the directory permissions are unsafe, or another recovery command owns the lock. After an interruption, keep the service stopped and rerun the command with the same administrator username; a pending, conflicting, or malformed marker blocks normal startup, and the recovery marker makes the operation safely resumable. MnemoNAS does not expose an anonymous or remote HTTP administrator-recovery endpoint.

## Daily Operations

```bash
systemctl status mnemonas --no-pager
systemctl status mnemonas-dataplane --no-pager

journalctl -u mnemonas -f
journalctl -u mnemonas-dataplane -f

sudo systemctl restart mnemonas
sudo systemctl restart mnemonas-dataplane
sudo mnemonas-doctor
```

`mnemonas-doctor` checks service state, Web UI, the config file, runtime-sensitive files, directory permissions, filesystem type, free space, and backup-root placement.
A non-regular `config.toml` is a failure.
`config.toml` must parse as TOML; the doctor reports syntax errors independently even if an older binary or wrapper lets `nasd --check-config` pass.
A config path that is a symlink, passes through symlink components, or is broadly readable is reported as a risk.

When authentication is enabled, the doctor reports a missing `users.json`.
It also reports risks when `users.json`, `secrets.json`, or their relevant parent directories are unsafe.
Unsafe cases include symlinks, symlink path components, unexpected file types, and broad read permissions.

When `BACKUP_ROOT` exists, it must not equal or live under `storage.root`.
The doctor also reports a risk when `BACKUP_ROOT` is unsafe.
Unsafe cases include a symlink, a non-directory path, or the same filesystem source as `storage.root`.
They also include no write access for the service user or current diagnostic environment.
This keeps backup targets separate from the live storage root and confirms they are usable by the service.
Use a separate disk, dataset, or remote mount path for backup targets.

The Web UI health and storage pages also show storage-backing details.
These include filesystem type, mount point, device or dataset source, redacted mount options, native-checksum hints, and space-alert runtime state.
Administrators can download a diagnostic bundle from the health page and copy a storage-backing summary from the storage page for troubleshooting records.
By default, the free-space check warns below 10 GiB; set `MIN_FREE_BYTES=<bytes> sudo mnemonas-doctor` to adjust the threshold.

If UFW is installed, `mnemonas-doctor` also checks whether the firewall is enabled and warns when dataplane ports `9090/9091` appear exposed.

After config changes:

```bash
sudo nasd --check-config --config /etc/mnemonas/config.toml
sudo systemctl restart mnemonas-dataplane
sudo systemctl restart mnemonas
```

`--check-config` reports hard errors and security warnings. Do not ignore warnings for long-running deployments.

`[dataplane.cdc]` and `dataplane.grpc_address` are read when the dataplane starts. When a Python TOML parser is available, the startup helper rejects invalid `config.toml` syntax before reading dataplane arguments from a partial file. Restart `mnemonas-dataplane` after changing them, then restart `mnemonas`.

## Network Guidance

For long-running deployments, start with trusted-network-only access.

- Prefer LAN, Tailscale, Headscale, or another private network for management.
- Do not expose SSH directly to the public internet.
- For external sharing, expose only HTTPS through a reverse proxy.
- Configure `server.trusted_proxy_hops` correctly behind Caddy, Nginx, Traefik, or another trusted proxy.

If the target is public-domain access, follow the [Public server quickstart](public-server-quickstart.en.md).
That path binds the MnemoNAS backend to `127.0.0.1:8080` and exposes only Caddy/Nginx `80/443` publicly.

Recommended boundary:

| Link | Purpose | Recommendation |
| --- | --- | --- |
| LAN / private network | Admin, SSH, authorized user access | Allow `8080` only from trusted ranges |
| HTTPS reverse proxy / tunnel | Public share links | Expose only `80/443` |
| Dataplane `9090/9091` or custom ports | Internal nasd-to-dataplane traffic | Keep loopback-only or container-internal |

UFW example:

```bash
sudo ufw allow from 192.168.0.0/16 to any port 22 proto tcp comment "SSH LAN"
sudo ufw allow from 100.64.0.0/10 to any port 22 proto tcp comment "SSH Tailnet"
sudo ufw allow from 192.168.0.0/16 to any port 8080 proto tcp comment "MnemoNAS LAN"
sudo ufw allow from 100.64.0.0/10 to any port 8080 proto tcp comment "MnemoNAS Tailnet"
sudo ufw deny 9090/tcp comment "MnemoNAS dataplane gRPC"
sudo ufw deny 9091/tcp comment "MnemoNAS dataplane HTTP"
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
sudo ufw status numbered
```

When `SERVER_PORT`, `DATAPLANE_GRPC_ADDR`, or `DATAPLANE_HTTP_ADDR` are changed, replace the example ports with the actual ports.

If the reverse proxy and MnemoNAS run on the same machine, set `[server].host = "127.0.0.1"` when LAN direct access is not needed.

WebDAV URL:

```text
http://<server-ip>:8080/dav
```

WebDAV credentials depend on `[webdav].auth_type`.
`users` mode uses MnemoNAS usernames and passwords.
The default `basic` mode uses separate WebDAV credentials.

After installation, the running Web UI exposes the active WebDAV URL, Basic username, and readable generated password on the Settings -> WebDAV tab.
Custom Basic passwords are not echoed back and should come from the config file or password manager.
Generated Basic Auth passwords are also available on the server at `<storage.root>/secrets.json`.

## Backup Strategy

RAID and mirrors are not backups. Keep at least one independent backup.

Minimal cold backup:

```bash
sudo mkdir -p /backup/mnemonas
sudo systemctl stop mnemonas
sudo systemctl stop mnemonas-dataplane
sudo rsync -aHAX --delete /srv/mnemonas/ /backup/mnemonas/
sudo systemctl start mnemonas-dataplane
sudo systemctl start mnemonas
```

If the underlying filesystem supports snapshots, back up from a snapshot instead. See [backup guide](backup-guide.en.md).

## Upgrade

Download a newer release archive and rerun the installer. Existing config and data are preserved:

```bash
tar -xzf mnemonas-<version>-linux-amd64.tar.gz
cd mnemonas-<version>-linux-amd64

sudo ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

Back up before large version jumps.
Keep the extracted previous release directory as well.
If an upgrade fails to start, breaks a core workflow, or fails `mnemonas-doctor`, roll back to the previous version:

```bash
cd mnemonas-<previous-version>-linux-amd64
sudo ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

Rollback replaces the binaries and Web UI assets while continuing to use the existing `/etc/mnemonas/config.toml` and `/srv/mnemonas` data directory.
If the newer release performed an irreversible data migration, follow that release note or restore from backup before starting the older version.
Do not point an older binary at migrated data without an explicit compatibility statement.

## Uninstall

Default uninstall removes systemd services, binaries, and Web UI assets while keeping config and data:

```bash
sudo mnemonas-uninstall-systemd
```

Only remove config and data after verifying backups:

```bash
sudo env REMOVE_CONFIG=1 REMOVE_DATA=1 CONFIRM_REMOVE_DATA=/srv/mnemonas mnemonas-uninstall-systemd
```

The uninstaller also refuses binary, shared-resource, config, systemd-unit, and data paths that contain symlink components.
When removing config or data, the target directory must not be a symlink or pass through symlink components.
When removing data, `CONFIRM_REMOVE_DATA` must exactly match `STORAGE_ROOT` to avoid deleting a real mount point or a replaced directory tree by mistake.

The service account is kept by default to preserve UID/GID reuse. Set `REMOVE_SERVICE_USER=1` to remove it as part of uninstall.

## Troubleshooting

Start with:

```bash
sudo mnemonas-doctor
```

Common checks:

| Symptom | Check |
| --- | --- |
| Web UI does not open | `systemctl status mnemonas`, firewall, port conflict |
| Administrator password is unavailable | Stop `mnemonas`, run `nasd --config /etc/mnemonas/config.toml --recover-admin <administrator-username>`, then read the reported `initial-password.txt` |
| Writes fail after login | Ownership of `/srv/mnemonas` and `/etc/mnemonas` |
| WebDAV cannot connect | URL ends with `/dav`; credentials match the current `[webdav].auth_type` |
| Large upload fails | Disk space, reverse proxy upload limits, `journalctl -u mnemonas` |
| Scrub reports errors | Stop writes, preserve logs, check filesystem and backups |

Useful issue diagnostics:

```bash
sudo mnemonas-doctor
systemctl status mnemonas --no-pager
journalctl -u mnemonas --since "1 hour ago" --no-pager
```
