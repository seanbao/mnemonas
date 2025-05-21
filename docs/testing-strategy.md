# MnemoNAS 测试策略

[English](testing-strategy.en.md) | 简体中文

本文档定义 MnemoNAS 项目的多层测试策略，确保功能正确性、配置行为一致性和系统可靠性。

## 测试金字塔

```text
                    ┌──────────────┐
                    │   手动测试    │  ← 探索性测试、UX 验证
                   ┌┴──────────────┴┐
                   │    E2E 测试    │  ← 用户场景、集成验证
                  ┌┴────────────────┴┐
                  │   集成测试       │  ← 组件交互、配置验证
                 ┌┴──────────────────┴┐
                 │     单元测试       │  ← 函数逻辑、边界条件
        └────────────────────┘
```

### 测试比例目标

| 层级     | 覆盖率目标 | 执行频率    | 典型耗时 |
| -------- | ---------- | ----------- | -------- |
| 单元测试 | ≥80%       | 每次提交    | < 30s    |
| 集成测试 | 关键路径   | 每次提交    | < 2min   |
| E2E 测试 | 核心场景   | 每日/发布前 | < 10min  |
| 测死矩阵 | 高风险路径 | 手动/定时   | 10-90min |

---

## 1. 单元测试

### 1.1 配置矩阵测试

对于配置驱动的功能（如认证、TLS），使用表驱动测试覆盖所有配置组合：

```go
// internal/auth/user_integration_test.go
func TestConfigMatrix_AuthInitialization(t *testing.T) {
  cases := []struct {
    name           string
    authEnabled    bool
    expectPassFile bool
    expectUserFile bool
    expectWarning  bool
  }{
    {
      name:           "auth enabled - creates password file",
      authEnabled:    true,
      expectPassFile: true,
      expectUserFile: true,
      expectWarning:  false,
    },
    {
      name:           "auth disabled - no initialization",
      authEnabled:    false,
      expectPassFile: false,
      expectUserFile: false,
      expectWarning:  true,
    },
  }

  for _, tc := range cases {
    t.Run(tc.name, func(t *testing.T) {
      // 在临时目录测试
      // 验证文件创建、日志输出
    })
  }
}
```

### 1.2 边界条件测试

```go
func TestBoundaryConditions(t *testing.T) {
    cases := []struct {
        name     string
        input    string
        expected error
    }{
        {"empty password", "", ErrPasswordTooShort},
        {"min length password", "12345678", nil},
        {"max length username", strings.Repeat("a", 255), nil},
        {"unicode username", "用户名", nil},
    }
    // ...
}
```

### 1.3 测试命名规范

```text
Test<Module>_<Scenario>_<ExpectedBehavior>
```

示例：

- `TestUserStore_DuplicateUsername_ReturnsError`
- `TestTokenManager_ExpiredToken_ReturnsErrTokenExpired`
- `TestConfig_MissingFile_UsesDefaults`

---

## 2. 集成测试

### 2.1 组件交互测试

测试多个模块协作的场景：

```go
// internal/auth/integration_test.go
func TestAuthIntegration_FullLoginFlow(t *testing.T) {
    // 1. 启动 UserStore
    // 2. 创建默认管理员
    // 3. 验证密码文件创建
    // 4. 模拟登录
    // 5. 验证密码文件删除
    // 6. 验证 JWT 有效
}
```

### 2.2 配置加载测试

```go
func TestConfigIntegration_LoadFromFile(t *testing.T) {
    configContent := `
[auth]
enabled = true

[webdav]
auth_type = "basic"
`
    // 写入临时文件
    // 加载配置
    // 验证所有字段正确解析
}
```

### 2.3 数据库/存储集成

```go
func TestCASIntegration_WriteReadVerify(t *testing.T) {
    // 1. 写入数据到 CAS
    // 2. 读取数据
    // 3. 验证 hash 一致性
    // 4. 测试去重
}
```

