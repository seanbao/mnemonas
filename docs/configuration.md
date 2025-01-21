# MnemoNAS 配置参考

本文档详细说明 MnemoNAS 的所有配置选项。配置文件使用 TOML 格式。

## 配置文件位置

系统按以下顺序查找配置文件：

1. `$HOME/.mnemonas/config.toml` — 用户目录（与数据同目录）

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

[server.tls]
enabled = false
cert_file = ""
key_file = ""
auto_generate = true
cert_dir = "~/.mnemonas/.mnemonas/certs"

[storage]
root = "~/.mnemonas"
data_dir = "~/.mnemonas/.mnemonas/objects"
metadata_dir = "~/.mnemonas/.mnemonas"
temp_dir = "~/.mnemonas/.mnemonas/tmp"
thumbnail_dir = "~/.mnemonas/.mnemonas/thumbnails"
maintenance_dir = "~/.mnemonas/.mnemonas/maintenance"
activity_dir = "~/.mnemonas/.mnemonas/activity"

[storage.retention]
max_versions = 50
max_age = "2160h"
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

### [server.tls] — HTTPS 配置

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | `false` | 是否启用 HTTPS |
| `cert_file` | string | `""` | 证书文件路径（留空使用 cert_dir 下的 server.crt） |
| `key_file` | string | `""` | 私钥文件路径（留空使用 cert_dir 下的 server.key） |
| `auto_generate` | bool | `true` | 自动生成自签名证书 |
| `cert_dir` | string | `~/.mnemonas/.mnemonas/certs` | 证书存放目录 |

**示例：**
```toml
[server.tls]
enabled = true
auto_generate = true
```

---

### [storage] — 存储配置

定义数据存储位置和目录结构。推荐使用 `root` 配置统一根目录，其他目录为兼容字段。

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `root` | string | `~/.mnemonas` | 存储根目录（用户文件在 `root/files`） |
| `data_dir` | string | `~/.mnemonas/.mnemonas/objects` | 兼容字段：CAS 对象目录 |
| `metadata_dir` | string | `~/.mnemonas/.mnemonas` | 兼容字段：元数据目录 |
| `temp_dir` | string | `~/.mnemonas/.mnemonas/tmp` | 临时文件目录（用于原子写入） |
| `thumbnail_dir` | string | `~/.mnemonas/.mnemonas/thumbnails` | 缩略图缓存目录 |
| `maintenance_dir` | string | `~/.mnemonas/.mnemonas/maintenance` | 维护状态文件目录 |
| `activity_dir` | string | `~/.mnemonas/.mnemonas/activity` | 活动日志目录 |

**存储目录说明：**

- **root**: 存储根目录。用户文件位于 `root/files`，内部数据位于 `root/.mnemonas`
- **data_dir**: CAS 对象目录（兼容字段）
- **metadata_dir**: SQLite 与维护元数据目录（兼容字段）
- **temp_dir**: 写入操作临时文件目录（原子写入）
- **thumbnail_dir**: 缩略图缓存目录
- **maintenance_dir**: Scrub、GC 等维护任务目录
- **activity_dir**: 活动日志目录

**示例：**
```toml
[storage]
root = "~/.mnemonas"
data_dir = "~/.mnemonas/.mnemonas/objects"
metadata_dir = "~/.mnemonas/.mnemonas"
temp_dir = "~/.mnemonas/.mnemonas/tmp"
thumbnail_dir = "~/.mnemonas/.mnemonas/thumbnails"
maintenance_dir = "~/.mnemonas/.mnemonas/maintenance"
activity_dir = "~/.mnemonas/.mnemonas/activity"
```

---

### [storage.retention] — 版本保留策略

控制文件版本的保留规则，实现版本历史与误删恢复功能。

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `max_versions` | int | `50` | 每个文件最大保留版本数（0 = 无限制） |
| `max_age` | duration | `"2160h"` (90天) | 版本最大保留时间（0 = 永久保留） |
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

