import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import { act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SettingsPage } from './Settings'
import * as HeroUI from '@heroui/react'

const mockAddToast = vi.fn()

import { getSettings } from '@/api/settings'
import { SettingsError } from '@/api/settings'
import { updateSettings } from '@/api/settings'
import { getWebDAVCredentials } from '@/api/settings'

const mockGetSettings = vi.mocked(getSettings)
const mockUpdateSettings = vi.mocked(updateSettings)
const mockGetWebDAVCredentials = vi.mocked(getWebDAVCredentials)

const { defaultSettingsResponse } = vi.hoisted(() => ({
  defaultSettingsResponse: {
    data: {
      server: { host: '0.0.0.0', port: 8080, read_timeout: '30s', write_timeout: '60s', idle_timeout: '120s', trusted_proxy_hops: 1, read_timeout_seconds: 60, write_timeout_seconds: 300 },
      storage: { root: '/root/.mnemonas' },
    trash: { enabled: true, retention_days: 30, max_size: 10737418240 },
      retention: { max_versions: 100, max_age: '8760h', min_free_space: 10737418240, gc_interval: '24h' },
      versioning: { auto_versioned_extensions: ['.md', '.txt', '.go'], auto_versioned_filenames: ['README', 'Dockerfile', 'Makefile'], max_versioned_size: 104857600 },
      webdav: { enabled: true, runtime_enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
      share: { enabled: false, base_url: '' },
      favorites: { enabled: true, runtime_available: true },
      alerts: { enabled: false, check_interval: '1h', threshold_pct: 90, critical_pct: 95, min_free_bytes: 10737418240, cooldown_period: '4h', webhook_url: '', webhook_method: 'POST', webhook_headers: [] },
      cdc: { min_chunk_size: 262144, avg_chunk_size: 1048576, max_chunk_size: 4194304 },
      dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
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
  updateSettings: vi.fn().mockResolvedValue({ success: true }),
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
    window.history.pushState({}, '', '/settings')
    mockGetSettings.mockResolvedValue(defaultSettingsResponse)
    mockGetWebDAVCredentials.mockResolvedValue({
      enabled: true,
      url: '/dav/',
      auth_type: 'basic',
      username: 'admin',
      password: 'secret',
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

  describe('rendering', () => {
    it('keeps the page header and actions visible while settings are loading', () => {
      mockGetSettings.mockReturnValue(new Promise(() => {}) as ReturnType<typeof getSettings>)

      render(<SettingsPage />)

      expect(screen.getByRole('heading', { name: '系统设置' })).toBeTruthy()
      expect(screen.getByRole('button', { name: '重置' })).toBeTruthy()
      expect(screen.getByRole('button', { name: '保存设置' })).toBeTruthy()
      expect(screen.getByText('加载设置...')).toBeTruthy()
    })

    it('renders page header', async () => {
      render(<SettingsPage />)
      await waitFor(() => {
        expect(screen.getByText('系统设置')).toBeTruthy()
        expect(screen.getByText('配置 MnemoNAS 系统参数')).toBeTruthy()
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
        expect(screen.getByText('服务器')).toBeTruthy()
        expect(screen.getByText('存储路径')).toBeTruthy()
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
        expect(screen.getByText('启用 WebDAV')).toBeTruthy()
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

      await openTab(user, '分享管理')

      const shareBaseUrlInput = await screen.findByPlaceholderText('https://nas.example.com')
      expect(shareBaseUrlInput).toHaveAttribute('type', 'url')
    })

    it('shows example duration placeholders for retention and alert timing settings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      expect(await screen.findByPlaceholderText('8760h')).toBeTruthy()
      expect(screen.getByPlaceholderText('24h')).toBeTruthy()

      await openTab(user, '高级')

      expect(await screen.findByPlaceholderText('30s')).toBeTruthy()
      expect(screen.getByPlaceholderText('1h')).toBeTruthy()
      expect(screen.getByPlaceholderText('4h')).toBeTruthy()
    })

    it('opens the tab selected in the query string on first render', async () => {
      window.history.pushState({}, '', '/settings?tab=advanced')
      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('CDC 分块参数')).toBeTruthy()
        expect(screen.getByText('存储告警')).toBeTruthy()
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
        expect(mockUpdateSettings).toHaveBeenCalledWith(expect.objectContaining({
          webdav: expect.objectContaining({
            auth_type: 'none',
          }),
        }))
      })
    })
  })

  describe('dataplane settings', () => {
    it('explains CDC and dataplane connection effect timing', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('配置内容定义分块算法；保存后需重启数据面服务，且仅影响后续新写入数据')).toBeTruthy()
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
      await user.clear(grpcInput)
      await user.type(grpcInput, '10.0.0.2:9091')

      const timeoutInput = screen.getByDisplayValue('30s')
      await user.clear(timeoutInput)
      await user.type(timeoutInput, '45s')

      const retriesInput = screen.getByDisplayValue('3')
      await user.clear(retriesInput)
      await user.type(retriesInput, '5')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockUpdateSettings).toHaveBeenCalledWith(expect.objectContaining({
          dataplane: expect.objectContaining({
            grpc_address: '10.0.0.2:9091',
            timeout: '45s',
            max_retries: 5,
          }),
        }))
      })
    })
  })

  describe('share settings', () => {
    it('allows editing share configuration and saves it', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '分享管理')

      await waitFor(() => {
        expect(screen.getByText('分享功能配置')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch'))
      const baseUrlInput = screen.getByPlaceholderText('https://nas.example.com')
      await user.type(baseUrlInput, 'https://share.example.com')
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockUpdateSettings).toHaveBeenCalledWith(expect.objectContaining({
          share: expect.objectContaining({
            enabled: true,
            base_url: 'https://share.example.com',
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

      const certInput = screen.getByPlaceholderText('/path/to/server.crt')
      await user.type(certInput, '/etc/mnemonas/tls/server.crt')

      const keyInput = screen.getByPlaceholderText('/path/to/server.key')
      await user.type(keyInput, '/etc/mnemonas/tls/server.key')

      const certDirInput = screen.getByPlaceholderText('~/.mnemonas/certs')
      await user.clear(certDirInput)
      await user.type(certDirInput, '/etc/mnemonas/tls')

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockUpdateSettings).toHaveBeenCalledWith(expect.objectContaining({
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
  })

  describe('alerts settings', () => {
    it('allows editing alerts configuration and saves it', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '高级')

      await waitFor(() => {
        expect(screen.getByText('存储告警')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch', { name: '启用告警' }))

      const checkIntervalInput = screen.getByDisplayValue('1h')
      await user.clear(checkIntervalInput)
      await user.type(checkIntervalInput, '30m')

      const thresholdInput = screen.getByDisplayValue('90')
      await user.clear(thresholdInput)
      await user.type(thresholdInput, '85')

      const criticalInput = screen.getByDisplayValue('95')
      await user.clear(criticalInput)
      await user.type(criticalInput, '92')

      const minFreeInput = screen.getByDisplayValue('10 GB')
      await user.clear(minFreeInput)
      await user.type(minFreeInput, '20GB')

      const cooldownInput = screen.getByDisplayValue('4h')
      await user.clear(cooldownInput)
      await user.type(cooldownInput, '2h')

      const webhookInput = screen.getByPlaceholderText('https://hooks.example.com/alert')
      await user.type(webhookInput, 'https://hooks.example.com/storage')

      await user.selectOptions(screen.getByLabelText('Webhook 方法'), 'GET')

      const headersInput = screen.getByLabelText('Webhook 自定义 Header')
      await user.type(headersInput, 'Authorization: Bearer token{enter}X-MnemoNAS: alerts')

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockUpdateSettings).toHaveBeenCalledWith(expect.objectContaining({
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
          }),
        }))
      })
    })
  })

  describe('trash settings', () => {
    it('allows toggling trash behavior and saves it', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await waitFor(() => {
        expect(screen.getByText('启用回收站')).toBeTruthy()
      })

      await user.click(screen.getByRole('switch'))
      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockUpdateSettings).toHaveBeenCalledWith(expect.objectContaining({
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

    it('allows editing auto-versioning rules and saves them', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<SettingsPage />)

      await openTab(user, '版本保留')

      await waitFor(() => {
        expect(screen.getByText('自动版本化')).toBeTruthy()
      })

      const extensionsInput = screen.getByLabelText('自动版本化后缀')
      await user.clear(extensionsInput)
      await user.type(extensionsInput, '.md{enter}.txt{enter}.rs')

      const filenamesInput = screen.getByLabelText('自动版本化文件名')
      await user.clear(filenamesInput)
      await user.type(filenamesInput, 'README{enter}Dockerfile{enter}Cargo.toml')

      const maxSizeInput = screen.getByDisplayValue('100 MB')
      await user.clear(maxSizeInput)
      await user.type(maxSizeInput, '256MB')

      await user.click(screen.getByText('保存设置'))

      await waitFor(() => {
        expect(mockUpdateSettings).toHaveBeenCalledWith(expect.objectContaining({
          versioning: expect.objectContaining({
            auto_versioned_extensions: ['.md', '.txt', '.rs'],
            auto_versioned_filenames: ['README', 'Dockerfile', 'Cargo.toml'],
            max_versioned_size: 268435456,
          }),
        }))
      })
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
      expect(mockUpdateSettings).toHaveBeenCalledWith(expect.objectContaining({
      trash: expect.objectContaining({
        enabled: true,
        retention_days: 7,
        max_size: 2147483648,
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
        expect(mockUpdateSettings).toHaveBeenCalledWith(expect.objectContaining({
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
        expect(mockUpdateSettings).toHaveBeenCalledWith(expect.objectContaining({
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

    it('allows editing trusted proxy hops and saves it', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<SettingsPage />)

    const input = await screen.findByLabelText('受信代理层数')
    await user.clear(input)
    await user.type(input, '2')
    await user.click(screen.getByText('保存设置'))

    await waitFor(() => {
    expect(mockUpdateSettings).toHaveBeenCalledWith(expect.objectContaining({
      server: expect.objectContaining({
      trusted_proxy_hops: 2,
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
      description: '受信代理层数必须是 0 或正整数',
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
        expect(screen.getByText('/root/.mnemonas')).toBeTruthy()
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
            storage: { root: '/root/.mnemonas' },
            trash: { enabled: true, retention_days: 30, max_size: 10737418240 },
            retention: { max_versions: 100, max_age: '8760h', min_free_space: 10737418240, gc_interval: '24h' },
            versioning: { auto_versioned_extensions: ['.md', '.txt', '.go'], auto_versioned_filenames: ['README', 'Dockerfile', 'Makefile'], max_versioned_size: 104857600 },
            webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
            share: { enabled: false, base_url: '' },
            favorites: { enabled: true },
            alerts: { enabled: false, check_interval: '1h', threshold_pct: 90, critical_pct: 95, min_free_bytes: 10737418240, cooldown_period: '4h', webhook_url: '', webhook_method: 'POST', webhook_headers: [] },
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
            alerts: { enabled: true, check_interval: '30m', threshold_pct: 85, critical_pct: 92, min_free_bytes: 21474836480, cooldown_period: '2h', webhook_url: 'https://hooks.example.com/storage', webhook_method: 'GET', webhook_headers: ['Authorization: Bearer token', 'X-MnemoNAS: alerts'] },
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
        expect(mockUpdateSettings).toHaveBeenCalledWith(expect.objectContaining({
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
        expect(mockUpdateSettings).toHaveBeenLastCalledWith(expect.objectContaining({
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
          alerts: { enabled: true, check_interval: '30m', threshold_pct: 85, critical_pct: 92, min_free_bytes: 21474836480, cooldown_period: '2h', webhook_url: 'https://hooks.example.com/storage', webhook_method: 'GET', webhook_headers: ['Authorization: Bearer token', 'X-MnemoNAS: alerts'] },
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
        expect(screen.getByText('启用 WebDAV')).toBeTruthy()
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
        expect(screen.getByText('webdav credentials unavailable')).toBeTruthy()
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
        expect(screen.getByText('当前无法读取运行中的 WebDAV 凭据，请检查系统状态或稍后重试。')).toBeTruthy()
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
          description: '当前无法读取运行中的 WebDAV 凭据，请检查系统状态或稍后重试。',
          color: 'warning',
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
        description: '最大版本数必须是 0 或正整数',
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
            storage: { root: '/root/.mnemonas' },
            trash: { enabled: true, retention_days: 30, max_size: 10737418240 },
            retention: { max_versions: 100, max_age: '8760h', min_free_space: 10737418240, gc_interval: '24h' },
            versioning: { auto_versioned_extensions: ['.md', '.txt', '.go'], auto_versioned_filenames: ['README', 'Dockerfile', 'Makefile'], max_versioned_size: 104857600 },
            webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
            share: { enabled: false, base_url: '' },
            favorites: { enabled: true },
            alerts: { enabled: false, check_interval: '1h', threshold_pct: 90, critical_pct: 95, min_free_bytes: 10737418240, cooldown_period: '4h', webhook_url: '', webhook_method: 'POST', webhook_headers: [] },
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
          description: 'Network error',
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
          description: '系统设置当前不可用，请检查服务健康状态或稍后重试。',
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
          description: '系统设置当前不可用，请检查服务健康状态或稍后重试。',
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
        expect(screen.getByText('系统设置当前不可用，请检查服务健康状态或稍后重试。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('shows retryable error state when initial settings load fails', async () => {
      mockGetSettings.mockRejectedValueOnce(new Error('Network error'))

      render(<SettingsPage />)

      await waitFor(() => {
        expect(screen.getByText('加载设置失败')).toBeTruthy()
        expect(screen.getByText('Network error')).toBeTruthy()
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
            storage: { root: '/root/.mnemonas' },
            trash: { enabled: true, retention_days: 30, max_size: 10737418240 },
            retention: { max_versions: 100, max_age: '8760h', min_free_space: 10737418240, gc_interval: '24h' },
            versioning: { auto_versioned_extensions: ['.md', '.txt', '.go'], auto_versioned_filenames: ['README', 'Dockerfile', 'Makefile'], max_versioned_size: 104857600 },
            webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
            share: { enabled: false, base_url: '' },
            favorites: { enabled: true },
            alerts: { enabled: false, check_interval: '1h', threshold_pct: 90, critical_pct: 95, min_free_bytes: 10737418240, cooldown_period: '4h', webhook_url: '', webhook_method: 'POST', webhook_headers: [] },
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
        expect(screen.getByText('系统设置')).toBeTruthy()
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
          description: '系统设置当前不可用，请检查服务健康状态或稍后重试。',
          color: 'warning',
        })
      })
    })
  })

})
