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

type StubDeleteMode = 'trash' | 'permanent'
const TRASH_POLICY_TOKEN = '1'.repeat(64)
const PERMANENT_POLICY_TOKEN = '2'.repeat(64)
const DELETE_TARGET_TOKEN = '3'.repeat(64)
const DELETE_IDENTITY_TOKEN = '4'.repeat(64)

type StubObservedDeleteTarget = {
  path: string
  observedIdentityToken: string
}

function buildStubFileListResponse(
  mode: StubDeleteMode | undefined,
  fileNames: string[],
  policyToken = TRASH_POLICY_TOKEN,
) {
  const policy = mode === undefined
    ? {}
    : {
        deleteMode: mode,
        deletePolicyToken: policyToken,
        trashRetentionDays: 30,
        trashAutoCleanupEnabled: true,
      }

  return {
    success: true,
    data: {
      path: '/',
      capabilities: { read: true, concreteRead: false, write: true },
      files: fileNames.map((name) => ({
        name,
        path: `/${name}`,
        isDir: false,
        size: 1024,
        modTime: '2026-01-01T00:00:00Z',
        deleteIdentityToken: DELETE_IDENTITY_TOKEN,
        capabilities: { read: true, concreteRead: true, write: true },
      })),
      ...policy,
    },
    timestamp: '2026-01-01T00:00:00Z',
  }
}

