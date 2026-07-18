# 公网服务器快速上线

[English](public-server-quickstart.en.md) | 简体中文

> [!WARNING]
> MnemoNAS 尚未发布可用版本。本文仅保留未来首次公开发布前的安全验证流程；不要把当前开发构建部署或暴露到公网。

本文档描述未来在公网 Linux 服务器上通过域名安全访问 MnemoNAS 的验证路径：

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

如果域名还没有解析成功，应先暂停证书申请步骤：

```bash
dig +short nas.example.com
```

## 1. 验证未来安装流程

以下步骤仅适用于未来首次公开发布产生 release 包之后；当前没有可用安装包，不应实际执行公网部署：

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

如果基础检查确认后端 control plane、dataplane 端口仍监听在非 loopback 地址，或 HTTPS health、`mnemonas-doctor --public-domain` 检查失败，脚本会停止并要求修复后重新运行，不会输出完成摘要。

```bash
sudo mnemonas-public-setup --proxy caddy nas.example.com admin@example.com
```

如需 Nginx：

```bash
sudo mnemonas-public-setup --proxy nginx nas.example.com admin@example.com
```

`mnemonas-public-setup` 会先将域名统一为小写，并移除单个 FQDN 尾点，再写入反向代理配置、证书命令和完成摘要。

Traefik 或 Cloudflare Tunnel 部署应从仓库模板开始：

- `deploy/public-access/traefik/`
- `deploy/public-access/cloudflare-tunnel/config.yml`

模板说明见 [公网访问模板](../deploy/public-access/README.md)，详细配置见 [反向代理配置](reverse-proxy-setup.md)。

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

从外部网络运行公网 smoke：

```bash
./scripts/public-go-live-smoke.sh nas.example.com
```

脚本的 TCP 探测需要 GNU `timeout` 兼容命令。Linux 通常提供 `timeout`；macOS 可使用 coreutils 提供的 `gtimeout`。脚本会按 `timeout`、`gtimeout` 顺序自动选择，也可通过 `TIMEOUT_BIN` 指定兼容命令。

该脚本会检查公网 HTTPS health、HTTP 到同一域名 HTTPS 的跳转，并确认 `8080/9090/9091` 从公网无法建立 TCP 连接且不返回任何 HTTP 状态码。无法运行仓库脚本时，可手动执行后续等价命令。

需要检查自定义后端端口时，可设置 `PUBLIC_SMOKE_BACKEND_TARGETS='18080:/health 19090:/'`。每个条目使用 `port:path`，其中 path 必须是不含 query、fragment、userinfo、反斜杠、编码斜杠、编码反斜杠、空路径段或 `.`/`..` 路径段的明确绝对路径。无效自定义目标或错误跳转的诊断只保留目标形状，不回显 query、fragment、userinfo 或控制字符路径内容。

直连后端端口应连接失败或超时。只要 TCP 可连接，即使没有 HTTP 状态码，也表示后端端口仍可从公网访问；如果返回任何 HTTP 状态码（包括 `401`、`403`、`404`），同样表示端口仍可从公网访问：

```bash
curl --connect-timeout 3 --max-time 10 http://nas.example.com:8080/health
curl --connect-timeout 3 --max-time 10 http://nas.example.com:9090/
curl --connect-timeout 3 --max-time 10 http://nas.example.com:9091/health
```

服务器本机上可以运行：

```bash
sudo mnemonas-doctor
sudo mnemonas-doctor --public-domain nas.example.com
ss -tlnp | grep -E '80|443|8080|9090|9091'
```

带 `--public-domain` 的检查会先将域名统一为小写，并移除单个 FQDN 尾点，然后验证：

公网检查需要公网完整域名，不接受 `localhost` 或 IP 地址；本机或 IP 字面量无法证明公网 DNS、证书 hostname 和外部网络访问路径。

- 公网 HTTPS health、同域 HTTP 到 HTTPS 跳转、证书 hostname 和证书剩余有效期；
- 公开部署认证配置、管理员账号冗余和初始密码文件清理状态；
- 分享链接基础 URL 形态和公开分享 API 响应缓存边界；
- 后端直连端口、dataplane 端口暴露情况，以及本机 UFW 是否仍宽泛放行后端 control plane 或 dataplane 端口。

