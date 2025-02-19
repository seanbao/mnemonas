# Copilot Instructions for MnemoNAS

## 代码协作规范

### 语言与风格

| 场景 | 规范 |
|------|------|
| 对话回复 | 中文 |
| 代码注释 | 英文 |
| 日志/UI | 中文 |
| 目录/文件引用 | 反引号包裹 |

### 代码变更

- 保持现有代码风格，不引入额外格式化变更
- 新增接口同步声明到对应头文件
- 功能变更同步更新关联的 Markdown 文档（包括本说明）
- 不主动生成兼容层代码或迁移脚本，如有必要先与用户确认

### 问题发现与处理

发现以下问题时需主动提示并确认处理方案：

- 潜在内存泄漏
- 数据竞争（race condition）
- 安全漏洞
- 明显的逻辑错误
- 代码质量问题（重复代码、过长函数、硬编码、命名不规范等）
- 架构设计问题（循环依赖、职责不清、抽象层次混乱、耦合过紧等）

## 文档写作规范

### 语气与措辞

| 避免 | 替代 |
|------|------|
| 第二人称（你/你的/如果你…） | 无主语陈述或"用户/调用方" |
| 口语化（其实/然后/那么） | 直接陈述 |
| 营销化（强大/轻松/一键） | 客观描述功能 |

### 文风收敛（去 AI 痕迹）

| 避免 | 替代 |
|------|------|
| 主观措辞（建议/适用于/可以考虑） | 客观表述（用于/范围/约束/行为说明） |
| 占位标记（for test/临时/先这样） | 正式、可复用的描述 |
| 引导式语句（接下来我们…） | 系统行为/接口约束/返回结构 |

### 结构维护

- 中文和英文之间添加空格（如 `使用 Rust 编写`）
- 修改标题后校验目录锚点与章节标题一致性
- 新增章节同步更新文档索引

## 版本控制

### Commit Message

基于 [Conventional Commits](https://www.conventionalcommits.org/) 规范。

**格式**：

```
<type>(<scope>): <subject>

[body]

[footer]
```

- `scope` 可选，取值为模块名或文件名
- `subject` 使用祈使语气（动词原形），首字母小写，结尾不加句号
- 破坏性变更在 type 后加 `!`，如 `feat!:` 或 `feat(api)!:`

**常用 type**：

| type | 说明 |
|------|------|
| `feat` | 新功能 |
| `fix` | 修复 |
| `docs` | 文档变更 |
| `refactor` | 重构（不影响功能） |
| `perf` | 性能优化 |
| `test` | 测试相关 |
| `chore` | 构建/工具/依赖 |
| `style` | 代码格式（不影响逻辑） |

**原则**：

- 精简准确，避免冗余
- 简单修改无需 body，复杂变更补充详情
- 关联 issue 在 footer 标注：`Closes #123`

**示例**：

```
feat(parser): add support for arrays

fix: resolve memory leak in cache module

docs: update API reference

refactor(auth)!: change token format

BREAKING CHANGE: token format changed from JWT to opaque
```

---

## 项目概述

MnemoNAS 是一个现代化的开源 NAS 系统，采用 **Go 控制面** + **Rust 数据面** 架构。核心理念：简单可靠、好看好用 — 数据在自己手里，体验不输云服务。

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

# 一键启动开发环境（推荐）
./scripts/dev.sh                # 启动所有组件
./scripts/dev.sh --backend      # 仅后端
./scripts/dev.sh --frontend     # 仅前端
./scripts/dev.sh --status       # 查看状态
./scripts/dev.sh --kill         # 停止所有

# 分别启动各组件
./bin/nasd                      # Go control plane (port 8080)
./bin/dataplane                 # Rust data plane (HTTP:9091, gRPC:9090)

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

Go 控制面（端口 8080）：
- `GET /health` — 健康检查
- `GET /api/v1/version` — 版本信息
- `GET /api/v1/files/*` — 文件列表
- `GET /api/v1/versions/*` — 版本历史
- `GET /api/v1/search?q=keyword` — 文件搜索
- `POST /api/v1/auth/login` — 用户登录
- `GET /api/v1/activity` — 活动日志
- `GET /api/v1/shares` — 分享列表
- `GET /api/v1/favorites` — 收藏列表
- `GET /api/v1/settings` — 系统设置
- WebDAV 挂载点 `/dav/*`（可配置前缀）

Rust 数据面（HTTP 端口 9091，gRPC 端口 9090）：
- `GET /health` — `{"status":"healthy","chunks":N,"size":N,...}`
- `GET /stats` — 存储统计与去重率

## 测试策略

```bash
# Go tests
go test -v ./...

# Rust tests
cd dataplane && cargo test

# E2E 验收测试
./scripts/e2e-test.sh           # 完整测试
./scripts/e2e-test.sh --quick   # 快速测试

# 性能基准测试
./scripts/benchmark.sh

# 手动测试 WebDAV
curl -X PROPFIND http://localhost:8080/dav/ -H "Depth: 1"

# 测试 dataplane
curl http://localhost:9091/health
curl http://localhost:9091/stats
```

**崩溃一致性测试**：写入过程中 kill 进程，重启后验证无半写入文件对外可见。

## 相关文档

详细设计与路线图见 [ideas-lab-notes](../ideas-lab-notes/)：
- [ideas/open-source-nas-go-rust.md](../ideas-lab-notes/ideas/open-source-nas-go-rust.md) — 完整项目规格说明
- [ideas/nas-data-safety-principles.md](../ideas-lab-notes/ideas/nas-data-safety-principles.md) — 数据安全设计原则
