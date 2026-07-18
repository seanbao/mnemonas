# Linux/systemd 部署指南

[English](linux-systemd-deployment.en.md) | 简体中文

> [!WARNING]
> MnemoNAS 尚未发布可用版本。本文仅用于维护者验证未来 systemd 发布路径；不要用当前开发构建承载真实数据或长期运行。

本文描述未来 Linux 服务器长期运行路径的验证方式。
目标是安装步骤少、开机自启、日志可查、数据目录固定，并在出问题时能快速诊断。

## 适用范围

- Ubuntu 22.04/24.04 LTS、Debian 或相近的 systemd Linux 发行版
- 单机文件服务、文档/媒体归档、局域网或反向代理后的 WebDAV
- 使用系统文件系统承载物理可靠性，MnemoNAS 负责 Web UI、WebDAV、版本、回收站、校验和 scrub

MnemoNAS 不自己实现 RAID。多盘可靠性建议交给 ZFS mirror、Btrfs RAID1 或 mdadm，再把挂载后的目录交给 MnemoNAS 使用。

## 推荐目录

| 路径 | 用途 |
| --- | --- |
| `/srv/mnemonas` | MnemoNAS 主数据目录 |
| `/etc/mnemonas/config.toml` | 服务配置 |
| `/usr/local/bin/nasd` | 控制面与 Web UI 服务 |
| `/usr/local/bin/dataplane` | 数据面服务 |
| `/usr/local/share/mnemonas/web` | Web UI 静态资源 |
| `/backup/mnemonas` | 本机或外接盘备份目标 |

## 存储准备

推荐的高可靠性方案是两块 SSD 做 ZFS mirror，另配独立磁盘或远端存储做定期备份：

以下命令会清空被选中的磁盘。先核对设备型号和序列号：

```bash
ls -l /dev/disk/by-id/
```

确认目标不是系统盘后再创建存储池：

```bash
sudo apt update
sudo apt install -y zfsutils-linux

sudo zpool create \
  -o ashift=12 \
  -o autotrim=on \
  -O compression=lz4 \
  -O atime=off \
  -O xattr=sa \
  -O acltype=posixacl \
  mnemonas mirror /dev/disk/by-id/<disk-a> /dev/disk/by-id/<disk-b>
sudo zfs create -o mountpoint=/srv/mnemonas -o recordsize=1M mnemonas/data
sudo mkdir -p /srv/mnemonas
```

如果暂时只有单盘，也可以先使用 ext4/XFS/Btrfs，但要明确它不能抵御硬盘损坏。至少准备一份独立备份。

如果同一台服务器还会运行 Docker、下载器、转码、模型缓存或其他服务，不要把这些数据放进 `/srv/mnemonas`。
建议单独准备 `/srv/fast-scratch` 一类可丢弃工作区，必要时把 Docker `data-root` 挪过去，避免系统根分区或未来 MnemoNAS 数据目录被缓存挤满。

## 验证未来安装流程

以下命令仅适用于未来首次公开发布产生 Linux release 包之后，当前没有可下载的可用归档：

```bash
tar -xzf mnemonas-<version>-linux-amd64.tar.gz
cd mnemonas-<version>-linux-amd64

sudo ./scripts/install-systemd.sh
```

默认安装行为：

- 创建 `mnemonas` 系统用户
- 创建 `/srv/mnemonas/files` 和 `/srv/mnemonas/.mnemonas`
- 安装 `mnemonas-dataplane.service` 和 `mnemonas.service`
- 监听 `0.0.0.0:8080`
- 自动启用并启动服务

自定义数据目录或端口：

```bash
sudo env STORAGE_ROOT=/srv/mnemonas SERVER_PORT=8080 ./scripts/install-systemd.sh
```

systemd 安装与卸载脚本要求 `BIN_DIR`、`SHARE_DIR`、`CONFIG_DIR`、`CONFIG_PATH`、`SYSTEMD_DIR`、`STORAGE_ROOT` 和 Web UI 目录使用绝对路径。
这些路径不能包含控制字符，路径组件不能包含符号链接。
`CONFIG_PATH` 必须位于 `CONFIG_DIR` 下。

除 Web UI 目录可位于 `SHARE_DIR` 内之外，二进制、共享资源、配置、systemd unit 和数据目录不能互相重叠。
安装脚本还会在创建或修改权限前检查 `STORAGE_ROOT/files` 与 `STORAGE_ROOT/.mnemonas/objects`。
这些托管子目录不能通过符号链接指向其他位置。

需要把数据放到单独磁盘时，先把真实文件系统挂载到目标目录，再运行安装脚本。
不要把 `STORAGE_ROOT` 指向符号链接。

