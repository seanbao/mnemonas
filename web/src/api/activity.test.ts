import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import {
  ACTIVITY_ACTIONS,
  ApiError,
  clearActivity,
  createActivityReviewRecord,
  getActionColor,
  getActionLabel,
  getActivityStats,
  listActivity,
  listActivityReviewRecords,
  updateActivityReviewRecordDisposition,
  type ActionType,
} from './activity'

vi.mock('./auth', () => ({
  authFetch: vi.fn(),
}))

import { authFetch } from './auth'

const mockAuthFetch = authFetch as Mock

describe('Activity API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('unwraps activity list responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [{ id: '1', timestamp: '2026-03-14T00:00:00Z', action: 'login', user: 'admin' }],
          total: 1,
          limit: 50,
          offset: 0,
        },
      }),
    })

    const result = await listActivity()

    expect(result.total).toBe(1)
    expect(result.items[0].action).toBe('login')
  })

  it('accepts favorite-related activity action types', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [{ id: '1', timestamp: '2026-03-14T00:00:00Z', action: 'favorite_note_update', path: '/docs/a.txt', user: 'admin' }],
          total: 1,
          limit: 50,
          offset: 0,
        },
      }),
    })

    const result = await listActivity()

    expect(result.items[0].action).toBe('favorite_note_update')
  })

  it('derives activity summary fields from returned items and request defaults when missing', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [{ id: '1', timestamp: '2026-03-14T00:00:00Z', action: 'login', user: 'admin' }],
        },
      }),
    })

    const result = await listActivity({ limit: 20, offset: 40 })

    expect(result.items).toHaveLength(1)
    expect(result.total).toBe(41)
    expect(result.limit).toBe(20)
    expect(result.offset).toBe(40)
  })

  it('sends optional activity filters as query parameters', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [],
          total: 0,
          limit: 10,
          offset: 20,
        },
      }),
    })

    await listActivity({
      limit: 10,
      offset: 20,
      action: 'delete',
      actionGroup: 'risk',
      user: 'admin',
      path: '/photos',
      since: '2026-05-01T00:00:00Z',
      until: '2026-05-02T00:00:00Z',
    })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/activity/?limit=10&offset=20&action=delete&action_group=risk&user=admin&path=%2Fphotos&since=2026-05-01T00%3A00%3A00Z&until=2026-05-02T00%3A00%3A00Z')
  })

  it('normalizes activity list path filters before sending requests', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [],
          total: 0,
          limit: 50,
          offset: 0,
        },
      }),
    })

    await listActivity({ path: 'photos' })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/activity/?path=%2Fphotos')
  })

  it('rejects invalid activity list path filters before sending requests', async () => {
    await expect(listActivity({ path: '/photos/./secret' })).rejects.toThrow('非法路径')
    expect(mockAuthFetch).not.toHaveBeenCalled()
  })

  it('forwards abort signals to the authenticated fetch request', async () => {
    const controller = new AbortController()
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [],
          total: 0,
          limit: 10,
          offset: 0,
        },
      }),
    })

    await listActivity({ limit: 10, signal: controller.signal })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/activity/?limit=10', {
      signal: controller.signal,
    })
  })

  it('rejects malformed successful activity list responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ items: [] }),
    })

    await expect(listActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects activity list responses with invalid pagination counts', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [],
          total: -1,
          limit: 20.5,
          offset: 0,
        },
      }),
    })

    await expect(listActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it.each([
    ['unsafe total', { total: 9007199254740992, limit: 20, offset: 0 }],
    ['unsafe limit', { total: 0, limit: 9007199254740992, offset: 0 }],
    ['unsafe offset', { total: 0, limit: 20, offset: 9007199254740992 }],
  ])('rejects activity list responses with %s', async (_label, pagination) => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [],
          ...pagination,
        },
      }),
    })

    await expect(listActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects activity list responses with inconsistent pagination totals', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [
            { id: '1', timestamp: '2026-03-14T00:00:00Z', action: 'login', user: 'admin' },
            { id: '2', timestamp: '2026-03-14T00:01:00Z', action: 'logout', user: 'admin' },
          ],
          total: 1,
          limit: 50,
          offset: 0,
        },
      }),
    })

    await expect(listActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects activity list responses with duplicate entry IDs', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [
            { id: 'duplicate', timestamp: '2026-03-14T00:00:00Z', action: 'login', user: 'admin' },
            { id: 'duplicate', timestamp: '2026-03-14T00:01:00Z', action: 'logout', user: 'admin' },
          ],
          total: 2,
          limit: 50,
          offset: 0,
        },
      }),
    })

    await expect(listActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects malformed successful activity list entries', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [{ timestamp: '2026-03-14T00:00:00Z', action: 'login', user: 'admin' }],
          total: 1,
          limit: 50,
          offset: 0,
        },
      }),
    })

    await expect(listActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects activity entries with empty IDs', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [{ id: '', timestamp: '2026-03-14T00:00:00Z', action: 'login', user: 'admin' }],
          total: 1,
          limit: 50,
          offset: 0,
        },
      }),
    })

    await expect(listActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects activity entries with noncanonical IDs', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [{ id: ' activity-id ', timestamp: '2026-03-14T00:00:00Z', action: 'login', user: 'admin' }],
          total: 1,
          limit: 50,
          offset: 0,
        },
      }),
    })

    await expect(listActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects activity entries with invalid timestamps', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [{ id: '1', timestamp: 'not-a-date', action: 'login', user: 'admin' }],
          total: 1,
          limit: 50,
          offset: 0,
        },
      }),
    })

    await expect(listActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it.each([
    ['relative path', 'docs/report.txt'],
    ['trailing-slash path', '/docs/report.txt/'],
    ['unsafe path', '/docs/./report.txt'],
  ])('rejects activity entries with noncanonical %s', async (_label, path) => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [{ id: '1', timestamp: '2026-03-14T00:00:00Z', action: 'upload', path }],
          total: 1,
          limit: 50,
          offset: 0,
        },
      }),
    })

    await expect(listActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('accepts activity entries with canonical and hidden path details', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [{
            id: '1',
            timestamp: '2026-03-14T00:00:00Z',
            action: 'move',
            path: '/docs/source.txt',
            details: {
              to: '/docs/target.txt',
              from: '',
              note: 'external reference /not-a-logical-path/',
            },
          }],
          total: 1,
          limit: 50,
          offset: 0,
        },
      }),
    })

    const result = await listActivity()

    expect(result.items[0].details?.to).toBe('/docs/target.txt')
    expect(result.items[0].details?.from).toBe('')
  })

  it.each([
    ['relative detail path', { to: 'docs/report.txt' }],
    ['trailing-slash detail path', { target_path: '/docs/report.txt/' }],
    ['unsafe detail path', { from: '/docs/./report.txt' }],
  ])('rejects activity entries with noncanonical %s', async (_label, details) => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [{
            id: '1',
            timestamp: '2026-03-14T00:00:00Z',
            action: 'move',
            path: '/docs/source.txt',
            details,
          }],
          total: 1,
          limit: 50,
          offset: 0,
        },
      }),
    })

    await expect(listActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects activity entries with non-string detail values', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [{
            id: '1',
            timestamp: '2026-03-14T00:00:00Z',
            action: 'login',
            user: 'admin',
            details: { attempts: 2 },
          }],
          total: 1,
          limit: 50,
          offset: 0,
        },
      }),
    })

    await expect(listActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects unreadable successful activity list responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.reject(new SyntaxError('bad json')),
    })

    await expect(listActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('unwraps valid activity stats responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          total: 12,
          today: 3,
          by_action: { login: 3, upload: 2 },
          by_user: { admin: 8, guest: 4 },
          risk_summary: {
            total: 5,
            today: 2,
            max_10m: 3,
            max_10m_started_at: '2026-05-01T10:00:00Z',
            max_10m_ended_at: '2026-05-01T10:08:00Z',
          },
        },
      }),
    })

    const result = await getActivityStats()

    expect(result.total).toBe(12)
    expect(result.today).toBe(3)
    expect(result.by_action.login).toBe(3)
    expect(result.risk_summary?.max_10m).toBe(3)
  })

  it('forwards abort signal when fetching activity stats', async () => {
    const controller = new AbortController()
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          total: 12,
          today: 3,
          by_action: { login: 3 },
          by_user: { admin: 8 },
        },
      }),
    })

    await getActivityStats({ signal: controller.signal })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/activity/stats', {
      signal: controller.signal,
    })
  })

  it('sends optional activity stats filters as query parameters', async () => {
    const controller = new AbortController()
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          total: 2,
          today: 1,
          by_action: { upload: 2 },
          by_user: { admin: 2 },
        },
      }),
    })

    await getActivityStats({
      action: 'upload',
      actionGroup: 'share',
      user: 'admin',
      path: '/photos',
      since: '2026-05-01T00:00:00Z',
      until: '2026-05-02T00:00:00Z',
      signal: controller.signal,
    })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/activity/stats?action=upload&action_group=share&user=admin&path=%2Fphotos&since=2026-05-01T00%3A00%3A00Z&until=2026-05-02T00%3A00%3A00Z', {
      signal: controller.signal,
    })
  })

  it('rejects invalid activity stats path filters before sending requests', async () => {
    await expect(getActivityStats({ path: '/photos/./secret' })).rejects.toThrow('非法路径')
    expect(mockAuthFetch).not.toHaveBeenCalled()
  })

  it('rejects malformed successful activity stats responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          total: 12,
          by_action: [],
        },
      }),
    })

    await expect(getActivityStats()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects activity stats responses with non-numeric map values', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          total: 12,
          today: 3,
          by_action: { login: '3' },
          by_user: { admin: 8 },
        },
      }),
    })

    await expect(getActivityStats()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects activity stats responses with negative or fractional counts', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          total: 12,
          today: 1.5,
          by_action: { login: -1 },
          by_user: { admin: 8 },
          risk_summary: {
            total: 2,
            today: 1,
            max_10m: 1,
          },
        },
      }),
    })

    await expect(getActivityStats()).rejects.toThrow('服务器返回了无效的数据')
  })

  it.each([
    ['unsafe total', { total: 9007199254740992, today: 3, by_action: { login: 3 }, by_user: { admin: 8 } }],
    ['unsafe today', { total: 12, today: 9007199254740992, by_action: { login: 3 }, by_user: { admin: 8 } }],
    ['unsafe action count', { total: 12, today: 3, by_action: { login: 9007199254740992 }, by_user: { admin: 8 } }],
    ['unsafe user count', { total: 12, today: 3, by_action: { login: 3 }, by_user: { admin: 9007199254740992 } }],
    ['unsafe risk summary total', { total: 12, today: 3, by_action: { login: 3 }, by_user: { admin: 8 }, risk_summary: { total: 9007199254740992, today: 1, max_10m: 1 } }],
    ['unsafe risk summary max window', { total: 12, today: 3, by_action: { login: 3 }, by_user: { admin: 8 }, risk_summary: { total: 1, today: 1, max_10m: 9007199254740992 } }],
  ])('rejects activity stats responses with %s', async (_label, data) => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data,
      }),
    })

    await expect(getActivityStats()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects activity stats responses with unknown action keys', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          total: 12,
          today: 3,
          by_action: { login: 3, unknown_action: 1 },
          by_user: { admin: 8 },
        },
      }),
    })

    await expect(getActivityStats()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects malformed activity risk summaries', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          total: 12,
          today: 3,
          by_action: { login: 3 },
          by_user: { admin: 8 },
          risk_summary: { total: 1, today: 1, max_10m: '3' },
        },
      }),
    })

    await expect(getActivityStats()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects activity risk summaries with invalid review windows', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          total: 12,
          today: 3,
          by_action: { delete: 5 },
          by_user: { admin: 8 },
          risk_summary: {
            total: 5,
            today: 3,
            max_10m: 5,
            max_10m_started_at: '2026-05-01T10:08:00Z',
            max_10m_ended_at: '2026-05-01T10:00:00Z',
          },
        },
      }),
    })

    await expect(getActivityStats()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('reads structured activity stats errors', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      statusText: 'Internal Server Error',
      json: () => Promise.resolve({ success: false, error: { message: 'stats unavailable' } }),
    })

    await expect(getActivityStats()).rejects.toThrow('stats unavailable')
  })

  it('surfaces problem-json activity errors', async () => {
    const body = {
      title: 'Service unavailable',
      detail: 'activity log storage unavailable',
      status: 503,
    }

    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      statusText: 'Service Unavailable',
      headers: new Headers({ 'Content-Type': 'application/problem+json' }),
      clone: () => ({ json: () => Promise.resolve(body) }),
      json: () => Promise.resolve(body),
    })

    await expect(listActivity()).rejects.toMatchObject({
      message: 'activity log storage unavailable',
      status: 503,
      isUnavailable: true,
    })
  })

  it('reads legacy string activity errors', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 400,
      statusText: 'Bad Request',
      json: () => Promise.resolve({ success: false, error: 'invalid activity filter' }),
    })

    await expect(listActivity()).rejects.toMatchObject({
      message: 'invalid activity filter',
      status: 400,
    })
  })

  it('preserves service-unavailable activity error codes', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      statusText: 'Service Unavailable',
      json: () => Promise.resolve({ success: false, error: { code: 'SERVICE_UNAVAILABLE', message: 'activity log unavailable' } }),
    })

    await expect(listActivity()).rejects.toMatchObject({
      message: 'activity log unavailable',
      status: 503,
      code: 'SERVICE_UNAVAILABLE',
      isUnavailable: true,
    })
  })

  it('preserves top-level service-unavailable activity error codes', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      statusText: 'Service Unavailable',
      json: () => Promise.resolve({ code: 'SERVICE_UNAVAILABLE', message: 'activity log unavailable', timestamp: '2026-04-23T00:00:00Z' }),
    })

    await expect(listActivity()).rejects.toMatchObject({
      message: 'activity log unavailable',
      status: 503,
      code: 'SERVICE_UNAVAILABLE',
      isUnavailable: true,
    })
  })

  it('marks ApiError as unavailable for 503 responses', () => {
    const error = new ApiError('activity log unavailable', 503, 'Service Unavailable', 'SERVICE_UNAVAILABLE')

    expect(error.isUnavailable).toBe(true)
  })

  it('unwraps persisted activity review record lists', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [{
            id: 'review-1',
            reviewed_at: '2026-05-01T10:10:00Z',
            reviewer: 'admin',
            note: '已确认误删文件已恢复',
            scope_label: '集中窗口',
            filter_summary: '分组 高风险变更',
            disposition_status: 'restored',
            action_counts: {
              delete: 1,
              move: 1,
            },
            review_count: 3,
            total_count: 5,
            path_count: 2,
            user_count: 1,
            path_samples: ['/docs/deleted.txt', '/docs/moved.txt'],
            user_samples: ['admin'],
            activity_entry_ids: ['delete-1', 'move-1'],
          }],
          total: 1,
          limit: 5,
          offset: 0,
        },
      }),
    })

    const result = await listActivityReviewRecords({
      limit: 5,
      reviewer: 'admin',
      activityEntryId: 'delete-1',
      dispositionStatus: 'restored',
      since: '2026-05-01T00:00:00Z',
      until: '2026-05-02T00:00:00Z',
    })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/activity/reviews?limit=5&reviewer=admin&activity_entry_id=delete-1&disposition_status=restored&since=2026-05-01T00%3A00%3A00Z&until=2026-05-02T00%3A00%3A00Z')
    expect(result.items[0].note).toBe('已确认误删文件已恢复')
    expect(result.items[0].disposition_status).toBe('restored')
    expect(result.items[0].action_counts?.delete).toBe(1)
    expect(result.items[0].path_samples).toEqual(['/docs/deleted.txt', '/docs/moved.txt'])
    expect(result.items[0].activity_entry_ids).toEqual(['delete-1', 'move-1'])
  })

  it('creates persisted activity review records', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          id: 'review-created',
          reviewed_at: '2026-05-01T10:10:00Z',
          reviewer: 'admin',
          note: '分享链接已关闭',
          scope_label: '当前页',
          filter_summary: '未筛选',
          disposition_status: 'disabled',
          action_counts: {
            share: 1,
          },
          review_count: 1,
          total_count: 2,
          path_count: 1,
          user_count: 1,
          path_samples: ['/docs/report.pdf'],
          user_samples: ['admin'],
          activity_entry_ids: ['share-1'],
        },
      }),
    })

    const input = {
      note: '分享链接已关闭',
      scope_label: '当前页',
      filter_summary: '未筛选',
      disposition_status: 'disabled' as const,
      action_counts: {
        share: 1,
      },
      review_count: 1,
      total_count: 2,
      path_count: 1,
      user_count: 1,
      path_samples: ['/docs/report.pdf'],
      user_samples: ['admin'],
      activity_entry_ids: ['share-1'],
    }

    const result = await createActivityReviewRecord(input)

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/activity/reviews', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    })
    expect(result.id).toBe('review-created')
  })

  it('updates persisted activity review disposition status', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          id: 'review-created',
          reviewed_at: '2026-05-01T11:10:00Z',
          reviewer: 'admin',
          note: '分享链接已关闭',
          scope_label: '当前页',
          filter_summary: '未筛选',
          disposition_status: 'disabled',
          action_counts: {
            share: 1,
          },
          review_count: 1,
          total_count: 2,
          path_count: 1,
          user_count: 1,
          path_samples: ['/docs/report.pdf'],
          user_samples: ['admin'],
          activity_entry_ids: ['share-1'],
        },
      }),
    })

    const result = await updateActivityReviewRecordDisposition('review-created', {
      disposition_status: 'disabled',
      note: '分享链接已关闭，访问入口已核对',
    })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/activity/reviews/review-created', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ disposition_status: 'disabled', note: '分享链接已关闭，访问入口已核对' }),
    })
    expect(result.disposition_status).toBe('disabled')
  })

  it('rejects malformed activity review records', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          items: [{
            id: 'review-1',
            reviewed_at: '2026-05-01T10:10:00Z',
            reviewer: 'admin',
            note: '',
            scope_label: '当前页',
            review_count: 0,
            total_count: 0,
            path_count: 0,
            user_count: 0,
            activity_entry_ids: [],
          }],
          total: 1,
          limit: 5,
          offset: 0,
        },
      }),
    })

    await expect(listActivityReviewRecords({ limit: 5 })).rejects.toThrow('服务器返回了无效的数据')
  })

  it('reads clear activity error message from response body', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 403,
      statusText: 'Forbidden',
      json: () => Promise.resolve({ success: false, error: { message: 'admin access required' } }),
    })

    await expect(clearActivity()).rejects.toThrow('admin access required')
  })

  it('unwraps wrapped clear activity success responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      statusText: 'OK',
      json: () => Promise.resolve({
        success: true,
        data: { message: 'Activity log cleared' },
      }),
    })

    await expect(clearActivity()).resolves.toEqual({ message: 'Activity log cleared' })
  })

  it('forwards abort signal when clearing activity', async () => {
    const controller = new AbortController()
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      statusText: 'OK',
      json: () => Promise.resolve({
        success: true,
        data: { message: 'Activity log cleared' },
      }),
    })

    await expect(clearActivity({ signal: controller.signal })).resolves.toEqual({ message: 'Activity log cleared' })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/activity/', {
      method: 'DELETE',
      signal: controller.signal,
    })
  })

  it('rejects malformed successful clear activity responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      statusText: 'OK',
      json: () => Promise.resolve({ message: 'Activity log cleared' }),
    })

    await expect(clearActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects false-success clear activity responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      statusText: 'OK',
      json: () => Promise.resolve({ success: false, data: {} }),
    })

    await expect(clearActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects clear activity responses with non-string messages', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      statusText: 'OK',
      json: () => Promise.resolve({
        success: true,
        data: { message: 42 },
      }),
    })

    await expect(clearActivity()).rejects.toThrow('服务器返回了无效的数据')
  })

  it('maps every activity action to a UI color and falls back for unknown actions', () => {
    const expected: Record<ActionType, ReturnType<typeof getActionColor>> = {
      upload: 'success',
      download: 'primary',
      delete: 'danger',
      rename: 'warning',
      move: 'warning',
      copy: 'primary',
      create: 'success',
      restore: 'success',
      share: 'primary',
      unshare: 'warning',
      favorite: 'primary',
      unfavorite: 'warning',
      favorite_note_update: 'primary',
      login: 'success',
      logout: 'default',
      trash_restore: 'success',
      trash_delete: 'danger',
      trash_empty: 'danger',
      disk_health: 'warning',
      scrub: 'warning',
    }

    expect(ACTIVITY_ACTIONS).toEqual(Object.keys(expected))

    for (const [action, color] of Object.entries(expected) as Array<[ActionType, ReturnType<typeof getActionColor>]>) {
      expect(getActionColor(action)).toBe(color)
    }
    expect(getActionColor('unknown' as ActionType)).toBe('default')
  })

  it('maps every activity action to a Chinese UI label and falls back without exposing raw keys', () => {
    const expected: Record<ActionType, string> = {
      upload: '上传文件',
      download: '下载文件',
      delete: '删除文件',
      rename: '重命名',
      move: '移动文件',
      copy: '复制文件',
      create: '创建文件夹',
      restore: '恢复版本',
      share: '创建分享',
      unshare: '取消分享',
      favorite: '添加收藏',
      unfavorite: '取消收藏',
      favorite_note_update: '更新收藏备注',
      login: '登录',
      logout: '登出',
      trash_restore: '从回收站恢复',
      trash_delete: '从回收站删除',
      trash_empty: '清空回收站',
      disk_health: '磁盘健康异常',
      scrub: '数据校验',
    }

    expect(ACTIVITY_ACTIONS).toEqual(Object.keys(expected))

    for (const [action, label] of Object.entries(expected) as Array<[ActionType, string]>) {
      expect(getActionLabel(action)).toBe(label)
    }
    expect(getActionLabel('unknown' as ActionType)).toBe('未知操作')
  })
})