### [storage.trash] — 回收站配置

控制回收站是否启用、保留时间与容量上限。

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | `true` | 是否启用回收站（关闭后删除将直接永久删除） |
| `retention_days` | int | `30` | 回收站保留天数 |
| `max_size` | int64 | `10737418240` (10GB) | 回收站最大容量（字节） |

**示例：**
```toml
[storage.trash]
enabled = true
retention_days = 30
max_size = 10737418240
```

---

### [storage.versioning] — 版本策略

控制自动版本化规则与文件大小阈值。

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `auto_versioned_extensions` | string[] | 常见文本/代码后缀 | 默认启用自动版本化的后缀列表 |
| `auto_versioned_filenames` | string[] | 常见配置文件 | 默认启用自动版本化的文件名列表 |
| `max_versioned_size` | int64 | `104857600` | 最大自动版本化文件大小（字节） |

**示例：**
```toml
[storage.versioning]
auto_versioned_extensions = [".md", ".txt", ".go"]
auto_versioned_filenames = ["README", "LICENSE"]
max_versioned_size = 104857600
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
| `auth_type` | string | `"basic"` | 认证类型：`none`（无认证）、`basic`（Basic Auth） |
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

**自动生成行为：**
当 `auth_type = "basic"` 且 `password` 为空时，首次启动会自动生成密码，并写入 `<storage.root>/secrets.json`；同时在 `username` 为空时使用默认值 `admin`。

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

### [auth] — 认证配置

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | `true` | 是否启用 JWT 认证 |
| `jwt_secret` | string | 自动生成 | JWT 签名密钥 |
| `access_token_ttl` | duration | `15m` | Access Token 有效期 |
| `refresh_token_ttl` | duration | `168h` | Refresh Token 有效期 |
| `users_file` | string | `~/.mnemonas/.mnemonas/users.json` | 用户数据文件路径 |

**示例：**
```toml
[auth]
enabled = true
access_token_ttl = "15m"
refresh_token_ttl = "168h"
```

---

### [share] — 文件分享配置

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | `false` | 是否启用分享 |
| `store_file` | string | `~/.mnemonas/.mnemonas/shares.json` | 分享数据文件路径 |
| `base_url` | string | `""` | 分享链接基础 URL |

**示例：**
```toml
[share]
enabled = true
base_url = "https://nas.example.com"
```

---

### [favorites] — 收藏夹配置

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | `true` | 是否启用收藏 |
| `store_file` | string | `~/.mnemonas/.mnemonas/favorites.json` | 收藏数据文件路径 |

---

### [alerts] — 存储空间告警配置

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | `false` | 是否启用存储告警 |
| `check_interval` | duration | `1h` | 检查间隔 |
| `threshold_pct` | float | `90` | 告警阈值（百分比） |
| `critical_pct` | float | `95` | 严重告警阈值（百分比） |
| `min_free_bytes` | uint64 | `10737418240` | 最小可用空间（字节） |
| `cooldown_period` | duration | `4h` | 告警冷却时间 |
| `webhook_url` | string | `""` | Webhook URL |
| `webhook_method` | string | `POST` | Webhook 方法 |
| `webhook_headers` | string[] | `[]` | 自定义 Header（"Key:Value"） |

**示例：**
```toml
[alerts]
enabled = true
check_interval = "1h"
threshold_pct = 90.0
critical_pct = 95.0
min_free_bytes = 10737418240
cooldown_period = "4h"
webhook_url = "https://hooks.example.com/alert"
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
root = "~/.mnemonas"
data_dir = "~/.mnemonas/.mnemonas/objects"
metadata_dir = "~/.mnemonas/.mnemonas"
temp_dir = "~/.mnemonas/.mnemonas/tmp"

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
# 默认使用 ~/.mnemonas，可自定义为其他路径如 /mnt/data/mnemonas
root = "~/.mnemonas"

[storage.retention]
max_versions = 50
max_age = "2160h"
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
