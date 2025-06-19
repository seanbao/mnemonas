# 外网访问配置指南

本文档介绍如何通过反向代理配置 MnemoNAS 的 HTTPS 外网访问。

## 前置条件

- 一台公网服务器（或已配置好内网穿透）
- 域名已解析到服务器 IP
- 服务器开放 80/443 端口

## 方案一：Caddy（推荐）

Caddy 自动申请和续期 Let's Encrypt 证书，配置最简单。

### 1. 安装 Caddy

```bash
# Debian/Ubuntu
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https curl
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

# 测试 WebDAV
WEBDAV_USER="family"
WEBDAV_PASS="your-webdav-password"
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
        
        # WebDAV 方法支持
        proxy_method $request_method;
        
        # WebSocket 支持（未来可能需要）
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

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

## 方案三：Docker Compose 一体化

如果 MnemoNAS 和反向代理都用 Docker，可以用 Traefik。

### docker-compose.yml

```yaml
services:
  traefik:
    image: traefik:v3.0
    command:
      - "--api.insecure=true"
      - "--providers.docker=true"
      - "--entrypoints.web.address=:80"
      - "--entrypoints.websecure.address=:443"
      - "--certificatesresolvers.letsencrypt.acme.httpchallenge=true"
      - "--certificatesresolvers.letsencrypt.acme.httpchallenge.entrypoint=web"
      - "--certificatesresolvers.letsencrypt.acme.email=your-email@example.com"
      - "--certificatesresolvers.letsencrypt.acme.storage=/letsencrypt/acme.json"
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./letsencrypt:/letsencrypt
    restart: unless-stopped

  mnemonas:
    image: mnemonas:latest
    volumes:
      - ~/.mnemonas:/root/.mnemonas
      - ~/.mnemonas/config.toml:/root/.mnemonas/config.toml:ro
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
|--------|------|
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

# 手动续期
sudo certbot renew

# Caddy 证书位置
ls ~/.local/share/caddy/certificates/
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
# 测试 PROPFIND
WEBDAV_USER="family"
WEBDAV_PASS="your-webdav-password"
curl -u "$WEBDAV_USER:$WEBDAV_PASS" -X PROPFIND https://nas.example.com/dav/ \
  -H "Depth: 1" \
  -v

# 检查后端日志
docker logs mnemonas 2>&1 | tail -50
```
