# 公网服务器快速上线

[English](public-server-quickstart.en.md) | 简体中文

本文档面向“已有一台公网 Linux 服务器，希望先本地部署 MnemoNAS，再通过域名安全访问”的场景。推荐路径是：

```text
公网 80/443 -> Caddy/Nginx -> 127.0.0.1:8080 -> MnemoNAS
```

不要把 MnemoNAS 的 `8080` 或 dataplane 的 `9090/9091` 直接暴露到公网；如果改过端口，同样不要暴露对应的自定义后端端口。

## 前置条件

- Ubuntu 22.04/24.04 或 Debian 系 Linux 服务器
- 一个域名，例如 `nas.example.com`
- 域名 A/AAAA 记录已解析到服务器公网 IP
- 云安全组或防火墙允许 TCP `80/443`
- SSH 只允许可信 IP、VPN、Tailscale/Headscale 或其他私有网络访问

如果域名还没有解析成功，先不要运行证书申请步骤：

```bash
dig +short nas.example.com
```

## 1. 安装 MnemoNAS

从 release 包中安装 systemd 服务：

```bash
tar -xzf mnemonas-<version>-linux-amd64.tar.gz
cd mnemonas-<version>-linux-amd64

sudo ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

首次登录密码：

```bash
sudo cat /srv/mnemonas/.mnemonas/initial-password.txt
```

先通过服务器本机、SSH 隧道或临时可信网络访问。推荐用 SSH 隧道，不需要临时开放 `8080`：

```bash
ssh -L 18080:127.0.0.1:8080 <user>@<server-ip>
```

然后在本机浏览器打开：

```text
http://localhost:18080
```

登录后立即修改管理员密码。

## 2. 配置公网 HTTPS 入口

推荐使用 Caddy，脚本会自动：

- 安装并配置 Caddy 或 Nginx；
- 将 MnemoNAS 的 `[server].host` 收紧到 `127.0.0.1`；
- 设置 `trusted_proxy_hops = 1`；
- 重启 `mnemonas.service`；
- 允许本机 `80/443`，移除 `8080/9090/9091` 或自定义后端端口上的宽泛 UFW 放行规则，并限制直接访问；
- 运行基础公网入口检查。

```bash
sudo mnemonas-public-setup --proxy caddy nas.example.com admin@example.com
```

如需 Nginx：

```bash
sudo mnemonas-public-setup --proxy nginx nas.example.com admin@example.com
```

`mnemonas-public-setup` 会先将域名统一为小写，并移除单个 FQDN 尾点，再写入反向代理配置、证书命令和完成摘要。

如需 Traefik 或 Cloudflare Tunnel，请从 `deploy/public-access/traefik/` 或 `deploy/public-access/cloudflare-tunnel/config.yml` 模板开始；模板说明见 [公网访问模板](../deploy/public-access/README.md)，详细配置见 [反向代理配置](reverse-proxy-setup.md)。

如果已经登录 Web UI，也可以打开 `设置 -> 常规 -> 公网访问向导`：

- 填写公网域名；
- 选择 Caddy 或 Nginx；
- 点击“应用推荐到表单”，再保存设置；
- 按向导显示的 `mnemonas-public-setup` 和 `mnemonas-doctor --public-domain` 命令在服务器上完成代理配置与复核。

Web UI 向导会把 MnemoNAS 调整为适合反向代理的表单配置，但证书签发、防火墙和反向代理安装仍需要在服务器上执行脚本。

脚本不能修改云厂商安全组。运行后仍需按 [公网云防火墙复核清单](cloud-firewall-checklist.md) 在云控制台确认只开放：

| 端口 | 建议 |
| --- | --- |
| `80/tcp` | 允许公网，用于 HTTP 到 HTTPS 跳转和证书签发 |
| `443/tcp` | 允许公网，用于 Web、API、WebDAV |
| `22/tcp` | 只允许可信 IP 或私有网络 |
| `8080/tcp` | 不开放公网 |
| `9090/tcp` | 不开放公网 |
| `9091/tcp` | 不开放公网 |

## 3. 验证

公网 HTTPS 应可访问：

```bash
curl -I https://nas.example.com/health
```

直连后端端口应连接失败或超时；如果返回任何 HTTP 状态码（包括 `401`、`403`、`404`），都表示端口仍可从公网访问：

```bash
curl --connect-timeout 3 http://nas.example.com:8080/health
curl --connect-timeout 3 http://nas.example.com:9090/
curl --connect-timeout 3 http://nas.example.com:9091/health
```

服务器本机上可以运行：

```bash
sudo mnemonas-doctor
sudo mnemonas-doctor --public-domain nas.example.com
ss -tlnp | grep -E '80|443|8080|9090|9091'
```

带 `--public-domain` 的检查会先将域名统一为小写，并移除单个 FQDN 尾点，再验证公网 HTTPS health、HTTP 是否跳转到同一域名的 HTTPS、证书 hostname、证书剩余有效期、公开部署认证配置、管理员账号冗余、分享链接基础 URL、后端直连端口和 dataplane 端口暴露情况，并提示云安全组人工复核项；证书检查需要服务器上有 `openssl`。公开部署认证检查要求 `auth.enabled = true`、`security.allow_unsafe_no_auth = false`，且启用 WebDAV 时不能使用 `auth_type = "none"`。管理员冗余检查会读取 `auth.users_file`，未配置时读取 `$STORAGE_ROOT/.mnemonas/users.json`；文件缺失、解析失败或没有启用中的管理员会失败，只有一个启用中的管理员会产生告警，两个及以上启用中的管理员为通过。启用分享功能时，`share.base_url` 应使用 HTTPS 默认端口，不能包含 userinfo、查询参数或片段，且主机名必须有效；使用 HTTP、非 443 端口、userinfo、查询参数、片段或无效主机名会失败，留空或使用其他域名会给出人工复核提示。它也会提示本机可检测到的续期路径：Nginx/certbot 部署应先执行 `sudo certbot renew --dry-run`，Caddy 部署应检查 `sudo journalctl -u caddy --since '24 hours ago'` 是否没有 ACME 错误。

期望状态：

- Caddy/Nginx 监听 `0.0.0.0:80` 和 `0.0.0.0:443`；
- MnemoNAS Web/API/WebDAV 只监听 `127.0.0.1:8080`；
- dataplane `9090/9091` 或自定义端口只监听 `127.0.0.1`。

## 4. WebDAV 地址

公网 WebDAV 使用 HTTPS：

```text
https://nas.example.com/dav
```

公网部署推荐使用 MnemoNAS 用户认证，让 WebDAV 客户端使用 Web UI 用户账号，并继承 `home_dir`、目录授权和容量配额边界：

```toml
[webdav]
auth_type = "users"
```

保留旧版全局 Basic Auth 模式时，WebDAV 凭据不是 Web UI 管理员密码。默认生成的凭据保存在：

```bash
sudo cat /srv/mnemonas/secrets.json
```

也可以在 `/etc/mnemonas/config.toml` 的 `[webdav]` 中设置自定义 Basic Auth 强密码，修改后先校验再重启：

```bash
sudo nasd --check-config --config /etc/mnemonas/config.toml
sudo systemctl restart mnemonas
```

## 5. 上线前清单

- [ ] 管理员初始密码已修改，`initial-password.txt` 已删除或不再存在。
- [ ] 至少保留两个启用中的管理员账号，避免唯一管理员丢失密码后无法维护。
- [ ] Web UI “安全自检”没有 `block` 项；`warning` 项已逐条处理或确认。
- [ ] `https://nas.example.com/health` 正常返回。
- [ ] 公网 `8080/9090/9091` 或自定义后端端口不可访问。
- [ ] `/etc/mnemonas/config.toml` 中 `server.host = "127.0.0.1"`。
- [ ] `/etc/mnemonas/config.toml` 中 `trusted_proxy_hops = 1`。
- [ ] `/etc/mnemonas/config.toml` 中 `security.allow_unsafe_no_auth = false`。
- [ ] 云安全组只开放 `80/443`，SSH 只允许可信来源。
- [ ] 已配置外部备份，不把这台公网服务器当作唯一数据副本。

更多反向代理细节见 [反向代理配置](reverse-proxy-setup.md)，更多安全说明见 [安全指南](security.md)。
