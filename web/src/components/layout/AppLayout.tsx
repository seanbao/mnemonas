import { useState } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import { Folder, Image, Search, Star, Home } from 'lucide-react'
import { Sidebar } from './Sidebar'
import { Header } from './Header'
import { cn } from '@/lib/utils'

const mobilePrimaryNav = [
  { label: '首页', path: '/', icon: Home, end: true },
  { label: '文件', path: '/files', icon: Folder },
  { label: '搜索', path: '/search', icon: Search },
  { label: '相册', path: '/album', icon: Image },
  { label: '收藏', path: '/favorites', icon: Star },
]

function MobileTabBar() {
  return (
    <nav
      aria-label="移动端主导航"
      className="mobile-tabbar z-30 shrink-0 border-t border-divider bg-content1/95 px-2 pt-1.5 pb-[calc(env(safe-area-inset-bottom)+0.375rem)] shadow-[0_-10px_24px_rgba(15,23,42,0.08)] backdrop-blur lg:hidden"
    >
      <div className="mx-auto grid max-w-md grid-cols-5 gap-1">
        {mobilePrimaryNav.map((item) => (
          <NavLink
            key={item.path}
            to={item.path}
            end={item.end}
            className={({ isActive }) => cn(
              'flex h-14 min-w-0 flex-col items-center justify-center gap-1 rounded-lg px-1 text-[11px] font-medium transition-colors',
              isActive
                ? 'bg-primary/10 text-primary'
                : 'text-default-500 hover:bg-content2 hover:text-foreground'
            )}
          >
            <item.icon size={19} className="shrink-0" />
            <span className="max-w-full truncate leading-none">{item.label}</span>
          </NavLink>
        ))}
      </div>
    </nav>
  )
}

export function AppLayout() {
  const [sidebarOpen, setSidebarOpen] = useState(false)

  return (
    <div className="app-shell flex h-dvh overflow-hidden font-sans text-foreground">
      {/* Mobile sidebar overlay */}
      {sidebarOpen && (
        <button
          type="button"
          aria-label="关闭导航遮罩"
          className="fixed inset-0 z-40 bg-black/45 backdrop-blur-sm lg:hidden"
          onClick={() => setSidebarOpen(false)}
        />
      )}
      
      {/* Sidebar */}
      <div
        className={cn(
          'fixed inset-y-0 left-0 z-50 transform transition-transform duration-300 ease-in-out lg:static lg:z-auto',
          sidebarOpen
            ? 'visible translate-x-0 pointer-events-auto'
            : 'invisible -translate-x-full pointer-events-none lg:visible lg:translate-x-0 lg:pointer-events-auto'
        )}
      >
        <Sidebar onClose={() => setSidebarOpen(false)} />
      </div>

      {/* Main content */}
      <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
        <Header onMenuClick={() => setSidebarOpen(true)} />
        
        <main className="min-w-0 flex-1 overflow-auto pb-4 scroll-pb-4 scroll-pt-4 lg:pb-0 lg:scroll-pb-0">
          <div className="mx-auto min-h-full w-full max-w-7xl">
            <Outlet />
          </div>
        </main>

        <MobileTabBar />
      </div>
    </div>
  )
}
