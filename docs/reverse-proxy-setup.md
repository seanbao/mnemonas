# 外网访问配置指南

[English](reverse-proxy-setup.en.md) | 简体中文

本文档介绍如何通过反向代理配置 MnemoNAS 的 HTTPS 外网访问。如果从一台公网服务器开始部署，优先按 [公网服务器快速上线](public-server-quickstart.md) 走推荐路径；本文保留 Caddy、Nginx、Traefik 的细节配置。

## 前置条件

- 一台公网服务器（或已配置好内网穿透）
- 域名已解析到服务器 IP
- 服务器开放 80/443 端口

## 推荐脚本

systemd 部署完成后，推荐用脚本自动生成公网 HTTPS 入口并收紧 MnemoNAS 后端监听：

```bash
sudo mnemonas-public-setup --proxy caddy nas.example.com admin@example.com
```

systemd 安装会把源码中的 `scripts/setup-reverse-proxy.sh` 安装为 `mnemonas-public-setup`。脚本会设置 `server.host = "127.0.0.1"`、`trusted_proxy_hops = 1`、配置 Caddy/Nginx、调整本机 UFW 规则并运行基础检查。调整 UFW 时，脚本会删除 `8080/9090/9091` 或自定义后端端口上的宽泛放行规则，再写入拒绝规则。云厂商安全组仍需人工确认只开放 `80/443`，不要开放 `8080/9090/9091` 或改过的后端端口。

脚本会将域名统一为小写，并移除单个 FQDN 尾点；规范化后的值会用于 Caddy/Nginx 配置、证书申请、WebDAV 地址和验证命令。

脚本要求 `--config` 使用绝对路径，且路径不能包含空白字符、控制字符、父目录段或符号链接组件，以保持与 `nasd` 配置文件安全检查一致。配置更新、后续 `mnemonas-doctor --public-domain` 诊断以及 WebDAV 地址解析都会使用同一个配置路径。

脚本完成摘要会读取配置中的 `webdav.prefix` 并输出对应的公网 WebDAV 地址；未配置时使用默认 `/dav`。显式配置为空字符串、根路径，或覆盖 `/api`、`/s`、`/health` 保留路由的前缀会被视为无效，因为它会与 Web UI/API 路由重叠。该前缀会按服务端 URL 路径规则规范化，例如去除首尾空白、折叠重复斜杠、处理 `.` 和 `..` 段。如果已把 WebDAV 前缀改为其他路径，验证命令中的 `/dav` 也应替换为实际前缀。

使用 `--skip-mnemonas-config` 或 `--no-firewall` 时，完成摘要会把对应项目标记为已跳过，并列出需要手动确认的配置。

如需通过 `MNEMONAS_UPSTREAM_HOST` 覆盖默认后端主机，该值必须是主机名、IPv4 或 IPv6 字面量，不包含协议、路径或端口；IPv6 可写作 `::1` 或 `[::1]`。

如果需要 Traefik 或 Cloudflare Tunnel，不建议临时拼命令；优先从仓库模板开始：

- `deploy/public-access/traefik/`：Linux 主机上运行 Traefik，MnemoNAS 仍由 systemd 监听 `127.0.0.1:8080`。
- `deploy/public-access/cloudflare-tunnel/config.yml`：无公网 IP 或希望通过 Cloudflare Tunnel 暴露 HTTPS 域名时使用。

模板说明见 [公网访问模板](../deploy/public-access/README.md)。复制模板后至少替换 `nas.example.com`、ACME 邮箱或 tunnel ID，并继续运行 `sudo mnemonas-doctor --public-domain <domain>`。

## MnemoNAS 配置

MnemoNAS 默认不信任 `X-Forwarded-*` 头。部署在受信反向代理后方时，需要在 `config.toml` 中设置代理层数：

```toml
[server]
trusted_proxy_hops = 1
```

单层 Caddy/Nginx/Traefik 设置为 `1`；多跳链路设置为代理总层数。直连来源为本机 loopback 时无需额外配置；如果代理来自 Docker 网桥、内网负载均衡或其他非 loopback 地址，还需要设置 `trusted_proxy_cidrs = ["<proxy-ip-or-cidr>"]`。修改后重启 `mnemonas`。未设置时，服务仍可通过反向代理访问，但登录、分享下载等 cookie 的 `Secure` 判断和按客户端 IP 的限流会按直连来源处理。

公网入口只应开放 80/443。`8080` 是 MnemoNAS Web/API/WebDAV 的默认直连端口；如果反向代理和 MnemoNAS 在同一台机器，建议把 `[server].host` 改为 `127.0.0.1` 或用防火墙限制直连端口只允许可信来源访问。`9090/9091` 是 dataplane 默认内部端口；如果 dataplane 端口已修改，也不要通过防火墙、端口映射或反向代理暴露到公网或不可信局域网。

## 方案一：Caddy（推荐）

Caddy 自动申请和续期 Let's Encrypt 证书，配置最简单。

### 1. 安装 Caddy

