import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { ReactNode } from 'react'
import { render, screen, waitFor } from '@/test/utils'
import { fireEvent } from '@testing-library/react'
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
  listActivity: vi.fn(),
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

import { ApiError, clearActivity, getActivityStats, listActivity } from '@/api/activity'

const mockListActivity = listActivity as ReturnType<typeof vi.fn>
const mockClearActivity = clearActivity as ReturnType<typeof vi.fn>
const mockGetActivityStats = getActivityStats as ReturnType<typeof vi.fn>

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
    mockClearActivity.mockResolvedValue({ message: '最近操作已清空' })
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
        expect(screen.getByText('累计操作')).toBeTruthy()
        expect(screen.getByText('今日操作')).toBeTruthy()
        expect(screen.getByText('最常见操作')).toBeTruthy()
        expect(screen.getByText('最活跃用户')).toBeTruthy()
        expect(screen.getByText('高风险摘要')).toBeTruthy()
        expect(screen.getByText('未发现明显集中批量变更')).toBeTruthy()
        expect(screen.getByText('10 分钟最多')).toBeTruthy()
        expect(screen.getAllByText('上传文件').length).toBeGreaterThan(1)
        expect(screen.getAllByText('admin').length).toBeGreaterThan(1)
        expect(screen.getAllByText('12').length).toBeGreaterThan(0)
        expect(screen.getAllByText('3').length).toBeGreaterThan(0)
        expect(screen.getByText('7 次')).toBeTruthy()
        expect(screen.getByText('8 次')).toBeTruthy()
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
        expect(screen.getByText('类型: 文件')).toBeTruthy()
        expect(screen.getByText('权限: 只读')).toBeTruthy()
        expect(screen.getByText('密码保护: 是')).toBeTruthy()
        expect(screen.getByText('访问次数: 1 次')).toBeTruthy()
        expect(screen.getByText('访问上限: 2 次')).toBeTruthy()
        expect(screen.getByText(/^过期时间:/)).toBeTruthy()
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
        expect(screen.getByText('状态: 严重异常')).toBeTruthy()
        expect(screen.getByText('诊断: 磁盘健康严重异常，请尽快备份并检查 SMART、温度、磨损和设备连接状态。')).toBeTruthy()
        expect(screen.getByText('严重异常设备: data、未命名设备 等 2 个')).toBeTruthy()
        expect(screen.getByText('首个异常设备: 未命名设备')).toBeTruthy()
        expect(screen.getByText('设备状态: 严重异常')).toBeTruthy()
        expect(screen.getByText('设备诊断: 磁盘温度 61 C 已达到严重阈值 60 C。')).toBeTruthy()
        expect(screen.getByText('温度: 61 C')).toBeTruthy()
        expect(screen.getByText('介质磨损: 98%')).toBeTruthy()
        expect(screen.getByText('可用备用空间: 5%')).toBeTruthy()
        expect(screen.getByText('介质错误: 7 个')).toBeTruthy()
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
        expect(screen.getByText('状态: 已完成')).toBeTruthy()
        expect(screen.getByText('触发方式: 定时任务')).toBeTruthy()
        expect(screen.getByText('总对象: 12 个')).toBeTruthy()
        expect(screen.getByText('有效对象: 10 个')).toBeTruthy()
        expect(screen.getByText('损坏对象: 1 个')).toBeTruthy()
        expect(screen.getByText('缺失对象: 1 个')).toBeTruthy()
        expect(screen.getByText('校验数据量: 2 KB')).toBeTruthy()
        expect(screen.getByText('耗时: 1 分 30 秒')).toBeTruthy()
        expect(screen.getByText('错误数: 2 个')).toBeTruthy()
        expect(screen.getByText('记录持久化: 结果记录保存异常，请检查维护历史。')).toBeTruthy()
        expect(screen.getByText(/^开始时间:/)).toBeTruthy()
        expect(screen.getByText(/^完成时间:/)).toBeTruthy()
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
        expect(screen.getByText('项目数: 3 项')).toBeTruthy()
        expect(screen.getByText('执行结果: 仅完成部分项目')).toBeTruthy()
        expect(screen.getByText('清理状态: 部分垃圾箱数据清理不完整，请检查存储状态。')).toBeTruthy()
        expect(screen.getByText('记录持久化: 操作已完成，但变更记录保存异常。')).toBeTruthy()
        expect(screen.getByText('关联元数据: 关联分享或收藏恢复失败，请检查相关记录。')).toBeTruthy()
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
        expect(screen.getByText('目标路径: /archive/report.pdf')).toBeTruthy()
        expect(screen.getByText('记录持久化: 操作已完成，但变更记录保存异常。')).toBeTruthy()
        expect(screen.getByText('类型: 文件夹')).toBeTruthy()
        expect(screen.getByText('清理状态: 残留数据清理不完整，请检查存储状态。')).toBeTruthy()
        expect(screen.getByText('回收站清理: 回收站关联数据清理不完整，请检查回收站状态。')).toBeTruthy()
        expect(screen.getByText('版本哈希: abcdef123456...')).toBeTruthy()
      })
      expect(screen.queryByText('to: /archive/report.pdf')).toBeNull()
      expect(screen.queryByText('type: directory')).toBeNull()
      expect(screen.queryByText('cleanup_warning: true')).toBeNull()
      expect(screen.queryByText('trash_cleanup_warning: true')).toBeNull()
      expect(screen.queryByText(/abcdef1234567890abcdef/)).toBeNull()
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
        expect(screen.getByText('归档格式: ZIP')).toBeTruthy()
        expect(screen.getByText('归档项目数: 128 项')).toBeTruthy()
      })
      expect(screen.queryByText('archive: zip')).toBeNull()
      expect(screen.queryByText('entries: 128')).toBeNull()
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
        expect(screen.getByPlaceholderText('按路径筛选')).toBeTruthy()
        expect(screen.getAllByText('数据校验').length).toBeGreaterThan(0)
      })
    })

    it('renders an admin-only user filter input', async () => {
      const { unmount } = render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getByPlaceholderText('按用户筛选')).toBeTruthy()
      })

      unmount()
      useIsAdminMock.mockReturnValue(false)
      useUserMock.mockReturnValue({ id: 'member-id', username: 'member', role: 'user', homeDir: '/member' })

      render(<ActivityPage />)

      await waitFor(() => {
        expect(screen.getAllByText(/共 3 条记录/).length).toBeGreaterThan(0)
      })

      expect(screen.queryByPlaceholderText('按用户筛选')).toBeNull()
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
        expect(screen.getByText('当前筛选:')).toBeTruthy()
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
        expect(screen.getByText('当前筛选:')).toBeTruthy()
        expect(screen.getByText('分组：分享相关')).toBeTruthy()
        expect(screen.getByText('当前筛选结果')).toBeTruthy()
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
        expect(screen.getByText('当前筛选:')).toBeTruthy()
      })
    })

    it('requests the entered user and shows the active user filter chip', async () => {
      render(<ActivityPage />)

      const userFilterInput = await screen.findByPlaceholderText('按用户筛选')
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
        expect(screen.getByText('当前筛选:')).toBeTruthy()
        expect(screen.getByText('用户：user1')).toBeTruthy()
      })
    })

    it('requests the entered path and shows the active path filter chip', async () => {
      render(<ActivityPage />)

      const pathFilterInput = await screen.findByPlaceholderText('按路径筛选')
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
        expect(screen.getByText('当前筛选:')).toBeTruthy()
        expect(screen.getByText('路径：/photos')).toBeTruthy()
        expect(screen.getByText('当前筛选结果')).toBeTruthy()
      })
    })

    it('keeps invalid path filters local and lets users clear the invalid state', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<ActivityPage />)

      const pathFilterInput = await screen.findByPlaceholderText('按路径筛选')
      await waitFor(() => {
        expect(mockListActivity).toHaveBeenCalled()
        expect(mockGetActivityStats).toHaveBeenCalled()
      })

      mockListActivity.mockClear()
      mockGetActivityStats.mockClear()
      fireEvent.change(pathFilterInput, { target: { value: '../secret' } })

      await waitFor(() => {
        expect(screen.getAllByText('路径不能包含 .. 或控制字符').length).toBeGreaterThan(0)
        expect(pathFilterInput).toHaveAttribute('aria-invalid', 'true')
      })
      expect(mockListActivity).not.toHaveBeenCalled()
      expect(mockGetActivityStats).not.toHaveBeenCalled()
      expect(screen.queryByText('路径：/../secret')).toBeNull()
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
        expect(screen.getByText('当前筛选:')).toBeTruthy()
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

      const pathFilterInput = await screen.findByPlaceholderText('按路径筛选')
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
        expect(screen.getByText('当前筛选:')).toBeTruthy()
      })

      await user.click(screen.getByText('清除筛选'))

      await waitFor(() => {
        expect(screen.queryByText('当前筛选:')).toBeNull()
      })
    })

    it('clears the active user filter from the input clear action', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<ActivityPage />)

      const userFilterInput = await screen.findByPlaceholderText('按用户筛选')
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

      const pathFilterInput = await screen.findByPlaceholderText('按路径筛选')
      fireEvent.change(pathFilterInput, { target: { value: 'photos' } })

      await waitFor(() => {
        expect(screen.getByText('路径：/photos')).toBeTruthy()
      })

      const userFilterInput = await screen.findByPlaceholderText('按用户筛选')
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
        expect(screen.queryByText('当前筛选:')).toBeNull()
        expect((screen.getByPlaceholderText('按路径筛选') as HTMLInputElement).value).toBe('')
        expect((screen.getByPlaceholderText('按用户筛选') as HTMLInputElement).value).toBe('')
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

      const pathFilterInput = await screen.findByPlaceholderText('按路径筛选')
      await waitFor(() => {
        expect(mockListActivity).toHaveBeenCalled()
        expect(mockGetActivityStats).toHaveBeenCalled()
      })

      mockListActivity.mockClear()
      mockGetActivityStats.mockClear()
      mockAddToast.mockClear()
      fireEvent.change(pathFilterInput, { target: { value: '../secret' } })

      await waitFor(() => {
        expect(screen.getAllByText('路径不能包含 .. 或控制字符').length).toBeGreaterThan(0)
      })

      await user.click(screen.getByText('刷新'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '路径筛选无效',
          description: '路径不能包含 .. 或控制字符',
          color: 'warning',
        })
      })
      expect(mockListActivity).not.toHaveBeenCalled()
      expect(mockGetActivityStats).not.toHaveBeenCalled()
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
        expect(mockAddToast).toHaveBeenCalledWith({ title: '最近操作已清空', color: 'success' })
      })
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
