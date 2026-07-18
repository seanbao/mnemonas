# MnemoNAS 开发指南

[English](development.en.md) | 简体中文

本文档说明 MnemoNAS 本地开发环境、构建流程、测试入口和调试方法。

## 环境要求

| 工具 | 最低版本 | 推荐版本 | 用途 |
| --- | --- | --- | --- |
| Go | 1.25.12 | 1.25.12+ | Go 控制面 |
| Rust | 1.92 | 1.92.x | Rust 数据面与 protobuf 生成器 |
| Node.js | `^20.19.0` 或 `>=22.12.0` | `.nvmrc` 指定的 22.x | 前端 |
| Flutter | 3.44.4 | 3.44.4 | Android、Linux 和 Windows 客户端 |
| JDK | 17 | 17 | Android 客户端 Gradle 构建 |
| protoc | 3.20 | CI 对齐版本 3.20.1 | 重新生成 protobuf 代码 |
| make | 3.x | 4.x | 构建自动化 |

可选工具：

| 工具 | 用途 |
| --- | --- |
| Docker Engine + Compose v2 | 容器构建和部署 |
| golangci-lint | `make lint` 和 `make check` 默认要求安装，除非显式跳过 |
| Python 3 | `make verify-changed` 中的未跟踪文本空白检查和本地校验脚本 |
| PyYAML | `make verify-changed`、`make workflows-check` 和 `make docs-check` 中的 YAML 语法和重复键校验 |
| `timeout` 或 `gtimeout` | Docker 变更触发 `make verify-changed` 镜像构建和容器烟测时限制最长运行时间；macOS 可通过 GNU coreutils 提供 `gtimeout` |
| cargo-watch | Rust 热重载 |
| nvm | Node.js 版本管理 |

仓库包含 `.go-version` 和 `.nvmrc`。Rust 版本在 `dataplane/Cargo.toml` 和 `tools/proto-gen/Cargo.toml` 中声明。前端命令应在 `nvm use` 后运行。

## 安装依赖

### macOS

```bash
brew install go

curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs -o /tmp/rustup-init.sh
sed -n '1,120p' /tmp/rustup-init.sh
sh /tmp/rustup-init.sh
source ~/.cargo/env

brew install nvm
nvm install 22
nvm use 22

brew install protobuf

go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1

brew install golangci-lint python
python3 -m pip install --user PyYAML
cargo install cargo-watch --version 8.5.3
```

### Ubuntu / Debian

```bash
sudo apt update

GO_VERSION=1.25.12
wget "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
sudo tar -C /usr/local -xzf "go${GO_VERSION}.linux-amd64.tar.gz"
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc
source ~/.bashrc

curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs -o /tmp/rustup-init.sh
sed -n '1,120p' /tmp/rustup-init.sh
sh /tmp/rustup-init.sh
source ~/.cargo/env

curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh -o /tmp/nvm-install.sh
sed -n '1,120p' /tmp/nvm-install.sh
bash /tmp/nvm-install.sh
source ~/.nvm/nvm.sh
nvm install 22
nvm use 22

sudo apt install protobuf-compiler
protoc --version

sudo apt install python3-yaml

go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1

echo fs.inotify.max_user_watches=524288 | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

如果发行版 `protoc` 低于 3.20，应使用发行版 backport 或官方预编译包。CI 使用 3.20.1，以保持已提交 Go 生成文件头部稳定。

### Windows

推荐在 WSL2 Ubuntu 环境开发。原生 Windows 环境可通过 winget 或 scoop 安装 Go、Rust、Node.js 和 protobuf。

## 验证工具链

```bash
go version
rustc --version
node --version
npm --version
protoc --version
python3 --version
python3 -c 'import yaml'

which protoc-gen-go
which protoc-gen-go-grpc

