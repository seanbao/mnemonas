# Security Hardening Guide

English | [简体中文](security.md)

This guide describes recommended MnemoNAS security settings for LAN and public deployments.

## Web UI Authentication

Web UI authentication is enabled by default. On first startup with no user data, MnemoNAS creates an administrator account and writes the initial password to:

```text
<storage.root>/.mnemonas/initial-password.txt
```

Notes:

- The initial administrator password is not stored permanently in `secrets.json`.
- The setup API does not return the initial username or password.
- The password is not printed in clear text by default. For controlled local debugging only, set `MNEMONAS_PRINT_INITIAL_PASSWORD=1` before first startup.
- After the first successful login for the administrator, `initial-password.txt` is removed.
- Change the administrator password after first login.

Systemd default path:

```bash
sudo cat /srv/mnemonas/.mnemonas/initial-password.txt
```

Docker default path:

```bash
cat ~/.mnemonas/.mnemonas/initial-password.txt
```

## WebDAV Basic Auth

Configure WebDAV in `config.toml`:

```toml
[webdav]
enabled = true
auth_type = "basic"
username = "admin"
password = ""
```

When `password` is empty and Basic Auth is enabled, MnemoNAS generates a random WebDAV password on first startup and stores it in:

```text
<storage.root>/secrets.json
```

This WebDAV password is separate from the Web UI administrator password. Startup logs show where to find the credentials but do not print the password.

Custom password:

```toml
[webdav]
password = "your-strong-password"
```

Use at least 16 characters with mixed letters, numbers, and symbols. A password manager is strongly recommended.

## Disable Auth Only for Local Development

```toml
[auth]
enabled = false

[webdav]
auth_type = "none"

[server]
host = "127.0.0.1"
```

`auth.enabled = false` disables Web UI/API login. `webdav.auth_type = "none"` disables WebDAV Basic Auth. If either is disabled, bind only to loopback. If a non-loopback bind is intentionally protected by an outer firewall, container port binding, or reverse proxy, set `security.allow_unsafe_no_auth = true` explicitly to pass configuration validation.

## Network Binding

### Local Only

```toml
[server]
host = "127.0.0.1"
port = 8080
```

Use this for development and single-machine testing.

### LAN Access

```toml
[server]
host = "0.0.0.0"
port = 8080

[webdav]
auth_type = "basic"
username = "webdav"
password = "your-password"
```

Firewall example:

```bash
sudo ufw allow from 192.168.0.0/24 to any port 8080
sudo ufw deny 9090/tcp comment "MnemoNAS dataplane gRPC"
sudo ufw deny 9091/tcp comment "MnemoNAS dataplane HTTP"
```

### Public Access

Do not expose MnemoNAS directly to the public internet. Put it behind HTTPS using Caddy, Nginx, Traefik, Cloudflare Tunnel, or another trusted reverse proxy.

## HTTPS

MnemoNAS has built-in TLS options, but for public or long-running deployments a reverse proxy is usually better for certificate renewal, upload limits, and hardening.

### Nginx + Let's Encrypt

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

    client_max_body_size 0;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_pass_request_headers on;
    }
}
```

Certificate:

```bash
sudo certbot --nginx -d nas.example.com
```

### Caddy

```caddyfile
nas.example.com {
    reverse_proxy localhost:8080
}
```

Caddy handles certificate issuance and renewal automatically.

### Cloudflare Tunnel

For deployments without a public IP that still need tunnel-based access:

```bash
curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 -o /tmp/cloudflared
sudo install -m 0755 /tmp/cloudflared /usr/local/bin/cloudflared

cloudflared tunnel login
cloudflared tunnel create mnemonas
```

`~/.cloudflared/config.yml`:

```yaml
tunnel: <tunnel-id>
credentials-file: ~/.cloudflared/<tunnel-id>.json

ingress:
  - hostname: nas.example.com
    service: http://localhost:8080
  - service: http_status:404