---

## 3. E2E 测试

### 3.1 用户场景测试

默认入口是 `make e2e`，它通过 `scripts/run-e2e-isolated.sh` 启动临时后端、临时存储和非默认端口，再调用 `scripts/e2e-test.sh` 测试真实用户场景。需要跳过耗时测试时：

```bash
./scripts/run-e2e-isolated.sh --quick
```

`scripts/e2e-test.sh` 可以手动打一个已启动的服务；此时需要显式提供 `BASE_URL` 和对应的临时存储、配置、密钥、初始密码文件路径。

示例场景：

```bash
# 场景：首次启动（认证启用）
test_fresh_install_auth_enabled() {
    rm -rf ~/.mnemonas
    mkdir -p ~/.mnemonas
    echo '[auth]\nenabled = true' > ~/.mnemonas/config.toml
    ./bin/nasd &
    sleep 2
    
    # 验证密码文件创建
    [ -f ~/.mnemonas/.mnemonas/initial-password.txt ] || fail "Password file not created"
    
    # 提取密码并登录
    password=$(grep "Password:" ~/.mnemonas/.mnemonas/initial-password.txt | awk '{print $2}')
    response=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
        -H "Content-Type: application/json" \
        -d "{\"username\":\"admin\",\"password\":\"$password\"}")
    
    # 验证登录成功
    echo "$response" | grep -q '"success":true' || fail "Login failed"
    
    # 验证密码文件删除
    [ ! -f ~/.mnemonas/.mnemonas/initial-password.txt ] || fail "Password file not deleted after login"
}
```

### 3.2 E2E 测试分组

| 分组             | 测试内容           | 模式        |
| ---------------- | ------------------ | ----------- |
| Basic            | 健康检查、版本 API | --quick     |
| File Operations  | CRUD、COPY、MOVE   | --quick     |
| Authentication   | 登录、刷新、权限   | --quick     |
| ETag/Conditional | 304、412 响应      | --quick     |
| Versions         | 版本历史 API       | --quick     |
| Concurrency      | 并发读写           | --quick     |
| Large Files      | 100MB 文件         | --full only |
| Crash Recovery   | 中断恢复           | --full only |

---

## 4. 高级测试方法

### 4.1 属性测试 (Property-Based Testing)

使用 `pgregory.net/rapid` 生成随机输入，验证系统不变量：

```go
import "pgregory.net/rapid"

func TestProperty_PasswordFileLifecycle(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        authEnabled := rapid.Bool().Draw(t, "authEnabled")
        
        // 属性: 当且仅当 auth 启用时，密码文件应存在
        if authEnabled {
            // 初始化后文件存在
            // 登录后文件删除
        } else {
            // 文件始终不存在
        }
    })
}
```

### 4.2 变异测试 (Mutation Testing)

使用 `go-mutesting` 检测测试覆盖盲区：

```bash
# 安装
GO_MUTESTING_VERSION=v0.0.0-20210610104036-6d9217011a00
go install "github.com/zimmski/go-mutesting/cmd/go-mutesting@${GO_MUTESTING_VERSION}"

# 运行
go-mutesting ./internal/auth/...

# 分析未杀死的变异体 → 缺失的测试用例
```

### 4.3 契约测试 (Contract Testing)

定义 API 契约，确保前后端一致：

```yaml
# contracts/auth.pact.yaml
interactions:
  - description: "Login with valid credentials"
    request:
      method: POST
      path: /api/v1/auth/login
      headers:
        Content-Type: application/json
      body:
        username: "admin"
        password: "validpassword"
    response:
      status: 200
      body:
        success: true
        data:
          access_token: !!regexp ^eyJ.*
          refresh_token: !!regexp ^eyJ.*
          token_type: "Bearer"

  - description: "Login with invalid credentials"
    request:
      method: POST
      path: /api/v1/auth/login
      body:
        username: "admin"
        password: "wrongpassword"
    response:
      status: 401
```

