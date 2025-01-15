import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import React from 'react'
import { Header } from './Header'

const invalidateQueries = vi.fn().mockResolvedValue(undefined)

vi.mock('@tanstack/react-query', () => ({
  useQueryClient: () => ({ invalidateQueries }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ logout: vi.fn().mockResolvedValue(undefined) }),
  useUser: () => ({ username: 'admin', email: 'admin@local' }),
}))

vi.mock('react-router-dom', () => ({
  useNavigate: () => vi.fn(),
}))

vi.mock('@/components/ThemeToggle', () => ({
  ThemeToggle: () => <div data-testid="theme-toggle" />,
}))

vi.mock('@heroui/react', () => ({
  Button: ({ children, onPress, ...props }: { children: React.ReactNode; onPress?: () => void }) => (
    <button onClick={onPress} {...props}>{children}</button>
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
  })

  it('triggers refresh invalidation', async () => {
    render(<Header />)

    const refreshButton = screen.getByLabelText('刷新数据')
    refreshButton.click()

    expect(invalidateQueries).toHaveBeenCalled()
  })
})
