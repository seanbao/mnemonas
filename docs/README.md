# MnemoNAS 文档

欢迎使用 MnemoNAS 文档。本目录包含详细的使用指南、部署说明和参考文档。

## 📚 文档索引

### 快速入门

| 文档 | 说明 |
|------|------|
| [README](../README.md) | 项目介绍与快速开始 |
| [Ubuntu 笔记本部署指南](ubuntu-laptop-deployment.md) | 在闲置 Ubuntu 笔记本/小主机上用 systemd 长期运行 |
| [Docker 部署指南](docker-deployment.md) | 使用 Docker 部署 MnemoNAS |
| [挂载指南](mounting-guide.md) | 各平台 WebDAV 挂载教程 |
| [反向代理配置](reverse-proxy-setup.md) | HTTPS 外网访问配置（Caddy/Nginx/Traefik） |

### 用户指南

| 文档 | 说明 |
|------|------|
| [FAQ](faq.md) | 常见问题与故障排除 |
| [存储原理与最佳实践](storage-internals.md) | CAS 存储原理、文件系统推荐、性能调优 |
| [备份指南](backup-guide.md) | 3-2-1 备份策略与恢复流程 |
| [WebDAV 兼容性](webdav-compatibility.md) | 客户端兼容性与协议支持 |
| [安全指南](security.md) | 认证、HTTPS、网络安全配置 |

### 开发文档

| 文档 | 说明 |
|------|------|
| [架构设计](architecture.md) | 系统架构与技术选型 |
| [设计决策](design-decisions.md) | 技术选型理由与竞争力目标 |
| [开发指南](development.md) | 本地开发环境搭建 |
| [测试策略](testing-strategy.md) | 多层测试方案：单元/集成/E2E/AI 辅助 |
| [API 参考](api-reference.md) | REST API 端点与请求/响应格式 |
| [扩展点设计](extension-points.md) | 阶段 2 接口草案（S3/插件/Runner） |

### 配置参考

| 文件 | 说明 |
|------|------|
| [配置参考](configuration.md) | 完整配置选项说明 |
| [mnemonas.example.toml](../mnemonas.example.toml) | 配置文件示例 |
| [CHANGELOG](../CHANGELOG.md) | 版本更新记录 |
| [支持说明](../SUPPORT.md) | 支持渠道、问题分流和维护边界 |

## 🔗 相关链接

- [GitHub 仓库](https://github.com/seanbao/mnemonas)
- [问题反馈](https://github.com/seanbao/mnemonas/issues)
- [支持说明](../SUPPORT.md)

## 📖 阅读建议

**首次使用**：
1. 阅读 [README](../README.md) 了解项目
2. 长期运行优先按 [Ubuntu 笔记本部署指南](ubuntu-laptop-deployment.md) 安装；临时试用可按 [Docker 部署指南](docker-deployment.md) 启动
3. 参考 [挂载指南](mounting-guide.md) 连接到 NAS

**遇到问题**：
1. 查看 [FAQ](faq.md) 寻找解决方案
2. 检查 [WebDAV 兼容性](webdav-compatibility.md) 确认客户端支持
3. 按 [支持说明](../SUPPORT.md) 选择 Issues 或安全报告渠道

**开发参考**：
1. 阅读 [架构设计](architecture.md) 了解系统结构
2. 按照 [开发指南](development.md) 搭建环境
3. 查看 [测试策略](testing-strategy.md) 了解质量检查入口
