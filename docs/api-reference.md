<!-- markdownlint-disable MD022 MD031 MD032 MD036 MD040 MD060 -->

# MnemoNAS API 参考

[English](api-reference.en.md) | 简体中文

本文档描述 MnemoNAS REST API 的约定、端点分组和请求/响应形状。默认 Base URL 为：

```text
http://localhost:8080
```

多数端点使用 JSON。文件上传、文件下载和归档下载端点使用文件载荷或流式响应。

JSON 请求体采用严格解析。写入端点会拒绝未知字段和拼接的多个 JSON 值，并返回 `400 invalid request body`。

## 认证

启用 Web UI/API 认证时，Web UI 使用同源 `HttpOnly` cookie 作为主会话。API 客户端仍可携带：

```http
Authorization: Bearer <access_token>
```

浏览器认证 Cookie 的名称和路径与当前请求模式对应：

- HTTPS 模式使用 `__Host-mnemonas_access`、`__Host-mnemonas_refresh` 和 `__Host-mnemonas_download_access`。Cookie 均带 `Secure`，作用域为 `/`，不设置 `Domain`。
- 本机 HTTP 模式使用 `mnemonas_access`、`mnemonas_refresh` 和 `mnemonas_download_access`。access 与 download Cookie 的作用域为 `/api/v1`，refresh Cookie 的作用域为 `/api/v1/auth`。
- HTTPS 请求只解析 `__Host-` 名称，HTTP 请求只解析无前缀名称，两种模式不会相互回退。同一请求中同名 Cookie 出现不同值时，服务端拒绝认证；access 与 download Cookie 解析到不同账号时也会拒绝认证。

登录和刷新端点设置 access 与 refresh Cookie，下载会话端点设置 download Cookie。浏览器客户端可以发送 `X-MnemoNAS-Session-Mode: cookie`；在该模式下，JSON 响应不会返回 bearer token，只返回用户和会话元数据。

WebDAV `auth_type = "users"` 接受 MnemoNAS 用户凭据的 HTTP Basic 登录，并应用角色、用户组、`home_dir`、目录访问规则、home 范围用户配额和目录配额边界。WebDAV `auth_type = "basic"` 仍是独立的全局服务凭据模式。

## 响应格式

多数 `/api/v1` 成功响应：

```json
{
  "success": true,
  "data": {},
  "message": "ok",
  "request_id": "optional",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

多数 `/api/v1` 错误响应：

```json
{
  "code": "BAD_REQUEST",
  "message": "error description",
  "details": {},
  "request_id": "optional",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

认证和公开分享错误使用：

```json
{
  "success": false,
  "error": {
    "code": "ERROR_CODE",
    "message": "error description"
  }
}
```

认证后的分享和收藏管理端点使用 `success + data (+ message)`。`/api/v1/public/shares/*` 下的公开分享端点在成功时返回原始 JSON 对象或数组，失败时返回结构化的 `success: false` 错误。

## HTTP 状态码

| 状态码 | 含义 |
| --- | --- |
| `200` | 成功 |
| `201` | 已创建 |
| `400` | 请求无效 |
| `401` | 未认证或 token 已过期 |
| `403` | 已认证但无权限 |
| `404` | 未找到 |
| `409` | 资源冲突或操作当前不可执行 |
| `410` | 资源不可用、已过期、已禁用或达到访问上限 |
| `413` | 文件过大 |
| `429` | 已限流 |
| `507` | 用户或目录配额不足 |
| `500` | 内部错误 |
| `503` | 服务依赖不可用 |

## Warning 响应头

部分写入端点可能已经提交可见变更，但后续持久化或清理步骤失败。此时端点仍返回成功状态，并附带 HTTP `Warning` 响应头，例如：

- `199 MnemoNAS "activity log persistence failed"`
- `199 MnemoNAS "auth state persistence incomplete"`
- `199 MnemoNAS "workspace mutation persistence incomplete"`
- `199 MnemoNAS "share persistence incomplete"`
- `199 MnemoNAS "favorites persistence incomplete"`
- `199 MnemoNAS "scrub result persistence incomplete"`
- `199 MnemoNAS "backup run completed with warnings"`
- `199 MnemoNAS "trash restore metadata reconciliation failed"`
- `199 MnemoNAS "delete cleanup incomplete"`
- `199 MnemoNAS "trash delete cleanup incomplete"`

客户端应同时检查 HTTP `Warning` 响应头和 JSON body。

## MnemoNAS 路径约定

文件、目录、收藏、活动筛选、`home_dir`、目录配额和目录访问规则字段使用 MnemoNAS 逻辑绝对路径：

- 路径使用 `/` 分隔，并归一化为以 `/` 开头的形式。
- 控制字符和独立的 `.` 或 `..` 路径段无效；合法名称中的连续点号（例如 `foo..txt`）仍有效。
- 根路径 `/` 只在端点明确允许时有效。
- URL 路径参数按路径段编码，同时保留 `/` 分隔符。

## 认证端点

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/api/v1/auth/login` | 使用用户名和密码登录 |
| `POST` | `/api/v1/auth/refresh` | 用 refresh token 换取新的 access token |
| `GET` | `/api/v1/auth/me` | 获取当前用户 |
| `POST` | `/api/v1/auth/logout` | 退出登录 |
| `POST` | `/api/v1/auth/download-session` | 创建短期下载会话 cookie |
| `POST` | `/api/v1/auth/password` | 修改当前用户密码 |

登录请求：

```json
{
  "username": "admin",
  "password": "example_password"
}
```

API 客户端登录响应：

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
      "home_dir": "/",
      "must_change_password": true
    }
  }
}
```

Cookie 会话登录会设置当前 HTTPS 或 HTTP 模式对应的 access 与 refresh Cookie。refresh Cookie 的路径覆盖认证端点，因此 access Cookie 过期后，退出登录仍可吊销当前会话。携带 `X-MnemoNAS-Session-Mode: cookie` 时，`data` 对象会省略 `access_token` 和 `refresh_token`。

每次登录都会在首次签发 token 前持久化一个活动登录会话。每个用户最多保留 64 个活动会话，服务全局最多保留 4096 个；超过任一限制时，登录返回 `429 REFRESH_SESSION_LIMIT`。

刷新端点接受 API 客户端提交的 JSON refresh token，也接受 Web UI 在当前请求模式下使用的 refresh Cookie。刷新会轮换 refresh token，并设置新的 access/refresh Cookie；轮换后的 token 保留登录时确定的会话绝对到期时间，不会无限延长会话。每个 refresh token 只能成功使用一次；再次使用已轮换的 token 会返回 `401 TOKEN_REVOKED`，并吊销同一登录会话中已经签发的子 token。

同一登录会话最多每 30 秒轮换一次；提前轮换返回 `429 REFRESH_RATE_LIMITED` 和 `Retry-After: 30`。使用 refresh Cookie，或携带 `X-MnemoNAS-Session-Mode: cookie` 时，JSON 响应不返回 bearer token。

认证状态持久化在原子重命名前明确失败时，签发和轮换请求不会发布新 Cookie，退出请求也不会清理现有 Cookie。如果重命名已提交，但父目录同步结果不确定，变更仍视为成功，响应会附带 `Warning: 199 MnemoNAS "auth state persistence incomplete"`。认证时间租约已耗尽且无法续租时，受保护请求和刷新请求返回 `503 TOKEN_STATE_UNAVAILABLE`，并保留现有 Cookie。

当前用户响应示例：

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
      "home_dir": "/",
      "must_change_password": true
    }
  }
}
```

退出登录会从权威会话注册表中删除当前登录会话，立即使该会话的 access、refresh 及已轮换 token 失效，但不影响同一用户的其他独立登录。Web UI 通过会话 Cookie 提交；API 客户端也可以在请求体中提交 `{"refresh_token":"<refresh-token>"}`。即使 access Cookie 已过期，只要 refresh Cookie 或请求体中的 refresh token 仍有效，服务端仍会吊销该会话。吊销持久化明确失败时返回 `500` 并保留会话 Cookie，以便重试；成功或仅目录同步结果不确定时，服务端会清理当前请求模式对应的 access、refresh 和短期 download Cookie。

`POST /api/v1/auth/download-session` 创建短期下载会话 cookie，用于浏览器预览、缩略图和下载等无法附加 `Authorization` 头的流程。

该 Cookie 为 `HttpOnly`、`SameSite=Strict`，过期时间与当前 access token 一致。HTTPS 模式设置 `__Host-mnemonas_download_access`、`Secure`、`Path=/` 且不设置 `Domain`；本机 HTTP 模式设置 `mnemonas_download_access`，作用域为 `/api/v1`。

退出登录响应示例：

```json
{
  "success": true,
  "data": null,
  "message": "logged out successfully"
}
```

下载会话响应示例：

```json
{
  "success": true,
  "data": null
}
```

修改密码请求：

```json
{
  "old_password": "current_password",
  "new_password": "new_secure_password",
  "expected_user_id": "current_session_user_id"
}
```

`expected_user_id` 为必填字段，必须与认证上下文中的当前用户 ID 一致。两者不一致时，端点返回 `409 AUTH_SCOPE_CHANGED`，且不会修改任何账户的密码。

修改密码响应示例：

```json
{
  "success": true,
  "data": null,
  "message": "password changed successfully"
}
```

修改成功后，服务端会递增账户凭据版本、撤销该账户的全部活动会话，并清除当前请求模式下的 access、refresh 和 download Cookie。响应不会签发新令牌，客户端必须使用新密码重新登录。

Web 客户端在请求已经发出，且单页应用仍能观察到传输中断、应用内页面离开，或未携带已知 MnemoNAS 错误码的网关与代理失败响应时，会将修改结果视为无法确认，清除当前浏览器的认证状态，并在登录页提示先尝试使用新密码；新密码无法登录时，再尝试原密码。浏览器硬刷新、关闭标签页或进程终止可能先于客户端中止处理，不能保证立即清理状态或显示提示；此类中断同样应按修改结果无法确认处理。

密码已经修改，但会话撤销或认证状态持久化未完全确认时，端点仍返回 HTTP 200，同时设置 `Warning: 199 MnemoNAS "auth state persistence incomplete"`，并返回：

```json
{
  "success": true,
  "data": {
    "warning": true
  },
  "message": "password changed with persistence warning"
}
```

修改密码错误：

| HTTP 状态 | 错误码 | 含义 |
|---|---|---|
| `400` | `MISSING_PASSWORD` | 当前密码或新密码为空 |
| `400` | `MISSING_EXPECTED_USER_ID` | 请求未提供 `expected_user_id` |
| `401` | `INVALID_PASSWORD` | 当前密码不正确 |
| `400` | `PASSWORD_TOO_SHORT` | 新密码少于 8 个 UTF-8 字节或只包含空白字符 |
| `400` | `PASSWORD_TOO_LONG` | 新密码超过 72 个 UTF-8 字节 |
| `400` | `PASSWORD_UNCHANGED` | 新密码与当前密码相同 |
| `409` | `AUTH_SCOPE_CHANGED` | `expected_user_id` 与认证上下文中的当前用户不一致 |
| `500` | `PASSWORD_ERROR` | 密码状态未能更新 |

认证中间件仍可能返回 `MISSING_AUTH_HEADER`、`INVALID_AUTH_HEADER`、`INVALID_TOKEN`、`TOKEN_EXPIRED`、`TOKEN_REVOKED`、`USER_NOT_FOUND`、`USER_DISABLED` 或 `TOKEN_STATE_UNAVAILABLE`。除成功或持久化警告响应外，端点不会主动清除有效会话。

自动创建的初始管理员返回 `must_change_password=true`。成功登录和刷新会话都会保留服务器端 `initial-password.txt`，避免在完成改密前丢失唯一的持久化初始凭据。标记为 `true` 时，认证会话只能访问当前用户信息、修改密码和退出端点；其他受保护端点返回 `403 PASSWORD_CHANGE_REQUIRED`。当前用户通过 `POST /api/v1/auth/password` 设置不同于当前密码的新密码后，标记变为 `false`，初始密码文件会删除，旧访问令牌和刷新令牌因凭据版本变化而失效；提交相同密码返回 `400 PASSWORD_UNCHANGED`。管理员重置其他用户密码时，目标用户重新进入需要修改密码状态；管理员重置自己的密码按自行修改处理。

登录防护包含凭据检查限制和连续失败锁定：

- 客户端地址默认使用直连来源。
- 仅当配置了 `server.trusted_proxy_hops`，且请求来自 loopback 或 `server.trusted_proxy_cidrs` 中列出的代理地址时，才根据转发头解析客户端地址。
- 每个客户端 IP 在固定的 10 秒窗口内最多执行 12 次密码凭据检查；超限请求在执行 bcrypt 前返回 `429 LOGIN_RATE_LIMITED`。
- 通过凭据检查窗口的请求仍按“归一化用户名 + 客户端 IP”统计连续失败；达到失败阈值后应用短期锁定。
- 配置提醒通道时，登录限流会发送限频的 `login_rate_limited` warning 事件。
- 事件详情只包含 `trigger`、`key_scope`、`username_status` 和 `client_ip_scope` 分类字段，不包含原始用户名、客户端地址、密码或 token。
- `username_status` 为 `unknown`、`invalid` 或 `provided`；`client_ip_scope` 为 `public`、`private`、`loopback`、`link_local`、`multicast`、`unspecified` 或 `unknown`。

## 管理员用户端点

需要管理员角色。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/admin/users` | 列出用户 |
| `POST` | `/api/v1/admin/users` | 创建用户 |
| `PUT` | `/api/v1/admin/users/{id}` | 更新用户元数据、角色、home 目录或配额 |
| `DELETE` | `/api/v1/admin/users/{id}` | 删除用户 |
| `POST` | `/api/v1/admin/users/{id}/reset-password` | 重置用户密码 |
| `POST` | `/api/v1/admin/users/{id}/revoke-sessions` | 吊销用户现有会话 |
| `PUT` | `/api/v1/admin/users/{id}/status` | 启用或禁用用户 |

用户角色为 `admin`、`user` 和 `guest`。非管理员用户受 `home_dir` 和匹配的目录访问规则约束。

用户响应包含 `id`、`username`、`email`、`role`、`groups`、`disabled`、`home_dir`、`created_at`、`updated_at`、可选 `last_login_at`、`must_change_password`、可选 `password_changed_at`、`quota_bytes` 和 `used_bytes`。密码修改时间只出现在管理员用户管理响应中，不出现在普通登录和当前用户响应中。列表响应还会返回 `quota_history_available` 和 `quota_history`；服务端按分层策略保留配额聚合变化快照：近 30 天保留所有变化，1 年内保留每天最新快照，3 年内保留每月最新快照，并最多保留 512 条。历史不可写时该标记为 `false`，用户列表仍会返回。

