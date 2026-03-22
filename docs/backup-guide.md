# 备份指南

[English](backup-guide.en.md) | 简体中文

数据安全是 MnemoNAS 的核心关注点。本文档介绍如何正确备份 MnemoNAS 数据，确保在各类故障后都能恢复。

## 备份策略：3-2-1 原则

推荐遵循业界标准的 3-2-1 备份原则：

| 原则 | 说明 | MnemoNAS 实践 |
| --- | --- | --- |
| 3 份数据 | 生产数据 + 2 份副本 | 主数据 + 外置硬盘 + 云存储或另一台机器 |
| 2 种介质 | 不同类型的存储介质 | SSD + HDD，或本地磁盘 + 云存储 |
| 1 份异地/离线副本 | 至少 1 份不在主机内 | 云存储或离线物理磁盘 |

RAID、ZFS mirror、Btrfs RAID1 和 mdadm 可以降低磁盘故障风险，但不是备份。

## 需要备份的内容

默认 `storage.root` 为 `~/.mnemonas`。如果部署使用其他存储根目录，应替换以下路径。

systemd 部署通常使用 `/srv/mnemonas` 保存数据，并使用 `/etc/mnemonas/config.toml` 保存配置。配置文件位于数据目录之外，应单独纳入备份。

```text
storage.root/
├── files/                  # 用户文件
├── .mnemonas/              # 内部数据
│   ├── objects/            # CAS 对象
│   ├── index.db            # SQLite 元数据
│   ├── trash/              # 回收站内容
│   ├── thumbnails/         # 缩略图缓存
│   ├── maintenance/        # Scrub/GC 状态
│   └── activity/           # 最近操作日志
├── secrets.json            # 自动生成的 JWT/WebDAV 密钥
└── config.toml             # 直接运行或 Docker 配置（如果放在此处）
```

| 路径 | 重要性 | 说明 |
| --- | --- | --- |
| `files/` | 关键 | 当前用户数据 |
| `.mnemonas/` | 关键 | 元数据、版本、回收站和维护状态 |
| `secrets.json` | 中等 | 自动生成的 WebDAV/JWT 密钥 |
| `config.toml` | 中等 | 直接运行和 Docker 配置；systemd 通常放在 `/etc/mnemonas` |

## 一致性优先

备份 MnemoNAS 时不要在服务运行中直接复制活跃数据目录，尤其不要把 `files/` 和 `.mnemonas/` 分开同步。推荐二选一：

1. 使用 ZFS/Btrfs/LVM 快照，从只读快照目录备份。
2. 没有快照能力时，先停止 `mnemonas` 和 `mnemonas-dataplane`，完成备份后再启动。

下面的示例都假设 `SOURCE_DIR` 是一致性来源：可以是快照挂载目录，也可以是停服务后的 `storage.root`。

systemd 部署的冷备份窗口：

```bash
sudo systemctl stop mnemonas mnemonas-dataplane
# 在这里运行 rclone/restic/rsync 备份
sudo install -D -m 0600 /etc/mnemonas/config.toml /backup/mnemonas-config.toml
sudo systemctl start mnemonas-dataplane mnemonas
```

Docker 部署的冷备份窗口：

```bash
docker compose stop
# 在这里运行 rclone/restic/rsync 备份
docker compose start
```

## 方法 0：内置备份任务

MnemoNAS 提供内置备份任务入口，可在维护页或 API 中执行、查看健康状态和触发恢复演练。任务类型支持：

- `local`：把来源目录复制成带 `manifest.json` 的本地快照。
- `restic`：调用系统中的 `restic` 可执行文件，把来源目录写入 restic 仓库。
- `rclone`：调用系统中的 `rclone` 可执行文件，把来源目录同步到 rclone remote。

限制：