### 4.4 模糊测试 (Fuzzing)

Go 1.18+ 原生支持：

```go
// internal/auth/fuzz_test.go
func FuzzPasswordValidation(f *testing.F) {
    f.Add("short")
    f.Add("validpassword123")
    f.Add(strings.Repeat("a", 1000))
    
    f.Fuzz(func(t *testing.T, password string) {
        _, err := validatePassword(password)
        // 不应 panic
        // 结果应一致（相同输入 → 相同输出）
    })
}
```

运行：

```bash
go test -fuzz=FuzzPasswordValidation ./internal/auth/
```

### 4.5 测死矩阵 (Torture Matrix)

`make test-torture` 是发布前和深度排障入口，覆盖普通测试不容易暴露的问题：

```bash
make test-torture
```

默认矩阵包括：

- Go race detector：控制面、认证、分享、存储、版本、数据面客户端、工作区等并发敏感包
- Go 原生 fuzz：路径校验、路径越界防护、WebDAV 前缀归一化
- 前端 property test：工具函数不变量和边界输入
- Playwright 人类交互：文件页真实操作、路径稳定性、布局完整性、运行时错误扫描

本地快速验证可降低耗时：

```bash
GO_FUZZTIME=2s RUN_GO_RACE=0 RUN_E2E_TORTURE=0 make test-torture
```

GitHub Actions 的 `.github/workflows/torture.yml` 提供手动和定时的非破坏性深度测试入口。该 workflow 固定 `RUN_LIVE_FAULTS=0`，不会触发杀进程或数据损坏测试。

### 4.6 破坏性故障注入

`scripts/fault-injection-test.sh` 专门验证崩溃恢复、并发写冲突、版本恢复、对象损坏和元数据损坏处理。它会杀死并重启目标 `nasd`，并可直接改写内部数据文件，所以默认拒绝运行。

最小隔离运行示例：

```bash
MNEMONAS_LIVE_FAULTS=1 \
BASE_URL=http://127.0.0.1:18080 \
STORAGE_ROOT=/tmp/mnemonas-fault-target \
NASD_BIN="$PWD/bin/nasd" \
FAULT_INJECTION_ASSUME_YES=1 \
RUN_CORRUPTION_TESTS=0 \
./scripts/fault-injection-test.sh
```

安全边界：

- 必须显式传入 `BASE_URL`、`STORAGE_ROOT` 和 `NASD_BIN`
- 默认只允许 `/tmp` 或当前 checkout 下的 `STORAGE_ROOT`
- 默认拒绝 `$HOME/.mnemonas`
- 非交互环境必须设置 `FAULT_INJECTION_ASSUME_YES=1`
- 真实存储路径必须额外设置 `ALLOW_REAL_STORAGE=1`

这些门禁由 `scripts/test-fault-injection-safety.sh` 回归测试覆盖，并纳入 `make scripts-check`。

---

## 5. AI 辅助测试

### 5.1 测试用例生成

使用 AI 工具（Copilot、Claude）生成边界用例：

```text
提示词示例：
"分析 internal/auth/user.go 的 createDefaultAdmin 函数，
列出所有可能的边界条件和错误场景，
并生成对应的测试用例。"
```

### 5.2 代码审查辅助

```text
提示词示例：
"审查以下测试代码，指出：
1. 缺失的断言
2. 未覆盖的分支
3. 潜在的竞态条件
4. 测试隔离问题"
```

### 5.3 不变量发现

```text
提示词示例：
"分析 MnemoNAS 认证模块，
识别系统应该始终满足的不变量（invariants），
例如：'密码文件在成功登录后必须被删除'"
```

---

## 6. 持续集成配置

### 6.1 GitHub Actions 工作流

Workflow 配置变更必须先运行 `make workflows-check`。该目标优先使用本机 `actionlint`；未安装时通过固定版本的 `go run github.com/rhysd/actionlint/cmd/actionlint` 执行，并在 CI 中作为独立 job 运行。

