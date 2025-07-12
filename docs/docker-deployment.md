# Docker 部署指南

[English](docker-deployment.en.md) | 简体中文

本文档说明如何使用 Docker Compose 运行 MnemoNAS。Docker 适合快速评估、已有容器宿主机，以及需要和其他服务一起编排的环境。

如果部署需要长期运行、开机自启和 systemd 日志管理，优先使用 [Linux/systemd 部署指南](linux-systemd-deployment.md)。

## 前置要求

- Docker 20.10+。
- Docker Compose v2 插件，命令形式为 `docker compose`。
- Docker Buildx 插件，用于本地源码构建。
- 至少 1GB 可用内存。
- 建议使用 SSD 存储。

检查：

```bash
docker --version
docker compose version
docker buildx version
```

Ubuntu 24.04 或近期 Debian 通常使用发行版包：

```bash
sudo apt update
sudo apt install -y docker-compose-v2 docker-buildx
```

使用 Docker 官方 apt 仓库时，包名通常为：

```bash
sudo apt update
sudo apt install -y docker-compose-plugin docker-buildx-plugin
```

不要为本仓库安装旧版 Python `docker-compose` v1 包。

如果 `apt update` 因某个仓库不支持额外 foreign architecture 而失败，可临时按宿主机架构安装：

```bash
sudo apt-get -o APT::Architectures=amd64 update
sudo apt-get -o APT::Architectures=amd64 install -y docker-compose-v2 docker-buildx
```

## 快速开始

克隆仓库：

```bash
git clone https://github.com/seanbao/mnemonas.git
cd mnemonas
```

仓库自带的 `docker-compose.yml` 会从当前源码 checkout 构建 `mnemonas:local`。
宿主机不需要 Go、Rust 或 Node.js，但 Docker 必须能拉取 Rust、Node、Go 和 Debian 基础镜像。
公开 release 镜像可用后，可按“发布镜像”一节切换到 GHCR。

准备并启动：

```bash
./scripts/docker-quickstart.sh --start
```

如果宿主机 `8080` 已占用，可指定其他端口：

```bash
./scripts/docker-quickstart.sh --port 8888 --start
```

只准备环境、不启动服务：

```bash
./scripts/docker-quickstart.sh
```

脚本会：

- 创建或更新 `.env`。
- 从当前宿主机用户写入 `MNEMONAS_UID` 和 `MNEMONAS_GID`。
- 创建 `MNEMONAS_DATA_DIR`。
- 运行 Docker 预检。
- 使用 `--start` 时启动 Compose。
- 当 `MNEMONAS_IMAGE=mnemonas:local` 时执行本地构建。
- 当使用 release 镜像标签时，使用 `docker compose up --pull missing --no-build` 拉取缺失镜像并禁止本地构建。
- 启动后等待本机 `/health` 端点就绪。
- 输出可直接执行的下一步，包括 Web UI URL、health 检查命令、初始密码读取命令、WebDAV URL、Compose 状态命令和日志命令。

首次启动会在 `<MNEMONAS_DATA_DIR>/config.toml` 下创建持久化配置。初始 Web UI 密码位于：

```text
<MNEMONAS_DATA_DIR>/.mnemonas/initial-password.txt
```

如果 `[auth].users_file` 被自定义，`initial-password.txt` 会位于该 users 文件同目录。

自动生成的 WebDAV Basic Auth 密码位于：

```text
<MNEMONAS_DATA_DIR>/secrets.json
```

运行中的 Web UI 会在“设置 -> WebDAV”标签页显示当前 WebDAV URL、Basic 用户名和可读取的自动生成密码。
自定义 Basic 密码不会回显。

常规多用户挂载建议使用 `[webdav].auth_type = "users"`。
如果继续使用 `basic` 模式，应将 `[webdav].password` 设置为密码管理器生成的强随机值。

## 数据目录和用户 ID

容器以非 root 用户运行。容器内数据路径为 `/data`，默认映射到宿主机 `~/.mnemonas`。

修改数据位置时，优先使用辅助脚本：

