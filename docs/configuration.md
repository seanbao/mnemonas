# MnemoNAS 配置参考

[English](configuration.en.md) | 简体中文

本文档详细说明 MnemoNAS 的所有配置选项。配置文件使用 TOML 格式。

## 配置文件位置

系统按以下顺序查找配置文件：

1. `nasd --config /path/to/config.toml` — 显式指定的配置文件
2. `$HOME/.mnemonas/config.toml` — 用户目录（与数据同目录）

如果未找到配置文件，系统使用默认配置。Ubuntu/systemd 安装脚本默认生成 `/etc/mnemonas/config.toml`，并在 systemd unit 中用 `--config` 指向该文件。

配置文件可能包含 `auth.jwt_secret`、WebDAV 密码、告警 Webhook Header 和 Telegram Bot Token 等敏感值。MnemoNAS 保存或读取已有配置文件时会把文件权限收紧为 `0600`。

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
email_enabled = false
smtp_host = ""
smtp_port = 587
smtp_username = ""
smtp_password = ""
smtp_from = ""
smtp_to = []

[disk_health]
enabled = false
check_interval = "1h"
probe_timeout = "15s"
cooldown_period = "4h"
command = "smartctl"
temperature_warning_c = 50
temperature_critical_c = 60
media_wear_warning_percent = 80
media_wear_critical_percent = 100

[[disk_health.devices]]
name = "data-disk"
path = "/dev/disk/by-id/ata-example"
type = "sat"
serial = ""

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
| `prefix` | string | `"/dav"` | WebDAV 挂载前缀；会归一化为以 `/` 开头的 URL 路径，不能包含反斜杠、`?`、`#` 或控制字符；启用时不能使用 `/`、`/api`、`/s`、`/health` 或这些保留路由的子路径 |
| `read_only` | bool | `false` | 是否禁止写入类 WebDAV 方法 |
| `auth_type` | string | `"basic"` | 认证方式，支持 `users`、`basic` 和 `none` |
| `username` | string | `""` | `basic` 认证用户名，留空时运行态默认使用 `admin` |
| `password` | string | `""` | `basic` 认证密码，留空时运行态使用 `secrets.json` 中的自动生成密码 |

**运行态行为：**

- 通过设置 API 更新 `webdav` 配置后，运行中的 WebDAV handler 会立即切换到新前缀、读写模式和认证配置
- `auth_type = "users"` 时，WebDAV 使用 MnemoNAS 用户账号的 Basic 登录；管理员访问全局目录，普通用户的 WebDAV 根目录映射到自己的 `home_dir`，guest 账号只读，用户配额同样约束 PUT/COPY 写入
- `password = ""` 且 `auth_type = "basic"` 时，运行态继续使用已有自动生成密码，不要求重启
- `auth_type = "basic"` 是兼容模式：`username/password` 是全局服务凭据，不携带应用层 `home_dir` 隔离；认证启用时，`username` 不应复用现有普通用户或 guest 用户名
- WebDAV 在主 HTTP handler 中优先于 API/前端路由匹配，因此启用时前缀不能覆盖应用保留路由

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
| `host` | string | `"0.0.0.0"` | 监听地址；必须为空、`*`、合法主机名、IPv4 或 IPv6 字面量，不能包含端口、空白或控制字符（`0.0.0.0` 监听所有网络接口，`127.0.0.1` 或 `::1` 仅本地） |
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

`server.host` 只配置监听主机，不包含端口；端口必须写在 `server.port`。IPv6 可写作 `::1` 或 `[::1]`，启动监听时会自动转换为 `net.JoinHostPort` 需要的括号形式。`*` 与空字符串等同于通配监听。

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
| `directory_quotas` | array | `[]` | 目录级容量配额；每项包含 `path` 和 `quota_bytes` |

**存储目录说明：**

- **root**: 存储根目录，不能设置为文件系统根目录 `/`。用户文件位于 `root/files`，内部数据位于 `root/.mnemonas`
- 内部数据目录结构固定在 `root/.mnemonas` 下。
- 启动时会将 `root` 和 `root/files` 权限收紧为 `0750`，内部目录为 `0700`。
- `directory_quotas` 使用 MnemoNAS 逻辑路径，例如 `/team`。上传、复制、移动、回收站恢复、版本恢复和 WebDAV PUT/COPY/MOVE 会在写入前检查当前目录逻辑大小。根目录 `/` 可用于设置全局硬限制。管理员可在存储页查看每个目录配额的当前用量、剩余额度和状态。

