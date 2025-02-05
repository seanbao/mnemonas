# MnemoNAS Web Frontend

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
npm install

# 启动开发服务器 (http://localhost:5173)
npm run dev

# 构建生产版本
npm run build

# 预览生产构建
npm run preview
```

## 测试

### 单元测试 (Vitest)

```bash
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

# 运行所有 E2E 测试
npm run test:e2e

# 带 UI 界面（调试模式）
npm run test:e2e:ui

# 更新截图基准
npm run test:e2e:update
```

### 组件文档 (Storybook)

```bash
# 启动 Storybook 开发服务器 (http://localhost:6006)
npm run storybook

# 构建静态 Storybook
npm run build-storybook
```

### 视觉回归测试

```bash
# 运行视觉回归测试（对比截图）
npm run test:visual

# 更新视觉基准截图
npm run test:visual:update
```

## 项目结构

```
src/
├── components/       # 可复用组件
│   ├── layout/       # 布局组件（Sidebar, Header）
│   ├── auth/         # 认证相关组件
│   └── share/        # 分享功能组件
├── pages/            # 页面组件
├── stores/           # Zustand 状态管理
├── lib/              # 工具函数
├── hooks/            # 自定义 Hooks
├── stories/          # Storybook Stories
└── types/            # TypeScript 类型定义

e2e/                  # Playwright E2E 测试
.storybook/           # Storybook 配置
```

## 代码规范

- ESLint 检查：`npm run lint`
- 提交前自动检查（husky + lint-staged）

## UI 规范

- 共享 UI 组件统一放在 `src/components/ui/`（如 PageHeader、StatCard、EmptyState、FileIcon）。
- 页面标题区域优先使用 PageHeader，统计卡片优先使用 StatCard。
- 空状态统一使用 EmptyState，文件类型图标统一使用 FileIcon。
- 颜色与背景使用 HeroUI 语义 token（如 bg-content1、bg-content2、text-foreground、text-default-500）。
- 工具函数优先复用 `src/lib/utils.ts`（如 formatBytes、formatRelativeTime）。

## 相关文档

- [测试策略](../docs/testing-strategy.md) - 完整测试方案说明
- [开发指南](../docs/development.md) - 开发环境搭建

