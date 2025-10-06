# Public Access Templates

English | [简体中文](README.md)

These templates provide optional starting points for public HTTPS access paths that are not fully automated by `mnemonas-public-setup`.

- `traefik/`: Traefik file-provider template for a Linux host where MnemoNAS runs on the same machine as the reverse proxy.
- `cloudflare-tunnel/`: Cloudflare Tunnel ingress template for deployments without a directly reachable public IP.

The `nas.example.com` placeholder should be replaced with the normalized public domain. The domain should be lowercase and without a single FQDN trailing dot, and it should not include a scheme, path, query string, user information, or port.

After copying a template, the MnemoNAS runtime configuration still needs to match the public entry:

```toml
[server]
host = "127.0.0.1"
trusted_proxy_hops = 1

[share]
base_url = "https://nas.example.com"
```

`host` should be restricted to the local listener. `trusted_proxy_hops = 1` lets MnemoNAS trust `X-Forwarded-*` headers from same-host Traefik or cloudflared, so HTTPS detection, client addresses, login rate limiting, and download cookies are evaluated correctly. If the reverse proxy does not reach MnemoNAS from loopback, list the proxy IP address or CIDR in `[server].trusted_proxy_cidrs`. When sharing is enabled, `share.base_url` should use the public HTTPS domain.

Public deployments should still run:

```bash
sudo mnemonas-doctor --public-domain nas.example.com
```

Cloud firewalls or security groups must expose only `80/443` publicly. MnemoNAS backend `8080`, dataplane `9090/9091`, and any custom backend or dataplane ports must not be exposed to the public internet.
