# 未来扩展点设计草案

[English](extension-points.en.md) | 简体中文

本文档记录 MnemoNAS 的未来扩展接口方向，用于指导后续设计讨论，不代表当前版本承诺。

> **原则**：当前主线不实现这些功能，但代码结构应保留清晰边界，避免后续大规模重构。

---

## S3 兼容存储后端

### S3 目标

支持将 CAS 数据存储到 S3 兼容对象存储（AWS S3、MinIO、Cloudflare R2 等），实现：

- 无限扩展存储容量
- 跨地域备份
- 冷热数据分层

### S3 接口设计

```go
// internal/storage/backend.go

// Backend defines the storage backend interface
type Backend interface {
    // Put stores data and returns its hash
    Put(ctx context.Context, data io.Reader) (hash string, err error)
    
    // Get retrieves data by hash
    Get(ctx context.Context, hash string) (io.ReadCloser, error)
    
    // Exists checks if an object exists
    Exists(ctx context.Context, hash string) (bool, error)
    
    // Delete removes an object
    Delete(ctx context.Context, hash string) error
    
    // List returns all object hashes (for GC/scrub)
    List(ctx context.Context) ([]string, error)
    
    // Stats returns storage statistics
    Stats(ctx context.Context) (*StorageStats, error)
}

// LocalBackend implements Backend for local filesystem.
type LocalBackend struct {
    root string
}

// S3Backend implements Backend for S3-compatible storage.
type S3Backend struct {
    client *s3.Client
    bucket string
    prefix string
}
```

### S3 配置草案

```toml
[storage]
backend = "local"  # "local" | "s3" | "hybrid"

[storage.local]
root = "~/.mnemonas"

[storage.s3]
endpoint = "s3.amazonaws.com"  # 或 MinIO/R2 端点
bucket = "mnemonas-data"
prefix = "cas/"
access_key = "..."
secret_key = "..."
region = "us-east-1"

[storage.hybrid]
# 热数据本地，冷数据 S3
hot_backend = "local"
cold_backend = "s3"
tier_policy = "age:30d"  # 30 天后迁移到冷存储
```

### S3 预留边界

当前 `internal/caslayout` 的 `Store` 接口已可扩展：

```go
// internal/caslayout/layout.go - 当前实现
type Store interface {
    Put(data []byte) (string, error)
    Get(hash string) ([]byte, error)
    Exists(hash string) bool
    Delete(hash string) error
}
```

后续实现需要：

1. 添加 `context.Context` 参数
2. 改用 `io.Reader/io.ReadCloser` 支持流式传输
3. 实现 `S3Backend`

---

## 插件系统

### 插件目标

支持通过插件扩展功能，无需修改核心代码：

- 文件处理器（缩略图生成、元数据提取）
- 通知集成（Webhook、邮件）
- 自定义认证提供者

### 插件接口设计

```go
// internal/plugin/plugin.go

// Plugin defines the plugin interface
type Plugin interface {
    // Metadata
    Name() string
    Version() string
    
    // Lifecycle
    Init(config map[string]interface{}) error
    Shutdown() error
}

// FileProcessor handles file events
type FileProcessor interface {
    Plugin
    
    // OnFileCreated is called when a new file is uploaded
    OnFileCreated(ctx context.Context, path string, hash string) error
    
    // OnFileDeleted is called when a file is deleted
    OnFileDeleted(ctx context.Context, path string) error
    
    // SupportedTypes returns file extensions this processor handles
    SupportedTypes() []string
}

// Notifier sends notifications
type Notifier interface {
    Plugin
    
    // Notify sends a notification
    Notify(ctx context.Context, event Event) error
}

// AuthProvider provides custom authentication
type AuthProvider interface {
    Plugin
    
    // Authenticate validates credentials
    Authenticate(ctx context.Context, username, password string) (User, error)
}
```

### 插件加载方式

```go
// 方案 A：编译时链接（后续优先评估）
import (
    _ "github.com/seanbao/mnemonas-plugin-thumbnail"
    _ "github.com/seanbao/mnemonas-plugin-webhook"
)

// 方案 B：运行时加载（Go plugin，跨版本兼容性差）
// 不推荐

// 方案 C：子进程 + gRPC（隔离性好，性能开销）
// 适合不信任的第三方插件
```

