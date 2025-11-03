import { useEffect, useRef, useState, useMemo } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
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
  ListChecks,
  ClipboardCheck,
  X,
} from 'lucide-react'
import {
  ApiError,
  ACTIVITY_ACTIONS,
  ACTIVITY_ACTION_GROUPS,
  ACTIVITY_REVIEW_DISPOSITION_STATUSES,
  clearActivity,
  createActivityReviewRecord,
  getActivityStats,
  listActivityReviewRecords,
  listActivity,
  updateActivityReviewRecordDisposition,
  getActionLabel,
  getActionColor,
  type ActivityActionCountMap,
  type ActionType,
  type ActivityActionGroup,
  type ActivityEntry,
  type ActivityReviewRecord,
  type ActivityReviewRecordCreateInput,
  type ActivityReviewDispositionStatus,
  type ActivityReviewRecordListResponse,
  type ActivityStats,
} from '@/api/activity'
import { cn, formatDate, formatRelativeTime, hasControlCharacter, normalizePath } from '@/lib/utils'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'
import { StatCard } from '@/components/ui/StatCard'
import { useIsAdmin, useUser } from '@/stores/auth'
import { getInvalidHomeDirDescription, invalidHomeDirTitle, resolveUserHomeScope } from '@/lib/userScope'
import { getUserFacingErrorDescription } from '@/lib/apiMessages'
import { getActivityDetailEntries } from '@/lib/activityDetails'
import { getRedactedDiagnosticMessage } from '@/lib/diagnosticMessages'
import { triggerBrowserDownload } from '@/lib/downloadResponse'

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

const ACTIVITY_REVIEW_DISPOSITION_OPTIONS: Array<{ key: ActivityReviewDispositionStatus; label: string }> = [
  { key: 'documented', label: '已记录' },
  { key: 'confirmed', label: '确认保留' },
  { key: 'restored', label: '已恢复' },
  { key: 'disabled', label: '已禁用' },
  { key: 'needs_follow_up', label: '需跟进' },
]

type ActivityReviewDispositionFilterKey = 'all' | ActivityReviewDispositionStatus
type ActivityReviewActionGroupFilterKey = 'all' | ActivityActionGroup

const ACTIVITY_REVIEW_DISPOSITION_FILTER_OPTIONS: Array<{ key: ActivityReviewDispositionFilterKey; label: string }> = [
  { key: 'all', label: '全部处置' },
  ...ACTIVITY_REVIEW_DISPOSITION_OPTIONS,
]

const ACTIVITY_REVIEW_ACTION_GROUP_FILTER_OPTIONS: Array<{ key: ActivityReviewActionGroupFilterKey; label: string }> = [
  { key: 'all', label: '全部类型' },
  ...ACTIVITY_ACTION_GROUP_OPTIONS,
]

const ACTIVITY_REVIEW_ACTIONS = new Set<ActionType>([
  'delete',
  'rename',
  'move',
  'restore',
  'share',
  'unshare',
  'trash_restore',
  'trash_delete',
  'trash_empty',
])

const ACTIVITY_DESTRUCTIVE_ACTIONS = new Set<ActionType>(['delete', 'trash_delete', 'trash_empty'])
const ACTIVITY_SHARE_ACTIONS = new Set<ActionType>(['share', 'unshare'])
const ACTIVITY_RESTORE_ACTIONS = new Set<ActionType>(['restore', 'trash_restore'])
const ACTIVITY_RECOVERY_FOLLOW_UP_ACTIONS = new Set<ActionType>(['delete', 'rename', 'move', 'restore', 'trash_restore', 'trash_delete', 'trash_empty'])
const ACTIVITY_WARNING_DETAIL_KEYS = new Set(['persistence_warning', 'cleanup_warning', 'trash_cleanup_warning'])

const ACTIVITY_REVIEW_FILTERED_LIMIT = 500
const ACTIVITY_REVIEW_EXPORT_LIMIT = 100
const ACTIVITY_REVIEW_FOLLOW_UP_LIMIT = 3

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

interface ActivityURLFilterState {
  actionFilter: ActionType | ''
  actionGroupFilter: ActivityActionGroup | ''
  timeRangeFilter: ActivityTimeRangeKey
  pathFilter: string
  userFilter: string
}

const ACTIVITY_PATH_FILTER_ERROR = '路径不能包含 .、.. 或控制字符'

function isActivityTimeRangeKey(value: string | undefined): value is ActivityTimeRangeKey {
  return ACTIVITY_TIME_RANGES.some((range) => range.key === value)
}

function isActivityAction(value: string | undefined): value is ActionType {
  return ACTIVITY_ACTIONS.includes(value as ActionType)
}

function isActivityReviewDispositionStatus(value: string | undefined): value is ActivityReviewDispositionStatus {
  return ACTIVITY_REVIEW_DISPOSITION_STATUSES.includes(value as ActivityReviewDispositionStatus)
}

function isActivityReviewDispositionFilterKey(value: string | undefined): value is ActivityReviewDispositionFilterKey {
  return value === 'all' || isActivityReviewDispositionStatus(value)
}

