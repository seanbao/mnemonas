# 外网访问配置指南

[English](reverse-proxy-setup.en.md) | 简体中文

本文档介绍如何通过反向代理配置 MnemoNAS 的 HTTPS 外网访问。
公网访问应通过 `80/443` 上的 HTTPS 入口，不应直接暴露原始 `8080` 服务。
如果从一台公网服务器开始部署，优先按 [公网服务器快速上线](public-server-quickstart.md) 走推荐路径。
本文保留 Caddy、Nginx、Traefik 的细节配置。

## 前置条件

- 一台公网服务器（或已配置好内网穿透）
- 域名已解析到服务器 IP
- 服务器开放 80/443 端口

## 推荐脚本

systemd 部署完成后，推荐用脚本自动生成公网 HTTPS 入口并收紧 MnemoNAS 后端监听：

```bash
sudo mnemonas-public-setup --proxy caddy nas.example.com admin@example.com
```

systemd 安装会把源码中的 `scripts/setup-reverse-proxy.sh` 安装为 `mnemonas-public-setup`。
脚本会：

- 设置 `server.host = "127.0.0.1"` 和 `trusted_proxy_hops = 1`。
- 配置 Caddy 或 Nginx。
- 调整本机 UFW 规则。
- 运行基础检查。

基础检查会优先使用 `ss` 读取本机监听地址。
`ss` 不可用时，会退回读取 `/proc/net/tcp` 和 `/proc/net/tcp6`。
普通基础检查在两个来源都不可读时，会提示无法确认端口暴露状态。
如果基础检查确认 Web/API/WebDAV 后端或 dataplane 端口监听在非 loopback 地址，`mnemonas-public-setup` 会失败并要求修复后重新运行。
公网严格检查要求同时覆盖 IPv4 和 IPv6 监听；没有 `ss` 时，`/proc/net/tcp` 和 `/proc/net/tcp6` 必须都可读，否则 `mnemonas-doctor --public-domain` 会失败，避免漏检 IPv6 `[::]` 暴露。
公网严格检查还需要 `curl`、`python3`、`getent` 和 `openssl`，用于检查公网 HTTP(S) 入口、解析 duration 配置、检查 DNS 解析、`users.json` 管理员冗余、自动生成的 WebDAV 凭据和 HTTPS 证书；缺少任一工具时，`mnemonas-doctor --public-domain` 会失败。

调整 UFW 时，脚本会删除 `8080/9090/9091` 或自定义后端端口上的宽泛放行规则，再写入拒绝规则。
后续 `mnemonas-doctor --public-domain` 会把本机 UFW 中仍宽泛放行后端 control plane 或 dataplane 端口的规则视为公网部署失败。

云厂商安全组仍需人工确认。
只开放 `80/443`，不要开放 `8080/9090/9091` 或改过的后端端口。

脚本会将域名统一为小写，并移除单个 FQDN 尾点。
规范化后的值会用于 Caddy/Nginx 配置、证书申请、WebDAV 地址和验证命令。

脚本要求 `--config` 使用绝对路径，且路径不能包含空白字符、控制字符、父目录段或符号链接组件。
这与 `nasd` 配置文件安全检查保持一致。
配置更新、后续 `mnemonas-doctor --public-domain` 诊断以及 WebDAV 地址解析都会使用同一个配置路径。

脚本完成摘要会读取配置中的 `webdav.prefix` 并输出对应的公网 WebDAV 地址。
未配置时使用默认 `/dav`。

显式配置为空字符串、根路径，或覆盖 `/api`、`/s`、`/health` 保留路由的前缀会被视为无效，因为它会与 Web UI/API 路由重叠。
该前缀会按服务端 URL 路径规则规范化。
规范化会去除首尾空白、折叠重复斜杠，并处理 `.` 和 `..` 段。
如果已把 WebDAV 前缀改为其他路径，验证命令中的 `/dav` 也应替换为实际前缀。

使用 `--skip-mnemonas-config` 或 `--no-firewall` 时，完成摘要会把对应项目标记为已跳过，并列出需要手动确认的配置。

如需通过 `MNEMONAS_UPSTREAM_HOST` 覆盖默认后端主机，该值只能是主机名、IPv4 或 IPv6 字面量。
不要包含协议、路径或端口。
IPv6 可写作 `::1` 或 `[::1]`。

如果需要 Traefik 或 Cloudflare Tunnel，应优先从仓库模板开始，不要手工拼接临时命令：

- `deploy/public-access/traefik/`：Linux 主机上运行 Traefik，MnemoNAS 仍由 systemd 监听 `127.0.0.1:8080`。
- `deploy/public-access/cloudflare-tunnel/config.yml`：无公网 IP 或希望通过 Cloudflare Tunnel 暴露 HTTPS 域名时使用。

