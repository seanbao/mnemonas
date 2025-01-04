import { useQuery } from '@tanstack/react-query'
import { Card, CardBody, Skeleton, Button } from '@heroui/react'
import { 
  HardDrive, 
  FileBox, 
  Activity, 
  Clock,
  CheckCircle2,
  AlertCircle,
  ArrowRight,
  Upload,
  Download,
  Trash2,
  Edit3,
  Share2,
  LogIn,
  LogOut,
  FolderPlus,
  RotateCcw,
  Move,
} from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { getHealth, getStorageStats } from '@/api/files'
import { listActivity, getActionLabel, type ActionType, type ActivityEntry } from '@/api/activity'
import { formatBytes, cn } from '@/lib/utils'

interface StatCardProps {
  title: string
  value: string | number
  icon: React.ComponentType<{ size?: number; className?: string }>
  description?: string
  iconBg: string
}

function StatCard({ title, value, icon: Icon, description, iconBg }: StatCardProps) {
  return (
    <div className="rounded-xl bg-bg-card border border-divider p-5 card-hover">
      <div className="flex items-start justify-between mb-4">
        <div className={cn("p-2.5 rounded-lg", iconBg)}>
          <Icon size={20} className="text-white" />
        </div>
      </div>
      
      <p className="text-sm text-text-muted mb-1">{title}</p>
      <p className="text-2xl font-semibold tracking-tight text-text-primary">{value}</p>
      {description && (
        <p className="text-xs text-text-muted mt-1">{description}</p>
      )}
    </div>
  )
}

interface QuickActionProps {
  icon: React.ComponentType<{ size?: number; className?: string }>
  label: string
  description: string
  onClick: () => void
  iconBg: string
}

function QuickAction({ icon: Icon, label, description, onClick, iconBg }: QuickActionProps) {
  return (
    <button 
      className="group p-4 rounded-xl bg-bg-card border border-divider text-left transition-all hover:border-accent-primary/50 card-hover"
      onClick={onClick}
    >
      <div className={cn("w-10 h-10 rounded-lg flex items-center justify-center mb-3", iconBg)}>
        <Icon size={20} className="text-white" />
      </div>
      <h3 className="font-medium text-text-primary mb-0.5">{label}</h3>
      <p className="text-sm text-text-muted">{description}</p>
      
      <div className="flex items-center gap-1 mt-3 text-sm text-accent-light opacity-0 group-hover:opacity-100 transition-opacity">
        <span>进入</span>
        <ArrowRight size={14} className="group-hover:translate-x-0.5 transition-transform" />
      </div>
    </button>
  )
}

// Get icon for action type
function ActionIcon({ action }: { action: ActionType }) {
  const icons: Record<ActionType, React.ComponentType<{ size?: number; className?: string }>> = {
    upload: Upload,
    download: Download,
    delete: Trash2,
    rename: Edit3,
    move: Move,
    copy: FileBox,
    create: FolderPlus,
    restore: RotateCcw,
    share: Share2,
    unshare: Share2,
    login: LogIn,
    logout: LogOut,
    trash_restore: RotateCcw,
    trash_delete: Trash2,
    trash_empty: Trash2,
  }
  const Icon = icons[action] || Activity
  return <Icon size={14} />
}

// Format relative time
function formatRelativeTime(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffSeconds = Math.floor(diffMs / 1000)
  const diffMinutes = Math.floor(diffSeconds / 60)
  const diffHours = Math.floor(diffMinutes / 60)
  const diffDays = Math.floor(diffHours / 24)

  if (diffSeconds < 60) return '刚刚'
  if (diffMinutes < 60) return `${diffMinutes} 分钟前`
  if (diffHours < 24) return `${diffHours} 小时前`
  if (diffDays === 1) return '昨天'
  if (diffDays < 7) return `${diffDays} 天前`
  return date.toLocaleDateString('zh-CN')
}

// Recent activity item
function RecentActivityItem({ entry }: { entry: ActivityEntry }) {
  const colorMap: Record<string, string> = {
    upload: 'text-emerald-500',
    download: 'text-blue-500',
    delete: 'text-red-500',
    rename: 'text-amber-500',
    move: 'text-amber-500',
    create: 'text-emerald-500',
    restore: 'text-emerald-500',
    share: 'text-violet-500',
    login: 'text-emerald-500',
    logout: 'text-zinc-500',
    trash_restore: 'text-emerald-500',
    trash_delete: 'text-red-500',
    trash_empty: 'text-red-500',
  }

  return (
    <div className="flex items-center gap-3 py-2.5 border-b border-divider last:border-0">
      <div className={cn("w-7 h-7 rounded-full flex items-center justify-center bg-bg-secondary", colorMap[entry.action])}>
        <ActionIcon action={entry.action} />
      </div>
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium text-text-primary truncate">{getActionLabel(entry.action)}</p>
        {entry.path && (
          <p className="text-xs text-text-muted truncate">{entry.path}</p>
        )}
      </div>
      <span className="text-xs text-text-muted whitespace-nowrap">{formatRelativeTime(entry.timestamp)}</span>
    </div>
  )
}

