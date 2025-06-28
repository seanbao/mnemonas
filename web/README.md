# MnemoNAS Web Frontend

[English](README.en.md) | 简体中文

MnemoNAS 前端应用，基于 React 19 + TypeScript + Vite 构建。

## 技术栈

- **框架**: React 19 + TypeScript
- **构建工具**: Vite 7
- **UI 组件库**: HeroUI
- **样式**: Tailwind CSS v4
- **状态管理**: Zustand + TanStack Query
- **路由**: React Router v7

## 开发

```bash
# 安装依赖
npm ci

# 启动开发服务器 (http://localhost:5173)
npm run dev

# 构建生产版本
npm run build

# 预览生产构建
npm run preview
```

## 测试

> 前端命令需 Node.js `^20.19.0` 或 `>=22.12.0`；推荐使用仓库 `.nvmrc` 指定的 22.x。

### 单元测试 (Vitest)

```bash
# 版本检查
npm run check:node

# 运行所有单元测试
npm test

# 带 UI 界面
npm run test:ui

# 单次运行（CI 模式）
npm run test:run

# 生成覆盖率报告
npm run test:coverage
```

### E2E 测试 (Playwright)

```bash
# 首次运行需安装浏览器
npx playwright install

# Playwright 会自动启动隔离的后端和 Vite 前端

# 如需使用已有环境运行受保护页面测试，显式复用已有服务并提供管理员凭据
export MNEMONAS_E2E_REUSE_EXISTING=1
export MNEMONAS_E2E_BACKEND_URL=http://127.0.0.1:8080
export MNEMONAS_E2E_FRONTEND_URL=http://127.0.0.1:5173
export E2E_USERNAME=admin
export E2E_PASSWORD='your-password'

# 运行所有 E2E 测试
npm run test:e2e

# 快速回归桌面/移动导航与响应式外壳
npm run test:e2e:navigation

# 带 UI 界面（调试模式）
npm run test:e2e:ui

# 更新截图基准
npm run test:e2e:update
```

说明：

- 受保护页面测试会优先读取 `E2E_PASSWORD`，也支持通过 `E2E_PASSWORD_FILE` 指向初始密码文件。
- 默认配置会启动隔离的测试后端，自动生成初始密码并写入 `MNEMONAS_E2E_ROOT` 下的 password file。
- `MNEMONAS_E2E_ROOT` 必须位于 `/tmp` 或当前 checkout 下，且不能包含 `..` 或符号链接路径组件。
- `MNEMONAS_E2E_BACKEND_URL` 和 `MNEMONAS_E2E_FRONTEND_URL` 可用于调整隔离测试服务器的端口；设置 `MNEMONAS_E2E_REUSE_EXISTING=1` 时才会跳过自动启动并连接已有服务。
- 未设置 `E2E_PASSWORD_FILE` 时，Playwright 会尝试读取默认初始密码文件路径：`~/.mnemonas/.mnemonas/initial-password.txt`，兼容默认配置布局。
- 如果当前环境没有可用管理员密码，受保护页面测试会自动跳过，而不会继续猜测默认密码并触发登录限流。

截图回归由 Playwright 用例中的 `toHaveScreenshot` 覆盖；更新基准统一使用 `npm run test:e2e:update`。

## 项目结构

```text
src/
├── components/       # 可复用组件
│   ├── layout/       # 布局组件（Sidebar, Header）
│   ├── auth/         # 认证相关组件
│   └── share/        # 分享功能组件
├── pages/            # 页面组件
├── stores/           # Zustand 状态管理
├── lib/              # 工具函数
├── hooks/            # 自定义 Hooks
├── test/             # 测试工具与全局 setup
└── types/            # TypeScript 类型定义

e2e/                  # Playwright E2E 测试
```

## 代码规范

- ESLint 检查：`npm run lint`
- 提交前自动检查（husky + lint-staged）

## 依赖维护

前端栈使用 React 19、Vite、Tailwind CSS v4、HeroUI、TanStack Query 和 Playwright。常规维护先做兼容范围内的 patch/minor 更新，并跑完 lint、单测、构建和关键 E2E：

```bash
npm outdated --long
npm run lint
npm run test:run
npm run build
npm run test:e2e
```

React、Vite、HeroUI、Tailwind、TypeScript、Vitest 或 Playwright 的 major 升级需要单独分支验证，尤其要检查 HeroUI 交互组件、移动端布局和截图回归。

## UI 规范

- 共享 UI 组件统一放在 `src/components/ui/`（如 PageHeader、StatCard、EmptyState、FileIcon）。
- 页面标题区域优先使用 PageHeader，统计卡片优先使用 StatCard。
- 空状态统一使用 EmptyState，文件类型图标统一使用 FileIcon。
- 颜色与背景使用 HeroUI 语义 token（如 bg-content1、bg-content2、text-foreground、text-default-500）。
- 工具函数优先复用 `src/lib/utils.ts`（如 formatBytes、formatRelativeTime）。
- 视觉风格以“现代、克制、可长期使用”为目标：保留品牌色和轻量层级，避免大面积玻璃、漂浮光球、强发光和过度紫蓝渐变。
- operational 页面优先信息扫描效率，使用 8px 以内圆角、细边框、稳定尺寸和明确状态色；不要用营销式大 hero、装饰卡片堆叠或卡片嵌套卡片。
- 移动端必须是独立可用体验：常用路径应能通过底部主导航或明确按钮到达，内容不能被 header、抽屉或底部导航遮挡。
- 改动登录页、应用外壳、导航或关键响应式布局时，至少运行相关 Playwright 用例并检查截图基准是否需要更新。
- 改动导航、侧栏、底部移动导航或页面外壳时，优先跑 `npm run test:e2e:navigation`，再按影响范围补充完整 E2E 或截图回归。

## 相关文档

- [测试策略](../docs/testing-strategy.md) - 完整测试方案说明
- [开发指南](../docs/development.md) - 开发环境搭建
