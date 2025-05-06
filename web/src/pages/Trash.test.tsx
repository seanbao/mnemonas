import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TrashPage } from './Trash'
import * as HeroUI from '@heroui/react'

const mockAddToast = vi.fn()
const useCanWriteMock = vi.fn(() => true)
const mockUser = { id: 'u1', username: 'admin', role: 'admin' as const, email: 'admin@local', homeDir: '/' }

// Mock API functions
vi.mock('@/api/files', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/files')>()
  return {
    ...actual,
    listTrash: vi.fn(),
    restoreFromTrash: vi.fn(),
    deleteFromTrash: vi.fn(),
    emptyTrash: vi.fn(),
    getFileIcon: vi.fn(() => 'file'),
  }
})

// Mock useBatchOperation hook
const mockBatchExecute = vi.fn()
let mockUseRealBatchOperation = false
let mockBatchResult = {
  succeeded: 1,
  failed: 0,
  total: 1,
  succeededItems: ['item1'],
  failedItems: [] as string[],
  failedErrors: [] as unknown[],
  warningCount: 0,
  warningMessages: [] as string[],
}
vi.mock('@/lib/useBatchOperation', () => ({
  useBatchOperation: (options: {
    operation: (item: string) => Promise<unknown>
    messages: {
      success: string
      failure: string
      partial: string
    }
    getToast?: (result: typeof mockBatchResult) => { title: string; description?: string; color: 'success' | 'warning' | 'danger' } | null | undefined
    onComplete?: (result: typeof mockBatchResult) => void
  }) => ({
    execute: vi.fn(async (items: string[]) => {
      if (mockUseRealBatchOperation) {
        const results = await Promise.allSettled(items.map((item) => options.operation(item)))
        const warningMessages = results
          .filter((result): result is PromiseFulfilledResult<unknown> => result.status === 'fulfilled')
          .map((result) => result.value)
          .filter((value): value is { warning?: boolean; message?: string } => !!value && typeof value === 'object')
          .map((value) => value.warning ? value.message : undefined)
          .filter((message): message is string => typeof message === 'string')
        const result = {
          succeeded: results.filter((result) => result.status === 'fulfilled').length,
          failed: results.filter((result) => result.status === 'rejected').length,
          total: items.length,
          succeededItems: items.filter((_, index) => results[index]?.status === 'fulfilled'),
          failedItems: items.filter((_, index) => results[index]?.status === 'rejected'),
          failedErrors: results
            .filter((result): result is PromiseRejectedResult => result.status === 'rejected')
            .map((result) => result.reason),
          warningCount: warningMessages.length,
          warningMessages,
        }
        mockBatchExecute(items)
        const toast = options.getToast?.(result) ?? (result.failed === 0
          ? {
            title: result.warningMessages[0] ?? options.messages.success.replace('{count}', String(result.succeeded)),
            color: result.warningMessages.length > 0 ? 'warning' as const : 'success' as const,
          }
          : result.succeeded === 0
            ? {
              title: options.messages.failure.replace('{count}', String(result.failed)),
              color: 'danger' as const,
            }
            : {
              title: options.messages.partial
                .replace('{succeeded}', String(result.succeeded))
                .replace('{failed}', String(result.failed)),
              color: 'warning' as const,
            })
        if (toast) {
          mockAddToast(toast)
        }
        options.onComplete?.(result)
        return result
      }

      const result = {
        ...mockBatchResult,
        failedErrors: mockBatchResult.failedErrors ?? [],
        warningCount: mockBatchResult.warningCount ?? 0,
        warningMessages: mockBatchResult.warningMessages ?? [],
        total: items.length,
      }
      mockBatchExecute(items)
      const toast = options.getToast?.(result) ?? (result.failed === 0
        ? {
          title: result.warningMessages[0] ?? options.messages.success.replace('{count}', String(result.succeeded)),
          color: result.warningMessages.length > 0 ? 'warning' as const : 'success' as const,
        }
        : result.succeeded === 0
          ? {
            title: options.messages.failure.replace('{count}', String(result.failed)),
            color: 'danger' as const,
          }
          : {
            title: options.messages.partial
              .replace('{succeeded}', String(result.succeeded))
              .replace('{failed}', String(result.failed)),
            color: 'warning' as const,
          })
      if (toast) {
        mockAddToast(toast)
      }
      options.onComplete?.(result)
      return result
    }),
    isLoading: false,
  }),
}))

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useCanWrite: () => useCanWriteMock(),
    useUser: () => mockUser,
  }
})

import { ApiError, listTrash, restoreFromTrash, deleteFromTrash, emptyTrash } from '@/api/files'

