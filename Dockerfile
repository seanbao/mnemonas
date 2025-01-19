# 多阶段构建

# === Rust构建阶段 ===
FROM rust:1.92 AS rust-builder

WORKDIR /build/dataplane
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
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o nasd ./cmd/nasd

# === 最终镜像 ===
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

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
COPY <<EOF /app/start.sh
#!/bin/bash
set -e

# 启动Rust数据面
/app/dataplane --listen 127.0.0.1:9091 --grpc 127.0.0.1:9090 --data-dir /root/.mnemonas/.mnemonas/objects &

# 等待数据面启动
sleep 1

# 启动Go控制面
exec /app/nasd --config /root/.mnemonas/config.toml
EOF

RUN chmod +x /app/start.sh

CMD ["/app/start.sh"]
