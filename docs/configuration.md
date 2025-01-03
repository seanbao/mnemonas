# MnemoNAS 配置参考

本文档详细说明 MnemoNAS 的所有配置选项。配置文件使用 TOML 格式。

## 配置文件位置

系统按以下顺序查找配置文件：

1. `./mnemonas.toml` — 当前目录
2. `$HOME/.config/mnemonas/config.toml` — 用户配置目录
3. `/etc/mnemonas/config.toml` — 系统配置目录

如果未找到配置文件，系统使用默认配置。

## 完整配置示例

```toml
# MnemoNAS 配置文件

[server]
host = "0.0.0.0"
port = 8080
read_timeout = "30s"
write_timeout = "60s"
idle_timeout = "120s"

[storage]
data_dir = "/var/lib/mnemonas/data"
metadata_dir = "/var/lib/mnemonas/metadata"
temp_dir = "/var/lib/mnemonas/tmp"
thumbnail_dir = "/var/lib/mnemonas/thumbnails"
maintenance_dir = "/var/lib/mnemonas/maintenance"

[storage.retention]
max_versions = 100
max_age = "8760h"
min_free_space = 10737418240
gc_interval = "24h"

[dataplane]
grpc_address = "127.0.0.1:9090"
timeout = "30s"
max_retries = 3

[dataplane.cdc]
min_chunk_size = 262144
avg_chunk_size = 1048576
max_chunk_size = 4194304

[webdav]
enabled = true
prefix = "/dav"
read_only = false
auth_type = "basic"
username = "admin"
password = "changeme"

[log]
level = "info"
format = "console"
output = "stdout"
time_format = "RFC3339"
```

---

## 配置段详解

### [server] — HTTP 服务器配置

控制主 API 服务器的行为。

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `host` | string | `"0.0.0.0"` | 监听地址（`0.0.0.0` 监听所有网络接口，`127.0.0.1` 仅本地） |
| `port` | int | `8080` | HTTP 端口（1-65535） |
| `read_timeout` | duration | `"30s"` | 读取请求的超时时间 |
| `write_timeout` | duration | `"60s"` | 写入响应的超时时间 |
| `idle_timeout` | duration | `"120s"` | Keep-Alive 连接的空闲超时 |

**示例：**
```toml
[server]
host = "127.0.0.1"  # 仅允许本地访问
port = 8443
read_timeout = "60s"
write_timeout = "120s"
```

---

### [storage] — 存储配置

定义数据存储位置和目录结构。

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `data_dir` | string | `~/.mnemonas/data` | CAS 数据存储目录 |
| `metadata_dir` | string | `~/.mnemonas/metadata` | 元数据数据库目录 |
| `temp_dir` | string | `~/.mnemonas/tmp` | 临时文件目录（用于原子写入） |
| `thumbnail_dir` | string | `~/.mnemonas/thumbnails` | 缩略图缓存目录 |
| `maintenance_dir` | string | `~/.mnemonas/maintenance` | 维护状态文件目录 |

**存储目录说明：**

- **data_dir**: 存储 CAS（内容寻址存储）数据块。目录结构为 `ab/cd/abcd1234...`（两层分片）
- **metadata_dir**: 存储文件路径映射、版本历史等元数据
- **temp_dir**: 写入操作的临时文件，保证原子性（先写 `.tmp` → fsync → rename）
- **thumbnail_dir**: 图片缩略图缓存，可安全删除重建
- **maintenance_dir**: Scrub、GC 等维护任务的状态和结果

**示例：**
```toml
[storage]
data_dir = "/mnt/nas/data"
metadata_dir = "/mnt/nas/meta"
temp_dir = "/mnt/nas/tmp"
thumbnail_dir = "/mnt/nas/cache/thumbnails"
maintenance_dir = "/mnt/nas/maintenance"
```

---

### [storage.retention] — 版本保留策略

控制文件版本的保留规则，实现版本历史与误删恢复功能。

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `max_versions` | int | `100` | 每个文件最大保留版本数（0 = 无限制） |
| `max_age` | duration | `"8760h"` (1年) | 版本最大保留时间（0 = 永久保留） |
| `min_free_space` | uint64 | `10737418240` (10GB) | 最小剩余磁盘空间（字节），低于此值触发强制 GC |
| `gc_interval` | duration | `"24h"` | 自动 GC 运行间隔 |

**保留规则优先级：**

1. 最新版本始终保留
2. 超过 `max_age` 的版本可被删除
3. 超过 `max_versions` 的旧版本可被删除
4. 当剩余空间低于 `min_free_space` 时，强制清理最旧版本

**示例：**
```toml
[storage.retention]
max_versions = 50        # 保留最近 50 个版本
max_age = "2160h"        # 保留 90 天
min_free_space = 53687091200  # 至少保留 50GB 空间
gc_interval = "12h"      # 每 12 小时运行 GC
```

---

### [dataplane] — Rust 数据面配置

配置与 Rust 数据面服务的通信。

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `grpc_address` | string | `"127.0.0.1:9090"` | Rust 数据面 gRPC 地址 |
| `timeout` | duration | `"30s"` | gRPC 调用超时时间 |
| `max_retries` | int | `3` | gRPC 调用失败时的最大重试次数 |

**示例：**
```toml
[dataplane]
grpc_address = "localhost:9090"
timeout = "60s"       # 大文件操作可能需要更长超时
max_retries = 5
```

---

### [dataplane.cdc] — 内容定义分块配置

