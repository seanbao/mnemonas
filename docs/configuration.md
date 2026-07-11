# MnemoNAS 配置参考

[English](configuration.en.md) | 简体中文

MnemoNAS 使用 TOML 配置。本文档覆盖配置发现、配置检查、完整示例和主要配置段。

## 配置文件位置

`nasd` 按以下顺序查找配置：

1. `nasd --config /path/to/config.toml`
2. `$HOME/.mnemonas/config.toml`

如果未找到配置文件，则使用默认值。

Ubuntu/systemd 安装器会写入 `/etc/mnemonas/config.toml`，并在 systemd unit 中通过 `--config` 指向它。

配置文件可能包含 `auth.jwt_secret`、WebDAV 密码、提醒 Webhook Header、Telegram Bot Token、WeCom Webhook URL 和 DingTalk Webhook URL 等敏感值。MnemoNAS 保存配置文件时使用 `0600` 权限，并在加载现有配置文件时收紧权限。

## 配置检查

修改配置后先运行：

```bash
nasd --check-config --config /etc/mnemonas/config.toml
```

该命令会校验 TOML、端口、时长、路径和其他硬错误。当 HTTP server 监听范围超过 loopback 时，禁用 `auth.enabled` 或使用 `webdav.auth_type = "none"` 默认会被拒绝；仅在外层网络边界有意限制访问时才设置 `security.allow_unsafe_no_auth = true`。dataplane gRPC 绑定到外部地址仍会输出可部署但有风险的 warning。

长期运行系统应把 `warning:` 行作为部署前检查项处理。

## 完整配置示例

```toml
[server]
host = "0.0.0.0"
port = 8080
read_timeout = "30s"
write_timeout = "60s"
idle_timeout = "120s"
trusted_proxy_hops = 0
trusted_proxy_cidrs = []

[server.tls]
enabled = false
cert_file = ""
key_file = ""
auto_generate = true
cert_dir = ""

[storage]
root = "~/.mnemonas"

[storage.retention]
max_versions = 50
max_age = "2160h"
min_free_space = 10737418240
gc_interval = "24h"

[storage.versioning]
auto_versioned_extensions = [
  ".md", ".txt", ".org", ".rst", ".tex",
  ".go", ".rs", ".py", ".ts", ".js", ".tsx", ".jsx",
  ".c", ".cpp", ".h", ".java", ".kt", ".swift",
  ".toml", ".yaml", ".yml", ".json", ".xml",
  ".sh", ".bash", ".zsh", ".fish",
]
auto_versioned_filenames = [
  "Makefile", "Dockerfile", "Vagrantfile",
  "LICENSE", "README", "CHANGELOG",
  ".gitignore", ".dockerignore", ".editorconfig",
]
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

[smb]
enabled = false
listen = "127.0.0.1:1445"
server_name = "mnemonas"
gateway_socket = ""
credential_file = ""
signing_required = true
encryption_required = false

# [[smb.shares]]
# name = "homes"
# path = "/"
# read_only = false
# allowed_roles = ["admin", "user"]
# allowed_users = []

[backup]
# [[backup.jobs]]
# id = "external-disk"
# name = "外置硬盘备份"
# type = "local"
# source = ""
# destination = "/mnt/backup-drive/mnemonas"
# schedule_interval = "24h"
# schedule_window_start = "02:00"
# schedule_window_end = "05:00"
# stale_after = "72h"
# restore_drill_stale_after = "720h"
# max_snapshots = 7
# max_age = "720h"
# include_config = true
# verify_after_backup = true

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
default_expires_in = "168h"
default_max_access = 0

# [[share.policy_rules]]
# path = "/Family"
# require_password = true
# max_expires_in = "24h"
# max_access = 20
# allowed_users = ["alice"]
# allowed_groups = ["family"]
# allowed_roles = ["user"]

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
webhook_headers = []
telegram_enabled = false
telegram_bot_token = ""
telegram_chat_id = ""
wecom_enabled = false
wecom_webhook_url = ""
dingtalk_enabled = false
dingtalk_webhook_url = ""
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

# [[disk_health.devices]]
# name = "data-disk"
# path = "/dev/disk/by-id/ata-example"
# type = "sat"
# serial = ""

[maintenance.scrub]
enabled = false
schedule_interval = "168h"
retry_interval = "1h"
max_retries = 1

[security]
allow_unsafe_no_auth = false

[log]
level = "info"
format = "console"
output = "stdout"
time_format = "2006-01-02T15:04:05Z07:00"
```

## `[server]`

控制主 API 服务器的行为。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `host` | string | `"0.0.0.0"` | 监听地址；必须为空、`*`、合法主机名、IPv4 或 IPv6 字面量，不能包含端口、空白或控制字符（`0.0.0.0` 监听所有网络接口，`127.0.0.1` 或 `::1` 仅本地） |
| `port` | int | `8080` | HTTP 端口（1-65535） |
| `read_timeout` | duration | `"30s"` | 读取请求的超时时间 |
| `write_timeout` | duration | `"60s"` | 普通响应的总写入超时；浏览器/API 文件与 ZIP 下载将其用作每次写入都会刷新的空闲写入截止期 |
| `idle_timeout` | duration | `"120s"` | Keep-Alive 连接的空闲超时 |
| `trusted_proxy_hops` | int | `0` | 信任的反向代理层数；默认忽略转发头，部署在受信反向代理后方时按 `X-Forwarded-For` 从右向左数第 N 个地址作为客户端 IP |
| `trusted_proxy_cidrs` | string[] | `[]` | 受信反向代理直连来源的 IP 或 CIDR 列表；loopback 来源始终受信，其他来源必须显式配置 |

**示例：**

```toml
[server]
host = "127.0.0.1"  # 仅允许本地访问
port = 8443
read_timeout = "60s"
write_timeout = "120s"
trusted_proxy_hops = 2 # app 前面有两层反向代理时显式设置
trusted_proxy_cidrs = ["10.0.0.0/8"] # 代理不在本机时显式列出来源网段
```

默认 `trusted_proxy_hops = 0`，直接暴露服务时不会采信客户端可伪造的 `X-Forwarded-*` 头。若 MnemoNAS 位于受信反向代理后方，一层代理设置为 `1`；多层代理必须设置为代理总层数，才能从 `X-Forwarded-For` 中选到真实客户端地址。直连来源为 `127.0.0.1` 或 `::1` 时自动受信；代理来自 Docker 网桥、内网负载均衡或其他非 loopback 地址时，必须通过 `trusted_proxy_cidrs` 显式列出代理 IP 或 CIDR。

