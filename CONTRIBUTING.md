# 贡献指南

[English](CONTRIBUTING.en.md) | 简体中文

本文档说明 MnemoNAS 的贡献流程、质量门禁和安全边界。它适用于代码、脚本、文档、部署模板和测试变更。

## 贡献范围

优先接受以下类型的变更：

- 修复可复现缺陷、数据边界问题、安全边界问题或部署阻塞问题
- 改进 Web UI、WebDAV、版本历史、回收站、备份、维护和诊断等既有能力
- 提升 systemd、Docker、反向代理、公网访问和发布路径的可靠性
- 补齐中英文文档、配置示例、测试覆盖和验证脚本
- 简化现有实现并保持公共行为清晰

大范围重构应拆成可审阅的小提交。每个提交应只覆盖一个风险面，并包含对应测试或文档。

## 开发准备

推荐先阅读：

- [README](README.md)
- [文档索引](docs/README.md)
- [开发指南](docs/development.md)
- [测试策略](docs/testing-strategy.md)
- [安全加固指南](docs/security.md)

本地环境需要 Go、Rust、Node.js、Docker Engine 和 Compose v2。具体版本以 `.go-version`、`dataplane/rust-toolchain.toml`、`.nvmrc`、`web/package.json` 和 [开发指南](docs/development.md) 为准。

## 提交前检查

变更应先运行最窄但足以证明修改正确的命令，再按风险扩大验证范围。

常用入口：

```bash
make verify-changed
make quick-check
make check
make docs-check
make scripts-check
make security-check
```

分支范围验证：

```bash
GOTOOLCHAIN=local ./scripts/verify-changed.sh --base master
```

文档或脚本变更通常至少需要：

```bash
git diff --check
make docs-check
make scripts-check
./scripts/check-secret-leaks.sh
```

前端可见行为变更应补充对应的 Vitest 或 Playwright 覆盖。部署、配置、协议或安全边界变更应同步更新测试、示例和中英文文档。

## 文档要求

- 中英文文档应同步更新。
- 项目文档保持正式、客观，避免营销式表达和第二人称措辞。
- 配置、API、部署路径和公开行为变化应更新 README、`docs/`、示例配置和发布说明草稿中的相关入口。
- 安全敏感示例不得包含真实密钥、Token、Cookie 或可复制的弱口令。

## 安全边界

以下区域按安全敏感变更处理：

- 路径解析、归档下载、WebDAV、公开分享、回收站和恢复流程
- 密码、Token、Cookie、初始凭据、Webhook 和告警密钥
- 公网访问、反向代理、防火墙、TLS、Docker 端口和 systemd 服务权限
- 备份、恢复、Scrub、GC、CAS 对象和版本历史

安全漏洞不应通过公开 Issue 披露。报告方式见 [安全策略](SECURITY.zh-CN.md)。

## 提交信息

提交信息使用 Conventional Commits：

```text
<type>(<scope>): <subject>
```

示例：

```text
fix(api): reject unsafe archive paths
docs(deploy): clarify public access checklist
build(docker): harden smoke test port binding
```

提交标题使用英文祈使语气，冒号后的主题小写，末尾不加句号。非显而易见的迁移、风险或设计动机可放在提交正文。

## Pull Request 准备

PR 应说明：

- 变更目标和范围
- 用户可见行为变化
- 数据、权限、安全或部署影响
- 已运行的验证命令
- 未覆盖的风险和后续工作

大型 PR 应按风险面拆分。中英文文档变更应与对应行为变更保持同组提交。
