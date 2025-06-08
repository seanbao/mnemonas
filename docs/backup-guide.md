# 备份指南

[English](backup-guide.en.md) | 简体中文

数据安全是 MnemoNAS 的核心关注点。本文档介绍如何正确备份 MnemoNAS 数据，确保在各种故障场景下都能恢复。

## 📋 备份策略：3-2-1 原则

推荐遵循业界标准的 **3-2-1 备份原则**：

| 原则 | 说明 | MnemoNAS 实践 |
| ---- | ---- | -------------- |
| **3 份数据** | 生产数据 + 2 份副本 | 本地 + 外置盘 + 云存储 |
| **2 种介质** | 不同类型的存储介质 | SSD + HDD / 云 |
| **1 份离线** | 至少 1 份在异地或离线 | 云存储 / 物理备份盘 |

---

## 🗂️ 需要备份的内容

MnemoNAS 数据分为以下几个部分：

默认路径使用 `storage.root = ~/.mnemonas`。如已调整 `storage.root`，以下路径需替换为实际根目录。

systemd 部署通常使用 `/srv/mnemonas` 且目录归 `mnemonas` 服务用户所有；配置文件通常在 `/etc/mnemonas/config.toml`，不在数据目录内，需要单独纳入备份。直接读取备份时请用 `sudo`、root 定时任务，或把备份任务加入有权限的系统用户/组。

备份内部数据时需要一致性窗口。没有 ZFS/Btrfs/LVM 快照时，建议先停止 `mnemonas` 和 `mnemonas-dataplane`，备份完成后再启动；如果底层文件系统支持快照，优先从快照目录备份。

```text
~/.mnemonas/
├── files/                  # 用户文件（原生文件）
├── .mnemonas/              # 内部数据
│   ├── objects/            # CAS 对象
│   ├── index.db            # SQLite 元数据
│   ├── trash/              # 回收站内容
│   ├── thumbnails/         # 缩略图缓存
│   ├── maintenance/        # Scrub/GC 状态
│   └── activity/           # 活动日志
└── secrets.json            # 自动生成密钥（JWT/WebDAV）

~/.mnemonas/
└── config.toml             # 配置文件
```

| 目录 | 重要性 | 说明 |
| ---- | ------ | ---- |
| `files/` | ⭐⭐⭐ 极高 | 用户文件内容，丢失无法恢复 |
| `.mnemonas/` | ⭐⭐⭐ 极高 | 元数据与版本对象 |
| `secrets.json` | ⭐⭐ 中等 | JWT/WebDAV 密钥；首启 Web 管理员密码不长期保存在此文件 |
| `config.toml` | ⭐⭐ 中等 | 直接运行默认位于 `~/.mnemonas/`；systemd 安装通常位于 `/etc/mnemonas/config.toml` |

---

## 🔄 备份方法

### 备份前：先获得一致性来源

备份 MnemoNAS 时不要在服务运行中直接复制活跃数据目录，尤其不要把 `files/` 和 `.mnemonas/` 分开同步。推荐二选一：

1. 使用 ZFS/Btrfs/LVM 快照，从只读快照目录备份。
2. 没有快照能力时，先停止 `mnemonas` 和 `mnemonas-dataplane`，完成备份后再启动。

下面的示例都假设 `SOURCE_DIR` 是一致性来源：可以是快照挂载目录，也可以是停服务后的 `storage.root`。

systemd 部署的冷备份窗口：

```bash
sudo systemctl stop mnemonas mnemonas-dataplane
# 运行 rclone/restic/rsync 备份
sudo install -D -m 0600 /etc/mnemonas/config.toml /backup/mnemonas-config.toml
sudo systemctl start mnemonas-dataplane mnemonas
```

Docker 部署的冷备份窗口：

```bash
docker compose stop
# 运行 rclone/restic/rsync 备份
docker compose start
```

### 方法 0：内置备份任务

MnemoNAS 提供内置备份任务入口，可在维护页或 API 中执行、查看健康状态和触发恢复演练。任务类型支持：

