# 公网服务器快速上线

[English](public-server-quickstart.en.md) | 简体中文

本文档面向“已有一台公网 Linux 服务器，希望先本地部署 MnemoNAS，再通过域名安全访问”的场景。推荐路径是：

```text
公网 80/443 -> Caddy/Nginx -> 127.0.0.1:8080 -> MnemoNAS
```

不要把 MnemoNAS 的 `8080` 或 dataplane 的 `9090/9091` 直接暴露到公网。

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
- 允许本机 `80/443`，限制直接访问 `8080/9090/9091`；
- 运行基础公网入口检查。

```bash
sudo mnemonas-public-setup --proxy caddy nas.example.com admin@example.com
```

如需 Nginx：

```bash
sudo mnemonas-public-setup --proxy nginx nas.example.com admin@example.com
```

如需 Traefik 或 Cloudflare Tunnel，请从 `deploy/public-access/traefik/` 或 `deploy/public-access/cloudflare-tunnel/config.yml` 模板开始，并参考 [反向代理配置](reverse-proxy-setup.md)。

如果已经登录 Web UI，也可以打开 `系统设置 -> 常规 -> 公网访问向导`：

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

直连后端端口应失败或超时：

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

带 `--public-domain` 的检查会验证公网 HTTPS health、HTTP 到 HTTPS 跳转、证书 hostname、证书剩余有效期、后端直连端口和 dataplane 端口暴露情况，并提示云安全组人工复核项；证书检查需要服务器上有 `openssl`。

期望状态：

- Caddy/Nginx 监听 `0.0.0.0:80` 和 `0.0.0.0:443`；
- MnemoNAS Web/API/WebDAV 只监听 `127.0.0.1:8080`；
- dataplane `9090/9091` 只监听 `127.0.0.1`。

## 4. WebDAV 地址

公网 WebDAV 使用 HTTPS：

```text
https://nas.example.com/dav
```

WebDAV 凭据不是 Web UI 管理员密码。默认凭据保存在：

```bash
sudo cat /srv/mnemonas/secrets.json
```

也可以在 `/etc/mnemonas/config.toml` 的 `[webdav]` 中设置自定义强密码，修改后重启：

```bash
sudo nasd --check-config --config /etc/mnemonas/config.toml
sudo systemctl restart mnemonas
```

## 5. 上线前清单

- [ ] 管理员初始密码已修改，`initial-password.txt` 已删除或不再存在。
- [ ] `https://nas.example.com/health` 正常返回。
- [ ] 公网 `8080/9090/9091` 不可访问。
- [ ] `/etc/mnemonas/config.toml` 中 `server.host = "127.0.0.1"`。
- [ ] `/etc/mnemonas/config.toml` 中 `trusted_proxy_hops = 1`。
- [ ] 云安全组只开放 `80/443`，SSH 只允许可信来源。
- [ ] 已配置外部备份，不把这台公网服务器当作唯一数据副本。

更多反向代理细节见 [反向代理配置](reverse-proxy-setup.md)，更多安全说明见 [安全指南](security.md)。
