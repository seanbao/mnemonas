import { test, expect, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

async function openSidebarIfNeeded(page: Page): Promise<void> {
  const menuButton = page.getByRole('button', { name: '打开导航菜单' })
  if (await menuButton.isVisible({ timeout: 1000 }).catch(() => false)) {
    await menuButton.click()
    await page.waitForTimeout(300)
  }
}

async function navigateToSearchFromSidebar(page: Page): Promise<void> {
  await openSidebarIfNeeded(page)
  const searchLink = page.getByTestId('app-sidebar-shell').getByRole('link', { name: /搜索|Search/i })
  await expect(searchLink).toBeVisible({ timeout: 2000 })
  await searchLink.dispatchEvent('click')
  await expect(page).toHaveURL(/\/search/)
}

async function expectNoPageHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => {
    const root = document.documentElement
    const body = document.body
    return Math.max(root.scrollWidth, body.scrollWidth) - root.clientWidth
  })

  expect(overflow, `${page.url()} should not overflow horizontally`).toBeLessThanOrEqual(2)
}

async function gotoAuthenticatedRouteForLayout(page: Page, route: string) {
  await page.goto(route, { waitUntil: 'domcontentloaded' })
  if (page.url().includes('/login')) {
    await ensureAuthenticatedAt(page, route)
  }
  await page.locator('body').waitFor({ state: 'visible' })
  await page.getByText('加载中...').waitFor({ state: 'hidden', timeout: 3000 }).catch(() => {})
}

async function expectRoutesDoNotOverflowOnMobile(page: Page, routes: string[]) {
  await page.setViewportSize({ width: 375, height: 667 })

  const [firstRoute, ...remainingRoutes] = routes
  await ensureAuthenticatedAt(page, firstRoute)
  await expect(page.locator('body')).toBeVisible()
  await expectNoPageHorizontalOverflow(page)

  for (const route of remainingRoutes) {
    await gotoAuthenticatedRouteForLayout(page, route)
    await expect(page.locator('body')).toBeVisible()
    await expectNoPageHorizontalOverflow(page)
  }
}

/**
 * 导航 E2E 测试
 * 认证状态由 auth.setup.ts 通过 storageState 自动注入
 * 如果认证启用但登录失败，测试会被跳过
 */

test.describe('侧边栏导航', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/')
  })

  test('应显示侧边栏', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await openSidebarIfNeeded(page)

    const sidebar = page.getByTestId('app-sidebar-shell')
    await expect(sidebar).toBeVisible({ timeout: 5000 })
  })

  test('侧边栏应包含文件导航链接', async ({ page }) => {
    await openSidebarIfNeeded(page)
    const filesLink = page.getByTestId('app-sidebar-shell').getByRole('link', { name: /文件|Files/i })
    await expect(filesLink).toBeVisible({ timeout: 5000 })
  })

  test('侧边栏应包含搜索链接', async ({ page }) => {
    await openSidebarIfNeeded(page)
    const searchLink = page.getByTestId('app-sidebar-shell').getByRole('link', { name: /搜索|Search/i })
    await expect(searchLink).toBeVisible({ timeout: 5000 })
  })

  test('侧边栏应包含设置链接', async ({ page }) => {
    await openSidebarIfNeeded(page)
    const settingsLink = page.getByTestId('app-sidebar-shell').getByRole('link', { name: /设置|Settings/i })
    await expect(settingsLink).toBeVisible({ timeout: 5000 })
  })
})

test.describe('页面路由导航', () => {
  test('导航到 /files 应显示文件页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')

    expect(page.url()).toMatch(/files/)
  })

  test('导航到 /search 应显示搜索页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/search')

    expect(page.url()).toMatch(/search/)
  })

  test('导航到 /settings 应显示设置页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/settings')

    expect(page.url()).toMatch(/settings/)
  })

  test('导航到 /storage 应显示存储页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/storage')

    expect(page.url()).toMatch(/storage/)
  })

  test('导航到 /trash 应显示回收站页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/trash')

    expect(page.url()).toMatch(/trash/)
  })

  test('导航到 /versions 应显示版本页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/versions')

    expect(page.url()).toMatch(/versions/)
  })
})

test.describe('404 页面', () => {
  test('不存在的路由应显示 404 页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/nonexistent-page-xyz123')

    await expect(page).toHaveURL(/\/nonexistent-page-xyz123/)
    await expect(page.getByRole('heading', { name: '404' })).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('heading', { name: '页面不存在' })).toBeVisible()
  })
})