```yaml
# .github/workflows/ci.yml
name: CI

on:
  push:
    branches: [main, master]
  pull_request:
    branches: [main, master]

env:
  GO_VERSION: '1.25.9'
  RUST_VERSION: '1.92'
  NODE_VERSION: '22'
  GOLANGCI_LINT_VERSION: 'v2.11.4'
  GOVULNCHECK_VERSION: 'v1.3.0'
  CARGO_AUDIT_VERSION: '0.22.1'

jobs:
  go:
    name: Go
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true

      - name: Setup Rust
        uses: dtolnay/rust-toolchain@stable
        with:
          toolchain: ${{ env.RUST_VERSION }}

      - name: Install protoc
        uses: arduino/setup-protoc@v3
        with:
          version: '3.20.1'

      - name: Install protoc-gen-go
        run: |
          go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
          go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1

      - name: Generate protobuf
        run: make proto

      - name: Check generated protobuf is committed
        run: git diff --exit-code -- proto/dataplane.pb.go proto/dataplane_grpc.pb.go dataplane/src/proto/mnemonas.dataplane.v1.rs

      - name: Lint
        uses: golangci/golangci-lint-action@v9
        with:
          version: ${{ env.GOLANGCI_LINT_VERSION }}
          args: --timeout=5m

      - name: Test
        run: |
          packages=$(make --no-print-directory go-packages)
          CGO_ENABLED=1 bash ./scripts/with-test-dataplane.sh go test -v -race -coverprofile=coverage.out $packages

      - name: Vulnerability scan
        run: |
          go install golang.org/x/vuln/cmd/govulncheck@${{ env.GOVULNCHECK_VERSION }}
          packages=$(make --no-print-directory go-packages)
          "$(go env GOPATH)/bin/govulncheck" $packages

      - name: Upload coverage
        uses: codecov/codecov-action@v4
        with:
          files: coverage.out
          flags: go
          fail_ci_if_error: false

  rust:
    name: Rust
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: dataplane
    steps:
      - uses: actions/checkout@v4

      - name: Setup Rust
        uses: dtolnay/rust-toolchain@stable
        with:
          toolchain: ${{ env.RUST_VERSION }}
          components: rustfmt, clippy

      - name: Cache cargo
        uses: Swatinem/rust-cache@v2
        with:
          workspaces: dataplane

      - name: Format check
        run: cargo fmt --check

      - name: Clippy
        run: cargo clippy -- -D warnings

      - name: Test
        run: cargo test --all-features --locked

      - name: Install cargo-audit
        env:
          CARGO_REGISTRIES_CRATES_IO_PROTOCOL: sparse
        run: cargo install cargo-audit --version "${{ env.CARGO_AUDIT_VERSION }}" --locked

      - name: Dependency audit
        run: cargo audit

      - name: Build
        run: cargo build --release --locked

  frontend:
    name: Frontend
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: web
    steps:
      - uses: actions/checkout@v4

      - name: Setup Node.js
        uses: actions/setup-node@v4
        with:
          node-version: ${{ env.NODE_VERSION }}
          cache: 'npm'
          cache-dependency-path: web/package-lock.json

      - name: Install dependencies
        run: npm ci

      - name: Dependency audit
        run: npm audit --audit-level=high

      - name: Lint
        run: npm run lint

      - name: Type check
        run: npx tsc --noEmit

      - name: Build
        run: npm run build

      - name: Test
        run: npm run test:coverage

      - name: Upload coverage
        uses: codecov/codecov-action@v4
        with:
          files: web/coverage/coverage-final.json
          flags: frontend
          fail_ci_if_error: false

  docker:
    name: Docker Build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3

      - name: Set build metadata
        id: build_meta
        run: echo "build_time=$(date -u +'%Y-%m-%dT%H:%M:%SZ')" >> "$GITHUB_OUTPUT"

      - name: Build
        uses: docker/build-push-action@v5
        with:
          context: .
          push: false
          load: true
          tags: mnemonas:test
          build-args: |
            VERSION=ci
            BUILD_TIME=${{ steps.build_meta.outputs.build_time }}
          cache-from: type=gha
          cache-to: type=gha,mode=max

      - name: Smoke test container
        run: |
          set -euo pipefail

          docker run -d --name mnemonas-smoke -p 18080:8080 mnemonas:test >/dev/null
          cleanup() {
            status=$?
            if [ $status -ne 0 ]; then
              docker logs mnemonas-smoke || true
            fi
            docker rm -f mnemonas-smoke >/dev/null 2>&1 || true
            exit $status
          }
          trap cleanup EXIT

          for _ in $(seq 1 40); do
            if curl -fsS http://127.0.0.1:18080/health >/dev/null; then
              curl -fsS http://127.0.0.1:18080/health | grep -q '"version":"ci"'
              curl -fsS -H 'Accept: text/html' http://127.0.0.1:18080/ | grep -q 'id="root"'
              exit 0
            fi

            if ! docker ps --format '{{.Names}}' | grep -qx 'mnemonas-smoke'; then
              echo "mnemonas smoke container exited before becoming healthy" >&2
              exit 1
            fi

            sleep 1
          done

          echo "timed out waiting for mnemonas smoke container health endpoint" >&2
          exit 1
```

