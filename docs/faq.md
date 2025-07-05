# 常见问题 (FAQ)

[English](faq.en.md) | 简体中文

## 📦 安装与部署

### Q: 支持哪些操作系统？

**A:** MnemoNAS 支持以下平台：

| 平台 | 支持状态 |
| ---- | -------- |
| Linux (x86_64) | 长期运行主路径 |
| Linux (ARM64) | 长期运行主路径 |
| macOS (Apple Silicon) | 支持开发、本地运行和手动二进制 |
| macOS (Intel) | 支持开发、本地运行和手动二进制 |
| Windows | 通过 WSL2 支持 |

### Q: Ubuntu/systemd、Docker 和手动二进制部署有什么区别？

**A:**

| 方式 | 优点 | 缺点 |
| ---- | ---- | ---- |
| **Linux/systemd** | 开机自启、日志清晰、适合长期运行、诊断脚本完整 | 主要面向 Linux 主机 |
| **Docker** | 启动简单、隔离性好、易于升级 | 需要 Docker 环境，存储路径需正确挂载 |
| **手动二进制** | 无依赖、适合调试 | 需手动管理进程，不推荐长期无人值守 |

长期运行优先参考 [Linux/systemd 部署指南](linux-systemd-deployment.md)。临时试用或已有容器平台时使用 Docker。

### Q: 如何更新到新版本？

**A:**

Docker 方式：

```bash
# 源码 checkout 默认本地构建
docker compose build --pull
docker compose up -d

# 如果使用已公开的 release 镜像，则改用：
# docker compose pull
# docker compose up -d --no-build
```

Docker release 镜像升级前应记录 `.env` 中当前 `MNEMONAS_IMAGE` 标签。若升级后容器无法启动、核心流程异常或健康检查失败，可把 `MNEMONAS_IMAGE` 改回上一版本标签后执行：

```bash
docker compose pull
docker compose up -d --no-build
docker compose logs --tail 100 mnemonas
```

Docker 回退只切换镜像，继续使用同一个宿主机数据目录。若新版本已执行不可逆数据迁移，应先按对应 release note 或备份恢复流程处理。

Ubuntu/systemd 方式：

```bash
tar -xzf mnemonas-<version>-linux-amd64.tar.gz
cd mnemonas-<version>-linux-amd64

sudo ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

systemd 升级前建议完成备份，并保留上一版本 release 解压目录。若升级后服务无法启动、核心流程异常或 `mnemonas-doctor` 失败，可从上一版本目录重新运行安装脚本回退二进制和 Web UI：

```bash
cd mnemonas-<previous-version>-linux-amd64
sudo ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

回退继续使用现有 `/etc/mnemonas/config.toml` 和 `/srv/mnemonas` 数据目录。若新版本已执行不可逆数据迁移，应按对应 release note 或备份恢复流程处理，不应直接用旧版本读取迁移后的数据。

手动二进制方式：

```bash
pkill nasd
pkill dataplane
./dataplane --data-dir ~/.mnemonas/.mnemonas/objects &
./nasd --config ~/.mnemonas/config.toml
```

所有部署路径在跨大版本升级前都应先完成备份。

### Q: 数据存储在哪里？

**A:** 默认路径：

- **数据文件**（用户文件）：`~/.mnemonas/files/`
- **内部数据**（CAS/元数据）：`~/.mnemonas/.mnemonas/`
- **配置文件**：`~/.mnemonas/config.toml`

Linux/systemd 部署默认使用 `/srv/mnemonas` 存放数据，配置文件在 `/etc/mnemonas/config.toml`。

Docker 部署时，宿主机 `~/.mnemonas` 通常映射到容器内 `/data`，内部数据位于 `/data/.mnemonas`。

---

## 🔌 WebDAV 相关

### Q: WebDAV 挂载后很慢？

**A:** 几个可能的原因和解决方案：

