import { useEffect, useRef, useState, useMemo } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Button,
  Chip,
  Input,
  Select,
  SelectItem,
  Pagination,
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
  addToast,
} from '@heroui/react'
import {
  Activity,
  Upload,
  Download,
  Trash2,
  Edit3,
  Move,
  Copy,
  FolderPlus,
  RotateCcw,
  Share2,
  LogIn,
  LogOut,
  RefreshCw,
  Clock,
  User,
  Search,
  Filter,
  AlertCircle,
  HardDrive,
  Star,
  StarOff,
  MessageSquareText,
  Database,
  BarChart3,
  CalendarDays,
  X,
} from 'lucide-react'
import {
  ApiError,
  ACTIVITY_ACTIONS,
  ACTIVITY_ACTION_GROUPS,
  clearActivity,
  getActivityStats,
  listActivity,
  getActionLabel,
  getActionColor,
  type ActionType,
  type ActivityActionGroup,
  type ActivityEntry,
  type ActivityStats,
} from '@/api/activity'
import { cn, formatDate, formatRelativeTime, normalizePath } from '@/lib/utils'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'
import { StatCard } from '@/components/ui/StatCard'
import { useIsAdmin, useUser } from '@/stores/auth'
import { getInvalidHomeDirDescription, invalidHomeDirTitle, resolveUserHomeScope } from '@/lib/userScope'
import { getUserFacingErrorDescription } from '@/lib/apiMessages'
import { getActivityDetailEntries } from '@/lib/activityDetails'

const ACTIVITY_TIME_RANGES = [
  { key: 'all', label: '全部时间' },
  { key: 'today', label: '今天' },
  { key: '7d', label: '近 7 天' },
  { key: '30d', label: '近 30 天' },
] as const

const ACTIVITY_ACTION_GROUP_OPTIONS: Array<{ key: ActivityActionGroup; label: string }> = [
  { key: 'risk', label: '高风险变更' },
  { key: 'share', label: '分享相关' },
]

const activityLoadErrorDescription = '最近操作加载失败，请检查网络或稍后重试。'

type ActivityTimeRangeKey = typeof ACTIVITY_TIME_RANGES[number]['key']

interface ActivityReviewWindow {
  since: string
  until: string
}

interface ActivityPathFilterState {
  normalizedPath: string
  errorMessage?: string
}

const ACTIVITY_PATH_FILTER_ERROR = '路径不能包含 .、.. 或控制字符'

function isActivityTimeRangeKey(value: string | undefined): value is ActivityTimeRangeKey {
  return ACTIVITY_TIME_RANGES.some((range) => range.key === value)
}

function getActivityTimeRangeLabel(key: ActivityTimeRangeKey): string {
  return ACTIVITY_TIME_RANGES.find((range) => range.key === key)?.label ?? '全部时间'
}

function isActivityActionGroup(value: string | undefined): value is ActivityActionGroup {
  return ACTIVITY_ACTION_GROUPS.includes(value as ActivityActionGroup)
}

function getActivityActionGroupLabel(group: ActivityActionGroup): string {
  return ACTIVITY_ACTION_GROUP_OPTIONS.find((option) => option.key === group)?.label ?? group
}

function toRFC3339Seconds(date: Date): string {
  return date.toISOString().replace(/\.\d{3}Z$/, 'Z')
}

function getActivityTimeRangeSince(range: ActivityTimeRangeKey, now = new Date()): string | undefined {
  if (range === 'all') {
    return undefined
  }

  const since = new Date(now)
  if (range === 'today') {
    since.setHours(0, 0, 0, 0)
  } else if (range === '7d') {
    since.setDate(since.getDate() - 7)
  } else if (range === '30d') {
    since.setDate(since.getDate() - 30)
  }

  return toRFC3339Seconds(since)
}

