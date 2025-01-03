import { useState, useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  Button,
  Skeleton,
  Chip,
  Select,
  SelectItem,
  Pagination,
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
  listActivity,
  getActionLabel,
  getActionColor,
  type ActionType,
  type ActivityEntry,
} from '@/api/activity'
import { cn } from '@/lib/utils'

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
    <div className="flex items-center gap-4 px-4 py-3 border-b border-divider hover:bg-content2 transition-colors">
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

export function ActivityPage() {
  const [page, setPage] = useState(1)
  const [actionFilter, setActionFilter] = useState<ActionType | ''>('')
  const pageSize = 20

  const { data, isLoading, refetch, isRefetching } = useQuery({
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

  if (isLoading) {
    return (
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <Skeleton className="w-48 h-8 rounded-lg" />
          <Skeleton className="w-32 h-8 rounded-lg" />
        </div>
        <div className="space-y-2">
          {[1, 2, 3, 4, 5, 6, 7, 8].map((i) => (
            <Skeleton key={i} className="w-full h-14 rounded-lg" />
          ))}
        </div>
      </div>
    )
  }

  const entries = data?.items ?? []

  return (
    <div className="h-full flex flex-col space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <div className="w-12 h-12 rounded-xl bg-gradient-to-br from-blue-500/20 to-cyan-500/20 flex items-center justify-center">
            <Activity size={24} className="text-blue-500" />
          </div>
          <div>
            <h1 className="text-xl font-bold">活动日志</h1>
            <p className="text-sm text-default-500">
              共 {data?.total ?? 0} 条记录
            </p>
          </div>
        </div>

        <div className="flex items-center gap-3">
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
            variant="flat"
            size="sm"
            startContent={<RefreshCw size={14} className={isRefetching ? 'animate-spin' : ''} />}
            onPress={() => refetch()}
            isLoading={isRefetching}
          >
            刷新
          </Button>
        </div>
      </div>

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
      <div className="flex-1 overflow-auto glass-card rounded-xl">
        {entries.length > 0 ? (
          entries.map((entry) => (
            <ActivityRow key={entry.id} entry={entry} />
          ))
        ) : (
          <div className="flex flex-col items-center justify-center h-64 text-default-500">
            <div className="w-20 h-20 rounded-2xl bg-gradient-to-br from-default-100 to-default-200 flex items-center justify-center mb-4">
              <Activity size={40} className="text-default-400" />
            </div>
            <p className="text-lg font-medium text-default-600 mb-1">暂无活动记录</p>
            <p className="text-sm text-default-400">文件操作将在这里显示</p>
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
