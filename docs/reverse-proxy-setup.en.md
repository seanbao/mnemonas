# Public HTTPS and Reverse Proxy Setup

English | [简体中文](reverse-proxy-setup.md)

This guide explains how to expose MnemoNAS through HTTPS with a reverse proxy.
Public access should go through HTTPS on `80/443`, not by exposing the raw `8080` service directly.
For a new public-server deployment, follow the [Public server quickstart](public-server-quickstart.en.md) first.
This guide keeps the detailed Caddy, Nginx, and Traefik examples.

## Prerequisites

- A public server or a working tunnel.
- A domain name pointing to the server.
- Firewall rules allowing `80` and `443`.

## Recommended Script

After systemd deployment, use the helper to create the public HTTPS entry and restrict the MnemoNAS backend listener:

```bash
sudo mnemonas-public-setup --proxy caddy nas.example.com admin@example.com
```

The systemd installer installs `scripts/setup-reverse-proxy.sh` as `mnemonas-public-setup`.
The script:

- Sets `server.host = "127.0.0.1"` and `trusted_proxy_hops = 1`.
- Configures Caddy or Nginx.
- Adjusts local UFW rules.
- Runs basic checks.

The basic checks read local listening addresses with `ss` first.
When `ss` is unavailable, they fall back to `/proc/net/tcp` and `/proc/net/tcp6`.
For non-public basic checks, the report says port exposure cannot be confirmed when neither source is readable.
If the basic checks confirm that the Web/API/WebDAV backend or dataplane ports listen on non-loopback addresses, `mnemonas-public-setup` fails and asks for remediation before a rerun.
Public strict checks must cover both IPv4 and IPv6 listeners; without `ss`, both `/proc/net/tcp` and `/proc/net/tcp6` must be readable, otherwise `mnemonas-doctor --public-domain` fails to avoid missing IPv6 `[::]` exposure.
Public strict checks also require `curl`, `python3`, and `openssl` for public HTTP(S) entry checks, duration parsing, `users.json` administrator-redundancy checks, generated WebDAV credential checks, and HTTPS certificate checks; without any of these tools, `mnemonas-doctor --public-domain` fails.

When adjusting UFW, the script removes broad allow rules for `8080/9090/9091` or custom backend ports before writing deny rules.
A follow-up `mnemonas-doctor --public-domain` treats remaining broad local UFW allow rules for backend control-plane or dataplane ports as public-deployment failures.

Cloud-provider security groups still need manual confirmation.
Expose only `80/443`, not `8080/9090/9091` or any custom backend ports.

The script lowercases the domain and removes a single FQDN trailing dot.
The normalized value is used in the Caddy/Nginx configuration, certificate request, WebDAV URL, and verification commands.

The helper requires `--config` to be an absolute path without whitespace, control characters, parent-directory segments, or symlink components.
This matches the `nasd` config-file safety checks.
Configuration updates, the follow-up `mnemonas-doctor --public-domain` diagnostics, and WebDAV URL resolution use the same configuration path.

The completion summary reads `webdav.prefix` from the configuration and prints the matching public WebDAV URL.
If no prefix is configured, it uses the default `/dav`.

An explicitly empty prefix, root prefix, or prefix under the reserved `/api`, `/s`, or `/health` route namespaces is invalid because it overlaps the Web UI/API routes.
The prefix is normalized with the same URL-path rules as the server.
Normalization trims surrounding whitespace, folds repeated slashes, and resolves `.` and `..` path segments.
When a deployment uses a custom WebDAV prefix, replace `/dav` in the verification commands with that prefix.

When `--skip-mnemonas-config` or `--no-firewall` is used, the completion summary marks the corresponding item as skipped and lists the manual settings to verify.

When overriding the default backend host with `MNEMONAS_UPSTREAM_HOST`, use only a hostname, IPv4 address, or IPv6 literal.
Do not include a scheme, path, or port.
IPv6 may be written as `::1` or `[::1]`.

For Traefik or Cloudflare Tunnel, start from the repository templates instead of assembling commands ad hoc:

- `deploy/public-access/traefik/`: Traefik on a Linux host, while MnemoNAS still runs under systemd on `127.0.0.1:8080`.
- `deploy/public-access/cloudflare-tunnel/config.yml`: Cloudflare Tunnel ingress for deployments without a directly reachable public IP.

See [Public access templates](../deploy/public-access/README.en.md) for the template notes.
After copying a template, replace at least `nas.example.com`, the ACME email or tunnel ID.
Then run `sudo mnemonas-doctor --public-domain <domain>`.

## MnemoNAS Configuration

MnemoNAS does not trust `X-Forwarded-*` headers by default. When it is behind trusted proxies, set the number of proxy hops:

