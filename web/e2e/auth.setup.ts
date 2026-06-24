import { expect, test as setup, type Page } from '@playwright/test'
import { resolveE2ECredentials } from './helpers/credentials'
import {
  isAuthSkipAllowed,
  LOGIN_BUTTON_PATTERN,
  PASSWORD_INPUT_PATTERN,
  PASSWORD_CHANGE_GATE_HEADING,
  USERNAME_INPUT_PATTERN,
  type AuthSurface,
  waitForAuthenticatedSurface,
  waitForAuthSurface,
} from './helpers/auth-check'

const STORAGE_STATE_PATH = './e2e/.auth/user.json'
const PASSWORD_CHANGE_TARGET_ENV = 'E2E_PASSWORD_CHANGE_TO'
const MIN_PASSWORD_BYTES = 8
const MAX_PASSWORD_BYTES = 72

async function saveEmptyAuthStateOrFail(page: Page, message: string): Promise<void> {
  if (!isAuthSkipAllowed()) {
    throw new Error(`${message}. Set MNEMONAS_E2E_ALLOW_AUTH_SKIP=1 only when intentionally reusing an environment where protected-page checks may be skipped.`)
  }
  console.log(`${message}, saving empty auth state`)
  await page.context().storageState({ path: STORAGE_STATE_PATH })
}

function requiredPasswordChangeTarget(currentPassword: string): string {
  const replacement = process.env[PASSWORD_CHANGE_TARGET_ENV]
  if (!replacement) {
    throw new Error(
      `The account requires a password change. Set ${PASSWORD_CHANGE_TARGET_ENV} to an explicit replacement password; `
      + 'this state is never treated as an authentication skip.',
    )
  }

  const byteLength = new TextEncoder().encode(replacement).length
  if (byteLength < MIN_PASSWORD_BYTES || byteLength > MAX_PASSWORD_BYTES) {
    throw new Error(
      `${PASSWORD_CHANGE_TARGET_ENV} must contain ${MIN_PASSWORD_BYTES} through ${MAX_PASSWORD_BYTES} UTF-8 bytes.`,
    )
  }
  if (replacement === currentPassword) {
    throw new Error(`${PASSWORD_CHANGE_TARGET_ENV} must differ from the current E2E password.`)
  }

  return replacement
}

async function submitLogin(page: Page, username: string, password: string): Promise<void> {
  const loginButton = page.getByRole('button', { name: LOGIN_BUTTON_PATTERN })
  const usernameInput = page.getByLabel(USERNAME_INPUT_PATTERN)
  const passwordInput = page.getByLabel(PASSWORD_INPUT_PATTERN)

  await expect(loginButton).toBeVisible({ timeout: 3000 })
  await expect(usernameInput).toBeVisible({ timeout: 3000 })
  await usernameInput.fill(username)
  await passwordInput.fill(password)
  await loginButton.click()
}

async function completeRequiredPasswordChange(
  page: Page,
  username: string,
  currentPassword: string | null,
): Promise<void> {
  if (!currentPassword) {
    throw new Error(
      'The account requires a password change, but no current E2E password is available. '
      + 'Provide E2E_PASSWORD or E2E_PASSWORD_FILE.',
    )
  }

  const replacement = requiredPasswordChangeTarget(currentPassword)
  await expect(page.getByRole('heading', { name: PASSWORD_CHANGE_GATE_HEADING })).toBeVisible()
  await page.getByLabel('当前密码', { exact: true }).fill(currentPassword)
  await page.getByLabel('新密码', { exact: true }).fill(replacement)
  await page.getByLabel('确认新密码', { exact: true }).fill(replacement)
  await page.getByRole('button', { name: '修改密码并重新登录' }).click()

  await expect(page).toHaveURL(/\/login(?:[?#].*)?$/, { timeout: 10_000 })
  await submitLogin(page, username, replacement)
  const surface = await waitForAuthenticatedSurface(page)
  if (surface !== 'app') {
    throw new Error('The replacement password was accepted, but the required password-change gate remained active.')
  }
}

/**
 * Playwright authentication setup.
 * Runs before browser projects and stores the authentication state for reuse.
 *
 * Test account configuration:
 * - E2E_USERNAME: test username, defaults to admin.
 * - E2E_PASSWORD: explicit test password.
 * - E2E_PASSWORD_FILE: initial-password file used when E2E_PASSWORD is unset.
 * - E2E_PASSWORD_CHANGE_TO: explicit replacement used only when the account requires a password change.
 *
 * Authentication strategy:
 * 1. Visit /files first. If it does not redirect to /login, auth is disabled or already valid.
 * 2. If redirected to /login, sign in with the test account.
 * 3. If the account requires a password change, require an explicit replacement, complete the gate, and sign in again.
 * 4. If login fails, fail by default; allow empty-state fallback only when auth skipping is explicitly enabled.
 */
setup('authenticate', async ({ page }) => {
  const credentials = resolveE2ECredentials()
  const { username, password, passwordSource } = credentials

  await page.goto('/files', { waitUntil: 'domcontentloaded' })
  await page.locator('body').waitFor({ state: 'visible' })
  const initialSurface = await waitForAuthSurface(page).catch(() => page.url().includes('/login') ? 'login' : 'unknown')

  if (initialSurface === 'password-change') {
    await completeRequiredPasswordChange(page, username, password)
    console.log(`Completed required password change and authenticated as ${username}`)
    await page.context().storageState({ path: STORAGE_STATE_PATH })
    return
  }

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

  await submitLogin(page, username, password)

  let authenticatedSurface: Exclude<AuthSurface, 'login' | 'loading'>
  try {
    authenticatedSurface = await waitForAuthenticatedSurface(page)
  } catch (error) {
    if (!page.url().includes('/login')) {
      const detail = error instanceof Error ? error.message : String(error)
      throw new Error(`Login left the login route, but the authenticated surface did not settle: ${detail}`)
    }

    const errorAlert = page.getByRole('alert')
    const hasError = await errorAlert.isVisible({ timeout: 1000 }).catch(() => false)

    if (hasError) {
      await saveEmptyAuthStateOrFail(page, 'Login failed (invalid credentials, rate limit, or backend error)')
      return
    }
    await saveEmptyAuthStateOrFail(page, 'Login timeout (backend may not be running)')
    return
  }

  if (authenticatedSurface === 'password-change') {
    await completeRequiredPasswordChange(page, username, password)
    console.log(`Completed required password change and authenticated as ${username}`)
  } else {
    console.log(`Authenticated as ${username}`)
  }

  await page.context().storageState({ path: STORAGE_STATE_PATH })
})
