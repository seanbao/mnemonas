import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { ReactNode } from 'react'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ActivityPage } from './Activity'
import * as HeroUI from '@heroui/react'

const useIsAdminMock = vi.fn(() => true)
const useUserMock = vi.fn(() => ({ id: 'admin-id', username: 'admin', role: 'admin' }))

vi.mock('@heroui/react', async () => {
  const actual = await vi.importActual<typeof import('@heroui/react')>('@heroui/react')
  return {
    ...actual,
    Chip: ({ children, onClose }: { children: ReactNode; onClose?: () => void }) => (
      <div>
        <span>{children}</span>
        {onClose ? <button onClick={onClose}>清除筛选</button> : null}
      </div>
    ),
    Pagination: ({ total, page, onChange }: { total: number; page: number; onChange: (page: number) => void }) => (
      <div>
        <span>{`page ${page} of ${total}`}</span>
        {total > 1 ? <button onClick={() => onChange(2)}>转到第 2 页</button> : null}
      </div>
    ),
    Select: ({
      children,
      onSelectionChange,
      placeholder,
    }: {
      children: ReactNode
      onSelectionChange?: (keys: Set<string>) => void
      placeholder?: string
    }) => (
      <div>
        <span>{placeholder}</span>
        <button onClick={() => onSelectionChange?.(new Set(['delete']))}>筛选删除文件</button>
        <button onClick={() => onSelectionChange?.(new Set(['scrub']))}>筛选数据校验</button>
        <button onClick={() => onSelectionChange?.(new Set())}>清空筛选</button>
        <div>{children}</div>
      </div>
    ),
    SelectItem: ({ children }: { children: ReactNode }) => <span>{children}</span>,
  }
})

const mockAddToast = vi.fn()

// Mock activity API
vi.mock('@/api/activity', () => ({
  ACTIVITY_ACTIONS: [
    'upload',
    'download',
    'delete',
    'rename',
    'move',
    'copy',
    'create',
    'restore',
    'share',
    'unshare',
    'favorite',
    'unfavorite',
    'favorite_note_update',
    'login',
    'logout',
    'trash_restore',
    'trash_delete',
    'trash_empty',
    'disk_health',
    'scrub',
  ],
  listActivity: vi.fn(),
  getActivityStats: vi.fn(),
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
  getActionLabel: vi.fn((action: string) => {
    const labels: Record<string, string> = {
      upload: '上传文件',
      download: '下载文件',
      delete: '删除文件',
      login: '登录',
      scrub: '数据校验',
    }
    return labels[action] || action
  }),
  getActionColor: vi.fn((action: string) => {
    const colors: Record<string, string> = {
      upload: 'success',
      delete: 'danger',
      download: 'warning',
      login: 'default',
      scrub: 'warning',
    }
    return colors[action] || 'primary'
  }),
}))

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useIsAdmin: () => useIsAdminMock(),
    useUser: () => useUserMock(),
  }
})

import { ApiError, listActivity } from '@/api/activity'

const mockListActivity = listActivity as ReturnType<typeof vi.fn>

