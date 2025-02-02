import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AUTH_CLEARED_EVENT, authFetch, getCurrentUser, getStoredUser, login } from './auth'

const fetchMock = vi.fn()

describe('auth API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    global.fetch = fetchMock as typeof fetch
  })

  it('syncs download session after login', async () => {
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-1',
            refresh_token: 'refresh-1',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/' },
          },
        }),
      })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true }) })

    await login('admin', 'password')

    expect(getStoredUser()).toMatchObject({ homeDir: '/' })

    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/auth/download-session', expect.objectContaining({
      method: 'POST',
      headers: { Authorization: 'Bearer access-1' },
    }))
  })

  it('syncs download session after loading current user', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/' },
          },
        }),
      })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true }) })

    await getCurrentUser()

    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/auth/download-session', expect.objectContaining({
      method: 'POST',
      headers: { Authorization: 'Bearer access-1' },
    }))
  })

  it('retries once after refreshing token', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-2',
            refresh_token: 'refresh-2',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/' },
          },
        }),
      })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })

    const response = await authFetch('/api/v1/files')

    expect(response.ok).toBe(true)
    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/v1/files', expect.objectContaining({
      headers: expect.any(Headers),
    }))
    expect((fetchMock.mock.calls[0]?.[1]?.headers as Headers).get('Authorization')).toBe('Bearer access-1')
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/auth/refresh', expect.objectContaining({
      method: 'POST',
    }))
    expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/v1/auth/download-session', expect.objectContaining({
      method: 'POST',
      headers: { Authorization: 'Bearer access-2' },
    }))
    expect(fetchMock).toHaveBeenNthCalledWith(4, '/api/v1/files', expect.objectContaining({
      headers: expect.any(Headers),
    }))
    expect((fetchMock.mock.calls[3]?.[1]?.headers as Headers).get('Authorization')).toBe('Bearer access-2')
  })

  it('does not retry refresh endpoint after 401', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 401,
      statusText: 'Unauthorized',
    })

    const response = await authFetch('/api/v1/auth/refresh', { method: 'POST' })

    expect(response.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('stops after one retry when retried request is still unauthorized', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-2',
            refresh_token: 'refresh-2',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/' },
          },
        }),
      })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })
      .mockResolvedValueOnce({ ok: false, status: 401, statusText: 'Unauthorized' })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(4)
  })

  it('normalizes legacy stored user payloads', () => {
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/legacy',
    }))

    expect(getStoredUser()).toMatchObject({ homeDir: '/legacy' })
  })

  it('preserves Headers instances and custom headers across retry', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    fetchMock
      .mockResolvedValueOnce({ ok: false, status: 401, statusText: 'Unauthorized' })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-2',
            refresh_token: 'refresh-2',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/' },
          },
        }),
      })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })

    const customHeaders = new Headers({
      'Content-Type': 'application/json',
      'X-Trace-Id': 'trace-1',
    })

    await authFetch('/api/v1/files', {
      method: 'POST',
      headers: customHeaders,
      body: JSON.stringify({ name: 'demo' }),
    })

    const firstRequestHeaders = fetchMock.mock.calls[0]?.[1]?.headers as Headers
    const retriedRequestHeaders = fetchMock.mock.calls[3]?.[1]?.headers as Headers

    expect(firstRequestHeaders).toBeInstanceOf(Headers)
    expect(firstRequestHeaders.get('Authorization')).toBe('Bearer access-1')
    expect(firstRequestHeaders.get('Content-Type')).toBe('application/json')
    expect(firstRequestHeaders.get('X-Trace-Id')).toBe('trace-1')

    expect(retriedRequestHeaders).toBeInstanceOf(Headers)
    expect(retriedRequestHeaders.get('Authorization')).toBe('Bearer access-2')
    expect(retriedRequestHeaders.get('Content-Type')).toBe('application/json')
    expect(retriedRequestHeaders.get('X-Trace-Id')).toBe('trace-1')
  })

  it('shares a single refresh request across concurrent unauthorized calls', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    let fileAttempts = 0
    fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)

      if (url === '/api/v1/files') {
        fileAttempts += 1
        if (fileAttempts <= 2) {
          return { ok: false, status: 401, statusText: 'Unauthorized' } as Response
        }

        const authHeader = (init?.headers as Headers).get('Authorization')
        expect(authHeader).toBe('Bearer access-2')
        return { ok: true, status: 200, json: () => Promise.resolve({ success: true }) } as Response
      }

      if (url === '/api/v1/auth/refresh') {
        return {
          ok: true,
          status: 200,
          json: () => Promise.resolve({
            success: true,
            data: {
              access_token: 'access-2',
              refresh_token: 'refresh-2',
              expires_at: '2026-03-13T00:00:00Z',
              token_type: 'Bearer',
              user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/' },
            },
          }),
        } as Response
      }

      if (url === '/api/v1/auth/download-session') {
        return { ok: true, status: 200, json: () => Promise.resolve({ success: true }) } as Response
      }

      throw new Error(`unexpected fetch: ${url}`)
    })

    const [first, second] = await Promise.all([
      authFetch('/api/v1/files'),
      authFetch('/api/v1/files'),
    ])

    expect(first.ok).toBe(true)
    expect(second.ok).toBe(true)
    expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/auth/refresh')).toHaveLength(1)
    expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/auth/download-session')).toHaveLength(1)
  })

  it('dispatches auth-cleared when refresh fails', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(authCleared).toHaveBeenCalledTimes(1)
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })
})