```bash
# Debian/Ubuntu
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https curl gnupg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update
sudo apt install caddy

# CentOS/RHEL
sudo yum install yum-plugin-copr
sudo yum copr enable @caddy/caddy
sudo yum install caddy
```

### 2. 配置 Caddyfile

编辑 `/etc/caddy/Caddyfile`：

```caddyfile
nas.example.com {
    # 自动 HTTPS（Let's Encrypt）
    
    # 反向代理到 MnemoNAS
    reverse_proxy localhost:8080 {
        # WebDAV 需要的 headers
        header_up Host {host}
        header_up X-Real-IP {remote_host}
        header_up X-Forwarded-For {remote_host}
        header_up X-Forwarded-Proto {scheme}
    }
    
    # 上传大文件支持（默认 10GB）
    request_body {
        max_size 10GB
    }
    
    # 日志
    log {
        output file /var/log/caddy/nas.log
        format json
    }
}
```

### 3. 启动服务

```bash
sudo systemctl enable caddy
sudo systemctl restart caddy

# 检查状态
sudo systemctl status caddy
```

### 4. 验证

```bash
# 检查 HTTPS 证书
curl -I https://nas.example.com/health

# 测试 WebDAV；auth_type=users 时使用 MnemoNAS 用户名和密码
# auth_type=basic 时使用设置页显示的 WebDAV 凭据，自动生成密码为 /srv/mnemonas/secrets.json 中的 webdav_password 字段
WEBDAV_USER="admin"
WEBDAV_PASS="<实际 WebDAV 密码>"
curl -u "$WEBDAV_USER:$WEBDAV_PASS" -X PROPFIND https://nas.example.com/dav/ -H "Depth: 0"
```

---

## 方案二：Nginx + Certbot

适合已有 Nginx 环境的场景。

### 1. 安装

```bash
# Debian/Ubuntu
sudo apt install nginx certbot python3-certbot-nginx

# CentOS/RHEL
sudo yum install nginx certbot python3-certbot-nginx
```

### 2. Nginx 配置

创建 `/etc/nginx/sites-available/nas.example.com`：

```nginx
server {
    listen 80;
    server_name nas.example.com;
    
    # Certbot 验证用
    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }
    
    # 强制跳转 HTTPS
    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl http2;
    server_name nas.example.com;
    
    # SSL 证书（certbot 会自动填充）
    ssl_certificate /etc/letsencrypt/live/nas.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/nas.example.com/privkey.pem;
    
    # SSL 安全配置
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384;
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 1d;
    
    # 大文件上传
    client_max_body_size 10G;
    client_body_timeout 3600s;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
    
    # 禁用缓冲（大文件流式传输）
    proxy_buffering off;
    proxy_request_buffering off;
    
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_pass_request_headers on;
        proxy_set_header Destination $http_destination;
        
        # WebDAV 方法支持
        proxy_method $request_method;
        
        # WebSocket 支持（未来可能需要）
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

MnemoNAS 的 `trusted_proxy_hops` 设置见本文开头的 [MnemoNAS 配置](#mnemonas-配置)。

### 3. 启用站点并申请证书

```bash
# 启用站点
sudo ln -s /etc/nginx/sites-available/nas.example.com /etc/nginx/sites-enabled/

# 测试配置
sudo nginx -t

# 重载 nginx
sudo systemctl reload nginx

# 申请证书（首次）
sudo certbot --nginx -d nas.example.com

# 测试自动续期
sudo certbot renew --dry-run
```

---

## 方案三：Traefik 模板

仓库提供了更适合 systemd MnemoNAS 的 Traefik file-provider 模板：

```bash
cp -r deploy/public-access/traefik ./mnemonas-traefik
cd ./mnemonas-traefik

# 编辑 traefik.yml 中的 ACME 邮箱
# 编辑 dynamic/mnemonas.yml 中的 nas.example.com
docker compose up -d
```

模板使用 `network_mode: host`，让容器内 Traefik 可以访问宿主机上的 `127.0.0.1:8080`。公网只应开放 `80/443`；不要给 `8080/9090/9091` 或改过的后端端口增加端口映射或安全组放行。

### Docker Compose 一体化示例

如果 MnemoNAS 和反向代理都用 Docker，可以用 Traefik。

示例中的 MnemoNAS 镜像默认使用源码构建的 `mnemonas:local`。发布镜像公开后，可在 `.env` 中设置 `MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas:<version>`。

### docker-compose.yml

```yaml
services:
  traefik:
    image: traefik:v3.0
    command:
      - "--providers.docker=true"
      - "--providers.docker.exposedbydefault=false"
      - "--entrypoints.web.address=:80"
      - "--entrypoints.websecure.address=:443"
      - "--certificatesresolvers.letsencrypt.acme.httpchallenge=true"
      - "--certificatesresolvers.letsencrypt.acme.httpchallenge.entrypoint=web"
      - "--certificatesresolvers.letsencrypt.acme.email=admin@example.com"
      - "--certificatesresolvers.letsencrypt.acme.storage=/letsencrypt/acme.json"
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./letsencrypt:/letsencrypt
    restart: unless-stopped

  mnemonas:
    image: ${MNEMONAS_IMAGE:-mnemonas:local}
    user: "${MNEMONAS_UID:-1000}:${MNEMONAS_GID:-1000}"
    volumes:
      - type: bind
        source: ${HOME}/.mnemonas
        target: /data
        bind:
          create_host_path: true
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.nas.rule=Host(`nas.example.com`)"
      - "traefik.http.routers.nas.entrypoints=websecure"
      - "traefik.http.routers.nas.tls.certresolver=letsencrypt"
      - "traefik.http.services.nas.loadbalancer.server.port=8080"
      # HTTP 跳转 HTTPS
      - "traefik.http.routers.nas-http.rule=Host(`nas.example.com`)"
      - "traefik.http.routers.nas-http.entrypoints=web"
      - "traefik.http.middlewares.https-redirect.redirectscheme.scheme=https"
      - "traefik.http.routers.nas-http.middlewares=https-redirect"
    restart: unless-stopped
