# Docker 部署指南

[English](docker-deployment.en.md) | 简体中文

本文档介绍如何使用 Docker 部署 MnemoNAS，包括基础部署、发布镜像切换、反向代理配置和常见场景示例。

如果目标是长期运行并希望开机自启、用 systemd 管理日志和重启，优先参考 [Linux/systemd 部署指南](linux-systemd-deployment.md)。Docker 更适合快速试用、已有容器平台或希望把 MnemoNAS 和其他服务一起编排的场景。

## 📋 前置要求

- Docker 20.10+ 和 Docker Compose v2 插件（命令形式是 `docker compose`，不是旧版 `docker-compose`）
- 至少 1GB 可用内存
- 建议使用 SSD 存储（HDD 也可工作，但性能较低）

检查 Docker 版本：

```bash
docker --version
docker compose version
```

如果 `docker --version` 可用但 `docker compose version` 提示 unknown command，说明只安装了 Docker CLI，没有安装 Compose v2 插件。Ubuntu 24.04/近期 Debian 系统通常可以直接安装发行版插件：

```bash
sudo apt update
sudo apt install -y docker-compose-v2 docker-buildx
docker compose version
docker buildx version
```

如果提示找不到 `docker-compose-v2`，先确认 Ubuntu `universe` 仓库已启用，或改用 Docker 官方 apt 仓库。使用 Docker 官方 apt 仓库时，包名通常是 `docker-compose-plugin`，并建议同时安装 Buildx 插件以获得更好的构建缓存：

```bash
sudo apt update
sudo apt install -y docker-compose-plugin docker-buildx-plugin
docker compose version
docker buildx version
```

不要安装旧版 Python `docker-compose` v1；本文档和仓库脚本按 Compose v2 的 `docker compose` 命令维护。

如果 `apt update` 因为额外启用的 foreign architecture 失败，例如日志里反复出现 `binary-armhf/Packages 404 Not Found`，先修复 apt 源或临时只对宿主机架构安装这些 Docker 插件：

```bash
sudo apt-get -o APT::Architectures=amd64 update
sudo apt-get -o APT::Architectures=amd64 install -y docker-compose-v2 docker-buildx
```

这不会移除系统的 foreign architecture，只是这次安装命令按 `amd64` 索引解析；长期看仍建议把不支持该架构的 apt 源补上 `Architectures: amd64` 或改到支持该架构的源。

---

## 🚀 快速开始

### 1. 克隆项目

```bash
git clone https://github.com/seanbao/mnemonas.git
cd mnemonas
```

仓库自带的 `docker-compose.yml` 默认从当前源码构建 `mnemonas:local` 镜像。宿主机不需要安装 Go/Rust/Node.js 构建工具，但 Docker 需要能拉取 Rust、Node、Go 和 Debian 基础镜像。发布镜像公开后，可按“发布镜像”小节切换到 GHCR 镜像。

### 2. 准备并启动

```bash
./scripts/docker-quickstart.sh --start
```

这个脚本会创建或更新 `.env`，写入当前宿主机用户的 `MNEMONAS_UID`/`MNEMONAS_GID`，创建 `MNEMONAS_DATA_DIR`，运行 Docker 预检，并启动 Compose 服务。`.env` 中 `MNEMONAS_IMAGE` 保持 `mnemonas:local` 时，`--start` 会执行本地构建；设置为发布镜像标签时，`--start` 会使用 `docker compose up --pull missing --no-build`，拉取缺失镜像且禁止本地构建。如果宿主机 8080 已被占用，可改用：

```bash
./scripts/docker-quickstart.sh --port 8888 --start
```

如果只想准备环境并查看将要执行的下一步，不启动容器：

```bash
./scripts/docker-quickstart.sh
```

首次启动会在 `<MNEMONAS_DATA_DIR>/config.toml` 自动生成持久化配置。启动后请先用 `<MNEMONAS_DATA_DIR>/.mnemonas/initial-password.txt` 中的初始管理员密码登录 Web UI 并修改密码；自动生成的 WebDAV 密码保存在 `<MNEMONAS_DATA_DIR>/secrets.json`，不会直接写入容器日志。如使用 WebDAV，也建议修改：

- `[webdav].password` - WebDAV 认证密码

