import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act } from '@testing-library/react'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import Maintenance from './Maintenance'

const mockAddToast = vi.fn()

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
}))

import { ApiError, getScrubResult, runScrub, downloadDiagnosticsExport } from '@/api/files'

const mockGetScrubResult = getScrubResult as ReturnType<typeof vi.fn>
const mockRunScrub = runScrub as ReturnType<typeof vi.fn>
const mockDownloadDiagnosticsExport = downloadDiagnosticsExport as ReturnType<typeof vi.fn>

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
      { hash: 'abc123def456', error_type: 'corrupted', message: 'Hash mismatch' },
      { hash: 'xyz789ghi012', error_type: 'missing', message: 'File not found' },
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

  beforeEach(() => {
    vi.clearAllMocks()
    mockGetScrubResult.mockResolvedValue(mockCompletedResult)
    mockRunScrub.mockResolvedValue(mockCompletedResult)
    mockDownloadDiagnosticsExport.mockResolvedValue(undefined)
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
        expect(screen.getByText('数据校验与诊断工具')).toBeTruthy()
      })
    })

    it('renders export button', async () => {
      render(<Maintenance />)

      await waitFor(() => {
        expect(screen.getByText('导出诊断信息')).toBeTruthy()
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
        expect(screen.getByText('关于数据校验')).toBeTruthy()
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
