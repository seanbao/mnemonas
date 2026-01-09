# 常见问题

[English](faq.en.md) | 简体中文

## 安装与部署

### 支持哪些操作系统？

| 平台 | 支持状态 |
| --- | --- |
| Linux x86_64 | 长期运行主路径 |
| Linux ARM64 | 长期运行主路径 |
| macOS Apple Silicon | 支持开发和本地运行 |
| macOS Intel | 支持开发和本地运行 |
| Windows | 通过 WSL2 支持 |

### systemd、Docker 和手动二进制部署有什么区别？

| 方式 | 优点 | 取舍 |
| --- | --- | --- |
| Linux/systemd | 开机自启、日志清晰、诊断脚本完整，适合长期运行的服务器 | 主要面向 Linux 主机 |
| Docker | 设置步骤少、运行环境隔离、升级路径明确 | 需要 Docker 环境，并正确映射数据卷 |
| 手动二进制 | 适合调试，流程直接 | 需要手动管理进程 |

长期运行的服务部署应参考 [Linux/systemd 部署指南](linux-systemd-deployment.md)。快速评估或已有容器平台时，可使用 [Docker 部署指南](docker-deployment.md)。

### 升级流程是什么？

Docker 源码 checkout：

```bash
docker compose build --pull
docker compose up -d
```

Docker release 镜像：

```bash
docker compose pull
docker compose up -d --no-build
```

升级 Docker release 镜像前，应记录 `.env` 中当前的 `MNEMONAS_IMAGE` 标签。若升级后容器无法启动、核心流程异常或健康检查失败，可把 `MNEMONAS_IMAGE` 改回上一版本标签后运行：

```bash
docker compose pull
docker compose up -d --no-build
docker compose logs --tail 100 mnemonas
```

Docker 回退只切换镜像，继续使用同一个宿主机数据目录。若新版本已执行不可逆数据迁移，应先按对应 release note 或备份恢复流程处理。

Ubuntu/systemd：

```bash
tar -xzf mnemonas-<version>-linux-amd64.tar.gz
cd mnemonas-<version>-linux-amd64

sudo ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

systemd 升级前应完成备份，并保留上一版本 release 解压目录。若升级后服务无法启动、核心流程异常或 `mnemonas-doctor` 失败，可从上一版本目录重新运行安装脚本，回退二进制和 Web UI 资源：

```bash
cd mnemonas-<previous-version>-linux-amd64
sudo ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

回退继续使用现有 `/etc/mnemonas/config.toml` 和 `/srv/mnemonas` 数据目录。若新版本已执行不可逆数据迁移，应按对应 release note 或备份恢复流程处理，不应直接用旧版本读取迁移后的数据。

手动二进制：

```bash
pkill nasd
pkill dataplane
./dataplane --data-dir ~/.mnemonas/.mnemonas/objects &
./nasd --config ~/.mnemonas/config.toml
```

所有部署路径在跨大版本升级前都应先完成备份。

### 数据存储在哪里？

默认直接运行布局：

- 用户文件：`~/.mnemonas/files/`
- 内部数据：`~/.mnemonas/.mnemonas/`
- 配置文件：`~/.mnemonas/config.toml`

Ubuntu/systemd 默认布局：

- 数据目录：`/srv/mnemonas`
- 配置文件：`/etc/mnemonas/config.toml`

Docker 默认布局：

- 宿主机 `~/.mnemonas` 映射到容器内 `/data`
- 内部数据位于 `/data/.mnemonas`

## WebDAV

### WebDAV 挂载后较慢时应检查什么？

常见原因：

- macOS Finder 会发送大量 `PROPFIND` 请求。可改用 Transmit、Cyberduck 或 rclone。
- Windows File Explorer 受 WebClient 限制影响。可改用 WinSCP、Cyberduck、Raidrive 或 rclone。
- 网络延迟较高。处理大文件时，服务器和客户端应尽量位于同一局域网。
- 反向代理启用了请求缓冲或请求体限制过小。公网 HTTPS 部署应关闭缓冲，并提高上传大小和超时限制。

MnemoNAS 包含短时间 `PROPFIND` 缓存，但客户端行为仍会影响体验。

### Windows 为什么无法连接 HTTP WebDAV？

Windows 默认偏好 HTTPS。本地 HTTP 测试可用管理员权限运行 PowerShell：

```powershell
Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\WebClient\Parameters" -Name "BasicAuthLevel" -Value 2
Restart-Service WebClient
```

常规使用应部署 HTTPS。

### 如何启用 WebDAV 认证？

```toml
[webdav]
enabled = true
prefix = "/dav"
auth_type = "users"
```

如需单独的全局 WebDAV 凭据，可使用：

```toml
[webdav]
auth_type = "basic"
username = "admin"
password = "" # 留空使用生成的凭据；自定义时使用密码管理器生成的随机强密码
```

`password` 为空时，MnemoNAS 会生成 WebDAV 密码并保存到 `<storage.root>/secrets.json`。

### 是否支持 HTTPS？

支持内置 TLS：

```toml
[server.tls]
enabled = true
auto_generate = true
```

公网访问建议使用反向代理：

