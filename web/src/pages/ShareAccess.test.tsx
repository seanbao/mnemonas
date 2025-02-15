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
    <div className={className} data-testid="card">{children}</div>
  ),
  CardBody: ({ children, className }: { children: React.ReactNode; className?: string }) => (
    <div className={className}>{children}</div>
  ),
  Button: ({ children, onPress, type }: { children: React.ReactNode; onPress?: () => void; type?: 'button' | 'submit' | 'reset' }) => (
    <button onClick={onPress} type={type}>{children}</button>
  ),
  Input: ({ label, placeholder, value, onValueChange, type }: { 
    label?: string; 
    placeholder?: string; 
    value?: string;
    onValueChange?: (v: string) => void;
    type?: string;
  }) => (
    <input 
      aria-label={label} 
      placeholder={placeholder}
      value={value}
      onChange={(e) => onValueChange?.(e.target.value)}
      type={type}
    />
  ),
  Spinner: () => <div data-testid="spinner">Loading...</div>,
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
    constructor(message: string, status: number) {
      super(message)
      this.status = status
    }
    get isUnauthorized() { return this.status === 401 }
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
    expect(screen.getByText('加载分享信息...')).toBeInTheDocument()
  })

  it('shows error when share not found', async () => {
    mockGetPublicShare.mockRejectedValue(new Error('分享不存在或已失效'))
    
    renderWithRouter('invalid-id')
    
    await waitFor(() => {
      expect(screen.getByText('无法访问分享')).toBeInTheDocument()
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
      expect(screen.getByRole('button', { name: '重试加载' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '重试加载' }))

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeInTheDocument()
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

    await user.type(screen.getByPlaceholderText('请输入密码'), 'secret')
    await user.click(screen.getByText('验证密码'))

    await waitFor(() => {
      expect(screen.getByText('下载文件')).toBeInTheDocument()
    })

    await user.click(screen.getByText('下载文件'))

    expect(mockAccessShareWithPassword).toHaveBeenCalledWith('abc123', 'secret')
    expect(mockDownloadShare).toHaveBeenCalledWith('abc123', { filename: 'secret.txt' })
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

    await user.type(screen.getByPlaceholderText('请输入密码'), 'secret')
    await user.click(screen.getByText('验证密码'))

    await waitFor(() => {
      expect(screen.getByText('此分享需要密码')).toBeInTheDocument()
    })

    expect(screen.getByPlaceholderText('请输入密码')).toHaveValue('')
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

    expect(screen.getByPlaceholderText('请输入密码')).toHaveValue('')
  })
})