describe('ActivityPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
    useIsAdminMock.mockReturnValue(true)
    useUserMock.mockReturnValue({ id: 'admin-id', username: 'admin', role: 'admin' })
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

    it('shows an invalid-home error instead of loading activity for non-admin users without a home directory', async () => {
      useIsAdminMock.mockReturnValue(false)
      useUserMock.mockReturnValue({ id: 'member-id', username: 'member', role: 'user', homeDir: '' })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getAllByText('主目录配置无效').length).toBeGreaterThan(0)
        expect(screen.getByText('当前账户未配置有效的主目录，无法查看最近操作。请联系管理员修复账户 home_dir。')).toBeTruthy()
      })

      expect(mockListActivity).not.toHaveBeenCalled()
    })

    it('renders page header', async () => {
      render(<ActivityPage />)
      
      await waitFor(() => {
        expect(screen.getByText('最近操作')).toBeTruthy()
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

    it('renders activity details when an entry includes metadata', async () => {
      mockListActivity.mockResolvedValue({
        items: [
          {
            id: '1',
            timestamp: new Date(Date.now() - 1000 * 60).toISOString(),
            action: 'upload',
            path: '/documents/report.pdf',
            user: 'admin',
            details: {
              client: 'web',
              result: 'ok',
            },
          },
          {
            id: '2',
            timestamp: new Date(Date.now() - 1000 * 120).toISOString(),
            action: 'download',
            path: '/documents/report.pdf',
            user: 'admin',
          },
          {
            id: '3',
            timestamp: new Date(Date.now() - 1000 * 180).toISOString(),
            action: 'unknown-action',
            path: '/documents/unknown.txt',
            user: 'admin',
          },
        ],
        total: 3,
        limit: 20,
        offset: 0,
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('client: web')).toBeTruthy()
        expect(screen.getByText('result: ok')).toBeTruthy()
      })
    })

    it('shows relative time', async () => {
      render(<ActivityPage />)
      
      await waitFor(() => {
        // Should show "5 分钟前" or similar
        expect(screen.getByText(/分钟前/)).toBeTruthy()
      })
    })

    it('does not reuse cached admin activity from another user session', async () => {
    useIsAdminMock.mockReturnValue(false)
      useUserMock.mockReturnValue({ id: 'scoped-user', username: 'member', role: 'user', homeDir: '/member' })
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
    queryClient.setQueryData(['activity', 1, ''], {
      items: [
        {
          id: 'act-admin',
          timestamp: '2024-01-15T10:00:00Z',
          action: 'upload',
          path: '/admin/secret.txt',
          user: 'admin',
        },
      ],
      total: 1,
      limit: 20,
      offset: 0,
    })

    render(
      <QueryClientProvider client={queryClient}>
        <ActivityPage />
      </QueryClientProvider>
    )

    await waitFor(() => {
      expect(mockListActivity).toHaveBeenCalledTimes(1)
    })

    expect(screen.queryByText('/admin/secret.txt')).toBeNull()
  })

    it('does not reuse cached activity when the same user home directory changes', async () => {
      useIsAdminMock.mockReturnValue(false)
      useUserMock.mockReturnValue({ id: 'scoped-user', username: 'member', role: 'user', homeDir: '/member-next' })
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
      queryClient.setQueryData(['activity', 'scoped-user', false, 1, ''], {
        items: [
          {
            id: 'act-old-home',
            timestamp: '2024-01-15T10:00:00Z',
            action: 'upload',
            path: '/member-old/secret.txt',
            user: 'member',
          },
        ],
        total: 1,
        limit: 20,
        offset: 0,
      })

      render(
        <QueryClientProvider client={queryClient}>
          <ActivityPage />
        </QueryClientProvider>
      )

      await waitFor(() => {
        expect(mockListActivity).toHaveBeenCalledTimes(1)
      })

      expect(screen.queryByText('/member-old/secret.txt')).toBeNull()
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
        expect(screen.getByText('暂无最近操作')).toBeTruthy()
      })
    })

    it('shows retryable error state when activity loading fails', async () => {
      mockListActivity.mockRejectedValue(new Error('Network error'))

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('加载最近操作失败')).toBeTruthy()
        expect(screen.getByText('Network error')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('shows an unavailable state when the activity log service returns 503', async () => {
      mockListActivity.mockRejectedValue(new ApiError('activity log unavailable', 503, 'SERVICE_UNAVAILABLE'))

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('最近操作暂不可用')).toBeTruthy()
        expect(screen.getByText('操作记录当前不可用，请检查设备状态或稍后重试。')).toBeTruthy()
      })
    })

    it('uses server error messages when activity reload fails with an Error object', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockListActivity.mockRejectedValueOnce(new Error('Network error'))
      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      mockListActivity.mockRejectedValueOnce(new Error('activity reload failed'))
      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '刷新失败',
          description: 'activity reload failed',
          color: 'danger',
        })
      })
    })
  })

  describe('filtering', () => {
    it('renders filter dropdown', async () => {
      render(<ActivityPage />)
      
      await waitFor(() => {
        expect(screen.getByText('筛选操作')).toBeTruthy()
        expect(screen.getAllByText('数据校验').length).toBeGreaterThan(0)
      })
    })

    it('renders refresh button', async () => {
      render(<ActivityPage />)
      
      await waitFor(() => {
        expect(screen.getByText('刷新')).toBeTruthy()
      })
    })

    it('requests the selected action and shows the active filter chip', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('筛选删除文件')).toBeTruthy()
      })

      await user.click(screen.getByText('筛选删除文件'))

      await waitFor(() => {
        expect(mockListActivity).toHaveBeenCalledWith({
          limit: 20,
          offset: 0,
          action: 'delete',
        })
        expect(screen.getByText('当前筛选:')).toBeTruthy()
      })
    })

    it('requests scrub activity when the scrub filter is selected', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('筛选数据校验')).toBeTruthy()
      })

      await user.click(screen.getByText('筛选数据校验'))

      await waitFor(() => {
        expect(mockListActivity).toHaveBeenCalledWith({
          limit: 20,
          offset: 0,
          action: 'scrub',
        })
        expect(screen.getByText('当前筛选:')).toBeTruthy()
      })
    })

    it('clears the active action filter from the chip', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('筛选删除文件')).toBeTruthy()
      })

      await user.click(screen.getByText('筛选删除文件'))

      await waitFor(() => {
        expect(screen.getByText('当前筛选:')).toBeTruthy()
      })

      await user.click(screen.getByText('清除筛选'))

      await waitFor(() => {
        expect(screen.queryByText('当前筛选:')).toBeNull()
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
        expect(mockAddToast).toHaveBeenCalledWith({ title: '最近操作已刷新', color: 'success' })
      })
    })

    it('shows warning toast when activity reload is temporarily unavailable', async () => {
        const user = userEvent.setup({ writeToClipboard: false })
        mockListActivity.mockRejectedValueOnce(new Error('Network error'))
        render(<ActivityPage />)

        await waitFor(() => {
          expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
        })

        mockListActivity.mockRejectedValueOnce(new ApiError('activity log unavailable', 503, 'SERVICE_UNAVAILABLE'))
        await user.click(screen.getByRole('button', { name: '重新加载' }))

        await waitFor(() => {
          expect(mockAddToast).toHaveBeenCalledWith({
            title: '最近操作暂不可用',
            description: '操作记录当前不可用，请检查设备状态或稍后重试。',
            color: 'warning',
          })
        })
    })

    it('shows danger toast when activity reload fails with a generic error', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockListActivity.mockRejectedValueOnce(new Error('Network error'))
      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      mockListActivity.mockRejectedValueOnce('still unavailable')
      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '刷新失败',
          description: '请稍后重试',
          color: 'danger',
        })
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

    it('returns to the last available page when refreshed data shrinks the total page count', async () => {
      mockListActivity.mockImplementation(({ offset = 0 }: { offset?: number }) => {
        if (offset === 20) {
          return Promise.resolve({
            items: [],
            total: 3,
            limit: 20,
            offset: 20,
          })
        }

        return Promise.resolve({
          items: [
            {
              id: '1',
              timestamp: new Date(Date.now() - 1000 * 60 * 5).toISOString(),
              action: 'upload',
              path: '/documents/report.pdf',
              user: 'admin',
            },
          ],
          total: 21,
          limit: 20,
          offset,
        })
      })

      const user = userEvent.setup({ writeToClipboard: false })
      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('/documents/report.pdf')).toBeTruthy()
        expect(screen.getByText('转到第 2 页')).toBeTruthy()
      })

      await user.click(screen.getByText('转到第 2 页'))

      await waitFor(() => {
        expect(mockListActivity).toHaveBeenCalledWith({
          limit: 20,
          offset: 20,
          action: undefined,
        })
      })

      await waitFor(() => {
        expect(mockListActivity).toHaveBeenCalledWith({
          limit: 20,
          offset: 0,
          action: undefined,
        })
        expect(screen.getByText('/documents/report.pdf')).toBeTruthy()
      })
    })
  })
})
