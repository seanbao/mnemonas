import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
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
  Star,
  StarOff,
  MessageSquareText,
  TrendingUp,
  Database,
  Archive,
  RefreshCw,
} from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { ApiError as FilesApiError, getAppVersion, getHealth, getStorageStats, listBackupJobs, type BackupJob } from '@/api/files'
import { ApiError as ActivityApiError, listActivity, getActionLabel, getActionColor, type ActionType, type ActivityEntry } from '@/api/activity'
import { acknowledgeSetup, deferSetup, getSetupReadiness, SetupError as SetupApiError, type DeferSetupRequest, type SetupReadinessAction } from '@/api/setup'
import { formatBytes, cn, formatRelativeTime, formatUptimeSeconds } from '@/lib/utils'
import { areDiskStatsAvailable, clampUsagePercent, formatUsagePercent, getDiskSpaceStatus } from '@/lib/storageStats'
import { backupJobNeedsAttention, getBackupAttentionNextStepSummary, getBackupAttentionReasonSummary } from '@/lib/backupAttention'
import { getInvalidHomeDirDescription, invalidHomeDirTitle, resolveUserHomeScope } from '@/lib/userScope'
import { getUserFacingErrorDescription } from '@/lib/apiMessages'
import { getSetupReadinessRefetchInterval } from '@/lib/setupReadinessPolling'
import { PageHeader } from '@/components/ui/PageHeader'
import { StatCard } from '@/components/ui/StatCard'
import { SetupReadinessCard } from '@/components/dashboard'
import { useIsAdmin, useUser } from '@/stores/auth'

interface QuickActionProps {
  icon: React.ComponentType<{ size?: number; className?: string }>
  label: string
  description: string
  onClick: () => void
}

function QuickAction({ icon: Icon, label, description, onClick }: QuickActionProps) {
  return (
    <button
      type="button"
      className="group stat-card flex min-h-[4.5rem] min-w-0 items-center gap-3 p-3 text-left transition-colors hover:border-primary/35 sm:min-h-[8.5rem] sm:block sm:p-5"
      onClick={onClick}
    >
      <div className="gradient-mnemonas-subtle flex h-9 w-9 shrink-0 items-center justify-center rounded-lg sm:mb-3 sm:h-10 sm:w-10">
        <Icon size={20} className="text-accent-primary" />
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center justify-between gap-1">
          <span className="truncate font-medium text-foreground">{label}</span>
          <ArrowRight size={15} className="shrink-0 text-accent-primary transition-transform group-hover:translate-x-0.5 sm:hidden" />
        </div>
        <p className="hidden text-sm text-default-500 sm:block">{description}</p>

        <div className="mt-3 hidden items-center gap-1 text-sm text-accent-primary opacity-0 transition-opacity group-hover:opacity-100 sm:flex">
          <span>进入</span>
          <ArrowRight size={14} className="transition-transform group-hover:translate-x-0.5" />
        </div>
      </div>
    </button>
  )
}

// Get icon for action type
function ActionIcon({ action }: { action: ActionType }) {
  const icons: Record<ActionType, React.ComponentType<{ size?: number; className?: string; 'aria-hidden'?: boolean }>> = {
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
    favorite: Star,
    unfavorite: StarOff,
    favorite_note_update: MessageSquareText,
    login: LogIn,
    logout: LogOut,
    trash_restore: RotateCcw,
    trash_delete: Trash2,
    trash_empty: Trash2,
    disk_health: HardDrive,
    scrub: Database,
  }
  const Icon = icons[action] || Activity
  return <Icon size={14} aria-hidden={true} />
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
      description: '操作记录当前不可用，请稍后重试，或前往最近操作页查看最新状态。',
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
    (error instanceof ActivityApiError && error.isUnavailable) ||
    (error instanceof SetupApiError && error.isUnavailable)
  )
}

function getRecentActivityIconClass(action: ActionType): string {
  const colors: Record<ReturnType<typeof getActionColor>, string> = {
    default: 'text-zinc-500',
    primary: 'text-blue-500',
    success: 'text-emerald-500',
    warning: 'text-amber-500',
    danger: 'text-red-500',
  }
  return colors[getActionColor(action)]
}

