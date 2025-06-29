import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { FilesPage } from './Files'
import * as HeroUI from '@heroui/react'

const mockAddToast = vi.fn()
const useCanWriteMock = vi.fn(() => true)
const mockUser = { id: 'u1', username: 'admin', role: 'admin' as const, email: 'admin@local', homeDir: '/' }

vi.mock('@tanstack/react-virtual', () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => count * 72,
    getVirtualItems: () =>
      Array.from({ length: count }, (_, index) => ({
        index,
        size: 72,
        start: index * 72,
        key: index,
      })),
  }),
}))

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
  MAX_UPLOAD_FILE_SIZE_BYTES: 50 * 1024 * 1024,
  MAX_UPLOAD_FILE_SIZE_LABEL: '50 MB',
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

vi.mock('@/api/share', () => ({
  ShareError: class ShareError extends Error {
    status: number
    code?: string
    constructor(message: string, status: number, code?: string) {
      super(message)
      this.status = status
      this.code = code
    }
  },
  listShares: vi.fn(),
}))

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

import { listFiles, downloadFile } from '@/api/files'
import { checkFavorites, toggleFavorite } from '@/api/favorites'
import { listShares } from '@/api/share'

const mockListFiles = vi.mocked(listFiles)
const mockDownloadFile = vi.mocked(downloadFile)
const mockCheckFavorites = vi.mocked(checkFavorites)
const mockToggleFavorite = vi.mocked(toggleFavorite)
const mockListShares = vi.mocked(listShares)

async function getFileActionArea(name: string) {
  const trigger = await screen.findByLabelText(`${name} 操作菜单`)
  const area = trigger.closest('.group')
  expect(area).toBeTruthy()
  return area as HTMLElement
}

function expectToggleFavoriteCalledWithAbortSignal(path: string, isFavorited: boolean) {
  const call = mockToggleFavorite.mock.calls.find((args) => {
    const [calledPath, calledIsFavorited, options] = args as [string, boolean, { signal?: AbortSignal } | undefined]
    return calledPath === path && calledIsFavorited === isFavorited && options?.signal instanceof AbortSignal
  })
  expect(call).toBeTruthy()
  const [, , options] = call as unknown as [string, boolean, { signal?: AbortSignal }]
  expect(Object.keys(options)).toEqual(['signal'])
}

