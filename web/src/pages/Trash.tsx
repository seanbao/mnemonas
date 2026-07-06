import { getInvalidHomeDirDescription, invalidHomeDirTitle, resolveUserHomeScope } from '@/lib/userScope'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Button,
  Checkbox,
  Modal,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalFooter,
  useDisclosure,
  addToast,
  Chip
} from '@heroui/react'
import {
  Trash2,
  RotateCcw,
  AlertTriangle,
  Clock,
  AlertCircle,
  Copy,
  X,
} from 'lucide-react'
import {
  listTrash,
  restoreFromTrash,
  deleteFromTrash,
  emptyTrash,
  ApiError,
  MAX_EMPTY_TRASH_IDS,
  type ActionResult,
  type EmptyTrashResult,
  type TrashItem,
  type TrashListResponse
} from '@/api/files'
import {
  createActivityReviewRecord,
  listActivity,
  type ActivityEntry,
  type ActivityReviewRecordCreateInput,
} from '@/api/activity'
import { FileIcon } from '@/components/ui/FileIcon'
import { EmptyState } from '@/components/ui/EmptyState'
import { formatBytes, cn, copyTextToClipboard, formatRelativeTime, normalizePath } from '@/lib/utils'
import { useBatchOperation, type BatchOperationResult } from '@/lib/useBatchOperation'
import { GENERIC_ACTION_ERROR_DESCRIPTION, GENERIC_LOAD_ERROR_DESCRIPTION, getUserFacingErrorDescription } from '@/lib/apiMessages'
import { PageHeader } from '@/components/ui/PageHeader'
import { useDestructiveDialogFocus } from '@/hooks/useDestructiveDialogFocus'
import { useCanWrite, useUser } from '@/stores/auth'
import {
  getPathConflictErrorToast,
  getQuotaExceededErrorToast,
  getSharedPathConflictErrorToast,
  getSharedQuotaExceededErrorToast,
} from '@/lib/fileActionErrors'

function daysUntilDelete(expiresAt: string): number {
  const expiryTime = new Date(expiresAt).getTime()
  return Math.max(0, Math.ceil((expiryTime - Date.now()) / (1000 * 60 * 60 * 24)))
}

function getAutoDeleteBadgeLabel(item: TrashItem, trashAutoCleanupEnabled: boolean | undefined): string | null {
  if (trashAutoCleanupEnabled === undefined) {
    return '自动清理设置未知；容量不足时仍可能提前清理'
  }

  if (!trashAutoCleanupEnabled) {
    return null
  }

  const daysLeft = daysUntilDelete(item.expiresAt)
  if (daysLeft > 7) {
    return null
  }
  if (daysLeft === 0) {
    return '已过期，等待清理'
  }

  return `${daysLeft} 天后到期`
}

function getExistingTrashCleanupDescription(trashAutoCleanupEnabled: boolean | undefined): string {
  if (trashAutoCleanupEnabled === undefined) {
    return '现有项目自动清理设置未知，容量不足时仍可能提前清理'
  }
  if (!trashAutoCleanupEnabled) {
    return '现有项目按到期时间自动清理未启用，容量不足时仍可能提前清理'
  }
  return '现有项目到期后由后台清理，容量不足时可能提前清理'
}

function getTrashPolicyLabel(
  retentionEnabled: boolean | undefined,
  retentionDays: number | undefined,
  trashAutoCleanupEnabled: boolean | undefined,
): string {
  const existingCleanupDescription = getExistingTrashCleanupDescription(trashAutoCleanupEnabled)
  if (retentionEnabled === undefined) {
    return `当前删除方式未知 · ${existingCleanupDescription}`
  }
  if (!retentionEnabled) {
    return `当前删除方式为永久删除，新删除项目不会进入回收站 · ${existingCleanupDescription}`
  }
  if (trashAutoCleanupEnabled === undefined) {
    return '新删除项目会进入回收站 · 自动清理设置未知 · 容量不足时可能提前清理'
  }
  if (!trashAutoCleanupEnabled) {
    return '新删除项目会进入回收站 · 按到期时间自动清理未启用 · 容量不足时仍可能提前清理'
  }
  if (retentionDays === undefined) {
    return '新删除项目会进入回收站，到期时间未知 · 容量不足时可能提前清理'
  }
  if (retentionDays <= 0) {
    return '新删除项目会进入回收站并立即到期 · 容量不足时可能提前清理'
  }
  return `新删除项目会进入回收站并在 ${retentionDays} 天后到期 · 容量不足时可能提前清理`
}

function getTrashEmptyStateDescription(retentionEnabled: boolean | undefined): string {
  if (retentionEnabled === undefined) {
    return '当前删除方式未知；新删除项目是否进入回收站暂不可确认'
  }
  if (!retentionEnabled) {
    return '当前删除方式为永久删除；新删除项目不会进入回收站'
  }
  return '新删除项目会进入回收站，并按当前策略保留'
}

const trashUnavailableDescription = '文件系统当前不可用，请稍后重试'
const missingTrashItemTitle = '回收站条目已不存在，已同步更新'
const missingTrashBatchDescription = '已同步移除 {count} 个不存在回收站项目'
const clipboardWriteFailureDescription = '请检查浏览器剪贴板权限。'
const TRASH_RESTORE_ACTIVITY_LIMIT = 100

type TrashBatchIntent = {
  items: TrashItem[]
  trashAutoCleanupEnabled: boolean | undefined
}

type TrashEmptyIntent = {
  items: Array<Pick<TrashItem, 'id' | 'name' | 'size'>>
  reconciliationRequired?: boolean
}

class EmptyTrashChunkExecutionError extends Error {
  readonly executionError: unknown
  readonly completedResult: EmptyTrashResult
  readonly uncertainIds: string[]
  readonly unattemptedIds: string[]
  readonly aborted: boolean

  constructor(
    executionError: unknown,
    completedResult: EmptyTrashResult,
    uncertainIds: string[],
    unattemptedIds: string[],
    aborted = false,
  ) {
    super(executionError instanceof Error ? executionError.message : 'empty trash chunk failed')
    this.name = 'EmptyTrashChunkExecutionError'
    this.executionError = executionError
    this.completedResult = completedResult
    this.uncertainIds = uncertainIds
    this.unattemptedIds = unattemptedIds
    this.aborted = aborted
  }
}

async function emptyConfirmedTrashItems(ids: string[], signal: AbortSignal): Promise<EmptyTrashResult> {
  const aggregate: EmptyTrashResult = {
    deleted: [],
    remaining: [],
    skipped: [],
    partial: false,
    warning: false,
    auditWarning: false,
  }
  const messages: string[] = []

  for (let offset = 0; offset < ids.length; offset += MAX_EMPTY_TRASH_IDS) {
    const chunkEnd = Math.min(offset + MAX_EMPTY_TRASH_IDS, ids.length)
    const chunkIds = ids.slice(offset, chunkEnd)
    let result: EmptyTrashResult
    try {
      result = await emptyTrash(chunkIds, { signal })
    } catch (error) {
      if (signal.aborted || isAbortError(error)) {
        if (aggregate.deleted.length > 0 || aggregate.skipped.length > 0) {
          throw new EmptyTrashChunkExecutionError(
            error,
            aggregate,
            chunkIds,
            ids.slice(chunkEnd),
            true,
          )
        }
        throw error
      }
      throw new EmptyTrashChunkExecutionError(
        error,
        aggregate,
        chunkIds,
        ids.slice(chunkEnd),
      )
    }
    aggregate.deleted.push(...result.deleted)
    aggregate.remaining.push(...result.remaining)
    aggregate.skipped.push(...result.skipped)
    aggregate.partial = aggregate.partial || result.partial
    aggregate.warning = aggregate.warning || result.warning
    aggregate.auditWarning = aggregate.auditWarning || result.auditWarning
    if (result.message && !messages.includes(result.message)) {
      messages.push(result.message)
    }
    if (result.remaining.length > 0) {
      aggregate.remaining.push(...ids.slice(chunkEnd))
      aggregate.partial = true
      break
    }
  }

  aggregate.partial = aggregate.partial || aggregate.remaining.length > 0 || aggregate.skipped.length > 0
  aggregate.message = messages.length > 0 ? messages.join('；') : undefined
  return aggregate
}

function getEmptyTrashSuccessToast(
  result: EmptyTrashResult,
  refreshFailed = false,
  reconciledAbsentCount = 0,
) {
  const descriptions: string[] = []
  if (result.remaining.length > 0) {
    descriptions.push(refreshFailed
      ? `${result.remaining.length} 项状态待核对`
      : `仍有 ${result.remaining.length} 项保留在回收站`)
  }
  if (result.skipped.length > 0) {
    descriptions.push(`已跳过 ${result.skipped.length} 项`)
  }
  if (reconciledAbsentCount > 0) {
    descriptions.push(`核对后另有 ${reconciledAbsentCount} 项已不在回收站`)
  }
  if (result.warning) {
    descriptions.push('删除过程报告了清理警告')
  }
  if (result.auditWarning) {
    descriptions.push('操作记录未保存')
  }
  if (refreshFailed) {
    descriptions.push('删除结果已返回，但列表刷新失败')
  }

  const hasWarning = result.partial || result.warning || result.auditWarning || refreshFailed || reconciledAbsentCount > 0
  return {
    title: `已永久删除已确认的 ${result.deleted.length} 项`,
    ...(descriptions.length > 0 ? { description: `${descriptions.join('；')}。` } : {}),
    color: hasWarning ? 'warning' as const : 'success' as const,
  }
}