function isActivityReviewActionGroupFilterKey(value: string | undefined): value is ActivityReviewActionGroupFilterKey {
  return value === 'all' || isActivityActionGroup(value)
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

function getActivityReviewDispositionStatusLabel(status?: ActivityReviewDispositionStatus): string {
  return ACTIVITY_REVIEW_DISPOSITION_OPTIONS.find((option) => option.key === (status ?? 'documented'))?.label ?? '已记录'
}

function toRFC3339Seconds(date: Date): string {
  return date.toISOString().replace(/\.\d{3}Z$/, 'Z')
}

function getSingleActivitySearchParam(params: URLSearchParams, key: string): string | undefined {
  const values = params.getAll(key)
  return values.length === 1 ? values[0] : undefined
}

function parseActivityURLFilters(search: string): ActivityURLFilterState {
  const params = new URLSearchParams(search)
  const actionParam = getSingleActivitySearchParam(params, 'action')
  const actionGroupParam = getSingleActivitySearchParam(params, 'action_group')
  const timeRangeParam = getSingleActivitySearchParam(params, 'time_range')

  const actionFilter = isActivityAction(actionParam) ? actionParam : ''
  const actionGroupFilter = !actionFilter && isActivityActionGroup(actionGroupParam) ? actionGroupParam : ''

  return {
    actionFilter,
    actionGroupFilter,
    timeRangeFilter: isActivityTimeRangeKey(timeRangeParam) ? timeRangeParam : 'all',
    pathFilter: getSingleActivitySearchParam(params, 'path') ?? '',
    userFilter: getSingleActivitySearchParam(params, 'user')?.trim() ?? '',
  }
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

  if (hasControlCharacter(trimmed)) {
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

function getActivityReviewScopeLabel(isFiltered: boolean, reviewWindow: ActivityReviewWindow | null): string {
  return reviewWindow ? '集中窗口' : isFiltered ? '当前筛选结果' : '当前页'
}

function getActivityReviewFilteredScopeLabel(isFiltered: boolean, reviewWindow: ActivityReviewWindow | null): string {
  return reviewWindow ? '集中窗口' : isFiltered ? '当前筛选结果' : '全部记录'
}

function getActivityReviewFilterSummary({
  timeRangeFilter,
  reviewWindow,
  actionFilter,
  actionGroupFilter,
  pathFilter,
  userFilter,
}: {
  timeRangeFilter: ActivityTimeRangeKey
  reviewWindow: ActivityReviewWindow | null
  actionFilter: ActionType | ''
  actionGroupFilter: ActivityActionGroup | ''
  pathFilter: string
  userFilter: string
}): string {
  const filters: string[] = []
  if (reviewWindow) {
    filters.push(`窗口 ${formatActivityWindowLabel(reviewWindow)}`)
  } else if (timeRangeFilter !== 'all') {
    filters.push(`时间 ${getActivityTimeRangeLabel(timeRangeFilter)}`)
  }
  if (actionFilter) {
    filters.push(`操作 ${getActionLabel(actionFilter)}`)
  }
  if (actionGroupFilter) {
    filters.push(`分组 ${getActivityActionGroupLabel(actionGroupFilter)}`)
  }
  if (pathFilter) {
    filters.push(`路径 ${pathFilter}`)
  }
  if (userFilter) {
    filters.push(`用户 ${userFilter}`)
  }
  return filters.length > 0 ? filters.join(' · ') : '未筛选'
}

function activityReviewRecordMatchesFilters(
  record: ActivityReviewRecord,
  reviewerFilter: string,
  activityEntryIDFilter: string,
  actionGroupFilter?: ActivityActionGroup,
  sinceFilter?: string,
  dispositionStatusFilter?: ActivityReviewDispositionStatus,
): boolean {
  if (reviewerFilter && record.reviewer !== reviewerFilter) {
    return false
  }
  if (activityEntryIDFilter && !record.activity_entry_ids.includes(activityEntryIDFilter)) {
    return false
  }
  if (dispositionStatusFilter && (record.disposition_status ?? 'documented') !== dispositionStatusFilter) {
    return false
  }
  if (actionGroupFilter && !activityReviewRecordMatchesActionGroup(record, actionGroupFilter)) {
    return false
  }
  if (sinceFilter && Date.parse(record.reviewed_at) < Date.parse(sinceFilter)) {
    return false
  }
  return true
}

function activityReviewRecordMatchesActionGroup(record: ActivityReviewRecord, actionGroup: ActivityActionGroup): boolean {
  const matchingActions = actionGroup === 'share' ? ACTIVITY_SHARE_ACTIONS : ACTIVITY_REVIEW_ACTIONS
  return activityReviewRecordHasAction(record, matchingActions)
}

function prependActivityReviewRecord(
  current: ActivityReviewRecordListResponse | undefined,
  record: ActivityReviewRecord,
  limit: number,
): ActivityReviewRecordListResponse {
  const currentItems = current?.items ?? []
  const alreadyExists = currentItems.some((item) => item.id === record.id)
  return {
    items: [record, ...currentItems.filter((item) => item.id !== record.id)].slice(0, limit),
    total: (current?.total ?? 0) + (alreadyExists ? 0 : 1),
    limit: current?.limit ?? limit,
    offset: current?.offset ?? 0,
  }
}

function updateActivityReviewRecordList(
  current: ActivityReviewRecordListResponse | undefined,
  record: ActivityReviewRecord,
  shouldKeep: (record: ActivityReviewRecord) => boolean,
): ActivityReviewRecordListResponse | undefined {
  if (!current) {
    return current
  }

  const existed = current.items.some((item) => item.id === record.id)
  const nextItems = shouldKeep(record)
    ? current.items.map((item) => (item.id === record.id ? record : item))
    : current.items.filter((item) => item.id !== record.id)

  return {
    ...current,
    items: nextItems,
    total: shouldKeep(record)
      ? current.total
      : Math.max(0, current.total - (existed ? 1 : 0)),
  }
}

function getActivityReviewRecordPrimaryPath(record: ActivityReviewRecord): string {
  return record.path_samples?.find((path) => path.trim().length > 0) ?? ''
}

function activityReviewRecordHasAction(record: ActivityReviewRecord, actions: Set<ActionType>): boolean {
  return Object.entries(record.action_counts ?? {}).some(([action, count]) => actions.has(action as ActionType) && count > 0)
}

function getActivityReviewRecordTraceActionGroup(record: ActivityReviewRecord): ActivityActionGroup | '' {
  if (activityReviewRecordHasAction(record, ACTIVITY_SHARE_ACTIONS)) {
    return 'share'
  }
  if (activityReviewRecordHasAction(record, ACTIVITY_REVIEW_ACTIONS)) {
    return 'risk'
  }
  return ''
}

function activityReviewRecordHasRecoveryFollowUp(record: ActivityReviewRecord): boolean {
  return activityReviewRecordHasAction(record, ACTIVITY_RECOVERY_FOLLOW_UP_ACTIONS)
}

function getActivityEntryPrimaryPath(entry: ActivityEntry): string {
  return entry.path?.trim() ?? ''
}

function activityEntryHasRecoveryFollowUp(entry: ActivityEntry): boolean {
  return ACTIVITY_RECOVERY_FOLLOW_UP_ACTIONS.has(entry.action)
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
function ActivityRow({
  entry,
  onOpenVersions,
  onOpenTrash,
  onOpenShares,
}: {
  entry: ActivityEntry
  onOpenVersions: (entry: ActivityEntry) => void
  onOpenTrash: (entry: ActivityEntry) => void
  onOpenShares: (entry: ActivityEntry) => void
}) {
  const color = getActionColor(entry.action)
  const primaryPath = getActivityEntryPrimaryPath(entry)
  const showRecoveryActions = Boolean(primaryPath && activityEntryHasRecoveryFollowUp(entry))
  const showShareActions = Boolean(primaryPath && ACTIVITY_SHARE_ACTIONS.has(entry.action))
  const hasActions = showRecoveryActions || showShareActions

  return (
    <div
      role="listitem"
      aria-label={`活动记录 ${getActionLabel(entry.action)}${primaryPath ? ` ${primaryPath}` : ''}`}
      className="activity-log-row grid grid-cols-[2.5rem_minmax(0,1fr)] gap-x-3 gap-y-2 border-b border-divider px-4 py-3.5 transition-colors last:border-b-0 hover:bg-content2/40 sm:grid-cols-[2.5rem_minmax(0,1fr)_auto] sm:items-center sm:px-5"
    >
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
              <span key={detail.key} className="min-w-0 truncate">{detail.label}：{detail.value}</span>
            ))}
          </div>
        )}
        {hasActions && (
          <div className="activity-log-actions mt-2 flex flex-wrap gap-1.5">
            {showRecoveryActions && (
              <>
                <Button
                  size="sm"
                  variant="light"
                  className="h-8 rounded-lg px-2.5 text-xs"
                  startContent={<RotateCcw size={13} />}
                  onPress={() => onOpenVersions(entry)}
                >
                  查看版本
                </Button>
                <Button
                  size="sm"
                  variant="light"
                  className="h-8 rounded-lg px-2.5 text-xs"
                  startContent={<Trash2 size={13} />}
                  onPress={() => onOpenTrash(entry)}
                >
                  查回收站
                </Button>
              </>
            )}
            {showShareActions && (
              <Button
                size="sm"
                variant="light"
                className="h-8 rounded-lg px-2.5 text-xs"
                startContent={<Share2 size={13} />}
                onPress={() => onOpenShares(entry)}
              >
                处理分享
              </Button>
            )}
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

const getNonBlankToastDescription = getRedactedDiagnosticMessage

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

function hasActivityWarningDetails(entry: ActivityEntry): boolean {
  return Object.entries(entry.details ?? {}).some(([key, value]) => (
    ACTIVITY_WARNING_DETAIL_KEYS.has(key) && value === 'true'
  ))
}

function shouldReviewActivityEntry(entry: ActivityEntry): boolean {
  return ACTIVITY_REVIEW_ACTIONS.has(entry.action) || hasActivityWarningDetails(entry)
}

function getActivityReviewReasons(entry: ActivityEntry): string[] {
  const reasons: string[] = []
  if (ACTIVITY_DESTRUCTIVE_ACTIONS.has(entry.action)) {
    reasons.push('删除或清空')
  }
  if (entry.action === 'rename' || entry.action === 'move') {
    reasons.push('路径变更')
  }
  if (ACTIVITY_SHARE_ACTIONS.has(entry.action)) {
    reasons.push('分享变更')
  }
  if (ACTIVITY_RESTORE_ACTIONS.has(entry.action)) {
    reasons.push('恢复变更')
  }
  if (hasActivityWarningDetails(entry)) {
    reasons.push('执行警告')
  }
  return reasons.length > 0 ? reasons : ['需要复核']
}

function getUniqueActivityReviewValues(entries: ActivityEntry[], selector: (entry: ActivityEntry) => string | undefined): string[] {
  return Array.from(new Set(entries.map(selector).filter((value): value is string => Boolean(value && value.trim()))))
}

function getActivityReviewActionCounts(entries: ActivityEntry[]): ActivityActionCountMap {
  return entries.reduce<ActivityActionCountMap>((counts, entry) => {
    counts[entry.action] = (counts[entry.action] ?? 0) + 1
    return counts
  }, {})
}

function buildActivityReviewRecordInput({
  reviewEntries,
  totalEntries,
  note,
  scopeLabel,
  filterSummary,
  dispositionStatus,
}: {
  reviewEntries: ActivityEntry[]
  totalEntries: number
  note: string
  scopeLabel: string
  filterSummary: string
  dispositionStatus: ActivityReviewDispositionStatus
}): ActivityReviewRecordCreateInput {
  const paths = getUniqueActivityReviewValues(reviewEntries, (entry) => entry.path)
  const users = getUniqueActivityReviewValues(reviewEntries, (entry) => entry.user)
  return {
    note,
    scope_label: scopeLabel,
    filter_summary: filterSummary,
    disposition_status: dispositionStatus,
    action_counts: getActivityReviewActionCounts(reviewEntries),
    review_count: reviewEntries.length,
    total_count: totalEntries,
    path_count: paths.length,
    user_count: users.length,
    path_samples: paths.slice(0, 10),
    user_samples: users.slice(0, 10),
    activity_entry_ids: reviewEntries.map((entry) => entry.id),
  }
}

function formatActivityReviewActionCounts(actionCounts?: ActivityActionCountMap): string {
  const labels = ACTIVITY_ACTIONS
    .map((action) => ({ action, count: actionCounts?.[action] ?? 0 }))
    .filter(({ count }) => count > 0)
    .map(({ action, count }) => `${getActionLabel(action)} ${count}`)

  if (labels.length === 0) {
    return ''
  }
  if (labels.length <= 4) {
    return labels.join(' · ')
  }
  return `${labels.slice(0, 4).join(' · ')} · +${labels.length - 4}`
}

function formatActivityReviewActionCountsForExport(actionCounts?: ActivityActionCountMap): string {
  return ACTIVITY_ACTIONS
    .map((action) => ({ action, count: actionCounts?.[action] ?? 0 }))
    .filter(({ count }) => count > 0)
    .map(({ action, count }) => `${getActionLabel(action)} ${count}`)
    .join('; ')
}

function csvCell(value: string | number | undefined): string {
  const text = String(value ?? '')
  return `"${text.replaceAll('"', '""')}"`
}

function buildActivityReviewRecordsCSV(records: ActivityReviewRecord[]): string {
  const headers = [
    '复核时间',
    '复核人',
    '处置状态',
    '结论',
    '范围',
    '筛选条件',
    '待处置记录',
    '总记录',
    '路径数',
    '用户数',
    '操作统计',
    '路径样例',
    '用户样例',
    '活动记录ID',
  ]
  const rows = records.map((record) => [
    record.reviewed_at,
    record.reviewer,
    getActivityReviewDispositionStatusLabel(record.disposition_status),
    record.note,
    record.scope_label,
    record.filter_summary || '未筛选',
    record.review_count,
    record.total_count,
    record.path_count,
    record.user_count,
    formatActivityReviewActionCountsForExport(record.action_counts),
    record.path_samples?.join('; ') ?? '',
    record.user_samples?.join('; ') ?? '',
    record.activity_entry_ids.join('; '),
  ])
  return [
    headers.map(csvCell).join(','),
    ...rows.map((row) => row.map(csvCell).join(',')),
  ].join('\n')
}

function activityReviewExportFilename(now = new Date()): string {
  return `mnemonas-activity-review-records-${toRFC3339Seconds(now).replaceAll(':', '-')}.csv`
}

function getActivityReviewDispositionItems(entries: ActivityEntry[]): string[] {
  const paths = getUniqueActivityReviewValues(entries, (entry) => entry.path)
  const users = getUniqueActivityReviewValues(entries, (entry) => entry.user)
  const hasDestructive = entries.some((entry) => ACTIVITY_DESTRUCTIVE_ACTIONS.has(entry.action))
  const hasPathChange = entries.some((entry) => entry.action === 'rename' || entry.action === 'move')
  const hasShare = entries.some((entry) => ACTIVITY_SHARE_ACTIONS.has(entry.action))
  const hasRestore = entries.some((entry) => ACTIVITY_RESTORE_ACTIONS.has(entry.action))
  const hasWarning = entries.some(hasActivityWarningDetails)

  const items = [
    `确认影响范围：${entries.length} 条记录，涉及 ${paths.length || 0} 个路径、${users.length || 0} 个用户。`,
  ]

  if (hasDestructive) {
    items.push('删除类操作：检查回收站、版本历史和最近备份，确认是否需要恢复。')
  }
  if (hasPathChange) {
    items.push('路径变更：核对来源和目标路径，确认移动或重命名是否符合预期。')
  }
  if (hasShare) {
    items.push('分享变更：核对分享链接、密码、有效期和访问次数，关闭不再需要的公开链接。')
  }
  if (hasRestore) {
    items.push('恢复变更：检查恢复后的文件、权限和相关分享/收藏状态。')
  }
  if (hasWarning) {
    items.push('带警告记录：先处理持久化或清理警告，再把本次复核标记为完成。')
  }

  items.push('记录处置结论：在团队工单或运维记录中写明复核人、处理结果和时间。')
  return items
}

function ActivityReviewDetails({
  entries,
  isFiltered,
  reviewWindow,
}: {
  entries: ActivityEntry[]
  isFiltered: boolean
  reviewWindow: ActivityReviewWindow | null
}) {
  const reviewEntries = entries.filter(shouldReviewActivityEntry)
  if (reviewEntries.length === 0) {
    return null
  }

  const visibleEntries = reviewEntries.slice(0, 5)
  const destructiveCount = reviewEntries.filter((entry) => ACTIVITY_DESTRUCTIVE_ACTIONS.has(entry.action)).length
  const shareCount = reviewEntries.filter((entry) => ACTIVITY_SHARE_ACTIONS.has(entry.action)).length
  const warningCount = reviewEntries.filter(hasActivityWarningDetails).length
  const scopeLabel = getActivityReviewScopeLabel(isFiltered, reviewWindow)
  const dispositionItems = getActivityReviewDispositionItems(reviewEntries)

  return (
    <div aria-label="当前页复核明细" className="rounded-lg border border-warning/20 bg-warning/10 px-4 py-3 text-sm">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex items-center gap-2 font-medium text-warning">
            <AlertCircle size={16} />
            <span>当前页复核明细</span>
          </div>
          <p className="mt-1 text-xs text-warning/80">
            {scopeLabel}包含 {reviewEntries.length} 条需复核记录
          </p>
        </div>
        <div className="grid grid-cols-3 gap-3 text-right text-xs text-warning/80 sm:min-w-[18rem]">
          <div>
            <div className="text-base font-semibold text-warning">{destructiveCount}</div>
            <div>删除类</div>
          </div>
          <div>
            <div className="text-base font-semibold text-warning">{shareCount}</div>
            <div>分享类</div>
          </div>
          <div>
            <div className="text-base font-semibold text-warning">{warningCount}</div>
            <div>带警告</div>
          </div>
        </div>
      </div>

      <div className="mt-3 divide-y divide-warning/20">
        {visibleEntries.map((entry) => {
          const details = getActivityDetailEntries(entry.action, entry.details ?? {}).slice(0, 2)
          return (
            <div key={entry.id} className="grid gap-2 py-2 first:pt-0 last:pb-0 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-start">
              <div className="min-w-0">
                <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                  <span className="font-medium text-foreground">{getActionLabel(entry.action)}</span>
                  <span className="text-xs text-warning/80">{getActivityReviewReasons(entry).join(' · ')}</span>
                </div>
                <div className="mt-1 truncate text-xs text-default-600" title={entry.path || undefined}>
                  路径：{entry.path || '全局操作'}
                </div>
                {details.length > 0 && (
                  <div className="mt-1 flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-xs text-default-500">
                    {details.map((detail) => (
                      <span key={detail.key} className="min-w-0 truncate">复核：{detail.label}：{detail.value}</span>
                    ))}
                  </div>
                )}
              </div>
              <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-default-500 sm:justify-end">
                {entry.user && (
                  <span className="truncate">{entry.user}</span>
                )}
                <span className="shrink-0">{formatRelativeTime(entry.timestamp)}</span>
              </div>
            </div>
          )
        })}
      </div>
      {reviewEntries.length > visibleEntries.length && (
        <div className="mt-2 text-xs text-warning/80">
          还有 {reviewEntries.length - visibleEntries.length} 条需在下方列表继续复核
        </div>
      )}

      <div aria-label="复核处置清单" className="mt-3 rounded-lg border border-warning/20 bg-content1/70 px-3 py-2">
        <div className="flex items-center gap-2 text-xs font-medium text-warning">
          <ListChecks size={14} />
          <span>复核处置清单</span>
        </div>
        <ol className="mt-2 list-decimal space-y-1 pl-5 text-xs leading-5 text-default-600">
          {dispositionItems.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ol>
      </div>
    </div>
  )
}

function ActivityReviewDispositionRecorder({
  entries,
  totalEntries,
  isFiltered,
  reviewWindow,
  records,
  recordsError,
  followUpRecords,
  followUpTotal,
  followUpRecordsError,
  reviewReviewerFilter,
  reviewActivityEntryIDFilter,
  reviewTimeRangeFilter,
  reviewDispositionStatusFilter,
  reviewActionGroupFilter,
  reviewDispositionStatus,
  note,
  onNoteChange,
  onReviewReviewerFilterChange,
  onReviewActivityEntryIDFilterChange,
  onReviewTimeRangeFilterChange,
  onReviewDispositionStatusFilterChange,
  onReviewActionGroupFilterChange,
  onReviewDispositionStatusChange,
  onClearReviewRecordFilters,
  onExportReviewRecords,
  onShowFollowUpRecords,
  onShowShareReviewRecords,
  onTraceReviewRecord,
  onOpenReviewRecordVersions,
  onOpenReviewRecordTrash,
  onOpenReviewRecordShares,
  onUpdateReviewRecordDisposition,
  onRecord,
  onRecordFiltered,
  isRecording,
  isExportingReviewRecords,
  updatingReviewRecordID,
}: {
  entries: ActivityEntry[]
  totalEntries: number
  isFiltered: boolean
  reviewWindow: ActivityReviewWindow | null
  records: ActivityReviewRecord[]
  recordsError: unknown
  followUpRecords: ActivityReviewRecord[]
  followUpTotal: number
  followUpRecordsError: unknown
  reviewReviewerFilter: string
  reviewActivityEntryIDFilter: string
  reviewTimeRangeFilter: ActivityTimeRangeKey
  reviewDispositionStatusFilter: ActivityReviewDispositionFilterKey
  reviewActionGroupFilter: ActivityReviewActionGroupFilterKey
  reviewDispositionStatus: ActivityReviewDispositionStatus
  note: string
  onNoteChange: (value: string) => void
  onReviewReviewerFilterChange: (value: string) => void
  onReviewActivityEntryIDFilterChange: (value: string) => void
  onReviewTimeRangeFilterChange: (value: string | undefined) => void
  onReviewDispositionStatusFilterChange: (value: string | undefined) => void
  onReviewActionGroupFilterChange: (value: string | undefined) => void
  onReviewDispositionStatusChange: (value: string | undefined) => void
  onClearReviewRecordFilters: () => void
  onExportReviewRecords: () => void
  onShowFollowUpRecords: () => void
  onShowShareReviewRecords: () => void
  onTraceReviewRecord: (record: ActivityReviewRecord) => void
  onOpenReviewRecordVersions: (record: ActivityReviewRecord) => void
  onOpenReviewRecordTrash: (record: ActivityReviewRecord) => void
  onOpenReviewRecordShares: (record: ActivityReviewRecord) => void
  onUpdateReviewRecordDisposition: (record: ActivityReviewRecord, dispositionStatus: ActivityReviewDispositionStatus, note?: string) => void
  onRecord: () => void
  onRecordFiltered: () => void
  isRecording: boolean
  isExportingReviewRecords: boolean
  updatingReviewRecordID: string | null
}) {
  const [reviewRecordDispositionNotes, setReviewRecordDispositionNotes] = useState<Record<string, string>>({})
  const [expandedReviewRecordID, setExpandedReviewRecordID] = useState<string | null>(null)
  const reviewEntries = entries.filter(shouldReviewActivityEntry)
  if (reviewEntries.length === 0 && records.length === 0 && !recordsError && totalEntries <= entries.length) {
    return null
  }

  const paths = getUniqueActivityReviewValues(reviewEntries, (entry) => entry.path)
  const users = getUniqueActivityReviewValues(reviewEntries, (entry) => entry.user)
  const scopeLabel = getActivityReviewScopeLabel(isFiltered, reviewWindow)
  const trimmedNote = note.trim()
  const hasRecordFilters = Boolean(reviewReviewerFilter.trim() || reviewActivityEntryIDFilter.trim() || reviewTimeRangeFilter !== 'all' || reviewDispositionStatusFilter !== 'all' || reviewActionGroupFilter !== 'all')
  const hasFollowUpRecords = followUpTotal > 0 || followUpRecords.length > 0
  const hiddenFollowUpRecords = Math.max(followUpTotal - followUpRecords.length, 0)
  const getReviewRecordDispositionNote = (record: ActivityReviewRecord): string => reviewRecordDispositionNotes[record.id] ?? ''
  const getReviewRecordDispositionUpdateNote = (record: ActivityReviewRecord): string | undefined => {
    const noteValue = getReviewRecordDispositionNote(record).trim()
    return noteValue || undefined
  }
  const setReviewRecordDispositionNote = (record: ActivityReviewRecord, value: string) => {
    setReviewRecordDispositionNotes((current) => ({
      ...current,
      [record.id]: value,
    }))
  }
  const renderReviewRecordDetailPanel = (record: ActivityReviewRecord) => {
    const actionSummary = formatActivityReviewActionCounts(record.action_counts) || '未记录'
    const pathSamples = record.path_samples ?? []
    const userSamples = record.user_samples ?? []
    const primaryPath = getActivityReviewRecordPrimaryPath(record)
    const isShareReview = activityReviewRecordHasAction(record, ACTIVITY_SHARE_ACTIONS)

    return (
      <div aria-label={`复核记录详情 ${record.id}`} className="mt-2 rounded-lg border border-divider bg-content2/60 px-3 py-2 text-xs text-default-600 sm:col-span-2">
        <div className="grid gap-2 md:grid-cols-2">
          <div className="min-w-0">
            <div className="flex items-center gap-2 font-medium text-foreground">
              <ClipboardCheck size={14} />
              <span>复核详情</span>
            </div>
            <dl className="mt-2 grid gap-1">
              <div className="min-w-0">
                <dt className="text-default-400">处置状态</dt>
                <dd className="truncate text-foreground">{getActivityReviewDispositionStatusLabel(record.disposition_status)}</dd>
              </div>
              <div className="min-w-0">
                <dt className="text-default-400">操作类型</dt>
                <dd className="truncate text-foreground" title={actionSummary}>{actionSummary}</dd>
              </div>
              <div className="min-w-0">
                <dt className="text-default-400">复核时间</dt>
                <dd className="truncate text-foreground">{formatDate(record.reviewed_at)}</dd>
              </div>
              <div className="min-w-0">
                <dt className="text-default-400">筛选条件</dt>
                <dd className="truncate text-foreground" title={record.filter_summary || '未筛选'}>{record.filter_summary || '未筛选'}</dd>
              </div>
            </dl>
          </div>

          <div className="min-w-0">
            <div className="flex items-center gap-2 font-medium text-foreground">
              <ListChecks size={14} />
              <span>复核线索</span>
            </div>
            <dl className="mt-2 grid gap-1">
              <div className="min-w-0">
                <dt className="text-default-400">关联活动</dt>
                <dd className="truncate text-foreground" title={record.activity_entry_ids.join(', ')}>{record.activity_entry_ids.join(', ')}</dd>
              </div>
              {pathSamples.length > 0 && (
                <div className="min-w-0">
                  <dt className="text-default-400">完整路径样例</dt>
                  <dd className="truncate text-foreground" title={pathSamples.join(', ')}>{pathSamples.join(', ')}</dd>
                </div>
              )}
              {userSamples.length > 0 && (
                <div className="min-w-0">
                  <dt className="text-default-400">涉及用户</dt>
                  <dd className="truncate text-foreground" title={userSamples.join(', ')}>{userSamples.join(', ')}</dd>
                </div>
              )}
              {isShareReview && (
                <div className="min-w-0 rounded-lg border border-primary/15 bg-primary/5 px-2 py-1">
                  <dt className="flex items-center gap-1 text-primary">
                    <Share2 size={13} />
                    <span>分享处置线索</span>
                  </dt>
                  <dd className="mt-1 space-y-1 text-foreground">
                    {primaryPath && <div className="truncate" title={primaryPath}>主要路径：{primaryPath}</div>}
                    <div>核对项：密码、有效期、访问次数、是否仍需要公开访问</div>
                  </dd>
                </div>
              )}
            </dl>
          </div>
        </div>
      </div>
    )
  }
  const renderReviewRecordActions = (record: ActivityReviewRecord) => {
    const isUpdating = updatingReviewRecordID === record.id
    const isFollowUp = (record.disposition_status ?? 'documented') === 'needs_follow_up'
    const dispositionNote = getReviewRecordDispositionNote(record)

    return (
      <>
        <Button
          size="sm"
          variant="light"
          className="h-8 rounded-lg px-2.5"
          startContent={<Search size={13} />}
          onPress={() => onTraceReviewRecord(record)}
        >
          追踪活动
        </Button>
        {getActivityReviewRecordPrimaryPath(record) && activityReviewRecordHasRecoveryFollowUp(record) && (
          <>
            <Button
              size="sm"
              variant="light"
              className="h-8 rounded-lg px-2.5"
              startContent={<RotateCcw size={13} />}
              onPress={() => onOpenReviewRecordVersions(record)}
            >
              查看版本
            </Button>
            <Button
              size="sm"
              variant="light"
              className="h-8 rounded-lg px-2.5"
              startContent={<Trash2 size={13} />}
              onPress={() => onOpenReviewRecordTrash(record)}
            >
              查回收站
            </Button>
          </>
        )}
        {getActivityReviewRecordPrimaryPath(record) && activityReviewRecordHasAction(record, ACTIVITY_SHARE_ACTIONS) && (
          <Button
            size="sm"
            variant="light"
            className="h-8 rounded-lg px-2.5"
            startContent={<Share2 size={13} />}
            onPress={() => onOpenReviewRecordShares(record)}
          >
            处理分享
          </Button>
        )}
        {isFollowUp && (
          <div className="mt-1 flex basis-full flex-col gap-1">
            <textarea
              aria-label={`复核记录处置备注 ${record.id}`}
              className="min-h-9 w-full resize-y rounded-lg border border-divider bg-content2 px-2 py-1 text-xs text-foreground outline-none transition-colors placeholder:text-default-400 focus:border-primary"
              maxLength={300}
              placeholder="补充处置备注（可选）"
              value={dispositionNote}
              disabled={isUpdating}
              onChange={(event) => setReviewRecordDispositionNote(record, event.target.value)}
            />
            <div className="flex flex-wrap gap-1">
              <Button
                size="sm"
                variant="flat"
                className="h-8 rounded-lg px-2.5"
                isLoading={isUpdating}
                isDisabled={isUpdating}
                onPress={() => onUpdateReviewRecordDisposition(record, 'confirmed', getReviewRecordDispositionUpdateNote(record))}
              >
                确认保留
              </Button>
              <Button
                size="sm"
                variant="flat"
                className="h-8 rounded-lg px-2.5"
                isLoading={isUpdating}
                isDisabled={isUpdating}
                onPress={() => onUpdateReviewRecordDisposition(record, 'restored', getReviewRecordDispositionUpdateNote(record))}
              >
                标记已恢复
              </Button>
              <Button
                size="sm"
                variant="flat"
                className="h-8 rounded-lg px-2.5"
                isLoading={isUpdating}
                isDisabled={isUpdating}
                onPress={() => onUpdateReviewRecordDisposition(record, 'disabled', getReviewRecordDispositionUpdateNote(record))}
              >
                标记已禁用
              </Button>
            </div>
          </div>
        )}
      </>
    )
  }

  return (
    <div aria-label="活动复核记录" className="rounded-lg border border-divider bg-content1 px-4 py-3 text-sm">
      <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
        <div className="min-w-0">
          <div className="flex items-center gap-2 font-medium text-foreground">
            <ClipboardCheck size={16} />
            <span>活动复核记录</span>
          </div>
          <div className="mt-2 grid gap-2 text-xs text-default-600 sm:grid-cols-4">
            <div>
              <div className="text-base font-semibold text-foreground">{reviewEntries.length}</div>
              <div>待处置记录</div>
            </div>
            <div>
              <div className="text-base font-semibold text-foreground">{paths.length}</div>
              <div>涉及路径</div>
            </div>
            <div>
              <div className="text-base font-semibold text-foreground">{users.length}</div>
              <div>涉及用户</div>
            </div>
            <div>
              <div className="text-base font-semibold text-foreground">{totalEntries}</div>
              <div>{scopeLabel}总数</div>
            </div>
          </div>
        </div>

        <div className="w-full min-w-0 lg:max-w-xl">
          <Select
            placeholder="处置状态"
            size="sm"
            className="mb-2 w-full sm:w-40"
            aria-label="复核处置状态"
            selectedKeys={[reviewDispositionStatus]}
            onSelectionChange={(keys) => {
              onReviewDispositionStatusChange(Array.from(keys)[0]?.toString())
            }}
            startContent={<ListChecks size={14} />}
          >
            {ACTIVITY_REVIEW_DISPOSITION_OPTIONS.map((option) => (
              <SelectItem key={option.key}>
                {option.label}
              </SelectItem>
            ))}
          </Select>
          <textarea
            aria-label="复核处置结论"
            className="min-h-20 w-full resize-y rounded-lg border border-divider bg-content2 px-3 py-2 text-sm text-foreground outline-none transition-colors placeholder:text-default-400 focus:border-primary"
            maxLength={300}
            placeholder="填写本页复核结论"
            value={note}
            onChange={(event) => onNoteChange(event.target.value)}
          />
          <div className="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <span className="text-xs text-default-500">{trimmedNote.length}/300</span>
            <div className="flex flex-col gap-2 sm:flex-row sm:justify-end">
              <Button
                size="sm"
                variant="flat"
                className="h-8 rounded-lg"
                startContent={<ListChecks size={14} />}
                isDisabled={totalEntries === 0 || trimmedNote.length === 0 || isRecording}
                isLoading={isRecording}
                onPress={onRecordFiltered}
              >
                记录当前筛选复核
              </Button>
              <Button
                size="sm"
                color="primary"
                className="h-8 rounded-lg"
                startContent={<ClipboardCheck size={14} />}
                isDisabled={reviewEntries.length === 0 || trimmedNote.length === 0 || isRecording}
                isLoading={isRecording}
                onPress={onRecord}
              >
                记录本页复核
              </Button>
            </div>
          </div>
        </div>
      </div>

      {Boolean(recordsError) && (
        <div className="mt-3 rounded-lg border border-warning/20 bg-warning/10 px-3 py-2 text-xs text-warning">
          复核历史暂不可用，当前页仍可继续查看和筛选。
        </div>
      )}

      {(hasFollowUpRecords || Boolean(followUpRecordsError)) && (
        <div aria-label="需跟进复核批量视图" className="mt-3 rounded-lg border border-warning/25 bg-warning/10 px-3 py-2">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
            <div className="min-w-0">
              <div className="flex items-center gap-2 text-xs font-medium text-warning">
                <ListChecks size={14} />
                <span>需跟进复核</span>
              </div>
              <div className="mt-1 text-xs text-warning/80">
                {followUpRecordsError ? '需跟进复核暂不可用' : `当前还有 ${followUpTotal} 条复核记录需跟进`}
              </div>
            </div>
            {hasFollowUpRecords && (
              <Button
                size="sm"
                variant="flat"
                color="warning"
                className="h-8 rounded-lg px-2"
                startContent={<ListChecks size={14} />}
                onPress={onShowFollowUpRecords}
              >
                查看需跟进
              </Button>
            )}
          </div>

          {followUpRecords.length > 0 && (
            <div className="mt-2 grid gap-2 lg:grid-cols-3">
              {followUpRecords.map((record) => (
                <div key={record.id} className="min-w-0 rounded-lg border border-warning/20 bg-content1/80 px-3 py-2">
                  <div className="truncate text-xs font-medium text-foreground" title={record.note}>
                    {record.note}
                  </div>
                  <div className="mt-1 text-xs text-default-500">
                    {record.scope_label}：{record.review_count} 条待处置 / {record.total_count} 条总记录
                  </div>
                  {record.path_samples && record.path_samples.length > 0 && (
                    <div className="mt-1 truncate text-xs text-default-500" title={record.path_samples.join(', ')}>
                      路径样例：{record.path_samples.join(', ')}
                    </div>
                  )}
                  <div className="mt-1 flex items-center justify-between gap-2 text-xs text-default-500">
                    <span className="truncate">{record.reviewer}</span>
                    <span>{formatRelativeTime(record.reviewed_at)}</span>
                  </div>
                  <div className="mt-2 flex flex-wrap gap-1">
                    {renderReviewRecordActions(record)}
                  </div>
                </div>
              ))}
            </div>
          )}

          {hiddenFollowUpRecords > 0 && (
            <div className="mt-2 text-xs text-warning/80">
              其余 {hiddenFollowUpRecords} 条未在此处展开
            </div>
          )}
        </div>
      )}

      <div aria-label="复核历史筛选" className="mt-3 flex flex-col gap-2 rounded-lg border border-divider bg-content2/60 px-3 py-2 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-start">
          <Input
            aria-label="按复核人筛选"
            placeholder="按复核人筛选"
            size="sm"
            className="w-full sm:w-40"
            value={reviewReviewerFilter}
            onValueChange={onReviewReviewerFilterChange}
            startContent={<User size={14} />}
          />
          <Input
            aria-label="按关联活动筛选"
            placeholder="按关联活动筛选"
            size="sm"
            className="w-full sm:w-44"
            value={reviewActivityEntryIDFilter}
            onValueChange={onReviewActivityEntryIDFilterChange}
            startContent={<Search size={14} />}
          />
          <Select
            placeholder="复核类型"
            size="sm"
            className="w-full sm:w-36"
            aria-label="筛选复核类型"
            selectedKeys={[reviewActionGroupFilter]}
            onSelectionChange={(keys) => {
              onReviewActionGroupFilterChange(Array.from(keys)[0]?.toString())
            }}
            startContent={<Filter size={14} />}
          >
            {ACTIVITY_REVIEW_ACTION_GROUP_FILTER_OPTIONS.map((option) => (
              <SelectItem key={option.key}>
                {option.label}
              </SelectItem>
            ))}
          </Select>
          <Select
            placeholder="复核时间"
            size="sm"
            className="w-full sm:w-36"
            aria-label="筛选复核时间"
            selectedKeys={[reviewTimeRangeFilter]}
            onSelectionChange={(keys) => {
              onReviewTimeRangeFilterChange(Array.from(keys)[0]?.toString())
            }}
            startContent={<CalendarDays size={14} />}
          >
            {ACTIVITY_TIME_RANGES.map((range) => (
              <SelectItem key={range.key}>
                {range.label}
              </SelectItem>
            ))}
          </Select>
          <Select
            placeholder="历史处置"
            size="sm"
            className="w-full sm:w-36"
            aria-label="筛选复核处置状态"
            selectedKeys={[reviewDispositionStatusFilter]}
            onSelectionChange={(keys) => {
              onReviewDispositionStatusFilterChange(Array.from(keys)[0]?.toString())
            }}
            startContent={<ListChecks size={14} />}
          >
            {ACTIVITY_REVIEW_DISPOSITION_FILTER_OPTIONS.map((option) => (
              <SelectItem key={option.key}>
                {option.label}
              </SelectItem>
            ))}
          </Select>
        </div>
        <div className="flex flex-col gap-2 sm:flex-row sm:justify-end">
          <Button
            size="sm"
            variant="flat"
            className="h-8 rounded-lg px-2"
            startContent={<Share2 size={14} />}
            isDisabled={reviewActionGroupFilter === 'share'}
            onPress={onShowShareReviewRecords}
          >
            只看分享复核
          </Button>
          <Button
            size="sm"
            variant="flat"
            color="warning"
            className="h-8 rounded-lg px-2"
            startContent={<ListChecks size={14} />}
            isDisabled={reviewDispositionStatusFilter === 'needs_follow_up'}
            onPress={onShowFollowUpRecords}
          >
            只看需跟进
          </Button>
          <Button
            size="sm"
            variant="flat"
            className="h-8 rounded-lg px-2"
            startContent={<Download size={14} />}
            isLoading={isExportingReviewRecords}
            isDisabled={isExportingReviewRecords}
            onPress={onExportReviewRecords}
          >
            导出复核记录
          </Button>
          <Button
            size="sm"
            variant="light"
            className="h-8 rounded-lg px-2 text-default-500"
            startContent={<X size={14} />}
            isDisabled={!hasRecordFilters}
            onPress={onClearReviewRecordFilters}
          >
            清除复核筛选
          </Button>
        </div>
      </div>

      {records.length > 0 && (
        <div className="mt-3 divide-y divide-divider rounded-lg border border-divider">
          {records.map((record) => {
            const isExpanded = expandedReviewRecordID === record.id
            return (
              <div key={record.id} className="grid gap-2 px-3 py-2 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-start">
                <div className="min-w-0">
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    <span className="min-w-0 font-medium text-foreground">{record.note}</span>
                    <Chip size="sm" variant="flat" color={record.disposition_status === 'needs_follow_up' ? 'warning' : 'primary'}>
                      {getActivityReviewDispositionStatusLabel(record.disposition_status)}
                    </Chip>
                  </div>
                  <div className="mt-1 text-xs text-default-500">
                    {record.scope_label}：{record.review_count} 条待处置 / {record.total_count} 条总记录 · {record.path_count} 个路径 · {record.user_count} 个用户
                  </div>
                  {record.action_counts && (
                    <div className="mt-1 truncate text-xs text-default-500" title={formatActivityReviewActionCounts(record.action_counts)}>
                      类型：{formatActivityReviewActionCounts(record.action_counts)}
                    </div>
                  )}
                  {record.path_samples && record.path_samples.length > 0 && (
                    <div className="mt-1 truncate text-xs text-default-500" title={record.path_samples.join(', ')}>
                      路径样例：{record.path_samples.join(', ')}
                    </div>
                  )}
                  {record.user_samples && record.user_samples.length > 0 && (
                    <div className="mt-1 truncate text-xs text-default-500" title={record.user_samples.join(', ')}>
                      用户样例：{record.user_samples.join(', ')}
                    </div>
                  )}
                  <div className="mt-1 truncate text-xs text-default-500" title={record.filter_summary || '未筛选'}>
                    条件：{record.filter_summary || '未筛选'}
                  </div>
                </div>
                <div className="text-xs text-default-500 sm:text-right">
                  <div>{record.reviewer}</div>
                  <div>{formatRelativeTime(record.reviewed_at)}</div>
                  <div className="mt-2 flex flex-wrap gap-1 sm:justify-end">
                    <Button
                      size="sm"
                      variant="light"
                      className="h-8 rounded-lg px-2.5"
                      startContent={<ClipboardCheck size={13} />}
                      onPress={() => setExpandedReviewRecordID(isExpanded ? null : record.id)}
                    >
                      {isExpanded ? '收起详情' : '查看详情'}
                    </Button>
                    {renderReviewRecordActions(record)}
                  </div>
                </div>
                {isExpanded && renderReviewRecordDetailPanel(record)}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
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
    <div className="grid grid-cols-2 gap-2 sm:gap-3 xl:grid-cols-4">
      <StatCard
        title="累计操作"
        value={stats?.total ?? '--'}
        subtitle={stats ? (isFiltered ? '当前筛选结果' : '历史记录总量') : '正在加载统计'}
        icon={BarChart3}
        tone="primary"
        density="compact"
      />
      <StatCard
        title="今日操作"
        value={stats?.today ?? '--'}
        subtitle={stats ? '当天新增记录' : '正在加载统计'}
        icon={CalendarDays}
        tone="success"
        density="compact"
      />
      <StatCard
        title="最常见操作"
        value={topAction ? getActivityActionLabel(topAction.key) : '--'}
        subtitle={topAction ? `${topAction.count} 次` : '暂无操作类型'}
        icon={Database}
        tone="warning"
        density="compact"
      />
      <StatCard
        title="最活跃用户"
        value={topUser?.key ?? '--'}
        subtitle={topUser ? `${topUser.count} 次` : '暂无用户记录'}
        icon={User}
        tone="secondary"
        density="compact"
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
  const location = useLocation()
  const navigate = useNavigate()
  const [initialURLFilters] = useState(() => parseActivityURLFilters(location.search))
  const [page, setPage] = useState(1)
  const [actionFilter, setActionFilter] = useState<ActionType | ''>(initialURLFilters.actionFilter)
  const [actionGroupFilter, setActionGroupFilter] = useState<ActivityActionGroup | ''>(initialURLFilters.actionGroupFilter)
  const [timeRangeFilter, setTimeRangeFilter] = useState<ActivityTimeRangeKey>(initialURLFilters.timeRangeFilter)
  const [reviewWindowFilter, setReviewWindowFilter] = useState<ActivityReviewWindow | null>(null)
  const [pathFilter, setPathFilter] = useState(initialURLFilters.pathFilter)
  const [userFilter, setUserFilter] = useState(initialURLFilters.userFilter)
  const [isClearConfirmOpen, setIsClearConfirmOpen] = useState(false)
  const [reviewDispositionNote, setReviewDispositionNote] = useState('')
  const [reviewDispositionStatus, setReviewDispositionStatus] = useState<ActivityReviewDispositionStatus>('documented')
  const [reviewRecordReviewerFilter, setReviewRecordReviewerFilter] = useState('')
  const [reviewRecordActivityEntryIDFilter, setReviewRecordActivityEntryIDFilter] = useState('')
  const [reviewRecordTimeRangeFilter, setReviewRecordTimeRangeFilter] = useState<ActivityTimeRangeKey>('all')
  const [reviewRecordDispositionStatusFilter, setReviewRecordDispositionStatusFilter] = useState<ActivityReviewDispositionFilterKey>('all')
  const [reviewRecordActionGroupFilter, setReviewRecordActionGroupFilter] = useState<ActivityReviewActionGroupFilterKey>('all')
  const [isRecordingFilteredReview, setIsRecordingFilteredReview] = useState(false)
  const [isExportingReviewRecords, setIsExportingReviewRecords] = useState(false)
  const pageSize = 20
  const user = useUser()
  const isAdmin = useIsAdmin()
  const { rootPath, hasInvalidHomeDir } = resolveUserHomeScope(user)
  const authScopeKey = user?.id ?? 'anonymous'
  const homeScopeKey = hasInvalidHomeDir ? '__invalid__' : (rootPath ?? '/')
  const queryClient = useQueryClient()
  const clearActivityAbortControllerRef = useRef<AbortController | null>(null)
  const normalizedUserFilter = isAdmin ? userFilter.trim() : ''
  const normalizedReviewRecordReviewerFilter = isAdmin ? reviewRecordReviewerFilter.trim() : ''
  const normalizedReviewRecordActivityEntryIDFilter = isAdmin ? reviewRecordActivityEntryIDFilter.trim() : ''
  const normalizedReviewRecordDispositionStatusFilter = isAdmin && reviewRecordDispositionStatusFilter !== 'all'
    ? reviewRecordDispositionStatusFilter
    : undefined
  const normalizedReviewRecordActionGroupFilter = isAdmin && reviewRecordActionGroupFilter !== 'all'
    ? reviewRecordActionGroupFilter
    : undefined
  const pathFilterState = useMemo(() => parseActivityPathFilter(pathFilter), [pathFilter])
  const normalizedPathFilter = pathFilterState.normalizedPath
  const pathFilterErrorMessage = pathFilterState.errorMessage
  const timeRangeSinceFilter = useMemo(() => getActivityTimeRangeSince(timeRangeFilter), [timeRangeFilter])
  const reviewRecordSinceFilter = useMemo(() => getActivityTimeRangeSince(reviewRecordTimeRangeFilter), [reviewRecordTimeRangeFilter])
  const activityReviewRecordsQueryKey = useMemo(
    () => ['activity-review-records', authScopeKey, normalizedReviewRecordReviewerFilter, normalizedReviewRecordActivityEntryIDFilter, normalizedReviewRecordDispositionStatusFilter, normalizedReviewRecordActionGroupFilter, reviewRecordSinceFilter] as const,
    [authScopeKey, normalizedReviewRecordReviewerFilter, normalizedReviewRecordActivityEntryIDFilter, normalizedReviewRecordDispositionStatusFilter, normalizedReviewRecordActionGroupFilter, reviewRecordSinceFilter],
  )
  const activityReviewFollowUpRecordsQueryKey = useMemo(
    () => ['activity-review-follow-up-records', authScopeKey] as const,
    [authScopeKey],
  )
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

  const { data: reviewDispositionRecordsData, error: reviewDispositionRecordsError } = useQuery({
    queryKey: activityReviewRecordsQueryKey,
    queryFn: ({ signal }) => listActivityReviewRecords({
      limit: 5,
      reviewer: normalizedReviewRecordReviewerFilter || undefined,
      activityEntryId: normalizedReviewRecordActivityEntryIDFilter || undefined,
      dispositionStatus: normalizedReviewRecordDispositionStatusFilter,
      actionGroup: normalizedReviewRecordActionGroupFilter,
      since: reviewRecordSinceFilter,
      signal,
    }),
    enabled: isAdmin && !hasInvalidHomeDir,
  })

  const { data: reviewFollowUpRecordsData, error: reviewFollowUpRecordsError } = useQuery({
    queryKey: activityReviewFollowUpRecordsQueryKey,
    queryFn: ({ signal }) => listActivityReviewRecords({
      limit: ACTIVITY_REVIEW_FOLLOW_UP_LIMIT,
      dispositionStatus: 'needs_follow_up',
      signal,
    }),
    enabled: isAdmin && !hasInvalidHomeDir,
  })

  const totalPages = useMemo(() => {
    if (!data?.total) return 1
    return Math.ceil(data.total / pageSize)
  }, [data?.total])
  const errorState = getActivityErrorState(error)
  const entries = pathFilterErrorMessage ? [] : (data?.items ?? [])
  const totalEntries = pathFilterErrorMessage ? 0 : (data?.total ?? entries.length)
  const reviewDispositionRecords = reviewDispositionRecordsData?.items ?? []
  const rawReviewFollowUpRecords = reviewFollowUpRecordsData?.items ?? []
  const reviewFollowUpRecords = rawReviewFollowUpRecords.filter((record) => (record.disposition_status ?? 'documented') === 'needs_follow_up')
  const reviewFollowUpTotal = rawReviewFollowUpRecords.length === reviewFollowUpRecords.length
    ? (reviewFollowUpRecordsData?.total ?? reviewFollowUpRecords.length)
    : reviewFollowUpRecords.length

  const clearActivityMutation = useMutation({
    mutationFn: async ({ signal }: { signal: AbortSignal }) => clearActivity({ signal }),
    retry: false,
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }

      setPage(1)
      setIsClearConfirmOpen(false)
      void queryClient.invalidateQueries({ queryKey: ['activity'] })
      void queryClient.invalidateQueries({ queryKey: ['activity-stats'] })
      addToast({
        title: result.warning ? '最近操作已清空，但存在警告' : '最近操作已清空',
        description: result.warning ? getNonBlankToastDescription(result.message) : undefined,
        color: result.warning ? 'warning' : 'success',
      })
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

  const activityReviewMutation = useMutation({
    mutationFn: ({ input }: { input: ActivityReviewRecordCreateInput; successTitle: string }) => createActivityReviewRecord(input),
    retry: false,
    onSuccess: (record, variables) => {
      if (activityReviewRecordMatchesFilters(record, normalizedReviewRecordReviewerFilter, normalizedReviewRecordActivityEntryIDFilter, normalizedReviewRecordActionGroupFilter, reviewRecordSinceFilter, normalizedReviewRecordDispositionStatusFilter)) {
        queryClient.setQueryData<ActivityReviewRecordListResponse>(activityReviewRecordsQueryKey, (current) => {
          return prependActivityReviewRecord(current, record, 5)
        })
      }
      if ((record.disposition_status ?? 'documented') === 'needs_follow_up') {
        queryClient.setQueryData<ActivityReviewRecordListResponse>(activityReviewFollowUpRecordsQueryKey, (current) => {
          return prependActivityReviewRecord(current, record, ACTIVITY_REVIEW_FOLLOW_UP_LIMIT)
        })
      }
      setReviewDispositionNote('')
      setReviewDispositionStatus('documented')
      addToast({ title: variables.successTitle, color: 'success' })
    },
    onError: (mutationError: unknown) => {
      addToast({
        title: '记录复核失败',
        description: getUserFacingErrorDescription(mutationError),
        color: 'danger',
      })
    },
  })

  const activityReviewStatusMutation = useMutation({
    mutationFn: ({ recordID, dispositionStatus, note }: { recordID: string; dispositionStatus: ActivityReviewDispositionStatus; note?: string }) => {
      return updateActivityReviewRecordDisposition(recordID, {
        disposition_status: dispositionStatus,
        ...(note ? { note } : {}),
      })
    },
    retry: false,
    onSuccess: (record) => {
      queryClient.setQueryData<ActivityReviewRecordListResponse>(activityReviewRecordsQueryKey, (current) => (
        updateActivityReviewRecordList(
          current,
          record,
          (candidate) => activityReviewRecordMatchesFilters(
            candidate,
            normalizedReviewRecordReviewerFilter,
            normalizedReviewRecordActivityEntryIDFilter,
            normalizedReviewRecordActionGroupFilter,
            reviewRecordSinceFilter,
            normalizedReviewRecordDispositionStatusFilter,
          ),
        )
      ))
      queryClient.setQueryData<ActivityReviewRecordListResponse>(activityReviewFollowUpRecordsQueryKey, (current) => (
        updateActivityReviewRecordList(
          current,
          record,
          (candidate) => (candidate.disposition_status ?? 'documented') === 'needs_follow_up',
        )
      ))
      addToast({
        title: `复核状态已更新为${getActivityReviewDispositionStatusLabel(record.disposition_status)}`,
        color: 'success',
      })
    },
    onError: (mutationError: unknown) => {
      addToast({
        title: '更新复核状态失败',
        description: getUserFacingErrorDescription(mutationError),
        color: 'danger',
      })
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

  const handleReviewRecordTimeRangeChange = (nextValue: string | undefined) => {
    setReviewRecordTimeRangeFilter(isActivityTimeRangeKey(nextValue) ? nextValue : 'all')
  }

  const handleReviewRecordDispositionStatusFilterChange = (nextValue: string | undefined) => {
    setReviewRecordDispositionStatusFilter(isActivityReviewDispositionFilterKey(nextValue) ? nextValue : 'all')
  }

  const handleReviewRecordActionGroupFilterChange = (nextValue: string | undefined) => {
    setReviewRecordActionGroupFilter(isActivityReviewActionGroupFilterKey(nextValue) ? nextValue : 'all')
  }

  const handleReviewDispositionStatusChange = (nextValue: string | undefined) => {
    if (isActivityReviewDispositionStatus(nextValue)) {
      setReviewDispositionStatus(nextValue)
    }
  }

  const handleClearReviewRecordFilters = () => {
    setReviewRecordReviewerFilter('')
    setReviewRecordActivityEntryIDFilter('')
    setReviewRecordTimeRangeFilter('all')
    setReviewRecordDispositionStatusFilter('all')
    setReviewRecordActionGroupFilter('all')
  }

  const handleShowFollowUpReviewRecords = () => {
    setReviewRecordReviewerFilter('')
    setReviewRecordActivityEntryIDFilter('')
    setReviewRecordTimeRangeFilter('all')
    setReviewRecordDispositionStatusFilter('needs_follow_up')
    setReviewRecordActionGroupFilter('all')
  }

  const handleShowShareReviewRecords = () => {
    setReviewRecordReviewerFilter('')
    setReviewRecordActivityEntryIDFilter('')
    setReviewRecordTimeRangeFilter('all')
    setReviewRecordDispositionStatusFilter('all')
    setReviewRecordActionGroupFilter('share')
  }

  const handleTraceReviewRecordActivity = (record: ActivityReviewRecord) => {
    const nextPathFilter = getActivityReviewRecordPrimaryPath(record)
    const nextActionGroupFilter = getActivityReviewRecordTraceActionGroup(record)
    if (!nextPathFilter && !nextActionGroupFilter) {
      addToast({ title: '没有可追踪的活动线索', color: 'warning' })
      return
    }

    setReviewWindowFilter(null)
    setTimeRangeFilter('all')
    setActionFilter('')
    setActionGroupFilter(nextActionGroupFilter)
    setPathFilter(nextPathFilter)
    setUserFilter('')
    setPage(1)
    const traceDescription = nextPathFilter
      ? `路径 ${nextPathFilter}${nextActionGroupFilter ? ` · 分组 ${getActivityActionGroupLabel(nextActionGroupFilter)}` : ''}`
      : nextActionGroupFilter
        ? `分组 ${getActivityActionGroupLabel(nextActionGroupFilter)}`
        : undefined
    addToast({
      title: '已切换到相关活动筛选',
      description: traceDescription,
      color: 'success',
    })
  }

  const handleOpenActivityEntryVersions = (entry: ActivityEntry) => {
    const path = getActivityEntryPrimaryPath(entry)
    if (!path) {
      addToast({ title: '没有可查看版本的路径', color: 'warning' })
      return
    }
    navigate(`/versions?path=${encodeURIComponent(path)}`)
  }

  const handleOpenActivityEntryTrash = (entry: ActivityEntry) => {
    const path = getActivityEntryPrimaryPath(entry)
    if (!path) {
      addToast({ title: '没有可查询回收站的路径', color: 'warning' })
      return
    }
    navigate(`/trash?path=${encodeURIComponent(path)}`)
  }

  const handleOpenActivityEntryShares = (entry: ActivityEntry) => {
    const path = getActivityEntryPrimaryPath(entry)
    if (!path) {
      addToast({ title: '没有可处理分享的路径', color: 'warning' })
      return
    }

    const params = new URLSearchParams({
      tab: 'shares',
      share_path: path,
    })
    navigate(`/settings?${params.toString()}`)
  }

  const handleOpenReviewRecordVersions = (record: ActivityReviewRecord) => {
    const path = getActivityReviewRecordPrimaryPath(record)
    if (!path) {
      addToast({ title: '没有可查看版本的路径', color: 'warning' })
      return
    }
    navigate(`/versions?path=${encodeURIComponent(path)}`)
  }

  const handleOpenReviewRecordTrash = (record: ActivityReviewRecord) => {
    const path = getActivityReviewRecordPrimaryPath(record)
    if (!path) {
      addToast({ title: '没有可查询回收站的路径', color: 'warning' })
      return
    }
    navigate(`/trash?path=${encodeURIComponent(path)}`)
  }

  const handleOpenReviewRecordShares = (record: ActivityReviewRecord) => {
    const path = getActivityReviewRecordPrimaryPath(record)
    if (!path) {
      addToast({ title: '没有可处理分享的路径', color: 'warning' })
      return
    }

    const params = new URLSearchParams({
      tab: 'shares',
      share_path: path,
    })
    navigate(`/settings?${params.toString()}`)
  }

  const handleUpdateReviewRecordDisposition = (record: ActivityReviewRecord, dispositionStatus: ActivityReviewDispositionStatus, note?: string) => {
    if (activityReviewStatusMutation.isPending) {
      return
    }
    activityReviewStatusMutation.mutate({ recordID: record.id, dispositionStatus, note })
  }

  const handleExportReviewRecords = async () => {
    setIsExportingReviewRecords(true)
    try {
      const result = await listActivityReviewRecords({
        limit: ACTIVITY_REVIEW_EXPORT_LIMIT,
        offset: 0,
        reviewer: normalizedReviewRecordReviewerFilter || undefined,
        activityEntryId: normalizedReviewRecordActivityEntryIDFilter || undefined,
        dispositionStatus: normalizedReviewRecordDispositionStatusFilter,
        actionGroup: normalizedReviewRecordActionGroupFilter,
        since: reviewRecordSinceFilter,
      })
      if (result.items.length === 0) {
        addToast({ title: '没有可导出的复核记录', color: 'warning' })
        return
      }

      const csv = `\uFEFF${buildActivityReviewRecordsCSV(result.items)}`
      triggerBrowserDownload(new Blob([csv], { type: 'text/csv;charset=utf-8' }), activityReviewExportFilename())
      addToast({
        title: '复核记录已导出',
        description: result.total > result.items.length
          ? `已导出最近 ${result.items.length} 条，当前筛选共有 ${result.total} 条。`
          : `已导出 ${result.items.length} 条复核记录。`,
        color: 'success',
      })
    } catch (exportError) {
      addToast({
        title: '导出复核记录失败',
        description: getUserFacingErrorDescription(exportError),
        color: 'danger',
      })
    } finally {
      setIsExportingReviewRecords(false)
    }
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

  const handleRecordActivityReview = () => {
    const reviewEntries = entries.filter(shouldReviewActivityEntry)
    const note = reviewDispositionNote.trim()
    if (reviewEntries.length === 0) {
      addToast({ title: '当前页没有待处置记录', color: 'warning' })
      return
    }
    if (!note) {
      addToast({ title: '请输入复核处置结论', color: 'warning' })
      return
    }

    activityReviewMutation.mutate({
      input: buildActivityReviewRecordInput({
        reviewEntries,
        totalEntries,
        note,
        scopeLabel: getActivityReviewScopeLabel(hasActiveFilters, reviewWindowFilter),
        filterSummary: getActivityReviewFilterSummary({
          timeRangeFilter,
          reviewWindow: reviewWindowFilter,
          actionFilter,
          actionGroupFilter,
          pathFilter: normalizedPathFilter,
          userFilter: normalizedUserFilter,
        }),
        dispositionStatus: reviewDispositionStatus,
      }),
      successTitle: '本页复核已记录',
    })
  }

  const handleRecordFilteredActivityReview = async () => {
    const note = reviewDispositionNote.trim()
    if (!note) {
      addToast({ title: '请输入复核处置结论', color: 'warning' })
      return
    }

    setIsRecordingFilteredReview(true)
    let createStarted = false
    try {
      const result = await listActivity({
        limit: ACTIVITY_REVIEW_FILTERED_LIMIT,
        offset: 0,
        action: actionFilter || undefined,
        actionGroup: actionGroupFilter || undefined,
        path: normalizedPathFilter || undefined,
        user: normalizedUserFilter || undefined,
        since: sinceFilter,
        until: untilFilter,
      })
      if (result.total > ACTIVITY_REVIEW_FILTERED_LIMIT) {
        addToast({
          title: '当前筛选范围过大',
          description: `当前筛选包含 ${result.total} 条记录，请先缩小到 ${ACTIVITY_REVIEW_FILTERED_LIMIT} 条以内再记录复核。`,
          color: 'warning',
        })
        return
      }

      const reviewEntries = result.items.filter(shouldReviewActivityEntry)
      if (reviewEntries.length === 0) {
        addToast({ title: '当前筛选没有待处置记录', color: 'warning' })
        return
      }

      createStarted = true
      await activityReviewMutation.mutateAsync({
        input: buildActivityReviewRecordInput({
          reviewEntries,
          totalEntries: result.total,
          note,
          scopeLabel: getActivityReviewFilteredScopeLabel(hasActiveFilters, reviewWindowFilter),
          filterSummary: getActivityReviewFilterSummary({
            timeRangeFilter,
            reviewWindow: reviewWindowFilter,
            actionFilter,
            actionGroupFilter,
            pathFilter: normalizedPathFilter,
            userFilter: normalizedUserFilter,
          }),
          dispositionStatus: reviewDispositionStatus,
        }),
        successTitle: '当前筛选复核已记录',
      })
    } catch (reviewError) {
      if (!createStarted && !isAbortError(reviewError)) {
        addToast({
          title: '记录复核失败',
          description: getUserFacingErrorDescription(reviewError),
          color: 'danger',
        })
      }
    } finally {
      setIsRecordingFilteredReview(false)
    }
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
      <div
        role="status"
        aria-label="加载最近操作"
        aria-busy="true"
        className="flex h-full items-center justify-center p-6 lg:p-8"
      >
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

      {!pathFilterErrorMessage && (
        <ActivityReviewDetails
          entries={entries}
          isFiltered={hasActiveFilters}
          reviewWindow={reviewWindowFilter}
        />
      )}

      {isAdmin && !pathFilterErrorMessage && (
        <ActivityReviewDispositionRecorder
          entries={entries}
          totalEntries={totalEntries}
          isFiltered={hasActiveFilters}
          reviewWindow={reviewWindowFilter}
          records={reviewDispositionRecords}
          recordsError={reviewDispositionRecordsError}
          followUpRecords={reviewFollowUpRecords}
          followUpTotal={reviewFollowUpTotal}
          followUpRecordsError={reviewFollowUpRecordsError}
          reviewReviewerFilter={reviewRecordReviewerFilter}
          reviewActivityEntryIDFilter={reviewRecordActivityEntryIDFilter}
          reviewTimeRangeFilter={reviewRecordTimeRangeFilter}
          reviewDispositionStatusFilter={reviewRecordDispositionStatusFilter}
          reviewActionGroupFilter={reviewRecordActionGroupFilter}
          reviewDispositionStatus={reviewDispositionStatus}
          note={reviewDispositionNote}
          onNoteChange={setReviewDispositionNote}
          onReviewReviewerFilterChange={setReviewRecordReviewerFilter}
          onReviewActivityEntryIDFilterChange={setReviewRecordActivityEntryIDFilter}
          onReviewTimeRangeFilterChange={handleReviewRecordTimeRangeChange}
          onReviewDispositionStatusFilterChange={handleReviewRecordDispositionStatusFilterChange}
          onReviewActionGroupFilterChange={handleReviewRecordActionGroupFilterChange}
          onReviewDispositionStatusChange={handleReviewDispositionStatusChange}
          onClearReviewRecordFilters={handleClearReviewRecordFilters}
          onExportReviewRecords={handleExportReviewRecords}
          onShowFollowUpRecords={handleShowFollowUpReviewRecords}
          onShowShareReviewRecords={handleShowShareReviewRecords}
          onTraceReviewRecord={handleTraceReviewRecordActivity}
          onOpenReviewRecordVersions={handleOpenReviewRecordVersions}
          onOpenReviewRecordTrash={handleOpenReviewRecordTrash}
          onOpenReviewRecordShares={handleOpenReviewRecordShares}
          onUpdateReviewRecordDisposition={handleUpdateReviewRecordDisposition}
          onRecord={handleRecordActivityReview}
          onRecordFiltered={handleRecordFilteredActivityReview}
          isRecording={activityReviewMutation.isPending || isRecordingFilteredReview}
          isExportingReviewRecords={isExportingReviewRecords}
          updatingReviewRecordID={activityReviewStatusMutation.isPending ? activityReviewStatusMutation.variables?.recordID ?? null : null}
        />
      )}

      {/* Filter chips */}
      {hasActiveFilters && (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm text-default-500">当前筛选：</span>
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
            className="h-8 rounded-lg px-2.5 text-default-500"
            startContent={<X size={14} />}
            onPress={handleClearAllFilters}
          >
            清空全部筛选
          </Button>
        </div>
      )}

      {/* Activity list */}
      <div role="list" aria-label="最近操作列表" className="card-mnemonas min-h-0 flex-1 overflow-auto rounded-lg">
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
            <ActivityRow
              key={entry.id}
              entry={entry}
              onOpenVersions={handleOpenActivityEntryVersions}
              onOpenTrash={handleOpenActivityEntryTrash}
              onOpenShares={handleOpenActivityEntryShares}
            />
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
