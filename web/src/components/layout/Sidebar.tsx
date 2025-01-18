import { Link, useLocation } from 'react-router-dom'
import { 
  Folder, 
  Image, 
  History, 
  Trash2, 
  HardDrive, 
  ShieldCheck, 
  Activity, 
  Settings,
  Archive
} from 'lucide-react'
import { cn } from '@/lib/utils'

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
      { icon: History, label: '时光回溯', path: '/versions', badge: '核心', badgeColor: 'bg-gradient-to-r from-rose to-accent-primary' },
    ]
  },
  {
    title: '管理',
    items: [
      { icon: Trash2, label: '回收站', path: '/trash' },
      { icon: HardDrive, label: '存储', path: '/storage' },
      { icon: ShieldCheck, label: '守护', path: '/maintenance' },
    ]
  },
  {
    title: '系统',
    items: [
      { icon: Activity, label: '健康', path: '/health' },
      { icon: Settings, label: '设置', path: '/settings' },
    ]
  }
]

interface SidebarProps {
  collapsed?: boolean
}

export function Sidebar({ collapsed = false }: SidebarProps) {
  const location = useLocation()

  return (
    <aside 
      className={cn(
        "h-screen bg-bg-secondary border-r border-divider flex flex-col transition-all duration-300 relative overflow-hidden sidebar-glow",
        collapsed ? "w-16" : "w-[260px]"
      )}
    >
      {/* Starry Background */}
      <div className="stars-bg" />

      {/* Logo */}
      <div className="p-6 flex items-center gap-3.5 border-b border-divider relative z-10">
        <div className="w-[42px] h-[42px] rounded-xl bg-gradient-to-br from-accent-primary to-accent-dark flex items-center justify-center logo-glow flex-shrink-0">
          <Archive size={20} className="text-white" />
        </div>
        {!collapsed && (
          <div>
            <div className="font-bold text-lg tracking-tight text-text-primary">
              <span className="bg-gradient-to-br from-accent-light to-moonlight bg-clip-text text-transparent">Mnemo</span>NAS
            </div>
            <div className="text-[10px] text-text-muted tracking-widest uppercase mt-0.5">
              Memory Palace
            </div>
          </div>
        )}
      </div>

      {/* Navigation */}
      <nav className="flex-1 py-5 px-3 overflow-y-auto relative z-10 custom-scrollbar">
        {navSections.map((section) => (
          <div key={section.title} className={cn("mb-7", collapsed && "mb-4")}>
            {!collapsed && (
              <div className="px-3.5 mb-2.5 text-[10px] font-semibold uppercase tracking-widest text-text-muted">
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
                        "flex items-center gap-3 px-3.5 py-2.5 rounded-[10px] transition-all duration-200 group",
                        isActive 
                          ? "nav-item-active" 
                          : "text-text-secondary hover:bg-bg-hover hover:text-text-primary"
                      )}
                      title={collapsed ? item.label : undefined}
                    >
                      <item.icon 
                        size={20} 
                        className={cn(
                          "flex-shrink-0 transition-colors",
                          isActive ? "text-accent-light" : "text-text-secondary group-hover:text-text-primary"
                        )} 
                      />
                      {!collapsed && (
                        <>
                          <span className="text-[13px] font-medium flex-1">
                            {item.label}
                          </span>
                          {item.badge && (
                            <span className={cn(
                              "px-2 py-0.5 text-[10px] font-semibold rounded-md text-white shadow-sm",
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

      {/* Storage Status (Simplified for now) */}
      {!collapsed && (
        <div className="p-5 border-t border-divider bg-bg-secondary/50 relative z-10">
          <div className="flex items-center justify-between text-xs mb-2">
            <span className="text-text-muted">存储空间</span>
            <span className="text-accent-light font-medium">60%</span>
          </div>
          <div className="h-1.5 bg-bg-card rounded-full overflow-hidden">
            <div className="h-full w-3/5 bg-gradient-to-r from-accent-primary to-aurora rounded-full shadow-[0_0_8px_rgba(167,139,250,0.4)]" />
          </div>
        </div>
      )}
    </aside>
  )
}
