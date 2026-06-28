import { expect, test, type Locator, type Page } from '@playwright/test'
import { resolveE2ECredentials } from './credentials'
import { waitForRouteSettled } from './route-ready'

export const LOGIN_BUTTON_PATTERN = /^(登录|sign in|login)$/i
export const USERNAME_INPUT_PATTERN = /^用户名$/i
export const PASSWORD_INPUT_PATTERN = /^密码$/i
export const PASSWORD_CHANGE_GATE_HEADING = '必须修改密码'
const AUTH_SURFACE_TIMEOUT_MS = 20_000

export type AuthSurface = 'app' | 'login' | 'password-change' | 'loading'

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

export async function waitForAuthSurface(page: Page, timeout = AUTH_SURFACE_TIMEOUT_MS): Promise<Exclude<AuthSurface, 'loading'>> {
  const desktopNavigation = page.getByRole('navigation', { name: '主导航', exact: true })
  const mobileNavigation = page.getByRole('navigation', { name: '移动端主导航', exact: true })
  const mobileMenuButton = page.getByRole('button', { name: '打开导航菜单' })
  const usernameInput = page.getByLabel(USERNAME_INPUT_PATTERN)
  const loginButton = page.getByRole('button', { name: LOGIN_BUTTON_PATTERN })
  const passwordChangeHeading = page.getByRole('heading', { name: PASSWORD_CHANGE_GATE_HEADING })
  let observedSurface: AuthSurface = 'loading'

  await expect.poll(async () => {
    if (await isVisible(passwordChangeHeading)) {
      observedSurface = 'password-change'
      return observedSurface
    }
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

export async function waitForAuthenticatedSurface(
  page: Page,
  timeout = 10_000,
): Promise<Exclude<AuthSurface, 'login' | 'loading'>> {
  let surface: AuthSurface = 'loading'
  await expect.poll(async () => {
    surface = await waitForAuthSurface(page, 1000).catch(() => 'loading')
    return surface === 'app' || surface === 'password-change'
  }, { timeout }).toBe(true)

  return surface as Exclude<AuthSurface, 'login' | 'loading'>
}

export async function waitForAppReady(page: Page, route?: string): Promise<Exclude<AuthSurface, 'loading'>> {
  // Vite HMR and app-level polling keep background requests alive, so networkidle is not a stable readiness signal.
  await page.waitForLoadState('domcontentloaded')
  await page.locator('body').waitFor({ state: 'visible' })

  const routeFallback = page.getByText('加载中…')
  await routeFallback.waitFor({ state: 'hidden', timeout: 10_000 }).catch(() => {})

  const surface = await waitForAuthSurface(page)
  if (route) {
    await waitForRouteSettled(page, route)
  }
  return surface
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

  if (surface === 'password-change') {
    throw new Error(
      'Password change is required before protected-page tests can continue. '
      + 'The authentication setup must complete the password-change gate or fail explicitly.',
    )
  }

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

  let postLoginSurface: Exclude<AuthSurface, 'loading'>
  try {
    await page.waitForURL(url => !url.pathname.includes('/login'), {
      timeout: 10000,
    })

    if (targetPath && !page.url().includes(targetPath)) {
      await page.goto(targetPath, { waitUntil: 'domcontentloaded' })
    }
    postLoginSurface = await waitForAppReady(page, targetPath)
    if (postLoginSurface === 'login') {
      postLoginSurface = await waitForAuthenticatedSurface(page)
    }
  } catch {
    skipOrFail('Auto-login failed (invalid credentials, rate limit, or backend error)')
  }

  if (postLoginSurface === 'password-change') {
    throw new Error(
      'Auto-login reached the required password-change gate. '
      + 'Run the authentication setup with E2E_PASSWORD_CHANGE_TO before protected-page tests.',
    )
  }
}

/**
 * Navigate to the target page and complete authentication if required.
 */
export async function ensureAuthenticatedAt(page: Page, path: string): Promise<void> {
  await page.goto(path, { waitUntil: 'domcontentloaded' })
  await waitForAppReady(page, path)
  await skipIfAuthRequired(page, path)
}

/**
 * Assert that the current page is not the login route.
 */
export async function assertNotOnLoginPage(page: Page): Promise<void> {
  await expect(page).not.toHaveURL(/\/login/)
}
