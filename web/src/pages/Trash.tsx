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
  X,
} from 'lucide-react'
import {
  listTrash,
  restoreFromTrash,
  deleteFromTrash,
  emptyTrash,
  ApiError,
  type ActionResult,
  type TrashItem,
  type TrashListResponse
} from '@/api/files'
import { FileIcon } from '@/components/ui/FileIcon'
import { EmptyState } from '@/components/ui/EmptyState'
import { formatBytes, cn, formatRelativeTime, normalizePath } from '@/lib/utils'
import { useBatchOperation, type BatchOperationResult } from '@/lib/useBatchOperation'
import { GENERIC_ACTION_ERROR_DESCRIPTION, GENERIC_LOAD_ERROR_DESCRIPTION, getUserFacingErrorDescription } from '@/lib/apiMessages'
import { PageHeader } from '@/components/ui/PageHeader'
import { useCanWrite, useUser } from '@/stores/auth'
import {
  getPathConflictErrorToast,
  getQuotaExceededErrorToast,
  getSharedPathConflictErrorToast,
  getSharedQuotaExceededErrorToast,
} from '@/lib/fileActionErrors'

// Calculate days until auto-delete based on retention config
function daysUntilDelete(deletedAt: string, retentionDays: number): number | null {
  if (retentionDays <= 0) {
    return null
  }
  const deleted = new Date(deletedAt)
  const autoDelete = new Date(deleted.getTime() + retentionDays * 24 * 60 * 60 * 1000)
  const now = new Date()
  return Math.max(0, Math.ceil((autoDelete.getTime() - now.getTime()) / (1000 * 60 * 60 * 24)))
}

function getAutoDeleteBadgeLabel(deletedAt: string, retentionDays: number | undefined, retentionEnabled: boolean | undefined): string | null {
  if (retentionEnabled === undefined || retentionDays === undefined) {
    return '自动清理设置未知'
  }

  if (!retentionEnabled) {
    return null
  }

  if (retentionDays === 0) {
    return '已过期，等待清理'
  }

  const daysLeft = daysUntilDelete(deletedAt, retentionDays)
  if (daysLeft === null) {
    return '已过期，等待清理'
  }
  if (daysLeft > 7) {
    return null
  }
  if (daysLeft === 0) {
    return '已过期，等待清理'
  }

  return `${daysLeft} 天后自动删除`
}

const trashUnavailableDescription = '文件系统当前不可用，请稍后重试'
const missingTrashItemTitle = '回收站条目已不存在，已同步更新'

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

