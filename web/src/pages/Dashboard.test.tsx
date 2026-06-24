import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { DashboardPage } from './Dashboard'
import * as HeroUI from '@heroui/react'

const useIsAdminMock = vi.fn(() => true)
const useUserMock = vi.fn(() => ({ id: 'admin-id', username: 'admin', role: 'admin' }))
const mockAddToast = vi.fn()

// Mock the API functions
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
  getHealth: vi.fn().mockResolvedValue({
    status: 'healthy',
    uptime: '1h30m',
    uptimeSecs: 5400,
  }),
  getAppVersion: vi.fn().mockResolvedValue({
    name: 'MnemoNAS',
    version: '0.1.0',
    go: 'go1.24.0',
  }),
  getStorageStats: vi.fn().mockResolvedValue({
    fileCount: 42,
    fileCountAvailable: true,
    storageStatsAvailable: true,
    totalSize: 1073741824, // 1 GB
    totalObjects: 100,
    dedupRatio: 1.5,
    diskStatsAvailable: true,
    diskTotal: 21474836480, // 20 GB
    diskAvailable: 20401094656, // 19 GB
    diskUsed: 1073741824, // 1 GB
    diskUsageRatio: 0.05,
  }),
  listBackupJobs: vi.fn().mockResolvedValue([{
    id: 'external-disk',
    name: '外置硬盘备份',
    type: 'local',
    disabled: false,
    health_status: 'ok',
    retention_status: 'ok',
    restore_drill_status: 'ok',
    running: false,
    last_successful_run: {
      status: 'completed',
      started_at: '2026-05-18T10:00:00Z',
      finished_at: '2026-05-18T10:05:00Z',
    },
  }]),
}))

vi.mock('@/api/activity', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/activity')>()
  return {
    ...actual,
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
    listActivity: vi.fn().mockResolvedValue({
      items: [
        { id: 'act-1', action: 'upload', path: '/docs/report.pdf', timestamp: '2024-01-15T10:00:00Z' },
      ],
    }),
  }
})

vi.mock('@/api/setup', () => ({
  SETUP_DEFER_DAYS: [1, 3, 7, 30],
  SetupError: class SetupError extends Error {
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
  acknowledgeSetup: vi.fn(),
  deferSetup: vi.fn(),
  getSetupReadiness: vi.fn(),
}))

// Mock navigation
const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useIsAdmin: () => useIsAdminMock(),
    useUser: () => useUserMock(),
  }
})

import { ApiError as FilesApiError, getAppVersion, getHealth, getStorageStats, listBackupJobs } from '@/api/files'
import { ApiError, listActivity } from '@/api/activity'
import {
  acknowledgeSetup,
  deferSetup,
  getSetupReadiness,
  type SetupReadiness,
} from '@/api/setup'

const mockGetHealth = getHealth as ReturnType<typeof vi.fn>
const mockGetAppVersion = getAppVersion as ReturnType<typeof vi.fn>
const mockGetStorageStats = getStorageStats as ReturnType<typeof vi.fn>
const mockListBackupJobs = listBackupJobs as ReturnType<typeof vi.fn>
const mockListActivity = listActivity as ReturnType<typeof vi.fn>
const mockAcknowledgeSetup = acknowledgeSetup as ReturnType<typeof vi.fn>
const mockDeferSetup = deferSetup as ReturnType<typeof vi.fn>
const mockGetSetupReadiness = getSetupReadiness as ReturnType<typeof vi.fn>

function createSetupReadiness(overrides: Partial<SetupReadiness> = {}): SetupReadiness {
  return {
    lifecycle: 'pending',
    overall_status: 'action_required',
    prompt: true,
    generated_at: '2026-07-13T01:02:03Z',
    can_complete: false,
    can_defer: true,
    required: { completed: 1, total: 2 },
    recommended: { completed: 0, total: 1 },
    checks: [
      {
        id: 'admin_access',
        requirement: 'required',
        status: 'complete',
        deferrable: false,
        title: '管理员访问可用',
        message: '至少有一个启用中的管理员账号。',
        action: 'manage_users',
      },
      {
        id: 'backup_job',
        requirement: 'required',
        status: 'incomplete',
        deferrable: true,
        title: '添加独立备份',
        message: '尚未添加启用中的备份任务。',
        action: 'create_backup',
      },
      {
        id: 'restore_verification',
        requirement: 'recommended',
        status: 'incomplete',
        deferrable: true,
        title: '验证恢复能力',
        message: '建议执行一次恢复演练并保持验证结果有效。',
        action: 'run_restore_drill',
      },
    ],
    summary: {
      auth_enabled: true,
      active_admin_count: 1,
      password_change_required_admin_count: 0,
      initial_password_file: 'missing',
      enabled_backup_job_count: 0,
      security_status: 'warning',
      security_blocking_check_ids: [],
    },
    ...overrides,
  }
}

