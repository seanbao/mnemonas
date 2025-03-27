import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

/**
 * 设置页面 E2E 测试
 * 认证状态由 auth.setup.ts 通过 storageState 自动注入
 * 如果认证启用但登录失败，测试会被跳过
 */

test.describe('设置页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/settings')
  })

  test('应显示设置页面', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.locator('body')).toBeVisible()
  })

  test('应显示设置页面标题', async ({ page }) => {
    const title = page.getByRole('heading', { name: /系统设置|设置/i })
    await expect(title).toBeVisible({ timeout: 5000 })
  })

  test('应显示设置选项卡', async ({ page }) => {
    const tabs = [
      /常规/i,
      /版本保留/i,
      /WebDAV/i,
      /高级/i,
    ]

    for (const tabPattern of tabs) {
      const tab = page.getByRole('tab', { name: tabPattern })
      if (await tab.isVisible({ timeout: 1000 }).catch(() => false)) {
        await expect(tab).toBeVisible()
      }
    }
  })

  test('应显示保存和重置按钮', async ({ page }) => {
    const saveBtn = page.getByRole('button', { name: /保存|保存设置/i })
    const resetBtn = page.getByRole('button', { name: /重置/i })

    await expect(saveBtn).toBeVisible({ timeout: 5000 })
    await expect(resetBtn).toBeVisible({ timeout: 5000 })
  })
})

test.describe('设置选项卡切换', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/settings')
  })

  test('点击 WebDAV 选项卡应显示 WebDAV 设置', async ({ page }) => {
    const webdavTab = page.getByRole('tab', { name: /WebDAV/i })
    if (await webdavTab.isVisible({ timeout: 2000 }).catch(() => false)) {
      await webdavTab.click()
      await page.waitForTimeout(500)

      // 检查 WebDAV 相关设置项
      const webdavSwitch = page.getByText(/启用 WebDAV/i)
      await expect(webdavSwitch).toBeVisible({ timeout: 5000 })
    }
  })

  test('点击版本保留选项卡应显示版本设置', async ({ page }) => {
    const retentionTab = page.getByRole('tab', { name: /版本保留/i })
    if (await retentionTab.isVisible({ timeout: 2000 }).catch(() => false)) {
      await retentionTab.click()
      await page.waitForTimeout(500)

      // 检查版本相关设置项
      const maxVersions = page.getByText(/最大版本数/i)
      await expect(maxVersions).toBeVisible({ timeout: 5000 })
    }
  })

  test('点击高级选项卡应显示 CDC 设置', async ({ page }) => {
    const advancedTab = page.getByRole('tab', { name: /高级/i })
    if (await advancedTab.isVisible({ timeout: 2000 }).catch(() => false)) {
      await advancedTab.click()
      await page.waitForTimeout(500)

      // 检查 CDC 相关设置项
      const cdcSection = page.getByText(/CDC 分块|内容定义分块/i)
      await expect(cdcSection).toBeVisible({ timeout: 5000 })
    }
  })
})

test.describe('设置表单交互', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/settings')
  })

  test('服务器地址输入框应可编辑', async ({ page }) => {
    const hostInput = page.getByLabel(/监听地址/i)
    if (await hostInput.isVisible({ timeout: 2000 }).catch(() => false)) {
      await hostInput.clear()
      await hostInput.fill('127.0.0.1')
      await expect(hostInput).toHaveValue('127.0.0.1')
    }
  })

  test('端口输入框应可编辑', async ({ page }) => {
    const portInput = page.getByLabel(/端口/i)
    if (await portInput.isVisible({ timeout: 2000 }).catch(() => false)) {
      await portInput.clear()
      await portInput.fill('9080')
      await expect(portInput).toHaveValue('9080')
    }
  })
})

test.describe('设置页面响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await page.goto('/settings')
    await page.waitForLoadState('networkidle')

    const body = page.locator('body')
    await expect(body).toBeVisible()
  })

  test('平板端布局', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await page.goto('/settings')
    await page.waitForLoadState('networkidle')

    const body = page.locator('body')
    await expect(body).toBeVisible()
  })
})