```

示例没有启用 Traefik insecure dashboard。若需要 Traefik 管理界面，请单独配置认证、HTTPS 和内网访问限制；不要在公网环境使用 `--api.insecure=true`。

Traefik 需要读取 Docker socket 才能发现容器标签。`/var/run/docker.sock:/var/run/docker.sock:ro` 只限制挂载文件系统的写入，并不等同于低权限 API；更严格的公网部署建议使用 Docker socket proxy，或改用 Caddy/Nginx 这类静态反向代理配置。

---

## 方案四：Cloudflare Tunnel 模板

没有公网 IP，或不想直接开放入站 `80/443` 时，可以使用 Cloudflare Tunnel：

```bash
cp deploy/public-access/cloudflare-tunnel/config.yml ./cloudflared-config.yml
# 替换 tunnel、credentials-file 和 nas.example.com
cloudflared tunnel run --config ./cloudflared-config.yml
```

Cloudflare Tunnel 模板把外部 HTTPS 域名转发到本机 `http://127.0.0.1:8080`，最后一条 ingress 固定为 `http_status:404`，避免未匹配主机名落到 MnemoNAS。即使使用隧道，也应保持 dataplane `9090/9091` 或改过的 dataplane 端口仅本机或受信私网可达。

---

## 安全加固

### 1. 基础认证（可选双重保护）

Caddy：

```caddyfile
nas.example.com {
    basicauth /dav/* {
        admin $2a$14$... # caddy hash-password 生成
    }
    reverse_proxy localhost:8080
}
```

### 2. 限制访问 IP（可选）

Caddy：

```caddyfile
nas.example.com {
    @blocked not remote_ip 192.168.0.0/16 10.0.0.0/8
    respond @blocked "Forbidden" 403
    
    reverse_proxy localhost:8080
}
```

### 3. Fail2ban 防暴力破解

创建 `/etc/fail2ban/filter.d/mnemonas.conf`：

```ini
[Definition]
failregex = ^.*"POST /api/v1/auth/login.*" 401.*client=<HOST>.*$
ignoreregex =
```

创建 `/etc/fail2ban/jail.d/mnemonas.conf`：

```ini
[mnemonas]
enabled = true
port = http,https
filter = mnemonas
logpath = /var/log/caddy/nas.log
maxretry = 5
bantime = 3600
```

---

## 客户端连接

配置完成后，各客户端连接方式：

| 客户端 | 地址 |
| ------ | ---- |
| Web 浏览器 | `https://nas.example.com` |
| macOS Finder | `https://nas.example.com/dav` |
| Windows 资源管理器 | `https://nas.example.com/dav` |
| Linux (davfs2) | `https://nas.example.com/dav` |
| Rclone | `webdav:https://nas.example.com/dav` |

---

## 故障排查

### 证书问题

```bash
# 检查证书状态
sudo certbot certificates

# 续期演练
sudo certbot renew --dry-run

# 查看 certbot 定时任务和最近日志
systemctl list-timers 'certbot*' 'snap.certbot*'
journalctl -u certbot --since '24 hours ago'

# Caddy 证书位置与续期日志
ls ~/.local/share/caddy/certificates/
journalctl -u caddy --since '24 hours ago'

# 统一复核证书 hostname、30 天有效期、续期提示、匿名 WebDAV PROPFIND 和端口暴露
sudo mnemonas-doctor --public-domain nas.example.com
```

### 连接超时

```bash
# 检查防火墙
sudo ufw status
sudo firewall-cmd --list-all

# 检查端口监听
ss -tlnp | grep -E '80|443|8080'
```

### WebDAV 问题

```bash
# 测试 PROPFIND；auth_type=users 时使用 MnemoNAS 用户名和密码
# auth_type=basic 时使用设置页显示的 WebDAV 凭据，自动生成密码为 /srv/mnemonas/secrets.json 中的 webdav_password 字段
WEBDAV_USER="admin"
WEBDAV_PASS="<实际 WebDAV 密码>"
curl -u "$WEBDAV_USER:$WEBDAV_PASS" -X PROPFIND https://nas.example.com/dav/ \
  -H "Depth: 1" \
  -v

# 检查后端日志
docker logs mnemonas 2>&1 | tail -50
```
