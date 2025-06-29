import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import { act, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SettingsPage } from './Settings'
import * as HeroUI from '@heroui/react'

const mockAddToast = vi.fn()

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
    trash: { enabled: true, retention_days: 30, max_size: 10737418240 },
      retention: { max_versions: 100, max_age: '8760h', min_free_space: 10737418240, gc_interval: '24h' },
      versioning: { auto_versioned_extensions: ['.md', '.txt', '.go'], auto_versioned_filenames: ['README', 'Dockerfile', 'Makefile'], max_versioned_size: 104857600 },
      webdav: { enabled: true, runtime_enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
      share: { enabled: false, base_url: '' },
      favorites: { enabled: true, runtime_available: true },
      alerts: { enabled: false, check_interval: '1h', threshold_pct: 90, critical_pct: 95, min_free_bytes: 10737418240, cooldown_period: '4h', webhook_url: '', webhook_method: 'POST', webhook_headers: [], telegram_enabled: false, telegram_bot_token_configured: false, telegram_chat_id: '', email_enabled: false, smtp_host: '', smtp_port: 587, smtp_username: '', smtp_password_configured: false, smtp_from: '', smtp_to: [] },
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

  it('passes abort signals to the WebDAV credentials query', async () => {
    window.history.pushState({}, '', '/settings?tab=webdav')

    render(<SettingsPage />)

    await waitFor(() => {
      expectCalledWithOnlyAbortSignal(mockGetWebDAVCredentials)
    })
  })

  it('passes an abort signal when saving settings', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await waitFor(() => {
      expect(screen.getByDisplayValue('8080')).toBeTruthy()
    })

    const portInput = screen.getByDisplayValue('8080')
    await user.clear(portInput)
    await user.type(portInput, '9000')
    await user.click(screen.getByText('保存设置'))

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

    await waitFor(() => {
      expect(screen.getByDisplayValue('8080')).toBeTruthy()
    })

    const portInput = screen.getByDisplayValue('8080')
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
        expect(screen.getByText('服务器')).toBeTruthy()
        expect(screen.getByText('存储路径')).toBeTruthy()
      })
    })

    it('applies public access recommendations to the settings form', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('公网访问向导')).toBeTruthy()
        expect(screen.getByDisplayValue('0.0.0.0')).toBeTruthy()
      })

      fireEvent.change(screen.getByLabelText('公网域名'), { target: { value: 'nas.example.com' } })
      await user.click(screen.getByRole('button', { name: '应用推荐到表单' }))

      expect(screen.getByDisplayValue('127.0.0.1')).toBeTruthy()
      expect(screen.getByLabelText('受信代理层数')).toHaveValue(1)
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已应用公网访问推荐',
      }))
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

    it('rejects public access domains that include a path or port', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('公网访问向导')).toBeTruthy()
      })

      fireEvent.change(screen.getByLabelText('公网域名'), { target: { value: 'https://nas.example.com:8443/path' } })

      expect(screen.getByText('请输入域名，不要包含路径或端口')).toBeTruthy()
      expect(screen.queryByText('https://nas.example.com')).toBeNull()
      expect(screen.getByRole('button', { name: '应用推荐到表单' })).toBeDisabled()

      await user.click(screen.getByRole('button', { name: '应用推荐到表单' }))

      expect(screen.queryByDisplayValue('https://nas.example.com')).toBeNull()
    })

    it('rejects public access domains with invalid hostname labels', async () => {
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('公网访问向导')).toBeTruthy()
      })

      fireEvent.change(screen.getByLabelText('公网域名'), { target: { value: 'bad_domain.example.com' } })

      expect(screen.getByText('请输入有效域名，域名标签只能包含字母、数字和连字符，且不能以连字符开头或结尾')).toBeTruthy()
      expect(screen.queryByText('https://bad_domain.example.com')).toBeNull()
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

      expect(screen.getByDisplayValue('127.0.0.1')).toBeTruthy()
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '已改为本机监听',
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

      expect(screen.getByDisplayValue('127.0.0.1')).toBeTruthy()
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
              message: '公开分享链接应使用 HTTPS 默认端口 443。',
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

      expect(screen.getByDisplayValue('0.0.0.0')).toBeTruthy()
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

      expect(screen.getByDisplayValue('0.0.0.0')).toBeTruthy()
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

      await user.click(screen.getByRole('button', { name: '查看处理方式' }))

      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '需要编辑配置文件',
        description: expect.stringContaining('allow_unsafe_no_auth'),
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

      await user.click(screen.getByRole('button', { name: '查看处理方式' }))

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
              message: '建议创建一个备用管理员账号。',
            },
          ],
          request: { scheme: 'https' },
          config: { active_admins: 1 },
        },
      })
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('只有一个启用中的管理员')).toBeTruthy()
      })

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

      await waitFor(() => {
        expect(screen.getByDisplayValue('8080')).toBeTruthy()
      })

      const portInput = screen.getByDisplayValue('8080')
      expect(portInput).toHaveAttribute('type', 'number')
      expect(portInput).toHaveAttribute('min', '1')
      expect(portInput).toHaveAttribute('max', '65535')

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByDisplayValue('3')).toBeTruthy()
      })

      const maxRetriesInput = screen.getByDisplayValue('3')
      expect(maxRetriesInput).toHaveAttribute('type', 'number')
      expect(maxRetriesInput).toHaveAttribute('min', '0')
    })

    it('uses url inputs for alert webhook and share base addresses', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      const alertsWebhookInput = await screen.findByPlaceholderText('https://hooks.example.com/alert')
      expect(alertsWebhookInput).toHaveAttribute('type', 'url')

      await openTab(user, '分享')

      const shareBaseUrlInput = await screen.findByPlaceholderText('https://nas.example.com')
      expect(shareBaseUrlInput).toHaveAttribute('type', 'url')
    })

    it('shows example duration placeholders for retention and alert timing settings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      expect(await screen.findByPlaceholderText('2160h')).toBeTruthy()
      expect(screen.getByPlaceholderText('24h')).toBeTruthy()

      await openTab(user, '高级')

      expect(await screen.findByPlaceholderText('30s')).toBeTruthy()
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
      fireEvent.change(screen.getByDisplayValue('/dav'), { target: { value: 'remote' } })
      await user.click(switches[1])
      fireEvent.change(screen.getByDisplayValue('admin'), { target: { value: 'webdav-admin' } })
      fireEvent.change(screen.getByPlaceholderText('••••••••'), { target: { value: 'new-secret' } })

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

    it('clears newly saved WebDAV password while waiting for refreshed settings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings
        .mockResolvedValueOnce(defaultSettingsResponse)
        .mockImplementationOnce(() => new Promise(() => {}))
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      const passwordInput = await screen.findByPlaceholderText('••••••••')
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
        expect(screen.queryByDisplayValue('new-webdav-secret')).toBeNull()
        expect(screen.getByPlaceholderText('••••••••')).toHaveValue('')
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

      await waitFor(() => {
        expect(screen.getByDisplayValue('127.0.0.1:9090')).toBeTruthy()
      })

      const grpcInput = screen.getByDisplayValue('127.0.0.1:9090')
      fireEvent.change(grpcInput, { target: { value: '10.0.0.2:9091' } })

      const timeoutInput = screen.getByDisplayValue('30s')
      fireEvent.change(timeoutInput, { target: { value: '45s' } })

      const retriesInput = screen.getByDisplayValue('3')
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
      const baseUrlInput = screen.getByPlaceholderText('https://nas.example.com')
      await user.type(baseUrlInput, 'https://share.example.com')
      await user.clear(screen.getByPlaceholderText('168h'))
      await user.type(screen.getByPlaceholderText('168h'), '24h')
      await user.clear(screen.getByPlaceholderText('0'))
      await user.type(screen.getByPlaceholderText('0'), '3')
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

    it('rejects invalid share default policy values before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '分享')

      await user.click(screen.getByRole('switch'))
      await user.clear(screen.getByPlaceholderText('168h'))
      await user.type(screen.getByPlaceholderText('168h'), '7d')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '分享默认有效期无效',
          description: '默认有效期必须为空、0，或使用 168h / 30m 这类 Go duration 格式',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()

      await user.clear(screen.getByPlaceholderText('168h'))
      await user.type(screen.getByPlaceholderText('168h'), '24h')
      await user.clear(screen.getByPlaceholderText('0'))
      await user.type(screen.getByPlaceholderText('0'), '-1')
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
      await user.clear(screen.getByPlaceholderText('0'))
      await user.type(screen.getByPlaceholderText('0'), maxAccess)
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
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '分享路径策略格式无效',
          description: '第 1 行至少需要一个约束',
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

      const certInput = screen.getByPlaceholderText('/path/to/server.crt')
      fireEvent.change(certInput, { target: { value: '/etc/mnemonas/tls/server.crt' } })

      const keyInput = screen.getByPlaceholderText('/path/to/server.key')
      fireEvent.change(keyInput, { target: { value: '/etc/mnemonas/tls/server.key' } })

      const certDirInput = screen.getByPlaceholderText('<storage.root>/.mnemonas/certs')
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

      await user.click(screen.getAllByRole('switch')[0])
      fireEvent.change(screen.getByPlaceholderText('/path/to/server.crt'), { target: { value: '/etc/mnemonas/tls/server.crt' } })
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

      await user.click(screen.getAllByRole('switch')[0])
      fireEvent.change(screen.getByPlaceholderText('/path/to/server.crt'), { target: { value: '/etc/mnemonas/tls/server.pem' } })
      fireEvent.change(screen.getByPlaceholderText('/path/to/server.key'), { target: { value: '/etc/mnemonas/tls/server.pem' } })
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

      const thresholdInput = screen.getByDisplayValue('90')
      fireEvent.change(thresholdInput, { target: { value: '85' } })

      const criticalInput = screen.getByDisplayValue('95')
      fireEvent.change(criticalInput, { target: { value: '92' } })

      const minFreeInput = screen.getByDisplayValue('10 GB')
      fireEvent.change(minFreeInput, { target: { value: '20GB' } })

      const cooldownInput = screen.getByLabelText('提醒冷却时间')
      fireEvent.change(cooldownInput, { target: { value: '2h' } })

      const webhookInput = screen.getByPlaceholderText('https://hooks.example.com/alert')
      fireEvent.change(webhookInput, { target: { value: 'https://hooks.example.com/storage' } })

      fireEvent.change(screen.getByLabelText('Webhook 方法'), { target: { value: 'GET' } })

      const headersInput = screen.getByLabelText('Webhook 自定义 Header')
      fireEvent.change(headersInput, { target: { value: 'Authorization: Bearer token\nX-MnemoNAS: alerts' } })

      fireEvent.click(screen.getByRole('switch', { name: '启用 Telegram 通知' }))
      fireEvent.change(screen.getByLabelText('Telegram Bot Token'), { target: { value: '123456:secret-token' } })
      fireEvent.change(screen.getByLabelText('Telegram Chat ID'), { target: { value: '-1001234567890' } })

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
        data: { event_type: 'alert_test', channels: ['webhook', 'telegram', 'email'] },
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
        description: '已发送到 Webhook / Telegram / SMTP 邮件',
        color: 'success',
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
        description: '请至少配置 Webhook、Telegram 或邮件通道并保存后再发送测试提醒。',
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
        description: '请至少配置 Webhook、Telegram 或邮件通道并保存后再发送测试提醒。',
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
          },
        },
      })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByDisplayValue('<redacted>')).toBeTruthy()
      })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expectUpdateSettingsCalledWith(expect.objectContaining({
          alerts: expect.objectContaining({
            webhook_url: '<redacted>',
            webhook_headers: ['Authorization: <redacted>'],
          }),
        }))
      })
    })

    it('rejects redacted webhook URL placeholders without a saved URL', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')
      await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
      fireEvent.change(screen.getByPlaceholderText('https://hooks.example.com/alert'), {
        target: { value: '<redacted>' },
      })
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'Webhook URL 占位符无效',
          description: '只有服务端已保存的 Webhook URL 才能保留为 <redacted>；新增 Webhook URL 需要填写真实地址。',
          color: 'danger',
        })
      })
      expect(mockUpdateSettings).not.toHaveBeenCalled()
    })

    it('rejects redacted webhook header placeholders without a saved header', async () => {
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
          title: 'Webhook Header 占位符无效',
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
      fireEvent.change(screen.getByPlaceholderText('https://hooks.example.com/alert'), {
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
        expect(screen.queryByDisplayValue('https://hooks.example.com/secret-token')).toBeNull()
        expect(screen.getByDisplayValue('<redacted>')).toBeTruthy()
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

      const maxSizeInput = await screen.findByDisplayValue('100 MB')
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
          message: 'directory access rule grants read through a descendant',
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
        expect(screen.getByText('子目录存在读取规则，因此允许查看相关路径。')).toBeTruthy()
        expect(screen.getByText('路径位于该用户主目录外。')).toBeTruthy()
      })
      expect(screen.queryByText('directory access rule grants read through a descendant')).toBeNull()
      expect(screen.queryByText('path is outside the user\'s home_dir')).toBeNull()
    })

    it('builds directory access user matrix', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
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

      fireEvent.change(screen.getByDisplayValue('8760h'), { target: { value: '720h' } })
      fireEvent.change(screen.getAllByDisplayValue('10 GB')[1], { target: { value: '5GB' } })
      fireEvent.change(screen.getByDisplayValue('24h'), { target: { value: '12h' } })

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

      await waitFor(() => {
        expect(screen.getByDisplayValue('30s')).toBeTruthy()
      })

      const readTimeoutInput = screen.getByDisplayValue('30s')
      await user.clear(readTimeoutInput)
      await user.type(readTimeoutInput, '45s')

      const writeTimeoutInput = screen.getByDisplayValue('60s')
      await user.clear(writeTimeoutInput)
      await user.type(writeTimeoutInput, '90s')

      const idleTimeoutInput = screen.getByDisplayValue('120s')
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
        const input = screen.getByDisplayValue('0.0.0.0')
        expect(input).toBeTruthy()
      })
    })

    it('renders server port input', async () => {
      render(<SettingsPage />)
      await waitFor(() => {
        const input = screen.getByDisplayValue('8080')
        expect(input).toBeTruthy()
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

      await waitFor(() => {
        expect(screen.getByDisplayValue('0.0.0.0')).toBeTruthy()
      })
      
      const input = screen.getByDisplayValue('0.0.0.0')
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
        expect(screen.getByDisplayValue('0.0.0.0')).toBeTruthy()
      })

      const input = screen.getByDisplayValue('0.0.0.0')
      await user.clear(input)
      await user.type(input, '127.0.0.1')
      expect(input).toHaveValue('127.0.0.1')

      await act(async () => {
        window.dispatchEvent(new Event('focus'))
      })

      await waitFor(() => {
        expect(screen.getByDisplayValue('127.0.0.1')).toBeTruthy()
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

      await waitFor(() => {
        expect(screen.getByDisplayValue('8080')).toBeTruthy()
      })

      const input = screen.getByDisplayValue('8080')
      await user.clear(input)
      await user.type(input, '9000')

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockUpdateSettings).toHaveBeenCalled()
        expect(mockGetSettings).toHaveBeenCalledTimes(2)
      })

      expect(screen.getByDisplayValue('9000')).toBeTruthy()

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
        expect(screen.getByDisplayValue('9000')).toBeTruthy()
      })
    })

    it('shows a restart-required warning when the backend reports save changes may need restart', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUpdateSettings.mockResolvedValueOnce({
        success: true,
        message: 'settings updated, some changes may require restart',
      })

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByDisplayValue('8080')).toBeTruthy()
      })

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '设置已保存，部分变更需要重启后生效',
          description: '部分配置项需要重启相关服务后才会生效。',
          color: 'warning',
        })
      })
    })

    it('shows a success toast when the backend reports a hot-applied save', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockUpdateSettings.mockResolvedValueOnce({
      success: true,
      message: 'settings updated',
    })

    render(<SettingsPage />)

    await waitFor(() => {
      expect(screen.getByDisplayValue('8080')).toBeTruthy()
    })

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

      await waitFor(() => {
        expect(screen.getByDisplayValue('8080')).toBeTruthy()
      })

      const input = screen.getByDisplayValue('8080')
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
      expect(screen.getByDisplayValue('9001')).toBeTruthy()

      await act(async () => {
        firstSave.resolve({ success: true, message: 'ok' })
      })

      await waitFor(() => {
        expect(mockGetSettings).toHaveBeenCalledTimes(2)
        expect(screen.getByDisplayValue('9001')).toBeTruthy()
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

      await waitFor(() => {
        expect(screen.getByDisplayValue('8080')).toBeTruthy()
      })

      const input = screen.getByDisplayValue('8080')
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
        expect(screen.getByDisplayValue('10.0.0.1')).toBeTruthy()
      })

      const input = screen.getByDisplayValue('10.0.0.1')
      await user.clear(input)
      await user.type(input, '127.0.0.1')
      expect(input).toHaveValue('127.0.0.1')

      await user.click(screen.getByText('重置'))

      await waitFor(() => {
        expect(screen.getByDisplayValue('10.0.0.1')).toBeTruthy()
      })
    })
  })

  describe('retention settings', () => {
    it('renders max versions input', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await waitFor(() => {
        const input = screen.getByDisplayValue('100')
        expect(input).toBeTruthy()
      })
    })

    it('renders max age input', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await waitFor(() => {
        const input = screen.getByDisplayValue('8760h')
        expect(input).toBeTruthy()
      })
    })

    it('allows editing max versions', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await waitFor(() => {
        expect(screen.getByDisplayValue('100')).toBeTruthy()
      })

      const input = screen.getByDisplayValue('100')
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
        const input = screen.getByDisplayValue('/dav')
        expect(input).toBeTruthy()
      })
    })

    it('rejects reserved WebDAV prefixes before saving', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByDisplayValue('/dav')).toBeTruthy()
      })

      fireEvent.change(screen.getByDisplayValue('/dav'), { target: { value: '/api/v1' } })
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
        expect(screen.getByDisplayValue('/dav')).toBeTruthy()
      })

      fireEvent.change(screen.getByDisplayValue('/dav'), { target: { value: '/dav\\files' } })
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
        const input = screen.getByDisplayValue('admin')
        expect(input).toBeTruthy()
      })
    })

    it('exposes accessible labels for WebDAV credential action buttons', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, 'WebDAV')

      await waitFor(() => {
        expect(screen.getByText('WebDAV 访问凭据')).toBeTruthy()
      })

      expect(screen.getByText('复制 WebDAV 地址')).toBeTruthy()
      expect(screen.getByText('复制 WebDAV 用户名')).toBeTruthy()
      const showPasswordText = screen.getByText('显示 WebDAV 密码')
      expect(showPasswordText).toBeTruthy()
      expect(screen.getByText('复制 WebDAV 密码')).toBeTruthy()

      await user.click(showPasswordText.closest('button') as HTMLButtonElement)

      expect(screen.getByText('隐藏 WebDAV 密码')).toBeTruthy()
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
          fireEvent.click(screen.getByText('复制 WebDAV 地址').closest('button') as HTMLButtonElement)
          await Promise.resolve()
        })

        expect(writeText).toHaveBeenCalledWith(expect.stringContaining('/dav/'))

        await act(async () => {
          fireEvent.click(screen.getByText('复制 WebDAV 用户名').closest('button') as HTMLButtonElement)
          await Promise.resolve()
        })

        expect(writeText).toHaveBeenCalledWith('admin')

        await act(async () => {
          fireEvent.click(screen.getByText('复制 WebDAV 密码').closest('button') as HTMLButtonElement)
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

      await user.click(screen.getByText('复制 WebDAV 用户名').closest('button') as HTMLButtonElement)

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
        expect(screen.getByText('最小块大小')).toBeTruthy()
        expect(screen.getByText('平均块大小')).toBeTruthy()
        expect(screen.getByText('最大块大小')).toBeTruthy()
      })
    })

    it('shows gRPC connection info', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('gRPC 地址')).toBeTruthy()
        expect(screen.getByDisplayValue('127.0.0.1:9090')).toBeTruthy()
        expect(screen.getByDisplayValue('30s')).toBeTruthy()
        expect(screen.getByDisplayValue('3')).toBeTruthy()
      })
    })

    it('allows editing CDC chunk sizes and saves them', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('CDC 分块参数')).toBeTruthy()
      })

      fireEvent.change(screen.getByDisplayValue('256 KB'), { target: { value: '512KB' } })
      fireEvent.change(screen.getByDisplayValue('1 MB'), { target: { value: '2MB' } })
      fireEvent.change(screen.getByDisplayValue('4 MB'), { target: { value: '8MB' } })

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

    await waitFor(() => {
      expect(screen.getByDisplayValue('8080')).toBeTruthy()
    })

    const portInput = screen.getByDisplayValue('8080')
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

    await waitFor(() => {
      expect(screen.getByDisplayValue('0.0.0.0')).toBeTruthy()
    })

    const hostInput = screen.getByDisplayValue('0.0.0.0')
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
      expect(screen.getByDisplayValue('100')).toBeTruthy()
    })

    const maxVersionsInput = screen.getByDisplayValue('100')
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
      expect(screen.getByDisplayValue('1 MB')).toBeTruthy()
    })

    const avgChunkInput = screen.getByDisplayValue('1 MB')
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
      expect(screen.getAllByDisplayValue('10 GB').length).toBeGreaterThan(1)
    })

    fireEvent.change(screen.getAllByDisplayValue('10 GB')[1], { target: { value: 'not-a-size' } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '大小格式无效',
        color: 'danger',
      }))
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
      expect(screen.getByDisplayValue('256 KB')).toBeTruthy()
    })

    fireEvent.change(screen.getByDisplayValue('256 KB'), { target: { value: '0' } })
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
      expect(screen.getByDisplayValue('256 KB')).toBeTruthy()
    })

    fireEvent.change(screen.getByDisplayValue('256 KB'), { target: { value: '32 KB' } })
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
      expect(screen.getByDisplayValue('4 MB')).toBeTruthy()
    })

    fireEvent.change(screen.getByDisplayValue('4 MB'), { target: { value: '65 MB' } })
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

    await waitFor(() => {
      expect(screen.getByDisplayValue('30s')).toBeTruthy()
    })

    fireEvent.change(screen.getByDisplayValue('30s'), { target: { value: '' } })
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

    await waitFor(() => {
      expect(screen.getByDisplayValue('60s')).toBeTruthy()
    })

    fireEvent.change(screen.getByDisplayValue('60s'), { target: { value: '' } })
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

    await waitFor(() => {
      expect(screen.getByDisplayValue('120s')).toBeTruthy()
    })

    fireEvent.change(screen.getByDisplayValue('120s'), { target: { value: '' } })
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

    await waitFor(() => {
      expect(screen.getByDisplayValue('30s')).toBeTruthy()
    })

    fireEvent.change(screen.getByDisplayValue('30s'), { target: { value: 'soon' } })
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
      expect(screen.getByDisplayValue('8760h')).toBeTruthy()
    })

    fireEvent.change(screen.getByDisplayValue('8760h'), { target: { value: 'forever' } })
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

    await waitFor(() => {
      expect(screen.getByDisplayValue('3')).toBeTruthy()
    })

    fireEvent.change(screen.getByDisplayValue('3'), { target: { value: '-1' } })
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

    await waitFor(() => {
      expect(screen.getByDisplayValue('30s')).toBeTruthy()
    })

    fireEvent.change(screen.getByDisplayValue('30s'), { target: { value: '' } })
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

    await waitFor(() => {
      expect(screen.getByDisplayValue('127.0.0.1:9090')).toBeTruthy()
    })

    fireEvent.change(screen.getByDisplayValue('127.0.0.1:9090'), { target: { value: '127.0.0.1:70000' } })
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
    fireEvent.change(screen.getByDisplayValue('90'), { target: { value: '120' } })
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
    fireEvent.change(screen.getByDisplayValue('90'), { target: { value: '90.5' } })
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
    fireEvent.change(screen.getByDisplayValue('90'), { target: { value: '' } })
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
    fireEvent.change(screen.getByDisplayValue('95'), { target: { value: '120' } })
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
    fireEvent.change(screen.getByDisplayValue('95'), { target: { value: '95.5' } })
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
    fireEvent.change(screen.getByDisplayValue('95'), { target: { value: '' } })
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
    fireEvent.change(screen.getByDisplayValue('90'), { target: { value: '96' } })
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
  ])('shows danger toast and skips save for invalid share base URL with %s', async (_label, baseURL) => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '分享')

    await user.click(await screen.findByRole('switch'))
    fireEvent.change(screen.getByPlaceholderText('https://nas.example.com'), { target: { value: baseURL } })
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '分享基础 URL 无效',
        description: '分享基础 URL 必须为空，或使用不含 userinfo、查询参数、片段且主机名有效的 http/https 地址',
        color: 'danger',
      })
    })
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows danger toast and skips save for invalid alert webhook URL', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    await openTab(user, '高级')

    await user.click(await screen.findByRole('switch', { name: '启用提醒' }))
    fireEvent.change(screen.getByPlaceholderText('https://hooks.example.com/alert'), { target: { value: 'ftp://hooks.example.com/storage' } })
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

      await waitFor(() => {
        expect(screen.getByText('保存设置')).toBeTruthy()
      })

      const saveBtn = screen.getByText('保存设置')
      await user.click(saveBtn)

      // Button should show loading state
      await waitFor(() => {
        const btn = screen.getByText('保存设置').closest('button')
        expect(btn).toBeTruthy()
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

    it('shows a validation warning when WebDAV username conflicts with a non-admin user', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockUpdateSettings.mockRejectedValueOnce(
        new SettingsError('webdav.username must not match a non-admin user when auth is enabled', 400)
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