`server.host` 只配置监听主机，不包含端口；端口必须写在 `server.port`。IPv6 可写作 `::1` 或 `[::1]`，启动监听时会自动转换为 `net.JoinHostPort` 需要的括号形式。`*` 与空字符串等同于通配监听。

浏览器/API 下载响应会在每次响应头、数据块或刷新操作前，把连接写入截止期推进 `server.write_timeout`。因此，持续产生数据的大文件和 ZIP 下载不会仅因总时长超过该值而被截断；单次写入停滞超过该值时仍会超时。该行为不适用于 WebDAV 响应。反向代理还必须配置与预期下载时长和空闲边界一致的超时。

## `[server.tls]`

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `false` | 是否启用 HTTPS |
| `cert_file` | string | `""` | 证书文件路径（留空使用 cert_dir 下的 server.crt） |
| `key_file` | string | `""` | 私钥文件路径（留空使用 cert_dir 下的 server.key） |
| `auto_generate` | bool | `true` | 自动生成自签名证书 |
| `cert_dir` | string | `<storage.root>/.mnemonas/certs` | 证书存放目录 |

`cert_file` 与 `key_file` 必须同时设置或同时留空；同时设置时必须指向不同文件。两者留空时使用 `cert_dir/server.crt` 与 `cert_dir/server.key`。若 `auto_generate = false`，这些证书文件必须已存在。

公开部署通常更适合使用 Caddy 或 Nginx 等反向代理。

## `[storage]`

定义数据存储位置和目录结构。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `root` | string | `~/.mnemonas` | 存储根目录（用户文件在 `root/files`） |
| `directory_quotas` | array | `[]` | 目录级容量配额；每项包含 `path` 和 `quota_bytes` |
| `directory_access_rules` | array | `[]` | 面向用户、用户组和角色的目录读写授权 |

规则：

- **root**: 存储根目录，不能设置为文件系统根目录 `/`。用户文件位于 `root/files`，内部数据位于 `root/.mnemonas`
- 内部数据目录结构固定在 `root/.mnemonas` 下。
- 启动时会将 `root` 和 `root/files` 权限收紧为 `0750`，内部目录为 `0700`。
- systemd 安装的 `mnemonas-dataplane-start` helper 会在启动 dataplane 前拒绝包含换行字符、父目录段、符号链接路径组件或受保护系统目录的 `storage.root` 与 `DATAPLANE_DATA_DIR`。
- `directory_quotas`、`directory_access_rules` 和分享路径策略中的 `path` 字段均使用 MnemoNAS 逻辑路径。路径必须以 `/` 开头，不能包含 Windows/UNC 语法、反斜杠、查询或片段字符、控制字符，或 `.`/`..` 路径段。配置加载和设置 API 会规范化重复斜杠与末尾斜杠；包含 `.` 或 `..` 的路径不会被折叠，会被拒绝。
- `directory_quotas` 使用 MnemoNAS 逻辑路径，例如 `/team`。上传、复制、移动、回收站恢复、版本恢复和 WebDAV PUT/COPY/MOVE 会在写入前检查当前目录逻辑大小。根目录 `/` 可用于设置全局硬限制。
- 存储页会显示目录配额总用量、接近上限、已超限、路径不存在数量、优先复核的目录配额关注清单，以及每个目录配额的当前用量、剩余额度和状态。存储健康摘要会汇总容量、底层校验和目录配额风险，并显示建议处理摘要。
- Web 设置页会在保存前按已保存配额和当前草稿显示新增、修改和删除的目录配额摘要。行式输入中，包含空格或双引号的路径使用双引号包裹，路径内双引号写作 `\"`，例如 `"/Family Photos" 500 GB`。
- `directory_access_rules` 使用干净的 MnemoNAS 绝对路径，例如 `/team`。每条规则可设置 `read_users`、`write_users`、`read_groups`、`write_groups`、`read_roles`、`write_roles`。
- 规则匹配采用最具体路径规则。写权限同时包含读权限，写操作必须显式命中写授权。非管理员 Web/API、WebDAV `users` 模式、搜索、分享、收藏、回收站和最近操作使用同一套权限判定；未命中规则的路径继续按用户 `home_dir` 边界处理。
- Web/API 根目录列表只返回用户 `home_dir` 和可读共享目录的顶层入口。若仅授权嵌套目录，Web/API 与 WebDAV 可将已存在的祖先目录作为只读导航入口，直接子项仍按各自规则过滤，祖先目录下的写入仍需显式写授权。
- Web 设置页会在保存前按已保存规则和当前草稿显示新增、修改和删除的目录权限规则摘要，并基于当前草稿展示规则数量、可读/可写主体、写权限路径和根路径或宽角色授权等覆盖关注项。用户矩阵和未保存规则预览可复制权限复核记录，并保留后端持久化近期复核历史；服务端历史不可用时回退当前浏览器记录。该页使用结构化规则编辑器，路径字段直接填写 MnemoNAS 逻辑路径，包含空格或双引号时不需要手动加引号。

**示例：**

```toml
[storage]
root = "~/.mnemonas"
directory_quotas = [
  { path = "/team", quota_bytes = 1099511627776 }, # 1 TiB
]
directory_access_rules = [
  { path = "/team", read_groups = ["family"], write_groups = ["editors"] },
  { path = "/team/public", read_roles = ["user"], write_users = ["alice"] },
]
```

## `[storage.retention]`

控制文件版本和回收站到期项目的后台保留清理周期。版本数量、版本年龄与剩余空间阈值只作用于文件版本。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `max_versions` | int | `50` | 每个文件最大保留版本数（0 = 无限制） |
| `max_age` | duration | `"2160h"` (90天) | 版本最大保留时间（0 = 永久保留） |
| `min_free_space` | uint64 | `10737418240` (10GB) | 最小剩余磁盘空间（字节），低于此值时写入后触发一次强制版本清理 |
| `gc_interval` | duration | `"24h"` | 版本与到期回收站项目的后台清理间隔，设为 `"0"` 表示禁用周期清理 |

