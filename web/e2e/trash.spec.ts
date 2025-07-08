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
    await requireOk(await fetch(fileUrl, { method: 'DELETE' }), 'delete trash fixture')
  }, { fileUrl, fileName, content })
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
    await expect(page.getByText(/项\s*·.*天后自动清理/i)).toBeVisible()
  })

  test('应显示回收站标题', async ({ page }) => {
    const title = trashPageTitle(page)
    await expect(title).toBeVisible({ timeout: 5000 })
  })

  test('应显示回收站统计信息', async ({ page }) => {
    // Check item count, size, and automatic cleanup retention.
    const statsText = page.getByText(/项\s*·.*天后自动清理/i)
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
      await requireOk(await fetch(fileUrl, { method: 'DELETE' }), 'delete restore fixture')
    }, { fileUrl, fileName })

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

    await page.evaluate(async ({ rootDirUrl, nestedDirUrl, nestedFileUrl, deleteDirUrl, nestedFileName }) => {
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

      await requireOk(await fetch(deleteDirUrl, { method: 'DELETE' }), 'delete non-empty directory')
    }, { rootDirUrl, nestedDirUrl, nestedFileUrl, deleteDirUrl, nestedFileName })

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
    await expect(page.getByText(/项\s*·.*天后自动清理/i)).toBeVisible()
    await waitForTrashContentState(page)
    await expectNoPageHorizontalOverflow(page)
  })

  test('平板端布局', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await ensureAuthenticatedAt(page, '/trash')

    const title = trashPageTitle(page)
    await expect(title).toBeVisible({ timeout: 5000 })
    await expect(page.getByText(/项\s*·.*天后自动清理/i)).toBeVisible()
    await expectNoPageHorizontalOverflow(page)
  })
})
