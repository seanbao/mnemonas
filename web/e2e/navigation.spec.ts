import { test, expect, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { expectNoPageHorizontalOverflow } from './helpers/layout'

test.setTimeout(45_000)

async function openSidebarIfNeeded(page: Page): Promise<void> {
  const sidebar = getSidebar(page)
  const menuButton = page.getByRole('button', { name: '打开导航菜单' })
  if (await menuButton.isVisible({ timeout: 1000 }).catch(() => false)) {
    await menuButton.click()
  }
  await expect(sidebar).toBeVisible({ timeout: 5000 })
}

function getSidebar(page: Page) {
  return page.getByRole('complementary', { name: '侧边栏' })
}

function getMobileSidebarOverlay(page: Page) {
  return page.getByRole('button', { name: '关闭导航遮罩' })
}

async function navigateToSearchFromSidebar(page: Page): Promise<void> {
  await openSidebarIfNeeded(page)
  const searchLink = getSidebar(page).getByRole('link', { name: /搜索|Search/i })
  await expect(searchLink).toBeVisible({ timeout: 5000 })
  await searchLink.click()
  await expect(page).toHaveURL(/\/search/)
}

async function clickSidebarLinkAndExpectURL(page: Page, name: RegExp, url: RegExp): Promise<void> {
  await openSidebarIfNeeded(page)

  const link = getSidebar(page).getByRole('link', { name })
  await expect(link).toBeVisible({ timeout: 2000 })
  await link.dispatchEvent('click')
  await expect(page).toHaveURL(url)
}

async function expectRouteSurface(page: Page, route: string): Promise<void> {
  const main = page.locator('main')
  await expect(page).not.toHaveURL(/\/login/)

  switch (route) {
    case '/':
      await expect(main.getByRole('heading', { name: '首页' })).toBeVisible({ timeout: 5000 })
      await expect(main.getByText('存储概览', { exact: true })).toBeVisible()
      break
    case '/files':
      await expect(main.getByRole('button', { name: '根目录' })).toBeVisible({ timeout: 5000 })
      await expect(main.getByRole('button', { name: '上传文件', exact: true })).toBeVisible()
      break
    case '/search':
      await expect(main.getByRole('heading', { name: '搜索', exact: true })).toBeVisible({ timeout: 5000 })
      await expect(main.getByLabel('搜索文件名')).toBeVisible()
      break
    case '/album':
      await expect(main.getByRole('heading', { name: '相册', exact: true })).toBeVisible({ timeout: 5000 })
      await expect(main.getByText(/共 \d+ 张图片/)).toBeVisible()
      break
    case '/favorites':
      await expect(main.getByRole('heading', { name: '收藏夹', exact: true }).first()).toBeVisible({ timeout: 5000 })
      break
    case '/trash':
      await expect(main.getByRole('heading', { name: '回收站' })).toBeVisible({ timeout: 5000 })
      await expect(main.getByText(/项\s*·.*天后自动清理/i)).toBeVisible()
      break
    case '/versions':
      await expect(main.getByRole('heading', { name: '版本历史', exact: true })).toBeVisible({ timeout: 5000 })
      await expect(main.getByRole('textbox', { name: /输入文件路径|文件路径/i })).toBeVisible()
      break
    case '/storage':
      await expect(main.getByText('空间与存储').first()).toBeVisible({ timeout: 5000 })
      await expect(main.getByRole('button', { name: '刷新', exact: true })).toBeVisible()
      break
    case '/maintenance':
      await expect(main.getByRole('heading', { name: '备份与维护' })).toBeVisible({ timeout: 5000 })
      break
    case '/users':
      await expect(main.getByRole('heading', { name: '用户管理' })).toBeVisible({ timeout: 5000 })
      break
    case '/system-health':
      await expect(main.getByRole('heading', { name: '设备状态' })).toBeVisible({ timeout: 5000 })
      await expect(main.getByRole('button', { name: /刷新/ })).toBeVisible()
      break
    case '/activity':
      await expect(main.getByRole('heading', { name: '最近操作' }).first()).toBeVisible({ timeout: 5000 })
      break
    case '/settings':
      await expect(main.getByRole('heading', { name: /设置/i }).first()).toBeVisible({ timeout: 5000 })
      await expect(main.getByRole('heading', { name: '按使用目标调整设备' })).toBeVisible()
      await expect(main.getByRole('button', { name: /账户与远程访问/ })).toBeVisible()
      break
    default:
      throw new Error(`no route surface assertion for ${route}`)
  }
}

async function gotoAuthenticatedRouteForLayout(page: Page, route: string) {
  await page.goto(route, { waitUntil: 'domcontentloaded' })
  if (page.url().includes('/login')) {
    await ensureAuthenticatedAt(page, route)
  }
  await page.locator('body').waitFor({ state: 'visible' })
  await page.getByText('加载中…').waitFor({ state: 'hidden', timeout: 3000 }).catch(() => {})
  await expectRouteSurface(page, route)
}

async function expectRoutesDoNotOverflowOnMobile(page: Page, routes: string[]) {
  await page.setViewportSize({ width: 375, height: 667 })

  const [firstRoute, ...remainingRoutes] = routes
  await ensureAuthenticatedAt(page, firstRoute)
  await expectRouteSurface(page, firstRoute)
  await expectNoPageHorizontalOverflow(page)

  for (const route of remainingRoutes) {
    await gotoAuthenticatedRouteForLayout(page, route)
    await expectNoPageHorizontalOverflow(page)
  }
}

/**
 * Navigation E2E tests.
 * Authentication state is injected by auth.setup.ts through storageState.
 * Login setup failures fail by default; protected-page tests skip only when
 * auth skipping is explicitly enabled for reused environments.
 */

test.describe('侧边栏导航', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/')
  })

  test('应显示侧边栏', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await openSidebarIfNeeded(page)

    const sidebar = getSidebar(page)
    await expect(sidebar).toBeVisible({ timeout: 5000 })
  })

  test('侧边栏应包含文件导航链接', async ({ page }) => {
    await openSidebarIfNeeded(page)
    const filesLink = getSidebar(page).getByRole('link', { name: /文件|Files/i })
    await expect(filesLink).toBeVisible({ timeout: 5000 })
  })

  test('侧边栏应包含搜索链接', async ({ page }) => {
    await openSidebarIfNeeded(page)
    const searchLink = getSidebar(page).getByRole('link', { name: /搜索|Search/i })
    await expect(searchLink).toBeVisible({ timeout: 5000 })
  })

  test('侧边栏应包含设置链接', async ({ page }) => {
    await openSidebarIfNeeded(page)
    const settingsLink = getSidebar(page).getByRole('link', { name: /设置|Settings/i })
    await expect(settingsLink).toBeVisible({ timeout: 5000 })
  })
})

