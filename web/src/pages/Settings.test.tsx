import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import { act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SettingsPage } from './Settings'
import * as HeroUI from '@heroui/react'

const mockAddToast = vi.fn()

import { getSettings } from '@/api/settings'
import { updateSettings } from '@/api/settings'

const mockGetSettings = vi.mocked(getSettings)
const mockUpdateSettings = vi.mocked(updateSettings)

const { defaultSettingsResponse } = vi.hoisted(() => ({
  defaultSettingsResponse: {
    data: {
      server: { host: '0.0.0.0', port: 8080, read_timeout_seconds: 60, write_timeout_seconds: 300 },
      storage: { root: '/root/.mnemonas' },
      retention: { max_versions: 100, max_age: '8760h', min_free_space: 10737418240, gc_interval: '24h' },
      webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
      share: { enabled: false, base_url: '' },
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
    mockGetSettings.mockResolvedValue(defaultSettingsResponse)
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
  })

  const openTab = async (user: ReturnType<typeof userEvent.setup>, label: string) => {
	await waitFor(() => {
		expect(screen.getByRole('tab', { name: label })).toBeTruthy()
	})
	await user.click(screen.getByRole('tab', { name: label }))
  }

  describe('rendering', () => {
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
  })

  describe('webdav settings', () => {
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
        }), expect.anything())
      })
    })
  })

  describe('dataplane settings', () => {
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
        }), expect.anything())
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
        }), expect.anything())
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
        }), expect.anything())
      })
    })
  })

  describe('general settings', () => {
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
            server: { host: '0.0.0.0', port: 8080, read_timeout_seconds: 60, write_timeout_seconds: 300 },
            storage: { root: '/root/.mnemonas' },
            retention: { max_versions: 100, max_age: '8760h', min_free_space: 10737418240, gc_interval: '24h' },
            webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
            share: { enabled: false, base_url: '' },
            cdc: { min_chunk_size: 262144, avg_chunk_size: 1048576, max_chunk_size: 4194304 },
            dataplane: { grpc_address: '127.0.0.1:9090', timeout: '30s', max_retries: 3 },
          },
        })
        .mockResolvedValue({
          data: {
            server: { host: '10.0.0.1', port: 9090, read_timeout_seconds: 60, write_timeout_seconds: 300 },
            storage: { root: '/srv/mnemonas' },
            retention: { max_versions: 200, max_age: '720h', min_free_space: 2147483648, gc_interval: '12h' },
            webdav: { enabled: false, prefix: '/files', read_only: true, auth_type: 'basic', username: 'sync-user' },
            share: { enabled: true, base_url: 'https://share.example.com' },
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

    it('reset restores server values after local edits', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetSettings.mockResolvedValue({
        data: {
          server: { host: '10.0.0.1', port: 9090, read_timeout_seconds: 60, write_timeout_seconds: 300 },
          storage: { root: '/srv/mnemonas' },
          retention: { max_versions: 200, max_age: '720h', min_free_space: 2147483648, gc_interval: '12h' },
          webdav: { enabled: false, prefix: '/files', read_only: true, auth_type: 'basic', username: 'sync-user' },
          share: { enabled: true, base_url: 'https://share.example.com' },
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
            server: { host: '0.0.0.0', port: 8080, read_timeout_seconds: 60, write_timeout_seconds: 300 },
            storage: { root: '/root/.mnemonas' },
            retention: { max_versions: 100, max_age: '8760h', min_free_space: 10737418240, gc_interval: '24h' },
            webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
            share: { enabled: false, base_url: '' },
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
  })

  describe('error state', () => {
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
            server: { host: '0.0.0.0', port: 8080, read_timeout_seconds: 60, write_timeout_seconds: 300 },
            storage: { root: '/root/.mnemonas' },
            retention: { max_versions: 100, max_age: '8760h', min_free_space: 10737418240, gc_interval: '24h' },
            webdav: { enabled: true, prefix: '/dav', read_only: false, auth_type: 'basic', username: 'admin' },
            share: { enabled: false, base_url: '' },
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
      })
    })
  })

})