**保留规则优先级：**

1. 最新版本始终保留
2. 超过 `max_age` 的版本可被删除
3. 超过 `max_versions` 的旧版本可被删除
4. 当剩余空间低于 `min_free_space` 时，强制清理最旧版本

## `[storage.trash]`

控制回收站是否启用、保留时间与容量上限。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `true` | 是否启用回收站（关闭后删除将直接永久删除） |
| `retention_days` | int | `30` | 新回收站项目的到期天数；到期后由启用的 `gc_interval` 周期清理 |
| `max_size` | int64 | `10737418240` (10GB) | 回收站最大容量（字节） |

项目进入回收站时会持久化独立到期时间；之后修改 `retention_days` 不会改写已有项目。到期时间表示项目可由后台周期任务清理的时点，不构成最低保留期限。

当写入新的回收站项目会超过 `max_size` 时，系统按删除时间从旧到新清理已有项目，优先为最新删除的项目腾出空间。容量清理可以早于项目到期时间。如果单个最新项目本身已经大于 `max_size`，旧项目仍会先被清理，但该最新项目会保留，因此总占用可能暂时高于 `max_size`。

基于对象身份、禁止覆盖重命名和句柄锚定隔离区的删除事务当前在 Linux 与 macOS 上可用。其他平台的文件列表将删除身份报告为 `null`，观察式删除意图不可用，缺少等价原子原语的实际删除路径会拒绝执行。该限制同时适用于 `trash` 与 `permanent` 模式。

Web 回收站页面会在批量恢复前展示跨目录恢复复核，说明涉及目录、自动清理窗口、冲突处理和执行结果，并支持复制复核记录用于恢复前确认。单项或批量恢复成功后，页面会关联匹配的 `trash_restore` 活动并写入 `restored` 处置状态的活动复核记录；活动日志不可用或未匹配到恢复活动时，不影响恢复本身。

## `[storage.versioning]`

控制自动版本化规则与文件大小阈值。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `auto_versioned_extensions` | string[] | 常见文本/代码后缀 | 默认启用自动版本化的后缀列表 |
| `auto_versioned_filenames` | string[] | 常见配置文件 | 默认启用自动版本化的文件名列表 |
| `max_versioned_size` | int64 | `104857600` | 最大自动版本化文件大小（字节） |

Web 版本历史页面会在执行版本恢复前展示目标文件、覆盖影响、安全保留、执行校验和冲突处理复核，并支持复制复核记录用于恢复前确认。版本恢复成功后，页面会关联匹配路径和版本 hash 的 `restore` 活动并写入 `restored` 处置状态的活动复核记录；活动日志不可用或未匹配到恢复活动时，不影响版本恢复本身。

**示例：**

```toml
[storage.versioning]
auto_versioned_extensions = [".md", ".txt", ".go"]
auto_versioned_filenames = ["README", "LICENSE"]
max_versioned_size = 104857600
```

## `[dataplane]`

数据面 gRPC 端口用于 `nasd` 和 Rust dataplane 之间的内部通信，不提供面向外部客户端的认证层。除非部署环境具备明确的私有网络隔离方案，否则应保持 `127.0.0.1:9090`，不要把 dataplane gRPC 或 HTTP 健康端口直接暴露到公网或不可信局域网。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `grpc_address` | string | `"127.0.0.1:9090"` | Rust 数据面 gRPC 地址；必须是 `host:port`，端口 1-65535，不能包含空白或控制字符 |
| `timeout` | duration | `"30s"` | 数据面连接与重连的总超时预算 |
| `max_retries` | int | `3` | 数据面连接建立/重连时的最大重试次数 |

## `[dataplane.cdc]`

配置 Rust 数据面 FastCDC 文件 API 的算法参数。当前 Go 版本历史路径仍使用整对象 CAS 快照，因此这些参数只影响接入该数据面文件 API 的新写入，不表示当前版本历史已启用分块级去重。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `min_chunk_size` | uint32 | `262144` (256KB) | 最小块大小（字节），下限 `65536` (64KB) |
| `avg_chunk_size` | uint32 | `1048576` (1MB) | 平均块大小（字节） |
| `max_chunk_size` | uint32 | `4194304` (4MB) | 最大块大小（字节），上限 `67108864` (64MB) |

调优指南：

| 用途 | 推荐配置 | 说明 |
| ---- | -------- | ---- |
| **小文件为主** | min=64KB, avg=256KB, max=1MB | 更小的块适合小文件 |
| **默认/混合** | min=256KB, avg=1MB, max=4MB | 平衡存储效率与性能 |
| **大文件/备份** | min=512KB, avg=2MB, max=8MB | 减少元数据开销 |

约束：

```text
65536 <= min_chunk_size < avg_chunk_size < max_chunk_size <= 67108864
```

dataplane 在启动时读取这些值。修改后需要重启 dataplane。64MB 上限用于避免过大的流式 chunk buffer。

## `[webdav]`

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `true` | 是否启用 WebDAV 服务 |
| `prefix` | string | `"/dav"` | WebDAV URL 前缀；会归一化为以 `/` 开头的路径，不能包含反斜杠、`?`、`#` 或控制字符；启用时不能覆盖 `/`、`/api`、`/s`、`/health` |
| `read_only` | bool | `false` | 是否为只读模式 |
| `auth_type` | string | `"basic"` | `users`、`basic` 或 `none`；空值会归一化为 `basic` |
| `username` | string | `""` | Basic Auth 用户名；为空时运行态使用默认值 `admin` |
| `password` | string | `""` | Basic Auth 密码；为空时使用 `secrets.json` 中的自动生成值 |

运行时行为：

- 通过设置 API 更新 `webdav` 配置后，运行中的 WebDAV handler 会立即切换到新前缀、读写模式和认证配置
- Basic Auth 用户名为空时使用运行时默认值 `admin`
- `auth_type = "users"` 使用 MnemoNAS 应用用户通过 HTTP Basic 登录。管理员看到全局命名空间；普通用户把自己的 `home_dir` 作为 WebDAV 根目录，并在根目录列出已授权共享目录的顶层导航入口。为嵌套授权合成的祖先入口只用于只读导航；写入仍需要匹配写授权。Guest 用户只读；写入 `home_dir` 的 PUT/COPY/MOVE 会执行用户配额
- Basic Auth 密码为空时使用 `secrets.json` 中的生成密码。首次启动会自动生成 16 字符可读密码，至少包含小写字母、大写字母和数字，并排除易混淆字符。公网部署中，该文件应为非 symlink 普通文件，并保持私有权限
- WebDAV 在主 HTTP handler 中优先于 API/前端路由匹配，因此启用时前缀不能覆盖应用保留路由
- `auth_type = "basic"` 是兼容模式：使用一组全局服务凭据，不提供应用级 `home_dir` 隔离

