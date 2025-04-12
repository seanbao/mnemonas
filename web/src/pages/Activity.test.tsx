import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { ActivityPage } from './Activity'

// Mock activity API
vi.mock('@/api/activity', () => ({
  listActivity: vi.fn(),
  getActivityStats: vi.fn(),
  getActionLabel: vi.fn((action: string) => {
    const labels: Record<string, string> = {
      upload: '上传文件',
      download: '下载文件',
      delete: '删除文件',
      login: '登录',
    }
    return labels[action] || action
  }),
  getActionColor: vi.fn(() => 'primary'),
}))

import { listActivity } from '@/api/activity'

const mockListActivity = listActivity as ReturnType<typeof vi.fn>

describe('ActivityPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListActivity.mockResolvedValue({
      items: [
        {
          id: '1',
          timestamp: new Date(Date.now() - 1000 * 60 * 5).toISOString(), // 5 minutes ago
          action: 'upload',
          path: '/documents/report.pdf',
          user: 'admin',
          ip: '192.168.1.1',
        },
        {
          id: '2',
          timestamp: new Date(Date.now() - 1000 * 60 * 60).toISOString(), // 1 hour ago
          action: 'delete',
          path: '/old-file.txt',
          user: 'user1',
        },
        {
          id: '3',
          timestamp: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(), // 1 day ago
          action: 'login',
          user: 'admin',
          ip: '10.0.0.1',
        },
      ],
      total: 3,
      limit: 20,
      offset: 0,
    })
  })

  describe('rendering', () => {
    it('shows loading state initially', () => {
      mockListActivity.mockImplementation(() => new Promise(() => {}))
      render(<ActivityPage />)
      
      // Should show skeleton loaders
      const skeletons = document.querySelectorAll('[class*="skeleton"], [class*="animate"]')
      expect(skeletons.length).toBeGreaterThan(0)
    })

    it('renders page header', async () => {
      render(<ActivityPage />)
      
      await waitFor(() => {
        expect(screen.getByText('活动日志')).toBeTruthy()
      })
    })

    it('shows activity count', async () => {
      render(<ActivityPage />)

      await waitFor(() => {
        expect(mockListActivity).toHaveBeenCalledTimes(1)
        expect(screen.getByText(/共 3 条记录/)).toBeTruthy()
      })
    })

    it('derives activity count from the returned items when the summary field is missing', async () => {
      mockListActivity.mockResolvedValue({
        items: [
          {
            id: '1',
            timestamp: new Date(Date.now() - 1000 * 60 * 5).toISOString(),
            action: 'upload',
            path: '/documents/report.pdf',
            user: 'admin',
          },
        ],
        total: undefined as unknown as number,
        limit: 20,
        offset: 0,
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText(/共 1 条记录/)).toBeTruthy()
      })
    })

    it('displays activity entries', async () => {
      render(<ActivityPage />)
      
      await waitFor(() => {
        // Use getAllByText since labels appear both in dropdown and activity list
        const uploadElements = screen.getAllByText('上传文件')
        expect(uploadElements.length).toBeGreaterThanOrEqual(1)
        const deleteElements = screen.getAllByText('删除文件')
        expect(deleteElements.length).toBeGreaterThanOrEqual(1)
        const loginElements = screen.getAllByText('登录')
        expect(loginElements.length).toBeGreaterThanOrEqual(1)
      })
    })

    it('shows file paths', async () => {
      render(<ActivityPage />)
      
      await waitFor(() => {
        expect(screen.getByText('/documents/report.pdf')).toBeTruthy()
        expect(screen.getByText('/old-file.txt')).toBeTruthy()
      })
    })

    it('displays usernames', async () => {
      render(<ActivityPage />)
      
      await waitFor(() => {
        const adminElements = screen.getAllByText('admin')
        expect(adminElements.length).toBeGreaterThan(0)
      })
    })

    it('shows relative time', async () => {
      render(<ActivityPage />)
      
      await waitFor(() => {
        // Should show "5 分钟前" or similar
        expect(screen.getByText(/分钟前/)).toBeTruthy()
      })
    })
  })

  describe('empty state', () => {
    it('shows empty message when no activity', async () => {
      mockListActivity.mockResolvedValue({
        items: [],
        total: 0,
        limit: 20,
        offset: 0,
      })
      
      render(<ActivityPage />)
      
      await waitFor(() => {
        expect(screen.getByText('暂无活动记录')).toBeTruthy()
      })
    })

    it('shows retryable error state when activity loading fails', async () => {
      mockListActivity.mockRejectedValue(new Error('Network error'))

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('加载活动日志失败')).toBeTruthy()
        expect(screen.getByText('Network error')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })
  })

  describe('filtering', () => {
    it('renders filter dropdown', async () => {
      render(<ActivityPage />)
      
      await waitFor(() => {
        expect(screen.getByText('筛选操作')).toBeTruthy()
      })
    })

    it('renders refresh button', async () => {
      render(<ActivityPage />)
      
      await waitFor(() => {
        expect(screen.getByText('刷新')).toBeTruthy()
      })
    })
  })

  describe('refresh', () => {
    it('refetches data on refresh click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      
      render(<ActivityPage />)
      
      await waitFor(() => {
        expect(screen.getByText('刷新')).toBeTruthy()
      })

      mockListActivity.mockClear()

      await user.click(screen.getByText('刷新'))

      await waitFor(() => {
        expect(mockListActivity).toHaveBeenCalledTimes(1)
      })
    })
  })

  describe('API calls', () => {
    it('calls listActivity with default parameters', async () => {
      render(<ActivityPage />)
      
      await waitFor(() => {
        expect(mockListActivity).toHaveBeenCalledWith({
          limit: 20,
          offset: 0,
          action: undefined,
        })
      })
    })
  })
})