列表响应示例：

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
    "total": 1,
    "quota_history_available": true,
    "quota_history": [
      {
        "captured_at": "2024-01-15T10:00:00Z",
        "total_count": 1,
        "active_count": 1,
        "limited_count": 0,
        "warning_count": 0,
        "exceeded_count": 0,
        "attention_count": 0,
        "used_bytes": 0,
        "limited_used_bytes": 0,
        "quota_bytes": 0
      }
    ]
  }
}
```

创建请求示例：

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

创建响应示例：

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

`POST /api/v1/admin/users/{id}/revoke-sessions` 会让该用户现有 Web cookie 会话、access token 和 refresh token 失效，但不改变用户密码或启用状态。用户下一次请求需要重新登录。

创建和更新用户时应用下列字段规则：

- 用户名最长 255 个字符，不能包含 `/`、`\`、控制字符、`.` 或 `..`。
- 密码必须包含 8 到 72 个 UTF-8 字节，且不能只包含空白字符。
- 创建时可省略 `home_dir`，默认值为 `/<username>`。
- 提供 `home_dir` 时，会归一化为干净的 MnemoNAS 绝对路径，且不能为空，不能包含 `.`、`..` 路径段或控制字符。
- `user` 和 `guest` 角色不能使用 `/` 作为 `home_dir`；`admin` 可以使用 `/` 访问全局命名空间。
- `quota_bytes` 可选，`0` 表示不限制。
- 用户组名会归一化为小写，只允许字母、数字、`.`、`_` 和 `-`。

`PUT /api/v1/admin/users/{id}` 至少接受下列字段之一：

```json
{
  "email": "user@example.com",
  "role": "user",
  "groups": ["family", "editors"],
  "home_dir": "/alice",
  "quota_bytes": 10737418240
}
```

更新响应示例：

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

删除响应示例：

```json
{
  "success": true,
  "data": null,
  "message": "user deleted successfully"
}
```

用户被删除或禁用后，该用户创建的公开分享不再暴露元数据、下载或文件夹列表；公开请求返回 `404 Not Found` 和 `SHARE_NOT_FOUND`，避免泄露所有者账户是否曾存在。

重置密码响应示例：

```json
{
  "success": true,
  "data": null,
  "message": "password reset successfully"
}
```

吊销会话响应示例：

```json
{
  "success": true,
  "data": {
    "revoked": true
  },
  "message": "user sessions revoked successfully"
}
```

启用或禁用响应示例：

```json
{
  "success": true,
  "data": {
    "disabled": true
  },
  "message": "user status updated successfully"
}
```

用户配额：

- `quota_bytes = 0` 表示无限制。
- 大于零时，服务端配额检查会应用于非管理员 Web/API 上传、复制、移动、回收站恢复。
- 当 `webdav.auth_type = "users"` 且写入目标位于用户 `home_dir` 内时，WebDAV PUT/COPY/MOVE 也会应用该检查。
- 检查基于 `home_dir` 下的当前逻辑大小；共享目录应使用 `storage.directory_quotas` 限制。
- 超出配额返回 `507 Insufficient Storage`，错误码为 `QUOTA_EXCEEDED`。
- `details` 包含 `quota_type`、`quota_path`、`used_bytes`、`quota_bytes`、`required_bytes` 和 `available_bytes`。
- 启用提醒通道时，Web/API 上传、复制、移动和回收站恢复的配额拒绝也会发送 `quota_exceeded` warning 事件。
  事件详情只保留操作、`actor_scope`、配额类型和字节数，不包含账户名、home 目录、目标路径或配额路径。

目录配额：

- `storage.directory_quotas` 可为 MnemoNAS 逻辑目录定义硬限制。
- 匹配的 Web/API 上传、复制、移动、回收站恢复、版本恢复和 WebDAV PUT/COPY/MOVE 返回同样的 `QUOTA_EXCEEDED` 错误。
- 目录配额拒绝会在 `details` 中加入 `quota_type="directory"` 与 `quota_path`。
- Web/API 目录配额拒绝，包括版本恢复，也会发送不含匹配目录路径的 `quota_exceeded` 提醒事件。

`storage.directory_access_rules` 可按用户、用户组或角色授予共享目录读写访问。对非管理员用户，匹配规则使用最具体路径，并在该路径上覆盖 `home_dir` 边界。写入授权同时允许读取；写操作必须有写授权。

`webdav.auth_type = "basic"` 仍是全局服务凭据兼容模式，不携带应用用户的 `home_dir` 身份。

把当前管理员自己的角色改为非管理员会被 `SELF_ROLE_CHANGE` 拒绝。会移除最后一个已启用管理员的角色或状态更新会被 `LAST_ADMIN` 拒绝。

## 系统端点

| 方法 | 路径 | 认证 | 说明 |
| --- | --- | --- | --- |
| `GET` | `/health` | 否 | 健康检查 |
| `HEAD` | `/health` | 否 | 只返回健康状态和响应头，不返回 body |
| `GET` | `/api/v1/version` | 通常否 | 版本和构建信息 |
| `GET` | `/api/v1/setup/` | 否 | 初始设置状态 |
| `GET` | `/api/v1/setup/readiness` | 管理员；禁用认证时只读 | 获取自动验证的初始化就绪状态 |
| `POST` | `/api/v1/setup/acknowledge` | 管理员 | 完成首次设置 |
| `POST` | `/api/v1/setup/defer` | 管理员 | 延期处理可延期的备份项 |
| `GET` | `/api/v1/stats` | 是 | 存储统计 |
| `GET` | `/api/v1/diagnostics` | 管理员 | 诊断信息 |
| `GET` | `/api/v1/diagnostics-export` | 管理员 | 下载脱敏诊断包 |
| `GET` | `/api/v1/metrics` | 启用认证时需管理员 | JSON 指标 |

Prometheus 不能直接以原生 exposition 格式抓取 `/api/v1/metrics`。需要使用 JSON exporter 或转换层。

健康检查响应：

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

`uptime` 保留 Go duration 字符串，`uptime_secs` 提供整秒数，便于客户端稳定展示。配置的数据平面、缩略图缓存、维护历史、活动日志、收藏存储或 WebDAV 凭据子系统初始化失败时，`status` 会降级为 `degraded`。

### 初始化状态

返回首次运行设置状态。

```http
GET /api/v1/setup/
```

响应示例：

```json
{
  "success": true,
  "is_first_run": true,
  "auth_enabled": true,
  "share_enabled": true,
  "webdav_enabled": true,
  "webdav_auth_type": "basic",
  "allow_unsafe_no_auth": false
}
```

说明：

- 该端点不返回初始用户名或密码。
- `allow_unsafe_no_auth` 只反映危险配置例外是否开启；公网部署前仍应以安全自检、`mnemonas-doctor --public-domain` 和云防火墙复核为准。
- 首次运行的 Web 管理员密码只写入 `auth.users_file` 旁的 `initial-password.txt`；默认路径为 `<storage.root>/.mnemonas/initial-password.txt`，非交互启动日志只报告文件路径。
- `is_first_run` 在首次设置完成前为 `true`；有效延期期间为 `false`，到期后自动恢复为 `true`。管理员就绪接口会独立重新计算证据，并可在延期期间提前恢复提示。
- 该端点返回 setup 专用的扁平 JSON，不使用通用 `data` 包装。
- 响应包含 `Cache-Control: private, no-store`，并以 `Vary: Cookie` 和 `Vary: Authorization` 标记认证相关请求差异。

### 初始化就绪状态

管理员就绪状态由账号、初始凭据文件、备份与安全自检的服务端证据计算，不接受浏览器提交的主观完成标记。

```http
GET /api/v1/setup/readiness
```

响应使用通用 `data` 包装。完整响应结构如下；`title` 和 `message` 是随检测结果变化的展示文本，不应作为程序判断依据。

```json
{
  "success": true,
  "data": {
    "lifecycle": "pending",
    "prompt": true,
    "generated_at": "2026-07-13T12:00:00Z",
    "overall_status": "action_required",
    "can_complete": false,
    "can_defer": true,
    "required": {
      "completed": 4,
      "total": 6
    },
    "recommended": {
      "completed": 2,
      "total": 4
    },
    "checks": [
      {
        "id": "admin_access",
        "requirement": "required",
        "status": "complete",
        "deferrable": false,
        "title": "管理员访问可用",
        "message": "至少有一个启用中的管理员账号。",
        "action": "manage_users"
      },
      {
        "id": "bootstrap_credential",
        "requirement": "required",
        "status": "complete",
        "deferrable": false,
        "title": "初始密码已更换",
        "message": "启用中的管理员均已完成密码更换。",
        "action": "change_password"
      },
      {
        "id": "initial_password_file",
        "requirement": "required",
        "status": "complete",
        "deferrable": false,
        "title": "清理初始密码文件",
        "message": "服务器上没有遗留初始密码文件。",
        "action": "change_password"
      },
      {
        "id": "security_baseline",
        "requirement": "required",
        "status": "complete",
        "deferrable": false,
        "title": "满足安全基线",
        "message": "安全基线没有阻断项。",
        "action": "review_security"
      },
      {
        "id": "backup_job",
        "requirement": "required",
        "status": "incomplete",
        "deferrable": true,
        "title": "添加独立备份",
        "message": "尚未添加启用中的备份任务。",
        "action": "create_backup"
      },
      {
        "id": "backup_success",
        "requirement": "required",
        "status": "incomplete",
        "deferrable": true,
        "title": "完成首次备份",
        "message": "尚无当前有效的成功备份。",
        "action": "run_backup"
      },
      {
        "id": "admin_redundancy",
        "requirement": "recommended",
        "status": "complete",
        "deferrable": false,
        "title": "准备备用管理员",
        "message": "已有备用管理员账号。",
        "action": "manage_users"
      },
      {
        "id": "backup_schedule",
        "requirement": "recommended",
        "status": "incomplete",
        "deferrable": false,
        "title": "启用自动备份",
        "message": "建议为备份任务启用自动计划。",
        "action": "create_backup"
      },
      {
        "id": "restore_verification",
        "requirement": "recommended",
        "status": "incomplete",
        "deferrable": false,
        "title": "验证恢复能力",
        "message": "建议执行一次恢复演练并保持验证结果有效。",
        "action": "run_restore_drill"
      },
      {
        "id": "security_recommendations",
        "requirement": "recommended",
        "status": "complete",
        "deferrable": false,
        "title": "处理安全建议",
        "message": "安全自检全部通过。",
        "action": "review_security"
      }
    ],
    "summary": {
      "auth_enabled": true,
      "active_admin_count": 2,
      "password_change_required_admin_count": 0,
      "initial_password_file": "missing",
      "enabled_backup_job_count": 0,
      "security_status": "pass",
      "security_blocking_check_ids": []
    }
  },
  "timestamp": "2026-07-13T12:00:00Z"
}
```

固定枚举如下：

- `lifecycle`：`pending`、`deferred`、`completed`。
- `overall_status`：`ready`、`action_required`、`unavailable`。
- `requirement`：`required`、`recommended`。
- 检查项 `status`：`complete`、`incomplete`、`unavailable`、`not_applicable`。
- `action`：`change_password`、`manage_users`、`create_backup`、`run_backup`、`run_restore_drill`、`review_security`。
- `summary.initial_password_file`：`missing`、`present`、`unavailable`。
- `summary.security_status`：`pass`、`warning`、`block`、`unavailable`。

固定检查项及操作映射如下：

| 检查 ID | 级别 | 可延期 | 可能的 `action` | 服务端证据 |
| --- | --- | --- | --- | --- |
| `admin_access` | `required` | 否 | `manage_users`、`review_security` | 认证已启用且至少有一个启用中的管理员 |
| `bootstrap_credential` | `required` | 否 | `change_password`、`manage_users`、`review_security` | 启用中的管理员均无需强制修改密码 |
| `initial_password_file` | `required` | 否 | `change_password` | 初始密码文件已不存在且路径检查通过 |
| `security_baseline` | `required` | 否 | `review_security` | 安全自检没有必须处理的阻断项 |
| `backup_job` | `required` | 是 | `create_backup` | 存在启用中的独立备份任务 |
| `backup_success` | `required` | 是 | `run_backup` | 存在当前有效的成功备份 |
| `admin_redundancy` | `recommended` | 否 | `manage_users` | 至少有两个启用中的管理员 |
| `backup_schedule` | `recommended` | 否 | `create_backup` | 存在启用中的自动备份计划 |
| `restore_verification` | `recommended` | 否 | `run_restore_drill` | 存在当前有效的恢复验证记录 |
| `security_recommendations` | `recommended` | 否 | `review_security` | 安全自检没有警告或阻断项 |

`required.completed` 和 `recommended.completed` 统计状态为 `complete` 或 `not_applicable` 的检查项。`completed_at`、`deferred_until`、`summary.latest_backup_success_at` 和 `summary.latest_restore_verification_at` 为可选 RFC 3339 时间；仅在相应证据存在时返回。`generated_at` 是本次重新计算就绪状态的时间。

响应不会包含用户名、用户 ID、文件路径、备份目标或安全自检原始详情。

响应包含 `Cache-Control: private, no-store`，并以 `Vary: Cookie` 和 `Vary: Authorization` 标记认证相关请求差异。

禁用认证时可读取脱敏状态，但 `admin_access` 不会通过，完成和延期接口均返回 `403`。

### 完成首次设置

服务端会在写入完成时间前重新计算全部必需项。建议项不阻止完成。

```http
POST /api/v1/setup/acknowledge
```

请求体：`{}`。

成功响应中的 `data` 与就绪状态的完整结构相同，并返回 `message: "setup completed"`。此时 `lifecycle` 为 `completed`，`prompt`、`can_complete` 和 `can_defer` 均为 `false`，`completed_at` 为首次完成时间。重复调用返回 `message: "setup already completed"`，并保留首次 `completed_at`。

失败行为：

- 调用方未登录或不是管理员时返回 `401` 或 `403`。
- 必需项未完成时返回 `409 SETUP_NOT_READY`，`details.required_check_ids` 只列出检查 ID。
- 必需证据不可用时返回 `503 SETUP_READINESS_UNAVAILABLE`。
- 已完成后重复调用返回 `200`。

### 延期首次设置

只有备份任务和首次成功备份尚未完成、且其他必需项均通过时才允许延期。

```http
POST /api/v1/setup/defer
Content-Type: application/json

{"remind_in_days": 7}
```

`remind_in_days` 必须是 `1` 到 `30` 的整数。成功响应中的 `data` 与就绪状态的完整结构相同，并返回 `message: "setup deferred"`；`lifecycle` 为 `deferred`，`prompt` 为 `false`，`deferred_until` 为新的提醒期限。延期不写入完成时间；到期后 `lifecycle` 自动恢复为 `pending`。不可延期时返回 `409 SETUP_DEFER_FORBIDDEN`。

`GET /api/v1/stats` 返回各统计组的可用性标志。

管理员响应可包含来自 Linux mountinfo 的磁盘挂载元数据：

- `disk_mount_point`
- `disk_mount_source`
- `disk_mount_options`

这些字段用于确认承载 MnemoNAS 的文件系统、设备或数据集：

- `disk_mount_point` 中类似 secret 的路径片段会被脱敏。
- `disk_mount_source` 中的 URL userinfo 和类似 secret 的参数会被脱敏。
- 凭据、用户名、密码、密钥和 token 等敏感 mount option 值也会被脱敏。

管理员响应还可包含 `directory_quota_stats_available` 与 `directory_quotas`。每个目录配额条目包含 `path`、`quota_bytes`、`used_bytes`、`available_bytes`、`usage_ratio`、`exists` 和 `status`。目录配额 `status` 为 `normal`、`warning`、`exceeded` 或 `missing`。

启用认证时，home 范围非管理员用户不会收到全局磁盘、CAS、文件数量或目录配额统计。

## 文件操作

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/files/{path}` | 列出目录或获取文件元数据 |
| `POST` | `/api/v1/files/{path}` | 上传或覆盖文件 |
| `POST` | `/api/v1/files-delete-intents` | 获取待删除目标与当前删除策略的原子确认快照 |
| `DELETE` | `/api/v1/files/{path}` | 按已确认的目标快照和当前删除策略移入回收站或永久删除 |
| `POST` | `/api/v1/files-move` | 移动或重命名资源 |
| `POST` | `/api/v1/files-copy` | 递归复制文件或目录 |
| `GET` | `/api/v1/download/{path}` | 认证后的文件下载或 ZIP 归档下载 |
| `POST` | `/api/v1/directories/{path}` | 创建目录 |

目录列表权限：

