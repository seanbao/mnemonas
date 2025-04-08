import { useQuery } from '@tanstack/react-query'
import { Card, CardBody, CardHeader, Skeleton, Button, Chip, addToast } from '@heroui/react'
import { 
  HardDrive, 
  FileBox, 
  Activity, 
  Clock,
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
  TrendingUp,
  Database,
  RefreshCw,
} from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { ApiError as FilesApiError, getAppVersion, getHealth, getStorageStats } from '@/api/files'
import { ApiError as ActivityApiError, listActivity, getActionLabel, type ActionType, type ActivityEntry } from '@/api/activity'
import { formatBytes, cn, formatRelativeTime } from '@/lib/utils'
import { PageHeader } from '@/components/ui/PageHeader'
import { useIsAdmin } from '@/stores/auth'

interface QuickActionProps {
  icon: React.ComponentType<{ size?: number; className?: string }>
  label: string
  description: string
  onClick: () => void
  gradient: string
}

function QuickAction({ icon: Icon, label, description, onClick, gradient }: QuickActionProps) {
  return (
    <button 
      className="group stat-card p-5 text-left transition-all hover:scale-[1.02] rounded-2xl"
      onClick={onClick}
    >
      <div className={`absolute inset-0 bg-gradient-to-br ${gradient} rounded-2xl opacity-50`} />
      <div className="relative">
        <div className="gradient-meridian-subtle w-10 h-10 rounded-xl flex items-center justify-center mb-3">
          <Icon size={20} className="text-accent-primary" />
        </div>
        <h3 className="font-medium text-foreground mb-0.5">{label}</h3>
        <p className="text-sm text-default-500">{description}</p>
        
        <div className="flex items-center gap-1 mt-3 text-sm text-accent-primary opacity-0 group-hover:opacity-100 transition-opacity">
          <span>进入</span>
          <ArrowRight size={14} className="group-hover:translate-x-0.5 transition-transform" />
        </div>
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

function formatStorageSize(value: number | undefined): string {
  return value === undefined ? '--' : formatBytes(value)
}

function formatCount(value: number | undefined): string {
  return value === undefined ? '--' : value.toLocaleString()
}

function getRecentActivityErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof ActivityApiError && error.isUnavailable) {
    return {
      title: '活动记录暂时不可用',
      description: '活动日志当前不可用，请稍后重试，或前往活动页查看最新状态。',
    }
  }

  return {
    title: '活动记录加载失败',
    description: '请刷新页面后重试，或前往活动页查看详细状态。',
  }
}

function isUnavailableRefreshError(error: unknown): boolean {
  return (
    (error instanceof FilesApiError && error.isUnavailable) ||
    (error instanceof ActivityApiError && error.isUnavailable)
  )
}

function getDashboardRefreshErrorToast(errors: Array<unknown>): { title: string; description: string; color: 'warning' | 'danger' } {
  if (errors.some(isUnavailableRefreshError)) {
    return {
      title: '刷新暂不可用',
      description: '部分系统概览数据当前不可用，请检查服务状态后重试。',
      color: 'warning',
    }
  }

  return {
    title: '刷新失败',
    description: '系统概览刷新失败，请稍后重试。',
    color: 'danger',
  }
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

  const statusMap: Record<string, 'success' | 'warning' | 'primary'> = {
    upload: 'success',
    download: 'primary',
    delete: 'warning',
    create: 'success',
    share: 'primary',
  }

  return (
    <div className="bg-content2/30 hover:bg-content2/50 flex items-center justify-between rounded-xl p-3 transition-colors">
      <div className="flex items-center gap-4">
        <span className="data-value text-default-500 w-20 text-xs">
          {formatRelativeTime(entry.timestamp)}
        </span>
        <div className={cn("w-2 h-2 rounded-full", colorMap[entry.action] ? 'status-online' : 'bg-primary')} />
        <div className={cn("w-7 h-7 rounded-full flex items-center justify-center bg-content2", colorMap[entry.action])}>
          <ActionIcon action={entry.action} />
        </div>
        <div className="flex-1 min-w-0">
          <p className="text-sm font-medium text-foreground truncate">{getActionLabel(entry.action)}</p>
          {entry.path && (
            <p className="text-xs text-default-500 truncate">{entry.path}</p>
          )}
        </div>
      </div>
      <Chip
        size="sm"
        color={statusMap[entry.action] || 'primary'}
        variant="flat"
      >
        {entry.action}
      </Chip>
    </div>
  )
}

