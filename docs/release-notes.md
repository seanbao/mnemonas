# 发布说明草稿

[English](release-notes.en.md) | 简体中文

本文档为下一次公开发布的发布说明草稿。最终发布时应以对应 tag、CI 结果、Release 产物和部署验证结果为准。

## 摘要

本轮发布候选重点强化 MnemoNAS 作为自托管 NAS 的稳定性、公开访问安全边界、部署可验证性和文档可维护性。当前硬化分支按风险面拆分为可审阅提交，并已通过完整分支范围验证。

## 主要变化

- 加强路径、归档下载、WebDAV、公开分享、工作区、CAS 和备份恢复相关边界检查，覆盖符号链接、路径穿越、百分号编码点段、控制字符和回滚错误情况。
- 完善认证、用户、主目录、目录配额、目录访问规则、分享策略和会话安全默认值的后端与前端覆盖。
- 加固邮件告警通知出口，消息头和 SMTP envelope 会清理控制字符，降低内部调用或后续扩展绕过配置校验后的头部注入风险。
- 提升 Web 可见质量，核心页面、公开入口、移动端布局、基础可访问性、运行时错误、失败请求和破碎可见文本已纳入 Playwright 扫描。
- 加固 systemd、Docker、反向代理、公网访问模板、doctor、公网域名就绪校验、release package 和 release artifact 验证路径；Release workflow 会在创建 GitHub Release 前校验归档、checksums、必需目标集合、归档条目类型、重复条目、控制字符路径、空白字符路径、反斜杠路径和歧义路径。
- Release tag 会在产物构建前校验，必须使用 `vMAJOR.MINOR.PATCH` 或 `v1.2.3-rc.1` 这类语义化预发布形式。
- 新增可复跑的 WebDAV curl 协议 smoke，可对已运行服务验证基础读写、URL 编码空格路径、复制、移动和删除操作，并通过脚本门禁覆盖。
- 新增 WebDAV 兼容性报告表单，用于收集 Finder、Windows File Explorer、移动端文件管理器、媒体播放器和命令行客户端的验证结果或客户端特定失败。
- 维护页恢复完成后可复制恢复切换记录，内容包含恢复目标、只读校验、切换步骤和回滚清单，便于记录到工单或值班流程。
- 设置页目录权限用户矩阵和未保存规则预览可复制权限复核记录，内容包含路径、用户读写判定、命中规则和相关分享影响，并会保留后端持久化近期复核历史；服务端历史不可用时回退当前浏览器记录。
- 分享路径策略可按用户、用户组或角色限制允许创建和维护分享链接的认证调用方；管理员保留修复既有分享的管理权限。
- 收紧发布就绪摘要：记录的完整验证目标之后如出现非发布文档变更，`release-readiness` 默认失败，并要求刷新完整验证或显式草稿放行。
- `release-readiness` 现在要求四份 hardening 证据文档都存在，并且都记录同一个完整验证目标，避免发布前证据缺失被静默跳过。
- `release-readiness` 还会检查双语 release notes 草稿记录当前完整验证目标，避免发布说明中的验证快照滞后。
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

最近本地完整验证快照：验证目标 `77a8089962dc`，`GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master` 通过，覆盖 `make check`、依赖安全扫描、示例配置、public-access 模板、公网 go-live 有效 DNS label、重复尾点、完整域名、同域 HTTPS 重定向变体、doctor public-domain DNS label 边界与 go-live/doctor `localhost`/IPv4 字面量拒绝校验、proto 再生成稳定性、Rust fmt/test/clippy、前端 lint/typecheck/unit/build、Playwright 371 个 E2E 用例、systemd 升级/回退 installer 回归、WebDAV COPY/MOVE 后内容一致性和 MOVE 源路径清理 smoke、恢复演练清理失败警告路径、恢复完成弹窗导出摘要路径、恢复切换记录复制路径、用户配额总览路径、分享复核摘要复制路径、目录权限复核记录复制、后端持久化近期历史 API 与本地回退路径、当前范围复核历史入口和布局完整性验证、Docker build 和 Docker smoke。Docker smoke 使用 Docker 自动分配的 loopback 端口 `http://127.0.0.1:32825`。

- `GOTOOLCHAIN=local ./scripts/verify-changed.sh`
- `GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master`
- `make scripts-check`
- `make docs-check`
- `./scripts/test-release-tag.sh`
- `./scripts/test-release-package.sh`
- `./scripts/test-release-artifacts.sh`
- WebDAV curl smoke safety test：`scripts/test-webdav-client-smoke.sh`
- Release workflow 增量验证：`make workflows-check`、`make scripts-check`、`./scripts/check-secret-leaks.sh`、`make toolchains-check`、`git diff --check`
- Playwright E2E：`371 passed`
- 前端单测：`3075 passed`
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
