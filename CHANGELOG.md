# 变更记录

[English](CHANGELOG.en.md) | 简体中文

所有重要变更都会记录在此文件中。

本项目遵循 [语义化版本 2.0.0](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

#### Web UI 功能
- **Dashboard 首页**
  - 首页概览（存储使用、文件数量、版本数量）
  - 最近活动动态
  - 快捷操作入口
  - 数据面健康状态

- **Files 文件管理**
  - 面包屑导航，支持目录层级快速跳转
  - 列表/网格视图切换
  - 文件操作上下文菜单（下载、重命名、复制路径、版本历史、删除）
  - 拖拽上传，支持文件夹
  - 批量操作（批量下载、批量删除）
  - 多文件上传队列面板，显示进度和状态
  - 缩略图预览

- **Album 相册模式**
  - 图片瀑布流布局
  - 自动缩略图生成
  - 沉浸式浏览体验

- **Versions 版本历史**
  - 查看文件所有历史版本
  - 版本时间、大小对比
  - 恢复到指定版本

- **Trash 回收站**
  - 已删除文件列表
  - 按删除时间排序
  - 单个/批量恢复
  - 清空回收站

- **Search 全局搜索**
  - 按文件名实时搜索
  - 搜索结果高亮
  - 快速跳转到文件位置

- **Activity 最近操作**
  - 关键操作记录
  - 按时间/操作类型筛选
  - 操作详情查看
  - 活动统计视图
  - 磁盘健康异常系统事件记录
  - 存储页显示文件系统类型、挂载点和设备/数据集来源

- **Users 用户管理**
  - 用户列表与状态
  - 创建/编辑/删除用户
  - 用户主目录与容量配额编辑
  - 密码重置
  - 启用/禁用用户

- **ShareAccess 分享链接**
  - 创建分享链接
  - 密码保护设置
  - 有效期配置
  - 访问统计
  - 分享链接访问页面
  - 分享风险提示、复核筛选、即将到期提醒、策略预设、按目录路径强制分享约束、按用户/组/角色限制路径分享创建维护范围和直接停用高风险分享链接

- **Settings 设置**
  - 服务器配置（监听地址、端口）
  - 存储路径配置（数据目录、元数据目录、临时目录）
  - 版本保留策略（最大版本数、保留时间、空间阈值、GC 间隔）
  - WebDAV 配置（启用开关、URL 前缀、只读模式、用户认证）
  - CDC 分块参数说明
  - 数据面连接状态
  - 周期 Scrub 调度开关、常规间隔、失败重试间隔和最大重试次数配置
  - 目录权限用户矩阵和未保存规则预览可复制复核记录，并保留后端持久化近期复核历史；服务端历史不可用时回退当前浏览器记录，便于留存路径、用户读写判定和相关分享影响
  - 公网访问向导和安全自检入口，辅助配置 HTTPS 反向代理、受信代理跳数和分享域名
  - 公网访问向导桌面与移动端 E2E 回归覆盖

- **Health 设备状态**
  - 系统运行时间
  - 数据面连接状态
  - 存储健康检查
  - 磁盘 SMART、温度、介质磨损、设备缺失和序列号漂移健康状态
  - 周期 Scrub 调度、最近状态和失败重试状态
  - SMB 预览运行态提示，避免把已配置共享误判为可挂载服务

- **Maintenance 备份与维护**
  - Scrub 数据完整性校验
  - GC 垃圾回收
  - 对象列表浏览
  - 磁盘健康即时探测
  - 备份任务健康状态、定时计划、快照保留策略和恢复演练状态
  - 诊断包导出

#### 后端 API
- **认证 API**
  - JWT Token 认证
  - 登录/登出/刷新
  - 密码修改
  - 用户信息获取

- **用户管理 API**
  - 用户 CRUD
  - 用户级容量配额，非管理员 Web/API 上传、复制和回收站恢复超限时返回 `QUOTA_EXCEEDED`，并可通过 Webhook/Telegram/企业微信/钉钉/SMTP 发送 `quota_exceeded` 提醒事件
  - `storage.directory_quotas` 目录级硬限制和存储页目录配额用量展示，Web/API 上传、复制、移动、回收站恢复、版本恢复以及 WebDAV PUT/COPY/MOVE 会在写入前检查命中的目录配额
  - 用户组和 `storage.directory_access_rules` 目录读写授权，可按用户、用户组或角色授予共享目录权限；Web/API、WebDAV 用户模式、搜索、分享、收藏、回收站和活动过滤使用同一权限判定
  - 设置 API 和 Web 设置页支持有效权限检查、未保存规则预览、按路径用户矩阵、相关分享影响检查、可复制权限复核记录和后端持久化近期复核历史，管理员可查看用户对指定路径的读写结果与来源
  - WebDAV 支持 `auth_type = "users"`，客户端可使用 MnemoNAS 用户账号挂载；非管理员挂载根目录映射到自己的 `home_dir`，guest 只读，PUT/COPY 写入遵守用户配额
  - 密码重置
  - 状态切换

- **分享 API**
  - 分享链接 CRUD
  - 密码验证
  - 公开访问
  - 新分享默认有效期、默认访问次数上限、按目录路径的密码/有效期/访问次数策略、按用户/组/角色限制路径分享创建维护范围和分享风险标记

- **最近操作 API**
  - 活动记录
  - 活动查询
  - 统计接口
  - Scrub 数据校验系统事件记录

- **设置 API**
  - 配置读取
  - 配置更新
  - 公网访问安全自检
  - 公网访问向导增加证书续期检查和失败排障提示
  - 磁盘健康配置热更新
  - 周期 Scrub 调度配置热更新
  - 提醒 Webhook、Telegram 与 SMTP 邮件通知配置热更新

- **备份与恢复 API**
  - 配置化本地备份任务与 restic/rclone 命令型远端目标
  - 立即执行备份
  - 轻量定时调度、自动备份时间窗、成功快照保留策略和任务健康状态
  - 最近快照恢复演练与 manifest 校验；本地快照、restic 仓库和 rclone 远端可先生成恢复预览，再恢复到安全目录；支持最多 20 个条目的批量恢复预览与顺序执行，并逐项返回恢复和只读校验结果；恢复前预检目标隔离、目录状态、容量、备份内容和配置处理，失败预检会阻止恢复；恢复后只读校验、切换清单、回滚清单、可复制恢复切换记录和恢复摘要导出；远端保留策略检测与周期化恢复演练提醒；恢复演练缺失/过期时发送限频提醒；恢复演练历史、成功率摘要与失败归因；恢复结果记录与历史记录；restic/rclone 任务支持远端一致性校验
  - 备份成功后自动检测保留策略，维护页可手动触发“检查保留”；本地任务统计快照范围，restic 解析 `snapshots --json`，rclone 解析 `lsjson`
  - 恢复后的只读校验结果持久化为 `last_restore_verify`，刷新维护页后仍可查看最近一次恢复检查
  - 备份失败、恢复演练失败、保留策略检测失败和备份警告事件接入 Webhook/Telegram/企业微信/钉钉/SMTP 提醒

- **磁盘健康 API**
  - `smartctl --json` 采集 SMART、自检状态、温度和通电时间
  - 解析 NVMe 介质磨损、可用备用容量、critical warning、介质错误和常见 ATA 寿命属性
  - 设备缺失、SMART 失败、温度过高和序列号不匹配会标记异常，并写入最近操作或通过 Webhook/Telegram/企业微信/钉钉/SMTP 发送 `disk_health` 事件

- **Scrub 数据完整性事件**
  - 手动 Scrub 完成后写入最近操作
  - `[maintenance.scrub]` 支持后台周期 Scrub 和失败后的限次自动重试
  - Settings API 和 Web 设置页可热更新周期 Scrub 调度配置
  - 诊断接口返回周期 Scrub 配置、最近执行状态和失败重试计数
  - Scrub 失败、发现对象异常或结果持久化不完整时，通过 Webhook/Telegram/企业微信/钉钉/SMTP 发送 `scrub_run` 事件

- **安全提醒事件**
  - 登录失败触发限流时发送限频的 `login_rate_limited` warning 事件
  - 登录限流提醒只包含用户名和客户端地址，不包含密码或 token

- **SMB 预览诊断**
  - 诊断接口和诊断导出返回脱敏的 `smb` 预览状态、共享数量和运行态说明
  - `nasd --check-config` 在 `smb.enabled=true` 时提示当前版本不会启动 SMB/Samba 监听器

#### 项目工程化
- GitHub Actions CI/CD 工作流（Go/Rust/Frontend 测试、Docker 构建）
- Release 自动化工作流（多平台二进制构建、Docker 镜像发布）
- Ubuntu/systemd 安装脚本，可将 release 包安装为 `mnemonas` 与 `mnemonas-dataplane` 服务
- `mnemonas-doctor` 部署诊断脚本，检查二进制、配置、systemd、健康端点、端口、存储挂载、备份目录、公网 HTTPS 证书状态、HTTP 到 HTTPS 跳转，并提示云安全组人工复核项
- `mnemonas-doctor --public-domain` 可识别后端控制面和数据面端口的宽泛 UFW 放行规则，并对存储路径和 WebDAV 用户文件路径中的 `~` 做一致展开
- `mnemonas-public-setup` 公网 HTTPS 反向代理配置助手
- Traefik 和 Cloudflare Tunnel 公网访问模板，并通过脚本检查避免开放后端和 dataplane 端口
- `mnemonas-uninstall-systemd` 卸载脚本，默认保留配置和数据，删除数据需要显式确认
- Docker Compose 启动前预检脚本，检查 Compose v2、Buildx、端口、目录权限、磁盘空间和已有配置
- Docker 镜像内置 `mnemonas-healthcheck` 健康检查二进制，不再依赖运行时 `curl`
- `tools/proto-gen` Rust protobuf 生成器，普通 dataplane/Docker 构建不再依赖系统 `protoc`
- systemd/Docker 脚本模拟测试，并接入 CI 的脚本校验流程
- 脚本模拟测试覆盖变更文件选择、WebDAV 认证模式、公网反向代理暴露检查、benchmark 路径和 Web Husky hooks
- `scripts/webdav-client-smoke.sh` 可对已运行服务执行 curl 协议 smoke，覆盖 WebDAV 基础读写、URL 编码空格路径、复制、移动和删除，并提前拒绝含空白、query、fragment、内嵌凭据的 `WEBDAV_URL` 和非 `0/1` 的 `CURL_INSECURE`，通过独立 safety test 纳入 `make scripts-check`
- WebDAV 兼容性报告表单，用于提交常见桌面、移动端、媒体播放器和命令行客户端的验证结果或客户端特定失败
- `scripts/check-release-tag.sh` 会在构建 release 产物前校验 release tag 是否为 `vMAJOR.MINOR.PATCH` 或语义化预发布 tag，并限制去掉 `v` 前缀后的 Docker 镜像 tag 长度不超过 128 个字符
- `scripts/release-readiness.sh` 在记录的完整验证目标之后发现非发布文档变更时默认失败；草稿摘要可显式使用 `--allow-post-validation-changes` 放行
- `scripts/release-readiness.sh` 要求四份 hardening 证据文档存在且记录一致的完整验证目标，避免发布前证据缺失被静默跳过
- `scripts/release-readiness.sh` 会检查双语 release notes 草稿记录当前完整验证目标，避免发布说明中的验证快照滞后
- `scripts/release-readiness.sh` 会要求 `CHANGELOG.md` 和 `CHANGELOG.en.md` 的发布清单包含文档检查、依赖安全检查和 Docker 构建烟测命令，避免关键本地门禁从最终发布核验中遗漏
- `scripts/release-readiness.sh` 会要求 Dependabot 配置覆盖 Go、Rust 数据面、Rust proto 生成器、Web npm、GitHub Actions 和 Docker 依赖更新入口，避免发布分支丢失依赖维护基线
- `scripts/release-readiness.sh` 会要求 `.github/workflows/ci.yml` 和 `.github/workflows/release.yml` 保留关键 CI、E2E、Docker smoke、release tag 校验、release artifact 校验和发布权限基线，避免核心自动化路径在发布前失效
- `scripts/release-readiness.sh` 会要求 `Makefile` 保留 `check`、`verify-changed`、`quick-check`、`security-check`、`docker-check` 和 `test-torture` 等核心本地门禁目标，避免 CI、发布清单和维护者文档引用的入口在发布前失效
- `scripts/release-readiness.sh` 会要求 `.github/workflows/torture.yml` 保留手动入口、定时入口、只读权限、`RUN_LIVE_FAULTS: '0'` 非破坏性开关和 `make test-torture` 执行入口，避免长期回归工作流在发布前失效
- `scripts/release-readiness.sh` 会要求关闭空白 Issue，并检查缺陷报告、使用问题、功能建议和 WebDAV 兼容性 Issue 表单保留敏感信息脱敏、诊断信息和安全影响提示，避免公开协作入口绕过安全提示
- `scripts/release-readiness.sh` 会检查安全策略和支持说明保留私密漏洞报告入口、禁止公开漏洞细节、dataplane 端口不外露、依赖安全检查和公网直连限制等关键提示
- `scripts/release-readiness.sh` 会要求发布清单和双语 release notes 保留 `mnemonas-doctor --public-domain`、`scripts/public-go-live-smoke.sh` 和 `cloud-firewall-checklist` 入口，避免公网部署环境复核从最终发布流程中遗漏
- `scripts/release-readiness.sh` 会拒绝不是当前 HEAD 祖先的 base ref，避免用旁支范围生成误导性的发布就绪摘要
- `scripts/release-readiness.sh` 会检查当前发布分支的本地提交标题是否符合 Conventional Commits，并拒绝遗留的 `fixup!` / `squash!` 临时提交
- `scripts/public-go-live-smoke.sh` 会先检查后端端口 TCP 可达性；`8080/9090/9091` 或自定义后端端口即使不返回 HTTP 状态，只要从外部网络可建立 TCP 连接就会失败
- `scripts/public-go-live-smoke.sh` 的 TCP 探测会按 `timeout`、`gtimeout` 顺序自动选择 GNU timeout 兼容命令，并支持通过 `TIMEOUT_BIN` 指定兼容替代命令
- `make test`、`make quick-check`、`make coverage`、torture 测试和 hardening 分组规划命令会使用 20 分钟 Go 包级超时，避免重负载 race 包被 Go 默认 10 分钟超时中断
- `scripts/check-doc-links.sh` 会要求备份指南保留恢复演练命令、30 天演练提醒、失败分类、保留演练产物、恢复摘要导出和“未恢复过不算验证”的说明，避免恢复可用性文档退化
- WebDAV COPY/MOVE 目标路径回归覆盖，验证绝对 path-reference 目标，并拒绝包括 `dav/path` 在内的裸相对目标
- `npm run typecheck` 覆盖前端应用、Playwright 规格和共享 E2E helper
- `.go-version`、`.nvmrc`、Go `toolchain` 与 Rust `rust-version` 共同记录本地开发工具链要求
- `.gitattributes` 统一文本换行并标记提交的生成文件，降低跨平台和评审噪声
- SECURITY.md 安全策略
- SUPPORT.md 支持渠道与维护边界说明
- pre-commit 配置（代码格式化、lint 检查）
- golangci-lint 配置
- .gitignore 完善

#### 文档完善
- Linux/systemd 部署指南，覆盖 ZFS/Btrfs/mdadm 分层、systemd 安装、网络、备份、升级和故障排查
- 备份指南（3-2-1 策略、内置本地备份任务、恢复演练、rclone/restic 配置、恢复流程）
- API 参考文档
- README 徽章和快速开始指南
- README、文档索引、主要专题文档、支持说明和安全策略提供中英文版本
- Docker 部署指南补充 Compose v2 安装、非 root UID/GID、可配置 `MNEMONAS_HTTP_PORT`、弱网构建策略和 dataplane 端口边界

### Changed
- `share.base_url` 校验会拒绝路径中编码后的查询或片段标记，避免公开分享基础 URL 被代理或浏览器解码成歧义地址
- `scripts/docker-smoke.sh` 会在启动容器前拒绝空值、以 `-` 开头或包含空白/控制字符的镜像引用，避免 Docker smoke 把镜像名误解释为 Docker 运行参数
- 容器 healthcheck 的 `MNEMONAS_HEALTHCHECK_URL` 覆盖值会拒绝嵌入凭据和 fragment，同时继续允许合法 query 探针参数
- Release archive 改为包含顶层目录，并随包附带 Web UI、安装/卸载脚本、诊断脚本、完整 docs 文档、公网访问 deploy 模板，以及预设匹配 release 镜像的 Docker Compose/env 模板
- 默认 `docker-compose.yml` 从源码构建 `mnemonas:local`，公开 release 镜像可按文档改用明确版本标签
- Docker Compose 宿主机 HTTP 端口改为通过 `.env` 中的 `MNEMONAS_HTTP_PORT` 配置
- CI 固定 protobuf 生成器和 `protoc 3.20.1`，并检查 `make proto` 后生成文件无漂移
- Rust CI/Makefile 检查覆盖 dataplane all-targets 和 `tools/proto-gen`
- Makefile 改为在 Go 目标运行时再解析包列表，避免 `make help` 等非 Go 目标在解析阶段触发 toolchain 下载，同时继续排除 `web/node_modules`
- 新增 `make go-packages`，集中 Go 包解析规则，供 CI、文档示例和安全扫描复用
- 新增 `make workflows-check` 并接入 CI，用 actionlint 检查 GitHub Actions 工作流配置
- 统一 README、开发与测试文档中的前端 Node.js engine 要求，匹配 `web/package.json`
- 安全策略文档补充 `make security-check NPM_AUDIT=1` 用法，避免误解前端依赖安全扫描默认行为
- CI 和 release 工作流增加最小权限、job 级权限收缩、并发控制和 job 超时，减少权限面、重复运行和挂起风险
- Release workflow 在创建 GitHub Release 前校验下载归档、checksums 和必需目标集合
- Release workflow 在构建归档和容器镜像前拒绝非语义化版本 release tag
- Release artifact verifier 在执行 checksum 前拒绝不安全的 checksum 路径、控制字符路径、空白字符路径、符号链接归档、下载目录中的未知条目、特殊归档条目、重复条目、归档成员控制字符路径、归档成员空白字符路径、反斜杠路径和歧义路径段，并会校验显式或从归档名推断出的 release version 是否符合 Docker/GHCR 镜像标签约束；发布后本地核验也支持通过 `--` 传入以 `-` 开头的 artifact 目录
- Release artifact verifier 成功时会输出已验证目标集合，便于发布后核对平台归档覆盖范围
- Release archive 随包附带 `SUPPORT.md`
- CI push/pull_request 触发分支覆盖 `main` 和 `master`，避免当前仓库默认分支未切换时漏跑检查
- pre-commit 的 `golangci-lint` 版本对齐 CI/Makefile 使用的 v2.11.4
- Release archive 随包附带 README、支持说明和安全策略的中英文版本
- 安全文档区分 Web UI 初始管理员密码与 WebDAV Basic Auth 自动密码
- 安全文档和 doctor 明确提示 dataplane `9090/9091` 不应被防火墙放行到不可信网络
- 新增公网云防火墙复核清单，覆盖常见云安全组、VPC 防火墙、IPv6 和端口转发误配置
- 备份文档补充运行中数据的一致性窗口和快照建议
- 设置页目录权限从原始规则文本框改为结构化规则编辑器，并增加按路径生成用户权限矩阵、相关分享影响、未保存规则预览和复核记录复制入口，降低家庭成员或小团队成员授权配置出错概率
- 设置页分享路径策略增加允许用户、允许组和允许角色字段，可限制非管理员账号创建或维护对应路径下的分享链接
- `make verify-changed` 将 Web Husky hooks 视为脚本变更，并在 Web 变更时运行前端 typecheck，覆盖未跟踪的 E2E helper 和配置文件
- 根目录示例配置注释统一为英文，`make verify-changed` 在 `mnemonas.example.toml` 变更时运行 `nasd --check-config`
- `make verify-changed` 在 `.env.example` 或 Compose 模板变更时运行 Docker 模板相关脚本 fixture
- `make verify-changed` 在 `.dockerignore` 变更时运行 Docker 构建，避免构建上下文规则漂移
- `make verify-changed` 总是按 worktree、staged 或 base 模式运行对应范围的 `git diff --check`，并在 `.go-version`、`.nvmrc` 或 `.golangci.yml`/`.golangci.yaml` 变更时选择对应的工具链检查
- `make verify-changed` 在 `deploy/public-access/` 公网模板变更时运行公网模板安全 fixture
- `make verify-changed` 在 `.github/dependabot.yml`、`.github/dependabot.yaml`、`codecov.yml` 或 `codecov.yaml` 变更时验证 YAML 配置语法
- WebDAV 设置脚本和开发辅助脚本明确展示 Basic、users 和 no-auth 模式，避免混淆生成凭据与用户账号挂载
- 优化 Files 页面表格列布局，新增操作列
- 优化 Vite 代理配置，添加 `/health` 端点代理
- 改进配置加载逻辑，支持配置路径传递

### Fixed
- 修复备份与前端诊断脱敏未覆盖百分号编码敏感参数名的问题，避免 `access%5Fkey`、`secret%2Dkey` 等错误文本泄漏凭据值。
- 修复设置 API 修改 `server.trusted_proxy_hops` 后，运行态请求来源和 HTTPS 转发语义识别未立即同步的问题
- 修复公网 go-live smoke 和 `mnemonas-doctor --public-domain` 会把全数字四段但超出 IPv4 范围的输入当作 DNS 主机名接受的问题；手动公网端口复核示例也补充总请求超时，避免半开放连接长时间阻塞。
- 修复 Web Husky pre-commit hook，使其解析仓库根目录、切换到 `web/`，并使用前端 lint-staged 配置
- 修复前端认证初始化：复用已有服务的 E2E 可显式跳过认证状态写入，隔离 E2E 默认失败而不是静默保存空认证状态
- 修复维护页移动端备份配置示例中长路径被代码块截断的问题
- 防止 systemd 安装和 `nasd` 静态文件发现误把 Vite 源码目录当成已构建 Web UI
- 修复 `.gitignore` / `.dockerignore` 中 `nasd` 规则过宽，避免忽略 `cmd/nasd` 下的新文件或 Docker 构建上下文
- 修复 Docker 运行镜像依赖 `apt-get` 安装健康检查工具的问题
- 修复 Docker/Rust 构建阶段需要系统 `protoc` 的问题
- 移除仓库中被跟踪的根目录 `nasd` 二进制、`coverage.out`、`.pids/` 和 `logs/` 构建/运行产物
- 修复 Files.tsx 语法错误（模板字面量、hook 调用）
- 修复 Trash.tsx useCallback 依赖警告
- 修复 utils.ts 控制字符正则 lint 错误
- 移除未使用的导入和变量
- 移除 Git 跟踪的构建产物

---

## [0.1.0] - 未发布

首个公开发布版本。

### Added

#### 核心功能
- **CAS 存储引擎**：基于 BLAKE3 哈希的内容寻址存储
- **CDC 分块**：使用 FastCDC 算法实现智能分块（256KB-4MB）
- **版本管理**：按策略自动保留适合版本化的文件历史，支持恢复
- **软删除**：删除操作仅移除引用，数据由 GC 异步清理

#### WebDAV 协议
- 覆盖 RFC 4918 核心读写方法（PROPFIND, GET, PUT, DELETE, MKCOL, COPY, MOVE）
- 虚拟锁实现（LOCK/UNLOCK）
- Basic Auth 认证
- 记录常见客户端兼容性矩阵，并在发布前后补充真实客户端回归

#### 性能优化
- PROPFIND 响应缓存（30 秒 TTL）
- 请求指标收集与统计
- 流式文件传输，文件大小主要受磁盘、客户端和反向代理限制影响

#### 运维功能
- 健康检查端点
- Scrub 数据完整性检查
- GC 垃圾回收
- 诊断信息导出

#### 部署
- Docker / Docker Compose 支持
- Linux / macOS 二进制分发
- TOML 配置文件

### 已知限制

- LOCK/UNLOCK 为虚拟实现，多客户端并发编辑同一文件时需注意
- Windows WebClient 需要修改注册表以支持 HTTP（非 HTTPS）连接
- 已支持用户、角色和用户主目录范围，但暂不支持细粒度 per-file ACL
- 不建议在没有 HTTPS 反向代理或 VPN 的情况下直接暴露到公网

### 兼容性

- **Go**: 1.25.11+
- **Rust**: 1.92+
- **Node.js**: `^20.19.0` 或 `>=22.12.0`
- **Docker**: 20.10+ 与 Compose v2 插件
- **支持平台**: Linux (x86_64, ARM64), macOS (Intel, Apple Silicon)

---

## 版本发布检查清单

发布新版本前应完成以下检查：

- [ ] 记录当前基线并保持工作树干净：`git status --short --branch`
- [ ] 变更感知完整验证通过：`GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master`
- [ ] 文档检查通过：`make docs-check`
- [ ] 脚本检查通过：`make scripts-check`
- [ ] 依赖安全检查通过：`make security-check NPM_AUDIT=1`
- [ ] Docker 构建和烟测通过：`make docker-check`
- [ ] 如果计划公网发布，在服务器运行 `sudo mnemonas-doctor --public-domain <domain>`，并按 [公网云防火墙复核清单](docs/cloud-firewall-checklist.md) 确认 DNS、防火墙、TLS 和云安全组
- [ ] 如果计划公网发布，从外部网络运行 `./scripts/public-go-live-smoke.sh <domain>`，确认 HTTPS、同域跳转和后端端口不可外露
- [ ] `./scripts/plan-hardening-commits.sh --fail-on-manual` 确认没有未归类路径
- [ ] 发布前就绪摘要通过：`./scripts/release-readiness.sh`
- [ ] 更新 CHANGELOG.md、CHANGELOG.en.md、README 版本引用和 [发布说明草稿](docs/release-notes.md)
- [ ] 创建 Git tag：`git tag -a v0.1.0 -m "Release v0.1.0"`
- [ ] 推送 tag：`git push origin v0.1.0`
- [ ] GitHub Release 包含：
  - 版本说明（从 CHANGELOG 复制）
  - checksums
  - 二进制文件（Linux x86_64, ARM64, macOS）
  - Docker 镜像标签
- [ ] 发布后下载 GitHub Release 产物，并运行 `./scripts/verify-release-artifacts.sh --version <tag> --repository seanbao/mnemonas --require-targets --check-image <artifact-dir>`，验证 release 产物、checksums 和容器镜像标签
- [ ] 发布后验证 release 归档安装、Docker release 镜像启动和公开文档链接

---

## 版本号规则

- **MAJOR** (X.0.0)：不兼容的 API 变更
- **MINOR** (0.X.0)：向后兼容的功能新增
- **PATCH** (0.0.X)：向后兼容的问题修复

### 预发布版本

- `0.1.0-alpha.1`：Alpha 版本，功能不完整
- `0.1.0-beta.1`：Beta 版本，功能完整但可能有 bug
- `0.1.0-rc.1`：Release Candidate，准备正式发布

---

[Unreleased]: https://github.com/seanbao/mnemonas/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/seanbao/mnemonas/releases/tag/v0.1.0
