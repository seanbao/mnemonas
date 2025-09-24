import { test, expect, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

function backupRun(jobId: string) {
  return {
    id: `${jobId}-run-20260509T020304Z`,
    job_id: jobId,
    status: 'completed',
    started_at: '2026-05-09T02:03:04Z',
    finished_at: '2026-05-09T02:03:05Z',
    duration_ms: 1000,
    source: '/srv/mnemonas',
    destination: `/mnt/backup-drive/${jobId}`,
    snapshot_path: `/mnt/backup-drive/${jobId}/snapshots/20260509T020304Z`,
    manifest_path: `/mnt/backup-drive/${jobId}/snapshots/20260509T020304Z/manifest.json`,
    file_count: 12,
    total_bytes: 4096,
    config_included: true,
    trigger: 'scheduled',
    warning: false,
    warnings: [],
    pruned_snapshots: 0,
  }
}

function restoreResult(jobId: string, targetPath: string) {
  return {
    id: `${jobId}-restore-20260509T040000Z`,
    job_id: jobId,
    status: 'completed',
    started_at: '2026-05-09T04:00:00Z',
    finished_at: '2026-05-09T04:00:01Z',
    duration_ms: 1000,
    snapshot_path: `/mnt/backup-drive/${jobId}/snapshots/20260509T020304Z`,
    manifest_path: `/mnt/backup-drive/${jobId}/snapshots/20260509T020304Z/manifest.json`,
    target_path: targetPath,
    config_restored: true,
    config_path: `${targetPath}/.mnemonas-restore/config.toml`,
    file_count: 12,
    verified_bytes: 4096,
    warnings: [],
  }
}

function restoreVerifyResult(jobId: string, targetPath: string) {
  return {
    id: `${jobId}-restore-verify-20260509T040005Z`,
    job_id: jobId,
    status: 'completed',
    started_at: '2026-05-09T04:00:05Z',
    finished_at: '2026-05-09T04:00:06Z',
    duration_ms: 1000,
    source: '/srv/mnemonas',
    destination: `/mnt/backup-drive/${jobId}`,
    snapshot_path: `/mnt/backup-drive/${jobId}/snapshots/20260509T020304Z`,
    manifest_path: `/mnt/backup-drive/${jobId}/snapshots/20260509T020304Z/manifest.json`,
    target_path: targetPath,
    file_count: 12,
    verified_bytes: 4096,
    config_path: `${targetPath}/.mnemonas-restore/config.toml`,
    config_found: true,
    files_dir_found: true,
    internal_dir_found: true,
    index_found: true,
    objects_dir_found: true,
    looks_like_storage_root: true,
    warnings: [],
  }
}

function backupJob(id: string, name: string, targetPath: string, matchingVerify = true) {
  const run = backupRun(id)
  const restore = restoreResult(id, targetPath)
  const verify = restoreVerifyResult(id, targetPath)

  return {
    id,
    name,
    type: 'local',
    source: '/srv/mnemonas',
    destination: `/mnt/backup-drive/${id}`,
    disabled: false,
    schedule_interval: '24h0m0s',
    next_run_at: '2026-05-10T02:03:04Z',
    stale_after: '72h0m0s',
    restore_drill_stale_after: '720h0m0s',
    max_snapshots: 7,
    max_age: '720h0m0s',
    retention_status: 'ok',
    retention_message: '本地快照自动清理已配置',
    health_status: 'ok',
    health_message: 'last successful backup completed recently',
    restore_drill_status: 'ok',
    restore_drill_message: '恢复演练仍在预期窗口内',
    include_config: true,
    verify_after_backup: true,
    exclude: [],
    running: false,
    last_run: run,
    last_successful_run: run,
    last_restore: restore,
    last_restore_verify: verify,
    last_matching_restore_verify: matchingVerify ? verify : undefined,
    restore_history: [restore],
    restore_report_findings: matchingVerify
      ? ['未发现阻塞项；仍需在切换前按恢复清单人工复核。']
      : ['最近一次恢复尚无匹配的只读校验。'],
  }
}

async function routeBackupJobs(page: Page) {
  await page.route('**/api/v1/maintenance/backups', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: [
          backupJob('external-disk', '外置硬盘备份', '/restore/mnemonas', true),
          backupJob('pending-restore', '待复核恢复备份', '/restore/pending', false),
        ],
      }),
    })
  })
}

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
    await expect(page.getByLabel('待处理原因: 恢复待校验')).toBeVisible()
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
