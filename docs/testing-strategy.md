# MnemoNAS 测试策略

本文档定义 MnemoNAS 项目的多层测试策略，确保功能正确性、配置行为一致性和系统可靠性。

## 测试金字塔

```
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

| 层级     | 覆盖率目标 | 执行频率        | 典型耗时   |
|----------|-----------|-----------------|-----------|
| 单元测试  | ≥80%      | 每次提交        | < 30s     |
| 集成测试  | 关键路径   | 每次 PR         | < 2min    |
| E2E 测试 | 核心场景   | 每日/发布前     | < 10min   |

---

## 1. 单元测试

### 1.1 配置矩阵测试

对于配置驱动的功能（如认证、TLS），使用表驱动测试覆盖所有配置组合：

```go
// internal/auth/user_integration_test.go
func TestConfigMatrix_AuthInitialization(t *testing.T) {
    cases := []struct {
        name            string
        authEnabled     bool
        expectPassFile  bool
        expectUserFile  bool
        expectWarning   bool
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

```
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

位于 `scripts/e2e-test.sh`，测试真实用户场景：

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

| 分组              | 测试内容                           | 模式        |
|-------------------|-----------------------------------|-------------|
| Basic             | 健康检查、版本 API                  | --quick     |
| File Operations   | CRUD、COPY、MOVE                   | --quick     |
| Authentication    | 登录、刷新、权限                    | --quick     |
| ETag/Conditional  | 304、412 响应                      | --quick     |
| Versions          | 版本历史 API                       | --quick     |
| Concurrency       | 并发读写                           | --quick     |
| Large Files       | 100MB 文件                         | --full only |
| Crash Recovery    | 中断恢复                           | --full only |

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
go install github.com/zimmski/go-mutesting/cmd/go-mutesting@latest

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
        access_token: !!regexp ^eyJ.*
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

---

## 5. AI 辅助测试

### 5.1 测试用例生成

使用 AI 工具（Copilot、Claude）生成边界用例：

```
提示词示例：
"分析 internal/auth/user.go 的 createDefaultAdmin 函数，
列出所有可能的边界条件和错误场景，
并生成对应的测试用例。"
```

### 5.2 代码审查辅助

```
提示词示例：
"审查以下测试代码，指出：
1. 缺失的断言
2. 未覆盖的分支
3. 潜在的竞态条件
4. 测试隔离问题"
```

### 5.3 不变量发现

```
提示词示例：
"分析 MnemoNAS 认证模块，
识别系统应该始终满足的不变量（invariants），
例如：'密码文件在成功登录后必须被删除'"
```

---

## 6. 持续集成配置

### 6.1 GitHub Actions 工作流

```yaml
# .github/workflows/test.yml
name: Tests

on: [push, pull_request]

jobs:
  unit-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - name: Run unit tests
        run: go test -v -race -coverprofile=coverage.out ./...
      - name: Upload coverage
        uses: codecov/codecov-action@v4

  e2e-tests:
    runs-on: ubuntu-latest
    needs: unit-tests
    steps:
      - uses: actions/checkout@v4
      - name: Build
        run: make build
      - name: Start services
        run: |
          ./bin/dataplane &
          ./bin/nasd &
          sleep 3
      - name: Run E2E tests
        run: ./scripts/e2e-test.sh --quick
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

```
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
└── benchmark.sh               # 性能测试
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
- [Storybook](https://storybook.js.org/)
- [Vitest](https://vitest.dev/)

---

## 10. 前端测试策略

> 借鉴 Meridian 项目的优秀实践

### 10.1 测试分层

```
┌─────────────────────────────────────────┐
│        视觉回归测试 (Visual)             │  ← 截图对比、防止 UI 回归
├─────────────────────────────────────────┤
│        E2E 测试 (Playwright)            │  ← 用户场景、跨组件交互
├─────────────────────────────────────────┤
│        组件测试 (Storybook + Vitest)    │  ← 组件隔离、Props 验证
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

```bash
cd web

# 运行所有单元测试
npm test

# 带 UI 界面
npm run test:ui

# 生成覆盖率报告
npm run test:coverage
```

### 10.3 组件测试 (Storybook)

使用 Storybook 进行组件隔离开发和测试：

```typescript
// src/stories/ThemeToggle.stories.tsx
import type { Meta, StoryObj } from '@storybook/react'
import { ThemeToggle } from '../components/ThemeToggle'

const meta: Meta<typeof ThemeToggle> = {
  title: 'Components/ThemeToggle',
  component: ThemeToggle,
  parameters: {
    layout: 'centered',
  },
  tags: ['autodocs'],
}

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {}

export const OnDarkBackground: Story = {
  decorators: [
    (Story) => (
      <div className="p-8 bg-gray-900 rounded-lg">
        <Story />
      </div>
    ),
  ],
}
```

**运行命令**：

```bash
cd web

# 启动 Storybook 开发服务器
npm run storybook

# 构建静态 Storybook
npm run build-storybook
```

**Storybook 优势**：

- 组件隔离开发，无需启动完整应用
- 自动生成文档 (autodocs)
- 可视化 Props 调试
- 无障碍检查 (a11y addon)
- 支持视觉回归测试

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

# 带 UI 界面（调试模式）
npm run test:e2e:ui

# 更新截图基准
npm run test:e2e:update
```

**E2E 测试目录结构**：

```
web/e2e/
├── login.spec.ts           # 登录流程测试
├── dashboard.spec.ts       # 仪表板测试
├── files.spec.ts           # 文件管理测试
└── *.spec.ts-snapshots/    # 截图基准
```

### 10.5 视觉回归测试

使用 Storycap + reg-cli 进行视觉回归：

```bash
cd web

# 运行视觉回归测试
npm run test:visual

# 更新视觉基准
npm run test:visual:update
```

**工作流程**：

1. Storycap 自动截取所有 Stories 的截图
2. reg-cli 对比当前截图与基准截图
3. 生成 HTML 报告显示差异
4. CI 中自动检测视觉回归

### 10.6 测试文件组织

```
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
│   └── stories/
│       └── ThemeToggle.stories.tsx
├── e2e/
│   ├── login.spec.ts            # E2E 测试
│   ├── dashboard.spec.ts
│   └── *.spec.ts-snapshots/     # 截图基准
├── __screenshots__/             # 视觉回归截图
│   ├── expected/                # 基准截图
│   └── diff/                    # 差异截图
├── .storybook/
│   ├── main.ts                  # Storybook 配置
│   └── preview.tsx              # 全局装饰器
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

  storybook:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
      - name: Install dependencies
        run: cd web && npm ci
      - name: Build Storybook
        run: cd web && npm run build-storybook
      - name: Deploy to GitHub Pages
        uses: peaceiris/actions-gh-pages@v4
        if: github.ref == 'refs/heads/main'
        with:
          github_token: ${{ secrets.GITHUB_TOKEN }}
          publish_dir: web/storybook-static
```

### 10.8 前端测试清单

#### 新组件开发清单

- [ ] Storybook Story：覆盖主要状态
- [ ] Props 测试：各种输入组合
- [ ] 无障碍测试：键盘导航、屏幕阅读器
- [ ] 响应式测试：移动端/平板/桌面
- [ ] 视觉回归：截图基准

#### 新页面开发清单

- [ ] 页面组件测试：渲染、交互
- [ ] E2E 测试：完整用户流程
- [ ] 视觉回归：各设备尺寸截图
- [ ] 错误状态：网络错误、空数据
- [ ] 加载状态：骨架屏、加载指示器

#### PR 检查清单

- [ ] 所有单元测试通过
- [ ] E2E 测试通过
- [ ] 视觉回归无意外变化
- [ ] Storybook 构建成功
- [ ] 无 TypeScript 错误
- [ ] 无 ESLint 警告

