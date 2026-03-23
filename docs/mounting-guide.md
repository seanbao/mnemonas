# WebDAV 挂载指南

[English](mounting-guide.en.md) | 简体中文

MnemoNAS 通过 WebDAV 暴露文件。Web UI 提供管理操作，WebDAV 则为桌面端、移动端和命令行客户端提供同一存储空间的访问入口。
下列客户端步骤用于连接配置参考；实际兼容状态以 [WebDAV 客户端兼容性](webdav-compatibility.md) 为准。`rclone` 有可选真实客户端 E2E 覆盖，Finder、Windows File Explorer、移动端和媒体客户端仍按矩阵跟踪。

## 连接信息

| 项目 | 值 |
| --- | --- |
| 协议 | 基于 HTTP 或 HTTPS 的 WebDAV |
| 默认地址 | `http://<server-ip>:8080/dav` |
| 默认本地地址 | `http://localhost:8080/dav` |
| 用户名 | `auth_type = "users"` 时使用 MnemoNAS 用户名；`basic` 模式使用配置的 WebDAV 用户名 |
| 密码 | `users` 模式使用 MnemoNAS 用户密码；`basic` 模式使用配置或生成的 WebDAV 密码 |

日常挂载建议使用 `auth_type = "users"`。
该模式会让 WebDAV 遵守 MnemoNAS 用户角色、用户组、`home_dir`、目录授权、写入 `home_dir` 的用户配额和目录配额边界。
默认兼容性 `basic` 模式使用独立的全局 WebDAV 凭据。

Basic Auth 凭据处理：

- 运行中的 Web UI 会在设置页 `WebDAV` 标签页显示当前 WebDAV 地址、Basic 用户名和可读取的生成密码。
- 自定义 Basic 密码不会回显，应以配置文件或密码管理器记录为准。
- Basic Auth 使用自动生成密码时，`<storage.root>/secrets.json` 是服务器端兜底来源。

参见 [配置说明](configuration.md)。

## macOS

### Finder

1. 打开 Finder。
2. 使用 **前往** -> **连接服务器...**，或按 `Command+K`。
3. 输入 `http://localhost:8080/dav`。
4. 点击 **连接**。
5. 按所选认证模式输入凭据：`users` 模式使用 MnemoNAS 用户名和密码；`basic` 模式使用 WebDAV 用户名和密码。

断开连接时，在 Finder 侧边栏推出已挂载的共享。

### Transmit

1. 新建连接。
2. 选择 **WebDAV**。
3. 服务器：`localhost`。
4. 端口：`8080`。
5. 路径：`/dav`。
6. 使用所选认证模式对应的凭据连接。

### Cyberduck

1. 新建书签。
2. 选择 **WebDAV (HTTP)** 或 **WebDAV (HTTPS)**。
3. 服务器：`localhost:8080`。
4. 路径：`/dav`。
5. 输入所选认证模式对应的凭据。

## Windows

### File Explorer

1. 打开 **此电脑**。
2. 点击 **映射网络驱动器**。
3. 选择驱动器号，例如 `Z:`。
4. 文件夹：`http://localhost:8080/dav`。
5. 启用 **使用其他凭据连接**。
6. 完成后输入所选认证模式对应的凭据。

Windows 内置 WebDAV 客户端对 HTTP 支持有限。非 HTTPS 测试可用管理员权限运行 PowerShell：

```powershell
Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\WebClient\Parameters" -Name "BasicAuthLevel" -Value 2
Restart-Service WebClient
```

常规使用建议通过反向代理部署 HTTPS。

### WinSCP

1. 安装 WinSCP。
2. 新建站点。
3. 文件协议：**WebDAV**。
4. 主机名：`localhost`。
5. 端口：`8080`。
6. 目录：`/dav`。
7. 输入所选认证模式对应的凭据。

### Raidrive

1. 添加新的 NAS/WebDAV 驱动器。
2. URL：`http://localhost:8080/dav`。
3. 选择驱动器号。
4. 使用所选认证模式对应的凭据连接。

## Linux

### GNOME Files

1. 打开 Files。
2. 选择 **其他位置**。
3. 输入 `dav://localhost:8080/dav`。
4. 连接。

### KDE Dolphin

在地址栏输入：

```text
webdav://localhost:8080/dav
```

### davfs2

```bash
sudo apt install davfs2
sudo mkdir -p /mnt/nas
sudo mount -t davfs http://localhost:8080/dav /mnt/nas
sudo umount /mnt/nas
```

可选 `/etc/fstab` 条目：

```fstab
http://localhost:8080/dav  /mnt/nas  davfs  _netdev,user,noauto  0  0
```

凭据文件：

```text
http://localhost:8080/dav  <mnemonas-or-webdav-username>  <mnemonas-or-webdav-password>
```

### rclone

```bash
rclone config
```

交互式配置值：

```text
n) New remote
name> mnemonas
Storage> webdav
url> http://localhost:8080/dav
vendor> other
user> <mnemonas-or-webdav-username>
pass> <mnemonas-or-webdav-password>
```

挂载：

```bash
rclone mount mnemonas: /mnt/nas --vfs-cache-mode full
```

后台挂载：

```bash
rclone mount mnemonas: /mnt/nas --daemon --vfs-cache-mode full
```

## iOS 和 iPadOS

### 文件 App

1. 打开 **文件** App。
2. 点击菜单按钮。
3. 选择 **连接服务器**。
4. 输入 `http://192.168.x.x:8080/dav`。
5. 输入所选认证模式对应的凭据。

### Documents by Readdle

1. 添加新连接。
2. 选择 WebDAV。
3. 输入服务器 URL 和所选认证模式对应的凭据。

## Android

### Solid Explorer

1. 添加云连接。
2. 选择 WebDAV。
3. 输入 `http://192.168.x.x:8080/dav`。
4. 输入所选认证模式对应的凭据。

### Cx 文件管理器

1. 打开 **网络**。
2. 添加远程存储。
3. 选择 WebDAV 并输入服务器 URL。
4. 输入所选认证模式对应的凭据。

### Total Commander

安装 WebDAV 插件，然后用所选认证模式对应的凭据添加 WebDAV 连接。

## 故障排查

### 连接被拒绝

检查：

```bash
curl http://localhost:8080/health
```

从其他设备连接时，应使用服务器局域网 IP，而不是 `localhost`。同时检查防火墙和端口映射。

### Windows 无法通过 HTTP 连接

启用 Windows WebClient 服务的 HTTP Basic Auth，或改用 HTTPS。

```powershell
Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\WebClient\Parameters" -Name "BasicAuthLevel" -Value 2
Restart-Service WebClient
```

### 大文件上传失败

davfs2 可在 `/etc/davfs2/davfs2.conf` 中提高缓存设置：

```text
cache_size  1024
buf_size    256
```

rclone：

```bash
rclone copy localfile mnemonas:/ --size-only
```

使用 HTTPS 时，还应检查反向代理上传限制。

### macOS Finder 响应慢

Finder 会频繁发送 `PROPFIND` 请求。处理较大的目录时，可改用 Transmit、Cyberduck 或 rclone。

### 锁定告警

MnemoNAS 实现虚拟 WebDAV 锁，主要用于客户端兼容。
如果客户端报告锁定问题，可刷新客户端、重新挂载共享，并检查是否有其他客户端正在编辑同一文件。

## 更多资源

- [WebDAV 客户端兼容性](webdav-compatibility.md)
- [配置参考](configuration.md)
- [FAQ](faq.md)