test.describe('页面路由导航', () => {
  test('导航到 /files 应显示文件页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')

    await expect(page).toHaveURL(/\/files/)
    await expectRouteSurface(page, '/files')
  })

  test('导航到 /search 应显示搜索页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/search')

    await expect(page).toHaveURL(/\/search/)
    await expectRouteSurface(page, '/search')
  })

  test('导航到 /settings 应显示设置页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/settings')

    await expect(page).toHaveURL(/\/settings/)
    await expectRouteSurface(page, '/settings')
  })

  test('导航到 /storage 应显示存储页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/storage')

    await expect(page).toHaveURL(/\/storage/)
    await expectRouteSurface(page, '/storage')
  })

  test('导航到 /trash 应显示回收站页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/trash')

    await expect(page).toHaveURL(/\/trash/)
    await expectRouteSurface(page, '/trash')
  })

  test('导航到 /versions 应显示版本页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/versions')

    await expect(page).toHaveURL(/\/versions/)
    await expectRouteSurface(page, '/versions')
  })
})

test.describe('404 页面', () => {
  test('不存在的路由应显示 404 页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/nonexistent-page-xyz123')

    await expect(page).toHaveURL(/\/nonexistent-page-xyz123/)
    await expect(page.getByRole('heading', { name: '404' })).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('heading', { name: '页面不存在' })).toBeVisible()
    await expect(page.getByText('该页面可能已被移动或删除，请检查 URL 是否正确。')).toBeVisible()
  })
})