function getEmptyTrashInterruptedToast(
  error: EmptyTrashChunkExecutionError,
  remainingCount: number,
  reconciledAbsentCount: number,
  refreshFailed: boolean,
) {
  const errorPresentation = getTrashActionErrorPresentation(error.executionError, {
    unavailable: '清空回收站暂不可用',
    failure: '清空失败',
  })
  const hasCompletedChunk = error.completedResult.deleted.length > 0
    || error.completedResult.skipped.length > 0
    || error.completedResult.remaining.length > 0
  const descriptions = [
    `${hasCompletedChunk ? '后续批次' : '清空请求'}未完成：${errorPresentation.description}`,
  ]
  if (error.completedResult.skipped.length > 0) {
    descriptions.push(`已跳过 ${error.completedResult.skipped.length} 项`)
  }
  if (reconciledAbsentCount > 0) {
    descriptions.push(`核对后另有 ${reconciledAbsentCount} 项已不在回收站`)
  }
  if (remainingCount > 0) {
    if (refreshFailed) {
      if (error.uncertainIds.length > 0) {
        descriptions.push(`${error.uncertainIds.length} 项请求结果未知`)
      }
      if (error.unattemptedIds.length > 0) {
        descriptions.push(`${error.unattemptedIds.length} 项尚未处理`)
      }
      descriptions.push('请先重新核对')
    } else {
      descriptions.push(`已核对 ${remainingCount} 项仍在回收站，可重试`)
    }
  }
  if (error.completedResult.warning) {
    descriptions.push('删除过程报告了清理警告')
  }
  if (error.completedResult.auditWarning) {
    descriptions.push('操作记录未保存')
  }
  if (refreshFailed) {
    descriptions.push('列表刷新失败')
  }

  const completedCount = error.completedResult.deleted.length
  const title = completedCount > 0
    ? `已永久删除已确认的 ${completedCount} 项`
    : reconciledAbsentCount > 0 && remainingCount === 0
      ? `已核对 ${reconciledAbsentCount} 项不再位于回收站`
      : errorPresentation.title
  return {
    title,
    description: `${descriptions.join('；')}。`,
    color: 'warning' as const,
  }
}

class MissingTrashItemSynchronizationError extends Error {
  constructor() {
    super(missingTrashItemTitle)
    this.name = 'MissingTrashItemSynchronizationError'
  }
}

type TrashBatchResultSummary = {
  succeeded: number
  missing: number
  failed: number
  missingItems: unknown[]
  failedItems: unknown[]
  failedErrors: unknown[]
}

function normalizeTrashPathFilter(value: string | null): string {
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

function trashItemMatchesPathFilter(item: TrashItem, pathFilter: string): boolean {
  if (!pathFilter) {
    return true
  }
  if (item.originalPath === pathFilter) {
    return true
  }
  const directoryPrefix = pathFilter.endsWith('/') ? pathFilter : `${pathFilter}/`
  return item.originalPath.startsWith(directoryPrefix)
}

function getUniqueTrashReviewValues(values: Array<string | undefined>): string[] {
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

function trashActivityMatchesAnyPath(entry: ActivityEntry, paths: Set<string>): boolean {
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

function getTrashRestoreMatchedItems(items: TrashItem[], entries: ActivityEntry[]): TrashItem[] {
  const entryPaths = new Set(entries.flatMap((entry) => {
    if (!entry.path) {
      return []
    }
    try {
      return [normalizePath(entry.path)]
    } catch {
      return []
    }
  }))
  return items.filter((item) => {
    try {
      return entryPaths.has(normalizePath(item.originalPath))
    } catch {
      return false
    }
  })
}

function buildTrashRestoreReviewRecordInput({
  entries,
  totalEntries,
  restoredItems,
  pathFilter,
}: {
  entries: ActivityEntry[]
  totalEntries: number
  restoredItems: TrashItem[]
  pathFilter: string
}): ActivityReviewRecordCreateInput {
  const paths = getUniqueTrashReviewValues(entries.map((entry) => entry.path))
  const users = getUniqueTrashReviewValues(entries.map((entry) => entry.user))
  const fileCount = restoredItems.filter((item) => !item.isDir).length
  const directoryCount = restoredItems.filter((item) => item.isDir).length
  const scopeLabel = pathFilter ? `回收站路径 ${pathFilter}` : '回收站'
  const filterSummary = pathFilter
    ? `审计分组 风险操作 · 路径 ${pathFilter} · 执行结果 回收站恢复`
    : '审计分组 风险操作 · 执行结果 回收站恢复'

  return {
    note: `回收站恢复执行结果：已恢复 ${restoredItems.length} 项（${directoryCount} 个目录，${fileCount} 个文件）；已关联 ${entries.length} 条恢复活动。`,
    scope_label: scopeLabel,
    filter_summary: filterSummary,
    disposition_status: 'restored',
    action_counts: { trash_restore: entries.length },
    review_count: entries.length,
    total_count: Math.max(totalEntries, entries.length),
    path_count: paths.length,
    user_count: users.length,
    path_samples: paths.slice(0, 10),
    user_samples: users.slice(0, 10),
    activity_entry_ids: entries.map((entry) => entry.id),
  }
}

function getMissingTrashItemResult(): ActionResult {
  return {
    warning: true,
    message: missingTrashItemTitle,
  }
}

function isMissingTrashItemResult(result: ActionResult): boolean {
  return result.warning === true && result.message === missingTrashItemTitle
}

async function classifyTrashBatchAction(operation: () => Promise<ActionResult>): Promise<ActionResult> {
  const result = await operation()
  if (isMissingTrashItemResult(result)) {
    throw new MissingTrashItemSynchronizationError()
  }
  return result
}

function getTrashBatchWarningMessage(result: ActionResult): string | undefined {
  return result.message === missingTrashItemTitle ? missingTrashItemTitle : undefined
}

function getTrashRestoreSuccessToast(result: ActionResult): {
  title: string
  color: 'success' | 'warning'
} {
  if (result.warning) {
    return {
      title: result.message === missingTrashItemTitle ? missingTrashItemTitle : '恢复完成，但存在警告',
      color: 'warning',
    }
  }

  return { title: '恢复成功', color: 'success' }
}

function getTrashDeleteSuccessToast(result: ActionResult): {
  title: string
  color: 'success' | 'warning'
} {
  if (result.warning) {
    return {
      title: result.message === missingTrashItemTitle ? missingTrashItemTitle : '已永久删除，但存在警告',
      color: 'warning',
    }
  }

  return { title: '已永久删除', color: 'success' }
}

function isAbortError(error: unknown): boolean {
  return typeof error === 'object'
    && error !== null
    && 'name' in error
    && (error as { name?: unknown }).name === 'AbortError'
}

function getTrashLoadErrorPresentation(error: unknown): {
  title: string
  subtitle: string
  description: string
} {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: '回收站暂不可用',
      subtitle: '暂不可用',
      description: trashUnavailableDescription,
    }
  }

  return {
    title: '加载回收站失败',
    subtitle: '加载失败',
    description: getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION),
  }
}

function getTrashActionErrorPresentation(
  error: unknown,
  titles: {
    unavailable: string
    failure: string
  }
): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: titles.unavailable,
      description: trashUnavailableDescription,
      color: 'warning',
    }
  }

  const pathConflictToast = getPathConflictErrorToast(error)
  if (pathConflictToast) {
    return pathConflictToast
  }

  const quotaExceededToast = getQuotaExceededErrorToast(error)
  if (quotaExceededToast) {
    return quotaExceededToast
  }

  return {
    title: titles.failure,
    description: getUserFacingErrorDescription(error, GENERIC_ACTION_ERROR_DESCRIPTION),
    color: 'danger',
  }
}

function summarizeTrashBatchResult(result: BatchOperationResult): TrashBatchResultSummary {
  const missingItems: unknown[] = []
  const failedItems: unknown[] = []
  const failedErrors: unknown[] = []

  result.failedItems.forEach((item, index) => {
    const error = result.failedErrors[index]
    if (error instanceof MissingTrashItemSynchronizationError) {
      missingItems.push(item)
      return
    }
    failedItems.push(item)
    failedErrors.push(error)
  })

  return {
    succeeded: result.succeeded,
    missing: missingItems.length,
    failed: failedItems.length,
    missingItems,
    failedItems,
    failedErrors,
  }
}