test.describe('侧边栏点击导航', () => {
  test('点击文件链接应导航到文件页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/')
    await openSidebarIfNeeded(page)

    const filesLink = page.getByTestId('app-sidebar-shell').getByRole('link', { name: /文件|Files/i })
    await expect(filesLink).toBeVisible({ timeout: 2000 })
    await filesLink.dispatchEvent('click')
    await page.waitForTimeout(500)
    expect(page.url()).toMatch(/files/)
  })

  test('点击搜索链接应导航到搜索页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/')
    await openSidebarIfNeeded(page)

    const searchLink = page.getByTestId('app-sidebar-shell').getByRole('link', { name: /搜索|Search/i })
    await expect(searchLink).toBeVisible({ timeout: 2000 })
    await searchLink.dispatchEvent('click')
    await page.waitForTimeout(500)
    expect(page.url()).toMatch(/search/)
  })

  test('点击设置链接应导航到设置页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/')
    await openSidebarIfNeeded(page)

    const settingsLink = page.getByTestId('app-sidebar-shell').getByRole('link', { name: /设置|Settings/i })
    await expect(settingsLink).toBeVisible({ timeout: 2000 })
    await settingsLink.dispatchEvent('click')
    await page.waitForTimeout(500)
    expect(page.url()).toMatch(/settings/)
  })
})

test.describe('浏览器历史导航', () => {
  test('后退按钮应正常工作', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')
    await navigateToSearchFromSidebar(page)
    
    await page.goBack()
    await expect(page).toHaveURL(/\/files/)
  })

  test('前进按钮应正常工作', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')
    await navigateToSearchFromSidebar(page)
    
    await page.goBack()
    await expect(page).toHaveURL(/\/files/)
    
    await page.goForward()
    await expect(page).toHaveURL(/\/search/)
  })
})

test.describe('响应式侧边栏', () => {
  test('移动端应显示汉堡菜单或折叠侧边栏', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/')

    // 移动端侧边栏可能折叠
    const hamburger = page.getByRole('button', { name: '打开导航菜单' })
    const sidebar = page.locator('aside, [class*="sidebar"]').first()
    
    const hasHamburger = await hamburger.isVisible({ timeout: 2000 }).catch(() => false)
    const sidebarVisible = await sidebar.isVisible({ timeout: 1000 }).catch(() => false)
    
    // 移动端至少应存在一种导航入口
    expect(hasHamburger || sidebarVisible).toBe(true)
  })

  test('移动端点击导航链接后应关闭侧边栏', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/')

    const openButton = page.getByRole('button', { name: '打开导航菜单' })
    const sidebarShell = page.getByTestId('app-sidebar-shell')
    const overlay = page.getByTestId('mobile-sidebar-overlay')
    const tabbar = page.getByRole('navigation', { name: '移动端主导航' })

    await openButton.click()

    await expect(overlay).toBeVisible({ timeout: 2000 })
    await expect(sidebarShell).toHaveClass(/translate-x-0/, { timeout: 2000 })

    const overlayBox = await overlay.boundingBox()
    const tabbarBox = await tabbar.boundingBox()
    if (!overlayBox || !tabbarBox) {
      throw new Error('expected mobile sidebar overlay and tabbar to have layout boxes')
    }
    const tabbarPointOutsideSidebar = {
      x: tabbarBox.x + tabbarBox.width - 8,
      y: tabbarBox.y + tabbarBox.height / 2,
    }
    const topAtTabbar = await page.evaluate(({ x, y }) => {
      const element = document.elementFromPoint(x, y)
      return element?.getAttribute('data-testid')
    }, tabbarPointOutsideSidebar)
    expect(topAtTabbar).toBe('mobile-sidebar-overlay')

    const topAtHeader = await page.evaluate(() => {
      const element = document.elementFromPoint(window.innerWidth - 8, 32)
      return element?.getAttribute('data-testid')
    })
    expect(topAtHeader).toBe('mobile-sidebar-overlay')

    await sidebarShell.getByRole('link', { name: /搜索|Search/i }).click()

    await expect(page).toHaveURL(/\/search/)
    await expect(overlay).toHaveCount(0)
    await expect(sidebarShell).toHaveClass(/-translate-x-full/, { timeout: 2000 })
    await expect(openButton).toBeVisible({ timeout: 2000 })
  })

  test('移动端底部主导航应可见并可切换常用页面', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/')

    const tabbar = page.getByRole('navigation', { name: '移动端主导航' })
    await expect(tabbar).toBeVisible({ timeout: 2000 })

    await tabbar.getByRole('link', { name: '文件' }).click()
    await expect(page).toHaveURL(/\/files/)

    await tabbar.getByRole('link', { name: '搜索' }).click()
    await expect(page).toHaveURL(/\/search/)
  })

  test('移动端浏览页面不应出现页面级横向溢出', async ({ page }) => {
    test.setTimeout(60_000)
    await expectRoutesDoNotOverflowOnMobile(page, [
      '/',
      '/files',
      '/search',
      '/album',
      '/favorites',
      '/trash',
    ])
  })

  test('移动端管理页面不应出现页面级横向溢出', async ({ page }) => {
    test.setTimeout(60_000)
    await expectRoutesDoNotOverflowOnMobile(page, [
      '/versions',
      '/storage',
      '/maintenance',
      '/users',
      '/system-health',
      '/activity',
      '/settings',
    ])
  })
})
