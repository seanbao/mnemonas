import { test, expect, type Locator, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { createFolderThroughUi, dropTextFileOnFileBrowser, fileRowByName, openFolderThroughUi, uploadTextFileThroughPicker } from './helpers/files'
import { expectNoPageHorizontalOverflow } from './helpers/layout'

test.describe.configure({ mode: 'serial' })

const FILE_EMPTY_STATE_PATTERN = /这里空空如也|暂无文件|no files/i

/**
 * File browser page E2E tests.
 * auth.setup.ts injects storageState automatically.
 * Login setup failures fail by default; protected-page tests skip only when
 * auth skipping is explicitly enabled for reused environments.
 */

async function waitForFileBrowser(page: Page) {
  await page.getByText('加载中…').waitFor({ state: 'hidden', timeout: 15_000 }).catch(() => {})
  await expect(page.getByRole('button', { name: '根目录' })).toBeVisible({ timeout: 15_000 })
}

async function isVisible(locator: Locator, timeout = 1000): Promise<boolean> {
  return locator.isVisible({ timeout }).catch(() => false)
}

async function waitForFileBrowserContentState(page: Page): Promise<'empty' | 'items'> {
  const emptyState = page.getByText(FILE_EMPTY_STATE_PATTERN)
  const fileList = page.getByRole('checkbox', { name: /^选择 / }).first()
  let observedState: 'empty' | 'items' | 'loading' = 'loading'

  await expect.poll(async () => {
    if (await isVisible(emptyState, 500)) {
      observedState = 'empty'
      return observedState
    }
    if (await isVisible(fileList, 500)) {
      observedState = 'items'
      return observedState
    }
    observedState = 'loading'
    return observedState
  }, { timeout: 15_000 }).not.toBe('loading')

  return observedState as 'empty' | 'items'
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

async function createFolder(page: Page, folderName: string) {
  await createFolderThroughUi(page, folderName)
}

async function openFolder(page: Page, folderName: string) {
  await openFolderThroughUi(page, folderName)
}

async function expectFolderView(page: Page, expectedPath: string, breadcrumbName: string) {
  await expect(page).toHaveURL(new RegExp(`${escapeRegExp(expectedPath)}$`), { timeout: 10_000 })
  await expect(page.getByRole('button', { name: breadcrumbName })).toBeVisible({ timeout: 5_000 })
}

async function openDeleteDialogFromFileMenu(page: Page, fileName: string) {
  const menuButton = page.getByLabel(`${fileName} 操作菜单`).first()
  const deleteMenuItem = page.getByRole('menuitem', { name: /^删除$/ })
  const deleteDialogHeading = page.getByRole('heading', { name: '确认删除' })

  for (let attempt = 0; attempt < 2; attempt += 1) {
    if (!(await deleteMenuItem.isVisible({ timeout: 500 }).catch(() => false))) {
      await menuButton.click()
      await expect(deleteMenuItem).toBeVisible({ timeout: 5_000 })
    }

    await deleteMenuItem.click()
    if (await deleteDialogHeading.isVisible({ timeout: 2_000 }).catch(() => false)) {
      return
    }

    await page.keyboard.press('Escape').catch(() => {})
  }

  await expect(deleteDialogHeading).toBeVisible({ timeout: 5_000 })
}

async function startPathRecorder(page: Page) {
  await page.evaluate(() => {
    type PathRecorderWindow = Window & {
      __mnemonasPathSamples?: string[]
      __mnemonasStopPathRecorder?: () => void
    }
    const recorderWindow = window as PathRecorderWindow
    recorderWindow.__mnemonasStopPathRecorder?.()
    const samples: string[] = []
    let stopped = false
    const record = () => {
      const pathname = window.location.pathname
      if (samples[samples.length - 1] !== pathname) {
        samples.push(pathname)
      }
    }
    const originalPushState = window.history.pushState
    const originalReplaceState = window.history.replaceState

    window.history.pushState = function patchedPushState(...args) {
      const result = originalPushState.apply(this, args)
      record()
      return result
    }
    window.history.replaceState = function patchedReplaceState(...args) {
      const result = originalReplaceState.apply(this, args)
      record()
      return result
    }

    const onPopState = () => record()
    const tick = () => {
      if (stopped) return
      record()
      window.requestAnimationFrame(tick)
    }

    window.addEventListener('popstate', onPopState)
    recorderWindow.__mnemonasPathSamples = samples
    recorderWindow.__mnemonasStopPathRecorder = () => {
      stopped = true
      window.history.pushState = originalPushState
      window.history.replaceState = originalReplaceState
      window.removeEventListener('popstate', onPopState)
    }
    record()
    window.requestAnimationFrame(tick)
  })
}

async function stopPathRecorder(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    type PathRecorderWindow = Window & {
      __mnemonasPathSamples?: string[]
      __mnemonasStopPathRecorder?: () => void
    }
    const recorderWindow = window as PathRecorderWindow
    recorderWindow.__mnemonasStopPathRecorder?.()
    return recorderWindow.__mnemonasPathSamples ?? []
  })
}

