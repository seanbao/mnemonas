import { test, expect } from '@playwright/test'
import { resolveE2ECredentials } from './helpers/credentials'

test.use({
  storageState: { cookies: [], origins: [] },
})

test.describe('登录页面', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/login')
  })

  test('应显示登录表单', async ({ page }) => {
    await expect(page.getByRole('heading', { name: '欢迎回来' })).toBeVisible()
    await expect(page.getByPlaceholder('请输入用户名')).toBeVisible()
    await expect(page.getByPlaceholder('请输入密码')).toBeVisible()
    await expect(page.getByRole('button', { name: /登录/i })).toBeVisible()
  })

  test('应显示产品品牌信息', async ({ page }) => {
    await expect(page.getByRole('heading', { name: 'MnemoNAS', level: 1 })).toBeVisible()
  })

  test('空表单提交应显示错误或阻止提交', async ({ page }) => {
    await page.getByRole('button', { name: /登录/i }).click()
    // 应停留在登录页
    await expect(page).toHaveURL(/\/login/)
  })

  test('错误密码应显示错误提示', async ({ page }) => {
    await page.getByPlaceholder('请输入用户名').fill(`invalid-user-${Date.now()}`)
    await page.getByPlaceholder('请输入密码').fill('wrongpassword')
    await page.getByRole('button', { name: /登录/i }).click()

    const alert = page.getByRole('alert')
    await expect(alert).toBeVisible({ timeout: 5000 })
    await expect(alert).toContainText(/.+/)
    await expect(page).toHaveURL(/\/login/)
  })

  test('正确凭据应登录成功', async ({ page }) => {
    const { username: e2eUsername, password: e2ePassword } = resolveE2ECredentials()
    test.skip(!e2ePassword, 'Skipped: no E2E password configured or discoverable')

    await page.getByPlaceholder('请输入用户名').fill(e2eUsername)
    await page.getByPlaceholder('请输入密码').fill(e2ePassword)
    await page.getByRole('button', { name: /登录/i }).click()
    
    // 成功登录应跳转到主页或仪表板
    await expect(page).not.toHaveURL(/\/login/, { timeout: 5000 })
  })

  test('正确凭据应建立 HttpOnly cookie 会话和下载会话', async ({ page, context }) => {
    const { username: e2eUsername, password: e2ePassword } = resolveE2ECredentials()
    test.skip(!e2ePassword, 'Skipped: no E2E password configured or discoverable')

    await page.getByPlaceholder('请输入用户名').fill(e2eUsername)
    await page.getByPlaceholder('请输入密码').fill(e2ePassword)
    await page.getByRole('button', { name: /登录/i }).click()

    await expect(page).not.toHaveURL(/\/login/, { timeout: 5000 })
    const cookies = await context.cookies()
    const accessCookie = cookies.find((cookie) => cookie.name === 'mnemonas_access')
    const refreshCookie = cookies.find((cookie) => cookie.name === 'mnemonas_refresh')
    const downloadCookie = cookies.find((cookie) => cookie.name === 'mnemonas_download_access')

    expect(accessCookie).toMatchObject({ httpOnly: true, path: '/api/v1' })
    expect(refreshCookie).toMatchObject({ httpOnly: true, path: '/api/v1/auth/refresh' })
    expect(downloadCookie).toMatchObject({ httpOnly: true, path: '/api/v1' })
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
    
    await expect(page.getByRole('button', { name: /登录/i })).toBeVisible()
    await expect(page).toHaveScreenshot('login-mobile.png', {
      maxDiffPixelRatio: 0.05,
    })
  })
})