**示例：**

```toml
[storage]
root = "~/.mnemonas"
directory_quotas = [
  { path = "/team", quota_bytes = 1099511627776 }, # 1 TiB
]
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
| `grpc_address` | string | `"127.0.0.1:9090"` | Rust 数据面 gRPC 地址；必须是 `host:port`，端口 1-65535，不能包含空白或控制字符 |
| `timeout` | duration | `"30s"` | 数据面连接与重连的总超时预算 |
| `max_retries` | int | `3` | 数据面连接建立/重连时的最大重试次数 |

**示例：**

```toml
[dataplane]
grpc_address = "127.0.0.1:9090"
timeout = "60s"       # 大文件操作可能需要更长超时
max_retries = 5
```

---

### [dataplane.cdc] — 内容定义分块配置

配置 CDC（Content-Defined Chunking）算法参数，影响存储效率和去重率。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `min_chunk_size` | uint32 | `262144` (256KB) | 最小块大小（字节），下限 `65536` (64KB) |
| `avg_chunk_size` | uint32 | `1048576` (1MB) | 平均块大小（字节） |
| `max_chunk_size` | uint32 | `4194304` (4MB) | 最大块大小（字节），上限 `67108864` (64MB) |

**参数调优指南：**

| 场景 | 推荐配置 | 说明 |
| ---- | -------- | ---- |
| **小文件为主** | min=64KB, avg=256KB, max=1MB | 更小的块适合小文件 |
| **默认/混合** | min=256KB, avg=1MB, max=4MB | 平衡存储效率与性能 |
| **大文件/备份** | min=512KB, avg=2MB, max=8MB | 减少元数据开销 |

**约束条件：**

- `65536 <= min_chunk_size < avg_chunk_size < max_chunk_size`
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
| `prefix` | string | `"/dav"` | WebDAV URL 前缀；会归一化为以 `/` 开头的路径，不能包含反斜杠、`?`、`#` 或控制字符；启用时不能覆盖 `/`、`/api`、`/s`、`/health` |
| `read_only` | bool | `false` | 是否为只读模式 |
| `auth_type` | string | `"basic"` | 认证类型：`users`（MnemoNAS 用户账号）、`basic`（全局 Basic Auth）、`none`（无认证） |
| `username` | string | `""` | Basic Auth 用户名 |
| `password` | string | `""` | Basic Auth 密码 |

**认证类型：**

| 类型 | 说明 | 适用场景 |
| ---- | ---- | ---------- |
| `none` | 无认证，任何人可访问 | 本地开发、受信任网络 |
| `users` | 使用 MnemoNAS 用户名/密码进行 HTTP Basic 登录；普通用户根目录映射到自己的 `home_dir`，guest 只读，并执行用户配额 | 推荐的日常挂载模式 |
| `basic` | 单组全局 HTTP Basic 凭据，不携带应用用户身份 | 兼容旧配置或专用服务凭据 |

**安全建议：**

⚠️ 生产环境务必：

1. 优先设置 `auth_type = "users"`；如需兼容旧客户端或服务账号，再使用 `auth_type = "basic"` 并配置强密码
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

### [smb] — SMB 网关预览配置

当前版本不会启动 SMB/Samba 监听器。该配置段只保留给后续 SMB 网关侧车使用，启用后 `nasd --check-config` 会输出预览警告，健康页和诊断导出也会显示 SMB 运行态不可用。生产环境需要局域网挂载时，请继续使用 WebDAV。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `false` | 预览开关；当前不会启动 SMB 服务 |
| `listen` | string | `"127.0.0.1:1445"` | 预留的 SMB 侧车监听地址 |
| `server_name` | string | `"mnemonas"` | 预留的 SMB 服务名 |
| `gateway_socket` | string | `<storage.root>/.mnemonas/run/smb-gateway.sock` | 预留的 MnemoNAS 网关 Unix socket |
| `credential_file` | string | `<storage.root>/.mnemonas/smb-credentials.json` | 预留的 SMB 专用凭据文件；不复用 Web 登录密码 |
| `signing_required` | bool | `true` | 预留的 SMB 签名要求 |
| `encryption_required` | bool | `false` | 预留的 SMB 加密要求 |
| `[[smb.shares]]` | array | `[]` | 预留的共享映射；启用预览开关时至少需要一个共享 |