- `local`：把来源目录复制成带 `manifest.json` 的本地快照，适合外置硬盘、已挂载 NAS 目录或文件系统快照挂载点。
- `restic`：调用系统中的 `restic` 可执行文件，把来源目录写入 restic 仓库。
- `rclone`：调用系统中的 `rclone` 可执行文件，把来源目录同步到 rclone remote。

限制：

- `local.destination` 必须是 `storage.root` 之外的绝对路径，避免递归把备份写回源目录。
- 默认来源是 `storage.root`；生产环境更推荐把 `source` 指向 ZFS/Btrfs/LVM 快照挂载目录。
- 源目录中遇到符号链接会中止任务，避免备份逃逸到源目录之外。
- `restic` 和 `rclone` 任务不会通过 shell 拼接命令；`command` 只能是可执行名或绝对路径，`extra_args` 会作为 argv 追加到备份命令，恢复命令不会复用备份专用参数。
- `password_file`、`config_file` 必须是 `source` 与 `storage.root` 之外的普通文件，避免把备份凭据重新纳入备份数据。
- `schedule_interval` 是服务内置的轻量调度器，适合固定间隔任务；复杂窗口、限速、网络唤醒和多阶段恢复仍建议配合 systemd timer 或外部编排。

本地快照示例：

```toml
[backup]

[[backup.jobs]]
id = "external-disk"
name = "外置硬盘备份"
type = "local"
source = ""                                # 留空表示 storage.root
destination = "/mnt/backup-drive/mnemonas" # 不能位于 storage.root 内
disabled = false
schedule_interval = "24h"                  # 每 24 小时自动执行；0 或留空表示仅手动执行
schedule_window_start = "02:00"            # 可选；自动任务只在服务器本地时间窗口内启动
schedule_window_end = "05:00"              # 支持跨午夜，例如 22:00 到 06:00
stale_after = "72h"                        # 超过 72 小时无成功备份时显示过期
restore_drill_stale_after = "720h"         # 超过 30 天无成功恢复演练时提醒
max_snapshots = 7                          # 最多保留 7 个快照
max_age = "720h"                           # 快照最长保留 30 天
include_config = true
verify_after_backup = true
exclude = [".mnemonas/thumbnails"]
```

restic 示例：

```toml
[[backup.jobs]]
id = "restic-remote"
name = "Restic 加密备份"
type = "restic"
source = "/mnt/snapshots/mnemonas-latest"
repository = "rest:http://backup.example:8000/mnemonas"
command = "restic"
password_file = "/etc/mnemonas/restic.pass"
schedule_interval = "24h"
schedule_window_start = "02:00"
schedule_window_end = "05:00"
stale_after = "72h"
restore_drill_stale_after = "720h"
retention_policy = "external: restic forget --keep-daily 7 --keep-weekly 4 --prune"
verify_after_backup = true
exclude = [".mnemonas/thumbnails"]
extra_args = ["--compression", "max"]
```

rclone 示例：

```toml
[[backup.jobs]]
id = "rclone-cloud"
name = "Rclone 云端同步"
type = "rclone"
source = "/mnt/snapshots/mnemonas-latest"
remote = "cloud:mnemonas/current"
command = "rclone"
config_file = "/etc/mnemonas/rclone.conf"
schedule_interval = "24h"
schedule_window_start = "02:00"
schedule_window_end = "05:00"
stale_after = "72h"
restore_drill_stale_after = "720h"
retention_policy = "external: cloud lifecycle keeps 30 daily versions"
verify_after_backup = true
exclude = [".mnemonas/thumbnails"]
extra_args = ["--fast-list"]
```

