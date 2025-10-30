# 发布说明草稿

[English](release-notes.en.md) | 简体中文

本文档为下一次公开发布的发布说明草稿。最终发布时应以对应 tag、CI 结果、Release 产物和部署验证结果为准。

## 摘要

本轮发布候选重点强化 MnemoNAS 作为自托管 NAS 的稳定性、公开访问安全边界、部署可验证性和文档可维护性。当前硬化分支按风险面拆分为可审阅提交，并已通过分支范围验证。

## 主要变化

- 加强路径、归档下载、WebDAV、公开分享、工作区、CAS 和备份恢复相关边界检查，覆盖符号链接、路径穿越、百分号编码点段、控制字符和回滚错误场景。
- 完善认证、用户、主目录、目录配额、目录访问规则、分享策略和会话安全默认值的后端与前端覆盖。
- 提升 Web 可见质量，核心页面、公开入口、移动端布局、基础可访问性、运行时错误、失败请求和破碎可见文本已纳入 Playwright 扫描。
- 加固 systemd、Docker、反向代理、公网访问模板、doctor、release package 和 release artifact 验证路径。
- 精简并同步中英文文档，补齐部署、配置、FAQ、路线图、安全、硬化进度和发布前审查入口。

## 发布产物

Release workflow 预期生成以下产物：

- Linux x86_64 / ARM64 二进制归档。
- macOS Intel / Apple Silicon 手动运行归档。
- `checksums.txt`。
- GHCR 容器镜像标签。

归档内应包含顶层目录、`nasd`、`dataplane`、Web UI 静态资源、systemd 安装/卸载脚本、doctor、Docker Compose 模板、`.env.example`、部署模板和中英文文档。归档内 `.env.example` 应预设同一 release tag 的 GHCR 镜像。

## 发布前验证

当前硬化分支已通过以下验证：

- `GOTOOLCHAIN=local ./scripts/verify-changed.sh`
- `GOTOOLCHAIN=local timeout 45m ./scripts/verify-changed.sh --base master`
- `make scripts-check`
- `make docs-check`
- `./scripts/test-release-package.sh`
- `./scripts/test-release-artifacts.sh`
- Playwright E2E：`369 passed`
- 前端单测：`3054 passed`
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
- 创建并推送 tag 后，确认 Release workflow 成功。
- 发布后运行 release artifact verifier 并记录结果。
