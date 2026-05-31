import { test, expect, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { backupJob, routeBackupJobs } from './helpers/backups'
import { expectNoPageHorizontalOverflow } from './helpers/layout'

async function expectDashboardReady(page: Page) {
  const main = page.getByRole('main')
  await expect(page).not.toHaveURL(/\/login/)
  await expect(main.getByRole('heading', { name: '首页' })).toBeVisible({ timeout: 5000 })
  await expect(main.getByText('存储概览', { exact: true })).toBeVisible()
  await expect(main.getByText('最近操作', { exact: true })).toBeVisible()
}

test.describe('主页', () => {
  test('认证后应显示首页内容', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/')

    await expectDashboardReady(page)
  })

  test('认证后应显示导航入口', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/')
    await expectDashboardReady(page)

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
  })

  test('首次部署检查应提示分享启用但认证关闭的公网风险', async ({ page }) => {
    await page.route('**/api/v1/setup/', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        json: {
          success: true,
          is_first_run: true,
          auth_enabled: false,
          share_enabled: true,
          webdav_enabled: true,
          webdav_auth_type: 'basic',
        },
      })
    })

    await ensureAuthenticatedAt(page, '/')
    await expectDashboardReady(page)

    await expect(page.getByText('首次部署检查')).toBeVisible()
    await expect(page.getByText(/认证：\s*需启用/)).toBeVisible()
    await expect(page.getByText(/分享：\s*可用/)).toBeVisible()
    await expect(page.getByText(/分享在无认证保护下可访问/)).toBeVisible()
    await expect(page.getByText(/公网部署前应先处理/)).toBeVisible()
  })
})

test.describe('首页备份风险提示', () => {
  test.beforeEach(async ({ page }) => {
    await routeBackupJobs(page, [
      backupJob('external-disk', '外置硬盘备份', '/restore/mnemonas', false),
    ])
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
  test('认证后文件页面应显示文件浏览器', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')

    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.getByRole('button', { name: '根目录' })).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('button', { name: '上传文件', exact: true })).toBeVisible()
  })
})

test.describe('响应式布局', () => {
  test('移动端应正常渲染', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/')

    await expectDashboardReady(page)
    await expectNoPageHorizontalOverflow(page)
  })

  test('平板端应正常渲染', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await ensureAuthenticatedAt(page, '/')

    await expectDashboardReady(page)
    await expectNoPageHorizontalOverflow(page)
  })
})
