# 发布说明草稿

[English](release-notes.en.md) | 简体中文

本文档为下一次公开发布的发布说明草稿。最终发布时应以对应 tag、CI 结果、Release 产物和部署验证结果为准。

## 摘要

本轮发布候选重点强化 MnemoNAS 作为自托管 NAS 的稳定性、公开访问安全边界、部署可验证性和文档可维护性。当前硬化分支按风险面拆分为可审阅提交，并已通过完整分支范围验证。

## 主要变化

- 加强路径、归档下载、WebDAV、公开分享、工作区、CAS 和备份恢复相关边界检查，覆盖符号链接、路径穿越、百分号编码点段、编码后的查询或片段标记、百分号编码敏感参数名、控制字符和回滚错误情况。
- 升级 `golang.org/x/image` 到 `v0.43.0`，修复缩略图解码路径命中的 TIFF/WebP 依赖安全告警；同步刷新间接 `golang.org/x/text` 版本。
- 完善认证、用户、主目录、目录配额、目录访问规则、分享策略和会话安全默认值的后端与前端覆盖。
- 加固邮件告警通知出口，消息头和 SMTP envelope 会清理控制字符，降低内部调用或后续扩展绕过配置校验后的头部注入风险。
- 提升 Web 可见质量，核心页面、公开入口、移动端布局、基础可访问性、运行时错误、失败请求和破碎可见文本已纳入 Playwright 扫描。首页首次部署检查和登录页会基于 setup 状态提示认证关闭、分享启用且认证关闭、WebDAV 匿名访问和 `allow_unsafe_no_auth` 开启的部署风险。
- 加固 systemd、Docker、反向代理、公网访问模板、doctor、公网域名就绪校验、release package 和 release artifact 验证路径；Docker preflight 会在 Compose 检查前拒绝空值、以 `-` 开头、包含空白或控制字符、URL 形态、无效 `sha256` digest 或不兼容 Docker tag 的 `MNEMONAS_IMAGE`，且 URL 形态诊断不回显凭据、query 或 fragment；Docker quickstart、preflight 和容器入口还会拒绝配置中包含父目录段或控制字符的 `auth.users_file` 容器路径，避免将 `/data/../...` 映射为宿主数据目录外的初始密码读取路径；Docker smoke 会在启动容器前拒绝以 `-` 开头或包含空白/控制字符的镜像引用；容器 healthcheck 对无效目标 URL 的诊断日志只输出脱敏后的 URL 形状，不写入嵌入凭据、原始查询字符串或 fragment；反向代理安装脚本对无效 `MNEMONAS_UPSTREAM_HOST` 只输出主机格式约束，不回显原始 host 值或误粘贴 URL 中的凭据、query、fragment；`mnemonas-doctor --public-domain` 对无效 `share.base_url` 诊断只输出脱敏 URL 形状，不回显误配置中的凭据、query 或 fragment；公网 go-live smoke 和 doctor 会拒绝 `localhost`、IP 地址和全数字四段主机名，给手动端口复核命令设置连接和总耗时上限，并拒绝空白的自定义后端目标列表和歧义目标路径，避免跳过端口暴露检查或生成不明确的后端探测 URL；公网 go-live smoke 对无效自定义后端目标和错误 HTTP 跳转只输出脱敏后的目标形状，不回显 query、fragment、userinfo 或控制字符路径内容；Release workflow 会在创建 GitHub Release 前校验归档、checksums、必需目标集合、下载目录未知条目、归档条目类型、重复条目、控制字符路径、空白字符路径、归档成员控制字符路径、归档成员空白字符路径、反斜杠路径、歧义路径、GHCR 仓库名和已推送的容器镜像标签；release artifact verifier 支持通过 `--` 传入以 `-` 开头的本地产物目录，并对下载目录、checksum 清单和归档成员中的控制字符路径使用 shell-safe 诊断表示，避免发布后核验路径被 shell 内建命令按选项解释或把原始控制字符写入验收日志；发布后统一核验入口会把以 `-` 开头的显式 artifact 目录规范化为本地路径，并在下载前拒绝非法仓库名。
- systemd 安装和卸载脚本在拒绝包含控制字符的路径、地址、端口或账号参数时，会使用 shell-safe 诊断表示，避免失败日志写入原始控制字符或形成多行注入。
- 基准测试、E2E、故障注入脚本、反向代理安装向导和双语反向代理文档的 WebDAV PROPFIND 示例均通过临时 curl config 传递 WebDAV Basic Auth 凭据，避免密码出现在 `curl` 命令参数；开发文档和反向代理文档均不再保留直接把 WebDAV 密码放入 `curl -u` 的手动示例，并由脚本测试和文档契约覆盖。
- 公网 go-live smoke 会在 TCP 探测中按 `timeout`、`gtimeout` 顺序自动选择 GNU timeout 兼容命令，并支持用 `TIMEOUT_BIN` 指定兼容替代命令。
- Release tag 会在产物构建前校验，必须使用 `vMAJOR.MINOR.PATCH` 或 `v1.2.3-rc.1` 这类语义化预发布形式，并且去掉 `v` 前缀后的 Docker 镜像 tag 长度不能超过 128 个字符；发布后 artifact verifier 会复用同一版本校验逻辑，对显式或归档名推断出的版本应用同一约束。
- 新增可复跑的 WebDAV curl 协议 smoke，可对已运行服务验证基础读写、URL 编码空格路径、复制、移动和删除操作；脚本会提前拒绝含空白、query、fragment、内嵌凭据、反斜杠、编码斜杠、编码反斜杠或 `.`/`..` 路径段的 `WEBDAV_URL`，并拒绝非 `0/1` 的 `CURL_INSECURE`，相关契约通过脚本门禁覆盖。
- 新增可复跑的备份恢复演练 smoke 入口，可对已运行服务按显式备份任务 ID 执行任务列表读取、单任务读取、立即备份、保留策略检查、恢复演练和恢复报告下载；脚本不创建或删除备份任务，并提前拒绝含空白、query、fragment、内嵌凭据、反斜杠、编码斜杠/反斜杠、空路径段或点段的 API URL。
- 新增发布后上线总核验入口 `scripts/release-go-live-check.sh`，按顺序执行发布就绪摘要、GitHub Release/GHCR 产物核验、公网 `mnemonas-doctor --public-domain`、外部网络 go-live smoke，以及备份恢复演练 smoke；脚本会在任何 helper 启动前校验 release tag、仓库名和公网域名，并把大写或尾点域名规范化后传给公网检查；备份演练需要显式 API URL 和任务 ID，或显式跳过并在发布记录中保留该事实。
- 新增 WebDAV 兼容性报告表单，用于收集 Finder、Windows File Explorer、移动端文件管理器、媒体播放器和命令行客户端的验证结果或客户端特定失败。
- 维护页恢复完成后可复制恢复切换记录，内容包含恢复目标、只读校验、切换步骤、切换前确认和回滚清单；恢复报告会基于原始恢复目标匹配结果，在最近一次恢复已完成但匹配只读校验缺失、只读校验早于恢复完成、只读校验不属于当前恢复目标或只读校验状态不能作为当前目标证据时给出明确 findings，避免把陈旧、跨目标或不可用校验误读为当前恢复已验证；批量恢复结果会列出跨目录切换候选和冲突处置记录，并在可复制结果记录中写入任务名称、备份目标、保留策略状态、候选目录、只读校验复核结论、校验错误详情、冲突处置建议和配置文件保留要求，便于记录到工单或值班流程。
- 设置页目录权限用户矩阵和未保存规则预览可复制权限复核记录，内容包含路径、用户读写判定、命中规则和相关分享影响，并会保留后端持久化近期复核历史；服务端历史不可用时回退当前浏览器记录。
- 分享路径策略可按用户、用户组或角色限制允许创建和维护分享链接的认证调用方；管理员保留修复既有分享的管理权限。
- 分享、版本历史、回收站和维护页的关键处置入口会写入活动复核记录，覆盖分享停用、删除、重新启用、策略更新、版本恢复、回收站恢复和备份恢复执行结果；活动页复核历史在处置后会立即显示符合当前筛选的新记录，便于追踪误分享、误删和恢复处置闭环。
- 收紧发布就绪摘要：记录的完整验证目标之后如出现已提交或未提交的非发布文档变更，`release-readiness` 默认失败，并要求刷新完整验证或显式草稿放行；草稿放行非发布文档变更时会输出 `validation-warning`，避免被误读为正式发布就绪。
- `release-readiness` 现在要求四份 hardening 证据文档都存在，并且都记录同一个完整验证目标，避免发布前证据缺失被静默跳过。
- `release-readiness` 还会要求双语 hardening progress 台账在 `make release-readiness` 记录中写入同一个完整验证目标，避免完整验证证据刷新后发布就绪摘要仍停留在旧目标。
- `release-readiness` 还会检查双语 release notes 草稿记录当前完整验证目标，避免发布说明中的验证快照滞后。
- `release-readiness` 会要求双语 release notes 的发布后下载和 artifact verifier 示例使用 `<tag>` 占位，避免首次发布前把固定版本号写入可复制命令。
- `release-readiness` 会要求 `CHANGELOG.md` 和 `CHANGELOG.en.md` 的发布清单包含文档检查、依赖安全检查、Docker 构建烟测、所选发布 tag 校验和发布脚本回归命令，并保留 L1/L1+ 发布候选定位、非唯一长期副本和外部备份边界，避免最终发布核验遗漏关键本地门禁或数据安全限制。
- `release-readiness` 会要求 Dependabot 配置覆盖 Go、Rust 数据面、Rust proto 生成器、Web npm、GitHub Actions 和 Docker 依赖更新入口，避免发布分支丢失依赖维护基线。
- `release-readiness` 会要求 `.github/workflows/ci.yml` 和 `.github/workflows/release.yml` 保留关键 CI、E2E、Docker smoke、release tag 校验、release artifact 上传/下载、checksums 生成与发布、带版本和仓库绑定的 release artifact 校验、发布前镜像校验、release job 依赖和发布权限基线，避免核心自动化路径在发布前失效。
- `release-readiness` 会要求 `Makefile` 保留 `check`、`verify-changed`、`release-readiness`、`quick-check`、`security-check`、`docker-check` 和 `test-torture` 等核心本地门禁目标，避免 CI、发布清单和维护者文档引用的入口在发布前失效。
- `release-readiness` 会要求 `.github/workflows/torture.yml` 保留手动入口、定时入口、只读权限、`RUN_LIVE_FAULTS: '0'` 非破坏性开关和 `make test-torture` 执行入口，避免长期回归工作流在发布前失效。
- `release-readiness` 会要求关闭空白 Issue，并检查缺陷报告、使用问题、功能建议和 WebDAV 兼容性 Issue 表单保留敏感信息脱敏、诊断信息和安全影响提示，避免公开协作入口绕过安全提示。
- `release-readiness` 会检查安全策略和支持说明保留私密漏洞报告入口、禁止公开漏洞细节、dataplane 端口不外露、依赖安全检查和公网直连限制等关键提示。
- `release-readiness` 会要求发布清单和双语 release notes 保留 `mnemonas-doctor --public-domain`、`scripts/public-go-live-smoke.sh`、`scripts/backup-restore-drill-smoke.sh`、`scripts/release-go-live-check.sh` 和 `cloud-firewall-checklist` 入口，避免公网部署环境复核、发布后上线总核验和恢复演练入口从最终发布流程中遗漏。
- `release-readiness` 会拒绝不是当前 HEAD 祖先的 base ref，避免用旁支范围生成误导性的发布就绪摘要。
- Go 测试入口现在保留 20 分钟包级超时，避免重负载 race 包在完整分支验证中被 Go 默认 10 分钟超时中断。
- 文档检查会拒绝 API 示例中可复制的 `?path=/...` 裸路径查询，要求恢复和收藏检查等 `path` 查询示例使用 `%2F...` 编码形式。
- 文档检查会要求双语 release notes 发布前验证清单中的 Playwright E2E、前端单测数量、Docker image 和 Docker smoke 端口与 hardening 审查摘要中的最新完整验证证据一致，避免验证证据刷新后发布说明局部数据滞后。
- 文档检查会要求双语 Docker 部署指南保留发布后 `verify-published-release.sh` 命令、版本和仓库参数、可选 artifact 目录、镜像 manifest 重试参数、`--skip-image-check`、空目录要求、dash-prefixed artifact 目录和仓库名下载前校验说明，避免发布后核验说明退化。
- 文档检查会要求安全加固指南的公网部署清单保留初始密码、WebDAV 认证、doctor、公网防火墙、匿名 WebDAV、直连后端和 dataplane 暴露等关键复核项。
- 文档检查会要求备份指南保留恢复演练命令、30 天演练提醒、失败分类、保留演练产物、恢复摘要导出和“未恢复过不算验证”的说明，避免恢复可用性文档退化。
- 存储和配置文档明确 FastCDC API 属于 Rust 数据面能力，当前版本历史仍使用整对象 CAS 快照，不按 CDC 分块引用计数；文档检查会拒绝回退为块级版本去重的过度承诺。
- 精简并同步中英文文档，补齐部署、配置、FAQ、路线图、安全、硬化进度和发布前审查入口。

