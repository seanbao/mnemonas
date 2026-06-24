import { Component, Suspense, lazy, useEffect, useRef, type ErrorInfo, type ReactNode } from 'react'
import { BrowserRouter, Routes, Route, useLocation } from 'react-router-dom'
import { AppLayout } from '@/components/layout'
import { PasswordChangeGate, ProtectedRoute } from '@/components/auth'
import { useAuthStore } from '@/stores/auth'
import { shouldValidateSessionOnInitialRoute } from '@/lib/authInitialRoute'
import { routeRenderDiagnosticMessage } from '@/lib/routeDiagnostics'

const DashboardPage = lazy(() => import('@/pages/Dashboard').then((mod) => ({ default: mod.DashboardPage })))
const FilesPage = lazy(() => import('@/pages/Files').then((mod) => ({ default: mod.FilesPage })))
const AlbumPage = lazy(() => import('@/pages/Album').then((mod) => ({ default: mod.AlbumPage })))
const VersionsPage = lazy(() => import('@/pages/Versions').then((mod) => ({ default: mod.VersionsPage })))
const TrashPage = lazy(() => import('@/pages/Trash').then((mod) => ({ default: mod.TrashPage })))
const FavoritesPage = lazy(() => import('@/pages/Favorites').then((mod) => ({ default: mod.FavoritesPage })))
const StoragePage = lazy(() => import('@/pages/Storage').then((mod) => ({ default: mod.StoragePage })))
const HealthPage = lazy(() => import('@/pages/Health').then((mod) => ({ default: mod.HealthPage })))
const MaintenancePage = lazy(() => import('@/pages/Maintenance'))
const SettingsPage = lazy(() => import('@/pages/Settings').then((mod) => ({ default: mod.SettingsPage })))
const NotFoundPage = lazy(() => import('@/pages/NotFound').then((mod) => ({ default: mod.NotFoundPage })))
const LoginPage = lazy(() => import('@/pages/Login').then((mod) => ({ default: mod.LoginPage })))
const ShareAccessPage = lazy(() => import('@/pages/ShareAccess').then((mod) => ({ default: mod.ShareAccessPage })))
const UsersPage = lazy(() => import('@/pages/Users').then((mod) => ({ default: mod.UsersPage })))
const SearchPage = lazy(() => import('@/pages/Search').then((mod) => ({ default: mod.SearchPage })))
const ActivityPage = lazy(() => import('@/pages/Activity').then((mod) => ({ default: mod.ActivityPage })))

function RouteFallback() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-content1 px-6 text-default-500">
      <div className="rounded-lg border border-divider bg-content2/60 px-4 py-3 text-sm">加载中…</div>
    </div>
  )
}

function RouteErrorFallback() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-content1 px-6">
      <div className="max-w-sm rounded-lg border border-divider bg-content2/70 p-5 text-center shadow-[var(--shadow-soft)]">
        <h1 className="text-lg font-semibold text-foreground">页面加载失败</h1>
        <p className="mt-2 text-sm text-default-600">当前页面渲染时发生异常，请刷新后重试。</p>
        <button
          type="button"
          className="mt-4 rounded-lg bg-accent-primary px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-accent-primary/90"
          onClick={() => window.location.reload()}
        >
          重新加载
        </button>
      </div>
    </div>
  )
}

interface RouteErrorBoundaryProps {
  children: ReactNode
  resetKey: string
}

interface RouteErrorBoundaryState {
  hasError: boolean
}

class RouteErrorBoundary extends Component<RouteErrorBoundaryProps, RouteErrorBoundaryState> {
  state: RouteErrorBoundaryState = { hasError: false }

  static getDerivedStateFromError(): RouteErrorBoundaryState {
    return { hasError: true }
  }

  componentDidCatch(error: unknown, errorInfo: ErrorInfo) {
    if (import.meta.env.DEV) {
      console.error('Route render failed', routeRenderDiagnosticMessage(error), errorInfo.componentStack)
    }
  }

  componentDidUpdate(prevProps: RouteErrorBoundaryProps) {
    if (this.state.hasError && prevProps.resetKey !== this.props.resetKey) {
      this.setState({ hasError: false })
    }
  }

  render() {
    if (this.state.hasError) {
      return <RouteErrorFallback />
    }

    return this.props.children
  }
}

function AppRoutes() {
  const location = useLocation()

  return (
    <RouteErrorBoundary resetKey={`${location.pathname}${location.search}`}>
      <Suspense fallback={<RouteFallback />}>
        <Routes>
          {/* Public routes */}
          <Route path="/login" element={<LoginPage />} />
          <Route path="/s" element={<ShareAccessPage />} />
          <Route path="/s/:id" element={<ShareAccessPage />} />

          {/* Protected routes */}
          <Route
            path="/"
            element={
              <ProtectedRoute>
                <PasswordChangeGate>
                  <AppLayout />
                </PasswordChangeGate>
              </ProtectedRoute>
            }
          >
            <Route index element={<DashboardPage />} />
            <Route path="files/*" element={<FilesPage />} />
            <Route path="album" element={<AlbumPage />} />
            <Route path="versions/*" element={<VersionsPage />} />
            <Route path="trash" element={<TrashPage />} />
            <Route path="favorites" element={<FavoritesPage />} />
            <Route
              path="storage"
              element={
                <ProtectedRoute adminOnly>
                  <StoragePage />
                </ProtectedRoute>
              }
            />
            <Route
              path="system-health"
              element={
                <ProtectedRoute adminOnly>
                  <HealthPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="maintenance"
              element={
                <ProtectedRoute adminOnly>
                  <MaintenancePage />
                </ProtectedRoute>
              }
            />
            <Route
              path="users"
              element={
                <ProtectedRoute adminOnly>
                  <UsersPage />
                </ProtectedRoute>
              }
            />
            <Route path="search" element={<SearchPage />} />
            <Route path="activity" element={<ActivityPage />} />
            <Route
              path="settings"
              element={
                <ProtectedRoute adminOnly>
                  <SettingsPage />
                </ProtectedRoute>
              }
            />
            <Route path="*" element={<NotFoundPage />} />
          </Route>
        </Routes>
      </Suspense>
    </RouteErrorBoundary>
  )
}

function AuthInitializer() {
  const initialize = useAuthStore((state) => state.initialize)
  const location = useLocation()
  const initialPathname = useRef(location.pathname)

  useEffect(() => {
    void initialize({
      validateSession: shouldValidateSessionOnInitialRoute(initialPathname.current),
    })
  }, [initialize])

  return null
}

function App() {
  return (
    <BrowserRouter>
      <AuthInitializer />
      <AppRoutes />
    </BrowserRouter>
  )
}

export default App