安全建议：

- 生产部署应优先使用 `auth_type = "users"`。Basic Auth 应仅用于旧客户端或专用服务凭据，并在手动配置时使用密码管理器生成的值。
- 网络暴露时应通过反向代理终止 HTTPS。
- `read_only = true` 可降低只读挂载的写入风险。

示例：

```toml
[webdav]
enabled = true
prefix = "/dav"
read_only = false
auth_type = "basic"
username = "webdav-service"
password = "" # 留空使用自动生成密码；自定义时填入密码管理器生成的随机强密码
```

## `[smb]`

当前版本不会启动 SMB/Samba 监听器。该配置段只保留给后续 SMB 网关侧车使用，启用后 `nasd --check-config` 会输出预览警告，健康页和诊断导出也会显示 SMB 运行态不可用。当前局域网挂载应继续使用 WebDAV。

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

共享路径必须是 MnemoNAS 内部绝对路径，例如 `/` 或 `/team/docs`。后续侧车会继续通过 MnemoNAS 权限、`home_dir` 和网关 API 访问文件，避免直接把 `files/` 目录交给 Samba 后绕过版本历史、回收站和活动记录。

## `[backup]`

备份任务默认未配置。`[[backup.jobs]]` 支持 `local`、`restic` 和 `rclone` 三种类型，并由维护页、API 或后台调度触发。

维护页可创建以当前 `storage.root` 为来源的 `local` 整机备份任务。Web UI 生成的任务默认每 24 小时运行、保留 7 个快照、包含配置并在备份后执行校验；选择仅手动运行时，`schedule_interval` 保存为 `"0"`。保存过程会更新 `config.toml` 和当前备份管理器，不需要重启服务。自定义 `source`、`restic` 和 `rclone` 任务仍通过配置文件管理。

| 配置项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `[[backup.jobs]]` | array | `[]` | 备份任务列表 |

`[[backup.jobs]]` 字段：

| 配置项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `id` | string | 必填 | 任务标识；只能包含 ASCII 字母、数字、`-`、`_`、`.`，最大 64 字符，且不能为 `.` 或 `..` |
| `name` | string | 必填 | 维护页显示名称 |
| `type` | string | `"local"` | `local`、`restic` 或 `rclone`；空值会归一化为 `local` |
| `source` | string | `storage.root` | 备份源目录；必须是绝对路径。空值会使用 `storage.root` |
| `destination` | string | `""` | `local` 任务的本地目标目录；必须是绝对路径，且不能位于备份源或 `storage.root` 内 |
| `repository` | string | `""` | `restic` 仓库；`restic` 任务必填，只接受位于备份源和 `storage.root` 之外的绝对本地路径，或显式的 `rest:http://`、`rest:https://` REST 服务地址 |
| `remote` | string | `""` | `rclone` 远端路径；`rclone` 任务必填，且必须以 `<config_file 中的命名 remote>:<path>` 形式引用配置段 |
| `command` | string | 任务类型 | `restic` 或 `rclone` 可执行文件；为空时使用任务类型名称。非空值必须是无空白和控制字符的可执行名或绝对路径 |
| `password_file` | string | `""` | `restic` 密码文件；`restic` 任务必填，必须是存在的非 symlink 普通文件，不能位于备份源或 `storage.root` 内，且最大为 4 MiB |
| `config_file` | string | `""` | `rclone` 明文配置文件；`rclone` 任务必填，必须是存在的非 symlink 普通文件，不能位于备份源或 `storage.root` 内，且最大为 4 MiB |
| `extra_args` | string[] | `[]` | 附加到备份命令的 argv 项；不能包含空项、控制字符或覆盖仓库、密码文件等备份身份的参数。rclone 任务当前只接受 `--fast-list`；恢复命令不会复用这些参数 |
| `disabled` | bool | `false` | 停用任务；停用后不会自动调度，也不能手动运行 |
| `schedule_interval` | duration | `"0"` | 自动调度间隔；`0` 或空值表示仅手动运行 |
| `schedule_window_start` | string | `""` | 自动调度窗口开始时间，格式为 `HH:MM` |
| `schedule_window_end` | string | `""` | 自动调度窗口结束时间，格式为 `HH:MM`；支持跨午夜窗口 |
| `stale_after` | duration | `schedule_interval * 2` | 备份成功状态过期阈值；仅当值大于 `0` 时显式生效。未设置且存在自动调度时使用调度间隔的两倍 |
| `restore_drill_stale_after` | duration | `"720h"` | 恢复演练过期提醒阈值；空值或省略时使用 30 天 |
| `retention_policy` | string | `""` | 外部保留策略说明；`restic` 和 `rclone` 任务未设置时，保留检查会报告 warning |
| `max_snapshots` | int | `0` | `local` 任务最多保留快照数；`0` 表示不按数量清理 |
| `max_age` | duration | `"0"` | `local` 任务快照最大保留时间；`0` 表示不按时间清理 |
| `include_config` | bool | `false` | `local` 备份是否复制当前配置文件 |
| `verify_after_backup` | bool | `false` | 备份完成后是否执行校验；`local` 校验快照文件哈希，`restic` 执行 `restic check`，`rclone` 执行 `rclone check --one-way` |
| `exclude` | string[] | `[]` | 排除模式；不能包含空项或控制字符 |

运行时行为：

