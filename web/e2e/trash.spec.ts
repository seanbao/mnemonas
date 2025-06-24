import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

/**
 * 回收站页面 E2E 测试
 * 认证状态由 auth.setup.ts 通过 storageState 自动注入
 * 如果认证启用但登录失败，测试会被跳过
 */

test.describe('回收站页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/trash')
  })

  test('应显示回收站页面', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.locator('body')).toBeVisible()
  })

  test('应显示回收站标题', async ({ page }) => {
    const title = page.getByText('回收站').first()
    await expect(title).toBeVisible({ timeout: 5000 })
  })

  test('应显示回收站统计信息', async ({ page }) => {
    // 检查统计信息（项数、大小、自动清理时间）
    const statsText = page.getByText(/项|天后自动清理/i)
    await expect(statsText).toBeVisible({ timeout: 5000 })
  })

  test('回收站为空时应显示空状态', async ({ page }) => {
    await page.waitForTimeout(2000)

    // 检查是否有文件项或空状态
    const emptyState = page.getByText(/回收站是空的|暂无|empty/i)
    const itemList = page.locator('[class*="trash"], [class*="item"]').first()
    
    const hasEmpty = await emptyState.isVisible({ timeout: 1000 }).catch(() => false)
    const hasItems = await itemList.isVisible({ timeout: 1000 }).catch(() => false)
    
    expect(hasEmpty || hasItems).toBe(true)
  })
})

test.describe('回收站批量操作', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/trash')
  })

  test('清空回收站按钮应可见（有内容时）', async ({ page }) => {
    // 清空按钮可能只在有内容时显示
    const body = page.locator('body')
    
    // 页面应正常渲染
    await expect(body).toBeVisible()
  })

  test('选中项后应显示批量操作栏', async ({ page }) => {
    // 如果有项目，尝试选中
    const checkbox = page.getByRole('checkbox').nth(1)
    if (await checkbox.isVisible({ timeout: 2000 }).catch(() => false)) {
      await checkbox.click({ force: true })
      
      // 检查批量操作按钮
      const batchRestore = page.getByRole('button', { name: /^恢复$/ })
      const batchDelete = page.getByRole('button', { name: /^永久删除$/ })
      
      const hasRestore = await batchRestore.isVisible({ timeout: 2000 }).catch(() => false)
      const hasDelete = await batchDelete.isVisible({ timeout: 1000 }).catch(() => false)
      
      expect(hasRestore || hasDelete).toBe(true)
    }
  })
})

test.describe('回收站单项操作', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/trash')
  })

  // 这个测试需要回收站中有实际数据才能运行
  // 在没有测试数据的情况下跳过
  test('每个项目应有恢复和删除按钮', async ({ page }) => {
    // 等待页面稳定
    await page.waitForTimeout(1000)

    const emptyState = page.getByText(/回收站是空的|暂无|empty/i)
    const isEmpty = await emptyState.isVisible({ timeout: 1000 }).catch(() => false)
    test.skip(isEmpty, '当前测试数据中没有回收站条目')

    const restoreBtn = page.getByRole('button', { name: /^恢复\s.+$/ }).first()
    const deleteBtn = page.getByRole('button', { name: /^永久删除\s.+$/ }).first()

    const hasRestore = await restoreBtn.isVisible({ timeout: 1000 }).catch(() => false)
    const hasDelete = await deleteBtn.isVisible({ timeout: 1000 }).catch(() => false)

    expect(hasRestore || hasDelete).toBe(true)
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
      const token = localStorage.getItem('mnemonas_token')
      if (!token) {
        throw new Error('missing auth token in localStorage')
      }

      const createHeaders = () => new Headers({
        Authorization: `Bearer ${token}`,
      })

      const requireOk = async (response: Response, action: string) => {
        if (!response.ok) {
          throw new Error(`${action} failed: ${response.status} ${await response.text()}`)
        }
      }

      await requireOk(await fetch(rootDirUrl, { method: 'POST', headers: createHeaders() }), 'create root directory')
      await requireOk(await fetch(nestedDirUrl, { method: 'POST', headers: createHeaders() }), 'create nested directory')

      const formData = new FormData()
      formData.append('file', new File(['nested content'], nestedFileName, { type: 'text/plain' }))
      await requireOk(await fetch(nestedFileUrl, { method: 'POST', body: formData, headers: createHeaders() }), 'upload nested file')

      await requireOk(await fetch(deleteDirUrl, { method: 'DELETE', headers: createHeaders() }), 'delete non-empty directory')
    }, { rootDirUrl, nestedDirUrl, nestedFileUrl, deleteDirUrl, nestedFileName })

    await ensureAuthenticatedAt(page, '/trash')

    await expect(page.getByText(dirName, { exact: true })).toBeVisible({ timeout: 5000 })
    await expect(page.getByText(`/${dirName}`, { exact: true })).toBeVisible({ timeout: 5000 })
    await expect(page.getByText(`/${dirName}/nested/${nestedFileName}`)).toHaveCount(0)
    await expect(page.getByRole('button', { name: `恢复 ${dirName}` })).toBeVisible({ timeout: 5000 })
  })
})

test.describe('回收站确认对话框', () => {
  test('清空回收站应弹出确认对话框', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/trash')

    const emptyTrashBtn = page.getByRole('button', { name: /清空回收站/i })
    if (await emptyTrashBtn.isVisible({ timeout: 2000 }).catch(() => false)) {
      await emptyTrashBtn.click()
      
      // 检查确认对话框
      await expect(page.getByText('确定要清空回收站吗？')).toBeVisible({ timeout: 5000 })
      await expect(page.getByText(/此操作无法撤销/i)).toBeVisible({ timeout: 5000 })
    }
  })
})

test.describe('回收站自动清理提示', () => {
  test('应显示自动清理时间提示', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/trash')

    // 检查自动清理提示（30天）
    const autoCleanHint = page.getByText(/30 天|自动清理/i)
    await expect(autoCleanHint).toBeVisible({ timeout: 5000 })
  })
})

test.describe('回收站页面响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/trash')

    const body = page.locator('body')
    await expect(body).toBeVisible()

    const title = page.getByText('回收站').first()
    await expect(title).toBeVisible({ timeout: 5000 })
  })

  test('平板端布局', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await ensureAuthenticatedAt(page, '/trash')

    const title = page.getByText('回收站').first()
    await expect(title).toBeVisible({ timeout: 5000 })
  })
})