source ~/.nvm/nvm.sh
nvm use
```

## 仓库结构

```text
mnemonas/
├── cmd/nasd/              # Go 控制面入口
├── internal/              # Go 内部包
│   ├── api/               # REST API
│   ├── auth/              # 用户、JWT、密码
│   ├── config/            # TOML 配置
│   ├── storage/           # 存储编排
│   ├── versionstore/      # 版本、回收站、元数据
│   ├── webdav/            # WebDAV 实现
│   └── workspace/         # 原生文件操作
├── dataplane/             # Rust 数据面
├── web/                   # React 前端
├── client/                # Flutter Android、Linux 和 Windows 客户端
├── proto/                 # gRPC 协议定义
├── scripts/               # 开发、测试和部署辅助脚本
├── docs/                  # 文档
├── docker-compose.yml
├── Dockerfile
├── Makefile
└── mnemonas.example.toml
```

## 构建

完整构建：

```bash
git clone https://github.com/seanbao/mnemonas.git
cd mnemonas

make deps
make build
```

构建产物：

```text
bin/nasd
bin/dataplane
web/dist/
```

分步构建：

```bash
make proto

CGO_ENABLED=0 go build -o bin/nasd ./cmd/nasd

cd dataplane && cargo build --release --locked
cp target/release/dataplane ../bin/

cd web && npm run build
```

普通 Rust 和 Docker 构建使用已提交的 Rust protobuf 生成代码，不需要 `protoc`，除非 protobuf 文件被重新生成。

快速 debug 构建：

```bash
make dev
```

## 本地开发

通常使用开发脚本：

```bash
source ~/.nvm/nvm.sh
nvm use

./scripts/dev.sh
./scripts/dev.sh --backend
./scripts/dev.sh --creds # 显示 WebDAV 认证模式；默认隐藏 Basic Auth 明文密码
./scripts/dev.sh --frontend
./scripts/dev.sh --status
./scripts/dev.sh --kill
```

脚本行为：

- `--creds` 显示初始密码文件和当前 WebDAV 认证模式。
- `users` 模式使用 MnemoNAS 账号。
- `none` 表示 WebDAV 认证关闭。
- `basic` 默认隐藏明文 Basic Auth 密码。
- 只有明确需要在本机终端披露时，才使用 `MNEMONAS_DEV_SHOW_SECRETS=1 ./scripts/dev.sh --creds`。
- 构建 Go 和 Rust 组件。
- 启动 dataplane、`nasd` 和可选 Vite。
- 检查端口和服务就绪状态。
- 将日志写入 `logs/`。
- 将进程 ID 写入 `.pids/`。
- 前端启动前强制校验 Node.js engine。

### 手动启动组件

终端 1：

```bash
cd dataplane
cargo run -- --data-dir ~/.mnemonas/.mnemonas/objects --grpc 127.0.0.1:9090 --listen 127.0.0.1:9091
```

终端 2：

```bash
go run ./cmd/nasd
# 或使用已构建二进制
./bin/nasd
```

终端 3：

```bash
source ~/.nvm/nvm.sh
nvm use

cd web
npm run dev
```

前端开发服务器为 `http://localhost:5173`；API proxy 目标为 `http://localhost:8080`。

如果需要由 `nasd` 直接托管静态 Web UI，应先构建前端，或设置 `MNEMONAS_WEB_DIR=web/dist`。

## Flutter 客户端开发

`client/` 使用 Flutter 3.44.4，并保留 Android、Linux 和 Windows runner。当前优先完成 Android 版本；Linux 和 Windows runner 用于保持跨平台工程边界，尚不表示相应桌面构建已经完成发布验证。

### 支持范围与工具链

Android 构建需要 Flutter 3.44.4、Dart SDK、包含 `javac` 的完整 JDK 17，以及通过 `flutter doctor -v` 检查的 Android SDK、platform tools 和 NDK `28.2.13676358`；Android SDK 许可协议也必须已接受。仅安装 Java 运行时不足以完成 Gradle 编译。存在多套 Java 安装时，应把 `JAVA_HOME` 和 `PATH` 指向同一套完整 JDK。CI 使用固定 Flutter 版本、JDK 17 和 NDK，不跟随 `stable` 渠道的后续版本自动漂移。