## 发布产物

Release workflow 预期生成以下产物：

- Linux x86_64 / ARM64 二进制归档。
- macOS Intel / Apple Silicon 手动运行归档。
- `checksums.txt`。
- GHCR 容器镜像标签。

归档内应包含顶层目录、`nasd`、`dataplane`、Web UI 静态资源、systemd 安装/卸载脚本、doctor、Docker Compose 模板、`.env.example`、部署模板和中英文文档。归档内 `.env.example` 应预设同一 release tag 的 GHCR 镜像。

## 发布前验证

当前硬化分支已有以下验证证据；最终发布前应以最新 tag、Release workflow 结果和必要的环境验证为准：

最近本地完整验证快照：验证目标 `dd07c2ee5a9f`，`GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master` 通过，覆盖 diff 空白、密钥泄漏扫描、workflow/YAML/脚本门禁、恢复报告基于原始恢复目标匹配结果，在最近一次恢复已完成但匹配只读校验缺失、只读校验早于恢复完成、只读校验不属于当前恢复目标或只读校验状态不能作为当前目标证据时给出明确 findings、首页首次部署检查和登录页基于 setup 状态提示认证关闭、分享启用且认证关闭、WebDAV 匿名访问和 `allow_unsafe_no_auth` 开启的部署安全风险、Activity 复核处置后把符合当前历史筛选的更新记录即时并入列表缓存、反向代理 WebDAV 验证文档契约门禁、hardening progress 整体状态边界文档契约门禁、发布后核验入口对以 `-` 开头的显式 artifact 目录和非法仓库名下载前失败的回归、Docker 部署指南发布后核验文档契约扩展、发布后上线总核验入口和模拟回归、release-readiness 发布清单摘要范围门禁、CHANGELOG 已知限制保留 L1/L1+ 发布候选定位、非唯一长期副本和外部备份边界的 release-readiness 门禁增量、Release workflow 结构门禁、hardening progress 中 `make release-readiness` 行级验证目标门禁、`make release-readiness` 入口基线、历史最小配置加载后回填当前默认值的配置兼容性回归、分享创建执行结果记录增量、分享策略更新执行结果记录增量、`make check`、工具链一致性、Go/Rust/frontend 依赖安全扫描、示例配置、public-access 模板、proto 再生成稳定性、Rust fmt/test/clippy、proto-gen fmt/test/clippy、前端 lint/typecheck/unit/build、Playwright 379 个 E2E 用例、Docker build、Docker image `sha256:74b496b23c78c81b2ef2595c258ee71db4257d3b16d4b38c0258e464cdb60bf6` 和 Docker smoke。Docker smoke 使用 Docker 自动分配的 loopback 端口 `http://127.0.0.1:32807`。

