import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
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

  return render(
    <QueryClientProvider client={queryClient}>
      <DirectoryPicker {...defaultProps} {...props} />
    </QueryClientProvider>
  )
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
    mockCreateDirectory.mockResolvedValue(undefined)
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
})