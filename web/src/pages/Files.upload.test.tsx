import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@/test/utils'
import React from 'react'
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
  listFiles: vi.fn(),
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

    constructor(message: string, status: number, statusText: string) {
      super(message)
      this.status = status
      this.statusText = statusText
    }
  },
}))

vi.mock('@/api/favorites', () => ({
  checkFavorites: vi.fn().mockResolvedValue({}),
  toggleFavorite: vi.fn(),
}))

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

import { listFiles, uploadFile, createDirectory, MAX_UPLOAD_FILE_SIZE_BYTES } from '@/api/files'

const mockListFiles = vi.mocked(listFiles)
const mockUploadFile = vi.mocked(uploadFile)
const mockCreateDirectory = vi.mocked(createDirectory)

describe('FilesPage upload queue', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    useCanWriteMock.mockReturnValue(true)
    mockListFiles.mockResolvedValue({
      files: [],
      path: '/',
    })
    mockUploadFile.mockResolvedValue(undefined)
    mockCreateDirectory.mockResolvedValue(undefined)
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

  it('clears successful uploads after timeout', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).toBeTruthy()
    const file = new File(['data'], 'test.txt', { type: 'text/plain' })

    fireEvent.change(fileInput as HTMLInputElement, { target: { files: [file] } })

    await flushUi()

    expect(screen.getByText('上传完成')).toBeTruthy()

    act(() => {
      vi.advanceTimersByTime(3000)
    })

    expect(screen.queryByText('上传完成')).toBeNull()
  })

  it('clears only the latest upload timer', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).toBeTruthy()

    const firstFile = new File(['data1'], 'first.txt', { type: 'text/plain' })
    const secondFile = new File(['data2'], 'second.txt', { type: 'text/plain' })

    fireEvent.change(fileInput as HTMLInputElement, { target: { files: [firstFile] } })
    await flushUi()

    expect(screen.getByText('上传完成')).toBeTruthy()

    act(() => {
      vi.advanceTimersByTime(1000)
    })

    fireEvent.change(fileInput as HTMLInputElement, { target: { files: [secondFile] } })
    await flushUi()

    expect(screen.getByText('上传完成')).toBeTruthy()

    act(() => {
      vi.advanceTimersByTime(2000)
    })

    expect(screen.queryByText('上传完成')).toBeTruthy()

    act(() => {
      vi.advanceTimersByTime(1000)
    })

    expect(screen.queryByText('上传完成')).toBeNull()
  })

  it('stops folder upload when creating parent directory fails', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    mockCreateDirectory.mockRejectedValueOnce(new Error('权限不足'))

    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).toBeTruthy()
    const file = new File(['data'], 'test.txt', { type: 'text/plain' })
    Object.defineProperty(file, 'webkitRelativePath', { configurable: true, value: 'folder/test.txt' })

    fireEvent.change(fileInput as HTMLInputElement, { target: { files: [file] } })

    await flushUi()

    expect(mockCreateDirectory).toHaveBeenCalledWith('/folder')
    expect(mockUploadFile).not.toHaveBeenCalled()
    expect(screen.getByText('权限不足')).toBeTruthy()
  })

  it('rejects oversized files before starting the upload request', async () => {
    render(<FilesPage />)

    await act(async () => {
      await vi.runOnlyPendingTimersAsync()
    })

    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).toBeTruthy()

    const oversizedFile = new File(['data'], 'huge.bin', { type: 'application/octet-stream' })
    Object.defineProperty(oversizedFile, 'size', {
      configurable: true,
      value: MAX_UPLOAD_FILE_SIZE_BYTES + 1,
    })

    fireEvent.change(fileInput as HTMLInputElement, { target: { files: [oversizedFile] } })

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

    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).toBeTruthy()

    const oversizedFile = new File(['data'], 'huge.bin', { type: 'application/octet-stream' })
    Object.defineProperty(oversizedFile, 'size', {
      configurable: true,
      value: MAX_UPLOAD_FILE_SIZE_BYTES + 1,
    })
    const smallFile = new File(['ok'], 'small.txt', { type: 'text/plain' })

    fireEvent.change(fileInput as HTMLInputElement, { target: { files: [oversizedFile, smallFile] } })

    await flushUi()

    expect(mockUploadFile).toHaveBeenCalledTimes(1)
    expect(mockUploadFile).toHaveBeenCalledWith('/', smallFile, expect.any(Function))
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
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error('网络错误'))

    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).toBeTruthy()

    const firstFile = new File(['ok'], 'first.txt', { type: 'text/plain' })
    const secondFile = new File(['bad'], 'second.txt', { type: 'text/plain' })
    Object.defineProperty(firstFile, 'webkitRelativePath', { configurable: true, value: 'folder/first.txt' })
    Object.defineProperty(secondFile, 'webkitRelativePath', { configurable: true, value: 'folder/second.txt' })

    fireEvent.change(fileInput as HTMLInputElement, { target: { files: [firstFile, secondFile] } })

    await flushUi()

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '文件夹上传部分完成',
      description: '成功上传 1 个文件，失败 1 个',
      color: 'warning',
    })
  })
})
