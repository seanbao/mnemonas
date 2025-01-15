import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@/test/utils'
import React from 'react'
import { FilesPage } from './Files'

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
    Button: ({ children, onPress, isDisabled, isLoading, ...props }: { children: React.ReactNode; onPress?: () => void; isDisabled?: boolean; isLoading?: boolean }) => (
      <button disabled={isDisabled || isLoading} onClick={onPress} {...props}>{children}</button>
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
  ShareDialog: ({ isOpen, isFolder }: { isOpen: boolean; isFolder?: boolean }) => (
    <div data-testid="share-dialog" data-open={isOpen ? 'true' : 'false'} data-folder={isFolder ? 'true' : 'false'} />
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

import { listFiles } from '@/api/files'

const mockListFiles = vi.mocked(listFiles)

describe('FilesPage sharing behavior', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListFiles.mockResolvedValue({
      files: [
        { name: 'folder', path: '/folder', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
        { name: 'video.mp4', path: '/video.mp4', isDir: false, size: 1024, modTime: '2024-01-02T00:00:00Z' },
      ],
      path: '/',
    })
  })

  it('disables share action for folders in list view', async () => {
    render(<FilesPage />)

    const shareButtons = await screen.findAllByText('创建分享链接')
    expect(shareButtons.length).toBeGreaterThan(1)
    expect(shareButtons[0]).toBeDisabled()
  })

  it('opens share dialog for files with isFolder=false', async () => {
    render(<FilesPage />)

    const shareButtons = await screen.findAllByText('创建分享链接')
    shareButtons[1].click()

    const dialog = await screen.findByTestId('share-dialog')
    expect(dialog.getAttribute('data-open')).toBe('true')
    expect(dialog.getAttribute('data-folder')).toBe('false')
  })
})