- `local` 任务在 `destination/<job-id>/snapshots/<run-id>/` 下创建快照。目标目录不能位于 `storage.root` 或备份源内，路径组件不能通过 symlink 绕过边界。v2 manifest 会精确登记 `data/` 目录拓扑、空目录和 POSIX `rwx` 权限位，并拒绝 setuid、setgid、sticky bit 等特殊权限位；ACL、扩展属性和所有者身份不在该证据范围内。`config/` 是不进入目录清单的固定结构目录，恢复时使用 `0700`。
- `restic` 和 `rclone` 任务直接以 argv 调用外部命令，不通过 shell 拼接命令。`command`、凭据文件、`extra_args`、`exclude` 和远端位置都会按配置校验规则处理。
- 远端任务会在解析后的系统临时目录中创建权限为 `0700` 的私有目录，并从经过验证的凭据文件创建权限为 `0600` 的快照；临时目录必须位于备份源与 `storage.root` 之外，命令只读取该快照。
- rclone 配置必须定义 `remote` 引用的命名配置段及其 `type`，并保持静态、自包含。配置不能启用 `env_auth`、包含 `${...}` 展开，也不能使用名称含 `_file`、`_path`、`command`、`agent`、`ssh` 或 `token` 的非空配置项。当前不支持加密 rclone 配置或 token 自动写回。
- 子进程不会继承云服务凭据、代理、SSH agent 或可覆盖任务身份的 `RESTIC_*`、`RCLONE_*` 环境变量；执行身份只由显式配置的仓库、remote 和凭据文件决定。restic 不接受依赖云 SDK、SSH 或 rclone 外部凭据发现的仓库形式。
- 备份状态根目录固定为 `<storage.root>/.mnemonas/backup`。备份管理器在其整个生命周期内持有 `backup-state.lock`，同一状态根目录同时只允许一个写入者。锁文件在关闭后保留，不表示存在过期锁；MnemoNAS 运行时不得删除。
- Unix 上的备份状态根目录必须由当前服务账号或 `root` 拥有，且不得允许 group/other 写入；祖先目录必须由可信账号拥有，并且不得让其他本地账号替换状态根目录。状态锁无法安全取得时，备份管理器不会初始化。
- 备份管理器保留已锁定状态根目录的身份，并在操作与状态写入边界复核原路径。路径被移动、删除或替换时，当前管理器会立即隔离，不会向替换目录写入。已原子提交的 `completed` 结果保持完成，并返回“请检查状态目录并重启服务”的 warning；后续备份 API 返回 `503`。未完成原子提交的普通硬持久化失败返回 `500`。
- `local` 任务在备份、恢复预览、恢复演练、恢复、恢复后只读校验和保留检测期间持有 `<destination>/<job-id>/.mnemonas-target.lock`，防止不同进程同时操作同一本地任务目录。Unix 上的任务目录必须由当前服务账号或 `root` 拥有，且不得允许 group/other 写入；祖先目录必须由可信账号拥有，可写祖先只有在设置 sticky bit 且能防止其他账号替换可信子目录时才会被接受。不满足条件时，本地操作拒绝继续。
- FAT、exFAT 等不持久保存 Unix 所有者和 mode 的外置盘必须使用安全的挂载映射。按实际服务 UID/GID 设置 `uid=<mnemonas-uid>,gid=<mnemonas-gid>,dmask=0077,fmask=0177`，使任务目录对服务进程显示为可信所有者，且 group/other 不可写。不具备可靠 Unix mode，且没有等价挂载约束的目标会被 `local` 任务以 fail-closed 方式拒绝。
- v2 本地 manifest 要求来源、快照和恢复目标具备稳定的 POSIX `rwx` 权限语义。原生 Windows 无法保真保存这些权限位，因此 MnemoNAS 会拒绝在原生 Windows 上执行 v2 本地备份、恢复预览、恢复、恢复演练、恢复校验和保留检测。
- 远端任务不创建本地目标锁。restic 任务依赖 restic 原生仓库锁；rclone 不提供适用于所有 remote 的通用分布式互斥机制，多个任务或实例共用同一 rclone 路径时必须由外部调度串行化备份、恢复和校验操作。
- 备份运行会在创建本地快照或启动远端命令前持久化 `running` 状态。该状态无法保存时，运行失败且不产生备份目标副作用。
- 本地快照会先同步内容、manifest 和暂存目录，再发布到最终目录并同步快照根目录。包含 manifest 证据的成功状态提交后，系统才会清理旧快照并执行保留检测。
- 成功状态在原子替换前保存失败时，之前的 `last_successful_run` 保持不变，未绑定的最终快照不参与恢复。备份已经提交但后续目录同步、清理、保留检测或结果持久化未完整完成时，运行保持 `completed`，API 通过 `warning=true`、`warnings[]` 和 HTTP `Warning` 响应头报告不完整步骤。
- 本地恢复、预览和恢复演练只接受 `status.json.last_successful_run` 保存的 v2 manifest 路径、大小与摘要证据，不扫描未绑定的快照目录。恢复、恢复演练和恢复后只读校验均以受信 manifest 为目录拓扑和权限真值，不从快照当前目录树推断。恢复安装前还会按显式目标布局复核完整目录树，拒绝额外或缺失条目、权限漂移和大小写折叠冲突。v1 manifest 或缺少证据时，需要先重新完成一次本地备份。
- 恢复记录内部保存 manifest 大小和原始字节摘要证据，供后续只读校验选择同一次恢复的可信基线；仅改变 JSON 空白或字段排列也会使绑定失效。该证据不会作为公共 `RestoreResult` API 字段返回。
- 显式恢复使用与目标同级的私有暂存目录，并在安装和清理边界复核其文件系统身份。Unix 上暂存目录及其祖先还必须满足可信所有者和不可替换约束；路径身份发生变化时操作以不安全路径失败。
- 上述 v2 目录清单语义仅适用于 `local`；`restic` 与 `rclone` 仍采用各自后端的快照、复制和校验语义。
- `schedule_window_start` 与 `schedule_window_end` 只限制自动调度；维护页和 API 的手动运行不受调度窗口限制。窗口使用服务器本地时间。
- `restore_drill_stale_after` 用于恢复演练缺失或过期提醒。空值、`"0"` 或省略值在运行时按 30 天处理。提醒冷却标记仅在通知通道成功返回后持久化；投递或标记持久化失败会在后续调度重试。
- 本地保留清理会始终保留当前快照，并固定保留已完成恢复历史仍引用的快照。自动清理只处理 manifest 身份与快照树布局校验通过的 v2 快照；v1 快照会固定保留并产生人工处置提醒，不参与 v2 的数量或时间清理计算。`restic` 与 `rclone` 的实际保留策略由外部工具或云端生命周期规则负责，`retention_policy` 用于记录该策略已由部署侧确认。
- 维护页单任务恢复会先生成执行前复核，说明目标目录、恢复内容、配置文件处理、预检结果、写入边界和恢复后检查，并支持复制复核记录用于恢复前确认。
- 备份、恢复、恢复演练、恢复校验和保留检查的提醒复用 `[alerts]` 通道；外部通知不会暴露任务名称、源路径、目标路径、快照路径、原始 warning 或底层错误文本。