function parseActivityPathFilter(value: string): ActivityPathFilterState {
  const trimmed = value.trim()
  if (!trimmed) {
    return { normalizedPath: '' }
  }

  // eslint-disable-next-line no-control-regex
  if (/[\x00-\x1F\x7F]/.test(trimmed)) {
    return { normalizedPath: '', errorMessage: ACTIVITY_PATH_FILTER_ERROR }
  }

  try {
    return { normalizedPath: normalizePath(trimmed) }
  } catch {
    return { normalizedPath: '', errorMessage: ACTIVITY_PATH_FILTER_ERROR }
  }
}

function formatActivityWindowLabel(window: ActivityReviewWindow): string {
  const startedAt = formatDate(window.since)
  const endedAt = formatDate(window.until)
  if (startedAt === '--' || endedAt === '--') {
    return '集中窗口'
  }
  return `${startedAt} - ${endedAt}`
}

// Format relative time
// Get icon for action type
function ActionIcon({ action }: { action: ActionType }) {
  const icons: Record<ActionType, React.ComponentType<{ size?: number; className?: string }>> = {
    upload: Upload,
    download: Download,
    delete: Trash2,
    rename: Edit3,
    move: Move,
    copy: Copy,
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
  return <Icon size={16} />
}

// Activity entry row
function ActivityRow({ entry }: { entry: ActivityEntry }) {
  const color = getActionColor(entry.action)

  return (
    <div className="activity-log-row grid grid-cols-[2.5rem_minmax(0,1fr)] gap-x-3 gap-y-2 border-b border-divider px-4 py-3.5 transition-colors last:border-b-0 hover:bg-content2/40 sm:grid-cols-[2.5rem_minmax(0,1fr)_auto] sm:items-center sm:px-5">
      <div className={cn(
        "activity-log-icon flex h-9 w-9 shrink-0 items-center justify-center rounded-lg",
        color === 'success' && "bg-success/20 text-success",
        color === 'danger' && "bg-danger/20 text-danger",
        color === 'warning' && "bg-warning/20 text-warning",
        color === 'primary' && "bg-primary/20 text-primary",
        color === 'default' && "bg-default/20 text-default-500",
      )}>
        <ActionIcon action={entry.action} />
      </div>

      <div className="activity-log-main min-w-0">
        <div className="activity-log-summary flex min-w-0 flex-col gap-0.5 sm:flex-row sm:items-baseline sm:gap-2">
          <span className="activity-log-action shrink-0 text-sm font-semibold text-foreground">{getActionLabel(entry.action)}</span>
          {entry.path && (
            <span className="activity-log-path min-w-0 truncate text-sm text-default-500">{entry.path}</span>
          )}
        </div>
        {entry.details && Object.keys(entry.details).length > 0 && (
          <div className="activity-log-details mt-1 flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-xs text-default-400">
            {getActivityDetailEntries(entry.action, entry.details).map((detail) => (
              <span key={detail.key} className="min-w-0 truncate">{detail.label}: {detail.value}</span>
            ))}
          </div>
        )}
      </div>

      <div className="activity-log-meta col-start-2 flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 text-xs text-default-500 sm:col-start-auto sm:flex-nowrap sm:justify-end sm:text-sm">
        {entry.user && (
          <div className="flex min-w-0 items-center gap-1">
            <User size={14} />
            <span className="truncate">{entry.user}</span>
          </div>
        )}
        <div className="flex shrink-0 items-center gap-1 sm:w-24 sm:justify-end">
          <Clock size={14} />
          <span>{formatRelativeTime(entry.timestamp)}</span>
        </div>
      </div>
    </div>
  )
}

function getActivityErrorState(error: unknown): 'unavailable' | null {
  if (error instanceof ApiError && error.isUnavailable) {
    return 'unavailable'
  }
  return null
}

function getActivityRefreshErrorToast(error: unknown): { title: string; description: string; color: 'warning' | 'danger' } {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: '最近操作暂不可用',
      description: '操作记录当前不可用，请检查设备状态或稍后重试。',
      color: 'warning',
    }
  }

  return {
    title: '刷新失败',
    description: getUserFacingErrorDescription(error),
    color: 'danger',
  }
}

