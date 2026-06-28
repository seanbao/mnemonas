import {
  expect,
  test,
  type APIResponse,
  type Browser,
  type BrowserContext,
  type BrowserContextOptions,
  type Page,
  type TestInfo,
} from '@playwright/test'
import {
  LOGIN_BUTTON_PATTERN,
  PASSWORD_CHANGE_GATE_HEADING,
  PASSWORD_INPUT_PATTERN,
  USERNAME_INPUT_PATTERN,
  waitForAuthenticatedSurface,
  waitForAuthSurface,
} from './helpers/auth-check'

const TEMPORARY_PASSWORD = 'Temporary-pass-123!'
const REPLACEMENT_PASSWORD = 'Replacement-pass-456!'
const FINAL_PASSWORD = 'Final-pass-789!'

type CreateUserResponse = {
  success?: unknown
  data?: {
    user?: {
      id?: unknown
    }
  }
}

function isolatedUserContextOptions(testInfo: TestInfo): BrowserContextOptions {
  const projectUse = testInfo.project.use

  return {
    baseURL: typeof projectUse.baseURL === 'string'
      ? projectUse.baseURL
      : process.env.MNEMONAS_E2E_FRONTEND_URL ?? 'http://127.0.0.1:14173',
    storageState: { cookies: [], origins: [] },
    viewport: projectUse.viewport,
    userAgent: projectUse.userAgent,
    deviceScaleFactor: projectUse.deviceScaleFactor,
    isMobile: projectUse.isMobile,
    hasTouch: projectUse.hasTouch,
    locale: projectUse.locale,
  }
}

async function createUserPage(browser: Browser, testInfo: TestInfo): Promise<{
  context: BrowserContext
  page: Page
}> {
  const context = await browser.newContext(isolatedUserContextOptions(testInfo))
  return {
    context,
    page: await context.newPage(),
  }
}

async function readSuccessfulJson(response: APIResponse, action: string): Promise<unknown> {
  let body: unknown
  try {
    body = await response.json()
  } catch {
    body = await response.text()
  }

  expect(
    response.ok(),
    `${action} failed with HTTP ${response.status()}: ${JSON.stringify(body)}`,
  ).toBe(true)
  return body
}

async function submitLogin(page: Page, username: string, password: string): Promise<void> {
  await page.getByLabel(USERNAME_INPUT_PATTERN).fill(username)
  await page.getByLabel(PASSWORD_INPUT_PATTERN).fill(password)
  await page.getByRole('button', { name: LOGIN_BUTTON_PATTERN }).click()
}

