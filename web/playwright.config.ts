import { defineConfig, devices } from '@playwright/test'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

delete process.env.NO_COLOR

const STORAGE_STATE = './e2e/.auth/user.json'
const CONFIG_DIR = path.dirname(fileURLToPath(import.meta.url))
const E2E_ROOT = process.env.MNEMONAS_E2E_ROOT || '/tmp/mnemonas-playwright'
const BACKEND_URL = process.env.MNEMONAS_E2E_BACKEND_URL || 'http://127.0.0.1:18180'
const FRONTEND_URL = process.env.MNEMONAS_E2E_FRONTEND_URL || 'http://127.0.0.1:14173'
const REUSE_EXISTING_SERVER = process.env.MNEMONAS_E2E_REUSE_EXISTING === '1'
const ALLOW_AUTH_SKIP = process.env.MNEMONAS_E2E_ALLOW_AUTH_SKIP ?? (REUSE_EXISTING_SERVER ? '1' : '0')
const BACKEND_PARSED_URL = new URL(BACKEND_URL)
const BACKEND_PORT = BACKEND_PARSED_URL.port || (BACKEND_PARSED_URL.protocol === 'https:' ? '443' : '80')
const FRONTEND_PARSED_URL = new URL(FRONTEND_URL)
const RAW_BACKEND_HOST = BACKEND_PARSED_URL.hostname || '127.0.0.1'
const RAW_FRONTEND_HOST = FRONTEND_PARSED_URL.hostname || '127.0.0.1'
const BACKEND_HOST = normalizeTcpHostForCli('MNEMONAS_E2E_BACKEND_URL host', RAW_BACKEND_HOST)
const FRONTEND_HOST = normalizeTcpHostForCli('MNEMONAS_E2E_FRONTEND_URL host', RAW_FRONTEND_HOST)
const FRONTEND_PORT = FRONTEND_PARSED_URL.port || (FRONTEND_PARSED_URL.protocol === 'https:' ? '443' : '80')
const WEB_SERVER_ENV = { ...process.env }
const LOCAL_WORKERS = parseOptionalPositiveInteger('MNEMONAS_E2E_WORKERS', process.env.MNEMONAS_E2E_WORKERS) ?? 4
const TEST_TIMEOUT_MS = parseOptionalPositiveInteger('MNEMONAS_E2E_TEST_TIMEOUT_MS', process.env.MNEMONAS_E2E_TEST_TIMEOUT_MS) ?? 60_000
const EXPECT_TIMEOUT_MS = parseOptionalPositiveInteger('MNEMONAS_E2E_EXPECT_TIMEOUT_MS', process.env.MNEMONAS_E2E_EXPECT_TIMEOUT_MS) ?? 10_000
delete WEB_SERVER_ENV.NO_COLOR

process.env.E2E_USERNAME ||= 'admin'
if (!REUSE_EXISTING_SERVER) {
  // The isolated backend writes this file; reused environments should keep caller credentials authoritative.
  process.env.E2E_PASSWORD_FILE ||= path.join(E2E_ROOT, 'backend', 'e2e-password.txt')
}
process.env.MNEMONAS_E2E_ALLOW_AUTH_SKIP = ALLOW_AUTH_SKIP

assertSafeTcpPort('MNEMONAS_E2E_BACKEND_URL port', BACKEND_PORT)
assertSafeTcpPort('MNEMONAS_E2E_FRONTEND_URL port', FRONTEND_PORT)

function normalizeTcpHostForCli(label: string, value: string): string {
  const host = value.startsWith('[') && value.endsWith(']')
    ? value.slice(1, -1)
    : value

  assertSafeTcpHost(label, host)
  return host
}

function assertSafeTcpHost(label: string, value: string): void {
  const host = value.endsWith('.') ? value.slice(0, -1) : value
  if (!host || /\s/.test(host) || host.includes('[') || host.includes(']')) {
    throw new Error(`${label} is invalid: ${value}`)
  }
  if (host.includes(':')) {
    if (!/^[0-9A-Fa-f:.]+$/.test(host)) {
      throw new Error(`${label} is invalid: ${value}`)
    }
    return
  }
  if (host.length > 253) {
    throw new Error(`${label} is invalid: ${value}`)
  }
  for (const labelPart of host.split('.')) {
    if (
      !labelPart
      || labelPart.length > 63
      || labelPart.startsWith('-')
      || labelPart.endsWith('-')
      || !/^[A-Za-z0-9-]+$/.test(labelPart)
    ) {
      throw new Error(`${label} is invalid: ${value}`)
    }
  }
}

function assertSafeTcpPort(label: string, value: string): void {
  if (!/^[0-9]+$/.test(value)) {
    throw new Error(`${label} must be numeric: ${value}`)
  }
  const parsed = Number.parseInt(value, 10)
  if (!Number.isInteger(parsed) || parsed < 1 || parsed > 65535) {
    throw new Error(`${label} must be between 1 and 65535: ${value}`)
  }
}

function parseOptionalPositiveInteger(label: string, value: string | undefined): number | undefined {
  if (value === undefined || value.trim() === '') {
    return undefined
  }
  if (!/^[0-9]+$/.test(value)) {
    throw new Error(`${label} must be a positive integer: ${value}`)
  }
  const parsed = Number.parseInt(value, 10)
  if (!Number.isInteger(parsed) || parsed < 1) {
    throw new Error(`${label} must be a positive integer: ${value}`)
  }
  return parsed
}

/**
 * Playwright E2E test configuration.
 * @see https://playwright.dev/docs/test-configuration
 *
 * Authentication notes:
 * - The setup project logs in first and stores state in .auth/user.json.
 * - Chromium and mobile projects depend on setup and reuse that login state.
 * - The default run starts isolated backend/frontend test servers instead of
 *   relying on local user instances on 8080/5173.
 * - Test credentials prefer E2E_USERNAME / E2E_PASSWORD, then E2E_PASSWORD_FILE.
 * - Reused environments leave E2E_PASSWORD_FILE unset unless provided by the caller.
 */
export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: true,
  retries: process.env.CI ? 2 : 0,
  timeout: TEST_TIMEOUT_MS,
  expect: {
    timeout: EXPECT_TIMEOUT_MS,
  },
  workers: process.env.CI ? 1 : LOCAL_WORKERS,
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

  /* Run isolated backend + frontend for browser tests unless explicitly reusing existing services. */
  webServer: REUSE_EXISTING_SERVER ? undefined : [
    {
      name: 'mnemonas-e2e-backend',
      command: 'bash ./scripts/start-e2e-backend.sh',
      cwd: CONFIG_DIR,
      env: {
        ...WEB_SERVER_ENV,
        MNEMONAS_E2E_ROOT: E2E_ROOT,
        MNEMONAS_E2E_NASD_HOST: BACKEND_HOST,
        MNEMONAS_E2E_NASD_PORT: BACKEND_PORT,
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
      command: `npm run build && npm run preview -- --host ${FRONTEND_HOST} --port ${FRONTEND_PORT}`,
      cwd: CONFIG_DIR,
      env: {
        ...WEB_SERVER_ENV,
        VITE_API_PROXY_TARGET: BACKEND_URL,
      },
      url: FRONTEND_URL,
      reuseExistingServer: false,
      timeout: 120 * 1000,
    },
  ],
})
