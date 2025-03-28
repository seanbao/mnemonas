import { useCallback, useMemo, useState } from 'react'
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
} from 'lucide-react'
import {
  listTrash,
  restoreFromTrash,
  deleteFromTrash,
  emptyTrash,
  ApiError,
  type TrashItem,
  type TrashListResponse
} from '@/api/files'
import { FileIcon } from '@/components/ui/FileIcon'
import { EmptyState } from '@/components/ui/EmptyState'
import { formatBytes, cn, formatRelativeTime } from '@/lib/utils'
import { useBatchOperation, type BatchOperationResult } from '@/lib/useBatchOperation'
import { PageHeader } from '@/components/ui/PageHeader'
import { useCanWrite } from '@/stores/auth'

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

const trashUnavailableDescription = '文件系统当前不可用，请稍后重试'

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
    description: error instanceof Error && error.message ? error.message : '请稍后重试',
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

  return {
    title: titles.failure,
    description: error instanceof Error && error.message ? error.message : '请稍后重试',
    color: 'danger',
  }
}

function getTrashBatchActionToast(
  result: BatchOperationResult,
  titles: {
    unavailable: string
  }
) {
  if (result.succeeded === 0 && result.failedErrors.length > 0 && result.failedErrors.every((error) => {
    return error instanceof ApiError && error.isUnavailable
  })) {
    return {
      title: titles.unavailable,
      description: trashUnavailableDescription,
      color: 'warning' as const,
    }
  }

  return undefined
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
  retentionDays: number
  retentionEnabled: boolean
  canWrite: boolean
}) {
  const daysLeft = retentionEnabled ? daysUntilDelete(item.deletedAt, retentionDays) : null
  
  return (
    <div
      className={cn(
        "flex items-center gap-4 px-4 py-3 transition-all duration-200 border-b border-divider hover:bg-content2/50",
        isSelected && "bg-accent-primary/10"
      )}
    >
      {canWrite ? (
        <Checkbox
          isSelected={isSelected}
          onValueChange={onSelect}
        />
      ) : (
        <div className="w-6 shrink-0" />
      )}
      <div className="w-8 flex items-center justify-center">
        <FileIcon name={item.name} isDir={item.isDir} size={24} variant="bare" />
      </div>
      <div className="flex-1 min-w-0">
        <p className="truncate font-medium text-foreground">{item.name}</p>
        <p className="text-xs text-default-500 truncate">{item.originalPath}</p>
      </div>
      <div className="w-24 text-right text-sm text-default-500">
        {item.isDir ? '-' : formatBytes(item.size)}
      </div>
      <div className="w-32 text-right">
        <div className="text-sm text-default-500 flex items-center justify-end gap-1">
          <Clock size={12} />
          {formatRelativeTime(item.deletedAt)}
        </div>
        {daysLeft !== null && daysLeft <= 7 && (
          <Chip size="sm" variant="flat" color="warning" className="mt-1">
            {daysLeft} 天后自动删除
          </Chip>
        )}
      </div>
      <div className="w-20 flex items-center justify-end gap-1">
        {canWrite && (
          <>
            <Button
              isIconOnly
              size="sm"
              variant="light"
              color="success"
              aria-label={`恢复 ${item.name}`}
              onPress={onRestore}
              title="恢复"
              className="rounded-xl"
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
              className="rounded-xl"
            >
              <Trash2 size={16} />
            </Button>
          </>
        )}
      </div>
    </div>
  )
}

