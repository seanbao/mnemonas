# 安全加固指南

[English](security.en.md) | 简体中文

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
- 默认不会把初始密码明文打印到终端；只提示 `initial-password.txt` 路径。仅本地受控调试时，可在首次启动前设置 `MNEMONAS_PRINT_INITIAL_PASSWORD=1` 临时打印
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

### WebDAV 认证

编辑 `~/.mnemonas/config.toml`：

```toml
[webdav]
enabled = true
auth_type = "users"
```

推荐使用 `auth_type = "users"`：WebDAV 客户端用 MnemoNAS 用户名和密码登录，管理员访问全局目录，普通用户的挂载根目录映射到自己的 `home_dir`，guest 账号只读，用户配额会限制 WebDAV PUT/COPY 写入。

如需兼容旧配置或单独的服务凭据，可使用全局 Basic Auth：

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

⚠️ **警告**：`auth.enabled = false` 会关闭 Web UI/API 登录；`webdav.auth_type = "none"` 会关闭 WebDAV 认证。禁用任一认证时必须将 `host` 设为 `127.0.0.1`。如果非 loopback 监听确实由外层防火墙、容器端口绑定或反向代理限制访问范围，必须显式设置 `security.allow_unsafe_no_auth = true` 才能通过配置校验。

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
auth_type = "users"  # 必须启用认证
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

推荐操作路径：

1. 在服务器上运行 `sudo mnemonas-public-setup --proxy caddy <domain> <email>` 或 `sudo mnemonas-public-setup --proxy nginx <domain> <email>`，生成并安装反向代理配置。
2. 登录 Web UI 后打开 `设置 -> 常规 -> 公网访问向导`，根据部署方式应用 `server.host`、`trusted_proxy_hops`、分享域名等配置。
3. 在 Web UI 运行“安全自检”，确认认证、HTTPS 请求语义、受信代理、监听地址、dataplane 端口、WebDAV 认证、分享 Base URL、初始密码文件和管理员账号备用状态。
4. 在服务器上运行 `sudo mnemonas-doctor --public-domain <domain>`，复核公网域名、反向代理和本机端口暴露情况。

Web UI 向导只负责 MnemoNAS 应用配置和操作提示；证书签发、防火墙、云安全组和反向代理安装仍应在服务器或云控制台完成。安全自检只能检查 MnemoNAS 进程能观察到的运行态和当前请求语义，不能替代云厂商安全组、真实公网端口和证书链检查。

---

## 🔒 HTTPS 配置

MnemoNAS 支持内置 TLS 配置，但公网或长期部署更推荐使用 Caddy/Nginx/Traefik 等反向代理统一处理 HTTPS、证书续期和上传限制。

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

适合没有公网 IP、但需要通过隧道访问的部署：

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
- [ ] WebDAV 使用 `auth_type = "users"`，或已记录全局 Basic Auth 凭据并设置自定义强密码
- [ ] `auth_type` 不是 `none`（除非仅本地访问）
- [ ] 公网部署时 `server.host = "127.0.0.1"`，只通过 HTTPS 反向代理访问
- [ ] dataplane gRPC/HTTP 端口保持在 `127.0.0.1` 或受信私有网络内，没有直接暴露到公网
- [ ] Web UI “安全自检”没有 `block` 项；公网部署前应处理所有 `warning`，尤其是 `allow_unsafe_no_auth`、反向代理 header、dataplane `9090/9091` 和备用管理员提醒
- [ ] systemd 部署已运行 `sudo mnemonas-doctor --public-domain <domain>`，并确认 HTTP 会跳转到 HTTPS、HTTPS 证书 hostname 匹配、30 天内不过期，续期路径已验证，且没有 Web 后端直连、dataplane 端口暴露或 UFW 放行警告
- [ ] 已按 [公网云防火墙复核清单](cloud-firewall-checklist.md) 确认云安全组或防火墙公网入口只开放 `80/443`；管理端口、Web 后端端口和 dataplane 端口不对公网开放
- [ ] 生产环境使用 HTTPS

### 运行时检查

