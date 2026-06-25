# Security Hardening Guide

English | [简体中文](security.md)

This guide describes recommended MnemoNAS security settings for LAN and public deployments.

## Web UI Authentication

Web UI authentication is enabled by default. On first startup with no user data, MnemoNAS creates an administrator account and writes the initial password to `initial-password.txt` next to `auth.users_file`. The default path is:

```text
<storage.root>/.mnemonas/initial-password.txt
```

Notes:

- The initial administrator password is not stored permanently in `secrets.json`.
- The setup API does not return the initial username or password.
- The password is not printed in clear text by default. For controlled local debugging only, set `MNEMONAS_PRINT_INITIAL_PASSWORD=1` before first startup.
- Login, session refresh, and service restart retain `initial-password.txt`. The file is removed only after the corresponding administrator's password is successfully changed or reset.
- Change the administrator password after first login. Until the bootstrap administrator changes it, the authenticated session can access only current-user information, password change, and logout endpoints; other product APIs remain unavailable.
- New passwords must contain 8 through 72 UTF-8 bytes and must not consist only of whitespace.
- WebDAV `auth_type = "users"` also rejects accounts that still require a password change; mounts become available after self-service password change succeeds.
- The administrator Dashboard first-run check uses server-side account, initial-password-file, security-self-check, backup, and restore-verification evidence. It does not use browser-local checkboxes and does not replace pre-deployment `mnemonas-doctor` or cloud-firewall review.

Systemd default path:

```bash
sudo cat /srv/mnemonas/.mnemonas/initial-password.txt
```

Docker default path:

```bash
cat ~/.mnemonas/.mnemonas/initial-password.txt
```

### Offline Administrator Credential Recovery

When an existing enabled administrator loses the password, a local server operator can run the offline recovery command:

```bash
nasd --config <config-path> --recover-admin <existing-enabled-admin>
```

The recovery command enforces these security boundaries:

- Stop `nasd` before running the command. The service and recovery command both exclusively acquire `auth-state.lock` next to `auth.users_file`; a running service or another recovery command causes the new command to be rejected. Root or the current service account must own the authentication-state path, the authentication-state directory must not be writable by group or other users, and no ancestor may be replaceable by another local account. Otherwise, the service rejects the lock.
- The configuration must keep `auth.enabled = true`. The target must already exist in `users.json`, be enabled, and have the `admin` role. The command does not create or enable an account and does not elevate a role.
- The command does not accept a caller-supplied password. It generates a random temporary password and writes `initial-password.txt` next to `auth.users_file` with mode `0600`.
- Standard output contains only the administrator username, credential-file path, and non-sensitive status information. It does not contain the temporary password. Treat the credential file as a clear-text password and permit only authorized local operators to read it.
- Recovery revokes every existing session for that administrator. The temporary password requires an immediate password change after login; until then, the account has the same restricted access as the bootstrap administrator.
- A recovery marker in the credential file makes an interrupted operation safely resumable. A pending, conflicting, or malformed recovery marker blocks normal `nasd` startup. If the command exits unexpectedly, keep the service stopped and rerun the command with the same administrator username.
- MnemoNAS does not expose an anonymous or remote HTTP administrator-recovery endpoint. Recovery authority comes from host access to the config, authentication-state files, and service account.

The command depends on Unix file modes and no-follow file operations, and the filesystem containing the authentication-state directory must support same-directory hard links. It supports Unix systems and Linux environments. On Windows, use WSL2; native Windows binaries do not support offline recovery. If the filesystem does not support hard links, the command exits safely before changing users or sessions.

