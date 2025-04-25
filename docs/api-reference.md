<!-- markdownlint-disable MD022 MD031 MD032 MD036 MD040 MD060 -->

# MnemoNAS API 参考文档

本文档描述 MnemoNAS REST API 的所有端点、请求/响应格式和错误处理。

## 基础信息

- **Base URL**: `http://localhost:8080` (默认)
- **Content-Type**: `application/json` (除文件上传外)
- **认证**: 支持 JWT Token 认证（可通过配置启用/禁用）
- JSON 请求体采用严格解析：写接口会拒绝未知字段和拼接的多个 JSON 值，并返回 `400 invalid request body`

### 认证方式

当认证启用时，需要在请求头中携带 JWT Token：

```
Authorization: Bearer <access_token>
```

## 通用响应格式

### 成功响应（多数 /api/v1 端点）

```json
{
  "success": true,
  "data": { ... },
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
  "details": { ... },
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

认证后的分享管理端点 `/api/v1/shares` 使用 `success + data (+ message)` 包装；公开分享 `/s/*` 的成功响应保持原始 JSON 对象或数组，错误响应使用 `success: false` 和结构化 `error` 对象。

```json
{
  "success": true,
  "data": { ... }
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
| 404 | 资源不存在 |
| 429 | 请求过于频繁 / 密码尝试次数过多 |
| 410 | 资源不可用（过期/禁用/访问上限） |
| 413 | 文件过大 |
| 500 | 服务器内部错误 |

---

## 认证端点

### 用户登录

使用用户名和密码登录获取令牌。

```
POST /api/v1/auth/login
```

**请求体**:
```json
{
  "username": "admin",
  "password": "your_password"
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
      "role": "admin",
      "home_dir": "/"
    }
  }
}
```

**失败行为**:
- 同一 `username + 客户端地址` 组合连续登录失败达到限制时，返回 `429 Too Many Requests`，错误码为 `LOGIN_RATE_LIMITED`
- `username` 分桶遵循账户名大小写不敏感语义，`handleruser` 与 `HANDLERUSER` 计入同一限流桶
- 客户端地址默认使用直连来源；只有当请求直接来自 loopback 或私有网段代理时，才采信 `X-Forwarded-For` / `X-Real-IP`

### 刷新令牌

使用 refresh_token 获取新的 access_token。

```
POST /api/v1/auth/refresh
```

**请求体**:
```json
{
  "refresh_token": "eyJ..."
}
```

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
      "role": "admin",
      "home_dir": "/"
    }
  }
}
```

### 退出登录

```
POST /api/v1/auth/logout
```

**需要认证**: 是

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
        "role": "admin",
        "disabled": false,
        "home_dir": "/"
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

**删除用户**:
```
DELETE /api/v1/admin/users/{id}
```

**重置用户密码**:
```
POST /api/v1/admin/users/{id}/reset-password
```

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
```

**响应示例**:
```json
{
  "status": "healthy",
  "timestamp": "2024-01-15T10:00:00Z",
  "uptime": "24h30m15s",
  "dataplane": {
    "healthy": true,
    "version": "0.3.0",
    "uptime": 86400
  }
}
```

**说明**:
- 当已配置的数据面、缩略图缓存、维护历史、活动日志或收藏存储子系统未能初始化时，`status` 会降级为 `degraded`

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
    "version": "0.1.0",
    "go": "go1.22.0"
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
  "webdav_enabled": true,
  "webdav_auth_type": "basic"
}
```

**说明**:
- 该接口不再返回任何初始密码或用户名
- 首次启动生成的初始密码仅写入启动日志和 `secrets.json`

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
    "total_files": 0,
    "total_size": 5368709120,
    "unique_size": 2147483648,
    "dedup_ratio": 0.35,
    "total_chunks": 1234
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

**说明**:
- `total_files` 统计文件索引中的文件数量，不包含目录。
- 当文件计数或数据面统计暂不可用时，对应字段会被省略，而不是回填误导性的 `0`。

### 诊断信息

获取详细的系统诊断信息。

```
GET /api/v1/diagnostics
```

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
      "version": "0.1.0",
      "go": "go1.22.0"
    },
    "system": {
      "filesystem_initialized": true,
      "dataplane_connected": true,
      "thumbnail_service_ready": true,
      "maintenance_history_ready": true,
      "activity_log_ready": true,
      "favorites_store_ready": true
    },
    "memory": {
      "alloc_mb": 50,
      "total_alloc_mb": 100,
      "sys_mb": 150,
      "num_gc": 10
    },
    "goroutines": 25,
    "filesystem": {
      "trash_items": 5,
      "trash_size": 52428800
    },
    "storage": {
      "total_chunks": 1234,
      "total_size": 5368709120,
      "unique_size": 2147483648,
      "dedup_ratio": 0.35
    },
    "dataplane": {
      "healthy": true,
      "version": "0.3.0",
      "uptime_sec": 86000
    }
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

**说明**:
- 当回收站统计暂不可用时，`filesystem.trash_items` 和 `filesystem.trash_size` 会被省略，而不是回填 `0`。

### 指标信息

获取 JSON 格式的指标数据。

**需要认证**: 当 `auth.enabled = true` 时需要 JWT 认证

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

**查询参数**:
- 无

**响应示例**:
```json
{
  "success": true,
  "data": {
    "path": "/documents",
    "files": [
      {
        "name": "report.pdf",
        "path": "/documents/report.pdf",
        "isDir": false,
        "size": 1048576,
        "modTime": "2024-01-15T10:00:00Z",
        "hash": "abc123...",
        "versioned": true
      },
      {
        "name": "images",
        "path": "/documents/images",
        "isDir": true,
        "size": 0,
        "modTime": "2024-01-14T15:30:00Z"
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

### 复制资源

```
POST /api/v1/files-copy
```

说明：该 REST 端点支持复制单个文件或递归复制目录。目标路径必须不存在；如需 `Overwrite: T/F` 语义，请使用 WebDAV `COPY`。

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
- `download`: 设置为 `true` 时强制下载（设置 `Content-Disposition`）
- `version`: 指定版本哈希（64 位 BLAKE3）下载历史版本

**鉴权说明**:
- API 客户端可使用现有认证会话或 `Authorization` 请求头
- 浏览器下载、预览与外部打开使用短期 `HttpOnly` download-session cookie
- 当前实现不再支持通过 `auth` 查询参数传递访问令牌

**响应**: 返回文件二进制数据；当前版本支持 Range 请求，历史版本不保证 Range

### 创建目录

```
POST /api/v1/directories/{path}
```

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
- `size`: 缩略图尺寸，可选值: `small` (150px), `medium` (300px), `large` (600px)

**鉴权说明**:
- API 客户端可使用现有认证会话或 `Authorization` 请求头
- 浏览器缩略图请求依赖短期 `HttpOnly` download-session cookie
- 当前实现不再支持通过 `auth` 查询参数传递访问令牌

**支持格式**: JPEG, PNG, GIF, WebP

**响应**: 返回图片二进制数据

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
- `path`: 文件路径（必填）

**响应示例**:
```json
{
  "success": true,
  "data": {
    "path": "/documents/report.pdf",
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

将文件从回收站恢复到原位置。

```
POST /api/v1/trash/{id}/restore
```

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

### 清空回收站

清空整个回收站。

```
DELETE /api/v1/trash
```

---

## 搜索

### 文件搜索

按文件名搜索文件。

```
GET /api/v1/search?q={query}
```

**查询参数**:
- `q`: 搜索关键词（必填，最长 100 字符）
- `limit`: 返回数量限制（默认 50，最大 100）

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
- `permission`: `read`
- 响应中的 `url` 为动态生成字段：当 `share.base_url` 已配置时返回 `<base_url>/s/{id}`；未配置时返回相对路径 `/s/{id}`
- `share.base_url` 配置错误不会破坏分享记录本身，但会让返回给前端/调用方的公开链接不可直接访问

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
    "url": "http://localhost:8080/s/share-abc123"
  }
}
```

### 列出分享

```
GET /api/v1/shares
```

**查询参数**:
- `all=true`: 管理员查看所有用户的分享

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
      "url": "http://localhost:8080/s/share-abc123"
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

**说明**:
- 更新分享不会改变 `id`；响应中的 `url` 会根据当前运行时 `share.base_url` 重新生成

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
    "url": "http://localhost:8080/s/share-abc123"
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

```
GET /s/{share_id}
```

如果分享有密码保护，需要 POST 并提供密码：

```
POST /s/{share_id}
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
  "permission": "read",
  "description": ""
}
```

**说明**:
- 当分享不需要密码，或已通过密码验证后，会返回 `file_name` / `file_size` / `folder_items`
- 当 `max_access > 0` 且 `access_count` 达到上限时，返回 `410 Gone`
- `access_count` 在下载与文件夹列表请求时递增；`POST /s/{share_id}` 验证密码不会计数
- 密码验证成功后，服务端通过 HttpOnly cookie 记录访问状态；后续下载和文件夹列表请求不使用 `password` 查询参数
- 连续密码错误达到限制时，返回 `429 Too Many Requests`，错误码为 `SHARE_PASSWORD_RATE_LIMITED`
- 口令失败限流默认按 share ID 与直连客户端地址组合统计；只有当请求直接来自 loopback 或私有网段代理时，才采信 `X-Forwarded-For` / `X-Real-IP`

**下载文件**:
```
GET /s/{share_id}/download
```

**列出分享文件夹内容**:
```
GET /s/{share_id}/items?path=subdir
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

**下载分享文件夹内文件**:
```
GET /s/{share_id}/download/{path}
```

**说明**:
- `{path}` 需要按路径段进行 URL 编码（保留 `/` 分隔）
- 分享启用密码时，需先通过 `POST /s/{share_id}` 完成密码验证，再使用返回的 cookie 访问下载和文件夹列表接口

---

## 收藏夹

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
  "message": "favorite note updated successfully"
}
```

---

## 活动日志

### 列出活动

获取用户操作日志。

**说明**:
- 启用认证时，管理员可查看全量活动日志；普通用户仅返回当前账号自己的活动记录，`user` 查询参数不会越权查看其他账号
- 未配置活动日志时，接口返回空列表
- 若活动日志已配置但初始化失败或当前不可用，接口返回 `503 Service Unavailable`

```
GET /api/v1/activity
```

**查询参数**:
- `limit`: 返回数量（默认 50，最大 500）
- `offset`: 分页偏移
- `action`: 按操作类型过滤
- `user`: 按用户过滤

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
- 未配置活动日志时，接口返回零统计
- 若活动日志已配置但初始化失败或当前不可用，接口返回 `503 Service Unavailable`

```
GET /api/v1/activity/stats
```

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
    }
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### 清空活动日志（管理员）

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
- 若活动日志已配置但初始化失败或当前不可用，接口返回 `503 Service Unavailable`，而不是伪装成清理成功

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
      "tls": {
        "enabled": false,
        "cert_file": "",
        "key_file": "",
        "auto_generate": true,
        "cert_dir": "~/.mnemonas/.mnemonas/certs"
      }
    },
    "storage": {
      "root": "~/.mnemonas"
    },
    "retention": {
      "max_versions": 50,
      "max_age": "2160h",
      "min_free_space": 10737418240,
      "gc_interval": "24h"
    },
    "versioning": {
      "auto_versioned_extensions": [".md", ".txt", ".go"],
      "auto_versioned_filenames": ["README", "Dockerfile", "Makefile"],
      "max_versioned_size": 104857600
    },
    "webdav": {
      "enabled": true,
      "prefix": "/dav",
      "read_only": false,
      "auth_type": "basic",
      "username": "admin"
    },
    "share": {
      "enabled": false,
      "base_url": ""
    },
    "favorites": {
      "enabled": true
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
      "webhook_method": "POST",
      "webhook_headers": []
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

### 更新设置

```
PUT /api/v1/settings
```

**说明**:
- `storage` 路径为只读配置，需修改配置文件并重启服务

**请求体**:
```json
{
  "trash": {
    "enabled": false
  },
  "server": {
    "host": "0.0.0.0",
    "port": 8080,
    "read_timeout": "30s",
    "write_timeout": "60s",
    "idle_timeout": "120s",
    "tls": {
      "enabled": true,
      "auto_generate": true,
      "cert_dir": "/etc/mnemonas/tls"
    }
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
    "base_url": "https://share.example.com"
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
    "webhook_headers": ["Authorization: Bearer token", "X-MnemoNAS: alerts"]
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
  "message": "settings updated, some changes may require restart"
}
```

**失败行为**:
- `trash` 支持更新 `enabled`、`retention_days`、`max_size`；保存后会立即影响运行中的回收站策略
- `retention` 支持更新 `max_versions`、`max_age`、`min_free_space`、`gc_interval`；保存后会立即更新运行中的版本保留阈值与周期清理任务，`gc_interval` 设为 `0` 表示禁用周期清理
- `server` 支持更新 `host`、`port`、`read_timeout`、`write_timeout`、`idle_timeout`；保存后需重启服务才能影响运行中的 HTTP 监听器
- `server.tls` 支持更新 `enabled`、`cert_file`、`key_file`、`auto_generate`、`cert_dir`；保存后需重启服务才能切换 HTTPS 监听
- `versioning` 支持更新 `auto_versioned_extensions`、`auto_versioned_filenames`、`max_versioned_size`；保存后会立即更新运行中的自动版本策略
- `share` 支持更新 `enabled`、`base_url`；`enabled` 会立即影响公开分享访问和新分享创建，`base_url` 会立即影响后续新生成的分享链接
- `favorites` 支持更新 `enabled`；保存后会立即影响收藏接口的可用性
- `alerts` 支持更新 `enabled`、`check_interval`、`threshold_pct`、`critical_pct`、`min_free_bytes`、`cooldown_period`、`webhook_url`、`webhook_method`、`webhook_headers`；保存后会立即更新运行中的告警监控
- `dataplane` 支持更新 `grpc_address`、`timeout`、`max_retries`；保存后会立即替换运行中的数据面 client，并用于后续按需重连和连接重试策略
- 请求中的 `trash.retention_days` 不能为负数，`trash.max_size` 必须是正整数
- 请求中的 `versioning.max_versioned_size` 必须是正整数，`versioning.auto_versioned_extensions` 每项必须以 `.` 开头，`versioning.auto_versioned_filenames` 不能包含空项
- `webdav` 支持更新 `enabled`、`prefix`、`read_only`、`auth_type`、`username`、`password`；保存后会立即切换运行中的 WebDAV 前缀、鉴权方式和只读状态
- 认证启用时，`webdav.username` 不得复用现有非 admin 用户名；WebDAV 基本认证是全局服务凭据，不携带应用层 `home_dir` 隔离
- 请求中的 `server.read_timeout`、`server.write_timeout`、`server.idle_timeout` 必须是正的 `time.ParseDuration` 字符串，例如 `30s`、`2m`
- 请求中的 `retention.max_age`、`retention.gc_interval` 必须是 `time.ParseDuration` 可解析的字符串，例如 `720h`、`24h`、`0`
- 请求中的 `alerts.check_interval`、`alerts.cooldown_period` 必须是正的 `time.ParseDuration` 字符串
- 请求中的 `alerts.webhook_method` 仅支持 `GET` 或 `POST`，`alerts.webhook_headers` 每项必须是 `Key:Value` 格式
- 请求中的 `dataplane.timeout` 必须是正的 `time.ParseDuration` 字符串，`dataplane.max_retries` 必须是 `0` 或正整数
- 配置校验失败时返回 `400 Bad Request` 和稳定错误消息 `invalid configuration`
- 非法设置请求不会修改进程内当前生效配置

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
        "message": "Hash mismatch"
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
        "message": "Hash mismatch"
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
- `limit`: 返回数量限制（默认 1000）
- `cursor`: 游标（从上一次返回的 `next_cursor` 开始）

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
- `dry_run`: 是否仅计算不删除（默认 `true`，只有显式传入 `false` 才会执行删除）
- `grace_period_hours`: 跳过最近创建对象的小时数（默认 24）

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

### 导出诊断信息

下载完整的诊断信息包（JSON 格式）。

```
GET /api/v1/diagnostics-export
```

**响应**: 返回 JSON 文件下载

---

## WebDAV 接口

MnemoNAS 支持标准 WebDAV 协议，可用于文件管理器挂载。

**挂载地址**: `http://localhost:8080/dav/`

支持的 WebDAV 方法:
- `PROPFIND` - 列出目录
- `GET` - 下载文件
- `PUT` - 上传文件  
- `DELETE` - 删除文件
- `MKCOL` - 创建目录
- `COPY` - 复制文件
- `MOVE` - 移动/重命名文件
- `LOCK/UNLOCK` - 文件锁定（虚拟实现）

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

## 版本变更记录

### v0.4.0
- 新增认证系统 API（登录/用户管理）
- 新增文件分享 API
- 新增收藏夹 API
- 新增活动日志 API
- 新增设置管理 API
- 新增文件搜索 API

### v0.3.0
- 新增缩略图服务 API
- 新增数据校验 API
- 新增回收站管理 API
- 新增诊断导出 API

### v0.2.0
- 新增版本历史 API
- 新增 WebDAV 支持

### v0.1.0
- 初始版本
- 基础文件操作 API
