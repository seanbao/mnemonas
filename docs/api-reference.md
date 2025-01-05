# MnemoNAS API 参考文档

本文档描述 MnemoNAS REST API 的所有端点、请求/响应格式和错误处理。

## 基础信息

- **Base URL**: `http://localhost:8080` (默认)
- **Content-Type**: `application/json` (除文件上传外)
- **认证**: 支持 JWT Token 认证（可通过配置启用/禁用）

### 认证方式

当认证启用时，需要在请求头中携带 JWT Token：

```
Authorization: Bearer <access_token>
```

## 通用响应格式

### 成功响应

```json
{
  "data": { ... },
  "message": "操作成功"
}
```

### 错误响应

```json
{
  "error": "错误描述",
  "code": "ERROR_CODE"
}
```

### HTTP 状态码

| 状态码 | 说明 |
|--------|------|
| 200 | 成功 |
| 201 | 创建成功 |
| 400 | 请求参数错误 |
| 404 | 资源不存在 |
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
    "expires_in": 900,
    "user": {
      "id": "user-123",
      "username": "admin",
      "role": "admin"
    }
  }
}
```

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

### 版本信息

获取系统版本信息。

```
GET /api/v1/version
```

**响应示例**:
```json
{
  "data": {
    "name": "MnemoNAS",
    "version": "0.3.0",
    "go": "1.22.0"
  }
}
```

### 存储统计

获取存储使用统计。

```
GET /api/v1/stats
```

**响应示例**:
```json
{
  "data": {
    "totalObjects": 1234,
    "totalSize": 5368709120,
    "dedupRatio": 0.35
  }
}
```

### 诊断信息

获取详细的系统诊断信息。

```
GET /api/v1/diagnostics
```

**响应示例**:
```json
{
  "data": {
    "version": {
      "name": "MnemoNAS",
      "version": "0.3.0",
      "go": "1.22.0"
    },
    "uptimeSecs": 86400,
    "system": {
      "filesystemInitialized": true,
      "dataplaneConnected": true,
      "thumbnailServiceReady": true
    },
    "memory": {
      "allocMb": 50,
      "totalAllocMb": 100,
      "sysMb": 150,
      "numGc": 10
    },
    "goroutines": 25,
    "filesystem": {
      "trashItems": 5,
      "trashSize": 52428800
    },
    "dataplane": {
      "healthy": true,
      "version": "0.3.0",
      "uptimeSec": 86000
    }
  }
}
```

### 指标信息

获取 Prometheus 格式的指标数据。

```
GET /api/v1/metrics
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
- `download`: 设置为 `true` 时返回文件内容而非文件信息

**响应示例**:
```json
{
  "data": {
    "path": "/documents",
    "files": [
      {
        "name": "report.pdf",
        "path": "/documents/report.pdf",
        "isDir": false,
        "size": 1048576,
        "modTime": "2024-01-15T10:00:00Z"
      },
      {
        "name": "images",
        "path": "/documents/images",
        "isDir": true,
        "size": 0,
        "modTime": "2024-01-14T15:30:00Z"
      }
    ]
  }
}
```

### 上传文件

上传文件到指定路径。

```
POST /api/v1/files/{path}
```

**Content-Type**: `multipart/form-data`

**表单字段**:
- `file`: 要上传的文件
- `mkdir`: 设置为 `true` 时创建目录而非上传文件

**限制**:
- 单文件最大: 10GB
- 请求超时: 30 分钟

**响应示例**:
```json
{
  "data": {
    "path": "/documents/report.pdf",
    "size": 1048576,
    "hash": "abc123..."
  },
  "message": "文件上传成功"
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
  "message": "文件已移至回收站"
}
```

---

## 缩略图

### 获取缩略图

获取图片文件的缩略图。

```
GET /api/v1/thumbnails/{path}
```

