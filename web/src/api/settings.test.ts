import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { getSettings, getWebDAVCredentials, updateSettings } from './settings'

vi.mock('./auth', () => ({
  authFetch: vi.fn(),
}))

import { authFetch } from './auth'

const mockAuthFetch = authFetch as Mock

describe('Settings API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('unwraps settings data responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          server: { host: '0.0.0.0', port: 8080 },
          storage: { root: '/root/.mnemonas' },
          retention: { max_versions: 10, max_age: '24h', min_free_space: 1024, gc_interval: '1h' },
          webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
          dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
          cdc: { min_chunk_size: 1, avg_chunk_size: 2, max_chunk_size: 3 },
        },
      }),
    })

    const result = await getSettings()

    expect(result.data.server.port).toBe(8080)
  })

  it('uses structured api error message when settings request fails', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: () => Promise.resolve({ success: false, error: { code: 'SERVICE_UNAVAILABLE', message: 'settings not available' } }),
    })

    await expect(getSettings()).rejects.toThrow('settings not available')
  })

  it('returns update success message', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, message: 'settings updated, some changes may require restart' }),
    })

    const result = await updateSettings({ server: { port: 8081 } })

    expect(result.message).toContain('require restart')
  })

  it('unwraps webdav credentials payload', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          enabled: true,
          url: '/dav/',
          auth_type: 'basic',
          username: 'admin',
          password: 'secret',
        },
      }),
    })

    const result = await getWebDAVCredentials()

    expect(result.password).toBe('secret')
  })

  it('uses wrapped error message for webdav credentials failures', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      json: () => Promise.resolve({ success: false, error: { message: 'webdav unavailable' } }),
    })

    await expect(getWebDAVCredentials()).rejects.toThrow('webdav unavailable')
  })
})