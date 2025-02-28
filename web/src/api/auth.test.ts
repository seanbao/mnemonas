import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getCurrentUser, login } from './auth'

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
          access_token: 'access-1',
          refresh_token: 'refresh-1',
          expires_at: '2026-03-13T00:00:00Z',
          token_type: 'Bearer',
          user: { id: 'u1', username: 'admin', role: 'admin', homeDir: '/' },
        }),
      })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true }) })

    await login('admin', 'password')

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
          user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/' },
        }),
      })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true }) })

    await getCurrentUser()

    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/auth/download-session', expect.objectContaining({
      method: 'POST',
      headers: { Authorization: 'Bearer access-1' },
    }))
  })
})