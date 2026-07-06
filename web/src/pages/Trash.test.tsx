import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor, within } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TrashPage } from './Trash'
import * as HeroUI from '@heroui/react'

const mockAddToast = vi.fn()
const useCanWriteMock = vi.fn(() => true)
const mockUser = { id: 'u1', username: 'admin', role: 'admin' as const, email: 'admin@local', homeDir: '/' }
const originalClipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')

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

vi.mock('@/api/activity', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/activity')>()
  return {
    ...actual,
    listActivity: vi.fn(),
    createActivityReviewRecord: vi.fn(),
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
    operation: (item: string, context: { signal?: AbortSignal }) => Promise<unknown>
    messages: {
      success: string
      failure: string
      partial: string
    }
    getWarningMessage?: (result: unknown) => string | undefined
    getToast?: (result: typeof mockBatchResult) => { title: string; description?: string; color: 'success' | 'warning' | 'danger' } | null | undefined
    onComplete?: (result: typeof mockBatchResult) => void
  }) => ({
    execute: vi.fn(async (items: string[], executeOptions: { signal?: AbortSignal } = {}) => {
      if (mockUseRealBatchOperation) {
        const results = await Promise.allSettled(items.map((item) => options.operation(item, { signal: executeOptions.signal })))
        const warningResults = results
          .filter((result): result is PromiseFulfilledResult<unknown> => result.status === 'fulfilled')
          .map((result) => result.value)
          .filter((value): value is { warning?: boolean; message?: string } => !!value && typeof value === 'object')
          .filter((value) => value.warning === true)
        const warningMessages = warningResults
          .map((value) => options.getWarningMessage?.(value))
          .filter((message): message is string => typeof message === 'string' && message.trim().length > 0)
        const result = {
          succeeded: results.filter((result) => result.status === 'fulfilled').length,
          failed: results.filter((result) => result.status === 'rejected').length,
          total: items.length,
          succeededItems: items.filter((_, index) => results[index]?.status === 'fulfilled'),
          failedItems: items.filter((_, index) => results[index]?.status === 'rejected'),
          failedErrors: results
            .filter((result): result is PromiseRejectedResult => result.status === 'rejected')
            .map((result) => result.reason),
          warningCount: warningResults.length,
          warningMessages,
        }
        mockBatchExecute(items)
        if (executeOptions.signal?.aborted) {
          return result
        }
        const hasWarnings = result.warningCount > 0 || result.warningMessages.length > 0
        const toast = options.getToast?.(result) ?? (result.failed === 0
          ? {
            title: hasWarnings
              ? `${options.messages.success.replace('{count}', String(result.succeeded))}，但存在警告`
              : options.messages.success.replace('{count}', String(result.succeeded)),
            color: hasWarnings ? 'warning' as const : 'success' as const,
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
      if (executeOptions.signal?.aborted) {
        return result
      }
      const hasWarnings = result.warningCount > 0 || result.warningMessages.length > 0
      const toast = options.getToast?.(result) ?? (result.failed === 0
        ? {
          title: hasWarnings
            ? `${options.messages.success.replace('{count}', String(result.succeeded))}，但存在警告`
            : options.messages.success.replace('{count}', String(result.succeeded)),
          color: hasWarnings ? 'warning' as const : 'success' as const,
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
import { createActivityReviewRecord, listActivity } from '@/api/activity'

const mockListTrash = vi.mocked(listTrash)
const mockRestoreFromTrash = vi.mocked(restoreFromTrash)
const mockDeleteFromTrash = vi.mocked(deleteFromTrash)
const mockEmptyTrash = vi.mocked(emptyTrash)
const mockListActivity = vi.mocked(listActivity)
const mockCreateActivityReviewRecord = vi.mocked(createActivityReviewRecord)

function expectCalledWithOnlyAbortSignal(mockFn: ReturnType<typeof vi.fn>) {
  const call = mockFn.mock.calls.find(([options]) => {
    return (options as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
  })
  expect(call).toBeTruthy()
  expect(Object.keys((call?.[0] ?? {}) as Record<string, unknown>).sort()).toEqual(['signal'])
}

function expectAbortSignal(signal: AbortSignal | undefined): asserts signal is AbortSignal {
  expect(signal).toBeDefined()
  expect(typeof signal?.aborted).toBe('boolean')
}

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

function futureTrashExpiry(days = 30): string {
  return new Date(Date.now() + days * 24 * 60 * 60 * 1000).toISOString()
}

function trashTestItem(id: string, name: string, options: {
  originalPath?: string
  isDir?: boolean
  size?: number
} = {}) {
  return {
    id,
    originalPath: options.originalPath ?? `/${name}`,
    deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
    expiresAt: futureTrashExpiry(),
    name,
    isDir: options.isDir ?? false,
    size: options.size ?? 1024,
  }
}

function emptyTrashDeletedResult(ids: string[], options: {
  remaining?: string[]
  skipped?: string[]
  warning?: boolean
  message?: string
} = {}): Awaited<ReturnType<typeof emptyTrash>> {
  const remaining = options.remaining ?? []
  const skipped = options.skipped ?? []
  return {
    deleted: [...ids],
    remaining,
    skipped,
    partial: remaining.length > 0 || skipped.length > 0,
    warning: options.warning ?? false,
    auditWarning: false,
    message: options.message,
  }
}

async function selectTrashItem(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(screen.getByRole('checkbox', { name: `选择 ${name}` }))
}

async function toggleAllTrashItems(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('checkbox', { name: /^(全选|取消全选)回收站项目$/ }))
}

function getEmptyTrashConfirmButton() {
  return screen.getByRole('button', { name: '确认清空回收站' })
}

function getModalContent(labelledBy: string): HTMLElement {
  const modalContent = document.querySelector(`[aria-labelledby="${labelledBy}"]`)
  expect(modalContent).toBeInstanceOf(HTMLElement)
  return modalContent as HTMLElement
}

async function clickEmptyTrashConfirm(user: ReturnType<typeof userEvent.setup>) {
  await waitFor(() => {
    expect(getEmptyTrashConfirmButton()).toBeTruthy()
  })
  await user.click(getEmptyTrashConfirmButton())
}

async function openBatchRestoreReview(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: '恢复' }))
  await waitFor(() => {
    expect(screen.getByText('确认批量恢复')).toBeTruthy()
    expect(screen.getByLabelText('跨目录恢复执行前复核')).toBeTruthy()
  })
}

async function clickBatchRestoreConfirm(user: ReturnType<typeof userEvent.setup>) {
  await openBatchRestoreReview(user)
  await user.click(screen.getByRole('button', { name: '确认恢复' }))
}

describe('TrashPage', () => {
  const pendingTrashRefetch = () => new Promise<Awaited<ReturnType<typeof listTrash>>>(() => {})

  beforeEach(() => {
    vi.clearAllMocks()
    window.history.pushState({}, '', '/')
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
    mockListActivity.mockResolvedValue({
      items: [],
      total: 0,
      limit: 100,
      offset: 0,
    })
    mockCreateActivityReviewRecord.mockImplementation(async (input) => ({
      id: 'trash-review-record-1',
      reviewed_at: '2026-04-12T00:00:00Z',
      reviewer: 'admin',
      note: input.note,
      scope_label: input.scope_label,
      filter_summary: input.filter_summary,
      disposition_status: input.disposition_status,
      action_counts: input.action_counts,
      review_count: input.review_count,
      total_count: input.total_count,
      path_count: input.path_count,
      user_count: input.user_count,
      path_samples: input.path_samples,
      user_samples: input.user_samples,
      activity_entry_ids: input.activity_entry_ids,
    }))
    mockListTrash.mockResolvedValue({
      items: [
        {
          id: 'item1',
          originalPath: '/deleted-file.txt',
          deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(), // 1 hour ago
          expiresAt: futureTrashExpiry(),
          name: 'deleted-file.txt',
          isDir: false,
          size: 1024,
        },
        {
          id: 'item2',
          originalPath: '/deleted-folder',
          deletedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(), // 1 day ago
          expiresAt: futureTrashExpiry(),
          name: 'deleted-folder',
          isDir: true,
          size: 0,
        },
      ],
      count: 2,
      totalSize: 1024,
      retentionEnabled: true,
      retentionDays: 30,
      trashAutoCleanupEnabled: true,
    })
  })

  afterEach(() => {
    if (originalClipboardDescriptor) {
      Object.defineProperty(navigator, 'clipboard', originalClipboardDescriptor)
    } else {
      Reflect.deleteProperty(navigator, 'clipboard')
    }
  })

  it('passes abort signals to the trash query', async () => {
    render(<TrashPage />)

    await waitFor(() => {
      expectCalledWithOnlyAbortSignal(mockListTrash)
    })
  })

  describe('rendering', () => {
    it('shows loading state initially', () => {
      mockListTrash.mockImplementation(() => new Promise(() => {}))
      render(<TrashPage />)
      
      expect(screen.getByRole('status', { name: '加载回收站' })).toBeInTheDocument()
      expect(screen.getByText('加载回收站…')).toBeInTheDocument()
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

    it('filters trash items from the path query', async () => {
      const user = userEvent.setup()
      window.history.pushState({}, '', '/trash?path=%2Fdeleted-file.txt')

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByLabelText('回收站路径筛选')).toBeTruthy()
        expect(screen.getByText('路径：/deleted-file.txt')).toBeTruthy()
        expect(screen.getByText('/deleted-file.txt')).toBeTruthy()
        expect(screen.queryByText('/deleted-folder')).toBeNull()
      })

      await user.click(screen.getByRole('button', { name: '清除路径筛选' }))

      await waitFor(() => {
        expect(window.location.search).toBe('')
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
          expiresAt: '2024-02-14T10:00:00Z',
          name: 'secret.txt',
          isDir: false,
          size: 128,
        },
      ],
      count: 1,
      totalSize: 128,
      trashAutoCleanupEnabled: true,
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
        retentionEnabled: true,
        trashAutoCleanupEnabled: true,
      })
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('回收站是空的')).toBeTruthy()
      })
    })

    it.each([
      {
        caseName: 'enabled',
        retentionEnabled: true,
        description: '新删除项目会进入回收站，并按当前策略保留',
      },
      {
        caseName: 'disabled',
        retentionEnabled: false,
        description: '当前删除方式为永久删除；新删除项目不会进入回收站',
      },
      {
        caseName: 'unknown',
        retentionEnabled: undefined,
        description: '当前删除方式未知；新删除项目是否进入回收站暂不可确认',
      },
    ])('describes the $caseName delete policy in the empty state', async ({ retentionEnabled, description }) => {
      mockListTrash.mockResolvedValue({
        items: [],
        count: 0,
        totalSize: 0,
        retentionEnabled,
        trashAutoCleanupEnabled: true,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(description)).toBeTruthy()
      })
    })

    it('does not claim that new deletions enter trash when the current delete mode is permanent', async () => {
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000).toISOString(),
            expiresAt: futureTrashExpiry(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: false,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/当前删除方式为永久删除，新删除项目不会进入回收站/)).toBeTruthy()
      })
      expect(screen.queryByText(/新删除项目会进入回收站并在 30 天后到期/)).toBeNull()
    })

    it('labels the current delete mode as unknown without inferring trash behavior', async () => {
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000).toISOString(),
            expiresAt: futureTrashExpiry(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/当前删除方式未知/)).toBeTruthy()
      })
      expect(screen.queryByText(/新删除项目会进入回收站/)).toBeNull()
    })

    it('does not show empty trash button when empty', async () => {
      mockListTrash.mockResolvedValue({
        items: [],
        count: 0,
        totalSize: 0,
        trashAutoCleanupEnabled: true,
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
        expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeTruthy()
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
              expiresAt: futureTrashExpiry(),
              name: 'deleted-file.txt',
              isDir: false,
              size: 1024,
            },
          ],
          count: 1,
          totalSize: 1024,
          trashAutoCleanupEnabled: true,
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
          description: '操作未完成，请稍后重试。',
          color: 'danger',
        })
      })
    })

    it('shows generic failure toast when trash reload fails with an Error object', async () => {
      const user = userEvent.setup()
      mockListTrash
        .mockRejectedValueOnce(new Error('Network error'))
        .mockRejectedValueOnce(new Error('reload failed'))

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '刷新失败',
          description: '操作未完成，请稍后重试。',
          color: 'danger',
        })
      })
    })

    it('shows an unknown current-policy expiry when retention days are missing', async () => {
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            expiresAt: futureTrashExpiry(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: true,
        trashAutoCleanupEnabled: true,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/新删除项目会进入回收站，到期时间未知 · 容量不足时可能提前清理/)).toBeTruthy()
      })
    })

    it('calculates total trash size when the response omits totalSize', async () => {
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            expiresAt: futureTrashExpiry(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
          {
            id: 'item2',
            originalPath: '/deleted-folder',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(),
            expiresAt: futureTrashExpiry(),
            name: 'deleted-folder',
            isDir: true,
            size: 2048,
          },
        ],
        count: 2,
        trashAutoCleanupEnabled: false,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/2 项 · 3 KB/)).toBeTruthy()
      })
    })

    it('shows immediate expiry copy when retention is enabled with zero days', async () => {
      const now = Date.now()
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(now - 1000 * 60 * 60).toISOString(),
            expiresAt: new Date(now - 1000).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 0,
        trashAutoCleanupEnabled: true,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/新删除项目会进入回收站并立即到期 · 容量不足时可能提前清理/)).toBeTruthy()
        expect(screen.getByText('已过期，等待清理')).toBeTruthy()
      })
      expect(screen.queryByText('自动清理未启用')).toBeNull()
    })

    it('keeps an existing item expiry independent from a zero-day current policy', async () => {
      const now = Date.now()
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(now - 1000 * 60 * 60).toISOString(),
            expiresAt: new Date(now + 5 * 24 * 60 * 60 * 1000).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 0,
        trashAutoCleanupEnabled: true,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/新删除项目会进入回收站并立即到期 · 容量不足时可能提前清理/)).toBeTruthy()
        expect(screen.getByText('5 天后到期')).toBeTruthy()
      })
    })

    it('shows disabled retention copy without row countdown badges', async () => {
      const now = Date.now()
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(now - 1000 * 60 * 60).toISOString(),
            expiresAt: new Date(now + 2 * 24 * 60 * 60 * 1000).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 7,
        trashAutoCleanupEnabled: false,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/自动清理未启用/)).toBeTruthy()
      })
      expect(screen.queryByText('2 天后到期')).toBeNull()
    })

    it('uses each item expiry instead of recalculating it from the current retention policy', async () => {
      const now = Date.now()
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(now - 29 * 24 * 60 * 60 * 1000).toISOString(),
            expiresAt: new Date(now + 4 * 24 * 60 * 60 * 1000).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('4 天后到期')).toBeTruthy()
        expect(screen.getByText(/新删除项目会进入回收站并在 30 天后到期 · 容量不足时可能提前清理/)).toBeTruthy()
      })
      expect(screen.queryByText('1 天后到期')).toBeNull()
    })

    it('shows an expired item from its persisted expiry even when the current policy is longer', async () => {
      const now = Date.now()
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(now - 1000).toISOString(),
            expiresAt: new Date(now - 24 * 60 * 60 * 1000).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('已过期，等待清理')).toBeTruthy()
      })
    })

    it('does not claim automatic cleanup when the trash cleanup task is disabled', async () => {
      const now = Date.now()
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(now - 1000).toISOString(),
            expiresAt: new Date(now + 2 * 24 * 60 * 60 * 1000).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 7,
        trashAutoCleanupEnabled: false,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/自动清理未启用/)).toBeTruthy()
        expect(screen.getByText(/容量不足时仍可能提前清理/)).toBeTruthy()
      })
      expect(screen.queryByText('2 天后到期')).toBeNull()
      expect(screen.queryByText(/天后自动清理/)).toBeNull()
    })

    it('shows near auto-delete countdown badges when retention is within a week', async () => {
      const now = Date.now()
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(now - 1000).toISOString(),
            expiresAt: new Date(now + 7 * 24 * 60 * 60 * 1000).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 7,
        trashAutoCleanupEnabled: true,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/新删除项目会进入回收站并在 7 天后到期 · 容量不足时可能提前清理/)).toBeTruthy()
        expect(screen.getByText('7 天后到期')).toBeTruthy()
      })
    })

    it('hides row countdown badges when auto-delete is more than a week away', async () => {
      const now = Date.now()
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(now - 1000 * 60 * 60).toISOString(),
            expiresAt: new Date(now + 30 * 24 * 60 * 60 * 1000).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText(/新删除项目会进入回收站并在 30 天后到期 · 容量不足时可能提前清理/)).toBeTruthy()
      })
      expect(screen.queryByText('30 天后到期')).toBeNull()
    })

    it('shows expired cleanup badge instead of zero-day countdown', async () => {
      const now = Date.now()
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(now - 8 * 24 * 60 * 60 * 1000).toISOString(),
            expiresAt: new Date(now - 24 * 60 * 60 * 1000).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 7,
        trashAutoCleanupEnabled: true,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('已过期，等待清理')).toBeTruthy()
      })
      expect(screen.queryByText('0 天后到期')).toBeNull()
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

      await user.click(screen.getByRole('button', { name: '恢复 deleted-file.txt' }))

      await waitFor(() => {
        expect(mockRestoreFromTrash).toHaveBeenCalledWith('item1', undefined, expect.objectContaining({ signal: expect.any(AbortSignal) }))
      })
    })

    it('records single restore execution results into activity review history', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockRestoreFromTrash.mockResolvedValue({ warning: false })
      mockListActivity.mockResolvedValueOnce({
        items: [{
          id: 'trash-restore-activity-1',
          timestamp: '2026-04-12T01:00:00Z',
          action: 'trash_restore',
          path: '/deleted-file.txt',
          user: 'admin',
        }],
        total: 1,
        limit: 100,
        offset: 0,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '恢复 deleted-file.txt' }))

      await waitFor(() => {
        expect(mockCreateActivityReviewRecord).toHaveBeenCalledTimes(1)
      })
      expect(mockListActivity).toHaveBeenCalledWith(expect.objectContaining({
        action: 'trash_restore',
        actionGroup: 'risk',
        limit: 100,
        offset: 0,
        signal: expect.any(AbortSignal),
      }))
      expect(mockCreateActivityReviewRecord).toHaveBeenCalledWith(expect.objectContaining({
        note: '回收站恢复执行结果：已恢复 1 项（0 个目录，1 个文件）；已关联 1 条恢复活动。',
        scope_label: '回收站',
        filter_summary: '审计分组 风险操作 · 执行结果 回收站恢复',
        disposition_status: 'restored',
        action_counts: { trash_restore: 1 },
        review_count: 1,
        total_count: 1,
        path_count: 1,
        user_count: 1,
        path_samples: ['/deleted-file.txt'],
        user_samples: ['admin'],
        activity_entry_ids: ['trash-restore-activity-1'],
      }), expect.objectContaining({
        signal: expect.any(AbortSignal),
      }))
      expect(mockAddToast).toHaveBeenCalledWith({ title: '恢复结果已记录', color: 'success' })
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
          description: '操作未完成，请稍后重试。',
          color: 'danger',
        })
      })
    })

    it('shows quota guidance when restore fails because quota is exceeded', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockRestoreFromTrash.mockRejectedValue(new ApiError('directory quota exceeded', 507, 'Insufficient Storage', 'QUOTA_EXCEEDED'))

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '恢复 deleted-file.txt' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '容量配额不足',
          description: '目标目录的容量配额不足，请清理空间或调整目录配额后重试。',
          color: 'warning',
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
            expiresAt: futureTrashExpiry(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
          {
            id: 'item2',
            originalPath: '/deleted-folder',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(),
            expiresAt: futureTrashExpiry(),
            name: 'deleted-folder',
            isDir: true,
            size: 0,
          },
        ],
        count: 2,
        totalSize: 1024,
        trashAutoCleanupEnabled: false,
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
          title: '恢复完成，但存在警告',
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
            expiresAt: futureTrashExpiry(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
          {
            id: 'item2',
            originalPath: '/deleted-folder',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(),
            expiresAt: futureTrashExpiry(),
            name: 'deleted-folder',
            isDir: true,
            size: 0,
          },
        ],
        count: 2,
        totalSize: 1024,
        trashAutoCleanupEnabled: false,
      })
      mockListTrash.mockImplementation(() => pendingTrashRefetch())

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await selectTrashItem(user, 'deleted-file.txt')

      await waitFor(() => {
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '恢复 deleted-file.txt' }))

      await waitFor(() => {
        expect(mockRestoreFromTrash).toHaveBeenCalledWith('item1', undefined, expect.objectContaining({ signal: expect.any(AbortSignal) }))
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

      const deleteButtons = screen.getAllByRole('button', { name: /^永久删除 / })
      expect(deleteButtons.length).toBeGreaterThan(0)
    })

    it('closes the delete confirmation modal when cancellation is allowed', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const deleteTrigger = screen.getByRole('button', { name: '永久删除 deleted-file.txt' })
      await user.click(deleteTrigger)

      await waitFor(() => {
        expect(screen.getByText(/确定要永久删除/)).toBeTruthy()
      })

      const dialog = getModalContent('trash-delete-dialog-title')
      expect(dialog).toHaveAttribute('aria-describedby', 'trash-delete-dialog-description')
      expect(dialog).toHaveAttribute('aria-busy', 'false')
      expect(within(dialog).getByRole('button', { name: '取消' })).toHaveFocus()

      await user.click(screen.getByRole('button', { name: '取消' }))

      await waitFor(() => {
        expect(screen.queryByText(/确定要永久删除/)).toBeNull()
        expect(deleteTrigger).toHaveFocus()
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
        expect(mockDeleteFromTrash).toHaveBeenCalledWith('item1', expect.objectContaining({ signal: expect.any(AbortSignal) }))
      })
    })

    it('moves focus to the stable trash region after a successful delete removes its trigger', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockDeleteFromTrash.mockResolvedValue({ warning: false })

      render(<TrashPage />)
      await screen.findByText('deleted-file.txt')
      mockListTrash.mockImplementation(() => pendingTrashRefetch())
      await user.click(screen.getByRole('button', { name: '永久删除 deleted-file.txt' }))
      await user.click(await screen.findByRole('button', { name: '永久删除' }))

      const focusFallback = screen.getByRole('region', { name: '回收站内容' })
      await waitFor(() => {
        expect(screen.queryByText('deleted-file.txt')).toBeNull()
        expect(screen.queryByText(/确定要永久删除/)).toBeNull()
        expect(focusFallback).toHaveFocus()
      })

      const remainingTrigger = screen.getByRole('button', { name: '永久删除 deleted-folder' })
      await user.click(remainingTrigger)
      await user.click(await screen.findByRole('button', { name: '取消' }))
      await waitFor(() => {
        expect(remainingTrigger).toHaveFocus()
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
            expiresAt: futureTrashExpiry(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
          {
            id: 'item2',
            originalPath: '/deleted-folder',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(),
            expiresAt: futureTrashExpiry(),
            name: 'deleted-folder',
            isDir: true,
            size: 0,
          },
        ],
        count: 2,
        totalSize: 1024,
        trashAutoCleanupEnabled: false,
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
          title: '已永久删除，但存在警告',
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
        expect(mockDeleteFromTrash).toHaveBeenCalledWith('item1', expect.objectContaining({ signal: expect.any(AbortSignal) }))
      })

      const dialog = getModalContent('trash-delete-dialog-title')
      await waitFor(() => {
        expect(dialog).toHaveAttribute('aria-busy', 'true')
      })
      await user.keyboard('{Escape}')
      expect(screen.getByText(/确定要永久删除/)).toBeTruthy()

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
        expect(mockDeleteFromTrash).toHaveBeenCalledWith('item1', expect.objectContaining({ signal: expect.any(AbortSignal) }))
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

    it('opens confirmation modal with a frozen count and total size', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })

      await user.click(screen.getByText('清空回收站'))

      await waitFor(() => {
        expect(screen.getByText('确定要清空回收站吗？')).toBeTruthy()
        expect(screen.getByText('将永久删除 2 项，共 1 KB。')).toBeTruthy()
      })

      const dialog = getModalContent('trash-empty-dialog-title')
      expect(dialog).toHaveAttribute('aria-describedby', 'trash-empty-dialog-description')
      expect(dialog).toHaveAttribute('aria-busy', 'false')
      expect(within(dialog).getByRole('button', { name: '取消' })).toHaveFocus()
    })

    it('moves focus to the stable trash region when a completed empty removes its trigger', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockEmptyTrash.mockResolvedValueOnce(emptyTrashDeletedResult(['item1', 'item2']))
      mockListTrash.mockResolvedValueOnce({
        items: [
          trashTestItem('item1', 'deleted-file.txt'),
          trashTestItem('item2', 'deleted-folder', { isDir: true, size: 0 }),
        ],
        count: 2,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      }).mockResolvedValueOnce({
        items: [],
        count: 0,
        totalSize: 0,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      })

      render(<TrashPage />)
      await user.click(await screen.findByRole('button', { name: '清空回收站' }))
      await clickEmptyTrashConfirm(user)

      const focusFallback = screen.getByRole('region', { name: '回收站内容' })
      await waitFor(() => {
        expect(screen.queryByRole('button', { name: '清空回收站' })).toBeNull()
        expect(screen.queryByText('确定要清空回收站吗？')).toBeNull()
        expect(focusFallback).toHaveFocus()
      })
    })

    it('returns focus to the empty trigger when concurrent additions keep it mounted', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const addedAfterOpen = trashTestItem('item3', 'new-after-open.txt')
      mockEmptyTrash.mockResolvedValueOnce(emptyTrashDeletedResult(['item1', 'item2']))
      mockListTrash.mockResolvedValueOnce({
        items: [
          trashTestItem('item1', 'deleted-file.txt'),
          trashTestItem('item2', 'deleted-folder', { isDir: true, size: 0 }),
        ],
        count: 2,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      }).mockResolvedValueOnce({
        items: [addedAfterOpen],
        count: 1,
        totalSize: addedAfterOpen.size,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      })

      render(<TrashPage />)
      const trigger = await screen.findByRole('button', { name: '清空回收站' })
      await user.click(trigger)
      await clickEmptyTrashConfirm(user)

      await waitFor(() => {
        expect(screen.queryByText('确定要清空回收站吗？')).toBeNull()
        expect(trigger).toHaveFocus()
      })
      expect(screen.getByText(addedAfterOpen.name)).toBeTruthy()
    })

    it('clears the frozen intent on cancel and captures a new snapshot when reopened', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
      })

      render(
        <QueryClientProvider client={queryClient}>
          <TrashPage />
        </QueryClientProvider>,
      )

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

      act(() => {
        queryClient.setQueryData(['trash', 'u1'], {
          items: [
            trashTestItem('item1', 'deleted-file.txt'),
            trashTestItem('item2', 'deleted-folder', { isDir: true, size: 0 }),
            trashTestItem('item3', 'new-after-cancel.txt', { size: 2048 }),
          ],
          count: 3,
          totalSize: 3072,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
      })

      await user.click(screen.getByText('清空回收站'))
      expect(await screen.findByText('将永久删除 3 项，共 3 KB。')).toBeTruthy()
    })

    it('freezes item ids and excludes cache additions from confirmation and request', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
      })
      mockEmptyTrash.mockResolvedValue(emptyTrashDeletedResult(['item1', 'item2']))
      mockListTrash.mockImplementationOnce(async () => ({
        items: [
          trashTestItem('item1', 'deleted-file.txt'),
          trashTestItem('item2', 'deleted-folder', { isDir: true, size: 0 }),
        ],
        count: 2,
        totalSize: 1024,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      })).mockResolvedValue({
        items: [trashTestItem('item3', 'new-after-open.txt', { size: 4096 })],
        count: 1,
        totalSize: 4096,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      })

      render(
        <QueryClientProvider client={queryClient}>
          <TrashPage />
        </QueryClientProvider>,
      )

      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })

      await user.click(screen.getByText('清空回收站'))
      expect(await screen.findByText('将永久删除 2 项，共 1 KB。')).toBeTruthy()

      act(() => {
        queryClient.setQueryData(['trash', 'u1'], {
          items: [
            trashTestItem('item1', 'deleted-file.txt'),
            trashTestItem('item2', 'deleted-folder', { isDir: true, size: 0 }),
            trashTestItem('item3', 'new-after-open.txt', { size: 4096 }),
          ],
          count: 3,
          totalSize: 5120,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
      })

      expect(screen.getByText('将永久删除 2 项，共 1 KB。')).toBeTruthy()
      expect(screen.queryByText('将永久删除 3 项，共 5 KB。')).toBeNull()

      await clickEmptyTrashConfirm(user)

      await waitFor(() => {
        expect(mockEmptyTrash).toHaveBeenCalledTimes(1)
      })
      expect(mockEmptyTrash.mock.calls[0]?.[0]).toEqual(['item1', 'item2'])
      expect(mockEmptyTrash.mock.calls[0]?.[0]).not.toContain('item3')
      expect(mockEmptyTrash.mock.calls[0]?.[1]?.signal).toBeInstanceOf(AbortSignal)

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '已永久删除已确认的 2 项', color: 'success' })
      })
      expect(screen.getByText('new-after-open.txt')).toBeTruthy()
      expect(mockListTrash).toHaveBeenCalledTimes(2)

      await user.click(screen.getByText('清空回收站'))
      expect(await screen.findByText('将永久删除 1 项，共 4 KB。')).toBeTruthy()
    })

    it('keeps cached items visible while requests and the final refetch are pending', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const pendingEmpty = createDeferred<Awaited<ReturnType<typeof emptyTrash>>>()
      mockEmptyTrash.mockImplementationOnce(() => pendingEmpty.promise)

      render(<TrashPage />)
      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
        expect(screen.getByText('deleted-folder')).toBeTruthy()
      })
      await user.click(screen.getByText('清空回收站'))
      await clickEmptyTrashConfirm(user)
      await waitFor(() => {
        expect(mockEmptyTrash).toHaveBeenCalledTimes(1)
      })
      expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      expect(screen.getByText('deleted-folder')).toBeTruthy()

      const dialog = getModalContent('trash-empty-dialog-title')
      expect(dialog).toHaveAttribute('aria-busy', 'true')
      await user.click(within(dialog).getByRole('button', { name: '取消' }))
      expect(screen.getByText('确定要清空回收站吗？')).toBeTruthy()

      mockListTrash.mockImplementation(() => pendingTrashRefetch())
      await act(async () => {
        pendingEmpty.resolve(emptyTrashDeletedResult(['item1', 'item2']))
      })

      await waitFor(() => expect(mockListTrash).toHaveBeenCalledTimes(2))
      expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      expect(screen.getByText('deleted-folder')).toBeTruthy()
      expect(screen.queryByText('回收站是空的')).toBeNull()
    })

    it('chunks more than 1000 frozen ids without an intermediate refetch', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const frozenItems = Array.from({ length: 1001 }, (_, index) => (
        trashTestItem(`item-${index + 1}`, `item-${index + 1}.txt`, { size: 1 })
      ))
      mockListTrash.mockResolvedValueOnce({
        items: frozenItems,
        count: frozenItems.length,
        totalSize: frozenItems.length,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      }).mockResolvedValue({
        items: [],
        count: 0,
        totalSize: 0,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      })
      mockEmptyTrash
        .mockImplementationOnce(async (ids) => ({
          deleted: ids.slice(0, 999),
          remaining: [],
          skipped: [ids[999]!],
          partial: true,
          warning: false,
          auditWarning: true,
        }))
        .mockImplementationOnce(async (ids) => {
          expect(mockListTrash).toHaveBeenCalledTimes(1)
          return emptyTrashDeletedResult(ids)
        })

      render(<TrashPage />)
      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })
      await user.click(screen.getByText('清空回收站'))
      await clickEmptyTrashConfirm(user)

      await waitFor(() => {
        expect(mockEmptyTrash).toHaveBeenCalledTimes(2)
      })
      expect(mockEmptyTrash.mock.calls[0]?.[0]).toEqual(frozenItems.slice(0, 1000).map((item) => item.id))
      expect(mockEmptyTrash.mock.calls[1]?.[0]).toEqual([frozenItems[1000]!.id])
      expect(mockEmptyTrash.mock.calls[0]?.[1]?.signal).toBe(mockEmptyTrash.mock.calls[1]?.[1]?.signal)
      await waitFor(() => expect(mockListTrash).toHaveBeenCalledTimes(2))
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '已永久删除已确认的 1000 项',
        description: '已跳过 1 项；操作记录未保存。',
        color: 'warning',
      })
    }, 20_000)

    it('preserves committed chunks and retries only the frozen remainder after a later request fails', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
      })
      const frozenItems = Array.from({ length: 1001 }, (_, index) => (
        trashTestItem(`item-${index + 1}`, `item-${index + 1}.txt`, { size: 1 })
      ))
      const firstChunkIds = frozenItems.slice(0, 1000).map((item) => item.id)
      const remainingItem = frozenItems[1000]!
      const addedAfterOpen = trashTestItem('new-after-open', 'new-after-open.txt', { size: 4096 })
      mockListTrash
        .mockResolvedValueOnce({
          items: frozenItems,
          count: frozenItems.length,
          totalSize: frozenItems.length,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
        .mockResolvedValueOnce({
          items: [remainingItem, addedAfterOpen],
          count: 2,
          totalSize: remainingItem.size + addedAfterOpen.size,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
        .mockResolvedValue({
          items: [addedAfterOpen],
          count: 1,
          totalSize: addedAfterOpen.size,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
      mockEmptyTrash
        .mockImplementationOnce(async (ids) => emptyTrashDeletedResult(ids))
        .mockRejectedValueOnce(new TypeError('Failed to fetch'))
        .mockImplementationOnce(async (ids) => emptyTrashDeletedResult(ids))

      render(
        <QueryClientProvider client={queryClient}>
          <TrashPage />
        </QueryClientProvider>,
      )
      await waitFor(() => expect(screen.getByText('清空回收站')).toBeTruthy())
      await user.click(screen.getByText('清空回收站'))
      expect(await screen.findByText('将永久删除 1001 项，共 1001 B。')).toBeTruthy()

      act(() => {
        queryClient.setQueryData(['trash', 'u1'], {
          items: [...frozenItems, addedAfterOpen],
          count: frozenItems.length + 1,
          totalSize: frozenItems.length + addedAfterOpen.size,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
      })

      await clickEmptyTrashConfirm(user)

      await waitFor(() => expect(mockEmptyTrash).toHaveBeenCalledTimes(2))
      expect(mockEmptyTrash.mock.calls[0]?.[0]).toEqual(firstChunkIds)
      expect(mockEmptyTrash.mock.calls[1]?.[0]).toEqual([remainingItem.id])
      await waitFor(() => expect(mockListTrash).toHaveBeenCalledTimes(2))
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已永久删除已确认的 1000 项',
        color: 'warning',
      }))
      expect(screen.queryByText('item-1.txt')).toBeNull()
      expect(screen.getByText(remainingItem.name)).toBeTruthy()
      expect(screen.getByText(addedAfterOpen.name)).toBeTruthy()
      expect(screen.getByText('将永久删除 1 项，共 1 B。')).toBeTruthy()

      await clickEmptyTrashConfirm(user)

      await waitFor(() => expect(mockEmptyTrash).toHaveBeenCalledTimes(3))
      expect(mockEmptyTrash.mock.calls[2]?.[0]).toEqual([remainingItem.id])
      expect(mockEmptyTrash.mock.calls[2]?.[0]).not.toContain(addedAfterOpen.id)
    }, 20_000)

    it('requires a successful reconciliation before retrying unknown and unattempted frozen items', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
      })
      const frozenItems = Array.from({ length: 2001 }, (_, index) => (
        trashTestItem(`item-${index + 1}`, `item-${index + 1}.txt`, { size: 1 })
      ))
      const firstChunkIds = frozenItems.slice(0, 1000).map((item) => item.id)
      const failedChunkIds = frozenItems.slice(1000, 2000).map((item) => item.id)
      const unknownItem = frozenItems[1000]!
      const unattemptedItem = frozenItems[2000]!
      const addedAfterOpen = trashTestItem('new-after-open', 'new-after-open.txt', { size: 4096 })
      mockListTrash
        .mockResolvedValueOnce({
          items: frozenItems,
          count: frozenItems.length,
          totalSize: frozenItems.length,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
        .mockRejectedValueOnce(new TypeError('Failed to refresh'))
        .mockRejectedValueOnce(new TypeError('Still offline'))
        .mockResolvedValueOnce({
          items: [unknownItem, unattemptedItem, addedAfterOpen],
          count: 3,
          totalSize: unknownItem.size + unattemptedItem.size + addedAfterOpen.size,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
        .mockResolvedValue({
          items: [addedAfterOpen],
          count: 1,
          totalSize: addedAfterOpen.size,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
      mockEmptyTrash
        .mockResolvedValueOnce({
          deleted: firstChunkIds.slice(0, 999),
          remaining: [],
          skipped: [firstChunkIds[999]!],
          partial: true,
          warning: false,
          auditWarning: false,
        })
        .mockRejectedValueOnce(new TypeError('Failed to fetch'))
        .mockImplementationOnce(async (ids) => emptyTrashDeletedResult(ids))

      render(
        <QueryClientProvider client={queryClient}>
          <TrashPage />
        </QueryClientProvider>,
      )
      await waitFor(() => expect(screen.getByText('清空回收站')).toBeTruthy())
      await user.click(screen.getByText('清空回收站'))

      act(() => {
        queryClient.setQueryData(['trash', 'u1'], {
          items: [...frozenItems, addedAfterOpen],
          count: frozenItems.length + 1,
          totalSize: frozenItems.length + addedAfterOpen.size,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
      })

      await clickEmptyTrashConfirm(user)

      await waitFor(() => expect(mockEmptyTrash).toHaveBeenCalledTimes(2))
      expect(mockEmptyTrash.mock.calls[0]?.[0]).toEqual(firstChunkIds)
      expect(mockEmptyTrash.mock.calls[1]?.[0]).toEqual(failedChunkIds)
      await waitFor(() => expect(mockListTrash).toHaveBeenCalledTimes(2))
      expect(screen.queryByText(frozenItems[0]!.name)).toBeNull()
      expect(screen.queryByText(frozenItems[999]!.name)).toBeNull()
      expect(screen.getByText(unknownItem.name)).toBeTruthy()
      expect(screen.getByText(unattemptedItem.name)).toBeTruthy()
      expect(screen.getByText(addedAfterOpen.name)).toBeTruthy()
      expect(screen.getByText('1001 项仍需通过最新回收站列表确认状态。')).toBeTruthy()
      expect(screen.queryByRole('button', { name: '确认清空回收站' })).toBeNull()
      expect(screen.getByRole('button', { name: '重新核对清空范围' })).toBeEnabled()

      await user.click(screen.getByRole('button', { name: '重新核对清空范围' }))

      await waitFor(() => expect(mockListTrash).toHaveBeenCalledTimes(3))
      expect(mockEmptyTrash).toHaveBeenCalledTimes(2)
      expect(screen.queryByRole('button', { name: '确认清空回收站' })).toBeNull()
      expect(screen.getByRole('button', { name: '重新核对清空范围' })).toBeEnabled()

      await user.click(screen.getByRole('button', { name: '重新核对清空范围' }))

      await waitFor(() => expect(mockListTrash).toHaveBeenCalledTimes(4))
      expect(mockEmptyTrash).toHaveBeenCalledTimes(2)
      expect(await screen.findByText('将永久删除 2 项，共 2 B。')).toBeTruthy()
      expect(screen.queryByRole('button', { name: '重新核对清空范围' })).toBeNull()
      expect(getEmptyTrashConfirmButton()).toBeEnabled()

      await clickEmptyTrashConfirm(user)

      await waitFor(() => expect(mockEmptyTrash).toHaveBeenCalledTimes(3))
      expect(mockEmptyTrash.mock.calls[2]?.[0]).toEqual([unknownItem.id, unattemptedItem.id])
      expect(mockEmptyTrash.mock.calls[2]?.[0]).not.toContain(addedAfterOpen.id)
    }, 30_000)

    it('closes without another delete when reconciliation finds the frozen range absent', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const addedAfterOpen = trashTestItem('item3', 'new-after-open.txt')
      mockEmptyTrash.mockRejectedValueOnce(new TypeError('Failed to fetch'))
      mockListTrash
        .mockResolvedValueOnce({
          items: [
            trashTestItem('item1', 'deleted-file.txt'),
            trashTestItem('item2', 'deleted-folder', { isDir: true, size: 0 }),
          ],
          count: 2,
          totalSize: 1024,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
        .mockRejectedValueOnce(new TypeError('Failed to refresh'))
        .mockResolvedValueOnce({
          items: [addedAfterOpen],
          count: 1,
          totalSize: addedAfterOpen.size,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })

      render(<TrashPage />)
      await user.click(await screen.findByRole('button', { name: '清空回收站' }))
      await clickEmptyTrashConfirm(user)
      await user.click(await screen.findByRole('button', { name: '重新核对清空范围' }))

      await waitFor(() => {
        expect(screen.queryByText('上次清空请求的最终结果尚未核对。')).toBeNull()
        expect(screen.getByText(addedAfterOpen.name)).toBeTruthy()
      })
      expect(mockEmptyTrash).toHaveBeenCalledTimes(1)
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '清空范围已核对',
        description: '本次确认范围内的项目均已不在回收站。',
        color: 'success',
      })
    })

    it('keeps reconciliation busy, non-dismissible, and single-flight until refresh completes', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const pendingReconciliation = createDeferred<Awaited<ReturnType<typeof listTrash>>>()
      mockEmptyTrash.mockRejectedValueOnce(new TypeError('Failed to fetch'))
      mockListTrash
        .mockResolvedValueOnce({
          items: [
            trashTestItem('item1', 'deleted-file.txt'),
            trashTestItem('item2', 'deleted-folder', { isDir: true, size: 0 }),
          ],
          count: 2,
          totalSize: 1024,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
        .mockRejectedValueOnce(new TypeError('Failed to refresh'))
        .mockImplementationOnce(() => pendingReconciliation.promise)

      render(<TrashPage />)
      await user.click(await screen.findByRole('button', { name: '清空回收站' }))
      await clickEmptyTrashConfirm(user)
      const reconcileButton = await screen.findByRole('button', { name: '重新核对清空范围' })
      await user.click(reconcileButton)

      const dialog = getModalContent('trash-empty-dialog-title')
      await waitFor(() => {
        expect(mockListTrash).toHaveBeenCalledTimes(3)
        expect(dialog).toHaveAttribute('aria-busy', 'true')
        expect(reconcileButton).toBeDisabled()
        expect(within(dialog).getByRole('button', { name: '关闭' })).toBeDisabled()
      })
      await user.click(reconcileButton)
      await user.keyboard('{Escape}')
      expect(mockListTrash).toHaveBeenCalledTimes(3)
      expect(screen.getByText('上次清空请求的最终结果尚未核对。')).toBeTruthy()

      await act(async () => {
        pendingReconciliation.resolve({
          items: [trashTestItem('item1', 'deleted-file.txt')],
          count: 1,
          totalSize: 1024,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
        await pendingReconciliation.promise
      })

      await waitFor(() => {
        expect(dialog).toHaveAttribute('aria-busy', 'false')
        expect(screen.queryByRole('button', { name: '重新核对清空范围' })).toBeNull()
        expect(screen.getByRole('button', { name: '确认清空回收站' })).toBeEnabled()
      })
      expect(mockEmptyTrash).toHaveBeenCalledTimes(1)
    })

    it('stops after a remaining result and classifies unsent frozen ids as remaining', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
      })
      const frozenItems = Array.from({ length: 1001 }, (_, index) => (
        trashTestItem(`item-${index + 1}`, `item-${index + 1}.txt`, { size: 1 })
      ))
      const firstChunkIds = frozenItems.slice(0, 1000).map((item) => item.id)
      const remainingItems = frozenItems.slice(999)
      const addedAfterOpen = trashTestItem('new-after-open', 'new-after-open.txt', { size: 4096 })
      mockListTrash
        .mockResolvedValueOnce({
          items: frozenItems,
          count: frozenItems.length,
          totalSize: frozenItems.length,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
        .mockResolvedValueOnce({
          items: [...remainingItems, addedAfterOpen],
          count: 3,
          totalSize: 2 + addedAfterOpen.size,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
        .mockResolvedValue({
          items: [addedAfterOpen],
          count: 1,
          totalSize: addedAfterOpen.size,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
      mockEmptyTrash
        .mockResolvedValueOnce({
          deleted: firstChunkIds.slice(0, 999),
          remaining: [firstChunkIds[999]!],
          skipped: [],
          partial: true,
          warning: false,
          auditWarning: false,
        })
        .mockImplementationOnce(async (ids) => emptyTrashDeletedResult(ids))

      render(
        <QueryClientProvider client={queryClient}>
          <TrashPage />
        </QueryClientProvider>,
      )
      await waitFor(() => expect(screen.getByText('清空回收站')).toBeTruthy())
      await user.click(screen.getByText('清空回收站'))

      act(() => {
        queryClient.setQueryData(['trash', 'u1'], {
          items: [...frozenItems, addedAfterOpen],
          count: frozenItems.length + 1,
          totalSize: frozenItems.length + addedAfterOpen.size,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
      })

      await clickEmptyTrashConfirm(user)

      await waitFor(() => expect(mockEmptyTrash).toHaveBeenCalledTimes(1))
      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '已永久删除已确认的 999 项',
          description: '仍有 2 项保留在回收站。',
          color: 'warning',
        })
      })
      expect(mockEmptyTrash.mock.calls[0]?.[0]).toEqual(firstChunkIds)
      expect(screen.getByText('将永久删除 2 项，共 2 B。')).toBeTruthy()
      expect(screen.getByText(remainingItems[0]!.name)).toBeTruthy()
      expect(screen.getByText(remainingItems[1]!.name)).toBeTruthy()
      expect(screen.getByText(addedAfterOpen.name)).toBeTruthy()

      await clickEmptyTrashConfirm(user)

      await waitFor(() => expect(mockEmptyTrash).toHaveBeenCalledTimes(2))
      expect(mockEmptyTrash.mock.calls[1]?.[0]).toEqual(remainingItems.map((item) => item.id))
      expect(mockEmptyTrash.mock.calls[1]?.[0]).not.toContain(addedAfterOpen.id)
    }, 20_000)

    it('aggregates partial and warning results into one warning toast', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockEmptyTrash.mockResolvedValue({
        deleted: ['item1'],
        remaining: ['item2'],
        skipped: [],
        partial: true,
        warning: true,
        auditWarning: false,
        message: 'cleanup warning',
      })

      render(<TrashPage />)
      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })
      await user.click(screen.getByText('清空回收站'))
      await clickEmptyTrashConfirm(user)

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '已永久删除已确认的 1 项',
          description: '仍有 1 项保留在回收站；删除过程报告了清理警告。',
          color: 'warning',
        })
      })
    })

    it('shows audit persistence warnings separately from cleanup warnings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockEmptyTrash.mockResolvedValue({
        deleted: ['item1', 'item2'],
        remaining: [],
        skipped: [],
        partial: false,
        warning: false,
        auditWarning: true,
      })

      render(<TrashPage />)
      await waitFor(() => expect(screen.getByText('清空回收站')).toBeTruthy())
      await user.click(screen.getByText('清空回收站'))
      await clickEmptyTrashConfirm(user)

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '已永久删除已确认的 2 项',
          description: '操作记录未保存。',
          color: 'warning',
        })
      })
    })

    it('retains the frozen intent after failure and retries the same ids', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
      })
      mockEmptyTrash
        .mockRejectedValueOnce(new ApiError('temporary failure', 500, 'Internal Server Error'))
        .mockResolvedValueOnce(emptyTrashDeletedResult(['item1', 'item2']))

      render(
        <QueryClientProvider client={queryClient}>
          <TrashPage />
        </QueryClientProvider>,
      )
      await waitFor(() => expect(screen.getByText('清空回收站')).toBeTruthy())
      await user.click(screen.getByText('清空回收站'))

      act(() => {
        queryClient.setQueryData(['trash', 'u1'], {
          items: [
            trashTestItem('item1', 'deleted-file.txt'),
            trashTestItem('item2', 'deleted-folder', { isDir: true, size: 0 }),
            trashTestItem('item3', 'new-during-retry.txt'),
          ],
          count: 3,
          totalSize: 2048,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
      })

      await clickEmptyTrashConfirm(user)
      await waitFor(() => expect(mockEmptyTrash).toHaveBeenCalledTimes(1))
      const failureToast = mockAddToast.mock.calls
        .map(([toast]) => toast as { description?: string })
        .find((toast) => toast.description?.includes('清空请求未完成'))
      expect(failureToast?.description).toContain('清空请求未完成')
      expect(failureToast?.description).not.toContain('后续批次未完成')
      expect(screen.getByText('确定要清空回收站吗？')).toBeTruthy()
      expect(screen.getByText('将永久删除 2 项，共 1 KB。')).toBeTruthy()

      await clickEmptyTrashConfirm(user)
      await waitFor(() => expect(mockEmptyTrash).toHaveBeenCalledTimes(2))
      expect(mockEmptyTrash.mock.calls[0]?.[0]).toEqual(['item1', 'item2'])
      expect(mockEmptyTrash.mock.calls[1]?.[0]).toEqual(['item1', 'item2'])
      expect(mockEmptyTrash.mock.calls[1]?.[0]).not.toContain('item3')
      await waitFor(() => expect(screen.queryByText('确定要清空回收站吗？')).toBeNull())
    })

    it('removes confirmed cached items and preserves concurrent additions when the final refetch fails', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 60_000 } },
      })
      const addedAfterOpen = trashTestItem('item3', 'new-after-open.txt', { size: 4096 })
      mockEmptyTrash.mockResolvedValue(emptyTrashDeletedResult(['item1'], { skipped: ['item2'] }))

      render(
        <QueryClientProvider client={queryClient}>
          <TrashPage />
        </QueryClientProvider>,
      )
      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
        expect(screen.getByText('deleted-folder')).toBeTruthy()
      })

      await user.click(screen.getByText('清空回收站'))

      act(() => {
        queryClient.setQueryData(['trash', 'u1'], {
          items: [
            trashTestItem('item1', 'deleted-file.txt'),
            trashTestItem('item2', 'deleted-folder', { isDir: true, size: 0 }),
            addedAfterOpen,
          ],
          count: 3,
          totalSize: 5120,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
      })
      mockListTrash.mockRejectedValue(new Error('refetch failed'))

      await clickEmptyTrashConfirm(user)

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '已永久删除已确认的 1 项',
          description: '已跳过 1 项；删除结果已返回，但列表刷新失败。',
          color: 'warning',
        })
      })
      expect(mockEmptyTrash.mock.calls[0]?.[0]).toEqual(['item1', 'item2'])
      expect(mockEmptyTrash.mock.calls[0]?.[0]).not.toContain(addedAfterOpen.id)
      expect(screen.queryByText('deleted-file.txt')).toBeNull()
      expect(screen.queryByText('deleted-folder')).toBeNull()
      expect(screen.getByText(addedAfterOpen.name)).toBeTruthy()
      expect(screen.queryByText('回收站是空的')).toBeNull()
      expect(queryClient.getQueryState(['trash', 'u1'])?.isInvalidated).toBe(true)
    })
  })

  describe('request cancellation', () => {
    it('aborts a pending restore request when the page unmounts', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const pendingRestore = createDeferred<Awaited<ReturnType<typeof restoreFromTrash>>>()
      let signal: AbortSignal | undefined
      mockRestoreFromTrash.mockImplementationOnce((_id, _newPath, options) => {
        signal = options?.signal
        return pendingRestore.promise
      })

      const { unmount } = render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '恢复 deleted-file.txt' }))

      await waitFor(() => {
        expectAbortSignal(signal)
      })
      expect(signal.aborted).toBe(false)

      unmount()

      expect(signal.aborted).toBe(true)
    })

    it('aborts a pending permanent delete request when the page unmounts', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const pendingDelete = createDeferred<Awaited<ReturnType<typeof deleteFromTrash>>>()
      let signal: AbortSignal | undefined
      mockDeleteFromTrash.mockImplementationOnce((_id, options) => {
        signal = options?.signal
        return pendingDelete.promise
      })

      const { unmount } = render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '永久删除 deleted-file.txt' }))
      await waitFor(() => {
        expect(screen.getByRole('button', { name: '永久删除' })).toBeTruthy()
      })
      await user.click(screen.getByRole('button', { name: '永久删除' }))

      await waitFor(() => {
        expectAbortSignal(signal)
      })
      expect(signal.aborted).toBe(false)

      unmount()

      expect(signal.aborted).toBe(true)
    })

    it('aborts a pending empty trash request when the page unmounts', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 60_000, staleTime: 60_000 } },
      })
      let signal: AbortSignal | undefined
      mockEmptyTrash.mockImplementationOnce((_ids, options) => {
        signal = options?.signal
        return new Promise<Awaited<ReturnType<typeof emptyTrash>>>((_resolve, reject) => {
          signal?.addEventListener('abort', () => {
            reject(new DOMException('Aborted', 'AbortError'))
          }, { once: true })
        })
      })

      const { unmount } = render(
        <QueryClientProvider client={queryClient}>
          <TrashPage />
        </QueryClientProvider>,
      )

      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })

      await user.click(screen.getByText('清空回收站'))
      await waitFor(() => {
        expect(screen.getByText('确定要清空回收站吗？')).toBeTruthy()
      })
      await clickEmptyTrashConfirm(user)

      await waitFor(() => {
        expectAbortSignal(signal)
      })
      expect(signal.aborted).toBe(false)

      unmount()

      expect(signal.aborted).toBe(true)
      await waitFor(() => {
        expect(queryClient.getQueryState(['trash', 'u1'])?.isInvalidated).toBe(true)
      })
      expect(mockAddToast).not.toHaveBeenCalled()
    })

    it('preserves committed empty chunks in cache when a later chunk aborts on unmount', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 60_000, staleTime: 60_000 } },
      })
      const frozenItems = Array.from({ length: 1001 }, (_, index) => (
        trashTestItem(`item-${index + 1}`, `item-${index + 1}.txt`, { size: 1 })
      ))
      let secondSignal: AbortSignal | undefined
      mockListTrash.mockResolvedValueOnce({
        items: frozenItems,
        count: frozenItems.length,
        totalSize: frozenItems.length,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      })
      mockEmptyTrash
        .mockImplementationOnce(async (ids) => emptyTrashDeletedResult(ids))
        .mockImplementationOnce((_ids, options) => {
          secondSignal = options?.signal
          return new Promise<Awaited<ReturnType<typeof emptyTrash>>>((_resolve, reject) => {
            secondSignal?.addEventListener('abort', () => {
              reject(new DOMException('Aborted', 'AbortError'))
            }, { once: true })
          })
        })

      const { unmount } = render(
        <QueryClientProvider client={queryClient}>
          <TrashPage />
        </QueryClientProvider>,
      )
      await user.click(await screen.findByRole('button', { name: '清空回收站' }))
      await clickEmptyTrashConfirm(user)
      await waitFor(() => {
        expect(mockEmptyTrash).toHaveBeenCalledTimes(2)
        expectAbortSignal(secondSignal)
      })

      unmount()
      expect(secondSignal?.aborted).toBe(true)

      await waitFor(() => {
        const cached = queryClient.getQueryData<{ items: ReturnType<typeof trashTestItem>[] }>(['trash', 'u1'])
        expect(cached?.items.map((item) => item.id)).toEqual([frozenItems[1000]!.id])
        expect(queryClient.getQueryState(['trash', 'u1'])?.isInvalidated).toBe(true)
      })
      expect(mockAddToast).not.toHaveBeenCalled()
    }, 20_000)

    it('preserves a completed empty result when unmount aborts its final refetch', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 60_000, staleTime: 60_000 } },
      })
      let refetchSignal: AbortSignal | undefined
      mockEmptyTrash.mockResolvedValueOnce(emptyTrashDeletedResult(['item1', 'item2']))
      mockListTrash
        .mockResolvedValueOnce({
          items: [
            trashTestItem('item1', 'deleted-file.txt'),
            trashTestItem('item2', 'deleted-folder', { isDir: true, size: 0 }),
          ],
          count: 2,
          totalSize: 1024,
          retentionEnabled: true,
          retentionDays: 30,
          trashAutoCleanupEnabled: true,
        })
        .mockImplementationOnce((options) => {
          refetchSignal = options?.signal
          return new Promise<Awaited<ReturnType<typeof listTrash>>>((_resolve, reject) => {
            refetchSignal?.addEventListener('abort', () => {
              reject(new DOMException('Aborted', 'AbortError'))
            }, { once: true })
          })
        })

      const { unmount } = render(
        <QueryClientProvider client={queryClient}>
          <TrashPage />
        </QueryClientProvider>,
      )
      await user.click(await screen.findByRole('button', { name: '清空回收站' }))
      await clickEmptyTrashConfirm(user)
      await waitFor(() => {
        expect(mockListTrash).toHaveBeenCalledTimes(2)
        expectAbortSignal(refetchSignal)
      })

      unmount()
      expect(refetchSignal?.aborted).toBe(true)

      await waitFor(() => {
        const cached = queryClient.getQueryData<{ items: ReturnType<typeof trashTestItem>[] }>(['trash', 'u1'])
        expect(cached?.items).toEqual([])
        expect(queryClient.getQueryState(['trash', 'u1'])?.isInvalidated).toBe(true)
      })
      expect(mockAddToast).not.toHaveBeenCalled()
    })

    it('aborts pending batch restore requests when the page unmounts', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUseRealBatchOperation = true
      const pendingRestore = createDeferred<Awaited<ReturnType<typeof restoreFromTrash>>>()
      let signal: AbortSignal | undefined
      mockRestoreFromTrash.mockImplementation((_id, _newPath, options) => {
        signal = options?.signal
        return pendingRestore.promise
      })

      const { unmount } = render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await toggleAllTrashItems(user)

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await clickBatchRestoreConfirm(user)

      await waitFor(() => {
        expectAbortSignal(signal)
      })
      expect(signal.aborted).toBe(false)

      unmount()

      expect(signal.aborted).toBe(true)
    })

    it('aborts pending batch permanent delete requests when the page unmounts', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUseRealBatchOperation = true
      const pendingDelete = createDeferred<Awaited<ReturnType<typeof deleteFromTrash>>>()
      let signal: AbortSignal | undefined
      mockDeleteFromTrash.mockImplementation((_id, options) => {
        signal = options?.signal
        return pendingDelete.promise
      })

      const { unmount } = render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await selectTrashItem(user, 'deleted-file.txt')

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
        expectAbortSignal(signal)
      })
      expect(signal.aborted).toBe(false)

      unmount()

      expect(signal.aborted).toBe(true)
    })
  })

  describe('selection', () => {
    it('shows selection bar when items selected', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await selectTrashItem(user, 'deleted-file.txt')

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

      await toggleAllTrashItems(user)

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

      await toggleAllTrashItems(user)

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await toggleAllTrashItems(user)

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

      await selectTrashItem(user, 'deleted-file.txt')

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

      await selectTrashItem(user, 'deleted-file.txt')

      await waitFor(() => {
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })

      await selectTrashItem(user, 'deleted-file.txt')

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
            expiresAt: futureTrashExpiry(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
          {
            id: 'item2',
            originalPath: '/deleted-folder',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(),
            expiresAt: futureTrashExpiry(),
            name: 'deleted-folder',
            isDir: true,
            size: 0,
          },
        ],
        count: 2,
        totalSize: 1024,
        trashAutoCleanupEnabled: false,
      })
      mockListTrash.mockImplementation(() => pendingTrashRefetch())

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await toggleAllTrashItems(user)

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '恢复 deleted-file.txt' }))

      await waitFor(() => {
        expect(mockRestoreFromTrash).toHaveBeenCalledWith('item1', undefined, expect.objectContaining({ signal: expect.any(AbortSignal) }))
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

      await selectTrashItem(user, 'deleted-file.txt')

      await waitFor(() => {
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })

      await clickBatchRestoreConfirm(user)

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

      await toggleAllTrashItems(user)

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await clickBatchRestoreConfirm(user)

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '已恢复 2 项，但存在警告',
          color: 'warning',
        })
      })
    })

    it('shows operation-specific warning toast for successful batch permanent delete with warnings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUseRealBatchOperation = true
      mockDeleteFromTrash.mockResolvedValue({
        warning: true,
        message: 'delete completed with cleanup warning',
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await selectTrashItem(user, 'deleted-file.txt')

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
          title: '已永久删除 1 项，但存在警告',
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

      await toggleAllTrashItems(user)

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await clickBatchRestoreConfirm(user)

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

      await toggleAllTrashItems(user)

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await clickBatchRestoreConfirm(user)

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '2 项恢复失败',
          color: 'danger',
        })
      })
    })

    it('shows quota guidance when all batch restore items exceed quota', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUseRealBatchOperation = true
      mockRestoreFromTrash.mockRejectedValue(new ApiError('user quota exceeded', 507, 'Insufficient Storage', 'QUOTA_EXCEEDED'))

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await toggleAllTrashItems(user)

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await clickBatchRestoreConfirm(user)

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '容量配额不足',
          description: '当前用户的容量配额不足，请清理空间或调整用户配额后重试。',
          color: 'warning',
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

      await selectTrashItem(user, 'deleted-file.txt')

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

    it('confirms before batch restore and shows cross-directory review', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await toggleAllTrashItems(user)

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await openBatchRestoreReview(user)

      const review = within(screen.getByLabelText('跨目录恢复执行前复核'))
      expect(review.getByText('跨目录恢复复核')).toBeTruthy()
      expect(review.getByText('2 项 · 1 个目录 · 1 个文件')).toBeTruthy()
      expect(review.getByText('1 个目标目录')).toBeTruthy()
      expect(review.getByText('1 KB')).toBeTruthy()
      expect(review.getByText('所选项目近期不会到期；容量不足时仍可能提前清理')).toBeTruthy()
      expect(review.getByText('若原路径已存在同名文件、父目录不可写或配额不足，服务端会拒绝对应项目并保留在回收站。')).toBeTruthy()
      expect(review.getByText('成功项目会从回收站移除；失败项目会保持选中，便于继续处理。')).toBeTruthy()
      expect(review.getByText('/')).toBeTruthy()
      expect(mockBatchExecute).not.toHaveBeenCalled()

      await user.click(screen.getByRole('button', { name: '取消' }))

      expect(screen.queryByText('确认批量恢复')).toBeNull()
      expect(mockBatchExecute).not.toHaveBeenCalled()
    })

    it('uses persisted item expiries in the batch restore review', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const now = Date.now()
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(now - 1000).toISOString(),
            expiresAt: new Date(now - 24 * 60 * 60 * 1000).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
          {
            id: 'item2',
            originalPath: '/deleted-folder',
            deletedAt: new Date(now - 1000).toISOString(),
            expiresAt: new Date(now + 20 * 24 * 60 * 60 * 1000).toISOString(),
            name: 'deleted-folder',
            isDir: true,
            size: 0,
          },
        ],
        count: 2,
        totalSize: 1024,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })
      await toggleAllTrashItems(user)
      await openBatchRestoreReview(user)

      const review = within(screen.getByLabelText('跨目录恢复执行前复核'))
      expect(review.getByText('1 项已过期并等待清理；容量不足时仍可能提前清理')).toBeTruthy()
    })

    it.each([
      {
        caseName: 'unknown automatic cleanup state',
        trashAutoCleanupEnabled: undefined,
        expiresAt: futureTrashExpiry(30),
        summary: '自动清理设置未知；容量不足时仍可能提前清理',
      },
      {
        caseName: 'approaching expiry',
        trashAutoCleanupEnabled: true,
        expiresAt: futureTrashExpiry(2),
        summary: '1 项接近到期时间；容量不足时仍可能提前清理',
      },
    ])('keeps capacity cleanup risk in the batch restore review for $caseName', async ({ trashAutoCleanupEnabled, expiresAt, summary }) => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(Date.now() - 1000).toISOString(),
            expiresAt,
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionDays: 30,
        trashAutoCleanupEnabled: trashAutoCleanupEnabled as boolean,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })
      await selectTrashItem(user, 'deleted-file.txt')
      await openBatchRestoreReview(user)

      const review = within(screen.getByLabelText('跨目录恢复执行前复核'))
      expect(review.getByText(summary)).toBeTruthy()
    })

    it('explains capacity cleanup without showing an expiry countdown when automatic cleanup is disabled', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const now = Date.now()
      mockListTrash.mockResolvedValue({
        items: [
          {
            id: 'item1',
            originalPath: '/deleted-file.txt',
            deletedAt: new Date(now - 1000).toISOString(),
            expiresAt: new Date(now + 2 * 24 * 60 * 60 * 1000).toISOString(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
        ],
        count: 1,
        totalSize: 1024,
        retentionDays: 7,
        trashAutoCleanupEnabled: false,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })
      await selectTrashItem(user, 'deleted-file.txt')
      await openBatchRestoreReview(user)

      const review = within(screen.getByLabelText('跨目录恢复执行前复核'))
      expect(review.getByText('按到期时间自动清理未启用；容量不足时仍可能提前清理')).toBeTruthy()
      expect(screen.queryByText('2 天后到期')).toBeNull()
    })

    it('copies the batch restore cross-directory review record', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await toggleAllTrashItems(user)

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await openBatchRestoreReview(user)
      await user.click(screen.getByRole('button', { name: '复制复核记录' }))

      await waitFor(() => {
        expect(writeText).toHaveBeenCalledTimes(1)
      })
      const report = String(writeText.mock.calls[0]?.[0] ?? '')
      expect(report).toContain('回收站批量恢复复核')
      expect(report).toContain('恢复项目：2 项（1 个目录，1 个文件）')
      expect(report).toContain('原始目录：1 个目标目录')
      expect(report).toContain('可见数据量：1 KB')
      expect(report).toContain('自动清理：所选项目近期不会到期；容量不足时仍可能提前清理')
      expect(report).toContain('冲突处理：若原路径已存在同名文件、父目录不可写或配额不足，服务端会拒绝对应项目并保留在回收站。')
      expect(report).toContain('执行结果：成功项目会从回收站移除；失败项目会保持选中，便于继续处理。')
      expect(report).toContain('- /')
      expect(mockAddToast).toHaveBeenCalledWith({ title: '批量恢复复核记录已复制', color: 'success' })
      expect(mockBatchExecute).not.toHaveBeenCalled()
    })

    it('records batch restore execution results into activity review history', async () => {
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
      mockListActivity.mockResolvedValueOnce({
        items: [
          {
            id: 'trash-restore-activity-1',
            timestamp: '2026-04-12T01:00:00Z',
            action: 'trash_restore',
            path: '/deleted-file.txt',
            user: 'admin',
          },
          {
            id: 'trash-restore-activity-2',
            timestamp: '2026-04-12T01:01:00Z',
            action: 'trash_restore',
            path: '/deleted-folder',
            user: 'admin',
          },
        ],
        total: 2,
        limit: 100,
        offset: 0,
      })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await toggleAllTrashItems(user)

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await clickBatchRestoreConfirm(user)

      await waitFor(() => {
        expect(mockCreateActivityReviewRecord).toHaveBeenCalledTimes(1)
      })
      expect(mockBatchExecute).toHaveBeenCalledWith(['item1', 'item2'])
      expect(mockListActivity).toHaveBeenCalledWith(expect.objectContaining({
        action: 'trash_restore',
        actionGroup: 'risk',
        limit: 100,
        offset: 0,
        signal: expect.any(AbortSignal),
      }))
      expect(mockCreateActivityReviewRecord).toHaveBeenCalledWith(expect.objectContaining({
        note: '回收站恢复执行结果：已恢复 2 项（1 个目录，1 个文件）；已关联 2 条恢复活动。',
        scope_label: '回收站',
        filter_summary: '审计分组 风险操作 · 执行结果 回收站恢复',
        disposition_status: 'restored',
        action_counts: { trash_restore: 2 },
        review_count: 2,
        total_count: 2,
        path_count: 2,
        user_count: 1,
        path_samples: ['/deleted-file.txt', '/deleted-folder'],
        user_samples: ['admin'],
        activity_entry_ids: ['trash-restore-activity-1', 'trash-restore-activity-2'],
      }), expect.objectContaining({
        signal: expect.any(AbortSignal),
      }))
      expect(mockAddToast).toHaveBeenCalledWith({ title: '恢复结果已记录', color: 'success' })
    })

    it('confirms before batch permanent delete', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await selectTrashItem(user, 'deleted-file.txt')

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

      await selectTrashItem(user, 'deleted-file.txt')

      await waitFor(() => {
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })

      await user.click(screen.getByText('永久删除'))

      await waitFor(() => {
        expect(screen.getByText('确认批量永久删除')).toBeTruthy()
      })

      const dialog = getModalContent('trash-batch-delete-dialog-title')
      expect(dialog).toHaveAttribute('aria-describedby', 'trash-batch-delete-dialog-description')
      expect(dialog).toHaveAttribute('aria-busy', 'false')
      expect(within(dialog).getByRole('button', { name: '取消' })).toHaveFocus()

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

      await toggleAllTrashItems(user)

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await clickBatchRestoreConfirm(user)

      await waitFor(() => {
        expect(mockBatchExecute).toHaveBeenCalledWith(['item1', 'item2'])
      })

      await waitFor(() => {
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })
    })

    it('includes conflict guidance after partial batch restore conflict', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockBatchResult = {
        succeeded: 1,
        failed: 1,
        total: 2,
        succeededItems: ['item1'],
        failedItems: ['item2'],
        failedErrors: [new ApiError('resource already exists', 409, 'Conflict')],
        warningCount: 0,
        warningMessages: [],
      }

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await toggleAllTrashItems(user)

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await clickBatchRestoreConfirm(user)

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '1 项恢复成功，1 项失败',
          description: '当前目录中已存在同名文件或文件夹，请使用其他名称。',
          color: 'warning',
        })
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

      await selectTrashItem(user, 'deleted-file.txt')

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
            expiresAt: futureTrashExpiry(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
          {
            id: 'item2',
            originalPath: '/deleted-folder',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(),
            expiresAt: futureTrashExpiry(),
            name: 'deleted-folder',
            isDir: true,
            size: 0,
          },
        ],
        count: 2,
        totalSize: 1024,
        trashAutoCleanupEnabled: false,
      })
      mockListTrash.mockImplementation(() => pendingTrashRefetch())

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await toggleAllTrashItems(user)

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

    it('keeps the batch restore review and execution bound to the targets captured when the dialog opens', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const initialResponse = {
        items: [
          trashTestItem('item1', 'restore-a.txt', { originalPath: '/original-a/restore-a.txt' }),
          trashTestItem('item2', 'restore-b.txt', { originalPath: '/original-b/restore-b.txt' }),
        ],
        count: 2,
        totalSize: 2048,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      }
      mockListTrash.mockResolvedValue(initialResponse)
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 } },
      })

      render(
        <QueryClientProvider client={queryClient}>
          <TrashPage />
        </QueryClientProvider>
      )

      await waitFor(() => {
        expect(screen.getByText('restore-a.txt')).toBeTruthy()
      })
      await selectTrashItem(user, 'restore-a.txt')
      await openBatchRestoreReview(user)

      const review = within(screen.getByLabelText('跨目录恢复执行前复核'))
      expect(review.getByText('/original-a')).toBeTruthy()

      act(() => {
        queryClient.setQueryData(['trash', 'u1'], {
          ...initialResponse,
          items: [initialResponse.items[1]],
          count: 1,
          totalSize: 1024,
        })
      })

      await waitFor(() => {
        expect(screen.queryByText('restore-a.txt')).toBeNull()
        expect(screen.getByText('restore-b.txt')).toBeTruthy()
      })
      expect(screen.getByText(/确定要恢复已选择的/).textContent).toContain('1 项')
      expect(review.getByText('/original-a')).toBeTruthy()
      expect(review.queryByText('/original-b')).toBeNull()

      await user.click(screen.getByRole('button', { name: '确认恢复' }))

      await waitFor(() => {
        expect(mockBatchExecute).toHaveBeenCalledWith(['item1'])
      })
    })

    it('keeps batch permanent delete bound to the targets captured when the dialog opens', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const initialResponse = {
        items: [
          trashTestItem('item1', 'delete-a.txt'),
          trashTestItem('item2', 'delete-b.txt'),
        ],
        count: 2,
        totalSize: 2048,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      }
      mockListTrash.mockResolvedValue(initialResponse)
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 } },
      })

      render(
        <QueryClientProvider client={queryClient}>
          <TrashPage />
        </QueryClientProvider>
      )

      await waitFor(() => {
        expect(screen.getByText('delete-a.txt')).toBeTruthy()
      })
      await selectTrashItem(user, 'delete-a.txt')
      await user.click(screen.getByRole('button', { name: '永久删除' }))

      await screen.findByText('确认批量永久删除')
      const dialog = getModalContent('trash-batch-delete-dialog-title')
      expect(dialog.textContent).toContain('已选择的 1 项')

      act(() => {
        queryClient.setQueryData(['trash', 'u1'], {
          ...initialResponse,
          items: [initialResponse.items[1]],
          count: 1,
          totalSize: 1024,
        })
      })

      await waitFor(() => {
        expect(screen.queryByText('delete-a.txt')).toBeNull()
        expect(screen.getByText('delete-b.txt')).toBeTruthy()
      })
      expect(dialog.textContent).toContain('已选择的 1 项')

      await user.click(within(dialog).getByRole('button', { name: '永久删除' }))

      await waitFor(() => {
        expect(mockBatchExecute).toHaveBeenCalledWith(['item1'])
      })
    })

    it('separates restored, missing, and failed items in a mixed batch restore', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUseRealBatchOperation = true
      mockRestoreFromTrash.mockImplementation(async (id: string) => {
        if (id === 'item2') {
          throw new ApiError('trash item not found', 404, 'Not Found', 'TRASH_NOT_FOUND')
        }
        if (id === 'item3') {
          throw new Error('restore failed')
        }
        return { warning: false }
      })
      mockListActivity.mockResolvedValueOnce({
        items: [
          {
            id: 'restore-actual',
            timestamp: '2026-04-12T01:00:00Z',
            action: 'trash_restore',
            path: '/restored.txt',
            user: 'admin',
          },
          {
            id: 'restore-missing',
            timestamp: '2026-04-12T01:01:00Z',
            action: 'trash_restore',
            path: '/missing.txt',
            user: 'admin',
          },
        ],
        total: 2,
        limit: 100,
        offset: 0,
      })
      mockListTrash.mockReset()
      mockListTrash.mockResolvedValueOnce({
        items: [
          trashTestItem('item1', 'restored.txt'),
          trashTestItem('item2', 'missing.txt'),
          trashTestItem('item3', 'restore-error.txt'),
        ],
        count: 3,
        totalSize: 3072,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      })
      mockListTrash.mockImplementation(() => pendingTrashRefetch())

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('restored.txt')).toBeTruthy()
      })
      await toggleAllTrashItems(user)
      await clickBatchRestoreConfirm(user)

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '1 项恢复成功，1 项失败',
          description: '已同步移除 1 个不存在回收站项目',
          color: 'warning',
        })
      })
      await waitFor(() => {
        expect(screen.queryByText('restored.txt')).toBeNull()
        expect(screen.queryByText('missing.txt')).toBeNull()
        expect(screen.getByText('restore-error.txt')).toBeTruthy()
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
      })
      expect(mockCreateActivityReviewRecord).toHaveBeenCalledWith(expect.objectContaining({
        path_samples: ['/restored.txt'],
        activity_entry_ids: ['restore-actual'],
      }), expect.objectContaining({ signal: expect.any(AbortSignal) }))
    })

    it('separates deleted, missing, and failed items in a mixed batch permanent delete', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUseRealBatchOperation = true
      mockDeleteFromTrash.mockImplementation(async (id: string) => {
        if (id === 'item2') {
          throw new ApiError('trash item not found', 404, 'Not Found', 'TRASH_NOT_FOUND')
        }
        if (id === 'item3') {
          throw new Error('delete failed')
        }
        return { warning: false }
      })
      mockListTrash.mockReset()
      mockListTrash.mockResolvedValueOnce({
        items: [
          trashTestItem('item1', 'deleted.txt'),
          trashTestItem('item2', 'missing.txt'),
          trashTestItem('item3', 'delete-error.txt'),
        ],
        count: 3,
        totalSize: 3072,
        retentionEnabled: true,
        retentionDays: 30,
        trashAutoCleanupEnabled: true,
      })
      mockListTrash.mockImplementation(() => pendingTrashRefetch())

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted.txt')).toBeTruthy()
      })
      await toggleAllTrashItems(user)
      await user.click(screen.getByText('永久删除'))
      await screen.findByText('确认批量永久删除')
      const dialog = getModalContent('trash-batch-delete-dialog-title')
      await user.click(within(dialog).getByRole('button', { name: '永久删除' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '1 项永久删除成功，1 项失败',
          description: '已同步移除 1 个不存在回收站项目',
          color: 'warning',
        })
      })
      await waitFor(() => {
        expect(screen.queryByText('deleted.txt')).toBeNull()
        expect(screen.queryByText('missing.txt')).toBeNull()
        expect(screen.getByText('delete-error.txt')).toBeTruthy()
        expect(screen.getByText(/已选择 1 项/)).toBeTruthy()
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
            expiresAt: futureTrashExpiry(),
            name: 'deleted-file.txt',
            isDir: false,
            size: 1024,
          },
          {
            id: 'item2',
            originalPath: '/deleted-folder',
            deletedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(),
            expiresAt: futureTrashExpiry(),
            name: 'deleted-folder',
            isDir: true,
            size: 0,
          },
        ],
        count: 2,
        totalSize: 1024,
        trashAutoCleanupEnabled: false,
      })
      mockListTrash.mockImplementation(() => pendingTrashRefetch())

      render(<TrashPage />)

      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      await toggleAllTrashItems(user)

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })

      await clickBatchRestoreConfirm(user)

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
