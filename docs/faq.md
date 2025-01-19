# 常见问题 (FAQ)

## 📦 安装与部署

### Q: 支持哪些操作系统？

**A:** MnemoNAS 支持以下平台：

| 平台 | 支持状态 |
|------|---------|
| Linux (x86_64) | ✅ 完全支持 |
| Linux (ARM64) | ✅ 完全支持 |
| macOS (Apple Silicon) | ✅ 完全支持 |
| macOS (Intel) | ✅ 完全支持 |
| Windows | 🔄 通过 WSL2 支持 |

### Q: Docker 和二进制部署有什么区别？

**A:**

| 方式 | 优点 | 缺点 |
|------|------|------|
| **Docker** | 开箱即用、隔离性好、易于升级 | 需要 Docker 环境 |
| **二进制** | 无依赖、性能略优 | 需手动管理进程 |

推荐普通用户使用 Docker 部署。

### Q: 如何更新到新版本？

**A:**

Docker 方式：
```bash
docker compose pull
docker compose up -d
```

二进制方式：
```bash
# 停止服务
pkill nasd
pkill dataplane

# 下载新版本并替换二进制文件
# ...

# 重启服务
./dataplane &
./nasd
```

### Q: 数据存储在哪里？

**A:** 默认路径：

- **数据文件**（用户文件）：`~/.mnemonas/files/`
- **内部数据**（CAS/元数据）：`~/.mnemonas/.mnemonas/`
- **配置文件**：`~/.mnemonas/config.toml`

Docker 部署时，存储根目录通常映射到容器内的 `/root/.mnemonas`，内部数据位于 `/root/.mnemonas/.mnemonas`。

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
auth_type = "basic"  # 启用 Basic Auth
username = "admin"
password = "your-password-here"
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

**A:** MnemoNAS 支持文件版本历史，可以轻松恢复删除或修改的文件！

1. 打开 Web UI：`http://localhost:8080`
2. 导航到文件所在目录
3. 右键点击 → **查看历史版本**
4. 选择要恢复的版本 → **恢复**

或通过 API：
```bash
# 获取文件历史版本
curl http://localhost:8080/api/v1/versions/path/to/file

# 恢复到指定版本
curl -X POST "http://localhost:8080/api/v1/versions/<hash>/restore?path=/path/to/file"
```

### Q: 存储空间如何去重？

**A:** MnemoNAS 使用 CAS（内容寻址存储）+ CDC（内容定义分块）：

1. **内容寻址**：相同内容只存储一份
2. **分块去重**：大文件切分为 256KB-4MB 的块，相似文件共享相同块
3. **版本共享**：文件的不同版本共享未修改的块

查看去重效果：
```bash
curl http://localhost:9091/stats
# 返回: {"dedup_ratio": 0.6, ...}  # 0.6 = 60% 去重率（节省 60% 存储）
```

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
curl -X POST http://localhost:8080/api/v1/maintenance/gc
```

### Q: 最大支持多大的文件？

**A:** 理论上无限制（受磁盘空间限制）。已测试：

- ✅ 100GB 单文件上传/下载
- ✅ 1TB+ 总存储容量

大文件使用流式传输，不会占用过多内存。

---

## ⚙️ 性能与维护

### Q: 如何监控服务状态？

**A:** 

健康检查：
```bash
curl http://localhost:8080/health
# {"status":"healthy","version":"0.1.0"}
```

性能指标：
```bash
curl http://localhost:8080/api/v1/metrics
# 返回请求统计、延迟、吞吐量等
```

存储统计：
```bash
curl http://localhost:9091/stats
# 返回 CAS 存储统计
```

### Q: Scrub 检查是做什么的？

**A:** Scrub 是数据完整性检查，验证每个存储块的哈希值是否正确：

```bash
# 手动触发 scrub
curl -X POST http://localhost:8080/api/v1/maintenance/scrub

# 查看 scrub 历史
curl http://localhost:8080/api/v1/maintenance/scrub/history
```

建议每月运行一次 scrub，使用维护接口或脚本触发。

### Q: 如何备份 MnemoNAS 数据？

**A:** 

1. **冷备份**：停止服务后复制数据目录
   ```bash
   docker compose stop
   cp -r ~/.mnemonas/files /backup/mnemonas-files
   cp -r ~/.mnemonas/.mnemonas /backup/mnemonas-internal
   docker compose start
   ```

2. **热备份**：使用 rsync 增量同步
   ```bash
   rsync -av ~/.mnemonas/ /backup/mnemonas/
   ```

3. **远程备份**：使用 rclone 同步到云存储
   ```bash
   rclone sync ~/.mnemonas/ remote:mnemonas-backup/
   ```

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
rm -rf ~/.mnemonas/files/*
rm -rf ~/.mnemonas/.mnemonas/*

# 重启服务
docker compose up -d
```

---

## 📖 更多帮助

- [项目主页](https://github.com/seanbao/mnemonas)
- [挂载指南](mounting-guide.md)
- [WebDAV 兼容性](webdav-compatibility.md)
- [配置参考](../mnemonas.example.toml)

遇到其他问题？请在 [GitHub Issues](https://github.com/seanbao/mnemonas/issues) 提交反馈。