- `local.destination` 必须是 `storage.root` 之外的绝对路径，且不能是文件系统根目录或受保护系统目录；已存在的路径组件不能是符号链接，避免递归把备份写回源目录或写入符号链接指向的位置。本地恢复预览、恢复和恢复演练在读取快照 manifest 或创建演练产物前也会重新检查该目标路径。
- 默认来源是 `storage.root`；生产环境更推荐把 `source` 指向 ZFS/Btrfs/LVM 快照挂载目录。
- 源目录中遇到符号链接会中止备份任务，避免备份逃逸到源目录之外；`rclone` 恢复演练也会在执行远端校验前拒绝当前源树中的符号链接。
- `restic` 和 `rclone` 任务不会通过 shell 拼接命令；`command` 只能是可执行名或绝对路径，不能包含空白或控制字符；`extra_args`、`exclude` 和 `retention_policy` 不能包含控制字符。`extra_args` 会作为 argv 追加到备份命令，恢复命令不会复用备份专用参数。
- `password_file`、`config_file` 必须是 `source` 与 `storage.root` 之外的普通文件，且已存在的路径组件不能是符号链接，避免把备份凭据重新纳入备份数据或通过符号链接别名访问凭据。
- 任务视图、运行结果、恢复/预览结果、恢复报告和批量恢复结果中的目标路径与远端目标字段，以及 API 可见的备份错误、警告和恢复报告 findings 文本，会对内嵌 userinfo、token、密码、secret 和 key 参数做 `<redacted>` 脱敏；参数名中的 `_`/`-` 分隔符即使以 `%5F`/`%2D` 编码也会识别。
- 备份提醒事件不会外发来源、目标、恢复目录、快照/manifest 路径或原始错误/警告文本，只保留状态、触发原因、计数、时间、失败分类和是否省略位置/错误详情的摘要字段。
- 实际 restic/rclone 命令仍使用配置中的原始 `repository` 或 `remote`。客户端在恢复后继续调用 `restore-verify` 时，应复用原请求中的 `target_path`，不要把响应中用于展示的脱敏 `target_path` 当作新的请求参数。
- `schedule_interval` 是服务内置的轻量调度器，适合固定间隔任务；复杂窗口、限速、网络唤醒和多阶段恢复仍建议配合 systemd timer 或外部编排。

示例配置：

```toml
[backup]

[[backup.jobs]]
id = "external-disk"
name = "External disk backup"
type = "local"
source = ""                                # empty means storage.root
destination = "/mnt/backup-drive/mnemonas" # must be outside storage.root
disabled = false
schedule_interval = "24h"                  # 每 24 小时运行；0、空值或省略表示只手动运行
schedule_window_start = "02:00"            # 可选；自动运行只会在这个本地时间窗口内开始
schedule_window_end = "05:00"              # 可跨越午夜，例如 22:00 到 06:00
stale_after = "72h"                        # 72 小时内没有成功备份时标记为过期
restore_drill_stale_after = "720h"         # 30 天内没有成功恢复演练时提醒
max_snapshots = 7                          # 最多保留 7 个快照
max_age = "720h"                           # 快照最多保留 30 天
include_config = true
verify_after_backup = true
exclude = [".mnemonas/thumbnails"]
```

restic 示例：

