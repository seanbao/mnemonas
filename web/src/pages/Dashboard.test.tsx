import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { DashboardPage } from './Dashboard'

const useIsAdminMock = vi.fn(() => true)

// Mock the API functions
vi.mock('@/api/files', () => ({
  getHealth: vi.fn().mockResolvedValue({
    status: 'healthy',
    version: '0.1.0',
    uptime: '1h30m',
    storage: {
      dataDir: '/root/.mnemonas/.mnemonas/objects',
      writable: true,
    },
  }),
  getStorageStats: vi.fn().mockResolvedValue({
    totalSize: 1073741824, // 1 GB
    totalObjects: 100,
    dedupRatio: 1.5,
  }),
}))

vi.mock('@/api/activity', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/activity')>()
  return {
    ...actual,
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
  }
})

import { getHealth, getStorageStats } from '@/api/files'
import { listActivity } from '@/api/activity'

const mockGetHealth = getHealth as ReturnType<typeof vi.fn>
const mockGetStorageStats = getStorageStats as ReturnType<typeof vi.fn>
const mockListActivity = listActivity as ReturnType<typeof vi.fn>

describe('DashboardPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useIsAdminMock.mockReturnValue(true)
    mockNavigate.mockClear()
    // Reset mocks to default values (vi.clearAllMocks clears mockResolvedValue)
    mockGetHealth.mockResolvedValue({
      status: 'healthy',
      version: '0.1.0',
      uptime: '1h30m',
      storage: {
        dataDir: '/root/.mnemonas/.mnemonas/objects',
        writable: true,
      },
    })
    mockGetStorageStats.mockResolvedValue({
      totalSize: 1073741824, // 1 GB
      totalObjects: 100,
      dedupRatio: 1.5,
    })
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
      expect(document.querySelector('.rounded-lg, .rounded-xl')).toBeTruthy()
    })

    it('shows skeleton placeholders while loading', () => {
      mockGetHealth.mockImplementation(() => new Promise(() => {}))
      mockGetStorageStats.mockImplementation(() => new Promise(() => {}))
      
      render(<DashboardPage />)
      const skeletons = document.querySelectorAll('.rounded-lg, .rounded-xl, [class*="animate"]')
      expect(skeletons.length).toBeGreaterThan(0)
    })
  })

  describe('content rendering', () => {
    it('renders dashboard header after loading', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('系统概览')).toBeTruthy()
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
        version: '0.1.0',
        uptime: '1h30m',
      })
      
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('异常')).toBeTruthy()
      })
    })

    it('shows unhealthy status when system is degraded', async () => {
      mockGetHealth.mockResolvedValueOnce({
        status: 'degraded',
        version: '0.1.0',
        uptime: '1h30m',
      })

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('异常')).toBeTruthy()
      })
    })

    it('displays storage statistics', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('存储使用')).toBeTruthy()
        expect(screen.getByText('文件对象')).toBeTruthy()
        // Multiple elements may have '去重率' text
        expect(screen.getAllByText('去重率').length).toBeGreaterThan(0)
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

    it('displays object count', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        // Multiple elements may display '100'
        expect(screen.getAllByText('100').length).toBeGreaterThan(0)
      })
    })

    it('displays uptime', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('1h30m')).toBeTruthy()
      })
    })

    it('displays dedup ratio percentage', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        // 1.5 * 100 = 150%
        expect(screen.getAllByText(/150.*%/i).length).toBeGreaterThan(0)
      })
    })

    it('shows unknown capacity guidance instead of a full usage bar', async () => {
      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('总容量未配置，无法计算占用比例')).toBeTruthy()
      })
    })
  })

  describe('quick actions', () => {
    it('displays quick action buttons', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('快速操作')).toBeTruthy()
      })
    })

    it('has file management action', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('文件管理')).toBeTruthy()
      })
    })

    it('has storage management action', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('存储管理')).toBeTruthy()
      })
    })

    it('has system health action', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('系统健康')).toBeTruthy()
      })
    })

    it('has version history action', async () => {
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('版本历史')).toBeTruthy()
      })
    })

    it('navigates to files on file management click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('文件管理')).toBeTruthy()
      })

      await user.click(screen.getByText('文件管理'))
      expect(mockNavigate).toHaveBeenCalledWith('/files')
    })

    it('navigates to storage on storage management click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('存储管理')).toBeTruthy()
      })

      await user.click(screen.getByText('存储管理'))
      expect(mockNavigate).toHaveBeenCalledWith('/storage')
    })

    it('navigates to health on system health click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('系统健康')).toBeTruthy()
      })

      await user.click(screen.getByText('系统健康'))
      expect(mockNavigate).toHaveBeenCalledWith('/system-health')
    })

    it('hides system health quick action for non-admin users', async () => {
      useIsAdminMock.mockReturnValue(false)
      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('文件管理')).toBeTruthy()
      })

      expect(screen.queryByText('系统健康')).toBeNull()
    })

    it('navigates to versions on version history click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('版本历史')).toBeTruthy()
      })

      await user.click(screen.getByText('版本历史'))
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

  describe('error handling', () => {
    it('handles missing stats gracefully', async () => {
      mockGetStorageStats.mockResolvedValueOnce({})
      
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('系统概览')).toBeTruthy()
      })

      expect(screen.getAllByText('统计不可用').length).toBeGreaterThan(0)
      expect(screen.getAllByText('--').length).toBeGreaterThan(0)
    })

    it('handles missing health data gracefully', async () => {
      mockGetHealth.mockResolvedValueOnce({})
      
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('系统概览')).toBeTruthy()
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

    it('shows a recent-activity error state when activity loading fails', async () => {
      mockListActivity.mockRejectedValueOnce(new Error('Activity unavailable'))

      render(<DashboardPage />)

      await waitFor(() => {
        expect(screen.getByText('活动记录暂时不可用')).toBeTruthy()
        expect(screen.getByText('请稍后重试，或前往活动页查看最新状态。')).toBeTruthy()
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
        expect(mockGetStorageStats).toHaveBeenCalledTimes(2)
      })
    })
  })
})
