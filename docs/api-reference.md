<!-- markdownlint-disable MD022 MD031 MD032 MD036 MD040 MD060 -->

# MnemoNAS API 参考文档

[English](api-reference.en.md) | 简体中文

本文档描述 MnemoNAS REST API 的所有端点、请求/响应格式和错误处理。

## 基础信息

- **Base URL**: `http://localhost:8080` (默认)
- **Content-Type**: `application/json` (除文件上传外)
- **认证**: 支持 JWT Token 认证（可通过配置启用/禁用）
- JSON 请求体采用严格解析：写接口会拒绝未知字段和拼接的多个 JSON 值，并返回 `400 invalid request body`

### 认证方式

Web UI 使用同源 `HttpOnly` cookie 作为主会话。API 客户端仍可在请求头中携带 JWT Token：

```
Authorization: Bearer <access_token>
```

登录和刷新接口会设置 `mnemonas_access` 与 `mnemonas_refresh` cookie。浏览器客户端可发送请求头 `X-MnemoNAS-Session-Mode: cookie`，此时响应 JSON 不返回 bearer token，只返回用户信息与过期时间等非敏感字段。

## 通用响应格式

### 成功响应（多数 /api/v1 端点）

```json
{
  "success": true,
  "data": {
    "id": "example"
  },
  "message": "操作成功",
  "request_id": "optional",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### 错误响应（多数 /api/v1 端点）

```json
{
  "code": "BAD_REQUEST",
  "message": "错误描述",
  "details": {
    "field": "path"
  },
  "request_id": "optional",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### 认证端点响应（auth 模块）

认证模块成功响应与多数 `/api/v1` 端点一致，业务数据位于 `data` 字段；错误响应使用 `success: false` 和结构化 `error` 对象：

```json
{
  "success": false,
  "error": {
    "code": "ERROR_CODE",
    "message": "错误描述"
  }
}
```

### 分享/收藏端点响应

认证后的分享链接管理端点 `/api/v1/shares` 使用 `success + data (+ message)` 包装；公开分享 API 推荐使用 `/api/v1/public/shares/*`，其成功响应保持原始 JSON 对象或数组，错误响应使用 `success: false` 和结构化 `error` 对象。兼容路径 `/s/*` 继续返回相同的公开分享 JSON / 下载响应，适用于不经过 SPA 的直接调用。

```json
{
  "success": true,
  "data": {
    "id": "example"
  }
}
```

公开分享错误响应示例：

```json
{
  "success": false,
  "error": {
    "code": "SHARE_PASSWORD_RATE_LIMITED",
    "message": "too many attempts, try later"
  }
}
```

### HTTP 状态码

| 状态码 | 说明 |
|--------|------|
| 200 | 成功 |
| 201 | 创建成功 |
| 400 | 请求参数错误 |
| 401 | 未认证或认证已失效 |
| 403 | 已认证但无权限 |
| 409 | 资源状态冲突 / 当前操作不可执行 |
| 404 | 资源不存在 |
| 429 | 请求过于频繁 / 密码尝试次数过多 |
| 410 | 资源不可用（过期/禁用/访问上限） |
| 413 | 文件过大 |
| 503 | 服务暂不可用（如文件系统、分享服务、最近操作记录、版本存储未就绪） |
| 507 | 用户或目录容量配额不足 |
| 500 | 服务器内部错误 |

### Warning 响应头

部分写接口在变更已经对外可见、但后续持久化或清理步骤失败时，仍返回成功状态码，并附带 HTTP `Warning` 响应头。当前使用的 warning 文案包括：

- `199 MnemoNAS "activity log persistence failed"`
- `199 MnemoNAS "auth state persistence incomplete"`
- `199 MnemoNAS "workspace mutation persistence incomplete"`
- `199 MnemoNAS "share persistence incomplete"`
- `199 MnemoNAS "favorites persistence incomplete"`
- `199 MnemoNAS "scrub result persistence incomplete"`
- `199 MnemoNAS "trash restore metadata reconciliation failed"`
- `199 MnemoNAS "delete cleanup incomplete"`
- `199 MnemoNAS "trash delete cleanup incomplete"`

调用方应优先检查 HTTP `Warning` 响应头，而不是只依赖 JSON body。部分 `/api/v1` 写接口会额外返回 `warning: true` 和 `message`，例如 `resource copied with persistence warning`、`version restored with persistence warning`；但最近操作记录补写失败等场景可能只有 `Warning` header，body 仍保持原成功结构。

### MnemoNAS 路径约定

文件、目录、收藏、活动筛选、`home_dir`、目录配额和目录访问规则使用 MnemoNAS 逻辑绝对路径。路径以 `/` 分隔，并归一化为以 `/` 开头的形式；空字符和独立的 `.` / `..` 路径段无效，合法文件名中的连续点号（如 `foo..txt`）不受影响。根路径 `/` 只在端点明确允许时有效。路径作为 URL 参数时应按路径段编码，并保留 `/` 分隔符。

---

## 认证端点

### 用户登录

使用用户名和密码登录。响应会设置 Web UI 使用的 `HttpOnly` 会话 cookie；API 客户端仍可从默认 JSON 响应中读取 bearer token。

```
POST /api/v1/auth/login
```

**请求体**:
```json
{
  "username": "admin",
  "password": "example_password"
}
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "access_token": "eyJ...",
    "refresh_token": "eyJ...",
    "expires_at": "2024-01-15T10:15:00Z",
    "token_type": "Bearer",
    "user": {
      "id": "user-123",
      "username": "admin",
      "email": "admin@example.com",
      "role": "admin",
      "groups": ["family"],
      "home_dir": "/"
    }
  }
}
```

**Web cookie 会话模式**:

```http
X-MnemoNAS-Session-Mode: cookie
```

带该请求头时，响应 `data` 不包含 `access_token` 和 `refresh_token`，但仍会设置 `mnemonas_access` 与 `mnemonas_refresh` cookie。

**失败行为**:
- 同一 `username + 客户端地址` 组合连续登录失败达到限制时，返回 `429 Too Many Requests`，错误码为 `LOGIN_RATE_LIMITED`
- 若已配置提醒通道，登录限流会发送限频的 `login_rate_limited` warning 事件，事件详情只包含 `trigger`、`key_scope`、`username_status` 和 `client_ip_scope` 分类字段，不包含原始用户名、客户端地址、密码或 token；`username_status` 使用 `unknown`、`invalid` 或 `provided` 表示用户名状态，`client_ip_scope` 使用 `public`、`private`、`loopback`、`link_local`、`multicast`、`unspecified` 或 `unknown` 表示客户端地址范围
- `username` 分桶遵循账户名大小写不敏感语义，`handleruser` 与 `HANDLERUSER` 计入同一限流桶
- 客户端地址默认不信任转发头，始终使用直连来源；只有显式设置 `server.trusted_proxy_hops > 0` 且请求直接来自 loopback 或 `server.trusted_proxy_cidrs` 中的代理地址时，才按 `X-Forwarded-For` 从右侧回溯客户端地址。多跳代理部署需要设置为代理总层数

### 刷新令牌

使用 refresh token 获取新的 access token，并轮换刷新令牌。API 客户端可提交 JSON body；浏览器 Web UI 可直接依赖 `mnemonas_refresh` cookie，body 可为空。

```
POST /api/v1/auth/refresh
```

**请求体**:
```json
{
  "refresh_token": "eyJ..."
}
```

当请求使用 refresh cookie，或发送 `X-MnemoNAS-Session-Mode: cookie` 时，成功响应不会在 JSON 中返回 bearer token，只会设置新的 `mnemonas_access` 与 `mnemonas_refresh` cookie。

### 获取当前用户信息

```
GET /api/v1/auth/me
```

**需要认证**: 是

**响应示例**:
```json
{
  "success": true,
  "data": {
    "user": {
      "id": "user-123",
      "username": "admin",
      "email": "admin@example.com",
      "role": "admin",
      "groups": ["family"],
      "home_dir": "/"
    }
  }
}
```

### 退出登录

```
POST /api/v1/auth/logout
```

**需要认证**: 可选；有有效认证时会吊销当前 access token，无有效认证时仍会清理浏览器 cookie

**行为说明**:
- 如果请求携带有效 access token 或主会话 cookie，当前 access token 会被吊销
- `mnemonas_access`、`mnemonas_refresh` 与 `/api/v1` 作用域下的短期 `mnemonas_download_access` cookie 会一并清理
- 即使 access cookie 已过期，该端点也会尽力清理浏览器中的 HttpOnly cookie

**响应示例**:
```json
{
  "success": true,
  "data": null,
  "message": "logged out successfully"
}
```

### 创建下载会话 Cookie

为浏览器下载、预览、缩略图等无法稳定附带 `Authorization` header 的请求创建短期 `HttpOnly` cookie。

```
POST /api/v1/auth/download-session
```

**需要认证**: 是

**认证方式**:

- Web UI 可使用主会话 cookie
- API 客户端可使用 bearer token:

```http
Authorization: Bearer <access_token>
```

**请求体**: 无

**成功行为**:
- 返回 `200 OK`
- 设置名为 `mnemonas_download_access` 的 `HttpOnly`、`SameSite=Strict` cookie，路径为 `/api/v1`
- cookie 过期时间与当前 access token 剩余有效期对齐
- 当请求被后端识别为 HTTPS（直连 TLS，或显式启用 `trusted_proxy_hops > 0` 后由可信代理转发 `X-Forwarded-Proto: https`）时，cookie 带 `Secure`

**响应示例**:
```json
{
  "success": true,
  "data": null
}
```

**失败行为**:
- 该路由复用通用认证 middleware；缺少主会话 cookie 和认证头时返回 `401`，错误码 `MISSING_AUTH_HEADER`
- `Authorization` 头格式非法时返回 `401`，错误码 `INVALID_AUTH_HEADER`
- access token 已过期、已吊销或无效时分别返回 `401`，错误码 `TOKEN_EXPIRED`、`TOKEN_REVOKED`、`INVALID_TOKEN`
- token 对应用户不存在或已被禁用时分别返回 `401/403`，错误码 `USER_NOT_FOUND`、`USER_DISABLED`

### 修改密码

```
POST /api/v1/auth/password
```

**需要认证**: 是

**请求体**:
```json
{
  "old_password": "current_password",
  "new_password": "new_secure_password"
}
```

**响应示例**:
```json
{
  "success": true,
  "data": null,
  "message": "password changed successfully"
}
```

### 用户管理（管理员）

**列出用户**:
```
GET /api/v1/admin/users
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "users": [
      {
        "id": "user-123",
        "username": "admin",
        "email": "admin@example.com",
        "role": "admin",
        "groups": ["family"],
        "disabled": false,
        "home_dir": "/",
        "created_at": "2024-01-01T00:00:00Z",
        "updated_at": "2024-01-15T10:00:00Z",
        "last_login_at": "2024-01-15T10:00:00Z",
        "quota_bytes": 0,
        "used_bytes": 0
      }
    ],
    "total": 1
  }
}
```

**创建用户**:
```
POST /api/v1/admin/users
```

用户名最多 255 个字符，不能包含 `/`、`\`、控制字符或 `.` / `..`；密码长度必须为 8 到 72 字节。用户组名称会归一化为小写，只能包含字母、数字、`.`、`_` 和 `-`。
`home_dir` 可选；省略时默认使用 `/<username>`。提供时会归一化为干净的 MnemoNAS 绝对路径，不能为空，不能包含 `.` / `..` 路径段或控制字符。`user` 和 `guest` 角色不能使用 `/` 作为 `home_dir`；`admin` 可使用 `/` 表示全局命名空间。`quota_bytes` 可选，`0` 表示不限额。

**请求体**:
```json
{
  "username": "alice",
  "password": "example_password",
  "email": "alice@example.com",
  "role": "user",
  "groups": ["family"],
  "home_dir": "/alice",
  "quota_bytes": 10737418240
}
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "user": {
      "id": "user-123",
      "username": "alice",
      "email": "alice@example.com",
      "role": "user",
      "groups": ["family"],
      "disabled": false,
      "home_dir": "/alice",
      "created_at": "2024-01-01T00:00:00Z",
      "updated_at": "2024-01-01T00:00:00Z",
      "quota_bytes": 10737418240,
      "used_bytes": 0
    }
  }
}
```

**更新用户资料、角色、主目录或配额**:
```
PUT /api/v1/admin/users/{id}
```

**请求体**（至少包含一个字段）:
```json
{
  "email": "user@example.com",
  "role": "user",
  "groups": ["family", "editors"],
  "home_dir": "/alice",
  "quota_bytes": 10737418240
}
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "user": {
      "id": "user-123",
      "username": "alice",
      "email": "alice@example.com",
      "role": "user",
      "groups": ["family", "editors"],
      "disabled": false,
      "home_dir": "/alice",
      "created_at": "2024-01-01T00:00:00Z",
      "updated_at": "2024-01-02T00:00:00Z",
      "last_login_at": "2024-01-15T10:00:00Z",
      "quota_bytes": 10737418240,
      "used_bytes": 536870912
    }
  },
  "message": "user updated successfully"
}
```

- `quota_bytes = 0` 表示不限额；大于 0 时，非管理员用户的 Web/API 上传、复制、移动、回收站恢复，以及 `webdav.auth_type = "users"` 下的 WebDAV PUT/COPY/MOVE，在写入目标位于该用户 `home_dir` 内时，会按 `home_dir` 当前逻辑大小执行硬限制。共享目录容量应通过 `storage.directory_quotas` 设置目录级限制。
- 超出配额返回 `507 Insufficient Storage`，错误码为 `QUOTA_EXCEEDED`，`details` 包含 `quota_type`、`quota_path`、`used_bytes`、`quota_bytes`、`required_bytes` 和 `available_bytes`。如果已启用提醒通道，Web/API 的上传、复制、移动和回收站恢复超限会发送 `quota_exceeded` warning 事件，事件详情包含操作类型、访问者范围 `actor_scope`、配额类型和配额字节数；事件详情不包含账号名、主目录、目标路径或配额路径。
- `storage.directory_quotas` 可配置目录级硬限制。命中的 Web/API 上传、复制、移动、回收站恢复、版本恢复，以及 WebDAV PUT/COPY/MOVE 会返回同样的 `QUOTA_EXCEEDED`，并在 `details` 中额外包含 `quota_type="directory"` 和 `quota_path`。Web/API 目录配额拒绝（包括版本恢复）同样会发送 `quota_exceeded` 提醒事件，但不会暴露命中的目录路径。
- `storage.directory_access_rules` 可配置目录读写授权。非管理员访问命中规则时按最具体路径规则判断用户、用户组或角色；写授权同时包含读权限。未命中规则的路径继续按 `home_dir` 边界处理。
- `webdav.auth_type = "basic"` 仍是全局服务凭据兼容模式，不携带应用层 `home_dir` 用户身份。
- 不允许把当前登录管理员自身角色改为非管理员，错误码为 `SELF_ROLE_CHANGE`；角色更新或状态更新不允许移除最后一个启用管理员，错误码为 `LAST_ADMIN`。

**删除用户**:
```
DELETE /api/v1/admin/users/{id}
```

**响应示例**:
```json
{
  "success": true,
  "data": null,
  "message": "user deleted successfully"
}
```

- 用户被删除或禁用后，该用户创建的公开分享不再公开元数据、下载或文件夹列表。相关公开请求返回 `404 Not Found` 和错误码 `SHARE_NOT_FOUND`，避免链接暴露历史 owner 账号是否存在。

**重置用户密码**:
```
POST /api/v1/admin/users/{id}/reset-password
```

**响应示例**:
```json
{
  "success": true,
  "data": null,
  "message": "password reset successfully"
}
```

**让用户现有登录失效**:
```
POST /api/v1/admin/users/{id}/revoke-sessions
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "revoked": true
  },
  "message": "user sessions revoked successfully"
}
```

- 不修改用户密码或启用状态；该用户已有的 Web cookie 会话、access token 和 refresh token 会在后续请求中失效，需要重新登录。

**切换用户状态**:
```
PUT /api/v1/admin/users/{id}/status
```

**请求体**:
```json
{
  "disabled": true
}
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "disabled": true
  },
  "message": "user status updated successfully"
}
```

**约束**:
- 仅管理员可调用
- 不允许禁用当前登录用户自身，错误码为 `SELF_DISABLE`
- 不允许禁用最后一个仍处于启用状态的管理员，错误码为 `LAST_ADMIN`
- 当用户被禁用时，服务端会撤销其现有令牌

---

## 系统端点

### 健康检查

检查系统运行状态。

```
GET /health
HEAD /health
```

`HEAD /health` 返回与 `GET /health` 相同的状态码和响应头，不返回响应体。

**响应示例**:
```json
{
  "status": "healthy",
  "timestamp": "2024-01-15T10:00:00Z",
  "uptime": "24h30m15s",
  "uptime_secs": 88215,
  "version": "<version>",
  "dataplane": {
    "healthy": true,
    "version": "<dataplane-version>",
    "uptime": 86400
  }
}
```

**说明**:
- `uptime` 保留 Go duration 字符串，`uptime_secs` 提供整秒值，便于客户端稳定展示运行时长
- 当已配置的数据面、缩略图缓存、维护历史、最近操作记录、收藏存储或 WebDAV 凭据子系统未能初始化时，`status` 会降级为 `degraded`

### 版本信息

获取系统版本信息。

```
GET /api/v1/version
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "name": "MnemoNAS",
    "version": "<version>",
    "build_time": "2024-01-15T09:30:00Z",
    "go": "go1.25.11"
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### 初始化状态

