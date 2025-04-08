import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { FilesPage } from './Files'
import * as HeroUI from '@heroui/react'

const mockAddToast = vi.fn()
const useCanWriteMock = vi.fn(() => true)
const mockUser = { id: 'u1', username: 'admin', role: 'admin' as const, email: 'admin@local', homeDir: '/' }

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
  listFiles: vi.fn(),
  deleteFile: vi.fn(),
  createDirectory: vi.fn(),
  uploadFile: vi.fn(),
  moveFile: vi.fn(),
  copyFile: vi.fn(),
  downloadFile: vi.fn(),
}))

vi.mock('@/api/favorites', () => ({
  checkFavorites: vi.fn(),
  toggleFavorite: vi.fn(),
}))

vi.mock('@/api/share', async () => {
  const actual = await vi.importActual<typeof import('@/api/share')>('@/api/share')
  return {
    ...actual,
    listShares: vi.fn().mockResolvedValue([]),
  }
})

// Mock navigation
const mockNavigate = vi.fn()
let mockLocationPathname = '/files'
let mockLocationState: unknown = null
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useLocation: () => ({ pathname: mockLocationPathname, search: '', state: mockLocationState }),
  }
})

const mockFilesStoreState = {
  currentPath: '/',
  selectedFiles: new Set<string>(),
  viewMode: 'list' as const,
  sortBy: 'name' as const,
  sortOrder: 'asc' as const,
  setCurrentPath: vi.fn(),
  selectFile: vi.fn(),
  toggleFileSelection: vi.fn(),
  setSelection: vi.fn(),
  selectAll: vi.fn(),
  clearSelection: vi.fn(),
  setViewMode: vi.fn(),
  setSortBy: vi.fn(),
  toggleSortOrder: vi.fn(),
}

const mockClipboardState = {
  paths: [] as string[],
  operation: null as 'copy' | 'cut' | null,
  sourcePath: null as string | null,
  copy: vi.fn(),
  cut: vi.fn(),
  clear: vi.fn(),
  hasPaths: vi.fn(() => false),
}

// Mock stores
vi.mock('@/stores/files', () => ({
  useFilesStore: () => mockFilesStoreState,
}))

vi.mock('@/stores/clipboard', () => ({
  useClipboardStore: () => mockClipboardState,
}))

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useCanWrite: () => useCanWriteMock(),
    useUser: () => mockUser,
  }
})

vi.mock('@/components/share', () => ({
  ShareDialog: () => null,
}))

import { ApiError, listFiles, createDirectory, deleteFile, moveFile, copyFile } from '@/api/files'
import { listShares } from '@/api/share'
import { downloadFile } from '@/api/files'
import { checkFavorites, toggleFavorite } from '@/api/favorites'

const mockListFiles = vi.mocked(listFiles)
const mockCreateDirectory = vi.mocked(createDirectory)
const mockDeleteFile = vi.mocked(deleteFile)
const mockMoveFile = vi.mocked(moveFile)
const mockCopyFile = vi.mocked(copyFile)
const mockDownloadFile = vi.mocked(downloadFile)
const mockCheckFavorites = vi.mocked(checkFavorites)
const mockToggleFavorite = vi.mocked(toggleFavorite)
const mockListShares = vi.mocked(listShares)
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

