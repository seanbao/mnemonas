# 支持说明

本文档说明 MnemoNAS 的支持渠道、问题分流方式和维护边界，帮助用户更快获得有效反馈。

## 支持渠道

| 场景 | 推荐渠道 | 说明 |
| --- | --- | --- |
| 可复现 Bug | [GitHub Issues](https://github.com/seanbao/mnemonas/issues) | 请使用 Bug Report 模板并附诊断信息 |
| 新功能或产品建议 | [GitHub Issues](https://github.com/seanbao/mnemonas/issues) | 请使用 Feature Request 模板，说明使用场景 |
| 安全漏洞 | GitHub Private Vulnerability Reporting | 不要公开提交漏洞细节，详见 [SECURITY.md](SECURITY.md) |

使用问题也可以先通过 Issues 记录，但请在标题和内容中说明这是使用问题而不是已确认 Bug。

## 提交问题前

请先检查：

- [README](README.md) 和 [文档索引](docs/README.md)
- [FAQ](docs/faq.md)
- [Docker 部署指南](docs/docker-deployment.md) 或 [Ubuntu 笔记本部署指南](docs/ubuntu-laptop-deployment.md)
- [WebDAV 兼容性](docs/webdav-compatibility.md)
- 现有 Issues 是否已有相同问题

## 提交 Bug 时请包含

- MnemoNAS 版本或 Git commit
- 部署方式：Ubuntu/systemd、Docker、手动二进制或开发环境
- 操作系统、文件系统和客户端信息
- 复现步骤、期望行为和实际行为
- 相关日志、截图或错误信息
- 对 systemd 部署，附 `sudo mnemonas-doctor` 输出
- 对 Docker 部署，附 `./scripts/mnemonas-docker-preflight.sh`、`docker compose ps` 和相关 Compose 日志

请在粘贴日志前移除密码、Token、Cookie、内网敏感地址和其他私密信息。

## 支持边界

维护者会尽力处理清晰、可复现、影响明确的问题，但不承诺固定响应时间或商业级 SLA。

当前重点支持：

- Linux 服务器和常见 NAS/小主机场景
- Docker Compose 和 Ubuntu/systemd 部署路径
- 浏览器 Web UI 和常见 WebDAV 客户端
- 数据可迁移性、备份、恢复和安全配置相关问题

以下场景可能只能提供有限帮助：

- 非官方二次打包或大规模改造版本
- 直接暴露到公网且缺少反向代理、TLS、VPN 或防火墙保护的部署
- 远超家庭/小团队定位的大规模生产环境容量规划
- 未提供复现信息、日志或诊断输出的问题

商业支持、托管服务和企业级 SLA 当前不在项目支持范围内。
