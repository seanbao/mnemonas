import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen } from '@testing-library/react'
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
  Select: ({ children }: { children: React.ReactNode }) => <select>{children}</select>,
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

import { createShare, ShareError } from '@/api/share'

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

  it('keeps a reopened dialog focused on the new file when an older create request resolves', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    const pendingShare = createDeferred<Awaited<ReturnType<typeof createShare>>>()
    vi.mocked(createShare).mockImplementation((request) => {
      if (request.path === '/test/file.txt') {
        return pendingShare.promise as ReturnType<typeof createShare>
      }
      return Promise.resolve({
        id: 'share-2',
        path: '/test/other.txt',
        type: 'file',
        created_by: 'user-1',
        created_at: new Date().toISOString(),
        has_password: false,
        permission: 'read',
        enabled: true,
        access_count: 0,
        max_access: 0,
        description: '',
        url: '/s/share-2',
      } as never)
    })

    const view = render(
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
    expect(onClose).toHaveBeenCalledTimes(1)

    view.rerender(
      <ShareDialog
        isOpen={false}
        onClose={onClose}
        filePath="/test/file.txt"
      />,
    )

    view.rerender(
      <ShareDialog
        isOpen={true}
        onClose={onClose}
        filePath="/test/other.txt"
      />,
    )

    expect(screen.getByText('/test/other.txt')).toBeInTheDocument()
    expect(screen.getByText('创建分享链接')).toBeInTheDocument()

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

    expect(screen.getByText('/test/other.txt')).toBeInTheDocument()
    expect(screen.getByText('创建分享链接')).toBeInTheDocument()
    expect(screen.queryAllByText('分享链接已创建')).toHaveLength(0)
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})
