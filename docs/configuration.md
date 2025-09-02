# MnemoNAS 配置参考

[English](configuration.en.md) | 简体中文

本文档详细说明 MnemoNAS 的所有配置选项。配置文件使用 TOML 格式。

## 配置文件位置

系统按以下顺序查找配置文件：

1. `nasd --config /path/to/config.toml` — 显式指定的配置文件
2. `$HOME/.mnemonas/config.toml` — 用户目录（与数据同目录）

如果未找到配置文件，系统使用默认配置。Ubuntu/systemd 安装脚本默认生成 `/etc/mnemonas/config.toml`，并在 systemd unit 中用 `--config` 指向该文件。

## 配置检查

修改配置后先运行：

```bash
nasd --check-config --config /etc/mnemonas/config.toml
```

该命令会校验 TOML、端口、时长、路径等硬性错误，也会输出可部署但风险较高的安全警告。关闭 `auth.enabled` 或 WebDAV 使用 `auth_type = "none"` 时，如果 HTTP 服务监听非 loopback 地址，默认会被视为配置错误；只有显式设置 `security.allow_unsafe_no_auth = true` 才允许继续启动。dataplane gRPC 监听到外部网络仍会输出警告，长期运行时应把这些 `warning:` 当作上线前检查项处理。

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

[storage.retention]
max_versions = 50
max_age = "2160h"
min_free_space = 10737418240
gc_interval = "24h"

[storage.versioning]
auto_versioned_extensions = [".md", ".txt", ".go", ".rs", ".toml", ".yaml", ".json"]
auto_versioned_filenames = ["README", "LICENSE", "CHANGELOG", "Dockerfile", "Makefile"]
max_versioned_size = 104857600

[storage.trash]
enabled = true
retention_days = 30
max_size = 10737418240

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
password = ""

[auth]
enabled = true
jwt_secret = ""
access_token_ttl = "15m"
refresh_token_ttl = "168h"
users_file = ""

[share]
enabled = false
store_file = ""
base_url = ""

[favorites]
enabled = true
store_file = ""

[alerts]
enabled = false
check_interval = "1h"
threshold_pct = 90.0
critical_pct = 95.0
min_free_bytes = 10737418240
cooldown_period = "4h"
webhook_url = ""
webhook_method = "POST"

[security]
allow_unsafe_no_auth = false

[log]
level = "info"
format = "console"
output = "stdout"
time_format = "RFC3339"
```

---

### [webdav] — WebDAV 配置

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `true` | 是否启用 WebDAV 服务 |
| `prefix` | string | `"/dav"` | WebDAV 挂载前缀 |
| `read_only` | bool | `false` | 是否禁止写入类 WebDAV 方法 |
| `auth_type` | string | `"basic"` | 认证方式，支持 `basic` 和 `none` |
| `username` | string | `""` | `basic` 认证用户名，留空时运行态默认使用 `admin` |
| `password` | string | `""` | `basic` 认证密码，留空时运行态使用 `secrets.json` 中的自动生成密码 |

**运行态行为：**

- 通过设置 API 更新 `webdav` 配置后，运行中的 WebDAV handler 会立即切换到新前缀、读写模式和认证配置
- `password = ""` 且 `auth_type = "basic"` 时，运行态继续使用已有自动生成密码，不要求重启
- 认证启用时，`username` 不应复用现有普通用户或 guest 用户名；WebDAV 基本认证是全局服务凭据，不携带应用层 `home_dir` 隔离

**示例：**

```toml
[webdav]
enabled = true
prefix = "/dav"
read_only = false
auth_type = "basic"
username = "admin"
password = ""
```

---

## 配置段详解

### [server] — HTTP 服务器配置

控制主 API 服务器的行为。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `host` | string | `"0.0.0.0"` | 监听地址（`0.0.0.0` 监听所有网络接口，`127.0.0.1` 仅本地） |
| `port` | int | `8080` | HTTP 端口（1-65535） |
| `read_timeout` | duration | `"30s"` | 读取请求的超时时间 |
| `write_timeout` | duration | `"60s"` | 写入响应的超时时间 |
| `idle_timeout` | duration | `"120s"` | Keep-Alive 连接的空闲超时 |
| `trusted_proxy_hops` | int | `0` | 信任的反向代理层数；默认忽略转发头，部署在受信反向代理后方时按 `X-Forwarded-For` 从右向左数第 N 个地址作为客户端 IP |

**示例：**

```toml
[server]
host = "127.0.0.1"  # 仅允许本地访问
port = 8443
read_timeout = "60s"
write_timeout = "120s"
trusted_proxy_hops = 2 # app 前面有两层反向代理时显式设置
```

默认 `trusted_proxy_hops = 0`，直接暴露服务时不会采信客户端可伪造的 `X-Forwarded-*` 头。若 MnemoNAS 位于受信反向代理后方，一层代理设置为 `1`；多层代理必须设置为代理总层数，才能从 `X-Forwarded-For` 中选到真实客户端地址。

---

### [server.tls] — HTTPS 配置

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
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

定义数据存储位置和目录结构。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `root` | string | `~/.mnemonas` | 存储根目录（用户文件在 `root/files`） |

**存储目录说明：**

- **root**: 存储根目录，不能设置为文件系统根目录 `/`。用户文件位于 `root/files`，内部数据位于 `root/.mnemonas`
- 内部数据目录结构固定在 `root/.mnemonas` 下。
- 启动时会将 `root` 和 `root/files` 权限收紧为 `0750`，内部目录为 `0700`。

**示例：**

```toml
[storage]
root = "~/.mnemonas"
```

---

### [storage.retention] — 版本保留策略

控制文件版本的保留规则，实现版本历史与误删恢复功能。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `max_versions` | int | `50` | 每个文件最大保留版本数（0 = 无限制） |
| `max_age` | duration | `"2160h"` (90天) | 版本最大保留时间（0 = 永久保留） |
| `min_free_space` | uint64 | `10737418240` (10GB) | 最小剩余磁盘空间（字节），低于此值时写入后触发一次强制版本清理 |
| `gc_interval` | duration | `"24h"` | 自动版本清理运行间隔，设为 `"0"` 表示禁用 |

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
gc_interval = "12h"      # 每 12 小时运行一次版本清理
```

