import { describe, expect, it, vi } from 'vitest'
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

  it('redirects non-admin users away from admin-only routes', () => {
    useAuthStoreMock.mockReturnValue({ isLoading: false, authEnabled: true })
    useIsAuthenticatedMock.mockReturnValue(true)
    useIsAdminMock.mockReturnValue(false)

    render(
      <MemoryRouter initialEntries={["/settings"]}>
        <Routes>
          <Route path="/" element={<div>home</div>} />
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

    expect(screen.getByText('home')).toBeInTheDocument()
    expect(screen.queryByText('settings')).not.toBeInTheDocument()
  })
})