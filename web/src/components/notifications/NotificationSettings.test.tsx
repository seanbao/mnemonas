import { act } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import * as HeroUI from '@heroui/react'
import { render, screen, waitFor, within } from '@/test/utils'

vi.mock('@/api/settings', () => ({
  SettingsError: class SettingsError extends Error {
    status: number
    code?: string

    constructor(message: string, status: number, code?: string) {
      super(message)
      this.name = 'SettingsError'
      this.status = status
      this.code = code
    }

    get isUnavailable() {
      return this.status === 503 || this.code === 'SERVICE_UNAVAILABLE'
    }
  },
  getSettings: vi.fn(),
  sendTestAlert: vi.fn(),
  updateSettings: vi.fn(),
}))

import { getSettings, sendTestAlert, SettingsError, updateSettings } from '@/api/settings'
import { NotificationSettings } from './NotificationSettings'

const mockGetSettings = vi.mocked(getSettings)
const mockSendTestAlert = vi.mocked(sendTestAlert)
const mockUpdateSettings = vi.mocked(updateSettings)
const mockAddToast = vi.fn()

type AlertOverrides = Partial<NonNullable<
  Awaited<ReturnType<typeof getSettings>>['data']['alerts']
>>

function settingsResponse(overrides: AlertOverrides = {}) {
  return {
    success: true,
    data: {
      alerts: {
        enabled: false,
        check_interval: '1h',
        threshold_pct: 90,
        critical_pct: 95,
        min_free_bytes: 10 * 1024 * 1024 * 1024,
        cooldown_period: '4h',
        webhook_url: '',
        webhook_url_configured: false,
        webhook_method: 'POST',
        webhook_headers: [],
        webhook_headers_configured: false,
        telegram_enabled: false,
        telegram_bot_token_configured: false,
        telegram_chat_id: '',
        wecom_enabled: false,
        wecom_webhook_url: '',
        wecom_webhook_url_configured: false,
        dingtalk_enabled: false,
        dingtalk_webhook_url: '',
        dingtalk_webhook_url_configured: false,
        email_enabled: false,
        smtp_host: '',
        smtp_port: 587,
        smtp_username: '',
        smtp_password_configured: false,
        smtp_from: '',
        smtp_to: [],
        ...overrides,
      },
    },
  } as unknown as Awaited<ReturnType<typeof getSettings>>
}

function configuredSettingsResponse() {
  return settingsResponse({
    webhook_url: '<redacted>',
    webhook_url_configured: true,
    webhook_headers: ['Authorization: <redacted>'],
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
    email_enabled: true,
    smtp_host: 'smtp.example.com',
    smtp_port: 587,
    smtp_username: 'alerts@example.com',
    smtp_password_configured: true,
    smtp_from: 'alerts@example.com',
    smtp_to: ['admin@example.com'],
  })
}

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

async function waitForNotificationSettings() {
  await screen.findByRole('checkbox', { name: '启用提醒' })
}

async function replaceInput(
  user: ReturnType<typeof userEvent.setup>,
  input: HTMLElement,
  value: string,
) {
  await user.clear(input)
  await user.type(input, value)
}

