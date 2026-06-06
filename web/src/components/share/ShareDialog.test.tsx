import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ShareDialog } from './ShareDialog'

const mockAddToast = vi.fn()

// Mock HeroUI components
vi.mock('@heroui/react', () => ({
  Modal: ({ children, isOpen }: { children: React.ReactNode; isOpen: boolean }) =>
    isOpen ? <div role="dialog" aria-label="分享对话框">{children}</div> : null,
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
    'aria-label': ariaLabel,
    label,
    placeholder,
    value,
    onValueChange,
    type,
    errorMessage,
  }: {
    'aria-label'?: string
    label?: string
    placeholder?: string
    value?: string
    onValueChange?: (value: string) => void
    type?: string
    errorMessage?: string
  }) => (
    <div>
      <input
        aria-label={ariaLabel ?? label ?? placeholder}
        placeholder={placeholder}
        type={type}
        value={value ?? ''}
        onChange={(event) => onValueChange?.(event.target.value)}
      />
      {errorMessage ? <span>{errorMessage}</span> : null}
    </div>
  ),
  Select: ({
    'aria-label': ariaLabel,
    children,
    onSelectionChange,
  }: {
    'aria-label'?: string
    children: React.ReactNode
    onSelectionChange?: (keys: Set<string>) => void
  }) => {
    const mapLabelToValue = (label: string) => {
      if (label === '使用系统默认') return ''
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
        aria-label={ariaLabel}
        onChange={(event) => onSelectionChange?.(new Set([mapLabelToValue(event.target.value)]))}
      >
        {children}
      </select>
    )
  },
  SelectItem: ({ children }: { children: React.ReactNode }) => <option>{children}</option>,
  Switch: ({
    'aria-label': ariaLabel,
    isSelected,
    isDisabled,
    onValueChange,
  }: {
    'aria-label'?: string
    isSelected?: boolean
    isDisabled?: boolean
    onValueChange?: (value: boolean) => void
  }) => (
    <input
      aria-label={ariaLabel}
      type="checkbox"
      checked={isSelected ?? false}
      disabled={isDisabled}
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
    getSharePolicy: vi.fn(),
  }
})

vi.mock('@/api/activity', () => ({
  createActivityReviewRecord: vi.fn(),
  listActivity: vi.fn(),
}))

import { copyShareUrl, createShare, getSharePolicy, ShareError } from '@/api/share'
import * as activityApi from '@/api/activity'

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

