import { test, expect, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

const ACTIVITY_STAT_LABELS = ['累计操作', '今日操作', '最常见操作', '最活跃用户'] as const

type StatCardLayout = {
  label: string
  left: number
  top: number
  width: number
  height: number
}

async function expectActivityStatsVisible(page: Page): Promise<void> {
  await expect(page.getByRole('heading', { name: '最近操作' })).toBeVisible({ timeout: 10_000 })
  await expect(page.getByText(/历史记录总量|统计暂不可用/).first()).toBeVisible({ timeout: 10_000 })
  await expect(page.getByText('统计暂不可用')).toHaveCount(0)

  for (const label of ACTIVITY_STAT_LABELS) {
    await expect(page.getByText(label, { exact: true })).toBeVisible({ timeout: 10_000 })
  }
  await expect(page.getByText('高风险摘要')).toBeVisible({ timeout: 10_000 })
}

async function collectActivityStatCardLayout(page: Page): Promise<StatCardLayout[]> {
  return page.evaluate((labels) => {
    return labels.map((label) => {
      const title = Array.from(document.querySelectorAll('p')).find((element) => (
        element.textContent?.trim() === label
      ))
      const card = title?.closest('.card-meridian')

      if (!card) {
        return null
      }

      const rect = card.getBoundingClientRect()
      return {
        label,
        left: Math.round(rect.left),
        top: Math.round(rect.top),
        width: Math.round(rect.width),
        height: Math.round(rect.height),
      }
    }).filter((layout): layout is StatCardLayout => layout !== null)
  }, ACTIVITY_STAT_LABELS)
}

function groupLayoutRows(layouts: StatCardLayout[]): StatCardLayout[][] {
  const sortedLayouts = [...layouts].sort((left, right) => (
    left.top === right.top ? left.left - right.left : left.top - right.top
  ))
  const rows: StatCardLayout[][] = []

  for (const layout of sortedLayouts) {
    const row = rows.find((candidate) => Math.abs(candidate[0].top - layout.top) <= 4)

    if (row) {
      row.push(layout)
    } else {
      rows.push([layout])
    }
  }

  return rows.map((row) => row.sort((left, right) => left.left - right.left))
}

test.describe('最近操作统计概览', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/activity')
  })

  test('应显示统计概览并保留最近操作列表区域', async ({ page }) => {
    await expectActivityStatsVisible(page)
    await expect(page.getByText('历史记录总量')).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('当天新增记录')).toBeVisible({ timeout: 10_000 })

    const hasActivityRow = await page.locator('.activity-log-row').first().isVisible({ timeout: 5_000 }).catch(() => false)
    const hasEmptyState = await page.getByText('暂无最近操作').isVisible({ timeout: 1_000 }).catch(() => false)

    expect(hasActivityRow || hasEmptyState).toBe(true)
  })

  test('管理员可按用户筛选最近操作', async ({ page }) => {
    const userFilter = page.getByPlaceholder('按用户筛选')
    const canFilterByUser = await userFilter.isVisible({ timeout: 3_000 }).catch(() => false)
    test.skip(!canFilterByUser, '当前测试账号不是管理员或无权按用户筛选最近操作')

    const filteredResponsePromise = page.waitForResponse((response) => {
      const url = new URL(response.url())
      return response.request().method() === 'GET'
        && url.pathname.startsWith('/api/v1/activity')
        && url.searchParams.get('user') === 'admin'
    })

    await userFilter.fill('admin')

    const filteredResponse = await filteredResponsePromise
    expect(filteredResponse.ok()).toBe(true)
    await expect(page.getByText('用户：admin')).toBeVisible({ timeout: 5_000 })
  })

  test('可按时间范围筛选最近操作', async ({ page }) => {
    const timeRangeFilter = page.getByRole('button', { name: /筛选时间范围/ })
    await expect(timeRangeFilter).toBeVisible({ timeout: 10_000 })

    const filteredResponsePromise = page.waitForResponse((response) => {
      const url = new URL(response.url())
      return response.request().method() === 'GET'
        && url.pathname === '/api/v1/activity/'
        && url.searchParams.has('since')
    })

    await timeRangeFilter.click()
    await page.getByRole('option', { name: '近 7 天' }).click()

    const filteredResponse = await filteredResponsePromise
    expect(filteredResponse.ok()).toBe(true)
    await expect(page.getByText('时间：近 7 天')).toBeVisible({ timeout: 5_000 })
  })

  test('可按审计分组筛选最近操作', async ({ page }) => {
    const groupFilter = page.getByRole('button', { name: /筛选审计分组/ })
    await expect(groupFilter).toBeVisible({ timeout: 10_000 })

    const filteredResponsePromise = page.waitForResponse((response) => {
      const url = new URL(response.url())
      return response.request().method() === 'GET'
        && url.pathname === '/api/v1/activity/'
        && url.searchParams.get('action_group') === 'share'
    })

    await groupFilter.click()
    await page.getByRole('option', { name: '分享相关' }).click()

    const filteredResponse = await filteredResponsePromise
    expect(filteredResponse.ok()).toBe(true)
    await expect(page.getByText('分组：分享相关')).toBeVisible({ timeout: 5_000 })
  })

  test('可按路径筛选最近操作', async ({ page }) => {
    const pathFilter = page.getByPlaceholder('按路径筛选')
    await expect(pathFilter).toBeVisible({ timeout: 10_000 })

    const filteredResponsePromise = page.waitForResponse((response) => {
      const url = new URL(response.url())
      return response.request().method() === 'GET'
        && url.pathname === '/api/v1/activity/'
        && url.searchParams.get('path') === '/'
    })

    await pathFilter.fill('/')

    const filteredResponse = await filteredResponsePromise
    expect(filteredResponse.ok()).toBe(true)
    await expect(page.getByText('路径：/')).toBeVisible({ timeout: 5_000 })
  })

  test('非法路径筛选应停留在本地校验', async ({ page }) => {
    const pathFilter = page.getByPlaceholder('按路径筛选')
    await expect(pathFilter).toBeVisible({ timeout: 10_000 })

    const activityRequests: string[] = []
    page.on('request', (request) => {
      const url = new URL(request.url())
      if (request.method() === 'GET' && url.pathname.startsWith('/api/v1/activity')) {
        activityRequests.push(url.href)
      }
    })

    await pathFilter.fill('../secret')

    await expect(page.getByRole('heading', { name: '路径筛选无效' })).toBeVisible({ timeout: 5_000 })
    await expect(page.getByText('路径不能包含 .. 或控制字符').first()).toBeVisible({ timeout: 5_000 })
    expect(activityRequests.some((url) => url.includes('..'))).toBe(false)
    await expect(page.getByText('路径：/../secret')).toHaveCount(0)

    await page.getByRole('button', { name: '清除路径条件' }).click()

    await expect(pathFilter).toHaveValue('')
    await expect(page.getByRole('heading', { name: '路径筛选无效' })).toHaveCount(0)
    await expect(page.getByText('累计操作')).toBeVisible({ timeout: 5_000 })
    expect(activityRequests.some((url) => url.includes('..'))).toBe(false)
  })

  test('移动端统计卡片应保持两列且无横向溢出', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await ensureAuthenticatedAt(page, '/activity')
    await expectActivityStatsVisible(page)

    const layouts = await collectActivityStatCardLayout(page)
    expect(layouts).toHaveLength(ACTIVITY_STAT_LABELS.length)

    const rows = groupLayoutRows(layouts)
    expect(rows.map((row) => row.map((layout) => layout.label))).toEqual([
      ['累计操作', '今日操作'],
      ['最常见操作', '最活跃用户'],
    ])
    expect(rows.every((row) => row.every((layout) => layout.width > 0 && layout.height > 0))).toBe(true)

    const hasHorizontalOverflow = await page.evaluate(() => (
      document.documentElement.scrollWidth > document.documentElement.clientWidth + 2
    ))
    expect(hasHorizontalOverflow).toBe(false)
  })

  test('清空记录操作应先打开确认对话框', async ({ page }) => {
    const clearButton = page.getByRole('button', { name: '清空记录' })
    const canClearActivity = await clearButton.isVisible({ timeout: 3_000 }).catch(() => false)
    test.skip(!canClearActivity, '当前测试账号不是管理员或无权清空最近操作')

    await clearButton.click()

    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible({ timeout: 5_000 })
    await expect(page.getByText('确认清空最近操作')).toBeVisible()
    await expect(page.getByText('该操作会删除所有最近操作记录，无法撤销。')).toBeVisible()
    await expect(page.getByRole('button', { name: '确认清空' })).toBeVisible()

    await page.getByRole('button', { name: '取消' }).click()
    await expect(dialog).toBeHidden({ timeout: 5_000 })
  })
})
