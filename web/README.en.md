# MnemoNAS Web Frontend

English | [简体中文](README.md)

MnemoNAS frontend application built with React 19, TypeScript, and Vite.

## Technology Stack

- **Framework**: React 19 + TypeScript
- **Build tool**: Vite 7
- **UI components**: HeroUI
- **Styling**: Tailwind CSS v4
- **State**: Zustand + TanStack Query
- **Routing**: React Router v7

## Development

```bash
# Install dependencies
npm ci

# Start dev server (http://localhost:5173)
npm run dev

# Build production assets
npm run build

# Preview production build
npm run preview
```

## Testing

> Frontend commands require Node.js `^20.19.0` or `>=22.12.0`; the repository `.nvmrc` pins the recommended 22.x line.

### Unit Tests (Vitest)

```bash
npm run check:node
npm test
npm run test:ui
npm run test:run
npm run test:coverage
```

### E2E Tests (Playwright)

```bash
# Install browsers before the first run
npx playwright install

# Playwright starts an isolated backend, builds the frontend, and serves it through Vite preview by default.

# Reusing an existing environment for protected-page tests requires explicit services and admin credentials.
export MNEMONAS_E2E_REUSE_EXISTING=1
export MNEMONAS_E2E_BACKEND_URL=http://127.0.0.1:8080
export MNEMONAS_E2E_FRONTEND_URL=http://127.0.0.1:5173
export E2E_USERNAME=admin
export E2E_PASSWORD_FILE="$HOME/.mnemonas/.mnemonas/initial-password.txt"
# If the admin password was changed and no password file is used:
# export E2E_PASSWORD="<admin-password>"

npm run test:e2e
npm run test:e2e:navigation
npm run test:e2e:ui
npm run test:e2e:update
```

Notes:

- Protected-page tests prefer `E2E_PASSWORD` and also support `E2E_PASSWORD_FILE`.
- The default configuration starts an isolated test backend, builds the frontend, serves it through Vite preview, generates an initial password, and writes it under `MNEMONAS_E2E_ROOT`.
- The isolated backend uses a 2-hour access-token lifetime and a 168-hour refresh-token lifetime to reduce shared storageState expiration risk during long parallel test runs.
- `MNEMONAS_E2E_ROOT` must be under `/tmp` or the current checkout and must not contain `..` or symlink path components.
- The default isolated ports are backend `18180` and frontend `14173`; `MNEMONAS_E2E_BACKEND_URL` and `MNEMONAS_E2E_FRONTEND_URL` can adjust isolated test server ports. `MNEMONAS_E2E_REUSE_EXISTING=1` is required to skip automatic startup.
- Local Playwright runs use 4 workers by default; set `MNEMONAS_E2E_WORKERS` to a positive integer to override it. CI always uses 1 worker.
- Without `E2E_PASSWORD_FILE`, Playwright tries the default initial-password path `~/.mnemonas/.mnemonas/initial-password.txt`.
- Protected-page tests skip automatically when no admin password is available.

Screenshot regression coverage uses Playwright `toHaveScreenshot`; update baselines with `npm run test:e2e:update`.

## Project Structure

```text
src/
├── components/       # Reusable components
│   ├── layout/       # Layout components
│   ├── auth/         # Auth components
│   └── share/        # Share components
├── pages/            # Page components
├── stores/           # Zustand stores
├── lib/              # Utilities
├── hooks/            # Custom hooks
├── test/             # Test utilities and global setup
└── types/            # TypeScript types

e2e/                  # Playwright E2E tests
```

## Code Quality

- ESLint: `npm run lint`
- Pre-commit checks through husky and lint-staged

## Dependency Maintenance

The frontend stack uses React 19, Vite, Tailwind CSS v4, HeroUI, TanStack Query, and Playwright. For routine maintenance, update compatible patch/minor versions and run:

```bash
npm outdated --long
npm run lint
npm run test:run
npm run build
npm run test:e2e
```

Major upgrades for React, Vite, HeroUI, Tailwind, TypeScript, Vitest, or Playwright need separate validation, especially around HeroUI interactive components, mobile layout, and screenshot regression.

## UI Guidelines

- Shared UI components live under `src/components/ui/`.
- Page title areas should use PageHeader; statistic cards should use StatCard.
- Empty states should use EmptyState; file-type icons should use FileIcon.
- Colors and backgrounds should use HeroUI semantic tokens such as `bg-content1`, `bg-content2`, `text-foreground`, and `text-default-500`.
- Utility functions should reuse `src/lib/utils.ts` where practical, such as `formatBytes` and `formatRelativeTime`.
- Native `<button>` elements in production code must declare `type` explicitly to avoid implicit form submission when reused inside forms.
- The visual style should be compact, calm, and suitable for repeated operational use.
- Operational pages should prioritize scanning efficiency, 8px-or-smaller radii, fine borders, stable dimensions, and explicit state colors.
- Mobile must be independently usable: common paths should be reachable through bottom navigation or clear actions, and content must not be hidden by headers, drawers, or bottom navigation.
- Changes to login, app shell, navigation, or key responsive layouts should run relevant Playwright tests and check screenshot baselines.
- Changes to navigation, sidebar, mobile bottom navigation, or page shell should run `npm run test:e2e:navigation` first, then add full E2E or screenshot regression as needed.

## Related Docs

- [Testing strategy](../docs/testing-strategy.en.md)
- [Development guide](../docs/development.en.md)