function buildStubDeleteIntentResponse(
  mode: StubDeleteMode,
  targets: StubObservedDeleteTarget[],
  policyToken: string,
) {
  return {
    success: true,
    data: {
      deleteMode: mode,
      deletePolicyToken: policyToken,
      trashRetentionDays: 30,
      trashAutoCleanupEnabled: true,
      targets: targets.map((target) => ({
        path: target.path,
        name: target.path.slice(target.path.lastIndexOf('/') + 1),
        isDir: false,
        size: 1024,
        modTime: '2026-01-01T00:00:00Z',
        deleteIdentityToken: target.observedIdentityToken,
        deleteTargetToken: DELETE_TARGET_TOKEN,
      })),
    },
    timestamp: '2026-01-01T00:00:00Z',
  }
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

async function openDeleteDialogFromFileMenu(
  page: Page,
  fileName: string,
  expectedHeading: '移入回收站' | '永久删除' = '移入回收站',
) {
  const menuButton = page.getByLabel(`${fileName} 操作菜单`).first()
  const deleteMenuItem = page.getByRole('menuitem', { name: /^删除$/ })
  const deleteDialogHeading = page.getByRole('heading', { name: expectedHeading })
  const preparationStatus = page.getByRole('status').filter({ hasText: '正在确认删除目标…' })
  const readDeleteState = async (): Promise<'idle' | 'preparing' | 'dialog'> => {
    if (await deleteDialogHeading.isVisible().catch(() => false)) {
      return 'dialog'
    }
    if (await preparationStatus.isVisible().catch(() => false)) {
      return 'preparing'
    }
    return 'idle'
  }

  for (let attempt = 0; attempt < 2; attempt += 1) {
    if (!(await deleteMenuItem.isVisible().catch(() => false))) {
      await menuButton.click()
      await expect(deleteMenuItem).toBeVisible({ timeout: 5_000 })
    }

    let clickError: unknown
    try {
      await deleteMenuItem.click({ timeout: 5_000 })
    } catch (error) {
      clickError = error
    }
    await expect.poll(readDeleteState, { timeout: 2_000 }).not.toBe('idle').catch(() => {})
    const observedState = await readDeleteState()

    if (observedState === 'dialog') {
      return
    }
    if (observedState === 'preparing') {
      await expect(preparationStatus).toBeHidden({ timeout: 30_000 })
      if (await deleteDialogHeading.waitFor({ state: 'visible', timeout: 5_000 }).then(
        () => true,
        () => false
      )) {
        return
      }
    }
    if (await deleteDialogHeading.waitFor({ state: 'visible', timeout: 500 }).then(
      () => true,
      () => false
    )) {
      return
    }

    if (clickError && attempt === 1) {
      throw clickError
    }
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
    await page.getByRole('button', { name: `移入回收站 ${fileName}` }).click()

    const deleteResponse = await deleteResponsePromise
    expect(deleteResponse.ok()).toBe(true)
    const deleteURL = new URL(deleteResponse.url())
    expect(deleteURL.searchParams.getAll('expected_delete_mode')).toEqual(['trash'])
    expect(deleteURL.searchParams.getAll('expected_delete_policy_token')).toHaveLength(1)
    expect(deleteURL.searchParams.get('expected_delete_policy_token')).toMatch(/^[0-9a-f]{64}$/)
    expect(deleteURL.searchParams.getAll('expected_delete_target_token')).toHaveLength(1)
    expect(deleteURL.searchParams.get('expected_delete_target_token')).toMatch(/^[0-9a-f]{64}$/)
    await expect(page.getByText('已移入回收站')).toBeVisible({ timeout: 10_000 })

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

test.describe('文件删除策略确认', () => {
  test.use({ colorScheme: 'light' })

  test('删除目标确认可取消且迟到响应不会打开对话框', async ({ page }) => {
    const fileName = 'cancel-delete-review.txt'
    let releaseIntentResponse: (() => void) | undefined
    let markIntentSettled: (() => void) | undefined
    const intentResponseGate = new Promise<void>((resolve) => {
      releaseIntentResponse = resolve
    })
    const intentSettled = new Promise<void>((resolve) => {
      markIntentSettled = resolve
    })
    let intentRequestCount = 0

    await page.route('**/api/v1/files**', async (route) => {
      const request = route.request()
      const requestURL = new URL(request.url())
      if (request.method() === 'POST' && requestURL.pathname === '/api/v1/files-delete-intents') {
        intentRequestCount += 1
        const body = request.postDataJSON() as { targets: StubObservedDeleteTarget[] }
        await intentResponseGate
        try {
          await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: JSON.stringify(buildStubDeleteIntentResponse('trash', body.targets, TRASH_POLICY_TOKEN)),
          })
        } catch {
          // The browser may have already aborted the routed request.
        } finally {
          markIntentSettled?.()
        }
        return
      }
      if (request.method() === 'GET') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(buildStubFileListResponse('trash', [fileName])),
        })
        return
      }
      await route.continue()
    })

    await ensureAuthenticatedAt(page, '/files')
    await waitForFileBrowser(page)
    const menuButton = page.getByLabel(`${fileName} 操作菜单`).first()
    await menuButton.click()
    await page.getByRole('menuitem', { name: /^删除$/ }).click()

    const preparationStatus = page.getByRole('status')
    await expect(preparationStatus).toContainText('正在确认删除目标…')
    await page.getByRole('button', { name: '取消确认' }).click()

    await expect(preparationStatus).toBeHidden()
    await expect(menuButton).toBeFocused()
    await expect(menuButton).toHaveCSS('opacity', '1')
    expect(intentRequestCount).toBe(1)

    releaseIntentResponse?.()
    await intentSettled
    await expect(page.getByRole('heading', { name: '移入回收站' })).toBeHidden()
  })

  test('删除方式变化时应保留文件并要求按新后果重新确认', async ({ page }) => {
    const fileName = 'policy-review.txt'
    let deleteMode: StubDeleteMode = 'trash'
    let deletePolicyToken = TRASH_POLICY_TOKEN
    let listRequestCount = 0
    const intentTargets: StubObservedDeleteTarget[][] = []
    const deleteRequests: URL[] = []

    await page.route('**/api/v1/files**', async (route) => {
      const request = route.request()
      const requestURL = new URL(request.url())
      if (request.method() === 'POST' && requestURL.pathname === '/api/v1/files-delete-intents') {
        const body = request.postDataJSON() as { targets: StubObservedDeleteTarget[] }
        intentTargets.push(body.targets)
        if (intentTargets.length === 1) {
          await new Promise((resolve) => setTimeout(resolve, 2_500))
        }
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(buildStubDeleteIntentResponse(deleteMode, body.targets, deletePolicyToken)),
        })
        return
      }
      if (request.method() === 'GET') {
        listRequestCount += 1
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(buildStubFileListResponse(deleteMode, [fileName], deletePolicyToken)),
        })
        return
      }
      if (request.method() === 'DELETE') {
        deleteRequests.push(requestURL)
        deleteMode = 'permanent'
        deletePolicyToken = PERMANENT_POLICY_TOKEN
        await route.fulfill({
          status: 409,
          contentType: 'application/json',
          body: JSON.stringify({
            code: 'DELETE_POLICY_CHANGED',
            message: 'delete policy changed',
            details: {
              expected_delete_mode: 'trash',
              actual_delete_mode: 'permanent',
              trash_retention_days: 30,
              trash_auto_cleanup_enabled: true,
            },
            timestamp: '2026-01-01T00:00:00Z',
          }),
        })
        return
      }
      await route.continue()
    })

    await ensureAuthenticatedAt(page, '/files')
    await waitForFileBrowser(page)
    await expect(fileRowByName(page, fileName)).toBeVisible()

    await openDeleteDialogFromFileMenu(page, fileName)
    expect(intentTargets).toEqual([[
      { path: `/${fileName}`, observedIdentityToken: DELETE_IDENTITY_TOKEN },
    ]])
    const trashDialog = page.getByRole('dialog')
    await expect(trashDialog).toHaveAttribute('aria-labelledby', /\S+/)
    await expect(trashDialog).toHaveAttribute('aria-describedby', /\S+/)
    await expect(trashDialog.getByText('将移入回收站，不会立即永久删除。')).toBeVisible()
    await expect(trashDialog.getByRole('button', { name: '取消' })).toBeFocused()
    await expect(trashDialog).toHaveScreenshot('file-delete-trash-dialog.png', {
      animations: 'disabled',
      caret: 'hide',
      maxDiffPixelRatio: 0.005,
    })
    await trashDialog.getByRole('button', { name: `移入回收站 ${fileName}` }).click()

    await expect.poll(() => deleteRequests.length).toBe(1)
    expect(deleteRequests[0].searchParams.getAll('expected_delete_mode')).toEqual(['trash'])
    expect(deleteRequests[0].searchParams.getAll('expected_delete_policy_token')).toEqual([TRASH_POLICY_TOKEN])
    expect(deleteRequests[0].searchParams.getAll('expected_delete_target_token')).toEqual([DELETE_TARGET_TOKEN])
    await expect.poll(() => listRequestCount).toBeGreaterThanOrEqual(2)
    await expect(fileRowByName(page, fileName)).toBeVisible()
    await expect(page.getByText('删除策略已更改，文件未删除')).toBeVisible()
    await expect(page.getByText('列表已刷新，请按当前删除策略重新确认。')).toBeVisible()
    await expect(page.getByRole('heading', { name: '移入回收站' })).toBeHidden()

    await openDeleteDialogFromFileMenu(page, fileName, '永久删除')
    expect(intentTargets).toEqual([
      [{ path: `/${fileName}`, observedIdentityToken: DELETE_IDENTITY_TOKEN }],
      [{ path: `/${fileName}`, observedIdentityToken: DELETE_IDENTITY_TOKEN }],
    ])
    const permanentDialog = page.getByRole('dialog')
    await expect(permanentDialog).toHaveAttribute('aria-labelledby', /\S+/)
    await expect(permanentDialog).toHaveAttribute('aria-describedby', /\S+/)
    await expect(permanentDialog.getByText('文件不会进入回收站，删除后无法恢复。此操作无法撤销。')).toBeVisible()
    await expect(permanentDialog.getByRole('button', { name: '取消' })).toBeFocused()
    await expect(permanentDialog.getByRole('button', { name: `永久删除 ${fileName}` })).toBeVisible()
    await expect(permanentDialog).toHaveScreenshot('file-delete-permanent-dialog.png', {
      animations: 'disabled',
      caret: 'hide',
      maxDiffPixelRatio: 0.005,
    })
  })

  test('删除策略未知时应保持浏览可用并关闭所有删除入口', async ({ page }) => {
    const fileName = 'unknown-policy.txt'
    let listRequestCount = 0
    let deleteRequestCount = 0

    await page.route('**/api/v1/files**', async (route) => {
      const request = route.request()
      if (request.method() === 'GET') {
        listRequestCount += 1
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(buildStubFileListResponse(undefined, [fileName])),
        })
        return
      }
      if (request.method() === 'DELETE') {
        deleteRequestCount += 1
        await route.fulfill({ status: 500, contentType: 'application/json', body: '{}' })
        return
      }
      await route.continue()
    })

    await ensureAuthenticatedAt(page, '/files')
    await waitForFileBrowser(page)

    await expect(fileRowByName(page, fileName)).toBeVisible()
    await expect(page.getByRole('alert').filter({
      hasText: '无法确认当前删除策略。为避免文件被永久删除，删除操作已停用。',
    })).toBeVisible()
    const reloadPolicyButton = page.getByRole('button', { name: '重新加载删除策略' })
    await expect(reloadPolicyButton).toBeVisible()

    await page.getByRole('checkbox', { name: `选择 ${fileName}` }).click()
    await page.keyboard.press('Delete')
    await expect(page.getByRole('dialog')).toBeHidden()
    expect(deleteRequestCount).toBe(0)

    await reloadPolicyButton.click()
    await expect.poll(() => listRequestCount).toBeGreaterThanOrEqual(2)
    expect(deleteRequestCount).toBe(0)
  })

  test('批量删除遇到目标变化时应停止后续请求并保留未处理选择', async ({ page }) => {
    const fileNames = ['batch-a.txt', 'batch-b.txt', 'batch-c.txt']
    let visibleFileNames = [...fileNames]
    const deleteMode: StubDeleteMode = 'trash'
    const deletePolicyToken = TRASH_POLICY_TOKEN
    let listRequestCount = 0
    const intentTargets: StubObservedDeleteTarget[][] = []
    const deletePaths: string[] = []

    await page.route('**/api/v1/files**', async (route) => {
      const request = route.request()
      const requestURL = new URL(request.url())
      if (request.method() === 'POST' && requestURL.pathname === '/api/v1/files-delete-intents') {
        const body = request.postDataJSON() as { targets: StubObservedDeleteTarget[] }
        intentTargets.push(body.targets)
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(buildStubDeleteIntentResponse(deleteMode, body.targets, deletePolicyToken)),
        })
        return
      }
      if (request.method() === 'GET') {
        listRequestCount += 1
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(buildStubFileListResponse(deleteMode, visibleFileNames, deletePolicyToken)),
        })
        return
      }
      if (request.method() === 'DELETE') {
        deletePaths.push(requestURL.pathname)
        expect(requestURL.searchParams.getAll('expected_delete_mode')).toEqual(['trash'])
        expect(requestURL.searchParams.getAll('expected_delete_policy_token')).toEqual([TRASH_POLICY_TOKEN])
        expect(requestURL.searchParams.getAll('expected_delete_target_token')).toEqual([DELETE_TARGET_TOKEN])
        if (deletePaths.length === 1) {
          visibleFileNames = visibleFileNames.filter((name) => name !== 'batch-a.txt')
          await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: JSON.stringify({
              success: true,
              data: { path: '/batch-a.txt' },
              timestamp: '2026-01-01T00:00:00Z',
            }),
          })
          return
        }

        await route.fulfill({
          status: 409,
          contentType: 'application/json',
          body: JSON.stringify({
            code: 'DELETE_TARGET_CHANGED',
            message: 'delete target changed',
            details: {
              path: '/batch-b.txt',
            },
            timestamp: '2026-01-01T00:00:00Z',
          }),
        })
        return
      }
      await route.continue()
    })

    await ensureAuthenticatedAt(page, '/files')
    await waitForFileBrowser(page)
    for (const fileName of fileNames) {
      await page.getByRole('checkbox', { name: `选择 ${fileName}` }).click()
    }

    await page.getByRole('button', { name: '批量删除' }).click()
    await expect.poll(() => intentTargets.length).toBe(1)
    expect(intentTargets).toEqual([fileNames.map((name) => ({
      path: `/${name}`,
      observedIdentityToken: DELETE_IDENTITY_TOKEN,
    }))])
    const batchDialog = page.getByRole('dialog')
    await expect(batchDialog.getByRole('heading', { name: '批量移入回收站' })).toBeVisible()
    await batchDialog.getByRole('button', { name: /批量移入回收站/ }).click()

    await expect.poll(() => deletePaths.length).toBe(2)
    await expect.poll(() => listRequestCount).toBeGreaterThanOrEqual(2)
    await page.waitForTimeout(200)
    expect(deletePaths).toEqual([
      '/api/v1/files/batch-a.txt',
      '/api/v1/files/batch-b.txt',
    ])

    await expect(fileRowByName(page, 'batch-a.txt')).toBeHidden()
    for (const fileName of ['batch-b.txt', 'batch-c.txt']) {
      await expect(fileRowByName(page, fileName)).toBeVisible()
      await expect(page.getByRole('checkbox', { name: `选择 ${fileName}` })).toHaveAttribute('aria-checked', 'true')
    }
    await expect(page.getByText('删除目标已更改，批量删除已停止')).toBeVisible()
    await expect(page.getByText(/已移入回收站 1 项.*2 项未删除/)).toBeVisible()
    await expect(page.getByRole('button', { name: /批量删除/ })).toBeVisible()
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