describe('FilesPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useCanWriteMock.mockReturnValue(true)
    mockUser.id = 'u1'
    mockUser.username = 'admin'
    mockUser.role = 'admin'
    mockUser.email = 'admin@local'
    mockUser.homeDir = '/'
    mockListShares.mockResolvedValue([])
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
    mockFilesStoreState.selectedFiles = new Set<string>()
    mockFilesStoreState.currentPath = '/'
    mockFilesStoreState.viewMode = 'list'
    mockFilesStoreState.sortBy = 'name'
    mockFilesStoreState.sortOrder = 'asc'
    mockClipboardState.paths = []
    mockClipboardState.operation = null
    mockClipboardState.sourcePath = null
    mockLocationPathname = '/files'
    mockLocationState = null
    mockClipboardState.copy.mockClear()
    mockClipboardState.cut.mockClear()
    mockClipboardState.clear.mockClear()
    mockClipboardState.hasPaths.mockReturnValue(false)
    mockDownloadFile.mockResolvedValue(undefined)
    mockCheckFavorites.mockResolvedValue({
      '/documents': false,
      '/photo.jpg': false,
      '/video.mp4': false,
    })
    mockToggleFavorite.mockResolvedValue(true)
    // Default mock response
    mockListFiles.mockResolvedValue({
      files: [
        { name: 'documents', path: '/documents', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
        { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024000, modTime: '2024-01-02T00:00:00Z' },
        { name: 'video.mp4', path: '/video.mp4', isDir: false, size: 10240000, modTime: '2024-01-03T00:00:00Z' },
      ],
      path: '/',
    })
  })

  describe('rendering', () => {
    it('shows loading state initially', () => {
      mockListFiles.mockImplementation(() => new Promise(() => {})) // Never resolves
      render(<FilesPage />)
      expect(screen.getByText('加载记忆中...')).toBeTruthy()
    })

    it('calls listFiles API on mount', async () => {
      render(<FilesPage />)
      
      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })
    })

    it('displays breadcrumb navigation', async () => {
      render(<FilesPage />)
      
      await waitFor(() => {
        expect(screen.getByText('根目录')).toBeTruthy()
      })
    })

    it('shows empty state when no files', async () => {
      mockListFiles.mockResolvedValue({ files: [], path: '/' })
      render(<FilesPage />)
      
      await waitFor(() => {
        expect(screen.getByText('这里空空如也')).toBeTruthy()
      })
    })

    it('falls back to root when the route path has invalid URI encoding', async () => {
      mockLocationPathname = '/files/%E0%A4%A'
      render(<FilesPage />)

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '路径格式无效，已返回根目录',
          color: 'warning',
        }))
      })

      expect(mockNavigate).toHaveBeenCalledWith('/files', { replace: true })
      expect(mockFilesStoreState.setCurrentPath).not.toHaveBeenCalledWith('/%E0%A4%A')
    })

    it('returns to the last valid folder when an invalid route path is encountered', async () => {
      mockFilesStoreState.currentPath = '/documents'
      mockLocationPathname = '/files/%E0%A4%A'

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '路径格式无效，已返回上一个有效位置',
          color: 'warning',
        }))
      })

      expect(mockNavigate).toHaveBeenCalledWith('/files/documents', { replace: true })
      expect(mockFilesStoreState.setCurrentPath).not.toHaveBeenCalledWith('/')
    })

    it('normalizes valid route paths before syncing page state', async () => {
      mockLocationPathname = '/files//documents//'

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockFilesStoreState.setCurrentPath).toHaveBeenCalledWith('/documents')
      })
    })

    it('redirects non-admin root browsing to the assigned home directory', async () => {
      mockUser.id = 'u2'
      mockUser.username = 'tester'
      mockUser.role = 'user'
      mockUser.homeDir = '/tester'
      mockFilesStoreState.currentPath = '/'
      mockLocationPathname = '/files'

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockFilesStoreState.setCurrentPath).toHaveBeenCalledWith('/tester')
      })
    })

    it('redirects non-admin out-of-home file routes back to the assigned home directory', async () => {
      mockUser.id = 'u2'
      mockUser.username = 'tester'
      mockUser.role = 'user'
      mockUser.homeDir = '/tester'
      mockFilesStoreState.currentPath = '/'
      mockLocationPathname = '/files/shared'

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '仅可访问主目录内的文件',
          color: 'warning',
        }))
        expect(mockFilesStoreState.setCurrentPath).toHaveBeenCalledWith('/tester')
      })

      expect(mockFilesStoreState.setCurrentPath).not.toHaveBeenCalledWith('/shared')
      expect(mockListFiles).not.toHaveBeenCalled()
    })

    it('shows an invalid-home error instead of browsing root for non-admin users without a home directory', async () => {
      mockUser.id = 'u2'
      mockUser.username = 'tester'
      mockUser.role = 'user'
      mockUser.homeDir = ''
      mockFilesStoreState.currentPath = '/'
      mockLocationPathname = '/files'

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('主目录配置无效')).toBeTruthy()
        expect(screen.getByText('当前账户未配置有效的主目录，无法浏览文件。请联系管理员修复账户 home_dir。')).toBeTruthy()
      })

      expect(mockListFiles).not.toHaveBeenCalled()
    })
  })

  describe('toolbar', () => {
    it('renders upload button', async () => {
      render(<FilesPage />)
      
      await waitFor(() => {
        expect(screen.getByText('上传文件')).toBeTruthy()
      })
    })

    it('renders new folder button', async () => {
      render(<FilesPage />)
      
      await waitFor(() => {
        expect(screen.getByText('新建空间')).toBeTruthy()
      })
    })

    it('renders view mode toggle buttons', async () => {
      render(<FilesPage />)
      
      await waitFor(() => {
        // Find list and grid toggle buttons
        const buttons = document.querySelectorAll('button')
        expect(buttons.length).toBeGreaterThan(2)
      })
    })

    it('hides guest write actions from the selection toolbar but keeps batch download', async () => {
      useCanWriteMock.mockReturnValue(false)
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('批量下载（仅文件）')).toBeTruthy()
      })

      expect(screen.queryByText('批量移动')).toBeNull()
      expect(screen.queryByText('批量复制')).toBeNull()
      expect(screen.queryByText('批量删除')).toBeNull()
      expect(screen.getByText('批量下载（仅文件）')).toBeTruthy()
      expect(screen.getByText('访客账户为只读，仅可查看和下载')).toBeTruthy()
    })
  })

  describe('folder creation', () => {
    it('opens new folder modal on button click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<FilesPage />)
      
      await waitFor(() => {
        expect(screen.getByText('新建空间')).toBeTruthy()
      })

      const newFolderBtn = screen.getByText('新建空间')
      await user.click(newFolderBtn)

      await waitFor(() => {
        expect(screen.getByText('新建文件夹')).toBeTruthy()
        expect(screen.getByPlaceholderText('请输入文件夹名称')).toBeTruthy()
      })
    })

    it('creates folder on confirm', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockCreateDirectory.mockResolvedValue(successActionResult)
      
      render(<FilesPage />)
      
      await waitFor(() => {
        expect(screen.getByText('新建空间')).toBeTruthy()
      })

      await user.click(screen.getByText('新建空间'))

      await waitFor(() => {
        expect(screen.getByPlaceholderText('请输入文件夹名称')).toBeTruthy()
      })

      const input = screen.getByPlaceholderText('请输入文件夹名称')
      await user.type(input, 'new-folder')

      const createBtn = screen.getByRole('button', { name: '创建' })
      await user.click(createBtn)

      await waitFor(() => {
        // createDirectory is called with path as first arg (react-query adds mutation context)
        expect(mockCreateDirectory.mock.calls[0][0]).toBe('/new-folder')
      })
    })

    it('trims folder name before creating a folder', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockCreateDirectory.mockResolvedValue(successActionResult)

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('新建空间')).toBeTruthy()
      })

      await user.click(screen.getByText('新建空间'))

      await waitFor(() => {
        expect(screen.getByPlaceholderText('请输入文件夹名称')).toBeTruthy()
      })

      const input = screen.getByPlaceholderText('请输入文件夹名称')
      await user.type(input, '  spaced-folder  ')
      await user.click(screen.getByRole('button', { name: '创建' }))

      await waitFor(() => {
        expect(mockCreateDirectory.mock.calls[0][0]).toBe('/spaced-folder')
      })
    })

    it('keeps the create folder modal open while a pending request is in flight', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const firstCreate = createDeferred<typeof successActionResult>()
      mockCreateDirectory.mockImplementation((path) => {
        if (path === '/first-folder') {
          return firstCreate.promise
        }
        return Promise.resolve(successActionResult)
      })

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('新建空间')).toBeTruthy()
      })

      await user.click(screen.getByText('新建空间'))

      const input = await screen.findByPlaceholderText('请输入文件夹名称')
      await user.type(input, 'first-folder')
      await user.click(screen.getByRole('button', { name: '创建' }))

      await waitFor(() => {
        expect(mockCreateDirectory.mock.calls[0][0]).toBe('/first-folder')
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      expect(screen.getByText('新建文件夹')).toBeTruthy()
      expect(screen.getByDisplayValue('first-folder')).toBeTruthy()

      firstCreate.resolve(successActionResult)

      await waitFor(() => {
        expect(screen.queryByText('新建文件夹')).toBeFalsy()
      })
    })

    it('keeps the create folder modal open when a pending request later fails', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const firstCreate = createDeferred<typeof successActionResult>()
      mockCreateDirectory.mockImplementationOnce(() => firstCreate.promise)

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('新建空间')).toBeTruthy()
      })

      await user.click(screen.getByText('新建空间'))

      const input = await screen.findByPlaceholderText('请输入文件夹名称')
      await user.type(input, 'failed-folder')
      await user.click(screen.getByRole('button', { name: '创建' }))

      await waitFor(() => {
        expect(mockCreateDirectory.mock.calls[0][0]).toBe('/failed-folder')
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      expect(screen.getByText('新建文件夹')).toBeTruthy()
      expect(screen.getByDisplayValue('failed-folder')).toBeTruthy()

      await act(async () => {
        firstCreate.reject(new Error('create failed'))
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '创建失败',
          description: 'create failed',
          color: 'danger',
        })
      })

      expect(screen.getByText('新建文件夹')).toBeTruthy()
      expect(screen.getByDisplayValue('failed-folder')).toBeTruthy()
    })

    it('closes modal on cancel', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<FilesPage />)
      
      await waitFor(() => {
        expect(screen.getByText('新建空间')).toBeTruthy()
      })

      await user.click(screen.getByText('新建空间'))

      await waitFor(() => {
        expect(screen.getByText('新建文件夹')).toBeTruthy()
      })

      const cancelBtn = screen.getByRole('button', { name: '取消' })
      await user.click(cancelBtn)

      await waitFor(() => {
        expect(screen.queryByText('新建文件夹')).toBeFalsy()
      })
    })

    it('shows warning toast when folder creation succeeds with a persistence warning', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockCreateDirectory.mockResolvedValueOnce(warningActionResult('directory created with persistence warning'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('新建空间')).toBeTruthy()
      })

      await user.click(screen.getByText('新建空间'))
      await user.type(await screen.findByPlaceholderText('请输入文件夹名称'), 'warn-folder')
      await user.click(screen.getByRole('button', { name: '创建' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'directory created with persistence warning',
          color: 'warning',
        })
      })
    })
  })

  describe('file selection', () => {
    it('renders checkboxes for each file', async () => {
      render(<FilesPage />)
      
      await waitFor(() => {
        const checkboxes = document.querySelectorAll('[class*="border-2"]')
        expect(checkboxes.length).toBeGreaterThan(0)
      })
    })

    it('shows selection summary when items are selected', async () => {
      mockFilesStoreState.selectedFiles = new Set(['/documents', '/photo.jpg'])
      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('已选')).toBeTruthy()
        expect(screen.getByText('选择工具')).toBeTruthy()
      })
    })

    it('clears selection when path changes', async () => {
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      const { rerender } = render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })

      mockFilesStoreState.clearSelection.mockClear()
      mockFilesStoreState.currentPath = '/documents'
      rerender(<FilesPage />)

      await waitFor(() => {
        expect(mockFilesStoreState.clearSelection).toHaveBeenCalled()
      })
    })

    it('prunes selection when files disappear', async () => {
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      mockListFiles
        .mockResolvedValueOnce({
          files: [
            { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024, modTime: '2024-01-02T00:00:00Z' },
          ],
          path: '/',
        })
        .mockResolvedValueOnce({
          files: [],
          path: '/',
        })

      const firstRender = render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })

      firstRender.unmount()
      mockFilesStoreState.setSelection.mockClear()
      render(<FilesPage />)

      await waitFor(() => {
        expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith([])
      })
    })

    it('selects a highlighted file from search navigation state', async () => {
      mockLocationState = { highlightPath: '/photo.jpg' }

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/photo.jpg'])
      })

      expect(mockNavigate).toHaveBeenCalledWith('/files', { replace: true, state: null })
    })

    it('keeps remaining selections when some files disappear', async () => {
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg', '/video.mp4'])
      mockListFiles
        .mockResolvedValueOnce({
          files: [
            { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024, modTime: '2024-01-02T00:00:00Z' },
            { name: 'video.mp4', path: '/video.mp4', isDir: false, size: 2048, modTime: '2024-01-03T00:00:00Z' },
          ],
          path: '/',
        })
        .mockResolvedValueOnce({
          files: [
            { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024, modTime: '2024-01-02T00:00:00Z' },
          ],
          path: '/',
        })

      const firstRender = render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })

      firstRender.unmount()
      mockFilesStoreState.setSelection.mockClear()
      render(<FilesPage />)

      await waitFor(() => {
        expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/photo.jpg'])
      })
    })
  })

  describe('file operations', () => {
    it('deleteFile API is available', async () => {
      mockDeleteFile.mockResolvedValue(successActionResult)
      await mockDeleteFile('/photo.jpg')
      expect(mockDeleteFile).toHaveBeenCalledWith('/photo.jpg')
    })

    it('moveFile API is available', async () => {
      mockMoveFile.mockResolvedValue(successActionResult)
      await mockMoveFile('/photo.jpg', '/documents/photo.jpg')
      expect(mockMoveFile).toHaveBeenCalledWith('/photo.jpg', '/documents/photo.jpg')
    })

    it('handles API errors gracefully', async () => {
      mockListFiles.mockRejectedValue(new Error('Network error'))
      render(<FilesPage />)
      
      // Should not crash on error
      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })
    })

    it('shows danger toast when batch download fully fails', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg', '/video.mp4'])
      mockDownloadFile.mockRejectedValue(new Error('download failed'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('批量下载（仅文件）')).toBeTruthy()
      })

      await user.click(screen.getByText('批量下载（仅文件）'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '批量下载失败',
          description: '共 2 个文件下载失败',
          color: 'danger',
        })
      })
    })

    it('shows unavailable toast when batch download fully fails due to unavailable filesystem', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg', '/video.mp4'])
      mockDownloadFile.mockRejectedValue(new ApiError('filesystem not initialized', 503, 'SERVICE_UNAVAILABLE'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('批量下载（仅文件）')).toBeTruthy()
      })

      await user.click(screen.getByText('批量下载（仅文件）'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '批量下载暂不可用',
          description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('shows unavailable toast when single-file download fails due to unavailable filesystem', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockDownloadFile.mockRejectedValue(new ApiError('filesystem not initialized', 503, 'SERVICE_UNAVAILABLE'))
      mockFilesStoreState.viewMode = 'grid'

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('photo.jpg')).toBeTruthy()
      })

      const menuButton = screen.getByLabelText('photo.jpg 操作菜单')

      await user.click(menuButton)

      await waitFor(() => {
        expect(screen.getAllByText('下载').length).toBeGreaterThan(0)
      })

      await user.click(screen.getAllByText('下载')[0])

      await waitFor(() => {
        expect(mockDownloadFile).toHaveBeenCalledWith('/photo.jpg', { filename: 'photo.jpg' })
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '下载暂不可用',
          description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('keeps failed items selected after partial batch delete failure', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg', '/video.mp4'])
      mockDeleteFile
        .mockResolvedValueOnce(successActionResult)
        .mockRejectedValueOnce(new Error('delete failed'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('批量删除')).toBeTruthy()
      })

      mockFilesStoreState.clearSelection.mockClear()

      await user.click(screen.getByText('批量删除'))

      await waitFor(() => {
        expect(screen.getByText('删除全部')).toBeTruthy()
      })

      await user.click(screen.getByText('删除全部'))

      await waitFor(() => {
        expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/video.mp4'])
      })

      expect(mockFilesStoreState.clearSelection).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '批量删除部分完成',
        description: '成功 1 个，失败 1 个',
        color: 'warning',
      })
    })

    it('shows danger toast and preserves selection when batch delete fully fails', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg', '/video.mp4'])
      mockDeleteFile.mockRejectedValue(new Error('delete failed'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('批量删除')).toBeTruthy()
      })

      mockFilesStoreState.clearSelection.mockClear()

      await user.click(screen.getByText('批量删除'))

      await waitFor(() => {
        expect(screen.getByText('删除全部')).toBeTruthy()
      })

      await user.click(screen.getByText('删除全部'))

      await waitFor(() => {
        expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/photo.jpg', '/video.mp4'])
      })

      expect(mockFilesStoreState.clearSelection).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '批量删除失败',
        description: '共 2 个项目删除失败',
        color: 'danger',
      })
    })

    it('shows unavailable toast and preserves selection when batch delete fully fails due to unavailable filesystem', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg', '/video.mp4'])
      mockDeleteFile.mockRejectedValue(new ApiError('filesystem not initialized', 503, 'SERVICE_UNAVAILABLE'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('批量删除')).toBeTruthy()
      })

      mockFilesStoreState.clearSelection.mockClear()

      await user.click(screen.getByText('批量删除'))

      await waitFor(() => {
        expect(screen.getByText('删除全部')).toBeTruthy()
      })

      await user.click(screen.getByText('删除全部'))

      await waitFor(() => {
        expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/photo.jpg', '/video.mp4'])
      })

      expect(mockFilesStoreState.clearSelection).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '批量删除暂不可用',
        description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
        color: 'warning',
      })
    })

    it('keeps the batch delete modal open while a pending batch delete is in flight', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const pendingDelete = createDeferred<typeof successActionResult>()
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      mockDeleteFile.mockImplementationOnce(() => pendingDelete.promise)

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('批量删除')).toBeTruthy()
      })

      await user.click(screen.getByText('批量删除'))

      await waitFor(() => {
        expect(screen.getByText('删除全部')).toBeTruthy()
      })

      await user.click(screen.getByText('删除全部'))

      await waitFor(() => {
        expect(mockDeleteFile).toHaveBeenCalledWith('/photo.jpg')
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      expect(screen.getByRole('heading', { name: '批量删除' })).toBeTruthy()
      expect(screen.getByRole('button', { name: '删除全部' })).toBeTruthy()

      await act(async () => {
        pendingDelete.resolve(successActionResult)
        await pendingDelete.promise
      })

      await waitFor(() => {
        expect(screen.queryByRole('button', { name: '删除全部' })).toBeFalsy()
      })
    })

    it('keeps the batch delete modal open when a pending batch delete later fails', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const pendingDelete = createDeferred<void>()
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      mockDeleteFile.mockImplementationOnce(() => pendingDelete.promise)

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('批量删除')).toBeTruthy()
      })

      await user.click(screen.getByText('批量删除'))

      await waitFor(() => {
        expect(screen.getByText('删除全部')).toBeTruthy()
      })

      await user.click(screen.getByText('删除全部'))

      await waitFor(() => {
        expect(mockDeleteFile).toHaveBeenCalledWith('/photo.jpg')
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      expect(screen.getByRole('heading', { name: '批量删除' })).toBeTruthy()
      expect(screen.getByRole('button', { name: '删除全部' })).toBeTruthy()

      await act(async () => {
        pendingDelete.reject(new Error('delete failed'))
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '批量删除失败',
          description: '共 1 个项目删除失败',
          color: 'danger',
        })
      })

      expect(screen.getByRole('heading', { name: '批量删除' })).toBeTruthy()
      expect(screen.getByRole('button', { name: '删除全部' })).toBeTruthy()
    })

    it('shows warning toast when batch download partially fails', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg', '/video.mp4'])
      mockDownloadFile
        .mockResolvedValueOnce(undefined)
        .mockRejectedValueOnce(new Error('download failed'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('批量下载（仅文件）')).toBeTruthy()
      })

      await user.click(screen.getByText('批量下载（仅文件）'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '部分文件开始下载',
          description: '已开始 1 个，失败 1 个',
          color: 'warning',
        })
      })
    })

    it('shows success toast only after keyboard refresh succeeds', async () => {
      render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalledTimes(1)
      })

      mockAddToast.mockClear()
      mockListFiles.mockResolvedValueOnce({ files: [], path: '/' })

      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'F5', bubbles: true }))

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalledTimes(2)
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '刷新成功', color: 'success' })
      })
    })

    it('shows warning toast when keyboard refresh fails due to unavailable filesystem', async () => {
      render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalledTimes(1)
      })

      mockAddToast.mockClear()
      mockListFiles.mockRejectedValueOnce(new ApiError('filesystem not initialized', 503, 'SERVICE_UNAVAILABLE'))

      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'r', ctrlKey: true, bubbles: true }))

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalledTimes(2)
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '刷新暂不可用',
          description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('keeps failed cut items in clipboard after partial paste failure', async () => {
      mockClipboardState.paths = ['/source/photo.jpg', '/source/video.mp4']
      mockClipboardState.operation = 'cut'
      mockClipboardState.sourcePath = '/source'
      mockClipboardState.hasPaths.mockReturnValue(true)
      mockMoveFile
        .mockResolvedValueOnce(successActionResult)
        .mockRejectedValueOnce(new Error('move failed'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })

      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'v', ctrlKey: true, bubbles: true }))

      await waitFor(() => {
        expect(mockMoveFile).toHaveBeenCalledTimes(2)
      })

      expect(mockClipboardState.cut).toHaveBeenCalledWith(['/source/video.mp4'], '/source')
      expect(mockClipboardState.clear).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '批量移动部分完成',
        description: '成功 1 个，失败 1 个',
        color: 'warning',
      })
    })

    it('shows warning toast when batch delete succeeds with cleanup warnings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      mockDeleteFile.mockResolvedValueOnce(warningActionResult('file deleted with persistence warning'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('批量删除')).toBeTruthy()
      })

      await user.click(screen.getByText('批量删除'))
      await user.click(await screen.findByText('删除全部'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'file deleted with persistence warning',
          color: 'warning',
        })
      })
    })

    it('shows danger toast when copy paste fully fails', async () => {
      mockClipboardState.paths = ['/source/photo.jpg', '/source/video.mp4']
      mockClipboardState.operation = 'copy'
      mockClipboardState.sourcePath = '/source'
      mockClipboardState.hasPaths.mockReturnValue(true)
      mockCopyFile.mockRejectedValue(new Error('copy failed'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })

      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'v', ctrlKey: true, bubbles: true }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '批量复制失败',
          description: '共 2 个项目失败',
          color: 'danger',
        })
      })
    })

    it('shows warning toast when copy paste fully succeeds with warnings', async () => {
      mockClipboardState.paths = ['/source/photo.jpg', '/source/video.mp4']
      mockClipboardState.operation = 'copy'
      mockClipboardState.sourcePath = '/source'
      mockClipboardState.hasPaths.mockReturnValue(true)
      mockCopyFile
        .mockResolvedValueOnce(warningActionResult('resource copied with persistence warning'))
        .mockResolvedValueOnce(successActionResult)

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })

      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'v', ctrlKey: true, bubbles: true }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'resource copied with persistence warning',
          color: 'warning',
        })
      })
    })

    it('shows warning toast when cut paste fully succeeds with warnings', async () => {
      mockClipboardState.paths = ['/source/photo.jpg']
      mockClipboardState.operation = 'cut'
      mockClipboardState.sourcePath = '/source'
      mockClipboardState.hasPaths.mockReturnValue(true)
      mockMoveFile.mockResolvedValueOnce(warningActionResult('resource moved with persistence warning'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })

      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'v', ctrlKey: true, bubbles: true }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'resource moved with persistence warning',
          color: 'warning',
        })
      })
    })

    it('keeps the rename modal open while a pending rename is in flight', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const firstRename = createDeferred<typeof successActionResult>()
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      mockMoveFile.mockImplementation((from) => {
        if (from === '/photo.jpg') {
          return firstRename.promise
        }
        return Promise.resolve(successActionResult)
      })

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('photo.jpg')).toBeTruthy()
      })

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'F2', bubbles: true }))
      })

      const renameInput = await screen.findByPlaceholderText('请输入新名称')
      await user.clear(renameInput)
      await user.type(renameInput, 'photo-renamed.jpg')
      await user.click(screen.getByRole('button', { name: '确定' }))

      await waitFor(() => {
        expect(mockMoveFile).toHaveBeenCalledWith('/photo.jpg', '/photo-renamed.jpg')
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      expect(screen.getByText('重命名')).toBeTruthy()
      expect(screen.getByDisplayValue('photo-renamed.jpg')).toBeTruthy()

      await act(async () => {
        firstRename.resolve(successActionResult)
        await firstRename.promise
      })

      await waitFor(() => {
        expect(screen.queryByText('重命名')).toBeFalsy()
      })
    })

    it('keeps the rename modal open when a pending rename later fails', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const firstRename = createDeferred<typeof successActionResult>()
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      mockMoveFile.mockImplementationOnce(() => firstRename.promise)

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('photo.jpg')).toBeTruthy()
      })

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'F2', bubbles: true }))
      })

      const renameInput = await screen.findByPlaceholderText('请输入新名称')
      await user.clear(renameInput)
      await user.type(renameInput, 'photo-failed.jpg')
      await user.click(screen.getByRole('button', { name: '确定' }))

      await waitFor(() => {
        expect(mockMoveFile).toHaveBeenCalledWith('/photo.jpg', '/photo-failed.jpg')
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      expect(screen.getByText('重命名')).toBeTruthy()
      expect(screen.getByDisplayValue('photo-failed.jpg')).toBeTruthy()

      await act(async () => {
        firstRename.reject(new Error('rename failed'))
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '重命名失败',
          description: 'rename failed',
          color: 'danger',
        })
      })

      expect(screen.getByText('重命名')).toBeTruthy()
      expect(screen.getByDisplayValue('photo-failed.jpg')).toBeTruthy()
    })

    it('shows warning toast when rename succeeds with a persistence warning', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      mockMoveFile.mockResolvedValueOnce(warningActionResult('resource moved with persistence warning'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('photo.jpg')).toBeTruthy()
      })

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'F2', bubbles: true }))
      })

      const renameInput = await screen.findByPlaceholderText('请输入新名称')
      await user.clear(renameInput)
      await user.type(renameInput, 'photo-warning.jpg')
      await user.click(screen.getByRole('button', { name: '确定' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'resource moved with persistence warning',
          color: 'warning',
        })
      })
    })
  })

  describe('view modes', () => {
    it('supports list view mode', async () => {
      render(<FilesPage />)
      
      await waitFor(() => {
        // In list mode, there should be grid layout columns
        const gridLayout = document.querySelector('[class*="grid-cols"]')
        expect(gridLayout).toBeTruthy()
      })
    })

    it('has view mode toggle buttons', async () => {
      render(<FilesPage />)
      
      await waitFor(() => {
        // Grid and list toggle buttons should be present
        const buttons = document.querySelectorAll('button')
        expect(buttons.length).toBeGreaterThan(3) // Upload, new folder, list, grid
      })
    })
  })

  describe('breadcrumb navigation', () => {
    it('shows root directory label', async () => {
      render(<FilesPage />)
      
      await waitFor(() => {
        expect(screen.getByText('根目录')).toBeTruthy()
      })
    })

    it('displays home icon in breadcrumb', async () => {
      render(<FilesPage />)
      
      await waitFor(() => {
        const homeButton = screen.getByText('根目录').closest('button')
        expect(homeButton).toBeTruthy()
      })
    })
  })

  describe('file list header', () => {
    it('renders column headers in list view', async () => {
      render(<FilesPage />)
      
      await waitFor(() => {
        expect(screen.getByText('名称')).toBeTruthy()
        expect(screen.getByText('大小')).toBeTruthy()
      })
    })
  })

  describe('different file types', () => {
    beforeEach(() => {
      mockListFiles.mockResolvedValue({
        files: [
          { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024, modTime: '2024-01-01T00:00:00Z' },
          { name: 'video.mp4', path: '/video.mp4', isDir: false, size: 10240, modTime: '2024-01-02T00:00:00Z' },
          { name: 'document.pdf', path: '/document.pdf', isDir: false, size: 2048, modTime: '2024-01-03T00:00:00Z' },
          { name: 'music.mp3', path: '/music.mp3', isDir: false, size: 5120, modTime: '2024-01-04T00:00:00Z' },
          { name: 'archive.zip', path: '/archive.zip', isDir: false, size: 8192, modTime: '2024-01-05T00:00:00Z' },
          { name: 'code.ts', path: '/code.ts', isDir: false, size: 512, modTime: '2024-01-06T00:00:00Z' },
        ],
        path: '/',
      })
    })

    it('renders different file types', async () => {
      render(<FilesPage />)
      
      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })
    })
  })

  describe('error states', () => {
    it('handles API loading state', async () => {
      mockListFiles.mockImplementation(() => new Promise(() => {})) // Never resolves
      render(<FilesPage />)
      
      expect(screen.getByText('加载记忆中...')).toBeTruthy()
    })

    it('shows retryable error state on API failure', async () => {
      mockListFiles.mockRejectedValueOnce(new Error('Network error'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('当前目录加载失败')).toBeTruthy()
        expect(screen.getByText('Network error')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('retries on API failure', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockListFiles.mockRejectedValueOnce(new Error('Network error'))
      mockListFiles.mockResolvedValueOnce({ files: [], path: '/' })
      
      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      mockListFiles.mockClear()
      await user.click(screen.getByRole('button', { name: '重新加载' }))
      
      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalledTimes(1)
        expect(screen.getByText('这里空空如也')).toBeTruthy()
        expect(mockAddToast).toHaveBeenCalledWith({ title: '刷新成功', color: 'success' })
      })
    })

    it('shows a retryable warning when favorites status fails to load', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockCheckFavorites
        .mockRejectedValueOnce(new Error('favorites unavailable'))
        .mockResolvedValueOnce({
          '/documents': false,
          '/photo.jpg': true,
          '/video.mp4': false,
        })

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('收藏状态加载失败')).toBeTruthy()
        expect(screen.getByText('favorites unavailable')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载收藏状态' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载收藏状态' }))

      await waitFor(() => {
        expect(screen.queryByText('收藏状态加载失败')).toBeNull()
        expect(mockAddToast).toHaveBeenCalledWith({ title: '收藏状态已刷新', color: 'success' })
      })
    })

    it('shows warning toast when favorites status reload becomes unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockCheckFavorites
        .mockRejectedValueOnce(new Error('favorites unavailable'))
        .mockRejectedValueOnce(Object.assign(new Error('favorites unavailable'), { status: 503, code: 'FAVORITES_UNAVAILABLE' }))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载收藏状态' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载收藏状态' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '收藏功能暂不可用',
          description: '收藏存储未成功初始化，请检查系统健康状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('shows an unavailable state when the current directory returns service unavailable', async () => {
      mockListFiles.mockRejectedValueOnce(new ApiError('filesystem not initialized', 503, 'SERVICE_UNAVAILABLE'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('当前目录暂不可用')).toBeTruthy()
        expect(screen.getByText('文件系统当前不可用，请检查系统健康状态或稍后重试。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

  it('shows a feature-disabled warning when favorites are turned off', async () => {
    mockCheckFavorites.mockRejectedValueOnce(Object.assign(new Error('favorites feature disabled'), {
      status: 503,
      code: 'FAVORITES_FEATURE_DISABLED',
    }))

    render(<FilesPage />)

    await waitFor(() => {
      expect(screen.getByText('收藏功能已关闭')).toBeTruthy()
      expect(screen.getByText('当前服务已关闭收藏功能。启用后重新加载即可恢复收藏状态与相关操作。')).toBeTruthy()
    })
  })

  it('uses the specific unavailable label when favorites are temporarily unavailable', async () => {
    mockCheckFavorites.mockRejectedValueOnce(Object.assign(new Error('favorites unavailable'), {
      status: 503,
      code: 'FAVORITES_UNAVAILABLE',
    }))

    render(<FilesPage />)

    await waitFor(() => {
      expect(screen.getByText('收藏功能暂不可用')).toBeTruthy()
      expect(screen.getByText('收藏存储未成功初始化，请检查系统健康状态或稍后重试。')).toBeTruthy()
      expect(screen.queryByText('收藏状态不可用')).toBeNull()
    })
  })
  })
})