```toml
[[backup.jobs]]
id = "restic-remote"
name = "Restic encrypted backup"
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
name = "Rclone cloud sync"
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

`schedule_window_start`/`schedule_window_end` 只限制自动调度，手动“立即备份”不受影响。窗口使用服务器本地时间的 `HH:MM`，可以跨午夜。

`local` 保留策略在成功备份后执行，始终保留当前快照；`max_snapshots = 0` 和 `max_age = "0"` 表示不启用对应维度的自动清理。

`restic` 和 `rclone` 的保留策略由外部工具管理，例如 `restic forget --prune`、systemd timer 或 rclone 目标端生命周期规则。配置 `retention_policy` 后，维护页会把该任务标记为“远端保留策略已确认”，否则显示需要确认。

每次成功备份后会自动执行一次保留策略检测，也可以在维护页手动点击“检查保留”：

- `local` 会校验本地快照 `manifest.json` 和快照布局后统计快照范围。
- `restic` 会执行 `restic snapshots --json --tag mnemonas --tag job:<id>`。
- `rclone` 会执行 `rclone lsjson <remote> --recursive --files-only`。

本地快照目录名不是规范运行 ID，或 `snapshots/` 根目录出现非快照条目（只有 `.partial` 目录会被当作未完成快照跳过），或 manifest 缺失、manifest 路径包含符号链接、manifest 不是常规文件时，保留策略检测会失败并写入失败结果。

manifest 中出现任务 ID、运行 ID、`created_at` 不匹配、不安全归档路径、重复路径、负文件大小、无效权限位、无效 SHA-256、统计字段不一致、缺少 `data/` 目录、manifest 未登记的额外文件或顶层异常目录时，也会失败，避免把损坏快照当作可用快照。

本地恢复预览、恢复和恢复演练解析最近一次快照时，还会要求持久化状态里的快照路径和 manifest 路径与当前 `local.destination/<job-id>/snapshots/<run-id>/manifest.json` 位置一致。最新完成快照不能缺失或缺少 manifest，`snapshots/` 根目录不能包含非快照条目或非规范快照目录，manifest 路径不能包含符号链接，manifest 必须是常规文件；不一致会拒绝执行，避免使用当前备份目标之外的快照。

检测结果会写入 `last_retention_check`，并在快照缺失、远端为空、未填写 `retention_policy` 或命令失败时提示警告。`restore_drill_stale_after` 控制定期恢复演练提醒；留空或未配置时默认 30 天。

首页会按同一口径汇总备份需处理任务数量、主要原因和建议处理步骤。维护页会显示任务健康状态、保留策略状态、恢复演练状态、恢复演练历史与成功率摘要、下次自动运行时间、自动窗口、最近备份、最近恢复目标、最近恢复记录列表和最近清理的旧快照数量。

存在备份失败、保留策略异常、恢复演练待处理、最近备份或恢复警告、恢复待校验或恢复检查失败/警告时，任务行会汇总显示“需处理”原因，并给出对应的建议处理步骤。

备份、恢复、恢复演练、只读校验和保留检测开始执行时会先写入 `running` 状态。服务启动时，如果状态文件中仍有上次进程退出前遗留的 `running` 记录，会将其标记为失败并写回状态文件。

恢复演练历史与显式恢复历史默认都保留最近 20 条，失败演练和失败恢复也会记录错误信息。失败演练还会记录稳定的 `failure_category`，用于区分无快照、完整性校验失败、外部命令失败、I/O 问题等常见原因。

重启服务后可通过维护 API 执行：

```bash
# 列出任务
curl -b cookies.txt http://localhost:8080/api/v1/maintenance/backups

# 立即运行
curl -X POST -b cookies.txt http://localhost:8080/api/v1/maintenance/backups/external-disk/run

# 检查保留策略和可见的远端/本地内容
curl -X POST -b cookies.txt http://localhost:8080/api/v1/maintenance/backups/external-disk/retention-check

# 执行恢复演练或远端一致性检查；本地临时恢复目录默认删除
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore-drill \
  -H 'Content-Type: application/json' \
  -d '{"keep_artifact":false}'
```

`local` 恢复演练会先确认快照中没有 manifest 未登记的额外文件，再把最近一次快照复制到临时目录，然后按 manifest 校验每个文件的大小、权限位和 SHA-256。`keep_artifact = true` 会保留临时恢复目录，便于人工抽查。默认不保留产物时，如果校验完成但临时恢复目录清理失败，演练结果保持 completed，同时返回 `warning=true`、`warnings[]`、`artifact_kept=true` 和 `restored_path`，便于在维护页继续处理残留产物。`restic` 恢复演练当前执行 `restic check`；`rclone` 恢复演练当前执行 `rclone check --one-way`，用于验证仓库或远端一致性。

真正取回数据时，`local`、`restic` 和 `rclone` 任务应恢复到指定的独立目录：

```bash
# 先预览：校验目标安全性，并确认预估文件数、字节数和样例路径
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore-preview \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas","include_config":true}'

curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas","include_config":true}'

# 恢复后检查：只读扫描目标并识别 storage-root 布局
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore-verify \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas"}'

# rclone 任务示例：从远端复制，并在安装前用 rclone check 校验
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/rclone-cloud/restore \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas-rclone","include_config":false}'

# restic 任务示例：恢复 latest 和 job 标签匹配的内容，并安装到目标根目录
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/restic-cloud/restore \
  -H 'Content-Type: application/json' \
  -d '{"target_path":"/mnt/restore/mnemonas-restic","include_config":false}'
