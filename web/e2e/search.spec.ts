import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { uploadTextFileThroughPicker } from './helpers/files'

/**
 * 搜索页面 E2E 测试
 * 认证状态由 auth.setup.ts 通过 storageState 自动注入
 * 如果认证启用但登录失败，测试会被跳过
 */

test.describe('搜索页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/search')
  })

  test('应显示搜索页面', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.locator('body')).toBeVisible()
  })

  test('应显示搜索输入框', async ({ page }) => {
    const searchInput = page.getByPlaceholder(/输入文件名/i)
    await expect(searchInput).toBeVisible({ timeout: 5000 })
  })

  test('应显示搜索页面标题', async ({ page }) => {
    const title = page.getByText('搜索').first()
    await expect(title).toBeVisible({ timeout: 5000 })
  })

  test('空搜索应显示提示信息', async ({ page }) => {
    const emptyHint = page.getByText(/输入关键词开始搜索/i)
    await expect(emptyHint).toBeVisible({ timeout: 5000 })
  })

  test('输入搜索词应触发搜索', async ({ page }) => {
    const searchInput = page.getByPlaceholder(/输入文件名/i)
    await expect(searchInput).toBeVisible({ timeout: 2000 })
    await searchInput.fill('test')
    await page.waitForTimeout(500)

    // URL 应该包含搜索参数
    await expect(page).toHaveURL(/q=test/)
  })

  test('搜索不存在的文件应显示无结果提示', async ({ page }) => {
    const searchInput = page.getByPlaceholder(/输入文件名/i)
    await expect(searchInput).toBeVisible({ timeout: 2000 })
    await searchInput.fill('nonexistent_file_xyz_123')
    await page.waitForTimeout(2000)

    // 应显示无结果提示
    const noResults = page.getByText(/未找到|no.*result|没有匹配/i)
    const hasNoResults = await noResults.isVisible({ timeout: 3000 }).catch(() => false)

    // 或者显示结果数为 0
    const zeroResults = page.getByText(/找到 0 个结果/i)
    const hasZeroResults = await zeroResults.isVisible({ timeout: 1000 }).catch(() => false)

    expect(hasNoResults || hasZeroResults).toBe(true)
  })
})

test.describe('搜索结果交互', () => {
  test('搜索结果应可点击并跳转到文件所在目录', async ({ page }, testInfo) => {
    testInfo.setTimeout(60_000)

    const suffix = `${testInfo.workerIndex}-${Date.now()}`
    const fileName = `e2e-search-${suffix}.txt`

    await ensureAuthenticatedAt(page, '/files')
    await uploadTextFileThroughPicker(page, fileName, `searchable playwright fixture ${suffix}`)

    await ensureAuthenticatedAt(page, `/search?q=${encodeURIComponent(fileName)}`)
    const resultItem = page.getByRole('button', { name: `打开文件 /${fileName}` })
    await expect(resultItem).toBeVisible({ timeout: 10_000 })

    await resultItem.click()

    await expect(page).toHaveURL(/\/files\/?$/)
    await expect(page.getByText(fileName, { exact: true }).first()).toBeVisible({ timeout: 10_000 })
    await expect(page.getByLabel(`${fileName} 操作菜单`).first()).toBeVisible({ timeout: 10_000 })
  })
})

test.describe('搜索页面响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/search')

    const body = page.locator('body')
    await expect(body).toBeVisible()

    // 搜索输入在移动端也应可见
    const searchInput = page.getByPlaceholder(/输入文件名/i)
    await expect(searchInput).toBeVisible({ timeout: 3000 })
  })

  test('平板端布局', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await ensureAuthenticatedAt(page, '/search')

    const searchInput = page.getByPlaceholder(/输入文件名/i)
    await expect(searchInput).toBeVisible({ timeout: 3000 })
  })
})
