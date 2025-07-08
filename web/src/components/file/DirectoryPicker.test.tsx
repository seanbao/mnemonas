import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { DirectoryPicker } from './DirectoryPicker'

const mockAddToast = vi.fn()
const mockUser = { id: 'u1', username: 'admin', role: 'admin' as const, email: 'admin@local', homeDir: '/' }

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
  listFiles: vi.fn(),
  createDirectory: vi.fn(),
}))

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useUser: () => mockUser,
  }
})

import { ApiError, listFiles, createDirectory } from '@/api/files'

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

function expectListFilesCalledWithAbortSignal(path: string) {
  const call = mockListFiles.mock.calls.find(([calledPath]) => calledPath === path)
  expect(call).toBeTruthy()
  expect((call?.[1] as { signal?: AbortSignal } | undefined)?.signal).toBeInstanceOf(AbortSignal)
}

function expectCreateDirectoryCalledWithAbortSignal(path: string) {
  const call = mockCreateDirectory.mock.calls.find(([calledPath]) => calledPath === path)
  expect(call).toBeTruthy()
  expect((call?.[1] as { signal?: AbortSignal } | undefined)?.signal).toBeInstanceOf(AbortSignal)
}

function expectListFilesCalledWithPath(path: string) {
  expect(mockListFiles.mock.calls.some(([calledPath]) => calledPath === path)).toBe(true)
}

function expectListFilesNotCalledWithPath(path: string) {
  expect(mockListFiles.mock.calls.some(([calledPath]) => calledPath === path)).toBe(false)
}

function getDirectoryToggle(name: string, expanded = false) {
  return screen.getByRole('button', { name: `${expanded ? '折叠' : '展开'} ${name}` })
}

function renderPicker(props?: Partial<React.ComponentProps<typeof DirectoryPicker>>) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })

  const defaultProps: React.ComponentProps<typeof DirectoryPicker> = {
    isOpen: true,
    onClose: vi.fn(),
    onSelect: vi.fn(),
  }

  return {
    queryClient,
    ...render(
      <QueryClientProvider client={queryClient}>
        <DirectoryPicker {...defaultProps} {...props} />
      </QueryClientProvider>
    ),
  }
}

