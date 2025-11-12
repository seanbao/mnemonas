import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Card,
  CardBody,
  Button,
  Chip,
  Dropdown,
  DropdownTrigger,
  DropdownMenu,
  DropdownItem,
  addToast,
  Modal,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalFooter,
} from '@heroui/react'
import {
  Link2,
  MoreVertical,
  Copy,
  Trash2,
  ToggleLeft,
  ToggleRight,
  Lock,
  Clock,
  Eye,
  RefreshCw,
  AlertCircle,
  Activity,
  ShieldAlert,
  X,
} from 'lucide-react'
import { 
  listShares, 
  deleteShare, 
  updateShare, 
  copyShareUrl,
  formatExpiration,
  ShareError,
  type Share,
  type ShareRiskReason,
} from '@/api/share'
import {
  createActivityReviewRecord,
  listActivity,
  type ActivityActionCountMap,
  type ActivityEntry,
  type ActivityReviewRecordCreateInput,
  type ActivityReviewShareDispositionDetail,
} from '@/api/activity'
import { EmptyState } from '@/components/ui/EmptyState'
import { FileIcon } from '@/components/ui/FileIcon'
import { GENERIC_LOAD_ERROR_DESCRIPTION, getUserFacingErrorDescription } from '@/lib/apiMessages'
import { formatShareReviewReport, summarizeShareReview, type ShareReviewSummary } from '@/lib/shareReview'
import { copyTextToClipboard, normalizePath } from '@/lib/utils'
import { normalizeShareReviewFilter, type ShareReviewFilter } from './reviewFilters'

function getShareFeatureState(error: unknown): 'disabled' | 'unavailable' | null {
  if (!(error instanceof ShareError)) {
    return null
  }

  if (error.isFeatureDisabled) {
    return 'disabled'
  }

  if (error.isUnavailable) {
    return 'unavailable'
  }

  return null
}

function getShareActionErrorToast(
  error: unknown,
  titles: {
    unavailable: string
    failure: string
  }
): {
  title: string
  description?: string
  color: 'warning' | 'danger'
} {
  if (error instanceof ShareError) {
    if (error.isFeatureDisabled) {
      return {
        title: '分享功能已关闭',
        description: '当前服务已关闭分享功能。启用后可在此管理已创建的分享链接。',
        color: 'warning',
      }
    }

    if (error.isUnavailable) {
      return {
        title: titles.unavailable,
        description: '分享服务当前不可用，请检查设备状态或稍后重试。',
        color: 'warning',
      }
    }
  }

  return {
    title: titles.failure,
    description: getUserFacingErrorDescription(error),
    color: 'danger',
  }
}

function getShareLoadErrorToast(error: unknown): {
  title: string
  description?: string
  color: 'warning' | 'danger'
} {
  return {
    title: '刷新分享列表失败',
    description: getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION),
    color: 'danger',
  }
}

function getMissingShareToast(): {
  title: string
  description: string
  color: 'warning'
} {
  return {
    title: '分享已不存在',
    description: '该分享可能已被其他操作删除，列表已同步更新。',
    color: 'warning',
  }
}

function getShareDeleteSuccessToast(result: { warning: boolean }): {
  title: string
  color: 'success' | 'warning'
} {
  return result.warning
    ? { title: '分享已删除，但存在警告', color: 'warning' }
    : { title: '分享已删除', color: 'success' }
}

function getShareToggleSuccessToast(enabled: boolean, warning: boolean): {
  title: string
  color: 'success' | 'warning'
} {
  const actionTitle = enabled ? '分享已启用' : '分享已禁用'
  return warning
    ? { title: `${actionTitle}，但存在警告`, color: 'warning' }
    : { title: actionTitle, color: 'success' }
}

function isAbortError(error: unknown): boolean {
  return typeof error === 'object'
    && error !== null
    && 'name' in error
    && (error as { name?: unknown }).name === 'AbortError'
}

function isRiskyShare(share: Share): boolean {
  return share.enabled && !!share.risk && share.risk.level !== 'none'
}

function isHighRiskShare(share: Share): boolean {
  return share.enabled && share.risk?.level === 'high'
}

function shareHasRiskCode(share: Share, code: string): boolean {
  return share.enabled && (share.risk?.reasons?.some(reason => !reason.resolved && reason.code === code) ?? false)
}

function getRiskPresentation(level?: string): { label: string; color: 'default' | 'warning' | 'danger' | 'primary' } | null {
  switch (level) {
  case 'high':
    return { label: '需处理', color: 'danger' }
  case 'medium':
    return { label: '需关注', color: 'warning' }
  case 'low':
    return { label: '低风险', color: 'primary' }
  default:
    return null
  }
}

interface ShareManagerProps {
  showAllShares?: boolean
  featureEnabled?: boolean
  initialReviewFilter?: ShareReviewFilter | string | null
  pathFilter?: string | null
  onClearPathFilter?: () => void
}

interface ShareReviewSummaryMetric {
  key: Exclude<ShareReviewFilter, 'all'>
  label: string
  value: number
  description: string
  color: 'default' | 'warning' | 'danger' | 'primary'
}

const SHARE_REVIEW_ACTIVITY_LIMIT = 100
const SHARE_REVIEW_DISPOSITION_DETAIL_LIMIT = 10

const shareRiskReasonMessages: Record<string, string> = {
  root_folder: '根目录分享会公开整个文件空间。',
  broad_folder: '顶层文件夹分享可能覆盖较多内容。',
  no_password: '未设置密码，持有链接的人可直接访问。',
  no_expiration: '未设置过期时间，链接会长期有效。',
  expiring_soon: '分享即将到期，请确认是否需要延长或关闭。',
  unlimited_access: '未设置访问次数上限。',
  unused_enabled: '该分享长期未被访问但仍处于启用状态。',
  stale_enabled: '该分享最近访问时间较久，请确认是否仍需保留。',
}

