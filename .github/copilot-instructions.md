# Copilot Instructions for MnemoNAS

## 沟通约定

- 回复默认使用中文，代码注释保持英文；引用目录/文件时使用反引号
- 代码修改保持现有风格，新增接口需同步声明到对应文件
- 当新增或调整功能时，需同步更新本说明与相关 Markdown 文档，保持文档与实现一致
- 浏览或修改代码时若发现严重问题（如潜在内存泄漏、数据竞争等），需主动提示并与用户确认处理方案
- 不要随意产生兼容代码或脚本，如有必要需与用户确认后再进行

## 文档规范

- 文档语气保持中性、工程化：避免第二人称（如"你/你的/如果你…"）与口语化/营销化表达
- 用"如需/可/建议/按实际环境"替代对话式句式；避免"AI 味"描述与拟人化措辞
- 中文和英文之间添加空格（如 `使用 Rust 编写`）

---

## 项目概述

MnemoNAS 是一个现代化的开源 NAS 系统，采用 **Go 控制面** + **Rust 数据面** 架构。核心理念："永远能回退" — 文件自动保留历史版本，误删/误改可一键恢复。

**语言约定**：代码注释使用英文，日志/UI 使用中文。

## 架构

```
┌─────────────────────────────────────────┐
│           Web UI (React, future)        │
├─────────────────────────────────────────┤
│          Go Control Plane (nasd)        │
│  cmd/nasd/         → Main entry point   │
│  internal/api/     → REST API (chi)     │
│  internal/webdav/  → WebDAV protocol    │
│  internal/config/  → TOML config        │
│  internal/webdavcas/→ WebDAV↔CAS adapter│
│  internal/caslayout/→ CAS storage layout│
├─────────────────────────────────────────┤
│              gRPC (proto/)              │
├─────────────────────────────────────────┤
│         Rust Data Plane (dataplane/)    │
│  src/cas.rs   → BLAKE3 CAS storage      │
│  src/cdc.rs   → FastCDC chunking        │
│  src/service.rs → gRPC service impl     │
└─────────────────────────────────────────┘
```

**核心设计决策**：
- **CAS（内容寻址存储）**：文件按 BLAKE3 哈希存储，支持去重与版本管理
- **CDC（内容定义分块）**：大文件切分为 256KB–4MB 的块，提升存储效率
- **Loose Object 模型**：一个 chunk 对应一个文件（比 packfile 简单，利用文件系统原子性）
- **软删除**：删除仅移除元数据引用，CAS 数据由 GC 清理

## 构建与运行

```bash
# Full build (proto → Go → Rust)
make build

# Development build (faster, debug mode)
make dev

# Run tests
make test

# Start service (after build)
./bin/nasd                      # Go control plane (port 8080)
./bin/dataplane                 # Rust data plane (port 9090)

# Or via Docker
docker compose up -d
```

**Proto 生成**：修改 `proto/dataplane.proto` 后需执行 `make proto` 重新生成：
- Go: `proto/dataplane.pb.go`, `proto/dataplane_grpc.pb.go`
- Rust: `dataplane/src/proto/mnemonas.dataplane.v1.rs`（通过 `build.rs`）

## 关键模式

### Go 控制面

**配置加载**（[internal/config/config.go](internal/config/config.go)）：
- TOML 格式，默认目录 `~/.mnemonas/`
- 候选路径：`./mnemonas.toml`、`/etc/mnemonas/config.toml`、`~/.config/mnemonas/config.toml`

**WebDAV 实现**（[internal/webdav/handler.go](internal/webdav/handler.go)）：
- 实现 RFC 4918（PROPFIND、GET、PUT、DELETE、MKCOL、COPY、MOVE、LOCK/UNLOCK）
- `LOCK/UNLOCK` 返回虚拟锁（简化实现）
- 始终使用 `cleanPath()` 规范化路径

**CAS 布局**（[internal/caslayout/layout.go](internal/caslayout/layout.go)）：
- 分片目录结构：`ab/cd/abcd1234...`（2 层，每层 2 字符）
- 原子写入：写入 `.tmp` → fsync → rename
- 设计为未来可独立开源的模块

### Rust 数据面

**CAS 存储**（[dataplane/src/cas.rs](dataplane/src/cas.rs)）：
- BLAKE3 哈希（比 SHA256 快 10 倍以上）
- 内存索引（DashMap）加速存在性检查
- 存储层去重

**CDC 分块**（[dataplane/src/cdc.rs](dataplane/src/cdc.rs)）：
- 使用 `fastcdc` crate，块大小可配置
- `FileManifest` 记录块列表用于重组文件
- 可用重复模式数据测试去重效果

**gRPC 服务**（[dataplane/src/service.rs](dataplane/src/service.rs)）：
- 大文件使用流式传输（`PutFile`、`GetFile`）
- 所有 chunk 读取时校验哈希

## 配置说明

`mnemonas.toml` 关键配置（参见 [mnemonas.example.toml](mnemonas.example.toml)）：

| 配置段 | 关键设置 |
|---------|-------------|
| `[server]` | `host`, `port`, timeouts |
| `[storage]` | `data_dir`, `metadata_dir`, retention policy |
| `[dataplane.cdc]` | `min/avg/max_chunk_size` (affects dedup ratio) |
| `[webdav]` | `enabled`、`prefix`（默认 `/dav`）、`auth_type` |

## API 端点

- `GET /health` — 健康检查
- `GET /api/v1/version` — 版本信息
- `GET /api/v1/files/*` — 文件列表
- `GET /api/v1/versions/*` — 版本历史
- WebDAV 挂载点 `/dav/*`（可配置前缀）

Rust 数据面 HTTP（端口 9090）：
- `GET /health` — `{"status":"healthy","chunks":N,"size":N,...}`
- `GET /stats` — 存储统计与去重率

## 测试策略

```bash
# Go tests
go test -v ./...

# Rust tests
cd dataplane && cargo test

# 手动测试 WebDAV
curl -X PROPFIND http://localhost:8080/dav/ -H "Depth: 1"
```

**崩溃一致性测试**：写入过程中 kill 进程，重启后验证无半写入文件对外可见。

## 相关文档

详细设计与路线图见 [ideas-lab-notes](../ideas-lab-notes/)：
- [ideas/open-source-nas-go-rust.md](../ideas-lab-notes/ideas/open-source-nas-go-rust.md) — 完整项目规格说明
- [ideas/nas-data-safety-principles.md](../ideas-lab-notes/ideas/nas-data-safety-principles.md) — 数据安全设计原则