共享路径必须是 MnemoNAS 内部绝对路径，例如 `/` 或 `/team/docs`。后续侧车会继续通过 MnemoNAS 权限、`home_dir` 和网关 API 访问文件，避免直接把 `files/` 目录交给 Samba 后绕过版本历史、回收站和审计。

---

### [auth] — 认证配置

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `true` | 是否启用 JWT 认证 |
| `jwt_secret` | string | 自动生成 | JWT 签名密钥；留空时使用 `secrets.json` 中的持久化自动生成密钥，显式设置时至少 32 字节 |
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

`base_url` 只影响接口返回给调用方的分享链接展示值，不改变分享 `id` 本身。配置为空时，后端返回相对路径 `/s/{id}`；非空时必须是完整的 `http` 或 `https` URL。

---

### [security] — 安全覆盖开关

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `allow_unsafe_no_auth` | bool | `false` | 允许在非 loopback 监听地址上关闭 Web UI/API 认证或 WebDAV 认证 |

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
| `webhook_url` | string | `""` | Webhook URL；非空时必须是完整的 `http` 或 `https` URL |
| `webhook_method` | string | `POST` | Webhook 方法；`POST` 发送 JSON body，`GET` 将告警字段编码到 URL query |
| `webhook_headers` | string[] | `[]` | 自定义 Header（`"Key: Value"`）；Header 名称必须是合法 HTTP token，值不能包含换行或控制字符 |
| `telegram_enabled` | bool | `false` | 是否启用 Telegram 机器人通知 |
| `telegram_bot_token` | string | `""` | Telegram Bot Token；不会在诊断或设置响应中明文返回 |
| `telegram_chat_id` | string | `""` | Telegram Chat ID 或 `@channel` 用户名 |
| `email_enabled` | bool | `false` | 是否启用 SMTP 邮件通知 |
| `smtp_host` | string | `""` | SMTP 主机名，不包含端口 |
| `smtp_port` | int | `587` | SMTP 端口 |
| `smtp_username` | string | `""` | SMTP 用户名 |
| `smtp_password` | string | `""` | SMTP 密码或应用专用密码 |
| `smtp_from` | string | `""` | 发件人地址，例如 `MnemoNAS <alerts@example.com>` |
| `smtp_to` | string[] | `[]` | 收件人地址列表 |

