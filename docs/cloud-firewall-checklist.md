# 公网云防火墙复核清单

[English](cloud-firewall-checklist.en.md) | 简体中文

本清单用于把 MnemoNAS 暴露到公网前复核云厂商安全组、网络安全组、VPC 防火墙或实例防火墙规则。不同云控制台名称不同，但最终都应满足同一组入站规则。

## 最小公网规则

| 端口 | 协议 | 来源 | 用途 | 公网开放 |
| --- | --- | --- | --- | --- |
| `80` | TCP | `0.0.0.0/0` 和 `::/0` | HTTP 跳转 HTTPS、ACME HTTP-01 证书签发 | 可以 |
| `443` | TCP | `0.0.0.0/0` 和 `::/0` | Web、API、WebDAV HTTPS 入口 | 可以 |
| `22` | TCP | 管理员固定 IP、VPN 或堡垒机 | SSH 运维 | 不建议全网开放 |
| `8080` | TCP | 无公网来源 | MnemoNAS 直连 Web/API/WebDAV 后端 | 不开放 |
| `9090` | TCP | 无公网来源 | dataplane gRPC 内部端口 | 不开放 |
| `9091` | TCP | 无公网来源 | dataplane HTTP 内部端口 | 不开放 |

如果修改了 MnemoNAS 直连端口或 dataplane 端口，自定义端口也应按上表的“不开放”处理。使用 Cloudflare Tunnel、Tailscale、Headscale 或其他隧道/VPN，并且不需要公网直接入站时，可以不开放 `80/443`；但仍不要开放 `8080/9090/9091` 或自定义后端端口。

## 云厂商对照

| 云厂商或平台 | 常见规则位置 | 需要确认 |
| --- | --- | --- |
| AWS EC2 | Security Group inbound rules、Network ACL | 实例绑定的所有 Security Group 都没有放行 `8080/9090/9091` 或自定义后端端口；`22` 仅限可信来源 |
| Azure VM | Network Security Group inbound rules | NIC 和子网级 NSG 都要检查；高优先级规则不能放行后端端口 |
| Google Cloud VM | VPC firewall rules、instance network tags | 匹配实例 tag/service account 的规则不能放行后端端口 |
| Oracle Cloud | Security Lists、Network Security Groups | 子网 Security List 和实例 NSG 都要检查 |
| 腾讯云/阿里云等 | 安全组入站规则 | 实例绑定的每个安全组都要检查；不要保留临时调试端口 |
| 自建机房或家宽 | 路由器端口转发、防火墙/NAT | 只转发 `80/443` 到反向代理主机；不要转发后端和 dataplane 端口 |

## 服务器侧复核

在服务器上运行：

```bash
sudo mnemonas-doctor --public-domain nas.example.com
```

这个命令会检查 HTTPS health、HTTP 是否跳转到同一域名的 HTTPS、证书 hostname、证书 30 天有效期、后端直连端口、dataplane 端口和本机监听范围。它不能直接读取云控制台规则，因此云安全组仍需人工确认。

从外部网络执行：

```bash
./scripts/public-go-live-smoke.sh nas.example.com
```

该脚本会把公网 HTTPS health、同域 HTTP 到 HTTPS 跳转，以及 `8080/9090/9091` 后端端口不可达检查合并为一次 smoke。无法运行仓库脚本时，可从外部网络手动执行：

需要检查自定义后端端口时，可设置 `PUBLIC_SMOKE_BACKEND_TARGETS='18080:/health 19090:/'`。每个条目使用 `port:path`，其中 path 必须是不含 query、fragment、userinfo、反斜杠、编码斜杠、编码反斜杠、空路径段或 `.`/`..` 路径段的明确绝对路径。

```bash
curl -I https://nas.example.com/health
curl --connect-timeout 3 --max-time 10 http://nas.example.com:8080/health
curl --connect-timeout 3 --max-time 10 http://nas.example.com:9090/
curl --connect-timeout 3 --max-time 10 http://nas.example.com:9091/health
```

预期结果：

- `https://nas.example.com/health` 可访问。
- `http://nas.example.com:8080/health` 连接失败或超时，不应返回任何 HTTP 状态码。
- `http://nas.example.com:9090/` 连接失败或超时，不应返回任何 HTTP 状态码。
- `http://nas.example.com:9091/health` 连接失败或超时，不应返回任何 HTTP 状态码。
- 如果使用自定义后端端口，对应端口也应连接失败或超时，不应返回任何 HTTP 状态码。

## 常见错误

- 开放了 `8080` 或自定义后端端口用来“临时测试”，上线后忘记删除。
- 云安全组限制了 `8080`，但本机 UFW 或路由器仍转发了该端口。
- dataplane `9090/9091` 或自定义 dataplane 端口在 Docker Compose、路由器或云安全组里被误映射到公网。
- SSH `22` 对 `0.0.0.0/0` 开放，且没有额外限速、密钥策略或堡垒机。
- IPv4 规则正确，但 IPv6 `::/0` 中仍开放了后端端口。