test.describe('文件浏览页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')
    await waitForFileBrowser(page)
  })

  test('应显示文件页面', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.getByRole('button', { name: '根目录' })).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('button', { name: '上传文件', exact: true })).toBeVisible()
  })

  test('应显示面包屑导航', async ({ page }) => {
    const rootBreadcrumb = page.getByRole('button', { name: '根目录' })
    await expect(rootBreadcrumb).toBeVisible({ timeout: 15_000 })
  })

  test('应显示工具栏按钮', async ({ page }) => {
    const uploadFileBtn = page.getByRole('button', { name: '上传文件', exact: true })
    await expect(uploadFileBtn).toBeVisible({ timeout: 5000 })

    const uploadFolderBtn = page.getByRole('button', { name: '上传文件夹', exact: true })
    await expect(uploadFolderBtn).toBeVisible({ timeout: 5000 })

    const newFolderBtn = page.getByRole('button', { name: /新建空间|新建文件夹/i })
    await expect(newFolderBtn).toBeVisible({ timeout: 5000 })
  })

  test('应支持列表和网格视图切换', async ({ page }) => {
    const listViewButton = page.getByRole('button', { name: '列表视图' })
    const gridViewButton = page.getByRole('button', { name: '网格视图' })

    await expect(listViewButton).toBeVisible({ timeout: 5000 })
    await expect(gridViewButton).toBeVisible({ timeout: 5000 })

    await gridViewButton.click()
    await expect(gridViewButton).toHaveAttribute('aria-pressed', 'true')
    await expect(listViewButton).toHaveAttribute('aria-pressed', 'false')

    await listViewButton.click()
    await expect(listViewButton).toHaveAttribute('aria-pressed', 'true')
    await expect(gridViewButton).toHaveAttribute('aria-pressed', 'false')
  })

  test('空目录应显示空状态提示', async ({ page }) => {
    await waitForFileBrowserContentState(page)
  })

  test('目录加载失败应显示人类可识别的错误和重试入口', async ({ page }) => {
    await page.route(/\/api\/v1\/files(\/|\?|$)/, async (route) => {
      await route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({
          success: false,
          error: {
            code: 'SERVICE_UNAVAILABLE',
            message: 'filesystem not initialized',
          },
        }),
      })
    })

    await page.reload({ waitUntil: 'domcontentloaded' })
    await page.getByText('加载中…').waitFor({ state: 'hidden', timeout: 15_000 }).catch(() => {})

    await expect(page.getByRole('heading', { name: '当前目录暂不可用' })).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('文件系统当前不可用，请检查设备状态或稍后重试。')).toBeVisible()
    await expect(page.getByRole('button', { name: '重新加载' })).toBeVisible()
  })

  test('双击文件夹后路径和面包屑应保持稳定', async ({ page }, testInfo) => {
    testInfo.setTimeout(60_000)

    const folderName = `e2e-nav-${testInfo.workerIndex}-${Date.now()}`

    await createFolder(page, folderName)
    await startPathRecorder(page)
    await openFolder(page, folderName)

    const expectedPath = `/files/${folderName}`
    await expectFolderView(page, expectedPath, folderName)

    await waitForFileBrowserContentState(page)
    await expect(page).toHaveURL(new RegExp(`${escapeRegExp(expectedPath)}$`))
    await expect(page.getByText('这里空空如也')).toBeVisible({ timeout: 5_000 })

    const pathSamples = await stopPathRecorder(page)
    const enteredFolderIndex = pathSamples.indexOf(expectedPath)
    expect(enteredFolderIndex).toBeGreaterThanOrEqual(0)
    expect(pathSamples.slice(enteredFolderIndex + 1)).not.toContain('/files')

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

  test('应通过真实 UI 完成建目录、上传文件、删除到回收站', async ({ page }, testInfo) => {
    testInfo.setTimeout(60_000)

    const suffix = `${testInfo.workerIndex}-${Date.now()}`
    const folderName = `e2e-human-${suffix}`
    const fileName = `note-${suffix}.txt`

    await createFolder(page, folderName)
    await openFolder(page, folderName)
    await expectFolderView(page, `/files/${folderName}`, folderName)

    await uploadTextFileThroughPicker(page, fileName, `human playwright workflow ${suffix}`)
    await expect(fileRowByName(page, fileName)).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('上传完成')).toBeVisible({ timeout: 10_000 })
    const hideUploadRecords = page.getByRole('button', { name: '隐藏上传记录' })
    if (await hideUploadRecords.isVisible({ timeout: 1_000 }).catch(() => false)) {
      await hideUploadRecords.click()
      await expect(hideUploadRecords).toBeHidden()
    }

    const deleteResponsePromise = page.waitForResponse((response) => {
      const { pathname } = new URL(response.url())
      return response.request().method() === 'DELETE'
        && pathname === `/api/v1/files/${folderName}/${fileName}`
    })

    await openDeleteDialogFromFileMenu(page, fileName)
    await page.getByRole('button', { name: '删除' }).click()

    const deleteResponse = await deleteResponsePromise
    expect(deleteResponse.ok()).toBe(true)
    await expect(page.getByText('删除成功')).toBeVisible({ timeout: 10_000 })

    await ensureAuthenticatedAt(page, '/trash')
    await expect(page.getByText(fileName, { exact: true }).first()).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText(`/${folderName}/${fileName}`, { exact: true }).first()).toBeVisible({ timeout: 10_000 })
  })
})