function formatMissingTrashBatchDescription(count: number): string {
  return missingTrashBatchDescription.replace('{count}', String(count))
}

function getTrashBatchActionToast(
  result: BatchOperationResult,
  titles: {
    success: string
    failure: string
    unavailable: string
    warning: string
    partial: string
  }
) {
  const summary = summarizeTrashBatchResult(result)
  const missingDescription = summary.missing > 0
    ? formatMissingTrashBatchDescription(summary.missing)
    : undefined

  if (summary.failed === 0 && summary.missing > 0) {
    if (summary.succeeded === 0) {
      return {
        title: missingDescription!,
        color: 'warning' as const,
      }
    }
    return {
      title: (result.warningCount > 0 ? titles.warning : titles.success)
        .replace('{count}', String(summary.succeeded)),
      description: missingDescription,
      color: 'warning' as const,
    }
  }

  if (summary.failed === 0 && result.warningCount > 0) {
    return {
      title: titles.warning.replace('{count}', String(summary.succeeded)),
      color: 'warning' as const,
    }
  }

  if (summary.succeeded === 0 && summary.failed > 0 && summary.failedErrors.every((error) => {
    return error instanceof ApiError && error.isUnavailable
  })) {
    return {
      title: titles.unavailable,
      description: [missingDescription, trashUnavailableDescription].filter(Boolean).join('；'),
      color: 'warning' as const,
    }
  }

  const sharedFailureToast =
    getSharedPathConflictErrorToast(summary.failedErrors)
    ?? getSharedQuotaExceededErrorToast(summary.failedErrors)

  if (summary.succeeded === 0 && summary.missing === 0 && sharedFailureToast) {
    return sharedFailureToast
  }

  if (summary.missing > 0 && summary.failed > 0) {
    const title = summary.succeeded > 0
      ? titles.partial
        .replace('{succeeded}', String(summary.succeeded))
        .replace('{failed}', String(summary.failed))
      : titles.failure.replace('{count}', String(summary.failed))
    return {
      title,
      description: [missingDescription, sharedFailureToast?.description].filter(Boolean).join('；') || undefined,
      color: 'warning' as const,
    }
  }

  if (summary.succeeded > 0 && summary.failed > 0 && sharedFailureToast) {
    return {
      title: titles.partial
        .replace('{succeeded}', String(summary.succeeded))
        .replace('{failed}', String(summary.failed)),
      description: sharedFailureToast.description,
      color: 'warning' as const,
    }
  }

  return undefined
}

function getTrashItemParentPath(item: TrashItem): string {
  let originalPath = item.originalPath
  try {
    originalPath = normalizePath(item.originalPath)
  } catch {
    originalPath = item.originalPath.trim() || '/'
  }
  const lastSlashIndex = originalPath.lastIndexOf('/')
  if (lastSlashIndex <= 0) {
    return '/'
  }
  return originalPath.slice(0, lastSlashIndex)
}

function getBatchRestoreAutoDeleteSummary(
  items: TrashItem[],
  trashAutoCleanupEnabled: boolean | undefined,
): string {
  if (items.length === 0) {
    return '尚未选择项目'
  }
  if (trashAutoCleanupEnabled === undefined) {
    return '自动清理设置未知；容量不足时仍可能提前清理'
  }
  if (!trashAutoCleanupEnabled) {
    return '按到期时间自动清理未启用；容量不足时仍可能提前清理'
  }

  const expiredCount = items.filter((item) => getAutoDeleteBadgeLabel(item, trashAutoCleanupEnabled) === '已过期，等待清理').length
  if (expiredCount > 0) {
    return `${expiredCount} 项已过期并等待清理；容量不足时仍可能提前清理`
  }

  const soonCount = items.filter((item) => {
    const label = getAutoDeleteBadgeLabel(item, trashAutoCleanupEnabled)
    return Boolean(label && label !== '自动清理设置未知' && label !== '已过期，等待清理')
  }).length
  if (soonCount > 0) {
    return `${soonCount} 项接近到期时间；容量不足时仍可能提前清理`
  }

  return '所选项目近期不会到期；容量不足时仍可能提前清理'
}

function formatTrashBatchRestoreReviewReport(
  items: TrashItem[],
  trashAutoCleanupEnabled: boolean | undefined,
): string {
  const fileCount = items.filter((item) => !item.isDir).length
  const directoryCount = items.filter((item) => item.isDir).length
  const totalSize = items.reduce((sum, item) => sum + item.size, 0)
  const targetDirectories = Array.from(new Set(items.map(getTrashItemParentPath))).sort()

  return [
    '回收站批量恢复复核',
    `恢复项目：${items.length} 项（${directoryCount} 个目录，${fileCount} 个文件）`,
    `原始目录：${targetDirectories.length} 个目标目录`,
    `可见数据量：${formatBytes(totalSize)}`,
    `自动清理：${getBatchRestoreAutoDeleteSummary(items, trashAutoCleanupEnabled)}`,
    '冲突处理：若原路径已存在同名文件、父目录不可写或配额不足，服务端会拒绝对应项目并保留在回收站。',
    '执行结果：成功项目会从回收站移除；失败项目会保持选中，便于继续处理。',
    '涉及目录：',
    ...targetDirectories.map((target) => `- ${target}`),
  ].join('\n')
}

function TrashRestoreReviewItem({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 rounded-lg border border-divider bg-content1 px-3 py-2">
      <div className="text-xs text-default-500">{label}</div>
      <div className="mt-1 break-words text-sm font-medium text-foreground">{value}</div>
    </div>
  )
}

