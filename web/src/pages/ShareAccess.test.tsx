import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { ShareAccessPage } from './ShareAccess'

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
  addToast: vi.fn(),
}))

// Mock share API
const mockGetPublicShare = vi.fn()
const mockAccessShareWithPassword = vi.fn()

vi.mock('@/api/share', () => ({
  getPublicShare: (...args: unknown[]) => mockGetPublicShare(...args),
  accessShareWithPassword: (...args: unknown[]) => mockAccessShareWithPassword(...args),
  getShareDownloadUrl: (id: string) => `/s/${id}/download`,
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

  it('shows folder message for folder shares', async () => {
    mockGetPublicShare.mockResolvedValue({
      id: 'abc123',
      type: 'folder',
      has_password: false,
      permission: 'read',
      folder_items: 5,
    })
    
    renderWithRouter('abc123')
    
    await waitFor(() => {
      expect(screen.getByText('5 个项目')).toBeInTheDocument()
      expect(screen.getByText('文件夹浏览功能开发中...')).toBeInTheDocument()
    })
  })
})
