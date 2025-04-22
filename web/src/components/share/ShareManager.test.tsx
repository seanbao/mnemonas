import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, userEvent } from '@/test/utils'
import { ShareManager } from './ShareManager'
import * as shareApi from '@/api/share'
import { ShareError } from '@/api/share'

const mockAddToast = vi.fn()

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
})