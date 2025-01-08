import { useMemo, useCallback, useRef, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useVirtualizer } from '@tanstack/react-virtual'
import { 
  Button, 
  Modal,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalFooter,
  Input,
  useDisclosure,
  addToast,
  Dropdown,
  DropdownTrigger,
  DropdownMenu,
  DropdownItem,
  DropdownSection,
  Progress,
} from '@heroui/react'
import { 
  Folder, 
  Grid,
  List,
  FolderPlus,
  Star,
  ChevronRight,
  Home,
  MoreVertical,
  Download,
  Pencil,
  Trash2,
  History,
  Copy,
  FolderOpen,
  Upload,
  CheckCircle2,
  AlertCircle,
  X,
  Link2,
} from 'lucide-react'
import { ShareDialog } from '@/components/share'
import { FileIcon } from '@/components/ui/FileIcon'
import { EmptyState } from '@/components/ui/EmptyState'
import { useNavigate } from 'react-router-dom'
import { useFilesStore, type FileItem } from '@/stores/files'
import { listFiles, deleteFile, createDirectory, uploadFile, moveFile } from '@/api/files'
import { checkFavorites, toggleFavorite } from '@/api/favorites'
import { formatBytes, formatDate, cn } from '@/lib/utils'

// Breadcrumb navigation component
function Breadcrumbs({ 
  path, 
  onNavigate 
}: { 
  path: string
  onNavigate: (path: string) => void 
}) {
  const segments = path === '/' ? [] : path.split('/').filter(Boolean)
  
  return (
    <nav className="flex items-center gap-1 text-sm mb-4 px-1">
      <button
        onClick={() => onNavigate('/')}
        className={cn(
          "flex items-center gap-1.5 px-2.5 py-1.5 rounded-xl transition-all max-w-[180px] truncate border border-transparent",
          segments.length === 0
            ? "bg-content1/80 text-foreground font-medium border-divider shadow-[var(--shadow-soft)]" 
            : "text-default-500 hover:text-foreground hover:bg-content1/60 hover:border-divider"
        )}
      >
        <Home size={14} />
        <span>根目录</span>
      </button>
      
      {segments.map((segment, index) => {
        const segmentPath = '/' + segments.slice(0, index + 1).join('/')
        const isLast = index === segments.length - 1
        
        return (
          <div key={segmentPath} className="flex items-center gap-1">
            <ChevronRight size={14} className="text-default-500" />
            <button
              onClick={() => onNavigate(segmentPath)}
              className={cn(
                "px-2.5 py-1.5 rounded-xl transition-all max-w-[180px] truncate border border-transparent",
                isLast 
                  ? "bg-content1/80 text-foreground font-medium border-divider shadow-[var(--shadow-soft)]" 
                  : "text-default-500 hover:text-foreground hover:bg-content1/60 hover:border-divider"
              )}
            >
              {segment}
            </button>
          </div>
        )
      })}
    </nav>
  )
}

