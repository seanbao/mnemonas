import { act } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
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
import { ScrubScheduleSettings } from './ScrubScheduleSettings'

const mockGetSettings = vi.mocked(getSettings)
const mockUpdateSettings = vi.mocked(updateSettings)
const mockAddToast = vi.fn()

function settingsResponse(overrides: Partial<{
  enabled: boolean
  schedule_interval: string
  retry_interval: string
  max_retries: number
}> = {}) {
  return {
    success: true,
    data: {
      maintenance: {
        scrub: {
          enabled: false,
          schedule_interval: '168h',
          retry_interval: '1h',
          max_retries: 1,
          ...overrides,
        },
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
  await screen.findByRole('checkbox', { name: '启用周期校验' })
}

describe('ScrubScheduleSettings', () => {
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

  it('loads the saved schedule with an abort signal', async () => {
    render(<ScrubScheduleSettings />)

    expect(screen.getByRole('status')).toHaveTextContent('加载周期校验设置')
    await waitForSettingsForm()

    expect(mockGetSettings).toHaveBeenCalledTimes(1)
    const options = mockGetSettings.mock.calls[0][0]
    expect(options?.signal).toBeInstanceOf(AbortSignal)
    expect(Object.keys(options ?? {})).toEqual(['signal'])
    expect(screen.getByRole('textbox', { name: '常规间隔' })).toHaveValue('168h')
    expect(screen.getByRole('textbox', { name: '失败重试间隔' })).toHaveValue('1h')
    expect(screen.getByRole('spinbutton', { name: '最大重试次数' })).toHaveValue(1)
  })

  it('marks edits dirty and resets to the saved schedule', async () => {
    const user = userEvent.setup()
    render(<ScrubScheduleSettings />)
    await waitForSettingsForm()

    const scheduleInterval = screen.getByRole('textbox', { name: '常规间隔' })
    await user.clear(scheduleInterval)
    await user.type(scheduleInterval, '336h')

    expect(screen.getByText('有未保存更改')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存计划' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: '重置' }))

    expect(scheduleInterval).toHaveValue('168h')
    expect(screen.queryByText('有未保存更改')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存计划' })).toBeDisabled()
  })

  it('submits only the maintenance scrub domain', async () => {
    const user = userEvent.setup()
    render(<ScrubScheduleSettings />)
    await waitForSettingsForm()

    await user.click(screen.getByRole('checkbox', { name: '启用周期校验' }))
    const scheduleInterval = screen.getByRole('textbox', { name: '常规间隔' })
    await user.clear(scheduleInterval)
    await user.type(scheduleInterval, '336h')
    const retryInterval = screen.getByRole('textbox', { name: '失败重试间隔' })
    await user.clear(retryInterval)
    await user.type(retryInterval, '2h')
    const maxRetries = screen.getByRole('spinbutton', { name: '最大重试次数' })
    await user.clear(maxRetries)
    await user.type(maxRetries, '4')
    await user.click(screen.getByRole('button', { name: '保存计划' }))

    await waitFor(() => expect(mockUpdateSettings).toHaveBeenCalledTimes(1))
    const [request, options] = mockUpdateSettings.mock.calls[0]
    expect(request).toEqual({
      maintenance: {
        scrub: {
          enabled: true,
          schedule_interval: '336h',
          retry_interval: '2h',
          max_retries: 4,
        },
      },
    })
    expect(Object.keys(request)).toEqual(['maintenance'])
    expect(Object.keys(request.maintenance ?? {})).toEqual(['scrub'])
    expect(options?.signal).toBeInstanceOf(AbortSignal)
    expect(Object.keys(options ?? {})).toEqual(['signal'])
    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '周期校验设置已保存',
        color: 'success',
      }))
    })
    expect(screen.getByRole('button', { name: '保存计划' })).toBeDisabled()
  })

  it('rejects invalid duration and retry inputs before submitting', async () => {
    const user = userEvent.setup()
    render(<ScrubScheduleSettings />)
    await waitForSettingsForm()

    const scheduleInterval = screen.getByRole('textbox', { name: '常规间隔' })
    await user.clear(scheduleInterval)
    await user.type(scheduleInterval, '7d')
    const retryInterval = screen.getByRole('textbox', { name: '失败重试间隔' })
    await user.clear(retryInterval)
    await user.type(retryInterval, '0h')
    const maxRetries = screen.getByRole('spinbutton', { name: '最大重试次数' })
    await user.clear(maxRetries)
    await user.type(maxRetries, '-1')
    await user.click(screen.getByRole('button', { name: '保存计划' }))

    expect(mockUpdateSettings).not.toHaveBeenCalled()
    expect(screen.getByText(/常规间隔必须使用/)).toBeInTheDocument()
    expect(screen.getByText(/失败重试间隔必须使用/)).toBeInTheDocument()
    expect(screen.getByText(/最大重试次数必须是 0/)).toBeInTheDocument()
    expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
      title: '周期校验设置格式无效',
      color: 'danger',
    }))
  })

  it('shows a load error and retries with a fresh signal', async () => {
    const user = userEvent.setup()
    mockGetSettings
      .mockRejectedValueOnce(new SettingsError('settings not available', 503, 'SERVICE_UNAVAILABLE'))
      .mockResolvedValueOnce(settingsResponse({ enabled: true }))

    render(<ScrubScheduleSettings />)

    expect(await screen.findByText('周期校验设置暂不可用')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重新加载' }))
    await waitForSettingsForm()

    expect(mockGetSettings).toHaveBeenCalledTimes(2)
    const firstSignal = mockGetSettings.mock.calls[0][0]?.signal
    const secondSignal = mockGetSettings.mock.calls[1][0]?.signal
    expect(firstSignal).toBeInstanceOf(AbortSignal)
    expect(secondSignal).toBeInstanceOf(AbortSignal)
    expect(secondSignal).not.toBe(firstSignal)
    expect(screen.getByRole('checkbox', { name: '启用周期校验' })).toBeChecked()
  })

  it('keeps the draft dirty and reports save failures', async () => {
    const user = userEvent.setup()
    mockUpdateSettings.mockRejectedValueOnce(new SettingsError('settings not available', 503, 'SERVICE_UNAVAILABLE'))
    render(<ScrubScheduleSettings />)
    await waitForSettingsForm()

    await user.click(screen.getByRole('checkbox', { name: '启用周期校验' }))
    await user.click(screen.getByRole('button', { name: '保存计划' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '周期校验设置暂不可用',
        color: 'warning',
      }))
    })
    expect(screen.getByText('有未保存更改')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存计划' })).toBeEnabled()
  })

  it('aborts an in-flight settings load on unmount', async () => {
    const deferred = createDeferred<Awaited<ReturnType<typeof getSettings>>>()
    mockGetSettings.mockReturnValueOnce(deferred.promise)
    const view = render(<ScrubScheduleSettings />)

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
    const view = render(<ScrubScheduleSettings />)
    await waitForSettingsForm()

    await user.click(screen.getByRole('checkbox', { name: '启用周期校验' }))
    await user.click(screen.getByRole('button', { name: '保存计划' }))
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
