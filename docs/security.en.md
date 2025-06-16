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

## WebDAV Authentication

Configure WebDAV in `config.toml`:

```toml
[webdav]
enabled = true
auth_type = "users"
```

`auth_type = "users"` is recommended for day-to-day mounting. WebDAV clients log in with MnemoNAS usernames and passwords; admins see the global namespace; regular users see their `home_dir` as the mount root; guest users are read-only; user quotas limit WebDAV PUT/COPY writes.

For legacy setups or a separate service credential, use global Basic Auth:

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

`auth.enabled = false` disables Web UI/API login. `webdav.auth_type = "none"` disables WebDAV authentication. If either is disabled, bind only to loopback. If a non-loopback bind is intentionally protected by an outer firewall, container port binding, or reverse proxy, set `security.allow_unsafe_no_auth = true` explicitly to pass configuration validation.

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
auth_type = "users"
```

Firewall example:

```bash
sudo ufw allow from 192.168.0.0/24 to any port 8080
sudo ufw deny 9090/tcp comment "MnemoNAS dataplane gRPC"
sudo ufw deny 9091/tcp comment "MnemoNAS dataplane HTTP"
```

### Public Access

Do not expose MnemoNAS directly to the public internet. Put it behind HTTPS using Caddy, Nginx, Traefik, Cloudflare Tunnel, or another trusted reverse proxy.

Recommended path:

1. Run `sudo mnemonas-public-setup --proxy caddy <domain> <email>` or `sudo mnemonas-public-setup --proxy nginx <domain> <email>` on the server to generate and install reverse-proxy configuration.
2. After logging into the Web UI, open `System Settings -> General -> Public Access Wizard` and apply the recommended `server.host`, `trusted_proxy_hops`, share base URL, and related settings for the chosen deployment mode.
3. Run the Web UI security self-check to verify authentication, HTTPS request semantics, trusted-proxy handling, listener scope, dataplane ports, WebDAV authentication, share base URL, initial password file state, and administrator-account redundancy.
4. Run `sudo mnemonas-doctor --public-domain <domain>` on the server to re-check the public domain, reverse proxy, and local port exposure.

The Web UI wizard updates MnemoNAS application settings and shows operational guidance. Certificate issuance, firewall rules, cloud security groups, and reverse-proxy installation still belong on the server or in the cloud console. The security self-check can only verify runtime state and request semantics that the MnemoNAS process can observe; it does not replace cloud security-group, real public-port, or certificate-chain checks.

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
- [ ] WebDAV uses `auth_type = "users"`, or global Basic Auth credentials are recorded and changed to a strong password.
- [ ] `webdav.auth_type` is not `none` unless the server is loopback-only.
- [ ] Public deployments use `server.host = "127.0.0.1"` and are reachable only through the HTTPS reverse proxy.
- [ ] Dataplane gRPC/HTTP ports are loopback-only or private.
- [ ] The Web UI security self-check has no `block` items; public deployments should resolve all `warning` items before exposure, especially `allow_unsafe_no_auth`, reverse-proxy headers, dataplane `9090/9091`, and backup-administrator warnings.
- [ ] `sudo mnemonas-doctor --public-domain <domain>` reports HTTP-to-HTTPS redirect behavior, a matching HTTPS certificate with at least 30 days remaining, verified renewal guidance, and no direct backend exposure, dataplane exposure, or UFW allow warnings.
- [ ] The [Public cloud firewall checklist](cloud-firewall-checklist.en.md) has been applied: cloud security groups or public firewall rules expose only `80/443`; management ports, the Web backend port, and dataplane ports are not publicly reachable.
- [ ] Public deployments use HTTPS.

Runtime checks:

```bash
sudo mnemonas-doctor --public-domain <domain>
# Checks HTTPS health, HTTP-to-HTTPS redirect behavior, certificate hostname, 30-day certificate validity, renewal guidance, direct backend exposure, and dataplane exposure.

ss -tlnp | grep 8080
ss -tlnp | grep -E '9090|9091'

