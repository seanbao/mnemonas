import { useCallback, useState } from 'react'
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
  type TrashItem
} from '@/api/files'
import { FileIcon } from '@/components/ui/FileIcon'
import { EmptyState } from '@/components/ui/EmptyState'
import { formatBytes, cn, formatRelativeTime } from '@/lib/utils'
import { useBatchOperation } from '@/lib/useBatchOperation'
import { PageHeader } from '@/components/ui/PageHeader'

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

// Trash item row
function TrashRow({
  item,
  isSelected,
  onSelect,
  onRestore,
  onDelete,
  retentionDays,
  retentionEnabled
}: {
  item: TrashItem
  isSelected: boolean
  onSelect: () => void
  onRestore: () => void
  onDelete: () => void
  retentionDays: number
  retentionEnabled: boolean
}) {
  const daysLeft = retentionEnabled ? daysUntilDelete(item.deletedAt, retentionDays) : null
  
  return (
    <div
      className={cn(
        "flex items-center gap-4 px-4 py-3 transition-all duration-200 border-b border-divider hover:bg-content2/50",
        isSelected && "bg-accent-primary/10"
      )}
    >
      <Checkbox
        isSelected={isSelected}
        onValueChange={onSelect}
      />
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
        <Button
          isIconOnly
          size="sm"
          variant="light"
          color="success"
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
          onPress={onDelete}
          title="永久删除"
          className="rounded-xl"
        >
          <Trash2 size={16} />
        </Button>
      </div>
    </div>
  )
}

