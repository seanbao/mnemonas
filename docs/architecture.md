# MnemoNAS 架构设计

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

### 核心原则

1. **永远能回退**：所有文件自动保留历史版本，误删/误改可一键恢复
2. **数据随身走**：数据与操作系统解耦，硬盘拔下来插到任何电脑都能读取
3. **崩溃一致性**：写入路径崩溃后可恢复到"旧版本"或"新版本"，不出现半写入
4. **端到端校验**：全链路 BLAKE3 哈希校验，能发现并报告静默损坏

### 非目标（MVP 阶段不做）

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
│  │  │       WebDAV-CAS Adapter     │                       │  │
│  │  │    (webdav.FileSystem 实现)   │                       │  │
│  │  └──────────────┬───────────────┘                       │  │
│  │                 │                                        │  │
│  │  ┌──────────────┴───────────────┐                       │  │
│  │  │         CAS Layout           │                       │  │
│  │  │  (目录结构 + 原子写入)         │                       │  │
│  │  └──────────────┬───────────────┘                       │  │
│  │                 │                                        │  │
│  └─────────────────┼────────────────────────────────────────┘  │
│                    │ gRPC                                      │
├────────────────────┼───────────────────────────────────────────┤
│                    ▼                                           │
│  ┌─────────────────────────────────────────────────────────┐  │
│  │            Rust 数据面 (dataplane) :9090                 │  │
│  │                                                          │  │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐      │  │
│  │  │  gRPC Svc   │  │  HTTP API   │  │   Metrics   │      │  │
│  │  │ (Streaming) │  │  (Health)   │  │             │      │  │
│  │  └──────┬──────┘  └─────────────┘  └─────────────┘      │  │
│  │         │                                                │  │
│  │  ┌──────┴──────────────────────────────────────┐        │  │
│  │  │              Core Services                   │        │  │
│  │  │  ┌────────┐  ┌────────┐  ┌────────┐        │        │  │
│  │  │  │  CDC   │  │  CAS   │  │ Scrub  │        │        │  │
│  │  │  │FastCDC │  │BLAKE3  │  │Verify  │        │        │  │
│  │  │  └────────┘  └────────┘  └────────┘        │        │  │
│  │  │  ┌────────┐  ┌────────┐                    │        │  │
│  │  │  │   GC   │  │ Index  │                    │        │  │
│  │  │  │Cleanup │  │DashMap │                    │        │  │
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
│  │  ├── data/                 # CAS 对象存储                 │  │
│  │  │   └── objects/          # 分片目录 ab/cd/abcd1234...  │  │
│  │  ├── meta/                 # 元数据                      │  │
│  │  │   ├── files.db          # 文件树 (SQLite, 未来)       │  │
│  │  │   └── versions.db       # 版本历史                    │  │
│  │  ├── staging/              # 写入暂存区                   │  │
│  │  └── logs/                 # 日志文件                     │  │
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

#### WebDAV Handler (`internal/webdav/`)

实现 RFC 4918 规范的 WebDAV 协议：

| 方法 | 实现状态 | 说明 |
|------|---------|------|
| `PROPFIND` | ✅ | 目录列举（支持 `Depth: 0/1/infinity`） |
| `GET` | ✅ | 文件下载（支持 `Range` 断点续传） |
| `HEAD` | ✅ | 文件元信息 |
| `PUT` | ✅ | 文件上传 |
| `MKCOL` | ✅ | 创建目录 |
| `DELETE` | ✅ | 删除（软删除） |
| `MOVE` | ✅ | 移动/重命名 |
| `COPY` | ✅ | 复制 |
| `LOCK/UNLOCK` | ⚠️ | 返回虚拟锁（简化实现） |
| `PROPPATCH` | ❌ | 暂不支持 |

关键实现：