function getShareRiskFallbackReasonMessage(level: ShareRiskReason['level']): string {
  switch (level) {
    case 'high':
      return '存在高风险分享配置，请检查分享范围和访问限制。'
    case 'medium':
      return '存在需要关注的分享配置，请检查分享范围和访问限制。'
    case 'low':
      return '存在低风险分享配置，请检查分享设置。'
    default:
      return '存在未分类的分享风险，请检查分享设置。'
  }
}

function getShareRiskReasonMessage(reason: ShareRiskReason): string {
  return shareRiskReasonMessages[reason.code] ?? getShareRiskFallbackReasonMessage(reason.level)
}

function getShareActivityReviewRoute(path?: string): string {
  const params = new URLSearchParams({ action_group: 'share' })
  if (path) {
    params.set('path', path)
  }
  return `/activity?${params.toString()}`
}

function normalizeSharePathFilter(value: string | null | undefined): string {
  const trimmed = value?.trim() ?? ''
  if (!trimmed) {
    return ''
  }

  try {
    return normalizePath(trimmed)
  } catch {
    return ''
  }
}

function shareMatchesPathFilter(share: Share, pathFilter: string): boolean {
  if (!pathFilter) {
    return true
  }

  let sharePath: string
  try {
    sharePath = normalizePath(share.path)
  } catch {
    return false
  }

  if (pathFilter === '/' || sharePath === '/') {
    return true
  }

  return sharePath === pathFilter
    || sharePath.startsWith(`${pathFilter}/`)
    || pathFilter.startsWith(`${sharePath}/`)
}

function getShareReviewStatus(metrics: ShareReviewSummaryMetric[]): {
  label: string
  description: string
  color: 'success' | 'warning' | 'danger'
} {
  const highRiskCount = metrics.find(metric => metric.key === 'review')?.value ?? 0
  const broadCount = metrics.find(metric => metric.key === 'broad')?.value ?? 0
  const passwordlessCount = metrics.find(metric => metric.key === 'passwordless')?.value ?? 0
  const expiringCount = metrics.find(metric => metric.key === 'expiring')?.value ?? 0
  const staleCount = metrics.find(metric => metric.key === 'stale')?.value ?? 0

  if (highRiskCount > 0 || broadCount > 0 || passwordlessCount > 0) {
    return {
      label: '需处理',
      description: '优先处理无密码、覆盖范围较大或其他高风险分享。',
      color: 'danger',
    }
  }

  if (expiringCount > 0 || staleCount > 0) {
    return {
      label: '需确认',
      description: '存在即将到期或长期未访问的分享，建议确认是否保留。',
      color: 'warning',
    }
  }

  return {
    label: '状态正常',
    description: '当前没有需要复核的启用分享。',
    color: 'success',
  }
}

function getUniqueShareReviewValues(values: Array<string | undefined>): string[] {
  const seen = new Set<string>()
  const result: string[] = []
  for (const value of values) {
    const trimmed = value?.trim() ?? ''
    if (!trimmed || seen.has(trimmed)) {
      continue
    }
    seen.add(trimmed)
    result.push(trimmed)
  }
  return result
}

function getShareReviewActivityActionCounts(entries: ActivityEntry[]): ActivityActionCountMap {
  return entries.reduce<ActivityActionCountMap>((counts, entry) => {
    if (entry.action === 'share' || entry.action === 'unshare') {
      counts[entry.action] = (counts[entry.action] ?? 0) + 1
    }
    return counts
  }, {})
}

function formatShareReviewAccessSummary(share: Share): string {
  const passwordLabel = share.has_password ? '密码保护' : '无密码'
  const maxAccess = share.max_access && share.max_access > 0 ? `${share.max_access}` : '不限'
  return `${passwordLabel} · 访问 ${share.access_count}/${maxAccess}`
}

function getShareReviewReasonSummary(share: Share): string {
  const reasons = share.risk?.reasons
    ?.filter(reason => !reason.resolved)
    .map(reason => reason.message.trim() || getShareRiskReasonMessage(reason))
    .filter(Boolean) ?? []
  return reasons.length > 0 ? reasons.join('；') : '无'
}

function getShareReviewSuggestedAction(share: Share): string {
  if (!share.enabled) {
    return '确认是否仍需保留；不再使用时可删除。'
  }
  if (share.risk?.level === 'high') {
    return '停用或补齐密码、有效期和访问次数限制。'
  }
  if (shareHasRiskCode(share, 'expiring_soon')) {
    return '确认延期或关闭。'
  }
  if (shareHasRiskCode(share, 'unused_enabled') || shareHasRiskCode(share, 'stale_enabled')) {
    return '确认是否仍需保留；不再使用时停用。'
  }
  if (isRiskyShare(share)) {
    return '复核分享范围和访问限制。'
  }
  return '无需处理。'
}

function getShareReviewDispositionDetails(shares: Share[]): ActivityReviewShareDispositionDetail[] {
  return [...shares]
    .filter(isRiskyShare)
    .sort((left, right) => {
      const leftPriority = left.risk?.level === 'high' ? 0 : left.risk?.level === 'medium' ? 1 : 2
      const rightPriority = right.risk?.level === 'high' ? 0 : right.risk?.level === 'medium' ? 1 : 2
      return leftPriority - rightPriority || left.path.localeCompare(right.path)
    })
    .slice(0, SHARE_REVIEW_DISPOSITION_DETAIL_LIMIT)
    .map((share) => ({
      path: normalizePath(share.path),
      type: share.type,
      enabled: share.enabled,
      risk_level: share.risk?.level ?? 'none',
      reason_summary: getShareReviewReasonSummary(share),
      suggested_action: getShareReviewSuggestedAction(share),
      access_summary: formatShareReviewAccessSummary(share),
      expires_at: formatExpiration(share.expires_at),
    }))
}