模板说明见 [公网访问模板](../deploy/public-access/README.md)。
复制模板后至少替换 `nas.example.com`、ACME 邮箱或 tunnel ID。
然后运行 `sudo mnemonas-doctor --public-domain <domain>`。

## MnemoNAS 配置

MnemoNAS 默认不信任 `X-Forwarded-*` 头。部署在受信反向代理后方时，需要在 `config.toml` 中设置代理层数：

```toml
[server]
trusted_proxy_hops = 1
```

单层 Caddy/Nginx/Traefik 设置为 `1`。
多层代理设置为代理总层数。
直连来源为本机 loopback 时无需额外配置。
如果代理来自 Docker 网桥、内网负载均衡或其他非 loopback 地址，还需要设置 `trusted_proxy_cidrs = ["<proxy-ip-or-cidr>"]`。
修改后重启 `mnemonas`。

未设置时，服务仍可通过反向代理访问，但登录、分享下载等 cookie 的 `Secure` 判断和按客户端 IP 的限流会按直连来源处理。

公网入口只应开放 `80/443`。
`8080` 是 MnemoNAS Web/API/WebDAV 的默认直连端口。
如果反向代理和 MnemoNAS 在同一台机器，建议把 `[server].host` 改为 `127.0.0.1`，或用防火墙限制直连端口只允许可信来源访问。
`9090/9091` 是 dataplane 默认内部端口。
如果 dataplane 端口已修改，也不要通过防火墙、端口映射或反向代理暴露到公网或不可信局域网。

## 方案一：Caddy

Caddy 自动申请和续期 Let's Encrypt 证书，配置量较少。

安装：

```bash
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https curl gnupg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update
sudo apt install caddy
```

`/etc/caddy/Caddyfile`：

```caddyfile
nas.example.com {
    reverse_proxy localhost:8080 {
        header_up Host {host}
        header_up X-Real-IP {remote_host}
        header_up X-Forwarded-For {remote_host}
        header_up X-Forwarded-Proto {scheme}
    }

    request_body {
        max_size 10GB
    }

    log {
        output file /var/log/caddy/nas.log
        format json
    }
}
```

启动：

```bash
sudo systemctl enable caddy
sudo systemctl restart caddy
sudo systemctl status caddy
```

验证：

```bash
curl -I https://nas.example.com/health

# auth_type=users 时使用 MnemoNAS 用户名和密码。
# auth_type=basic 时使用 WebDAV 用户名和密码；Settings 会显示 Basic 用户名和可读取的生成密码。
# 自定义 Basic 密码不会回显；生成密码位于 /srv/mnemonas/secrets.json 的 webdav_password 字段。
WEBDAV_USER="<mnemonas-or-webdav-username>"
WEBDAV_PASS="<mnemonas-or-webdav-password>"
curl_escape_config_value() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '%s' "$value"
}
curl_auth_config="$(mktemp -t mnemonas-webdav-curl-auth.XXXXXX)"
trap 'rm -f "$curl_auth_config"' EXIT
chmod 600 "$curl_auth_config"
printf 'user = "%s:%s"\n' \
  "$(curl_escape_config_value "$WEBDAV_USER")" \
  "$(curl_escape_config_value "$WEBDAV_PASS")" > "$curl_auth_config"
curl --config "$curl_auth_config" -X PROPFIND https://nas.example.com/dav/ -H "Depth: 0"
```

## 方案二：Nginx + Certbot

安装：

```bash
sudo apt install nginx certbot python3-certbot-nginx
```

创建 `/etc/nginx/sites-available/nas.example.com`：

```nginx
server {
    listen 80;
    server_name nas.example.com;

    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }

    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl http2;
    server_name nas.example.com;

    ssl_certificate /etc/letsencrypt/live/nas.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/nas.example.com/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;

    client_max_body_size 10G;
    client_body_timeout 3600s;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
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
        proxy_method $request_method;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

启用站点并申请证书：

```bash
sudo ln -s /etc/nginx/sites-available/nas.example.com /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
sudo certbot --nginx -d nas.example.com
sudo certbot renew --dry-run
```

单层 Nginx 代理需要在 MnemoNAS 中设置 `trusted_proxy_hops = 1`。

## 方案三：Traefik 模板

仓库提供了更适合 systemd MnemoNAS 的 Traefik file-provider 模板：

```bash
cp -r deploy/public-access/traefik ./mnemonas-traefik
cd ./mnemonas-traefik

# 在 traefik.yml 中修改 ACME 邮箱。
# 在 dynamic/mnemonas.yml 中修改 nas.example.com。
docker compose up -d
```

模板使用 `network_mode: host`，让容器内 Traefik 可以访问宿主机上的 `127.0.0.1:8080`。
公网只应开放 `80/443`。
不要给 `8080/9090/9091` 或改过的后端端口增加端口映射或安全组放行。

### Docker Compose 一体化示例

示例中的 MnemoNAS 镜像默认使用源码构建的 `mnemonas:local`。发布镜像公开后，可在 `.env` 中设置 `MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas:<version>`。

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
      - "traefik.http.routers.nas-http.rule=Host(`nas.example.com`)"
      - "traefik.http.routers.nas-http.entrypoints=web"
      - "traefik.http.middlewares.https-redirect.redirectscheme.scheme=https"
      - "traefik.http.routers.nas-http.middlewares=https-redirect"
    restart: unless-stopped
```