```toml
[server]
trusted_proxy_hops = 1
```

Use `1` for a single Caddy, Nginx, or Traefik proxy.
For multiple hops, set the total number of trusted proxy layers.
Loopback direct peers need no extra setting.
If the proxy reaches MnemoNAS through a Docker bridge, internal load balancer, or another non-loopback address, also set `trusted_proxy_cidrs = ["<proxy-ip-or-cidr>"]`.
Restart `mnemonas` after changing the value.

Without this setting, MnemoNAS still works behind a proxy.
Secure-cookie detection and client-IP-based rate limiting use the direct peer address instead of the real client address.

Only expose `80/443` publicly.
`8080` is the default direct Web/API/WebDAV port.
When the reverse proxy and MnemoNAS run on the same host, prefer `[server].host = "127.0.0.1"` or firewall the direct backend port to trusted sources.
Dataplane `9090/9091`, or custom dataplane ports, must not be exposed.

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

# Use a MnemoNAS username and password when auth_type=users.
# Use the WebDAV username and password when auth_type=basic. Settings shows the Basic username and readable generated password.
# Custom Basic passwords are not echoed back; generated passwords use the webdav_password field in /srv/mnemonas/secrets.json.
WEBDAV_USER="<mnemonas-or-webdav-username>"
WEBDAV_PASS="<mnemonas-or-webdav-password>"
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
        proxy_pass_request_headers on;
        proxy_set_header Destination $http_destination;
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

## Option 3: Traefik Template

The repository includes a Traefik file-provider template that fits a systemd MnemoNAS backend:

```bash
cp -r deploy/public-access/traefik ./mnemonas-traefik
cd ./mnemonas-traefik

# Edit the ACME email in traefik.yml.
# Edit nas.example.com in dynamic/mnemonas.yml.
docker compose up -d
```

The template uses `network_mode: host` so Traefik can reach the host's `127.0.0.1:8080` MnemoNAS listener.
Expose only `80/443` publicly.
Do not add port mappings or security-group rules for `8080/9090/9091` or custom backend ports.

### Docker Compose All-in-One Example

When MnemoNAS and the reverse proxy both run under Docker, the example defaults to the source-built `mnemonas:local` image.
After public release images are available, set `MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas:<version>` in `.env`.

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

The example does not enable the insecure Traefik dashboard.
If a dashboard is needed, protect it separately with authentication, HTTPS, and network restrictions.
Do not use `--api.insecure=true` in public deployments.

Mounting `/var/run/docker.sock` read-only still gives Traefik broad Docker API visibility.
More hardened deployments can use a Docker socket proxy or a static Caddy/Nginx config.

## Option 4: Cloudflare Tunnel Template

Use Cloudflare Tunnel when the server has no public IP or inbound `80/443` should not be exposed directly:

```bash
cp deploy/public-access/cloudflare-tunnel/config.yml ./cloudflared-config.yml
# Replace tunnel, credentials-file, and nas.example.com.
cloudflared tunnel run --config ./cloudflared-config.yml
```

The tunnel template forwards the public HTTPS hostname to local `http://127.0.0.1:8080` and ends with `http_status:404` for unmatched hosts.
Even with a tunnel, do not expose `8080` or custom backend ports to the public network.
Keep dataplane `9090/9091`, or custom dataplane ports, loopback-only or private-network-only.

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

Certificate, anonymous WebDAV PROPFIND, and exposure checks:

```bash
sudo certbot certificates
sudo certbot renew --dry-run
systemctl list-timers 'certbot*' 'snap.certbot*'
journalctl -u certbot --since '24 hours ago'
journalctl -u caddy --since '24 hours ago'
sudo mnemonas-doctor --public-domain nas.example.com
```

Connectivity:

```bash
sudo ufw status
ss -tlnp | grep -E '80|443|8080|9090|9091'
```

`8080/9090/9091`, or custom backend and dataplane ports, should not listen on public addresses.

WebDAV:

```bash
# Use a MnemoNAS username and password when auth_type=users.
# Use the WebDAV username and password when auth_type=basic. Settings shows the Basic username and readable generated password.
# Custom Basic passwords are not echoed back; generated passwords use the webdav_password field in /srv/mnemonas/secrets.json.
WEBDAV_USER="<mnemonas-or-webdav-username>"
WEBDAV_PASS="<mnemonas-or-webdav-password>"
curl -u "$WEBDAV_USER:$WEBDAV_PASS" -X PROPFIND https://nas.example.com/dav/ \
  -H "Depth: 1" \
  -v
```

Backend logs:

```bash
docker logs mnemonas 2>&1 | tail -50
```