镜像默认以非 root 用户运行，容器内数据目录是 `/data`，默认对应宿主机 `~/.mnemonas`。如需改宿主机数据目录，优先使用 `./scripts/docker-quickstart.sh --data-dir /path/to/mnemonas --start`；脚本会把该路径写入 `.env` 的 `MNEMONAS_DATA_DIR`。Docker 中的自定义配置必须显式设置 `[storage].root`，通常保持 `root = "/data"`，且不能设置为 `/`。启动时如果已有配置里的 `[storage].root` 和 Docker 的 `STORAGE_ROOT` 不一致，容器日志会输出警告，并继续以配置文件为准。如修改为其他容器内路径，需额外挂载该路径；否则数据会写入容器临时层。例如设置 `root = "/data-root"` 时，需要在 `docker-compose.yml` 中增加指向 `/data-root` 的长语法 bind 挂载。

Docker quickstart、容器启动入口和预检脚本都会要求数据目录为绝对路径，并拒绝控制字符、`..` 片段、受保护的系统目录以及符号链接路径组件，避免把配置或对象数据写入被替换或过宽的目录。仓库自带的 Compose 文件使用长卷语法挂载 `/data`，避免宿主机路径中的 `:` 被误解析为卷目标或挂载模式；自定义 Compose 片段也应使用长卷语法。容器启动入口还会在创建或修改权限前检查 `STORAGE_ROOT/files` 与 `STORAGE_ROOT/.mnemonas/objects`，这些托管子目录不能通过符号链接指向其他位置。容器内 `CONFIG_PATH` 必须是绝对路径，并且位于 `STORAGE_ROOT` 之下，且不能包含控制字符、父目录段或符号链接路径组件；默认即为 `/data/config.toml`。Docker 容器以内置 `config.toml` 中的 `[dataplane].grpc_address` 作为内部 gRPC 地址的唯一来源；启动时会拒绝与该配置不一致的 `DATAPLANE_GRPC_ADDR` 环境变量，防止控制面和数据面使用不同端点。如果需要自定义宿主机路径，请挂载真实目录而不是符号链接。

自定义 `--env` 路径必须指向已有目录中的文件。脚本不会隐式创建 `.env` 的父目录，避免输入错误时先创建数据目录再失败。

仓库自带的 Compose 文件默认使用 UID/GID `1000:1000`，Compose 会自动读取 `.env`。如果宿主机用户不是 1000，优先按上面的命令把当前 UID/GID 写入 `.env`；也可以启动时显式传入当前用户：

```bash
MNEMONAS_UID="$(id -u)" MNEMONAS_GID="$(id -g)" docker compose up -d --build
```

`./scripts/mnemonas-docker-preflight.sh` 不会启动或修改容器，只检查启动前最常见的失败点：Docker daemon、Compose v2 插件、Buildx、数据目录权限、可用磁盘空间、`MNEMONAS_HTTP_PORT` 端口占用、已有 `config.toml` 的 `[storage].root`，以及 Compose 配置是否能渲染。预检有失败项时先按输出修复，再运行 `./scripts/docker-quickstart.sh --start`。

### 3. 手动启动服务

```bash
docker compose up -d --build
```

仓库自带的 Compose 文件只发布 `8080`，即 Web UI、REST API 和 WebDAV 入口。容器内 dataplane 的 `9090/9091` 默认绑定到 loopback，只供 `nasd` 内部访问；不要把这两个端口加入 `ports:`，也不要通过反向代理暴露到公网或不可信局域网。

Compose 文件启用了 `init: true`，让容器内的最小 init 负责信号转发和子进程回收。MnemoNAS 容器里会同时运行 `nasd` 与 dataplane，长期运行时不要移除这个设置。

### 4. 验证服务

```bash
# 健康检查
curl "http://localhost:${MNEMONAS_HTTP_PORT:-8080}/health"

# 查看日志
docker compose logs -f
```

### 手动构建镜像

如果要绕开 Compose 直接验证本地 Dockerfile：

```bash
docker build \
  --build-arg VERSION=local \
  --build-arg BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t mnemonas:local .
docker run --rm --user "$(id -u):$(id -g)" -p 8080:8080 \
  --mount type=bind,source="$HOME/.mnemonas",target=/data \
  mnemonas:local
```

