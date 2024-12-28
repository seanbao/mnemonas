# Changelog

所有重要变更都会记录在此文件中。

本项目遵循 [语义化版本 2.0.0](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added
- **Files 页面增强**
  - 面包屑导航，支持目录层级快速跳转
  - 文件操作上下文菜单（下载、重命名、复制路径、版本历史、删除）
  - 网格视图模式，支持列表/网格切换
  - 空目录友好提示文案
  - 拖拽上传支持，可直接拖放文件到页面上传
  - 批量操作支持（批量下载、批量删除）
  - 多文件上传队列面板，显示上传进度和状态
- **Settings 设置页面**
  - 服务器配置（监听地址、端口）
  - 存储路径配置（数据目录、元数据目录、临时目录）
  - 版本保留策略（最大版本数、保留时间、空间阈值、GC 间隔）
  - WebDAV 配置（启用开关、URL 前缀、只读模式、用户认证）
  - CDC 分块参数说明与数据面连接状态
- **项目工程化**
  - GitHub Actions CI/CD 工作流（Go/Rust/Frontend 测试、Docker 构建）
  - Release 自动化工作流（多平台二进制构建、Docker 镜像发布）
  - CONTRIBUTING.md 贡献指南
  - SECURITY.md 安全策略
  - pre-commit 配置（代码格式化、lint 检查）
  - golangci-lint 配置
- **文档完善**
  - 备份指南（3-2-1 策略、rclone/restic 配置、恢复流程）
  - README 徽章和快速开始指南

### Changed
- 优化 Files 页面表格列布局，新增操作列
- 优化 Vite 代理配置，添加 `/health` 端点代理

### Fixed
- 修复 Files.tsx 语法错误（模板字面量、hook 调用）
- 修复 Trash.tsx useCallback 依赖警告
- 修复 utils.ts 控制字符正则 lint 错误
- 移除未使用的导入和变量

---

## [0.1.0] - 2024-XX-XX

首个公开发布版本。

### Added

#### 核心功能
- **CAS 存储引擎**：基于 BLAKE3 哈希的内容寻址存储
- **CDC 分块**：使用 FastCDC 算法实现智能分块（256KB-4MB）
- **版本管理**：所有文件自动保留历史版本，支持一键恢复
- **软删除**：删除操作仅移除引用，数据由 GC 异步清理

#### WebDAV 协议
- 完整 RFC 4918 实现（PROPFIND, GET, PUT, DELETE, MKCOL, COPY, MOVE）
- 虚拟锁实现（LOCK/UNLOCK）
- Basic Auth 认证
- 兼容主流客户端（macOS Finder, Windows Explorer, Transmit, rclone 等）

#### 性能优化
- PROPFIND 响应缓存（30 秒 TTL）
- 请求指标收集与统计
- 流式文件传输，支持任意大小文件

#### 运维功能
- 健康检查端点
- Scrub 数据完整性检查
- GC 垃圾回收
- 诊断信息导出

#### 部署
- Docker / Docker Compose 支持
- Linux / macOS 二进制分发
- TOML 配置文件

### 已知限制

- LOCK/UNLOCK 为虚拟实现，多客户端并发编辑同一文件时需注意
- Windows WebClient 需要修改注册表以支持 HTTP（非 HTTPS）连接
- 暂不支持多用户独立权限（计划在 0.2.0 实现）

### 兼容性

- **Go**: 1.22+
- **Rust**: 1.75+
- **Docker**: 20.10+
- **支持平台**: Linux (x86_64, ARM64), macOS (Intel, Apple Silicon)

---

## 版本发布检查清单

发布新版本前，请确认：

- [ ] 所有测试通过：`make test`
- [ ] 更新 CHANGELOG.md
- [ ] 更新 README.md 中的版本号（如有）
- [ ] 创建 Git tag：`git tag -a v0.1.0 -m "Release v0.1.0"`
- [ ] 推送 tag：`git push origin v0.1.0`
- [ ] GitHub Release 包含：
  - 版本说明（从 CHANGELOG 复制）
  - 二进制文件（Linux x86_64, ARM64, macOS）
  - Docker 镜像标签
- [ ] 更新文档站点（如有）

---

## 版本号规则

- **MAJOR** (X.0.0)：不兼容的 API 变更
- **MINOR** (0.X.0)：向后兼容的功能新增
- **PATCH** (0.0.X)：向后兼容的问题修复

### 预发布版本

- `0.1.0-alpha.1`：Alpha 版本，功能不完整
- `0.1.0-beta.1`：Beta 版本，功能完整但可能有 bug
- `0.1.0-rc.1`：Release Candidate，准备正式发布

---

[Unreleased]: https://github.com/seanbao/mnemonas/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/seanbao/mnemonas/releases/tag/v0.1.0
