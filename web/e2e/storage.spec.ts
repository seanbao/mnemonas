import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

/**
 * 空间与存储页面 E2E 测试
 * 认证状态由 auth.setup.ts 通过 storageState 自动注入
 * 如果认证启用但登录失败，测试会被跳过
 */

test.describe('空间与存储页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/storage')
  })

  test('应显示空间与存储页面', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.locator('body')).toBeVisible()
  })

  test('应显示空间与存储标题', async ({ page }) => {
    const title = page.getByText('空间与存储').first()
    await expect(title).toBeVisible()
  })

  test('应显示刷新按钮', async ({ page }) => {
    const refreshBtn = page.getByRole('button', { name: '刷新', exact: true })
    await expect(refreshBtn).toBeVisible()
  })
})

test.describe('存储空间概览', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/storage')
  })

  test('应显示存储使用进度条', async ({ page }) => {
    const storageOverview = page.getByText('存储空间使用情况')
    await expect(storageOverview).toBeVisible()

    const usageLabel = page.getByText('已用')
    await expect(usageLabel).toBeVisible()
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

  test('应显示磁盘容量', async ({ page }) => {
    const diskCard = page.getByText(/磁盘容量/i)
    await expect(diskCard).toBeVisible()
  })

  test('应显示文件系统类型', async ({ page }) => {
    const filesystemCard = page.getByText(/文件系统/i)
    await expect(filesystemCard).toBeVisible()
  })

  test('应显示 CAS 大小', async ({ page }) => {
    const sizeCard = page.getByText(/CAS 大小/i)
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
    const scrubCard = page.getByRole('heading', { name: '完整性检查' })
    await expect(scrubCard).toBeVisible()
  })

  test('应显示垃圾回收卡片', async ({ page }) => {
    const gcCard = page.getByRole('heading', { name: '清理历史对象' })
    await expect(gcCard).toBeVisible()
  })

  test('维护按钮应打开维护工具入口', async ({ page }) => {
    const maintenanceButton = page.getByRole('button', { name: '打开维护工具' }).first()
    await expect(maintenanceButton).toBeVisible()
    await maintenanceButton.click()
    await expect(page).toHaveURL(/\/maintenance$/)
  })
})

test.describe('空间与存储刷新功能', () => {
  test('点击刷新按钮应更新数据', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/storage')

    const refreshBtn = page.getByRole('button', { name: '刷新', exact: true })
    await expect(refreshBtn).toBeVisible()
    await refreshBtn.click()

    const title = page.getByText('空间与存储').first()
    await expect(title).toBeVisible()
  })
})

test.describe('存储系统说明', () => {
  test('应显示混合存储描述', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/storage')

    const casDescription = page.getByText(/文件占用、版本对象和目录配额/i)
    await expect(casDescription).toBeVisible()
  })
})

test.describe('空间与存储页面响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/storage')

    const body = page.locator('body')
    await expect(body).toBeVisible()

    const title = page.getByText('空间与存储').first()
    await expect(title).toBeVisible()
  })

  test('平板端布局', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await ensureAuthenticatedAt(page, '/storage')

    const title = page.getByText('空间与存储').first()
    await expect(title).toBeVisible({ timeout: 5000 })
  })

  test('桌面端卡片应水平排列', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await ensureAuthenticatedAt(page, '/storage')

    const statsCards = page.getByText(/磁盘容量|CAS 大小|对象总数|去重率|节省空间/i)
    await expect(statsCards.first()).toBeVisible({ timeout: 5000 })
  })
})
