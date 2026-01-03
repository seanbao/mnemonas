# MnemoNAS 测试策略

[English](testing-strategy.en.md) | 简体中文

本文档定义 MnemoNAS 在单元测试、集成测试、端到端测试、torture 矩阵和前端测试中的测试策略。

## 测试金字塔

```text
                    手动测试
                 探索性测试与 UX 验证
              ----------------------
                    E2E 测试
               用户流程与浏览器验证
            --------------------------
                    集成测试
              组件协作与配置行为
          ------------------------------
                    单元测试
              函数、边界与不变量
```

目标：

| 层级 | 目标 | 执行频率 | 典型耗时 |
| --- | --- | --- | --- |
| 单元测试 | 可行范围内达到 80%+ | 每次提交 | < 30s |
| 集成测试 | 关键路径 | 每次提交 | < 2min |
| E2E | 核心流程 | 每日/发布前 | < 10min |
| Torture 矩阵 | 高风险路径 | 手动/定时 | 10-90min |

## 单元测试

配置密集型行为使用表驱动测试：

```go
func TestConfigMatrix_AuthInitialization(t *testing.T) {
    cases := []struct {
        name           string
        authEnabled    bool
        expectPassFile bool
        expectUserFile bool
    }{
        {"auth enabled", true, true, true},
        {"auth disabled", false, false, false},
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            // use temp directories
            // verify files and behavior
        })
    }
}
```

边界测试应覆盖空输入、最大长度、Unicode、路径穿越、非法 TOML、非法 duration 和权限失败。

测试命名规范：

```text
Test<Module>_<Scenario>_<ExpectedBehavior>
```

示例：

- `TestUserStore_DuplicateUsername_ReturnsError`
- `TestTokenManager_ExpiredToken_ReturnsErrTokenExpired`
- `TestConfig_MissingFile_UsesDefaults`

## 集成测试

集成测试验证多个模块的协作行为：

- 认证 store、默认管理员创建、登录、token refresh 和初始密码文件删除。
- 配置文件加载与校验。
- 工作区写入、版本库、回收站和 CAS 行为。
- WebDAV handler 在真实 HTTP 请求上的行为。
- 设置更新对运行时 WebDAV 行为的影响。

示例：

```go
func TestAuthIntegration_FullLoginFlow(t *testing.T) {
    // create temporary storage root
    // initialize auth
    // read initial password
    // login
    // verify token and password-file cleanup
}
```

## E2E 测试

默认入口：

```bash
make e2e
```

快速隔离运行：

```bash
./scripts/run-e2e-isolated.sh --quick
RUN_RCLONE_WEBDAV=1 ./scripts/run-e2e-isolated.sh --quick
WEBDAV_URL=http://localhost:8080/dav ./scripts/webdav-client-smoke.sh
```

隔离 runner 会先启动临时后端、临时存储和非默认端口，再调用 `scripts/e2e-test.sh`。
隔离根目录必须位于 `/tmp` 或当前 checkout 下，且不能包含控制字符、`..` 或符号链接路径组件。
设置 `RUN_RCLONE_WEBDAV=1` 时，隔离 runner 会把该开关传给底层 E2E，用已安装的 `rclone` 执行 WebDAV 客户端 smoke，覆盖上传、下载、移动/重命名、列出和清理。
`scripts/webdav-client-smoke.sh` 用于已经运行的服务，提供独立 curl 协议 smoke，并覆盖 URL 编码空格路径读写；`WEBDAV_URL` 必须是不包含空白、query、fragment 或内嵌凭据的 HTTP(S) WebDAV 根 URL，需要认证时通过 `MNEMONAS_WEBDAV_USERNAME` 和 `MNEMONAS_WEBDAV_PASSWORD` 传入凭据。每次 curl 请求默认使用 `CURL_CONNECT_TIMEOUT=10` 和 `CURL_MAX_TIME=30`，高延迟网络可通过环境变量调大。
Playwright 默认后端端口为 `18180`，默认前端端口为 `14173`。
Playwright 隔离后端使用 2 小时 access token 生命周期和 168 小时 refresh token 生命周期，降低长时间并行运行时共享 storageState 过期的风险。
隔离后端还会创建公开文件分享、密码分享、停用分享和文件夹分享 fixture，并写入 `MNEMONAS_E2E_ROOT/backend/*-share-id.txt`，供公开分享、公开入口布局和运行时完整性用例使用；默认隔离运行缺少这些 fixture 时应失败，而不是静默跳过覆盖。

