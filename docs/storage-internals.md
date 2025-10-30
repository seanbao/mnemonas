# 存储原理与运维建议

[English](storage-internals.en.md) | 简体中文

本文档说明 MnemoNAS 的存储架构、它与传统 NAS 系统的差异，以及推荐的底层文件系统配置。

## 概览

MnemoNAS 使用混合布局：

- 当前用户文件保存为普通文件。
- 历史版本保存为内容寻址对象。
- SQLite 保存版本、回收站、锁和索引等元数据。

```text
+---------------------------------------------------------+
|                  MnemoNAS application                   |
| WebDAV/API -> storage layer -> versions -> SQLite       |
+---------------------------------------------------------+
|                     Storage root                        |
| files/      当前用户文件                                |
| .mnemonas/  元数据、CAS 对象、回收站                    |
+---------------------------------------------------------+
|                Underlying filesystem                    |
| ext4 / XFS / Btrfs / ZFS / APFS / NTFS                  |
+---------------------------------------------------------+
|                    Physical media                       |
| single disk / mirror / RAID / remote backup             |
+---------------------------------------------------------+
```

设计目标：

- 当前文件无需专用软件即可读取。
- 版本历史与当前文件分离。
- 一致性敏感的元数据使用事务性存储。
- 让完整存储根目录的备份和迁移保持直接。

## 目录布局

默认存储根目录：

```text
~/.mnemonas/
├── files/
│   ├── documents/
│   │   └── report.docx
│   └── photos/
│       └── vacation.jpg
└── .mnemonas/
    ├── index.db
    ├── objects/
    │   └── ab/
    │       └── cd/
    │           └── abcd1234...
    └── trash/
        └── {trash-id}/
            └── content
```

## 原生当前文件

当前文件是 `files/` 下的普通文件。

优点：

- 具备操作系统级目录访问权限的用户可以直接读取当前文件。
- 离线迁移和备份更容易推理。
- 当前版本不会被锁定在专有的对象布局中。

重要边界：

- 读取 `files/` 是安全的。
- 在绕过 MnemoNAS 的情况下写入或删除 `files/`，不会创建版本、回收站记录、活动日志或元数据更新。
- 完整恢复版本、回收站和索引时，需要同时保留 `.mnemonas/` 和 `files/`。

## CAS 对象

历史内容保存在内容寻址存储中：

```text
objects/
├── ab/
│   └── cd/
│       └── abcd1234567890...
```

属性：

- 使用 BLAKE3 哈希寻址。
- 相同内容可以复用同一个对象。
- 读取时校验哈希完整性。
- 写入使用临时文件、`fsync` 和 rename，以保证崩溃一致性。
- 当对象载荷压缩后更小时，支持使用 zstd 压缩。

## SQLite 元数据

`index.db` 保存如下元数据：

| 表 | 用途 |
| --- | --- |
| `files` | 文件索引数据 |
| `versions` | 版本历史 |
| `versioning_overrides` | 单文件版本策略覆盖 |
| `trash` | 回收站元数据 |
| `file_locks` | WebDAV lock 状态 |

SQLite 为 MnemoNAS 提供 ACID 事务、索引和可迁移的元数据文件。

## 回收站

删除的文件会移入 `.mnemonas/trash/`，元数据保存在 SQLite 中。元数据记录原始路径、删除时间、过期时间和内容位置。

回收站保留时间和容量上限在 `[storage.trash]` 下配置。

## 版本策略

MnemoNAS 会自动为通常有历史价值的文件保存版本：

| 文件类型 | 默认行为 | 原因 |
| --- | --- | --- |
| 文本、Markdown、办公文档 | 保存版本 | 经常编辑 |
| 配置和源码文件 | 保存版本 | 变更应可追踪 |
| 图片 | 默认不保存版本 | 通常体积较大且追加式保存 |
| 视频 | 默认不保存版本 | 体积很大 |
| 超过默认大小限制的文件 | 默认不保存版本 | 存储成本高 |

保留策略示例：

```toml
[storage.retention]
max_versions = 50
max_age = "2160h"
```

版本 API：

```bash
curl -H "Authorization: Bearer <access-token>" \
  http://localhost:8080/api/v1/versions/documents/report.docx

curl -X POST \
  -H "Authorization: Bearer <access-token>" \
  "http://localhost:8080/api/v1/versions/abc123.../restore?path=/documents/report.docx"
```

## 与传统 NAS 对比

| 范围 | MnemoNAS | 传统 NAS | 纯 CAS 系统 |
| --- | --- | --- | --- |
| 当前文件 | 原生文件 | 原生文件 | CAS 对象 |
| 版本存储 | CAS 对象 | 文件系统快照 | CAS 对象 |
| 当前文件可读性 | 可直接读取 | 可直接读取 | 需要专用软件 |
| 去重 | BLAKE3 整对象版本；dataplane 中提供 CDC file API | 依赖文件系统 | 核心功能 |
| 元数据 | SQLite | 文件系统和应用元数据 | JSON/DB |
| 复杂度 | 中 | 简单文件共享用法较低 | 高 |

