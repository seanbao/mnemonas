import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ShareDialog } from './ShareDialog'

// Mock HeroUI components
vi.mock('@heroui/react', () => ({
  Modal: ({ children, isOpen }: { children: React.ReactNode; isOpen: boolean }) =>
    isOpen ? <div data-testid="modal">{children}</div> : null,
  ModalContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  ModalHeader: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  ModalBody: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  ModalFooter: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Button: ({ children, onPress }: { children: React.ReactNode; onPress?: () => void }) => (
    <button onClick={onPress}>{children}</button>
  ),
  Input: ({ label, placeholder }: { label?: string; placeholder?: string }) => (
    <input aria-label={label} placeholder={placeholder} />
  ),
  Select: ({ children }: { children: React.ReactNode }) => <select>{children}</select>,
  SelectItem: ({ children }: { children: React.ReactNode }) => <option>{children}</option>,
  Switch: () => <input type="checkbox" />,
  Snippet: ({ children }: { children: React.ReactNode }) => <code>{children}</code>,
  addToast: vi.fn(),
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
})
