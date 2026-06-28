import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@/test/utils'
import React from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { FilesPage } from './Files'

const mockAddToast = vi.fn()
const useCanWriteMock = vi.fn(() => true)

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

vi.mock('@heroui/react', async () => {
  const react = await vi.importActual<typeof import('react')>('react')
  return {
    HeroUIProvider: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    Button: ({ children, onPress, isDisabled, isLoading, startContent }: { children: React.ReactNode; onPress?: () => void; isDisabled?: boolean; isLoading?: boolean; startContent?: React.ReactNode }) => (
      <button disabled={isDisabled || isLoading} onClick={onPress}>{startContent}{children}</button>
    ),
    Modal: ({ children, isOpen }: { children: React.ReactNode; isOpen: boolean }) =>
      isOpen ? <div>{children}</div> : null,
    ModalContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    ModalHeader: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    ModalBody: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    ModalFooter: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    Input: (props: React.ComponentProps<'input'> & { onValueChange?: (value: string) => void }) => (
      <input {...props} onChange={(e) => props.onValueChange?.(e.target.value)} />
    ),
    Dropdown: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    DropdownTrigger: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    DropdownItem: ({ children, onPress, isDisabled }: { children: React.ReactNode; onPress?: () => void; isDisabled?: boolean }) => (
      <button disabled={isDisabled} onClick={() => !isDisabled && onPress?.()}>{children}</button>
    ),
    DropdownSection: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    Progress: () => <div />,
    useDisclosure: () => {
      const [isOpen, setIsOpen] = react.useState(false)
      return {
        isOpen,
        onOpen: () => setIsOpen(true),
        onClose: () => setIsOpen(false),
      }
    },
    addToast: (...args: unknown[]) => mockAddToast(...args),
  }
})

vi.mock('@/components/share', () => ({
  ShareDialog: () => null,
}))

vi.mock('@/components/preview', () => ({
  PreviewModal: () => null,
}))

vi.mock('@/components/file', () => ({
  MoveDialog: () => null,
}))

vi.mock('@/components/ui/ContextMenu', () => ({
  ContextMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  ContextMenuSection: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  ContextMenuItem: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}))

vi.mock('@/hooks', () => ({
  useContextMenu: () => ({
    state: { isOpen: false, position: { x: 0, y: 0 } },
    show: vi.fn(),
    hide: vi.fn(),
  }),
  useKeyboardShortcuts: () => undefined,
}))

vi.mock('@/api/files', () => ({
  MAX_DELETE_INTENT_TARGETS: 1000,
  listFiles: vi.fn(),
  createFileDeleteIntent: vi.fn(),
  deleteFile: vi.fn(),
  createDirectory: vi.fn(),
  uploadFile: vi.fn(),
  moveFile: vi.fn(),
  copyFile: vi.fn(),
  MAX_UPLOAD_FILE_SIZE_BYTES: 10 * 1024 * 1024 * 1024,
  MAX_UPLOAD_FILE_SIZE_LABEL: '10 GB',
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
}))

vi.mock('@/api/favorites', () => ({
  checkFavorites: vi.fn().mockResolvedValue({}),
  toggleFavorite: vi.fn(),
}))

vi.mock('@/api/share', async () => {
  const actual = await vi.importActual<typeof import('@/api/share')>('@/api/share')
  return {
    ...actual,
    listShares: vi.fn().mockResolvedValue([]),
  }
})
import { listShares } from '@/api/share'

const mockListShares = vi.mocked(listShares)

vi.mock('@/stores/files', () => ({
  useFilesStore: () => ({
    currentPath: '/',
    selectedFiles: new Set<string>(),
    viewMode: 'list' as const,
    sortBy: 'name' as const,
    sortOrder: 'asc' as const,
    setCurrentPath: vi.fn(),
    toggleFileSelection: vi.fn(),
    selectAll: vi.fn(),
    clearSelection: vi.fn(),
    setViewMode: vi.fn(),
    setSortBy: vi.fn(),
    toggleSortOrder: vi.fn(),
  }),
}))

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useCanWrite: () => useCanWriteMock(),
  }
})

