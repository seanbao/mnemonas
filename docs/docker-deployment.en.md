# Docker Deployment Guide

English | [简体中文](docker-deployment.md)

This guide explains how to run MnemoNAS with Docker Compose. Docker is a good fit for quick evaluation, existing container hosts, and setups where MnemoNAS is managed alongside other services.

For long-running deployments that should auto-start on boot and use systemd logs, prefer [Linux/systemd deployment](linux-systemd-deployment.en.md).

## Requirements

- Docker 20.10+.
- Docker Compose v2 plugin, invoked as `docker compose`.
- Docker Buildx plugin for local source builds.
- At least 1GB free memory.
- SSD storage is recommended.

Check:

```bash
docker --version
docker compose version
docker buildx version
```

On Ubuntu 24.04 or recent Debian, distribution packages are often:

```bash
sudo apt update
sudo apt install -y docker-compose-v2 docker-buildx
```

When using Docker's official apt repository, package names are usually:

```bash
sudo apt update
sudo apt install -y docker-compose-plugin docker-buildx-plugin
```

Do not install the old Python `docker-compose` v1 package for this repository.

When `apt update` fails because an extra foreign architecture is enabled for a repository that does not provide it, a host-architecture-only install can be used temporarily:

```bash
sudo apt-get -o APT::Architectures=amd64 update
sudo apt-get -o APT::Architectures=amd64 install -y docker-compose-v2 docker-buildx
```

## Quick Start

Clone the repository:

```bash
git clone https://github.com/seanbao/mnemonas.git
cd mnemonas
```

The bundled `docker-compose.yml` builds `mnemonas:local` from the current source checkout.
The host does not need Go, Rust, or Node.js, but Docker must be able to pull Rust, Node, Go, and Debian base images.
After public release images are available, switch to GHCR as described in the release image section.

Prepare and start:

```bash
./scripts/docker-quickstart.sh --start
```

Use another host port if `8080` is busy:

```bash
./scripts/docker-quickstart.sh --port 8888 --start
```

Prepare only, without starting:

```bash
./scripts/docker-quickstart.sh
```

The script:

- Creates or updates `.env`.
- Writes `MNEMONAS_UID` and `MNEMONAS_GID` from the current host user.
- Creates `MNEMONAS_DATA_DIR`.
- Runs Docker preflight checks.
- Starts Compose when `--start` is used.
- With `MNEMONAS_IMAGE=mnemonas:local`, runs a local build.
- With a release image tag, uses `docker compose up --pull missing --no-build` to pull a missing image and prevent local builds.
- After start, waits for the local `/health` endpoint before reporting the service as ready.
- Prints directly runnable next steps, including the Web UI URL, health check command, initial-password read command, WebDAV URL, Compose status command, and log command.

First startup creates persistent config under `<MNEMONAS_DATA_DIR>/config.toml`. The initial Web UI password is stored at:

```text
<MNEMONAS_DATA_DIR>/.mnemonas/initial-password.txt
```

If `[auth].users_file` is customized, `initial-password.txt` is stored next to that users file instead.

The generated WebDAV Basic Auth password is stored in:

```text
<MNEMONAS_DATA_DIR>/secrets.json
```

The running Web UI exposes the active WebDAV URL, Basic username, and readable generated password on the Settings -> WebDAV tab.
Custom Basic passwords are not echoed back.

For regular multi-user mounting, `[webdav].auth_type = "users"` is preferred.
If `basic` mode remains in use, set `[webdav].password` to a strong random value from a password manager.

## Data Directory and User IDs

The container runs as a non-root user. Container data path is `/data`, mapped by default to the host's `~/.mnemonas`.

Prefer this helper when changing data location:

```bash
./scripts/docker-quickstart.sh --data-dir /path/to/mnemonas --start
```

Docker config should usually keep:

```toml
[storage]
root = "/data"
```

Do not set `root = "/"`. When another container path such as `/data-root` is used, mount that path explicitly in Compose or the data will land in the container's writable layer.

Docker quickstart, the container start entrypoint, and the preflight script require an absolute data directory.
They reject:

