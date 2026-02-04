import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { render, screen } from '@testing-library/react'
import { ProtectedRoute } from './ProtectedRoute'

const useAuthStoreMock = vi.fn()
const useIsAuthenticatedMock = vi.fn()
const useIsAdminMock = vi.fn()

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => useAuthStoreMock(),
  useIsAuthenticated: () => useIsAuthenticatedMock(),
  useIsAdmin: () => useIsAdminMock(),
}))

function LoginRedirectProbe() {
  const location = useLocation()
  return <div>{(location.state as { from?: string } | null)?.from ?? 'missing'}</div>
}

describe('ProtectedRoute', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows a loading state while auth is being checked', () => {
    useAuthStoreMock.mockReturnValue({ isLoading: true, authEnabled: true })
    useIsAuthenticatedMock.mockReturnValue(false)
    useIsAdminMock.mockReturnValue(false)

    render(
      <MemoryRouter>
        <ProtectedRoute>
          <div>private</div>
        </ProtectedRoute>
      </MemoryRouter>
    )

    expect(screen.getByText('加载中…')).toBeInTheDocument()
    expect(screen.queryByText('private')).not.toBeInTheDocument()
  })

  it('allows access when server auth is disabled', () => {
    useAuthStoreMock.mockReturnValue({ isLoading: false, authEnabled: false })
    useIsAuthenticatedMock.mockReturnValue(false)
    useIsAdminMock.mockReturnValue(false)

    render(
      <MemoryRouter>
        <ProtectedRoute>
          <div>private</div>
        </ProtectedRoute>
      </MemoryRouter>
    )

    expect(screen.getByText('private')).toBeInTheDocument()
  })

  it('renders children for authenticated admin-only access', () => {
    useAuthStoreMock.mockReturnValue({ isLoading: false, authEnabled: true })
    useIsAuthenticatedMock.mockReturnValue(true)
    useIsAdminMock.mockReturnValue(true)

    render(
      <MemoryRouter>
        <ProtectedRoute adminOnly>
          <div>settings</div>
        </ProtectedRoute>
      </MemoryRouter>
    )

    expect(screen.getByText('settings')).toBeInTheDocument()
  })

  it('preserves query and hash in the post-login redirect target', () => {
    useAuthStoreMock.mockReturnValue({ isLoading: false, authEnabled: true })
    useIsAuthenticatedMock.mockReturnValue(false)
    useIsAdminMock.mockReturnValue(false)

    render(
      <MemoryRouter initialEntries={["/files/report?view=grid#preview"]}>
        <Routes>
          <Route path="/login" element={<LoginRedirectProbe />} />
          <Route
            path="*"
            element={
              <ProtectedRoute>
                <div>private</div>
              </ProtectedRoute>
            }
          />
        </Routes>
      </MemoryRouter>
    )

    expect(screen.getByText('/files/report?view=grid#preview')).toBeInTheDocument()
  })

  it('shows an explicit access denied state for non-admin admin-only route access', () => {
    useAuthStoreMock.mockReturnValue({ isLoading: false, authEnabled: true })
    useIsAuthenticatedMock.mockReturnValue(true)
    useIsAdminMock.mockReturnValue(false)

    render(
      <MemoryRouter initialEntries={["/settings"]}>
        <Routes>
          <Route
            path="/settings"
            element={
              <ProtectedRoute adminOnly>
                <div>settings</div>
              </ProtectedRoute>
            }
          />
        </Routes>
      </MemoryRouter>
    )

    expect(screen.getByText('访问被拒绝')).toBeInTheDocument()
    expect(screen.getByText('当前账户没有访问此页面的权限。')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '返回首页' })).toHaveAttribute('href', '/')
    expect(screen.queryByText('settings')).not.toBeInTheDocument()
  })
})
