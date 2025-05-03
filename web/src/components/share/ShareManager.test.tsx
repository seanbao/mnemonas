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

const disabledExpiredFolderShare = {
  id: 'share-folder',
  path: '/archive/photos',
  type: 'folder' as const,
  enabled: false,
  has_password: true,
  expires_at: '2020-01-01T00:00:00Z',
  access_count: 5,
  max_access: 10,
  url: '/s/share-folder',
  created_at: '2026-03-27T00:00:00Z',
  updated_at: '2026-03-27T00:00:00Z',
}

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

  it('retries loading from the unavailable empty state', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.listShares)
      .mockRejectedValueOnce(new ShareError('share service unavailable', 503))
      .mockResolvedValueOnce(mockShares)

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('分享功能暂不可用')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '重新加载' }))

    await waitFor(() => {
      expect(screen.getByText('我的分享 (1)')).toBeInTheDocument()
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })
  })

  it('shows a disabled state without loading shares when the feature is off', async () => {
    render(<ShareManager featureEnabled={false} />)

    expect(screen.getByText('分享功能已关闭')).toBeInTheDocument()
    expect(screen.getByText('当前服务已关闭分享功能。启用后可在此管理已创建的分享链接。')).toBeInTheDocument()
    expect(shareApi.listShares).not.toHaveBeenCalled()
  })

  it('shows an empty state when there are no shares', async () => {
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([])

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('暂无分享')).toBeInTheDocument()
      expect(screen.getByText('在文件浏览器中选择文件或文件夹创建分享链接')).toBeInTheDocument()
    })
  })

  it('renders disabled expired protected folder metadata', async () => {
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([disabledExpiredFolderShare])

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('photos')).toBeInTheDocument()
      expect(screen.getByText('/archive/photos')).toBeInTheDocument()
      expect(screen.getByText('已禁用')).toBeInTheDocument()
      expect(screen.getAllByText('已过期')).toHaveLength(2)
      expect(screen.getByText('密码保护')).toBeInTheDocument()
      expect(screen.getByText('5 次访问 / 10')).toBeInTheDocument()
    })
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

  it('ignores stale share load failures after unmount', async () => {
    const pendingLoad = createDeferred<typeof mockShares>()
    vi.mocked(shareApi.listShares).mockReturnValueOnce(pendingLoad.promise)

    const { unmount } = render(<ShareManager />)

    unmount()
    pendingLoad.reject(new Error('late failure'))

    await waitFor(() => {
      expect(mockAddToast).not.toHaveBeenCalled()
    })
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

  it('copies a share link from the inline action button', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.copyShareUrl).mockResolvedValueOnce()

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 复制分享链接'))

    await waitFor(() => {
      expect(shareApi.copyShareUrl).toHaveBeenCalledWith(mockShares[0])
      expect(mockAddToast).toHaveBeenCalledWith({ title: '链接已复制', color: 'success' })
    })
  })

  it('copies a share link from the action menu', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.copyShareUrl).mockResolvedValueOnce()

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('复制链接'))

    await waitFor(() => {
      expect(shareApi.copyShareUrl).toHaveBeenCalledWith(mockShares[0])
      expect(mockAddToast).toHaveBeenCalledWith({ title: '链接已复制', color: 'success' })
    })
  })

  it('shows a toast when copying a share link fails', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.copyShareUrl).mockRejectedValueOnce(new Error('clipboard blocked'))

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 复制分享链接'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({ title: '复制失败', color: 'danger' })
    })
  })

  it('disables an enabled share successfully', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.updateShare).mockResolvedValueOnce({ ...mockShares[0], enabled: false })

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('禁用分享'))

    await waitFor(() => {
      expect(shareApi.updateShare).toHaveBeenCalledWith('share-1', { enabled: false })
      expect(mockAddToast).toHaveBeenCalledWith({ title: '分享已禁用', color: 'success' })
      expect(screen.getByText('已禁用')).toBeInTheDocument()
    })
  })

  it('enables a disabled share successfully', async () => {
    const user = userEvent.setup()
    const disabledShare = { ...mockShares[0], enabled: false }
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([disabledShare])
    vi.mocked(shareApi.updateShare).mockResolvedValueOnce({ ...disabledShare, enabled: true })

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('已禁用')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('启用分享'))

    await waitFor(() => {
      expect(shareApi.updateShare).toHaveBeenCalledWith('share-1', { enabled: true })
      expect(mockAddToast).toHaveBeenCalledWith({ title: '分享已启用', color: 'success' })
      expect(screen.queryByText('已禁用')).not.toBeInTheDocument()
    })
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

  it('shows disabled toast when toggling a share fails because sharing is off', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.updateShare).mockRejectedValue(new ShareError('share feature disabled', 503, 'SHARE_FEATURE_DISABLED'))

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('禁用分享'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '分享功能已关闭',
        description: '当前服务已关闭分享功能。启用后可在此管理已创建的分享链接。',
        color: 'warning',
      })
    })
  })

  it('shows fallback action error details when toggling fails with an unknown value', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.updateShare).mockRejectedValue('network stopped')

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('禁用分享'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '操作失败',
        description: '请稍后重试',
        color: 'danger',
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

  it('closes the delete modal without removing the share when canceled', async () => {
    const user = userEvent.setup()

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('删除分享'))

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '删除分享' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '取消' }))

    await waitFor(() => {
      expect(screen.queryByRole('heading', { name: '删除分享' })).not.toBeInTheDocument()
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })
  })

  it('deletes a share successfully', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.deleteShare).mockResolvedValueOnce(successActionResult)

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('删除分享'))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '删除' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '删除' }))

    await waitFor(() => {
      expect(shareApi.deleteShare).toHaveBeenCalledWith('share-1')
      expect(mockAddToast).toHaveBeenCalledWith({ title: '分享已删除', color: 'success' })
      expect(screen.queryByText('report.pdf')).not.toBeInTheDocument()
    })
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

  it('uses a fallback warning message when delete succeeds with unnamed warning metadata', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.deleteShare).mockResolvedValueOnce({
      warning: true,
      message: undefined,
    })

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('删除分享'))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '删除' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '删除' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '分享已删除，但存在警告',
        color: 'warning',
      })
    })
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