describe('DirectoryPicker', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUser.id = 'u1'
    mockUser.username = 'admin'
    mockUser.role = 'admin'
    mockUser.email = 'admin@local'
    mockUser.homeDir = '/'
    mockListFiles.mockResolvedValue({
      path: '/',
      files: [
        { name: 'docs', path: '/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
      ],
    })
    mockCreateDirectory.mockResolvedValue(successActionResult)
  })

  it('shows a danger toast when expanding a directory fails', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockListFiles
      .mockResolvedValueOnce({
        path: '/',
        files: [{ name: 'docs', path: '/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' }],
      })
      .mockRejectedValueOnce(new Error('directory unavailable'))
      .mockResolvedValueOnce({
        path: '/docs',
        files: [],
      })

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
    })

    await user.click(getDirectoryToggle('docs'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '加载目录失败',
        description: '数据加载失败，请检查网络或稍后重试。',
        color: 'danger',
      })
    })

    await user.click(getDirectoryToggle('docs'))

    await waitFor(() => {
      expect(mockListFiles).toHaveBeenCalledTimes(3)
    })
  })

  it('passes the query abort signal when loading the root directory', async () => {
    renderPicker()

    await waitFor(() => {
      expectListFilesCalledWithAbortSignal('/')
    })
  })

  it('passes an abort signal when expanding a directory', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockListFiles
      .mockResolvedValueOnce({
        path: '/',
        files: [{ name: 'docs', path: '/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' }],
      })
      .mockResolvedValueOnce({
        path: '/docs',
        files: [],
      })

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
    })

    await user.click(getDirectoryToggle('docs'))

    await waitFor(() => {
      expectListFilesCalledWithAbortSignal('/docs')
    })
  })

  it('aborts an in-flight expanded directory load when the picker unmounts', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    let expandedSignal: AbortSignal | undefined
    const pendingDirectory = createDeferred<Awaited<ReturnType<typeof listFiles>>>()
    mockListFiles
      .mockResolvedValueOnce({
        path: '/',
        files: [{ name: 'docs', path: '/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' }],
      })
      .mockImplementationOnce((_path, options?: { signal?: AbortSignal }) => {
        expandedSignal = options?.signal
        return pendingDirectory.promise
      })

    const { unmount } = renderPicker()

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
    })

    await user.click(getDirectoryToggle('docs'))

    await waitFor(() => {
      expect(expandedSignal).toBeInstanceOf(AbortSignal)
    })

    unmount()

    expect(expandedSignal?.aborted).toBe(true)
  })

  it('collapses and re-expands an already loaded directory without refetching it', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockListFiles
      .mockResolvedValueOnce({
        path: '/',
        files: [{ name: 'docs', path: '/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' }],
      })
      .mockResolvedValueOnce({
        path: '/docs',
        files: [{ name: 'reports', path: '/docs/reports', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' }],
      })

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
    })

    await user.click(getDirectoryToggle('docs'))

    await waitFor(() => {
      expect(screen.getByText('reports')).toBeTruthy()
    })

    await user.click(getDirectoryToggle('docs', true))
    expect(screen.queryByText('reports')).toBeNull()

    await user.click(getDirectoryToggle('docs'))

    await waitFor(() => {
      expect(screen.getByText('reports')).toBeTruthy()
      expect(mockListFiles.mock.calls.filter(([path]) => path === '/docs')).toHaveLength(1)
    })
  })

  it('selects a folder and confirms the selected path', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()
    const onSelect = vi.fn()

    renderPicker({ onClose, onSelect })

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
    })

    await user.click(screen.getByText('docs'))

    await waitFor(() => {
      expect(screen.getByText('/docs')).toBeTruthy()
    })

    await user.click(screen.getByRole('button', { name: '选择此目录' }))

    expect(onSelect).toHaveBeenCalledWith('/docs')
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('does not select or expand excluded folders', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onSelect = vi.fn()

    renderPicker({ excludePaths: ['/docs'], onSelect })

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
    })

    await user.click(screen.getByText('docs'))

    await user.click(getDirectoryToggle('docs'))
    await user.click(screen.getByRole('button', { name: '选择此目录' }))

    expect(onSelect).toHaveBeenCalledWith('/')
    expectListFilesNotCalledWithPath('/docs')
  })

  it('normalizes excluded folder paths before selection', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onSelect = vi.fn()

    renderPicker({ excludePaths: ['docs/'], onSelect })

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
    })

    await user.click(screen.getByText('docs'))
    await user.click(screen.getByRole('button', { name: '选择此目录' }))

    expect(onSelect).toHaveBeenCalledWith('/')
  })

  it('does not confirm an excluded root directory', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onSelect = vi.fn()

    renderPicker({ excludePaths: ['/'], onSelect })

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
    })

    const confirmButton = screen.getByRole('button', { name: '选择此目录' })
    expect(confirmButton).toBeDisabled()
    expect(screen.queryByText('在此处新建文件夹')).toBeNull()

    await user.click(screen.getByText('docs'))
    await user.click(confirmButton)

    expect(onSelect).not.toHaveBeenCalled()
    expectListFilesNotCalledWithPath('/docs')
  })

  it('cancels folder creation before submitting', async () => {
    const user = userEvent.setup({ writeToClipboard: false })

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), 'draft')
    await user.click(screen.getByRole('button', { name: '取消新建文件夹' }))

    expect(screen.queryByLabelText('新文件夹名称')).toBeNull()
    expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    expect(mockCreateDirectory).not.toHaveBeenCalled()
  })

  it('rejects folder names with path separators before creating a directory', async () => {
    const user = userEvent.setup({ writeToClipboard: false })

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), '../escape')

    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()

    await user.keyboard('{Enter}')

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '文件夹名称无效',
        description: '名称不能包含路径分隔符、空字符，且不能为 . 或 ..。',
        color: 'warning',
      })
    })
    expect(mockCreateDirectory).not.toHaveBeenCalled()
  })

  it('closes from the footer cancel button when idle', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()

    renderPicker({ onClose })

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByRole('button', { name: '取消' }))

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('does not reuse cached root directory entries from another user session', async () => {
    mockUser.id = 'u2'
    mockUser.username = 'member'
    mockUser.role = 'user'
    mockUser.email = 'member@local'
    mockUser.homeDir = '/member'
    mockListFiles.mockImplementation(() => new Promise(() => {}))

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
      path: '/member',
      files: [{ name: 'admin-secret', path: '/member/admin-secret', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' }],
    })

    render(
      <QueryClientProvider client={queryClient}>
        <DirectoryPicker isOpen={true} onClose={vi.fn()} onSelect={vi.fn()} />
      </QueryClientProvider>
    )

    await waitFor(() => {
      expectListFilesCalledWithPath('/member')
    })

    expect(screen.queryByText('admin-secret')).toBeNull()
  })

  it('shows generic error details when creating a folder fails', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockCreateDirectory.mockRejectedValueOnce(new Error('permission denied'))

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), 'private')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '创建文件夹失败',
        description: '操作未完成，请稍后重试。',
        color: 'danger',
      })
    })
  })

  it('updates the visible root tree immediately after creating a folder at root', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockListFiles
      .mockResolvedValueOnce({
        path: '/',
        files: [
          { name: 'docs', path: '/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
        ],
      })
      .mockResolvedValueOnce({
        path: '/',
        files: [
          { name: 'docs', path: '/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
          { name: 'private', path: '/private', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
        ],
      })

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), 'private')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expectCreateDirectoryCalledWithAbortSignal('/private')
      expect(screen.getByText('private')).toBeTruthy()
      expect(screen.getByText('/private')).toBeTruthy()
    })
  })

  it('passes an abort signal when creating a folder', async () => {
    const user = userEvent.setup({ writeToClipboard: false })

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), 'private')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expectCreateDirectoryCalledWithAbortSignal('/private')
    })
  })

  it('passes an abort signal when reloading the parent after creating a folder', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    let parentReloadSignal: AbortSignal | undefined
    mockListFiles
      .mockResolvedValueOnce({
        path: '/',
        files: [
          { name: 'docs', path: '/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
        ],
      })
      .mockImplementationOnce((_path, options?: { signal?: AbortSignal }) => {
        parentReloadSignal = options?.signal
        return Promise.resolve({
          path: '/',
          files: [
            { name: 'docs', path: '/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
            { name: 'private', path: '/private', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
          ],
        })
      })

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), 'private')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(parentReloadSignal).toBeInstanceOf(AbortSignal)
    })
  })

  it('aborts an in-flight folder create request when the picker unmounts', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    let createSignal: AbortSignal | undefined
    const pendingCreate = createDeferred<typeof successActionResult>()
    mockCreateDirectory.mockImplementationOnce((_path, options?: Parameters<typeof createDirectory>[1]) => {
      createSignal = options?.signal
      return pendingCreate.promise
    })

    const { unmount } = renderPicker()

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), 'private')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(createSignal).toBeInstanceOf(AbortSignal)
    })

    unmount()

    expect(createSignal?.aborted).toBe(true)
  })

  it('aborts an in-flight parent reload when the picker unmounts after creating a folder', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    let parentReloadSignal: AbortSignal | undefined
    const pendingParentReload = createDeferred<Awaited<ReturnType<typeof listFiles>>>()
    mockListFiles
      .mockResolvedValueOnce({
        path: '/',
        files: [
          { name: 'docs', path: '/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
        ],
      })
      .mockImplementationOnce((_path, options?: { signal?: AbortSignal }) => {
        parentReloadSignal = options?.signal
        return pendingParentReload.promise
      })

    const { unmount } = renderPicker()

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), 'private')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(parentReloadSignal).toBeInstanceOf(AbortSignal)
    })

    unmount()

    expect(parentReloadSignal?.aborted).toBe(true)
  })

  it('shows warning toast when creating a folder succeeds with a persistence warning', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockCreateDirectory.mockResolvedValueOnce(warningActionResult('directory created with persistence warning'))

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), 'warn-folder')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '文件夹创建完成，但存在警告',
        color: 'warning',
      })
    })
  })

  it('shows a synchronized warning when the created folder already exists', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockCreateDirectory.mockResolvedValueOnce({ warning: false, message: 'directory already exists' })

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), 'docs')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '文件夹已存在，已同步更新',
        color: 'warning',
      })
    })

    await waitFor(() => {
      expect(screen.queryByLabelText('新文件夹名称')).toBeFalsy()
      expect(screen.getByText('/docs')).toBeTruthy()
    })
  })

  it('shows a localized warning when creating a folder hits a name conflict', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockCreateDirectory.mockRejectedValueOnce(new ApiError('resource already exists', 409, 'Conflict'))

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), 'docs')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '同名项目已存在',
        description: '当前目录中已存在同名文件或文件夹，请使用其他名称。',
        color: 'warning',
      })
    })

    expect(screen.getByLabelText('新文件夹名称')).toHaveValue('docs')
  })

  it('shows a localized warning when the create parent stops being a directory', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockCreateDirectory.mockRejectedValueOnce(new ApiError('parent path is not a directory', 409, 'Conflict'))

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), 'child')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '目标位置不可用',
        description: '当前目录状态已变更，请刷新列表后重试。',
        color: 'warning',
      })
    })
  })

  it('shows quota guidance when creating a folder exceeds quota', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockCreateDirectory.mockRejectedValueOnce(new ApiError('user quota exceeded', 507, 'Insufficient Storage', 'QUOTA_EXCEEDED'))

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), 'quota-child')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '容量配额不足',
        description: '当前用户的容量配额不足，请清理空间或调整用户配额后重试。',
        color: 'warning',
      })
    })
  })

  it('shows an unavailable toast when expanding a directory hits filesystem unavailability', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockListFiles
      .mockResolvedValueOnce({
        path: '/',
        files: [{ name: 'docs', path: '/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' }],
      })
      .mockRejectedValueOnce(new ApiError('filesystem not initialized', 503, 'Service Unavailable', 'SERVICE_UNAVAILABLE'))

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
    })

    await user.click(getDirectoryToggle('docs'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '目录暂不可用',
        description: '文件系统当前不可用，请检查设备状态或稍后重试。',
        color: 'warning',
      })
    })
  })

  it('shows unavailable details when root directory is unavailable', async () => {
    mockListFiles.mockRejectedValueOnce(new ApiError('filesystem not initialized', 503, 'Service Unavailable', 'SERVICE_UNAVAILABLE'))

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('目录暂不可用')).toBeTruthy()
      expect(screen.getByText('文件系统当前不可用，请检查设备状态或稍后重试。')).toBeTruthy()
      expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
    })
  })

  it('shows unavailable toast when creating a folder is unavailable', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockCreateDirectory.mockRejectedValueOnce(new ApiError('filesystem not initialized', 503, 'Service Unavailable', 'SERVICE_UNAVAILABLE'))

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), 'private')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '创建目录暂不可用',
        description: '文件系统当前不可用，请检查设备状态或稍后重试。',
        color: 'warning',
      })
    })
  })

  it('shows a retryable error state when the root directory fails to load', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockListFiles
      .mockRejectedValueOnce(new Error('root unavailable'))
      .mockResolvedValueOnce({
        path: '/',
        files: [{ name: 'docs', path: '/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' }],
      })

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('加载目录失败')).toBeTruthy()
      expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeTruthy()
      expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
    })
    expect(screen.queryByText('root unavailable')).toBeNull()

    expect(screen.queryByText('当前目录没有子文件夹')).toBeNull()

    await user.click(screen.getByRole('button', { name: '重新加载' }))

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
      expect(mockAddToast).toHaveBeenCalledWith({ title: '目录已刷新', color: 'success' })
    })
  })

  it('shows warning toast when reloading the root directory is temporarily unavailable', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockListFiles
      .mockRejectedValueOnce(new Error('root unavailable'))
      .mockRejectedValueOnce(new ApiError('filesystem not initialized', 503, 'Service Unavailable', 'SERVICE_UNAVAILABLE'))

    renderPicker()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
    })

    await user.click(screen.getByRole('button', { name: '重新加载' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '目录暂不可用',
        description: '文件系统当前不可用，请检查设备状态或稍后重试。',
        color: 'warning',
      })
    })
  })

  it('uses the assigned home directory as the visible root for non-admin users', async () => {
    mockUser.id = 'u2'
    mockUser.username = 'tester'
    mockUser.role = 'user'
    mockUser.homeDir = '/tester'
    mockListFiles.mockResolvedValueOnce({
      path: '/tester',
      files: [
        { name: 'docs', path: '/tester/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
      ],
    })

    renderPicker({ initialPath: '/tester/projects' })

    await waitFor(() => {
      expectListFilesCalledWithPath('/tester')
      expect(screen.getByText('主目录')).toBeTruthy()
      expect(screen.getByText('/tester/projects')).toBeTruthy()
      expect(screen.getByText('docs')).toBeTruthy()
    })
  })

  it('uses an out-of-home initial path as the visible root so server access rules can resolve it', async () => {
    mockUser.id = 'u2'
    mockUser.username = 'tester'
    mockUser.role = 'user'
    mockUser.homeDir = '/tester'
    mockListFiles.mockResolvedValueOnce({
      path: '/team/projects',
      files: [
        { name: 'drafts', path: '/team/projects/drafts', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
      ],
    })

    renderPicker({ initialPath: '/team/projects' })

    await waitFor(() => {
      expectListFilesCalledWithPath('/team/projects')
      expect(screen.getAllByText('projects').length).toBeGreaterThan(0)
      expect(screen.getByText('/team/projects')).toBeTruthy()
      expect(screen.getByText('drafts')).toBeTruthy()
    })
  })

  it('falls back to the visible root when the initial path is malformed', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onSelect = vi.fn()
    mockUser.id = 'u2'
    mockUser.username = 'tester'
    mockUser.role = 'user'
    mockUser.homeDir = '/tester'
    mockListFiles.mockResolvedValueOnce({
      path: '/tester',
      files: [
        { name: 'docs', path: '/tester/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
      ],
    })

    renderPicker({ initialPath: '/team/./projects', onSelect })

    await waitFor(() => {
      expectListFilesCalledWithPath('/tester')
      expectListFilesNotCalledWithPath('/team/./projects')
      expect(screen.getAllByText('主目录').length).toBeGreaterThan(0)
      expect(screen.getByText('docs')).toBeTruthy()
    })

    await user.click(screen.getByRole('button', { name: '选择此目录' }))

    expect(onSelect).toHaveBeenCalledWith('/tester')
  })

  it('shows an invalid-home error instead of loading root for non-admin users without a home directory', async () => {
    mockUser.id = 'u2'
    mockUser.username = 'tester'
    mockUser.role = 'user'
    mockUser.homeDir = ''

    renderPicker({ initialPath: '/tester/projects' })

    await waitFor(() => {
      expect(screen.getByText('主目录配置无效')).toBeTruthy()
      expect(screen.getByText('当前账户未配置有效的主目录，无法选择目录。请联系管理员修复账户 home_dir。')).toBeTruthy()
    })

    expect(mockListFiles).not.toHaveBeenCalled()
  })

  it('keeps the picker open while a pending create request is in flight', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const pendingCreate = createDeferred<typeof successActionResult>()
    const onClose = vi.fn()
    mockListFiles.mockImplementation(async (path) => {
      if (path === '/') {
        return {
          path: '/',
          files: [
            { name: 'docs', path: '/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
            { name: 'old', path: '/old', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
          ],
        }
      }

      return { path: '/docs', files: [] }
    })
    mockCreateDirectory.mockImplementation((path) => {
      if (path === '/old') {
        return pendingCreate.promise
      }
      return Promise.resolve(successActionResult)
    })

    renderPicker({ onClose })

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), 'old')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expectCreateDirectoryCalledWithAbortSignal('/old')
    })

    await user.click(screen.getByRole('button', { name: '选择此目录' }))

    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByLabelText('新文件夹名称')).toHaveValue('old')

    await act(async () => {
      pendingCreate.resolve(successActionResult)
      await pendingCreate.promise
    })

    await waitFor(() => {
      expect(screen.getByText('/old')).toBeTruthy()
      expect(screen.queryByLabelText('新文件夹名称')).toBeFalsy()
    })
  })

  it('keeps the picker open when a pending create request later fails', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const pendingCreate = createDeferred<typeof successActionResult>()
    const onClose = vi.fn()
    mockCreateDirectory.mockImplementationOnce(() => pendingCreate.promise)

    renderPicker({ onClose })

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByLabelText('新文件夹名称'), 'old')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expectCreateDirectoryCalledWithAbortSignal('/old')
    })

    await user.click(screen.getByRole('button', { name: '选择此目录' }))

    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByLabelText('新文件夹名称')).toHaveValue('old')

    await act(async () => {
      pendingCreate.reject(new Error('create failed'))
    })

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '创建文件夹失败',
        description: '操作未完成，请稍后重试。',
        color: 'danger',
      })
    })

    expect(screen.getByLabelText('新文件夹名称')).toHaveValue('old')
    expect(onClose).not.toHaveBeenCalled()
  })

  it('supports keyboard shortcuts in the create folder input', async () => {
    const user = userEvent.setup({ writeToClipboard: false })

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.keyboard('{Enter}')
    expect(mockCreateDirectory).not.toHaveBeenCalled()

    await user.type(screen.getByLabelText('新文件夹名称'), 'draft')
    await user.keyboard('{Escape}')

    expect(screen.queryByLabelText('新文件夹名称')).toBeNull()
    expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    expect(mockCreateDirectory).not.toHaveBeenCalled()
  })

  it('reloads a directory after reopen instead of using a stale older expansion result', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const firstLoad = createDeferred<{ path: string; files: { name: string; path: string; isDir: boolean; size: number; modTime: string }[] }>()
    let docsLoadCount = 0
    mockListFiles.mockImplementation((path) => {
      if (path === '/docs') {
        docsLoadCount += 1
        if (docsLoadCount === 1) {
          return firstLoad.promise as ReturnType<typeof listFiles>
        }
        return Promise.resolve({
          path: '/docs',
          files: [{ name: 'fresh-child', path: '/docs/fresh-child', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' }],
        }) as ReturnType<typeof listFiles>
      }

      return Promise.resolve({
        path: '/',
        files: [{ name: 'docs', path: '/docs', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' }],
      }) as ReturnType<typeof listFiles>
    })

    const view = renderPicker()

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
    })

    await user.click(getDirectoryToggle('docs'))

    await waitFor(() => {
      expectListFilesCalledWithPath('/docs')
    })

    view.rerender(
      <QueryClientProvider client={view.queryClient}>
        <DirectoryPicker isOpen={false} onClose={vi.fn()} onSelect={vi.fn()} />
      </QueryClientProvider>,
    )

    view.rerender(
      <QueryClientProvider client={view.queryClient}>
        <DirectoryPicker isOpen={true} onClose={vi.fn()} onSelect={vi.fn()} />
      </QueryClientProvider>,
    )

    await act(async () => {
      firstLoad.resolve({
        path: '/docs',
        files: [{ name: 'stale-child', path: '/docs/stale-child', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' }],
      })
      await firstLoad.promise
    })

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
    })

    await user.click(getDirectoryToggle('docs'))

    await waitFor(() => {
      expect(mockListFiles.mock.calls.filter(([calledPath]) => calledPath === '/docs')).toHaveLength(2)
      expect(screen.getByText('fresh-child')).toBeTruthy()
      expect(screen.queryByText('stale-child')).toBeNull()
    })
  })
})