const mockListTrash = vi.mocked(listTrash)
const mockRestoreFromTrash = vi.mocked(restoreFromTrash)
const mockDeleteFromTrash = vi.mocked(deleteFromTrash)
const mockEmptyTrash = vi.mocked(emptyTrash)

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('TrashPage', () => {
  const pendingTrashRefetch = () => new Promise<Awaited<ReturnType<typeof listTrash>>>(() => {})

  beforeEach(() => {
    vi.clearAllMocks()
    useCanWriteMock.mockReturnValue(true)
    mockUser.id = 'u1'
    mockUser.username = 'admin'
    mockUser.role = 'admin'
    mockUser.email = 'admin@local'
    mockUser.homeDir = '/'
    mockUseRealBatchOperation = false
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
    mockBatchExecute.mockClear()
    mockBatchResult = {
      succeeded: 1,
      failed: 0,
      total: 1,
      succeededItems: ['item1'],
      failedItems: [],
      failedErrors: [],
      warningCount: 0,
      warningMessages: [],
    }
    mockListTrash.mockResolvedValue({
      items: [
        {
          id: 'item1',
          originalPath: '/deleted-file.txt',
          deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(), // 1 hour ago
          name: 'deleted-file.txt',
          isDir: false,
          size: 1024,
        },
        {
          id: 'item2',
          originalPath: '/deleted-folder',
          deletedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(), // 1 day ago
          name: 'deleted-folder',
          isDir: true,
          size: 0,
        },
      ],
      count: 2,
      totalSize: 1024,
    })
  })

  describe('rendering', () => {
    it('shows loading state initially', () => {
      mockListTrash.mockImplementation(() => new Promise(() => {}))
      render(<TrashPage />)
      
      // Should show skeleton loaders
      const skeletons = document.querySelectorAll('[class*="skeleton"], [class*="animate"]')
      expect(skeletons.length).toBeGreaterThan(0)
    })

    it('shows an invalid-home error instead of loading trash for non-admin users without a home directory', async () => {
      mockUser.id = 'u2'
      mockUser.username = 'member'
      mockUser.role = 'user'
      mockUser.homeDir = ''

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getAllByText('主目录配置无效').length).toBeGreaterThan(0)
        expect(screen.getByText('当前账户未配置有效的主目录，无法查看回收站。请联系管理员修复账户 home_dir。')).toBeTruthy()
      })

      expect(mockListTrash).not.toHaveBeenCalled()
    })

    it('renders page header', async () => {
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('回收站')).toBeTruthy()
      })
    })

    it('calls listTrash API with correct data', async () => {
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(mockListTrash).toHaveBeenCalled()
      })
      
      // Verify the mock returned data structure
      const mockResult = await mockListTrash()
      expect(mockResult.count).toBe(2)
      expect(mockResult.totalSize).toBe(1024)
    })

    it('shows trash items after loading', async () => {
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
        expect(screen.getByText('deleted-folder')).toBeTruthy()
      })
    })

    it('displays original paths', async () => {
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('/deleted-file.txt')).toBeTruthy()
        expect(screen.getByText('/deleted-folder')).toBeTruthy()
      })
    })

    it('shows relative time for deletion', async () => {
      render(<TrashPage />)
      
      await waitFor(() => {
        // 1 hour ago
        expect(screen.getByText(/小时前|分钟前|刚刚/)).toBeTruthy()
      })
    })

    it('does not reuse cached trash items from another user session', async () => {
    mockUser.id = 'u2'
    mockUser.username = 'member'
    mockUser.role = 'user'
    mockUser.homeDir = '/member'
    mockListTrash.mockImplementation(() => pendingTrashRefetch())

    const queryClient = new QueryClient({
      defaultOptions: {
        queries: {
          retry: false,
          gcTime: 0,
          staleTime: 0,
        },
      },
    })
    queryClient.setQueryData(['trash'], {
      items: [
        {
          id: 'admin-item',
          originalPath: '/admin/secret.txt',
          deletedAt: '2024-01-15T10:00:00Z',
          name: 'secret.txt',
          isDir: false,
          size: 128,
        },
      ],
      count: 1,
      totalSize: 128,
    })

    render(
      <QueryClientProvider client={queryClient}>
        <TrashPage />
      </QueryClientProvider>
    )

    await waitFor(() => {
      expect(mockListTrash).toHaveBeenCalledTimes(1)
    })

    expect(screen.queryByText('/admin/secret.txt')).toBeNull()
    expect(screen.queryByText('secret.txt')).toBeNull()
  })

    it('hides guest write controls on trash page', async () => {
      useCanWriteMock.mockReturnValue(false)

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      expect(screen.queryByText('清空回收站')).toBeNull()
      expect(screen.queryByRole('button', { name: '恢复 deleted-file.txt' })).toBeNull()
      expect(screen.queryByRole('button', { name: '永久删除 deleted-file.txt' })).toBeNull()
      expect(screen.queryByText(/已选择.*项/)).toBeNull()
    })
  })

  describe('empty state', () => {
    it('shows empty message when trash is empty', async () => {
      mockListTrash.mockResolvedValue({
        items: [],
        count: 0,
        totalSize: 0,
      })
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('回收站是空的')).toBeTruthy()
      })
    })

    it('does not show empty trash button when empty', async () => {
      mockListTrash.mockResolvedValue({
        items: [],
        count: 0,
        totalSize: 0,
      })
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.queryByText('清空回收站')).toBeFalsy()
      })
    })

    it('shows retryable error state when trash loading fails', async () => {
      mockListTrash.mockRejectedValue(new Error('Network error'))

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('加载回收站失败')).toBeTruthy()
        expect(screen.getByText('Network error')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('shows unavailable state when trash backend is unavailable', async () => {
      mockListTrash.mockRejectedValue(new ApiError(
        'filesystem not initialized',
        503,
        'Service Unavailable',
        'SERVICE_UNAVAILABLE'
      ))

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('回收站暂不可用')).toBeTruthy()
        expect(screen.getByText('文件系统当前不可用，请稍后重试')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('shows success toast when trash reload succeeds', async () => {
      const user = userEvent.setup()
      mockListTrash
        .mockRejectedValueOnce(new Error('Network error'))
        .mockResolvedValueOnce({
          items: [
            {
              id: 'item1',
              originalPath: '/deleted-file.txt',
              deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
              name: 'deleted-file.txt',
              isDir: false,
              size: 1024,
            },
          ],
          count: 1,
          totalSize: 1024,
        })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '回收站已刷新', color: 'success' })
      })
    })

    it('shows warning toast when trash reload becomes unavailable', async () => {
      const user = userEvent.setup()
      mockListTrash
        .mockRejectedValueOnce(new Error('Network error'))
        .mockRejectedValueOnce(new ApiError('filesystem not initialized', 503, 'Service Unavailable', 'SERVICE_UNAVAILABLE'))

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '回收站暂不可用',
          description: '文件系统当前不可用，请稍后重试',
          color: 'warning',
        })
      })
    })

    it('shows generic failure toast when trash reload fails without an Error object', async () => {
      const user = userEvent.setup()
      mockListTrash
        .mockRejectedValueOnce(new Error('Network error'))
        .mockRejectedValueOnce('still broken')

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '刷新失败',
          description: '请稍后重试',
          color: 'danger',
        })
      })
    })

    it('shows unknown retention copy when retention settings are missing', async () => {
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getAllByText(/自动清理设置未知/).length).toBeGreaterThan(1)
      })
    })

    it('calculates total trash size when the response omits totalSize', async () => {
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
          {
            id: 'item2',
            originalPath: '/deleted-folder',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            name: 'deleted-folder',
            isDir: true,
            size: 2048,
          },
        ],
        count: 2,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/2 项 · 3 KB/)).toBeTruthy()
      })
    })

    it('shows immediate expiry copy when retention is enabled with zero days', async () => {
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 0,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/立即过期，等待清理/)).toBeTruthy()
        expect(screen.getByText('已过期，等待清理')).toBeTruthy()
      })
      expect(screen.queryByText('自动清理未启用')).toBeNull()
    })

    it('treats negative retention windows as immediately expired', async () => {
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: -1,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/立即过期，等待清理/)).toBeTruthy()
        expect(screen.getByText('已过期，等待清理')).toBeTruthy()
      })
    })

    it('shows disabled retention copy without row countdown badges', async () => {
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: false,
        retentionDays: 7,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/自动清理未启用/)).toBeTruthy()
      })
      expect(screen.queryByText(/天后自动删除/)).toBeNull()
    })

    it('shows near auto-delete countdown badges when retention is within a week', async () => {
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 7,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/7 天后自动清理/)).toBeTruthy()
        expect(screen.getByText(/天后自动删除/)).toBeTruthy()
      })
    })

    it('hides row countdown badges when auto-delete is more than a week away', async () => {
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 30,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/30 天后自动清理/)).toBeTruthy()
      })
      expect(screen.queryByText(/天后自动删除/)).toBeNull()
    })

    it('shows expired cleanup badge instead of zero-day countdown', async () => {
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 8 * 24 * 60 * 60 * 1000).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 7,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('已过期，等待清理')).toBeTruthy()
      })
      expect(screen.queryByText('0 天后自动删除')).toBeNull()
    })
  })

  describe('restore functionality', () => {
    it('exposes accessible labels for row actions', async () => {
      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '恢复 deleted-file.txt' })).toBeTruthy()
        expect(screen.getByRole('button', { name: '永久删除 deleted-file.txt' })).toBeTruthy()
      })
    })

    it('restores item on restore button click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
		mockRestoreFromTrash.mockResolvedValue({ warning: false })
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const restoreButtons = screen.getAllByTitle('恢复')
      await user.click(restoreButtons[0])

      await waitFor(() => {
        expect(mockRestoreFromTrash).toHaveBeenCalledWith('item1')
      })
    })

    it('shows unavailable toast when restore is unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockRestoreFromTrash.mockRejectedValue(new ApiError(
        'filesystem not initialized',
        503,
        'Service Unavailable',
        'SERVICE_UNAVAILABLE'
      ))

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '恢复 deleted-file.txt' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '恢复暂不可用',
          description: '文件系统当前不可用，请稍后重试',
          color: 'warning',
        })
      })
    })

    it('shows generic failure toast when restore fails without an Error object', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockRestoreFromTrash.mockRejectedValue('restore stopped')

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '恢复 deleted-file.txt' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '恢复失败',
          description: '请稍后重试',
          color: 'danger',
        })
      })
    })

    it('removes a stale trash item and shows a warning when restore hits not found', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockRestoreFromTrash.mockRejectedValue(new ApiError('trash item not found', 404, 'Not Found', 'TRASH_NOT_FOUND'))
      mockListTrash.mockReset()
      mockListTrash.mockResolvedValueOnce({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
          {
            id: 'item2',
            originalPath: '/deleted-folder',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(),
            name: 'deleted-folder',
            isDir: true,
            size: 0,
          },
        ],
        count: 2,
        totalSize: 1024,
      })
      mockListTrash.mockImplementation(() => pendingTrashRefetch())

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '恢复 deleted-file.txt' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '回收站条目已不存在，已同步更新',
          color: 'warning',
        })
      })

      await waitFor(() => {
        expect(screen.queryByText('deleted-file.txt')).toBeNull()
        expect(screen.getByText('deleted-folder')).toBeTruthy()
      })
    })

  it('shows warning toast when restore succeeds with a warning', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockRestoreFromTrash.mockResolvedValue({
      warning: true,
      message: 'file restored with metadata warning',
    })

    render(<TrashPage />)

    await waitFor(() => {
      expect(screen.getByText('deleted-file.txt')).toBeTruthy()
    })

    await user.click(screen.getByRole('button', { name: '恢复 deleted-file.txt' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: 'file restored with metadata warning',
        color: 'warning',
      })
    })
  })

    it('optimistically removes a restored selected item before trash refetch completes', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
		mockRestoreFromTrash.mockResolvedValue({ warning: false })
      mockListTrash.mockReset()
      mockListTrash.mockResolvedValueOnce({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
          {
            id: 'item2',
            originalPath: '/deleted-folder',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(),
            name: 'deleted-folder',
            isDir: true,
            size: 0,
          },
        ],
        count: 2,
        totalSize: 1024,
      })
      mockListTrash.mockImplementation(() => pendingTrashRefetch())

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 1) {
        await user.click(checkboxes[1] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '恢复 deleted-file.txt' }))

      await waitFor(() => {
        expect(mockRestoreFromTrash).toHaveBeenCalledWith('item1')
      })

      await waitFor(() => {
        expect(screen.queryByText('deleted-file.txt')).toBeNull()
        expect(screen.queryByText('/deleted-file.txt')).toBeNull()
        expect(screen.queryByText(/已选择.*项/)).toBeNull()
        expect(screen.getByText('deleted-folder')).toBeTruthy()
      })
    })
  })

  describe('delete functionality', () => {
    it('has delete buttons available', async () => {
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const deleteButtons = screen.getAllByTitle('永久删除')
      expect(deleteButtons.length).toBeGreaterThan(0)
    })

    it('closes the delete confirmation modal when cancellation is allowed', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '永久删除 deleted-file.txt' }))

      await waitFor(() => {
        expect(screen.getByText(/确定要永久删除/)).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      await waitFor(() => {
        expect(screen.queryByText(/确定要永久删除/)).toBeNull()
      })
    })

    it('deletes item on confirm', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
		mockDeleteFromTrash.mockResolvedValue({ warning: false })
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '永久删除 deleted-file.txt' }))

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '永久删除' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '永久删除' }))

      await waitFor(() => {
        expect(mockDeleteFromTrash).toHaveBeenCalledWith('item1')
      })
    })

    it('shows unavailable toast when permanent delete is unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockDeleteFromTrash.mockRejectedValue(new ApiError(
        'filesystem not initialized',
        503,
        'Service Unavailable',
        'SERVICE_UNAVAILABLE'
      ))

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '永久删除 deleted-file.txt' }))

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '永久删除' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '永久删除' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '永久删除暂不可用',
          description: '文件系统当前不可用，请稍后重试',
          color: 'warning',
        })
      })
    })

    it('removes a stale trash item and closes the modal when permanent delete hits not found', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockDeleteFromTrash.mockRejectedValue(new ApiError('trash item not found', 404, 'Not Found', 'TRASH_NOT_FOUND'))
      mockListTrash.mockReset()
      mockListTrash.mockResolvedValueOnce({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
          {
            id: 'item2',
            originalPath: '/deleted-folder',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(),
            name: 'deleted-folder',
            isDir: true,
            size: 0,
          },
        ],
        count: 2,
        totalSize: 1024,
      })
      mockListTrash.mockImplementation(() => pendingTrashRefetch())

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '永久删除 deleted-file.txt' }))

      await waitFor(() => {
        expect(screen.getByText(/确定要永久删除/)).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '永久删除' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '回收站条目已不存在，已同步更新',
          color: 'warning',
        })
      })

      await waitFor(() => {
        expect(screen.queryByText('deleted-file.txt')).toBeNull()
        expect(screen.queryByText(/确定要永久删除/)).toBeNull()
        expect(screen.getByText('deleted-folder')).toBeTruthy()
      })
    })

  it('shows warning toast when permanent delete succeeds with a warning', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockDeleteFromTrash.mockResolvedValue({
      warning: true,
      message: 'item permanently deleted with cleanup warning',
    })

    render(<TrashPage />)

    await waitFor(() => {
      expect(screen.getByText('deleted-file.txt')).toBeTruthy()
    })

    await user.click(screen.getByRole('button', { name: '永久删除 deleted-file.txt' }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '永久删除' })).toBeTruthy()
    })

    await user.click(screen.getByRole('button', { name: '永久删除' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: 'item permanently deleted with cleanup warning',
        color: 'warning',
      })
    })
  })

    it('keeps the delete modal open when a pending permanent delete later fails', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
		const pendingDelete = createDeferred<{ warning: boolean }>()
      mockDeleteFromTrash.mockImplementationOnce(() => pendingDelete.promise)

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '永久删除 deleted-file.txt' }))

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '永久删除' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '永久删除' }))

      await waitFor(() => {
        expect(mockDeleteFromTrash).toHaveBeenCalledWith('item1')
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      expect(screen.getByRole('button', { name: '永久删除' })).toBeTruthy()

      await act(async () => {
        pendingDelete.reject(new ApiError(
          'filesystem not initialized',
          503,
          'Service Unavailable',
          'SERVICE_UNAVAILABLE'
        ))
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '永久删除暂不可用',
          description: '文件系统当前不可用，请稍后重试',
          color: 'warning',
        })
      })

      expect(screen.getByRole('button', { name: '永久删除' })).toBeTruthy()
    })

    it('keeps a newer delete modal open when an older delete request resolves', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
		const pendingDelete = createDeferred<{ warning: boolean }>()
      mockDeleteFromTrash.mockImplementationOnce(() => pendingDelete.promise)

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
        expect(screen.getByText('deleted-folder')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '永久删除 deleted-file.txt' }))

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '永久删除' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '永久删除' }))

      await waitFor(() => {
        expect(mockDeleteFromTrash).toHaveBeenCalledWith('item1')
      })

      await user.click(screen.getByRole('button', { name: '取消' }))
      await user.click(screen.getByRole('button', { name: '永久删除 deleted-folder' }))

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '取消' })).toBeTruthy()
        expect(screen.getAllByText('deleted-folder').length).toBeGreaterThan(1)
      })

      await act(async () => {
  		pendingDelete.resolve({ warning: false })
      })

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '取消' })).toBeTruthy()
        expect(screen.getAllByText('deleted-folder').length).toBeGreaterThan(1)
      })
    })
  })

  describe('empty trash', () => {
    it('shows empty trash button', async () => {
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })
    })

    it('opens confirmation modal on empty trash click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })

      await user.click(screen.getByText('清空回收站'))

      await waitFor(() => {
        expect(screen.getByText('确定要清空回收站吗？')).toBeTruthy()
      })
    })

    it('closes the empty trash modal when cancellation is allowed', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })

      await user.click(screen.getByText('清空回收站'))

      await waitFor(() => {
        expect(screen.getByText('确定要清空回收站吗？')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      await waitFor(() => {
        expect(screen.queryByText('确定要清空回收站吗？')).toBeNull()
      })
    })

    it('empties trash on confirm', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockEmptyTrash.mockResolvedValue({ deletedCount: 2, partial: false })
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })

      await user.click(screen.getByText('清空回收站'))

      await waitFor(() => {
        // Find the button in modal footer
        const buttons = screen.getAllByText('清空回收站')
        const confirmBtn = buttons.find(btn => btn.closest('[class*="ModalFooter"], footer'))
        if (confirmBtn) {
          return user.click(confirmBtn)
        }
        // Click the last one (modal button)
        return user.click(buttons[buttons.length - 1])
      })

      await waitFor(() => {
        expect(mockEmptyTrash).toHaveBeenCalled()
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '已清空回收站，删除 2 项', color: 'success' })
      })
    })

    it('shows warning toast when empty trash partially succeeds', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockEmptyTrash.mockResolvedValue({ deletedCount: 1, partial: true })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })

      await user.click(screen.getByText('清空回收站'))

      await waitFor(() => {
        const buttons = screen.getAllByText('清空回收站')
        const confirmBtn = buttons.find(btn => btn.closest('[class*="ModalFooter"], footer'))
        if (confirmBtn) {
          return user.click(confirmBtn)
        }
        return user.click(buttons[buttons.length - 1])
      })

      await waitFor(() => {
        expect(mockEmptyTrash).toHaveBeenCalled()
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '回收站已部分清空，删除 1 项', color: 'warning' })
      })
    })

    it('shows warning toast when empty trash succeeds with cleanup warnings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockEmptyTrash.mockResolvedValue({
        deletedCount: 2,
        partial: false,
        warning: true,
        message: 'trash emptied with cleanup warning',
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })

      await user.click(screen.getByText('清空回收站'))

      await waitFor(() => {
        const buttons = screen.getAllByText('清空回收站')
        const confirmBtn = buttons.find(btn => btn.closest('[class*="ModalFooter"], footer'))
        if (confirmBtn) {
          return user.click(confirmBtn)
        }
        return user.click(buttons[buttons.length - 1])
      })

      await waitFor(() => {
        expect(mockEmptyTrash).toHaveBeenCalled()
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: 'trash emptied with cleanup warning', color: 'warning' })
      })
    })

    it('shows unavailable toast when empty trash is unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockEmptyTrash.mockRejectedValue(new ApiError(
        'filesystem not initialized',
        503,
        'Service Unavailable',
        'SERVICE_UNAVAILABLE'
      ))

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })

      await user.click(screen.getByText('清空回收站'))

      await waitFor(() => {
        const buttons = screen.getAllByText('清空回收站')
        const confirmBtn = buttons.find(btn => btn.closest('[class*="ModalFooter"], footer'))
        if (confirmBtn) {
          return user.click(confirmBtn)
        }
        return user.click(buttons[buttons.length - 1])
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '清空回收站暂不可用',
          description: '文件系统当前不可用，请稍后重试',
          color: 'warning',
        })
      })
    })

    it('keeps the empty trash modal open when a pending request later fails', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const pendingEmpty = createDeferred<{ deletedCount: number; partial: boolean }>()
      mockEmptyTrash.mockImplementationOnce(() => pendingEmpty.promise)

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })

      await user.click(screen.getByText('清空回收站'))

      await waitFor(() => {
        expect(screen.getByText('确定要清空回收站吗？')).toBeTruthy()
      })

      const confirmButtons = screen.getAllByText('清空回收站')
      const confirmButton = confirmButtons.find(btn => btn.closest('[class*="ModalFooter"], footer')) ?? confirmButtons[confirmButtons.length - 1]

      await user.click(confirmButton)

      await waitFor(() => {
        expect(mockEmptyTrash).toHaveBeenCalled()
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      expect(screen.getByText('确定要清空回收站吗？')).toBeTruthy()

      await act(async () => {
        pendingEmpty.reject(new ApiError(
          'filesystem not initialized',
          503,
          'Service Unavailable',
          'SERVICE_UNAVAILABLE'
        ))
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '清空回收站暂不可用',
          description: '文件系统当前不可用，请稍后重试',
          color: 'warning',
        })
      })

      expect(screen.getByText('确定要清空回收站吗？')).toBeTruthy()
    })
  })

  describe('selection', () => {
    it('shows selection bar when items selected', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      // Click checkbox to select item
      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 1) {
        await user.click(checkboxes[1] as Element) // First item checkbox (skip header)
      }

      await waitFor(() => {
        // Selection bar should appear
        expect(screen.getByText(/已选择.*项/)).toBeTruthy()
      })
    })

    it('can select all items', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      // Click header checkbox to select all
      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 0) {
        await user.click(checkboxes[0] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })
    })

    it('clears all selected items from the header checkbox', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 0) {
        await user.click(checkboxes[0] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      if (checkboxes.length > 0) {
        await user.click(checkboxes[0] as Element)
      }

      await waitFor(() => {
        expect(screen.queryByText(/已选择.*项/)).toBeNull()
      })
    })

    it('clears selected trash items from the selection bar action', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 1) {
        await user.click(checkboxes[1] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '取消选择' }))

      expect(screen.queryByText(/已选择.*项/)).toBeNull()
    })

    it('toggles an individual row selection back off', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const firstCheckboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (firstCheckboxes.length > 1) {
        await user.click(firstCheckboxes[1] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })

      const secondCheckboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (secondCheckboxes.length > 1) {
        await user.click(secondCheckboxes[1] as Element)
      }

      await waitFor(() => {
        expect(screen.queryByText(/已选择.*项/)).toBeNull()
      })
    })

    it('keeps still-present selected items selected after restoring one selected row', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockRestoreFromTrash.mockResolvedValue({ warning: false })
      mockListTrash.mockReset()
      mockListTrash.mockResolvedValueOnce({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
          {
            id: 'item2',
            originalPath: '/deleted-folder',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(),
            name: 'deleted-folder',
            isDir: true,
            size: 0,
          },
        ],
        count: 2,
        totalSize: 1024,
      })
      mockListTrash.mockImplementation(() => pendingTrashRefetch())

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 0) {
        await user.click(checkboxes[0] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '恢复 deleted-file.txt' }))

      await waitFor(() => {
        expect(mockRestoreFromTrash).toHaveBeenCalledWith('item1')
      })

      await waitFor(() => {
        expect(screen.queryByText('deleted-file.txt')).toBeNull()
        expect(screen.getByText('deleted-folder')).toBeTruthy()
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })
    })

    it('uses a fallback batch warning title when warning details are omitted', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockBatchResult = {
        succeeded: 1,
        failed: 0,
        total: 1,
        succeededItems: ['item1'],
        failedItems: [],
        failedErrors: [],
        warningCount: 1,
        warningMessages: [],
      }

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 1) {
        await user.click(checkboxes[1] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })

      await user.click(screen.getByText('恢复'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '已恢复 1 项，但存在警告',
          color: 'warning',
        })
      })
    })

    it('shows warning toast for successful batch restore with warnings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUseRealBatchOperation = true
      mockRestoreFromTrash.mockResolvedValue({
        warning: true,
        message: 'restore completed with warnings',
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 0) {
        await user.click(checkboxes[0] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await user.click(screen.getByText('恢复'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'restore completed with warnings',
          color: 'warning',
        })
      })
    })

    it('shows unavailable toast when all batch restore items are unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUseRealBatchOperation = true
      mockRestoreFromTrash.mockRejectedValue(new ApiError(
        'filesystem not initialized',
        503,
        'Service Unavailable',
        'SERVICE_UNAVAILABLE'
      ))

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 0) {
        await user.click(checkboxes[0] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await user.click(screen.getByText('恢复'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '批量恢复暂不可用',
          description: '文件系统当前不可用，请稍后重试',
          color: 'warning',
        })
      })
    })

    it('falls back to generic batch restore failure toast for non-unavailable errors', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUseRealBatchOperation = true
      mockRestoreFromTrash.mockRejectedValue(new Error('restore failed'))

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 0) {
        await user.click(checkboxes[0] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await user.click(screen.getByText('恢复'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '2 项恢复失败',
          color: 'danger',
        })
      })
    })

    it('shows unavailable toast when all batch permanent delete items are unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUseRealBatchOperation = true
      mockDeleteFromTrash.mockRejectedValue(new ApiError(
        'filesystem not initialized',
        503,
        'Service Unavailable',
        'SERVICE_UNAVAILABLE'
      ))

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 1) {
        await user.click(checkboxes[1] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })

      await user.click(screen.getByText('永久删除'))

      await waitFor(() => {
        expect(screen.getByText('确认批量永久删除')).toBeTruthy()
      })

      const confirmButtons = screen.getAllByRole('button', { name: '永久删除' })
      await user.click(confirmButtons[confirmButtons.length - 1])

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '批量永久删除暂不可用',
          description: '文件系统当前不可用，请稍后重试',
          color: 'warning',
        })
      })
    })

    it('confirms before batch permanent delete', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 1) {
        await user.click(checkboxes[1] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })

      const deleteButtons = screen.getAllByRole('button', { name: '永久删除' })
      await user.click(deleteButtons[0])

      await waitFor(() => {
        expect(screen.getByText('确认批量永久删除')).toBeTruthy()
      })

      expect(mockBatchExecute).not.toHaveBeenCalled()

      const confirmButtons = screen.getAllByRole('button', { name: '永久删除' })
      await user.click(confirmButtons[confirmButtons.length - 1])

      await waitFor(() => {
        expect(mockBatchExecute).toHaveBeenCalledWith(['item1'])
      })
    })

    it('closes the batch permanent delete modal when cancellation is allowed', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 1) {
        await user.click(checkboxes[1] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })

      await user.click(screen.getByText('永久删除'))

      await waitFor(() => {
        expect(screen.getByText('确认批量永久删除')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      expect(screen.queryByText('确认批量永久删除')).toBeNull()
      expect(mockBatchExecute).not.toHaveBeenCalled()
    })

    it('keeps failed items selected after partial batch restore failure', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockBatchResult = {
        succeeded: 1,
        failed: 1,
        total: 2,
        succeededItems: ['item1'],
        failedItems: ['item2'],
        failedErrors: [new Error('restore failed')],
        warningCount: 0,
        warningMessages: [],
      }

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 0) {
        await user.click(checkboxes[0] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await user.click(screen.getByText('恢复'))

      await waitFor(() => {
        expect(mockBatchExecute).toHaveBeenCalledWith(['item1', 'item2'])
      })

      await waitFor(() => {
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })
    })

    it('keeps failed items selected after batch permanent delete failure', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockBatchResult = {
        succeeded: 0,
        failed: 1,
        total: 1,
        succeededItems: [],
        failedItems: ['item1'],
        failedErrors: [new Error('delete failed')],
        warningCount: 0,
        warningMessages: [],
      }

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 1) {
        await user.click(checkboxes[1] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })

      await user.click(screen.getByText('永久删除'))

      await waitFor(() => {
        expect(screen.getByText('确认批量永久删除')).toBeTruthy()
      })

      const confirmButtons = screen.getAllByRole('button', { name: '永久删除' })
      await user.click(confirmButtons[confirmButtons.length - 1])

      await waitFor(() => {
        expect(mockBatchExecute).toHaveBeenCalledWith(['item1'])
      })

      await waitFor(() => {
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })
    })

    it('optimistically removes batch-deleted items before trash refetch completes', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockBatchResult = {
        succeeded: 2,
        failed: 0,
        total: 2,
        succeededItems: ['item1', 'item2'],
        failedItems: [],
        failedErrors: [],
        warningCount: 0,
        warningMessages: [],
      }
      mockListTrash.mockReset()
      mockListTrash.mockResolvedValueOnce({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
          {
            id: 'item2',
            originalPath: '/deleted-folder',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(),
            name: 'deleted-folder',
            isDir: true,
            size: 0,
          },
        ],
        count: 2,
        totalSize: 1024,
      })
      mockListTrash.mockImplementation(() => pendingTrashRefetch())

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 0) {
        await user.click(checkboxes[0] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await user.click(screen.getByText('永久删除'))

      await waitFor(() => {
        expect(screen.getByText('确认批量永久删除')).toBeTruthy()
      })

      const confirmButtons = screen.getAllByRole('button', { name: '永久删除' })
      await user.click(confirmButtons[confirmButtons.length - 1])

      await waitFor(() => {
        expect(mockBatchExecute).toHaveBeenCalledWith(['item1', 'item2'])
      })

      await waitFor(() => {
        expect(screen.queryByText('deleted-file.txt')).toBeNull()
        expect(screen.queryByText('deleted-folder')).toBeNull()
        expect(screen.queryByText(/已选择.*项/)).toBeNull()
        expect(screen.getByText('回收站是空的')).toBeTruthy()
      })
    })

    it('treats batch restore not-found results as already synchronized instead of keeping stale selections', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUseRealBatchOperation = true
      mockRestoreFromTrash.mockImplementation(async (id: string) => {
        if (id === 'item2') {
          throw new ApiError('trash item not found', 404, 'Not Found', 'TRASH_NOT_FOUND')
        }

        return { warning: false }
      })
      mockListTrash.mockReset()
      mockListTrash.mockResolvedValueOnce({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
          {
            id: 'item2',
            originalPath: '/deleted-folder',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(),
            name: 'deleted-folder',
            isDir: true,
            size: 0,
          },
        ],
        count: 2,
        totalSize: 1024,
      })
      mockListTrash.mockImplementation(() => pendingTrashRefetch())

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 0) {
        await user.click(checkboxes[0] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await user.click(screen.getByText('恢复'))

      await waitFor(() => {
        expect(mockBatchExecute).toHaveBeenCalledWith(['item1', 'item2'])
      })

      await waitFor(() => {
        expect(screen.queryByText('deleted-file.txt')).toBeNull()
        expect(screen.queryByText('deleted-folder')).toBeNull()
        expect(screen.queryByText(/已选择.*项/)).toBeNull()
        expect(screen.getByText('回收站是空的')).toBeTruthy()
      })
    })
  })
})
