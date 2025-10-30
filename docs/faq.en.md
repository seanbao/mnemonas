# Frequently Asked Questions

English | [简体中文](faq.md)

## Installation and Deployment

### Which operating systems are supported?

| Platform | Status |
| --- | --- |
| Linux x86_64 | Primary path for long-running deployments |
| Linux ARM64 | Primary path for long-running deployments |
| macOS Apple Silicon | Fully supported for development and local runs |
| macOS Intel | Fully supported for development and local runs |
| Windows | Supported through WSL2 |

### What is the difference between systemd, Docker, and manual binaries?

| Method | Strengths | Tradeoffs |
| --- | --- | --- |
| Linux/systemd | Auto-start, clear logs, good diagnostics, best for long-running servers | Linux host focused |
| Docker | Short setup, isolated runtime, straightforward upgrades | Requires Docker and correct volume mapping |
| Manual binaries | Simple for debugging | Manual process management |

For long-running service deployments, use [Linux/systemd deployment](linux-systemd-deployment.en.md). For quick evaluation or existing container hosts, use [Docker deployment](docker-deployment.en.md).

### How do I upgrade?

Docker source checkout:

```bash
docker compose build --pull
docker compose up -d
```

Docker release image:

```bash
docker compose pull
docker compose up -d --no-build
```

Before upgrading a Docker release image, record the current `MNEMONAS_IMAGE` tag in `.env`. If the upgraded container does not start, a core workflow regresses, or health checks fail, set `MNEMONAS_IMAGE` back to the previous tag and run:

```bash
docker compose pull
docker compose up -d --no-build
docker compose logs --tail 100 mnemonas
```

Docker rollback changes only the image and keeps using the same host data directory. If the newer release performed an irreversible data migration, follow that release note or restore from backup first.

Ubuntu/systemd:

```bash
tar -xzf mnemonas-<version>-linux-amd64.tar.gz
cd mnemonas-<version>-linux-amd64

sudo ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

For systemd upgrades, back up first and keep the extracted previous release directory. If the upgraded service does not start, a core workflow regresses, or `mnemonas-doctor` fails, rerun the installer from the previous release directory to roll binaries and Web UI assets back:

```bash
cd mnemonas-<previous-version>-linux-amd64
sudo ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

Rollback continues to use the existing `/etc/mnemonas/config.toml` and `/srv/mnemonas` data directory. If the newer release performed an irreversible data migration, follow that release note or restore from backup before starting the older version.

Manual binaries:

```bash
pkill nasd
pkill dataplane
./dataplane --data-dir ~/.mnemonas/.mnemonas/objects &
./nasd --config ~/.mnemonas/config.toml
```

Back up before major upgrades on every deployment path.

### Where is data stored?

Default direct-run layout:

- User files: `~/.mnemonas/files/`
- Internal data: `~/.mnemonas/.mnemonas/`
- Config: `~/.mnemonas/config.toml`

Ubuntu/systemd default:

- Data: `/srv/mnemonas`
- Config: `/etc/mnemonas/config.toml`

Docker default:

- Host `~/.mnemonas` maps to container `/data`
- Internal data is under `/data/.mnemonas`

## WebDAV

### WebDAV feels slow. What should I check?

Common causes:

- macOS Finder sends many `PROPFIND` requests. Try Transmit, Cyberduck, or rclone.
- Windows File Explorer has WebClient limitations. Try WinSCP, Cyberduck, Raidrive, or rclone.
- High network latency. Keep server and client on the same LAN for large file work.
- Reverse proxy buffering or small body limits. Disable buffering and raise upload limits for public HTTPS deployments.

MnemoNAS includes short PROPFIND caching, but client behavior still matters.

### Why does Windows fail to connect to HTTP WebDAV?

Windows prefers HTTPS. For local HTTP testing, run PowerShell as administrator:

```powershell
Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\WebClient\Parameters" -Name "BasicAuthLevel" -Value 2
Restart-Service WebClient
```

For regular use, deploy HTTPS.

### How do I enable WebDAV authentication?

```toml
[webdav]
enabled = true
prefix = "/dav"
auth_type = "users"
```

For a separate global WebDAV credential, use:

```toml
[webdav]
auth_type = "basic"
username = "admin"
password = "" # leave empty to use generated credentials; use a password-manager value for custom credentials
```

If `password` is empty, MnemoNAS generates a WebDAV password and stores it in `<storage.root>/secrets.json`.

### Is HTTPS supported?

Yes. Built-in TLS exists:

```toml
[server.tls]
enabled = true
auto_generate = true
```

For public access, use a reverse proxy:

