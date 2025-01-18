# Changelog

所有重要变更都会记录在此文件中。

本项目遵循 [语义化版本 2.0.0](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

#### Web UI 功能
- **Dashboard 仪表盘**
  - 系统概览（存储使用、文件数量、版本数量）
  - 最近活动动态
  - 快捷操作入口
  - 数据面健康状态

- **Files 文件管理**
  - 面包屑导航，支持目录层级快速跳转
  - 列表/网格视图切换
  - 文件操作上下文菜单（下载、重命名、复制路径、版本历史、删除）
  - 拖拽上传，支持文件夹
  - 批量操作（批量下载、批量删除）
  - 多文件上传队列面板，显示进度和状态
  - 缩略图预览

- **Album 相册模式**
  - 图片瀑布流布局
  - 自动缩略图生成
  - 沉浸式浏览体验

- **Versions 版本历史**
  - 查看文件所有历史版本
  - 版本时间、大小对比
  - 一键恢复到指定版本

- **Trash 回收站**
  - 已删除文件列表
  - 按删除时间排序
  - 单个/批量恢复
  - 清空回收站

- **Search 全局搜索**
  - 按文件名实时搜索
  - 搜索结果高亮
  - 快速跳转到文件位置

- **Activity 活动日志**
  - 全操作审计记录
  - 按时间/操作类型筛选
  - 操作详情查看
  - 活动统计报表

- **Users 用户管理**
  - 用户列表与状态
  - 创建/编辑/删除用户
  - 密码重置
  - 启用/禁用用户

- **ShareAccess 分享管理**
  - 创建分享链接
  - 密码保护设置
  - 有效期配置
  - 访问统计
  - 分享链接访问页面

- **Settings 系统设置**
  - 服务器配置（监听地址、端口）
  - 存储路径配置（数据目录、元数据目录、临时目录）
  - 版本保留策略（最大版本数、保留时间、空间阈值、GC 间隔）
  - WebDAV 配置（启用开关、URL 前缀、只读模式、用户认证）
  - CDC 分块参数说明
  - 数据面连接状态

- **Health 健康状态**
  - 系统运行时间
  - 数据面连接状态
  - 存储健康检查

- **Maintenance 系统维护**
  - Scrub 数据完整性校验
  - GC 垃圾回收
  - 对象列表浏览
  - 诊断包导出

#### 后端 API
- **认证 API**
  - JWT Token 认证
  - 登录/登出/刷新
  - 密码修改
  - 用户信息获取

- **用户管理 API**
  - 用户 CRUD
  - 密码重置
  - 状态切换

- **分享 API**
  - 分享链接 CRUD
  - 密码验证
  - 公开访问

- **活动日志 API**
  - 活动记录
  - 活动查询
  - 统计接口

- **系统设置 API**
  - 配置读取
  - 配置更新

#### 项目工程化
- GitHub Actions CI/CD 工作流（Go/Rust/Frontend 测试、Docker 构建）
- Release 自动化工作流（多平台二进制构建、Docker 镜像发布）
- CONTRIBUTING.md 贡献指南
- SECURITY.md 安全策略
- pre-commit 配置（代码格式化、lint 检查）
- golangci-lint 配置
- .gitignore 完善

#### 文档完善
- 备份指南（3-2-1 策略、rclone/restic 配置、恢复流程）
- API 参考文档
- README 徽章和快速开始指南

### Changed
- 优化 Files 页面表格列布局，新增操作列
- 优化 Vite 代理配置，添加 `/health` 端点代理
- 改进配置加载逻辑，支持配置路径传递

### Fixed
- 修复 Files.tsx 语法错误（模板字面量、hook 调用）
- 修复 Trash.tsx useCallback 依赖警告
- 修复 utils.ts 控制字符正则 lint 错误
- 移除未使用的导入和变量
- 移除 Git 跟踪的构建产物

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
