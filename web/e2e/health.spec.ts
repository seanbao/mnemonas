import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

test.describe('健康检查页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/system-health')
  })

  test('应显示系统健康页面', async ({ page }) => {
    await expect(page).toHaveURL(/\/system-health/)
    await expect(page.getByRole('heading', { name: '系统健康' })).toBeVisible()
  })

  test('刷新按钮应可见', async ({ page }) => {
    await expect(page.getByRole('button', { name: '刷新', exact: true })).toBeVisible()
  })
})
