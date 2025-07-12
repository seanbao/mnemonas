# MnemoNAS 架构

[English](architecture.en.md) | 简体中文

本文档描述 MnemoNAS 的系统架构、主要设计决策和实现边界。

## 设计定位

MnemoNAS 是面向日常文件管理的自托管私有云存储系统。它保持当前文件树可直接读取，在其上提供版本历史和回收站，并同时暴露 Web UI 和 WebDAV。

核心原则：

- 数据所有权：数据位于用户自己的磁盘上，迁移完整存储根目录即可迁移服务。
- 可用界面：桌面端和移动端视图应清晰、高效，适合日常文件操作。
- 崩溃一致性：写入路径恢复后应处于上一个完整版本或新的完整版本，不留下半写入状态。
- 端到端校验：使用 BLAKE3 哈希检测缺失或损坏对象。
- 可恢复性：版本历史和回收站是一等功能。

当前非目标：

- 可挂载 SMB/NFS 运行时。SMB 目前仅提供网关配置预览；协议兼容性和安全边界尚未完整。
- 在 MnemoNAS 内部管理 RAID 或卷。
- 多节点集群一致性。

## 高层架构

```text
+---------------------------------------------------------+
|                      Clients                            |
|  Web UI / Finder / Explorer / rclone / mobile clients   |
+-------------------------+-------------------------------+
                          |
+-------------------------v-------------------------------+
|                 Go control plane (nasd)                 |
|  WebDAV handler / REST API / static Web UI / auth       |
|  config / users / shares / activity / storage facade    |
+-------------------------+-------------------------------+
                          | gRPC
+-------------------------v-------------------------------+
|                Rust data plane (dataplane)              |
|  CAS object storage / CDC chunking / scrub / GC         |
+-------------------------+-------------------------------+
                          |
+-------------------------v-------------------------------+
|                      Filesystem                         |
|  storage.root/files        当前用户文件                 |
|  storage.root/.mnemonas    元数据、对象、回收站         |
+---------------------------------------------------------+
```

Go 进程负责面向用户的协议和策略。Rust 进程负责高吞吐的内容寻址存储工作。

## Go 控制面

控制面由 `cmd/nasd` 和 `internal/` 下的包实现。

主要职责：

- HTTP server 和静态 Web UI 托管。
- 文件、用户、分享、设置、维护和诊断 REST API。
- WebDAV RFC 4918 核心方法。
- 认证、JWT refresh token、每用户根目录边界和管理员端点。
- 存储编排：workspace 文件、版本存储、回收站、活动日志和维护任务。
- 配置加载、校验和运行时设置更新。

重要模块：

| 模块 | 职责 |
| --- | --- |
| `internal/storage` | 统一文件操作、版本、回收站和元数据编排 |
| `internal/workspace` | `storage.root/files` 下的原生文件操作 |
| `internal/versionstore` | 基于 SQLite 的版本元数据和 object-store 抽象 |
| `internal/webdav` | WebDAV 请求处理和客户端兼容行为 |
| `internal/api` | REST handler 和响应契约 |
| `internal/config` | TOML 配置加载和校验 |
| `internal/auth` | 用户、用户组、角色、密码、JWT、登录限制和下载会话 |

当前文件先写入原生 workspace。文件符合版本策略时，历史内容会提交到 CAS-backed version store。

## Rust 数据面

数据面位于 `dataplane/`。

主要职责：

- 存取内容寻址对象。
- 为 dataplane file API 使用 FastCDC 对大内容分块。
- 使用 BLAKE3 对内容做哈希。
- 可选使用 zstd 压缩对象载荷。
- 运行 scrub 和对象列表操作。
- 向 `nasd` 提供 gRPC，并提供内部健康/统计 HTTP 端点。

Go 版本历史路径当前把历史快照存储为 BLAKE3 整对象 CAS 对象。dataplane `PutFile` / `GetFile` RPC 提供 FastCDC 分块能力，但分块级版本引用追踪尚未接入 Go 控制面。

数据面有意不暴露给最终用户。正常部署中，gRPC `9090` 和 HTTP `9091` 应保持在 loopback 或容器内部。