```

`restore-preview` 不会写入目标目录，也不会写入恢复历史。它会复用恢复目标安全校验，并返回预计文件数、字节数、最多 10 个样例路径、`preflight_checks`、`warnings`、`cutover_checklist` 和 `rollback_checklist`。本地恢复预览会先检查快照布局，拒绝 manifest 未登记的额外文件和顶层异常目录。

维护页会要求当前目标目录和配置选项已经成功预览，且没有失败预检项后才允许开始恢复。执行前还会展示目标目录、写入边界、恢复内容、配置文件、预检统计和恢复后只读校验安排的复核摘要，并额外汇总目标状态、冲突/覆盖风险、配置与权限影响、恢复范围和恢复后校验要求。

预检覆盖目标路径隔离、`target_state` 目标目录状态、备份内容、目标文件系统容量和配置文件处理。`target_state` 会明确目标目录尚不存在或目标目录已存在且为空；两者均为允许状态。目标不存在时容量预检检查父目录，目标目录已存在且为空时检查目标目录所在文件系统。

`preflight_checks[].status` 可能为 `passed`、`warning` 或 `failed`。`status = "warning"` 表示恢复可以继续但需要人工确认；`status = "failed"` 会阻止维护页开始恢复，并会在 `restore` 写入前被服务端预检拒绝。`warnings` 会汇总 warning 与 failed 预检详情，供维护页、批量预览和恢复历史展示。

`target_path` 必须满足以下规则：

- 使用服务器上的 POSIX 绝对路径，并以 `/` 开头。
- 不包含控制字符、反斜杠、`.` 或 `..` 路径段。
- 不是文件系统根目录或受保护系统目录。
- 位于当前 `storage.root`、备份来源和本地备份目标/仓库之外。

Windows 或 UNC 路径不是有效的服务器恢复目标。父目录必须已存在，目标目录不存在或为空，且已存在的路径组件不能是符号链接。无效的恢复 `target_path` 或批量恢复条目会返回 `400 Bad Request`；备份任务配置路径、备份源树或外部命令导致的不安全路径属于任务执行失败。

`restore` 写入前会在服务端重新执行同一套预检，并先确认本地快照没有 manifest 未登记的额外文件；预检或快照校验失败会拒绝恢复并写入失败恢复记录。`local` 恢复会把快照中的 `data/` 内容复制到目标目录根部，保留空目录和目录权限并立即校验文件；`include_config = true` 时，配置文件会恢复到 `target_path/.mnemonas-restore/config.toml`。

`restic` 恢复预览使用 `restic ls --json`，实际恢复执行 `restic restore latest --tag mnemonas --tag job:<id> --path <source>`，并把 restic 默认恢复出的来源目录内容整理到目标根目录。

`rclone` 恢复预览使用 `rclone lsjson`，实际恢复执行 `rclone copy <remote> <临时目录>` 和 `rclone check <remote> <临时目录> --one-way`。`restic` 预览和 `rclone` 预览/保留检查会拒绝输出中的不安全文件路径，包括空路径、控制字符、反斜杠、Windows/UNC 语法、`.`/`..` 路径段，或越过已配置来源边界的绝对路径。

`restic` 与 `rclone` 恢复在安装目标目录前会拒绝恢复出的符号链接和非常规文件；`rclone` 拒绝后，再把临时目录安装到 `target_path`。

恢复完成后，`restore-verify` 会只读检查目标目录，返回文件数、字节数、配置文件是否存在、是否检测到 `files/` 与 `.mnemonas/` 等完整存储根目录结构，并把符号链接、非常规文件或结构不完整情况作为警告返回。

对于 `local` 任务，它会优先对照同一目标目录最近一次成功恢复记录中的快照，找不到匹配恢复记录时才回退到最新本地快照 manifest 和目录布局，并在结果中返回本次对照的 `snapshot_path` 与 `manifest_path`。

缺失文件、校验和/大小/权限不匹配、额外常规文件或目录、缺失目录、目录权限漂移，以及已存在的 `.mnemonas-restore/config.toml` 与对照 manifest 不一致，都会作为警告返回。最近一次报告会持久化为 `last_restore_verify`，刷新维护页后仍可查看。维护页会在恢复成功后自动进入恢复后切换清单，并显示本次恢复的回滚清单；两组清单均可逐项确认，并显示本地确认进度。

语法无效的 `target_path` 请求会以 `400 Bad Request` 拒绝，且不会更新最近恢复或最近恢复校验状态；通过语法校验但因边界、目标状态、备份内容或外部命令失败的恢复尝试会按失败结果记录。

`restore-verify` 在检查目标目录是否存在之前，会复用同一套 `target_path` 校验，包括控制字符、反斜杠、`.` / `..` 路径段、Windows/UNC 语法、受保护目录、边界目录和符号链接路径组件检查。

维护页和恢复报告只会把目标路径一致、校验开始时间不早于最近恢复完成时间，且最近恢复已成功完成的 `last_restore_verify` 关联到最近一次恢复；运行中、成功或失败的匹配校验都会写入 `last_matching_restore_verify`。

校验仍在运行时，任务视图会显示检查中，恢复报告会提示完成前不应切换恢复目标；不匹配时会提示最近恢复尚未完成匹配的只读校验。维护页可从备份任务列表直接对最近一次成功恢复的目标目录重新执行只读校验，用于补齐中断、刷新或人工排障后的“待校验”状态。

最近恢复仍在运行时，恢复报告会明确提示恢复未完成，且不会关联旧的只读校验结果。

批量恢复多个独立任务或多个目标目录时，应先调用批量预览，再执行批量恢复：

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

批量恢复最多包含 20 个条目，并会拒绝同一批次内重复或父子嵌套的目标目录。批量预览不写入数据。

维护页会在批量恢复弹窗内显示准备度摘要，集中展示选中任务数、目标目录填写情况、配置文件恢复项数和预览状态。

维护页还提供“选择待处理”“选择全部”和“清空选择”入口：“选择待处理”只选择当前可恢复且存在失败、过期、待校验、最近备份/恢复警告或恢复检查警告状态的任务；“选择全部”会选中当前可恢复任务。两者都会为未填写目标填入建议目录。

批量预览后，维护页会展示恢复项目数、目标目录互斥状态、预计恢复内容、配置文件恢复项数、预检统计和恢复后只读校验安排的执行前复核，并以批量影响摘要汇总目标冲突、覆盖风险、配置与权限影响、失败/提醒预检和恢复后校验。

批量恢复 API 在写入任何目标目录前会重新执行整批预检。如果任一条目目标冲突、预检失败或无法生成预览，本次批量恢复直接失败并返回逐项错误，且不会写入任何目标数据。整批预检通过后，批量恢复按顺序执行，每个成功恢复都会立即运行一次 `restore-verify`。

顶层 `total_files` 与 `verified_bytes` 汇总已完成条目的只读校验结果。预检通过后的执行期故障仍可能导致部分失败并让总结果带 `warning`，因此应逐项检查 `items[]` 的状态、错误和只读校验结果。

维护页的批量恢复结果会在项目卡片和可复制记录中保留任务名称、备份目标、远端或仓库标识、保留策略状态、只读校验结论、校验错误和处置建议，便于远端或可移动介质恢复后留档。

维护页会在任务摘要中展示恢复摘要发现项，并可通过任务行或恢复完成弹窗中的“导出摘要”下载当前备份任务的恢复 JSON。该 JSON 包括最近备份、保留检测、恢复演练、恢复演练历史、显式恢复、只读校验、恢复历史和待处理发现项。下载响应使用 `Cache-Control: no-store`、`Pragma: no-cache`、`X-Content-Type-Options: nosniff` 和 `Referrer-Policy: no-referrer`。建议在切换 `storage.root` 前下载一份，恢复失败时也可连同诊断包一起保存。

切换建议：

1. 确认 `restore-verify` 没有无法解释的警告；如果恢复的是整个 MnemoNAS 存储根目录，应看到 `files/` 和 `.mnemonas/`。
2. 将当前配置文件和当前 `storage.root` 保留为回滚点。
3. 停止 `mnemonas` 和 `mnemonas-dataplane`，把 `storage.root` 指向恢复目录，或把恢复目录迁移到正式挂载点。
4. 启动服务后检查健康端点、登录、文件列表、上传、下载和版本历史。
5. 确认新目录可用后再清理旧目录；如果失败，恢复旧配置并指回旧 `storage.root`。

## 方法 1：rclone

[rclone](https://rclone.org/) 是命令行同步工具，支持数十种云存储。

安装：

```bash
sudo apt install rclone
brew install rclone
```

如果发行版仓库里的 rclone 版本过旧，再参考 rclone 官方安装文档选择合适的安装方式。生产机器应避免运行未经审阅的 pipe-to-shell 安装命令。

配置远端：

```bash
rclone config
```

示例备份脚本：

```bash
#!/bin/bash
set -euo pipefail

