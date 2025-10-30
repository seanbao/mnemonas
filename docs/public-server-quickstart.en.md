# Public Server Quickstart

English | [简体中文](public-server-quickstart.md)

This guide is for a public Linux server where MnemoNAS runs locally and users access it through a public HTTPS domain. The recommended path is:

```text
Public 80/443 -> Caddy/Nginx -> 127.0.0.1:8080 -> MnemoNAS
```

Do not expose MnemoNAS `8080` or dataplane `9090/9091` directly to the public internet. Custom backend ports must remain private as well.

## Prerequisites

- Ubuntu 22.04/24.04, Debian, or a similar Linux server
- A domain such as `nas.example.com`
- A/AAAA DNS records pointing to the server public IP
- Cloud security group or firewall allowing TCP `80/443`
- SSH limited to trusted IPs, VPN, Tailscale/Headscale, or another private network

If DNS is not ready yet, wait before requesting certificates:

```bash
dig +short nas.example.com
```

## 1. Install MnemoNAS

Install the systemd services from a release package:

```bash
tar -xzf mnemonas-<version>-linux-amd64.tar.gz
cd mnemonas-<version>-linux-amd64

sudo ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

Initial login password:

```bash
sudo cat /srv/mnemonas/.mnemonas/initial-password.txt
```

First access the app through the server, an SSH tunnel, or a temporary trusted network.
SSH tunneling is preferred because it does not require opening `8080` temporarily:

```bash
ssh -L 18080:127.0.0.1:8080 <user>@<server-ip>
```

Then open this URL on the local machine:

```text
http://localhost:18080
```

Change the administrator password immediately after login.

## 2. Configure Public HTTPS

Caddy is the recommended default. The helper script will:

- install and configure Caddy or Nginx;
- bind MnemoNAS `[server].host` to `127.0.0.1`;
- set `trusted_proxy_hops = 1`;
- restart `mnemonas.service`;
- allow local `80/443`, remove broad UFW allow rules for `8080/9090/9091` or custom backend ports, and restrict direct access;
- run basic public-entry checks.

If the basic checks confirm that backend control-plane or dataplane ports still listen on non-loopback addresses, or if the HTTPS health check or `mnemonas-doctor --public-domain` fails, the script stops and asks for remediation before a rerun instead of printing the completion summary.

```bash
sudo mnemonas-public-setup --proxy caddy nas.example.com admin@example.com
```

For Nginx:

```bash
sudo mnemonas-public-setup --proxy nginx nas.example.com admin@example.com
```

`mnemonas-public-setup` first lowercases the domain and removes a single FQDN trailing dot.
It then writes the normalized value into reverse-proxy config, certificate commands, and the completion summary.

For Traefik or Cloudflare Tunnel, start from repository templates:

- `deploy/public-access/traefik/`
- `deploy/public-access/cloudflare-tunnel/config.yml`

See [Public access templates](../deploy/public-access/README.en.md) for the template notes.
See [Reverse proxy setup](reverse-proxy-setup.en.md) for the full configuration.

After logging into the Web UI, `System Settings -> General -> Public Access Wizard` can also apply the recommended app settings:

- enter the public domain;
- select Caddy or Nginx;
- click "Apply recommendation to form", then save settings;
- run the displayed `mnemonas-public-setup` and `mnemonas-doctor --public-domain` commands on the server to finish proxy setup and verification.

The Web UI wizard adjusts the MnemoNAS form settings for a reverse-proxy deployment.
Certificate issuance, firewall changes, and reverse-proxy installation still need to run on the server.

The script cannot modify cloud-provider security groups.
After running it, use the [Public cloud firewall checklist](cloud-firewall-checklist.en.md) and confirm in the cloud console:

| Port | Recommendation |
| --- | --- |
| `80/tcp` | Public, for HTTP-to-HTTPS redirect and certificate issuance |
| `443/tcp` | Public, for Web, API, and WebDAV |
| `22/tcp` | Trusted IPs or private network only |
| `8080/tcp` | Not public |
| `9090/tcp` | Not public |
| `9091/tcp` | Not public |

## 3. Verify

Public HTTPS should work:

```bash
curl -I https://nas.example.com/health
```

Direct backend ports should fail to connect or time out. Any HTTP status response, including `401`, `403`, or `404`, means the port is still publicly reachable:

```bash
curl --connect-timeout 3 http://nas.example.com:8080/health
curl --connect-timeout 3 http://nas.example.com:9090/
curl --connect-timeout 3 http://nas.example.com:9091/health
```

On the server:

```bash
sudo mnemonas-doctor
sudo mnemonas-doctor --public-domain nas.example.com
ss -tlnp | grep -E '80|443|8080|9090|9091'
```

The `--public-domain` check lowercases the domain, removes a single FQDN trailing dot, and then verifies:

- public HTTPS health, same-domain HTTP-to-HTTPS redirects, certificate hostname, and remaining certificate validity;
- public-deployment authentication settings, administrator-account redundancy, and initial-password cleanup;
- share-link base URL shape and public-share API response cache boundaries;
- direct backend exposure, dataplane port exposure, and local UFW rules for backend control-plane or dataplane ports.

Certificate inspection requires `openssl` on the server; without `openssl`, `mnemonas-doctor --public-domain` fails. Cloud security groups or upstream firewalls still need a separate checklist review.
Public HTTP must redirect to HTTPS on the same domain; unreachable HTTP, non-redirect responses, or redirects to another domain make `mnemonas-doctor --public-domain` fail.

Public authentication checks require `auth.enabled = true`, `security.allow_unsafe_no_auth = false`, and authenticated WebDAV when WebDAV is enabled.
If global Basic Auth remains in use, explicit common placeholder passwords or passwords shorter than 16 characters produce a warning.
For generated Basic Auth, `secrets.json` must exist, be a private regular file, not be a symlink, and not pass through symlink path components.

Administrator redundancy is checked from `auth.users_file`, or `$STORAGE_ROOT/.mnemonas/users.json` when it is unset.
When a custom `auth.users_file` moves the checked password file location, the doctor checks the same user-data directory.
The users file check requires that the users file and its directory must not be symlinks and must not pass through symlink components.
The users file must be a valid list with:

- unique non-empty `id` and `username` values;
- valid `role` and `disabled` fields;
- bcrypt-format `password_hash` values for enabled administrators.

Missing files, invalid JSON, invalid structure, unusable administrator hashes, or zero enabled administrators fail the check.
One usable administrator warns; two or more usable administrators pass.

The initial password check uses `initial-password.txt` in the same user-data directory as the users file.
A symlink, symlink component, or non-regular file at that path fails in public deployments.

When sharing is enabled, `share.base_url` should use HTTPS on the default port, have a valid host, and contain no userinfo, query string, fragment, backslash, duplicated path slash, or `.`/`..` path segments.
HTTP, non-443 ports, userinfo, query strings, fragments, backslashes, duplicated path slashes, `.`/`..` path segments, or invalid hosts fail.
Empty values, a different host, or a path already ending in `/s` produce a manual review warning.
Generated share URLs may otherwise contain a duplicated `/s/s` route.

The public-share API response check uses a reserved probe ID.
It verifies only that the missing-share JSON response reaching MnemoNAS includes:

- `Cache-Control: private, no-cache`;
- `Vary: Cookie`;
- `X-Content-Type-Options: nosniff`;
- `Referrer-Policy: no-referrer`.
The probe response must not set `Set-Cookie`.
It does not read real share IDs, passwords, or cookie values.
When public sharing is enabled, the public-share API probe must reach MnemoNAS. If the reverse proxy or an access-control layer returns `401`, `403`, a redirect, or another response that cannot prove MnemoNAS handled the lookup, `mnemonas-doctor --public-domain` fails. Cache and security-header validation runs only after the probe receives MnemoNAS's missing-share JSON response.

The report also lists detectable renewal paths.
Nginx/certbot deployments should pass `sudo certbot renew --dry-run`.
Caddy deployments should have clean ACME logs from `sudo journalctl -u caddy --since '24 hours ago'`.

For public deployments, `mnemonas-doctor --public-domain` checks `auth.access_token_ttl` and `auth.refresh_token_ttl`.
Access tokens longer than `1h` or refresh tokens longer than `720h` (30 days) produce warnings.
Empty, `0`, or negative values fail.

When sharing is enabled, `mnemonas-doctor --public-domain` also checks `share.default_expires_in` and `share.default_max_access`.
No default expiry, values longer than `720h` (30 days), or `0` default access limits produce warnings.
Negative expiry or negative default access limits fail.
For family public sharing, keep the default at `168h` (7 days) or another explicit expiry no longer than 30 days.
Set an explicit default access limit such as `20`.

Expected state:

- Caddy/Nginx listens on `0.0.0.0:80` and `0.0.0.0:443`;
- MnemoNAS Web/API/WebDAV listens only on `127.0.0.1:8080`;
- dataplane `9090/9091`, or custom dataplane ports, listen only on `127.0.0.1`.
- Local port inspection covers IPv4 and IPv6; `iproute2` should be installed for `ss`, or both `/proc/net/tcp` and `/proc/net/tcp6` must be readable when `ss` is unavailable.
- The host running `mnemonas-doctor --public-domain` has `curl`, `python3`, and `openssl` installed for public HTTP(S) entry checks, duration parsing, `users.json` administrator-redundancy checks, generated WebDAV credential checks, and HTTPS certificate checks.

## 4. WebDAV URL

Use HTTPS for public WebDAV:

```text
https://nas.example.com/dav
```

For public deployments, prefer MnemoNAS user authentication.
WebDAV clients then use Web UI account credentials and inherit `home_dir`, directory grants, and quota boundaries:

```toml
[webdav]
auth_type = "users"
```

When keeping the legacy global Basic Auth mode, WebDAV credentials are separate from the Web UI administrator password.
The generated value is visible on the Web settings page after administrator login.
For command-line troubleshooting, print only the `webdav_password` field so JWT signing secrets are not shown together:

```bash
sudo python3 -c 'import json; print(json.load(open("/srv/mnemonas/secrets.json", encoding="utf-8")).get("webdav_password", ""))'
```

The full `secrets.json` also contains runtime signing secrets and should not be copied into support requests, chats, or logs.
Keep it as a regular file with private permissions, and keep its path free of symlink components.

A custom strong Basic Auth password can be set in `[webdav]` in `/etc/mnemonas/config.toml`, followed by validation and restart:

```bash
sudo nasd --check-config --config /etc/mnemonas/config.toml
sudo systemctl restart mnemonas
```

## 5. Go-Live Checklist

- [ ] The initial administrator password has been changed.
  The `initial-password.txt` path is absent with no symlink, symlink component, or non-regular file left behind.
- [ ] At least two enabled administrator accounts exist, so one lost password does not lock out maintenance.
- [ ] The Web UI security self-check has no `block` items; all `warning` items are fixed or explicitly accepted.
- [ ] `https://nas.example.com/health` works.
- [ ] `http://nas.example.com/health` redirects to an HTTPS URL on the same domain.
- [ ] Public `8080/9090/9091`, or custom backend ports, are unreachable.
- [ ] The host running `mnemonas-doctor --public-domain` can use `ss`, or can read both `/proc/net/tcp` and `/proc/net/tcp6`.
- [ ] The host running `mnemonas-doctor --public-domain` has `curl`, `python3`, and `openssl` installed.
- [ ] Local UFW has no broad allow rule for `8080/9090/9091` or custom backend ports.
- [ ] `/etc/mnemonas/config.toml` is a private regular file, does not pass through symlink components, and parses as valid TOML.
- [ ] `/etc/mnemonas/config.toml` has `server.host = "127.0.0.1"`.
- [ ] `/etc/mnemonas/config.toml` has `trusted_proxy_hops = 1`.
- [ ] `/etc/mnemonas/config.toml` has `security.allow_unsafe_no_auth = false`.
- [ ] `/etc/mnemonas/config.toml` has `auth.access_token_ttl <= 1h` and `auth.refresh_token_ttl <= 720h`.
- [ ] If WebDAV keeps global Basic Auth with a generated password, `<storage.root>/secrets.json` exists.
  It is not a symlink, does not pass through symlink components, is a regular file, and has private permissions.
- [ ] If public sharing is enabled, `share.base_url` uses HTTPS on the default port with a valid host and no userinfo, query string, fragment, backslash, duplicated path slash, or `.`/`..` path segments; `share.default_expires_in` is not empty or `0`, is at most `720h`, and `share.default_max_access` is greater than `0`.
  The `mnemonas-doctor --public-domain` public-share probe reaches MnemoNAS with a passing JSON response boundary check: private cache, `Vary: Cookie`, and no probe cookie.
- [ ] Cloud security group exposes only `80/443`, with SSH limited to trusted sources.
- [ ] External backups exist; this public server is not the only copy of important data.

See [Reverse proxy setup](reverse-proxy-setup.en.md) for more proxy details and [Security Hardening Guide](security.en.md) for hardening notes.