- Control characters.
- `..` path segments.
- Protected system directories.
- Symlink path components.

These checks prevent config and object data from being written through a replaced or overly broad directory.
The bundled Compose file uses long bind-mount syntax for `/data`, preventing `:` in a host path from being parsed as the volume target or mode.
Custom Compose snippets should use long bind-mount syntax as well.

Before creating directories or changing permissions, the container start entrypoint checks `STORAGE_ROOT/files` and `STORAGE_ROOT/.mnemonas/objects`.
Those managed subdirectories must not point elsewhere through symlinks.

Docker preflight checks existing `config.toml`, `.mnemonas/`, `.mnemonas/users.json`, `.mnemonas/initial-password.txt`, and `secrets.json`.
It rejects symlinks or unexpected file types, warns about broad permissions, and rejects a relative `[storage].root`.
When Python `tomllib` is available, it also validates existing `config.toml` TOML syntax.

When `auth.users_file` is customized under the container `/data` mount, preflight also checks that users file and its sibling `initial-password.txt` for type and permission issues.
When it is explicitly customized outside `/data`, preflight warns that it cannot inspect that users file or initial-password file from the host data directory.
Preflight reports only paths and permission state, not password or secret contents.

Container `CONFIG_PATH` must be absolute, stay under `STORAGE_ROOT`, and not contain control characters, parent-directory segments, or symlink path components.
The default is `/data/config.toml`.
When that file already exists, the container start entrypoint reads it in place and does not rewrite its comments, formatting, or field order.

Docker containers use `[dataplane].grpc_address` in `config.toml` as the only source of truth for the internal gRPC address.
Startup rejects a divergent `DATAPLANE_GRPC_ADDR` environment value so the control plane and dataplane cannot use different endpoints.
When customizing host storage, mount a real directory rather than a symlink.

Custom `--env` paths must point to a file in an existing directory. The script does not implicitly create the `.env` parent directory, so invalid input fails before creating the data directory.

When the host user is not UID/GID `1000`, use the quickstart script or pass the IDs directly:

```bash
MNEMONAS_UID="$(id -u)" MNEMONAS_GID="$(id -g)" docker compose up -d --build
```

## Manual Start

```bash
docker compose up -d --build
```

The bundled Compose file publishes only `8080`, which serves Web UI, REST API, and WebDAV. Dataplane ports `9090/9091` are internal. Do not add them to `ports:` and do not expose them through a reverse proxy.

`init: true` is enabled so the container has a minimal init process for signal forwarding and child-process reaping. Keep it for long-running deployments.

`./scripts/docker-quickstart.sh --start` waits for `http://127.0.0.1:<port>/health` by default.
If Compose startup fails or the health check times out, the script prints the matching `docker compose ps` or `docker compose logs --tail 100 mnemonas` diagnostic command.

The bundled `/app/mnemonas-healthcheck` command requests `http://127.0.0.1:8080/health` by default.
When `MNEMONAS_HEALTHCHECK_URL` overrides the target, it must be an absolute `http` or `https` URL and must not contain whitespace, control characters, embedded credentials, or a fragment.

For a remote Docker context, SSH tunnel, or another environment where the host cannot reach the published port locally, pass `--skip-health-check`.
Then verify service state with `docker compose ps`, `docker compose logs --tail 100 mnemonas`, or the reachable `/health` endpoint.

After the health check passes, quickstart derives the `initial-password.txt` location from the existing `config.toml`.
When the path is under the container `/data` mount, it reports the corresponding host path and includes a `cat` command in the next steps.
When the path is outside `/data`, it reports the container path and includes a `docker compose exec` read command.

The script does not print the password.
Existing deployments may have removed that file after the first login.
A new deployment without that file should be investigated through the container logs.

Verify:

```bash
curl "http://localhost:${MNEMONAS_HTTP_PORT:-8080}/health"
docker compose logs -f
```

## Manual Image Build

To test the Dockerfile directly:

