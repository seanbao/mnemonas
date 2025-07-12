# MnemoNAS 文档

[English](README.en.md) | 简体中文

本目录包含 MnemoNAS 的使用指南、部署说明和参考文档。

## 文档索引

### 快速入门

| 中文 | English | 说明 |
|------|---------|------|
| [README](../README.md) | [README](../README.en.md) | 项目介绍与快速开始 |
| [Linux/systemd 部署指南](linux-systemd-deployment.md) | [Linux/systemd deployment](linux-systemd-deployment.en.md) | 在 Linux 服务器上用 systemd 长期运行 |
| [公网服务器快速上线](public-server-quickstart.md) | [Public server quickstart](public-server-quickstart.en.md) | 公网域名、HTTPS、反向代理和安全检查的一条推荐路径 |
| [Docker 部署指南](docker-deployment.md) | [Docker deployment](docker-deployment.en.md) | 使用 Docker 部署 MnemoNAS |
| [挂载指南](mounting-guide.md) | [Mounting guide](mounting-guide.en.md) | 各平台 WebDAV 挂载教程 |
| [反向代理配置](reverse-proxy-setup.md) | [Reverse proxy setup](reverse-proxy-setup.en.md) | HTTPS 外网访问配置（Caddy/Nginx/Traefik/Cloudflare Tunnel） |
| [公网云防火墙复核清单](cloud-firewall-checklist.md) | [Public cloud firewall checklist](cloud-firewall-checklist.en.md) | 云安全组、VPC 防火墙和端口暴露复核 |

### 用户指南

| 中文 | English | 说明 |
|------|---------|------|
| [FAQ](faq.md) | [FAQ](faq.en.md) | 常见问题与故障排除 |
| [存储原理与运维建议](storage-internals.md) | [Storage internals and operations guidance](storage-internals.en.md) | CAS 存储原理、文件系统推荐、性能调优 |
| [备份指南](backup-guide.md) | [Backup guide](backup-guide.en.md) | 3-2-1 备份策略与恢复流程 |
| [WebDAV 兼容性](webdav-compatibility.md) | [WebDAV compatibility](webdav-compatibility.en.md) | 客户端兼容性与协议支持 |
| [安全加固指南](security.md) | [Security hardening guide](security.en.md) | 认证、HTTPS、网络安全配置 |

### 开发文档

| 中文 | English | 说明 |
|------|---------|------|
| [架构设计](architecture.md) | [Architecture](architecture.en.md) | 系统架构与技术选型 |
| [设计决策](design-decisions.md) | [Design decisions](design-decisions.en.md) | 技术选型理由与竞争力目标 |
| [路线图](roadmap.md) | [Roadmap](roadmap.en.md) | 从私有文件云盘到家庭/小团队 NAS 的功能优先级 |
| [开发指南](development.md) | [Development guide](development.en.md) | 本地开发环境搭建 |
| [测试策略](testing-strategy.md) | [Testing strategy](testing-strategy.en.md) | 多层测试方案：单元、集成、E2E、torture 与前端测试 |
| [硬化进度台账](hardening-progress.md) | [Hardening progress ledger](hardening-progress.en.md) | 已完成硬化区域、验证证据和剩余工作分流 |
| [硬化审查摘要](hardening-review-summary.md) | [Hardening review summary](hardening-review-summary.en.md) | 当前硬化工作树的审查分组、残余风险和发布前检查 |
| [API 参考](api-reference.md) | [API reference](api-reference.en.md) | REST API 端点与请求/响应格式 |
| [扩展点设计](extension-points.md) | [Extension points](extension-points.en.md) | 未来接口草案（S3/插件/Runner） |

### 支持与安全

| 中文 | English | 说明 |
|------|---------|------|
| [支持说明](../SUPPORT.md) | [Support](../SUPPORT.en.md) | 支持渠道、问题分流和支持边界 |
| [安全策略](../SECURITY.zh-CN.md) | [Security policy](../SECURITY.md) | 安全漏洞报告方式和部署安全提醒 |

### 配置参考

| 中文 | English | 说明 |
|------|---------|------|
| [配置参考](configuration.md) | [Configuration reference](configuration.en.md) | 完整配置选项说明 |
| [mnemonas.example.toml](../mnemonas.example.toml) | [mnemonas.example.toml](../mnemonas.example.toml) | 配置文件示例 |
| [CHANGELOG](../CHANGELOG.md) | [CHANGELOG](../CHANGELOG.en.md) | 版本更新记录 |

## 相关链接

- [GitHub 仓库](https://github.com/seanbao/mnemonas)
- [问题反馈](https://github.com/seanbao/mnemonas/issues)
- [支持说明](../SUPPORT.md)

## 阅读建议

**首次使用**：
1. 阅读 [README](../README.md) 了解项目
2. 长期运行优先按 [Linux/systemd 部署指南](linux-systemd-deployment.md) 安装；临时试用可按 [Docker 部署指南](docker-deployment.md) 启动
3. 需要公网访问时，按 [公网服务器快速上线](public-server-quickstart.md) 配置 HTTPS 入口
4. 参考 [挂载指南](mounting-guide.md) 连接到 NAS

**遇到问题**：
1. 查看 [FAQ](faq.md) 寻找解决方案
2. 检查 [WebDAV 兼容性](webdav-compatibility.md) 确认客户端支持
3. 按 [支持说明](../SUPPORT.md) 选择 Issues 或安全报告渠道

**开发参考**：
1. 阅读 [架构设计](architecture.md) 了解系统结构
2. 按照 [开发指南](development.md) 搭建环境
3. 查看 [测试策略](testing-strategy.md) 了解质量检查入口