export function TrashPage() {
  const queryClient = useQueryClient()
  const [selectedItems, setSelectedItems] = useState<Set<string>>(new Set())
  const [actionItem, setActionItem] = useState<TrashItem | null>(null)

  const { isOpen: isDeleteOpen, onOpen: onDeleteOpen, onClose: onDeleteClose } = useDisclosure()
  const { isOpen: isBatchDeleteOpen, onOpen: onBatchDeleteOpen, onClose: onBatchDeleteClose } = useDisclosure()
  const { isOpen: isEmptyOpen, onOpen: onEmptyOpen, onClose: onEmptyClose } = useDisclosure()

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['trash'],
    queryFn: listTrash,
  })

  // Mutations
  const restoreMutation = useMutation({
    mutationFn: (id: string) => restoreFromTrash(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['trash'] })
      queryClient.invalidateQueries({ queryKey: ['files'] })
      addToast({ title: '恢复成功', color: 'success' })
    },
    onError: (error) => {
      addToast({ title: '恢复失败', description: error.message, color: 'danger' })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteFromTrash(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['trash'] })
      addToast({ title: '已永久删除', color: 'success' })
      onDeleteClose()
      setActionItem(null)
    },
    onError: (error) => {
      addToast({ title: '删除失败', description: error.message, color: 'danger' })
    },
  })

  const emptyMutation = useMutation({
    mutationFn: emptyTrash,
    onSuccess: (count) => {
      queryClient.invalidateQueries({ queryKey: ['trash'] })
      addToast({ title: `已清空回收站，删除 ${count} 项`, color: 'success' })
      onEmptyClose()
    },
    onError: (error) => {
      addToast({ title: '清空失败', description: error.message, color: 'danger' })
    },
  })

  const handleSelectAll = useCallback(() => {
    if (!data?.items) return
    if (selectedItems.size === data.items.length) {
      setSelectedItems(new Set())
    } else {
      setSelectedItems(new Set(data.items.map(item => item.id)))
    }
  }, [data, selectedItems.size])

  // Batch restore using custom hook
  const { execute: executeBatchRestore, isLoading: isBatchRestoring } = useBatchOperation({
    operation: restoreFromTrash,
    messages: {
      success: '{count} 项恢复成功',
      failure: '{count} 项恢复失败',
      partial: '{succeeded} 项恢复成功，{failed} 项失败',
    },
    onComplete: (result) => {
      setSelectedItems(new Set(result.failedItems as string[]))
      queryClient.invalidateQueries({ queryKey: ['trash'] })
      queryClient.invalidateQueries({ queryKey: ['files'] })
    },
  })

  const handleBatchRestore = useCallback(async () => {
    const ids = Array.from(selectedItems)
    if (ids.length === 0) return
    await executeBatchRestore(ids)
  }, [selectedItems, executeBatchRestore])

  // Batch delete using custom hook
  const { execute: executeBatchDelete, isLoading: isBatchDeleting } = useBatchOperation({
    operation: deleteFromTrash,
    messages: {
      success: '{count} 项已永久删除',
      failure: '{count} 项永久删除失败',
      partial: '{succeeded} 项永久删除成功，{failed} 项失败',
    },
    onComplete: (result) => {
      setSelectedItems(new Set(result.failedItems as string[]))
      queryClient.invalidateQueries({ queryKey: ['trash'] })
    },
  })

  const handleBatchDelete = useCallback(async () => {
    const ids = Array.from(selectedItems)
    if (ids.length === 0) return
    await executeBatchDelete(ids)
    onBatchDeleteClose()
  }, [selectedItems, executeBatchDelete, onBatchDeleteClose])

  const handleDeleteClick = useCallback((item: TrashItem) => {
    setActionItem(item)
    onDeleteOpen()
  }, [onDeleteOpen])

  const handleConfirmDelete = useCallback(() => {
    if (actionItem) {
      deleteMutation.mutate(actionItem.id)
    }
  }, [actionItem, deleteMutation])

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
    return (
      <div className="h-full flex flex-col space-y-4 p-6 overflow-auto custom-scrollbar">
        <PageHeader
          title="回收站"
          subtitle="加载失败"
          icon={Trash2}
        />
        <div className="flex flex-1 items-center justify-center">
          <EmptyState
            icon={AlertCircle}
            title="加载回收站失败"
            description={(error as Error).message || '请稍后重试'}
            action={
              <Button variant="bordered" className="rounded-xl" onPress={() => refetch()}>
                重新加载
              </Button>
            }
          />
        </div>
      </div>
    )
  }

  const items = data?.items ?? []
  const totalSize = data?.totalSize ?? 0
  const itemCount = data?.count ?? 0
  const retentionDays = data?.retentionDays ?? 0
  const retentionEnabled = data?.retentionEnabled ?? retentionDays > 0
  const retentionLabel = retentionEnabled && retentionDays > 0
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
          itemCount > 0 ? (
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
      {selectedItems.size > 0 && (
        <div className="flex items-center gap-4 px-4 py-2.5 bg-accent-primary/10 backdrop-blur-sm rounded-xl border border-divider shadow-[var(--shadow-soft)]">
          <div className="w-8 h-8 rounded-full bg-accent-primary/15 flex items-center justify-center">
            <span className="text-sm font-bold text-accent-primary">{selectedItems.size}</span>
          </div>
          <span className="text-sm font-medium">已选择 {selectedItems.size} 项</span>
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
          <Checkbox
            isSelected={selectedItems.size === items.length && items.length > 0}
            isIndeterminate={selectedItems.size > 0 && selectedItems.size < items.length}
            onValueChange={handleSelectAll}
            classNames={{
              wrapper: "before:border-divider",
            }}
          />
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
              isSelected={selectedItems.has(item.id)}
              retentionDays={retentionDays}
              retentionEnabled={retentionEnabled}
              onSelect={() => {
                const newSet = new Set(selectedItems)
                if (newSet.has(item.id)) {
                  newSet.delete(item.id)
                } else {
                  newSet.add(item.id)
                }
                setSelectedItems(newSet)
              }}
              onRestore={() => restoreMutation.mutate(item.id)}
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
            <p className="text-foreground">确定要永久删除已选择的 <strong>{selectedItems.size}</strong> 项吗？</p>
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
