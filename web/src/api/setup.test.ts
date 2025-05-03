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

  it('rejects malformed successful setup status responses', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, is_first_run: true }),
    } as Response)

    await expect(getSetupStatus()).rejects.toThrow('Invalid setup status response')
  })

  it('rejects invalid setup status JSON', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.reject(new SyntaxError('bad json')),
    } as Response)

    await expect(getSetupStatus()).rejects.toThrow('Invalid setup status response')
  })

  it('reads structured error for setup status', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: false,
      json: () => Promise.resolve({ code: 'SERVICE_UNAVAILABLE', message: 'setup status unavailable' }),
    } as Response)

    await expect(getSetupStatus()).rejects.toThrow('setup status unavailable')
  })

  it('uses fallback setup status error when the error payload is unreadable', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: false,
      json: () => Promise.reject('bad json'),
    } as Response)

    await expect(getSetupStatus()).rejects.toThrow('Failed to get setup status')
  })

  it('acknowledges setup and defaults a missing message to an empty string', async () => {
    vi.mocked(authFetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true }),
    } as Response)

    await expect(acknowledgeSetup()).resolves.toEqual({
      success: true,
      message: '',
    })
    expect(authFetch).toHaveBeenCalledWith('/api/v1/setup/acknowledge', {
      method: 'POST',
    })
  })

  it('returns the acknowledge setup message when provided', async () => {
    vi.mocked(authFetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, message: 'acknowledged' }),
    } as Response)

    await expect(acknowledgeSetup()).resolves.toEqual({
      success: true,
      message: 'acknowledged',
    })
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

  it('uses fallback acknowledge setup error when the error payload is unreadable', async () => {
    vi.mocked(authFetch).mockResolvedValueOnce({
      ok: false,
      json: () => Promise.reject('bad json'),
    } as Response)

    await expect(acknowledgeSetup()).rejects.toThrow('Failed to acknowledge setup')
  })

  it('rejects malformed successful acknowledge responses', async () => {
    vi.mocked(authFetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ message: 'acknowledged' }),
    } as Response)

    await expect(acknowledgeSetup()).rejects.toThrow('Invalid acknowledge setup response')
  })
})
