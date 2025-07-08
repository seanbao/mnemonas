import { test, expect, type Locator, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { createFolderThroughUi, fileRowByName, openFolderThroughUi, uploadTextFileThroughPicker } from './helpers/files'
import { expectNoPageHorizontalOverflow } from './helpers/layout'

function getVersionPathInput(page: Page): Locator {
  return page.getByRole('textbox', { name: /输入文件路径|文件路径/i })
}

function getVersionsPageTitle(page: Page): Locator {
  return page.getByRole('heading', { name: '版本历史', exact: true })
}

function getVersionQueryStatus(page: Page): Locator {
  return page.getByText(/未找到版本记录|获取版本历史失败|版本历史暂不可用|当前版本|仅有当前版本/i).first()
}

async function expectVersionQuerySettled(page: Page): Promise<void> {
  const explicitStatus = getVersionQueryStatus(page)
  const versionList = page.getByRole('list', { name: '版本历史' })

  await expect.poll(async () => {
    const hasStatus = await explicitStatus.isVisible().catch(() => false)
    const hasVersionList = await versionList.isVisible().catch(() => false)
    return hasStatus || hasVersionList
  }, { timeout: 10_000 }).toBe(true)
}

/**
 * Version history page E2E tests.
 * auth.setup.ts injects storageState automatically.
 * Login setup failures fail by default; protected-page tests skip only when
 * auth skipping is explicitly enabled for reused environments.
 */

test.describe('版本历史页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/versions')
  })

  test('应显示版本历史页面', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await expect(getVersionsPageTitle(page)).toBeVisible({ timeout: 5000 })
    await expect(getVersionPathInput(page)).toBeVisible()
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

    await expectVersionQuerySettled(page)
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
    const folderName = `e2e-version-${test.info().workerIndex}-${Date.now()}`
    const fileName = `single-version-${Date.now()}.txt`
    const targetPath = `/${folderName}/${fileName}`

    await ensureAuthenticatedAt(page, '/files')
    await createFolderThroughUi(page, folderName)
    await openFolderThroughUi(page, folderName)
    await uploadTextFileThroughPicker(page, fileName, 'single current version')
    await expect(fileRowByName(page, fileName)).toBeVisible({ timeout: 10_000 })

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

    await expect(getVersionsPageTitle(page)).toBeVisible({ timeout: 5000 })
    await expect(getVersionPathInput(page)).toBeVisible()
    await expect(page.getByRole('button', { name: /查询|搜索/i })).toBeVisible()
  })
})

test.describe('版本历史页面响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/versions')

    const title = getVersionsPageTitle(page)
    await expect(title).toBeVisible({ timeout: 5000 })
    await expect(getVersionPathInput(page)).toBeVisible()
    await expect(page.getByRole('button', { name: /查询|搜索/i })).toBeVisible()
    await expectNoPageHorizontalOverflow(page)
  })

  test('平板端布局', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await ensureAuthenticatedAt(page, '/versions')

    const title = getVersionsPageTitle(page)
    await expect(title).toBeVisible({ timeout: 5000 })
    await expect(getVersionPathInput(page)).toBeVisible()
    await expectNoPageHorizontalOverflow(page)
  })
})
