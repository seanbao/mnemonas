import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { routeBackupJobs, routeBatchBackupRestore } from './helpers/backups'

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

  test('恢复弹窗应显示引导式阶段进度', async ({ page }) => {
    await page.getByRole('row').filter({ hasText: '外置硬盘备份' }).getByRole('button', { name: '恢复', exact: true }).click()

    const guide = page.getByLabel('恢复流程进度')
    await expect(guide.getByText('恢复流程', { exact: true })).toBeVisible()
    await expect(guide.getByText('目标目录', { exact: true })).toBeVisible()
    await expect(guide.getByText('恢复预览', { exact: true })).toBeVisible()
    await expect(guide.getByText('执行恢复', { exact: true })).toBeVisible()
    await expect(guide.getByText('只读校验与切换', { exact: true })).toBeVisible()
    await expect(guide.getByText('目标已填写：/mnt/restore/external-disk', { exact: true })).toBeVisible()
    await expect(guide.getByText('生成预览以确认文件、配置和预检', { exact: true })).toBeVisible()
    await expect(guide.getByText('预览通过后执行恢复', { exact: true })).toBeVisible()
    await expect(guide.getByText('恢复完成后自动检查', { exact: true })).toBeVisible()
  })

  test('批量恢复弹窗应显示准备度摘要', async ({ page }) => {
    await page.getByRole('button', { name: '批量恢复' }).click()
    const flow = page.getByLabel('批量恢复流程进度')
    const readiness = page.getByLabel('批量恢复准备度摘要')
    await expect(flow.getByText('批量恢复流程', { exact: true })).toBeVisible()
    await expect(flow.getByText('选择任务', { exact: true })).toBeVisible()
    await expect(flow.getByText('目标目录', { exact: true })).toBeVisible()
    await expect(flow.getByText('批量预览', { exact: true })).toBeVisible()
    await expect(flow.getByText('执行与只读校验', { exact: true })).toBeVisible()
    await expect(flow.getByText('选择要恢复的备份任务', { exact: true })).toBeVisible()
    await expect(flow.getByText('选择任务后填写独立目标目录', { exact: true })).toBeVisible()
    await expect(flow.getByText('目标目录确认后生成预览', { exact: true })).toBeVisible()
    await expect(flow.getByText('预览通过后执行批量恢复', { exact: true })).toBeVisible()
    await expect(readiness.getByText('尚未选择任务')).toBeVisible()
    await expect(readiness.getByText('尚未选择目标')).toBeVisible()
    await expect(page.getByText('可恢复任务 2 项，待处理 1 项')).toBeVisible()

    await page.getByRole('button', { name: '选择待处理' }).click()
    await expect(flow.getByText('已选择 1 项', { exact: true })).toBeVisible()
    await expect(flow.getByText('1 个目标目录已确认', { exact: true })).toBeVisible()
    await expect(flow.getByText('生成批量预览以确认预检', { exact: true })).toBeVisible()
    await expect(readiness.getByText('1 / 20 项')).toBeVisible()
    await expect(readiness.getByText('1 / 1 已填写')).toBeVisible()
    await expect(readiness.getByText('需要生成批量预览')).toBeVisible()

    await page.getByRole('button', { name: '清空选择' }).click()
    await expect(readiness.getByText('尚未选择任务')).toBeVisible()
    await expect(readiness.getByText('尚未选择目标')).toBeVisible()

    await page.getByRole('button', { name: '选择全部' }).click()
    await expect(flow.getByText('已选择 2 项', { exact: true })).toBeVisible()
    await expect(flow.getByText('2 个目标目录已确认', { exact: true })).toBeVisible()
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

  test('批量恢复完成后应显示可复制恢复记录入口', async ({ page }) => {
    await routeBatchBackupRestore(page)

    await page.getByRole('button', { name: '批量恢复' }).click()
    await page.getByLabel('选择 外置硬盘备份').click()

    await page.getByRole('button', { name: '生成批量预览' }).click()
    await expect(page.getByText('批量预览结果')).toBeVisible()
    await expect(page.getByLabel('批量恢复流程进度').getByText('预览通过，可开始批量恢复', { exact: true })).toBeVisible()

    await page.getByRole('button', { name: '开始批量恢复' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByText('批量恢复已完成', { exact: true })).toBeVisible()
    await expect(dialog.getByRole('button', { name: '复制批量恢复记录' })).toBeVisible()
  })

  test('批量恢复预检失败后应显示未写入和处置建议', async ({ page }) => {
    await routeBatchBackupRestore(page, { restoreMode: 'preflight-failure' })

    await page.getByRole('button', { name: '批量恢复' }).click()
    await page.getByLabel('选择 外置硬盘备份').click()

    await page.getByRole('button', { name: '生成批量预览' }).click()
    await expect(page.getByText('批量预览结果')).toBeVisible()
    await expect(page.getByLabel('批量恢复流程进度').getByText('预览通过，可开始批量恢复', { exact: true })).toBeVisible()

    await page.getByRole('button', { name: '开始批量恢复' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByText('批量恢复失败', { exact: true }).first()).toBeVisible()
    await expect(dialog.getByLabel('批量恢复流程进度').getByText('批量恢复失败，未完成的项目需处理', { exact: true })).toBeVisible()
    await expect(dialog.getByText('所有批量恢复项目均失败')).toBeVisible()
    await expect(dialog.getByText('批量恢复预检未通过，未写入任何目标数据')).toBeVisible()
    await expect(dialog.getByText('批量恢复预检未通过，该项目未开始写入')).toBeVisible()
    await expect(dialog.getByText('预检拦截未写入：处理失败预检项后重新生成批量预览。', { exact: true })).toBeVisible()
    await expect(dialog.getByRole('button', { name: '复制批量恢复记录' })).toBeVisible()
  })
})
