# Public Access Templates

English | [简体中文](README.md)

These templates provide optional starting points for public HTTPS access paths that are not fully automated by `mnemonas-public-setup`.

- `traefik/`: Traefik file-provider template for a Linux host where MnemoNAS runs on the same machine as the reverse proxy.
- `cloudflare-tunnel/`: Cloudflare Tunnel ingress template for deployments without a directly reachable public IP.

The `nas.example.com` placeholder should be replaced with the normalized public domain. The domain should be lowercase and without a single FQDN trailing dot, and it should not include a scheme, path, query string, user information, or port.

Public deployments should still run:

```bash
sudo mnemonas-doctor --public-domain nas.example.com
```

Cloud firewalls or security groups must expose only `80/443` publicly. MnemoNAS backend `8080`, dataplane `9090/9091`, and any custom backend or dataplane ports must not be exposed to the public internet.