```

Run:

```bash
cloudflared tunnel run mnemonas
```

## Deployment Checklist

- [ ] First login completed using server-side `initial-password.txt`.
- [ ] Administrator password changed.
- [ ] WebDAV Basic Auth credentials recorded or changed to a strong password.
- [ ] `webdav.auth_type` is not `none` unless the server is loopback-only.
- [ ] Firewall is configured when `server.host = "0.0.0.0"`.
- [ ] Dataplane gRPC/HTTP ports are loopback-only or private.
- [ ] `mnemonas-doctor` reports no dataplane exposure warnings.
- [ ] Public deployments use HTTPS.

Runtime checks:

```bash
ss -tlnp | grep 8080
ss -tlnp | grep -E '9090|9091'

curl http://<server-ip>:8080/health

curl http://<server-ip>:8080/dav/
# expected: 401 Unauthorized when WebDAV Basic Auth is enabled
```

Regular maintenance:

- Run scrub periodically.
- Back up the full storage root.
- Check logs for unusual access.
- Upgrade to current releases.

## Known Security Limits

### Virtual WebDAV Locks

`LOCK` and `UNLOCK` are implemented for client compatibility, not as a full collaborative editing lock system. Coordinate multi-user editing of the same file.

### Multi-User Boundary

MnemoNAS supports users and roles. Non-admin users are limited by `home_dir` for files, search, favorites, shares, trash, and activity log views.

WebDAV Basic Auth is a global service credential and does not carry application-level user identity. Use Web UI/API accounts when per-user isolation is required.

### Rate Limiting

MnemoNAS includes a built-in concurrent request limit. Finer per-IP or per-user rate limits should be added at the reverse proxy.

Nginx example:

```nginx
limit_req_zone $binary_remote_addr zone=api:10m rate=10r/s;
location /api/ {
    limit_req zone=api burst=20;
    proxy_pass http://localhost:8080;
}
```

### Preview and Download Auth

File downloads, version previews, media previews, and external-open flows use a short-lived `HttpOnly` download-session cookie. Long-lived access tokens are not passed through URL query parameters.

The `Secure` cookie flag is enabled when the request is actually HTTPS, or when `trusted_proxy_hops > 0` and a trusted private/loopback proxy forwards `X-Forwarded-Proto=https`.

### Web UI Session Tokens

The Web UI stores the primary access and refresh session in `HttpOnly`, `SameSite=Lax` cookies. It no longer writes bearer access or refresh tokens to `localStorage`; REST API calls, uploads, refresh, and logout use same-origin cookies sent by the browser. Legacy tokens left by older versions are cleared during initialization, refresh, logout, and related auth paths.

For `POST`, `PUT`, `PATCH`, and `DELETE` requests that carry browser `Origin` or `Referer` metadata, the server rejects requests whose source host does not match the current request host. Script clients without browser origin metadata and explicit `Authorization` API clients continue to work.

API clients can still use `Authorization: Bearer <access-token>` and JSON refresh tokens for scripts and automation. The server adds security headers, CSP, and `Permissions-Policy`, but public deployments still need careful origin hygiene.

For public deployments:

- Serve only trusted static assets.
- Use HTTPS.
- Avoid injecting third-party scripts under the same origin.
- Sign out on shared computers.

Signing out, changing a user's password, deleting the user, or disabling the user revokes or clears the relevant sessions.

### Public Share Passwords

Password-protected public shares issue an `HttpOnly` cookie after successful password validation. Folder browsing and downloads use that cookie instead of passing passwords in URLs.

After clearing site data, switching browser, or changing the share password, the password must be entered again.

Five failed password attempts for the same share and client address lock access for five minutes and return `429 Too Many Requests`.

## Security Capability Status

| Status | Capability |
| --- | --- |
| Supported | Web UI login, users and roles, user root-directory isolation, WebDAV Basic Auth, path traversal protection, WebDAV read-only mode, share password validation and lockout |
| Add through reverse proxy | HTTPS certificate renewal, finer rate limits, public access controls |
| Planned | OAuth/OIDC integration, finer application-level access policies |

## More Resources

- [Reverse proxy setup](reverse-proxy-setup.en.md)
- [Docker deployment](docker-deployment.en.md)
- [FAQ](faq.en.md)
- [Configuration reference](configuration.en.md)