`schedule_window_start`/`schedule_window_end` 只限制自动调度，手动“立即备份”不受影响。窗口使用服务器本地时间的 `HH:MM`，可以跨午夜。`local` 保留策略在成功备份后执行，始终保留当前快照；`max_snapshots = 0` 和 `max_age = "0"` 表示不启用对应维度的自动清理。`restic` 和 `rclone` 的保留策略由外部工具管理，例如 `restic forget --prune`、systemd timer 或 rclone 目标端生命周期规则；配置 `retention_policy` 后，维护页会把该任务标记为“远端保留策略已确认”，否则显示需要确认。每次成功备份后会自动执行一次保留策略检测，也可以在维护页手动点击“检查保留”：`local` 会统计本地快照范围，`restic` 会执行 `restic snapshots --json --tag mnemonas --tag job:<id>`，`rclone` 会执行 `rclone lsjson <remote> --recursive --files-only`。检测结果会写入 `last_retention_check`，并在快照缺失、远端为空、未填写 `retention_policy` 或命令失败时提示警告。`restore_drill_stale_after` 控制定期恢复演练提醒；未配置时默认 30 天。维护页会显示任务健康状态、保留策略状态、恢复演练状态、下次自动运行时间、自动窗口、最近备份、最近恢复目标和最近清理的旧快照数量。恢复历史默认保留最近 20 条，失败恢复也会记录错误信息。

重启服务后可通过维护 API 执行：

```bash
# 查看任务
curl -b cookies.txt http://localhost:8080/api/v1/maintenance/backups

# 立即执行备份
curl -X POST -b cookies.txt http://localhost:8080/api/v1/maintenance/backups/external-disk/run

# 检查快照保留策略和远端可见内容
curl -X POST -b cookies.txt http://localhost:8080/api/v1/maintenance/backups/external-disk/retention-check

# 执行恢复演练或远端校验；local 默认临时恢复目录会在校验后删除
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore-drill \
  -H 'Content-Type: application/json' \
  -d '{"keep_artifact":false}'
```

`local` 恢复演练会把最近一次快照复制到临时目录，然后按 manifest 校验每个文件的大小和 SHA-256。`keep_artifact = true` 会保留临时恢复目录，便于人工抽查。`restic` 恢复演练当前执行 `restic check`；`rclone` 恢复演练当前执行 `rclone check --one-way`，用于验证仓库或远端一致性。

需要真正取回数据时，`local`、`restic` 和 `rclone` 任务可以恢复到指定的独立目录：

```bash
# 先预览：校验目标目录安全性，并确认预计恢复的文件数、字节数和样例路径
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore-preview \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas","include_config":true}'

curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas","include_config":true}'

# 恢复后检查：只读统计目标目录，并识别是否像完整 storage.root
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore-verify \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas"}'

# rclone 任务示例：从 remote 复制到独立目录，并在安装前执行 rclone check
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/rclone-cloud/restore \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas-rclone","include_config":false}'

# restic 任务示例：恢复 latest + job tag，并把来源目录内容安装到目标根目录
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/restic-cloud/restore \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas-restic","include_config":false}'
```

`restore-preview` 不会写入目标目录，也不会写入恢复历史。它会复用恢复目标安全校验，并返回预计文件数、字节数、最多 10 个样例路径、`preflight_checks`、`warnings`、`cutover_checklist` 和 `rollback_checklist`；维护页会要求当前目标目录和配置选项已经成功预览，且没有失败预检项后才允许开始恢复。预检覆盖目标路径隔离、目标目录状态、备份内容、目标文件系统容量和配置文件处理。`target_path` 必须是服务器上的绝对路径，并且必须位于当前 `storage.root`、备份来源和本地备份目标/仓库之外；父目录必须已存在，目标目录不存在或为空。`restore` 写入前会在服务端重新执行同一套预检；预检失败会拒绝恢复并写入失败恢复记录。`local` 恢复会把快照中的 `data/` 内容复制到目标目录根部并立即校验；`include_config = true` 时，配置文件会恢复到 `target_path/.mnemonas-restore/config.toml`。`restic` 恢复预览使用 `restic ls --json`，实际恢复执行 `restic restore latest --tag mnemonas --tag job:<id> --path <source>`，并把 restic 默认恢复出的来源目录内容整理到目标根目录。`rclone` 恢复预览使用 `rclone lsjson`，实际恢复执行 `rclone copy` 和 `rclone check --one-way`。恢复完成后，`restore-verify` 会只读检查目标目录，返回文件数、字节数、配置文件是否存在、是否检测到 `files/` 与 `.mnemonas/` 等完整 storage root 结构，并把符号链接、非常规文件或结构不完整情况作为警告返回；最近一次报告会持久化为 `last_restore_verify`，刷新维护页后仍可查看。维护页会在恢复成功后自动进入恢复后切换清单，并显示本次恢复的回滚清单。

