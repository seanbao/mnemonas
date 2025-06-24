import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MoveDialog } from './MoveDialog'

const mockAddToast = vi.fn()

vi.mock('@heroui/react', async () => {
  const actual = await vi.importActual<typeof import('@heroui/react')>('@heroui/react')
  return {
    ...actual,
    addToast: (...args: unknown[]) => mockAddToast(...args),
  }
})

vi.mock('@/api/files', () => ({
  ApiError: class ApiError extends Error {
    status: number
    statusText: string
    code?: string

    constructor(message: string, status: number, statusText: string, code?: string) {
      super(message)
      this.status = status
      this.statusText = statusText
      this.code = code
    }

    get isUnavailable() {
      return this.status === 503 || this.code === 'SERVICE_UNAVAILABLE'
    }
  },
  moveFile: vi.fn(),
  copyFile: vi.fn(),
  listFiles: vi.fn(),
  createDirectory: vi.fn(),
}))

import { ApiError, moveFile, copyFile, listFiles, createDirectory } from '@/api/files'

const mockMoveFile = vi.mocked(moveFile)
const mockCopyFile = vi.mocked(copyFile)
const mockListFiles = vi.mocked(listFiles)
const mockCreateDirectory = vi.mocked(createDirectory)
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

function renderDialog(props?: Partial<React.ComponentProps<typeof MoveDialog>>) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })

  const defaultProps: React.ComponentProps<typeof MoveDialog> = {
    isOpen: true,
    onClose: vi.fn(),
    files: [
      { path: '/source/a.txt', name: 'a.txt', isDir: false },
      { path: '/source/b.txt', name: 'b.txt', isDir: false },
    ],
    currentPath: '/source',
    mode: 'move',
  }

  return {
    queryClient,
    ...render(
    <QueryClientProvider client={queryClient}>
      <MoveDialog {...defaultProps} {...props} />
    </QueryClientProvider>
    ),
  }
}