```bash
# MnemoNAS 自检
sudo mnemonas-doctor --public-domain <domain>
# 该命令会检查 HTTPS health、HTTP 到 HTTPS 跳转、证书 hostname、证书 30 天有效期、证书续期提示、后端直连端口和 dataplane 端口

# 检查监听端口
ss -tlnp | grep 8080
ss -tlnp | grep -E '9090|9091'

# 公网 HTTPS 应可用
curl -I https://<domain>/health

# 公网直连后端端口应失败或超时
curl --connect-timeout 3 http://<domain>:8080/health

# 检查认证是否生效
curl https://<domain>/dav/
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
- 当前版本已支持用户组和 `storage.directory_access_rules` 目录授权；非管理员默认按账号 `home_dir` 限制访问，命中目录规则时按用户、用户组或角色授权放行
- 文件、搜索、收藏、分享、回收站、最近操作和 WebDAV `users` 模式使用同一套路径权限判定
- 管理员可为用户设置 `quota_bytes`；非管理员通过 Web/API 上传、复制、回收站恢复，以及 `webdav.auth_type = "users"` 下的 WebDAV PUT/COPY 会按该用户 `home_dir` 的当前用量执行服务端配额限制
- WebDAV `users` 模式携带应用层用户身份并执行角色、用户组、`home_dir` 和目录授权边界；`basic` 模式是全局服务凭据兼容模式，不携带应用层用户身份

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

- 文件下载、版本预览、音视频预览、缩略图与外部打开使用短期 `HttpOnly` download-session cookie，不再通过 URL 查询参数传递长期访问令牌
- 该 cookie 由已认证会话在登录、初始化或刷新令牌后同步到 `/api/v1` 路径，并覆盖下载与缩略图请求
- 内部文件预览与缩略图链路不再依赖 `auth` 查询参数
- `Secure` 标记只会在实际 HTTPS，或显式启用 `trusted_proxy_hops > 0` 且请求直接来自 loopback / 私有网段代理并携带 `X-Forwarded-Proto=https` 时启用，避免公网请求伪造 HTTPS 语义

#### Web UI 会话令牌

- Web UI 主会话使用 `HttpOnly`、`SameSite=Lax` cookie 保存访问令牌和刷新令牌，不再把 bearer token 写入 `localStorage`
- REST API、上传请求、刷新令牌与退出登录请求由浏览器自动携带同源 cookie；旧版本残留在 `localStorage` 的令牌会在初始化、刷新、登出等路径中清理
- 对带浏览器 `Origin` / `Referer` / `Sec-Fetch-Site` 元数据的 REST 写请求和 WebDAV 写方法（`POST`、`PUT`、`PATCH`、`DELETE`、`MKCOL`、`COPY`、`MOVE`、`PROPPATCH`、`LOCK`、`UNLOCK`），服务端会拒绝来源 scheme、主机或端口与当前请求不一致的请求，并拒绝浏览器明确标记为 `cross-site` 或 `same-site` 的无 `Authorization` 写请求；无浏览器来源头的脚本客户端以及显式 `Authorization` API 客户端继续可用
- API 客户端仍可使用 `Authorization: Bearer <access-token>` 与 JSON refresh token，兼容脚本和自动化调用
- 服务端已设置基础安全响应头、CSP 与 `Permissions-Policy`；文件下载、版本预览、缩略图、WebDAV 文件与 WebDAV 目录列表响应额外带 `X-Content-Type-Options: nosniff` 和 sandbox CSP，降低同源打开用户文件时的脚本执行面。公网部署仍必须使用受信任的静态资源、HTTPS 反向代理和较新的浏览器，不要在同一域名下注入第三方脚本
- 共用电脑上使用后应主动退出登录；修改密码、退出登录、删除用户、禁用用户或管理员手动让用户现有登录失效时，会撤销或清理对应会话

#### 公开分享密码验证

- 受密码保护的公开分享在浏览器完成一次密码验证后，会下发 `HttpOnly` cookie；cookie 只作用于对应的 `/s/<id>` 与 `/api/v1/public/shares/<id>` 路径
- 文件夹浏览与文件下载依赖该 cookie，不再通过 URL 查询参数传递分享密码
- 清除浏览器站点数据、切换浏览器或密码变更后，需要重新输入分享密码
- 同一 share 与客户端地址组合连续 5 次口令失败后，会锁定 5 分钟并返回 `429 Too Many Requests`
- 客户端地址默认不信任转发头，始终使用直连来源；只有显式设置 `server.trusted_proxy_hops > 0` 且请求直接来自 loopback 或私有网段代理时，才按 `X-Forwarded-For` 从右侧回溯客户端地址。多跳代理部署需要设置为代理总层数
- `Secure` cookie 标记同样只在实际 HTTPS 或受信代理转发的 HTTPS 请求上启用

### 安全能力状态

| 状态 | 安全特性 |
| ---- | -------- |
| 已支持 | Web UI 登录、多用户/角色/用户组、用户根目录隔离、目录授权、用户会话吊销、WebDAV 用户认证/全局 Basic Auth、路径遍历保护、WebDAV 只读模式、公开分享密码验证与失败锁定 |
| 建议通过反向代理补充 | HTTPS 证书自动续期、按 IP/用户限速、公网访问控制 |
| 计划中 | OAuth/OIDC 集成、更细粒度的应用层访问策略 |

---

## 📖 更多资源

- [Docker 部署指南](docker-deployment.md) - 包含反向代理配置示例
- [FAQ](faq.md) - 常见安全问题
- [配置参考](../mnemonas.example.toml)
