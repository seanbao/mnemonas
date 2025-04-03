import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'
import React from 'react'
import { Header } from './Header'

const invalidateQueries = vi.fn().mockResolvedValue(undefined)
const useIsAdminMock = vi.fn(() => true)
const mockAddToast = vi.fn()
const openUrlInNewTabMock = vi.fn(() => true)
const navigateMock = vi.fn()
let locationPathname = '/'

vi.mock('@tanstack/react-query', () => ({
  useQueryClient: () => ({ invalidateQueries }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ logout: vi.fn().mockResolvedValue(undefined) }),
  useUser: () => ({ username: 'admin', email: 'admin@local' }),
  useIsAdmin: () => useIsAdminMock(),
}))

vi.mock('react-router-dom', () => ({
  useNavigate: () => navigateMock,
  useLocation: () => ({ pathname: locationPathname }),
}))

vi.mock('@/components/ThemeToggle', () => ({
  ThemeToggle: () => <div data-testid="theme-toggle" />,
}))

vi.mock('@/lib/utils', async () => {
  const actual = await vi.importActual<typeof import('@/lib/utils')>('@/lib/utils')
  return {
    ...actual,
    openUrlInNewTab: (...args: unknown[]) => openUrlInNewTabMock(...args),
  }
})

vi.mock('@heroui/react', () => ({
  Button: ({ children, onPress, isLoading, 'aria-label': ariaLabel }: { children: React.ReactNode; onPress?: () => void; isLoading?: boolean; 'aria-label'?: string }) => (
    <button onClick={onPress} disabled={isLoading} aria-label={ariaLabel}>{children}</button>
  ),
  Avatar: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  Dropdown: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownTrigger: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownItem: ({ children, onPress }: { children: React.ReactNode; onPress?: () => void }) => <button onClick={onPress}>{children}</button>,
  addToast: (...args: unknown[]) => mockAddToast(...args),
}))

describe('Header', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useIsAdminMock.mockReturnValue(true)
    openUrlInNewTabMock.mockReturnValue(true)
    locationPathname = '/'
  })

  it('triggers refresh invalidation', async () => {
    render(<Header />)

    const refreshButton = screen.getByLabelText('刷新数据')
    await act(async () => {
      refreshButton.click()
      await Promise.resolve()
    })

    expect(invalidateQueries).toHaveBeenCalled()
    expect(mockAddToast).toHaveBeenCalledWith({ title: '数据已刷新', color: 'success' })
  })

  it('shows a danger toast when refresh invalidation fails', async () => {
    invalidateQueries.mockRejectedValueOnce(new Error('refresh failed'))
    render(<Header />)

    const refreshButton = screen.getByLabelText('刷新数据')
    await act(async () => {
      refreshButton.click()
      await Promise.resolve()
    })

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '刷新失败',
      description: 'refresh failed',
      color: 'danger',
    })
  })

  it('hides settings menu item for non-admin users', () => {
    useIsAdminMock.mockReturnValue(false)
    render(<Header />)

    expect(screen.queryByText('设置')).toBeNull()
    expect(screen.getByText('帮助文档')).toBeTruthy()
  })

  it('navigates admins to the alerts settings entry from the bell button', () => {
    render(<Header />)

    screen.getByLabelText('系统提醒设置').click()

    expect(navigateMock).toHaveBeenCalledWith('/settings?tab=advanced')
  })

  it('shows a warning when non-admin users click the alerts settings button', () => {
    useIsAdminMock.mockReturnValue(false)
    render(<Header />)

    screen.getByLabelText('系统提醒设置').click()

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '系统提醒设置仅管理员可用',
      color: 'warning',
    })
    expect(navigateMock).not.toHaveBeenCalledWith('/settings?tab=advanced')
  })

  it('opens project docs when the help item is clicked', () => {
    render(<Header />)

    screen.getByRole('button', { name: '帮助文档' }).click()

    expect(openUrlInNewTabMock).toHaveBeenCalledWith('https://github.com/seanbao/mnemonas/tree/main/docs')
  })

  it('replaces search history when quick search is submitted from the search page', () => {
    locationPathname = '/search'
    render(<Header />)

    const searchInput = screen.getByPlaceholderText('搜索文件与记忆')
    fireEvent.change(searchInput, { target: { value: 'report' } })
    fireEvent.keyDown(searchInput, { key: 'Enter' })

    expect(navigateMock).toHaveBeenCalledWith('/search?q=report', { replace: true })
  })

  it('shows a warning toast when the browser blocks the docs tab', () => {
    openUrlInNewTabMock.mockReturnValue(false)
    render(<Header />)

    screen.getByRole('button', { name: '帮助文档' }).click()

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '浏览器拦截了新标签页，请允许弹窗后重试',
      color: 'warning',
    })
  })
})
