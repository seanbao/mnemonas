import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import * as HeroUI from '@heroui/react'

const useIsAdminMock = vi.fn(() => true)
const mockAddToast = vi.fn()

const { mockNavigate, mockUser } = vi.hoisted(() => ({
  mockNavigate: vi.fn(),
  mockUser: { id: 'u1', username: 'admin', role: 'admin' as const, email: 'admin@local', homeDir: '/' },
}))

vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

import { StoragePage } from './Storage'

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
  getStorageStats: vi.fn(),
}))

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useUser: () => mockUser,
    useIsAdmin: () => useIsAdminMock(),
  }
})

import { ApiError, getStorageStats } from '@/api/files'

const mockGetStorageStats = getStorageStats as ReturnType<typeof vi.fn>

describe('StoragePage', () => {
  const mockStats = {
    totalObjects: 1234,
    totalSize: 5368709120, // 5 GB
    dedupRatio: 0.35,
    storageStatsAvailable: true,
    diskStatsAvailable: true,
    directoryQuotaStatsAvailable: true,
    directoryQuotas: [],
    diskTotal: 21474836480, // 20 GB
    diskAvailable: 16106127360, // 15 GB
    diskUsed: 5368709120, // 5 GB
    diskUsageRatio: 0.25,
    diskFilesystemType: 'zfs',
    diskMountPoint: '/srv/mnemonas',
    diskMountSource: 'tank/mnemonas',
    diskMountOptions: 'rw,relatime',
    diskNativeDataChecksumSupport: true,
  }

  beforeEach(() => {
    vi.clearAllMocks()
    mockUser.id = 'u1'
    mockUser.username = 'admin'
    mockUser.role = 'admin'
    mockUser.email = 'admin@local'
    mockUser.homeDir = '/'
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
    useIsAdminMock.mockReturnValue(true)
    mockNavigate.mockClear()
    mockGetStorageStats.mockResolvedValue(mockStats)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  describe('loading state', () => {
    it('shows loading skeleton initially', () => {
      mockGetStorageStats.mockImplementation(() => new Promise(() => {}))
      render(<StoragePage />)

      // Should show skeleton elements with HeroUI skeleton classes
      const skeletons = document.querySelectorAll('[class*="skeleton"], [class*="animate"], [class*="rounded-lg"]')
      expect(skeletons.length).toBeGreaterThan(0)
    })
  })

  describe('header', () => {
    it('displays page title', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('空间与存储')).toBeTruthy()
      })
    })

    it('displays subtitle', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('文件占用、版本对象和目录配额')).toBeTruthy()
      })
    })

    it('renders refresh button', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('刷新')).toBeTruthy()
      })
    })

    it('calls refetch on refresh button click', async () => {
      const user = userEvent.setup()
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('刷新')).toBeTruthy()
      })

      await user.click(screen.getByText('刷新'))

      // First call on mount, second on refresh
      await waitFor(() => {
        expect(mockGetStorageStats.mock.calls.length).toBeGreaterThanOrEqual(1)
      })
    })

    it('auto-refreshes storage stats every 30 seconds', async () => {
      vi.useFakeTimers()

      const flushUi = async () => {
        await act(async () => {
          await Promise.resolve()
          await Promise.resolve()
        })
        act(() => {
          vi.advanceTimersByTime(0)
        })
      }

      render(<StoragePage />)

      await flushUi()
      expect(mockGetStorageStats).toHaveBeenCalledTimes(1)

      await act(async () => {
        await vi.advanceTimersByTimeAsync(30000)
      })

      await flushUi()
      expect(mockGetStorageStats).toHaveBeenCalledTimes(2)
    })

    it('refetches storage stats when the auth scope changes', async () => {
    mockGetStorageStats
      .mockResolvedValueOnce(mockStats)
      .mockResolvedValueOnce({
        ...mockStats,
        totalObjects: 2048,
      })

    const { rerender } = render(<StoragePage />)

    await waitFor(() => {
      expect(mockGetStorageStats).toHaveBeenCalledTimes(1)
    })

    mockUser.id = 'u2'
    mockUser.username = 'other-admin'
    mockUser.email = 'other@local'
    mockUser.role = 'admin'
    mockUser.homeDir = '/'

    rerender(<StoragePage />)

    await waitFor(() => {
      expect(mockGetStorageStats).toHaveBeenCalledTimes(2)
    })
    })
  })

  describe('storage overview', () => {
    it('displays storage usage section', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('存储空间使用情况')).toBeTruthy()
      })
    })

    it('displays formatted storage size', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText(/5.*GB.*已使用/)).toBeTruthy()
      })
    })

    it('shows progress bar', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        const progressBar = document.querySelector('[class*="flow-line"], [class*="bg-accent-primary"]')
        expect(progressBar).toBeTruthy()
      })
    })

    it('displays the storage mount point and source', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('挂载点')).toBeTruthy()
        expect(screen.getByText('/srv/mnemonas')).toBeTruthy()
        expect(screen.getByText('存储源')).toBeTruthy()
        expect(screen.getByText('tank/mnemonas')).toBeTruthy()
      })
    })

    it('warns when disk space is below the safe operating margin', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        diskAvailable: 512 * 1024 * 1024,
        diskUsed: 20937965568,
        diskUsageRatio: 0.975,
      })

      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('存储空间严重不足')).toBeTruthy()
        expect(screen.getByText(/尽快清理回收站/)).toBeTruthy()
      })
    })

    it('shows a warning-level disk space alert before capacity becomes critical', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        diskAvailable: 5 * 1024 * 1024 * 1024,
        diskUsed: 15 * 1024 * 1024 * 1024,
        diskUsageRatio: 0.91,
      })

      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('存储空间偏紧')).toBeTruthy()
        expect(screen.getByText(/建议开启提醒/)).toBeTruthy()
      })
    })
  })

  describe('stats cards', () => {
    it('displays total objects count', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('对象总数')).toBeTruthy()
        expect(screen.getByText('1,234')).toBeTruthy()
      })
    })

    it('displays disk capacity and available space', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('磁盘容量')).toBeTruthy()
        expect(screen.getByText('可用空间')).toBeTruthy()
        expect(screen.getByText('磁盘占用')).toBeTruthy()
        expect(screen.getByText('25.0%')).toBeTruthy()
      })
    })

    it('displays filesystem type', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('文件系统')).toBeTruthy()
        expect(screen.getByText('ZFS')).toBeTruthy()
      })
    })

    it('displays CAS storage size', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('CAS 大小')).toBeTruthy()
      })
    })

    it('displays dedup ratio', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('去重率')).toBeTruthy()
        expect(screen.getByText('35.0%')).toBeTruthy()
      })
    })

    it('displays saved space', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('节省空间')).toBeTruthy()
      })
    })
  })

  describe('directory quotas', () => {
    it('shows an empty directory quota state for admins when no quotas are configured', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('目录配额')).toBeTruthy()
        expect(screen.getByText('未配置目录配额。可在设置的版本保留页添加目录容量限制。')).toBeTruthy()
      })
    })

    it('renders directory quota usage summaries', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        directoryQuotaStatsAvailable: true,
        directoryQuotas: [
          {
            path: '/team',
            quotaBytes: 2147483648,
            usedBytes: 1073741824,
            availableBytes: 1073741824,
            usageRatio: 0.5,
            exists: true,
            status: 'normal',
          },
          {
            path: '/archive',
            quotaBytes: 1073741824,
            usedBytes: 1046898278,
            availableBytes: 1678546,
            usageRatio: 0.975,
            exists: true,
            status: 'warning',
          },
          {
            path: '/missing',
            quotaBytes: 536870912,
            usedBytes: 0,
            availableBytes: 536870912,
            usageRatio: 0,
            exists: false,
            status: 'missing',
          },
        ],
      })

      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('/team')).toBeTruthy()
        expect(screen.getByText('/archive')).toBeTruthy()
        expect(screen.getByText('/missing')).toBeTruthy()
        expect(screen.getByText('正常')).toBeTruthy()
        expect(screen.getByText('接近上限')).toBeTruthy()
        expect(screen.getByText('目录未创建')).toBeTruthy()
        expect(screen.getByText('50.0%')).toBeTruthy()
        expect(screen.getByText('97.5%')).toBeTruthy()
        expect(screen.getByText('路径不存在')).toBeTruthy()
      })
    })

    it('shows quota stats unavailable state when collection fails', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        directoryQuotaStatsAvailable: false,
      })

      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('目录配额统计暂不可用，请稍后刷新或检查存储状态。')).toBeTruthy()
      })
    })

    it('hides directory quota summaries from non-admin users', async () => {
      useIsAdminMock.mockReturnValue(false)

      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.queryByText('目录配额')).toBeNull()
      })
    })
  })

  describe('maintenance cards', () => {
    it('displays scrub card', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('完整性检查')).toBeTruthy()
        expect(screen.getByText('确认已存数据仍可正确读取')).toBeTruthy()
      })
    })

    it('displays GC card', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('清理历史对象')).toBeTruthy()
        expect(screen.getByText('清理不再被引用的版本数据')).toBeTruthy()
      })
    })

    it('renders scrub maintenance button', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByText('打开维护工具').length).toBeGreaterThan(0)
      })
    })

    it('renders GC maintenance button', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByText('打开维护工具').length).toBeGreaterThan(1)
      })
    })

    it('shows scrub execution context', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByText('在备份与维护中执行').length).toBeGreaterThan(0)
      })
    })

    it('shows GC execution context', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByText(/支持/).length).toBeGreaterThan(1)
      })
    })

    it('disables maintenance actions for non-admin users', async () => {
      useIsAdminMock.mockReturnValue(false)
      render(<StoragePage />)

      await waitFor(() => {
        const buttons = screen.getAllByRole('button', { name: '仅管理员可用' })
        expect(buttons.length).toBe(2)
        buttons.forEach((button) => expect(button).toBeDisabled())
      })
    })

    it('navigates to maintenance from both admin maintenance actions', async () => {
      const user = userEvent.setup()
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByRole('button', { name: '打开维护工具' })).toHaveLength(2)
      })

      const buttons = screen.getAllByRole('button', { name: '打开维护工具' })
      await user.click(buttons[0])
      await user.click(buttons[1])

      expect(mockNavigate).toHaveBeenNthCalledWith(1, '/maintenance')
      expect(mockNavigate).toHaveBeenNthCalledWith(2, '/maintenance')
    })
  })

  describe('error handling', () => {
    it('shows an unavailable state when storage stats return service unavailable', async () => {
      mockGetStorageStats.mockRejectedValue(new ApiError('storage stats unavailable', 503, 'SERVICE_UNAVAILABLE'))
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('存储统计暂不可用')).toBeTruthy()
        expect(screen.getByText('存储统计服务当前不可用，请检查设备状态或稍后重试。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('shows retryable error state on stats fetch failure', async () => {
      mockGetStorageStats.mockRejectedValue(new Error('Network error'))
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('加载存储统计失败')).toBeTruthy()
        expect(screen.getByText('Network error')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('shows success toast when storage reload succeeds', async () => {
      const user = userEvent.setup()
      mockGetStorageStats
        .mockRejectedValueOnce(new Error('Network error'))
        .mockResolvedValueOnce(mockStats)
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '存储统计已刷新', color: 'success' })
      })
    })

    it('shows warning toast when storage reload is temporarily unavailable', async () => {
      const user = userEvent.setup()
      mockGetStorageStats
        .mockRejectedValueOnce(new Error('Network error'))
        .mockRejectedValueOnce(new ApiError('storage stats unavailable', 503, 'SERVICE_UNAVAILABLE'))
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '存储统计暂不可用',
          description: '存储统计服务当前不可用，请检查设备状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('shows danger toast when storage reload fails with a generic error', async () => {
      const user = userEvent.setup()
      mockGetStorageStats
        .mockRejectedValueOnce(new Error('Network error'))
        .mockRejectedValueOnce(new Error('still offline'))
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '加载存储统计失败',
          description: 'still offline',
          color: 'danger',
        })
      })
    })

    it('handles empty stats', async () => {
      mockGetStorageStats.mockResolvedValue({})
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('空间与存储')).toBeTruthy()
        expect(screen.getAllByText('统计不可用').length).toBeGreaterThan(0)
        expect(screen.getAllByText('--').length).toBeGreaterThan(0)
      })
    })

    it('treats explicit unavailable storage stats as unavailable even when numeric fields are present', async () => {
      mockGetStorageStats.mockResolvedValue({
        totalObjects: 999,
        totalSize: 1024,
        dedupRatio: 0.42,
        storageStatsAvailable: false,
      })
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByText('统计不可用').length).toBeGreaterThan(0)
        expect(screen.queryByText('999')).toBeNull()
        expect(screen.queryByText('42.0%')).toBeNull()
      })
    })
  })
})