针对已有服务的手动测试必须显式提供目标信息：

```bash
BASE_URL=http://127.0.0.1:18080 \
STORAGE_ROOT=/tmp/mnemonas-e2e-target \
CONFIG_FILE=/tmp/mnemonas-e2e-config.toml \
SECRETS_FILE=/tmp/mnemonas-e2e-secrets.json \
INITIAL_PASSWORD_FILE=/tmp/mnemonas-e2e-initial-password.txt \
./scripts/e2e-test.sh
```

手动 `STORAGE_ROOT` 必须是绝对路径。
该路径不能包含控制字符、`..` 或符号链接路径组件，且默认只允许位于 `/tmp` 或当前 checkout 下。
WebDAV 使用 `auth_type = "users"` 时，手动运行还必须显式传入 `MNEMONAS_WEBDAV_USERNAME` 和 `MNEMONAS_WEBDAV_PASSWORD`。

手动检查需要初始管理员密码时，应解析 `Password:` 前缀后的完整值，例如：

```bash
password=$(sed -n 's/^Password:[[:space:]]*//p' "$INITIAL_PASSWORD_FILE" | head -n1)
```

手动登录 payload 应通过 JSON encoder 生成，避免字符串插值导致的转义问题，例如：

```bash
login_payload=$(PASSWORD="$password" python3 - <<'PY'
import json
import os

print(json.dumps({"username": "admin", "password": os.environ["PASSWORD"]}))
PY
)
```

首次启动示例：

```bash
test_fresh_install_auth_enabled() {
    TEST_HOME="$(mktemp -d)"
    trap 'rm -rf -- "$TEST_HOME"' EXIT
    export HOME="$TEST_HOME"
    # If [auth].users_file is customized, point INITIAL_PASSWORD_FILE at the sibling initial-password.txt.
    initial_password_file="${INITIAL_PASSWORD_FILE:-$HOME/.mnemonas/.mnemonas/initial-password.txt}"

    mkdir -p ~/.mnemonas
    cat > ~/.mnemonas/config.toml <<'TOML'
[auth]
enabled = true
TOML
    ./bin/nasd &
    sleep 2

    [ -f "$initial_password_file" ] || fail "Password file not created"

    password=$(sed -n 's/^Password:[[:space:]]*//p' "$initial_password_file" | head -n1)
    login_payload=$(PASSWORD="$password" python3 - <<'PY'
import json
import os

print(json.dumps({"username": "admin", "password": os.environ["PASSWORD"]}))
PY
)
    response=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
        -H "Content-Type: application/json" \
        -d "$login_payload")

    echo "$response" | grep -q '"success":true' || fail "Login failed"

    [ ! -f "$initial_password_file" ] || fail "Password file not deleted after login"
}
```

E2E 分组：

| 分组 | 覆盖范围 | 模式 |
| --- | --- | --- |
| Basic | 健康检查、版本 API、WebDAV OPTIONS | quick |
| File operations | PUT、GET、DELETE、MKCOL、COPY、MOVE | quick |
| Authentication | 登录、刷新、权限 | quick |
| Conditional requests | ETag、If-None-Match、If-Match | quick |
| WebDAV locks | LOCK/UNLOCK 虚拟锁 token 往返 | quick |
| Versions | 版本历史 API | quick |
| Concurrency | 并发读写 | quick |
| Standalone WebDAV smoke | curl `OPTIONS`、`MKCOL`、`PUT`、`PROPFIND`、`GET`、`HEAD`、`COPY`、`MOVE`、`DELETE`、COPY/MOVE 后内容校验、URL 编码空格路径和请求超时配置 | 手动指定已运行服务 |
| WebDAV client smoke | 可选 rclone 上传、下载、移动/重命名、列出和清理 | `RUN_RCLONE_WEBDAV=1` |
| Large files | 100MB 路径 | full |
| Crash recovery | 中断写入和重启行为 | full |

