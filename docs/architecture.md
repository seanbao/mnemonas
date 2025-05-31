# MnemoNAS 架构设计

[English](architecture.en.md) | 简体中文

本文档详细介绍 MnemoNAS 的系统架构、核心设计决策和技术实现细节。

## 目录

- [设计理念](#设计理念)
- [整体架构](#整体架构)
- [Go 控制面](#go-控制面)
- [Rust 数据面](#rust-数据面)
- [前端架构](#前端架构)
- [通信协议](#通信协议)
- [数据模型](#数据模型)
- [安全设计](#安全设计)

---

## 设计理念

### 一句话定位

**自托管私有云存储** — 提供 Web UI、WebDAV、版本历史、回收站、Scrub 和诊断包，面向日常文件管理。

### 核心原则

1. **数据自主权**：数据在自己手里，容量由本机磁盘决定，迁移完整存储目录即可换机运行
2. **易用界面**：桌面端和移动端都保持清晰、克制、可扫描，避免传统运维后台式堆砌
3. **崩溃一致性**：写入路径崩溃后可恢复到"旧版本"或"新版本"，不出现半写入
4. **端到端校验**：全链路 BLAKE3 哈希校验，能发现并报告静默损坏
5. **版本可回退**：按策略保留适合版本化的文件历史，配合回收站处理误删/误改

### 非目标（当前范围不做）

- SMB/NFS 协议（兼容性成本过高）
- RAID/复杂卷管理（先目录模式 + 清晰的数据目录约定）
- 集群一致性（先单机可靠，再谈多机）

---

## 整体架构

```
┌────────────────────────────────────────────────────────────────┐
│                        客户端层                                 │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐       │
│  │ Web UI   │  │ Finder   │  │ Explorer │  │ nPlayer  │       │
│  │ (React)  │  │ (macOS)  │  │ (Windows)│  │ (iOS)    │       │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘       │
│       │             │             │             │              │
│       └─────────────┴──────┬──────┴─────────────┘              │
│                            │                                   │
├────────────────────────────┼───────────────────────────────────┤
│                            ▼                                   │
│  ┌─────────────────────────────────────────────────────────┐  │
│  │              Go 控制面 (nasd) :8080                      │  │
│  │                                                          │  │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐      │  │
│  │  │   WebDAV    │  │  REST API   │  │   Static    │      │  │
│  │  │  Handler    │  │  Handlers   │  │   Files     │      │  │
│  │  └──────┬──────┘  └──────┬──────┘  └─────────────┘      │  │
│  │         │                │                               │  │
│  │  ┌──────┴────────────────┴──────┐                       │  │
│  │  │         Storage Layer         │                       │  │
│  │  │    (统一文件操作接口)          │                       │  │
│  │  └──────────────┬───────────────┘                       │  │
│  │                 │                                        │  │
│  │  ┌──────────────┴───────────────┐                       │  │
│  │  │  ┌─────────┐  ┌───────────┐  │                       │  │
│  │  │  │Workspace│  │VersionStore│  │                       │  │
│  │  │  │(原生文件)│  │(SQLite+CAS)│  │                       │  │
│  │  │  └─────────┘  └───────────┘  │                       │  │
│  │  └──────────────────────────────┘                       │  │
│  │                                                          │  │
│  └─────────────────────────────────────────────────────────┘  │
│                    │ gRPC                                      │
├────────────────────┼───────────────────────────────────────────┤
│                    ▼                                           │
│  ┌─────────────────────────────────────────────────────────┐  │
│  │            Rust 数据面 (dataplane) :9090                 │  │
│  │                                                          │  │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐      │  │
│  │  │  gRPC Svc   │  │  HTTP API   │  │   Stats     │      │  │
│  │  │ (Streaming) │  │ (Health)    │  │             │      │  │
│  │  └──────┬──────┘  └─────────────┘  └─────────────┘      │  │
│  │         │                                                │  │
│  │  ┌──────┴──────────────────────────────────────┐        │  │
│  │  │              Core Services                   │        │  │
│  │  │  ┌────────┐  ┌────────┐  ┌────────┐        │        │  │
│  │  │  │  CDC   │  │  CAS   │  │ Scrub  │        │        │  │
│  │  │  │FastCDC │  │BLAKE3  │  │Verify  │        │        │  │
│  │  │  └────────┘  └────────┘  └────────┘        │        │  │
│  │  │  ┌────────┐  ┌────────┐                    │        │  │
│  │  │  │  List  │  │ Index  │                    │        │  │
│  │  │  │Objects │  │DashMap │                    │        │  │
│  │  │  └────────┘  └────────┘                    │        │  │
│  │  └─────────────────────────────────────────────┘        │  │
│  │                                                          │  │
│  └─────────────────────────────────────────────────────────┘  │
│                                                                │
├────────────────────────────────────────────────────────────────┤
│                         存储层                                  │
│  ┌─────────────────────────────────────────────────────────┐  │
│  │                 文件系统 (ext4/xfs/zfs/btrfs)            │  │
│  │                                                          │  │
│  │  ~/.mnemonas/                                            │  │
│  │  ├── files/                # 用户文件（原生存储）          │  │
│  │  │   ├── documents/                                      │  │
│  │  │   └── photos/                                         │  │
│  │  │                                                       │  │
│  │  └── .mnemonas/            # 内部数据                     │  │
│  │      ├── index.db          # SQLite 元数据库              │  │
│  │      ├── objects/          # 版本对象 (CAS)               │  │
│  │      │   └── ab/cd/abcd... # 分片目录                     │  │
│  │      └── trash/            # 回收站                       │  │
│  │                                                          │  │
│  └─────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────┘
```

---

## Go 控制面

### 职责

- **协议网关**：WebDAV RFC 4918 实现，对接各类客户端
- **REST API**：提供文件管理、版本查询、系统监控接口
- **配置管理**：TOML 配置加载与热更新
- **认证鉴权**：Token 认证、Share 权限管理
- **任务调度**：Scrub、GC 任务的调度与状态管理

### 核心模块

#### Storage Layer (`internal/storage/`)

统一存储层，整合原生文件操作和版本管理：

```go
type FileSystem struct {
    workspace *workspace.Workspace   // 原生文件操作
    versions  *versionstore.Store    // SQLite + CAS 版本管理
    policy    *versionstore.VersioningPolicy
    trashRoot string
    config    *Config
}

// 核心操作
func (fs *FileSystem) Stat(ctx context.Context, name string) (*FileInfo, error)
func (fs *FileSystem) ReadDir(ctx context.Context, name string) ([]*FileInfo, error)
func (fs *FileSystem) OpenFile(ctx context.Context, name string) (*os.File, error)
func (fs *FileSystem) WriteFile(ctx context.Context, name string, r io.Reader) error
func (fs *FileSystem) Delete(ctx context.Context, name string) error  // 软删除到回收站
func (fs *FileSystem) Rename(ctx context.Context, old, new string) error

// 版本操作
func (fs *FileSystem) ListVersions(ctx context.Context, name string) ([]VersionRef, error)
func (fs *FileSystem) GetVersion(ctx context.Context, name, hash string) (io.ReadCloser, error)
func (fs *FileSystem) RestoreVersion(ctx context.Context, name, hash string) error

// 回收站操作
func (fs *FileSystem) ListTrash(ctx context.Context) ([]*TrashItem, error)
func (fs *FileSystem) RestoreFromTrash(ctx context.Context, id string) error
func (fs *FileSystem) EmptyTrash(ctx context.Context) (int, error)
```

#### Workspace (`internal/workspace/`)

原生文件操作封装：

```go
type Workspace struct {
    root string  // 文件根目录
}

// 所有文件操作直接映射到文件系统
func (w *Workspace) ReadFile(ctx context.Context, name string) ([]byte, error)
func (w *Workspace) WriteFile(ctx context.Context, name string, data []byte) error
func (w *Workspace) Delete(ctx context.Context, name string) error
func (w *Workspace) Rename(ctx context.Context, old, new string) error
func (w *Workspace) Walk(ctx context.Context, root string, fn WalkFunc) error
```

优势：
- 文件直接可读取，无需特殊软件
- 原子写入 (.tmp → fsync → rename)
- 简单可靠的实现

#### VersionStore (`internal/versionstore/`)

SQLite 驱动的版本管理，支持可插拔的对象存储后端：

```go
// ObjectStore 接口 - 支持本地或远程存储后端
type ObjectStore interface {
    Put(ctx context.Context, data []byte) (hash string, err error)
    Get(ctx context.Context, hash string) ([]byte, error)
    Has(ctx context.Context, hash string) bool
    Delete(ctx context.Context, hash string) error
}

// LocalObjectStore - 本地文件存储（测试/独立模式）
type LocalObjectStore struct { root string }

// RemoteObjectStore - 通过 gRPC 调用 Rust 数据面（生产模式）
type RemoteObjectStore struct { client *dataplane.Client }

type Store struct {
    db      *sql.DB       // SQLite 连接
    objects ObjectStore   // 可插拔的对象存储后端
}

// 版本记录管理
func (s *Store) AddVersion(ctx context.Context, path, hash string, size int64, comment string) error
func (s *Store) GetVersions(ctx context.Context, path string) ([]Version, error)
func (s *Store) DeleteOldVersions(ctx context.Context, path string, maxCount int, maxAge time.Duration) ([]string, error)

// 版本对象存储 (委托给 ObjectStore)
func (s *Store) PutObject(data []byte) (string, error)
func (s *Store) GetObject(hash string) ([]byte, error)
func (s *Store) HasObject(hash string) bool

// 回收站管理
func (s *Store) AddToTrash(ctx context.Context, item *TrashItem) error
func (s *Store) ListTrash(ctx context.Context) ([]TrashItem, error)
func (s *Store) CleanupExpiredTrash(ctx context.Context) ([]string, error)

// 文件锁 (WebDAV LOCK 支持)
func (s *Store) AcquireLock(ctx context.Context, path, holder string, lockType LockType, duration time.Duration) error
func (s *Store) ReleaseLock(ctx context.Context, path, holder string) error
```

**职责划分**：
- Go `Store`: SQLite 元数据管理、版本策略、回收站、文件锁
- `ObjectStore`: 纯数据 I/O（本地文件或 Rust gRPC）

SQLite 表结构：
- `files`: 文件索引（路径、大小、修改时间、哈希）
- `versions`: 版本历史（路径、版本哈希、时间戳）
- `versioning_overrides`: 用户自定义版本策略
- `trash`: 回收站元数据
- `file_locks`: 文件锁

#### WebDAV Handler (`internal/webdav/`)

实现 RFC 4918 规范的 WebDAV 协议：

---

## Rust 数据面

### 职责

Go 控制面通过 gRPC 调用 Rust 数据面处理 **CAS/CDC/对象 I/O**，原生文件读写仍由 Go 控制面的 Workspace 直接操作：

| 功能 | 说明 |
|------|------|
| **CAS 存储** | BLAKE3 哈希、内存索引（DashMap）、分片目录 |
| **CDC 分块** | dataplane 的 `PutFile` / `GetFile` 文件 RPC 使用 FastCDC；当前 Go 版本历史路径仍以 whole-object CAS 快照存储 |
| **Scrub** | 数据完整性校验 |
| **对象列表** | 列出对象供 Go 进行引用计数删除 |

当前版本历史写入路径保存的是 BLAKE3 whole-object CAS 对象，具备整对象去重与校验能力；chunk 级版本引用追踪尚未接入控制面 GC，因此不能把运行态版本历史描述为已按 CDC 块级去重。

### 核心模块

```rust
// cas.rs - BLAKE3 内容寻址存储
pub struct CasStore {
    config: CasConfig,
    index: DashMap<String, u64>,  // 内存索引
    stats: CasStats,
}

impl CasStore {
    pub async fn put(&self, data: &[u8]) -> Result<String>;
    pub async fn get(&self, hash: &str) -> Result<Vec<u8>>;
    pub fn has(&self, hash: &str) -> bool;
    pub async fn delete(&self, hash: &str) -> Result<bool>;
    pub async fn scrub(&self, hashes: Option<&[String]>) -> Result<ScrubSummary>;
}

// cdc.rs - FastCDC 智能分块
pub struct Chunker { config: ChunkerConfig }

impl Chunker {
    pub fn chunk(&self, data: &[u8]) -> Vec<Chunk>;
}

// service.rs - gRPC 服务
pub struct DataPlaneService {
    cas: Arc<CasStore>,
    chunker: Arc<Chunker>,
}
```

### gRPC API

```protobuf
service DataPlane {
  // 数据块操作
  rpc PutChunk(PutChunkRequest) returns (PutChunkResponse);
  rpc GetChunk(GetChunkRequest) returns (GetChunkResponse);
  rpc HasChunk(HasChunkRequest) returns (HasChunkResponse);
  rpc DeleteChunk(DeleteChunkRequest) returns (DeleteChunkResponse);

  // 文件操作（CDC 分块）
  rpc PutFile(stream PutFileRequest) returns (PutFileResponse);
  rpc GetFile(GetFileRequest) returns (stream GetFileResponse);

  // 系统操作
  rpc Health(HealthRequest) returns (HealthResponse);
  rpc Stats(StatsRequest) returns (StatsResponse);
  rpc Scrub(ScrubRequest) returns (ScrubResponse);
  rpc ListObjects(ListObjectsRequest) returns (ListObjectsResponse);
}
```

### 为什么用 Rust

| 场景 | Go | Rust | 选择 |
|------|-----|------|------|
| 协议解析、业务逻辑 | 简洁、维护性好 | 过度复杂 | **Go** |
| CDC 分块、批量哈希 | 可用但非最优 | SIMD 优化、零拷贝 | **Rust** |
| 内存索引（百万对象） | GC 压力 | 无 GC、DashMap 高并发 | **Rust** |

**总结**：Go 做控制面逻辑，Rust 做计算密集型数据操作，分工明确、各取所长。

---

## 安全设计

### 认证

- **Web UI/API 认证**：默认启用基于 JWT 的登录；浏览器主会话通过同源 `HttpOnly` cookie 保存 access/refresh token，多用户按角色和 `home_dir` 控制访问范围
- **WebDAV 认证**：默认启用全局 Basic Auth 凭据，适配 Finder、Windows 文件资源管理器、rclone 等客户端
- **公开分享认证**：每个 Share 使用独立标识，密码保护分享会使用短期 `HttpOnly` 访问 cookie
- **网络暴露**：默认配置监听 `0.0.0.0:8080` 以便局域网访问；长期部署应配合防火墙、Tailscale/Headscale 或 HTTPS 反向代理限制访问面

### 数据完整性

- **端到端校验**：写入/读取均校验 BLAKE3 哈希
- **Scrub 校验**：通过 Web UI/API 触发对象校验，也可配合 cron 或运维计划定期执行
- **写入原子性**：`.tmp` + `rename` 确保崩溃一致性

### 未来规划

- 对象级加密（用户密钥）
- OAuth/OIDC 集成
- 更细粒度的访问策略