function getActivityClearErrorToast(error: unknown): { title: string; description: string; color: 'warning' | 'danger' } {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: '最近操作暂不可用',
      description: '操作记录当前不可用，请检查设备状态或稍后重试。',
      color: 'warning',
    }
  }

  return {
    title: '清空最近操作失败',
    description: getUserFacingErrorDescription(error),
    color: 'danger',
  }
}

function isAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === 'AbortError'
}

interface TopActivityMetric {
  key: string
  count: number
}

function getTopActivityMetric(values?: Record<string, number>): TopActivityMetric | null {
  const entries = Object.entries(values ?? {})
    .filter(([, count]) => Number.isFinite(count) && count > 0)
    .sort(([leftKey, leftCount], [rightKey, rightCount]) => {
      if (rightCount !== leftCount) {
        return rightCount - leftCount
      }
      return leftKey.localeCompare(rightKey)
    })

  const [key, count] = entries[0] ?? []
  return key && count !== undefined ? { key, count } : null
}

function getActivityActionLabel(action: string): string {
  return ACTIVITY_ACTIONS.includes(action as ActionType) ? getActionLabel(action as ActionType) : '未知操作'
}

function ActivityStatsOverview({ stats, error, isFiltered }: { stats?: ActivityStats; error: unknown; isFiltered: boolean }) {
  if (error) {
    return (
      <div className="rounded-lg border border-warning/20 bg-warning/10 px-4 py-3 text-sm text-warning">
        <div className="flex items-center gap-2 font-medium">
          <AlertCircle size={16} />
          <span>统计暂不可用</span>
        </div>
        <p className="mt-1 text-warning/80">最近操作列表仍可继续查看。</p>
      </div>
    )
  }

  const topAction = getTopActivityMetric(stats?.by_action)
  const topUser = getTopActivityMetric(stats?.by_user)

  return (
    <div className="grid grid-cols-2 gap-3 sm:gap-4 xl:grid-cols-4">
      <StatCard
        title="累计操作"
        value={stats?.total ?? '--'}
        subtitle={stats ? (isFiltered ? '当前筛选结果' : '历史记录总量') : '正在加载统计'}
        icon={BarChart3}
        tone="primary"
      />
      <StatCard
        title="今日操作"
        value={stats?.today ?? '--'}
        subtitle={stats ? '当天新增记录' : '正在加载统计'}
        icon={CalendarDays}
        tone="success"
      />
      <StatCard
        title="最常见操作"
        value={topAction ? getActivityActionLabel(topAction.key) : '--'}
        subtitle={topAction ? `${topAction.count} 次` : '暂无操作类型'}
        icon={Database}
        tone="warning"
      />
      <StatCard
        title="最活跃用户"
        value={topUser?.key ?? '--'}
        subtitle={topUser ? `${topUser.count} 次` : '暂无用户记录'}
        icon={User}
        tone="secondary"
      />
    </div>
  )
}

