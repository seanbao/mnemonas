# 贡献指南

感谢你对 MnemoNAS 的兴趣！本文档将帮助你了解如何参与项目开发。

## 📋 行为准则

参与本项目即表示你同意遵守以下准则：
- 尊重所有参与者
- 使用包容性语言
- 接受建设性批评
- 关注对社区最有利的事情

## 🚀 快速开始

### 环境要求

| 工具 | 版本 | 用途 |
|------|------|------|
| Go | 1.22+ | 控制面开发 |
| Rust | 1.75+ | 数据面开发 |
| Node.js | 20+ | 前端开发 |
| Docker | 20.10+ | 容器化部署 |
| protoc | 25+ | Protocol Buffers 编译 |

### 克隆仓库

```bash
git clone https://github.com/seanbao/mnemonas.git
cd mnemonas
```

### 安装依赖

```bash
# Go 依赖
go mod download

# Rust 依赖
cd dataplane && cargo fetch && cd ..

# 前端依赖
cd web && npm ci && cd ..

# protoc 插件
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

### 构建项目

```bash
# 完整构建
make build

# 开发模式（debug 构建，更快）
make dev
```

### 运行测试

```bash
# 运行所有测试
make test

# 仅 Go 测试
go test -v ./...

# 仅 Rust 测试
cd dataplane && cargo test

# 前端测试
cd web && npm run test:run
```

## 📝 开发流程

### 1. 创建 Issue

在开始工作之前，请先创建或认领一个 Issue：
- 搜索现有 Issue 避免重复
- 使用合适的模板（bug report / feature request）
- 清晰描述问题或需求

### 2. 创建分支

```bash
# 从 main 创建功能分支
git checkout main
git pull origin main
git checkout -b feature/your-feature-name

# 或修复分支
git checkout -b fix/issue-description
```

分支命名规范：
- `feature/xxx` - 新功能
- `fix/xxx` - Bug 修复
- `docs/xxx` - 文档更新
- `refactor/xxx` - 代码重构
- `test/xxx` - 测试相关

### 3. 编写代码

#### 代码风格

**Go:**
- 使用 `gofmt` 格式化
- 遵循 [Effective Go](https://golang.org/doc/effective_go.html)
- 注释使用英文

**Rust:**
- 使用 `cargo fmt` 格式化
- 遵循 [Rust API Guidelines](https://rust-lang.github.io/api-guidelines/)
- 注释使用英文

**TypeScript/React:**
- 使用 ESLint 和 Prettier
- 优先使用函数组件和 Hooks
- 组件使用 PascalCase 命名

**文档:**
- 中文文档使用中文，技术术语保留英文
- 中英文之间添加空格（如 `使用 Rust 编写`）

#### 提交规范

使用 [Conventional Commits](https://www.conventionalcommits.org/zh-hans/):

```
<type>(<scope>): <description>

[optional body]

[optional footer(s)]
```

类型：
- `feat`: 新功能
- `fix`: Bug 修复
- `docs`: 文档更新
- `style`: 代码格式（不影响功能）
- `refactor`: 重构
- `test`: 测试相关
- `chore`: 构建/工具变更

示例：
```
feat(webdav): add COPY method support

- Implement RFC 4918 COPY operation
- Add tests for directory copy
- Update documentation

Closes #123
```

### 4. 提交 Pull Request

1. 确保所有测试通过
2. 更新相关文档
3. 填写 PR 模板
4. 等待 Code Review

PR 检查清单：
- [ ] 代码通过 lint 检查
- [ ] 添加或更新了测试
- [ ] 文档已更新
- [ ] CHANGELOG 已更新（如适用）
- [ ] Commit message 符合规范

## 🏗️ 项目结构

```
mnemonas/
├── cmd/nasd/           # Go 主程序入口
├── internal/           # Go 内部包
│   ├── api/            # REST API
│   ├── webdav/         # WebDAV 协议实现
│   ├── webdavcas/      # WebDAV-CAS 适配层
│   ├── caslayout/      # CAS 存储布局
│   ├── config/         # 配置管理
│   ├── maintenance/    # 维护任务
│   ├── metrics/        # 指标收集
│   └── thumbnail/      # 缩略图服务
├── dataplane/          # Rust 数据面
│   └── src/
│       ├── cas.rs      # CAS 存储引擎
│       ├── cdc.rs      # CDC 分块算法
│       └── service.rs  # gRPC 服务
├── web/                # React 前端
│   └── src/
│       ├── pages/      # 页面组件
│       ├── components/ # 公共组件
│       ├── api/        # API 调用
│       └── stores/     # 状态管理
├── proto/              # Protocol Buffers 定义
├── docs/               # 文档
└── scripts/            # 脚本工具
```

## 🔧 常用命令

```bash
# 格式化代码
make fmt

# 代码检查
make lint

# 生成 protobuf
make proto

# 清理构建产物
make clean

# 构建 Docker 镜像
make docker
```

## 📚 相关资源

- [架构设计](docs/architecture.md)
- [开发指南](docs/development.md)
- [API 文档](docs/README.md)
- [设计文档](https://github.com/seanbao/ideas-lab-notes/blob/main/ideas/open-source-nas-go-rust.md)

## ❓ 获取帮助

- 在 [Discussions](https://github.com/seanbao/mnemonas/discussions) 提问
- 在 [Issues](https://github.com/seanbao/mnemonas/issues) 报告 Bug
- 查看 [FAQ](docs/faq.md)

## 🙏 致谢

感谢所有贡献者！

---

*Made with ❤️ by the MnemoNAS community*