1. **macOS Finder**：Finder 的 WebDAV 实现效率较低，推荐使用 [Transmit](https://panic.com/transmit/) 或 [Cyberduck](https://cyberduck.io/)

2. **Windows Explorer**：WebClient 服务有限制，推荐使用 [WinSCP](https://winscp.net/) 或 [Raidrive](https://www.raidrive.com/)

3. **网络延迟**：确保服务器和客户端在同一局域网

4. **缓存未生效**：MnemoNAS 已内置 30 秒 PROPFIND 缓存

### Q: 为什么 Windows 无法连接 HTTP 的 WebDAV？

**A:** Windows 默认只允许 HTTPS。修改注册表启用 HTTP：

```powershell
# 以管理员身份运行
Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\WebClient\Parameters" -Name "BasicAuthLevel" -Value 2
Restart-Service WebClient
```

### Q: 如何启用 WebDAV 认证？

**A:** 编辑配置文件 `~/.mnemonas/config.toml`：

```toml
[webdav]
enabled = true
prefix = "/dav"
auth_type = "users"  # 使用 MnemoNAS 用户账号登录
```

如需单独的全局 WebDAV 凭据，可改用：

```toml
[webdav]
auth_type = "basic"
username = "admin"
password = ""  # 留空使用自动生成密码；自定义时使用密码管理器生成的随机强密码
```

重启服务后生效。

### Q: 支持 HTTPS 吗？

**A:** MnemoNAS 支持内置 HTTPS（自签名或自定义证书），也可使用反向代理：

```toml
[server.tls]
enabled = true
auto_generate = true
```

反向代理示例：

```nginx
# nginx 示例
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

---

## 📁 文件与存储

### Q: 误删了文件怎么办？

**A:** MnemoNAS 支持回收站和文件版本历史，可恢复删除或修改的文件。

1. 打开 Web UI：`http://localhost:8080`
2. 导航到文件所在目录
3. 右键点击 → **查看历史版本**
4. 选择要恢复的版本 → **恢复**

或通过 API：

```bash
# 获取文件历史版本
curl -H "Authorization: Bearer <access-token>" \
   http://localhost:8080/api/v1/versions/path/to/file

# 恢复到指定版本
curl -X POST \
   -H "Authorization: Bearer <access-token>" \
   "http://localhost:8080/api/v1/versions/<hash>/restore?path=/path/to/file"
```

### Q: 存储空间如何去重？

**A:** MnemoNAS 使用 CAS（内容寻址存储）+ CDC（内容定义分块）：

1. **内容寻址**：当前版本历史使用 BLAKE3 whole-object CAS，相同完整内容只存储一份
2. **分块能力**：Rust dataplane 已提供 FastCDC 文件 API，块大小通常在 256KB-4MB 区间
3. **版本去重边界**：当前 Go 版本历史路径还没有接入 chunk 级引用追踪；不同版本只有在完整内容相同时才会共享对象

查看 dataplane 统计：

```bash
curl http://localhost:9091/stats
```

`9091` 是 dataplane 本机健康/统计端口，只应在服务器本机或容器内部访问，不要通过防火墙、端口映射或反向代理暴露给外部网络。

### Q: 如何清理旧版本？

**A:** 默认情况下，MnemoNAS 按保留策略自动清理旧版本。可通过配置调整保留范围：

```toml
[storage.retention]
# 保留策略（满足任一条件即清理）
max_age = "720h"      # 保留 30 天内的版本
max_versions = 10     # 每个文件保留最近 10 个版本
```

手动触发 GC：

```bash
curl -X POST \
   -H "Authorization: Bearer <access-token>" \
   http://localhost:8080/api/v1/maintenance/gc
```

### Q: 最大支持多大的文件？

**A:** 设计上使用流式传输，文件大小主要受磁盘空间、客户端、反向代理上传限制和底层文件系统限制影响。当前不建议把“最大文件大小”理解为固定承诺；部署前应按实际媒体/备份文件做一次上传、下载和恢复演练。

大文件场景需要特别检查反向代理配置，例如 Nginx 的 `client_max_body_size`、`proxy_request_buffering` 和超时时间。Docker 或 systemd 本地直连时，也需要确认目标数据盘有足够空闲空间。

---

## ⚙️ 性能与维护

### Q: 如何监控服务状态？

**A:**

健康检查：

```bash
curl http://localhost:8080/health
# {"status":"healthy","version":"<version>",...}
```

性能指标：

```bash
curl -H "Authorization: Bearer <admin-access-token>" http://localhost:8080/api/v1/metrics
# 返回请求统计、延迟、吞吐量等
```

存储统计：

```bash
curl http://localhost:9091/stats
# 返回 CAS 存储统计
```

该端口没有面向外部客户端的认证层，长期部署时应保持 loopback 绑定。

### Q: Scrub 检查是做什么的？

**A:** Scrub 是数据完整性检查，验证每个存储块的哈希值是否正确：

```bash
# 手动触发 scrub
curl -X POST \
   -H "Authorization: Bearer <access-token>" \
   http://localhost:8080/api/v1/maintenance/scrub

# 查看最新 scrub 结果
curl -H "Authorization: Bearer <access-token>" \
   http://localhost:8080/api/v1/maintenance/scrub
```

建议每月运行一次 scrub，使用维护接口或脚本触发。

### Q: 如何备份 MnemoNAS 数据？

**A:**

1. **冷备份**：停止服务后复制数据目录

   ```bash
   docker compose stop
   rsync -aHAX --delete ~/.mnemonas/ /backup/mnemonas/
   docker compose start
   ```

2. **快照备份**：如果底层是 ZFS/Btrfs/LVM，先创建文件系统快照，再从快照目录同步。这样可以避免备份过程中元数据仍在变化。

3. **远程备份**：从快照目录或停服务后的数据目录，用 restic、borg 或 rclone 同步到云存储

   ```bash
   SOURCE_DIR=/path/to/mnemonas-snapshot-or-cold-root
   rclone sync "$SOURCE_DIR/" remote:mnemonas-backup/current/
   ```

没有快照能力时，不建议在服务运行中直接复制整个数据目录；优先停服务做冷备份。完整流程见 [备份指南](backup-guide.md)。

---

## 🔧 故障排除

### Q: 服务无法启动

**A:** 检查清单：

1. **端口占用**：

   ```bash
   lsof -i :8080  # 检查 8080 端口
   lsof -i :9090  # 检查 9090 端口
   ```

2. **数据目录权限**：

   ```bash
   ls -la ~/.mnemonas/
   # 确保当前用户有读写权限
   ```

3. **查看日志**：

   ```bash
   # Docker
   docker compose logs -f
   
   # 二进制
   ./nasd 2>&1 | tee nasd.log
   ```

### Q: 数据面连接失败

**A:** 控制面无法连接数据面（gRPC）：

1. 确认数据面运行中：

   ```bash
   curl http://localhost:9091/health
   ```

   如果这条命令只能从服务器本机访问，这是正常且推荐的部署边界。

2. 检查配置中的数据面地址：

   ```toml
   [dataplane]
   grpc_address = "localhost:9090"
   ```

3. 检查防火墙

### Q: 如何重置所有数据？

**A:** ⚠️ **警告：此操作不可逆！**

```bash
# 停止服务
docker compose down

# 删除数据
DEFAULT_DATA_DIR="$HOME/.mnemonas"
DATA_DIR="${MNEMONAS_DATA_DIR:-$DEFAULT_DATA_DIR}"
[ "$DATA_DIR" = "$DEFAULT_DATA_DIR" ] || { echo "refusing non-default DATA_DIR; inspect and delete manually: $DATA_DIR"; exit 1; }
[ ! -L "$DATA_DIR" ] || { echo "refusing symlink DATA_DIR: $DATA_DIR"; exit 1; }
rm -rf -- "$DATA_DIR/files" "$DATA_DIR/.mnemonas"

# 重启服务；release 镜像可改用 docker compose up -d --no-build
docker compose up -d
```

---

## 📖 更多帮助

- [项目主页](https://github.com/seanbao/mnemonas)
- [挂载指南](mounting-guide.md)
- [WebDAV 兼容性](webdav-compatibility.md)
- [配置参考](../mnemonas.example.toml)

遇到其他问题？请在 [GitHub Issues](https://github.com/seanbao/mnemonas/issues) 提交反馈。
