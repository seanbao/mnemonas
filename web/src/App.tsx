import { Suspense, lazy, useEffect } from 'react'
import { BrowserRouter, Routes, Route } from 'react-router-dom'
import { AppLayout } from '@/components/layout'
import { ProtectedRoute } from '@/components/auth'
import { useAuthStore } from '@/stores/auth'

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
      <div className="rounded-lg border border-divider bg-content2/60 px-4 py-3 text-sm">加载中...</div>
    </div>
  )
}

function App() {
  const initialize = useAuthStore((state) => state.initialize)

  // Initialize auth state on app mount
  useEffect(() => {
    initialize()
  }, [initialize])

  return (
    <BrowserRouter>
      <Suspense fallback={<RouteFallback />}>
        <Routes>
          {/* Public routes */}
          <Route path="/login" element={<LoginPage />} />
          <Route path="/s/:id" element={<ShareAccessPage />} />

          {/* Protected routes */}
          <Route
            path="/"
            element={
              <ProtectedRoute>
                <AppLayout />
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
    </BrowserRouter>
  )
}

export default App