手动运行镜像时同样只需要 `-p 8080:8080`。`9090/9091` 是容器内部 dataplane 端口，不需要也不应发布。

构建阶段的基础镜像可以通过 build args 覆盖，便于使用内部镜像缓存或区域镜像源：

```bash
docker build -t mnemonas:local \
  --build-arg NODE_IMAGE=node:22-bookworm-slim \
  --build-arg GO_IMAGE=golang:1.25.10-alpine \
  --build-arg RUST_IMAGE=rust:1.92 \
  --build-arg RUNTIME_IMAGE=debian:bookworm-slim \
  --build-arg VERSION=local \
  --build-arg BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  .
```

## 发布镜像

公开 release 镜像可用后，可在 `.env` 中设置镜像标签：

```bash
MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas:<version>
```

建议优先使用明确版本标签。`latest` 只适合临时评估或明确接受自动升级风险的环境。

使用发布镜像时，仓库自带的 Compose 文件仍包含本地构建配置。通过 quickstart 启动时脚本会自动传入 `--pull missing --no-build`；手动启动时也应使用：

```bash
docker compose up -d --pull missing --no-build
```

GitHub Releases 中的二进制归档会附带 `docker-compose.yml` 和 `.env.example`；归档内的模板会把 `MNEMONAS_IMAGE` 预设为同一 release tag 的 GHCR 镜像。因此从解压后的归档运行 `./scripts/docker-quickstart.sh --start` 时，默认使用发布镜像路径，而不是源码构建路径。

---

## 场景配置

下面的场景示例默认使用源码构建的本地镜像，也可通过 `MNEMONAS_IMAGE` 切换到已验证的发布镜像。

### 场景一：媒体归档服务器

将 MnemoNAS 用作照片、视频或文档归档服务，外接或挂载大容量存储。

**docker-compose.yml**:

```yaml
services:
  mnemonas:
    image: ${MNEMONAS_IMAGE:-mnemonas:local}
    container_name: mnemonas
    user: "${MNEMONAS_UID:-1000}:${MNEMONAS_GID:-1000}"
    ports:
      - "${MNEMONAS_HTTP_PORT:-8080}:8080"
    volumes:
      # 数据存储到用户目录
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

**~/.mnemonas/config.toml**:

```toml
[server]
host = "0.0.0.0"
port = 8080

[storage]
root = "/data"            # 容器内路径，对应宿主机 ~/.mnemonas

[storage.retention]
max_versions = 50        # 照片/视频保留 50 个版本足够
max_age = "17520h"       # 保留 2 年

[webdav]
enabled = true
prefix = "/dav"
auth_type = "basic"
username = "webdav"
password = "change-this-strong-password"  # 请修改！

[log]
level = "info"
```

### 场景二：开发者工作站备份

用于备份代码、文档等工作文件，需要更频繁的版本保留。

**docker-compose.yml**:

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

**~/.mnemonas/config.toml**:

```toml
[server]
host = "0.0.0.0"  # 容器内监听；宿主机访问范围由 ports 的 127.0.0.1 绑定限制
port = 8080

[storage.retention]
max_versions = 200       # 代码文件保留更多版本
max_age = "8760h"        # 保留 1 年
gc_interval = "1h"       # 更频繁的版本清理

[webdav]
enabled = true
auth_type = "none"       # 仅限绑定到宿主机 127.0.0.1 的本地场景

[security]
allow_unsafe_no_auth = true  # 容器内监听 0.0.0.0 时必须显式确认外层端口已限本机

