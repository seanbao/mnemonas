import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom'
import { ShareAccessPage } from './ShareAccess'
import { getFolderPathAfterShareAuth } from './shareAccessUtils'
import { ShareError } from '@/api/share'

const mockAddToast = vi.fn()

// Mock HeroUI components
vi.mock('@heroui/react', () => ({
  Card: ({ children, className }: { children: React.ReactNode; className?: string }) => (
    <div className={className} role="group">{children}</div>
  ),
  CardBody: ({ children, className }: { children: React.ReactNode; className?: string }) => (
    <div className={className}>{children}</div>
  ),
  Button: ({ children, onPress, type }: { children: React.ReactNode; onPress?: () => void; type?: 'button' | 'submit' | 'reset' }) => (
    <button onClick={onPress} type={type}>{children}</button>
  ),
  Input: ({ label, placeholder, value, onValueChange, type, id, isDisabled }: {
    label?: string;
    placeholder?: string;
    value?: string;
    onValueChange?: (v: string) => void;
    type?: string;
    id?: string;
    isDisabled?: boolean;
  }) => (
    <input
      id={id}
      aria-label={label}
      placeholder={placeholder}
      value={value}
      disabled={isDisabled}
      onChange={(e) => onValueChange?.(e.target.value)}
      type={type}
    />
  ),
  Spinner: () => <div role="status" aria-label="加载中">Loading...</div>,
  addToast: (...args: unknown[]) => mockAddToast(...args),
}))

// Mock share API
const mockGetPublicShare = vi.fn()
const mockAccessShareWithPassword = vi.fn()
const mockDownloadShare = vi.fn()
const mockGetPublicShareItems = vi.fn()

vi.mock('@/api/share', () => ({
  getPublicShare: (...args: unknown[]) => mockGetPublicShare(...args),
  accessShareWithPassword: (...args: unknown[]) => mockAccessShareWithPassword(...args),
  downloadShare: (...args: unknown[]) => mockDownloadShare(...args),
  getPublicShareItems: (...args: unknown[]) => mockGetPublicShareItems(...args),
  ShareError: class ShareError extends Error {
    status: number
    code?: string
    constructor(message: string, status: number, code?: string) {
      super(message)
      this.status = status
      this.code = code
    }
    get isUnauthorized() { return this.status === 401 }
    get isNotFound() { return this.status === 404 }
    get isFeatureDisabled() { return this.code === 'SHARE_FEATURE_DISABLED' }
    get isDisabled() { return this.code === 'SHARE_DISABLED' }
    get isAccessLimitReached() { return this.code === 'SHARE_ACCESS_LIMIT_REACHED' }
    get isExpired() { return this.code === 'SHARE_EXPIRED' || (this.status === 410 && !this.code) }
    get isUnavailable() { return this.status === 503 && !this.isFeatureDisabled }
  }
}))

const renderWithRouter = (shareId: string) => {
  return render(
    <MemoryRouter initialEntries={[`/s/${shareId}`]}>
      <Routes>
        <Route path="/s/:id" element={<ShareAccessPage />} />
      </Routes>
    </MemoryRouter>
  )
}

const renderWithoutShareId = () => {
  return render(
    <MemoryRouter initialEntries={['/s']}>
      <Routes>
        <Route path="/s" element={<ShareAccessPage />} />
      </Routes>
    </MemoryRouter>
  )
}

function createDeferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

const NavigatingWrapper = ({ nextId }: { nextId: string }) => {
  const navigate = useNavigate()
  return (
    <div>
      <button type="button" onClick={() => navigate(`/s/${nextId}`)}>go</button>
      <ShareAccessPage />
    </div>
  )
}