```bash
./scripts/docker-quickstart.sh --data-dir /path/to/mnemonas --start
```

Docker 配置通常应保持：

```toml
[storage]
root = "/data"
```

不要设置 `root = "/"`。如果使用 `/data-root` 等其他容器内路径，必须在 Compose 中显式挂载该路径，否则数据会写入容器可写层。

Docker quickstart、容器启动入口和预检脚本都要求数据目录是绝对路径。
它们会拒绝：

- 控制字符。
- `..` 路径片段。
- 受保护系统目录。
- 符号链接路径组件。

这些检查避免配置和对象数据写入被替换或范围过宽的目录。
仓库自带 Compose 文件对 `/data` 使用长 bind-mount 语法，避免宿主机路径中的 `:` 被解析成卷目标或模式。
自定义 Compose 片段也应使用长 bind-mount 语法。

容器启动入口会在创建目录或修改权限前检查 `STORAGE_ROOT/files` 和 `STORAGE_ROOT/.mnemonas/objects`。
这些托管子目录不能通过符号链接指向其他位置。

Docker 预检会检查已有 `config.toml`、`.mnemonas/`、`.mnemonas/users.json`、`.mnemonas/initial-password.txt` 和 `secrets.json`。
它会拒绝符号链接或非预期文件类型，提示权限过宽，拒绝相对 `[storage].root`。
当 Python `tomllib` 可用时，还会校验已有 `config.toml` 的 TOML 语法。

当 `auth.users_file` 自定义到容器 `/data` 挂载内时，预检也会检查该 users 文件及同目录 `initial-password.txt` 的类型和权限。
当它被显式自定义到 `/data` 之外时，预检会提示无法从宿主机数据目录检查该 users 文件或初始密码文件。
预检只报告路径和权限状态，不输出密码或 secret 内容。

容器 `CONFIG_PATH` 必须是绝对路径，位于 `STORAGE_ROOT` 下，并且不能包含控制字符、父目录片段或符号链接路径组件。
默认值为 `/data/config.toml`。
当该文件已存在时，容器启动入口只读取配置，不会重写其注释、格式或字段顺序。

Docker 容器只以 `config.toml` 中的 `[dataplane].grpc_address` 作为内部 gRPC 地址来源。
启动时会拒绝不一致的 `DATAPLANE_GRPC_ADDR` 环境值，避免控制面和 dataplane 使用不同端点。
自定义宿主机存储时，应挂载真实目录而不是符号链接。

自定义 `--env` 路径必须指向已有目录中的文件。脚本不会隐式创建 `.env` 父目录，因此无效输入会在创建数据目录前失败。

如果宿主机用户不是 UID/GID `1000`，使用 quickstart 脚本，或直接传入当前用户 ID：

```bash
MNEMONAS_UID="$(id -u)" MNEMONAS_GID="$(id -g)" docker compose up -d --build
```

## 手动启动

```bash
docker compose up -d --build
```

仓库自带 Compose 文件只发布 `8080`，它同时服务 Web UI、REST API 和 WebDAV。
dataplane 端口 `9090/9091` 是内部端口。不要将它们加入 `ports:`，也不要通过反向代理暴露它们。

Compose 启用 `init: true`，为容器提供最小 init 进程，用于信号转发和子进程回收。长期运行部署应保留该设置。

`./scripts/docker-quickstart.sh --start` 默认等待 `http://127.0.0.1:<port>/health`。
如果 Compose 启动失败或健康检查超时，脚本会输出对应的 `docker compose ps` 或 `docker compose logs --tail 100 mnemonas` 诊断命令。

远程 Docker context、SSH 隧道或其他宿主机无法本地访问发布端口的环境，可传入 `--skip-health-check`。
随后应使用 `docker compose ps`、`docker compose logs --tail 100 mnemonas` 或可访问入口的 `/health` 复核服务状态。

健康检查通过后，quickstart 会从已有 `config.toml` 推导 `initial-password.txt` 位置。
当路径位于容器 `/data` 挂载内时，脚本会报告对应宿主机路径，并在下一步中包含 `cat` 命令。
当路径位于 `/data` 之外时，脚本会报告容器内路径，并包含 `docker compose exec` 读取命令。

