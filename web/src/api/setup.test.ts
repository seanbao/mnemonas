import { beforeEach, describe, expect, it, vi } from 'vitest'
import { acknowledgeSetup, getSetupStatus } from './setup'

vi.mock('./auth', () => ({
  authFetch: vi.fn(),
}))

import { authFetch } from './auth'

const invalidResponseMessage = '服务器返回了无效的数据'

describe('Setup API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    global.fetch = vi.fn() as typeof fetch
  })

  it('returns setup status payload', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, is_first_run: true, auth_enabled: true, share_enabled: false, webdav_enabled: true, webdav_auth_type: 'basic' }),
    } as Response)

    const result = await getSetupStatus()

    expect(result.is_first_run).toBe(true)
    expect(result.share_enabled).toBe(false)
    expect(result.webdav_auth_type).toBe('basic')
  })

  it('forwards abort signal when fetching setup status', async () => {
    const controller = new AbortController()
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, is_first_run: false, auth_enabled: true, share_enabled: true, webdav_enabled: true, webdav_auth_type: 'basic' }),
    } as Response)

    await getSetupStatus({ signal: controller.signal })

    expect(global.fetch).toHaveBeenCalledWith('/api/v1/setup/', {
      signal: controller.signal,
    })
  })

  it('rejects invalid share status values', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, is_first_run: true, auth_enabled: true, share_enabled: 'no', webdav_enabled: true, webdav_auth_type: 'basic' }),
    } as Response)

    await expect(getSetupStatus()).rejects.toThrow(invalidResponseMessage)
  })

  it('rejects malformed successful setup status responses', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, is_first_run: true }),
    } as Response)

    await expect(getSetupStatus()).rejects.toThrow(invalidResponseMessage)
  })

  it('rejects invalid setup status JSON', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.reject(new SyntaxError('bad json')),
    } as Response)

    await expect(getSetupStatus()).rejects.toThrow(invalidResponseMessage)
  })

  it('reads structured error for setup status', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: false,
      json: () => Promise.resolve({ code: 'SERVICE_UNAVAILABLE', message: 'setup status unavailable' }),
    } as Response)

    await expect(getSetupStatus()).rejects.toThrow('setup status unavailable')
  })

  it('surfaces problem-json setup status errors', async () => {
    const body = {
      title: 'Service unavailable',
      detail: 'setup status storage unavailable',
      status: 503,
    }

    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: false,
      status: 503,
      headers: new Headers({ 'Content-Type': 'application/problem+json' }),
      clone: () => ({ json: () => Promise.resolve(body) }),
      json: () => Promise.resolve(body),
    } as unknown as Response)

    await expect(getSetupStatus()).rejects.toThrow('setup status storage unavailable')
  })

  it('reads legacy string error for setup status', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: false,
      json: () => Promise.resolve({ success: false, error: 'setup status failed' }),
    } as Response)

    await expect(getSetupStatus()).rejects.toThrow('setup status failed')
  })

  it('uses fallback setup status error when the payload has no message', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: false,
      json: () => Promise.resolve({ error: {} }),
    } as Response)

    await expect(getSetupStatus()).rejects.toThrow('获取初始化状态失败')
  })

  it('uses fallback setup status error when the error payload is unreadable', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: false,
      json: () => Promise.reject('bad json'),
    } as Response)

    await expect(getSetupStatus()).rejects.toThrow('获取初始化状态失败')
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

  it('forwards abort signal when acknowledging setup', async () => {
    const controller = new AbortController()
    vi.mocked(authFetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true }),
    } as Response)

    await acknowledgeSetup({ signal: controller.signal })

    expect(authFetch).toHaveBeenCalledWith('/api/v1/setup/acknowledge', {
      method: 'POST',
      signal: controller.signal,
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

  it('uses fallback acknowledge setup error when the payload has no message', async () => {
    vi.mocked(authFetch).mockResolvedValueOnce({
      ok: false,
      json: () => Promise.resolve({ error: {} }),
    } as Response)

    await expect(acknowledgeSetup()).rejects.toThrow('确认初始化完成失败')
  })

  it('uses fallback acknowledge setup error when the error payload is unreadable', async () => {
    vi.mocked(authFetch).mockResolvedValueOnce({
      ok: false,
      json: () => Promise.reject('bad json'),
    } as Response)

    await expect(acknowledgeSetup()).rejects.toThrow('确认初始化完成失败')
  })

  it('rejects malformed successful acknowledge responses', async () => {
    vi.mocked(authFetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ message: 'acknowledged' }),
    } as Response)

    await expect(acknowledgeSetup()).rejects.toThrow(invalidResponseMessage)
  })
})