- 对非管理员调用方，目录列表会对请求目录及其直接子项应用相同的 `home_dir` 和最具体的 `storage.directory_access_rules` 检查。
- 无读取权限的子项会从响应中省略。
- 请求根目录 `/` 时，只返回用户的 `home_dir` 和可读共享目录的顶层入口，不返回其他全局根内容。
- 仅授予嵌套共享目录时，已存在的祖先目录可作为只读导航入口；在这些祖先下创建、移动或复制仍需要显式写授权。

列表响应会为当前目录和每个条目返回 `capabilities`：

- `read` 表示路径可列出或作为导航打开。
- `concreteRead` 表示允许下载、复制源、分享或收藏等精确资源读取动作。
- `write` 表示可在该路径或容器内执行变更。

例如，根目录可能因允许在根下上传或创建而返回 `write: true`，同时因根目录本身不可下载或复制而返回 `concreteRead: false`。

每个实际文件或目录条目还包含 `deleteIdentityToken`。Linux 与 macOS 上，该字段是 64 位小写十六进制 SHA-256 不透明值，绑定文件系统设备号、inode、ctime、类型与权限位、大小和纳秒级修改时间。即使同一路径被替换成类型、大小和修改时间相同的新对象，令牌也会变化。平台无法提供所需对象身份时，该字段为 `null`，对应条目不能创建删除意图。

列表响应的 `data` 还会返回当前删除策略：

- `deleteMode`：`trash` 表示移入回收站，`permanent` 表示直接永久删除。
- `deletePolicyToken`：当前完整删除策略的 64 位小写十六进制 SHA-256 标识。该字段为不透明值，不用于推导设置内容。
- `trashRetentionDays`：当前策略为新回收站项目设置的到期天数。`0` 表示项目创建后立即进入待清理状态。
- `trashAutoCleanupEnabled`：保留清理周期是否已启用。该值为 `false` 时，到期项目不会由周期任务自动清理。

客户端必须把这四个字段作为同一策略快照处理。策略缺失或无法识别时，不应发起删除请求。`deletePolicyToken` 会覆盖删除方式、回收站保留天数、后台清理周期和回收站容量上限等会改变删除后果的设置。

删除确认应先调用 `POST /api/v1/files-delete-intents`。请求体只接受 `targets`，必须包含 1 至 1000 个互不重复、非根路径且互不嵌套的目标。每个目标必须同时提供 `path`，以及从同一条当前列表记录原样复制的 `observedIdentityToken`：

```json
{
  "targets": [
    {
      "path": "/documents/report.pdf",
      "observedIdentityToken": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
    },
    {
      "path": "/photos/2026",
      "observedIdentityToken": "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
    }
  ]
}
```

`targets` 缺失、为 `null` 或为空，目标字段缺失，令牌为空、长度错误、包含大写或非十六进制字符，路径重复或嵌套，出现旧 `paths` 字段或任意未知字段时，返回 `400 Bad Request`。服务端会在同一文件系统读锁内按“写权限、挂载边界、文件类型、观察身份”的顺序检查根对象。观察身份不匹配时返回 `409 DELETE_TARGET_CHANGED`，`details` 只包含 `path`，且不会读取文件内容、遍历目录或修改数据。服务端当前无法生成对象身份时返回 `503 Service Unavailable`。

响应同时返回 `deleteMode`、`deletePolicyToken`、`trashRetentionDays`、`trashAutoCleanupEnabled` 和按请求顺序排列的 `targets`。每个目标包含 `path`、`name`、`isDir`、`size`、UTC RFC 3339 纳秒格式的 `modTime`、`deleteIdentityToken` 与 `deleteTargetToken`；其中 `deleteIdentityToken` 与请求中对应的 `observedIdentityToken` 相同。`deleteTargetToken` 是 64 位小写十六进制不透明值，覆盖目标路径及其完整当前目录树的条目路径、对象身份、类型、大小、纳秒级修改时间和文件内容。目标不存在、父路径类型错误或任一目标树不具备完整写权限时，不会返回部分确认结果；目标树包含符号链接、FIFO、Unix socket、其他非普通文件或工作区根目录下的嵌套挂载点时返回 `409 Conflict`。嵌套挂载检查包括 bind mount，以及目标本身位于嵌套挂载目录内的情况。

`DELETE /api/v1/files/{path}` 必须恰好提供一次 `expected_delete_mode`、一次 `expected_delete_policy_token` 和一次 `expected_delete_target_token` 查询参数。模式与两个令牌必须原样使用同一删除意图响应中的对应值；`expected_delete_mode` 的值严格为 `trash` 或 `permanent`，两个令牌均为 64 位小写十六进制值。

`expected_delete_mode` 缺失或为空时返回 `400 MISSING_EXPECTED_DELETE_MODE`；重复、非法查询编码、大小写不符、包含首尾空白或值未知时返回 `400 INVALID_EXPECTED_DELETE_MODE`。`expected_delete_policy_token` 缺失或为空时返回 `400 MISSING_EXPECTED_DELETE_POLICY_TOKEN`；重复、长度错误、包含大写或非十六进制字符、包含首尾空白或非法查询编码时返回 `400 INVALID_EXPECTED_DELETE_POLICY_TOKEN`。`expected_delete_target_token` 缺失或为空时返回 `400 MISSING_EXPECTED_DELETE_TARGET_TOKEN`；重复、长度错误、包含大写或非十六进制字符、包含首尾空白或非法查询编码时返回 `400 INVALID_EXPECTED_DELETE_TARGET_TOKEN`。

服务端会在同一存储写锁内先比较预期策略，再逐项复核当前目标树的写权限与目标令牌，最后执行删除。完整策略已经变化时返回 `409 DELETE_POLICY_CHANGED`，`details` 包含 `expected_delete_mode`、`expected_delete_policy_token`、`actual_delete_mode`、`actual_delete_policy_token`、`trash_retention_days` 和 `trash_auto_cleanup_enabled`。策略未变但目标路径、内容或目录树已经变化时返回 `409 DELETE_TARGET_CHANGED`。服务端能够生成当前目标令牌时，`details` 包含 `path`、`expected_delete_target_token` 和 `actual_delete_target_token`；已确认目标消失或父路径不再是目录时，`details` 包含 `path` 和 `expected_delete_target_token`，不返回无法生成的 `actual_delete_target_token`。挂载点和特殊文件冲突仍按各自的 `409 Conflict` 处理。在原子对象捕获开始前完成判定的策略与目标冲突不会提交工作区、索引、版本、分享、收藏、回收站或活动变更；调用方应刷新列表、重新取得删除意图并再次确认。

策略、目标令牌或 WebDAV 条件通过后，服务端以禁止覆盖的原子重命名把当前对象捕获到源端同一父目录下的随机暂存路径，并从该路径继续处理。回收站副本在提交前会按路径、类型、大小、权限、对象身份和内容摘要建立并复核完整清单。最终物理清理会把已验证暂存对象移入权限不宽于 `0700` 的随机隔离目录，并通过服务端持有的目录句柄逐项删除。原逻辑路径在此期间出现的新对象不会被复制、覆盖或删除。

逻辑提交前无法安全回滚时，REST 返回 `500 INTERNAL_ERROR`，不写入删除活动；内部暂存路径、回收站元数据或配对副本可能为恢复处理而保留，具体主机路径不会写入 API 响应。逻辑删除已经提交但隔离区物理清理未完成时，REST 返回带 `warning=true` 的 `200 OK`，`Warning` 响应头包含 `199 MnemoNAS "delete cleanup incomplete"`，删除活动包含 `cleanup_warning=true`，服务端错误日志记录残留路径。`trash` 模式已经提交目标项目、但后续容量回收未完成时，响应头改为 `199 MnemoNAS "trash delete cleanup incomplete"`，删除活动包含 `trash_cleanup_warning=true`。两类结果均可同时包含适用的持久化警告与 `persistence_warning=true`。只有内容已经移除、最终目录同步失败时，响应只包含持久化警告，不标记清理残留。WebDAV 对已提交的同类结果返回带对应 `Warning` 响应头的 `204 No Content`；未提交的恢复残留返回 `500 Internal Server Error`。

删除意图、删除前复核、跨根复制前后、源暂存树进入隔离区前后和递归移除前都会重新读取主机挂载表。挂载表无法读取、包含非法路径，或目标跨越工作区根目录下的嵌套挂载边界时，流程停止。对象捕获前的冲突返回 `409 Conflict`；捕获后无法安全回滚时返回恢复残留；逻辑提交后返回清理警告。仅在目标边界仍可验证时清理已复制副本，否则保留内部副本以避免跨越新挂载。工作区根目录自身可以是挂载点；该限制只适用于其下的嵌套挂载。

存储写锁只串行化通过 MnemoNAS 执行的操作。同 UID 进程直接修改文件系统、特权进程并发挂载、进程崩溃或断电不在该原子边界内；对象捕获和禁止覆盖回滚可能改变 ctime 或父目录时间戳。服务端不会移动或删除身份未知的替换物，也不会自动清理所有权无法确认的内部暂存或隔离残留。

`trash` 模式下，文件或完整目录树会移入回收站。项目达到 `trashRetentionDays` 对应的持久化到期时间后，由启用的保留清理周期处理；回收站容量不足时，较早项目可能在到期前被永久清理。`permanent` 模式下，项目不会进入回收站且无法从回收站恢复；该模式下删除非空目录返回 `409 Conflict`。

`GET /api/v1/download/{path}` 默认返回文件字节。支持的查询参数：

- `download=true`：最多出现一次，用于强制附件文件名。
- `version=<hash>`：最多出现一次，用于下载历史版本。
- `archive=zip`：最多出现一次，用于将目标路径下载为 ZIP。

ZIP 归档语义：

- 适用于目录和单个文件，不能与 `version` 同时使用。
- 要求目标和所有包含条目具备具体读取权限，不允许只读导航祖先被归档。
- 最多包含 10000 个条目和 20 GiB 文件内容。
- 超过条目数量或内容大小上限返回 `413 Request Entity Too Large`。
- 重复归档条目名称或开始传输前检测到条目快照变化返回 `409 Conflict`。
- 归档条目名称会拒绝路径穿越、绝对路径、反斜杠、冒号和控制字符，以避免跨平台解压歧义。
- 归档附件文件名使用目标路径 basename；根路径使用 `mnemonas-files.zip`，已以 `.zip` 结尾的名称不会重复添加后缀。
- 当前文件和历史版本下载支持 Range 请求；ZIP 归档下载不保证支持 Range。

`POST /api/v1/files/{path}` 要求 `{path}` 指向非根文件路径。根路径或等价根路径上传目标返回 `400 Bad Request` 和 `invalid path`。

`POST /api/v1/directories/{path}` 只创建一个目录，且直接父目录必须已存在。直接父目录不存在时返回 `409 Conflict`，不会创建中间目录。

移动请求：

```json
{
  "from": "/documents/old.txt",
  "to": "/documents/new.txt"
}
```

目标路径不能已存在，也不能保留历史版本元数据。目录移动会检查目标下的后代路径元数据。目标冲突会在配额检查前返回 `409 Conflict`，且不发送配额提醒。

移动或重命名已完成但后续 workspace 持久化失败时，端点仍返回 `200 OK`，并附带 `Warning: 199 MnemoNAS "workspace mutation persistence incomplete"`。响应 body 包含 `data.warning: true` 和 `message: "resource moved with persistence warning"`。

复制请求：

```json
{
  "from": "/documents/report.txt",
  "to": "/archive/report.txt"
}
```

该 REST 端点复制单个文件或目录树。源路径和目标路径必须不同，目标路径不能已存在，目录复制不能以源目录的后代为目标。需要 WebDAV `Overwrite: T/F` 语义时使用 WebDAV `COPY`。

复制已完成但后续 workspace 持久化失败时，端点仍返回 `201 Created`，并附带 `Warning: 199 MnemoNAS "workspace mutation persistence incomplete"`。响应 body 包含 `data.warning: true` 和 `message: "resource copied with persistence warning"`。

其他文件变更也可能在文件操作成功但后续元数据、活动或清理工作未完全完成时，返回成功状态和 `Warning` 响应头。

## 缩略图

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/thumbnails/{path}` | 获取图片或受支持预览的生成缩略图 |

预览和缩略图流程使用下载会话 cookie，因为浏览器媒体元素不能附加 Authorization 头。

`POST /api/v1/auth/download-session` 可由 Web UI 会话 Cookie 或 `Authorization: Bearer <access-token>` 认证。HTTPS 模式设置 `__Host-mnemonas_download_access`，本机 HTTP 模式设置 `mnemonas_download_access`。该 Cookie 为 `HttpOnly`、`SameSite=Strict`；HTTPS 模式使用 `Secure`、`Path=/` 且不设置 `Domain`，HTTP 模式使用 `/api/v1` 路径。缩略图响应是生成图片，并包含 `nosniff` 和 sandbox CSP。

`GET /api/v1/thumbnails/{path}` 接受可选 `size` 查询参数，最多出现一次。支持值为 `small` 或 `s`（150 px）、`medium` 或 `m`（300 px）、`large` 或 `l`（600 px）。省略 `size` 时默认为 `medium`。

缩略图生成会拒绝超过 100 MiB 的源文件、超过 10000x10000 的图片尺寸，或超过 5000 万像素的图片。

## 版本历史

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/versions/{path}` | 列出文件版本 |
| `POST` | `/api/v1/versions/{hash}/restore` | 将版本恢复到指定路径 |

### 恢复版本

将文件恢复到指定历史版本。

**认证**：需要

**权限**：管理员

```text
POST /api/v1/versions/{hash}/restore
```

**查询参数**：

- `path`：文件路径，必填，最多出现一次。

`path` 必须指向非根文件路径。根路径或等价根路径返回 `400 Bad Request` 和 `invalid path`。可复制的 shell 或浏览器示例应对查询值进行 URL 编码，例如 `/documents/report.txt` 对应 `%2Fdocuments%2Freport.txt`。

版本内容已经恢复但最终 workspace 元数据持久化失败时，API 仍返回 `200 OK`，附带 `Warning: 199 MnemoNAS "workspace mutation persistence incomplete"`，响应 `message` 为 `version restored with persistence warning`。

成功恢复会写入一条 `restore` 活动记录，`details.restore_source` 为 `version`，`details.hash` 为被恢复的版本 hash。workspace 持久化 warning 还会包含 `details.persistence_warning="true"`。

请求示例：

```bash
MNEMONAS_ACCESS_TOKEN="<access-token>"
curl_auth_config="$(mktemp)"
trap 'rm -f "$curl_auth_config"' EXIT
chmod 600 "$curl_auth_config"
printf 'header = "Authorization: Bearer %s"\n' "$MNEMONAS_ACCESS_TOKEN" > "$curl_auth_config"

curl -X POST \
  --config "$curl_auth_config" \
  "http://localhost:8080/api/v1/versions/<hash>/restore?path=%2Fdocuments%2Freport.txt"
```

响应示例：

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

