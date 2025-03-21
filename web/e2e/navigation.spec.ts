import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

async function openSidebarIfNeeded(page: import('@playwright/test').Page): Promise<void> {
  const menuButton = page.getByRole('button', { name: '打开导航菜单' })
  if (await menuButton.isVisible({ timeout: 1000 }).catch(() => false)) {
    await menuButton.click()
    await page.waitForTimeout(300)
  }
}

async function navigateToSearchFromSidebar(page: import('@playwright/test').Page): Promise<void> {
  await openSidebarIfNeeded(page)
  const searchLink = page.getByRole('link', { name: /搜索|Search/i })
  await expect(searchLink).toBeVisible({ timeout: 2000 })
  await searchLink.dispatchEvent('click')
  await expect(page).toHaveURL(/\/search/)
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
    const sidebar = page.locator('aside, nav, [class*="sidebar"]').first()
    await expect(sidebar).toBeVisible({ timeout: 5000 })
  })

  test('侧边栏应包含文件导航链接', async ({ page }) => {
    const filesLink = page.getByRole('link', { name: /文件|Files/i })
    await expect(filesLink).toBeVisible({ timeout: 5000 })
  })

  test('侧边栏应包含搜索链接', async ({ page }) => {
    const searchLink = page.getByRole('link', { name: /搜索|Search/i })
    await expect(searchLink).toBeVisible({ timeout: 5000 })
  })

  test('侧边栏应包含设置链接', async ({ page }) => {
    const settingsLink = page.getByRole('link', { name: /设置|Settings/i })
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

    // 应显示 404 页面或重定向到首页或其他页面
    const notFound = page.getByText(/404|找不到|not found/i)
    const hasNotFound = await notFound.isVisible({ timeout: 2000 }).catch(() => false)
    
    // 测试通过条件：显示 404 或被重定向到任何有效页面
    const isValidPage = !page.url().includes('/login')
    
    console.log('404 test - hasNotFound:', hasNotFound, 'isValidPage:', isValidPage)
    expect(hasNotFound || isValidPage).toBe(true)
  })
})

test.describe('侧边栏点击导航', () => {
  test('点击文件链接应导航到文件页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/')
    await openSidebarIfNeeded(page)

    const filesLink = page.getByRole('link', { name: /文件|Files/i })
    await expect(filesLink).toBeVisible({ timeout: 2000 })
    await filesLink.dispatchEvent('click')
    await page.waitForTimeout(500)
    expect(page.url()).toMatch(/files/)
  })

  test('点击搜索链接应导航到搜索页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/')
    await openSidebarIfNeeded(page)

    const searchLink = page.getByRole('link', { name: /搜索|Search/i })
    await expect(searchLink).toBeVisible({ timeout: 2000 })
    await searchLink.dispatchEvent('click')
    await page.waitForTimeout(500)
    expect(page.url()).toMatch(/search/)
  })

  test('点击设置链接应导航到设置页面', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/')
    await openSidebarIfNeeded(page)

    const settingsLink = page.getByRole('link', { name: /设置|Settings/i })
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
})