Flutter 和 Dart 不在默认 `PATH` 时，可显式传入二进制路径：

```bash
make client-check \
  FLUTTER=/path/to/flutter/bin/flutter \
  DART=/path/to/flutter/bin/dart \
  JAVA=/path/to/jdk-17/bin/java \
  JAVAC=/path/to/jdk-17/bin/javac
```

Gradle 仍从进程环境读取 Java 工具链；显式指定上述变量时，还应设置 `JAVA_HOME=/path/to/jdk-17`。

### 本机构建与门禁

仓库级入口如下：

```bash
make client-toolchain-check
make client-android-policy-check
make client-android-release-signing-check
make client-format
make client-analyze
make client-test
make client-apk-debug
make client-check
```

`make client-check` 依次检查 Flutter 版本、Android 备份与应用身份策略、Dart 格式、静态分析、单元/组件测试和 Android debug APK 构建。debug APK 写入 `client/build/app/outputs/flutter-apk/app-debug.apk`。`make client-format` 会修改 Dart 源文件；其余门禁不会主动格式化代码。

直接运行 Flutter 命令时：

```bash
cd client
flutter pub get
dart format --output=none --set-exit-if-changed lib test
flutter analyze
flutter test
flutter build apk --debug
```

### 平台 runner 依赖

- Android runner 需要 JDK 17、Android SDK command-line tools、platform tools、对应 compile SDK、build tools 和 NDK `28.2.13676358`。模拟器验证还需要 Android Emulator、硬件虚拟化和可用的 AVD。
- Linux runner 必须在 Linux 主机上构建，并安装 `clang`、`cmake`、`ninja-build`、`pkg-config` 和 GTK 3 开发包。
- Windows runner 必须在 Windows 主机上构建，并安装 Visual Studio 2022 的“使用 C++ 的桌面开发”工作负载和 Windows SDK。

当前 CI 构建 Android debug APK，并使用临时测试密钥执行 release APK/AAB 的失败关闭签名门禁。Linux 和 Windows 构建应在对应原生 runner 上增加门禁；Android CI 结果不能替代桌面平台验证。

### Android 签名与设备验证边界

debug APK 只用于开发和测试，不是可发布产物。Release 保留应用 ID `com.mnemonas.app`；Debug 和 Profile 分别使用 `com.mnemonas.app.debug` 与 `com.mnemonas.app.profile`，显示名称也会标明开发或性能分析用途，避免开发安装占用正式应用身份。

任何 release APK 或 AAB 都不得使用 Android debug key。构建 release 产物时，Gradle 必须从源码 checkout 外的 `key.properties` 读取 `storeFile`、`storePassword`、`keyAlias`、`keyPassword`、`certificateSha256`，并可选读取 `storeType`；配置文件引用的密钥库也必须位于源码 checkout 外。仓库内没有默认配置回退。`certificateSha256` 是签名证书 DER 内容的 SHA-256 摘要，使用 64 个十六进制字符表示。通过环境变量注入配置文件：

```bash
export MNEMONAS_ANDROID_KEY_PROPERTIES=/secure/mnemonas-android-key.properties
cd client
flutter build appbundle --release
```

直接调用 Gradle 时，也可使用 `-Pmnemonas.android.keyProperties=/secure/mnemonas-android-key.properties`。两个入口同时存在但指向不同文件时，构建会失败。配置缺失、字段为空、文件不可读、别名不是私钥、口令错误、证书未生效或已过期、Android Debug 证书及证书指纹不匹配都会阻止 release 产物生成；错误输出不包含签名口令。构建同时禁止所有 `android.injected.signing.*` 覆盖。门禁根据 Gradle 解析后的任务图识别 release 产物任务，因此任务缩写或排除独立校验任务不能绕过签名材料检查。

`make client-android-release-signing-check` 在临时目录生成测试证书，覆盖上述拒绝路径、注入签名覆盖和任务缩写加排除校验任务的绕过回归，并构建、验签 release APK 与 AAB 后清理临时密钥和 release 测试产物。该门禁只验证构建逻辑，不代表已建立正式密钥托管、轮换、恢复或发布审批流程，也不会产生可分发候选包。

