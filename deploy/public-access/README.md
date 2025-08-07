# Public Access Templates

These templates are optional starting points for public HTTPS access paths that are not fully automated by `mnemonas-public-setup`.

- `traefik/`: Traefik file-provider template for a Linux host where MnemoNAS runs on the same machine.
- `cloudflare-tunnel/`: Cloudflare Tunnel ingress template for deployments without a directly reachable public IP.

Public deployments should still run:

```bash
sudo mnemonas-doctor --public-domain nas.example.com
```

Cloud firewalls or security groups must expose only `80/443` publicly. Do not expose MnemoNAS backend `8080` or dataplane `9090/9091` to the public internet.
