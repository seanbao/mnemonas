import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent } from '@testing-library/react'
import { render, screen, waitFor, within } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import Maintenance from './Maintenance'

const mockAddToast = vi.fn()
const originalClipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')

const { mockUser } = vi.hoisted(() => ({
  mockUser: { id: 'u1', username: 'admin', role: 'admin' as const, email: 'admin@local', homeDir: '/' },
}))

vi.mock('@heroui/react', async () => {
  const actual = await vi.importActual<typeof import('@heroui/react')>('@heroui/react')
  return {
    ...actual,
    addToast: (...args: unknown[]) => mockAddToast(...args),
  }
})

// Mock API
vi.mock('@/api/files', () => ({
  ApiError: class ApiError extends Error {
    status: number
    code?: string
    constructor(message: string, status: number, code?: string) {
      super(message)
      this.status = status
      this.code = code
    }
    get isUnavailable() {
      return this.status === 503 || this.code === 'SERVICE_UNAVAILABLE'
    }
  },
  getScrubResult: vi.fn(),
  runScrub: vi.fn(),
  downloadDiagnosticsExport: vi.fn(),
  listBackupJobs: vi.fn(),
  runBackupJob: vi.fn(),
  checkBackupRetentionJob: vi.fn(),
  runBackupRestoreDrill: vi.fn(),
  previewBackupRestoreJob: vi.fn(),
  previewBatchBackupRestore: vi.fn(),
  restoreBackupJob: vi.fn(),
  runBatchBackupRestore: vi.fn(),
  verifyBackupRestoreJob: vi.fn(),
  downloadBackupRestoreReport: vi.fn(),
}))

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useUser: () => mockUser,
  }
})

import {
  ApiError,
  getScrubResult,
  runScrub,
  downloadDiagnosticsExport,
  listBackupJobs,
  runBackupJob,
  checkBackupRetentionJob,
  runBackupRestoreDrill,
  previewBackupRestoreJob,
  previewBatchBackupRestore,
  restoreBackupJob,
  runBatchBackupRestore,
  verifyBackupRestoreJob,
  downloadBackupRestoreReport,
} from '@/api/files'

const mockGetScrubResult = getScrubResult as ReturnType<typeof vi.fn>
const mockRunScrub = runScrub as ReturnType<typeof vi.fn>
const mockDownloadDiagnosticsExport = downloadDiagnosticsExport as ReturnType<typeof vi.fn>
const mockListBackupJobs = listBackupJobs as ReturnType<typeof vi.fn>
const mockRunBackupJob = runBackupJob as ReturnType<typeof vi.fn>
const mockCheckBackupRetentionJob = checkBackupRetentionJob as ReturnType<typeof vi.fn>
const mockRunBackupRestoreDrill = runBackupRestoreDrill as ReturnType<typeof vi.fn>
const mockPreviewBackupRestoreJob = previewBackupRestoreJob as ReturnType<typeof vi.fn>
const mockPreviewBatchBackupRestore = previewBatchBackupRestore as ReturnType<typeof vi.fn>
const mockRestoreBackupJob = restoreBackupJob as ReturnType<typeof vi.fn>
const mockRunBatchBackupRestore = runBatchBackupRestore as ReturnType<typeof vi.fn>
const mockVerifyBackupRestoreJob = verifyBackupRestoreJob as ReturnType<typeof vi.fn>
const mockDownloadBackupRestoreReport = downloadBackupRestoreReport as ReturnType<typeof vi.fn>

