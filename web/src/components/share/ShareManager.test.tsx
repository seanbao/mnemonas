import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, userEvent } from '@/test/utils'
import { ShareManager } from './ShareManager'
import * as shareApi from '@/api/share'
import { ShareError } from '@/api/share'

const mockAddToast = vi.fn()
const successActionResult = { warning: false, message: undefined } as const

vi.mock('@heroui/react', async () => {
  const actual = await vi.importActual<typeof import('@heroui/react')>('@heroui/react')
  return {
    ...actual,
    addToast: (...args: unknown[]) => mockAddToast(...args),
  }
})

vi.mock('@/api/share', async () => {
  const actual = await vi.importActual<typeof import('@/api/share')>('@/api/share')
  return {
    ...actual,
    listShares: vi.fn(),
    deleteShare: vi.fn(),
    updateShare: vi.fn(),
    copyShareUrl: vi.fn(),
  }
})

const mockShares = [
  {
    id: 'share-1',
    path: '/docs/report.pdf',
    type: 'file' as const,
    enabled: true,
    has_password: false,
    expires_at: null,
    access_count: 3,
    max_access: 0,
    url: '/s/share-1',
    created_at: '2026-03-27T00:00:00Z',
    updated_at: '2026-03-27T00:00:00Z',
  },
]

function createDeferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('ShareManager', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(shareApi.listShares).mockResolvedValue(mockShares)
  })

  it('shows a retryable error state when the share list fails to load', async () => {
    vi.mocked(shareApi.listShares).mockRejectedValue(new Error('share feature disabled'))

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('加载分享列表失败')).toBeInTheDocument()
      expect(screen.getByText('share feature disabled')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })

    expect(screen.queryByText('暂无分享')).not.toBeInTheDocument()
  })

  it('shows a disabled state when the backend reports sharing is off', async () => {
    vi.mocked(shareApi.listShares).mockRejectedValue(new ShareError('share feature disabled', 503, 'SHARE_FEATURE_DISABLED'))

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('分享功能已关闭')).toBeInTheDocument()
      expect(screen.getByText('当前服务已关闭分享功能。启用后可在此管理已创建的分享链接。')).toBeInTheDocument()
    })

    expect(mockAddToast).not.toHaveBeenCalled()
  })

  it('shows an unavailable state when share listing returns a generic 503', async () => {
    vi.mocked(shareApi.listShares).mockRejectedValue(new ShareError('share service unavailable', 503))

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('分享功能暂不可用')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })

    expect(mockAddToast).not.toHaveBeenCalled()
  })

  it('shows a disabled state without loading shares when the feature is off', async () => {
    render(<ShareManager featureEnabled={false} />)

    expect(screen.getByText('分享功能已关闭')).toBeInTheDocument()
    expect(screen.getByText('当前服务已关闭分享功能。启用后可在此管理已创建的分享链接。')).toBeInTheDocument()
    expect(shareApi.listShares).not.toHaveBeenCalled()
  })

  it('ignores stale share loads after the feature is disabled', async () => {
    const pendingLoad = createDeferred<typeof mockShares>()
    vi.mocked(shareApi.listShares).mockReturnValueOnce(pendingLoad.promise)

    const { rerender } = render(<ShareManager featureEnabled={true} />)

    rerender(<ShareManager featureEnabled={false} />)

    pendingLoad.resolve(mockShares)

    await waitFor(() => {
      expect(screen.getByText('分享功能已关闭')).toBeInTheDocument()
    })

    expect(screen.queryByText('我的分享 (1)')).not.toBeInTheDocument()
    expect(screen.queryByText('report.pdf')).not.toBeInTheDocument()
  })

  it('retries loading from the error state', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.listShares)
      .mockRejectedValueOnce(new Error('Network error'))
      .mockResolvedValueOnce(mockShares)

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '重新加载' }))

    await waitFor(() => {
      expect(screen.getByText('我的分享 (1)')).toBeInTheDocument()
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })
  })

  it('shows unavailable state after refreshing an existing list when share service becomes unavailable', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.listShares)
      .mockResolvedValueOnce(mockShares)
      .mockRejectedValueOnce(new ShareError('share service unavailable', 503))

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('我的分享 (1)')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('刷新分享列表'))

    await waitFor(() => {
      expect(screen.getByText('分享功能暂不可用')).toBeInTheDocument()
      expect(screen.getByText('分享服务当前不可用，请检查系统健康状态或稍后重试。')).toBeInTheDocument()
    })

    expect(mockAddToast).not.toHaveBeenCalled()
  })

  it('keeps the existing list and shows a toast when refresh fails with a generic error', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.listShares)
      .mockResolvedValueOnce(mockShares)
      .mockRejectedValueOnce(new Error('Network error'))

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('我的分享 (1)')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('刷新分享列表'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '刷新分享列表失败',
        description: 'Network error',
        color: 'danger',
      })
    })

    expect(screen.getByText('我的分享 (1)')).toBeInTheDocument()
    expect(screen.getByText('report.pdf')).toBeInTheDocument()
  })

  it('shows unavailable toast when toggling a share fails because the share service is unavailable', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.updateShare).mockRejectedValue(new ShareError('share service unavailable', 503))

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))

    await waitFor(() => {
      expect(screen.getByText('禁用分享')).toBeInTheDocument()
    })

    await user.click(screen.getByText('禁用分享'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '分享操作暂不可用',
        description: '分享服务当前不可用，请检查系统健康状态或稍后重试。',
        color: 'warning',
      })
    })
  })

  it('removes a stale share and shows a warning when toggle hits SHARE_NOT_FOUND', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.updateShare).mockRejectedValue(new ShareError('share not found', 404, 'SHARE_NOT_FOUND'))

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))

    await waitFor(() => {
      expect(screen.getByText('禁用分享')).toBeInTheDocument()
    })

    await user.click(screen.getByText('禁用分享'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '分享已不存在',
        description: '该分享可能已被其他操作删除，列表已同步更新。',
        color: 'warning',
      })
    })

    expect(screen.queryByText('report.pdf')).not.toBeInTheDocument()
  })

  it('shows unavailable toast when deleting a share fails because the share service is unavailable', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.deleteShare).mockRejectedValue(new ShareError('share service unavailable', 503))

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))

    await waitFor(() => {
      expect(screen.getByText('删除分享')).toBeInTheDocument()
    })

    await user.click(screen.getByText('删除分享'))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '删除' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '删除' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '删除分享暂不可用',
        description: '分享服务当前不可用，请检查系统健康状态或稍后重试。',
        color: 'warning',
      })
    })
  })

  it('removes a stale share and closes the modal when delete hits SHARE_NOT_FOUND', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.deleteShare).mockRejectedValue(new ShareError('share not found', 404, 'SHARE_NOT_FOUND'))

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))

    await waitFor(() => {
      expect(screen.getByText('删除分享')).toBeInTheDocument()
    })

    await user.click(screen.getByText('删除分享'))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '删除' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '删除' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '分享已不存在',
        description: '该分享可能已被其他操作删除，列表已同步更新。',
        color: 'warning',
      })
    })

    expect(screen.queryByRole('heading', { name: '删除分享' })).not.toBeInTheDocument()
    expect(screen.queryByText('report.pdf')).not.toBeInTheDocument()
  })

  it('keeps the delete modal open while a pending delete is in flight', async () => {
    const user = userEvent.setup()
    const firstDelete = createDeferred<typeof successActionResult>()
    vi.mocked(shareApi.deleteShare).mockImplementationOnce(() => firstDelete.promise)

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))

    await waitFor(() => {
      expect(screen.getByText('删除分享')).toBeInTheDocument()
    })

    await user.click(screen.getByText('删除分享'))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '删除' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '删除' }))

    await waitFor(() => {
      expect(shareApi.deleteShare).toHaveBeenCalledWith('share-1')
    })

    await user.click(screen.getByRole('button', { name: '取消' }))

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '删除分享' })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: /删除/ })).toBeInTheDocument()
      expect(screen.getByText('/docs/report.pdf', { selector: 'span' })).toBeInTheDocument()
    })

    firstDelete.resolve(successActionResult)

    await waitFor(() => {
      expect(screen.queryByRole('heading', { name: '删除分享' })).not.toBeInTheDocument()
      expect(screen.queryByLabelText('report.pdf 分享操作')).not.toBeInTheDocument()
    })
  })

  it('keeps the delete modal open when a pending delete later fails', async () => {
    const user = userEvent.setup()
    const firstDelete = createDeferred<typeof successActionResult>()
    vi.mocked(shareApi.deleteShare).mockImplementationOnce(() => firstDelete.promise)

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))

    await waitFor(() => {
      expect(screen.getByText('删除分享')).toBeInTheDocument()
    })

    await user.click(screen.getByText('删除分享'))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '删除' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '删除' }))

    await waitFor(() => {
      expect(shareApi.deleteShare).toHaveBeenCalledWith('share-1')
    })

    await user.click(screen.getByRole('button', { name: '取消' }))

    expect(screen.getByRole('heading', { name: '删除分享' })).toBeInTheDocument()
    expect(screen.getByText('/docs/report.pdf', { selector: 'span' })).toBeInTheDocument()

    firstDelete.reject(new Error('delete failed'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '删除失败',
        description: 'delete failed',
        color: 'danger',
      })
    })

    expect(screen.getByRole('heading', { name: '删除分享' })).toBeInTheDocument()
    expect(screen.getByText('/docs/report.pdf', { selector: 'span' })).toBeInTheDocument()
  })

  it('shows warning toast when delete succeeds with backend warning metadata', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.deleteShare).mockResolvedValueOnce({
      warning: true,
      message: 'share deleted with audit warning',
    })

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))

    await waitFor(() => {
      expect(screen.getByText('删除分享')).toBeInTheDocument()
    })

    await user.click(screen.getByText('删除分享'))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '删除' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '删除' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: 'share deleted with audit warning',
        color: 'warning',
      })
    })

    expect(screen.queryByText('report.pdf')).not.toBeInTheDocument()
  })
})