脚本不会输出密码。既有部署可能已在首次登录后删除该文件。全新部署缺少该文件时，应通过容器日志排查。

验证：

```bash
curl "http://localhost:${MNEMONAS_HTTP_PORT:-8080}/health"
docker compose logs -f
```

## 手动构建镜像

直接验证 Dockerfile：

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

使用 bind mount 前，宿主机数据目录必须已经存在。
如果目录不存在，Docker 可能以 root 创建它，导致非 root 容器用户首次启动时无法写入 `/data/config.toml`。
仅发布 `8080`。

本地源码构建并执行 loopback 容器烟测可运行：

```bash
make docker-check
```

该目标构建 `mnemonas:latest`，然后通过 `scripts/docker-smoke.sh` 仅在 `127.0.0.1` 发布临时端口，检查 `/health` 和前端根页面。

可覆盖构建基础镜像，以便使用私有缓存或区域镜像源：

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

## 发布镜像

公开 release 镜像可用后，在 `.env` 中设置镜像标签：

```bash
MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas:<version>
```

优先使用明确版本标签。
`latest` 只适合临时评估，或明确接受自动升级风险的环境。
Docker 预检会对未显式设置标签、未使用 digest 或使用 `latest` 的 release 镜像给出提示，便于升级和回退定位到确定镜像。

仓库自带 Compose 文件仍包含本地构建配置。quickstart 脚本会自动为 release 镜像标签添加 `--pull missing --no-build`。手动启动时应使用：

```bash
docker compose up -d --pull missing --no-build
```

GitHub Releases 的二进制归档包含 `docker-compose.yml` 和 `.env.example`。
打包模板会将 `MNEMONAS_IMAGE` 预设为同一 release tag 的 GHCR 镜像。
因此，从解压后的归档运行 `./scripts/docker-quickstart.sh --start` 时，默认走 release 镜像路径，而不是源码构建路径。

下列示例默认使用源码构建的本地镜像，也可通过 `MNEMONAS_IMAGE` 切换到已验证的 release 镜像。

## 媒体归档示例

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

配置：

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

## 本地开发工作站示例

将宿主机端口绑定到 loopback：

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

不要在容器内用 `server.host = "127.0.0.1"` 限制宿主机访问；该设置绑定的是容器自己的 loopback。
宿主机访问范围应在 Compose 端口映射中限制。

如果 loopback-only 开发容器还关闭 WebDAV 认证，应保持容器内 `server.host = "0.0.0.0"`。
同时设置 `security.allow_unsafe_no_auth = true`，显式确认宿主机端口映射是访问边界。

## 多用户 NAS 示例

管理员可在 Web UI 中创建用户，并设置用户组、`home_dir`、用户配额和目录访问规则。
非管理员默认受配置的根目录限制，除非命中共享目录规则。

文件浏览、搜索、收藏、分享、回收站、最近操作和 WebDAV `users` 模式使用同一边界。
写入 `home_dir` 的 Web/API 上传、复制、移动和回收站恢复会遵守配置的用户配额。
共享路径容量由目录配额处理。

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

## 使用 Nginx 提供 HTTPS

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

关键 Nginx 设置：

```nginx
client_max_body_size 0;
proxy_set_header Host $host;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header X-Forwarded-Proto $scheme;
proxy_pass_request_headers on;
proxy_set_header Destination $http_destination;
```

当 Nginx 是应用前唯一可信代理时，在 MnemoNAS 配置中设置 `server.trusted_proxy_hops = 1`。
Docker bridge 代理通常会从非 loopback 地址访问 MnemoNAS，因此还需要信任 Docker 网络来源。
用 `docker network inspect <compose-project>_internal` 查看实际子网后配置：

```toml
[server]
trusted_proxy_hops = 1
trusted_proxy_cidrs = ["172.18.0.0/16"] # 替换为实际 Docker 网络子网或 nginx 容器 IP
```

