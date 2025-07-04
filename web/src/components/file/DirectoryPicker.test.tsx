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

    const toggleButtons = screen.getAllByRole('button')
    await user.click(toggleButtons.find((button) => button.className.includes('w-5 h-5')) ?? toggleButtons[0])

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '加载目录失败',
        description: 'directory unavailable',
        color: 'danger',
      })
    })

    await user.click(toggleButtons.find((button) => button.className.includes('w-5 h-5')) ?? toggleButtons[0])

    await waitFor(() => {
      expect(mockListFiles).toHaveBeenCalledTimes(3)
    })
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
      expect(mockListFiles).toHaveBeenCalledWith('/member')
    })

    expect(screen.queryByText('admin-secret')).toBeNull()
  })

  it('shows backend error details when creating a folder fails', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockCreateDirectory.mockRejectedValueOnce(new Error('permission denied'))

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByPlaceholderText('新文件夹名称'), 'private')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '创建文件夹失败',
        description: 'permission denied',
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
    await user.type(screen.getByPlaceholderText('新文件夹名称'), 'private')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(mockCreateDirectory).toHaveBeenCalledWith('/private')
      expect(screen.getByText('private')).toBeTruthy()
      expect(screen.getByText('/private')).toBeTruthy()
    })
  })

  it('shows warning toast when creating a folder succeeds with a persistence warning', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockCreateDirectory.mockResolvedValueOnce(warningActionResult('directory created with persistence warning'))

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('在此处新建文件夹')).toBeTruthy()
    })

    await user.click(screen.getByText('在此处新建文件夹'))
    await user.type(screen.getByPlaceholderText('新文件夹名称'), 'warn-folder')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: 'directory created with persistence warning',
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
    await user.type(screen.getByPlaceholderText('新文件夹名称'), 'docs')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '文件夹已存在，已同步更新',
        color: 'warning',
      })
    })

    await waitFor(() => {
      expect(screen.queryByDisplayValue('docs')).toBeFalsy()
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
    await user.type(screen.getByPlaceholderText('新文件夹名称'), 'docs')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '同名项目已存在',
        description: '当前目录中已存在同名文件或文件夹，请使用其他名称。',
        color: 'warning',
      })
    })

    expect(screen.getByDisplayValue('docs')).toBeTruthy()
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

    const toggleButtons = screen.getAllByRole('button')
    await user.click(toggleButtons.find((button) => button.className.includes('w-5 h-5')) ?? toggleButtons[0])

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '目录暂不可用',
        description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
        color: 'warning',
      })
    })
  })

  it('shows unavailable details when root directory is unavailable', async () => {
    mockListFiles.mockRejectedValueOnce(new ApiError('filesystem not initialized', 503, 'Service Unavailable', 'SERVICE_UNAVAILABLE'))

    renderPicker()

    await waitFor(() => {
      expect(screen.getByText('目录暂不可用')).toBeTruthy()
      expect(screen.getByText('文件系统当前不可用，请检查系统健康状态或稍后重试。')).toBeTruthy()
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
    await user.type(screen.getByPlaceholderText('新文件夹名称'), 'private')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '创建目录暂不可用',
        description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
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
      expect(screen.getByText('root unavailable')).toBeTruthy()
      expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
    })

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
        description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
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
      expect(mockListFiles).toHaveBeenCalledWith('/tester')
      expect(screen.getByText('主目录')).toBeTruthy()
      expect(screen.getByText('/tester/projects')).toBeTruthy()
      expect(screen.getByText('docs')).toBeTruthy()
    })
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
    await user.type(screen.getByPlaceholderText('新文件夹名称'), 'old')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(mockCreateDirectory).toHaveBeenCalledWith('/old')
    })

    await user.click(screen.getByRole('button', { name: '选择此目录' }))

    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByDisplayValue('old')).toBeTruthy()

    await act(async () => {
      pendingCreate.resolve(successActionResult)
      await pendingCreate.promise
    })

    await waitFor(() => {
      expect(screen.getByText('/old')).toBeTruthy()
      expect(screen.queryByDisplayValue('old')).toBeFalsy()
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
    await user.type(screen.getByPlaceholderText('新文件夹名称'), 'old')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      expect(mockCreateDirectory).toHaveBeenCalledWith('/old')
    })

    await user.click(screen.getByRole('button', { name: '选择此目录' }))

    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByDisplayValue('old')).toBeTruthy()

    await act(async () => {
      pendingCreate.reject(new Error('create failed'))
    })

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '创建文件夹失败',
        description: 'create failed',
        color: 'danger',
      })
    })

    expect(screen.getByDisplayValue('old')).toBeTruthy()
    expect(onClose).not.toHaveBeenCalled()
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

    const docsToggle = screen.getByText('docs').closest('div')?.querySelector('button')
    expect(docsToggle).toBeTruthy()
    await user.click(docsToggle as HTMLButtonElement)

    await waitFor(() => {
      expect(mockListFiles).toHaveBeenCalledWith('/docs')
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

    const reopenedDocsToggle = screen.getByText('docs').closest('div')?.querySelector('button')
    expect(reopenedDocsToggle).toBeTruthy()
    await user.click(reopenedDocsToggle as HTMLButtonElement)

    await waitFor(() => {
      expect(mockListFiles.mock.calls.filter(([calledPath]) => calledPath === '/docs')).toHaveLength(2)
      expect(screen.getByText('fresh-child')).toBeTruthy()
      expect(screen.queryByText('stale-child')).toBeNull()
    })
  })
})