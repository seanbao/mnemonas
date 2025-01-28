import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
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
  moveFile: vi.fn(),
  copyFile: vi.fn(),
  listFiles: vi.fn(),
  createDirectory: vi.fn(),
}))

import { moveFile, copyFile, listFiles, createDirectory } from '@/api/files'

const mockMoveFile = vi.mocked(moveFile)
const mockCopyFile = vi.mocked(copyFile)
const mockListFiles = vi.mocked(listFiles)
const mockCreateDirectory = vi.mocked(createDirectory)

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

  return render(
    <QueryClientProvider client={queryClient}>
      <MoveDialog {...defaultProps} {...props} />
    </QueryClientProvider>
  )
}

describe('MoveDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListFiles.mockResolvedValue({ files: [], path: '/' })
    mockCreateDirectory.mockResolvedValue(undefined)
  })

  it('keeps only failed files visible after partial move failure', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const onClose = vi.fn()
    mockMoveFile
      .mockResolvedValueOnce(undefined)
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
})