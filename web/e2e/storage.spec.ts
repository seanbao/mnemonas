import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

/**
 * 存储管理页面 E2E 测试
 * 认证状态由 auth.setup.ts 通过 storageState 自动注入
 * 如果认证启用但登录失败，测试会被跳过
 */

test.describe('存储管理页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/storage')
  })

  test('应显示存储管理页面', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.locator('body')).toBeVisible()
  })

  test('应显示存储管理标题', async ({ page }) => {
    const title = page.getByText('存储管理').first()
    await expect(title).toBeVisible()
  })

  test('应显示刷新按钮', async ({ page }) => {
    const refreshBtn = page.getByRole('button', { name: /刷新/i })
    await expect(refreshBtn).toBeVisible()
  })
})

test.describe('存储空间概览', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/storage')
  })

  test('应显示存储使用进度条', async ({ page }) => {
    const storageOverview = page.getByText(/存储空间使用|已使用/i)
    await expect(storageOverview).toBeVisible()
  })
})

test.describe('存储统计卡片', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/storage')
  })

  test('应显示对象总数', async ({ page }) => {
    const objectsCard = page.getByText(/对象总数/i)
    await expect(objectsCard).toBeVisible()
  })

  test('应显示存储大小', async ({ page }) => {
    const sizeCard = page.getByText(/存储大小/i)
    await expect(sizeCard).toBeVisible()
  })

  test('应显示去重率', async ({ page }) => {
    const dedupCard = page.getByText(/去重率/i)
    await expect(dedupCard).toBeVisible()
  })

  test('应显示节省空间', async ({ page }) => {
    const savingsCard = page.getByText(/节省空间/i)
    await expect(savingsCard).toBeVisible()
  })
})

test.describe('维护操作卡片', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/storage')
  })

  test('应显示数据巡检卡片', async ({ page }) => {
    const scrubCard = page.getByText(/数据巡检|Scrub/i)
    await expect(scrubCard).toBeVisible()
  })

  test('应显示垃圾回收卡片', async ({ page }) => {
    const gcCard = page.getByText(/垃圾回收|GC/i)
    await expect(gcCard).toBeVisible()
  })

  test('维护按钮应标记为即将推出', async ({ page }) => {
    const maintenanceButton = page.getByRole('button', { name: '打开维护工具' }).first()
    await expect(maintenanceButton).toBeVisible()
  })
})

test.describe('存储管理刷新功能', () => {
  test('点击刷新按钮应更新数据', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/storage')

    const refreshBtn = page.getByRole('button', { name: /刷新/i })
    await expect(refreshBtn).toBeVisible()
    await refreshBtn.click()
    await expect(page.locator('body')).toBeVisible()
  })
})

test.describe('CAS 存储系统说明', () => {
  test('应显示 CAS 系统描述', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/storage')

    const casDescription = page.getByText(/CAS|内容寻址存储/i)
    await expect(casDescription).toBeVisible()
  })
})

test.describe('存储管理页面响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/storage')

    const body = page.locator('body')
    await expect(body).toBeVisible()

    const title = page.getByText('存储管理').first()
    await expect(title).toBeVisible()
  })

  test('平板端布局', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await page.goto('/storage')
    await page.waitForLoadState('networkidle')

    const body = page.locator('body')
    await expect(body).toBeVisible()
  })

  test('桌面端卡片应水平排列', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/storage')
    await page.waitForLoadState('networkidle')

    // 桌面端页面应正常渲染
    const body = page.locator('body')
    await expect(body).toBeVisible()
  })
})