---

### [storage.trash] — 回收站配置

控制回收站是否启用、保留时间与容量上限。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `true` | 是否启用回收站（关闭后删除将直接永久删除） |
| `retention_days` | int | `30` | 回收站保留天数 |
| `max_size` | int64 | `10737418240` (10GB) | 回收站最大容量（字节） |

当写入新的回收站项目会超过 `max_size` 时，系统按删除时间从旧到新清理已有项目，优先为最新删除的项目腾出空间。如果单个最新项目本身已经大于 `max_size`，旧项目仍会先被清理，但该最新项目会保留，因此总占用可能暂时高于 `max_size`。

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
| ---- | ---- | ------ | ---- |
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

数据面 gRPC 端口用于 `nasd` 和 Rust dataplane 之间的内部通信，不提供面向外部客户端的认证层。除非你有明确的私有网络隔离方案，否则保持 `127.0.0.1:9090`，不要把 dataplane gRPC 或 HTTP 健康端口直接暴露到公网或不可信局域网。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `grpc_address` | string | `"127.0.0.1:9090"` | Rust 数据面 gRPC 地址 |
| `timeout` | duration | `"30s"` | 数据面连接与重连的总超时预算 |
| `max_retries` | int | `3` | 数据面连接建立/重连时的最大重试次数 |

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
| ---- | ---- | ------ | ---- |
| `min_chunk_size` | uint32 | `262144` (256KB) | 最小块大小（字节） |
| `avg_chunk_size` | uint32 | `1048576` (1MB) | 平均块大小（字节） |
| `max_chunk_size` | uint32 | `4194304` (4MB) | 最大块大小（字节），上限 `67108864` (64MB) |

**参数调优指南：**

| 场景 | 推荐配置 | 说明 |
| ---- | -------- | ---- |
| **小文件为主** | min=64KB, avg=256KB, max=1MB | 更小的块适合小文件 |
| **默认/混合** | min=256KB, avg=1MB, max=4MB | 平衡存储效率与性能 |
| **大文件/备份** | min=512KB, avg=2MB, max=8MB | 减少元数据开销 |

**约束条件：**

- `min_chunk_size < avg_chunk_size < max_chunk_size`
- `max_chunk_size <= 67108864` (64MB)，避免 dataplane 为流式分块预留过大的内存缓冲
- 建议：`min = avg / 4`，`max = avg * 4`

这些参数由 Rust dataplane 在启动时读取。Docker 启动脚本和 systemd 安装的 `mnemonas-dataplane-start` helper 会把配置中的字节值传给 dataplane；修改后需要重启 dataplane 才会影响新的对象写入。

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
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `true` | 是否启用 WebDAV 服务 |
| `prefix` | string | `"/dav"` | WebDAV URL 前缀 |
| `read_only` | bool | `false` | 是否为只读模式 |
| `auth_type` | string | `"basic"` | 认证类型：`none`（无认证）、`basic`（Basic Auth） |
| `username` | string | `""` | Basic Auth 用户名 |
| `password` | string | `""` | Basic Auth 密码 |

**认证类型：**

| 类型 | 说明 | 适用场景 |
| ---- | ---- | ---------- |
| `none` | 无认证，任何人可访问 | 本地开发、受信任网络 |
| `basic` | HTTP Basic 认证 | 生产环境、外部访问 |

**安全建议：**

⚠️ 生产环境务必：