function getShareReviewRecordNote(summary: ShareReviewSummary): string {
  return [
    `分享复核摘要：需复核 ${summary.reviewCount} 个`,
    `需处理 ${summary.highRiskCount} 个`,
    `无密码 ${summary.passwordlessCount} 个`,
    `覆盖较大 ${summary.broadCount} 个`,
    `即将到期 ${summary.expiringSoonCount} 个`,
    `长期未访问 ${summary.staleCount} 个。`,
  ].join('，')
}

function getShareReviewRecordFilterSummary(pathFilter: string, visibleShareCount: number, totalShareCount: number): string {
  const filters = ['审计分组 分享相关']
  if (pathFilter) {
    filters.push(`路径 ${pathFilter}`)
  }
  filters.push(`当前分享 ${visibleShareCount}/${totalShareCount}`)
  return filters.join(' · ')
}

function buildShareReviewRecordInput({
  entries,
  totalEntries,
  summary,
  visibleShareCount,
  totalShareCount,
  pathFilter,
  shares,
}: {
  entries: ActivityEntry[]
  totalEntries: number
  summary: ShareReviewSummary
  visibleShareCount: number
  totalShareCount: number
  pathFilter: string
  shares: Share[]
}): ActivityReviewRecordCreateInput {
  const paths = getUniqueShareReviewValues(entries.map((entry) => entry.path))
  const users = getUniqueShareReviewValues(entries.map((entry) => entry.user))
  return {
    note: getShareReviewRecordNote(summary),
    scope_label: pathFilter ? `分享路径 ${pathFilter}` : '分享管理',
    filter_summary: getShareReviewRecordFilterSummary(pathFilter, visibleShareCount, totalShareCount),
    disposition_status: summary.reviewCount > 0 ? 'needs_follow_up' : 'documented',
    action_counts: getShareReviewActivityActionCounts(entries),
    review_count: entries.length,
    total_count: totalEntries,
    path_count: paths.length,
    user_count: users.length,
    path_samples: paths.slice(0, 10),
    user_samples: users.slice(0, 10),
    share_disposition_details: getShareReviewDispositionDetails(shares),
    activity_entry_ids: entries.map((entry) => entry.id),
  }
}

function shareActivityMatchesAnyPath(entry: ActivityEntry, paths: Set<string>): boolean {
  const path = entry.path?.trim()
  if (!path) {
    return false
  }
  try {
    return paths.has(normalizePath(path))
  } catch {
    return false
  }
}

function getShareDisableExecutionDispositionDetails(shares: Share[]): ActivityReviewShareDispositionDetail[] {
  return shares
    .slice(0, SHARE_REVIEW_DISPOSITION_DETAIL_LIMIT)
    .map((share) => ({
      path: normalizePath(share.path),
      type: share.type,
      enabled: false,
      risk_level: share.risk?.level ?? 'high',
      reason_summary: getShareReviewReasonSummary(share),
      suggested_action: '已停用高风险分享；继续复核外部引用和访问入口。',
      access_summary: formatShareReviewAccessSummary(share),
      expires_at: formatExpiration(share.expires_at),
    }))
}

function buildShareDisableExecutionRecordInput({
  entries,
  totalEntries,
  disabledShares,
  pathFilter,
}: {
  entries: ActivityEntry[]
  totalEntries: number
  disabledShares: Share[]
  pathFilter: string
}): ActivityReviewRecordCreateInput {
  const paths = getUniqueShareReviewValues(disabledShares.map((share) => share.path))
  const users = getUniqueShareReviewValues(entries.map((entry) => entry.user))
  return {
    note: `分享执行结果：已停用 ${disabledShares.length} 个需处理分享；已关联 ${entries.length} 条分享活动。`,
    scope_label: pathFilter ? `分享路径 ${pathFilter}` : '分享管理',
    filter_summary: `${getShareReviewRecordFilterSummary(pathFilter, disabledShares.length, disabledShares.length)} · 执行结果 停用需处理分享`,
    disposition_status: 'disabled',
    action_counts: getShareReviewActivityActionCounts(entries),
    review_count: entries.length,
    total_count: Math.max(totalEntries, entries.length),
    path_count: paths.length,
    user_count: users.length,
    path_samples: paths.slice(0, 10),
    user_samples: users.slice(0, 10),
    share_disposition_details: getShareDisableExecutionDispositionDetails(disabledShares),
    activity_entry_ids: entries.map((entry) => entry.id),
  }
}