function expectCalledWithAbortSignal(mockFn: ReturnType<typeof vi.fn>) {
  const call = mockFn.mock.calls.find(([options]) => {
    return (options as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
  })
  expect(call).toBeTruthy()
  expect(Object.keys((call?.[0] ?? {}) as Record<string, unknown>).sort()).toEqual(['signal'])
}

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

function selectBatchRestoreJob(name: string) {
  fireEvent.click(screen.getByRole('checkbox', { name }))
}

describe('MaintenancePage', () => {
  const mockCompletedResult = {
    has_result: true,
    status: 'completed',
    id: 'scrub-123',
    start_time: '2024-01-15T10:00:00Z',
    duration_ms: 5000,
    total_objects: 1000,
    valid_objects: 1000,
    corrupted_objects: 0,
    missing_objects: 0,
    total_size: 5368709120,
    errors: [],
  }

  const mockRunningResult = {
    has_result: true,
    status: 'running',
    id: 'scrub-124',
    start_time: '2024-01-15T11:00:00Z',
    total_objects: 1000,
    valid_objects: 450,
    corrupted_objects: 0,
    missing_objects: 0,
  }

  const mockResultWithErrors = {
    has_result: true,
    status: 'completed',
    id: 'scrub-125',
    start_time: '2024-01-15T10:00:00Z',
    duration_ms: 6000,
    total_objects: 1000,
    valid_objects: 995,
    corrupted_objects: 3,
    missing_objects: 2,
    errors: [
      { hash: 'abc123def456', error_type: 'corrupted', message: 'object failed integrity verification' },
      { hash: 'xyz789ghi012', error_type: 'missing', message: 'object is missing' },
    ],
  }

  const mockNoResult = {
    has_result: false,
  }

  const mockIncompleteResult = {
    has_result: true,
    status: 'completed',
    id: 'scrub-126',
    errors: [],
  }

  const mockBackupJobs = [{
    id: 'external-disk',
    name: '外置硬盘备份',
    type: 'local',
    source: '/srv/mnemonas',
    destination: '/mnt/backup-drive/mnemonas',
    disabled: false,
    schedule_interval: '24h0m0s',
    schedule_window_start: '02:00',
    schedule_window_end: '05:00',
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
    restore_drill_stats: {
      total_runs: 2,
      successful_runs: 1,
      failed_runs: 1,
      success_rate: 0.5,
      consecutive_successes: 1,
      latest_success_at: '2026-05-09T03:00:01Z',
      latest_failure_at: '2026-05-08T03:00:01Z',
      last_failure_message: 'manifest missing',
      last_failure_category: 'integrity_check',
    },
    include_config: true,
    verify_after_backup: true,
    exclude: ['.mnemonas/thumbnails'],
    running: false,
    last_run: {
      id: '20260509T020304.000000000Z',
      job_id: 'external-disk',
      status: 'completed',
      started_at: '2026-05-09T02:03:04Z',
      finished_at: '2026-05-09T02:03:05Z',
      duration_ms: 1000,
      source: '/srv/mnemonas',
      destination: '/mnt/backup-drive/mnemonas',
      snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
      manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
      file_count: 12,
      total_bytes: 4096,
      config_included: true,
      trigger: 'scheduled',
      warning: false,
      warnings: [],
      pruned_snapshots: 1,
    },
    last_successful_run: {
      id: '20260509T020304.000000000Z',
      job_id: 'external-disk',
      status: 'completed',
      started_at: '2026-05-09T02:03:04Z',
      finished_at: '2026-05-09T02:03:05Z',
      duration_ms: 1000,
      source: '/srv/mnemonas',
      destination: '/mnt/backup-drive/mnemonas',
      snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
      manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
      file_count: 12,
      total_bytes: 4096,
      config_included: true,
      trigger: 'scheduled',
      warning: false,
      warnings: [],
      pruned_snapshots: 1,
    },
    last_restore_drill: {
      id: '20260509T030000.000000000Z',
      job_id: 'external-disk',
      status: 'completed',
      started_at: '2026-05-09T03:00:00Z',
      finished_at: '2026-05-09T03:00:01Z',
      duration_ms: 1000,
      snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
      manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
      artifact_kept: false,
      file_count: 12,
      verified_bytes: 4096,
      warning: false,
      warnings: [],
    },
    restore_drill_history: [{
      id: '20260509T030000.000000000Z',
      job_id: 'external-disk',
      status: 'completed',
      started_at: '2026-05-09T03:00:00Z',
      finished_at: '2026-05-09T03:00:01Z',
      duration_ms: 1000,
      snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
      manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
      artifact_kept: false,
      file_count: 12,
      verified_bytes: 4096,
      warning: false,
      warnings: [],
    }, {
      id: '20260508T030000.000000000Z',
      job_id: 'external-disk',
      status: 'failed',
      started_at: '2026-05-08T03:00:00Z',
      finished_at: '2026-05-08T03:00:01Z',
      duration_ms: 1000,
      artifact_kept: false,
      file_count: 0,
      verified_bytes: 0,
      warning: false,
      warnings: [],
      error_message: 'manifest missing',
      failure_category: 'integrity_check',
    }],
    last_restore: {
      id: '20260509T040000.000000000Z',
      job_id: 'external-disk',
      status: 'completed',
      started_at: '2026-05-09T04:00:00Z',
      finished_at: '2026-05-09T04:00:01Z',
      duration_ms: 1000,
      snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
      manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
      target_path: '/restore/mnemonas',
      config_restored: true,
      config_path: '/restore/mnemonas/.mnemonas-restore/config.toml',
      file_count: 12,
      verified_bytes: 4096,
    },
    last_restore_verify: {
      id: '20260509T040005.000000000Z',
      job_id: 'external-disk',
      status: 'completed',
      started_at: '2026-05-09T04:00:05Z',
      finished_at: '2026-05-09T04:00:06Z',
      duration_ms: 1000,
      source: '/srv/mnemonas',
      destination: '/mnt/backup-drive/mnemonas',
      snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
      manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
      target_path: '/restore/mnemonas',
      file_count: 12,
      verified_bytes: 4096,
      config_found: true,
      files_dir_found: true,
      internal_dir_found: true,
      index_found: true,
      objects_dir_found: true,
      looks_like_storage_root: true,
      warnings: [],
    },
    last_matching_restore_verify: {
      id: '20260509T040005.000000000Z',
      job_id: 'external-disk',
      status: 'completed',
      started_at: '2026-05-09T04:00:05Z',
      finished_at: '2026-05-09T04:00:06Z',
      duration_ms: 1000,
      source: '/srv/mnemonas',
      destination: '/mnt/backup-drive/mnemonas',
      snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
      manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
      target_path: '/restore/mnemonas',
      file_count: 12,
      verified_bytes: 4096,
      config_found: true,
      files_dir_found: true,
      internal_dir_found: true,
      index_found: true,
      objects_dir_found: true,
      looks_like_storage_root: true,
      warnings: [],
    },
    restore_history: [{
      id: '20260509T040000.000000000Z',
      job_id: 'external-disk',
      status: 'completed',
      started_at: '2026-05-09T04:00:00Z',
      finished_at: '2026-05-09T04:00:01Z',
      duration_ms: 1000,
      snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
      manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
      target_path: '/restore/mnemonas',
      config_restored: true,
      config_path: '/restore/mnemonas/.mnemonas-restore/config.toml',
      file_count: 12,
      verified_bytes: 4096,
    }],
    last_retention_check: {
      id: '20260509T041000.000000000Z',
      job_id: 'external-disk',
      status: 'completed',
      started_at: '2026-05-09T04:10:00Z',
      finished_at: '2026-05-09T04:10:01Z',
      duration_ms: 1000,
      target: '/mnt/backup-drive/mnemonas',
      snapshot_count: 3,
      warning: false,
      warnings: [],
    },
  }]

  beforeEach(() => {
    vi.clearAllMocks()
    mockUser.id = 'u1'
    mockUser.username = 'admin'
    mockUser.role = 'admin'
    mockUser.email = 'admin@local'
    mockUser.homeDir = '/'
    window.history.pushState({}, '', '/maintenance')
    mockGetScrubResult.mockResolvedValue(mockCompletedResult)
    mockRunScrub.mockResolvedValue(mockCompletedResult)
    mockDownloadDiagnosticsExport.mockResolvedValue(undefined)
    mockListBackupJobs.mockResolvedValue([])
    mockRunBackupJob.mockResolvedValue(mockBackupJobs[0].last_run)
    mockCheckBackupRetentionJob.mockResolvedValue(mockBackupJobs[0].last_retention_check)
    mockRunBackupRestoreDrill.mockResolvedValue(mockBackupJobs[0].last_restore_drill)
    mockPreviewBackupRestoreJob.mockResolvedValue({
      id: '20260509T035900.000000000Z',
      job_id: 'external-disk',
      status: 'completed',
      started_at: '2026-05-09T03:59:00Z',
      finished_at: '2026-05-09T03:59:01Z',
      duration_ms: 1000,
      source: '/srv/mnemonas',
      destination: '/mnt/backup-drive/mnemonas',
      snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
      manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
      target_path: '/restore/mnemonas',
      file_count: 12,
      total_bytes: 4096,
      config_available: true,
      config_included: true,
      sample_paths: ['docs/note.txt', '.mnemonas-restore/config.toml'],
      preflight_checks: [{
        id: 'target_scope',
        status: 'passed',
        title: '目标路径隔离',
        detail: '目标目录位于受保护路径之外。',
      }],
      warnings: [],
      cutover_checklist: ['校验恢复目录'],
      rollback_checklist: ['指回原 storage.root'],
    })
    mockPreviewBatchBackupRestore.mockResolvedValue({
      id: '20260509T035901.000000000Z',
      status: 'completed',
      started_at: '2026-05-09T03:59:01Z',
      finished_at: '2026-05-09T03:59:02Z',
      duration_ms: 1000,
      total_files: 12,
      total_bytes: 4096,
      warning: false,
      warnings: [],
      items: [{
        index: 0,
        job_id: 'external-disk',
        target_path: '/restore/batch',
        include_config: true,
        status: 'completed',
        preview: {
          id: '20260509T035900.000000000Z',
          job_id: 'external-disk',
          status: 'completed',
          started_at: '2026-05-09T03:59:00Z',
          finished_at: '2026-05-09T03:59:01Z',
          duration_ms: 1000,
          source: '/srv/mnemonas',
          destination: '/mnt/backup-drive/mnemonas',
          target_path: '/restore/batch',
          file_count: 12,
          total_bytes: 4096,
          config_available: true,
          config_included: true,
          preflight_checks: [{
            id: 'target_scope',
            status: 'passed',
            title: '目标路径隔离',
            detail: '目标目录位于受保护路径之外。',
          }],
          warnings: [],
        },
      }],
    })
    mockRestoreBackupJob.mockResolvedValue({
      id: '20260509T040000.000000000Z',
      job_id: 'external-disk',
      status: 'completed',
      started_at: '2026-05-09T04:00:00Z',
      finished_at: '2026-05-09T04:00:01Z',
      duration_ms: 1000,
      snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
      manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
      target_path: '/restore/mnemonas',
      config_restored: true,
      config_path: '/restore/mnemonas/.mnemonas-restore/config.toml',
      file_count: 12,
      verified_bytes: 4096,
      preflight_checks: [{
        id: 'target_scope',
        status: 'passed',
        title: '目标路径隔离',
        detail: '目标目录位于受保护路径之外。',
      }],
      warnings: [],
      cutover_checklist: ['校验恢复目录'],
      rollback_checklist: ['指回原 storage.root'],
    })
    mockRunBatchBackupRestore.mockResolvedValue({
      id: '20260509T040001.000000000Z',
      status: 'completed',
      started_at: '2026-05-09T04:00:01Z',
      finished_at: '2026-05-09T04:00:02Z',
      duration_ms: 1000,
      total_files: 12,
      verified_bytes: 4096,
      warning: false,
      warnings: [],
      items: [{
        index: 0,
        job_id: 'external-disk',
        target_path: '/restore/batch',
        include_config: true,
        status: 'completed',
        restore: {
          id: '20260509T040000.000000000Z',
          job_id: 'external-disk',
          status: 'completed',
          started_at: '2026-05-09T04:00:00Z',
          finished_at: '2026-05-09T04:00:01Z',
          duration_ms: 1000,
          target_path: '/restore/batch',
          config_restored: true,
          config_path: '/restore/batch/.mnemonas-restore/config.toml',
          file_count: 12,
          verified_bytes: 4096,
          warnings: [],
        },
        verify: {
          id: '20260509T040005.000000000Z',
          job_id: 'external-disk',
          status: 'completed',
          started_at: '2026-05-09T04:00:05Z',
          finished_at: '2026-05-09T04:00:06Z',
          duration_ms: 1000,
          source: '/srv/mnemonas',
          destination: '/mnt/backup-drive/mnemonas',
          snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
          manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
          target_path: '/restore/batch',
          file_count: 12,
          verified_bytes: 4096,
          config_path: '/restore/batch/.mnemonas-restore/config.toml',
          config_found: true,
          files_dir_found: true,
          internal_dir_found: true,
          index_found: true,
          objects_dir_found: true,
          looks_like_storage_root: true,
          warnings: [],
        },
        warnings: [],
      }],
    })
    mockDownloadBackupRestoreReport.mockResolvedValue(undefined)
    mockVerifyBackupRestoreJob.mockResolvedValue({
      id: '20260509T040005.000000000Z',
      job_id: 'external-disk',
      status: 'completed',
      started_at: '2026-05-09T04:00:05Z',
      finished_at: '2026-05-09T04:00:06Z',
      duration_ms: 1000,
      source: '/srv/mnemonas',
      destination: '/mnt/backup-drive/mnemonas',
      snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
      manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
      target_path: '/restore/mnemonas',
      file_count: 12,
      verified_bytes: 4096,
      config_path: '/restore/mnemonas/.mnemonas-restore/config.toml',
      config_found: true,
      files_dir_found: true,
      internal_dir_found: true,
      index_found: true,
      objects_dir_found: true,
      looks_like_storage_root: true,
      warnings: [],
    })
  })

  afterEach(() => {
    if (originalClipboardDescriptor) {
      Object.defineProperty(navigator, 'clipboard', originalClipboardDescriptor)
    } else {
      Reflect.deleteProperty(navigator, 'clipboard')
    }
  })

  it('passes abort signals to maintenance queries', async () => {
    render(<Maintenance />)

    await waitFor(() => {
      expectCalledWithAbortSignal(mockGetScrubResult)
      expectCalledWithAbortSignal(mockListBackupJobs)
    })
  })

  describe('request cancellation', () => {
    it('aborts pending scrub when the page unmounts and ignores abort feedback', async () => {
      const user = userEvent.setup()
      const scrubRequest = createDeferred<unknown>()
      let signal: AbortSignal | undefined
      mockGetScrubResult.mockResolvedValue(mockNoResult)
      mockRunScrub.mockImplementationOnce((_hashes, options) => {
        signal = options?.signal
        return scrubRequest.promise
      })
      const view = render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('开始校验')).toBeTruthy()
      })

      await user.click(screen.getByText('开始校验'))

      await waitFor(() => {
        expect(signal).toBeInstanceOf(AbortSignal)
      })

      view.unmount()
      expect(signal?.aborted).toBe(true)
      await act(async () => {
        scrubRequest.reject(new DOMException('scrub aborted', 'AbortError'))
        await Promise.resolve()
      })
      expect(mockAddToast).not.toHaveBeenCalled()
    })

    it('aborts a pending backup run when the page unmounts and ignores abort feedback', async () => {
      const user = userEvent.setup()
      const backupRequest = createDeferred<unknown>()
      let signal: AbortSignal | undefined
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockRunBackupJob.mockImplementationOnce((_jobId, options) => {
        signal = options?.signal
        return backupRequest.promise
      })
      const view = render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /立即备份/ }))

      await waitFor(() => {
        expect(signal).toBeInstanceOf(AbortSignal)
      })

      view.unmount()
      expect(signal?.aborted).toBe(true)
      await act(async () => {
        backupRequest.reject(new DOMException('backup aborted', 'AbortError'))
        await Promise.resolve()
      })
      expect(mockAddToast).not.toHaveBeenCalled()
    })

    it('aborts a pending restore preview when the page unmounts and ignores abort feedback', async () => {
      const previewRequest = createDeferred<unknown>()
      let signal: AbortSignal | undefined
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockPreviewBackupRestoreJob.mockImplementationOnce((_jobId, _targetPath, _includeConfig, options) => {
        signal = options?.signal
        return previewRequest.promise
      })
      const view = render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore/mnemonas' } })
      fireEvent.click(screen.getByRole('button', { name: /生成预览/ }))

      await waitFor(() => {
        expect(signal).toBeInstanceOf(AbortSignal)
      })

      view.unmount()
      expect(signal?.aborted).toBe(true)
      await act(async () => {
        previewRequest.reject(new DOMException('restore preview aborted', 'AbortError'))
        await Promise.resolve()
      })
      expect(mockAddToast).not.toHaveBeenCalled()
    })

    it('aborts a pending restore when the page unmounts and ignores abort feedback', async () => {
      const restoreRequest = createDeferred<unknown>()
      let signal: AbortSignal | undefined
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockRestoreBackupJob.mockImplementationOnce((_jobId, _targetPath, _includeConfig, options) => {
        signal = options?.signal
        return restoreRequest.promise
      })
      const view = render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore/mnemonas' } })
      fireEvent.click(screen.getByRole('button', { name: /生成预览/ }))

      await waitFor(() => {
        expect(screen.getByText('预览已确认')).toBeTruthy()
      })
      mockAddToast.mockClear()
      fireEvent.click(screen.getByRole('button', { name: /开始恢复/ }))

      await waitFor(() => {
        expect(signal).toBeInstanceOf(AbortSignal)
      })

      view.unmount()
      expect(signal?.aborted).toBe(true)
      await act(async () => {
        restoreRequest.reject(new DOMException('restore aborted', 'AbortError'))
        await Promise.resolve()
      })
      expect(mockAddToast).not.toHaveBeenCalled()
    })

    it('aborts a pending restore verification when the page unmounts and ignores abort feedback', async () => {
      const verifyRequest = createDeferred<unknown>()
      let signal: AbortSignal | undefined
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockVerifyBackupRestoreJob.mockImplementationOnce((_jobId, _targetPath, options) => {
        signal = options?.signal
        return verifyRequest.promise
      })
      const view = render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore/mnemonas' } })
      fireEvent.click(screen.getByRole('button', { name: /生成预览/ }))

      await waitFor(() => {
        expect(screen.getByText('预览已确认')).toBeTruthy()
      })
      fireEvent.click(screen.getByRole('button', { name: /开始恢复/ }))

      await waitFor(() => {
        expect(signal).toBeInstanceOf(AbortSignal)
      })
      mockAddToast.mockClear()

      view.unmount()
      expect(signal?.aborted).toBe(true)
      await act(async () => {
        verifyRequest.reject(new DOMException('restore verify aborted', 'AbortError'))
        await Promise.resolve()
      })
      expect(mockAddToast).not.toHaveBeenCalled()
    })

    it('aborts a pending batch restore preview when the page unmounts and ignores abort feedback', async () => {
      const previewRequest = createDeferred<unknown>()
      let signal: AbortSignal | undefined
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockPreviewBatchBackupRestore.mockImplementationOnce((_items, options) => {
        signal = options?.signal
        return previewRequest.promise
      })
      const view = render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: '/restore/batch' } })
      fireEvent.click(screen.getByRole('button', { name: /生成批量预览/ }))

      await waitFor(() => {
        expect(signal).toBeInstanceOf(AbortSignal)
      })

      view.unmount()
      expect(signal?.aborted).toBe(true)
      await act(async () => {
        previewRequest.reject(new DOMException('batch restore preview aborted', 'AbortError'))
        await Promise.resolve()
      })
      expect(mockAddToast).not.toHaveBeenCalled()
    })

    it('aborts a pending batch restore when the page unmounts and ignores abort feedback', async () => {
      const restoreRequest = createDeferred<unknown>()
      let signal: AbortSignal | undefined
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockRunBatchBackupRestore.mockImplementationOnce((_items, options) => {
        signal = options?.signal
        return restoreRequest.promise
      })
      const view = render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: '/restore/batch' } })
      fireEvent.click(screen.getByRole('button', { name: /生成批量预览/ }))

      await waitFor(() => {
        expect(screen.getByText('批量预览结果')).toBeTruthy()
      })
      mockAddToast.mockClear()
      fireEvent.click(screen.getByRole('button', { name: /开始批量恢复/ }))

      await waitFor(() => {
        expect(signal).toBeInstanceOf(AbortSignal)
      })

      view.unmount()
      expect(signal?.aborted).toBe(true)
      await act(async () => {
        restoreRequest.reject(new DOMException('batch restore aborted', 'AbortError'))
        await Promise.resolve()
      })
      expect(mockAddToast).not.toHaveBeenCalled()
    })
  })

  describe('header', () => {
    it('displays page title', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('备份与维护')).toBeTruthy()
      })
    })

    it('displays subtitle', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('检查数据完整性，执行备份和恢复演练')).toBeTruthy()
      })
    })

    it('renders export button', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('下载诊断包')).toBeTruthy()
      })
    })

    it('refetches scrub history when the auth scope changes', async () => {
    mockGetScrubResult
      .mockResolvedValueOnce(mockCompletedResult)
      .mockResolvedValueOnce({
        ...mockCompletedResult,
        id: 'scrub-999',
      })

    const { rerender } = render(<Maintenance />)

    await waitFor(() => {
      expect(mockGetScrubResult).toHaveBeenCalledTimes(1)
    })

    mockUser.id = 'u2'
    mockUser.username = 'other-admin'
    mockUser.email = 'other@local'

    rerender(<Maintenance />)

    await waitFor(() => {
      expect(mockGetScrubResult).toHaveBeenCalledTimes(2)
    })
    })
  })

  describe('no previous scrub', () => {
    beforeEach(() => {
      mockGetScrubResult.mockResolvedValue(mockNoResult)
    })

    it('shows empty state message', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('尚未执行过数据校验')).toBeTruthy()
      })
    })

    it('shows start scrub button', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('开始校验')).toBeTruthy()
      })
    })
  })

  describe('backup jobs', () => {
    it('shows empty backup configuration state', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('尚未配置备份任务')).toBeTruthy()
        expect(screen.getByText('外置盘本地备份示例')).toBeTruthy()
        const starterSnippet = screen.getByText(/\[\[backup\.jobs\]\]/)
        const starterSnippetBlock = starterSnippet.closest('pre')
        expect(starterSnippetBlock).toHaveClass('whitespace-pre-wrap')
        expect(starterSnippetBlock).toHaveClass('break-words')
        expect(starterSnippetBlock).toHaveClass('overflow-x-hidden')
        expect(starterSnippet.textContent).toContain('destination = "/mnt/backup-drive/mnemonas"')
        expect(starterSnippet.textContent).toContain('verify_after_backup = true')
        expect(screen.getByText(/不要把备份目标放在 storage\.root 内/)).toBeTruthy()
      })
    })

    it('shows configured backup jobs and latest results', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
        expect(screen.getByText('/mnt/backup-drive/mnemonas')).toBeTruthy()
        expect(screen.getByText('每 1 天')).toBeTruthy()
        expect(screen.getByText('自动窗口：02:00-05:00')).toBeTruthy()
        expect(screen.getByText('最多 7 个快照 · 最长 30 天')).toBeTruthy()
        expect(screen.getByText(/最近检测：3 个快照/)).toBeTruthy()
        expect(screen.getByText('健康')).toBeTruthy()
        expect(screen.getAllByText(/12 个文件 · 4 KB/).length).toBeGreaterThan(0)
        expect(screen.getByText('校验 12 个文件 · 4 KB')).toBeTruthy()
        expect(screen.getByText('恢复演练仍在预期窗口内')).toBeTruthy()
        expect(screen.getByText('近 2 次成功率 50% · 连续成功 1 次')).toBeTruthy()
        expect(screen.getByText('最近一次备份成功完成。')).toBeTruthy()
        expect(screen.getByText('最近失败：清单文件缺失')).toBeTruthy()
        expect(screen.getByText('失败类型：完整性校验失败')).toBeTruthy()
        expect(screen.getByText('最近演练记录')).toBeTruthy()
        expect(screen.getByText('近 2 次包含 1 次失败')).toBeTruthy()
        expect(screen.getByText('目标：/restore/mnemonas')).toBeTruthy()
        expect(screen.getByText('最近检查：检查 12 个文件 · 4 KB')).toBeTruthy()
        expect(screen.getByText('已校验')).toBeTruthy()
        expect(screen.getAllByText('对照快照 20260509T020304.000000000Z').length).toBeGreaterThan(0)
      })
      expect(screen.queryByText('last successful backup completed recently')).toBeNull()
      expect(screen.queryByText('最近失败：manifest missing')).toBeNull()
    })

    it('formats backup duration labels without exposing raw unknown values', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        schedule_interval: '1h30m0s',
        max_age: 'backend_raw_max_age',
        restore_drill_stale_after: 'backend_raw_restore_drill_stale_after',
        last_restore_drill: undefined,
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('每 1 小时 30 分钟')).toBeTruthy()
        expect(screen.getByText('最多 7 个快照 · 最长 未知时长')).toBeTruthy()
        expect(screen.getByText('提醒周期：未知时长')).toBeTruthy()
      })
      expect(screen.queryByText(/backend_raw_max_age/)).toBeNull()
      expect(screen.queryByText(/backend_raw_restore_drill_stale_after/)).toBeNull()
    })

    it('does not expose raw unknown backup history statuses', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        restore_drill_history: [
          mockBackupJobs[0].last_restore_drill,
          {
            ...mockBackupJobs[0].last_restore_drill,
            id: 'unknown-drill-status',
            status: 'backend_raw_drill_status',
            started_at: '2026-05-08T03:00:00Z',
            finished_at: '2026-05-08T03:00:01Z',
          },
        ],
        restore_history: [
          mockBackupJobs[0].last_restore,
          {
            ...mockBackupJobs[0].last_restore,
            id: 'unknown-restore-status',
            status: 'backend_raw_restore_status',
            started_at: '2026-05-08T04:00:00Z',
            finished_at: '2026-05-08T04:00:01Z',
            target_path: '/restore/unknown-status',
          },
        ],
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getAllByText('未知状态').length).toBeGreaterThanOrEqual(2)
        expect(screen.getByText('目标：/restore/unknown-status')).toBeTruthy()
      })
      expect(screen.queryByText('backend_raw_drill_status')).toBeNull()
      expect(screen.queryByText('backend_raw_restore_status')).toBeNull()
    })

    it('does not expose raw unknown backup health or policy statuses', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        health_status: 'backend_raw_health_status',
        retention_status: 'backend_raw_retention_status',
        restore_drill_status: 'backend_raw_restore_policy_status',
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getAllByText('未知状态').length).toBeGreaterThanOrEqual(3)
      })
      expect(screen.queryByText('backend_raw_health_status')).toBeNull()
      expect(screen.queryByText('backend_raw_retention_status')).toBeNull()
      expect(screen.queryByText('backend_raw_restore_policy_status')).toBeNull()
    })

    it('does not mislabel unknown backup triggers as manual', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        last_run: {
          ...mockBackupJobs[0].last_run,
          trigger: 'backend_raw_trigger',
        },
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText(/未知触发方式 · 12 个文件 · 4 KB/)).toBeTruthy()
      })
      expect(screen.queryByText(/手动 · 12 个文件 · 4 KB/)).toBeNull()
      expect(screen.queryByText('backend_raw_trigger')).toBeNull()
    })

    it('marks the backup job referenced by the security check query', async () => {
      window.history.pushState({}, '', '/maintenance?backupJob=external-disk')
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('安全自检定位')).toBeTruthy()
      })

      const focusedJob = screen.getByRole('group', { name: '外置硬盘备份 备份任务，安全自检定位' })
      expect(within(focusedJob).getByText('安全自检定位')).toBeTruthy()
      expect(focusedJob).toHaveAttribute('data-focused-backup-job', 'true')
    })

    it('localizes restore drill summary messages from backend keys', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        restore_drill_status: 'stale',
        restore_drill_message: 'restore drill stale',
        last_restore_drill: undefined,
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('恢复演练已过期')).toBeTruthy()
        expect(screen.getByText('演练过期')).toBeTruthy()
      })
      expect(screen.queryByText('restore drill stale')).toBeNull()
    })

    it('shows stale restore drill and retention warnings', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        max_snapshots: undefined,
        max_age: undefined,
        retention_status: 'warning',
        retention_message: '本地快照未配置自动清理',
        restore_drill_status: 'stale',
        restore_drill_message: '恢复演练已过期',
        last_restore_drill_reminder_at: '2026-05-10T03:00:00Z',
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('本地快照未配置自动清理')).toBeTruthy()
        expect(screen.getByText('恢复演练已过期')).toBeTruthy()
        expect(screen.getByText('演练过期')).toBeTruthy()
        expect(screen.getByText(/最近提醒：/)).toBeTruthy()
      })
    })

    it('shows attention reasons for warning-only backup jobs', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        id: 'warning-only',
        name: '有警告的备份',
        last_run: {
          ...mockBackupJobs[0].last_run,
          warning: true,
          warnings: ['backup completed with warnings'],
        },
        last_restore: {
          ...mockBackupJobs[0].last_restore,
          warnings: ['restore completed with warnings'],
        },
      }])

      render(<Maintenance />)

      await waitFor(() => {
        const attention = screen.getByLabelText('待处理原因：最近备份有警告；最近恢复有警告')
        expect(within(attention).getByText('需处理')).toBeTruthy()
        expect(within(attention).getByText('最近备份有警告、最近恢复有警告')).toBeTruthy()
      })
    })

    it('shows running and failed retention check states without content metrics', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        id: 'running-retention',
        name: '运行中保留检测',
        last_retention_check: {
          id: 'retention-running',
          job_id: 'running-retention',
          status: 'running',
          started_at: '2026-05-09T04:10:00Z',
          duration_ms: 0,
          target: '/mnt/backup-drive/mnemonas',
        },
      }, {
        ...mockBackupJobs[0],
        id: 'failed-retention',
        name: '失败保留检测',
        last_retention_check: {
          id: 'retention-failed',
          job_id: 'failed-retention',
          status: 'failed',
          started_at: '2026-05-09T04:10:00Z',
          finished_at: '2026-05-09T04:10:01Z',
          duration_ms: 1000,
          target: '/mnt/backup-drive/mnemonas',
          error_message: 'check command failed',
        },
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText(/最近检测：检测中/)).toBeTruthy()
        expect(screen.getByText(/最近检测：检测失败：检测命令执行失败/)).toBeTruthy()
        expect(screen.queryByText(/最近检测：未发现可恢复内容/)).toBeNull()
      })
      expect(screen.queryByText(/check command failed/)).toBeNull()
    })

    it('does not summarize failed backup task states as completed commands', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        last_run: {
          ...mockBackupJobs[0].last_run,
          status: 'failed',
          finished_at: '2026-05-09T02:03:05Z',
          snapshot_path: undefined,
          manifest_path: undefined,
          file_count: 0,
          total_bytes: 0,
          trigger: 'manual',
          error_message: 'disk full',
        },
        last_restore_drill: {
          ...mockBackupJobs[0].last_restore_drill,
          status: 'failed',
          finished_at: '2026-05-09T03:00:01Z',
          snapshot_path: undefined,
          manifest_path: undefined,
          restored_path: undefined,
          file_count: 0,
          verified_bytes: 0,
          error_message: 'manifest missing',
        },
        restore_drill_message: '恢复演练未通过',
        last_restore: {
          ...mockBackupJobs[0].last_restore,
          status: 'failed',
          snapshot_path: undefined,
          manifest_path: undefined,
          config_restored: false,
          config_path: undefined,
          file_count: 0,
          verified_bytes: 0,
          error_message: 'restore failed',
        },
        last_restore_verify: {
          ...mockBackupJobs[0].last_restore_verify,
          status: 'failed',
          file_count: 0,
          verified_bytes: 0,
          config_path: undefined,
          config_found: false,
          files_dir_found: false,
          internal_dir_found: false,
          index_found: false,
          objects_dir_found: false,
          looks_like_storage_root: false,
          error_message: 'verify failed',
        },
        last_matching_restore_verify: undefined,
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText(/手动 · 备份任务失败/)).toBeTruthy()
        expect(screen.getByText('恢复演练失败')).toBeTruthy()
        expect(screen.getByText('恢复任务失败')).toBeTruthy()
        expect(screen.queryByText('外部备份命令已完成')).toBeNull()
        expect(screen.queryByText('校验命令已完成')).toBeNull()
        expect(screen.queryByText('恢复命令已完成')).toBeNull()
        expect(screen.queryByText(/最近检查：/)).toBeNull()
        expect(screen.queryByText(/最近检查：目标目录已检查/)).toBeNull()
      })
    })

    it('shows interrupted backup task states after service restart', async () => {
      const interrupted = '任务在服务重启或进程退出前中断'
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        health_status: 'failed',
        health_message: '最近一次手动备份失败',
        retention_status: 'failed',
        retention_message: '保留策略检测失败',
        restore_drill_status: 'failed',
        restore_drill_message: '最近一次恢复演练失败',
        last_run: {
          ...mockBackupJobs[0].last_run,
          status: 'failed',
          finished_at: '2026-05-09T02:03:05Z',
          snapshot_path: undefined,
          manifest_path: undefined,
          file_count: 0,
          total_bytes: 0,
          trigger: 'manual',
          error_message: interrupted,
        },
        last_restore_drill: {
          ...mockBackupJobs[0].last_restore_drill,
          status: 'failed',
          finished_at: '2026-05-09T03:00:01Z',
          snapshot_path: undefined,
          manifest_path: undefined,
          restored_path: undefined,
          artifact_kept: false,
          file_count: 0,
          verified_bytes: 0,
          failure_category: 'cancelled',
          error_message: interrupted,
        },
        restore_drill_history: [{
          ...mockBackupJobs[0].last_restore_drill,
          id: 'interrupted-drill',
          status: 'failed',
          finished_at: '2026-05-09T03:00:01Z',
          snapshot_path: undefined,
          manifest_path: undefined,
          restored_path: undefined,
          artifact_kept: false,
          file_count: 0,
          verified_bytes: 0,
          failure_category: 'cancelled',
          error_message: interrupted,
        }],
        restore_drill_stats: {
          total_runs: 1,
          successful_runs: 0,
          failed_runs: 1,
          success_rate: 0,
          consecutive_failures: 1,
          latest_failure_at: '2026-05-09T03:00:01Z',
          last_failure_message: interrupted,
          last_failure_category: 'cancelled',
        },
        last_restore: {
          ...mockBackupJobs[0].last_restore,
          status: 'failed',
          finished_at: '2026-05-09T04:00:01Z',
          snapshot_path: undefined,
          manifest_path: undefined,
          config_restored: false,
          config_path: undefined,
          file_count: 0,
          verified_bytes: 0,
          error_message: interrupted,
        },
        restore_history: [{
          ...mockBackupJobs[0].last_restore,
          id: 'interrupted-restore',
          status: 'failed',
          finished_at: '2026-05-09T04:00:01Z',
          snapshot_path: undefined,
          manifest_path: undefined,
          config_restored: false,
          config_path: undefined,
          file_count: 0,
          verified_bytes: 0,
          error_message: interrupted,
        }],
        last_retention_check: {
          id: 'retention-interrupted',
          job_id: 'external-disk',
          status: 'failed',
          started_at: '2026-05-09T04:10:00Z',
          finished_at: '2026-05-09T04:10:01Z',
          duration_ms: 1000,
          target: '/mnt/backup-drive/mnemonas',
          error_message: interrupted,
        },
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText(/手动 · 备份任务失败/)).toBeTruthy()
        expect(screen.getByText('恢复演练失败')).toBeTruthy()
        expect(screen.getByText('恢复任务失败')).toBeTruthy()
        expect(screen.getByText(/最近检测：检测失败：任务在服务重启或进程退出前中断/)).toBeTruthy()
        expect(screen.getByText('失败类型：任务被取消')).toBeTruthy()
        expect(screen.getByText('最近失败：任务在服务重启或进程退出前中断')).toBeTruthy()
        expect(screen.getAllByText(interrupted).length).toBeGreaterThanOrEqual(3)
        expect(screen.queryByText('外部备份命令已完成')).toBeNull()
        expect(screen.queryByText('校验命令已完成')).toBeNull()
        expect(screen.queryByText('恢复命令已完成')).toBeNull()
      })
    })

    it('shows a stable fallback for unknown restore drill failure categories', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        restore_drill_status: 'failed',
        restore_drill_message: 'restore drill failed',
        restore_drill_stats: undefined,
        last_restore_drill: {
          ...mockBackupJobs[0].last_restore_drill,
          status: 'failed',
          failure_category: 'backend_raw_drill_category',
          error_message: 'manifest missing',
        },
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('失败类型：未分类失败')).toBeTruthy()
      })
      expect(screen.queryByText('backend_raw_drill_category')).toBeNull()
    })

    it('shows latest restore drill warnings in the backup summary', async () => {
      const drillWarning = '恢复演练已完成，但临时恢复目录清理失败；请检查备份目标中的 restore-drills 目录。'
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        last_restore_drill: {
          ...mockBackupJobs[0].last_restore_drill,
          warning: true,
          warnings: [drillWarning],
        },
        restore_drill_history: [
          {
            ...mockBackupJobs[0].restore_drill_history![0],
            warning: true,
            warnings: [drillWarning],
          },
          mockBackupJobs[0].restore_drill_history![1],
        ],
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('完成（有警告）')).toBeTruthy()
        expect(screen.getByText(drillWarning)).toBeTruthy()
        expect(screen.getByText('近 2 次包含 1 次警告')).toBeTruthy()
      })
    })

    it('shows restore history details when multiple restores exist', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        restore_history: [
          mockBackupJobs[0].last_restore,
          {
            ...mockBackupJobs[0].last_restore,
            id: '20260508T040000.000000000Z',
            started_at: '2026-05-08T04:00:00Z',
            finished_at: '2026-05-08T04:00:01Z',
            target_path: '/restore/older',
            file_count: 3,
            verified_bytes: 2048,
          },
        ],
      }])

      render(<Maintenance />)

      await waitFor(() => {
        const history = within(screen.getByLabelText('最近恢复记录'))
        expect(history.getByText('最近恢复记录（2 条）')).toBeTruthy()
        expect(history.getByText('目标：/restore/mnemonas')).toBeTruthy()
        expect(history.getByText('12 个文件 · 4 KB')).toBeTruthy()
        expect(history.getByText('目标：/restore/older')).toBeTruthy()
        expect(history.getByText('3 个文件 · 2 KB')).toBeTruthy()
      })
    })

    it('shows next steps for backup attention reasons', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        health_status: 'failed',
        health_message: 'latest backup failed but a previous snapshot is available',
        retention_status: 'warning',
        retention_message: '远端保留策略需要确认',
        restore_drill_status: 'due',
        restore_drill_message: '需要执行恢复演练',
        last_run: {
          ...mockBackupJobs[0].last_run,
          id: '20260510T020304.000000000Z',
          status: 'failed',
          warning: false,
          warnings: [],
          error_message: 'backup failed',
        },
        last_restore: {
          ...mockBackupJobs[0].last_restore,
          status: 'completed',
          target_path: '/restore/pending',
        },
        last_matching_restore_verify: undefined,
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByLabelText('待处理原因：备份健康异常；最近备份失败；保留策略需确认；恢复演练待执行；恢复待校验')).toBeTruthy()
        const nextSteps = screen.getByLabelText('建议处理：运行立即备份并查看最近备份结果；运行检查保留并确认快照或远端保留策略；执行恢复演练并复核演练历史；运行检查恢复完成只读校验')
        expect(nextSteps).toHaveTextContent('建议：运行立即备份并查看最近备份结果、运行检查保留并确认快照或远端保留策略 等 4 步')
      })
    })

    it('shows restore report findings in the restore summary', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        restore_report_findings: [
          '最近一次显式恢复尚未完成匹配的只读校验。',
          '尚未持久化恢复后的只读校验报告。',
        ],
      }])

      render(<Maintenance />)

      await waitFor(() => {
        const summary = screen.getByText('摘要发现：最近一次显式恢复尚未完成匹配的只读校验。 等 2 项')
        expect(summary).toBeTruthy()
        expect(summary).toHaveAttribute('title', '最近一次显式恢复尚未完成匹配的只读校验。\n尚未持久化恢复后的只读校验报告。')
      })
    })

    it('shows restore report findings when no restore has run', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        last_restore: undefined,
        last_restore_verify: undefined,
        last_matching_restore_verify: undefined,
        restore_history: undefined,
        restore_report_findings: ['尚未执行过显式恢复。'],
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('尚未恢复')).toBeTruthy()
        expect(screen.getByText('摘要发现：尚未执行过显式恢复。')).toBeTruthy()
      })
    })

    it('checks the latest restored target from the backup task list', async () => {
      const user = userEvent.setup()
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /检查恢复/ }))

      await waitFor(() => {
        expect(mockVerifyBackupRestoreJob).toHaveBeenCalledWith('external-disk', '/restore/mnemonas', expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '恢复目录检查完成',
          description: '检查 12 个文件，4 KB',
          color: 'success',
        }))
      })
    })

    it('shows latest restore warnings in the backup summary', async () => {
      const restoreWarning = '本地快照包含配置文件，但本次不会恢复；切换 storage.root 前请确认配置是否仍适配。'
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        last_restore: {
          ...mockBackupJobs[0].last_restore,
          warnings: [restoreWarning],
        },
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText(restoreWarning)).toBeTruthy()
        expect(screen.getByText('完成（有警告）')).toBeTruthy()
      })
    })

    it('shows latest restore verify warnings in the backup summary', async () => {
      const verifyWarning = '未同时检测到 files/ 和 .mnemonas/，仅在恢复的是子目录时才适合直接切换 storage.root'
      const matchingRestoreVerify = {
        ...mockBackupJobs[0].last_restore_verify,
        looks_like_storage_root: false,
        warnings: [verifyWarning],
      }
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        last_restore_verify: matchingRestoreVerify,
        last_matching_restore_verify: matchingRestoreVerify,
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText(verifyWarning)).toBeTruthy()
        expect(screen.getByText(/最近检查：检查 12 个文件/)).toBeTruthy()
        expect(screen.getByText('检查有警告')).toBeTruthy()
      })
    })

    it('shows a running restore verify that belongs to the latest restore', async () => {
      const matchingRestoreVerify = {
        ...mockBackupJobs[0].last_restore_verify,
        status: 'running',
        started_at: '2026-05-09T04:00:05Z',
        finished_at: undefined,
        file_count: 0,
        verified_bytes: 0,
        snapshot_path: undefined,
        manifest_path: undefined,
        warnings: undefined,
      }
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        last_restore_verify: matchingRestoreVerify,
        last_matching_restore_verify: matchingRestoreVerify,
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('最近检查：恢复目录检查中')).toBeTruthy()
        expect(screen.getByText('检查中')).toBeTruthy()
        expect(screen.queryByText('最近恢复尚未完成匹配的只读校验')).toBeNull()
      })
    })

    it('does not attach stale restore verify results to the latest restore summary', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        last_restore: {
          ...mockBackupJobs[0].last_restore,
          id: '20260509T050000.000000000Z',
          started_at: '2026-05-09T05:00:00Z',
          finished_at: '2026-05-09T05:00:01Z',
          target_path: '/restore/new',
        },
        last_restore_verify: {
          ...mockBackupJobs[0].last_restore_verify,
          id: '20260509T040005.000000000Z',
          target_path: '/restore/old',
          warnings: ['stale verify warning'],
        },
        last_matching_restore_verify: undefined,
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('目标：/restore/new')).toBeTruthy()
        expect(screen.getByText('最近恢复尚未完成匹配的只读校验')).toBeTruthy()
        expect(screen.getByText('待校验')).toBeTruthy()
        expect(screen.queryByText(/最近检查：/)).toBeNull()
        expect(screen.queryByText('stale verify warning')).toBeNull()
      })
    })

    it('keeps local restore actions available after a failed latest backup when a successful snapshot exists', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        health_status: 'failed',
        health_message: 'latest backup failed but a previous snapshot is available',
        last_run: {
          ...mockBackupJobs[0].last_run,
          id: '20260510T020304.000000000Z',
          status: 'failed',
          started_at: '2026-05-10T02:03:04Z',
          finished_at: '2026-05-10T02:03:05Z',
          snapshot_path: undefined,
          manifest_path: undefined,
          file_count: 0,
          total_bytes: 0,
          trigger: 'scheduled',
          error_message: 'disk full',
        },
        last_successful_run: mockBackupJobs[0].last_successful_run,
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      expect((screen.getByRole('button', { name: /恢复演练/ }) as HTMLButtonElement).disabled).toBe(false)
      expect((screen.getByRole('button', { name: /^恢复$/ }) as HTMLButtonElement).disabled).toBe(false)
      expect((screen.getByRole('button', { name: /批量恢复/ }) as HTMLButtonElement).disabled).toBe(false)
    })

    it('disables backup actions for disabled jobs', async () => {
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        disabled: true,
        schedule_interval: undefined,
        next_run_at: undefined,
        health_status: 'disabled',
        health_message: 'backup job disabled',
      }])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('已停用')).toBeTruthy()
      })

      const runButton = screen.getByRole('button', { name: /立即备份/ }) as HTMLButtonElement
      const retentionButton = screen.getByRole('button', { name: /检查保留/ }) as HTMLButtonElement
      const drillButton = screen.getByRole('button', { name: /恢复演练/ }) as HTMLButtonElement
      expect(runButton.disabled).toBe(true)
      expect(retentionButton.disabled).toBe(true)
      expect(drillButton.disabled).toBe(true)
    })

    it('runs a backup job from the task list', async () => {
      const user = userEvent.setup()
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /立即备份/ }))

      await waitFor(() => {
        expect(mockRunBackupJob).toHaveBeenCalledWith('external-disk', expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '备份已完成' }))
      })
    })

    it('shows a normalized disabled-job warning when running a backup returns a conflict', async () => {
      const user = userEvent.setup()
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockRunBackupJob.mockRejectedValueOnce(new ApiError('  Backup Job DISABLED  ', 409))

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /立即备份/ }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '备份任务已停用',
          description: '请先在配置文件中启用该任务并重启服务。',
          color: 'warning',
        })
      })
    })

    it('runs a restore drill from the task list', async () => {
      const user = userEvent.setup()
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /恢复演练/ }))

      await waitFor(() => {
        expect(mockRunBackupRestoreDrill).toHaveBeenCalledWith('external-disk', false, expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '恢复演练已完成' }))
      })
    })

    it('shows warning toast when restore drill leaves cleanup warnings', async () => {
      const user = userEvent.setup()
      const drillWarning = '恢复演练已完成，但临时恢复目录清理失败；请检查备份目标中的 restore-drills 目录。'
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockRunBackupRestoreDrill.mockResolvedValueOnce({
        ...mockBackupJobs[0].last_restore_drill!,
        warning: true,
        warnings: [drillWarning],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /恢复演练/ }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '恢复演练完成，有警告',
          description: drillWarning,
          color: 'warning',
        })
      })
    })

    it('checks retention from the task list', async () => {
      const user = userEvent.setup()
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /检查保留/ }))

      await waitFor(() => {
        expect(mockCheckBackupRetentionJob).toHaveBeenCalledWith('external-disk', expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '保留策略检测完成' }))
      })
    })

    it('shows failed backup task results as danger toasts', async () => {
      const user = userEvent.setup()
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockRunBackupJob.mockResolvedValueOnce({
        ...mockBackupJobs[0].last_run,
        status: 'failed',
        warning: false,
        warnings: [],
        error_message: 'disk full',
      })
      mockCheckBackupRetentionJob.mockResolvedValueOnce({
        ...mockBackupJobs[0].last_retention_check,
        status: 'failed',
        warning: false,
        warnings: [],
        error_message: 'check command failed',
      })
      mockRunBackupRestoreDrill.mockResolvedValueOnce({
        ...mockBackupJobs[0].last_restore_drill,
        status: 'failed',
        file_count: 0,
        verified_bytes: 0,
        error_message: 'manifest missing',
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /立即备份/ }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '备份任务失败',
          description: '磁盘空间不足',
          color: 'danger',
        })
      })

      await user.click(screen.getByRole('button', { name: /检查保留/ }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '保留策略检测失败',
          description: '检测命令执行失败',
          color: 'danger',
        })
      })

      await user.click(screen.getByRole('button', { name: /恢复演练/ }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '恢复演练失败',
          description: '清单文件缺失',
          color: 'danger',
        })
      })
    })

    it('downloads a restore summary from the task list', async () => {
      const user = userEvent.setup()
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /导出摘要/ }))

      await waitFor(() => {
        expect(mockDownloadBackupRestoreReport).toHaveBeenCalledWith('external-disk', expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '恢复摘要导出已开始' }))
      })
    })

    it('downloads a restore summary from the completed restore dialog', async () => {
      const user = userEvent.setup()
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore/mnemonas' } })
      await user.click(screen.getByRole('button', { name: /生成预览/ }))

      await waitFor(() => {
        expect(screen.getByText('预览已确认')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /开始恢复/ }))

      await waitFor(() => {
        expect(screen.getByText('恢复已完成')).toBeTruthy()
      })

      mockDownloadBackupRestoreReport.mockClear()
      mockAddToast.mockClear()
      const exportButtons = screen.getAllByRole('button', { name: /导出摘要/ })
      await user.click(exportButtons[exportButtons.length - 1])

      await waitFor(() => {
        expect(mockDownloadBackupRestoreReport).toHaveBeenCalledWith('external-disk', expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '恢复摘要导出已开始' }))
      })
    })

    it('aborts pending restore summary export when the page unmounts and ignores abort feedback', async () => {
      const user = userEvent.setup()
      const exportRequest = createDeferred<void>()
      let signal: AbortSignal | undefined
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockDownloadBackupRestoreReport.mockImplementationOnce((_id, options) => {
        signal = options?.signal
        return exportRequest.promise
      })
      const view = render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /导出摘要/ }))

      await waitFor(() => {
        expect(signal).toBeInstanceOf(AbortSignal)
      })

      view.unmount()
      expect(signal?.aborted).toBe(true)
      exportRequest.reject(new DOMException('restore report export aborted', 'AbortError'))

      await waitFor(() => {
        expect(mockAddToast).not.toHaveBeenCalled()
      })
    })

    it('prefills a suggested restore target path', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /^恢复$/ }))

      await waitFor(() => {
        const targetInput = screen.getByLabelText('目标目录') as HTMLInputElement
        expect(targetInput.value).toBe('/mnt/restore/external-disk')
        expect(screen.getByText(/已填入建议目录/)).toBeTruthy()
        expect((screen.getByRole('button', { name: /生成预览/ }) as HTMLButtonElement).disabled).toBe(false)
        expect((screen.getByRole('button', { name: /开始恢复/ }) as HTMLButtonElement).disabled).toBe(true)
      })
    })

    it('restores a local backup from the task list', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /^恢复$/ }))

      await waitFor(() => {
        expect(screen.getByText('恢复备份到目录')).toBeTruthy()
      })

      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: ' /restore//mnemonas/ ' } })
      const restoreAction = screen.getByRole('button', { name: /开始恢复/ }) as HTMLButtonElement
      expect(restoreAction.disabled).toBe(true)
      fireEvent.click(screen.getByRole('button', { name: /生成预览/ }))

      await waitFor(() => {
        expect(mockPreviewBackupRestoreJob).toHaveBeenCalledWith('external-disk', '/restore/mnemonas', true, expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(screen.getByText('预览已确认')).toBeTruthy()
        const impact = within(screen.getByLabelText('恢复影响摘要'))
        expect(impact.getByText('目标状态')).toBeTruthy()
        expect(impact.getByText('冲突与覆盖')).toBeTruthy()
        expect(impact.getByText('权限影响')).toBeTruthy()
        expect(impact.getByText((content) => content.includes('配置文件会单独恢复到 .mnemonas-restore/config.toml'))).toBeTruthy()
        expect(impact.getByText('恢复后校验')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /开始恢复/ }))

      await waitFor(() => {
        expect(mockRestoreBackupJob).toHaveBeenCalledWith('external-disk', '/restore/mnemonas', true, expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockVerifyBackupRestoreJob).toHaveBeenCalledWith('external-disk', '/restore/mnemonas', expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '备份已恢复' }))
        expect(screen.getByText('恢复已完成')).toBeTruthy()
        expect(screen.getByText('切换准备')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /复制切换记录/ }))

      await waitFor(() => {
        expect(writeText).toHaveBeenCalledTimes(1)
      })
      const report = writeText.mock.calls[0]?.[0] as string
      expect(report).toContain('恢复切换记录')
      expect(report).toContain('任务 ID：external-disk')
      expect(report).toContain('恢复目标：/restore/mnemonas')
      expect(report).toContain('只读校验：检查完成；检查 12 个文件 · 4 KB')
      expect(report).toContain('切换步骤\n1. 校验恢复目录')
      expect(report).toContain('回滚清单\n1. 指回原 storage.root')
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '恢复切换记录已复制' }))
    })

    it('blocks restore preview when the target path is relative', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: 'relative/restore' } })

      expect(screen.getByText('恢复目标必须是服务器上的绝对路径，例如 /mnt/restore/mnemonas。')).toBeTruthy()
      const previewAction = screen.getByRole('button', { name: /生成预览/ }) as HTMLButtonElement
      expect(previewAction.disabled).toBe(true)

      fireEvent.click(previewAction)
      expect(mockPreviewBackupRestoreJob).not.toHaveBeenCalled()
    })

    it('blocks restore preview when the target path contains control characters', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore/\u0081bad' } })

      expect(screen.getByText('恢复目标不能包含控制字符。')).toBeTruthy()
      const previewAction = screen.getByRole('button', { name: /生成预览/ }) as HTMLButtonElement
      expect(previewAction.disabled).toBe(true)

      fireEvent.click(previewAction)
      expect(mockPreviewBackupRestoreJob).not.toHaveBeenCalled()
    })

    it('blocks restore preview when the target path contains dot segments', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore/../mnemonas' } })

      expect(screen.getByText('恢复目标不能包含 . 或 .. 路径段。')).toBeTruthy()
      const previewAction = screen.getByRole('button', { name: /生成预览/ }) as HTMLButtonElement
      expect(previewAction.disabled).toBe(true)

      fireEvent.click(previewAction)
      expect(mockPreviewBackupRestoreJob).not.toHaveBeenCalled()
    })

    it('blocks restore preview when an absolute target path contains backslashes', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore\\mnemonas' } })

      expect(screen.getByText('恢复目标不能包含反斜杠，请使用服务器上的 POSIX 绝对路径。')).toBeTruthy()
      const previewAction = screen.getByRole('button', { name: /生成预览/ }) as HTMLButtonElement
      expect(previewAction.disabled).toBe(true)

      fireEvent.click(previewAction)
      expect(mockPreviewBackupRestoreJob).not.toHaveBeenCalled()
    })

    it('blocks restore preview when the target path is a filesystem root', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/' } })

      expect(screen.getByText('恢复目标不能是文件系统根目录或受保护系统目录。')).toBeTruthy()
      const previewAction = screen.getByRole('button', { name: /生成预览/ }) as HTMLButtonElement
      expect(previewAction.disabled).toBe(true)

      fireEvent.click(previewAction)
      expect(mockPreviewBackupRestoreJob).not.toHaveBeenCalled()
    })

    it('keeps the normalized original restore target for preview matching and follow-up verification', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      const rawTarget = '/restore//token=restore-secret/'
      const originalTarget = '/restore/token=restore-secret'
      const redactedTarget = '/restore/token=<redacted>'
      mockPreviewBackupRestoreJob.mockResolvedValueOnce({
        id: '20260509T035900.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T03:59:00Z',
        finished_at: '2026-05-09T03:59:01Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: '/mnt/backup-drive/mnemonas',
        snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
        manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
        target_path: redactedTarget,
        file_count: 12,
        total_bytes: 4096,
        config_available: true,
        config_included: true,
        preflight_checks: [{
          id: 'target_scope',
          status: 'passed',
          title: '目标路径隔离',
          detail: '目标目录位于受保护路径之外。',
        }],
        warnings: [],
        cutover_checklist: ['校验恢复目录'],
        rollback_checklist: ['指回原 storage.root'],
      })
      mockRestoreBackupJob.mockResolvedValueOnce({
        id: '20260509T040000.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T04:00:00Z',
        finished_at: '2026-05-09T04:00:01Z',
        duration_ms: 1000,
        snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
        manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
        target_path: redactedTarget,
        config_restored: true,
        config_path: '/restore/token=<redacted>/.mnemonas-restore/config.toml',
        file_count: 12,
        verified_bytes: 4096,
        preflight_checks: [{
          id: 'target_scope',
          status: 'passed',
          title: '目标路径隔离',
          detail: '目标目录位于受保护路径之外。',
        }],
        warnings: [],
        cutover_checklist: ['校验恢复目录'],
        rollback_checklist: ['指回原 storage.root'],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: rawTarget } })
      fireEvent.click(screen.getByRole('button', { name: /生成预览/ }))

      await waitFor(() => {
        expect(mockPreviewBackupRestoreJob).toHaveBeenCalledWith('external-disk', originalTarget, true, expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(screen.getByText('预览已确认')).toBeTruthy()
      })

      const restoreAction = screen.getByRole('button', { name: /开始恢复/ }) as HTMLButtonElement
      expect(restoreAction.disabled).toBe(false)
      fireEvent.click(restoreAction)

      await waitFor(() => {
        expect(mockRestoreBackupJob).toHaveBeenCalledWith('external-disk', originalTarget, true, expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockVerifyBackupRestoreJob).toHaveBeenCalledWith('external-disk', originalTarget, expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
      })

      const cutoverChecklist = within(screen.getByLabelText('切换步骤确认进度'))
      expect(cutoverChecklist.getByText('已确认 0 / 1 项')).toBeTruthy()
      fireEvent.click(cutoverChecklist.getByLabelText('校验恢复目录'))
      expect(cutoverChecklist.getByText('已确认 1 / 1 项')).toBeTruthy()

      const rollbackChecklist = within(screen.getByLabelText('回滚清单确认进度'))
      expect(rollbackChecklist.getByText('已确认 0 / 1 项')).toBeTruthy()
      expect(rollbackChecklist.getByLabelText('指回原 storage.root')).toBeTruthy()

      mockVerifyBackupRestoreJob.mockClear()
      fireEvent.click(screen.getByRole('button', { name: /重新检查/ }))

      await waitFor(() => {
        expect(mockVerifyBackupRestoreJob).toHaveBeenCalledWith('external-disk', originalTarget, expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
      })
    })

    it('keeps restore warnings visible after a successful local restore', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      const restoreWarning = '本地快照包含配置文件，但本次不会恢复；切换 storage.root 前请确认配置是否仍适配。'
      mockPreviewBackupRestoreJob.mockResolvedValueOnce({
        id: '20260509T035900.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T03:59:00Z',
        finished_at: '2026-05-09T03:59:01Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: '/mnt/backup-drive/mnemonas',
        snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
        manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
        target_path: '/restore/mnemonas',
        file_count: 12,
        total_bytes: 4096,
        config_available: true,
        config_included: false,
        preflight_checks: [{
          id: 'config_restore',
          status: 'warning',
          title: '配置文件',
          detail: restoreWarning,
        }],
        warnings: [restoreWarning],
        cutover_checklist: ['校验恢复目录'],
        rollback_checklist: ['指回原 storage.root'],
      })
      mockRestoreBackupJob.mockResolvedValueOnce({
        id: '20260509T040000.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T04:00:00Z',
        finished_at: '2026-05-09T04:00:01Z',
        duration_ms: 1000,
        snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
        manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
        target_path: '/restore/mnemonas',
        config_restored: false,
        file_count: 12,
        verified_bytes: 4096,
        preflight_checks: [{
          id: 'config_restore',
          status: 'warning',
          title: '配置文件',
          detail: restoreWarning,
        }],
        warnings: [restoreWarning],
        cutover_checklist: ['校验恢复目录'],
        rollback_checklist: ['指回原 storage.root'],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore/mnemonas' } })
      fireEvent.click(screen.getByLabelText('同时恢复备份中的配置文件'))
      fireEvent.click(screen.getByRole('button', { name: /生成预览/ }))

      await waitFor(() => {
        expect(screen.getByText('预览已确认，有提醒')).toBeTruthy()
        expect(screen.getByText('完成（有警告）')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /开始恢复/ }))

      await waitFor(() => {
        expect(mockRestoreBackupJob).toHaveBeenCalledWith('external-disk', '/restore/mnemonas', false, expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '备份已恢复，有警告',
          description: restoreWarning,
          color: 'warning',
        }))
        expect(screen.getByText('恢复已完成，有警告')).toBeTruthy()
        expect(screen.getAllByText(restoreWarning).length).toBeGreaterThan(0)
      })
    })

    it('maps diagnostic restore warnings in the completion toast and summary', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockRestoreBackupJob.mockResolvedValueOnce({
        ...mockBackupJobs[0].last_restore,
        warnings: ['restore target already exists'],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore/mnemonas' } })
      fireEvent.click(screen.getByRole('button', { name: /生成预览/ }))

      await waitFor(() => {
        expect(screen.getByText('预览已确认')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /开始恢复/ }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '备份已恢复，有警告',
          description: '恢复目标已存在',
          color: 'warning',
        }))
        expect(screen.getByText('恢复已完成，有警告')).toBeTruthy()
        expect(screen.getByText('恢复目标已存在')).toBeTruthy()
      })
      expect(screen.queryByText('restore target already exists')).toBeNull()
    })

    it('does not start restore verification when a restore result reports failure', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockRestoreBackupJob.mockResolvedValueOnce({
        ...mockBackupJobs[0].last_restore,
        status: 'failed',
        file_count: 0,
        verified_bytes: 0,
        error_message: 'restore failed',
        warnings: [],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore/mnemonas' } })
      fireEvent.click(screen.getByRole('button', { name: /生成预览/ }))

      await waitFor(() => {
        expect(screen.getByText('预览已确认')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /开始恢复/ }))

      await waitFor(() => {
        expect(mockRestoreBackupJob).toHaveBeenCalledWith('external-disk', '/restore/mnemonas', true, expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '备份恢复失败',
          description: '恢复失败',
          color: 'danger',
        })
        expect(screen.getByText('恢复任务失败')).toBeTruthy()
      })
      expect(mockVerifyBackupRestoreJob).not.toHaveBeenCalled()
    })

    it('prefills suggested batch restore target paths', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))

      await waitFor(() => {
        const flow = within(screen.getByLabelText('批量恢复流程进度'))
        expect(flow.getByText('批量恢复流程')).toBeTruthy()
        expect(flow.getByText('选择要恢复的备份任务')).toBeTruthy()
        expect(flow.getByText('选择任务后填写独立目标目录')).toBeTruthy()
        expect(flow.getByText('目标目录确认后生成预览')).toBeTruthy()
        expect(flow.getByText('预览通过后执行批量恢复')).toBeTruthy()
        const readiness = within(screen.getByLabelText('批量恢复准备度摘要'))
        expect(readiness.getByText('尚未选择任务')).toBeTruthy()
        expect(readiness.getByText('尚未选择目标')).toBeTruthy()
        expect(readiness.getByText('选择任务后生成批量预览')).toBeTruthy()
        expect(screen.getByText('可恢复任务 1 项，待处理 0 项；选择后会保留已填写目标，空目标使用建议目录。')).toBeTruthy()
        expect((screen.getByRole('button', { name: '选择待处理' }) as HTMLButtonElement).disabled).toBe(true)
        expect((screen.getByRole('button', { name: '选择全部' }) as HTMLButtonElement).disabled).toBe(false)
        expect((screen.getByRole('button', { name: '清空选择' }) as HTMLButtonElement).disabled).toBe(true)
        const targetInput = screen.getByLabelText('外置硬盘备份 目标目录') as HTMLInputElement
        expect(targetInput.value).toBe('/mnt/restore/external-disk')
        expect(targetInput.disabled).toBe(true)
        expect(screen.getByText('选择该任务后可使用建议目标目录，或改成自定义独立目录。')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: '选择全部' }))

      let selectedTargetInput = screen.getByLabelText('外置硬盘备份 目标目录') as HTMLInputElement
      expect(selectedTargetInput.value).toBe('/mnt/restore/external-disk')
      expect(selectedTargetInput.disabled).toBe(false)
      let readiness = within(screen.getByLabelText('批量恢复准备度摘要'))
      let flow = within(screen.getByLabelText('批量恢复流程进度'))
      expect(flow.getByText('已选择 1 项')).toBeTruthy()
      expect(flow.getByText('1 个目标目录已确认')).toBeTruthy()
      expect(flow.getByText('生成批量预览以确认预检')).toBeTruthy()
      expect(flow.getByText('预览通过后执行批量恢复')).toBeTruthy()
      expect(readiness.getByText('1 / 20 项')).toBeTruthy()
      expect(readiness.getByText('1 / 1 已填写')).toBeTruthy()
      expect(readiness.getByText('需要生成批量预览')).toBeTruthy()
      expect((screen.getByRole('button', { name: '清空选择' }) as HTMLButtonElement).disabled).toBe(false)

      fireEvent.click(screen.getByRole('button', { name: '清空选择' }))

      selectedTargetInput = screen.getByLabelText('外置硬盘备份 目标目录') as HTMLInputElement
      expect(selectedTargetInput.value).toBe('/mnt/restore/external-disk')
      expect(selectedTargetInput.disabled).toBe(true)
      readiness = within(screen.getByLabelText('批量恢复准备度摘要'))
      flow = within(screen.getByLabelText('批量恢复流程进度'))
      expect(flow.getByText('选择要恢复的备份任务')).toBeTruthy()
      expect(flow.getByText('选择任务后填写独立目标目录')).toBeTruthy()
      expect(readiness.getByText('尚未选择任务')).toBeTruthy()
      expect(readiness.getByText('尚未选择目标')).toBeTruthy()

      selectBatchRestoreJob('外置硬盘备份')

      selectedTargetInput = screen.getByLabelText('外置硬盘备份 目标目录') as HTMLInputElement
      expect(selectedTargetInput.value).toBe('/mnt/restore/external-disk')
      expect(selectedTargetInput.disabled).toBe(false)
      readiness = within(screen.getByLabelText('批量恢复准备度摘要'))
      flow = within(screen.getByLabelText('批量恢复流程进度'))
      expect(flow.getByText('已选择 1 项')).toBeTruthy()
      expect(flow.getByText('1 个目标目录已确认')).toBeTruthy()
      expect(readiness.getByText('1 / 20 项')).toBeTruthy()
      expect(readiness.getByText('1 / 1 已填写')).toBeTruthy()
      expect(readiness.getByText('需要生成批量预览')).toBeTruthy()
    })

    it('selects only backup jobs that need restore attention', async () => {
      mockListBackupJobs.mockResolvedValue([
        mockBackupJobs[0],
        {
          ...mockBackupJobs[0],
          id: 'pending-restore',
          name: '待校验备份',
          destination: '/mnt/backup-drive/pending-restore',
          last_matching_restore_verify: undefined,
          last_restore_verify: undefined,
        },
        {
          ...mockBackupJobs[0],
          id: 'warning-restore',
          name: '有警告恢复备份',
          destination: '/mnt/backup-drive/warning-restore',
          last_restore: {
            ...mockBackupJobs[0].last_restore,
            warnings: ['restore completed with warnings'],
          },
        },
      ])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
        expect(screen.getByText('待校验备份')).toBeTruthy()
        expect(screen.getByText('有警告恢复备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))

      await waitFor(() => {
        expect(screen.getByText('可恢复任务 3 项，待处理 2 项；选择后会保留已填写目标，空目标使用建议目录。')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: '选择待处理' }))

      const healthyTargetInput = screen.getByLabelText('外置硬盘备份 目标目录') as HTMLInputElement
      const pendingTargetInput = screen.getByLabelText('待校验备份 目标目录') as HTMLInputElement
      const warningTargetInput = screen.getByLabelText('有警告恢复备份 目标目录') as HTMLInputElement
      expect(healthyTargetInput.disabled).toBe(true)
      expect(pendingTargetInput.value).toBe('/mnt/restore/pending-restore')
      expect(pendingTargetInput.disabled).toBe(false)
      expect(warningTargetInput.value).toBe('/mnt/restore/warning-restore')
      expect(warningTargetInput.disabled).toBe(false)

      const readiness = within(screen.getByLabelText('批量恢复准备度摘要'))
      const flow = within(screen.getByLabelText('批量恢复流程进度'))
      expect(flow.getByText('已选择 2 项')).toBeTruthy()
      expect(flow.getByText('2 个目标目录已确认')).toBeTruthy()
      expect(readiness.getByText('2 / 20 项')).toBeTruthy()
      expect(readiness.getByText('2 / 2 已填写')).toBeTruthy()
      expect(readiness.getByText('需要生成批量预览')).toBeTruthy()
    })

    it('previews and runs a batch restore from the backup card', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      const batchTarget = '/restore/token=batch-secret'
      const redactedBatchTarget = '/restore/token=<redacted>'
      mockPreviewBatchBackupRestore.mockResolvedValueOnce({
        id: '20260509T035901.000000000Z',
        status: 'completed',
        started_at: '2026-05-09T03:59:01Z',
        finished_at: '2026-05-09T03:59:02Z',
        duration_ms: 1000,
        total_files: 12,
        total_bytes: 4096,
        warning: false,
        warnings: [],
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: redactedBatchTarget,
          include_config: true,
          status: 'completed',
          preview: {
            id: '20260509T035900.000000000Z',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T03:59:00Z',
            finished_at: '2026-05-09T03:59:01Z',
            duration_ms: 1000,
            source: '/srv/mnemonas',
            destination: '/mnt/backup-drive/mnemonas',
            target_path: redactedBatchTarget,
            file_count: 12,
            total_bytes: 4096,
            config_available: true,
            config_included: true,
            preflight_checks: [{
              id: 'target_scope',
              status: 'passed',
              title: '目标路径隔离',
              detail: '目标目录位于受保护路径之外。',
            }],
            warnings: [],
          },
        }],
      })
      mockRunBatchBackupRestore.mockResolvedValueOnce({
        id: '20260509T040001.000000000Z',
        status: 'completed',
        started_at: '2026-05-09T04:00:01Z',
        finished_at: '2026-05-09T04:00:02Z',
        duration_ms: 1000,
        total_files: 12,
        verified_bytes: 4096,
        warning: false,
        warnings: [],
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: redactedBatchTarget,
          include_config: true,
          status: 'completed',
          restore: {
            id: '20260509T040000.000000000Z',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T04:00:00Z',
            finished_at: '2026-05-09T04:00:01Z',
            duration_ms: 1000,
            target_path: redactedBatchTarget,
            config_restored: true,
            config_path: '/restore/token=<redacted>/.mnemonas-restore/config.toml',
            file_count: 12,
            verified_bytes: 4096,
            warnings: [],
          },
          verify: {
            id: '20260509T040005.000000000Z',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T04:00:05Z',
            finished_at: '2026-05-09T04:00:06Z',
            duration_ms: 1000,
            source: '/srv/mnemonas',
            destination: '/mnt/backup-drive/mnemonas',
            snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
            manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
            target_path: redactedBatchTarget,
            file_count: 12,
            verified_bytes: 4096,
            config_path: '/restore/token=<redacted>/.mnemonas-restore/config.toml',
            config_found: true,
            files_dir_found: true,
            internal_dir_found: true,
            index_found: true,
            objects_dir_found: true,
            looks_like_storage_root: true,
            warnings: [],
          },
          warnings: [],
        }],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))

      await waitFor(() => {
        expect(screen.getByText('批量恢复到独立目录')).toBeTruthy()
      })

      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: batchTarget } })

      const runButton = screen.getByRole('button', { name: /开始批量恢复/ }) as HTMLButtonElement
      expect(runButton.disabled).toBe(true)

      fireEvent.click(screen.getByRole('button', { name: /生成批量预览/ }))

      await waitFor(() => {
        expect(mockPreviewBatchBackupRestore).toHaveBeenCalledWith([{
          job_id: 'external-disk',
          target_path: batchTarget,
          include_config: true,
        }], expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(screen.getByText('批量预览结果')).toBeTruthy()
        const flow = within(screen.getByLabelText('批量恢复流程进度'))
        expect(flow.getByText('批量预览可用于执行')).toBeTruthy()
        expect(flow.getByText('预览通过，可开始批量恢复')).toBeTruthy()
        const readiness = within(screen.getByLabelText('批量恢复准备度摘要'))
        expect(readiness.getByText('1 / 20 项')).toBeTruthy()
        expect(readiness.getByText('1 项会恢复配置文件')).toBeTruthy()
        expect(readiness.getByText('批量预览可用于执行')).toBeTruthy()
        const impact = within(screen.getByLabelText('批量恢复影响摘要'))
        expect(impact.getByText('目标目录')).toBeTruthy()
        expect(impact.getByText('冲突与覆盖')).toBeTruthy()
        expect(impact.getByText('权限影响')).toBeTruthy()
        expect(impact.getByText(/1 项会恢复配置文件到各自目标目录/)).toBeTruthy()
        expect(impact.getByText('恢复后校验')).toBeTruthy()
        const review = within(screen.getByLabelText('批量恢复执行前复核'))
        expect(review.getByText('恢复项目')).toBeTruthy()
        expect(review.getByText('1 项')).toBeTruthy()
        expect(review.getByText('目标目录')).toBeTruthy()
        expect(review.getByText('1 个互不重叠的独立目标目录')).toBeTruthy()
        expect(review.getByText('恢复内容')).toBeTruthy()
        expect(review.getByText('12 个文件 · 4 KB')).toBeTruthy()
        expect(review.getByText('配置文件')).toBeTruthy()
        expect(review.getByText('1 项会恢复配置文件')).toBeTruthy()
        expect(review.getByText('预检结果')).toBeTruthy()
        expect(review.getByText('1 项通过 · 0 项提醒 · 0 项失败')).toBeTruthy()
        expect(review.getByText('恢复后检查')).toBeTruthy()
        expect(review.getByText('每个成功项目都会自动执行只读校验；批量结果会汇总已校验文件数和字节数。')).toBeTruthy()
        expect(review.getByText('跨目录切换')).toBeTruthy()
        expect(review.getByText('逐项只读校验通过后再切换；保留原目录和原配置作为回滚点。')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /开始批量恢复/ }))

      await waitFor(() => {
        expect(mockRunBatchBackupRestore).toHaveBeenCalledWith([{
          job_id: 'external-disk',
          target_path: batchTarget,
          include_config: true,
        }], expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '批量恢复已完成' }))
        const flow = within(screen.getByLabelText('批量恢复流程进度'))
        expect(flow.getByText('已完成批量预览和预检')).toBeTruthy()
        expect(flow.getByText('批量恢复和只读校验已完成')).toBeTruthy()
        expect(screen.getByText('批量恢复已完成')).toBeTruthy()
        expect(screen.getByText('只读校验：检查 12 个文件 · 4 KB')).toBeTruthy()
        expect(screen.getAllByText('对照快照 20260509T020304.000000000Z').length).toBeGreaterThan(0)
        const cutoverCandidates = within(screen.getByLabelText('批量恢复跨目录切换候选'))
        expect(cutoverCandidates.getByText('跨目录切换候选')).toBeTruthy()
        expect(cutoverCandidates.getByText('逐项只读校验通过后再切换；切换前保留原 storage.root、原配置文件和回滚清单。')).toBeTruthy()
        expect(cutoverCandidates.getByText('只读校验通过，可作为 storage.root 候选目录；切换前保留原目录和原配置。')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /复制批量恢复记录/ }))

      await waitFor(() => {
        expect(writeText).toHaveBeenCalledTimes(1)
      })
      const report = writeText.mock.calls[0]?.[0] as string
      expect(report).toContain('批量恢复记录')
      expect(report).toContain('批次 ID：20260509T040001.000000000Z')
      expect(report).toContain('恢复项目：1 项')
      expect(report).toContain('1. external-disk')
      expect(report).toContain('目标目录：/restore/token=<redacted>')
      expect(report).toContain('配置文件：/restore/token=<redacted>/.mnemonas-restore/config.toml')
      expect(report).toContain('只读校验：检查完成；检查 12 个文件 · 4 KB；可作为完整 storage.root 候选目录')
      expect(report).toContain('快照：对照快照 20260509T020304.000000000Z')
      expect(report).toContain('跨目录切换候选')
      expect(report).toContain('候选目录：/restore/token=<redacted>')
      expect(report).toContain('切换复核：只读校验通过，可作为 storage.root 候选目录；切换前保留原目录和原配置。')
      expect(report).not.toContain(batchTarget)
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '批量恢复记录已复制' }))
    })

    it('blocks batch restore preview when selected targets overlap', async () => {
      const remoteJob = {
        ...mockBackupJobs[0],
        id: 'rclone-remote',
        name: '远端备份',
        type: 'rclone',
        destination: 'remote:mnemonas',
        include_config: false,
        last_run: undefined,
        last_successful_run: undefined,
      }
      mockListBackupJobs.mockResolvedValue([mockBackupJobs[0], remoteJob])

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
        expect(screen.getByText('远端备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: '/restore/batch' } })
      selectBatchRestoreJob('远端备份')
      fireEvent.change(screen.getByLabelText('远端备份 目标目录'), { target: { value: '/restore/batch/nested' } })

      const readiness = within(screen.getByLabelText('批量恢复准备度摘要'))
      expect(readiness.getByText('2 / 20 项')).toBeTruthy()
      expect(readiness.getByText('2 / 2 已填写；存在重复或父子嵌套')).toBeTruthy()
      expect(readiness.getByText('处理目标目录后再生成批量预览')).toBeTruthy()
      expect(screen.getByText('第 1 项和第 2 项的目标目录重复或存在父子嵌套，请改为互不包含的独立目录。')).toBeTruthy()
      expect((screen.getByRole('button', { name: /生成批量预览/ }) as HTMLButtonElement).disabled).toBe(true)

      fireEvent.click(screen.getByRole('button', { name: /生成批量预览/ }))
      expect(mockPreviewBatchBackupRestore).not.toHaveBeenCalled()
    })

    it('blocks batch restore preview when a selected target path uses Windows syntax', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: 'C:\\restore\\batch' } })

      expect(screen.getByText('第 1 项：恢复目标不能包含反斜杠，请使用服务器上的 POSIX 绝对路径。')).toBeTruthy()
      expect((screen.getByRole('button', { name: /生成批量预览/ }) as HTMLButtonElement).disabled).toBe(true)

      fireEvent.click(screen.getByRole('button', { name: /生成批量预览/ }))
      expect(mockPreviewBatchBackupRestore).not.toHaveBeenCalled()
    })

    it('maps backend batch target conflict messages to one-based item labels', async () => {
      const remoteJob = {
        ...mockBackupJobs[0],
        id: 'rclone-remote',
        name: '远端备份',
        type: 'rclone',
        destination: 'remote:mnemonas',
        include_config: false,
        last_run: undefined,
        last_successful_run: undefined,
      }
      const backendConflict = 'item 1: restore target already exists: restore target conflicts with batch item 0'
      mockListBackupJobs.mockResolvedValue([mockBackupJobs[0], remoteJob])
      mockPreviewBatchBackupRestore.mockResolvedValueOnce({
        id: 'batch-preview-backend-conflict',
        status: 'completed',
        started_at: '2026-05-09T03:59:00Z',
        finished_at: '2026-05-09T03:59:01Z',
        duration_ms: 1000,
        total_files: 12,
        total_bytes: 4096,
        warning: true,
        warnings: [backendConflict],
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: '/restore/a',
          include_config: true,
          status: 'completed',
          preview: {
            id: '20260509T035900.000000000Z',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T03:59:00Z',
            finished_at: '2026-05-09T03:59:01Z',
            duration_ms: 1000,
            source: '/srv/mnemonas',
            destination: '/mnt/backup-drive/mnemonas',
            target_path: '/restore/a',
            file_count: 12,
            total_bytes: 4096,
            config_available: true,
            config_included: true,
            preflight_checks: [],
            warnings: [],
            cutover_checklist: [],
            rollback_checklist: [],
          },
        }, {
          index: 1,
          job_id: 'rclone-remote',
          target_path: '/restore/b',
          include_config: false,
          status: 'failed',
          error_message: 'restore target already exists: restore target conflicts with batch item 0',
        }],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
        expect(screen.getByText('远端备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: '/restore/a' } })
      selectBatchRestoreJob('远端备份')
      fireEvent.change(screen.getByLabelText('远端备份 目标目录'), { target: { value: '/restore/b' } })
      fireEvent.click(screen.getByRole('button', { name: /生成批量预览/ }))

      await waitFor(() => {
        expect(screen.getByText('批量预览结果')).toBeTruthy()
        expect(screen.getAllByText('项目 2：恢复目标与第 1 项重复或存在父子嵌套。').length).toBeGreaterThan(0)
      })
      expect(screen.queryByText(backendConflict)).toBeNull()
      expect(screen.queryByText('restore target already exists: restore target conflicts with batch item 0')).toBeNull()
    })

    it('blocks batch restore preview when a selected target path is relative', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: 'relative/restore' } })

      expect(screen.getByText('第 1 项：恢复目标必须是服务器上的绝对路径，例如 /mnt/restore/mnemonas。')).toBeTruthy()
      const previewAction = screen.getByRole('button', { name: /生成批量预览/ }) as HTMLButtonElement
      expect(previewAction.disabled).toBe(true)

      fireEvent.click(previewAction)
      expect(mockPreviewBatchBackupRestore).not.toHaveBeenCalled()
    })

    it('blocks batch restore preview when an absolute selected target path contains backslashes', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: '/restore\\batch' } })

      expect(screen.getByText('第 1 项：恢复目标不能包含反斜杠，请使用服务器上的 POSIX 绝对路径。')).toBeTruthy()
      const previewAction = screen.getByRole('button', { name: /生成批量预览/ }) as HTMLButtonElement
      expect(previewAction.disabled).toBe(true)

      fireEvent.click(previewAction)
      expect(mockPreviewBatchBackupRestore).not.toHaveBeenCalled()
    })

    it('blocks batch restore preview when a selected target path contains control characters', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: '/restore/\u0081bad' } })

      expect(screen.getByText('第 1 项：恢复目标不能包含控制字符。')).toBeTruthy()
      const previewAction = screen.getByRole('button', { name: /生成批量预览/ }) as HTMLButtonElement
      expect(previewAction.disabled).toBe(true)

      fireEvent.click(previewAction)
      expect(mockPreviewBatchBackupRestore).not.toHaveBeenCalled()
    })

    it('blocks batch restore preview when a selected target path is protected', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: '/etc' } })

      expect(screen.getByText('第 1 项：恢复目标不能是文件系统根目录或受保护系统目录。')).toBeTruthy()
      const previewAction = screen.getByRole('button', { name: /生成批量预览/ }) as HTMLButtonElement
      expect(previewAction.disabled).toBe(true)

      fireEvent.click(previewAction)
      expect(mockPreviewBatchBackupRestore).not.toHaveBeenCalled()
    })

    it('blocks batch restore preview when a selected target path contains dot segments', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: '/restore/./batch' } })

      expect(screen.getByText('第 1 项：恢复目标不能包含 . 或 .. 路径段。')).toBeTruthy()
      const previewAction = screen.getByRole('button', { name: /生成批量预览/ }) as HTMLButtonElement
      expect(previewAction.disabled).toBe(true)

      fireEvent.click(previewAction)
      expect(mockPreviewBatchBackupRestore).not.toHaveBeenCalled()
    })

    it('shows warning details for every batch restore preview item', async () => {
      const firstWarning = '外置硬盘备份预览包含配置文件，请确认是否需要同时恢复配置。'
      const secondWarning = '远端备份预览未同时检测到 files/ 和 .mnemonas/，仅在恢复的是子目录时才适合直接切换 storage.root'
      const remoteJob = {
        ...mockBackupJobs[0],
        id: 'rclone-remote',
        name: '远端备份',
        type: 'rclone',
        destination: 'remote:mnemonas',
        include_config: false,
        last_run: undefined,
        last_successful_run: undefined,
      }
      mockListBackupJobs.mockResolvedValue([mockBackupJobs[0], remoteJob])
      mockPreviewBatchBackupRestore.mockResolvedValueOnce({
        id: 'batch-preview-warnings',
        status: 'completed',
        started_at: '2026-05-09T03:59:00Z',
        finished_at: '2026-05-09T03:59:01Z',
        duration_ms: 1000,
        total_files: 15,
        total_bytes: 6144,
        warning: true,
        warnings: [firstWarning, secondWarning],
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: '/restore/batch-local',
          include_config: true,
          status: 'completed',
          preview: {
            id: 'preview-local',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T03:59:00Z',
            finished_at: '2026-05-09T03:59:01Z',
            duration_ms: 1000,
            source: '/srv/mnemonas',
            destination: '/mnt/backup-drive/mnemonas',
            target_path: '/restore/batch-local',
            file_count: 12,
            total_bytes: 4096,
            config_available: true,
            config_included: true,
            preflight_checks: [{
              id: 'target_scope',
              status: 'passed',
              title: '目标路径隔离',
              detail: '目标目录位于受保护路径之外。',
            }],
            warnings: [firstWarning],
          },
        }, {
          index: 1,
          job_id: 'rclone-remote',
          target_path: '/restore/batch-remote',
          include_config: false,
          status: 'completed',
          preview: {
            id: 'preview-remote',
            job_id: 'rclone-remote',
            status: 'completed',
            started_at: '2026-05-09T03:59:00Z',
            finished_at: '2026-05-09T03:59:01Z',
            duration_ms: 1000,
            source: '/srv/mnemonas',
            destination: 'remote:mnemonas',
            target_path: '/restore/batch-remote',
            file_count: 3,
            total_bytes: 2048,
            config_available: false,
            config_included: false,
            preflight_checks: [{
              id: 'target_scope',
              status: 'passed',
              title: '目标路径隔离',
              detail: '目标目录位于受保护路径之外。',
            }],
            warnings: [secondWarning],
          },
        }],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
        expect(screen.getByText('远端备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: '/restore/batch-local' } })
      selectBatchRestoreJob('远端备份')
      fireEvent.change(screen.getByLabelText('远端备份 目标目录'), { target: { value: '/restore/batch-remote' } })
      fireEvent.click(screen.getByRole('button', { name: /生成批量预览/ }))

      await waitFor(() => {
        expect(mockPreviewBatchBackupRestore).toHaveBeenCalledWith([{
          job_id: 'external-disk',
          target_path: '/restore/batch-local',
          include_config: true,
        }, {
          job_id: 'rclone-remote',
          target_path: '/restore/batch-remote',
          include_config: false,
        }], expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(screen.getAllByText(firstWarning).length).toBeGreaterThan(0)
        expect(screen.getByText(secondWarning)).toBeTruthy()
      })
    })

    it('shows batch restore warning details in the completion toast', async () => {
      const batchWarning = '恢复目录未同时检测到 files/ 和 .mnemonas/，仅在恢复的是子目录时才适合直接切换 storage.root'
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockRunBatchBackupRestore.mockResolvedValueOnce({
        id: '20260509T040001.000000000Z',
        status: 'completed',
        started_at: '2026-05-09T04:00:01Z',
        finished_at: '2026-05-09T04:00:02Z',
        duration_ms: 1000,
        total_files: 12,
        verified_bytes: 4096,
        warning: true,
        warnings: [batchWarning],
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: '/restore/batch',
          include_config: true,
          status: 'completed',
          restore: {
            id: '20260509T040000.000000000Z',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T04:00:00Z',
            finished_at: '2026-05-09T04:00:01Z',
            duration_ms: 1000,
            target_path: '/restore/batch',
            config_restored: true,
            config_path: '/restore/batch/.mnemonas-restore/config.toml',
            file_count: 12,
            verified_bytes: 4096,
            warnings: [],
          },
          verify: {
            id: '20260509T040005.000000000Z',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T04:00:05Z',
            finished_at: '2026-05-09T04:00:06Z',
            duration_ms: 1000,
            source: '/srv/mnemonas',
            destination: '/mnt/backup-drive/mnemonas',
            target_path: '/restore/batch',
            file_count: 12,
            verified_bytes: 4096,
            config_path: '/restore/batch/.mnemonas-restore/config.toml',
            config_found: true,
            files_dir_found: true,
            internal_dir_found: true,
            index_found: false,
            objects_dir_found: false,
            looks_like_storage_root: false,
            warnings: [batchWarning],
          },
          warnings: [batchWarning],
        }],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: '/restore/batch' } })
      fireEvent.click(screen.getByRole('button', { name: /生成批量预览/ }))

      await waitFor(() => {
        expect(screen.getByText('批量预览结果')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /开始批量恢复/ }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '批量恢复完成，有警告',
          description: batchWarning,
          color: 'warning',
        }))
        expect(screen.getAllByText(batchWarning).length).toBeGreaterThan(0)
      })
    })

    it('localizes partial batch restore failure summaries', async () => {
      const secondJob = {
        ...mockBackupJobs[0],
        id: 'garage-disk',
        name: '车库硬盘备份',
        destination: '/mnt/garage/mnemonas',
      }
      mockListBackupJobs.mockResolvedValue([mockBackupJobs[0], secondJob])
      mockPreviewBatchBackupRestore.mockResolvedValueOnce({
        id: 'batch-preview-partial',
        status: 'completed',
        started_at: '2026-05-09T03:59:00Z',
        finished_at: '2026-05-09T03:59:01Z',
        duration_ms: 1000,
        total_files: 24,
        total_bytes: 8192,
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: '/restore/external',
          include_config: true,
          status: 'completed',
          preview: {
            id: 'preview-external',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T03:59:00Z',
            finished_at: '2026-05-09T03:59:01Z',
            duration_ms: 1000,
            source: '/srv/mnemonas',
            destination: '/mnt/backup-drive/mnemonas',
            target_path: '/restore/external',
            file_count: 12,
            total_bytes: 4096,
            config_available: true,
            config_included: true,
          },
        }, {
          index: 1,
          job_id: 'garage-disk',
          target_path: '/restore/garage',
          include_config: true,
          status: 'completed',
          preview: {
            id: 'preview-garage',
            job_id: 'garage-disk',
            status: 'completed',
            started_at: '2026-05-09T03:59:00Z',
            finished_at: '2026-05-09T03:59:01Z',
            duration_ms: 1000,
            source: '/srv/mnemonas',
            destination: '/mnt/garage/mnemonas',
            target_path: '/restore/garage',
            file_count: 12,
            total_bytes: 4096,
            config_available: true,
            config_included: true,
          },
        }],
      })
      mockRunBatchBackupRestore.mockResolvedValueOnce({
        id: 'batch-restore-partial',
        status: 'completed',
        started_at: '2026-05-09T04:00:00Z',
        finished_at: '2026-05-09T04:00:02Z',
        duration_ms: 2000,
        total_files: 12,
        verified_bytes: 4096,
        warning: true,
        warnings: ['item 1: restore target already exists'],
        error_message: '1 of 2 batch restore items failed',
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: '/restore/external',
          include_config: true,
          status: 'completed',
          restore: {
            id: 'restore-external',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T04:00:00Z',
            finished_at: '2026-05-09T04:00:01Z',
            duration_ms: 1000,
            target_path: '/restore/external',
            config_restored: true,
            file_count: 12,
            verified_bytes: 4096,
            warnings: [],
          },
          verify: {
            id: 'verify-external',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T04:00:01Z',
            finished_at: '2026-05-09T04:00:02Z',
            duration_ms: 1000,
            source: '/srv/mnemonas',
            destination: '/mnt/backup-drive/mnemonas',
            target_path: '/restore/external',
            file_count: 12,
            verified_bytes: 4096,
            config_found: true,
            files_dir_found: true,
            internal_dir_found: true,
            index_found: true,
            objects_dir_found: true,
            looks_like_storage_root: true,
            warnings: [],
          },
          warnings: [],
        }, {
          index: 1,
          job_id: 'garage-disk',
          target_path: '/restore/garage',
          include_config: true,
          status: 'failed',
          error_message: 'restore target already exists',
        }],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
        expect(screen.getByText('车库硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      selectBatchRestoreJob('车库硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: '/restore/external' } })
      fireEvent.change(screen.getByLabelText('车库硬盘备份 目标目录'), { target: { value: '/restore/garage' } })
      fireEvent.click(screen.getByRole('button', { name: /生成批量预览/ }))

      await waitFor(() => {
        expect(screen.getByText('批量预览结果')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /开始批量恢复/ }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '批量恢复完成，有警告',
          description: '项目 2：恢复目标已存在',
          color: 'warning',
        }))
        expect(screen.getByText('1 / 2 个批量恢复项目失败')).toBeTruthy()
        expect(screen.getAllByText('项目 2：恢复目标已存在').length).toBeGreaterThan(0)
      })
      expect(screen.queryByText('1 of 2 batch restore items failed')).toBeNull()
      expect(screen.queryByText('item 1: restore target already exists')).toBeNull()
    })

    it('keeps unknown indexed batch restore warnings unchanged', async () => {
      const rawWarning = 'item 0: RCLONE Exit Status 1'
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockRunBatchBackupRestore.mockResolvedValueOnce({
        id: '20260509T040001.000000000Z',
        status: 'completed',
        started_at: '2026-05-09T04:00:01Z',
        finished_at: '2026-05-09T04:00:02Z',
        duration_ms: 1000,
        total_files: 12,
        verified_bytes: 4096,
        warning: true,
        warnings: [rawWarning],
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: '/restore/batch',
          include_config: true,
          status: 'completed',
          restore: {
            id: '20260509T040000.000000000Z',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T04:00:00Z',
            finished_at: '2026-05-09T04:00:01Z',
            duration_ms: 1000,
            target_path: '/restore/batch',
            config_restored: true,
            file_count: 12,
            verified_bytes: 4096,
            warnings: [],
          },
          warnings: [rawWarning],
        }],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: '/restore/batch' } })
      fireEvent.click(screen.getByRole('button', { name: /生成批量预览/ }))

      await waitFor(() => {
        expect(screen.getByText('批量预览结果')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /开始批量恢复/ }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '批量恢复完成，有警告',
          description: rawWarning,
          color: 'warning',
        }))
        expect(screen.getAllByText(rawWarning).length).toBeGreaterThan(0)
      })
      expect(screen.queryByText('项目 1：rclone exit status 1')).toBeNull()
    })

    it('redacts secret-like fragments in unknown backup diagnostics', async () => {
      const rawWarning = 'item 0: rclone failed https://backup.example/repo?token=batch-secret --password restic-secret Authorization: Bearer bearer-secret api-key: api-secret'
      const redactedWarning = 'item 0: rclone failed https://backup.example/repo?token=<redacted> --password <redacted> Authorization: Bearer <redacted> api-key: <redacted>'
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockRunBatchBackupRestore.mockResolvedValueOnce({
        id: '20260509T040001.000000000Z',
        status: 'completed',
        started_at: '2026-05-09T04:00:01Z',
        finished_at: '2026-05-09T04:00:02Z',
        duration_ms: 1000,
        total_files: 12,
        verified_bytes: 4096,
        warning: true,
        warnings: [rawWarning],
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: '/restore/batch',
          include_config: true,
          status: 'completed',
          restore: {
            id: '20260509T040000.000000000Z',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T04:00:00Z',
            finished_at: '2026-05-09T04:00:01Z',
            duration_ms: 1000,
            target_path: '/restore/batch',
            config_restored: true,
            file_count: 12,
            verified_bytes: 4096,
            warnings: [],
          },
          warnings: [rawWarning],
        }],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: '/restore/batch' } })
      fireEvent.click(screen.getByRole('button', { name: /生成批量预览/ }))

      await waitFor(() => {
        expect(screen.getByText('批量预览结果')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /开始批量恢复/ }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '批量恢复完成，有警告',
          description: redactedWarning,
          color: 'warning',
        }))
        expect(screen.getAllByText(redactedWarning).length).toBeGreaterThan(0)
      })
      expect(screen.queryByText(rawWarning)).toBeNull()
      for (const leaked of ['batch-secret', 'restic-secret', 'bearer-secret', 'api-secret']) {
        expect(document.body.textContent).not.toContain(leaked)
      }
    })

    it('shows failed batch restore results without completed-count metrics', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockPreviewBatchBackupRestore.mockResolvedValueOnce({
        id: 'batch-preview-failed-run',
        status: 'completed',
        started_at: '2026-05-09T03:59:00Z',
        finished_at: '2026-05-09T03:59:01Z',
        duration_ms: 1000,
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: '/restore/batch',
          include_config: true,
          status: 'completed',
          preview: {
            id: 'preview-item',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T03:59:00Z',
            finished_at: '2026-05-09T03:59:01Z',
            duration_ms: 1000,
            source: '/srv/mnemonas',
            destination: '/mnt/backup-drive/mnemonas',
            target_path: '/restore/batch',
            file_count: 12,
            total_bytes: 4096,
            config_available: true,
            config_included: true,
          },
        }],
        total_files: 12,
        total_bytes: 4096,
      })
      mockRunBatchBackupRestore.mockResolvedValueOnce({
        id: 'batch-restore-failed',
        status: 'failed',
        started_at: '2026-05-09T04:00:00Z',
        finished_at: '2026-05-09T04:00:01Z',
        duration_ms: 1000,
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: '/restore/batch',
          include_config: true,
          status: 'failed',
          error_message: 'batch restore preflight failed before this item started',
        }],
        total_files: 0,
        verified_bytes: 0,
        warning: true,
        warnings: ['batch restore preflight failed before writes; no target data was written'],
        error_message: 'all batch restore items failed',
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /批量恢复/ }))
      selectBatchRestoreJob('外置硬盘备份')
      fireEvent.change(screen.getByLabelText('外置硬盘备份 目标目录'), { target: { value: '/restore/batch' } })
      fireEvent.click(screen.getByRole('button', { name: /生成批量预览/ }))

      await waitFor(() => {
        expect(screen.getByText('批量预览结果')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('button', { name: /开始批量恢复/ }))

      await waitFor(() => {
        expect(mockRunBatchBackupRestore).toHaveBeenCalledWith([{
          job_id: 'external-disk',
          target_path: '/restore/batch',
          include_config: true,
        }], expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '批量恢复失败',
          description: '批量恢复预检未通过，未写入任何目标数据',
          color: 'danger',
        }))
        expect(screen.getAllByText('批量恢复失败').length).toBeGreaterThan(0)
        expect(screen.getAllByText('批量恢复预检未通过，未写入任何目标数据').length).toBeGreaterThan(0)
        expect(screen.getByText('批量恢复预检未通过，该项目未开始写入')).toBeTruthy()
        expect(screen.getByText('所有批量恢复项目均失败')).toBeTruthy()
        expect(within(screen.getByLabelText('批量恢复流程进度')).getByText('批量恢复失败，未完成的项目需处理')).toBeTruthy()
        expect(screen.queryByText(/0\/1 项完成/)).toBeNull()
      })
      expect(screen.queryByText('batch restore preflight failed before writes; no target data was written')).toBeNull()
      expect(screen.queryByText('batch restore preflight failed before this item started')).toBeNull()
      expect(screen.queryByText('all batch restore items failed')).toBeNull()
    })

    it('keeps restore disabled when preview fails', async () => {
      const user = userEvent.setup()
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockPreviewBackupRestoreJob.mockRejectedValueOnce(new ApiError('restore target already exists', 409))

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore/mnemonas' } })
      await user.click(screen.getByRole('button', { name: /生成预览/ }))

      await waitFor(() => {
        expect(mockPreviewBackupRestoreJob).toHaveBeenCalledWith('external-disk', '/restore/mnemonas', true, expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '生成恢复预览失败' }))
      })

      expect((screen.getByRole('button', { name: /开始恢复/ }) as HTMLButtonElement).disabled).toBe(true)
      expect(mockRestoreBackupJob).not.toHaveBeenCalled()
    })

    it('keeps restore disabled when preflight has failed checks', async () => {
      const user = userEvent.setup()
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockPreviewBackupRestoreJob.mockResolvedValueOnce({
        id: '20260509T035900.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T03:59:00Z',
        finished_at: '2026-05-09T03:59:01Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: '/mnt/backup-drive/mnemonas',
        target_path: '/restore/mnemonas',
        file_count: 12,
        total_bytes: 4096,
        config_available: true,
        config_included: true,
        preflight_checks: [{
          id: 'target_capacity',
          status: 'failed',
          title: '目标容量',
          detail: '目标文件系统可用空间不足。',
        }],
        warnings: ['目标文件系统可用空间不足。'],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore/mnemonas' } })
      await user.click(screen.getByRole('button', { name: /生成预览/ }))

      await waitFor(() => {
        expect(screen.getByText('预检未通过，需处理失败项后重新生成预览。')).toBeTruthy()
        expect(within(screen.getByLabelText('恢复流程进度')).getByText('预检未通过，处理失败项后重新生成预览')).toBeTruthy()
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '恢复预检未通过' }))
      })

      expect((screen.getByRole('button', { name: /开始恢复/ }) as HTMLButtonElement).disabled).toBe(true)
      expect(mockRestoreBackupJob).not.toHaveBeenCalled()
    })

    it('shows guided restore steps from target selection through verification', async () => {
      const user = userEvent.setup()
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /^恢复$/ }))

      let guide = within(screen.getByLabelText('恢复流程进度'))
      expect(guide.getByText('恢复流程')).toBeTruthy()
      expect(guide.getByText('目标目录')).toBeTruthy()
      expect(guide.getByText('恢复预览')).toBeTruthy()
      expect(guide.getByText('执行恢复')).toBeTruthy()
      expect(guide.getByText('只读校验与切换')).toBeTruthy()
      expect(guide.getByText('目标已填写：/mnt/restore/external-disk')).toBeTruthy()
      expect(guide.getByText('生成预览以确认文件、配置和预检')).toBeTruthy()
      expect(guide.getByText('预览通过后执行恢复')).toBeTruthy()
      expect(guide.getByText('恢复完成后自动检查')).toBeTruthy()

      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore/mnemonas' } })
      await user.click(screen.getByRole('button', { name: /生成预览/ }))

      await waitFor(() => {
        guide = within(screen.getByLabelText('恢复流程进度'))
        expect(guide.getByText('预览已确认，可复核执行')).toBeTruthy()
        expect(guide.getByText('预览通过，可开始恢复')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /开始恢复/ }))

      await waitFor(() => {
        guide = within(screen.getByLabelText('恢复流程进度'))
        expect(guide.getByText('已完成预览和预检')).toBeTruthy()
        expect(guide.getByText('恢复已写入独立目录')).toBeTruthy()
        expect(guide.getByText('只读校验完成，可按清单人工切换')).toBeTruthy()
      })
    })

    it('shows a pre-submit restore execution review after preview succeeds', async () => {
      const user = userEvent.setup()
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)
      mockPreviewBackupRestoreJob.mockResolvedValueOnce({
        id: '20260509T035900.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T03:59:00Z',
        finished_at: '2026-05-09T03:59:01Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: '/mnt/backup-drive/mnemonas',
        target_path: '/restore/mnemonas',
        file_count: 12,
        total_bytes: 4096,
        config_available: true,
        config_included: true,
        sample_paths: ['docs/note.txt', '.mnemonas-restore/config.toml'],
        preflight_checks: [
          {
            id: 'target_isolated',
            status: 'passed',
            title: '目标隔离',
            detail: '恢复目标位于当前 storage.root 和备份目标之外。',
          },
          {
            id: 'target_capacity',
            status: 'warning',
            title: '目标容量',
            detail: '目标文件系统剩余空间接近下限。',
          },
        ],
        warnings: ['目标文件系统剩余空间接近下限。'],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /^恢复$/ }))
      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore/mnemonas' } })
      await user.click(screen.getByRole('button', { name: /生成预览/ }))

      await waitFor(() => {
        const review = within(screen.getByLabelText('恢复执行前复核'))
        expect(review.getByText('目标目录')).toBeTruthy()
        expect(review.getByText('/restore/mnemonas')).toBeTruthy()
        expect(review.getByText('写入边界')).toBeTruthy()
        expect(review.getByText('恢复只写入独立目录，不覆盖当前 storage.root。')).toBeTruthy()
        expect(review.getByText('恢复内容')).toBeTruthy()
        expect(review.getByText('预计 12 个文件 · 4 KB')).toBeTruthy()
        expect(review.getByText('配置文件')).toBeTruthy()
        expect(review.getByText('将恢复到 .mnemonas-restore/config.toml')).toBeTruthy()
        expect(review.getByText('预检结果')).toBeTruthy()
        expect(review.getByText('1 项通过 · 1 项提醒 · 0 项失败')).toBeTruthy()
        expect(review.getByText('恢复后检查')).toBeTruthy()
        expect(review.getByText('恢复完成后自动执行只读校验，并显示切换步骤和回滚清单。')).toBeTruthy()
        expect(review.getByText('切换前确认')).toBeTruthy()
        expect(review.getByText('只在只读校验通过后切换；保留原 storage.root、原配置文件和回滚清单。')).toBeTruthy()
      })

      expect((screen.getByRole('button', { name: /开始恢复/ }) as HTMLButtonElement).disabled).toBe(false)
      await user.click(screen.getByRole('button', { name: '复制复核记录' }))

      await waitFor(() => {
        expect(writeText).toHaveBeenCalledTimes(1)
      })
      const report = String(writeText.mock.calls[0]?.[0] ?? '')
      expect(report).toContain('恢复执行前复核')
      expect(report).toContain('任务 ID：external-disk')
      expect(report).toContain('恢复目标：/restore/mnemonas')
      expect(report).toContain('恢复内容：预计 12 个文件 · 4 KB')
      expect(report).toContain('配置文件：将恢复到 .mnemonas-restore/config.toml')
      expect(report).toContain('预检结果：1 项通过 · 1 项提醒 · 0 项失败')
      expect(report).toContain('预览状态：当前目标目录和配置选项与预览一致')
      expect(report).toContain('写入边界：恢复只写入独立目录，不覆盖当前 storage.root。')
      expect(report).toContain('切换前确认：只在只读校验通过后切换；保留原 storage.root、原配置文件和回滚清单。')
      expect(report).toContain('路径样例：docs/note.txt；.mnemonas-restore/config.toml')
      expect(report).toContain('恢复提醒：目标文件系统剩余空间接近下限。')
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '恢复执行前复核记录已复制' }))
      expect(mockRestoreBackupJob).not.toHaveBeenCalled()
    })

    it('restores an rclone remote backup from the task list', async () => {
      const user = userEvent.setup()
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        id: 'rclone-remote',
        name: 'Rclone 远端备份',
        type: 'rclone',
        destination: 'backup:mnemonas/source',
        remote: 'backup:mnemonas/source',
        command: '/usr/bin/rclone',
        include_config: true,
        max_snapshots: undefined,
        max_age: undefined,
        retention_status: 'warning',
        retention_message: '远端保留策略需要在外部工具中确认',
        restore_drill_status: 'due',
        restore_drill_message: '尚未完成恢复演练',
        last_run: undefined,
        last_successful_run: undefined,
        last_restore_drill: undefined,
        last_restore: undefined,
        restore_history: undefined,
      }])
      mockPreviewBackupRestoreJob.mockResolvedValueOnce({
        id: '20260509T035900.000000000Z',
        job_id: 'rclone-remote',
        status: 'completed',
        started_at: '2026-05-09T03:59:00Z',
        finished_at: '2026-05-09T03:59:01Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: 'backup:mnemonas/source',
        manifest_path: 'backup:mnemonas/source',
        target_path: '/restore/rclone',
        file_count: 0,
        total_bytes: 0,
        config_available: false,
        config_included: false,
      })
      mockRestoreBackupJob.mockResolvedValueOnce({
        id: '20260509T040000.000000000Z',
        job_id: 'rclone-remote',
        status: 'completed',
        started_at: '2026-05-09T04:00:00Z',
        finished_at: '2026-05-09T04:00:01Z',
        duration_ms: 1000,
        manifest_path: 'backup:mnemonas/source',
        target_path: '/restore/rclone',
        config_restored: false,
        file_count: 0,
        verified_bytes: 0,
      })
      mockVerifyBackupRestoreJob.mockResolvedValueOnce({
        id: '20260509T040005.000000000Z',
        job_id: 'rclone-remote',
        status: 'completed',
        started_at: '2026-05-09T04:00:05Z',
        finished_at: '2026-05-09T04:00:06Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: 'backup:mnemonas/source',
        target_path: '/restore/rclone',
        file_count: 0,
        verified_bytes: 0,
        config_found: false,
        files_dir_found: false,
        internal_dir_found: false,
        index_found: false,
        objects_dir_found: false,
        looks_like_storage_root: false,
        warnings: ['目标目录未发现常规文件'],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('Rclone 远端备份')).toBeTruthy()
        expect(screen.getByText('远端保留策略需要在外部工具中确认')).toBeTruthy()
      })

      const restoreButton = screen.getByRole('button', { name: /^恢复$/ }) as HTMLButtonElement
      expect(restoreButton.disabled).toBe(false)
      await user.click(restoreButton)

      await waitFor(() => {
        expect(screen.getByText('恢复备份到目录')).toBeTruthy()
        expect(screen.queryByText('同时恢复备份中的配置文件')).toBeNull()
      })

      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore/rclone' } })
      await user.click(screen.getByRole('button', { name: /生成预览/ }))
      await waitFor(() => {
        expect(mockPreviewBackupRestoreJob).toHaveBeenCalledWith('rclone-remote', '/restore/rclone', false, expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
      })
      await user.click(screen.getByRole('button', { name: /开始恢复/ }))

      await waitFor(() => {
        expect(mockRestoreBackupJob).toHaveBeenCalledWith('rclone-remote', '/restore/rclone', false, expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockVerifyBackupRestoreJob).toHaveBeenCalledWith('rclone-remote', '/restore/rclone', expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '备份已恢复',
          description: '恢复命令已完成，目标：/restore/rclone',
        }))
      })
    })

    it('restores a restic remote backup from the task list', async () => {
      const user = userEvent.setup()
      const verifyWarning = '未同时检测到 files/ 和 .mnemonas/，仅在恢复的是子目录时才适合直接切换 storage.root'
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        id: 'restic-remote',
        name: 'Restic 远端备份',
        type: 'restic',
        destination: 'rest:http://backup.example/repo',
        repository: 'rest:http://backup.example/repo',
        command: '/usr/bin/restic',
        include_config: true,
        max_snapshots: undefined,
        max_age: undefined,
        retention_policy: 'external: restic forget --keep-daily 7 --prune',
        retention_status: 'ok',
        retention_message: '远端保留策略已标记为外部管理',
        restore_drill_status: 'due',
        restore_drill_message: '尚未完成恢复演练',
        last_run: undefined,
        last_successful_run: undefined,
        last_restore_drill: undefined,
        last_restore: undefined,
        restore_history: undefined,
      }])
      mockPreviewBackupRestoreJob.mockResolvedValueOnce({
        id: '20260509T035900.000000000Z',
        job_id: 'restic-remote',
        status: 'completed',
        started_at: '2026-05-09T03:59:00Z',
        finished_at: '2026-05-09T03:59:01Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: 'rest:http://backup.example/repo',
        manifest_path: 'rest:http://backup.example/repo',
        target_path: '/restore/restic',
        file_count: 3,
        total_bytes: 2048,
        config_available: false,
        config_included: false,
        sample_paths: ['docs/note.txt'],
      })
      mockRestoreBackupJob.mockResolvedValueOnce({
        id: '20260509T040000.000000000Z',
        job_id: 'restic-remote',
        status: 'completed',
        started_at: '2026-05-09T04:00:00Z',
        finished_at: '2026-05-09T04:00:01Z',
        duration_ms: 1000,
        manifest_path: 'rest:http://backup.example/repo',
        target_path: '/restore/restic',
        config_restored: false,
        file_count: 3,
        verified_bytes: 2048,
      })
      mockVerifyBackupRestoreJob.mockResolvedValueOnce({
        id: '20260509T040005.000000000Z',
        job_id: 'restic-remote',
        status: 'completed',
        started_at: '2026-05-09T04:00:05Z',
        finished_at: '2026-05-09T04:00:06Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: 'rest:http://backup.example/repo',
        target_path: '/restore/restic',
        file_count: 3,
        verified_bytes: 2048,
        config_found: false,
        files_dir_found: false,
        internal_dir_found: false,
        index_found: false,
        objects_dir_found: false,
        looks_like_storage_root: false,
        warnings: [verifyWarning],
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('Restic 远端备份')).toBeTruthy()
      })

      const restoreButton = screen.getByRole('button', { name: /^恢复$/ }) as HTMLButtonElement
      expect(restoreButton.disabled).toBe(false)
      await user.click(restoreButton)

      await waitFor(() => {
        expect(screen.getByText('恢复备份到目录')).toBeTruthy()
        expect(screen.queryByText('同时恢复备份中的配置文件')).toBeNull()
      })

      fireEvent.change(screen.getByLabelText('目标目录'), { target: { value: '/restore/restic' } })
      await user.click(screen.getByRole('button', { name: /生成预览/ }))
      await waitFor(() => {
        expect(mockPreviewBackupRestoreJob).toHaveBeenCalledWith('restic-remote', '/restore/restic', false, expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
      })
      await user.click(screen.getByRole('button', { name: /开始恢复/ }))

      await waitFor(() => {
        expect(mockRestoreBackupJob).toHaveBeenCalledWith('restic-remote', '/restore/restic', false, expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockVerifyBackupRestoreJob).toHaveBeenCalledWith('restic-remote', '/restore/restic', expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '备份已恢复',
          description: '3 个文件 · 2 KB，目标：/restore/restic',
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '恢复目录检查完成，有警告',
          description: verifyWarning,
          color: 'warning',
        }))
        expect(screen.getByText(verifyWarning)).toBeTruthy()
      })
    })

    it('allows remote restore checks before a local snapshot exists', async () => {
      const user = userEvent.setup()
      const remoteDrill = {
        id: '20260509T030000.000000000Z',
        job_id: 'restic-remote',
        status: 'completed',
        started_at: '2026-05-09T03:00:00Z',
        finished_at: '2026-05-09T03:00:01Z',
        duration_ms: 1000,
        manifest_path: 'rest:http://backup.example/repo',
        artifact_kept: false,
        file_count: 0,
        verified_bytes: 0,
      }
      mockListBackupJobs.mockResolvedValue([{
        ...mockBackupJobs[0],
        id: 'restic-remote',
        name: 'Restic 远端备份',
        type: 'restic',
        destination: 'rest:http://backup.example/repo',
        repository: 'rest:http://backup.example/repo',
        command: '/usr/bin/restic',
        max_snapshots: undefined,
        max_age: undefined,
        retention_status: 'warning',
        retention_message: '远端保留策略需要在外部工具中确认',
        restore_drill_status: 'due',
        restore_drill_message: '尚未完成恢复演练',
        last_run: undefined,
        last_successful_run: undefined,
        last_restore_drill: undefined,
        last_restore: undefined,
        restore_history: undefined,
      }])
      mockRunBackupRestoreDrill.mockResolvedValueOnce(remoteDrill)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('Restic 远端备份')).toBeTruthy()
        expect(screen.getByText('远端保留策略需要在外部工具中确认')).toBeTruthy()
        expect(screen.getByText('命令：/usr/bin/restic')).toBeTruthy()
      })

      const drillButton = screen.getByRole('button', { name: /恢复演练/ }) as HTMLButtonElement
      expect(drillButton.disabled).toBe(false)
      await user.click(drillButton)

      await waitFor(() => {
        expect(mockRunBackupRestoreDrill).toHaveBeenCalledWith('restic-remote', false, expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ description: '校验命令已完成' }))
      })
    })

    it('shows unavailable backup manager state', async () => {
      mockListBackupJobs.mockRejectedValue(new ApiError('backup unavailable', 503, 'SERVICE_UNAVAILABLE'))

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('备份任务暂不可用')).toBeTruthy()
        expect(screen.getByText('备份管理器当前不可用，请检查配置后重试。')).toBeTruthy()
      })
    })

    it('shows generic backup load failure state without exposing raw errors', async () => {
      mockListBackupJobs.mockRejectedValue(new Error('backup database timeout'))

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('加载备份任务失败')).toBeTruthy()
        expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeTruthy()
      })
      expect(screen.queryByText('backup database timeout')).toBeNull()
    })
  })

  describe('completed scrub', () => {
    it('shows completed status', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('校验完成')).toBeTruthy()
      })
    })

    it('shows total objects count', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        // Check for the total objects indicator
        expect(screen.getAllByText('1000').length).toBeGreaterThan(0)
        expect(screen.getByText('总对象数')).toBeTruthy()
      })
    })

    it('shows valid objects count', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('有效对象')).toBeTruthy()
      })
    })

    it('shows scrub description', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('数据完整性校验')).toBeTruthy()
      })
    })

    it('shows task id', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText(/任务 ID/)).toBeTruthy()
      })
    })

    it('shows unknown summary values when scrub metrics are missing', async () => {
      mockGetScrubResult.mockResolvedValue(mockIncompleteResult)
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('总对象数')).toBeTruthy()
        expect(screen.getAllByText('--').length).toBeGreaterThan(0)
      })
    })

    it('does not expose raw unknown scrub status values', async () => {
      mockGetScrubResult.mockResolvedValue({
        ...mockCompletedResult,
        status: 'backend_raw_scrub_status',
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('未知状态')).toBeTruthy()
      })
      expect(screen.queryByText('backend_raw_scrub_status')).toBeNull()
    })

    it('keeps warning state visible when the stored scrub result has a warning', async () => {
      mockGetScrubResult.mockResolvedValue({
        ...mockCompletedResult,
        warning: true,
        message: 'scrub completed with persistence warning',
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('校验完成（有警告）')).toBeTruthy()
        expect(screen.getByText('本次校验完成，但存在警告')).toBeTruthy()
        expect(screen.getByText('校验结果已完成，但历史记录保存不完整；建议下载诊断包并检查服务日志。')).toBeTruthy()
      })
      expect(screen.queryByText('scrub completed with persistence warning')).toBeNull()
    })

    it('does not expose raw unknown scrub result diagnostics', async () => {
      mockGetScrubResult.mockResolvedValue({
        ...mockCompletedResult,
        warning: true,
        message: 'backend raw scrub warning with token=secret-value',
        error_message: 'backend raw scrub error with password secret-value',
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getAllByText('数据校验结果已记录，请下载诊断包并检查服务日志。').length).toBeGreaterThanOrEqual(2)
      })
      expect(screen.queryByText(/backend raw scrub/)).toBeNull()
      expect(screen.queryByText(/secret-value/)).toBeNull()
    })
  })

  describe('running state', () => {
    beforeEach(() => {
      mockGetScrubResult.mockResolvedValue(mockRunningResult)
    })

    it('shows running status', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        // The action button switches to the running validation label.
        expect(screen.getAllByText('校验中...').length).toBeGreaterThan(0)
      })
    })

    it('shows progress indicator', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText(/正在校验数据完整性/)).toBeTruthy()
      })

      expect(screen.getByRole('progressbar', { name: '校验进行中' })).toHaveAttribute(
        'aria-valuetext',
        '正在校验数据完整性，已验证 450 / 1,000 个对象'
      )
    })

    it('disables start button when running', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        const buttons = screen.getAllByText('校验中...')
        expect(buttons.length).toBeGreaterThan(0)
      })
    })
  })

  describe('scrub with errors', () => {
    beforeEach(() => {
      mockGetScrubResult.mockResolvedValue(mockResultWithErrors)
    })

    it('shows corrupted objects count', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('损坏对象')).toBeTruthy()
        expect(screen.getByText('3')).toBeTruthy()
      })
    })

    it('shows missing objects count', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('缺失对象')).toBeTruthy()
        expect(screen.getByText('2')).toBeTruthy()
      })
    })

    it('shows error list', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText(/发现的问题/)).toBeTruthy()
      })
    })

    it('shows localized error types and details in list', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('校验不一致')).toBeTruthy()
        expect(screen.getByText('对象内容与索引记录不一致，请检查存储介质并从备份恢复。')).toBeTruthy()
        expect(screen.getByText('对象数据缺失，请从备份恢复受影响文件。')).toBeTruthy()
      })
      expect(screen.queryByText('corrupted')).toBeNull()
      expect(screen.queryByText('object failed integrity verification')).toBeNull()
      expect(screen.queryByText('object is missing')).toBeNull()
    })
  })

  describe('start scrub', () => {
    beforeEach(() => {
      mockGetScrubResult.mockResolvedValue(mockNoResult)
    })

    it('starts scrub on button click', async () => {
      const user = userEvent.setup()
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('开始校验')).toBeTruthy()
      })

      await user.click(screen.getByText('开始校验'))

      await waitFor(() => {
        expect(mockRunScrub).toHaveBeenCalled()
      })

      expect(mockAddToast).toHaveBeenCalledWith({
        title: '数据校验已完成',
        color: 'success',
      })
    })

    it('shows warning toast when scrub completes with a persistence warning', async () => {
      const user = userEvent.setup()
      mockRunScrub.mockResolvedValue({
        ...mockCompletedResult,
        warning: true,
        message: 'scrub completed with persistence warning',
      })

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('开始校验')).toBeTruthy()
      })

      await user.click(screen.getByText('开始校验'))

      await waitFor(() => {
        expect(mockRunScrub).toHaveBeenCalled()
      })

      expect(mockAddToast).toHaveBeenCalledWith({
        title: '数据校验完成，但存在警告',
        color: 'warning',
      })
    })

    it('keeps the start button disabled until the running result refresh completes', async () => {
      const user = userEvent.setup()
      const runningRefresh = createDeferred<typeof mockRunningResult>()

      mockGetScrubResult
        .mockResolvedValueOnce(mockNoResult)
        .mockImplementationOnce(() => runningRefresh.promise)
      mockRunScrub.mockResolvedValue(mockRunningResult)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('开始校验')).toBeTruthy()
      })

      await user.click(screen.getByText('开始校验'))

      await waitFor(() => {
        expect(mockRunScrub).toHaveBeenCalledTimes(1)
        expect(screen.getByRole('button', { name: '校验中...' })).toBeDisabled()
      })

      await user.click(screen.getByRole('button', { name: '校验中...' }))
      expect(mockRunScrub).toHaveBeenCalledTimes(1)

      await act(async () => {
        runningRefresh.resolve(mockRunningResult)
        await runningRefresh.promise
      })

      await waitFor(() => {
        expect(mockGetScrubResult).toHaveBeenCalledTimes(2)
      })
    })
  })

  describe('diagnostics export', () => {
    it('shows success feedback when export starts', async () => {
      const user = userEvent.setup()
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('下载诊断包')).toBeTruthy()
      })

      await user.click(screen.getByText('下载诊断包'))

      await waitFor(() => {
        expect(mockDownloadDiagnosticsExport).toHaveBeenCalledWith(expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
      })

      expect(mockAddToast).toHaveBeenCalledWith({
        title: '诊断信息导出已开始',
        color: 'success',
      })
    })

    it('aborts pending diagnostics export when the page unmounts and ignores abort feedback', async () => {
      const user = userEvent.setup()
      const exportRequest = createDeferred<void>()
      let signal: AbortSignal | undefined
      mockDownloadDiagnosticsExport.mockImplementationOnce((options) => {
        signal = options?.signal
        return exportRequest.promise
      })
      const view = render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('下载诊断包')).toBeTruthy()
      })

      await user.click(screen.getByText('下载诊断包'))

      await waitFor(() => {
        expect(signal).toBeInstanceOf(AbortSignal)
      })

      view.unmount()
      expect(signal?.aborted).toBe(true)
      exportRequest.reject(new DOMException('export aborted', 'AbortError'))

      await waitFor(() => {
        expect(mockAddToast).not.toHaveBeenCalled()
      })
    })

    it('shows error feedback when export fails', async () => {
      const user = userEvent.setup()
      mockDownloadDiagnosticsExport.mockRejectedValue(new Error('磁盘不可用'))
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('下载诊断包')).toBeTruthy()
      })

      await user.click(screen.getByText('下载诊断包'))

      await waitFor(() => {
        expect(mockDownloadDiagnosticsExport).toHaveBeenCalled()
      })

      expect(mockAddToast).toHaveBeenCalledWith({
        title: '下载诊断包失败',
        description: '操作未完成，请稍后重试。',
        color: 'danger',
      })
    })

    it('shows an unavailable warning when diagnostics export is temporarily unavailable', async () => {
      const user = userEvent.setup()
      mockDownloadDiagnosticsExport.mockRejectedValue(new ApiError('diagnostics unavailable', 503, 'SERVICE_UNAVAILABLE'))
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('下载诊断包')).toBeTruthy()
      })

      await user.click(screen.getByText('下载诊断包'))

      await waitFor(() => {
        expect(mockDownloadDiagnosticsExport).toHaveBeenCalled()
      })

      expect(mockAddToast).toHaveBeenCalledWith({
        title: '诊断包暂不可用',
        description: '诊断包服务当前不可用，请检查设备状态后重试。',
        color: 'warning',
      })
    })
  })

  describe('info card', () => {
    it('displays info about scrub process', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('维护建议')).toBeTruthy()
      })
    })

    it('shows blake3 info in header', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText(/检查已存数据是否仍可正确读取/)).toBeTruthy()
      })
    })
  })

  describe('error handling', () => {
    it('shows an unavailable state when scrub history is temporarily unavailable', async () => {
      mockGetScrubResult.mockRejectedValue(new ApiError('maintenance history not initialized', 503, 'SERVICE_UNAVAILABLE'))
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('校验结果暂不可用')).toBeTruthy()
        expect(screen.getByText('维护历史或数据面当前不可用，请检查设备状态或稍后重试。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('shows retryable error state when loading scrub result fails', async () => {
      mockGetScrubResult.mockRejectedValue(new Error('Network error'))
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('加载校验结果失败')).toBeTruthy()
        expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('shows success toast when scrub result reload succeeds', async () => {
      const user = userEvent.setup()
      mockGetScrubResult
        .mockRejectedValueOnce(new Error('Network error'))
        .mockResolvedValueOnce(mockNoResult)
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '校验结果已刷新', color: 'success' })
      })
    })

    it('shows warning toast when scrub result reload becomes unavailable', async () => {
      const user = userEvent.setup()
      mockGetScrubResult
        .mockRejectedValueOnce(new Error('Network error'))
        .mockRejectedValueOnce(new ApiError('maintenance history not initialized', 503, 'SERVICE_UNAVAILABLE'))
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '校验结果暂不可用',
          description: '维护历史或数据面当前不可用，请检查设备状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('handles start scrub error', async () => {
      mockGetScrubResult.mockResolvedValue(mockNoResult)
      mockRunScrub.mockRejectedValue(new Error('Already running'))
      const user = userEvent.setup()
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('开始校验')).toBeTruthy()
      })

      await user.click(screen.getByText('开始校验'))

      // Should not crash
      await waitFor(() => {
        expect(mockRunScrub).toHaveBeenCalled()
      })

      expect(mockAddToast).toHaveBeenCalledWith({
        title: '启动校验失败',
        description: '操作未完成，请稍后重试。',
        color: 'danger',
      })
    })

    it('shows an unavailable warning when starting scrub is temporarily unavailable', async () => {
      mockGetScrubResult.mockResolvedValue(mockNoResult)
      mockRunScrub.mockRejectedValue(new ApiError('dataplane not connected', 503, 'SERVICE_UNAVAILABLE'))
      const user = userEvent.setup()
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('开始校验')).toBeTruthy()
      })

      await user.click(screen.getByText('开始校验'))

      await waitFor(() => {
        expect(mockRunScrub).toHaveBeenCalled()
      })

      expect(mockAddToast).toHaveBeenCalledWith({
        title: '数据校验暂不可用',
        description: '数据面或维护服务当前不可用，请检查设备状态后重试。',
        color: 'warning',
      })
    })

    it('shows a running-state warning for normalized already-running scrub errors', async () => {
      mockGetScrubResult
        .mockResolvedValueOnce(mockNoResult)
        .mockResolvedValueOnce(mockRunningResult)
      mockRunScrub.mockRejectedValue(new ApiError('  Scrub Is Already Running  ', 409))
      const user = userEvent.setup()

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('开始校验')).toBeTruthy()
      })

      await user.click(screen.getByText('开始校验'))

      await waitFor(() => {
        expect(mockRunScrub).toHaveBeenCalled()
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '数据校验已在运行',
          description: '已有校验任务正在执行，已同步最新状态。',
          color: 'warning',
        })
      })

      await waitFor(() => {
        expect(screen.getAllByText('校验中...').length).toBeGreaterThan(0)
      })
    })
  })
})
