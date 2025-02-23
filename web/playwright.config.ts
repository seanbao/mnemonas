import { defineConfig, devices } from '@playwright/test'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const STORAGE_STATE = './e2e/.auth/user.json'
const CONFIG_DIR = path.dirname(fileURLToPath(import.meta.url))
const E2E_ROOT = process.env.MNEMONAS_E2E_ROOT || '/tmp/mnemonas-playwright'
const BACKEND_URL = process.env.MNEMONAS_E2E_BACKEND_URL || 'http://127.0.0.1:18080'
const FRONTEND_URL = process.env.MNEMONAS_E2E_FRONTEND_URL || 'http://127.0.0.1:4173'

process.env.E2E_USERNAME ||= 'admin'
process.env.E2E_PASSWORD_FILE ||= path.join(E2E_ROOT, 'backend', 'e2e-password.txt')

/**
 * Playwright E2E 测试配置
 * @see https://playwright.dev/docs/test-configuration
 * 
 * 认证说明：
 * - setup project 会先执行登录并保存状态到 .auth/user.json
 * - chromium 和 mobile projects 依赖 setup，会复用登录状态
 * - 默认启动隔离的后端/前端测试环境，不依赖用户本地 8080/5173 实例
 * - 测试账号优先读 E2E_USERNAME / E2E_PASSWORD，其次回退到 E2E_PASSWORD_FILE
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
    baseURL: FRONTEND_URL,
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

  /* Run isolated backend + frontend for browser tests */
  webServer: [
    {
      name: 'mnemonas-e2e-backend',
      command: 'bash ./scripts/start-e2e-backend.sh',
      cwd: CONFIG_DIR,
      env: {
        ...process.env,
        MNEMONAS_E2E_ROOT: E2E_ROOT,
      },
      url: `${BACKEND_URL}/health`,
      reuseExistingServer: false,
      timeout: 180 * 1000,
      stdout: 'pipe',
      stderr: 'pipe',
      gracefulShutdown: {
        signal: 'SIGTERM',
        timeout: 5000,
      },
    },
    {
      name: 'mnemonas-e2e-frontend',
      command: 'npm run dev -- --host 127.0.0.1 --port 4173',
      cwd: CONFIG_DIR,
      env: {
        ...process.env,
        VITE_API_PROXY_TARGET: BACKEND_URL,
      },
      url: FRONTEND_URL,
      reuseExistingServer: false,
      timeout: 120 * 1000,
    },
  ],
})