获取首次启动的初始化状态。

```
GET /api/v1/setup/
```

**响应示例**:
```json
{
  "success": true,
  "is_first_run": true,
  "auth_enabled": true,
  "share_enabled": true,
  "webdav_enabled": true,
  "webdav_auth_type": "basic"
}
```

**说明**:
- 该接口不再返回任何初始密码或用户名
- 首次启动生成的 Web 登录初始管理员密码仅写入 `auth.users_file` 同目录的 `initial-password.txt`；默认路径为 `<storage.root>/.mnemonas/initial-password.txt`，非交互启动日志只提示该文件路径
- 该接口返回 setup 专用平铺 JSON，不使用通用 `data` wrapper

### 确认已查看初始化信息

将首次启动提示标记为已查看，后续 `GET /api/v1/setup/` 会返回 `is_first_run=false`。

```
POST /api/v1/setup/acknowledge
```

**需要认证**:
- 当认证启用时，需要管理员权限
- 当认证未启用时，可匿名调用

**请求体**: 无

**响应示例**:
```json
{
  "success": true,
  "message": "setup acknowledged"
}
```

**失败行为**:
- 认证启用但未登录时返回 `401`
- 认证启用但非管理员时返回 `403`
- 运行时 secrets 不可用时返回 `503`，message 为 `setup acknowledge unavailable`
- 该接口同样返回 setup 专用 JSON，而不是通用 `data` wrapper

### 存储统计

获取存储使用统计。