**查询参数**:
- `size`: 缩略图尺寸，可选值: `small` (150px), `medium` (300px), `large` (600px)

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
  "data": {
    "path": "/documents/report.pdf",
    "versions": [
      {
        "hash": "abc123...",
        "size": 1048576,
        "modTime": "2024-01-15T10:00:00Z",
        "current": true
      },
      {
        "hash": "def456...",
        "size": 1024000,
        "modTime": "2024-01-14T15:00:00Z",
        "current": false
      }
    ]
  }
}
```

### 恢复版本

将文件恢复到指定的历史版本。

```
POST /api/v1/versions/{hash}/restore
```

**请求体**:
```json
{
  "path": "/documents/report.pdf"
}
```

**响应示例**:
```json
{
  "message": "版本恢复成功"
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
  "data": {
    "items": [
      {
        "id": "trash-123",
        "originalPath": "/documents/old-file.txt",
        "deletedAt": "2024-01-15T10:00:00Z",
        "size": 1024,
        "expiresAt": "2024-02-14T10:00:00Z"
      }
    ],
    "totalSize": 52428800
  }
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
  "message": "文件已恢复",
  "data": {
    "restoredPath": "/documents/old-file.txt"
  }
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
  "data": {
    "query": "report",
    "results": [
      {
        "name": "report.pdf",
        "path": "/documents/report.pdf",
        "is_dir": false,
        "size": 1048576,
        "mod_time": "2024-01-15T10:00:00Z"
      }
    ],
    "count": 1
  }
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
  "password": "optional_password",
  "expires_at": "2024-02-15T00:00:00Z"
}
```

**响应示例**:
```json
{
  "data": {
    "id": "share-abc123",
    "path": "/documents/report.pdf",
    "url": "http://localhost:8080/s/abc123",
    "has_password": true,
    "expires_at": "2024-02-15T00:00:00Z",
    "created_at": "2024-01-15T10:00:00Z"
  }
}
```

### 列出分享

```
GET /api/v1/shares
```

### 获取分享详情

```
GET /api/v1/shares/{id}
```

### 更新分享

```
PUT /api/v1/shares/{id}
```

### 删除分享

```
DELETE /api/v1/shares/{id}
```

### 访问分享链接（公开）

```
GET /s/{share_id}
```

如果分享有密码保护，需要在查询参数中携带：

```
GET /s/{share_id}?password=xxx
```

---

## 收藏夹

### 列出收藏

```
GET /api/v1/favorites
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

### 检查是否已收藏

```
GET /api/v1/favorites/check?path=/documents/file.pdf
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

### 取消收藏

```
DELETE /api/v1/favorites/{path}
```

### 更新备注

```
PATCH /api/v1/favorites/{path}
```

---

## 活动日志

### 列出活动

获取用户操作日志。

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
  "data": {
    "items": [
      {
        "id": "act-123",
        "action": "upload",
        "path": "/documents/file.pdf",
        "user": "admin",
        "ip": "127.0.0.1",
        "timestamp": "2024-01-15T10:00:00Z"
      }
    ],
    "total": 100,
    "limit": 50,
    "offset": 0
  }
}
```

### 活动统计

```
GET /api/v1/activity/stats
```

### 清空活动日志（管理员）

```
DELETE /api/v1/activity
```

---

## 设置管理

### 获取设置

```
GET /api/v1/settings
```

**需要管理员权限**

### 更新设置

```
PUT /api/v1/settings
```

**请求体**:
```json
{
  "retention": {
    "max_versions": 10,
    "max_age": "720h"
  },
  "webdav": {
    "enabled": true,
    "read_only": false
  }
}
```

---

## 维护操作

### 获取数据校验结果

获取最近一次数据完整性校验（Scrub）的结果。

```
GET /api/v1/scrub
```

**响应示例**:
```json
{
  "data": {
    "has_result": true,
    "status": "completed",
    "id": "scrub-20240115-100000",
    "start_time": "2024-01-15T10:00:00Z",
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
    ]
  }
}
```

### 执行数据校验

启动数据完整性校验任务。

```
POST /api/v1/scrub
```

**请求体** (可选):
```json
{
  "hashes": ["abc123...", "def456..."]
}
```

如果不提供 `hashes`，将校验所有对象。

### 列出存储对象

列出 CAS 存储中的所有对象。

```
GET /api/v1/objects
```

**查询参数**:
- `limit`: 返回数量限制
- `offset`: 分页偏移

### 执行垃圾回收

启动垃圾回收，清理无引用的数据块。

```
POST /api/v1/gc
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
- 新增认证系统 API（登录/注册/用户管理）
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
