# Public HTTPS and Reverse Proxy Setup

English | [简体中文](reverse-proxy-setup.md)

This guide explains how to expose MnemoNAS through HTTPS with a reverse proxy. Public access should go through HTTPS on `80/443`, not by exposing the raw `8080` service directly. If you are starting from a public server, follow the [Public server quickstart](public-server-quickstart.en.md) first; this guide keeps the detailed Caddy, Nginx, and Traefik examples.

## Prerequisites

- A public server or a working tunnel.
- A domain name pointing to the server.
- Firewall rules allowing `80` and `443`.

## Recommended Script

After systemd deployment, use the helper to create the public HTTPS entry and restrict the MnemoNAS backend listener:

```bash
sudo mnemonas-public-setup --proxy caddy nas.example.com admin@example.com
```

The systemd installer installs `scripts/setup-reverse-proxy.sh` as `mnemonas-public-setup`. The script sets `server.host = "127.0.0.1"`, `trusted_proxy_hops = 1`, configures Caddy or Nginx, adjusts local UFW rules, and runs basic checks. Cloud-provider security groups still need manual confirmation: expose only `80/443`, not `8080/9090/9091`.

## MnemoNAS Configuration

MnemoNAS does not trust `X-Forwarded-*` headers by default. When it is behind trusted proxies, set the number of proxy hops:

```toml
[server]
trusted_proxy_hops = 1
```

Use `1` for a single Caddy, Nginx, or Traefik proxy. For multiple hops, set the total number of trusted proxy layers. Restart `mnemonas` after changing the value.

Without this setting, MnemoNAS still works behind a proxy, but Secure-cookie detection and client-IP-based rate limiting use the direct peer address instead of the real client address.

Only expose `80/443` publicly. `8080` is the direct Web/API/WebDAV port. If the reverse proxy and MnemoNAS run on the same host, prefer `[server].host = "127.0.0.1"` or firewall `8080` to trusted sources. Dataplane `9090/9091` must not be exposed.

## Option 1: Caddy

Caddy is the simplest option because it handles Let's Encrypt certificates automatically.

Install:

```bash
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https curl gnupg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update
sudo apt install caddy
```

`/etc/caddy/Caddyfile`:

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

Start:

```bash
sudo systemctl enable caddy
sudo systemctl restart caddy
sudo systemctl status caddy
```

Verify:

```bash
curl -I https://nas.example.com/health

WEBDAV_USER="webdav"
WEBDAV_PASS="your-webdav-password"
curl -u "$WEBDAV_USER:$WEBDAV_PASS" -X PROPFIND https://nas.example.com/dav/ -H "Depth: 0"
```

## Option 2: Nginx + Certbot

Install:

```bash
sudo apt install nginx certbot python3-certbot-nginx
```

Create `/etc/nginx/sites-available/nas.example.com`:

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
        proxy_method $request_method;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

Enable and request certificate:

```bash
sudo ln -s /etc/nginx/sites-available/nas.example.com /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
sudo certbot --nginx -d nas.example.com
sudo certbot renew --dry-run
```

Set `trusted_proxy_hops = 1` in MnemoNAS for a single Nginx proxy.

## Option 3: Docker Compose with Traefik

When MnemoNAS and the reverse proxy both run under Docker, the example defaults to the source-built `mnemonas:local` image. After public release images are available, set `MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas:<version>` in `.env`.

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
    image: ${MNEMONAS_IMAGE:-mnemonas:local}
    user: "${MNEMONAS_UID:-1000}:${MNEMONAS_GID:-1000}"
    volumes:
      - ${HOME}/.mnemonas:/data
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

The example does not enable the insecure Traefik dashboard. If a dashboard is needed, protect it separately with authentication, HTTPS, and network restrictions.

Mounting `/var/run/docker.sock` read-only still gives Traefik broad Docker API visibility. More hardened deployments can use a Docker socket proxy or a static Caddy/Nginx config.

## Hardening

### Optional Extra Basic Auth

Caddy:

```caddyfile
nas.example.com {
    basicauth /dav/* {
        admin $2a$14$...
    }
    reverse_proxy localhost:8080
}
```

### Optional IP Restrictions

Caddy:

```caddyfile
nas.example.com {
    @blocked not remote_ip 192.168.0.0/16 10.0.0.0/8
    respond @blocked "Forbidden" 403

    reverse_proxy localhost:8080
}
```

### Fail2ban Example

`/etc/fail2ban/filter.d/mnemonas.conf`:

```ini
[Definition]
failregex = ^.*"POST /api/v1/auth/login.*" 401.*client=<HOST>.*$
ignoreregex =
```

`/etc/fail2ban/jail.d/mnemonas.conf`:

```ini
[mnemonas]
enabled = true
port = http,https
filter = mnemonas
logpath = /var/log/caddy/nas.log
maxretry = 5
bantime = 3600
```

## Client URLs

| Client | URL |
| --- | --- |
| Browser | `https://nas.example.com` |
| macOS Finder | `https://nas.example.com/dav` |
| Windows File Explorer | `https://nas.example.com/dav` |
| Linux davfs2 | `https://nas.example.com/dav` |
| rclone | `webdav:https://nas.example.com/dav` |

## Troubleshooting

Certificate checks:

```bash
sudo certbot certificates
sudo certbot renew
```

Connectivity:

```bash
sudo ufw status
ss -tlnp | grep -E '80|443|8080'
```

WebDAV:

```bash
WEBDAV_USER="webdav"
WEBDAV_PASS="your-webdav-password"
curl -u "$WEBDAV_USER:$WEBDAV_PASS" -X PROPFIND https://nas.example.com/dav/ \
  -H "Depth: 1" \
  -v
```

Backend logs:

```bash
docker logs mnemonas 2>&1 | tail -50
```
