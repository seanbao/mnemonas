# 发布说明草稿

[English](release-notes.en.md) | 简体中文

本文档为下一次公开发布的发布说明草稿。最终发布时应以对应 tag、CI 结果、Release 产物和部署验证结果为准。

## 摘要

本轮发布候选重点强化 MnemoNAS 作为自托管 NAS 的稳定性、公开访问安全边界、部署可验证性和文档可维护性。当前硬化分支按风险面拆分为可审阅提交，并已通过完整分支范围验证。

## 主要变化

- 加强路径、归档下载、WebDAV、公开分享、工作区、CAS 和备份恢复相关边界检查，覆盖符号链接、路径穿越、百分号编码点段、编码后的查询或片段标记、百分号编码敏感参数名、控制字符和回滚错误情况。
- 完善认证、用户、主目录、目录配额、目录访问规则、分享策略和会话安全默认值的后端与前端覆盖。
- 加固邮件告警通知出口，消息头和 SMTP envelope 会清理控制字符，降低内部调用或后续扩展绕过配置校验后的头部注入风险。
- 提升 Web 可见质量，核心页面、公开入口、移动端布局、基础可访问性、运行时错误、失败请求和破碎可见文本已纳入 Playwright 扫描。
- 加固 systemd、Docker、反向代理、公网访问模板、doctor、公网域名就绪校验、release package 和 release artifact 验证路径；Docker preflight 会在 Compose 检查前拒绝空值、以 `-` 开头、包含空白或控制字符、URL 形态、无效 `sha256` digest 或不兼容 Docker tag 的 `MNEMONAS_IMAGE`，且 URL 形态诊断不回显凭据、query 或 fragment；Docker quickstart、preflight 和容器入口还会拒绝配置中包含父目录段或控制字符的 `auth.users_file` 容器路径，避免将 `/data/../...` 映射为宿主数据目录外的初始密码读取路径；Docker smoke 会在启动容器前拒绝以 `-` 开头或包含空白/控制字符的镜像引用；容器 healthcheck 对无效目标 URL 的诊断日志只输出脱敏后的 URL 形状，不写入嵌入凭据、原始查询字符串或 fragment；反向代理安装脚本对无效 `MNEMONAS_UPSTREAM_HOST` 只输出主机格式约束，不回显原始 host 值或误粘贴 URL 中的凭据、query、fragment；`mnemonas-doctor --public-domain` 对无效 `share.base_url` 诊断只输出脱敏 URL 形状，不回显误配置中的凭据、query 或 fragment；公网 go-live smoke 和 doctor 会拒绝 `localhost`、IP 地址和全数字四段主机名，给手动端口复核命令设置连接和总耗时上限，并拒绝空白的自定义后端目标列表和歧义目标路径，避免跳过端口暴露检查或生成不明确的后端探测 URL；Release workflow 会在创建 GitHub Release 前校验归档、checksums、必需目标集合、下载目录未知条目、归档条目类型、重复条目、控制字符路径、空白字符路径、归档成员控制字符路径、归档成员空白字符路径、反斜杠路径、歧义路径和 GHCR 仓库名；release artifact verifier 支持通过 `--` 传入以 `-` 开头的本地产物目录，避免发布后核验路径被 shell 内建命令按选项解释。
- Release tag 会在产物构建前校验，必须使用 `vMAJOR.MINOR.PATCH` 或 `v1.2.3-rc.1` 这类语义化预发布形式，并且去掉 `v` 前缀后的 Docker 镜像 tag 长度不能超过 128 个字符；发布后 artifact verifier 会复用同一版本校验逻辑，对显式或归档名推断出的版本应用同一约束。
- 新增可复跑的 WebDAV curl 协议 smoke，可对已运行服务验证基础读写、URL 编码空格路径、复制、移动和删除操作；脚本会提前拒绝含空白、query、fragment、内嵌凭据、反斜杠、编码斜杠、编码反斜杠或 `.`/`..` 路径段的 `WEBDAV_URL`，并拒绝非 `0/1` 的 `CURL_INSECURE`，相关契约通过脚本门禁覆盖。
- 新增 WebDAV 兼容性报告表单，用于收集 Finder、Windows File Explorer、移动端文件管理器、媒体播放器和命令行客户端的验证结果或客户端特定失败。
- 维护页恢复完成后可复制恢复切换记录，内容包含恢复目标、只读校验、切换步骤、切换前确认和回滚清单；批量恢复结果会列出跨目录切换候选和冲突处置记录，并在可复制结果记录中写入候选目录、只读校验复核结论、校验错误详情、冲突处置建议和配置文件保留要求，便于记录到工单或值班流程。
- 设置页目录权限用户矩阵和未保存规则预览可复制权限复核记录，内容包含路径、用户读写判定、命中规则和相关分享影响，并会保留后端持久化近期复核历史；服务端历史不可用时回退当前浏览器记录。
- 分享路径策略可按用户、用户组或角色限制允许创建和维护分享链接的认证调用方；管理员保留修复既有分享的管理权限。
- 分享、版本历史、回收站和维护页的关键处置入口会写入活动复核记录，覆盖分享停用、删除、重新启用、版本恢复、回收站恢复和备份恢复执行结果，便于追踪误分享、误删和恢复处置闭环。
- 收紧发布就绪摘要：记录的完整验证目标之后如出现非发布文档变更，`release-readiness` 默认失败，并要求刷新完整验证或显式草稿放行。
- `release-readiness` 现在要求四份 hardening 证据文档都存在，并且都记录同一个完整验证目标，避免发布前证据缺失被静默跳过。
- `release-readiness` 还会检查双语 release notes 草稿记录当前完整验证目标，避免发布说明中的验证快照滞后。
- `release-readiness` 会拒绝不是当前 HEAD 祖先的 base ref，避免用旁支范围生成误导性的发布就绪摘要。
- 文档检查会拒绝 API 示例中可复制的 `?path=/...` 裸路径查询，要求恢复和收藏检查等 `path` 查询示例使用 `%2F...` 编码形式。
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

