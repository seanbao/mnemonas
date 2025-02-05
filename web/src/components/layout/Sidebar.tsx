import { Link, useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { 
  Folder, 
  Image, 
  History, 
  Trash2, 
  HardDrive, 
  ShieldCheck, 
  Activity, 
  Settings,
  Archive,
  Users,
  FileText,
} from 'lucide-react'
import { cn, formatBytes } from '@/lib/utils'
import { getStorageStats } from '@/api/files'

interface NavItem {
  icon: React.ComponentType<{ size?: number; className?: string }>
  label: string
  path: string
  badge?: string
  badgeColor?: string
}

interface NavSection {
  title: string
  items: NavItem[]
}

const navSections: NavSection[] = [
  {
    title: '浏览',
    items: [
      { icon: Folder, label: '文件', path: '/files' },
      { icon: Image, label: '相册', path: '/album' },
      { icon: History, label: '时光回溯', path: '/versions', badge: '核心', badgeColor: 'bg-accent-primary/15 text-accent-primary' },
    ]
  },
  {
    title: '管理',
    items: [
      { icon: Trash2, label: '回收站', path: '/trash' },
      { icon: HardDrive, label: '存储', path: '/storage' },
      { icon: ShieldCheck, label: '守护', path: '/maintenance' },
      { icon: Users, label: '用户', path: '/users' },
    ]
  },
  {
    title: '系统',
    items: [
      { icon: Activity, label: '健康', path: '/health' },
      { icon: FileText, label: '活动', path: '/activity' },
      { icon: Settings, label: '设置', path: '/settings' },
    ]
  }
]

interface SidebarProps {
  collapsed?: boolean
}

export function Sidebar({ collapsed = false }: SidebarProps) {
  const location = useLocation()
  
  // Fetch storage stats for the sidebar indicator
  const { data: storageStats } = useQuery({
    queryKey: ['storage-stats-sidebar'],
    queryFn: getStorageStats,
    staleTime: 1000 * 60 * 5, // Cache for 5 minutes
    refetchInterval: 1000 * 60 * 5, // Refresh every 5 minutes
  })

  // Calculate storage usage percentage (assume 1TB total for now, or use dedup ratio as indicator)
  const totalCapacity = 1024 * 1024 * 1024 * 1024 // 1TB in bytes
  const usedBytes = storageStats?.totalSize ?? 0
  const usagePercent = Math.min(100, Math.round((usedBytes / totalCapacity) * 100))

  return (
    <aside 
      className={cn(
        "h-screen border-r border-divider flex flex-col transition-all duration-300 relative overflow-hidden sidebar-surface glass-strong",
        collapsed ? "w-16" : "w-[248px]"
      )}
    >
      {/* Logo */}
      <div className="p-5 flex items-center gap-3 border-b border-divider relative z-10">
        <div className="w-10 h-10 rounded-xl gradient-meridian flex items-center justify-center logo-glow flex-shrink-0">
          <Archive size={20} className="text-white" />
        </div>
        {!collapsed && (
          <div>
            <div className="font-semibold text-base tracking-tight text-gradient-meridian">
              MnemoNAS
            </div>
            <div className="text-[10px] text-default-500 tracking-widest uppercase mt-0.5">
              Memory Palace
            </div>
          </div>
        )}
      </div>

      {/* Navigation */}
      <nav className="flex-1 py-4 px-3 overflow-y-auto relative z-10 custom-scrollbar">
        {navSections.map((section) => (
          <div key={section.title} className={cn("mb-7", collapsed && "mb-4")}>
            {!collapsed && (
              <div className="px-3.5 mb-2 text-[10px] font-semibold uppercase tracking-widest text-default-500">
                {section.title}
              </div>
            )}
            <ul className="space-y-1">
              {section.items.map((item) => {
                const isActive = location.pathname === item.path || 
                  (item.path !== '/' && location.pathname.startsWith(item.path))
                
                return (
                  <li key={item.path}>
                    <Link
                      to={item.path}
                      className={cn(
                        "flex items-center gap-3 px-3 py-2.5 rounded-[10px] transition-all duration-200 group",
                        isActive 
                          ? "nav-item-active" 
                          : "text-default-600 hover:bg-content2 hover:text-foreground"
                      )}
                      title={collapsed ? item.label : undefined}
                    >
                      <item.icon 
                        size={20} 
                        className={cn(
                          "flex-shrink-0 transition-colors",
                          isActive ? "text-accent-light" : "text-default-600 group-hover:text-foreground"
                        )} 
                      />
                      {!collapsed && (
                        <>
                          <span className="text-[13px] font-medium flex-1">
                            {item.label}
                          </span>
                          {item.badge && (
                            <span className={cn(
                              "px-2 py-0.5 text-[10px] font-medium rounded-md",
                              item.badgeColor || "bg-accent-primary"
                            )}>
                              {item.badge}
                            </span>
                          )}
                        </>
                      )}
                    </Link>
                  </li>
                )
              })}
            </ul>
          </div>
        ))}
      </nav>

      {/* Storage Status */}
      {!collapsed && (
        <div className="p-4 border-t border-divider glass relative z-10">
          <div className="flex items-center justify-between text-xs mb-2">
            <span className="text-default-500">存储空间</span>
            <span className="text-accent-light font-medium">
              {storageStats ? formatBytes(usedBytes) : '--'}
            </span>
          </div>
          <div className="h-1.5 bg-content1 rounded-full overflow-hidden">
            <div 
              className="h-full bg-accent-primary rounded-full transition-all duration-500" 
              style={{ width: `${Math.max(5, usagePercent)}%` }}
            />
          </div>
          {storageStats && storageStats.dedupRatio > 1 && (
            <div className="mt-1.5 text-[10px] text-default-500">
              去重率 {((1 - 1 / storageStats.dedupRatio) * 100).toFixed(1)}%
            </div>
          )}
        </div>
      )}
    </aside>
  )
}