## 高级测试

### 属性测试

属性测试用于路径安全、密码生命周期和前端格式化工具等不变量。

Go 示例：

```go
import "pgregory.net/rapid"

func TestProperty_PasswordFileLifecycle(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        authEnabled := rapid.Bool().Draw(t, "authEnabled")
        _ = authEnabled
        // assert lifecycle invariant
    })
}
```

前端属性测试位于 Vitest 测试套件，主要覆盖纯工具函数和边界较多的逻辑。

### 模糊测试

Go 模糊测试用于路径校验、WebDAV 前缀归一化和其他输入解析器：

```bash
go test -fuzz=FuzzPasswordValidation ./internal/auth/
```

### 契约测试

API 契约测试应固定前后端之间重要的请求和响应形态。
当前仓库内的路由契约测试位于 `internal/api/routes_contract_test.go`；下方片段只展示契约形态。

```yaml
interactions:
  - description: "Login with valid credentials"
    request:
      method: POST
      path: /api/v1/auth/login
      headers:
        Content-Type: application/json
        X-MnemoNAS-Session-Mode: cookie
    response:
      status: 200
      headers:
        Set-Cookie: !!regexp mnemonas_access=.*HttpOnly
      body:
        success: true
        data:
          user:
            username: "admin"
        absent:
          - data.access_token
          - data.refresh_token
```

### 变异测试

变异测试是可选方法，适合高风险模块：

```bash
go install "github.com/zimmski/go-mutesting/cmd/go-mutesting@v0.0.0-20210610104036-6d9217011a00"
go-mutesting ./internal/auth/...
```

## Torture 测试矩阵

运行：

```bash
make test-torture
```

默认矩阵：

- 并发敏感包的 Go 竞态检测。
- 路径安全和 WebDAV 归一化的 Go 模糊测试种子。
- 前端属性测试。
- Playwright 人类交互流程。
- 运行时错误扫描和布局完整性检查。

本地快速覆盖：

```bash
GO_FUZZTIME=2s RUN_GO_RACE=0 RUN_E2E_TORTURE=0 make test-torture
```

`.github/workflows/torture.yml` 提供手动和定时的非破坏性深度测试入口，并保持 `RUN_LIVE_FAULTS=0`。

## 破坏性故障注入

`scripts/run-fault-injection-isolated.sh` 会启动隔离后端，并对其运行 `scripts/fault-injection-test.sh`。
破坏性 runner 验证崩溃恢复、并发写冲突、版本恢复、对象损坏和元数据损坏处理。
该 runner 可以杀死 `nasd` 并修改内部文件，因此底层 runner 在缺少显式目标信息时会拒绝运行。
WebDAV 使用 `auth_type = "users"` 时，底层 runner 还要求显式传入 `MNEMONAS_WEBDAV_USERNAME` 和 `MNEMONAS_WEBDAV_PASSWORD`。

隔离运行：

```bash
make fault-injection
./scripts/run-fault-injection-isolated.sh
```

显式目标运行：

```bash
MNEMONAS_LIVE_FAULTS=1 \
BASE_URL=http://127.0.0.1:18080 \
STORAGE_ROOT=/tmp/mnemonas-fault-target \
NASD_BIN="$PWD/bin/nasd" \
FAULT_INJECTION_ASSUME_YES=1 \
RUN_CORRUPTION_TESTS=0 \
./scripts/fault-injection-test.sh
```

安全门禁：

