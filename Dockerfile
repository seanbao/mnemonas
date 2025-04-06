# 多阶段构建

# === Rust构建阶段 ===
FROM rust:1.92 AS rust-builder

WORKDIR /build/dataplane
RUN apt-get update && apt-get install -y protobuf-compiler && rm -rf /var/lib/apt/lists/*
COPY dataplane/Cargo.toml dataplane/Cargo.lock ./
COPY proto ../proto

# 先构建依赖（利用缓存）
RUN mkdir src && echo "fn main() {}" > src/main.rs && cargo build --release && rm -rf src

COPY dataplane/src ./src
COPY dataplane/build.rs ./
RUN cargo build --release

# === Go构建阶段 ===
FROM golang:1.25 AS go-builder

WORKDIR /build
RUN apt-get update && apt-get install -y gcc libc6-dev && rm -rf /var/lib/apt/lists/*
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o nasd ./cmd/nasd

# === 最终镜像 ===
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y ca-certificates curl && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# 复制二进制文件
COPY --from=rust-builder /build/dataplane/target/release/dataplane /app/
COPY --from=go-builder /build/nasd /app/

# 创建数据目录
RUN mkdir -p /root/.mnemonas/files \
	&& mkdir -p /root/.mnemonas/.mnemonas/{objects,trash,thumbnails,maintenance,activity,tmp}

# 复制默认配置
COPY mnemonas.example.toml /root/.mnemonas/config.toml
RUN sed -i 's|^root = ".*"|root = "/root/.mnemonas"|' /root/.mnemonas/config.toml

# 暴露端口
EXPOSE 8080 9090 9091

# 启动脚本
COPY scripts/docker-start.sh /app/start.sh

RUN chmod +x /app/start.sh

CMD ["/app/start.sh"]