function completedSetupReadiness(): SetupReadiness {
  return createSetupReadiness({
    lifecycle: 'completed',
    overall_status: 'ready',
    prompt: false,
    completed_at: '2026-07-13T01:03:00Z',
    can_complete: false,
    can_defer: false,
    required: { completed: 2, total: 2 },
  })
}

function deferredSetupReadiness(): SetupReadiness {
  return createSetupReadiness({
    lifecycle: 'deferred',
    prompt: false,
    deferred_until: '2026-07-20T01:02:03Z',
    can_complete: false,
    can_defer: false,
  })
}

function expectListActivityCalledWithSignal(options: { limit?: number }) {
  const call = mockListActivity.mock.calls.find(([calledOptions]) => calledOptions?.limit === options.limit)
  expect(call).toBeTruthy()
  expect((call?.[0] as { signal?: AbortSignal } | undefined)?.signal).toBeInstanceOf(AbortSignal)
}

function expectCalledWithAbortSignal(mockFn: ReturnType<typeof vi.fn>) {
  const call = mockFn.mock.calls.find(([options]) => {
    return (options as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
  })
  expect(call).toBeTruthy()
  expect(Object.keys((call?.[0] ?? {}) as Record<string, unknown>).sort()).toEqual(['signal'])
}

describe('DashboardPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
    useIsAdminMock.mockReturnValue(true)
    useUserMock.mockReturnValue({ id: 'admin-id', username: 'admin', role: 'admin' })
    mockNavigate.mockClear()
    // Reset mocks to default values (vi.clearAllMocks clears mockResolvedValue)
    mockGetHealth.mockResolvedValue({
      status: 'healthy',
      uptime: '1h30m',
      uptimeSecs: 5400,
    })
    mockGetAppVersion.mockResolvedValue({
      name: 'MnemoNAS',
      version: '0.1.0',
      go: 'go1.24.0',
    })
    mockGetStorageStats.mockResolvedValue({
      fileCount: 42,
      fileCountAvailable: true,
      storageStatsAvailable: true,
      totalSize: 1073741824, // 1 GB
      totalObjects: 100,
      dedupRatio: 1.5,
      diskStatsAvailable: true,
      diskTotal: 21474836480, // 20 GB
      diskAvailable: 20401094656, // 19 GB
      diskUsed: 1073741824, // 1 GB
      diskUsageRatio: 0.05,
    })
    mockListBackupJobs.mockResolvedValue([{
      id: 'external-disk',
      name: '外置硬盘备份',
      type: 'local',
      disabled: false,
      health_status: 'ok',
      retention_status: 'ok',
      restore_drill_status: 'ok',
      running: false,
      last_successful_run: {
        status: 'completed',
        started_at: '2026-05-18T10:00:00Z',
        finished_at: '2026-05-18T10:05:00Z',
      },
    }])
    mockListActivity.mockResolvedValue({
      items: [
        { id: 'act-1', action: 'upload', path: '/docs/report.pdf', timestamp: '2024-01-15T10:00:00Z' },
      ],
    })
    mockGetSetupReadiness.mockResolvedValue(completedSetupReadiness())
    mockAcknowledgeSetup.mockResolvedValue(completedSetupReadiness())
    mockDeferSetup.mockResolvedValue(deferredSetupReadiness())
  })

  describe('loading state', () => {
    it('renders loading state initially', () => {
      mockGetHealth.mockImplementation(() => new Promise(() => {}))
      mockGetStorageStats.mockImplementation(() => new Promise(() => {}))
      
      render(<DashboardPage />)
      expect(screen.getByRole('status', { name: '加载首页' })).toBeInTheDocument()
    })

    it('shows skeleton placeholders while loading', () => {
      mockGetHealth.mockImplementation(() => new Promise(() => {}))
      mockGetStorageStats.mockImplementation(() => new Promise(() => {}))
      
      render(<DashboardPage />)
      expect(screen.getByRole('status', { name: '加载首页' })).toBeInTheDocument()
      expect(screen.queryByRole('heading', { name: '首页' })).not.toBeInTheDocument()
    })
  })

  describe('content rendering', () => {
    it('passes abort signals to dashboard status queries', async () => {
      render(<DashboardPage />)

      await waitFor(() => {
        expectCalledWithAbortSignal(mockGetHealth)
        expectCalledWithAbortSignal(mockGetStorageStats)
        expectCalledWithAbortSignal(mockGetAppVersion)
        expectListActivityCalledWithSignal({ limit: 5 })
        expectCalledWithAbortSignal(mockListBackupJobs)
        expectCalledWithAbortSignal(mockGetSetupReadiness)
      })
    })

    it('renders dashboard header after loading', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('首页')).toBeTruthy()
      })
    })

    it('displays system status indicator', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('运行正常')).toBeTruthy()
      })
    })

    it('shows unhealthy status when system is not healthy', async () => {
      mockGetHealth.mockResolvedValueOnce({
        status: 'unhealthy',
        uptime: '1h30m',
      })
      
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('异常')).toBeTruthy()
      })
    })

    it('shows degraded status when system is degraded', async () => {
      mockGetHealth.mockResolvedValueOnce({
        status: 'degraded',
        uptime: '1h30m',
      })

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('已降级')).toBeTruthy()
      })
    })

    it('displays storage statistics', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('存储使用')).toBeTruthy()
        expect(screen.getByText('文件数量')).toBeTruthy()
        expect(screen.getByText('备份状态')).toBeTruthy()
        expect(screen.getByText('运行时间')).toBeTruthy()
      })
    })

    it('displays formatted storage size', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        // 1073741824 bytes = 1 GB
        expect(screen.getAllByText(/1.*GB|GB/i).length).toBeGreaterThan(0)
      })
    })

    it('displays file count', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('42')).toBeTruthy()
      })
    })

    it('displays uptime', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('1 小时 30 分钟')).toBeTruthy()
      })
    })

    it('displays backup status on the home page', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('正常')).toBeTruthy()
        expect(screen.getByText(/最近备份/)).toBeTruthy()
      })
    })

    it('surfaces backup issues from the overview', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockListBackupJobs.mockResolvedValueOnce([{
        id: 'external-disk',
        name: '外置硬盘备份',
        type: 'local',
        disabled: false,
        health_status: 'failed',
        retention_status: 'ok',
        restore_drill_status: 'ok',
        running: false,
        last_run: { status: 'failed' },
      }])

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('备份需要查看')).toBeTruthy()
        expect(screen.getByText('建议：运行立即备份并查看最近备份结果')).toBeTruthy()
        expect(screen.getByRole('button', { name: '打开备份' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '打开备份' }))
      expect(mockNavigate).toHaveBeenCalledWith('/maintenance')
    })

    it('surfaces completed restores that still need matching verification', async () => {
      mockListBackupJobs.mockResolvedValueOnce([{
        id: 'external-disk',
        name: '外置硬盘备份',
        type: 'local',
        disabled: false,
        health_status: 'ok',
        retention_status: 'ok',
        restore_drill_status: 'ok',
        running: false,
        last_run: { status: 'completed' },
        last_restore: {
          status: 'completed',
          target_path: '/restore/mnemonas',
        },
        last_matching_restore_verify: undefined,
      }])

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('备份需要查看')).toBeTruthy()
        expect(screen.getByText('1 项待处理')).toBeTruthy()
        expect(screen.getAllByText('恢复待校验').length).toBeGreaterThan(0)
        expect(screen.getByText('建议：运行检查恢复完成只读校验')).toBeTruthy()
      })
    })

    it('counts warning-only backup jobs as attention items', async () => {
      mockListBackupJobs.mockResolvedValueOnce([{
        id: 'external-disk',
        name: '外置硬盘备份',
        type: 'local',
        disabled: false,
        health_status: 'ok',
        retention_status: 'ok',
        restore_drill_status: 'ok',
        running: false,
        last_run: {
          status: 'completed',
          warning: true,
          warnings: ['backup completed with warnings'],
        },
        last_restore: {
          status: 'completed',
          target_path: '/restore/mnemonas',
          warnings: ['restore completed with warnings'],
        },
        last_matching_restore_verify: {
          status: 'completed',
          warnings: [],
        },
      }])

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('备份需要查看')).toBeTruthy()
        expect(screen.getByText('1 项待处理')).toBeTruthy()
        expect(screen.getAllByText('最近备份有警告、最近恢复有警告').length).toBeGreaterThan(0)
        expect(screen.getByText('建议：运行立即备份并查看最近备份结果、导出恢复摘要并复核恢复警告')).toBeTruthy()
      })
    })

    it('shows disk capacity guidance when disk stats are available', async () => {
      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText(/可用 .* 总容量/)).toBeTruthy()
        expect(screen.getByText('磁盘总量')).toBeTruthy()
      })
    })

    it('surfaces critical disk space risk from the overview', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetStorageStats.mockResolvedValueOnce({
        fileCount: 42,
        fileCountAvailable: true,
        storageStatsAvailable: true,
        totalSize: 1073741824,
        totalObjects: 100,
        dedupRatio: 1.5,
        diskStatsAvailable: true,
        diskTotal: 21474836480,
        diskAvailable: 512 * 1024 * 1024,
        diskUsed: 20937965568,
        diskUsageRatio: 0.975,
      })

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('存储空间严重不足')).toBeTruthy()
          expect(screen.getByText(/尽快清理回收站/)).toBeTruthy()
          expect(screen.getByRole('button', { name: '查看存储' })).toBeTruthy()
        })

      await user.click(screen.getByRole('button', { name: '查看存储' }))
      expect(mockNavigate).toHaveBeenCalledWith('/storage')
    })

    it('surfaces warning disk space risk from the overview', async () => {
      mockGetStorageStats.mockResolvedValueOnce({
        fileCount: 42,
        fileCountAvailable: true,
        storageStatsAvailable: true,
        totalSize: 1073741824,
        totalObjects: 100,
        dedupRatio: 1.5,
        diskStatsAvailable: true,
        diskTotal: 21474836480,
        diskAvailable: 2147483648,
        diskUsed: 19327352832,
        diskUsageRatio: 0.9,
      })

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('存储空间偏紧')).toBeTruthy()
      })
    })

    it('falls back to CAS guidance when disk stats are unavailable', async () => {
      mockGetStorageStats.mockResolvedValueOnce({
        fileCount: 42,
        fileCountAvailable: true,
        storageStatsAvailable: true,
        totalSize: 1073741824,
        totalObjects: 100,
        dedupRatio: 1.5,
      })
      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('磁盘容量统计不可用，仅显示 CAS 数据')).toBeTruthy()
      })
    })

    it('renders server-derived required and recommended evidence without subjective checkboxes', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSetupReadiness.mockResolvedValueOnce(createSetupReadiness())

      render(<DashboardPage />)

      const setupCard = await screen.findByRole('region', { name: '首次设置检查' })
      expect(within(setupCard).getByText('必需 1/2')).toBeInTheDocument()
      expect(within(setupCard).getByText('建议 0/1')).toBeInTheDocument()
      expect(within(setupCard).queryByRole('checkbox')).not.toBeInTheDocument()

      const disclosure = within(setupCard).getByRole('button', { name: '查看检测结果' })
      expect(disclosure).toHaveAttribute('aria-expanded', 'false')
      await user.click(disclosure)

      expect(within(setupCard).getByRole('heading', { name: '必需项目' })).toBeInTheDocument()
      expect(within(setupCard).getByRole('heading', { name: '建议项目' })).toBeInTheDocument()
      expect(within(setupCard).getByText('尚未添加启用中的备份任务。')).toBeInTheDocument()
      expect(within(setupCard).getByText('建议执行一次恢复演练并保持验证结果有效。')).toBeInTheDocument()
      expect(within(setupCard).queryByRole('checkbox')).not.toBeInTheDocument()
    })

    it('routes every setup action enum to its owning page', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const actions = [
        ['change_password', '更换初始密码', '修改密码', '/users?filter=password-change-required'],
        ['manage_users', '准备备用管理员', '管理用户', '/users'],
        ['create_backup', '添加独立备份', '创建备份', '/maintenance'],
        ['run_backup', '完成首次备份', '运行备份', '/maintenance'],
        ['run_restore_drill', '验证恢复能力', '运行恢复演练', '/maintenance'],
        ['review_security', '满足安全基线', '查看安全检查', '/settings'],
      ] as const
      mockGetSetupReadiness.mockResolvedValueOnce(createSetupReadiness({
        required: { completed: 0, total: actions.length },
        recommended: { completed: 0, total: 0 },
        checks: actions.map(([action, title]) => ({
          id: action,
          requirement: 'required' as const,
          status: 'incomplete' as const,
          deferrable: false,
          title,
          message: `${title}尚未完成。`,
          action,
        })),
        summary: {
          ...createSetupReadiness().summary,
          password_change_required_admin_count: 1,
        },
      }))

      render(<DashboardPage />)

      const setupCard = await screen.findByRole('region', { name: '首次设置检查' })
      await user.click(within(setupCard).getByRole('button', { name: '查看检测结果' }))
      for (const [, , buttonLabel, path] of actions) {
        await user.click(within(setupCard).getByRole('button', { name: buttonLabel }))
        expect(mockNavigate).toHaveBeenLastCalledWith(path)
      }

      expect(mockNavigate.mock.calls).toEqual(expect.arrayContaining([
        ['/users?filter=password-change-required'],
        ['/users'],
        ['/maintenance'],
        ['/settings'],
      ]))
      expect(mockNavigate.mock.calls.filter(([path]) => path === '/users')).toHaveLength(1)
      expect(mockNavigate.mock.calls.filter(([path]) => path === '/maintenance')).toHaveLength(3)
      expect(mockNavigate.mock.calls.filter(([path]) => path === '/settings')).toHaveLength(1)
    })

    it('keeps readiness visible until the server confirms completion', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const ready = createSetupReadiness({
        overall_status: 'ready',
        can_complete: true,
        can_defer: false,
        required: { completed: 2, total: 2 },
      })
      const completed = completedSetupReadiness()
      let resolveAcknowledge!: (value: SetupReadiness) => void
      mockGetSetupReadiness
        .mockResolvedValueOnce(ready)
        .mockResolvedValue(completed)
      mockAcknowledgeSetup.mockReturnValueOnce(new Promise((resolve) => {
        resolveAcknowledge = resolve
      }))

      render(<DashboardPage />)

      const setupCard = await screen.findByRole('region', { name: '首次设置检查' })
      await user.click(within(setupCard).getByRole('button', { name: '完成设置' }))
      expect(mockAcknowledgeSetup).toHaveBeenCalledTimes(1)
      expect(screen.getByRole('region', { name: '首次设置检查' })).toBeInTheDocument()

      resolveAcknowledge(completed)
      await waitFor(() => {
        expect(screen.queryByRole('region', { name: '首次设置检查' })).not.toBeInTheDocument()
      })
      expect(mockGetSetupReadiness).toHaveBeenCalledTimes(2)
      expect(mockAddToast).toHaveBeenCalledWith({ title: '首次设置已完成', color: 'success' })
    })

    it('refetches and keeps server evidence after a completion conflict', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const ready = createSetupReadiness({
        overall_status: 'ready',
        can_complete: true,
        can_defer: false,
        required: { completed: 2, total: 2 },
      })
      mockGetSetupReadiness.mockResolvedValue(ready)
      mockAcknowledgeSetup.mockRejectedValueOnce(Object.assign(new Error('required checks changed'), {
        status: 409,
        code: 'SETUP_NOT_READY',
      }))

      render(<DashboardPage />)

      const setupCard = await screen.findByRole('region', { name: '首次设置检查' })
      await user.click(within(setupCard).getByRole('button', { name: '完成设置' }))

      await waitFor(() => {
        expect(mockGetSetupReadiness).toHaveBeenCalledTimes(2)
      })
      expect(screen.getByRole('region', { name: '首次设置检查' })).toBeInTheDocument()
      expect(screen.getByRole('alert')).toHaveTextContent('操作未完成，请稍后重试。')
    })

    it('shows a lightweight deferred state after the server accepts a bounded delay', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const pending = createSetupReadiness({ can_complete: false, can_defer: true })
      const deferred = deferredSetupReadiness()
      mockGetSetupReadiness
        .mockResolvedValueOnce(pending)
        .mockResolvedValue(deferred)
      mockDeferSetup.mockResolvedValueOnce(deferred)

      render(<DashboardPage />)

      const setupCard = await screen.findByRole('region', { name: '首次设置检查' })
      await user.click(within(setupCard).getByRole('button', { name: '稍后提醒' }))
      await user.click(screen.getByRole('radio', { name: '3 天后提醒' }))
      await user.click(screen.getByRole('button', { name: '确认延期' }))

      await waitFor(() => {
        expect(mockDeferSetup).toHaveBeenCalledWith({ remind_in_days: 3 })
        expect(screen.getByRole('region', { name: '设置提醒已延期' })).toBeInTheDocument()
      })
      expect(mockGetSetupReadiness).toHaveBeenCalledTimes(2)
      expect(mockAddToast).toHaveBeenCalledWith({ title: '首次设置提醒已延期', color: 'success' })
    })

    it('retries unavailable readiness evidence', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const unavailable = createSetupReadiness({
        overall_status: 'unavailable',
        can_complete: false,
        can_defer: false,
      })
      mockGetSetupReadiness
        .mockResolvedValueOnce(unavailable)
        .mockResolvedValueOnce(createSetupReadiness())

      render(<DashboardPage />)

      const unavailableCard = await screen.findByRole('region', { name: '设置检查暂不可用' })
      await user.click(within(unavailableCard).getByRole('button', { name: '重新检查' }))

      await waitFor(() => {
        expect(mockGetSetupReadiness).toHaveBeenCalledTimes(2)
        expect(screen.getByRole('region', { name: '首次设置检查' })).toBeInTheDocument()
      })
    })

    it('shows and retries an initial readiness query failure', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSetupReadiness
        .mockRejectedValueOnce(new Error('readiness unavailable'))
        .mockResolvedValueOnce(createSetupReadiness())

      render(<DashboardPage />)

      const failureCard = await screen.findByRole('region', { name: '首次设置检查加载失败' })
      await user.click(within(failureCard).getByRole('button', { name: '重新检查' }))

      await waitFor(() => {
        expect(mockGetSetupReadiness).toHaveBeenCalledTimes(2)
        expect(screen.getByRole('region', { name: '首次设置检查' })).toBeInTheDocument()
      })
    })

    it('places the daily summary and ordered daily entries before setup and backup attention', async () => {
      mockGetSetupReadiness.mockResolvedValueOnce(createSetupReadiness())
      mockListBackupJobs.mockResolvedValueOnce([{
        id: 'external-disk',
        name: '外置硬盘备份',
        type: 'local',
        disabled: false,
        health_status: 'failed',
        retention_status: 'ok',
        restore_drill_status: 'ok',
        running: false,
        last_run: { status: 'failed' },
      }])

      render(<DashboardPage />)

      const dailySummary = await screen.findByRole('region', { name: '日常空间摘要' })
      const dailyEntries = screen.getByRole('navigation', { name: '常用入口' })
      const setupCard = await screen.findByRole('region', { name: '首次设置检查' })
      const backupAttention = await screen.findByText('备份需要查看')
      const entries = within(dailyEntries).getAllByRole('button')

      expect(entries).toHaveLength(4)
      expect(entries[0]).toHaveTextContent('文件')
      expect(entries[1]).toHaveTextContent('版本')
      expect(entries[2]).toHaveTextContent('空间')
      expect(entries[3]).toHaveTextContent('备份与维护')
      for (const priorityContent of [dailySummary, dailyEntries]) {
        expect(priorityContent.compareDocumentPosition(setupCard) & Node.DOCUMENT_POSITION_FOLLOWING).not.toBe(0)
        expect(priorityContent.compareDocumentPosition(backupAttention) & Node.DOCUMENT_POSITION_FOLLOWING).not.toBe(0)
      }
    })
  })

  describe('retry feedback', () => {
    it('shows success toast when retry refresh succeeds', async () => {
      const user = userEvent.setup()
      mockGetHealth.mockRejectedValueOnce(new FilesApiError('health unavailable', 503, 'SERVICE_UNAVAILABLE'))

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('重新加载')).toBeTruthy()
      })

      await user.click(screen.getByText('重新加载'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '首页已刷新', color: 'success' })
        expect(mockGetSetupReadiness).toHaveBeenCalledTimes(2)
      })
    })

    it('shows warning toast when retry is temporarily unavailable', async () => {
      const user = userEvent.setup()
      mockGetHealth.mockRejectedValueOnce(new FilesApiError('health unavailable', 503, 'SERVICE_UNAVAILABLE'))
      mockGetStorageStats.mockResolvedValueOnce({
        fileCount: 42,
        fileCountAvailable: true,
        storageStatsAvailable: true,
        totalSize: 1073741824,
        totalObjects: 100,
        dedupRatio: 1.5,
      })
      mockGetStorageStats.mockRejectedValueOnce(new FilesApiError('stats unavailable', 503, 'SERVICE_UNAVAILABLE'))

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('重新加载')).toBeTruthy()
      })

      await user.click(screen.getByText('重新加载'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '刷新暂不可用',
          description: '部分首页数据当前不可用，请检查设备状态后重试。',
          color: 'warning',
        })
      })
    })

    it('shows danger toast when retry fails without an unavailable error', async () => {
      const user = userEvent.setup()
      mockGetHealth.mockRejectedValue(new Error('health failed'))
      mockGetStorageStats.mockRejectedValue(new Error('stats failed'))

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('重新加载')).toBeTruthy()
      })

      await user.click(screen.getByText('重新加载'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '刷新失败',
          description: '首页刷新失败，请稍后重试。',
          color: 'danger',
        })
      })
    })

    it('does not retry scoped queries when the user home directory is invalid', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      useIsAdminMock.mockReturnValue(false)
      useUserMock.mockReturnValue({ id: 'scoped-user', username: 'member', role: 'user', homeDir: '' })
      mockGetHealth.mockRejectedValueOnce(new FilesApiError('health unavailable', 503, 'SERVICE_UNAVAILABLE'))

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('主目录配置无效')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '首页已刷新', color: 'success' })
      })
      expect(mockGetHealth).toHaveBeenCalledTimes(2)
      expect(mockGetAppVersion).toHaveBeenCalledTimes(2)
      expect(mockGetStorageStats).not.toHaveBeenCalled()
      expect(mockListActivity).not.toHaveBeenCalled()
    })
  })

  describe('quick actions', () => {
    it('displays quick action buttons', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('常用入口')).toBeTruthy()
      })
    })

    it('has file management action', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('文件')).toBeTruthy()
      })
    })

    it('has storage management action', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('空间')).toBeTruthy()
      })
    })

    it('has backup and maintenance action', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('备份与维护')).toBeTruthy()
      })
    })

    it('has version history action', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByRole('button', { name: /版本\s+找回历史版本/ })).toBeTruthy()
      })
    })

    it('navigates to files on file management click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('文件')).toBeTruthy()
      })

      await user.click(screen.getByText('文件'))
      expect(mockNavigate).toHaveBeenCalledWith('/files')
    })

    it('navigates to storage on space click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('空间')).toBeTruthy()
      })

      await user.click(screen.getByText('空间'))
      expect(mockNavigate).toHaveBeenCalledWith('/storage')
    })

    it('navigates to maintenance on backup quick action click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('备份与维护')).toBeTruthy()
      })

      await user.click(screen.getByText('备份与维护'))
      expect(mockNavigate).toHaveBeenCalledWith('/maintenance')
    })

    it('hides admin quick actions for non-admin users', async () => {
      useIsAdminMock.mockReturnValue(false)
      useUserMock.mockReturnValue({ id: 'scoped-user', username: 'member', role: 'user', homeDir: '/member' })
      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /文件/ })).toBeTruthy()
      })

    expect(screen.queryByText('空间')).toBeNull()
    expect(screen.queryByText('备份与维护')).toBeNull()
  })

    it('passes an abort signal when loading recent activity', async () => {
      render(<DashboardPage />)

      await waitFor(() => {
        expectListActivityCalledWithSignal({ limit: 5 })
      })
    })

    it('does not reuse cached admin stats or recent activity for another user session', async () => {
    useIsAdminMock.mockReturnValue(false)
    useUserMock.mockReturnValue({ id: 'scoped-user', username: 'member', role: 'user', homeDir: '/member' })
    mockGetStorageStats.mockImplementation(() => new Promise(() => {}))
    mockListActivity.mockImplementation(() => new Promise(() => {}))

    const queryClient = new QueryClient({
      defaultOptions: {
        queries: {
          retry: false,
          gcTime: 0,
          staleTime: 0,
        },
      },
    })
    queryClient.setQueryData(['health'], {
      status: 'healthy',
      uptime: '1h30m',
    })
    queryClient.setQueryData(['app-version'], {
      name: 'MnemoNAS',
      version: '0.1.0',
      go: 'go1.24.0',
    })
    queryClient.setQueryData(['stats'], {
      fileCount: 7,
      fileCountAvailable: true,
      storageStatsAvailable: true,
      totalSize: 2048,
      totalObjects: 3,
      dedupRatio: 1.5,
    })
    queryClient.setQueryData(['recent-activity'], {
      items: [
        { id: 'act-admin', action: 'upload', path: '/admin/secret.txt', timestamp: '2024-01-15T10:00:00Z' },
      ],
    })

    render(
      <QueryClientProvider client={queryClient}>
        <DashboardPage />
      </QueryClientProvider>
    )

    await waitFor(() => {
      expect(mockGetStorageStats).toHaveBeenCalledTimes(1)
    })

    expect(screen.queryByText('/admin/secret.txt')).toBeNull()
    expect(screen.queryByText('2 KB')).toBeNull()
  })

    it('does not reuse scoped stats or recent activity when the same user home directory changes', async () => {
    useIsAdminMock.mockReturnValue(false)
    useUserMock.mockReturnValue({ id: 'scoped-user', username: 'member', role: 'user', homeDir: '/member-next' })
    mockGetStorageStats.mockImplementation(() => new Promise(() => {}))
    mockListActivity.mockImplementation(() => new Promise(() => {}))

    const queryClient = new QueryClient({
      defaultOptions: {
        queries: {
          retry: false,
          gcTime: 0,
          staleTime: 0,
        },
      },
    })
    queryClient.setQueryData(['health'], {
      status: 'healthy',
      uptime: '1h30m',
    })
    queryClient.setQueryData(['app-version'], {
      name: 'MnemoNAS',
      version: '0.1.0',
      go: 'go1.24.0',
    })
    queryClient.setQueryData(['stats', 'scoped-user', false], {
      fileCount: 7,
      fileCountAvailable: true,
      storageStatsAvailable: true,
      totalSize: 2048,
      totalObjects: 3,
      dedupRatio: 1.5,
    })
    queryClient.setQueryData(['recent-activity', 'scoped-user', false], {
      items: [
        { id: 'act-admin', action: 'upload', path: '/member-old/secret.txt', timestamp: '2024-01-15T10:00:00Z' },
      ],
    })

    render(
      <QueryClientProvider client={queryClient}>
        <DashboardPage />
      </QueryClientProvider>
    )

    await waitFor(() => {
      expect(mockGetStorageStats).toHaveBeenCalledTimes(1)
      expect(mockListActivity).toHaveBeenCalledTimes(1)
    })

    expect(screen.queryByText('/member-old/secret.txt')).toBeNull()
    expect(screen.queryByText('2 KB')).toBeNull()
  })

    it('does not load scoped dashboard data for non-admin users with invalid home directories', async () => {
      useIsAdminMock.mockReturnValue(false)
      useUserMock.mockReturnValue({ id: 'scoped-user', username: 'member', role: 'user', homeDir: '' })

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('主目录配置无效')).toBeTruthy()
      })

      expect(screen.getByText('当前账户未配置有效的主目录，无法查看空间和最近操作。请联系管理员修复账户 home_dir。')).toBeTruthy()
      expect(mockGetStorageStats).not.toHaveBeenCalled()
      expect(mockListActivity).not.toHaveBeenCalled()
      expect(mockGetHealth).toHaveBeenCalled()
      expect(mockGetAppVersion).toHaveBeenCalled()
    })

    it('navigates to versions on version history click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByRole('button', { name: /版本\s+找回历史版本/ })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /版本\s+找回历史版本/ }))
      expect(mockNavigate).toHaveBeenCalledWith('/versions')
    })
  })

  describe('storage overview section', () => {
    it('displays storage overview card', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('存储概览')).toBeTruthy()
      })
    })

    it('shows storage progress bar', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        const progressBar = screen.getByRole('progressbar', { name: '首页存储使用率' })
        expect(progressBar).toBeTruthy()
        expect(progressBar).toHaveAttribute('aria-valuenow', '5')
        expect(progressBar).toHaveAttribute('aria-valuetext', '5.0% 已用')
      })
    })

    it('displays version information', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('0.1.0')).toBeTruthy()
      })
    })
  })

  describe('recent activity', () => {
    it('renders scrub activity with the maintenance action presentation', async () => {
      mockListActivity.mockResolvedValueOnce({
        items: [
          { id: 'act-scrub', action: 'scrub', path: '/system/scrub', timestamp: '2024-01-15T10:00:00Z' },
        ],
      })

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('数据校验')).toBeTruthy()
        expect(screen.getByText('/system/scrub')).toBeTruthy()
      })
    })

    it('navigates to the activity page from the recent activity header', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /查看全部/i })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: /查看全部/i }))
      expect(mockNavigate).toHaveBeenCalledWith('/activity')
    })
  })

  describe('error handling', () => {
    it('handles missing stats gracefully', async () => {
      mockGetStorageStats.mockResolvedValueOnce({})
      
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('首页')).toBeTruthy()
      })

      expect(screen.getAllByText('统计不可用').length).toBeGreaterThan(0)
      expect(screen.getAllByText('--').length).toBeGreaterThan(0)
    })

    it('handles missing health data gracefully', async () => {
      mockGetHealth.mockResolvedValueOnce({})
      
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('首页')).toBeTruthy()
      })

      expect(screen.getByText('状态未知')).toBeTruthy()
    })

    it('shows a partial-data warning when overview queries fail', async () => {
      mockGetStorageStats.mockRejectedValueOnce(new Error('Network error'))

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('部分系统数据加载失败')).toBeTruthy()
        expect(screen.getByText('当前页面展示的是可用数据，部分卡片或活动记录可能不是最新状态。')).toBeTruthy()
      })
    })

    it('shows a dedicated unavailable state when recent activity is temporarily unavailable', async () => {
      mockListActivity.mockRejectedValueOnce(new ApiError('activity log unavailable', 503, 'SERVICE_UNAVAILABLE'))

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('活动记录暂时不可用')).toBeTruthy()
        expect(screen.getByText('操作记录当前不可用，请稍后重试，或前往最近操作页查看最新状态。')).toBeTruthy()
      })
    })

    it('shows a generic recent-activity load failure state for non-503 errors', async () => {
      mockListActivity.mockRejectedValueOnce(new Error('Network error'))

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('活动记录加载失败')).toBeTruthy()
        expect(screen.getByText('请刷新页面后重试，或前往活动页查看详细状态。')).toBeTruthy()
      })
    })

    it('offers a retry action when overview queries fail', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetStorageStats.mockRejectedValueOnce(new Error('Network error'))

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockGetHealth).toHaveBeenCalledTimes(2)
        expect(mockGetAppVersion).toHaveBeenCalledTimes(2)
        expect(mockGetStorageStats).toHaveBeenCalledTimes(2)
      })
    })
  })
})