function getDashboardRefreshErrorToast(errors: Array<unknown>): { title: string; description: string; color: 'warning' | 'danger' } {
  if (errors.some(isUnavailableRefreshError)) {
    return {
      title: '刷新暂不可用',
      description: '部分首页数据当前不可用，请检查设备状态后重试。',
      color: 'warning',
    }
  }

  return {
    title: '刷新失败',
    description: '首页刷新失败，请稍后重试。',
    color: 'danger',
  }
}

function getBackupIssueCount(jobs: BackupJob[]): number {
  return jobs.filter(backupJobNeedsAttention).length
}

function getBackupOverview(
  isAdmin: boolean,
  jobs: BackupJob[] | undefined,
  isLoading: boolean,
  error: unknown,
): { value: string; trend: string; needsAttention: boolean; nextStep?: string } {
  if (!isAdmin) {
    return { value: '可用', trend: '由管理员维护备份', needsAttention: false }
  }
  if (isLoading) {
    return { value: '--', trend: '正在读取备份状态', needsAttention: false }
  }
  if (error) {
    return { value: '暂不可用', trend: '前往备份与维护查看', needsAttention: true }
  }
  if (!jobs || jobs.length === 0) {
    return { value: '未配置', trend: '建议先添加外置盘或远端备份', needsAttention: true }
  }
  if (jobs.some((job) => job.running)) {
    return { value: '运行中', trend: '有备份或恢复任务正在执行', needsAttention: false }
  }

  const issueCount = getBackupIssueCount(jobs)
  if (issueCount > 0) {
    return {
      value: `${issueCount} 项待处理`,
      trend: getBackupAttentionReasonSummary(jobs),
      needsAttention: true,
      nextStep: getBackupAttentionNextStepSummary(jobs),
    }
  }

  const latestSuccess = jobs
    .map((job) => job.last_successful_run?.finished_at ?? job.last_successful_run?.started_at)
    .filter((value): value is string => Boolean(value))
    .map((value) => new Date(value))
    .filter((date) => !Number.isNaN(date.getTime()))
    .sort((left, right) => right.getTime() - left.getTime())[0]

  return {
    value: '正常',
    trend: latestSuccess ? `最近备份 ${formatRelativeTime(latestSuccess.toISOString())}` : '任务已配置，等待首次成功备份',
    needsAttention: false,
  }
}

function getDiskSpaceAlertClass(level: 'unknown' | 'normal' | 'warning' | 'critical'): string {
  return level === 'critical'
    ? 'border-danger/30 bg-danger/5'
    : 'border-warning/30 bg-warning/5'
}

function getDiskSpaceIconClass(level: 'unknown' | 'normal' | 'warning' | 'critical'): string {
  return level === 'critical' ? 'text-danger' : 'text-warning'
}

function getDiskUsageBarClass(level: 'unknown' | 'normal' | 'warning' | 'critical'): string {
  if (level === 'critical') {
    return 'bg-danger/70'
  }
  if (level === 'warning') {
    return 'bg-warning/70'
  }
  return 'bg-accent-primary/60'
}

// Recent activity item
function RecentActivityItem({ entry }: { entry: ActivityEntry }) {
  const color = getActionColor(entry.action)

  return (
    <div className="flex items-center justify-between gap-3 rounded-lg bg-content2/30 p-3 transition-colors hover:bg-content2/50">
      <div className="flex min-w-0 flex-1 items-center gap-3 sm:gap-4">
        <span className="data-value hidden w-20 shrink-0 text-xs text-default-500 sm:block">
          {formatRelativeTime(entry.timestamp)}
        </span>
        <div className={cn(
          "h-2 w-2 shrink-0 rounded-full",
          color === 'success' && 'status-online',
          color === 'warning' && 'status-warning',
          color === 'danger' && 'bg-danger',
          color === 'primary' && 'bg-primary',
          color === 'default' && 'status-offline',
        )} />
        <div className={cn("flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-content2", getRecentActivityIconClass(entry.action))}>
          <ActionIcon action={entry.action} />
        </div>
        <div className="flex-1 min-w-0">
          <div className="flex min-w-0 items-center gap-2">
            <p className="truncate text-sm font-medium text-foreground">{getActionLabel(entry.action)}</p>
            <span className="data-value shrink-0 text-[11px] text-default-400 sm:hidden">
              {formatRelativeTime(entry.timestamp)}
            </span>
          </div>
          {entry.path && (
            <p className="text-xs text-default-500 truncate">{entry.path}</p>
          )}
        </div>
      </div>
      <Chip
        size="sm"
        color={color}
        variant="flat"
        className="hidden shrink-0 sm:inline-flex"
      >
        {entry.action}
      </Chip>
    </div>
  )
}