最近本地完整验证快照：验证目标 `09b6c4753900`，`GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master` 通过，覆盖批量恢复冲突处置记录增量、`make check`、依赖安全扫描、示例配置、public-access 模板、proto 再生成稳定性、Rust fmt/test/clippy、proto-gen fmt/test/clippy、前端 lint/typecheck/unit/build、Playwright 375 个 E2E 用例、Docker build 和 Docker smoke。Docker smoke 使用 Docker 自动分配的 loopback 端口 `http://127.0.0.1:32861`。

- `GOTOOLCHAIN=local ./scripts/verify-changed.sh`
- `GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master`
- `make scripts-check`
- `make docs-check`
- `./scripts/test-release-tag.sh`
- `./scripts/test-release-package.sh`
- `./scripts/test-release-artifacts.sh`
- Release artifact dash-prefixed directory test：`scripts/test-release-artifacts.sh`
- Docker quickstart safety test：`scripts/test-docker-quickstart.sh`
- Docker preflight safety test：`scripts/test-docker-preflight.sh`
- Docker container startup safety test：`scripts/test-docker-start.sh`
- Docker smoke safety test：`scripts/test-docker-smoke.sh`
- WebDAV curl smoke safety test：`scripts/test-webdav-client-smoke.sh`
- Release workflow 增量验证：`make workflows-check`、`make scripts-check`、`./scripts/check-secret-leaks.sh`、`make toolchains-check`、`git diff --check`
- Playwright E2E：`375 passed`
- 前端单测：`3108 passed`
- Docker build 和 `scripts/docker-smoke.sh`

最终发布前如代码、脚本、配置、文档或 workflow 再次变更，应重跑对应验证。

## 发布后核验

发布 tag 后，应下载 GitHub Release 产物并执行：

```bash
mkdir -p dist/release-check
gh release download v0.1.0 \
  --repo seanbao/mnemonas \
  --dir dist/release-check

./scripts/verify-release-artifacts.sh \
  --version v0.1.0 \
  --repository seanbao/mnemonas \
  --require-targets \
  --check-image \
  dist/release-check
```

随后应完成至少一次归档安装 smoke、Docker release 镜像启动 smoke、公开文档链接检查，以及公网部署环境的 DNS、防火墙、TLS 和云安全组复核。

## 已知限制

- SMB/Samba 可挂载运行时仍未启用；当前仅保留配置、诊断和运行态提示。
- `LOCK` / `UNLOCK` 为 WebDAV 兼容性虚拟实现，多客户端并发编辑同一文件时仍应由客户端或上层流程控制冲突。
- 真实公网部署依赖具体 DNS、防火墙、TLS、反向代理和云厂商安全组配置，模板和 doctor 无法替代环境级复核。
- 如未来版本引入不可逆数据迁移，回退应按对应 release note 或备份恢复流程处理。

## 维护者发布清单

- 确认 `CHANGELOG.md` 和 `CHANGELOG.en.md` 已覆盖本次发布。
- 确认本草稿已按最终 tag、验证结果和产物名称更新。
- 确认 `git status --short --branch` 干净。
- 确认 `./scripts/plan-hardening-commits.sh --fail-on-manual` 没有待分组路径。
- 运行 `./scripts/release-readiness.sh`，确认提交标题、临时 `fixup!` / `squash!` 提交、hardening 验证证据、发布文档命令和 community health 文件均通过检查。
- 创建并推送 tag 后，确认 Release workflow 成功。
- 发布后运行 release artifact verifier 并记录结果。
