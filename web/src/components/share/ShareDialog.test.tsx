import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ShareDialog } from './ShareDialog'

const mockAddToast = vi.fn()

// Mock HeroUI components
vi.mock('@heroui/react', () => ({
  Modal: ({ children, isOpen }: { children: React.ReactNode; isOpen: boolean }) =>
    isOpen ? <div data-testid="modal">{children}</div> : null,
  ModalContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  ModalHeader: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  ModalBody: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  ModalFooter: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Button: ({
    children,
    onPress,
    isDisabled,
  }: {
    children: React.ReactNode
    onPress?: () => void
    isDisabled?: boolean
  }) => (
    <button onClick={onPress} disabled={isDisabled}>{children}</button>
  ),
  Input: ({
    label,
    placeholder,
    value,
    onValueChange,
    type,
    errorMessage,
  }: {
    label?: string
    placeholder?: string
    value?: string
    onValueChange?: (value: string) => void
    type?: string
    errorMessage?: string
  }) => (
    <div>
      <input
        aria-label={label ?? placeholder}
        placeholder={placeholder}
        type={type}
        value={value ?? ''}
        onChange={(event) => onValueChange?.(event.target.value)}
      />
      {errorMessage ? <span>{errorMessage}</span> : null}
    </div>
  ),
  Select: ({
    children,
    onSelectionChange,
  }: {
    children: React.ReactNode
    onSelectionChange?: (keys: Set<string>) => void
  }) => {
    const mapLabelToValue = (label: string) => {
      if (label === '永不过期') return ''
      if (label === '1 小时') return '1h'
      if (label === '24 小时') return '24h'
      if (label === '7 天') return '7d'
      if (label === '30 天') return '30d'
      if (label === '90 天') return '90d'
      if (label === '仅查看') return 'read'
      return label
    }
    return (
      <select
        onChange={(event) => onSelectionChange?.(new Set([mapLabelToValue(event.target.value)]))}
      >
        {children}
      </select>
    )
  },
  SelectItem: ({ children }: { children: React.ReactNode }) => <option>{children}</option>,
  Switch: ({
    isSelected,
    onValueChange,
  }: {
    isSelected?: boolean
    onValueChange?: (value: boolean) => void
  }) => (
    <input
      type="checkbox"
      checked={isSelected ?? false}
      onChange={(event) => onValueChange?.(event.target.checked)}
    />
  ),
  Snippet: ({ children }: { children: React.ReactNode }) => <code>{children}</code>,
  addToast: (...args: unknown[]) => mockAddToast(...args),
}))

// Mock share API
vi.mock('@/api/share', async () => {
  const actual = await vi.importActual<typeof import('@/api/share')>('@/api/share')
  return {
    ...actual,
    createShare: vi.fn(),
    copyShareUrl: vi.fn(),
  }
})

