import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
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

const mockGetHealth = getHealth as ReturnType<typeof vi.fn>
const mockGetAppVersion = getAppVersion as ReturnType<typeof vi.fn>
const mockGetStorageStats = getStorageStats as ReturnType<typeof vi.fn>
const mockListBackupJobs = listBackupJobs as ReturnType<typeof vi.fn>
const mockListActivity = listActivity as ReturnType<typeof vi.fn>

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
  })

  describe('loading state', () => {
    it('renders loading state initially', () => {
      mockGetHealth.mockImplementation(() => new Promise(() => {}))
      mockGetStorageStats.mockImplementation(() => new Promise(() => {}))
      
      render(<DashboardPage />)
      // Should show skeleton loaders (HeroUI Skeleton component)
      expect(document.querySelector('.rounded-lg, .rounded-lg')).toBeTruthy()
    })

    it('shows skeleton placeholders while loading', () => {
      mockGetHealth.mockImplementation(() => new Promise(() => {}))
      mockGetStorageStats.mockImplementation(() => new Promise(() => {}))
      
      render(<DashboardPage />)
      const skeletons = document.querySelectorAll('.rounded-lg, .rounded-lg, [class*="animate"]')
      expect(skeletons.length).toBeGreaterThan(0)
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
        expect(screen.getByRole('button', { name: '打开备份' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '打开备份' }))
      expect(mockNavigate).toHaveBeenCalledWith('/maintenance')
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
        const progressBar = document.querySelector('[class*="gradient"]')
        expect(progressBar).toBeTruthy()
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
      expect(document.querySelector('.text-amber-500')).toBeTruthy()
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
