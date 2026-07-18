# MnemoNAS 文档

[English](README.en.md) | 简体中文

本目录包含 MnemoNAS 的使用指南、部署说明和参考文档。

> [!WARNING]
> MnemoNAS 仍处于开发阶段，尚未发布任何可用版本。本文档用于源码开发、测试和未来发布准备；问题与建议通过 [GitHub Issues](https://github.com/seanbao/mnemonas/issues) 提交。

## 文档索引

### 开发验证与未来部署参考

| 中文 | English | 说明 |
|------|---------|------|
| [README](../README.md) | [README](../README.en.md) | 项目状态与源码试运行入口 |
| [Linux/systemd 部署指南](linux-systemd-deployment.md) | [Linux/systemd deployment](linux-systemd-deployment.en.md) | 未来 release 包的 systemd 验证流程 |
| [公网服务器快速上线](public-server-quickstart.md) | [Public server quickstart](public-server-quickstart.en.md) | 未来公网发布前的 HTTPS 与安全验证流程 |
| [Docker 部署指南](docker-deployment.md) | [Docker deployment](docker-deployment.en.md) | 源码构建与未来容器发布验证 |
| [挂载指南](mounting-guide.md) | [Mounting guide](mounting-guide.en.md) | 开发环境中的 WebDAV 客户端验证 |
| [反向代理配置](reverse-proxy-setup.md) | [Reverse proxy setup](reverse-proxy-setup.en.md) | 未来公网发布路径的反向代理验证 |
| [公网云防火墙复核清单](cloud-firewall-checklist.md) | [Public cloud firewall checklist](cloud-firewall-checklist.en.md) | 未来公网发布的云防火墙复核 |

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
| [路线图](roadmap.md) | [Roadmap](roadmap.en.md) | 从私有文件云盘到家庭/小团队 NAS 的优先级和能力边界 |
| [客户端重构记录](refactor-log.md) | [Client refactor log](refactor-log.en.md) | Flutter 客户端重构进度、已完成范围和剩余工作 |
| [开发指南](development.md) | [Development guide](development.en.md) | 服务端、Web 与 Flutter 客户端本地开发环境和门禁 |
| [Flutter 客户端](../client/README.md) | [Flutter client](../client/README.en.md) | Android-first 客户端当前实现范围、缺口与验证边界 |
| [测试策略](testing-strategy.md) | [Testing strategy](testing-strategy.en.md) | 多层测试方案：单元、集成、E2E、torture 与前端测试 |
| [开发阶段变更记录](release-notes.md) | [Development change record](release-notes.en.md) | 未发布开发分支的变更、验证证据和首次公开发布准备 |
| [API 参考](api-reference.md) | [API reference](api-reference.en.md) | REST API 端点与请求/响应格式 |
| [扩展点设计](extension-points.md) | [Extension points](extension-points.en.md) | 未来接口草案（S3/插件/Runner） |

### 反馈与安全

| 中文 | English | 说明 |
|------|---------|------|
| [反馈说明](../SUPPORT.md) | [Feedback](../SUPPORT.en.md) | Issue 反馈渠道、所需信息和处理边界 |
| [行为准则](../CODE_OF_CONDUCT.zh-CN.md) | [Code of Conduct](../CODE_OF_CONDUCT.md) | 问题反馈渠道的行为要求 |
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

**开发验证**：

1. 阅读 [README](../README.md) 了解项目
2. 按 [开发指南](development.md) 搭建本地环境，或用 [Docker 部署指南](docker-deployment.md) 从源码构建开发镜像
3. 使用非重要测试数据验证文件与 WebDAV 行为
4. 不要把当前源码用于生产环境或公网服务

**遇到问题**：
1. 查看 [FAQ](faq.md) 寻找解决方案
2. 检查 [WebDAV 兼容性](webdav-compatibility.md) 确认客户端支持
3. 按 [反馈说明](../SUPPORT.md) 选择 Issue 或私密安全报告渠道

**开发参考**：
1. 阅读 [架构设计](architecture.md) 了解系统结构
2. 按照 [开发指南](development.md) 搭建环境
3. 查看 [测试策略](testing-strategy.md) 了解质量检查入口
