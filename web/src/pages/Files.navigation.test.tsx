import React, { useEffect } from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { FilesPage } from './Files'
import { useFilesStore } from '@/stores/files'

const mockAddToast = vi.fn()

vi.mock('@heroui/react', async () => {
  const react = await vi.importActual<typeof import('react')>('react')
  const DropdownContext = react.createContext<{ open: boolean; setOpen: (open: boolean) => void } | null>(null)

  return {
    HeroUIProvider: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    Button: ({ children, onPress, isDisabled, isLoading, startContent }: { children: React.ReactNode; onPress?: () => void; isDisabled?: boolean; isLoading?: boolean; startContent?: React.ReactNode }) => (
      <button disabled={isDisabled || isLoading} onClick={onPress}>{startContent}{children}</button>
    ),
    Modal: ({ children, isOpen }: { children: React.ReactNode; isOpen: boolean }) => isOpen ? <div>{children}</div> : null,
    ModalContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    ModalHeader: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    ModalBody: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    ModalFooter: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    Input: (props: React.ComponentProps<'input'> & { onValueChange?: (value: string) => void }) => (
      <input {...props} onChange={(event) => props.onValueChange?.(event.target.value)} />
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
    useCanWrite: () => true,
    useUser: () => ({ id: 'u1', username: 'admin', role: 'admin', email: 'admin@local', homeDir: '/' }),
  }
})

import { listFiles } from '@/api/files'
import { listShares } from '@/api/share'

const mockListFiles = vi.mocked(listFiles)
const mockListShares = vi.mocked(listShares)

function expectListFilesCalledWithPath(path: string) {
  expect(mockListFiles.mock.calls.some(([calledPath]) => calledPath === path)).toBe(true)
}

function LocationProbe({ onPath }: { onPath: (path: string) => void }) {
  const location = useLocation()

  useEffect(() => {
    onPath(location.pathname)
  }, [location.pathname, onPath])

  return <output aria-label="当前路由">{location.pathname}</output>
}

function renderFilesRoute(initialPath: string, observedPaths: string[]) {
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

  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route
            path="/files/*"
            element={
              <>
                <LocationProbe onPath={(path) => observedPaths.push(path)} />
                <FilesPage />
              </>
            }
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('FilesPage navigation integration', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListShares.mockResolvedValue([])
    useFilesStore.setState({
      currentPath: '/',
      selectedFiles: new Set(),
      viewMode: 'grid',
      sortBy: 'name',
      sortOrder: 'asc',
    })
    mockListFiles.mockImplementation(async (path: string) => {
      if (path === '/documents') {
        return {
          path: '/documents',
          files: [
            { name: 'notes.txt', path: '/documents/notes.txt', isDir: false, size: 512, modTime: '2024-01-03T00:00:00Z' },
          ],
        }
      }

      return {
        path: '/',
        files: [
          { name: 'documents', path: '/documents', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
          { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024, modTime: '2024-01-02T00:00:00Z' },
        ],
      }
    })
  })

  it('keeps URL and file state stable after double-clicking a folder', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const observedPaths: string[] = []

    renderFilesRoute('/files', observedPaths)

    await user.dblClick(await screen.findByText('documents'))

    await waitFor(() => {
      expect(screen.getByLabelText('当前路由')).toHaveTextContent('/files/documents')
      expect(useFilesStore.getState().currentPath).toBe('/documents')
      expect(screen.getByText('notes.txt')).toBeTruthy()
    })

    const enteredFolderIndex = observedPaths.indexOf('/files/documents')
    expect(enteredFolderIndex).toBeGreaterThanOrEqual(0)
    expect(observedPaths.slice(enteredFolderIndex + 1)).not.toContain('/files')
  })

  it('uses the folder route as the source of truth before the store catches up', async () => {
    const observedPaths: string[] = []

    renderFilesRoute('/files/documents', observedPaths)

    await waitFor(() => {
      expect(screen.getByLabelText('当前路由')).toHaveTextContent('/files/documents')
      expect(useFilesStore.getState().currentPath).toBe('/documents')
      expect(screen.getByText('notes.txt')).toBeTruthy()
    })

    expect(observedPaths).not.toContain('/files')
    expectListFilesCalledWithPath('/documents')
  })
})
