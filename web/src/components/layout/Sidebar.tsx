import { Link, useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useIsAdmin } from '@/stores/auth'
import { Button, addToast } from '@heroui/react'
import { 
  Folder, 
  Image, 
  History, 
  Search,
  Trash2, 
  HardDrive, 
  ShieldCheck, 
  Activity, 
  Settings,
  Archive,
  Users,
  FileText,
  X,
  Star,
  AlertCircle,
  RefreshCw,
} from 'lucide-react'
import { cn, formatBytes } from '@/lib/utils'
import { areDiskStatsAvailable, clampUsagePercent } from '@/lib/storageStats'
import { ApiError, getAppVersion, getStorageStats } from '@/api/files'

interface NavItem {
  icon: React.ComponentType<{ size?: number; className?: string }>
  label: string
  path: string
  badge?: string
  badgeColor?: string
  adminOnly?: boolean
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
      { icon: Search, label: '搜索', path: '/search' },
      { icon: Image, label: '相册', path: '/album' },
      { icon: Star, label: '收藏', path: '/favorites' },
      { icon: History, label: '时光回溯', path: '/versions', badge: '核心', badgeColor: 'bg-accent-primary/15 text-accent-primary' },
    ]
  },
  {
    title: '管理',
    items: [
      { icon: Trash2, label: '回收站', path: '/trash' },
      { icon: HardDrive, label: '存储', path: '/storage', adminOnly: true },
      { icon: ShieldCheck, label: '守护', path: '/maintenance', adminOnly: true },
      { icon: Users, label: '用户', path: '/users', adminOnly: true },
    ]
  },
  {
    title: '系统',
    items: [
      { icon: Activity, label: '健康', path: '/system-health', adminOnly: true },
      { icon: FileText, label: '活动', path: '/activity' },
      { icon: Settings, label: '设置', path: '/settings', adminOnly: true },
    ]
  }
]

interface SidebarProps {
  collapsed?: boolean
  onClose?: () => void
}

function getSidebarStorageErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: '统计暂不可用',
      description: '存储统计服务当前不可用。',
    }
  }

  return {
    title: '统计加载失败',
    description: (error as Error).message || '请稍后重试',
  }
}

function getSidebarStorageRetryErrorToast(error: unknown): { title: string; description: string; color: 'warning' | 'danger' } {
  const presentation = getSidebarStorageErrorPresentation(error)
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      ...presentation,
      color: 'warning',
    }
  }

  return {
    ...presentation,
    color: 'danger',
  }
}