[log]
level = "debug"          # 开发时可用 debug
```

Docker 中不要用 `server.host = "127.0.0.1"` 来限制宿主机访问范围；那会让进程只监听容器自己的 loopback，发布端口可能无法访问。需要本地-only 时，在 Compose 端口映射里写 `127.0.0.1:${MNEMONAS_HTTP_PORT:-8080}:8080`。`webdav.auth_type = "none"` 只关闭 WebDAV 认证，Web UI/API 登录仍由 `[auth].enabled` 控制；由于容器内仍监听 `0.0.0.0`，必须设置 `security.allow_unsafe_no_auth = true` 来显式确认外层端口映射已经限制为本机。

### 场景三：多用户共享 NAS

支持多个用户使用独立账号。管理员可在 Web UI 中创建用户并设置用户组、`home_dir`、容量配额和目录授权；非管理员默认只能访问自己的主目录范围，命中目录授权时可访问共享路径。文件浏览、搜索、收藏、分享、回收站、最近操作和 WebDAV `users` 模式使用同一边界；写入 `home_dir` 的 Web/API 上传、复制、移动和回收站恢复会遵守该账号配额，共享路径容量由目录配额限制。

**docker-compose.yml**:

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
    # 限制资源使用
    deploy:
      resources:
        limits:
          memory: 2G
        reservations:
          memory: 512M
```

---

## 🔒 长期运行与反向代理配置

### 使用 HTTPS（Nginx 反向代理）

**docker-compose.yml**:

```yaml
services:
  mnemonas:
    image: ${MNEMONAS_IMAGE:-mnemonas:local}
    container_name: mnemonas
    user: "${MNEMONAS_UID:-1000}:${MNEMONAS_GID:-1000}"
    # 不暴露端口，通过 nginx 访问
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

**nginx.conf**:

```nginx
events {
    worker_connections 1024;
}

http {
    upstream mnemonas {
        server mnemonas:8080;
    }

    server {
        listen 80;
        server_name nas.example.com;
        return 301 https://$server_name$request_uri;
    }

    server {
        listen 443 ssl;
        server_name nas.example.com;

        ssl_certificate /etc/nginx/certs/fullchain.pem;
        ssl_certificate_key /etc/nginx/certs/privkey.pem;
        ssl_protocols TLSv1.2 TLSv1.3;

        client_max_body_size 0;  # 不限制上传大小

        location / {
            proxy_pass http://mnemonas;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
            
            # WebDAV 需要这些头
            proxy_pass_request_headers on;
            proxy_set_header Destination $http_destination;
        }
    }
}
```

Docker bridge 反向代理的直连来源通常不是 loopback。使用上面的 Nginx 容器示例时，MnemoNAS 配置还需要信任该 Docker 网络的代理来源，否则 `X-Forwarded-Proto` 会被忽略，登录请求和 Secure cookie 判断可能仍按 HTTP 直连处理。可用 `docker network inspect <compose-project>_internal` 查看实际子网，并写入：

```toml
[server]
trusted_proxy_hops = 1
trusted_proxy_cidrs = ["172.18.0.0/16"] # 替换为实际 Docker network 子网或 nginx 容器 IP
```

### 使用 Traefik 反向代理

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

---

## 📊 监控与日志

### 查看日志

```bash
# 实时日志
docker compose logs -f mnemonas

# 最近 100 行
docker compose logs --tail 100 mnemonas

# 输出到文件
docker compose logs mnemonas > mnemonas.log
```

Docker 启动脚本会在每次容器启动时从 `/data/config.toml` 读取 `[dataplane.cdc]` 并传给 dataplane。修改 CDC 参数后执行 `docker compose restart mnemonas`，新的对象写入会使用更新后的分块参数。dataplane 的 HTTP 健康和 gRPC 端口默认仅在容器内部 loopback 上监听；运维检查优先使用 `http://localhost:8080/health`（或 `.env` 中配置的 `MNEMONAS_HTTP_PORT`）和 Web UI 的健康页。

### 健康检查

```bash
# 内置健康检查
docker inspect --format='{{.State.Health.Status}}' mnemonas

# API 健康检查
curl "http://localhost:${MNEMONAS_HTTP_PORT:-8080}/health"
```

### 集成 Prometheus

MnemoNAS 提供 `/api/v1/metrics` JSON 指标端点。

- Prometheus 原生抓取器不能直接解析该 JSON 响应；接入时需使用 `json_exporter`、自定义 exporter，或先由中间层转换为 Prometheus exposition format。
- 当 `auth.enabled = true` 时，转换层或抓取代理还需要附带有效管理员认证信息。

---

## 🔄 升级与备份

### 升级服务

```bash
# 源码 checkout 默认重新构建本地镜像
docker compose build --pull

# 重启服务（数据保留）
docker compose up -d

# 如果使用已公开的 release 镜像，则改用：
# docker compose pull
# docker compose up -d --no-build
```

