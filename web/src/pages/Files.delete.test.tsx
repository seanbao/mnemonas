import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
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
  const DropdownContext = react.createContext<{ open: boolean; setOpen: (open: boolean) => void } | null>(null)

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
    Dropdown: ({ children }: { children: React.ReactNode }) => {
      const [open, setOpen] = react.useState(false)
      return <DropdownContext.Provider value={{ open, setOpen }}>{children}</DropdownContext.Provider>
    },
    DropdownTrigger: ({ children }: { children: React.ReactNode }) => {
      const context = react.useContext(DropdownContext)
      if (!react.isValidElement(children)) {
        return <div>{children}</div>
      }
      return react.cloneElement(children, {
        onClick: (...args: unknown[]) => {
          children.props.onClick?.(...args)
          context?.setOpen(!context.open)
        },
      })
    },
    DropdownMenu: ({ children }: { children: React.ReactNode }) => {
      const context = react.useContext(DropdownContext)
      return context?.open ? <div>{children}</div> : null
    },
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
  downloadFile: vi.fn(),
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
    viewMode: 'grid' as const,
    sortBy: 'name' as const,
    sortOrder: 'asc' as const,
    setCurrentPath: vi.fn(),
    toggleFileSelection: vi.fn(),
    setSelection: vi.fn(),
    selectAll: vi.fn(),
    clearSelection: vi.fn(),
    setViewMode: vi.fn(),
  }),
}))

vi.mock('@/stores/clipboard', () => ({
  useClipboardStore: () => ({
    paths: [],
    operation: null,
    sourcePath: null,
    copy: vi.fn(),
    cut: vi.fn(),
    clear: vi.fn(),
    hasPaths: vi.fn(() => false),
  }),
}))

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useCanWrite: () => useCanWriteMock(),
    useUser: () => ({ id: 'u1', username: 'admin', role: 'admin', email: 'admin@local', homeDir: '/' }),
  }
})

import { listFiles, deleteFile } from '@/api/files'
import { listShares } from '@/api/share'

const mockListFiles = vi.mocked(listFiles)
const mockDeleteFile = vi.mocked(deleteFile)
const mockListShares = vi.mocked(listShares)

describe('FilesPage single delete modal', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useCanWriteMock.mockReturnValue(true)
    mockListShares.mockResolvedValue([])
    mockListFiles.mockResolvedValue({
      files: [
        { name: 'documents', path: '/documents', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
        { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024, modTime: '2024-01-02T00:00:00Z' },
      ],
      path: '/',
    })
  })

  it('keeps the single-file delete modal open when deletion fails', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockDeleteFile.mockRejectedValue(new Error('delete failed'))

    render(<FilesPage />)

    await waitFor(() => {
      expect(screen.getByText('photo.jpg')).toBeTruthy()
    })

    await user.click(screen.getByLabelText('photo.jpg 操作菜单'))
    await user.click(screen.getAllByText('删除')[0])

    await waitFor(() => {
      expect(screen.getByText('确认删除')).toBeTruthy()
      expect(screen.getAllByText((_, element) => {
        const text = element?.textContent?.replace(/\s+/g, ' ').trim()
        return text?.includes('确定要删除 photo.jpg 吗？') ?? false
      }).length).toBeGreaterThan(0)
    })

    const deleteButtons = screen.getAllByRole('button', { name: '删除' })
    await user.click(deleteButtons[deleteButtons.length - 1])

    await waitFor(() => {
      expect(mockDeleteFile).toHaveBeenCalledWith('/photo.jpg', expect.anything())
    })

    await waitFor(() => {
      expect(screen.getByText('确认删除')).toBeTruthy()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '删除失败',
        description: '操作未完成，请稍后重试。',
        color: 'danger',
      })
    })
  })
})
