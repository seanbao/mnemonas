# 未来扩展点设计草案

[English](extension-points.en.md) | 简体中文

本文档概述 MnemoNAS 未来可能的扩展接口。它是设计草案，不代表当前版本的发布承诺。

原则：当前 main 分支不实现这些功能，但代码边界应保持足够清晰，避免后续大规模重写。

## S3 兼容存储后端

目标：

- 将 CAS 数据保存到 AWS S3、MinIO 或 Cloudflare R2 等 S3 兼容对象存储。
- 支持较大的远程容量。
- 支持跨区域备份。
- 后续允许冷热分层。

可能的后端接口：

```go
type Backend interface {
    Put(ctx context.Context, data io.Reader) (hash string, err error)
    Get(ctx context.Context, hash string) (io.ReadCloser, error)
    Exists(ctx context.Context, hash string) (bool, error)
    Delete(ctx context.Context, hash string) error
    List(ctx context.Context) ([]string, error)
    Stats(ctx context.Context) (*StorageStats, error)
}

type LocalBackend struct {
    root string
}

type S3Backend struct {
    client *s3.Client
    bucket string
    prefix string
}
```

配置草案：

```toml
[storage]
backend = "local"

[storage.local]
root = "~/.mnemonas"

[storage.s3]
endpoint = "s3.amazonaws.com"
bucket = "mnemonas-data"
prefix = "cas/"
access_key = "<access-key>"
secret_key = "<secret-key>"
region = "us-east-1"

[storage.hybrid]
hot_backend = "local"
cold_backend = "s3"
tier_policy = "age:30d"
```

当前预留边界：

- `internal/caslayout.Store` 已经隔离 object 操作。
- 本地 CAS 层已提供 `PutContext`、`PutReaderContext`、`GetContext`、`ReaderContext`、`WalkContext` 和 `StatsContext` 等 context-aware 变体；旧入口保持兼容。
- 本地 CAS 层已支持通过 `io.Reader` 流式写入对象，后续远程后端可沿用同一取消和清理语义。
- 后续可在不改变面向用户文件 API 的情况下加入 `S3Backend`。

## 插件系统

潜在插件范围：

- 文件处理器：缩略图、元数据提取、媒体分析。
- 通知：webhook、email、聊天集成。
- 认证提供者：家庭或小团队身份适配器，例如 OIDC。

接口草案：

```go
type Plugin interface {
    Name() string
    Version() string
    Init(config map[string]interface{}) error
    Shutdown() error
}

type FileProcessor interface {
    Plugin
    OnFileCreated(ctx context.Context, path string, hash string) error
    OnFileDeleted(ctx context.Context, path string) error
    SupportedTypes() []string
}

type Notifier interface {
    Plugin
    Notify(ctx context.Context, event Event) error
}

type AuthProvider interface {
    Plugin
    Authenticate(ctx context.Context, username, password string) (User, error)
}
```

加载选项：

| 选项 | 决策 |
| --- | --- |
| 编译期注册 | 后续评估时优先采用 |
| Go runtime plugin | 不优先采用，因为版本兼容性脆弱 |
| Subprocess + gRPC | 对不可信第三方插件有用，但运维成本更高 |

配置草案：

```toml
[plugins]
enabled = ["thumbnail", "webhook"]

[plugins.thumbnail]
quality = 85
max_size = 1024

[plugins.webhook]
url = "https://example.com/webhook"
events = ["file.created", "file.deleted"]
secret = "<webhook-secret>"
```

预留边界：

```go
func (fs *FileSystem) WriteFile(ctx context.Context, name string, data []byte) error {
    // native write and version archive
    // future hook: triggerFileCreated(ctx, name)
}
```

## 远程 Runner

远程 Runner 可以卸载 CPU/GPU 密集任务：

- 缩略图生成。
- 转码。
- AI 标签。
- 媒体元数据提取。

架构草案：

```text
MnemoNAS control plane -> task queue -> runner nodes
runner nodes -> task results -> MnemoNAS control plane
```

Runner 服务草案：

```protobuf
service RunnerService {
    rpc Register(RegisterRequest) returns (RegisterResponse);
    rpc GetTask(GetTaskRequest) returns (stream Task);
    rpc SubmitResult(SubmitResultRequest) returns (SubmitResultResponse);
    rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
}

message Task {
    string id = 1;
    string type = 2;
    string file_hash = 3;
    bytes config = 4;
}

message TaskResult {
    string task_id = 1;
    bool success = 2;
    bytes output = 3;
    map<string, string> metadata = 4;
}
```

配置草案：

```toml
[runner]
enabled = false

[runner.queue]
type = "memory"

[runner.tasks]
thumbnail = { runners = 2, timeout = "30s" }
transcode = { runners = 1, timeout = "5m", gpu = true }
```

任务队列草案：

```go
type Task struct {
    ID       string
    Type     string
    FileHash string
    Status   TaskStatus
    Result   []byte
}

type TaskQueue interface {
    Enqueue(ctx context.Context, task *Task) error
    Dequeue(ctx context.Context, taskType string) (*Task, error)
    Complete(ctx context.Context, taskID string, result []byte) error
    Fail(ctx context.Context, taskID string, err error) error
}
```

## 扩展检查清单

当前 main 分支应保持：

- CAS/object-store 接口边界。
- 文件操作生命周期 hook 位置。
- 嵌套 TOML 配置支持。
- 清晰的 protobuf service 组织。
- 不假设所有 object 永远位于本地的 storage 和 API 边界。

未来实现目标：

| 范围 | 候选工作 |
| --- | --- |
| S3 | 流式后端、配置、凭据处理、远程 object 的 scrub/GC |
| Plugins | 注册框架、生命周期、配置校验、事件 hook |
| Runner | 任务队列、worker 注册、结果持久化、重试策略 |
| Tiering | 本地热缓存、冷 object 后端、迁移策略 |

## 相关文档

- [架构](architecture.md)
- [开发指南](development.md)
- [安全加固指南](security.md)