### 备份数据

```bash
# 停止服务
docker compose stop

# 备份目录
tar czf mnemonas-backup-$(date +%Y%m%d).tar.gz ~/.mnemonas

# 重启服务
docker compose start
```

### 恢复数据

```bash
# 停止服务
docker compose down

# 恢复目录
DEFAULT_DATA_DIR="$HOME/.mnemonas"
DATA_DIR="${MNEMONAS_DATA_DIR:-$DEFAULT_DATA_DIR}"
[ "$DATA_DIR" = "$DEFAULT_DATA_DIR" ] || { echo "refusing non-default DATA_DIR; inspect and delete manually: $DATA_DIR"; exit 1; }
case "$DATA_DIR" in *$'\n'*|*$'\r'*|*"/../"*|*"/.."|"../"*|"..") echo "refusing unsafe DATA_DIR: $DATA_DIR"; exit 1 ;; esac
[ ! -L "$DATA_DIR" ] || { echo "refusing symlink DATA_DIR: $DATA_DIR"; exit 1; }
rm -rf -- "$DATA_DIR"
tar xzf mnemonas-backup-YYYYMMDD.tar.gz -C ~

# 启动服务；release 镜像可改用 docker compose up -d --no-build
docker compose up -d
```

---

## 🔧 故障排除

### 容器无法启动

```bash
# 查看详细日志
docker compose logs mnemonas

# 检查配置文件语法与基础字段校验（无副作用，不会启动 dataplane）
docker run --rm --entrypoint /app/nasd \
  --user "$(id -u):$(id -g)" \
  --mount type=bind,source="$HOME/.mnemonas",target=/data \
  "${MNEMONAS_IMAGE:-mnemonas:local}" --check-config --config /data/config.toml
```

### 构建时基础镜像拉取很慢

发布镜像公开可用时，可设置 `MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas:<version>`，本机无需构建工具。源码本地构建时，可用 Docker 镜像源或内部缓存覆盖 `NODE_IMAGE`、`GO_IMAGE`、`RUST_IMAGE` 和 `RUNTIME_IMAGE` build args。

### 构建卡在 Cargo / npm / Go 依赖下载

仓库 Dockerfile 已避免在运行镜像里执行 `apt-get`，Rust 构建也不再需要系统 `protoc`，Go builder 默认使用较小的 Alpine 变体；但首次从源码构建仍需要在容器内下载 Rust crates、npm 包和 Go modules。弱网环境下可以先采用这些办法：

- 优先使用公开 release 镜像，跳过本机源码构建。
- 安装 Buildx 插件（Ubuntu 仓库通常是 `docker-buildx`，Docker 官方仓库通常是 `docker-buildx-plugin`）并使用 BuildKit/Buildx 缓存，避免每次从零下载。
- 在网络更稳定的机器上构建后用 `docker save` / `docker load` 迁移镜像到目标服务器。
- 私有或区域网络内，把 `NODE_IMAGE`、`GO_IMAGE`、`RUST_IMAGE` 和 `RUNTIME_IMAGE` 指向内部镜像缓存；Rust crates/npm/Go module 镜像源仍需在构建环境中按各语言工具链单独配置。

### 权限问题

```bash
# 检查挂载目录权限
ls -la ~/.mnemonas

# 默认镜像以 UID/GID 1000 运行，仓库 Compose 文件也允许用 MNEMONAS_UID/MNEMONAS_GID 覆盖。
# 如果手动固定了其他 UID/GID，请把目录所有者调整为实际运行容器的用户。
sudo chown -R 1000:1000 ~/.mnemonas
chmod 750 ~/.mnemonas
```

### 端口冲突

```bash
# 查看端口占用
sudo lsof -i :8080

# 使用其他端口
sed -i "s/^MNEMONAS_HTTP_PORT=.*/MNEMONAS_HTTP_PORT=8888/" .env
./scripts/mnemonas-docker-preflight.sh

# release 镜像可改用 docker compose up -d --no-build
docker compose up -d
```

---

## 📖 更多资源

- [挂载指南](mounting-guide.md)
- [FAQ](faq.md)
- [配置参考](../mnemonas.example.toml)
