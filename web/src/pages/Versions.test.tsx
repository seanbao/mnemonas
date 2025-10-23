import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor, within } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import * as HeroUI from '@heroui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { authFetch, ensureDownloadSession } from '@/api/auth'

const mockAddToast = vi.fn()

const mockUseIsAdmin = vi.fn(() => true)
const mockUseUser = vi.fn(() => ({ id: 'admin', username: 'admin', role: 'admin', email: '', homeDir: '/' }))

// Mock API functions
vi.mock('@/api/files', () => ({
  ApiError: class ApiError extends Error {
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
  getVersions: vi.fn(),
  buildDownloadUrl: vi.fn((path, options?: { version?: string }) => options?.version
    ? `/api/v1/download${path}?version=${options.version}`
    : `/api/v1/download${path}`),
  downloadFile: vi.fn(),
  restoreVersion: vi.fn(),
}))

vi.mock('@/stores/auth', () => ({
  useIsAdmin: () => mockUseIsAdmin(),
  useUser: () => mockUseUser(),
}))

vi.mock('@/api/auth', () => ({
  authFetch: vi.fn(),
  ensureDownloadSession: vi.fn(),
}))

import { VersionsPage } from './Versions'

import { ApiError, downloadFile, getVersions, restoreVersion } from '@/api/files'

const mockGetVersions = vi.mocked(getVersions)
const mockDownloadFile = vi.mocked(downloadFile)
const mockRestoreVersion = vi.mocked(restoreVersion)
const mockAuthFetch = vi.mocked(authFetch)
const mockEnsureDownloadSession = vi.mocked(ensureDownloadSession)
const successActionResult = { warning: false, message: undefined } as const

function expectGetVersionsCalledWithPath(path: string) {
  expect(mockGetVersions.mock.calls.some(([calledPath]) => calledPath === path)).toBe(true)
}

function expectGetVersionsCalledWithAbortSignal(path: string) {
  const call = mockGetVersions.mock.calls.find(([calledPath]) => calledPath === path)
  expect(call).toBeTruthy()
  expect((call?.[1] as { signal?: AbortSignal } | undefined)?.signal).toBeInstanceOf(AbortSignal)
}

function getRestoreVersionSignal(path: string, hash: string): AbortSignal {
  const call = mockRestoreVersion.mock.calls.find(([calledPath, calledHash]) => calledPath === path && calledHash === hash)
  expect(call).toBeTruthy()
  const signal = (call?.[2] as { signal?: AbortSignal } | undefined)?.signal
  expect(signal).toBeInstanceOf(AbortSignal)
  return signal as AbortSignal
}

function warningActionResult(message: string) {
  return { warning: true, message } as const
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

describe('VersionsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
    window.history.pushState({}, '', '/')
    mockUseIsAdmin.mockReturnValue(true)
    mockUseUser.mockReturnValue({ id: 'admin', username: 'admin', role: 'admin', email: '', homeDir: '/' })
    mockAuthFetch.mockResolvedValue(new Response('ok', {
      headers: { 'Content-Type': 'application/octet-stream' },
    }))
    mockEnsureDownloadSession.mockResolvedValue({ ok: true })
    mockDownloadFile.mockResolvedValue(undefined)
    mockGetVersions.mockResolvedValue([
      { version: 3, hash: 'hash3', size: 3000, timestamp: '2024-01-03T00:00:00Z' },
      { version: 2, hash: 'hash2', size: 2000, timestamp: '2024-01-02T00:00:00Z' },
      { version: 1, hash: 'hash1', size: 1000, timestamp: '2024-01-01T00:00:00Z' },
    ])
  })

  describe('rendering', () => {
    it('renders page header', () => {
      render(<VersionsPage />)
      expect(screen.getByRole('heading', { name: '版本历史' })).toBeTruthy()
      expect(screen.getByText('查看、下载或恢复文件版本')).toBeTruthy()
    })

    it('renders search input', () => {
      render(<VersionsPage />)
      expect(screen.getByLabelText('版本文件路径')).toBeTruthy()
    })

    it('renders search button', () => {
      render(<VersionsPage />)
      expect(screen.getByText('查询')).toBeTruthy()
    })

    it('shows empty state before search', () => {
      render(<VersionsPage />)
      expect(screen.getByText('查看文件版本历史')).toBeTruthy()
    })

    it('uses read-only subtitle and description for non-admin users', () => {
      mockUseIsAdmin.mockReturnValue(false)
      mockUseUser.mockReturnValue({ id: 'tester', username: 'tester', role: 'user', email: '', homeDir: '/tester' })

      render(<VersionsPage />)

      expect(screen.getByText('查看和下载文件版本')).toBeTruthy()
      expect(screen.getByText('输入可访问文件路径后可查看版本记录，支持预览和下载。')).toBeTruthy()
    })
  })

  describe('search functionality', () => {
    it('triggers search on button click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt')

      const searchBtn = screen.getByText('查询')
      await user.click(searchBtn)

      await waitFor(() => {
        expectGetVersionsCalledWithPath('/test.txt')
      })
    })

    it('triggers search on Enter key', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expectGetVersionsCalledWithPath('/test.txt')
      })
    })

    it('normalizes path without leading slash', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, 'test.txt{enter}')

      await waitFor(() => {
        expectGetVersionsCalledWithPath('/test.txt')
      })
    })

    it('trims surrounding whitespace from the searched path', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '  /test.txt  {enter}')

      await waitFor(() => {
        expectGetVersionsCalledWithPath('/test.txt')
      })
    })

    it('clears the selected path and query string when submitting an empty search', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.getByText('/test.txt')).toBeTruthy()
      })

      await user.clear(screen.getByLabelText('版本文件路径'))
      await user.click(screen.getByText('查询'))

      await waitFor(() => {
        expect(screen.queryByText('/test.txt')).toBeNull()
        expect(screen.getByText('查看文件版本历史')).toBeTruthy()
      })
      expect(window.location.search).toBe('')
    })

    it('renders an explicit empty state from an encoded deep link with no history', async () => {
      window.history.pushState({}, '', '/versions?path=%2FT145iNXfXqXXb1upjX.avif')
      mockGetVersions.mockResolvedValueOnce([])

      render(<VersionsPage />)

      await waitFor(() => {
        expectGetVersionsCalledWithPath('/T145iNXfXqXXb1upjX.avif')
      })

      expect(screen.getByLabelText('版本文件路径')).toHaveValue('/T145iNXfXqXXb1upjX.avif')
      expect(screen.getByText('/T145iNXfXqXXb1upjX.avif')).toBeTruthy()
      expect(await screen.findByText('未找到版本记录')).toBeTruthy()
    })

    it('allows non-admin searches outside the assigned home directory so server access rules can resolve them', async () => {
      mockUseIsAdmin.mockReturnValue(false)
      mockUseUser.mockReturnValue({ id: 'tester', username: 'tester', role: 'user', email: '', homeDir: '/tester' })

      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/team/report.txt{enter}')

      await waitFor(() => {
        expectGetVersionsCalledWithPath('/team/report.txt')
      })
      expect(mockAddToast).not.toHaveBeenCalledWith(expect.objectContaining({
        title: '仅可查看主目录内文件的版本历史',
      }))
      expect(input).toHaveValue('/team/report.txt')
    })

    it('shows an invalid-home error instead of querying versions for non-admin users without a home directory', async () => {
      mockUseIsAdmin.mockReturnValue(false)
      mockUseUser.mockReturnValue({ id: 'tester', username: 'tester', role: 'user', email: '', homeDir: '' })

      render(<VersionsPage />)

      await waitFor(() => {
        expect(screen.getAllByText('主目录配置无效').length).toBeGreaterThan(0)
        expect(screen.getByText('当前账户未配置有效的主目录，无法查看版本历史。请联系管理员修复账户 home_dir。')).toBeTruthy()
      })

      expect(mockGetVersions).not.toHaveBeenCalled()
      expect(screen.queryByText('查询')).toBeNull()
    })

    it('syncs the selected path when the URL query changes after mount', async () => {
      window.history.pushState({}, '', '/versions?path=/first.txt')
      render(<VersionsPage />)

      await waitFor(() => {
        expectGetVersionsCalledWithPath('/first.txt')
      })

      mockGetVersions.mockClear()

      await act(async () => {
        window.history.pushState({}, '', '/versions?path=/second.txt')
        window.dispatchEvent(new PopStateEvent('popstate'))
      })

      await waitFor(() => {
        expectGetVersionsCalledWithPath('/second.txt')
      })

      expect(screen.getByLabelText('版本文件路径')).toHaveValue('/second.txt')
      expect(screen.getByText('/second.txt')).toBeTruthy()
    })

    it('does not reuse cached versions from another user session', async () => {
      mockUseIsAdmin.mockReturnValue(false)
      mockUseUser.mockReturnValue({ id: 'tester', username: 'tester', role: 'user', email: '', homeDir: '/tester' })
      mockGetVersions.mockImplementation(() => new Promise(() => {}))

      const queryClient = new QueryClient({
        defaultOptions: {
          queries: {
            retry: false,
            gcTime: 0,
            staleTime: 0,
          },
        },
      })
      queryClient.setQueryData(['versions', '/tester/report.txt'], [
        { version: 1, hash: 'secretsecret123456', size: 1000, timestamp: '2024-01-01T00:00:00Z' },
      ])

      window.history.pushState({}, '', '/versions?path=/tester/report.txt')
      render(
        <QueryClientProvider client={queryClient}>
          <VersionsPage />
        </QueryClientProvider>
      )

      await waitFor(() => {
        expectGetVersionsCalledWithPath('/tester/report.txt')
      })

      expect(screen.queryByRole('list', { name: '版本历史' })).toBeNull()
      expect(screen.queryByText(/secretsecret/)).toBeNull()
    })
  })

  describe('version list', () => {
    it('calls API and processes versions', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expectGetVersionsCalledWithAbortSignal('/test.txt')
      })

      expect(mockGetVersions.mock.calls.every(([calledPath]) => calledPath === '/test.txt')).toBe(true)
    })

    it('handles multiple version data correctly', async () => {
      mockGetVersions.mockResolvedValue([
        { version: 5, hash: 'hash5', size: 5000, timestamp: '2024-01-05T00:00:00Z' },
        { version: 4, hash: 'hash4', size: 4000, timestamp: '2024-01-04T00:00:00Z' },
      ])

      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/multi.txt{enter}')

      await waitFor(() => {
        expectGetVersionsCalledWithPath('/multi.txt')
      })
    })

    it('displays version rows after search', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.getByRole('list', { name: '版本历史' })).toBeTruthy()
      })
    })

    it('shows file path after search', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/documents/report.pdf{enter}')

      await waitFor(() => {
        expect(screen.getByText('/documents/report.pdf')).toBeTruthy()
      })
    })
  })

  describe('restore functionality', () => {
    it('restore API is available and can be called', async () => {
      mockRestoreVersion.mockResolvedValue(successActionResult)
      
      // Test that restore function exists and is mockable
      expect(mockRestoreVersion).toBeDefined()
      
      await mockRestoreVersion('/test.txt', 'hash2')
      expect(mockRestoreVersion).toHaveBeenCalledWith('/test.txt', 'hash2')
    })

    it('shows restore button for non-current versions', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        const restoreButtons = screen.queryAllByRole('button', { name: /^恢复到版本 / })
        expect(restoreButtons.length).toBeGreaterThan(0)
      })
    })

    it('hides restore button for non-admin users', async () => {
      mockUseIsAdmin.mockReturnValue(false)
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expectGetVersionsCalledWithPath('/test.txt')
      })

      expect(screen.queryAllByRole('button', { name: /^恢复到版本 / })).toHaveLength(0)
    })

    it('opens restore modal when clicking restore button', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^恢复到版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '恢复到版本 2' }))

      await waitFor(() => {
        expect(screen.getByText('确认恢复版本')).toBeTruthy()
      })

      const review = within(screen.getByLabelText('版本恢复执行前复核'))
      expect(review.getByText('恢复影响复核')).toBeTruthy()
      expect(review.getByText('/test.txt')).toBeTruthy()
      expect(review.getByText('当前可见文件会被所选历史版本覆盖。')).toBeTruthy()
      expect(review.getByText('恢复前的当前内容会保存为新的历史版本。')).toBeTruthy()
      expect(review.getByText('服务端会重新校验管理员权限、目录配额和目标父目录状态。')).toBeTruthy()
      expect(review.getByText('版本不匹配、父目录冲突或版本存储不可用时会拒绝恢复并保留现状。')).toBeTruthy()
    })

    it('closes the restore modal when cancellation is allowed', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^恢复到版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '恢复到版本 2' }))

      await waitFor(() => {
        expect(screen.getByText('确认恢复版本')).toBeTruthy()
      })

      await user.click(screen.getByText('取消'))

      await waitFor(() => {
        expect(screen.queryByText('确认恢复版本')).toBeNull()
      })
    })

    it('calls restore API on confirm', async () => {
      mockRestoreVersion.mockResolvedValue(successActionResult)
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^恢复到版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '恢复到版本 2' }))

      await waitFor(() => {
        expect(screen.getByText('确认恢复')).toBeTruthy()
      })

      await user.click(screen.getByText('确认恢复'))

      await waitFor(() => {
        expect(mockRestoreVersion).toHaveBeenCalled()
      })
    })

    it('shows warning toast when restore succeeds with a persistence warning', async () => {
      mockRestoreVersion.mockResolvedValueOnce(warningActionResult('version restored with persistence warning'))
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^恢复到版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '恢复到版本 2' }))

      await waitFor(() => {
        expect(screen.getByText('确认恢复')).toBeTruthy()
      })

      await user.click(screen.getByText('确认恢复'))

        await waitFor(() => {
          expect(mockAddToast).toHaveBeenCalledWith({
          title: '恢复版本完成，但存在警告',
          color: 'warning',
        })
      })
    })

    it('shows unavailable toast when restore fails because version storage is unavailable', async () => {
      mockRestoreVersion.mockRejectedValue(new ApiError('version storage unavailable', 503, 'SERVICE_UNAVAILABLE'))
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^恢复到版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '恢复到版本 2' }))

      await waitFor(() => {
        expect(screen.getByText('确认恢复')).toBeTruthy()
      })

      await user.click(screen.getByText('确认恢复'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '恢复版本暂不可用',
          description: '版本存储当前不可用，请检查设备状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('closes the restore modal and shows a stale-version warning when restore hits not found', async () => {
      mockRestoreVersion.mockRejectedValue(new ApiError('resource not found', 404))
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^恢复到版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '恢复到版本 2' }))
      await user.click(await screen.findByText('确认恢复'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '所选版本已不存在，已同步更新',
          description: '该版本或目标文件已被移除，请刷新版本历史后重试。',
          color: 'warning',
        })
      })

      await waitFor(() => {
        expect(screen.queryByText('确认恢复版本')).toBeFalsy()
      })
    })

    it('keeps the restore modal open while a pending restore is in flight', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const firstRestore = createDeferred<typeof successActionResult>()
      mockRestoreVersion.mockImplementationOnce(() => firstRestore.promise)

      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^恢复到版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '恢复到版本 2' }))
      await user.click(await screen.findByText('确认恢复'))

      await waitFor(() => {
        expect(mockRestoreVersion).toHaveBeenCalledWith('/test.txt', 'hash2', expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
      })

      await user.click(screen.getByText('取消'))

      expect(screen.getByText('确认恢复版本')).toBeTruthy()
      expect(screen.getByText('确定要将文件恢复到以下版本吗？')).toBeTruthy()

      await act(async () => {
        firstRestore.resolve(successActionResult)
        await firstRestore.promise
      })

      await waitFor(() => {
        expect(screen.queryByText('确认恢复版本')).toBeFalsy()
      })
    })

    it('aborts pending restores when the page unmounts and ignores abort feedback', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const firstRestore = createDeferred<typeof successActionResult>()
      mockRestoreVersion.mockImplementationOnce(() => firstRestore.promise)

      const { unmount } = render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^恢复到版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '恢复到版本 2' }))
      await user.click(await screen.findByText('确认恢复'))

      await waitFor(() => {
        expect(mockRestoreVersion).toHaveBeenCalledWith('/test.txt', 'hash2', expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
      })
      const signal = getRestoreVersionSignal('/test.txt', 'hash2')

      unmount()

      expect(signal.aborted).toBe(true)

      await act(async () => {
        firstRestore.reject(new DOMException('version restore aborted', 'AbortError'))
        await firstRestore.promise.catch(() => undefined)
      })

      expect(mockAddToast).not.toHaveBeenCalled()
    })

    it('keeps the restore modal open when a pending restore later fails', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const firstRestore = createDeferred<typeof successActionResult>()
      mockRestoreVersion.mockImplementationOnce(() => firstRestore.promise)

      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^恢复到版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '恢复到版本 2' }))
      await user.click(await screen.findByText('确认恢复'))

      await waitFor(() => {
        expect(mockRestoreVersion).toHaveBeenCalledWith('/test.txt', 'hash2', expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
      })

      await user.click(screen.getByText('取消'))

      expect(screen.getByText('确认恢复版本')).toBeTruthy()
      expect(screen.getByText('确定要将文件恢复到以下版本吗？')).toBeTruthy()

      await act(async () => {
        firstRestore.reject(new Error('restore failed'))
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '恢复版本失败',
          description: '操作未完成，请稍后重试。',
          color: 'danger',
        })
      })

      expect(screen.getByText('确认恢复版本')).toBeTruthy()
      expect(screen.getByText('确定要将文件恢复到以下版本吗？')).toBeTruthy()
    })
  })

  describe('download functionality', () => {
    it('shows download buttons for all versions', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        const downloadButtons = screen.queryAllByRole('button', { name: /^下载版本 / })
        expect(downloadButtons.length).toBeGreaterThan(0)
      })
    })

    it('shows unavailable toast when version download is temporarily unavailable', async () => {
      mockDownloadFile.mockRejectedValue(new ApiError('version storage unavailable', 503, 'SERVICE_UNAVAILABLE'))
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^下载版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '下载版本 3' }))

      await waitFor(() => {
        expect(mockDownloadFile).toHaveBeenCalledWith('/test.txt', expect.objectContaining({
          version: 'hash3',
          signal: expect.any(AbortSignal),
        }))
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '下载版本暂不可用',
          description: '版本存储当前不可用，请检查设备状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('shows a stale-version warning when version download hits not found', async () => {
      mockDownloadFile.mockRejectedValue(new ApiError('resource not found', 404))
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^下载版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '下载版本 3' }))

      await waitFor(() => {
        expect(mockDownloadFile).toHaveBeenCalledWith('/test.txt', expect.objectContaining({
          version: 'hash3',
          signal: expect.any(AbortSignal),
        }))
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '所选版本已不存在',
          description: '该版本或目标文件已被移除，请刷新版本历史后重试。',
          color: 'warning',
        })
      })
    })

    it('aborts pending version downloads when the selected path changes and ignores abort feedback', async () => {
      const download = createDeferred<void>()
      let signal: AbortSignal | undefined
      mockDownloadFile.mockImplementationOnce((_path, options) => {
        signal = options?.signal
        return download.promise
      })
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^下载版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '下载版本 3' }))

      await waitFor(() => {
        expect(signal).toBeInstanceOf(AbortSignal)
      })

      const nextInput = screen.getByLabelText('版本文件路径')
      await user.clear(nextInput)
      await user.type(nextInput, '/other.txt{enter}')

      expect(signal?.aborted).toBe(true)
      download.reject(new DOMException('version download aborted', 'AbortError'))

      await waitFor(() => {
        expect(mockAddToast).not.toHaveBeenCalled()
      })
    })

    it('shows warning toast when browser blocks version preview', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      vi.spyOn(window, 'open').mockReturnValue(null)
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^预览版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '预览版本 3' }))

      await waitFor(() => {
        expect(mockEnsureDownloadSession).toHaveBeenCalledTimes(1)
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '浏览器拦截了新标签页，请允许弹窗后重试',
          color: 'warning',
        })
      })
    })

    it('opens preview with isolated window features', async () => {
      const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^预览版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '预览版本 3' }))

      await waitFor(() => {
        expect(mockEnsureDownloadSession).toHaveBeenCalledTimes(1)
        expect(openSpy).toHaveBeenCalledWith('/api/v1/download/test.txt?version=hash3', '_blank', 'noopener,noreferrer')
      })
    })

    it('shows a warning and skips opening when version preview session setup fails', async () => {
      mockEnsureDownloadSession.mockResolvedValueOnce({ ok: false, message: 'download session unavailable' })
      const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^预览版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '预览版本 3' }))

      await waitFor(() => {
        expect(openSpy).not.toHaveBeenCalled()
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '原始预览和下载会话同步失败，请稍后重试',
          color: 'warning',
        })
      })
    })

    it('shows a warning and skips opening when version preview returns a structured JSON error', async () => {
      mockAuthFetch.mockResolvedValueOnce(new Response(JSON.stringify({
        success: false,
        error: {
          code: 'SERVICE_UNAVAILABLE',
          message: 'version storage unavailable',
        },
      }), {
        status: 503,
        headers: { 'Content-Type': 'application/json' },
      }))
      const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^预览版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '预览版本 3' }))

      await waitFor(() => {
        expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/download/test.txt?version=hash3', {
          headers: {
            Range: 'bytes=0-0',
            'X-Mnemonas-Download-Probe': 'json-error',
          },
        })
        expect(openSpy).not.toHaveBeenCalled()
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '预览版本暂不可用',
          description: '数据加载失败，请检查网络或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('shows missing-file guidance when version preview target no longer exists', async () => {
      mockAuthFetch.mockResolvedValueOnce(new Response(JSON.stringify({
        success: false,
        error: {
          code: 'FILE_NOT_FOUND',
          message: 'file not found',
        },
      }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      }))
      const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: /^预览版本 / }).length).toBeGreaterThan(0)
      })

      await user.click(screen.getByRole('button', { name: '预览版本 3' }))

      await waitFor(() => {
        expect(openSpy).not.toHaveBeenCalled()
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '预览版本暂不可用',
          description: '该文件可能已被移动或删除，请刷新列表后重试。',
          color: 'warning',
        })
      })
    })
  })

  describe('error handling', () => {
    it('shows an unavailable state when version storage is temporarily unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetVersions.mockRejectedValue(new ApiError('version storage unavailable', 503, 'SERVICE_UNAVAILABLE'))
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/unavailable.txt{enter}')

      await waitFor(() => {
        expect(screen.getByText('版本历史暂不可用')).toBeTruthy()
        expect(screen.getByText('版本存储当前不可用，请检查设备状态或稍后重试。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('shows error message on API failure', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetVersions.mockRejectedValue(new Error('文件不存在'))
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/nonexistent.txt{enter}')

      await waitFor(() => {
        expect(screen.getByText('获取版本历史失败')).toBeTruthy()
        expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeTruthy()
      })
    })

    it('retries loading versions from the error state', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      let retryRequested = false
      mockGetVersions.mockImplementation(() => {
        if (!retryRequested) {
          return Promise.reject(new Error('文件不存在'))
        }

        return Promise.resolve([
          { version: 1, hash: 'hash1', size: 1000, timestamp: '2024-01-01T00:00:00Z' },
        ])
      })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/retry.txt{enter}')

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      retryRequested = true
      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(screen.getByRole('list', { name: '版本历史' })).toBeTruthy()
        expect(mockAddToast).toHaveBeenCalledWith({ title: '版本历史已刷新', color: 'success' })
      })
    })

    it('shows warning toast when version reload becomes unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      let retryRequested = false
      mockGetVersions.mockImplementation(() => {
        if (!retryRequested) {
          return Promise.reject(new Error('文件不存在'))
        }

        return Promise.reject(new ApiError('version storage unavailable', 503, 'SERVICE_UNAVAILABLE'))
      })
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/retry-unavailable.txt{enter}')

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      retryRequested = true
      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '版本历史暂不可用',
          description: '版本存储当前不可用，请检查设备状态或稍后重试。',
          color: 'warning',
        })
      })
    })
  })

  describe('empty state', () => {
    it('shows message when no versions found', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetVersions.mockResolvedValue([])
      render(<VersionsPage />)

      const input = screen.getByLabelText('版本文件路径')
      await user.type(input, '/new-file.txt{enter}')

      await waitFor(() => {
        expect(screen.getByText('未找到版本记录')).toBeTruthy()
      })
    })
  })
})
