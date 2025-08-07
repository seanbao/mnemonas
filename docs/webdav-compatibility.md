<!-- markdownlint-disable MD032 MD060 -->

# WebDAV 客户端兼容性清单

[English](webdav-compatibility.en.md) | 简体中文

本文档记录 MnemoNAS WebDAV 服务的协议能力，以及常见客户端的预期兼容性。客户端版本、系统策略和网络环境会影响结果，发布前后应持续用真实客户端回归验证。

说明：本文档描述的是 WebDAV 协议能力。REST API `/api/v1/files-copy` 现已支持递归目录复制，但不提供 `Overwrite: T/F` 控制。

补充：部分写请求在变更已经提交、但后续持久化或清理步骤失败时，会保持成功状态码并附带 HTTP `Warning` 响应头，而不是改写成整体失败。当前已覆盖的 warning 文案包括 `199 MnemoNAS "workspace mutation persistence incomplete"`、`199 MnemoNAS "delete cleanup incomplete"`、`199 MnemoNAS "trash delete cleanup incomplete"`。

认证：`auth_type = "users"` 使用 MnemoNAS 用户账号登录，普通用户挂载根目录映射到自己的 `home_dir`，guest 只读，并对 PUT/COPY 执行用户配额限制；`auth_type = "basic"` 保留为全局服务凭据兼容模式。

## 协议实现状态

### 已实现 (RFC 4918)

| 方法 | 状态 | 说明 |
|------|------|------|
| `OPTIONS` | ✅ 核心支持 | 返回 DAV: 1, 2 |
| `PROPFIND` | ✅ 核心支持 | 支持 Depth: 0, 1, infinity |
| `GET` | ✅ 核心支持 | 支持 Range 请求、ETag、条件请求 |
| `HEAD` | ✅ 核心支持 | 返回文件元信息 |
| `PUT` | ✅ 核心支持 | 支持 `If-Match`、`If-Unmodified-Since` 条件写入；仅接受完整覆盖写入，`Content-Range` partial PUT 返回 `400 Bad Request` |
| `DELETE` | ✅ 核心支持 | 删除进入回收站（软删除）；集合资源仅接受 `Depth: infinity`（省略时按 `infinity` 处理） |
| `MKCOL` | ✅ 核心支持 | 创建目录 |
| `MOVE` | ✅ 核心支持 | 移动/重命名，支持 `Overwrite: T/F`；集合资源仅接受 `Depth: infinity`（省略时按 `infinity` 处理）；覆盖目标已提交后若 backup cleanup 失败，返回 `204 + Warning` |
| `COPY` | ✅ 核心支持 | 复制文件/目录，支持 `Overwrite: T/F`；集合资源支持 `Depth: 0` 和 `Depth: infinity`；递归目录复制在目标目录已创建、仅持久化失败时返回成功并附带 `Warning` |
| `PROPPATCH` | ⚠️ 简化 | 解析请求并显式拒绝属性修改；返回 `207 Multi-Status`，属性状态为 `403 Forbidden`，不持久化 dead properties |
| `LOCK` | ⚠️ 简化 | 返回虚拟锁 token，支持 `Depth: 0` / `Depth: infinity`，默认 1 小时过期 |
| `UNLOCK` | ⚠️ 简化 | 需要匹配 `Lock-Token`，过期锁会自动清理 |

### 未实现

| 方法 | 状态 | 说明 |
|------|------|------|
| `ACL` | ❌ 不支持 | RFC 3744 扩展 |
| `SEARCH` | ❌ 不支持 | RFC 5323 扩展 |

## 客户端兼容性矩阵

状态说明：

- ✅ 已验证：有自动化或真实客户端回归记录。
- ◐ 预期可用：依赖核心 WebDAV 行为，尚需真实客户端回归确认。
- ⚠️ 需要配置/待验证：已知需要额外系统设置，或还没有足够验证数据。

当前自动化测试覆盖 `OPTIONS`、`MKCOL`、`PUT`、`PROPFIND`、`COPY`、`MOVE`、条件请求、Range/ETag、LOCK/UNLOCK 等核心协议行为；下表用于发布前后补齐真实客户端验证。

### Linux

| 客户端 | 版本 | 状态 | 备注 |
|--------|------|------|------|
| Nautilus (GNOME Files) | 45+ | ◐ 预期可用 | 通过 GVfs davs:// |
| Dolphin (KDE) | 23+ | ◐ 预期可用 | 内置 WebDAV 支持 |
| davfs2 | 1.6+ | ◐ 预期可用 | 挂载为本地目录 |
| rclone | 1.60+ | ◐ 预期可用 | 推荐优先验证；脚本和文档均按 rclone 使用方式设计 |

### macOS

