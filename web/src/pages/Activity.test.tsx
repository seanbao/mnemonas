import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { ReactNode } from 'react'
import { render, screen, waitFor, within } from '@/test/utils'
import { fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ActivityPage } from './Activity'
import * as HeroUI from '@heroui/react'
import { triggerBrowserDownload } from '@/lib/downloadResponse'

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
    Input: ({
      'aria-label': ariaLabel,
      placeholder,
      value,
      onValueChange,
      onClear,
      isClearable,
      isInvalid,
      errorMessage,
    }: {
      'aria-label'?: string
      placeholder?: string
      value?: string
      onValueChange?: (value: string) => void
      onClear?: () => void
      isClearable?: boolean
      isInvalid?: boolean
      errorMessage?: ReactNode
    }) => (
      <div>
        <input
          aria-label={ariaLabel}
          aria-invalid={isInvalid ? 'true' : undefined}
          placeholder={placeholder}
          value={value}
          onChange={(event) => onValueChange?.(event.target.value)}
        />
        {isInvalid && errorMessage ? <span>{errorMessage}</span> : null}
        {isClearable && placeholder === '按路径筛选' ? <button onClick={onClear}>清除路径筛选</button> : null}
        {isClearable && placeholder === '按用户筛选' ? <button onClick={onClear}>清除用户筛选</button> : null}
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
    }) => {
      if (placeholder === '时间范围') {
        return (
          <div>
            <span>{placeholder}</span>
            <button onClick={() => onSelectionChange?.(new Set(['today']))}>筛选今天</button>
            <button onClick={() => onSelectionChange?.(new Set(['7d']))}>筛选近 7 天</button>
            <button onClick={() => onSelectionChange?.(new Set(['all']))}>筛选全部时间</button>
            <div>{children}</div>
          </div>
        )
      }

      if (placeholder === '复核时间') {
        return (
          <div>
            <span>{placeholder}</span>
            <button onClick={() => onSelectionChange?.(new Set(['7d']))}>筛选复核近 7 天</button>
            <button onClick={() => onSelectionChange?.(new Set(['all']))}>筛选复核全部时间</button>
            <div>{children}</div>
          </div>
        )
      }

      if (placeholder === '处置状态') {
        return (
          <div>
            <span>{placeholder}</span>
            <button onClick={() => onSelectionChange?.(new Set(['restored']))}>选择已恢复</button>
            <button onClick={() => onSelectionChange?.(new Set(['needs_follow_up']))}>选择需跟进</button>
            <div>{children}</div>
          </div>
        )
      }

      if (placeholder === '历史处置') {
        return (
          <div>
            <span>{placeholder}</span>
            <button onClick={() => onSelectionChange?.(new Set(['needs_follow_up']))}>筛选复核需跟进</button>
            <button onClick={() => onSelectionChange?.(new Set(['all']))}>筛选复核全部处置</button>
            <div>{children}</div>
          </div>
        )
      }

      if (placeholder === '复核类型') {
        return (
          <div>
            <span>{placeholder}</span>
            <button onClick={() => onSelectionChange?.(new Set(['share']))}>筛选分享复核</button>
            <button onClick={() => onSelectionChange?.(new Set(['risk']))}>筛选风险复核</button>
            <button onClick={() => onSelectionChange?.(new Set(['all']))}>筛选全部复核类型</button>
            <div>{children}</div>
          </div>
        )
      }

      if (placeholder === '审计分组') {
        return (
          <div>
            <span>{placeholder}</span>
            <button onClick={() => onSelectionChange?.(new Set(['risk']))}>筛选高风险变更</button>
            <button onClick={() => onSelectionChange?.(new Set(['share']))}>筛选分享相关</button>
            <button onClick={() => onSelectionChange?.(new Set())}>清空分组筛选</button>
            <div>{children}</div>
          </div>
        )
      }

      return (
        <div>
          <span>{placeholder}</span>
          <button onClick={() => onSelectionChange?.(new Set(['delete']))}>筛选删除文件</button>
          <button onClick={() => onSelectionChange?.(new Set(['scrub']))}>筛选数据校验</button>
          <button onClick={() => onSelectionChange?.(new Set())}>清空筛选</button>
          <div>{children}</div>
        </div>
      )
    },
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
  ACTIVITY_ACTION_GROUPS: ['risk', 'share'],
  ACTIVITY_REVIEW_DISPOSITION_STATUSES: ['documented', 'confirmed', 'restored', 'disabled', 'needs_follow_up'],
  listActivity: vi.fn(),
  listActivityReviewRecords: vi.fn(),
  createActivityReviewRecord: vi.fn(),
  updateActivityReviewRecordDisposition: vi.fn(),
  clearActivity: vi.fn(),
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
      rename: '重命名',
      move: '移动文件',
      copy: '复制文件',
      create: '创建文件夹',
      share: '创建分享',
      unshare: '取消分享',
      favorite: '添加收藏',
      unfavorite: '取消收藏',
      favorite_note_update: '更新收藏备注',
      restore: '恢复版本',
      login: '登录',
      logout: '登出',
      trash_restore: '从回收站恢复',
      trash_delete: '永久删除回收站项目',
      trash_empty: '清空回收站',
      disk_health: '磁盘健康异常',
      scrub: '数据校验',
    }
    return labels[action] || action
  }),
  getActionColor: vi.fn((action: string) => {
    const colors: Record<string, string> = {
      upload: 'success',
      delete: 'danger',
      download: 'warning',
      rename: 'warning',
      move: 'warning',
      copy: 'primary',
      create: 'success',
      share: 'primary',
      unshare: 'warning',
      favorite: 'primary',
      unfavorite: 'warning',
      favorite_note_update: 'primary',
      restore: 'success',
      login: 'default',
      logout: 'default',
      trash_restore: 'success',
      trash_delete: 'danger',
      trash_empty: 'danger',
      disk_health: 'warning',
      scrub: 'warning',
    }
    return colors[action] || 'primary'
  }),
}))

vi.mock('@/lib/downloadResponse', () => ({
  triggerBrowserDownload: vi.fn(),
}))

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useIsAdmin: () => useIsAdminMock(),
    useUser: () => useUserMock(),
  }
})

import { ApiError, clearActivity, createActivityReviewRecord, getActivityStats, listActivity, listActivityReviewRecords, updateActivityReviewRecordDisposition } from '@/api/activity'

const mockListActivity = listActivity as ReturnType<typeof vi.fn>
const mockListActivityReviewRecords = listActivityReviewRecords as ReturnType<typeof vi.fn>
const mockCreateActivityReviewRecord = createActivityReviewRecord as ReturnType<typeof vi.fn>
const mockUpdateActivityReviewRecordDisposition = updateActivityReviewRecordDisposition as ReturnType<typeof vi.fn>
const mockClearActivity = clearActivity as ReturnType<typeof vi.fn>
const mockGetActivityStats = getActivityStats as ReturnType<typeof vi.fn>
const mockTriggerBrowserDownload = triggerBrowserDownload as ReturnType<typeof vi.fn>

function matchesOptionalString(actual: unknown, expected: string | RegExp | undefined): boolean {
  if (expected instanceof RegExp) {
    return typeof actual === 'string' && expected.test(actual)
  }

  return actual === expected
}

function expectListActivityCalledWith(options: {
  limit?: number
  offset?: number
  action?: string
  actionGroup?: string
  path?: string
  user?: string
  since?: string | RegExp
  until?: string | RegExp
}) {
  expect(mockListActivity.mock.calls.some(([calledOptions]) => (
    calledOptions?.limit === options.limit
    && calledOptions?.offset === options.offset
    && calledOptions?.action === options.action
    && calledOptions?.actionGroup === options.actionGroup
    && calledOptions?.path === options.path
    && calledOptions?.user === options.user
    && matchesOptionalString(calledOptions?.since, options.since)
    && matchesOptionalString(calledOptions?.until, options.until)
  ))).toBe(true)
}

function expectListActivityCalledWithSignal(options: {
  limit?: number
  offset?: number
  action?: string
  actionGroup?: string
  path?: string
  user?: string
  since?: string | RegExp
  until?: string | RegExp
}) {
  const call = mockListActivity.mock.calls.find(([calledOptions]) => (
    calledOptions?.limit === options.limit
    && calledOptions?.offset === options.offset
    && calledOptions?.action === options.action
    && calledOptions?.actionGroup === options.actionGroup
    && calledOptions?.path === options.path
    && calledOptions?.user === options.user
    && matchesOptionalString(calledOptions?.since, options.since)
    && matchesOptionalString(calledOptions?.until, options.until)
  ))
  expect(call).toBeTruthy()
  expect((call?.[0] as { signal?: AbortSignal } | undefined)?.signal).toBeInstanceOf(AbortSignal)
}

