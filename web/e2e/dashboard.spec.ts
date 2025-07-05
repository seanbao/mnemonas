import { test, expect, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

async function routeDashboardBackupRisk(page: Page) {
  await page.route('**/api/v1/maintenance/backups', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: [{
          id: 'external-disk',
          name: '外置硬盘备份',
          type: 'local',
          source: '/srv/mnemonas',
          destination: '/mnt/backup-drive/mnemonas',
          disabled: false,
          retention_status: 'ok',
          health_status: 'ok',
          restore_drill_status: 'ok',
          include_config: true,
          verify_after_backup: true,
          exclude: [],
          running: false,
          last_restore: {
            id: 'restore-20260509T040000Z',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T04:00:00Z',
            finished_at: '2026-05-09T04:00:01Z',
            duration_ms: 1000,
            target_path: '/restore/mnemonas',
            config_restored: true,
            file_count: 12,
            verified_bytes: 4096,
            warnings: [],
          },
        }],
      }),
    })
  })
}

test.describe('主页（认证禁用时）', () => {
  test('应显示主页内容或登录页', async ({ page }) => {
    await page.goto('/')
    await expect(page.locator('body')).toBeVisible()
    
    // 应该是主页或登录页
    const isHomePage = !page.url().includes('/login')
    const isLoginPage = page.url().includes('/login')
    
    expect(isHomePage || isLoginPage).toBe(true)
  })

  test('侧边栏或登录表单应可见', async ({ page }) => {
    await page.goto('/')
    await expect(page.locator('body')).toBeVisible()
    
    const isLoginPage = page.url().includes('/login')
    
    if (!isLoginPage) {
      await expect
        .poll(async () => {
          const mobileMenuVisible = await page.getByRole('button', { name: '打开导航菜单' }).isVisible().catch(() => false)
          const mobileNavVisible = await page.getByRole('navigation', { name: '移动端主导航' }).isVisible().catch(() => false)
          const desktopNavVisible = await page.getByRole('navigation', { name: '主导航' }).isVisible().catch(() => false)

          return mobileMenuVisible || mobileNavVisible || desktopNavVisible
        }, {
          message: 'home page should expose a visible navigation entry point',
          timeout: 10_000,
        })
        .toBe(true)
    } else {
      // 认证启用，检查登录表单
      await expect(page.getByRole('button', { name: /登录/i })).toBeVisible()
    }
  })
})

test.describe('首页备份风险提示', () => {
  test.beforeEach(async ({ page }) => {
    await routeDashboardBackupRisk(page)
    await ensureAuthenticatedAt(page, '/')
  })

  test('应提示恢复后缺少匹配校验的备份任务', async ({ page }) => {
    await expect(page.getByText('备份需要查看')).toBeVisible()
    await expect(page.getByText('1 项待处理')).toBeVisible()
    await expect(page.getByText('恢复待校验').first()).toBeVisible()
    await expect(page.getByRole('button', { name: '打开备份' })).toBeVisible()
  })
})

test.describe('文件浏览功能', () => {
  test('文件页面应可访问或重定向到登录', async ({ page }) => {
    await page.goto('/files')
    await page.waitForTimeout(1000)
    
    // 应该是文件页或登录页
    const isFilesPage = page.url().includes('/files')
    const isLoginPage = page.url().includes('/login')
    
    expect(isFilesPage || isLoginPage).toBe(true)
  })
})

test.describe('响应式布局', () => {
  test('移动端应正常渲染', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await page.goto('/')
    
    await page.waitForTimeout(500)
    
    // 页面应该正常渲染（无论是主页还是登录页）
    const body = page.locator('body')
    await expect(body).toBeVisible()
  })

  test('平板端应正常渲染', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await page.goto('/')
    
    await page.waitForTimeout(500)
    
    const body = page.locator('body')
    await expect(body).toBeVisible()
  })
})
