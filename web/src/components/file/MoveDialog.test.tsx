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

type MoveCopyOptions = { signal?: AbortSignal }

function expectMoveFileCalledWithAbortSignal(callIndex: number, fromPath: string, toPath: string): AbortSignal {
  const call = mockMoveFile.mock.calls[callIndex]
  expect(call).toBeDefined()
  expect(call[0]).toBe(fromPath)
  expect(call[1]).toBe(toPath)

  const signal = (call[2] as MoveCopyOptions | undefined)?.signal
  expect(signal).toBeDefined()
  expect(signal?.aborted).toBe(false)
  return signal as AbortSignal
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

  it('closes from the cancel button while idle', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()

    renderDialog({ onClose })

    await user.click(screen.getByRole('button', { name: '取消' }))

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('normalizes the current directory before blocking same-folder submissions', async () => {
    const user = userEvent.setup({ writeToClipboard: false })

    renderDialog({ currentPath: '/source/' })

    await user.click(screen.getByText('点击选择目标文件夹'))
    await waitFor(() => {
      expect(screen.getByText('/source')).toBeTruthy()
    })
    await user.click(screen.getByRole('button', { name: '选择此目录' }))

    expect(screen.getByText('目标目录与当前目录相同')).toBeTruthy()
    const confirmButton = screen.getByRole('button', { name: '移动' })
    expect(confirmButton).toBeDisabled()

    await user.click(confirmButton)

    expect(mockMoveFile).not.toHaveBeenCalled()
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

  it('drops stale missing files from retry state during partial move failure', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()
    mockMoveFile
      .mockRejectedValueOnce(new ApiError('file not found', 404, 'Not Found', 'FILE_NOT_FOUND'))
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
        title: '成功移动 2 个项目，但存在警告',
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
        title: '成功复制 2 个项目，但存在警告',
        color: 'warning',
      })
    })
  })

  it('closes with a warning when all copy sources are already missing', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()
    mockCopyFile.mockRejectedValue(new ApiError('file not found', 404, 'Not Found', 'FILE_NOT_FOUND'))

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
        title: '文件或文件夹已不存在，已同步更新',
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

  it('stays open with localized warning after full copy target conflict', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()
    mockCopyFile.mockRejectedValue(new ApiError('resource already exists', 409, 'Conflict'))

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
      title: '同名项目已存在',
      description: '当前目录中已存在同名文件或文件夹，请使用其他名称。',
      color: 'warning',
    })
  })

  it('stays open with localized warning after full copy target quota failure', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()
    mockCopyFile.mockRejectedValue(new ApiError('directory quota exceeded', 507, 'Insufficient Storage', 'QUOTA_EXCEEDED'))

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
      title: '容量配额不足',
      description: '目标目录的容量配额不足，请清理空间或调整目录配额后重试。',
      color: 'warning',
    })
  })

  it('stays open with localized warning after full move target parent conflict', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()
    mockMoveFile.mockRejectedValue(new ApiError('parent path is not a directory', 409, 'Conflict'))

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
    expect(screen.getByText('a.txt')).toBeTruthy()
    expect(screen.getByText('b.txt')).toBeTruthy()
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '目标位置不可用',
      description: '当前目录状态已变更，请刷新列表后重试。',
      color: 'warning',
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
      description: '文件系统当前不可用，请检查设备状态或稍后重试。',
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
      expectMoveFileCalledWithAbortSignal(0, '/source/a.txt', '/a.txt')
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
      expectMoveFileCalledWithAbortSignal(0, '/source/a.txt', '/a.txt')
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

  it('aborts a pending move request when the dialog unmounts', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()
    const firstMove = createDeferred<typeof successActionResult>()
    mockMoveFile.mockImplementationOnce(() => firstMove.promise)

    const view = renderDialog({ onClose })

    await user.click(screen.getByText('点击选择目标文件夹'))
    await waitFor(() => {
      expect(screen.getAllByText('根目录').length).toBeGreaterThan(0)
    })
    await user.click(screen.getAllByText('根目录')[0])
    await user.click(screen.getByRole('button', { name: '选择此目录' }))
    await user.click(screen.getByRole('button', { name: '移动' }))

    let moveSignal: AbortSignal | undefined
    await waitFor(() => {
      moveSignal = expectMoveFileCalledWithAbortSignal(0, '/source/a.txt', '/a.txt')
    })

    view.rerender(
      <QueryClientProvider client={view.queryClient}>
        <MoveDialog isOpen={false} onClose={onClose} files={[]} currentPath="/source" mode="move" />
      </QueryClientProvider>,
    )

    expect(moveSignal?.aborted).toBe(true)

    await act(async () => {
      firstMove.resolve(successActionResult)
      await firstMove.promise
    })

    expect(mockMoveFile).toHaveBeenCalledTimes(1)
    expect(onClose).not.toHaveBeenCalled()
    expect(mockAddToast).not.toHaveBeenCalled()
  })
})
