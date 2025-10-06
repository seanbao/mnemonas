# syntax=docker/dockerfile:1.7
# 多阶段构建

ARG RUST_IMAGE=rust:1.92
ARG NODE_IMAGE=node:22-bookworm-slim
ARG GO_IMAGE=golang:1.25.11-alpine
ARG RUNTIME_IMAGE=debian:bookworm-slim
ARG VERSION=dev
ARG BUILD_TIME=unknown

# === Rust构建阶段 ===
FROM ${RUST_IMAGE} AS rust-builder

WORKDIR /build/dataplane
ENV CARGO_REGISTRIES_CRATES_IO_PROTOCOL=sparse \
	CARGO_HTTP_TIMEOUT=120 \
	CARGO_NET_RETRY=5
COPY dataplane/Cargo.toml dataplane/Cargo.lock ./
COPY proto ../proto

# 先构建依赖（利用缓存）
RUN --mount=type=cache,target=/usr/local/cargo/registry \
	--mount=type=cache,target=/usr/local/cargo/git \
	--mount=type=cache,target=/build/dataplane/target \
	mkdir src && echo "fn main() {}" > src/main.rs && cargo build --release --locked && rm -rf src

COPY dataplane/src ./src
COPY dataplane/build.rs ./
RUN --mount=type=cache,target=/usr/local/cargo/registry \
	--mount=type=cache,target=/usr/local/cargo/git \
	--mount=type=cache,target=/build/dataplane/target \
	cargo build --release --locked \
	&& cp target/release/dataplane /tmp/dataplane

# === 前端构建阶段 ===
FROM ${NODE_IMAGE} AS web-builder

WORKDIR /build/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm,sharing=locked \
	npm ci --prefer-offline

COPY web ./
RUN npm run build

# === Go构建阶段 ===
FROM ${GO_IMAGE} AS go-builder
ARG VERSION=dev
ARG BUILD_TIME=unknown

WORKDIR /build
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
	go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" -o nasd ./cmd/nasd \
	&& CGO_ENABLED=0 go build -ldflags="-s -w" -o mnemonas-healthcheck ./cmd/healthcheck

# === 最终镜像 ===
FROM ${RUNTIME_IMAGE}
ARG VERSION=dev
ARG BUILD_TIME=unknown
LABEL org.opencontainers.image.title="MnemoNAS" \
	org.opencontainers.image.description="Self-hosted NAS with Web UI, WebDAV, versioning, and content-addressed storage" \
	org.opencontainers.image.version="${VERSION}" \
	org.opencontainers.image.created="${BUILD_TIME}" \
	org.opencontainers.image.source="https://github.com/seanbao/mnemonas" \
	org.opencontainers.image.licenses="MIT"

WORKDIR /app
ENV HOME=/data
RUN mkdir -p /etc/ssl/certs

# 复制二进制文件
COPY --from=rust-builder /tmp/dataplane /app/
COPY --from=go-builder /build/nasd /app/
COPY --from=go-builder /build/mnemonas-healthcheck /app/
COPY --from=go-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=web-builder /build/web/dist /app/web

# 创建非 root 运行用户。UID/GID 1000 能直接写入大多数 Linux 用户创建的 bind mount。
RUN groupadd --gid 1000 mnemonas \
	&& useradd --uid 1000 --gid 1000 --home-dir /data --shell /usr/sbin/nologin mnemonas \
	&& mkdir -p /data/files \
	&& mkdir -p /data/.mnemonas/{objects,trash,thumbnails,maintenance,activity,tmp} \
	&& chown -R mnemonas:mnemonas /data /app

# 复制默认配置；/app 里的副本用于首次启动时填充挂载卷中的缺失配置。
COPY mnemonas.example.toml /app/mnemonas.example.toml
COPY --chown=mnemonas:mnemonas mnemonas.example.toml /data/config.toml
RUN sed -i 's|^root = ".*"|root = "/data"|' /data/config.toml \
	&& chown mnemonas:mnemonas /data/config.toml

# 只公开 Web/API/WebDAV 入口。dataplane 的 9090/9091 默认绑定到容器内 loopback，
# 不应发布到公网或不可信局域网。
EXPOSE 8080

# 启动脚本
COPY scripts/docker-start.sh /app/start.sh

RUN chmod +x /app/start.sh

USER mnemonas:mnemonas

HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 CMD ["/app/mnemonas-healthcheck"]

CMD ["/app/start.sh"]
