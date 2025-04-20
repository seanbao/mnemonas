# 备份指南

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
sudo rclone sync aliyun:mnemonas-backup/current /srv/mnemonas-restored

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

设置备份失败告警：

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

### 家庭用户（最小成本）

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
