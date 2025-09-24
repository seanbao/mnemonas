# MnemoNAS

[English](README.en.md) | 简体中文

[![CI](https://github.com/seanbao/mnemonas/actions/workflows/ci.yml/badge.svg)](https://github.com/seanbao/mnemonas/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/seanbao/mnemonas)](https://goreportcard.com/report/github.com/seanbao/mnemonas)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

> 私有文件、本地控制的自托管存储系统。

MnemoNAS 是一个开源的自托管 NAS 系统，提供 Web UI、WebDAV、版本历史、回收站、Scrub 和诊断包等日常文件管理能力。数据保存在自有存储目录中，迁移完整存储目录即可换机运行。

**命名来源**：Mnemosyne（摩涅莫辛涅），希腊神话中的记忆女神，九位缪斯之母，象征着知识、艺术与文明的传承。

## 能力概览

### 核心能力

- **数据自主权**：数据保存在配置的本地存储目录中，容量由底层磁盘决定，迁移完整存储目录即可换机运行
- **Web 界面**：桌面端和移动端均可使用，界面克制清晰，避免传统运维后台式堆砌
- **部署路径**：提供 Docker Compose 和 Linux/systemd 部署方式
- **维护与诊断**：健康检查、Scrub、GC 和诊断包用于发现并定位数据问题
- **Web 与 WebDAV 覆盖**：浏览器管理界面和常见 WebDAV 客户端均可访问，不只是文件浏览器

### 功能列表

| 功能模块 | 描述 |
| --- | --- |
| **文件管理** | 列表/网格视图、拖拽上传、批量操作、面包屑导航、缩略图预览 |
| **版本历史** | 按策略保留适合版本化的文件历史、版本对比、回退到指定版本、恢复前影响复核、恢复结果活动详情 |
| **回收站** | 软删除、按时间浏览、单个/批量恢复、定期自动清理 |
| **相册模式** | 图片瀑布流、缩略图自动生成、沉浸式浏览 |
| **全局搜索** | 按文件名搜索、实时结果、快速定位 |
| **用户管理** | 多用户支持、角色权限、密码策略、登录记录 |
| **分享链接** | 创建公开/私密链接、密码保护、有效期设置、访问统计 |
| **最近操作** | 关键操作记录、统计概览、高风险摘要与高风险分组集中窗口复核、当前页/当前筛选跨页复核、处置清单、带结构化批量处置摘要的持久化复核记录、需跟进复核批量视图、需跟进状态和处置备注回写、复核记录导出、单条活动与复核记录到相关活动、版本历史、回收站和分享处置页的入口、按复核人/关联活动/时间/处置状态的复核历史筛选、需跟进复核快速聚焦、按时间范围、路径、审计分组、类型和用户筛选、管理员清空记录、家庭/小团队活动回顾 |
| **设置** | 服务器配置、存储路径、版本保留策略、WebDAV 配置 |
| **备份与维护** | Scrub 完整性校验、GC 垃圾回收、诊断包导出、运行状态 |
| **WebDAV** | 覆盖 RFC 4918 核心读写方法，兼容性矩阵持续补充，Basic Auth 认证 |

## 架构

```text
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

MnemoNAS 采用 **原生文件 + CAS 版本历史** 的混合架构：当前文件保存在 `files/` 标准目录中，历史版本和去重对象保存在内部 CAS 中。

- **当前文件可读**：`files/` 下保存当前版本，具备操作系统目录权限的用户可离线迁移和备份
- **内容寻址版本**：历史版本按 BLAKE3 哈希存储，相同内容可复用
- **CDC 能力**：Rust 数据面提供 FastCDC 文件分块 API；当前 Go 版本历史路径使用 whole-object CAS 快照，后续接入分块版本存储前不会按块引用计数
- **明确边界**：具备操作系统权限时，直接在 `files/` 目录读文件是安全的；绕过 Web UI/WebDAV/API 直接写入不会触发版本历史或回收站
- **不依赖特定文件系统**：ext4/XFS/Btrfs/ZFS 均可，推荐 ZFS mirror
- **数据可迁移**：完整搬迁存储根目录即可在新机器上继续运行；版本历史和回收站需要 MnemoNAS 读取内部元数据

详见 [存储原理与最佳实践](docs/storage-internals.md)。

## 快速开始

### Linux / systemd（推荐用于长期运行）

从 [Releases](https://github.com/seanbao/mnemonas/releases) 下载 Linux release 包，在 Linux 服务器上安装为 systemd 服务：

```bash
tar -xzf mnemonas-<version>-linux-amd64.tar.gz
cd mnemonas-<version>-linux-amd64

sudo ./scripts/install-systemd.sh
sudo mnemonas-doctor
```

默认会安装到 `/usr/local/bin`，配置写入 `/etc/mnemonas/config.toml`，数据放在 `/srv/mnemonas`，Web UI 监听 `http://<server-ip>:8080`。首次登录密码在 `/srv/mnemonas/.mnemonas/initial-password.txt`。升级前保留上一版本 release 解压目录，升级失败时可重新运行旧目录里的安装脚本回退二进制和 Web UI；完整步骤见部署指南。如果要通过公网域名访问，先按 [公网服务器快速上线](docs/public-server-quickstart.md) 收紧后端端口并配置 HTTPS 反向代理。

详见 [Linux/systemd 部署指南](docs/linux-systemd-deployment.md)。

### Docker Compose

需要 Docker Engine 和 Compose v2；源码本地构建还需要 Buildx 插件。先确认 `docker compose version` 可用，源码构建时再确认 `docker buildx version` 可用。Ubuntu 24.04 上如果只有 `docker` 没有 `docker compose`，通常可先执行 `sudo apt install docker-compose-v2 docker-buildx`；使用 Docker 官方 apt 仓库时对应包名通常是 `docker-compose-plugin` 和 `docker-buildx-plugin`。

```bash
# 克隆项目
git clone https://github.com/seanbao/mnemonas.git
cd mnemonas

# 准备 .env、数据目录、UID/GID，运行预检并启动服务
./scripts/docker-quickstart.sh --start

# 默认打开浏览器访问:
# http://localhost:8080
```

仓库自带的 `docker-compose.yml` 默认从当前源码构建 `mnemonas:local` 镜像，不要求宿主机安装 Go/Rust/Node.js，但需要能拉取 Docker 基础镜像。Dockerfile 使用 BuildKit 缓存和较小的 Alpine Go builder，弱网环境下重试构建不会从零下载所有依赖。`docker-quickstart.sh` 会创建或更新 `.env`，把 `MNEMONAS_UID`/`MNEMONAS_GID` 设置为当前宿主机用户，创建 `MNEMONAS_DATA_DIR`，运行 Docker 预检，并在 `--start` 时按 `MNEMONAS_IMAGE` 选择启动方式：本地镜像执行源码构建，发布镜像使用 `docker compose up -d --pull missing --no-build`；启动后脚本会等待本机 `/health` 通过，避免容器已创建但服务未就绪，并输出 Web UI、health 检查、读取初始密码、WebDAV、Compose 状态和日志命令。如果本机无法访问 Docker 发布端口，可显式传 `--skip-health-check`。GitHub Releases 的二进制归档会附带 `docker-compose.yml` 和 `.env.example`，归档内模板会把 `MNEMONAS_IMAGE` 预设为同一 release tag 的 GHCR 镜像。如果 8080 已被占用，可运行 `./scripts/docker-quickstart.sh --port 8888 --start`。首次启动会在数据目录中自动生成持久化配置；默认 Web 登录初始密码在 `<MNEMONAS_DATA_DIR>/.mnemonas/initial-password.txt`，如果自定义 `auth.users_file`，`initial-password.txt` 会位于用户文件同目录。管理员首次登录后，首页会显示首次部署检查，逐项确认初始登录凭据处理、管理员冗余、备份和公网入口后可关闭提示。发布镜像使用方式见 [Docker 部署指南](docs/docker-deployment.md)。

### 二进制安装

适合开发调试或不使用 systemd 的环境。长期运行建议使用上面的 systemd 安装方式。

```bash
# 解压
tar -xzf mnemonas-<version>-linux-amd64.tar.gz
cd mnemonas-<version>-linux-amd64

# 创建配置
mkdir -p ~/.mnemonas
chmod 750 ~/.mnemonas
cp mnemonas.example.toml ~/.mnemonas/config.toml

# 启动数据面
./dataplane &

# 启动控制面
./nasd
```

### 客户端连接

MnemoNAS 通过 WebDAV 协议提供文件访问，面向常见桌面、移动端和命令行客户端：

| 平台 | 推荐客户端 | 连接地址 |
| --- | --- | --- |
| macOS | Finder | `http://localhost:8080/dav` |
| Windows | 文件资源管理器 | `http://localhost:8080/dav` |
| Linux | GNOME Files / davfs2 | `http://localhost:8080/dav` |
| iOS | Files / Documents | `http://<server-ip>:8080/dav` |
| Android | Solid Explorer | `http://<server-ip>:8080/dav` |
| CLI | rclone | `webdav:` remote |

默认配置启用 WebDAV Basic Auth；连接客户端时需要使用当前配置的 WebDAV 用户名和密码。

详见 [挂载指南](docs/mounting-guide.md)。

## 项目结构

```text
mnemonas/
├── cmd/nasd/           # Go 主程序入口
├── internal/           # Go 内部包
│   ├── webdav/         # WebDAV 协议实现
│   ├── api/            # REST/gRPC API
│   ├── config/         # 配置管理
│   ├── caslayout/      # CAS 存储布局（未来独立开源）
│   └── storage/        # 文件系统、版本、回收站与 CAS 编排
├── dataplane/          # Rust 数据面
├── web/                # React 前端
├── proto/              # gRPC 协议定义
├── docs/               # 文档
└── docker-compose.yml
```

## 开发

### 环境要求

- Go 1.25.11+
- Rust 1.92+
- Node.js `^20.19.0` 或 `>=22.12.0`（推荐使用 `.nvmrc` 指定的 22.x）
- Docker Engine + Compose v2 插件（支持 `docker compose`）
- protoc 3.20+（`make proto` / `make build` 或修改 proto 时需要；Docker 镜像构建不需要）

### 开发环境脚本启动

推荐使用 `scripts/dev.sh` 脚本快速启动开发环境：

前端 Node.js 版本由项目根目录 `.nvmrc` 固定为 `22`，并通过 `web/package.json` 的 engine 约束要求 `^20.19.0` 或 `>=22.12.0`。执行前端相关命令前先加载：

```bash
source "$HOME/.nvm/nvm.sh"
nvm use
```

`scripts/dev.sh` 在启动前端前会强制校验该版本；未安装或未加载 `nvm` 时会直接失败，不再静默使用错误版本继续启动。

```bash
# 启动完整环境（后端 + 前端）
./scripts/dev.sh

# 或使用选项
./scripts/dev.sh --backend   # 仅启动后端 (nasd + dataplane)
./scripts/dev.sh --creds     # 显示 Web UI 初始密码文件和 WebDAV 登录凭据
./scripts/dev.sh --frontend  # 仅启动前端 (localhost:5173)
./scripts/dev.sh --status    # 查看服务状态
./scripts/dev.sh --kill      # 停止所有组件
```

脚本会自动：

- 构建 Go 控制面和 Rust 数据面
- 启动服务并检查端口状态
- 将日志写入 `logs/` 目录
- 检测并使用 `.nvmrc` 指定的 Node.js 版本

### Makefile 命令

```bash
# 完整构建（proto → Web → Go → Rust）
make build

# 开发模式构建（快速，debug 模式）
make dev

# 运行所有测试
make test

# 深度测试矩阵：Go race/fuzz、前端 property、Playwright 交互完整性
make test-torture

# 测试覆盖率
make coverage

# E2E 验收测试
make e2e

# 破坏性故障注入测试，默认启动隔离后端
make fault-injection

# 性能基准测试
make bench

# 代码检查
make lint

# golangci-lint 默认复用本地 Go 工具链环境；需要自动下载工具链时可覆盖
GO_LINT_ENV="GOSUMDB=sum.golang.org GOTOOLCHAIN=auto" make lint

# golangci-lint 不在 PATH 时可显式指定位置
GOLANGCI_LINT=/path/to/golangci-lint make lint

# 仅限本地临时排障，提交前不要跳过 Go 静态检查
SKIP_GOLANGCI_LINT=1 make lint

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
| --- | --- | --- |
| Go 控制面 (nasd) | 8080 | REST API + WebDAV |
| Rust 数据面 HTTP | 9091 | 健康检查 + 统计 |
| Rust 数据面 gRPC | 9090 | CAS 存储服务 |
| 前端开发服务器 | 5173 | Vite dev server |

Docker 和 systemd 部署默认只对外提供 `8080`；`9090/9091` 是内部 dataplane 端口，应保持在容器内或 `127.0.0.1`。如果修改过 Web 或 dataplane 端口，也不要把对应的自定义后端端口发布到公网或不可信局域网。

详见 [开发指南](docs/development.md)。

## 文档

| 文档 | 说明 |
| --- | --- |
| [文档索引](docs/README.md) | 中文文档入口，包含每篇主要文档的中英文链接 |
| [开发指南](docs/development.md) | 本地开发环境搭建与调试 |
| [English documentation index](docs/README.en.md) | English entry point with English links for the main docs |
| [Linux/systemd 部署](docs/linux-systemd-deployment.md) | Linux 服务器的 systemd 长期运行指南 |
| [公网服务器快速上线](docs/public-server-quickstart.md) | 公网域名、HTTPS、反向代理和安全检查的一条推荐路径 |
| [Docker 部署](docs/docker-deployment.md) | Docker 部署指南 |
| [挂载指南](docs/mounting-guide.md) | 各平台 WebDAV 连接教程 |
| [WebDAV 兼容性](docs/webdav-compatibility.md) | 客户端兼容性与协议支持范围 |
| [反向代理配置](docs/reverse-proxy-setup.md) | HTTPS 与公网入口配置 |
| [存储原理与最佳实践](docs/storage-internals.md) | CAS 原理、文件系统推荐、性能调优 |
| [备份指南](docs/backup-guide.md) | 3-2-1 备份策略与恢复 |
| [FAQ](docs/faq.md) | 常见问题解答 |
| [架构设计](docs/architecture.md) | 系统架构与技术选型 |
| [路线图](docs/roadmap.md) | 从私有文件云盘到家庭/小团队 NAS 的功能优先级 |
| [安全指南](docs/security.md) | 认证与网络安全配置 |
| [支持说明](SUPPORT.md) | 支持渠道、问题分流和维护边界 |

## 脚本工具

| 脚本 | 说明 |
| --- | --- |
| [scripts/dev.sh](scripts/dev.sh) | 开发环境启动脚本 |
| [scripts/install-systemd.sh](scripts/install-systemd.sh) | Linux release 包 systemd 安装脚本 |
| [scripts/uninstall-systemd.sh](scripts/uninstall-systemd.sh) | systemd 卸载脚本，安装后也可用 `mnemonas-uninstall-systemd` 调用 |
| [scripts/mnemonas-doctor.sh](scripts/mnemonas-doctor.sh) | 部署健康诊断脚本 |
| [scripts/mnemonas-docker-preflight.sh](scripts/mnemonas-docker-preflight.sh) | Docker Compose 启动前预检脚本 |
| [scripts/run-e2e-isolated.sh](scripts/run-e2e-isolated.sh) | 启动隔离后端并运行 E2E 验收测试，`make e2e` 默认使用它 |
| [scripts/e2e-test.sh](scripts/e2e-test.sh) | 对显式指定的已运行服务执行 E2E 验收测试 |
| [scripts/torture-test.sh](scripts/torture-test.sh) | 非破坏性深度测试矩阵：race、fuzz、property、浏览器交互完整性 |
| [scripts/run-benchmark-isolated.sh](scripts/run-benchmark-isolated.sh) | 启动隔离后端并运行性能基准测试，`make bench` 默认使用它 |
| [scripts/benchmark.sh](scripts/benchmark.sh) | 对显式指定的本地服务和存储根执行性能基准测试 |
| [scripts/run-fault-injection-isolated.sh](scripts/run-fault-injection-isolated.sh) | 启动隔离后端并运行破坏性故障注入测试，`make fault-injection` 默认使用它 |
| [scripts/fault-injection-test.sh](scripts/fault-injection-test.sh) | 对显式指定的目标执行底层破坏性故障注入测试 |
| [scripts/setup-reverse-proxy.sh](scripts/setup-reverse-proxy.sh) | 公网 HTTPS 反向代理配置与 MnemoNAS 安全入口收紧 |

## License

MIT License - 详见 [LICENSE](LICENSE)

*MnemoNAS - 自托管文件管理与版本历史*
