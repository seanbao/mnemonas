import { getInvalidHomeDirDescription, invalidHomeDirTitle, resolveUserHomeScope } from '@/lib/userScope'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
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
  AlertCircle,
} from 'lucide-react'
import {
  listFavorites,
  removeFavorite,
  updateFavoriteNote,
  FavoritesError,
  type FavoritesActionResult,
  type Favorite,
} from '@/api/favorites'
import { FileIcon } from '@/components/ui/FileIcon'
import { EmptyState } from '@/components/ui/EmptyState'
import { FavoritesSettings } from '@/components/favorites/FavoritesSettings'
import { cn, encodePathForUrl, formatRelativeTime } from '@/lib/utils'
import { useBatchOperation, type BatchOperationResult } from '@/lib/useBatchOperation'
import { PageHeader } from '@/components/ui/PageHeader'
import { useCanWrite, useUser } from '@/stores/auth'
import { GENERIC_LOAD_ERROR_DESCRIPTION, getUserFacingErrorDescription } from '@/lib/apiMessages'

// Get filename from path
function getFileName(path: string): string {
  const normalizedPath = path.endsWith('/') && path !== '/' ? path.slice(0, -1) : path
  const parts = normalizedPath.split('/')
  return parts[parts.length - 1] || normalizedPath
}

function getFavoritesFeatureState(error: unknown): 'disabled' | 'unavailable' | null {
  if (!(error instanceof FavoritesError)) {
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

function getFavoritesActionErrorPresentation(error: unknown): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  if (error instanceof FavoritesError) {
    if (error.isFeatureDisabled) {
      return {
        title: '收藏功能已关闭',
        description: '当前服务已关闭收藏功能。如需使用，请在设置中重新启用。',
        color: 'warning',
      }
    }

    if (error.isUnavailable) {
      return {
        title: '收藏功能暂不可用',
        description: '收藏存储未成功初始化，请检查设备状态或稍后重试。',
        color: 'warning',
      }
    }
  }

  return {
    title: '操作失败',
    description: getUserFacingErrorDescription(error),
    color: 'danger',
  }
}

function getFavoritesActionSuccessToast(action: 'remove' | 'update-note', result: FavoritesActionResult): {
  title: string
  color: 'success' | 'warning'
} {
  if (action === 'remove') {
    if (result.warning) {
      return { title: '已取消收藏，但存在警告', color: 'warning' }
    }

    return { title: '已取消收藏', color: 'success' }
  }

  if (result.warning) {
    return { title: '备注已更新，但存在警告', color: 'warning' }
  }

  return { title: '备注已更新', color: 'success' }
}

function getFavoritesRefreshErrorPresentation(error: unknown): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  if (error instanceof FavoritesError) {
    if (error.isFeatureDisabled) {
      return {
        title: '收藏功能已关闭',
        description: '当前服务已关闭收藏功能。如需使用，请在设置中重新启用。',
        color: 'warning',
      }
    }

    if (error.isUnavailable) {
      return {
        title: '收藏功能暂不可用',
        description: '收藏存储未成功初始化，请检查设备状态或稍后重试。',
        color: 'warning',
      }
    }
  }

  return {
    title: '刷新失败',
    description: getUserFacingErrorDescription(error),
    color: 'danger',
  }
}

function getFavoritesBatchActionToast(result: BatchOperationResult) {
  if (result.succeeded !== 0 || result.failedErrors.length === 0) {
    return undefined
  }

  if (result.failedErrors.every((error) => error instanceof FavoritesError && error.isFeatureDisabled)) {
    return {
      title: '收藏功能已关闭',
      description: '当前服务已关闭收藏功能。如需使用，请在设置中重新启用。',
      color: 'warning' as const,
    }
  }

  if (result.failedErrors.every((error) => error instanceof FavoritesError && error.isUnavailable)) {
    return {
      title: '收藏功能暂不可用',
      description: '收藏存储未成功初始化，请检查设备状态或稍后重试。',
      color: 'warning' as const,
    }
  }

  return undefined
}

function getMissingFavoriteToast(): {
  title: string
  description: string
  color: 'warning'
} {
  return {
    title: '收藏已不存在',
    description: '该收藏可能已被其他操作移除，列表已同步更新。',
    color: 'warning',
  }
}