安装脚本默认只修正 `/srv/mnemonas`、`files` 和 `.mnemonas` 这些顶层托管目录的所有者，不会在升级时递归改动已有数据。若因手动复制数据导致服务用户无权访问，可显式运行：

```bash
sudo env FIX_STORAGE_OWNERSHIP=1 ./scripts/install-systemd.sh
```

安装完成后脚本会输出可直接执行的下一步，包括 Web UI 地址、按当前 `auth.users_file` 推导出的初始密码 `sudo cat .../initial-password.txt` 命令、`mnemonas-doctor` 诊断命令和日志查看命令。

如果安装在重载、启用或启动 systemd 服务阶段失败，脚本会输出失败阶段以及对应的 `systemctl cat`、`systemctl status` 或 `journalctl` 检查命令。先保留配置和数据目录，按输出排查 unit 或服务日志后，可重新运行安装脚本。

如果还没有可用的 release 包，也可以在目标机器或另一台 Linux 机器上从源码构建后安装：

```bash
git clone https://github.com/seanbao/mnemonas.git
cd mnemonas

make deps
make build
sudo env RELEASE_DIR="$PWD" ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

源码构建需要 Go、Rust、Node.js 和 protobuf 编译器；版本要求见 [开发指南](development.md)。

## 首次登录

安装后运行诊断：

```bash
sudo mnemonas-doctor
```

默认首次登录密码：

```bash
sudo cat /srv/mnemonas/.mnemonas/initial-password.txt
```

在局域网、服务器本机或 SSH 隧道中打开浏览器访问：

```text
http://<server-ip>:8080
```

公网域名访问不要直接开放 `8080`，应按 [公网服务器快速上线](public-server-quickstart.md) 配置 HTTPS 反向代理。

登录后应立即修改管理员密码；初始密码文件如果仍存在，`mnemonas-doctor` 会提示。

## 管理员密码恢复

现有且已启用的管理员忘记密码时，应在服务器本地停服后运行恢复命令。以下示例恢复 `admin` 账号：

```bash
sudo systemctl stop mnemonas
sudo -u mnemonas /usr/local/bin/nasd \
  --config /etc/mnemonas/config.toml \
  --recover-admin admin
sudo cat /srv/mnemonas/.mnemonas/initial-password.txt
sudo systemctl start mnemonas
```

恢复命令不接受指定密码，也不会把生成的临时密码写入终端。命令只输出管理员用户名、凭据文件路径和非敏感状态信息；随机临时密码保存在权限为 `0600` 的 `initial-password.txt` 中。若自定义了 `auth.users_file`，凭据文件位于该 users 文件同目录，应以命令输出的路径为准。

配置必须保持 `auth.enabled = true`。恢复对象必须是现有、已启用且角色为 `admin` 的账号。恢复会撤销该账号的全部现有会话；使用临时密码登录后必须立即修改密码，成功改密后凭据文件会被删除。

`nasd` 服务和恢复命令都会独占认证状态目录中的 `auth-state.lock`。认证状态路径必须由 root 或 `mnemonas` 服务账号持有，该目录不能授予 group/other 写权限，祖先目录也不能被其他本地账号替换。未停止服务、目录权限不安全或已有另一个恢复命令运行时，恢复会被拒绝。恢复中断后应保持服务停止，并用相同管理员用户名重新运行命令；待提交、冲突或损坏的恢复标记会阻止正常启动，凭据文件中的恢复标记用于安全续跑。MnemoNAS 不提供匿名或远程 HTTP 管理员恢复端点。

## 日常管理

```bash
systemctl status mnemonas --no-pager
systemctl status mnemonas-dataplane --no-pager

journalctl -u mnemonas -f
journalctl -u mnemonas-dataplane -f