需要同时恢复多个独立任务或多个目标目录时，可以先调用批量预览，再执行批量恢复：

```bash
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/batch-restore-preview \
  -H 'Content-Type: application/json' \
  -d '{"items":[{"job_id":"external-disk","target_path":"/mnt/restore/a","include_config":true},{"job_id":"rclone-cloud","target_path":"/mnt/restore/b","include_config":false}]}'

curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/batch-restore \
  -H 'Content-Type: application/json' \
  -d '{"items":[{"job_id":"external-disk","target_path":"/mnt/restore/a","include_config":true},{"job_id":"rclone-cloud","target_path":"/mnt/restore/b","include_config":false}]}'
```

批量恢复最多包含 20 个条目，并会拒绝同一批次内重复或父子嵌套的目标目录。批量预览不写入数据；批量恢复按顺序执行，每个成功恢复都会立即运行一次 `restore-verify`。部分失败时总结果会带 `warning`，因此应逐项检查 `items[]` 的状态、错误和只读校验结果。

维护页的“导出报告”会下载当前备份任务的恢复审计 JSON，内容包括最近备份、保留检测、恢复演练、显式恢复、只读校验、恢复历史和待处理发现项。建议在切换 `storage.root` 前下载一份，恢复失败时也可连同诊断包一起保存。

切换建议：

1. 确认 `restore-verify` 没有无法解释的警告；如果恢复的是整个 MnemoNAS 存储根目录，应看到 `files/` 和 `.mnemonas/`。
2. 将当前配置文件和当前 `storage.root` 保留为回滚点。
3. 停止 `mnemonas` 和 `mnemonas-dataplane`，把 `storage.root` 指向恢复目录，或把恢复目录迁移到正式挂载点。
4. 启动服务后检查健康端点、登录、文件列表、上传、下载和版本历史。
5. 确认新目录可用后再清理旧目录；如果失败，恢复旧配置并指回旧 `storage.root`。

### 方法 1：使用 rclone

