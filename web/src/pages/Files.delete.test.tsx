import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor, within } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
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
    Button: react.forwardRef<HTMLButtonElement, React.ComponentProps<'button'> & { onPress?: () => void; isDisabled?: boolean; isLoading?: boolean; startContent?: React.ReactNode }>(
      ({ children, onPress, isDisabled, isLoading, startContent, autoFocus, ...props }, ref) => (
        <button ref={ref} {...props} data-auto-focus={autoFocus ? 'true' : undefined} disabled={isDisabled || isLoading} onClick={onPress}>{startContent}{children}</button>
      ),
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
  MAX_DELETE_INTENT_TARGETS: 1000,
  listFiles: vi.fn(),
  createFileDeleteIntent: vi.fn(),
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

import { listFiles, createFileDeleteIntent, deleteFile } from '@/api/files'
import { listShares } from '@/api/share'

const mockListFiles = vi.mocked(listFiles)
const mockCreateFileDeleteIntent = vi.mocked(createFileDeleteIntent)
const mockDeleteFile = vi.mocked(deleteFile)
const mockListShares = vi.mocked(listShares)
const photoDeleteIdentityToken = '5'.repeat(64)
const documentsDeleteIdentityToken = '6'.repeat(64)

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

async function clickPhotoAction(user: ReturnType<typeof userEvent.setup>, actionName: string | RegExp) {
  await user.click(screen.getByLabelText('photo.jpg 操作菜单'))
  const actionArea = await screen.findByRole('group', { name: 'photo.jpg 操作控制' })
  const action = within(actionArea).getByRole('button', { name: actionName })
  await user.click(action)
  return action
}

describe('FilesPage single delete modal', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockDeleteFile.mockReset()
    mockDeleteFile.mockResolvedValue({ warning: false, message: undefined })
    mockCreateFileDeleteIntent.mockReset()
    mockCreateFileDeleteIntent.mockImplementation(async ([target]) => ({
      deleteMode: 'trash',
      deletePolicyToken: '1'.repeat(64),
      trashRetentionDays: 30,
      trashAutoCleanupEnabled: true,
      targets: [{
        path: target!.path,
        name: target!.path.slice(target!.path.lastIndexOf('/') + 1),
        isDir: false,
        size: 1024,
        modTime: '2024-01-02T00:00:00Z',
        deleteIdentityToken: target!.observedIdentityToken,
        deleteTargetToken: '3'.repeat(64),
      }],
    }))
    useCanWriteMock.mockReturnValue(true)
    mockListShares.mockResolvedValue([])
    mockListFiles.mockResolvedValue({
      files: [
        { name: 'documents', path: '/documents', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z', deleteIdentityToken: documentsDeleteIdentityToken },
        { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024, modTime: '2024-01-02T00:00:00Z', deleteIdentityToken: photoDeleteIdentityToken },
      ],
      path: '/',
      deleteMode: 'trash',
      deletePolicyToken: '1'.repeat(64),
      trashRetentionDays: 30,
      trashAutoCleanupEnabled: true,
    })
  })

  it('keeps the single-file delete modal open when deletion fails', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockDeleteFile.mockRejectedValue(new Error('delete failed'))

    render(<FilesPage />)

    await waitFor(() => {
      expect(screen.getByText('photo.jpg')).toBeTruthy()
    })

    await clickPhotoAction(user, '删除')

    expect(mockCreateFileDeleteIntent).toHaveBeenCalledWith([{
      path: '/photo.jpg',
      observedIdentityToken: photoDeleteIdentityToken,
    }], expect.objectContaining({ signal: expect.any(AbortSignal) }))

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '移入回收站' })).toBeTruthy()
      expect(screen.getAllByText((_, element) => {
        const text = element?.textContent?.replace(/\s+/g, ' ').trim()
        return text?.includes('确定要将 photo.jpg 移入回收站吗？') ?? false
      }).length).toBeGreaterThan(0)
    })

    await user.click(screen.getByRole('button', { name: '移入回收站 photo.jpg' }))

    await waitFor(() => {
      expect(mockDeleteFile).toHaveBeenCalledWith('/photo.jpg', expect.objectContaining({
        expectedDeleteMode: 'trash',
        expectedDeletePolicyToken: '1'.repeat(64),
        expectedDeleteTargetToken: '3'.repeat(64),
      }))
    })

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '移入回收站' })).toBeTruthy()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '删除失败',
        description: '操作未完成，请稍后重试。',
        color: 'danger',
      })
    })
  })

  it('shows preparation state and suppresses duplicate single-target intent requests', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const pendingIntent = createDeferred<Awaited<ReturnType<typeof createFileDeleteIntent>>>()
    mockCreateFileDeleteIntent.mockImplementationOnce(() => pendingIntent.promise)

    render(<FilesPage />)
    await screen.findByText('photo.jpg')
    const deleteTrigger = await clickPhotoAction(user, '删除')

    expect(await screen.findByRole('status')).toHaveTextContent('正在确认删除目标…')
    const actionArea = await screen.findByRole('group', { name: 'photo.jpg 操作控制' })
    const deleteButton = within(actionArea).getByRole('button', { name: '删除' })
    expect(deleteButton).toBeDisabled()
    await user.click(deleteButton)
    expect(mockCreateFileDeleteIntent).toHaveBeenCalledTimes(1)

    await act(async () => {
      pendingIntent.resolve({
        deleteMode: 'trash',
        deletePolicyToken: '1'.repeat(64),
        trashRetentionDays: 30,
        trashAutoCleanupEnabled: true,
        targets: [{
          path: '/photo.jpg',
          name: 'photo.jpg',
          isDir: false,
          size: 1024,
          modTime: '2024-01-02T00:00:00Z',
          deleteIdentityToken: photoDeleteIdentityToken,
          deleteTargetToken: '3'.repeat(64),
        }],
      })
      await pendingIntent.promise
    })

    expect(await screen.findByRole('heading', { name: '移入回收站' })).toBeTruthy()
    expect(screen.queryByRole('status')).toBeNull()
    const cancelButton = screen.getByRole('button', { name: '取消' })
    await waitFor(() => expect(cancelButton).toHaveFocus())

    await user.click(cancelButton)
    await waitFor(() => {
      expect(screen.queryByRole('heading', { name: '移入回收站' })).toBeNull()
      expect(deleteTrigger).toHaveFocus()
    })
  })

  it('discards a single-target intent when identity changes despite matching path and metadata', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const pendingIntent = createDeferred<Awaited<ReturnType<typeof createFileDeleteIntent>>>()
    mockCreateFileDeleteIntent.mockImplementationOnce(() => pendingIntent.promise)

    render(<FilesPage />)
    await screen.findByText('photo.jpg')
    await clickPhotoAction(user, '删除')
    expect(await screen.findByRole('status')).toHaveTextContent('正在确认删除目标…')

    await act(async () => {
      pendingIntent.resolve({
        deleteMode: 'trash',
        deletePolicyToken: '1'.repeat(64),
        trashRetentionDays: 30,
        trashAutoCleanupEnabled: true,
        targets: [{
          path: '/photo.jpg',
          name: 'photo.jpg',
          isDir: false,
          size: 1024,
          modTime: '2024-01-02T00:00:00Z',
          deleteIdentityToken: '7'.repeat(64),
          deleteTargetToken: '3'.repeat(64),
        }],
      })
      await pendingIntent.promise
    })

    await waitFor(() => {
      expect(screen.queryByRole('status')).toBeNull()
      expect(screen.queryByRole('heading', { name: '移入回收站' })).toBeNull()
      expect(mockListFiles).toHaveBeenCalledTimes(2)
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '目标已变化，请重新选择/确认',
        color: 'warning',
      })
    })
    expect(mockDeleteFile).not.toHaveBeenCalled()
  })

  it('invalidates trash cache after a confirmed move to trash', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries')

    render(
      <QueryClientProvider client={queryClient}>
        <FilesPage />
      </QueryClientProvider>,
    )
    await screen.findByText('photo.jpg')
    await clickPhotoAction(user, '删除')
    await user.click(await screen.findByRole('button', { name: '移入回收站 photo.jpg' }))

    await waitFor(() => {
      expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['trash'] })
    })
  })

  it('closes a stale confirmation without removing cache on target drift', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockDeleteFile.mockRejectedValue({
      status: 409,
      code: 'DELETE_TARGET_CHANGED',
      message: 'delete target changed',
    })

    render(<FilesPage />)
    await screen.findByText('photo.jpg')
    await clickPhotoAction(user, '删除')
    await user.click(await screen.findByRole('button', { name: '移入回收站 photo.jpg' }))

    await waitFor(() => {
      expect(screen.queryByRole('heading', { name: '移入回收站' })).toBeNull()
      expect(screen.getByText('photo.jpg')).toBeTruthy()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '删除目标已更改，文件未删除',
        description: '列表已刷新，请重新确认删除目标。',
        color: 'warning',
      })
    })
    expect(mockDeleteFile).toHaveBeenCalledWith('/photo.jpg', expect.objectContaining({
      expectedDeleteTargetToken: '3'.repeat(64),
    }))
  })

  it('refreshes the list when identity changes while creating the delete intent', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockCreateFileDeleteIntent.mockRejectedValueOnce({
      status: 409,
      code: 'DELETE_TARGET_CHANGED',
      message: 'delete target changed',
    })

    render(<FilesPage />)
    await screen.findByText('photo.jpg')
    await clickPhotoAction(user, '删除')

    await waitFor(() => {
      expect(mockListFiles).toHaveBeenCalledTimes(2)
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '删除目标已更改，文件未删除',
        description: '列表已刷新，请重新确认删除目标。',
        color: 'warning',
      })
    })
    expect(screen.queryByRole('heading', { name: '移入回收站' })).toBeNull()
    expect(mockDeleteFile).not.toHaveBeenCalled()
  })

  it('shows permanent-delete consequences and submits the permanent mode', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockListFiles.mockResolvedValue({
      files: [
        { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024, modTime: '2024-01-02T00:00:00Z', deleteIdentityToken: photoDeleteIdentityToken },
      ],
      path: '/',
      deleteMode: 'permanent',
      deletePolicyToken: '2'.repeat(64),
      trashRetentionDays: 30,
      trashAutoCleanupEnabled: true,
    })
    mockCreateFileDeleteIntent.mockImplementationOnce(async ([target]) => ({
      deleteMode: 'permanent',
      deletePolicyToken: '2'.repeat(64),
      trashRetentionDays: 30,
      trashAutoCleanupEnabled: true,
      targets: [{
        path: target!.path,
        name: 'photo.jpg',
        isDir: false,
        size: 1024,
        modTime: '2024-01-02T00:00:00Z',
        deleteIdentityToken: target!.observedIdentityToken,
        deleteTargetToken: '4'.repeat(64),
      }],
    }))

    render(<FilesPage />)
    await screen.findByText('photo.jpg')
    await clickPhotoAction(user, '删除')

    expect(await screen.findByRole('heading', { name: '永久删除' })).toBeTruthy()
    expect(screen.getByText('文件不会进入回收站，删除后无法恢复。此操作无法撤销。')).toBeTruthy()
    await user.click(screen.getByRole('button', { name: '永久删除 photo.jpg' }))

    await waitFor(() => {
      expect(mockDeleteFile).toHaveBeenCalledWith('/photo.jpg', expect.objectContaining({
        expectedDeleteMode: 'permanent',
        expectedDeletePolicyToken: '2'.repeat(64),
        expectedDeleteTargetToken: '4'.repeat(64),
        signal: expect.any(AbortSignal),
      }))
    })
  })

  it('keeps browsing available but removes item delete entries when policy is unknown', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockListFiles.mockResolvedValue({
      files: [
        { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024, modTime: '2024-01-02T00:00:00Z', deleteIdentityToken: photoDeleteIdentityToken },
      ],
      path: '/',
      deleteMode: 'unknown',
      deletePolicyToken: null,
      trashRetentionDays: null,
      trashAutoCleanupEnabled: null,
    })

    render(<FilesPage />)

    expect(await screen.findByText('photo.jpg')).toBeTruthy()
    expect(screen.getByRole('alert')).toHaveTextContent('无法确认当前删除策略。为避免文件被永久删除，删除操作已停用。')
    await user.click(screen.getByLabelText('photo.jpg 操作菜单'))
    const actionArea = await screen.findByRole('group', { name: 'photo.jpg 操作控制' })
    expect(within(actionArea).getByRole('button', { name: '重命名' })).toBeTruthy()
    expect(within(actionArea).queryByRole('button', { name: '删除' })).toBeNull()
    expect(mockDeleteFile).not.toHaveBeenCalled()
  })

  it('keeps browsing available but disables delete when the item identity is unavailable', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockListFiles.mockResolvedValue({
      files: [
        { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024, modTime: '2024-01-02T00:00:00Z', deleteIdentityToken: null },
      ],
      path: '/',
      deleteMode: 'trash',
      deletePolicyToken: '1'.repeat(64),
      trashRetentionDays: 30,
      trashAutoCleanupEnabled: true,
    })

    render(<FilesPage />)

    expect(await screen.findByText('photo.jpg')).toBeTruthy()
    await user.click(screen.getByLabelText('photo.jpg 操作菜单'))
    const actionArea = await screen.findByRole('group', { name: 'photo.jpg 操作控制' })
    expect(within(actionArea).getByRole('button', { name: '重命名' })).toBeTruthy()
    expect(within(actionArea).queryByRole('button', { name: '删除' })).toBeNull()
    expect(mockCreateFileDeleteIntent).not.toHaveBeenCalled()
  })

  it('keeps the dialog policy snapshot when live query data changes before confirmation', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: 0 } },
    })

    render(
      <QueryClientProvider client={queryClient}>
        <FilesPage />
      </QueryClientProvider>
    )
    await screen.findByText('photo.jpg')
    await clickPhotoAction(user, '删除')
    expect(await screen.findByRole('heading', { name: '移入回收站' })).toBeTruthy()

    act(() => {
      queryClient.setQueryData(['files', 'u1:admin:/', '/'], {
        files: [
          { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024, modTime: '2024-01-02T00:00:00Z', deleteIdentityToken: photoDeleteIdentityToken },
        ],
        path: '/',
        deleteMode: 'permanent',
        deletePolicyToken: '2'.repeat(64),
        trashRetentionDays: 30,
        trashAutoCleanupEnabled: true,
      })
    })

    expect(screen.getByRole('heading', { name: '移入回收站' })).toBeTruthy()
    await user.click(screen.getByRole('button', { name: '移入回收站 photo.jpg' }))

    await waitFor(() => {
      expect(mockDeleteFile).toHaveBeenCalledWith('/photo.jpg', expect.objectContaining({
        expectedDeleteMode: 'trash',
        expectedDeletePolicyToken: '1'.repeat(64),
        expectedDeleteTargetToken: '3'.repeat(64),
      }))
    })
  })

  it('destroys a stale intent when the same trash mode receives a new policy token', async () => {
    const user = userEvent.setup({ writeToClipboard: false })
    mockDeleteFile.mockRejectedValue({
      status: 409,
      code: 'DELETE_POLICY_CHANGED',
      message: 'delete policy changed',
    })

    render(<FilesPage />)
    await screen.findByText('photo.jpg')
    await clickPhotoAction(user, '删除')

    mockListFiles.mockResolvedValue({
      files: [
        { name: 'documents', path: '/documents', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z', deleteIdentityToken: documentsDeleteIdentityToken },
        { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024, modTime: '2024-01-02T00:00:00Z', deleteIdentityToken: photoDeleteIdentityToken },
      ],
      path: '/',
      deleteMode: 'trash',
      deletePolicyToken: '2'.repeat(64),
      trashRetentionDays: 0,
      trashAutoCleanupEnabled: true,
    })
    mockCreateFileDeleteIntent.mockImplementation(async ([target]) => ({
      deleteMode: 'trash',
      deletePolicyToken: '2'.repeat(64),
      trashRetentionDays: 0,
      trashAutoCleanupEnabled: true,
      targets: [{
        path: target!.path,
        name: 'photo.jpg',
        isDir: false,
        size: 1024,
        modTime: '2024-01-02T00:00:00Z',
        deleteIdentityToken: target!.observedIdentityToken,
        deleteTargetToken: '4'.repeat(64),
      }],
    }))
    await user.click(screen.getByRole('button', { name: '移入回收站 photo.jpg' }))

    await waitFor(() => {
      expect(screen.queryByRole('heading', { name: '移入回收站' })).toBeNull()
      expect(screen.getByText('photo.jpg')).toBeTruthy()
      expect(mockListFiles.mock.calls.length).toBeGreaterThanOrEqual(2)
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '删除策略已更改，文件未删除',
        description: '列表已刷新，请按当前删除策略重新确认。',
        color: 'warning',
      })
    })

    expect(screen.queryByRole('alert')).toBeNull()
    await user.click(screen.getByLabelText('photo.jpg 操作菜单'))
    await user.click(screen.getByLabelText('photo.jpg 操作菜单'))
    const refreshedActionArea = await screen.findByRole('group', { name: 'photo.jpg 操作控制' })
    await user.click(within(refreshedActionArea).getByRole('button', { name: '删除' }))
    expect(await screen.findByText('当前策略下，新删除项目会立即到期并等待后台清理；回收站容量不足时也可能提前清理。')).toBeTruthy()
  })
})