function expectGetActivityStatsCalledWithSignal(options: {
  action?: string
  actionGroup?: string
  path?: string
  user?: string
  since?: string | RegExp
  until?: string | RegExp
} = {}) {
  const call = mockGetActivityStats.mock.calls.find(([calledOptions]) => (
    (calledOptions as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
    && (calledOptions as { action?: string } | undefined)?.action === options.action
    && (calledOptions as { actionGroup?: string } | undefined)?.actionGroup === options.actionGroup
    && (calledOptions as { path?: string } | undefined)?.path === options.path
    && (calledOptions as { user?: string } | undefined)?.user === options.user
    && matchesOptionalString((calledOptions as { since?: string } | undefined)?.since, options.since)
    && matchesOptionalString((calledOptions as { until?: string } | undefined)?.until, options.until)
  ))
  expect(call).toBeTruthy()
}

function expectListActivityReviewRecordsCalledWith(options: {
  limit?: number
  offset?: number
  reviewer?: string
  activityEntryId?: string
  dispositionStatus?: string
  actionGroup?: string
  since?: string | RegExp
}) {
  expect(mockListActivityReviewRecords.mock.calls.some(([calledOptions]) => (
    (calledOptions as { limit?: number } | undefined)?.limit === options.limit
    && (calledOptions as { offset?: number } | undefined)?.offset === options.offset
    && (calledOptions as { reviewer?: string } | undefined)?.reviewer === options.reviewer
    && (calledOptions as { activityEntryId?: string } | undefined)?.activityEntryId === options.activityEntryId
    && (calledOptions as { dispositionStatus?: string } | undefined)?.dispositionStatus === options.dispositionStatus
    && (calledOptions as { actionGroup?: string } | undefined)?.actionGroup === options.actionGroup
    && matchesOptionalString((calledOptions as { since?: string } | undefined)?.since, options.since)
  ))).toBe(true)
}

function getClearActivitySignal(): AbortSignal {
  const call = mockClearActivity.mock.calls.find(([calledOptions]) => (
    (calledOptions as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
  ))
  expect(call).toBeTruthy()
  const signal = (call?.[0] as { signal?: AbortSignal } | undefined)?.signal
  expect(signal).toBeInstanceOf(AbortSignal)
  return signal as AbortSignal
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

describe('ActivityPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    window.history.pushState({}, '', '/')
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
    mockGetActivityStats.mockResolvedValue({
      total: 12,
      today: 3,
      by_action: {
        upload: 7,
        delete: 2,
        login: 3,
      },
      by_user: {
        admin: 8,
        user1: 4,
      },
      risk_summary: {
        total: 3,
        today: 2,
        max_10m: 2,
      },
    })
    mockListActivityReviewRecords.mockResolvedValue({
      items: [],
      total: 0,
      limit: 5,
      offset: 0,
    })
    mockCreateActivityReviewRecord.mockImplementation(async (input) => ({
      id: 'review-record-1',
      reviewed_at: new Date(Date.now() - 1000).toISOString(),
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
    mockUpdateActivityReviewRecordDisposition.mockImplementation(async (id, input) => ({
      id,
      reviewed_at: new Date(Date.now() - 500).toISOString(),
      reviewer: 'admin',
      note: input.note ?? '复核记录已更新',
      scope_label: '全部记录',
      filter_summary: '未筛选',
      disposition_status: input.disposition_status,
      review_count: 1,
      total_count: 1,
      path_count: 1,
      user_count: 1,
      path_samples: ['/docs/report.pdf'],
      user_samples: ['owner'],
      activity_entry_ids: ['share-1'],
    }))
    mockClearActivity.mockResolvedValue({ warning: false, message: '最近操作已清空' })
  })

  describe('rendering', () => {
    it('shows loading state initially', () => {
      mockListActivity.mockImplementation(() => new Promise(() => {}))
      render(<ActivityPage />)
      
      expect(screen.getByRole('status', { name: '加载最近操作' })).toBeInTheDocument()
      expect(screen.getByText('加载最近操作...')).toBeInTheDocument()
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
      expect(mockGetActivityStats).not.toHaveBeenCalled()
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

    it('shows activity stats overview', async () => {
      render(<ActivityPage />)

      await waitFor(() => {
        const totalCard = screen.getByRole('group', { name: '累计操作，12，历史记录总量' })
        const todayCard = screen.getByRole('group', { name: '今日操作，3，当天新增记录' })
        const topActionCard = screen.getByRole('group', { name: '最常见操作，上传文件，7 次' })
        const topUserCard = screen.getByRole('group', { name: '最活跃用户，admin，8 次' })

        expect(within(totalCard).getByText('12')).toBeTruthy()
        expect(within(todayCard).getByText('3')).toBeTruthy()
        expect(within(topActionCard).getByText('上传文件')).toBeTruthy()
        expect(within(topActionCard).getByText('7 次')).toBeTruthy()
        expect(within(topUserCard).getByText('admin')).toBeTruthy()
        expect(within(topUserCard).getByText('8 次')).toBeTruthy()
        expect(screen.getByText('高风险摘要')).toBeTruthy()
        expect(screen.getByText('未发现明显集中批量变更')).toBeTruthy()
        expect(screen.getByText('10 分钟最多')).toBeTruthy()
      })
    })

    it('keeps the activity list usable when stats loading fails', async () => {
      mockGetActivityStats.mockRejectedValue(new Error('stats unavailable'))

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('统计暂不可用')).toBeTruthy()
        expect(screen.getByText('/documents/report.pdf')).toBeTruthy()
      })
    })

    it('does not expose raw unknown action keys in the stats overview', async () => {
      mockGetActivityStats.mockResolvedValue({
        total: 1,
        today: 1,
        by_action: {
          unknown_action: 1,
        },
        by_user: {
          admin: 1,
        },
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('未知操作')).toBeTruthy()
        expect(screen.queryByText('unknown_action')).toBeNull()
      })
    })

    it('highlights concentrated risky activity in the summary', async () => {
      mockGetActivityStats.mockResolvedValue({
        total: 12,
        today: 6,
        by_action: {
          delete: 5,
          move: 1,
        },
        by_user: {
          admin: 6,
        },
        risk_summary: {
          total: 6,
          today: 6,
          max_10m: 5,
          max_10m_started_at: '2026-05-01T10:00:00Z',
          max_10m_ended_at: '2026-05-01T10:08:00Z',
        },
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('发现 10 分钟内集中高风险变更')).toBeTruthy()
        expect(screen.getAllByText('高风险变更').length).toBeGreaterThan(0)
        expect(screen.getByText('今日高风险')).toBeTruthy()
        expect(screen.getByText('10 分钟最多')).toBeTruthy()
      })
    })

    it('filters to the concentrated risky activity window from the summary', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetActivityStats.mockResolvedValue({
        total: 12,
        today: 6,
        by_action: {
          delete: 5,
          move: 1,
        },
        by_user: {
          admin: 6,
        },
        risk_summary: {
          total: 6,
          today: 6,
          max_10m: 5,
          max_10m_started_at: '2026-05-01T10:00:00Z',
          max_10m_ended_at: '2026-05-01T10:08:00Z',
        },
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('查看集中窗口')).toBeTruthy()
      })

      mockListActivity.mockClear()
      mockGetActivityStats.mockClear()
      await user.click(screen.getByText('查看集中窗口'))

      await waitFor(() => {
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          action: undefined,
          actionGroup: 'risk',
          since: '2026-05-01T10:00:00Z',
          until: '2026-05-01T10:08:00Z',
        })
        expectGetActivityStatsCalledWithSignal({
          action: undefined,
          actionGroup: 'risk',
          since: '2026-05-01T10:00:00Z',
          until: '2026-05-01T10:08:00Z',
        })
        expect(screen.getAllByText(/窗口：/).length).toBeGreaterThan(0)
        expect(screen.getByText('分组：高风险变更')).toBeTruthy()
        expect(screen.getByText('当前筛选结果')).toBeTruthy()
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

    it('shows review details for review-worthy current page activity', async () => {
      mockListActivity.mockResolvedValue({
        items: [
          {
            id: 'delete-1',
            timestamp: new Date(Date.now() - 1000 * 60).toISOString(),
            action: 'delete',
            path: '/old-file.txt',
            user: 'user1',
            details: {
              cleanup_warning: 'true',
            },
          },
          {
            id: 'move-1',
            timestamp: new Date(Date.now() - 1000 * 120).toISOString(),
            action: 'move',
            path: '/documents/report.pdf',
            user: 'admin',
            details: {
              to: '/archive/report.pdf',
            },
          },
          {
            id: 'share-1',
            timestamp: new Date(Date.now() - 1000 * 180).toISOString(),
            action: 'share',
            path: '/documents/report.pdf',
            user: 'admin',
            details: {
              has_password: 'true',
              max_access: '2',
            },
          },
          {
            id: 'login-1',
            timestamp: new Date(Date.now() - 1000 * 240).toISOString(),
            action: 'login',
            user: 'admin',
          },
        ],
        total: 4,
        limit: 20,
        offset: 0,
      })

      render(<ActivityPage />)

      await waitFor(() => {
        const review = within(screen.getByLabelText('当前页复核明细'))
        expect(review.getByText('当前页复核明细')).toBeTruthy()
        expect(review.getByText('当前页包含 3 条需复核记录')).toBeTruthy()
        expect(review.getByText('路径：/old-file.txt')).toBeTruthy()
        expect(review.getByText('复核：清理状态：残留数据清理不完整，请检查存储状态。')).toBeTruthy()
        expect(review.getAllByText('路径：/documents/report.pdf')).toHaveLength(2)
        expect(review.getByText('复核：目标路径：/archive/report.pdf')).toBeTruthy()
        expect(review.getByText('复核：密码保护：是')).toBeTruthy()
        expect(review.queryByText('登录')).toBeNull()
        const checklist = within(screen.getByLabelText('复核处置清单'))
        expect(checklist.getByText('确认影响范围：3 条记录，涉及 2 个路径、2 个用户。')).toBeTruthy()
        expect(checklist.getByText('删除类操作：检查回收站、版本历史和最近备份，确认是否需要恢复。')).toBeTruthy()
        expect(checklist.getByText('路径变更：核对来源和目标路径，确认移动或重命名是否符合预期。')).toBeTruthy()
        expect(checklist.getByText('分享变更：核对分享链接、密码、有效期和访问次数，关闭不再需要的公开链接。')).toBeTruthy()
        expect(checklist.getByText('带警告记录：先处理持久化或清理警告，再把本次复核标记为完成。')).toBeTruthy()
        expect(checklist.getByText('记录处置结论：在团队工单或运维记录中写明复核人、处理结果和时间。')).toBeTruthy()
      })
    })

    it('persists a current-page review disposition note', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockListActivity.mockResolvedValue({
        items: [
          {
            id: 'delete-1',
            timestamp: new Date(Date.now() - 1000 * 60).toISOString(),
            action: 'delete',
            path: '/old-file.txt',
            user: 'user1',
          },
          {
            id: 'share-1',
            timestamp: new Date(Date.now() - 1000 * 120).toISOString(),
            action: 'share',
            path: '/documents/report.pdf',
            user: 'admin',
          },
          {
            id: 'upload-1',
            timestamp: new Date(Date.now() - 1000 * 180).toISOString(),
            action: 'upload',
            path: '/documents/report.pdf',
            user: 'admin',
          },
        ],
        total: 3,
        limit: 20,
        offset: 0,
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByLabelText('活动复核记录')).toBeTruthy()
      })

      const recorder = within(screen.getByLabelText('活动复核记录'))
      const noteInput = recorder.getByLabelText('复核处置结论') as HTMLTextAreaElement
      const recordButton = recorder.getByRole('button', { name: '记录本页复核' })
      expect(recordButton).toBeDisabled()

      await user.click(recorder.getByText('选择已恢复'))
      await user.type(noteInput, '已确认删除可恢复，分享链接已关闭')
      expect(recordButton).not.toBeDisabled()
      await user.click(recordButton)

      await waitFor(() => {
        expect(mockCreateActivityReviewRecord).toHaveBeenCalledWith(expect.objectContaining({
          note: '已确认删除可恢复，分享链接已关闭',
          scope_label: '当前页',
          filter_summary: '未筛选',
          disposition_status: 'restored',
          action_counts: {
            delete: 1,
            share: 1,
          },
          review_count: 2,
          total_count: 3,
          path_count: 2,
          user_count: 2,
          path_samples: ['/old-file.txt', '/documents/report.pdf'],
          user_samples: ['user1', 'admin'],
          activity_entry_ids: ['delete-1', 'share-1'],
        }))
        expect(recorder.getByText('已确认删除可恢复，分享链接已关闭')).toBeTruthy()
        expect(recorder.getAllByText('已恢复').length).toBeGreaterThan(0)
        expect(recorder.getByText('类型：删除文件 1 · 创建分享 1')).toBeTruthy()
        expect(recorder.getByText('路径样例：/old-file.txt, /documents/report.pdf')).toBeTruthy()
        expect(recorder.getByText('用户样例：user1, admin')).toBeTruthy()
        expect(recorder.getByText('当前页：2 条待处置 / 3 条总记录 · 2 个路径 · 2 个用户')).toBeTruthy()
        expect(recorder.getByText('条件：未筛选')).toBeTruthy()
        expect(recorder.getAllByText('admin').length).toBeGreaterThan(0)
        expect(noteInput.value).toBe('')
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '本页复核已记录', color: 'success' }))
      })
    })

    it('records a filtered review across pages', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockListActivity.mockResolvedValueOnce({
        items: [
          {
            id: 'upload-visible',
            timestamp: new Date(Date.now() - 1000 * 60).toISOString(),
            action: 'upload',
            path: '/documents/new.pdf',
            user: 'admin',
          },
        ],
        total: 3,
        limit: 20,
        offset: 0,
      }).mockResolvedValueOnce({
        items: [
          {
            id: 'upload-visible',
            timestamp: new Date(Date.now() - 1000 * 60).toISOString(),
            action: 'upload',
            path: '/documents/new.pdf',
            user: 'admin',
          },
          {
            id: 'delete-hidden',
            timestamp: new Date(Date.now() - 1000 * 120).toISOString(),
            action: 'delete',
            path: '/documents/old.pdf',
            user: 'owner',
          },
          {
            id: 'share-hidden',
            timestamp: new Date(Date.now() - 1000 * 180).toISOString(),
            action: 'share',
            path: '/team/report.pdf',
            user: 'member',
          },
        ],
        total: 3,
        limit: 500,
        offset: 0,
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByLabelText('活动复核记录')).toBeTruthy()
      })

      const recorder = within(screen.getByLabelText('活动复核记录'))
      expect(recorder.getByRole('button', { name: '记录本页复核' })).toBeDisabled()

      await user.click(recorder.getByText('选择需跟进'))
      await user.type(recorder.getByLabelText('复核处置结论'), '跨页检查发现仍需跟进')
      await user.click(recorder.getByRole('button', { name: '记录当前筛选复核' }))

      await waitFor(() => {
        expect(mockListActivity).toHaveBeenCalledWith(expect.objectContaining({
          limit: 500,
          offset: 0,
        }))
        expect(mockCreateActivityReviewRecord).toHaveBeenCalledWith(expect.objectContaining({
          note: '跨页检查发现仍需跟进',
          scope_label: '全部记录',
          filter_summary: '未筛选',
          disposition_status: 'needs_follow_up',
          action_counts: {
            delete: 1,
            share: 1,
          },
          review_count: 2,
          total_count: 3,
          path_count: 2,
          user_count: 2,
          path_samples: ['/documents/old.pdf', '/team/report.pdf'],
          user_samples: ['owner', 'member'],
          activity_entry_ids: ['delete-hidden', 'share-hidden'],
        }))
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '当前筛选复核已记录', color: 'success' }))
      })
    })

    it('shows persisted activity review records without requiring current-page risk entries', async () => {
      mockListActivity.mockResolvedValue({
        items: [
          {
            id: 'upload-1',
            timestamp: new Date(Date.now() - 1000 * 60).toISOString(),
            action: 'upload',
            path: '/documents/report.pdf',
            user: 'admin',
          },
        ],
        total: 1,
        limit: 20,
        offset: 0,
      })
      mockListActivityReviewRecords.mockResolvedValue({
        items: [
          {
            id: 'review-older',
            reviewed_at: new Date(Date.now() - 1000 * 60 * 5).toISOString(),
            reviewer: 'owner',
            note: '批量删除已完成恢复确认',
            scope_label: '集中窗口',
            filter_summary: '窗口 2026-05-01 10:00 - 2026-05-01 10:08 · 分组 高风险变更',
            disposition_status: 'restored',
            action_counts: {
              delete: 2,
            },
            review_count: 6,
            total_count: 8,
            path_count: 4,
            user_count: 2,
            path_samples: ['/family/photos/a.jpg', '/family/photos/b.jpg'],
            user_samples: ['owner', 'member'],
            activity_entry_ids: ['delete-1', 'delete-2'],
          },
        ],
        total: 1,
        limit: 5,
        offset: 0,
      })

      render(<ActivityPage />)

      await waitFor(() => {
        const recorder = within(screen.getByLabelText('活动复核记录'))
        expect(recorder.getByText('批量删除已完成恢复确认')).toBeTruthy()
        expect(recorder.getAllByText('已恢复').length).toBeGreaterThan(0)
        expect(recorder.getByText('类型：删除文件 2')).toBeTruthy()
        expect(recorder.getByText('路径样例：/family/photos/a.jpg, /family/photos/b.jpg')).toBeTruthy()
        expect(recorder.getByText('用户样例：owner, member')).toBeTruthy()
        expect(recorder.getByText('集中窗口：6 条待处置 / 8 条总记录 · 4 个路径 · 2 个用户')).toBeTruthy()
        expect(recorder.getByText('条件：窗口 2026-05-01 10:00 - 2026-05-01 10:08 · 分组 高风险变更')).toBeTruthy()
        expect(recorder.getByText('owner')).toBeTruthy()
      })
    })

    it('expands persisted share review records with full detail clues', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockListActivity.mockResolvedValue({
        items: [
          {
            id: 'upload-1',
            timestamp: new Date(Date.now() - 1000 * 60).toISOString(),
            action: 'upload',
            path: '/documents/report.pdf',
            user: 'admin',
          },
        ],
        total: 1,
        limit: 20,
        offset: 0,
      })
      mockListActivityReviewRecords.mockImplementation(async (options) => {
        if (options?.limit === 5) {
          return {
            items: [
              {
                id: 'review-share-history-1',
                reviewed_at: '2026-06-03T10:30:00Z',
                reviewer: 'owner',
                note: '分享链接仍需处理',
                scope_label: '全部记录',
                filter_summary: '分组 分享相关',
                disposition_status: 'needs_follow_up',
                action_counts: {
                  share: 1,
                  unshare: 1,
                },
                review_count: 2,
                total_count: 4,
                path_count: 2,
                user_count: 2,
                path_samples: ['/docs/report.pdf', '/team/public'],
                user_samples: ['owner', 'member'],
                share_disposition_details: [{
                  path: '/docs/report.pdf',
                  type: 'file',
                  enabled: true,
                  risk_level: 'high',
                  reason_summary: '未设置密码，持有链接的人可直接访问。',
                  suggested_action: '停用或补齐密码、有效期和访问次数限制。',
                  access_summary: '无密码 · 访问 5/不限',
                  expires_at: '永不过期',
                }],
                activity_entry_ids: ['share-1', 'unshare-1'],
              },
            ],
            total: 1,
            limit: 5,
            offset: 0,
          }
        }

        return {
          items: [],
          total: 0,
          limit: options?.limit ?? 5,
          offset: 0,
        }
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('分享链接仍需处理')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '查看详情' }))

      await waitFor(() => {
        const detail = within(screen.getByLabelText('复核记录详情 review-share-history-1'))
        expect(detail.getByText('分享处置线索')).toBeTruthy()
        expect(detail.getByText('文件：/docs/report.pdf')).toBeTruthy()
        expect(detail.getByText('启用 · 高风险 · 无密码 · 访问 5/不限 · 永不过期')).toBeTruthy()
        expect(detail.getByText('建议：停用或补齐密码、有效期和访问次数限制。')).toBeTruthy()
        expect(detail.getByText('share-1, unshare-1')).toBeTruthy()
        expect(detail.getByText('/docs/report.pdf, /team/public')).toBeTruthy()
        expect(detail.getByText('owner, member')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '收起详情' }))
      await waitFor(() => {
        expect(screen.queryByLabelText('复核记录详情 review-share-history-1')).toBeNull()
      })
    })

    it('filters persisted review records by reviewer, linked activity, review type, review time, and disposition status', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByLabelText('复核历史筛选')).toBeTruthy()
      })

      mockListActivityReviewRecords.mockClear()
      const reviewFilters = within(screen.getByLabelText('复核历史筛选'))
      await user.type(reviewFilters.getByLabelText('按复核人筛选'), 'owner')
      await user.type(reviewFilters.getByLabelText('按关联活动筛选'), 'delete-1')
      await user.click(reviewFilters.getByText('筛选分享复核'))
      await user.click(reviewFilters.getByText('筛选复核近 7 天'))
      await user.click(reviewFilters.getByText('筛选复核需跟进'))

      await waitFor(() => {
        expectListActivityReviewRecordsCalledWith({
          limit: 5,
          reviewer: 'owner',
          activityEntryId: 'delete-1',
          dispositionStatus: 'needs_follow_up',
          actionGroup: 'share',
          since: /Z$/,
        })
      })
    })

    it('quickly focuses review history on follow-up records', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByLabelText('复核历史筛选')).toBeTruthy()
      })

      mockListActivityReviewRecords.mockClear()
      const reviewFilters = within(screen.getByLabelText('复核历史筛选'))
      await user.click(reviewFilters.getByRole('button', { name: '只看需跟进' }))

      await waitFor(() => {
        expectListActivityReviewRecordsCalledWith({
          limit: 5,
          dispositionStatus: 'needs_follow_up',
          reviewer: undefined,
          activityEntryId: undefined,
          actionGroup: undefined,
          since: undefined,
        })
        expect(reviewFilters.getByRole('button', { name: '只看需跟进' })).toBeDisabled()
      })
    })

    it('quickly focuses review history on share review records', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByLabelText('复核历史筛选')).toBeTruthy()
      })

      mockListActivityReviewRecords.mockClear()
      const reviewFilters = within(screen.getByLabelText('复核历史筛选'))
      await user.click(reviewFilters.getByRole('button', { name: '只看分享复核' }))

      await waitFor(() => {
        expectListActivityReviewRecordsCalledWith({
          limit: 5,
          reviewer: undefined,
          activityEntryId: undefined,
          dispositionStatus: undefined,
          actionGroup: 'share',
          since: undefined,
        })
        expect(reviewFilters.getByRole('button', { name: '只看分享复核' })).toBeDisabled()
      })
    })

    it('shows a batch follow-up view for review records that still need action', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockListActivityReviewRecords.mockImplementation(async (options) => {
        if (options?.limit === 3 && options?.dispositionStatus === 'needs_follow_up') {
          return {
            items: [
              {
                id: 'review-follow-up-1',
                reviewed_at: new Date(Date.now() - 1000 * 60 * 10).toISOString(),
                reviewer: 'owner',
                note: '误删恢复仍未确认',
                scope_label: '全部记录',
                filter_summary: '分组 高风险变更',
                disposition_status: 'needs_follow_up',
                action_counts: {
                  delete: 2,
                },
                review_count: 2,
                total_count: 5,
                path_count: 2,
                user_count: 1,
                path_samples: ['/photos/a.jpg', '/photos/b.jpg'],
                user_samples: ['owner'],
                activity_entry_ids: ['delete-1', 'delete-2'],
              },
            ],
            total: 3,
            limit: 3,
            offset: 0,
          }
        }

        return {
          items: [],
          total: 0,
          limit: options?.limit ?? 5,
          offset: 0,
        }
      })

      render(<ActivityPage />)

      await waitFor(() => {
        const followUpView = within(screen.getByLabelText('需跟进复核批量视图'))
        expect(followUpView.getByText('当前还有 3 条复核记录需跟进')).toBeTruthy()
        expect(followUpView.getByText('误删恢复仍未确认')).toBeTruthy()
        expect(followUpView.getByText('全部记录：2 条待处置 / 5 条总记录')).toBeTruthy()
        expect(followUpView.getByText('路径样例：/photos/a.jpg, /photos/b.jpg')).toBeTruthy()
        expect(followUpView.getByText('其余 2 条未在此处展开')).toBeTruthy()
      })

      mockListActivity.mockClear()
      mockGetActivityStats.mockClear()
      await user.click(within(screen.getByLabelText('需跟进复核批量视图')).getByRole('button', { name: '追踪活动' }))

      await waitFor(() => {
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          actionGroup: 'risk',
          path: '/photos/a.jpg',
        })
        expectGetActivityStatsCalledWithSignal({
          actionGroup: 'risk',
          path: '/photos/a.jpg',
        })
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '已切换到相关活动筛选',
          color: 'success',
        }))
        expect(screen.getByText('分组：高风险变更')).toBeTruthy()
        expect(screen.getByText('路径：/photos/a.jpg')).toBeTruthy()
      })

      await user.click(within(screen.getByLabelText('需跟进复核批量视图')).getByRole('button', { name: '查看版本' }))
      expect(window.location.pathname).toBe('/versions')
      expect(new URLSearchParams(window.location.search).get('path')).toBe('/photos/a.jpg')

      await user.click(within(screen.getByLabelText('需跟进复核批量视图')).getByRole('button', { name: '查回收站' }))
      expect(window.location.pathname).toBe('/trash')
      expect(new URLSearchParams(window.location.search).get('path')).toBe('/photos/a.jpg')

      mockListActivityReviewRecords.mockClear()
      await user.click(within(screen.getByLabelText('需跟进复核批量视图')).getByRole('button', { name: '查看需跟进' }))

      await waitFor(() => {
        expectListActivityReviewRecordsCalledWith({
          limit: 5,
          dispositionStatus: 'needs_follow_up',
          reviewer: undefined,
          actionGroup: undefined,
          since: undefined,
        })
      })
    })

    it('opens the path-scoped share disposition view from share review records', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockListActivityReviewRecords.mockImplementation(async (options) => {
        if (options?.limit === 3 && options?.dispositionStatus === 'needs_follow_up') {
          return {
            items: [
              {
                id: 'review-share-follow-up-1',
                reviewed_at: new Date(Date.now() - 1000 * 60 * 10).toISOString(),
                reviewer: 'owner',
                note: '分享链接仍需处理',
                scope_label: '全部记录',
                filter_summary: '分组 分享相关',
                disposition_status: 'needs_follow_up',
                action_counts: {
                  share: 1,
                },
                review_count: 1,
                total_count: 2,
                path_count: 1,
                user_count: 1,
                path_samples: ['/docs/report.pdf'],
                user_samples: ['owner'],
                activity_entry_ids: ['share-1'],
              },
            ],
            total: 1,
            limit: 3,
            offset: 0,
          }
        }

        return {
          items: [],
          total: 0,
          limit: options?.limit ?? 5,
          offset: 0,
        }
      })

      render(<ActivityPage />)

      await waitFor(() => {
        const followUpView = within(screen.getByLabelText('需跟进复核批量视图'))
        expect(followUpView.getByText('分享链接仍需处理')).toBeTruthy()
        expect(followUpView.getByRole('button', { name: '处理分享' })).toBeTruthy()
      })

      await user.click(within(screen.getByLabelText('需跟进复核批量视图')).getByRole('button', { name: '处理分享' }))

      expect(window.location.pathname).toBe('/settings')
      const params = new URLSearchParams(window.location.search)
      expect(params.get('tab')).toBe('shares')
      expect(params.get('share_path')).toBe('/docs/report.pdf')
    })

    it('writes back follow-up review record disposition status', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockListActivityReviewRecords.mockImplementation(async (options) => {
        if (options?.limit === 3 && options?.dispositionStatus === 'needs_follow_up') {
          return {
            items: [
              {
                id: 'review-follow-up-1',
                reviewed_at: new Date(Date.now() - 1000 * 60 * 10).toISOString(),
                reviewer: 'owner',
                note: '误删恢复仍未确认',
                scope_label: '全部记录',
                filter_summary: '分组 高风险变更',
                disposition_status: 'needs_follow_up',
                action_counts: {
                  delete: 1,
                },
                review_count: 1,
                total_count: 1,
                path_count: 1,
                user_count: 1,
                path_samples: ['/photos/a.jpg'],
                user_samples: ['owner'],
                activity_entry_ids: ['delete-1'],
              },
            ],
            total: 1,
            limit: 3,
            offset: 0,
          }
        }

        return {
          items: [],
          total: 0,
          limit: options?.limit ?? 5,
          offset: 0,
        }
      })
      mockUpdateActivityReviewRecordDisposition.mockImplementationOnce(async (id, input) => ({
        id,
        reviewed_at: new Date(Date.now() - 1000).toISOString(),
        reviewer: 'admin',
        note: input.note ?? '误删恢复仍未确认',
        scope_label: '全部记录',
        filter_summary: '分组 高风险变更',
        disposition_status: input.disposition_status,
        action_counts: {
          delete: 1,
        },
        review_count: 1,
        total_count: 1,
        path_count: 1,
        user_count: 1,
        path_samples: ['/photos/a.jpg'],
        user_samples: ['owner'],
        activity_entry_ids: ['delete-1'],
      }))

      render(<ActivityPage />)

      await waitFor(() => {
        const followUpView = within(screen.getByLabelText('需跟进复核批量视图'))
        expect(followUpView.getByText('误删恢复仍未确认')).toBeTruthy()
        expect(followUpView.getByRole('button', { name: '标记已恢复' })).toBeTruthy()
      })

      await user.type(screen.getByLabelText('复核记录处置备注 review-follow-up-1'), '已从回收站恢复到原路径')
      await user.click(within(screen.getByLabelText('需跟进复核批量视图')).getByRole('button', { name: '标记已恢复' }))

      await waitFor(() => {
        expect(mockUpdateActivityReviewRecordDisposition).toHaveBeenCalledWith('review-follow-up-1', {
          disposition_status: 'restored',
          note: '已从回收站恢复到原路径',
        })
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '复核状态已更新为已恢复',
          color: 'success',
        }))
        expect(screen.queryByLabelText('需跟进复核批量视图')).toBeNull()
      })
    })

    it('exports persisted review records as CSV', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByLabelText('复核历史筛选')).toBeTruthy()
      })

      const reviewFilters = within(screen.getByLabelText('复核历史筛选'))
      await user.type(reviewFilters.getByLabelText('按关联活动筛选'), 'share-1')
      await user.click(reviewFilters.getByText('筛选分享复核'))

      await waitFor(() => {
        expectListActivityReviewRecordsCalledWith({
          limit: 5,
          activityEntryId: 'share-1',
          actionGroup: 'share',
        })
      })

      mockListActivityReviewRecords.mockClear()
      mockListActivityReviewRecords.mockResolvedValueOnce({
        items: [
          {
            id: 'review-export-1',
            reviewed_at: '2026-06-03T10:30:00Z',
            reviewer: 'owner',
            note: '删除记录仍需跟进，已转入运维工单',
            scope_label: '全部记录',
            filter_summary: '分组 高风险变更',
            disposition_status: 'needs_follow_up',
            action_counts: {
              delete: 1,
              share: 1,
            },
            review_count: 2,
            total_count: 4,
            path_count: 2,
            user_count: 2,
            path_samples: ['/documents/old.pdf', '/team/report.pdf'],
            user_samples: ['owner', 'member'],
            share_disposition_details: [{
              path: '/team/report.pdf',
              type: 'file',
              enabled: true,
              risk_level: 'high',
              reason_summary: '未设置密码，持有链接的人可直接访问。',
              suggested_action: '停用或补齐密码、有效期和访问次数限制。',
              access_summary: '无密码 · 访问 1/不限',
              expires_at: '永不过期',
            }],
            activity_entry_ids: ['delete-1', 'share-1'],
          },
        ],
        total: 1,
        limit: 100,
        offset: 0,
      })

      await user.click(screen.getByRole('button', { name: '导出复核记录' }))

      await waitFor(() => {
        expect(mockListActivityReviewRecords).toHaveBeenCalledWith(expect.objectContaining({
          limit: 100,
          offset: 0,
          reviewer: undefined,
          activityEntryId: 'share-1',
          dispositionStatus: undefined,
          actionGroup: 'share',
          since: undefined,
        }))
        expect(mockTriggerBrowserDownload).toHaveBeenCalledTimes(1)
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '复核记录已导出',
          color: 'success',
        }))
      })

      const [blob, filename] = mockTriggerBrowserDownload.mock.calls[0] as [Blob, string]
      expect(filename).toMatch(/^mnemonas-activity-review-records-.+\.csv$/)
      const csv = await blob.text()
      expect(csv).toContain('复核时间')
      expect(csv).toContain('删除记录仍需跟进，已转入运维工单')
      expect(csv).toContain('需跟进')
      expect(csv).toContain('删除文件 1; 创建分享 1')
      expect(csv).toContain('/documents/old.pdf; /team/report.pdf')
      expect(csv).toContain('/team/report.pdf | 高风险 | 未设置密码，持有链接的人可直接访问。 | 停用或补齐密码、有效期和访问次数限制。')
    })

    it('does not download an empty persisted review export', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByLabelText('复核历史筛选')).toBeTruthy()
      })

      mockListActivityReviewRecords.mockClear()
      mockListActivityReviewRecords.mockResolvedValueOnce({
        items: [],
        total: 0,
        limit: 100,
        offset: 0,
      })

      await user.click(screen.getByRole('button', { name: '导出复核记录' }))

      await waitFor(() => {
        expect(mockListActivityReviewRecords).toHaveBeenCalledWith(expect.objectContaining({
          limit: 100,
          offset: 0,
        }))
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '没有可导出的复核记录',
          color: 'warning',
        })
      })
      expect(mockTriggerBrowserDownload).not.toHaveBeenCalled()
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
        expect(screen.getByText('client：web')).toBeTruthy()
        expect(screen.getByText('result：ok')).toBeTruthy()
      })
    })

    it('renders share activity details with review-friendly labels', async () => {
      mockListActivity.mockResolvedValue({
        items: [
          {
            id: '1',
            timestamp: new Date(Date.now() - 1000 * 60).toISOString(),
            action: 'share',
            path: '/documents/report.pdf',
            user: 'admin',
            details: {
              max_access: '2',
              has_password: 'true',
              type: 'file',
              expires_at: '2026-03-13T00:00:00Z',
              access_count: '1',
              permission: 'read',
            },
          },
        ],
        total: 1,
        limit: 20,
        offset: 0,
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('类型：文件')).toBeTruthy()
        expect(screen.getByText('权限：只读')).toBeTruthy()
        expect(screen.getByText('密码保护：是')).toBeTruthy()
        expect(screen.getByText('访问次数：1 次')).toBeTruthy()
        expect(screen.getByText('访问上限：2 次')).toBeTruthy()
        expect(screen.getByText(/^过期时间：/)).toBeTruthy()
        expect(screen.queryByText('has_password: true')).toBeNull()
        expect(screen.queryByText('max_access: 2')).toBeNull()
      })
    })

    it('maps disk health activity details before rendering them', async () => {
      mockListActivity.mockResolvedValue({
        items: [
          {
            id: '1',
            timestamp: new Date(Date.now() - 1000 * 60).toISOString(),
            action: 'disk_health',
            path: '/system/disk-health',
            user: 'system',
            details: {
              status: 'critical',
              message: 'one or more disks require immediate attention',
              checked_at: '2026-05-13T08:30:00Z',
              device_count: '3',
              warning_count: '2',
              critical_devices: 'data, unnamed device (+2 more)',
              warning_devices: 'backup',
              device: 'unnamed device',
              device_status: 'critical',
              device_message: 'temperature 61 C reached critical threshold 60 C',
              temperature_c: '61',
              wear_percent_used: '98',
              available_spare_percent: '5',
              media_errors: '7',
            },
          },
        ],
        total: 1,
        limit: 20,
        offset: 0,
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('状态：严重异常')).toBeTruthy()
        expect(screen.getByText('诊断：磁盘健康严重异常，请尽快备份并检查 SMART、温度、磨损和设备连接状态。')).toBeTruthy()
        expect(screen.getByText('严重异常设备：data、未命名设备 等 2 个')).toBeTruthy()
        expect(screen.getByText('首个异常设备：未命名设备')).toBeTruthy()
        expect(screen.getByText('设备状态：严重异常')).toBeTruthy()
        expect(screen.getByText('设备诊断：磁盘温度 61 C 已达到严重阈值 60 C。')).toBeTruthy()
        expect(screen.getByText('温度：61 C')).toBeTruthy()
        expect(screen.getByText('介质磨损：98%')).toBeTruthy()
        expect(screen.getByText('可用备用空间：5%')).toBeTruthy()
        expect(screen.getByText('介质错误：7 个')).toBeTruthy()
      })
      expect(screen.queryByText(/temperature 61 C reached critical threshold 60 C/)).toBeNull()
      expect(screen.queryByText('device_status: critical')).toBeNull()
      expect(screen.queryByText('message: one or more disks require immediate attention')).toBeNull()
      expect(screen.queryByText(/unnamed device/)).toBeNull()
    })

    it('maps scrub activity details before rendering them', async () => {
      mockListActivity.mockResolvedValue({
        items: [
          {
            id: '1',
            timestamp: new Date(Date.now() - 1000 * 60).toISOString(),
            action: 'scrub',
            path: '/system/scrub',
            user: 'system',
            details: {
              status: 'completed',
              trigger: 'scheduled',
              started_at: '2026-05-13T08:30:00Z',
              finished_at: '2026-05-13T08:31:30Z',
              total_objects: '12',
              valid_objects: '10',
              corrupted_objects: '1',
              missing_objects: '1',
              total_size: '2048',
              duration_ms: '90000',
              error_count: '2',
              persistence_warning: 'true',
            },
          },
        ],
        total: 1,
        limit: 20,
        offset: 0,
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('状态：已完成')).toBeTruthy()
        expect(screen.getByText('触发方式：定时任务')).toBeTruthy()
        expect(screen.getByText('总对象：12 个')).toBeTruthy()
        expect(screen.getByText('有效对象：10 个')).toBeTruthy()
        expect(screen.getByText('损坏对象：1 个')).toBeTruthy()
        expect(screen.getByText('缺失对象：1 个')).toBeTruthy()
        expect(screen.getByText('校验数据量：2 KB')).toBeTruthy()
        expect(screen.getByText('耗时：1 分 30 秒')).toBeTruthy()
        expect(screen.getByText('错误数：2 个')).toBeTruthy()
        expect(screen.getByText('记录持久化：结果记录保存异常，请检查维护历史。')).toBeTruthy()
        expect(screen.getByText(/^开始时间：/)).toBeTruthy()
        expect(screen.getByText(/^完成时间：/)).toBeTruthy()
      })
      expect(screen.queryByText('status: completed')).toBeNull()
      expect(screen.queryByText('trigger: scheduled')).toBeNull()
      expect(screen.queryByText('persistence_warning: true')).toBeNull()
      expect(screen.queryByText('duration_ms: 90000')).toBeNull()
    })

    it('maps trash activity details before rendering them', async () => {
      mockListActivity.mockResolvedValue({
        items: [
          {
            id: '1',
            timestamp: new Date(Date.now() - 1000 * 60).toISOString(),
            action: 'trash_empty',
            path: '',
            user: 'admin',
            details: {
              count: '3',
              partial: 'true',
              cleanup_warning: 'true',
            },
          },
          {
            id: '2',
            timestamp: new Date(Date.now() - 1000 * 120).toISOString(),
            action: 'trash_restore',
            path: '/documents/report.pdf',
            user: 'admin',
            details: {
              persistence_warning: 'true',
              metadata_restore: 'failed',
            },
          },
        ],
        total: 2,
        limit: 20,
        offset: 0,
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('项目数：3 项')).toBeTruthy()
        expect(screen.getByText('执行结果：仅完成部分项目')).toBeTruthy()
        expect(screen.getByText('清理状态：部分垃圾箱数据清理不完整，请检查存储状态。')).toBeTruthy()
        expect(screen.getByText('记录持久化：操作已完成，但变更记录保存异常。')).toBeTruthy()
        expect(screen.getByText('关联元数据：关联分享或收藏恢复失败，请检查相关记录。')).toBeTruthy()
      })
      expect(screen.queryByText('count: 3')).toBeNull()
      expect(screen.queryByText('partial: true')).toBeNull()
      expect(screen.queryByText('cleanup_warning: true')).toBeNull()
      expect(screen.queryByText('metadata_restore: failed')).toBeNull()
      expect(screen.queryByText('persistence_warning: true')).toBeNull()
    })

    it('maps file operation activity details before rendering them', async () => {
      mockListActivity.mockResolvedValue({
        items: [
          {
            id: '1',
            timestamp: new Date(Date.now() - 1000 * 60).toISOString(),
            action: 'move',
            path: '/documents/report.pdf',
            user: 'admin',
            details: {
              to: '/archive/report.pdf',
              persistence_warning: 'true',
            },
          },
          {
            id: '2',
            timestamp: new Date(Date.now() - 1000 * 120).toISOString(),
            action: 'create',
            path: '/documents/new-folder',
            user: 'admin',
            details: {
              type: 'directory',
            },
          },
          {
            id: '3',
            timestamp: new Date(Date.now() - 1000 * 180).toISOString(),
            action: 'delete',
            path: '/documents/old.txt',
            user: 'admin',
            details: {
              cleanup_warning: 'true',
              trash_cleanup_warning: 'true',
            },
          },
          {
            id: '4',
            timestamp: new Date(Date.now() - 1000 * 240).toISOString(),
            action: 'restore',
            path: '/documents/report.pdf',
            user: 'admin',
            details: {
              hash: 'abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
            },
          },
        ],
        total: 4,
        limit: 20,
        offset: 0,
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('目标路径：/archive/report.pdf')).toBeTruthy()
        expect(screen.getByText('记录持久化：操作已完成，但变更记录保存异常。')).toBeTruthy()
        expect(screen.getByText('类型：文件夹')).toBeTruthy()
        expect(screen.getByText('清理状态：残留数据清理不完整，请检查存储状态。')).toBeTruthy()
        expect(screen.getByText('回收站清理：回收站关联数据清理不完整，请检查回收站状态。')).toBeTruthy()
        expect(screen.getByText('版本哈希：abcdef123456...')).toBeTruthy()
      })
      expect(screen.queryByText('to: /archive/report.pdf')).toBeNull()
      expect(screen.queryByText('type: directory')).toBeNull()
      expect(screen.queryByText('cleanup_warning: true')).toBeNull()
      expect(screen.queryByText('trash_cleanup_warning: true')).toBeNull()
      expect(screen.queryByText(/abcdef1234567890abcdef/)).toBeNull()
    })

    it('opens recovery and share disposition destinations from activity rows', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockListActivity.mockResolvedValue({
        items: [
          {
            id: 'delete-row',
            timestamp: new Date(Date.now() - 1000 * 60).toISOString(),
            action: 'delete',
            path: '/documents/old.txt',
            user: 'admin',
          },
          {
            id: 'share-row',
            timestamp: new Date(Date.now() - 1000 * 120).toISOString(),
            action: 'share',
            path: '/team/report.pdf',
            user: 'admin',
          },
        ],
        total: 2,
        limit: 20,
        offset: 0,
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByLabelText('活动记录 删除文件 /documents/old.txt')).toBeTruthy()
        expect(screen.getByLabelText('活动记录 创建分享 /team/report.pdf')).toBeTruthy()
      })

      const deleteRow = within(screen.getByLabelText('活动记录 删除文件 /documents/old.txt'))
      await user.click(deleteRow.getByRole('button', { name: '查看版本' }))
      expect(window.location.pathname).toBe('/versions')
      expect(new URLSearchParams(window.location.search).get('path')).toBe('/documents/old.txt')

      await user.click(deleteRow.getByRole('button', { name: '查回收站' }))
      expect(window.location.pathname).toBe('/trash')
      expect(new URLSearchParams(window.location.search).get('path')).toBe('/documents/old.txt')

      await user.click(within(screen.getByLabelText('活动记录 创建分享 /team/report.pdf')).getByRole('button', { name: '处理分享' }))
      expect(window.location.pathname).toBe('/settings')
      const params = new URLSearchParams(window.location.search)
      expect(params.get('tab')).toBe('shares')
      expect(params.get('share_path')).toBe('/team/report.pdf')
    })

    it('maps archive download activity details before rendering them', async () => {
      mockListActivity.mockResolvedValue({
        items: [
          {
            id: '1',
            timestamp: new Date(Date.now() - 1000 * 60).toISOString(),
            action: 'download',
            path: '/photos',
            user: 'admin',
            details: {
              archive: 'zip',
              entries: '128',
            },
          },
        ],
        total: 1,
        limit: 20,
        offset: 0,
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('归档格式：ZIP')).toBeTruthy()
        expect(screen.getByText('归档项目数：128 项')).toBeTruthy()
      })
      expect(screen.queryByText('archive: zip')).toBeNull()
      expect(screen.queryByText('entries: 128')).toBeNull()
    })

    it('shows relative time', async () => {
      render(<ActivityPage />)
      
      await waitFor(() => {
        // Should show localized relative-time text.
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
        expect(screen.getByText('最近操作加载失败，请检查网络或稍后重试。')).toBeTruthy()
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

    it('uses a generic toast description when activity reload fails with an Error object', async () => {
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
          description: '操作未完成，请稍后重试。',
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
        expect(screen.getByText('审计分组')).toBeTruthy()
        expect(screen.getByText('时间范围')).toBeTruthy()
        expect(screen.getByLabelText('按路径筛选')).toBeTruthy()
        expect(screen.getAllByText('数据校验').length).toBeGreaterThan(0)
        expect(screen.getByText('重命名')).toBeTruthy()
        expect(screen.getByText('复制文件')).toBeTruthy()
        expect(screen.getByText('创建文件夹')).toBeTruthy()
        expect(screen.getByText('取消分享')).toBeTruthy()
        expect(screen.getByText('更新收藏备注')).toBeTruthy()
        expect(screen.getByText('磁盘健康异常')).toBeTruthy()
        expect(screen.queryByText('rename')).toBeNull()
        expect(screen.queryByText('copy')).toBeNull()
        expect(screen.queryByText('create')).toBeNull()
        expect(screen.queryByText('unshare')).toBeNull()
        expect(screen.queryByText('favorite_note_update')).toBeNull()
        expect(screen.queryByText('disk_health')).toBeNull()
      })
    })

    it('renders an admin-only user filter input', async () => {
      const { unmount } = render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByLabelText('按用户筛选')).toBeTruthy()
      })

      unmount()
      useIsAdminMock.mockReturnValue(false)
      useUserMock.mockReturnValue({ id: 'member-id', username: 'member', role: 'user', homeDir: '/member' })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getAllByText(/共 3 条记录/).length).toBeGreaterThan(0)
      })

      expect(screen.queryByLabelText('按用户筛选')).toBeNull()
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
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          action: 'delete',
        })
        expectGetActivityStatsCalledWithSignal({
          action: 'delete',
        })
        expect(screen.getByText('当前筛选：')).toBeTruthy()
        expect(screen.getByText('当前筛选结果')).toBeTruthy()
      })
    })

    it('requests the selected action group and shows the active group chip', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('筛选分享相关')).toBeTruthy()
      })

      mockListActivity.mockClear()
      mockGetActivityStats.mockClear()
      await user.click(screen.getByText('筛选分享相关'))

      await waitFor(() => {
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          action: undefined,
          actionGroup: 'share',
        })
        expectGetActivityStatsCalledWithSignal({
          actionGroup: 'share',
        })
        expect(screen.getByText('当前筛选：')).toBeTruthy()
        expect(screen.getByText('分组：分享相关')).toBeTruthy()
        expect(screen.getByText('当前筛选结果')).toBeTruthy()
      })
    })

    it('loads share review filters from the URL query', async () => {
      window.history.pushState({}, '', '/activity?action_group=share&path=%2Fdocs%2Freport.pdf')

      render(<ActivityPage />)

      await waitFor(() => {
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          action: undefined,
          actionGroup: 'share',
          path: '/docs/report.pdf',
        })
        expectGetActivityStatsCalledWithSignal({
          actionGroup: 'share',
          path: '/docs/report.pdf',
        })
        expect(screen.getByText('当前筛选：')).toBeTruthy()
        expect(screen.getByText('分组：分享相关')).toBeTruthy()
        expect(screen.getByText('路径：/docs/report.pdf')).toBeTruthy()
        expect(screen.getByLabelText('按路径筛选')).toHaveValue('/docs/report.pdf')
      })
    })

    it('clears the action group when a single action filter is selected', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('筛选高风险变更')).toBeTruthy()
      })

      await user.click(screen.getByText('筛选高风险变更'))

      await waitFor(() => {
        expect(screen.getByText('分组：高风险变更')).toBeTruthy()
      })

      mockListActivity.mockClear()
      await user.click(screen.getByText('筛选删除文件'))

      await waitFor(() => {
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          action: 'delete',
          actionGroup: undefined,
        })
        expect(screen.queryByText('分组：高风险变更')).toBeNull()
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
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          action: 'scrub',
        })
        expect(screen.getByText('当前筛选：')).toBeTruthy()
      })
    })

    it('requests the entered user and shows the active user filter chip', async () => {
      render(<ActivityPage />)

      const userFilterInput = await screen.findByLabelText('按用户筛选')
      fireEvent.change(userFilterInput, { target: { value: 'user1' } })

      await waitFor(() => {
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          action: undefined,
          user: 'user1',
        })
        expectGetActivityStatsCalledWithSignal({
          user: 'user1',
        })
        expect(screen.getByText('当前筛选：')).toBeTruthy()
        expect(screen.getByText('用户：user1')).toBeTruthy()
      })
    })

    it('requests the entered path and shows the active path filter chip', async () => {
      render(<ActivityPage />)

      const pathFilterInput = await screen.findByLabelText('按路径筛选')
      fireEvent.change(pathFilterInput, { target: { value: 'photos' } })

      await waitFor(() => {
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          action: undefined,
          path: '/photos',
          user: undefined,
        })
        expectGetActivityStatsCalledWithSignal({
          path: '/photos',
        })
        expect(screen.getByText('当前筛选：')).toBeTruthy()
        expect(screen.getByText('路径：/photos')).toBeTruthy()
        expect(screen.getByText('当前筛选结果')).toBeTruthy()
      })
    })

    it('keeps invalid path filters local and lets users clear the invalid state', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<ActivityPage />)

      const pathFilterInput = await screen.findByLabelText('按路径筛选')
      await waitFor(() => {
        expect(mockListActivity).toHaveBeenCalled()
        expect(mockGetActivityStats).toHaveBeenCalled()
      })

      mockListActivity.mockClear()
      mockGetActivityStats.mockClear()
      fireEvent.change(pathFilterInput, { target: { value: '/photos/./secret' } })

      await waitFor(() => {
        expect(screen.getAllByText('路径不能包含 .、.. 或控制字符').length).toBeGreaterThan(0)
        expect(pathFilterInput).toHaveAttribute('aria-invalid', 'true')
      })
      expect(mockListActivity).not.toHaveBeenCalled()
      expect(mockGetActivityStats).not.toHaveBeenCalled()
      expect(screen.queryByText('路径：/photos/./secret')).toBeNull()
      expect(screen.getAllByText('路径筛选无效').length).toBeGreaterThan(0)
      expect(screen.queryByText('/documents/report.pdf')).toBeNull()
      expect(screen.queryByText('累计操作')).toBeNull()
      expect(screen.queryByText('高风险摘要')).toBeNull()

      await user.click(screen.getByRole('button', { name: '清除路径条件' }))

      await waitFor(() => {
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          action: undefined,
          path: undefined,
          user: undefined,
        })
        expectGetActivityStatsCalledWithSignal()
        expect((pathFilterInput as HTMLInputElement).value).toBe('')
        expect(screen.queryByText('路径筛选无效')).toBeNull()
      })
    })

    it('requests the selected time range and shows the active time chip', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('筛选今天')).toBeTruthy()
      })

      mockListActivity.mockClear()
      mockGetActivityStats.mockClear()
      await user.click(screen.getByText('筛选今天'))

      await waitFor(() => {
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          action: undefined,
          user: undefined,
          since: /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/,
        })
        expectGetActivityStatsCalledWithSignal({
          since: /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/,
        })
        expect(screen.getByText('当前筛选：')).toBeTruthy()
        expect(screen.getByText('时间：今天')).toBeTruthy()
        expect(screen.getByText('当前筛选结果')).toBeTruthy()
      })
    })

    it('clears the active time range filter from the chip', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText('筛选近 7 天')).toBeTruthy()
      })

      await user.click(screen.getByText('筛选近 7 天'))

      await waitFor(() => {
        expect(screen.getByText('时间：近 7 天')).toBeTruthy()
      })

      mockListActivity.mockClear()
      await user.click(screen.getByText('清除筛选'))

      await waitFor(() => {
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          action: undefined,
          user: undefined,
          since: undefined,
        })
        expect(screen.queryByText('时间：近 7 天')).toBeNull()
      })
    })

    it('clears the active path filter from the input clear action', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<ActivityPage />)

      const pathFilterInput = await screen.findByLabelText('按路径筛选')
      fireEvent.change(pathFilterInput, { target: { value: '/photos' } })

      await waitFor(() => {
        expect(screen.getByText('路径：/photos')).toBeTruthy()
      })

      mockListActivity.mockClear()
      await user.click(screen.getByRole('button', { name: '清除路径筛选' }))

      await waitFor(() => {
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          action: undefined,
          path: undefined,
          user: undefined,
        })
        expect(screen.queryByText('路径：/photos')).toBeNull()
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
        expect(screen.getByText('当前筛选：')).toBeTruthy()
      })

      await user.click(screen.getByText('清除筛选'))

      await waitFor(() => {
        expect(screen.queryByText('当前筛选：')).toBeNull()
      })
    })

    it('clears the active user filter from the input clear action', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<ActivityPage />)

      const userFilterInput = await screen.findByLabelText('按用户筛选')
      fireEvent.change(userFilterInput, { target: { value: 'user1' } })

      await waitFor(() => {
        expect(screen.getByText('用户：user1')).toBeTruthy()
      })

      mockListActivity.mockClear()
      await user.click(screen.getByRole('button', { name: '清除用户筛选' }))

      await waitFor(() => {
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          action: undefined,
          user: undefined,
        })
        expect(screen.queryByText('用户：user1')).toBeNull()
      })
    })

    it('clears all active filters from the summary action', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<ActivityPage />)

      const pathFilterInput = await screen.findByLabelText('按路径筛选')
      fireEvent.change(pathFilterInput, { target: { value: 'photos' } })

      await waitFor(() => {
        expect(screen.getByText('路径：/photos')).toBeTruthy()
      })

      const userFilterInput = await screen.findByLabelText('按用户筛选')
      fireEvent.change(userFilterInput, { target: { value: 'user1' } })

      await waitFor(() => {
        expect(screen.getByText('路径：/photos')).toBeTruthy()
        expect(screen.getByText('用户：user1')).toBeTruthy()
      })

      mockListActivity.mockClear()
      mockGetActivityStats.mockClear()
      await user.click(screen.getByRole('button', { name: '清空全部筛选' }))

      await waitFor(() => {
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          action: undefined,
          actionGroup: undefined,
          path: undefined,
          user: undefined,
          since: undefined,
          until: undefined,
        })
        expectGetActivityStatsCalledWithSignal()
        expect(screen.queryByText('当前筛选：')).toBeNull()
        expect((screen.getByLabelText('按路径筛选') as HTMLInputElement).value).toBe('')
        expect((screen.getByLabelText('按用户筛选') as HTMLInputElement).value).toBe('')
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

    it('keeps refresh local while the path filter is invalid', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<ActivityPage />)

      const pathFilterInput = await screen.findByLabelText('按路径筛选')
      await waitFor(() => {
        expect(mockListActivity).toHaveBeenCalled()
        expect(mockGetActivityStats).toHaveBeenCalled()
      })

      mockListActivity.mockClear()
      mockGetActivityStats.mockClear()
      mockAddToast.mockClear()
      fireEvent.change(pathFilterInput, { target: { value: '../secret' } })

      await waitFor(() => {
        expect(screen.getAllByText('路径不能包含 .、.. 或控制字符').length).toBeGreaterThan(0)
      })

      await user.click(screen.getByText('刷新'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '路径筛选无效',
          description: '路径不能包含 .、.. 或控制字符',
          color: 'warning',
        })
      })
      expect(mockListActivity).not.toHaveBeenCalled()
      expect(mockGetActivityStats).not.toHaveBeenCalled()
    })

    it('keeps Unicode control path filters local', async () => {
      render(<ActivityPage />)

      const pathFilterInput = await screen.findByLabelText('按路径筛选')
      await waitFor(() => {
        expect(mockListActivity).toHaveBeenCalled()
        expect(mockGetActivityStats).toHaveBeenCalled()
      })

      mockListActivity.mockClear()
      mockGetActivityStats.mockClear()
      fireEvent.change(pathFilterInput, { target: { value: '/photos\u0085secret' } })

      await waitFor(() => {
        expect(screen.getAllByText('路径不能包含 .、.. 或控制字符').length).toBeGreaterThan(0)
        expect(pathFilterInput).toHaveAttribute('aria-invalid', 'true')
      })
      expect(mockListActivity).not.toHaveBeenCalled()
      expect(mockGetActivityStats).not.toHaveBeenCalled()
      expect(screen.queryByText('路径：/photos\u0085secret')).toBeNull()
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
          description: '操作未完成，请稍后重试。',
          color: 'danger',
        })
      })
    })
  })

  describe('clear activity', () => {
    it('lets admins clear the activity log after confirmation', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '清空记录' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '清空记录' }))
      await user.click(await screen.findByRole('button', { name: '确认清空' }))

      await waitFor(() => {
        expect(mockClearActivity).toHaveBeenCalledWith({
          signal: expect.any(AbortSignal),
        })
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '最近操作已清空',
          description: undefined,
          color: 'success',
        })
      })
    })

    it('shows warning toast when activity clear succeeds with warnings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockClearActivity.mockResolvedValueOnce({
        warning: true,
        message: 'activity clear completed with persistence warning --password activity-secret',
      })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '清空记录' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '清空记录' }))
      await user.click(await screen.findByRole('button', { name: '确认清空' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '最近操作已清空，但存在警告',
          description: 'activity clear completed with persistence warning --password <redacted>',
          color: 'warning',
        })
      })
      expect(mockAddToast).not.toHaveBeenCalledWith(expect.objectContaining({
        description: expect.stringContaining('activity-secret'),
      }))
    })

    it('hides the clear action for non-admin users', async () => {
      useIsAdminMock.mockReturnValue(false)
      useUserMock.mockReturnValue({ id: 'member-id', username: 'member', role: 'user', homeDir: '/member' })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByText(/共 3 条记录/)).toBeTruthy()
      })

      expect(screen.queryByRole('button', { name: '清空记录' })).toBeNull()
    })

    it('aborts a pending clear request on unmount and ignores abort feedback', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const clearRequest = createDeferred<{ message?: string }>()
      mockClearActivity.mockImplementationOnce(() => clearRequest.promise)

      const { unmount } = render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '清空记录' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '清空记录' }))
      await user.click(await screen.findByRole('button', { name: '确认清空' }))

      await waitFor(() => {
        expect(mockClearActivity).toHaveBeenCalledWith({
          signal: expect.any(AbortSignal),
        })
      })
      const signal = getClearActivitySignal()

      unmount()

      expect(signal.aborted).toBe(true)

      clearRequest.reject(new DOMException('activity clear aborted', 'AbortError'))
      await clearRequest.promise.catch(() => undefined)

      expect(mockAddToast).not.toHaveBeenCalled()
    })

    it('uses a generic toast description when clearing activity fails with an Error object', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockClearActivity.mockRejectedValueOnce(new Error('activity clear failed'))

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '清空记录' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '清空记录' }))
      await user.click(await screen.findByRole('button', { name: '确认清空' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '清空最近操作失败',
          description: '操作未完成，请稍后重试。',
          color: 'danger',
        })
      })
    })
  })

  describe('API calls', () => {
    it('calls listActivity with default parameters', async () => {
      render(<ActivityPage />)
      
      await waitFor(() => {
        expectListActivityCalledWithSignal({
          limit: 20,
          offset: 0,
          action: undefined,
        })
        expectGetActivityStatsCalledWithSignal()
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
        expectListActivityCalledWith({
          limit: 20,
          offset: 20,
          action: undefined,
        })
      })

      await waitFor(() => {
        expectListActivityCalledWith({
          limit: 20,
          offset: 0,
          action: undefined,
        })
        expect(screen.getByText('/documents/report.pdf')).toBeTruthy()
      })
    })
  })
})