function expectCreateShareCalledWithAbortSignal(request: unknown): AbortSignal {
  expect(createShare).toHaveBeenCalledWith(
    request,
    expect.objectContaining({ signal: expect.any(AbortSignal) }),
  )
  const call = vi.mocked(createShare).mock.calls.find(([calledRequest, options]) => {
    try {
      expect(calledRequest).toEqual(request)
      return (options as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
    } catch {
      return false
    }
  })
  expect(call).toBeTruthy()
  const [, options] = call as unknown as [unknown, { signal: AbortSignal }]
  expect(Object.keys(options)).toEqual(['signal'])
  return options.signal
}

describe('ShareDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(getSharePolicy).mockResolvedValue({
      default_expires_in: '168h',
      default_max_access: 0,
    })
    vi.mocked(activityApi.listActivity).mockResolvedValue({
      items: [],
      total: 0,
      limit: 100,
      offset: 0,
    })
    vi.mocked(activityApi.createActivityReviewRecord).mockImplementation(async (input) => ({
      id: 'review-created',
      reviewed_at: '2026-03-27T01:01:00Z',
      reviewer: 'admin',
      ...input,
    }))
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

    expect(screen.getByRole('dialog', { name: '分享对话框' })).toBeInTheDocument()
    expect(screen.getByText('分享文件')).toBeInTheDocument()
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

    expect(screen.queryByRole('dialog', { name: '分享对话框' })).not.toBeInTheDocument()
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

    expect(screen.getByText('分享文件夹')).toBeInTheDocument()
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

    expect(screen.getAllByText('有效期').length).toBeGreaterThan(0)
  })

  it('shows server default share policy in the form', async () => {
    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await waitFor(() => {
      expect(getSharePolicy).toHaveBeenCalled()
      expect(screen.getByText('系统默认：7 天')).toBeInTheDocument()
      expect(screen.getByText('系统默认：不限制；0 表示不限制')).toBeInTheDocument()
    })
  })

  it('warns when the server default share policy never expires', async () => {
    vi.mocked(getSharePolicy).mockResolvedValueOnce({
      default_expires_in: '0',
      default_max_access: 0,
    })

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await waitFor(() => {
      expect(screen.getByText('系统默认：不过期')).toBeInTheDocument()
      expect(screen.getByText('系统默认不设置过期时间。')).toBeInTheDocument()
      expect(screen.getByText('系统默认不限制访问次数。')).toBeInTheDocument()
    })
  })

  it('passes an abort signal when loading share policy', async () => {
    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await waitFor(() => {
      const call = vi.mocked(getSharePolicy).mock.calls.find(([options]) => {
        return (options as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
      })
      expect(call).toBeTruthy()
      expect(Object.keys((call?.[0] ?? {}) as Record<string, unknown>).sort()).toEqual(['signal'])
    })
  })

  it('shows matched path policy and requires a password before creating', async () => {
    const user = userEvent.setup()
    vi.mocked(getSharePolicy).mockResolvedValueOnce({
      default_expires_in: '168h',
      default_max_access: 0,
      policy_rules: [{
        path: '/Family',
        require_password: true,
        max_expires_in: '24h',
        max_access: 5,
      }],
    })
    vi.mocked(createShare).mockResolvedValue({
      id: 'share-1',
      path: '/Family/report.pdf',
      type: 'file',
      created_by: 'user-1',
      created_at: new Date().toISOString(),
      has_password: true,
      permission: 'read',
      enabled: true,
      access_count: 0,
      max_access: 5,
      description: '',
      url: '/s/share-1',
      warning: false,
      message: undefined,
    } as never)

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/Family/report.pdf"
      />
    )

    await waitFor(() => {
      expect(screen.getByText('当前路径分享规则')).toBeInTheDocument()
      expect(screen.getByText('此路径要求设置分享密码。')).toBeInTheDocument()
      expect(screen.getByText('有效期最多 1 天。')).toBeInTheDocument()
      expect(screen.getByText('访问次数最多 5 次。')).toBeInTheDocument()
    })

    const createButton = screen.getByText('创建分享链接')
    expect(createButton).toBeDisabled()
    expect(screen.getByText('当前路径要求设置分享密码')).toBeInTheDocument()

    await user.type(screen.getByLabelText('分享访问密码'), 'family-secret')
    expect(createButton).not.toBeDisabled()
    await user.click(createButton)

    await waitFor(() => {
      expectCreateShareCalledWithAbortSignal(expect.objectContaining({
        path: '/Family/report.pdf',
        password: 'family-secret',
      }))
    })
  })

  it('matches server share policy paths after runtime path normalization', async () => {
    vi.mocked(getSharePolicy).mockResolvedValueOnce({
      default_expires_in: '168h',
      default_max_access: 0,
      policy_rules: [{
        path: '/Family//Reports/',
        require_password: true,
        max_expires_in: '24h',
      }],
    })

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/Family/Reports/report.pdf"
      />
    )

    const review = await screen.findByLabelText('分享创建前复核')
    await waitFor(() => {
      expect(screen.getByText('当前路径分享规则')).toBeInTheDocument()
      expect(screen.getByText('此路径要求设置分享密码。')).toBeInTheDocument()
      expect(within(review).getByText('路径策略 /Family/Reports')).toBeInTheDocument()
    })
  })

  it('ignores invalid server share policy paths in the dialog preview', async () => {
    vi.mocked(getSharePolicy).mockResolvedValueOnce({
      default_expires_in: '168h',
      default_max_access: 0,
      policy_rules: [
        {
          path: 'Family',
          require_password: true,
          max_access: 2,
        },
        {
          path: '/Family',
          max_access: 5,
        },
      ],
    })

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/Family/report.pdf"
      />
    )

    const review = await screen.findByLabelText('分享创建前复核')
    await waitFor(() => {
      expect(screen.getByText('当前路径分享规则')).toBeInTheDocument()
      expect(screen.getByText('访问次数最多 5 次。')).toBeInTheDocument()
      expect(screen.queryByText('此路径要求设置分享密码。')).not.toBeInTheDocument()
      expect(screen.queryByText('当前路径要求设置分享密码')).not.toBeInTheDocument()
      expect(within(review).getByText('路径策略 /Family')).toBeInTheDocument()
      expect(within(review).queryByText('必须设置密码')).not.toBeInTheDocument()
    })
  })

  it('ignores server share policy paths with Unicode control characters', async () => {
    vi.mocked(getSharePolicy).mockResolvedValueOnce({
      default_expires_in: '168h',
      default_max_access: 0,
      policy_rules: [{
        path: '/Family\u0081Private',
        require_password: true,
        max_access: 2,
      }],
    })

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/Family\u0081Private/report.pdf"
      />
    )

    await screen.findByText('系统默认：7 天')
    const review = await screen.findByLabelText('分享创建前复核')
    await waitFor(() => {
      expect(screen.queryByText('当前路径分享规则')).not.toBeInTheDocument()
      expect(screen.queryByText('此路径要求设置分享密码。')).not.toBeInTheDocument()
      expect(within(review).getByText('系统默认')).toBeInTheDocument()
    })
  })

  it('summarizes effective share policy before creating', async () => {
    const user = userEvent.setup()
    vi.mocked(getSharePolicy).mockResolvedValueOnce({
      default_expires_in: '168h',
      default_max_access: 0,
      policy_rules: [{
        path: '/Family',
        require_password: true,
        max_expires_in: '24h',
        max_access: 5,
      }],
    })

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/Family/report.pdf"
      />
    )

    const review = await screen.findByLabelText('分享创建前复核')
    await waitFor(() => {
      expect(within(review).getByText('路径策略 /Family')).toBeInTheDocument()
      expect(within(review).getByText('必须设置密码')).toBeInTheDocument()
    })

    fireEvent.change(screen.getByLabelText('分享有效期'), { target: { value: '7 天' } })
    await user.type(screen.getByLabelText('分享访问次数限制'), '12')

    expect(within(review).getByText('1 天（路径策略上限）')).toBeInTheDocument()
    expect(within(review).getByText('5 次（路径策略上限）')).toBeInTheDocument()

    await user.type(screen.getByLabelText('分享访问密码'), 'family-secret')

    expect(within(review).getByText('已设置，满足路径策略')).toBeInTheDocument()
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
    expect(screen.getByLabelText('分享访问次数限制')).toBeInTheDocument()
  })

  it('labels share creation controls for assistive technology', async () => {
    const user = userEvent.setup()
    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByRole('checkbox', { name: '启用密码保护' }))

    expect(screen.getByLabelText('分享访问密码')).toBeInTheDocument()
    expect(screen.getByLabelText('分享有效期')).toBeInTheDocument()
    expect(screen.getByLabelText('分享权限')).toBeInTheDocument()
    expect(screen.getByLabelText('分享访问次数限制')).toBeInTheDocument()
    expect(screen.getByLabelText('分享备注')).toBeInTheDocument()
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
    expect(screen.getByLabelText('分享访问密码')).toBeInTheDocument()

    await user.click(screen.getByText('取消'))

    expect(onClose).toHaveBeenCalled()
  })

  it('blocks share creation when the password exceeds the bcrypt byte limit', async () => {
    const user = userEvent.setup()

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByRole('checkbox'))
    await user.type(screen.getByLabelText('分享访问密码'), 'a'.repeat(73))

    expect(screen.getByText('分享密码最多 72 字节')).toBeInTheDocument()
    const createButton = screen.getByRole('button', { name: '创建分享链接' })
    expect(createButton).toBeDisabled()
    await user.click(createButton)
    expect(createShare).not.toHaveBeenCalled()
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
      description: '分享服务当前不可用，请检查设备状态或稍后重试。',
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
      description: '操作未完成，请稍后重试。',
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

  it('shows a policy-scope warning when the account cannot share the path', async () => {
    const user = userEvent.setup()
    vi.mocked(createShare).mockRejectedValue(new ShareError('policy scope', 403, 'SHARE_POLICY_PRINCIPAL_FORBIDDEN'))

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByText('创建分享链接'))

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '当前账号不能分享该路径',
      description: '该路径的分享策略限制了允许创建或维护分享链接的用户、组或角色。',
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
    await user.type(screen.getByLabelText('分享访问密码'), 'secret-123')

    const createButton = screen.getByText('创建分享链接')
    expect(createButton).not.toBeDisabled()

    await user.click(createButton)

    expectCreateShareCalledWithAbortSignal(
      expect.objectContaining({
        path: '/test/file.txt',
        type: 'file',
        password: 'secret-123',
      }),
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

    fireEvent.change(screen.getByLabelText('分享有效期'), { target: { value: '7 天' } })
    fireEvent.change(screen.getByLabelText('分享权限'), { target: { value: '仅查看' } })
    await user.type(screen.getByLabelText('分享访问次数限制'), '12')
    await user.type(screen.getByLabelText('分享备注'), '  release package  ')
    await user.click(screen.getByText('创建分享链接'))

    expectCreateShareCalledWithAbortSignal(expect.objectContaining({
      expires_in: '7d',
      max_access: 12,
      description: 'release package',
    }))
  })

  it('sends explicit unlimited access limit when set to zero', async () => {
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

    await user.type(screen.getByLabelText('分享访问次数限制'), '0')
    await user.click(screen.getByText('创建分享链接'))

    expectCreateShareCalledWithAbortSignal(expect.objectContaining({
      max_access: 0,
    }))
  })

  it.each([
    ['小数', '1.5', '访问次数必须是 0 或正整数'],
    ['科学计数法', '1e3', '访问次数必须是 0 或正整数'],
    ['超出安全整数范围', '9007199254740992', '访问次数过大'],
  ])('blocks invalid access limits with %s before creating a share', async (_label, maxAccess, message) => {
    const user = userEvent.setup()
    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.type(screen.getByLabelText('分享访问次数限制'), maxAccess)

    const createButton = screen.getByRole('button', { name: '创建分享链接' })
    expect(createButton).toBeDisabled()
    expect(screen.getByText(message)).toBeInTheDocument()

    await user.click(createButton)

    expect(mockAddToast).not.toHaveBeenCalledWith(expect.objectContaining({
      title: '分享链接已创建',
    }))
    expect(createShare).not.toHaveBeenCalled()
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

  it('records share creation execution results when a matching share activity exists', async () => {
    const user = userEvent.setup()
    const createdShare = {
      id: 'share-1',
      path: '/test/file.txt',
      type: 'file' as const,
      created_by: 'user-1',
      created_at: '2026-03-27T01:00:00Z',
      expires_at: null,
      has_password: true,
      permission: 'read' as const,
      enabled: true,
      access_count: 0,
      max_access: 5,
      description: '',
      url: '/s/share-1',
      warning: false,
      message: undefined,
    }
    vi.mocked(createShare).mockResolvedValue(createdShare)
    vi.mocked(activityApi.listActivity).mockResolvedValueOnce({
      items: [
        {
          id: 'act-share-create-1',
          timestamp: '2026-03-27T01:00:00Z',
          action: 'share',
          path: '/test/file.txt',
          user: 'admin',
        },
        {
          id: 'act-share-other-1',
          timestamp: '2026-03-27T01:00:01Z',
          action: 'share',
          path: '/test/other.txt',
          user: 'admin',
        },
        {
          id: 'act-unshare-1',
          timestamp: '2026-03-27T01:00:02Z',
          action: 'unshare',
          path: '/test/file.txt',
          user: 'admin',
        },
      ],
      total: 3,
      limit: 100,
      offset: 0,
    })

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByText('创建分享链接'))

    await waitFor(() => {
      expect(activityApi.createActivityReviewRecord).toHaveBeenCalledTimes(1)
    })
    expect(activityApi.listActivity).toHaveBeenCalledWith(expect.objectContaining({
      actionGroup: 'share',
      path: '/test/file.txt',
      limit: 100,
      offset: 0,
      signal: expect.any(AbortSignal),
    }))
    expect(activityApi.createActivityReviewRecord).toHaveBeenCalledWith(expect.objectContaining({
      note: '分享执行结果：已创建 1 个分享；已关联 1 条分享活动。',
      scope_label: '分享 /test/file.txt',
      filter_summary: '审计分组 分享相关 · 路径 /test/file.txt · 当前分享 1/1 · 执行结果 创建分享',
      disposition_status: 'confirmed',
      action_counts: { share: 1 },
      review_count: 1,
      total_count: 3,
      path_count: 1,
      user_count: 1,
      path_samples: ['/test/file.txt'],
      user_samples: ['admin'],
      share_disposition_details: [{
        path: '/test/file.txt',
        type: 'file',
        enabled: true,
        risk_level: 'none',
        reason_summary: '新建分享。',
        suggested_action: '已创建该分享；继续复核有效期、密码、访问次数和外部引用。',
        access_summary: '密码保护 · 访问 0/5',
        expires_at: '永不过期',
      }],
      activity_entry_ids: ['act-share-create-1'],
    }), expect.objectContaining({
      signal: expect.any(AbortSignal),
    }))
    expect(mockAddToast).toHaveBeenCalledWith({ title: '分享链接已创建', color: 'success' })
    expect(mockAddToast).toHaveBeenCalledWith({ title: '分享创建结果已记录', color: 'success' })
  })

  it('keeps a created share visible when creation result recording fails', async () => {
    const user = userEvent.setup()
    vi.mocked(createShare).mockResolvedValue({
      id: 'share-1',
      path: '/test/file.txt',
      type: 'file',
      created_by: 'user-1',
      created_at: '2026-03-27T01:00:00Z',
      has_password: false,
      permission: 'read',
      enabled: true,
      access_count: 0,
      max_access: 0,
      description: '',
      url: '/s/share-1',
      warning: false,
      message: undefined,
    } as never)
    vi.mocked(activityApi.listActivity).mockResolvedValueOnce({
      items: [{
        id: 'act-share-create-1',
        timestamp: '2026-03-27T01:00:00Z',
        action: 'share',
        path: '/test/file.txt',
        user: 'admin',
      }],
      total: 1,
      limit: 100,
      offset: 0,
    })
    vi.mocked(activityApi.createActivityReviewRecord).mockRejectedValueOnce(new Error('review write failed'))

    render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />
    )

    await user.click(screen.getByText('创建分享链接'))

    expect(await screen.findByText('分享链接已创建')).toBeInTheDocument()
    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '分享创建结果记录失败',
        description: '操作未完成，请稍后重试。',
        color: 'warning',
      })
    })
    expect(screen.getByText('http://localhost:3000/s/share-1')).toBeInTheDocument()
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
      title: '分享链接已创建，但存在警告',
      color: 'warning',
    })
    expect(screen.getByText('分享链接已创建，但存在警告')).toBeInTheDocument()
    expect(screen.getByText('分享链接已创建，但后台记录可能存在延迟，请稍后确认分享列表。')).toBeInTheDocument()
    expect(screen.queryByText('share created with audit warning')).not.toBeInTheDocument()
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
    const createSignal = expectCreateShareCalledWithAbortSignal(expect.objectContaining({
      path: '/old/file.txt',
      type: 'file',
    }))
    expect(createSignal.aborted).toBe(false)

    rerender(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/new/file.txt"
      />
    )
    expect(createSignal.aborted).toBe(true)

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

  it('aborts a pending create request when the dialog unmounts', async () => {
    const user = userEvent.setup()
    const pendingShare = createDeferred<Awaited<ReturnType<typeof createShare>>>()
    vi.mocked(createShare).mockImplementationOnce(() => pendingShare.promise as ReturnType<typeof createShare>)

    const { unmount } = render(
      <ShareDialog
        isOpen={true}
        onClose={() => {}}
        filePath="/test/file.txt"
      />,
    )

    await user.click(screen.getByText('创建分享链接'))

    const createSignal = expectCreateShareCalledWithAbortSignal(expect.objectContaining({
      path: '/test/file.txt',
      type: 'file',
    }))
    expect(createSignal.aborted).toBe(false)

    unmount()

    expect(createSignal.aborted).toBe(true)
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

    expectCreateShareCalledWithAbortSignal(
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

    expectCreateShareCalledWithAbortSignal(
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
      description: '操作未完成，请稍后重试。',
      color: 'danger',
    })
    expect(screen.getByText('/test/file.txt')).toBeInTheDocument()
    expect(onClose).not.toHaveBeenCalled()
  })
})