- `scripts/run-fault-injection-isolated.sh` 只接受 `/tmp` 或 checkout 本地根目录，以及 loopback Web 和 dataplane 地址。
- `BASE_URL`、`STORAGE_ROOT` 和 `NASD_BIN` 必须显式传入。
- 默认允许的存储根目录为 `/tmp` 或当前 checkout。
- `STORAGE_ROOT` 必须是绝对路径，且不能包含控制字符、`..` 或符号链接路径组件。
- 默认拒绝 `$HOME/.mnemonas`。
- 非交互运行必须设置 `FAULT_INJECTION_ASSUME_YES=1`。
- 真实存储路径必须额外设置 `ALLOW_REAL_STORAGE=1`，并且仍必须是绝对路径，不能指向 `/`、`/tmp`、`/var` 等受保护系统目录。
- 可能被破坏性检查读取或修改的 `OBJECTS_DIR`、`INDEX_DB` 和可选 `NASD_PID_FILE` 必须位于 `STORAGE_ROOT` 下。

这些门禁由 `scripts/test-fault-injection-safety.sh` 覆盖，并纳入 `make scripts-check`。

## AI 辅助测试

AI 可辅助生成：

- 边界用例表。
- 现有测试缺失的断言。
- 不变量候选。
- 高风险变更的审查清单。

AI 生成的测试仍应审查其假设、fixture 质量，以及是否能在错误行为上失败。

## CI

CI 应覆盖：

- 通过 `make workflows-check` 验证工作流，包括 YAML 语法、重复键检查和 actionlint 校验。
- 通过 `make scripts-check` 验证脚本，包括 shell 语法、ShellCheck 和安全回归用例。
- Go protobuf 生成、生成文件漂移检查、`golangci-lint`、race 测试、Go 覆盖率门槛和 `govulncheck`。
- Rust dataplane 和 `tools/proto-gen` 的格式检查、Clippy、测试、依赖审计和 release 构建。
- 前端 `npm audit`、lint、typecheck、生产构建和覆盖率测试。
- 使用隔离后端和前端测试服务器的 Playwright E2E。
- Docker 镜像构建，以及 `/health` 和前端入口烟测。

`.github/workflows/ci.yml` 是 CI 的权威定义。该文件应保持只读仓库权限和基于工作流/ref 的并发取消策略。新增 job 或安全边界变化应同步更新本节和对应的本地检查目标。

发布和安全检查不应跳过 `golangci-lint`。
Go 覆盖率由 `GO_COVERAGE_MIN` 强制执行，当前在 CI 和 `make coverage` 中均为 75%。
Rust dataplane 覆盖率由 `make rust-coverage` 通过 `cargo-llvm-cov` 和 `RUST_COVERAGE_MIN` 强制执行，当前行覆盖率门槛为 70%。
Codecov status 是信息性状态，仅用于趋势展示和 PR 注释，不能作为唯一阻塞质量门禁。

## 本地变更感知验证

提交本地变更前应运行 `make verify-changed`。
该目标调用 `scripts/verify-changed.sh`，根据 worktree、staged diff 或 `--base REF` 范围内的变更文件选择验证命令，并始终对对应范围运行 `git diff --check`。
Worktree 模式还会检查未跟踪文本文件中的尾随空白和缩进中的空格制表混用。
该目标还会对已跟踪和未跟踪文本文件运行高置信密钥泄漏扫描，覆盖私钥块和常见平台 token 模式；失败输出只包含文件、行号和模式类别，不输出命中内容。

选择器覆盖以下变更类型：

- Go、Rust dataplane、`tools/proto-gen`、protobuf、Web UI、Playwright E2E、Docker、文档、GitHub Actions 工作流和 shell 脚本变更。
- Go、Rust 和 Web 依赖清单或锁文件变更会追加依赖安全检查。
- 工具链和质量配置变更，包括 `.go-version`、`.nvmrc`、`.golangci.yml`/`.golangci.yaml`、`.github/dependabot.yml`/`.github/dependabot.yaml`、`codecov.yml`/`codecov.yaml` 和 `mnemonas.example.toml`。
- Docker 和 public-access 模板变更，包括 `.env.example`、Compose 模板和 `deploy/public-access/`。

YAML 配置校验会拒绝语法错误和同一映射内的重复键，避免重复键在本地解析时被静默覆盖。