混合方案牺牲一部分纯粹性，换取可恢复性和用户可检查性。

## 文件系统兼容性

MnemoNAS 不要求特定文件系统。

| 文件系统 | 兼容性 | 建议 | 说明 |
| --- | --- | --- | --- |
| ext4 | 支持 | 好 | 稳定的 Linux 默认选择 |
| XFS | 支持 | 好 | 适合大文件和并发 |
| Btrfs | 支持 | 很好 | 快照和 scrub 可增加保护层 |
| ZFS | 支持 | 最佳 | Mirror、scrub、压缩和运维模型成熟 |
| NTFS | 支持 | 有限 | 适合 Windows 环境 |
| APFS | 支持 | 好 | 适合 macOS 环境 |
| exFAT | 不建议 | 差 | 原子性预期较弱 |
| NFS mount | 谨慎支持 | 有限 | 注意延迟和一致性行为 |

## 推荐配置

| 用途 | 配置 | 数据安全 | 成本 |
| --- | --- | --- | --- |
| 预算有限 | 单盘 ext4 + 云备份 | 基础 | 低 |
| 推荐镜像 | ZFS mirror，2 块盘 | 强 | 中 |
| 进阶 Linux | Btrfs RAID1 | 强 | 中 |
| 兼容性优先 | mdadm RAID1 + ext4 | 强 | 中 |
| 大容量 | ZFS RAIDZ1 或 RAIDZ2 | 更强 | 高 |

### ZFS Mirror

```bash
sudo zpool create mnemonas mirror /dev/sda /dev/sdb
sudo zfs set mountpoint=/srv/mnemonas mnemonas
sudo zfs set compression=lz4 mnemonas
sudo zfs set recordsize=1M mnemonas
```

计划 scrub：

```cron
0 2 * * 0 /sbin/zpool scrub mnemonas
```

配置：

```toml
[storage]
root = "/srv/mnemonas"
```

### Btrfs RAID1

```bash
sudo mkfs.btrfs -m raid1 -d raid1 /dev/sda /dev/sdb
sudo mkdir -p /srv/mnemonas
sudo mount /dev/sda /srv/mnemonas
```

fstab 示例：

```bash
echo "UUID=$(blkid -s UUID -o value /dev/sda) /srv/mnemonas btrfs defaults,compress=zstd 0 0" | sudo tee -a /etc/fstab
```

Scrub：

```cron
0 2 * * 0 /sbin/btrfs scrub start /srv/mnemonas
```

### mdadm RAID1 + ext4

```bash
sudo mdadm --create /dev/md0 --level=1 --raid-devices=2 /dev/sda /dev/sdb
sudo mkfs.ext4 /dev/md0
sudo mkdir -p /srv/mnemonas
sudo mount /dev/md0 /srv/mnemonas
sudo mdadm --detail --scan | sudo tee /etc/mdadm/mdadm.conf
sudo update-initramfs -u
```

### 单盘 + 云备份

```bash
sudo mkdir -p /srv/mnemonas
```

使用 rclone、restic 或 borg 同步前，应先使用快照或冷备份窗口。单盘部署必须有离机备份。

## 数据安全层

```text
第 1 层：MnemoNAS 应用保护
  - BLAKE3 校验
  - 原子写入
  - 版本历史和回收站
  - scrub

第 2 层：文件系统保护
  - ZFS/Btrfs scrub
  - 写时复制
  - 快照

第 3 层：硬件冗余
  - mirror 或 RAID
  - 热备盘

第 4 层：独立备份
  - 云备份
  - 离线外置盘
```

## 性能说明

| 操作 | 主要因素 |
| --- | --- |
| 顺序写入 | 磁盘 I/O |
| 顺序读取 | 磁盘 I/O |
| 小文件写入 | `fsync` 频率和元数据 I/O |
| 目录列表 | 元数据 I/O |
| 去重命中 | 内存/object 索引行为 |
| Scrub | 顺序读取吞吐 |

ZFS 调优：

```bash
echo "options zfs zfs_arc_max=8589934592" | sudo tee /etc/modprobe.d/zfs.conf
sudo zpool add mnemonas cache /dev/nvme0n1
```

MnemoNAS 调优：

```toml
[dataplane.cdc]
avg_chunk_size = 2097152

[storage.retention]
max_versions = 20
max_age = "2160h"
```

## 总结

| 问题 | 答案 |
| --- | --- |
| 是否需要特定文件系统？ | 不需要。ext4 足以运行。 |
| 推荐什么？ | ZFS mirror 提供更强可靠性。 |
| 能满足常见 NAS 可靠性预期吗？ | 可以，但需要配合 mirror/RAID 和备份。 |
| 数据能迁移到另一台机器吗？ | 可以，迁移完整存储根目录。当前文件仍可读取；版本需要 MnemoNAS 元数据。 |

核心原则：MnemoNAS 增加应用级版本、整对象版本去重、校验和恢复能力。文件系统冗余和独立备份仍由用户负责。

## 相关文档

- [架构](architecture.md)
- [备份指南](backup-guide.md)
- [Docker 部署](docker-deployment.md)
- [FAQ](faq.md)