该门禁以受信源码 checkout、固定 Gradle/AGP 工具链和受控构建主机为前提，不能防止有权替换构建脚本、注入 Gradle init script 或在构建后重新签名的本地主体。AGP 升级、增加 flavor 或自定义发布任务时，必须同步复核受保护的 release 任务集合。正式候选包还应在隔离构建环境中从只读密钥来源签名，并在发布前对最终 APK/AAB 的摘要、应用身份和证书指纹执行独立验签。

格式、分析、测试和 debug APK 构建只证明主机侧门禁通过，不证明客户端已在真实设备可用。发布候选至少需要在真实 Android 设备上验证安装与升级、进程重启后的会话恢复、HTTPS 连接、登录与 token 轮换、文件选择和传输、下载打开、前后台切换、网络中断恢复及权限拒绝路径。模拟器结果和 CI 产物不能替代该证据；在真实设备记录完成前，客户端状态仍应标记为开发中。

### Android 应用数据备份策略

Android 客户端通过平台安全存储保存会话材料。为避免加密后的 `SharedPreferences` 在缺少原设备 Android Keystore 密钥时被恢复，客户端不参与 Android 云备份、设备间迁移或应用数据恢复。`AndroidManifest.xml` 同时关闭 `allowBackup`，并分别引用 Android 11 及更早版本的 `full-backup-content` 规则和 Android 12 及更新版本的 `data-extraction-rules` 规则；两套规则均排除所有应用可控存储域。重新安装或更换设备后需要重新登录，这是当前安全策略的预期行为。

清单、备份规则、应用身份或签名构建逻辑发生变化时，运行 `make client-android-policy-check`。该静态检查会解析清单、两套备份 XML、变体名称和 Gradle 签名约束，验证云备份与设备迁移未重新启用，并执行针对不安全回归的测试。签名逻辑变化还必须运行 `make client-android-release-signing-check`。

## 端口

| 服务 | 端口 | 说明 |
| --- | --- | --- |
| `nasd` | 8080 | Web UI、REST API、WebDAV |
| dataplane HTTP | 9091 | 健康检查和统计信息 |
| dataplane gRPC | 9090 | CAS 存储服务 |
| Vite | 5173 | 前端开发服务器 |

## 开发配置

`~/.mnemonas/config.toml`：

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

## 测试

主要入口：

```bash
make verify-changed
make release-readiness
make test
make test-torture
make e2e
make bench
make lint
make check
make docs-check
make coverage
make docker-check
make client-check
```

`make verify-changed` 会根据 worktree、staged area 或指定 base ref 中的变更文件选择检查。
可选择 workflow、脚本、Go/Rust、Web 前端、Flutter 客户端、E2E、Docker、文档、依赖安全、工具链配置、质量配置、示例配置和 public-access 模板检查。
Go 测试默认通过 `GO_TEST_PACKAGE_PARALLELISM=3` 将包级并发限制为 3，并通过 `GO_TEST_TIMEOUT=30m` 为重负载 race 包保留包级执行余量，避免多个重包竞争资源和详细日志开销造成误超时；需要在资源更充足的环境中调整时，可显式覆盖这些变量。
当 `go.mod`、`go.sum`、Cargo 清单/锁文件或 Web npm 清单/锁文件变化时，`verify-changed` 会追加依赖安全检查；Web npm 清单或锁文件变化会使用 `NPM_AUDIT=1` 运行 npm audit。
YAML 配置校验会拒绝语法错误和同一映射内的重复键，避免本地解析时静默覆盖配置值。
使用 `./scripts/verify-changed.sh --staged` 只检查暂存内容，使用 `./scripts/verify-changed.sh --base <ref>` 检查分支范围，使用 `--dry-run` 查看将运行的命令而不执行。
发布前运行 `make release-readiness` 汇总当前分支、提交标题、分组规划、验证证据和 release checklist 状态。
Docker 镜像构建和容器烟测默认受 `VERIFY_CHANGED_DOCKER_TIMEOUT=45m` 限制，可按本地网络和构建机性能覆盖该值；脚本会自动使用 `timeout` 或 GNU coreutils 的 `gtimeout`。

