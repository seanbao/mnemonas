# 存储原理与最佳实践

本文档介绍 MnemoNAS 的存储架构原理、与传统 NAS 的区别，以及推荐的底层文件系统配置。

## 目录

- [存储架构概述](#存储架构概述)
- [CAS 存储原理](#cas-存储原理)
- [与传统 NAS 的区别](#与传统-nas-的区别)
- [文件系统兼容性](#文件系统兼容性)
- [最佳实践：底层配置推荐](#最佳实践底层配置推荐)
- [性能与安全指标](#性能与安全指标)

---

## 存储架构概述

MnemoNAS 采用 **CAS (Content-Addressable Storage) + CDC (Content-Defined Chunking)** 架构，这是现代备份工具（如 Git、restic、borg）的主流设计，而非传统 NAS 的直接文件存储方式。

```
┌─────────────────────────────────────────────────────────────┐
│                   MnemoNAS 应用层                            │
│  ┌───────────────────────────────────────────────────────┐  │
│  │  WebDAV 接口  →  元数据管理  →  CAS 存储  →  CDC 分块  │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                             │
│  功能：去重、版本历史、完整性校验、软删除                     │
├─────────────────────────────────────────────────────────────┤
│                   文件系统层（可选任意）                      │
│  ext4 / XFS / Btrfs / ZFS / NTFS / APFS                    │
├─────────────────────────────────────────────────────────────┤
│                   物理存储层                                 │
│  单盘 / RAID / 云存储                                       │
└─────────────────────────────────────────────────────────────┘
```

### 核心设计理念

1. **应用层智能**：去重、版本、校验在应用层实现，不依赖底层文件系统特性
2. **数据可迁移**：硬盘拔下来插到任何电脑都能读取，不绑定特定软件
3. **备份友好**：纯文件结构，rclone 可直接同步到云存储

---

## CAS 存储原理

### 内容寻址 (Content-Addressable)

文件内容通过 BLAKE3 哈希算法生成唯一地址：

```
用户文件: /photos/vacation.jpg (10MB)
    ↓ BLAKE3 哈希
内容哈希: ab3dd7a00797b2337fdc76ed3ef4851c5c8d1248...
    ↓ 分片目录
存储路径: data/ab/3d/ab3dd7a00797b2337fdc76ed3ef4851c...
```

### 目录结构

```
~/.mnemonas/
├── data/                           # CAS 对象存储
│   ├── ab/                         # 一级分片 (hash[0:2])
│   │   └── 3d/                     # 二级分片 (hash[2:4])
│   │       └── ab3dd7a00797...     # 完整哈希作为文件名
│   └── ...
├── metadata/                       # 文件元数据 (JSON)
│   ├── %2Fphotos%2Fvacation.jpg.json
│   └── .trash/                     # 回收站
├── thumbnails/                     # 缩略图缓存
└── maintenance/                    # 维护记录
```

### CDC 分块 (大文件处理)

大文件使用 FastCDC 算法切分为可变大小块：

| 参数 | 值 | 说明 |
|------|-----|------|
| 最小块 | 256 KB | 避免过小的块 |
| 平均块 | 1 MB | 平衡去重率与元数据开销 |
| 最大块 | 4 MB | 限制单块大小 |

**去重效果示例**：

```
文件 v1: [chunk-A] [chunk-B] [chunk-C]  → 存储 3 个块
文件 v2: [chunk-A] [chunk-B'] [chunk-C] → 仅存储 chunk-B'
                                          (chunk-A, chunk-C 复用)
```

### 元数据格式

每个文件对应一个 JSON 元数据文件：

```json
{
  "path": "/photos/vacation.jpg",
  "is_dir": false,
  "size": 10485760,
  "mod_time": "2026-01-15T10:30:00Z",
  "content_hash": "ab3dd7a00797b2337fdc76ed3ef4851c...",
  "versions": [
    {
      "hash": "ab3dd7a00797b2337fdc76ed3ef4851c...",
      "size": 10485760,
      "timestamp": "2026-01-15T10:30:00Z"
    },
    {
      "hash": "f8e2c1a9b4d6e7f0123456789abcdef...",
      "size": 10240000,
      "timestamp": "2026-01-10T14:20:00Z"
    }
  ]
}
```

### 原子写入保证

写入流程确保崩溃一致性：

```
1. 写入临时文件:  data/ab/3d/ab3dd7a...tmp
2. fsync 数据:    确保数据落盘
3. 原子重命名:    data/ab/3d/ab3dd7a...tmp → data/ab/3d/ab3dd7a...
4. fsync 目录:    确保元数据落盘
```

**保证**：任何时刻断电/崩溃，要么是旧版本，要么是新版本，绝不会出现半写入的损坏文件。

---

## 与传统 NAS 的区别

| 特性 | MnemoNAS (CAS) | 传统 NAS (TrueNAS/Synology) |
|------|----------------|---------------------------|
| **存储方式** | 内容寻址，哈希为地址 | 直接文件存储 |
| **去重** | 应用层，跨文件系统 | 依赖 ZFS/Btrfs |
| **版本历史** | 每文件独立版本 | 整个文件系统快照 |
| **文件系统依赖** | 无，ext4 即可 | 通常需要 ZFS/Btrfs |
| **数据可迁移** | ✅ 拔盘即用 | ❌ 需要原环境 |
| **云备份** | ✅ rclone 直接同步 | ⚠️ 需要 zfs send |
| **设计来源** | Git/restic/borg | 传统文件服务器 |

### 类似设计的项目

| 项目 | 存储方式 | 相似度 |
|------|---------|--------|
| **Git** | 对象存储 (loose objects) | ⭐⭐⭐⭐⭐ |
| **restic** | CAS + CDC + 加密 | ⭐⭐⭐⭐⭐ |
| **borg** | CAS + 去重 + 压缩 | ⭐⭐⭐⭐⭐ |
| **Perkeep** | 内容寻址 + Merkle Tree | ⭐⭐⭐⭐ |
| **IPFS** | 内容寻址 + DHT | ⭐⭐⭐ |

---

## 文件系统兼容性

**MnemoNAS 不依赖特定文件系统**，可运行在任何 POSIX 兼容系统上。

| 文件系统 | 兼容性 | 推荐度 | 说明 |
|----------|--------|--------|------|
| **ext4** | ✅ | ⭐⭐⭐ | Linux 默认，稳定可靠，单盘首选 |
| **XFS** | ✅ | ⭐⭐⭐ | 大文件/高并发性能好 |
| **Btrfs** | ✅ | ⭐⭐⭐⭐ | 支持快照、scrub，可做额外保护层 |
| **ZFS** | ✅ | ⭐⭐⭐⭐⭐ | 自带 scrub、mirror、压缩，最推荐 |
| **NTFS** | ✅ | ⭐⭐ | Windows 环境可用 |
| **APFS** | ✅ | ⭐⭐⭐ | macOS 环境可用 |
| **exFAT** | ⚠️ | ⭐ | 无原子操作保证，不推荐 |
| **NFS 挂载** | ✅ | ⭐⭐ | 网络存储可用，注意延迟 |

---

## 最佳实践：底层配置推荐

### 推荐配置矩阵

根据预算和需求选择合适的底层配置：

| 场景 | 推荐配置 | 数据安全 | 性能 | 成本 |
|------|---------|---------|------|------|
| **入门/预算有限** | 单盘 ext4 + 云备份 | ⭐⭐ | ⭐⭐⭐ | $ |
| **家庭推荐** | ZFS mirror (2盘) | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | $$ |
| **进阶玩家** | Btrfs RAID1 (2盘) | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | $$ |
| **兼容性优先** | mdadm RAID1 + ext4 | ⭐⭐⭐⭐ | ⭐⭐⭐ | $$ |
| **大容量** | ZFS RAIDZ1 (3盘+) | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | $$$ |
| **企业级** | ZFS RAIDZ2 + SSD 缓存 | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | $$$$ |

---

### 配置 1：ZFS Mirror（推荐）

**最省心的高安全方案**

```bash
# 创建镜像池（2块盘）
sudo zpool create mnemonas mirror /dev/sda /dev/sdb

# 设置挂载点
sudo zfs set mountpoint=/srv/mnemonas mnemonas

# 启用压缩（推荐 lz4，透明压缩）
sudo zfs set compression=lz4 mnemonas

# 设置记录大小（匹配 CAS 平均块大小）
sudo zfs set recordsize=1M mnemonas

# 定期 scrub（添加到 crontab）
# 0 2 * * 0 /sbin/zpool scrub mnemonas
```

**配置 MnemoNAS**：
```toml
[storage]
data_dir = "/srv/mnemonas/data"
metadata_dir = "/srv/mnemonas/metadata"
```

**优点**：
- 坏一块盘数据不丢失
- ZFS scrub 定期校验底层数据
- 透明压缩节省 20-40% 空间
- 企业级可靠性

---

### 配置 2：Btrfs RAID1

**Linux 原生方案**

```bash
# 创建 Btrfs RAID1（2块盘）
sudo mkfs.btrfs -m raid1 -d raid1 /dev/sda /dev/sdb

# 挂载
sudo mkdir -p /srv/mnemonas
sudo mount /dev/sda /srv/mnemonas

# 添加到 fstab
echo "UUID=$(blkid -s UUID -o value /dev/sda) /srv/mnemonas btrfs defaults,compress=zstd 0 0" | sudo tee -a /etc/fstab

# 定期 scrub
# 0 2 * * 0 /sbin/btrfs scrub start /srv/mnemonas
```

**优点**：
- Linux 内核原生支持
- 支持快照（可做额外备份层）
- 可在线扩容

---

### 配置 3：mdadm RAID1 + ext4

**经典兼容方案**

```bash
# 创建 RAID1
sudo mdadm --create /dev/md0 --level=1 --raid-devices=2 /dev/sda /dev/sdb

# 格式化
sudo mkfs.ext4 /dev/md0

# 挂载
sudo mkdir -p /srv/mnemonas
sudo mount /dev/md0 /srv/mnemonas

# 保存配置
sudo mdadm --detail --scan | sudo tee /etc/mdadm/mdadm.conf
sudo update-initramfs -u
```

**优点**：
- 兼容性最好，任何 Linux 都能读
- ext4 成熟稳定
- 恢复简单

---

### 配置 4：单盘 + 云备份（最小方案）

**预算有限时的选择**

```bash
# 使用现有单盘，ext4 即可
sudo mkdir -p /srv/mnemonas

# 配置 rclone 定时备份到云存储
# crontab: 0 3 * * * rclone sync /srv/mnemonas remote:mnemonas-backup
```

**重要**：单盘方案必须配合云备份或外置盘备份，否则硬盘故障将导致数据丢失。

---

## 性能与安全指标

### 专业 NAS 数据安全标准

MnemoNAS 达到的数据安全指标：

| 指标 | 专业 NAS 标准 | MnemoNAS 实现 |
|------|--------------|---------------|
| **静默损坏检测** | ✅ 定期校验 | ✅ BLAKE3 端到端校验 + Scrub |
| **崩溃一致性** | ✅ 事务性写入 | ✅ 原子写入 (tmp→fsync→rename) |
| **误删恢复** | ✅ 快照/回收站 | ✅ 回收站 + 版本历史 |
| **版本回退** | ✅ 快照 | ✅ 每文件独立版本历史 |
| **数据去重** | ⚠️ 部分支持 | ✅ CDC 块级去重 |
| **备份验证** | ✅ 校验和 | ✅ 所有数据自带校验 |

### 数据安全层次

```
┌─────────────────────────────────────────────────────────┐
│ 第 1 层：应用层保护（MnemoNAS 提供）                      │
│  • 端到端 BLAKE3 校验                                   │
│  • 原子写入保证                                         │
│  • 版本历史 + 回收站                                    │
│  • Scrub 定期巡检                                      │
├─────────────────────────────────────────────────────────┤
│ 第 2 层：文件系统保护（ZFS/Btrfs 提供）                  │
│  • 底层 scrub 校验                                     │
│  • 写时复制 (CoW)                                      │
│  • 文件系统级快照（可选额外备份）                        │
├─────────────────────────────────────────────────────────┤
│ 第 3 层：硬件冗余（RAID/Mirror 提供）                    │
│  • 单盘故障不丢数据                                    │
│  • 热备盘自动重建                                      │
├─────────────────────────────────────────────────────────┤
│ 第 4 层：异地备份（3-2-1 原则）                         │
│  • 云存储备份                                          │
│  • 物理外置盘                                          │
└─────────────────────────────────────────────────────────┘
```

### 性能基准

| 操作 | 基准 | 影响因素 |
|------|------|---------|
| **顺序写入** | 接近磁盘原始速度 | 磁盘 I/O |
| **顺序读取** | 接近磁盘原始速度 | 磁盘 I/O |
| **小文件写入** | ~1000 文件/秒 | fsync 频率 |
| **目录列举** | ~10000 文件/秒 | 元数据 I/O |
| **去重命中** | 微秒级 | 内存索引 |
| **Scrub 速度** | ~100 MB/秒 | 磁盘顺序读 |

### 调优建议

**ZFS 调优**：
```bash
# 设置 ARC 缓存大小（服务器内存充足时）
echo "options zfs zfs_arc_max=8589934592" | sudo tee /etc/modprobe.d/zfs.conf  # 8GB

# 启用 SSD 缓存（L2ARC）
sudo zpool add mnemonas cache /dev/nvme0n1
```

**MnemoNAS 调优**：
```toml
[dataplane.cdc]
# 大文件为主时，增大平均块大小
avg_chunk_size = 2097152  # 2MB

[storage.retention]
# 减少版本保留以节省空间
max_versions = 20
max_age = "2160h"  # 90 天
```

---

## 总结

| 问题 | 答案 |
|------|------|
| **需要特定文件系统吗？** | 不需要，ext4 即可运行 |
| **推荐什么配置？** | ZFS mirror（2盘）最省心 |
| **能达到专业 NAS 安全标准吗？** | ✅ 结合 RAID + 备份可达到 |
| **与传统 NAS 有什么区别？** | CAS 架构，更接近 Git/restic |
| **数据能迁移吗？** | ✅ 拔盘插到任何电脑都能读 |

**关键原则**：
1. **应用层**：MnemoNAS 负责智能（去重、版本、校验）
2. **文件系统层**：推荐 ZFS/Btrfs 提供额外保护
3. **硬件层**：至少 2 块盘做镜像
4. **备份层**：遵循 3-2-1 原则，异地备份必不可少

---

## 相关文档

- [架构设计](architecture.md) - 系统架构详解
- [备份指南](backup-guide.md) - 3-2-1 备份策略
- [Docker 部署](docker-deployment.md) - 容器化部署
- [FAQ](faq.md) - 常见问题