export function DashboardPage() {
  const navigate = useNavigate()
  
  const { data: health, isLoading: healthLoading } = useQuery({
    queryKey: ['health'],
    queryFn: getHealth,
    refetchInterval: 30000,
  })

  const { data: stats, isLoading: statsLoading } = useQuery({
    queryKey: ['stats'],
    queryFn: getStorageStats,
    refetchInterval: 30000,
  })

  const { data: recentActivity } = useQuery({
    queryKey: ['recent-activity'],
    queryFn: () => listActivity({ limit: 5 }),
    refetchInterval: 30000,
  })

  const isLoading = healthLoading || statsLoading

  if (isLoading) {
    return (
      <div className="space-y-6">
        <div>
          <Skeleton className="w-48 h-8 rounded-lg mb-2" />
          <Skeleton className="w-64 h-4 rounded-lg" />
        </div>
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
          {[1, 2, 3, 4].map((i) => (
            <div key={i} className="rounded-xl bg-bg-card border border-divider p-5">
              <Skeleton className="w-10 h-10 rounded-lg mb-4" />
              <Skeleton className="w-20 h-4 rounded mb-2" />
              <Skeleton className="w-32 h-7 rounded" />
            </div>
          ))}
        </div>
      </div>
    )
  }

  const isHealthy = health?.status === 'healthy'

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-text-primary">仪表盘</h1>
          <p className="text-text-muted mt-0.5 text-sm">系统概览</p>
        </div>
        <div className={cn(
          "flex items-center gap-2 px-3 py-1.5 rounded-full text-sm",
          isHealthy 
            ? "bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600 dark:text-emerald-400" 
            : "bg-red-50 dark:bg-red-500/10 text-red-600 dark:text-red-400"
        )}>
          {isHealthy ? <CheckCircle2 size={14} /> : <AlertCircle size={14} />}
          <span>{isHealthy ? '运行正常' : '异常'}</span>
        </div>
      </div>

      {/* Stats Grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard
          title="存储使用"
          value={formatBytes(stats?.totalSize || 0)}
          icon={HardDrive}
          iconBg="bg-gradient-to-br from-blue-500 to-cyan-500"
        />
        <StatCard
          title="文件对象"
          value={stats?.totalObjects?.toLocaleString() || '0'}
          icon={FileBox}
          description="总计存储对象"
          iconBg="bg-gradient-to-br from-violet-500 to-purple-500"
        />
        <StatCard
          title="去重率"
          value={`${((stats?.dedupRatio || 1) * 100).toFixed(1)}%`}
          icon={Activity}
          description="存储效率"
          iconBg="bg-gradient-to-br from-emerald-500 to-teal-500"
        />
        <StatCard
          title="运行时间"
          value={health?.uptime || '-'}
          icon={Clock}
          iconBg="bg-gradient-to-br from-amber-500 to-orange-500"
        />
      </div>

      {/* Storage Overview */}
      <Card className="bg-bg-card border-divider">
        <CardBody className="p-5">
          <div className="flex items-center justify-between mb-4">
            <h2 className="font-medium text-text-primary">存储概览</h2>
            <span className="text-xs text-text-muted">实时</span>
          </div>
          
          <div className="space-y-2 mb-5">
            <div className="flex justify-between text-sm">
              <span className="text-text-secondary">已用空间</span>
              <span>{formatBytes(stats?.totalSize || 0)}</span>
            </div>
            <div className="h-2 rounded-full bg-bg-secondary overflow-hidden">
              <div 
                className="h-full rounded-full bg-gradient-to-r from-violet-500 to-purple-500"
                style={{ width: '30%' }}
              />
            </div>
          </div>

          <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
            {[
              { label: '总对象数', value: stats?.totalObjects?.toLocaleString() || '0' },
              { label: '总大小', value: formatBytes(stats?.totalSize || 0) },
              { label: '去重率', value: `${((stats?.dedupRatio || 1) * 100).toFixed(1)}%` },
              { label: '版本', value: health?.version || '-' },
            ].map((item, i) => (
              <div key={i} className="p-3 rounded-lg bg-bg-secondary text-center">
                <p className="text-lg font-medium text-text-primary">{item.value}</p>
                <p className="text-xs text-text-muted">{item.label}</p>
              </div>
            ))}
          </div>
        </CardBody>
      </Card>

      {/* Quick Actions */}
      <div>
        <h2 className="font-medium mb-3">快速操作</h2>
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3">
          <QuickAction
            icon={FileBox}
            label="文件管理"
            description="浏览和管理文件"
            onClick={() => navigate('/files')}
            iconBg="bg-gradient-to-br from-blue-500 to-cyan-500"
          />
          <QuickAction
            icon={HardDrive}
            label="存储管理"
            description="查看存储状态"
            onClick={() => navigate('/storage')}
            iconBg="bg-gradient-to-br from-emerald-500 to-teal-500"
          />
          <QuickAction
            icon={Activity}
            label="系统健康"
            description="检查系统状态"
            onClick={() => navigate('/health')}
            iconBg="bg-gradient-to-br from-amber-500 to-orange-500"
          />
          <QuickAction
            icon={Clock}
            label="版本历史"
            description="查看文件版本"
            onClick={() => navigate('/versions')}
            iconBg="bg-gradient-to-br from-violet-500 to-purple-500"
          />
        </div>
      </div>

      {/* Recent Activity */}
      <Card className="bg-bg-card border-divider">
        <CardBody className="p-5">
          <div className="flex items-center justify-between mb-4">
            <h2 className="font-medium text-text-primary">最近活动</h2>
            <Button
              size="sm"
              variant="light"
              className="text-accent-light"
              onPress={() => navigate('/activity')}
            >
              查看全部
              <ArrowRight size={14} />
            </Button>
          </div>
          
          {recentActivity?.items && recentActivity.items.length > 0 ? (
            <div className="divide-y divide-divider">
              {recentActivity.items.map((entry) => (
                <RecentActivityItem key={entry.id} entry={entry} />
              ))}
            </div>
          ) : (
            <div className="py-8 text-center text-text-muted">
              <Activity size={24} className="mx-auto mb-2 opacity-50" />
              <p className="text-sm">暂无活动记录</p>
            </div>
          )}
        </CardBody>
      </Card>
    </div>
  )
}