| 客户端 | 版本 | 状态 | 备注 |
|--------|------|------|------|
| Finder | macOS 12+ | ◐ 预期可用 | 使用「连接服务器」|
| Transmit | 5+ | ◐ 预期可用 | 专业文件传输工具 |
| Cyberduck | 8+ | ◐ 预期可用 | 开源文件浏览器 |
| rclone | 1.60+ | ◐ 预期可用 | 命令行工具 |

### Windows

| 客户端 | 版本 | 状态 | 备注 |
|--------|------|------|------|
| 资源管理器 | Win 10/11 | ⚠️ 需要配置 | 需要启用 WebClient 服务；HTTP 还需要注册表设置 |
| WinSCP | 6+ | ◐ 预期可用 | 推荐优先验证 |
| Cyberduck | 8+ | ◐ 预期可用 | 开源文件浏览器 |
| rclone | 1.60+ | ◐ 预期可用 | 可挂载为盘符 |
| NetDrive | 3+ | ⚠️ 待验证 | 商业软件，不同版本行为可能不同 |

**Windows 资源管理器已知问题**：
- 默认只支持 HTTPS（需要配置注册表启用 HTTP）
- 大文件传输可能超时
- 建议使用第三方客户端获得更好体验

### iOS / iPadOS

| 客户端 | 版本 | 状态 | 备注 |
|--------|------|------|------|
| 文件 App | iOS 15+ | ◐ 预期可用 | 原生 WebDAV 支持 |
| Documents by Readdle | 8+ | ◐ 预期可用 | 功能丰富 |
| FileBrowser | 14+ | ⚠️ 待验证 | 专业文件管理 |

### Android

| 客户端 | 版本 | 状态 | 备注 |
|--------|------|------|------|
| Solid Explorer | 2.8+ | ◐ 预期可用 | 推荐优先验证 |
| Total Commander + WebDAV 插件 | - | ⚠️ 待验证 | 老牌文件管理器 |
| FolderSync | 5+ | ⚠️ 待验证 | 同步工具 |
| rclone | - | ◐ 预期可用 | Termux 中运行 |

### 媒体播放器

| 客户端 | 平台 | 状态 | 备注 |
|--------|------|------|------|
| Infuse | iOS/tvOS/macOS | ⚠️ 待验证 | 支持 WebDAV 源，播放效果取决于网络和客户端缓存策略 |
| nPlayer | iOS/Android | ⚠️ 待验证 | 支持字幕，需验证大文件拖动体验 |
| VLC | 全平台 | ◐ 预期可用 | 网络流播放；重点验证 Range 请求和大文件拖动 |
| Kodi | 全平台 | ⚠️ 待验证 | 需配置 WebDAV 源 |

## 已知限制

### LOCK 机制

当前实现为「虚拟锁」，不提供真正的文件锁定：
- 返回锁 token 以满足客户端协议要求
- 支持 `Depth: 0` 和 `Depth: infinity`；省略 `Depth` 时按 `infinity` 处理
- 仅对已存在资源返回锁成功；不存在的路径返回 `404 Not Found`
- 刷新锁要求空请求体并携带作用域匹配的 lock token；刷新响应不返回 `Lock-Token` 头
- `UNLOCK` 需要提供 `Lock-Token` 请求头；缺失时返回 `400 Bad Request`
- 过期时间固定为 1 小时；后续请求会自动清理过期锁
- 会阻止缺少匹配 token 的写请求，但不做跨进程持久化
- 适用于单节点、单用户或低并发场景

**影响**：Office 等依赖锁机制的应用可能出现冲突警告

### 大文件上传

- 默认超时 60 秒（可配置）
- 大于 10GB 的文件建议使用命令行工具

### 目录深度

- `PROPFIND Depth: infinity` 在大目录可能较慢
- 建议客户端使用 `Depth: 1` 逐级浏览

## 性能优化建议

1. **目录缓存**：PROPFIND 结果默认缓存 30 秒
2. **Range 请求**：支持断点续传和视频拖动
3. **ETag 支持**：启用客户端缓存避免重复下载
4. **重复内容复用**：相同内容可复用已有对象；客户端仍需要完成一次上传请求

## 配置示例

### rclone 配置

```ini
[mnemonas]
type = webdav
url = http://localhost:8080/dav
vendor = other
user = admin
pass = <obscured-webdav-password>
```

使用 `rclone obscure <webdav-password>` 生成 `pass` 字段的值。

### davfs2 挂载

```bash
# /etc/davfs2/secrets
http://localhost:8080/dav admin <your-webdav-password>

# 挂载命令
sudo mount -t davfs http://localhost:8080/dav /mnt/nas
```

## 问题反馈

如遇到客户端兼容性问题，请提供：
1. 客户端名称和版本
2. 操作系统和版本
3. 复现步骤
4. 诊断包（可通过 Web UI 导出）
