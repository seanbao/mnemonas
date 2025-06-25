import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { ApiError, clearActivity, getActivityStats, listActivity } from './activity'

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
    expect(result.total).toBe(1)
    expect(result.limit).toBe(20)
    expect(result.offset).toBe(40)
  })

  it('rejects malformed successful activity list responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ items: [] }),
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
        },
      }),
    })

    const result = await getActivityStats()

    expect(result.total).toBe(12)
    expect(result.today).toBe(3)
    expect(result.by_action.login).toBe(3)
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

  it('reads structured activity stats errors', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      statusText: 'Internal Server Error',
      json: () => Promise.resolve({ success: false, error: { message: 'stats unavailable' } }),
    })

    await expect(getActivityStats()).rejects.toThrow('stats unavailable')
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

  it('marks ApiError as unavailable for 503 responses', () => {
    const error = new ApiError('activity log unavailable', 503, 'Service Unavailable', 'SERVICE_UNAVAILABLE')

    expect(error.isUnavailable).toBe(true)
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
})