sudo systemctl restart mnemonas
sudo systemctl restart mnemonas-dataplane
sudo mnemonas-doctor
```

`mnemonas-doctor` 会检查服务状态、Web UI、配置文件、运行态敏感文件、目录权限、存储挂载类型、剩余磁盘空间和备份目录位置。
`config.toml` 不是普通文件时会失败。
`config.toml` 必须能按 TOML 语法解析；即使 `nasd --check-config` 被旧版本或包装脚本误判通过，doctor 也会独立报告语法错误。
如果配置文件是符号链接、路径组件包含符号链接或权限过宽，会提示风险。

启用认证时，如果 `users.json` 缺失，诊断会提示风险。
如果 `users.json`、`secrets.json` 及相关目录是符号链接、路径组件包含符号链接、非普通文件或权限过宽，诊断也会提示风险。

`BACKUP_ROOT` 存在时不能等于或位于 `storage.root` 内部。
如果它是符号链接、不是目录、与 `storage.root` 使用同一个 filesystem source，或服务用户/当前诊断环境无法写入，诊断会提示风险。
备份目标应指向独立磁盘、独立数据集或远端挂载路径。

Web UI 的“设备状态”和“空间与存储”页也会显示底层文件系统类型、挂载点、设备/数据集来源、脱敏挂载选项、ZFS/Btrfs 原生校验提示，以及空间提醒运行态。
管理员可在“设备状态”页下载诊断包，也可在“空间与存储”页复制存储承载摘要用于排障记录。
默认低于 10 GiB 可用空间时给出警告，可用 `MIN_FREE_BYTES=<bytes> sudo mnemonas-doctor` 调整阈值。

如果系统安装了 UFW，`mnemonas-doctor` 也会检查防火墙是否启用，并提示不要放行 dataplane 的 `9090/9091` 端口。

修改配置后检查并重启：

```bash
sudo nasd --check-config --config /etc/mnemonas/config.toml
sudo systemctl restart mnemonas-dataplane
sudo systemctl restart mnemonas
```

`--check-config` 会同时输出安全警告。配置语法合法但风险较高时，例如关闭登录认证后仍监听非 loopback 地址、WebDAV 选择无认证、或 dataplane gRPC 监听到外部网络，命令仍会通过但会打印 `warning:`。生产或长期部署不要忽略这些警告。

`[dataplane.cdc]` 和 `dataplane.grpc_address` 会在 `mnemonas-dataplane` 每次启动时从配置文件读取；启动 helper 在 Python TOML parser 可用时会先拒绝语法无效的 `config.toml`，避免从残缺配置中读取 dataplane 参数。修改这些项后需要重启 `mnemonas-dataplane`，再重启 `mnemonas`。

## 网络建议

长期部署的默认原则是先限制到可信网络。

- 管理入口优先走局域网或 Tailscale/Headscale 这类私有网络
- 不建议把 SSH 直接暴露到公网
- 如果需要分享给外部用户，优先只暴露 HTTPS 反向代理后的 Web 入口
- 使用 Caddy/Nginx/Traefik 时参考 [反向代理配置](reverse-proxy-setup.md)，并正确配置 `server.trusted_proxy_hops`

如果目标是通过公网域名访问，优先按 [公网服务器快速上线](public-server-quickstart.md) 配置。该路径会把 MnemoNAS 后端收紧到 `127.0.0.1:8080`，公网只开放 Caddy/Nginx 的 `80/443`。

推荐把以下访问路径分开规划：

| 访问路径 | 用途 | 建议 |
| --- | --- | --- |
| 局域网 / Tailscale / Headscale | 管理、SSH、授权用户访问 | 只允许可信网段访问 `8080`；SSH 仅走私有网络 |
| HTTPS 反向代理 / FRP / 隧道 | 给外部用户打开分享链接 | 公网只开放 `80/443`，代理到 MnemoNAS 的 Web 入口 |
| dataplane `9090/9091` 或自定义端口 | `nasd` 与 Rust 数据面内部通信 | 只绑定 loopback，不做端口映射，不走公网代理 |

如果使用 UFW，先按自己的 LAN/Tailnet 网段替换示例中的地址，再应用规则：

```bash
sudo ufw allow from 192.168.0.0/16 to any port 22 proto tcp comment "SSH LAN"
sudo ufw allow from 100.64.0.0/10 to any port 22 proto tcp comment "SSH Tailnet"
sudo ufw allow from 192.168.0.0/16 to any port 8080 proto tcp comment "MnemoNAS LAN"
sudo ufw allow from 100.64.0.0/10 to any port 8080 proto tcp comment "MnemoNAS Tailnet"
sudo ufw deny 9090/tcp comment "MnemoNAS dataplane gRPC"
sudo ufw deny 9091/tcp comment "MnemoNAS dataplane HTTP"

# 如果使用公网 HTTPS 入口，也放行反向代理端口。
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp

