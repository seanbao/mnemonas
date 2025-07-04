# 公网访问模板

[English](README.en.md) | 简体中文

这些模板提供未被 `mnemonas-public-setup` 完全自动化覆盖的公网 HTTPS 访问配置起点。

- `traefik/`: Traefik file-provider 模板，适用于 MnemoNAS 与反向代理运行在同一台 Linux 主机的部署。
- `cloudflare-tunnel/`: Cloudflare Tunnel ingress 模板，适用于没有公网直连 IP 的部署。

模板中的 `nas.example.com` 应替换为规范化后的公开域名。域名应保持小写、无单个 FQDN 尾点，并且不包含协议、路径、查询参数、用户信息或端口。

复制模板后，MnemoNAS 运行配置仍需与公网入口同步：

```toml
[server]
host = "127.0.0.1"
trusted_proxy_hops = 1

[share]
base_url = "https://nas.example.com"
```

`host` 应收紧到本机监听地址，`trusted_proxy_hops = 1` 用于让 MnemoNAS 信任同机 Traefik 或 cloudflared 转发的 `X-Forwarded-*` 头，从而正确识别 HTTPS、客户端地址、登录限流和下载 cookie。反向代理不是本机 loopback 来源时，还需要在 `[server].trusted_proxy_cidrs` 中列出代理 IP 或 CIDR。启用分享功能时，`share.base_url` 应设置为公开 HTTPS 域名。

公网部署完成后仍应运行：

```bash
sudo mnemonas-doctor --public-domain nas.example.com
```

云防火墙或安全组只开放 `80/443` 到公网。MnemoNAS 后端 `8080`、dataplane `9090/9091` 以及其他自定义后端或 dataplane 端口不得暴露到公网。
