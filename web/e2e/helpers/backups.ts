import { type Page } from '@playwright/test'

type BatchRestoreRequestItem = {
  job_id: string
  target_path: string
  include_config?: boolean
}

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

export function backupJob(id: string, name: string, targetPath: string, matchingVerify = true) {
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

export const defaultBackupJobs = [
  backupJob('external-disk', '外置硬盘备份', '/restore/mnemonas', true),
  backupJob('pending-restore', '待复核恢复备份', '/restore/pending', false),
]

export async function routeBackupJobs(page: Page, jobs = defaultBackupJobs) {
  await page.route(/\/api\/v1\/maintenance\/backups(?:\?.*)?$/, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: jobs,
      }),
    })
  })
}

export async function routeBatchBackupRestore(page: Page) {
  await page.route(/\/api\/v1\/maintenance\/backups\/batch-restore-preview$/, async (route) => {
    const body = route.request().postDataJSON() as { items?: BatchRestoreRequestItem[] }
    const items = body.items ?? []
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        message: 'batch restore preview completed',
        data: {
          id: 'batch-restore-preview-20260509T035900Z',
          status: 'completed',
          started_at: '2026-05-09T03:59:00Z',
          finished_at: '2026-05-09T03:59:01Z',
          duration_ms: 1000,
          total_files: items.length * 12,
          total_bytes: items.length * 4096,
          warning: false,
          warnings: [],
          items: items.map((item, index) => ({
            index,
            job_id: item.job_id,
            target_path: item.target_path,
            include_config: item.include_config ?? false,
            status: 'completed',
            preview: {
              id: `${item.job_id}-restore-preview-20260509T035900Z`,
              job_id: item.job_id,
              status: 'completed',
              started_at: '2026-05-09T03:59:00Z',
              finished_at: '2026-05-09T03:59:01Z',
              duration_ms: 1000,
              source: '/srv/mnemonas',
              destination: `/mnt/backup-drive/${item.job_id}`,
              snapshot_path: `/mnt/backup-drive/${item.job_id}/snapshots/20260509T020304Z`,
              manifest_path: `/mnt/backup-drive/${item.job_id}/snapshots/20260509T020304Z/manifest.json`,
              target_path: item.target_path,
              file_count: 12,
              total_bytes: 4096,
              config_available: true,
              config_included: item.include_config ?? false,
              sample_paths: ['docs/note.txt'],
              preflight_checks: [{
                id: 'target_scope',
                status: 'passed',
                title: '目标路径隔离',
                detail: '目标目录位于受保护路径之外。',
              }],
              warnings: [],
            },
          })),
        },
      }),
    })
  })

  await page.route(/\/api\/v1\/maintenance\/backups\/batch-restore$/, async (route) => {
    const body = route.request().postDataJSON() as { items?: BatchRestoreRequestItem[] }
    const items = body.items ?? []
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        message: 'batch restore completed',
        data: {
          id: 'batch-restore-20260509T040000Z',
          status: 'completed',
          started_at: '2026-05-09T04:00:00Z',
          finished_at: '2026-05-09T04:00:02Z',
          duration_ms: 2000,
          total_files: items.length * 12,
          verified_bytes: items.length * 4096,
          warning: false,
          warnings: [],
          items: items.map((item, index) => ({
            index,
            job_id: item.job_id,
            target_path: item.target_path,
            include_config: item.include_config ?? false,
            status: 'completed',
            restore: restoreResult(item.job_id, item.target_path),
            verify: restoreVerifyResult(item.job_id, item.target_path),
            warnings: [],
          })),
        },
      }),
    })
  })
}