```go
// ETag 支持条件请求
func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
    etag := h.calculateETag(path)
    
    // If-None-Match 检查
    if match := r.Header.Get("If-None-Match"); match == etag {
        w.WriteHeader(http.StatusNotModified)
        return
    }
    
    w.Header().Set("ETag", etag)
    // ...
}

// 路径锁防止并发写入冲突
func (h *Handler) handlePut(w http.ResponseWriter, r *http.Request) {
    h.pathLocks.Lock(path)
    defer h.pathLocks.Unlock(path)
    // ...
}
```

#### CAS Layout (`internal/caslayout/`)

内容寻址存储的目录布局：

```
objects/
├── ab/
│   └── cd/
│       └── abcd1234567890...  # 完整 BLAKE3 哈希作为文件名
├── 12/
│   └── 34/
│       └── 1234abcdef...
```

设计要点：
- **2 层分片**：避免单目录文件过多（每层 256 个子目录）
- **原子写入**：写入 `.tmp` → `fsync` → `rename` → 目录 `fsync`
- **rclone 友好**：纯文件结构，可直接同步到云存储

```go
// 原子写入流程
func (l *Layout) Put(hash string, data []byte) error {
    tmpPath := l.tempPath()
    finalPath := l.objectPath(hash)
    
    // 1. 写入临时文件
    f, _ := os.Create(tmpPath)
    f.Write(data)
    f.Sync()  // fsync 数据
    f.Close()
    
    // 2. 原子重命名
    os.Rename(tmpPath, finalPath)
    
    // 3. fsync 目录（确保元数据持久化）
    dir, _ := os.Open(filepath.Dir(finalPath))
    dir.Sync()
    dir.Close()
    
    return nil
}
```

#### WebDAV-CAS Adapter (`internal/webdavcas/`)

将 `webdav.FileSystem` 接口适配到 CAS 后端：

```go
type CASFileSystem struct {
    layout    *caslayout.Layout
    metaStore MetadataStore
}

// 实现 webdav.FileSystem 接口
func (fs *CASFileSystem) OpenFile(name string, flag int, perm os.FileMode) (webdav.File, error) {
    // 从元数据获取文件信息
    meta := fs.metaStore.Get(name)
    
    // 从 CAS 读取内容
    content := fs.layout.Get(meta.Hash)
    
    return &casFile{
        name:    name,
        content: content,
        meta:    meta,
    }, nil
}
```

---

## Rust 数据面

### 职责

- **CAS 存储**：基于 BLAKE3 哈希的内容寻址存储
- **CDC 分块**：FastCDC 算法将大文件切分为可变大小块
- **Scrub 巡检**：定期校验所有数据块完整性
- **GC 清理**：回收无引用的数据块

### 核心模块

#### CAS Store (`dataplane/src/cas.rs`)

```rust
pub struct CasStore {
    data_dir: PathBuf,
    // 内存索引：hash -> 是否存在
    index: DashMap<String, bool>,
}

impl CasStore {
    /// 存储数据块，返回 BLAKE3 哈希
    pub fn put(&self, data: &[u8]) -> Result<String> {
        let hash = blake3::hash(data).to_hex().to_string();
        
        // 去重：已存在则跳过
        if self.exists(&hash) {
            return Ok(hash);
        }
        
        // 原子写入
        let tmp_path = self.temp_path();
        let final_path = self.object_path(&hash);
        
        std::fs::write(&tmp_path, data)?;
        std::fs::rename(&tmp_path, &final_path)?;
        
        // 更新索引
        self.index.insert(hash.clone(), true);
        
        Ok(hash)
    }
    
    /// 读取时校验哈希
    pub fn get(&self, hash: &str) -> Result<Vec<u8>> {
        let data = std::fs::read(self.object_path(hash))?;
        
        // 端到端校验
        let actual_hash = blake3::hash(&data).to_hex().to_string();
        if actual_hash != hash {
            return Err(Error::CorruptedData { expected: hash.to_string(), actual: actual_hash });
        }
        
        Ok(data)
    }
}
```

#### CDC Chunking (`dataplane/src/cdc.rs`)