```nginx
server {
    listen 443 ssl;
    server_name nas.example.com;

    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

参见 [反向代理配置](reverse-proxy-setup.md)。

## 文件与存储

### 误删文件后的恢复方式是什么？

使用 Web UI 的回收站和版本历史：

1. 打开 `http://localhost:8080`。
2. 进入原始目录。
3. 使用回收站或文件版本历史。
4. 恢复目标项目或版本。

API 示例：

`path` 查询值在可复制的 shell 或浏览器示例中应进行 URL 编码，例如 `/path/to/file` 对应 `%2Fpath%2Fto%2Ffile`。

```bash
curl -H "Authorization: Bearer <access-token>" \
  http://localhost:8080/api/v1/versions/path/to/file

curl -X POST \
  -H "Authorization: Bearer <access-token>" \
  "http://localhost:8080/api/v1/versions/<hash>/restore?path=%2Fpath%2Fto%2Ffile"
```

### 去重如何工作？

当前版本内容存储在 BLAKE3 整对象 CAS 中：

- 完全相同的完整文件内容只存储一次。
- Rust dataplane 已提供 FastCDC 文件 API，典型块大小为 256KB-4MB。
- 当前 Go 版本历史路径尚未接入 chunk 级引用追踪，因此不同版本只有在完整内容相同时才会共享存储。

Dataplane 统计：

```bash
curl http://localhost:9091/stats
```

`9091` 是本地或私有 dataplane 健康与统计端口，不应对公网开放。

### 旧版本如何清理？

保留策略配置：

```toml
[storage.retention]
max_age = "720h"
max_versions = 10
```

手动触发 GC：

```bash
curl -X POST \
  -H "Authorization: Bearer <access-token>" \
  http://localhost:8080/api/v1/maintenance/gc
```

### 最大文件大小是多少？

MnemoNAS 使用流式路径。实际限制来自磁盘空间、客户端、反向代理设置和底层文件系统。

大文件路径应按预期的上传、下载和恢复负载测试。公网部署必须配置反向代理参数，例如 Nginx `client_max_body_size`、`proxy_request_buffering` 和超时时间。

## 性能与维护

### 如何监控服务状态？

健康检查：

```bash
curl http://localhost:8080/health
```

指标：

```bash
curl -H "Authorization: Bearer <admin-access-token>" http://localhost:8080/api/v1/metrics
```

Dataplane 本地统计：

```bash
curl http://localhost:9091/stats
```

Dataplane 端口应保持 loopback 或私有网络绑定。

### Scrub 检查的作用是什么？

Scrub 会按哈希校验已存储对象，并报告缺失或损坏的数据。

```bash
curl -X POST \
  -H "Authorization: Bearer <access-token>" \
  http://localhost:8080/api/v1/maintenance/scrub

curl -H "Authorization: Bearer <access-token>" \
  http://localhost:8080/api/v1/maintenance/scrub
```

Scrub 应定期运行，例如每月一次。

### MnemoNAS 数据应如何备份？

使用一致性来源：

1. 使用 ZFS、Btrfs 或 LVM 时，优先从文件系统快照备份。
2. 否则，停止 `mnemonas` 和 `mnemonas-dataplane`，备份完整存储根目录，然后再启动服务。

Docker 冷备份示例：

```bash
docker compose stop
rsync -aHAX --delete ~/.mnemonas/ /backup/mnemonas/
docker compose start
```

远程备份可从快照或冷备份根目录使用 restic、borg 或 rclone。

参见 [备份指南](backup-guide.md)。

## 故障排除

### 服务无法启动

检查：

```bash
lsof -i :8080
lsof -i :9090
ls -la ~/.mnemonas/
```

日志：

```bash
docker compose logs -f
./nasd 2>&1 | tee nasd.log
```

systemd：

```bash
sudo mnemonas-doctor
journalctl -u mnemonas -f
journalctl -u mnemonas-dataplane -f
```

### 控制面无法连接数据面

检查 dataplane 健康状态：

```bash
curl http://localhost:9091/health
```

检查配置：

```toml
[dataplane]
grpc_address = "localhost:9090"
```

还应检查防火墙，并确认 dataplane 是否绑定到 loopback。

### 如何重置所有数据？

此操作具有破坏性：

```bash
docker compose down
DEFAULT_DATA_DIR="$HOME/.mnemonas"
DATA_DIR="${MNEMONAS_DATA_DIR:-$DEFAULT_DATA_DIR}"
[ "$DATA_DIR" = "$DEFAULT_DATA_DIR" ] || { echo "refusing non-default DATA_DIR; inspect and delete manually: $DATA_DIR"; exit 1; }
[ ! -L "$DATA_DIR" ] || { echo "refusing symlink DATA_DIR: $DATA_DIR"; exit 1; }
rm -rf -- "$DATA_DIR/files" "$DATA_DIR/.mnemonas"

# release 镜像可改用 docker compose up -d --no-build。
docker compose up -d
```

现有数据仍可能需要保留时，应先完成备份。

## 更多帮助

- [README](../README.md)
- [文档索引](README.md)
- [挂载指南](mounting-guide.md)
- [WebDAV 兼容性](webdav-compatibility.md)
- [配置参考](configuration.md)
- [支持说明](../SUPPORT.md)