DNS 检查需要服务器上有 `getent`；缺少 `getent`，或 `getent ahosts <domain>` 没有返回地址时，`mnemonas-doctor --public-domain` 会失败。证书检查需要服务器上有 `openssl`；缺少 `openssl` 时，`mnemonas-doctor --public-domain` 会失败。云安全组或上游防火墙仍需按清单单独复核。
公网 HTTP 必须返回到同一域名的 HTTPS 跳转；HTTP 不可达、非重定向响应或跳转到其他域名都会导致 `mnemonas-doctor --public-domain` 失败。

公开部署认证检查要求 `auth.enabled = true`、`security.allow_unsafe_no_auth = false`，且启用 WebDAV 时不能使用 `auth_type = "none"`。
如果保留全局 Basic Auth，显式配置的常见占位密码或少于 16 字符的密码会产生告警。
使用自动生成 Basic Auth 密码时，`secrets.json` 必须存在，且必须是私有普通文件；该文件不能是符号链接，路径组件也不能包含符号链接。

管理员冗余检查会读取 `auth.users_file`，未配置时读取 `$STORAGE_ROOT/.mnemonas/users.json`。
自定义 `auth.users_file` 时会检查该文件所在目录。
用户文件及其目录不能是符号链接，路径组件也不能包含符号链接。
用户文件必须是有效列表，并满足：

- 每个用户都有非空且唯一的 `id` 和 `username`；
- 每个用户都有有效的 `role` 和 `disabled` 字段；
- 启用中的管理员有 bcrypt 格式的 `password_hash`。

文件缺失、JSON 或结构校验失败、管理员密码哈希不可用，或没有启用中的管理员会失败。
只有一个可用的启用管理员会产生告警，两个及以上可用的启用管理员为通过。

初始密码文件检查使用用户文件同目录中的 `initial-password.txt`。
公开部署下，符号链接、路径组件包含符号链接或该路径是非普通文件都会失败。

启用分享功能时，`share.base_url` 应使用 HTTPS 默认端口，不能包含 userinfo、查询参数、片段、编码后的查询或片段标记、反斜杠、重复的路径斜杠或 `.`/`..` 路径段，且主机名必须有效。
使用 HTTP、非 443 端口、userinfo、查询参数、片段、编码后的查询或片段标记、反斜杠、重复路径斜杠、`.`/`..` 路径段或无效主机名会失败。
留空、使用其他域名，或路径已经以 `/s` 结尾时会给出人工复核提示，因为生成的分享链接可能包含重复的 `/s/s` 路由。

公开分享 API 响应检查使用保留探测 ID。
它只验证已到达 MnemoNAS 的缺失分享 JSON 响应是否包含 `Cache-Control: private, no-cache`、`Vary: Cookie`、`X-Content-Type-Options: nosniff` 和 `Referrer-Policy: no-referrer`，并且不返回 `Set-Cookie`。
它不会读取真实分享 ID、口令或 cookie 值。
启用公开分享时，公开分享 API 探测必须到达 MnemoNAS。若反向代理或访问控制层返回 `401`、`403`、重定向或其他无法确认到达 MnemoNAS 的响应，`mnemonas-doctor --public-domain` 会失败；只有探测返回 MnemoNAS 的缺失分享 JSON 响应后，才会继续检查缓存和安全响应头。

检查报告还会提示本机可检测到的续期路径：Nginx/certbot 部署应先执行 `sudo certbot renew --dry-run`，Caddy 部署应检查 `sudo journalctl -u caddy --since '24 hours ago'` 是否没有 ACME 错误。

公网部署时，`mnemonas-doctor --public-domain` 会检查 `auth.access_token_ttl` 和 `auth.refresh_token_ttl`。访问令牌长于 `1h` 或刷新令牌长于 `720h`（30 天）会产生告警；访问令牌短于 `30s`，或任一值为空、为零或为负时，配置校验会失败。

启用分享功能时，`mnemonas-doctor --public-domain` 还会检查 `share.default_expires_in` 和 `share.default_max_access`。
默认不过期、长于 `720h`（30 天），或默认下载次数为 `0` 会产生告警。
负值有效期或负值默认下载次数会失败。
家庭公网分享建议保持默认 `168h`（7 天）或其他不超过 30 天的明确有效期，并设置明确的默认下载次数，例如 `20`。该数值按下载票据计算逻辑下载会话，不按目录浏览或单个 Range 请求计数。

期望状态：

