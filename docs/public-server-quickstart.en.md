# Public Server Quickstart

English | [简体中文](public-server-quickstart.md)

This guide is for a public Linux server where MnemoNAS runs locally and users access it through a public HTTPS domain. The recommended path is:

```text
Public 80/443 -> Caddy/Nginx -> 127.0.0.1:8080 -> MnemoNAS
```

Do not expose MnemoNAS `8080` or dataplane `9090/9091` directly to the public internet.

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

First access the app through the server, an SSH tunnel, or a temporary trusted network. SSH tunneling is preferred because it does not require opening `8080` temporarily:

```bash
ssh -L 18080:127.0.0.1:8080 <user>@<server-ip>
```

Then open this on your local machine:

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
- allow local `80/443` and restrict direct `8080/9090/9091`;
- run basic public-entry checks.

```bash
sudo mnemonas-public-setup --proxy caddy nas.example.com admin@example.com
```

For Nginx:

```bash
sudo mnemonas-public-setup --proxy nginx nas.example.com admin@example.com
```

For Traefik or Cloudflare Tunnel, start from `deploy/public-access/traefik/` or `deploy/public-access/cloudflare-tunnel/config.yml` and see [Reverse proxy setup](reverse-proxy-setup.en.md).

If you are already logged into the Web UI, you can also open `System Settings -> General -> Public Access Wizard`:

- enter the public domain;
- select Caddy or Nginx;
- click "Apply recommendation to form", then save settings;
- run the displayed `mnemonas-public-setup` and `mnemonas-doctor --public-domain` commands on the server to finish proxy setup and verification.

The Web UI wizard adjusts the MnemoNAS form settings for a reverse-proxy deployment, but certificate issuance, firewall changes, and reverse-proxy installation still need to run on the server.

The script cannot modify cloud-provider security groups. After running it, use the [Public cloud firewall checklist](cloud-firewall-checklist.en.md) and confirm in the cloud console:

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

Direct backend ports should fail or time out:

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

The `--public-domain` check verifies public HTTPS health, HTTP-to-HTTPS redirect behavior, certificate hostname, remaining certificate validity, direct backend exposure, and dataplane port exposure, then prints the manual cloud-firewall review item. Certificate inspection requires `openssl` on the server. It also reports detectable renewal paths: Nginx/certbot deployments should pass `sudo certbot renew --dry-run`, and Caddy deployments should have clean ACME logs from `sudo journalctl -u caddy --since '24 hours ago'`.

Expected state:

- Caddy/Nginx listens on `0.0.0.0:80` and `0.0.0.0:443`;
- MnemoNAS Web/API/WebDAV listens only on `127.0.0.1:8080`;
- dataplane `9090/9091` listen only on `127.0.0.1`.

## 4. WebDAV URL

Use HTTPS for public WebDAV:

```text
https://nas.example.com/dav
```

WebDAV credentials are not the Web UI administrator password. Default credentials are stored in:

```bash
sudo cat /srv/mnemonas/secrets.json
```

You can set a custom strong password in `[webdav]` in `/etc/mnemonas/config.toml`, then restart:

```bash
sudo nasd --check-config --config /etc/mnemonas/config.toml
sudo systemctl restart mnemonas
```

## 5. Go-Live Checklist

- [ ] The initial administrator password has been changed, and `initial-password.txt` is gone or no longer needed.
- [ ] At least two enabled administrator accounts exist, so one lost password does not lock out maintenance.
- [ ] The Web UI security self-check has no `block` items; all `warning` items are fixed or explicitly accepted.
- [ ] `https://nas.example.com/health` works.
- [ ] Public `8080/9090/9091` are unreachable.
- [ ] `/etc/mnemonas/config.toml` has `server.host = "127.0.0.1"`.
- [ ] `/etc/mnemonas/config.toml` has `trusted_proxy_hops = 1`.
- [ ] `/etc/mnemonas/config.toml` has `security.allow_unsafe_no_auth = false`.
- [ ] Cloud security group exposes only `80/443`, with SSH limited to trusted sources.
- [ ] External backups exist; this public server is not the only copy of important data.

See [Reverse proxy setup](reverse-proxy-setup.en.md) for more proxy details and [Security guide](security.en.md) for hardening notes.