```rust
use fastcdc::v2020::FastCDC;

pub struct CdcConfig {
    pub min_size: u32,  // 256KB
    pub avg_size: u32,  // 1MB
    pub max_size: u32,  // 4MB
}

/// 将文件切分为块，返回块哈希列表
pub fn chunk_file(data: &[u8], config: &CdcConfig) -> Vec<String> {
    let chunker = FastCDC::new(data, config.min_size, config.avg_size, config.max_size);
    
    chunker
        .map(|chunk| {
            let chunk_data = &data[chunk.offset..chunk.offset + chunk.length];
            // 存入 CAS 并返回哈希
            cas_store.put(chunk_data).unwrap()
        })
        .collect()
}

/// 文件清单：记录文件由哪些块组成
#[derive(Serialize, Deserialize)]
pub struct FileManifest {
    pub path: String,
    pub size: u64,
    pub chunks: Vec<ChunkRef>,
    pub created_at: DateTime<Utc>,
}

#[derive(Serialize, Deserialize)]
pub struct ChunkRef {
    pub hash: String,
    pub offset: u64,
    pub length: u32,
}
```

#### Scrub 巡检 (`dataplane/src/scrub.rs`)

```rust
pub struct ScrubResult {
    pub total_objects: u64,
    pub verified_objects: u64,
    pub corrupted_objects: Vec<String>,
    pub missing_objects: Vec<String>,
    pub duration: Duration,
}

/// 遍历所有对象，校验哈希
pub async fn run_scrub(store: &CasStore) -> ScrubResult {
    let mut result = ScrubResult::default();
    
    for entry in store.iter_objects() {
        result.total_objects += 1;
        
        match store.verify(&entry.hash) {
            Ok(true) => result.verified_objects += 1,
            Ok(false) => result.corrupted_objects.push(entry.hash),
            Err(_) => result.missing_objects.push(entry.hash),
        }
    }
    
    result
}
```

#### GC 垃圾回收 (`dataplane/src/gc.rs`)

```rust
/// 标记-清除 GC
pub async fn run_gc(store: &CasStore, meta_store: &MetaStore) -> GcResult {
    // 1. 标记阶段：遍历所有文件清单，收集被引用的 chunk
    let mut referenced: HashSet<String> = HashSet::new();
    
    for manifest in meta_store.iter_manifests() {
        for chunk in &manifest.chunks {
            referenced.insert(chunk.hash.clone());
        }
    }
    
    // 2. 清除阶段：删除未被引用的 chunk
    let mut deleted = 0;
    let mut freed_bytes = 0;
    
    for entry in store.iter_objects() {
        if !referenced.contains(&entry.hash) {
            let size = store.delete(&entry.hash)?;
            deleted += 1;
            freed_bytes += size;
        }
    }
    
    GcResult { deleted, freed_bytes }
}
```

---

## 前端架构

### 技术选型理由

| 技术 | 选择理由 |
|------|---------|
| **React 19** | 最新并发特性，生态成熟，AI 辅助开发友好 |
| **TypeScript** | 类型安全，IDE 支持好，重构有信心 |
| **Vite** | 极快的冷启动和 HMR，原生 ESM |
| **HeroUI** | 开箱即有 Apple/Vercel 级别美观，Tailwind 原生 |
| **Tailwind CSS 4** | 零运行时，极致性能，设计系统一致 |
| **TanStack Query** | 自动缓存、重试、乐观更新，减少样板代码 |
| **TanStack Virtual** | 万级文件虚拟滚动，内存占用恒定 |
| **Zustand** | 极简 API，无 Provider 地狱，TypeScript 友好 |

### 状态管理

```typescript
// stores/files.ts - 文件状态
interface FilesState {
  currentPath: string
  selectedFiles: Set<string>
  viewMode: 'list' | 'grid' | 'album'
  
  setCurrentPath: (path: string) => void
  toggleFileSelection: (path: string) => void
  // ...
}

export const useFilesStore = create<FilesState>((set) => ({
  currentPath: '/',
  selectedFiles: new Set(),
  viewMode: 'list',
  
  setCurrentPath: (path) => set({ currentPath: path }),
  // ...
}))
```

### 数据请求

