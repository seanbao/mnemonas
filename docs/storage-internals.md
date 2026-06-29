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

启用回收站时，删除的文件会移入 `.mnemonas/trash/`，元数据保存在 SQLite 中。元数据记录原始路径、删除时间、持久化到期时间和内容位置。关闭回收站时，删除会直接永久生效。

回收站到期天数和容量上限在 `[storage.trash]` 下配置。`[storage.retention].gc_interval` 驱动同一个后台任务清理过期文件版本与到期回收站项目。设为 `0` 会停用这两类周期清理，但不会停用容量清理、永久删除或清空回收站操作。

每个回收站项目在创建时取得随机 ID，该 ID 在项目生命周期内保持不变。精确清空操作在同一存储写锁内载入一次当前回收站状态，并在任何删除发生前，对所有仍存在的已选项目及其后代恢复逻辑路径完成访问规则预检。任一预检失败都不会删除已选项目。预检通过后，项目按请求 ID 顺序删除；未选择或在操作开始后新增的项目保持不变，已不存在的已选 ID 记为跳过。执行阶段出现硬失败时，尚未处理且仍存在的已选项目保持不变。删除、保留和跳过三个结果集构成原始请求的完整、互不重叠分区，并分别保留请求顺序。

Linux 与 macOS 上，工作区元数据读取会根据设备号、inode、ctime、类型与权限位、大小和纳秒级修改时间生成不透明对象身份令牌。REST 删除确认要求调用方提交从当前列表取得的身份令牌，并在同一存储读锁内取得完整删除策略与全部目标树快照。根对象回调依次检查写权限、工作区根目录下的嵌套挂载边界、文件类型和观察身份；身份不匹配时不会读取内容或遍历目录。通过根对象检查后，快照遍历会对每个后续条目执行相同的权限、挂载边界和类型检查，再通过不跟随符号链接且只接受普通文件的读取路径计算内容哈希。目标树中的嵌套挂载点、符号链接、FIFO、Unix socket 和其他特殊文件会被立即拒绝，不会形成不完整快照，也不会在打开阶段阻塞。挂载边界来自主机挂载表，因此同一设备上的 bind mount 也会被识别；目标树包含挂载点或目标位于挂载点内都会被拒绝。不透明目标令牌覆盖路径、对象身份、条目类型、大小、纳秒级修改时间与内容，目录与空目录也会参与计算。单次确认最多包含 1000 个互不嵌套的目标。平台无法提供所需对象身份时，列表返回 `null`，且服务端拒绝创建观察式删除意图。

执行删除时，完整删除策略比较、当前目标树的逐项写权限复核、目标令牌比较与变更操作在同一存储写锁内完成。策略比较先于目标树遍历，过期策略不会触发全树读取。在对象捕获开始前发现删除方式、保留天数、清理周期、容量上限或目标树变化时，不会提交工作区、索引、版本、分享、收藏、回收站或活动变更；已确认目标消失或父路径不再是目录也按目标变化处理。对象捕获已经开始后，失败路径可能执行禁止覆盖的回滚，并可能改变对象 ctime 或父目录时间戳。WebDAV 条件删除会先在同一写锁内复核完整目标树的写权限，再读取相关目标属性并求值条件；只有依赖 ETag 的条件才计算内容哈希。

权限、目标令牌或 WebDAV 条件通过后，删除流程不会再次从原逻辑路径解析待删除对象。服务端先通过不跟随符号链接的句柄打开根对象作为见证对象，并将句柄身份与本次请求中已验证快照的根身份比较；随后使用禁止覆盖的原子重命名，将当前叶节点捕获到源文件系统同一父目录下的随机暂存路径。暂存对象必须与见证对象指向同一对象。完整暂存树会映射回原逻辑路径，并再次核对条目集合、类型、大小、修改时间、后代身份与文件内容。文件哈希前后也会复核打开句柄和暂存路径仍指向同一对象。重命名可能改变根对象的 ctime，因此仅在 `os.SameFile` 验证成功后沿用变更前的根身份；后代身份仍按暂存状态逐项比较。

回收站副本发布后，服务端会从源暂存树强制计算内容摘要，并对目标副本建立包含路径、类型、大小、权限、对象身份和摘要的完整清单；源端、目标端和挂载边界会在提交前再次复核。最终物理删除会把已验证源暂存对象移入同一父目录下权限不宽于 `0700` 的随机隔离目录，并通过服务端持有的目录句柄逐项核对后移除。硬链接组中的第一个条目使用完整身份令牌核对，后续条目同时要求与清单中的同一 inode 匹配，以容纳本次删除前序链接引起的 ctime 变化。未知条目、同名替换、身份变化或新挂载都会停止递归删除并保留隔离内容。

