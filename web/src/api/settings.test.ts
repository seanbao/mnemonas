import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { checkDirectoryAccess, getSecurityCheck, getSettings, getWebDAVCredentials, previewDirectoryAccess, reportDirectoryAccess, sendTestAlert, SettingsError, updateSettings, type SettingsData } from './settings'

vi.mock('./auth', () => ({
  authFetch: vi.fn(),
}))

import { authFetch } from './auth'

const mockAuthFetch = authFetch as Mock
const invalidResponseMessage = '服务器返回了无效的数据'
const validCDCSettings = { min_chunk_size: 262144, avg_chunk_size: 1048576, max_chunk_size: 4194304 }
const validAlertsSettings = {
  enabled: true,
  check_interval: '1m',
  threshold_pct: 80,
  critical_pct: 90,
  min_free_bytes: 1024,
  cooldown_period: '10m',
  webhook_url: '',
  webhook_method: 'POST',
  webhook_headers: [],
}
const validDiskHealthSettings = {
  enabled: true,
  check_interval: '1h',
  probe_timeout: '15s',
  cooldown_period: '4h',
  command: 'smartctl',
  temperature_warning_c: 45,
  temperature_critical_c: 55,
  media_wear_warning_percent: 80,
  media_wear_critical_percent: 95,
  devices: [],
}
const validMaintenanceSettings = {
  scrub: {
    enabled: true,
    schedule_interval: '168h',
    retry_interval: '1h',
    max_retries: 1,
  },
}

