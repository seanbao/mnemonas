# 备份指南

数据安全是 MnemoNAS 的核心关注点。本文档介绍如何正确备份 MnemoNAS 数据，确保在各种故障场景下都能恢复。

## 📋 备份策略：3-2-1 原则

推荐遵循业界标准的 **3-2-1 备份原则**：

| 原则 | 说明 | MnemoNAS 实践 |
|------|------|---------------|
| **3 份数据** | 生产数据 + 2 份副本 | 本地 + 外置盘 + 云存储 |
| **2 种介质** | 不同类型的存储介质 | SSD + HDD / 云 |
| **1 份离线** | 至少 1 份在异地或离线 | 云存储 / 物理备份盘 |

---

## 🗂️ 需要备份的内容

MnemoNAS 数据分为以下几个部分：

```
~/.mnemonas/
├── data/           # CAS 对象存储（核心数据，必须备份）
│   └── objects/    # 按哈希分片存储的数据块
├── metadata/       # 元数据（文件树、版本、回收站）
│   ├── fileinfo/   # 文件元信息
│   ├── versions/   # 版本历史
│   └── trash/      # 回收站
└── config.toml     # 配置文件
```

| 目录 | 重要性 | 说明 |
|------|--------|------|
| `data/` | ⭐⭐⭐ 极高 | 所有文件内容，丢失无法恢复 |
| `metadata/` | ⭐⭐⭐ 极高 | 文件结构和版本，丢失后文件名信息丢失 |
| `config.toml` | ⭐⭐ 中等 | 可重新配置，但备份省事 |

---

## 🔄 备份方法

### 方法 1：使用 rclone（推荐）

[rclone](https://rclone.org/) 是强大的命令行同步工具，支持数十种云存储。

#### 安装 rclone

```bash
# Linux/macOS
curl https://rclone.org/install.sh | sudo bash

# 或使用包管理器
sudo apt install rclone      # Debian/Ubuntu
brew install rclone          # macOS
```

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
set -e

# 配置
MNEMONAS_DIR="$HOME/.mnemonas"
REMOTE="aliyun:mnemonas-backup"  # 改为你的远程配置
DATE=$(date +%Y%m%d)

echo "=== MnemoNAS 备份开始 $(date) ==="

# 同步数据目录（增量）
echo "正在同步 data 目录..."
rclone sync "$MNEMONAS_DIR/data" "$REMOTE/data" \
    --progress \
    --transfers 4 \
    --checkers 8

# 同步元数据目录
echo "正在同步 metadata 目录..."
rclone sync "$MNEMONAS_DIR/metadata" "$REMOTE/metadata" \
    --progress

# 备份配置文件
echo "正在备份配置..."
rclone copy "$MNEMONAS_DIR/config.toml" "$REMOTE/config/" \
    --backup-dir "$REMOTE/config-history/$DATE"

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
restic backup ~/.mnemonas \
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
# 同步到外置硬盘
BACKUP_DIR="/mnt/backup-drive/mnemonas"

rsync -avz --delete \
    --exclude='*.tmp' \
    ~/.mnemonas/ \
    "$BACKUP_DIR/"
```

---

### 方法 4：Docker 数据卷备份

如果使用 Docker 部署：

```bash
# 停止服务
docker compose stop

# 备份数据卷
docker run --rm \
    -v mnemonas_data:/data:ro \
    -v $(pwd):/backup \
    alpine tar czf /backup/mnemonas-data.tar.gz -C /data .

# 启动服务
docker compose start
```

---

## 🔙 恢复数据

### 从 rclone 恢复

```bash
# 恢复到新目录
rclone sync aliyun:mnemonas-backup ~/.mnemonas-restored

# 验证后替换原目录
mv ~/.mnemonas ~/.mnemonas-old
mv ~/.mnemonas-restored ~/.mnemonas
```

### 从 restic 恢复

```bash
# 查看可用快照
restic snapshots --repo /backup/mnemonas-restic

# 恢复指定快照
restic restore <snapshot-id> \
    --repo /backup/mnemonas-restic \
    --target /restore

# 或恢复到指定路径的特定目录
restic restore latest \
    --repo /backup/mnemonas-restic \
    --target ~/.mnemonas \
    --include /home/user/.mnemonas/data
```

### 验证恢复

恢复后务必验证：

```bash
# 启动服务
docker compose up -d

# 检查健康状态
curl http://localhost:8080/health

# 运行 scrub 检查数据完整性
curl -X POST http://localhost:8080/api/v1/scrub
```

---

## ⚠️ 重要提示

### 备份验证

定期验证备份是否可用：

```bash
# restic 验证
restic check --repo /backup/mnemonas-restic

# rclone 验证
rclone check ~/.mnemonas aliyun:mnemonas-backup
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
# backup.sh 末尾添加
if [ $? -ne 0 ]; then
    curl -X POST "https://your-webhook.com/alert" \
        -d "message=MnemoNAS 备份失败"
fi
```

### 勿备份到同一硬盘

备份的核心是**冗余**，备份到同一块硬盘毫无意义。

---

## 📊 备份策略示例

### 家庭用户（最小成本）

```
日常备份：每周 1 次 rclone 同步到网盘（阿里云盘/百度网盘）
月度备份：每月 1 次 rsync 到外置硬盘
```

### 进阶用户

```
每日备份：restic 到本地 NAS/外置盘（增量、加密）
每周备份：rclone 同步到 S3/OSS
每月备份：物理外置硬盘离线存放
```

### 生产环境

```
实时同步：rclone/lsyncd 实时同步到热备服务器
每日快照：restic 保留 30 天每日快照
异地备份：跨区域复制到另一云厂商
```

---

## 📖 相关资源

- [rclone 官方文档](https://rclone.org/docs/)
- [restic 官方文档](https://restic.readthedocs.io/)
- [FAQ - 如何备份 MnemoNAS 数据](faq.md#q-如何备份-mnemonas-数据)
- [配置参考](../mnemonas.example.toml)
