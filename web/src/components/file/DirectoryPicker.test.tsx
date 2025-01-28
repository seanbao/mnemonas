import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { DirectoryPicker } from './DirectoryPicker'

const mockAddToast = vi.fn()

vi.mock('@heroui/react', async () => {
  const actual = await vi.importActual<typeof import('@heroui/react')>('@heroui/react')
  return {
    ...actual,
    addToast: (...args: unknown[]) => mockAddToast(...args),
  }
})

vi.mock('@/api/files', () => ({
  listFiles: vi.fn(),
  createDirectory: vi.fn(),
}))

import { listFiles, createDirectory } from '@/api/files'

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
})