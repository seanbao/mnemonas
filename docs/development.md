# MnemoNAS 开发指南

[English](development.en.md) | 简体中文

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
| ---- | -------- | -------- | ---- |
| **Go** | 1.25.9 | 1.25.9+ | Go 控制面开发 |
| **Rust** | 1.92 | 1.92.x | Rust 数据面开发 |
| **Node.js** | `^20.19.0` 或 `>=22.12.0` | `.nvmrc` 指定的 22.x | 前端开发 |
| **protoc** | 3.20 | 3.20.1（CI 固定） | 重新生成 protobuf 代码；普通 dataplane/Docker 构建使用已提交生成代码 |
| **make** | 3.x | 4.x | 构建自动化 |

### 可选依赖

| 工具 | 用途 |
| ---- | ---- |
| Docker Engine + Compose v2 插件 | 容器化部署，需支持 `docker compose` 命令 |
| golangci-lint | Go 代码静态检查；`make lint` / `make check` 默认要求安装 |
| cargo-watch | Rust 热重载 |
| nvm | Node.js 版本管理 |

项目根目录 `.go-version` 和 `.nvmrc` 分别提示 Go 与 Node.js 开发版本，Rust 版本要求写在 `dataplane/Cargo.toml` 的 `rust-version` 字段中。前端相关命令默认通过 `nvm use` 进入 `.nvmrc` 指定版本执行，并由 `web/package.json` 与 `web/scripts/check-node.cjs` 校验实际 Node.js engine 范围。

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

# 安装 protobuf 编译器（make proto / make build 需要；普通 Docker 构建不需要）
brew install protobuf

# 安装 Go protobuf 插件
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1

# 代码检查工具（make lint / make check 需要）
brew install golangci-lint
cargo install cargo-watch --version 8.5.3
```

### Ubuntu/Debian

```bash
# 更新包管理器
sudo apt update

# 安装 Go (推荐从 https://go.dev/dl/ 选择 1.25.9 或更新的 1.25.x 补丁版本)
GO_VERSION=1.25.9
wget "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
sudo tar -C /usr/local -xzf "go${GO_VERSION}.linux-amd64.tar.gz"
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

# 安装 protobuf 编译器（make proto / make build 需要；普通 Docker 构建不需要）
sudo apt install protobuf-compiler
protoc --version
# 如果系统仓库版本低于 3.20，请改用发行版 backports 或官方预编译包安装新版 protoc。
# CI 使用 protoc 3.20.1 以保持已提交 Go 生成文件头部稳定；提交 proto 生成文件前建议使用同一版本。

# 安装 Go protobuf 插件
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1

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
node --version      # v22.12.0 或更新的 22.x
npm --version       # 10.x.x
protoc --version    # libprotoc 3.20+（CI 固定 3.20.1）

# 验证 Go protobuf 插件
which protoc-gen-go       # 应该在 $GOPATH/bin 下
which protoc-gen-go-grpc

# 加载仓库要求的 Node.js 版本
source ~/.nvm/nvm.sh
nvm use
```

---

## 项目结构

```text
mnemonas/
├── cmd/nasd/                    # Go 主程序入口
│   └── main.go                  # 程序入口，启动控制面服务
│
├── internal/                    # Go 内部包（不对外暴露）
│   ├── api/                     # REST API
│   │   ├── server.go            # HTTP 路由与主处理器
│   │   ├── errors.go            # 统一错误响应
│   │   └── limits.go            # 请求体 / 资源限制
│   ├── config/                  # 配置管理
│   │   └── config.go            # TOML 配置加载
│   ├── webdav/                  # WebDAV 协议实现
│   │   └── handler.go           # RFC 4918 WebDAV handler
│   ├── caslayout/               # CAS 存储布局
│   │   └── layout.go            # 分片目录结构实现
│   ├── workspace/               # 原生文件工作区操作
│   │   └── workspace.go         # 本地文件读写 / 路径清理 / 原子写入
│   ├── storage/                 # 对外统一文件系统
│   │   └── storage.go           # workspace + versionstore 组合层
│   └── versionstore/            # 版本历史与回收站元数据
│       └── store.go             # SQLite 版本记录 / CAS 索引
│
├── dataplane/                   # Rust 数据面
│   ├── src/
│   │   ├── main.rs              # 入口，启动 HTTP + gRPC 服务
│   │   ├── cas.rs               # CAS 存储（BLAKE3 哈希）
│   │   ├── cdc.rs               # CDC 分块（FastCDC）
│   │   ├── service.rs           # gRPC 服务实现
│   ├── Cargo.toml               # Rust 依赖配置
│   │   └── proto/               # 已提交的 Rust protobuf 生成代码
│   └── build.rs                 # 只声明 proto 变更依赖，普通构建不运行 protoc
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