纯文档变更运行 `make docs-check`。
该命令会校验仓库内 Markdown 链接、文件路径和标题锚点。
它还会校验 JSON、YAML 和 TOML 代码块；JSON 和 YAML 代码块会拒绝同一对象或映射内的重复键。
它还会确认 README、CHANGELOG、SUPPORT、SECURITY、Web README、public-access 模板 README 和 `docs/` 下文档保持中英文配对。
`docs/` 下文档还必须出现在两个文档索引中。

`make coverage` 会通过临时 dataplane 包装脚本运行全仓库 Go 覆盖率，执行 `GO_COVERAGE_MIN` 门槛，运行前端覆盖率，并将已忽略的本地报告写入 `coverage/go.html` 和 `web/coverage/`。

`make lint` 和 `make check` 默认要求安装 `golangci-lint`，除非显式设置 `SKIP_GOLANGCI_LINT=1` 用于本地排障。
Go lint 默认通过 `GO_LINT_ENV` 继承 `GO_CMD_ENV`，因此本地检查使用 `GOTOOLCHAIN=local`。
只有需要自动下载 toolchain 时才覆盖 `GO_LINT_ENV`。

### Go

```bash
GO_PACKAGES=$(make --no-print-directory go-packages)
bash ./scripts/with-test-dataplane.sh go test -v $GO_PACKAGES

bash ./scripts/with-test-dataplane.sh go test -v ./internal/webdav/...

bash ./scripts/with-test-dataplane.sh go test -v -cover $GO_PACKAGES

make coverage
```

`with-test-dataplane.sh` 启动的临时 dataplane 默认会自动选择空闲的 `127.0.0.1` gRPC 和 HTTP 端口。
未显式设置时，`MNEMONAS_TEST_DATAPLANE_ADDR` 和 `MNEMONAS_TEST_DATAPLANE_HTTP_ADDR` 会以选中地址导出给被包装命令。

地址覆盖必须满足：

- 保持 loopback：`localhost`、`ip6-localhost`、`::1` 或四段数字形式的 `127.0.0.0/8`；
- 使用不同端口；
- 不包含空白或控制字符。

这些限制用于避免测试服务监听公网或不可信局域网接口。

安装前端依赖后，不要使用 `go test ./...` 或 `go list ./...` 作为仓库包集合；Go 会遍历 `web/node_modules` 下的第三方包。仓库级 Go 检查应使用 `make --no-print-directory go-packages`。

### Rust

```bash
cd dataplane
cargo test
cargo test test_cas_store
cargo test -- --nocapture
```

在仓库根目录运行覆盖率：

```bash
cargo install cargo-llvm-cov --locked
make rust-coverage
```

### 前端

```bash
cd web
npm run check:node
npm run test:run
npm run test
npm run test:coverage
npm run lint
npm run typecheck
npm run test:e2e
npm run test:e2e:ui
```

Playwright 默认启动隔离后端和前端服务器。
本地运行默认使用 4 个 worker，除非 `MNEMONAS_E2E_WORKERS` 设置为正整数；CI 使用 1 个 worker。
默认单个 Playwright 测试超时为 60 秒，断言等待超时为 10 秒；可用 `MNEMONAS_E2E_TEST_TIMEOUT_MS` 和 `MNEMONAS_E2E_EXPECT_TIMEOUT_MS` 覆盖。

隔离后端使用 2 小时 access token 生命周期和 168 小时 refresh token 生命周期。
这可避免长时间并行 E2E 运行在共享 storageState 过期后进入并发 refresh-token 轮换。

复用已有服务时设置：

- `MNEMONAS_E2E_REUSE_EXISTING=1`；
- `MNEMONAS_E2E_BACKEND_URL`；
- `MNEMONAS_E2E_FRONTEND_URL`；
- `E2E_PASSWORD` 或 `E2E_PASSWORD_FILE`。