function getMissingTrashItemResult(): ActionResult {
  return {
    warning: true,
    message: missingTrashItemTitle,
  }
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

function getTrashBatchActionToast(
  result: BatchOperationResult,
  titles: {
    unavailable: string
    warning: string
    partial: string
  }
) {
  if (result.failed === 0 && result.warningCount > 0) {
    return {
      title: result.warningMessages.find((message) => message === missingTrashItemTitle)
        ?? titles.warning.replace('{count}', String(result.succeeded)),
      color: 'warning' as const,
    }
  }

  if (result.succeeded === 0 && result.failedErrors.length > 0 && result.failedErrors.every((error) => {
    return error instanceof ApiError && error.isUnavailable
  })) {
    return {
      title: titles.unavailable,
      description: trashUnavailableDescription,
      color: 'warning' as const,
    }
  }

  const sharedFailureToast =
    getSharedPathConflictErrorToast(result.failedErrors)
    ?? getSharedQuotaExceededErrorToast(result.failedErrors)

  if (result.succeeded === 0 && sharedFailureToast) {
    return sharedFailureToast
  }

  if (result.succeeded > 0 && result.failed > 0 && sharedFailureToast) {
    return {
      title: titles.partial
        .replace('{succeeded}', String(result.succeeded))
        .replace('{failed}', String(result.failed)),
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
  retentionDays: number | undefined,
  retentionEnabled: boolean | undefined,
): string {
  if (items.length === 0) {
    return '尚未选择项目'
  }
  if (retentionEnabled === undefined || retentionDays === undefined) {
    return '自动清理设置未知'
  }
  if (!retentionEnabled) {
    return '自动清理未启用'
  }

  const expiredCount = items.filter((item) => getAutoDeleteBadgeLabel(item.deletedAt, retentionDays, retentionEnabled) === '已过期，等待清理').length
  if (expiredCount > 0) {
    return `${expiredCount} 项已过期，恢复前应尽快确认`
  }

  const soonCount = items.filter((item) => {
    const label = getAutoDeleteBadgeLabel(item.deletedAt, retentionDays, retentionEnabled)
    return Boolean(label && label !== '自动清理设置未知' && label !== '已过期，等待清理')
  }).length
  if (soonCount > 0) {
    return `${soonCount} 项接近自动清理窗口`
  }

  return '所选项目不在近期自动清理窗口内'
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
  retentionDays,
  retentionEnabled,
}: {
  items: TrashItem[]
  retentionDays: number | undefined
  retentionEnabled: boolean | undefined
}) {
  const fileCount = items.filter((item) => !item.isDir).length
  const directoryCount = items.filter((item) => item.isDir).length
  const totalSize = items.reduce((sum, item) => sum + item.size, 0)
  const targetDirectories = Array.from(new Set(items.map(getTrashItemParentPath))).sort()
  const visibleTargets = targetDirectories.slice(0, 4)
  const hiddenTargetCount = Math.max(0, targetDirectories.length - visibleTargets.length)

  return (
    <div aria-label="跨目录恢复执行前复核" className="rounded-lg border border-warning/20 bg-warning/10 p-3 text-sm">
      <div className="flex items-center gap-2 font-medium text-default-800">
        <AlertTriangle size={16} className="text-warning" />
        <span>跨目录恢复复核</span>
      </div>
      <div className="mt-3 grid gap-2 sm:grid-cols-2">
        <TrashRestoreReviewItem label="恢复项目" value={`${items.length} 项 · ${directoryCount} 个目录 · ${fileCount} 个文件`} />
        <TrashRestoreReviewItem label="原始目录" value={`${targetDirectories.length} 个目标目录`} />
        <TrashRestoreReviewItem label="可见数据量" value={formatBytes(totalSize)} />
        <TrashRestoreReviewItem label="自动清理" value={getBatchRestoreAutoDeleteSummary(items, retentionDays, retentionEnabled)} />
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
  retentionDays,
  retentionEnabled,
  canWrite,
}: {
  item: TrashItem
  isSelected: boolean
  onSelect: () => void
  onRestore: () => void
  onDelete: () => void
  retentionDays: number | undefined
  retentionEnabled: boolean | undefined
  canWrite: boolean
}) {
  const autoDeleteBadgeLabel = getAutoDeleteBadgeLabel(item.deletedAt, retentionDays, retentionEnabled)
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
  const actionItemRef = useRef(actionItem)
  const restoreAbortControllerRef = useRef<AbortController | null>(null)
  const deleteAbortControllerRef = useRef<AbortController | null>(null)
  const emptyAbortControllerRef = useRef<AbortController | null>(null)
  const batchRestoreAbortControllerRef = useRef<AbortController | null>(null)
  const batchDeleteAbortControllerRef = useRef<AbortController | null>(null)

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
    }
  }, [])

  const { isOpen: isDeleteOpen, onOpen: onDeleteOpen, onClose: onDeleteClose } = useDisclosure()
  const { isOpen: isBatchDeleteOpen, onOpen: onBatchDeleteOpen, onClose: onBatchDeleteClose } = useDisclosure()
  const { isOpen: isBatchRestoreOpen, onOpen: onBatchRestoreOpen, onClose: onBatchRestoreClose } = useDisclosure()
  const { isOpen: isEmptyOpen, onOpen: onEmptyOpen, onClose: onEmptyClose } = useDisclosure()

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: trashQueryKey,
    queryFn: ({ signal }) => listTrash({ signal }),
    enabled: !hasInvalidHomeDir,
  })

  const items = useMemo(() => data?.items ?? [], [data?.items])
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

  const clearTrashCache = useCallback(() => {
    queryClient.setQueryData<TrashListResponse>(trashQueryKey, (current) => {
      if (!current) {
        return current
      }

      return {
        ...current,
        items: [],
        count: 0,
        totalSize: 0,
      }
    })
  }, [queryClient, trashQueryKey])

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

  // Mutations
  const restoreMutation = useMutation({
    mutationFn: ({ id, signal }: { id: string; signal: AbortSignal }) => restoreTrashItem(id, { signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }
      const id = variables.id
      removeTrashItemsFromCache([id])
      removeSelectedIds([id])
      queryClient.invalidateQueries({ queryKey: trashQueryKey })
      queryClient.invalidateQueries({ queryKey: ['files'] })
      addToast(getTrashRestoreSuccessToast(result))
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
    mutationFn: ({ signal }: { signal: AbortSignal }) => emptyTrash({ signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }
      clearTrashCache()
      setSelectedItems(new Set())
      queryClient.invalidateQueries({ queryKey: trashQueryKey })
      if (result.partial) {
        addToast({ title: `回收站已部分清空，删除 ${result.deletedCount} 项`, color: 'warning' })
      } else if (result.warning) {
        addToast({ title: `已清空回收站，删除 ${result.deletedCount} 项，但存在警告`, color: 'warning' })
      } else {
        addToast({ title: `已清空回收站，删除 ${result.deletedCount} 项`, color: 'success' })
      }
      onEmptyClose()
    },
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) {
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
    if (emptyMutation.isPending) {
      return
    }
    onEmptyClose()
  }, [emptyMutation.isPending, onEmptyClose])

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
    operation: (id, context) => restoreTrashItem(id, { signal: context.signal }),
    messages: {
      success: '{count} 项恢复成功',
      failure: '{count} 项恢复失败',
      partial: '{succeeded} 项恢复成功，{failed} 项失败',
    },
    getWarningMessage: getTrashBatchWarningMessage,
    getToast: (result) => getTrashBatchActionToast(result, {
      unavailable: '批量恢复暂不可用',
      warning: '已恢复 {count} 项，但存在警告',
      partial: '{succeeded} 项恢复成功，{failed} 项失败',
    }),
    onComplete: (result) => {
      removeTrashItemsFromCache(result.succeededItems as string[])
      setSelectedItems(new Set(result.failedItems as string[]))
      queryClient.invalidateQueries({ queryKey: trashQueryKey })
      queryClient.invalidateQueries({ queryKey: ['files'] })
    },
  })

  const handleCloseBatchRestoreModal = useCallback(() => {
    if (isBatchRestoring) {
      return
    }
    onBatchRestoreClose()
  }, [isBatchRestoring, onBatchRestoreClose])

  const handleBatchRestoreClick = useCallback(() => {
    if (!canWrite) return
    if (visibleSelectedItems.size === 0) return
    onBatchRestoreOpen()
  }, [canWrite, onBatchRestoreOpen, visibleSelectedItems.size])

  const handleConfirmBatchRestore = useCallback(async () => {
    if (!canWrite) return
    const ids = Array.from(visibleSelectedItems)
    if (ids.length === 0) return
    batchRestoreAbortControllerRef.current?.abort()
    const controller = new AbortController()
    batchRestoreAbortControllerRef.current = controller
    try {
      await executeBatchRestore(ids, { signal: controller.signal })
      if (!controller.signal.aborted) {
        onBatchRestoreClose()
      }
    } finally {
      if (batchRestoreAbortControllerRef.current === controller) {
        batchRestoreAbortControllerRef.current = null
      }
    }
  }, [canWrite, executeBatchRestore, onBatchRestoreClose, visibleSelectedItems])

  // Batch delete using custom hook
  const { execute: executeBatchDelete, isLoading: isBatchDeleting } = useBatchOperation<string, ActionResult>({
    operation: (id, context) => deleteTrashItem(id, { signal: context.signal }),
    messages: {
      success: '{count} 项已永久删除',
      failure: '{count} 项永久删除失败',
      partial: '{succeeded} 项永久删除成功，{failed} 项失败',
    },
    getWarningMessage: getTrashBatchWarningMessage,
    getToast: (result) => getTrashBatchActionToast(result, {
      unavailable: '批量永久删除暂不可用',
      warning: '已永久删除 {count} 项，但存在警告',
      partial: '{succeeded} 项永久删除成功，{failed} 项失败',
    }),
    onComplete: (result) => {
      removeTrashItemsFromCache(result.succeededItems as string[])
      setSelectedItems(new Set(result.failedItems as string[]))
      queryClient.invalidateQueries({ queryKey: trashQueryKey })
    },
  })

  const handleCloseBatchDeleteModal = useCallback(() => {
    if (isBatchDeleting) {
      return
    }
    onBatchDeleteClose()
  }, [isBatchDeleting, onBatchDeleteClose])

  const handleBatchDelete = useCallback(async () => {
    if (!canWrite) return
    const ids = Array.from(visibleSelectedItems)
    if (ids.length === 0) return
    batchDeleteAbortControllerRef.current?.abort()
    const controller = new AbortController()
    batchDeleteAbortControllerRef.current = controller
    try {
      await executeBatchDelete(ids, { signal: controller.signal })
      if (!controller.signal.aborted) {
        onBatchDeleteClose()
      }
    } finally {
      if (batchDeleteAbortControllerRef.current === controller) {
        batchDeleteAbortControllerRef.current = null
      }
    }
  }, [canWrite, visibleSelectedItems, executeBatchDelete, onBatchDeleteClose])

  const handleDeleteClick = useCallback((item: TrashItem) => {
    if (!canWrite) return
    setActionItem(item)
    onDeleteOpen()
  }, [canWrite, onDeleteOpen])

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
    emptyAbortControllerRef.current?.abort()
    const controller = new AbortController()
    emptyAbortControllerRef.current = controller
    emptyMutation.mutate({ signal: controller.signal })
  }, [canWrite, emptyMutation])

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
          <p className="text-default-500">加载回收站...</p>
        </div>
      </div>
    )
  }

  if (error) {
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
  const visibleItemCount = visibleItems.length
  const retentionDays = data?.retentionDays
  const retentionEnabled = data?.retentionEnabled
  const retentionKnown = retentionEnabled !== undefined || retentionDays !== undefined
  const retentionLabel = !retentionKnown
    ? '自动清理设置未知'
    : retentionEnabled && retentionDays !== undefined
      ? retentionDays <= 0
        ? '立即过期，等待清理'
        : `${retentionDays} 天后自动清理`
      : '自动清理未启用'
  const pageSubtitle = pathFilter
    ? `当前筛选 ${visibleItemCount} 项 / 共 ${itemCount} 项 · ${formatBytes(totalSize)} · ${retentionLabel}`
    : `${itemCount} 项 · ${formatBytes(totalSize)} · ${retentionLabel}`

  return (
    <div className="flex h-full min-h-0 flex-col space-y-4 overflow-auto p-4 custom-scrollbar sm:p-6">
      {/* Header */}
      <PageHeader
        title="回收站"
        subtitle={pageSubtitle}
        icon={Trash2}
        actions={
          canWrite && itemCount > 0 ? (
            <Button
              color="danger"
              variant="flat"
              className="rounded-lg"
              startContent={<Trash2 size={16} />}
              onPress={onEmptyOpen}
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
            onPress={onBatchDeleteOpen}
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
              retentionDays={retentionDays}
              retentionEnabled={retentionEnabled}
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
              description={items.length > 0 && pathFilter ? '当前路径筛选没有找到可恢复项目' : '删除的文件将按配置保留'}
            />
          </div>
        )}
      </div>

      {/* Delete Confirmation Modal */}
      <Modal 
        isOpen={isDeleteOpen} 
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
              <AlertTriangle size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">永久删除</h3>
              <p className="text-xs text-default-500 font-normal">此操作无法撤销</p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4">
            <p className="text-foreground">确定要永久删除 <strong>{actionItem?.name}</strong> 吗？</p>
            <p className="text-xs text-default-500 mt-2">
              文件将被彻底删除，无法找回。
            </p>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button
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
            <p className="text-foreground">确定要恢复已选择的 <strong>{selectedTrashItems.length}</strong> 项吗？</p>
            <p className="text-xs text-default-500 mt-2">
              所选项目可能分布在多个原始目录。恢复前请确认目标路径、父目录权限和配额风险。
            </p>
            <div className="mt-4">
              <TrashBatchRestoreReview
                items={selectedTrashItems}
                retentionDays={retentionDays}
                retentionEnabled={retentionEnabled}
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
              isDisabled={selectedTrashItems.length === 0}
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
              <AlertTriangle size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">确认批量永久删除</h3>
              <p className="text-xs text-default-500 font-normal">此操作无法撤销</p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4">
            <p className="text-foreground">确定要永久删除已选择的 <strong>{visibleSelectedItems.size}</strong> 项吗？</p>
            <p className="text-xs text-default-500 mt-2">
              所选项目将从回收站中彻底移除，无法找回。
            </p>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button
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
              <AlertTriangle size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">清空回收站</h3>
              <p className="text-xs text-default-500 font-normal">删除所有文件</p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4">
            <p className="text-foreground">确定要清空回收站吗？</p>
            <p className="text-sm text-default-600 mt-2">
              将永久删除 {itemCount} 项，共 {formatBytes(totalSize)}。
            </p>
            <p className="text-xs text-danger mt-2 bg-danger/10 p-2 rounded-lg">
              警告：此操作无法撤销，所有文件将被彻底删除。
            </p>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button
              variant="flat"
              onPress={handleCloseEmptyModal}
              isDisabled={emptyMutation.isPending}
              className="text-default-600 rounded-lg"
            >
              取消
            </Button>
            <Button
              color="danger"
              onPress={handleConfirmEmpty}
              isLoading={emptyMutation.isPending}
              aria-label="确认清空回收站"
              className="rounded-lg"
            >
              清空回收站
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  )
}
