import { useEffect } from 'react'
import { BrowserRouter, Routes, Route } from 'react-router-dom'
import { AppLayout } from '@/components/layout'
import { ProtectedRoute } from '@/components/auth'
import { 
  DashboardPage, 
  FilesPage, 
  AlbumPage, 
  VersionsPage, 
  TrashPage,
  FavoritesPage,
  StoragePage,
  HealthPage,
  MaintenancePage,
  SettingsPage,
  NotFoundPage,
  LoginPage,
  ShareAccessPage,
  UsersPage,
  SearchPage,
  ActivityPage,
} from '@/pages'
import { useAuthStore } from '@/stores/auth'

function App() {
  const initialize = useAuthStore((state) => state.initialize)

  // Initialize auth state on app mount
  useEffect(() => {
    initialize()
  }, [initialize])

  return (
    <BrowserRouter>
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
          <Route path="storage" element={<StoragePage />} />
          <Route path="system-health" element={<HealthPage />} />
          <Route path="maintenance" element={<MaintenancePage />} />
          <Route path="users" element={<UsersPage />} />
          <Route path="search" element={<SearchPage />} />
          <Route path="activity" element={<ActivityPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}

export default App