test.describe('文件批量操作', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')
  })

  test('选择文件后应显示批量操作按钮', async ({ page }, testInfo) => {
    testInfo.setTimeout(60_000)

    await waitForFileBrowser(page)

    const fileName = `e2e-batch-${testInfo.workerIndex}-${Date.now()}.txt`
    await uploadTextFileThroughPicker(page, fileName, `batch operation fixture ${fileName}`)

    const fileCheckbox = page.getByRole('checkbox', { name: `选择 ${fileName}` })
    await expect(fileRowByName(page, fileName)).toBeVisible({ timeout: 10_000 })
    await fileCheckbox.click()

    await expect(fileCheckbox).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByRole('button', { name: '取消选择' })).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('button', { name: /批量删除/i })).toBeVisible({ timeout: 5000 })
  })
})

test.describe('新建文件夹功能', () => {
  test('点击新建按钮应打开模态框', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')
    await waitForFileBrowser(page)

    const newFolderBtn = page.getByRole('button', { name: /新建空间|新建文件夹/i })
    await expect(newFolderBtn).toBeVisible({ timeout: 5000 })
    await newFolderBtn.click()

    await expect(page.getByRole('heading', { name: '新建文件夹' })).toBeVisible({ timeout: 5000 })
    await expect(page.getByLabel('文件夹名称')).toBeVisible()
  })
})

test.describe('文件拖放上传', () => {
  test('拖放文件应上传到当前目录', async ({ page }, testInfo) => {
    testInfo.setTimeout(60_000)

    await ensureAuthenticatedAt(page, '/files')
    await waitForFileBrowser(page)

    const fileName = `e2e-drop-${testInfo.workerIndex}-${Date.now()}.txt`
    await dropTextFileOnFileBrowser(page, fileName, `drag and drop fixture ${fileName}`)

    await expect(page.getByText('释放以上传')).toBeHidden({ timeout: 5_000 })
    await expect(fileRowByName(page, fileName)).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('上传完成')).toBeVisible({ timeout: 10_000 })
  })
})

test.describe('文件页面响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/files')

    await expect(page.getByRole('button', { name: '根目录' })).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('button', { name: '上传文件', exact: true })).toBeVisible()
    await expectNoPageHorizontalOverflow(page)
  })

  test('平板端布局', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await ensureAuthenticatedAt(page, '/files')

    await expect(page.getByRole('button', { name: '根目录' })).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('button', { name: '上传文件', exact: true })).toBeVisible()
    await expectNoPageHorizontalOverflow(page)
  })
})
