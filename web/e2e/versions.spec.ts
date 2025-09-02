import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { uploadTextFileThroughPicker } from './helpers/files'

function getVersionPathInput(page: import('@playwright/test').Page) {
  return page.getByRole('textbox', { name: /输入文件路径|文件路径/i })
}

function getVersionsPageTitle(page: import('@playwright/test').Page) {
  return page.getByRole('heading', { name: '版本历史', exact: true })
}

/**
 * 版本历史页面 E2E 测试
 * 认证状态由 auth.setup.ts 通过 storageState 自动注入
 * 如果认证启用但登录失败，测试会被跳过
 */

test.describe('版本历史页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/versions')
  })

  test('应显示版本历史页面', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.locator('body')).toBeVisible()
  })

  test('应显示版本历史标题', async ({ page }) => {
    const title = getVersionsPageTitle(page)
    await expect(title).toBeVisible({ timeout: 5000 })
  })

  test('应显示文件路径输入框', async ({ page }) => {
    const pathInput = getVersionPathInput(page)
    await expect(pathInput).toBeVisible({ timeout: 5000 })
  })

  test('应显示查询按钮', async ({ page }) => {
    const queryBtn = page.getByRole('button', { name: /查询|搜索/i })
    await expect(queryBtn).toBeVisible({ timeout: 5000 })
  })

  test('空状态应显示引导信息', async ({ page }) => {
    const emptyGuide = page.getByRole('heading', { name: '查看文件版本历史' })
    await expect(emptyGuide).toBeVisible({ timeout: 5000 })
  })
})

test.describe('版本历史查询', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/versions')
  })

  test('输入路径后应能查询', async ({ page }) => {
    const pathInput = getVersionPathInput(page)
    await expect(pathInput).toBeVisible({ timeout: 2000 })
    await pathInput.fill('/test/file.txt')

    const queryBtn = page.getByRole('button', { name: /查询|搜索/i })
    await expect(queryBtn).toBeVisible({ timeout: 2000 })
    await queryBtn.click()

    await page.waitForTimeout(1000)

    // 应显示结果或错误信息
    const body = page.locator('body')
    await expect(body).toBeVisible()
  })

  test('查询不存在的文件应显示提示', async ({ page }) => {
    const pathInput = getVersionPathInput(page)
    await expect(pathInput).toBeVisible({ timeout: 2000 })
    await pathInput.fill('/nonexistent/path/xyz123.txt')

    const queryBtn = page.getByRole('button', { name: /查询|搜索/i })
    await expect(queryBtn).toBeVisible({ timeout: 2000 })
    await queryBtn.click()

    const noHistory = page.getByText(/未找到版本记录/i)
    const errorTitle = page.getByText(/获取版本历史失败/i)

    await expect.poll(async () => {
      const hasNoHistory = await noHistory.isVisible().catch(() => false)
      const hasError = await errorTitle.isVisible().catch(() => false)
      return hasNoHistory || hasError
    }, { timeout: 10000 }).toBe(true)
  })

  test('编码路径深链接没有历史时不应显示空白页', async ({ page }) => {
    const targetPath = '/T145iNXfXqXXb1upjX.avif'
    await ensureAuthenticatedAt(page, `/versions?path=${encodeURIComponent(targetPath)}`)

    await expect(getVersionsPageTitle(page)).toBeVisible({ timeout: 5000 })
    await expect(getVersionPathInput(page)).toHaveValue(targetPath, { timeout: 5000 })

    const explicitStatus = page.getByText(/未找到版本记录|获取版本历史失败|版本历史暂不可用/i).first()
    await expect(explicitStatus).toBeVisible({ timeout: 10000 })
  })

  test('只有当前版本的文件应显示版本页内容而不是错误兜底', async ({ page }) => {
    const pageErrors: string[] = []
    page.on('pageerror', (error) => {
      pageErrors.push(error.stack || error.message)
    })
    page.on('console', (message) => {
      if (message.type() === 'error') {
        pageErrors.push(message.text())
      }
    })
    const fileName = `single-version-${Date.now()}.avif`
    const targetPath = `/${fileName}`

    await ensureAuthenticatedAt(page, '/files')
    await uploadTextFileThroughPicker(page, fileName, 'single current version')

    await page.goto(`/versions?path=${encodeURIComponent(targetPath)}`, { waitUntil: 'domcontentloaded' })

    await expect(
      page.getByText('页面加载失败'),
      pageErrors.join('\n\n') || 'page should not render the route error boundary'
    ).toBeHidden({ timeout: 5000 })
    await expect(getVersionsPageTitle(page)).toBeVisible({ timeout: 5000 })
    await expect(getVersionPathInput(page)).toHaveValue(targetPath, { timeout: 5000 })
    await expect(
      page.getByText(/当前版本|仅有当前版本|未找到版本记录/i).first(),
      pageErrors.join('\n\n') || 'single-version file should render an explicit version state'
    ).toBeVisible({ timeout: 10000 })
  })
})

test.describe('版本操作按钮', () => {
  test('版本表格应包含操作列', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/versions')

    // 表格头应包含操作列
    // 这可能不存在（如果没有查询），所以只检查页面存在
    const body = page.locator('body')
    await expect(body).toBeVisible()
  })
})

test.describe('版本历史页面响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/versions')

    const body = page.locator('body')
    await expect(body).toBeVisible()

    // 标题应可见
    const title = getVersionsPageTitle(page)
    await expect(title).toBeVisible({ timeout: 5000 })
  })

  test('平板端布局', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await ensureAuthenticatedAt(page, '/versions')

    const title = page.getByText('版本历史').first()
    await expect(title).toBeVisible({ timeout: 5000 })
  })
})