```bash
docker build \
  --build-arg VERSION=local \
  --build-arg BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t mnemonas:local .
mkdir -p "$HOME/.mnemonas"
docker run --rm --user "$(id -u):$(id -g)" -p 8080:8080 \
  --mount type=bind,source="$HOME/.mnemonas",target=/data \
  mnemonas:local
```

The host data directory must exist before the bind mount is used.
If the directory is absent, Docker can create it as root, which prevents the non-root container user from writing `/data/config.toml` on first startup.
Only `8080` needs to be published.

For a local source build followed by a loopback container smoke test, run:

```bash
make docker-check
```

This target builds `mnemonas:latest`, then runs `scripts/docker-smoke.sh` with a temporary `127.0.0.1` port and checks `/health` plus the frontend root page. When `MNEMONAS_DOCKER_SMOKE_HOST` overrides the published host, it only accepts dotted-quad `127.0.0.0/8` loopback IPv4 addresses.

Build base images can be overridden for private caches or regional mirrors:

```bash
docker build -t mnemonas:local \
  --build-arg NODE_IMAGE=node:22-bookworm-slim \
  --build-arg GO_IMAGE=golang:1.25.11-alpine \
  --build-arg RUST_IMAGE=rust:1.92 \
  --build-arg RUNTIME_IMAGE=debian:bookworm-slim \
  --build-arg VERSION=local \
  --build-arg BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  .
```

## Release Images

After public release images are available, set the image tag in `.env`:

```bash
MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas:<version>
```

Prefer an explicit version tag.
Use `latest` only for temporary evaluation or environments that intentionally accept automatic upgrades.
Docker preflight warns when a release image has no explicit tag, no digest, or uses `latest`, so upgrades and rollbacks can return to a known image.

The bundled Compose file still includes local build settings. The quickstart script adds `--pull missing --no-build` for release image tags automatically. Manual starts should use:

```bash
docker compose up -d --pull missing --no-build
```

Binary archives from GitHub Releases include `docker-compose.yml` and `.env.example`.
The packaged template presets `MNEMONAS_IMAGE` to the GHCR image for the same release tag.
Running `./scripts/docker-quickstart.sh --start` from an extracted archive therefore uses the release-image path by default instead of a source build.

After a release is published, verify the GitHub Release archives, `checksums.txt`, and matching GHCR image tag from the download directory:

```bash
mkdir -p dist/release-check
gh release download v1.2.3 \
  --repo seanbao/mnemonas \
  --dir dist/release-check

./scripts/verify-release-artifacts.sh \
  --version v1.2.3 \
  --repository seanbao/mnemonas \
  --require-targets \
  --check-image \
  dist/release-check
```

`--check-image` uses Docker to check that `ghcr.io/seanbao/mnemonas:1.2.3` exists.
Omit it when only the downloaded archives and checksums need offline verification.
`--repository` must use a GHCR-compatible lowercase `owner/repo`; owner only allows lowercase letters, digits, and hyphens, while the repo name also allows dots and underscores, and both segments must start and end with a letter or digit.

The examples below default to the source-built local image and can be switched to a verified release image with `MNEMONAS_IMAGE`.

## Media Archive Example

```yaml
services:
  mnemonas:
    image: ${MNEMONAS_IMAGE:-mnemonas:local}
    container_name: mnemonas
    user: "${MNEMONAS_UID:-1000}:${MNEMONAS_GID:-1000}"
    ports:
      - "${MNEMONAS_HTTP_PORT:-8080}:8080"
    volumes:
      - type: bind
        source: ${HOME}/.mnemonas
        target: /data
        bind:
          create_host_path: true
    environment:
      - TZ=Asia/Shanghai
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "/app/mnemonas-healthcheck"]
      interval: 30s
      timeout: 10s
      retries: 3
```

Config:

```toml
[server]
host = "0.0.0.0"
port = 8080

[storage]
root = "/data"

[storage.retention]
max_versions = 50
max_age = "17520h"

[webdav]
enabled = true
prefix = "/dav"
auth_type = "basic"
username = "webdav"
password = "" # leave empty to use generated credentials; use a password-manager value for custom credentials

[log]
level = "info"
```