function TrashBatchRestoreReview({
  items,
  trashAutoCleanupEnabled,
}: {
  items: TrashItem[]
  trashAutoCleanupEnabled: boolean | undefined
}) {
  const fileCount = items.filter((item) => !item.isDir).length
  const directoryCount = items.filter((item) => item.isDir).length
  const totalSize = items.reduce((sum, item) => sum + item.size, 0)
  const targetDirectories = Array.from(new Set(items.map(getTrashItemParentPath))).sort()
  const visibleTargets = targetDirectories.slice(0, 4)
  const hiddenTargetCount = Math.max(0, targetDirectories.length - visibleTargets.length)
  const handleCopyReviewReport = async () => {
    try {
      await copyTextToClipboard(formatTrashBatchRestoreReviewReport(items, trashAutoCleanupEnabled))
      addToast({ title: '批量恢复复核记录已复制', color: 'success' })
    } catch {
      addToast({
        title: '复制批量恢复复核记录失败',
        description: clipboardWriteFailureDescription,
        color: 'danger',
      })
    }
  }

  return (
    <div aria-label="跨目录恢复执行前复核" className="rounded-lg border border-warning/20 bg-warning/10 p-3 text-sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2 font-medium text-default-800">
          <AlertTriangle size={16} className="text-warning" />
          <span>跨目录恢复复核</span>
        </div>
        <Button size="sm" variant="flat" className="rounded-lg" startContent={<Copy size={14} />} onPress={handleCopyReviewReport}>
          复制复核记录
        </Button>
      </div>
      <div className="mt-3 grid gap-2 sm:grid-cols-2">
        <TrashRestoreReviewItem label="恢复项目" value={`${items.length} 项 · ${directoryCount} 个目录 · ${fileCount} 个文件`} />
        <TrashRestoreReviewItem label="原始目录" value={`${targetDirectories.length} 个目标目录`} />
        <TrashRestoreReviewItem label="可见数据量" value={formatBytes(totalSize)} />
        <TrashRestoreReviewItem label="自动清理" value={getBatchRestoreAutoDeleteSummary(items, trashAutoCleanupEnabled)} />
        <TrashRestoreReviewItem label="冲突处理" value="若原路径已存在同名文件、父目录不可写或配额不足，服务端会拒绝对应项目并保留在回收站。" />
        <TrashRestoreReviewItem label="执行结果" value="成功项目会从回收站移除；失败项目会保持选中，便于继续处理。" />
      </div>
      {visibleTargets.length > 0 && (
        <div className="mt-3 rounded-lg border border-divider bg-content1 px-3 py-2">
          <div className="text-xs text-default-500">涉及目录</div>
          <div className="mt-2 flex flex-wrap gap-2">
            {visibleTargets.map((target) => (
              <code key={target} className="break-anywhere rounded-md bg-content2 px-2 py-1 text-xs text-default-700">
                {target}
              </code>
            ))}
            {hiddenTargetCount > 0 && (
              <span className="rounded-md bg-content2 px-2 py-1 text-xs text-default-500">另有 {hiddenTargetCount} 个目录</span>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

// Trash item row
function TrashRow({
  item,
  isSelected,
  onSelect,
  onRestore,
  onDelete,
  trashAutoCleanupEnabled,
  canWrite,
}: {
  item: TrashItem
  isSelected: boolean
  onSelect: () => void
  onRestore: () => void
  onDelete: () => void
  trashAutoCleanupEnabled: boolean | undefined
  canWrite: boolean
}) {
  const autoDeleteBadgeLabel = getAutoDeleteBadgeLabel(item, trashAutoCleanupEnabled)
  const isExpiredForCleanup = autoDeleteBadgeLabel === '已过期，等待清理'
  const autoDeleteBadgeColor = autoDeleteBadgeLabel === '自动清理设置未知'
    ? 'default'
    : isExpiredForCleanup
      ? 'danger'
      : 'warning'
  const sizeLabel = item.isDir ? '-' : formatBytes(item.size)
  const selectionControl = canWrite ? (
    <Checkbox
      aria-label={`选择 ${item.name}`}
      isSelected={isSelected}
      onValueChange={onSelect}
      classNames={{ label: "sr-only" }}
    >
      选择 {item.name}
    </Checkbox>
  ) : (
    <div className="w-6 shrink-0" />
  )
  const actionButtons = canWrite ? (
    <>
      <Button
        isIconOnly
        size="sm"
        variant="light"
        color="success"
        aria-label={`恢复 ${item.name}`}
        onPress={onRestore}
        title="恢复"
        className="rounded-lg"
      >
        <RotateCcw size={16} />
      </Button>
      <Button
        isIconOnly
        size="sm"
        variant="light"
        color="danger"
        aria-label={`永久删除 ${item.name}`}
        onPress={onDelete}
        title="永久删除"
        className="rounded-lg"
      >
        <Trash2 size={16} />
      </Button>
    </>
  ) : null
  
  return (
    <div
      className={cn(
        "border-b border-divider transition-all duration-200 hover:bg-content2/50",
        isSelected && "bg-accent-primary/10"
      )}
    >
      <div className="flex flex-wrap items-start gap-x-3 gap-y-2 px-4 py-4 sm:flex-nowrap sm:items-center sm:gap-4 sm:py-3">
        {selectionControl}
        <div className="flex w-8 shrink-0 items-center justify-center sm:w-8">
          <FileIcon name={item.name} isDir={item.isDir} size={24} variant="bare" />
        </div>
        <div className="min-w-0 flex-1 basis-[calc(100%-6rem)] sm:basis-auto">
          <p className="truncate font-medium text-foreground">{item.name}</p>
          <p className="truncate text-xs text-default-500">{item.originalPath}</p>
        </div>
        <div className="ml-14 text-xs text-default-500 sm:ml-0 sm:w-24 sm:text-right sm:text-sm">
          {sizeLabel}
        </div>
        <div className="flex items-center gap-1 text-xs text-default-500 sm:w-32 sm:justify-end sm:text-sm">
          <div className="flex items-center gap-1">
            <Clock size={12} />
            {formatRelativeTime(item.deletedAt)}
          </div>
          {autoDeleteBadgeLabel && (
            <Chip size="sm" variant="flat" color={autoDeleteBadgeColor} className="sm:ml-1">
              {autoDeleteBadgeLabel}
            </Chip>
          )}
        </div>
        <div className="ml-auto flex shrink-0 items-center justify-end gap-1 sm:w-20">
          {actionButtons}
        </div>
      </div>
    </div>
  )
}

export function TrashPage() {
  const queryClient = useQueryClient()
  const [searchParams, setSearchParams] = useSearchParams()
  const canWrite = useCanWrite()
  const user = useUser()
  const { hasInvalidHomeDir } = resolveUserHomeScope(user)
  const authScopeKey = user?.id ?? 'anonymous'
  const trashQueryKey = useMemo(() => ['trash', authScopeKey] as const, [authScopeKey])
  const [selectedItems, setSelectedItems] = useState<Set<string>>(new Set())
  const [actionItem, setActionItem] = useState<TrashItem | null>(null)
  const [batchRestoreIntent, setBatchRestoreIntent] = useState<TrashBatchIntent | null>(null)
  const [batchDeleteIntent, setBatchDeleteIntent] = useState<TrashBatchIntent | null>(null)
  const [emptyTrashIntent, setEmptyTrashIntent] = useState<TrashEmptyIntent | null>(null)
  const [isEmptyTrashReconciling, setIsEmptyTrashReconciling] = useState(false)
  const actionItemRef = useRef(actionItem)
  const restoreAbortControllerRef = useRef<AbortController | null>(null)
  const deleteAbortControllerRef = useRef<AbortController | null>(null)
  const emptyAbortControllerRef = useRef<AbortController | null>(null)
  const batchRestoreAbortControllerRef = useRef<AbortController | null>(null)
  const batchDeleteAbortControllerRef = useRef<AbortController | null>(null)
  const emptyReconciliationSessionRef = useRef(0)
  const deleteFocusFallbackRef = useRef<HTMLDivElement>(null)

  useLayoutEffect(() => {
    actionItemRef.current = actionItem
  }, [actionItem])

  useEffect(() => {
    return () => {
      restoreAbortControllerRef.current?.abort()
      restoreAbortControllerRef.current = null
      deleteAbortControllerRef.current?.abort()
      deleteAbortControllerRef.current = null
      emptyAbortControllerRef.current?.abort()
      emptyAbortControllerRef.current = null
      batchRestoreAbortControllerRef.current?.abort()
      batchRestoreAbortControllerRef.current = null
      batchDeleteAbortControllerRef.current?.abort()
      batchDeleteAbortControllerRef.current = null
      emptyReconciliationSessionRef.current += 1
    }
  }, [])

  const { isOpen: isDeleteOpen, onOpen: onDeleteOpen, onClose: onDeleteClose } = useDisclosure()
  const { isOpen: isBatchDeleteOpen, onOpen: onBatchDeleteOpen, onClose: onBatchDeleteClose } = useDisclosure()
  const { isOpen: isBatchRestoreOpen, onOpen: onBatchRestoreOpen, onClose: onBatchRestoreClose } = useDisclosure()
  const { isOpen: isEmptyOpen, onOpen: onEmptyOpen, onClose: onEmptyClose } = useDisclosure()
  const {
    initialFocusRef: deleteCancelButtonRef,
    captureReturnFocus: captureDeleteReturnFocus,
    setFallbackReturnFocus: setDeleteFallbackReturnFocus,
  } = useDestructiveDialogFocus(isDeleteOpen)
  const {
    initialFocusRef: emptyCancelButtonRef,
    captureReturnFocus: captureEmptyReturnFocus,
    setFallbackReturnFocus: setEmptyFallbackReturnFocus,
  } = useDestructiveDialogFocus(isEmptyOpen)

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: trashQueryKey,
    queryFn: ({ signal }) => listTrash({ signal }),
    enabled: !hasInvalidHomeDir,
  })

  const items = useMemo(() => data?.items ?? [], [data?.items])
  const retentionDays = data?.retentionDays
  const retentionEnabled = data?.retentionEnabled
  const trashAutoCleanupEnabled = data?.trashAutoCleanupEnabled
  const pathFilter = useMemo(() => normalizeTrashPathFilter(searchParams.get('path')), [searchParams])
  const visibleItems = useMemo(() => {
    return pathFilter ? items.filter((item) => trashItemMatchesPathFilter(item, pathFilter)) : items
  }, [items, pathFilter])
  const visibleSelectedItems = useMemo(() => {
    if (selectedItems.size === 0) {
      return selectedItems
    }

    const validIds = new Set(visibleItems.map((item) => item.id))
    let changed = false
    const next = new Set<string>()

    for (const id of selectedItems) {
      if (validIds.has(id)) {
        next.add(id)
        continue
      }
      changed = true
    }

    return changed ? next : selectedItems
  }, [selectedItems, visibleItems])
  const selectedTrashItems = useMemo(() => {
    if (visibleSelectedItems.size === 0) {
      return []
    }
    return visibleItems.filter((item) => visibleSelectedItems.has(item.id))
  }, [visibleItems, visibleSelectedItems])

  const removeSelectedIds = useCallback((ids: string[]) => {
    if (ids.length === 0) {
      return
    }

    const removedIds = new Set(ids)
    setSelectedItems((prev) => {
      if (prev.size === 0) {
        return prev
      }

      let changed = false
      const next = new Set<string>()
      for (const id of prev) {
        if (removedIds.has(id)) {
          changed = true
          continue
        }
        next.add(id)
      }

      return changed ? next : prev
    })
  }, [])

  const removeTrashItemsFromCache = useCallback((ids: string[]) => {
    if (ids.length === 0) {
      return
    }

    const removedIds = new Set(ids)
    queryClient.setQueryData<TrashListResponse>(trashQueryKey, (current) => {
      if (!current) {
        return current
      }

      const items = current.items.filter((item) => !removedIds.has(item.id))
      if (items.length === current.items.length) {
        return current
      }

      return {
        ...current,
        items,
        count: items.length,
        totalSize: items.reduce((sum, item) => sum + item.size, 0),
      }
    })
  }, [queryClient, trashQueryKey])

  const markEmptyTrashCacheForReconciliation = useCallback((completedIds: string[]) => {
    removeTrashItemsFromCache(completedIds)
    queryClient.invalidateQueries({ queryKey: trashQueryKey, refetchType: 'none' })
  }, [queryClient, removeTrashItemsFromCache, trashQueryKey])

  const handleRefreshTrash = useCallback(async () => {
  const result = await refetch()
  if (result.error) {
    addToast(getTrashActionErrorPresentation(result.error, {
      unavailable: '回收站暂不可用',
      failure: '刷新失败',
    }))
    return
  }
  addToast({ title: '回收站已刷新', color: 'success' })
  }, [refetch])

  const restoreTrashItem = useCallback(async (id: string, options: { signal?: AbortSignal } = {}) => {
    try {
      return await restoreFromTrash(id, undefined, options)
    } catch (error) {
      if (error instanceof ApiError && error.isNotFound) {
        return getMissingTrashItemResult()
      }

      throw error
    }
  }, [])

  const deleteTrashItem = useCallback(async (id: string, options: { signal?: AbortSignal } = {}) => {
    try {
      return await deleteFromTrash(id, options)
    } catch (error) {
      if (error instanceof ApiError && error.isNotFound) {
        return getMissingTrashItemResult()
      }

      throw error
    }
  }, [])

  const recordTrashRestoreReview = useCallback(async (restoredItems: TrashItem[], signal: AbortSignal) => {
    if (restoredItems.length === 0) {
      return
    }

    try {
      const restoredPaths = new Set(restoredItems.map((item) => normalizePath(item.originalPath)))
      const activityResult = await listActivity({
        action: 'trash_restore',
        actionGroup: 'risk',
        path: pathFilter || undefined,
        limit: TRASH_RESTORE_ACTIVITY_LIMIT,
        offset: 0,
        signal,
      })
      if (signal.aborted) {
        return
      }

      const executionEntries = activityResult.items.filter((entry) => (
        entry.action === 'trash_restore' && trashActivityMatchesAnyPath(entry, restoredPaths)
      ))
      if (executionEntries.length === 0) {
        return
      }

      const matchedItems = getTrashRestoreMatchedItems(restoredItems, executionEntries)
      await createActivityReviewRecord(buildTrashRestoreReviewRecordInput({
        entries: executionEntries,
        totalEntries: activityResult.total,
        restoredItems: matchedItems.length > 0 ? matchedItems : restoredItems,
        pathFilter,
      }), { signal })
      if (!signal.aborted) {
        addToast({ title: '恢复结果已记录', color: 'success' })
      }
    } catch (error) {
      if (!signal.aborted && !isAbortError(error)) {
        addToast({
          title: '恢复已完成，复核记录写入失败',
          description: getUserFacingErrorDescription(error),
          color: 'warning',
        })
      }
    }
  }, [pathFilter])

  // Mutations
  const restoreMutation = useMutation({
    mutationFn: ({ id, signal }: { id: string; signal: AbortSignal }) => restoreTrashItem(id, { signal }),
    onSuccess: async (result, variables) => {
      if (variables.signal.aborted) {
        return
      }
      const id = variables.id
      const restoredItem = items.find((item) => item.id === id)
      removeTrashItemsFromCache([id])
      removeSelectedIds([id])
      queryClient.invalidateQueries({ queryKey: trashQueryKey })
      queryClient.invalidateQueries({ queryKey: ['files'] })
      addToast(getTrashRestoreSuccessToast(result))
      if (restoredItem && result.message !== missingTrashItemTitle) {
        await recordTrashRestoreReview([restoredItem], variables.signal)
      }
    },
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) {
        return
      }
      addToast(getTrashActionErrorPresentation(error, {
        unavailable: '恢复暂不可用',
        failure: '恢复失败',
      }))
    },
    onSettled: (_result, _error, variables) => {
      if (restoreAbortControllerRef.current?.signal === variables?.signal) {
        restoreAbortControllerRef.current = null
      }
    },
  })

  const deleteMutation = useMutation({
    mutationFn: ({ id, signal }: { id: string; signal: AbortSignal }) => deleteTrashItem(id, { signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }
      const id = variables.id
      const focusFallback = deleteFocusFallbackRef.current
      if (focusFallback) {
        focusFallback.tabIndex = -1
      }
      setDeleteFallbackReturnFocus(focusFallback)
      removeTrashItemsFromCache([id])
      removeSelectedIds([id])
      queryClient.invalidateQueries({ queryKey: trashQueryKey })
      addToast(getTrashDeleteSuccessToast(result))
      if (actionItemRef.current?.id === id) {
        onDeleteClose()
      }
      setActionItem((current) => current?.id === id ? null : current)
    },
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) {
        return
      }
      addToast(getTrashActionErrorPresentation(error, {
        unavailable: '永久删除暂不可用',
        failure: '删除失败',
      }))
    },
    onSettled: (_result, _error, variables) => {
      if (deleteAbortControllerRef.current?.signal === variables?.signal) {
        deleteAbortControllerRef.current = null
      }
    },
  })

  const emptyMutation = useMutation({
    mutationFn: ({ items, signal }: { items: TrashEmptyIntent['items']; signal: AbortSignal }) => (
      emptyConfirmedTrashItems(items.map((item) => item.id), signal)
    ),
    onSuccess: async (result, variables) => {
      const completedIds = [...result.deleted, ...result.skipped]
      if (variables.signal.aborted) {
        markEmptyTrashCacheForReconciliation(completedIds)
        return
      }
      let refreshedData: TrashListResponse | undefined
      let refreshFailed = false
      try {
        const refreshResult = await refetch()
        refreshedData = refreshResult.data
        refreshFailed = refreshResult.error != null || !refreshedData
      } catch {
        refreshFailed = true
      }
      if (variables.signal.aborted) {
        markEmptyTrashCacheForReconciliation(completedIds)
        return
      }

      const remainingIds = new Set(result.remaining)
      const currentIds = !refreshFailed && refreshedData
        ? new Set(refreshedData.items.map((item) => item.id))
        : null
      const remainingItems = variables.items.filter((item) => (
        remainingIds.has(item.id) && (currentIds === null || currentIds.has(item.id))
      ))
      const reconciledAbsentCount = currentIds === null
        ? 0
        : result.remaining.length - remainingItems.length
      if (refreshFailed) {
        markEmptyTrashCacheForReconciliation(completedIds)
      }
      removeSelectedIds(completedIds)
      const reconciledResult = currentIds === null
        ? result
        : { ...result, remaining: remainingItems.map((item) => item.id) }
      addToast(getEmptyTrashSuccessToast(reconciledResult, refreshFailed, reconciledAbsentCount))
      if (remainingItems.length > 0) {
        setEmptyTrashIntent({
          items: remainingItems,
          reconciliationRequired: refreshFailed,
        })
      } else {
        onEmptyClose()
        setEmptyTrashIntent(null)
      }
    },
    onError: async (error, variables) => {
      if (error instanceof EmptyTrashChunkExecutionError && error.aborted) {
        const completedIds = [
          ...error.completedResult.deleted,
          ...error.completedResult.skipped,
        ]
        markEmptyTrashCacheForReconciliation(completedIds)
        return
      }
      if (variables.signal.aborted || isAbortError(error)) {
        markEmptyTrashCacheForReconciliation([])
        return
      }
      if (error instanceof EmptyTrashChunkExecutionError) {
        const completedIds = [
          ...error.completedResult.deleted,
          ...error.completedResult.skipped,
        ]
        const completedIdSet = new Set(completedIds)
        let currentIds: Set<string> | null = null
        let refreshFailed = false
        try {
          const refreshResult = await refetch()
          if (refreshResult.error || !refreshResult.data) {
            refreshFailed = true
          } else {
            currentIds = new Set(refreshResult.data.items.map((item) => item.id))
          }
        } catch {
          refreshFailed = true
        }
        if (variables.signal.aborted) {
          markEmptyTrashCacheForReconciliation(completedIds)
          return
        }

        const remainingItems = variables.items.filter((item) => (
          currentIds === null ? !completedIdSet.has(item.id) : currentIds.has(item.id)
        ))
        const reconciledAbsentCount = currentIds === null
          ? 0
          : variables.items.reduce((count, item) => (
              !completedIdSet.has(item.id) && !currentIds.has(item.id) ? count + 1 : count
            ), 0)
        if (refreshFailed) {
          markEmptyTrashCacheForReconciliation(completedIds)
        }
        removeSelectedIds(completedIds)
        addToast(getEmptyTrashInterruptedToast(
          error,
          remainingItems.length,
          reconciledAbsentCount,
          refreshFailed,
        ))
        if (remainingItems.length > 0) {
          setEmptyTrashIntent({
            items: remainingItems,
            reconciliationRequired: refreshFailed,
          })
        } else {
          onEmptyClose()
          setEmptyTrashIntent(null)
        }
        return
      }
      addToast(getTrashActionErrorPresentation(error, {
        unavailable: '清空回收站暂不可用',
        failure: '清空失败',
      }))
    },
    onSettled: (_result, _error, variables) => {
      if (emptyAbortControllerRef.current?.signal === variables?.signal) {
        emptyAbortControllerRef.current = null
      }
    },
  })

  const handleCloseDeleteModal = useCallback(() => {
    if (deleteMutation.isPending) {
      return
    }
    onDeleteClose()
  }, [deleteMutation.isPending, onDeleteClose])

  const handleCloseEmptyModal = useCallback(() => {
    if (emptyMutation.isPending || isEmptyTrashReconciling) {
      return
    }
    emptyReconciliationSessionRef.current += 1
    setIsEmptyTrashReconciling(false)
    onEmptyClose()
    setEmptyTrashIntent(null)
  }, [emptyMutation.isPending, isEmptyTrashReconciling, onEmptyClose])

  const handleEmptyTrashClick = useCallback(() => {
    if (!canWrite || items.length === 0) {
      return
    }
    const focusFallback = deleteFocusFallbackRef.current
    if (focusFallback) {
      focusFallback.tabIndex = -1
    }
    captureEmptyReturnFocus()
    setEmptyFallbackReturnFocus(focusFallback)
    emptyReconciliationSessionRef.current += 1
    setIsEmptyTrashReconciling(false)
    setEmptyTrashIntent({
      items: items.map(({ id, name, size }) => ({ id, name, size })),
    })
    onEmptyOpen()
  }, [canWrite, captureEmptyReturnFocus, items, onEmptyOpen, setEmptyFallbackReturnFocus])

  const handleSelectAll = useCallback(() => {
    if (!canWrite) return
    if (visibleItems.length === 0) return
    if (visibleSelectedItems.size === visibleItems.length) {
      setSelectedItems(new Set())
    } else {
      setSelectedItems(new Set(visibleItems.map(item => item.id)))
    }
  }, [canWrite, visibleItems, visibleSelectedItems.size])

  // Batch restore using custom hook
  const { execute: executeBatchRestore, isLoading: isBatchRestoring } = useBatchOperation<string, ActionResult>({
    operation: (id, context) => classifyTrashBatchAction(
      () => restoreTrashItem(id, { signal: context.signal })
    ),
    messages: {
      success: '{count} 项恢复成功',
      failure: '{count} 项恢复失败',
      partial: '{succeeded} 项恢复成功，{failed} 项失败',
    },
    getWarningMessage: getTrashBatchWarningMessage,
    getToast: (result) => getTrashBatchActionToast(result, {
      success: '{count} 项恢复成功',
      failure: '{count} 项恢复失败',
      unavailable: '批量恢复暂不可用',
      warning: '已恢复 {count} 项，但存在警告',
      partial: '{succeeded} 项恢复成功，{failed} 项失败',
    }),
    onComplete: (result) => {
      const summary = summarizeTrashBatchResult(result)
      removeTrashItemsFromCache([
        ...(result.succeededItems as string[]),
        ...(summary.missingItems as string[]),
      ])
      setSelectedItems(new Set(summary.failedItems as string[]))
      queryClient.invalidateQueries({ queryKey: trashQueryKey })
      queryClient.invalidateQueries({ queryKey: ['files'] })
    },
  })

  const handleCloseBatchRestoreModal = useCallback(() => {
    if (isBatchRestoring) {
      return
    }
    onBatchRestoreClose()
    setBatchRestoreIntent(null)
  }, [isBatchRestoring, onBatchRestoreClose])

  const handleBatchRestoreClick = useCallback(() => {
    if (!canWrite) return
    if (selectedTrashItems.length === 0) return
    setBatchRestoreIntent({
      items: selectedTrashItems.map((item) => ({ ...item })),
      trashAutoCleanupEnabled,
    })
    onBatchRestoreOpen()
  }, [canWrite, onBatchRestoreOpen, selectedTrashItems, trashAutoCleanupEnabled])

  const handleConfirmBatchRestore = useCallback(async () => {
    if (!canWrite) return
    if (!batchRestoreIntent) return
    const restoreTargets = batchRestoreIntent.items
    const ids = restoreTargets.map((item) => item.id)
    if (ids.length === 0) return
    batchRestoreAbortControllerRef.current?.abort()
    const controller = new AbortController()
    batchRestoreAbortControllerRef.current = controller
    try {
      const result = await executeBatchRestore(ids, { signal: controller.signal })
      if (!controller.signal.aborted) {
        const restoredIds = new Set(result.succeededItems as string[])
        const restoredTargets = restoreTargets.filter((item) => restoredIds.has(item.id))
        await recordTrashRestoreReview(restoredTargets, controller.signal)
      }
      if (!controller.signal.aborted) {
        onBatchRestoreClose()
        setBatchRestoreIntent(null)
      }
    } finally {
      if (batchRestoreAbortControllerRef.current === controller) {
        batchRestoreAbortControllerRef.current = null
      }
    }
  }, [batchRestoreIntent, canWrite, executeBatchRestore, onBatchRestoreClose, recordTrashRestoreReview])

  // Batch delete using custom hook
  const { execute: executeBatchDelete, isLoading: isBatchDeleting } = useBatchOperation<string, ActionResult>({
    operation: (id, context) => classifyTrashBatchAction(
      () => deleteTrashItem(id, { signal: context.signal })
    ),
    messages: {
      success: '{count} 项已永久删除',
      failure: '{count} 项永久删除失败',
      partial: '{succeeded} 项永久删除成功，{failed} 项失败',
    },
    getWarningMessage: getTrashBatchWarningMessage,
    getToast: (result) => getTrashBatchActionToast(result, {
      success: '{count} 项已永久删除',
      failure: '{count} 项永久删除失败',
      unavailable: '批量永久删除暂不可用',
      warning: '已永久删除 {count} 项，但存在警告',
      partial: '{succeeded} 项永久删除成功，{failed} 项失败',
    }),
    onComplete: (result) => {
      const summary = summarizeTrashBatchResult(result)
      removeTrashItemsFromCache([
        ...(result.succeededItems as string[]),
        ...(summary.missingItems as string[]),
      ])
      setSelectedItems(new Set(summary.failedItems as string[]))
      queryClient.invalidateQueries({ queryKey: trashQueryKey })
    },
  })

  const handleCloseBatchDeleteModal = useCallback(() => {
    if (isBatchDeleting) {
      return
    }
    onBatchDeleteClose()
    setBatchDeleteIntent(null)
  }, [isBatchDeleting, onBatchDeleteClose])

  const handleBatchDeleteClick = useCallback(() => {
    if (!canWrite) return
    if (selectedTrashItems.length === 0) return
    setBatchDeleteIntent({
      items: selectedTrashItems.map((item) => ({ ...item })),
      trashAutoCleanupEnabled,
    })
    onBatchDeleteOpen()
  }, [canWrite, onBatchDeleteOpen, selectedTrashItems, trashAutoCleanupEnabled])

  const handleBatchDelete = useCallback(async () => {
    if (!canWrite) return
    if (!batchDeleteIntent) return
    const ids = batchDeleteIntent.items.map((item) => item.id)
    if (ids.length === 0) return
    batchDeleteAbortControllerRef.current?.abort()
    const controller = new AbortController()
    batchDeleteAbortControllerRef.current = controller
    try {
      await executeBatchDelete(ids, { signal: controller.signal })
      if (!controller.signal.aborted) {
        onBatchDeleteClose()
        setBatchDeleteIntent(null)
      }
    } finally {
      if (batchDeleteAbortControllerRef.current === controller) {
        batchDeleteAbortControllerRef.current = null
      }
    }
  }, [batchDeleteIntent, canWrite, executeBatchDelete, onBatchDeleteClose])

  const handleDeleteClick = useCallback((item: TrashItem) => {
    if (!canWrite) return
    deleteFocusFallbackRef.current?.removeAttribute('tabindex')
    captureDeleteReturnFocus()
    setActionItem(item)
    onDeleteOpen()
  }, [canWrite, captureDeleteReturnFocus, onDeleteOpen])

  const handleRestoreClick = useCallback((item: TrashItem) => {
    if (!canWrite) return
    restoreAbortControllerRef.current?.abort()
    const controller = new AbortController()
    restoreAbortControllerRef.current = controller
    restoreMutation.mutate({ id: item.id, signal: controller.signal })
  }, [canWrite, restoreMutation])

  const handleConfirmDelete = useCallback(() => {
    if (!canWrite) return
    if (actionItem) {
      deleteAbortControllerRef.current?.abort()
      const controller = new AbortController()
      deleteAbortControllerRef.current = controller
      deleteMutation.mutate({ id: actionItem.id, signal: controller.signal })
    }
  }, [canWrite, actionItem, deleteMutation])

  const handleConfirmEmpty = useCallback(() => {
    if (!canWrite) return
    if (!emptyTrashIntent || emptyTrashIntent.reconciliationRequired || emptyTrashIntent.items.length === 0) return
    emptyAbortControllerRef.current?.abort()
    const controller = new AbortController()
    emptyAbortControllerRef.current = controller
    emptyMutation.mutate({ items: emptyTrashIntent.items, signal: controller.signal })
  }, [canWrite, emptyMutation, emptyTrashIntent])

  const handleReconcileEmptyTrash = useCallback(async () => {
    if (!emptyTrashIntent?.reconciliationRequired || isEmptyTrashReconciling) {
      return
    }
    const session = emptyReconciliationSessionRef.current + 1
    emptyReconciliationSessionRef.current = session
    setIsEmptyTrashReconciling(true)
    try {
      const refreshResult = await refetch()
      if (emptyReconciliationSessionRef.current !== session) {
        return
      }
      if (refreshResult.error || !refreshResult.data) {
        const presentation = getTrashActionErrorPresentation(refreshResult.error, {
          unavailable: '清空结果仍无法核对',
          failure: '清空结果仍无法核对',
        })
        addToast(presentation)
        return
      }
      const currentIds = new Set(refreshResult.data.items.map((item) => item.id))
      const remainingItems = emptyTrashIntent.items.filter((item) => currentIds.has(item.id))
      if (remainingItems.length === 0) {
        addToast({
          title: '清空范围已核对',
          description: '本次确认范围内的项目均已不在回收站。',
          color: 'success',
        })
        onEmptyClose()
        setEmptyTrashIntent(null)
        return
      }
      setEmptyTrashIntent({ items: remainingItems })
      addToast({
        title: '清空范围已核对',
        description: `${remainingItems.length} 项仍在回收站，可继续处理。`,
        color: 'success',
      })
    } catch (error) {
      if (emptyReconciliationSessionRef.current !== session) {
        return
      }
      const presentation = getTrashActionErrorPresentation(error, {
        unavailable: '清空结果仍无法核对',
        failure: '清空结果仍无法核对',
      })
      addToast(presentation)
    } finally {
      if (emptyReconciliationSessionRef.current === session) {
        setIsEmptyTrashReconciling(false)
      }
    }
  }, [emptyTrashIntent, isEmptyTrashReconciling, onEmptyClose, refetch])

  const handleClearPathFilter = useCallback(() => {
    setSearchParams({})
  }, [setSearchParams])

  if (hasInvalidHomeDir) {
    return (
      <div className="flex h-full min-h-0 flex-col space-y-4 overflow-auto p-4 custom-scrollbar sm:p-6">
        <PageHeader
          title="回收站"
          subtitle={invalidHomeDirTitle}
          icon={Trash2}
        />
        <div className="flex flex-1 items-center justify-center">
          <EmptyState
            icon={AlertCircle}
            title={invalidHomeDirTitle}
            description={getInvalidHomeDirDescription('查看回收站')}
          />
        </div>
      </div>
    )
  }

  if (isLoading) {
    return (
      <div
        role="status"
        aria-label="加载回收站"
        aria-busy="true"
        className="flex h-full items-center justify-center p-6 lg:p-8"
      >
        <div className="text-center">
          <div className="w-12 h-12 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-4" />
          <p className="text-default-500">加载回收站…</p>
        </div>
      </div>
    )
  }

  if (error && !data) {
    const errorPresentation = getTrashLoadErrorPresentation(error)

    return (
      <div className="flex h-full min-h-0 flex-col space-y-4 overflow-auto p-4 custom-scrollbar sm:p-6">
        <PageHeader
          title="回收站"
          subtitle={errorPresentation.subtitle}
          icon={Trash2}
        />
        <div className="flex flex-1 items-center justify-center">
          <EmptyState
            icon={AlertCircle}
            title={errorPresentation.title}
            description={errorPresentation.description}
            action={
		      <Button variant="bordered" className="rounded-lg" onPress={handleRefreshTrash}>
                重新加载
              </Button>
            }
          />
        </div>
      </div>
    )
  }

  const totalSize = data?.totalSize ?? items.reduce((sum, item) => sum + item.size, 0)
  const itemCount = data?.count ?? items.length
  const emptyIntentItemCount = emptyTrashIntent?.items.length ?? 0
  const emptyIntentTotalSize = emptyTrashIntent?.items.reduce((sum, item) => sum + item.size, 0) ?? 0
  const emptyReconciliationRequired = emptyTrashIntent?.reconciliationRequired === true
  const visibleItemCount = visibleItems.length
  const retentionLabel = getTrashPolicyLabel(retentionEnabled, retentionDays, trashAutoCleanupEnabled)
  const pageSubtitle = pathFilter
    ? `当前筛选 ${visibleItemCount} 项 / 共 ${itemCount} 项 · ${formatBytes(totalSize)} · ${retentionLabel}`
    : `${itemCount} 项 · ${formatBytes(totalSize)} · ${retentionLabel}`

  return (
    <div
      ref={deleteFocusFallbackRef}
      role="region"
      aria-label="回收站内容"
      className="flex h-full min-h-0 flex-col space-y-4 overflow-auto p-4 custom-scrollbar sm:p-6"
    >
      {/* Header */}
      <PageHeader
        title="回收站"
        subtitle={pageSubtitle}
        icon={Trash2}
        actions={
          canWrite && items.length > 0 ? (
            <Button
              color="danger"
              variant="flat"
              className="rounded-lg"
              startContent={<Trash2 size={16} />}
              onPress={handleEmptyTrashClick}
            >
              清空回收站
            </Button>
          ) : null
        }
      />

      {pathFilter && (
        <div aria-label="回收站路径筛选" className="flex flex-col gap-2 rounded-lg border border-divider bg-content1 px-4 py-2.5 text-sm sm:flex-row sm:items-center sm:justify-between">
          <div className="min-w-0">
            <div className="text-xs text-default-500">路径筛选</div>
            <div className="truncate font-medium text-foreground" title={pathFilter}>
              路径：{pathFilter}
            </div>
          </div>
          <Button
            size="sm"
            variant="light"
            className="h-8 rounded-lg px-2 text-default-500"
            startContent={<X size={14} />}
            onPress={handleClearPathFilter}
          >
            清除路径筛选
          </Button>
        </div>
      )}

      {/* Selection bar */}
      {canWrite && visibleSelectedItems.size > 0 && (
        <div className="flex flex-wrap items-center gap-3 rounded-lg border border-divider bg-accent-primary/10 px-4 py-2.5 shadow-[var(--shadow-soft)]">
          <div className="w-8 h-8 rounded-full bg-accent-primary/15 flex items-center justify-center">
            <span className="text-sm font-bold text-accent-primary">{visibleSelectedItems.size}</span>
          </div>
          <span className="text-sm font-medium">已选择 {visibleSelectedItems.size} 项</span>
          <div className="hidden flex-1 sm:block" />
          <Button size="sm" variant="flat" onPress={() => setSelectedItems(new Set())} className="rounded-lg">
            取消选择
          </Button>
          <Button
            size="sm"
            variant="flat"
            color="success"
            startContent={<RotateCcw size={14} />}
            onPress={handleBatchRestoreClick}
            isLoading={isBatchRestoring}
            isDisabled={isBatchRestoring}
            className="rounded-lg"
          >
            恢复
          </Button>
          <Button
            size="sm"
            variant="flat"
            color="danger"
            startContent={<Trash2 size={14} />}
            onPress={handleBatchDeleteClick}
            isLoading={isBatchDeleting}
            className="rounded-lg"
          >
            永久删除
          </Button>
        </div>
      )}

      {/* List header */}
      {visibleItems.length > 0 && (
        <div className="hidden items-center gap-4 px-4 py-2.5 bg-content2/50 backdrop-blur-sm rounded-lg border border-divider text-sm font-medium text-default-400 sm:flex">
          {canWrite ? (
            <Checkbox
              aria-label={visibleSelectedItems.size === visibleItems.length && visibleItems.length > 0 ? '取消全选回收站项目' : '全选回收站项目'}
              isSelected={visibleSelectedItems.size === visibleItems.length && visibleItems.length > 0}
              isIndeterminate={visibleSelectedItems.size > 0 && visibleSelectedItems.size < visibleItems.length}
              onValueChange={handleSelectAll}
              classNames={{
                label: "sr-only",
                wrapper: "before:border-divider",
              }}
            >
              {visibleSelectedItems.size === items.length && items.length > 0 ? '取消全选回收站项目' : '全选回收站项目'}
            </Checkbox>
          ) : (
            <div className="w-6 shrink-0" />
          )}
          <div className="w-8" />
          <div className="flex-1">名称</div>
          <div className="w-24 text-right">大小</div>
          <div className="w-32 text-right">删除时间</div>
          <div className="w-20" />
        </div>
      )}

      {/* Item list */}
      <div className="card-mnemonas min-h-0 flex-1 overflow-auto rounded-lg">
        {visibleItems.length > 0 ? (
          visibleItems.map(item => (
            <TrashRow
              key={item.id}
              item={item}
              isSelected={visibleSelectedItems.has(item.id)}
              trashAutoCleanupEnabled={trashAutoCleanupEnabled}
              canWrite={canWrite}
              onSelect={() => {
                if (!canWrite) return
                const newSet = new Set(visibleSelectedItems)
                if (newSet.has(item.id)) {
                  newSet.delete(item.id)
                } else {
                  newSet.add(item.id)
                }
                setSelectedItems(newSet)
              }}
              onRestore={() => handleRestoreClick(item)}
              onDelete={() => handleDeleteClick(item)}
            />
          ))
        ) : (
          <div className="flex items-center justify-center h-64">
            <EmptyState
              icon={Trash2}
              title={items.length > 0 && pathFilter ? '没有匹配的回收站条目' : '回收站是空的'}
              description={items.length > 0 && pathFilter
                ? '当前路径筛选没有找到可恢复项目'
                : getTrashEmptyStateDescription(retentionEnabled)}
            />
          </div>
        )}
      </div>

      {/* Delete Confirmation Modal */}
      <Modal 
        isOpen={isDeleteOpen} 
        onClose={handleCloseDeleteModal}
        isDismissable={!deleteMutation.isPending}
        isKeyboardDismissDisabled={deleteMutation.isPending}
        placement="center"
        size="md"
        classNames={{
          base: "bg-content1 border border-divider shadow-xl rounded-lg",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent
          aria-labelledby="trash-delete-dialog-title"
          aria-describedby="trash-delete-dialog-description"
          aria-busy={deleteMutation.isPending}
        >
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-lg bg-danger/10 text-danger flex items-center justify-center">
              <AlertTriangle size={20} />
            </div>
            <div>
              <h3 id="trash-delete-dialog-title" className="text-lg font-semibold text-foreground">永久删除</h3>
              <p className="text-xs text-default-500 font-normal">此操作无法撤销</p>
            </div>
          </ModalHeader>
          <ModalBody id="trash-delete-dialog-description" className="px-6 py-4">
            <p className="text-foreground">确定要永久删除 <strong>{actionItem?.name}</strong> 吗？</p>
            <p className="text-xs text-default-500 mt-2">
              文件将被彻底删除，无法找回。
            </p>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button
              ref={deleteCancelButtonRef}
              autoFocus
              variant="flat"
              onPress={handleCloseDeleteModal}
              isDisabled={deleteMutation.isPending}
              className="text-default-600 rounded-lg"
            >
              取消
            </Button>
            <Button
              color="danger"
              onPress={handleConfirmDelete}
              isLoading={deleteMutation.isPending}
              className="rounded-lg"
            >
              永久删除
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      {/* Batch Restore Confirmation Modal */}
      <Modal
        isOpen={isBatchRestoreOpen}
        onClose={handleCloseBatchRestoreModal}
        placement="center"
        size="2xl"
        classNames={{
          base: "bg-content1 border border-divider shadow-xl rounded-lg",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-lg bg-success/10 text-success flex items-center justify-center">
              <RotateCcw size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">确认批量恢复</h3>
              <p className="text-xs text-default-500 font-normal">恢复到各自原始路径</p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4">
            <p className="text-foreground">确定要恢复已选择的 <strong>{batchRestoreIntent?.items.length ?? 0}</strong> 项吗？</p>
            <p className="text-xs text-default-500 mt-2">
              所选项目可能分布在多个原始目录。恢复前请确认目标路径、父目录权限和配额风险。
            </p>
            <div className="mt-4">
              <TrashBatchRestoreReview
                items={batchRestoreIntent?.items ?? []}
                trashAutoCleanupEnabled={batchRestoreIntent?.trashAutoCleanupEnabled}
              />
            </div>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button
              variant="flat"
              onPress={handleCloseBatchRestoreModal}
              isDisabled={isBatchRestoring}
              className="text-default-600 rounded-lg"
            >
              取消
            </Button>
            <Button
              color="success"
              onPress={handleConfirmBatchRestore}
              isLoading={isBatchRestoring}
              isDisabled={!batchRestoreIntent || batchRestoreIntent.items.length === 0}
              className="rounded-lg"
            >
              确认恢复
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      {/* Batch Delete Confirmation Modal */}
      <Modal
        isOpen={isBatchDeleteOpen}
        onClose={handleCloseBatchDeleteModal}
        isDismissable={!isBatchDeleting}
        isKeyboardDismissDisabled={isBatchDeleting}
        placement="center"
        size="md"
        classNames={{
          base: "bg-content1 border border-divider shadow-xl rounded-lg",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent
          aria-labelledby="trash-batch-delete-dialog-title"
          aria-describedby="trash-batch-delete-dialog-description"
          aria-busy={isBatchDeleting}
        >
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-lg bg-danger/10 text-danger flex items-center justify-center">
              <AlertTriangle size={20} />
            </div>
            <div>
              <h3 id="trash-batch-delete-dialog-title" className="text-lg font-semibold text-foreground">确认批量永久删除</h3>
              <p className="text-xs text-default-500 font-normal">此操作无法撤销</p>
            </div>
          </ModalHeader>
          <ModalBody id="trash-batch-delete-dialog-description" className="px-6 py-4">
            <p className="text-foreground">确定要永久删除已选择的 <strong>{batchDeleteIntent?.items.length ?? 0}</strong> 项吗？</p>
            <p className="text-xs text-default-500 mt-2">
              所选项目将从回收站中彻底移除，无法找回。
            </p>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button
              autoFocus
              variant="flat"
              onPress={handleCloseBatchDeleteModal}
              isDisabled={isBatchDeleting}
              className="text-default-600 rounded-lg"
            >
              取消
            </Button>
            <Button
              color="danger"
              onPress={handleBatchDelete}
              isLoading={isBatchDeleting}
              isDisabled={!batchDeleteIntent || batchDeleteIntent.items.length === 0}
              className="rounded-lg"
            >
              永久删除
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      {/* Empty Trash Confirmation Modal */}
      <Modal 
        isOpen={isEmptyOpen} 
        onClose={handleCloseEmptyModal}
        isDismissable={!emptyMutation.isPending && !isEmptyTrashReconciling}
        isKeyboardDismissDisabled={emptyMutation.isPending || isEmptyTrashReconciling}
        placement="center"
        size="md"
        classNames={{
          base: "bg-content1 border border-divider shadow-xl rounded-lg",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent
          aria-labelledby="trash-empty-dialog-title"
          aria-describedby="trash-empty-dialog-description"
          aria-busy={emptyMutation.isPending || isEmptyTrashReconciling}
        >
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-lg bg-danger/10 text-danger flex items-center justify-center">
              <AlertTriangle size={20} />
            </div>
            <div>
              <h3 id="trash-empty-dialog-title" className="text-lg font-semibold text-foreground">清空回收站</h3>
              <p className="text-xs text-default-500 font-normal">删除已确认项目</p>
            </div>
          </ModalHeader>
          <ModalBody id="trash-empty-dialog-description" className="px-6 py-4">
            <p className="text-foreground">
              {emptyReconciliationRequired ? '上次清空请求的最终结果尚未核对。' : '确定要清空回收站吗？'}
            </p>
            <p className="text-sm text-default-600 mt-2">
              {emptyReconciliationRequired
                ? `${emptyIntentItemCount} 项仍需通过最新回收站列表确认状态。`
                : `将永久删除 ${emptyIntentItemCount} 项，共 ${formatBytes(emptyIntentTotalSize)}。`}
            </p>
            <p className="text-xs text-danger mt-2 bg-danger/10 p-2 rounded-lg">
              {emptyReconciliationRequired
                ? '核对完成前不会再次发送删除请求。打开对话框后新增的项目仍不在此次操作范围内。'
                : '警告：此操作无法撤销，以上已确认项目将被彻底删除。打开对话框后新增的项目不在此次操作范围内。'}
            </p>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button
              ref={emptyCancelButtonRef}
              autoFocus
              variant="flat"
              onPress={handleCloseEmptyModal}
              isDisabled={emptyMutation.isPending || isEmptyTrashReconciling}
              className="text-default-600 rounded-lg"
            >
              {emptyReconciliationRequired ? '关闭' : '取消'}
            </Button>
            <Button
              color={emptyReconciliationRequired ? 'primary' : 'danger'}
              onPress={emptyReconciliationRequired ? handleReconcileEmptyTrash : handleConfirmEmpty}
              isLoading={emptyReconciliationRequired ? isEmptyTrashReconciling : emptyMutation.isPending}
              isDisabled={!emptyTrashIntent || emptyIntentItemCount === 0}
              aria-label={emptyReconciliationRequired ? '重新核对清空范围' : '确认清空回收站'}
              className="rounded-lg"
            >
              {emptyReconciliationRequired ? '重新核对' : '清空回收站'}
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  )
}