默认配置的初始密码文件位于 `~/.mnemonas/.mnemonas/initial-password.txt`。如果 `auth.users_file` 位于 `storage.root` 根目录，初始密码文件通常位于 `~/.mnemonas/initial-password.txt`。未设置 `E2E_PASSWORD_FILE` 时，Playwright 会按此顺序尝试这两个路径。显式设置 `E2E_PASSWORD_FILE` 时，该文件是权威来源；文件缺失或没有有效密码时不会回退默认路径。

### WebDAV 烟测

```bash
# 对已运行服务执行独立 curl 协议 smoke；脚本会创建临时集合并在结束时清理。
WEBDAV_URL=http://localhost:8080/dav \
MNEMONAS_WEBDAV_USERNAME="<mnemonas-or-webdav-username>" \
MNEMONAS_WEBDAV_PASSWORD="<mnemonas-or-webdav-password>" \
./scripts/webdav-client-smoke.sh

curl http://localhost:8080/health
curl http://localhost:9091/health
curl http://localhost:9091/stats
```

`scripts/webdav-client-smoke.sh` 覆盖 `OPTIONS`、`MKCOL`、`PUT`、`PROPFIND`、`GET`、`HEAD`、`COPY`、`MOVE`、`DELETE`、COPY/MOVE 后内容校验和 URL 编码空格路径读写。`WEBDAV_URL` 必须是不包含空白、query、fragment、内嵌凭据、反斜杠、编码斜杠或编码反斜杠，也不包含 `.`/`..` 路径段的 HTTP(S) WebDAV 根 URL；凭据应通过环境变量传入。如果启用 `webdav.auth_type = "basic"`，可用 `./scripts/dev.sh --creds` 查看凭据位置；如果启用 `webdav.auth_type = "users"`，则使用 MnemoNAS 用户名和密码。每次 curl 请求默认使用 `CURL_CONNECT_TIMEOUT=10` 和 `CURL_MAX_TIME=30`，高延迟网络可通过环境变量调大。
脚本通过临时 curl 配置传递认证信息，避免在命令参数中输出明文密码。手工只读 PROPFIND 排查也应使用临时 curl 配置或该 smoke 脚本，不应把 WebDAV 密码写进 `curl -u` 命令参数。

`9091` 应保持本地或私有网络可见。

### 备份恢复演练烟测

```bash
# 对已运行服务执行维护 API smoke；脚本不会创建或删除备份任务。
MNEMONAS_API_URL=http://localhost:8080/api/v1 \
MNEMONAS_BACKUP_JOB_ID=external-disk \
MNEMONAS_COOKIE_FILE=cookies.txt \
./scripts/backup-restore-drill-smoke.sh
```

`scripts/backup-restore-drill-smoke.sh` 会按显式任务 ID 读取备份任务列表和单任务详情，触发立即备份，执行保留策略检查，运行恢复演练，并下载恢复报告。`MNEMONAS_API_URL` 必须是不包含空白、query、fragment、内嵌凭据、反斜杠、编码斜杠或编码反斜杠，也不包含空路径段或 `.`/`..` 路径段的 HTTP(S) API 根 URL；`MNEMONAS_BACKUP_JOB_ID` 必须是安全任务 ID。需要认证时通过 `MNEMONAS_COOKIE_FILE` 传入 curl cookie 文件。每次 curl 请求默认使用 `CURL_CONNECT_TIMEOUT=10` 和 `CURL_MAX_TIME=600`；高延迟备份目标可调大 `CURL_MAX_TIME`。如需保留本地演练产物供人工抽查，可设置 `MNEMONAS_BACKUP_KEEP_ARTIFACT=1`。

### E2E

```bash
make e2e
./scripts/run-e2e-isolated.sh --quick
RUN_RCLONE_WEBDAV=1 ./scripts/run-e2e-isolated.sh --quick
```