## Local Developer Workstation Example

Bind the host port to loopback:

```yaml
services:
  mnemonas:
    image: ${MNEMONAS_IMAGE:-mnemonas:local}
    container_name: mnemonas-dev
    user: "${MNEMONAS_UID:-1000}:${MNEMONAS_GID:-1000}"
    ports:
      - "127.0.0.1:${MNEMONAS_HTTP_PORT:-8080}:8080"
    volumes:
      - type: bind
        source: ${HOME}/.mnemonas
        target: /data
        bind:
          create_host_path: true
    restart: unless-stopped
```

Do not use `server.host = "127.0.0.1"` inside the container to limit host access; that binds to the container's loopback.
Restrict host exposure in the Compose port mapping instead.

When WebDAV authentication is also disabled for a loopback-only developer container, keep `server.host = "0.0.0.0"` inside the container.
Set `security.allow_unsafe_no_auth = true` to explicitly confirm that the host port mapping is the access boundary.

## Multi-User NAS Example

Admins can create users in the Web UI and set groups, `home_dir`, user quotas, and directory access rules.
Non-admin users are scoped to that configured root unless a matching directory rule grants shared access.

File browsing, search, favorites, shares, trash, activity, and WebDAV `users` mode use the same boundary.
Web/API uploads, copies, moves, and trash restores into `home_dir` honor the configured user quota.
Shared-path capacity limits are handled by directory quotas.

```yaml
services:
  mnemonas:
    image: ${MNEMONAS_IMAGE:-mnemonas:local}
    container_name: shared-nas
    user: "${MNEMONAS_UID:-1000}:${MNEMONAS_GID:-1000}"
    ports:
      - "${MNEMONAS_HTTP_PORT:-8080}:8080"
    volumes:
      - type: bind
        source: ${HOME}/.mnemonas
        target: /data
        bind:
          create_host_path: true
    environment:
      - TZ=Asia/Shanghai
    restart: always
    deploy:
      resources:
        limits:
          memory: 2G
        reservations:
          memory: 512M
```

## HTTPS with Nginx

```yaml
services:
  mnemonas:
    image: ${MNEMONAS_IMAGE:-mnemonas:local}
    container_name: mnemonas
    user: "${MNEMONAS_UID:-1000}:${MNEMONAS_GID:-1000}"
    expose:
      - "8080"
    volumes:
      - type: bind
        source: ${HOME}/.mnemonas
        target: /data
        bind:
          create_host_path: true
    restart: unless-stopped
    networks:
      - internal

  nginx:
    image: nginx:alpine
    container_name: nginx
    ports:
      - "443:443"
      - "80:80"
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf:ro
      - ./certs:/etc/nginx/certs:ro
    depends_on:
      - mnemonas
    restart: unless-stopped
    networks:
      - internal

networks:
  internal:
```

Important Nginx settings:

```nginx
client_max_body_size 0;
proxy_set_header Host $host;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header X-Forwarded-Proto $scheme;
proxy_pass_request_headers on;
proxy_set_header Destination $http_destination;
```

Set `server.trusted_proxy_hops = 1` in MnemoNAS config when Nginx is the only trusted proxy in front of the app.
Because a Docker bridge proxy usually reaches MnemoNAS from a non-loopback address, also trust the Docker network source.
Inspect the actual subnet with `docker network inspect <compose-project>_internal`, then configure:

```toml
[server]
trusted_proxy_hops = 1
trusted_proxy_cidrs = ["172.18.0.0/16"] # replace with the actual Docker network subnet or nginx container IP
```

## Traefik Example

```yaml
services:
  mnemonas:
    image: ${MNEMONAS_IMAGE:-mnemonas:local}
    user: "${MNEMONAS_UID:-1000}:${MNEMONAS_GID:-1000}"
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.mnemonas.rule=Host(`nas.example.com`)"
      - "traefik.http.routers.mnemonas.tls=true"
      - "traefik.http.routers.mnemonas.tls.certresolver=letsencrypt"
      - "traefik.http.services.mnemonas.loadbalancer.server.port=8080"
    volumes:
      - type: bind
        source: ${HOME}/.mnemonas
        target: /data
        bind:
          create_host_path: true
    networks:
      - traefik-network
```