export function ShareManager({
  showAllShares = false,
  featureEnabled = true,
  initialReviewFilter = 'all',
  pathFilter = '',
  onClearPathFilter,
}: ShareManagerProps) {
  const navigate = useNavigate()
  const [shares, setShares] = useState<Share[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [loadError, setLoadError] = useState<unknown | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Share | null>(null)
  const [isDeleting, setIsDeleting] = useState(false)
  const [reviewFilter, setReviewFilter] = useState<ShareReviewFilter>(() => normalizeShareReviewFilter(initialReviewFilter))
  const [isDisablingHighRisk, setIsDisablingHighRisk] = useState(false)
  const [isRecordingShareReview, setIsRecordingShareReview] = useState(false)
  const sharesRef = useRef<Share[]>([])
  const loadRequestRef = useRef(0)
  const loadAbortControllerRef = useRef<AbortController | null>(null)
  const toggleAbortControllersRef = useRef(new Map<string, AbortController>())
  const disableHighRiskAbortControllerRef = useRef<AbortController | null>(null)
  const deleteAbortControllerRef = useRef<AbortController | null>(null)
  const recordReviewAbortControllerRef = useRef<AbortController | null>(null)

  useEffect(() => () => {
    loadRequestRef.current += 1
    loadAbortControllerRef.current?.abort()
    loadAbortControllerRef.current = null
    toggleAbortControllersRef.current.forEach((controller) => controller.abort())
    toggleAbortControllersRef.current.clear()
    disableHighRiskAbortControllerRef.current?.abort()
    disableHighRiskAbortControllerRef.current = null
    deleteAbortControllerRef.current?.abort()
    deleteAbortControllerRef.current = null
    recordReviewAbortControllerRef.current?.abort()
    recordReviewAbortControllerRef.current = null
  }, [])

  useEffect(() => {
    sharesRef.current = shares
  }, [shares])

  const loadShares = useCallback(async () => {
    const requestId = loadRequestRef.current + 1
    loadRequestRef.current = requestId
    loadAbortControllerRef.current?.abort()
    const controller = new AbortController()
    loadAbortControllerRef.current = controller
    setIsLoading(true)
    setLoadError(null)
    try {
      const data = await listShares(showAllShares, { signal: controller.signal })
      if (requestId !== loadRequestRef.current) {
        return
      }
      setShares(data)
    } catch (err) {
      if (controller.signal.aborted) {
        return
      }
      if (requestId !== loadRequestRef.current) {
        return
      }
      const featureState = getShareFeatureState(err)
      setLoadError(err)
      if (featureState !== null) {
        setShares([])
        return
      }

      if (sharesRef.current.length > 0) {
        addToast(getShareLoadErrorToast(err))
      }
    } finally {
      if (loadAbortControllerRef.current === controller) {
        loadAbortControllerRef.current = null
      }
      if (requestId === loadRequestRef.current) {
        setIsLoading(false)
      }
    }
  }, [showAllShares])

  const loadSharesRef = useRef(loadShares)

  useEffect(() => {
    loadSharesRef.current = loadShares
  }, [loadShares])

  useEffect(() => {
    if (!featureEnabled) {
      loadRequestRef.current += 1
      loadAbortControllerRef.current?.abort()
      loadAbortControllerRef.current = null
      toggleAbortControllersRef.current.forEach((controller) => controller.abort())
      toggleAbortControllersRef.current.clear()
      disableHighRiskAbortControllerRef.current?.abort()
      disableHighRiskAbortControllerRef.current = null
      deleteAbortControllerRef.current?.abort()
      deleteAbortControllerRef.current = null
      recordReviewAbortControllerRef.current?.abort()
      recordReviewAbortControllerRef.current = null
      let cancelled = false
      queueMicrotask(() => {
        if (cancelled) return
        setIsLoading(false)
        setLoadError(null)
        setShares([])
      })
      sharesRef.current = []
      return () => {
        cancelled = true
      }
    }

    void loadSharesRef.current()
  }, [featureEnabled])

  const normalizedPathFilter = useMemo(() => normalizeSharePathFilter(pathFilter), [pathFilter])
  const pathFilteredShares = useMemo(() => (
    normalizedPathFilter ? shares.filter((share) => shareMatchesPathFilter(share, normalizedPathFilter)) : shares
  ), [normalizedPathFilter, shares])
  const riskyShares = useMemo(() => pathFilteredShares.filter(isRiskyShare), [pathFilteredShares])
  const highRiskShares = useMemo(() => pathFilteredShares.filter(isHighRiskShare), [pathFilteredShares])
  const expiringSoonShares = useMemo(() => pathFilteredShares.filter(share => shareHasRiskCode(share, 'expiring_soon')), [pathFilteredShares])
  const passwordlessShares = useMemo(() => pathFilteredShares.filter(share => shareHasRiskCode(share, 'no_password')), [pathFilteredShares])
  const broadFolderShares = useMemo(() => pathFilteredShares.filter(share => (
    shareHasRiskCode(share, 'root_folder') || shareHasRiskCode(share, 'broad_folder')
  )), [pathFilteredShares])
  const staleShares = useMemo(() => pathFilteredShares.filter(share => (
    shareHasRiskCode(share, 'unused_enabled') || shareHasRiskCode(share, 'stale_enabled')
  )), [pathFilteredShares])
  const visibleShares = useMemo(() => {
    switch (reviewFilter) {
    case 'review':
      return riskyShares
    case 'expiring':
      return expiringSoonShares
    case 'passwordless':
      return passwordlessShares
    case 'broad':
      return broadFolderShares
    case 'stale':
      return staleShares
    default:
      return pathFilteredShares
    }
  }, [broadFolderShares, expiringSoonShares, passwordlessShares, pathFilteredShares, reviewFilter, riskyShares, staleShares])
  const reviewSummaryMetrics = useMemo<ShareReviewSummaryMetric[]>(() => ([
    {
      key: 'review',
      label: '需复核',
      value: riskyShares.length,
      description: '存在未解决风险的启用分享',
      color: riskyShares.length > 0 ? 'warning' : 'default',
    },
    {
      key: 'passwordless',
      label: '无密码',
      value: passwordlessShares.length,
      description: '持有链接即可访问',
      color: passwordlessShares.length > 0 ? 'danger' : 'default',
    },
    {
      key: 'broad',
      label: '覆盖较大',
      value: broadFolderShares.length,
      description: '根目录或顶层目录分享',
      color: broadFolderShares.length > 0 ? 'danger' : 'default',
    },
    {
      key: 'expiring',
      label: '即将到期',
      value: expiringSoonShares.length,
      description: '需要确认延期或关闭',
      color: expiringSoonShares.length > 0 ? 'primary' : 'default',
    },
    {
      key: 'stale',
      label: '长期未访问',
      value: staleShares.length,
      description: '建议确认是否仍需保留',
      color: staleShares.length > 0 ? 'warning' : 'default',
    },
  ]), [broadFolderShares.length, expiringSoonShares.length, passwordlessShares.length, riskyShares.length, staleShares.length])
  const reviewStatus = useMemo(() => getShareReviewStatus(reviewSummaryMetrics), [reviewSummaryMetrics])
  const reviewSummary = useMemo(() => summarizeShareReview(pathFilteredShares), [pathFilteredShares])

  if (!featureEnabled) {
    return (
      <EmptyState
        icon={Link2}
        title="分享功能已关闭"
        description="当前服务已关闭分享功能。启用后可在此管理已创建的分享链接。"
        className="py-12"
      />
    )
  }

  const loadFeatureState = getShareFeatureState(loadError)

  const handleCopy = async (share: Share) => {
    try {
      await copyShareUrl(share)
      addToast({ title: '链接已复制', color: 'success' })
    } catch {
      addToast({ title: '复制失败', color: 'danger' })
    }
  }

  const handleCopyReviewReport = async () => {
    try {
      await copyTextToClipboard(formatShareReviewReport(pathFilteredShares, reviewSummary, {
        pathFilter: normalizedPathFilter || undefined,
      }))
      addToast({ title: '分享复核摘要已复制', color: 'success' })
    } catch {
      addToast({
        title: '复制分享复核摘要失败',
        description: '请检查浏览器剪贴板权限。',
        color: 'danger',
      })
    }
  }

  const handleRecordShareReview = async () => {
    if (isRecordingShareReview) {
      return
    }

    recordReviewAbortControllerRef.current?.abort()
    const controller = new AbortController()
    recordReviewAbortControllerRef.current = controller
    setIsRecordingShareReview(true)
    try {
      const result = await listActivity({
        actionGroup: 'share',
        path: normalizedPathFilter || undefined,
        limit: SHARE_REVIEW_ACTIVITY_LIMIT,
        offset: 0,
        signal: controller.signal,
      })
      if (controller.signal.aborted) {
        return
      }

      const reviewEntries = result.items.filter((entry) => entry.action === 'share' || entry.action === 'unshare')
      if (reviewEntries.length === 0) {
        addToast({
          title: '没有可关联的分享活动',
          description: '活动日志中没有找到当前范围的分享或取消分享记录。',
          color: 'warning',
        })
        return
      }

      await createActivityReviewRecord(buildShareReviewRecordInput({
        entries: reviewEntries,
        totalEntries: result.total,
        summary: reviewSummary,
        visibleShareCount: pathFilteredShares.length,
        totalShareCount: shares.length,
        pathFilter: normalizedPathFilter,
        shares: pathFilteredShares,
      }), { signal: controller.signal })
      if (controller.signal.aborted) {
        return
      }

      addToast({
        title: '分享复核已记录',
        description: result.total > reviewEntries.length
          ? `已关联最近 ${reviewEntries.length} 条分享活动；当前筛选共有 ${result.total} 条分享活动。`
          : `已关联 ${reviewEntries.length} 条分享活动。`,
        color: 'success',
      })
    } catch (err) {
      if (controller.signal.aborted || isAbortError(err)) {
        return
      }
      addToast({
        title: '记录分享复核失败',
        description: getUserFacingErrorDescription(err),
        color: 'danger',
      })
    } finally {
      if (recordReviewAbortControllerRef.current === controller) {
        recordReviewAbortControllerRef.current = null
      }
      if (!controller.signal.aborted) {
        setIsRecordingShareReview(false)
      }
    }
  }

  const handleToggle = async (share: Share) => {
    toggleAbortControllersRef.current.get(share.id)?.abort()
    const controller = new AbortController()
    toggleAbortControllersRef.current.set(share.id, controller)
    try {
      const result = await updateShare(share.id, { enabled: !share.enabled }, { signal: controller.signal })
      if (controller.signal.aborted) {
        return
      }

      setShares(prev => prev.map(s => (
        s.id === share.id ? result : s
      )))
      addToast(getShareToggleSuccessToast(result.enabled, result.warning))
    } catch (err) {
      if (controller.signal.aborted || isAbortError(err)) {
        return
      }

      if (err instanceof ShareError && err.isNotFound) {
        setShares(prev => prev.filter(s => s.id !== share.id))
        addToast(getMissingShareToast())
        return
      }

      addToast(getShareActionErrorToast(err, {
        unavailable: '分享操作暂不可用',
        failure: '操作失败',
      }))
    } finally {
      if (toggleAbortControllersRef.current.get(share.id) === controller) {
        toggleAbortControllersRef.current.delete(share.id)
      }
    }
  }

  const handleDisableHighRisk = async () => {
    const targets = highRiskShares
    if (targets.length === 0 || isDisablingHighRisk) {
      return
    }

    setIsDisablingHighRisk(true)
    disableHighRiskAbortControllerRef.current?.abort()
    const controller = new AbortController()
    disableHighRiskAbortControllerRef.current = controller
    try {
      const results = await Promise.allSettled(
        targets.map(target => updateShare(target.id, { enabled: false }, { signal: controller.signal }))
      )
      if (controller.signal.aborted) {
        return
      }

      const disabledIds = new Set<string>()
      let firstFailure: unknown | null = null
      let warningCount = 0

      results.forEach((result, index) => {
        if (result.status === 'fulfilled') {
          disabledIds.add(targets[index].id)
          if (result.value.warning) {
            warningCount++
          }
          return
        }
        if (firstFailure === null) {
          firstFailure = result.reason
        }
      })

      if (disabledIds.size > 0) {
        const disabledTargets = targets.filter(target => disabledIds.has(target.id))
        setShares(prev => prev.map(s => (
          disabledIds.has(s.id)
            ? { ...s, enabled: false, risk: { level: 'none' } }
            : s
        )))
        addToast({
          title: warningCount > 0
            ? `已停用 ${disabledIds.size} 个需处理分享，但存在警告`
            : `已停用 ${disabledIds.size} 个需处理分享`,
          color: warningCount > 0 ? 'warning' : 'success',
        })
        try {
          const disabledPaths = new Set(disabledTargets.map(target => normalizePath(target.path)))
          const activityResult = await listActivity({
            actionGroup: 'share',
            path: normalizedPathFilter || undefined,
            limit: SHARE_REVIEW_ACTIVITY_LIMIT,
            offset: 0,
            signal: controller.signal,
          })
          if (controller.signal.aborted) {
            return
          }
          const executionEntries = activityResult.items.filter((entry) => (
            entry.action === 'unshare' && shareActivityMatchesAnyPath(entry, disabledPaths)
          ))
          if (executionEntries.length > 0) {
            await createActivityReviewRecord(buildShareDisableExecutionRecordInput({
              entries: executionEntries,
              totalEntries: activityResult.total,
              disabledShares: disabledTargets,
              pathFilter: normalizedPathFilter,
            }), { signal: controller.signal })
            if (!controller.signal.aborted) {
              addToast({ title: '分享停用结果已记录', color: 'success' })
            }
          }
        } catch (err) {
          if (!controller.signal.aborted && !isAbortError(err)) {
            addToast({
              title: '分享已停用，复核记录写入失败',
              description: getUserFacingErrorDescription(err),
              color: 'warning',
            })
          }
        }
      }

      if (firstFailure !== null) {
        addToast(getShareActionErrorToast(firstFailure, {
          unavailable: '分享操作暂不可用',
          failure: '部分分享停用失败',
        }))
      }
    } finally {
      if (disableHighRiskAbortControllerRef.current === controller) {
        disableHighRiskAbortControllerRef.current = null
      }
      if (!controller.signal.aborted) {
        setIsDisablingHighRisk(false)
      }
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    const target = deleteTarget
    deleteAbortControllerRef.current?.abort()
    const controller = new AbortController()
    deleteAbortControllerRef.current = controller
    setIsDeleting(true)
    try {
      const result = await deleteShare(target.id, { signal: controller.signal })
      if (controller.signal.aborted) {
        return
      }

      setShares(prev => prev.filter(s => s.id !== target.id))
      addToast(getShareDeleteSuccessToast(result))
      setDeleteTarget(current => (current?.id === target.id ? null : current))
    } catch (err) {
      if (controller.signal.aborted || isAbortError(err)) {
        return
      }

      if (err instanceof ShareError && err.isNotFound) {
        setShares(prev => prev.filter(s => s.id !== target.id))
        addToast(getMissingShareToast())
        setDeleteTarget(current => (current?.id === target.id ? null : current))
        return
      }

      addToast(getShareActionErrorToast(err, {
        unavailable: '删除分享暂不可用',
        failure: '删除失败',
      }))
    } finally {
      if (deleteAbortControllerRef.current === controller) {
        deleteAbortControllerRef.current = null
      }
      if (!controller.signal.aborted) {
        setIsDeleting(false)
      }
    }
  }

  const handleCloseDeleteModal = () => {
    if (isDeleting) {
      return
    }
    setDeleteTarget(null)
  }

  const handleReviewActivity = (share: Share) => {
    navigate(getShareActivityReviewRoute(share.path))
  }

  const handleReviewHistory = () => {
    navigate(getShareActivityReviewRoute(normalizedPathFilter || undefined))
  }

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-12">
        <div className="text-center">
          <div className="w-10 h-10 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-3" />
          <p className="text-default-500 text-sm">加载分享列表...</p>
        </div>
      </div>
    )
  }

  if (loadError && shares.length === 0) {
    if (loadFeatureState === 'disabled') {
      return (
        <EmptyState
          icon={Link2}
          title="分享功能已关闭"
          description="当前服务已关闭分享功能。启用后可在此管理已创建的分享链接。"
          className="py-12"
        />
      )
    }

    if (loadFeatureState === 'unavailable') {
      return (
        <EmptyState
          icon={AlertCircle}
          title="分享功能暂不可用"
          description="分享服务当前不可用，请检查设备状态或稍后重试。"
          action={
            <Button variant="bordered" className="rounded-lg" onPress={() => loadShares()}>
              重新加载
            </Button>
          }
          className="py-12"
        />
      )
    }

    return (
      <EmptyState
        icon={AlertCircle}
        title="加载分享列表失败"
        description={getUserFacingErrorDescription(loadError, GENERIC_LOAD_ERROR_DESCRIPTION)}
        action={
          <Button variant="bordered" className="rounded-lg" onPress={() => loadShares()}>
            重新加载
          </Button>
        }
        className="py-12"
      />
    )
  }

  if (shares.length === 0) {
    return (
      <EmptyState
        icon={Link2}
        title="暂无分享"
        description="在文件浏览器中选择文件或文件夹创建分享链接"
        className="py-12"
      />
    )
  }

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <h2 className="text-lg font-semibold text-foreground">
            我的分享 ({normalizedPathFilter ? `${pathFilteredShares.length} / ${shares.length}` : shares.length})
          </h2>
          {riskyShares.length > 0 && (
            <Chip size="sm" color="warning" variant="flat">
              风险 {riskyShares.length}
            </Chip>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant={reviewFilter === 'all' ? 'solid' : 'flat'}
            size="sm"
            onPress={() => setReviewFilter('all')}
            className="rounded-lg"
          >
            全部
          </Button>
          <Button
            variant={reviewFilter === 'review' ? 'solid' : 'flat'}
            size="sm"
            onPress={() => setReviewFilter('review')}
            className="rounded-lg"
            isDisabled={riskyShares.length === 0}
          >
            需复核 ({riskyShares.length})
          </Button>
          <Button
            variant={reviewFilter === 'expiring' ? 'solid' : 'flat'}
            size="sm"
            onPress={() => setReviewFilter('expiring')}
            className="rounded-lg"
            isDisabled={expiringSoonShares.length === 0}
          >
            即将到期 ({expiringSoonShares.length})
          </Button>
          <Button
            variant={reviewFilter === 'passwordless' ? 'solid' : 'flat'}
            size="sm"
            onPress={() => setReviewFilter('passwordless')}
            className="rounded-lg"
            isDisabled={passwordlessShares.length === 0}
          >
            无密码 ({passwordlessShares.length})
          </Button>
          <Button
            variant={reviewFilter === 'broad' ? 'solid' : 'flat'}
            size="sm"
            onPress={() => setReviewFilter('broad')}
            className="rounded-lg"
            isDisabled={broadFolderShares.length === 0}
          >
            覆盖较大 ({broadFolderShares.length})
          </Button>
          <Button
            variant={reviewFilter === 'stale' ? 'solid' : 'flat'}
            size="sm"
            onPress={() => setReviewFilter('stale')}
            className="rounded-lg"
            isDisabled={staleShares.length === 0}
          >
            长期未访问 ({staleShares.length})
          </Button>
          <Button
            isIconOnly
            variant="flat"
            size="sm"
            onPress={loadShares}
            aria-label="刷新分享列表"
            className="rounded-lg"
          >
            <RefreshCw size={16} />
          </Button>
        </div>
      </div>

      {normalizedPathFilter && (
        <div
          role="region"
          aria-label="分享路径筛选"
          className="flex min-w-0 flex-col gap-2 rounded-lg border border-divider bg-content2/60 px-3 py-2 text-sm sm:flex-row sm:items-center sm:justify-between"
        >
          <div className="min-w-0 truncate text-default-600" title={normalizedPathFilter}>
            路径：{normalizedPathFilter}
          </div>
          {onClearPathFilter && (
            <Button
              size="sm"
              variant="light"
              className="h-8 rounded-lg px-2 text-default-500"
              startContent={<X size={14} />}
              onPress={onClearPathFilter}
            >
              清除路径筛选
            </Button>
          )}
        </div>
      )}

      <div
        role="region"
        aria-label="分享复核摘要"
        className="rounded-lg border border-divider bg-content1 px-4 py-4"
      >
        <div className="flex min-w-0 flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
          <div className="flex min-w-0 items-start gap-3">
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-warning/10 text-warning">
              <ShieldAlert size={18} />
            </div>
            <div className="min-w-0">
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <div className="text-sm font-semibold text-foreground">分享复核摘要</div>
                <Chip size="sm" color={reviewStatus.color} variant="flat">
                  {reviewStatus.label}
                </Chip>
              </div>
              <div className="mt-1 text-xs text-default-500">
                {reviewStatus.description}
              </div>
            </div>
          </div>
          <div className="flex flex-wrap gap-2 self-start lg:self-center">
            <Button
              variant="flat"
              size="sm"
              startContent={<Activity size={16} />}
              onPress={handleReviewHistory}
              className="rounded-lg"
            >
              查看复核历史
            </Button>
            <Button
              variant="flat"
              size="sm"
              startContent={<Copy size={16} />}
              onPress={handleCopyReviewReport}
              className="rounded-lg"
            >
              复制摘要
            </Button>
            <Button
              variant="flat"
              size="sm"
              startContent={<ShieldAlert size={16} />}
              onPress={handleRecordShareReview}
              isLoading={isRecordingShareReview}
              className="rounded-lg"
            >
              记录复核
            </Button>
            {highRiskShares.length > 0 && (
              <Button
                color="danger"
                variant="flat"
                size="sm"
                isLoading={isDisablingHighRisk}
                onPress={handleDisableHighRisk}
                className="rounded-lg"
              >
                停用需处理 ({highRiskShares.length})
              </Button>
            )}
          </div>
        </div>

        <div className="mt-4 grid gap-2 sm:grid-cols-2 xl:grid-cols-5">
          {reviewSummaryMetrics.map(metric => (
            <button
              key={metric.key}
              type="button"
              onClick={() => metric.value > 0 && setReviewFilter(metric.key)}
              disabled={metric.value === 0}
              aria-label={`筛选${metric.label}分享 ${metric.value}`}
              className={[
                'min-h-24 rounded-lg border px-3 py-3 text-left transition',
                reviewFilter === metric.key ? 'border-primary bg-primary/10' : 'border-divider bg-content2',
                metric.value > 0 ? 'hover:border-primary/50 hover:bg-content3' : 'cursor-default opacity-70',
              ].join(' ')}
            >
              <div className="flex items-center justify-between gap-2">
                <span className="text-xs text-default-500">{metric.label}</span>
                <Chip size="sm" color={metric.color} variant="flat">
                  {metric.value}
                </Chip>
              </div>
              <div className="mt-2 text-lg font-semibold text-foreground">
                {metric.value} 个
              </div>
              <div className="mt-1 text-xs leading-5 text-default-500">
                {metric.description}
              </div>
            </button>
          ))}
        </div>
      </div>

      {/* Share list */}
      <div className="space-y-3">
        {visibleShares.length === 0 ? (
          <EmptyState
            icon={AlertCircle}
            title="暂无符合条件的分享"
            description="当前筛选条件下没有需要处理的分享链接"
            className="py-10"
          />
        ) : (
          visibleShares.map((share) => (
            <ShareItem
              key={share.id}
              share={share}
              onCopy={() => handleCopy(share)}
              onReviewActivity={() => handleReviewActivity(share)}
              onToggle={() => handleToggle(share)}
              onDelete={() => setDeleteTarget(share)}
            />
          ))
        )}
      </div>

      {(riskyShares.length > 0 || expiringSoonShares.length > 0 || staleShares.length > 0) && (
        <div className="rounded-lg border border-divider bg-content1 px-4 py-3 text-sm text-default-600">
          <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
            <span>复核：{riskyShares.length}</span>
            <span>即将到期：{expiringSoonShares.length}</span>
            <span>无密码：{passwordlessShares.length}</span>
            <span>覆盖较大：{broadFolderShares.length}</span>
            <span>长期未访问：{staleShares.length}</span>
          </div>
        </div>
      )}

      {/* Delete confirmation modal */}
      <Modal 
        isOpen={!!deleteTarget} 
        onClose={handleCloseDeleteModal}
        placement="center"
        size="md"
        classNames={{
          base: "bg-content1 border border-divider shadow-xl rounded-lg",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-lg bg-danger/10 text-danger flex items-center justify-center">
              <Trash2 size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">删除分享</h3>
              <p className="text-xs text-default-500 font-normal">已分享的链接将失效</p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4">
            <p className="text-default-600">确定要删除此分享链接吗？</p>
            {deleteTarget && (
              <div className="p-3 bg-content2 rounded-lg mt-3 border border-divider">
                <div className="text-xs text-default-500 mb-1">分享路径</div>
                <div className="font-medium truncate text-foreground flex items-center gap-2">
                  <FileIcon name={deleteTarget.path} isDir={deleteTarget.type === 'folder'} size={16} />
                  <span className="truncate">{deleteTarget.path}</span>
                </div>
              </div>
            )}
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button
              variant="flat"
              onPress={handleCloseDeleteModal}
              isDisabled={isDeleting}
              className="text-default-600 rounded-lg"
            >
              取消
            </Button>
            <Button 
              color="danger" 
              onPress={handleDelete}
              isLoading={isDeleting}
              className="rounded-lg"
            >
              删除
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  )
}

interface ShareItemProps {
  share: Share
  onCopy: () => void
  onReviewActivity: () => void
  onToggle: () => void
  onDelete: () => void
}

function ShareItem({ share, onCopy, onReviewActivity, onToggle, onDelete }: ShareItemProps) {
  const fileName = share.path.split('/').pop() || share.path
  const isExpired = share.expires_at ? new Date(share.expires_at) <= new Date() : false
  const riskPresentation = getRiskPresentation(share.risk?.level)
  const riskReasons = share.risk?.reasons?.filter(reason => !reason.resolved) ?? []

  return (
    <Card className="card-mnemonas">
      <CardBody className="p-4">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-start">
          {/* Icon */}
          <FileIcon
            name={fileName}
            isDir={share.type === 'folder'}
            size={28}
          />

          {/* Content */}
          <div className="min-w-0 flex-1">
            <div className="mb-1 flex min-w-0 flex-wrap items-center gap-2">
              <span className="font-medium text-foreground truncate">
                {fileName}
              </span>
              {!share.enabled && (
                <Chip size="sm" color="default" variant="flat">已禁用</Chip>
              )}
              {isExpired && (
                <Chip size="sm" color="danger" variant="flat">已过期</Chip>
              )}
              {riskPresentation && (
                <Chip size="sm" color={riskPresentation.color} variant="flat">
                  {riskPresentation.label}
                </Chip>
              )}
            </div>
            
            <div className="text-sm text-default-500 truncate mb-2">
              {share.path}
            </div>

            {/* Stats */}
            <div className="flex flex-wrap items-center gap-3 text-xs text-default-500">
              {share.has_password && (
                <div className="flex items-center gap-1">
                  <Lock size={12} />
                  <span>密码保护</span>
                </div>
              )}
              <div className="flex items-center gap-1">
                <Clock size={12} />
                <span>{formatExpiration(share.expires_at)}</span>
              </div>
              <div className="flex items-center gap-1">
                <Eye size={12} />
                <span>
                  {share.access_count} 次访问
                  {share.max_access && share.max_access > 0 && ` / ${share.max_access}`}
                </span>
              </div>
            </div>

            {riskReasons.length > 0 && (
              <div className="mt-3 rounded-lg border border-warning/20 bg-warning/10 px-3 py-2 text-xs text-default-700">
                <div className="mb-1 flex items-center gap-1 font-medium text-warning">
                  <AlertCircle size={12} />
                  <span>风险提醒</span>
                </div>
                <div className="space-y-1">
                  {riskReasons.slice(0, 3).map(reason => (
                    <div key={reason.code}>{getShareRiskReasonMessage(reason)}</div>
                  ))}
                </div>
              </div>
            )}
          </div>

          {/* Actions */}
          <div className="flex shrink-0 items-center gap-2 self-end sm:self-start">
            <Button
              isIconOnly
              variant="flat"
              size="sm"
              onPress={onCopy}
              aria-label={`${fileName} 复制分享链接`}
              className="rounded-lg"
            >
              <Copy size={16} />
            </Button>
            <Button
              isIconOnly
              variant="flat"
              size="sm"
              onPress={onReviewActivity}
              aria-label={`${fileName} 查看分享活动`}
              className="rounded-lg"
            >
              <Activity size={16} />
            </Button>
            
            <Dropdown>
              <DropdownTrigger>
                <Button isIconOnly variant="flat" size="sm" aria-label={`${fileName} 分享操作`} className="rounded-lg">
                  <MoreVertical size={16} />
                </Button>
              </DropdownTrigger>
              <DropdownMenu aria-label="分享操作">
                <DropdownItem 
                  key="copy" 
                  startContent={<Copy size={14} />}
                  onPress={onCopy}
                >
                  复制链接
                </DropdownItem>
                <DropdownItem
                  key="review-activity"
                  startContent={<Activity size={14} />}
                  onPress={onReviewActivity}
                >
                  查看分享活动
                </DropdownItem>
                <DropdownItem 
                  key="toggle"
                  startContent={share.enabled ? <ToggleLeft size={14} /> : <ToggleRight size={14} />}
                  onPress={onToggle}
                >
                  {share.enabled ? '禁用分享' : '启用分享'}
                </DropdownItem>
                <DropdownItem 
                  key="delete" 
                  className="text-danger"
                  color="danger"
                  startContent={<Trash2 size={14} />}
                  onPress={onDelete}
                >
                  删除分享
                </DropdownItem>
              </DropdownMenu>
            </Dropdown>
          </div>
        </div>
      </CardBody>
    </Card>
  )
}