# 完整构建（proto → Web → Go → Rust）
make build

# 构建产物
ls -la bin/
# -rwxr-xr-x  nasd       # Go 控制面 (~19MB)
# -rwxr-xr-x  dataplane  # Rust 数据面 (~3.5MB)
# 前端产物位于 web/dist/
```

### 分步构建

```bash
# 1. 生成 protobuf 代码
make proto
# 生成文件:
#   proto/dataplane.pb.go
#   proto/dataplane_grpc.pb.go
#   dataplane/src/proto/mnemonas.dataplane.v1.rs
# 普通 cargo build 直接使用已提交的 Rust 生成代码，不需要 protoc。

# 2. 构建 Go 控制面
CGO_ENABLED=0 go build -o bin/nasd ./cmd/nasd

# 3. 构建 Rust 数据面
cd dataplane && cargo build --release --locked
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
CGO_ENABLED=0 go build -o bin/nasd ./cmd/nasd     # Go
cd dataplane && cargo build                       # Rust (debug)
cd web && npm run build                           # 前端
```

---

## 本地开发

### 脚本启动（推荐）

使用 `scripts/dev.sh` 脚本可以启动完整的开发环境：

```bash
source ~/.nvm/nvm.sh
nvm use
```

未安装或未加载 `nvm` 时，`./scripts/dev.sh --frontend` 会直接退出并提示修复方式。

```bash
# 启动所有组件（构建 + dataplane + nasd + 前端）
./scripts/dev.sh

# 仅启动后端
./scripts/dev.sh --backend   # 或 -b

# 仅启动前端
./scripts/dev.sh --frontend  # 或 -f

# 查看服务状态
./scripts/dev.sh --status    # 或 -s

# 查看初始密码文件和 WebDAV 凭据位置；默认不打印明文 WebDAV 密码
./scripts/dev.sh --creds     # 或 -c

# 停止所有组件
./scripts/dev.sh --kill      # 或 -k
```

脚本特性：

- `--creds` 默认隐藏 WebDAV 明文密码；确需在本机终端显示时，使用 `MNEMONAS_DEV_SHOW_SECRETS=1 ./scripts/dev.sh --creds`

- **自动构建**：启动前自动构建 Go 和 Rust 组件
- **端口检测**：避免重复启动，自动跳过已占用的端口
- **健康检查**：等待服务就绪后再继续
- **日志管理**：所有日志写入 `logs/` 目录
- **PID 跟踪**：使用 `.pids/` 目录跟踪进程，支持干净停止
- **Node.js 版本**：通过 `nvm use` 使用项目根 `.nvmrc`，并校验当前 Node.js 满足前端依赖的 engine 要求

启动后的服务状态表：

```text
┌─────────────┬────────┬──────────────────────────────────┐
│ 组件        │ 状态   │ 地址                             │
├─────────────┼────────┼──────────────────────────────────┤
│ dataplane   │ ✅ 运行 │ HTTP:9091 gRPC:9090              │
│ nasd        │ ✅ 运行 │ http://127.0.0.1:8080            │
│ frontend    │ ✅ 运行 │ http://127.0.0.1:5173            │
└─────────────┴────────┴──────────────────────────────────┘
```

### 分组件启动

如果需要更细粒度的控制，可以分别启动各组件：

#### 终端 1 - Rust 数据面

```bash
cd dataplane
cargo run -- --data-dir ~/.mnemonas/.mnemonas/objects --grpc 127.0.0.1:9090 --listen 127.0.0.1:9091

# 或使用构建产物
./bin/dataplane --data-dir ~/.mnemonas/.mnemonas/objects --grpc 127.0.0.1:9090 --listen 127.0.0.1:9091
```

#### 终端 2 - Go 控制面

```bash
./bin/nasd

# 或直接运行
go run ./cmd/nasd
```

`nasd` 只会托管已构建的前端产物。开发时推荐使用下面的 Vite dev server；如果需要验证单进程静态 Web UI，请先在 `web/` 下执行 `npm run build`，或显式设置 `MNEMONAS_WEB_DIR=web/dist`。

#### 终端 3 - 前端开发服务器

```bash
source ~/.nvm/nvm.sh
nvm use

