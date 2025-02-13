# WebDAV 客户端兼容性清单

本文档记录 MnemoNAS WebDAV 服务与各客户端的兼容性状态。

## 协议实现状态

### 已实现 (RFC 4918)

| 方法 | 状态 | 说明 |
|------|------|------|
| `OPTIONS` | ✅ 完整 | 返回 DAV: 1, 2 |
| `PROPFIND` | ✅ 完整 | 支持 Depth: 0, 1, infinity |
| `GET` | ✅ 完整 | 支持 Range 请求、ETag、条件请求 |
| `HEAD` | ✅ 完整 | 返回文件元信息 |
| `PUT` | ✅ 完整 | 支持 If-Match 条件写入 |
| `DELETE` | ✅ 完整 | 删除进入回收站（软删除） |
| `MKCOL` | ✅ 完整 | 创建目录 |
| `MOVE` | ⚠️ 部分 | 移动/重命名，Overwrite 头目前不处理 |
| `COPY` | ✅ 完整 | 复制文件/目录 |
| `PROPPATCH` | ⚠️ 简化 | 接受请求但不实际修改属性 |
| `LOCK` | ⚠️ 虚拟 | 返回虚拟锁 token（不实际锁定） |
| `UNLOCK` | ⚠️ 虚拟 | 接受请求但不实际解锁 |

### 未实现

| 方法 | 状态 | 说明 |
|------|------|------|
| `ACL` | ❌ 不支持 | RFC 3744 扩展 |
| `SEARCH` | ❌ 不支持 | RFC 5323 扩展 |

## 客户端兼容性测试

### Linux

| 客户端 | 版本 | 状态 | 备注 |
|--------|------|------|------|
| Nautilus (GNOME Files) | 45+ | ✅ 完整 | 通过 GVfs davs:// |
| Dolphin (KDE) | 23+ | ✅ 完整 | 内置 WebDAV 支持 |
| davfs2 | 1.6+ | ✅ 完整 | 挂载为本地目录 |
| rclone | 1.60+ | ✅ 完整 | 推荐使用 |

### macOS

| 客户端 | 版本 | 状态 | 备注 |
|--------|------|------|------|
| Finder | macOS 12+ | ✅ 完整 | 使用「连接服务器」|
| Transmit | 5+ | ✅ 完整 | 专业文件传输工具 |
| Cyberduck | 8+ | ✅ 完整 | 开源文件浏览器 |
| rclone | 1.60+ | ✅ 完整 | 命令行工具 |

### Windows

| 客户端 | 版本 | 状态 | 备注 |
|--------|------|------|------|
| 资源管理器 | Win 10/11 | ⚠️ 部分 | 需要启用 WebClient 服务 |
| WinSCP | 6+ | ✅ 完整 | 推荐使用 |
| Cyberduck | 8+ | ✅ 完整 | 开源文件浏览器 |
| rclone | 1.60+ | ✅ 完整 | 可挂载为盘符 |
| NetDrive | 3+ | ✅ 完整 | 商业软件 |

**Windows 资源管理器已知问题**：
- 默认只支持 HTTPS（需要配置注册表启用 HTTP）
- 大文件传输可能超时
- 建议使用第三方客户端获得更好体验

### iOS / iPadOS

| 客户端 | 版本 | 状态 | 备注 |
|--------|------|------|------|
| 文件 App | iOS 15+ | ✅ 完整 | 原生 WebDAV 支持 |
| Documents by Readdle | 8+ | ✅ 完整 | 功能丰富 |
| FileBrowser | 14+ | ✅ 完整 | 专业文件管理 |

### Android

| 客户端 | 版本 | 状态 | 备注 |
|--------|------|------|------|
| Solid Explorer | 2.8+ | ✅ 完整 | 推荐 |
| Total Commander + WebDAV 插件 | - | ✅ 完整 | 老牌文件管理器 |
| FolderSync | 5+ | ✅ 完整 | 同步工具 |
| rclone | - | ✅ 完整 | Termux 中运行 |

### 媒体播放器

| 客户端 | 平台 | 状态 | 备注 |
|--------|------|------|------|
| Infuse | iOS/tvOS/macOS | ✅ 完整 | 视频播放流畅 |
| nPlayer | iOS/Android | ✅ 完整 | 支持字幕 |
| VLC | 全平台 | ✅ 完整 | 网络流播放 |
| Kodi | 全平台 | ✅ 完整 | 需配置 WebDAV 源 |

## 已知限制

### LOCK 机制

当前实现为「虚拟锁」，不提供真正的文件锁定：
- 返回锁 token 以满足客户端协议要求
- 不阻止其他客户端修改文件
- 适用于单用户或低并发场景

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
4. **秒传**：相同内容自动去重

## 配置示例

### rclone 配置

```ini
[mnemonas]
type = webdav
url = http://localhost:8080/dav
vendor = other
user = admin
pass = <your-token>
```

### davfs2 挂载

```bash
# /etc/davfs2/secrets
http://localhost:8080/dav admin <your-token>

# 挂载命令
sudo mount -t davfs http://localhost:8080/dav /mnt/nas
```

## 问题反馈

如遇到客户端兼容性问题，请提供：
1. 客户端名称和版本
2. 操作系统和版本
3. 复现步骤
4. 诊断包（可通过 Web UI 导出）
