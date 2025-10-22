import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import { act, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SettingsPage } from './Settings'
import * as HeroUI from '@heroui/react'

const mockAddToast = vi.fn()
const originalClipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')

const { mockUser } = vi.hoisted(() => ({
  mockUser: { id: 'u1', username: 'admin', role: 'admin' as const, email: 'admin@local', homeDir: '/' },
}))

import { getSettings } from '@/api/settings'
import { SettingsError } from '@/api/settings'
import { updateSettings } from '@/api/settings'
import { getWebDAVCredentials } from '@/api/settings'
import { getSecurityCheck } from '@/api/settings'
import { checkDirectoryAccess } from '@/api/settings'
import { previewDirectoryAccess } from '@/api/settings'
import { reportDirectoryAccess } from '@/api/settings'
import { sendTestAlert } from '@/api/settings'

const mockGetSettings = vi.mocked(getSettings)
const mockUpdateSettings = vi.mocked(updateSettings)
const mockGetWebDAVCredentials = vi.mocked(getWebDAVCredentials)
const mockGetSecurityCheck = vi.mocked(getSecurityCheck)
const mockCheckDirectoryAccess = vi.mocked(checkDirectoryAccess)
const mockPreviewDirectoryAccess = vi.mocked(previewDirectoryAccess)
const mockReportDirectoryAccess = vi.mocked(reportDirectoryAccess)
const mockSendTestAlert = vi.mocked(sendTestAlert)

function expectCalledWithOnlyAbortSignal(mockFn: ReturnType<typeof vi.fn>) {
  const call = mockFn.mock.calls.find(([options]) => {
    return (options as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
  })
  expect(call).toBeTruthy()
  expect(Object.keys((call?.[0] ?? {}) as Record<string, unknown>).sort()).toEqual(['signal'])
}

function expectUpdateSettingsCalledWith(request: unknown) {
  expect(mockUpdateSettings).toHaveBeenCalledWith(request, expect.objectContaining({
    signal: expect.any(AbortSignal),
  }))
}

function expectUpdateSettingsLastCalledWith(request: unknown) {
  expect(mockUpdateSettings).toHaveBeenLastCalledWith(request, expect.objectContaining({
    signal: expect.any(AbortSignal),
  }))
}

const defaultVersioningExtensionsText = [
  '.md', '.txt', '.org', '.rst', '.tex',
  '.go', '.rs', '.py', '.ts', '.js', '.tsx', '.jsx',
  '.c', '.cpp', '.h', '.java', '.kt', '.swift',
  '.toml', '.yaml', '.yml', '.json', '.xml',
  '.sh', '.bash', '.zsh', '.fish',
].join('\n')
const defaultVersioningFilenamesText = [
  'Makefile', 'Dockerfile', 'Vagrantfile',
  'LICENSE', 'README', 'CHANGELOG',
  '.gitignore', '.dockerignore', '.editorconfig',
].join('\n')

const { defaultSettingsResponse, defaultSecurityCheckResponse } = vi.hoisted(() => ({
  defaultSettingsResponse: {
    data: {
      server: { host: '0.0.0.0', port: 8080, read_timeout: '30s', write_timeout: '60s', idle_timeout: '120s', trusted_proxy_hops: 1, trusted_proxy_cidrs: ['10.0.0.0/8'], read_timeout_seconds: 60, write_timeout_seconds: 300 },
      storage: { root: '~/.mnemonas' },
      auth: { enabled: true, access_token_ttl: '15m0s', refresh_token_ttl: '168h0m0s' },
    trash: { enabled: true, retention_days: 30, max_size: 10737418240 },
      retention: { max_versions: 100, max_age: '8760h', min_free_space: 10737418240, gc_interval: '24h' },
      versioning: { auto_versioned_extensions: ['.md', '.txt', '.go'], auto_versioned_filenames: ['README', 'Dockerfile', 'Makefile'], max_versioned_size: 104857600 },
      webdav: { enabled: true, runtime_enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
      share: { enabled: false, base_url: '' },
      favorites: { enabled: true, runtime_available: true },
      alerts: { enabled: false, check_interval: '1h', threshold_pct: 90, critical_pct: 95, min_free_bytes: 10737418240, cooldown_period: '4h', webhook_url: '', webhook_method: 'POST', webhook_headers: [], telegram_enabled: false, telegram_bot_token_configured: false, telegram_chat_id: '', wecom_enabled: false, wecom_webhook_url: '', wecom_webhook_url_configured: false, dingtalk_enabled: false, dingtalk_webhook_url: '', dingtalk_webhook_url_configured: false, email_enabled: false, smtp_host: '', smtp_port: 587, smtp_username: '', smtp_password_configured: false, smtp_from: '', smtp_to: [] },
      maintenance: { scrub: { enabled: false, schedule_interval: '168h', retry_interval: '1h', max_retries: 1 } },
      disk_health: { enabled: false, check_interval: '1h', probe_timeout: '15s', cooldown_period: '4h', command: 'smartctl', temperature_warning_c: 50, temperature_critical_c: 60, media_wear_warning_percent: 80, media_wear_critical_percent: 100, devices: [] },
      cdc: { min_chunk_size: 262144, avg_chunk_size: 1048576, max_chunk_size: 4194304 },
      dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
    },
  },
  defaultSecurityCheckResponse: {
    success: true,
    data: {
      status: 'warning' as const,
      generated_at: '2026-05-08T00:00:00Z',
      checks: [
        {
          id: 'https_request',
          status: 'warning' as const,
          title: '当前访问不是 HTTPS',
          message: '公网访问前应通过内置 TLS 或受信反向代理提供 HTTPS。',
        },
        {
          id: 'auth_enabled',
          status: 'pass' as const,
          title: 'Web 登录认证已启用',
          message: '管理界面需要账号登录。',
        },
      ],
      request: { scheme: 'http' },
      config: { trusted_proxy_hops: 1, trusted_proxy_cidrs: ['10.0.0.0/8'] },
    },
  },
}))

vi.mock('@heroui/react', async () => {
  const actual = await vi.importActual<typeof import('@heroui/react')>('@heroui/react')
  const React = await vi.importActual<typeof import('react')>('react')

  const normalizeKey = (key: React.Key | null | undefined) => String(key ?? '').replace(/^\.\$/, '')

  const Tab = ({ children }: { children: React.ReactNode }) => <>{children}</>

  const Tabs = ({
    children,
    selectedKey,
    onSelectionChange,
  }: {
    children: React.ReactNode
    selectedKey?: React.Key
    onSelectionChange?: (key: React.Key) => void
  }) => {
    const items = React.Children.toArray(children).filter(React.isValidElement)
    const activeKey = selectedKey ?? items[0]?.key
    const activeItem = items.find((item) => normalizeKey(item.key) === normalizeKey(activeKey)) ?? items[0]

    return (
      <div>
        <div role="tablist">
          {items.map((item) => (
            <button
              key={normalizeKey(item.key)}
              type="button"
              role="tab"
              aria-selected={normalizeKey(item.key) === normalizeKey(activeKey)}
              onClick={() => onSelectionChange?.(normalizeKey(item.key))}
            >
              {item.props.title}
            </button>
          ))}
        </div>
        <div>{activeItem}</div>
      </div>
    )
  }

  return {
    ...actual,
    Tabs,
    Tab,
  }
})

vi.mock('@/components/share', () => ({
  ShareManager: () => <div>ShareManager</div>,
  normalizeShareReviewFilter: (value: string | null | undefined) => {
    const filters = new Set(['all', 'review', 'expiring', 'passwordless', 'broad', 'stale'])
    return value && filters.has(value) ? value : 'all'
  },
}))

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useUser: () => mockUser,
  }
})

// Mock the settings API
vi.mock('@/api/settings', () => ({
  SettingsError: class SettingsError extends Error {
    status: number
    code?: string
    constructor(message: string, status: number, code?: string) {
      super(message)
      this.status = status
      this.code = code
    }
    get isUnavailable() {
      return this.status === 503 || this.code === 'SERVICE_UNAVAILABLE'
    }
  },
  getSettings: vi.fn().mockResolvedValue(defaultSettingsResponse),
  getSecurityCheck: vi.fn().mockResolvedValue(defaultSecurityCheckResponse),
  updateSettings: vi.fn().mockResolvedValue({ success: true }),
  sendTestAlert: vi.fn().mockResolvedValue({
    success: true,
    message: 'test alert sent',
    data: { event_type: 'alert_test', channels: ['webhook'] },
  }),
  checkDirectoryAccess: vi.fn().mockResolvedValue({
    username: 'alice',
    user_id: 'u1',
    role: 'user',
    groups: ['family'],
    home_dir: '/users/alice',
    path: '/team/readme.txt',
    read: { mode: 'read', allowed: true, source: 'directory_access_rule', matched_rule: { path: '/team', read_groups: ['family'] } },
    write: { mode: 'write', allowed: false, source: 'directory_access_rule', matched_rule: { path: '/team', read_groups: ['family'] } },
  }),
  reportDirectoryAccess: vi.fn().mockResolvedValue({
    path: '/team/readme.txt',
    summary: { users: 2, read_allowed: 1, read_denied: 1, write_allowed: 1, write_denied: 1, related_shares: 1, active_related_shares: 1, password_protected_shares: 1 },
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
  }),
  previewDirectoryAccess: vi.fn().mockResolvedValue({
    path: '/team/readme.txt',
    preview: true,
    summary: { users: 2, read_allowed: 2, read_denied: 0, write_allowed: 1, write_denied: 1, related_shares: 1, active_related_shares: 1, password_protected_shares: 1 },
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
        read: { mode: 'read', allowed: true, source: 'directory_access_rule', matched_rule: { path: '/team', read_roles: ['user'] } },
        write: { mode: 'write', allowed: false, source: 'directory_access_rule', matched_rule: { path: '/team', read_roles: ['user'] } },
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
  }),
  getWebDAVCredentials: vi.fn().mockResolvedValue({
    success: true,
    enabled: true,
    url: '/dav/',
    auth_type: 'basic',
    username: 'admin',
    password: 'secret',
  }),
}))

