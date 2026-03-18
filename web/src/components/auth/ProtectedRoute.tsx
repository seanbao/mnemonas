import { Link, Navigate, useLocation } from 'react-router-dom'
import { useAuthStore, useIsAdmin, useIsAuthenticated } from '@/stores/auth'

interface ProtectedRouteProps {
  children: React.ReactNode
  adminOnly?: boolean
}

/**
 * Route guard that redirects to login if not authenticated.
 * Preserves the attempted location for redirect after login.
 */
export function ProtectedRoute({ children, adminOnly = false }: ProtectedRouteProps) {
  const isAuthenticated = useIsAuthenticated()
  const isAdmin = useIsAdmin()
  const { isLoading, authEnabled } = useAuthStore()
  const location = useLocation()

  // Show loading spinner while checking auth state
  if (isLoading) {
    return (
      <div className="min-h-screen bg-background flex items-center justify-center">
        <div className="text-center">
          <div className="w-12 h-12 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-4" />
          <p className="text-default-500">加载中…</p>
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
    const attemptedPath = `${location.pathname}${location.search}${location.hash}`
    return <Navigate to="/login" state={{ from: attemptedPath }} replace />
  }

  if (adminOnly && !isAdmin) {
    return (
      <div className="min-h-screen bg-background flex items-center justify-center p-6">
        <div className="max-w-md w-full text-center bg-content1 border border-divider rounded-lg shadow-sm p-8 space-y-4">
          <div className="w-12 h-12 rounded-full bg-warning/10 text-warning flex items-center justify-center mx-auto text-xl font-semibold">
            403
          </div>
          <div className="space-y-2">
            <h1 className="text-xl font-semibold text-foreground">访问被拒绝</h1>
            <p className="text-default-500">当前账户没有访问此页面的权限。</p>
          </div>
          <Link
            to="/"
            className="inline-flex items-center justify-center rounded-lg bg-primary px-4 py-2 text-primary-foreground font-medium"
          >
            返回首页
          </Link>
        </div>
      </div>
    )
  }

  return <>{children}</>
}