[rclone](https://rclone.org/) 是强大的命令行同步工具，支持数十种云存储。

#### 安装 rclone

```bash
# Debian/Ubuntu
sudo apt install rclone      # Debian/Ubuntu

# macOS
brew install rclone          # macOS
```

如果发行版仓库里的 rclone 版本过旧，再参考 rclone 官方安装文档选择合适的安装方式；避免在生产机器上直接复制执行未审阅的管道安装命令。

#### 配置远程存储

```bash
# 交互式配置
rclone config

# 示例：配置阿里云 OSS
# n) New remote
# name> aliyun
# Storage> s3
# provider> Alibaba
# env_auth> false
# access_key_id> <你的 AccessKey ID>
# secret_access_key> <你的 AccessKey Secret>
# endpoint> oss-cn-shanghai.aliyuncs.com
# acl> private
```

#### 备份脚本

创建 `backup.sh`：

```bash
#!/bin/bash
set -euo pipefail

# 配置：SOURCE_DIR 必须是快照目录，或已停服务后的 storage.root
SOURCE_DIR="${SOURCE_DIR:-$HOME/.mnemonas}"
REMOTE="aliyun:mnemonas-backup"  # 改为你的远程配置
DATE=$(date +%Y%m%d)

echo "=== MnemoNAS 备份开始 $(date) ==="

echo "正在同步完整存储根目录..."
rclone sync "$SOURCE_DIR" "$REMOTE/current" \
    --progress \
    --transfers 4 \
    --checkers 8 \
    --backup-dir "$REMOTE/history/$DATE"

echo "=== 备份完成 $(date) ==="
```

#### 定时备份

```bash
# 添加 crontab
crontab -e

# 每天凌晨 3 点执行备份
0 3 * * * /path/to/backup.sh >> /var/log/mnemonas-backup.log 2>&1
```

---

### 方法 2：使用 restic

[restic](https://restic.net/) 是现代化的备份工具，支持加密和去重。

#### 安装

```bash
sudo apt install restic      # Debian/Ubuntu
brew install restic          # macOS
```

#### 初始化仓库

```bash
# 本地仓库
restic init --repo /backup/mnemonas-restic

# 远程仓库（S3 兼容）
export AWS_ACCESS_KEY_ID=<key>
export AWS_SECRET_ACCESS_KEY=<secret>
restic init --repo s3:s3.amazonaws.com/bucket/mnemonas
```

#### 执行备份

```bash
# 备份
SOURCE_DIR="${SOURCE_DIR:-$HOME/.mnemonas}"
restic backup "$SOURCE_DIR" \
    --repo /backup/mnemonas-restic \
    --tag mnemonas

# 查看快照
restic snapshots --repo /backup/mnemonas-restic

# 恢复
restic restore latest \
    --repo /backup/mnemonas-restic \
    --target /restore/mnemonas
```

#### 自动清理旧快照

```bash
# 保留策略：保留最近 7 天每天、4 周每周、12 个月每月的快照
restic forget \
    --repo /backup/mnemonas-restic \
    --keep-daily 7 \
    --keep-weekly 4 \
    --keep-monthly 12 \
    --prune
```

---

### 方法 3：rsync 本地同步

适用于本地外置硬盘备份：

```bash
#!/bin/bash
# 同步到外置硬盘；SOURCE_DIR 必须是快照目录，或已停服务后的 storage.root
SOURCE_DIR="${SOURCE_DIR:-$HOME/.mnemonas}"
BACKUP_DIR="/mnt/backup-drive/mnemonas"

rsync -aHAX --delete \
    --exclude='*.tmp' \
    "$SOURCE_DIR/" \
    "$BACKUP_DIR/"
```

---

### 方法 4：Docker 目录备份

如果使用 Docker 部署（宿主机 `~/.mnemonas` 映射到容器内 `/data`）：

```bash
# 停止服务
docker compose stop

# 备份目录
tar czf mnemonas-data.tar.gz -C ~/.mnemonas .

# 启动服务
docker compose start
```

---

## 🔙 恢复数据

恢复前先停止 MnemoNAS，避免新写入和恢复目录交叉。systemd 部署通常恢复到 `/srv/mnemonas`，Docker 部署通常恢复到宿主机映射的 `~/.mnemonas`。

### 从 rclone 恢复

```bash
# systemd 示例：恢复到临时目录
sudo systemctl stop mnemonas mnemonas-dataplane
sudo mkdir -p /srv/mnemonas-restored
sudo rclone copy aliyun:mnemonas-backup/current /srv/mnemonas-restored
sudo rclone check aliyun:mnemonas-backup/current /srv/mnemonas-restored --one-way

# 验证后替换原目录
sudo mv /srv/mnemonas /srv/mnemonas-old
sudo mv /srv/mnemonas-restored /srv/mnemonas
sudo chown -R mnemonas:mnemonas /srv/mnemonas
sudo chmod 0750 /srv/mnemonas /srv/mnemonas/files
sudo chmod 0700 /srv/mnemonas/.mnemonas
```

### 从 restic 恢复

```bash
# 查看可用快照
restic snapshots --repo /backup/mnemonas-restic

# 恢复指定快照到临时目录，验证后再替换原 storage.root
sudo systemctl stop mnemonas mnemonas-dataplane
restic restore <snapshot-id> \
    --repo /backup/mnemonas-restic \
    --target /restore/mnemonas
```

通过 MnemoNAS 维护页或 `/api/v1/maintenance/backups/{id}/restore` 恢复 restic 任务时，服务会自动使用 `mnemonas` 与 `job:<id>` tag 选择最近快照，并把 restic 默认生成的原始来源路径整理为目标目录根部，避免手动移动 `/restore/.../srv/mnemonas` 这类嵌套路径。

如果恢复目标是 Docker 的宿主机目录，把上面的服务命令替换为 `docker compose stop` / `docker compose start`，并确认目录所有者与 `.env` 中的 `MNEMONAS_UID` / `MNEMONAS_GID` 一致。

### 验证恢复

恢复后务必验证：

```bash
# systemd 启动服务
sudo systemctl start mnemonas-dataplane mnemonas
sudo mnemonas-doctor

# Docker 启动服务
docker compose up -d

# 检查健康状态
curl http://localhost:8080/health

# 运行 scrub 检查数据完整性
curl -X POST \
    -H "Authorization: Bearer <access-token>" \
    http://localhost:8080/api/v1/maintenance/scrub
```

---

## ⚠️ 重要提示

### 备份验证

定期验证备份是否可用：

```bash
# restic 验证
restic check --repo /backup/mnemonas-restic

# rclone 验证
SOURCE_DIR="${SOURCE_DIR:-$HOME/.mnemonas}"
rclone check "$SOURCE_DIR" aliyun:mnemonas-backup/current
```

### 备份加密

如果备份到云存储，建议启用加密：

```bash
# restic 默认加密
# 或使用 rclone crypt
rclone config
# 创建 crypt remote 包装原有 remote
```

### 备份监控

内置 `[[backup.jobs]]` 会复用 `[alerts]` 通知通道：当备份失败、恢复演练失败、恢复演练超过 `restore_drill_stale_after` 后仍缺失或过期，成功备份带有保留策略检测警告，或手动保留策略检测失败/告警时，会发送 `backup_run`、`backup_restore_drill` 或 `backup_retention_check` 事件。恢复演练提醒会限频发送，并在任务视图中记录 `last_restore_drill_reminder_at`。可使用 Webhook，也可启用 SMTP 邮件。Webhook 启用方式：

```toml
[alerts]
enabled = true
webhook_url = "https://your-webhook.example/alert"
webhook_method = "POST"
```

`POST` 会发送 JSON body，包含 `type`、`level`、`message`、`timestamp`、`hostname` 和 `details`；`details` 中包含任务 ID、任务名、运行 ID、状态、错误信息和快照路径等字段。`GET` 模式会把同样的基础字段编码进 query，并把 `details` 作为 JSON 字符串。

外部 restic/rclone 脚本仍建议保留失败告警：

```bash
# backup.sh 开头添加；set -e 退出时也能触发
notify_failure() {
    local status=$?
    if [ "$status" -ne 0 ]; then
        curl -fsS -X POST "https://your-webhook.com/alert" \
            -d "message=MnemoNAS 备份失败" || true
    fi
    exit "$status"
}
trap notify_failure EXIT
```

### 勿备份到同一硬盘

备份的核心是**冗余**，备份到同一块硬盘毫无意义。

---

## 📊 备份策略示例

### 最小成本部署

```text
日常备份：每周停服务或从快照用 rclone 同步到网盘
月度备份：停服务或从快照用 rsync 备份到外置硬盘
```

### 进阶用户

```text
每日备份：restic 到本地 NAS/外置盘（增量、加密）
每周备份：rclone 同步到 S3/OSS
每月备份：物理外置硬盘离线存放
```

### 生产环境

```text
每日快照：从 ZFS/Btrfs/LVM 快照运行 restic，保留 30 天每日快照
异地备份：从同一快照复制到另一云厂商或异地机器
恢复演练：每季度至少做一次抽样恢复
```

---

## 📖 相关资源

- [rclone 官方文档](https://rclone.org/docs/)
- [restic 官方文档](https://restic.readthedocs.io/)
- [FAQ - 如何备份 MnemoNAS 数据](faq.md#q-如何备份-mnemonas-数据)
- [配置参考](../mnemonas.example.toml)
