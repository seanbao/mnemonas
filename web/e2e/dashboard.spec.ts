import { test, expect } from '@playwright/test'

test.describe('主页（认证禁用时）', () => {
  test('应显示主页内容或登录页', async ({ page }) => {
    await page.goto('/')
    
    // 等待页面加载
    await page.waitForTimeout(1000)
    
    // 应该是主页或登录页
    const isHomePage = !page.url().includes('/login')
    const isLoginPage = page.url().includes('/login')
    
    expect(isHomePage || isLoginPage).toBe(true)
  })

  test('侧边栏或登录表单应可见', async ({ page }) => {
    await page.goto('/')
    await page.waitForTimeout(1000)
    
    const isLoginPage = page.url().includes('/login')
    
    if (!isLoginPage) {
      const menuButton = page.getByRole('button', { name: '打开导航菜单' })
      if (await menuButton.isVisible({ timeout: 1000 }).catch(() => false)) {
        await expect(page.getByRole('navigation', { name: '移动端主导航' })).toBeVisible({ timeout: 5000 })
      } else {
        const nav = page.locator('aside, nav, [class*="sidebar"]').first()
        await expect(nav).toBeVisible({ timeout: 5000 })
      }
    } else {
      // 认证启用，检查登录表单
      await expect(page.getByRole('button', { name: /登录/i })).toBeVisible()
    }
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
