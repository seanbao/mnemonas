import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { getSettings, getWebDAVCredentials, SettingsError, updateSettings, type SettingsData } from './settings'

vi.mock('./auth', () => ({
  authFetch: vi.fn(),
}))

import { authFetch } from './auth'

const mockAuthFetch = authFetch as Mock

function createSettings(overrides: Partial<SettingsData> = {}): SettingsData {
  return {
    server: { host: '0.0.0.0', port: 8080, read_timeout: '30s', write_timeout: '30s', idle_timeout: '60s', trusted_proxy_hops: 2 },
    storage: { root: '~/.mnemonas' },
    retention: { max_versions: 10, max_age: '24h', min_free_space: 1024, gc_interval: '1h' },
    webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
    share: { enabled: true, base_url: 'http://localhost:8080' },
    dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
    cdc: { min_chunk_size: 1, avg_chunk_size: 2, max_chunk_size: 3 },
    ...overrides,
  }
}

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
          server: { host: '0.0.0.0', port: 8080, read_timeout: '30s', write_timeout: '30s', idle_timeout: '60s', trusted_proxy_hops: 2 },
          storage: { root: '~/.mnemonas' },
          retention: { max_versions: 10, max_age: '24h', min_free_space: 1024, gc_interval: '1h' },
          webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
          share: { enabled: true, base_url: 'http://localhost:8080' },
          dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
          cdc: { min_chunk_size: 1, avg_chunk_size: 2, max_chunk_size: 3 },
        },
      }),
    })

    const result = await getSettings()

    expect(result.data.server.port).toBe(8080)
    expect(result.data.server.trusted_proxy_hops).toBe(2)
  })

  it('accepts favorites runtime availability in settings responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          server: { host: '0.0.0.0', port: 8080, read_timeout: '30s', write_timeout: '30s', idle_timeout: '60s', trusted_proxy_hops: 2 },
          storage: { root: '~/.mnemonas' },
          retention: { max_versions: 10, max_age: '24h', min_free_space: 1024, gc_interval: '1h' },
          webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
          share: { enabled: true, base_url: 'http://localhost:8080' },
          favorites: { enabled: true, runtime_available: false },
          dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
          cdc: { min_chunk_size: 1, avg_chunk_size: 2, max_chunk_size: 3 },
        },
      }),
    })

    const result = await getSettings()

    expect(result.data.favorites?.runtime_available).toBe(false)
  })

  it('accepts WebDAV runtime enablement in settings responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          server: { host: '0.0.0.0', port: 8080, read_timeout: '30s', write_timeout: '30s', idle_timeout: '60s', trusted_proxy_hops: 2 },
          storage: { root: '~/.mnemonas' },
          retention: { max_versions: 10, max_age: '24h', min_free_space: 1024, gc_interval: '1h' },
          webdav: { enabled: true, runtime_enabled: false, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
          share: { enabled: true, base_url: 'http://localhost:8080' },
          dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
          cdc: { min_chunk_size: 1, avg_chunk_size: 2, max_chunk_size: 3 },
        },
      }),
    })

    const result = await getSettings()

    expect(result.data.webdav.runtime_enabled).toBe(false)
  })

  it('accepts optional settings sections with valid shapes', async () => {
    const data = createSettings({
      server: {
        ...createSettings().server,
        tls: {
          enabled: true,
          cert_file: '/cert.pem',
          key_file: '/key.pem',
          auto_generate: false,
          cert_dir: '/certs',
        },
      },
      trash: { enabled: true, retention_days: 30, max_size: 1024 },
      versioning: {
        auto_versioned_extensions: ['.txt', '.md'],
        auto_versioned_filenames: ['README'],
        max_versioned_size: 1048576,
      },
      favorites: { enabled: true, runtime_available: true },
      alerts: {
        enabled: true,
        check_interval: '1m',
        threshold_pct: 80,
        critical_pct: 95,
        min_free_bytes: 1024,
        cooldown_period: '10m',
        webhook_url: 'https://hooks.example.com/storage',
        webhook_method: 'POST',
        webhook_headers: ['X-Test: true'],
      },
    })
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, data }),
    })

    const result = await getSettings()

    expect(result.data.server.tls?.cert_file).toBe('/cert.pem')
    expect(result.data.versioning?.auto_versioned_extensions).toEqual(['.txt', '.md'])
    expect(result.data.alerts?.webhook_headers).toEqual(['X-Test: true'])
  })

  it('rejects malformed successful settings responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true }),
    })

    await expect(getSettings()).rejects.toThrow('Invalid settings response')
  })

  it('rejects successful settings responses with malformed data shape', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          server: { host: '0.0.0.0', port: 8080, read_timeout: '30s', write_timeout: '30s', idle_timeout: '60s' },
          storage: { root: '~/.mnemonas' },
          retention: { max_versions: 10, max_age: '24h', min_free_space: 1024, gc_interval: '1h' },
          webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
          dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
          cdc: { min_chunk_size: 1, avg_chunk_size: 2, max_chunk_size: 3 },
        },
      }),
    })

    await expect(getSettings()).rejects.toThrow('Invalid settings response')
  })

  it('uses structured api error message when settings request fails', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: () => Promise.resolve({ success: false, error: { code: 'SERVICE_UNAVAILABLE', message: 'settings not available' } }),
    })

    await expect(getSettings()).rejects.toThrow('settings not available')
  })

  it('uses fallback api error messages when settings error bodies are unreadable', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
    })

    await expect(getSettings()).rejects.toMatchObject({
      message: 'Failed to get settings',
      status: 500,
    })
  })

  it('preserves service-unavailable settings error codes', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: () => Promise.resolve({ success: false, error: { code: 'SERVICE_UNAVAILABLE', message: 'settings not available' } }),
    })

    await expect(getSettings()).rejects.toMatchObject({
      message: 'settings not available',
      status: 503,
      code: 'SERVICE_UNAVAILABLE',
      isUnavailable: true,
    })
  })

  it('returns update success message', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, data: null, message: 'settings updated, some changes may require restart' }),
    })

    const result = await updateSettings({ server: { port: 8081 } })

    expect(result.message).toContain('require restart')
  })

  it('sends trusted_proxy_hops in update payloads', async () => {
  mockAuthFetch.mockResolvedValueOnce({
    ok: true,
    json: () => Promise.resolve({ success: true, data: null, message: 'settings updated' }),
  })

  await updateSettings({ server: { trusted_proxy_hops: 3 } })

  expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/settings/', expect.objectContaining({
    method: 'PUT',
    body: JSON.stringify({ server: { trusted_proxy_hops: 3 } }),
	  }))
	  })

  it('uses structured api error message when update settings fails', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 400,
      json: () => Promise.resolve({ success: false, error: { message: 'invalid settings' } }),
    })

    await expect(updateSettings({ server: { port: 0 } })).rejects.toMatchObject({
      message: 'invalid settings',
      status: 400,
    })
  })

  it('rejects successful update responses missing wrapped data', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, message: 'settings updated, some changes may require restart' }),
    })

    await expect(updateSettings({ server: { port: 8081 } })).rejects.toThrow('Invalid update settings response')
  })

  it('rejects malformed successful update responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ message: 'settings updated' }),
    })

    await expect(updateSettings({ server: { port: 8081 } })).rejects.toThrow('Invalid update settings response')
  })

  it.each([
    ['tls', { server: { ...createSettings().server, tls: { enabled: true } } }],
    ['trash', { trash: { enabled: true, retention_days: '30', max_size: 1024 } }],
    ['versioning', { versioning: { auto_versioned_extensions: ['.txt'], auto_versioned_filenames: [123], max_versioned_size: 1024 } }],
    ['favorites', { favorites: { enabled: true, runtime_available: 'yes' } }],
    ['alerts', { alerts: { enabled: true, check_interval: '1m', threshold_pct: 80, critical_pct: 90, min_free_bytes: 1024, cooldown_period: '10m', webhook_url: 'https://hooks.example.com', webhook_method: 'POST', webhook_headers: [123] } }],
  ])('rejects malformed optional %s settings sections', async (_name, override) => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, data: createSettings(override as Partial<SettingsData>) }),
    })

    await expect(getSettings()).rejects.toThrow('Invalid settings response')
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

  it('rejects malformed successful webdav credentials responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true }),
    })

    await expect(getWebDAVCredentials()).rejects.toThrow('Invalid WebDAV credentials response')
  })

  it('rejects unreadable successful webdav credentials responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
    })

    await expect(getWebDAVCredentials()).rejects.toThrow('Invalid WebDAV credentials response')
  })

  it('rejects webdav credentials responses with malformed data shape', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          enabled: 'yes',
          url: '/dav/',
          auth_type: 'basic',
        },
      }),
    })

    await expect(getWebDAVCredentials()).rejects.toThrow('Invalid WebDAV credentials response')
  })

  it('uses wrapped error message for webdav credentials failures', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      json: () => Promise.resolve({ success: false, error: { message: 'webdav unavailable' } }),
    })

    await expect(getWebDAVCredentials()).rejects.toThrow('webdav unavailable')
  })

  it('preserves service-unavailable webdav credentials error codes', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: () => Promise.resolve({ success: false, error: { code: 'SERVICE_UNAVAILABLE', message: 'webdav credentials unavailable' } }),
    })

    await expect(getWebDAVCredentials()).rejects.toMatchObject({
      message: 'webdav credentials unavailable',
      status: 503,
      code: 'SERVICE_UNAVAILABLE',
      isUnavailable: true,
    })
  })

  it('marks SettingsError as unavailable for service-unavailable responses', () => {
    const error = new SettingsError('settings not available', 503, 'SERVICE_UNAVAILABLE')

    expect(error.isUnavailable).toBe(true)
  })
})