隔离 runner 避免写入真实用户存储根目录。
`scripts/e2e-test.sh` 可以指向显式运行中的服务，但必须提供：

- `BASE_URL`；
- `STORAGE_ROOT`；
- `CONFIG_FILE`；
- `SECRETS_FILE`；
- `INITIAL_PASSWORD_FILE`。

`STORAGE_ROOT` 不能包含控制字符、`..` 或符号链接路径组件。
`BASE_URL` 必须是带 host 的 HTTP(S) URL；不能包含空白、控制字符、内嵌凭据、query、fragment、反斜杠、编码斜杠或编码反斜杠、编码 query 或 fragment 标记、空路径段，也不能包含 `.` 或 `..` 路径段。末尾斜杠会在校验后规范化。
WebDAV `auth_type = "basic"` 时，脚本可以从配置或 `secrets.json` 读取 Basic Auth 凭据。
WebDAV `auth_type = "users"` 时，应显式设置 `MNEMONAS_WEBDAV_USERNAME` 和 `MNEMONAS_WEBDAV_PASSWORD`。
设置 `RUN_RCLONE_WEBDAV=1` 时，隔离 runner 和 `scripts/e2e-test.sh` 会在已安装 `rclone` 的环境中额外运行 WebDAV 客户端 smoke，覆盖上传、下载、移动/重命名、列出和清理。

隔离 E2E runner 和 Playwright 后端只接受 loopback Web 与 dataplane 地址：`localhost`、`ip6-localhost`、`::1` 或四段数字形式的 `127.0.0.0/8` 地址。
这可避免测试后端意外监听公网或不可信局域网接口。

### 故障注入

故障注入会杀死并重启 `nasd`，并可能破坏测试对象。默认项目入口会在 `/tmp` 下启动隔离后端，并把显式目标信息传给破坏性 runner：

```bash
make fault-injection
./scripts/run-fault-injection-isolated.sh
```

已有隔离目标需要测试时，可使用底层 runner：

```bash
MNEMONAS_LIVE_FAULTS=1 \
BASE_URL=http://127.0.0.1:18080 \
STORAGE_ROOT=/tmp/mnemonas-fault-target \
NASD_BIN="$PWD/bin/nasd" \
FAULT_INJECTION_ASSUME_YES=1 \
RUN_CORRUPTION_TESTS=0 \
./scripts/fault-injection-test.sh
```

安全检查由 `scripts/test-fault-injection-safety.sh` 和 `make scripts-check` 覆盖。
隔离 runner 只接受 `/tmp` 或 checkout-local 根目录，以及 loopback Web 和 dataplane 地址。

底层 runner 要求显式提供 `BASE_URL`、`STORAGE_ROOT` 和 `NASD_BIN`。
`BASE_URL` 使用与 E2E runner 相同的 HTTP(S) URL 安全规则。
WebDAV 使用 `auth_type = "users"` 时，还要求显式提供 `MNEMONAS_WEBDAV_USERNAME` 和 `MNEMONAS_WEBDAV_PASSWORD`。

真实存储路径需要 `ALLOW_REAL_STORAGE=1`。
该路径仍必须是绝对路径，不能包含控制字符、`..` 或符号链接路径组件。
该路径不能指向 `/`、`/tmp`、`/var` 等受保护系统目录。

可能被破坏性检查读取或修改的 `OBJECTS_DIR`、`INDEX_DB` 和可选 `NASD_PID_FILE` 必须位于 `STORAGE_ROOT` 下。

### 基准测试

```bash
make bench
./scripts/run-benchmark-isolated.sh
```

针对显式服务的手动 benchmark：

```bash
MNEMONAS_STORAGE_ROOT=/tmp/mnemonas-bench-target \
./scripts/benchmark.sh http://127.0.0.1:18080

# 需要显式 WebDAV 凭据或受保护指标时：
MNEMONAS_WEBDAV_USERNAME="<mnemonas-or-webdav-username>" \
MNEMONAS_WEBDAV_PASSWORD="<mnemonas-or-webdav-password>" \
MNEMONAS_ACCESS_TOKEN="<access-token>" \
MNEMONAS_STORAGE_ROOT=/tmp/mnemonas-bench-target \
./scripts/benchmark.sh http://127.0.0.1:18080
```

