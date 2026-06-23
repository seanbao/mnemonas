import { act } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import * as HeroUI from '@heroui/react'
import { render, screen, waitFor } from '@/test/utils'

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
  updateSettings: vi.fn(),
}))

import { getSettings, SettingsError, updateSettings } from '@/api/settings'
import { DiskHealthSettings } from './DiskHealthSettings'

const mockGetSettings = vi.mocked(getSettings)
const mockUpdateSettings = vi.mocked(updateSettings)
const mockAddToast = vi.fn()

function settingsResponse(overrides: Partial<{
  enabled: boolean
  check_interval: string
  probe_timeout: string
  cooldown_period: string
  command: string
  temperature_warning_c: number
  temperature_critical_c: number
  media_wear_warning_percent: number
  media_wear_critical_percent: number
  devices: Array<{
    path: string
    name?: string
    type?: string
    serial?: string
    temperature_warning_c?: number
    temperature_critical_c?: number
  }>
}> = {}) {
  return {
    success: true,
    data: {
      disk_health: {
        enabled: true,
        check_interval: '1h',
        probe_timeout: '15s',
        cooldown_period: '4h',
        command: 'smartctl',
        temperature_warning_c: 50,
        temperature_critical_c: 60,
        media_wear_warning_percent: 80,
        media_wear_critical_percent: 100,
        devices: [{
          path: '/dev/disk/by-id/test',
          name: 'Data',
          type: 'sat',
          serial: 'SER123',
          temperature_warning_c: 45,
          temperature_critical_c: 55,
        }],
        ...overrides,
      },
    },
  } as unknown as Awaited<ReturnType<typeof getSettings>>
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

async function waitForSettingsForm() {
  await screen.findByRole('checkbox', { name: '启用磁盘健康检查' })
}

function changeField(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } })
}