describe('SettingsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUser.id = 'u1'
    mockUser.username = 'admin'
    mockUser.role = 'admin'
    mockUser.email = 'admin@local'
    mockUser.homeDir = '/'
    window.history.pushState({}, '', '/settings')
    mockGetSettings.mockResolvedValue(defaultSettingsResponse)
    mockGetSecurityCheck.mockResolvedValue(defaultSecurityCheckResponse)
    mockSendTestAlert.mockResolvedValue({
      success: true,
      message: 'test alert sent',
      data: { event_type: 'alert_test', channels: ['webhook'] },
    })
    mockGetWebDAVCredentials.mockResolvedValue({
      enabled: true,
      url: '/dav/',
      auth_type: 'basic',
      username: 'admin',
      password: 'secret',
    })
    mockCheckDirectoryAccess.mockResolvedValue({
      username: 'alice',
      user_id: 'u1',
      role: 'user',
      groups: ['family'],
      home_dir: '/users/alice',
      path: '/team/readme.txt',
      read: { mode: 'read', allowed: true, source: 'directory_access_rule', message: 'directory access rule grants read', matched_rule: { path: '/team', read_groups: ['family'] } },
      write: { mode: 'write', allowed: false, source: 'directory_access_rule', message: 'directory access rule does not grant write', matched_rule: { path: '/team', read_groups: ['family'] } },
    })
    mockReportDirectoryAccess.mockResolvedValue({
      path: '/team/readme.txt',
      summary: { users: 2, read_allowed: 1, read_denied: 1, write_allowed: 1, write_denied: 1, related_shares: 1, active_related_shares: 1, password_protected_shares: 1 },
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
    })
    mockPreviewDirectoryAccess.mockResolvedValue({
      path: '/team/readme.txt',
      preview: true,
      summary: { users: 2, read_allowed: 2, read_denied: 0, write_allowed: 1, write_denied: 1, related_shares: 1, active_related_shares: 1, password_protected_shares: 1 },
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
          read: { mode: 'read', allowed: true, source: 'directory_access_rule', matched_rule: { path: '/team', read_roles: ['user'] } },
          write: { mode: 'write', allowed: false, source: 'directory_access_rule', matched_rule: { path: '/team', read_roles: ['user'] } },
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
    })
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
  })

  const createDeferred = <T,>() => {
    let resolve!: (value: T | PromiseLike<T>) => void
    let reject!: (reason?: unknown) => void
    const promise = new Promise<T>((res, rej) => {
      resolve = res
      reject = rej
    })
    return { promise, resolve, reject }
  }

  const openTab = async (user: ReturnType<typeof userEvent.setup>, label: string) => {
    await waitFor(() => {
      expect(screen.getByRole('tab', { name: label })).toBeTruthy()
    })
    await user.click(screen.getByRole('tab', { name: label }))
  }

  it('passes abort signals to settings and security-check queries', async () => {
    render(<SettingsPage />)

    await waitFor(() => {
      expectCalledWithOnlyAbortSignal(mockGetSettings)
      expectCalledWithOnlyAbortSignal(mockGetSecurityCheck)
    })
  })

  afterEach(() => {
    if (originalClipboardDescriptor) {
      Object.defineProperty(navigator, 'clipboard', originalClipboardDescriptor)
    } else {
      Reflect.deleteProperty(navigator, 'clipboard')
    }
  })

  it('passes abort signals to the WebDAV credentials query', async () => {
    window.history.pushState({}, '', '/settings?tab=webdav')

    render(<SettingsPage />)

    await waitFor(() => {
      expectCalledWithOnlyAbortSignal(mockGetWebDAVCredentials)
    })
  })

  it('passes an abort signal when saving settings', async () => {
    render(<SettingsPage />)

    const portInput = await screen.findByLabelText('服务器端口')
    fireEvent.change(portInput, { target: { value: '9000' } })
    fireEvent.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockUpdateSettings).toHaveBeenCalledWith(
        expect.objectContaining({
          server: expect.objectContaining({ port: 9000 }),
        }),
        expect.objectContaining({
          signal: expect.any(AbortSignal),
        }),
      )
    })
  })

  it('aborts a pending settings save when the page unmounts and ignores abort feedback', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const pendingSave = createDeferred<{ success: boolean; message: string }>()
    let signal: AbortSignal | undefined
    mockUpdateSettings.mockImplementationOnce((_request, options) => {
      signal = options?.signal
      return pendingSave.promise
    })
    const view = render(<SettingsPage />)

    const portInput = await screen.findByLabelText('服务器端口')
    await user.clear(portInput)
    await user.type(portInput, '9000')
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(signal).toBeInstanceOf(AbortSignal)
    })

    view.unmount()
    expect(signal?.aborted).toBe(true)

    await act(async () => {
      pendingSave.reject(new DOMException('settings save aborted', 'AbortError'))
      await Promise.resolve()
    })

    expect(mockAddToast).not.toHaveBeenCalledWith(expect.objectContaining({
      title: '保存失败',
    }))
    expect(mockAddToast).not.toHaveBeenCalledWith(expect.objectContaining({
      title: '保存设置暂不可用',
    }))
  })

  describe('rendering', () => {
    it('keeps the page header and actions visible while settings are loading', () => {
      mockGetSettings.mockReturnValue(new Promise(() => {}) as ReturnType<typeof getSettings>)

      render(<SettingsPage />)

      expect(screen.getByRole('heading', { name: '设置' })).toBeTruthy()
      expect(screen.getByRole('button', { name: '重置' })).toBeTruthy()
      expect(screen.getByRole('button', { name: '保存设置' })).toBeTruthy()
      expect(screen.getByText('加载设置...')).toBeTruthy()
    })

    it('renders page header', async () => {
      render(<SettingsPage />)
      await waitFor(() => {
        expect(screen.getByText('设置')).toBeTruthy()
        expect(screen.getByText('调整网络、访问和数据保留')).toBeTruthy()
      })
    })

    it('renders save button', async () => {
      render(<SettingsPage />)
      await waitFor(() => {
        expect(screen.getByText('保存设置')).toBeTruthy()
      })
    })

    it('renders reset button', async () => {
      render(<SettingsPage />)
      await waitFor(() => {
        expect(screen.getByText('重置')).toBeTruthy()
      })
    })

    it('refetches settings and WebDAV credentials when the auth scope changes', async () => {
    window.history.pushState({}, '', '/settings?tab=webdav')
    mockGetSettings
      .mockResolvedValueOnce(defaultSettingsResponse)
      .mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          webdav: {
            ...defaultSettingsResponse.data.webdav,
            username: 'other-admin',
          },
        },
      })
    mockGetWebDAVCredentials
      .mockResolvedValueOnce({
        enabled: true,
        url: '/dav/',
        auth_type: 'basic',
        username: 'admin',
        password: 'secret',
      })
      .mockResolvedValueOnce({
        enabled: true,
        url: '/dav/',
        auth_type: 'basic',
        username: 'other-admin',
        password: 'secret-2',
      })

    const { rerender } = render(<SettingsPage />)

    await waitFor(() => {
      expect(mockGetSettings).toHaveBeenCalledTimes(1)
      expect(mockGetWebDAVCredentials).toHaveBeenCalledTimes(1)
    })

    mockUser.id = 'u2'
    mockUser.username = 'other-admin'
    mockUser.email = 'other@local'

    rerender(<SettingsPage />)

    await waitFor(() => {
      expect(mockGetSettings).toHaveBeenCalledTimes(2)
      expect(mockGetWebDAVCredentials).toHaveBeenCalledTimes(2)
    })
    })

  })

  describe('tabs', () => {
    it('renders all setting tabs', async () => {
      render(<SettingsPage />)
      await waitFor(() => {
        expect(screen.getByText('常规')).toBeTruthy()
        expect(screen.getByText('版本保留')).toBeTruthy()
        expect(screen.getByText('WebDAV')).toBeTruthy()
        expect(screen.getByText('高级')).toBeTruthy()
      })
    })

    it('shows general settings by default', async () => {
      render(<SettingsPage />)
      await waitFor(() => {
        expect(screen.getByText('公网访问向导')).toBeTruthy()
        expect(screen.getByText('公网访问安全自检')).toBeTruthy()
        expect(screen.getByText('证书续期检查')).toBeTruthy()
        expect(screen.getByText("sudo journalctl -u caddy --since '24 hours ago'")).toBeTruthy()
        expect(screen.getByText('当前访问不是 HTTPS')).toBeTruthy()
        expect(screen.getByText('Web 自检只覆盖当前服务可观察到的运行态。公网域名、证书链、云防火墙和端口暴露仍需在服务器上运行 mnemonas-doctor 复核。')).toBeTruthy()
        expect(screen.getByText('服务器')).toBeTruthy()
        expect(screen.getByText('存储路径')).toBeTruthy()
      })
    })

    it('applies public access recommendations to the settings form', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('公网访问向导')).toBeTruthy()
        expect(screen.getByLabelText('服务器监听地址')).toHaveValue('0.0.0.0')
      })

      fireEvent.change(screen.getByLabelText('公网域名'), { target: { value: 'nas.example.com' } })
      await user.click(screen.getByRole('button', { name: '应用推荐到表单' }))

      expect(screen.getByLabelText('服务器监听地址')).toHaveValue('127.0.0.1')
      expect(screen.getByLabelText('受信代理层数')).toHaveValue(1)
      expect(screen.getByLabelText('访问令牌有效期')).toHaveValue('15m0s')
      expect(screen.getByLabelText('刷新令牌有效期')).toHaveValue('168h0m0s')
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已应用公网访问推荐',
        description: expect.stringContaining('会话有效期、新分享默认有效期和默认访问次数会保持在公网建议范围内'),
      }))
    })

    it('caps long session token lifetimes when applying public access recommendations', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          auth: {
            ...defaultSettingsResponse.data.auth,
            access_token_ttl: '2h0m0s',
            refresh_token_ttl: '1080h0m0s',
          },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('公网访问向导')).toBeTruthy()
        expect(screen.getByLabelText('访问令牌有效期')).toHaveValue('2h0m0s')
      })

      fireEvent.change(screen.getByLabelText('公网域名'), { target: { value: 'nas.example.com' } })
      await user.click(screen.getByRole('button', { name: '应用推荐到表单' }))

      expect(screen.getByLabelText('访问令牌有效期')).toHaveValue('1h')
      expect(screen.getByLabelText('刷新令牌有效期')).toHaveValue('720h')

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          auth: expect.objectContaining({
            access_token_ttl: '1h',
            refresh_token_ttl: '720h',
          }),
        }))
      })
    })

    it('requires a public domain before applying recommendations while sharing is enabled', async () => {
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            base_url: '',
          },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('公网访问向导')).toBeTruthy()
      })

      expect(screen.getByText('填写公网域名后设置')).toBeTruthy()
      expect(screen.queryByText('https://nas.example.com')).toBeNull()
      expect(screen.getByRole('button', { name: '应用推荐到表单' })).toBeDisabled()
    })

    it('normalizes public access domains before previewing commands and share URLs', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            base_url: '',
          },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('公网访问向导')).toBeTruthy()
      })

      fireEvent.change(screen.getByLabelText('公网域名'), { target: { value: 'HTTPS://NAS.EXAMPLE.COM./' } })

      expect(screen.getByText('https://nas.example.com')).toBeTruthy()
      expect(screen.getByText('sudo mnemonas-doctor --public-domain nas.example.com')).toBeTruthy()
      expect(screen.queryByText('https://nas.example.com.')).toBeNull()

      await user.click(screen.getByRole('button', { name: '应用推荐到表单' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            enabled: true,
            base_url: 'https://nas.example.com',
          }),
        }))
      })
    })

    it('sets unsafe public sharing defaults to seven days when applying recommendations', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            base_url: '',
            default_expires_in: '0',
            default_max_access: 0,
          },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('公网访问向导')).toBeTruthy()
      })

      fireEvent.change(screen.getByLabelText('公网域名'), { target: { value: 'nas.example.com' } })
      await user.click(screen.getByRole('button', { name: '应用推荐到表单' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            enabled: true,
            base_url: 'https://nas.example.com',
            default_expires_in: '168h',
            default_max_access: 20,
          }),
        }))
      })
    })

    it('keeps stricter public sharing defaults when applying recommendations', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            base_url: '',
            default_expires_in: '24h',
            default_max_access: 3,
          },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('公网访问向导')).toBeTruthy()
      })

      fireEvent.change(screen.getByLabelText('公网域名'), { target: { value: 'nas.example.com' } })
      await user.click(screen.getByRole('button', { name: '应用推荐到表单' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            enabled: true,
            base_url: 'https://nas.example.com',
            default_expires_in: '24h',
            default_max_access: 3,
          }),
        }))
      })
    })

    it('rejects public access domains that include a path or port', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('公网访问向导')).toBeTruthy()
      })

      fireEvent.change(screen.getByLabelText('公网域名'), { target: { value: 'https://nas.example.com:8443/path' } })

      expect(screen.getByText('请输入域名，不要包含路径或端口')).toBeTruthy()
      expect(screen.queryByText('https://nas.example.com')).toBeNull()
      expect(screen.getByText('sudo mnemonas-public-setup --proxy caddy nas.example.com admin@example.com')).toBeTruthy()
      expect(screen.getByText('sudo mnemonas-doctor --public-domain nas.example.com')).toBeTruthy()
      expect(screen.queryByText('sudo mnemonas-doctor --public-domain https://nas.example.com:8443/path')).toBeNull()
      expect(screen.getByRole('button', { name: '应用推荐到表单' })).toBeDisabled()

      await user.click(screen.getByRole('button', { name: '应用推荐到表单' }))

      expect(screen.getByLabelText('公网域名')).toHaveValue('https://nas.example.com:8443/path')
    })

    it('rejects public access domains with invalid hostname labels', async () => {
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('公网访问向导')).toBeTruthy()
      })

      fireEvent.change(screen.getByLabelText('公网域名'), { target: { value: 'bad_domain.example.com' } })

      expect(screen.getByText('请输入有效域名，域名标签只能包含字母、数字和连字符，且不能以连字符开头或结尾')).toBeTruthy()
      expect(screen.queryByText('https://bad_domain.example.com')).toBeNull()
      expect(screen.getByText('sudo mnemonas-public-setup --proxy caddy nas.example.com admin@example.com')).toBeTruthy()
      expect(screen.getByText('sudo mnemonas-doctor --public-domain nas.example.com')).toBeTruthy()
      expect(screen.queryByText('sudo mnemonas-doctor --public-domain bad_domain.example.com')).toBeNull()
      expect(screen.getByRole('button', { name: '应用推荐到表单' })).toBeDisabled()
    })

    it('switches certificate renewal guidance for nginx proxy setup', async () => {
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('公网访问向导')).toBeTruthy()
      })

      fireEvent.change(screen.getByLabelText('反向代理'), { target: { value: 'nginx' } })

      expect(screen.getByText('sudo certbot renew --dry-run')).toBeTruthy()
    })

    it('uses localized accessible names for public access copy buttons', async () => {
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('公网访问向导')).toBeTruthy()
      })

      expect(screen.getByRole('button', { name: '复制公网配置命令' })).toBeTruthy()
      expect(screen.getByRole('button', { name: '复制公网自检命令' })).toBeTruthy()
      expect(screen.getByRole('button', { name: '复制证书续期日志命令' })).toBeTruthy()
      expect(screen.queryByRole('button', { name: 'Copy to clipboard' })).toBeNull()

      fireEvent.change(screen.getByLabelText('反向代理'), { target: { value: 'nginx' } })

      expect(screen.getByRole('button', { name: '复制证书续期演练命令' })).toBeTruthy()
      expect(screen.queryByRole('button', { name: '复制证书续期日志命令' })).toBeNull()
    })

    it('offers a repair action for security check findings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'server_listen',
              status: 'warning',
              title: 'Web 服务监听范围偏宽',
              message: '建议只监听本机地址。',
            },
          ],
          request: { scheme: 'http' },
          config: { server_host: '0.0.0.0' },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('Web 服务监听范围偏宽')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '改为本机监听' }))

      expect(screen.getByLabelText('服务器监听地址')).toHaveValue('127.0.0.1')
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已改为本机监听',
      }))
    })

    it('does not show repair actions for passing security checks', async () => {
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'pass',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'auth_enabled',
              status: 'pass',
              title: 'Web 登录认证已启用',
              message: '管理界面需要账号登录。',
            },
            {
              id: 'session_token_ttl',
              status: 'pass',
              title: '会话有效期处于建议范围',
              message: 'Web UI 访问令牌和刷新令牌有效期处于公网部署建议范围内。',
            },
            {
              id: 'login_rate_limit',
              status: 'pass',
              title: '登录失败限速已启用',
              message: '连续失败登录会按用户名和客户端 IP 触发短期锁定。',
            },
            {
              id: 'share_default_policy',
              status: 'pass',
              title: '分享默认策略处于建议范围',
              message: '新分享默认有效期和默认访问次数处于公网部署建议范围内。',
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true, share_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('Web 登录认证已启用')).toBeTruthy()
        expect(screen.getByText('会话有效期处于建议范围')).toBeTruthy()
        expect(screen.getByText('登录失败限速已启用')).toBeTruthy()
      })

      expect(screen.queryByRole('button', { name: '启用认证' })).toBeNull()
      expect(screen.queryByRole('button', { name: '应用建议' })).toBeNull()
    })

    it('shows login rate-limit guidance when auth is disabled', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'login_rate_limit',
              status: 'warning',
              title: '登录限速检查不可用',
              message: 'backend raw login rate-limit detail',
              details: {
                auth_enabled: false,
                enabled: false,
                failure_limit: 0,
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: false },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('登录限速检查不可用')).toBeTruthy()
        expect(screen.getByText('Web 登录认证未启用，登录失败限速暂不可用。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw login rate-limit detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '启用认证' }))

      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '需要启用 Web 登录认证',
      }))
    })

    it('maps security check backend messages before rendering them', async () => {
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'server_listen',
              status: 'warning',
              title: 'Web 服务监听范围偏宽',
              message: 'backend raw server listen detail',
            },
            {
              id: 'custom_probe',
              status: 'warning',
              title: '自定义安全检查',
              message: 'backend raw custom probe detail',
            },
          ],
          request: { scheme: 'http' },
          config: { server_host: '0.0.0.0' },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('建议仅监听本机地址，并由受信反向代理对外提供 HTTPS。')).toBeTruthy()
        expect(screen.getByText('该安全检查需要确认，请检查相关配置。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw server listen detail')).toBeNull()
      expect(screen.queryByText('backend raw custom probe detail')).toBeNull()
    })

    it('shows SMB preview security guidance without exposing raw backend text', async () => {
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'smb_preview',
              status: 'warning',
              title: 'SMB 仍是预览能力',
              message: 'backend raw smb preview detail',
              details: {
                runtime_available: false,
                listen: '0.0.0.0:1445',
                listen_loopback: false,
                share_count: 1,
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('SMB 仍是预览能力')).toBeTruthy()
        expect(screen.getByText('当前构建未包含可挂载的 SMB/Samba 运行组件；启用前应先收紧监听范围和防火墙。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw smb preview detail')).toBeNull()
      expect(screen.queryByText(/未内置/)).toBeNull()
    })

    it('shows users file access security guidance without exposing raw backend text', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'users_file_access',
              status: 'warning',
              title: '用户文件权限过宽',
              message: 'backend raw users file mode detail',
              details: {
                dir: '/srv/mnemonas/.mnemonas',
                path: '/srv/mnemonas/.mnemonas/users.json',
                dir_mode: '0755',
                file_mode: '0644',
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('用户文件权限过宽')).toBeTruthy()
        expect(screen.getByText('用户文件或其目录允许组或其他用户访问；建议将目录设为 0700、用户文件设为 0600。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw users file mode detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '查看权限路径' }))

      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '需要收紧用户文件权限',
        description: expect.stringContaining('用户文件目录 /srv/mnemonas/.mnemonas 设为 0700，将用户文件 /srv/mnemonas/.mnemonas/users.json 设为 0600'),
      }))
    })

    it('shows config file symlink component guidance without exposing raw backend text', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'config_file_access',
              status: 'block',
              title: '配置文件路径包含符号链接',
              message: 'backend raw config file symlink component detail',
              details: {
                path: '/srv/mnemonas/config/config.toml',
                path_kind: 'symlink_component',
                symlink_component: '/srv/mnemonas/config',
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('配置文件路径包含符号链接')).toBeTruthy()
        expect(screen.getByText('配置文件路径经过符号链接组件；请改为服务账号可读取的普通私有路径。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw config file symlink component detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '查看配置路径' }))

      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '需要检查配置文件路径',
        description: expect.stringContaining('/srv/mnemonas/config/config.toml 是普通文件、路径组件不经过符号链接'),
      }))
    })

    it('redacts sensitive path fragments in security check action toasts', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'config_file_access',
              status: 'block',
              title: '配置文件路径包含敏感片段',
              message: 'backend raw config file detail token=config-secret',
              details: {
                path: '/srv/mnemonas/token=config-secret/config.toml',
                path_kind: 'symlink_component',
                symlink_component: '/srv/mnemonas/token=config-secret',
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('配置文件路径包含敏感片段')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw config file detail token=config-secret')).toBeNull()

      await user.click(screen.getByRole('button', { name: '查看配置路径' }))

      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '需要检查配置文件路径',
        description: expect.stringContaining('token=<redacted>'),
      }))
      const toastDescriptions = mockAddToast.mock.calls
        .map(([toast]) => (toast as { description?: unknown }).description)
        .filter((description): description is string => typeof description === 'string')
        .join('\n')
      expect(toastDescriptions).not.toContain('config-secret')
    })

    it('shows generated WebDAV secrets symlink component guidance without exposing raw backend text', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'secrets_file_access',
              status: 'block',
              title: '自动 WebDAV 凭据路径包含符号链接',
              message: 'backend raw secrets file symlink component detail',
              details: {
                path: '/srv/mnemonas/data/secrets.json',
                path_kind: 'symlink_component',
                symlink_component: '/srv/mnemonas/data',
                generated_webdav_password_required: true,
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true, webdav_enabled: true, webdav_auth_type: 'basic' },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('自动 WebDAV 凭据路径包含符号链接')).toBeTruthy()
        expect(screen.getByText('自动 WebDAV 凭据路径经过符号链接组件；请改为服务账号可读取的普通私有路径。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw secrets file symlink component detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '查看凭据路径' }))

      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '需要检查自动 WebDAV 凭据',
        description: expect.stringContaining('/srv/mnemonas/data/secrets.json 是普通文件、路径组件不经过符号链接'),
      }))
    })

    it('shows local backup destination guidance without exposing raw backend text', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'backup_local_destinations',
              status: 'block',
              title: '本地备份目标位于主存储内',
              message: 'backend raw backup destination detail',
              details: {
                job_id: 'external-disk',
                destination: '/srv/mnemonas/data/backups',
                source: '/srv/mnemonas/data',
                storage_root: '/srv/mnemonas/data',
                destination_kind: 'inside_storage_root',
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('本地备份目标位于主存储内')).toBeTruthy()
        expect(screen.getByText('本地备份目标位于 storage.root 内部；请改用独立磁盘、独立数据集或远端备份目标。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw backup destination detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '查看备份目标' }))

      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '需要检查本地备份目标',
        description: expect.stringContaining('备份作业 external-disk 的目标目录 /srv/mnemonas/data/backups'),
      }))
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        description: expect.stringContaining('移出 storage.root'),
      }))
      await waitFor(() => {
        expect(window.location.pathname).toBe('/maintenance')
        expect(new URLSearchParams(window.location.search).get('backupJob')).toBe('external-disk')
      })
    })

    it('shows writable local backup destination action guidance', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'backup_local_destinations',
              status: 'warning',
              title: '本地备份目标不可写',
              message: 'backend raw backup writable detail',
              details: {
                job_id: 'external-disk',
                destination: '/mnt/backup-drive/mnemonas',
                destination_kind: 'not_writable',
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('本地备份目标不可写')).toBeTruthy()
        expect(screen.getByText('本地备份目标目录没有写权限位；请确认 MnemoNAS 服务账号可以写入该目录。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw backup writable detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '查看备份目标' }))

      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '需要检查本地备份目标',
        description: expect.stringContaining('授予 MnemoNAS 服务账号写权限'),
      }))
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        description: expect.stringContaining('/mnt/backup-drive/mnemonas'),
      }))
      await waitFor(() => {
        expect(window.location.pathname).toBe('/maintenance')
        expect(new URLSearchParams(window.location.search).get('backupJob')).toBe('external-disk')
      })
    })

    it('shows users file symlink component guidance without exposing raw backend text', async () => {
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'users_file_access',
              status: 'block',
              title: '用户文件目录路径包含符号链接',
              message: 'backend raw users file symlink component detail',
              details: {
                dir: '/srv/mnemonas/.mnemonas',
                path: '/srv/mnemonas/.mnemonas/users.json',
                dir_kind: 'symlink_component',
                symlink_component: '/srv/mnemonas',
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('用户文件目录路径包含符号链接')).toBeTruthy()
        expect(screen.getByText('用户文件路径经过符号链接组件；请改为服务账号可读取的普通私有目录路径。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw users file symlink component detail')).toBeNull()
    })

    it('shows initial password symlink guidance without exposing raw backend text', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'initial_password_file',
              status: 'block',
              title: '初始管理员密码路径是符号链接',
              message: 'backend raw initial password symlink detail',
              details: {
                path: '/srv/mnemonas/.mnemonas/initial-password.txt',
                path_kind: 'symlink',
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('初始管理员密码路径是符号链接')).toBeTruthy()
        expect(screen.getByText('初始管理员密码路径是符号链接；公网访问前请删除该路径，避免初始凭据指向不受控位置。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw initial password symlink detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '查看文件路径' }))

      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '需要移除初始密码路径',
        description: expect.stringContaining('/srv/mnemonas/.mnemonas/initial-password.txt'),
      }))
    })

    it('shows initial password symlink component guidance without exposing raw backend text', async () => {
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'initial_password_file',
              status: 'block',
              title: '初始管理员密码路径包含符号链接',
              message: 'backend raw initial password symlink component detail',
              details: {
                path: '/srv/mnemonas/.mnemonas/initial-password.txt',
                path_kind: 'symlink_component',
                symlink_component: '/srv/mnemonas',
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('初始管理员密码路径包含符号链接')).toBeTruthy()
        expect(screen.getByText('初始管理员密码路径经过符号链接组件；公网访问前请改为普通私有目录并确认该文件不存在。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw initial password symlink component detail')).toBeNull()
    })

    it('repairs session token TTL findings without exposing raw backend text', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'session_token_ttl',
              status: 'warning',
              title: '会话有效期偏长',
              message: 'backend raw session ttl detail',
              details: {
                access_token_ttl: '2h0m0s',
                refresh_token_ttl: '1080h0m0s',
                access_token_ttl_too_long: true,
                refresh_token_ttl_too_long: true,
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('会话有效期偏长')).toBeTruthy()
        expect(screen.getByText('Web UI 会话有效期偏长，公网访问前建议缩短配置以降低会话泄露后的风险窗口。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw session ttl detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '应用建议' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          auth: expect.objectContaining({
            access_token_ttl: '1h',
            refresh_token_ttl: '720h',
          }),
        }))
      })
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已应用会话有效期建议',
      }))
    })

    it('shows browser session boundary guidance without exposing raw backend text', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'browser_session_boundary',
              status: 'warning',
              title: '浏览器会话 cookie 未使用 Secure',
              message: 'backend raw browser session boundary detail',
              details: {
                auth_enabled: true,
                session_cookie_secure: false,
                same_origin_browser_write_protection: true,
                request_scheme: 'http',
              },
            },
          ],
          request: { scheme: 'http' },
          config: { auth_enabled: true, trusted_proxy_hops: 0 },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('浏览器会话 cookie 未使用 Secure')).toBeTruthy()
        expect(screen.getByText('当前访问未被识别为 HTTPS，Web UI 会话和下载 cookie 不会带 Secure 标记；公网访问前请修正 TLS 或受信代理配置。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw browser session boundary detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '应用代理推荐' }))

      expect(screen.getByLabelText('服务器监听地址')).toHaveValue('127.0.0.1')
      expect(screen.getByLabelText('受信代理层数')).toHaveValue(1)
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已应用反向代理推荐',
      }))
    })

    it('shows public share boundary guidance without exposing raw backend text', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'public_share_boundary',
              status: 'warning',
              title: '公开分享访问 cookie 未使用 Secure',
              message: 'backend raw public share boundary detail',
              details: {
                share_enabled: true,
                password_cookie_secure: false,
                password_cookie_same_site: 'Strict',
                metadata_vary_cookie: true,
              },
            },
          ],
          request: { scheme: 'http' },
          config: { auth_enabled: true, trusted_proxy_hops: 0 },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('公开分享访问 cookie 未使用 Secure')).toBeTruthy()
        expect(screen.getByText('分享功能已启用，但当前访问未被识别为 HTTPS；受密码保护的公开分享访问 cookie 不会带 Secure 标记。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw public share boundary detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '应用代理推荐' }))

      expect(screen.getByLabelText('服务器监听地址')).toHaveValue('127.0.0.1')
      expect(screen.getByLabelText('受信代理层数')).toHaveValue(1)
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已应用反向代理推荐',
      }))
    })

    it('shows public share boundary block guidance without proxy repair action', async () => {
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'public_share_boundary',
              status: 'block',
              title: '公开分享浏览器边界异常',
              message: 'backend raw public share boundary block detail',
              details: {
                share_enabled: true,
                password_cookie_secure: true,
                password_cookie_same_site: 'Strict',
                metadata_vary_cookie: false,
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true, trusted_proxy_hops: 1 },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('公开分享浏览器边界异常')).toBeTruthy()
        expect(screen.getByText('公开分享访问 cookie、失败限速或缓存边界未满足公网安全要求。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw public share boundary block detail')).toBeNull()
      expect(screen.queryByText('分享功能已启用，但当前访问未被识别为 HTTPS；受密码保护的公开分享访问 cookie 不会带 Secure 标记。')).toBeNull()
      expect(screen.queryByRole('button', { name: '应用代理推荐' })).toBeNull()
    })

    it('repairs unsafe share default policy from the security check', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            default_expires_in: '0',
            default_max_access: 0,
          },
        },
      })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'share_default_policy',
              status: 'warning',
              title: '新分享默认不会过期且访问次数不限制',
              message: 'backend raw share default policy detail',
              details: {
                share_enabled: true,
                default_expires_in: '0s',
                default_expires_in_unlimited: true,
                default_max_access: 0,
                default_max_access_unlimited: true,
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true, share_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('新分享默认不会过期且访问次数不限制')).toBeTruthy()
        expect(screen.getByText('分享功能已启用，但新分享默认不会过期且访问次数不限制；家庭公网分享建议同时设置默认有效期和默认访问次数。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw share default policy detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '应用建议' }))

      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已应用分享默认策略建议',
      }))

      await openTab(user, '分享')

      expect(screen.getByLabelText('新分享默认有效期')).toHaveValue('168h')
      expect(screen.getByLabelText('新分享默认访问次数')).toHaveValue('20')
    })

    it('repairs unlimited share default access count from the security check', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            default_expires_in: '24h',
            default_max_access: 0,
          },
        },
      })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'share_default_policy',
              status: 'warning',
              title: '新分享默认访问次数不限制',
              message: 'backend raw share default policy detail',
              details: {
                share_enabled: true,
                default_expires_in: '24h0m0s',
                default_expires_in_seconds: 86400,
                default_max_access: 0,
                default_max_access_unlimited: true,
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true, share_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('新分享默认访问次数不限制')).toBeTruthy()
        expect(screen.getByText('分享功能已启用，但新分享默认访问次数不限制；家庭公网分享建议设置默认访问次数，避免公开链接被反复访问。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw share default policy detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '应用建议' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            default_expires_in: '24h',
            default_max_access: 20,
          }),
        }))
      })
    })

    it('repairs invalid share default policy values from the security check', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            default_expires_in: '-1h',
            default_max_access: -1,
          },
        },
      })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'share_default_policy',
              status: 'block',
              title: '分享默认策略无效',
              message: 'backend raw share default policy detail',
              details: {
                share_enabled: true,
                default_expires_in: '-1h0m0s',
                default_expires_in_seconds: -3600,
                default_max_access: -1,
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true, share_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('分享默认策略无效')).toBeTruthy()
        expect(screen.getByText('分享默认有效期或默认访问次数配置无效，请修复负值后重新检查。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw share default policy detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '应用建议' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            default_expires_in: '168h',
            default_max_access: 20,
          }),
        }))
      })
    })

    it('shows a specific security check message for share base URL host mismatch', async () => {
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'share_base_url',
              status: 'warning',
              title: '分享基础 URL 域名与当前访问域名不同',
              message: 'backend raw share host mismatch detail',
              details: {
                base_url: 'https://share.example.com',
                base_url_host: 'share.example.com',
                request_host: 'nas.example.com',
              },
            },
          ],
          request: { scheme: 'https', host: 'nas.example.com' },
          config: { share_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('分享基础 URL 与当前访问域名不同，请确认分享域名同样具备 HTTPS、认证和防火墙保护。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw share host mismatch detail')).toBeNull()
    })

    it('repairs share base URL host mismatch to the current request host', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            base_url: 'https://share.example.com/base/',
          },
        },
      })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'share_base_url',
              status: 'warning',
              title: '分享基础 URL 域名与当前访问域名不同',
              message: '分享基础 URL 使用其他域名。',
              details: {
                base_url: 'https://share.example.com/base/',
                base_url_host: 'share.example.com',
                request_host: 'nas.example.com',
              },
            },
          ],
          request: { scheme: 'https', host: 'nas.example.com' },
          config: { share_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('分享基础 URL 域名与当前访问域名不同')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '使用 HTTPS URL' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            enabled: true,
            base_url: 'https://nas.example.com/base/',
          }),
        }))
      })
    })

    it('repairs share base URLs that already include the share route', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            base_url: 'https://nas.example.com/s/',
          },
        },
      })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'share_base_url',
              status: 'warning',
              title: '分享基础 URL 包含分享路由',
              message: '当前值会生成重复的 /s/s 分享链接。',
              details: {
                base_url: 'https://nas.example.com/s/',
                base_url_path: '/s/',
              },
            },
          ],
          request: { scheme: 'https', host: 'nas.example.com' },
          config: { share_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('分享基础 URL 包含分享路由')).toBeTruthy()
        expect(screen.getByText('分享基础 URL 已包含 /s 分享路由，继续使用会生成重复的 /s/s 分享链接。')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '使用 HTTPS URL' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            enabled: true,
            base_url: 'https://nas.example.com',
          }),
        }))
      })
    })

    it('repairs share base URLs that include an escaped share route', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            base_url: 'https://nas.example.com/base%2Fs/',
          },
        },
      })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'share_base_url',
              status: 'warning',
              title: '分享基础 URL 包含分享路由',
              message: '当前值会生成重复的 /s/s 分享链接。',
              details: {
                base_url: 'https://nas.example.com/base%2Fs/',
                base_url_path: '/base/s/',
              },
            },
          ],
          request: { scheme: 'https', host: 'nas.example.com' },
          config: { share_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('分享基础 URL 已包含 /s 分享路由，继续使用会生成重复的 /s/s 分享链接。')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '使用 HTTPS URL' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            enabled: true,
            base_url: 'https://nas.example.com/base/',
          }),
        }))
      })
    })

    it('repairs share base URLs with duplicate path slashes', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            base_url: 'https://nas.example.com/shares%2F%2Fteam',
          },
        },
      })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'share_base_url',
              status: 'block',
              title: '分享基础 URL 路径包含重复斜杠',
              message: 'backend raw duplicate slash detail',
              details: {
                base_url: 'https://nas.example.com/shares%2F%2Fteam',
                base_url_path: '/shares//team',
              },
            },
          ],
          request: { scheme: 'https', host: 'nas.example.com' },
          config: { share_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('分享基础 URL 路径包含重复斜杠')).toBeTruthy()
        expect(screen.getByText('分享基础 URL 路径包含重复斜杠，继续使用可能被代理或浏览器规范化为不一致的分享地址。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw duplicate slash detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '使用 HTTPS URL' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            enabled: true,
            base_url: 'https://nas.example.com/shares/team',
          }),
        }))
      })
    })

    it.each([
      [
        'host-relative backslash path',
        '分享基础 URL 路径包含反斜杠',
        'backend raw host-relative backslash detail',
        { base_url: String.raw`https://nas.example.com\shares`, base_url_path: String.raw`\shares` },
        '分享基础 URL 路径包含反斜杠，继续使用可能被代理或浏览器规范化为不一致的分享地址。',
      ],
      [
        'backslash path',
        '分享基础 URL 路径包含反斜杠',
        'backend raw backslash detail',
        { base_url: 'https://nas.example.com/shares%5Cteam', base_url_path: '/shares\\team' },
        '分享基础 URL 路径包含反斜杠，继续使用可能被代理或浏览器规范化为不一致的分享地址。',
      ],
      [
        'dot segment path',
        '分享基础 URL 路径包含点段',
        'backend raw dot segment detail',
        { base_url: 'https://nas.example.com/shares/%2e%2e/team', base_url_path: '/shares/../team' },
        '分享基础 URL 路径包含 . 或 .. 路径段，继续使用可能被代理或浏览器规范化为不一致的分享地址。',
      ],
    ])('shows a specific security check message for share base URL with %s', async (_label, title, backendMessage, details, expectedMessage) => {
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'share_base_url',
              status: 'block',
              title,
              message: backendMessage,
              details,
            },
          ],
          request: { scheme: 'https', host: 'nas.example.com' },
          config: { share_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText(title)).toBeTruthy()
        expect(screen.getByText(expectedMessage)).toBeTruthy()
      })
      expect(screen.queryByText(backendMessage)).toBeNull()
    })

    it('repairs share base URLs with host-relative backslashes', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            base_url: String.raw`https://nas.example.com\shares`,
          },
        },
      })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'share_base_url',
              status: 'block',
              title: '分享基础 URL 路径包含反斜杠',
              message: 'backend raw host-relative backslash repair detail',
              details: {
                base_url: String.raw`https://nas.example.com\shares`,
                base_url_path: String.raw`\shares`,
              },
            },
          ],
          request: { scheme: 'https', host: 'nas.example.com' },
          config: { share_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('分享基础 URL 路径包含反斜杠')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '使用 HTTPS URL' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            enabled: true,
            base_url: 'https://nas.example.com/shares',
          }),
        }))
      })
      expect(screen.queryByText('backend raw host-relative backslash repair detail')).toBeNull()
    })

    it('repairs share base URLs with escaped backslashes', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            base_url: 'https://nas.example.com/shares%5Cteam',
          },
        },
      })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'share_base_url',
              status: 'block',
              title: '分享基础 URL 路径包含反斜杠',
              message: 'backend raw backslash repair detail',
              details: {
                base_url: 'https://nas.example.com/shares%5Cteam',
                base_url_path: '/shares\\team',
              },
            },
          ],
          request: { scheme: 'https', host: 'nas.example.com' },
          config: { share_enabled: true },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('分享基础 URL 路径包含反斜杠')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '使用 HTTPS URL' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            enabled: true,
            base_url: 'https://nas.example.com/shares/team',
          }),
        }))
      })
      expect(screen.queryByText('backend raw backslash repair detail')).toBeNull()
    })

    it('shows stable fallback text when the security check cannot load', async () => {
      mockGetSecurityCheck.mockRejectedValueOnce(new Error('security check failed'))

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('安全自检暂不可用')).toBeTruthy()
        expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeTruthy()
      })
    })

    it('applies public HTTP exposure recommendations to the settings form', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'public_http_exposure',
              status: 'block',
              title: '检测到公网 HTTP 直连风险',
              message: '请只开放 80/443 给反向代理。',
            },
          ],
          request: { scheme: 'http' },
          config: { server_host: '0.0.0.0' },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('检测到公网 HTTP 直连风险')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '应用代理推荐' }))

      expect(screen.getByLabelText('服务器监听地址')).toHaveValue('127.0.0.1')
      expect(screen.getByLabelText('受信代理层数')).toHaveValue(1)
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已应用反向代理推荐',
      }))
    })

    it('uses Basic Auth for the WebDAV auth repair when app auth is disabled', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          webdav: {
            ...defaultSettingsResponse.data.webdav,
            auth_type: 'none',
            username: '',
          },
        },
      })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'webdav_auth',
              status: 'block',
              title: 'WebDAV 暴露面缺少认证',
              message: 'WebDAV 已启用但认证方式不是 basic 或 users。',
            },
          ],
          request: { scheme: 'http' },
          config: { auth_enabled: false, webdav_enabled: true, webdav_auth_type: 'none' },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('WebDAV 暴露面缺少认证')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '启用认证' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          webdav: expect.objectContaining({
            auth_type: 'basic',
            username: 'admin',
            password: '',
          }),
        }))
      })
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已启用 WebDAV Basic 认证',
      }))
    })

    it('switches weak WebDAV Basic passwords back to generated credentials from security check', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'webdav_auth',
              status: 'warning',
              title: 'WebDAV Basic 密码需要更换',
              message: 'WebDAV 使用全局 Basic Auth 且配置了弱密码或示例密码。',
              details: {
                prefix: '/dav',
                auth_type: 'basic',
                password_risk: 'placeholder',
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true, webdav_enabled: true, webdav_auth_type: 'basic' },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('WebDAV Basic 密码需要更换')).toBeTruthy()
        expect(screen.getByText('WebDAV Basic Auth 使用弱密码或示例密码，公网访问前应更换为自动生成密码、自定义强密码，或改用 MnemoNAS 用户认证。')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '更换密码' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          webdav: expect.objectContaining({
            auth_type: 'basic',
            password: '',
          }),
        }))
      })
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已改用自动生成 WebDAV 密码',
      }))
    })

    it('offers user authentication when generated WebDAV credentials are unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'webdav_auth',
              status: 'block',
              title: '自动 WebDAV 密码不可用',
              message: 'WebDAV 使用自动生成 Basic Auth 密码，但运行态没有加载到可用密码。',
              details: {
                prefix: '/dav',
                auth_type: 'basic',
                password_source: 'generated',
                generated_password_available: false,
              },
            },
          ],
          request: { scheme: 'https' },
          config: { auth_enabled: true, webdav_enabled: true, webdav_auth_type: 'basic' },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('自动 WebDAV 密码不可用')).toBeTruthy()
        expect(screen.getByText('WebDAV 使用自动生成 Basic Auth，但运行态没有可用密码；请检查 secrets.json，设置自定义强密码，或改用 MnemoNAS 用户认证。')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '改用用户认证' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          webdav: expect.objectContaining({
            auth_type: 'users',
          }),
        }))
      })
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已启用 WebDAV 用户认证',
      }))
    })

    it('repairs unsafe WebDAV prefixes from security check findings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          webdav: {
            ...defaultSettingsResponse.data.webdav,
            prefix: '/api/v1',
          },
        },
      })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'webdav_prefix',
              status: 'block',
              title: 'WebDAV 前缀占用保留路由',
              message: 'WebDAV 前缀不能位于 /api、/s 或 /health 路由下。',
              details: {
                prefix: '/api/v1',
                normalized_prefix: '/api/v1',
                prefix_risk: 'reserved_route',
                recommended_prefix: '/dav',
              },
            },
          ],
          request: { scheme: 'https' },
          config: { webdav_enabled: true, webdav_prefix: '/api/v1', webdav_auth_type: 'basic' },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('WebDAV 前缀占用保留路由')).toBeTruthy()
        expect(screen.getByText('WebDAV 前缀占用了 /api、/s 或 /health 保留路由；请改为 /dav 或其他独立路径。')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '改为 /dav' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          webdav: expect.objectContaining({
            prefix: '/dav',
          }),
        }))
      })
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已改回 WebDAV 默认前缀',
      }))
    })

    it('uses the existing share base URL when repairing HTTPS share links', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            base_url: 'http://share.example.com',
          },
        },
      })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'share_base_url',
              status: 'warning',
              title: '分享基础 URL 未使用 HTTPS',
              message: '公开分享链接应使用 HTTPS 基础地址。',
              details: { base_url: 'http://share.example.com' },
            },
          ],
          request: { scheme: 'http' },
          config: { share_enabled: true },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('分享基础 URL 未使用 HTTPS')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '使用 HTTPS URL' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            enabled: true,
            base_url: 'https://share.example.com',
          }),
        }))
      })
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已更新分享基础 URL',
      }))
    })

    it('removes userinfo and non-default ports when repairing share base URLs', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            base_url: 'https://operator@share.example.com:8443/base/',
          },
        },
      })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'share_base_url',
              status: 'block',
              title: '分享基础 URL 使用非标准 HTTPS 端口',
              message: 'backend raw non-default port detail',
              details: { base_url: 'https://operator@share.example.com:8443/base/' },
            },
          ],
          request: { scheme: 'https' },
          config: { share_enabled: true },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('分享基础 URL 使用非标准 HTTPS 端口')).toBeTruthy()
        expect(screen.getByText('分享基础 URL 使用非标准 HTTPS 端口，公网分享通常应使用默认 443 端口，避免额外暴露入口。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw non-default port detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '使用 HTTPS URL' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            enabled: true,
            base_url: 'https://share.example.com/base/',
          }),
        }))
      })
    })

    it('removes query and fragment when repairing share base URLs', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            base_url: 'https://share.example.com/base/?token=secret#fragment',
          },
        },
      })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'share_base_url',
              status: 'block',
              title: '分享基础 URL 包含查询参数或片段',
              message: '公开分享链接的基础地址不应包含查询参数或片段。',
              details: { base_url: 'https://share.example.com/base/?token=secret#fragment' },
            },
          ],
          request: { scheme: 'https' },
          config: { share_enabled: true },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('分享基础 URL 包含查询参数或片段')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '使用 HTTPS URL' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            enabled: true,
            base_url: 'https://share.example.com/base/',
          }),
        }))
      })
    })

    it('preserves a custom dataplane gRPC port when repairing external listening', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          dataplane: {
            ...defaultSettingsResponse.data.dataplane,
            grpc_address: '0.0.0.0:19090',
          },
        },
      })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'dataplane_listen',
              status: 'block',
              title: '数据面 gRPC 不应暴露外网',
              message: '请将 dataplane.grpc_address 绑定到 127.0.0.1 或 ::1，并通过 Web 控制面访问文件能力。',
              details: { grpc_address: '0.0.0.0:19090' },
            },
          ],
          request: { scheme: 'http' },
          config: { dataplane_grpc_addr: '0.0.0.0:19090' },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('数据面 gRPC 不应暴露外网')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '改为本机地址' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          dataplane: expect.objectContaining({
            grpc_address: '127.0.0.1:19090',
          }),
        }))
      })
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已改为本机数据面地址',
      }))
    })

    it('adds a private forwarded proxy source instead of forcing loopback listening', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'forwarded_proto_trust',
              status: 'block',
              title: '转发 header 来源不受信任',
              message: '请求携带反向代理 header，但直连来源不是本机或显式配置的受信代理网段。',
              details: {
                remote_ip: '172.18.0.3',
                trusted_proxy_hops: 1,
                trusted_proxy_cidrs: [],
              },
            },
          ],
          request: { scheme: 'http', remote_ip: '172.18.0.3' },
          config: { server_host: '0.0.0.0', trusted_proxy_hops: 1, trusted_proxy_cidrs: [] },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('转发 header 来源不受信任')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '修正代理设置' }))

        expect(screen.getByLabelText('服务器监听地址')).toHaveValue('0.0.0.0')
        expect(screen.getByLabelText('受信代理层数')).toHaveValue(1)
      expect(screen.getByLabelText('受信代理来源')).toHaveValue('10.0.0.0/8\n172.18.0.3')
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已加入受信代理来源',
      }))
    })

    it('shows manual guidance for trusted proxies forwarding http proto', async () => {
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'forwarded_proto_trust',
              status: 'warning',
              title: '反向代理未声明 HTTPS',
              message: '当前请求来自受信代理来源，但 X-Forwarded-Proto 不是 https。',
              details: {
                remote_ip: '127.0.0.1',
                trusted_proxy_hops: 1,
                forwarded_proto: 'http',
                trusted_forwarded_source: true,
              },
            },
          ],
          request: { scheme: 'http', remote_ip: '127.0.0.1' },
          config: { server_host: '127.0.0.1', trusted_proxy_hops: 1 },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('反向代理未声明 HTTPS')).toBeTruthy()
      })

      expect(screen.getByText('受信代理已转发协议头，但当前值不是 https，请检查反向代理的 X-Forwarded-Proto 配置。')).toBeTruthy()
      expect(screen.queryByRole('button', { name: '修正代理设置' })).toBeNull()
    })

    it('sets trusted proxy hops without forcing loopback listening', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'trusted_proxy_or_tls',
              status: 'warning',
              title: '未配置 HTTPS 信任边界',
              message: '如果通过反向代理发布公网，请将受信代理层数设为实际代理层数。',
            },
          ],
          request: { scheme: 'http' },
          config: { server_host: '0.0.0.0', trusted_proxy_hops: 0 },
        },
      })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          server: {
            ...defaultSettingsResponse.data.server,
            trusted_proxy_hops: 0,
            trusted_proxy_cidrs: [],
          },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('未配置 HTTPS 信任边界')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '设置代理层数' }))

        expect(screen.getByLabelText('服务器监听地址')).toHaveValue('0.0.0.0')
        expect(screen.getByLabelText('受信代理层数')).toHaveValue(1)
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已设置受信代理层数',
      }))
    })

    it('shows manual guidance for unsafe no-auth overrides', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'unsafe_no_auth_override',
              status: 'block',
              title: '无认证暴露例外已开启',
              message: '公网访问前必须修复。',
            },
          ],
          request: { scheme: 'http' },
          config: { allow_unsafe_no_auth: true },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('无认证暴露例外已开启')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '关闭例外' }))

      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '需要编辑配置文件',
        description: expect.stringContaining('allow_unsafe_no_auth'),
      }))
    })

    it('does not imply non-loopback exposure for unsafe no-auth warning states', async () => {
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'unsafe_no_auth_override',
              status: 'warning',
              title: '无认证暴露例外已开启',
              message: 'backend warning should be normalized',
            },
          ],
          request: { scheme: 'https' },
          config: { allow_unsafe_no_auth: true },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('无认证暴露例外已开启')).toBeTruthy()
      })

      expect(screen.getByText('无认证例外已开启；该设置只适合受控网络边界或临时调试，公网访问前请关闭该例外。')).toBeTruthy()
      expect(screen.queryByText(/绑定到非本机地址/)).toBeNull()
      expect(screen.getByRole('button', { name: '关闭例外' })).toBeTruthy()
    })

    it('shows manual guidance when Web login auth is disabled', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'auth_enabled',
              status: 'block',
              title: 'Web 登录认证未启用',
              message: 'backend raw auth disabled detail',
            },
          ],
          request: { scheme: 'http' },
          config: { auth_enabled: false },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('Web 登录认证未启用')).toBeTruthy()
        expect(screen.getByText('管理界面未启用登录认证，公网访问前必须启用认证。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw auth disabled detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '启用认证' }))

      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '需要启用 Web 登录认证',
        description: expect.stringContaining('[auth].enabled = true'),
      }))
    })

    it('preserves the dataplane HTTP port in manual repair guidance', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'block',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'dataplane_http_listen',
              status: 'block',
              title: '数据面 HTTP 不应暴露外网',
              message: '请将 DATAPLANE_HTTP_ADDR 绑定到 127.0.0.1:9091 或 [::1]:9091，并通过 Web 控制面查看健康状态。',
              details: { http_address: '0.0.0.0:19091' },
            },
          ],
          request: { scheme: 'http' },
          config: { dataplane_http_addr: '0.0.0.0:19091' },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('数据面 HTTP 不应暴露外网')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '查看环境变量' }))

      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '需要调整启动环境',
        description: expect.stringContaining('127.0.0.1:19091'),
      }))
    })

    it('opens user management for administrator-account warnings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSecurityCheck.mockResolvedValueOnce({
        success: true,
        data: {
          status: 'warning',
          generated_at: '2026-05-08T00:00:00Z',
          checks: [
            {
              id: 'admin_accounts',
              status: 'warning',
              title: '只有一个启用中的管理员',
              message: 'backend raw admin account detail',
            },
          ],
          request: { scheme: 'https' },
          config: { active_admins: 1 },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('只有一个启用中的管理员')).toBeTruthy()
        expect(screen.getByText('建议至少保留两个启用中的管理员账号，避免主账号失效后无法管理。')).toBeTruthy()
      })
      expect(screen.queryByText('backend raw admin account detail')).toBeNull()

      await user.click(screen.getByRole('button', { name: '管理用户' }))

      await waitFor(() => {
        expect(window.location.pathname).toBe('/users')
      })
    })

    it('switches to retention tab', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await waitFor(() => {
        expect(screen.getByText('版本策略')).toBeTruthy()
        expect(screen.getByText('最大版本数')).toBeTruthy()
      })
    })

    it('switches to WebDAV tab', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByText('WebDAV 服务')).toBeTruthy()
        expect(screen.getByRole('switch', { name: '启用 WebDAV' })).toBeTruthy()
      })
    })

    it('switches to advanced tab', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('CDC 分块参数')).toBeTruthy()
        expect(screen.getByText('数据面连接')).toBeTruthy()
      })
    })

    it('clears the tab query string when switching back to general settings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      window.history.pushState({}, '', '/settings?tab=webdav')
      render(<SettingsPage />)

      await openTab(user, '常规')

      await waitFor(() => {
        expect(screen.getByText('服务器')).toBeTruthy()
        expect(window.location.search).toBe('')
      })
    })

    it('uses numeric input constraints for server port and retry settings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      const portInput = await screen.findByLabelText('服务器端口')
      expect(portInput).toHaveAttribute('type', 'number')
      expect(portInput).toHaveAttribute('min', '1')
      expect(portInput).toHaveAttribute('max', '65535')

      await openTab(user, '高级')

      const maxRetriesInput = await screen.findByLabelText('数据面最大重试次数')
      expect(maxRetriesInput).toHaveAttribute('type', 'number')
      expect(maxRetriesInput).toHaveAttribute('min', '0')
    })

    it('uses url inputs for alert webhook and share base addresses', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      const alertsWebhookInput = await screen.findByLabelText('Webhook URL')
      expect(alertsWebhookInput).toHaveAttribute('type', 'url')

      const wecomWebhookInput = await screen.findByLabelText('企业微信 Webhook URL')
      expect(wecomWebhookInput).toHaveAttribute('type', 'url')

      const dingTalkWebhookInput = await screen.findByLabelText('钉钉 Webhook URL')
      expect(dingTalkWebhookInput).toHaveAttribute('type', 'url')

      await openTab(user, '分享')

      const shareBaseUrlInput = await screen.findByLabelText('分享基础 URL')
      expect(shareBaseUrlInput).toHaveAttribute('type', 'url')
    })

    it('shows example duration placeholders for retention and alert timing settings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      expect(await screen.findByLabelText('最大保留时间')).toHaveAttribute('placeholder', '2160h')
      expect(screen.getByLabelText('GC 运行间隔')).toHaveAttribute('placeholder', '24h')

      await openTab(user, '高级')

      expect(await screen.findByLabelText('数据面连接超时')).toHaveAttribute('placeholder', '30s')
      expect(screen.getByLabelText('提醒检查间隔')).toHaveAttribute('placeholder', '1h')
      expect(screen.getByLabelText('提醒冷却时间')).toHaveAttribute('placeholder', '4h')
    })

    it('opens the tab selected in the query string on first render', async () => {
      window.history.pushState({}, '', '/settings?tab=advanced')
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('CDC 分块参数')).toBeTruthy()
        expect(screen.getByText('提醒通知')).toBeTruthy()
      })
    })
  })

  describe('webdav settings', () => {
    it('describes WebDAV changes as taking effect immediately', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByText('配置 WebDAV 协议接入；保存后会立即更新运行中的 WebDAV 配置')).toBeTruthy()
        expect(screen.getByText('配置访问凭据；保存后会立即作用到运行中的 WebDAV 服务')).toBeTruthy()
        expect(screen.getByText('用于挂载当前运行中的 WebDAV 服务；保存成功后这里会显示最新的运行配置')).toBeTruthy()
      })
    })

    it('warns when WebDAV is enabled but the runtime service is unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          webdav: {
            ...defaultSettingsResponse.data.webdav,
            enabled: true,
            runtime_enabled: false,
          },
        },
      })

      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByText('WebDAV 运行态当前不可用')).toBeTruthy()
        expect(screen.getByText('配置已启用，但运行中的 WebDAV 服务未成功启动；请检查自动生成凭据和内部存储状态。')).toBeTruthy()
      })
    })

    it('allows changing WebDAV auth type and saves it', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByLabelText('WebDAV 认证方式')).toBeTruthy()
      })

      await user.selectOptions(screen.getByLabelText('WebDAV 认证方式'), 'none')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          webdav: expect.objectContaining({
            auth_type: 'none',
          }),
        }))
      })
    })

    it('allows editing WebDAV service fields and saves them', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByLabelText('WebDAV 认证方式')).toBeTruthy()
      })

      const switches = screen.getAllByRole('switch')
      await user.click(switches[0])
      await user.click(switches[0])
      fireEvent.change(screen.getByLabelText('WebDAV URL 前缀'), { target: { value: 'remote' } })
      await user.click(switches[1])
      fireEvent.change(screen.getByLabelText('WebDAV 用户名'), { target: { value: 'webdav-admin' } })
      fireEvent.change(screen.getByLabelText('WebDAV 密码'), { target: { value: 'new-secret' } })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          webdav: expect.objectContaining({
            enabled: true,
            prefix: '/remote',
            read_only: true,
            username: 'webdav-admin',
            password: 'new-secret',
          }),
        }))
      })
    })

    it('normalizes WebDAV prefixes with path-segment spaces like the server', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByLabelText('WebDAV URL 前缀')).toHaveValue('/dav')
      })

      fireEvent.change(screen.getByLabelText('WebDAV URL 前缀'), { target: { value: '/team /sub ' } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          webdav: expect.objectContaining({
            enabled: true,
            prefix: '/team /sub',
          }),
        }))
      })
    })

    it('clears newly saved WebDAV password while waiting for refreshed settings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings
        .mockResolvedValueOnce(defaultSettingsResponse)
        .mockImplementationOnce(() => new Promise(() => {}))
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      const passwordInput = await screen.findByLabelText('WebDAV 密码')
      fireEvent.change(passwordInput, { target: { value: 'new-webdav-secret' } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          webdav: expect.objectContaining({
            auth_type: 'basic',
            password: 'new-webdav-secret',
          }),
        }))
      })
      await waitFor(() => {
        expect(screen.getByLabelText('WebDAV 密码')).toHaveValue('')
      })
    })

    it('can switch Basic Auth WebDAV back to the generated password', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByLabelText('保存时使用自动生成密码')).toBeTruthy()
      })

      await user.click(screen.getByLabelText('保存时使用自动生成密码'))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          webdav: expect.objectContaining({
            auth_type: 'basic',
            password: '',
          }),
        }))
      })
    })

    it('warns before saving WebDAV without authentication on a non-loopback listener', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByLabelText('WebDAV 认证方式')).toBeTruthy()
      })

      await user.selectOptions(screen.getByLabelText('WebDAV 认证方式'), 'none')

      expect(screen.getByText('WebDAV 当前将无认证开放')).toBeTruthy()
      expect(screen.getByText(/任何能访问该端口的人都可以读写 WebDAV/)).toBeTruthy()
    })
  })

  describe('dataplane settings', () => {
    it('explains CDC and dataplane connection effect timing', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('配置 dataplane 文件分块 API；保存后需重启数据面服务')).toBeTruthy()
        expect(screen.getByText('配置与 Rust 数据面的 gRPC 连接；地址变更会立即校验并切换，超时与重试设置用于后续连接建立')).toBeTruthy()
      })
    })

    it('allows editing dataplane connection settings and saves them', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      const grpcInput = await screen.findByLabelText('数据面 gRPC 地址')
      fireEvent.change(grpcInput, { target: { value: '10.0.0.2:9091' } })

      const timeoutInput = screen.getByLabelText('数据面连接超时')
      fireEvent.change(timeoutInput, { target: { value: '45s' } })

      const retriesInput = screen.getByLabelText('数据面最大重试次数')
      fireEvent.change(retriesInput, { target: { value: '5' } })
      fireEvent.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          dataplane: expect.objectContaining({
            grpc_address: '10.0.0.2:9091',
            timeout: '45s',
            max_retries: 5,
          }),
        }))
      })
    })
  })

  describe('scrub schedule settings', () => {
    it('renders scheduled scrub controls', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('数据巡检计划')).toBeTruthy()
        expect(screen.getByRole('switch', { name: '启用周期 Scrub' })).toBeTruthy()
        expect(screen.getByLabelText('Scrub 常规间隔')).toHaveValue('168h')
      })
    })

    it('allows editing scheduled scrub settings and saves them', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByRole('switch', { name: '启用周期 Scrub' })).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用周期 Scrub' }))
      const scheduleInput = screen.getByLabelText('Scrub 常规间隔')
      await user.clear(scheduleInput)
      await user.type(scheduleInput, '12h')
      const retryInput = screen.getByLabelText('Scrub 失败重试间隔')
      await user.clear(retryInput)
      await user.type(retryInput, '30m')
      const maxRetriesInput = screen.getByLabelText('Scrub 最大重试次数')
      await user.clear(maxRetriesInput)
      await user.type(maxRetriesInput, '2')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          maintenance: {
            scrub: {
              enabled: true,
              schedule_interval: '12h',
              retry_interval: '30m',
              max_retries: 2,
            },
          },
        }))
      })
    })

    it('rejects invalid scheduled scrub retry count before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByLabelText('Scrub 最大重试次数')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用周期 Scrub' }))
      const maxRetriesInput = screen.getByLabelText('Scrub 最大重试次数')
      await user.clear(maxRetriesInput)
      await user.type(maxRetriesInput, '-1')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'Scrub 重试次数格式无效',
          description: '最大重试次数必须是 0 或不超过安全范围的整数',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })
  })

  describe('share settings', () => {
    it('allows editing share configuration and saves it', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '分享')

      await waitFor(() => {
        expect(screen.getByText('分享功能配置')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch'))
      const baseUrlInput = screen.getByLabelText('分享基础 URL')
      await user.type(baseUrlInput, 'https://share.example.com')
      await user.clear(screen.getByLabelText('新分享默认有效期'))
      await user.type(screen.getByLabelText('新分享默认有效期'), '24h')
      await user.clear(screen.getByLabelText('新分享默认访问次数'))
      await user.type(screen.getByLabelText('新分享默认访问次数'), '3')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            enabled: true,
            base_url: 'https://share.example.com',
            default_expires_in: '24h',
            default_max_access: 3,
            policy_rules: [],
          }),
        }))
      })
    })

    it('allows editing share path policy rules and saves them', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '分享')

      await user.click(screen.getByRole('switch'))
      await user.click(screen.getByRole('button', { name: '添加路径策略' }))
      await user.clear(screen.getByLabelText('分享策略路径 1'))
      await user.type(screen.getByLabelText('分享策略路径 1'), '/Family')
      await user.type(screen.getByLabelText('分享策略最长有效期 1'), '24h')
      await user.type(screen.getByLabelText('分享策略最多访问次数 1'), '20')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            policy_rules: [{
              path: '/Family',
              require_password: true,
              max_expires_in: '24h',
              max_access: 20,
            }],
          }),
        }))
      })
    })

    it('normalizes duplicate and trailing slashes in share policy paths before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '分享')

      await user.click(screen.getByRole('switch'))
      await user.click(screen.getByRole('button', { name: '添加路径策略' }))
      await user.clear(screen.getByLabelText('分享策略路径 1'))
      await user.type(screen.getByLabelText('分享策略路径 1'), '/Family//Photos/')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            policy_rules: [{
              path: '/Family/Photos',
              require_password: true,
            }],
          }),
        }))
      })
    })

    it('allows share policy paths with spaces', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '分享')

      await user.click(screen.getByRole('switch'))
      await user.click(screen.getByRole('button', { name: '添加路径策略' }))
      expect(screen.getByText(/分享策略路径填写 MnemoNAS 逻辑路径/)).toBeTruthy()
      await user.clear(screen.getByLabelText('分享策略路径 1'))
      fireEvent.change(screen.getByLabelText('分享策略路径 1'), { target: { value: '/Family Photos' } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            policy_rules: [{
              path: '/Family Photos',
              require_password: true,
            }],
          }),
        }))
      })
    })

    it('summarizes unsaved share defaults and path policy changes', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          share: {
            ...defaultSettingsResponse.data.share,
            enabled: true,
            base_url: 'https://old.example.com',
            default_expires_in: '168h',
            default_max_access: 0,
            policy_rules: [
              { path: '/Family', require_password: true, max_expires_in: '24h' },
              { path: '/Archive', require_password: true, max_access: 10 },
            ],
          },
        },
      })
      render(<SettingsPage />)

      await openTab(user, '分享')

      const review = await screen.findByLabelText('分享策略变更复核')
      expect(within(review).getByText('分享策略与已保存配置一致。')).toBeTruthy()
      const initialCoverage = within(screen.getByLabelText('分享策略覆盖摘要'))
      expect(initialCoverage.getByText('分享策略覆盖摘要')).toBeTruthy()
      expect(initialCoverage.getByText('关注项 3')).toBeTruthy()
      expect(initialCoverage.getByText('默认访问次数')).toBeTruthy()
      expect(initialCoverage.getByText('不限制')).toBeTruthy()
      expect(initialCoverage.getByText('路径策略')).toBeTruthy()
      expect(initialCoverage.getAllByText('2 条').length).toBeGreaterThanOrEqual(1)
      expect(initialCoverage.getByText('1 条路径策略未限制最长有效期。')).toBeTruthy()
      expect(initialCoverage.getByText('1 条路径策略未限制访问次数。')).toBeTruthy()

      fireEvent.change(screen.getByLabelText('分享基础 URL'), {
        target: { value: 'https://new.example.com' },
      })
      fireEvent.change(screen.getByLabelText('新分享默认访问次数'), { target: { value: '30' } })
      fireEvent.change(screen.getByLabelText('分享策略最长有效期 1'), { target: { value: '12h' } })
      await user.click(screen.getByLabelText('删除分享策略 2'))
      await user.click(screen.getByRole('button', { name: '添加路径策略' }))
      fireEvent.change(screen.getByLabelText('分享策略路径 2'), { target: { value: '/Public' } })
      fireEvent.change(screen.getByLabelText('分享策略最多访问次数 2'), { target: { value: '5' } })

      const updatedReview = screen.getByLabelText('分享策略变更复核')
      expect(within(updatedReview).getByText('默认项 2')).toBeTruthy()
      expect(within(updatedReview).getByText('新增 1')).toBeTruthy()
      expect(within(updatedReview).getByText('修改 1')).toBeTruthy()
      expect(within(updatedReview).getByText('删除 1')).toBeTruthy()
      expect(within(updatedReview).getByText('https://old.example.com -> https://new.example.com')).toBeTruthy()
      expect(within(updatedReview).getByText('不限制 -> 30')).toBeTruthy()
      expect(within(updatedReview).getByText('/Family')).toBeTruthy()
      expect(within(updatedReview).getByText('/Public')).toBeTruthy()
      expect(within(updatedReview).getByText('/Archive')).toBeTruthy()
      expect(within(updatedReview).getByText('变更字段：最长有效期')).toBeTruthy()
      expect(within(updatedReview).getByText('必须设置密码 · 最长有效期：12h')).toBeTruthy()
      expect(within(updatedReview).getByText('必须设置密码 · 最多访问：5')).toBeTruthy()
      expect(within(updatedReview).getByText('必须设置密码 · 最多访问：10')).toBeTruthy()
      const updatedCoverage = within(screen.getByLabelText('分享策略覆盖摘要'))
      expect(updatedCoverage.getByText('关注项 2')).toBeTruthy()
      expect(updatedCoverage.getByText('默认访问次数')).toBeTruthy()
      expect(updatedCoverage.getByText('30')).toBeTruthy()
      expect(updatedCoverage.getByText('强制密码路径')).toBeTruthy()
      expect(updatedCoverage.getAllByText('2 条').length).toBeGreaterThanOrEqual(1)
      expect(updatedCoverage.getByText('完整限制路径')).toBeTruthy()
      expect(updatedCoverage.getByText('0 条')).toBeTruthy()
    })

    it('rejects invalid share default policy values before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '分享')

      await user.click(screen.getByRole('switch'))
      await user.clear(screen.getByLabelText('新分享默认有效期'))
      await user.type(screen.getByLabelText('新分享默认有效期'), '7d')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '分享默认有效期无效',
          description: '默认有效期必须为空、0，或使用 168h / 30m 这类 Go duration 格式',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()

      await user.clear(screen.getByLabelText('新分享默认有效期'))
      await user.type(screen.getByLabelText('新分享默认有效期'), '24h')
      await user.clear(screen.getByLabelText('新分享默认访问次数'))
      await user.type(screen.getByLabelText('新分享默认访问次数'), '-1')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '分享默认访问次数无效',
          description: '默认访问次数必须是 0 或不超过安全范围的正整数',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it.each([
      ['科学计数法', '1e3'],
      ['超过安全整数范围', '9007199254740992'],
    ])('rejects invalid share default max access with %s before saving', async (_label, maxAccess) => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '分享')

      await user.click(screen.getByRole('switch'))
      await user.clear(screen.getByLabelText('新分享默认访问次数'))
      await user.type(screen.getByLabelText('新分享默认访问次数'), maxAccess)
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '分享默认访问次数无效',
          description: '默认访问次数必须是 0 或不超过安全范围的正整数',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('rejects invalid share path policy rules before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '分享')

      await user.click(screen.getByRole('switch'))
      await user.click(screen.getByRole('button', { name: '添加路径策略' }))
      await user.clear(screen.getByLabelText('分享策略路径 1'))
      await user.type(screen.getByLabelText('分享策略路径 1'), '/Family')
      await user.click(screen.getByLabelText('分享策略必须设置密码 1'))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(screen.getByText('分享策略覆盖摘要暂不可用：第 1 行至少需要一个约束')).toBeTruthy()
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '分享路径策略格式无效',
          description: '第 1 行至少需要一个约束',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it.each([
      ['dot segment', '/Family/./Photos'],
      ['backslash', '/Family\\Photos'],
      ['query marker', '/Family?Photos'],
      ['fragment marker', '/Family#Photos'],
    ])('rejects invalid share path policy paths with %s before saving', async (_label, invalidPath) => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '分享')

      await user.click(screen.getByRole('switch'))
      await user.click(screen.getByRole('button', { name: '添加路径策略' }))
      await user.clear(screen.getByLabelText('分享策略路径 1'))
      fireEvent.change(screen.getByLabelText('分享策略路径 1'), { target: { value: invalidPath } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '分享路径策略格式无效',
          description: '第 1 行路径无效',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it.each(['0', '0s'])('rejects %s-only share path policy rules before saving', async (maxExpiresIn) => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '分享')

      await user.click(screen.getByRole('switch'))
      await user.click(screen.getByRole('button', { name: '添加路径策略' }))
      await user.clear(screen.getByLabelText('分享策略路径 1'))
      await user.type(screen.getByLabelText('分享策略路径 1'), '/Family')
      await user.click(screen.getByLabelText('分享策略必须设置密码 1'))
      await user.type(screen.getByLabelText('分享策略最长有效期 1'), maxExpiresIn)
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '分享路径策略格式无效',
          description: '第 1 行至少需要一个约束',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it.each([
      ['科学计数法', '1e3'],
      ['超过安全整数范围', '9007199254740992'],
    ])('rejects invalid share path policy access limits with %s before saving', async (_label, maxAccess) => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '分享')

      await user.click(screen.getByRole('switch'))
      await user.click(screen.getByRole('button', { name: '添加路径策略' }))
      await user.clear(screen.getByLabelText('分享策略路径 1'))
      await user.type(screen.getByLabelText('分享策略路径 1'), '/Family')
      await user.type(screen.getByLabelText('分享策略最多访问次数 1'), maxAccess)
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '分享路径策略格式无效',
          description: '第 1 行访问次数上限必须是 0 或不超过安全范围的正整数',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('applies share policy presets before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '分享')

      await user.click(screen.getByRole('switch'))
      await user.click(screen.getByRole('button', { name: /临时协作/ }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            default_expires_in: '72h',
            default_max_access: 20,
          }),
        }))
      })
    })
  })

  describe('tls settings', () => {
    it('allows editing TLS configuration and saves it', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('TLS / HTTPS')).toBeTruthy()
      })

      const switches = screen.getAllByRole('switch')
      await user.click(switches[0])
      await user.click(switches[1])
      await user.click(switches[1])

      const certInput = screen.getByLabelText('TLS 证书文件')
      fireEvent.change(certInput, { target: { value: '/etc/mnemonas/tls/server.crt' } })

      const keyInput = screen.getByLabelText('TLS 私钥文件')
      fireEvent.change(keyInput, { target: { value: '/etc/mnemonas/tls/server.key' } })

      const certDirInput = screen.getByLabelText('TLS 证书目录')
      fireEvent.change(certDirInput, { target: { value: '/etc/mnemonas/tls' } })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          server: expect.objectContaining({
            tls: expect.objectContaining({
              enabled: true,
              auto_generate: true,
              cert_file: '/etc/mnemonas/tls/server.crt',
              key_file: '/etc/mnemonas/tls/server.key',
              cert_dir: '/etc/mnemonas/tls',
            }),
          }),
        }))
      })
    })

    it('shows danger toast and skips save when TLS certificate pair is incomplete', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('TLS / HTTPS')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用 HTTPS' }))
      fireEvent.change(screen.getByLabelText('TLS 证书文件'), { target: { value: '/etc/mnemonas/tls/server.crt' } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'TLS 证书配置无效',
          description: '证书文件和私钥文件必须同时设置或同时留空',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('shows danger toast and skips save when TLS certificate and key paths match', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('TLS / HTTPS')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用 HTTPS' }))
      fireEvent.change(screen.getByLabelText('TLS 证书文件'), { target: { value: '/etc/mnemonas/tls/server.pem' } })
      fireEvent.change(screen.getByLabelText('TLS 私钥文件'), { target: { value: '/etc/mnemonas/tls/server.pem' } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'TLS 证书配置无效',
          description: '证书文件和私钥文件必须指向不同文件',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('shows danger toast and skips save when TLS has no certificate source without auto-generation', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('TLS / HTTPS')).toBeTruthy()
      })

      const switches = screen.getAllByRole('switch')
      await user.click(switches[0])
      await user.click(switches[1])
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'TLS 证书配置无效',
          description: '禁用自动生成时必须配置证书目录或证书文件对',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })
  })

  describe('alerts settings', () => {
    it('allows editing alerts configuration and saves it', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('提醒通知')).toBeTruthy()
      })

      fireEvent.click(screen.getByRole('switch', { name: '启用提醒' }))

      const checkIntervalInput = screen.getByLabelText('提醒检查间隔')
      fireEvent.change(checkIntervalInput, { target: { value: '30m' } })

      const thresholdInput = screen.getByLabelText('提醒阈值')
      fireEvent.change(thresholdInput, { target: { value: '85' } })

      const criticalInput = screen.getByLabelText('严重提醒阈值')
      fireEvent.change(criticalInput, { target: { value: '92' } })

      const minFreeInput = screen.getByLabelText('最小剩余空间')
      fireEvent.change(minFreeInput, { target: { value: '20GB' } })

      const cooldownInput = screen.getByLabelText('提醒冷却时间')
      fireEvent.change(cooldownInput, { target: { value: '2h' } })

      const webhookInput = screen.getByLabelText('Webhook URL')
      fireEvent.change(webhookInput, { target: { value: 'https://hooks.example.com/storage' } })

      fireEvent.change(screen.getByLabelText('Webhook 方法'), { target: { value: 'GET' } })

      const headersInput = screen.getByLabelText('Webhook 自定义 Header')
      fireEvent.change(headersInput, { target: { value: 'Authorization: Bearer token\nX-MnemoNAS: alerts' } })

      fireEvent.click(screen.getByRole('switch', { name: '启用 Telegram 通知' }))
      fireEvent.change(screen.getByLabelText('Telegram Bot Token'), { target: { value: '123456:secret-token' } })
      fireEvent.change(screen.getByLabelText('Telegram Chat ID'), { target: { value: '-1001234567890' } })

      fireEvent.click(screen.getByRole('switch', { name: '启用企业微信通知' }))
      fireEvent.change(screen.getByLabelText('企业微信 Webhook URL'), {
        target: { value: 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret-key' },
      })

      fireEvent.click(screen.getByRole('switch', { name: '启用钉钉通知' }))
      fireEvent.change(screen.getByLabelText('钉钉 Webhook URL'), {
        target: { value: 'https://oapi.dingtalk.com/robot/send?access_token=secret-token' },
      })

      fireEvent.click(screen.getByRole('switch', { name: '启用邮件通知' }))
      fireEvent.change(screen.getByLabelText('SMTP 主机'), { target: { value: 'smtp.example.com' } })
      fireEvent.change(screen.getByLabelText('SMTP 端口'), { target: { value: '2525' } })
      fireEvent.change(screen.getByLabelText('SMTP 用户名'), { target: { value: 'alerts' } })
      fireEvent.change(screen.getByLabelText('SMTP 密码'), { target: { value: 'smtp-secret' } })
      fireEvent.change(screen.getByLabelText('SMTP 发件人'), { target: { value: 'MnemoNAS <alerts@example.com>' } })
      fireEvent.change(screen.getByLabelText('SMTP 收件人'), { target: { value: 'admin@example.com\nops@example.com' } })

      fireEvent.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          alerts: expect.objectContaining({
            enabled: true,
            check_interval: '30m',
            threshold_pct: 85,
            critical_pct: 92,
            min_free_bytes: 21474836480,
            cooldown_period: '2h',
            webhook_url: 'https://hooks.example.com/storage',
            webhook_method: 'GET',
            webhook_headers: ['Authorization: Bearer token', 'X-MnemoNAS: alerts'],
            telegram_enabled: true,
            telegram_bot_token: '123456:secret-token',
            telegram_chat_id: '-1001234567890',
            wecom_enabled: true,
            wecom_webhook_url: 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret-key',
            dingtalk_enabled: true,
            dingtalk_webhook_url: 'https://oapi.dingtalk.com/robot/send?access_token=secret-token',
            email_enabled: true,
            smtp_host: 'smtp.example.com',
            smtp_port: 2525,
            smtp_username: 'alerts',
            smtp_password: 'smtp-secret',
            smtp_from: 'MnemoNAS <alerts@example.com>',
            smtp_to: ['admin@example.com', 'ops@example.com'],
          }),
        }))
      })
    })

    it('sends a saved test alert from the alerts section', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          alerts: {
            ...defaultSettingsResponse.data.alerts,
            enabled: true,
            webhook_url: '<redacted>',
            webhook_url_configured: true,
          },
        },
      })
      mockSendTestAlert.mockResolvedValueOnce({
        success: true,
        message: 'test alert sent',
        data: { event_type: 'alert_test', channels: ['webhook', 'telegram', 'wecom', 'dingtalk', 'email'] },
      })
      render(<SettingsPage />)

      await openTab(user, '高级')
      await user.click(await screen.findByRole('button', { name: '发送测试提醒' }))

      await waitFor(() => {
        expect(mockSendTestAlert).toHaveBeenCalledWith(expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
      })
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '测试提醒已发送',
        description: '已发送到 Webhook / Telegram / 企业微信 / 钉钉 / SMTP 邮件',
        color: 'success',
      }))
    })

    it('does not expose raw unknown alert channel keys in the test alert toast', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          alerts: {
            ...defaultSettingsResponse.data.alerts,
            enabled: true,
            webhook_url: '<redacted>',
            webhook_url_configured: true,
          },
        },
      })
      mockSendTestAlert.mockResolvedValueOnce({
        success: true,
        data: { event_type: 'alert_test', channels: ['webhook', 'backend_custom_channel'] },
      })
      render(<SettingsPage />)

      await openTab(user, '高级')
      await user.click(await screen.findByRole('button', { name: '发送测试提醒' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '测试提醒已发送',
          description: '已发送到 Webhook / 未知通道',
          color: 'success',
        }))
      })
      expect(mockAddToast).not.toHaveBeenCalledWith(expect.objectContaining({
        description: expect.stringContaining('backend_custom_channel'),
      }))
    })

    it('shows a warning toast when a saved test alert succeeds with warnings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          alerts: {
            ...defaultSettingsResponse.data.alerts,
            enabled: true,
            webhook_url: '<redacted>',
            webhook_url_configured: true,
          },
        },
      })
      mockSendTestAlert.mockResolvedValueOnce({
        success: true,
        warning: true,
        message: 'test alert sent with delivery warning token=webhook-secret Authorization: Bearer bearer-secret',
        data: { event_type: 'alert_test', channels: ['webhook'] },
      })
      render(<SettingsPage />)

      await openTab(user, '高级')
      await user.click(await screen.findByRole('button', { name: '发送测试提醒' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '测试提醒已发送，但存在警告',
          description: 'test alert sent with delivery warning token=<redacted> Authorization: Bearer <redacted>',
          color: 'warning',
        }))
      })
      expect(mockAddToast).not.toHaveBeenCalledWith(expect.objectContaining({
        description: expect.stringMatching(/webhook-secret|bearer-secret/),
      }))
    })

    it('asks to enable alerts before sending a test alert', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')
      await user.click(await screen.findByRole('button', { name: '发送测试提醒' }))

      expect(mockSendTestAlert).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '提醒尚未启用',
        description: '测试提醒会使用服务端已保存配置；请先启用提醒并保存。',
        color: 'warning',
      }))
    })

    it('asks to configure a channel before sending a test alert', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          alerts: {
            ...defaultSettingsResponse.data.alerts,
            enabled: true,
          },
        },
      })
      render(<SettingsPage />)

      await openTab(user, '高级')
      await user.click(await screen.findByRole('button', { name: '发送测试提醒' }))

      expect(mockSendTestAlert).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '没有可用提醒通道',
        description: '请至少配置 Webhook、Telegram、企业微信、钉钉或邮件通道并保存后再发送测试提醒。',
        color: 'warning',
      }))
    })

    it('does not treat blank saved SMTP recipients as a test alert channel', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          alerts: {
            ...defaultSettingsResponse.data.alerts,
            enabled: true,
            email_enabled: true,
            smtp_host: 'smtp.example.com',
            smtp_port: 587,
            smtp_from: 'MnemoNAS <alerts@example.com>',
            smtp_to: [' ', '\t'],
          },
        },
      })
      render(<SettingsPage />)

      await openTab(user, '高级')
      await user.click(await screen.findByRole('button', { name: '发送测试提醒' }))

      expect(mockSendTestAlert).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '没有可用提醒通道',
        description: '请至少配置 Webhook、Telegram、企业微信、钉钉或邮件通道并保存后再发送测试提醒。',
        color: 'warning',
      }))
    })

    it('asks to save before sending a test alert with dirty alert settings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')
      fireEvent.click(screen.getByRole('switch', { name: '启用提醒' }))
      await user.click(screen.getByRole('button', { name: '发送测试提醒' }))

      expect(mockSendTestAlert).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '需要先保存设置',
        color: 'warning',
      }))
    })

    it('preserves redacted webhook values when saving unchanged settings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          alerts: {
            ...defaultSettingsResponse.data.alerts,
            enabled: true,
            webhook_url: '<redacted>',
            webhook_url_configured: true,
            webhook_headers: ['Authorization: <redacted>'],
            webhook_headers_configured: true,
            wecom_enabled: true,
            wecom_webhook_url: '<redacted>',
            wecom_webhook_url_configured: true,
            dingtalk_enabled: true,
            dingtalk_webhook_url: '<redacted>',
            dingtalk_webhook_url_configured: true,
          },
        },
      })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByLabelText('Webhook URL')).toHaveValue('<redacted>')
        expect(screen.getByLabelText('企业微信 Webhook URL')).toHaveValue('<redacted>')
        expect(screen.getByLabelText('钉钉 Webhook URL')).toHaveValue('<redacted>')
      })
      expect(screen.getByText('企业微信群机器人 Webhook 地址；保存后不会回显完整地址')).toBeTruthy()
      expect(screen.getByText('钉钉群机器人 Webhook 地址；保存后不会回显完整地址')).toBeTruthy()
      expect(screen.queryByText(/已配置占位/)).toBeNull()

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          alerts: expect.objectContaining({
            webhook_url: '<redacted>',
            webhook_headers: ['Authorization: <redacted>'],
            wecom_enabled: true,
            wecom_webhook_url: '<redacted>',
            dingtalk_enabled: true,
            dingtalk_webhook_url: '<redacted>',
          }),
        }))
      })
    })

    it('rejects redacted webhook URL values without a saved URL', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')
      await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
      fireEvent.change(screen.getByLabelText('Webhook URL'), {
        target: { value: '<redacted>' },
      })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'Webhook URL 无法保留',
          description: '当前没有已保存的 Webhook URL；新增 Webhook URL 需要填写真实地址。',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('rejects redacted WeCom webhook URL values without a saved URL', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')
      await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
      await user.click(screen.getByRole('switch', { name: '启用企业微信通知' }))
      fireEvent.change(screen.getByLabelText('企业微信 Webhook URL'), {
        target: { value: '<redacted>' },
      })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '企业微信 Webhook 无法保留',
          description: '当前没有已保存的企业微信 Webhook URL；新增企业微信 Webhook 需要填写真实地址。',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('rejects redacted DingTalk webhook URL values without a saved URL', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')
      await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
      await user.click(screen.getByRole('switch', { name: '启用钉钉通知' }))
      fireEvent.change(screen.getByLabelText('钉钉 Webhook URL'), {
        target: { value: '<redacted>' },
      })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '钉钉 Webhook 无法保留',
          description: '当前没有已保存的钉钉 Webhook URL；新增钉钉 Webhook 需要填写真实地址。',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('rejects redacted webhook header values without a saved header', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')
      await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
      fireEvent.change(screen.getByLabelText('Webhook 自定义 Header'), {
        target: { value: 'Authorization: <redacted>' },
      })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'Webhook Header 无法保留',
          description: 'Header Authorization 没有已保存的值；新增或改名的 Header 需要填写真实值。',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('redacts newly saved alert secrets while waiting for refreshed settings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      let resolveUpdateSettings!: (value: { success: boolean }) => void
      mockUpdateSettings.mockImplementationOnce(() => new Promise(resolve => {
        resolveUpdateSettings = resolve
      }))
      mockGetSettings
        .mockResolvedValueOnce(defaultSettingsResponse)
        .mockImplementationOnce(() => new Promise(() => {}))
      render(<SettingsPage />)

      await openTab(user, '高级')

      fireEvent.click(await screen.findByRole('switch', { name: '启用提醒' }))
      fireEvent.change(screen.getByLabelText('Webhook URL'), {
        target: { value: 'https://hooks.example.com/secret-token' },
      })
      fireEvent.change(screen.getByLabelText('Webhook 自定义 Header'), {
        target: { value: 'Authorization: Bearer secret-token\nX-Api-Key: api-secret' },
      })
      fireEvent.click(screen.getByRole('switch', { name: '启用 Telegram 通知' }))
      fireEvent.change(screen.getByLabelText('Telegram Bot Token'), {
        target: { value: '123456:secret-token' },
      })
      fireEvent.change(screen.getByLabelText('Telegram Chat ID'), {
        target: { value: '-1001234567890' },
      })
      fireEvent.click(screen.getByRole('switch', { name: '启用企业微信通知' }))
      fireEvent.change(screen.getByLabelText('企业微信 Webhook URL'), {
        target: { value: 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret-key' },
      })
      fireEvent.click(screen.getByRole('switch', { name: '启用钉钉通知' }))
      fireEvent.change(screen.getByLabelText('钉钉 Webhook URL'), {
        target: { value: 'https://oapi.dingtalk.com/robot/send?access_token=secret-token' },
      })
      fireEvent.click(screen.getByRole('switch', { name: '启用邮件通知' }))
      fireEvent.change(screen.getByLabelText('SMTP 主机'), {
        target: { value: 'smtp.example.com' },
      })
      fireEvent.change(screen.getByLabelText('SMTP 密码'), {
        target: { value: 'smtp-secret' },
      })
      fireEvent.change(screen.getByLabelText('SMTP 发件人'), {
        target: { value: 'MnemoNAS <alerts@example.com>' },
      })
      fireEvent.change(screen.getByLabelText('SMTP 收件人'), {
        target: { value: 'admin@example.com' },
      })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          alerts: expect.objectContaining({
            webhook_url: 'https://hooks.example.com/secret-token',
            webhook_headers: ['Authorization: Bearer secret-token', 'X-Api-Key: api-secret'],
            telegram_bot_token: '123456:secret-token',
            wecom_webhook_url: 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret-key',
            dingtalk_webhook_url: 'https://oapi.dingtalk.com/robot/send?access_token=secret-token',
            smtp_password: 'smtp-secret',
          }),
        }))
      })
      fireEvent.change(screen.getByLabelText('提醒检查间隔'), {
        target: { value: '45m' },
      })
      await act(async () => {
        resolveUpdateSettings({ success: true })
      })
      await waitFor(() => {
        expect(screen.getByLabelText('Webhook URL')).toHaveValue('<redacted>')
        expect(screen.getByLabelText('企业微信 Webhook URL')).toHaveValue('<redacted>')
        expect(screen.getByLabelText('钉钉 Webhook URL')).toHaveValue('<redacted>')
        const headersInput = screen.getByLabelText('Webhook 自定义 Header') as HTMLTextAreaElement
        expect(headersInput.value).toContain('<redacted>')
        expect(headersInput.value).not.toContain('secret-token')
        expect(headersInput.value).not.toContain('api-secret')
        expect(screen.getByLabelText('Telegram Bot Token')).toHaveValue('')
        expect(screen.getByLabelText('Telegram Bot Token')).toHaveAttribute('placeholder', '已配置，留空不变')
        expect(screen.getByLabelText('SMTP 密码')).toHaveValue('')
        expect(screen.getByLabelText('SMTP 密码')).toHaveAttribute('placeholder', '已配置，留空不变')
        expect(screen.getByLabelText('提醒检查间隔')).toHaveValue('45m')
      })
    })

    it('keeps existing Telegram token when the token field is left blank', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          alerts: {
            ...defaultSettingsResponse.data.alerts,
            enabled: true,
            telegram_enabled: true,
            telegram_bot_token_configured: true,
            telegram_chat_id: '-1001234567890',
          },
        },
      })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByLabelText('Telegram Bot Token')).toHaveAttribute('placeholder', '已配置，留空不变')
      })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          alerts: expect.objectContaining({
            telegram_enabled: true,
            telegram_chat_id: '-1001234567890',
          }),
        }))
      })
      expect(mockUpdateSettings.mock.calls[0][0].alerts).not.toHaveProperty('telegram_bot_token')
    })

    it('clears existing Telegram token when the clear option is selected', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          alerts: {
            ...defaultSettingsResponse.data.alerts,
            enabled: true,
            telegram_enabled: false,
            telegram_bot_token_configured: true,
            telegram_chat_id: '-1001234567890',
          },
        },
      })
      render(<SettingsPage />)

      await openTab(user, '高级')

      const clearTokenCheckbox = await screen.findByRole('checkbox', { name: '保存时清除已保存 Telegram Token' })
      await user.click(clearTokenCheckbox)
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          alerts: expect.objectContaining({
            telegram_enabled: false,
            telegram_bot_token: '',
            telegram_chat_id: '-1001234567890',
          }),
        }))
      })
    })

    it('rejects clearing Telegram token while Telegram notifications stay enabled', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          alerts: {
            ...defaultSettingsResponse.data.alerts,
            enabled: true,
            telegram_enabled: true,
            telegram_bot_token_configured: true,
            telegram_chat_id: '-1001234567890',
          },
        },
      })
      render(<SettingsPage />)

      await openTab(user, '高级')

      const clearTokenCheckbox = await screen.findByRole('checkbox', { name: '保存时清除已保存 Telegram Token' })
      await user.click(clearTokenCheckbox)
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'Telegram Bot Token 缺失',
          description: '启用 Telegram 通知时不能清除已保存 Token；请先关闭 Telegram 通知或填写新的 Bot Token。',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('keeps existing SMTP password when the password field is left blank', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          alerts: {
            ...defaultSettingsResponse.data.alerts,
            enabled: true,
            email_enabled: true,
            smtp_host: 'smtp.example.com',
            smtp_port: 587,
            smtp_username: 'alerts',
            smtp_password_configured: true,
            smtp_from: 'MnemoNAS <alerts@example.com>',
            smtp_to: ['admin@example.com', 'ops@example.com'],
          },
        },
      })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByLabelText('SMTP 密码')).toHaveAttribute('placeholder', '已配置，留空不变')
      })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          alerts: expect.objectContaining({
            email_enabled: true,
            smtp_host: 'smtp.example.com',
            smtp_port: 587,
            smtp_username: 'alerts',
            smtp_from: 'MnemoNAS <alerts@example.com>',
            smtp_to: ['admin@example.com', 'ops@example.com'],
          }),
        }))
      })
      expect(mockUpdateSettings.mock.calls[0][0].alerts).not.toHaveProperty('smtp_password')
    })

    it('clears existing SMTP password when the clear option is selected', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          alerts: {
            ...defaultSettingsResponse.data.alerts,
            enabled: true,
            email_enabled: false,
            smtp_host: 'smtp.example.com',
            smtp_port: 587,
            smtp_username: 'alerts',
            smtp_password_configured: true,
            smtp_from: 'MnemoNAS <alerts@example.com>',
            smtp_to: ['admin@example.com'],
          },
        },
      })
      render(<SettingsPage />)

      await openTab(user, '高级')

      const clearPasswordCheckbox = await screen.findByRole('checkbox', { name: '保存时清除已保存 SMTP 密码' })
      await user.click(clearPasswordCheckbox)
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          alerts: expect.objectContaining({
            email_enabled: false,
            smtp_host: 'smtp.example.com',
            smtp_port: 587,
            smtp_username: 'alerts',
            smtp_password: '',
            smtp_from: 'MnemoNAS <alerts@example.com>',
            smtp_to: ['admin@example.com'],
          }),
        }))
      })
    })

    it('rejects incomplete SMTP configuration before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('提醒通知')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用提醒' }))
      await user.click(screen.getByRole('switch', { name: '启用邮件通知' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'SMTP 主机缺失',
          description: '启用邮件通知时必须填写 SMTP 主机。',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('rejects invalid SMTP ports before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('提醒通知')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用提醒' }))
      await user.click(screen.getByRole('switch', { name: '启用邮件通知' }))
      fireEvent.change(screen.getByLabelText('SMTP 主机'), { target: { value: 'smtp.example.com' } })
      fireEvent.change(screen.getByLabelText('SMTP 端口'), { target: { value: '70000' } })
      fireEvent.change(screen.getByLabelText('SMTP 发件人'), { target: { value: 'MnemoNAS <alerts@example.com>' } })
      fireEvent.change(screen.getByLabelText('SMTP 收件人'), { target: { value: 'admin@example.com' } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'SMTP 端口格式无效',
          description: 'SMTP 端口必须是 1 到 65535 之间的整数',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('rejects invalid webhook header names before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('提醒通知')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用提醒' }))
      const headersInput = screen.getByLabelText('Webhook 自定义 Header')
      fireEvent.change(headersInput, { target: { value: 'Bad Header: value' } })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: 'Webhook Header 格式无效',
          color: 'danger',
        }))
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('rejects Unicode control characters in webhook header values before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('提醒通知')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用提醒' }))
      const headersInput = screen.getByLabelText('Webhook 自定义 Header')
      fireEvent.change(headersInput, { target: { value: 'X-MnemoNAS: alerts\u0081private' } })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: 'Webhook Header 格式无效',
          color: 'danger',
        }))
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('rejects duplicate webhook header names before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('提醒通知')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用提醒' }))
      const headersInput = screen.getByLabelText('Webhook 自定义 Header')
      fireEvent.change(headersInput, {
        target: { value: 'Authorization: Bearer one\nauthorization: Bearer two' },
      })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'Webhook Header 重复',
          description: 'Header authorization 重复；每个自定义 Header 名称只能配置一次。',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })
  })

  describe('disk health settings', () => {
    it('allows editing disk health configuration and saves it', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('磁盘健康监控')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用磁盘健康检查' }))
      fireEvent.change(screen.getByLabelText('磁盘健康检查间隔'), { target: { value: '45m' } })
      fireEvent.change(screen.getByLabelText('磁盘健康探测超时'), { target: { value: '20s' } })
      fireEvent.change(screen.getByLabelText('磁盘健康冷却时间'), { target: { value: '3h' } })
      fireEvent.change(screen.getByLabelText('磁盘健康探测命令'), { target: { value: '/usr/sbin/smartctl' } })
      fireEvent.change(screen.getByLabelText('磁盘温度提醒阈值'), { target: { value: '47' } })
      fireEvent.change(screen.getByLabelText('磁盘温度严重阈值'), { target: { value: '57' } })
      fireEvent.change(screen.getByLabelText('介质磨损提醒阈值'), { target: { value: '82' } })
      fireEvent.change(screen.getByLabelText('介质磨损严重阈值'), { target: { value: '98' } })
      fireEvent.change(screen.getByLabelText('磁盘健康设备列表'), {
        target: { value: '/dev/disk/by-id/test | Data | sat | SER123 | 45 | 55' },
      })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          disk_health: {
            enabled: true,
            check_interval: '45m',
            probe_timeout: '20s',
            cooldown_period: '3h',
            command: '/usr/sbin/smartctl',
            temperature_warning_c: 47,
            temperature_critical_c: 57,
            media_wear_warning_percent: 82,
            media_wear_critical_percent: 98,
            devices: [{
              path: '/dev/disk/by-id/test',
              name: 'Data',
              type: 'sat',
              serial: 'SER123',
              temperature_warning_c: 45,
              temperature_critical_c: 55,
            }],
          },
        }))
      })
    })

    it('rejects invalid disk health devices before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('磁盘健康监控')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用磁盘健康检查' }))
      fireEvent.change(screen.getByLabelText('磁盘健康设备列表'), { target: { value: 'sda | Data' } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '磁盘健康设备格式无效',
          description: '第 1 行设备路径必须是绝对路径',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('shows danger toast and skips save for unsafe disk temperature warning threshold', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('磁盘健康监控')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用磁盘健康检查' }))
      fireEvent.change(screen.getByLabelText('磁盘温度提醒阈值'), { target: { value: '9007199254740992' } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '磁盘温度提醒阈值格式无效',
          description: '温度提醒阈值必须是 0 或不超过安全范围的整数',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('rejects unsafe disk health device temperature thresholds before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('磁盘健康监控')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用磁盘健康检查' }))
      fireEvent.change(screen.getByLabelText('磁盘健康设备列表'), {
        target: { value: '/dev/disk/by-id/test | Data | sat | SER123 | 9007199254740992 | 9007199254740992' },
      })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '磁盘健康设备格式无效',
          description: '第 1 行 温度提醒阈值 必须是 0 或不超过安全范围的整数',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('shows danger toast and skips save for out-of-range media wear warning threshold', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('磁盘健康监控')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用磁盘健康检查' }))
      fireEvent.change(screen.getByLabelText('介质磨损提醒阈值'), { target: { value: '101' } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '介质磨损提醒阈值格式无效',
          description: '介质磨损提醒阈值必须是 0 到 100 之间的整数',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('shows danger toast and skips save for out-of-range media wear critical threshold', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('磁盘健康监控')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用磁盘健康检查' }))
      fireEvent.change(screen.getByLabelText('介质磨损严重阈值'), { target: { value: '101' } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '介质磨损严重阈值格式无效',
          description: '介质磨损严重阈值必须是 0 到 100 之间的整数',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })
  })

  describe('trash settings', () => {
    it('allows toggling trash behavior and saves it', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await waitFor(() => {
        expect(screen.getByRole('switch', { name: '启用回收站' })).toBeTruthy()
      })

      await user.click(screen.getByRole('switch'))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          trash: expect.objectContaining({
            enabled: false,
          }),
        }))
      })
    })
  })

  describe('versioning settings', () => {
    it('describes versioning rules as affecting future writes immediately', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await waitFor(() => {
        expect(screen.getByText('配置默认自动版本化规则；保存后会立即影响后续新写入文件的版本策略')).toBeTruthy()
      })
    })

    it('uses backend defaults when older settings responses omit versioning rules', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          versioning: undefined,
        },
      } as ReturnType<typeof getSettings>)
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      expect(await screen.findByLabelText('自动版本化后缀')).toHaveValue(defaultVersioningExtensionsText)
      expect(screen.getByLabelText('自动版本化文件名')).toHaveValue(defaultVersioningFilenamesText)
    })

    it('allows editing auto-versioning rules and saves them', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await waitFor(() => {
        expect(screen.getByText('自动版本化')).toBeTruthy()
      })

      const extensionsInput = await screen.findByLabelText('自动版本化后缀')
      fireEvent.change(extensionsInput, { target: { value: '.md\n.txt\n.rs' } })

      const filenamesInput = screen.getByLabelText('自动版本化文件名')
      fireEvent.change(filenamesInput, { target: { value: 'README\nDockerfile\nCargo.toml' } })

      const maxSizeInput = await screen.findByLabelText('最大自动版本化文件大小')
      fireEvent.change(maxSizeInput, { target: { value: '256MB' } })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          versioning: expect.objectContaining({
            auto_versioned_extensions: ['.md', '.txt', '.rs'],
            auto_versioned_filenames: ['README', 'Dockerfile', 'Cargo.toml'],
            max_versioned_size: 268435456,
          }),
        }))
      })
    })
  })

  describe('directory quota settings', () => {
    it('allows editing directory quotas and saves them', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          storage: {
            root: '~/.mnemonas',
            directory_quotas: [{ path: '/team', quota_bytes: 1073741824 }],
          },
        },
      } as ReturnType<typeof getSettings>)
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      const quotasInput = await screen.findByLabelText('目录配额')
      expect(quotasInput).toHaveValue('/team 1 GB')

      await user.clear(quotasInput)
      await user.type(quotasInput, '/team 2 GB{enter}/media 512 MB')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          storage: expect.objectContaining({
            directory_quotas: [
              { path: '/team', quota_bytes: 2147483648 },
              { path: '/media', quota_bytes: 536870912 },
            ],
          }),
        }))
      })
    })

    it('allows quoted directory quota paths with spaces', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          storage: {
            root: '~/.mnemonas',
            directory_quotas: [{ path: '/Family Photos', quota_bytes: 1073741824 }],
          },
        },
      } as ReturnType<typeof getSettings>)
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      const quotasInput = await screen.findByLabelText('目录配额')
      expect(quotasInput).toHaveValue('"/Family Photos" 1 GB')
      expect(screen.getByText(/路径含空格或双引号时使用双引号/)).toBeTruthy()

      fireEvent.change(quotasInput, {
        target: { value: '"/Family Photos" 2 GB\n"/Media Archive" 512 MB' },
      })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          storage: expect.objectContaining({
            directory_quotas: [
              { path: '/Family Photos', quota_bytes: 2147483648 },
              { path: '/Media Archive', quota_bytes: 536870912 },
            ],
          }),
        }))
      })
    })

    it('escapes directory quota paths with literal quotes', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          storage: {
            root: '~/.mnemonas',
            directory_quotas: [{ path: '/Family "Photos"', quota_bytes: 1073741824 }],
          },
        },
      } as ReturnType<typeof getSettings>)
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      const quotasInput = await screen.findByLabelText('目录配额')
      expect(quotasInput).toHaveValue('"/Family \\"Photos\\"" 1 GB')

      fireEvent.change(quotasInput, {
        target: { value: '"/Family \\"Photos\\"" 2 GB' },
      })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          storage: expect.objectContaining({
            directory_quotas: [
              { path: '/Family "Photos"', quota_bytes: 2147483648 },
            ],
          }),
        }))
      })
    })

    it('summarizes unsaved directory quota additions, updates, and removals', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          storage: {
            root: '~/.mnemonas',
            directory_quotas: [
              { path: '/team', quota_bytes: 1073741824 },
              { path: '/archive', quota_bytes: 10737418240 },
            ],
          },
        },
      } as ReturnType<typeof getSettings>)

      render(<SettingsPage />)

      await openTab(user, '版本保留')

      const review = await screen.findByLabelText('目录配额变更复核')
      expect(within(review).getByText('目录配额与已保存配置一致。')).toBeTruthy()

      const quotasInput = screen.getByLabelText('目录配额')
      fireEvent.change(quotasInput, { target: { value: '/team 2 GB\n/media 512 MB' } })

      expect(within(review).getByText('新增 1')).toBeTruthy()
      expect(within(review).getByText('修改 1')).toBeTruthy()
      expect(within(review).getByText('删除 1')).toBeTruthy()
      expect(within(review).getByText('/team')).toBeTruthy()
      expect(within(review).getByText('/media')).toBeTruthy()
      expect(within(review).getByText('/archive')).toBeTruthy()
      expect(within(review).getByText('配额从 1 GB 调整为 2 GB')).toBeTruthy()
      expect(within(review).getByText('容量 512 MB')).toBeTruthy()
      expect(within(review).getByText('容量 10 GB')).toBeTruthy()
    })

    it('normalizes duplicate and trailing slashes in scoped storage paths before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      const quotasInput = await screen.findByLabelText('目录配额')
      await user.clear(quotasInput)
      await user.type(quotasInput, '/team// 2 GB')
      fireEvent.change(await screen.findByLabelText('目录权限路径 1'), { target: { value: '/team//public/' } })
      fireEvent.change(screen.getByLabelText('读组 1'), { target: { value: 'family' } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          storage: expect.objectContaining({
            directory_quotas: [{ path: '/team', quota_bytes: 2147483648 }],
            directory_access_rules: [{ path: '/team/public', read_groups: ['family'] }],
          }),
        }))
      })
    })

    it('rejects invalid directory quota lines before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      const quotasInput = await screen.findByLabelText('目录配额')
      await user.type(quotasInput, 'team 1 GB')
      await user.click(screen.getByText('保存设置'))

      expect(mockUpdateSettings).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '目录配额格式无效',
      }))
    })

    it('rejects Unicode control characters in directory quota paths before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      const quotasInput = await screen.findByLabelText('目录配额')
      fireEvent.change(quotasInput, { target: { value: '/team\u0081private 1 GB' } })
      await user.click(screen.getByText('保存设置'))

      expect(mockUpdateSettings).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '目录配额格式无效',
        description: '第 1 行路径无效',
      }))
    })

    it('rejects unclosed quoted directory quota paths before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      const quotasInput = await screen.findByLabelText('目录配额')
      fireEvent.change(quotasInput, { target: { value: '"/Family Photos 1 GB' } })
      await user.click(screen.getByText('保存设置'))

      expect(mockUpdateSettings).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '目录配额格式无效',
        description: '第 1 行路径引号未闭合',
      }))
    })

    it('rejects unsafe directory quota sizes before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      const quotasInput = await screen.findByLabelText('目录配额')
      await user.type(quotasInput, '/team 9007199254740992 B')
      await user.click(screen.getByText('保存设置'))

      expect(mockUpdateSettings).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '目录配额格式无效',
        description: '第 1 行容量必须是大于 0 且不超过安全范围的整数',
      }))
    })

    it('allows editing directory access rules and saves them', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          storage: {
            root: '~/.mnemonas',
            directory_access_rules: [{ path: '/team', read_groups: ['family'], write_groups: ['editors'] }],
          },
        },
      } as ReturnType<typeof getSettings>)
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      const pathInput = await screen.findByLabelText('目录权限路径 1')
      const readGroupsInput = screen.getByLabelText('读组 1')
      const writeGroupsInput = screen.getByLabelText('写组 1')
      expect(pathInput).toHaveValue('/team')
      expect(readGroupsInput).toHaveValue('family')
      expect(writeGroupsInput).toHaveValue('editors')

      fireEvent.change(pathInput, { target: { value: '/media' } })
      fireEvent.change(readGroupsInput, { target: { value: '' } })
      fireEvent.change(writeGroupsInput, { target: { value: '' } })
      fireEvent.change(screen.getByLabelText('读用户 1'), { target: { value: 'alice,bob' } })
      fireEvent.change(screen.getByLabelText('写角色 1'), { target: { value: 'admin' } })
      fireEvent.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          storage: expect.objectContaining({
            directory_access_rules: [
              { path: '/media', read_users: ['alice', 'bob'], write_roles: ['admin'] },
            ],
          }),
        }))
      })
    })

    it('allows directory access rule paths with spaces', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          storage: {
            root: '~/.mnemonas',
            directory_access_rules: [{ path: '/Family Photos', read_groups: ['family'] }],
          },
        },
      } as ReturnType<typeof getSettings>)
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      expect(await screen.findByLabelText('目录权限路径 1')).toHaveValue('/Family Photos')
      expect(screen.getByText(/路径直接填写 MnemoNAS 逻辑路径/)).toBeTruthy()
      expect(screen.getByLabelText('读组 1')).toHaveValue('family')

      fireEvent.change(screen.getByLabelText('写组 1'), { target: { value: 'editors' } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          storage: expect.objectContaining({
            directory_access_rules: [
              { path: '/Family Photos', read_groups: ['family'], write_groups: ['editors'] },
            ],
          }),
        }))
      })
    })

    it('escapes directory access rule paths with literal quotes', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          storage: {
            root: '~/.mnemonas',
            directory_access_rules: [{ path: '/Family "Photos"', read_groups: ['family'] }],
          },
        },
      } as ReturnType<typeof getSettings>)
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      expect(await screen.findByLabelText('目录权限路径 1')).toHaveValue('/Family "Photos"')
      expect(screen.getByLabelText('读组 1')).toHaveValue('family')

      fireEvent.change(screen.getByLabelText('写组 1'), { target: { value: 'editors' } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          storage: expect.objectContaining({
            directory_access_rules: [
              { path: '/Family "Photos"', read_groups: ['family'], write_groups: ['editors'] },
            ],
          }),
        }))
      })
    })

    it('rejects line-syntax quotes in structured directory access rule paths before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          storage: {
            root: '~/.mnemonas',
            directory_access_rules: [{ path: '/team', read_groups: ['family'] }],
          },
        },
      } as ReturnType<typeof getSettings>)
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      const pathInput = await screen.findByLabelText('目录权限路径 1')
      fireEvent.change(pathInput, { target: { value: '"/Family Photos' } })
      await user.click(screen.getByText('保存设置'))

      expect(mockUpdateSettings).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '目录权限格式无效',
        description: '第 1 行路径无效',
      }))
    })

    it('rejects Unicode control characters in directory access rule paths before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          storage: {
            root: '~/.mnemonas',
            directory_access_rules: [{ path: '/team', read_groups: ['family'] }],
          },
        },
      } as ReturnType<typeof getSettings>)
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      const pathInput = await screen.findByLabelText('目录权限路径 1')
      fireEvent.change(pathInput, { target: { value: '/team\u0081private' } })
      await user.click(screen.getByText('保存设置'))

      expect(mockUpdateSettings).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '目录权限格式无效',
        description: '第 1 行路径无效',
      }))
    })

    it('adds directory access rule rows', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          storage: {
            root: '~/.mnemonas',
            directory_access_rules: [{ path: '/team', read_groups: ['family'] }],
          },
        },
      } as ReturnType<typeof getSettings>)
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await user.click(await screen.findByRole('button', { name: '添加规则' }))
      await user.type(screen.getByLabelText('目录权限路径 2'), '/shared')
      await user.type(screen.getByLabelText('读角色 2'), 'user')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          storage: expect.objectContaining({
            directory_access_rules: [
              { path: '/team', read_groups: ['family'] },
              { path: '/shared', read_roles: ['user'] },
            ],
          }),
        }))
      })
    })

    it('applies directory access rule presets before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await user.click(await screen.findByRole('button', { name: /全员协作/ }))

      expect(screen.getByLabelText('目录权限路径 1')).toHaveValue('/shared')
      expect(screen.getByLabelText('读角色 1')).toHaveValue('user')
      expect(screen.getByLabelText('写角色 1')).toHaveValue('user')

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          storage: expect.objectContaining({
            directory_access_rules: [
              { path: '/shared', read_roles: ['user'], write_roles: ['user'] },
            ],
          }),
        }))
      })
    })

    it('summarizes unsaved directory access rule additions, updates, and removals', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          storage: {
            ...defaultSettingsResponse.data.storage,
            directory_access_rules: [
              { path: '/team', read_groups: ['family'] },
              { path: '/archive', read_roles: ['admin'], write_roles: ['admin'] },
            ],
          },
        },
      })

      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await waitFor(() => {
        expect(screen.getByText('目录权限与已保存配置一致。')).toBeTruthy()
      })

      fireEvent.change(screen.getByLabelText('写组 1'), { target: { value: 'family' } })
      await user.click(screen.getByLabelText('删除目录权限规则 2'))
      await user.click(screen.getByRole('button', { name: /添加规则/ }))
      fireEvent.change(screen.getByLabelText('目录权限路径 2'), { target: { value: '/shared' } })
      fireEvent.change(screen.getByLabelText('读角色 2'), { target: { value: 'user' } })

      expect(screen.getByText('新增 1')).toBeTruthy()
      expect(screen.getByText('修改 1')).toBeTruthy()
      expect(screen.getByText('删除 1')).toBeTruthy()
      expect(screen.getByText('变更字段：写组')).toBeTruthy()
      expect(screen.getByText('/team')).toBeTruthy()
      expect(screen.getByText('/shared')).toBeTruthy()
      expect(screen.getByText('/archive')).toBeTruthy()
      expect(screen.getByText('读角色: user')).toBeTruthy()
      expect(screen.getByText('读角色: admin · 写角色: admin')).toBeTruthy()
    })

    it('summarizes directory access rule coverage and attention items', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        ...defaultSettingsResponse,
        data: {
          ...defaultSettingsResponse.data,
          storage: {
            ...defaultSettingsResponse.data.storage,
            directory_access_rules: [
              { path: '/', read_roles: ['user'] },
              { path: '/shared', read_roles: ['user'], write_roles: ['user'] },
              { path: '/team', read_groups: ['family'], write_groups: ['editors'] },
            ],
          },
        },
      } as ReturnType<typeof getSettings>)

      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await waitFor(() => {
        const summary = within(screen.getByLabelText('目录权限覆盖摘要'))
        expect(summary.getByText('目录权限覆盖摘要')).toBeTruthy()
        expect(summary.getByText('规则总数')).toBeTruthy()
        expect(summary.getByText('3 条')).toBeTruthy()
        expect(summary.getByText('有效可读主体')).toBeTruthy()
        expect(summary.getByText('3 个')).toBeTruthy()
        expect(summary.getByText('可写主体')).toBeTruthy()
        expect(summary.getByText('写权限路径')).toBeTruthy()
        expect(summary.getAllByText('2 个')).toHaveLength(2)
        expect(summary.getByText('权限关注项')).toBeTruthy()
        expect(summary.getByText('根路径授权')).toBeTruthy()
        expect(summary.getByText('普通用户可写')).toBeTruthy()
      })
    })

    it('checks effective directory permissions', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await user.type(await screen.findByLabelText('检查用户'), 'alice')
      const pathInput = screen.getByLabelText('检查路径')
      await user.clear(pathInput)
      await user.type(pathInput, '/team/readme.txt')
      await user.click(screen.getByRole('button', { name: '检查权限' }))

      await waitFor(() => {
        expect(mockCheckDirectoryAccess).toHaveBeenCalled()
      })
      expect(mockCheckDirectoryAccess.mock.calls[0]?.[0]).toEqual({ username: 'alice', path: '/team/readme.txt' })
      expect(mockCheckDirectoryAccess.mock.calls[0]?.[1]).toEqual({
        signal: expect.any(AbortSignal),
      })
      expect(await screen.findByText('读取')).toBeTruthy()
      expect(screen.getByText('写入')).toBeTruthy()
      expect(screen.getByText('允许')).toBeTruthy()
      expect(screen.getByText('拒绝')).toBeTruthy()
      expect(screen.getAllByText(/目录规则/).length).toBeGreaterThanOrEqual(2)
    })

    it.each([
      ['dot segment', '/team/./readme.txt'],
      ['backslash', '/team\\readme.txt'],
      ['query marker', '/team?readme.txt'],
      ['fragment marker', '/team#readme.txt'],
    ])('rejects malformed directory access check paths with %s before checking', async (_label, invalidPath) => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await user.type(await screen.findByLabelText('检查用户'), 'alice')
      const pathInput = screen.getByLabelText('检查路径')
      await user.clear(pathInput)
      fireEvent.change(pathInput, { target: { value: invalidPath } })
      await user.click(screen.getByRole('button', { name: '检查权限' }))

      expect(mockCheckDirectoryAccess).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '权限检查路径无效',
        description: '路径必须是站内绝对路径，且不能包含反斜杠、?、#、控制字符、. 或 .. 路径段。',
        color: 'warning',
      })
    })

    it('maps directory access decision backend messages before rendering them', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockCheckDirectoryAccess.mockResolvedValueOnce({
        username: 'bob',
        user_id: 'u2',
        role: 'user',
        groups: [],
        home_dir: '/users/bob',
        path: '/team/readme.txt',
        read: {
          mode: 'read',
          allowed: true,
          source: 'directory_access_rule',
          message: 'directory access rule grants read through an existing descendant',
          matched_rule: { path: '/team/projects', read_roles: ['user'] },
        },
        write: {
          mode: 'write',
          allowed: false,
          source: 'home_dir',
          message: 'path is outside the user\'s home_dir',
        },
      })

      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await user.type(await screen.findByLabelText('检查用户'), 'bob')
      const pathInput = screen.getByLabelText('检查路径')
      await user.clear(pathInput)
      await user.type(pathInput, '/team/readme.txt')
      await user.click(screen.getByRole('button', { name: '检查权限' }))

      await waitFor(() => {
        expect(screen.getByText('已存在的子目录命中读取规则，因此允许查看相关路径。')).toBeTruthy()
        expect(screen.getByText('路径位于该用户主目录外。')).toBeTruthy()
      })
      expect(screen.queryByText('directory access rule grants read through an existing descendant')).toBeNull()
      expect(screen.queryByText('path is outside the user\'s home_dir')).toBeNull()
    })

    it('builds directory access user matrix', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      const pathInput = await screen.findByLabelText('检查路径')
      await user.clear(pathInput)
      await user.type(pathInput, '/team/readme.txt')
      await user.click(screen.getByRole('button', { name: '用户矩阵' }))

      await waitFor(() => {
        expect(mockReportDirectoryAccess).toHaveBeenCalled()
      })
      expect(mockReportDirectoryAccess.mock.calls[0]?.[0]).toEqual({ path: '/team/readme.txt' })
      expect(mockReportDirectoryAccess.mock.calls[0]?.[1]).toEqual({
        signal: expect.any(AbortSignal),
      })
      expect(await screen.findByLabelText('目录权限用户矩阵')).toBeTruthy()
      expect(screen.getByText('用户 2')).toBeTruthy()
      expect(screen.getByText('可读 1')).toBeTruthy()
      expect(screen.getByText('可写 1')).toBeTruthy()
      expect(screen.getByText('相关分享 1')).toBeTruthy()
      expect(screen.getByText('活跃分享 1')).toBeTruthy()
      expect(screen.getByText('密码分享 1')).toBeTruthy()
      expect(screen.getByText('alice')).toBeTruthy()
      expect(screen.getByText('bob')).toBeTruthy()
      expect(screen.getByText('/team')).toBeTruthy()
      expect(screen.getByText('可访问')).toBeTruthy()

      await user.click(screen.getByRole('button', { name: '复制复核记录' }))

      await waitFor(() => {
        expect(writeText).toHaveBeenCalled()
      })
      const copiedReport = String(writeText.mock.calls[0]?.[0] ?? '')
      expect(copiedReport).toContain('目录权限复核记录')
      expect(copiedReport).toContain('类型: 用户矩阵')
      expect(copiedReport).toContain('路径: /team/readme.txt')
      expect(copiedReport).toContain('读取: 允许 1 / 拒绝 1')
      expect(copiedReport).toContain('写入: 允许 1 / 拒绝 1')
      expect(copiedReport).toContain('- alice (user · 组 family, home /users/alice): 读 允许 · 目录规则 · 规则 /team; 写 允许 · 目录规则 · 规则 /team')
      expect(copiedReport).toContain('- /team (文件夹 · 父级覆盖): 可访问 · 密码保护 · 访问 0/不限 · 创建者 u1')
      expect(mockAddToast).toHaveBeenCalledWith({ title: '目录权限复核记录已复制', color: 'success' })
    })

    it('rejects malformed directory access matrix paths before reporting', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      const pathInput = await screen.findByLabelText('检查路径')
      await user.clear(pathInput)
      await user.type(pathInput, '/team/./readme.txt')
      await user.click(screen.getByRole('button', { name: '用户矩阵' }))

      expect(mockReportDirectoryAccess).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '权限矩阵路径无效',
        description: '路径必须是站内绝对路径，且不能包含反斜杠、?、#、控制字符、. 或 .. 路径段。',
        color: 'warning',
      })
    })

    it('previews unsaved directory access rule changes', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      fireEvent.change(await screen.findByLabelText('目录权限路径 1'), { target: { value: '/team' } })
      fireEvent.change(screen.getByLabelText('读角色 1'), { target: { value: 'user' } })
      const pathInput = screen.getByLabelText('检查路径')
      fireEvent.change(pathInput, { target: { value: '/team/readme.txt' } })
      await user.click(screen.getByRole('button', { name: '预览变更' }))

      await waitFor(() => {
        expect(mockPreviewDirectoryAccess).toHaveBeenCalled()
      })
      expect(mockPreviewDirectoryAccess.mock.calls[0]?.[0]).toEqual({
        path: '/team/readme.txt',
        directory_access_rules: [{ path: '/team', read_roles: ['user'] }],
      })
      expect(mockPreviewDirectoryAccess.mock.calls[0]?.[1]).toEqual({
        signal: expect.any(AbortSignal),
      })
      expect(await screen.findByLabelText('目录权限变更预览')).toBeTruthy()
      expect(screen.getByText('变更预览')).toBeTruthy()
      expect(screen.getByText('可读 2')).toBeTruthy()
    })

    it('rejects malformed directory access preview paths before previewing', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      fireEvent.change(await screen.findByLabelText('目录权限路径 1'), { target: { value: '/team' } })
      fireEvent.change(screen.getByLabelText('读角色 1'), { target: { value: 'user' } })
      fireEvent.change(screen.getByLabelText('检查路径'), { target: { value: '/team/./readme.txt' } })
      await user.click(screen.getByRole('button', { name: '预览变更' }))

      expect(mockPreviewDirectoryAccess).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '权限预览路径无效',
        description: '路径必须是站内绝对路径，且不能包含反斜杠、?、#、控制字符、. 或 .. 路径段。',
        color: 'warning',
      })
    })

    it('aborts pending directory access checks when the page unmounts', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockCheckDirectoryAccess.mockReturnValue(new Promise(() => {}) as ReturnType<typeof checkDirectoryAccess>)
      const { unmount } = render(<SettingsPage />)

      await openTab(user, '版本保留')

      await user.type(await screen.findByLabelText('检查用户'), 'alice')
      const pathInput = screen.getByLabelText('检查路径')
      await user.clear(pathInput)
      await user.type(pathInput, '/team/readme.txt')
      await user.click(screen.getByRole('button', { name: '检查权限' }))

      await waitFor(() => {
        expect(mockCheckDirectoryAccess).toHaveBeenCalled()
      })
      const signal = mockCheckDirectoryAccess.mock.calls[0]?.[1]?.signal
      expect(signal).toBeInstanceOf(AbortSignal)
      expect(signal?.aborted).toBe(false)

      unmount()

      expect(signal?.aborted).toBe(true)
    })
  })

  describe('trash settings', () => {
    it('allows editing trash retention policy and saves it', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '版本保留')

    await waitFor(() => {
      expect(screen.getByText('版本策略')).toBeTruthy()
    })

    const retentionInput = screen.getByLabelText('回收站保留天数')
    await user.clear(retentionInput)
    await user.type(retentionInput, '7')

    const maxSizeInput = screen.getByLabelText('回收站最大容量')
    await user.clear(maxSizeInput)
    await user.type(maxSizeInput, '2GB')

    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expectUpdateSettingsCalledWith(expect.objectContaining({
      trash: expect.objectContaining({
        enabled: true,
        retention_days: 7,
        max_size: 2147483648,
      }),
      }))
    })
    })

    it('allows editing version retention thresholds and saves them', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await waitFor(() => {
        expect(screen.getByText('版本策略')).toBeTruthy()
      })

      fireEvent.change(screen.getByLabelText('最大保留时间'), { target: { value: '720h' } })
      fireEvent.change(screen.getByLabelText('最小空闲空间'), { target: { value: '5GB' } })
      fireEvent.change(screen.getByLabelText('GC 运行间隔'), { target: { value: '12h' } })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          retention: expect.objectContaining({
            max_age: '720h',
            min_free_space: 5368709120,
            gc_interval: '12h',
          }),
        }))
      })
    })
  })

  describe('favorites settings', () => {
    it('allows toggling favorites behavior and saves it', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('收藏功能')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用收藏功能' }))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          favorites: expect.objectContaining({
            enabled: false,
          }),
        }))
      })
    })

    it('warns when favorites are enabled but runtime is unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValueOnce({
        data: {
          ...defaultSettingsResponse.data,
          favorites: {
            enabled: true,
            runtime_available: false,
          },
        },
      })

      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('收藏运行态当前不可用')).toBeTruthy()
        expect(screen.getByText('配置已启用，但运行中的收藏存储未就绪；收藏接口会返回不可用，直到服务恢复对收藏存储的访问。')).toBeTruthy()
      })
    })
  })

  describe('general settings', () => {
    it('allows editing server timeouts and saves them', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      const readTimeoutInput = await screen.findByLabelText('服务器读取超时')
      await user.clear(readTimeoutInput)
      await user.type(readTimeoutInput, '45s')

      const writeTimeoutInput = screen.getByLabelText('服务器写入超时')
      await user.clear(writeTimeoutInput)
      await user.type(writeTimeoutInput, '90s')

      const idleTimeoutInput = screen.getByLabelText('服务器空闲超时')
      await user.clear(idleTimeoutInput)
      await user.type(idleTimeoutInput, '5m')

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          server: expect.objectContaining({
            read_timeout: '45s',
            write_timeout: '90s',
            idle_timeout: '5m',
          }),
        }))
      })
    })

    it('allows editing auth session token lifetimes and saves them', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      const accessTokenInput = await screen.findByLabelText('访问令牌有效期')
      const refreshTokenInput = screen.getByLabelText('刷新令牌有效期')

      expect(accessTokenInput).toHaveValue('15m0s')
      expect(refreshTokenInput).toHaveValue('168h0m0s')

      await user.clear(accessTokenInput)
      await user.type(accessTokenInput, '30m')
      await user.clear(refreshTokenInput)
      await user.type(refreshTokenInput, '720h')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          auth: expect.objectContaining({
            access_token_ttl: '30m',
            refresh_token_ttl: '720h',
          }),
        }))
      })
    })

    it('rejects invalid auth session token lifetimes before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      const accessTokenInput = await screen.findByLabelText('访问令牌有效期')
      await user.clear(accessTokenInput)
      await user.type(accessTokenInput, '0')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '会话有效期无效',
          description: '访问令牌有效期必须使用 15m / 1h 这类 Go duration 格式，且大于 0',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('renders trusted proxy hops input', async () => {
      render(<SettingsPage />)
      await waitFor(() => {
        expect(screen.getByLabelText('受信代理层数')).toHaveValue(1)
      })
    })

    it('renders trusted proxy CIDR input', async () => {
      render(<SettingsPage />)
      await waitFor(() => {
        expect(screen.getByLabelText('受信代理来源')).toHaveValue('10.0.0.0/8')
      })
    })

    it('allows editing trusted proxy hops and saves it', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      const input = await screen.findByLabelText('受信代理层数')
      await user.clear(input)
      await user.type(input, '2')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          server: expect.objectContaining({
            trusted_proxy_hops: 2,
          }),
        }))
      })
    })

    it('allows editing trusted proxy CIDRs and saves them', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      const input = await screen.findByLabelText('受信代理来源')
      await user.clear(input)
      await user.type(input, '10.0.0.0/8\n192.168.1.10\nfd00::/8')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          server: expect.objectContaining({
            trusted_proxy_cidrs: ['10.0.0.0/8', '192.168.1.10', 'fd00::/8'],
          }),
        }))
      })
    })

    it('rejects negative trusted proxy hops before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      const input = await screen.findByLabelText('受信代理层数')
      await user.clear(input)
      await user.type(input, '-1')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '受信代理层数格式无效',
          description: '受信代理层数必须是 0 或不超过安全范围的整数',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('rejects invalid trusted proxy CIDRs before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      const input = await screen.findByLabelText('受信代理来源')
      await user.clear(input)
      await user.type(input, 'not-a-cidr')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '受信代理来源格式无效',
          description: '每行必须是 IP 地址或 CIDR，例如 10.0.0.0/8、192.168.1.10 或 fd00::/8',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('renders server host input', async () => {
      render(<SettingsPage />)
      await waitFor(() => {
        expect(screen.getByLabelText('服务器监听地址')).toHaveValue('0.0.0.0')
      })
    })

    it('renders server port input', async () => {
      render(<SettingsPage />)
      await waitFor(() => {
        expect(screen.getByLabelText('服务器端口')).toHaveValue(8080)
      })
    })

    it('renders storage root as read-only display with guidance', async () => {
      render(<SettingsPage />)
      await waitFor(() => {
        expect(screen.getByText('~/.mnemonas')).toBeTruthy()
        expect(screen.getByText('当前值由服务端配置文件决定，界面中不可直接修改。如需调整，请修改配置文件并重启服务。')).toBeTruthy()
      })
    })

    it('allows editing server host', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

        const input = await screen.findByLabelText('服务器监听地址')
        await user.clear(input)
        await user.type(input, '127.0.0.1')

      expect(input).toHaveValue('127.0.0.1')
    })

    it('keeps unsaved edits when settings refetch in background', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings
        .mockResolvedValueOnce({
          data: {
            server: { host: '0.0.0.0', port: 8080, read_timeout: '30s', write_timeout: '60s', idle_timeout: '120s', trusted_proxy_hops: 1, read_timeout_seconds: 60, write_timeout_seconds: 300 },
            storage: { root: '~/.mnemonas' },
            auth: { enabled: true, access_token_ttl: '15m0s', refresh_token_ttl: '168h0m0s' },
            trash: { enabled: true, retention_days: 30, max_size: 10737418240 },
            retention: { max_versions: 100, max_age: '8760h', min_free_space: 10737418240, gc_interval: '24h' },
            versioning: { auto_versioned_extensions: ['.md', '.txt', '.go'], auto_versioned_filenames: ['README', 'Dockerfile', 'Makefile'], max_versioned_size: 104857600 },
            webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
            share: { enabled: false, base_url: '' },
            favorites: { enabled: true },
            alerts: { enabled: false, check_interval: '1h', threshold_pct: 90, critical_pct: 95, min_free_bytes: 10737418240, cooldown_period: '4h', webhook_url: '', webhook_method: 'POST', webhook_headers: [], telegram_enabled: false, telegram_bot_token_configured: false, telegram_chat_id: '' },
            cdc: { min_chunk_size: 262144, avg_chunk_size: 1048576, max_chunk_size: 4194304 },
            dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
          },
        })
        .mockResolvedValue({
          data: {
            server: { host: '10.0.0.1', port: 9090, read_timeout: '45s', write_timeout: '90s', idle_timeout: '5m', trusted_proxy_hops: 3, read_timeout_seconds: 60, write_timeout_seconds: 300 },
            storage: { root: '/srv/mnemonas' },
            auth: { enabled: true, access_token_ttl: '30m0s', refresh_token_ttl: '720h0m0s' },
            trash: { enabled: false, retention_days: 14, max_size: 2147483648 },
            retention: { max_versions: 200, max_age: '720h', min_free_space: 2147483648, gc_interval: '12h' },
            versioning: { auto_versioned_extensions: ['.md', '.txt'], auto_versioned_filenames: ['README', 'LICENSE'], max_versioned_size: 209715200 },
            webdav: { enabled: false, prefix: '/files', read_only: true, auth_type: 'basic', username: 'sync-user' },
            share: { enabled: true, base_url: 'https://share.example.com' },
            favorites: { enabled: false },
            alerts: { enabled: true, check_interval: '30m', threshold_pct: 85, critical_pct: 92, min_free_bytes: 21474836480, cooldown_period: '2h', webhook_url: '<redacted>', webhook_url_configured: true, webhook_method: 'GET', webhook_headers: ['Authorization: <redacted>', 'X-MnemoNAS: <redacted>'], webhook_headers_configured: true, telegram_enabled: true, telegram_bot_token_configured: true, telegram_chat_id: '-1001234567890' },
            cdc: { min_chunk_size: 131072, avg_chunk_size: 524288, max_chunk_size: 2097152 },
            dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
          },
        })

      render(<SettingsPage />)

        const input = await screen.findByLabelText('服务器监听地址')
        await user.clear(input)
        await user.type(input, '127.0.0.1')
      expect(input).toHaveValue('127.0.0.1')

      await act(async () => {
        window.dispatchEvent(new Event('focus'))
      })

        await waitFor(() => {
          expect(screen.getByLabelText('服务器监听地址')).toHaveValue('127.0.0.1')
        })
    })

    it('keeps saved values visible until the post-save refetch completes', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      let resolveRefetch: ((value: typeof defaultSettingsResponse) => void) | undefined

      mockGetSettings
        .mockResolvedValueOnce(defaultSettingsResponse)
        .mockReturnValueOnce(new Promise<typeof defaultSettingsResponse>((resolve) => {
          resolveRefetch = resolve
        }) as ReturnType<typeof getSettings>)

      render(<SettingsPage />)

        const input = await screen.findByLabelText('服务器端口')
        await user.clear(input)
        await user.type(input, '9000')

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockUpdateSettings).toHaveBeenCalled()
        expect(mockGetSettings).toHaveBeenCalledTimes(2)
      })

      expect(screen.getByLabelText('服务器端口')).toHaveValue(9000)

      await act(async () => {
        resolveRefetch?.({
          data: {
            ...defaultSettingsResponse.data,
            server: {
              ...defaultSettingsResponse.data.server,
              port: 9000,
            },
          },
        })
      })

      await waitFor(() => {
        expect(screen.getByLabelText('服务器端口')).toHaveValue(9000)
      })
    })

    it('shows a restart-required warning when the backend reports save changes may need restart', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUpdateSettings.mockResolvedValueOnce({
        success: true,
        message: 'settings updated, some changes may require restart',
      })

      render(<SettingsPage />)

    expect(await screen.findByLabelText('服务器端口')).toHaveValue(8080)

    await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '设置已保存，部分变更需要重启后生效',
          description: '部分配置项需要重启相关服务后才会生效。',
          color: 'warning',
        })
      })
    })

    it('shows a warning toast when the backend marks a save as successful with warnings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUpdateSettings.mockResolvedValueOnce({
        success: true,
        warning: true,
        message: 'settings saved with persistence warning --password repo:pass/with/slash',
      })

      render(<SettingsPage />)

        expect(await screen.findByLabelText('服务器端口')).toHaveValue(8080)

        await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '设置已保存，但存在警告',
          description: 'settings saved with persistence warning --password <redacted>',
          color: 'warning',
        })
      })
      expect(mockAddToast).not.toHaveBeenCalledWith(expect.objectContaining({
        description: expect.stringMatching(/repo:pass|with\/slash/),
      }))
    })

    it('shows a success toast when the backend reports a hot-applied save', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUpdateSettings.mockResolvedValueOnce({
        success: true,
        message: 'settings updated',
      })

      render(<SettingsPage />)

      expect(await screen.findByLabelText('服务器端口')).toHaveValue(8080)

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '设置已保存',
          color: 'success',
        })
      })
    })

    it('preserves newer local edits when an older save resolves', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const firstSave = createDeferred<{ success: boolean; message: string }>()

      mockGetSettings
        .mockResolvedValueOnce(defaultSettingsResponse)
        .mockResolvedValueOnce({
          data: {
            ...defaultSettingsResponse.data,
            server: {
              ...defaultSettingsResponse.data.server,
              port: 9000,
            },
          },
        })
      mockUpdateSettings
        .mockImplementationOnce(() => firstSave.promise)
        .mockResolvedValueOnce({ success: true, message: 'ok' })

      render(<SettingsPage />)

        const input = await screen.findByLabelText('服务器端口')
        await user.clear(input)
        await user.type(input, '9000')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          server: expect.objectContaining({
            port: 9000,
          }),
        }))
      })

        await user.clear(input)
        await user.type(input, '9001')
        expect(input).toHaveValue(9001)

      await act(async () => {
        firstSave.resolve({ success: true, message: 'ok' })
      })

        await waitFor(() => {
          expect(mockGetSettings).toHaveBeenCalledTimes(2)
          expect(screen.getByLabelText('服务器端口')).toHaveValue(9001)
        })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsLastCalledWith(expect.objectContaining({
          server: expect.objectContaining({
            port: 9001,
          }),
        }))
      })
    })

    it('disables reset while a save request is pending', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const pendingSave = createDeferred<{ success: boolean; message: string }>()

      mockGetSettings.mockResolvedValue(defaultSettingsResponse)
      mockUpdateSettings.mockImplementationOnce(() => pendingSave.promise)

      render(<SettingsPage />)

        const input = await screen.findByLabelText('服务器端口')
        await user.clear(input)
      await user.type(input, '9000')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockUpdateSettings).toHaveBeenCalled()
      })

      const resetButton = screen.getByRole('button', { name: '重置' })
      expect(resetButton).toBeDisabled()

      await user.click(resetButton)

      expect(mockGetSettings).toHaveBeenCalledTimes(1)

      await act(async () => {
        pendingSave.resolve({ success: true, message: 'ok' })
      })
    })

    it('reset restores server values after local edits', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValue({
        data: {
          server: { host: '10.0.0.1', port: 9090, read_timeout: '45s', write_timeout: '90s', idle_timeout: '5m', read_timeout_seconds: 60, write_timeout_seconds: 300 },
          storage: { root: '/srv/mnemonas' },
          auth: { enabled: true, access_token_ttl: '30m0s', refresh_token_ttl: '720h0m0s' },
          trash: { enabled: false, retention_days: 14, max_size: 2147483648 },
          retention: { max_versions: 200, max_age: '720h', min_free_space: 2147483648, gc_interval: '12h' },
          versioning: { auto_versioned_extensions: ['.md', '.txt'], auto_versioned_filenames: ['README', 'LICENSE'], max_versioned_size: 209715200 },
          webdav: { enabled: false, prefix: '/files', read_only: true, auth_type: 'basic', username: 'sync-user' },
          share: { enabled: true, base_url: 'https://share.example.com' },
          favorites: { enabled: false },
          alerts: { enabled: true, check_interval: '30m', threshold_pct: 85, critical_pct: 92, min_free_bytes: 21474836480, cooldown_period: '2h', webhook_url: '<redacted>', webhook_url_configured: true, webhook_method: 'GET', webhook_headers: ['Authorization: <redacted>', 'X-MnemoNAS: <redacted>'], webhook_headers_configured: true, telegram_enabled: true, telegram_bot_token_configured: true, telegram_chat_id: '-1001234567890' },
          cdc: { min_chunk_size: 131072, avg_chunk_size: 524288, max_chunk_size: 2097152 },
          dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
        },
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByLabelText('服务器监听地址')).toHaveValue('10.0.0.1')
      })

      const input = screen.getByLabelText('服务器监听地址')
      await user.clear(input)
      await user.type(input, '127.0.0.1')
      expect(input).toHaveValue('127.0.0.1')

      await user.click(screen.getByText('重置'))

      await waitFor(() => {
        expect(screen.getByLabelText('服务器监听地址')).toHaveValue('10.0.0.1')
      })
    })
  })

  describe('retention settings', () => {
    it('renders max versions input', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await waitFor(() => {
        const input = screen.getByLabelText('最大版本数')
        expect(input).toBeTruthy()
      })
    })

    it('renders max age input', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await waitFor(() => {
        const input = screen.getByLabelText('最大保留时间')
        expect(input).toBeTruthy()
      })
    })

    it('allows editing max versions', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await waitFor(() => {
        expect(screen.getByLabelText('最大版本数')).toHaveValue(100)
      })

      const input = screen.getByLabelText('最大版本数')
      await user.clear(input)
      await user.type(input, '50')

      expect(input).toHaveValue(50)
    })
  })

  describe('WebDAV settings', () => {
    it('renders WebDAV enabled toggle', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByRole('switch', { name: '启用 WebDAV' })).toBeTruthy()
      })
    })

    it('renders WebDAV prefix input', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByLabelText('WebDAV URL 前缀')).toHaveValue('/dav')
      })
    })

    it('rejects reserved WebDAV prefixes before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByLabelText('WebDAV URL 前缀')).toHaveValue('/dav')
      })

      fireEvent.change(screen.getByLabelText('WebDAV URL 前缀'), { target: { value: '/api/v1' } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'WebDAV 前缀不可用',
          description: 'WebDAV 前缀不能是 /、/api、/s、/health 或它们的子路径',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('rejects malformed WebDAV prefixes before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByLabelText('WebDAV URL 前缀')).toHaveValue('/dav')
      })

      fireEvent.change(screen.getByLabelText('WebDAV URL 前缀'), { target: { value: '/dav\\files' } })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'WebDAV 前缀格式无效',
          description: 'WebDAV 前缀只能是 URL 路径，不能包含反斜杠、?、# 或控制字符',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('renders read-only toggle', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByText('只读模式')).toBeTruthy()
      })
    })

    it('renders username input', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByLabelText('WebDAV 用户名')).toHaveValue('admin')
      })
    })

    it('exposes accessible labels for WebDAV credential action buttons', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByText('WebDAV 访问凭据')).toBeTruthy()
      })

      expect(screen.getAllByRole('button', { name: '复制 WebDAV 地址' }).length).toBeGreaterThan(0)
      expect(screen.getByRole('button', { name: '复制 WebDAV 用户名' })).toBeTruthy()
      const showPasswordButton = screen.getByRole('button', { name: '显示 WebDAV 密码' })
      expect(showPasswordButton).toBeTruthy()
      expect(screen.getByRole('button', { name: '复制 WebDAV 密码' })).toBeTruthy()

      await user.click(showPasswordButton)

      expect(screen.getByRole('button', { name: '隐藏 WebDAV 密码' })).toBeTruthy()
    })

    it('copies WebDAV credential values and clears the copied indicator', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })

      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByText('WebDAV 访问凭据')).toBeTruthy()
      })

      vi.useFakeTimers()
      try {
        await act(async () => {
          fireEvent.click(screen.getByRole('button', { name: '复制 WebDAV 地址' }))
          await Promise.resolve()
        })

        expect(writeText).toHaveBeenCalledWith(expect.stringContaining('/dav/'))

        await act(async () => {
          fireEvent.click(screen.getByRole('button', { name: '复制 WebDAV 用户名' }))
          await Promise.resolve()
        })

        expect(writeText).toHaveBeenCalledWith('admin')

        await act(async () => {
          fireEvent.click(screen.getByRole('button', { name: '复制 WebDAV 密码' }))
          await Promise.resolve()
        })

        expect(writeText).toHaveBeenCalledWith('secret')

        act(() => {
          vi.runOnlyPendingTimers()
        })
      } finally {
        vi.useRealTimers()
      }
    })

    it('shows a toast when copying WebDAV credentials fails', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText: vi.fn().mockRejectedValue(new Error('blocked')) },
      })

      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByText('WebDAV 访问凭据')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '复制 WebDAV 用户名' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '复制失败', color: 'danger' })
      })
    })

    it('shows a retryable warning when WebDAV credentials fail to load', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetWebDAVCredentials
        .mockRejectedValueOnce(new Error('webdav credentials unavailable'))
        .mockResolvedValueOnce({
          enabled: true,
          url: '/dav/',
          auth_type: 'basic',
          username: 'admin',
          password: 'secret',
        })

      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByText('WebDAV 凭据加载失败')).toBeTruthy()
        expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载凭据' })).toBeTruthy()
        expect(screen.getByText('WebDAV 服务')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载凭据' }))

      await waitFor(() => {
        expect(screen.queryByText('WebDAV 凭据加载失败')).toBeNull()
        expect(screen.getByText('WebDAV 访问凭据')).toBeTruthy()
        expect(mockAddToast).toHaveBeenCalledWith({ title: 'WebDAV 凭据已刷新', color: 'success' })
      })
    })

    it('shows an unavailable warning when WebDAV credentials return service unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetWebDAVCredentials.mockRejectedValueOnce(new SettingsError('webdav credentials unavailable', 503, 'SERVICE_UNAVAILABLE'))

      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByText('WebDAV 凭据暂不可用')).toBeTruthy()
        expect(screen.getByText('当前无法读取运行中的 WebDAV 凭据，请检查设备状态或稍后重试。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载凭据' })).toBeTruthy()
      })
    })

    it('shows warning toast when reloading WebDAV credentials becomes unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetWebDAVCredentials.mockRejectedValueOnce(new Error('webdav credentials unavailable'))

      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载凭据' })).toBeTruthy()
      })

      mockGetWebDAVCredentials.mockRejectedValueOnce(new SettingsError('webdav credentials unavailable', 503, 'SERVICE_UNAVAILABLE'))
      await user.click(screen.getByRole('button', { name: '重新加载凭据' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'WebDAV 凭据暂不可用',
          description: '当前无法读取运行中的 WebDAV 凭据，请检查设备状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('shows a generic toast when reloading WebDAV credentials fails with an unknown error', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetWebDAVCredentials.mockRejectedValueOnce(new Error('webdav credentials unavailable'))

      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载凭据' })).toBeTruthy()
      })

      mockGetWebDAVCredentials.mockRejectedValueOnce('webdav offline')
      await user.click(screen.getByRole('button', { name: '重新加载凭据' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '刷新失败',
          description: '操作未完成，请稍后重试。',
          color: 'danger',
        })
      })
    })
  })

  describe('advanced settings', () => {
    it('renders CDC info box', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('关于 CDC 分块')).toBeTruthy()
      })
    })

    it('renders chunk size inputs', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByLabelText('最小块大小')).toHaveValue('256 KB')
        expect(screen.getByLabelText('平均块大小')).toHaveValue('1 MB')
        expect(screen.getByLabelText('最大块大小')).toHaveValue('4 MB')
      })
    })

    it('shows gRPC connection info', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('gRPC 地址')).toBeTruthy()
        expect(screen.getByLabelText('数据面 gRPC 地址')).toHaveValue('127.0.0.1:9090')
        expect(screen.getByLabelText('数据面连接超时')).toHaveValue('30s')
        expect(screen.getByLabelText('数据面最大重试次数')).toHaveValue(3)
      })
    })

    it('allows editing CDC chunk sizes and saves them', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('CDC 分块参数')).toBeTruthy()
      })

      fireEvent.change(screen.getByLabelText('最小块大小'), { target: { value: '512KB' } })
      fireEvent.change(screen.getByLabelText('平均块大小'), { target: { value: '2MB' } })
      fireEvent.change(screen.getByLabelText('最大块大小'), { target: { value: '8MB' } })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          cdc: expect.objectContaining({
            min_chunk_size: 524288,
            avg_chunk_size: 2097152,
            max_chunk_size: 8388608,
          }),
        }))
      })
    })
  })

  describe('actions', () => {
  it('shows danger toast and skips save for out-of-range server ports', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    const portInput = await screen.findByLabelText('服务器端口')
    await user.clear(portInput)
    await user.type(portInput, '70000')
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '端口格式无效',
        description: '端口必须是 1 到 65535 之间的整数',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for invalid server host', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    const hostInput = await screen.findByLabelText('服务器监听地址')
    fireEvent.change(hostInput, { target: { value: '[::1]:8080' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '监听地址格式无效',
        description: '监听地址必须为空、*、合法主机名、IPv4 或 IPv6，且不能包含端口、空白或控制字符',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for blank max versions', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '版本保留')

    await waitFor(() => {
      expect(screen.getByLabelText('最大版本数')).toHaveValue(100)
    })

    const maxVersionsInput = screen.getByLabelText('最大版本数')
    await user.clear(maxVersionsInput)
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '最大版本数格式无效',
        description: '最大版本数必须是 0 或不超过安全范围的整数',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it.each([
    ['trusted proxy hops', '常规', '受信代理层数', undefined, '受信代理层数格式无效', '受信代理层数必须是 0 或不超过安全范围的整数'],
    ['max versions', '版本保留', '最大版本数', undefined, '最大版本数格式无效', '最大版本数必须是 0 或不超过安全范围的整数'],
    ['dataplane max retries', '高级', '数据面最大重试次数', undefined, '最大重试次数格式无效', '最大重试次数必须是 0 或不超过安全范围的整数'],
    ['scrub max retries', '高级', 'Scrub 最大重试次数', '启用周期 Scrub', 'Scrub 重试次数格式无效', '最大重试次数必须是 0 或不超过安全范围的整数'],
    ['trash retention days', '版本保留', '回收站保留天数', undefined, '回收站保留天数格式无效', '回收站保留天数必须是 0 或不超过安全范围的整数'],
  ])('shows danger toast and skips save for unsafe %s', async (_label, tab, inputLabel, switchLabel, title, description) => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, tab)
    if (switchLabel) {
      await user.click(await screen.findByRole('switch', { name: switchLabel }))
    }

    const input = await screen.findByLabelText(inputLabel)
    fireEvent.change(input, { target: { value: '9007199254740992' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title,
        description,
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for invalid CDC chunk ordering', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await waitFor(() => {
      expect(screen.getByLabelText('平均块大小')).toHaveValue('1 MB')
    })

    const avgChunkInput = screen.getByLabelText('平均块大小')
    await user.clear(avgChunkInput)
    await user.type(avgChunkInput, '128 KB')
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: 'CDC 分块参数无效',
        description: '请保持最小块大小 < 平均块大小 < 最大块大小',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for invalid byte sizes', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '版本保留')

    await waitFor(() => {
      expect(screen.getByLabelText('最小空闲空间')).toHaveValue('10 GB')
    })

    fireEvent.change(screen.getByLabelText('最小空闲空间'), { target: { value: 'not-a-size' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '大小格式无效',
        description: '请使用 1024、1 KB、1.5 MB 之类的格式。',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it.each([
    ['minimum free space', '版本保留', '最小空闲空间', undefined, '最小空闲空间必须是 0 或不超过安全范围的整数'],
    ['trash max size', '版本保留', '回收站最大容量', undefined, '回收站最大容量必须是大于 0 且不超过安全范围的整数'],
    ['versioning max size', '版本保留', '最大自动版本化文件大小', undefined, '最大自动版本化文件大小必须是大于 0 且不超过安全范围的整数'],
    ['alert min free space', '高级', '最小剩余空间', '启用提醒', '提醒最小剩余空间必须是 0 或不超过安全范围的整数'],
  ])('shows danger toast and skips save for unsafe %s', async (_label, tab, inputLabel, switchLabel, description) => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, tab)
    if (switchLabel) {
      await user.click(await screen.findByRole('switch', { name: switchLabel }))
    }

    const input = await screen.findByLabelText(inputLabel)
    fireEvent.change(input, { target: { value: '9007199254740992 B' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '大小格式无效',
        description,
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for non-positive CDC chunk sizes', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await waitFor(() => {
      expect(screen.getByLabelText('最小块大小')).toHaveValue('256 KB')
    })

    fireEvent.change(screen.getByLabelText('最小块大小'), { target: { value: '0' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: 'CDC 分块参数无效',
        description: '最小、平均和最大块大小都必须大于 0',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for CDC min chunk below safety floor', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await waitFor(() => {
      expect(screen.getByLabelText('最小块大小')).toHaveValue('256 KB')
    })

    fireEvent.change(screen.getByLabelText('最小块大小'), { target: { value: '32 KB' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: 'CDC 分块参数无效',
        description: '最小块大小不能小于 64 KB',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for CDC max chunk above safety cap', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await waitFor(() => {
      expect(screen.getByLabelText('最大块大小')).toHaveValue('4 MB')
    })

    fireEvent.change(screen.getByLabelText('最大块大小'), { target: { value: '65 MB' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: 'CDC 分块参数无效',
        description: '最大块大小不能超过 64 MB',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for blank server read timeout', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    fireEvent.change(await screen.findByLabelText('服务器读取超时'), { target: { value: '' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '读取超时格式无效',
        description: '读取超时不能为空',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for blank server write timeout', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    fireEvent.change(await screen.findByLabelText('服务器写入超时'), { target: { value: '' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '写入超时格式无效',
        description: '写入超时不能为空',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for blank server idle timeout', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    fireEvent.change(await screen.findByLabelText('服务器空闲超时'), { target: { value: '' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '空闲超时格式无效',
        description: '空闲超时不能为空',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for invalid server read timeout duration', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    fireEvent.change(await screen.findByLabelText('服务器读取超时'), { target: { value: 'soon' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '读取超时格式无效',
        description: '读取超时必须使用 30s / 1m 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for invalid retention max age duration', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '版本保留')

    await waitFor(() => {
      expect(screen.getByLabelText('最大保留时间')).toHaveValue('8760h')
    })

    fireEvent.change(screen.getByLabelText('最大保留时间'), { target: { value: 'forever' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '最大保留时间格式无效',
        description: '最大保留时间必须是 0，或使用 2160h / 30m 这类 Go duration 格式',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for invalid dataplane retries', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    fireEvent.change(await screen.findByLabelText('数据面最大重试次数'), { target: { value: '-1' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '最大重试次数格式无效',
          description: '最大重试次数必须是 0 或不超过安全范围的整数',
          color: 'danger',
        })
      })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for zero alert check interval', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
    fireEvent.change(screen.getByLabelText('提醒检查间隔'), { target: { value: '0' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '提醒检查间隔格式无效',
        description: '检查间隔必须使用 1h / 30m 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for invalid scrub schedule interval', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await waitFor(() => {
      expect(screen.getByLabelText('Scrub 常规间隔')).toBeTruthy()
    })

    fireEvent.change(screen.getByLabelText('Scrub 常规间隔'), { target: { value: 'daily' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: 'Scrub 周期间隔格式无效',
        description: '周期 Scrub 的常规间隔必须使用 168h / 1h 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for blank dataplane timeout', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    fireEvent.change(await screen.findByLabelText('数据面连接超时'), { target: { value: '' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '数据面超时格式无效',
        description: '连接超时不能为空',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for invalid dataplane grpc address', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    fireEvent.change(await screen.findByLabelText('数据面 gRPC 地址'), { target: { value: '127.0.0.1:70000' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '数据面地址格式无效',
        description: 'gRPC 地址必须是合法的 host:port，端口为 1 到 65535，且不能包含空白或控制字符',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for out-of-range alert threshold', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
    fireEvent.change(screen.getByLabelText('提醒阈值'), { target: { value: '120' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '提醒阈值格式无效',
        description: '提醒阈值必须是 0 到 100 之间的整数',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for fractional alert threshold', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
    fireEvent.change(screen.getByLabelText('提醒阈值'), { target: { value: '90.5' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '提醒阈值格式无效',
        description: '提醒阈值必须是 0 到 100 之间的整数',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for blank alert threshold', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
    fireEvent.change(screen.getByLabelText('提醒阈值'), { target: { value: '' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '提醒阈值格式无效',
        description: '提醒阈值必须是 0 到 100 之间的整数',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for blank alert check interval', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
    fireEvent.change(screen.getByLabelText('提醒检查间隔'), { target: { value: '' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '提醒检查间隔格式无效',
        description: '检查间隔不能为空',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for blank alert cooldown', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
    fireEvent.change(screen.getByLabelText('提醒冷却时间'), { target: { value: '' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '提醒冷却时间格式无效',
        description: '冷却时间不能为空',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for out-of-range critical alert threshold', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
    fireEvent.change(screen.getByLabelText('严重提醒阈值'), { target: { value: '120' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '严重提醒阈值格式无效',
        description: '严重提醒阈值必须是 0 到 100 之间的整数',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for fractional critical alert threshold', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
    fireEvent.change(screen.getByLabelText('严重提醒阈值'), { target: { value: '95.5' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '严重提醒阈值格式无效',
        description: '严重提醒阈值必须是 0 到 100 之间的整数',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for blank critical alert threshold', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
    fireEvent.change(screen.getByLabelText('严重提醒阈值'), { target: { value: '' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '严重提醒阈值格式无效',
        description: '严重提醒阈值必须是 0 到 100 之间的整数',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save when critical alert threshold is lower than warning threshold', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
    fireEvent.change(screen.getByLabelText('提醒阈值'), { target: { value: '96' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '提醒阈值关系无效',
        description: '严重提醒阈值不能小于普通提醒阈值',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it.each([
    ['embedded credentials', 'https://operator@nas.example.com'],
    ['query parameters', 'https://nas.example.com?token=secret'],
    ['empty query marker', 'https://nas.example.com?'],
    ['URL fragments', 'https://nas.example.com#share'],
    ['empty fragment marker', 'https://nas.example.com#'],
    ['invalid host labels', 'https://nas..example.com'],
    ['spaces', 'https://nas.example.com/base path'],
    ['host-relative backslash path', 'https://nas.example.com\\shares'],
    ['backslash path', 'https://nas.example.com/shares\\team'],
    ['escaped backslash path', 'https://nas.example.com/shares%5Cteam'],
    ['duplicate path slashes', 'https://nas.example.com/shares//team'],
    ['escaped duplicate path slashes', 'https://nas.example.com/shares%2F%2Fteam'],
    ['dot segment path', 'https://nas.example.com/shares/./team'],
    ['escaped dot segment path', 'https://nas.example.com/shares/%2e%2e/team'],
  ])('shows danger toast and skips save for invalid share base URL with %s', async (_label, baseURL) => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '分享')

    await user.click(await screen.findByRole('switch'))
    fireEvent.change(screen.getByLabelText('分享基础 URL'), { target: { value: baseURL } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '分享基础 URL 无效',
        description: '分享基础 URL 必须为空，或使用不含 userinfo、查询参数、片段、反斜杠、重复路径斜杠、. 或 .. 路径段且主机名有效的 http/https 地址',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it.each([
    [
      'HTTP URL',
      'http://nas.example.com',
      '公网分享建议使用 HTTPS 基础 URL；HTTP 链接只适用于内网或受控测试环境。',
    ],
    [
      'non-default HTTPS port',
      'https://nas.example.com:8443/base',
      'HTTPS 非标准端口需要额外公网入口和防火墙规则；公网分享建议使用默认 443 端口。',
    ],
    [
      'host-relative backslash path',
      'https://nas.example.com\\shares',
      '路径包含反斜杠；公网部署中代理或浏览器可能规范化为不同的分享地址。',
    ],
    [
      'backslash path',
      'https://nas.example.com/shares\\team',
      '路径包含反斜杠；公网部署中代理或浏览器可能规范化为不同的分享地址。',
    ],
    [
      'escaped backslash path',
      'https://nas.example.com/shares%5Cteam',
      '路径包含反斜杠；公网部署中代理或浏览器可能规范化为不同的分享地址。',
    ],
    [
      'duplicate path slashes',
      'https://nas.example.com/shares//team',
      '路径包含重复斜杠；公网部署中代理或浏览器可能规范化为不同的分享地址。',
    ],
    [
      'escaped duplicate path slashes',
      'https://nas.example.com/shares%2F%2Fteam',
      '路径包含重复斜杠；公网部署中代理或浏览器可能规范化为不同的分享地址。',
    ],
    [
      'dot segment path',
      'https://nas.example.com/shares/./team',
      '路径包含 . 或 .. 路径段；公网部署中代理或浏览器可能规范化为不同的分享地址。',
    ],
    [
      'escaped dot segment path',
      'https://nas.example.com/shares/%2e%2e/team',
      '路径包含 . 或 .. 路径段；公网部署中代理或浏览器可能规范化为不同的分享地址。',
    ],
    [
      'embedded share route',
      'https://nas.example.com/s/',
      '基础 URL 已包含 /s 分享路由，生成的链接会出现重复的 /s/s。',
    ],
    [
      'escaped embedded share route',
      'https://nas.example.com/base%2Fs',
      '基础 URL 已包含 /s 分享路由，生成的链接会出现重复的 /s/s。',
    ],
  ])('shows an inline public review warning for share base URL with %s', async (_label, baseURL, warning) => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '分享')

    await user.click(await screen.findByRole('switch', { name: '启用分享功能' }))
    fireEvent.change(screen.getByLabelText('分享基础 URL'), { target: { value: baseURL } })

    await waitFor(() => {
      expect(screen.getByText(warning)).toBeTruthy()
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for invalid alert webhook URL', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
    fireEvent.change(screen.getByLabelText('Webhook URL'), { target: { value: 'ftp://hooks.example.com/storage' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: 'Webhook URL 无效',
        description: 'Webhook URL 必须为空，或使用 http/https 的完整地址',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for unsupported alert webhook methods', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockGetSettings.mockResolvedValueOnce({
      data: {
        ...defaultSettingsResponse.data,
        alerts: {
          ...defaultSettingsResponse.data.alerts,
          enabled: true,
          webhook_method: 'PATCH',
        },
      },
    })

    render(<SettingsPage />)

    await waitFor(() => {
      expect(screen.getByText('保存设置')).toBeTruthy()
    })

    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: 'Webhook 方法无效',
        description: 'Webhook 方法必须是 GET 或 POST',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for invalid trash retention days', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '版本保留')

    await waitFor(() => {
      expect(screen.getByLabelText('回收站保留天数')).toBeTruthy()
    })

    fireEvent.change(screen.getByLabelText('回收站保留天数'), { target: { value: '-1' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '回收站保留天数格式无效',
          description: '回收站保留天数必须是 0 或不超过安全范围的整数',
          color: 'danger',
        })
      })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for malformed alert webhook headers', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
    fireEvent.change(screen.getByLabelText('Webhook 自定义 Header'), { target: { value: 'BrokenHeader' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: 'Webhook Header 格式无效',
        description: '每行必须使用合法的 HTTP Header 名称和值',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

    it('shows loading state on save', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      const saveBtn = await screen.findByRole('button', { name: '保存设置' })
      await user.click(saveBtn)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '保存设置' })).toBeTruthy()
      })
    })

    it('shows success toast on reset after refetch succeeds', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('重置')).toBeTruthy()
      })

      const resetBtn = screen.getByText('重置')
      await user.click(resetBtn)

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '已恢复为服务端当前配置', color: 'success' })
      })
    })

    it('shows danger toast on reset when refetch fails', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings
        .mockResolvedValueOnce({
          data: {
            server: { host: '0.0.0.0', port: 8080, read_timeout: '30s', write_timeout: '60s', idle_timeout: '120s', read_timeout_seconds: 60, write_timeout_seconds: 300 },
            storage: { root: '~/.mnemonas' },
            auth: { enabled: true, access_token_ttl: '15m0s', refresh_token_ttl: '168h0m0s' },
            trash: { enabled: true, retention_days: 30, max_size: 10737418240 },
            retention: { max_versions: 100, max_age: '8760h', min_free_space: 10737418240, gc_interval: '24h' },
            versioning: { auto_versioned_extensions: ['.md', '.txt', '.go'], auto_versioned_filenames: ['README', 'Dockerfile', 'Makefile'], max_versioned_size: 104857600 },
            webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
            share: { enabled: false, base_url: '' },
            favorites: { enabled: true },
            alerts: { enabled: false, check_interval: '1h', threshold_pct: 90, critical_pct: 95, min_free_bytes: 10737418240, cooldown_period: '4h', webhook_url: '', webhook_method: 'POST', webhook_headers: [], telegram_enabled: false, telegram_bot_token_configured: false, telegram_chat_id: '' },
            cdc: { min_chunk_size: 262144, avg_chunk_size: 1048576, max_chunk_size: 4194304 },
            dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
          },
        })
        .mockRejectedValueOnce(new Error('Network error'))

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('重置')).toBeTruthy()
      })

      await user.click(screen.getByText('重置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '重置失败',
          description: '操作未完成，请稍后重试。',
          color: 'danger',
        })
      })
    })

    it('shows unavailable toast on reset when settings service is unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings
        .mockResolvedValueOnce(defaultSettingsResponse)
        .mockRejectedValueOnce(new SettingsError('settings not available', 503, 'SERVICE_UNAVAILABLE'))

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('重置')).toBeTruthy()
      })

      await user.click(screen.getByText('重置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '重置暂不可用',
          description: '设置当前不可用，请检查设备状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('shows unavailable toast when saving fails because settings service is unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUpdateSettings.mockRejectedValueOnce(new SettingsError('settings not available', 503, 'SERVICE_UNAVAILABLE'))

      render(<SettingsPage />)
      await waitFor(() => {
        expect(screen.getByText('保存设置')).toBeTruthy()
      })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '保存设置暂不可用',
          description: '设置当前不可用，请检查设备状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('shows a validation warning for normalized WebDAV username conflict messages', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUpdateSettings.mockRejectedValueOnce(
        new SettingsError('  WebDAV.Username Must Not Match A Non-Admin User when auth is enabled  ', 400)
      )

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('保存设置')).toBeTruthy()
      })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'WebDAV 用户名不可用',
          description: '当前 WebDAV 用户名与现有非管理员账号冲突，请改用管理员账号或其他专用用户名。',
          color: 'warning',
        })
      })
    })
  })

  describe('error state', () => {
    it('shows an unavailable state when the settings service returns 503', async () => {
      mockGetSettings.mockRejectedValueOnce(new SettingsError('settings not available', 503, 'SERVICE_UNAVAILABLE'))

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('设置服务暂不可用')).toBeTruthy()
        expect(screen.getByText('设置当前不可用，请检查设备状态或稍后重试。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('shows retryable error state when initial settings load fails', async () => {
      mockGetSettings.mockRejectedValueOnce(new Error('Network error'))

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('加载设置失败')).toBeTruthy()
        expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('retries loading settings from the error state', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings
        .mockRejectedValueOnce(new Error('Network error'))
        .mockResolvedValueOnce({
          data: {
            server: { host: '0.0.0.0', port: 8080, read_timeout: '30s', write_timeout: '60s', idle_timeout: '120s', read_timeout_seconds: 60, write_timeout_seconds: 300 },
            storage: { root: '~/.mnemonas' },
            auth: { enabled: true, access_token_ttl: '15m0s', refresh_token_ttl: '168h0m0s' },
            trash: { enabled: true, retention_days: 30, max_size: 10737418240 },
            retention: { max_versions: 100, max_age: '8760h', min_free_space: 10737418240, gc_interval: '24h' },
            versioning: { auto_versioned_extensions: ['.md', '.txt', '.go'], auto_versioned_filenames: ['README', 'Dockerfile', 'Makefile'], max_versioned_size: 104857600 },
            webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
            share: { enabled: false, base_url: '' },
            favorites: { enabled: true },
            alerts: { enabled: false, check_interval: '1h', threshold_pct: 90, critical_pct: 95, min_free_bytes: 10737418240, cooldown_period: '4h', webhook_url: '', webhook_method: 'POST', webhook_headers: [], telegram_enabled: false, telegram_bot_token_configured: false, telegram_chat_id: '' },
            cdc: { min_chunk_size: 262144, avg_chunk_size: 1048576, max_chunk_size: 4194304 },
            dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
          },
        })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(screen.getByText('设置')).toBeTruthy()
        expect(mockAddToast).toHaveBeenCalledWith({ title: '设置已刷新', color: 'success' })
      })
    })

    it('shows warning toast when settings reload becomes unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings
        .mockRejectedValueOnce(new Error('Network error'))
        .mockRejectedValueOnce(new SettingsError('settings not available', 503, 'SERVICE_UNAVAILABLE'))

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '设置服务暂不可用',
          description: '设置当前不可用，请检查设备状态或稍后重试。',
          color: 'warning',
        })
      })
    })
  })

})