describe('NotificationSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => (
      mockAddToast(...args)
    )) as typeof HeroUI.addToast)
    mockGetSettings.mockResolvedValue(settingsResponse())
    mockSendTestAlert.mockResolvedValue({
      success: true,
      message: '',
      warning: false,
      data: { event_type: 'alert_test', channels: ['webhook'] },
    })
    mockUpdateSettings.mockResolvedValue({
      success: true,
      message: 'settings updated',
      warning: false,
    })
  })

  it('loads the notification domain with an abort signal and keeps professional parameters collapsed', async () => {
    mockGetSettings.mockResolvedValueOnce(configuredSettingsResponse())
    render(<NotificationSettings />)

    expect(screen.getByRole('status')).toHaveTextContent('加载通知设置')
    await waitForNotificationSettings()

    expect(mockGetSettings).toHaveBeenCalledTimes(1)
    const options = mockGetSettings.mock.calls[0][0]
    expect(options?.signal).toBeInstanceOf(AbortSignal)
    expect(Object.keys(options ?? {})).toEqual(['signal'])
    expect(screen.getByText('已配置目的地')).toBeInTheDocument()
    expect(screen.getByText(/^Webhook · 未启用$/u)).toBeInTheDocument()
    expect(screen.getByText(/^企业微信 · 未启用$/u)).toBeInTheDocument()
    expect(screen.getByText(/^钉钉 · 未启用$/u)).toBeInTheDocument()
    expect(screen.getByText(/^邮件 · 未启用$/u)).toBeInTheDocument()
    expect(screen.getByText(/^Telegram · 未启用$/u)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '添加目的地' })).toBeEnabled()
    const advanced = screen.getByText('专业参数').closest('details')
    expect(advanced).not.toHaveAttribute('open')
  })

  it('marks changes dirty and resets the complete draft', async () => {
    const user = userEvent.setup()
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    await user.click(screen.getByRole('checkbox', { name: '启用提醒' }))
    await replaceInput(user, screen.getByRole('textbox', { name: 'Webhook URL' }), 'https://hooks.example.com/alert')

    expect(screen.getByText('有未保存更改')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存通知设置' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: '重置更改' }))

    expect(screen.getByRole('checkbox', { name: '启用提醒' })).not.toBeChecked()
    expect(screen.getByRole('textbox', { name: 'Webhook URL' })).toHaveValue('')
    expect(screen.queryByText('有未保存更改')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存通知设置' })).toBeDisabled()
  })

  it('sends a test alert from the saved configuration with an abort signal', async () => {
    const user = userEvent.setup()
    mockGetSettings.mockResolvedValueOnce(settingsResponse({
      enabled: true,
      webhook_url: '<redacted>',
      webhook_url_configured: true,
    }))
    mockSendTestAlert.mockResolvedValueOnce({
      success: true,
      message: '',
      warning: false,
      data: { event_type: 'alert_test', channels: ['webhook', 'backend_custom_channel'] },
    })
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    await user.click(screen.getByRole('button', { name: '发送测试提醒' }))

    await waitFor(() => expect(mockSendTestAlert).toHaveBeenCalledTimes(1))
    const options = mockSendTestAlert.mock.calls[0][0]
    expect(options?.signal).toBeInstanceOf(AbortSignal)
    expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
      title: '测试提醒已发送',
      description: '已发送到 Webhook / 未知通道',
      color: 'success',
    }))
    expect(mockAddToast).not.toHaveBeenCalledWith(expect.objectContaining({
      description: expect.stringContaining('backend_custom_channel'),
    }))
  })

  it('requires saved changes and an active destination before testing', async () => {
    const user = userEvent.setup()
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    await user.click(screen.getByRole('checkbox', { name: '启用提醒' }))
    await user.click(screen.getByRole('button', { name: '发送测试提醒' }))
    expect(mockSendTestAlert).not.toHaveBeenCalled()
    expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
      title: '需要先保存通知设置',
      color: 'warning',
    }))

    await user.click(screen.getByRole('button', { name: '重置更改' }))
    await user.click(screen.getByRole('button', { name: '发送测试提醒' }))
    expect(mockSendTestAlert).not.toHaveBeenCalled()
    expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
      title: '提醒尚未启用',
      color: 'warning',
    }))
  })

  it('requires a configured destination when saved alerts are enabled', async () => {
    const user = userEvent.setup()
    mockGetSettings.mockResolvedValueOnce(settingsResponse({ enabled: true }))
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    await user.click(screen.getByRole('button', { name: '发送测试提醒' }))

    expect(mockSendTestAlert).not.toHaveBeenCalled()
    expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
      title: '没有可用提醒通道',
      color: 'warning',
    }))
  })

  it('uses a saved Telegram destination for test alerts', async () => {
    const user = userEvent.setup()
    mockGetSettings.mockResolvedValueOnce(settingsResponse({
      enabled: true,
      telegram_enabled: true,
      telegram_bot_token_configured: true,
      telegram_chat_id: '-1001234567890',
    }))
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    await user.click(screen.getByRole('button', { name: '发送测试提醒' }))

    await waitFor(() => expect(mockSendTestAlert).toHaveBeenCalledTimes(1))
  })

  it.each([
    ['企业微信', {
      wecom_enabled: true,
      wecom_webhook_url: '<redacted>',
      wecom_webhook_url_configured: true,
    }],
    ['钉钉', {
      dingtalk_enabled: true,
      dingtalk_webhook_url: '<redacted>',
      dingtalk_webhook_url_configured: true,
    }],
  ] as const)('uses a saved %s destination for test alerts', async (_label, destination) => {
    const user = userEvent.setup()
    mockGetSettings.mockResolvedValueOnce(settingsResponse({ enabled: true, ...destination }))
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    await user.click(screen.getByRole('button', { name: '发送测试提醒' }))

    await waitFor(() => expect(mockSendTestAlert).toHaveBeenCalledTimes(1))
  })

  it('reports generic test alert failures without changing saved settings', async () => {
    const user = userEvent.setup()
    mockGetSettings.mockResolvedValueOnce(settingsResponse({
      enabled: true,
      webhook_url: '<redacted>',
      webhook_url_configured: true,
    }))
    mockSendTestAlert.mockRejectedValueOnce(new Error('delivery failed'))
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    await user.click(screen.getByRole('button', { name: '发送测试提醒' }))

    await waitFor(() => expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
      title: '测试提醒失败',
      color: 'danger',
    })))
    expect(mockUpdateSettings).not.toHaveBeenCalled()
  })

  it('shows generic load failures and moves focus toward destinations', async () => {
    mockGetSettings.mockRejectedValueOnce(new Error('invalid response'))
    const { unmount } = render(<NotificationSettings />)
    expect(await screen.findByText('加载通知设置失败')).toBeInTheDocument()
    unmount()

    mockGetSettings.mockResolvedValueOnce(settingsResponse())
    const user = userEvent.setup()
    render(<NotificationSettings />)
    await waitForNotificationSettings()
    await user.click(screen.getByRole('button', { name: '添加目的地' }))
    expect(screen.getByRole('textbox', { name: 'Webhook URL' })).toHaveFocus()
  })

  it('redacts diagnostic details from test alert warnings', async () => {
    const user = userEvent.setup()
    mockGetSettings.mockResolvedValueOnce(settingsResponse({
      enabled: true,
      webhook_url: '<redacted>',
      webhook_url_configured: true,
    }))
    mockSendTestAlert.mockResolvedValueOnce({
      success: true,
      message: 'delivery warning token=webhook-secret Authorization: Bearer bearer-secret',
      warning: true,
      data: { event_type: 'alert_test', channels: ['webhook'] },
    })
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    await user.click(screen.getByRole('button', { name: '发送测试提醒' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '测试提醒已发送，但存在警告',
        description: 'delivery warning token=<redacted> Authorization: Bearer <redacted>',
        color: 'warning',
      }))
    })
  })

  it('submits only the alerts domain and normalizes every destination', async () => {
    const user = userEvent.setup()
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    await user.click(screen.getByRole('checkbox', { name: '启用提醒' }))
    await replaceInput(user, screen.getByRole('textbox', { name: 'Webhook URL' }), ' https://hooks.example.com/alert ')
    await user.selectOptions(screen.getByRole('combobox', { name: 'Webhook 方法' }), 'GET')
    await replaceInput(
      user,
      screen.getByRole('textbox', { name: 'Webhook 自定义 Header' }),
      ' Authorization: Bearer secret \n X-MnemoNAS: alerts ',
    )

    await user.click(screen.getByRole('checkbox', { name: '启用企业微信通知' }))
    await replaceInput(
      user,
      screen.getByRole('textbox', { name: '企业微信 Webhook URL' }),
      'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret',
    )
    await user.click(screen.getByRole('checkbox', { name: '启用钉钉通知' }))
    await replaceInput(
      user,
      screen.getByRole('textbox', { name: '钉钉 Webhook URL' }),
      'https://oapi.dingtalk.com/robot/send?access_token=secret',
    )

    await user.click(screen.getByRole('checkbox', { name: '启用邮件通知' }))
    await replaceInput(user, screen.getByRole('textbox', { name: 'SMTP 主机' }), ' smtp.example.com ')
    await replaceInput(user, screen.getByRole('textbox', { name: 'SMTP 用户名' }), ' alerts@example.com ')
    await replaceInput(user, screen.getByLabelText('SMTP 密码'), 'smtp-secret')
    await replaceInput(user, screen.getByRole('textbox', { name: 'SMTP 发件人' }), ' alerts@example.com ')
    await replaceInput(
      user,
      screen.getByRole('textbox', { name: 'SMTP 收件人' }),
      ' admin@example.com, ops@example.com ',
    )

    await user.click(screen.getByRole('checkbox', { name: '启用 Telegram 通知' }))
    await replaceInput(user, screen.getByLabelText('Telegram Bot Token'), '123456:secret')
    await replaceInput(user, screen.getByRole('textbox', { name: 'Telegram Chat ID' }), ' -1001234567890 ')
    await user.click(screen.getByText('专业参数'))
    await replaceInput(user, screen.getByRole('spinbutton', { name: '提醒阈值' }), '85')
    await replaceInput(user, screen.getByRole('spinbutton', { name: '严重提醒阈值' }), '97')
    await replaceInput(user, screen.getByRole('textbox', { name: '最小剩余空间' }), '8 GB')
    await replaceInput(user, screen.getByRole('textbox', { name: '提醒检查间隔' }), '30m')
    await replaceInput(user, screen.getByRole('textbox', { name: '提醒冷却时间' }), '2h')

    await user.click(screen.getByRole('button', { name: '保存通知设置' }))

    await waitFor(() => expect(mockUpdateSettings).toHaveBeenCalledTimes(1))
    const [request, options] = mockUpdateSettings.mock.calls[0]
    expect(Object.keys(request)).toEqual(['alerts'])
    expect(request).toEqual({
      alerts: {
        enabled: true,
        check_interval: '30m',
        threshold_pct: 85,
        critical_pct: 97,
        min_free_bytes: 8 * 1024 * 1024 * 1024,
        cooldown_period: '2h',
        webhook_url: 'https://hooks.example.com/alert',
        webhook_method: 'GET',
        webhook_headers: ['Authorization: Bearer secret', 'X-MnemoNAS: alerts'],
        telegram_enabled: true,
        telegram_chat_id: '-1001234567890',
        telegram_bot_token: '123456:secret',
        wecom_enabled: true,
        wecom_webhook_url: 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret',
        dingtalk_enabled: true,
        dingtalk_webhook_url: 'https://oapi.dingtalk.com/robot/send?access_token=secret',
        email_enabled: true,
        smtp_host: 'smtp.example.com',
        smtp_port: 587,
        smtp_username: 'alerts@example.com',
        smtp_password: 'smtp-secret',
        smtp_from: 'alerts@example.com',
        smtp_to: ['admin@example.com', 'ops@example.com'],
      },
    })
    expect(options?.signal).toBeInstanceOf(AbortSignal)
    expect(Object.keys(options ?? {})).toEqual(['signal'])

    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: 'Webhook URL' })).toHaveValue('<redacted>')
    })
    expect(screen.getByRole('textbox', { name: 'Webhook 自定义 Header' })).toHaveValue(
      'Authorization: <redacted>\nX-MnemoNAS: <redacted>',
    )
    expect(screen.getByLabelText('Telegram Bot Token')).toHaveValue('')
    expect(screen.getByLabelText('Telegram Bot Token')).toHaveAttribute('placeholder', '已配置，留空不变')
    expect(screen.getByLabelText('SMTP 密码')).toHaveValue('')
    expect(screen.getByLabelText('SMTP 密码')).toHaveAttribute('placeholder', '已配置，留空不变')
    expect(screen.getByRole('button', { name: '保存通知设置' })).toBeDisabled()
  }, 15_000)

  it('preserves redacted values and omits unchanged non-returned secrets', async () => {
    const user = userEvent.setup()
    mockGetSettings.mockResolvedValueOnce(configuredSettingsResponse())
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    await user.click(screen.getByRole('checkbox', { name: '启用提醒' }))
    await user.click(screen.getByRole('button', { name: '保存通知设置' }))

    await waitFor(() => expect(mockUpdateSettings).toHaveBeenCalledTimes(1))
    const request = mockUpdateSettings.mock.calls[0][0]
    expect(Object.keys(request)).toEqual(['alerts'])
    expect(request.alerts).toEqual(expect.objectContaining({
      webhook_url: '<redacted>',
      webhook_headers: ['Authorization: <redacted>'],
      wecom_webhook_url: '<redacted>',
      dingtalk_webhook_url: '<redacted>',
    }))
    expect(request.alerts).not.toHaveProperty('telegram_bot_token')
    expect(request.alerts).not.toHaveProperty('smtp_password')
  })

  it('sends explicit empty secrets only when clear is selected', async () => {
    const user = userEvent.setup()
    mockGetSettings.mockResolvedValueOnce(configuredSettingsResponse())
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    await user.click(screen.getByRole('checkbox', { name: '启用 Telegram 通知' }))
    await user.click(screen.getByRole('checkbox', { name: '保存时清除已保存 Telegram Token' }))
    await user.click(screen.getByRole('checkbox', { name: '保存时清除已保存 SMTP 密码' }))
    await user.click(screen.getByRole('button', { name: '保存通知设置' }))

    await waitFor(() => expect(mockUpdateSettings).toHaveBeenCalledTimes(1))
    const alerts = mockUpdateSettings.mock.calls[0][0].alerts
    expect(alerts).toEqual(expect.objectContaining({
      telegram_enabled: false,
      telegram_bot_token: '',
      smtp_password: '',
    }))
    expect(screen.queryByRole('checkbox', { name: '保存时清除已保存 Telegram Token' })).not.toBeInTheDocument()
    expect(screen.queryByRole('checkbox', { name: '保存时清除已保存 SMTP 密码' })).not.toBeInTheDocument()
  })

  it('reports destination and professional parameter validation together without submitting', async () => {
    const user = userEvent.setup()
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    await replaceInput(user, screen.getByRole('textbox', { name: 'Webhook URL' }), 'ftp://example.com/hook')
    await replaceInput(
      user,
      screen.getByRole('textbox', { name: 'Webhook 自定义 Header' }),
      'X-Key: one\nx-key: two',
    )
    await user.click(screen.getByRole('checkbox', { name: '启用企业微信通知' }))
    await user.click(screen.getByRole('checkbox', { name: '启用钉钉通知' }))
    await user.click(screen.getByRole('checkbox', { name: '启用邮件通知' }))
    await replaceInput(user, screen.getByRole('spinbutton', { name: 'SMTP 端口' }), '0')
    await user.click(screen.getByRole('checkbox', { name: '启用 Telegram 通知' }))

    await user.click(screen.getByText('专业参数'))
    await replaceInput(user, screen.getByRole('spinbutton', { name: '提醒阈值' }), '95')
    await replaceInput(user, screen.getByRole('spinbutton', { name: '严重提醒阈值' }), '90')
    await replaceInput(user, screen.getByRole('textbox', { name: '最小剩余空间' }), 'not-a-size')
    await replaceInput(user, screen.getByRole('textbox', { name: '提醒检查间隔' }), '0h')
    await replaceInput(user, screen.getByRole('textbox', { name: '提醒冷却时间' }), '4d')
    await user.click(screen.getByRole('button', { name: '保存通知设置' }))

    expect(mockUpdateSettings).not.toHaveBeenCalled()
    expect(screen.getByText(/Webhook URL 必须为空/)).toBeInTheDocument()
    expect(screen.getByText(/Header x-key 重复/)).toBeInTheDocument()
    expect(screen.getByText(/企业微信通知时必须填写/)).toBeInTheDocument()
    expect(screen.getByText(/钉钉通知时必须填写/)).toBeInTheDocument()
    expect(screen.getByText(/^启用邮件通知时必须填写 SMTP 主机。$/u)).toBeInTheDocument()
    expect(screen.getByText(/SMTP 端口必须是/)).toBeInTheDocument()
    expect(screen.getByText(/至少需要一个收件人/)).toBeInTheDocument()
    expect(screen.getByText(/首次启用 Telegram/)).toBeInTheDocument()
    expect(screen.getByText(/^启用 Telegram 通知时必须填写 Chat ID 或频道用户名。$/u)).toBeInTheDocument()
    expect(screen.getByText(/严重提醒阈值不能小于/)).toBeInTheDocument()
    expect(screen.getByText(/大小格式/)).toBeInTheDocument()
    expect(screen.getByText(/检查间隔必须使用/)).toBeInTheDocument()
    expect(screen.getByText(/冷却时间必须使用/)).toBeInTheDocument()
    expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
      title: '通知设置格式无效',
      color: 'danger',
    }))
  }, 15_000)

  it('rejects a redacted header that cannot be matched to saved state', async () => {
    const user = userEvent.setup()
    mockGetSettings.mockResolvedValueOnce(configuredSettingsResponse())
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    await replaceInput(
      user,
      screen.getByRole('textbox', { name: 'Webhook 自定义 Header' }),
      'X-Renamed: <redacted>',
    )
    await user.click(screen.getByRole('button', { name: '保存通知设置' }))

    expect(mockUpdateSettings).not.toHaveBeenCalled()
    expect(screen.getByText(/X-Renamed 没有可保留的已保存值/)).toBeInTheDocument()
  }, 15_000)

  it('retries load failures with a fresh signal', async () => {
    const user = userEvent.setup()
    mockGetSettings
      .mockRejectedValueOnce(new SettingsError('settings not available', 503, 'SERVICE_UNAVAILABLE'))
      .mockResolvedValueOnce(settingsResponse({ enabled: true }))

    render(<NotificationSettings />)

    expect(await screen.findByText('通知设置暂不可用')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重新加载' }))
    await waitForNotificationSettings()

    expect(mockGetSettings).toHaveBeenCalledTimes(2)
    const firstSignal = mockGetSettings.mock.calls[0][0]?.signal
    const secondSignal = mockGetSettings.mock.calls[1][0]?.signal
    expect(firstSignal).toBeInstanceOf(AbortSignal)
    expect(secondSignal).toBeInstanceOf(AbortSignal)
    expect(secondSignal).not.toBe(firstSignal)
    expect(screen.getByRole('checkbox', { name: '启用提醒' })).toBeChecked()
  })

  it('keeps the draft dirty after save failures', async () => {
    const user = userEvent.setup()
    mockUpdateSettings.mockRejectedValueOnce(
      new SettingsError('settings not available', 503, 'SERVICE_UNAVAILABLE'),
    )
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    await user.click(screen.getByRole('checkbox', { name: '启用提醒' }))
    await user.click(screen.getByRole('button', { name: '保存通知设置' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '通知设置暂不可用',
        color: 'warning',
      }))
    })
    expect(screen.getByText('有未保存更改')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存通知设置' })).toBeEnabled()
  })

  it('reports generic save failures and preserves the draft', async () => {
    const user = userEvent.setup()
    mockUpdateSettings.mockRejectedValueOnce(new Error('invalid response'))
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    await user.click(screen.getByRole('checkbox', { name: '启用提醒' }))
    await user.click(screen.getByRole('button', { name: '保存通知设置' }))

    await waitFor(() => expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
      title: '保存通知设置失败',
      color: 'danger',
    })))
    expect(screen.getByText('有未保存更改')).toBeInTheDocument()
  })

  it('aborts in-flight load and save requests when unmounted', async () => {
    const loadDeferred = createDeferred<Awaited<ReturnType<typeof getSettings>>>()
    mockGetSettings.mockReturnValueOnce(loadDeferred.promise)
    const loadingView = render(<NotificationSettings />)

    await waitFor(() => expect(mockGetSettings).toHaveBeenCalledTimes(1))
    const loadSignal = mockGetSettings.mock.calls[0][0]?.signal
    loadingView.unmount()
    expect(loadSignal?.aborted).toBe(true)
    await act(async () => {
      loadDeferred.reject(new DOMException('settings load aborted', 'AbortError'))
      await Promise.resolve()
    })

    vi.clearAllMocks()
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => (
      mockAddToast(...args)
    )) as typeof HeroUI.addToast)
    mockGetSettings.mockResolvedValue(settingsResponse())
    const saveDeferred = createDeferred<Awaited<ReturnType<typeof updateSettings>>>()
    mockUpdateSettings.mockReturnValueOnce(saveDeferred.promise)
    const user = userEvent.setup()
    const savingView = render(<NotificationSettings />)
    await waitForNotificationSettings()
    await user.click(screen.getByRole('checkbox', { name: '启用提醒' }))
    await user.click(screen.getByRole('button', { name: '保存通知设置' }))
    await waitFor(() => expect(mockUpdateSettings).toHaveBeenCalledTimes(1))
    const saveSignal = mockUpdateSettings.mock.calls[0][1]?.signal

    savingView.unmount()
    expect(saveSignal?.aborted).toBe(true)
    await act(async () => {
      saveDeferred.reject(new DOMException('settings save aborted', 'AbortError'))
      await Promise.resolve()
    })
    expect(mockAddToast).not.toHaveBeenCalled()
  })

  it('keeps destination groups individually identifiable', async () => {
    render(<NotificationSettings />)
    await waitForNotificationSettings()

    const destinations = screen.getByLabelText('通知设置')
    expect(within(destinations).getByRole('heading', { name: 'Webhook' })).toBeInTheDocument()
    expect(within(destinations).getByRole('heading', { name: '企业微信' })).toBeInTheDocument()
    expect(within(destinations).getByRole('heading', { name: '钉钉' })).toBeInTheDocument()
    expect(within(destinations).getByRole('heading', { name: '邮件' })).toBeInTheDocument()
    expect(within(destinations).getByRole('heading', { name: 'Telegram' })).toBeInTheDocument()
  })
})
