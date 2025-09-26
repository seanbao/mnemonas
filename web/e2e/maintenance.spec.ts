import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { routeBackupJobs } from './helpers/backups'

test.describe('备份与维护页面', () => {
  test.beforeEach(async ({ page }) => {
    await routeBackupJobs(page)
    await ensureAuthenticatedAt(page, '/maintenance')
  })

  test('应显示维护页和备份任务主入口', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.getByRole('heading', { name: '数据完整性校验' })).toBeVisible()
    await expect(page.getByRole('heading', { name: '备份任务与恢复演练' })).toBeVisible()
    await expect(page.getByRole('button', { name: '批量恢复' })).toBeVisible()
  })

  test('最近恢复应显示已校验和待校验状态', async ({ page }) => {
    await expect(page.getByText('外置硬盘备份')).toBeVisible()
    await expect(page.getByText('待复核恢复备份')).toBeVisible()
    await expect(page.getByText('已校验')).toBeVisible()
    await expect(page.getByText('待校验', { exact: true })).toBeVisible()
    await expect(page.getByLabel('待处理原因：恢复待校验')).toBeVisible()
    await expect(page.getByText('最近恢复尚未完成匹配的只读校验')).toBeVisible()
    await expect(page.getByRole('row').filter({ hasText: '待复核恢复备份' }).getByRole('button', { name: '检查恢复' })).toBeEnabled()
  })

  test('批量恢复弹窗应显示准备度摘要', async ({ page }) => {
    await page.getByRole('button', { name: '批量恢复' }).click()
    const readiness = page.getByLabel('批量恢复准备度摘要')
    await expect(readiness.getByText('尚未选择任务')).toBeVisible()
    await expect(readiness.getByText('尚未选择目标')).toBeVisible()
    await expect(page.getByText('可恢复任务 2 项，待处理 1 项')).toBeVisible()

    await page.getByRole('button', { name: '选择待处理' }).click()
    await expect(readiness.getByText('1 / 20 项')).toBeVisible()
    await expect(readiness.getByText('1 / 1 已填写')).toBeVisible()
    await expect(readiness.getByText('需要生成批量预览')).toBeVisible()

    await page.getByRole('button', { name: '清空选择' }).click()
    await expect(readiness.getByText('尚未选择任务')).toBeVisible()
    await expect(readiness.getByText('尚未选择目标')).toBeVisible()

    await page.getByRole('button', { name: '选择全部' }).click()
    await expect(readiness.getByText('2 / 20 项')).toBeVisible()
    await expect(readiness.getByText('2 / 2 已填写')).toBeVisible()
    await expect(readiness.getByText('需要生成批量预览')).toBeVisible()

    await page.getByRole('button', { name: '清空选择' }).click()
    await expect(readiness.getByText('尚未选择任务')).toBeVisible()
    await expect(readiness.getByText('尚未选择目标')).toBeVisible()

    await page.getByLabel('选择 外置硬盘备份').click()
    await expect(readiness.getByText('1 / 20 项')).toBeVisible()
    await expect(readiness.getByText('1 / 1 已填写')).toBeVisible()
    await expect(readiness.getByText('需要生成批量预览')).toBeVisible()
  })
})