## 通信

`nasd` 通过 gRPC 与 `dataplane` 通信。该边界保持进程间接口简单，避免 CGO/FFI 复杂性，同时保留强类型接口。

NAS 工作负载通常由磁盘 I/O 和网络 I/O 主导，而不是 Go 到 Rust 的序列化开销。因此，gRPC 是当前架构的务实默认选择。

## 存储模型

MnemoNAS 使用混合布局：

```text
storage.root/
├── files/                # 当前用户文件，按普通文件保存
└── .mnemonas/
    ├── index.db          # SQLite 元数据
    ├── objects/          # 版本使用的 CAS 对象
    ├── trash/            # 软删除内容
    ├── thumbnails/       # 生成的缩略图缓存
    ├── maintenance/      # scrub/GC 状态
    └── users.json        # auth 使用默认文件时的用户数据
```

该布局让用户保留可读的当前文件树，同时将版本历史以内容寻址、整对象去重且可校验的方式保存。

拥有操作系统级访问权限的用户可以安全地直接读取 `files/` 下的文件。但在 MnemoNAS 运行时直接写入或删除这些文件，会绕过版本历史、回收站、活动日志和元数据协调。

## 数据模型

主要逻辑实体包括：

- `files/` 下的当前文件和目录。
- 按路径和内容哈希索引的版本记录。
- 按 BLAKE3 哈希寻址的 CAS 对象。
- 包含原路径、删除时间和内容引用的回收站记录。
- 包含角色、用户组和 `home_dir` 的用户。
- 可包含密码、过期时间和访问上限的分享链接。
- 按每用户根目录限定范围的收藏和活动记录。

需要 ACID 语义的事务性元数据使用 SQLite。数据形状较小且本地化的部分功能存储使用 JSON 文件。

## 安全设计

安全边界：

- Web UI/API 认证基于 JWT，默认启用；浏览器会话将 access 和 refresh token 存入同源 `HttpOnly` cookie。
- 用户角色为 `admin`、`user` 和 `guest`。
- 非管理员用户受配置的 `home_dir` 限制，并可通过 `storage.directory_access_rules` 获得共享目录授权。
- 目录访问规则对 files、search、shares、favorites、trash、activity logs 和 WebDAV users mode 使用相同的最具体路径决策。
- WebDAV 可以认证 MnemoNAS 用户，并应用角色、用户组、`home_dir`、目录访问规则、home 范围用户配额和目录配额边界；旧版 `basic` 模式仍是独立的全局服务凭据。
- 分享链接密码验证使用短期 HttpOnly cookie。
- 下载和预览流程使用短期下载会话 cookie，而不是在 URL 中放置长期 token。

部署边界：

- 保持 dataplane 端口私有。
- 公网访问通过 Caddy、Nginx、Traefik 或其他可信反向代理提供 HTTPS。
- 仅在 MnemoNAS 位于可信代理之后时设置 `server.trusted_proxy_hops`。
- 不要在 loopback-only 开发环境之外禁用认证。

## 前端架构

Web UI 位于 `web/`，使用 React、TypeScript、Vite、HeroUI、Tailwind CSS、Zustand 和 TanStack Query。

UI 围绕重复的文件管理工作流组织：

- 支持列表和网格视图的文件浏览器。
- 上传、下载、重命名、移动、复制、删除和批量操作。
- 版本历史和恢复。
- 回收站浏览和恢复。
- 相册和缩略图。
- 分享、收藏、活动、设置和维护视图。

前端访问 `/api/v1/*`，生产环境与 `nasd` 同源。生产环境中，`nasd` 托管已构建的静态 Web UI，并确保 API、WebDAV、健康检查和直接分享 API 路由优先于 SPA fallback；`index.html` 使用 `Cache-Control: no-cache`，使浏览器在升级后重新校验应用入口。开发环境中，Vite 在 `5173` 提供前端服务，并将 API 调用代理到 `8080`。

## 相关文档

- [存储内部机制](storage-internals.md)
- [配置参考](configuration.md)
- [安全加固](security.md)
- [API 参考](api-reference.md)
- [开发指南](development.md)