cd web
npm run dev

# 访问 http://localhost:5173
# API 请求会代理到 localhost:8080
```

### 热重载开发

#### Go 热重载 (使用 air)

```bash
# 安装 air
AIR_VERSION=v1.65.1
go install "github.com/air-verse/air@${AIR_VERSION}"

# 创建 .air.toml 或直接运行
air
```

#### Rust 热重载 (使用 cargo-watch)

```bash
CARGO_WATCH_VERSION=8.5.3
cargo install cargo-watch --version "${CARGO_WATCH_VERSION}"
cd dataplane
cargo watch -x run
```

#### 前端热重载

```bash
# Vite 默认支持 HMR
cd web && npm run dev
```

### 端口说明

| 服务 | 端口 | 说明 |
| ---- | ---- | ---- |
| Go 控制面 (nasd) | 8080 | REST API + WebDAV |
| Rust 数据面 HTTP | 9091 | 健康检查 + 统计信息 |
| Rust 数据面 gRPC | 9090 | CAS 存储服务 |
| 前端开发服务器 | 5173 | Vite dev server |

### 配置文件

开发时使用 `~/.mnemonas/config.toml`：

```toml
[server]
host = "127.0.0.1"
port = 8080

[storage]
root = "~/.mnemonas"

[dataplane]
grpc_address = "127.0.0.1:9090"

[webdav]
enabled = true
prefix = "/dav"

[log]
level = "debug"
format = "console"
```

---

## 测试

统一入口：

```bash
# 运行全部测试（Go + Rust + 前端单测）
make test

# 深度测试矩阵（Go race/fuzz + 前端 property + Playwright 交互完整性）
make test-torture
```

`make test-torture` 默认只运行非破坏性测试。它会覆盖控制面竞态、Go fuzz 种子、前端属性测试，以及真实浏览器里的人类交互路径和运行时完整性扫描；耗时比 `make test` 更长，适合发布前、重构后或排查隐性问题时使用。需要缩短本地验证时可临时覆盖：

```bash
GO_FUZZTIME=2s RUN_GO_RACE=0 RUN_E2E_TORTURE=0 make test-torture
```

### Go 测试

```bash
# 运行所有测试（自动启动临时 dataplane）
GO_PACKAGES=$(make --no-print-directory go-packages)
bash ./scripts/with-test-dataplane.sh go test -v $GO_PACKAGES

# 运行特定包测试（需要 dataplane 的包同样通过包装脚本执行）
bash ./scripts/with-test-dataplane.sh go test -v ./internal/webdav/...

# 带覆盖率
bash ./scripts/with-test-dataplane.sh go test -v -cover $GO_PACKAGES

# 生成覆盖率报告
bash ./scripts/with-test-dataplane.sh go test -coverprofile=coverage.out $GO_PACKAGES
go tool cover -html=coverage.out
```

不要在安装前端依赖后直接用 `go test ./...` 或 `go list ./...` 作为全仓库包集合；Go 会进入 `web/node_modules` 中第三方包。仓库级 Go 检查应先用 `make --no-print-directory go-packages` 解析包列表。

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

> 前端工具链需 Node.js `^20.19.0` 或 `>=22.12.0`；推荐使用项目 `.nvmrc` 指定的 22.x。

```bash
cd web

# 版本检查
npm run check:node

# 运行前端单元测试（一次性）
npm run test:run

# 运行前端单元测试（watch）
npm run test

# 覆盖率
npm run test:coverage

# ESLint 检查
npm run lint

# TypeScript 类型检查
npx tsc --noEmit

# Playwright E2E
npm run test:e2e

# Playwright E2E（UI）
npm run test:e2e:ui
```

`web/playwright.config.ts` 默认会启动隔离的测试后端和 Vite 前端，不依赖当前开发服务器。`MNEMONAS_E2E_BACKEND_URL` 和 `MNEMONAS_E2E_FRONTEND_URL` 可用于调整隔离测试服务器的地址或端口；需要连接已有服务时，同时设置 `MNEMONAS_E2E_REUSE_EXISTING=1`、`MNEMONAS_E2E_BACKEND_URL`、`MNEMONAS_E2E_FRONTEND_URL` 和 `E2E_PASSWORD`。

### 集成测试

```bash
# 当前默认配置启用 WebDAV Basic Auth；可通过 ./scripts/dev.sh --creds 查看凭据位置
WEBDAV_USER="<webdav-username>"
WEBDAV_PASS="<webdav-password>"