### 6.2 测试覆盖率门槛

```yaml
# codecov.yml
coverage:
  status:
    project:
      default:
        target: 80%
        threshold: 2%
    patch:
      default:
        target: 90%
```

---

## 7. 测试文件组织

```text
internal/
├── auth/
│   ├── auth_test.go           # 单元测试
│   ├── integration_test.go    # 集成测试
│   └── fuzz_test.go           # 模糊测试
├── config/
│   ├── config_test.go         # 配置解析测试
│   └── matrix_test.go         # 配置矩阵测试
scripts/
├── e2e-test.sh                # E2E 测试
├── run-e2e-isolated.sh        # 隔离 E2E 包装入口
├── torture-test.sh            # 非破坏性测死矩阵
├── fault-injection-test.sh    # 显式隔离目标上的破坏性故障注入
├── run-benchmark-isolated.sh  # 隔离 benchmark 包装入口
├── test-benchmark-safety.sh   # benchmark 门禁回归测试
├── test-fault-injection-safety.sh # 故障注入门禁回归测试
└── benchmark.sh               # 性能测试
.github/workflows/
└── torture.yml                # 手动/定时非破坏性深度测试
contracts/
└── auth.pact.yaml             # API 契约
testdata/
├── configs/                   # 测试配置文件
└── fixtures/                  # 测试数据
```

---

## 8. 测试清单

### 新功能开发清单

- [ ] 单元测试：核心逻辑覆盖
- [ ] 单元测试：边界条件
- [ ] 单元测试：错误处理
- [ ] 集成测试：与其他模块交互
- [ ] E2E 测试：用户场景
- [ ] 文档更新

### 配置相关功能清单

- [ ] 配置启用时行为正确
- [ ] 配置禁用时行为正确（无副作用）
- [ ] 配置缺失时使用默认值
- [ ] 配置无效时报错清晰
- [ ] 日志明确指示当前配置状态

### Bug 修复清单

- [ ] 添加复现 bug 的测试用例
- [ ] 修复后测试通过
- [ ] 检查相关功能无回归

---

## 9. 参考资源