export function Sidebar({ collapsed = false, onClose }: SidebarProps) {
  const location = useLocation()
  const isAdmin = useIsAdmin()
  
  // Fetch storage stats for the sidebar indicator
  const { data: storageStats, error: storageStatsError, refetch: refetchStorageStats, isRefetching: isRefetchingStorageStats } = useQuery({
    queryKey: ['storage-stats-sidebar'],
    queryFn: getStorageStats,
    enabled: isAdmin && !collapsed,
    staleTime: 1000 * 60 * 5, // Cache for 5 minutes
    refetchInterval: 1000 * 60 * 5, // Refresh every 5 minutes
  })
  const { data: appVersion } = useQuery({
    queryKey: ['app-version'],
    queryFn: getAppVersion,
    enabled: !collapsed,
    staleTime: 1000 * 60 * 10,
  })

  const diskStatsKnown = areDiskStatsAvailable(storageStats)
  const casStatsKnown = storageStats?.storageStatsAvailable ?? (
    storageStats?.totalSize !== undefined
    || storageStats?.dedupRatio !== undefined
  )
  const usedBytes = diskStatsKnown ? storageStats?.diskUsed : casStatsKnown ? storageStats?.totalSize : undefined
  const totalBytes = diskStatsKnown ? storageStats?.diskTotal : undefined
  const availableBytes = diskStatsKnown ? storageStats?.diskAvailable : undefined
  const usagePercent = diskStatsKnown ? clampUsagePercent(storageStats?.diskUsageRatio) : undefined
  const hasUsage = usedBytes !== undefined && usedBytes > 0
  const storageStatsKnown = diskStatsKnown || casStatsKnown
  const storageErrorPresentation = storageStatsError ? getSidebarStorageErrorPresentation(storageStatsError) : null
  const versionLabel = appVersion?.version ? `MnemoNAS ${appVersion.version}` : 'MnemoNAS'

  const handleRetryStorageStats = async () => {
    const result = await refetchStorageStats()
    if (result.error) {
      addToast(getSidebarStorageRetryErrorToast(result.error))
      return
    }
    addToast({ title: '存储统计已刷新', color: 'success' })
  }

  return (
    <aside 
      className={cn(
        "sidebar-surface flex h-dvh flex-col overflow-hidden border-r border-divider transition-all duration-300",
        collapsed ? "w-16" : "w-[calc(100vw-2rem)] max-w-72 lg:w-[264px]"
      )}
    >
      {/* Logo */}
      <div className="relative z-10 flex items-center justify-between border-b border-divider p-4">
        <div className="flex items-center gap-3">
          <div className="relative">
            <div className="gradient-meridian logo-glow flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-lg">
              <Archive size={20} className="text-white" />
            </div>
          </div>
          {!collapsed && (
            <div>
              <div className="text-base font-semibold text-foreground">
                MnemoNAS
              </div>
              <div className="mt-0.5 text-[10px] uppercase text-default-500">
                Memory Palace
              </div>
            </div>
          )}
        </div>
        {onClose && (
          <button
            onClick={onClose}
            className="rounded-lg p-2 transition-colors hover:bg-content2 lg:hidden"
            aria-label="关闭导航菜单"
          >
            <X size={20} className="text-default-500" />
          </button>
        )}
      </div>

      {/* Navigation */}
      <nav className="relative z-10 min-h-0 flex-1 overflow-y-auto py-4 px-3 custom-scrollbar" aria-label="主导航">
        {navSections
          .map((section) => ({
            ...section,
            items: section.items.filter((item) => !item.adminOnly || isAdmin),
          }))
          .filter((section) => section.items.length > 0)
          .map((section) => (
          <div key={section.title} className={cn("mb-7", collapsed && "mb-4")}>
            {!collapsed && (
              <div className="mb-2 px-3.5 text-[10px] font-semibold uppercase text-default-500">
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
                      onClick={onClose}
                      className={cn(
                        "group flex items-center gap-3 rounded-lg px-3 py-2.5 transition-colors duration-150",
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

      {/* Storage Status - 存储状态底栏 */}
      {!collapsed && (
        <div className="relative z-10 border-t border-divider p-4">
          {isAdmin && (
            <div className="rounded-lg border border-divider bg-content2/55 p-4">
              <div className="mb-2 flex items-center gap-2">
                <HardDrive size={16} className="text-accent-primary" />
                <span className="text-sm font-medium">存储空间</span>
              </div>
              {storageStatsError ? (
                <div className="space-y-3 text-xs">
                  <div className="flex items-start gap-2 text-warning">
                    <AlertCircle size={14} className="mt-0.5 shrink-0" />
                    <div>
                      <div className="font-medium text-foreground">{storageErrorPresentation?.title}</div>
                      <div className="text-default-500">{storageErrorPresentation?.description}</div>
                    </div>
                  </div>
                  <Button
                    size="sm"
                    variant="bordered"
                    className="w-full rounded-lg"
                    onPress={handleRetryStorageStats}
                    isLoading={isRefetchingStorageStats}
                    startContent={!isRefetchingStorageStats ? <RefreshCw size={14} /> : undefined}
                  >
                    重新加载
                  </Button>
                </div>
              ) : (
                <div className="space-y-2 text-xs">
                  <div className="flex items-center justify-between">
                    <span className="text-default-500">已使用</span>
                    <span className="data-value text-accent-light font-medium">
                      {usedBytes !== undefined ? formatBytes(usedBytes) : '--'}
                    </span>
                  </div>
                  <div className="h-1.5 bg-content1 rounded-full overflow-hidden">
                    <div
                      className={cn(
                        "h-full bg-accent-primary rounded-full transition-all duration-500",
                        hasUsage ? "flow-line opacity-60" : "opacity-20"
                      )}
                      style={{ width: usagePercent !== undefined ? `${usagePercent}%` : hasUsage ? '100%' : '0%' }}
                    />
                  </div>
                  <div className="text-[11px] text-default-400">
                    {diskStatsKnown
                      ? `${availableBytes !== undefined ? formatBytes(availableBytes) : '--'} 可用 / ${totalBytes !== undefined ? formatBytes(totalBytes) : '--'} 总容量`
                      : storageStatsKnown ? '磁盘容量统计不可用' : '统计不可用'}
                  </div>
                  {casStatsKnown && storageStats?.dedupRatio !== undefined && storageStats.dedupRatio > 0 && (
                    <div className="flex items-center gap-2">
                      <div className="live-indicator scale-75" />
                      <span className="text-default-400">
                        去重率 {(storageStats.dedupRatio * 100).toFixed(1)}%
                      </span>
                    </div>
                  )}
                </div>
              )}
            </div>
          )}
          <p className="text-default-500 text-center text-[10px] mt-3">
            {versionLabel}
          </p>
        </div>
      )}
    </aside>
  )
}
