import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within, userEvent } from '@/test/utils'
import { ShareManager } from './ShareManager'
import * as shareApi from '@/api/share'
import { ShareError, type Share } from '@/api/share'
import * as activityApi from '@/api/activity'

const mockAddToast = vi.fn()
const successActionResult = { warning: false, message: undefined } as const
const originalClipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')

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

vi.mock('@/api/activity', async () => {
  const actual = await vi.importActual<typeof import('@/api/activity')>('@/api/activity')
  return {
    ...actual,
    listActivity: vi.fn(),
    createActivityReviewRecord: vi.fn(),
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

function expectUpdateShareCalledWithAbortSignal(id: string, request: unknown): AbortSignal {
  const call = vi.mocked(shareApi.updateShare).mock.calls.find(([calledId, calledRequest, options]) => {
    return calledId === id
      && JSON.stringify(calledRequest) === JSON.stringify(request)
      && (options as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
  })
  expect(call).toBeTruthy()
  const [, , options] = call as unknown as [string, unknown, { signal: AbortSignal }]
  expect(Object.keys(options)).toEqual(['signal'])
  return options.signal
}

function expectDeleteShareCalledWithAbortSignal(id: string): AbortSignal {
  const call = vi.mocked(shareApi.deleteShare).mock.calls.find(([calledId, options]) => {
    return calledId === id
      && (options as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
  })
  expect(call).toBeTruthy()
  const [, options] = call as unknown as [string, { signal: AbortSignal }]
  expect(Object.keys(options)).toEqual(['signal'])
  return options.signal
}

describe('ShareManager', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    window.history.pushState({}, '', '/')
    vi.mocked(shareApi.listShares).mockResolvedValue(mockShares)
    vi.mocked(activityApi.listActivity).mockResolvedValue({
      items: [],
      total: 0,
      limit: 100,
      offset: 0,
    })
    vi.mocked(activityApi.createActivityReviewRecord).mockImplementation(async (input) => ({
      id: 'share-review-record-1',
      reviewed_at: '2026-03-27T00:00:00Z',
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
  })

  afterEach(() => {
    if (originalClipboardDescriptor) {
      Object.defineProperty(navigator, 'clipboard', originalClipboardDescriptor)
    } else {
      Reflect.deleteProperty(navigator, 'clipboard')
    }
  })

  it('shows a retryable error state when the share list fails to load', async () => {
    vi.mocked(shareApi.listShares).mockRejectedValue(new Error('share feature disabled'))

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('加载分享列表失败')).toBeInTheDocument()
      expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })

    expect(screen.queryByText('share feature disabled')).not.toBeInTheDocument()
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
      expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })
    expect(screen.queryByText('bad request')).not.toBeInTheDocument()
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

  it('opens the activity review for a share path', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.listShares).mockResolvedValueOnce(mockShares)

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: 'report.pdf 查看分享活动' }))

    expect(window.location.pathname).toBe('/activity')
    const params = new URLSearchParams(window.location.search)
    expect(params.get('action_group')).toBe('share')
    expect(params.get('path')).toBe('/docs/report.pdf')
  })

  it('filters shares by the linked disposition path', async () => {
    const user = userEvent.setup()
    const onClearPathFilter = vi.fn()
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([
      mockShares[0],
      {
        ...mockShares[0],
        id: 'share-other',
        path: '/photos/a.jpg',
      },
    ])

    render(<ShareManager pathFilter="/docs/report.pdf" onClearPathFilter={onClearPathFilter} />)

    await waitFor(() => {
      expect(screen.getByText('我的分享 (1 / 2)')).toBeInTheDocument()
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
      expect(screen.queryByText('a.jpg')).not.toBeInTheDocument()
    })

    const pathFilterRegion = within(screen.getByRole('region', { name: '分享路径筛选' }))
    expect(pathFilterRegion.getByText('路径：/docs/report.pdf')).toBeInTheDocument()

    await user.click(pathFilterRegion.getByRole('button', { name: '清除路径筛选' }))
    expect(onClearPathFilter).toHaveBeenCalledTimes(1)
  })

  it('applies the initial review filter within the linked path', async () => {
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([
      {
        ...mockShares[0],
        id: 'share-safe',
        path: '/docs/safe.pdf',
      },
      {
        ...mockShares[0],
        id: 'share-risk',
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
        id: 'share-other',
        path: '/photos/open.pdf',
        risk: {
          level: 'high',
          reasons: [
            { code: 'no_password', level: 'high', message: '未设置密码，拿到链接的人都能访问' },
          ],
        },
      },
    ])

    render(<ShareManager pathFilter="/docs" initialReviewFilter="review" />)

    await waitFor(() => {
      expect(screen.getByText('我的分享 (2 / 3)')).toBeInTheDocument()
      expect(screen.getByText('open.pdf')).toBeInTheDocument()
      expect(screen.queryByText('safe.pdf')).not.toBeInTheDocument()
      expect(screen.queryByText('/photos/open.pdf')).not.toBeInTheDocument()
      expect(screen.getByRole('button', { name: '需复核 (1)' })).toBeInTheDocument()
    })
  })

  it('renders a share as expired at the exact expiration time', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      vi.setSystemTime(new Date('2026-05-04T00:00:00Z'))
      vi.mocked(shareApi.listShares).mockResolvedValueOnce([
        {
          ...mockShares[0],
          id: 'share-exact-expiry',
          path: '/docs/exact.pdf',
          expires_at: '2026-05-04T00:00:00Z',
        },
      ])

      render(<ShareManager />)

      await waitFor(() => {
        expect(screen.getByText('exact.pdf')).toBeInTheDocument()
        expect(screen.getAllByText('已过期')).toHaveLength(2)
      })
    } finally {
      vi.useRealTimers()
    }
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
            { code: 'no_password', level: 'high', message: 'backend says link has no password' },
            { code: 'unlimited_access', level: 'medium', message: 'backend says access is unlimited' },
            { code: 'unknown_probe', level: 'low', message: 'backend raw fallback reason' },
          ],
        },
      },
    ])

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('风险 1')).toBeInTheDocument()
      expect(screen.getAllByText('需处理').length).toBeGreaterThanOrEqual(1)
      expect(screen.getByText('未设置密码，持有链接的人可直接访问。')).toBeInTheDocument()
      expect(screen.getByText('未设置访问次数上限。')).toBeInTheDocument()
      expect(screen.getByText('存在低风险分享配置，请检查分享设置。')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '需复核 (1)' })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '停用需处理 (1)' })).toBeInTheDocument()
    })

    expect(screen.queryByText('backend says link has no password')).not.toBeInTheDocument()
    expect(screen.queryByText('backend says access is unlimited')).not.toBeInTheDocument()
    expect(screen.queryByText('backend raw fallback reason')).not.toBeInTheDocument()
  })

  it('summarizes share review workload and filters from the summary', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([
      {
        ...mockShares[0],
        id: 'share-open',
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
      {
        ...mockShares[0],
        id: 'share-soon',
        path: '/docs/soon.pdf',
        risk: {
          level: 'low',
          reasons: [
            { code: 'expiring_soon', level: 'low', message: '分享即将到期，建议确认是否需要延长或关闭' },
          ],
        },
      },
      {
        ...mockShares[0],
        id: 'share-safe',
        path: '/docs/safe.pdf',
      },
    ])

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('我的分享 (4)')).toBeInTheDocument()
      expect(screen.getByText('open.pdf')).toBeInTheDocument()
      expect(screen.getByText('Photos')).toBeInTheDocument()
      expect(screen.getByText('soon.pdf')).toBeInTheDocument()
      expect(screen.getByText('safe.pdf')).toBeInTheDocument()
    })

    const summary = within(screen.getByRole('region', { name: '分享复核摘要' }))
    expect(summary.getByText('需处理')).toBeInTheDocument()
    expect(summary.getByText('优先处理无密码、覆盖范围较大或其他高风险分享。')).toBeInTheDocument()
    expect(summary.getByRole('button', { name: '筛选需复核分享 3' })).toBeInTheDocument()
    expect(summary.getByRole('button', { name: '筛选无密码分享 1' })).toBeInTheDocument()
    expect(summary.getByRole('button', { name: '筛选覆盖较大分享 1' })).toBeInTheDocument()
    expect(summary.getByRole('button', { name: '筛选即将到期分享 1' })).toBeInTheDocument()
    expect(summary.getByRole('button', { name: '筛选长期未访问分享 0' })).toBeDisabled()

    await user.click(summary.getByRole('button', { name: '筛选覆盖较大分享 1' }))

    await waitFor(() => {
      expect(screen.queryByText('open.pdf')).not.toBeInTheDocument()
      expect(screen.getByText('Photos')).toBeInTheDocument()
      expect(screen.queryByText('soon.pdf')).not.toBeInTheDocument()
      expect(screen.queryByText('safe.pdf')).not.toBeInTheDocument()
    })
  })

  it('opens path-scoped share review history from the summary', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([
      {
        ...mockShares[0],
        id: 'share-open',
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
        id: 'share-media',
        path: '/media/movie.mp4',
      },
    ])

    render(<ShareManager pathFilter="/docs" />)

    await waitFor(() => {
      expect(screen.getByText('我的分享 (1 / 2)')).toBeInTheDocument()
      expect(screen.getByText('open.pdf')).toBeInTheDocument()
      expect(screen.queryByText('movie.mp4')).not.toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '查看复核历史' }))

    expect(window.location.pathname).toBe('/activity')
    const params = new URLSearchParams(window.location.search)
    expect(params.get('action_group')).toBe('share')
    expect(params.get('path')).toBe('/docs')
  })

  it('copies a path-scoped share review summary for administrator review', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([
      {
        ...mockShares[0],
        id: 'share-open',
        path: '/docs/open.pdf',
        has_password: false,
        access_count: 5,
        max_access: 0,
        risk: {
          level: 'high',
          reasons: [
            { code: 'no_password', level: 'high', message: '未设置密码，拿到链接的人都能访问' },
          ],
        },
      },
      {
        ...mockShares[0],
        id: 'share-safe',
        path: '/docs/safe.pdf',
        has_password: true,
        access_count: 1,
        max_access: 10,
        risk: { level: 'none' },
      },
      {
        ...mockShares[0],
        id: 'share-media',
        path: '/media/movie.mp4',
        has_password: false,
        risk: {
          level: 'high',
          reasons: [
            { code: 'no_password', level: 'high', message: '未设置密码' },
          ],
        },
      },
    ])

    render(<ShareManager pathFilter="/docs" />)

    await waitFor(() => {
      expect(screen.getByText('我的分享 (2 / 3)')).toBeInTheDocument()
      expect(screen.getByText('open.pdf')).toBeInTheDocument()
      expect(screen.getByText('safe.pdf')).toBeInTheDocument()
      expect(screen.queryByText('movie.mp4')).not.toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '复制摘要' }))

    await waitFor(() => {
      expect(writeText).toHaveBeenCalledTimes(1)
    })
    const report = writeText.mock.calls[0]?.[0] as string
    expect(report).toContain('分享复核摘要')
    expect(report).toContain('分享总数：2 个')
    expect(report).toContain('启用分享：2 个')
    expect(report).toContain('需复核：1 个')
    expect(report).toContain('需处理：1 个')
    expect(report).toContain('路径筛选：/docs')
    expect(report).toContain('路径 | 类型 | 状态 | 风险等级 | 访问限制 | 访问次数 | 过期时间 | 风险原因 | 建议处理')
    expect(report).toContain('/docs/open.pdf | 文件 | 启用 | 高风险 | 无密码 | 5 / 不限 | 永不过期 | 未设置密码，拿到链接的人都能访问 | 停用或补齐密码、有效期和访问次数限制。')
    expect(report).toContain('/docs/safe.pdf | 文件 | 启用 | 无 | 密码保护 | 1 / 10 | 永不过期 | 无 | 无需处理。')
    expect(report).not.toContain('/media/movie.mp4')
    expect(mockAddToast).toHaveBeenCalledWith({ title: '分享复核摘要已复制', color: 'success' })
  })

  it('records a path-scoped share review into activity review history', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([
      {
        ...mockShares[0],
        id: 'share-open',
        path: '/docs/open.pdf',
        has_password: false,
        risk: {
          level: 'high',
          reasons: [
            { code: 'no_password', level: 'high', message: '未设置密码，拿到链接的人都能访问' },
          ],
        },
      },
      {
        ...mockShares[0],
        id: 'share-safe',
        path: '/docs/safe.pdf',
        has_password: true,
        risk: { level: 'none' },
      },
      {
        ...mockShares[0],
        id: 'share-media',
        path: '/media/movie.mp4',
        has_password: false,
        risk: {
          level: 'high',
          reasons: [
            { code: 'no_password', level: 'high', message: '未设置密码' },
          ],
        },
      },
    ])
    vi.mocked(activityApi.listActivity).mockResolvedValueOnce({
      items: [
        {
          id: 'act-share-1',
          timestamp: '2026-03-27T00:00:00Z',
          action: 'share',
          path: '/docs/open.pdf',
          user: 'alice',
        },
        {
          id: 'act-unshare-1',
          timestamp: '2026-03-27T01:00:00Z',
          action: 'unshare',
          path: '/docs/old.pdf',
          user: 'bob',
        },
      ],
      total: 3,
      limit: 100,
      offset: 0,
    })

    render(<ShareManager pathFilter="/docs" />)

    await waitFor(() => {
      expect(screen.getByText('我的分享 (2 / 3)')).toBeInTheDocument()
      expect(screen.getByText('open.pdf')).toBeInTheDocument()
      expect(screen.getByText('safe.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '记录复核' }))

    await waitFor(() => {
      expect(activityApi.createActivityReviewRecord).toHaveBeenCalledTimes(1)
    })
    expect(activityApi.listActivity).toHaveBeenCalledWith(expect.objectContaining({
      actionGroup: 'share',
      path: '/docs',
      limit: 100,
      offset: 0,
      signal: expect.any(AbortSignal),
    }))
    expect(activityApi.createActivityReviewRecord).toHaveBeenCalledWith(expect.objectContaining({
      note: '分享复核摘要：需复核 1 个，需处理 1 个，无密码 1 个，覆盖较大 0 个，即将到期 0 个，长期未访问 0 个。',
      scope_label: '分享路径 /docs',
      filter_summary: '审计分组 分享相关 · 路径 /docs · 当前分享 2/3',
      disposition_status: 'needs_follow_up',
      action_counts: {
        share: 1,
        unshare: 1,
      },
      review_count: 2,
      total_count: 3,
      path_count: 2,
      user_count: 2,
      path_samples: ['/docs/open.pdf', '/docs/old.pdf'],
      user_samples: ['alice', 'bob'],
      share_disposition_details: [{
        path: '/docs/open.pdf',
        type: 'file',
        enabled: true,
        risk_level: 'high',
        reason_summary: '未设置密码，拿到链接的人都能访问',
        suggested_action: '停用或补齐密码、有效期和访问次数限制。',
        access_summary: '无密码 · 访问 3/不限',
        expires_at: '永不过期',
      }],
      activity_entry_ids: ['act-share-1', 'act-unshare-1'],
    }), expect.objectContaining({
      signal: expect.any(AbortSignal),
    }))
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '分享复核已记录',
      description: '已关联最近 2 条分享活动；当前筛选共有 3 条分享活动。',
      color: 'success',
    })
  })

  it('does not record share review history without matching share activity', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.listShares).mockResolvedValueOnce(mockShares)
    vi.mocked(activityApi.listActivity).mockResolvedValueOnce({
      items: [],
      total: 0,
      limit: 100,
      offset: 0,
    })

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('我的分享 (1)')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '记录复核' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '没有可关联的分享活动',
        description: '活动日志中没有找到当前范围的分享或取消分享记录。',
        color: 'warning',
      })
    })
    expect(activityApi.createActivityReviewRecord).not.toHaveBeenCalled()
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

  it('filters the share list to stale enabled shares', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([
      {
        ...mockShares[0],
        id: 'share-active',
        path: '/docs/active.pdf',
      },
      {
        ...mockShares[0],
        id: 'share-stale',
        path: '/docs/stale.pdf',
        risk: {
          level: 'medium',
          reasons: [
            { code: 'stale_enabled', level: 'medium', message: '分享长期未访问，建议关闭或重新确认用途' },
          ],
        },
      },
    ])

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('active.pdf')).toBeInTheDocument()
      expect(screen.getByText('stale.pdf')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '长期未访问 (1)' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '长期未访问 (1)' }))

    await waitFor(() => {
      expect(screen.queryByText('active.pdf')).not.toBeInTheDocument()
      expect(screen.getByText('stale.pdf')).toBeInTheDocument()
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
      warning: false,
      message: undefined,
    })

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '停用需处理 (1)' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '停用需处理 (1)' }))

    await waitFor(() => {
      expectUpdateShareCalledWithAbortSignal('share-1', { enabled: false })
      expect(mockAddToast).toHaveBeenCalledWith({ title: '已停用 1 个需处理分享', color: 'success' })
      expect(screen.getByText('已禁用')).toBeInTheDocument()
      expect(screen.queryByText('需处理')).not.toBeInTheDocument()
    })
  })

  it('records high-risk share disable execution results into activity review history', async () => {
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
      warning: false,
      message: undefined,
    })
    vi.mocked(activityApi.listActivity).mockResolvedValueOnce({
      items: [{
        id: 'act-unshare-1',
        timestamp: '2026-03-27T01:00:00Z',
        action: 'unshare',
        path: '/docs/report.pdf',
        user: 'admin',
      }],
      total: 1,
      limit: 100,
      offset: 0,
    })

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '停用需处理 (1)' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '停用需处理 (1)' }))

    await waitFor(() => {
      expect(activityApi.createActivityReviewRecord).toHaveBeenCalledTimes(1)
    })
    expect(activityApi.listActivity).toHaveBeenCalledWith(expect.objectContaining({
      actionGroup: 'share',
      limit: 100,
      offset: 0,
      signal: expect.any(AbortSignal),
    }))
    expect(activityApi.createActivityReviewRecord).toHaveBeenCalledWith(expect.objectContaining({
      note: '分享执行结果：已停用 1 个需处理分享；已关联 1 条分享活动。',
      scope_label: '分享管理',
      filter_summary: '审计分组 分享相关 · 当前分享 1/1 · 执行结果 停用需处理分享',
      disposition_status: 'disabled',
      action_counts: { unshare: 1 },
      review_count: 1,
      total_count: 1,
      path_count: 1,
      user_count: 1,
      path_samples: ['/docs/report.pdf'],
      user_samples: ['admin'],
      share_disposition_details: [{
        path: '/docs/report.pdf',
        type: 'file',
        enabled: false,
        risk_level: 'high',
        reason_summary: '未设置密码，拿到链接的人都能访问',
        suggested_action: '已停用高风险分享；继续复核外部引用和访问入口。',
        access_summary: '无密码 · 访问 3/不限',
        expires_at: '永不过期',
      }],
      activity_entry_ids: ['act-unshare-1'],
    }), expect.objectContaining({
      signal: expect.any(AbortSignal),
    }))
    expect(mockAddToast).toHaveBeenCalledWith({ title: '分享停用结果已记录', color: 'success' })
  })

  it('shows a warning toast when high-risk shares are disabled with persistence warnings', async () => {
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
      warning: true,
      message: 'share updated with persistence warning',
    })

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '停用需处理 (1)' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '停用需处理 (1)' }))

    await waitFor(() => {
      expectUpdateShareCalledWithAbortSignal('share-1', { enabled: false })
      expect(mockAddToast).toHaveBeenCalledWith({ title: '已停用 1 个需处理分享，但存在警告', color: 'warning' })
      expect(screen.getByText('已禁用')).toBeInTheDocument()
    })
  })

  it('aborts pending high-risk disable requests when the manager unmounts', async () => {
    const user = userEvent.setup()
    const pendingUpdate = createDeferred<Awaited<ReturnType<typeof shareApi.updateShare>>>()
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
    vi.mocked(shareApi.updateShare).mockImplementationOnce(() => pendingUpdate.promise as ReturnType<typeof shareApi.updateShare>)

    const { unmount } = render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '停用需处理 (1)' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '停用需处理 (1)' }))

    const signal = expectUpdateShareCalledWithAbortSignal('share-1', { enabled: false })
    expect(signal.aborted).toBe(false)

    unmount()

    expect(signal.aborted).toBe(true)
  })

  it('ignores stale share loads after the feature is disabled', async () => {
    const pendingLoad = createDeferred<typeof mockShares>()
    vi.mocked(shareApi.listShares).mockReturnValueOnce(pendingLoad.promise)

    const { rerender } = render(<ShareManager featureEnabled={true} />)

    const loadSignal = vi.mocked(shareApi.listShares).mock.calls[0]?.[1]?.signal
    expect(loadSignal).toBeInstanceOf(AbortSignal)

    rerender(<ShareManager featureEnabled={false} />)

    expect(loadSignal?.aborted).toBe(true)
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
    const loadSignal = vi.mocked(shareApi.listShares).mock.calls[0]?.[1]?.signal
    expect(loadSignal).toBeInstanceOf(AbortSignal)

    unmount()
    expect(loadSignal?.aborted).toBe(true)
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
        description: '数据加载失败，请检查网络或稍后重试。',
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
    vi.mocked(shareApi.updateShare).mockResolvedValueOnce({
      ...mockShares[0],
      enabled: false,
      warning: false,
      message: undefined,
    })

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('禁用分享'))

    await waitFor(() => {
      expectUpdateShareCalledWithAbortSignal('share-1', { enabled: false })
      expect(mockAddToast).toHaveBeenCalledWith({ title: '分享已禁用', color: 'success' })
      expect(screen.getByText('已禁用')).toBeInTheDocument()
    })
  })

  it('records single share disable execution results into activity review history', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.updateShare).mockResolvedValueOnce({
      ...mockShares[0],
      enabled: false,
      warning: false,
      message: undefined,
    })
    vi.mocked(activityApi.listActivity).mockResolvedValueOnce({
      items: [{
        id: 'act-unshare-single-1',
        timestamp: '2026-03-27T01:00:00Z',
        action: 'unshare',
        path: '/docs/report.pdf',
        user: 'admin',
      }],
      total: 1,
      limit: 100,
      offset: 0,
    })

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('禁用分享'))

    await waitFor(() => {
      expect(activityApi.createActivityReviewRecord).toHaveBeenCalledTimes(1)
    })
    expect(activityApi.listActivity).toHaveBeenCalledWith(expect.objectContaining({
      actionGroup: 'share',
      path: '/docs/report.pdf',
      limit: 100,
      offset: 0,
      signal: expect.any(AbortSignal),
    }))
    expect(activityApi.createActivityReviewRecord).toHaveBeenCalledWith(expect.objectContaining({
      note: '分享执行结果：已停用 1 个分享；已关联 1 条分享活动。',
      scope_label: '分享 /docs/report.pdf',
      filter_summary: '审计分组 分享相关 · 路径 /docs/report.pdf · 当前分享 1/1 · 执行结果 停用分享',
      disposition_status: 'disabled',
      action_counts: { unshare: 1 },
      review_count: 1,
      total_count: 1,
      path_count: 1,
      user_count: 1,
      path_samples: ['/docs/report.pdf'],
      user_samples: ['admin'],
      share_disposition_details: [{
        path: '/docs/report.pdf',
        type: 'file',
        enabled: false,
        risk_level: 'none',
        reason_summary: '无',
        suggested_action: '已停用该分享；继续复核外部引用和访问入口。',
        access_summary: '无密码 · 访问 3/不限',
        expires_at: '永不过期',
      }],
      activity_entry_ids: ['act-unshare-single-1'],
    }), expect.objectContaining({
      signal: expect.any(AbortSignal),
    }))
    expect(mockAddToast).toHaveBeenCalledWith({ title: '分享已禁用', color: 'success' })
    expect(mockAddToast).toHaveBeenCalledWith({ title: '分享停用结果已记录', color: 'success' })
  })

  it('shows a warning toast when disabling a share succeeds with persistence warnings', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.updateShare).mockResolvedValueOnce({
      ...mockShares[0],
      enabled: false,
      warning: true,
      message: 'share updated with persistence warning',
    })

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('禁用分享'))

    await waitFor(() => {
      expectUpdateShareCalledWithAbortSignal('share-1', { enabled: false })
      expect(mockAddToast).toHaveBeenCalledWith({ title: '分享已禁用，但存在警告', color: 'warning' })
      expect(screen.getByText('已禁用')).toBeInTheDocument()
    })
  })

  it('aborts a pending share toggle when the manager unmounts', async () => {
    const user = userEvent.setup()
    const pendingUpdate = createDeferred<Awaited<ReturnType<typeof shareApi.updateShare>>>()
    vi.mocked(shareApi.updateShare).mockImplementationOnce(() => pendingUpdate.promise as ReturnType<typeof shareApi.updateShare>)

    const { unmount } = render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('禁用分享'))

    const signal = expectUpdateShareCalledWithAbortSignal('share-1', { enabled: false })
    expect(signal.aborted).toBe(false)

    unmount()

    expect(signal.aborted).toBe(true)
  })

  it('enables a disabled share successfully', async () => {
    const user = userEvent.setup()
    const disabledShare = { ...mockShares[0], enabled: false }
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([disabledShare])
    vi.mocked(shareApi.updateShare).mockResolvedValueOnce({
      ...disabledShare,
      enabled: true,
      warning: false,
      message: undefined,
    })

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('已禁用')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('启用分享'))

    await waitFor(() => {
      expectUpdateShareCalledWithAbortSignal('share-1', { enabled: true })
      expect(mockAddToast).toHaveBeenCalledWith({ title: '分享已启用', color: 'success' })
      expect(screen.queryByText('已禁用')).not.toBeInTheDocument()
    })
  })

  it('records single share enable execution results into activity review history', async () => {
    const user = userEvent.setup()
    const disabledShare = { ...mockShares[0], enabled: false, risk: { level: 'none' as const } }
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([disabledShare])
    vi.mocked(shareApi.updateShare).mockResolvedValueOnce({
      ...disabledShare,
      enabled: true,
      risk: {
        level: 'medium',
        reasons: [
          { code: 'no_expiration', level: 'medium', message: '未设置过期时间，链接会长期有效' },
        ],
      },
      warning: false,
      message: undefined,
    })
    vi.mocked(activityApi.listActivity).mockResolvedValueOnce({
      items: [{
        id: 'act-share-enable-1',
        timestamp: '2026-03-27T01:00:00Z',
        action: 'share',
        path: '/docs/report.pdf',
        user: 'admin',
      }],
      total: 1,
      limit: 100,
      offset: 0,
    })

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('已禁用')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('启用分享'))

    await waitFor(() => {
      expect(activityApi.createActivityReviewRecord).toHaveBeenCalledTimes(1)
    })
    expect(activityApi.listActivity).toHaveBeenCalledWith(expect.objectContaining({
      actionGroup: 'share',
      path: '/docs/report.pdf',
      limit: 100,
      offset: 0,
      signal: expect.any(AbortSignal),
    }))
    expect(activityApi.createActivityReviewRecord).toHaveBeenCalledWith(expect.objectContaining({
      note: '分享执行结果：已启用 1 个分享；已关联 1 条分享活动。',
      scope_label: '分享 /docs/report.pdf',
      filter_summary: '审计分组 分享相关 · 路径 /docs/report.pdf · 当前分享 1/1 · 执行结果 启用分享',
      disposition_status: 'confirmed',
      action_counts: { share: 1 },
      review_count: 1,
      total_count: 1,
      path_count: 1,
      user_count: 1,
      path_samples: ['/docs/report.pdf'],
      user_samples: ['admin'],
      share_disposition_details: [{
        path: '/docs/report.pdf',
        type: 'file',
        enabled: true,
        risk_level: 'medium',
        reason_summary: '未设置过期时间，链接会长期有效',
        suggested_action: '已重新启用该分享；继续复核有效期、密码、访问次数和外部引用。',
        access_summary: '无密码 · 访问 3/不限',
        expires_at: '永不过期',
      }],
      activity_entry_ids: ['act-share-enable-1'],
    }), expect.objectContaining({
      signal: expect.any(AbortSignal),
    }))
    expect(mockAddToast).toHaveBeenCalledWith({ title: '分享已启用', color: 'success' })
    expect(mockAddToast).toHaveBeenCalledWith({ title: '分享启用结果已记录', color: 'success' })
    expect(screen.queryByText('已禁用')).not.toBeInTheDocument()
  })

  it('shows a warning toast when enabling a share succeeds with persistence warnings', async () => {
    const user = userEvent.setup()
    const disabledShare = { ...mockShares[0], enabled: false }
    vi.mocked(shareApi.listShares).mockResolvedValueOnce([disabledShare])
    vi.mocked(shareApi.updateShare).mockResolvedValueOnce({
      ...disabledShare,
      enabled: true,
      warning: true,
      message: 'share updated with persistence warning',
    })

    render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('已禁用')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('启用分享'))

    await waitFor(() => {
      expectUpdateShareCalledWithAbortSignal('share-1', { enabled: true })
      expect(mockAddToast).toHaveBeenCalledWith({ title: '分享已启用，但存在警告', color: 'warning' })
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
        description: '操作未完成，请稍后重试。',
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
      expectDeleteShareCalledWithAbortSignal('share-1')
      expect(mockAddToast).toHaveBeenCalledWith({ title: '分享已删除', color: 'success' })
      expect(screen.queryByText('report.pdf')).not.toBeInTheDocument()
    })
  })

  it('records share deletion execution results into activity review history', async () => {
    const user = userEvent.setup()
    vi.mocked(shareApi.deleteShare).mockResolvedValueOnce(successActionResult)
    vi.mocked(activityApi.listActivity).mockResolvedValueOnce({
      items: [{
        id: 'act-unshare-delete-1',
        timestamp: '2026-03-27T01:00:00Z',
        action: 'unshare',
        path: '/docs/report.pdf',
        user: 'admin',
      }],
      total: 1,
      limit: 100,
      offset: 0,
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
      expect(activityApi.createActivityReviewRecord).toHaveBeenCalledTimes(1)
    })
    expect(activityApi.listActivity).toHaveBeenCalledWith(expect.objectContaining({
      actionGroup: 'share',
      path: '/docs/report.pdf',
      limit: 100,
      offset: 0,
      signal: expect.any(AbortSignal),
    }))
    expect(activityApi.createActivityReviewRecord).toHaveBeenCalledWith(expect.objectContaining({
      note: '分享执行结果：已删除 1 个分享；已关联 1 条分享活动。',
      scope_label: '分享 /docs/report.pdf',
      filter_summary: '审计分组 分享相关 · 路径 /docs/report.pdf · 当前分享 1/1 · 执行结果 删除分享',
      disposition_status: 'disabled',
      action_counts: { unshare: 1 },
      review_count: 1,
      total_count: 1,
      path_count: 1,
      user_count: 1,
      path_samples: ['/docs/report.pdf'],
      user_samples: ['admin'],
      share_disposition_details: [{
        path: '/docs/report.pdf',
        type: 'file',
        enabled: false,
        risk_level: 'none',
        reason_summary: '无',
        suggested_action: '已删除该分享；继续复核外部引用和访问入口。',
        access_summary: '无密码 · 访问 3/不限',
        expires_at: '永不过期',
      }],
      activity_entry_ids: ['act-unshare-delete-1'],
    }), expect.objectContaining({
      signal: expect.any(AbortSignal),
    }))
    expect(mockAddToast).toHaveBeenCalledWith({ title: '分享已删除', color: 'success' })
    expect(mockAddToast).toHaveBeenCalledWith({ title: '分享删除结果已记录', color: 'success' })
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
      expectDeleteShareCalledWithAbortSignal('share-1')
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

  it('aborts a pending delete when the manager unmounts', async () => {
    const user = userEvent.setup()
    const pendingDelete = createDeferred<typeof successActionResult>()
    vi.mocked(shareApi.deleteShare).mockImplementationOnce(() => pendingDelete.promise)

    const { unmount } = render(<ShareManager />)

    await waitFor(() => {
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })

    await user.click(screen.getByLabelText('report.pdf 分享操作'))
    await user.click(await screen.findByText('删除分享'))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '删除' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '删除' }))

    const signal = expectDeleteShareCalledWithAbortSignal('share-1')
    expect(signal.aborted).toBe(false)

    unmount()

    expect(signal.aborted).toBe(true)
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
      expectDeleteShareCalledWithAbortSignal('share-1')
    })

    await user.click(screen.getByRole('button', { name: '取消' }))

    expect(screen.getByRole('heading', { name: '删除分享' })).toBeInTheDocument()
    expect(screen.getByText('/docs/report.pdf', { selector: 'span' })).toBeInTheDocument()

    firstDelete.reject(new Error('delete failed'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '删除失败',
        description: '操作未完成，请稍后重试。',
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
        title: '分享已删除，但存在警告',
        color: 'warning',
      })
    })

    expect(screen.queryByText('report.pdf')).not.toBeInTheDocument()
  })
})
