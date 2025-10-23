import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { uploadTextFileThroughPicker } from './helpers/files'
import { expectNoPageHorizontalOverflow } from './helpers/layout'

/**
 * Search page E2E tests.
 * Authentication state is injected by auth.setup.ts through storageState.
 * Login setup failures fail by default; protected-page tests skip only when
 * auth skipping is explicitly enabled for reused environments.
 */

test.describe('搜索页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/search')
  })

  test('应显示搜索页面', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.getByRole('heading', { name: '搜索', exact: true })).toBeVisible({ timeout: 5000 })
    await expect(page.getByLabel('搜索文件名')).toBeVisible()
  })

  test('应显示搜索输入框', async ({ page }) => {
    const searchInput = page.getByLabel('搜索文件名')
    await expect(searchInput).toBeVisible({ timeout: 5000 })
  })

  test('应显示搜索页面标题', async ({ page }) => {
    const title = page.getByRole('heading', { name: '搜索', exact: true })
    await expect(title).toBeVisible({ timeout: 5000 })
  })

  test('空搜索应显示提示信息', async ({ page }) => {
    const emptyHint = page.getByText(/输入关键词开始搜索/i)
    await expect(emptyHint).toBeVisible({ timeout: 5000 })
  })

  test('输入搜索词应触发搜索', async ({ page }) => {
    const searchInput = page.getByLabel('搜索文件名')
    await expect(searchInput).toBeVisible({ timeout: 2000 })
    await searchInput.fill('test')

    await expect(page).toHaveURL(/q=test/)
  })

  test('搜索不存在的文件应显示无结果提示', async ({ page }) => {
    const searchInput = page.getByLabel('搜索文件名')
    await expect(searchInput).toBeVisible({ timeout: 2000 })
    await searchInput.fill('nonexistent_file_xyz_123')
    await expect(page).toHaveURL(/q=nonexistent_file_xyz_123/)

    await expect
      .poll(async () => {
        const noResultsVisible = await page.getByText('未找到匹配的文件').isVisible().catch(() => false)
        const zeroResultsVisible = await page.getByText('找到 0 个结果').isVisible().catch(() => false)
        return noResultsVisible || zeroResultsVisible
      }, {
        message: 'search should settle on an empty-result state',
        timeout: 10_000,
      })
      .toBe(true)
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

    await expect(page.getByRole('heading', { name: '搜索', exact: true })).toBeVisible({ timeout: 5000 })
    const searchInput = page.getByLabel('搜索文件名')
    await expect(searchInput).toBeVisible({ timeout: 5000 })
    await expect(page.getByText(/输入关键词开始搜索/i)).toBeVisible()
    await expectNoPageHorizontalOverflow(page)
  })

  test('平板端布局', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await ensureAuthenticatedAt(page, '/search')

    await expect(page.getByRole('heading', { name: '搜索', exact: true })).toBeVisible({ timeout: 5000 })
    const searchInput = page.getByLabel('搜索文件名')
    await expect(searchInput).toBeVisible({ timeout: 5000 })
    await expectNoPageHorizontalOverflow(page)
  })
})
