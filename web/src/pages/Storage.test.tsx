import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { StoragePage } from './Storage'
import * as HeroUI from '@heroui/react'

const useIsAdminMock = vi.fn(() => true)
const mockAddToast = vi.fn()

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
  }

  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
    useIsAdminMock.mockReturnValue(true)
    mockGetStorageStats.mockResolvedValue(mockStats)
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
        expect(screen.getByText('CAS 内容寻址存储系统')).toBeTruthy()
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
  })

  describe('stats cards', () => {
    it('displays total objects count', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('对象总数')).toBeTruthy()
        expect(screen.getByText('1,234')).toBeTruthy()
      })
    })

    it('displays storage size', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('存储大小')).toBeTruthy()
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
  })
})