export function DashboardPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const isAdmin = useIsAdmin()
  const user = useUser()
  const authScopeKey = user?.id ?? 'anonymous'
  const setupReadinessQueryKey = ['setup-readiness', authScopeKey] as const
  const { rootPath, hasInvalidHomeDir } = resolveUserHomeScope(user)
  const homeScopeKey = hasInvalidHomeDir ? '__invalid__' : (rootPath ?? '/')
  
  const { data: health, isLoading: healthLoading, error: healthError, refetch: refetchHealth } = useQuery({
    queryKey: ['health'],
    queryFn: ({ signal }) => getHealth({ signal }),
    refetchInterval: 30000,
  })

  const { data: stats, isLoading: statsLoading, error: statsError, refetch: refetchStats } = useQuery({
    queryKey: ['stats', authScopeKey, isAdmin, homeScopeKey],
    queryFn: ({ signal }) => getStorageStats({ signal }),
    enabled: !hasInvalidHomeDir,
    refetchInterval: 30000,
  })

  const { data: appVersion, isLoading: versionLoading, error: versionError, refetch: refetchVersion } = useQuery({
    queryKey: ['app-version'],
    queryFn: ({ signal }) => getAppVersion({ signal }),
    staleTime: 5 * 60 * 1000,
  })

  const { data: recentActivity, error: recentActivityError, refetch: refetchRecentActivity } = useQuery({
    queryKey: ['recent-activity', authScopeKey, isAdmin, homeScopeKey],
    queryFn: ({ signal }) => listActivity({ limit: 5, signal }),
    enabled: !hasInvalidHomeDir,
    refetchInterval: 30000,
  })
  const { data: backupJobs, isLoading: backupLoading, error: backupError, refetch: refetchBackupJobs } = useQuery({
    queryKey: ['dashboard-backup-jobs', authScopeKey],
    queryFn: ({ signal }) => listBackupJobs({ signal }),
    enabled: isAdmin,
    refetchInterval: 60000,
  })
  const { data: setupReadiness, error: setupReadinessError, refetch: refetchSetupReadiness, isFetching: setupReadinessFetching } = useQuery({
    queryKey: setupReadinessQueryKey,
    queryFn: ({ signal }) => getSetupReadiness({ signal }),
    enabled: isAdmin,
    staleTime: 30000,
    refetchInterval: (query) => getSetupReadinessRefetchInterval(query.state.data),
  })

  const completeSetupMutation = useMutation({
    mutationFn: () => acknowledgeSetup(),
    onSuccess: async (readiness) => {
      queryClient.setQueryData(setupReadinessQueryKey, readiness)
      await refetchSetupReadiness()
      addToast({ title: '首次设置已完成', color: 'success' })
    },
    onError: async () => {
      await refetchSetupReadiness()
    },
  })

  const deferSetupMutation = useMutation({
    mutationFn: (request: DeferSetupRequest) => deferSetup(request),
    onSuccess: async (readiness) => {
      queryClient.setQueryData(setupReadinessQueryKey, readiness)
      await refetchSetupReadiness()
      addToast({ title: '首次设置提醒已延期', color: 'success' })
    },
    onError: async () => {
      await refetchSetupReadiness()
    },
  })

  const isLoading = healthLoading || statsLoading || versionLoading
  const hasPartialError = Boolean(healthError || statsError || versionError || recentActivityError || backupError || setupReadinessError)
  const recentActivityErrorPresentation = recentActivityError
    ? getRecentActivityErrorPresentation(recentActivityError)
    : null

  const handleRetry = async () => {
    const [healthResult, statsResult, versionResult, recentActivityResult, backupResult, setupResult] = await Promise.all([
      refetchHealth(),
      hasInvalidHomeDir ? Promise.resolve({ error: null }) : refetchStats(),
      refetchVersion(),
      hasInvalidHomeDir ? Promise.resolve({ error: null }) : refetchRecentActivity(),
      isAdmin ? refetchBackupJobs() : Promise.resolve({ error: null }),
      isAdmin ? refetchSetupReadiness() : Promise.resolve({ error: null }),
    ])
    const refreshErrors = [
      healthResult.error,
      statsResult.error,
      versionResult.error,
      recentActivityResult.error,
      isAdmin ? backupResult.error : null,
      isAdmin ? setupResult.error : null,
    ].filter((error): error is Error => Boolean(error))

    if (refreshErrors.length > 0) {
      addToast(getDashboardRefreshErrorToast(refreshErrors))
      return
    }

    addToast({ title: '首页已刷新', color: 'success' })
  }

  const handleSetupAction = (action: SetupReadinessAction) => {
    switch (action) {
      case 'change_password':
        navigate(setupReadiness?.summary.password_change_required_admin_count
          ? '/users?filter=password-change-required'
          : '/settings')
        return
      case 'manage_users':
        navigate('/users')
        return
      case 'create_backup':
      case 'run_backup':
      case 'run_restore_drill':
        navigate('/maintenance')
        return
      case 'review_security':
        navigate('/settings')
    }
  }

  if (isLoading) {
    return (
      <div
        role="status"
        aria-label="加载首页"
        aria-busy="true"
        className="p-4 space-y-6 sm:p-6 lg:p-8"
      >
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
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
          <Skeleton className="h-64 w-full rounded-lg bg-content1/50 lg:col-span-2" />
          <Skeleton className="h-64 w-full rounded-lg bg-content1/50" />
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
  const diskStatsKnown = areDiskStatsAvailable(stats)
  const diskUsagePercent = diskStatsKnown ? clampUsagePercent(stats?.diskUsageRatio) : undefined
  const diskSpaceStatus = getDiskSpaceStatus(stats)
  const shouldShowDiskSpaceAlert = diskSpaceStatus.level === 'warning' || diskSpaceStatus.level === 'critical'
  const hasStorageData = diskStatsKnown
    ? stats?.diskUsed !== undefined && stats.diskUsed > 0
    : stats?.storageStatsAvailable === true && stats.totalSize !== undefined && stats.totalSize > 0
  const storageStatsKnown = stats?.storageStatsAvailable === true
  const fileCountKnown = stats?.fileCountAvailable === true
  const storageUsageValue = diskStatsKnown ? formatStorageSize(stats?.diskUsed) : formatStorageSize(stats?.totalSize)
  const storageProgressValueText = diskStatsKnown
    ? `${formatUsagePercent(stats?.diskUsageRatio)} 已用`
    : hasStorageData ? `已用 ${storageUsageValue}，磁盘容量统计不可用` : '统计不可用'
  const backupOverview = getBackupOverview(isAdmin, backupJobs, backupLoading, backupError)

  const statsCards = [
    {
      title: '存储使用',
      value: storageUsageValue,
      icon: HardDrive,
      tone: shouldShowDiskSpaceAlert ? 'warning' : 'primary',
      trend: diskStatsKnown
        ? `${formatUsagePercent(stats?.diskUsageRatio)} 已用 · ${diskSpaceStatus.label}`
        : storageStatsKnown ? 'CAS 统计可用' : '统计不可用',
    },
    {
      title: '文件数量',
      value: fileCountKnown ? formatCount(stats?.fileCount) : '--',
      icon: FileBox,
      tone: 'primary',
      trend: fileCountKnown ? '文件索引计数' : '统计不可用',
    },
    {
      title: '备份状态',
      value: backupOverview.value,
      icon: Archive,
      tone: backupOverview.needsAttention ? 'warning' : 'success',
      trend: backupOverview.trend,
    },
    {
      title: '运行时间',
      value: health?.uptimeSecs !== undefined ? formatUptimeSeconds(health.uptimeSecs) : health?.uptime ?? '--',
      icon: Clock,
      tone: healthStatus === 'healthy' ? 'success' : healthStatus === 'unknown' ? 'default' : 'warning',
      trend: health ? '稳定运行' : '状态未知',
    },
  ] as const

  return (
    <div className="p-4 space-y-6 sm:p-6 lg:p-8">
      {/* Header */}
      <PageHeader
        title="首页"
        subtitle="空间、备份和最近操作"
        actions={
          <div className={cn(
            "flex items-center gap-2 rounded-full px-3 py-1.5 text-sm",
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
        }
      />

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
              className="rounded-lg"
              startContent={<RefreshCw size={14} />}
              onPress={handleRetry}
            >
              重新加载
            </Button>
          </CardBody>
        </Card>
      )}

      {hasInvalidHomeDir && (
        <Card className="border-warning/30 bg-warning/5 shadow-none">
          <CardBody className="flex items-start gap-3">
            <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" />
            <div>
              <p className="text-sm font-medium text-foreground">{invalidHomeDirTitle}</p>
              <p className="text-xs text-default-600">{getInvalidHomeDirDescription('查看空间和最近操作')}</p>
            </div>
          </CardBody>
        </Card>
      )}

      {/* Quick Actions */}
      <nav aria-label="常用入口" className="min-w-0">
        <h2 className="mb-3 font-medium">常用入口</h2>
        <div className="grid min-w-0 grid-cols-2 gap-2 sm:gap-3 lg:grid-cols-4">
          <QuickAction
            icon={FileBox}
            label="文件"
            description="上传、下载和整理文件"
            onClick={() => navigate('/files')}
          />
          <QuickAction
            icon={Clock}
            label="版本"
            description="找回历史版本"
            onClick={() => navigate('/versions')}
          />
          {isAdmin && (
            <QuickAction
              icon={HardDrive}
              label="空间"
              description="查看磁盘和版本占用"
              onClick={() => navigate('/storage')}
            />
          )}
          {isAdmin && (
            <QuickAction
              icon={Archive}
              label="备份与维护"
              description="查看备份并执行恢复演练"
              onClick={() => navigate('/maintenance')}
            />
          )}
        </div>
      </nav>

      {/* Stats Grid */}
      <section aria-label="日常空间摘要" className="grid grid-cols-2 gap-2 sm:gap-3 lg:grid-cols-4">
        {statsCards.map((stat) => (
          <StatCard
            key={stat.title}
            title={stat.title}
            value={stat.value}
            subtitle={stat.trend}
            icon={stat.icon}
            tone={stat.tone}
            density="compact"
          />
        ))}
      </section>

      {shouldShowDiskSpaceAlert && (
        <Card className={cn('shadow-none', getDiskSpaceAlertClass(diskSpaceStatus.level))}>
          <CardBody className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
            <div className="flex items-start gap-3">
              <AlertCircle size={18} className={cn('mt-0.5 shrink-0', getDiskSpaceIconClass(diskSpaceStatus.level))} />
              <div>
                <p className="text-sm font-medium text-foreground">{diskSpaceStatus.title}</p>
                <p className="text-xs text-default-600">{diskSpaceStatus.description}</p>
              </div>
            </div>
            <Button
              size="sm"
              variant="flat"
              className="rounded-lg"
              startContent={<HardDrive size={14} />}
              onPress={() => navigate('/storage')}
            >
              查看存储
            </Button>
          </CardBody>
        </Card>
      )}

      {/* Storage Overview */}
      <Card className="card-mnemonas">
        <CardHeader className="pb-0">
          <div className="flex items-center gap-2">
            <div className="gradient-mnemonas rounded-lg p-2">
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
              <span className="data-value">{storageUsageValue}</span>
            </div>
            <div
              role="progressbar"
              aria-label="首页存储使用率"
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={diskUsagePercent !== undefined ? Math.round(diskUsagePercent) : undefined}
              aria-valuetext={storageProgressValueText}
              className="h-2 rounded-full bg-content2 overflow-hidden"
            >
              {hasStorageData ? (
                <div
                  className={cn('h-full rounded-full flow-line', getDiskUsageBarClass(diskSpaceStatus.level))}
                  style={{ width: diskUsagePercent !== undefined ? `${diskUsagePercent}%` : '3rem' }}
                />
              ) : (
                <div className="h-full w-0 rounded-full bg-accent-primary/30" />
              )}
            </div>
            <div className="text-xs text-default-400">
              {diskStatsKnown
                ? `${formatStorageSize(stats?.diskAvailable)} 可用 / ${formatStorageSize(stats?.diskTotal)} 总容量`
                : hasStorageData
                  ? '磁盘容量统计不可用，仅显示 CAS 数据'
                : storageStatsKnown
                  ? '暂无存储数据'
                  : '统计不可用'}
            </div>
          </div>

          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-4">
            {[
              { label: '总对象数', value: storageStatsKnown ? formatCount(stats?.totalObjects) : '--' },
              { label: diskStatsKnown ? '磁盘总量' : '总大小', value: diskStatsKnown ? formatStorageSize(stats?.diskTotal) : formatStorageSize(stats?.totalSize) },
              { label: '去重率', value: storageStatsKnown && stats?.dedupRatio !== undefined ? `${(stats.dedupRatio * 100).toFixed(1)}%` : '--' },
              { label: '版本', value: appVersion?.version ?? health?.version ?? '--' },
            ].map((item, i) => (
              <div key={i} className="rounded-lg bg-content2/50 p-3 text-center">
                <p className="data-value break-anywhere text-2xl font-medium leading-tight text-foreground">{item.value}</p>
                <p className="text-xs text-default-500">{item.label}</p>
              </div>
            ))}
          </div>
        </CardBody>
      </Card>

      {isAdmin && setupReadiness && setupReadiness.lifecycle !== 'completed' && (
        <SetupReadinessCard
          readiness={setupReadiness}
          isRetrying={setupReadinessFetching}
          isCompleting={completeSetupMutation.isPending}
          isDeferring={deferSetupMutation.isPending}
          mutationError={completeSetupMutation.error
            ? getUserFacingErrorDescription(completeSetupMutation.error)
            : deferSetupMutation.error
              ? getUserFacingErrorDescription(deferSetupMutation.error)
              : null}
          onRetry={() => {
            void refetchSetupReadiness()
          }}
          onAction={handleSetupAction}
          onComplete={() => completeSetupMutation.mutate()}
          onDefer={(request) => deferSetupMutation.mutate(request)}
        />
      )}

      {isAdmin && setupReadinessError && !setupReadiness && (
        <Card className="border-warning/30 bg-warning/5 shadow-none" role="region" aria-label="首次设置检查加载失败">
          <CardBody className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-start gap-3">
              <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" aria-hidden="true" />
              <div>
                <p className="text-sm font-medium text-foreground">首次设置检查加载失败</p>
                <p className="text-xs text-default-600">无法读取自动检测结果，请重新检查。</p>
              </div>
            </div>
            <Button
              size="sm"
              variant="flat"
              className="min-h-11 rounded-lg"
              isLoading={setupReadinessFetching}
              onPress={() => {
                void refetchSetupReadiness()
              }}
            >
              重新检查
            </Button>
          </CardBody>
        </Card>
      )}

      {backupOverview.needsAttention && (
        <Card className="border-warning/30 bg-warning/5 shadow-none">
          <CardBody className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
            <div className="flex items-start gap-3">
              <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" />
              <div>
                <p className="text-sm font-medium text-foreground">备份需要查看</p>
                <p className="text-xs text-default-600">{backupOverview.trend}</p>
                {backupOverview.nextStep && (
                  <p className="mt-1 text-xs text-default-600">建议：{backupOverview.nextStep}</p>
                )}
              </div>
            </div>
            <Button
              size="sm"
              variant="flat"
              className="rounded-lg"
              startContent={<Archive size={14} />}
              onPress={() => navigate('/maintenance')}
            >
              打开备份
            </Button>
          </CardBody>
        </Card>
      )}

      {/* Recent Activity */}
      <Card className="card-mnemonas">
        <CardHeader className="pb-0">
          <div className="flex w-full items-center justify-between">
            <div className="flex items-center gap-2">
              <div className="bg-accent-primary/10 rounded-lg p-2">
                <TrendingUp className="text-accent-primary h-4 w-4" />
              </div>
              <div>
                <span className="font-semibold">最近操作</span>
                <p className="text-default-500 text-xs">上传、下载、分享和恢复记录</p>
              </div>
            </div>
            <Button
              size="sm"
              variant="light"
              className="rounded-lg text-accent-primary"
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
              <p className="text-sm">暂无最近操作</p>
            </div>
          )}
        </CardBody>
      </Card>
    </div>
  )
}