```typescript
// 使用 TanStack Query 自动缓存和重新验证
const { data, isLoading } = useQuery({
  queryKey: ['files', currentPath],
  queryFn: () => listFiles(currentPath),
  staleTime: 1000 * 60, // 1 分钟内不重新请求
})

// 乐观更新示例
const deleteMutation = useMutation({
  mutationFn: deleteFile,
  onMutate: async (path) => {
    // 乐观移除
    queryClient.setQueryData(['files', currentPath], (old) => 
      old.filter(f => f.path !== path)
    )
  },
  onError: () => {
    // 回滚
    queryClient.invalidateQueries(['files', currentPath])
  },
})
```

### 虚拟滚动

```typescript
// 万级文件列表虚拟滚动
const virtualizer = useVirtualizer({
  count: files.length,
  getScrollElement: () => parentRef.current,
  estimateSize: () => 56, // 预估行高
  overscan: 10, // 额外渲染的行数
})

// 只渲染可见区域的 DOM
{virtualizer.getVirtualItems().map((virtualItem) => (
  <div
    key={virtualItem.key}
    style={{
      height: `${virtualItem.size}px`,
      transform: `translateY(${virtualItem.start}px)`,
    }}
  >
    <FileRow file={files[virtualItem.index]} />
  </div>
))}
```

---

## 通信协议

### gRPC 接口定义

```protobuf
// proto/dataplane.proto
syntax = "proto3";

package mnemonas.dataplane.v1;

service DataPlane {
  // 流式上传
  rpc PutFile(stream PutFileRequest) returns (PutFileResponse);
  
  // 流式下载
  rpc GetFile(GetFileRequest) returns (stream GetFileResponse);
  
  // 元数据操作
  rpc GetManifest(GetManifestRequest) returns (Manifest);
  rpc DeleteManifest(DeleteManifestRequest) returns (DeleteManifestResponse);
  
  // 维护操作
  rpc RunScrub(ScrubRequest) returns (ScrubResponse);
  rpc RunGC(GCRequest) returns (GCResponse);
}

message PutFileRequest {
  oneof data {
    FileMetadata metadata = 1;
    bytes chunk = 2;
  }
}

message FileMetadata {
  string path = 1;
  int64 size = 2;
}
```

### 为什么选择 gRPC

| 方案 | 优点 | 缺点 | 适用场景 |
|------|------|------|---------|
| **gRPC** | 强类型、流式、高性能 | 需要 protobuf | ✅ 本项目 |
| REST | 简单、通用 | 无流式、无类型 | 简单 CRUD |
| FFI/CGO | 最高性能 | 开发复杂、调试难 | 热路径优化 |

---

## 数据模型

### CAS 对象

```
对象 = BLAKE3(内容) -> 内容
```

- 不可变：一旦写入不会修改
- 自校验：哈希即地址，读取时自动校验
- 自去重：相同内容只存一份

### 文件清单 (Manifest)

```json
{
  "path": "/documents/report.pdf",
  "size": 10485760,
  "chunks": [
    { "hash": "abc123...", "offset": 0, "length": 1048576 },
    { "hash": "def456...", "offset": 1048576, "length": 1048576 },
    // ...
  ],
  "version": 3,
  "created_at": "2025-01-01T12:00:00Z",
  "previous_version": "v2_hash"
}
```

### 版本链

```
v1 (初始) <- v2 (修改) <- v3 (当前)
     ↓           ↓           ↓
  manifest1   manifest2   manifest3
     ↓           ↓           ↓
  [chunks]    [chunks]    [chunks] (可能部分复用)
```

---

## 安全设计

### 认证

- **Token 认证**：每个 Share 独立 token
- **默认本地监听**：`127.0.0.1`，需显式开启局域网

### 数据完整性

- **端到端校验**：写入/读取均校验 BLAKE3 哈希
- **定期 Scrub**：后台巡检所有数据块
- **写入原子性**：`.tmp` + `rename` 确保崩溃一致性

### 未来规划

- HTTPS（自签名/ACME）
- 对象级加密（用户密钥）
- 审计日志（删除/覆盖/MOVE）