describe('DiskHealthSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
    mockGetSettings.mockResolvedValue(settingsResponse())
    mockUpdateSettings.mockResolvedValue({
      success: true,
      message: 'settings updated',
      warning: false,
    })
  })

  it('loads every saved disk health field with an abort signal', async () => {
    render(<DiskHealthSettings />)

    expect(screen.getByRole('status')).toHaveTextContent('加载磁盘健康设置')
    await waitForSettingsForm()

    expect(mockGetSettings).toHaveBeenCalledTimes(1)
    const options = mockGetSettings.mock.calls[0][0]
    expect(options?.signal).toBeInstanceOf(AbortSignal)
    expect(Object.keys(options ?? {})).toEqual(['signal'])
    expect(screen.getByLabelText('磁盘健康检查间隔')).toHaveValue('1h')
    expect(screen.getByLabelText('磁盘健康探测超时')).toHaveValue('15s')
    expect(screen.getByLabelText('磁盘健康冷却时间')).toHaveValue('4h')
    expect(screen.getByLabelText('磁盘健康探测命令')).toHaveValue('smartctl')
    expect(screen.getByLabelText('磁盘温度提醒阈值')).toHaveValue(50)
    expect(screen.getByLabelText('磁盘温度严重阈值')).toHaveValue(60)
    expect(screen.getByLabelText('介质磨损提醒阈值')).toHaveValue(80)
    expect(screen.getByLabelText('介质磨损严重阈值')).toHaveValue(100)
    expect(screen.getByLabelText('磁盘健康设备列表')).toHaveValue('/dev/disk/by-id/test | Data | sat | SER123 | 45 | 55')
  })

  it('marks edits dirty and resets to the saved values', async () => {
    const user = userEvent.setup()
    render(<DiskHealthSettings />)
    await waitForSettingsForm()

    changeField('磁盘健康检查间隔', '2h')
    expect(screen.getByText('有未保存更改')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存磁盘健康设置' })).toBeEnabled()

    await user.click(screen.getByRole('button', { name: '重置' }))
    expect(screen.getByLabelText('磁盘健康检查间隔')).toHaveValue('1h')
    expect(screen.queryByText('有未保存更改')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存磁盘健康设置' })).toBeDisabled()
  })

  it('submits only the disk_health domain and normalizes its values', async () => {
    const user = userEvent.setup()
    render(<DiskHealthSettings />)
    await waitForSettingsForm()

    changeField('磁盘健康检查间隔', ' 2h ')
    changeField('磁盘健康探测超时', ' 20s ')
    changeField('磁盘健康冷却时间', ' 6h ')
    changeField('磁盘健康探测命令', ' /usr/sbin/smartctl ')
    changeField('磁盘温度提醒阈值', '48')
    changeField('磁盘温度严重阈值', '58')
    changeField('介质磨损提醒阈值', '75')
    changeField('介质磨损严重阈值', '90')
    changeField('磁盘健康设备列表', '/dev/sda | Cache | sat | SER456 | 46 | 56')
    await user.click(screen.getByRole('button', { name: '保存磁盘健康设置' }))

    await waitFor(() => expect(mockUpdateSettings).toHaveBeenCalledTimes(1))
    const [request, options] = mockUpdateSettings.mock.calls[0]
    expect(request).toEqual({
      disk_health: {
        enabled: true,
        check_interval: '2h',
        probe_timeout: '20s',
        cooldown_period: '6h',
        command: '/usr/sbin/smartctl',
        temperature_warning_c: 48,
        temperature_critical_c: 58,
        media_wear_warning_percent: 75,
        media_wear_critical_percent: 90,
        devices: [{
          path: '/dev/sda',
          name: 'Cache',
          type: 'sat',
          serial: 'SER456',
          temperature_warning_c: 46,
          temperature_critical_c: 56,
        }],
      },
    })
    expect(Object.keys(request)).toEqual(['disk_health'])
    expect(options?.signal).toBeInstanceOf(AbortSignal)
    expect(Object.keys(options ?? {})).toEqual(['signal'])
    expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
      title: '磁盘健康设置已保存',
      color: 'success',
    }))
    expect(screen.getByRole('button', { name: '保存磁盘健康设置' })).toBeDisabled()
  })

  it.each([
    ['磁盘健康检查间隔', '0h', '磁盘健康检查间隔格式无效'],
    ['磁盘健康探测超时', '15', '磁盘健康探测超时格式无效'],
    ['磁盘健康冷却时间', '4d', '磁盘健康冷却时间格式无效'],
    ['磁盘健康探测命令', 'smart ctl', '磁盘健康命令格式无效'],
    ['磁盘温度提醒阈值', '-1', '磁盘温度提醒阈值格式无效'],
    ['磁盘温度严重阈值', '1.5', '磁盘温度严重阈值格式无效'],
    ['介质磨损提醒阈值', '101', '介质磨损提醒阈值格式无效'],
    ['介质磨损严重阈值', '1.5', '介质磨损严重阈值格式无效'],
  ])('rejects invalid %s values', async (label, value, title) => {
    const user = userEvent.setup()
    render(<DiskHealthSettings />)
    await waitForSettingsForm()

    changeField(label, value)
    await user.click(screen.getByRole('button', { name: '保存磁盘健康设置' }))

    expect(mockUpdateSettings).not.toHaveBeenCalled()
    expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title, color: 'danger' }))
    expect(screen.getByRole('alert')).toBeInTheDocument()
  })

  it.each([
    ['磁盘温度提醒阈值', '70', '磁盘温度阈值关系无效'],
    ['介质磨损严重阈值', '70', '介质磨损阈值关系无效'],
  ])('rejects invalid threshold relationships starting at %s', async (label, value, title) => {
    const user = userEvent.setup()
    render(<DiskHealthSettings />)
    await waitForSettingsForm()

    changeField(label, value)
    await user.click(screen.getByRole('button', { name: '保存磁盘健康设置' }))

    expect(mockUpdateSettings).not.toHaveBeenCalled()
    expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title, color: 'danger' }))
  })

  it.each([
    ['/dev/sda | Data | sat | SER | 45 | 55 | extra', '第 1 行最多包含 6 列'],
    ['sda | Data', '第 1 行设备路径必须是绝对路径'],
    ['/dev/sda | Data | sat | SER | invalid | 55', '第 1 行 温度提醒阈值 必须是 0 或不超过安全范围的整数'],
    ['/dev/sda | Data | sat | SER | 60 | 55', '第 1 行温度严重阈值不能小于提醒阈值'],
  ])('rejects invalid device line %s', async (devices, description) => {
    const user = userEvent.setup()
    render(<DiskHealthSettings />)
    await waitForSettingsForm()

    changeField('磁盘健康设备列表', devices)
    await user.click(screen.getByRole('button', { name: '保存磁盘健康设置' }))

    expect(mockUpdateSettings).not.toHaveBeenCalled()
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '磁盘健康设备格式无效',
      description,
      color: 'danger',
    })
  })

  it('shows a load error and retries with a fresh signal', async () => {
    const user = userEvent.setup()
    mockGetSettings
      .mockRejectedValueOnce(new SettingsError('settings not available', 503, 'SERVICE_UNAVAILABLE'))
      .mockResolvedValueOnce(settingsResponse({ enabled: false }))

    render(<DiskHealthSettings />)

    expect(await screen.findByText('磁盘健康设置暂不可用')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重新加载' }))
    await waitForSettingsForm()

    expect(mockGetSettings).toHaveBeenCalledTimes(2)
    const firstSignal = mockGetSettings.mock.calls[0][0]?.signal
    const secondSignal = mockGetSettings.mock.calls[1][0]?.signal
    expect(firstSignal).toBeInstanceOf(AbortSignal)
    expect(secondSignal).toBeInstanceOf(AbortSignal)
    expect(secondSignal).not.toBe(firstSignal)
    expect(screen.getByRole('checkbox', { name: '启用磁盘健康检查' })).not.toBeChecked()
  })

  it('keeps the draft dirty and reports save failures', async () => {
    const user = userEvent.setup()
    mockUpdateSettings.mockRejectedValueOnce(new SettingsError('settings not available', 503, 'SERVICE_UNAVAILABLE'))
    render(<DiskHealthSettings />)
    await waitForSettingsForm()

    await user.click(screen.getByRole('checkbox', { name: '启用磁盘健康检查' }))
    await user.click(screen.getByRole('button', { name: '保存磁盘健康设置' }))

    await waitFor(() => expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
      title: '磁盘健康设置暂不可用',
      color: 'warning',
    })))
    expect(screen.getByText('有未保存更改')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存磁盘健康设置' })).toBeEnabled()
  })

  it('aborts an in-flight settings load on unmount', async () => {
    const deferred = createDeferred<Awaited<ReturnType<typeof getSettings>>>()
    mockGetSettings.mockReturnValueOnce(deferred.promise)
    const view = render(<DiskHealthSettings />)

    await waitFor(() => expect(mockGetSettings).toHaveBeenCalledTimes(1))
    const signal = mockGetSettings.mock.calls[0][0]?.signal
    expect(signal).toBeInstanceOf(AbortSignal)

    view.unmount()
    expect(signal?.aborted).toBe(true)
    await act(async () => {
      deferred.reject(new DOMException('settings load aborted', 'AbortError'))
      await Promise.resolve()
    })
    expect(mockAddToast).not.toHaveBeenCalled()
  })

  it('aborts an in-flight save on unmount', async () => {
    const user = userEvent.setup()
    const deferred = createDeferred<Awaited<ReturnType<typeof updateSettings>>>()
    mockUpdateSettings.mockReturnValueOnce(deferred.promise)
    const view = render(<DiskHealthSettings />)
    await waitForSettingsForm()

    await user.click(screen.getByRole('checkbox', { name: '启用磁盘健康检查' }))
    await user.click(screen.getByRole('button', { name: '保存磁盘健康设置' }))
    await waitFor(() => expect(mockUpdateSettings).toHaveBeenCalledTimes(1))
    const signal = mockUpdateSettings.mock.calls[0][1]?.signal
    expect(signal).toBeInstanceOf(AbortSignal)

    view.unmount()
    expect(signal?.aborted).toBe(true)
    await act(async () => {
      deferred.reject(new DOMException('settings save aborted', 'AbortError'))
      await Promise.resolve()
    })
    expect(mockAddToast).not.toHaveBeenCalled()
  })
})
