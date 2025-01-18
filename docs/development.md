# MnemoNAS 开发指南

本文档详细介绍如何搭建 MnemoNAS 的开发环境，包括各组件的构建、运行和调试方法。

## 目录

- [环境要求](#环境要求)
- [依赖安装](#依赖安装)
- [项目结构](#项目结构)
- [构建流程](#构建流程)
- [本地开发](#本地开发)
- [测试](#测试)
- [调试技巧](#调试技巧)
- [常见问题](#常见问题)

---

## 环境要求

### 必需依赖

| 工具 | 最低版本 | 推荐版本 | 用途 |
|------|---------|---------|------|
| **Go** | 1.21 | 1.25 | Go 控制面开发 |
| **Rust** | 1.75 | 1.92 | Rust 数据面开发 |
| **Node.js** | 20.19 | 22.x | 前端开发 |
| **protoc** | 3.20 | 28.x | Protocol Buffers 编译器 |
| **make** | 3.x | 4.x | 构建自动化 |

### 可选依赖

| 工具 | 用途 |
|------|------|
| Docker & Docker Compose | 容器化部署 |
| golangci-lint | Go 代码静态检查 |
| cargo-watch | Rust 热重载 |
| nvm | Node.js 版本管理 |

---

## 依赖安装

### macOS (Homebrew)

```bash
# 安装 Go
brew install go

# 安装 Rust
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
source ~/.cargo/env

# 安装 Node.js (推荐使用 nvm)
brew install nvm
nvm install 22
nvm use 22

# 安装 protobuf 编译器
brew install protobuf

# 安装 Go protobuf 插件
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# 可选: 代码检查工具
brew install golangci-lint
cargo install cargo-watch
```

### Ubuntu/Debian

```bash
# 更新包管理器
sudo apt update

# 安装 Go (推荐从官网下载最新版)
wget https://go.dev/dl/go1.25.0.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.25.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc
source ~/.bashrc

# 安装 Rust
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
source ~/.cargo/env

# 安装 Node.js (使用 nvm)
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash
source ~/.nvm/nvm.sh
nvm install 22
nvm use 22

# 安装 protobuf 编译器
sudo apt install protobuf-compiler

# 安装 Go protobuf 插件
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# 解决文件监视器限制（前端开发需要）
echo fs.inotify.max_user_watches=524288 | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

### Windows (WSL2 推荐)

建议在 WSL2 Ubuntu 环境下开发，按照上述 Ubuntu 步骤操作。

原生 Windows 开发：
```powershell
# 使用 winget 或 scoop
winget install GoLang.Go
winget install Rustlang.Rust.MSVC
winget install OpenJS.NodeJS.LTS

# 或使用 scoop
scoop install go rust nodejs-lts protobuf
```

### 验证安装

```bash
# 验证版本
go version          # go version go1.25.x ...
rustc --version     # rustc 1.92.x ...
node --version      # v22.x.x
npm --version       # 10.x.x
protoc --version    # libprotoc 28.x

# 验证 Go protobuf 插件
which protoc-gen-go       # 应该在 $GOPATH/bin 下
which protoc-gen-go-grpc
```

---

## 项目结构

```
mnemonas/
├── cmd/nasd/                    # Go 主程序入口
│   └── main.go                  # 程序入口，启动控制面服务
│
├── internal/                    # Go 内部包（不对外暴露）
│   ├── api/                     # REST API
│   │   └── handlers.go          # HTTP handlers
│   ├── config/                  # 配置管理
│   │   └── config.go            # TOML 配置加载
│   ├── webdav/                  # WebDAV 协议实现
│   │   └── handler.go           # RFC 4918 WebDAV handler
│   ├── caslayout/               # CAS 存储布局
│   │   └── layout.go            # 分片目录结构实现
│   └── webdavcas/               # WebDAV-CAS 适配层
│       └── adapter.go           # FileSystem 接口适配
│
├── dataplane/                   # Rust 数据面
│   ├── src/
│   │   ├── main.rs              # 入口，启动 HTTP + gRPC 服务
│   │   ├── cas.rs               # CAS 存储（BLAKE3 哈希）
│   │   ├── cdc.rs               # CDC 分块（FastCDC）
│   │   ├── service.rs           # gRPC 服务实现
│   │   └── proto/               # 生成的 protobuf 代码
│   │       └── mnemonas.dataplane.v1.rs
│   ├── Cargo.toml               # Rust 依赖配置
│   └── build.rs                 # 构建脚本（protobuf 生成）
│
├── web/                         # React 前端
│   ├── src/
│   │   ├── main.tsx             # 入口，Provider 配置
│   │   ├── App.tsx              # 路由配置
│   │   ├── components/          # UI 组件
│   │   │   ├── layout/          # 布局组件
│   │   │   │   ├── Sidebar.tsx
│   │   │   │   ├── Header.tsx
│   │   │   │   └── AppLayout.tsx
│   │   │   └── ThemeToggle.tsx
│   │   ├── pages/               # 页面组件
│   │   │   ├── Dashboard.tsx    # 仪表盘
│   │   │   ├── Files.tsx        # 文件管理器
│   │   │   ├── Album.tsx        # 相册
│   │   │   ├── Versions.tsx     # 版本历史
│   │   │   ├── Storage.tsx      # 存储管理
│   │   │   └── Settings.tsx     # 设置
│   │   ├── stores/              # Zustand 状态管理
│   │   │   ├── theme.ts         # 主题状态
│   │   │   └── files.ts         # 文件状态
│   │   ├── api/                 # API 调用封装
│   │   │   └── files.ts
│   │   └── lib/                 # 工具函数
│   │       └── utils.ts
│   ├── index.html
│   ├── package.json
│   ├── tsconfig.json
│   └── vite.config.ts           # Vite 配置（含代理）
│
├── proto/                       # gRPC 协议定义
│   └── dataplane.proto          # 数据面 RPC 接口
│
├── bin/                         # 构建产物（.gitignore）
│   ├── nasd                     # Go 控制面二进制
│   └── dataplane                # Rust 数据面二进制
│
├── Makefile                     # 构建脚本
├── go.mod                       # Go 模块定义
├── go.sum
├── docker-compose.yml
├── Dockerfile
└── mnemonas.example.toml        # 配置示例
```

---

## 构建流程

### 完整构建

```bash
# 克隆项目
git clone https://github.com/seanbao/mnemonas.git
cd mnemonas

# 安装依赖
make deps
cd web && npm install && cd ..

# 完整构建（proto → Go → Rust）
make build

# 构建产物
ls -la bin/
# -rwxr-xr-x  nasd       # Go 控制面 (~19MB)
# -rwxr-xr-x  dataplane  # Rust 数据面 (~3.5MB)
```

### 分步构建

```bash
# 1. 生成 protobuf 代码
make proto
# 生成文件:
#   proto/dataplane.pb.go
#   proto/dataplane_grpc.pb.go
#   dataplane/src/proto/mnemonas.dataplane.v1.rs

# 2. 构建 Go 控制面
go build -o bin/nasd ./cmd/nasd

# 3. 构建 Rust 数据面
cd dataplane && cargo build --release
cp target/release/dataplane ../bin/

# 4. 构建前端
cd web && npm run build
# 产物在 web/dist/
```

### 开发模式构建

```bash
# 快速构建（debug 模式，不优化）
make dev

# 单独构建各组件
go build -o bin/nasd ./cmd/nasd                    # Go
cd dataplane && cargo build                         # Rust (debug)
cd web && npm run build                             # 前端
```

---

## 本地开发

### 启动所有服务

**终端 1 - Rust 数据面**
```bash
cd dataplane
cargo run -- --data-dir ~/.mnemonas/data --port 9090

# 或使用构建产物
./bin/dataplane --data-dir ~/.mnemonas/data --port 9090
```

**终端 2 - Go 控制面**
```bash
./bin/nasd

# 或直接运行
go run ./cmd/nasd
```

**终端 3 - 前端开发服务器**
```bash
cd web
npm run dev

# 访问 http://localhost:5173
# API 请求会代理到 localhost:8080
```

### 热重载开发

**Go 热重载** (使用 air)
```bash
# 安装 air
go install github.com/air-verse/air@latest

# 创建 .air.toml 或直接运行
air
```

**Rust 热重载** (使用 cargo-watch)
```bash
cargo install cargo-watch
cd dataplane
cargo watch -x run
```

**前端热重载**
```bash
# Vite 默认支持 HMR
cd web && npm run dev
```

### 端口说明

| 服务 | 端口 | 说明 |
|------|------|------|
| Go 控制面 | 8080 | REST API + WebDAV |
| Rust 数据面 | 9090 | HTTP 健康检查 + gRPC |
| 前端开发服务器 | 5173 | Vite dev server |

### 配置文件

开发时使用项目根目录的 `mnemonas.toml`：

```toml
[server]
host = "127.0.0.1"
port = 8080

[storage]
data_dir = "~/.mnemonas/data"
metadata_dir = "~/.mnemonas/meta"

[dataplane]
address = "127.0.0.1:9090"

[webdav]
enabled = true
prefix = "/dav"

[log]
level = "debug"
format = "pretty"
```

---

## 测试

### Go 测试

```bash
# 运行所有测试
go test -v ./...

# 运行特定包测试
go test -v ./internal/webdav/...

# 带覆盖率
go test -v -cover ./...

# 生成覆盖率报告
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Rust 测试

```bash
cd dataplane

# 运行所有测试
cargo test

# 运行特定测试
cargo test test_cas_store

# 显示输出
cargo test -- --nocapture

# 带覆盖率（需要 cargo-tarpaulin）
cargo install cargo-tarpaulin
cargo tarpaulin --out Html
```

### 前端测试

```bash
cd web

# ESLint 检查
npm run lint

# TypeScript 类型检查
npx tsc --noEmit
```

### 集成测试

```bash
# WebDAV 功能测试
curl -X PROPFIND http://localhost:8080/dav/ -H "Depth: 1"

# 上传文件
curl -X PUT http://localhost:8080/dav/test.txt -d "Hello World"

# 下载文件
curl http://localhost:8080/dav/test.txt

# 健康检查
curl http://localhost:8080/health
curl http://localhost:9090/health
```

---

## 调试技巧

### Go 调试

```bash
# 使用 delve
go install github.com/go-delve/delve/cmd/dlv@latest

# 调试模式运行
dlv debug ./cmd/nasd

# VS Code launch.json
{
  "version": "0.2.0",
  "configurations": [
    {
      "name": "Debug nasd",
      "type": "go",
      "request": "launch",
      "mode": "debug",
      "program": "${workspaceFolder}/cmd/nasd"
    }
  ]
}
```

### Rust 调试

```bash
# 使用 lldb 或 gdb
cd dataplane
cargo build
rust-lldb target/debug/dataplane

# VS Code 配置 (需要 CodeLLDB 扩展)
{
  "version": "0.2.0",
  "configurations": [
    {
      "name": "Debug dataplane",
      "type": "lldb",
      "request": "launch",
      "program": "${workspaceFolder}/dataplane/target/debug/dataplane",
      "args": ["--data-dir", "/tmp/mnemonas-test"],
      "cwd": "${workspaceFolder}/dataplane"
    }
  ]
}
```

### 日志级别

```bash
# Go 控制面
export LOG_LEVEL=debug
./bin/nasd

# Rust 数据面
RUST_LOG=debug ./bin/dataplane

# 或在配置文件中设置
[log]
level = "debug"
```

### 网络调试

```bash
# 监控 HTTP 请求
mitmproxy -p 8888

# 查看 gRPC 调用
grpcurl -plaintext localhost:9090 list
grpcurl -plaintext localhost:9090 describe

# 抓包
sudo tcpdump -i lo port 8080 -w debug.pcap
```

---

## 常见问题

### Q: `protoc-gen-go: program not found`

确保 Go bin 目录在 PATH 中：
```bash
export PATH=$PATH:$(go env GOPATH)/bin
```

### Q: 前端开发服务器报 `ENOSPC: System limit for file watchers reached`

增加文件监视器限制：
```bash
echo fs.inotify.max_user_watches=524288 | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

### Q: Rust 编译慢

使用增量编译和 sccache：
```bash
cargo install sccache
export RUSTC_WRAPPER=sccache
```

### Q: Go 模块下载慢

配置 GOPROXY：
```bash
export GOPROXY=https://goproxy.cn,direct
```

### Q: WebDAV 客户端连接失败

1. 确认服务在运行：`curl http://localhost:8080/health`
2. 检查 WebDAV 前缀配置（默认 `/dav`）
3. 某些客户端需要尾部斜杠：`http://localhost:8080/dav/`

### Q: 如何重置开发数据？

```bash
rm -rf ~/.mnemonas
# 重启服务会自动创建目录
```

---

## 代码风格

### Go

遵循官方 Go 代码规范，使用 `gofmt` 格式化：
```bash
go fmt ./...
```

### Rust

使用 `rustfmt` 格式化：
```bash
cd dataplane && cargo fmt
```

### TypeScript/React

使用 ESLint + Prettier：
```bash
cd web && npm run lint
```

---

## 提交规范

使用 Conventional Commits 格式：

```
<type>(<scope>): <description>

[optional body]

[optional footer(s)]
```

类型：
- `feat`: 新功能
- `fix`: 修复 bug
- `docs`: 文档更新
- `style`: 代码格式（不影响功能）
- `refactor`: 重构
- `test`: 测试相关
- `chore`: 构建/工具链

示例：
```
feat(webdav): add ETag support for conditional requests
fix(dataplane): fix memory leak in CDC chunking
docs(readme): update installation instructions
```