## Monitoring and Logs

```bash
docker compose logs -f mnemonas
docker compose logs --tail 100 mnemonas
docker compose logs mnemonas > mnemonas.log
```

The container reads `[dataplane.cdc]` from `/data/config.toml` at startup and passes it to dataplane. Restart the container after changing CDC settings:

```bash
docker compose restart mnemonas
```

Health:

```bash
docker inspect --format='{{.State.Health.Status}}' mnemonas
curl "http://localhost:${MNEMONAS_HTTP_PORT:-8080}/health"
```

`/api/v1/metrics` returns JSON metrics.
Prometheus needs a JSON exporter, custom exporter, or conversion layer.
If `auth.enabled = true`, the scraper must authenticate as an admin.

## Upgrade and Backup

Source checkout:

```bash
docker compose build --pull
docker compose up -d
```

Release image:

```bash
docker compose pull
docker compose up -d --no-build
```

Before upgrading a release image, record the current `MNEMONAS_IMAGE` tag in `.env` and complete a backup.
If the upgraded container does not start, a core workflow regresses, or health checks fail, set `MNEMONAS_IMAGE` back to the previous tag and roll the container image back:

```bash
docker compose pull
docker compose up -d --no-build
docker compose logs --tail 100 mnemonas
```

Rollback changes only the container image.
It continues to use the same host data directory and `/data/config.toml` inside the container.
If the newer release performed an irreversible data migration, follow that release note or restore from backup before starting the older image.

Cold backup:

```bash
docker compose stop
tar czf mnemonas-backup-$(date +%Y%m%d).tar.gz ~/.mnemonas
docker compose start
```

Restore:

```bash
docker compose down
DEFAULT_DATA_DIR="$HOME/.mnemonas"
DATA_DIR="${MNEMONAS_DATA_DIR:-$DEFAULT_DATA_DIR}"
[ "$DATA_DIR" = "$DEFAULT_DATA_DIR" ] || { echo "refusing non-default DATA_DIR; inspect and delete manually: $DATA_DIR"; exit 1; }
case "$DATA_DIR" in *$'\n'*|*$'\r'*|*"/../"*|*"/.."|"../"*|"..") echo "refusing unsafe DATA_DIR: $DATA_DIR"; exit 1 ;; esac
[ ! -L "$DATA_DIR" ] || { echo "refusing symlink DATA_DIR: $DATA_DIR"; exit 1; }
rm -rf -- "$DATA_DIR"
tar xzf mnemonas-backup-YYYYMMDD.tar.gz -C ~

# For release images, use docker compose up -d --no-build instead.
docker compose up -d
```

## Troubleshooting

Container does not start:

```bash
docker compose logs mnemonas
docker run --rm --entrypoint /app/nasd \
  --user "$(id -u):$(id -g)" \
  --mount type=bind,source="$HOME/.mnemonas",target=/data \
  "${MNEMONAS_IMAGE:-mnemonas:local}" --check-config --config /data/config.toml
```

Slow base-image pulls:

- Prefer the public release image when available.
- Use Buildx cache.
- Build on a better network and transfer with `docker save` / `docker load`.
- Override base images with internal mirrors.

Permission problems:

```bash
ls -la ~/.mnemonas
sudo chown -R 1000:1000 ~/.mnemonas
chmod 750 ~/.mnemonas
```

Use the actual UID/GID from `.env` if they are not `1000`.

Port conflict:

```bash
sudo lsof -i :8080
sed -i "s/^MNEMONAS_HTTP_PORT=.*/MNEMONAS_HTTP_PORT=8888/" .env
./scripts/mnemonas-docker-preflight.sh

# For release images, use docker compose up -d --no-build instead.
docker compose up -d
```

## More Resources

- [Mounting guide](mounting-guide.en.md)
- [FAQ](faq.en.md)
- [Configuration reference](configuration.en.md)
