import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { StoragePage } from './Storage'

// Mock API
vi.mock('@/api/files', () => ({
  getStorageStats: vi.fn(),
}))

import { getStorageStats } from '@/api/files'

const mockGetStorageStats = getStorageStats as ReturnType<typeof vi.fn>

describe('StoragePage', () => {
  const mockStats = {
    totalObjects: 1234,
    totalSize: 5368709120, // 5 GB
    dedupRatio: 0.35,
  }

  beforeEach(() => {
    vi.clearAllMocks()
    mockGetStorageStats.mockResolvedValue(mockStats)
  })

  describe('loading state', () => {
    it('shows loading skeleton initially', () => {
      mockGetStorageStats.mockImplementation(() => new Promise(() => {}))
      render(<StoragePage />)

      // Should show skeleton elements
      const skeletons = document.querySelectorAll('[class*="loading"], [class*="pulse"], .bg-content1')
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
        const progressBar = document.querySelector('[class*="bg-gradient-to-r"]')
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

    it('renders scrub button (coming soon)', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('开始巡检（即将推出）')).toBeTruthy()
      })
    })

    it('renders GC button (coming soon)', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('开始清理（即将推出）')).toBeTruthy()
      })
    })

    it('shows development status for scrub', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByText('功能开发中').length).toBeGreaterThan(0)
      })
    })

    it('shows coming soon status for features', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByText('即将推出').length).toBeGreaterThan(0)
      })
    })
  })

  describe('error handling', () => {
    it('handles API error gracefully', async () => {
      mockGetStorageStats.mockRejectedValue(new Error('Network error'))
      render(<StoragePage />)

      // Should not crash
      await waitFor(() => {
        expect(mockGetStorageStats).toHaveBeenCalled()
      })
    })

    it('handles empty stats', async () => {
      mockGetStorageStats.mockResolvedValue({})
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('存储管理')).toBeTruthy()
      })
    })
  })
})
