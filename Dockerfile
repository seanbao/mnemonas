# syntax=docker/dockerfile:1.7
# Multi-stage build.

ARG RUST_IMAGE=rust:1.92
ARG NODE_IMAGE=node:22-bookworm-slim
ARG GO_IMAGE=golang:1.25.12-alpine
ARG RUNTIME_IMAGE=debian:bookworm-slim
ARG VERSION=dev
ARG BUILD_TIME=unknown

# === Rust build stage ===
FROM ${RUST_IMAGE} AS rust-builder

WORKDIR /build/dataplane
ENV CARGO_REGISTRIES_CRATES_IO_PROTOCOL=sparse \
	CARGO_HTTP_TIMEOUT=120 \
	CARGO_NET_RETRY=5
COPY dataplane/Cargo.toml dataplane/Cargo.lock ./
COPY proto ../proto

# Build dependencies first so Docker can reuse the cache.
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

# === Frontend build stage ===
FROM ${NODE_IMAGE} AS web-builder

WORKDIR /build/web
COPY web/package.json web/package-lock.json ./
COPY web/scripts/prepare-husky.cjs ./scripts/prepare-husky.cjs
RUN --mount=type=cache,target=/root/.npm,sharing=locked \
	npm ci --prefer-offline

COPY web ./
RUN npm run build

# === Go build stage ===
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

# === Runtime image ===
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

# Copy built binaries and Web assets.
COPY --from=rust-builder /tmp/dataplane /app/
COPY --from=go-builder /build/nasd /app/
COPY --from=go-builder /build/mnemonas-healthcheck /app/
COPY --from=go-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=web-builder /build/web/dist /app/web

# Create a non-root runtime user. UID/GID 1000 can write to most bind mounts
# created by a regular Linux user.
RUN groupadd --gid 1000 mnemonas \
	&& useradd --uid 1000 --gid 1000 --home-dir /data --shell /usr/sbin/nologin mnemonas \
	&& mkdir -p /data/files \
	&& mkdir -p \
		/data/.mnemonas/objects \
		/data/.mnemonas/trash \
		/data/.mnemonas/thumbnails \
		/data/.mnemonas/maintenance \
		/data/.mnemonas/activity \
		/data/.mnemonas/tmp \
	&& chown -R mnemonas:mnemonas /data /app

# Copy the default config. The /app copy is used to populate missing config
# files in mounted volumes on first startup.
COPY --chmod=0644 mnemonas.example.toml /app/mnemonas.example.toml
COPY --chown=mnemonas:mnemonas mnemonas.example.toml /data/config.toml
RUN sed -i 's|^root = ".*"|root = "/data"|' /data/config.toml \
	&& chown mnemonas:mnemonas /data/config.toml

# Expose only the Web/API/WebDAV entry point. Dataplane ports 9090/9091 bind
# to container loopback by default and must not be published to public or
# untrusted LAN networks.
EXPOSE 8080

# Startup script.
COPY --chmod=0755 scripts/docker-start.sh /app/start.sh

USER mnemonas:mnemonas

HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 CMD ["/app/mnemonas-healthcheck"]

CMD ["/app/start.sh"]
