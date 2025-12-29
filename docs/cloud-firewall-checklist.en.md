# Public Cloud Firewall Checklist

English | [简体中文](cloud-firewall-checklist.md)

Use this checklist before exposing MnemoNAS through a public domain. Cloud providers use different names such as security groups, network security groups, VPC firewall rules, or security lists, but the required inbound policy is the same.

## Minimum Public Rules

| Port | Protocol | Source | Purpose | Public |
| --- | --- | --- | --- | --- |
| `80` | TCP | `0.0.0.0/0` and `::/0` | HTTP-to-HTTPS redirect and ACME HTTP-01 certificate issuance | Allowed |
| `443` | TCP | `0.0.0.0/0` and `::/0` | Web, API, and WebDAV HTTPS entry | Allowed |
| `22` | TCP | Admin fixed IP, VPN, or bastion host | SSH operations | Do not expose to the whole internet |
| `8080` | TCP | No public source | Direct MnemoNAS Web/API/WebDAV backend | Not public |
| `9090` | TCP | No public source | Internal dataplane gRPC | Not public |
| `9091` | TCP | No public source | Internal dataplane HTTP | Not public |

When the MnemoNAS direct port or dataplane ports are changed, treat those custom ports as not public too. With Cloudflare Tunnel, Tailscale, Headscale, or another tunnel/VPN that does not need direct public ingress, `80/443` may also stay closed. Still keep `8080/9090/9091` and custom backend ports private.

## Provider Mapping

| Provider or platform | Common rule location | Confirm |
| --- | --- | --- |
| AWS EC2 | Security Group inbound rules, Network ACL | Every Security Group attached to the instance keeps `8080/9090/9091` and custom backend ports closed; `22` is restricted to trusted sources |
| Azure VM | Network Security Group inbound rules | Check both NIC-level and subnet-level NSGs; higher-priority rules must not allow backend ports |
| Google Cloud VM | VPC firewall rules, instance network tags | Rules matching the instance tag or service account do not expose backend ports |
| Oracle Cloud | Security Lists, Network Security Groups | Check both subnet Security Lists and instance NSGs |
| Tencent Cloud, Alibaba Cloud, and similar platforms | Security group inbound rules | Check every group attached to the instance; remove temporary debug ports |
| Self-hosted router or home ISP | Router port forwarding, firewall, NAT | Forward only `80/443` to the reverse-proxy host; do not forward backend or dataplane ports |

## Server-Side Verification

Run on the server:

```bash
sudo mnemonas-doctor --public-domain nas.example.com
```

This checks HTTPS health, HTTP redirects to HTTPS on the same public domain, certificate hostname, 30-day certificate validity, direct backend exposure, dataplane exposure, and local listener scope. It cannot read cloud-console firewall rules, so the cloud security group still needs manual review.

Run from an external network:

```bash
./scripts/public-go-live-smoke.sh nas.example.com
```

This script combines public HTTPS health, same-domain HTTP-to-HTTPS redirect, and `8080/9090/9091` backend-port-unreachable checks into one smoke. If the repository script is not available, run the equivalent commands manually from an external network:

```bash
curl -I https://nas.example.com/health
curl --connect-timeout 3 --max-time 10 http://nas.example.com:8080/health
curl --connect-timeout 3 --max-time 10 http://nas.example.com:9090/
curl --connect-timeout 3 --max-time 10 http://nas.example.com:9091/health
```

Expected result:

- `https://nas.example.com/health` is reachable.
- `http://nas.example.com:8080/health` fails to connect or times out, with no HTTP status response.
- `http://nas.example.com:9090/` fails to connect or times out, with no HTTP status response.
- `http://nas.example.com:9091/health` fails to connect or times out, with no HTTP status response.
- Any custom backend ports also fail to connect or time out, with no HTTP status response.

## Common Mistakes

- Opening `8080` or a custom backend port for temporary testing and forgetting to remove it.
- Blocking `8080` in a cloud security group while local UFW or a router still forwards it.
- Accidentally publishing dataplane `9090/9091` or custom dataplane ports through Docker Compose, router NAT, or cloud security groups.
- Exposing SSH `22` to `0.0.0.0/0` without additional rate limiting, key policy, or a bastion host.
- Fixing IPv4 rules but leaving backend ports open through IPv6 `::/0`.