回收站删除只从已验证的暂存路径复制内容，并在回收站元数据、索引和删除钩子完成前保留源暂存对象；同文件系统和 `EXDEV` 路径采用相同流程。永久删除也只清理暂存对象，不再按原逻辑路径执行删除。删除期间原路径出现的新对象不会被复制、覆盖或移除。逻辑提交前失败时，回滚仅执行从暂存路径到原路径的禁止覆盖重命名。原路径已被占用、挂载边界发生变化或暂存对象身份无法确认时，新对象保持不变，流程返回需要恢复处理的残留错误；已创建的回收站元数据与副本会在无法安全撤销时成对保留。REST 将此类未提交结果返回为 `500 Internal Server Error`，且不记录删除活动。

适用的索引、删除钩子、回收站或版本元数据已经提交后，物理清理失败不会把已生效删除报告为失败。REST 返回带清理警告的 `200 OK`，并在删除活动详情中标记对应的清理警告；只有暂存或隔离区仍有残留时，服务端错误日志才记录可供恢复处理的残留路径。WebDAV 返回带清理警告的 `204 No Content`。两者的逻辑路径均保持已删除。请求取消不会中断已经提交后的隔离清理。内容已经移除、仅父目录同步失败时只报告持久化警告，不生成不存在的残留路径。

上述原子边界只覆盖通过 MnemoNAS 存储锁执行的操作。同 UID 进程直接修改文件系统、特权进程并发挂载、进程崩溃或断电不受该锁串行化；这类事件可能使暂存对象无法确认或无法恢复。服务端不会反向移动或删除身份未知的替换物，也不会自动清理所有权无法确认的 `.mnemonas-delete-*.stage`、`.mnemonas-delete-*.quarantine` 或回收站内部残留。当前没有启动时恢复日志，残留需要依据服务端日志和文件系统证据人工复核。

删除专用遍历不会改变普通目录遍历、搜索或文件计数行为。删除流程在意图快照、权限复核、回收站目标描述、跨根复制前后、源暂存树进入隔离区前后以及递归移除前分别重新读取挂载表。读取失败或挂载路径非法时会拒绝继续操作并返回 `ErrNotRegular`。尚未捕获目标时，REST 和 WebDAV 将其映射为 `409 Conflict`；捕获后无法安全回滚时返回恢复残留，逻辑提交后的同类问题返回清理警告。仅在目标边界仍可验证时清理已复制副本；边界无法验证时停止递归清理并保留内部副本，以避免跨越新挂载。回收站项目的暂存和递归清理采用相同原则。

每个回收站项目的到期时间在删除时确定，后续配置变化不会重新计算。容量不足时，较早项目仍可能在到期前被清理，因此到期时间不是最低保留保证。

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

恢复 URL 的 `path` 查询值应在可复制示例中编码，例如 `/documents/report.docx` 对应 `%2Fdocuments%2Freport.docx`。

```bash
MNEMONAS_ACCESS_TOKEN="<access-token>"
curl_auth_config="$(mktemp)"
trap 'rm -f "$curl_auth_config"' EXIT
chmod 600 "$curl_auth_config"
printf 'header = "Authorization: Bearer %s"\n' "$MNEMONAS_ACCESS_TOKEN" > "$curl_auth_config"

curl --config "$curl_auth_config" \
  http://localhost:8080/api/v1/versions/documents/report.docx

curl -X POST \
  --config "$curl_auth_config" \
  "http://localhost:8080/api/v1/versions/abc123.../restore?path=%2Fdocuments%2Freport.docx"
```

## 与传统 NAS 对比

| 范围 | MnemoNAS | 传统 NAS | 纯 CAS 系统 |
| --- | --- | --- | --- |
| 当前文件 | 原生文件 | 原生文件 | CAS 对象 |
| 版本存储 | CAS 对象 | 文件系统快照 | CAS 对象 |
| 当前文件可读性 | 可直接读取 | 可直接读取 | 需要专用软件 |
| 去重 | BLAKE3 整对象版本；dataplane 中提供 CDC file API，但当前版本历史不会按 CDC 分块引用计数 | 依赖文件系统 | 核心功能 |
| 元数据 | SQLite | 文件系统和应用元数据 | JSON/DB |
| 复杂度 | 中 | 简单文件共享用法较低 | 高 |

混合方案牺牲一部分纯粹性，换取可恢复性和用户可检查性。
当前版本历史使用整对象 CAS 快照；FastCDC API 属于数据面能力，不表示已启用块级版本去重。

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