import { copyShareUrl, createShare, ShareError } from '@/api/share'

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('ShareDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders when open', () => {
    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
        isFolder={false}
      />
    )

    expect(screen.getByTestId('modal')).toBeInTheDocument()
    expect(screen.getByText('分享 文件')).toBeInTheDocument()
    expect(screen.getByText('/test/file.txt')).toBeInTheDocument()
  })

  it('does not render when closed', () => {
    render(
      <ShareDialog
        isOpen={false}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    expect(screen.queryByTestId('modal')).not.toBeInTheDocument()
  })

  it('shows folder text when isFolder is true', () => {
    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/folder"
        isFolder={true}
      />
    )

    expect(screen.getByText('分享 文件夹')).toBeInTheDocument()
  })

  it('has password protection toggle', () => {
    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    expect(screen.getByText('密码保护')).toBeInTheDocument()
  })

  it('has expiration options', () => {
    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    expect(screen.getByText('有效期')).toBeInTheDocument()
  })

  it('renders the access limit section without crashing', () => {
    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    expect(screen.getByText('访问次数限制')).toBeInTheDocument()
    expect(screen.getByPlaceholderText('不限制')).toBeInTheDocument()
  })

  it('shows create button', () => {
    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    expect(screen.getByText('创建分享链接')).toBeInTheDocument()
  })

  it('closes and resets the form when not loading', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()

    render(
      <ShareDialog
        isOpen={true}
        onClose={onClose}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByRole('checkbox'))
    expect(screen.getByPlaceholderText('设置访问密码')).toBeInTheDocument()

    await user.click(screen.getByText('取消'))

    expect(onClose).toHaveBeenCalled()
  })

  it('shows a disabled state when share creation reports the feature is off', async () => {
    const user = userEvent.setup()
    vi.mocked(createShare).mockRejectedValue(new ShareError('share feature disabled', 503, 'SHARE_FEATURE_DISABLED'))

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByText('创建分享链接'))

    expect(await screen.findByText('分享功能已关闭')).toBeInTheDocument()
    expect(screen.getByText('当前服务已关闭分享功能。重新启用后，才能为文件或文件夹创建分享链接。')).toBeInTheDocument()
  })

  it('notifies the parent when share creation reports the feature is off', async () => {
    const user = userEvent.setup()
    const onFeatureDisabled = vi.fn()
    vi.mocked(createShare).mockRejectedValue(new ShareError('share feature disabled', 503, 'SHARE_FEATURE_DISABLED'))

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
        onFeatureDisabled={onFeatureDisabled}
      />
    )

    await user.click(screen.getByText('创建分享链接'))

    expect(onFeatureDisabled).toHaveBeenCalled()
  })

  it('shows unavailable toast when share creation is temporarily unavailable', async () => {
    const user = userEvent.setup()
    vi.mocked(createShare).mockRejectedValue(new ShareError('share service unavailable', 503))

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByText('创建分享链接'))

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '创建分享暂不可用',
      description: '分享服务当前不可用，请检查系统健康状态或稍后重试。',
      color: 'warning',
    })
  })

  it('shows generic fallback details when share creation throws an unknown value', async () => {
    const user = userEvent.setup()
    vi.mocked(createShare).mockRejectedValue('share failed')

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByText('创建分享链接'))

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '创建分享失败',
      description: '请稍后重试',
      color: 'danger',
    })
  })

  it('shows a stale-target warning when share creation target no longer exists', async () => {
    const user = userEvent.setup()
    vi.mocked(createShare).mockRejectedValue(new ShareError('file not found', 404, 'FILE_NOT_FOUND'))

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByText('创建分享链接'))

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '分享目标已不存在',
      description: '该文件或文件夹可能已被移动或删除，请刷新列表后重试。',
      color: 'warning',
    })
  })

  it('blocks creating a protected share when password is empty', async () => {
    const user = userEvent.setup()

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByRole('checkbox'))

    const createButton = screen.getByText('创建分享链接')
    expect(createButton).toBeDisabled()
    expect(screen.getByText('启用密码保护后必须输入密码')).toBeInTheDocument()

    await user.click(createButton)

    expect(createShare).not.toHaveBeenCalled()
  })

  it('allows creating a protected share after entering a password', async () => {
    const user = userEvent.setup()
    vi.mocked(createShare).mockResolvedValue({
      id: 'share-1',
      path: '/test/file.txt',
      type: 'file',
      created_by: 'user-1',
      created_at: new Date().toISOString(),
      has_password: true,
      permission: 'read',
      enabled: true,
      access_count: 0,
      max_access: 0,
      description: '',
      url: '/s/share-1',
      warning: false,
      message: undefined,
    } as never)

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByRole('checkbox'))
    await user.type(screen.getByPlaceholderText('设置访问密码'), 'secret-123')

    const createButton = screen.getByText('创建分享链接')
    expect(createButton).not.toBeDisabled()

    await user.click(createButton)

    expect(createShare).toHaveBeenCalledWith(
      expect.objectContaining({
        path: '/test/file.txt',
        type: 'file',
        password: 'secret-123',
      })
    )
  })

  it('sends optional share settings when configured', async () => {
    const user = userEvent.setup()
    vi.mocked(createShare).mockResolvedValue({
      id: 'share-1',
      path: '/test/file.txt',
      type: 'file',
      created_by: 'user-1',
      created_at: new Date().toISOString(),
      has_password: false,
      permission: 'read',
      enabled: true,
      access_count: 0,
      max_access: 12,
      description: 'release package',
      url: '/s/share-1',
      warning: false,
      message: undefined,
    } as never)

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    fireEvent.change(screen.getAllByRole('combobox')[0], { target: { value: '7 天' } })
    fireEvent.change(screen.getAllByRole('combobox')[1], { target: { value: '仅查看' } })
    await user.type(screen.getByPlaceholderText('不限制'), '12')
    await user.type(screen.getByPlaceholderText('添加备注信息'), '  release package  ')
    await user.click(screen.getByText('创建分享链接'))

    expect(createShare).toHaveBeenCalledWith(expect.objectContaining({
      expires_in: '7d',
      max_access: 12,
      description: 'release package',
    }))
  })

  it('ignores non-positive access limits when creating a share', async () => {
    const user = userEvent.setup()
    vi.mocked(createShare).mockResolvedValue({
      id: 'share-1',
      path: '/test/file.txt',
      type: 'file',
      created_by: 'user-1',
      created_at: new Date().toISOString(),
      has_password: false,
      permission: 'read',
      enabled: true,
      access_count: 0,
      url: '/s/share-1',
      warning: false,
      message: undefined,
    } as never)

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.type(screen.getByPlaceholderText('不限制'), '0')
    await user.click(screen.getByText('创建分享链接'))

    expect(createShare).toHaveBeenCalledWith(expect.not.objectContaining({
      max_access: expect.any(Number),
    }))
  })

  it('calls the created callback and supports copying the created share link', async () => {
    const user = userEvent.setup()
    const onShareCreated = vi.fn()
    const createdShare = {
      id: 'share-1',
      path: '/test/file.txt',
      type: 'file' as const,
      created_by: 'user-1',
      created_at: new Date().toISOString(),
      has_password: true,
      permission: 'read' as const,
      enabled: true,
      access_count: 0,
      max_access: 0,
      description: '',
      url: '/s/share-1',
      warning: false,
      message: undefined,
    }
    vi.mocked(createShare).mockResolvedValue(createdShare)
    vi.mocked(copyShareUrl).mockResolvedValue()

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
        onShareCreated={onShareCreated}
      />
    )

    await user.click(screen.getByText('创建分享链接'))
    await screen.findByText('此链接需要密码才能访问')
    await user.click(screen.getByText('复制链接'))

    expect(onShareCreated).toHaveBeenCalledWith(createdShare)
    expect(copyShareUrl).toHaveBeenCalledWith(createdShare)
    expect(mockAddToast).toHaveBeenCalledWith({ title: '链接已复制', color: 'success' })
  })

  it('uses absolute share URLs without adding the current origin', async () => {
    const user = userEvent.setup()
    vi.mocked(createShare).mockResolvedValue({
      id: 'share-absolute',
      path: '/test/file.txt',
      type: 'file',
      created_by: 'user-1',
      created_at: new Date().toISOString(),
      has_password: false,
      permission: 'read',
      enabled: true,
      access_count: 0,
      max_access: 0,
      description: '',
      url: 'https://shares.example.test/s/share-absolute',
      warning: false,
      message: undefined,
    } as never)

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByText('创建分享链接'))

    expect(await screen.findByText('https://shares.example.test/s/share-absolute')).toBeInTheDocument()
  })

  it('shows a toast when copying the created link fails', async () => {
    const user = userEvent.setup()
    vi.mocked(createShare).mockResolvedValue({
      id: 'share-1',
      path: '/test/file.txt',
      type: 'file',
      created_by: 'user-1',
      created_at: new Date().toISOString(),
      has_password: false,
      permission: 'read',
      enabled: true,
      access_count: 0,
      url: '/s/share-1',
      warning: false,
      message: undefined,
    } as never)
    vi.mocked(copyShareUrl).mockRejectedValue(new Error('clipboard unavailable'))

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByText('创建分享链接'))
    await user.click(await screen.findByText('复制链接'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({ title: '复制失败', color: 'danger' })
    })
  })

  it('shows warning toast when share creation succeeds with backend warning metadata', async () => {
    const user = userEvent.setup()
    vi.mocked(createShare).mockResolvedValue({
      id: 'share-1',
      path: '/test/file.txt',
      type: 'file',
      created_by: 'user-1',
      created_at: new Date().toISOString(),
      has_password: false,
      permission: 'read',
      enabled: true,
      access_count: 0,
      max_access: 0,
      description: '',
      url: '/s/share-1',
      warning: true,
      message: 'share created with audit warning',
    } as never)

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByText('创建分享链接'))

    expect(mockAddToast).toHaveBeenCalledWith({
      title: 'share created with audit warning',
      color: 'warning',
    })
    expect(screen.getByText('分享链接已创建，但存在警告')).toBeInTheDocument()
    expect(screen.getByText('share created with audit warning')).toBeInTheDocument()
    expect(screen.getByText('http://localhost:3000/s/share-1')).toBeInTheDocument()
  })

  it('uses a fallback warning title when warning metadata has no message', async () => {
    const user = userEvent.setup()
    vi.mocked(createShare).mockResolvedValue({
      id: 'share-1',
      path: '/test/file.txt',
      type: 'file',
      created_by: 'user-1',
      created_at: new Date().toISOString(),
      has_password: false,
      permission: 'read',
      enabled: true,
      access_count: 0,
      max_access: 0,
      description: '',
      url: '/s/share-1',
      warning: true,
      message: undefined,
    } as never)

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByText('创建分享链接'))

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '分享链接已创建，但存在警告',
      color: 'warning',
    })
    expect(await screen.findByText('分享链接已创建，但存在警告')).toBeInTheDocument()
  })

  it('does not show an older created share after the dialog target changes', async () => {
    const user = userEvent.setup()
    const pendingShare = createDeferred<Awaited<ReturnType<typeof createShare>>>()
    vi.mocked(createShare).mockImplementationOnce(() => pendingShare.promise as ReturnType<typeof createShare>)

    const { rerender } = render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/old/file.txt"
      />
    )

    await user.click(screen.getByText('创建分享链接'))

    rerender(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/new/file.txt"
      />
    )

    await act(async () => {
      pendingShare.resolve({
        id: 'share-old',
        path: '/old/file.txt',
        type: 'file',
        created_by: 'user-1',
        created_at: new Date().toISOString(),
        has_password: false,
        permission: 'read',
        enabled: true,
        access_count: 0,
        max_access: 0,
        description: '',
        url: '/s/share-old',
      } as never)
      await pendingShare.promise
    })

    expect(screen.getByText('/new/file.txt')).toBeInTheDocument()
    expect(screen.queryByText('http://localhost:3000/s/share-old')).not.toBeInTheDocument()
  })

  it('keeps the dialog open while a pending create request is in flight', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    const pendingShare = createDeferred<Awaited<ReturnType<typeof createShare>>>()
    vi.mocked(createShare).mockImplementationOnce(() => pendingShare.promise as ReturnType<typeof createShare>)

    render(
      <ShareDialog
        isOpen={true}
        onClose={onClose}
        filePath="/test/file.txt"
      />,
    )

    await user.click(screen.getByText('创建分享链接'))

    expect(createShare).toHaveBeenCalledWith(
      expect.objectContaining({
        path: '/test/file.txt',
        type: 'file',
      }),
    )

    await user.click(screen.getByText('取消'))
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByText('/test/file.txt')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /创建分享链接/ })).toBeInTheDocument()

    await act(async () => {
      pendingShare.resolve({
        id: 'share-1',
        path: '/test/file.txt',
        type: 'file',
        created_by: 'user-1',
        created_at: new Date().toISOString(),
        has_password: false,
        permission: 'read',
        enabled: true,
        access_count: 0,
        max_access: 0,
        description: '',
        url: '/s/share-1',
      } as never)
      await pendingShare.promise
    })

    expect(screen.getByText('/test/file.txt')).toBeInTheDocument()
    expect(screen.getByText('分享链接已创建')).toBeInTheDocument()
    expect(onClose).not.toHaveBeenCalled()
  })

  it('keeps the dialog open when a pending create request later fails', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    const pendingShare = createDeferred<Awaited<ReturnType<typeof createShare>>>()
    vi.mocked(createShare).mockImplementationOnce(() => pendingShare.promise as ReturnType<typeof createShare>)

    render(
      <ShareDialog
        isOpen={true}
        onClose={onClose}
        filePath="/test/file.txt"
      />,
    )

    await user.click(screen.getByText('创建分享链接'))

    expect(createShare).toHaveBeenCalledWith(
      expect.objectContaining({
        path: '/test/file.txt',
        type: 'file',
      }),
    )

    await user.click(screen.getByText('取消'))
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByText('/test/file.txt')).toBeInTheDocument()

    await act(async () => {
      pendingShare.reject(new Error('create failed'))
    })

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '创建分享失败',
      description: 'create failed',
      color: 'danger',
    })
    expect(screen.getByText('/test/file.txt')).toBeInTheDocument()
    expect(onClose).not.toHaveBeenCalled()
  })
})
