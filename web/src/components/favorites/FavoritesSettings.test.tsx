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
import { FavoritesSettings } from './FavoritesSettings'

const mockGetSettings = vi.mocked(getSettings)
const mockUpdateSettings = vi.mocked(updateSettings)
const mockAddToast = vi.fn()

function settingsResponse(enabled = true) {
  return {
    success: true,
    data: {
      favorites: { enabled },
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

async function waitForSettingsSwitch() {
  return screen.findByRole('checkbox', { name: '启用收藏功能' })
}

describe('FavoritesSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
    mockGetSettings.mockResolvedValue(settingsResponse(true))
    mockUpdateSettings.mockResolvedValue({
      success: true,
      warning: false,
      message: 'settings updated',
    })
  })

  it('loads the saved setting with an abort signal', async () => {
    render(<FavoritesSettings />)

    expect(screen.getByRole('status')).toHaveTextContent('加载收藏设置')
    const settingSwitch = await waitForSettingsSwitch()

    expect(mockGetSettings).toHaveBeenCalledTimes(1)
    const options = mockGetSettings.mock.calls[0][0]
    expect(options?.signal).toBeInstanceOf(AbortSignal)
    expect(Object.keys(options ?? {})).toEqual(['signal'])
    expect(settingSwitch).toBeChecked()
  })

  it('defaults to enabled when the optional favorites domain is absent', async () => {
    mockGetSettings.mockResolvedValue({
      success: true,
      data: {},
    } as unknown as Awaited<ReturnType<typeof getSettings>>)

    render(<FavoritesSettings />)

    expect(await waitForSettingsSwitch()).toBeChecked()
  })

  it('marks edits dirty and resets to the saved setting', async () => {
    const user = userEvent.setup()
    render(<FavoritesSettings />)
    const settingSwitch = await waitForSettingsSwitch()

    await user.click(settingSwitch)

    expect(screen.getByText('有未保存更改')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存设置' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: '重置' }))

    expect(settingSwitch).toBeChecked()
    expect(screen.queryByText('有未保存更改')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存设置' })).toBeDisabled()
  })

  it('submits only the favorites domain with an abort signal', async () => {
    const user = userEvent.setup()
    render(<FavoritesSettings />)
    const settingSwitch = await waitForSettingsSwitch()

    await user.click(settingSwitch)
    await user.click(screen.getByRole('button', { name: '保存设置' }))

    await waitFor(() => expect(mockUpdateSettings).toHaveBeenCalledTimes(1))
    const [request, options] = mockUpdateSettings.mock.calls[0]
    expect(request).toEqual({ favorites: { enabled: false } })
    expect(Object.keys(request)).toEqual(['favorites'])
    expect(Object.keys(request.favorites ?? {})).toEqual(['enabled'])
    expect(options?.signal).toBeInstanceOf(AbortSignal)
    expect(Object.keys(options ?? {})).toEqual(['signal'])
    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '收藏设置已保存',
        color: 'success',
      }))
    })
    expect(screen.queryByText('有未保存更改')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存设置' })).toBeDisabled()
  })

  it('shows a load error and retries with a fresh signal', async () => {
    const user = userEvent.setup()
    mockGetSettings
      .mockRejectedValueOnce(new SettingsError('settings unavailable', 503, 'SERVICE_UNAVAILABLE'))
      .mockResolvedValueOnce(settingsResponse(false))

    render(<FavoritesSettings />)

    expect(await screen.findByText('收藏设置暂不可用')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重新加载' }))
    const settingSwitch = await waitForSettingsSwitch()

    expect(mockGetSettings).toHaveBeenCalledTimes(2)
    const firstSignal = mockGetSettings.mock.calls[0][0]?.signal
    const secondSignal = mockGetSettings.mock.calls[1][0]?.signal
    expect(firstSignal).toBeInstanceOf(AbortSignal)
    expect(secondSignal).toBeInstanceOf(AbortSignal)
    expect(secondSignal).not.toBe(firstSignal)
    expect(settingSwitch).not.toBeChecked()
  })

  it('keeps the draft dirty and reports save failures', async () => {
    const user = userEvent.setup()
    mockUpdateSettings.mockRejectedValueOnce(new SettingsError('settings unavailable', 503, 'SERVICE_UNAVAILABLE'))
    render(<FavoritesSettings />)
    const settingSwitch = await waitForSettingsSwitch()

    await user.click(settingSwitch)
    await user.click(screen.getByRole('button', { name: '保存设置' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '收藏设置暂不可用',
        color: 'warning',
      }))
    })
    expect(screen.getByText('有未保存更改')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存设置' })).toBeEnabled()
  })

  it('aborts an in-flight settings load on unmount', async () => {
    const deferred = createDeferred<Awaited<ReturnType<typeof getSettings>>>()
    mockGetSettings.mockReturnValueOnce(deferred.promise)
    const view = render(<FavoritesSettings />)

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
    const view = render(<FavoritesSettings />)
    const settingSwitch = await waitForSettingsSwitch()

    await user.click(settingSwitch)
    await user.click(screen.getByRole('button', { name: '保存设置' }))
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
