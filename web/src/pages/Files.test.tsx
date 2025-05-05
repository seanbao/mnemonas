import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
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

function pendingFilesRefetch() {
  return new Promise<Awaited<ReturnType<typeof listFiles>>>(() => {})
}

async function openContextMenuFor(name: string, coordinates = { clientX: 120, clientY: 80 }) {
  const target = (await screen.findAllByText(name))[0]
  fireEvent.contextMenu(target, coordinates)

  return waitFor(() => {
    const menu = document.querySelector('[data-context-menu]')
    expect(menu).toBeTruthy()
    return menu as HTMLElement
  })
}

async function getFileActionArea(name: string) {
  const trigger = await screen.findByLabelText(`${name} 操作菜单`)
  const area = trigger.closest('.group')
  expect(area).toBeTruthy()
  return area as HTMLElement
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

    it('navigates from breadcrumb root and parent segments without route feedback loops', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.currentPath = '/alpha/beta'
      mockLocationPathname = '/files/alpha/beta'
      mockListFiles.mockResolvedValue({ files: [], path: '/alpha/beta' })
      mockCheckFavorites.mockResolvedValue({})

      render(<FilesPage />)

      await screen.findByRole('button', { name: 'beta' })
      mockFilesStoreState.setCurrentPath.mockClear()
      mockNavigate.mockClear()

      await user.click(screen.getByRole('button', { name: 'alpha' }))
      expect(mockFilesStoreState.setCurrentPath).toHaveBeenCalledWith('/alpha')
      expect(mockNavigate).toHaveBeenCalledWith('/files/alpha', { replace: false })

      await user.click(screen.getByRole('button', { name: '根目录' }))
      expect(mockFilesStoreState.setCurrentPath).toHaveBeenCalledWith('/')
      expect(mockNavigate).toHaveBeenCalledWith('/files', { replace: false })
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

    it('decodes encoded file route segments before syncing page state', async () => {
      mockLocationPathname = '/files/docs/a%20%231%3F'

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockFilesStoreState.setCurrentPath).toHaveBeenCalledWith('/docs/a #1?')
      })
    })

    it('does not rewrite a valid folder route back to the stale store path while syncing', async () => {
      mockLocationPathname = '/files/documents'

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockFilesStoreState.setCurrentPath).toHaveBeenCalledWith('/documents')
      })

      expect(mockNavigate).not.toHaveBeenCalledWith('/files', expect.objectContaining({ replace: true }))
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

    it('shows a synchronized warning when the folder already exists', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockCreateDirectory.mockResolvedValueOnce({ warning: false, message: 'directory already exists' })

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('新建空间')).toBeTruthy()
      })

      await user.click(screen.getByText('新建空间'))
      await user.type(await screen.findByPlaceholderText('请输入文件夹名称'), 'existing-folder')
      await user.click(screen.getByRole('button', { name: '创建' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '文件夹已存在，已同步更新',
          color: 'warning',
        })
      })

      await waitFor(() => {
        expect(screen.queryByText('新建文件夹')).toBeFalsy()
      })
    })

    it('keeps the create folder modal open with a localized warning when a name conflict occurs', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockCreateDirectory.mockRejectedValueOnce(new ApiError('resource already exists', 409))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('新建空间')).toBeTruthy()
      })

      await user.click(screen.getByText('新建空间'))
      await user.type(await screen.findByPlaceholderText('请输入文件夹名称'), 'conflict-folder')
      await user.click(screen.getByRole('button', { name: '创建' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '同名项目已存在',
          description: '当前目录中已存在同名文件或文件夹，请使用其他名称。',
          color: 'warning',
        })
      })

      expect(screen.getByText('新建文件夹')).toBeTruthy()
      expect(screen.getByDisplayValue('conflict-folder')).toBeTruthy()
    })

    it('keeps the create folder modal open with a localized warning when the parent path stops being a directory', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockCreateDirectory.mockRejectedValueOnce(new ApiError('parent path is not a directory', 409))

      render(<FilesPage />)

      await user.click(await screen.findByText('新建空间'))
      await user.type(await screen.findByPlaceholderText('请输入文件夹名称'), 'stale-parent')
      await user.click(screen.getByRole('button', { name: '创建' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '目标位置不可用',
          description: '当前目录状态已变更，请刷新列表后重试。',
          color: 'warning',
        })
      })

      expect(screen.getByText('新建文件夹')).toBeTruthy()
      expect(screen.getByDisplayValue('stale-parent')).toBeTruthy()
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

    it('activates a row for details without entering selection mode', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.viewMode = 'grid'
      render(<FilesPage />)

      const fileName = await screen.findByText('photo.jpg')
      mockFilesStoreState.setSelection.mockClear()
      mockFilesStoreState.toggleFileSelection.mockClear()
      mockFilesStoreState.clearSelection.mockClear()

      await user.click(fileName)

      expect(mockFilesStoreState.setSelection).not.toHaveBeenCalled()
      expect(mockFilesStoreState.toggleFileSelection).not.toHaveBeenCalled()
      expect(mockFilesStoreState.clearSelection).not.toHaveBeenCalled()
      expect(screen.queryByText('已选')).toBeNull()
      expect(screen.getAllByText('photo.jpg').length).toBeGreaterThan(1)
    })

    it('selects a row only from its checkbox', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.viewMode = 'grid'
      render(<FilesPage />)

      const checkbox = await screen.findByRole('checkbox', { name: '选择 photo.jpg' })
      mockFilesStoreState.setSelection.mockClear()
      mockFilesStoreState.toggleFileSelection.mockClear()

      await user.click(checkbox)

      expect(mockFilesStoreState.toggleFileSelection).toHaveBeenCalledWith('/photo.jpg')
      expect(mockFilesStoreState.setSelection).not.toHaveBeenCalledWith(['/photo.jpg'])
    })

    it('does not open a folder when its checkbox is double-clicked', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.viewMode = 'grid'
      render(<FilesPage />)

      const checkbox = await screen.findByRole('checkbox', { name: '选择 documents' })
      mockFilesStoreState.setCurrentPath.mockClear()

      await user.dblClick(checkbox)

      expect(mockFilesStoreState.setCurrentPath).not.toHaveBeenCalledWith('/documents')
    })

    it('keeps grid control shell pointer events from activating the card', async () => {
      mockFilesStoreState.viewMode = 'grid'
      render(<FilesPage />)

      const checkbox = await screen.findByRole('checkbox', { name: '选择 photo.jpg' })
      const checkboxShell = checkbox.closest('.absolute')
      expect(checkboxShell).toBeTruthy()

      mockFilesStoreState.setSelection.mockClear()
      mockFilesStoreState.toggleFileSelection.mockClear()

      fireEvent.click(checkboxShell as HTMLElement)
      fireEvent.doubleClick(checkboxShell as HTMLElement)
      fireEvent.contextMenu(checkboxShell as HTMLElement)

      const actionButton = screen.getByLabelText('photo.jpg 操作菜单')
      const actionShell = actionButton.closest('.absolute')
      expect(actionShell).toBeTruthy()

      fireEvent.click(actionShell as HTMLElement)
      fireEvent.doubleClick(actionShell as HTMLElement)
      fireEvent.contextMenu(actionShell as HTMLElement)

      expect(mockFilesStoreState.setSelection).not.toHaveBeenCalledWith(['/photo.jpg'])
      expect(mockFilesStoreState.toggleFileSelection).not.toHaveBeenCalled()
    })

    it('clears grid selection from an empty grid-space click', async () => {
      mockFilesStoreState.viewMode = 'grid'
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      render(<FilesPage />)

      const gridContainer = (await screen.findAllByText('photo.jpg'))[0].closest('.custom-scrollbar')
      expect(gridContainer).toBeTruthy()

      vi.useFakeTimers()
      fireEvent.click(gridContainer as HTMLElement)
      expect(mockFilesStoreState.clearSelection).toHaveBeenCalled()

      act(() => {
        vi.advanceTimersByTime(1500)
      })
      vi.useRealTimers()
    })

    it('clears a pending grid multi-select hint timer on unmount', async () => {
      mockFilesStoreState.viewMode = 'grid'
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      const { unmount } = render(<FilesPage />)

      const gridContainer = (await screen.findAllByText('photo.jpg'))[0].closest('.custom-scrollbar')
      expect(gridContainer).toBeTruthy()

      vi.useFakeTimers()
      fireEvent.click(gridContainer as HTMLElement)
      fireEvent.click(gridContainer as HTMLElement)
      unmount()
      vi.useRealTimers()

      expect(mockFilesStoreState.clearSelection).toHaveBeenCalled()
    })

    it('opens a folder by syncing the file path state and route together', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.viewMode = 'grid'
      render(<FilesPage />)

      const folderName = await screen.findByText('documents')
      mockFilesStoreState.setCurrentPath.mockClear()
      mockNavigate.mockClear()

      await user.dblClick(folderName)

      expect(mockFilesStoreState.setCurrentPath).toHaveBeenCalledWith('/documents')
      expect(mockNavigate).toHaveBeenCalledWith('/files/documents', { replace: false })
      expect(mockNavigate).not.toHaveBeenCalledWith('/files', expect.objectContaining({ replace: true }))
    })

    it('marks only selected rows as multi-selected', async () => {
      mockFilesStoreState.viewMode = 'grid'
      mockFilesStoreState.selectedFiles = new Set(['/documents', '/photo.jpg'])

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getAllByText('多选中')).toHaveLength(2)
        expect(screen.getByText('9.77 MB')).toBeTruthy()
      })
    })

    it('moves keyboard focus for details without selecting on arrow keys', async () => {
      mockFilesStoreState.viewMode = 'grid'
      render(<FilesPage />)

      await screen.findByText('photo.jpg')
      mockFilesStoreState.setSelection.mockClear()
      mockFilesStoreState.toggleFileSelection.mockClear()
      mockFilesStoreState.clearSelection.mockClear()

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
      })
      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
      })

      expect(mockFilesStoreState.setSelection).not.toHaveBeenCalled()
      expect(mockFilesStoreState.toggleFileSelection).not.toHaveBeenCalled()
      expect(mockFilesStoreState.clearSelection).not.toHaveBeenCalled()
      expect(screen.queryByText('已选')).toBeNull()
      expect(screen.getAllByText('photo.jpg').length).toBeGreaterThan(1)
    })

    it('toggles selection for the focused row with Space', async () => {
      mockFilesStoreState.viewMode = 'grid'
      render(<FilesPage />)

      await screen.findByText('photo.jpg')

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
      })
      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
      })
      mockFilesStoreState.toggleFileSelection.mockClear()

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }))
      })

      expect(mockFilesStoreState.toggleFileSelection).toHaveBeenCalledWith('/photo.jpg')
    })

    it('supports keyboard range selection with Shift+Arrow keys', async () => {
      mockFilesStoreState.viewMode = 'grid'
      render(<FilesPage />)

      await screen.findByText('photo.jpg')
      mockFilesStoreState.setSelection.mockClear()

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
      })
      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', shiftKey: true, bubbles: true }))
      })

      expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/documents', '/photo.jpg'])

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', shiftKey: true, bubbles: true }))
      })

      expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/documents'])
    })

    it('runs clipboard and clear keyboard shortcuts for selected files', async () => {
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg', '/video.mp4'])
      render(<FilesPage />)

      await screen.findByText('批量下载（仅文件）')

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'c', ctrlKey: true, bubbles: true }))
      })
      expect(mockClipboardState.copy).toHaveBeenCalledWith(['/photo.jpg', '/video.mp4'], '/')
      expect(mockAddToast).toHaveBeenCalledWith({ title: '已复制 2 个项目', color: 'success' })

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'x', ctrlKey: true, bubbles: true }))
      })
      expect(mockClipboardState.cut).toHaveBeenCalledWith(['/photo.jpg', '/video.mp4'], '/')
      expect(mockAddToast).toHaveBeenCalledWith({ title: '已剪切 2 个项目', color: 'success' })

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
      })
      expect(mockFilesStoreState.clearSelection).toHaveBeenCalled()
    })

    it('opens delete and preview actions from keyboard state', async () => {
      mockFilesStoreState.viewMode = 'grid'
      const firstRender = render(<FilesPage />)

      await screen.findByText('photo.jpg')

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
      })
      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
      })
      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Delete', bubbles: true }))
      })

      expect(await screen.findByRole('heading', { name: '确认删除' })).toBeTruthy()

      firstRender.unmount()
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getAllByText('photo.jpg').length).toBeGreaterThan(0)
      })
      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
      })

      expect(screen.getAllByText('photo.jpg').length).toBeGreaterThan(1)
    })

    it('renames the active row without requiring selection', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.viewMode = 'grid'
      render(<FilesPage />)

      const fileName = await screen.findByText('photo.jpg')
      await user.click(fileName)

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'F2', bubbles: true }))
      })

      expect(await screen.findByDisplayValue('photo.jpg')).toBeTruthy()
    })

    it('shows selection summary when items are selected', async () => {
      mockFilesStoreState.selectedFiles = new Set(['/documents', '/photo.jpg'])
      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('已选')).toBeTruthy()
        expect(screen.getByText('选择工具')).toBeTruthy()
      })
    })

    it('opens batch move and copy dialogs from the selection toolbar', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      render(<FilesPage />)

      await user.click(await screen.findByText('批量移动'))
      expect(await screen.findByText('移动到')).toBeTruthy()

      await user.click(screen.getByRole('button', { name: '取消' }))
      await user.click(screen.getByText('批量复制'))
      expect(await screen.findByText('复制到')).toBeTruthy()
    })

    it('runs selection helper commands from the selection toolbar', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      render(<FilesPage />)

      await user.click(await screen.findByText('反选'))
      expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/documents', '/video.mp4'])

      await user.click(screen.getByText('仅文件'))
      expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/photo.jpg', '/video.mp4'])

      await user.click(screen.getByText('仅文件夹'))
      expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/documents'])
    })

    it('toggles all files from the list header checkbox', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<FilesPage />)

      await user.click(await screen.findByRole('checkbox', { name: '全选当前目录' }))
      expect(mockFilesStoreState.selectAll).toHaveBeenCalledWith(['/documents', '/photo.jpg', '/video.mp4'])

      mockFilesStoreState.selectedFiles = new Set(['/documents', '/photo.jpg', '/video.mp4'])
      const { unmount } = render(<FilesPage />)
      await user.click(await screen.findByRole('checkbox', { name: '取消全选' }))
      expect(mockFilesStoreState.clearSelection).toHaveBeenCalled()
      unmount()
    })

    it('supports mouse range selection and ctrl-activation without opening items', async () => {
      mockFilesStoreState.viewMode = 'grid'
      mockFilesStoreState.selectedFiles = new Set(['/documents'])
      render(<FilesPage />)

      await screen.findByText('photo.jpg')
      mockFilesStoreState.setSelection.mockClear()
      mockFilesStoreState.toggleFileSelection.mockClear()
      mockFilesStoreState.setCurrentPath.mockClear()

      fireEvent.click(screen.getByRole('checkbox', { name: '选择 documents' }))
      fireEvent.click(screen.getByRole('checkbox', { name: '选择 video.mp4' }), { shiftKey: true })

      expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/documents', '/photo.jpg', '/video.mp4'])

      const photoCard = (await screen.findByText('photo.jpg')).closest('.group')
      expect(photoCard).toBeTruthy()
      fireEvent.click(photoCard as HTMLElement, { ctrlKey: true })

      expect(mockFilesStoreState.toggleFileSelection).toHaveBeenCalledWith('/photo.jpg')
      expect(mockFilesStoreState.setCurrentPath).not.toHaveBeenCalled()
    })

    it('clears existing selection when keyboard focus moves to a file', async () => {
      mockFilesStoreState.viewMode = 'grid'
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getAllByText('photo.jpg').length).toBeGreaterThan(0)
      })
      mockFilesStoreState.clearSelection.mockClear()

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
      })

      expect(mockFilesStoreState.clearSelection).toHaveBeenCalled()
    })

    it('toggles the first folder when Space is pressed without a focused row', async () => {
      mockFilesStoreState.viewMode = 'grid'
      render(<FilesPage />)

      await screen.findByText('documents')
      mockFilesStoreState.toggleFileSelection.mockClear()

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }))
      })

      expect(mockFilesStoreState.toggleFileSelection).toHaveBeenCalledWith('/documents')
    })

    it('ignores keyboard navigation and selection shortcuts when the directory is empty', async () => {
      mockListFiles.mockResolvedValue({ files: [], path: '/' })
      render(<FilesPage />)

      await screen.findByText('这里空空如也')
      mockFilesStoreState.setSelection.mockClear()
      mockFilesStoreState.toggleFileSelection.mockClear()

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', shiftKey: true, bubbles: true }))
        document.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }))
      })

      expect(mockFilesStoreState.setSelection).not.toHaveBeenCalled()
      expect(mockFilesStoreState.toggleFileSelection).not.toHaveBeenCalled()
    })

    it('keeps write keyboard shortcuts inert for read-only users', async () => {
      useCanWriteMock.mockReturnValue(false)
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      mockClipboardState.paths = ['/source/photo.jpg']
      mockClipboardState.operation = 'copy'
      mockClipboardState.sourcePath = '/source'
      mockClipboardState.hasPaths.mockReturnValue(true)

      render(<FilesPage />)

      await screen.findByText('批量下载（仅文件）')

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'c', ctrlKey: true, bubbles: true }))
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'x', ctrlKey: true, bubbles: true }))
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'v', ctrlKey: true, bubbles: true }))
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Delete', bubbles: true }))
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'F2', bubbles: true }))
      })

      expect(mockClipboardState.copy).not.toHaveBeenCalled()
      expect(mockClipboardState.cut).not.toHaveBeenCalled()
      expect(mockCopyFile).not.toHaveBeenCalled()
      expect(screen.queryByRole('heading', { name: '批量删除' })).toBeFalsy()
      expect(screen.queryByPlaceholderText('请输入新名称')).toBeFalsy()
    })

    it('opens batch delete from the keyboard when files are already selected', async () => {
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      render(<FilesPage />)

      await screen.findByText('批量删除')

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Delete', bubbles: true }))
      })

      expect(await screen.findByRole('heading', { name: '批量删除' })).toBeTruthy()
    })

    it('inverts an all-selected directory back to an empty selection', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.selectedFiles = new Set(['/documents', '/photo.jpg', '/video.mp4'])
      render(<FilesPage />)

      await user.click(await screen.findByText('反选'))

      expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith([])
    })

    it('forwards upload toolbar buttons to their hidden file inputs', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<FilesPage />)

      await screen.findByText('上传文件')
      const inputs = document.querySelectorAll('input[type="file"]')
      expect(inputs).toHaveLength(2)

      const fileInputClick = vi.spyOn(inputs[0] as HTMLInputElement, 'click').mockImplementation(() => undefined)
      const folderInputClick = vi.spyOn(inputs[1] as HTMLInputElement, 'click').mockImplementation(() => undefined)

      await user.click(screen.getByRole('button', { name: '上传文件' }))
      expect(fileInputClick).toHaveBeenCalled()

      await user.click(screen.getByRole('button', { name: '上传文件夹' }))
      expect(folderInputClick).toHaveBeenCalled()
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

  describe('context menu', () => {
    it('runs copy actions from the grid card menu', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })
      mockFilesStoreState.viewMode = 'grid'

      render(<FilesPage />)

      const actionArea = await getFileActionArea('photo.jpg')
      await user.click(within(actionArea).getByText('复制路径'))

      await waitFor(() => {
        expect(writeText).toHaveBeenCalledWith('/photo.jpg')
        expect(mockAddToast).toHaveBeenCalledWith({ title: '路径已复制', color: 'success' })
      })
    })

    it('shows a danger toast when copying from the grid card menu fails', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) },
      })
      mockFilesStoreState.viewMode = 'grid'

      render(<FilesPage />)

      const actionArea = await getFileActionArea('photo.jpg')
      await user.click(within(actionArea).getByText('复制路径'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '复制失败', color: 'danger' })
      })
    })

    it('runs write and history actions from the grid card menu', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.viewMode = 'grid'

      render(<FilesPage />)

      const actionArea = await getFileActionArea('photo.jpg')

      await user.click(within(actionArea).getByText('重命名'))
      expect(await screen.findByDisplayValue('photo.jpg')).toBeTruthy()
      await user.click(screen.getByRole('button', { name: '取消' }))

      await user.click(within(actionArea).getByText('删除'))
      expect(await screen.findByRole('heading', { name: '确认删除' })).toBeTruthy()
      await user.click(screen.getByRole('button', { name: '取消' }))

      await user.click(within(actionArea).getByText('添加收藏'))
      await waitFor(() => {
        expect(mockToggleFavorite).toHaveBeenCalledWith('/photo.jpg', false)
      })

      await user.click(within(actionArea).getByText('创建分享链接'))
      expect(screen.getAllByText('创建分享链接').length).toBeGreaterThan(0)

      await user.click(within(actionArea).getByText('查看版本历史'))
      expect(mockNavigate).toHaveBeenCalledWith('/versions?path=%2Fphoto.jpg')
    })

    it('executes file actions from the custom context menu', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })
      mockFilesStoreState.viewMode = 'grid'

      render(<FilesPage />)

      await screen.findByText('photo.jpg')
      mockFilesStoreState.setSelection.mockClear()
      mockNavigate.mockClear()
      mockDownloadFile.mockClear()

      let menu = await openContextMenuFor('photo.jpg')
      expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/photo.jpg'])
      await user.click(within(menu).getByRole('button', { name: '复制路径' }))

      await waitFor(() => {
        expect(writeText).toHaveBeenCalledWith('/photo.jpg')
        expect(mockAddToast).toHaveBeenCalledWith({ title: '路径已复制', color: 'success' })
      })

      menu = await openContextMenuFor('photo.jpg', { clientX: 140, clientY: 90 })
      await user.click(within(menu).getByRole('button', { name: '下载' }))

      await waitFor(() => {
        expect(mockDownloadFile).toHaveBeenCalledWith('/photo.jpg', { filename: 'photo.jpg' })
      })

      menu = await openContextMenuFor('photo.jpg', { clientX: 160, clientY: 100 })
      await user.click(within(menu).getByRole('button', { name: '查看版本历史' }))

      expect(mockNavigate).toHaveBeenCalledWith('/versions?path=%2Fphoto.jpg')
    })

    it('opens folders and protects folder-only actions from the custom context menu', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.viewMode = 'grid'
      render(<FilesPage />)

      await screen.findByText('documents')
      mockFilesStoreState.setCurrentPath.mockClear()
      mockNavigate.mockClear()

      const menu = await openContextMenuFor('documents')
      expect(within(menu).getByRole('button', { name: '查看版本历史' })).toBeDisabled()

      await user.click(within(menu).getByRole('button', { name: '打开文件夹' }))

      expect(mockFilesStoreState.setCurrentPath).toHaveBeenCalledWith('/documents')
      expect(mockNavigate).toHaveBeenCalledWith('/files/documents', { replace: false })
    })

    it('runs multi-selection commands from the custom context menu', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.viewMode = 'grid'
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg', '/video.mp4'])
      mockDownloadFile.mockResolvedValue(undefined)

      render(<FilesPage />)

      await screen.findByText('photo.jpg')
      mockFilesStoreState.clearSelection.mockClear()
      mockFilesStoreState.setSelection.mockClear()

      let menu = await openContextMenuFor('photo.jpg')
      expect(within(menu).getByText('已选 2 项')).toBeTruthy()
      await user.click(within(menu).getByRole('button', { name: '反选' }))
      expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/documents'])

      menu = await openContextMenuFor('photo.jpg', { clientX: 150, clientY: 90 })
      await user.click(within(menu).getByRole('button', { name: '仅文件（2）' }))
      expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/photo.jpg', '/video.mp4'])

      menu = await openContextMenuFor('photo.jpg', { clientX: 160, clientY: 100 })
      await user.click(within(menu).getByRole('button', { name: '仅文件夹（1）' }))
      expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/documents'])

      menu = await openContextMenuFor('photo.jpg', { clientX: 170, clientY: 110 })
      await user.click(within(menu).getByRole('button', { name: '批量下载（仅文件）' }))

      await waitFor(() => {
        expect(mockDownloadFile).toHaveBeenCalledWith('/photo.jpg', { filename: 'photo.jpg' })
        expect(mockDownloadFile).toHaveBeenCalledWith('/video.mp4', { filename: 'video.mp4' })
      })

      menu = await openContextMenuFor('photo.jpg', { clientX: 180, clientY: 120 })
      await user.click(within(menu).getByRole('button', { name: '清空选择' }))
      expect(mockFilesStoreState.clearSelection).toHaveBeenCalled()

      menu = await openContextMenuFor('photo.jpg', { clientX: 190, clientY: 130 })
      await user.click(within(menu).getByRole('button', { name: '批量删除（进回收站）' }))
      expect(screen.getByRole('heading', { name: '批量删除' })).toBeTruthy()
    })

    it('opens batch move and copy from the multi-selection custom context menu', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.viewMode = 'grid'
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg', '/video.mp4'])

      render(<FilesPage />)

      let menu = await openContextMenuFor('photo.jpg')
      await user.click(within(menu).getByRole('button', { name: '批量移动' }))
      expect(await screen.findByText('移动到')).toBeTruthy()

      await user.click(screen.getByRole('button', { name: '取消' }))

      menu = await openContextMenuFor('photo.jpg', { clientX: 150, clientY: 100 })
      await user.click(within(menu).getByRole('button', { name: '批量复制' }))
      expect(await screen.findByText('复制到')).toBeTruthy()
    })

    it('runs single-item write actions from the custom context menu', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.viewMode = 'grid'

      render(<FilesPage />)

      await screen.findByText('photo.jpg')

      let menu = await openContextMenuFor('photo.jpg')
      await user.click(within(menu).getByRole('button', { name: '重命名' }))
      expect(await screen.findByDisplayValue('photo.jpg')).toBeTruthy()
      await user.click(screen.getByRole('button', { name: '取消' }))

      menu = await openContextMenuFor('photo.jpg', { clientX: 150, clientY: 90 })
      await user.click(within(menu).getByRole('button', { name: '移动到...' }))
      expect(await screen.findByText('移动到')).toBeTruthy()
      await user.click(screen.getByRole('button', { name: '取消' }))

      menu = await openContextMenuFor('photo.jpg', { clientX: 160, clientY: 100 })
      await user.click(within(menu).getByRole('button', { name: '复制到...' }))
      expect(await screen.findByText('复制到')).toBeTruthy()
      await user.click(screen.getByRole('button', { name: '取消' }))

      menu = await openContextMenuFor('photo.jpg', { clientX: 170, clientY: 110 })
      await user.click(within(menu).getByRole('button', { name: '添加收藏' }))
      await waitFor(() => {
        expect(mockToggleFavorite).toHaveBeenCalledWith('/photo.jpg', false)
      })

      menu = await openContextMenuFor('photo.jpg', { clientX: 180, clientY: 120 })
      await user.click(within(menu).getByRole('button', { name: '创建分享链接' }))
      expect(screen.getAllByText('创建分享链接').length).toBeGreaterThan(0)

      menu = await openContextMenuFor('photo.jpg', { clientX: 190, clientY: 130 })
      await user.click(within(menu).getByRole('button', { name: '删除' }))
      expect(await screen.findByRole('heading', { name: '确认删除' })).toBeTruthy()
    })

    it('shows failure toasts from custom context menu download and copy actions', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.viewMode = 'grid'
      mockDownloadFile.mockRejectedValueOnce(new Error('download failed'))
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) },
      })

      render(<FilesPage />)

      await screen.findByText('photo.jpg')

      let menu = await openContextMenuFor('photo.jpg')
      await user.click(within(menu).getByRole('button', { name: '下载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '下载失败',
          color: 'danger',
        }))
      })

      menu = await openContextMenuFor('photo.jpg', { clientX: 150, clientY: 95 })
      await user.click(within(menu).getByRole('button', { name: '复制路径' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '复制失败', color: 'danger' })
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
        expect(screen.getAllByText('photo.jpg').length).toBeGreaterThan(0)
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

    it('removes a stale file and closes the modal when single delete hits not found', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.viewMode = 'grid'
      mockDeleteFile.mockRejectedValue(new ApiError('file not found', 404, 'FILE_NOT_FOUND'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getAllByText('photo.jpg').length).toBeGreaterThan(0)
      })

      mockListFiles.mockImplementation(() => pendingFilesRefetch())

      await user.click(screen.getAllByRole('button', { name: '删除' })[1])

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: '确认删除' })).toBeTruthy()
      })

      const deleteButtons = screen.getAllByRole('button', { name: '删除' })
      await user.click(deleteButtons[deleteButtons.length - 1])

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '文件或文件夹已不存在，已同步更新',
          color: 'warning',
        })
      })

      await waitFor(() => {
        expect(screen.queryByRole('heading', { name: '确认删除' })).toBeFalsy()
        expect(screen.queryByText('photo.jpg')).toBeFalsy()
        expect(screen.getByText('video.mp4')).toBeTruthy()
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

    it('treats batch delete not-found results as already synchronized and clears selection', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.viewMode = 'grid'
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg', '/video.mp4'])
      mockDeleteFile.mockRejectedValue(new ApiError('file not found', 404, 'FILE_NOT_FOUND'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('批量删除')).toBeTruthy()
      })

      mockListFiles.mockImplementation(() => pendingFilesRefetch())

      mockFilesStoreState.clearSelection.mockClear()
      mockFilesStoreState.setSelection.mockClear()

      await user.click(screen.getByText('批量删除'))

      await waitFor(() => {
        expect(screen.getByText('删除全部')).toBeTruthy()
      })

      await user.click(screen.getByText('删除全部'))

      await waitFor(() => {
        expect(mockFilesStoreState.clearSelection).toHaveBeenCalled()
      })

      expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith([])
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '文件或文件夹已不存在，已同步更新',
        color: 'warning',
      })

      await waitFor(() => {
        expect(screen.queryByRole('heading', { name: '批量删除' })).toBeFalsy()
        expect(screen.queryByText('photo.jpg')).toBeFalsy()
        expect(screen.queryByText('video.mp4')).toBeFalsy()
        expect(screen.getByText('documents')).toBeTruthy()
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

    it('removes stale cut items from clipboard when paste source no longer exists', async () => {
      mockClipboardState.paths = ['/source/photo.jpg']
      mockClipboardState.operation = 'cut'
      mockClipboardState.sourcePath = '/source'
      mockClipboardState.hasPaths.mockReturnValue(true)
      mockMoveFile.mockRejectedValue(new ApiError('file not found', 404, 'FILE_NOT_FOUND'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })

      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'v', ctrlKey: true, bubbles: true }))

      await waitFor(() => {
        expect(mockMoveFile).toHaveBeenCalledWith('/source/photo.jpg', '/photo.jpg')
      })

      expect(mockClipboardState.clear).toHaveBeenCalled()
      expect(mockClipboardState.cut).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '文件或文件夹已不存在，已同步更新',
        color: 'warning',
      })
    })

    it('removes stale copied items from clipboard while preserving remaining paths', async () => {
      mockClipboardState.paths = ['/source/photo.jpg', '/source/video.mp4']
      mockClipboardState.operation = 'copy'
      mockClipboardState.sourcePath = '/source'
      mockClipboardState.hasPaths.mockReturnValue(true)
      mockCopyFile
        .mockRejectedValueOnce(new ApiError('file not found', 404, 'FILE_NOT_FOUND'))
        .mockResolvedValueOnce(successActionResult)

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })

      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'v', ctrlKey: true, bubbles: true }))

      await waitFor(() => {
        expect(mockCopyFile).toHaveBeenCalledTimes(2)
      })

      expect(mockClipboardState.copy).toHaveBeenCalledWith(['/source/video.mp4'], '/source')
      expect(mockClipboardState.clear).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '文件或文件夹已不存在，已同步更新',
        color: 'warning',
      })
    })

    it('clears copied clipboard paths when every copied source is stale', async () => {
      mockClipboardState.paths = ['/source/photo.jpg']
      mockClipboardState.operation = 'copy'
      mockClipboardState.sourcePath = '/source'
      mockClipboardState.hasPaths.mockReturnValue(true)
      mockCopyFile.mockRejectedValue(new ApiError('file not found', 404, 'FILE_NOT_FOUND'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })

      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'v', ctrlKey: true, bubbles: true }))

      await waitFor(() => {
        expect(mockCopyFile).toHaveBeenCalledWith('/source/photo.jpg', '/photo.jpg')
      })

      expect(mockClipboardState.clear).toHaveBeenCalled()
      expect(mockClipboardState.copy).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '文件或文件夹已不存在，已同步更新',
        color: 'warning',
      })
    })

    it('shows unavailable toast when copy paste fully fails because the filesystem is unavailable', async () => {
      mockClipboardState.paths = ['/source/photo.jpg']
      mockClipboardState.operation = 'copy'
      mockClipboardState.sourcePath = '/source'
      mockClipboardState.hasPaths.mockReturnValue(true)
      mockCopyFile.mockRejectedValue(new ApiError('filesystem not initialized', 503, 'SERVICE_UNAVAILABLE'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })

      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'v', ctrlKey: true, bubbles: true }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '批量复制暂不可用',
          description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
          color: 'warning',
        })
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

    it('preserves warning detail when batch delete partially succeeds with warnings', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg', '/video.mp4'])
      mockDeleteFile
        .mockResolvedValueOnce(warningActionResult('file deleted with persistence warning'))
        .mockRejectedValueOnce(new Error('delete failed'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getByText('批量删除')).toBeTruthy()
      })

      await user.click(screen.getByText('批量删除'))
      await user.click(await screen.findByText('删除全部'))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'file deleted with persistence warning',
          description: '成功 1 个，失败 1 个',
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

    it('preserves warning detail when cut paste partially succeeds with warnings', async () => {
      mockClipboardState.paths = ['/source/photo.jpg', '/source/video.mp4']
      mockClipboardState.operation = 'cut'
      mockClipboardState.sourcePath = '/source'
      mockClipboardState.hasPaths.mockReturnValue(true)
      mockMoveFile
        .mockResolvedValueOnce(warningActionResult('resource moved with persistence warning'))
        .mockRejectedValueOnce(new Error('move failed'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })

      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'v', ctrlKey: true, bubbles: true }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: 'resource moved with persistence warning',
          description: '成功 1 个，失败 1 个',
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
        expect(screen.getAllByText('photo.jpg').length).toBeGreaterThan(0)
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

    it('closes the rename modal and removes a stale file when rename hits not found', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])
      mockFilesStoreState.viewMode = 'grid'
      mockMoveFile.mockRejectedValueOnce(new ApiError('file not found', 404, 'FILE_NOT_FOUND'))

      render(<FilesPage />)

      await waitFor(() => {
        expect(screen.getAllByText('photo.jpg').length).toBeGreaterThan(0)
      })

      mockListFiles.mockImplementation(() => pendingFilesRefetch())

      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'F2', bubbles: true }))
      })

      const renameInput = await screen.findByPlaceholderText('请输入新名称')
      await user.clear(renameInput)
      await user.type(renameInput, 'photo-gone.jpg')
      await user.click(screen.getByRole('button', { name: '确定' }))

      await waitFor(() => {
        expect(mockMoveFile).toHaveBeenCalledWith('/photo.jpg', '/photo-gone.jpg')
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '文件或文件夹已不存在，已同步更新',
          color: 'warning',
        })
      })

      await waitFor(() => {
        expect(screen.queryByPlaceholderText('请输入新名称')).toBeFalsy()
        expect(screen.queryByRole('button', { name: '确定' })).toBeFalsy()
        expect(screen.queryAllByText('photo.jpg')).toHaveLength(0)
        expect(screen.getByText('video.mp4')).toBeTruthy()
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

    it('exposes sort controls', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<FilesPage />)

      await screen.findByRole('button', { name: '排序：名称' })

      await user.click(screen.getByRole('button', { name: '按名称' }))
      expect(mockFilesStoreState.setSortBy).toHaveBeenCalledWith('name')

      await user.click(screen.getByRole('button', { name: '按大小' }))
      expect(mockFilesStoreState.setSortBy).toHaveBeenCalledWith('size')

      await user.click(screen.getByRole('button', { name: '按修改时间' }))
      expect(mockFilesStoreState.setSortBy).toHaveBeenCalledWith('modTime')

      await user.click(screen.getByRole('button', { name: '切换为降序' }))
      expect(mockFilesStoreState.toggleSortOrder).toHaveBeenCalled()

      await user.click(screen.getByRole('button', { name: '网格视图' }))
      expect(mockFilesStoreState.setViewMode).toHaveBeenCalledWith('grid')

      await user.click(screen.getByRole('button', { name: '列表视图' }))
      expect(mockFilesStoreState.setViewMode).toHaveBeenCalledWith('list')
    })

    it('sorts files by size and modification time from the store state', async () => {
      mockFilesStoreState.viewMode = 'grid'
      mockFilesStoreState.sortBy = 'size'
      mockFilesStoreState.sortOrder = 'asc'
      const { unmount } = render(<FilesPage />)

      await waitFor(() => {
        const text = document.body.textContent ?? ''
        expect(text.indexOf('photo.jpg')).toBeGreaterThanOrEqual(0)
        expect(text.indexOf('video.mp4')).toBeGreaterThan(text.indexOf('photo.jpg'))
      })

      unmount()
      vi.clearAllMocks()
      mockListShares.mockResolvedValue([])
      mockCheckFavorites.mockResolvedValue({
        '/documents': false,
        '/photo.jpg': false,
        '/video.mp4': false,
      })
      mockFilesStoreState.viewMode = 'grid'
      mockFilesStoreState.sortBy = 'modTime'
      mockFilesStoreState.sortOrder = 'desc'

      render(<FilesPage />)

      await waitFor(() => {
        const text = document.body.textContent ?? ''
        expect(text.indexOf('video.mp4')).toBeGreaterThanOrEqual(0)
        expect(text.indexOf('photo.jpg')).toBeGreaterThan(text.indexOf('video.mp4'))
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

    it('shows danger toast when favorites status reload fails with a generic error', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockCheckFavorites
        .mockRejectedValueOnce(new Error('favorites unavailable'))
        .mockRejectedValueOnce(new Error('still down'))

      render(<FilesPage />)

      await screen.findByRole('button', { name: '重新加载收藏状态' })

      await user.click(screen.getByRole('button', { name: '重新加载收藏状态' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '刷新失败',
          description: 'still down',
          color: 'danger',
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

  it('shows a success toast after adding a favorite', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockFilesStoreState.viewMode = 'grid'
    mockToggleFavorite.mockResolvedValueOnce(true)

    render(<FilesPage />)

    await screen.findByText('photo.jpg')
    const actionArea = await getFileActionArea('photo.jpg')
    await user.click(within(actionArea).getByText('添加收藏'))

    await waitFor(() => {
      expect(mockToggleFavorite).toHaveBeenCalledWith('/photo.jpg', false)
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '已添加收藏',
        color: 'success',
      })
    })
  })

  it('shows a success toast after removing a favorite', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockFilesStoreState.viewMode = 'grid'
    mockCheckFavorites.mockResolvedValueOnce({
      '/documents': false,
      '/photo.jpg': true,
      '/video.mp4': false,
    })
    mockToggleFavorite.mockResolvedValueOnce(false)

    render(<FilesPage />)

    await screen.findByText('photo.jpg')
    const actionArea = await getFileActionArea('photo.jpg')
    await user.click(await within(actionArea).findByText('取消收藏'))

    await waitFor(() => {
      expect(mockToggleFavorite).toHaveBeenCalledWith('/photo.jpg', true)
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '已取消收藏',
        color: 'success',
      })
    })
  })

  it('shows the feature-disabled toast when adding a favorite is blocked by configuration', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockFilesStoreState.viewMode = 'grid'
    mockToggleFavorite.mockRejectedValueOnce(Object.assign(new Error('favorites feature disabled'), {
      status: 503,
      code: 'FAVORITES_FEATURE_DISABLED',
    }))

    render(<FilesPage />)

    await screen.findByText('photo.jpg')
    const actionArea = await getFileActionArea('photo.jpg')
    await user.click(within(actionArea).getByText('添加收藏'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '收藏功能已关闭',
        description: '当前服务已关闭收藏功能。启用后重新加载即可恢复收藏状态与相关操作。',
        color: 'warning',
      })
    })
  })

  it('shows the unavailable toast when favorites storage rejects an action', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockFilesStoreState.viewMode = 'grid'
    mockToggleFavorite.mockRejectedValueOnce(Object.assign(new Error('favorites unavailable'), {
      status: 503,
      code: 'FAVORITES_UNAVAILABLE',
    }))

    render(<FilesPage />)

    await screen.findByText('photo.jpg')
    const actionArea = await getFileActionArea('photo.jpg')
    await user.click(within(actionArea).getByText('添加收藏'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '收藏功能暂不可用',
        description: '收藏存储未成功初始化，请检查系统健康状态或稍后重试。',
        color: 'warning',
      })
    })
  })

  it('shows a generic danger toast when adding a favorite fails unexpectedly', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockFilesStoreState.viewMode = 'grid'
    mockToggleFavorite.mockRejectedValueOnce(new Error('database timeout'))

    render(<FilesPage />)

    await screen.findByText('photo.jpg')
    const actionArea = await getFileActionArea('photo.jpg')
    await user.click(within(actionArea).getByText('添加收藏'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '操作失败',
        description: 'database timeout',
        color: 'danger',
      })
    })
  })

  it('treats add-favorite conflict as already favorited and syncs the status', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockFilesStoreState.viewMode = 'grid'
    mockCheckFavorites
      .mockResolvedValueOnce({
        '/documents': false,
        '/photo.jpg': false,
        '/video.mp4': false,
      })
      .mockResolvedValueOnce({
        '/documents': false,
        '/photo.jpg': true,
        '/video.mp4': false,
      })
    mockToggleFavorite.mockRejectedValueOnce(Object.assign(new Error('favorite already exists'), {
      status: 409,
      code: 'FAVORITE_ALREADY_EXISTS',
    }))

    render(<FilesPage />)

    await waitFor(() => {
      expect(screen.getByText('photo.jpg')).toBeTruthy()
    })

    await user.click(screen.getByLabelText('photo.jpg 操作菜单'))

    await waitFor(() => {
      expect(screen.getAllByText('添加收藏').length).toBeGreaterThan(0)
    })

    await user.click(screen.getAllByText('添加收藏')[0])

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '已在收藏夹中',
        description: '该文件已被其他操作加入收藏，状态已同步。',
        color: 'warning',
      })
    })

    await waitFor(() => {
      expect(mockCheckFavorites).toHaveBeenCalledTimes(2)
    })
  })

  it('does not reuse cached favorite status from another user session', async () => {
  const user = userEvent.setup({ writeToClipboard: false })
  mockFilesStoreState.viewMode = 'grid'
  mockUser.id = 'u2'
  mockUser.username = 'member'
  mockUser.role = 'user'
  mockCheckFavorites.mockImplementation(() => new Promise(() => {}))

  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        gcTime: 0,
        staleTime: 0,
      },
    },
  })
  queryClient.setQueryData(['favorites-check', ['/documents', '/photo.jpg', '/video.mp4']], {
    '/documents': false,
    '/photo.jpg': true,
    '/video.mp4': false,
  })

  render(
    <QueryClientProvider client={queryClient}>
      <FilesPage />
    </QueryClientProvider>
  )

  await waitFor(() => {
    expect(screen.getByText('photo.jpg')).toBeTruthy()
  })

  await user.click(screen.getByLabelText('photo.jpg 操作菜单'))

  await waitFor(() => {
    expect(mockCheckFavorites).toHaveBeenCalledTimes(1)
  })

  expect(screen.queryByText('取消收藏')).toBeNull()
  expect(screen.getAllByText('添加收藏').length).toBeGreaterThan(0)
  })

  it('does not reuse cached favorite status when the same user home directory changes', async () => {
  const user = userEvent.setup({ writeToClipboard: false })
  mockFilesStoreState.viewMode = 'grid'
  mockFilesStoreState.currentPath = '/member-next'
  mockLocationPathname = '/files/member-next'
  mockUser.role = 'user'
  mockUser.homeDir = '/member-next'
  mockListFiles.mockResolvedValue({
    files: [
      { name: 'photo.jpg', path: '/member-next/photo.jpg', isDir: false, size: 1024000, modTime: '2024-01-02T00:00:00Z' },
    ],
    path: '/member-next',
  })
  mockCheckFavorites.mockImplementation(() => new Promise(() => {}))

  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        gcTime: 0,
        staleTime: 0,
      },
    },
  })
  queryClient.setQueryData(['favorites-check', 'u1', ['/member-next/photo.jpg']], {
    '/member-next/photo.jpg': true,
  })

  render(
    <QueryClientProvider client={queryClient}>
      <FilesPage />
    </QueryClientProvider>
  )

  await waitFor(() => {
    expect(screen.getByText('photo.jpg')).toBeTruthy()
  })

  await user.click(screen.getByLabelText('photo.jpg 操作菜单'))

  await waitFor(() => {
    expect(mockCheckFavorites).toHaveBeenCalledTimes(1)
  })

  expect(screen.queryByText('取消收藏')).toBeNull()
  expect(screen.getAllByText('添加收藏').length).toBeGreaterThan(0)
  })

  it('does not reuse cached file listings from another user session', async () => {
    mockFilesStoreState.currentPath = '/member'
    mockLocationPathname = '/files/member'
    mockUser.id = 'u2'
    mockUser.username = 'member'
    mockUser.role = 'user'
    mockUser.homeDir = '/member'
    mockListFiles.mockImplementation(() => pendingFilesRefetch())

    const queryClient = new QueryClient({
      defaultOptions: {
        queries: {
          retry: false,
          gcTime: 0,
          staleTime: 0,
        },
      },
    })
    queryClient.setQueryData(['files', '/member'], {
      files: [
        { name: 'admin-secret.txt', path: '/member/admin-secret.txt', isDir: false, size: 10, modTime: '2024-01-01T00:00:00Z' },
      ],
      path: '/member',
    })

    render(
      <QueryClientProvider client={queryClient}>
        <FilesPage />
      </QueryClientProvider>
    )

    await waitFor(() => {
      expect(mockListFiles).toHaveBeenCalledWith('/member')
    })

    expect(screen.queryByText('admin-secret.txt')).toBeNull()
  })

  it('treats remove-favorite not found as already removed and syncs the status', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockFilesStoreState.viewMode = 'grid'
    mockCheckFavorites
      .mockResolvedValueOnce({
        '/documents': false,
        '/photo.jpg': true,
        '/video.mp4': false,
      })
      .mockResolvedValueOnce({
        '/documents': false,
        '/photo.jpg': false,
        '/video.mp4': false,
      })
    mockToggleFavorite.mockRejectedValueOnce(Object.assign(new Error('favorite not found'), {
      status: 404,
      code: 'FAVORITE_NOT_FOUND',
    }))

    render(<FilesPage />)

    await waitFor(() => {
      expect(screen.getByText('photo.jpg')).toBeTruthy()
    })

    await user.click(screen.getByLabelText('photo.jpg 操作菜单'))

    await waitFor(() => {
      expect(screen.getAllByText('取消收藏').length).toBeGreaterThan(0)
    })

    await user.click(screen.getAllByText('取消收藏')[0])

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '收藏已移除',
        description: '该文件已不在收藏夹中，状态已同步。',
        color: 'warning',
      })
    })

    await waitFor(() => {
      expect(mockCheckFavorites).toHaveBeenCalledTimes(2)
    })
  })
  })
})