SOURCE_DIR="${SOURCE_DIR:-$HOME/.mnemonas}"
REMOTE="remote:mnemonas-backup"
DATE=$(date +%Y%m%d)

echo "=== MnemoNAS 备份开始 $(date) ==="

rclone sync "$SOURCE_DIR" "$REMOTE/current" \
  --progress \
  --transfers 4 \
  --checkers 8 \
  --backup-dir "$REMOTE/history/$DATE"

echo "=== 备份完成 $(date) ==="
```

通过 cron 定时执行：

```bash
crontab -e
```

```cron
0 3 * * * /path/to/backup.sh >> /var/log/mnemonas-backup.log 2>&1
```

## 方法 2：restic

[restic](https://restic.net/) 是现代化的备份工具，支持加密和去重。

安装：

```bash
sudo apt install restic
brew install restic
```

初始化：

```bash
restic init --repo /backup/mnemonas-restic

export AWS_ACCESS_KEY_ID=<key>
export AWS_SECRET_ACCESS_KEY=<secret>
restic init --repo s3:s3.amazonaws.com/bucket/mnemonas
```

备份并查看快照：

```bash
SOURCE_DIR="${SOURCE_DIR:-$HOME/.mnemonas}"
restic backup "$SOURCE_DIR" \
  --repo /backup/mnemonas-restic \
  --tag mnemonas

