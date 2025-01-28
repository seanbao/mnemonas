import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { clearActivity, getActivityStats, listActivity } from './activity'

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

  it('reads structured activity stats errors', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      statusText: 'Internal Server Error',
      json: () => Promise.resolve({ success: false, error: { message: 'stats unavailable' } }),
    })

    await expect(getActivityStats()).rejects.toThrow('stats unavailable')
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
})