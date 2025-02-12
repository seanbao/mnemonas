import { useCallback, useState } from 'react'
import { useNavigate } from 'react-router-dom'
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
  Input,
} from '@heroui/react'
import {
  Star,
  Folder,
  ExternalLink,
  Trash2,
  Edit3,
  Clock,
} from 'lucide-react'
import {
  listFavorites,
  removeFavorite,
  updateFavoriteNote,
  type Favorite,
} from '@/api/favorites'
import { FileIcon } from '@/components/ui/FileIcon'
import { EmptyState } from '@/components/ui/EmptyState'
import { cn, formatRelativeTime } from '@/lib/utils'
import { useBatchOperation } from '@/lib/useBatchOperation'
import { PageHeader } from '@/components/ui/PageHeader'

// Get filename from path
function getFileName(path: string): string {
  const parts = path.split('/')
  return parts[parts.length - 1] || path
}

// Get parent directory from path
function getParentPath(path: string): string {
  const parts = path.split('/')
  parts.pop()
  return parts.join('/') || '/'
}

// Favorite item row
function FavoriteRow({
  item,
  isSelected,
  onSelect,
  onNavigate,
  onRemove,
  onEditNote,
}: {
  item: Favorite
  isSelected: boolean
  onSelect: () => void
  onNavigate: () => void
  onRemove: () => void
  onEditNote: () => void
}) {
  const fileName = getFileName(item.path)
  const parentPath = getParentPath(item.path)
  const isDir = item.path.endsWith('/')
  
  return (
    <div
      className={cn(
        "flex items-center gap-4 px-4 py-3 transition-all duration-200 border-b border-divider hover:bg-content2/50 group",
        isSelected && "bg-accent-primary/10"
      )}
    >
      <Checkbox
        isSelected={isSelected}
        onValueChange={onSelect}
      />
      <div className="w-8 flex items-center justify-center">
        <FileIcon name={fileName} isDir={isDir} size={24} variant="bare" />
      </div>
      <div 
        className="flex-1 min-w-0 cursor-pointer"
        onClick={onNavigate}
      >
        <p className="truncate font-medium text-foreground hover:text-accent-primary transition-colors">
          {fileName}
        </p>
        <p className="text-xs text-default-500 truncate flex items-center gap-1">
          <Folder size={10} />
          {parentPath || '/'}
        </p>
      </div>
      {item.note && (
        <div className="max-w-[200px]">
          <p className="text-sm text-default-500 truncate" title={item.note}>
            {item.note}
          </p>
        </div>
      )}
      <div className="w-32 text-right">
        <div className="text-sm text-default-500 flex items-center justify-end gap-1">
          <Clock size={12} />
          {formatRelativeTime(item.created_at)}
        </div>
      </div>
      <div className="w-24 flex items-center justify-end gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
        <Button
          isIconOnly
          size="sm"
          variant="light"
          onPress={onNavigate}
          title="跳转到文件"
          className="rounded-xl"
        >
          <ExternalLink size={16} />
        </Button>
        <Button
          isIconOnly
          size="sm"
          variant="light"
          onPress={onEditNote}
          title="编辑备注"
          className="rounded-xl"
        >
          <Edit3 size={16} />
        </Button>
        <Button
          isIconOnly
          size="sm"
          variant="light"
          color="danger"
          onPress={onRemove}
          title="取消收藏"
          className="rounded-xl"
        >
          <Trash2 size={16} />
        </Button>
      </div>
    </div>
  )
}