```toml
[backup]

[[backup.jobs]]
id = "external-disk"
name = "外置硬盘备份"
type = "local"
source = ""
destination = "/mnt/backup-drive/mnemonas"
schedule_interval = "24h"
schedule_window_start = "02:00"
schedule_window_end = "05:00"
stale_after = "72h"
restore_drill_stale_after = "720h"
max_snapshots = 7
max_age = "720h"
include_config = true
verify_after_backup = true
exclude = [".mnemonas/thumbnails"]
```

## `[auth]`

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `true` | 是否启用 JWT 认证 |
| `jwt_secret` | string | 自动生成 | JWT 签名密钥；留空时使用 `secrets.json` 中的持久化自动生成密钥，显式设置时至少 32 字节 |
| `access_token_ttl` | duration | `15m` | Access Token 有效期；必须不少于 `30s`，公网部署建议不超过 `1h` |
| `refresh_token_ttl` | duration | `168h` | Refresh Token 有效期；公网部署建议不超过 `720h`（30 天） |
| `users_file` | string | `<storage.root>/.mnemonas/users.json` | 用户数据文件路径 |

首次启动且 `users_file` 不存在，或其中没有启用中的管理员时，MnemoNAS 会创建默认或恢复管理员账号，并把初始密码写入 `users_file` 同目录的 `initial-password.txt`。默认位置是 `<storage.root>/.mnemonas/initial-password.txt`。登录和会话刷新会保留该文件；对应管理员成功修改或重置密码后，文件才会删除。`users_file` 使用 `schema_version: 1` 对象格式，并要求每个用户显式保存 `must_change_password` 和正数 `credential_version`。加载时还会验证 bcrypt 密码哈希、角色与 `home_dir` 的组合，以及非负 `quota_bytes`。旧数组格式、缺少版本或必需安全字段、无效用户不变量以及未知版本都会导致认证初始化失败。`mnemonas-doctor` 会检查版本格式和管理员可用性所需的安全字段。

## `[share]`

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `false` | 是否启用分享 |
| `store_file` | string | `<storage.root>/.mnemonas/shares.json` | 分享数据文件路径 |
| `base_url` | string | `""` | 分享链接基础 URL；用于生成分享响应中的 `url` 字段，留空时返回相对路径 `/s/{id}`；非空时必须是完整 `http` 或 `https` URL，不能包含 userinfo、查询参数、片段、编码后的查询或片段标记、反斜杠、重复路径斜杠或 `.`/`..` 路径段，且主机名必须有效 |
| `default_expires_in` | duration | `168h` | 新创建分享的默认有效期；设为 `0` 或留空表示默认不过期。公网部署建议保留明确默认有效期，且不超过 `720h`（30 天） |
| `default_max_access` | int | `0` | 新创建分享的默认逻辑下载会话上限；`0` 表示不限制 |
| `[[share.policy_rules]]` | array | `[]` | 按 MnemoNAS 路径设置更严格的分享约束和允许创建/维护者范围；最具体路径规则优先生效 |

**示例：**

```toml
[share]
enabled = true
base_url = "https://nas.example.com"
default_expires_in = "168h"
default_max_access = 0

[[share.policy_rules]]
path = "/Family"
require_password = true
max_expires_in = "24h"
max_access = 20
allowed_users = ["alice"]
allowed_groups = ["family"]
allowed_roles = ["user"]
```

`base_url` 只影响接口返回给调用方的分享链接展示值，不改变分享 `id` 本身。配置为空时，后端返回相对路径 `/s/{id}`。

非空 `base_url` 必须满足以下规则：

- 使用完整的 `http` 或 `https` URL。
- 不包含用户名、密码或其他 userinfo。
- 不包含查询参数、片段、编码后的查询或片段标记、反斜杠、重复路径斜杠或 `.`/`..` 路径段。
- 主机名是有效域名或 IP，不能包含空标签或下划线。

单个 FQDN 尾点会按同一主机处理，重复尾点会被拒绝。编码后的查询或片段标记、反斜杠、重复路径斜杠和点段路径会被拒绝，避免代理或浏览器规范化后生成不一致的分享地址。

公网部署若使用反向代理应用基础路径，应填写该基础路径本身，例如 `https://nas.example.com/mnemonas`，不要把 `/s` 分享路由写入 `base_url`。路径以 `/s` 结尾时，安全自检和公网诊断会提示人工复核。

默认有效期和默认下载次数只影响之后创建的分享；创建请求体显式传入 `expires_in` 或 `max_access` 时以请求体为准。`max_access` 按成功签发的下载票据计数；目录浏览、密码验证以及同一票据的 Range 或断点续传不另行计数。

路径策略的 `path` 使用与目录配额和目录访问规则相同的 MnemoNAS 逻辑路径规则。路径策略可以设置 `require_password`、`max_expires_in`、`max_access`、`allowed_users`、`allowed_groups` 和 `allowed_roles`。命中策略时，未设置密码的创建请求，以及会使既有分享保持或变为无密码的更新请求会被拒绝。超过策略上限、显式清空上限字段，或既有分享缺少对应限制或超过策略上限时，有效期和下载次数会自动压到上限。

`allowed_users`、`allowed_groups` 和 `allowed_roles` 用于限制可在该路径创建或维护分享链接的认证调用方；用户值匹配用户 ID 或用户名，组值匹配用户组，角色值支持 `admin`、`user` 和 `guest`。管理员可绕过该范围限制以便处理既有分享；关闭应用认证时不会执行创建者范围限制。该限制只影响认证 API 中分享链接的创建和维护，不改变已经生成的公开分享访问边界。

