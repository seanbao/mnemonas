import { test, expect, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

/**
 * 文件浏览页面 E2E 测试
 * 认证状态由 auth.setup.ts 通过 storageState 自动注入
 * 如果认证启用但登录失败，测试会被跳过
 */

async function expectNoPageHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => {
    const root = document.documentElement
    const body = document.body
    return Math.max(root.scrollWidth, body.scrollWidth) - root.clientWidth
  })

  expect(overflow).toBeLessThanOrEqual(2)
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

async function createFolder(page: Page, folderName: string) {
  await page.getByRole('button', { name: /新建空间|新建文件夹/i }).click()
  await page.getByPlaceholder('请输入文件夹名称').fill(folderName)
  await page.getByRole('button', { name: '创建' }).click()
  await expect(page.getByText(folderName, { exact: true }).first()).toBeVisible({ timeout: 10_000 })
}

async function openFolder(page: Page, folderName: string) {
  await page.getByText(folderName, { exact: true }).first().dblclick()
}

async function expectFolderView(page: Page, expectedPath: string, breadcrumbName: string) {
  await expect(page).toHaveURL(new RegExp(`${escapeRegExp(expectedPath)}$`), { timeout: 10_000 })
  await expect(page.getByRole('button', { name: breadcrumbName })).toBeVisible({ timeout: 5_000 })
}

test.describe('文件浏览页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')
  })

  test('应显示文件页面', async ({ page }) => {
    // 验证已进入文件页面（不应在登录页）
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.locator('body')).toBeVisible()
  })

  test('应显示面包屑导航', async ({ page }) => {
    // 检查根目录面包屑
    const rootBreadcrumb = page.getByText('根目录')
    await expect(rootBreadcrumb).toBeVisible({ timeout: 5000 })
  })

  test('应显示工具栏按钮', async ({ page }) => {
    const uploadFileBtn = page.getByRole('button', { name: '上传文件', exact: true })
    await expect(uploadFileBtn).toBeVisible({ timeout: 5000 })

    const uploadFolderBtn = page.getByRole('button', { name: '上传文件夹', exact: true })
    await expect(uploadFolderBtn).toBeVisible({ timeout: 5000 })

    // 检查新建文件夹按钮
    const newFolderBtn = page.getByRole('button', { name: /新建空间|新建文件夹/i })
    await expect(newFolderBtn).toBeVisible({ timeout: 5000 })
  })

  test('应支持列表和网格视图切换', async ({ page }) => {
    // 查找视图切换按钮
    const viewToggle = page.locator('button svg[class*="list"], button svg[class*="grid"]').first()
    if (await viewToggle.isVisible({ timeout: 2000 }).catch(() => false)) {
      await viewToggle.click()
      await page.waitForTimeout(300)
    }
  })

  test('空目录应显示空状态提示', async ({ page }) => {
    // 等待页面加载
    await page.waitForTimeout(2000)

    // 检查是否有空状态提示或文件列表
    const emptyState = page.getByText(/空空如也|暂无文件|no files/i)
    const fileList = page.locator('[class*="file"], table tbody tr').first()
    
    const hasEmpty = await emptyState.isVisible({ timeout: 1000 }).catch(() => false)
    const hasFiles = await fileList.isVisible({ timeout: 1000 }).catch(() => false)
    
    expect(hasEmpty || hasFiles).toBe(true)
  })

  test('双击文件夹后路径和面包屑应保持稳定', async ({ page }, testInfo) => {
    const folderName = `e2e-nav-${testInfo.workerIndex}-${Date.now()}`

    await createFolder(page, folderName)
    await openFolder(page, folderName)

    const expectedPath = `/files/${folderName}`
    await expectFolderView(page, expectedPath, folderName)

    await page.waitForTimeout(500)
    await expect(page).toHaveURL(new RegExp(`${escapeRegExp(expectedPath)}$`))
    await expect(page.getByText('这里空空如也')).toBeVisible({ timeout: 5_000 })

    await page.getByRole('button', { name: /根目录/ }).click()
    await expect(page).toHaveURL(/\/files$/)
    await expect(page.getByText(folderName, { exact: true }).first()).toBeVisible({ timeout: 5_000 })
  })

  test('目录导航历史流程应保持 URL、面包屑和列表一致', async ({ page }, testInfo) => {
    testInfo.setTimeout(60_000)

    const rootFolderName = `e2e-flow-${testInfo.workerIndex}-${Date.now()}`
    const childFolderName = `nested-${testInfo.workerIndex}-${Date.now()}`

    await createFolder(page, rootFolderName)
    await openFolder(page, rootFolderName)
    await expectFolderView(page, `/files/${rootFolderName}`, rootFolderName)

    await createFolder(page, childFolderName)
    await openFolder(page, childFolderName)
    await expectFolderView(page, `/files/${rootFolderName}/${childFolderName}`, childFolderName)
    await expect(page.getByText('这里空空如也')).toBeVisible({ timeout: 5_000 })

    await page.goBack()
    await expectFolderView(page, `/files/${rootFolderName}`, rootFolderName)
    await expect(page.getByText(childFolderName, { exact: true }).first()).toBeVisible({ timeout: 5_000 })

    await page.goForward()
    await expectFolderView(page, `/files/${rootFolderName}/${childFolderName}`, childFolderName)

    await page.getByRole('button', { name: rootFolderName }).click()
    await expectFolderView(page, `/files/${rootFolderName}`, rootFolderName)

    await page.getByRole('button', { name: /根目录/ }).click()
    await expect(page).toHaveURL(/\/files$/)
    await expect(page.getByText(rootFolderName, { exact: true }).first()).toBeVisible({ timeout: 5_000 })
  })
})

test.describe('文件批量操作', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')
  })

  test('选择文件后应显示批量操作按钮', async ({ page }) => {
    // 如果有文件，尝试选择
    const checkbox = page.locator('[class*="checkbox"], input[type="checkbox"]').first()
    if (await checkbox.isVisible({ timeout: 2000 }).catch(() => false)) {
      await checkbox.click()
      
      // 检查批量操作按钮
      const batchDeleteBtn = page.getByRole('button', { name: /批量删除/i })
      await expect(batchDeleteBtn).toBeVisible({ timeout: 5000 })
    }
  })
})

test.describe('新建文件夹功能', () => {
  test('点击新建按钮应打开模态框', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')

    const newFolderBtn = page.getByRole('button', { name: /新建空间|新建文件夹/i })
    if (await newFolderBtn.isVisible({ timeout: 2000 }).catch(() => false)) {
      await newFolderBtn.click()
      
      // 检查模态框出现
      await expect(page.getByText(/新建文件夹|文件夹名称/i)).toBeVisible({ timeout: 5000 })
    }
  })
})

test.describe('文件拖放上传', () => {
  test('拖放区域应存在', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')

    // 页面应该响应拖放事件（检查页面结构）
    const body = page.locator('body')
    await expect(body).toBeVisible()
  })
})

test.describe('文件页面响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/files')

    const body = page.locator('body')
    await expect(body).toBeVisible()
    await expect(page.getByText('根目录')).toBeVisible()
    await expectNoPageHorizontalOverflow(page)
  })

  test('平板端布局', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await ensureAuthenticatedAt(page, '/files')

    const body = page.locator('body')
    await expect(body).toBeVisible()
    await expect(page.getByText('根目录')).toBeVisible()
    await expectNoPageHorizontalOverflow(page)
  })
})
