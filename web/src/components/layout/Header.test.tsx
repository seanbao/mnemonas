import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import React from 'react'
import { Header } from './Header'

const invalidateQueries = vi.fn().mockResolvedValue(undefined)
const useIsAdminMock = vi.fn(() => true)

vi.mock('@tanstack/react-query', () => ({
  useQueryClient: () => ({ invalidateQueries }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ logout: vi.fn().mockResolvedValue(undefined) }),
  useUser: () => ({ username: 'admin', email: 'admin@local' }),
  useIsAdmin: () => useIsAdminMock(),
}))

vi.mock('react-router-dom', () => ({
  useNavigate: () => vi.fn(),
}))

vi.mock('@/components/ThemeToggle', () => ({
  ThemeToggle: () => <div data-testid="theme-toggle" />,
}))

vi.mock('@heroui/react', () => ({
  Button: ({ children, onPress, isLoading, 'aria-label': ariaLabel }: { children: React.ReactNode; onPress?: () => void; isLoading?: boolean; 'aria-label'?: string }) => (
    <button onClick={onPress} disabled={isLoading} aria-label={ariaLabel}>{children}</button>
  ),
  Avatar: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  Dropdown: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownTrigger: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownItem: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  addToast: vi.fn(),
}))

describe('Header', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useIsAdminMock.mockReturnValue(true)
  })

  it('triggers refresh invalidation', async () => {
    render(<Header />)

    const refreshButton = screen.getByLabelText('刷新数据')
    await act(async () => {
      refreshButton.click()
      await Promise.resolve()
    })

    expect(invalidateQueries).toHaveBeenCalled()
  })

  it('hides settings menu item for non-admin users', () => {
    useIsAdminMock.mockReturnValue(false)
    render(<Header />)

    expect(screen.queryByText('设置')).toBeNull()
    expect(screen.getByText('帮助文档')).toBeTruthy()
  })
})