健康页和诊断导出会显示告警是否启用、运行态是否可用、最近一次检查级别，以及是否配置了 Webhook、Telegram 或邮件；不会暴露 `webhook_url`、`webhook_headers`、`telegram_bot_token` 或 `smtp_password`。同一通知通道也用于备份失败、恢复演练失败、恢复演练缺失/过期提醒、磁盘健康异常、Scrub 异常和登录限流事件。Webhook 发送成功和失败日志只记录 URL 的 scheme 与 host，不记录路径、查询参数、凭据或 GET payload；Telegram 发送错误不会包含 Bot Token。

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
telegram_enabled = true
telegram_bot_token = "123456:ABC..."
telegram_chat_id = "-1001234567890"
email_enabled = true
smtp_host = "smtp.example.com"
smtp_port = 587
smtp_username = "alerts@example.com"
smtp_password = "app-password"
smtp_from = "MnemoNAS <alerts@example.com>"
smtp_to = ["admin@example.com"]
```

---

### [disk_health] — 磁盘健康监控

通过 `smartctl --json --all` 采集已配置设备的 SMART、自检结论、温度、通电时间和设备在线状态。默认关闭；启用前需要安装 `smartmontools`，并确保运行 `nasd` 的用户有权限读取目标设备。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `false` | 是否启用周期性磁盘健康检查 |
| `check_interval` | duration | `1h` | 后台检查间隔 |
| `probe_timeout` | duration | `15s` | 单块磁盘 `smartctl` 探测超时 |
| `cooldown_period` | duration | `4h` | 同一健康级别重复告警的最小间隔 |
| `command` | string | `smartctl` | `smartctl` 可执行文件名或绝对路径；不能包含空白或 shell 参数 |
| `temperature_warning_c` | int | `50` | 默认温度提醒阈值，单位摄氏度 |
| `temperature_critical_c` | int | `60` | 默认温度严重阈值，单位摄氏度 |
| `media_wear_warning_percent` | int | `80` | 介质寿命已用百分比提醒阈值，`0` 表示使用默认值 |
| `media_wear_critical_percent` | int | `100` | 介质寿命已用百分比严重阈值，`0` 表示使用默认值 |
| `devices` | array | `[]` | 需要监控的设备列表 |

`[[disk_health.devices]]` 字段：

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `name` | string | `""` | Web UI 中显示的设备名称 |
| `path` | string | 必填 | 设备绝对路径，推荐使用 `/dev/disk/by-id/...` 这类稳定路径 |
| `type` | string | `""` | 传给 `smartctl --device` 的设备类型，例如 `sat`、`scsi`、`nvme` 或 USB 桥接需要的类型 |
| `serial` | string | `""` | 可选的期望序列号；配置后不匹配会标记为严重异常，用于发现换盘或路径漂移 |
| `temperature_warning_c` | int | 全局值 | 覆盖该设备的提醒温度阈值 |
| `temperature_critical_c` | int | 全局值 | 覆盖该设备的严重温度阈值 |

**运行态行为：**

- `GET /api/v1/maintenance/disk-health` 会立即探测并返回完整设备状态。
- 诊断页和诊断导出只包含脱敏摘要，不包含设备序列号等细节。
- 后台周期检查发现 `warning`、`critical` 或 `unavailable` 时，会以系统用户写入 `disk_health` 活动日志，并按 `cooldown_period` 控制重复记录。
- NVMe `percentage_used`、`available_spare`、`critical_warning`、`media_errors` 以及常见 ATA 寿命属性会参与状态判断。
- 当 `[alerts] enabled = true` 且配置了 Webhook、Telegram 或 SMTP 邮件时，磁盘缺失、SMART 失败、温度过高、序列号不匹配或 SMART 不可用会发送 `disk_health` 事件。
- 设备路径不存在会标记为 `critical`；`smartctl` 不可用或返回无效 JSON 会标记为 `unavailable`。

**示例：**

```toml
[disk_health]
enabled = true
check_interval = "30m"
probe_timeout = "20s"
command = "smartctl"
temperature_warning_c = 50
temperature_critical_c = 60
media_wear_warning_percent = 80
media_wear_critical_percent = 100

[[disk_health.devices]]
name = "data-ssd"
path = "/dev/disk/by-id/nvme-Samsung_SSD_1234"
type = "nvme"
serial = "S6..."
```

---

### [maintenance.scrub] — 数据校验计划

| 配置项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | 是否启用后台周期 Scrub |
| `schedule_interval` | duration | `168h` | 常规 Scrub 间隔 |
| `retry_interval` | duration | `1h` | Scrub 失败后的自动重试间隔 |
| `max_retries` | int | `1` | 单次失败后最多自动重试次数，`0` 表示不自动重试 |

启用后，服务会在后台以系统身份触发完整 Scrub。成功、失败、对象异常和结果持久化警告都会继续复用维护历史、活动日志和已配置的告警通道。若上次 Scrub 失败，后台会先按 `retry_interval` 做有限重试；达到 `max_retries` 后，下一次常规计划仍按 `schedule_interval` 重新开始。这些字段也可通过 Web 设置页或 Settings API 更新，保存后会立即替换运行中的后台调度。

```toml
[maintenance.scrub]
enabled = true
schedule_interval = "168h"
retry_interval = "1h"
max_retries = 1
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
- `grpc_address` 必须是合法 `host:port` 地址，端口范围 1-65535，且不能包含空白或控制字符
- CDC 参数必须满足 `65536 <= min < avg < max <= 67108864`

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