sudo ufw enable
sudo ufw status numbered
```

如果修改过 `SERVER_PORT`、`DATAPLANE_GRPC_ADDR` 或 `DATAPLANE_HTTP_ADDR`，应将示例中的端口替换为实际端口。

如果反向代理和 MnemoNAS 在同一台机器，最稳妥的做法是把 `[server].host` 改为 `127.0.0.1`，让公网只通过代理访问。需要局域网直连时，再用防火墙把 `8080` 限制到可信网段。

WebDAV 地址：

```text
http://<server-ip>:8080/dav
```

WebDAV 凭据取决于 `[webdav].auth_type`。
`users` 模式使用 MnemoNAS 用户名和密码。
默认 `basic` 模式使用独立 WebDAV 用户名和密码。

安装后可在设置页 `WebDAV` 标签页查看和复制当前 WebDAV 地址、Basic 用户名和可读取的自动生成密码。
自定义 Basic 密码不会回显，应以配置文件或密码管理器记录为准。
自动生成的 Basic Auth 密码也可在服务器端 `<storage.root>/secrets.json` 中查看。

## 备份策略

ZFS mirror、Btrfs RAID1 或 mdadm 只能降低单盘故障风险，不等于备份。建议至少保留一份独立备份。

最小可行方案是在低峰期短暂停服务后同步，避免复制到一半时元数据还在变化：

```bash
sudo mkdir -p /backup/mnemonas
sudo systemctl stop mnemonas
sudo systemctl stop mnemonas-dataplane
sudo rsync -aHAX --delete /srv/mnemonas/ /backup/mnemonas/
sudo systemctl start mnemonas-dataplane
sudo systemctl start mnemonas
```

如果底层是 ZFS/Btrfs，更可靠的做法是先创建文件系统快照，再从快照目录备份。也可以使用 restic 或 borg，把 `/srv/mnemonas` 定期备份到外接盘、另一台机器或对象存储。备份完成后需要定期做恢复演练，确认文件能取回。

更多策略见 [备份指南](backup-guide.md)。

## 升级

下载新的 release 包后重新运行安装脚本即可覆盖二进制和 Web UI，已有配置和数据会保留：

```bash
tar -xzf mnemonas-<version>-linux-amd64.tar.gz
cd mnemonas-<version>-linux-amd64

sudo ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

升级前建议先完成一次备份，尤其是跨大版本升级。还应保留上一版本的 release 解压目录，便于升级后发现启动失败、核心工作流异常或 `mnemonas-doctor` 失败时回退到上一版本：

```bash
cd mnemonas-<previous-version>-linux-amd64
sudo ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

回退会覆盖二进制和 Web UI 静态资源，并继续使用现有 `/etc/mnemonas/config.toml` 与 `/srv/mnemonas` 数据目录。若新版本已经执行过不可逆的数据迁移，应先按对应 release note 或备份恢复流程处理；不要直接用旧版本读取已经迁移后的数据。

## 卸载

如果只是停止试用或重新安装，默认卸载会移除 systemd 服务、二进制和 Web UI 文件，但保留 `/etc/mnemonas` 配置和 `/srv/mnemonas` 数据：

```bash
sudo mnemonas-uninstall-systemd
```

确认已经完成备份、并且确实要删除配置和数据时，才使用显式确认：

```bash
sudo env REMOVE_CONFIG=1 REMOVE_DATA=1 CONFIRM_REMOVE_DATA=/srv/mnemonas mnemonas-uninstall-systemd
```

卸载脚本同样拒绝经过符号链接组件的二进制、共享资源、配置、systemd unit 和数据路径。删除配置或数据时，目标目录不能是符号链接或经过符号链接组件；删除数据还要求 `CONFIRM_REMOVE_DATA` 必须与 `STORAGE_ROOT` 完全一致，避免误删真实挂载点或被替换的目录树。

服务账号默认保留，便于之后重新安装复用同一 UID/GID；如果确实要删除账号，可额外设置 `REMOVE_SERVICE_USER=1`。

## 故障排查

先运行：

```bash
sudo mnemonas-doctor
```

常见问题：

| 现象 | 检查项 |
| --- | --- |
| Web 打不开 | `systemctl status mnemonas`、防火墙、端口是否被占用 |
| 管理员忘记密码 | 停止 `mnemonas` 后运行 `nasd --config /etc/mnemonas/config.toml --recover-admin <管理员用户名>`，再读取命令报告的 `initial-password.txt` |
| 登录后写入失败 | `/srv/mnemonas` 和 `/etc/mnemonas` 是否归 `mnemonas` 用户所有 |
| WebDAV 连不上 | 地址是否为 `/dav`，客户端是否按当前 `[webdav].auth_type` 使用对应凭据 |
| 上传大文件失败 | 磁盘空间、反向代理上传限制、`journalctl -u mnemonas` |
| scrub 报错 | 先停止写入，保留日志，检查底层文件系统和备份可用性 |

提交 issue 时，应附上：

```bash
sudo mnemonas-doctor
systemctl status mnemonas --no-pager
journalctl -u mnemonas --since "1 hour ago" --no-pager
```
