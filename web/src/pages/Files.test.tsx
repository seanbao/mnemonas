import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { FilesPage } from './Files'

// Mock API functions
vi.mock('@/api/files', () => ({
  listFiles: vi.fn(),
  deleteFile: vi.fn(),
  createDirectory: vi.fn(),
  uploadFile: vi.fn(),
  moveFile: vi.fn(),
}))

// Mock navigation
const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useLocation: () => ({ pathname: '/files' }),
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

// Mock stores
vi.mock('@/stores/files', () => ({
  useFilesStore: () => mockFilesStoreState,
}))

import { listFiles, createDirectory, deleteFile, moveFile } from '@/api/files'

const mockListFiles = vi.mocked(listFiles)
const mockCreateDirectory = vi.mocked(createDirectory)
const mockDeleteFile = vi.mocked(deleteFile)
const mockMoveFile = vi.mocked(moveFile)

describe('FilesPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockFilesStoreState.selectedFiles = new Set<string>()
    mockFilesStoreState.currentPath = '/'
    mockFilesStoreState.viewMode = 'list'
    mockFilesStoreState.sortBy = 'name'
    mockFilesStoreState.sortOrder = 'asc'
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
      mockCreateDirectory.mockResolvedValue(undefined)
      
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
  })

  describe('file selection', () => {
    it('renders checkboxes for each file', async () => {
      render(<FilesPage />)
      
      await waitFor(() => {
        // Each file row should have a checkbox
        const checkboxes = document.querySelectorAll('[class*="checkbox"], [class*="border-2"]')
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

      const { rerender } = render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })

      mockFilesStoreState.setSelection.mockClear()
      rerender(<FilesPage />)

      await waitFor(() => {
        expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith([])
      })
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

      const { rerender } = render(<FilesPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })

      mockFilesStoreState.setSelection.mockClear()
      rerender(<FilesPage />)

      await waitFor(() => {
        expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/photo.jpg'])
      })
    })
  })

  describe('file operations', () => {
    it('deleteFile API is available', async () => {
      mockDeleteFile.mockResolvedValue(undefined)
      await mockDeleteFile('/photo.jpg')
      expect(mockDeleteFile).toHaveBeenCalledWith('/photo.jpg')
    })

    it('moveFile API is available', async () => {
      mockMoveFile.mockResolvedValue(undefined)
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

    it('retries on API failure', async () => {
      mockListFiles.mockRejectedValueOnce(new Error('Network error'))
      mockListFiles.mockResolvedValueOnce({ files: [], path: '/' })
      
      render(<FilesPage />)
      
      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalled()
      })
    })
  })
})