// File row for list view
function FileRow({ 
  file, 
  isSelected, 
  isFavorited,
  isActive,
  onSelect, 
  onOpen,
  onClick,
  onRename,
  onDelete,
  onViewVersions,
  onShare,
  onToggleFavorite,
}: { 
  file: FileItem
  isSelected: boolean
  isFavorited: boolean
  isActive: boolean
  onSelect: () => void
  onOpen: () => void
  onClick: () => void
  onRename: () => void
  onDelete: () => void
  onViewVersions: () => void
  onShare: () => void
  onToggleFavorite: () => void
}) {
  const handleDownload = useCallback(() => {
    // Construct download URL
    const downloadUrl = `/api/v1/files${file.path}?download=true`
    const link = document.createElement('a')
    link.href = downloadUrl
    link.download = file.name
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
  }, [file.path, file.name])

  const handleCopyPath = useCallback(() => {
    navigator.clipboard.writeText(file.path)
    addToast({ title: '路径已复制', color: 'success' })
  }, [file.path])

  return (
    <div 
      className={cn(
        "group grid grid-cols-[44px_1fr_100px_150px_120px_40px] gap-4 px-5 py-3 cursor-pointer transition-all duration-150 border-b border-divider items-center",
        "hover:bg-content2/60",
        isSelected && "bg-accent-primary/10",
        isActive && !isSelected && "bg-content2/40"
      )}
      onClick={onClick}
      onDoubleClick={onOpen}
      onContextMenu={(e) => {
        e.preventDefault()
        // Context menu is handled by the dropdown trigger
      }}
    >
      <div className="flex items-center justify-center" onClick={(e) => e.stopPropagation()}>
        <div 
          className={cn(
            "w-5 h-5 border-2 rounded-lg flex items-center justify-center transition-all duration-150 cursor-pointer",
            isSelected 
              ? "bg-accent-primary border-accent-primary" 
              : "border-default-400 group-hover:border-accent-primary"
          )}
          onClick={onSelect}
        >
          {isSelected && <span className="text-white text-xs font-bold">✓</span>}
        </div>
      </div>
      
      <div className="flex items-center gap-3.5 min-w-0">
        <FileIcon name={file.name} isDir={file.isDir} size={36} variant="tile" />
        <div className="min-w-0">
          <div className="font-medium text-foreground truncate text-[13px]">{file.name}</div>
          <div className="text-xs text-default-500 mt-0.5 truncate">
            {file.isDir ? '文件夹' : file.name.split('.').pop()?.toUpperCase() || 'FILE'}
          </div>
        </div>
      </div>
      
      <div className="text-sm text-default-600">
        {file.isDir ? '—' : formatBytes(file.size)}
      </div>
      
      <div className="text-sm text-default-600">
        {formatDate(file.modTime)}
      </div>
      
      <div className="flex items-center gap-2.5">
        {/* Version Indicator - Memory Stream */}
        <div className="relative w-12 h-1.5 bg-content2 rounded-full overflow-hidden">
          <div className="absolute left-0 top-0 h-full bg-accent-primary/60 w-1/3 rounded-full" />
        </div>
        <span className="text-[10px] font-semibold text-accent-primary bg-accent-primary/15 px-2 py-0.5 rounded-md">
          1
        </span>
      </div>

      {/* Action Menu */}
      <div className="flex items-center justify-center" onClick={(e) => e.stopPropagation()}>
        <Dropdown placement="bottom-end">
          <DropdownTrigger>
            <button className="p-1.5 rounded-md opacity-0 group-hover:opacity-100 transition-opacity hover:bg-content2">
              <MoreVertical size={16} className="text-default-500" />
            </button>
          </DropdownTrigger>
          <DropdownMenu 
            aria-label="文件操作"
            classNames={{ base: "bg-content1 border border-divider shadow-lg" }}
          >
            <DropdownSection title="操作" showDivider>
              {file.isDir ? (
                <DropdownItem 
                  key="open" 
                  startContent={<FolderOpen size={16} />}
                  onPress={onOpen}
                >
                  打开文件夹
                </DropdownItem>
              ) : (
                <DropdownItem 
                  key="download" 
                  startContent={<Download size={16} />}
                  onPress={handleDownload}
                >
                  下载
                </DropdownItem>
              )}
              <DropdownItem 
                key="rename" 
                startContent={<Pencil size={16} />}
                onPress={onRename}
              >
                重命名
              </DropdownItem>
              <DropdownItem 
                key="copy-path" 
                startContent={<Copy size={16} />}
                onPress={handleCopyPath}
              >
                复制路径
              </DropdownItem>
            </DropdownSection>
            <DropdownSection title="分享">
              <DropdownItem 
                key="favorite" 
                startContent={<Star size={16} className={isFavorited ? "fill-accent-primary text-accent-primary" : ""} />}
                onPress={onToggleFavorite}
              >
                {isFavorited ? '取消收藏' : '添加收藏'}
              </DropdownItem>
              <DropdownItem 
                key="share" 
                startContent={<Link2 size={16} />}
                onPress={onShare}
              >
                创建分享链接
              </DropdownItem>
              <DropdownItem 
                key="versions" 
                startContent={<History size={16} />}
                onPress={onViewVersions}
              >
                查看版本历史
              </DropdownItem>
            </DropdownSection>
            <DropdownSection>
              <DropdownItem 
                key="delete" 
                startContent={<Trash2 size={16} />}
                className="text-rose data-[hover=true]:text-rose data-[hover=true]:bg-rose/10"
                onPress={onDelete}
              >
                删除
              </DropdownItem>
            </DropdownSection>
          </DropdownMenu>
        </Dropdown>
      </div>
    </div>
  )
}

// Preview Panel
function PreviewPanel({ file }: { file: FileItem | null }) {
  if (!file) return null

  return (
    <aside className="w-[300px] bg-content2 border-l border-divider p-6 flex flex-col gap-6 relative overflow-hidden shrink-0">
      <div className="text-center relative z-10">
        <div className="w-[88px] h-[88px] mx-auto mb-4 rounded-[20px] bg-content1 flex items-center justify-center border border-divider">
          <FileIcon name={file.name} isDir={file.isDir} size={88} variant="tile" />
        </div>
        <h3 className="font-semibold text-base text-foreground mb-1 truncate px-2">{file.name}</h3>
        <p className="text-[13px] text-default-600">{file.isDir ? '文件夹' : file.name.split('.').pop()?.toUpperCase() || '文件'}</p>
      </div>

      <div className="bg-content1 rounded-xl p-4 relative z-10 border border-divider">
        <div className="text-[10px] font-semibold uppercase tracking-widest text-default-500 mb-3.5">详情</div>
        <div className="grid grid-cols-2 gap-4">
          <div className="text-center">
            <div className="text-lg font-semibold text-foreground">
              {file.isDir ? '-' : formatBytes(file.size)}
            </div>
            <div className="text-[11px] text-default-500 mt-1">大小</div>
          </div>
          <div className="text-center">
            <div className="text-lg font-semibold text-foreground">
              {file.name.split('.').pop()?.toUpperCase() || '-'}
            </div>
            <div className="text-[11px] text-default-500 mt-1">类型</div>
          </div>
        </div>
      </div>

      <div className="flex-1 relative z-10">
        <div className="text-[10px] font-semibold uppercase tracking-widest text-default-500 mb-3.5">时间线</div>
        <div className="relative pl-6 border-l border-divider">
          <div className="relative pb-5 last:pb-0">
            <div className="absolute -left-[20px] top-0 w-3 h-3 rounded-full bg-content1 border border-divider" />
            <div className="text-[13px] font-medium text-foreground">最后修改</div>
            <div className="text-[11px] text-default-500 mt-1">{formatDate(file.modTime)}</div>
          </div>
        </div>
      </div>
    </aside>
  )
}