### 插件配置草案

```toml
[plugins]
enabled = ["thumbnail", "webhook"]

[plugins.thumbnail]
quality = 85
max_size = 1024

[plugins.webhook]
url = "https://example.com/webhook"
events = ["file.created", "file.deleted"]
secret = "..."
```

### 插件预留边界

在文件操作处添加钩子点：

```go
// internal/storage/storage.go
func (fs *FileSystem) WriteFile(ctx context.Context, name string, data []byte) error {
    // ... 原生文件写入与版本归档逻辑 ...

    // 预留钩子点（当前可为空实现）
    // triggerFileCreated(ctx, name)
}
```

---

## 远程 Runner（分布式处理）

### Runner 目标

将计算密集型任务（缩略图、转码、AI 标签）卸载到独立的 Runner 节点：

- 避免阻塞主服务
- 支持 GPU 加速
- 水平扩展处理能力

### 架构

```text
┌─────────────────┐     任务队列      ┌───────────────┐
│   MnemoNAS      │  ─────────────►  │   Runner 1    │
│   (控制面)      │   Redis/NATS     │   (缩略图)    │
└─────────────────┘                  └───────────────┘
         │                                    │
         │                           ┌───────────────┐
         │                           │   Runner 2    │
         └───── 任务结果回写 ◄────── │   (AI 标签)   │
                                     └───────────────┘
```

### Runner 接口设计

```protobuf
// proto/runner.proto

service RunnerService {
    // 注册 Runner
    rpc Register(RegisterRequest) returns (RegisterResponse);
    
    // 获取任务
    rpc GetTask(GetTaskRequest) returns (stream Task);
    
    // 提交结果
    rpc SubmitResult(SubmitResultRequest) returns (SubmitResultResponse);
    
    // 心跳
    rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
}

message Task {
    string id = 1;
    string type = 2;  // "thumbnail", "transcode", "ai-tag"
    string file_hash = 3;
    bytes config = 4;  // JSON 配置
}

message TaskResult {
    string task_id = 1;
    bool success = 2;
    bytes output = 3;  // 结果数据或错误信息
    map<string, string> metadata = 4;
}
```

### Runner 配置草案

```toml
[runner]
enabled = false  # 当前关闭

[runner.queue]
type = "memory"  # "memory" | "redis" | "nats"
# redis_url = "redis://localhost:6379"

[runner.tasks]
thumbnail = { runners = 2, timeout = "30s" }
transcode = { runners = 1, timeout = "5m", gpu = true }
```

### Runner 预留边界

任务系统骨架：

```go
// internal/task/task.go

type Task struct {
    ID       string
    Type     string
    FileHash string
    Status   TaskStatus
    Result   []byte
}

type TaskStatus int

const (
    TaskPending TaskStatus = iota
    TaskRunning
    TaskCompleted
    TaskFailed
)

// TaskQueue defines the task queue interface
type TaskQueue interface {
    Enqueue(ctx context.Context, task *Task) error
    Dequeue(ctx context.Context, taskType string) (*Task, error)
    Complete(ctx context.Context, taskID string, result []byte) error
    Fail(ctx context.Context, taskID string, err error) error
}

// InMemoryQueue implements TaskQueue for the current synchronous path.
type InMemoryQueue struct {
    // 当前：直接同步执行，不入队
}
```

---

## 扩展点检查清单

### 当前主线需要确保

- [x] `caslayout.Store` 接口可扩展（已完成）
- [x] WebDAV 处理器有生命周期钩子（onFileCreated/onFileDeleted）
- [x] 配置系统支持嵌套结构（TOML）
- [x] gRPC proto 文件结构清晰，易于添加新服务

### 后续实现目标

- [ ] S3Backend 实现
- [ ] 插件加载框架
- [ ] 基础任务队列

### 长期实现目标

- [ ] 分布式 Runner
- [ ] 冷热分层存储
- [ ] 多用户权限

---

## 相关文档

- [架构设计](architecture.md)
- [开发指南](development.md)
- [安全指南](security.md)