describe('FilesPage list row actions', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useCanWriteMock.mockReturnValue(true)
    mockFilesStoreState.currentPath = '/'
    mockFilesStoreState.selectedFiles = new Set<string>()
    mockFilesStoreState.viewMode = 'list'
    mockFilesStoreState.sortBy = 'name'
    mockFilesStoreState.sortOrder = 'asc'
    mockClipboardState.paths = []
    mockClipboardState.operation = null
    mockClipboardState.sourcePath = null
    mockClipboardState.hasPaths.mockReturnValue(false)
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
    mockListShares.mockResolvedValue([])
    mockDownloadFile.mockResolvedValue(undefined)
    mockCheckFavorites.mockResolvedValue({
      '/documents': false,
      '/photo.jpg': false,
      '/video.mp4': false,
    })
    mockToggleFavorite.mockResolvedValue(true)
    mockListFiles.mockResolvedValue({
      files: [
        { name: 'documents', path: '/documents', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
        { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024000, modTime: '2024-01-02T00:00:00Z' },
        { name: 'video.mp4', path: '/video.mp4', isDir: false, size: 10240000, modTime: '2024-01-03T00:00:00Z' },
      ],
      path: '/',
    })
  })

  it('runs download and copy actions from a virtualized list row menu', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })

    render(<FilesPage />)

    const actionArea = await getFileActionArea('photo.jpg')
    await user.click(within(actionArea).getByText('下载'))

    await waitFor(() => {
      expect(mockDownloadFile).toHaveBeenCalledWith('/photo.jpg', expect.objectContaining({
        filename: 'photo.jpg',
        signal: expect.any(AbortSignal),
      }))
    })

    await user.click(within(actionArea).getByText('复制路径'))

    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith('/photo.jpg')
      expect(mockAddToast).toHaveBeenCalledWith({ title: '路径已复制', color: 'success' })
    })
  })

  it('shows a danger toast when downloading from a virtualized list row menu fails', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockDownloadFile.mockRejectedValueOnce(new Error('download failed'))

    render(<FilesPage />)

    const actionArea = await getFileActionArea('photo.jpg')
    await user.click(within(actionArea).getByText('下载'))

    await waitFor(() => {
      expect(mockDownloadFile).toHaveBeenCalledWith('/photo.jpg', expect.objectContaining({
        filename: 'photo.jpg',
        signal: expect.any(AbortSignal),
      }))
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
        title: '下载失败',
        color: 'danger',
      }))
    })
  })

  it('shows a danger toast when copying from a virtualized list row menu fails', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) },
    })

    render(<FilesPage />)

    const actionArea = await getFileActionArea('photo.jpg')
    await user.click(within(actionArea).getByText('复制路径'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({ title: '复制失败', color: 'danger' })
    })
  })

  it('keeps virtualized list row pointer events scoped to the intended control', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<FilesPage />)

    const row = (await screen.findByText('photo.jpg')).closest('.group')
    expect(row).toBeTruthy()

    mockFilesStoreState.setSelection.mockClear()
    mockFilesStoreState.toggleFileSelection.mockClear()

    await user.click(row as HTMLElement)
    fireEvent.contextMenu(row as HTMLElement)

    expect(mockFilesStoreState.setSelection).toHaveBeenCalledWith(['/photo.jpg'])

    const checkbox = screen.getByRole('checkbox', { name: '选择 photo.jpg' })
    const checkboxShell = checkbox.parentElement
    expect(checkboxShell).toBeTruthy()

    fireEvent.click(checkboxShell as HTMLElement)
    fireEvent.doubleClick(checkboxShell as HTMLElement)
    fireEvent.contextMenu(checkboxShell as HTMLElement)

    await user.click(checkbox)
    expect(mockFilesStoreState.toggleFileSelection).toHaveBeenCalledWith('/photo.jpg')

    const actionButton = screen.getByLabelText('photo.jpg 操作菜单')
    const actionShell = actionButton.closest('.flex.items-center.justify-center')
    expect(actionShell).toBeTruthy()

    fireEvent.doubleClick(actionShell as HTMLElement)
    fireEvent.contextMenu(actionShell as HTMLElement)

    await user.dblClick(row as HTMLElement)
    expect(screen.getAllByText('photo.jpg').length).toBeGreaterThan(0)
  })

  it('runs write actions from a virtualized list row menu', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    render(<FilesPage />)

    const actionArea = await getFileActionArea('photo.jpg')

    await user.click(within(actionArea).getByText('重命名'))
    expect(await screen.findByPlaceholderText('请输入新名称')).toBeTruthy()
    await user.click(screen.getByRole('button', { name: '取消' }))

    await user.click(within(actionArea).getByText('删除'))
    expect(await screen.findByRole('heading', { name: '确认删除' })).toBeTruthy()
    await user.click(screen.getByRole('button', { name: '取消' }))

    await user.click(within(actionArea).getByText('添加收藏'))
    await waitFor(() => {
      expectToggleFavoriteCalledWithAbortSignal('/photo.jpg', false)
    })

    await user.click(within(actionArea).getByText('查看版本历史'))
  })

  it('clears virtualized list selection from an empty list-space click', async () => {
    mockFilesStoreState.selectedFiles = new Set(['/photo.jpg'])

    render(<FilesPage />)

    const listContainer = (await screen.findAllByText('photo.jpg'))[0].closest('.custom-scrollbar')
    expect(listContainer).toBeTruthy()

    vi.useFakeTimers()
    fireEvent.click(listContainer as HTMLElement)
    expect(mockFilesStoreState.clearSelection).toHaveBeenCalled()

    act(() => {
      vi.advanceTimersByTime(1500)
    })
    vi.useRealTimers()
  })
})
