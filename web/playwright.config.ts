import { defineConfig, devices } from '@playwright/test'

const STORAGE_STATE = './e2e/.auth/user.json'

/**
 * Playwright E2E 测试配置
 * @see https://playwright.dev/docs/test-configuration
 * 
 * 认证说明：
 * - setup project 会先执行登录并保存状态到 .auth/user.json
 * - chromium 和 mobile projects 依赖 setup，会复用登录状态
 * - 通过环境变量 E2E_USERNAME 和 E2E_PASSWORD 配置测试账号
 */
export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: [
    ['html', { open: 'never' }],
    ['list'],
  ],
  
  use: {
    baseURL: 'http://localhost:5173',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },

  projects: [
    // Setup project - run authentication first
    {
      name: 'setup',
      testMatch: /auth\.setup\.ts/,
    },
    // Main browser tests - depend on setup
    {
      name: 'chromium',
      use: { 
        ...devices['Desktop Chrome'],
        storageState: STORAGE_STATE,
      },
      dependencies: ['setup'],
    },
    // Mobile viewport - depend on setup
    {
      name: 'mobile',
      use: { 
        ...devices['iPhone 13'],
        storageState: STORAGE_STATE,
      },
      dependencies: ['setup'],
    },
  ],

  /* Run local dev server before tests */
  webServer: {
    command: 'npm run dev',
    url: 'http://localhost:5173',
    reuseExistingServer: !process.env.CI,
    timeout: 120 * 1000,
  },
})
