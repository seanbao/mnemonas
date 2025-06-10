# 变更记录

[English](CHANGELOG.en.md) | 简体中文

所有重要变更都会记录在此文件中。

本项目遵循 [语义化版本 2.0.0](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

#### Web UI 功能
- **Dashboard 仪表盘**
  - 系统概览（存储使用、文件数量、版本数量）
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

- **Activity 活动日志**
  - 全操作审计记录
  - 按时间/操作类型筛选
  - 操作详情查看
  - 活动统计报表
  - 磁盘健康异常系统事件记录
  - 存储页显示文件系统类型、挂载点和设备/数据集来源

- **Users 用户管理**
  - 用户列表与状态
  - 创建/编辑/删除用户
  - 用户主目录与容量配额编辑
  - 密码重置
  - 启用/禁用用户

- **ShareAccess 分享管理**
  - 创建分享链接
  - 密码保护设置
  - 有效期配置
  - 访问统计
  - 分享链接访问页面

- **Settings 系统设置**
  - 服务器配置（监听地址、端口）
  - 存储路径配置（数据目录、元数据目录、临时目录）
  - 版本保留策略（最大版本数、保留时间、空间阈值、GC 间隔）
  - WebDAV 配置（启用开关、URL 前缀、只读模式、用户认证）
  - CDC 分块参数说明
  - 数据面连接状态
  - 周期 Scrub 调度开关、常规间隔、失败重试间隔和最大重试次数配置
  - 公网访问向导和安全自检入口，辅助配置 HTTPS 反向代理、受信代理跳数和分享域名
  - 公网访问向导桌面与移动端 E2E 回归覆盖

- **Health 健康状态**
  - 系统运行时间
  - 数据面连接状态
  - 存储健康检查
  - 磁盘 SMART、温度、介质磨损、设备缺失和序列号漂移健康状态
  - 周期 Scrub 调度、最近状态和失败重试状态
  - SMB 预览运行态提示，避免把已配置共享误判为可挂载服务

- **Maintenance 系统维护**
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
  - 用户级容量配额，非管理员 Web/API 上传、复制和回收站恢复超限时返回 `QUOTA_EXCEEDED`，并可通过 Webhook/Telegram/SMTP 发送 `quota_exceeded` 告警事件
  - `storage.directory_quotas` 目录级硬限制和存储页目录配额用量展示，Web/API 上传、复制、移动、回收站恢复、版本恢复以及 WebDAV PUT/COPY/MOVE 会在写入前检查命中的目录配额
  - 用户组和 `storage.directory_access_rules` 目录读写授权，可按用户、用户组或角色授予共享目录权限；Web/API、WebDAV 用户模式、搜索、分享、收藏、回收站和活动过滤使用同一权限判定
  - WebDAV 支持 `auth_type = "users"`，客户端可使用 MnemoNAS 用户账号挂载；非管理员挂载根目录映射到自己的 `home_dir`，guest 只读，PUT/COPY 写入遵守用户配额
  - 密码重置
  - 状态切换

- **分享 API**
  - 分享链接 CRUD
  - 密码验证
  - 公开访问

- **活动日志 API**
  - 活动记录
  - 活动查询
  - 统计接口
  - Scrub 数据校验系统事件记录

- **系统设置 API**
  - 配置读取
  - 配置更新
  - 公网访问安全自检
  - 公网访问向导增加证书续期检查和失败排障提示
  - 磁盘健康配置热更新
  - 周期 Scrub 调度配置热更新
  - 告警 Webhook、Telegram 与 SMTP 邮件通知配置热更新

- **备份与恢复 API**
  - 配置化本地备份任务与 restic/rclone 命令型远端目标
  - 立即执行备份
  - 轻量定时调度、自动备份时间窗、成功快照保留策略和任务健康状态
  - 最近快照恢复演练与 manifest 校验；本地快照、restic 仓库和 rclone 远端可先生成恢复预览，再恢复到安全目录；支持最多 20 个条目的批量恢复预览与顺序执行，并逐项返回恢复和只读校验结果；恢复前预检目标隔离、目录状态、容量、备份内容和配置处理，失败预检会阻止恢复；恢复后只读校验、切换清单、回滚清单和恢复报告导出；远端保留策略检测与周期化恢复演练提醒；恢复演练缺失/过期时发送限频告警；恢复演练历史、成功率摘要与失败归因；恢复结果审计与历史记录；restic/rclone 任务支持远端一致性校验
  - 备份成功后自动检测保留策略，维护页可手动触发“检查保留”；本地任务统计快照范围，restic 解析 `snapshots --json`，rclone 解析 `lsjson`
  - 恢复后的只读校验报告持久化为 `last_restore_verify`，刷新维护页后仍可审计最近一次恢复检查
  - 备份失败、恢复演练失败、保留策略检测失败和备份警告事件接入 Webhook/Telegram/SMTP 告警

- **磁盘健康 API**
  - `smartctl --json` 采集 SMART、自检状态、温度和通电时间
  - 解析 NVMe 介质磨损、可用备用容量、critical warning、介质错误和常见 ATA 寿命属性
  - 设备缺失、SMART 失败、温度过高和序列号不匹配会标记异常，并写入活动日志或通过 Webhook/Telegram/SMTP 发送 `disk_health` 事件

- **Scrub 数据完整性事件**
  - 手动 Scrub 完成后写入活动日志
  - `[maintenance.scrub]` 支持后台周期 Scrub 和失败后的限次自动重试
  - Settings API 和 Web 设置页可热更新周期 Scrub 调度配置
  - 诊断接口返回周期 Scrub 配置、最近执行状态和失败重试计数
  - Scrub 失败、发现对象异常或结果持久化不完整时，通过 Webhook/Telegram/SMTP 发送 `scrub_run` 事件

- **安全告警事件**
  - 登录失败触发限流时发送限频的 `login_rate_limited` warning 事件
  - 登录限流告警只包含用户名和客户端地址，不包含密码或 token

- **SMB 预览诊断**
  - 诊断接口和诊断导出返回脱敏的 `smb` 预览状态、共享数量和运行态说明
  - `nasd --check-config` 在 `smb.enabled=true` 时提示当前版本不会启动 SMB/Samba 监听器

#### 项目工程化
- GitHub Actions CI/CD 工作流（Go/Rust/Frontend 测试、Docker 构建）
- Release 自动化工作流（多平台二进制构建、Docker 镜像发布）
- Ubuntu/systemd 安装脚本，可将 release 包安装为 `mnemonas` 与 `mnemonas-dataplane` 服务
- `mnemonas-doctor` 部署诊断脚本，检查二进制、配置、systemd、健康端点、端口、存储挂载、备份目录、公网 HTTPS 证书状态、HTTP 到 HTTPS 跳转，并提示云安全组人工复核项
- `mnemonas-public-setup` 公网 HTTPS 反向代理配置助手
- Traefik 和 Cloudflare Tunnel 公网访问模板，并通过脚本检查避免开放后端和 dataplane 端口
- `mnemonas-uninstall-systemd` 卸载脚本，默认保留配置和数据，删除数据需要显式确认
- Docker Compose 启动前预检脚本，检查 Compose v2、Buildx、端口、目录权限、磁盘空间和已有配置
- Docker 镜像内置 `mnemonas-healthcheck` 健康检查二进制，不再依赖运行时 `curl`
- `tools/proto-gen` Rust protobuf 生成器，普通 dataplane/Docker 构建不再依赖系统 `protoc`
- systemd/Docker 脚本模拟测试，并接入 CI 的脚本校验流程
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
- Release archive 改为包含顶层目录，并随包附带 Web UI、安装/卸载脚本、诊断脚本和完整 docs 文档
- 默认 `docker-compose.yml` 从源码构建 `mnemonas:local`，公开 release 镜像可按文档改用明确版本标签
- Docker Compose 宿主机 HTTP 端口改为通过 `.env` 中的 `MNEMONAS_HTTP_PORT` 配置
- CI 固定 protobuf 生成器和 `protoc 3.20.1`，并检查 `make proto` 后生成文件无漂移
- Rust CI/Makefile 检查覆盖 dataplane all-targets 和 `tools/proto-gen`
- Makefile 改为在 Go 目标运行时再解析包列表，避免 `make help` 等非 Go 目标在解析阶段触发 toolchain 下载，同时继续排除 `web/node_modules`
- 新增 `make go-packages`，集中 Go 包解析规则，供 CI、文档示例和安全扫描复用
- 新增 `make workflows-check` 并接入 CI，用 actionlint 检查 GitHub Actions workflow 配置
- 统一 README、开发与测试文档中的前端 Node.js engine 要求，匹配 `web/package.json`
- 安全策略文档补充 `make security-check NPM_AUDIT=1` 用法，避免误解前端审计默认行为
- CI 和 release 工作流增加最小权限、job 级权限收缩、并发控制和 job 超时，减少权限面、重复运行和挂起风险
- Release archive 随包附带 `SUPPORT.md`
- CI push/pull_request 触发分支覆盖 `main` 和 `master`，避免当前仓库默认分支未切换时漏跑检查
- pre-commit 的 `golangci-lint` 版本对齐 CI/Makefile 使用的 v2.11.4
- Release archive 随包附带 README、支持说明和安全策略的中英文版本
- 安全文档区分 Web UI 初始管理员密码与 WebDAV Basic Auth 自动密码
- 安全文档和 doctor 明确提示 dataplane `9090/9091` 不应被防火墙放行到不可信网络
- 新增公网云防火墙复核清单，覆盖常见云安全组、VPC 防火墙、IPv6 和端口转发误配置
- 备份文档补充运行中数据的一致性窗口和快照建议
- 优化 Files 页面表格列布局，新增操作列
- 优化 Vite 代理配置，添加 `/health` 端点代理
- 改进配置加载逻辑，支持配置路径传递

### Fixed
- 修复通过设置 API 修改 `server.trusted_proxy_hops` 后，运行态请求来源和 HTTPS 转发语义识别未立即同步的问题
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

- **Go**: 1.25.9+
- **Rust**: 1.92+
- **Node.js**: `^20.19.0` 或 `>=22.12.0`
- **Docker**: 20.10+ 与 Compose v2 插件
- **支持平台**: Linux (x86_64, ARM64), macOS (Intel, Apple Silicon)

---

## 版本发布检查清单

发布新版本前，请确认：

- [ ] 快速检查通过：`make quick-check`
- [ ] 部署脚本检查通过：`make scripts-check`
- [ ] 全量测试通过：`make test`
- [ ] 依赖安全检查通过：`make security-check`
- [ ] 更新 CHANGELOG.md 和 CHANGELOG.en.md
- [ ] 更新 README.md 中的版本号（如有）
- [ ] 创建 Git tag：`git tag -a v0.1.0 -m "Release v0.1.0"`
- [ ] 推送 tag：`git push origin v0.1.0`
- [ ] GitHub Release 包含：
  - 版本说明（从 CHANGELOG 复制）
  - 二进制文件（Linux x86_64, ARM64, macOS）
  - Docker 镜像标签
- [ ] 更新文档站点（如有）

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
