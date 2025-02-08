import { beforeEach, describe, expect, it, vi } from 'vitest'
import { acknowledgeSetup, getSetupStatus } from './setup'

vi.mock('./auth', () => ({
  authFetch: vi.fn(),
}))

import { authFetch } from './auth'

describe('Setup API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    global.fetch = vi.fn() as typeof fetch
  })

  it('returns setup status payload', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, is_first_run: true, auth_enabled: true, webdav_enabled: true, webdav_auth_type: 'basic' }),
    } as Response)

    const result = await getSetupStatus()

    expect(result.is_first_run).toBe(true)
    expect(result.webdav_auth_type).toBe('basic')
  })

  it('reads legacy string error for acknowledge setup', async () => {
    vi.mocked(authFetch).mockResolvedValueOnce({
      ok: false,
      json: () => Promise.resolve({ success: false, error: 'failed to acknowledge setup' }),
    } as Response)

    await expect(acknowledgeSetup()).rejects.toThrow('failed to acknowledge setup')
  })

  it('reads structured error for acknowledge setup', async () => {
    vi.mocked(authFetch).mockResolvedValueOnce({
      ok: false,
      json: () => Promise.resolve({ code: 'INTERNAL_ERROR', message: 'failed to acknowledge setup' }),
    } as Response)

    await expect(acknowledgeSetup()).rejects.toThrow('failed to acknowledge setup')
  })
})