Web 分享创建弹窗会在提交前展示策略来源、密码要求、有效期和下载次数的实际复核摘要，并标出路径策略上限收紧的项目。Web 设置页会在保存前展示分享功能、基础 URL、默认有效期、默认下载次数和路径策略相对已保存配置的变更摘要，并在覆盖摘要中列出根路径规则、未继承上级限制的最具体路径规则、子路径放宽上级有效期/下载次数/允许创建者范围的规则，以及等价重复规则等整理建议。分享列表会汇总需复核、无密码、覆盖较大、即将到期和长期未下载链接，提供对应筛选、当前范围复核记录写入、按分享类型筛选的复核历史跳转、复核详情展开、需处理分享的停用入口、单项启停入口和删除入口；成功停用需处理分享、停用单项分享或删除分享后，会关联匹配的 `unshare` 活动并写入访问入口关闭执行结果复核记录；成功重新启用单项分享后，会关联匹配的 `share` 活动并写入确认保留状态的重新启用执行结果复核记录。

## `[security]`

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `allow_unsafe_no_auth` | bool | `false` | 允许在非 loopback 监听地址上关闭 Web UI/API 认证或 WebDAV 认证 |

默认情况下，`server.host` 监听非 loopback 地址时，`auth.enabled = false` 或启用 WebDAV 且 `webdav.auth_type = "none"` 会导致配置校验失败。只有在外层网络边界能确认限制访问范围时，才应把该值显式设为 `true`；设置后仍会输出安全警告。`mnemonas-doctor` 在普通部署诊断中也会报告这些无认证姿态，便于安装后复核外层网络边界是否符合预期。

## `[favorites]`

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `true` | 是否启用收藏 |
| `store_file` | string | `<storage.root>/.mnemonas/favorites.json` | 收藏数据文件路径 |

管理员可在收藏页独立加载、重置和保存收藏开关。Web 表单仅提交 `favorites`，不覆盖其他运行时设置。

## `[alerts]`

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `false` | 是否启用存储提醒 |
| `check_interval` | duration | `1h` | 检查间隔 |
| `threshold_pct` | float | `90` | 提醒阈值（百分比） |
| `critical_pct` | float | `95` | 严重提醒阈值（百分比） |
| `min_free_bytes` | uint64 | `10737418240` | 最小可用空间（字节） |
| `cooldown_period` | duration | `4h` | 提醒冷却时间 |
| `webhook_url` | string | `""` | Webhook URL；非空时必须是完整的 `http` 或 `https` URL，并使用合法主机名或 IP 地址 |
| `webhook_method` | string | `POST` | Webhook 方法；`POST` 发送 JSON body，`GET` 将提醒字段编码到 URL query |
| `webhook_headers` | string[] | `[]` | 自定义 Header（`"Key: Value"`）；Header 名称必须是合法 HTTP token，不能重复（大小写不敏感），值不能包含换行或控制字符 |
| `telegram_enabled` | bool | `false` | 是否启用 Telegram 机器人通知 |
| `telegram_bot_token` | string | `""` | Telegram Bot Token；不会在诊断或设置响应中明文返回 |
| `telegram_chat_id` | string | `""` | Telegram Chat ID 或 `@channel` 用户名 |
| `wecom_enabled` | bool | `false` | 是否启用企业微信/WeCom 群机器人通知 |
| `wecom_webhook_url` | string | `""` | 企业微信群机器人 Webhook URL；启用企业微信通知时必填，非空时必须是完整的 `http` 或 `https` URL，并使用合法主机名或 IP 地址；诊断和设置响应不会明文返回 |
| `dingtalk_enabled` | bool | `false` | 是否启用钉钉群机器人通知 |
| `dingtalk_webhook_url` | string | `""` | 钉钉群机器人 Webhook URL；启用钉钉通知时必填，非空时必须是完整的 `http` 或 `https` URL，并使用合法主机名或 IP 地址；诊断和设置响应不会明文返回 |
| `email_enabled` | bool | `false` | 是否启用 SMTP 邮件通知 |
| `smtp_host` | string | `""` | SMTP 主机名，不包含端口 |
| `smtp_port` | int | `587` | SMTP 端口 |
| `smtp_username` | string | `""` | SMTP 用户名 |
| `smtp_password` | string | `""` | SMTP 密码或应用专用密码 |
| `smtp_from` | string | `""` | 发件人地址，例如 `MnemoNAS <alerts@example.com>` |
| `smtp_to` | string[] | `[]` | 收件人地址列表 |

告警 Webhook、Telegram、WeCom 和 DingTalk 出站请求不跟随 HTTP 重定向；`3xx` 响应会作为投递失败处理。

设备状态页可独立加载、校验、重置和保存通知设置。Web 表单仅提交 `alerts`，不覆盖其他运行时设置。设备状态页和诊断也会显示提醒状态，以及 Webhook、Telegram、WeCom、DingTalk 或邮件通知是否已配置。邮件通道只有在启用邮件提醒，且 SMTP host、port、sender 和至少一个非空 recipient 都存在时才标记为已配置。

诊断不会暴露 webhook URL、webhook headers、`telegram_bot_token`、`wecom_webhook_url`、`dingtalk_webhook_url`、SMTP host、SMTP username、`smtp_password`、sender address 或 recipient address。

同一通知通道也用于以下事件：

- 备份失败、带 warning 的备份运行、显式恢复失败或 warning、恢复后只读校验失败或 warning、恢复演练失败或 warning、恢复演练过期或缺失提醒，以及保留检查失败或 warning。
- 磁盘健康异常、Scrub 异常、登录限流、目录访问或分享策略变更。
- 启用分享在 72 小时内过期的聚合提醒。

容量事件使用 `storage_alert`。外部载荷保留容量指标和 `path_scope = "configured_storage_root"`，但将 `path` 设为 `<omitted>`，文本通道不包含原始存储根目录路径。

备份相关事件类型包括 `backup_run`、`backup_restore`、`backup_restore_verify`、`backup_restore_drill` 和 `backup_retention_check`。`scrub_run` 详情省略对象哈希和底层错误文本；`login_rate_limited` 详情只包含用户名状态和客户端地址范围，不包含原始用户名或客户端地址；分享相关事件类型包括 `share_expiring_soon`，其详情使用聚合计数，不包含分享路径、URL、密码或 ID。

备份管理器对自定义 `backup.Notifier` 使用同步调用，每次 `NotifyBackupEvent` 都携带 10 秒 deadline，并会在管理器关闭时取消。自定义实现必须监听 `ctx.Done()`，并在 context 取消或超时后及时返回；否则可能阻塞备份操作或服务关闭。内置 SMTP 传输使用 30 秒默认超时，并遵循更早的上游 deadline；备份事件因此仍受 10 秒通知预算限制。