export function TrashPage() {
  const queryClient = useQueryClient()
  const canWrite = useCanWrite()
  const [selectedItems, setSelectedItems] = useState<Set<string>>(new Set())
  const [actionItem, setActionItem] = useState<TrashItem | null>(null)

  const { isOpen: isDeleteOpen, onOpen: onDeleteOpen, onClose: onDeleteClose } = useDisclosure()
  const { isOpen: isBatchDeleteOpen, onOpen: onBatchDeleteOpen, onClose: onBatchDeleteClose } = useDisclosure()
  const { isOpen: isEmptyOpen, onOpen: onEmptyOpen, onClose: onEmptyClose } = useDisclosure()

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['trash'],
    queryFn: listTrash,
  })

  const items = useMemo(() => data?.items ?? [], [data?.items])
  const visibleSelectedItems = useMemo(() => {
    if (selectedItems.size === 0) {
      return selectedItems
    }

    const validIds = new Set(items.map((item) => item.id))
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
  }, [items, selectedItems])

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
    queryClient.setQueryData<TrashListResponse>(['trash'], (current) => {
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
  }, [queryClient])

  const clearTrashCache = useCallback(() => {
    queryClient.setQueryData<TrashListResponse>(['trash'], (current) => {
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
  }, [queryClient])

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

  // Mutations
  const restoreMutation = useMutation({
    mutationFn: (id: string) => restoreFromTrash(id),
    onSuccess: (_, id) => {
      removeTrashItemsFromCache([id])
      removeSelectedIds([id])
      queryClient.invalidateQueries({ queryKey: ['trash'] })
      queryClient.invalidateQueries({ queryKey: ['files'] })
      addToast({ title: '恢复成功', color: 'success' })
    },
    onError: (error) => {
      addToast(getTrashActionErrorPresentation(error, {
        unavailable: '恢复暂不可用',
        failure: '恢复失败',
      }))
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteFromTrash(id),
    onSuccess: (_, id) => {
      removeTrashItemsFromCache([id])
      removeSelectedIds([id])
      queryClient.invalidateQueries({ queryKey: ['trash'] })
      addToast({ title: '已永久删除', color: 'success' })
      onDeleteClose()
      setActionItem((current) => current?.id === id ? null : current)
    },
    onError: (error) => {
      addToast(getTrashActionErrorPresentation(error, {
        unavailable: '永久删除暂不可用',
        failure: '删除失败',
      }))
    },
  })

  const emptyMutation = useMutation({
    mutationFn: emptyTrash,
    onSuccess: (result) => {
      clearTrashCache()
      setSelectedItems(new Set())
      queryClient.invalidateQueries({ queryKey: ['trash'] })
      if (result.partial) {
        addToast({ title: `回收站已部分清空，删除 ${result.deletedCount} 项`, color: 'warning' })
      } else {
        addToast({ title: `已清空回收站，删除 ${result.deletedCount} 项`, color: 'success' })
      }
      onEmptyClose()
    },
    onError: (error) => {
      addToast(getTrashActionErrorPresentation(error, {
        unavailable: '清空回收站暂不可用',
        failure: '清空失败',
      }))
    },
  })

  const handleSelectAll = useCallback(() => {
    if (!canWrite) return
    if (items.length === 0) return
    if (visibleSelectedItems.size === items.length) {
      setSelectedItems(new Set())
    } else {
      setSelectedItems(new Set(items.map(item => item.id)))
    }
  }, [canWrite, items, visibleSelectedItems.size])

  // Batch restore using custom hook
  const { execute: executeBatchRestore, isLoading: isBatchRestoring } = useBatchOperation({
    operation: restoreFromTrash,
    messages: {
      success: '{count} 项恢复成功',
      failure: '{count} 项恢复失败',
      partial: '{succeeded} 项恢复成功，{failed} 项失败',
    },
    getToast: (result) => getTrashBatchActionToast(result, {
      unavailable: '批量恢复暂不可用',
    }),
    onComplete: (result) => {
      removeTrashItemsFromCache(result.succeededItems as string[])
      setSelectedItems(new Set(result.failedItems as string[]))
      queryClient.invalidateQueries({ queryKey: ['trash'] })
      queryClient.invalidateQueries({ queryKey: ['files'] })
    },
  })

  const handleBatchRestore = useCallback(async () => {
    if (!canWrite) return
    const ids = Array.from(visibleSelectedItems)
    if (ids.length === 0) return
    await executeBatchRestore(ids)
  }, [canWrite, visibleSelectedItems, executeBatchRestore])

  // Batch delete using custom hook
  const { execute: executeBatchDelete, isLoading: isBatchDeleting } = useBatchOperation({
    operation: deleteFromTrash,
    messages: {
      success: '{count} 项已永久删除',
      failure: '{count} 项永久删除失败',
      partial: '{succeeded} 项永久删除成功，{failed} 项失败',
    },
    getToast: (result) => getTrashBatchActionToast(result, {
      unavailable: '批量永久删除暂不可用',
    }),
    onComplete: (result) => {
      removeTrashItemsFromCache(result.succeededItems as string[])
      setSelectedItems(new Set(result.failedItems as string[]))
      queryClient.invalidateQueries({ queryKey: ['trash'] })
    },
  })

  const handleBatchDelete = useCallback(async () => {
    if (!canWrite) return
    const ids = Array.from(visibleSelectedItems)
    if (ids.length === 0) return
    await executeBatchDelete(ids)
    onBatchDeleteClose()
  }, [canWrite, visibleSelectedItems, executeBatchDelete, onBatchDeleteClose])

  const handleDeleteClick = useCallback((item: TrashItem) => {
    if (!canWrite) return
    setActionItem(item)
    onDeleteOpen()
  }, [canWrite, onDeleteOpen])

  const handleConfirmDelete = useCallback(() => {
    if (!canWrite) return
    if (actionItem) {
      deleteMutation.mutate(actionItem.id)
    }
  }, [canWrite, actionItem, deleteMutation])

  if (isLoading) {
    return (
      <div className="p-6 lg:p-8 flex items-center justify-center h-full">
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
      <div className="h-full flex flex-col space-y-4 p-6 overflow-auto custom-scrollbar">
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
		      <Button variant="bordered" className="rounded-xl" onPress={handleRefreshTrash}>
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
  const retentionDays = data?.retentionDays
  const retentionEnabled = data?.retentionEnabled
  const retentionKnown = retentionEnabled !== undefined || retentionDays !== undefined
  const retentionLabel = !retentionKnown
    ? '自动清理设置未知'
    : retentionEnabled && retentionDays !== undefined && retentionDays > 0
      ? `${retentionDays} 天后自动清理`
      : '自动清理未启用'

  return (
    <div className="h-full flex flex-col space-y-4 p-6 overflow-auto custom-scrollbar">
      {/* Header */}
      <PageHeader
        title="回收站"
        subtitle={`${itemCount} 项 · ${formatBytes(totalSize)} · ${retentionLabel}`}
        icon={Trash2}
        actions={
          canWrite && itemCount > 0 ? (
            <Button
              color="danger"
              variant="flat"
              className="rounded-xl"
              startContent={<Trash2 size={16} />}
              onPress={onEmptyOpen}
            >
              清空回收站
            </Button>
          ) : null
        }
      />

      {/* Selection bar */}
      {canWrite && visibleSelectedItems.size > 0 && (
        <div className="flex items-center gap-4 px-4 py-2.5 bg-accent-primary/10 backdrop-blur-sm rounded-xl border border-divider shadow-[var(--shadow-soft)]">
          <div className="w-8 h-8 rounded-full bg-accent-primary/15 flex items-center justify-center">
            <span className="text-sm font-bold text-accent-primary">{visibleSelectedItems.size}</span>
          </div>
          <span className="text-sm font-medium">已选择 {visibleSelectedItems.size} 项</span>
          <div className="flex-1" />
          <Button size="sm" variant="flat" onPress={() => setSelectedItems(new Set())} className="rounded-xl">
            取消选择
          </Button>
          <Button
            size="sm"
            variant="flat"
            color="success"
            startContent={<RotateCcw size={14} />}
            onPress={handleBatchRestore}
            isLoading={isBatchRestoring}
            className="rounded-xl"
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
            className="rounded-xl"
          >
            永久删除
          </Button>
        </div>
      )}

      {/* List header */}
      {items.length > 0 && (
        <div className="flex items-center gap-4 px-4 py-2.5 bg-content2/50 backdrop-blur-sm rounded-xl border border-divider text-sm font-medium text-default-400">
          {canWrite ? (
            <Checkbox
              isSelected={visibleSelectedItems.size === items.length && items.length > 0}
              isIndeterminate={visibleSelectedItems.size > 0 && visibleSelectedItems.size < items.length}
              onValueChange={handleSelectAll}
              classNames={{
                wrapper: "before:border-divider",
              }}
            />
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
      <div className="flex-1 overflow-auto card-meridian rounded-xl">
        {items.length > 0 ? (
          items.map(item => (
            <TrashRow
              key={item.id}
              item={item}
              isSelected={visibleSelectedItems.has(item.id)}
              retentionDays={retentionDays ?? 0}
              retentionEnabled={retentionEnabled ?? false}
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
              onRestore={() => {
                if (!canWrite) return
                restoreMutation.mutate(item.id)
              }}
              onDelete={() => handleDeleteClick(item)}
            />
          ))
        ) : (
          <div className="flex items-center justify-center h-64">
            <EmptyState
              icon={Trash2}
              title="回收站是空的"
              description="删除的文件将按配置保留"
            />
          </div>
        )}
      </div>

      {/* Delete Confirmation Modal */}
      <Modal 
        isOpen={isDeleteOpen} 
        onClose={onDeleteClose}
        placement="center"
        size="md"
        classNames={{
          base: "bg-content1 border border-divider shadow-2xl rounded-2xl",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-xl bg-danger/10 text-danger flex items-center justify-center">
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
            <Button variant="flat" onPress={onDeleteClose} className="text-default-600 rounded-xl">
              取消
            </Button>
            <Button
              color="danger"
              onPress={handleConfirmDelete}
              isLoading={deleteMutation.isPending}
              className="rounded-xl"
            >
              永久删除
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      {/* Batch Delete Confirmation Modal */}
      <Modal
        isOpen={isBatchDeleteOpen}
        onClose={onBatchDeleteClose}
        placement="center"
        size="md"
        classNames={{
          base: "bg-content1 border border-divider shadow-2xl rounded-2xl",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-xl bg-danger/10 text-danger flex items-center justify-center">
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
            <Button variant="flat" onPress={onBatchDeleteClose} className="text-default-600 rounded-xl">
              取消
            </Button>
            <Button
              color="danger"
              onPress={handleBatchDelete}
              isLoading={isBatchDeleting}
              className="rounded-xl"
            >
              永久删除
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      {/* Empty Trash Confirmation Modal */}
      <Modal 
        isOpen={isEmptyOpen} 
        onClose={onEmptyClose}
        placement="center"
        size="md"
        classNames={{
          base: "bg-content1 border border-divider shadow-2xl rounded-2xl",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-xl bg-danger/10 text-danger flex items-center justify-center">
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
            <Button variant="flat" onPress={onEmptyClose} className="text-default-600 rounded-xl">
              取消
            </Button>
            <Button
              color="danger"
              onPress={() => emptyMutation.mutate()}
              isLoading={emptyMutation.isPending}
              className="rounded-xl"
            >
              清空回收站
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  )
}