- `GOTOOLCHAIN=local ./scripts/verify-changed.sh`
- `GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master`
- `make scripts-check`
- `make docs-check`
- `make security-check NPM_AUDIT=1`
- `make docker-check`
- `make release-readiness`
- `sudo mnemonas-doctor --public-domain <domain>`
- `./scripts/public-go-live-smoke.sh <domain>`
- `./scripts/backup-restore-drill-smoke.sh`
- `./scripts/release-go-live-check.sh`
- `docs/cloud-firewall-checklist.md`
- `./scripts/check-release-tag.sh <tag>`
- `./scripts/test-release-tag.sh`
- `./scripts/test-release-package.sh`
- `./scripts/test-release-artifacts.sh`
- Public go-live TCP reachability test：`scripts/test-public-go-live-smoke.sh`
- Backup restore-drill smoke safety test：`scripts/test-backup-restore-drill-smoke.sh`
- Release artifact dash-prefixed directory test：`scripts/test-release-artifacts.sh`
- Docker quickstart safety test：`scripts/test-docker-quickstart.sh`
- Docker preflight safety test：`scripts/test-docker-preflight.sh`
- Docker container startup safety test：`scripts/test-docker-start.sh`
- Docker smoke safety test：`scripts/test-docker-smoke.sh`
- WebDAV curl smoke safety test：`scripts/test-webdav-client-smoke.sh`
- Release workflow 增量验证：`make workflows-check`、`make scripts-check`、`./scripts/check-secret-leaks.sh`、`make toolchains-check`、`git diff --check`
- Playwright E2E：`379 passed`
- 前端单测：`3124 passed`
- Docker build 和 `scripts/docker-smoke.sh`

