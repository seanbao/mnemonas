import { Navigate, useLocation } from 'react-router-dom'
import { Spinner } from '@heroui/react'
import { useAuthStore, useIsAuthenticated } from '@/stores/auth'

interface ProtectedRouteProps {
  children: React.ReactNode
}

/**
 * Route guard that redirects to login if not authenticated.
 * Preserves the attempted location for redirect after login.
 */
export function ProtectedRoute({ children }: ProtectedRouteProps) {
  const isAuthenticated = useIsAuthenticated()
  const { isLoading, authEnabled } = useAuthStore()
  const location = useLocation()

  // Show loading spinner while checking auth state
  if (isLoading) {
    return (
      <div className="min-h-screen bg-background flex items-center justify-center">
        <div className="text-center">
          <Spinner size="lg" color="secondary" />
          <p className="text-default-500 mt-4">加载中...</p>
        </div>
      </div>
    )
  }

  // If auth is explicitly disabled on server, allow access
  if (!authEnabled) {
    return <>{children}</>
  }

  // If not authenticated, redirect to login
  if (!isAuthenticated) {
    return <Navigate to="/login" state={{ from: location.pathname }} replace />
  }

  return <>{children}</>
}