配置 CDC（Content-Defined Chunking）算法参数，影响存储效率和去重率。

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `min_chunk_size` | uint32 | `262144` (256KB) | 最小块大小（字节） |
| `avg_chunk_size` | uint32 | `1048576` (1MB) | 平均块大小（字节） |
| `max_chunk_size` | uint32 | `4194304` (4MB) | 最大块大小（字节） |

**参数调优指南：**

| 场景 | 推荐配置 | 说明 |
|------|---------|------|
| **小文件为主** | min=64KB, avg=256KB, max=1MB | 更小的块适合小文件 |
| **默认/混合** | min=256KB, avg=1MB, max=4MB | 平衡存储效率与性能 |
| **大文件/备份** | min=512KB, avg=2MB, max=8MB | 减少元数据开销 |

**约束条件：**
- `min_chunk_size < avg_chunk_size < max_chunk_size`
- 建议：`min = avg / 4`，`max = avg * 4`

**示例：**
```toml
[dataplane.cdc]
min_chunk_size = 65536    # 64KB - 适合小文件
avg_chunk_size = 262144   # 256KB
max_chunk_size = 1048576  # 1MB
```

---

### [webdav] — WebDAV 服务配置

配置 WebDAV 协议支持，允许文件系统挂载和第三方客户端访问。

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | `true` | 是否启用 WebDAV 服务 |
| `prefix` | string | `"/dav"` | WebDAV URL 前缀 |
| `read_only` | bool | `false` | 是否为只读模式 |
| `auth_type` | string | `"none"` | 认证类型：`none`（无认证）、`basic`（Basic Auth） |
| `username` | string | `""` | Basic Auth 用户名 |
| `password` | string | `""` | Basic Auth 密码 |

**认证类型：**

| 类型 | 说明 | 适用场景 |
|------|-----|----------|
| `none` | 无认证，任何人可访问 | 本地开发、受信任网络 |
| `basic` | HTTP Basic 认证 | 生产环境、外部访问 |

**安全建议：**

⚠️ 生产环境务必：
1. 设置 `auth_type = "basic"` 并配置强密码
2. 使用 HTTPS（通过反向代理）
3. 考虑 `read_only = true` 限制写入

**示例：**
```toml
[webdav]
enabled = true
prefix = "/dav"
read_only = false
auth_type = "basic"
username = "myuser"
password = "very-strong-password-here"
```

---

### [log] — 日志配置

配置系统日志输出。

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `level` | string | `"info"` | 日志级别：`debug`、`info`、`warn`、`error` |
| `format` | string | `"console"` | 输出格式：`console`（人类可读）、`json`（结构化） |
| `output` | string | `"stdout"` | 输出目标：`stdout`、`stderr`、或文件路径 |
| `time_format` | string | `"RFC3339"` | 时间戳格式 |

**日志级别说明：**

| 级别 | 说明 | 使用场景 |
|------|-----|----------|
| `debug` | 详细调试信息 | 开发/排查问题 |
| `info` | 常规运行信息 | 生产环境推荐 |
| `warn` | 警告信息 | 潜在问题 |
| `error` | 错误信息 | 仅记录错误 |

**示例：**
```toml
[log]
level = "debug"
format = "json"               # 便于日志分析
output = "/var/log/mnemonas/server.log"
time_format = "RFC3339"
```

---

## 时间格式说明

配置中的时间/持续时间使用 Go duration 格式：

| 单位 | 符号 | 示例 |
|------|-----|------|
| 纳秒 | ns | `100ns` |
| 微秒 | µs/us | `500us` |
| 毫秒 | ms | `200ms` |
| 秒 | s | `30s` |
| 分钟 | m | `5m` |
| 小时 | h | `24h` |

可组合使用：`1h30m`、`2h45m30s`

---

## 环境变量覆盖

（计划中）未来版本将支持通过环境变量覆盖配置：

```bash
MNEMONAS_SERVER_PORT=9000
MNEMONAS_LOG_LEVEL=debug
MNEMONAS_WEBDAV_ENABLED=false
```

---

## 配置验证

系统在启动时自动验证配置：

- `port` 必须在 1-65535 范围内
- `data_dir` 不能为空
- `grpc_address` 不能为空
- CDC 参数必须满足 `min < avg < max`

验证失败时会输出详细错误信息并拒绝启动。

---

## 常见配置场景

### 开发环境

```toml
[server]
host = "127.0.0.1"
port = 8080

[storage]
data_dir = "./data"
metadata_dir = "./metadata"
temp_dir = "./tmp"

[webdav]
enabled = true
auth_type = "none"

[log]
level = "debug"
```

### 生产环境

```toml
[server]
host = "0.0.0.0"
port = 8080
read_timeout = "60s"
write_timeout = "120s"

[storage]
data_dir = "/var/lib/mnemonas/data"
metadata_dir = "/var/lib/mnemonas/metadata"
temp_dir = "/var/lib/mnemonas/tmp"

[storage.retention]
max_versions = 100
max_age = "8760h"
min_free_space = 107374182400  # 100GB

[webdav]
enabled = true
auth_type = "basic"
username = "admin"
password = "${MNEMONAS_WEBDAV_PASSWORD}"

[log]
level = "info"
format = "json"
output = "/var/log/mnemonas/server.log"
```

### 只读归档服务器

```toml
[webdav]
enabled = true
read_only = true
auth_type = "none"

[storage.retention]
gc_interval = "0"  # 禁用自动 GC
```

---

## 相关文档

- [架构设计](architecture.md) — 系统架构说明
- [部署指南](docker-deployment.md) — Docker 部署说明
- [WebDAV 兼容性](webdav-compatibility.md) — 客户端兼容信息
- [安全配置](security.md) — 安全最佳实践
