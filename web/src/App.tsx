import { BrowserRouter, Routes, Route } from 'react-router-dom'
import { AppLayout } from '@/components/layout'
import { 
  DashboardPage, 
  FilesPage, 
  AlbumPage, 
  VersionsPage, 
  TrashPage,
  StoragePage,
  HealthPage,
  MaintenancePage,
  SettingsPage,
  NotFoundPage
} from '@/pages'

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<AppLayout />}>
          <Route index element={<DashboardPage />} />
          <Route path="files/*" element={<FilesPage />} />
          <Route path="album" element={<AlbumPage />} />
          <Route path="versions/*" element={<VersionsPage />} />
          <Route path="trash" element={<TrashPage />} />
          <Route path="storage" element={<StoragePage />} />
          <Route path="health" element={<HealthPage />} />
          <Route path="maintenance" element={<MaintenancePage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}

export default App
