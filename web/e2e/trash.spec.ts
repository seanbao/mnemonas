import { test, expect, type Locator, type Page, type TestInfo } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { expectNoPageHorizontalOverflow } from './helpers/layout'

test.describe.configure({ mode: 'serial' })

const TRASH_EMPTY_STATE_PATTERN = /回收站是空的|暂无|empty/i

type TrashContentState = 'empty' | 'items' | 'loading'

function trashPageTitle(page: Page): Locator {
  return page.getByRole('heading', { name: '回收站' })
}

function trashEmptyState(page: Page): Locator {
  return page.getByText(TRASH_EMPTY_STATE_PATTERN)
}

function firstTrashItemCheckbox(page: Page): Locator {
  return page.getByRole('checkbox', { name: /^选择 / }).first()
}

function trashFixtureFileName(testInfo: TestInfo, prefix: string): string {
  const suffix = `${testInfo.workerIndex}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  return `${prefix}-${suffix}.txt`
}

async function deleteFileThroughCurrentPolicy(page: Page, fileUrl: string, action: string): Promise<void> {
  await page.evaluate(async ({ fileUrl, action }) => {
    const requireOk = async (response: Response, requestAction: string) => {
      if (!response.ok) {
        throw new Error(`${requestAction} failed: ${response.status} ${await response.text()}`)
      }
      return response
    }

    const fileApiPrefix = '/api/v1/files'
    const encodedFilePath = new URL(fileUrl, window.location.origin).pathname.slice(fileApiPrefix.length)
    const filePath = decodeURIComponent(encodedFilePath)
    if (!filePath.startsWith('/') || filePath === '/') {
      throw new Error(`prepare delete intent failed: invalid file URL ${fileUrl}`)
    }

    const parentPath = filePath.slice(0, filePath.lastIndexOf('/')) || '/'
    const encodedParentPath = parentPath === '/'
      ? '/'
      : parentPath.split('/').map((segment) => encodeURIComponent(segment)).join('/')
    const listResponse = await requireOk(
      await fetch(`${fileApiPrefix}${encodedParentPath}`),
      'list delete target parent',
    )
    const listBody = await listResponse.json() as {
      success?: unknown
      data?: {
        files?: Array<{
          path?: unknown
          deleteIdentityToken?: unknown
        }>
      }
    }
    const matchingFiles = listBody.data?.files?.filter((file) => file.path === filePath)
    if (listBody.success !== true || !Array.isArray(matchingFiles) || matchingFiles.length !== 1) {
      throw new Error('prepare delete intent failed: target missing from parent listing')
    }
    const observedIdentityToken = matchingFiles[0]?.deleteIdentityToken
    if (typeof observedIdentityToken !== 'string' || !/^[0-9a-f]{64}$/.test(observedIdentityToken)) {
      throw new Error('prepare delete intent failed: invalid observed identity token')
    }

    const intentResponse = await requireOk(await fetch('/api/v1/files-delete-intents', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ targets: [{ path: filePath, observedIdentityToken }] }),
    }), 'prepare delete intent')
    const intentBody = await intentResponse.json() as {
      success?: unknown
      data?: {
        deleteMode?: unknown
        deletePolicyToken?: unknown
        targets?: Array<{
          path?: unknown
          deleteIdentityToken?: unknown
          deleteTargetToken?: unknown
        }>
      }
    }
    const deleteMode = intentBody.data?.deleteMode
    const deletePolicyToken = intentBody.data?.deletePolicyToken
    const targets = intentBody.data?.targets
    if (intentBody.success !== true) {
      throw new Error('prepare delete intent failed: invalid response wrapper')
    }
    if (deleteMode !== 'trash' && deleteMode !== 'permanent') {
      throw new Error(`prepare delete intent failed: invalid delete mode ${String(deleteMode)}`)
    }
    if (typeof deletePolicyToken !== 'string' || !/^[0-9a-f]{64}$/.test(deletePolicyToken)) {
      throw new Error('prepare delete intent failed: invalid delete policy token')
    }
    if (!Array.isArray(targets) || targets.length !== 1 || targets[0]?.path !== filePath) {
      throw new Error('prepare delete intent failed: invalid delete target')
    }
    if (targets[0].deleteIdentityToken !== observedIdentityToken) {
      throw new Error('prepare delete intent failed: delete identity mismatch')
    }
    const deleteTargetToken = targets[0].deleteTargetToken
    if (typeof deleteTargetToken !== 'string' || !/^[0-9a-f]{64}$/.test(deleteTargetToken)) {
      throw new Error('prepare delete intent failed: invalid delete target token')
    }

    const query = new URLSearchParams({
      expected_delete_mode: deleteMode,
      expected_delete_policy_token: deletePolicyToken,
      expected_delete_target_token: deleteTargetToken,
    })
    await requireOk(await fetch(`${fileUrl}?${query.toString()}`, { method: 'DELETE' }), action)
  }, { fileUrl, action })
}

async function seedDeletedTextFile(page: Page, fileName: string, content = 'trash fixture'): Promise<void> {
  const fileUrl = `/api/v1/files/${encodeURIComponent(fileName)}`

  await page.evaluate(async ({ fileUrl, fileName, content }) => {
    const requireOk = async (response: Response, action: string) => {
      if (!response.ok) {
        throw new Error(`${action} failed: ${response.status} ${await response.text()}`)
      }
    }

    await requireOk(await fetch(fileUrl, {
      method: 'POST',
      body: new File([content], fileName, { type: 'text/plain' }),
    }), 'create trash fixture')
  }, { fileUrl, fileName, content })
  await deleteFileThroughCurrentPolicy(page, fileUrl, 'delete trash fixture')
}

async function isVisible(locator: Locator, timeout = 1000): Promise<boolean> {
  return locator.isVisible({ timeout }).catch(() => false)
}

async function readTrashContentState(page: Page): Promise<TrashContentState> {
  if (await isVisible(trashEmptyState(page), 500)) {
    return 'empty'
  }
  if (await isVisible(firstTrashItemCheckbox(page), 500)) {
    return 'items'
  }
  return 'loading'
}

async function waitForTrashContentState(page: Page): Promise<Exclude<TrashContentState, 'loading'>> {
  let observedState: TrashContentState = 'loading'
  await expect.poll(async () => {
    observedState = await readTrashContentState(page)
    return observedState
  }, { timeout: 10_000 }).not.toBe('loading')

  return observedState as Exclude<TrashContentState, 'loading'>
}

/**
 * Trash page E2E tests.
 * auth.setup.ts injects storageState automatically.
 * Login setup failures fail by default; protected-page tests skip only when
 * auth skipping is explicitly enabled for reused environments.
 */

test.describe('回收站页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/trash')
  })

  test('应显示回收站页面', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await expect(trashPageTitle(page)).toBeVisible({ timeout: 5000 })
    await expect(page.getByText(/项\s*·.*天后到期/i)).toBeVisible()
  })

  test('应显示回收站标题', async ({ page }) => {
    const title = trashPageTitle(page)
    await expect(title).toBeVisible({ timeout: 5000 })
  })

  test('应显示回收站统计信息', async ({ page }) => {
    // Check item count, size, and automatic cleanup retention.
    const statsText = page.getByText(/项\s*·.*天后到期/i)
    await expect(statsText).toBeVisible({ timeout: 5000 })
  })

  test('应显示内容列表或空状态', async ({ page }) => {
    await waitForTrashContentState(page)
  })
})

test.describe('回收站批量操作', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/trash')
  })

  test('清空回收站按钮应可见（有内容时）', async ({ page }, testInfo) => {
    const fileName = trashFixtureFileName(testInfo, 'e2e-empty-action')
    await seedDeletedTextFile(page, fileName)

    await ensureAuthenticatedAt(page, '/trash')
    await expect(page.getByText(fileName, { exact: true }).filter({ visible: true })).toBeVisible({ timeout: 10_000 })
    await expect(page.getByRole('button', { name: '清空回收站' })).toBeVisible()
  })

  test('选中项后应显示批量操作栏', async ({ page }, testInfo) => {
    const fileName = trashFixtureFileName(testInfo, 'e2e-batch-actions')
    await seedDeletedTextFile(page, fileName)

    await ensureAuthenticatedAt(page, '/trash')
    await expect(page.getByText(fileName, { exact: true }).filter({ visible: true })).toBeVisible({ timeout: 10_000 })
    await page.getByRole('checkbox', { name: `选择 ${fileName}` }).click({ force: true })

    await expect(page.getByText('已选择 1 项')).toBeVisible()
    await expect(page.getByRole('button', { name: /^恢复$/ })).toBeVisible()
    await expect(page.getByRole('button', { name: /^永久删除$/ })).toBeVisible()

    await page.getByRole('button', { name: /^恢复$/ }).click()
    const review = page.getByLabel('跨目录恢复执行前复核')
    await expect(page.getByText('确认批量恢复')).toBeVisible()
    await expect(review.getByText('跨目录恢复复核')).toBeVisible()
    await expect(review.getByText('1 项 · 0 个目录 · 1 个文件')).toBeVisible()
    await expect(review.getByText('冲突处理')).toBeVisible()
    await expect(review.getByText('成功项目会从回收站移除；失败项目会保持选中，便于继续处理。')).toBeVisible()
  })
})

test.describe('回收站单项操作', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/trash')
  })

  test('每个项目应有恢复和删除按钮', async ({ page }, testInfo) => {
    const fileName = trashFixtureFileName(testInfo, 'e2e-row-actions')
    await seedDeletedTextFile(page, fileName)

    await ensureAuthenticatedAt(page, '/trash')

    await expect(page.getByText(fileName, { exact: true }).filter({ visible: true })).toBeVisible({ timeout: 10_000 })
    await expect(page.getByRole('button', { name: `恢复 ${fileName}` })).toBeVisible()
    await expect(page.getByRole('button', { name: `永久删除 ${fileName}` })).toBeVisible()
  })

  test('点击恢复应将回收站文件还原到原目录', async ({ page }, testInfo) => {
    testInfo.setTimeout(60_000)

    const suffix = `${testInfo.workerIndex}-${Date.now()}`
    const fileName = `e2e-restore-${suffix}.txt`
    const fileUrl = `/api/v1/files/${fileName}`

    await page.goto('/files')
    await page.evaluate(async ({ fileUrl, fileName }) => {
      const requireOk = async (response: Response, action: string) => {
        if (!response.ok) {
          throw new Error(`${action} failed: ${response.status} ${await response.text()}`)
        }
      }

      await requireOk(await fetch(fileUrl, {
        method: 'POST',
        body: new File(['restore workflow'], fileName, { type: 'text/plain' }),
      }), 'create restore fixture')
    }, { fileUrl, fileName })
    await deleteFileThroughCurrentPolicy(page, fileUrl, 'delete restore fixture')

    await ensureAuthenticatedAt(page, '/trash')
    await expect(page.getByText(fileName, { exact: true }).filter({ visible: true })).toBeVisible({ timeout: 10_000 })

    const restoreResponsePromise = page.waitForResponse((response) => {
      const { pathname } = new URL(response.url())
      return response.request().method() === 'POST'
        && pathname.startsWith('/api/v1/trash/')
        && pathname.endsWith('/restore')
    })

    await page.getByRole('button', { name: `恢复 ${fileName}` }).click()

    const restoreResponse = await restoreResponsePromise
    expect(restoreResponse.ok()).toBe(true)
    await expect(page.getByText('恢复成功')).toBeVisible({ timeout: 10_000 })

    await ensureAuthenticatedAt(page, '/files')
    await expect(page.getByText(fileName, { exact: true }).first()).toBeVisible({ timeout: 10_000 })
  })

  test('删除非空目录后应只显示目录回收站项', async ({ page }) => {
    const suffix = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
    const dirName = `e2e-trash-dir-${suffix}`
    const nestedFileName = `nested-${suffix}.txt`
    const rootDirUrl = `/api/v1/directories/${dirName}`
    const nestedDirUrl = `/api/v1/directories/${dirName}/nested`
    const nestedFileUrl = `/api/v1/files/${dirName}/nested/${nestedFileName}`
    const deleteDirUrl = `/api/v1/files/${dirName}`

    await page.goto('/files')

    await page.evaluate(async ({ rootDirUrl, nestedDirUrl, nestedFileUrl, nestedFileName }) => {
      const requireOk = async (response: Response, action: string) => {
        if (!response.ok) {
          throw new Error(`${action} failed: ${response.status} ${await response.text()}`)
        }
      }

      await requireOk(await fetch(rootDirUrl, { method: 'POST' }), 'create root directory')
      await requireOk(await fetch(nestedDirUrl, { method: 'POST' }), 'create nested directory')

      const formData = new FormData()
      formData.append('file', new File(['nested content'], nestedFileName, { type: 'text/plain' }))
      await requireOk(await fetch(nestedFileUrl, { method: 'POST', body: formData }), 'upload nested file')

    }, { rootDirUrl, nestedDirUrl, nestedFileUrl, nestedFileName })
    await deleteFileThroughCurrentPolicy(page, deleteDirUrl, 'delete non-empty directory')

    await ensureAuthenticatedAt(page, '/trash')

    await expect(page.getByText(dirName, { exact: true }).filter({ visible: true })).toBeVisible({ timeout: 5000 })
    await expect(page.getByText(`/${dirName}`, { exact: true }).filter({ visible: true })).toBeVisible({ timeout: 5000 })
    await expect(page.getByText(`/${dirName}/nested/${nestedFileName}`)).toHaveCount(0)
    await expect(page.getByRole('button', { name: `恢复 ${dirName}` })).toBeVisible({ timeout: 5000 })
  })
})

test.describe('回收站确认对话框', () => {
  test('清空回收站应弹出确认对话框', async ({ page }, testInfo) => {
    await ensureAuthenticatedAt(page, '/trash')
    const fileName = trashFixtureFileName(testInfo, 'e2e-empty-dialog')
    await seedDeletedTextFile(page, fileName)

    await ensureAuthenticatedAt(page, '/trash')
    await expect(page.getByText(fileName, { exact: true }).filter({ visible: true })).toBeVisible({ timeout: 10_000 })
    const emptyTrashBtn = page.getByRole('button', { name: /清空回收站/i })
    await emptyTrashBtn.click()

    // Check the destructive-action confirmation dialog.
    await expect(page.getByText('确定要清空回收站吗？')).toBeVisible({ timeout: 5000 })
    await expect(page.getByText(/此操作无法撤销/i)).toBeVisible({ timeout: 5000 })
  })
})

test.describe('回收站自动清理提示', () => {
  test('应显示自动清理时间提示', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/trash')

    // Check the automatic cleanup retention hint.
    const autoCleanHint = page.getByText(/30 天|自动清理/i)
    await expect(autoCleanHint).toBeVisible({ timeout: 5000 })
  })
})

test.describe('回收站页面响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/trash')

    const title = trashPageTitle(page)
    await expect(title).toBeVisible({ timeout: 5000 })
    await expect(page.getByText(/项\s*·.*天后到期/i)).toBeVisible()
    await waitForTrashContentState(page)
    await expectNoPageHorizontalOverflow(page)
  })

  test('平板端布局', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await ensureAuthenticatedAt(page, '/trash')

    const title = trashPageTitle(page)
    await expect(title).toBeVisible({ timeout: 5000 })
    await expect(page.getByText(/项\s*·.*天后到期/i)).toBeVisible()
    await expectNoPageHorizontalOverflow(page)
  })
})