## 回收站

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/trash` | 列出回收站项目 |
| `GET` | `/api/v1/trash/{id}` | 获取回收站项目详情 |
| `POST` | `/api/v1/trash/{id}/restore` | 恢复回收站项目 |
| `DELETE` | `/api/v1/trash/{id}` | 永久删除单个项目 |
| `POST` | `/api/v1/trash/empty` | 永久删除已确认的回收站项目 |

回收站可见性遵循当前用户配置的 `home_dir` 边界。

列表中的每个项目包含生命周期内保持不变的随机 `id`，以及持久化的 `expiresAt`。该 RFC 3339 时间在项目进入回收站时确定，之后修改 `retentionDays` 不会改写已有项目的到期时间。列表级 `retentionDays` 只表示新删除项目的当前策略；`trashAutoCleanupEnabled` 表示后台保留清理周期是否启用。容量上限仍可能使较早项目在 `expiresAt` 前被永久清理。

启用保留清理周期时，同一次周期任务会清理过期文件版本和已到达 `expiresAt` 的回收站项目。周期停用时，项目可以通过永久删除端点显式清理。

永久删除回收站项目会在暂存和递归清理前检查回收站根目录下的嵌套挂载。`DELETE /api/v1/trash/{id}` 遇到嵌套挂载或无法验证挂载表时返回 `409 Conflict`，且保留项目内容与元数据。

`POST /api/v1/trash/empty` 只永久删除请求中明确确认的项目。请求体是仅包含 `ids` 的 JSON 对象：

```json
{
  "ids": ["7d29d7827f68f1a3", "4fdd157f624d892b"]
}
```

`ids` 必须包含 1 至 1000 个互不重复的字符串。每个 ID 的 UTF-8 长度必须为 1 至 128 字节，且只能包含 ASCII 字母、数字、`-` 和 `_`。请求体超过 JSON 大小限制时返回 `413 Payload Too Large` 和 `PAYLOAD_TOO_LARGE`。未知字段、尾随 JSON、空数组、重复 ID 或格式不合要求的 ID 返回 `400 Bad Request`，错误代码为 `INVALID_TRASH_SELECTION`，且不会删除项目。

服务端在同一存储写锁内载入当前回收站，并在任何删除发生前，对所有仍存在的已选项目按其恢复逻辑路径完成访问规则预检。任一预检失败都会终止请求，不删除任何已选项目。顶层路径对当前账户不可见时返回 `404 Not Found`；顶层可写但后代被访问规则拒绝时返回 `403 Forbidden`。预检通过后，服务端按请求顺序处理已选 ID；请求发出后新增的项目和所有未选择项目均保持不变，已经不存在的已选 ID 归入 `skipped`。

响应中的 `deleted`、`remaining` 和 `skipped` 按原请求顺序构成输入 ID 的完整、互不重叠分区，三个对应的 `*_count` 字段分别等于数组长度。执行阶段出现硬失败时，尚未处理且仍存在的项目归入 `remaining`；响应仍描述已经完成的删除。仅当 `remaining` 或 `skipped` 非空时，`partial` 才为 `true`。`warning` 始终为布尔值，只表示已提交删除后的物理清理未完全结束，不用于表示选择缺失或执行失败。

```json
{
  "success": true,
  "data": {
    "deleted": ["7d29d7827f68f1a3"],
    "remaining": ["4fdd157f624d892b"],
    "skipped": [],
    "deleted_count": 1,
    "remaining_count": 1,
    "skipped_count": 0,
    "partial": true,
    "warning": false
  },
  "message": "trash selection emptied partially",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

`POST /api/v1/trash/{id}/restore` 默认恢复到原路径。

自定义恢复目标：

- `path` 查询参数最多出现一次，用于恢复到自定义目标路径。
- 自定义目标必须可写，必须是非根路径，直接父目录必须已存在，目标本身不能已存在。
- 根路径或等价根目标返回 `400 Bad Request` 和 `invalid path`。
- 直接父目录不存在时返回 `409 Conflict`，不会创建中间目录。

如果回收站项目包含历史版本，且原路径已被 live 文件占用，或其他回收站项目仍引用重叠的源/目标版本元数据路径，端点会在配额检查前返回 `409 Conflict`，且不发送配额提醒。目录恢复也会检查后代路径的重叠版本元数据。

## 搜索

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/search?q={query}` | 按名称搜索文件 |

搜索结果受配置的 `home_dir` 限制。

查询参数：

- `q`：必填搜索词，最多 100 个字符，必须恰好出现一次。
- `limit`：最大结果数量。默认值为 50，最大值为 100，最多出现一次。

搜索响应：

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
  }
}
```

## 分享链接

认证管理端点：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/api/v1/shares` | 创建分享 |
| `GET` | `/api/v1/shares` | 列出分享 |
| `GET` | `/api/v1/shares/policy` | 获取新建分享的默认策略 |
| `GET` | `/api/v1/shares/{id}` | 获取分享详情 |
| `PUT` | `/api/v1/shares/{id}` | 更新分享 |
| `DELETE` | `/api/v1/shares/{id}` | 删除分享 |

创建请求：

```json
{
  "path": "/documents/report.pdf",
  "type": "file",
  "password": "optional-password",
  "expires_in": "72h",
  "permission": "read",
  "max_access": 100,
  "description": ""
}
```

`GET /api/v1/shares` 列出当前调用方的分享。管理员可将 `all=true` 指定最多一次，用于列出所有用户的分享。

`GET /api/v1/shares/policy` 返回 `default_expires_in`、`default_max_access` 和 `policy_rules` 条目，规则字段包括 `path`、`require_password`、`max_expires_in`、`max_access`、`allowed_users`、`allowed_groups` 和 `allowed_roles`。

创建分享字段规则：

- `type` 为 `file` 或 `folder`；省略时默认为 `file`。
- `permission` 当前接受 `read` 或省略值。
- `password` 可选；非空分享密码最长 72 字节。
- 省略 `expires_in` 或 `max_access` 时，服务端应用 `share.default_expires_in` 和 `share.default_max_access`。
- 路径匹配 `share.policy_rules` 时，最具体路径规则生效。
- `require_password` 会拒绝无密码请求，`max_expires_in` 和 `max_access` 会限制超过规则上限的值。
- `allowed_users`、`allowed_groups` 或 `allowed_roles` 非空时，非管理员调用方必须匹配其中一个用户、用户组或角色；不匹配时返回 `403 Forbidden` 和 `SHARE_POLICY_PRINCIPAL_FORBIDDEN`。

认证分享响应包含 `risk.level`（`none`、`low`、`medium`、`high`）和可选原因对象。

风险原因用于标识无密码、长期、宽范围文件夹、无限制、长期未访问或即将过期的链接。已启用分享在创建后 30 天从未被访问会报告为 `unused_enabled`；已启用分享的最近访问超过 90 天会报告为 `stale_enabled`。

分享过期提醒：

- 启用 `[alerts] enabled = true` 且至少配置一个提醒通道时，服务端每小时扫描 72 小时内过期的已启用分享。
- 扫描会发送聚合的 `share_expiring_soon` warning 事件。
- 同一进程生命周期内，相同分享过期时间只提醒一次。
- 事件 `details` 包含 `source = "share"`、分享数量、扫描窗口、最早过期时间、文件/文件夹分享数量、无密码分享数量和无限访问分享数量。
- 事件不包含分享路径、分享 URL、访问密码或分享 ID。

更新请求：

```json
{
  "enabled": true,
  "password": "optional-password",
  "expires_in": "",
  "permission": "read",
  "max_access": 0,
  "description": ""
}
```

分享更新规则：

- 所有更新字段均可选；省略字段通常保留当前值。
- 空 `password` 清除密码，空 `expires_in` 清除过期时间，`permission` 当前只接受 `read`。
- 匹配 `share.policy_rules` 的分享更新也必须满足路径规则。
- `require_password` 会拒绝让匹配分享保持无密码的更新。
- `max_expires_in` 和 `max_access` 会限制清除或超过配置上限的显式值。
- 当已存分享没有相应限制或超过路径规则时，省略字段也会被上限限制。
- `allowed_users`、`allowed_groups` 或 `allowed_roles` 非空时，非管理员调用方必须匹配其中一个用户、用户组或角色；不匹配时返回 `403 Forbidden` 和 `SHARE_POLICY_PRINCIPAL_FORBIDDEN`。

公开端点：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/public/shares/{share_id}` | 获取公开分享元数据 |
| `POST` | `/api/v1/public/shares/{share_id}/access` | 提交密码并获得分享 cookie |
| `GET` | `/api/v1/public/shares/{share_id}/download` | 下载分享文件或分享文件夹 ZIP |
| `GET` | `/api/v1/public/shares/{share_id}/items?path=subdir` | 列出分享目录条目 |
| `GET` | `/api/v1/public/shares/{share_id}/download/{path}` | 下载分享目录中的条目或 ZIP |

密码保护分享在密码验证成功后使用 `HttpOnly`、`SameSite=Strict` cookie。密码失败会被限流。

公开分享行为：

- 无有效访问 cookie 的密码保护分享只返回 `id`、`type`、`has_password` 和 `permission`，不返回 `description` 或文件/文件夹元数据。
- 公开分享，以及带有效访问 cookie 的密码保护分享，会按情况返回 `description`、`file_name`、`file_size` 或 `folder_items` 元数据。
- 根文件夹公开分享的 `file_name` 使用稳定展示名 `mnemonas-share`，而不是 `/`。
- 已授权的零字节文件返回 `file_size: 0`；已授权的空文件夹返回 `folder_items: 0`。
- `max_access > 0` 且 `access_count` 已达到上限时，公开访问返回 `410 Gone` 和 `SHARE_ACCESS_LIMIT_REACHED`。
- 当前时间达到或超过 `expires_at` 后，分享视为过期，并返回 `410 Gone` 和 `SHARE_EXPIRED`。
- 禁用分享返回 `410 Gone` 和 `SHARE_DISABLED`。
- 由已禁用或已删除所有者创建的分享，在公开元数据、下载和文件夹列表请求中返回 `404 Not Found` 和 `SHARE_NOT_FOUND`。
- 下载和文件夹列表请求会递增 `access_count`。通过 `POST /api/v1/public/shares/{share_id}/access` 或兼容路径 `POST /s/{share_id}` 验证密码不会递增。
- `items?path=` 和 `download/{path}` 中的子路径相对于分享文件夹根目录。
  文件夹列表 `path` 查询参数最多出现一次。
  控制字符和独立的 `.` 或 `..` 路径段无效，合法名称中的连续点号（例如 `foo..txt`）仍有效。
  无效子路径不递增 `access_count`。
- 文件夹列表响应中的 `path` 和 `items[].path` 为相对于分享文件夹根目录的规范路径，不以 `/` 开头；根文件夹响应使用空 `path`。响应只包含对分享所有者仍可见的直接子项。
- 下载或文件夹列表响应一旦开始写入客户端，该请求即会计数，即使后续流式传输失败。
- 后端文件 reader 支持 seek 时，公开分享下载支持 HTTP Range 请求。
  MnemoNAS 本地存储支持该路径，用于断点续传和浏览器媒体播放。
  Range 响应只有在实际返回至少一个内容字节时才递增 `access_count`；零字节文件的普通完整下载仍会计数。
- 在公开下载端点上设置 `archive=zip` 最多一次，可将分享文件夹根、子文件夹或文件下载为 ZIP。
  公开 ZIP 归档返回 `application/zip`，不保证支持 Range，会跳过对分享所有者不再可见的条目，并限制为最多 10000 个条目和 20 GiB 文件内容。
  超过条目数量或内容大小上限返回 `413 Request Entity Too Large` 和归档错误码；重复归档条目名称或开始传输前检测到条目快照变化返回 `409 Conflict` 和归档错误码。
  归档条目名称会拒绝路径穿越、绝对路径、反斜杠、冒号和控制字符，以避免跨平台解压歧义。
  归档附件文件名使用被归档目标名称；分享根路径为 `/` 时使用 `mnemonas-share.zip`，已以 `.zip` 结尾的名称不会重复添加后缀。
- 返回 `416 Requested Range Not Satisfiable` 的不可满足 Range 请求，以及 `bytes=-0` 等零长度 Range 请求，不递增 `access_count`。
- 成功密码验证会设置 `HttpOnly`、`SameSite=Strict` 访问 cookie；后续下载和文件夹列表请求使用 cookie，而不是密码查询参数。
- 公开分享元数据、密码验证响应、文件夹列表响应和公开下载 JSON 错误响应包含 `Cache-Control: private, no-cache`、`Vary: Cookie`、`X-Content-Type-Options: nosniff` 和 `Referrer-Policy: no-referrer`。
- 重复密码失败返回 `429 Too Many Requests` 和 `SHARE_PASSWORD_RATE_LIMITED`。
- 密码失败限流按分享 ID 和客户端地址分桶。默认忽略转发头；仅当 `server.trusted_proxy_hops > 0` 且直连来源为 loopback 或属于 `server.trusted_proxy_cidrs` 时，才使用转发头。
- 兼容路径 `GET /s/{share_id}` 和 `POST /s/{share_id}` 提供相同公开 JSON 行为，适合脚本或非 SPA 直接调用。
- 兼容路径 `GET /s/{share_id}/items`、`GET /s/{share_id}/download` 和 `GET /s/{share_id}/download/{path}` 提供相同文件夹列表和下载行为，适合脚本或非 SPA 直接调用。

## 收藏

收藏路径必须归一化为非根绝对路径：

- 空值和根路径返回 `400 Bad Request` 和 `MISSING_PATH`。
- 包含独立 `.` 或 `..` 路径段的值返回 `400 Bad Request` 和 `INVALID_PATH`。
- 单路径检查端点的 `path` 查询参数最多出现一次。
- `path` 查询值在可复制 URL 中应进行 URL 编码，例如 `/documents/file.pdf` 对应 `%2Fdocuments%2Ffile.pdf`。
- 该校验先于非管理员 `home_dir` 授权。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/favorites` | 列出收藏 |
| `POST` | `/api/v1/favorites` | 添加收藏 |
| `GET` | `/api/v1/favorites/check?path=%2Fdocuments%2Ffile.pdf` | 检查单个路径 |
| `POST` | `/api/v1/favorites/check-batch` | 检查多个路径 |
| `DELETE` | `/api/v1/favorites/{path}` | 移除收藏 |
| `PATCH` | `/api/v1/favorites/{path}` | 更新备注 |

列表响应：

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

添加请求：

```json
{
  "path": "/documents/report.pdf",
  "note": "quarterly report"
}
```

添加响应：

```json
{
  "success": true,
  "data": {
    "path": "/documents/report.pdf",
    "user_id": "user-123",
    "created_at": "2024-01-15T10:00:00Z",
    "note": "quarterly report"
  }
}
```

检查响应：

```json
{
  "success": true,
  "data": {
    "path": "/documents/file.pdf",
    "is_favorite": true
  }
}
```

批量检查请求：

```json
{
  "paths": ["/file1.txt", "/file2.pdf"]
}
```

批量检查响应：

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

对 `DELETE /api/v1/favorites/{path}` 和 `PATCH /api/v1/favorites/{path}`，`{path}` 按路径段 URL 编码，同时保留 `/` 分隔符。成功移除和备注更新响应包含 `success: true`、`data: null` 和状态消息。

移除响应：

```json
{
  "success": true,
  "data": null,
  "message": "favorite removed successfully"
}
```

备注更新响应：

```json
{
  "success": true,
  "data": null,
  "message": "favorite note updated successfully"
}
```

## 活动日志

### 列出活动

返回用户活动条目。

说明：

- 启用认证时，管理员可以查看完整活动日志。非管理员用户只接收当前账户可见的条目，`user` 查询参数不能越过该范围。
- 系统事件也会写入活动日志，包括周期性的 `disk_health` 检查。
- 手动和定时 Scrub 运行会写入 `scrub` 活动条目。
  Scrub 失败、对象校验问题和结果持久化不完整会通过已配置的 Webhook、Telegram、WeCom、DingTalk 或 SMTP 提醒通道发送 `scrub_run` 事件。
  提醒详情使用计数、状态、公开错误类型和公开消息，不包含对象 hash 或底层错误文本。
- 创建、删除和启用状态更新会写入 `share` 或 `unshare` 活动。`details` 包含分享类型、权限、密码要求、过期时间和访问上限等复核元数据；启用状态更新还包含 `enabled` 和 `previous_enabled`。这些详情不包含分享密码、公开 URL 或分享 ID。
- 版本恢复会写入 `restore` 活动，`details.restore_source="version"` 表示来源为版本历史，`details.hash` 为被恢复版本 hash。
- 未配置活动日志时，API 返回空列表。
- 活动日志已配置但初始化失败或当前不可用时，API 返回 `503 Service Unavailable`。

```text
GET /api/v1/activity
```

查询参数：

每个参数最多出现一次。

- `limit`：结果数量。默认值为 50，最大值为 500。
- `offset`：分页偏移。
- `action`：按动作类型筛选。
  当前值包括 `upload`、`download`、`delete`、`rename`、`move`、`copy`、`create`、`restore`、`share`、`unshare`、`favorite`、`unfavorite`、`favorite_note_update`、`login`、`logout`、`trash_restore`、`trash_delete`、`trash_empty`、`disk_health` 和 `scrub`。
- `action_group`：按复核分组筛选。当前值为 `share`（share/unshare 事件）和 `risk`（delete、move、rename、版本恢复、回收站恢复、share、unshare、永久删除回收站项目和清空回收站事件）。
- `path`：按路径或目录筛选。筛选会匹配路径本身、后代路径，以及 `from`、`to` 等路径型活动详情。
- `user`：按用户筛选。
- `since`：返回此 RFC3339 时间戳或之后的条目。
- `until`：返回此 RFC3339 时间戳或之前的条目。

`action` 和 `action_group` 可以组合，结果取交集。`path` 使用 MnemoNAS 绝对路径规则归一化，包含遍历路径段时返回 `400 Bad Request`。无效 `action` 或 `action_group`、无效时间格式，或 `since` 晚于 `until`，都会返回 `400 Bad Request`。

对非管理员用户，`path=/` 表示当前账户可见范围。范围外的 `path` 筛选返回空列表，且不会泄露隐藏活动详情中的匹配记录。

响应示例：

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

说明：

- 启用认证时，管理员接收全局统计。非管理员用户接收当前账户可见活动条目的统计。
- 统计端点支持与列表端点相同的 `action`、`action_group`、`path`、`user`、`since` 和 `until` 查询参数。存在筛选条件时，`total`、`today`、`by_action`、`by_user` 和 `risk_summary` 都基于筛选后的记录计算。
- `risk_summary` 汇总高风险动作，包括 delete、move、rename、share、unshare、永久删除回收站项目和清空回收站。`max_10m` 是任意 10 分钟窗口内匹配高风险动作的最大数量，`max_10m_started_at` 和 `max_10m_ended_at` 标识该窗口，便于聚焦复核。
- 未配置活动日志时，API 返回零统计。
- 活动日志已配置但初始化失败或当前不可用时，API 返回 `503 Service Unavailable`。

```text
GET /api/v1/activity/stats
```

查询参数：

每个参数最多出现一次。

- `action`：按动作类型筛选，使用与列表端点相同的值。
- `action_group`：按复核分组筛选。当前值为 `share` 和 `risk`。
- `path`：按路径或目录筛选。筛选会匹配路径本身、后代路径，以及 `from`、`to` 等路径型活动详情。
- `user`：按用户筛选。
- `since`：统计此 RFC3339 时间戳或之后的条目。
- `until`：统计此 RFC3339 时间戳或之前的条目。

`action`、`action_group`、`path`、`user`、`since` 和 `until` 使用与列表端点相同的错误处理。

对非管理员用户，`path=/` 只统计当前账户可见范围内的记录。不可访问的 `path` 筛选返回零统计，且不会统计只由隐藏活动详情匹配的记录。

响应示例：

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

返回已持久化的活动复核处置记录。版本历史和回收站页面会把匹配的 `restore` 或 `trash_restore` 活动写入 `restored` 处置记录；分享复核记录可包含 `share_disposition_details`，用于保存脱敏的分享处置线索。

```text
GET /api/v1/activity/reviews
```

查询参数：

- `limit`：结果数量。默认值为 20，最大值为 100。
- `offset`：分页偏移。
- `reviewer`：按复核人筛选。
- `activity_entry_id`：仅返回关联到指定活动条目 ID 的复核记录。
- `disposition_status`：按处置状态筛选。允许值为 `documented`、`confirmed`、`restored`、`disabled` 和 `needs_follow_up`。
- `action_group`：按复核记录包含的操作分组筛选。允许值为 `share` 和 `risk`。
- `since`：返回此 RFC3339 时间戳或之后的复核记录。
- `until`：返回此 RFC3339 时间戳或之前的复核记录。

无效时间格式、`since` 晚于 `until`、非规范 `activity_entry_id`、不支持的 `disposition_status` 或不支持的 `action_group` 返回 `400 Bad Request`。

响应示例：

```json
{
  "success": true,
  "data": {
    "items": [
      {
        "id": "review-123",
        "reviewed_at": "2024-01-15T10:05:00Z",
        "reviewer": "admin",
        "note": "Deleted files were confirmed restored from trash",
        "scope_label": "concentrated window",
        "filter_summary": "group risk changes",
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

记录一次活动复核处置。服务端使用当前认证账户作为 `reviewer`，并设置 `reviewed_at`。

```text
POST /api/v1/activity/reviews
```

请求体：

```json
{
  "note": "Deleted files were confirmed restored from trash",
  "scope_label": "current page",
  "filter_summary": "group risk changes",
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

说明：

- `note`、`scope_label` 和 `activity_entry_ids` 必填。`review_count` 必须大于零，`total_count` 不能小于 `review_count`。
- `note`、`scope_label`、`filter_summary` 和 `user_samples` 不能包含控制字符；服务端生成的 `reviewer` 也使用同一文本约束。
- `disposition_status` 可选，默认值为 `documented`。允许值为 `documented`、`confirmed`、`restored`、`disabled` 和 `needs_follow_up`。
- `action_counts` 可选。键必须是已知活动动作类型，值必须是正整数，且总和必须等于 `review_count`。
- `path_samples` 和 `user_samples` 可选，各最多 10 项。路径使用与活动条目相同的逻辑路径规则归一化，重复样本会被拒绝。
- `share_disposition_details` 可选，最多 10 项。每项可包含 `path`、`type`（`file` 或 `folder`）、`enabled`、`risk_level`（`none`、`low`、`medium`、`high`）、`reason_summary`、`suggested_action`、`access_summary` 和 `expires_at`；该字段用于脱敏记录分享风险与处置建议，不包含分享 ID、URL 或密码。
- 活动日志未配置、初始化失败或当前不可用时，API 返回 `503 Service Unavailable`。

### 更新活动复核记录处置状态（管理员）

更新已持久化活动复核记录的当前处置状态，并可替换处置备注。

服务端将 `reviewer` 替换为当前认证账户，并将 `reviewed_at` 更新为状态写回时间；省略 `note` 时保留之前的备注。样本、计数和关联活动条目保持不变。

```text
PATCH /api/v1/activity/reviews/{id}
```

请求体：

```json
{
  "disposition_status": "disabled",
  "note": "The share link was disabled and the access entry point was verified"
}
```

说明：

- `disposition_status` 必填。允许值为 `documented`、`confirmed`、`restored`、`disabled` 和 `needs_follow_up`。
- `note` 可选。提供时必须是非空文本，不能包含控制字符；服务端会裁剪首尾空白，并应用活动复核备注长度限制。
- 非规范 `{id}` 或不支持的 `disposition_status` 返回 `400 Bad Request`。
- 复核记录不存在时返回 `404 Not Found`。
- 活动日志未配置、初始化失败或当前不可用时，API 返回 `503 Service Unavailable`。

### 清空活动日志（管理员）

```text
DELETE /api/v1/activity
```

响应示例：

```json
{
  "success": true,
  "data": {
    "message": "Activity log cleared"
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

说明：

- 活动日志已配置但初始化失败或当前不可用时，API 返回 `503 Service Unavailable`，而不是报告清空成功。

## 设置

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/settings` | 获取当前设置 |
| `POST` | `/api/v1/settings/access-check` | 检查用户和路径的有效读写权限 |
| `POST` | `/api/v1/settings/access-preview` | 使用未保存目录规则预览读写权限矩阵 |
| `POST` | `/api/v1/settings/access-report` | 为一个路径生成所有用户的读写权限矩阵 |
| `GET` | `/api/v1/settings/access-reviews` | 列出最近目录权限复核记录 |
| `POST` | `/api/v1/settings/access-reviews` | 持久化一条目录权限复核记录 |
| `DELETE` | `/api/v1/settings/access-reviews` | 清空目录权限复核记录 |
| `POST` | `/api/v1/settings/alerts/test` | 通过已保存提醒通道发送测试提醒 |
| `GET` | `/api/v1/settings/security-check` | 运行公网访问安全自检 |
| `PUT` | `/api/v1/settings` | 更新设置 |
| `GET` | `/api/v1/settings/webdav-credentials` | 获取当前 WebDAV 凭据状态 |

设置更新可在运行时改变以下配置：

- 目录配额、目录访问规则、WebDAV prefix、只读模式、认证模式、分享配置、收藏配置和保留/版本策略。
- Web UI 认证 token 生命周期。`auth.access_token_ttl` 必须是不少于 `30s` 的 Go duration 字符串，`auth.refresh_token_ttl` 必须是正 Go duration 字符串。新的登录会话使用更新后的 refresh 生命周期；现有会话刷新时使用当前 access 生命周期，但保留登录时确定的会话绝对到期时间。已签发 token 保持原到期时间。
- Webhook、Telegram、WeCom、DingTalk 和 SMTP 邮件提醒设置。
- 磁盘健康温度阈值和介质磨损阈值。
- 定时 Scrub 维护。更新会立即替换运行中的后台调度器。
- 数据平面连接设置。服务监听/TLS 变更和 CDC chunk-size 变更会保存，但需要重启对应服务后生效。

目录配额和访问规则更新会热应用到 Web/API 和 WebDAV 运行时。

路径字段规则：

- `storage.directory_quotas`、`storage.directory_access_rules` 和 `share.policy_rules` 中的 `path` 字段使用相同的 MnemoNAS 逻辑路径规则。
- 路径必须以 `/` 开头，不能包含 Windows 或 UNC 语法、反斜杠、查询或片段字符、控制字符，或 `.`/`..` 路径段。
- Settings API 会裁剪首尾空白，并归一化重复和末尾斜杠；包含 `.` 或 `..` 的路径不会被折叠，而是直接拒绝。
- Web 设置页的目录配额行式输入会用双引号包裹包含空格或双引号的路径；路径内双引号写作 `\"`，例如 `"/Family Photos" 500 GB`。
- 目录权限和分享路径策略使用结构化路径输入框，路径含空格或双引号时直接填写路径文本，不需要手动加引号。

Web 设置页会基于当前未保存草稿生成分享策略覆盖摘要。摘要展示默认过期时间、默认访问上限、路径策略数量、要求密码的路径数量、限制创建/维护者范围的路径数量、宽松默认值或路径策略的注意项，以及根路径规则、未继承上级限制的最具体路径规则、子路径放宽上级有效期/访问次数/允许创建者范围的规则和等价重复规则等整理建议。

该摘要仅用于保存前复核；实际强制行为仍来自 Settings API 保存成功后的服务端策略。

设置保存成功后，如果 `storage.directory_access_rules` 或分享策略字段实际发生变化，服务端会向提醒运行时提交 `settings_policy_changed` warning 事件。

触发字段包括 `share.enabled`、`share.default_expires_in`、`share.default_max_access` 和 `share.policy_rules`。

事件 `details` 包含 `source = "settings"`、`changed_sections`、目录访问和分享策略字段是否变化的布尔值，以及规则数量。事件不包含规则路径、`share.base_url`、提醒通道 secret 或用户/成员详情。

归一化后无变化的提交不会发送该事件。提醒投递失败会写日志，但不导致设置保存失败。

### 设置验证规则

`PUT /api/v1/settings` 会按字段组执行服务端校验。无效设置返回 `400 Bad Request`，且不会改变运行中配置。

- 服务器监听：`server.host` 必须为空、`*`、合法主机名、IPv4 或 IPv6 字面量，不能包含端口、空白或控制字符；端口通过 `server.port` 设置。
- 反向代理：`server.trusted_proxy_hops` 控制是否信任反向代理提供的转发头，用于评估 HTTPS 请求语义。`server.trusted_proxy_cidrs` 列出可提供这些转发头的非 loopback 代理 IP 或 CIDR。
- Web UI 认证：`auth.access_token_ttl` 必须不少于 `30s`，`auth.refresh_token_ttl` 必须为正值。公网部署建议 `auth.access_token_ttl <= 1h`、`auth.refresh_token_ttl <= 720h`。
- 存储规则：`storage.root` 通过 Settings API 只读。`storage.directory_quotas` 接受 MnemoNAS 逻辑路径和正 `quota_bytes`。
  `storage.directory_access_rules` 接受 MnemoNAS 逻辑路径，以及针对 `*_users`、`*_groups` 和 `*_roles` 的读写授权；最具体匹配规则生效，写授权同时允许读取。
- WebDAV 认证：`webdav.auth_type` 支持 `users`、`basic` 和 `none`；空值归一化为 `basic`，`users` 要求应用认证保持启用。
- WebDAV prefix：`webdav.prefix` 会归一化为以 `/` 开头的 URL path，不能包含反斜杠、`?`、`#` 或控制字符，启用时不能与 `/`、`/api`、`/s` 或 `/health` 重叠。
- WebDAV 密码：省略 `webdav.password` 会保留现有 WebDAV 密码；提交空字符串会切回 `secrets.json` 中生成的 Basic Auth 密码。
- URL 字段：非空 `share.base_url`、`alerts.webhook_url`、`alerts.wecom_webhook_url` 和 `alerts.dingtalk_webhook_url` 必须是绝对 `http` 或 `https` URL，并使用合法主机名或 IP 地址。
  `share.base_url` 还不能包含 userinfo、查询字符串、片段、编码后的查询或片段标记、反斜杠、重复路径斜杠或 `.`/`..` 路径段。
- 分享策略：`share.default_expires_in` 必须为空、`0` 或非负 Go duration 字符串；`share.default_max_access` 必须大于或等于零。
  `share.policy_rules` 条目必须使用 MnemoNAS 逻辑路径，并设置 `require_password`、`max_expires_in`、`max_access`、`allowed_users`、`allowed_groups` 或 `allowed_roles` 中至少一个字段。允许范围字段会被裁剪、去重并归一化为小写；角色只接受 `admin`、`user` 或 `guest`。
- Alert Webhook：`webhook_method` 支持 `GET` 和 `POST`。自定义 webhook header 使用 `"Key: Value"` 字符串，header 名必须是合法 HTTP token 且大小写不敏感地唯一，值不能包含换行或控制字符。Webhook、Telegram、WeCom 和 DingTalk 出站请求不跟随 HTTP 重定向；`3xx` 响应会作为投递失败处理。
- 存储容量提醒：`storage_alert` 投递保留容量指标和 `path_scope = "configured_storage_root"`，但将 `path` 设为 `<omitted>`，文本通道不包含原始存储根目录路径。
- Secret 响应：`GET /api/v1/settings` 不返回 Webhook URL/header、WeCom webhook URL 或 DingTalk webhook URL。
  `alerts.webhook_url`、`alerts.webhook_headers`、`alerts.wecom_webhook_url` 和 `alerts.dingtalk_webhook_url` 对已配置值使用 `<redacted>` 占位符，并通过 `*_configured` 布尔值表示是否存在。
- Secret 更新：`PUT /api/v1/settings` 可以提交真实 Webhook URL/header、WeCom webhook URL 和 DingTalk webhook URL 来更新配置；提交相同的 `<redacted>` 占位符会保留对应现有值。
  省略 `alerts.telegram_bot_token` 或 `alerts.smtp_password` 会保留已存 secret；提交空字符串会清除对应 secret。
- Telegram：`alerts.telegram_enabled` 仍为 true 时，清空 `alerts.telegram_bot_token` 无效。
  启用 `alerts.telegram_enabled` 时，`telegram_bot_token` 和 `telegram_chat_id` 必填；bot token 不能包含空白、`/`、`?` 或 `#`，且永远不会由 settings 或 diagnostics 响应返回。
- WeCom 和 DingTalk：启用 `alerts.wecom_enabled` 时，`wecom_webhook_url` 必填且永远不会由 settings 或 diagnostics 响应返回。启用 `alerts.dingtalk_enabled` 时，`dingtalk_webhook_url` 必填且永远不会由 settings 或 diagnostics 响应返回。
- 邮件提醒：启用 `alerts.email_enabled` 时，`smtp_host`、`smtp_from` 和至少一个 `smtp_to` 收件人必填；`smtp_port` 必须为 1-65535，发件人和收件人必须是合法邮箱地址。
- 磁盘健康：`disk_health.command` 必须是单个可执行文件名或绝对路径，`disk_health.media_wear_critical_percent` 不能低于 `disk_health.media_wear_warning_percent`，每个 `disk_health.devices[].path` 必须是绝对路径。
- 维护：`maintenance.scrub.schedule_interval` 和 `maintenance.scrub.retry_interval` 必须是正 duration 字符串，`maintenance.scrub.max_retries` 必须大于或等于零。
- 数据面：`dataplane.grpc_address` 必须是合法 `host:port` 地址，端口在 1-65535 范围内，且不含空白或控制字符。
  CDC chunk size 必须满足 `65536 <= min_chunk_size < avg_chunk_size < max_chunk_size <= 67108864`。

设置响应或请求片段示例（展示字段形状；`storage.root` 通过 Settings API 只读）：

```json
{
  "server": {
    "tls": {
      "cert_dir": "/srv/mnemonas/.mnemonas/certs"
    }
  },
  "storage": {
    "root": "/srv/mnemonas"
  }
}
```

### 发送测试提醒

```text
POST /api/v1/settings/alerts/test
```

**需要管理员访问**

该端点通过当前已保存的提醒通道发送一个 `alert_test` warning 事件。调用要求如下：

- `[alerts] enabled = true`。
- 至少配置一个 Webhook、Telegram、WeCom、DingTalk 或 SMTP 邮件通道。
- 提醒运行时可用。

WeCom 和 DingTalk 通道仅在通道启用且 webhook URL 非空时视为已配置。SMTP 邮件通道仅在启用邮件提醒，且 SMTP host、port、sender 和至少一个非空 recipient 都存在时视为已配置。

测试事件详情只包含 `trigger = "manual_test"`、`source = "settings"` 和通道列表；不包含 Webhook、Telegram、WeCom、DingTalk 或 SMTP secret。

响应示例：

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

提醒禁用或缺少通道时返回 `409 Conflict`；提醒运行时不可用时返回 `503 Service Unavailable`；投递失败时返回通用 `500` 错误，不暴露通道 secret。

`POST /api/v1/settings/access-check` 接受 `{"username":"alice","path":"/team/report.pdf"}`，并返回 read/write 决策。

每个决策包含 `allowed`、`source`、可选 `message`，以及由目录访问规则决定结果时的 `matched_rule`。

`source` 可为 `admin`、`home_dir`、`directory_access_rule`、`invalid_home_dir`、`user_disabled`、`user_not_found` 或 `auth_disabled`。

嵌套目录授权仅在后代目录当前存在时允许只读导航祖先；此时 `matched_rule` 指向该后代规则。

Access-check 响应：

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

`POST /api/v1/settings/access-report` 接受 `{"path":"/team/report.pdf"}`，为每个用户返回相同 read/write 决策。

响应包含 `summary`，其中包括用户数量、允许/拒绝读取数量、允许/拒绝写入数量和相关分享数量。

可选 `rule_effects` 列表按目录授权规则汇总本次检查中实际命中的读写允许/拒绝数量。`index` 是已保存或提交的 `directory_access_rules` 数组中的零基序号，`user_samples` 包含用于定位影响范围的用户样例。

可选 `shares` 列表报告与路径精确匹配、覆盖该路径的父文件夹分享，以及被检查目录下的子分享。该端点用于管理员在修改共享目录或分享规则前检查权限影响。

Access-report 响应：

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
    "rule_effects": [
      {
        "path": "/team",
        "index": 0,
        "read_allowed": 1,
        "read_denied": 1,
        "write_allowed": 1,
        "write_denied": 1,
        "user_samples": ["alice", "bob"]
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

`POST /api/v1/settings/access-preview` 接受 `{"path":"/team/report.pdf","directory_access_rules":[...]}`，只使用提交的未保存规则返回相同用户矩阵和相关分享影响。

该端点不持久化设置，并返回 `preview: true`。嵌套目录授权同样仅在后代目录当前存在时按只读导航祖先评估，因此可在保存家庭或小团队共享目录规则前预览实际影响。

Access-preview 响应：

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
    ],
    "rule_effects": [
      {
        "path": "/team",
        "index": 0,
        "read_allowed": 1,
        "read_denied": 0,
        "write_allowed": 0,
        "write_denied": 1,
        "user_samples": ["alice"]
      }
    ]
  }
}
```

### 目录权限复核历史

`GET /api/v1/settings/access-reviews` 返回最近目录权限复核记录，支持 `limit` 和 `offset` 查询参数。`limit` 范围为 1-100，默认 20。

`POST /api/v1/settings/access-reviews` 接受 Settings 页面生成的目录权限矩阵或未保存规则预览摘要。服务端使用当前认证账户作为 `reviewer`，设置 `reviewed_at`，并持久化最多最近 100 条记录。相同复核人、路径、标题和预览标记的记录会由新记录替换旧记录。

`DELETE /api/v1/settings/access-reviews` 清空已持久化的目录权限复核记录。

创建请求示例：

```json
{
  "title": "用户矩阵",
  "path": "/team/report.pdf",
  "preview": false,
  "users": 2,
  "read_allowed": 1,
  "read_denied": 1,
  "write_allowed": 1,
  "write_denied": 1,
  "related_shares": 1,
  "active_related_shares": 1,
  "password_protected_shares": 1,
  "report_text": "目录权限复核记录\n路径: /team/report.pdf"
}
```

列表响应示例：

```json
{
  "success": true,
  "data": {
    "items": [
      {
        "id": "review-id",
        "reviewed_at": "2026-06-20T08:30:00Z",
        "reviewer": "admin",
        "title": "用户矩阵",
        "path": "/team/report.pdf",
        "preview": false,
        "users": 2,
        "read_allowed": 1,
        "read_denied": 1,
        "write_allowed": 1,
        "write_denied": 1,
        "related_shares": 1,
        "active_related_shares": 1,
        "password_protected_shares": 1,
        "report_text": "目录权限复核记录\n路径: /team/report.pdf"
      }
    ],
    "total": 1,
    "limit": 20,
    "offset": 0
  }
}
```

WebDAV 凭据响应：

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

`password` 字段只在运行中的 WebDAV 服务使用自动生成的 Basic Auth 密码时出现。自定义 WebDAV 密码不会返回。

### 公网访问安全自检

`GET /api/v1/settings/security-check` 需要管理员访问。该端点返回 Web UI 安全自检使用的运行时检查，也可由部署自动化消费：

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
        "title": "Current request is not HTTPS",
        "message": "Public access should use built-in TLS or a trusted HTTPS reverse proxy.",
        "details": {
          "direct_tls": false,
          "forwarded_proto": "",
          "trusted_forwarded_source": true
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

`data.status` 和 `checks[].status` 使用 `pass`、`warning` 或 `block`。聚合状态中 `block` 优先于 `warning`，`warning` 优先于 `pass`。

当前检查项 ID 包括
`auth_enabled`、`session_token_ttl`、`login_rate_limit`、`browser_session_boundary`、`public_share_boundary`、`unsafe_no_auth_override`、
`config_file_access`、`secrets_file_access`、`users_file_access`、`https_request`、`public_http_exposure`、`trusted_proxy_or_tls`、
`forwarded_proto_trust`、`server_listen`、`admin_accounts`、`dataplane_listen`、`dataplane_http_listen`、`webdav_prefix`、`webdav_auth`、
`smb_preview`、`share_base_url`、`share_default_policy`、`backup_local_destinations` 和 `initial_password_file`。

按范围分组如下：

- 认证与会话：`auth_enabled`、`session_token_ttl`、`login_rate_limit`、`browser_session_boundary`、`unsafe_no_auth_override`、`admin_accounts`、`initial_password_file`。
- 公网入口与代理：`https_request`、`public_http_exposure`、`trusted_proxy_or_tls`、`forwarded_proto_trust`、`server_listen`。
- 运行时文件权限：`config_file_access`、`secrets_file_access`、`users_file_access`。
- 数据面和协议入口：`dataplane_listen`、`dataplane_http_listen`、`webdav_prefix`、`webdav_auth`、`smb_preview`。
- 分享和备份策略：`public_share_boundary`、`share_base_url`、`share_default_policy`、`backup_local_destinations`。

主要检查语义如下：

- `session_token_ttl` 检查 Web UI access-token 和 refresh-token 生命周期。
  公网部署建议 `auth.access_token_ttl <= 1h`、`auth.refresh_token_ttl <= 720h`，更长值报告为 `warning`。
  详情只包含 TTL 文本、秒数和长 TTL 布尔值，不包含 token 内容。
- `login_rate_limit` 检查 Web UI 连续登录失败节流。
  启用认证时，每个客户端 IP 在 10 秒内最多执行 12 次凭据检查；连续失败另按用户名和客户端 IP 计数，达到阈值后应用短期锁定，并发送 `login_rate_limited` 提醒事件。
  详情包含 `credential_check_limit=12`、`credential_check_window=10s`、`credential_check_window_seconds=10`、`credential_check_scope=client_ip` 以及失败阈值、计数窗口、锁定时长、提醒冷却和 key scope，不包含用户名、密码或 token。
- `browser_session_boundary` 检查当前浏览器访问路径是否会为 Web UI 会话 cookie 和下载 cookie 设置 `Secure`，并确认浏览器写请求的 same-origin 元数据校验已启用。
  Web 登录认证禁用或当前请求未识别为 HTTPS 时报告 `warning`。
  详情通过 `session_cookie_host_prefix`、`session_cookie_name_prefix`、`session_cookie_path`、`session_cookie_domain_set` 和 `request_mode_cookie_name_isolation` 报告 HTTPS `__Host-` 名称、路径、Domain 与请求模式隔离状态，并包含请求 scheme、代理信任和 same-origin 校验布尔值，不包含 token 或 Cookie 值。
- `public_share_boundary` 在启用分享时检查公开分享访问 cookie、密码失败节流和公开分享 JSON 响应缓存边界。
  HttpOnly、SameSite、cookie path 作用域、失败节流、`Cache-Control: private`、`Cache-Control: no-cache`、`Vary: Cookie`、`nosniff` 或 `Referrer-Policy: no-referrer` 边界无效时报告 `block`。
  只有这些边界有效且当前请求未识别为 HTTPS 时，缺少 `Secure` 的密码保护分享 cookie 才报告 `warning`。
  详情只包含 cookie 属性和路径作用域状态、公开分享 JSON 缓存与 referrer 边界状态、`Vary: Cookie`、`nosniff` 和密码失败限流参数，不包含分享密码、cookie 值或分享 ID。
- `config_file_access` 检查运行时配置文件路径。
  空路径、缺失或未确认路径报告为 `warning`；路径组件为 symlink、文件本身为 symlink 或非普通文件报告为 `block`；文件允许 group 或 other 访问报告为 `warning`。
  详情通过 `details.path`、`details.mode`、`details.path_kind`、`details.symlink_component` 和 `details.group_or_other_access` 暴露可观测路径、模式、类型、symlink 组件和 group/other 访问字段。
  `details.path_kind` 可为 `missing`、`regular`、`symlink`、`symlink_component` 或 `not_regular`。
- `secrets_file_access` 在 WebDAV 使用 `secrets.json` 中生成的 Basic Auth 密码时检查该文件。
  不需要生成密码时检查为 `pass`；空路径、缺失或未确认路径、路径组件为 symlink、文件本身为 symlink 或非普通文件报告为 `block`；group 或 other 访问报告为 `warning`。
  详情只包含 `details.path`、`details.mode`、`details.path_kind`、`details.symlink_component`、`details.group_or_other_access`、`details.generated_webdav_password_required`、`details.webdav_enabled` 和 `details.webdav_auth_type`，不包含密码内容。
- `users_file_access` 检查运行时用户文件路径。
  缺失路径、不可读目录、symlink 路径组件、symlink 目录、不可读文件、symlink 文件或非普通文件报告为 `block`；目录或文件允许 group/other 访问报告为 `warning`。
  详情包含 `details.path`、`details.dir`、`details.file_mode`、`details.dir_mode`、`details.file_kind`、`details.dir_kind`、`details.symlink_component`、`details.file_group_or_other_access` 和 `details.dir_group_or_other_access`。
  symlink 路径组件使 `details.dir_kind` 为 `symlink_component`。
- `admin_accounts` 检查已启用管理员数量。
  禁用认证或用户存储不可读为 `warning`，零个已启用管理员为 `block`，一个为 `warning`，两个或更多为 `pass`。
  可读时 `details.active_admins` 包含启用管理员数量。
- `initial_password_file` 检查 `auth.users_file` 旁的 `initial-password.txt`。
  不存在时以 `details.path_kind="missing"` 报告 `pass`；仍存在的普通文件、symlink、symlink 路径组件或非普通文件报告为 `block`。
  详情包含 `details.path`，可观测时还包含 `details.mode`、`details.path_kind` 和 `details.symlink_component`。
  symlink、symlink 路径组件或非普通文件分别返回 `details.path_kind` 为 `symlink`、`symlink_component` 或 `not_regular`。
- `webdav_prefix` 在启用 WebDAV 时检查 URL prefix。
  空 prefix、根 prefix、非法路径字符，或位于 `/api`、`/s`、`/health` 下的 prefix 报告为 `block`，详情包含 `details.prefix_risk` 和 `details.normalized_prefix`。
- `webdav_auth` 检查认证模式。
  非 loopback 监听下的 `auth_type = "none"` 报告为 `block`。
  全局 Basic Auth 密码如果是明确的常见占位符或短于 16 字符，会以 `password_source` 和 `password_risk` 类型报告 `warning`，不返回密码值。
  Basic Auth 使用生成密码时，运行时密码不可用报告为 `block` 并设置 `generated_password_available=false`，弱生成密码报告为 `warning`。
- `forwarded_proto_trust` 检查 `X-Forwarded-Proto` 和 trusted-proxy 设置。
  没有 `trusted_proxy_hops` 却收到该头为 `warning`，来自不可信直连来源的该头为 `block`，可信直连来源转发非 `https` 值为 `warning`。
- `share_base_url` 在启用分享时检查公开分享链接 Base URL。
  HTTP、非 443 HTTPS 端口、URL userinfo、查询字符串、片段、编码后的查询或片段标记、反斜杠、重复路径斜杠、`.`/`..` 路径段或非法主机名报告为 `block`。
  空值、不同主机或基础路径以 `/s` 分享路由结尾仍是需要人工复核的 `warning`。
- `share_default_policy` 检查新建分享的默认过期时间和默认访问次数。
  启用分享时，没有默认过期时间、值长于 `720h` 或默认无限访问次数为 `warning`，负数默认值为 `block`。
  详情只包含默认过期/访问限制元数据和 policy-rule 数量。
- `backup_local_destinations` 检查已启用本地备份任务目标。
  没有本地任务或全部本地任务禁用为 `pass`；空目标、相对目标、目标位于备份来源或 `storage.root` 内、symlink 路径组件、symlink 目标或非目录目标为 `block`；缺失、未确认或不可写目标为 `warning`。
  详情通过 `details.job_id`、`details.destination`、`details.source`、`details.storage_root`、`details.destination_kind`、`details.symlink_component`、`details.local_job_count` 和 `details.enabled_local_job_count` 暴露可观测元数据。

该端点只能验证 MnemoNAS 进程可观察到的运行时配置和当前请求代理/TLS 语义，不能直接验证云安全组、公网路由、外部暴露端口或证书链有效性。

公网部署仍应在服务器运行 `sudo mnemonas-doctor --public-domain <domain>`，并在云控制台确认仅 `80/443` 对公网开放。

如果运行时 users-file 路径为空，`initial_password_file` 返回 `block`，并使用空 `details.path`，不会探测当前工作目录。

## 维护

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/maintenance/scrub` | 获取最近 Scrub 结果 |
| `POST` | `/api/v1/maintenance/scrub` | 启动 Scrub |
| `GET` | `/api/v1/maintenance/disk-health` | 运行并返回磁盘 SMART/温度健康状态 |
| `GET` | `/api/v1/maintenance/objects` | 列出存储对象 |
| `POST` | `/api/v1/maintenance/gc` | 运行垃圾回收 |
| `GET` | `/api/v1/maintenance/backups` | 列出已配置备份任务 |
| `POST` | `/api/v1/maintenance/backups` | 创建本地整机备份任务 |
| `GET` | `/api/v1/maintenance/backups/{id}` | 获取单个备份任务状态 |
| `POST` | `/api/v1/maintenance/backups/{id}/run` | 立即运行备份任务 |
| `POST` | `/api/v1/maintenance/backups/{id}/retention-check` | 检查本地或远程保留状态 |
| `POST` | `/api/v1/maintenance/backups/batch-restore-preview` | 预览多个显式恢复目标，不写入数据 |
| `POST` | `/api/v1/maintenance/backups/batch-restore` | 顺序恢复多个备份任务或目标 |
| `POST` | `/api/v1/maintenance/backups/{id}/restore` | 将受支持备份任务恢复到安全目标目录 |
| `POST` | `/api/v1/maintenance/backups/{id}/restore-drill` | 对最近完成快照执行恢复演练 |
| `POST` | `/api/v1/maintenance/backups/{id}/restore-preview` | 预览一次显式恢复，不写入目标数据 |
| `GET` | `/api/v1/maintenance/backups/{id}/restore-report` | 下载单个备份任务的 JSON 恢复摘要 |
| `POST` | `/api/v1/maintenance/backups/{id}/restore-verify` | 只读校验已恢复目标目录 |
| `GET` | `/api/v1/diagnostics-export` | 导出诊断包 |

`POST /api/v1/maintenance/gc` 启动未引用数据块垃圾回收。查询参数：

- `dry_run`：可选布尔值，最多出现一次。默认值为 `true`；只有显式设置为 `false` 时才执行删除。
- `grace_period_hours`：可选非负整数，最多出现一次。默认值为 `24`；宽限期内创建的对象会被跳过。

`dry_run=false` 且部分删除失败时，响应包含 `failed_count` 和 `delete_failures`。

`POST /api/v1/maintenance/backups` 创建以当前 `storage.root` 为来源的本地备份任务。服务端生成任务 ID；该端点不接受任务类型、来源目录、远端仓库或外部命令字段。请求示例：

```json
{
  "name": "外置硬盘备份",
  "destination": "/mnt/backup-drive/mnemonas",
  "schedule_interval": "24h",
  "max_snapshots": 7,
  "include_config": true,
  "verify_after_backup": true
}
```

`name` 和 `destination` 必填。`destination` 必须是 `storage.root` 之外的安全绝对路径，不能指向受保护系统目录、普通文件或经过符号链接的路径。可选字段默认值分别为 `24h`、`7`、`true` 和 `true`；`schedule_interval` 使用 `"0"` 表示仅手动运行。创建成功返回 `201 Created`、任务视图和指向任务状态端点的 `Location` 响应头。配置会先安全写入 `config.toml`，再添加到当前备份管理器，不需要重启服务。

恢复预览请求：

```json
{
  "target_path": "/mnt/restore/mnemonas",
  "include_config": true
}
```

恢复预览响应：

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
    "cutover_checklist": ["Run read-only verification on the restored directory first"],
    "rollback_checklist": ["If cutover fails, stop services and point storage.root back to the previous directory"]
  }
}
```

`restore-preview` 复用显式恢复目标安全校验，并返回 `preflight_checks`、`warnings`、`cutover_checklist` 和 `rollback_checklist`。

预检覆盖目标隔离、`target_state`、备份内容、目标文件系统容量和配置处理。`target_state` 区分两种允许状态：目标目录不存在，或目标目录已存在且为空。

目标不存在时使用父目录做容量探测；已存在的空目标目录使用目标目录所在文件系统。

`preflight_checks[].status` 可为 `passed`、`warning` 或 `failed`。`status = "warning"` 表示复核后可继续恢复；`status = "failed"` 会阻止维护页开始恢复，并在 `/restore` 写入数据前被服务端预检拒绝。

`warnings` 汇总 warning 和 failed 预检详情，供预览卡片、批量预览和恢复历史使用。

批量预览响应：

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

批量恢复响应：

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

失败项会包含 `error_message`；成功项可省略该字段。单项 warning 位于 `items[].warnings` 或 `items[].preview.warnings`，聚合消息位于顶层 `warnings`。

维护端点面向管理员，部分操作可能长时间运行。Web UI 在维护页面暴露相同操作。

维护和诊断行为：

- Scrub 对象错误返回稳定的公开 `errors[].message` 值；更底层的 IO、路径和校验细节只保留在服务端日志中。
- 手动 Scrub 会写入 `scrub` 活动日志。启用 `[maintenance.scrub] enabled = true` 时，服务端按 `schedule_interval` 以系统用户身份运行完整 Scrub 后台任务；失败后按 `retry_interval` 重试，最多 `max_retries` 次。
- 定时运行与手动运行使用相同的维护历史、活动日志详情、结果形状和提醒事件。
  Scrub 失败、对象校验问题和结果持久化不完整会通过 Webhook、Telegram、WeCom、DingTalk 或 SMTP 提醒通道发送 `scrub_run` 事件；提醒详情不包含对象 hash 或底层错误文本。
- `GET /api/v1/maintenance/disk-health` 使用 `[disk_health]` 和 `smartctl --json --all` 报告 `disabled`、`ok`、`warning`、`critical` 或 `unavailable`。
  缺失设备、SMART 失败、序列号不匹配、临界温度、NVMe critical warning、spare capacity 耗尽、介质磨损阈值和介质错误都会影响设备状态。
- 周期检查发现 warning、critical 或 unavailable 时，会为 `system` 用户在 `/system/disk-health` 写入 `disk_health` 活动日志。
  配置提醒通道时，周期磁盘健康检查会为 warning、critical 和 unavailable 状态发送 `disk_health` 事件。
- 活动条目摘要使用配置的设备 `name`；提醒事件详情只使用聚合计数，不包含设备名、完整设备路径、序列号或 warning 文本。
  完整设备路径和 SMART 详情只由管理员维护端点返回。
- `GET /api/v1/maintenance/objects` 接受可选 `limit` 和 `cursor` 查询参数。
  `limit` 默认值和最大值均为 1000；`cursor` 来自上一页 `next_cursor`，非空时必须是 64 字符十六进制对象 hash。
  `limit` 和 `cursor` 均最多出现一次。

诊断响应：

- `GET /api/v1/diagnostics` 和 `/diagnostics-export` 包含脱敏文件系统统计。
  `filesystem.disk_stats_available=true` 时，`filesystem.disk_*` 可包含容量值、`disk_filesystem_type`、脱敏 Linux mountinfo 元数据和 `disk_native_data_checksum_support`。
- 两个诊断端点都设置 `Cache-Control: no-store`、`X-Content-Type-Options: nosniff` 和 `Referrer-Policy: no-referrer`，因为诊断内容可能包含运行状态。`/diagnostics-export` 返回附件，并设置根 `schema_version = 1`；`export_time` 和附件文件名时间戳使用 UTC。
- 诊断响应只暴露 Webhook、Telegram、WeCom、DingTalk 和 SMTP 邮件的通道布尔值。
  SMTP 邮件布尔值只有在启用邮件提醒，且 SMTP host、port、sender 和至少一个非空 recipient 都存在时为 true。
- 诊断响应永远不包含 Webhook URL/header、Telegram bot token、WeCom webhook URL、DingTalk webhook URL、SMTP host、SMTP username、SMTP password、sender address 或 recipient address。
- 诊断响应包含脱敏的 `maintenance` 摘要，其中包括 `history_ready`、`[maintenance.scrub]` 调度设置、最近 Scrub 状态/时间，以及最近失败 Scrub 的 retry count。
- 诊断响应包含脱敏的 `smb` 预览状态。当前构建不捆绑 SMB/Samba 运行时，因此 `runtime_available=false` 表示配置的 SMB 共享不可挂载。
  诊断只暴露共享数量、运行时状态和稳定的“当前局域网挂载使用 WebDAV”提示，不包含 SMB 凭据内容。

备份任务类型和命令执行：

- 备份端点操作 `[[backup.jobs]]` 下配置的任务。支持任务类型为 `local`、`restic` 和 `rclone`。
- 本地任务复制到 `destination/<job-id>/snapshots/<run-id>/`，并可按 `max_snapshots` 和 `max_age` 清理旧快照。
- Restic 任务调用 `restic -r <repository> --password-file <password_file> backup <source>`，并可选调用 `restic check`。
  rclone 任务调用 `rclone sync <source> <remote>`，并可选调用 `rclone check --one-way`。
- 外部命令不经 shell 执行。`command` 必须是裸可执行文件名或绝对路径，`extra_args` 会作为 argv 追加到备份命令，但不能覆盖任务身份；rclone 当前只接受 `--fast-list`，恢复命令不复用备份专用 extra args。
- 备份运行拒绝 `source` 树中的 symlink；`rclone` 恢复演练在远程校验前应用相同检查。
- `restic.password_file` 和 `rclone.config_file` 为对应任务的必填项。文件必须是不超过 4 MiB 的非 symlink 普通文件，且文件及其私有运行快照都必须位于 `source` 和 `storage.root` 之外。
- `restic.repository` 只接受位于备份源和 `storage.root` 之外的绝对本地路径，或显式的 `rest:http://`、`rest:https://` REST 服务地址。
- `rclone.remote` 必须引用 `config_file` 中存在且包含 `type` 的命名 remote。`config_file` 必须是静态、自包含的明文配置；不能启用 `env_auth`、包含 `${...}` 展开，或使用名称含 `_file`、`_path`、`command`、`agent`、`ssh` 或 `token` 的非空配置项。当前接口不支持加密 rclone 配置、环境凭据、外部凭据来源或 token 自动写回。
- 远端命令只读取在解析后的系统临时目录中创建的 `0600` 凭据快照；快照父目录权限为 `0700`。子进程不会继承云服务凭据、代理、SSH agent 或能够覆盖显式任务身份的 `RESTIC_*`、`RCLONE_*` 环境变量。仓库、remote 和凭据文件只取自经过验证的任务配置。

备份脱敏和提醒边界：

- API 任务视图、运行结果、恢复或预览结果、恢复报告和批量恢复结果会对展示字段中的 userinfo、token、密码、secret 和 key 参数脱敏。
  受影响字段包括 `repository`、`remote`、`destination`、`target_path`、`snapshot_path`、`manifest_path` 和 `config_path`。
- API 可见的备份 `error_message`、`warnings` 和预检详情也使用相同脱敏规则。
- 备份提醒事件不包含 source、destination、恢复目标路径、snapshot 或 manifest 路径，也不包含原始 warning/error 文本。
  事件只保留 status、trigger、计数、时间戳、failure category 和位置/错误详情已省略标记等摘要字段。
- Restic/rclone 命令仍接收原始配置值。串联 `restore-preview`、`restore` 和 `restore-verify` 的客户端应保留并复用原始请求 `target_path`；响应中的脱敏 `target_path` 仅用于展示。
- 任务视图的 `restore_report_findings` 和恢复报告下载中的 `findings` 文本也使用同一套备份凭据脱敏规则。
- 恢复报告下载响应设置 `Cache-Control: no-store`、`Pragma: no-cache`、`X-Content-Type-Options: nosniff` 和 `Referrer-Policy: no-referrer`，因为报告可能包含恢复状态和运维判断。

调度、保留和状态：

- 任务可定义 `disabled`、`schedule_interval`、`schedule_window_start`、`schedule_window_end`、`stale_after`、`restore_drill_stale_after`、`max_snapshots`、`max_age` 和 `retention_policy`。
- 正数 `schedule_interval` 会启用进程内调度器。两个 schedule-window 字段同时设置时，自动运行只会在服务器本地 `HH:MM` 窗口内开始，手动立即运行不受影响。
- 任务视图包含备份 `health_status`（`ok`、`manual`、`running`、`due`、`stale`、`failed` 或 `disabled`）、`retention_status` 和 `restore_drill_status`，以及可选消息。
- 成功备份会自动运行保留检查，`POST /retention-check` 可手动运行。
  Local 检查统计本地快照范围，restic 检查运行 `restic snapshots --json --tag mnemonas --tag job:<id>`，rclone 检查运行 `rclone lsjson <remote> --recursive --files-only`。
- 保留检查结果持久化为 `last_retention_check`，并驱动 `retention_status`/`retention_message`。
  `retention_policy` 将 restic/rclone 远程保留标记为外部确认；否则远程任务报告保留 warning。
- 备份管理器在 `<storage.root>/.mnemonas/backup/backup-state.lock` 上持有生命周期锁，同一状态根目录同时只允许一个写入者。Unix 上的状态根目录必须由当前服务账号或 `root` 拥有、不得允许 group/other 写入，其祖先目录也不得允许其他本地账号替换状态根目录。管理器无法取得该锁时，备份端点返回 `503`。
- 管理器保留已锁定状态根目录的文件系统身份，并在 API 可用性检查以及状态写入前后验证原路径。状态根目录被移动、删除或替换时，管理器会立即隔离，不会跟随新路径或向替换目录写入。已原子提交的 `completed` 结果保持完成，`warnings[]` 包含 `备份状态目录身份发生变化；当前结果已生成，但备份服务已停止后续操作。请检查状态目录并重启服务`；后续备份 API 返回 `503`。未完成原子提交的普通硬持久化失败返回 `500`。
- `local` 任务的备份、恢复预览、恢复演练、恢复、恢复后只读校验和保留检测使用 `<destination>/<job-id>/.mnemonas-target.lock`。本地目标已被其他进程占用时，操作返回 `409`。目标锁释放结果无法确认时返回 `500`；即使同一结果还包含“无快照”等业务冲突，也不会降级为 `409`。Unix 上的任务目录必须由当前服务账号或 `root` 拥有，且不得允许 group/other 写入；祖先目录必须由可信账号拥有，可写祖先只有在设置 sticky bit 且能防止替换可信子目录时才会被接受。不安全目标会拒绝操作并返回 `500`。
- FAT、exFAT 等不持久保存 Unix 所有者和 mode 的外置盘需用安全的 `uid`、`gid`、`dmask` 和 `fmask` 挂载。例如，可按实际服务 UID/GID 设置 `uid=<mnemonas-uid>,gid=<mnemonas-gid>,dmask=0077,fmask=0177`。无可靠 Unix mode 或等价挂载约束时，`local` 操作会拒绝继续。
- 远端任务不创建本地目标锁；restic 任务依赖原生仓库锁，rclone 不提供适用于所有 remote 的通用分布式互斥机制。多个任务或实例共用同一 rclone 路径时，必须用外部调度串行化备份、恢复和校验操作。

恢复演练和恢复报告：

- `restore_drill_stale_after` 为空或省略时默认为 30 天，并驱动恢复演练提醒状态。
  配置提醒通道时，过期或缺失恢复演练会发送限频 `backup_restore_drill` warning 事件，`trigger=restore_drill_reminder`。仅在通知通道成功返回后持久化 `last_restore_drill_reminder_at`；投递失败不推进冷却时间，投递成功但标记持久化失败时会在下次调度重试，并可能重复投递。
- 恢复演练历史最多保留最近 20 项，记录状态、文件/字节数、artifact 路径、失败消息，以及失败演练的稳定 `failure_category`。
  当前分类为 `no_snapshot`、`unsupported_job_type`、`unsafe_path`、`integrity_check`、`external_command`、`cancelled`、`io` 和 `unknown`。
- 失败分类会转发到提醒事件详情。任务视图还返回 `restore_drill_stats`，汇总该保留窗口内的总次数、成功、失败、成功率、连续成功或失败次数、最近成功/失败时间、最近失败消息和最近失败分类。
- 恢复历史同样最多保留最近 20 项，记录目标路径、状态、文件/字节数、预检、warning、rollback/cutover checklist 和失败消息。
  `last_restore_verify` 会在页面刷新后持久化最近一次只读恢复后校验结果。
- 任务视图在最近恢复有匹配只读校验时返回 `last_matching_restore_verify`，并返回与恢复报告相同待处理发现的 `restore_report_findings`。
- `GET /restore-report` 下载 `application/json` 附件，包含任务视图、最近备份、保留检查、恢复演练、恢复演练历史和统计、最近恢复、最近恢复校验、匹配校验、恢复历史和 findings。

备份提醒事件：

- 启用 `[alerts] enabled = true` 且配置提醒通道时，备份失败、显式恢复失败或 warning、恢复后只读校验失败或 warning、恢复演练失败或 warning、恢复演练过期/缺失提醒、保留检查失败/warning 和备份 warning 运行会发送事件。
- 事件类型为 `backup_run`、`backup_restore`、`backup_restore_verify`、`backup_restore_drill` 或 `backup_retention_check`，级别为 `warning` 或 `critical`。
- `message` 是固定公开摘要，不包含任务名、路径或原始错误文本。
- 非空 `details` 摘要字段可包含 job ID、run ID、任务类型、trigger、status、时间戳、文件/字节/快照数量、warning 数量、错误消息是否存在、failure category，以及位置详情是否省略。
  `details` 不包含任务名、source、备份目标、恢复目标路径、snapshot 路径、manifest 路径、原始 warning 或原始错误文本。
- 自定义 `backup.Notifier` 的 `NotifyBackupEvent` 为同步调用，每次调用带有 10 秒 deadline，并在管理器关闭时取消 context。实现必须监听 `ctx.Done()` 并在取消或超时后及时返回，否则可能阻塞当前操作或服务关闭。内置 SMTP 传输使用 30 秒默认超时并遵循更早的上游 deadline，因此备份事件仍受 10 秒通知预算限制。

备份操作语义：

- `POST /run` 接受空 body 或 `{}`。端点会在创建本地快照或启动远端命令前持久化 `running` 状态；该写入失败时返回 `500`，且不产生备份目标副作用。
  本地快照完成文件、manifest 和目录同步并发布到最终目录后，端点先提交包含 manifest 证据的成功运行状态，再执行快照清理和保留检测。成功状态在原子替换前写入失败时返回 `500`，`details` 包含失败运行，之前绑定的成功快照保持不变；未绑定的最终快照不会用于恢复。
  备份已经提交、但状态父目录同步、后续清理、保留检测或结果持久化未完整完成时，端点返回 `200 OK`，结果保持 `status="completed"` 并设置 `warning=true`、`warnings[]`。响应同时设置 `message="backup completed with warnings"` 和 `Warning: 199 MnemoNAS "backup run completed with warnings"`。无 warning 的成功响应继续使用 `message="backup completed"`。
  状态目录身份在当前结果原子提交后发生变化时，该次运行也按上述 warning 成功语义返回，但管理器已隔离，后续请求返回 `503`。普通硬持久化失败不使用该 warning 语义，而是返回 `500`。
- `POST /retention-check` 接受空 body 或 `{}`，并返回 `snapshot_count`、`file_count`、`total_bytes`、snapshot 时间范围、`warning` 和 `warnings`；失败返回 `500`，并在 `details` 中包含失败检查。
- `POST /restore-drill` 接受可选 `{"keep_artifact": true}`。
  local 任务临时恢复并校验最近快照，restic 任务运行 `restic check`，rclone 任务运行 `rclone check --one-way`。
  local 任务在默认不保留演练产物时，如果快照校验完成但临时恢复目录清理失败，响应会保持 `status="completed"`，同时设置 `warning=true`、填充 `warnings[]`，并将 `artifact_kept=true` 与 `restored_path` 返回给维护页；warning 文本不包含原始路径或底层错误文本。
- `POST /restore-preview` 使用与 restore 相同的目标规则，但不创建目标数据或写入恢复历史。
  它返回目标隔离、目标状态、备份内容、目标文件系统容量和配置处理对应的 `preflight_checks`、`warnings`、`cutover_checklist` 和 `rollback_checklist`。
- Local 任务只汇总 `status.json.last_successful_run` 绑定的 manifest，不扫描快照目录推断最近快照。manifest 的路径、大小、摘要、任务 ID、运行 ID 和统计字段都必须与成功运行时保存的证据一致；缺少证据时，需要先完成新的本地备份。restic 任务运行 `restic ls latest --json --tag mnemonas --tag job:<id> --path <source>`，rclone 任务运行 `rclone lsjson <remote> --recursive --files-only`。

批量恢复：

- `POST /batch-restore-preview` 接受 `{"items":[{"job_id":"external-disk","target_path":"/absolute/restore/a","include_config":true}]}`，最多 20 项。
  该端点拒绝同一批次中的重复或嵌套目标路径，并返回每项预览状态、`error_message` 和 warning，不写入目标数据或恢复历史。
- `POST /batch-restore` 使用相同请求形状，顺序执行条目，并在每个成功恢复后运行只读 `restore-verify`。
  响应返回每项 `restore`、`verify`、`warnings` 和 `error_message` 字段。
- 顶层 `total_files` 和 `verified_bytes` 聚合已完成条目的只读校验结果。批量恢复错误和 warning 文本使用相同远程目标凭据脱敏。
- 部分失败时整体返回 `status="completed"` 且 `warning=true`；全部条目失败时返回 `status="failed"`，因此客户端必须检查 `items[]`。
- 上述聚合语义仅适用于普通条目失败。状态持久化失败或目标锁释放无法确认时，批量端点返回 `500`；状态目录身份变化或管理器已隔离时返回 `503`。错误响应的 `details` 保留已生成的批量与条目结果。
- 批量预览响应中，单项预览 warning 位于 `items[].preview.warnings`；聚合消息位于顶层 `warnings`。

显式恢复和只读校验：

- `POST /restore` 支持 local、restic 和 rclone 任务，并要求 `{"target_path": "/absolute/restore/path", "include_config": true}`。
- 目标必须是以 `/` 开头的服务端 POSIX 绝对路径，不能包含控制字符、反斜杠或 `.`/`..` 路径段，不能是文件系统根目录或受保护系统目录。
  目标还必须位于 `storage.root`、备份来源以及任何本地备份目标或仓库之外。
- Windows 和 UNC 路径不是合法服务端恢复目标。目标父目录必须存在，目标本身必须不存在或为空。
- 服务端会在写入前重新运行相同恢复预检；失败预检会拒绝恢复，并和失败恢复结果一起持久化。
- Local 恢复将快照 `data/` 内容复制到目标根目录，校验大小和 SHA-256，并在请求时把配置恢复到 `.mnemonas-restore/config.toml`。
- Restic 恢复运行 `restic restore latest --target <临时目录> --tag mnemonas --tag job:<id> --path <source>`。
  服务端拒绝恢复出的 symlink 和特殊文件后，将恢复出的 source 目录内容安装到目标根。
- Rclone 恢复运行 `rclone copy <remote> <临时目录>`，再运行 `rclone check <remote> <临时目录> --one-way`。
  安装到 `target_path` 前同样拒绝恢复出的 symlink 和特殊文件，再把临时目录安装到 `target_path`。
- `include_config` 对 restic 或 rclone 任务没有特殊处理。恢复开始和完成会持久化，失败恢复尝试也会记录，便于后续排查。
- `POST /restore-verify` 要求目标目录已存在，应用相同服务端 POSIX 路径规则、受保护路径边界以及控制字符或点段拒绝规则，不修改数据。
  该端点持久化最近一次校验报告为 `last_restore_verify`，并报告文件/字节数量和关键目录或文件是否存在。
- 校验字段包括 `.mnemonas-restore/config.toml`、`files/`、`.mnemonas/`、`.mnemonas/index.db` 和 `.mnemonas/objects`；warning 会指出 symlink、特殊文件或看起来不像完整 `storage.root` 的目标。
- Local 任务会优先和同一目标最近一次成功恢复快照比较，否则和最近 local 快照比较，并返回比较用的 `snapshot_path` 和 `manifest_path`。

错误和边界条件：

- 无效恢复 `target_path` 和无效批量恢复请求条目返回 `400`。
- 由配置路径、备份来源内容或外部命令导致的备份任务执行失败返回 `500`，并在 `details` 中包含失败运行、演练或恢复结果。
- 未知任务返回 `404`；禁用任务、同一任务已有运行中操作、本地目标锁已由其他进程持有、没有任何完成快照的 local 恢复/恢复演练，以及非空恢复目标返回 `409`。
- 包含反斜杠的恢复目标路径会被 `restore-preview`、`restore` 和 `restore-verify` 拒绝为无效 Windows 或 UNC 风格语法。
- restic 预览和 rclone 预览/保留检查会拒绝输出中的不安全文件路径，包括空路径、控制字符、反斜杠、Windows/UNC 语法、`.`/`..` 路径段，或越过已配置来源边界的绝对路径。
- 备份、恢复、恢复演练、只读校验和保留检查操作会在执行前持久化 `running` 记录。
  服务启动期间，前一个进程退出留下的 `running` 记录会标记为 failed，并写回状态文件。
- 任务视图和恢复报告只在最近恢复成功完成、目标路径匹配且校验时间不早于最近恢复完成时间时，才将 `last_restore_verify` 关联到 `last_restore`。
  任务视图为该匹配校验和待处理 findings 暴露 `last_matching_restore_verify` 与 `restore_report_findings`，语义与恢复报告相同。
- 任务视图和恢复报告会把匹配结果复制到 `last_matching_restore_verify`；否则省略该字段，并用 findings 表示最近恢复仍需要匹配的只读校验。
  最近恢复仍在运行时，恢复报告 findings 会说明恢复尚未完成，并避免把旧校验结果关联到该恢复。
- 本地备份目标会拒绝已有 symlink 路径组件。本地恢复预览、恢复和恢复演练会在读取快照 manifest 或创建演练 artifact 前重新检查目标。
  相同 symlink 路径组件检查也应用于 `POST /restore-preview`、`POST /restore` 和 `POST /restore-verify` 的目标路径。

## WebDAV

WebDAV 服务地址为：

```text
http://localhost:8080/dav
```

WebDAV 访问和方法语义：

- 日常或生产挂载建议设置 `webdav.auth_type = "users"`，使用 MnemoNAS 用户账户挂载，并应用每个用户的 `home_dir` 边界。
  普通用户在 WebDAV 根目录也能看到已授权共享目录的顶层导航入口。
- `must_change_password=true` 的账号不能通过 `users` 模式挂载；完成自助改密后才恢复 WebDAV 认证。
- 根目录示例配置保留旧全局 Basic Auth 作为兼容基线；该模式使用 `[webdav]` 中的服务凭据，或 `secrets.json` 中生成的凭据。
- 为嵌套授权合成的祖先入口只是只读导航；写入仍需要匹配写授权。
- 支持的核心方法包括 `OPTIONS`、`PROPFIND`、`GET`、`HEAD`、`PUT`、`DELETE`、`MKCOL`、`MOVE`、`COPY`、简化 `PROPPATCH`、简化 `LOCK` 和简化 `UNLOCK`。
- HTTP 条件头遵循实体标签比较规则：`If-Match` 使用强比较，弱实体标签不能满足该条件；`If-None-Match` 使用弱比较。两者支持单独的 `*` 和严格的非空逗号分隔实体标签列表。条件 `DELETE` 会在同一存储写锁内完成目标树写权限复核后，再读取所需目标属性并求值条件；权限复核失败时不计算目标内容哈希，也不删除目标。
- WebDAV `DELETE` 使用与 REST 相同的源端暂存、回收站副本清单和隔离清理。未提交且无法回滚的残留返回 `500 Internal Server Error`；逻辑删除已经提交但隔离区物理清理未完成时返回带 `delete cleanup incomplete` 的 `204 No Content`，`trash` 模式的后续容量回收未完成时改为 `trash delete cleanup incomplete`，两者均可同时携带适用的持久化警告。
- 直接父目录不存在时，`MKCOL` 返回 `409 Conflict`；目标已存在时，返回带 `Allow` 的 `405 Method Not Allowed`。
- 不支持的 WebDAV 方法返回 `405 Method Not Allowed`，并在 `Allow` 响应头列出当前范围可用方法。
  只读挂载和只读用户只列出 `OPTIONS`、`GET`、`HEAD` 和 `PROPFIND`。
- 对 WebDAV `MOVE`，若目标路径不存在但保留历史版本元数据，则返回 `409 Conflict`。
  目录移动还会检查目标路径下的后代版本元数据。该目标冲突在用户配额或目录配额检查前返回。
- 带 `Origin`、`Referer` 或 `Sec-Fetch-Site` 元数据的浏览器请求，会对 WebDAV 写方法执行 same-origin 检查。
  脚本和 WebDAV 客户端通常不会发送这些浏览器 origin 头。
- WebDAV 文件和目录列表响应包含 `nosniff` 和 sandbox CSP，用于降低浏览器打开用户文件时执行脚本的风险。

参见 [WebDAV 兼容性](webdav-compatibility.md)。

## 错误代码

常见错误代码类别：

| 类别 | 示例 |
| --- | --- |
| 认证 | `UNAUTHORIZED`、`LOGIN_RATE_LIMITED`、`REFRESH_RATE_LIMITED`、`REFRESH_SESSION_LIMIT`、`TOKEN_EXPIRED`、`TOKEN_REVOKED`、`TOKEN_STATE_UNAVAILABLE`、`PASSWORD_CHANGE_REQUIRED`、`PASSWORD_UNCHANGED` |
| 请求 | `BAD_REQUEST`、`INVALID_REQUEST_BODY`、`VALIDATION_ERROR` |
| 文件 | `NOT_FOUND`、`CONFLICT`、`FILE_TOO_LARGE` |
| 分享 | `SHARE_NOT_FOUND`、`SHARE_EXPIRED`、`SHARE_PASSWORD_RATE_LIMITED` |
| 服务 | `SERVICE_UNAVAILABLE`、`INTERNAL_ERROR` |

广义控制流应使用 HTTP 状态码；面向用户展示或分支处理时使用 JSON 错误代码。

## 版本说明

本文档描述当前 main 分支 REST API。已发布版本、兼容性说明和变更历史由 Git tag 和 [CHANGELOG](../CHANGELOG.md) 跟踪。