restic snapshots --repo /backup/mnemonas-restic
```

恢复：

```bash
restic restore latest \
  --repo /backup/mnemonas-restic \
  --target /restore/mnemonas
```

保留策略：

```bash
restic forget \
  --repo /backup/mnemonas-restic \
  --keep-daily 7 \
  --keep-weekly 4 \
  --keep-monthly 12 \
  --prune
```

## 方法 3：本地 rsync

适用于本地外置硬盘备份：

```bash
#!/bin/bash
set -euo pipefail

SOURCE_DIR="${SOURCE_DIR:-$HOME/.mnemonas}"
BACKUP_DIR="/mnt/backup-drive/mnemonas"

rsync -aHAX --delete \
  --exclude='*.tmp' \
  "$SOURCE_DIR/" \
  "$BACKUP_DIR/"
```

`SOURCE_DIR` 必须是快照目录，或已停服务后的 `storage.root`。

## 方法 4：Docker 目录备份

如果使用 Docker 部署（宿主机 `~/.mnemonas` 映射到容器内 `/data`）：

```bash
docker compose stop
tar czf mnemonas-data.tar.gz -C ~/.mnemonas .
docker compose start
```

## 恢复数据

恢复前先停止 MnemoNAS，避免新写入和恢复目录交叉。systemd 部署通常恢复到 `/srv/mnemonas`，Docker 部署通常恢复到宿主机映射的 `~/.mnemonas`。

### 从 rclone 恢复

```bash
# systemd 示例：恢复到临时目录
sudo systemctl stop mnemonas mnemonas-dataplane
sudo mkdir -p /srv/mnemonas-restored
sudo rclone copy remote:mnemonas-backup/current /srv/mnemonas-restored
sudo rclone check remote:mnemonas-backup/current /srv/mnemonas-restored --one-way

# 验证后替换旧目录
sudo mv /srv/mnemonas /srv/mnemonas-old
sudo mv /srv/mnemonas-restored /srv/mnemonas
sudo chown -R mnemonas:mnemonas /srv/mnemonas
sudo chmod 0750 /srv/mnemonas /srv/mnemonas/files
sudo chmod 0700 /srv/mnemonas/.mnemonas
```

### 从 restic 恢复

```bash
restic snapshots --repo /backup/mnemonas-restic

sudo systemctl stop mnemonas mnemonas-dataplane
restic restore <snapshot-id> \
  --repo /backup/mnemonas-restic \
  --target /restore/mnemonas