- Caddy/Nginx 监听 `0.0.0.0:80` 和 `0.0.0.0:443`；
- MnemoNAS Web/API/WebDAV 只监听 `127.0.0.1:8080`；
- dataplane `9090/9091` 或自定义端口只监听 `127.0.0.1`。
- 本机端口检查覆盖 IPv4 和 IPv6；推荐安装 `iproute2` 以提供 `ss`，没有 `ss` 时 `/proc/net/tcp` 和 `/proc/net/tcp6` 必须都可读。
- 运行 `mnemonas-doctor --public-domain` 的主机已安装 `curl`、`python3`、`getent` 和 `openssl`，用于校验公网 HTTP(S) 入口、duration 配置、DNS 解析、`users.json` 管理员冗余、自动生成的 WebDAV 凭据和 HTTPS 证书。

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

保留旧版全局 Basic Auth 模式时，WebDAV 凭据不是 Web UI 管理员密码。默认生成值可在管理员登录后的设置页查看；命令行排查时只输出 `webdav_password` 字段，避免把 JWT 签名密钥一起打印：

```bash
sudo python3 -c 'import json; print(json.load(open("/srv/mnemonas/secrets.json", encoding="utf-8")).get("webdav_password", ""))'
```

完整 `secrets.json` 还包含运行态签名密钥，不应复制到工单、聊天记录或日志中。该文件必须保持普通文件和私有权限，路径组件不能包含符号链接。

也可以在 `/etc/mnemonas/config.toml` 的 `[webdav]` 中设置自定义 Basic Auth 强密码，修改后先校验再重启：

```bash
sudo nasd --check-config --config /etc/mnemonas/config.toml
sudo systemctl restart mnemonas
```

## 5. 上线前清单

- [ ] 已从外部网络运行 `./scripts/public-go-live-smoke.sh nas.example.com`，或手动完成等价的 HTTPS health、HTTP 跳转和后端端口不可达检查。
- [ ] 管理员初始密码已修改，`initial-password.txt` 路径不存在，且没有保留符号链接、符号链接路径组件或非普通文件。
- [ ] 至少保留两个启用中的管理员账号，避免唯一管理员丢失密码后无法维护。
- [ ] Web UI “安全自检”没有 `block` 项；`warning` 项已逐条处理或确认。
- [ ] `https://nas.example.com/health` 正常返回。
- [ ] `http://nas.example.com/health` 跳转到同一域名的 HTTPS 地址。
- [ ] 公网 `8080/9090/9091` 或自定义后端端口不可访问。
- [ ] 运行 `mnemonas-doctor --public-domain` 的主机可使用 `ss`，或可同时读取 `/proc/net/tcp` 和 `/proc/net/tcp6`。
- [ ] 运行 `mnemonas-doctor --public-domain` 的主机已安装 `curl`、`python3`、`getent` 和 `openssl`。
- [ ] 本机 UFW 没有宽泛放行 `8080/9090/9091` 或自定义后端端口。
- [ ] `/etc/mnemonas/config.toml` 是普通私有文件，路径组件不包含符号链接，且 TOML 语法有效。
- [ ] `/etc/mnemonas/config.toml` 中 `server.host = "127.0.0.1"`。
- [ ] `/etc/mnemonas/config.toml` 中 `trusted_proxy_hops = 1`。
- [ ] `/etc/mnemonas/config.toml` 中 `security.allow_unsafe_no_auth = false`。
- [ ] `/etc/mnemonas/config.toml` 中 `auth.access_token_ttl <= 1h`，`auth.refresh_token_ttl <= 720h`。
- [ ] 如果 WebDAV 保留全局 Basic Auth 且使用自动生成密码，`<storage.root>/secrets.json` 存在。
  该文件不是符号链接，路径组件不包含符号链接，且是权限私有的普通文件。
- [ ] 如果启用公开分享，`share.base_url` 使用 HTTPS 默认端口、有效主机名，且不包含 userinfo、查询参数、片段、编码后的查询或片段标记、反斜杠、重复路径斜杠或 `.`/`..` 路径段；`share.default_expires_in` 不是空值或 `0`，且不超过 `720h`；`share.default_max_access` 大于 `0`；`mnemonas-doctor --public-domain` 的公开分享探测能到达 MnemoNAS，并且 JSON 响应边界检查通过（私有缓存、`Vary: Cookie`、无探测 cookie）。
- [ ] 云安全组只开放 `80/443`，SSH 只允许可信来源。
- [ ] 已配置外部备份，不把这台公网服务器当作唯一数据副本。

更多反向代理细节见 [反向代理配置](reverse-proxy-setup.md)，更多安全说明见 [安全加固指南](security.md)。