最终发布前如代码、脚本、配置、文档或 workflow 再次变更，应重跑对应验证。

## 发布后核验

发布 tag 后，应优先运行统一上线核验入口：

```bash
./scripts/release-go-live-check.sh \
  --version <tag> \
  --domain nas.example.com \
  --repository seanbao/mnemonas \
  --artifact-dir dist/release-check \
  --backup-api-url https://nas.example.com/api/v1 \
  --backup-job-id external-disk \
  --cookie-file cookies.txt
```

如本次发布无法执行备份恢复演练，必须显式传入 `--skip-backup-restore-drill`，并在发布记录中标记为未形成完整恢复证据。
只核验 GitHub Release 产物时，也可单独执行：

```bash
mkdir -p dist/release-check
./scripts/verify-published-release.sh \
  --version <tag> \
  --repository seanbao/mnemonas \
  --artifact-dir dist/release-check
```

随后应完成至少一次归档安装 smoke、Docker release 镜像启动 smoke、公开文档链接检查，以及公网部署环境的 `mnemonas-doctor --public-domain`、外部网络 `public-go-live-smoke.sh`、DNS、防火墙、TLS 和云安全组复核。
显式 `--artifact-dir` 可以使用以 `-` 开头的相对路径；仓库名会在下载前校验为 GHCR 兼容的小写 `owner/repo`。

