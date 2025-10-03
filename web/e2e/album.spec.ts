import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

/**
 * 相册页面 E2E 测试
 * 认证状态由 auth.setup.ts 通过 storageState 自动注入
 * 如果认证启用但登录失败，测试会被跳过
 */

test.describe('相册页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/album')
  })

  test('应显示相册页面', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.getByRole('heading', { name: '相册', exact: true })).toBeVisible({ timeout: 5000 })
  })

  test('应显示相册内容或空状态', async ({ page }) => {
    await expect(page.getByText(/共 \d+ 张图片/)).toBeVisible({ timeout: 5000 })
  })

  test('有图片时应可打开预览，空相册时应保持空状态提示', async ({ page }) => {
    await expect(page.getByText(/共 \d+ 张图片/)).toBeVisible({ timeout: 5000 })

    const emptyStateHeading = page.getByRole('heading', { name: '暂无图片', exact: true })
    const thumbnails = page.locator('main img[alt]')

    if (await thumbnails.first().isVisible({ timeout: 1000 }).catch(() => false)) {
      await thumbnails.first().click({ force: true })
      await expect(page.getByRole('button', { name: '关闭预览' })).toBeVisible({ timeout: 5000 })
      return
    }

    await expect(emptyStateHeading).toBeVisible({ timeout: 5000 })
  })
})

test.describe('相册页面响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/album')

    await expect(page.getByRole('heading', { name: '相册', exact: true })).toBeVisible({ timeout: 5000 })
  })
})