手动 benchmark 目标会创建并删除 `storage.root/files/benchmark-test`。
benchmark 目标 URL 使用与 E2E runner 相同的 HTTP(S) URL 安全规则。
真实存储路径需要 `ALLOW_REAL_STORAGE=1`。
该路径仍必须是绝对路径，不能包含控制字符、`..` 或符号链接路径组件，且不能指向受保护系统目录。

WebDAV `auth_type = "basic"` 时，手动 benchmark 在未提供环境凭据时会从 `config.toml` 或 `secrets.json` 读取 Basic Auth 凭据。
WebDAV `auth_type = "users"` 时，应显式设置 `MNEMONAS_WEBDAV_USERNAME` 和 `MNEMONAS_WEBDAV_PASSWORD`。
脚本通过临时 curl config 文件传递 WebDAV 凭据和 `MNEMONAS_ACCESS_TOKEN`，避免把密码或 Bearer token 写进 `curl` 命令参数。
`[webdav].username/password` 不会被当作 MnemoNAS 用户凭据。

隔离 benchmark runner 使用同样的 Web 和 dataplane loopback-only 规则。压测远端或共享网络实例时，应直接运行 `scripts/benchmark.sh`，并显式提供隔离的 `MNEMONAS_STORAGE_ROOT`。

## 调试

### Go

```bash
go install "github.com/go-delve/delve/cmd/dlv@v1.26.3"
dlv debug ./cmd/nasd
```

### Rust

```bash
cd dataplane
cargo build
rust-lldb target/debug/dataplane
```

### 日志

```bash
LOG_LEVEL=debug ./bin/nasd
RUST_LOG=debug ./bin/dataplane
```

也可通过配置设置：

```toml
[log]
level = "debug"
```

### 网络

```bash
grpcurl -plaintext localhost:9090 list
grpcurl -plaintext localhost:9090 describe
sudo tcpdump -i lo port 8080 -w debug.pcap
```

## 常见问题

### `protoc-gen-go: program not found`

```bash
export PATH=$PATH:$(go env GOPATH)/bin
```

### Go 工具链下载失败

仓库固定 `toolchain go1.25.12`。如果本地网络阻止 toolchain 下载，但已安装兼容的本地 Go 1.25.x：

```bash
packages=$(GOTOOLCHAIN=local make --no-print-directory go-packages)
GOTOOLCHAIN=local go test $packages
GOTOOLCHAIN=local make build
```

发布构建和漏洞扫描应使用 Go 1.25.12 或更新的 1.25.x patch 版本。

如果 `GOSUMDB=off` 导致 toolchain 校验失败：

```bash
GOSUMDB=sum.golang.org go version
GOSUMDB=sum.golang.org govulncheck ./...
```

### 前端文件监视器限制

```bash
echo fs.inotify.max_user_watches=524288 | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

### 重置开发数据

```bash
DEFAULT_DATA_DIR="$HOME/.mnemonas"
DATA_DIR="${MNEMONAS_DATA_DIR:-$DEFAULT_DATA_DIR}"
[ "$DATA_DIR" = "$DEFAULT_DATA_DIR" ] || { echo "refusing non-default DATA_DIR; inspect and delete manually: $DATA_DIR"; exit 1; }
[ ! -L "$DATA_DIR" ] || { echo "refusing symlink DATA_DIR: $DATA_DIR"; exit 1; }
rm -rf -- "$DATA_DIR"
```

## 代码风格

Go：

```bash
go fmt ./...
```

Rust：

```bash
cd dataplane && cargo fmt
```

前端：

```bash
cd web
npm run lint
npm run build
```

提交遵循 Conventional Commits：

```text
feat(webdav): add ETag support for conditional requests
fix(dataplane): fix memory leak in CDC chunking
docs(readme): update installation instructions
```
