# 公网访问模板

[English](README.en.md) | 简体中文

> [!WARNING]
> MnemoNAS 尚未发布可用版本。当前开发构建不得暴露到公网；这些模板仅用于未来首次公开发布前的配置验证。

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

`host` 应收紧到本机监听地址，`trusted_proxy_hops = 1` 用于让 MnemoNAS 信任同机 Traefik 或 cloudflared 转发的 `X-Forwarded-*` 头，从而正确识别 HTTPS、客户端地址、登录限流和下载 cookie。反向代理不是本机 loopback 来源时，还需要在 `[server].trusted_proxy_cidrs` 中列出代理 IP 或 CIDR。

启用分享功能时，`share.base_url` 应设置为公开 HTTPS 域名，或设置为反向代理应用基础路径，例如 `https://nas.example.com/mnemonas`。该值不应包含 `/s` 分享路由；否则生成的分享链接可能变成重复的 `/s/s` 路由。

公网部署还要求 `share.base_url` 使用 HTTPS 默认端口，且不包含 userinfo、查询参数、片段、反斜杠、重复路径斜杠或 `.`/`..` 路径段。

`mnemonas-doctor --public-domain` 会将 HTTP、非 443 端口、userinfo、查询参数、片段、反斜杠、重复路径斜杠、`.`/`..` 路径段和无效主机名判为失败；空值、域名不一致或路径已经以 `/s` 结尾会产生人工复核提示。

公网部署完成后仍应运行：

```bash
sudo mnemonas-doctor --public-domain nas.example.com
```

公网诊断依赖本机监听端口检查。推荐安装 `iproute2` 以提供 `ss`；没有 `ss` 时，`/proc/net/tcp` 和 `/proc/net/tcp6` 必须都可读，确保 `mnemonas-doctor --public-domain` 同时覆盖 IPv4 和 IPv6 监听。运行诊断的主机还必须安装 `curl`、`python3`、`getent` 和 `openssl`，用于校验公网 HTTP(S) 入口、duration 配置、DNS 解析、管理员冗余、自动生成的 WebDAV 凭据和 HTTPS 证书。

云防火墙或安全组只开放 `80/443` 到公网。MnemoNAS 后端 `8080`、dataplane `9090/9091` 以及其他自定义后端或 dataplane 端口不得暴露到公网。具体复核项见 [公网云防火墙复核清单](../../docs/cloud-firewall-checklist.md)。