## Traefik 示例

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

## 监控和日志

```bash
docker compose logs -f mnemonas
docker compose logs --tail 100 mnemonas
docker compose logs mnemonas > mnemonas.log
```

容器启动时会从 `/data/config.toml` 读取 `[dataplane.cdc]` 并传给 dataplane。
修改 CDC 设置后，重启容器：

```bash
docker compose restart mnemonas
```

健康状态：

```bash
docker inspect --format='{{.State.Health.Status}}' mnemonas
curl "http://localhost:${MNEMONAS_HTTP_PORT:-8080}/health"
```

`/api/v1/metrics` 返回 JSON 指标。
Prometheus 需要 JSON exporter、自定义 exporter 或转换层。
如果 `auth.enabled = true`，抓取端必须以管理员身份认证。

## 升级和备份

源码 checkout：

```bash
docker compose build --pull
docker compose up -d
```

Release 镜像：

```bash
docker compose pull
docker compose up -d --no-build
```

升级 release 镜像前，应记录 `.env` 中当前 `MNEMONAS_IMAGE` 标签并完成备份。
如果升级后容器无法启动、核心流程回退或健康检查失败，将 `MNEMONAS_IMAGE` 改回上一标签并回退容器镜像：

```bash
docker compose pull
docker compose up -d --no-build
docker compose logs --tail 100 mnemonas
```

回退只切换容器镜像。
它仍会使用同一个宿主机数据目录和容器内 `/data/config.toml`。
如果新版本执行了不可逆数据迁移，应按对应 release note 或备份恢复流程处理，再启动旧镜像。

冷备份：

```bash
docker compose stop
tar czf mnemonas-backup-$(date +%Y%m%d).tar.gz ~/.mnemonas
docker compose start
```

恢复：

```bash
docker compose down
DEFAULT_DATA_DIR="$HOME/.mnemonas"
DATA_DIR="${MNEMONAS_DATA_DIR:-$DEFAULT_DATA_DIR}"
[ "$DATA_DIR" = "$DEFAULT_DATA_DIR" ] || { echo "refusing non-default DATA_DIR; inspect and delete manually: $DATA_DIR"; exit 1; }
case "$DATA_DIR" in *$'\n'*|*$'\r'*|*"/../"*|*"/.."|"../"*|"..") echo "refusing unsafe DATA_DIR: $DATA_DIR"; exit 1 ;; esac
[ ! -L "$DATA_DIR" ] || { echo "refusing symlink DATA_DIR: $DATA_DIR"; exit 1; }
rm -rf -- "$DATA_DIR"
tar xzf mnemonas-backup-YYYYMMDD.tar.gz -C ~

# 使用 release 镜像时，改用 docker compose up -d --no-build。
docker compose up -d
```

## 故障排除

容器无法启动：

```bash
docker compose logs mnemonas
docker run --rm --entrypoint /app/nasd \
  --user "$(id -u):$(id -g)" \
  --mount type=bind,source="$HOME/.mnemonas",target=/data \
  "${MNEMONAS_IMAGE:-mnemonas:local}" --check-config --config /data/config.toml
```

基础镜像拉取慢：

- 优先使用可用的公开 release 镜像。
- 使用 Buildx 缓存。
- 在网络更好的环境构建，再用 `docker save` / `docker load` 迁移。
- 用内部镜像源覆盖基础镜像。

权限问题：

```bash
ls -la ~/.mnemonas
sudo chown -R 1000:1000 ~/.mnemonas
chmod 750 ~/.mnemonas
```

如果 `.env` 中不是 `1000`，应使用实际 UID/GID。

端口冲突：

```bash
sudo lsof -i :8080
sed -i "s/^MNEMONAS_HTTP_PORT=.*/MNEMONAS_HTTP_PORT=8888/" .env
./scripts/mnemonas-docker-preflight.sh

# 使用 release 镜像时，改用 docker compose up -d --no-build。
docker compose up -d
```

## 更多资源

- [挂载指南](mounting-guide.md)
- [FAQ](faq.md)
- [配置参考](configuration.md)