test.describe('侧边栏点击导航', () => {
  test('桌面侧边栏应能返回首页', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')
    await clickSidebarLinkAndExpectURL(page, /首页|Home/i, /^http:\/\/[^/]+\/$/)
    await expectRouteSurface(page, '/')
  })

  test('点击文件链接应导航到文件页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/')
    await clickSidebarLinkAndExpectURL(page, /文件|Files/i, /\/files/)
    await expectRouteSurface(page, '/files')
  })

  test('点击搜索链接应导航到搜索页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/')
    await clickSidebarLinkAndExpectURL(page, /搜索|Search/i, /\/search/)
    await expectRouteSurface(page, '/search')
  })

  test('点击设置链接应导航到设置页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/')
    await clickSidebarLinkAndExpectURL(page, /设置|Settings/i, /\/settings/)
    await expectRouteSurface(page, '/settings')
  })
})

test.describe('浏览器历史导航', () => {
  test('后退按钮应正常工作', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')
    await navigateToSearchFromSidebar(page)
    
    await page.goBack()
    await expect(page).toHaveURL(/\/files/)
    await expectRouteSurface(page, '/files')
  })

  test('前进按钮应正常工作', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')
    await navigateToSearchFromSidebar(page)
    
    await page.goBack()
    await expect(page).toHaveURL(/\/files/)
    await expectRouteSurface(page, '/files')
    
    await page.goForward()
    await expect(page).toHaveURL(/\/search/)
    await expectRouteSurface(page, '/search')
  })
})

test.describe('响应式侧边栏', () => {
  test('移动端应显示汉堡菜单或折叠侧边栏', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/')

    const hamburger = page.getByRole('button', { name: '打开导航菜单' })
    const sidebar = getSidebar(page)
    
    const hasHamburger = await hamburger.isVisible({ timeout: 2000 }).catch(() => false)
    const sidebarVisible = await sidebar.isVisible({ timeout: 1000 }).catch(() => false)
    
    expect(hasHamburger || sidebarVisible).toBe(true)
  })

  test('移动端点击导航链接后应关闭侧边栏', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/')

    const openButton = page.getByRole('button', { name: '打开导航菜单' })
    const sidebar = getSidebar(page)
    const overlay = getMobileSidebarOverlay(page)
    const tabbar = page.getByRole('navigation', { name: '移动端主导航' })

    await openButton.click()

    await expect(overlay).toBeVisible({ timeout: 2000 })
    await expect(sidebar).toBeVisible({ timeout: 2000 })

    const overlayBox = await overlay.boundingBox()
    const tabbarBox = await tabbar.boundingBox()
    if (!overlayBox || !tabbarBox) {
      throw new Error('expected mobile sidebar overlay and tabbar to have layout boxes')
    }
    const tabbarPointOutsideSidebar = {
      x: tabbarBox.x + tabbarBox.width - 8,
      y: tabbarBox.y + tabbarBox.height / 2,
    }
    const buttonLabelAtPoint = async (point: { x: number; y: number }) => page.evaluate(({ x, y }) => {
      const element = document.elementFromPoint(x, y)
      return element instanceof HTMLButtonElement ? element.getAttribute('aria-label') : null
    }, point)
    const topAtTabbar = await buttonLabelAtPoint(tabbarPointOutsideSidebar)
    expect(topAtTabbar).toBe('关闭导航遮罩')

    const topAtHeader = await buttonLabelAtPoint({ x: await page.evaluate(() => window.innerWidth - 8), y: 32 })
    expect(topAtHeader).toBe('关闭导航遮罩')

    await sidebar.getByRole('link', { name: /搜索|Search/i }).click()

    await expect(page).toHaveURL(/\/search/)
    await expect(overlay).toHaveCount(0)
    await expect(sidebar).toBeHidden({ timeout: 2000 })
    await expect(openButton).toBeVisible({ timeout: 2000 })
  })

  test('移动端底部主导航应可见并可切换常用页面', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/')

    const tabbar = page.getByRole('navigation', { name: '移动端主导航' })
    await expect(tabbar).toBeVisible({ timeout: 2000 })

    await tabbar.getByRole('link', { name: '文件' }).click()
    await expect(page).toHaveURL(/\/files/)
    await expectRouteSurface(page, '/files')

    await tabbar.getByRole('link', { name: '搜索' }).click()
    await expect(page).toHaveURL(/\/search/)
    await expectRouteSurface(page, '/search')
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