// Grid view card component
function FileCard({
  file,
  isSelected,
  isFavorited,
  isActive,
  onSelect,
  onOpen,
  onClick,
  onRename,
  onDelete,
  onViewVersions,
  onShare,
  onToggleFavorite,
}: {
  file: FileItem
  isSelected: boolean
  isFavorited: boolean
  isActive: boolean
  onSelect: () => void
  onOpen: () => void
  onClick: () => void
  onRename: () => void
  onDelete: () => void
  onViewVersions: () => void
  onShare: () => void
  onToggleFavorite: () => void
}) {
  const handleDownload = useCallback(() => {
    const downloadUrl = `/api/v1/files${file.path}?download=true`
    const link = document.createElement('a')
    link.href = downloadUrl
    link.download = file.name
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
  }, [file.path, file.name])

  const handleCopyPath = useCallback(() => {
    navigator.clipboard.writeText(file.path)
    addToast({ title: '路径已复制', color: 'success' })
  }, [file.path])

  return (
    <div
      className={cn(
        "group relative bg-content1 border border-divider rounded-xl p-4 cursor-pointer transition-all duration-200",
        "shadow-[var(--shadow-soft)] hover:border-accent-primary/40 hover:shadow-[var(--shadow-medium)]",
        isSelected && "border-accent-primary bg-accent-primary/5",
        isActive && !isSelected && "border-accent-primary/30"
      )}
      onClick={onClick}
      onDoubleClick={onOpen}
    >
      {/* Selection checkbox */}
      <div 
        className="absolute top-3 left-3 z-10"
        onClick={(e) => e.stopPropagation()}
      >
        <div
          className={cn(
            "w-5 h-5 border-2 rounded-lg flex items-center justify-center transition-all duration-150 bg-content1/80 backdrop-blur-sm cursor-pointer",
            isSelected
              ? "bg-accent-primary border-accent-primary"
              : "border-default-400 opacity-0 group-hover:opacity-100"
          )}
          onClick={onSelect}
        >
          {isSelected && <span className="text-white text-xs font-bold">✓</span>}
        </div>
      </div>

      {/* Action menu */}
      <div 
        className="absolute top-3 right-3 z-10"
        onClick={(e) => e.stopPropagation()}
      >
        <Dropdown placement="bottom-end">
          <DropdownTrigger>
            <button className="p-1.5 rounded-md opacity-0 group-hover:opacity-100 transition-opacity bg-content1/80 backdrop-blur-sm hover:bg-content2">
              <MoreVertical size={14} className="text-default-500" />
            </button>
          </DropdownTrigger>
          <DropdownMenu 
            aria-label="文件操作"
            classNames={{ base: "bg-content1 border border-divider shadow-lg" }}
          >
            <DropdownSection title="操作" showDivider>
              {file.isDir ? (
                <DropdownItem 
                  key="open" 
                  startContent={<FolderOpen size={16} />}
                  onPress={onOpen}
                >
                  打开文件夹
                </DropdownItem>
              ) : (
                <DropdownItem 
                  key="download" 
                  startContent={<Download size={16} />}
                  onPress={handleDownload}
                >
                  下载
                </DropdownItem>
              )}
              <DropdownItem 
                key="rename" 
                startContent={<Pencil size={16} />}
                onPress={onRename}
              >
                重命名
              </DropdownItem>
              <DropdownItem 
                key="copy-path" 
                startContent={<Copy size={16} />}
                onPress={handleCopyPath}
              >
                复制路径
              </DropdownItem>
            </DropdownSection>
            <DropdownSection title="分享" showDivider>
              <DropdownItem 
                key="favorite" 
                startContent={<Star size={16} className={isFavorited ? "fill-accent-primary text-accent-primary" : ""} />}
                onPress={onToggleFavorite}
              >
                {isFavorited ? '取消收藏' : '添加收藏'}
              </DropdownItem>
              <DropdownItem 
                key="share" 
                startContent={<Link2 size={16} />}
                onPress={onShare}
                isDisabled={file.isDir}
              >
                创建分享链接
              </DropdownItem>
            </DropdownSection>
            <DropdownSection title="历史">
              <DropdownItem 
                key="versions" 
                startContent={<History size={16} />}
                onPress={onViewVersions}
              >
                查看版本历史
              </DropdownItem>
            </DropdownSection>
            <DropdownSection>
              <DropdownItem 
                key="delete" 
                startContent={<Trash2 size={16} />}
                className="text-rose data-[hover=true]:text-rose data-[hover=true]:bg-rose/10"
                onPress={onDelete}
              >
                删除
              </DropdownItem>
            </DropdownSection>
          </DropdownMenu>
        </Dropdown>
      </div>

      {/* Icon */}
      <div className="flex justify-center py-6">
        <div className="w-16 h-16 rounded-2xl flex items-center justify-center">
          <FileIcon name={file.name} isDir={file.isDir} size={64} variant="tile" />
        </div>
      </div>

      {/* File info */}
      <div className="text-center">
        <div className="font-medium text-foreground truncate text-sm mb-1">{file.name}</div>
        <div className="text-xs text-default-500">
          {file.isDir ? '文件夹' : formatBytes(file.size)}
        </div>
      </div>
    </div>
  )
}