test.describe('强制密码变更门禁', () => {
  test('重置后的强制改密和登录后的自助改密均应重新验证账户', async ({ browser, page }, testInfo) => {
    test.skip(
      process.env.MNEMONAS_E2E_REUSE_EXISTING === '1',
      'This case mutates a dedicated account and runs only against the default isolated backend.',
    )

    const suffix = `${testInfo.project.name}-${testInfo.workerIndex}-${Date.now().toString(36)}`
      .toLowerCase()
      .replace(/[^a-z0-9-]/g, '-')
    const username = `e2e-gate-${suffix}`
    let userID = ''
    let userContext: BrowserContext | undefined

    try {
      const createResponse = await page.request.post('/api/v1/admin/users/', {
        data: {
          username,
          password: 'Creation-pass-789!',
          role: 'user',
        },
      })
      const createBody = await readSuccessfulJson(createResponse, 'Create dedicated gate user') as CreateUserResponse
      expect(createBody.success).toBe(true)
      expect(typeof createBody.data?.user?.id).toBe('string')
      userID = createBody.data?.user?.id as string

      const resetResponse = await page.request.post(
        `/api/v1/admin/users/${encodeURIComponent(userID)}/reset-password`,
        { data: { new_password: TEMPORARY_PASSWORD } },
      )
      await readSuccessfulJson(resetResponse, 'Reset dedicated gate user password')

      const userSession = await createUserPage(browser, testInfo)
      userContext = userSession.context
      const userPage = userSession.page

      await userPage.goto('/files', { waitUntil: 'domcontentloaded' })
      await expect(userPage).toHaveURL(/\/login(?:[?#].*)?$/)
      expect(await waitForAuthSurface(userPage)).toBe('login')
      await submitLogin(userPage, username, TEMPORARY_PASSWORD)

      await userPage.waitForURL(url => !url.pathname.includes('/login'))
      expect(await waitForAuthenticatedSurface(userPage)).toBe('password-change')
      await expect(userPage.getByRole('heading', { name: PASSWORD_CHANGE_GATE_HEADING })).toBeVisible()
      await expect(userPage.getByRole('navigation', { name: '主导航', exact: true })).toHaveCount(0)
      await expect(userPage.getByRole('navigation', { name: '移动端主导航', exact: true })).toHaveCount(0)
      await expect(userPage.getByRole('button', { name: '打开导航菜单' })).toHaveCount(0)

      await userPage.getByLabel('当前密码', { exact: true }).fill(TEMPORARY_PASSWORD)
      await userPage.getByLabel('新密码', { exact: true }).fill(REPLACEMENT_PASSWORD)
      await userPage.getByLabel('确认新密码', { exact: true }).fill(REPLACEMENT_PASSWORD)
      await userPage.getByRole('button', { name: '修改密码并重新登录' }).click()

      await expect(userPage).toHaveURL(/\/login(?:[?#].*)?$/)
      await submitLogin(userPage, username, REPLACEMENT_PASSWORD)
      await userPage.waitForURL(url => !url.pathname.includes('/login'))
      expect(await waitForAuthenticatedSurface(userPage)).toBe('app')
      await expect(userPage.getByRole('heading', { name: PASSWORD_CHANGE_GATE_HEADING })).toHaveCount(0)

      await userPage.getByRole('button', { name: '打开用户菜单' }).click()
      await expect(userPage.getByText('账户安全', { exact: true })).toBeVisible()
      await userPage.getByText('账户安全', { exact: true }).click()
      await expect(userPage).toHaveURL(/\/account\/security(?:[?#].*)?$/)
      await expect(userPage.getByRole('menu', { name: '用户菜单' })).toBeHidden()
      await expect(userPage.getByRole('heading', { name: '账户安全' })).toBeVisible()
      await expect(userPage.getByText(/此账户在所有设备上的登录都会退出/)).toBeVisible()

      await userPage.getByLabel('当前密码', { exact: true }).fill(REPLACEMENT_PASSWORD)
      await expect(userPage.getByLabel('当前密码', { exact: true })).toHaveValue(REPLACEMENT_PASSWORD)
      await userPage.getByLabel('新密码', { exact: true }).fill(FINAL_PASSWORD)
      await expect(userPage.getByLabel('当前密码', { exact: true })).toHaveValue(REPLACEMENT_PASSWORD)
      await expect(userPage.getByLabel('新密码', { exact: true })).toHaveValue(FINAL_PASSWORD)
      await userPage.getByLabel('确认新密码', { exact: true }).fill(FINAL_PASSWORD)
      await expect(userPage.getByLabel('当前密码', { exact: true })).toHaveValue(REPLACEMENT_PASSWORD)
      await expect(userPage.getByLabel('新密码', { exact: true })).toHaveValue(FINAL_PASSWORD)
      await expect(userPage.getByLabel('确认新密码', { exact: true })).toHaveValue(FINAL_PASSWORD)
      await userPage.getByRole('button', { name: '修改密码并重新登录' }).click()

      await expect(userPage).toHaveURL(/\/login(?:[?#].*)?$/)
      await submitLogin(userPage, username, FINAL_PASSWORD)
      await userPage.waitForURL(url => url.pathname === '/')
      expect(await waitForAuthenticatedSurface(userPage)).toBe('app')
    } finally {
      await userContext?.close()
      if (userID) {
        const deleteResponse = await page.request.delete(`/api/v1/admin/users/${encodeURIComponent(userID)}`)
        await readSuccessfulJson(deleteResponse, 'Delete dedicated gate user')
      }
    }
  })
})
