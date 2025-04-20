# 安全加固指南

本文档介绍 MnemoNAS 的安全配置最佳实践，适用于局域网和公网部署场景。

## 🔐 认证配置

### Web UI 管理账号

默认启用 Web UI 登录认证。首次启动且没有用户数据时，系统会创建一个管理员账号，并把初始密码写入：

```text
<storage.root>/.mnemonas/initial-password.txt
```

注意：

- 初始管理员密码不会长期保存在 `secrets.json`
- setup API 不返回初始用户名或密码
- 首次成功登录对应管理员账号后，`initial-password.txt` 会自动删除
- 登录后应立即修改管理员密码

systemd 部署默认路径：

```bash
sudo cat /srv/mnemonas/.mnemonas/initial-password.txt
```

Docker 默认路径：

```bash
cat ~/.mnemonas/.mnemonas/initial-password.txt
```

### WebDAV Basic Auth

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
- 该密码用于 WebDAV 客户端，不是 Web UI 管理员密码
- 密码会保存到 `<storage_root>/secrets.json`，启动日志只提示文件路径，不输出明文密码
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
[auth]
enabled = false

[webdav]
auth_type = "none"

[server]
host = "127.0.0.1"  # 仅本地访问
```

⚠️ **警告**：`auth.enabled = false` 会关闭 Web UI/API 登录；`webdav.auth_type = "none"` 会关闭 WebDAV Basic Auth。禁用任一认证时必须将 `host` 设为 `127.0.0.1`，否则任何人都能访问。

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
sudo ufw deny 9090/tcp comment "MnemoNAS dataplane gRPC"
sudo ufw deny 9091/tcp comment "MnemoNAS dataplane HTTP"

# CentOS/RHEL
sudo firewall-cmd --add-rich-rule='rule family="ipv4" source address="192.168.0.0/24" port port="8080" protocol="tcp" accept' --permanent
sudo firewall-cmd --reload
```

### 场景三：公网访问（不推荐直接暴露）

**强烈建议**使用反向代理 + HTTPS，而不是直接暴露 MnemoNAS。

---

## 🔒 HTTPS 配置

MnemoNAS 支持内置 TLS 配置，但家庭和公网部署更推荐使用 Caddy/Nginx/Traefik 等反向代理统一处理 HTTPS、证书续期和上传限制。

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
curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 -o /tmp/cloudflared
sudo install -m 0755 /tmp/cloudflared /usr/local/bin/cloudflared

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

- [ ] 已通过服务器端 `initial-password.txt` 完成首次 Web UI 登录，并已修改管理员密码
- [ ] 已记录 WebDAV Basic Auth 凭据，或已设置自定义强密码
- [ ] `auth_type` 不是 `none`（除非仅本地访问）
- [ ] 如果 `host = "0.0.0.0"`，已配置防火墙
- [ ] dataplane gRPC/HTTP 端口保持在 `127.0.0.1` 或受信私有网络内，没有直接暴露到公网
- [ ] systemd 部署已运行 `sudo mnemonas-doctor`，并确认没有 dataplane 端口暴露或 UFW 放行警告
- [ ] 生产环境使用 HTTPS

### 运行时检查

```bash
# 检查监听端口
ss -tlnp | grep 8080
ss -tlnp | grep -E '9090|9091'

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
- 非管理员用户按账号 `home_dir` 限制文件、搜索、收藏、分享、回收站与活动日志范围
- WebDAV Basic Auth 是全局服务凭据，不携带应用层用户身份；需要按用户隔离时应使用 Web UI/API 账号访问

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

- 文件下载、版本预览、音视频预览与外部打开使用短期 `HttpOnly` download-session cookie，不再通过 URL 查询参数传递长期访问令牌
- 该 cookie 由已认证会话在登录、初始化或刷新令牌后同步到 `/api/v1` 路径，并覆盖下载与缩略图请求
- 内部文件预览与缩略图链路不再依赖 `auth` 查询参数
- `Secure` 标记只会在实际 HTTPS，或显式启用 `trusted_proxy_hops > 0` 且请求直接来自 loopback / 私有网段代理并携带 `X-Forwarded-Proto=https` 时启用，避免公网请求伪造 HTTPS 语义

#### Web UI 会话令牌

- 当前 Web UI 的访问令牌和刷新令牌保存在浏览器 `localStorage` 中，用于 REST API 与上传请求的 `Authorization` 头
- 服务端已设置基础安全响应头、CSP 与 `Permissions-Policy`，并且下载/预览链路使用短期 `HttpOnly` cookie，降低令牌出现在 URL 或日志里的风险
- `localStorage` 仍然会被同源 XSS 读取；公网部署必须使用受信任的静态资源、HTTPS 反向代理和较新的浏览器，不要在同一域名下注入第三方脚本
- 共用电脑上使用后应主动退出登录；管理员修改密码、删除或禁用用户会撤销该用户的刷新令牌
- 长期方向是把主 Web 会话也迁移到 `HttpOnly` cookie 或同等风险更低的会话机制

#### 公开分享密码验证

- 受密码保护的公开分享在浏览器完成一次密码验证后，会下发同路径 `HttpOnly` cookie
- 文件夹浏览与文件下载依赖该 cookie，不再通过 URL 查询参数传递分享密码
- 清除浏览器站点数据、切换浏览器或密码变更后，需要重新输入分享密码
- 同一 share 与客户端地址组合连续 5 次口令失败后，会锁定 5 分钟并返回 `429 Too Many Requests`
- 客户端地址默认不信任转发头，始终使用直连来源；只有显式设置 `server.trusted_proxy_hops > 0` 且请求直接来自 loopback 或私有网段代理时，才按 `X-Forwarded-For` 从右侧回溯客户端地址。多跳代理部署需要设置为代理总层数
- `Secure` cookie 标记同样只在实际 HTTPS 或受信代理转发的 HTTPS 请求上启用

### 安全能力状态

| 状态 | 安全特性 |
| ---- | -------- |
| 已支持 | Web UI 登录、多用户与角色、用户 home 目录隔离、WebDAV Basic Auth、路径遍历保护、WebDAV 只读模式、公开分享密码验证与失败锁定 |
| 建议通过反向代理补充 | HTTPS 证书自动续期、按 IP/用户限速、公网访问控制 |
| 计划中 | OAuth/OIDC 集成、更细粒度的应用层访问策略 |

---

## 📖 更多资源

- [Docker 部署指南](docker-deployment.md) - 包含反向代理配置示例
- [FAQ](faq.md) - 常见安全问题
- [配置参考](../mnemonas.example.toml)