function ActivityRiskInsight({
  summary,
  isLoading,
  onReviewWindow,
}: {
  summary?: ActivityStats['risk_summary']
  isLoading: boolean
  onReviewWindow?: (since: string, until: string) => void
}) {
  const total = summary?.total
  const today = summary?.today
  const maxWindow = summary?.max_10m
  const hasCluster = typeof maxWindow === 'number' && maxWindow >= 5
  const startedAt = summary?.max_10m_started_at
  const endedAt = summary?.max_10m_ended_at
  const canReviewWindow = hasCluster && Boolean(startedAt && endedAt)

  return (
    <div className={cn(
      "rounded-lg border px-4 py-3 text-sm",
      hasCluster ? "border-danger/20 bg-danger/10 text-danger" : "border-divider bg-content1 text-foreground"
    )}>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 items-start gap-2">
          <AlertCircle size={16} className={cn("mt-0.5 shrink-0", hasCluster ? "text-danger" : "text-warning")} />
          <div className="min-w-0">
            <p className="font-medium">高风险摘要</p>
            <p className={cn("mt-0.5 text-xs", hasCluster ? "text-danger/80" : "text-default-500")}>
              {hasCluster ? '发现 10 分钟内集中高风险变更' : '未发现明显集中批量变更'}
            </p>
            {startedAt && endedAt && (
              <p className={cn("mt-1 text-xs", hasCluster ? "text-danger/80" : "text-default-500")}>
                集中窗口：{formatActivityWindowLabel({ since: startedAt, until: endedAt })}
              </p>
            )}
          </div>
        </div>

        <div className="flex flex-col gap-3 sm:items-end">
          <div className="grid grid-cols-3 gap-3 text-right sm:min-w-[24rem]">
            <div>
              <p className="text-base font-semibold">{isLoading ? '--' : (total ?? 0)}</p>
              <p className="text-xs text-default-500">高风险变更</p>
            </div>
            <div>
              <p className="text-base font-semibold">{isLoading ? '--' : (today ?? 0)}</p>
              <p className="text-xs text-default-500">今日高风险</p>
            </div>
            <div>
              <p className="text-base font-semibold">{isLoading ? '--' : (maxWindow ?? 0)}</p>
              <p className="text-xs text-default-500">10 分钟最多</p>
            </div>
          </div>
          {canReviewWindow && startedAt && endedAt && (
            <Button
              size="sm"
              color="danger"
              variant="flat"
              className="h-8 rounded-lg"
              startContent={<Clock size={14} />}
              onPress={() => onReviewWindow?.(startedAt, endedAt)}
            >
              查看集中窗口
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}

export function ActivityPage() {
  const [page, setPage] = useState(1)
  const [actionFilter, setActionFilter] = useState<ActionType | ''>('')
  const [actionGroupFilter, setActionGroupFilter] = useState<ActivityActionGroup | ''>('')
  const [timeRangeFilter, setTimeRangeFilter] = useState<ActivityTimeRangeKey>('all')
  const [reviewWindowFilter, setReviewWindowFilter] = useState<ActivityReviewWindow | null>(null)
  const [pathFilter, setPathFilter] = useState('')
  const [userFilter, setUserFilter] = useState('')
  const [isClearConfirmOpen, setIsClearConfirmOpen] = useState(false)
  const pageSize = 20
  const user = useUser()
  const isAdmin = useIsAdmin()
  const { rootPath, hasInvalidHomeDir } = resolveUserHomeScope(user)
  const authScopeKey = user?.id ?? 'anonymous'
  const homeScopeKey = hasInvalidHomeDir ? '__invalid__' : (rootPath ?? '/')
  const queryClient = useQueryClient()
  const clearActivityAbortControllerRef = useRef<AbortController | null>(null)
  const normalizedUserFilter = isAdmin ? userFilter.trim() : ''
  const pathFilterState = useMemo(() => parseActivityPathFilter(pathFilter), [pathFilter])
  const normalizedPathFilter = pathFilterState.normalizedPath
  const pathFilterErrorMessage = pathFilterState.errorMessage
  const timeRangeSinceFilter = useMemo(() => getActivityTimeRangeSince(timeRangeFilter), [timeRangeFilter])
  const sinceFilter = reviewWindowFilter?.since ?? timeRangeSinceFilter
  const untilFilter = reviewWindowFilter?.until
  const hasActiveFilters = Boolean(actionFilter || actionGroupFilter || normalizedPathFilter || normalizedUserFilter || timeRangeFilter !== 'all' || reviewWindowFilter)

  const { data, isLoading, error, refetch, isRefetching } = useQuery({
    queryKey: ['activity', authScopeKey, isAdmin, homeScopeKey, page, actionFilter, actionGroupFilter, normalizedPathFilter, normalizedUserFilter, sinceFilter, untilFilter],
    queryFn: ({ signal }) => listActivity({
      limit: pageSize,
      offset: (page - 1) * pageSize,
      action: actionFilter || undefined,
      actionGroup: actionGroupFilter || undefined,
      path: normalizedPathFilter || undefined,
      user: normalizedUserFilter || undefined,
      since: sinceFilter,
      until: untilFilter,
      signal,
    }),
    enabled: !hasInvalidHomeDir && !pathFilterErrorMessage,
  })

  const { data: stats, error: statsError } = useQuery({
    queryKey: ['activity-stats', authScopeKey, isAdmin, homeScopeKey, actionFilter, actionGroupFilter, normalizedPathFilter, normalizedUserFilter, sinceFilter, untilFilter],
    queryFn: ({ signal }) => getActivityStats({
      action: actionFilter || undefined,
      actionGroup: actionGroupFilter || undefined,
      path: normalizedPathFilter || undefined,
      user: normalizedUserFilter || undefined,
      since: sinceFilter,
      until: untilFilter,
      signal,
    }),
    enabled: !hasInvalidHomeDir && !pathFilterErrorMessage,
  })

  const totalPages = useMemo(() => {
    if (!data?.total) return 1
    return Math.ceil(data.total / pageSize)
  }, [data?.total])
  const errorState = getActivityErrorState(error)

  const clearActivityMutation = useMutation({
    mutationFn: async ({ signal }: { signal: AbortSignal }) => clearActivity({ signal }),
    retry: false,
    onSuccess: (_result, variables) => {
      if (variables.signal.aborted) {
        return
      }

      setPage(1)
      setIsClearConfirmOpen(false)
      void queryClient.invalidateQueries({ queryKey: ['activity'] })
      void queryClient.invalidateQueries({ queryKey: ['activity-stats'] })
      addToast({ title: '最近操作已清空', color: 'success' })
    },
    onError: (mutationError: unknown, variables) => {
      if (variables.signal.aborted || isAbortError(mutationError)) {
        return
      }

      addToast(getActivityClearErrorToast(mutationError))
    },
    onSettled: (_result, _error, variables) => {
      if (clearActivityAbortControllerRef.current?.signal === variables.signal) {
        clearActivityAbortControllerRef.current = null
      }
    },
  })

  useEffect(() => {
    return () => {
      clearActivityAbortControllerRef.current?.abort()
      clearActivityAbortControllerRef.current = null
    }
  }, [])

  useEffect(() => {
    if (page <= totalPages) {
      return
    }

    let cancelled = false
    queueMicrotask(() => {
      if (!cancelled) {
        setPage(totalPages)
      }
    })

    return () => {
      cancelled = true
    }
  }, [page, totalPages])

  const handleRefresh = async () => {
    if (pathFilterErrorMessage) {
      addToast({
        title: '路径筛选无效',
        description: pathFilterErrorMessage,
        color: 'warning',
      })
      return
    }

    const result = await refetch()
    if (result.error) {
      addToast(getActivityRefreshErrorToast(result.error))
      return
    }
    addToast({ title: '最近操作已刷新', color: 'success' })
  }

  const handleConfirmClearActivity = () => {
    clearActivityAbortControllerRef.current?.abort()
    const controller = new AbortController()
    clearActivityAbortControllerRef.current = controller
    clearActivityMutation.mutate({ signal: controller.signal })
  }

  const handleCloseClearConfirm = () => {
    if (clearActivityMutation.isPending) {
      return
    }

    setIsClearConfirmOpen(false)
  }

  const handleUserFilterChange = (nextValue: string) => {
    setUserFilter(nextValue)
    setPage(1)
  }

  const handlePathFilterChange = (nextValue: string) => {
    setPathFilter(nextValue)
    setPage(1)
  }

  const handleActionGroupChange = (nextValue: string | undefined) => {
    const nextGroup = isActivityActionGroup(nextValue) ? nextValue : ''
    setActionGroupFilter(nextGroup)
    if (nextGroup) {
      setActionFilter('')
    }
    setPage(1)
  }

  const handleTimeRangeChange = (nextValue: string | undefined) => {
    setTimeRangeFilter(isActivityTimeRangeKey(nextValue) ? nextValue : 'all')
    setReviewWindowFilter(null)
    setPage(1)
  }

  const handleReviewWindowFilter = (since: string, until: string) => {
    setReviewWindowFilter({ since, until })
    setTimeRangeFilter('all')
    setActionFilter('')
    setActionGroupFilter('risk')
    setPage(1)
  }

  const handleClearAllFilters = () => {
    setActionFilter('')
    setActionGroupFilter('')
    setTimeRangeFilter('all')
    setReviewWindowFilter(null)
    setPathFilter('')
    setUserFilter('')
    setPage(1)
  }

  if (hasInvalidHomeDir) {
    return (
      <div className="flex h-full min-h-0 flex-col space-y-4 overflow-auto p-4 custom-scrollbar sm:p-6">
        <PageHeader
          title="最近操作"
          subtitle={invalidHomeDirTitle}
          icon={Activity}
        />

        <div className="flex flex-1 items-center justify-center">
          <EmptyState
            icon={AlertCircle}
            title={invalidHomeDirTitle}
            description={getInvalidHomeDirDescription('查看最近操作')}
          />
        </div>
      </div>
    )
  }

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center p-6 lg:p-8">
        <div className="text-center">
          <div className="w-12 h-12 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-4" />
          <p className="text-default-500">加载最近操作...</p>
        </div>
      </div>
    )
  }

  if (error) {
    if (errorState === 'unavailable') {
      return (
        <div className="flex h-full min-h-0 flex-col space-y-4 overflow-auto p-4 custom-scrollbar sm:p-6">
          <PageHeader
            title="最近操作"
            subtitle="暂不可用"
            icon={Activity}
            actions={
              <Button
                className="btn-secondary h-8 rounded-lg"
                size="sm"
                startContent={<RefreshCw size={14} className={isRefetching ? 'animate-spin' : ''} />}
                onPress={handleRefresh}
                isLoading={isRefetching}
              >
                刷新
              </Button>
            }
          />

          <div className="flex flex-1 items-center justify-center">
            <EmptyState
              icon={Activity}
              title="最近操作暂不可用"
              description="操作记录当前不可用，请检查设备状态或稍后重试。"
              action={
                <Button variant="bordered" className="rounded-lg" onPress={handleRefresh}>
                  重新加载
                </Button>
              }
            />
          </div>
        </div>
      )
    }

    return (
      <div className="flex h-full min-h-0 flex-col space-y-4 overflow-auto p-4 custom-scrollbar sm:p-6">
        <PageHeader
          title="最近操作"
          subtitle="加载失败"
          icon={Activity}
          actions={
            <Button
              className="btn-secondary h-8 rounded-lg"
              size="sm"
              startContent={<RefreshCw size={14} className={isRefetching ? 'animate-spin' : ''} />}
              onPress={handleRefresh}
              isLoading={isRefetching}
            >
              刷新
            </Button>
          }
        />

        <div className="flex flex-1 items-center justify-center">
          <EmptyState
            icon={Activity}
            title="加载最近操作失败"
            description={getUserFacingErrorDescription(error, activityLoadErrorDescription)}
            action={
              <Button variant="bordered" className="rounded-lg" onPress={handleRefresh}>
                重新加载
              </Button>
            }
          />
        </div>
      </div>
    )
  }

  const entries = pathFilterErrorMessage ? [] : (data?.items ?? [])
  const totalEntries = pathFilterErrorMessage ? 0 : (data?.total ?? entries.length)

  return (
    <div className="flex h-full min-h-0 flex-col space-y-4 overflow-auto p-4 custom-scrollbar sm:p-6">
      {/* Header */}
      <PageHeader
        title="最近操作"
        subtitle={pathFilterErrorMessage ? '路径筛选无效' : `共 ${totalEntries} 条记录`}
        icon={Activity}
        actions={
          <>
            <Select
              placeholder="筛选操作"
              size="sm"
              className="w-40"
              aria-label="筛选操作类型"
              selectedKeys={actionFilter ? [actionFilter] : []}
              onSelectionChange={(keys) => {
                const selected = Array.from(keys)[0] as ActionType | undefined
                setActionFilter(selected || '')
                if (selected) {
                  setActionGroupFilter('')
                }
                setPage(1)
              }}
              startContent={<Filter size={14} />}
            >
              {ACTIVITY_ACTIONS.map((action) => (
                <SelectItem key={action}>
                  {getActionLabel(action)}
                </SelectItem>
              ))}
            </Select>

            <Select
              placeholder="审计分组"
              size="sm"
              className="w-36"
              aria-label="筛选审计分组"
              selectedKeys={actionGroupFilter ? [actionGroupFilter] : []}
              onSelectionChange={(keys) => {
                handleActionGroupChange(Array.from(keys)[0]?.toString())
              }}
              startContent={<Filter size={14} />}
            >
              {ACTIVITY_ACTION_GROUP_OPTIONS.map((group) => (
                <SelectItem key={group.key}>
                  {group.label}
                </SelectItem>
              ))}
            </Select>

            <Select
              placeholder="时间范围"
              size="sm"
              className="w-36"
              aria-label="筛选时间范围"
              selectedKeys={[timeRangeFilter]}
              onSelectionChange={(keys) => {
                handleTimeRangeChange(Array.from(keys)[0]?.toString())
              }}
              startContent={<CalendarDays size={14} />}
            >
              {ACTIVITY_TIME_RANGES.map((range) => (
                <SelectItem key={range.key}>
                  {range.label}
                </SelectItem>
              ))}
            </Select>

            <div className="flex items-start gap-1">
              <Input
                aria-label="按路径筛选"
                placeholder="按路径筛选"
                size="sm"
                className="w-44"
                value={pathFilter}
                onValueChange={handlePathFilterChange}
                startContent={<Search size={14} />}
                isInvalid={Boolean(pathFilterErrorMessage)}
                errorMessage={pathFilterErrorMessage}
              />
              <Button
                aria-label="清除路径筛选"
                className="btn-secondary h-8 min-w-8 rounded-lg px-0"
                size="sm"
                isIconOnly
                isDisabled={!pathFilter}
                onPress={() => handlePathFilterChange('')}
              >
                <X size={14} />
              </Button>
            </div>

            {isAdmin && (
              <div className="flex items-start gap-1">
                <Input
                  aria-label="按用户筛选"
                  placeholder="按用户筛选"
                  size="sm"
                  className="w-40"
                  value={userFilter}
                  onValueChange={handleUserFilterChange}
                  startContent={<User size={14} />}
                />
                <Button
                  aria-label="清除用户筛选"
                  className="btn-secondary h-8 min-w-8 rounded-lg px-0"
                  size="sm"
                  isIconOnly
                  isDisabled={!userFilter}
                  onPress={() => handleUserFilterChange('')}
                >
                  <X size={14} />
                </Button>
              </div>
            )}

            <Button
              className="btn-secondary h-8 rounded-lg"
              size="sm"
              startContent={<RefreshCw size={14} className={isRefetching ? 'animate-spin' : ''} />}
              onPress={handleRefresh}
              isLoading={isRefetching}
            >
              刷新
            </Button>

            {isAdmin && (
              <Button
                color="danger"
                variant="flat"
                className="h-8 rounded-lg"
                size="sm"
                startContent={<Trash2 size={14} />}
                onPress={() => setIsClearConfirmOpen(true)}
                isDisabled={clearActivityMutation.isPending}
              >
                清空记录
              </Button>
            )}
          </>
        }
      />

      {!pathFilterErrorMessage && (
        <>
          <ActivityStatsOverview stats={stats} error={statsError} isFiltered={hasActiveFilters} />
          {!statsError && (
            <ActivityRiskInsight
              summary={stats?.risk_summary}
              isLoading={!stats}
              onReviewWindow={handleReviewWindowFilter}
            />
          )}
        </>
      )}

      {/* Filter chips */}
      {hasActiveFilters && (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm text-default-500">当前筛选:</span>
          {timeRangeFilter !== 'all' && (
            <Chip
              size="sm"
              color="primary"
              variant="flat"
              onClose={() => handleTimeRangeChange('all')}
            >
              时间：{getActivityTimeRangeLabel(timeRangeFilter)}
            </Chip>
          )}
          {reviewWindowFilter && (
            <Chip
              size="sm"
              color="danger"
              variant="flat"
              onClose={() => {
                setReviewWindowFilter(null)
                setPage(1)
              }}
            >
              窗口：{formatActivityWindowLabel(reviewWindowFilter)}
            </Chip>
          )}
          {actionFilter && (
            <Chip
              size="sm"
              color={getActionColor(actionFilter)}
              variant="flat"
              onClose={() => {
                setActionFilter('')
                setPage(1)
              }}
            >
              {getActionLabel(actionFilter)}
            </Chip>
          )}
          {actionGroupFilter && (
            <Chip
              size="sm"
              color="warning"
              variant="flat"
              onClose={() => handleActionGroupChange('')}
            >
              分组：{getActivityActionGroupLabel(actionGroupFilter)}
            </Chip>
          )}
          {normalizedPathFilter && (
            <Chip
              size="sm"
              color="primary"
              variant="flat"
              onClose={() => handlePathFilterChange('')}
            >
              路径：{normalizedPathFilter}
            </Chip>
          )}
          {normalizedUserFilter && (
            <Chip
              size="sm"
              color="secondary"
              variant="flat"
              onClose={() => handleUserFilterChange('')}
            >
              用户：{normalizedUserFilter}
            </Chip>
          )}
          <Button
            size="sm"
            variant="light"
            className="h-7 rounded-lg px-2 text-default-500"
            startContent={<X size={14} />}
            onPress={handleClearAllFilters}
          >
            清空全部筛选
          </Button>
        </div>
      )}

      {/* Activity list */}
      <div className="card-meridian min-h-0 flex-1 overflow-auto rounded-lg">
        {pathFilterErrorMessage ? (
          <div className="flex h-64 items-center justify-center">
            <EmptyState
              icon={AlertCircle}
              title="路径筛选无效"
              description={pathFilterErrorMessage}
              action={
                <Button
                  variant="bordered"
                  className="rounded-lg"
                  startContent={<X size={14} />}
                  onPress={() => handlePathFilterChange('')}
                >
                  清除路径条件
                </Button>
              }
            />
          </div>
        ) : entries.length > 0 ? (
          entries.map((entry) => (
            <ActivityRow key={entry.id} entry={entry} />
          ))
        ) : (
          <div className="flex items-center justify-center h-64">
            <EmptyState
              icon={Activity}
              title="暂无最近操作"
              description="上传、下载、分享和恢复记录会显示在这里"
            />
          </div>
        )}
      </div>

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="flex justify-center">
          <Pagination
            total={totalPages}
            page={page}
            onChange={setPage}
            showControls
          />
        </div>
      )}

      <Modal
        isOpen={isClearConfirmOpen}
        onClose={handleCloseClearConfirm}
        classNames={{
          base: 'bg-content1 border border-divider',
        }}
      >
        <ModalContent>
          <ModalHeader className="flex flex-col gap-1">
            <div className="flex items-center gap-3">
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-danger/10 text-danger">
                <Trash2 size={20} />
              </div>
              <span>确认清空最近操作</span>
            </div>
          </ModalHeader>
          <ModalBody>
            <p className="text-sm text-default-600">
              该操作会删除所有最近操作记录，无法撤销。
            </p>
          </ModalBody>
          <ModalFooter>
            <Button
              variant="light"
              className="rounded-lg"
              onPress={handleCloseClearConfirm}
              isDisabled={clearActivityMutation.isPending}
            >
              取消
            </Button>
            <Button
              color="danger"
              className="rounded-lg"
              onPress={handleConfirmClearActivity}
              isLoading={clearActivityMutation.isPending}
            >
              确认清空
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  )
}
