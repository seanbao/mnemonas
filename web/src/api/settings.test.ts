import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { checkDirectoryAccess, getSecurityCheck, getSettings, getWebDAVCredentials, previewDirectoryAccess, reportDirectoryAccess, SettingsError, updateSettings, type SettingsData } from './settings'

vi.mock('./auth', () => ({
  authFetch: vi.fn(),
}))

import { authFetch } from './auth'

const mockAuthFetch = authFetch as Mock
const invalidResponseMessage = '服务器返回了无效的数据'

function createSettings(overrides: Partial<SettingsData> = {}): SettingsData {
  return {
    server: { host: '0.0.0.0', port: 8080, read_timeout: '30s', write_timeout: '30s', idle_timeout: '60s', trusted_proxy_hops: 2, trusted_proxy_cidrs: [] },
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
          server: { host: '0.0.0.0', port: 8080, read_timeout: '30s', write_timeout: '30s', idle_timeout: '60s', trusted_proxy_hops: 2, trusted_proxy_cidrs: ['10.0.0.0/8'] },
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
    expect(result.data.server.trusted_proxy_cidrs).toEqual(['10.0.0.0/8'])
  })

  it('forwards abort signal when fetching settings', async () => {
    const controller = new AbortController()
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: createSettings(),
      }),
    })

    await getSettings({ signal: controller.signal })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/settings/', {
      signal: controller.signal,
    })
  })

  it('rejects malformed trusted proxy CIDR settings responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: createSettings({
          server: {
            ...createSettings().server,
            trusted_proxy_cidrs: [10] as unknown as string[],
          },
        }),
      }),
    })

    await expect(getSettings()).rejects.toThrow(invalidResponseMessage)
  })

  it('accepts directory quotas in settings responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: createSettings({
          storage: {
            root: '~/.mnemonas',
            directory_quotas: [{ path: '/team', quota_bytes: 1048576 }],
          },
        }),
      }),
    })

    const result = await getSettings()

    expect(result.data.storage.directory_quotas?.[0]).toEqual({ path: '/team', quota_bytes: 1048576 })
  })

  it('accepts directory access rules in settings responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: createSettings({
          storage: {
            root: '~/.mnemonas',
            directory_access_rules: [{
              path: '/team',
              read_groups: ['family'],
              write_groups: ['editors'],
              read_roles: ['user'],
            }],
          },
        }),
      }),
    })

    const result = await getSettings()

    expect(result.data.storage.directory_access_rules?.[0]).toEqual({
      path: '/team',
      read_groups: ['family'],
      write_groups: ['editors'],
      read_roles: ['user'],
    })
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
        webhook_url: '<redacted>',
        webhook_url_configured: true,
        webhook_method: 'POST',
        webhook_headers: ['X-Test: <redacted>'],
        webhook_headers_configured: true,
        telegram_enabled: true,
        telegram_bot_token_configured: true,
        telegram_chat_id: '-1001234567890',
      },
      disk_health: {
        enabled: true,
        check_interval: '1h',
        probe_timeout: '15s',
        cooldown_period: '4h',
        command: 'smartctl',
        temperature_warning_c: 45,
        temperature_critical_c: 55,
        media_wear_warning_percent: 80,
        media_wear_critical_percent: 95,
        devices: [{
          name: 'Data',
          path: '/dev/disk/by-id/test',
          type: 'sat',
          serial: 'SER123',
          temperature_warning_c: 42,
          temperature_critical_c: 52,
        }],
      },
      maintenance: {
        scrub: {
          enabled: true,
          schedule_interval: '168h',
          retry_interval: '1h',
          max_retries: 1,
        },
      },
    })
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, data }),
    })

    const result = await getSettings()

    expect(result.data.server.tls?.cert_file).toBe('/cert.pem')
    expect(result.data.versioning?.auto_versioned_extensions).toEqual(['.txt', '.md'])
    expect(result.data.alerts?.webhook_url).toBe('<redacted>')
    expect(result.data.alerts?.webhook_url_configured).toBe(true)
    expect(result.data.alerts?.webhook_headers).toEqual(['X-Test: <redacted>'])
    expect(result.data.alerts?.webhook_headers_configured).toBe(true)
    expect(result.data.alerts?.telegram_bot_token_configured).toBe(true)
    expect(result.data.alerts?.telegram_chat_id).toBe('-1001234567890')
    expect(result.data.disk_health?.devices[0]?.path).toBe('/dev/disk/by-id/test')
    expect(result.data.maintenance?.scrub?.schedule_interval).toBe('168h')
  })

  it('rejects malformed successful settings responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true }),
    })

    await expect(getSettings()).rejects.toThrow(invalidResponseMessage)
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

    await expect(getSettings()).rejects.toThrow(invalidResponseMessage)
  })

  it('uses structured api error message when settings request fails', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: () => Promise.resolve({ success: false, error: { code: 'SERVICE_UNAVAILABLE', message: 'settings not available' } }),
    })

    await expect(getSettings()).rejects.toThrow('settings not available')
  })

  it('surfaces problem-json settings errors', async () => {
    const body = {
      title: 'Service unavailable',
      detail: 'settings storage unavailable',
      status: 503,
    }

    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      headers: new Headers({ 'Content-Type': 'application/problem+json' }),
      clone: () => ({ json: () => Promise.resolve(body) }),
      json: () => Promise.resolve(body),
    })

    await expect(getSettings()).rejects.toMatchObject({
      message: 'settings storage unavailable',
      status: 503,
      isUnavailable: true,
    })
  })

  it('uses fallback api error messages when settings error bodies are unreadable', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
    })

    await expect(getSettings()).rejects.toMatchObject({
      message: '获取设置失败',
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

  it('unwraps security check responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'https_request',
              status: 'warning',
              title: '当前访问不是 HTTPS',
              message: '公网访问前应启用 HTTPS。',
              details: { direct_tls: false },
            },
          ],
          request: { scheme: 'http' },
          config: { trusted_proxy_hops: 0 },
        },
      }),
    })

    const result = await getSecurityCheck()

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/settings/security-check', {})
    expect(result.data.status).toBe('warning')
    expect(result.data.checks[0].id).toBe('https_request')
  })

  it('forwards abort signal when fetching security check', async () => {
    const controller = new AbortController()
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          status: 'pass',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [],
          request: { scheme: 'https' },
          config: { trusted_proxy_hops: 1 },
        },
      }),
    })

    await getSecurityCheck({ signal: controller.signal })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/settings/security-check', {
      signal: controller.signal,
    })
  })

  it('rejects malformed security check responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          status: 'unknown',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [],
          request: {},
          config: {},
        },
      }),
    })

    await expect(getSecurityCheck()).rejects.toThrow(invalidResponseMessage)
  })

  it('uses structured api error message when security check fails', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: () => Promise.resolve({ success: false, error: { code: 'SERVICE_UNAVAILABLE', message: 'settings not available' } }),
    })

    await expect(getSecurityCheck()).rejects.toMatchObject({
      message: 'settings not available',
      status: 503,
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

  it('forwards abort signal when updating settings', async () => {
    const controller = new AbortController()
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, data: null, message: 'settings updated' }),
    })

    await updateSettings({ server: { port: 8081 } }, { signal: controller.signal })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/settings/', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ server: { port: 8081 } }),
      signal: controller.signal,
    }))
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

  it('sends trusted_proxy_cidrs in update payloads', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, data: null, message: 'settings updated' }),
    })

    await updateSettings({ server: { trusted_proxy_cidrs: ['10.0.0.0/8', '192.168.1.10'] } })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/settings/', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ server: { trusted_proxy_cidrs: ['10.0.0.0/8', '192.168.1.10'] } }),
    }))
  })

  it('sends directory access rules in update payloads', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, data: null, message: 'settings updated' }),
    })

    await updateSettings({
      storage: {
        directory_access_rules: [{
          path: '/team',
          read_groups: ['family'],
          write_groups: ['editors'],
        }],
      },
    })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/settings/', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({
        storage: {
          directory_access_rules: [{
            path: '/team',
            read_groups: ['family'],
            write_groups: ['editors'],
          }],
        },
      }),
    }))
  })

  it('sends disk health settings in update payloads', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, data: null, message: 'settings updated' }),
    })

    await updateSettings({
      disk_health: {
        enabled: true,
        check_interval: '1h',
        probe_timeout: '15s',
        cooldown_period: '4h',
        command: 'smartctl',
        temperature_warning_c: 45,
        temperature_critical_c: 55,
        media_wear_warning_percent: 80,
        media_wear_critical_percent: 95,
        devices: [{ name: 'Data', path: '/dev/disk/by-id/test', type: 'sat', serial: 'SER123' }],
      },
    })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/settings/', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({
        disk_health: {
          enabled: true,
          check_interval: '1h',
          probe_timeout: '15s',
          cooldown_period: '4h',
          command: 'smartctl',
          temperature_warning_c: 45,
          temperature_critical_c: 55,
          media_wear_warning_percent: 80,
          media_wear_critical_percent: 95,
          devices: [{ name: 'Data', path: '/dev/disk/by-id/test', type: 'sat', serial: 'SER123' }],
        },
      }),
    }))
  })

  it('checks directory access and unwraps decisions', async () => {
    const controller = new AbortController()
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          username: 'alice',
          user_id: 'u1',
          role: 'user',
          groups: ['family'],
          home_dir: '/users/alice',
          path: '/team/readme.txt',
          read: {
            mode: 'read',
            allowed: true,
            source: 'directory_access_rule',
            message: 'directory access rule grants read',
            matched_rule: { path: '/team', read_groups: ['family'] },
          },
          write: {
            mode: 'write',
            allowed: false,
            source: 'directory_access_rule',
            message: 'directory access rule does not grant write',
            matched_rule: { path: '/team', read_groups: ['family'] },
          },
        },
      }),
    })

    const result = await checkDirectoryAccess(
      { username: 'alice', path: '/team/readme.txt' },
      { signal: controller.signal },
    )

    expect(result.read.allowed).toBe(true)
    expect(result.write.allowed).toBe(false)
    expect(result.read.matched_rule?.path).toBe('/team')
    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/settings/access-check', expect.objectContaining({
      method: 'POST',
      signal: controller.signal,
      body: JSON.stringify({ username: 'alice', path: '/team/readme.txt' }),
    }))
  })

  it('rejects malformed directory access check responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          username: 'alice',
          user_id: 'u1',
          role: 'owner',
          home_dir: '/users/alice',
          path: '/team/readme.txt',
          read: { mode: 'read', allowed: true, source: 'home_dir' },
          write: { mode: 'write', allowed: true, source: 'home_dir' },
        },
      }),
    })

    await expect(checkDirectoryAccess({ username: 'alice', path: '/team/readme.txt' })).rejects.toThrow(invalidResponseMessage)
  })

  it('uses structured api error message when directory access check fails', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 404,
      json: () => Promise.resolve({ success: false, error: { code: 'NOT_FOUND', message: 'user not found' } }),
    })

    await expect(checkDirectoryAccess({ username: 'missing', path: '/' })).rejects.toMatchObject({
      message: 'user not found',
      status: 404,
      code: 'NOT_FOUND',
    })
  })

  it('reports directory access for all users', async () => {
    const controller = new AbortController()
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          path: '/team/readme.txt',
          summary: {
            users: 2,
            read_allowed: 1,
            read_denied: 1,
            write_allowed: 1,
            write_denied: 1,
            related_shares: 1,
            active_related_shares: 1,
            password_protected_shares: 1,
          },
          users: [
            {
              username: 'alice',
              user_id: 'u1',
              role: 'user',
              groups: ['family'],
              home_dir: '/users/alice',
              path: '/team/readme.txt',
              read: { mode: 'read', allowed: true, source: 'directory_access_rule', matched_rule: { path: '/team', read_groups: ['family'] } },
              write: { mode: 'write', allowed: true, source: 'directory_access_rule', matched_rule: { path: '/team', write_groups: ['family'] } },
            },
            {
              username: 'bob',
              user_id: 'u2',
              role: 'user',
              home_dir: '/users/bob',
              path: '/team/readme.txt',
              read: { mode: 'read', allowed: false, source: 'home_dir' },
              write: { mode: 'write', allowed: false, source: 'home_dir' },
            },
          ],
          shares: [{
            id: 'share-1',
            path: '/team',
            type: 'folder',
            created_by: 'u1',
            relation: 'covers_path',
            enabled: true,
            active: true,
            has_password: true,
            access_count: 0,
            max_access: 0,
            url: '/s/share-1',
          }],
        },
      }),
    })

    const result = await reportDirectoryAccess(
      { path: '/team/readme.txt' },
      { signal: controller.signal },
    )

    expect(result.summary.read_allowed).toBe(1)
    expect(result.summary.related_shares).toBe(1)
    expect(result.shares?.[0]?.relation).toBe('covers_path')
    expect(result.users.map((entry) => entry.username)).toEqual(['alice', 'bob'])
    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/settings/access-report', expect.objectContaining({
      method: 'POST',
      signal: controller.signal,
      body: JSON.stringify({ path: '/team/readme.txt' }),
    }))
  })

  it('rejects malformed directory access report responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          path: '/team/readme.txt',
          summary: { users: 1, read_allowed: 1, read_denied: 0, write_allowed: 1, write_denied: 0, related_shares: 0, active_related_shares: 0, password_protected_shares: 0 },
          users: [{ username: 'alice' }],
        },
      }),
    })

    await expect(reportDirectoryAccess({ path: '/team/readme.txt' })).rejects.toThrow(invalidResponseMessage)
  })

  it('previews directory access rules without saving settings', async () => {
    const controller = new AbortController()
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          path: '/team/readme.txt',
          preview: true,
          summary: { users: 1, read_allowed: 1, read_denied: 0, write_allowed: 0, write_denied: 1, related_shares: 0, active_related_shares: 0, password_protected_shares: 0 },
          users: [{
            username: 'alice',
            user_id: 'u1',
            role: 'user',
            home_dir: '/users/alice',
            path: '/team/readme.txt',
            read: { mode: 'read', allowed: true, source: 'directory_access_rule', matched_rule: { path: '/team', read_groups: ['family'] } },
            write: { mode: 'write', allowed: false, source: 'directory_access_rule', matched_rule: { path: '/team', read_groups: ['family'] } },
          }],
        },
      }),
    })

    const rules = [{ path: '/team', read_groups: ['family'] }]
    const result = await previewDirectoryAccess(
      { path: '/team/readme.txt', directory_access_rules: rules },
      { signal: controller.signal },
    )

    expect(result.preview).toBe(true)
    expect(result.users[0]?.read.allowed).toBe(true)
    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/settings/access-preview', expect.objectContaining({
      method: 'POST',
      signal: controller.signal,
      body: JSON.stringify({ path: '/team/readme.txt', directory_access_rules: rules }),
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

    await expect(updateSettings({ server: { port: 8081 } })).rejects.toThrow(invalidResponseMessage)
  })

  it('rejects malformed successful update responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ message: 'settings updated' }),
    })

    await expect(updateSettings({ server: { port: 8081 } })).rejects.toThrow(invalidResponseMessage)
  })

  it.each([
    ['storage', { storage: { root: '~/.mnemonas', directory_quotas: [{ path: '/team', quota_bytes: '1GiB' }] } }],
    ['tls', { server: { ...createSettings().server, tls: { enabled: true } } }],
    ['trash', { trash: { enabled: true, retention_days: '30', max_size: 1024 } }],
    ['versioning', { versioning: { auto_versioned_extensions: ['.txt'], auto_versioned_filenames: [123], max_versioned_size: 1024 } }],
    ['favorites', { favorites: { enabled: true, runtime_available: 'yes' } }],
    ['alerts', { alerts: { enabled: true, check_interval: '1m', threshold_pct: 80, critical_pct: 90, min_free_bytes: 1024, cooldown_period: '10m', webhook_url: 'https://hooks.example.com', webhook_method: 'POST', webhook_headers: [], telegram_enabled: true, telegram_bot_token_configured: 'yes' } }],
    ['disk_health', { disk_health: { enabled: true, check_interval: '1h', probe_timeout: '15s', cooldown_period: '4h', command: 'smartctl', temperature_warning_c: 45, temperature_critical_c: 55, media_wear_warning_percent: 80, media_wear_critical_percent: 95, devices: [{ path: 12 }] } }],
    ['maintenance', { maintenance: { scrub: { enabled: true, schedule_interval: '168h', retry_interval: '1h', max_retries: '1' } } }],
  ])('rejects malformed optional %s settings sections', async (_name, override) => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, data: createSettings(override as Partial<SettingsData>) }),
    })

    await expect(getSettings()).rejects.toThrow(invalidResponseMessage)
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

  it('forwards abort signal when fetching WebDAV credentials', async () => {
    const controller = new AbortController()
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

    await getWebDAVCredentials({ signal: controller.signal })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/settings/webdav-credentials', {
      signal: controller.signal,
    })
  })

  it('rejects malformed successful webdav credentials responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true }),
    })

    await expect(getWebDAVCredentials()).rejects.toThrow(invalidResponseMessage)
  })

  it('rejects unreadable successful webdav credentials responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
    })

    await expect(getWebDAVCredentials()).rejects.toThrow(invalidResponseMessage)
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

    await expect(getWebDAVCredentials()).rejects.toThrow(invalidResponseMessage)
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