```
GET /api/v1/stats
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "total_files_available": true,
    "storage_stats_available": true,
    "disk_stats_available": true,
    "directory_quota_stats_available": true,
    "total_files": 0,
    "disk_total": 21474836480,
    "disk_free": 16106127360,
    "disk_available": 16106127360,
    "disk_used": 5368709120,
    "disk_usage_ratio": 0.25,
    "disk_filesystem_type": "zfs",
    "disk_mount_point": "/srv/mnemonas",
    "disk_mount_source": "tank/mnemonas",
    "disk_mount_options": "rw,relatime",
    "disk_native_data_checksum_support": true,
    "total_size": 5368709120,
    "unique_size": 2147483648,
    "dedup_ratio": 0.35,
    "total_chunks": 1234,
    "directory_quotas": [
      {
        "path": "/team",
        "quota_bytes": 10737418240,
        "used_bytes": 5368709120,
        "available_bytes": 5368709120,
        "usage_ratio": 0.5,
        "exists": true,
        "status": "normal"
      }
    ]
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

**说明**:
- `total_files` 统计当前 `files/` workspace 中的文件数量，不包含目录；直接导入到 `files/` 的现有文件也会计入。
- `total_chunks` 统计数据面中的存储对象（chunk）数量，不等同于用户文件数。
- `disk_total` / `disk_used` / `disk_available` 来自托管 `files/` workspace 所在文件系统，可用于显示真实磁盘容量和剩余空间；`disk_free` 是底层文件系统报告的原始空闲空间。
- `disk_filesystem_type` 是托管 `files/` workspace 所在挂载点的文件系统类型；`disk_mount_point`、`disk_mount_source` 和 `disk_mount_options` 来自 Linux mountinfo，可用于确认实际承载 MnemoNAS 的挂载点和设备/数据集；`disk_mount_point` 中的 secret-like 路径片段会被脱敏，`disk_mount_source` 中的 URL userinfo 和 secret-like 参数会被脱敏，`disk_mount_options` 中的凭据、用户名、密码、密钥和令牌类选项值也会被脱敏；`disk_native_data_checksum_support` 表示是否检测到 ZFS/Btrfs 级别的原生数据校验与 scrub 能力。
- `directory_quotas` 返回已配置目录配额的当前逻辑用量，`status` 可能为 `normal`、`warning`、`exceeded` 或 `missing`。
- `total_files_available` 表示文件计数是否可用；`storage_stats_available` 表示数据面统计是否可用；`disk_stats_available` 表示磁盘容量统计是否可用；`directory_quota_stats_available` 表示目录配额用量统计是否可用。
- 当文件计数、数据面统计、磁盘容量统计或目录配额统计暂不可用时，对应字段会被省略，而不是回填误导性的 `0`。

### 诊断信息

获取详细的系统诊断信息。

**需要认证**: 当 `auth.enabled = true` 时需要管理员 JWT；未开启认证时可直接访问

```
GET /api/v1/diagnostics
```

响应包含 `Cache-Control: no-store`，避免浏览器或代理缓存运行态诊断信息。

**响应示例**:
```json
{
  "success": true,
  "data": {
    "timestamp": "2024-01-15T10:00:00Z",
    "uptime": "24h30m15s",
    "uptime_secs": 86400,
    "version": {
      "name": "MnemoNAS",
      "version": "<version>",
      "build_time": "2024-01-15T09:30:00Z",
      "go": "go1.25.11"
    },
    "system": {
      "filesystem_initialized": true,
      "dataplane_connected": true,
      "thumbnail_service_ready": true,
      "maintenance_history_ready": true,
      "backup_manager_ready": true,
      "activity_log_ready": true,
      "favorites_store_ready": true,
      "smb_runtime_ready": false
    },
    "memory": {
      "alloc_mb": 50,
      "total_alloc_mb": 100,
      "sys_mb": 150,
      "num_gc": 10
    },
    "goroutines": 25,
    "filesystem": {
      "trash_stats_available": true,
      "trash_items": 5,
      "trash_size": 52428800,
      "disk_stats_available": true,
      "disk_total": 21474836480,
      "disk_free": 16106127360,
      "disk_available": 16106127360,
      "disk_used": 5368709120,
      "disk_usage_ratio": 0.25,
      "disk_filesystem_type": "zfs",
      "disk_mount_point": "/srv/mnemonas",
      "disk_mount_source": "tank/mnemonas",
      "disk_mount_options": "rw,relatime",
      "disk_native_data_checksum_support": true
    },
    "alerts": {
      "enabled": true,
      "runtime_available": true,
      "check_interval": "30m0s",
      "threshold_pct": 85,
      "critical_pct": 92,
      "min_free_bytes": 21474836480,
      "cooldown_period": "2h0m0s",
      "webhook_configured": true,
      "telegram_configured": true,
      "dingtalk_configured": true,
      "email_configured": true,
      "webhook_method": "POST",
      "last_level": "warning",
      "last_checked_at": "2026-04-29T10:30:00Z",
      "last_used_pct": 87.5,
      "last_free_bytes": 9663676416
    },
    "maintenance": {
      "history_ready": true,
      "scrub_schedule_enabled": true,
      "scrub_schedule_interval": "168h0m0s",
      "scrub_retry_interval": "1h0m0s",
      "scrub_max_retries": 1,
      "last_scrub_status": "completed",
      "last_scrub_at": "2026-05-13T08:30:00Z",
      "scrub_failure_retries": 0
    },
    "disk_health": {
      "enabled": true,
      "runtime_available": true,
      "check_interval": "1h0m0s",
      "probe_timeout": "15s",
      "cooldown_period": "4h0m0s",
      "temperature_warning_c": 50,
      "temperature_critical_c": 60,
      "media_wear_warning_percent": 80,
      "media_wear_critical_percent": 100,
      "device_count": 1,
      "last_status": "ok",
      "last_checked_at": "2026-05-13T08:30:00Z",
      "last_warning_count": 0,
      "last_device_count": 1,
      "last_critical_devices": 0,
      "last_warning_devices": 0,
      "last_unavailable_devices": 0
    },
    "smb": {
      "enabled": true,
      "runtime_available": false,
      "implementation": "planned_sidecar",
      "listen": "127.0.0.1:1445",
      "server_name": "mnemonas",
      "signing_required": true,
      "encryption_required": false,
      "share_count": 1,
      "credentials_ready": true,
      "gateway_configured": true,
      "message": "SMB is configured but the protocol sidecar is not implemented in this build."
    },
    "storage": {
      "total_chunks": 1234,
      "total_size": 5368709120,
      "unique_size": 2147483648,
      "dedup_ratio": 0.35
    },
    "dataplane": {
      "healthy": true,
      "version": "<dataplane-version>",
      "uptime_sec": 86000
    }
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

**说明**:
- `filesystem.trash_stats_available` 表示回收站统计是否可用。
- 当回收站统计暂不可用时，`filesystem.trash_stats_available` 为 `false`，并且 `filesystem.trash_items` 和 `filesystem.trash_size` 会被省略，而不是回填 `0`。
- `filesystem.disk_stats_available` 表示磁盘容量统计是否可用；不可用时 `filesystem.disk_*` 字段会被省略。可用时会同时包含 `filesystem.disk_filesystem_type`、脱敏后的 `filesystem.disk_mount_point`、脱敏后的 `filesystem.disk_mount_source`、脱敏后的 `filesystem.disk_mount_options` 和 `filesystem.disk_native_data_checksum_support`，用于确认实际承载 MnemoNAS 的挂载点、设备/数据集和文件系统校验能力。
- `alerts.runtime_available` 表示当前进程是否挂载了提醒监控；`alerts.webhook_configured` 只表示是否配置了 Webhook，不会暴露 `webhook_url` 或 `webhook_headers`。
- `alerts.telegram_configured` 只表示 Telegram 通知是否具备可用配置，不会暴露 `telegram_bot_token`。
- `alerts.wecom_configured` 只表示企业微信通知是否具备可用配置，不会暴露 `wecom_webhook_url`。
- `alerts.dingtalk_configured` 只表示钉钉通知是否具备可用配置，不会暴露 `dingtalk_webhook_url`。
- `alerts.email_configured` 只表示 SMTP 邮件通知是否具备可用配置，包括启用邮件通知、SMTP 主机、端口、发件人和至少一个非空收件人；不会暴露 `smtp_host`、`smtp_username`、`smtp_password`、`smtp_from` 或 `smtp_to`。
- `alerts.last_*` 来自上一次提醒检查；尚未完成首次检查时会被省略。
- `maintenance` 是维护任务脱敏摘要；其中 `scrub_schedule_*` 反映 `[maintenance.scrub]` 周期计划，`last_scrub_*` 来自最近一次 Scrub 历史，`scrub_failure_retries` 只在最近一次 Scrub 失败时出现。
- `disk_health` 是磁盘健康脱敏摘要；完整设备路径、序列号、温度和 SMART 状态通过管理员维护接口 `GET /api/v1/maintenance/disk-health` 获取。
- `smb` 是 SMB/Samba 预览状态；当前版本不会启动 SMB 监听器，`runtime_available=false` 表示不可挂载，诊断只展示共享数量和配置状态，不暴露凭据文件内容。

### 指标信息

获取 JSON 格式的指标数据。

**需要认证**: 当 `auth.enabled = true` 时需要管理员 JWT；未开启认证时可直接访问

```
GET /api/v1/metrics
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "requests": {
      "total": 100,
      "by_method": {"GET": 90},
      "count_2xx": 95,
      "count_4xx": 3,
      "count_5xx": 2,
      "error_rate": 0.02
    },
    "latency": {
      "avg_ms": 12,
      "max_ms": 200
    },
    "throughput": {
      "bytes_in": 1024,
      "bytes_out": 2048,
      "mb_per_s": 0.5
    },
    "uptime_secs": 3600,
    "slow_requests": [
      {
        "method": "GET",
        "path": "/api/v1/files/",
        "duration_ms": 180,
        "time": "2024-01-15T10:00:00Z"
      }
    ]
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

---

## 文件操作

### 列出文件

列出指定目录下的文件和文件夹。

```
GET /api/v1/files/{path}
```

**路径参数**:
- `path`: 目录路径，默认为根目录 `/`

非管理员请求会按 `home_dir` 和最具体的 `storage.directory_access_rules` 判定目标目录及其直接子项；未获读取授权的子项不会出现在列表响应中。请求根目录 `/` 时，响应只包含该用户的 `home_dir` 和可读共享目录的顶层入口，不返回全局根目录的其它内容。若仅授权嵌套共享目录，已存在的祖先目录可用于只读导航，祖先目录下的新建、移动和复制仍需显式写授权。

列表响应会在当前目录和每个返回项上包含 `capabilities`。`read` 表示该路径可用于列表或导航，`concreteRead` 表示下载、复制源、分享、收藏等精确资源读取操作可用，`write` 表示该路径或容器可执行修改操作。例如，允许在根目录下上传或新建时，根目录可返回 `write: true`，但仍返回 `concreteRead: false`，因为根目录本身不是可下载或可复制资源。

**查询参数**:
- 无

**响应示例**:
```json
{
  "success": true,
  "data": {
    "path": "/documents",
    "capabilities": {
      "read": true,
      "concreteRead": true,
      "write": true
    },
    "files": [
      {
        "name": "report.pdf",
        "path": "/documents/report.pdf",
        "isDir": false,
        "size": 1048576,
        "modTime": "2024-01-15T10:00:00Z",
        "hash": "abc123...",
        "versioned": true,
        "capabilities": {
          "read": true,
          "concreteRead": true,
          "write": true
        }
      },
      {
        "name": "images",
        "path": "/documents/images",
        "isDir": true,
        "size": 0,
        "modTime": "2024-01-14T15:30:00Z",
        "capabilities": {
          "read": true,
          "concreteRead": true,
          "write": true
        }
      }
    ]
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### 上传文件

上传文件到指定路径。

```
POST /api/v1/files/{path}
```

**Content-Type**: `application/octet-stream`

**限制**:
- 单文件最大: 10GB
- 请求超时: 30 分钟（可通过 server 配置调整）

说明：`{path}` 必须指向非根文件路径。根路径或根等价上传目标会返回 `400 Bad Request` 和 `invalid path`。

**响应示例**:
```json
{
  "success": true,
  "data": {
    "path": "/documents/report.pdf"
  },
  "message": "file uploaded successfully",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### 删除文件

删除指定文件或目录（移入回收站）。

```
DELETE /api/v1/files/{path}
```

说明：当删除已经生效、但后续目录持久化或历史对象清理失败时，接口仍返回成功状态码，并附带 `Warning` 响应头；`message` 会区分 persistence warning 与 cleanup warning。

常见 `message` 包括：`file deleted with persistence warning`、`file deleted with cleanup warning`、`file deleted with trash cleanup warning`。

**响应示例**:
```json
{
  "success": true,
  "data": {
    "path": "/documents/report.pdf"
  },
  "message": "file deleted successfully",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### 移动/重命名文件

```
POST /api/v1/files-move
```

**请求体**:
```json
{
  "from": "/documents/old-name.txt",
  "to": "/documents/new-name.txt"
}
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "from": "/documents/old-name.txt",
    "to": "/documents/new-name.txt"
  },
  "message": "file moved successfully",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

说明：目标路径必须不存在，且目标路径不能已有历史版本元数据；目录移动时该检查包含目标路径下的后代路径。发生目标冲突时接口返回 `409 Conflict`，不会执行配额检查或发送配额告警。

说明：当移动或重命名已经完成、但后续工作区持久化失败时，接口仍返回 `200 OK` 并附带 `Warning: 199 MnemoNAS "workspace mutation persistence incomplete"`；响应 `data.warning` 为 `true`，`message` 为 `resource moved with persistence warning`。

### 复制资源

```
POST /api/v1/files-copy
```

说明：该 REST 端点支持复制单个文件或递归复制目录。源路径与目标路径必须不同，目标路径必须不存在，目录不能复制到自身后代路径；如需 `Overwrite: T/F` 语义，请使用 WebDAV `COPY`。

说明：当复制已经完成、仅最后的目录持久化失败时，接口返回 `201 Created` 并附带 `Warning: 199 MnemoNAS "workspace mutation persistence incomplete"`；响应 `message` 为 `resource copied with persistence warning`。

**请求体**:
```json
{
  "from": "/documents/source.txt",
  "to": "/documents/copy.txt"
}
```

目录复制示例：

```json
{
  "from": "/projects/demo",
  "to": "/projects/demo-copy"
}
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "from": "/documents/source.txt",
    "to": "/documents/copy.txt"
  },
  "message": "resource copied successfully",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### 下载文件（认证）

```
GET /api/v1/download/{path}
```

**需要认证**: 是

**查询参数**:
- `download`: 最多指定一次；设置为 `true` 时强制下载（设置 `Content-Disposition`）
- `version`: 最多指定一次；指定版本哈希（64 位 BLAKE3）下载历史版本
- `archive`: 最多指定一次，且设置为 `zip` 时将目标路径打包为 ZIP；可用于目录或单个文件，不能与 `version` 同时使用。目录归档要求调用者对目标目录及其中每个条目具备具体读取权限，只读导航祖先不能被打包下载。归档响应最多包含 10000 个条目和 20 GiB 文件内容。ZIP 附件名使用目标路径末段；根路径使用 `mnemonas-files.zip`，已以 `.zip` 结尾的名称不会重复追加后缀。

**鉴权说明**:
- API 客户端可使用现有认证会话或 `Authorization` 请求头
- 浏览器下载、预览与外部打开使用短期 `HttpOnly`、`SameSite=Strict` `mnemonas_download_access` cookie
- 当前实现不再支持通过 `auth` 查询参数传递访问令牌

**响应**: 返回文件二进制数据；当前文件与历史版本下载均支持 Range 请求。`archive=zip` 返回 `application/zip` 附件，不保证支持 Range。

### 创建目录

```
POST /api/v1/directories/{path}
```

父目录要求：该接口仅在直接父目录已存在时创建目标目录；直接父目录不存在时返回 `409 Conflict`，不会隐式创建中间目录。

持久化警告：当目录已经创建成功、仅最后的工作区持久化失败时，接口返回 `201 Created` 并附带 `Warning: 199 MnemoNAS "workspace mutation persistence incomplete"`；响应 `message` 为 `directory created with persistence warning`。

**响应示例**:
```json
{
  "success": true,
  "data": {
    "path": "/documents/new-folder"
  },
  "message": "directory created successfully",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

---

## 缩略图

### 获取缩略图

获取图片文件的缩略图。

```
GET /api/v1/thumbnails/{path}
```

**需要认证**: 是

**查询参数**:
- `size`: 缩略图尺寸，至多出现一次；可选值: `small` / `s` (150px), `medium` / `m` (300px), `large` / `l` (600px)，省略时默认为 `medium`
- 传入未列出的 `size` 值时返回 `400 Bad Request`
- 源文件超过 100 MiB，或图片尺寸超过 10000x10000 / 5000 万像素时返回 `400 Bad Request`

**鉴权说明**:
- API 客户端可使用现有认证会话或 `Authorization` 请求头
- 浏览器缩略图请求依赖短期 `HttpOnly`、`SameSite=Strict` `mnemonas_download_access` cookie
- 当前实现不再支持通过 `auth` 查询参数传递访问令牌

**支持格式**: JPEG, PNG, GIF, WebP

**响应**: 返回图片二进制数据

缩略图响应是服务端生成的图片，并带 `X-Content-Type-Options: nosniff` 与 sandbox CSP。

---

## 版本历史

### 列出版本

获取文件的历史版本列表。

```
GET /api/v1/versions/{path}
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "path": "/documents/report.pdf",
    "versions": [
      {
        "version": 1,
        "hash": "abc123...",
        "size": 1048576,
        "timestamp": "2024-01-15T10:00:00Z",
        "comment": "(current)"
      },
      {
        "version": 2,
        "hash": "def456...",
        "size": 1024000,
        "timestamp": "2024-01-14T15:00:00Z",
        "comment": ""
      }
    ]
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### 恢复版本

将文件恢复到指定的历史版本。

**需要认证**: 是

**权限要求**: 管理员

```
POST /api/v1/versions/{hash}/restore
```

**请求参数**:
- `path`: 文件路径（必填，最多指定一次）

说明：`path` 必须指向非根文件路径。根路径或根等价值会返回 `400 Bad Request` 和 `invalid path`。

说明：当版本内容已经恢复成功、仅最后的工作区持久化失败时，接口仍返回 `200 OK`，并附带 `Warning: 199 MnemoNAS "workspace mutation persistence incomplete"`；响应 `message` 为 `version restored with persistence warning`。

说明：恢复成功后会写入 `restore` 最近操作记录，`details.restore_source` 为 `version`，`details.hash` 为恢复的版本哈希；若存在工作区持久化告警，还会包含 `details.persistence_warning="true"`。

**响应示例**:
```json
{
  "success": true,
  "data": {
    "path": "/documents/report.txt",
    "restored": "abc123..."
  },
  "message": "version restored successfully",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

---

## 回收站

### 列出回收站

获取回收站中的文件列表。

```
GET /api/v1/trash
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "items": [
      {
        "id": "trash-123",
        "originalPath": "/documents/old-file.txt",
        "deletedAt": "2024-01-15T10:00:00Z",
        "name": "old-file.txt",
        "isDir": false,
        "size": 1024,
        "hadVersions": true
      }
    ],
    "count": 1,
    "totalSize": 52428800,
    "retentionDays": 30,
    "retentionEnabled": true,
    "retentionMaxSize": 10737418240
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### 获取回收站项目详情

获取单个回收站项目的详细信息。

```
GET /api/v1/trash/{id}
```

### 从回收站恢复

将文件从回收站恢复到原位置。可通过至多出现一次的 `path` 查询参数指定自定义恢复目标路径。

```
POST /api/v1/trash/{id}/restore
```

说明：自定义恢复目标必须位于可写路径内、必须是非根路径、直接父目录必须已存在，且目标路径不能已存在；根路径或根等价自定义目标会返回 `400 Bad Request` 和 `invalid path`；直接父目录不存在时返回 `409 Conflict`，不会隐式创建中间目录。若回收站项目包含历史版本，并且原路径已有活动文件，或另一个回收站项目仍引用与来源或目标重叠的版本元数据路径（目录恢复时包括后代路径），接口返回 `409 Conflict`，不会执行配额检查或发送配额告警。

说明：当文件内容已经恢复成功、但 share/favorite 等关联 metadata 恢复失败时，接口仍返回 `200 OK`，并附带 `Warning` 响应头；响应 body 会包含 `warning: true`，`message` 为 `file restored with metadata warning`。

**响应示例**:
```json
{
  "success": true,
  "data": {
    "id": "trash-123",
    "restored": true
  },
  "message": "file restored successfully",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### 永久删除

从回收站中永久删除文件。

```
DELETE /api/v1/trash/{id}
```

说明：当回收站条目已经删除成功、但后续历史对象 cleanup 失败时，接口仍返回 `200 OK`，并附带 `Warning` 响应头；响应 body 会包含 `warning: true`，`message` 为 `item permanently deleted with cleanup warning`。

### 清空回收站

清空整个回收站。

```
DELETE /api/v1/trash
```

说明：当回收站已经清空、但部分历史对象 cleanup 仅部分完成时，接口仍返回 `200 OK`，并附带 `Warning` 响应头；响应 body 会包含 `warning: true`，`message` 为 `trash emptied with cleanup warning`。若前面已有条目删除成功且带 cleanup warning，随后又有其他条目硬失败，则接口仍返回 `200 OK`，同时保留 `partial: true`、`warning: true`，`message` 为 `trash emptied partially with cleanup warning`。

---

## 搜索

### 文件搜索

按文件名搜索文件。

```
GET /api/v1/search?q={query}
```

**查询参数**:
- `q`: 搜索关键词（必填，最长 100 字符，必须且只能出现一次）
- `limit`: 返回数量限制（默认 50，最大 100，至多出现一次）

**响应示例**:
```json
{
  "success": true,
  "data": {
    "query": "report",
    "results": [
      {
        "name": "report.pdf",
        "path": "/documents/report.pdf",
        "isDir": false,
        "size": 1048576,
        "modTime": "2024-01-15T10:00:00Z"
      }
    ],
    "count": 1
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

---

## 分享链接

### 获取分享默认策略

```
GET /api/v1/shares/policy
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "default_expires_in": "168h",
    "default_max_access": 0,
    "policy_rules": [
      {
        "path": "/Family",
        "require_password": true,
        "max_expires_in": "24h",
        "max_access": 20
      }
    ]
  }
}
```

### 创建分享

创建文件或目录的分享链接。

```
POST /api/v1/shares
```

**请求体**:
```json
{
  "path": "/documents/report.pdf",
  "type": "file",
  "password": "optional_password",
  "expires_in": "72h",
  "permission": "read",
  "max_access": 0,
  "description": ""
}
```

**字段说明**:
- `type`: `file` | `folder`
- `password`: 可选分享访问密码；非空时最多 72 字节
- `expires_in`: 可选；省略时使用 `share.default_expires_in`
- `permission`: `read`
- `max_access`: 可选；省略时使用 `share.default_max_access`
- 如果路径命中 `share.policy_rules`，最具体路径规则会生效：`require_password` 会拒绝未设置密码的请求；`max_expires_in` 和 `max_access` 会把超过上限的请求值压到上限
- 响应中的 `url` 为动态生成字段：当 `share.base_url` 已配置时返回 `<base_url>/s/{id}`；未配置时返回相对路径 `/s/{id}`
- `share.base_url` 为空时返回相对路径；非空时必须是完整的 `http` 或 `https` URL，不能包含 userinfo、查询参数或片段，且主机名必须有效
- 认证后的分享响应包含 `risk.level` 和可选 `risk.reasons`，用于标记无密码、不过期、无限访问、覆盖范围过大、长期未访问或即将到期的分享。启用分享创建 30 天后仍未访问会标记为 `unused_enabled`；最近一次访问距今 90 天以上会标记为 `stale_enabled`。
- 当 `[alerts] enabled = true` 且至少配置一个可用提醒通道时，服务端会按小时扫描启用且 72 小时内到期的分享，并发送聚合 `share_expiring_soon` warning 事件；同一进程内同一分享到期时间只提醒一次。事件 `details` 包含 `source = "share"`、分享数量、扫描窗口、最早到期时间、文件/文件夹分享数量、无密码分享数量和不限访问次数分享数量，不包含分享路径、分享 URL、访问密码或分享 ID。

**响应示例**:
```json
{
  "success": true,
  "data": {
    "id": "share-abc123",
    "path": "/documents/report.pdf",
    "type": "file",
    "created_by": "user-123",
    "created_at": "2024-01-15T10:00:00Z",
    "expires_at": "2024-02-15T00:00:00Z",
    "has_password": true,
    "permission": "read",
    "enabled": true,
    "access_count": 0,
    "max_access": 0,
    "last_access": null,
    "description": "",
    "url": "http://localhost:8080/s/share-abc123",
    "risk": {
      "level": "none"
    }
  }
}
```

### 列出分享

```
GET /api/v1/shares
```

**查询参数**:
- `all=true`: 管理员查看所有用户的分享，至多出现一次

**响应示例**:
```json
{
  "success": true,
  "data": [
    {
      "id": "share-abc123",
      "path": "/documents/report.pdf",
      "type": "file",
      "created_by": "user-123",
      "created_at": "2024-01-15T10:00:00Z",
      "expires_at": "2024-02-15T00:00:00Z",
      "has_password": true,
      "permission": "read",
      "enabled": true,
      "access_count": 0,
      "max_access": 0,
      "last_access": null,
      "description": "",
      "url": "http://localhost:8080/s/share-abc123",
      "risk": {
        "level": "medium",
        "reasons": [
          {
            "code": "unlimited_access",
            "level": "medium",
            "message": "未设置访问次数上限"
          }
        ]
      }
    }
  ]
}
```

### 获取分享详情

```
GET /api/v1/shares/{id}
```

**说明**:
- 返回中的 `url` 字段遵循相同规则：优先使用 `share.base_url`，否则返回相对路径 `/s/{id}`

### 更新分享

```
PUT /api/v1/shares/{id}
```

**请求体**:
```json
{
  "enabled": true,
  "password": "optional_password",
  "expires_in": "",
  "permission": "read",
  "max_access": 0,
  "description": ""
}
```

**说明**:
- 更新分享不会改变 `id`；响应中的 `url` 会根据当前运行时 `share.base_url` 重新生成
- `enabled`、`password`、`expires_in`、`permission`、`max_access` 和 `description` 均为可选字段；省略的字段通常保持原值
- `password` 设为空字符串会清除密码；`expires_in` 设为空字符串会清除过期时间；`permission` 目前仅支持 `read`
- 命中 `share.policy_rules` 的分享在更新后仍必须满足路径策略；`require_password` 会拒绝会使分享保持或变为无密码的更新；`max_expires_in` 和 `max_access` 会将显式清空或超过上限的请求值压到策略上限，若请求省略该字段但既有分享缺少对应限制或超过策略上限，也会在本次更新中压到上限

**响应示例**:
```json
{
  "success": true,
  "data": {
    "id": "share-abc123",
    "path": "/documents/report.pdf",
    "type": "file",
    "created_by": "user-123",
    "created_at": "2024-01-15T10:00:00Z",
    "expires_at": null,
    "has_password": false,
    "permission": "read",
    "enabled": true,
    "access_count": 0,
    "max_access": 0,
    "last_access": null,
    "description": "",
    "url": "http://localhost:8080/s/share-abc123",
    "risk": {
      "level": "high",
      "reasons": [
        {
          "code": "no_password",
          "level": "high",
          "message": "未设置密码，拿到链接的人都能访问"
        }
      ]
    }
  }
}
```

### 删除分享

```
DELETE /api/v1/shares/{id}
```

**响应示例**:
```json
{
  "success": true,
  "message": "share deleted successfully"
}
```

### 访问分享链接（公开）

公开分享前端入口使用 `/s/{share_id}`；直接访问兼容接口可使用 `GET /s/{share_id}` 和 `POST /s/{share_id}`。公开分享数据 API 推荐使用 `/api/v1/public/shares/*`，避免与前端路由冲突。

```
GET /api/v1/public/shares/{share_id}
```

如果分享有密码保护，需要 POST 并提供密码：

```
POST /api/v1/public/shares/{share_id}/access
```

请求体：
```json
{ "password": "xxx" }
```

**公开访问响应示例**:
```json
{
  "id": "share-abc123",
  "type": "file",
  "has_password": true,
  "permission": "read"
}
```

**说明**:
- 需要密码且尚未通过验证的分享只返回 `id`、`type`、`has_password` 和 `permission`；不会返回 `description` 或文件/文件夹元数据
- 当分享不需要密码，或已通过密码验证后，会返回 `description` 以及适用的 `file_name` / `file_size` / `folder_items`
- 根目录文件夹分享的 `file_name` 使用稳定显示名 `mnemonas-share`，不会返回 `/` 作为文件名
- 零字节文件会返回 `file_size: 0`；空文件夹会返回 `folder_items: 0`
- 当 `max_access > 0` 且 `access_count` 达到上限时，返回 `410 Gone`，错误码为 `SHARE_ACCESS_LIMIT_REACHED`
- 当当前时间达到或超过 `expires_at` 时，分享视为已过期，返回 `410 Gone`，错误码为 `SHARE_EXPIRED`
- 当分享本身被停用时，返回 `410 Gone`，错误码为 `SHARE_DISABLED`
- 当创建该分享的用户被禁用或删除后，公开访问、下载和文件夹列表都会返回 `404 Not Found`，错误码为 `SHARE_NOT_FOUND`
- `access_count` 在下载与文件夹列表请求时递增；`POST /api/v1/public/shares/{share_id}/access` 与兼容路径 `POST /s/{share_id}` 的密码验证不会计数
- `items?path=` 和 `download/{path}` 中的子路径相对于分享文件夹根目录；文件夹列表的 `path` 查询参数至多出现一次。空字符和独立的 `.` / `..` 路径段无效，合法文件名中的连续点号（如 `foo..txt`）不受影响。非法子路径不会递增 `access_count`
- 文件夹列表响应中的 `path` 与 `items[].path` 均为相对于分享文件夹根目录的规范路径，不以 `/` 开头；根目录响应的 `path` 为空字符串。响应只包含当前目录下对分享 owner 仍可见的直接子项。
- 一旦下载或文件夹列表响应已经开始向客户端写出字节，即使后续流式传输中断，该次访问仍计入 `access_count`
- 底层文件读取器支持 seek 时，公开分享下载会处理 HTTP Range 请求；MnemoNAS 本地存储支持该路径，可用于断点续传和浏览器媒体播放
- 公开下载端点最多指定一次 `archive`，且设置为 `archive=zip` 时，可将分享文件夹根目录、子文件夹或单个文件打包为 ZIP；归档响应返回 `application/zip`，不保证支持 Range，会跳过分享 owner 已不可见的条目，最多包含 10000 个条目和 20 GiB 文件内容。ZIP 附件名使用打包目标名称；分享根目录为 `/` 时使用 `mnemonas-share.zip`，已以 `.zip` 结尾的名称不会重复追加后缀
- 返回 `416 Requested Range Not Satisfiable` 的不可满足 Range 请求不会递增 `access_count`
- 密码验证成功后，服务端通过 `HttpOnly`、`SameSite=Strict` cookie 记录访问状态；后续下载和文件夹列表请求不使用 `password` 查询参数
- 公开分享信息、密码验证响应、文件夹列表响应和公开下载 JSON 错误响应会返回 `Cache-Control: private, no-cache` 和 `Vary: Cookie`，并附带 `X-Content-Type-Options: nosniff` 与 `Referrer-Policy: no-referrer`
- 连续密码错误达到限制时，返回 `429 Too Many Requests`，错误码为 `SHARE_PASSWORD_RATE_LIMITED`
- 口令失败限流默认按 share ID 与客户端地址组合统计；默认不信任转发头，只有显式设置 `server.trusted_proxy_hops > 0` 且请求直接来自 loopback 或 `server.trusted_proxy_cidrs` 中的代理地址时，才按 `X-Forwarded-For` 从右侧回溯客户端地址
- 兼容路径 `GET /s/{share_id}` 与 `POST /s/{share_id}` 保持相同 JSON 行为，适用于非 SPA 或直接脚本调用

**下载文件或分享文件夹 ZIP**:
```
GET /api/v1/public/shares/{share_id}/download
```

**列出分享文件夹内容**:
```
GET /api/v1/public/shares/{share_id}/items?path=subdir
```

**响应示例**:
```json
{
  "path": "subdir",
  "items": [
    {
      "name": "report.pdf",
      "path": "subdir/report.pdf",
      "is_dir": false,
      "size": 1234,
      "mod_time": "2024-01-15T10:00:00Z"
    }
  ]
}
```

**下载分享文件夹内文件或 ZIP**:
```
GET /api/v1/public/shares/{share_id}/download/{path}
```

**说明**:
- `{path}` 需要按路径段进行 URL 编码（保留 `/` 分隔）
- `archive=zip` 可用于分享文件夹根目录、分享文件夹内子文件夹或单个文件；不设置该参数时，目录路径仍按普通文件下载规则返回错误
- 分享启用密码时，需先通过 `POST /api/v1/public/shares/{share_id}/access` 完成密码验证，再使用返回的 `HttpOnly`、`SameSite=Strict` cookie 访问下载和文件夹列表接口
- 兼容路径 `GET /s/{share_id}/items`、`GET /s/{share_id}/download`、`GET /s/{share_id}/download/{path}` 保持相同行为，适用于非 SPA 直接访问

---

## 收藏夹

收藏路径必须规范化为非根绝对路径。空值或根路径会以 `400 Bad Request` 和 `MISSING_PATH` 拒绝；包含独立 `.` / `..` 路径段的值会以 `400 Bad Request` 和 `INVALID_PATH` 拒绝。单路径检查接口的 `path` 查询参数至多出现一次。该校验先于非管理员 `home_dir` 授权执行。

### 列出收藏

```
GET /api/v1/favorites
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "favorites": [
      {
        "path": "/documents/important.pdf",
        "user_id": "user-123",
        "created_at": "2024-01-15T10:00:00Z",
        "note": ""
      }
    ],
    "count": 1
  }
}
```

### 添加收藏

```
POST /api/v1/favorites
```

**请求体**:
```json
{
  "path": "/documents/important.pdf",
  "note": "可选备注"
}
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "path": "/documents/important.pdf",
    "user_id": "user-123",
    "created_at": "2024-01-15T10:00:00Z",
    "note": "可选备注"
  }
}
```

### 检查是否已收藏

```
GET /api/v1/favorites/check?path=/documents/file.pdf
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "path": "/documents/file.pdf",
    "is_favorite": true
  }
}
```

### 批量检查收藏状态

```
POST /api/v1/favorites/check-batch
```

**请求体**:
```json
{
  "paths": ["/file1.txt", "/file2.pdf"]
}
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "favorites": {
      "/file1.txt": true,
      "/file2.pdf": false
    }
  }
}
```

### 取消收藏

```
DELETE /api/v1/favorites/{path}
```

**说明**:
- `{path}` 需要 URL 编码，支持包含 `/` 的完整路径

**响应示例**:
```json
{
  "success": true,
  "data": null,
  "message": "favorite removed successfully"
}
```

### 更新备注

```
PATCH /api/v1/favorites/{path}
```

**说明**:
- `{path}` 需要 URL 编码，支持包含 `/` 的完整路径

**响应示例**:
```json
{
  "success": true,
  "data": null,
  "message": "favorite note updated successfully"
}
```

---

## 最近操作记录

### 列出活动

获取用户操作日志。

**说明**:
- 启用认证时，管理员可查看全量最近操作记录；普通用户仅返回当前账号自己的活动记录，`user` 查询参数不会越权查看其他账号
- 系统级事件也会进入最近操作记录，例如磁盘健康周期检查产生的 `disk_health`
- 手动和周期数据校验会写入 `scrub` 活动；当 Scrub 失败、发现对象问题或结果持久化不完整时，会通过已配置的 Webhook/Telegram/企业微信/钉钉/SMTP 提醒通道发送 `scrub_run` 事件。提醒详情只包含计数、状态、公开错误类型和公开文案，不包含对象 hash 或底层错误文本。
- `share` 和 `unshare` 活动的 `details` 会记录分享类型、权限、是否需要密码、过期时间和访问次数上限等复核摘要；不会写入分享密码、公开 URL 或分享 ID。
- 版本恢复会写入 `restore` 活动，`details.restore_source="version"` 表示来源为版本历史，`details.hash` 记录恢复的版本哈希。
- 未配置最近操作记录时，接口返回空列表
- 若最近操作记录已配置但初始化失败或当前不可用，接口返回 `503 Service Unavailable`

```
GET /api/v1/activity
```

**查询参数**:
下列查询参数均至多出现一次。

- `limit`: 返回数量（默认 50，最大 500）
- `offset`: 分页偏移
- `action`: 按操作类型过滤；当前支持 `upload`、`download`、`delete`、`rename`、`move`、`copy`、`create`、`restore`、`share`、`unshare`、`favorite`、`unfavorite`、`favorite_note_update`、`login`、`logout`、`trash_restore`、`trash_delete`、`trash_empty`、`disk_health`、`scrub`
- `action_group`: 按审计分组过滤；当前支持 `share`（分享/取消分享）和 `risk`（删除、移动、重命名、版本恢复、回收站恢复、分享、取消分享、回收站永久删除、清空回收站）
- `path`: 按路径或目录过滤；匹配该路径本身、其子路径，以及 `from`、`to` 等活动详情中的路径字段
- `user`: 按用户过滤
- `since`: 仅返回该时间点之后或等于该时间点的记录，格式为 RFC3339 时间戳
- `until`: 仅返回该时间点之前或等于该时间点的记录，格式为 RFC3339 时间戳

`action` 和 `action_group` 可组合使用，此时结果取交集。`path` 会按 MnemoNAS 绝对路径规则规范化，包含独立 `.` / `..` 路径段时返回 `400 Bad Request`。`action`、`action_group` 无效、时间参数格式无效，或 `since` 晚于 `until` 时，接口返回 `400 Bad Request`。

普通用户使用 `path` 过滤时，`/` 表示当前账号可见范围；若目标路径不在当前账号可见范围内，接口返回空列表，不会通过已隐藏的活动详情暴露不可见路径是否存在。

**响应示例**:
```json
{
  "success": true,
  "data": {
    "items": [
      {
        "id": "act-123",
        "timestamp": "2024-01-15T10:00:00Z",
        "action": "upload",
        "path": "/documents/file.pdf",
        "user": "admin",
        "ip": "127.0.0.1",
        "details": {
          "to": "/documents/new.pdf"
        }
      }
    ],
    "total": 100,
    "limit": 50,
    "offset": 0
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### 活动统计

**说明**:
- 启用认证时，管理员可查看全局统计；普通用户仅返回当前账号自己的活动统计
- 统计接口支持与列表接口相同的 `action`、`action_group`、`path`、`user`、`since`、`until` 查询参数；启用筛选时，`total`、`today`、`by_action`、`by_user` 和 `risk_summary` 均基于筛选后的记录计算
- `risk_summary` 汇总高风险操作，包括删除、移动、重命名、分享、取消分享、回收站永久删除和清空回收站；`max_10m` 表示任意 10 分钟窗口内的最高集中次数，`max_10m_started_at` 和 `max_10m_ended_at` 标识该集中窗口，便于复核对应时段的记录
- 未配置最近操作记录时，接口返回零统计
- 若最近操作记录已配置但初始化失败或当前不可用，接口返回 `503 Service Unavailable`

```
GET /api/v1/activity/stats
```

**查询参数**:
下列查询参数均至多出现一次。

- `action`: 按操作类型过滤；取值同列表接口的 `action`
- `action_group`: 按审计分组过滤；当前支持 `share` 和 `risk`
- `path`: 按路径或目录过滤；匹配该路径本身、其子路径，以及 `from`、`to` 等活动详情中的路径字段
- `user`: 按用户过滤
- `since`: 仅统计该时间点之后或等于该时间点的记录，格式为 RFC3339 时间戳
- `until`: 仅统计该时间点之前或等于该时间点的记录，格式为 RFC3339 时间戳

`action`、`action_group`、`path`、`user`、`since` 和 `until` 的错误处理与列表接口一致。

普通用户使用 `/` 作为 `path` 过滤条件时，统计结果限定在当前账号可见范围内；使用不可见路径作为 `path` 过滤条件时，统计结果返回零值，不会通过活动详情中的路径字段计入隐藏记录。

**响应示例**:
```json
{
  "success": true,
  "data": {
    "total": 100,
    "today": 10,
    "by_action": {
      "upload": 50,
      "delete": 10
    },
    "by_user": {
      "admin": 60
    },
    "risk_summary": {
      "total": 14,
      "today": 3,
      "max_10m": 5,
      "max_10m_started_at": "2024-01-15T09:00:00Z",
      "max_10m_ended_at": "2024-01-15T09:08:00Z"
    }
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### 列出活动复核记录（管理员）

获取已持久化的活动复核处置记录。

```
GET /api/v1/activity/reviews
```

**查询参数**:

- `limit`: 返回数量（默认 20，最大 100）
- `offset`: 分页偏移
- `reviewer`: 按复核人过滤
- `activity_entry_id`: 仅返回关联指定最近操作记录 ID 的复核记录
- `disposition_status`: 按处置状态过滤，允许值为 `documented`、`confirmed`、`restored`、`disabled`、`needs_follow_up`
- `since`: 仅返回该时间点之后或等于该时间点的复核记录，格式为 RFC3339 时间戳
- `until`: 仅返回该时间点之前或等于该时间点的复核记录，格式为 RFC3339 时间戳

`since` 晚于 `until`、时间格式无效、`activity_entry_id` 非规范值或 `disposition_status` 非允许值时，接口返回 `400 Bad Request`。

**响应示例**:
```json
{
  "success": true,
  "data": {
    "items": [
      {
        "id": "review-123",
        "reviewed_at": "2024-01-15T10:05:00Z",
        "reviewer": "admin",
        "note": "已确认误删文件已从回收站恢复",
        "scope_label": "集中窗口",
        "filter_summary": "分组 高风险变更",
        "disposition_status": "restored",
        "action_counts": {
          "delete": 2,
          "move": 1
        },
        "review_count": 3,
        "total_count": 5,
        "path_count": 2,
        "user_count": 1,
        "path_samples": ["/docs/deleted.txt", "/docs/moved.txt"],
        "user_samples": ["admin"],
        "activity_entry_ids": ["act-delete-1", "act-move-1"]
      }
    ],
    "total": 1,
    "limit": 20,
    "offset": 0
  },
  "timestamp": "2024-01-15T10:05:00Z"
}
```

### 创建活动复核记录（管理员）

记录一次活动复核处置结论。服务端会使用当前认证账号作为 `reviewer`，并写入 `reviewed_at`。

```
POST /api/v1/activity/reviews
```

**请求体**:
```json
{
  "note": "已确认误删文件已从回收站恢复",
  "scope_label": "当前页",
  "filter_summary": "分组 高风险变更",
  "disposition_status": "restored",
  "action_counts": {
    "delete": 2,
    "move": 1
  },
  "review_count": 3,
  "total_count": 5,
  "path_count": 2,
  "user_count": 1,
  "path_samples": ["/docs/deleted.txt", "/docs/moved.txt"],
  "user_samples": ["admin"],
  "activity_entry_ids": ["act-delete-1", "act-move-1"]
}
```

**说明**:
- `note`、`scope_label` 和 `activity_entry_ids` 必填；`review_count` 必须大于 0，且 `total_count` 不能小于 `review_count`
- `disposition_status` 可选，缺省为 `documented`；允许值为 `documented`、`confirmed`、`restored`、`disabled`、`needs_follow_up`
- `action_counts` 可选；键必须是已知最近操作类型，值必须为正整数，且计数总和必须等于 `review_count`
- `path_samples` 和 `user_samples` 可选，最多各 10 个；路径会按最近操作路径规则规范化，重复样例会被拒绝
- 若最近操作记录未配置、初始化失败或当前不可用，接口返回 `503 Service Unavailable`

### 更新活动复核记录处置状态（管理员）

更新一条已持久化活动复核记录的当前处置状态，并可同步更新处置备注。服务端会使用当前认证账号覆盖 `reviewer`，并将 `reviewed_at` 更新为本次状态回写时间；未传 `note` 时保留原备注，样例、计数和关联活动记录保持不变。

```
PATCH /api/v1/activity/reviews/{id}
```

**请求体**:
```json
{
  "disposition_status": "disabled",
  "note": "分享链接已关闭，访问入口已核对"
}
```

**说明**:
- `disposition_status` 必填；允许值为 `documented`、`confirmed`、`restored`、`disabled`、`needs_follow_up`
- `note` 可选；传入时必须为非空文本，服务端会按活动复核备注规则去除首尾空白并校验长度
- `{id}` 非规范或 `disposition_status` 非允许值时返回 `400 Bad Request`
- 复核记录不存在时返回 `404 Not Found`
- 若最近操作记录未配置、初始化失败或当前不可用，接口返回 `503 Service Unavailable`

### 清空最近操作记录（管理员）

```
DELETE /api/v1/activity
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "message": "Activity log cleared"
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

**说明**:
- 若最近操作记录已配置但初始化失败或当前不可用，接口返回 `503 Service Unavailable`，而不是伪装成清理成功

---

## 设置管理

### 获取设置

```
GET /api/v1/settings
```

**需要管理员权限**

**响应示例**:
```json
{
  "success": true,
  "data": {
    "server": {
      "host": "0.0.0.0",
      "port": 8080,
      "read_timeout": "30s",
      "write_timeout": "60s",
      "idle_timeout": "120s",
      "trusted_proxy_hops": 1,
      "trusted_proxy_cidrs": ["10.0.0.0/8"],
      "tls": {
        "enabled": false,
        "cert_file": "",
        "key_file": "",
        "auto_generate": true,
        "cert_dir": "/srv/mnemonas/.mnemonas/certs"
      }
    },
    "storage": {
      "root": "/srv/mnemonas",
      "directory_quotas": [
        { "path": "/team", "quota_bytes": 1099511627776 }
      ],
      "directory_access_rules": [
        { "path": "/team", "read_groups": ["family"], "write_groups": ["editors"] }
      ]
    },
    "retention": {
      "max_versions": 50,
      "max_age": "2160h",
      "min_free_space": 10737418240,
      "gc_interval": "24h"
    },
    "versioning": {
      "auto_versioned_extensions": [
        ".md", ".txt", ".org", ".rst", ".tex",
        ".go", ".rs", ".py", ".ts", ".js", ".tsx", ".jsx",
        ".c", ".cpp", ".h", ".java", ".kt", ".swift",
        ".toml", ".yaml", ".yml", ".json", ".xml",
        ".sh", ".bash", ".zsh", ".fish"
      ],
      "auto_versioned_filenames": [
        "Makefile", "Dockerfile", "Vagrantfile",
        "LICENSE", "README", "CHANGELOG",
        ".gitignore", ".dockerignore", ".editorconfig"
      ],
      "max_versioned_size": 104857600
    },
    "webdav": {
      "enabled": true,
      "runtime_enabled": true,
      "prefix": "/dav",
      "read_only": false,
      "auth_type": "basic",
      "username": "admin"
    },
    "share": {
      "enabled": false,
      "base_url": "",
      "default_expires_in": "168h",
      "default_max_access": 0,
      "policy_rules": []
    },
    "favorites": {
      "enabled": true,
      "runtime_available": true
    },
    "trash": {
      "enabled": true,
      "retention_days": 30,
      "max_size": 10737418240
    },
    "alerts": {
      "enabled": false,
      "check_interval": "1h",
      "threshold_pct": 90,
      "critical_pct": 95,
      "min_free_bytes": 10737418240,
      "cooldown_period": "4h",
      "webhook_url": "",
      "webhook_url_configured": false,
      "webhook_method": "POST",
      "webhook_headers": [],
      "webhook_headers_configured": false,
      "telegram_enabled": false,
      "telegram_bot_token_configured": false,
      "telegram_chat_id": "",
      "wecom_enabled": false,
      "wecom_webhook_url": "",
      "wecom_webhook_url_configured": false,
      "dingtalk_enabled": false,
      "dingtalk_webhook_url": "",
      "dingtalk_webhook_url_configured": false
    },
    "maintenance": {
      "scrub": {
        "enabled": false,
        "schedule_interval": "168h0m0s",
        "retry_interval": "1h0m0s",
        "max_retries": 1
      }
    },
    "dataplane": {
      "grpc_address": "127.0.0.1:9090",
      "timeout": "30s",
      "max_retries": 3
    },
    "cdc": {
      "min_chunk_size": 262144,
      "avg_chunk_size": 1048576,
      "max_chunk_size": 4194304
    }
  }
}
```

- `webdav.runtime_enabled` 表示当前进程中的 WebDAV 服务是否处于运行状态；当 `webdav.enabled = true` 但自动生成凭据不可用时，该值为 `false`
- `favorites.runtime_available` 表示当前进程中的收藏接口是否可用；当 `favorites.enabled = true` 但收藏存储初始化失败或运行态依赖缺失时，该值为 `false`

### 检查有效目录权限

```
POST /api/v1/settings/access-check
```

**需要管理员权限**

请求体：

```json
{
  "username": "alice",
  "path": "/team/report.pdf"
}
```

响应会同时返回 `read` 和 `write` 判定。每个判定包含 `allowed`、`source`、可选 `message`，以及由目录授权决定时的 `matched_rule`。`source` 可能是 `admin`、`home_dir`、`directory_access_rule`、`invalid_home_dir`、`user_disabled`、`user_not_found` 或 `auth_disabled`。当只读导航祖先由嵌套目录授权放行时，`matched_rule` 指向对应的后代规则。

**响应示例**:
```json
{
  "success": true,
  "data": {
    "username": "alice",
    "user_id": "u1",
    "role": "user",
    "groups": ["family"],
    "home_dir": "/users/alice",
    "path": "/team/report.pdf",
    "read": {
      "mode": "read",
      "allowed": true,
      "source": "directory_access_rule",
      "message": "directory access rule grants read",
      "matched_rule": {
        "path": "/team",
        "read_groups": ["family"]
      }
    },
    "write": {
      "mode": "write",
      "allowed": false,
      "source": "directory_access_rule",
      "message": "directory access rule does not grant write",
      "matched_rule": {
        "path": "/team",
        "read_groups": ["family"]
      }
    }
  }
}
```

### 目录权限用户矩阵

```
POST /api/v1/settings/access-report
```

**需要管理员权限**

请求体：

```json
{
  "path": "/team/report.pdf"
}
```

响应会对所有用户生成同一路径下的读写判定，并返回 `summary` 汇总：

```json
{
  "success": true,
  "data": {
    "path": "/team/report.pdf",
    "summary": {
      "users": 2,
      "read_allowed": 1,
      "read_denied": 1,
      "write_allowed": 1,
      "write_denied": 1,
      "related_shares": 1,
      "active_related_shares": 1,
      "password_protected_shares": 1
    },
    "users": [
      {
        "username": "alice",
        "user_id": "u1",
        "role": "user",
        "groups": ["family"],
        "home_dir": "/users/alice",
        "path": "/team/report.pdf",
        "read": { "mode": "read", "allowed": true, "source": "directory_access_rule" },
        "write": { "mode": "write", "allowed": true, "source": "directory_access_rule" }
      }
    ],
    "shares": [
      {
        "id": "share-id",
        "path": "/team",
        "type": "folder",
        "created_by": "u1",
        "relation": "covers_path",
        "enabled": true,
        "active": true,
        "has_password": true,
        "access_count": 0,
        "max_access": 0,
        "url": "/s/share-id"
      }
    ]
  }
}
```

`shares[].relation` 说明分享与检查路径的关系：`exact` 表示直接分享该路径，`covers_path` 表示父级分享会覆盖该路径，`inside_path` 表示被检查目录下存在子级分享。

### 预览目录权限变更

```
POST /api/v1/settings/access-preview
```

**需要管理员权限**

请求体：

```json
{
  "path": "/team/report.pdf",
  "directory_access_rules": [
    {
      "path": "/team",
      "read_groups": ["family"],
      "write_groups": ["parents"]
    }
  ]
}
```

该接口不保存配置，只用请求里的 `directory_access_rules` 临时生成同样的用户矩阵和相关分享影响，并在响应里返回 `preview: true`。嵌套目录授权同样会把祖先路径评估为只读导航入口。适合在保存共享目录权限前检查家庭成员或小团队成员是否会被误拒绝、误开放。

**响应示例**:
```json
{
  "success": true,
  "data": {
    "path": "/team/report.pdf",
    "preview": true,
    "summary": {
      "users": 1,
      "read_allowed": 1,
      "read_denied": 0,
      "write_allowed": 0,
      "write_denied": 1,
      "related_shares": 0,
      "active_related_shares": 0,
      "password_protected_shares": 0
    },
    "users": [
      {
        "username": "alice",
        "user_id": "u1",
        "role": "user",
        "groups": ["family"],
        "home_dir": "/users/alice",
        "path": "/team/report.pdf",
        "read": { "mode": "read", "allowed": true, "source": "directory_access_rule" },
        "write": { "mode": "write", "allowed": false, "source": "directory_access_rule" }
      }
    ]
  }
}
```

### 公网访问安全自检

```
GET /api/v1/settings/security-check
```

**需要管理员权限**

该接口返回当前运行态中与公网暴露直接相关的配置检查结果。它用于 Web UI 的“公网访问安全自检”，也可供自动化部署工具读取。

**响应示例**:
```json
{
  "success": true,
  "data": {
    "status": "warning",
    "generated_at": "2026-05-09T10:00:00Z",
    "checks": [
      {
        "id": "https_request",
        "status": "warning",
        "title": "当前访问不是 HTTPS",
        "message": "公网访问前应通过内置 TLS 或受信反向代理提供 HTTPS。",
        "details": {
          "direct_tls": false,
          "forwarded_proto": "",
          "trusted_forwarded_source": true
        }
      },
      {
        "id": "server_listen",
        "status": "warning",
        "title": "Web 服务监听范围偏宽",
        "message": "Web 服务当前监听非本机地址；公网部署时建议只监听 127.0.0.1 或 ::1，并由反向代理对外暴露。",
        "details": {
          "host": "0.0.0.0",
          "port": 8080
        }
      }
    ],
    "request": {
      "scheme": "http",
      "direct_tls": false,
      "host": "localhost:8080",
      "remote_ip": "127.0.0.1",
      "trusted_forwarded_source": true,
      "forwarded_proto": ""
    },
    "config": {
      "auth_enabled": true,
      "server_host": "0.0.0.0",
      "server_port": 8080,
      "tls_enabled": false,
      "trusted_proxy_hops": 0,
      "trusted_proxy_cidrs": [],
      "dataplane_grpc_addr": "127.0.0.1:9090",
      "dataplane_http_addr": "127.0.0.1:9091",
      "webdav_enabled": true,
      "webdav_prefix": "/dav",
      "webdav_auth_type": "basic",
      "smb_enabled": false,
      "allow_unsafe_no_auth": false,
      "share_enabled": false,
      "share_default_expires_in": "168h",
      "share_default_max_access": 0
    }
  }
}
```

**字段说明**:
- `data.status` 是整体状态，取值为 `pass`、`warning`、`block`
- `checks[].status` 是单项状态，取值同上；存在任一 `block` 时整体为 `block`，否则存在任一 `warning` 时整体为 `warning`
- `checks[].id` 当前包含 `auth_enabled`、`session_token_ttl`、`login_rate_limit`、`browser_session_boundary`、`public_share_boundary`、`unsafe_no_auth_override`、`users_file_access`、`https_request`、`public_http_exposure`、`trusted_proxy_or_tls`、`forwarded_proto_trust`、`server_listen`、`admin_accounts`、`dataplane_listen`、`dataplane_http_listen`、`webdav_prefix`、`webdav_auth`、`smb_preview`、`share_base_url`、`share_default_policy`、`initial_password_file`
- `session_token_ttl` 检查 Web UI 访问令牌和刷新令牌有效期；公网部署建议 `auth.access_token_ttl <= 1h`、`auth.refresh_token_ttl <= 720h`，超过建议值会标记为 `warning`，响应只返回 TTL 文本、秒数和是否过长，不返回任何令牌内容
- `login_rate_limit` 检查 Web UI 连续失败登录限速；启用认证后，默认按用户名和客户端 IP 统计失败次数，达到阈值后短期锁定并发送 `login_rate_limited` 提醒事件；响应返回阈值、统计窗口、锁定时长、提醒冷却时间和 key 范围，不返回用户名、密码或 token
- `browser_session_boundary` 检查当前浏览器访问路径是否会让 Web UI 会话 cookie 和下载 cookie 带 `Secure` 标记，并确认浏览器写请求同源元数据校验处于启用状态；未启用 Web 登录认证或当前请求未被识别为 HTTPS 时会标记为 `warning`，响应只返回 cookie 属性、请求 scheme、代理信任和同源校验布尔值，不返回任何 token 或 cookie 内容
- `public_share_boundary` 在分享功能启用时检查公开分享访问 cookie、口令失败限速与公开分享 JSON 响应缓存边界；如果 HttpOnly、SameSite、cookie 路径范围、失败限速、`Cache-Control: private`、`Cache-Control: no-cache`、`Vary: Cookie`、`nosniff` 或 `Referrer-Policy: no-referrer` 边界异常，会标记为 `block`；只有这些边界满足要求但当前请求未被识别为 HTTPS 时，才会将受密码保护分享 cookie 缺少 `Secure` 标记为 `warning`；响应只返回 cookie 属性和路径范围状态、公开分享 JSON 缓存与 referrer 边界状态、`Vary: Cookie`、`nosniff` 和口令失败限速参数，不返回分享口令、cookie 值或分享 ID
- `users_file_access` 检查当前运行时使用的用户文件；路径缺失、目录不可读、目录是符号链接、文件不可读、文件是符号链接或非普通文件会标记为 `block`，目录或文件仍允许组或其他用户访问会标记为 `warning`；响应会在 `details.path`、`details.dir`、`details.file_mode`、`details.dir_mode`、`details.file_kind`、`details.dir_kind`、`details.file_group_or_other_access` 和 `details.dir_group_or_other_access` 中返回可观测的路径、权限和类型信息
- `initial_password_file` 检查 `auth.users_file` 同目录的 `initial-password.txt`；如果无法确定检查路径，或发现普通文件残留、符号链接、非普通文件，均为 `block`，响应会在 `details.path` 返回检查路径；路径为空时表示无法从用户文件路径推导初始密码文件位置；可观测时会通过 `details.mode` 和 `details.path_kind` 返回权限和路径类型，其中符号链接和非普通文件会在 `details.path_kind` 中分别返回 `symlink` 或 `not_regular`
- `webdav_prefix` 在 WebDAV 启用时检查 WebDAV URL 前缀；空前缀、站点根路径、包含无效路径字符，或位于 `/api`、`/s`、`/health` 保留路由下的前缀会标记为 `block`，并返回 `details.prefix_risk` 和 `details.normalized_prefix`
- `webdav_auth` 在 WebDAV 启用时检查认证方式；`auth_type = "none"` 会在非本机监听时标记为 `block`。全局 Basic Auth 显式配置常见占位密码或少于 16 字符的密码会标记为需更换的 `warning`，响应只包含 `password_source` 和 `password_risk` 类型，不返回密码值。使用自动生成密码时，运行态密码不可用会标记为 `block` 并返回 `generated_password_available=false`，自动生成密码偏弱会标记为 `warning`
- `share_base_url` 在分享功能启用时检查公网分享链接基础 URL；HTTP、非 443 的 HTTPS 端口、包含 userinfo、查询参数、片段或主机名格式无效的 URL 会标记为 `block`，空值、其他域名或以 `/s` 分享路由结尾的基础路径会保留为需人工复核的告警
- `share_default_policy` 在分享功能启用时检查新分享默认有效期和默认访问次数；默认不过期、长于 `720h` 或默认访问次数不限制会标记为 `warning`，负值会标记为 `block`，响应只返回默认有效期、默认访问次数和策略规则数量
- `forwarded_proto_trust` 检查 `X-Forwarded-Proto` 与受信代理配置：未配置 `trusted_proxy_hops` 时出现该 header 会标记为 `warning`，header 来自非受信来源会标记为 `block`，来自受信来源但值不是 `https` 会标记为 `warning`
- `request` 描述当前请求如何被服务端识别，例如是否 HTTPS、是否来自受信转发源、`X-Forwarded-Proto` 是否被采纳
- `config` 描述自检使用的关键运行配置

**边界说明**:
- 该接口只检查服务端能可靠读取到的运行态和当前请求语义
- 它不能直接检查云厂商安全组、公网路由、真实外部端口暴露或证书链可用性
- 公网部署仍应在服务器上运行 `sudo mnemonas-doctor --public-domain <domain>`，并在云控制台确认只开放 `80/443`

### 更新设置

```
PUT /api/v1/settings
```

**说明**:
- `storage.root` 路径为只读配置，需修改配置文件并重启服务
- `storage.directory_quotas` 和 `storage.directory_access_rules` 可通过设置 API 热更新，并同步到 Web/API 与 WebDAV 运行态

**请求体**:
```json
{
  "server": {
    "host": "0.0.0.0",
    "port": 8080,
    "read_timeout": "30s",
    "write_timeout": "60s",
    "idle_timeout": "120s",
    "trusted_proxy_hops": 1,
    "trusted_proxy_cidrs": ["10.0.0.0/8"],
    "tls": {
      "enabled": true,
      "auto_generate": true,
      "cert_dir": "/etc/mnemonas/tls"
    }
  },
  "storage": {
    "directory_quotas": [
      { "path": "/team", "quota_bytes": 1099511627776 }
    ],
    "directory_access_rules": [
      { "path": "/team", "read_groups": ["family"], "write_groups": ["editors"] }
    ]
  },
  "auth": {
    "access_token_ttl": "1h",
    "refresh_token_ttl": "720h"
  },
  "retention": {
    "max_versions": 10,
    "max_age": "720h",
    "min_free_space": 10737418240
  },
  "versioning": {
    "auto_versioned_extensions": [".md", ".txt", ".rs"],
    "auto_versioned_filenames": ["README", "Dockerfile", "Cargo.toml"],
    "max_versioned_size": 268435456
  },
  "trash": {
    "enabled": true,
    "retention_days": 14,
    "max_size": 2147483648
  },
  "share": {
    "enabled": true,
    "base_url": "https://share.example.com",
    "default_expires_in": "168h",
    "default_max_access": 0,
    "policy_rules": [
      {
        "path": "/Family",
        "require_password": true,
        "max_expires_in": "24h",
        "max_access": 20
      }
    ]
  },
  "favorites": {
    "enabled": false
  },
  "alerts": {
    "enabled": true,
    "check_interval": "30m",
    "threshold_pct": 85,
    "critical_pct": 92,
    "min_free_bytes": 21474836480,
    "cooldown_period": "2h",
    "webhook_url": "https://hooks.example.com/storage",
    "webhook_method": "POST",
    "webhook_headers": ["Authorization: Bearer token", "X-MnemoNAS: alerts"],
    "telegram_enabled": true,
    "telegram_bot_token": "123456:ABC...",
    "telegram_chat_id": "-1001234567890",
    "wecom_enabled": true,
    "wecom_webhook_url": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...",
    "dingtalk_enabled": true,
    "dingtalk_webhook_url": "https://oapi.dingtalk.com/robot/send?access_token=...",
    "email_enabled": true,
    "smtp_host": "smtp.example.com",
    "smtp_port": 587,
    "smtp_username": "alerts@example.com",
    "smtp_password": "smtp-secret",
    "smtp_from": "MnemoNAS <alerts@example.com>",
    "smtp_to": ["admin@example.com"]
  },
  "disk_health": {
    "enabled": true,
    "check_interval": "1h",
    "probe_timeout": "15s",
    "cooldown_period": "4h",
    "command": "smartctl",
    "temperature_warning_c": 50,
    "temperature_critical_c": 60,
    "media_wear_warning_percent": 80,
    "media_wear_critical_percent": 100,
    "devices": [
      {
        "name": "data-ssd",
        "path": "/dev/disk/by-id/nvme-Samsung_SSD_1234",
        "type": "nvme",
        "serial": "S6..."
      }
    ]
  },
  "maintenance": {
    "scrub": {
      "enabled": true,
      "schedule_interval": "168h",
      "retry_interval": "1h",
      "max_retries": 1
    }
  },
  "dataplane": {
    "grpc_address": "127.0.0.1:9090",
    "timeout": "30s",
    "max_retries": 3
  },
  "cdc": {
    "min_chunk_size": 262144,
    "avg_chunk_size": 1048576,
    "max_chunk_size": 4194304
  },
  "webdav": {
    "enabled": true,
    "read_only": false
  }
}
```

**响应示例**:
```json
{
  "success": true,
  "message": "settings updated"
}
```

设置请求中的 `storage.directory_quotas[].path`、`storage.directory_access_rules[].path` 和 `share.policy_rules[].path` 使用同一套 MnemoNAS 逻辑路径规则：路径必须以 `/` 开头，不能包含 Windows/UNC 语法、反斜杠、查询或片段字符、控制字符，或独立的 `.` / `..` 路径段。设置 API 会规范化首尾空白、重复斜杠与末尾斜杠；包含 `.` 或 `..` 的路径不会被折叠，会被拒绝。

**失败行为**:
- 成功响应的 `message` 在仅包含热更新字段、或请求中携带但值未变化的重启类字段时为 `settings updated`；当 `server.host`、`server.port`、`server.read_timeout`、`server.write_timeout`、`server.idle_timeout`、`server.tls.*` 或 `cdc.*` 的值实际变化时为 `settings updated, some changes may require restart`
- `trash` 支持更新 `enabled`、`retention_days`、`max_size`；保存后会立即影响运行中的回收站策略
- `auth` 支持更新 `access_token_ttl`、`refresh_token_ttl`；两者必须是正的 `time.ParseDuration` 字符串，保存后会立即影响新签发的 Web UI 访问令牌和刷新令牌，已签发 token 的过期时间不变
- `retention` 支持更新 `max_versions`、`max_age`、`min_free_space`、`gc_interval`；保存后会立即更新运行中的版本保留阈值与周期清理任务，`gc_interval` 设为 `0` 表示禁用周期清理
- `server` 支持更新 `host`、`port`、`read_timeout`、`write_timeout`、`idle_timeout`、`trusted_proxy_hops`、`trusted_proxy_cidrs`；监听地址和超时保存后需重启服务才能影响运行中的 HTTP 监听器，`trusted_proxy_hops` 与 `trusted_proxy_cidrs` 会立即影响请求来源和 HTTPS 转发语义识别
- `server.tls` 支持更新 `enabled`、`cert_file`、`key_file`、`auto_generate`、`cert_dir`；保存后需重启服务才能切换 HTTPS 监听
- `cdc` 支持更新 `min_chunk_size`、`avg_chunk_size`、`max_chunk_size`；必须满足 `65536 <= min < avg < max <= 67108864`。Docker 和 systemd 启动入口会在 dataplane 重启时读取这些字节值，新对象写入才会使用新分块参数
- `versioning` 支持更新 `auto_versioned_extensions`、`auto_versioned_filenames`、`max_versioned_size`；保存后会立即更新运行中的自动版本策略
- `share` 支持更新 `enabled`、`base_url`、`default_expires_in`、`default_max_access`、`policy_rules`；`enabled` 会立即影响公开分享访问和新分享创建，`base_url` 会立即影响后续新生成的分享链接，非空时必须是完整的 `http` 或 `https` URL，不能包含 userinfo、查询参数或片段，且主机名必须有效；默认有效期和默认访问次数只影响之后创建的分享；`policy_rules` 每项 `path` 遵循上述逻辑路径规则，并至少设置 `require_password`、`max_expires_in` 或 `max_access` 中的一项
- Web 设置页会基于当前编辑内容显示分享策略覆盖摘要，集中展示默认有效期、默认访问次数、路径策略数量、强制密码路径和宽松策略关注项；该摘要仅用于保存前复核，实际限制仍以设置 API 保存后的服务端策略为准
- `favorites` 支持更新 `enabled`；保存后会立即影响收藏接口的可用性
- `storage.directory_quotas` 每项 `path` 遵循上述逻辑路径规则，`quota_bytes` 必须是正整数
- `storage.directory_access_rules` 每项 `path` 遵循上述逻辑路径规则，并至少包含一个 `read_users`、`write_users`、`read_groups`、`write_groups`、`read_roles` 或 `write_roles` 授权；角色只能是 `admin`、`user`、`guest`
- 保存成功后，如果 `storage.directory_access_rules` 或分享策略字段 `share.enabled`、`share.default_expires_in`、`share.default_max_access`、`share.policy_rules` 发生实际变化，服务端会向提醒运行态提交 `settings_policy_changed` warning 事件；事件 `details` 包含 `source = "settings"`、`changed_sections`、目录授权和分享策略的变更布尔值及规则数量，不包含规则路径、`share.base_url`、提醒通道密钥或用户成员明细；规范化后等价的提交不会产生该事件；事件发送失败只写入服务端日志，不会导致设置保存失败
- `alerts` 支持更新 `enabled`、`check_interval`、`threshold_pct`、`critical_pct`、`min_free_bytes`、`cooldown_period`、`webhook_url`、`webhook_method`、`webhook_headers`、`telegram_enabled`、`telegram_bot_token`、`telegram_chat_id`、`wecom_enabled`、`wecom_webhook_url`、`dingtalk_enabled`、`dingtalk_webhook_url`、`email_enabled`、`smtp_host`、`smtp_port`、`smtp_username`、`smtp_password`、`smtp_from`、`smtp_to`；保存后会立即更新运行中的提醒监控
- `disk_health` 支持更新 `enabled`、`check_interval`、`probe_timeout`、`cooldown_period`、`command`、温度阈值、介质磨损阈值和 `devices`；保存后会立即更新运行中的磁盘健康监控
- `maintenance.scrub` 支持更新 `enabled`、`schedule_interval`、`retry_interval`、`max_retries`；保存后会立即更新运行中的周期 Scrub 调度，关闭时会取消后台调度
- `dataplane` 支持更新 `grpc_address`、`timeout`、`max_retries`；保存后会立即替换运行中的数据面 client，并用于后续按需重连和连接重试策略
- 请求中的 `trash.retention_days` 不能为负数，`trash.max_size` 必须是正整数
- 请求中的 `versioning.max_versioned_size` 必须是正整数，`versioning.auto_versioned_extensions` 每项必须以 `.` 开头，`versioning.auto_versioned_filenames` 不能包含空项
- `webdav` 支持更新 `enabled`、`prefix`、`read_only`、`auth_type`、`username`、`password`；`auth_type` 支持 `users`、`basic`、`none`，空值会归一化为 `basic`；`prefix` 会归一化为以 `/` 开头的 URL 路径，不能包含反斜杠、`?`、`#` 或控制字符，启用时不能覆盖 `/`、`/api`、`/s`、`/health`；省略 `webdav.password` 会保留现有 WebDAV 密码，提交空字符串会把 Basic Auth 切回 `secrets.json` 中的自动生成密码；保存后会立即切换运行中的 WebDAV 前缀、鉴权方式和只读状态
- `webdav.auth_type = "users"` 使用 MnemoNAS 用户账号登录，普通用户的 WebDAV 根目录映射到自己的 `home_dir`，并会列出可导航到已授权共享目录的顶层入口；嵌套授权产生的祖先入口仅用于只读导航，写操作仍需命中写授权；guest 只读，用户配额约束写入 `home_dir` 的 PUT/COPY/MOVE；`basic` 模式下 `webdav.username` 不得复用现有非 admin 用户名，因为它是全局服务凭据
- 请求中的 `server.host` 必须为空、`*`、合法主机名、IPv4 或 IPv6 字面量，不能包含端口、空白或控制字符；端口必须通过 `server.port` 设置
- 请求中的 `server.trusted_proxy_hops` 不能为负数；默认值 `0` 表示不信任转发头。`server.trusted_proxy_cidrs` 每项必须是 IP 地址或 CIDR；loopback 来源不需要写入该列表
- 请求中的 `server.read_timeout`、`server.write_timeout`、`server.idle_timeout` 必须是正的 `time.ParseDuration` 字符串，例如 `30s`、`2m`
- 请求中的 `auth.access_token_ttl` 和 `auth.refresh_token_ttl` 必须是正的 `time.ParseDuration` 字符串；公网部署建议访问令牌不超过 `1h`，刷新令牌不超过 `720h`
- 请求中的 `retention.max_age`、`retention.gc_interval` 必须是 `time.ParseDuration` 可解析的字符串，例如 `720h`、`24h`、`0`
- 请求中的 `alerts.check_interval`、`alerts.cooldown_period` 必须是正的 `time.ParseDuration` 字符串
- 请求中的 `alerts.webhook_url` 为空时禁用 Webhook 发送，非空时必须是完整的 `http` 或 `https` URL
- 请求中的 `alerts.webhook_method` 仅支持 `GET` 或 `POST`；`POST` 发送 JSON body，`GET` 将提醒字段编码到 URL query。`storage_alert` 外发时保留容量指标和 `path_scope = "configured_storage_root"`，但 `path` 固定为 `<omitted>`，文本通道也不包含原始存储根路径。`alerts.webhook_headers` 每项必须是 `"Key: Value"` 格式，Header 名称必须是合法 HTTP token，不能重复（大小写不敏感），值不能包含换行或控制字符
- `GET /api/v1/settings` 不返回 Webhook URL 或 Header 值；响应中的 `alerts.webhook_url` 和 `alerts.webhook_headers` 使用 `<redacted>` 占位符表示已配置值，`alerts.webhook_url_configured` 与 `alerts.webhook_headers_configured` 表示是否存在对应配置。`PUT /api/v1/settings` 可提交真实 Webhook URL/Header 值来更新配置；如果提交原样的 `<redacted>` 占位值，服务端会保留对应的既有值。
- 省略 `alerts.telegram_bot_token` 或 `alerts.smtp_password` 会保留既有密钥；提交空字符串会清除对应密钥。`alerts.telegram_enabled = true` 时清除 `telegram_bot_token` 会因缺少必需 Token 被拒绝。
- `alerts.telegram_enabled = true` 时必须提供 `telegram_bot_token` 和 `telegram_chat_id`；`telegram_bot_token` 不能包含空白、`/`、`?` 或 `#`，诊断和设置读取响应不会明文返回该 Token
- `alerts.wecom_enabled = true` 时必须提供 `wecom_webhook_url`；非空 `wecom_webhook_url` 必须是完整的 `http` 或 `https` URL。`GET /api/v1/settings` 使用 `<redacted>` 和 `wecom_webhook_url_configured` 表示已保存的企业微信 Webhook，`PUT /api/v1/settings` 提交同样的 `<redacted>` 占位值会保留既有 URL。
- `alerts.dingtalk_enabled = true` 时必须提供 `dingtalk_webhook_url`；非空 `dingtalk_webhook_url` 必须是完整的 `http` 或 `https` URL。`GET /api/v1/settings` 使用 `<redacted>` 和 `dingtalk_webhook_url_configured` 表示已保存的钉钉 Webhook，`PUT /api/v1/settings` 提交同样的 `<redacted>` 占位值会保留既有 URL。
- `alerts.email_enabled = true` 时必须提供 `smtp_host`、`smtp_from` 和至少一个 `smtp_to`，`smtp_port` 必须在 1-65535 范围内，发件人和收件人必须是合法邮件地址
- 请求中的 `disk_health.check_interval`、`disk_health.probe_timeout`、`disk_health.cooldown_period` 必须是正的 `time.ParseDuration` 字符串；`disk_health.command` 必须是单个可执行文件名或绝对路径；`disk_health.media_wear_critical_percent` 不能低于 `disk_health.media_wear_warning_percent`；每个 `devices[].path` 必须是绝对路径，推荐 `/dev/disk/by-id/...`
- 请求中的 `maintenance.scrub.schedule_interval`、`maintenance.scrub.retry_interval` 必须是正的 `time.ParseDuration` 字符串；`maintenance.scrub.max_retries` 必须是 `0` 或正整数
- 请求中的 `dataplane.grpc_address` 必须是合法 `host:port` 地址，端口范围 1-65535，且不能包含空白或控制字符；`dataplane.timeout` 必须是正的 `time.ParseDuration` 字符串，`dataplane.max_retries` 必须是 `0` 或正整数
- 配置校验失败时返回 `400 Bad Request` 和稳定错误消息 `invalid configuration`
- 非法设置请求不会修改进程内当前生效配置

### 发送测试提醒

```
POST /api/v1/settings/alerts/test
```

**需要管理员权限**

该接口会通过当前已保存的提醒通道发送一次 `alert_test` warning 事件，用于验证 Webhook、Telegram、企业微信、钉钉或 SMTP 邮件链路。它要求 `[alerts] enabled = true`、至少存在一个已配置的通知通道，并且当前进程已挂载提醒运行态；企业微信和钉钉通道只有在启用对应通知且 Webhook URL 非空时才计入通道列表，SMTP 邮件通道只有在启用邮件通知、SMTP 主机、端口、发件人和至少一个非空收件人均存在时才计入通道列表。测试事件的 `details` 只包含 `trigger = "manual_test"`、`source = "settings"` 和通道名称列表，不包含 Webhook、Telegram、企业微信、钉钉或 SMTP 密钥。

**响应示例**:
```json
{
  "success": true,
  "data": {
    "event_type": "alert_test",
    "channels": ["webhook", "email"]
  },
  "message": "test alert sent"
}
```

提醒未启用或没有可用通道时返回 `409 Conflict`；提醒运行态不可用时返回 `503 Service Unavailable`；通道发送失败时返回通用 `500` 错误，不返回通道密钥。

### 获取 WebDAV 凭据

```
GET /api/v1/settings/webdav-credentials
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "enabled": true,
    "url": "/dav/",
    "auth_type": "basic",
    "username": "admin",
    "password": "***"
  }
}
```

**说明**:
- 认证启用时，该端点仅对 `admin` 角色开放
- 该端点返回当前运行中的 WebDAV 服务凭据，并与最近一次成功应用到运行态的 WebDAV 配置保持一致
- `password` 仅在使用自动生成密码时可返回

---

## 维护操作

**说明**:
- 启用认证时，维护操作仅管理员可用。

### 获取磁盘健康

立即运行一次磁盘健康探测并返回完整设备状态。该接口依赖 `[disk_health]` 配置和 `smartctl`；即使后台周期检查关闭，运行态监控未初始化时也会返回 `503`。

```
GET /api/v1/maintenance/disk-health
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "enabled": true,
    "status": "warning",
    "checked_at": "2026-05-13T08:30:00Z",
    "message": "one or more disks need attention",
    "warnings": ["data-ssd: temperature 52 C reached warning threshold 50 C"],
    "devices": [
      {
        "name": "data-ssd",
        "path": "/dev/disk/by-id/nvme-Samsung_SSD_1234",
        "type": "nvme",
        "expected_serial": "S6...",
        "serial": "S6...",
        "model": "Samsung SSD",
        "present": true,
        "smart_available": true,
        "smart_passed": true,
        "temperature_c": 52,
        "power_on_hours": 1234,
        "wear_percent_used": 12,
        "available_spare_percent": 95,
        "available_spare_threshold_percent": 10,
        "media_errors": 0,
        "nvme_critical_warning": 0,
        "status": "warning",
        "message": "temperature 52 C reached warning threshold 50 C",
        "temperature_warning_c": 50,
        "temperature_critical_c": 60
      }
    ]
  }
}
```

**状态说明**:

- `status` 可能为 `disabled`、`ok`、`warning`、`critical` 或 `unavailable`。
- 设备路径不存在、SMART 自检失败、配置了序列号但实际序列号不匹配会返回 `critical`。
- 温度达到提醒阈值返回 `warning`，达到严重阈值返回 `critical`。
- NVMe critical warning、可用备用容量低于阈值、介质寿命已用百分比达到阈值或介质错误计数非零会影响设备状态。
- `smartctl` 不可用、无 JSON 输出或 JSON 无法解析会返回 `unavailable`。
- 后台周期检查发现 `warning`、`critical` 或 `unavailable` 时，会写入 `disk_health` 最近操作记录，路径为 `/system/disk-health`，用户为 `system`。
- 当 `[alerts] enabled = true` 且配置了 Webhook、Telegram、企业微信、钉钉或 SMTP 邮件时，后台周期检查会对 `warning`、`critical` 和 `unavailable` 发送 `disk_health` 提醒事件。最近操作记录中的设备摘要使用配置的 `name`；提醒事件详情只包含聚合计数，不包含设备名、完整设备路径、序列号或 warning 文本。完整设备路径和 SMART 明细仅通过管理员维护接口返回。

### 获取数据校验结果

获取最近一次数据完整性校验（Scrub）的结果。

```
GET /api/v1/maintenance/scrub
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "has_result": true,
    "status": "completed",
    "id": "scrub-20240115-100000",
    "start_time": "2024-01-15T10:00:00Z",
    "end_time": "2024-01-15T10:00:05Z",
    "duration_ms": 5000,
    "total_objects": 1000,
    "valid_objects": 998,
    "corrupted_objects": 1,
    "missing_objects": 1,
    "total_size": 5368709120,
    "errors": [
      {
        "hash": "abc123...",
        "error_type": "corrupted",
        "message": "object failed integrity verification"
      }
    ],
    "error_message": ""
  }
}
```

### 执行数据校验

执行数据完整性校验，并在当前请求内返回本次校验结果摘要。

```
POST /api/v1/maintenance/scrub
```

**请求体** (可选):
```json
{
  "hashes": ["abc123...", "def456..."]
}
```

如果不提供 `hashes`，将校验所有对象。

**说明**:
- 此接口为同步执行，不会先返回 `running` 再异步完成
- 成功响应直接返回本次校验结果摘要；最近一次完整结果可通过 `GET /api/v1/maintenance/scrub` 再次读取
- `errors[].message` 返回稳定的公开文案，底层 IO/路径/校验细节只写入服务端日志
- 当校验已完成、但结果持久化失败时，接口仍返回 `200 OK`，并附带 `Warning` 响应头；响应 body 会包含 `warning: true`，`message` 为 `scrub completed with persistence warning`
- 若 `[maintenance.scrub] enabled = true`，服务会以系统身份按 `schedule_interval` 自动执行完整 Scrub；失败后按 `retry_interval` 最多重试 `max_retries` 次。周期任务会写入维护历史、最近操作记录和提醒事件，与手动 Scrub 使用同一套结果格式；提醒详情不包含对象 hash 或底层错误文本。

**响应示例**:
```json
{
  "success": true,
  "data": {
    "total_objects": 1000,
    "valid_objects": 998,
    "corrupted_objects": 1,
    "missing_objects": 1,
    "total_size": 5368709120,
    "duration_ms": 5000,
    "errors": [
      {
        "hash": "abc123...",
        "error_type": "corrupted",
        "message": "object failed integrity verification"
      }
    ]
  },
  "timestamp": "2024-01-15T10:00:05Z"
}
```

### 列出存储对象

列出 CAS 存储中的所有对象。

```
GET /api/v1/maintenance/objects
```

**查询参数**:
- `limit`: 返回数量限制（默认 1000，最大 1000，至多出现一次）
- `cursor`: 游标（从上一次返回的 `next_cursor` 开始，必须是 64 位十六进制对象 hash，至多出现一次）

**说明**:
- 当前响应仅返回 `hash` 和 `size`
- 服务端内部会读取对象时间戳用于 GC grace period 判断，但该字段不通过此接口暴露

**响应示例**:
```json
{
  "success": true,
  "data": {
    "objects": [
      {
        "hash": "abc123...",
        "size": 1048576
      }
    ],
    "count": 1,
    "next_cursor": "abc123..."
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### 执行垃圾回收

启动垃圾回收，清理无引用的数据块。

```
POST /api/v1/maintenance/gc
```

**查询参数**:
- `dry_run`: 是否仅计算不删除，至多出现一次（默认 `true`，只有显式传入 `false` 才会执行删除）
- `grace_period_hours`: 跳过最近创建对象的小时数，至多出现一次（默认 24）

**说明**:
- GC 会跳过 grace period 内的新对象，避免删除正在上传或刚写入的数据块
- 当对象缺少可用时间戳时，也会按保守策略计入 `skipped_by_grace`，不会直接进入删除集合
- dataplane 会优先使用对象创建时间，无法获取时回退到修改时间
- `deleted_count` 表示实际删除成功的对象数量
- 当 `dry_run=false` 且存在部分失败时，响应会额外返回 `failed_count` 和 `delete_failures`

**响应示例**:
```json
{
  "success": true,
  "data": {
    "dry_run": true,
    "grace_period_hours": 24,
    "total_objects": 1000,
    "referenced": 900,
    "unreferenced": 100,
    "unreferenced_size": 104857600,
    "skipped_by_grace": 5,
    "deleted_count": 0
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

执行删除时，如果存在部分失败：

```json
{
  "success": true,
  "data": {
    "dry_run": false,
    "grace_period_hours": 0,
    "total_objects": 1000,
    "referenced": 900,
    "unreferenced": 100,
    "unreferenced_size": 104857600,
    "skipped_by_grace": 0,
    "deleted_count": 99,
    "failed_count": 1,
    "delete_failures": [
      {
        "hash": "abc123...",
        "message": "failed to delete chunk"
      }
    ]
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### 备份任务

列出 `[[backup.jobs]]` 中配置的备份任务和最近状态。

```
GET /api/v1/maintenance/backups
```

**响应示例**:
```json
{
  "success": true,
  "data": [
    {
      "id": "external-disk",
      "name": "外置硬盘备份",
      "type": "local",
      "source": "/srv/mnemonas",
      "destination": "/mnt/backup-drive/mnemonas",
      "disabled": false,
      "schedule_interval": "24h0m0s",
      "schedule_window_start": "02:00",
      "schedule_window_end": "05:00",
      "next_run_at": "2026-05-10T02:03:04Z",
      "stale_after": "72h0m0s",
      "restore_drill_stale_after": "720h0m0s",
      "max_snapshots": 7,
      "max_age": "720h0m0s",
      "retention_status": "ok",
      "retention_message": "本地快照自动清理已配置",
      "health_status": "ok",
      "health_message": "last successful backup completed recently",
      "restore_drill_status": "ok",
      "restore_drill_message": "恢复演练仍在预期窗口内",
      "last_restore_drill_reminder_at": "2026-05-08T03:00:00Z",
      "include_config": true,
      "verify_after_backup": true,
      "exclude": [".mnemonas/thumbnails"],
      "running": false,
      "last_run": {
        "id": "20260509T020304.000000000Z",
        "job_id": "external-disk",
        "status": "completed",
        "started_at": "2026-05-09T02:03:04Z",
        "duration_ms": 1200,
        "file_count": 42,
        "total_bytes": 1048576,
        "trigger": "scheduled",
        "warning": false,
        "warnings": [],
        "pruned_snapshots": 1
      },
      "last_retention_check": {
        "id": "20260509T021000.000000000Z",
        "job_id": "external-disk",
        "status": "completed",
        "started_at": "2026-05-09T02:10:00Z",
        "finished_at": "2026-05-09T02:10:01Z",
        "duration_ms": 1000,
        "target": "/mnt/backup-drive/mnemonas",
        "snapshot_count": 7,
        "warning": false
      },
      "last_restore_verify": {
        "id": "20260509T041500.000000000Z",
        "job_id": "external-disk",
        "status": "completed",
        "started_at": "2026-05-09T04:15:00Z",
        "finished_at": "2026-05-09T04:15:01Z",
        "duration_ms": 1000,
        "snapshot_path": "/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z",
        "manifest_path": "/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json",
        "target_path": "/restore/mnemonas",
        "file_count": 42,
        "verified_bytes": 1048576,
        "looks_like_storage_root": true,
        "warnings": []
      },
      "last_matching_restore_verify": {
        "id": "20260509T041500.000000000Z",
        "job_id": "external-disk",
        "status": "completed",
        "started_at": "2026-05-09T04:15:00Z",
        "finished_at": "2026-05-09T04:15:01Z",
        "duration_ms": 1000,
        "snapshot_path": "/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z",
        "manifest_path": "/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json",
        "target_path": "/restore/mnemonas",
        "file_count": 42,
        "verified_bytes": 1048576,
        "looks_like_storage_root": true,
        "warnings": []
      },
      "restore_report_findings": [
        "未发现阻塞项；仍需在切换前按恢复清单人工复核。"
      ]
    }
  ]
}
```

获取单个任务：

```
GET /api/v1/maintenance/backups/{id}
```

立即执行备份：

```
POST /api/v1/maintenance/backups/{id}/run
```

请求体可为空，也可传 `{}`。备份任务支持三种类型：

- `type = "local"`：复制本地目录到 `destination/<job-id>/snapshots/<run-id>/`，写入 `manifest.json`，并在 `verify_after_backup = true` 时校验快照文件大小和 SHA-256。
- `type = "restic"`：调用 `command` 指定的 restic 可执行文件，执行 `restic -r <repository> --password-file <password_file> backup <source>`；`verify_after_backup = true` 时执行 `restic check`。
- `type = "rclone"`：调用 `command` 指定的 rclone 可执行文件，执行 `rclone sync <source> <remote>`；`verify_after_backup = true` 时执行 `rclone check --one-way`。

`restic` 和 `rclone` 不通过 shell 拼接命令；`command` 只能是可执行名或绝对路径，`extra_args` 会作为 argv 追加到备份命令，恢复命令不会复用备份专用参数。`local` 的 `destination` 必须位于 `storage.root` 之外，且已存在的路径组件不能是符号链接；本地恢复预览、恢复和恢复演练在读取快照 manifest 或创建演练产物前也会重新检查该目标路径。备份运行前会拒绝 `source` 树中的符号链接；`rclone` 恢复演练也会在执行远端校验前应用同一检查。`password_file`、`config_file` 必须是 `source` 与 `storage.root` 之外的普通文件。API 任务视图、运行结果、恢复/预览结果、恢复报告和批量恢复结果会对 `repository`、`remote`、`destination`、`target_path`、`snapshot_path`、`manifest_path`、`config_path` 等展示字段中的 userinfo、token、密码、secret 和 key 参数做 `<redacted>` 脱敏；API 可见的备份 `error_message`、`warnings` 和预检详情中出现的同类模式也会脱敏。备份提醒事件不会外发来源、目标、恢复目录、快照/manifest 路径或原始错误/警告文本，只保留状态、触发原因、计数、时间、失败分类和是否省略位置/错误详情的摘要字段。实际 restic/rclone 命令仍使用配置中的原始值。调用方在串联 `restore-preview`、`restore` 和 `restore-verify` 时，应保留并复用原请求中的 `target_path`；响应中的脱敏 `target_path` 仅适合作为展示值。

任务可配置 `disabled`、`schedule_interval`、`schedule_window_start`、`schedule_window_end`、`stale_after`、`restore_drill_stale_after`、`max_snapshots`、`max_age` 和 `retention_policy`。`schedule_interval` 大于 0 时服务内置调度器会自动按间隔执行；设置 `schedule_window_start`/`schedule_window_end` 后，自动任务只会在服务器本地时间窗口内启动，手动执行不受影响。`local` 成功备份后会按 `max_snapshots` 和 `max_age` 清理旧快照，并在响应的 `pruned_snapshots` 中返回清理数量。成功备份后会自动运行一次保留策略检测，也可调用 `POST /retention-check` 手动检查。`restic` 检测执行 `restic snapshots --json --tag mnemonas --tag job:<id>`，`rclone` 检测执行 `rclone lsjson <remote> --recursive --files-only`；检测结果写入 `last_retention_check`，并影响 `retention_status`/`retention_message`。`restic` 和 `rclone` 的远端保留策略仍由外部工具管理；配置 `retention_policy` 会把该外部策略标记为已确认，否则会返回 `warning` 提醒确认。`restore_drill_stale_after` 留空或未配置时默认 30 天，任务视图会通过 `restore_drill_status` 和 `restore_drill_message` 提示尚未演练、演练失败或演练过期；配置提醒通道后，缺失或过期恢复演练会发送限频的 `backup_restore_drill` warning 事件，`trigger` 为 `restore_drill_reminder`，并持久化 `last_restore_drill_reminder_at`。`health_status` 只表示备份运行健康，可能为 `ok`、`manual`、`running`、`due`、`stale`、`failed` 或 `disabled`。任务视图会返回 `last_restore_drill`、最近恢复演练历史 `restore_drill_history`、恢复演练统计 `restore_drill_stats`、`last_restore`、`last_restore_verify`、最近恢复匹配的只读校验 `last_matching_restore_verify` 与最近恢复历史 `restore_history`；恢复演练历史和显式恢复历史默认都保留最近 20 条，失败演练和失败恢复也会记录错误信息。失败的恢复演练会返回稳定的 `failure_category`，当前可能值包括 `no_snapshot`、`unsupported_job_type`、`unsafe_path`、`integrity_check`、`external_command`、`cancelled`、`io` 和 `unknown`，并会透传到提醒事件。`restore_drill_stats` 汇总最近保留窗口内的总次数、成功次数、失败次数、成功率、连续成功/失败次数、最近成功/失败时间、最近失败原因和失败类型，便于回顾恢复能力、恢复目标、恢复预检、只读校验结果、切换/回滚清单、状态、文件数和字节数。

备份、恢复、恢复演练、只读校验和保留检测开始执行时会先写入 `running` 状态。服务启动时，状态文件中遗留的 `running` 记录会被标记为失败并写回状态文件，避免重启后长期显示并未实际运行的任务。

当 `[alerts] enabled = true` 且配置了 Webhook、Telegram、企业微信、钉钉或 SMTP 邮件时，备份失败、显式恢复失败或带警告、恢复后只读校验失败或带警告、恢复演练失败、恢复演练缺失/过期提醒、保留策略检测失败或备份完成但带警告会发送提醒事件。事件 `type` 为 `backup_run`、`backup_restore`、`backup_restore_verify`、`backup_restore_drill` 或 `backup_retention_check`，`level` 为 `warning` 或 `critical`；`message` 使用固定公共摘要，不包含任务名称、路径或原始错误文本。`details` 只包含有值的摘要字段，可包括任务 ID、运行 ID、任务类型、触发原因、状态、时间、文件/字节/快照计数、警告数量、错误信息是否存在、失败分类，以及是否省略位置详情；不包含任务名称、来源、备份目标、恢复目标路径、快照路径、manifest 路径、原始 warning 或原始错误文本。

手动检查快照保留策略和远端可见内容：

```
POST /api/v1/maintenance/backups/{id}/retention-check
```

请求体可为空，也可传 `{}`。响应的 `data` 为 `RetentionCheckResult`，包含 `snapshot_count`、`file_count`、`total_bytes`、`oldest_snapshot_at`、`latest_snapshot_at`、`warning` 和 `warnings` 等字段。检测失败会返回 `500`，并把失败结果放入 `details`。

对最近一次成功快照执行恢复演练，或对远端备份执行一致性校验：

```
POST /api/v1/maintenance/backups/{id}/restore-drill
```

**请求体** (可选):
```json
{
  "keep_artifact": false
}
```

`local` 默认会把快照恢复到临时目录、校验每个文件后删除临时目录；`keep_artifact = true` 会保留临时恢复目录并在响应中返回 `restored_path`。`restic` 当前执行 `restic check`，`rclone` 当前执行 `rclone check --one-way`；这用于验证仓库或远端一致性。需要真正恢复 restic/rclone 数据时使用 `/restore`。

预览显式恢复，不写入目标目录，也不写入恢复历史：

```
POST /api/v1/maintenance/backups/{id}/restore-preview
```

**请求体**:
```json
{
  "target_path": "/mnt/restore/mnemonas",
  "include_config": true
}
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "id": "20260509T035900.000000000Z",
    "job_id": "external-disk",
    "status": "completed",
    "started_at": "2026-05-09T03:59:00Z",
    "finished_at": "2026-05-09T03:59:01Z",
    "duration_ms": 1000,
    "source": "/srv/mnemonas",
    "destination": "/mnt/backup-drive/mnemonas",
    "target_path": "/mnt/restore/mnemonas",
    "file_count": 42,
    "total_bytes": 1048576,
    "config_available": true,
    "config_included": true,
    "sample_paths": ["docs/note.txt", ".mnemonas-restore/config.toml"],
    "preflight_checks": [
      {
        "id": "target_scope",
        "status": "passed",
        "title": "目标路径隔离",
        "detail": "目标目录位于当前数据目录、备份来源和本地备份目标之外。"
      },
      {
        "id": "target_state",
        "status": "passed",
        "title": "目标目录状态",
        "detail": "目标目录尚不存在；恢复会先写入临时目录，再安装到该路径。"
      },
      {
        "id": "backup_content",
        "status": "passed",
        "title": "备份内容",
        "detail": "预览发现 42 个文件，预计恢复 1 MB。"
      },
      {
        "id": "target_capacity",
        "status": "passed",
        "title": "目标容量",
        "detail": "目标文件系统可用空间 100 GB，预计恢复 1 MB。"
      },
      {
        "id": "config_restore",
        "status": "passed",
        "title": "配置文件",
        "detail": "本地快照包含配置文件，并将恢复到 .mnemonas-restore/config.toml。"
      }
    ],
    "warnings": [],
    "cutover_checklist": ["先对恢复目录执行只读校验"],
    "rollback_checklist": ["切换失败时停止服务，将配置指回原 storage.root"]
  }
}
```

`restore-preview` 会复用显式恢复的目标路径安全校验，并返回 `preflight_checks`、`warnings`、`cutover_checklist` 和 `rollback_checklist`。预检会覆盖目标路径隔离、`target_state` 目标目录状态、备份内容、目标文件系统容量和配置文件处理。`target_state` 会区分目标目录尚不存在与目标目录已存在且为空两种允许状态；目标不存在时容量预检读取父目录可用空间，目标目录已存在且为空时读取该目录所在文件系统可用空间。`preflight_checks[].status` 可能为 `passed`、`warning` 或 `failed`；`status = "warning"` 表示恢复可以继续但需要人工确认，`status = "failed"` 会阻止维护页开始恢复，并会在 `/restore` 写入前被服务端预检拒绝。`warnings` 会汇总 warning 与 failed 预检详情，供客户端在预览卡片、批量预览和恢复历史中展示。`local` 从最近一次成功快照的 `manifest.json` 生成文件数、字节数和样例路径；`restic` 执行 `restic ls latest --json --tag mnemonas --tag job:<id> --path <source>`；`rclone` 执行 `rclone lsjson <remote> --recursive --files-only`。维护页要求成功预览与当前目标目录、配置选项一致，并且没有 `status = "failed"` 的预检项后才允许开始恢复。

批量预览多个恢复目标：

```
POST /api/v1/maintenance/backups/batch-restore-preview
```

**请求体**:
```json
{
  "items": [
    {
      "job_id": "external-disk",
      "target_path": "/mnt/restore/mnemonas-a",
      "include_config": true
    },
    {
      "job_id": "rclone-cloud",
      "target_path": "/mnt/restore/mnemonas-b",
      "include_config": false
    }
  ]
}
```

**批量预览响应**:
```json
{
  "success": true,
  "data": {
    "id": "20260509T035901.000000000Z",
    "status": "completed",
    "started_at": "2026-05-09T03:59:01Z",
    "finished_at": "2026-05-09T03:59:02Z",
    "duration_ms": 1000,
    "items": [
      {
        "index": 0,
        "job_id": "external-disk",
        "target_path": "/mnt/restore/mnemonas-a",
        "include_config": true,
        "status": "completed",
        "preview": {
          "id": "20260509T035900.000000000Z",
          "job_id": "external-disk",
          "status": "completed",
          "started_at": "2026-05-09T03:59:00Z",
          "finished_at": "2026-05-09T03:59:01Z",
          "duration_ms": 1000,
          "source": "/srv/mnemonas",
          "destination": "/mnt/backup-drive/mnemonas",
          "target_path": "/mnt/restore/mnemonas-a",
          "file_count": 12,
          "total_bytes": 4096,
          "config_available": true,
          "config_included": true,
          "warnings": []
        }
      }
    ],
    "total_files": 12,
    "total_bytes": 4096,
    "warning": false,
    "warnings": []
  }
}
```

执行批量恢复：

```
POST /api/v1/maintenance/backups/batch-restore
```

**批量恢复响应**:
```json
{
  "success": true,
  "data": {
    "id": "20260509T040001.000000000Z",
    "status": "completed",
    "started_at": "2026-05-09T04:00:01Z",
    "finished_at": "2026-05-09T04:00:02Z",
    "duration_ms": 1000,
    "items": [
      {
        "index": 0,
        "job_id": "external-disk",
        "target_path": "/mnt/restore/mnemonas-a",
        "include_config": true,
        "status": "completed",
        "restore": {
          "id": "20260509T040000.000000000Z",
          "job_id": "external-disk",
          "status": "completed",
          "started_at": "2026-05-09T04:00:00Z",
          "finished_at": "2026-05-09T04:00:01Z",
          "duration_ms": 1000,
          "target_path": "/mnt/restore/mnemonas-a",
          "config_restored": true,
          "file_count": 12,
          "verified_bytes": 4096
        },
        "verify": {
          "id": "20260509T040005.000000000Z",
          "job_id": "external-disk",
          "status": "completed",
          "started_at": "2026-05-09T04:00:05Z",
          "finished_at": "2026-05-09T04:00:06Z",
          "duration_ms": 1000,
          "source": "/srv/mnemonas",
          "destination": "/mnt/backup-drive/mnemonas",
          "target_path": "/mnt/restore/mnemonas-a",
          "file_count": 12,
          "verified_bytes": 4096,
          "config_found": true,
          "files_dir_found": true,
          "internal_dir_found": true,
          "index_found": true,
          "objects_dir_found": true,
          "looks_like_storage_root": true
        },
        "warnings": []
      }
    ],
    "total_files": 12,
    "verified_bytes": 4096,
    "warning": false,
    "warnings": []
  }
}
```

批量接口最多接受 20 个条目。每个条目复用单任务恢复的 `target_path` 与 `include_config` 语义；服务端会拒绝重复目标、父子嵌套目标和同一批次中互相覆盖的目标路径。批量预览不写入目标目录，也不写入恢复历史；响应的 `items[]` 会分别返回每个条目的 `index`、`status`、`error_message` 和 `preview`，预览警告位于 `preview.warnings`，汇总提示位于顶层 `warnings`。批量恢复按顺序执行每个条目；成功恢复后会立即对对应目标执行 `restore-verify`，并在条目结果中返回 `restore`、`verify`、`warnings` 和 `error_message`。顶层 `total_files` 与 `verified_bytes` 汇总已完成条目的只读校验结果。批量结果中的错误和警告同样会脱敏远端目标内嵌的凭据模式。部分条目失败时，批量结果 `status = "completed"` 且 `warning = true`；全部条目失败时，`status = "failed"`。调用方应始终读取 `items[]` 的逐项状态，而不是只看批量总状态。

把支持的备份任务恢复到指定目录：

```
POST /api/v1/maintenance/backups/{id}/restore
```

**请求体**:
```json
{
  "target_path": "/mnt/restore/mnemonas",
  "include_config": true
}
```

当前显式恢复支持 `type = "local"`、`type = "restic"` 和 `type = "rclone"`。`target_path` 必须是服务器上的 POSIX 绝对路径（以 `/` 开头），不能包含控制字符、反斜杠、`.` 或 `..` 路径段，不能是文件系统根目录或受保护系统目录，并且必须位于 `storage.root`、备份来源和本地备份目标/仓库之外；Windows 或 UNC 路径不是有效的服务器恢复目标。父目录必须已存在，目标目录不存在或为空，且已存在的路径组件不能是符号链接。该接口不会覆盖当前在线数据目录。服务端会在真正写入前重新执行同一套恢复预检；存在失败预检时恢复会被拒绝，失败结果仍写入恢复历史，便于之后排查。

- `local`：把最近一次成功快照中的 `data/` 内容复制到 `target_path` 根目录并校验大小和 SHA-256；`include_config = true` 时，备份中的配置文件会恢复到 `target_path/.mnemonas-restore/config.toml`。
- `restic`：执行 `restic restore latest --target <临时目录> --tag mnemonas --tag job:<id> --path <source>`，再把 restic 恢复出的来源目录内容安装到 `target_path` 根目录。安装目标前会拒绝恢复出的符号链接和非常规文件。`include_config` 对 restic 任务无特殊处理。
- `rclone`：执行 `rclone copy <remote> <临时目录>` 恢复远端内容，再执行 `rclone check <remote> <临时目录> --one-way` 校验临时目录。拒绝恢复出的符号链接和非常规文件后，再把临时目录安装到 `target_path`。`include_config` 对 rclone 任务无特殊处理。

恢复开始和结束都会写入备份状态文件。恢复结果会携带本次 `preflight_checks`、`warnings`、`cutover_checklist` 和 `rollback_checklist`。失败恢复也会进入 `restore_history`，便于之后排查目标路径、权限、外部命令或仓库问题。

下载单个备份任务的恢复摘要：

```
GET /api/v1/maintenance/backups/{id}/restore-report
```

响应为 `application/json` 附件，包含 `job`、最近备份、最近保留检测、最近恢复演练、恢复演练历史、最近恢复、最近恢复后只读校验、最近恢复匹配的只读校验 `last_matching_restore_verify`、恢复历史和 `findings`。该摘要适合在切换 storage.root 前留档，或在恢复失败后随诊断信息一起保存。

恢复完成后，对目标目录执行只读校验，不写入恢复历史：

```
POST /api/v1/maintenance/backups/{id}/restore-verify
```

**请求体**:
```json
{
  "target_path": "/mnt/restore/mnemonas"
}
```

**响应示例**:
```json
{
  "success": true,
  "data": {
    "id": "20260509T040005.000000000Z",
    "job_id": "external-disk",
    "status": "completed",
    "source": "/srv/mnemonas",
    "destination": "/mnt/backup-drive/mnemonas",
    "snapshot_path": "/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z",
    "manifest_path": "/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json",
    "target_path": "/mnt/restore/mnemonas",
    "file_count": 42,
    "verified_bytes": 1048576,
    "config_path": "/mnt/restore/mnemonas/.mnemonas-restore/config.toml",
    "config_found": true,
    "files_dir_found": true,
    "internal_dir_found": true,
    "index_found": true,
    "objects_dir_found": true,
    "looks_like_storage_root": true,
    "warnings": []
  }
}
```

`restore-verify` 要求目标目录已存在，目标路径必须是服务器上的 POSIX 绝对路径（以 `/` 开头），不能包含控制字符、反斜杠、`.` 或 `..` 路径段，不能是文件系统根目录或受保护系统目录，并且仍位于 `storage.root`、备份来源和本地备份目标/仓库之外；Windows 或 UNC 路径不是有效的服务器恢复目标，目标路径中已存在的路径组件也不能是符号链接。它会统计恢复目录中的常规文件和字节数，检查 `.mnemonas-restore/config.toml`、`files/`、`.mnemonas/`、`.mnemonas/index.db` 与 `.mnemonas/objects` 是否存在，并对符号链接、非常规文件或不像完整 `storage.root` 的目录给出警告。`local` 任务会优先对照同一目标最近一次成功恢复所用的快照，找不到匹配恢复记录时再对照最新本地快照；响应中的 `snapshot_path` 和 `manifest_path` 表示实际对照对象。维护页在恢复成功后会自动调用该接口，并展示恢复后切换检查清单。

任务视图和恢复报告中的最近恢复摘要只会把目标路径一致、校验时间不早于最近恢复完成时间，且最近恢复已成功完成的 `last_restore_verify` 关联到 `last_restore`。任务视图会返回 `last_matching_restore_verify` 和 `restore_report_findings`，用于展示与恢复报告 `last_matching_restore_verify` 和 `findings` 相同的匹配校验与待处理发现项。任务视图和恢复报告会把匹配结果复制到 `last_matching_restore_verify`；不匹配时，该字段省略，并输出最近恢复尚未完成匹配只读校验的发现项。最近恢复仍在运行时，恢复报告会输出恢复未完成的发现项，并避免把旧校验结果关联到本次恢复。

**错误语义**:
- 未配置备份管理器：`503 Service Unavailable`
- 任务不存在：`404 Not Found`
- 同一任务已有备份或恢复演练在运行：`409 Conflict`
- 任务已停用：`409 Conflict`
- 本地任务没有可预览、可演练或可恢复的成功快照：`409 Conflict`
- 恢复请求的 `target_path` 或批量请求条目无效：`400 Bad Request`
- 显式恢复目标目录非空：`409 Conflict`
- 任务配置路径、备份源树或外部命令导致执行失败：`500 Internal Server Error`，错误响应 `details` 中包含失败的 run/drill/restore 结果

### 导出诊断信息

下载完整的诊断信息包（JSON 格式）。

**需要认证**: 当 `auth.enabled = true` 时需要管理员 JWT；未开启认证时可直接访问

```
GET /api/v1/diagnostics-export
```

**响应**: 返回 JSON 文件下载；响应包含 `Content-Disposition: attachment`，并通过 `Cache-Control: no-store` 禁止缓存。

导出的 JSON 根对象包含 `schema_version = 1`，用于标识诊断包结构版本。内容会包含脱敏后的 `alerts`、`disk_health` 和 `smb` 运行态信息，例如 `enabled`、`runtime_available`、通知通道配置状态、阈值、最近一次检查级别和 SMB 预览运行态；不会包含 Webhook URL、自定义 Header、Telegram Bot Token、企业微信 Webhook URL、钉钉 Webhook URL、`smtp_host`、`smtp_username`、`smtp_password`、`smtp_from`、`smtp_to` 或 SMB 凭据内容。

---

## WebDAV 接口

MnemoNAS 实现 WebDAV RFC 4918 核心读写方法，可用于文件管理器挂载。

**挂载地址**: `http://localhost:8080/dav/`

浏览器携带 `Origin` / `Referer` / `Sec-Fetch-Site` 元数据访问 WebDAV 写方法时，会执行同源检查；脚本客户端和标准 WebDAV 客户端通常不会发送这些浏览器来源头。WebDAV 文件和目录列表响应会带 `nosniff` 与 sandbox CSP，以降低用户文件在浏览器中同源打开时的脚本执行面。

支持的 WebDAV 方法:
- `PROPFIND` - 列出目录
- `GET` - 下载文件
- `PUT` - 上传文件  
- `DELETE` - 删除文件
- `MKCOL` - 创建目录；直接父目录不存在时返回 `409 Conflict`，目标已存在时返回带 `Allow` 的 `405 Method Not Allowed`
- `COPY` - 复制文件
- `MOVE` - 移动/重命名文件
- `LOCK/UNLOCK` - 文件锁定（虚拟实现）

不支持的 WebDAV 方法会返回 `405 Method Not Allowed`，并通过 `Allow` 响应头列出当前支持的方法。

WebDAV `MOVE` 的目标路径不存在但仍保留历史版本元数据时会返回 `409 Conflict`；目录移动会同时检查目标路径下的后代版本元数据。该目标冲突会先于用户配额和目录配额检查返回。

### 客户端配置示例

**macOS Finder**:
1. Finder → 前往 → 连接服务器
2. 输入: `http://localhost:8080/dav/`

**Windows 文件资源管理器**:
1. 此电脑 → 添加网络位置
2. 输入: `http://localhost:8080/dav/`

**Linux (GNOME Files)**:
1. 其他位置 → 连接到服务器
2. 输入: `dav://localhost:8080/dav/`

---

## 错误代码

| 错误代码 | 说明 |
|----------|------|
| `INVALID_PATH` | 无效的文件路径 |
| `PATH_TRAVERSAL` | 检测到路径遍历攻击 |
| `FILE_NOT_FOUND` | 文件不存在 |
| `FILE_TOO_LARGE` | 文件超过大小限制 |
| `INVALID_HASH` | 无效的哈希值 |
| `DATAPLANE_ERROR` | 数据面服务错误 |
| `INTERNAL_ERROR` | 内部服务器错误 |

---

## 速率限制

- 默认并发请求限制: 100
- 单文件上传超时: 30 分钟
- 健康检查超时: 5 秒
- 数据面连接超时: 10 秒

---

## 版本说明

本文档描述当前主线的 REST API。具体发布版本、兼容性说明和变更历史以 Git tag 与 [CHANGELOG](../CHANGELOG.md) 为准。