export function DashboardPage() {
  const navigate = useNavigate()
  const isAdmin = useIsAdmin()
  
  const { data: health, isLoading: healthLoading, error: healthError, refetch: refetchHealth } = useQuery({
    queryKey: ['health'],
    queryFn: getHealth,
    refetchInterval: 30000,
  })

  const { data: stats, isLoading: statsLoading, error: statsError, refetch: refetchStats } = useQuery({
    queryKey: ['stats'],
    queryFn: getStorageStats,
    refetchInterval: 30000,
  })

  const { data: appVersion, isLoading: versionLoading, error: versionError, refetch: refetchVersion } = useQuery({
    queryKey: ['app-version'],
    queryFn: getAppVersion,
    staleTime: 5 * 60 * 1000,
  })

  const { data: recentActivity, error: recentActivityError, refetch: refetchRecentActivity } = useQuery({
    queryKey: ['recent-activity'],
    queryFn: () => listActivity({ limit: 5 }),
    refetchInterval: 30000,
  })

  const isLoading = healthLoading || statsLoading || versionLoading
  const hasPartialError = Boolean(healthError || statsError || versionError || recentActivityError)
  const recentActivityErrorPresentation = recentActivityError
    ? getRecentActivityErrorPresentation(recentActivityError)
    : null

  const handleRetry = async () => {
    const [healthResult, statsResult, versionResult, recentActivityResult] = await Promise.all([
      refetchHealth(),
      refetchStats(),
      refetchVersion(),
      refetchRecentActivity(),
    ])
    const refreshErrors = [
      healthResult.error,
      statsResult.error,
      versionResult.error,
      recentActivityResult.error,
    ].filter((error): error is Error => Boolean(error))

    if (refreshErrors.length > 0) {
      addToast(getDashboardRefreshErrorToast(refreshErrors))
      return
    }

    addToast({ title: '系统概览已刷新', color: 'success' })
  }

  if (isLoading) {
    return (
      <div className="p-6 lg:p-8 space-y-6">
        <div className="flex items-center justify-between">
          <div>
            <Skeleton className="w-48 h-8 rounded-lg mb-2 bg-content3" />
            <Skeleton className="w-64 h-4 rounded-lg bg-content2" />
          </div>
          <Skeleton className="w-24 h-8 rounded-full bg-content2" />
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
          {[1, 2, 3, 4].map((i) => (
            <div key={i} className="stat-card border-transparent bg-content1/50">
              <Skeleton className="w-10 h-10 rounded-lg mb-4 bg-content3" />
              <div className="space-y-2">
                <Skeleton className="w-20 h-4 rounded bg-content2" />
                <Skeleton className="w-32 h-8 rounded bg-content3" />
                <Skeleton className="w-24 h-3 rounded bg-content2" />
              </div>
            </div>
          ))}
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
          <Skeleton className="rounded-2xl h-64 w-full lg:col-span-2 bg-content1/50" />
          <Skeleton className="rounded-2xl h-64 w-full bg-content1/50" />
        </div>
      </div>
    )
  }

  const healthStatus = health?.status === 'healthy'
    ? 'healthy'
    : health?.status === 'degraded'
      ? 'degraded'
    : health?.status
      ? 'unhealthy'
      : 'unknown'
  const hasStorageData = stats?.storageStatsAvailable === true && stats.totalSize !== undefined && stats.totalSize > 0
  const storageStatsKnown = stats?.storageStatsAvailable === true
  const fileCountKnown = stats?.fileCountAvailable === true

  const statsCards = [
    {
      title: '存储使用',
      value: formatStorageSize(stats?.totalSize),
      icon: HardDrive,
      trend: storageStatsKnown ? '实时监控中' : '统计不可用',
      gradient: 'from-blue-500/20 to-violet-500/20',
    },
    {
      title: '文件数量',
      value: fileCountKnown ? formatCount(stats?.fileCount) : '--',
      icon: FileBox,
      trend: fileCountKnown ? '文件索引计数' : '统计不可用',
      gradient: 'from-emerald-500/20 to-cyan-500/20',
    },
    {
      title: '去重率',
      value: stats?.dedupRatio !== undefined
        ? `${(stats.dedupRatio * 100).toFixed(1)}%`
        : '--',
      icon: Activity,
      trend: '存储效率',
      gradient: 'from-violet-500/20 to-fuchsia-500/20',
    },
    {
      title: '运行时间',
      value: health?.uptime ?? '--',
      icon: Clock,
      trend: health ? '稳定运行' : '状态未知',
      gradient: 'from-amber-500/20 to-orange-500/20',
    },
  ]

  return (
    <div className="p-6 lg:p-8 space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <PageHeader
          title="系统概览"
          subtitle="实时监控存储状态"
        />
        <div className="flex items-center gap-2 text-sm">
          <div className={cn(
            "flex items-center gap-2 px-3 py-1.5 rounded-full",
            healthStatus === 'healthy'
              ? "bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
              : healthStatus === 'degraded'
                ? "bg-amber-50 dark:bg-amber-500/10 text-amber-600 dark:text-amber-400"
              : healthStatus === 'unhealthy'
                ? "bg-red-50 dark:bg-red-500/10 text-red-600 dark:text-red-400"
                : "bg-default-100 text-default-600 dark:bg-default-100/10 dark:text-default-400"
          )}>
            {healthStatus === 'healthy' ? (
              <>
                <div className="live-indicator scale-75" />
                <span>运行正常</span>
              </>
            ) : healthStatus === 'degraded' ? (
              <>
                <AlertCircle size={14} />
                <span>已降级</span>
              </>
            ) : healthStatus === 'unhealthy' ? (
              <>
                <AlertCircle size={14} />
                <span>异常</span>
              </>
            ) : (
              <>
                <AlertCircle size={14} />
                <span>状态未知</span>
              </>
            )}
          </div>
        </div>
      </div>

      {hasPartialError && (
        <Card className="border-warning/30 bg-warning/5 shadow-none">
          <CardBody className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
            <div className="flex items-start gap-3">
              <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" />
              <div>
                <p className="text-sm font-medium text-foreground">部分系统数据加载失败</p>
                <p className="text-xs text-default-600">当前页面展示的是可用数据，部分卡片或活动记录可能不是最新状态。</p>
              </div>
            </div>
            <Button
              size="sm"
              variant="flat"
              className="rounded-xl"
              startContent={<RefreshCw size={14} />}
              onPress={handleRetry}
            >
              重新加载
            </Button>
          </CardBody>
        </Card>
      )}

      {/* Stats Grid - Meridian Style */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        {statsCards.map((stat) => (
          <div key={stat.title} className="stat-card">
            <div className={`absolute inset-0 bg-gradient-to-br ${stat.gradient} rounded-2xl opacity-50`} />
            <div className="relative">
              <div className="flex items-start justify-between">
                <div>
                  <p className="text-default-500 text-sm">{stat.title}</p>
                  <div className="mt-1 flex items-baseline gap-1">
                    <span className="data-value-large">{stat.value}</span>
                  </div>
                  <p className="text-default-500 mt-2 text-xs">{stat.trend}</p>
                </div>
                <div className="gradient-meridian-subtle rounded-xl p-2.5">
                  <stat.icon className="text-accent-primary h-5 w-5" />
                </div>
              </div>
            </div>
          </div>
        ))}
      </div>

      {/* Storage Overview */}
      <Card className="card-meridian">
        <CardHeader className="pb-0">
          <div className="flex items-center gap-2">
            <div className="gradient-meridian rounded-lg p-2">
              <Database className="h-4 w-4 text-white" />
            </div>
            <div>
              <span className="font-semibold">存储概览</span>
              <p className="text-default-500 text-xs">实时数据</p>
            </div>
          </div>
        </CardHeader>
        <CardBody>
          <div className="space-y-2 mb-5">
            <div className="flex justify-between text-sm">
              <span className="text-default-600">已用空间</span>
              <span className="data-value">{formatStorageSize(stats?.totalSize)}</span>
            </div>
            <div className="h-2 rounded-full bg-content2 overflow-hidden">
              {hasStorageData ? (
                <div className="h-full w-12 rounded-full bg-accent-primary/60 flow-line" />
              ) : (
                <div className="h-full w-0 rounded-full bg-accent-primary/30" />
              )}
            </div>
            <div className="text-xs text-default-400">
              {hasStorageData
                ? '总容量未配置，无法计算占用比例'
                : storageStatsKnown
                  ? '暂无存储数据'
                  : '统计不可用'}
            </div>
          </div>

          <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
            {[
              { label: '总对象数', value: formatCount(stats?.totalObjects) },
              { label: '总大小', value: formatStorageSize(stats?.totalSize) },
              { label: '去重率', value: stats?.dedupRatio !== undefined ? `${(stats.dedupRatio * 100).toFixed(1)}%` : '--' },
              { label: '版本', value: appVersion?.version ?? health?.version ?? '--' },
            ].map((item, i) => (
              <div key={i} className="p-3 rounded-lg bg-content2/50 text-center">
                <p className="text-2xl font-medium text-foreground data-value">{item.value}</p>
                <p className="text-xs text-default-500">{item.label}</p>
              </div>
            ))}
          </div>
        </CardBody>
      </Card>

      {/* Quick Actions */}
      <div>
        <h2 className="font-medium mb-3">快速操作</h2>
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
          <QuickAction
            icon={FileBox}
            label="文件管理"
            description="浏览和管理文件"
            onClick={() => navigate('/files')}
            gradient="from-blue-500/20 to-violet-500/20"
          />
          <QuickAction
            icon={HardDrive}
            label="存储管理"
            description="查看存储状态"
            onClick={() => navigate('/storage')}
            gradient="from-emerald-500/20 to-cyan-500/20"
          />
          {isAdmin && (
            <QuickAction
              icon={Activity}
              label="系统健康"
              description="检查系统状态"
              onClick={() => navigate('/system-health')}
              gradient="from-violet-500/20 to-fuchsia-500/20"
            />
          )}
          <QuickAction
            icon={Clock}
            label="版本历史"
            description="查看文件版本"
            onClick={() => navigate('/versions')}
            gradient="from-amber-500/20 to-orange-500/20"
          />
        </div>
      </div>

      {/* Recent Activity */}
      <Card className="card-meridian">
        <CardHeader className="pb-0">
          <div className="flex w-full items-center justify-between">
            <div className="flex items-center gap-2">
              <div className="bg-accent-primary/10 rounded-lg p-2">
                <TrendingUp className="text-accent-primary h-4 w-4" />
              </div>
              <div>
                <span className="font-semibold">最近活动</span>
                <p className="text-default-500 text-xs">系统活动记录</p>
              </div>
            </div>
            <Button
              size="sm"
              variant="light"
              className="text-accent-primary rounded-xl"
              onPress={() => navigate('/activity')}
            >
              查看全部
              <ArrowRight size={14} />
            </Button>
          </div>
        </CardHeader>
        <CardBody>
          {recentActivity?.items && recentActivity.items.length > 0 ? (
            <div className="space-y-2">
              {recentActivity.items.map((entry) => (
                <RecentActivityItem key={entry.id} entry={entry} />
              ))}
            </div>
          ) : recentActivityError ? (
            <div className="py-8 text-center text-default-500">
              <AlertCircle size={24} className="mx-auto mb-2 text-warning" />
              <p className="text-sm font-medium text-foreground">{recentActivityErrorPresentation?.title}</p>
              <p className="mt-1 text-xs text-default-500">{recentActivityErrorPresentation?.description}</p>
            </div>
          ) : (
            <div className="py-8 text-center text-default-500">
              <Activity size={24} className="mx-auto mb-2 opacity-50" />
              <p className="text-sm">暂无活动记录</p>
            </div>
          )}
        </CardBody>
      </Card>
    </div>
  )
}
