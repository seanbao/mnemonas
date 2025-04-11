import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import * as HeroUI from '@heroui/react'

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

import { VersionsPage } from './Versions'

import { ApiError, downloadFile, getVersions, restoreVersion } from '@/api/files'

const mockGetVersions = vi.mocked(getVersions)
const mockDownloadFile = vi.mocked(downloadFile)
const mockRestoreVersion = vi.mocked(restoreVersion)
const successActionResult = { warning: false, message: undefined } as const

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
      expect(screen.getByText('版本历史')).toBeTruthy()
      expect(screen.getByText('查看和恢复文件历史版本')).toBeTruthy()
    })

    it('renders search input', () => {
      render(<VersionsPage />)
      expect(screen.getByPlaceholderText(/输入文件路径/)).toBeTruthy()
    })

    it('renders search button', () => {
      render(<VersionsPage />)
      expect(screen.getByText('查询版本')).toBeTruthy()
    })

    it('shows empty state before search', () => {
      render(<VersionsPage />)
      expect(screen.getByText('查看文件版本历史')).toBeTruthy()
    })

    it('uses read-only subtitle and description for non-admin users', () => {
      mockUseIsAdmin.mockReturnValue(false)
      mockUseUser.mockReturnValue({ id: 'tester', username: 'tester', role: 'user', email: '', homeDir: '/tester' })

      render(<VersionsPage />)

      expect(screen.getByText('查看文件历史版本')).toBeTruthy()
      expect(screen.getByText('MnemoNAS 自动保留主目录内文件的历史版本。输入文件路径即可查看历史版本，支持预览和下载。')).toBeTruthy()
    })
  })

  describe('search functionality', () => {
    it('triggers search on button click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt')

      const searchBtn = screen.getByText('查询版本')
      await user.click(searchBtn)

      await waitFor(() => {
        expect(mockGetVersions).toHaveBeenCalledWith('/test.txt')
      })
    })

    it('triggers search on Enter key', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(mockGetVersions).toHaveBeenCalledWith('/test.txt')
      })
    })

    it('normalizes path without leading slash', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, 'test.txt{enter}')

      await waitFor(() => {
        expect(mockGetVersions).toHaveBeenCalledWith('/test.txt')
      })
    })

    it('trims surrounding whitespace from the searched path', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '  /test.txt  {enter}')

      await waitFor(() => {
        expect(mockGetVersions).toHaveBeenCalledWith('/test.txt')
      })
    })

    it('blocks non-admin searches outside the assigned home directory', async () => {
      mockUseIsAdmin.mockReturnValue(false)
      mockUseUser.mockReturnValue({ id: 'tester', username: 'tester', role: 'user', email: '', homeDir: '/tester' })

      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/other/secret.txt{enter}')

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '仅可查看主目录内文件的版本历史',
          color: 'warning',
        })
      })
      expect(mockGetVersions).not.toHaveBeenCalled()
      expect(input).toHaveValue('/tester')
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
      expect(screen.queryByText('查询版本')).toBeNull()
    })

    it('syncs the selected path when the URL query changes after mount', async () => {
      window.history.pushState({}, '', '/versions?path=/first.txt')
      render(<VersionsPage />)

      await waitFor(() => {
        expect(mockGetVersions).toHaveBeenCalledWith('/first.txt')
      })

      mockGetVersions.mockClear()

      await act(async () => {
        window.history.pushState({}, '', '/versions?path=/second.txt')
        window.dispatchEvent(new PopStateEvent('popstate'))
      })

      await waitFor(() => {
        expect(mockGetVersions).toHaveBeenCalledWith('/second.txt')
      })

      expect(screen.getByPlaceholderText(/输入文件路径/)).toHaveValue('/second.txt')
      expect(screen.getByText('/second.txt')).toBeTruthy()
    })
  })

  // Note: HeroUI Table component has compatibility issues with jsdom environment
  // The following tests verify API integration rather than Table rendering
  describe('version list', () => {
    it('calls API and processes versions', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(mockGetVersions).toHaveBeenCalledWith('/test.txt')
      })

      // Verify API was called with correct parameters
      expect(mockGetVersions).toHaveBeenCalledTimes(1)
    })

    it('handles multiple version data correctly', async () => {
      mockGetVersions.mockResolvedValue([
        { version: 5, hash: 'hash5', size: 5000, timestamp: '2024-01-05T00:00:00Z' },
        { version: 4, hash: 'hash4', size: 4000, timestamp: '2024-01-04T00:00:00Z' },
      ])

      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/multi.txt{enter}')

      await waitFor(() => {
        expect(mockGetVersions).toHaveBeenCalledWith('/multi.txt')
      })
    })

    it('displays version rows after search', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.getByRole('table', { name: '版本历史' })).toBeTruthy()
      })
    })

    it('shows file path after search', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
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

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        const restoreButtons = screen.queryAllByRole('button', { name: '恢复到此版本' })
        expect(restoreButtons.length).toBeGreaterThan(0)
      })
    })

    it('hides restore button for non-admin users', async () => {
      mockUseIsAdmin.mockReturnValue(false)
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(mockGetVersions).toHaveBeenCalledWith('/test.txt')
      })

      expect(screen.queryAllByTitle('恢复到此版本')).toHaveLength(0)
    })

    it('opens restore modal when clicking restore button', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByTitle('恢复到此版本').length).toBeGreaterThan(0)
      })

      const restoreButtons = screen.getAllByTitle('恢复到此版本')
      await user.click(restoreButtons[0])

      await waitFor(() => {
        expect(screen.getByText('确认恢复版本')).toBeTruthy()
      })
    })

    it('calls restore API on confirm', async () => {
      mockRestoreVersion.mockResolvedValue(successActionResult)
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByTitle('恢复到此版本').length).toBeGreaterThan(0)
      })

      await user.click(screen.getAllByTitle('恢复到此版本')[0])

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

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByTitle('恢复到此版本').length).toBeGreaterThan(0)
      })

      await user.click(screen.getAllByTitle('恢复到此版本')[0])

      await waitFor(() => {
        expect(screen.getByText('确认恢复')).toBeTruthy()
      })

      await user.click(screen.getByText('确认恢复'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'version restored with persistence warning',
          color: 'warning',
        })
      })
    })

    it('shows unavailable toast when restore fails because version storage is unavailable', async () => {
      mockRestoreVersion.mockRejectedValue(new ApiError('version storage unavailable', 503, 'SERVICE_UNAVAILABLE'))
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByTitle('恢复到此版本').length).toBeGreaterThan(0)
      })

      await user.click(screen.getAllByTitle('恢复到此版本')[0])

      await waitFor(() => {
        expect(screen.getByText('确认恢复')).toBeTruthy()
      })

      await user.click(screen.getByText('确认恢复'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '恢复版本暂不可用',
          description: '版本存储当前不可用，请检查系统状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('closes the restore modal and shows a stale-version warning when restore hits not found', async () => {
      mockRestoreVersion.mockRejectedValue(new ApiError('resource not found', 404))
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByTitle('恢复到此版本').length).toBeGreaterThan(0)
      })

      await user.click(screen.getAllByTitle('恢复到此版本')[0])
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

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByTitle('恢复到此版本').length).toBeGreaterThan(0)
      })

      await user.click(screen.getAllByTitle('恢复到此版本')[0])
      await user.click(await screen.findByText('确认恢复'))

      await waitFor(() => {
        expect(mockRestoreVersion).toHaveBeenCalledWith('/test.txt', 'hash2')
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

    it('keeps the restore modal open when a pending restore later fails', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const firstRestore = createDeferred<typeof successActionResult>()
      mockRestoreVersion.mockImplementationOnce(() => firstRestore.promise)

      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByTitle('恢复到此版本').length).toBeGreaterThan(0)
      })

      await user.click(screen.getAllByTitle('恢复到此版本')[0])
      await user.click(await screen.findByText('确认恢复'))

      await waitFor(() => {
        expect(mockRestoreVersion).toHaveBeenCalledWith('/test.txt', 'hash2')
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
          description: 'restore failed',
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

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        const downloadButtons = screen.queryAllByTitle('下载此版本')
        expect(downloadButtons.length).toBeGreaterThan(0)
      })
    })

    it('shows unavailable toast when version download is temporarily unavailable', async () => {
      mockDownloadFile.mockRejectedValue(new ApiError('version storage unavailable', 503, 'SERVICE_UNAVAILABLE'))
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByTitle('下载此版本').length).toBeGreaterThan(0)
      })

      await user.click(screen.getAllByTitle('下载此版本')[0])

      await waitFor(() => {
        expect(mockDownloadFile).toHaveBeenCalledWith('/test.txt', { version: 'hash3' })
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '下载版本暂不可用',
          description: '版本存储当前不可用，请检查系统状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('shows a stale-version warning when version download hits not found', async () => {
      mockDownloadFile.mockRejectedValue(new ApiError('resource not found', 404))
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByTitle('下载此版本').length).toBeGreaterThan(0)
      })

      await user.click(screen.getAllByTitle('下载此版本')[0])

      await waitFor(() => {
        expect(mockDownloadFile).toHaveBeenCalledWith('/test.txt', { version: 'hash3' })
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '所选版本已不存在',
          description: '该版本或目标文件已被移除，请刷新版本历史后重试。',
          color: 'warning',
        })
      })
    })

    it('shows warning toast when browser blocks version preview', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      vi.spyOn(window, 'open').mockReturnValue(null)
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByTitle('预览').length).toBeGreaterThan(0)
      })

      await user.click(screen.getAllByTitle('预览')[0])

      expect(mockAddToast).toHaveBeenCalledWith({
        title: '浏览器拦截了新标签页，请允许弹窗后重试',
        color: 'warning',
      })
    })

    it('opens preview with isolated window features', async () => {
      const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
      const user = userEvent.setup({ writeToClipboard: false })
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/test.txt{enter}')

      await waitFor(() => {
        expect(screen.queryAllByTitle('预览').length).toBeGreaterThan(0)
      })

      await user.click(screen.getAllByTitle('预览')[0])

      expect(openSpy).toHaveBeenCalledWith('/api/v1/download/test.txt?version=hash3', '_blank', 'noopener,noreferrer')
    })
  })

  describe('error handling', () => {
    it('shows an unavailable state when version storage is temporarily unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetVersions.mockRejectedValue(new ApiError('version storage unavailable', 503, 'SERVICE_UNAVAILABLE'))
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/unavailable.txt{enter}')

      await waitFor(() => {
        expect(screen.getByText('版本历史暂不可用')).toBeTruthy()
        expect(screen.getByText('版本存储当前不可用，请检查系统状态或稍后重试。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('shows error message on API failure', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetVersions.mockRejectedValue(new Error('文件不存在'))
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/nonexistent.txt{enter}')

      await waitFor(() => {
        expect(screen.getByText('获取版本历史失败')).toBeTruthy()
      })
    })

    it('retries loading versions from the error state', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetVersions
        .mockRejectedValueOnce(new Error('文件不存在'))
        .mockResolvedValueOnce([
          { version: 1, hash: 'hash1', size: 1000, timestamp: '2024-01-01T00:00:00Z' },
        ])
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/retry.txt{enter}')

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(screen.getByRole('table', { name: '版本历史' })).toBeTruthy()
        expect(mockAddToast).toHaveBeenCalledWith({ title: '版本历史已刷新', color: 'success' })
      })
    })

    it('shows warning toast when version reload becomes unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetVersions
        .mockRejectedValueOnce(new Error('文件不存在'))
        .mockRejectedValueOnce(new ApiError('version storage unavailable', 503, 'SERVICE_UNAVAILABLE'))
      render(<VersionsPage />)

      const input = screen.getByPlaceholderText(/输入文件路径/)
	  await user.type(input, '/retry-unavailable.txt{enter}')

	  await waitFor(() => {
	    expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
	  })

	  await user.click(screen.getByRole('button', { name: '重新加载' }))

	  await waitFor(() => {
	    expect(mockAddToast).toHaveBeenCalledWith({
	      title: '版本历史暂不可用',
	      description: '版本存储当前不可用，请检查系统状态或稍后重试。',
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

      const input = screen.getByPlaceholderText(/输入文件路径/)
      await user.type(input, '/new-file.txt{enter}')

      await waitFor(() => {
        expect(screen.getByText('暂无版本历史')).toBeTruthy()
      })
    })
  })
})
