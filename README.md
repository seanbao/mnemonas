# MnemoNAS

[![CI](https://github.com/seanbao/mnemonas/actions/workflows/ci.yml/badge.svg)](https://github.com/seanbao/mnemonas/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/seanbao/mnemonas)](https://goreportcard.com/report/github.com/seanbao/mnemonas)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Docker Pulls](https://img.shields.io/docker/pulls/seanbao/mnemonas)](https://hub.docker.com/r/seanbao/mnemonas)

> 🧠 **Your files. Your control.** — 私有云存储，消费级体验

MnemoNAS 是一个简单可靠、好看好用的开源 NAS 系统。数据在自己手里，体验不输云服务。

**命名来源**：Mnemosyne（摩涅莫辛涅），希腊神话中的记忆女神，九位缪斯之母，象征着知识、艺术与文明的传承。

## ✨ 特性

### 核心能力

- 🔐 **数据自主权**：数据在自己手里，没有订阅费、没有容量限制，硬盘拔下来换台电脑就能用
- 🎨 **消费级体验**：告别"工程师 UI"，对标 iCloud/Synology Photos 的现代设计
- 🚀 **10 分钟上手**：Docker Compose 一键启动，开箱即用
- 🛡️ **可靠不折腾**：自动校验数据完整性，出问题能发现、能定位
- 📱 **全平台覆盖**：Web + Android 原生 App，功能齐全不只是文件浏览器

### 完整功能列表

| 功能模块 | 描述 |
|---------|------|
| **文件管理** | 列表/网格视图、拖拽上传、批量操作、面包屑导航、缩略图预览 |
| **版本历史** | 自动保留历史版本、版本对比、一键回退到任意版本 |
| **回收站** | 软删除、按时间浏览、单个/批量恢复、定期自动清理 |
| **相册模式** | 图片瀑布流、缩略图自动生成、沉浸式浏览 |
| **全局搜索** | 按文件名搜索、实时结果、快速定位 |
| **用户管理** | 多用户支持、角色权限、密码策略、登录审计 |
| **分享链接** | 创建公开/私密链接、密码保护、有效期设置、访问统计 |
| **活动日志** | 全操作审计、按时间/类型筛选、统计报表 |
| **系统设置** | 服务器配置、存储路径、版本保留策略、WebDAV 配置 |
| **数据维护** | Scrub 完整性校验、GC 垃圾回收、诊断包导出、系统指标 |
| **WebDAV** | RFC 4918 完整实现、主流客户端兼容、Basic Auth 认证 |

## 🏗️ 架构

```
┌─────────────────────────────────────────────────────────┐
│                      Web UI (React)                      │
├─────────────────────────────────────────────────────────┤
│                   Go 控制面 (nasd)                       │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐    │
│  │ WebDAV  │  │  API    │  │ Config  │  │  Auth   │    │
│  └─────────┘  └─────────┘  └─────────┘  └─────────┘    │
├─────────────────────────────────────────────────────────┤
│                      gRPC                                │
├─────────────────────────────────────────────────────────┤
│                 Rust 数据面 (dataplane)                  │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐    │
│  │   CAS   │  │  CDC    │  │  Scrub  │  │   GC    │    │
│  └─────────┘  └─────────┘  └─────────┘  └─────────┘    │
├─────────────────────────────────────────────────────────┤
│                    文件系统                              │
└─────────────────────────────────────────────────────────┘
```

### 存储原理

MnemoNAS 采用 **CAS (内容寻址存储)** 架构，与传统 NAS 直接存储文件不同：

- **内容寻址**：文件按 BLAKE3 哈希存储，相同内容只存一份
- **CDC 分块**：大文件切分为 256KB-4MB 的块，实现细粒度去重
- **不依赖特定文件系统**：ext4/XFS/Btrfs/ZFS 均可，推荐 ZFS mirror
- **数据可迁移**：纯文件结构，硬盘拔下来插任何电脑都能读

详见 [存储原理与最佳实践](docs/storage-internals.md)。

## 🚀 快速开始

### Docker Compose（推荐）

```bash
# 克隆项目
git clone https://github.com/seanbao/mnemonas.git
cd mnemonas

# 启动服务
docker compose up -d

# 打开浏览器
open http://localhost:8080
```

### 二进制安装

从 [Releases](https://github.com/seanbao/mnemonas/releases) 下载对应平台的二进制文件：

```bash
# 解压
tar -xzf mnemonas-v0.1.0-linux-amd64.tar.gz

# 创建配置
mkdir -p ~/.mnemonas
cp mnemonas.example.toml ~/.mnemonas/config.toml

# 启动数据面
./dataplane &

# 启动控制面
./nasd
```

### 客户端连接

MnemoNAS 通过 WebDAV 协议提供文件访问，支持所有主流客户端：

| 平台 | 推荐客户端 | 连接地址 |
|------|-----------|---------|
| macOS | Finder | `http://localhost:8080/dav` |
| Windows | 文件资源管理器 | `http://localhost:8080/dav` |
| iOS | Files / Documents | `http://your-ip:8080/dav` |
| Android | Solid Explorer | `http://your-ip:8080/dav` |
| CLI | rclone | `webdav:` remote |

详见 [挂载指南](docs/mounting-guide.md)。

## 📁 项目结构

```
mnemonas/
├── cmd/nasd/           # Go 主程序入口
├── internal/           # Go 内部包
│   ├── webdav/         # WebDAV 协议实现
│   ├── api/            # REST/gRPC API
│   ├── config/         # 配置管理
│   ├── caslayout/      # CAS 存储布局（未来独立开源）
│   └── webdavcas/      # WebDAV-CAS 适配层（未来独立开源）
├── dataplane/          # Rust 数据面
├── web/                # React 前端
├── proto/              # gRPC 协议定义
├── docs/               # 文档
└── docker-compose.yml
```

## 🛠️ 开发

### 环境要求

- Go 1.25+
- Rust 1.92+
- Node.js 22+ (最低 20.x)
- Docker & Docker Compose
- protoc 25+

### 开发环境一键启动

推荐使用 `scripts/dev.sh` 脚本快速启动开发环境：

```bash
# 一键启动完整环境（后端 + 前端）
./scripts/dev.sh

# 或使用选项
./scripts/dev.sh --backend   # 仅启动后端 (nasd + dataplane)
./scripts/dev.sh --frontend  # 仅启动前端 (localhost:5173)
./scripts/dev.sh --status    # 查看服务状态
./scripts/dev.sh --kill      # 停止所有组件
```

脚本会自动：
- 构建 Go 控制面和 Rust 数据面
- 启动服务并检查端口状态
- 将日志写入 `logs/` 目录
- 检测 nvm 并切换 Node.js 版本

### Makefile 命令

```bash
# 完整构建（proto → Go → Rust）
make build

# 开发模式构建（快速，debug 模式）
make dev

# 运行所有测试
make test

# 测试覆盖率
make coverage

# E2E 验收测试
make e2e

# 性能基准测试
make bench

# 代码检查
make lint

# 代码格式化
make fmt

# 安装依赖
make deps

# 清理构建产物
make clean

# 查看所有命令
make help
```

### 端口说明

| 服务 | 端口 | 说明 |
|------|------|------|
| Go 控制面 (nasd) | 8080 | REST API + WebDAV |
| Rust 数据面 HTTP | 9091 | 健康检查 + 统计 |
| Rust 数据面 gRPC | 9090 | CAS 存储服务 |
| 前端开发服务器 | 5173 | Vite dev server |

详见 [开发指南](docs/development.md)。

## 📖 文档

| 文档 | 说明 |
|------|------|
| [开发指南](docs/development.md) | 本地开发环境搭建与调试 |
| [Docker 部署](docs/docker-deployment.md) | 生产环境部署指南 |
| [挂载指南](docs/mounting-guide.md) | 各平台 WebDAV 连接教程 |
| [存储原理与最佳实践](docs/storage-internals.md) | CAS 原理、文件系统推荐、性能调优 |
| [备份指南](docs/backup-guide.md) | 3-2-1 备份策略与恢复 |
| [FAQ](docs/faq.md) | 常见问题解答 |
| [架构设计](docs/architecture.md) | 系统架构与技术选型 |
| [安全指南](docs/security.md) | 认证与网络安全配置 |

## 🔧 脚本工具

| 脚本 | 说明 |
|------|------|
| [scripts/dev.sh](scripts/dev.sh) | 开发环境启动脚本 |
| [scripts/e2e-test.sh](scripts/e2e-test.sh) | E2E 验收测试 |
| [scripts/benchmark.sh](scripts/benchmark.sh) | 性能基准测试 |
| [scripts/fault-injection-test.sh](scripts/fault-injection-test.sh) | 故障注入测试 |
| [scripts/setup-reverse-proxy.sh](scripts/setup-reverse-proxy.sh) | 反向代理配置 |

## 📜 License

MIT License - 详见 [LICENSE](LICENSE)

## 🤝 贡献

欢迎贡献！请查看 [CONTRIBUTING.md](CONTRIBUTING.md) 了解详情。

---

*MnemoNAS - 让记忆永不丢失* 🧠
