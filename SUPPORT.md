# 支持说明

[English](SUPPORT.en.md) | 简体中文

本文档说明 MnemoNAS 的支持渠道、问题分流方式和支持边界，用于提高反馈效率。

## 支持渠道

| 问题类型 | 推荐渠道 | 说明 |
| --- | --- | --- |
| 可复现 Bug | [GitHub Issues](https://github.com/seanbao/mnemonas/issues) | 附复现步骤和诊断信息 |
| WebDAV 客户端兼容性 | [WebDAV 兼容性报告表单](https://github.com/seanbao/mnemonas/issues/new?template=webdav_compatibility.yml) | 提交客户端验证结果、挂载差异或客户端特定失败 |
| 新功能或产品建议 | [GitHub Issues](https://github.com/seanbao/mnemonas/issues) | 说明具体用法和预期收益 |
| 安全漏洞 | GitHub Private Vulnerability Reporting | 不要公开提交漏洞细节，详见 [SECURITY.md](SECURITY.md) |

使用问题也可以先通过 Issues 记录，但标题和内容应说明这是使用问题而不是已确认 Bug。
WebDAV 客户端兼容性结果、挂载差异或客户端特定失败，应优先使用 WebDAV 兼容性报告表单。

## 提交问题前

提交前应检查：

- [README](README.md) 和 [文档索引](docs/README.md)
- [FAQ](docs/faq.md)
- [Docker 部署指南](docs/docker-deployment.md) 或 [Linux/systemd 部署指南](docs/linux-systemd-deployment.md)
- [WebDAV 兼容性](docs/webdav-compatibility.md)
- 现有 Issues 是否已有相同问题
- WebDAV 客户端问题是否更适合使用 [WebDAV 兼容性报告表单](https://github.com/seanbao/mnemonas/issues/new?template=webdav_compatibility.yml)

## 提交 Bug 时应包含

- MnemoNAS 版本或 Git commit
- 部署方式：Ubuntu/systemd、Docker、手动二进制或开发环境
- 操作系统、文件系统和客户端信息
- 复现步骤、期望行为和实际行为
- 相关日志、截图或错误信息
- 对 systemd 部署，附 `sudo mnemonas-doctor` 输出
- 对 Docker 部署，附 `./scripts/mnemonas-docker-preflight.sh`、`docker compose ps` 和相关 Compose 日志

粘贴日志前应移除密码、Token、Cookie、内网敏感地址和其他私密信息。

## 支持边界

维护者会尽力处理清晰、可复现、影响明确的问题，但不承诺固定响应时间或商业级 SLA。

当前重点支持：

- Linux 服务器和常见 NAS 部署方式
- Docker Compose 和 Ubuntu/systemd 部署路径
- 浏览器 Web UI 和常见 WebDAV 客户端
- 数据可迁移性、备份、恢复和安全配置相关问题

以下情况可能只能提供有限帮助：

- 非官方二次打包或大规模改造版本
- 直接暴露到公网且缺少反向代理、TLS、VPN 或防火墙保护的部署
- 大规模生产环境容量规划
- 未提供复现信息、日志或诊断输出的问题

商业支持、托管服务和付费 SLA 当前不在项目支持范围内。