- [Go Testing Package](https://pkg.go.dev/testing)
- [Rapid Property Testing](https://pkg.go.dev/pgregory.net/rapid)
- [go-mutesting](https://github.com/zimmski/go-mutesting)
- [Pact Contract Testing](https://docs.pact.io/)
- [Google Testing Blog](https://testing.googleblog.com/)
- [Playwright](https://playwright.dev/)
- [Vitest](https://vitest.dev/)

---

## 10. 前端测试策略

### 10.1 测试分层

```text
┌─────────────────────────────────────────┐
│        视觉回归测试 (Playwright)         │  ← 多视口截图对比、防止 UI 回归
├─────────────────────────────────────────┤
│        E2E 测试 (Playwright)            │  ← 用户场景、跨组件交互
├─────────────────────────────────────────┤
│        组件测试 (Vitest + Testing Library)│ ← 组件状态、Props、交互
├─────────────────────────────────────────┤
│        单元测试 (Vitest)                │  ← 工具函数、Hooks、Store
└─────────────────────────────────────────┘
```

### 10.2 单元测试 (Vitest)

测试纯函数、工具模块、状态管理：

```typescript
// src/lib/utils.test.ts
import { describe, it, expect } from 'vitest'
import { formatBytes, formatNumber } from './utils'

describe('formatBytes', () => {
  it('formats 0 bytes', () => {
    expect(formatBytes(0)).toBe('0 Bytes')
  })

  it('formats kilobytes', () => {
    expect(formatBytes(1024)).toBe('1 KB')
  })

  it('formats gigabytes', () => {
    expect(formatBytes(1073741824)).toBe('1 GB')
  })
})
```

```typescript
// src/stores/theme.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useThemeStore } from './theme'

describe('useThemeStore', () => {
  beforeEach(() => {
    localStorage.clear()
    useThemeStore.setState({ theme: 'dark' })
  })

  it('can toggle theme', () => {
    const { result } = renderHook(() => useThemeStore())
    
    act(() => {
      result.current.toggleTheme()
    })
    
    expect(result.current.theme).toBe('light')
  })
})
```

**运行命令**：

> 前端工具链需 Node.js `^20.19.0` 或 `>=22.12.0`；推荐使用项目 `.nvmrc` 指定的 22.x。

```bash
cd web

# 版本检查
npm run check:node

# 运行所有单元测试
npm test

# 带 UI 界面
npm run test:ui

# 生成覆盖率报告
npm run test:coverage
```

### 10.3 组件测试 (Vitest + Testing Library)

当前仓库只保留可运行的前端测试入口：组件和页面状态用 Vitest + Testing Library 覆盖，视觉回归用 Playwright 截图覆盖。不要在 `package.json` 中暴露未安装依赖的测试命令。

```typescript
// src/components/ThemeToggle.test.tsx
import { describe, expect, it } from 'vitest'
import { render, screen } from '@/test/utils'
import { ThemeToggle } from '../components/ThemeToggle'

describe('ThemeToggle', () => {
  it('renders the theme control', () => {
    render(<ThemeToggle />)
    expect(screen.getByRole('button')).toBeTruthy()
  })
})
```

**运行命令**：

```bash
cd web

# 运行组件和页面单测
npm run test:run
```

### 10.4 E2E 测试 (Playwright)

测试完整用户流程：

```typescript
// e2e/login.spec.ts
import { test, expect } from '@playwright/test'

test.describe('登录页面', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/login')
  })

  test('应显示登录表单', async ({ page }) => {
    await expect(page.getByLabel(/用户名/i)).toBeVisible()
    await expect(page.getByLabel(/密码/i)).toBeVisible()
    await expect(page.getByRole('button', { name: /登录/i })).toBeVisible()
  })

  test('错误密码应显示错误提示', async ({ page }) => {
    await page.getByLabel(/用户名/i).fill('admin')
    await page.getByLabel(/密码/i).fill('wrongpassword')
    await page.getByRole('button', { name: /登录/i }).click()
    
    await expect(page.getByText(/错误|失败/i)).toBeVisible()
  })

  test('视觉回归 - 登录页截图', async ({ page }) => {
    await expect(page).toHaveScreenshot('login-page.png', {
      maxDiffPixelRatio: 0.05,
    })
  })
})
```

**运行命令**：

```bash
cd web

# 运行所有 E2E 测试
npm run test:e2e

# 快速回归桌面/移动导航和响应式外壳
npm run test:e2e:navigation

# 带 UI 界面（调试模式）
npm run test:e2e:ui

# 更新截图基准
npm run test:e2e:update
```

默认 Playwright 配置会自动启动隔离的后端和前端测试服务器；`MNEMONAS_E2E_BACKEND_URL` 和 `MNEMONAS_E2E_FRONTEND_URL` 可调整隔离服务器地址或端口。本地已有服务只有在设置 `MNEMONAS_E2E_REUSE_EXISTING=1` 时才会被复用，调试已有环境时还应显式设置 `E2E_PASSWORD`。

**E2E 测试目录结构**：

```text
web/e2e/
├── login.spec.ts           # 登录流程测试
├── dashboard.spec.ts       # 仪表板测试
├── files.spec.ts           # 文件管理测试
└── *.spec.ts-snapshots/    # 截图基准
```

### 10.5 视觉回归测试

视觉回归由 Playwright 截图断言统一覆盖，避免维护第二套未接入 CI 的截图工具链：

```bash
cd web

# 运行含截图断言的 E2E 测试
npm run test:e2e

# 更新截图基准
npm run test:e2e:update
```

**工作流程**：

1. Playwright 启动隔离的后端和 Vite 前端
2. 关键页面在桌面、平板和移动视口执行交互测试
3. `toHaveScreenshot` 对比截图基准
4. 失败时保留 Playwright report 和截图差异供排查

### 10.6 测试文件组织

```text
web/
├── src/
│   ├── components/
│   │   └── ThemeToggle.tsx
│   ├── stores/
│   │   ├── theme.ts
│   │   └── theme.test.ts        # Store 单元测试
│   ├── lib/
│   │   ├── utils.ts
│   │   └── utils.test.ts        # 工具函数测试
│   ├── pages/
│   │   ├── Login.tsx
│   │   └── Login.test.tsx       # 页面组件测试
│   └── test/
│       └── setup.ts                 # 测试 setup
├── e2e/
│   ├── login.spec.ts            # E2E 测试
│   ├── dashboard.spec.ts
│   └── *.spec.ts-snapshots/     # 截图基准
└── playwright.config.ts         # Playwright 配置
```

### 10.7 CI 集成

```yaml
# .github/workflows/frontend-test.yml
name: Frontend Tests

on: [push, pull_request]

jobs:
  unit-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
      - name: Install dependencies
        run: cd web && npm ci
      - name: Run unit tests
        run: cd web && npm run test:run
      - name: Upload coverage
        uses: codecov/codecov-action@v4

  e2e-tests:
    runs-on: ubuntu-latest
    needs: unit-tests
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
      - name: Install dependencies
        run: cd web && npm ci
      - name: Install Playwright browsers
        run: cd web && npx playwright install --with-deps
      - name: Run E2E tests
        run: cd web && npm run test:e2e
      - uses: actions/upload-artifact@v4
        if: failure()
        with:
          name: playwright-report
          path: web/playwright-report

```

### 10.8 前端测试清单

#### 新组件开发清单

- [ ] Props 测试：各种输入组合
- [ ] 无障碍测试：键盘导航、屏幕阅读器
- [ ] 响应式测试：移动端/平板/桌面
- [ ] 视觉回归：必要时补 Playwright 截图基准

#### 新页面开发清单

- [ ] 页面组件测试：渲染、交互
- [ ] E2E 测试：完整用户流程
- [ ] 视觉回归：各设备尺寸截图
- [ ] 错误状态：网络错误、空数据
- [ ] 加载状态：骨架屏、加载指示器

#### 提交前检查清单

- [ ] 所有单元测试通过
- [ ] E2E 测试通过
- [ ] 视觉回归无意外变化
- [ ] 无 TypeScript 错误
- [ ] 无 ESLint 警告
