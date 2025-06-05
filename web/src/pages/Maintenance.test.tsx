import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act } from '@testing-library/react'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import Maintenance from './Maintenance'

const mockAddToast = vi.fn()

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
  runBackupRestoreDrill: vi.fn(),
  restoreBackupJob: vi.fn(),
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
  runBackupRestoreDrill,
  restoreBackupJob,
} from '@/api/files'

const mockGetScrubResult = getScrubResult as ReturnType<typeof vi.fn>
const mockRunScrub = runScrub as ReturnType<typeof vi.fn>
const mockDownloadDiagnosticsExport = downloadDiagnosticsExport as ReturnType<typeof vi.fn>
const mockListBackupJobs = listBackupJobs as ReturnType<typeof vi.fn>
const mockRunBackupJob = runBackupJob as ReturnType<typeof vi.fn>
const mockRunBackupRestoreDrill = runBackupRestoreDrill as ReturnType<typeof vi.fn>
const mockRestoreBackupJob = restoreBackupJob as ReturnType<typeof vi.fn>

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
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
    max_snapshots: 7,
    max_age: '720h0m0s',
    health_status: 'ok',
    health_message: 'last successful backup completed recently',
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
    },
  }]

  beforeEach(() => {
    vi.clearAllMocks()
    mockUser.id = 'u1'
    mockUser.username = 'admin'
    mockUser.role = 'admin'
    mockUser.email = 'admin@local'
    mockUser.homeDir = '/'
    mockGetScrubResult.mockResolvedValue(mockCompletedResult)
    mockRunScrub.mockResolvedValue(mockCompletedResult)
    mockDownloadDiagnosticsExport.mockResolvedValue(undefined)
    mockListBackupJobs.mockResolvedValue([])
    mockRunBackupJob.mockResolvedValue(mockBackupJobs[0].last_run)
    mockRunBackupRestoreDrill.mockResolvedValue(mockBackupJobs[0].last_restore_drill)
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
    })
  })

  describe('header', () => {
    it('displays page title', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('系统维护')).toBeTruthy()
      })
    })

    it('displays subtitle', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('数据校验、备份与诊断工具')).toBeTruthy()
      })
    })

    it('renders export button', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('导出诊断信息')).toBeTruthy()
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
      })
    })

    it('shows configured backup jobs and latest results', async () => {
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
        expect(screen.getByText('/mnt/backup-drive/mnemonas')).toBeTruthy()
        expect(screen.getByText('每 1 天')).toBeTruthy()
        expect(screen.getByText('自动窗口: 02:00-05:00')).toBeTruthy()
        expect(screen.getByText('最多 7 个快照 · 最长 30 天')).toBeTruthy()
        expect(screen.getByText('健康')).toBeTruthy()
        expect(screen.getAllByText(/12 个文件 · 4 KB/).length).toBeGreaterThan(0)
        expect(screen.getByText('校验 12 个文件 · 4 KB')).toBeTruthy()
      })
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
      const drillButton = screen.getByRole('button', { name: /恢复演练/ }) as HTMLButtonElement
      expect(runButton.disabled).toBe(true)
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
        expect(mockRunBackupJob).toHaveBeenCalledWith('external-disk')
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '备份已完成' }))
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
        expect(mockRunBackupRestoreDrill).toHaveBeenCalledWith('external-disk', false)
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '恢复演练已完成' }))
      })
    })

    it('restores a local backup from the task list', async () => {
      const user = userEvent.setup()
      mockListBackupJobs.mockResolvedValue(mockBackupJobs)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('外置硬盘备份')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /^恢复$/ }))

      await waitFor(() => {
        expect(screen.getByText('恢复备份到目录')).toBeTruthy()
      })

      await user.type(screen.getByLabelText('目标目录'), '/restore/mnemonas')
      await user.click(screen.getByRole('button', { name: /开始恢复/ }))

      await waitFor(() => {
        expect(mockRestoreBackupJob).toHaveBeenCalledWith('external-disk', '/restore/mnemonas', true)
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '备份已恢复' }))
      })
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
        last_run: undefined,
        last_successful_run: undefined,
        last_restore_drill: undefined,
      }])
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

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('Rclone 远端备份')).toBeTruthy()
        expect(screen.getByText('远端保留策略由外部工具管理')).toBeTruthy()
      })

      const restoreButton = screen.getByRole('button', { name: /^恢复$/ }) as HTMLButtonElement
      expect(restoreButton.disabled).toBe(false)
      await user.click(restoreButton)

      await waitFor(() => {
        expect(screen.getByText('恢复备份到目录')).toBeTruthy()
        expect(screen.queryByText('同时恢复备份中的配置文件')).toBeNull()
      })

      await user.type(screen.getByLabelText('目标目录'), '/restore/rclone')
      await user.click(screen.getByRole('button', { name: /开始恢复/ }))

      await waitFor(() => {
        expect(mockRestoreBackupJob).toHaveBeenCalledWith('rclone-remote', '/restore/rclone', false)
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '备份已恢复',
          description: '恢复命令已完成，目标: /restore/rclone',
        }))
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
        max_snapshots: 7,
        max_age: '720h0m0s',
        last_run: undefined,
        last_successful_run: undefined,
        last_restore_drill: undefined,
      }])
      mockRunBackupRestoreDrill.mockResolvedValueOnce(remoteDrill)

      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('Restic 远端备份')).toBeTruthy()
        expect(screen.getByText('远端保留策略由外部工具管理')).toBeTruthy()
        expect(screen.getByText('命令: /usr/bin/restic')).toBeTruthy()
      })

      const drillButton = screen.getByRole('button', { name: /恢复演练/ }) as HTMLButtonElement
      expect(drillButton.disabled).toBe(false)
      await user.click(drillButton)

      await waitFor(() => {
        expect(mockRunBackupRestoreDrill).toHaveBeenCalledWith('restic-remote', false)
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
        expect(screen.getByText('scrub completed with persistence warning')).toBeTruthy()
      })
    })
  })

  describe('running state', () => {
    beforeEach(() => {
      mockGetScrubResult.mockResolvedValue(mockRunningResult)
    })

    it('shows running status', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        // The button shows "校验中..." when running
        expect(screen.getAllByText('校验中...').length).toBeGreaterThan(0)
      })
    })

    it('shows progress indicator', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText(/正在校验数据完整性/)).toBeTruthy()
      })
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

    it('shows error type in list', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('corrupted')).toBeTruthy()
      })
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
        title: 'scrub completed with persistence warning',
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
        const runningButtons = screen.getAllByText('校验中...')
        expect(runningButtons.length).toBeGreaterThan(0)
        const startButton = runningButtons.find((button) => button.closest('button'))?.closest('button') as HTMLButtonElement | undefined
        expect(startButton?.disabled).toBe(true)
      })

      const runningButtons = screen.getAllByText('校验中...')
      const pendingStartButton = runningButtons.find((button) => button.closest('button'))?.closest('button') as HTMLButtonElement | undefined
      if (pendingStartButton) {
        await user.click(pendingStartButton)
      }
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
        expect(screen.getByText('导出诊断信息')).toBeTruthy()
      })

      await user.click(screen.getByText('导出诊断信息'))

      await waitFor(() => {
        expect(mockDownloadDiagnosticsExport).toHaveBeenCalled()
      })

      expect(mockAddToast).toHaveBeenCalledWith({
        title: '诊断信息导出已开始',
        color: 'success',
      })
    })

    it('shows error feedback when export fails', async () => {
      const user = userEvent.setup()
      mockDownloadDiagnosticsExport.mockRejectedValue(new Error('磁盘不可用'))
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('导出诊断信息')).toBeTruthy()
      })

      await user.click(screen.getByText('导出诊断信息'))

      await waitFor(() => {
        expect(mockDownloadDiagnosticsExport).toHaveBeenCalled()
      })

      expect(mockAddToast).toHaveBeenCalledWith({
        title: '导出诊断信息失败',
        description: '磁盘不可用',
        color: 'danger',
      })
    })

    it('shows an unavailable warning when diagnostics export is temporarily unavailable', async () => {
      const user = userEvent.setup()
      mockDownloadDiagnosticsExport.mockRejectedValue(new ApiError('diagnostics unavailable', 503, 'SERVICE_UNAVAILABLE'))
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('导出诊断信息')).toBeTruthy()
      })

      await user.click(screen.getByText('导出诊断信息'))

      await waitFor(() => {
        expect(mockDownloadDiagnosticsExport).toHaveBeenCalled()
      })

      expect(mockAddToast).toHaveBeenCalledWith({
        title: '诊断导出暂不可用',
        description: '诊断导出服务当前不可用，请检查系统状态后重试。',
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
        // The BLAKE3 text is in the header description: "验证所有存储对象的 BLAKE3 哈希值"
        expect(screen.getByText(/验证所有存储对象/)).toBeTruthy()
      })
    })
  })

  describe('error handling', () => {
    it('shows an unavailable state when scrub history is temporarily unavailable', async () => {
      mockGetScrubResult.mockRejectedValue(new ApiError('maintenance history not initialized', 503, 'SERVICE_UNAVAILABLE'))
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('校验结果暂不可用')).toBeTruthy()
        expect(screen.getByText('维护历史或数据面当前不可用，请检查系统状态或稍后重试。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('shows retryable error state when loading scrub result fails', async () => {
      mockGetScrubResult.mockRejectedValue(new Error('Network error'))
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('加载校验结果失败')).toBeTruthy()
        expect(screen.getByText('Network error')).toBeTruthy()
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
          description: '维护历史或数据面当前不可用，请检查系统状态或稍后重试。',
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
        description: 'Already running',
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
        description: '数据面或维护服务当前不可用，请检查系统状态后重试。',
        color: 'warning',
      })
    })

    it('shows a running-state warning and refreshes status when scrub is already running', async () => {
      mockGetScrubResult
        .mockResolvedValueOnce(mockNoResult)
        .mockResolvedValueOnce(mockRunningResult)
      mockRunScrub.mockRejectedValue(new ApiError('scrub is already running', 409))
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