function createSettings(overrides: Partial<SettingsData> = {}): SettingsData {
  return {
    server: { host: '0.0.0.0', port: 8080, read_timeout: '30s', write_timeout: '30s', idle_timeout: '60s', trusted_proxy_hops: 2, trusted_proxy_cidrs: [] },
    storage: { root: '~/.mnemonas' },
    auth: { enabled: true, access_token_ttl: '15m0s', refresh_token_ttl: '168h0m0s' },
    retention: { max_versions: 10, max_age: '24h', min_free_space: 1024, gc_interval: '1h' },
    webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
    share: { enabled: true, base_url: 'http://localhost:8080' },
    dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
    cdc: validCDCSettings,
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
          auth: { enabled: true, access_token_ttl: '15m0s', refresh_token_ttl: '168h0m0s' },
          retention: { max_versions: 10, max_age: '24h', min_free_space: 1024, gc_interval: '1h' },
          webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
          share: { enabled: true, base_url: 'http://localhost:8080' },
          dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
          cdc: validCDCSettings,
        },
      }),
    })

    const result = await getSettings()

    expect(result.data.server.port).toBe(8080)
    expect(result.data.server.trusted_proxy_hops).toBe(2)
    expect(result.data.server.trusted_proxy_cidrs).toEqual(['10.0.0.0/8'])
    expect(result.data.auth.access_token_ttl).toBe('15m0s')
    expect(result.data.auth.refresh_token_ttl).toBe('168h0m0s')
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
          auth: { enabled: true, access_token_ttl: '15m0s', refresh_token_ttl: '168h0m0s' },
          retention: { max_versions: 10, max_age: '24h', min_free_space: 1024, gc_interval: '1h' },
          webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
          share: { enabled: true, base_url: 'http://localhost:8080' },
          favorites: { enabled: true, runtime_available: false },
          dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
          cdc: validCDCSettings,
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
          auth: { enabled: true, access_token_ttl: '15m0s', refresh_token_ttl: '168h0m0s' },
          retention: { max_versions: 10, max_age: '24h', min_free_space: 1024, gc_interval: '1h' },
          webdav: { enabled: true, runtime_enabled: false, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
          share: { enabled: true, base_url: 'http://localhost:8080' },
          dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
          cdc: validCDCSettings,
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
        wecom_enabled: true,
        wecom_webhook_url: '<redacted>',
        wecom_webhook_url_configured: true,
        dingtalk_enabled: true,
        dingtalk_webhook_url: '<redacted>',
        dingtalk_webhook_url_configured: true,
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
    expect(result.data.alerts?.wecom_enabled).toBe(true)
    expect(result.data.alerts?.wecom_webhook_url).toBe('<redacted>')
    expect(result.data.alerts?.wecom_webhook_url_configured).toBe(true)
    expect(result.data.alerts?.dingtalk_enabled).toBe(true)
    expect(result.data.alerts?.dingtalk_webhook_url).toBe('<redacted>')
    expect(result.data.alerts?.dingtalk_webhook_url_configured).toBe(true)
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
          cdc: validCDCSettings,
        },
      }),
    })

    await expect(getSettings()).rejects.toThrow(invalidResponseMessage)
  })

  it.each([
    ['unsafe default max access', { default_max_access: 9007199254740992 }],
    ['fractional policy max access', { policy_rules: [{ path: '/team', max_access: 1.5 }] }],
    ['unsafe policy path', { policy_rules: [{ path: '/team/./private' }] }],
    ['relative policy path', { policy_rules: [{ path: 'team' }] }],
    ['trailing-slash policy path', { policy_rules: [{ path: '/team/' }] }],
  ])('rejects settings responses with %s', async (_name, sharePatch) => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: createSettings({
          share: {
            ...createSettings().share,
            ...sharePatch,
          },
        }),
      }),
    })

    await expect(getSettings()).rejects.toThrow(invalidResponseMessage)
  })

  it.each([
    ['unsafe directory quota bytes', { storage: { root: '~/.mnemonas', directory_quotas: [{ path: '/team', quota_bytes: 9007199254740992 }] } }],
    ['unsafe directory quota path', { storage: { root: '~/.mnemonas', directory_quotas: [{ path: '/team/./private', quota_bytes: 1048576 }] } }],
    ['relative directory quota path', { storage: { root: '~/.mnemonas', directory_quotas: [{ path: 'team', quota_bytes: 1048576 }] } }],
    ['trailing-slash directory quota path', { storage: { root: '~/.mnemonas', directory_quotas: [{ path: '/team/', quota_bytes: 1048576 }] } }],
    ['unsafe directory access rule path', { storage: { root: '~/.mnemonas', directory_access_rules: [{ path: '/team/../private', read_groups: ['family'] }] } }],
    ['relative directory access rule path', { storage: { root: '~/.mnemonas', directory_access_rules: [{ path: 'team', read_groups: ['family'] }] } }],
    ['trailing-slash directory access rule path', { storage: { root: '~/.mnemonas', directory_access_rules: [{ path: '/team/', read_groups: ['family'] }] } }],
    ['unsafe retention min free space', { retention: { ...createSettings().retention, min_free_space: 9007199254740992 } }],
    ['unsafe trash max size', { trash: { enabled: true, retention_days: 30, max_size: 9007199254740992 } }],
    ['unsafe versioning max size', { versioning: { auto_versioned_extensions: ['.txt'], auto_versioned_filenames: [], max_versioned_size: 9007199254740992 } }],
    ['unsafe alerts min free bytes', { alerts: { enabled: true, check_interval: '1m', threshold_pct: 80, critical_pct: 90, min_free_bytes: 9007199254740992, cooldown_period: '10m', webhook_url: '', webhook_method: 'POST', webhook_headers: [] } }],
    ['malformed auth access token TTL', { auth: { enabled: true, access_token_ttl: 900, refresh_token_ttl: '168h0m0s' } }],
    ['out-of-range alerts threshold', { alerts: { ...validAlertsSettings, threshold_pct: 101 } }],
    ['fractional alerts critical threshold', { alerts: { ...validAlertsSettings, critical_pct: 90.5 } }],
    ['inverted alerts thresholds', { alerts: { ...validAlertsSettings, threshold_pct: 95, critical_pct: 90 } }],
  ])('rejects settings responses with %s', async (_name, override) => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: createSettings(override as Partial<SettingsData>),
      }),
    })

    await expect(getSettings()).rejects.toThrow(invalidResponseMessage)
  })

  it.each([
    ['out-of-range server port', { server: { ...createSettings().server, port: 65536 } }],
    ['negative trusted proxy hops', { server: { ...createSettings().server, trusted_proxy_hops: -1 } }],
    ['fractional retention max versions', { retention: { ...createSettings().retention, max_versions: 1.5 } }],
    ['fractional dataplane max retries', { dataplane: { ...createSettings().dataplane, max_retries: 1.5 } }],
    ['invalid CDC chunk order', { cdc: { min_chunk_size: 1048576, avg_chunk_size: 262144, max_chunk_size: 4194304 } }],
    ['negative trash retention days', { trash: { enabled: true, retention_days: -1, max_size: 1024 } }],
    ['out-of-range alerts SMTP port', { alerts: { ...validAlertsSettings, smtp_port: 70000 } }],
    ['out-of-range disk health media wear threshold', { disk_health: { ...validDiskHealthSettings, media_wear_warning_percent: 101 } }],
    ['inverted disk health temperature thresholds', { disk_health: { ...validDiskHealthSettings, temperature_warning_c: 60, temperature_critical_c: 50 } }],
    ['negative maintenance scrub max retries', { maintenance: { scrub: { ...validMaintenanceSettings.scrub, max_retries: -1 } } }],
  ])('rejects settings responses with %s', async (_name, override) => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: createSettings(override as Partial<SettingsData>),
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

  it('normalizes scoped logical paths before updating settings', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, data: null, message: 'settings updated' }),
    })

    await updateSettings({
      storage: {
        directory_quotas: [{ path: ' /team// ', quota_bytes: 1048576 }],
        directory_access_rules: [{ path: '/team//public/', read_groups: ['family'] }],
      },
      share: {
        policy_rules: [{ path: '/share//media/', require_password: true }],
      },
    })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/settings/', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({
        storage: {
          directory_quotas: [{ path: '/team', quota_bytes: 1048576 }],
          directory_access_rules: [{ path: '/team/public', read_groups: ['family'] }],
        },
        share: {
          policy_rules: [{ path: '/share/media', require_password: true }],
        },
      }),
    }))
  })

  it.each([
    ['negative default max access', { share: { default_max_access: -1 } }, 'INVALID_SHARE_DEFAULT_MAX_ACCESS'],
    ['fractional default max access', { share: { default_max_access: 1.5 } }, 'INVALID_SHARE_DEFAULT_MAX_ACCESS'],
    ['unsafe default max access', { share: { default_max_access: 9007199254740992 } }, 'INVALID_SHARE_DEFAULT_MAX_ACCESS'],
    ['non-number default max access', { share: { default_max_access: '5' as unknown as number } }, 'INVALID_SHARE_DEFAULT_MAX_ACCESS'],
    ['negative policy max access', { share: { policy_rules: [{ path: '/team', max_access: -1 }] } }, 'INVALID_SHARE_POLICY_MAX_ACCESS'],
    ['fractional policy max access', { share: { policy_rules: [{ path: '/team', max_access: 1.5 }] } }, 'INVALID_SHARE_POLICY_MAX_ACCESS'],
    ['unsafe policy max access', { share: { policy_rules: [{ path: '/team', max_access: 9007199254740992 }] } }, 'INVALID_SHARE_POLICY_MAX_ACCESS'],
    ['non-number policy max access', { share: { policy_rules: [{ path: '/team', max_access: '5' as unknown as number }] } }, 'INVALID_SHARE_POLICY_MAX_ACCESS'],
  ])('rejects %s before updating settings', async (_label, request, code) => {
    await expect(updateSettings(request)).rejects.toMatchObject({
      status: 0,
      code,
    })
    expect(mockAuthFetch).not.toHaveBeenCalled()
  })

  it.each([
    ['zero directory quota bytes', { storage: { directory_quotas: [{ path: '/team', quota_bytes: 0 }] } }, 'INVALID_DIRECTORY_QUOTA_BYTES'],
    ['unsafe directory quota bytes', { storage: { directory_quotas: [{ path: '/team', quota_bytes: 9007199254740992 }] } }, 'INVALID_DIRECTORY_QUOTA_BYTES'],
    ['non-number directory quota bytes', { storage: { directory_quotas: [{ path: '/team', quota_bytes: '1GiB' as unknown as number }] } }, 'INVALID_DIRECTORY_QUOTA_BYTES'],
    ['fractional retention min free space', { retention: { min_free_space: 1.5 } }, 'INVALID_RETENTION_MIN_FREE_SPACE'],
    ['unsafe retention min free space', { retention: { min_free_space: 9007199254740992 } }, 'INVALID_RETENTION_MIN_FREE_SPACE'],
    ['zero trash max size', { trash: { max_size: 0 } }, 'INVALID_TRASH_MAX_SIZE'],
    ['unsafe trash max size', { trash: { max_size: 9007199254740992 } }, 'INVALID_TRASH_MAX_SIZE'],
    ['zero versioning max size', { versioning: { max_versioned_size: 0 } }, 'INVALID_VERSIONING_MAX_VERSIONED_SIZE'],
    ['unsafe versioning max size', { versioning: { max_versioned_size: 9007199254740992 } }, 'INVALID_VERSIONING_MAX_VERSIONED_SIZE'],
    ['fractional alerts min free bytes', { alerts: { min_free_bytes: 1.5 } }, 'INVALID_ALERTS_MIN_FREE_BYTES'],
    ['unsafe alerts min free bytes', { alerts: { min_free_bytes: 9007199254740992 } }, 'INVALID_ALERTS_MIN_FREE_BYTES'],
  ])('rejects %s before updating settings', async (_label, request, code) => {
    await expect(updateSettings(request)).rejects.toMatchObject({
      status: 0,
      code,
    })
    expect(mockAuthFetch).not.toHaveBeenCalled()
  })

  it.each([
    ['directory quota path', { storage: { directory_quotas: [{ path: '/team/./private', quota_bytes: 1048576 }] } }, 'INVALID_DIRECTORY_QUOTA_PATH'],
    ['directory access rule path', { storage: { directory_access_rules: [{ path: '/team/../private', read_groups: ['family'] }] } }, 'INVALID_DIRECTORY_ACCESS_RULE_PATH'],
    ['share policy path', { share: { policy_rules: [{ path: '/team/./private' }] } }, 'INVALID_SHARE_POLICY_PATH'],
    ['relative directory quota path', { storage: { directory_quotas: [{ path: 'team', quota_bytes: 1048576 }] } }, 'INVALID_DIRECTORY_QUOTA_PATH'],
    ['backslash directory access rule path', { storage: { directory_access_rules: [{ path: '/team\\private', read_groups: ['family'] }] } }, 'INVALID_DIRECTORY_ACCESS_RULE_PATH'],
    ['query share policy path', { share: { policy_rules: [{ path: '/team?private' }] } }, 'INVALID_SHARE_POLICY_PATH'],
  ])('rejects invalid %s before updating settings', async (_label, request, code) => {
    await expect(updateSettings(request)).rejects.toMatchObject({
      status: 0,
      code,
    })
    expect(mockAuthFetch).not.toHaveBeenCalled()
  })

  it.each([
    ['zero server port', { server: { port: 0 } }, 'INVALID_SERVER_PORT'],
    ['out-of-range server port', { server: { port: 65536 } }, 'INVALID_SERVER_PORT'],
    ['fractional trusted proxy hops', { server: { trusted_proxy_hops: 1.5 } }, 'INVALID_SERVER_TRUSTED_PROXY_HOPS'],
    ['negative trusted proxy hops', { server: { trusted_proxy_hops: -1 } }, 'INVALID_SERVER_TRUSTED_PROXY_HOPS'],
    ['fractional retention max versions', { retention: { max_versions: 1.5 } }, 'INVALID_RETENTION_MAX_VERSIONS'],
    ['negative trash retention days', { trash: { retention_days: -1 } }, 'INVALID_TRASH_RETENTION_DAYS'],
    ['fractional dataplane max retries', { dataplane: { max_retries: 1.5 } }, 'INVALID_DATAPLANE_MAX_RETRIES'],
    ['out-of-range SMTP port', { alerts: { smtp_port: 70000 } }, 'INVALID_ALERTS_SMTP_PORT'],
    ['out-of-range alerts threshold', { alerts: { threshold_pct: 101 } }, 'INVALID_ALERTS_THRESHOLDS'],
    ['fractional alerts critical threshold', { alerts: { critical_pct: 90.5 } }, 'INVALID_ALERTS_THRESHOLDS'],
    ['inverted alerts thresholds', { alerts: { threshold_pct: 95, critical_pct: 90 } }, 'INVALID_ALERTS_THRESHOLDS'],
    ['inverted disk temperature thresholds', { disk_health: { temperature_warning_c: 60, temperature_critical_c: 50 } }, 'INVALID_DISK_HEALTH_TEMPERATURE'],
    ['out-of-range disk media wear threshold', { disk_health: { media_wear_warning_percent: 101 } }, 'INVALID_DISK_HEALTH_MEDIA_WEAR'],
    ['negative scrub max retries', { maintenance: { scrub: { max_retries: -1 } } }, 'INVALID_SCRUB_MAX_RETRIES'],
    ['too-small CDC min chunk size', { cdc: { min_chunk_size: 65535 } }, 'INVALID_CDC_CHUNK_SIZE'],
    ['invalid CDC chunk order', { cdc: { min_chunk_size: 1048576, avg_chunk_size: 262144 } }, 'INVALID_CDC_CHUNK_SIZE'],
  ])('rejects %s before updating settings', async (_label, request, code) => {
    await expect(updateSettings(request)).rejects.toMatchObject({
      status: 0,
      code,
    })
    expect(mockAuthFetch).not.toHaveBeenCalled()
  })

  it('sends a test alert request and validates the response', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        message: 'test alert sent',
        data: {
          event_type: 'alert_test',
          channels: ['webhook', 'email'],
        },
      }),
    })

    const result = await sendTestAlert()

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/settings/alerts/test', {
      method: 'POST',
    })
    expect(result.message).toBe('test alert sent')
    expect(result.data.channels).toEqual(['webhook', 'email'])
  })

  it('forwards abort signal when sending a test alert', async () => {
    const controller = new AbortController()
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          event_type: 'alert_test',
          channels: ['webhook'],
        },
      }),
    })

    await sendTestAlert({ signal: controller.signal })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/settings/alerts/test', {
      method: 'POST',
      signal: controller.signal,
    })
  })

  it('rejects malformed successful test alert responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, data: { event_type: 'alert_test', channels: [1] } }),
    })

    await expect(sendTestAlert()).rejects.toThrow(invalidResponseMessage)
  })

  it('rejects successful test alert responses with an unexpected event type', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, data: { event_type: 'backup_run', channels: ['webhook'] } }),
    })

    await expect(sendTestAlert()).rejects.toThrow(invalidResponseMessage)
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

  it.each([
    ['unsafe home directory', '/users/./alice'],
    ['relative home directory', 'users/alice'],
    ['trimmed home directory', ' /users/alice '],
  ])('rejects directory access check responses with %s', async (_label, homeDir) => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          username: 'alice',
          user_id: 'u1',
          role: 'user',
          home_dir: homeDir,
          path: '/team/readme.txt',
          read: { mode: 'read', allowed: true, source: 'home_dir' },
          write: { mode: 'write', allowed: true, source: 'home_dir' },
        },
      }),
    })

    await expect(checkDirectoryAccess({ username: 'alice', path: '/team/readme.txt' })).rejects.toThrow(invalidResponseMessage)
  })

  it('rejects directory access check responses with non-canonical response paths', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          username: 'alice',
          user_id: 'u1',
          role: 'user',
          home_dir: '/users/alice',
          path: 'team/readme.txt',
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

  it('rejects invalid directory access check paths before sending requests', async () => {
    await expect(checkDirectoryAccess({ username: 'alice', path: '/team/./readme.txt' })).rejects.toMatchObject({
      status: 0,
      code: 'INVALID_DIRECTORY_ACCESS_PATH',
    })
    expect(mockAuthFetch).not.toHaveBeenCalled()
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

  it.each([
    ['unsafe user home directories', '/users/./alice'],
    ['relative user home directories', 'users/alice'],
    ['trimmed user home directories', ' /users/alice '],
  ])('rejects directory access report responses with %s', async (_label, homeDir) => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          path: '/team/readme.txt',
          summary: { users: 1, read_allowed: 1, read_denied: 0, write_allowed: 1, write_denied: 0, related_shares: 0, active_related_shares: 0, password_protected_shares: 0 },
          users: [{
            username: 'alice',
            user_id: 'u1',
            role: 'user',
            home_dir: homeDir,
            path: '/team/readme.txt',
            read: { mode: 'read', allowed: true, source: 'home_dir' },
            write: { mode: 'write', allowed: true, source: 'home_dir' },
          }],
        },
      }),
    })

    await expect(reportDirectoryAccess({ path: '/team/readme.txt' })).rejects.toThrow(invalidResponseMessage)
  })

  it('rejects directory access report responses with non-canonical report paths', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          path: 'team/readme.txt',
          summary: { users: 1, read_allowed: 1, read_denied: 0, write_allowed: 1, write_denied: 0, related_shares: 0, active_related_shares: 0, password_protected_shares: 0 },
          users: [{
            username: 'alice',
            user_id: 'u1',
            role: 'user',
            home_dir: '/users/alice',
            path: '/team/readme.txt',
            read: { mode: 'read', allowed: true, source: 'home_dir' },
            write: { mode: 'write', allowed: true, source: 'home_dir' },
          }],
        },
      }),
    })

    await expect(reportDirectoryAccess({ path: '/team/readme.txt' })).rejects.toThrow(invalidResponseMessage)
  })

  it('rejects invalid directory access report paths before sending requests', async () => {
    await expect(reportDirectoryAccess({ path: '/team/./readme.txt' })).rejects.toMatchObject({
      status: 0,
      code: 'INVALID_DIRECTORY_ACCESS_PATH',
    })
    expect(mockAuthFetch).not.toHaveBeenCalled()
  })

  it.each([
    ['negative users', { users: -1 }],
    ['fractional read_allowed', { read_allowed: 1.5 }],
    ['unsafe read_denied', { read_denied: 9007199254740992 }],
    ['non-number write_allowed', { write_allowed: '1' }],
    ['negative write_denied', { write_denied: -1 }],
    ['fractional related_shares', { related_shares: 1.5 }],
    ['unsafe active_related_shares', { active_related_shares: 9007199254740992 }],
    ['negative password_protected_shares', { password_protected_shares: -1 }],
  ])('rejects directory access report summaries with %s', async (_label, summaryOverride) => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          path: '/team/readme.txt',
          summary: {
            users: 1,
            read_allowed: 1,
            read_denied: 0,
            write_allowed: 1,
            write_denied: 0,
            related_shares: 0,
            active_related_shares: 0,
            password_protected_shares: 0,
            ...summaryOverride,
          },
          users: [{
            username: 'alice',
            user_id: 'u1',
            role: 'user',
            home_dir: '/users/alice',
            path: '/team/readme.txt',
            read: { mode: 'read', allowed: true, source: 'directory_access_rule', matched_rule: { path: '/team', read_users: ['alice'] } },
            write: { mode: 'write', allowed: true, source: 'directory_access_rule', matched_rule: { path: '/team', write_users: ['alice'] } },
          }],
        },
      }),
    })

    await expect(reportDirectoryAccess({ path: '/team/readme.txt' })).rejects.toThrow(invalidResponseMessage)
  })

  it('rejects directory access report shares with unsafe counters', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          path: '/team/readme.txt',
          summary: { users: 1, read_allowed: 1, read_denied: 0, write_allowed: 1, write_denied: 0, related_shares: 1, active_related_shares: 1, password_protected_shares: 1 },
          users: [{
            username: 'alice',
            user_id: 'u1',
            role: 'user',
            home_dir: '/users/alice',
            path: '/team/readme.txt',
            read: { mode: 'read', allowed: true, source: 'directory_access_rule', matched_rule: { path: '/team', read_users: ['alice'] } },
            write: { mode: 'write', allowed: true, source: 'directory_access_rule', matched_rule: { path: '/team', write_users: ['alice'] } },
          }],
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
            max_access: 9007199254740992,
            url: '/s/share-1',
          }],
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

  it('rejects invalid directory access preview paths before sending requests', async () => {
    await expect(previewDirectoryAccess({
      path: '/team/./readme.txt',
      directory_access_rules: [],
    })).rejects.toMatchObject({
      status: 0,
      code: 'INVALID_DIRECTORY_ACCESS_PATH',
    })
    expect(mockAuthFetch).not.toHaveBeenCalled()
  })

  it('rejects invalid directory access preview rule paths before sending requests', async () => {
    await expect(previewDirectoryAccess({
      path: '/team/readme.txt',
      directory_access_rules: [{ path: '/team/../private', read_groups: ['family'] }],
    })).rejects.toMatchObject({
      status: 0,
      code: 'INVALID_DIRECTORY_ACCESS_RULE_PATH',
    })
    expect(mockAuthFetch).not.toHaveBeenCalled()
  })

  it('uses structured api error message when update settings fails', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 400,
      json: () => Promise.resolve({ success: false, error: { message: 'invalid settings' } }),
    })

    await expect(updateSettings({ server: { port: 8081 } })).rejects.toMatchObject({
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
    ['alerts wecom', { alerts: { ...validAlertsSettings, wecom_enabled: true, wecom_webhook_url: 123 } }],
    ['alerts dingtalk', { alerts: { ...validAlertsSettings, dingtalk_enabled: true, dingtalk_webhook_url: 123 } }],
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