```nginx
server {
    listen 443 ssl;
    server_name nas.example.com;

    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

See [reverse proxy setup](reverse-proxy-setup.en.md).

## Files and Storage

### I deleted a file by mistake. What can I do?

Use the Web UI trash and version history:

1. Open `http://localhost:8080`.
2. Go to the original directory.
3. Use trash or file version history.
4. Restore the desired item or version.

API example:

```bash
curl -H "Authorization: Bearer <access-token>" \
  http://localhost:8080/api/v1/versions/path/to/file

curl -X POST \
  -H "Authorization: Bearer <access-token>" \
  "http://localhost:8080/api/v1/versions/<hash>/restore?path=/path/to/file"
```

### How does deduplication work?

MnemoNAS stores version content in a BLAKE3 whole-object CAS today:

- Identical full-file content is stored once.
- The Rust dataplane has a FastCDC file API with typical chunk sizes in the 256KB-4MB range.
- The current Go version-history path has not yet connected chunk-level reference tracking, so different versions share storage only when their full content is identical.

Dataplane stats:

```bash
curl http://localhost:9091/stats
```

`9091` is a local/private dataplane health and stats port. Do not expose it publicly.

### How are old versions cleaned?

Retention config:

```toml
[storage.retention]
max_age = "720h"
max_versions = 10
```

Manual GC:

```bash
curl -X POST \
  -H "Authorization: Bearer <access-token>" \
  http://localhost:8080/api/v1/maintenance/gc
```

### What is the maximum file size?

MnemoNAS uses streaming paths. Practical limits come from disk space, clients, reverse proxy settings, and the underlying filesystem.

Large-file paths should be tested with the expected upload, download, and restore workload. Public deployments must configure reverse proxy settings such as Nginx `client_max_body_size`, `proxy_request_buffering`, and timeouts.

## Performance and Maintenance

### How do I monitor service status?

Health:

```bash
curl http://localhost:8080/health
```

Metrics:

```bash
curl -H "Authorization: Bearer <admin-access-token>" http://localhost:8080/api/v1/metrics
```

Dataplane local stats:

```bash
curl http://localhost:9091/stats
```

Keep dataplane ports loopback/private.

### What does scrub do?

Scrub verifies stored objects against their hashes and reports missing or corrupted data.

```bash
curl -X POST \
  -H "Authorization: Bearer <access-token>" \
  http://localhost:8080/api/v1/maintenance/scrub

curl -H "Authorization: Bearer <access-token>" \
  http://localhost:8080/api/v1/maintenance/scrub
```

Run scrub periodically, for example monthly.

### How should I back up MnemoNAS?

Use a consistent source:

1. Filesystem snapshot if using ZFS, Btrfs, or LVM.
2. Otherwise, stop both `mnemonas` and `mnemonas-dataplane`, back up the full storage root, then start them.

Cold Docker example:

```bash
docker compose stop
rsync -aHAX --delete ~/.mnemonas/ /backup/mnemonas/
docker compose start
```

Use restic, borg, or rclone from a snapshot or cold root for remote backups.

See [backup guide](backup-guide.en.md).

## Troubleshooting

### Service does not start

Check:

```bash
lsof -i :8080
lsof -i :9090
ls -la ~/.mnemonas/
```

Logs:

```bash
docker compose logs -f
./nasd 2>&1 | tee nasd.log
```

For systemd:

```bash
sudo mnemonas-doctor
journalctl -u mnemonas -f
journalctl -u mnemonas-dataplane -f
```

### Control plane cannot connect to dataplane

Check dataplane health:

```bash
curl http://localhost:9091/health
```

Check config:

```toml
[dataplane]
grpc_address = "localhost:9090"
```

Also check firewall and whether dataplane is bound to loopback.

### How do I reset all data?

This is destructive:

```bash
docker compose down
DEFAULT_DATA_DIR="$HOME/.mnemonas"
DATA_DIR="${MNEMONAS_DATA_DIR:-$DEFAULT_DATA_DIR}"
[ "$DATA_DIR" = "$DEFAULT_DATA_DIR" ] || { echo "refusing non-default DATA_DIR; inspect and delete manually: $DATA_DIR"; exit 1; }
[ ! -L "$DATA_DIR" ] || { echo "refusing symlink DATA_DIR: $DATA_DIR"; exit 1; }
rm -rf -- "$DATA_DIR/files" "$DATA_DIR/.mnemonas"

# For release images, use docker compose up -d --no-build instead.
docker compose up -d
```

Back up first when existing data may still be needed.

## More Help

- [README](../README.en.md)
- [Documentation index](README.en.md)
- [Mounting guide](mounting-guide.en.md)
- [WebDAV compatibility](webdav-compatibility.en.md)
- [Configuration reference](configuration.en.md)
- [Support](../SUPPORT.en.md)