curl -I https://<domain>/health

curl --connect-timeout 3 http://<domain>:8080/health
# expected: failed connection or timeout

curl https://<domain>/dav/
# expected: 401 Unauthorized when WebDAV authentication is enabled
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

MnemoNAS supports users, roles, groups, and directory access rules. Non-admin users are limited by `home_dir` unless a matching `storage.directory_access_rules` entry grants read or write access; files, search, favorites, shares, trash, activity log views, and WebDAV `users` mode use the same path decision. Admins can set `quota_bytes`; non-admin Web/API uploads, copies, trash restores, and WebDAV PUT/COPY writes in `webdav.auth_type = "users"` are checked against the current logical size of that user's `home_dir`.

WebDAV `users` mode carries application user identity and enforces role, group, `home_dir`, and directory access-rule boundaries. WebDAV `basic` mode remains a global service credential compatibility mode.

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

File downloads, version previews, media previews, thumbnails, and external-open flows use a short-lived `HttpOnly` download-session cookie. Long-lived access tokens are not passed through URL query parameters.

The `Secure` cookie flag is enabled when the request is actually HTTPS, or when `trusted_proxy_hops > 0` and a trusted private/loopback proxy forwards `X-Forwarded-Proto=https`.

### Web UI Session Tokens

The Web UI stores the primary access and refresh session in `HttpOnly`, `SameSite=Lax` cookies. It no longer writes bearer access or refresh tokens to `localStorage`; REST API calls, uploads, refresh, and logout use same-origin cookies sent by the browser. Legacy tokens left by older versions are cleared during initialization, refresh, logout, and related auth paths.

For REST mutations and WebDAV write methods (`POST`, `PUT`, `PATCH`, `DELETE`, `MKCOL`, `COPY`, `MOVE`, `PROPPATCH`, `LOCK`, `UNLOCK`) that carry browser `Origin`, `Referer`, or `Sec-Fetch-Site` metadata, the server rejects requests whose source scheme, host, or port does not match the current request. It also rejects browser requests explicitly marked `cross-site` or `same-site` when they do not use an `Authorization` header. Script clients without browser origin metadata and explicit `Authorization` API clients continue to work.

API clients can still use `Authorization: Bearer <access-token>` and JSON refresh tokens for scripts and automation. The server adds security headers, CSP, and `Permissions-Policy`; file download, version preview, thumbnail, WebDAV file, and WebDAV directory-listing responses also include `X-Content-Type-Options: nosniff` and a sandbox CSP to reduce script execution when user files are opened in the browser. Public deployments still need careful origin hygiene.

For public deployments:

- Serve only trusted static assets.
- Use HTTPS.
- Avoid injecting third-party scripts under the same origin.
- Sign out on shared computers.

Signing out, changing a user's password, deleting the user, disabling the user, or manually revoking that user's active sessions clears the relevant sessions.

### Public Share Passwords

Password-protected public shares issue an `HttpOnly` cookie after successful password validation. The cookie is scoped to the matching `/s/<id>` and `/api/v1/public/shares/<id>` paths. Folder browsing and downloads use that cookie instead of passing passwords in URLs.

After clearing site data, switching browser, or changing the share password, the password must be entered again.

Five failed password attempts for the same share and client address lock access for five minutes and return `429 Too Many Requests`.

## Security Capability Status

| Status | Capability |
| --- | --- |
| Supported | Web UI login, users/roles/groups, user root-directory isolation, directory access rules, user session revocation, WebDAV user auth/global Basic Auth, path traversal protection, WebDAV read-only mode, share password validation and lockout |
| Add through reverse proxy | HTTPS certificate renewal, finer rate limits, public access controls |
| Planned | OAuth/OIDC integration, finer application-level access policies |

## More Resources

- [Reverse proxy setup](reverse-proxy-setup.en.md)
- [Docker deployment](docker-deployment.en.md)
- [FAQ](faq.en.md)
- [Configuration reference](configuration.en.md)
