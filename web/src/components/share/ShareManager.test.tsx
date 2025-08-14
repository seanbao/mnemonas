import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, userEvent } from '@/test/utils'
import { ShareManager } from './ShareManager'
import * as shareApi from '@/api/share'
import { ShareError, type Share } from '@/api/share'

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

const mockShares: Share[] = [
  {
    id: 'share-1',
    path: '/docs/report.pdf',
    type: 'file' as const,
    created_by: 'u1',
    created_at: '2026-03-27T00:00:00Z',
    permission: 'read' as const,
    enabled: true,
    has_password: false,
    access_count: 3,
    max_access: 0,
    url: '/s/share-1',
  },
]

const disabledExpiredFolderShare: Share = {
  id: 'share-folder',
  path: '/archive/photos',
  type: 'folder' as const,
  created_by: 'u1',
  created_at: '2026-03-27T00:00:00Z',
  permission: 'read' as const,
  enabled: false,
  has_password: true,
  expires_at: '2020-01-01T00:00:00Z',
  access_count: 5,
  max_access: 10,
  url: '/s/share-folder',
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

  it('shows a generic error state for ShareError failures unrelated to feature state', async () => {
    vi.mocked(shareApi.listShares).mockRejectedValue(new ShareError('bad request', 400))

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('加载分享列表失败')).toBeInTheDocument()
      expect(screen.getByText('bad request')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })
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

  it('uses the full path as the visible name for a root share', async () => {
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([
      {
        ...mockShares[0],
        id: 'share-root',
        path: '/',
        type: 'folder',
      },
    ])

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('我的分享 (1)')).toBeInTheDocument()
      expect(screen.getAllByText('/').length).toBeGreaterThanOrEqual(2)
    })
  })

  it('renders share risk markers and reason details', async () => {
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([
      {
        ...mockShares[0],
        risk: {
          level: 'high',
          reasons: [
            { code: 'no_password', level: 'high', message: '未设置密码，拿到链接的人都能访问' },
            { code: 'unlimited_access', level: 'medium', message: '未设置访问次数上限' },
          ],
        },
      },
    ])

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('风险 1')).toBeInTheDocument()
      expect(screen.getByText('需处理')).toBeInTheDocument()
      expect(screen.getByText('未设置密码，拿到链接的人都能访问')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '需复核 (1)' })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '停用需处理 (1)' })).toBeInTheDocument()
    })
  })

  it('filters the share list to risky shares only', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([
      {
        ...mockShares[0],
        id: 'share-safe',
        path: '/docs/safe.pdf',
      },
      {
        ...mockShares[0],
        id: 'share-risk',
        path: '/docs/risky.pdf',
        risk: {
          level: 'medium',
          reasons: [
            { code: 'no_expiration', level: 'medium', message: '未设置过期时间，链接会长期有效' },
          ],
        },
      },
    ])

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('safe.pdf')).toBeInTheDocument()
      expect(screen.getByText('risky.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '需复核 (1)' }))

    await waitFor(() => {
      expect(screen.queryByText('safe.pdf')).not.toBeInTheDocument()
      expect(screen.getByText('risky.pdf')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '全部' })).toBeInTheDocument()
    })
  })

  it('filters the share list to links that are expiring soon', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([
      {
        ...mockShares[0],
        id: 'share-normal',
        path: '/docs/normal.pdf',
      },
      {
        ...mockShares[0],
        id: 'share-expiring',
        path: '/docs/soon.pdf',
        risk: {
          level: 'low',
          reasons: [
            { code: 'expiring_soon', level: 'low', message: '分享即将到期，建议确认是否需要延长或关闭' },
          ],
        },
      },
    ])

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('normal.pdf')).toBeInTheDocument()
      expect(screen.getByText('soon.pdf')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '即将到期 (1)' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '即将到期 (1)' }))

    await waitFor(() => {
      expect(screen.queryByText('normal.pdf')).not.toBeInTheDocument()
      expect(screen.getByText('soon.pdf')).toBeInTheDocument()
    })
  })

  it('filters the share list to passwordless and broad folder shares', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([
      {
        ...mockShares[0],
        id: 'share-passwordless',
        path: '/docs/open.pdf',
        risk: {
          level: 'high',
          reasons: [
            { code: 'no_password', level: 'high', message: '未设置密码，拿到链接的人都能访问' },
          ],
        },
      },
      {
        ...mockShares[0],
        id: 'share-broad',
        path: '/Photos',
        type: 'folder',
        has_password: true,
        risk: {
          level: 'medium',
          reasons: [
            { code: 'broad_folder', level: 'medium', message: '分享顶层文件夹可能覆盖较多内容' },
          ],
        },
      },
    ])

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('open.pdf')).toBeInTheDocument()
      expect(screen.getByText('Photos')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '无密码 (1)' })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '覆盖较大 (1)' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '无密码 (1)' }))
    await waitFor(() => {
      expect(screen.getByText('open.pdf')).toBeInTheDocument()
      expect(screen.queryByText('Photos')).not.toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '覆盖较大 (1)' }))
    await waitFor(() => {
      expect(screen.queryByText('open.pdf')).not.toBeInTheDocument()
      expect(screen.getByText('Photos')).toBeInTheDocument()
    })
  })

  it('disables all currently high-risk shares from the header action', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([
      {
        ...mockShares[0],
        risk: {
          level: 'high',
          reasons: [
            { code: 'no_password', level: 'high', message: '未设置密码，拿到链接的人都能访问' },
          ],
        },
      },
    ])
    vi.mocked(shareApi.updateShare).mockResolvedValueOnce({
      ...mockShares[0],
      enabled: false,
      risk: { level: 'none' },
    })

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '停用需处理 (1)' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '停用需处理 (1)' }))

    await waitFor(() => {
      expect(shareApi.updateShare).toHaveBeenCalledWith('share-1', { enabled: false })
      expect(mockAddToast).toHaveBeenCalledWith({ title: '已停用 1 个需处理分享', color: 'success' })
      expect(screen.getByText('已禁用')).toBeInTheDocument()
      expect(screen.queryByText('需处理')).not.toBeInTheDocument()
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
      expect(screen.getByText('分享服务当前不可用，请检查设备状态或稍后重试。')).toBeInTheDocument()
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
        description: '分享服务当前不可用，请检查设备状态或稍后重试。',
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
        description: '分享服务当前不可用，请检查设备状态或稍后重试。',
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
