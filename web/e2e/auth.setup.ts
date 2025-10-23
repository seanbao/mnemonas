import { test as setup, type Page } from '@playwright/test'
import { resolveE2ECredentials } from './helpers/credentials'
import {
  isAuthSkipAllowed,
  LOGIN_BUTTON_PATTERN,
  PASSWORD_INPUT_PATTERN,
  USERNAME_INPUT_PATTERN,
  waitForAuthSurface,
} from './helpers/auth-check'

const STORAGE_STATE_PATH = './e2e/.auth/user.json'

async function saveEmptyAuthStateOrFail(page: Page, message: string): Promise<void> {
  if (!isAuthSkipAllowed()) {
    throw new Error(`${message}. Set MNEMONAS_E2E_ALLOW_AUTH_SKIP=1 only when intentionally reusing an environment where protected-page checks may be skipped.`)
  }
  console.log(`${message}, saving empty auth state`)
  await page.context().storageState({ path: STORAGE_STATE_PATH })
}

/**
 * Playwright authentication setup.
 * Runs before browser projects and stores the authentication state for reuse.
 *
 * Test account configuration:
 * - E2E_USERNAME: test username, defaults to admin.
 * - E2E_PASSWORD: explicit test password.
 * - E2E_PASSWORD_FILE: initial-password file used when E2E_PASSWORD is unset.
 *
 * Authentication strategy:
 * 1. Visit /files first. If it does not redirect to /login, auth is disabled or already valid.
 * 2. If redirected to /login, sign in with the test account.
 * 3. If login fails, fail by default; allow empty-state fallback only when auth skipping is explicitly enabled.
 */
setup('authenticate', async ({ page }) => {
  const credentials = resolveE2ECredentials()
  const { username, password, passwordSource } = credentials

  await page.goto('/files', { waitUntil: 'domcontentloaded' })
  await page.locator('body').waitFor({ state: 'visible' })
  const initialSurface = await waitForAuthSurface(page).catch(() => page.url().includes('/login') ? 'login' : 'unknown')

  if (initialSurface !== 'login' && !page.url().includes('/login')) {
    if (initialSurface === 'unknown') {
      await saveEmptyAuthStateOrFail(page, 'Authentication surface did not settle')
      return
    }
    console.log('Authentication disabled or already logged in, skipping login')
    await page.context().storageState({ path: STORAGE_STATE_PATH })
    return
  }

  if (!password) {
    await saveEmptyAuthStateOrFail(page, 'No E2E password configured or discoverable')
    return
  }

  console.log(`Authentication required, attempting login with ${passwordSource} password...`)

  const loginButton = page.getByRole('button', { name: LOGIN_BUTTON_PATTERN })
  const isLoginPage = await loginButton.isVisible({ timeout: 3000 }).catch(() => false)

  if (!isLoginPage) {
    await saveEmptyAuthStateOrFail(page, 'No login form found')
    return
  }

  const usernameInput = page.getByLabel(USERNAME_INPUT_PATTERN)
  const passwordInput = page.getByLabel(PASSWORD_INPUT_PATTERN)

  await usernameInput.fill(username)
  await passwordInput.fill(password)

  await loginButton.click()

  try {
    await page.waitForURL(url => !url.pathname.includes('/login'), {
      timeout: 10000,
    })
    console.log(`Authenticated as ${username}`)
  } catch {
    const errorAlert = page.getByRole('alert')
    const hasError = await errorAlert.isVisible({ timeout: 1000 }).catch(() => false)

    if (hasError) {
      await saveEmptyAuthStateOrFail(page, 'Login failed (invalid credentials, rate limit, or backend error)')
      return
    }
    await saveEmptyAuthStateOrFail(page, 'Login timeout (backend may not be running)')
    return
  }

  await page.context().storageState({ path: STORAGE_STATE_PATH })
})