function isAbortError(error: unknown): boolean {
  return typeof error === 'object'
    && error !== null
    && 'name' in error
    && (error as { name?: unknown }).name === 'AbortError'
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
  canWrite,
  onSelect,
  onNavigate,
  onRemove,
  onEditNote,
}: {
  item: Favorite
  isSelected: boolean
  canWrite: boolean
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
        "group flex flex-wrap items-start gap-x-3 gap-y-2 border-b border-divider px-4 py-4 transition-all duration-200 hover:bg-content2/50 sm:flex-nowrap sm:items-center sm:gap-4 sm:py-3",
        isSelected && "bg-accent-primary/10"
      )}
    >
      {canWrite ? (
        <Checkbox
          aria-label={`选择收藏 ${item.path}`}
          isSelected={isSelected}
          onValueChange={onSelect}
        />
      ) : (
        <div className="w-6 shrink-0" />
      )}
      <div className="w-8 flex items-center justify-center">
        <FileIcon name={fileName} isDir={isDir} size={24} variant="bare" />
      </div>
      <button
        type="button"
        className="min-w-0 flex-1 basis-[calc(100%-6rem)] text-left focus:outline-none sm:basis-auto"
        onClick={onNavigate}
        aria-label={`${isDir ? '打开文件夹' : '打开文件'} ${item.path}`}
      >
        <p className="truncate font-medium text-foreground hover:text-accent-primary transition-colors">
          {fileName}
        </p>
        <p className="text-xs text-default-500 truncate flex items-center gap-1">
          <Folder size={10} />
          {parentPath || '/'}
        </p>
      </button>
      {item.note && (
        <div className="ml-14 max-w-[calc(100%-3.5rem)] basis-full sm:ml-0 sm:max-w-[200px] sm:basis-auto">
          <p className="text-sm text-default-500 truncate" title={item.note}>
            {item.note}
          </p>
        </div>
      )}
      <div className="ml-14 text-left sm:ml-0 sm:w-32 sm:text-right">
        <div className="flex items-center gap-1 text-sm text-default-500 sm:justify-end">
          <Clock size={12} />
          {formatRelativeTime(item.created_at)}
        </div>
      </div>
      <div className="ml-auto flex shrink-0 items-center justify-end gap-1 opacity-100 transition-opacity sm:w-24 sm:opacity-0 sm:group-hover:opacity-100">
        <Button
          isIconOnly
          size="sm"
          variant="light"
          onPress={onNavigate}
          aria-label={`跳转到 ${item.path}`}
          title="跳转到文件"
          className="rounded-lg"
        >
          <ExternalLink size={16} />
        </Button>
        {canWrite && (
          <Button
            isIconOnly
            size="sm"
            variant="light"
            onPress={onEditNote}
            aria-label={`编辑备注 ${item.path}`}
            title="编辑备注"
            className="rounded-lg"
          >
            <Edit3 size={16} />
          </Button>
        )}
        {canWrite && (
          <Button
            isIconOnly
            size="sm"
            variant="light"
            color="danger"
            onPress={onRemove}
            aria-label={`取消收藏 ${item.path}`}
            title="取消收藏"
            className="rounded-lg"
          >
            <Trash2 size={16} />
          </Button>
        )}
      </div>
    </div>
  )
}