# WebDAV 功能测试
curl -u "$WEBDAV_USER:$WEBDAV_PASS" -X PROPFIND http://localhost:8080/dav/ -H "Depth: 1"

# 上传文件
curl -u "$WEBDAV_USER:$WEBDAV_PASS" -X PUT http://localhost:8080/dav/test.txt -d "Hello World"

# 下载文件
curl -u "$WEBDAV_USER:$WEBDAV_PASS" http://localhost:8080/dav/test.txt

# 健康检查
curl http://localhost:8080/health           # nasd
curl http://localhost:9091/health           # dataplane HTTP
curl http://localhost:9091/stats            # dataplane 统计
```

### E2E 验收测试

默认使用隔离后端运行完整的端到端测试，避免误写本机正在运行的 MnemoNAS 服务：

```bash
# 完整测试（包含大文件和崩溃恢复测试）
make e2e

# 快速测试（跳过耗时测试）
./scripts/run-e2e-isolated.sh --quick
```

`scripts/e2e-test.sh` 仍可用于手动验证一个已启动的服务；这种模式必须显式传入 `BASE_URL`、`STORAGE_ROOT`、`CONFIG_FILE`、`SECRETS_FILE` 和 `INITIAL_PASSWORD_FILE`，防止测试数据写入真实存储。

脚本会在认证测试阶段自动尝试使用 bootstrap admin 凭据登录；启用认证但当前环境没有可用 bootstrap 凭据时，依赖管理员权限的 maintenance / diagnostics 检查会标记为 `skip`，避免把权限前置条件误报成产品故障。

测试覆盖：

- 基础功能：健康检查、版本 API、WebDAV OPTIONS
- 文件操作：PUT/GET/DELETE/MKCOL/COPY/MOVE
- ETag 条件请求：If-None-Match/If-Match
- 版本历史 API
- 并发读写测试
- 维护操作：Scrub/Metrics/Diagnostics
- 安全测试：路径穿越防护
- 认证测试：登录/继续/令牌刷新

### 故障注入测试

`scripts/fault-injection-test.sh` 会杀死并重启 `nasd`、写入测试文件，并可直接损坏对象和元数据文件。它默认关闭，不能直接对真实数据目录运行；必须显式指定隔离测试实例：

```bash
MNEMONAS_LIVE_FAULTS=1 \
BASE_URL=http://127.0.0.1:18080 \
STORAGE_ROOT=/tmp/mnemonas-fault-target \
NASD_BIN="$PWD/bin/nasd" \
FAULT_INJECTION_ASSUME_YES=1 \
RUN_CORRUPTION_TESTS=0 \
./scripts/fault-injection-test.sh
```

安全门禁由 `scripts/test-fault-injection-safety.sh` 覆盖，并纳入 `make scripts-check`。脚本要求 `BASE_URL`、`STORAGE_ROOT`、`NASD_BIN` 都来自显式环境变量；默认只允许 `/tmp` 或当前 checkout 下的 `STORAGE_ROOT`，需要真实存储路径时必须额外设置 `ALLOW_REAL_STORAGE=1`。

### 性能基准测试

默认使用隔离后端测试 WebDAV PROPFIND 性能，避免在真实 `storage.root` 下创建和删除基准测试文件：

```bash
make bench

# 或显式调用隔离包装脚本
./scripts/run-benchmark-isolated.sh
```

`scripts/benchmark.sh` 可以手动打一个已启动的本地服务；这种模式必须显式提供服务地址和对应的本地 `storage.root`：

```bash
MNEMONAS_STORAGE_ROOT=/tmp/mnemonas-bench-target \
./scripts/benchmark.sh http://127.0.0.1:18080

# 真实存储路径需要额外确认
ALLOW_REAL_STORAGE=1 \
MNEMONAS_STORAGE_ROOT=/data/mnemonas \
./scripts/benchmark.sh http://192.168.1.100:8080

