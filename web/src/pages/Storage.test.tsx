import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { StoragePage } from './Storage'
import * as HeroUI from '@heroui/react'

const useIsAdminMock = vi.fn(() => true)
const mockAddToast = vi.fn()

const { mockUser } = vi.hoisted(() => ({
  mockUser: { id: 'u1', username: 'admin', role: 'admin' as const, email: 'admin@local', homeDir: '/' },
}))

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
    diskTotal: 21474836480, // 20 GB
    diskAvailable: 16106127360, // 15 GB
    diskUsed: 5368709120, // 5 GB
    diskUsageRatio: 0.25,
    diskFilesystemType: 'zfs',
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
        expect(screen.getByText('存储管理')).toBeTruthy()
      })
    })

    it('displays subtitle', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('原生文件 + CAS 版本历史')).toBeTruthy()
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

  describe('maintenance cards', () => {
    it('displays scrub card', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('数据巡检 (Scrub)')).toBeTruthy()
        expect(screen.getByText('验证所有数据完整性')).toBeTruthy()
      })
    })

    it('displays GC card', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('垃圾回收 (GC)')).toBeTruthy()
        expect(screen.getByText('清理无引用的数据块')).toBeTruthy()
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
        expect(screen.getAllByText('在系统维护中执行').length).toBeGreaterThan(0)
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
  })

  describe('error handling', () => {
    it('shows an unavailable state when storage stats return service unavailable', async () => {
      mockGetStorageStats.mockRejectedValue(new ApiError('storage stats unavailable', 503, 'SERVICE_UNAVAILABLE'))
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('存储统计暂不可用')).toBeTruthy()
        expect(screen.getByText('存储统计服务当前不可用，请检查系统健康状态或稍后重试。')).toBeTruthy()
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
          description: '存储统计服务当前不可用，请检查系统健康状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('handles empty stats', async () => {
      mockGetStorageStats.mockResolvedValue({})
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('存储管理')).toBeTruthy()
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
