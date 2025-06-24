import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@/test/utils'
import React from 'react'
import { act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { FilesPage } from './Files'

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
    addToast: vi.fn(),
  }
})

vi.mock('@/components/share', () => ({
  ShareDialog: ({ isOpen, isFolder, onFeatureDisabled }: { isOpen: boolean; isFolder?: boolean; onFeatureDisabled?: () => void }) => (
    <div data-testid="share-dialog" data-open={isOpen ? 'true' : 'false'} data-folder={isFolder ? 'true' : 'false'}>
      {isOpen && <button onClick={onFeatureDisabled}>disable share feature</button>}
    </div>
  ),
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

import { listFiles } from '@/api/files'
import { listShares, ShareError } from '@/api/share'

const mockListFiles = vi.mocked(listFiles)
const mockListShares = vi.mocked(listShares)

describe('FilesPage sharing behavior', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useCanWriteMock.mockReturnValue(true)
    mockListShares.mockResolvedValue([])
    mockListFiles.mockResolvedValue({
      files: [
        { name: 'folder', path: '/folder', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
        { name: 'video.mp4', path: '/video.mp4', isDir: false, size: 1024, modTime: '2024-01-02T00:00:00Z' },
      ],
      path: '/',
    })
  })

  it('opens share dialog for folders with isFolder=true', async () => {
    await act(async () => {
      render(<FilesPage />)
      await Promise.resolve()
    })

    const shareButtons = await screen.findAllByText('创建分享链接')
    expect(shareButtons.length).toBeGreaterThan(1)

    await act(async () => {
      shareButtons[0].click()
      await Promise.resolve()
    })

    const dialog = await screen.findByTestId('share-dialog')
    expect(dialog.getAttribute('data-open')).toBe('true')
    expect(dialog.getAttribute('data-folder')).toBe('true')
  })

  it('opens share dialog for files with isFolder=false', async () => {
    await act(async () => {
      render(<FilesPage />)
      await Promise.resolve()
    })

    const shareButtons = await screen.findAllByText('创建分享链接')
    await act(async () => {
      shareButtons[1].click()
      await Promise.resolve()
    })

    const dialog = await screen.findByTestId('share-dialog')
    expect(dialog.getAttribute('data-open')).toBe('true')
    expect(dialog.getAttribute('data-folder')).toBe('false')
  })

  it('disables share entry points after the dialog reports the feature is off', async () => {
    const user = userEvent.setup()

    await act(async () => {
      render(<FilesPage />)
      await Promise.resolve()
    })

    const shareButtons = await screen.findAllByText('创建分享链接')
    await act(async () => {
      shareButtons[0].click()
      await Promise.resolve()
    })

    await user.click(screen.getByText('disable share feature'))

    expect((await screen.findAllByText('分享功能已关闭')).length).toBeGreaterThan(0)
    expect(screen.getByText('当前服务已关闭分享功能。请在系统设置中重新启用后再创建分享链接。')).toBeInTheDocument()
  })

  it('disables share entry points when the initial share availability check reports the feature is off', async () => {
    mockListShares.mockRejectedValueOnce(new ShareError('share disabled', 503, 'SHARE_FEATURE_DISABLED'))

    await act(async () => {
      render(<FilesPage />)
      await Promise.resolve()
    })

    expect((await screen.findAllByText('分享功能已关闭')).length).toBeGreaterThan(0)
    expect(screen.getByText('当前服务已关闭分享功能。请在系统设置中重新启用后再创建分享链接。')).toBeInTheDocument()
  })

  it('disables share entry points with an unavailable label when the initial share availability check is temporarily unavailable', async () => {
    mockListShares.mockRejectedValueOnce(new ShareError('share unavailable', 503))

    await act(async () => {
      render(<FilesPage />)
      await Promise.resolve()
    })

    expect((await screen.findAllByText('分享功能暂不可用')).length).toBeGreaterThan(0)
    expect(screen.getByText('分享服务当前不可用，请检查系统健康状态或稍后重试。')).toBeInTheDocument()
  })
})
