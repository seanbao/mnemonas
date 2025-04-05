import { useEffect, useState, useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  Button,
  Chip,
  Select,
  SelectItem,
  Pagination,
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
  Filter,
} from 'lucide-react'
import {
  ApiError,
  listActivity,
  getActionLabel,
  getActionColor,
  type ActionType,
  type ActivityEntry,
} from '@/api/activity'
import { cn, formatRelativeTime } from '@/lib/utils'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'

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
    login: LogIn,
    logout: LogOut,
    trash_restore: RotateCcw,
    trash_delete: Trash2,
    trash_empty: Trash2,
  }

  const Icon = icons[action] || Activity
  return <Icon size={16} />
}

// Activity entry row
function ActivityRow({ entry }: { entry: ActivityEntry }) {
  const color = getActionColor(entry.action)

  return (
    <div className="flex items-center gap-4 px-4 py-2.5 border-b border-divider table-row">
      <div className={cn(
        "w-8 h-8 rounded-lg flex items-center justify-center",
        color === 'success' && "bg-success/20 text-success",
        color === 'danger' && "bg-danger/20 text-danger",
        color === 'warning' && "bg-warning/20 text-warning",
        color === 'primary' && "bg-primary/20 text-primary",
        color === 'default' && "bg-default/20 text-default-500",
      )}>
        <ActionIcon action={entry.action} />
      </div>

      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="font-medium">{getActionLabel(entry.action)}</span>
          {entry.path && (
            <span className="text-sm text-default-400 truncate">{entry.path}</span>
          )}
        </div>
        {entry.details && Object.keys(entry.details).length > 0 && (
          <div className="text-xs text-default-400 mt-1">
            {Object.entries(entry.details).map(([key, value]) => (
              <span key={key} className="mr-3">{key}: {value}</span>
            ))}
          </div>
        )}
      </div>

      <div className="flex items-center gap-4 text-sm text-default-500">
        {entry.user && (
          <div className="flex items-center gap-1">
            <User size={14} />
            <span>{entry.user}</span>
          </div>
        )}
        <div className="flex items-center gap-1 w-24 justify-end">
          <Clock size={14} />
          <span>{formatRelativeTime(entry.timestamp)}</span>
        </div>
      </div>
    </div>
  )
}

// All action types for filter
const ALL_ACTIONS: ActionType[] = [
  'upload', 'download', 'delete', 'rename', 'move', 'copy',
  'create', 'restore', 'share', 'unshare', 'login', 'logout',
  'trash_restore', 'trash_delete', 'trash_empty',
]

function getActivityErrorState(error: unknown): 'unavailable' | null {
  if (error instanceof ApiError && error.isUnavailable) {
    return 'unavailable'
  }
  return null
}

function getActivityRefreshErrorToast(error: unknown): { title: string; description: string; color: 'warning' | 'danger' } {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: '活动日志暂不可用',
      description: '活动日志存储当前不可用，请检查系统健康状态或稍后重试。',
      color: 'warning',
    }
  }

  return {
    title: '刷新失败',
    description: error instanceof Error ? error.message : '请稍后重试',
    color: 'danger',
  }
}

export function ActivityPage() {
  const [page, setPage] = useState(1)
  const [actionFilter, setActionFilter] = useState<ActionType | ''>('')
  const pageSize = 20

  const { data, isLoading, error, refetch, isRefetching } = useQuery({
    queryKey: ['activity', page, actionFilter],
    queryFn: () => listActivity({
      limit: pageSize,
      offset: (page - 1) * pageSize,
      action: actionFilter || undefined,
    }),
  })

  const totalPages = useMemo(() => {
    if (!data?.total) return 1
    return Math.ceil(data.total / pageSize)
  }, [data?.total])
  const errorState = getActivityErrorState(error)

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
    const result = await refetch()
    if (result.error) {
      addToast(getActivityRefreshErrorToast(result.error))
      return
    }
    addToast({ title: '活动日志已刷新', color: 'success' })
  }

  if (isLoading) {
    return (
      <div className="p-6 lg:p-8 flex items-center justify-center h-full">
        <div className="text-center">
          <div className="w-12 h-12 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-4" />
          <p className="text-default-500">加载活动日志...</p>
        </div>
      </div>
    )
  }

  if (error) {
    if (errorState === 'unavailable') {
      return (
        <div className="h-full flex flex-col space-y-4 p-6 overflow-auto custom-scrollbar">
          <PageHeader
            title="活动日志"
            subtitle="暂不可用"
            icon={Activity}
            actions={
              <Button
                className="btn-secondary h-8 rounded-xl"
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
              title="活动日志暂不可用"
              description="活动日志存储当前不可用，请检查系统健康状态或稍后重试。"
              action={
                <Button variant="bordered" className="rounded-xl" onPress={handleRefresh}>
                  重新加载
                </Button>
              }
            />
          </div>
        </div>
      )
    }

    return (
      <div className="h-full flex flex-col space-y-4 p-6 overflow-auto custom-scrollbar">
        <PageHeader
          title="活动日志"
          subtitle="加载失败"
          icon={Activity}
          actions={
            <Button
              className="btn-secondary h-8 rounded-xl"
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
            title="加载活动日志失败"
            description={(error as Error).message || '请稍后重试'}
            action={
              <Button variant="bordered" className="rounded-xl" onPress={handleRefresh}>
                重新加载
              </Button>
            }
          />
        </div>
      </div>
    )
  }

  const entries = data?.items ?? []
  const totalEntries = data?.total ?? entries.length

  return (
    <div className="h-full flex flex-col space-y-4 p-6 overflow-auto custom-scrollbar">
      {/* Header */}
      <PageHeader
        title="活动日志"
        subtitle={`共 ${totalEntries} 条记录`}
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
                setPage(1)
              }}
              startContent={<Filter size={14} />}
            >
              {ALL_ACTIONS.map((action) => (
                <SelectItem key={action}>
                  {getActionLabel(action)}
                </SelectItem>
              ))}
            </Select>

            <Button
              className="btn-secondary h-8 rounded-xl"
              size="sm"
              startContent={<RefreshCw size={14} className={isRefetching ? 'animate-spin' : ''} />}
              onPress={handleRefresh}
              isLoading={isRefetching}
            >
              刷新
            </Button>
          </>
        }
      />

      {/* Filter chips */}
      {actionFilter && (
        <div className="flex items-center gap-2">
          <span className="text-sm text-default-500">当前筛选:</span>
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
        </div>
      )}

      {/* Activity list */}
      <div className="flex-1 overflow-auto card-meridian rounded-xl">
        {entries.length > 0 ? (
          entries.map((entry) => (
            <ActivityRow key={entry.id} entry={entry} />
          ))
        ) : (
          <div className="flex items-center justify-center h-64">
            <EmptyState
              icon={Activity}
              title="暂无活动记录"
              description="文件操作将在这里显示"
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
    </div>
  )
}
