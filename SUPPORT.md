# 问题反馈

[English](SUPPORT.en.md) | 简体中文

MnemoNAS 仍处于开发阶段，尚未发布任何可用版本，也不提供稳定版本支持。当前仅通过 GitHub Issues 接收缺陷、使用问题、兼容性结果和功能建议；暂不接收外部代码或文档提交。

反馈应基于当前源码，并注明实际测试的 Git commit。维护者会按项目优先级处理，但不承诺响应时间、修复时间或发布时间。

## 反馈渠道

| 问题类型 | 推荐渠道 | 说明 |
| --- | --- | --- |
| 可复现 Bug | [GitHub Issues](https://github.com/seanbao/mnemonas/issues) | 附复现步骤和诊断信息 |
| WebDAV 客户端兼容性 | [WebDAV 兼容性报告表单](https://github.com/seanbao/mnemonas/issues/new?template=webdav_compatibility.yml) | 提交客户端验证结果、挂载差异或客户端特定失败 |
| 新功能或产品建议 | [GitHub Issues](https://github.com/seanbao/mnemonas/issues) | 说明具体用法和预期收益 |
| 安全漏洞 | GitHub Private Vulnerability Reporting | 不要公开提交漏洞细节，详见 [SECURITY.zh-CN.md](SECURITY.zh-CN.md) |

使用问题也可以先通过 Issues 记录，但标题和内容应说明这是使用问题而不是已确认 Bug。
WebDAV 客户端兼容性结果、挂载差异或客户端特定失败，应优先使用 WebDAV 兼容性报告表单。

## 提交反馈前

提交前应检查：

- [README](README.md) 和 [文档索引](docs/README.md)
- [FAQ](docs/faq.md)
- [Docker 部署指南](docs/docker-deployment.md) 或 [Linux/systemd 部署指南](docs/linux-systemd-deployment.md)
- [WebDAV 兼容性](docs/webdav-compatibility.md)
- 现有 Issues 是否已有相同问题
- WebDAV 客户端问题是否更适合使用 [WebDAV 兼容性报告表单](https://github.com/seanbao/mnemonas/issues/new?template=webdav_compatibility.yml)

## 提交缺陷时应包含

- 实际测试的 Git commit
- 部署方式：Ubuntu/systemd、Docker、手动二进制或开发环境
- 操作系统、文件系统和客户端信息
- 复现步骤、期望行为和实际行为
- 相关日志、截图或错误信息
- 对 systemd 部署，附 `sudo mnemonas-doctor` 输出
- 对 Docker 部署，附 `./scripts/mnemonas-docker-preflight.sh`、`docker compose ps` 和相关 Compose 日志

粘贴日志前应移除密码、Token、Cookie、内网敏感地址和其他私密信息。

## 反馈处理边界

维护者会优先复核清晰、可复现且影响明确的问题，但开发阶段的功能、接口、数据格式和部署方式仍可能变化。

当前重点复核：

- Linux 服务器和常见 NAS 部署方式
- Docker Compose 和 Ubuntu/systemd 部署路径
- 浏览器 Web UI 和常见 WebDAV 客户端
- 数据可迁移性、备份、恢复和安全配置相关问题

以下反馈可能无法处理：

- 非官方二次打包或大规模改造版本
- 直接暴露到公网且缺少反向代理、TLS、VPN 或防火墙保护的部署
- 大规模生产环境容量规划
- 未提供复现信息、日志或诊断输出的问题

项目当前不提供商业支持、托管服务或付费 SLA。