```

通过 MnemoNAS 维护页或 `/api/v1/maintenance/backups/{id}/restore` 恢复 restic 任务时，服务会自动使用 `mnemonas` 与 `job:<id>` tag 选择最近快照，并把 restic 默认生成的原始来源路径整理为目标目录根部，避免手动移动 `/restore/.../srv/mnemonas` 这类嵌套路径。

如果恢复目标是 Docker 的宿主机目录，把上面的服务命令替换为 `docker compose stop` / `docker compose start`，并确认目录所有者与 `.env` 中的 `MNEMONAS_UID` / `MNEMONAS_GID` 一致。

## 验证恢复

恢复后务必验证：

```bash
sudo systemctl start mnemonas-dataplane mnemonas
sudo mnemonas-doctor
```

Web UI 安全自检会检查启用中的本地备份作业目标，提示目标位于 `storage.root` 或来源目录内、路径经过符号链接、目标不是目录、目录不存在或可能不可写等问题。systemd 部署中，`mnemonas-doctor` 会检查 `BACKUP_ROOT` 是否位于 `storage.root` 内部，并在备份目标是符号链接、不是目录、与主存储共享同一个 filesystem source 或不可写时给出风险提示。长期备份目标应使用独立磁盘、独立数据集或远端存储。

```bash
# 启动 Docker；使用 release 镜像时，改用 docker compose up -d --no-build。
docker compose up -d

curl http://localhost:8080/health

curl -X POST \
  -H "Authorization: Bearer <access-token>" \
  http://localhost:8080/api/v1/maintenance/scrub
```

## 验证备份

定期验证备份是否可用：

```bash
restic check --repo /backup/mnemonas-restic

SOURCE_DIR="${SOURCE_DIR:-$HOME/.mnemonas}"
rclone check "$SOURCE_DIR" remote:mnemonas-backup/current
```

还应定期做恢复演练。从未恢复过的备份只是推测，不是已经验证过的恢复路径。

## 加密异地备份

如果备份到云存储，建议启用加密：

```bash
rclone config
```

restic 默认加密备份。rclone 可使用 `crypt` remote 包装实际存储 remote。仓库密码和云凭据应放在密码管理器或密钥存储中。

## 备份失败提醒

内置 `[[backup.jobs]]` 会复用 `[alerts]` 通知通道。备份失败、显式恢复失败、显式恢复完成但带警告、恢复后只读校验失败或带警告、恢复演练失败或带警告、恢复演练超过 `restore_drill_stale_after` 后仍缺失或过期、成功备份带有保留策略检测警告，或手动保留策略检测失败/提醒时，会发送 `backup_run`、`backup_restore`、`backup_restore_verify`、`backup_restore_drill` 或 `backup_retention_check` 事件。

事件 `message` 使用固定公共摘要，不包含任务名称、路径或原始错误文本。事件详情只包含任务 ID、运行 ID、任务类型、触发原因、状态、时间、文件/字节/快照计数、警告数量、错误信息是否存在、失败分类，以及是否省略位置详情；不包含任务名称、来源、备份目标、恢复目标路径、快照路径、manifest 路径、原始 warning 或原始错误文本。

恢复演练提醒会限频发送，并在任务视图中记录 `last_restore_drill_reminder_at`。可使用 Webhook、Telegram、企业微信、钉钉或 SMTP 邮件。Webhook 启用方式：

```toml
[alerts]
enabled = true
webhook_url = "https://webhook.example.com/alert"
webhook_method = "POST"
```

`POST` 会发送 JSON body，包含 `type`、`level`、`message`、`timestamp`、`hostname` 和 `details`；`details` 只使用上文列出的脱敏摘要字段。`GET` 模式会把同样的基础字段编码进 query，并把 `details` 作为 JSON 字符串；两种模式都不会发送任务名称、来源、备份目标、恢复目标路径、快照路径、manifest 路径、原始 warning 或原始错误文本。

外部 restic/rclone 脚本仍建议保留失败提醒：

```bash
notify_failure() {
  local status=$?
  if [ "$status" -ne 0 ]; then
    curl -fsS -X POST "https://webhook.example.com/alert" \
      -d "message=MnemoNAS backup failed" || true
  fi
  exit "$status"
}
trap notify_failure EXIT
```

## 策略示例

| 部署类型 | 策略 |
| --- | --- |
| 最小部署 | 每周冷备份或从快照备份到云存储；每月复制到外置硬盘 |
| 进阶部署 | 每日 restic 到本地 NAS 或外置盘；每周 rclone 到 S3/OSS；每月离线硬盘 |
| 类生产部署 | 每日文件系统快照，从快照运行 restic，复制到异地，并按季度执行恢复演练 |

## 相关资源

- [rclone 官方文档](https://rclone.org/docs/)
- [restic 官方文档](https://restic.readthedocs.io/)
- [FAQ](faq.md)
- [配置参考](configuration.md)
