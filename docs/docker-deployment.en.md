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

If `apt update` fails because an extra foreign architecture is enabled for a repository that does not provide it, you can temporarily install for the host architecture:

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

The bundled `docker-compose.yml` builds `mnemonas:local` from the current source checkout. The host does not need Go, Rust, or Node.js, but Docker must be able to pull Rust, Node, Go, and Debian base images. After public release images are available, switch to GHCR as described in the release image section.

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
- Starts Compose with `docker compose up -d --build` when `--start` is used.

First startup creates persistent config under `<MNEMONAS_DATA_DIR>/config.toml`. The initial Web UI password is stored at:

```text
<MNEMONAS_DATA_DIR>/.mnemonas/initial-password.txt
```

The generated WebDAV password is stored in:

```text
<MNEMONAS_DATA_DIR>/secrets.json
```

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

Do not set `root = "/"`. If you set another container path such as `/data-root`, mount that path explicitly in Compose or the data will land in the container's writable layer.

Docker quickstart, the container start entrypoint, and the preflight script reject symlink components in the data directory path so config and object data cannot be written through a replaced directory. Container `CONFIG_PATH` must be absolute and stay under `STORAGE_ROOT`; the default is `/data/config.toml`. When customizing host storage, mount a real directory rather than a symlink.

If your host user is not UID/GID `1000`, use the quickstart script or pass the IDs directly:

```bash
MNEMONAS_UID="$(id -u)" MNEMONAS_GID="$(id -g)" docker compose up -d --build
```

## Manual Start

```bash
docker compose up -d --build
```

The bundled Compose file publishes only `8080`, which serves Web UI, REST API, and WebDAV. Dataplane ports `9090/9091` are internal. Do not add them to `ports:` and do not expose them through a reverse proxy.

`init: true` is enabled so the container has a minimal init process for signal forwarding and child-process reaping. Keep it for long-running deployments.

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
docker run --rm --user "$(id -u):$(id -g)" -p 8080:8080 -v "$HOME/.mnemonas:/data" mnemonas:local
```

Only `8080` needs to be published.

Build base images can be overridden for private caches or regional mirrors:

```bash
docker build -t mnemonas:local \
  --build-arg NODE_IMAGE=node:22-bookworm-slim \
  --build-arg GO_IMAGE=golang:1.25.9-alpine \
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

Prefer an explicit version tag. Use `latest` only for temporary evaluation or environments that intentionally accept automatic upgrades.

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
      - ${HOME}/.mnemonas:/data
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
password = "your-secure-password"

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
      - ${HOME}/.mnemonas:/data
    restart: unless-stopped
```

Do not use `server.host = "127.0.0.1"` inside the container to limit host access; that binds to the container's loopback. Restrict host exposure in the Compose port mapping instead. If you also disable WebDAV authentication for a loopback-only developer container, keep `server.host = "0.0.0.0"` inside the container and set `security.allow_unsafe_no_auth = true` to explicitly confirm that the host port mapping is the access boundary.

## Multi-User NAS Example

Admins can create users in the Web UI and set groups, `home_dir`, user quotas, and directory access rules. Non-admin users are scoped to that configured root unless a matching directory rule grants shared access; file browsing, search, favorites, shares, trash, activity, and WebDAV `users` mode use the same boundary. Web/API uploads, copies, and trash restores honor the configured quota.

```yaml
services:
  mnemonas:
    image: ${MNEMONAS_IMAGE:-mnemonas:local}
    container_name: shared-nas
    user: "${MNEMONAS_UID:-1000}:${MNEMONAS_GID:-1000}"
    ports:
      - "${MNEMONAS_HTTP_PORT:-8080}:8080"
    volumes:
      - ${HOME}/.mnemonas:/data
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
      - ${HOME}/.mnemonas:/data
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
      - ${HOME}/.mnemonas:/data
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

`/api/v1/metrics` returns JSON metrics. Prometheus needs a JSON exporter, custom exporter, or conversion layer. If `auth.enabled = true`, the scraper must authenticate as an admin.

## Upgrade and Backup

Source checkout:

```bash
docker compose build --pull
docker compose up -d
```

Release image:

```bash
docker compose pull
docker compose up -d
```

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
[ ! -L "$DATA_DIR" ] || { echo "refusing symlink DATA_DIR: $DATA_DIR"; exit 1; }
rm -rf -- "$DATA_DIR"
tar xzf mnemonas-backup-YYYYMMDD.tar.gz -C ~
docker compose up -d
```

## Troubleshooting

Container does not start:

```bash
docker compose logs mnemonas
docker run --rm --entrypoint /app/nasd \
  --user "$(id -u):$(id -g)" \
  -v "$HOME/.mnemonas:/data" \
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
docker compose up -d
```

## More Resources

- [Mounting guide](mounting-guide.en.md)
- [FAQ](faq.en.md)
- [Configuration reference](configuration.en.md)
