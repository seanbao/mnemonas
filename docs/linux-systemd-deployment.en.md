# Linux/systemd Deployment Guide

English | [简体中文](linux-systemd-deployment.md)

This guide targets long-running Linux server deployments. The goal is a small number of setup steps, systemd auto-start, useful logs, a stable data directory, and fast diagnostics when something fails.

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

If the same server also runs Docker, downloaders, transcoding, model caches, or other heavy services, do not put their data under `/srv/mnemonas`. Use a separate scratch path such as `/srv/fast-scratch`.

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

The systemd installer and uninstaller require absolute paths for `BIN_DIR`, `SHARE_DIR`, `CONFIG_DIR`, `CONFIG_PATH`, `SYSTEMD_DIR`, `STORAGE_ROOT`, and the Web UI directory, and none of those paths may contain symbolic-link components. `CONFIG_PATH` must stay under `CONFIG_DIR`; except for the Web UI directory living under `SHARE_DIR`, binary, shared-resource, config, systemd unit, and data paths must not overlap each other. To place data on a separate disk, mount the real filesystem at the target directory before running the installer; do not point `STORAGE_ROOT` at a symlink.

The installer only fixes ownership of the managed top-level directories by default. If you manually copied data and need a recursive ownership repair:

```bash
sudo env FIX_STORAGE_OWNERSHIP=1 ./scripts/install-systemd.sh
```

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

Read the initial password:

```bash
sudo cat /srv/mnemonas/.mnemonas/initial-password.txt
```

Open:

```text
http://<server-ip>:8080
```

Change the administrator password after the first login. `mnemonas-doctor` reports if the initial-password file still exists.

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

`mnemonas-doctor` checks service state, Web UI, directory permissions, filesystem type, and free space. It also warns when UFW appears to expose dataplane ports.

After config changes:

```bash
sudo nasd --check-config --config /etc/mnemonas/config.toml
sudo systemctl restart mnemonas-dataplane
sudo systemctl restart mnemonas
```

`--check-config` reports hard errors and security warnings. Do not ignore warnings for long-running deployments.

`[dataplane.cdc]` and `dataplane.grpc_address` are read when the dataplane starts. Restart `mnemonas-dataplane` after changing them, then restart `mnemonas`.

## Network Guidance

For long-running deployments, start with trusted-network-only access.

- Prefer LAN, Tailscale, Headscale, or another private network for management.
- Do not expose SSH directly to the public internet.
- For external sharing, expose only HTTPS through a reverse proxy.
- Configure `server.trusted_proxy_hops` correctly behind Caddy, Nginx, Traefik, or another trusted proxy.

Recommended boundary:

| Link | Purpose | Recommendation |
| --- | --- | --- |
| LAN / private network | Admin, SSH, authorized user access | Allow `8080` only from trusted ranges |
| HTTPS reverse proxy / tunnel | Public share links | Expose only `80/443` |
| Dataplane `9090/9091` | Internal nasd-to-dataplane traffic | Keep loopback-only or container-internal |

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

If the reverse proxy and MnemoNAS run on the same machine, set `[server].host = "127.0.0.1"` when LAN direct access is not needed.

WebDAV URL:

```text
http://<server-ip>:8080/dav
```

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

## Uninstall

Default uninstall removes systemd services, binaries, and Web UI assets while keeping config and data:

```bash
sudo mnemonas-uninstall-systemd
```

Only remove config and data after verifying backups:

```bash
sudo env REMOVE_CONFIG=1 REMOVE_DATA=1 CONFIRM_REMOVE_DATA=/srv/mnemonas mnemonas-uninstall-systemd
```

The uninstaller also refuses install and data paths that contain symlink components. When removing data, `CONFIRM_REMOVE_DATA` must exactly match `STORAGE_ROOT` to avoid deleting a real mount point or a replaced directory tree by mistake.

The service account is kept by default to preserve UID/GID reuse. Set `REMOVE_SERVICE_USER=1` if you also want to remove it.

## Troubleshooting

Start with:

```bash
sudo mnemonas-doctor
```

Common checks:

| Symptom | Check |
| --- | --- |
| Web UI does not open | `systemctl status mnemonas`, firewall, port conflict |
| Writes fail after login | Ownership of `/srv/mnemonas` and `/etc/mnemonas` |
| WebDAV cannot connect | URL ends with `/dav`; current WebDAV credentials are used |
| Large upload fails | Disk space, reverse proxy upload limits, `journalctl -u mnemonas` |
| Scrub reports errors | Stop writes, preserve logs, check filesystem and backups |

Useful issue diagnostics:

```bash
sudo mnemonas-doctor
systemctl status mnemonas --no-pager
journalctl -u mnemonas --since "1 hour ago" --no-pager
```