使用 `./scripts/verify-changed.sh --staged` 检查暂存内容。
使用 `./scripts/verify-changed.sh --base <ref>` 验证分支差异。
使用 `--dry-run` 查看将运行的命令而不执行。
Docker 镜像构建和容器烟测由 `VERIFY_CHANGED_DOCKER_TIMEOUT` 控制超时，默认 `45m`，用于避免外部镜像拉取、构建环境异常或容器健康检查异常导致本地验证无限挂起；脚本会自动使用 `timeout` 或 GNU coreutils 的 `gtimeout`。

变更选择行为由 `scripts/test-verify-changed-safety.sh` 覆盖。
密钥泄漏扫描由 `scripts/test-secret-leaks.sh` 覆盖。
这些回归测试均纳入 `make scripts-check`。

## 前端测试

前端测试层次：

| 层级 | 工具 | 目的 |
| --- | --- | --- |
| 单元测试 | Vitest | 纯函数、store、API 辅助函数 |
| 组件测试 | Vitest + Testing Library | 组件行为和状态切换 |
| E2E | Playwright | 真实浏览器流程 |
| 视觉/回归 | Playwright 截图和布局断言 | 发现布局和交互回归 |

命令：

```bash
cd web
npm run check:node
npm run test:run
npm run test:coverage
npm run lint
npm run typecheck
npm run build
npm run test:e2e
npm run test:e2e:ui
```

Playwright 应覆盖桌面和移动端外壳、导航、文件页交互、运行时控制台错误，以及重要视图的截图和布局检查。

默认 Playwright 配置会启动隔离后端和前端测试服务器。
在隔离环境中，认证初始化失败应被视为测试失败，避免受保护页面回归被隐藏成跳过的测试。

复用已有环境仅在设置 `MNEMONAS_E2E_REUSE_EXISTING=1` 时启用。
复用服务需要认证时，设置 `E2E_PASSWORD` 或 `E2E_PASSWORD_FILE`。
未显式设置 `E2E_PASSWORD_FILE` 时，Playwright 凭据 helper 会依次尝试 `~/.mnemonas/.mnemonas/initial-password.txt` 和 `~/.mnemonas/initial-password.txt`。
显式设置 `E2E_PASSWORD_FILE` 时，该文件是权威来源；文件缺失或没有有效密码时不会回退默认路径。
这些运行默认允许凭据缺失时跳过受保护页面测试。
设置 `MNEMONAS_E2E_ALLOW_AUTH_SKIP=0` 可在复用环境中强制失败。
只有确认跳过受保护页面检查符合本次验证目的时，才应设置 `MNEMONAS_E2E_ALLOW_AUTH_SKIP=1`。

## 测试清单

新功能：

- 核心逻辑有单元测试。
- 模块边界有集成测试。
- 响应形态变化时补充 API contract 或 handler 测试。
- 前端测试覆盖 UI 状态和失败状态。
- 用户可见流程有 E2E 覆盖。
- 文档和配置示例同步更新。

配置变更：

- 默认值已测试。
- 无效值会被拒绝。
- 风险较高但可部署的状态有告警测试。
- Docker/systemd 行为已纳入考虑。

Bug 修复：

- 可行时先补复现测试。
- 回归测试在修复前应失败。
- 相关边界条件已检查。
- 用户可见问题补充 E2E 或集成覆盖。

提交前：

- 单元测试通过。
- 用户可见流程的 E2E 测试通过。
- 视觉回归没有意外变化。
- 前端变更通过 TypeScript 和 ESLint 检查。
- `make verify-changed` 通过，或已按变更范围运行等价的更宽验证命令。
- `make docs-check` 通过，文档本地链接、标题锚点、双语文档配对和文档索引入口没有断裂。

## 参考资源

- [Go testing](https://pkg.go.dev/testing)
- [Go fuzzing](https://go.dev/doc/security/fuzz/)
- [Vitest](https://vitest.dev/)
- [Playwright](https://playwright.dev/)