示例没有启用 Traefik insecure dashboard。若需要 Traefik 管理界面，应单独配置认证、HTTPS 和内网访问限制；不要在公网环境使用 `--api.insecure=true`。

Traefik 需要读取 Docker socket 才能发现容器标签。`/var/run/docker.sock:/var/run/docker.sock:ro` 只限制挂载文件系统的写入，并不等同于低权限 API；更严格的公网部署建议使用 Docker socket proxy，或改用 Caddy/Nginx 这类静态反向代理配置。

## 方案四：Cloudflare Tunnel 模板

没有公网 IP，或不想直接开放入站 `80/443` 时，可以使用 Cloudflare Tunnel：

```bash
cp deploy/public-access/cloudflare-tunnel/config.yml ./cloudflared-config.yml
# 替换 tunnel、credentials-file 和 nas.example.com。
cloudflared tunnel run --config ./cloudflared-config.yml
```

Cloudflare Tunnel 模板把外部 HTTPS 域名转发到本机 `http://127.0.0.1:8080`。
最后一条 ingress 固定为 `http_status:404`，避免未匹配主机名落到 MnemoNAS。
即使使用隧道，也不要把 `8080` 或改过的后端端口暴露到公网。
dataplane `9090/9091` 或改过的 dataplane 端口也应仅本机或受信私网可达。

## 安全加固

### 可选额外 Basic Auth

Caddy：

```caddyfile
nas.example.com {
    basicauth /dav/* {
        admin $2a$14$...
    }
    reverse_proxy localhost:8080
}
```

### 可选 IP 限制

Caddy：

```caddyfile
nas.example.com {
    @blocked not remote_ip 192.168.0.0/16 10.0.0.0/8
    respond @blocked "Forbidden" 403
    
    reverse_proxy localhost:8080
}
```

### Fail2ban 示例

`/etc/fail2ban/filter.d/mnemonas.conf`：

```ini
[Definition]
failregex = ^.*"POST /api/v1/auth/login.*" 401.*client=<HOST>.*$
ignoreregex =
```

`/etc/fail2ban/jail.d/mnemonas.conf`：

```ini
[mnemonas]
enabled = true
port = http,https
filter = mnemonas
logpath = /var/log/caddy/nas.log
maxretry = 5
bantime = 3600
```

## 客户端连接

| 客户端 | 地址 |
| --- | --- |
| Web 浏览器 | `https://nas.example.com` |
| macOS Finder | `https://nas.example.com/dav` |
| Windows 资源管理器 | `https://nas.example.com/dav` |
| Linux (davfs2) | `https://nas.example.com/dav` |
| Rclone | `webdav:https://nas.example.com/dav` |

## 故障排查

证书、匿名 WebDAV PROPFIND 和暴露检查：

```bash
sudo certbot certificates
sudo certbot renew --dry-run
systemctl list-timers 'certbot*' 'snap.certbot*'
journalctl -u certbot --since '24 hours ago'
journalctl -u caddy --since '24 hours ago'
sudo mnemonas-doctor --public-domain nas.example.com
```

连接性：

```bash
sudo ufw status
ss -tlnp | grep -E '80|443|8080|9090|9091'
```

`8080/9090/9091` 或改过的后端和 dataplane 端口不应监听在公网地址上。

WebDAV：

```bash
# auth_type=users 时使用 MnemoNAS 用户名和密码。
# auth_type=basic 时使用 WebDAV 用户名和密码；Settings 会显示 Basic 用户名和可读取的生成密码。
# 自定义 Basic 密码不会回显；生成密码位于 /srv/mnemonas/secrets.json 的 webdav_password 字段。
WEBDAV_USER="<mnemonas-or-webdav-username>"
WEBDAV_PASS="<mnemonas-or-webdav-password>"
curl_escape_config_value() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '%s' "$value"
}
curl_auth_config="$(mktemp -t mnemonas-webdav-curl-auth.XXXXXX)"
trap 'rm -f "$curl_auth_config"' EXIT
chmod 600 "$curl_auth_config"
printf 'user = "%s:%s"\n' \
  "$(curl_escape_config_value "$WEBDAV_USER")" \
  "$(curl_escape_config_value "$WEBDAV_PASS")" > "$curl_auth_config"
curl --config "$curl_auth_config" -X PROPFIND https://nas.example.com/dav/ \
  -H "Depth: 1" \
  -v
```

后端日志：

```bash
docker logs mnemonas 2>&1 | tail -50
```