export function FavoritesPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [selectedItems, setSelectedItems] = useState<Set<string>>(new Set())
  const [editingItem, setEditingItem] = useState<Favorite | null>(null)
  const [noteValue, setNoteValue] = useState('')

  const { isOpen: isEditOpen, onOpen: onEditOpen, onClose: onEditClose } = useDisclosure()

  const { data: favorites = [], isLoading } = useQuery({
    queryKey: ['favorites'],
    queryFn: listFavorites,
  })

  // Remove mutation
  const removeMutation = useMutation({
    mutationFn: (path: string) => removeFavorite(path),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['favorites'] })
      addToast({ title: '已取消收藏', color: 'success' })
    },
    onError: (error) => {
      addToast({ title: '取消收藏失败', description: error.message, color: 'danger' })
    },
  })

  // Update note mutation
  const updateNoteMutation = useMutation({
    mutationFn: ({ path, note }: { path: string; note: string }) => 
      updateFavoriteNote(path, note),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['favorites'] })
      addToast({ title: '备注已更新', color: 'success' })
      onEditClose()
      setEditingItem(null)
    },
    onError: (error) => {
      addToast({ title: '更新备注失败', description: error.message, color: 'danger' })
    },
  })

  const handleSelectAll = useCallback(() => {
    if (selectedItems.size === favorites.length) {
      setSelectedItems(new Set())
    } else {
      setSelectedItems(new Set(favorites.map(item => item.path)))
    }
  }, [favorites, selectedItems.size])

  // Batch remove using custom hook
  const { execute: executeBatchRemove, isLoading: isBatchRemoving } = useBatchOperation({
    operation: removeFavorite,
    messages: {
      success: '{count} 项已取消收藏',
      failure: '{count} 项取消收藏失败',
      partial: '{succeeded} 项取消收藏成功，{failed} 项失败',
    },
    onComplete: () => {
      setSelectedItems(new Set())
      queryClient.invalidateQueries({ queryKey: ['favorites'] })
    },
  })

  const handleBatchRemove = useCallback(async () => {
    const paths = Array.from(selectedItems)
    if (paths.length === 0) return
    await executeBatchRemove(paths)
  }, [selectedItems, executeBatchRemove])

  const handleNavigate = useCallback((path: string) => {
    // Navigate to the file location in Files page
    const isDir = path.endsWith('/')
    if (isDir) {
      // Go to the folder
      navigate(`/files${encodeURI(path)}`)
    } else {
      // Go to the parent folder and highlight the file
      const parentPath = getParentPath(path)
      navigate(`/files${encodeURI(parentPath || '/')}`)
    }
  }, [navigate])

  const handleEditNote = useCallback((item: Favorite) => {
    setEditingItem(item)
    setNoteValue(item.note || '')
    onEditOpen()
  }, [onEditOpen])

  const handleSaveNote = useCallback(() => {
    if (editingItem) {
      updateNoteMutation.mutate({ path: editingItem.path, note: noteValue })
    }
  }, [editingItem, noteValue, updateNoteMutation])

  if (isLoading) {
    return (
      <div className="p-6 lg:p-8 flex items-center justify-center h-full">
        <div className="text-center">
          <div className="w-12 h-12 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-4" />
          <p className="text-default-500">加载收藏列表...</p>
        </div>
      </div>
    )
  }

  return (
    <div className="h-full flex flex-col space-y-4 p-6 overflow-auto custom-scrollbar">
      {/* Header */}
      <PageHeader
        title="收藏夹"
        subtitle={`${favorites.length} 项收藏`}
        icon={Star}
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
            color="danger"
            startContent={<Star size={14} />}
            onPress={handleBatchRemove}
            isLoading={isBatchRemoving}
            className="rounded-xl"
          >
            取消收藏
          </Button>
        </div>
      )}

      {/* List header */}
      {favorites.length > 0 && (
        <div className="flex items-center gap-4 px-4 py-2.5 bg-content2/50 backdrop-blur-sm rounded-xl border border-divider text-sm font-medium text-default-400">
          <Checkbox
            isSelected={selectedItems.size === favorites.length && favorites.length > 0}
            isIndeterminate={selectedItems.size > 0 && selectedItems.size < favorites.length}
            onValueChange={handleSelectAll}
            classNames={{
              wrapper: "before:border-divider",
            }}
          />
          <div className="w-8" />
          <div className="flex-1">名称</div>
          <div className="max-w-[200px]">备注</div>
          <div className="w-32 text-right">收藏时间</div>
          <div className="w-24" />
        </div>
      )}

      {/* Item list */}
      <div className="flex-1 overflow-auto card-meridian rounded-xl">
        {favorites.length > 0 ? (
          favorites.map(item => (
            <FavoriteRow
              key={item.path}
              item={item}
              isSelected={selectedItems.has(item.path)}
              onSelect={() => {
                const newSet = new Set(selectedItems)
                if (newSet.has(item.path)) {
                  newSet.delete(item.path)
                } else {
                  newSet.add(item.path)
                }
                setSelectedItems(newSet)
              }}
              onNavigate={() => handleNavigate(item.path)}
              onRemove={() => removeMutation.mutate(item.path)}
              onEditNote={() => handleEditNote(item)}
            />
          ))
        ) : (
          <div className="flex items-center justify-center h-64">
            <EmptyState
              icon={Star}
              title="还没有收藏"
              description="在文件页面点击星标图标收藏常用文件"
            />
          </div>
        )}
      </div>

      {/* Edit Note Modal */}
      <Modal 
        isOpen={isEditOpen} 
        onClose={onEditClose}
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
            <div className="w-10 h-10 rounded-xl bg-accent-primary/10 text-accent-primary flex items-center justify-center">
              <Edit3 size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">编辑备注</h3>
              <p className="text-xs text-default-500 font-normal truncate max-w-[300px]">
                {editingItem && getFileName(editingItem.path)}
              </p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4">
            <Input
              label="备注"
              placeholder="添加备注信息..."
              value={noteValue}
              onValueChange={setNoteValue}
              variant="bordered"
              classNames={{
                inputWrapper: "rounded-xl",
              }}
            />
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button variant="flat" onPress={onEditClose} className="text-default-600 rounded-xl">
              取消
            </Button>
            <Button
              color="primary"
              onPress={handleSaveNote}
              isLoading={updateNoteMutation.isPending}
              className="rounded-xl"
            >
              保存
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  )
}