describe('ShareAccessPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows loading spinner initially', () => {
    mockGetPublicShare.mockImplementation(() => new Promise(() => {})) // Never resolves
    
    renderWithRouter('abc123')
    
    // Component uses custom CSS spinner, check for loading text
    expect(screen.getByText('加载分享信息…')).toBeInTheDocument()
  })

  it('passes an abort signal when loading share info', async () => {
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: false,
      permission: 'read',
      file_name: 'test.txt',
      file_size: 1024,
    })

    renderWithRouter('abc123')

    await waitFor(() => {
      const call = mockGetPublicShare.mock.calls.find(([, options]) => {
        return (options as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
      })
      expect(call).toBeTruthy()
      expect(Object.keys((call?.[1] ?? {}) as Record<string, unknown>).sort()).toEqual(['signal'])
    })
  })

  it('shows error when share not found', async () => {
    mockGetPublicShare.mockRejectedValue(new Error('分享不存在或已失效'))
    
    renderWithRouter('invalid-id')
    
    await waitFor(() => {
      expect(screen.getByText('无法访问分享')).toBeInTheDocument()
      expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeInTheDocument()
    })
  })

  it('shows a dedicated not-found state for structured missing public shares', async () => {
    mockGetPublicShare.mockRejectedValue(new ShareError('share not found', 404, 'SHARE_NOT_FOUND'))

    renderWithRouter('missing-share')

    await waitFor(() => {
      expect(screen.getByText('分享不存在或已失效')).toBeInTheDocument()
      expect(screen.getByText('该分享链接不存在、已被移除，或当前不可访问。')).toBeInTheDocument()
      expect(screen.queryByText('share not found')).not.toBeInTheDocument()
    })
  })

  it('shows a missing-content state when the shared file has been removed', async () => {
    mockGetPublicShare.mockRejectedValue(new ShareError('分享文件不存在或已被移除', 404, 'FILE_NOT_FOUND'))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('分享内容已不存在')).toBeInTheDocument()
      expect(screen.getByText('该分享指向的文件或文件夹已被移动或删除，请联系分享创建者。')).toBeInTheDocument()
    })
  })

  it('shows an invalid link error and retry toast when the route has no share id', async () => {
    const user = userEvent.setup()

    renderWithoutShareId()

    await waitFor(() => {
      expect(screen.getByText('无法访问分享')).toBeInTheDocument()
      expect(screen.getByText('无效的分享链接')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '重新加载' }))

    expect(mockGetPublicShare).not.toHaveBeenCalled()
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '刷新失败',
      description: '无效的分享链接',
      color: 'danger',
    })
  })

  it('shows a dedicated disabled state when public sharing is turned off', async () => {
    mockGetPublicShare.mockRejectedValue(new ShareError('share feature disabled', 503, 'SHARE_FEATURE_DISABLED'))

    renderWithRouter('disabled-share')

    await waitFor(() => {
      expect(screen.getByText('分享功能已关闭')).toBeInTheDocument()
      expect(screen.getByText('当前服务已关闭分享功能，公开分享链接暂不可访问。')).toBeInTheDocument()
    })
  })

  it('shows an expired-state title when the share has gone away', async () => {
    mockGetPublicShare.mockRejectedValue(new ShareError('分享已过期、已禁用或访问次数已达上限', 410))

    renderWithRouter('expired-share')

    await waitFor(() => {
      expect(screen.getByText('分享已失效')).toBeInTheDocument()
      expect(screen.getByText('该分享已失效，当前不可访问。')).toBeInTheDocument()
    })
  })

  it('shows a dedicated disabled-state title when the share has been turned off', async () => {
    mockGetPublicShare.mockRejectedValue(new ShareError('share disabled', 410, 'SHARE_DISABLED'))

    renderWithRouter('disabled-gone-share')

    await waitFor(() => {
      expect(screen.getByText('分享已停用')).toBeInTheDocument()
      expect(screen.getByText('该分享已被停用，当前不可访问。')).toBeInTheDocument()
    })
  })

  it('shows a dedicated access-limit state when a share reaches its visit cap', async () => {
    mockGetPublicShare.mockRejectedValue(new ShareError('share access limit reached', 410, 'SHARE_ACCESS_LIMIT_REACHED'))

    renderWithRouter('limited-share')

    await waitFor(() => {
      expect(screen.getByText('分享访问次数已用尽')).toBeInTheDocument()
      expect(screen.getByText('该分享已达到访问次数上限，当前不可访问。')).toBeInTheDocument()
    })
  })

  it('shows an unavailable state when shared content storage is temporarily unavailable', async () => {
    mockGetPublicShare.mockRejectedValue(new ShareError('filesystem not available', 503, 'FILESYSTEM_UNAVAILABLE'))

    renderWithRouter('unavailable-share')

    await waitFor(() => {
      expect(screen.getByText('分享内容暂不可用')).toBeInTheDocument()
      expect(screen.getByText('分享内容当前不可访问，请检查设备状态或稍后重试。')).toBeInTheDocument()
    })
  })

  it('retries loading share info from the error state', async () => {
    const user = userEvent.setup()
    mockGetPublicShare
      .mockRejectedValueOnce(new Error('分享不存在或已失效'))
      .mockResolvedValueOnce({
        id: 'abc123',
        type: 'file',
        has_password: false,
        permission: 'read',
        file_name: 'test.txt',
        file_size: 1024,
      })

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '重新加载' }))

    await waitFor(() => {
      expect(screen.getByText('test.txt')).toBeInTheDocument()
      expect(mockAddToast).toHaveBeenCalledWith({ title: '分享信息已刷新', color: 'success' })
    })
  })

  it('shows warning toast when retrying share access becomes unavailable', async () => {
    const user = userEvent.setup()
    mockGetPublicShare
      .mockRejectedValueOnce(new Error('network error'))
      .mockRejectedValueOnce(new ShareError('share unavailable', 503, 'SERVICE_UNAVAILABLE'))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '重新加载' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '分享内容暂不可用',
        description: '分享内容当前不可访问，请检查设备状态或稍后重试。',
        color: 'warning',
      })
    })
  })

  it('shows password form when share requires password', async () => {
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: true,
      permission: 'read',
    })
    
    renderWithRouter('abc123')
    
    await waitFor(() => {
      expect(screen.getByText('此分享需要密码')).toBeInTheDocument()
    })
  })

  it('shows warning feedback when submitting an empty password', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: true,
      permission: 'read',
    })

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('此分享需要密码')).toBeInTheDocument()
    })

    await user.click(screen.getByText('验证密码'))

    expect(mockAccessShareWithPassword).not.toHaveBeenCalled()
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '请输入访问密码',
      color: 'warning',
    })
  })

  it('shows unavailable toast when password verification is temporarily unavailable', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: true,
      permission: 'read',
    })
    mockAccessShareWithPassword.mockRejectedValue(new ShareError('filesystem not available', 503, 'FILESYSTEM_UNAVAILABLE'))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('此分享需要密码')).toBeInTheDocument()
    })

    await user.type(screen.getByLabelText('访问密码'), 'secret')
    await user.click(screen.getByText('验证密码'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '验证暂不可用',
        description: '分享内容当前不可访问，请检查设备状态或稍后重试。',
        color: 'warning',
      })
    })
  })

  it('shows password error feedback when verification is unauthorized', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: true,
      permission: 'read',
    })
    mockAccessShareWithPassword.mockRejectedValue(new ShareError('密码错误', 401))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('此分享需要密码')).toBeInTheDocument()
    })

    await user.type(screen.getByLabelText('访问密码'), 'wrong-secret')
    await user.click(screen.getByText('验证密码'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({ title: '密码错误', color: 'danger' })
    })
  })

  it('shows disabled feedback when password verification finds sharing is off', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: true,
      permission: 'read',
    })
    mockAccessShareWithPassword.mockRejectedValue(new ShareError('share feature disabled', 503, 'SHARE_FEATURE_DISABLED'))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('此分享需要密码')).toBeInTheDocument()
    })

    await user.type(screen.getByLabelText('访问密码'), 'secret')
    await user.click(screen.getByText('验证密码'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '分享功能已关闭',
        description: '当前服务已关闭分享功能，公开分享链接暂不可访问。',
        color: 'warning',
      })
    })
  })

  it('shows fallback feedback when password verification fails with an unknown value', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: true,
      permission: 'read',
    })
    mockAccessShareWithPassword.mockRejectedValue('verification stopped')

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('此分享需要密码')).toBeInTheDocument()
    })

    await user.type(screen.getByLabelText('访问密码'), 'secret')
    await user.click(screen.getByText('验证密码'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '验证失败',
        description: '操作未完成，请稍后重试。',
        color: 'danger',
      })
    })
  })

  it('shows file info when share is accessible', async () => {
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: false,
      permission: 'read',
      file_name: 'test.txt',
      file_size: 1024,
    })
    
    renderWithRouter('abc123')
    
    await waitFor(() => {
      expect(screen.getByText('test.txt')).toBeInTheDocument()
      expect(screen.getByText('下载文件')).toBeInTheDocument()
    })
  })

  it('shows zero-byte file size when share is accessible', async () => {
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: false,
      permission: 'read',
      file_name: 'empty.txt',
      file_size: 0,
    })

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('empty.txt')).toBeInTheDocument()
      expect(screen.getByText('0 B')).toBeInTheDocument()
    })
  })

  it('reuses existing authorized cookie access for protected shares', async () => {
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: true,
      permission: 'read',
      file_name: 'secret.txt',
      file_size: 1024,
    })

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('secret.txt')).toBeInTheDocument()
      expect(screen.getByText('下载文件')).toBeInTheDocument()
    })

    expect(screen.queryByText('此分享需要密码')).not.toBeInTheDocument()
  })

  it('shows folder listing for folder shares', async () => {
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      folder_items: 5,
    })
    mockGetPublicShareItems.mockResolvedValue({
      path: '',
      items: [
        { name: 'docs', path: 'docs', is_dir: true, size: 0, mod_time: '2024-01-01T00:00:00Z' },
        { name: 'note.txt', path: 'note.txt', is_dir: false, size: 12, mod_time: '2024-01-02T00:00:00Z' },
      ],
    })
    
    renderWithRouter('abc123')
    
    await waitFor(() => {
      expect(screen.getByText('docs')).toBeInTheDocument()
      expect(screen.getByText('note.txt')).toBeInTheDocument()
    })
  })

  it('shows empty folder item count when share is accessible', async () => {
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      folder_items: 0,
    })
    mockGetPublicShareItems.mockResolvedValue({
      path: '',
      items: [],
    })

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('0 个项目')).toBeInTheDocument()
    })
  })

  it('shows a folder listing loading state while items are loading', async () => {
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      folder_items: 1,
    })
    mockGetPublicShareItems.mockImplementation(() => new Promise(() => {}))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('加载文件夹内容…')).toBeInTheDocument()
    })
  })

  it('downloads a file from a shared folder', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      folder_items: 1,
    })
    mockGetPublicShareItems.mockResolvedValue({
      path: '',
      items: [
        { name: 'note.txt', path: 'note.txt', is_dir: false, size: 12, mod_time: '2024-01-02T00:00:00Z' },
      ],
    })
    mockDownloadShare.mockResolvedValue(undefined)

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('note.txt')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '下载' }))

    expect(mockDownloadShare).toHaveBeenCalledWith('abc123', expect.objectContaining({
      filePath: 'note.txt',
      filename: 'note.txt',
      signal: expect.any(AbortSignal),
    }))
  })

  it('downloads the shared folder root as a zip archive', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      file_name: 'docs',
      folder_items: 0,
    })
    mockGetPublicShareItems.mockResolvedValue({
      path: '',
      items: [],
    })
    mockDownloadShare.mockResolvedValue(undefined)

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '下载为 ZIP' }))

    expect(mockDownloadShare).toHaveBeenCalledWith('abc123', expect.objectContaining({
      archive: 'zip',
      filename: 'docs.zip',
      signal: expect.any(AbortSignal),
    }))
  })

  it('does not duplicate zip extensions for shared folder archive filenames', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      file_name: 'backups.zip',
      folder_items: 0,
    })
    mockGetPublicShareItems.mockResolvedValue({
      path: '',
      items: [],
    })
    mockDownloadShare.mockResolvedValue(undefined)

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('backups.zip')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '下载为 ZIP' }))

    expect(mockDownloadShare).toHaveBeenCalledWith('abc123', expect.objectContaining({
      archive: 'zip',
      filename: 'backups.zip',
      signal: expect.any(AbortSignal),
    }))
  })

  it('downloads the current shared subfolder as a zip archive', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      file_name: 'team-share',
      folder_items: 1,
    })
    mockGetPublicShareItems
      .mockResolvedValueOnce({
        path: '',
        items: [
          { name: 'docs', path: 'docs', is_dir: true, size: 0, mod_time: '2024-01-01T00:00:00Z' },
        ],
      })
      .mockResolvedValueOnce({
        path: 'docs',
        items: [],
      })
    mockDownloadShare.mockResolvedValue(undefined)

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: /docs/ }))

    await waitFor(() => {
      expect(screen.getByText('/docs')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '下载为 ZIP' }))

    expect(mockDownloadShare).toHaveBeenCalledWith('abc123', expect.objectContaining({
      filePath: 'docs',
      archive: 'zip',
      filename: 'docs.zip',
      signal: expect.any(AbortSignal),
    }))
  })

  it('downloads a folder item from a shared folder as a zip archive', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      folder_items: 1,
    })
    mockGetPublicShareItems.mockResolvedValue({
      path: '',
      items: [
        { name: 'reports', path: 'reports', is_dir: true, size: 0, mod_time: '2024-01-02T00:00:00Z' },
      ],
    })
    mockDownloadShare.mockResolvedValue(undefined)

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('reports')).toBeInTheDocument()
    })

    const zipButtons = screen.getAllByRole('button', { name: '下载为 ZIP' })
    await user.click(zipButtons[zipButtons.length - 1])

    expect(mockDownloadShare).toHaveBeenCalledWith('abc123', expect.objectContaining({
      filePath: 'reports',
      filename: 'reports.zip',
      archive: 'zip',
      signal: expect.any(AbortSignal),
    }))
  })

  it('returns to password prompt when shared folder item download is unauthorized', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      folder_items: 1,
    })
    mockGetPublicShareItems.mockResolvedValue({
      path: '',
      items: [
        { name: 'note.txt', path: 'note.txt', is_dir: false, size: 12, mod_time: '2024-01-02T00:00:00Z' },
      ],
    })
    mockDownloadShare.mockRejectedValue(new ShareError('unauthorized', 401))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('note.txt')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '下载' }))

    await waitFor(() => {
      expect(screen.getByText('此分享需要密码')).toBeInTheDocument()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '访问凭证已失效，请重新输入密码',
        color: 'warning',
      })
    })
  })

  it('shows fallback feedback when shared folder item download fails with an unknown value', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      folder_items: 1,
    })
    mockGetPublicShareItems.mockResolvedValue({
      path: '',
      items: [
        { name: 'note.txt', path: 'note.txt', is_dir: false, size: 12, mod_time: '2024-01-02T00:00:00Z' },
      ],
    })
    mockDownloadShare.mockRejectedValue('download stopped')

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('note.txt')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '下载' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '下载失败',
        description: '操作未完成，请稍后重试。',
        color: 'danger',
      })
    })
  })

  it('shows missing-content feedback when a shared folder item no longer exists', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      folder_items: 1,
    })
    mockGetPublicShareItems.mockResolvedValue({
      path: '',
      items: [
        { name: 'note.txt', path: 'note.txt', is_dir: false, size: 12, mod_time: '2024-01-02T00:00:00Z' },
      ],
    })
    mockDownloadShare.mockRejectedValue(new ShareError('分享文件不存在或已被移除', 404, 'FILE_NOT_FOUND'))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('note.txt')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '下载' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '分享内容已不存在',
        description: '该分享指向的文件或文件夹已被移动或删除，请联系分享创建者。',
        color: 'warning',
      })
    })
  })

  it('ignores stale folder listing responses after navigating back to a parent folder', async () => {
    const user = userEvent.setup()
    const nestedListing = createDeferred<{
      path: string
      items: Array<{ name: string; path: string; is_dir: boolean; size: number; mod_time: string }>
    }>()

    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      folder_items: 2,
    })
    mockGetPublicShareItems
      .mockResolvedValueOnce({
        path: '',
        items: [
          { name: 'docs', path: 'docs', is_dir: true, size: 0, mod_time: '2024-01-01T00:00:00Z' },
        ],
      })
      .mockReturnValueOnce(nestedListing.promise)
      .mockResolvedValueOnce({
        path: '',
        items: [
          { name: 'docs', path: 'docs', is_dir: true, size: 0, mod_time: '2024-01-01T00:00:00Z' },
        ],
      })

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeInTheDocument()
      expect(screen.getByText('根目录')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: /docs/ }))

    await waitFor(() => {
      expect(screen.getByText('/docs')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '返回上级' })).toBeInTheDocument()
    })
    const nestedCall = mockGetPublicShareItems.mock.calls.find(([, options]) => {
      return (options as { path?: string }).path === 'docs'
    })
    const nestedSignal = (nestedCall?.[1] as { signal?: AbortSignal } | undefined)?.signal
    expect(nestedSignal).toBeInstanceOf(AbortSignal)

    await user.click(screen.getByRole('button', { name: '返回上级' }))
    expect(nestedSignal?.aborted).toBe(true)

    await waitFor(() => {
      expect(screen.getByText('根目录')).toBeInTheDocument()
    })

    nestedListing.resolve({
      path: 'docs',
      items: [
        { name: 'nested.txt', path: 'docs/nested.txt', is_dir: false, size: 12, mod_time: '2024-01-02T00:00:00Z' },
      ],
    })

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeInTheDocument()
    })

    expect(screen.queryByText('nested.txt')).not.toBeInTheDocument()
    expect(screen.getByText('根目录')).toBeInTheDocument()
  })

  it('ignores stale folder listing responses after navigating to another share', async () => {
    const user = userEvent.setup()
    const folderListing = createDeferred<{
      path: string
      items: Array<{ name: string; path: string; is_dir: boolean; size: number; mod_time: string }>
    }>()

    mockGetPublicShare
      .mockResolvedValueOnce({
        id: 'folder-share',
        type: 'folder',
        has_password: false,
        permission: 'read',
        file_name: 'folder-share',
        folder_items: 1,
      })
      .mockResolvedValueOnce({
        id: 'file-share',
        type: 'file',
        has_password: false,
        permission: 'read',
        file_name: 'next.txt',
        file_size: 42,
      })
    mockGetPublicShareItems.mockReturnValueOnce(folderListing.promise)

    render(
      <MemoryRouter initialEntries={['/s/folder-share']}>
        <Routes>
          <Route path="/s/:id" element={<NavigatingWrapper nextId="file-share" />} />
        </Routes>
      </MemoryRouter>
    )

    await waitFor(() => {
      expect(mockGetPublicShareItems).toHaveBeenCalled()
    })
    const folderListSignal = (mockGetPublicShareItems.mock.calls[0]?.[1] as { signal?: AbortSignal } | undefined)?.signal
    expect(folderListSignal).toBeInstanceOf(AbortSignal)

    await user.click(screen.getByRole('button', { name: 'go' }))
    expect(folderListSignal?.aborted).toBe(true)

    await waitFor(() => {
      expect(screen.getByText('next.txt')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '下载文件' })).toBeInTheDocument()
    })

    folderListing.resolve({
      path: '',
      items: [
        { name: 'old-folder-file.txt', path: 'old-folder-file.txt', is_dir: false, size: 12, mod_time: '2024-01-02T00:00:00Z' },
      ],
    })

    await waitFor(() => {
      expect(screen.getByText('next.txt')).toBeInTheDocument()
    })
    expect(screen.queryByText('old-folder-file.txt')).not.toBeInTheDocument()
  })

  it('retries loading folder items after a listing failure', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      folder_items: 1,
    })
    mockGetPublicShareItems
      .mockRejectedValueOnce(new Error('加载文件夹失败'))
      .mockResolvedValueOnce({
        path: '',
        items: [
          { name: 'docs', path: 'docs', is_dir: true, size: 0, mod_time: '2024-01-01T00:00:00Z' },
        ],
      })

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('加载文件夹失败')).toBeInTheDocument()
      expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '重试加载' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '重试加载' }))

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeInTheDocument()
    })
  })

  it('shows an unavailable message when shared folder listing returns service unavailable', async () => {
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      folder_items: 1,
    })
    mockGetPublicShareItems.mockRejectedValueOnce(new ShareError('filesystem not available', 503, 'FILESYSTEM_UNAVAILABLE'))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('文件夹内容暂不可用')).toBeInTheDocument()
      expect(screen.getByText('分享目录当前不可访问，请检查设备状态或稍后重试。')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '重试加载' })).toBeInTheDocument()
    })
  })

  it('downloads protected share via fetch/blob flow after password verification', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: true,
      permission: 'read',
    })
    mockAccessShareWithPassword.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: true,
      permission: 'read',
      file_name: 'secret.txt',
      file_size: 10,
    })
    mockDownloadShare.mockResolvedValue(undefined)

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('此分享需要密码')).toBeInTheDocument()
    })

    await user.type(screen.getByLabelText('访问密码'), 'secret')
    await user.click(screen.getByText('验证密码'))

    await waitFor(() => {
      expect(screen.getByText('下载文件')).toBeInTheDocument()
    })

    await user.click(screen.getByText('下载文件'))

    expect(mockAccessShareWithPassword).toHaveBeenCalledWith('abc123', 'secret', expect.objectContaining({
      signal: expect.any(AbortSignal),
    }))
    expect(mockDownloadShare).toHaveBeenCalledWith('abc123', expect.objectContaining({
      filename: 'secret.txt',
      signal: expect.any(AbortSignal),
    }))
  })

  it('returns to password prompt when share download cookie is expired', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: false,
      permission: 'read',
      file_name: 'test.txt',
      file_size: 10,
    })
    mockDownloadShare.mockRejectedValue(new ShareError('访问凭证已失效，请重新输入密码', 401))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('下载文件')).toBeInTheDocument()
    })

    await user.click(screen.getByText('下载文件'))

    await waitFor(() => {
      expect(screen.getByText('此分享需要密码')).toBeInTheDocument()
    })

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '访问凭证已失效，请重新输入密码',
      color: 'warning',
    })
  })

  it('shows unavailable toast when file download is temporarily unavailable', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: false,
      permission: 'read',
      file_name: 'test.txt',
      file_size: 10,
    })
    mockDownloadShare.mockRejectedValue(new ShareError('filesystem not available', 503, 'FILESYSTEM_UNAVAILABLE'))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('下载文件')).toBeInTheDocument()
    })

    await user.click(screen.getByText('下载文件'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '下载暂不可用',
        description: '分享内容当前不可访问，请检查设备状态或稍后重试。',
        color: 'warning',
      })
    })
  })

  it('shows missing-content feedback when file download target no longer exists', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: false,
      permission: 'read',
      file_name: 'test.txt',
      file_size: 10,
    })
    mockDownloadShare.mockRejectedValue(new ShareError('分享文件不存在或已被移除', 404, 'FILE_NOT_FOUND'))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('下载文件')).toBeInTheDocument()
    })

    await user.click(screen.getByText('下载文件'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '分享内容已不存在',
        description: '该分享指向的文件或文件夹已被移动或删除，请联系分享创建者。',
        color: 'warning',
      })
    })
  })

  it('shows failure toast when file download fails with a generic error', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: false,
      permission: 'read',
      file_name: 'test.txt',
      file_size: 10,
    })
    mockDownloadShare.mockRejectedValue(new Error('download failed'))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('下载文件')).toBeInTheDocument()
    })

    await user.click(screen.getByText('下载文件'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '下载失败',
        description: '操作未完成，请稍后重试。',
        color: 'danger',
      })
    })
  })

  it('shows disabled feedback when file download returns a disabled share error', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'file',
      has_password: false,
      permission: 'read',
      file_name: 'test.txt',
      file_size: 10,
    })
    mockDownloadShare.mockRejectedValue(new ShareError('share disabled', 410, 'SHARE_DISABLED'))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('下载文件')).toBeInTheDocument()
    })

    await user.click(screen.getByText('下载文件'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '分享已停用',
        description: '该分享已被停用，当前不可访问。',
        color: 'warning',
      })
    })
  })

  it('returns to password prompt when listing fails with unauthorized', async () => {
    const user = userEvent.setup()
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: true,
      permission: 'read',
    })
    mockAccessShareWithPassword.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: true,
      permission: 'read',
      folder_items: 1,
    })
    mockGetPublicShareItems.mockRejectedValue(new ShareError('密码错误', 401))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('此分享需要密码')).toBeInTheDocument()
    })

    await user.type(screen.getByLabelText('访问密码'), 'secret')
    await user.click(screen.getByText('验证密码'))

    await waitFor(() => {
      expect(screen.getByText('此分享需要密码')).toBeInTheDocument()
    })

    expect(screen.getByLabelText('访问密码')).toHaveValue('')
  })

  it('shows a missing-content state when listing a folder share whose target no longer exists', async () => {
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      folder_items: 1,
    })
    mockGetPublicShareItems.mockRejectedValue(new ShareError('分享文件不存在或已被移除', 404, 'FILE_NOT_FOUND'))

    renderWithRouter('abc123')

    await waitFor(() => {
      expect(screen.getByText('分享内容已不存在')).toBeInTheDocument()
      expect(screen.getByText('该分享指向的文件或文件夹已被移动或删除，请联系分享创建者。')).toBeInTheDocument()
    })
  })

  it('preserves the current folder path after re-authenticating a folder share', () => {
    expect(getFolderPathAfterShareAuth('docs/nested', {
      id: 'abc123',
      type: 'folder',
      has_password: true,
      permission: 'read',
    })).toBe('docs/nested')

    expect(getFolderPathAfterShareAuth('docs/nested', {
      id: 'abc123',
      type: 'file',
      has_password: true,
      permission: 'read',
    })).toBe('')
  })

  it('resets auth state when share id changes', async () => {
    mockGetPublicShare
      .mockResolvedValueOnce({
        id: 'first',
        type: 'file',
        has_password: false,
        permission: 'read',
        file_name: 'public.txt',
        file_size: 10,
      })
      .mockResolvedValueOnce({
        id: 'second',
        type: 'file',
        has_password: true,
        permission: 'read',
      })

    render(
      <MemoryRouter initialEntries={[`/s/first`]}>
        <Routes>
          <Route path="/s/:id" element={<NavigatingWrapper initialId="first" nextId="second" />} />
        </Routes>
      </MemoryRouter>
    )

    await waitFor(() => {
      expect(screen.getByText('public.txt')).toBeInTheDocument()
    })

    await userEvent.click(screen.getByText('go'))

    await waitFor(() => {
      expect(screen.getByText('此分享需要密码')).toBeInTheDocument()
    })

    expect(screen.getByLabelText('访问密码')).toHaveValue('')
  })

  it('ignores stale password verification responses after navigating to another share', async () => {
    const verification = createDeferred<{
      id: string
      type: 'file'
      has_password: boolean
      permission: 'read'
      file_name: string
      file_size: number
    }>()

    mockGetPublicShare
      .mockResolvedValueOnce({
        id: 'first',
        type: 'file',
        has_password: true,
        permission: 'read',
      })
      .mockResolvedValueOnce({
        id: 'second',
        type: 'file',
        has_password: false,
        permission: 'read',
        file_name: 'second.txt',
        file_size: 12,
      })
    mockAccessShareWithPassword.mockReturnValueOnce(verification.promise)

    render(
      <MemoryRouter initialEntries={[`/s/first`]}>
        <Routes>
          <Route path="/s/:id" element={<NavigatingWrapper nextId="second" />} />
        </Routes>
      </MemoryRouter>
    )

    await waitFor(() => {
      expect(screen.getByText('此分享需要密码')).toBeInTheDocument()
    })

    await userEvent.type(screen.getByLabelText('访问密码'), 'secret')
    await userEvent.click(screen.getByText('验证密码'))

    await waitFor(() => {
      expect(mockAccessShareWithPassword).toHaveBeenCalledWith('first', 'secret', expect.objectContaining({
        signal: expect.any(AbortSignal),
      }))
    })
    const verificationSignal = (mockAccessShareWithPassword.mock.calls[0]?.[2] as { signal?: AbortSignal } | undefined)?.signal
    expect(verificationSignal).toBeInstanceOf(AbortSignal)

    await userEvent.click(screen.getByText('go'))
    expect(verificationSignal?.aborted).toBe(true)

    await waitFor(() => {
      expect(screen.getByText('second.txt')).toBeInTheDocument()
    })

    verification.resolve({
      id: 'first',
      type: 'file',
      has_password: true,
      permission: 'read',
      file_name: 'secret.txt',
      file_size: 99,
    })

    await waitFor(() => {
      expect(screen.getByText('second.txt')).toBeInTheDocument()
    })
    expect(screen.queryByText('secret.txt')).not.toBeInTheDocument()
  })

  it('ignores stale download unauthorized responses after navigating to another share', async () => {
    const download = createDeferred<void>()

    mockGetPublicShare
      .mockResolvedValueOnce({
        id: 'first',
        type: 'file',
        has_password: false,
        permission: 'read',
        file_name: 'first.txt',
        file_size: 10,
      })
      .mockResolvedValueOnce({
        id: 'second',
        type: 'file',
        has_password: false,
        permission: 'read',
        file_name: 'second.txt',
        file_size: 12,
      })
    mockDownloadShare.mockReturnValueOnce(download.promise)

    render(
      <MemoryRouter initialEntries={[`/s/first`]}>
        <Routes>
          <Route path="/s/:id" element={<NavigatingWrapper nextId="second" />} />
        </Routes>
      </MemoryRouter>
    )

    await waitFor(() => {
      expect(screen.getByText('first.txt')).toBeInTheDocument()
    })

    await userEvent.click(screen.getByText('下载文件'))
    await waitFor(() => {
      expect(mockDownloadShare).toHaveBeenCalledWith('first', expect.objectContaining({
        filename: 'first.txt',
        signal: expect.any(AbortSignal),
      }))
    })
    const downloadSignal = (mockDownloadShare.mock.calls[0]?.[1] as { signal?: AbortSignal } | undefined)?.signal
    expect(downloadSignal).toBeInstanceOf(AbortSignal)

    await userEvent.click(screen.getByText('go'))
    await waitFor(() => {
      expect(downloadSignal?.aborted).toBe(true)
    })

    await waitFor(() => {
      expect(screen.getByText('second.txt')).toBeInTheDocument()
    })

    download.reject(new ShareError('unauthorized', 401))

    await waitFor(() => {
      expect(screen.getByText('second.txt')).toBeInTheDocument()
    })
    expect(screen.queryByText('此分享需要密码')).not.toBeInTheDocument()
  })
})