describe('MoveDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListFiles.mockResolvedValue({ files: [], path: '/' })
    mockCreateDirectory.mockResolvedValue(successActionResult)
  })

  it('keeps only failed files visible after partial move failure', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()
    mockMoveFile
      .mockResolvedValueOnce(successActionResult)
      .mockRejectedValueOnce(new Error('move failed'))

    renderDialog({ onClose })

    await user.click(screen.getByText('点击选择目标文件夹'))
    await waitFor(() => {
      expect(screen.getAllByText('根目录').length).toBeGreaterThan(0)
    })
    await user.click(screen.getAllByText('根目录')[0])
    await user.click(screen.getByRole('button', { name: '选择此目录' }))

    await user.click(screen.getByRole('button', { name: '移动' }))

    await waitFor(() => {
      expect(mockMoveFile).toHaveBeenCalledTimes(2)
    })

    expect(onClose).not.toHaveBeenCalled()
    expect(screen.queryByText('a.txt')).toBeNull()
    expect(screen.getAllByText('b.txt').length).toBeGreaterThan(0)
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '批量移动部分完成',
      description: '成功 1 个，失败 1 个',
      color: 'warning',
    })
  })

  it('shows warning toast when full move succeeds with warnings', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()
    mockMoveFile
      .mockResolvedValueOnce(warningActionResult('resource moved with persistence warning'))
      .mockResolvedValueOnce(successActionResult)

    renderDialog({ onClose })

    await user.click(screen.getByText('点击选择目标文件夹'))
    await waitFor(() => {
      expect(screen.getAllByText('根目录').length).toBeGreaterThan(0)
    })
    await user.click(screen.getAllByText('根目录')[0])
    await user.click(screen.getByRole('button', { name: '选择此目录' }))
    await user.click(screen.getByRole('button', { name: '移动' }))

    await waitFor(() => {
      expect(onClose).toHaveBeenCalledTimes(1)
      expect(mockAddToast).toHaveBeenCalledWith({
        title: 'resource moved with persistence warning',
        color: 'warning',
      })
    })
  })

  it('shows warning toast when full copy succeeds with warnings', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()
    mockCopyFile
      .mockResolvedValueOnce(warningActionResult('resource copied with persistence warning'))
      .mockResolvedValueOnce(successActionResult)

    renderDialog({ onClose, mode: 'copy' })

    await user.click(screen.getByText('点击选择目标文件夹'))
    await waitFor(() => {
      expect(screen.getAllByText('根目录').length).toBeGreaterThan(0)
    })
    await user.click(screen.getAllByText('根目录')[0])
    await user.click(screen.getByRole('button', { name: '选择此目录' }))

    await user.click(screen.getByRole('button', { name: '复制' }))

    await waitFor(() => {
      expect(onClose).toHaveBeenCalledTimes(1)
      expect(mockAddToast).toHaveBeenCalledWith({
        title: 'resource copied with persistence warning',
        color: 'warning',
      })
    })
  })

  it('stays open after full copy failure for retry', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()
    mockCopyFile.mockRejectedValue(new Error('copy failed'))

    renderDialog({ onClose, mode: 'copy' })

    await user.click(screen.getByText('点击选择目标文件夹'))
    await waitFor(() => {
      expect(screen.getAllByText('根目录').length).toBeGreaterThan(0)
    })
    await user.click(screen.getAllByText('根目录')[0])
    await user.click(screen.getByRole('button', { name: '选择此目录' }))

    await user.click(screen.getByRole('button', { name: '复制' }))

    await waitFor(() => {
      expect(mockCopyFile).toHaveBeenCalledTimes(2)
    })

    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByText('a.txt')).toBeTruthy()
    expect(screen.getByText('b.txt')).toBeTruthy()
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '批量复制失败',
      description: '共 2 个项目失败',
      color: 'danger',
    })
  })

  it('shows unavailable feedback when full move fails due to unavailable filesystem', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()
    mockMoveFile.mockRejectedValue(new ApiError('filesystem not initialized', 503, 'Service Unavailable', 'SERVICE_UNAVAILABLE'))

    renderDialog({ onClose })

    await user.click(screen.getByText('点击选择目标文件夹'))
    await waitFor(() => {
      expect(screen.getAllByText('根目录').length).toBeGreaterThan(0)
    })
    await user.click(screen.getAllByText('根目录')[0])
    await user.click(screen.getByRole('button', { name: '选择此目录' }))

    await user.click(screen.getByRole('button', { name: '移动' }))

    await waitFor(() => {
      expect(mockMoveFile).toHaveBeenCalledTimes(2)
    })

    expect(onClose).not.toHaveBeenCalled()
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '批量移动暂不可用',
      description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
      color: 'warning',
    })
  })

  it('syncs the visible file list when reopened for a new selection', async () => {
    const onClose = vi.fn()
    const firstFiles = [{ path: '/source/a.txt', name: 'a.txt', isDir: false }]
    const secondFiles = [{ path: '/source/c.txt', name: 'c.txt', isDir: false }]

    const view = renderDialog({ onClose, files: firstFiles })

    expect(screen.getAllByText('a.txt').length).toBeGreaterThan(0)

    view.rerender(
      <QueryClientProvider client={view.queryClient}>
        <MoveDialog isOpen={false} onClose={onClose} files={firstFiles} currentPath="/source" mode="move" />
      </QueryClientProvider>,
    )

    view.rerender(
      <QueryClientProvider client={view.queryClient}>
        <MoveDialog isOpen={true} onClose={onClose} files={secondFiles} currentPath="/source" mode="move" />
      </QueryClientProvider>,
    )

    await waitFor(() => {
      expect(screen.queryAllByText('a.txt')).toHaveLength(0)
      expect(screen.getAllByText('c.txt').length).toBeGreaterThan(0)
    })
  })

  it('keeps the dialog open while a pending move request is in flight', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()
    const firstMove = createDeferred<typeof successActionResult>()
    const firstFiles = [{ path: '/source/a.txt', name: 'a.txt', isDir: false }]
    mockMoveFile.mockImplementationOnce(() => firstMove.promise)

    renderDialog({ onClose, files: firstFiles })

    await user.click(screen.getByText('点击选择目标文件夹'))
    await waitFor(() => {
      expect(screen.getAllByText('根目录').length).toBeGreaterThan(0)
    })
    await user.click(screen.getAllByText('根目录')[0])
    await user.click(screen.getByRole('button', { name: '选择此目录' }))
    await user.click(screen.getByRole('button', { name: '移动' }))

    await waitFor(() => {
      expect(mockMoveFile).toHaveBeenCalledWith('/source/a.txt', '/a.txt')
    })

    await user.click(screen.getByRole('button', { name: '取消' }))
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getAllByText('a.txt').length).toBeGreaterThan(0)

    await act(async () => {
      firstMove.resolve(successActionResult)
      await firstMove.promise
    })

    await waitFor(() => {
      expect(onClose).toHaveBeenCalledTimes(1)
    })
  })

  it('keeps the dialog open when a pending move request later fails', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()
    const firstMove = createDeferred<typeof successActionResult>()
    const firstFiles = [{ path: '/source/a.txt', name: 'a.txt', isDir: false }]
    mockMoveFile.mockImplementationOnce(() => firstMove.promise)

    renderDialog({ onClose, files: firstFiles })

    await user.click(screen.getByText('点击选择目标文件夹'))
    await waitFor(() => {
      expect(screen.getAllByText('根目录').length).toBeGreaterThan(0)
    })
    await user.click(screen.getAllByText('根目录')[0])
    await user.click(screen.getByRole('button', { name: '选择此目录' }))
    await user.click(screen.getByRole('button', { name: '移动' }))

    await waitFor(() => {
      expect(mockMoveFile).toHaveBeenCalledWith('/source/a.txt', '/a.txt')
    })

    await user.click(screen.getByRole('button', { name: '取消' }))
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getAllByText('a.txt').length).toBeGreaterThan(0)

    await act(async () => {
      firstMove.reject(new Error('move failed'))
    })

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '批量移动失败',
        description: '共 1 个项目失败',
        color: 'danger',
      })
    })

    expect(screen.getAllByText('a.txt').length).toBeGreaterThan(0)
    expect(onClose).not.toHaveBeenCalled()
  })
})