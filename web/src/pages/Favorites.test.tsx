import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor, userEvent } from '@/test/utils'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { FavoritesPage } from './Favorites'
import * as HeroUI from '@heroui/react'
import * as favoritesApi from '@/api/favorites'
import { FavoritesError } from '@/api/favorites'

const mockAddToast = vi.fn()
const mockNavigate = vi.fn()
const mockBatchExecute = vi.fn()
const useCanWriteMock = vi.fn(() => true)
const mockUser = { id: 'user-1', username: 'user1', role: 'user' as const, email: 'user1@local', homeDir: '/' }
let mockUseRealBatchOperation = false
let mockBatchResult = {
  succeeded: 1,
  failed: 0,
  total: 1,
  succeededItems: ['/docs/report.pdf'] as string[],
  failedItems: [] as string[],
  failedErrors: [] as unknown[],
  warningCount: 0,
  warningMessages: [] as string[],
}

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

vi.mock('@/api/favorites', async () => {
  const actual = await vi.importActual<typeof import('@/api/favorites')>('@/api/favorites')
  return {
    ...actual,
    listFavorites: vi.fn(),
    removeFavorite: vi.fn(),
    updateFavoriteNote: vi.fn(),
  }
})

vi.mock('@/lib/useBatchOperation', () => ({
  useBatchOperation: (options: {
    operation: (item: string, operationOptions?: { signal?: AbortSignal }) => Promise<unknown>
    messages: {
      success: string
      failure: string
      partial: string
    }
    getWarningMessage?: (result: unknown) => string | undefined
    getToast?: (result: typeof mockBatchResult) => { title: string; description?: string; color: 'success' | 'warning' | 'danger' } | null | undefined
    onComplete?: (result: typeof mockBatchResult) => void
  }) => ({
    execute: vi.fn(async (items: string[], operationOptions?: { signal?: AbortSignal }) => {
      if (mockUseRealBatchOperation) {
        const results = await Promise.allSettled(items.map((item) => options.operation(item, operationOptions)))
        if (operationOptions?.signal?.aborted) {
          return {
            succeeded: 0,
            failed: 0,
            total: items.length,
            succeededItems: [],
            failedItems: [],
            failedErrors: [],
            warningCount: 0,
            warningMessages: [],
          }
        }
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

const mockFavorites = [
  {
    path: '/docs/report.pdf',
    user_id: 'user-1',
    created_at: '2024-01-15T10:00:00Z',
    note: '月报',
  },
  {
    path: '/photos/',
    user_id: 'user-1',
    created_at: '2024-01-14T10:00:00Z',
  },
]

function expectCalledWithOnlyAbortSignal(mockFn: ReturnType<typeof vi.fn>) {
  const call = mockFn.mock.calls.find(([options]) => {
    return (options as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
  })
  expect(call).toBeTruthy()
  expect(Object.keys((call?.[0] ?? {}) as Record<string, unknown>).sort()).toEqual(['signal'])
}

function expectRemoveFavoriteCalledWithAbortSignal(path: string): AbortSignal {
  const call = vi.mocked(favoritesApi.removeFavorite).mock.calls.find(([calledPath, options]) => {
    return calledPath === path
      && (options as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
  })
  expect(call).toBeTruthy()
  const [, options] = call as unknown as [string, { signal: AbortSignal }]
  expect(Object.keys(options)).toEqual(['signal'])
  return options.signal
}

function expectUpdateFavoriteNoteCalledWithAbortSignal(path: string, note: string): AbortSignal {
  const call = vi.mocked(favoritesApi.updateFavoriteNote).mock.calls.find(([calledPath, calledNote, options]) => {
    return calledPath === path
      && calledNote === note
      && (options as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
  })
  expect(call).toBeTruthy()
  const [, , options] = call as unknown as [string, string, { signal: AbortSignal }]
  expect(Object.keys(options)).toEqual(['signal'])
  return options.signal
}

describe('FavoritesPage', () => {
  const pendingFavoritesRefetch = () => new Promise<Awaited<ReturnType<typeof favoritesApi.listFavorites>>>(() => {})

  const createDeferred = <T,>() => {
    let resolve!: (value: T | PromiseLike<T>) => void
    let reject!: (reason?: unknown) => void
    const promise = new Promise<T>((res, rej) => {
      resolve = res
      reject = rej
    })
    return { promise, resolve, reject }
  }

  beforeEach(() => {
    vi.clearAllMocks()
    useCanWriteMock.mockReturnValue(true)
    mockUser.id = 'user-1'
    mockUser.username = 'user1'
    mockUser.role = 'user'
    mockUser.email = 'user1@local'
    mockUser.homeDir = '/'
    mockUseRealBatchOperation = false
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
    mockNavigate.mockClear()
    mockBatchExecute.mockClear()
    mockBatchResult = {
      succeeded: 1,
      failed: 0,
      total: 1,
      succeededItems: ['/docs/report.pdf'],
      failedItems: [],
      failedErrors: [],
      warningCount: 0,
      warningMessages: [],
    }
    vi.mocked(favoritesApi.listFavorites).mockResolvedValue(mockFavorites)
  })

  it('passes abort signals to the favorites query', async () => {
    render(<FavoritesPage />)

    await waitFor(() => {
      expectCalledWithOnlyAbortSignal(vi.mocked(favoritesApi.listFavorites))
    })
  })

  it('renders favorites count after loading', async () => {
    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('收藏夹')).toBeInTheDocument()
      expect(screen.getByText('2 项收藏')).toBeInTheDocument()
    })
  })

  it('shows the leaf folder name for directory favorites', async () => {
    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('photos')).toBeInTheDocument()
    })

    expect(screen.queryByText('/photos/')).not.toBeInTheDocument()
  })

  it('shows empty state when there are no favorites', async () => {
    vi.mocked(favoritesApi.listFavorites).mockResolvedValue([])
    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('还没有收藏')).toBeInTheDocument()
    })
  })

  it('shows retryable error state when favorites fail to load', async () => {
    vi.mocked(favoritesApi.listFavorites).mockRejectedValue(new Error('Network error'))
    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('加载收藏列表失败')).toBeInTheDocument()
      expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })
  })

  it('shows an invalid-home error instead of loading favorites for non-admin users without a home directory', async () => {
    mockUser.id = 'user-2'
    mockUser.username = 'user2'
    mockUser.homeDir = ''

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getAllByText('主目录配置无效').length).toBeGreaterThan(0)
      expect(screen.getByText('当前账户未配置有效的主目录，无法查看收藏。请联系管理员修复账户 home_dir。')).toBeInTheDocument()
    })

    expect(vi.mocked(favoritesApi.listFavorites)).not.toHaveBeenCalled()
  })

  it('shows a disabled state when the favorites feature is turned off', async () => {
    vi.mocked(favoritesApi.listFavorites).mockRejectedValue(new FavoritesError('favorites feature disabled', 503, 'FAVORITES_FEATURE_DISABLED'))
    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('收藏功能已关闭')).toBeInTheDocument()
      expect(screen.getByText('当前服务已关闭收藏功能。如需使用，请在设置中重新启用。')).toBeInTheDocument()
    })

    expect(screen.queryByText('加载收藏列表失败')).not.toBeInTheDocument()
  })

  it('shows an unavailable state when the favorites store is unhealthy', async () => {
    vi.mocked(favoritesApi.listFavorites).mockRejectedValue(new FavoritesError('favorites feature unavailable', 503, 'FAVORITES_UNAVAILABLE'))
    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('收藏功能暂不可用')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })
  })

  it('shows a generic error state for non-feature favorites errors', async () => {
    vi.mocked(favoritesApi.listFavorites).mockRejectedValue(new FavoritesError('favorites crashed', 500, 'FAVORITES_ERROR'))
    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('加载收藏列表失败')).toBeInTheDocument()
      expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeInTheDocument()
    })
  })

  it('retries loading favorites from the error state', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.listFavorites)
      .mockRejectedValueOnce(new Error('Network error'))
      .mockResolvedValueOnce(mockFavorites)

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '重新加载' }))

    await waitFor(() => {
      expect(screen.getByText('2 项收藏')).toBeInTheDocument()
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
      expect(mockAddToast).toHaveBeenCalledWith({ title: '收藏夹已刷新', color: 'success' })
    })
  })

  it('does not reuse cached favorites from another user session', async () => {
    mockUser.id = 'user-2'
    mockUser.username = 'user2'
    vi.mocked(favoritesApi.listFavorites).mockImplementation(() => pendingFavoritesRefetch())

    const queryClient = new QueryClient({
      defaultOptions: {
        queries: {
          retry: false,
          gcTime: 0,
          staleTime: 0,
        },
      },
    })
    queryClient.setQueryData(['favorites'], [
      {
        path: '/admin/secret.txt',
        user_id: 'admin',
        created_at: '2024-01-15T10:00:00Z',
      },
    ])

    render(
      <QueryClientProvider client={queryClient}>
        <FavoritesPage />
      </QueryClientProvider>
    )

    await waitFor(() => {
      expect(vi.mocked(favoritesApi.listFavorites)).toHaveBeenCalledTimes(1)
    })

    expect(screen.queryByText('/admin/secret.txt')).toBeNull()
    expect(screen.queryByText('secret.txt')).toBeNull()
  })

  it('does not reuse cached favorites when the same user home directory changes', async () => {
    mockUser.homeDir = '/member-next'
    vi.mocked(favoritesApi.listFavorites).mockImplementation(() => pendingFavoritesRefetch())

    const queryClient = new QueryClient({
      defaultOptions: {
        queries: {
          retry: false,
          gcTime: 0,
          staleTime: 0,
        },
      },
    })
    queryClient.setQueryData(['favorites', 'user-1'], [
      {
        path: '/member-old/secret.txt',
        user_id: 'user-1',
        created_at: '2024-01-15T10:00:00Z',
      },
    ])

    render(
      <QueryClientProvider client={queryClient}>
        <FavoritesPage />
      </QueryClientProvider>
    )

    await waitFor(() => {
      expect(vi.mocked(favoritesApi.listFavorites)).toHaveBeenCalledTimes(1)
    })

    expect(screen.queryByText('/member-old/secret.txt')).toBeNull()
    expect(screen.queryByText('secret.txt')).toBeNull()
  })

  it('shows warning toast when favorites reload becomes unavailable', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.listFavorites)
      .mockRejectedValueOnce(new Error('Network error'))
      .mockRejectedValueOnce(new FavoritesError('favorites unavailable', 503, 'FAVORITES_UNAVAILABLE'))

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '重新加载' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '收藏功能暂不可用',
        description: '收藏存储未成功初始化，请检查设备状态或稍后重试。',
        color: 'warning',
      })
    })
  })

  it('shows warning toast when favorites reload finds the feature disabled', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.listFavorites)
      .mockRejectedValueOnce(new Error('Network error'))
      .mockRejectedValueOnce(new FavoritesError('favorites feature disabled', 503, 'FAVORITES_FEATURE_DISABLED'))

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '重新加载' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '收藏功能已关闭',
        description: '当前服务已关闭收藏功能。如需使用，请在设置中重新启用。',
        color: 'warning',
      })
    })
  })

  it('shows danger toast when favorites reload fails with a generic error', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.listFavorites)
      .mockRejectedValueOnce(new Error('Network error'))
      .mockRejectedValueOnce(new Error('still down'))

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
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

  it('supports keyboard navigation to a favorite item', async () => {
    const user = userEvent.setup()
    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '打开文件 /docs/report.pdf' })).toBeInTheDocument()
    })

    const navigateButton = screen.getByRole('button', { name: '打开文件 /docs/report.pdf' })
    navigateButton.focus()
    await user.keyboard('{Enter}')

    expect(mockNavigate).toHaveBeenCalledWith('/files/docs', {
      state: { highlightPath: '/docs/report.pdf' },
    })
  })

  it('navigates directly to a favorite folder', async () => {
    const user = userEvent.setup()

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '打开文件夹 /photos/' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '打开文件夹 /photos/' }))

    expect(mockNavigate).toHaveBeenCalledWith('/files/photos/')
  })

  it('encodes special characters when navigating to a favorite item', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.listFavorites).mockResolvedValueOnce([
      {
        path: '/docs/a #1?/report?.pdf',
        user_id: 'user-1',
        created_at: '2024-01-15T10:00:00Z',
      },
    ])

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '打开文件 /docs/a #1?/report?.pdf' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '打开文件 /docs/a #1?/report?.pdf' }))

    expect(mockNavigate).toHaveBeenCalledWith('/files/docs/a%20%231%3F', {
      state: { highlightPath: '/docs/a #1?/report?.pdf' },
    })
  })

  it('selects and clears all favorites from the list header checkbox', async () => {
    const user = userEvent.setup()

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getAllByRole('checkbox').length).toBeGreaterThan(2)
    })

    const selectAll = screen.getByRole('checkbox', { name: '选择全部收藏' })
    await user.click(selectAll)

    await waitFor(() => {
      expect(screen.getByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择2项')).toBeInTheDocument()
    })

    await user.click(selectAll)

    await waitFor(() => {
      expect(screen.queryByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择2项')).not.toBeInTheDocument()
    })
  })

  it('clears selected favorites from the selection bar action', async () => {
    const user = userEvent.setup()

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getAllByRole('checkbox').length).toBeGreaterThan(2)
    })

    await user.click(screen.getByRole('checkbox', { name: '选择收藏 /docs/report.pdf' }))

    await waitFor(() => {
      expect(screen.getByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择1项')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '取消选择' }))

    expect(screen.queryByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择1项')).not.toBeInTheDocument()
  })

  it('deselects a selected favorite from its row checkbox', async () => {
    const user = userEvent.setup()

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getAllByRole('checkbox').length).toBeGreaterThan(2)
    })

    const reportCheckbox = screen.getByRole('checkbox', { name: '选择收藏 /docs/report.pdf' })
    await user.click(reportCheckbox)

    await waitFor(() => {
      expect(screen.getByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择1项')).toBeInTheDocument()
    })

    await user.click(reportCheckbox)

    expect(screen.queryByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择1项')).not.toBeInTheDocument()
  })

  it('keeps failed favorites selected after partial batch removal', async () => {
    const user = userEvent.setup()
    mockBatchResult = {
      succeeded: 1,
      failed: 1,
      total: 2,
      succeededItems: ['/docs/report.pdf'],
      failedItems: ['/photos/'],
      failedErrors: [new Error('remove failed')],
      warningCount: 0,
      warningMessages: [],
    }

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
      expect(screen.getAllByRole('checkbox').length).toBeGreaterThan(2)
    })

    const checkboxes = screen.getAllByRole('checkbox')
    await user.click(checkboxes[1])
    await user.click(checkboxes[2])
    await user.click(screen.getByText('取消收藏'))

    await waitFor(() => {
      expect(mockBatchExecute).toHaveBeenCalledWith(['/docs/report.pdf', '/photos/'])
      expect(screen.getByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择1项')).toBeInTheDocument()
    })
  })

  it('hides guest write controls but keeps navigation', async () => {
    const user = userEvent.setup()
    useCanWriteMock.mockReturnValue(false)

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    expect(screen.queryByRole('button', { name: /编辑备注/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /取消收藏/ })).not.toBeInTheDocument()
    expect(screen.queryByText('取消收藏')).not.toBeInTheDocument()

    const navigateButton = screen.getByRole('button', { name: '打开文件 /docs/report.pdf' })
    await user.click(navigateButton)

    expect(mockNavigate).toHaveBeenCalledWith('/files/docs', {
      state: { highlightPath: '/docs/report.pdf' },
    })
  })

  it('shows unavailable toast when removing a favorite is unavailable', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.removeFavorite).mockRejectedValueOnce(
      new FavoritesError('favorites unavailable', 503, 'FAVORITES_UNAVAILABLE')
    )

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '取消收藏 /docs/report.pdf' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '收藏功能暂不可用',
        description: '收藏存储未成功初始化，请检查设备状态或稍后重试。',
        color: 'warning',
      })
    })
  })

  it('shows disabled toast when every batch remove fails because favorites are disabled', async () => {
    const user = userEvent.setup()
    mockUseRealBatchOperation = true
    vi.mocked(favoritesApi.removeFavorite).mockRejectedValue(
      new FavoritesError('favorites feature disabled', 503, 'FAVORITES_FEATURE_DISABLED')
    )

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getAllByRole('checkbox').length).toBeGreaterThan(2)
    })

    const checkboxes = screen.getAllByRole('checkbox')
    await user.click(checkboxes[1])
    await user.click(checkboxes[2])
    await user.click(screen.getByText('取消收藏'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '收藏功能已关闭',
        description: '当前服务已关闭收藏功能。如需使用，请在设置中重新启用。',
        color: 'warning',
      })
      expect(screen.getByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择2项')).toBeInTheDocument()
    })
  })

  it('shows unavailable toast when every batch remove fails because favorites are unavailable', async () => {
    const user = userEvent.setup()
    mockUseRealBatchOperation = true
    vi.mocked(favoritesApi.removeFavorite).mockRejectedValue(
      new FavoritesError('favorites unavailable', 503, 'FAVORITES_UNAVAILABLE')
    )

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getAllByRole('checkbox').length).toBeGreaterThan(2)
    })

    const checkboxes = screen.getAllByRole('checkbox')
    await user.click(checkboxes[1])
    await user.click(checkboxes[2])
    await user.click(screen.getByText('取消收藏'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '收藏功能暂不可用',
        description: '收藏存储未成功初始化，请检查设备状态或稍后重试。',
        color: 'warning',
      })
      expect(screen.getByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择2项')).toBeInTheDocument()
    })
  })

  it('falls back to the generic failure toast when every batch remove fails for a normal error', async () => {
    const user = userEvent.setup()
    mockUseRealBatchOperation = true
    vi.mocked(favoritesApi.removeFavorite).mockRejectedValue(new Error('remove failed'))

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getAllByRole('checkbox').length).toBeGreaterThan(2)
    })

    const checkboxes = screen.getAllByRole('checkbox')
    await user.click(checkboxes[1])
    await user.click(checkboxes[2])
    await user.click(screen.getByText('取消收藏'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '2 项取消收藏失败',
        color: 'danger',
      })
    })
  })

  it('shows a warning toast when batch remove succeeds with persistence warnings', async () => {
    const user = userEvent.setup()
    mockUseRealBatchOperation = true
    vi.mocked(favoritesApi.removeFavorite).mockResolvedValue({
      warning: true,
      message: 'favorite removed with persistence warning',
    })

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getAllByRole('checkbox').length).toBeGreaterThan(2)
    })

    const checkboxes = screen.getAllByRole('checkbox')
    await user.click(checkboxes[1])
    await user.click(checkboxes[2])
    await user.click(screen.getByText('取消收藏'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '2 项已取消收藏，但存在警告',
        color: 'warning',
      })
    })
  })

  it('optimistically removes a selected favorite before refetch completes', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.removeFavorite).mockResolvedValue({ warning: false, message: 'favorite removed successfully' })
    vi.mocked(favoritesApi.listFavorites).mockReset()
    vi.mocked(favoritesApi.listFavorites).mockResolvedValueOnce(mockFavorites)
    vi.mocked(favoritesApi.listFavorites).mockImplementation(() => pendingFavoritesRefetch())

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    const checkboxes = screen.getAllByRole('checkbox')
    await user.click(checkboxes[1])

    await waitFor(() => {
      expect(screen.getByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择1项')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '取消收藏 /docs/report.pdf' }))

    await waitFor(() => {
      expectRemoveFavoriteCalledWithAbortSignal('/docs/report.pdf')
    })

    await waitFor(() => {
      expect(screen.queryByText('report.pdf')).not.toBeInTheDocument()
      expect(screen.queryByText('月报')).not.toBeInTheDocument()
      expect(screen.queryByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择1项')).not.toBeInTheDocument()
      expect(screen.getByRole('button', { name: '打开文件夹 /photos/' })).toBeInTheDocument()
    })
  })

  it('keeps still-present selected favorites selected after a single removal', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.removeFavorite).mockResolvedValue({ warning: false, message: 'favorite removed successfully' })
    vi.mocked(favoritesApi.listFavorites).mockReset()
    vi.mocked(favoritesApi.listFavorites).mockResolvedValueOnce(mockFavorites)
    vi.mocked(favoritesApi.listFavorites).mockImplementation(() => pendingFavoritesRefetch())

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
      expect(screen.getByText('photos')).toBeInTheDocument()
    })

    const checkboxes = screen.getAllByRole('checkbox')
    await user.click(checkboxes[0])

    await waitFor(() => {
      expect(screen.getByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择2项')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '取消收藏 /docs/report.pdf' }))

    await waitFor(() => {
      expectRemoveFavoriteCalledWithAbortSignal('/docs/report.pdf')
    })

    await waitFor(() => {
      expect(screen.queryByText('report.pdf')).not.toBeInTheDocument()
      expect(screen.getByText('photos')).toBeInTheDocument()
      expect(screen.getByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择1项')).toBeInTheDocument()
    })
  })

  it('aborts a pending single remove when the page unmounts', async () => {
    const user = userEvent.setup()
    const pendingRemove = createDeferred<Awaited<ReturnType<typeof favoritesApi.removeFavorite>>>()
    vi.mocked(favoritesApi.removeFavorite).mockImplementationOnce(() => pendingRemove.promise as ReturnType<typeof favoritesApi.removeFavorite>)

    const { unmount } = render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '取消收藏 /docs/report.pdf' }))

    const signal = expectRemoveFavoriteCalledWithAbortSignal('/docs/report.pdf')
    expect(signal.aborted).toBe(false)

    unmount()

    expect(signal.aborted).toBe(true)
  })

  it('optimistically removes batch-removed favorites before refetch completes', async () => {
    const user = userEvent.setup()
    mockBatchResult = {
      succeeded: 2,
      failed: 0,
      total: 2,
      succeededItems: ['/docs/report.pdf', '/photos/'],
      failedItems: [],
      failedErrors: [],
      warningCount: 0,
      warningMessages: [],
    }
    vi.mocked(favoritesApi.listFavorites).mockReset()
    vi.mocked(favoritesApi.listFavorites).mockResolvedValueOnce(mockFavorites)
    vi.mocked(favoritesApi.listFavorites).mockImplementation(() => pendingFavoritesRefetch())

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
      expect(screen.getAllByRole('checkbox').length).toBeGreaterThan(2)
    })

    const checkboxes = screen.getAllByRole('checkbox')
    await user.click(checkboxes[1])
    await user.click(checkboxes[2])

    await waitFor(() => {
      expect(screen.getByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择2项')).toBeInTheDocument()
    })

    await user.click(screen.getByText('取消收藏'))

    await waitFor(() => {
      expect(mockBatchExecute).toHaveBeenCalledWith(['/docs/report.pdf', '/photos/'])
    })

    await waitFor(() => {
      expect(screen.queryByText('report.pdf')).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: '打开文件夹 /photos/' })).not.toBeInTheDocument()
      expect(screen.queryByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择2项')).not.toBeInTheDocument()
      expect(screen.getByText('还没有收藏')).toBeInTheDocument()
    })
  })

  it('aborts pending batch remove requests when the page unmounts', async () => {
    const user = userEvent.setup()
    mockUseRealBatchOperation = true
    const pendingRemove = createDeferred<Awaited<ReturnType<typeof favoritesApi.removeFavorite>>>()
    vi.mocked(favoritesApi.removeFavorite).mockImplementation(() => pendingRemove.promise as ReturnType<typeof favoritesApi.removeFavorite>)

    const { unmount } = render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getAllByRole('checkbox').length).toBeGreaterThan(2)
    })

    const checkboxes = screen.getAllByRole('checkbox')
    await user.click(checkboxes[1])
    await user.click(checkboxes[2])
    await user.click(screen.getByText('取消收藏'))

    const reportSignal = expectRemoveFavoriteCalledWithAbortSignal('/docs/report.pdf')
    const photosSignal = expectRemoveFavoriteCalledWithAbortSignal('/photos/')
    expect(reportSignal.aborted).toBe(false)
    expect(photosSignal.aborted).toBe(false)

    unmount()

    expect(reportSignal.aborted).toBe(true)
    expect(photosSignal.aborted).toBe(true)
  })

  it('treats batch remove not-found results as already removed instead of keeping stale selections', async () => {
    const user = userEvent.setup()
    mockUseRealBatchOperation = true
    vi.mocked(favoritesApi.removeFavorite).mockImplementation(async (path: string) => {
      if (path === '/photos/') {
        throw new FavoritesError('favorite not found', 404, 'FAVORITE_NOT_FOUND')
      }
    })
    vi.mocked(favoritesApi.listFavorites).mockReset()
    vi.mocked(favoritesApi.listFavorites).mockResolvedValueOnce(mockFavorites)
    vi.mocked(favoritesApi.listFavorites).mockImplementation(() => pendingFavoritesRefetch())

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
      expect(screen.getAllByRole('checkbox').length).toBeGreaterThan(2)
    })

    const checkboxes = screen.getAllByRole('checkbox')
    await user.click(checkboxes[1])
    await user.click(checkboxes[2])

    await waitFor(() => {
      expect(screen.getByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择2项')).toBeInTheDocument()
    })

    await user.click(screen.getByText('取消收藏'))

    await waitFor(() => {
      expect(mockBatchExecute).toHaveBeenCalledWith(['/docs/report.pdf', '/photos/'])
    })

    await waitFor(() => {
      expect(screen.queryByText('report.pdf')).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: '打开文件夹 /photos/' })).not.toBeInTheDocument()
      expect(screen.queryByText((_, node) => node?.textContent?.replace(/\s+/g, '') === '已选择2项')).not.toBeInTheDocument()
      expect(screen.getByText('还没有收藏')).toBeInTheDocument()
    })
  })

  it('shows disabled toast when updating a note after favorites are disabled', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.updateFavoriteNote).mockRejectedValueOnce(
      new FavoritesError('favorites feature disabled', 503, 'FAVORITES_FEATURE_DISABLED')
    )

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '编辑备注 /docs/report.pdf' }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '保存' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '收藏功能已关闭',
        description: '当前服务已关闭收藏功能。如需使用，请在设置中重新启用。',
        color: 'warning',
      })
    })
  })

  it('removes a stale favorite and shows a warning when single remove hits not found', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.removeFavorite).mockRejectedValueOnce(
      new FavoritesError('favorite not found', 404, 'FAVORITE_NOT_FOUND')
    )

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '取消收藏 /docs/report.pdf' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '收藏已不存在',
        description: '该收藏可能已被其他操作移除，列表已同步更新。',
        color: 'warning',
      })
    })

    expect(screen.queryByText('report.pdf')).not.toBeInTheDocument()
  })

  it('closes the note editor and removes a stale favorite when note update hits not found', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.updateFavoriteNote).mockRejectedValueOnce(
      new FavoritesError('favorite not found', 404, 'FAVORITE_NOT_FOUND')
    )

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '编辑备注 /docs/report.pdf' }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '保存' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '收藏已不存在',
        description: '该收藏可能已被其他操作移除，列表已同步更新。',
        color: 'warning',
      })
    })

    expect(screen.queryByRole('heading', { name: '编辑备注' })).not.toBeInTheDocument()
    expect(screen.queryByText('report.pdf')).not.toBeInTheDocument()
  })

  it('closes the note editor when canceling an idle edit', async () => {
    const user = userEvent.setup()

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '编辑备注 /docs/report.pdf' }))

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '编辑备注' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '取消' }))

    expect(screen.queryByRole('heading', { name: '编辑备注' })).not.toBeInTheDocument()
    expect(favoritesApi.updateFavoriteNote).not.toHaveBeenCalled()
  })

  it('keeps the note editor open while a pending save is in flight', async () => {
    const user = userEvent.setup()
    const pendingNoteSave = createDeferred<{ message?: string }>()
    vi.mocked(favoritesApi.updateFavoriteNote).mockImplementationOnce(() => pendingNoteSave.promise)

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
      expect(screen.getByText('photos')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '编辑备注 /docs/report.pdf' }))
    await user.clear(screen.getByLabelText('备注'))
    await user.type(screen.getByLabelText('备注'), '旧备注')
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      expectUpdateFavoriteNoteCalledWithAbortSignal('/docs/report.pdf', '旧备注')
    })

    await user.click(screen.getByRole('button', { name: '取消' }))

    expect(screen.getByRole('heading', { name: '编辑备注' })).toBeInTheDocument()
    expect(screen.getByLabelText('备注')).toHaveValue('旧备注')

    await act(async () => {
      pendingNoteSave.resolve({ warning: false, message: 'favorite note updated successfully' })
    })

    await waitFor(() => {
      expect(screen.queryByRole('heading', { name: '编辑备注' })).not.toBeInTheDocument()
    })
  })

  it('keeps the note editor open when a pending save later fails', async () => {
    const user = userEvent.setup()
    const pendingNoteSave = createDeferred<{ message?: string }>()
    vi.mocked(favoritesApi.updateFavoriteNote).mockImplementationOnce(() => pendingNoteSave.promise)

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '编辑备注 /docs/report.pdf' }))
    await user.clear(screen.getByLabelText('备注'))
    await user.type(screen.getByLabelText('备注'), '保留备注')
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      expectUpdateFavoriteNoteCalledWithAbortSignal('/docs/report.pdf', '保留备注')
    })

    await user.click(screen.getByRole('button', { name: '取消' }))

    expect(screen.getByRole('heading', { name: '编辑备注' })).toBeInTheDocument()
    expect(screen.getByLabelText('备注')).toHaveValue('保留备注')

    await act(async () => {
      pendingNoteSave.reject(new Error('save failed'))
    })

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '操作失败',
        description: '操作未完成，请稍后重试。',
        color: 'danger',
      })
    })

    expect(screen.getByRole('heading', { name: '编辑备注' })).toBeInTheDocument()
    expect(screen.getByLabelText('备注')).toHaveValue('保留备注')
  })

  it('aborts a pending note save when the page unmounts', async () => {
    const user = userEvent.setup()
    const pendingNoteSave = createDeferred<Awaited<ReturnType<typeof favoritesApi.updateFavoriteNote>>>()
    vi.mocked(favoritesApi.updateFavoriteNote).mockImplementationOnce(() => pendingNoteSave.promise as ReturnType<typeof favoritesApi.updateFavoriteNote>)

    const { unmount } = render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '编辑备注 /docs/report.pdf' }))
    await user.clear(screen.getByLabelText('备注'))
    await user.type(screen.getByLabelText('备注'), '待保存备注')
    await user.click(screen.getByRole('button', { name: '保存' }))

    const signal = expectUpdateFavoriteNoteCalledWithAbortSignal('/docs/report.pdf', '待保存备注')
    expect(signal.aborted).toBe(false)

    unmount()

    expect(signal.aborted).toBe(true)
  })

  it('shows the localized remove success toast after a successful single remove', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.removeFavorite).mockResolvedValueOnce({ warning: false, message: 'favorite removed successfully' })

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '取消收藏 /docs/report.pdf' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({ title: '已取消收藏', color: 'success' })
    })
  })

  it('uses the localized remove success toast for alternate backend messages', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.removeFavorite).mockResolvedValueOnce({ warning: false, message: 'removed' })

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '取消收藏 /docs/report.pdf' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({ title: '已取消收藏', color: 'success' })
    })
  })

  it('shows a localized warning toast after a successful single remove with persistence warnings', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.removeFavorite).mockResolvedValueOnce({
      warning: true,
      message: 'favorite removed with persistence warning',
    })

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '取消收藏 /docs/report.pdf' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({ title: '已取消收藏，但存在警告', color: 'warning' })
    })
  })

  it('shows the localized note update success toast after a successful save', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.updateFavoriteNote).mockResolvedValueOnce({ warning: false, message: 'favorite note updated successfully' })

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '编辑备注 /docs/report.pdf' }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '保存' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({ title: '备注已更新', color: 'success' })
    })
  })

  it('shows a localized warning toast after a successful note update with persistence warnings', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.updateFavoriteNote).mockResolvedValueOnce({
      warning: true,
      message: 'favorite note updated with persistence warning',
    })

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '编辑备注 /docs/report.pdf' }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '保存' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({ title: '备注已更新，但存在警告', color: 'warning' })
    })
  })

  it('uses the localized note update success toast for alternate backend messages', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.updateFavoriteNote).mockResolvedValueOnce({ warning: false, message: 'updated' })

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '编辑备注 /docs/report.pdf' }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '保存' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({ title: '备注已更新', color: 'success' })
    })
  })
})