For deployment-specific commands, see the [Linux/systemd deployment guide](linux-systemd-deployment.en.md#administrator-password-recovery) or [Docker deployment guide](docker-deployment.en.md#administrator-password-recovery).

## WebDAV Authentication

Configure WebDAV in `config.toml`:

```toml
[webdav]
enabled = true
auth_type = "users"
```

`auth_type = "users"` is recommended for day-to-day mounting. WebDAV clients log in with MnemoNAS usernames and passwords; admins see the global namespace; regular users see their `home_dir` as the mount root; guest users are read-only; user quotas limit WebDAV PUT/COPY/MOVE writes into `home_dir`.

For legacy setups or a separate service credential, use global Basic Auth:

```toml
[webdav]
enabled = true
auth_type = "basic"
username = "admin"
password = ""
```

When `password` is empty and Basic Auth is enabled, MnemoNAS generates a 16-character human-readable WebDAV password on first startup. It includes lowercase letters, uppercase letters, and digits, and the generated character set excludes ambiguous characters. The password is stored in:

```text
<storage.root>/secrets.json
```

This WebDAV password is separate from the Web UI administrator password. Startup logs show where to find the credentials but do not print the password.

`mnemonas-doctor` reports whether `config.toml` is a symlink, passes through symlink components, has an unexpected file type, or is broadly readable. It also reports a missing `users.json` when authentication is enabled, and reports whether `users.json`, `secrets.json`, or their relevant parent directories are symlinks, pass through symlink components, have unexpected file types, or are broadly readable.

The Web UI security self-check also marks runtime config-file, generated WebDAV credentials, users-file, or initial-password paths that pass through symlink components as `block`, and reports broadly readable config and generated WebDAV credentials files. Long-running deployments should keep these paths as private regular files and directories.

When authentication is enabled, `mnemonas-doctor` parses `users.json` and confirms that at least one enabled administrator is available. If no usable administrator exists, the next startup creates a recovery administrator and the diagnostic output reports that recovery posture.

`mnemonas-doctor` also reports unauthenticated postures such as `auth.enabled=false`, `webdav.auth_type="none"`, and `security.allow_unsafe_no_auth=true`. These configurations are suitable only when a controlled network, VPN, firewall, or outer access-control layer deliberately limits the reachable audience.

Custom password:

```toml
[webdav]
password = "" # leave empty to use generated credentials; use a password-manager value for custom credentials
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

When custom Web backend or dataplane ports are used, replace `8080/9090/9091` with the actual ports, and keep dataplane ports closed to public and untrusted networks.

### Public Access

Do not expose MnemoNAS directly to the public internet. Put it behind HTTPS using Caddy, Nginx, Traefik, Cloudflare Tunnel, or another trusted reverse proxy.

Recommended path:

1. Run `sudo mnemonas-public-setup --proxy caddy <domain> <email>` or `sudo mnemonas-public-setup --proxy nginx <domain> <email>` on the server to generate and install reverse-proxy configuration.
2. After logging into the Web UI, open `System Settings -> General -> Public Access Wizard` and apply the recommended `server.host`, `trusted_proxy_hops`, share base URL, and related settings for the chosen deployment mode.
3. Run the Web UI security self-check to verify authentication, session-token lifetimes, login throttling, browser session-cookie boundaries, public-share cookie/cache boundaries, config-file permissions, generated WebDAV credentials-file permissions, users-file permissions, HTTPS request semantics, trusted-proxy handling, listener scope, dataplane ports, WebDAV authentication, share base URL, share default policy, local backup destinations, initial password file state, and administrator-account redundancy.
4. Run `sudo mnemonas-doctor --public-domain <domain>` on the server to re-check the public domain, reverse proxy, and local port exposure.
5. Run `./scripts/public-go-live-smoke.sh <domain>` from an external network to verify public HTTPS, same-domain HTTP-to-HTTPS redirect, and public TCP reachability for the Web backend and dataplane ports.

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
        proxy_set_header Destination $http_destination;
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
- [ ] WebDAV uses `auth_type = "users"`, or global Basic Auth credentials are recorded and changed to a strong custom or generated password, without placeholder values.
- [ ] `webdav.auth_type` is not `none` unless the server is loopback-only.
- [ ] Public deployments use `server.host = "127.0.0.1"` and are reachable only through the HTTPS reverse proxy.
- [ ] The administrator Dashboard first-run requirements are complete according to server-side evidence. If backup requirements were deferred, an explicit reminder deadline is recorded.
- [ ] Dataplane gRPC/HTTP ports are loopback-only or private.
- [ ] The Web UI security self-check has no `block` items; public deployments should resolve all `warning` items before exposure.
- [ ] The security self-check covers `allow_unsafe_no_auth`, session-token lifetimes, login throttling, browser session-cookie boundaries, public-share cookie/cache boundaries, config-file permissions, generated WebDAV credentials-file permissions, users-file permissions, reverse-proxy headers, dataplane ports, local backup destinations, share base URL, share default policy, and spare-administrator warnings.
- [ ] `sudo mnemonas-doctor --public-domain <domain>` reports HTTP redirects to HTTPS on the same public domain, a matching HTTPS certificate with at least 30 days remaining, verified renewal guidance, and a non-symlink config file path that does not pass through symlink components and parses as valid TOML.
- [ ] `mnemonas-doctor --public-domain` reports usable administrator-account redundancy, public-safe session-token lifetimes, a non-symlink users file and users-file directory that do not pass through symlink components and have private permissions, and a non-symlink generated WebDAV credentials file that does not pass through symlink components and has private permissions.
- [ ] `mnemonas-doctor --public-domain` reports an absent `initial-password.txt` path with no symlink, symlink component, or non-regular file left behind.
- [ ] Public-share base URL, public-share default policy, and public-share JSON response boundaries have been reviewed (private cache, `Vary: Cookie`, no probe cookie), anonymous WebDAV `PROPFIND` is rejected, and there are no direct backend exposure, dataplane exposure, or UFW allow warnings.
- [ ] `./scripts/public-go-live-smoke.sh <domain>` has passed from an external network, covering public HTTPS, same-domain HTTP-to-HTTPS redirect, and external reachability for the Web backend and dataplane ports.
- [ ] The [Public cloud firewall checklist](cloud-firewall-checklist.en.md) has been applied: cloud security groups or public firewall rules expose only `80/443`; management ports, the Web backend port, and dataplane ports are not publicly reachable.
- [ ] Public deployments use HTTPS.

Runtime checks:

```bash
sudo mnemonas-doctor --public-domain <domain>
# Checks HTTPS health, HTTP redirects to HTTPS on the same public domain, certificate hostname, 30-day certificate validity, renewal guidance, public-share JSON response boundaries, anonymous WebDAV PROPFIND, direct backend exposure, and dataplane exposure.

ss -tlnp | grep 8080
ss -tlnp | grep -E '9090|9091'

curl -I https://<domain>/health

./scripts/public-go-live-smoke.sh <domain>
# expected: public HTTPS health is reachable, same-domain HTTP redirects to HTTPS, and backend ports are unreachable from the external network.

curl --connect-timeout 3 --max-time 10 http://<domain>:8080/health
# expected: failed connection or timeout, with no HTTP status response.
# Any successful TCP connection means the backend port is still publicly reachable, even when no HTTP status is returned.
# For custom backend ports, check that those ports also fail or time out without an HTTP status response.

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

MnemoNAS supports users, roles, groups, and directory access rules. Non-admin users are limited by `home_dir` unless a matching `storage.directory_access_rules` entry grants read or write access; files, search, favorites, shares, trash, activity log views, and WebDAV `users` mode use the same path decision. Admins can set `quota_bytes`; non-admin Web/API uploads, copies, moves, trash restores, and WebDAV PUT/COPY/MOVE writes in `webdav.auth_type = "users"` are checked against the current logical size of that user's `home_dir` when the write target is inside that `home_dir`. Use directory quotas for shared-directory capacity limits.

WebDAV `users` mode carries application user identity and enforces role, group, `home_dir`, and directory access-rule boundaries. WebDAV `basic` mode remains a global service credential compatibility mode.

### Rate Limiting

MnemoNAS includes a built-in concurrent request limit, with 100 requests allowed by default. Web UI login also limits password-credential checks by client IP before bcrypt runs: each fixed 10-second window permits at most 12 checks. Excess requests return `429 LOGIN_RATE_LIMITED`. Requests admitted by that window are still tracked by normalized username and client IP, with a short lockout after the consecutive-failure threshold. Other APIs do not provide a general per-IP or per-user rate limiter; add site-wide limits at the reverse proxy when required.

Nginx example:

```nginx
limit_req_zone $binary_remote_addr zone=api:10m rate=10r/s;
location /api/ {
    limit_req zone=api burst=20;
    proxy_pass http://localhost:8080;
}
```

### Preview and Download Auth

File downloads, version previews, media previews, thumbnails, and external-open flows use a short-lived `HttpOnly`, `SameSite=Strict` download-session cookie. Long-lived access tokens are not passed through URL query parameters.

HTTPS mode uses `__Host-mnemonas_download_access` with `Secure`, `Path=/`, and no `Domain` attribute. Local HTTP mode uses `mnemonas_download_access` with the `/api/v1` path.

HTTPS mode is enabled only when the request uses TLS, or when `trusted_proxy_hops > 0` and the direct peer is loopback or a proxy address listed in `trusted_proxy_cidrs` forwarding `X-Forwarded-Proto=https`.

### Web UI Session Tokens

The Web UI stores the primary access and refresh session in `HttpOnly`, `SameSite=Lax` cookies and does not write bearer access or refresh tokens to `localStorage`.

HTTPS mode uses `__Host-mnemonas_access` and `__Host-mnemonas_refresh`; both cookies use `Secure`, `Path=/`, and no `Domain` attribute. Local HTTP mode uses `mnemonas_access` with `/api/v1` and `mnemonas_refresh` with `/api/v1/auth`.

HTTPS requests parse only `__Host-` names, and HTTP requests parse only unprefixed names. The server rejects authentication when one request contains different values for the same cookie name, or when access and download cookies belong to different accounts.

`auth.access_token_ttl` must be at least `30s`. For public deployments, keep it at or below `1h` and keep `auth.refresh_token_ttl` at or below `720h` (30 days). The security self-check reports longer values as warnings.

Each login creates and durably registers an independent active session before the first tokens are issued. MnemoNAS retains at most 64 active sessions per user and 4096 active sessions globally. Exceeding either limit returns `429 REFRESH_SESSION_LIMIT`; logout or expiry releases capacity.

Refresh-token rotation does not extend the absolute session expiry established at login. A session can rotate at most once every 30 seconds, and a refresh token can succeed only once. Replaying a rotated token revokes the same session family. The refresh cookie still permits logout after the access token expires.

REST API calls, uploads, refresh, and logout use same-origin cookies sent by the browser. The Web UI uses a `storage` signal and `BroadcastChannel` to synchronize session changes across tabs, and Web Locks to serialize cross-tab refresh. These channels carry only session-change signals, not token values. Legacy tokens left in `localStorage` by older versions are cleared by authentication flows.

For REST mutations and WebDAV write methods (`POST`, `PUT`, `PATCH`, `DELETE`, `MKCOL`, `COPY`, `MOVE`, `PROPPATCH`, `LOCK`, `UNLOCK`) that carry browser `Origin`, `Referer`, or `Sec-Fetch-Site` metadata, the server rejects requests whose source scheme, host, or port does not match the current request. It also rejects browser requests explicitly marked `cross-site` or `same-site` when they do not use an `Authorization` header. Script clients without browser origin metadata and explicit `Authorization` API clients continue to work.

API clients can still use `Authorization: Bearer <access-token>` and JSON refresh tokens for scripts and automation. The server adds security headers, CSP, and `Permissions-Policy`; file download, version preview, thumbnail, WebDAV file, and WebDAV directory-listing responses also include `X-Content-Type-Options: nosniff` and a sandbox CSP to reduce script execution when user files are opened in the browser. Public deployments still need careful origin hygiene.

For public deployments:

- Serve only trusted static assets.
- Use HTTPS.
- Avoid injecting third-party scripts under the same origin.
- Sign out on shared computers.

Signing out, changing a user's password, deleting the user, disabling the user, or manually revoking that user's active sessions clears the relevant sessions.

`auth-sessions.json` is the authoritative, bounded active-session registry. It uses schema v3 and has a 2 MiB file limit. A signed JWT is rejected when its session ID is absent from the registry. Logout deletes the record instead of retaining a tombstone, so repeated login and logout do not grow the file linearly.

The service persists a 60-second restart-time lease in `auth-sessions.json` and attempts renewal with 15 seconds remaining. A hard renewal failure still permits validation while the existing lease remains valid. If renewal continues to fail after the lease expires, the server fails closed with `503 TOKEN_STATE_UNAVAILABLE` so a wall-clock rollback cannot restore an invalid session.

`auth-state.lock` enforces one writer per authentication-state directory. Normal startup and offline administrator recovery must acquire this process-level exclusive lock. Another `nasd` instance or recovery command is rejected and cannot share `users.json` and `auth-sessions.json` with the current writer.

At startup, the service advances logical validation time to the lower bound of a new 60-second lease. Rapid consecutive crash restarts therefore advance logical time by approximately 60 seconds per start and can expire active sessions earlier than wall-clock time.

If authentication-state persistence fails before the atomic rename, login and refresh do not publish new cookies, and logout does not clear existing cookies. If the rename has committed but the parent-directory sync is uncertain, the operation remains successful and returns an HTTP `Warning` header.

`auth-sessions.json` and `users.json` use strict, explicitly versioned formats. Authentication initialization fails when either file is corrupt, omits required fields, contains unknown fields, omits its version, or declares an unsupported version. Authentication also rejects invalid bcrypt password hashes, role and `home_dir` combinations, or `quota_bytes` invariants in `users.json`.

### Public Share Passwords

Password-protected public shares issue an `HttpOnly`, `SameSite=Strict` cookie after successful password validation. The cookie is scoped to the matching `/s/<id>` and `/api/v1/public/shares/<id>` paths. Folder browsing and downloads use that cookie instead of passing passwords in URLs.

Public share metadata, password-validation responses, and folder-listing responses include `Cache-Control: private, no-cache`, `Vary: Cookie`, `X-Content-Type-Options: nosniff`, and `Referrer-Policy: no-referrer` so cookie-dependent share metadata is not reused by browser or intermediary caches.

The security self-check blocks invalid public-share cookie, failure-throttling, or cache boundaries before reporting HTTPS-only `Secure` cookie warnings.

After clearing site data, switching browser, or changing the share password, the password must be entered again.

Five failed password attempts for the same share and client address lock access for five minutes and return `429 Too Many Requests`.

For family public sharing, keep newly created shares expiring by default, for example after 7 days, and set an explicit default access-count limit. The security self-check reports no default expiry, values above `720h` (30 days), or unlimited default access counts as warnings.

When sharing is enabled, `share.base_url` should use HTTPS on the default port, have a valid host, and contain no userinfo, query string, fragment, encoded query or fragment marker, backslash, duplicated path slash, or `.`/`..` path segments. It should be the site origin or application base path and should not include the `/s` share route. The security self-check and `mnemonas-doctor --public-domain` report base URLs that do not meet public-deployment requirements.

## Security Capability Status

| Status | Capability |
| --- | --- |
| Supported | Web UI login, users/roles/groups, user root-directory isolation, directory access rules, user session revocation, offline administrator credential recovery, WebDAV user auth/global Basic Auth, path traversal protection, WebDAV read-only mode, share password validation and lockout |
| Add through reverse proxy | HTTPS certificate renewal, finer rate limits, public access controls |
| Planned | OAuth/OIDC integration, finer application-level access policies |

## More Resources

- [Reverse proxy setup](reverse-proxy-setup.en.md)
- [Docker deployment](docker-deployment.en.md)
- [FAQ](faq.en.md)
- [Configuration reference](configuration.en.md)