import { ApiError, listFiles, uploadFile, createDirectory, MAX_UPLOAD_FILE_SIZE_BYTES } from '@/api/files'

const mockListFiles = vi.mocked(listFiles)
const mockUploadFile = vi.mocked(uploadFile)
const mockCreateDirectory = vi.mocked(createDirectory)
const successActionResult = { warning: false, message: undefined } as const

function expectUploadFileCalledWithSignal(path: string, file: File) {
  const call = mockUploadFile.mock.calls.find(([calledPath, calledFile]) => calledPath === path && calledFile === file)
  expect(call).toBeTruthy()
  expect(call?.[2]).toEqual(expect.any(Function))
  expect((call?.[3] as { signal?: AbortSignal } | undefined)?.signal).toBeInstanceOf(AbortSignal)
}

function expectCreateDirectoryCalledWithSignal(path: string) {
  const call = mockCreateDirectory.mock.calls.find(([calledPath]) => calledPath === path)
  expect(call).toBeTruthy()
  expect((call?.[1] as { signal?: AbortSignal } | undefined)?.signal).toBeInstanceOf(AbortSignal)
}

function warningActionResult(message: string) {
  return { warning: true, message } as const
}

describe('FilesPage upload queue', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    useCanWriteMock.mockReturnValue(true)
    mockListShares.mockResolvedValue([])
    mockListFiles.mockResolvedValue({
      files: [],
      path: '/',
      deleteMode: 'trash',
      deletePolicyToken: '1'.repeat(64),
      trashRetentionDays: 30,
      trashAutoCleanupEnabled: true,
    })
    mockUploadFile.mockResolvedValue(successActionResult)
    mockCreateDirectory.mockResolvedValue(successActionResult)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  const flushUi = async () => {
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })
    act(() => {
      vi.advanceTimersByTime(0)
    })
  }

  const createDeferred = <T,>() => {
    let resolve!: (value: T | PromiseLike<T>) => void
    let reject!: (reason?: unknown) => void
    const promise = new Promise<T>((res, rej) => {
      resolve = res
      reject = rej
    })
    return { promise, resolve, reject }
  }

  const getUploadFileInput = () => screen.getByLabelText('选择上传文件') as HTMLInputElement

  it('clears successful uploads after timeout', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    const fileInput = getUploadFileInput()
    const file = new File(['data'], 'test.txt', { type: 'text/plain' })

    fireEvent.change(fileInput, { target: { files: [file] } })

    await flushUi()

    expect(screen.getByText('上传完成')).toBeTruthy()

    act(() => {
      vi.advanceTimersByTime(3000)
    })

    expect(screen.queryByText('上传完成')).toBeTruthy()

    act(() => {
      vi.advanceTimersByTime(12000)
    })

    expect(screen.queryByText('上传完成')).toBeNull()
  })

  it('clears the hidden upload input after a selection so the same file can be picked again', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    const fileInput = getUploadFileInput()

    const file = new File(['data'], 'test.txt', { type: 'text/plain' })
    Object.defineProperty(fileInput, 'value', {
      configurable: true,
      writable: true,
      value: 'C:\\fakepath\\test.txt',
    })

    fireEvent.change(fileInput, { target: { files: [file] } })

    await flushUi()

    expect((fileInput).value).toBe('')
    expectUploadFileCalledWithSignal('/', file)
  })

  it('uploads dropped files after showing and clearing the drag overlay', async () => {
    mockUploadFile.mockImplementationOnce(async (_path, _file, onProgress) => {
      onProgress(42)
      return successActionResult
    })

    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    const dropZone = screen.getByRole('region', { name: '文件上传区域' })

    const file = new File(['dropped'], 'dropped.txt', { type: 'text/plain' })
    const dataTransfer = {
      types: ['Files'],
      files: [file],
    }

    fireEvent.dragEnter(dropZone, { dataTransfer })
    expect(screen.getByText('释放以上传')).toBeTruthy()

    fireEvent.dragLeave(dropZone, { dataTransfer })
    expect(screen.queryByText('释放以上传')).toBeNull()

    fireEvent.dragEnter(dropZone, { dataTransfer })
    fireEvent.dragOver(dropZone, { dataTransfer })
    fireEvent.drop(dropZone, { dataTransfer })

    await flushUi()

    expectUploadFileCalledWithSignal('/', file)
    expect(screen.queryByText('释放以上传')).toBeNull()
    expect(screen.getByText('上传完成')).toBeTruthy()
  })

  it('ignores drag and drop upload attempts for read-only users', async () => {
    useCanWriteMock.mockReturnValue(false)

    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    const dropZone = screen.getByRole('region', { name: '文件上传区域' })

    const file = new File(['readonly'], 'readonly.txt', { type: 'text/plain' })
    const dataTransfer = {
      types: ['Files'],
      files: [file],
    }

    fireEvent.dragEnter(dropZone, { dataTransfer })
    fireEvent.dragOver(dropZone, { dataTransfer })
    fireEvent.drop(dropZone, { dataTransfer })

    await flushUi()

    expect(screen.queryByText('释放以上传')).toBeNull()
    expect(mockUploadFile).not.toHaveBeenCalled()
  })

  it('hides, reopens, and clears completed upload history', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    const fileInput = getUploadFileInput()
    const file = new File(['data'], 'history.txt', { type: 'text/plain' })

    fireEvent.change(fileInput, { target: { files: [file] } })
    await flushUi()

    expect(screen.getByText('上传完成')).toBeTruthy()

    fireEvent.click(screen.getByLabelText('隐藏上传记录'))
    expect(screen.queryByText('上传完成')).toBeNull()

    fireEvent.click(screen.getByLabelText('上传记录'))
    expect(screen.getByText('上传完成')).toBeTruthy()

    fireEvent.click(screen.getByLabelText('清空上传记录'))
    expect(screen.queryByText('上传完成')).toBeNull()
    expect(screen.queryByLabelText('上传记录')).toBeNull()
  })

  it('clears only the latest upload timer', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    const fileInput = getUploadFileInput()

    const firstFile = new File(['data1'], 'first.txt', { type: 'text/plain' })
    const secondFile = new File(['data2'], 'second.txt', { type: 'text/plain' })

    fireEvent.change(fileInput, { target: { files: [firstFile] } })
    await flushUi()

    expect(screen.getByText('上传完成')).toBeTruthy()

    act(() => {
      vi.advanceTimersByTime(1000)
    })

    fireEvent.change(fileInput, { target: { files: [secondFile] } })
    await flushUi()

    expect(screen.getByText('上传完成')).toBeTruthy()

    act(() => {
      vi.advanceTimersByTime(2000)
    })

    expect(screen.queryByText('上传完成')).toBeTruthy()

    act(() => {
      vi.advanceTimersByTime(13000)
    })

    expect(screen.queryByText('上传完成')).toBeNull()
  })

  it('ignores stale upload completion from an older session', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    const firstUpload = createDeferred<typeof successActionResult>()
    const secondUpload = createDeferred<typeof successActionResult>()
    mockUploadFile
      .mockImplementationOnce(() => firstUpload.promise)
      .mockImplementationOnce(() => secondUpload.promise)

    const fileInput = getUploadFileInput()

    const firstFile = new File(['data1'], 'first.txt', { type: 'text/plain' })
    const secondFile = new File(['data2'], 'second.txt', { type: 'text/plain' })

    fireEvent.change(fileInput, { target: { files: [firstFile] } })
    await flushUi()

    expect(screen.getByText('上传中 (0/1)')).toBeTruthy()
    expect(screen.getByText('first.txt')).toBeTruthy()

    fireEvent.change(fileInput, { target: { files: [secondFile] } })
    await flushUi()

    expect(screen.getByText('上传中 (0/1)')).toBeTruthy()
    expect(screen.getByText('second.txt')).toBeTruthy()
    expect(screen.queryByText('first.txt')).toBeNull()

    firstUpload.resolve(successActionResult)
    await flushUi()

    expect(screen.getByText('上传中 (0/1)')).toBeTruthy()
    expect(screen.getByText('second.txt')).toBeTruthy()
    expect(screen.queryByText('上传完成')).toBeNull()

    act(() => {
      vi.advanceTimersByTime(3000)
    })

    expect(screen.getByText('上传中 (0/1)')).toBeTruthy()
    expect(screen.getByText('second.txt')).toBeTruthy()

    secondUpload.resolve(successActionResult)
    await flushUi()

    expect(screen.getByText('上传完成')).toBeTruthy()
    expect(screen.getByText('second.txt')).toBeTruthy()
  })

  it('aborts a pending upload and refreshes the file list when a new upload session starts', async () => {
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: {
          retry: false,
          gcTime: 0,
          staleTime: 0,
        },
        mutations: {
          retry: false,
        },
      },
    })
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries')

    render(
      <QueryClientProvider client={queryClient}>
        <FilesPage />
      </QueryClientProvider>
    )

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    let firstSignal: AbortSignal | undefined
    mockUploadFile
      .mockImplementationOnce((_path, _file, _onProgress, options?: Parameters<typeof uploadFile>[3]) => {
        firstSignal = options?.signal
        return new Promise((_resolve, reject) => {
          options?.signal?.addEventListener('abort', () => {
            reject(new DOMException('upload aborted', 'AbortError'))
          }, { once: true })
        })
      })
      .mockResolvedValueOnce(successActionResult)

    const fileInput = getUploadFileInput()

    const firstFile = new File(['data1'], 'first.txt', { type: 'text/plain' })
    const secondFile = new File(['data2'], 'second.txt', { type: 'text/plain' })

    fireEvent.change(fileInput, { target: { files: [firstFile] } })
    await flushUi()

    expect(firstSignal).toBeInstanceOf(AbortSignal)
    expect(firstSignal?.aborted).toBe(false)

    fireEvent.change(fileInput, { target: { files: [secondFile] } })
    await flushUi()

    expect(firstSignal?.aborted).toBe(true)
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ['files', 'anonymous:guest:/', '/'],
    })
    expect(mockUploadFile).toHaveBeenNthCalledWith(
      2,
      '/',
      secondFile,
      expect.any(Function),
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )
    expect(screen.getByText('second.txt')).toBeTruthy()
  })

  it('aborts pending folder directory creation when a new upload session starts', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    let directorySignal: AbortSignal | undefined
    mockCreateDirectory
      .mockImplementationOnce((_path, options?: Parameters<typeof createDirectory>[1]) => {
        directorySignal = options?.signal
        return new Promise((_resolve, reject) => {
          options?.signal?.addEventListener('abort', () => {
            reject(new DOMException('directory creation aborted', 'AbortError'))
          }, { once: true })
        })
      })
      .mockResolvedValueOnce(successActionResult)

    const fileInput = getUploadFileInput()

    const firstFile = new File(['data1'], 'first.txt', { type: 'text/plain' })
    Object.defineProperty(firstFile, 'webkitRelativePath', { configurable: true, value: 'folder/first.txt' })
    const secondFile = new File(['data2'], 'second.txt', { type: 'text/plain' })
    Object.defineProperty(secondFile, 'webkitRelativePath', { configurable: true, value: 'folder/second.txt' })

    fireEvent.change(fileInput, { target: { files: [firstFile] } })
    await flushUi()

    expect(directorySignal).toBeInstanceOf(AbortSignal)
    expect(directorySignal?.aborted).toBe(false)

    fireEvent.change(fileInput, { target: { files: [secondFile] } })
    await flushUi()

    expect(directorySignal?.aborted).toBe(true)
    expect(mockUploadFile).toHaveBeenCalledTimes(1)
    expectUploadFileCalledWithSignal('/folder', secondFile)
    expect(screen.getByText('folder/second.txt')).toBeTruthy()
  })

  it('stops folder upload when creating parent directory fails', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    mockCreateDirectory.mockRejectedValueOnce(new Error('权限不足'))

    const fileInput = getUploadFileInput()
    const file = new File(['data'], 'test.txt', { type: 'text/plain' })
    Object.defineProperty(file, 'webkitRelativePath', { configurable: true, value: 'folder/test.txt' })

    fireEvent.change(fileInput, { target: { files: [file] } })

    await flushUi()

    expectCreateDirectoryCalledWithSignal('/folder')
    expect(mockUploadFile).not.toHaveBeenCalled()
    expect(screen.getByText('操作未完成，请稍后重试。')).toBeTruthy()
  })

  it('keeps clean folder upload success feedback in the upload panel', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    const fileInput = getUploadFileInput()

    const file = new File(['ok'], 'only.txt', { type: 'text/plain' })
    Object.defineProperty(file, 'webkitRelativePath', { configurable: true, value: 'folder/only.txt' })

    fireEvent.change(fileInput, { target: { files: [file] } })

    await flushUi()

    expectCreateDirectoryCalledWithSignal('/folder')
    expectUploadFileCalledWithSignal('/folder', file)
    expect(screen.getByText('上传完成')).toBeTruthy()
    expect(document.body.textContent?.replace(/\s+/g, ' ')).toContain('成功 1 个')
    expect(mockAddToast).not.toHaveBeenCalled()
  })

  it('continues folder upload when the parent directory already exists and avoids duplicate directory creation', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    mockCreateDirectory.mockRejectedValueOnce(new ApiError('already exists', 409, 'Conflict'))

    const fileInput = getUploadFileInput()

    const firstFile = new File(['one'], 'one.txt', { type: 'text/plain' })
    const secondFile = new File(['two'], 'two.txt', { type: 'text/plain' })
    Object.defineProperty(firstFile, 'webkitRelativePath', { configurable: true, value: 'folder/one.txt' })
    Object.defineProperty(secondFile, 'webkitRelativePath', { configurable: true, value: 'folder/two.txt' })

    fireEvent.change(fileInput, { target: { files: [firstFile, secondFile] } })

    await flushUi()

    expect(mockCreateDirectory).toHaveBeenCalledTimes(1)
    expectUploadFileCalledWithSignal('/folder', firstFile)
    expectUploadFileCalledWithSignal('/folder', secondFile)
    expect(document.body.textContent?.replace(/\s+/g, ' ')).toContain('成功 2 个')
    expect(mockAddToast).not.toHaveBeenCalled()
  })

  it('rejects oversized files before starting the upload request', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    const fileInput = getUploadFileInput()

    const oversizedFile = new File(['data'], 'huge.bin', { type: 'application/octet-stream' })
    Object.defineProperty(oversizedFile, 'size', {
      configurable: true,
      value: MAX_UPLOAD_FILE_SIZE_BYTES + 1,
    })

    fireEvent.change(fileInput, { target: { files: [oversizedFile] } })

    await flushUi()

    expect(mockUploadFile).not.toHaveBeenCalled()
    expect(screen.getByText('huge.bin 超过 10 GB 上传限制')).toBeTruthy()
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '上传失败',
      description: 'huge.bin 超过 10 GB 上传限制',
      color: 'danger',
    })
  })

  it('skips oversized files and continues uploading the rest', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    const fileInput = getUploadFileInput()

    const oversizedFile = new File(['data'], 'huge.bin', { type: 'application/octet-stream' })
    Object.defineProperty(oversizedFile, 'size', {
      configurable: true,
      value: MAX_UPLOAD_FILE_SIZE_BYTES + 1,
    })
    const smallFile = new File(['ok'], 'small.txt', { type: 'text/plain' })

    fireEvent.change(fileInput, { target: { files: [oversizedFile, smallFile] } })

    await flushUi()

    expect(mockUploadFile).toHaveBeenCalledTimes(1)
    expectUploadFileCalledWithSignal('/', smallFile)
    expect(screen.getByText('huge.bin 超过 10 GB 上传限制')).toBeTruthy()
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '部分文件未上传',
      description: 'huge.bin 超过 10 GB 上传限制',
      color: 'warning',
    })
  })

  it('shows partial summary for folder uploads with failures', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    mockUploadFile
      .mockResolvedValueOnce(successActionResult)
      .mockRejectedValueOnce(new Error('网络错误'))

    const fileInput = getUploadFileInput()

    const firstFile = new File(['ok'], 'first.txt', { type: 'text/plain' })
    const secondFile = new File(['bad'], 'second.txt', { type: 'text/plain' })
    Object.defineProperty(firstFile, 'webkitRelativePath', { configurable: true, value: 'folder/first.txt' })
    Object.defineProperty(secondFile, 'webkitRelativePath', { configurable: true, value: 'folder/second.txt' })

    fireEvent.change(fileInput, { target: { files: [firstFile, secondFile] } })

    await flushUi()

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '文件夹上传部分完成',
      description: '成功上传 1 个文件，失败 1 个',
      color: 'warning',
    })
  })

  it('shows quota guidance when folder uploads fully fail due to quota', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    mockUploadFile.mockRejectedValue(new ApiError('directory quota exceeded', 507, 'Insufficient Storage', 'QUOTA_EXCEEDED'))

    const fileInput = getUploadFileInput()

    const file = new File(['bad'], 'only.txt', { type: 'text/plain' })
    Object.defineProperty(file, 'webkitRelativePath', { configurable: true, value: 'folder/only.txt' })

    fireEvent.change(fileInput, { target: { files: [file] } })

    await flushUi()

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '容量配额不足',
      description: '目标目录的容量配额不足，请清理空间或调整目录配额后重试。',
      color: 'warning',
    })
  })

  it('shows quota guidance in failed upload rows', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    mockUploadFile.mockRejectedValueOnce(new ApiError('user quota exceeded', 507, 'Insufficient Storage', 'QUOTA_EXCEEDED'))

    const fileInput = getUploadFileInput()
    const file = new File(['bad'], 'quota.txt', { type: 'text/plain' })

    fireEvent.change(fileInput, { target: { files: [file] } })

    await flushUi()

    expect(screen.getByText('quota.txt')).toBeTruthy()
    expect(screen.getByText('当前用户的容量配额不足，请清理空间或调整用户配额后重试。')).toBeTruthy()
  })

  it('shows quota guidance when folder uploads partially fail due to quota', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    mockUploadFile
      .mockResolvedValueOnce(successActionResult)
      .mockRejectedValueOnce(new ApiError('directory quota exceeded', 507, 'Insufficient Storage', 'QUOTA_EXCEEDED'))

    const fileInput = getUploadFileInput()

    const firstFile = new File(['ok'], 'first.txt', { type: 'text/plain' })
    const secondFile = new File(['bad'], 'second.txt', { type: 'text/plain' })
    Object.defineProperty(firstFile, 'webkitRelativePath', { configurable: true, value: 'folder/first.txt' })
    Object.defineProperty(secondFile, 'webkitRelativePath', { configurable: true, value: 'folder/second.txt' })

    fireEvent.change(fileInput, { target: { files: [firstFile, secondFile] } })

    await flushUi()

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '文件夹上传部分完成',
      description: '成功上传 1 个文件，失败 1 个；目标目录的容量配额不足，请清理空间或调整目录配额后重试。',
      color: 'warning',
    })
  })

  it('shows warning summary for folder uploads when directory creation succeeds with warnings', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    mockCreateDirectory.mockResolvedValueOnce(warningActionResult('directory created with persistence warning'))

    const fileInput = getUploadFileInput()

    const file = new File(['ok'], 'first.txt', { type: 'text/plain' })
    Object.defineProperty(file, 'webkitRelativePath', { configurable: true, value: 'folder/first.txt' })

    fireEvent.change(fileInput, { target: { files: [file] } })

    await flushUi()

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '文件夹上传完成，但存在警告',
      description: '成功上传 1 个文件',
      color: 'warning',
    })
  })

  it('uses a fallback warning summary when a folder upload warning has no message', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    mockUploadFile.mockResolvedValueOnce({ warning: true, message: undefined })

    const fileInput = getUploadFileInput()

    const file = new File(['ok'], 'first.txt', { type: 'text/plain' })
    Object.defineProperty(file, 'webkitRelativePath', { configurable: true, value: 'folder/first.txt' })

    fireEvent.change(fileInput, { target: { files: [file] } })

    await flushUi()

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '文件夹上传完成，但存在警告',
      description: '成功上传 1 个文件',
      color: 'warning',
    })
  })

  it('shows warning summary for file uploads when upload succeeds with warnings', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    mockUploadFile.mockResolvedValueOnce(warningActionResult('file uploaded with persistence warning'))

    const fileInput = getUploadFileInput()

    const file = new File(['ok'], 'warn.txt', { type: 'text/plain' })

    fireEvent.change(fileInput, { target: { files: [file] } })

    await flushUi()

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '上传完成，但存在警告',
      description: '成功上传 1 个文件',
      color: 'warning',
    })
  })

  it('uses a fallback warning summary when a file upload warning has no message', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    mockUploadFile.mockResolvedValueOnce({ warning: true, message: undefined })

    const fileInput = getUploadFileInput()

    const file = new File(['ok'], 'warn.txt', { type: 'text/plain' })

    fireEvent.change(fileInput, { target: { files: [file] } })

    await flushUi()

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '上传完成，但存在警告',
      description: '成功上传 1 个文件',
      color: 'warning',
    })
  })

  it('preserves warning detail when folder uploads partially succeed with warnings', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    mockUploadFile
      .mockResolvedValueOnce(warningActionResult('file uploaded with persistence warning'))
      .mockRejectedValueOnce(new Error('网络错误'))

    const fileInput = getUploadFileInput()

    const firstFile = new File(['ok'], 'first.txt', { type: 'text/plain' })
    const secondFile = new File(['bad'], 'second.txt', { type: 'text/plain' })
    Object.defineProperty(firstFile, 'webkitRelativePath', { configurable: true, value: 'folder/first.txt' })
    Object.defineProperty(secondFile, 'webkitRelativePath', { configurable: true, value: 'folder/second.txt' })

    fireEvent.change(fileInput, { target: { files: [firstFile, secondFile] } })

    await flushUi()

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '文件夹上传部分完成',
      description: '成功上传 1 个文件，失败 1 个',
      color: 'warning',
    })
  })

  it('shows unavailable summary for folder uploads when filesystem is unavailable', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    mockUploadFile.mockRejectedValue(new ApiError('filesystem not initialized', 503, 'Service Unavailable', 'SERVICE_UNAVAILABLE'))

    const fileInput = getUploadFileInput()

    const file = new File(['bad'], 'only.txt', { type: 'text/plain' })
    Object.defineProperty(file, 'webkitRelativePath', { configurable: true, value: 'folder/only.txt' })

    fireEvent.change(fileInput, { target: { files: [file] } })

    await flushUi()

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '文件夹上传暂不可用',
      description: '文件系统当前不可用，请检查设备状态或稍后重试。',
      color: 'warning',
    })
    expect(screen.getByText('文件系统当前不可用，请检查设备状态或稍后重试。')).toBeTruthy()
  })

  it('shows a generic failed upload row for unknown upload errors', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    mockUploadFile.mockRejectedValueOnce('upload stopped')

    const fileInput = getUploadFileInput()

    const file = new File(['bad'], 'unknown.txt', { type: 'text/plain' })

    fireEvent.change(fileInput, { target: { files: [file] } })

    await flushUi()

    expect(screen.getByText('unknown.txt')).toBeTruthy()
    expect(screen.getByText('操作未完成，请稍后重试。')).toBeTruthy()
  })
})
