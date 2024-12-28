# WebDAV 挂载指南

MnemoNAS 通过 WebDAV 协议提供文件访问。本文档介绍如何在各平台挂载 WebDAV 共享。

## 📍 连接信息

| 项目 | 值 |
|------|-----|
| **协议** | WebDAV (HTTP) |
| **地址** | `http://<服务器IP>:8080/dav` |
| **用户名** | 按配置，默认无需认证 |
| **密码** | 按配置，默认无需认证 |

> ⚠️ 生产环境建议启用认证，参见 [配置说明](../mnemonas.example.toml)

---

## 🍎 macOS

### Finder（原生）

1. 打开 Finder
2. 菜单栏 → **前往** → **连接服务器...** (⌘K)
3. 输入地址：`http://localhost:8080/dav`
4. 点击 **连接**
5. 如需认证，输入用户名和密码

**断开连接**：在 Finder 侧边栏右键点击已挂载的共享 → **推出**

### Transmit（推荐）

[Transmit](https://panic.com/transmit/) 是 macOS 上优秀的文件传输工具：

1. 新建连接 → 协议选择 **WebDAV**
2. 服务器：`localhost`
3. 端口：`8080`
4. 路径：`/dav`
5. 点击连接

### Cyberduck

1. 新建书签 → 协议选择 **WebDAV (HTTP)**
2. 服务器：`localhost:8080`
3. 路径：`/dav`

---

## 🪟 Windows

### 资源管理器（原生）

1. 打开 **此电脑**
2. 点击 **映射网络驱动器**
3. 驱动器号：选择一个字母（如 `Z:`）
4. 文件夹：`http://localhost:8080/dav`
5. 勾选 **使用其他凭据连接**（如需认证）
6. 点击 **完成**

**注意**：Windows 原生 WebDAV 客户端对 HTTP（非 HTTPS）支持有限。如遇问题：

```
# 以管理员身份运行 PowerShell
Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\WebClient\Parameters" -Name "BasicAuthLevel" -Value 2
Restart-Service WebClient
```

### WinSCP（推荐）

1. 下载安装 [WinSCP](https://winscp.net/)
2. 新建站点 → 协议选择 **WebDAV**
3. 主机名：`localhost`
4. 端口：`8080`
5. 目录：`/dav`

### Raidrive

[Raidrive](https://www.raidrive.com/) 可将 WebDAV 挂载为本地驱动器：

1. 添加 → NAS → WebDAV
2. 地址：`http://localhost:8080/dav`
3. 选择驱动器号并连接

---

## 🐧 Linux

### GNOME Files (Nautilus)

1. 打开 Files
2. 侧边栏点击 **其他位置**
3. 在底部 **连接服务器** 输入：`dav://localhost:8080/dav`
4. 点击 **连接**

### KDE Dolphin

1. 地址栏输入：`webdav://localhost:8080/dav`
2. 按 Enter 连接

### davfs2（命令行挂载）

```bash
# 安装 davfs2
sudo apt install davfs2  # Debian/Ubuntu
sudo dnf install davfs2  # Fedora

# 创建挂载点
sudo mkdir -p /mnt/nas

# 挂载
sudo mount -t davfs http://localhost:8080/dav /mnt/nas

# 卸载
sudo umount /mnt/nas
```

**开机自动挂载**（`/etc/fstab`）：

```fstab
http://localhost:8080/dav  /mnt/nas  davfs  _netdev,user,noauto  0  0
```

**凭据配置**（`~/.davfs2/secrets`）：

```
http://localhost:8080/dav  username  password
```

### rclone

```bash
# 配置 rclone
rclone config

# 交互式配置
# n) New remote
# name> mnemonas
# Storage> webdav
# url> http://localhost:8080/dav
# vendor> other
# user> (留空或输入用户名)
# pass> (留空或输入密码)

# 挂载
rclone mount mnemonas: /mnt/nas --vfs-cache-mode full

# 或作为 FUSE 挂载（后台运行）
rclone mount mnemonas: /mnt/nas --daemon --vfs-cache-mode full
```

---

## 📱 iOS

### 文件 App（原生）

1. 打开 **文件** App
2. 右上角点击 **...** → **连接服务器**
3. 输入地址：`http://192.168.x.x:8080/dav`（使用服务器局域网 IP）
4. 点击 **连接**

### Documents by Readdle

1. 下载 [Documents](https://apps.apple.com/app/documents-by-readdle/id364901807)
2. 添加连接 → WebDAV
3. 输入服务器地址和端口

---

## 🤖 Android

### Cx 文件管理器

1. 下载 [Cx 文件管理器](https://play.google.com/store/apps/details?id=com.cxinventor.file.explorer)
2. 网络 → 远程存储 → WebDAV
3. 输入服务器地址

### Solid Explorer

1. 添加云连接 → WebDAV
2. 输入 `http://192.168.x.x:8080/dav`

### Total Commander + WebDAV 插件

1. 安装 WebDAV 插件
2. 添加 WebDAV 连接

---

## 🔧 故障排除

### 连接被拒绝

1. 确认服务正在运行：`curl http://localhost:8080/health`
2. 检查防火墙是否开放 8080 端口
3. 如从其他设备访问，使用服务器 IP 而非 localhost

### Windows 无法连接 HTTP

Windows 默认只允许 HTTPS 的 WebDAV。解决方案：

```powershell
# 管理员权限运行
Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\WebClient\Parameters" -Name "BasicAuthLevel" -Value 2
Restart-Service WebClient
```

### 大文件上传失败

1. 检查 davfs2 缓存设置：编辑 `/etc/davfs2/davfs2.conf`
   ```
   cache_size  1024
   buf_size    256
   ```

2. rclone 用户尝试：
   ```bash
   rclone copy localfile mnemonas:/ --size-only
   ```

### macOS Finder 响应慢

macOS Finder 的 WebDAV 实现会频繁发送 PROPFIND 请求。解决方案：

1. 使用 Transmit 或 Cyberduck 等第三方客户端
2. MnemoNAS 已内置 PROPFIND 缓存（30 秒 TTL）

### 文件锁定问题

MnemoNAS 使用虚拟锁实现（简化设计）。如遇锁定问题：

1. 尝试刷新客户端
2. 重新挂载
3. 检查是否有其他客户端正在编辑同一文件

---

## 📖 更多资源

- [WebDAV 客户端兼容性](webdav-compatibility.md)
- [配置示例](../mnemonas.example.toml)
- [FAQ](faq.md)
