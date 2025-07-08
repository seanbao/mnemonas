import { expect, test, type Locator, type Page } from '@playwright/test'
import { resolveE2ECredentials } from './credentials'

export const LOGIN_BUTTON_PATTERN = /^(登录|sign in|login)$/i
export const USERNAME_INPUT_PATTERN = /^用户名$/i
export const PASSWORD_INPUT_PATTERN = /^密码$/i

type AuthSurface = 'app' | 'login' | 'loading'

async function isVisible(locator: Locator): Promise<boolean> {
  return locator.isVisible().catch(() => false)
}

export function isAuthSkipAllowed(): boolean {
  const value = process.env.MNEMONAS_E2E_ALLOW_AUTH_SKIP?.trim().toLowerCase()
  return value === '1' || value === 'true' || value === 'yes'
}

function skipOrFail(message: string): never {
  if (isAuthSkipAllowed()) {
    test.skip(true, `Skipped: ${message}`)
  }
  throw new Error(`${message}. Set MNEMONAS_E2E_ALLOW_AUTH_SKIP=1 only when intentionally reusing an environment where protected-page checks may be skipped.`)
}

export async function waitForAuthSurface(page: Page, timeout = 10_000): Promise<Exclude<AuthSurface, 'loading'>> {
  const desktopNavigation = page.getByRole('navigation', { name: '主导航' })
  const mobileNavigation = page.getByRole('navigation', { name: '移动端主导航' })
  const mobileMenuButton = page.getByRole('button', { name: '打开导航菜单' })
  const usernameInput = page.getByLabel(USERNAME_INPUT_PATTERN)
  const loginButton = page.getByRole('button', { name: LOGIN_BUTTON_PATTERN })
  let observedSurface: AuthSurface = 'loading'

  await expect.poll(async () => {
    if (
      await isVisible(desktopNavigation)
      || await isVisible(mobileNavigation)
      || await isVisible(mobileMenuButton)
    ) {
      observedSurface = 'app'
      return observedSurface
    }
    if (await isVisible(usernameInput) && await isVisible(loginButton)) {
      observedSurface = 'login'
      return observedSurface
    }
    observedSurface = 'loading'
    return observedSurface
  }, { timeout }).not.toBe('loading')

  return observedSurface as Exclude<AuthSurface, 'loading'>
}

export async function waitForAppReady(page: Page): Promise<void> {
  // Vite HMR and app-level polling keep background requests alive, so networkidle is not a stable readiness signal.
  await page.waitForLoadState('domcontentloaded')
  await page.locator('body').waitFor({ state: 'visible' })

  const routeFallback = page.getByText('加载中...')
  await routeFallback.waitFor({ state: 'hidden', timeout: 10_000 }).catch(() => {})

  await waitForAuthSurface(page)
}

/**
 * Check whether the page has been redirected to the login route.
 * Returns true when authentication is required.
 */
export async function isRedirectedToLogin(page: Page): Promise<boolean> {
  return page.url().includes('/login')
}

/**
 * Wait for authentication state to settle and sign in when required.
 *
 * storageState tokens can expire, so this helper retries login when the
 * protected route redirects to the login page.
 *
 * @param page - Playwright page object.
 * @param targetPath - Expected route after login.
 */
export async function skipIfAuthRequired(page: Page, targetPath?: string): Promise<void> {
  const surface = await waitForAuthSurface(page).catch(error => {
    if (page.url().includes('/login')) {
      return 'login'
    }
    throw error
  })

  if (surface !== 'login' && !page.url().includes('/login')) {
    return
  }

  const { username, password } = resolveE2ECredentials()

  const loginButton = page.getByRole('button', { name: LOGIN_BUTTON_PATTERN })
  const isLoginPage = await loginButton.isVisible({ timeout: 3000 }).catch(() => false)

  if (!isLoginPage) {
    skipOrFail('Login form not found while authentication is required')
  }

  if (!password) {
    skipOrFail('No E2E password configured or initial password file found')
  }

  const usernameInput = page.getByLabel(USERNAME_INPUT_PATTERN)
  const passwordInput = page.getByLabel(PASSWORD_INPUT_PATTERN)

  if (!await usernameInput.isVisible({ timeout: 2000 }).catch(() => false)) {
    skipOrFail('Login form not found')
  }

  await usernameInput.fill(username)
  await passwordInput.fill(password)
  await loginButton.click()

  try {
    await page.waitForURL(url => !url.pathname.includes('/login'), {
      timeout: 10000,
    })

    if (targetPath && !page.url().includes(targetPath)) {
      await page.goto(targetPath, { waitUntil: 'domcontentloaded' })
      await waitForAppReady(page)
    }
  } catch {
    skipOrFail('Auto-login failed (invalid credentials, rate limit, or backend error)')
  }
}

/**
 * Navigate to the target page and complete authentication if required.
 */
export async function ensureAuthenticatedAt(page: Page, path: string): Promise<void> {
  await page.goto(path, { waitUntil: 'domcontentloaded' })
  await waitForAppReady(page)
  await skipIfAuthRequired(page, path)
}

/**
 * Assert that the current page is not the login route.
 */
export async function assertNotOnLoginPage(page: Page): Promise<void> {
  await expect(page).not.toHaveURL(/\/login/)
}