管理员保存提醒配置后，可从设备状态页或 `POST /api/v1/settings/alerts/test` 发送 `alert_test` 事件。存在未保存更改、提醒未启用或没有已配置的启用通道时，Web UI 不会发送测试提醒。

Webhook、WeCom 和 DingTalk 成功或失败日志只记录 URL scheme 和 host，不记录 path、query string、凭据或 GET payload 字段。Telegram 发送错误不包含 bot token。SMTP 成功日志不记录 SMTP host 或地址，SMTP 失败错误不回显 SMTP host、username、password、sender、recipient 或原始服务端错误文本。

## `[disk_health]`

通过 `smartctl --json --all` 采集已配置设备的 SMART、自检结论、温度、通电时间和设备在线状态。默认关闭；启用前需要安装 `smartmontools`，并确保运行 `nasd` 的用户有权限读取目标设备。

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `enabled` | bool | `false` | 是否启用周期性磁盘健康检查 |
| `check_interval` | duration | `1h` | 后台检查间隔 |
| `probe_timeout` | duration | `15s` | 单块磁盘 `smartctl` 探测超时 |
| `cooldown_period` | duration | `4h` | 同一健康级别重复提醒的最小间隔 |
| `command` | string | `smartctl` | `smartctl` 可执行文件名或绝对路径；不能包含空白、控制字符或 shell 参数 |
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

运行时行为：

- 管理员可在设备状态页独立加载、校验、重置和保存磁盘健康设置。Web 表单仅提交 `disk_health`，不覆盖其他运行时设置。
- `GET /api/v1/maintenance/disk-health` 会立即探测并返回完整设备状态。
- 诊断页和诊断导出只包含脱敏摘要，不包含设备序列号等细节。
- 后台周期检查发现 `warning`、`critical` 或 `unavailable` 时，会以系统用户写入 `disk_health` 最近操作，并按 `cooldown_period` 控制重复记录。
- NVMe `percentage_used`、`available_spare`、`critical_warning`、`media_errors` 以及常见 ATA 寿命属性会参与状态判断。
- 当 `[alerts] enabled = true` 且配置了 Webhook、Telegram、企业微信、钉钉或 SMTP 邮件时，磁盘缺失、SMART 失败、温度过高、序列号不匹配或 SMART 不可用会发送 `disk_health` 事件。事件详情只包含聚合计数，不包含设备名、完整设备路径、序列号或 warning 文本。
- 设备路径不存在会标记为 `critical`；`smartctl` 不可用或返回无效 JSON 会标记为 `unavailable`。

## `[maintenance.scrub]`

| 配置项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | 是否启用后台周期 Scrub |
| `schedule_interval` | duration | `168h` | 常规 Scrub 间隔 |
| `retry_interval` | duration | `1h` | Scrub 失败后的自动重试间隔 |
| `max_retries` | int | `1` | 单次失败后最多自动重试次数，`0` 表示不自动重试 |

启用后，服务会在后台以系统身份触发完整 Scrub。成功、失败、对象异常和结果持久化警告都会继续复用维护历史、最近操作和已配置的提醒通道。若上次 Scrub 失败，后台会先按 `retry_interval` 做有限重试；达到 `max_retries` 后，下一次常规计划仍按 `schedule_interval` 重新开始。这些字段也可通过备份与维护页或 Settings API 更新；Web 表单仅提交 `maintenance.scrub`，保存后会立即替换运行中的后台调度，且不会覆盖其他运行时设置。

```toml
[maintenance.scrub]
enabled = true
schedule_interval = "168h"
retry_interval = "1h"
max_retries = 1
```

## `[log]`

| 选项 | 类型 | 默认值 | 说明 |
| ---- | ---- | ------ | ---- |
| `level` | string | `"info"` | 日志级别：`debug`、`info`、`warn`、`error` |
| `format` | string | `"console"` | 输出格式：`console`（人类可读）、`json`（结构化） |
| `output` | string | `"stdout"` | 输出目标：`stdout`、`stderr`、或文件路径 |
| `time_format` | string | `"2006-01-02T15:04:05Z07:00"` | 时间戳格式。支持 `RFC3339`、`RFC3339Nano`、`Unix`、`UnixMs`、`UnixMicro`、`UnixNano`，也可填写 Go 时间 layout |

示例：

```toml
[log]
level = "debug"
format = "json"               # 便于日志分析
output = "/var/log/mnemonas/server.log"
time_format = "2006-01-02T15:04:05Z07:00"
```

`console` 和 `json` 日志都识别上述命名格式。`Unix*` 命名格式在 `json` 输出中写入数值时间戳；在 `console` 输出中保留原始数值时间戳，便于与日志采集系统保持一致。自定义值按 Go `time.Format` layout 解释，例如 `2006-01-02T15:04:05Z07:00`。

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

支持 `1h30m` 等组合形式。

## 环境变量覆盖

环境变量配置覆盖尚未支持。当前配置文件不会展开 `${...}` 形式的环境变量；不要把 `${...}` 写入 TOML 并期待运行时替换。

## 常见配置

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
root = "/srv/mnemonas"

[storage.retention]
max_versions = 50
max_age = "2160h"
min_free_space = 107374182400  # 100GB

[webdav]
enabled = true
auth_type = "users"  # 生产部署优先使用 MnemoNAS 应用用户

[auth]
enabled = true

[log]
level = "info"
format = "json"
output = "/var/log/mnemonas/server.log"
```

旧客户端或专用服务凭据确实需要全局 Basic Auth 时，可改用 `auth_type = "basic"`，并使用密码管理器生成的强密码或留空由首次启动生成。

### 只读归档服务器

```toml
[webdav]
enabled = true
read_only = true
auth_type = "basic"
password = ""

[storage.retention]
gc_interval = "0"  # 禁用版本与回收站到期项目的周期清理
```

## 相关文档

- [架构设计](architecture.md) — 系统架构说明
- [部署指南](docker-deployment.md) — Docker 部署说明
- [WebDAV 兼容性](webdav-compatibility.md) — 客户端兼容信息
- [安全配置](security.md) — 安全配置建议