1. 设置 `auth_type = "basic"` 并配置强密码
2. 使用 HTTPS（通过反向代理）
3. 考虑 `read_only = true` 限制写入

**自动生成行为：**
当 `auth_type = "basic"` 且 `password` 为空时，首次启动会自动生成密码，并写入 `<storage.root>/secrets.json`；同时在 `username` 为空时使用默认值 `admin`。启动日志只提示凭据位置，不输出明文 WebDAV 密码。

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
| ---- | ---- | ------ | ---- |
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

首次启动且 `users_file` 不存在时，MnemoNAS 会创建默认管理员账号，并把初始密码写入 `users_file` 同目录的 `initial-password.txt`。默认位置是 `<storage.root>/.mnemonas/initial-password.txt`；该文件会在对应管理员首次成功登录后自动删除。

---

### [share] — 文件分享配置

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `false` | 是否启用分享 |
| `store_file` | string | `~/.mnemonas/.mnemonas/shares.json` | 分享数据文件路径 |
| `base_url` | string | `""` | 分享链接基础 URL；用于生成分享响应中的 `url` 字段，留空时返回相对路径 `/s/{id}` |

**示例：**

```toml
[share]
enabled = true
base_url = "https://nas.example.com"
```

`base_url` 只影响接口返回给调用方的分享链接展示值，不改变分享 `id` 本身。配置为空时，后端返回相对路径 `/s/{id}`；配置错误时，分享记录仍然有效，但返回的公开链接会指向错误地址。

---

### [security] — 安全覆盖开关

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `allow_unsafe_no_auth` | bool | `false` | 允许在非 loopback 监听地址上关闭 Web UI/API 认证或 WebDAV Basic Auth |

默认情况下，`server.host` 监听非 loopback 地址时，`auth.enabled = false` 或启用 WebDAV 且 `webdav.auth_type = "none"` 会导致配置校验失败。只有在外层网络边界能确认限制访问范围时，才应把该值显式设为 `true`；设置后仍会输出安全警告。

---

### [favorites] — 收藏夹配置

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `true` | 是否启用收藏 |
| `store_file` | string | `~/.mnemonas/.mnemonas/favorites.json` | 收藏数据文件路径 |

---

### [alerts] — 存储空间告警配置

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `false` | 是否启用存储告警 |
| `check_interval` | duration | `1h` | 检查间隔 |
| `threshold_pct` | float | `90` | 告警阈值（百分比） |
| `critical_pct` | float | `95` | 严重告警阈值（百分比） |
| `min_free_bytes` | uint64 | `10737418240` | 最小可用空间（字节） |
| `cooldown_period` | duration | `4h` | 告警冷却时间 |
| `webhook_url` | string | `""` | Webhook URL |
| `webhook_method` | string | `POST` | Webhook 方法；`POST` 发送 JSON body，`GET` 将告警字段编码到 URL query |
| `webhook_headers` | string[] | `[]` | 自定义 Header（"Key:Value"） |

健康页和诊断导出会显示告警是否启用、运行态是否可用、最近一次检查级别和是否配置了 Webhook；不会暴露 `webhook_url` 或 `webhook_headers`。

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
| ---- | ---- | ------ | ---- |
| `level` | string | `"info"` | 日志级别：`debug`、`info`、`warn`、`error` |
| `format` | string | `"console"` | 输出格式：`console`（人类可读）、`json`（结构化） |
| `output` | string | `"stdout"` | 输出目标：`stdout`、`stderr`、或文件路径 |
| `time_format` | string | `"RFC3339"` | 时间戳格式 |

**日志级别说明：**

| 级别 | 说明 | 使用场景 |
| ---- | ---- | ---------- |
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
| ---- | ---- | ---- |
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
- `storage.root` 不能为空，且不能是文件系统根目录 `/`
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

[webdav]
enabled = true
auth_type = "none"

[auth]
enabled = false

[log]
level = "debug"
```

仅在本机开发时禁用认证。只把 `webdav.auth_type` 设为 `none` 不会关闭 Web UI/API 登录；如需完全无认证，本地环境还需要显式设置 `auth.enabled = false`。

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
password = ""  # 留空时首次启动自动生成；也可以改成密码管理器生成的强密码

[log]
level = "info"
format = "json"
output = "/var/log/mnemonas/server.log"
```

当前配置文件不会展开环境变量；不要把 `${...}` 写入 TOML 并期待运行时替换。

### 只读归档服务器

```toml
[webdav]
enabled = true
read_only = true
auth_type = "basic"
password = ""

[storage.retention]
gc_interval = "0"  # 禁用自动版本清理
```

---

## 相关文档

- [架构设计](architecture.md) — 系统架构说明
- [部署指南](docker-deployment.md) — Docker 部署说明
- [WebDAV 兼容性](webdav-compatibility.md) — 客户端兼容信息
- [安全配置](security.md) — 安全最佳实践