export function FilesPage() {
  const parentRef = useRef<HTMLDivElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  
  // Modal states
  const { isOpen: isNewFolderOpen, onOpen: onNewFolderOpen, onClose: onNewFolderClose } = useDisclosure()
  const { isOpen: isRenameOpen, onOpen: onRenameOpen, onClose: onRenameClose } = useDisclosure()
  const { isOpen: isDeleteOpen, onOpen: onDeleteOpen, onClose: onDeleteClose } = useDisclosure()
  const { isOpen: isBatchDeleteOpen, onOpen: onBatchDeleteOpen, onClose: onBatchDeleteClose } = useDisclosure()
  const { isOpen: isShareOpen, onOpen: onShareOpen, onClose: onShareClose } = useDisclosure()
  const [shareFile, setShareFile] = useState<FileItem | null>(null)
  
  const [newFolderName, setNewFolderName] = useState('')
  const [renameValue, setRenameValue] = useState('')
  const [actionFile, setActionFile] = useState<FileItem | null>(null)
  
  // Drag and drop state
  const [isDragging, setIsDragging] = useState(false)
  const dragCountRef = useRef(0)
  
  // Multi-file upload state
  const [uploadQueue, setUploadQueue] = useState<{file: File, progress: number, status: 'pending' | 'uploading' | 'done' | 'error', error?: string}[]>([])
  const [isUploading, setIsUploading] = useState(false)
  
  const { 
    currentPath, 
    selectedFiles, 
    viewMode,
    sortBy,
    sortOrder,
    setCurrentPath, 
    toggleFileSelection,
    selectAll,
    clearSelection,
    setViewMode,
  } = useFilesStore()

  const { data, isLoading } = useQuery({
    queryKey: ['files', currentPath],
    queryFn: () => listFiles(currentPath),
  })

  // Mutations (omitted for brevity, same as before)
  const deleteMutation = useMutation({
    mutationFn: deleteFile,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['files', currentPath] })
      addToast({ title: '删除成功', color: 'success' })
    },
    onError: (error) => {
      addToast({ title: '删除失败', description: error.message, color: 'danger' })
    },
  })
  
  const createFolderMutation = useMutation({
    mutationFn: createDirectory,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['files', currentPath] })
      onNewFolderClose()
      setNewFolderName('')
      addToast({ title: '文件夹创建成功', color: 'success' })
    },
    onError: (error) => {
      addToast({ title: '创建失败', description: error.message, color: 'danger' })
    },
  })
  
  const renameMutation = useMutation({
    mutationFn: ({ from, to }: { from: string; to: string }) => moveFile(from, to),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['files', currentPath] })
      onRenameClose()
      setActionFile(null)
      addToast({ title: '重命名成功', color: 'success' })
    },
    onError: (error) => {
      addToast({ title: '重命名失败', description: error.message, color: 'danger' })
    },
  })

  // Sort files
  const sortedFiles = useMemo(() => {
    if (!data?.files) return []
    
    return [...data.files].sort((a, b) => {
      if (a.isDir !== b.isDir) {
        return a.isDir ? -1 : 1
      }
      let comparison = 0
      switch (sortBy) {
        case 'name': comparison = a.name.localeCompare(b.name); break
        case 'size': comparison = a.size - b.size; break
        case 'modTime': comparison = new Date(a.modTime).getTime() - new Date(b.modTime).getTime(); break
      }
      return sortOrder === 'asc' ? comparison : -comparison
    })
  }, [data?.files, sortBy, sortOrder])

  // Favorites query
  const filePaths = useMemo(() => sortedFiles.map(f => f.path), [sortedFiles])
  const { data: favoritesData } = useQuery({
    queryKey: ['favorites-check', filePaths],
    queryFn: () => checkFavorites(filePaths),
    enabled: filePaths.length > 0,
    staleTime: 30000, // Cache for 30 seconds
  })

  const favoriteMutation = useMutation({
    mutationFn: ({ path, isFavorited }: { path: string; isFavorited: boolean }) => 
      toggleFavorite(path, isFavorited),
    onSuccess: (newStatus) => {
      queryClient.invalidateQueries({ queryKey: ['favorites-check'] })
      queryClient.invalidateQueries({ queryKey: ['favorites'] })
      addToast({ 
        title: newStatus ? '已添加收藏' : '已取消收藏', 
        color: 'success' 
      })
    },
    onError: (error) => {
      addToast({ title: '操作失败', description: error.message, color: 'danger' })
    },
  })

  const virtualizer = useVirtualizer({
    count: sortedFiles.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 72, // Increased row height
    overscan: 10,
  })

  // Active file for preview panel (not selection)
  const [activeFilePath, setActiveFilePath] = useState<string | null>(null)

  // Handle single click - show preview panel without changing selection
  const handleFileClick = useCallback((file: FileItem) => {
    setActiveFilePath(file.path)
  }, [])

  // Handle double click - open folder or download file
  const handleFileOpen = useCallback((file: FileItem) => {
    if (file.isDir) {
      setCurrentPath(file.path)
      setActiveFilePath(null)
    } else {
      // Download the file on double-click
      const downloadUrl = `/api/v1/files${file.path}?download=true`
      const link = document.createElement('a')
      link.href = downloadUrl
      link.download = file.name
      document.body.appendChild(link)
      link.click()
      document.body.removeChild(link)
    }
  }, [setCurrentPath])

  const handleSelectAll = useCallback(() => {
    if (selectedFiles.size === sortedFiles.length) {
      clearSelection()
    } else {
      selectAll(sortedFiles.map(f => f.path))
    }
  }, [sortedFiles, selectedFiles.size, selectAll, clearSelection])

  const handleCreateFolder = useCallback(() => {
    if (!newFolderName.trim()) return
    const path = currentPath === '/' ? `/${newFolderName}` : `${currentPath}/${newFolderName}`
    createFolderMutation.mutate(path)
  }, [newFolderName, currentPath, createFolderMutation])

  const handleRename = useCallback(() => {
    if (!actionFile || !renameValue.trim()) return
    const parentPath = actionFile.path.substring(0, actionFile.path.lastIndexOf('/')) || '/'
    const newPath = parentPath === '/' ? `/${renameValue}` : `${parentPath}/${renameValue}`
    renameMutation.mutate({ from: actionFile.path, to: newPath })
  }, [actionFile, renameValue, renameMutation])

  const handleDelete = useCallback(() => {
    if (!actionFile) return
    deleteMutation.mutate(actionFile.path)
    onDeleteClose()
    setActionFile(null)
  }, [actionFile, deleteMutation, onDeleteClose])

  // Action handlers for context menu
  const handleOpenRenameModal = useCallback((file: FileItem) => {
    setActionFile(file)
    setRenameValue(file.name)
    onRenameOpen()
  }, [onRenameOpen])

  const handleOpenDeleteModal = useCallback((file: FileItem) => {
    setActionFile(file)
    onDeleteOpen()
  }, [onDeleteOpen])

  const handleViewVersions = useCallback((file: FileItem) => {
    navigate(`/versions?path=${encodeURIComponent(file.path)}`)
  }, [navigate])

  const handleOpenShareModal = useCallback((file: FileItem) => {
    setShareFile(file)
    onShareOpen()
  }, [onShareOpen])

  // Enhanced upload handler with queue support
  const handleUpload = useCallback(async (files: FileList | null) => {
    if (!files || files.length === 0) return
    
    const fileArray = Array.from(files)
    const queue = fileArray.map(file => ({
      file,
      progress: 0,
      status: 'pending' as const,
    }))
    
    setUploadQueue(queue)
    setIsUploading(true)
    
    for (let i = 0; i < fileArray.length; i++) {
      const file = fileArray[i]
      setUploadQueue(prev => prev.map((item, j) => 
        j === i ? { ...item, status: 'uploading' as const } : item
      ))
      
      try {
        await uploadFile(currentPath, file, (progress) => {
          setUploadQueue(prev => prev.map((item, j) => 
            j === i ? { ...item, progress } : item
          ))
        })
        setUploadQueue(prev => prev.map((item, j) => 
          j === i ? { ...item, status: 'done' as const, progress: 100 } : item
        ))
      } catch (error) {
        setUploadQueue(prev => prev.map((item, j) => 
          j === i ? { ...item, status: 'error' as const, error: error instanceof Error ? error.message : '上传失败' } : item
        ))
      }
    }
    
    setIsUploading(false)
    queryClient.invalidateQueries({ queryKey: ['files', currentPath] })
    
    // Auto-clear successful uploads after 3 seconds
    setTimeout(() => {
      setUploadQueue(prev => prev.filter(item => item.status === 'error'))
    }, 3000)
  }, [currentPath, queryClient])

  // Drag and drop handlers
  const handleDragEnter = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    dragCountRef.current++
    if (e.dataTransfer.types.includes('Files')) {
      setIsDragging(true)
    }
  }, [])

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    dragCountRef.current--
    if (dragCountRef.current === 0) {
      setIsDragging(false)
    }
  }, [])

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
  }, [])

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    dragCountRef.current = 0
    setIsDragging(false)
    
    const files = e.dataTransfer.files
    if (files.length > 0) {
      handleUpload(files)
    }
  }, [handleUpload])

  // Batch delete handler
  const handleBatchDelete = useCallback(async () => {
    const paths = Array.from(selectedFiles)
    let successCount = 0
    let errorCount = 0
    
    for (const path of paths) {
      try {
        await deleteFile(path)
        successCount++
      } catch {
        errorCount++
      }
    }
    
    queryClient.invalidateQueries({ queryKey: ['files', currentPath] })
    clearSelection()
    onBatchDeleteClose()
    
    if (errorCount === 0) {
      addToast({ title: `成功删除 ${successCount} 个文件`, color: 'success' })
    } else {
      addToast({ title: `删除完成：${successCount} 成功，${errorCount} 失败`, color: 'warning' })
    }
  }, [selectedFiles, queryClient, currentPath, clearSelection, onBatchDeleteClose])

  // Batch download handler
  const handleBatchDownload = useCallback(() => {
    const paths = Array.from(selectedFiles)
    const files = sortedFiles.filter(f => paths.includes(f.path) && !f.isDir)
    
    for (const file of files) {
      const downloadUrl = `/api/v1/files${file.path}?download=true`
      const link = document.createElement('a')
      link.href = downloadUrl
      link.download = file.name
      document.body.appendChild(link)
      link.click()
      document.body.removeChild(link)
    }
    
    addToast({ title: `已开始下载 ${files.length} 个文件`, color: 'success' })
  }, [selectedFiles, sortedFiles])

  // Determine active file for preview (prioritize activeFilePath, then single selection)
  const activeFile = useMemo(() => {
    if (activeFilePath) {
      return sortedFiles.find(f => f.path === activeFilePath) || null
    }
    if (selectedFiles.size === 1) {
      const path = Array.from(selectedFiles)[0]
      return sortedFiles.find(f => f.path === path) || null
    }
    return null
  }, [activeFilePath, selectedFiles, sortedFiles])

  if (isLoading) {
    return (
      <div className="p-6 lg:p-8 flex items-center justify-center h-full">
        <div className="text-center">
          <div className="w-12 h-12 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-4" />
          <p className="text-default-500">加载记忆中...</p>
        </div>
      </div>
    )
  }

  const hasSelection = selectedFiles.size > 0

  return (
    <div 
      className="h-full flex overflow-hidden relative"
      onDragEnter={handleDragEnter}
      onDragLeave={handleDragLeave}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
      {/* Drag overlay */}
      {isDragging && (
        <div className="absolute inset-0 z-50 bg-content1/95 backdrop-blur-sm flex items-center justify-center border-2 border-dashed border-accent-primary rounded-xl m-4">
          <div className="text-center">
            <div className="w-16 h-16 mx-auto mb-4 rounded-2xl bg-accent-primary flex items-center justify-center shadow-[var(--shadow-soft)]">
              <Upload size={32} className="text-white" />
            </div>
            <h3 className="text-xl font-semibold text-foreground mb-2">释放以上传</h3>
            <p className="text-default-500">文件将上传到当前目录</p>
          </div>
        </div>
      )}

      {/* Upload queue panel */}
      {uploadQueue.length > 0 && (
        <div className="absolute bottom-4 right-4 z-40 w-80 bg-content1 border border-divider rounded-xl shadow-lg overflow-hidden">
          <div className="flex items-center justify-between px-4 py-2.5 bg-content2 border-b border-divider">
            <span className="font-medium text-sm">
              {isUploading ? '上传中...' : '上传完成'}
            </span>
            <button 
              onClick={() => setUploadQueue([])}
              className="p-1 hover:bg-content2 rounded"
            >
              <X size={14} className="text-default-500" />
            </button>
          </div>
          <div className="max-h-60 overflow-y-auto">
            {uploadQueue.map((item, i) => (
              <div key={i} className="px-4 py-2.5 border-b border-divider last:border-b-0">
                <div className="flex items-center gap-2 mb-1.5">
                  {item.status === 'done' && <CheckCircle2 size={14} className="text-emerald-500" />}
                  {item.status === 'error' && <AlertCircle size={14} className="text-rose" />}
                  {(item.status === 'pending' || item.status === 'uploading') && (
                    <div className="w-3.5 h-3.5 border-2 border-accent-primary border-t-transparent rounded-full animate-spin" />
                  )}
                  <span className="text-sm truncate flex-1">{item.file.name}</span>
                </div>
                {item.status === 'uploading' && (
                  <Progress 
                    size="sm" 
                    value={item.progress} 
                    classNames={{ 
                      base: "h-1",
                      indicator: "bg-accent-primary"
                    }} 
                  />
                )}
                {item.status === 'error' && (
                  <p className="text-xs text-rose">{item.error}</p>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      <div className="flex-1 flex flex-col min-w-0 p-7">
        <input ref={fileInputRef} type="file" multiple className="hidden" onChange={(e) => handleUpload(e.target.files)} />
        
        {/* Breadcrumbs */}
        <Breadcrumbs path={currentPath} onNavigate={setCurrentPath} />
        
        {/* Toolbar */}
        <div className="flex items-center justify-between mb-6">
          <div className="flex items-center gap-3">
            {hasSelection ? (
              <>
                <Button 
                  variant="bordered" 
                  className="btn-secondary btn-sm rounded-xl"
                  startContent={<X size={16} />}
                  onPress={clearSelection}
                >
                  取消选择 ({selectedFiles.size})
                </Button>
                <Button 
                  variant="bordered" 
                  className="btn-secondary btn-sm rounded-xl"
                  startContent={<Download size={16} />}
                  onPress={handleBatchDownload}
                >
                  批量下载
                </Button>
                <Button 
                  color="danger"
                  variant="flat"
                  className="rounded-xl"
                  startContent={<Trash2 size={16} />}
                  onPress={onBatchDeleteOpen}
                >
                  批量删除
                </Button>
              </>
            ) : (
              <>
                <Button 
                  className="btn-primary btn-md border-none font-medium rounded-xl"
                  startContent={<Star size={16} className="fill-current" />}
                  onPress={() => fileInputRef.current?.click()}
                  isLoading={isUploading}
                >
                  {isUploading ? '上传中...' : '保存记忆'}
                </Button>
                <Button 
                  variant="bordered" 
                  className="btn-secondary btn-md rounded-xl"
                  startContent={<FolderPlus size={16} />}
                  onPress={onNewFolderOpen}
                >
                  新建空间
                </Button>
              </>
            )}
          </div>
          
          <div className="flex bg-content1 border border-divider rounded-xl p-0.5 shadow-[var(--shadow-soft)]">
            <button 
              className={cn("p-2 rounded-[10px] transition-all", viewMode === 'list' ? "bg-accent-primary text-white shadow-sm" : "text-default-500 hover:text-default-600")}
              onClick={() => setViewMode('list')}
            >
              <List size={16} />
            </button>
            <button 
              className={cn("p-2 rounded-[10px] transition-all", viewMode === 'grid' ? "bg-accent-primary text-white shadow-sm" : "text-default-500 hover:text-default-600")}
              onClick={() => setViewMode('grid')}
            >
              <Grid size={16} />
            </button>
          </div>
        </div>

        {/* File List / Grid */}
        {viewMode === 'list' ? (
          <div className="flex-1 surface-card overflow-hidden flex flex-col">
            {/* Header */}
            <div className="grid grid-cols-[44px_1fr_100px_150px_120px_40px] gap-4 px-5 py-3 table-head text-[11px] font-semibold">
              <div className="flex items-center justify-center">
                <div 
                  className={cn(
                    "w-5 h-5 border-2 rounded-lg cursor-pointer transition-colors",
                    selectedFiles.size === sortedFiles.length && sortedFiles.length > 0 ? "bg-accent-primary border-accent-primary" : "border-default-400 hover:border-accent-primary"
                  )}
                  onClick={handleSelectAll}
                />
              </div>
              <div>名称</div>
              <div>大小</div>
              <div>修改时间</div>
              <div>时光印记</div>
              <div></div>
            </div>

            {/* List Content */}
            <div ref={parentRef} className="flex-1 overflow-auto custom-scrollbar relative">
              <div style={{ height: `${virtualizer.getTotalSize()}px`, width: '100%', position: 'relative' }}>
                {virtualizer.getVirtualItems().map((virtualItem) => {
                  const file = sortedFiles[virtualItem.index]
                  if (!file) return null
                  return (
                    <div
                      key={virtualItem.key}
                      style={{
                        position: 'absolute',
                        top: 0,
                        left: 0,
                        width: '100%',
                        height: `${virtualItem.size}px`,
                        transform: `translateY(${virtualItem.start}px)`,
                      }}
                    >
                      <FileRow
                        file={file}
                        isSelected={selectedFiles.has(file.path)}
                        isFavorited={favoritesData?.[file.path] ?? false}
                        isActive={activeFilePath === file.path}
                        onSelect={() => toggleFileSelection(file.path)}
                        onOpen={() => handleFileOpen(file)}
                        onClick={() => handleFileClick(file)}
                        onRename={() => handleOpenRenameModal(file)}
                        onDelete={() => handleOpenDeleteModal(file)}
                        onViewVersions={() => handleViewVersions(file)}
                        onShare={() => handleOpenShareModal(file)}
                        onToggleFavorite={() => favoriteMutation.mutate({ 
                          path: file.path, 
                          isFavorited: favoritesData?.[file.path] ?? false 
                        })}
                      />
                    </div>
                  )
                })}
              </div>
              
              {sortedFiles.length === 0 && (
                <div className="absolute inset-0 flex items-center justify-center">
                  <EmptyState
                    icon={Folder}
                    title="这里空空如也"
                    description="点击「保存记忆」上传文件"
                    className="max-w-md"
                  />
                </div>
              )}
            </div>
          </div>
        ) : (
          /* Grid View */
          <div className="flex-1 overflow-auto custom-scrollbar">
            {sortedFiles.length === 0 ? (
              <div className="h-full flex items-center justify-center">
                <EmptyState
                  icon={Folder}
                  title="这里空空如也"
                  description="点击「保存记忆」上传文件"
                  className="max-w-md"
                />
              </div>
            ) : (
              <div className="grid grid-cols-[repeat(auto-fill,minmax(160px,1fr))] gap-4">
                {sortedFiles.map((file) => (
                  <FileCard
                    key={file.path}
                    file={file}
                    isSelected={selectedFiles.has(file.path)}
                    isFavorited={favoritesData?.[file.path] ?? false}
                    isActive={activeFilePath === file.path}
                    onSelect={() => toggleFileSelection(file.path)}
                    onOpen={() => handleFileOpen(file)}
                    onClick={() => handleFileClick(file)}
                    onRename={() => handleOpenRenameModal(file)}
                    onDelete={() => handleOpenDeleteModal(file)}
                    onViewVersions={() => handleViewVersions(file)}
                    onShare={() => handleOpenShareModal(file)}
                    onToggleFavorite={() => favoriteMutation.mutate({ 
                      path: file.path, 
                      isFavorited: favoritesData?.[file.path] ?? false 
                    })}
                  />
                ))}
              </div>
            )}
          </div>
        )}
      </div>

      {/* Preview Panel */}
      {activeFile && <PreviewPanel file={activeFile} />}

      {/* Modals */}
      <Modal
        isOpen={isNewFolderOpen}
        onClose={onNewFolderClose}
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
              <FolderPlus size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">新建文件夹</h3>
              <p className="text-xs text-default-500 font-normal">创建一个新的空间用于整理文件</p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4">
            <div>
              <Input
                placeholder="请输入文件夹名称"
                value={newFolderName}
                onValueChange={setNewFolderName}
                autoFocus
                size="lg"
                variant="bordered"
                classNames={{
                  inputWrapper: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                  input: "text-sm placeholder:text-default-400",
                }}
              />
              <div className="flex items-center justify-between text-xs mt-2">
                <span className="text-default-500">支持中文与英文名称</span>
                <span className="text-default-400">建议 2-24 个字符</span>
              </div>
            </div>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button variant="flat" onPress={onNewFolderClose} className="text-default-600 rounded-xl">取消</Button>
            <Button color="primary" onPress={handleCreateFolder} isLoading={createFolderMutation.isPending} isDisabled={!newFolderName.trim()} className="rounded-xl">创建</Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      <Modal
        isOpen={isRenameOpen}
        onClose={onRenameClose}
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
              <Pencil size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">重命名</h3>
              <p className="text-xs text-default-500 font-normal">为项目设置新的名称</p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4">
            <div>
              <Input
                placeholder="请输入新名称"
                value={renameValue}
                onValueChange={setRenameValue}
                autoFocus
                size="lg"
                variant="bordered"
                classNames={{
                  inputWrapper: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                  input: "text-sm placeholder:text-default-400",
                }}
              />
            </div>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button variant="flat" onPress={onRenameClose} className="text-default-600 rounded-xl">取消</Button>
            <Button color="primary" onPress={handleRename} isLoading={renameMutation.isPending} isDisabled={!renameValue.trim() || renameValue === actionFile?.name} className="rounded-xl">确定</Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

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
              <AlertCircle size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">确认删除</h3>
              <p className="text-xs text-default-500 font-normal">文件将被移入回收站</p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4">
            <p className="text-default-600">确定要删除 <strong className="text-foreground">{actionFile?.name}</strong> 吗？</p>
            <p className="text-xs text-default-500 mt-2">文件将被移入回收站，可在 30 天内恢复。</p>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button variant="flat" onPress={onDeleteClose} className="text-default-600 rounded-xl">取消</Button>
            <Button color="danger" onPress={handleDelete} isLoading={deleteMutation.isPending} className="rounded-xl">删除</Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

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
              <Trash2 size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">批量删除</h3>
              <p className="text-xs text-default-500 font-normal">选中文件将被移入回收站</p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4">
            <p className="text-default-600">确定要删除选中的 <strong className="text-foreground">{selectedFiles.size}</strong> 个文件吗？</p>
            <p className="text-xs text-default-500 mt-2">文件将被移入回收站，可在 30 天内恢复。</p>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button variant="flat" onPress={onBatchDeleteClose} className="text-default-600 rounded-xl">取消</Button>
            <Button color="danger" onPress={handleBatchDelete} className="rounded-xl">删除全部</Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      {/* Share Dialog */}
      <ShareDialog
        isOpen={isShareOpen}
        onClose={() => {
          onShareClose()
          setShareFile(null)
        }}
        filePath={shareFile?.path || ''}
      />
    </div>
  )
}
