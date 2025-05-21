# Stage 2 Extension Point Design

English | [简体中文](extension-points.md)

This document sketches future extension interfaces for MnemoNAS while keeping the MVP codebase simple.

Principle: the MVP does not implement these features, but current code should leave enough structure to avoid major rewrites later.

## S3-Compatible Storage Backend

Goals:

- Store CAS data in S3-compatible object storage such as AWS S3, MinIO, or Cloudflare R2.
- Support large remote capacity.
- Enable cross-region backup.
- Allow hot/cold tiering later.

Possible backend interface:

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

Configuration sketch:

```toml
[storage]
backend = "local"

[storage.local]
root = "~/.mnemonas"

[storage.s3]
endpoint = "s3.amazonaws.com"
bucket = "mnemonas-data"
prefix = "cas/"
access_key = "..."
secret_key = "..."
region = "us-east-1"

[storage.hybrid]
hot_backend = "local"
cold_backend = "s3"
tier_policy = "age:30d"
```

Current reservation:

- `internal/caslayout.Store` already isolates object operations.
- Future work should add `context.Context`.
- Future work should support streaming `io.Reader` / `io.ReadCloser`.
- Future work can add `S3Backend` without changing user-facing file APIs.

## Plugin System

Potential plugin areas:

- File processors: thumbnails, metadata extraction, media analysis.
- Notifications: webhook, email, chat integrations.
- Authentication providers: custom enterprise login or OIDC adapters.

Interface sketch:

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

Loading options:

| Option | Decision |
| --- | --- |
| Compile-time registration | Preferred first step after MVP |
| Go runtime plugin | Not preferred because version compatibility is fragile |
| Subprocess + gRPC | Useful for untrusted third-party plugins, but higher operational cost |

Configuration sketch:

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

MVP reservation:

```go
func (fs *FileSystem) WriteFile(ctx context.Context, name string, data []byte) error {
    // native write and version archive
    // future hook: triggerFileCreated(ctx, name)
}
```

## Remote Runner

Remote runners can offload CPU/GPU-heavy jobs:

- Thumbnail generation.
- Transcoding.
- AI tagging.
- Media metadata extraction.

Architecture sketch:

```text
MnemoNAS control plane -> task queue -> runner nodes
runner nodes -> task results -> MnemoNAS control plane
```

Runner service sketch:

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

Configuration sketch:

```toml
[runner]
enabled = false

[runner.queue]
type = "memory"

[runner.tasks]
thumbnail = { runners = 2, timeout = "30s" }
transcode = { runners = 1, timeout = "5m", gpu = true }
```

Task queue sketch:

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

## Extension Checklist

Current MVP should preserve:

- CAS/object-store interface boundaries.
- File-operation lifecycle hook positions.
- Nested TOML config support.
- Clear protobuf service organization.
- Storage and API boundaries that do not assume every object is local forever.

Future implementation targets:

| Area | Candidate Work |
| --- | --- |
| S3 | Streaming backend, config, credentials handling, scrub/GC over remote objects |
| Plugins | Registration framework, lifecycle, config validation, event hooks |
| Runner | Task queue, worker registration, result persistence, retry policy |
| Tiering | Local hot cache, cold object backend, migration policy |

## Related Documents

- [Architecture](architecture.en.md)
- [Development guide](development.en.md)
- [Security guide](security.en.md)
