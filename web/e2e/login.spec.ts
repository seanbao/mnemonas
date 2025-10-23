import { test, expect } from '@playwright/test'
import { isAuthSkipAllowed } from './helpers/auth-check'
import { resolveE2ECredentials } from './helpers/credentials'

test.use({
  storageState: { cookies: [], origins: [] },
})

function requireLoginCredentials(): { username: string; password: string } {
  const { username, password } = resolveE2ECredentials()
  if (password) {
    return { username, password }
  }

  const message = 'No E2E password configured or discoverable'
  if (isAuthSkipAllowed()) {
    test.skip(true, `Skipped: ${message}`)
  }

  throw new Error(`${message}. Default isolated Playwright runs must generate an E2E password file.`)
}

test.describe('登录页面', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/login')
    const usernameInput = page.getByLabel('用户名', { exact: true })
    if (await usernameInput.isVisible({ timeout: 2000 }).catch(() => false)) {
      await usernameInput.fill('visual-clean')
      await usernameInput.clear()
      await usernameInput.evaluate((element) => {
        if (element instanceof HTMLElement) element.blur()
      })
    }
  })

  test('应显示登录表单', async ({ page }) => {
    await expect(page.getByRole('heading', { name: '欢迎回来' })).toBeVisible()
    await expect(page.getByLabel('用户名', { exact: true })).toBeVisible()
    await expect(page.getByLabel('密码', { exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: /登录/i })).toBeVisible()
  })

  test('应显示产品品牌信息', async ({ page }) => {
    await expect(page.getByRole('heading', { name: 'MnemoNAS', level: 1 })).toBeVisible()
  })

  test('空表单提交应显示错误或阻止提交', async ({ page }) => {
    await page.getByRole('button', { name: /登录/i }).click()
    // The page should remain on the login route.
    await expect(page).toHaveURL(/\/login/)
  })

  test('错误密码应显示错误提示', async ({ page }) => {
    await page.getByLabel('用户名', { exact: true }).fill(`invalid-user-${Date.now()}`)
    await page.getByLabel('密码', { exact: true }).fill('wrongpassword')
    await page.getByRole('button', { name: /登录/i }).click()

    const alert = page.getByRole('alert')
    await expect(alert).toBeVisible({ timeout: 5000 })
    await expect(alert).toContainText(/.+/)
    await expect(page).toHaveURL(/\/login/)
  })

  test('正确凭据应登录成功', async ({ page }) => {
    const { username: e2eUsername, password: e2ePassword } = requireLoginCredentials()

    await page.getByLabel('用户名', { exact: true }).fill(e2eUsername)
    await page.getByLabel('密码', { exact: true }).fill(e2ePassword)
    await page.getByRole('button', { name: /登录/i }).click()
    
    // Successful login should redirect to the home/dashboard route.
    await expect(page).not.toHaveURL(/\/login/, { timeout: 5000 })
  })

  test('正确凭据应建立 HttpOnly cookie 会话和下载会话', async ({ page, context }) => {
    const { username: e2eUsername, password: e2ePassword } = requireLoginCredentials()

    await page.getByLabel('用户名', { exact: true }).fill(e2eUsername)
    await page.getByLabel('密码', { exact: true }).fill(e2ePassword)
    await page.getByRole('button', { name: /登录/i }).click()

    await expect(page).not.toHaveURL(/\/login/, { timeout: 5000 })
    const cookies = await context.cookies()
    const accessCookie = cookies.find((cookie) => cookie.name === 'mnemonas_access')
    const refreshCookie = cookies.find((cookie) => cookie.name === 'mnemonas_refresh')
    const downloadCookie = cookies.find((cookie) => cookie.name === 'mnemonas_download_access')

    expect(accessCookie).toMatchObject({ httpOnly: true, path: '/api/v1' })
    expect(refreshCookie).toMatchObject({ httpOnly: true, path: '/api/v1/auth/refresh' })
    expect(downloadCookie).toMatchObject({ httpOnly: true, path: '/api/v1', sameSite: 'Strict' })
  })

  test('视觉回归 - 登录页截图', async ({ page }) => {
    await expect(page).toHaveScreenshot('login-page.png', {
      maxDiffPixelRatio: 0.05,
    })
  })
})

test.describe('登录页响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await page.goto('/login')
    const usernameInput = page.getByLabel('用户名', { exact: true })
    if (await usernameInput.isVisible({ timeout: 2000 }).catch(() => false)) {
      await usernameInput.fill('visual-clean')
      await usernameInput.clear()
      await usernameInput.evaluate((element) => {
        if (element instanceof HTMLElement) element.blur()
      })
    }
    
    await expect(page.getByRole('button', { name: /登录/i })).toBeVisible()
    await expect(page).toHaveScreenshot('login-mobile.png', {
      maxDiffPixelRatio: 0.05,
    })
  })
})