## 已知限制

- 当前发布候选定位为已通过完整本地验证的 L1 私有文件云盘和 L1+ 公网安全入口基础，不应作为重要数据的唯一长期副本；生产使用仍应保留外部备份，并继续积累真实介质、远端恢复处置、跨版本升级和回退记录。
- SMB/Samba 可挂载运行时仍未启用；当前仅保留配置、诊断和运行态提示。
- `LOCK` / `UNLOCK` 为 WebDAV 兼容性虚拟实现，多客户端并发编辑同一文件时仍应由客户端或上层流程控制冲突。
- 真实公网部署依赖具体 DNS、防火墙、TLS、反向代理和云厂商安全组配置，模板和 doctor 无法替代环境级复核。
- 如未来版本引入不可逆数据迁移，回退应按对应 release note 或备份恢复流程处理。

## 维护者发布清单

- 确认 `CHANGELOG.md` 和 `CHANGELOG.en.md` 已覆盖本次发布。
- 确认本草稿已按最终 tag、验证结果和产物名称更新。
- 确认 `git status --short --branch` 干净。
- 确认 `./scripts/plan-hardening-commits.sh --fail-on-manual` 没有待分组路径。
- 运行 `make release-readiness`，确认提交标题、临时 `fixup!` / `squash!` 提交、hardening 验证证据、发布文档命令、公网部署复核命令、安全策略、Dependabot 基线、CI/Release workflow 基线、Makefile 核心本地门禁目标基线、torture workflow 基线、Issue 表单安全提示和 community health 文件均通过检查。
- 创建并推送 tag 后，确认 Release workflow 成功。
- 发布后运行 `./scripts/release-go-live-check.sh`，并记录产物核验、公网 smoke 和备份恢复演练结果。