# 若未使用默认 WebDAV 凭据或需要显式抓取受保护的 metrics
MNEMONAS_WEBDAV_USERNAME="webdav" \
MNEMONAS_WEBDAV_PASSWORD="secret" \
MNEMONAS_ACCESS_TOKEN="<access-token>" \
MNEMONAS_STORAGE_ROOT=/tmp/mnemonas-bench-target \
./scripts/benchmark.sh http://127.0.0.1:18080
```

脚本会在 `storage.root/files/benchmark-test` 下创建真实测试文件，退出时删除该目录，并优先从 `config.toml` / `secrets.json` 自动读取 WebDAV Basic Auth。默认只允许 `/tmp` 或当前 checkout 下的 `MNEMONAS_STORAGE_ROOT`；真实存储路径必须额外设置 `ALLOW_REAL_STORAGE=1`。若认证已初始化且 bootstrap admin 凭据不可用，可通过 `MNEMONAS_ACCESS_TOKEN` 显式传入管理员 token；否则 metrics 段会标记为 `skip`。若 WebDAV `PROPFIND` 返回非 `207 Multi-Status`，脚本会立即退出，而不是继续输出无效耗时。

测试内容：

- 不同目录大小的 PROPFIND 响应时间（10/100/500/1000 文件）
- 缓存效果测试（冷启动 vs 缓存后）
- API 指标统计

---

## 调试技巧

### Go 调试

```bash
# 使用 delve
DELVE_VERSION=v1.26.3
go install "github.com/go-delve/delve/cmd/dlv@${DELVE_VERSION}"

# 调试模式运行
dlv debug ./cmd/nasd

# VS Code launch.json
```

```json
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
```

```json
{
  "version": "0.2.0",
  "configurations": [
    {
      "name": "Debug dataplane",
      "type": "lldb",
      "request": "launch",
      "program": "${workspaceFolder}/dataplane/target/debug/dataplane",
      "args": ["--data-dir", "${env:HOME}/.mnemonas/.mnemonas/objects"],
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

### Q: Go 尝试下载 toolchain 但网络失败

仓库使用 `toolchain go1.25.9` 固定 CI 和 release 的补丁版本。若本机已经安装兼容的 Go 1.25.x，但网络无法下载指定 toolchain，可临时使用本机工具链运行本地检查：

```bash
packages=$(GOTOOLCHAIN=local make --no-print-directory go-packages)
GOTOOLCHAIN=local go test $packages
GOTOOLCHAIN=local make build
```

`GOTOOLCHAIN=local` 只适合本地临时验证。发布构建和安全扫描必须使用 `go1.25.9` 或更新的 1.25.x 补丁版本；低于该版本的本地工具链会被 `govulncheck` 报出标准库漏洞。Playwright 隔离后端默认会使用 `GOTOOLCHAIN=local`，避免 E2E 因 toolchain 下载超时而无法启动。

如果下载失败并提示 `checksum database disabled by GOSUMDB=off`，说明本机环境禁用了 Go checksum database，toolchain 模块无法完成校验。发布构建和安全扫描不要带这个覆盖值，可临时这样运行：

```bash
GOSUMDB=sum.golang.org go version
GOSUMDB=sum.golang.org govulncheck ./...
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

排查步骤：

1. 确认服务在运行：`curl http://localhost:8080/health`
2. 检查 WebDAV 前缀配置（默认 `/dav`）
3. 某些客户端需要尾部斜杠：`http://localhost:8080/dav/`

### Q: 如何重置开发数据？

```bash
DEFAULT_DATA_DIR="$HOME/.mnemonas"
DATA_DIR="${MNEMONAS_DATA_DIR:-$DEFAULT_DATA_DIR}"
[ "$DATA_DIR" = "$DEFAULT_DATA_DIR" ] || { echo "refusing non-default DATA_DIR; inspect and delete manually: $DATA_DIR"; exit 1; }
[ ! -L "$DATA_DIR" ] || { echo "refusing symlink DATA_DIR: $DATA_DIR"; exit 1; }
rm -rf -- "$DATA_DIR"
# 重启服务会自动创建目录
```

### Q: 如何调整 dataplane 端口？

dataplane 同时监听两个端口：

- HTTP 端口（默认 9091）：健康检查和统计信息
- gRPC 端口（默认 9090）：CAS 存储服务

可通过命令行参数调整：

```bash
./bin/dataplane --listen 127.0.0.1:9091 --grpc 127.0.0.1:9090
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

使用 ESLint 和 TypeScript 构建检查：

```bash
cd web && npm run lint
cd web && npm run build
```

---

## 提交规范

使用 Conventional Commits 格式：

```text
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

```text
feat(webdav): add ETag support for conditional requests
fix(dataplane): fix memory leak in CDC chunking
docs(readme): update installation instructions
```
