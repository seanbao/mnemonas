import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { DashboardPage } from './Dashboard'

// Mock the API functions
vi.mock('@/api/files', () => ({
  getHealth: vi.fn().mockResolvedValue({
    status: 'healthy',
    version: '0.1.0',
    uptime: '1h30m',
    storage: {
      dataDir: '/var/lib/mnemonas/data',
      writable: true,
    },
  }),
  getStorageStats: vi.fn().mockResolvedValue({
    totalSize: 1073741824, // 1 GB
    totalObjects: 100,
    dedupRatio: 1.5,
  }),
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

import { getHealth, getStorageStats } from '@/api/files'

const mockGetHealth = getHealth as ReturnType<typeof vi.fn>
const mockGetStorageStats = getStorageStats as ReturnType<typeof vi.fn>

describe('DashboardPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockNavigate.mockClear()
    // Reset mocks to default values (vi.clearAllMocks clears mockResolvedValue)
    mockGetHealth.mockResolvedValue({
      status: 'healthy',
      version: '0.1.0',
      uptime: '1h30m',
      storage: {
        dataDir: '/var/lib/mnemonas/data',
        writable: true,
      },
    })
    mockGetStorageStats.mockResolvedValue({
      totalSize: 1073741824, // 1 GB
      totalObjects: 100,
      dedupRatio: 1.5,
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
      // Multiple skeleton elements should be present
      const skeletons = document.querySelectorAll('[class*="skeleton"], [class*="animate"]')
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
      expect(mockNavigate).toHaveBeenCalledWith('/health')
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
      
      // Should show default values
      expect(screen.getAllByText('0').length).toBeGreaterThan(0)
    })

    it('handles missing health data gracefully', async () => {
      mockGetHealth.mockResolvedValueOnce({})
      
      render(<DashboardPage />)
      
      await waitFor(() => {
        expect(screen.getByText('系统概览')).toBeTruthy()
      })
    })
  })
})