export function FavoritesPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const canWrite = useCanWrite()
  const user = useUser()
  const isAdmin = user?.role === 'admin'
  const { scopedHomeDir, hasInvalidHomeDir } = resolveUserHomeScope(user)
  const authScopeKey = user?.id ?? 'anonymous'
  const favoritesScopeKey = `${authScopeKey}:${hasInvalidHomeDir ? '__invalid__' : (scopedHomeDir ?? '/')}`
  const favoritesQueryKey = useMemo(() => ['favorites', favoritesScopeKey] as const, [favoritesScopeKey])
  const [selectedItems, setSelectedItems] = useState<Set<string>>(new Set())
  const [editingItem, setEditingItem] = useState<Favorite | null>(null)
  const [noteValue, setNoteValue] = useState('')
  const editSessionRef = useRef(0)
  const editingItemRef = useRef(editingItem)
  const noteValueRef = useRef(noteValue)
  const removeAbortControllerRef = useRef<AbortController | null>(null)
  const updateNoteAbortControllerRef = useRef<AbortController | null>(null)
  const batchRemoveAbortControllerRef = useRef<AbortController | null>(null)

  useEffect(() => () => {
    removeAbortControllerRef.current?.abort()
    removeAbortControllerRef.current = null
    updateNoteAbortControllerRef.current?.abort()
    updateNoteAbortControllerRef.current = null
    batchRemoveAbortControllerRef.current?.abort()
    batchRemoveAbortControllerRef.current = null
  }, [])

  useLayoutEffect(() => {
    editingItemRef.current = editingItem
  }, [editingItem])

  useLayoutEffect(() => {
    noteValueRef.current = noteValue
  }, [noteValue])

  const { isOpen: isEditOpen, onOpen: onEditOpen, onClose: onEditClose } = useDisclosure()

  const { data: favorites, isLoading, error, refetch } = useQuery({
    queryKey: favoritesQueryKey,
    queryFn: ({ signal }) => listFavorites({ signal }),
    enabled: !hasInvalidHomeDir,
  })

  const favoriteItems = useMemo(() => favorites ?? [], [favorites])
  const visibleSelectedItems = useMemo(() => {
    if (selectedItems.size === 0) {
      return selectedItems
    }

    const validPaths = new Set(favoriteItems.map((item) => item.path))
    let changed = false
    const next = new Set<string>()

    for (const path of selectedItems) {
      if (validPaths.has(path)) {
        next.add(path)
        continue
      }
      changed = true
    }

    return changed ? next : selectedItems
  }, [favoriteItems, selectedItems])
  const featureState = getFavoritesFeatureState(error)

  const removeSelectedPaths = useCallback((paths: string[]) => {
    if (paths.length === 0) {
      return
    }

    const removedPaths = new Set(paths)
    setSelectedItems((prev) => {
      if (prev.size === 0) {
        return prev
      }

      let changed = false
      const next = new Set<string>()
      for (const path of prev) {
        if (removedPaths.has(path)) {
          changed = true
          continue
        }
        next.add(path)
      }

      return changed ? next : prev
    })
  }, [])

  const removeFavoritesFromCache = useCallback((paths: string[]) => {
    if (paths.length === 0) {
      return
    }

    const removedPaths = new Set(paths)
    queryClient.setQueryData<Favorite[]>(favoritesQueryKey, (current) => {
      if (!current) {
        return current
      }

      const next = current.filter((item) => !removedPaths.has(item.path))
      return next.length === current.length ? current : next
    })
  }, [favoritesQueryKey, queryClient])

  // Remove mutation
  const removeMutation = useMutation({
    mutationFn: ({ path, signal }: { path: string; signal: AbortSignal }) => removeFavorite(path, { signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }

      removeFavoritesFromCache([variables.path])
      removeSelectedPaths([variables.path])
      queryClient.invalidateQueries({ queryKey: favoritesQueryKey })
      addToast(getFavoritesActionSuccessToast('remove', result))
    },
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) {
        return
      }

      if (error instanceof FavoritesError && error.isNotFound) {
        removeFavoritesFromCache([variables.path])
        removeSelectedPaths([variables.path])
        addToast(getMissingFavoriteToast())
        return
      }

      addToast(getFavoritesActionErrorPresentation(error))
    },
    onSettled: (_result, _error, variables) => {
      if (removeAbortControllerRef.current?.signal === variables?.signal) {
        removeAbortControllerRef.current = null
      }
    },
  })

  // Update note mutation
  const updateNoteMutation = useMutation({
    mutationFn: ({ path, note, signal }: { path: string; note: string; editSession: number; signal: AbortSignal }) =>
      updateFavoriteNote(path, note, { signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }

      queryClient.invalidateQueries({ queryKey: favoritesQueryKey })
      addToast(getFavoritesActionSuccessToast('update-note', result))
      if (
        editSessionRef.current === variables.editSession
        && editingItemRef.current?.path === variables.path
        && noteValueRef.current === variables.note
      ) {
        onEditClose()
        setEditingItem(null)
      }
    },
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) {
        return
      }

      if (error instanceof FavoritesError && error.isNotFound) {
        removeFavoritesFromCache([variables.path])
        removeSelectedPaths([variables.path])
        if (editingItemRef.current?.path === variables.path) {
          onEditClose()
          setEditingItem(null)
        }
        addToast(getMissingFavoriteToast())
        return
      }

      addToast(getFavoritesActionErrorPresentation(error))
    },
    onSettled: (_result, _error, variables) => {
      if (updateNoteAbortControllerRef.current?.signal === variables?.signal) {
        updateNoteAbortControllerRef.current = null
      }
    },
  })

  const handleSelectAll = useCallback(() => {
    if (!canWrite) return
    if (visibleSelectedItems.size === favoriteItems.length) {
      setSelectedItems(new Set())
    } else {
      setSelectedItems(new Set(favoriteItems.map(item => item.path)))
    }
  }, [canWrite, favoriteItems, visibleSelectedItems.size])

  const handleRemoveFavorite = useCallback((path: string) => {
    removeAbortControllerRef.current?.abort()
    const controller = new AbortController()
    removeAbortControllerRef.current = controller
    removeMutation.mutate({ path, signal: controller.signal })
  }, [removeMutation])

  const batchRemoveFavorite = useCallback(async (path: string, options?: { signal?: AbortSignal }) => {
    try {
      return await removeFavorite(path, options?.signal ? { signal: options.signal } : undefined)
    } catch (error) {
      if (error instanceof FavoritesError && error.isNotFound) {
        return {
          warning: true,
          message: '收藏已不存在，已同步更新',
        }
      }

      throw error
    }
  }, [])

  // Batch remove using custom hook
  const { execute: executeBatchRemove, isLoading: isBatchRemoving } = useBatchOperation({
    operation: batchRemoveFavorite,
    messages: {
      success: '{count} 项已取消收藏',
      failure: '{count} 项取消收藏失败',
      partial: '{succeeded} 项取消收藏成功，{failed} 项失败',
    },
    getToast: getFavoritesBatchActionToast,
    onComplete: (result) => {
      const succeededPaths = result.succeededItems.filter((item): item is string => typeof item === 'string')
      const failedPaths = result.failedItems.filter((item): item is string => typeof item === 'string')
      removeFavoritesFromCache(succeededPaths)
      setSelectedItems(new Set(failedPaths))
      queryClient.invalidateQueries({ queryKey: favoritesQueryKey })
    },
  })

  const handleBatchRemove = useCallback(async () => {
    if (!canWrite) return
    const paths = Array.from(visibleSelectedItems)
    if (paths.length === 0) return
    batchRemoveAbortControllerRef.current?.abort()
    const controller = new AbortController()
    batchRemoveAbortControllerRef.current = controller
    try {
      await executeBatchRemove(paths, { signal: controller.signal })
    } finally {
      if (batchRemoveAbortControllerRef.current === controller) {
        batchRemoveAbortControllerRef.current = null
      }
    }
  }, [canWrite, visibleSelectedItems, executeBatchRemove])

  const handleNavigate = useCallback((path: string) => {
    // Navigate to the file location in Files page
    const isDir = path.endsWith('/')
    if (isDir) {
      // Go to the folder
      navigate(`/files${encodePathForUrl(path)}`)
    } else {
      // Go to the parent folder and highlight the file
      const parentPath = getParentPath(path)
      navigate(`/files${encodePathForUrl(parentPath || '/')}`, { state: { highlightPath: path } })
    }
  }, [navigate])

  const handleEditNote = useCallback((item: Favorite) => {
    if (!canWrite) return
    editSessionRef.current += 1
    setEditingItem(item)
    setNoteValue(item.note || '')
    onEditOpen()
  }, [canWrite, onEditOpen])

  const handleCloseEditModal = useCallback(() => {
    if (updateNoteMutation.isPending) return
    editSessionRef.current += 1
    onEditClose()
    setEditingItem(null)
  }, [onEditClose, updateNoteMutation.isPending])

  const handleSaveNote = useCallback(() => {
    if (!canWrite) return
    if (editingItem) {
      updateNoteAbortControllerRef.current?.abort()
      const controller = new AbortController()
      updateNoteAbortControllerRef.current = controller
      updateNoteMutation.mutate({
        path: editingItem.path,
        note: noteValue,
        editSession: editSessionRef.current,
        signal: controller.signal,
      })
    }
  }, [canWrite, editingItem, noteValue, updateNoteMutation])

  const handleRefreshFavorites = useCallback(async () => {
  const result = await refetch()
  if (result.error) {
    addToast(getFavoritesRefreshErrorPresentation(result.error))
    return
  }
  addToast({ title: '收藏夹已刷新', color: 'success' })
  }, [refetch])

  if (hasInvalidHomeDir) {
    return (
      <div className="flex h-full min-h-0 flex-col space-y-4 overflow-auto p-4 custom-scrollbar sm:p-6">
        <PageHeader
          title="收藏夹"
          subtitle={invalidHomeDirTitle}
          icon={Star}
        />
        {isAdmin && <FavoritesSettings />}
        <div className="flex flex-1 items-center justify-center">
          <EmptyState
            icon={AlertCircle}
            title={invalidHomeDirTitle}
            description={getInvalidHomeDirDescription('查看收藏')}
          />
        </div>
      </div>
    )
  }

  if (isLoading) {
    return (
      <div className="flex h-full min-h-0 flex-col space-y-4 overflow-auto p-4 custom-scrollbar sm:p-6">
        <PageHeader
          title="收藏夹"
          subtitle="正在加载"
          icon={Star}
        />
        {isAdmin && <FavoritesSettings />}
        <div className="flex flex-1 items-center justify-center">
          <div className="text-center">
            <div className="w-12 h-12 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-4" />
            <p className="text-default-500">加载收藏列表…</p>
          </div>
        </div>
      </div>
    )
  }

  if (error) {
    if (featureState === 'disabled') {
      return (
        <div className="flex h-full min-h-0 flex-col space-y-4 overflow-auto p-4 custom-scrollbar sm:p-6">
          <PageHeader
            title="收藏夹"
            subtitle="功能已关闭"
            icon={Star}
          />
          {isAdmin && <FavoritesSettings />}
          <div className="flex flex-1 items-center justify-center">
            <EmptyState
              icon={Star}
              title="收藏功能已关闭"
              description="当前服务已关闭收藏功能。如需使用，请在设置中重新启用。"
            />
          </div>
        </div>
      )
    }

    if (featureState === 'unavailable') {
      return (
        <div className="flex h-full min-h-0 flex-col space-y-4 overflow-auto p-4 custom-scrollbar sm:p-6">
          <PageHeader
            title="收藏夹"
            subtitle="暂不可用"
            icon={Star}
          />
          {isAdmin && <FavoritesSettings />}
          <div className="flex flex-1 items-center justify-center">
            <EmptyState
              icon={AlertCircle}
              title="收藏功能暂不可用"
              description="收藏存储未成功初始化，请检查设备状态或稍后重试。"
              action={
                <Button variant="bordered" className="rounded-lg" onPress={handleRefreshFavorites}>
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
          title="收藏夹"
          subtitle="加载失败"
          icon={Star}
        />
        {isAdmin && <FavoritesSettings />}
        <div className="flex flex-1 items-center justify-center">
          <EmptyState
            icon={AlertCircle}
            title="加载收藏列表失败"
            description={getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION)}
            action={
              <Button variant="bordered" className="rounded-lg" onPress={handleRefreshFavorites}>
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
        title="收藏夹"
        subtitle={`${favoriteItems.length} 项收藏`}
        icon={Star}
      />

      {isAdmin && <FavoritesSettings />}

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
            color="danger"
            startContent={<Star size={14} />}
            onPress={handleBatchRemove}
            isLoading={isBatchRemoving}
            className="rounded-lg"
          >
            取消收藏
          </Button>
        </div>
      )}

      {/* List header */}
      {favoriteItems.length > 0 && (
        <div className="hidden items-center gap-4 rounded-lg border border-divider bg-content2/50 px-4 py-2.5 text-sm font-medium text-default-400 sm:flex">
          {canWrite ? (
            <Checkbox
              aria-label="选择全部收藏"
              isSelected={visibleSelectedItems.size === favoriteItems.length && favoriteItems.length > 0}
              isIndeterminate={visibleSelectedItems.size > 0 && visibleSelectedItems.size < favoriteItems.length}
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
          <div className="max-w-[200px]">备注</div>
          <div className="w-32 text-right">收藏时间</div>
          <div className="w-24" />
        </div>
      )}

      {/* Item list */}
      <div className="card-mnemonas min-h-0 flex-1 overflow-auto rounded-lg">
        {favoriteItems.length > 0 ? (
          favoriteItems.map(item => (
            <FavoriteRow
              key={item.path}
              item={item}
              isSelected={visibleSelectedItems.has(item.path)}
              canWrite={canWrite}
              onSelect={() => {
                if (!canWrite) return
                const newSet = new Set(visibleSelectedItems)
                if (newSet.has(item.path)) {
                  newSet.delete(item.path)
                } else {
                  newSet.add(item.path)
                }
                setSelectedItems(newSet)
              }}
              onNavigate={() => handleNavigate(item.path)}
              onRemove={() => {
                if (!canWrite) return
                handleRemoveFavorite(item.path)
              }}
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
        onClose={handleCloseEditModal}
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
            <div className="w-10 h-10 rounded-lg bg-accent-primary/10 text-accent-primary flex items-center justify-center">
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
              placeholder="添加备注信息…"
              value={noteValue}
              onValueChange={setNoteValue}
              variant="bordered"
              classNames={{
                inputWrapper: "rounded-lg",
              }}
            />
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button
              variant="flat"
              onPress={handleCloseEditModal}
              isDisabled={updateNoteMutation.isPending}
              className="text-default-600 rounded-lg"
            >
              取消
            </Button>
            <Button
              color="primary"
              onPress={handleSaveNote}
              isLoading={updateNoteMutation.isPending}
              className="rounded-lg"
            >
              保存
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  )
}
