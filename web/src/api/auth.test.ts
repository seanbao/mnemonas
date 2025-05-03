import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  AUTH_CLEARED_EVENT,
  AuthError,
  authFetch,
  ensureDownloadSession,
  getAuthHeaders,
  getCurrentUser,
  getStoredUser,
  login,
  logout,
  refreshAuthSession,
} from './auth'

const fetchMock = vi.fn()

describe('auth API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    global.fetch = fetchMock as typeof fetch
  })

  it('treats download session as ready when no auth token is stored', async () => {
    await expect(ensureDownloadSession()).resolves.toEqual({ ok: true })
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('identifies common auth error statuses', () => {
    expect(new AuthError('unauthorized', 401).isUnauthorized).toBe(true)
    expect(new AuthError('forbidden', 403).isForbidden).toBe(true)
  })

  it('returns empty auth state when nothing is stored', async () => {
    expect(getStoredUser()).toBeNull()
    expect(getAuthHeaders()).toEqual({})
    await expect(getCurrentUser()).resolves.toBeNull()
  })

  it('clears tokens without calling logout when no token is stored', async () => {
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))

    await expect(logout()).resolves.toEqual({ warning: false, message: undefined })
    expect(fetchMock).not.toHaveBeenCalled()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it('syncs download session directly when an auth token is stored', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    fetchMock.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true }) })

    await expect(ensureDownloadSession()).resolves.toEqual({ ok: true })
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/download-session', expect.objectContaining({
      method: 'POST',
      headers: { Authorization: 'Bearer access-1' },
    }))
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

    await expect(login('admin', 'password')).resolves.toMatchObject({
      user: { homeDir: '/' },
      warning: false,
      message: undefined,
    })

    expect(getStoredUser()).toMatchObject({ homeDir: '/' })

    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/auth/download-session', expect.objectContaining({
      method: 'POST',
      headers: { Authorization: 'Bearer access-1' },
    }))
  })

  it('returns warning metadata for successful login responses with warning headers', async () => {
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: { get: (name: string) => name === 'Warning' ? '199 MnemoNAS "activity log persistence failed"' : null },
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

    await expect(login('admin', 'password')).resolves.toMatchObject({
      user: { username: 'admin' },
      warning: true,
      message: undefined,
    })
  })

  it('surfaces structured login failures', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 403,
      json: () => Promise.resolve({
        success: false,
        error: {
          code: 'USER_DISABLED',
          message: 'account disabled',
        },
      }),
    })

    await expect(login('admin', 'password')).rejects.toMatchObject({
      message: 'account disabled',
      status: 403,
      code: 'USER_DISABLED',
    })
  })

  it('uses the default login failure when the error body is unreadable', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 500,
      json: () => Promise.reject(new Error('invalid json')),
    })

    await expect(login('admin', 'password')).rejects.toMatchObject({
      message: '登录失败',
      status: 500,
    })
  })

  it('returns a warning when download session sync fails after login', async () => {
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: { get: () => null },
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
      .mockResolvedValueOnce({
        ok: false,
        status: 503,
        json: () => Promise.resolve({
          success: false,
          error: { message: 'download session unavailable' },
        }),
      })

    await expect(login('admin', 'password')).resolves.toMatchObject({
      user: { username: 'admin' },
      warning: true,
      message: 'download session unavailable',
    })
    expect(localStorage.getItem('mnemonas_token')).toBe('access-1')
    expect(localStorage.getItem('mnemonas_refresh_token')).toBe('refresh-1')
  })

  it('returns a warning when download session sync throws after login', async () => {
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: { get: () => null },
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
      .mockRejectedValueOnce(new Error('network down'))

    await expect(login('admin', 'password')).resolves.toMatchObject({
      user: { username: 'admin' },
      warning: true,
      message: '原始预览和下载会话同步失败，请稍后重试',
    })
  })

  it('rejects malformed successful login responses instead of storing fake session state', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          access_token: 'access-1',
          refresh_token: 'refresh-1',
          expires_at: '2026-03-13T00:00:00Z',
          token_type: 'Bearer',
          user: { id: 'u1', username: 'admin', role: 'admin' },
        },
      }),
    })

    await expect(login('admin', 'password')).rejects.toMatchObject({
      message: '登录响应无效',
      status: 200,
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('rejects successful login responses that omit required tokens', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          refresh_token: 'refresh-1',
          expires_at: '2026-03-13T00:00:00Z',
          token_type: 'Bearer',
          user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/' },
        },
      }),
    })

    await expect(login('admin', 'password')).rejects.toMatchObject({
      message: '登录响应无效',
      status: 200,
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('rejects successful login responses with an invalid home directory', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          access_token: 'access-1',
          refresh_token: 'refresh-1',
          expires_at: '2026-03-13T00:00:00Z',
          token_type: 'Bearer',
          user: { id: 'u1', username: 'tester', role: 'user', home_dir: '   ' },
        },
      }),
    })

    await expect(login('tester', 'password')).rejects.toMatchObject({
      message: '登录响应无效',
      status: 200,
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it('rejects false-success login responses instead of storing fake session state', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: false,
        data: {
          access_token: 'access-1',
          refresh_token: 'refresh-1',
          expires_at: '2026-03-13T00:00:00Z',
          token_type: 'Bearer',
          user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/' },
        },
      }),
    })

    await expect(login('admin', 'password')).rejects.toMatchObject({
      message: '登录响应无效',
      status: 200,
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
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

  it('clears local auth state when current user payload is malformed', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          user: { id: 'u1', username: 'admin', role: 'admin' },
        },
      }),
    })

    await expect(getCurrentUser()).resolves.toBeNull()
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it('clears local auth state when current user payload contains an invalid home directory', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '../secret' },
        },
      }),
    })

    await expect(getCurrentUser()).resolves.toBeNull()
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it('returns warning metadata for successful logout responses with warning headers', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    fetchMock.mockResolvedValueOnce({
      ok: true,
      headers: { get: (name: string) => name === 'Warning' ? '199 MnemoNAS "activity log persistence failed"' : null },
      json: () => Promise.resolve({ success: true, data: null }),
    })

    await expect(logout()).resolves.toMatchObject({
      warning: true,
      message: undefined,
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
  })

  it('returns a default logout result when the success body is unreadable', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    fetchMock.mockResolvedValueOnce({
      ok: true,
      headers: { get: () => null },
      json: () => Promise.reject(new Error('invalid json')),
    })

    await expect(logout()).resolves.toEqual({ warning: false, message: undefined })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
  })

  it('surfaces network failures during logout', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    fetchMock.mockRejectedValueOnce(new Error('network down'))

    await expect(logout()).rejects.toMatchObject({
      message: '退出登录失败',
      status: 0,
    })
    expect(localStorage.getItem('mnemonas_token')).toBe('access-1')
  })

  it('preserves local auth state when logout fails', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 500,
      json: () => Promise.resolve({
        success: false,
        error: {
          code: 'LOGOUT_FAILED',
          message: 'logout unavailable',
        },
      }),
    })

    await expect(logout()).rejects.toMatchObject({
      message: 'logout unavailable',
      status: 500,
      code: 'LOGOUT_FAILED',
    })
    expect(localStorage.getItem('mnemonas_token')).toBe('access-1')
    expect(localStorage.getItem('mnemonas_refresh_token')).toBe('refresh-1')
    expect(JSON.parse(localStorage.getItem('mnemonas_user') ?? '{}')).toMatchObject({ username: 'admin' })
  })

  it('preserves local auth state when current user lookup is temporarily unavailable', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: () => Promise.resolve({
        success: false,
        error: {
          code: 'SERVICE_UNAVAILABLE',
          message: 'auth unavailable',
        },
      }),
    })

    await expect(getCurrentUser()).rejects.toMatchObject({
      message: 'auth unavailable',
      status: 503,
      code: 'SERVICE_UNAVAILABLE',
    })
    expect(localStorage.getItem('mnemonas_token')).toBe('access-1')
    expect(localStorage.getItem('mnemonas_refresh_token')).toBe('refresh-1')
    expect(localStorage.getItem('mnemonas_user')).not.toBeNull()
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

  it('reports refreshAuthSession failure when download session sync does not complete', async () => {
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    fetchMock
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
      .mockResolvedValueOnce({
        ok: false,
        status: 503,
        json: () => Promise.resolve({
          success: false,
          error: { message: 'download session unavailable' },
        }),
      })

    await expect(refreshAuthSession()).resolves.toBe(false)
    expect(localStorage.getItem('mnemonas_token')).toBe('access-2')
    expect(localStorage.getItem('mnemonas_refresh_token')).toBe('refresh-2')
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

  it('does not retry unauthorized requests when no refresh token exists', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')

    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 401,
      statusText: 'Unauthorized',
      json: () => Promise.reject(new Error('invalid json')),
    })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
  })

  it('retries unauthorized requests even when URL parsing falls back to the raw input', async () => {
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

    const response = await authFetch('http://[')

    expect(response.ok).toBe(true)
    expect(fetchMock).toHaveBeenCalledTimes(4)
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

  it('clears auth state when refresh returns a malformed success payload', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))

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
            user: { id: 'u1', username: 'admin', role: 'admin' },
          },
        }),
      })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('clears auth state when refresh returns a false-success payload', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))

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
          success: false,
          data: {
            access_token: 'access-2',
            refresh_token: 'refresh-2',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/' },
          },
        }),
      })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(2)
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

  it('clears stored user payloads with an invalid home directory', () => {
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '   ',
    }))

    expect(getStoredUser()).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it('clears corrupted stored user payloads', () => {
    localStorage.setItem('mnemonas_user', '{invalid-json')

    expect(getStoredUser()).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
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

  it('clears local auth state when an authenticated request reports USER_NOT_FOUND', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'user',
      role: 'user',
      home_dir: '/users/user',
    }))

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
            user: { id: 'u1', username: 'user', role: 'user', home_dir: '/users/user' },
          },
        }),
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true }),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        json: () => Promise.resolve({
          success: false,
          error: {
            code: 'USER_NOT_FOUND',
            message: 'user not found',
          },
        }),
      })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(4)
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)

    const event = authCleared.mock.calls[0]?.[0] as CustomEvent<{ message?: string; reason?: string }>
    expect(event.detail).toEqual({
      reason: 'missing',
      message: 'user not found',
    })

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('clears local auth state on terminal unauthorized responses without a structured body', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'user',
      role: 'user',
      home_dir: '/users/user',
    }))

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
            user: { id: 'u1', username: 'user', role: 'user', home_dir: '/users/user' },
          },
        }),
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true }),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        json: () => Promise.reject(new Error('invalid json')),
      })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(4)
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)

    const event = authCleared.mock.calls[0]?.[0] as CustomEvent<{ message?: string; reason?: string }>
    expect(event.detail).toEqual({
      reason: 'expired',
      message: '登录已过期，请重新登录',
    })

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('clears local auth state when an authenticated request reports USER_DISABLED', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'user',
      role: 'user',
      home_dir: '/users/user',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 403,
      json: () => Promise.resolve({
        success: false,
        error: {
          code: 'USER_DISABLED',
          message: 'user account is disabled',
        },
      }),
    })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(403)
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)

    const event = authCleared.mock.calls[0]?.[0] as CustomEvent<{ message?: string; reason?: string }>
    expect(event.detail).toEqual({
      reason: 'disabled',
      message: 'user account is disabled',
    })

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('clears local auth state when current user lookup reports USER_DISABLED', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'user',
      role: 'user',
      home_dir: '/users/user',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 403,
      json: () => Promise.resolve({
        success: false,
        error: {
          code: 'USER_DISABLED',
          message: 'user account is disabled',
        },
      }),
    })

    await expect(getCurrentUser()).resolves.toBeNull()

    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('clears local auth state when current user lookup returns invalid JSON', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'user',
      role: 'user',
      home_dir: '/users/user',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.reject(new Error('invalid json')),
    })

    await expect(getCurrentUser()).resolves.toBeNull()

    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('clears local auth state when current user lookup returns no user payload', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'user',
      role: 'user',
      home_dir: '/users/user',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ success: true }),
    })

    await expect(getCurrentUser()).resolves.toBeNull()

    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

})
