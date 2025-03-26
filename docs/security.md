# 安全加固指南

本文档介绍 MnemoNAS 的安全配置最佳实践，适用于局域网和公网部署场景。

## 🔐 认证配置

### Basic Auth（当前支持）

编辑 `~/.mnemonas/config.toml`：

```toml
[webdav]
enabled = true
auth_type = "basic"
username = "admin"
password = ""  # 留空则首次启动时自动生成
```

**自动生成密码**：

- 首次启动时，如果 `password` 为空，系统会自动生成 16 位随机密码
- 密码会显示在启动日志中，并保存到 `<storage_root>/secrets.json`
- 浏览器端不会通过 setup API 或弹窗回传初始密码
- 后续启动会自动使用保存的密码

**手动设置密码**（如需自定义）：

```toml
[webdav]
password = "your-strong-password"  # 至少 16 字符，混合大小写、数字、符号
```

**密码强度建议**：

- 长度 ≥ 16 字符
- 包含大小写字母、数字、特殊符号
- 使用密码管理器生成

### 禁用认证（仅限本地开发）

```toml
[webdav]
auth_type = "none"

[server]
host = "127.0.0.1"  # 仅本地访问
```

⚠️ **警告**：禁用认证时必须将 host 设为 `127.0.0.1`，否则任何人都能访问。

---

## 🌐 网络监听配置

### 场景一：仅本地访问

```toml
[server]
host = "127.0.0.1"
port = 8080
```

适用于：开发测试、单机使用

### 场景二：局域网访问

```toml
[server]
host = "0.0.0.0"    # 监听所有接口
port = 8080

[webdav]
auth_type = "basic"  # 必须启用认证
username = "family"
password = "your-password"
```

**防火墙配置**：

```bash
# Ubuntu/Debian
sudo ufw allow from 192.168.0.0/24 to any port 8080

# CentOS/RHEL
sudo firewall-cmd --add-rich-rule='rule family="ipv4" source address="192.168.0.0/24" port port="8080" protocol="tcp" accept' --permanent
sudo firewall-cmd --reload
```

### 场景三：公网访问（不推荐直接暴露）

**强烈建议**使用反向代理 + HTTPS，而不是直接暴露 MnemoNAS。

---

## 🔒 HTTPS 配置

MnemoNAS 不直接处理 TLS，推荐使用反向代理：

### 方案一：Nginx + Let's Encrypt

```nginx
server {
    listen 80;
    server_name nas.example.com;
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl http2;
    server_name nas.example.com;

    ssl_certificate /etc/letsencrypt/live/nas.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/nas.example.com/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    # WebDAV 需要大上传支持
    client_max_body_size 0;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # WebDAV 特殊头
        proxy_pass_request_headers on;
    }
}
```

使用 Certbot 获取证书：

```bash
sudo certbot --nginx -d nas.example.com
```

### 方案二：Caddy（自动 HTTPS）

```caddyfile
nas.example.com {
    reverse_proxy localhost:8080
}
```

Caddy 自动获取和续期 Let's Encrypt 证书。

### 方案三：Cloudflare Tunnel（零公网 IP）

适合家庭网络没有公网 IP 的场景：

```bash
# 安装 cloudflared
curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 -o /usr/local/bin/cloudflared
chmod +x /usr/local/bin/cloudflared

# 登录并创建隧道
cloudflared tunnel login
cloudflared tunnel create mnemonas

# 配置隧道
cat > ~/.cloudflared/config.yml << EOF
tunnel: <tunnel-id>
credentials-file: ~/.cloudflared/<tunnel-id>.json

ingress:
  - hostname: nas.example.com
    service: http://localhost:8080
  - service: http_status:404
EOF

# 运行隧道
cloudflared tunnel run mnemonas
```

---

## 🛡️ 安全检查清单

### 部署前检查

- [ ] 已记录首次启动时显示的自动生成密码（或已设置自定义密码）
- [ ] `auth_type` 不是 `none`（除非仅本地访问）
- [ ] 如果 `host = "0.0.0.0"`，已配置防火墙
- [ ] 生产环境使用 HTTPS

### 运行时检查

```bash
# 检查监听端口
ss -tlnp | grep 8080

# 检查外部可访问性（从另一台机器）
curl http://<server-ip>:8080/health

# 检查认证是否生效
curl http://<server-ip>:8080/dav/
# 应该返回 401 Unauthorized
```

### 定期维护

- [ ] 定期运行 Scrub 检查数据完整性
- [ ] 定期备份数据目录
- [ ] 检查日志中的异常访问
- [ ] 更新到最新版本

---

## 🚫 已知安全限制

### 当前版本

#### LOCK/UNLOCK 是虚拟实现

- 不提供真正的并发编辑保护
- 多用户编辑同一文件时需协调

#### 多用户权限边界

- 当前版本已支持多用户与角色（admin/user/guest）
- 文件与目录权限隔离尚未实现，所有登录用户共享同一数据空间

#### 速率限制粒度

- 内置并发请求限制（默认 100 并发）
- 未提供按 IP/用户的细粒度速率限制
- 可通过反向代理实现更细粒度限制

示例（Nginx）：

```nginx
limit_req_zone $binary_remote_addr zone=api:10m rate=10r/s;
location /api/ {
    limit_req zone=api burst=20;
    proxy_pass http://localhost:8080;
}
```

#### 浏览器预览鉴权参数

- 文件预览/播放允许通过 `auth` 查询参数携带访问令牌（仅 `GET`/`HEAD` 且限定下载/缩略图路径）
- URL 可能出现在浏览器历史、代理日志或监控系统中
- 建议仅用于前端预览场景，避免将包含令牌的链接分享或长期保存

### 路线图

| 版本 | 安全特性 |
| ---- | -------- |
| v0.1.0 | Basic Auth, 路径遍历保护, 只读模式 |
| v0.2.0 | 多用户支持, 权限控制 |
| v0.3.0 | OAuth/OIDC 集成 |

---

## 📖 更多资源

- [Docker 部署指南](docker-deployment.md) - 包含反向代理配置示例
- [FAQ](faq.md) - 常见安全问题
- [配置参考](../mnemonas.example.toml)
