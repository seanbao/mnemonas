import { useMemo, useCallback, useEffect, useRef, useState } from 'react'
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
  FolderUp,
  CheckCircle2,
  AlertCircle,
  X,
  Link2,
  Move,
  Files,
  RotateCcw,
} from 'lucide-react'
import { ShareDialog } from '@/components/share'
import { FileIcon } from '@/components/ui/FileIcon'
import { EmptyState } from '@/components/ui/EmptyState'
import { ContextMenu, ContextMenuSection, ContextMenuItem } from '@/components/ui/ContextMenu'
import { MoveDialog } from '@/components/file'
import { PreviewModal, type PreviewFile } from '@/components/preview'
import { useLocation, useNavigate } from 'react-router-dom'
import { useFilesStore, type FileItem } from '@/stores/files'
import { useClipboardStore } from '@/stores/clipboard'
import { useCanWrite, useUser } from '@/stores/auth'
import { useContextMenu, useKeyboardShortcuts } from '@/hooks'
import {
  listFiles,
  deleteFile,
  createDirectory,
  uploadFile,
  moveFile,
  copyFile,
  downloadFile,
  ApiError,
  MAX_UPLOAD_FILE_SIZE_BYTES,
  MAX_UPLOAD_FILE_SIZE_LABEL,
  type ActionResult,
  type FileListResponse,
} from '@/api/files'
import { checkFavorites, toggleFavorite } from '@/api/favorites'
import { listShares, ShareError } from '@/api/share'
import { copyTextToClipboard, formatBytes, formatDate, cn, normalizePath } from '@/lib/utils'
import { getInvalidHomeDirDescription, invalidHomeDirTitle, resolveUserHomeScope } from '@/lib/userScope'

function isDirectoryAlreadyExistsError(error: unknown): boolean {
  return error instanceof ApiError && error.status === 409
}

function pathWithinBase(basePath: string, targetPath: string): boolean {
  if (basePath === '/') {
    return targetPath.startsWith('/')
  }
  return targetPath === basePath || targetPath.startsWith(`${basePath}/`)
}

function getUploadSizeError(relativePath: string | undefined, file: File): string {
  return `${relativePath || file.name} 超过 ${MAX_UPLOAD_FILE_SIZE_LABEL} 上传限制`
}

function getFavoritesBannerContent(error: unknown): { title: string; description: string } {
  if (error && typeof error === 'object') {
    const code = 'code' in error && typeof error.code === 'string' ? error.code : undefined
    if (code === 'FAVORITES_FEATURE_DISABLED') {
      return {
        title: '收藏功能已关闭',
        description: '当前服务已关闭收藏功能。启用后重新加载即可恢复收藏状态与相关操作。',
      }
    }
    if (code === 'FAVORITES_UNAVAILABLE') {
      return {
        title: '收藏功能暂不可用',
        description: '收藏存储未成功初始化，请检查系统健康状态或稍后重试。',
      }
    }
  }

  return {
    title: '收藏状态加载失败',
    description: error instanceof Error ? error.message : '请稍后重试',
  }
}

function getFilesLoadErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: '当前目录暂不可用',
      description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
    }
  }

  return {
    title: '当前目录加载失败',
    description: error instanceof Error ? error.message : '请稍后重试',
  }
}

function getErrorStatus(error: unknown): number | undefined {
  if (error && typeof error === 'object' && 'status' in error && typeof error.status === 'number') {
    return error.status
  }
  return undefined
}

function getErrorCode(error: unknown): string | undefined {
  if (error && typeof error === 'object' && 'code' in error && typeof error.code === 'string') {
    return error.code
  }
  return undefined
}

function isFilesystemUnavailableError(error: unknown): boolean {
  const status = getErrorStatus(error)
  const code = getErrorCode(error)
  return status === 503 || code === 'SERVICE_UNAVAILABLE'
}

function getFilesActionErrorToast(
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
  if (isFilesystemUnavailableError(error)) {
    return {
      title: titles.unavailable,
      description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
      color: 'warning',
    }
  }

  return {
    title: titles.failure,
    description: error instanceof Error ? error.message : '请稍后重试',
    color: 'danger',
  }
}

function getFilesActionSuccessToast(
  result: ActionResult,
  titles: {
    success: string
    warning: string
  }
): {
  title: string
  color: 'success' | 'warning'
} {
  if (result.warning) {
    return {
      title: result.message ?? titles.warning,
      color: 'warning',
    }
  }

  return {
    title: titles.success,
    color: 'success',
  }
}

function getMissingFileActionResult(): ActionResult {
  return {
    warning: true,
    message: '文件或文件夹已不存在，已同步更新',
  }
}

function getFavoriteActionErrorToast(error: unknown): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  const code = getErrorCode(error)
  const status = getErrorStatus(error)

  if (code === 'FAVORITES_FEATURE_DISABLED') {
    return {
      title: '收藏功能已关闭',
      description: '当前服务已关闭收藏功能。启用后重新加载即可恢复收藏状态与相关操作。',
      color: 'warning',
    }
  }

  if (code === 'FAVORITES_UNAVAILABLE' || (status === 503 && code !== 'FAVORITES_FEATURE_DISABLED')) {
    return {
      title: '收藏功能暂不可用',
      description: '收藏存储未成功初始化，请检查系统健康状态或稍后重试。',
      color: 'warning',
    }
  }

  return {
    title: '操作失败',
    description: error instanceof Error ? error.message : '请稍后重试',
    color: 'danger',
  }
}

function getFavoriteRefreshErrorToast(error: unknown): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  const code = getErrorCode(error)
  const status = getErrorStatus(error)

  if (code === 'FAVORITES_FEATURE_DISABLED') {
    return {
      title: '收藏功能已关闭',
      description: '当前服务已关闭收藏功能。启用后重新加载即可恢复收藏状态与相关操作。',
      color: 'warning',
    }
  }

  if (code === 'FAVORITES_UNAVAILABLE' || (status === 503 && code !== 'FAVORITES_FEATURE_DISABLED')) {
    return {
      title: '收藏功能暂不可用',
      description: '收藏存储未成功初始化，请检查系统健康状态或稍后重试。',
      color: 'warning',
    }
  }

  return {
    title: '刷新失败',
    description: error instanceof Error ? error.message : '请稍后重试',
    color: 'danger',
  }
}

function getUploadQueueErrorMessage(error: unknown): string {
  if (isFilesystemUnavailableError(error)) {
    return '文件系统当前不可用，请检查系统健康状态或稍后重试。'
  }

  return error instanceof Error ? error.message : '上传失败'
}

function getFolderUploadSummaryToast(
  successCount: number,
  errorCount: number,
  errors: unknown[],
  warningMessages: string[]
): {
  title: string
  description: string
  color: 'success' | 'warning' | 'danger'
} {
  if (errorCount === 0) {
    if (warningMessages.length > 0) {
      return {
        title: warningMessages[0] || '文件夹上传完成，但存在警告',
        description: `成功上传 ${successCount} 个文件`,
        color: 'warning',
      }
    }

    return {
      title: '文件夹上传完成',
      description: `成功上传 ${successCount} 个文件`,
      color: 'success',
    }
  }

  if (successCount === 0) {
    if (errors.length > 0 && errors.every(isFilesystemUnavailableError)) {
      return {
        title: '文件夹上传暂不可用',
        description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
        color: 'warning',
      }
    }

    return {
      title: '文件夹上传失败',
      description: `共 ${errorCount} 个文件上传失败`,
      color: 'danger',
    }
  }

  return {
    title: '文件夹上传部分完成',
    description: `成功上传 ${successCount} 个文件，失败 ${errorCount} 个`,
    color: 'warning',
  }
}

function getShareBannerContent(): { title: string; description: string } {
  return {
    title: '分享功能已关闭',
    description: '当前服务已关闭分享功能。请在系统设置中重新启用后再创建分享链接。',
  }
}

function getShareUnavailableBannerContent(): { title: string; description: string } {
  return {
    title: '分享功能暂不可用',
    description: '分享服务当前不可用，请检查系统健康状态或稍后重试。',
  }
}

function getShareActionLabel(state: 'disabled' | 'unavailable' | null): string {
  if (state === 'disabled') {
    return '分享功能已关闭'
  }
  if (state === 'unavailable') {
    return '分享功能暂不可用'
  }
  return '创建分享链接'
}

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
  favoriteActionsAvailable,
  favoriteUnavailableLabel,
  shareActionsAvailable,
  shareActionLabel,
  isMultiSelection,
  canWrite,
  onSelect, 
  onOpen,
  onClick,
  onRename,
  onDelete,
  onViewVersions,
  onShare,
  onToggleFavorite,
  onContextMenu,
}: { 
  file: FileItem
  isSelected: boolean
  isFavorited: boolean
  favoriteActionsAvailable: boolean
  favoriteUnavailableLabel: string
  shareActionsAvailable: boolean
  shareActionLabel: string
  isMultiSelection: boolean
  canWrite: boolean
  onSelect: (e: React.MouseEvent) => void
  onOpen: () => void
  onClick: (e: React.MouseEvent) => void
  onRename: () => void
  onDelete: () => void
  onViewVersions: () => void
  onShare: () => void
  onToggleFavorite: () => void
  onContextMenu: (e: React.MouseEvent) => void
}) {
  const handleDownload = useCallback(() => {
    void downloadFile(file.path, { filename: file.name }).catch((error: unknown) => {
      addToast(getFilesActionErrorToast(error, {
        unavailable: '下载暂不可用',
        failure: '下载失败',
      }))
    })
  }, [file.path, file.name])

  const handleCopyPath = useCallback(() => {
    copyTextToClipboard(file.path)
      .then(() => {
        addToast({ title: '路径已复制', color: 'success' })
      })
      .catch(() => {
        addToast({ title: '复制失败', color: 'danger' })
      })
  }, [file.path])

  return (
    <div 
      className={cn(
        "group grid grid-cols-[44px_1fr_100px_150px_120px_40px] gap-4 px-5 py-3 cursor-pointer transition-all duration-150 border-b border-divider items-center",
        "hover:bg-content2/60",
        isMultiSelection && "bg-content2/30",
        isSelected && "bg-accent-primary/10"
      )}
      onClick={(e) => {
        e.stopPropagation()
        onClick(e)
      }}
      onDoubleClick={onOpen}
      onContextMenu={onContextMenu}
    >
      <div className="flex items-center justify-center" onClick={(e) => e.stopPropagation()}>
        <button
          type="button"
          role="checkbox"
          aria-checked={isSelected}
          aria-label={`选择 ${file.name}`}
          className={cn(
            "w-5 h-5 border-2 rounded-lg flex items-center justify-center transition-all duration-150 cursor-pointer",
            isSelected 
              ? "bg-accent-primary border-accent-primary" 
              : "border-default-400 group-hover:border-accent-primary"
          )}
          onClick={(e) => {
            e.stopPropagation()
            onSelect(e)
          }}
        >
          {isSelected && <span className="text-white text-xs font-bold">✓</span>}
        </button>
      </div>
      
      <div className="flex items-center gap-3.5 min-w-0">
        <FileIcon name={file.name} isDir={file.isDir} size={36} variant="tile" />
        <div className="min-w-0">
          <div className="font-medium text-foreground truncate text-[13px]">{file.name}</div>
          <div className="text-xs text-default-500 mt-0.5 truncate">
            {isMultiSelection ? '多选中' : file.isDir ? '文件夹' : file.name.split('.').pop()?.toUpperCase() || 'FILE'}
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
        <span className="text-xs text-default-400">—</span>
      </div>

      <div className="flex items-center justify-center" onClick={(e) => e.stopPropagation()}>
        {!isMultiSelection && (
          <Dropdown placement="bottom-end">
            <DropdownTrigger>
              <button aria-label={`${file.name} 操作菜单`} className="p-1.5 rounded-md opacity-0 group-hover:opacity-100 transition-opacity hover:bg-content2">
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
                {canWrite ? (
                  <DropdownItem 
                    key="rename" 
                    startContent={<Pencil size={16} />}
                    onPress={onRename}
                  >
                    重命名
                  </DropdownItem>
                ) : null}
                <DropdownItem 
                  key="copy-path" 
                  startContent={<Copy size={16} />}
                  onPress={handleCopyPath}
                >
                  复制路径
                </DropdownItem>
              </DropdownSection>
              {canWrite ? (
                <DropdownSection title="分享">
                  <DropdownItem 
                    key="favorite" 
                    startContent={<Star size={16} className={isFavorited ? "fill-accent-primary text-accent-primary" : ""} />}
                    isDisabled={!favoriteActionsAvailable}
                    onPress={onToggleFavorite}
                  >
                    {favoriteActionsAvailable ? (isFavorited ? '取消收藏' : '添加收藏') : favoriteUnavailableLabel}
                  </DropdownItem>
                  <DropdownItem 
                    key="share" 
                    startContent={<Link2 size={16} />}
                    isDisabled={!shareActionsAvailable}
                    onPress={onShare}
                  >
                    {shareActionLabel}
                  </DropdownItem>
                </DropdownSection>
              ) : null}
              <DropdownSection title="历史">
                <DropdownItem 
                  key="versions" 
                  startContent={<History size={16} />}
                  onPress={onViewVersions}
                  isDisabled={file.isDir}
                >
                  查看版本历史
                </DropdownItem>
              </DropdownSection>
              {canWrite ? (
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
              ) : null}
            </DropdownMenu>
          </Dropdown>
        )}
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
  favoriteActionsAvailable,
  favoriteUnavailableLabel,
  shareActionsAvailable,
  shareActionLabel,
  isMultiSelection,
  canWrite,
  onSelect,
  onOpen,
  onClick,
  onRename,
  onDelete,
  onViewVersions,
  onShare,
  onToggleFavorite,
  onContextMenu,
}: {
  file: FileItem
  isSelected: boolean
  isFavorited: boolean
  favoriteActionsAvailable: boolean
  favoriteUnavailableLabel: string
  shareActionsAvailable: boolean
  shareActionLabel: string
  isMultiSelection: boolean
  canWrite: boolean
  onSelect: (e: React.MouseEvent) => void
  onOpen: () => void
  onClick: (e: React.MouseEvent) => void
  onRename: () => void
  onDelete: () => void
  onViewVersions: () => void
  onShare: () => void
  onToggleFavorite: () => void
  onContextMenu: (e: React.MouseEvent) => void
}) {
  const handleDownload = useCallback(() => {
    void downloadFile(file.path, { filename: file.name }).catch((error: unknown) => {
      addToast(getFilesActionErrorToast(error, {
        unavailable: '下载暂不可用',
        failure: '下载失败',
      }))
    })
  }, [file.path, file.name])

  const handleCopyPath = useCallback(() => {
    copyTextToClipboard(file.path)
      .then(() => {
        addToast({ title: '路径已复制', color: 'success' })
      })
      .catch(() => {
        addToast({ title: '复制失败', color: 'danger' })
      })
  }, [file.path])

  return (
    <div
      className={cn(
        "group relative bg-content1 border border-divider rounded-xl p-4 cursor-pointer transition-all duration-200",
        "shadow-[var(--shadow-soft)] hover:border-accent-primary/40 hover:shadow-[var(--shadow-medium)]",
        isMultiSelection && "bg-content2/40",
        isSelected && "border-accent-primary bg-accent-primary/5"
      )}
      onClick={(e) => {
        e.stopPropagation()
        onClick(e)
      }}
      onDoubleClick={onOpen}
      onContextMenu={onContextMenu}
    >
      <div 
        className="absolute top-3 left-3 z-10"
        onClick={(e) => e.stopPropagation()}
      >
        <button
          type="button"
          role="checkbox"
          aria-checked={isSelected}
          aria-label={`选择 ${file.name}`}
          className={cn(
            "w-5 h-5 border-2 rounded-lg flex items-center justify-center transition-all duration-150 bg-content1/80 backdrop-blur-sm cursor-pointer",
            isSelected
              ? "bg-accent-primary border-accent-primary"
              : "border-default-400 opacity-0 group-hover:opacity-100"
          )}
          onClick={(e) => {
            e.stopPropagation()
            onSelect(e)
          }}
        >
          {isSelected && <span className="text-white text-xs font-bold">✓</span>}
        </button>
      </div>

      <div 
        className="absolute top-3 right-3 z-10"
        onClick={(e) => e.stopPropagation()}
      >
        {!isMultiSelection && (
          <Dropdown placement="bottom-end">
            <DropdownTrigger>
              <button aria-label={`${file.name} 操作菜单`} className="p-1.5 rounded-md opacity-0 group-hover:opacity-100 transition-opacity bg-content1/80 backdrop-blur-sm hover:bg-content2">
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
                {canWrite ? (
                  <DropdownItem 
                    key="rename" 
                    startContent={<Pencil size={16} />}
                    onPress={onRename}
                  >
                    重命名
                  </DropdownItem>
                ) : null}
                <DropdownItem 
                  key="copy-path" 
                  startContent={<Copy size={16} />}
                  onPress={handleCopyPath}
                >
                  复制路径
                </DropdownItem>
              </DropdownSection>
              {canWrite ? (
                <DropdownSection title="分享" showDivider>
                  <DropdownItem 
                    key="favorite" 
                    startContent={<Star size={16} className={isFavorited ? "fill-accent-primary text-accent-primary" : ""} />}
                    isDisabled={!favoriteActionsAvailable}
                    onPress={onToggleFavorite}
                  >
                    {favoriteActionsAvailable ? (isFavorited ? '取消收藏' : '添加收藏') : favoriteUnavailableLabel}
                  </DropdownItem>
                  <DropdownItem 
                    key="share" 
                    startContent={<Link2 size={16} />}
                    isDisabled={!shareActionsAvailable}
                    onPress={onShare}
                  >
                    {shareActionLabel}
                  </DropdownItem>
                </DropdownSection>
              ) : null}
              <DropdownSection title="历史">
                <DropdownItem 
                  key="versions" 
                  startContent={<History size={16} />}
                  onPress={onViewVersions}
                  isDisabled={file.isDir}
                >
                  查看版本历史
                </DropdownItem>
              </DropdownSection>
              {canWrite ? (
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
              ) : null}
            </DropdownMenu>
          </Dropdown>
        )}
      </div>

      <div className="flex justify-center py-6">
        <div className="w-16 h-16 rounded-2xl flex items-center justify-center">
          <FileIcon name={file.name} isDir={file.isDir} size={64} variant="tile" />
        </div>
      </div>

      <div className="text-center">
        <div className="font-medium text-foreground truncate text-sm mb-1">{file.name}</div>
        <div className="text-xs text-default-500">
          {isMultiSelection ? '多选中' : file.isDir ? '文件夹' : formatBytes(file.size)}
        </div>
      </div>
    </div>
  )
}

export function FilesPage() {
  const parentRef = useRef<HTMLDivElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const folderInputRef = useRef<HTMLInputElement>(null)
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const location = useLocation()
  const highlightedPathFromState =
    typeof location.state === 'object' &&
    location.state !== null &&
    'highlightPath' in location.state &&
    typeof location.state.highlightPath === 'string'
      ? location.state.highlightPath
      : null
  
  // Context menu state
  const contextMenu = useContextMenu()
  const [contextMenuFile, setContextMenuFile] = useState<FileItem | null>(null)
  
  // Clipboard state
  const clipboard = useClipboardStore()
  const canWrite = useCanWrite()
  const user = useUser()
  const { scopedHomeDir, hasInvalidHomeDir } = resolveUserHomeScope(user)
  
  // Track focused file index for keyboard navigation
  const [focusedIndex, setFocusedIndex] = useState<number>(-1)
  
  // Modal states
  const { isOpen: isNewFolderOpen, onOpen: onNewFolderOpen, onClose: onNewFolderClose } = useDisclosure()
  const { isOpen: isRenameOpen, onOpen: onRenameOpen, onClose: onRenameClose } = useDisclosure()
  const { isOpen: isDeleteOpen, onOpen: onDeleteOpen, onClose: onDeleteClose } = useDisclosure()
  const { isOpen: isBatchDeleteOpen, onOpen: onBatchDeleteOpen, onClose: onBatchDeleteClose } = useDisclosure()
  const { isOpen: isShareOpen, onOpen: onShareOpen, onClose: onShareClose } = useDisclosure()
  const [shareFile, setShareFile] = useState<FileItem | null>(null)
  
  // Move/Copy dialog state
  const { isOpen: isMoveOpen, onOpen: onMoveOpen, onClose: onMoveClose } = useDisclosure()
  const [moveMode, setMoveMode] = useState<'move' | 'copy'>('move')
  const [moveFiles, setMoveFiles] = useState<FileItem[]>([])
  
  // Preview modal state
  const { isOpen: isPreviewOpen, onOpen: onPreviewOpen, onClose: onPreviewClose } = useDisclosure()
  const [previewFile, setPreviewFile] = useState<PreviewFile | null>(null)
  const [isBatchDeleting, setIsBatchDeleting] = useState(false)
  
  const [newFolderName, setNewFolderName] = useState('')
  const [renameValue, setRenameValue] = useState('')
  const [actionFile, setActionFile] = useState<FileItem | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<FileItem | null>(null)
  const newFolderSessionRef = useRef(0)
  const currentNewFolderNameRef = useRef('')
  const renameSessionRef = useRef(0)
  const currentRenameValueRef = useRef('')
  const currentRenameFileRef = useRef<FileItem | null>(null)
  const lastSelectedIndexRef = useRef<number | null>(null)
  const appliedHighlightedPathRef = useRef<string | null>(null)
  const [multiSelectHintVisible, setMultiSelectHintVisible] = useState(false)
  const hintTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  
  // Drag and drop state
  const [isDragging, setIsDragging] = useState(false)
  const dragCountRef = useRef(0)
  
  // Multi-file upload state
  const [uploadQueue, setUploadQueue] = useState<{file: File, relativePath?: string, progress: number, status: 'pending' | 'uploading' | 'done' | 'error', error?: string}[]>([])
  const [isUploading, setIsUploading] = useState(false)
  const [showUploadPanel, setShowUploadPanel] = useState(false)
  const uploadClearTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const uploadSessionRef = useRef(0)
  const [shareFeatureDisabled, setShareFeatureDisabled] = useState(false)
  
  const { 
    currentPath, 
    selectedFiles, 
    viewMode,
    sortBy,
    sortOrder,
    setCurrentPath, 
    toggleFileSelection,
    setSelection,
    selectAll,
    clearSelection,
    setViewMode,
  } = useFilesStore()

  useEffect(() => {
    if (hasInvalidHomeDir) return
    if (!location.pathname.startsWith('/files')) return
    const routePath = location.pathname.replace(/^\/files/, '')
    let finalPath = '/'
    if (routePath) {
      try {
        finalPath = normalizePath(decodeURI(routePath))
      } catch {
        const fallbackPath = currentPath || '/'
        const fallbackRoute = fallbackPath === '/' ? '/files' : `/files${encodeURI(fallbackPath)}`
        addToast({
          title: fallbackPath === '/' ? '路径格式无效，已返回根目录' : '路径格式无效，已返回上一个有效位置',
          color: 'warning',
        })
        if (location.pathname !== fallbackRoute) {
          navigate(fallbackRoute, { replace: true })
        }
        return
      }
    }
    if (scopedHomeDir && scopedHomeDir !== '/' && !pathWithinBase(scopedHomeDir, finalPath)) {
      addToast({
        title: '仅可访问主目录内的文件',
        color: 'warning',
      })
      if (currentPath !== scopedHomeDir) {
        setCurrentPath(scopedHomeDir)
      }
      return
    }
    if (finalPath !== currentPath) {
      setCurrentPath(finalPath)
    }
  }, [hasInvalidHomeDir, location.pathname, currentPath, navigate, scopedHomeDir, setCurrentPath])

  useEffect(() => {
    if (!hasInvalidHomeDir) return
    if (currentPath !== '/') {
      setCurrentPath('/')
    }
    if (location.pathname !== '/files') {
      navigate('/files', { replace: true })
    }
  }, [hasInvalidHomeDir, currentPath, location.pathname, navigate, setCurrentPath])

  useEffect(() => {
    if (hasInvalidHomeDir) return
    if (!user || user.role === 'admin' || !scopedHomeDir || scopedHomeDir === '/') return
    if (location.pathname !== '/files' || currentPath !== '/') return
    setCurrentPath(scopedHomeDir)
  }, [hasInvalidHomeDir, user, location.pathname, currentPath, scopedHomeDir, setCurrentPath])

  const currentPathAllowed = !hasInvalidHomeDir && (!scopedHomeDir || pathWithinBase(scopedHomeDir, currentPath))

  useEffect(() => {
    if (hasInvalidHomeDir) return
    const encodedPath = currentPath === '/' ? '' : encodeURI(currentPath)
    const targetPath = `/files${encodedPath}`
    if (location.pathname !== targetPath) {
      navigate(targetPath, { replace: true })
    }
  }, [hasInvalidHomeDir, currentPath, location.pathname, navigate])

  useEffect(() => {
    lastSelectedIndexRef.current = null
    clearSelection()
    setActiveFilePath(null)
    setFocusedIndex(-1)
  }, [currentPath, clearSelection])

  useEffect(() => {
    currentNewFolderNameRef.current = newFolderName
  }, [newFolderName])

  useEffect(() => {
    currentRenameValueRef.current = renameValue
  }, [renameValue])

  useEffect(() => {
    currentRenameFileRef.current = actionFile
  }, [actionFile])

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['files', currentPath],
    queryFn: () => listFiles(currentPath),
    enabled: currentPathAllowed,
  })

  const removeFilesFromCache = useCallback((paths: string[]) => {
    if (paths.length === 0) {
      return
    }

    const removedPaths = new Set(paths)
    queryClient.setQueryData<FileListResponse>(['files', currentPath], (current) => {
      if (!current) {
        return current
      }

      const files = current.files.filter((file) => !removedPaths.has(file.path))
      if (files.length === current.files.length) {
        return current
      }

      return {
        ...current,
        files,
      }
    })
  }, [currentPath, queryClient])

  const deleteFileWithMissingSync = useCallback(async (path: string) => {
    try {
      return await deleteFile(path)
    } catch (error) {
      if (getErrorStatus(error) === 404) {
        return getMissingFileActionResult()
      }

      throw error
    }
  }, [])

  // Mutations (omitted for brevity, same as before)
  const deleteMutation = useMutation({
    mutationFn: deleteFileWithMissingSync,
    onSuccess: (result, path) => {
      removeFilesFromCache([path])
      queryClient.invalidateQueries({ queryKey: ['files', currentPath] })
      onDeleteClose()
      setDeleteTarget(null)
      addToast(getFilesActionSuccessToast(result, {
        success: '删除成功',
        warning: '删除完成，但存在警告',
      }))
    },
    onError: (error) => {
      addToast(getFilesActionErrorToast(error, {
        unavailable: '删除暂不可用',
        failure: '删除失败',
      }))
    },
  })
  
  const createFolderMutation = useMutation({
    mutationFn: ({ path }: { path: string; directoryPath: string; folderName: string; sessionId: number }) => createDirectory(path),
    onSuccess: (result, variables) => {
      queryClient.invalidateQueries({ queryKey: ['files', variables.directoryPath] })
      if (
        newFolderSessionRef.current === variables.sessionId
        && currentNewFolderNameRef.current.trim() === variables.folderName
      ) {
        onNewFolderClose()
        setNewFolderName('')
      }
      addToast(getFilesActionSuccessToast(result, {
        success: '文件夹创建成功',
        warning: '文件夹创建完成，但存在警告',
      }))
    },
    onError: (error) => {
      addToast(getFilesActionErrorToast(error, {
        unavailable: '创建暂不可用',
        failure: '创建失败',
      }))
    },
  })
  
  const renameMutation = useMutation({
    mutationFn: ({ from, to }: { from: string; to: string; directoryPath: string; targetPath: string; submittedName: string; sessionId: number }) => moveFile(from, to),
    onSuccess: (result, variables) => {
      queryClient.invalidateQueries({ queryKey: ['files', variables.directoryPath] })
      if (
        renameSessionRef.current === variables.sessionId
        && currentRenameFileRef.current?.path === variables.targetPath
        && currentRenameValueRef.current.trim() === variables.submittedName
      ) {
        onRenameClose()
        setActionFile(null)
      }
      addToast(getFilesActionSuccessToast(result, {
        success: '重命名成功',
        warning: '重命名完成，但存在警告',
      }))
    },
    onError: (error) => {
      addToast(getFilesActionErrorToast(error, {
        unavailable: '重命名暂不可用',
        failure: '重命名失败',
      }))
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

  useEffect(() => {
    if (selectedFiles.size === 0) return

    const available = new Set(sortedFiles.map((file) => file.path))
    const filtered = Array.from(selectedFiles).filter((path) => available.has(path))
    if (filtered.length === selectedFiles.size) return

    setSelection(filtered)
    if (filtered.length === 0) {
      setFocusedIndex(-1)
      setActiveFilePath(null)
      lastSelectedIndexRef.current = null
      return
    }

    const firstMatch = sortedFiles.find((file) => filtered.includes(file.path))
    if (firstMatch) {
      const firstIndex = sortedFiles.findIndex((file) => file.path === firstMatch.path)
      if (firstIndex >= 0) {
        setFocusedIndex(firstIndex)
        lastSelectedIndexRef.current = firstIndex
      }
      if (firstMatch.isDir) {
        setActiveFilePath(null)
      } else {
        setActiveFilePath(firstMatch.path)
      }
    }
  }, [sortedFiles, selectedFiles, setSelection])

  useEffect(() => {
    if (!highlightedPathFromState) {
      appliedHighlightedPathRef.current = null
      return
    }
    if (isLoading || appliedHighlightedPathRef.current === highlightedPathFromState) {
      return
    }

    appliedHighlightedPathRef.current = highlightedPathFromState

    const targetIndex = sortedFiles.findIndex(
      (file) => !file.isDir && file.path === highlightedPathFromState
    )
    if (targetIndex >= 0) {
      setSelection([highlightedPathFromState])
      setActiveFilePath(highlightedPathFromState)
      setFocusedIndex(targetIndex)
      lastSelectedIndexRef.current = targetIndex
    }

    navigate(`${location.pathname}${location.search ?? ''}`, { replace: true, state: null })
  }, [highlightedPathFromState, isLoading, location.pathname, location.search, navigate, setSelection, sortedFiles])

  // Favorites query
  const filePaths = useMemo(() => sortedFiles.map(f => f.path), [sortedFiles])
  const {
    data: favoritesData,
    error: favoritesError,
    refetch: refetchFavorites,
  } = useQuery({
    queryKey: ['favorites-check', filePaths],
    queryFn: () => checkFavorites(filePaths),
    enabled: !hasInvalidHomeDir && filePaths.length > 0,
    staleTime: 30000, // Cache for 30 seconds
  })
  const { error: shareAvailabilityError } = useQuery({
    queryKey: ['shares-availability'],
    queryFn: () => listShares(),
    enabled: canWrite,
    retry: false,
    staleTime: 30000,
  })
  const favoriteActionsAvailable = !favoritesError
  const favoritesBanner = favoritesError ? getFavoritesBannerContent(favoritesError) : null
  const favoriteUnavailableLabel = favoritesBanner?.title ?? '收藏状态不可用'
  const syncFavoriteStatus = useCallback((path: string, isFavorited: boolean) => {
    queryClient.setQueriesData<Record<string, boolean>>({ queryKey: ['favorites-check'] }, (current) => {
      if (!current) {
        return current
      }

      return { ...current, [path]: isFavorited }
    })
  }, [queryClient])
  const shareFeatureState = shareFeatureDisabled ? 'disabled' : getShareFeatureState(shareAvailabilityError)
  const shareActionsAvailable = shareFeatureState === null
  const shareActionLabel = getShareActionLabel(shareFeatureState)
  const shareBanner = shareFeatureState === 'disabled'
    ? getShareBannerContent()
    : shareFeatureState === 'unavailable'
      ? getShareUnavailableBannerContent()
      : null

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
    onError: (error, variables) => {
      if (getErrorCode(error) === 'FAVORITE_ALREADY_EXISTS' || getErrorStatus(error) === 409) {
        syncFavoriteStatus(variables.path, true)
        queryClient.invalidateQueries({ queryKey: ['favorites-check'] })
        queryClient.invalidateQueries({ queryKey: ['favorites'] })
        addToast({
          title: '已在收藏夹中',
          description: '该文件已被其他操作加入收藏，状态已同步。',
          color: 'warning',
        })
        return
      }

      if (getErrorCode(error) === 'FAVORITE_NOT_FOUND' || getErrorStatus(error) === 404) {
        syncFavoriteStatus(variables.path, false)
        queryClient.invalidateQueries({ queryKey: ['favorites-check'] })
        queryClient.invalidateQueries({ queryKey: ['favorites'] })
        addToast({
          title: '收藏已移除',
          description: '该文件已不在收藏夹中，状态已同步。',
          color: 'warning',
        })
        return
      }

      addToast(getFavoriteActionErrorToast(error))
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

  const handleFileSelection = useCallback(
    (file: FileItem, index: number, event: React.MouseEvent, mode: 'primary' | 'toggle' = 'primary') => {
      const isShift = event.shiftKey
      const isMeta = event.metaKey || event.ctrlKey
      const hasAnchor = lastSelectedIndexRef.current !== null && lastSelectedIndexRef.current >= 0
      const isCurrentlySelected = selectedFiles.has(file.path)

      if (isShift && hasAnchor) {
        const start = Math.min(lastSelectedIndexRef.current as number, index)
        const end = Math.max(lastSelectedIndexRef.current as number, index)
        const rangePaths = sortedFiles.slice(start, end + 1).map((item) => item.path)
        if (isMeta || mode === 'toggle') {
          const merged = new Set(selectedFiles)
          rangePaths.forEach((path) => merged.add(path))
          setSelection(Array.from(merged))
        } else {
          setSelection(rangePaths)
        }
      } else if (isMeta || mode === 'toggle') {
        toggleFileSelection(file.path)
      } else {
        setSelection([file.path])
      }

      lastSelectedIndexRef.current = index
      setFocusedIndex(index)

      const willSelect = isShift ? true : (isMeta || mode === 'toggle') ? !isCurrentlySelected : true
      if (file.isDir) {
        setActiveFilePath(null)
      } else if (willSelect) {
        setActiveFilePath(file.path)
      } else if (activeFilePath === file.path) {
        setActiveFilePath(null)
      }
    },
    [activeFilePath, selectedFiles, setSelection, setFocusedIndex, sortedFiles, toggleFileSelection]
  )

  // Handle double click - open folder or preview file
  const handleFileOpen = useCallback((file: FileItem) => {
    if (file.isDir) {
      setCurrentPath(file.path)
      setActiveFilePath(null)
      return
    }
    setPreviewFile({ path: file.path, name: file.name })
    onPreviewOpen()
  }, [setCurrentPath, onPreviewOpen])

  const handleSelectAll = useCallback(() => {
    if (selectedFiles.size === sortedFiles.length) {
      clearSelection()
    } else {
      selectAll(sortedFiles.map(f => f.path))
    }
  }, [sortedFiles, selectedFiles.size, selectAll, clearSelection])

  const handleCreateFolder = useCallback(() => {
    if (!canWrite) return
    const trimmedFolderName = newFolderName.trim()
    if (!trimmedFolderName) return
    const path = currentPath === '/' ? `/${trimmedFolderName}` : `${currentPath}/${trimmedFolderName}`
    createFolderMutation.mutate({
      path,
      directoryPath: currentPath,
      folderName: trimmedFolderName,
      sessionId: newFolderSessionRef.current,
    })
  }, [canWrite, newFolderName, currentPath, createFolderMutation])

  const handleRename = useCallback(() => {
    if (!canWrite) return
    const trimmedRenameValue = renameValue.trim()
    if (!actionFile || !trimmedRenameValue) return
    if (trimmedRenameValue === actionFile.name) return
    const parentPath = actionFile.path.substring(0, actionFile.path.lastIndexOf('/')) || '/'
    const newPath = parentPath === '/' ? `/${trimmedRenameValue}` : `${parentPath}/${trimmedRenameValue}`
    renameMutation.mutate({
      from: actionFile.path,
      to: newPath,
      directoryPath: currentPath,
      targetPath: actionFile.path,
      submittedName: trimmedRenameValue,
      sessionId: renameSessionRef.current,
    })
  }, [canWrite, actionFile, currentPath, renameValue, renameMutation])

  const handleDelete = useCallback(() => {
    if (!canWrite) return
    if (!deleteTarget) return
    deleteMutation.mutate(deleteTarget.path)
  }, [canWrite, deleteTarget, deleteMutation])

  const handleOpenNewFolderModal = useCallback(() => {
    newFolderSessionRef.current += 1
    onNewFolderOpen()
  }, [onNewFolderOpen])

  const handleCloseNewFolderModal = useCallback(() => {
    if (createFolderMutation.isPending) return
    onNewFolderClose()
  }, [createFolderMutation.isPending, onNewFolderClose])

  // Action handlers for context menu
  const handleOpenRenameModal = useCallback((file: FileItem) => {
    if (!canWrite) return
    renameSessionRef.current += 1
    setActionFile(file)
    setRenameValue(file.name)
    onRenameOpen()
  }, [canWrite, onRenameOpen])

  const handleOpenDeleteModal = useCallback((file: FileItem) => {
    if (!canWrite) return
    setDeleteTarget(file)
    onDeleteOpen()
  }, [canWrite, onDeleteOpen])

  const handleCloseRenameModal = useCallback(() => {
    if (renameMutation.isPending) return
    onRenameClose()
  }, [renameMutation.isPending, onRenameClose])

  const handleCloseDeleteModal = useCallback(() => {
    if (deleteMutation.isPending) return
    onDeleteClose()
    setDeleteTarget(null)
  }, [deleteMutation.isPending, onDeleteClose])

  const handleCloseBatchDeleteModal = useCallback(() => {
    if (isBatchDeleting) return
    onBatchDeleteClose()
  }, [isBatchDeleting, onBatchDeleteClose])

  const handleViewVersions = useCallback((file: FileItem) => {
    if (file.isDir) return
    navigate(`/versions?path=${encodeURIComponent(file.path)}`)
  }, [navigate])

  const handleOpenShareModal = useCallback((file: FileItem) => {
    if (!canWrite) return
    if (!shareActionsAvailable) {
      addToast({ title: shareActionLabel, color: 'warning' })
      return
    }
    setShareFile(file)
    onShareOpen()
  }, [canWrite, onShareOpen, shareActionLabel, shareActionsAvailable])

  // Move/Copy handlers
  const handleOpenMoveModal = useCallback((files: FileItem[]) => {
    if (!canWrite) return
    setMoveFiles(files)
    setMoveMode('move')
    onMoveOpen()
  }, [canWrite, onMoveOpen])

  const handleOpenCopyModal = useCallback((files: FileItem[]) => {
    if (!canWrite) return
    setMoveFiles(files)
    setMoveMode('copy')
    onMoveOpen()
  }, [canWrite, onMoveOpen])

  // Context menu handler
  const handleContextMenu = useCallback((file: FileItem, e: React.MouseEvent) => {
    e.preventDefault()
    e.stopPropagation()
    if (!selectedFiles.has(file.path)) {
      setSelection([file.path])
      setFocusedIndex(sortedFiles.findIndex((item) => item.path === file.path))
      if (!file.isDir) {
        setActiveFilePath(file.path)
      } else {
        setActiveFilePath(null)
      }
    }
    setContextMenuFile(file)
    contextMenu.show(file.path, e.clientX, e.clientY)
  }, [contextMenu, selectedFiles, setSelection, setFocusedIndex, setActiveFilePath, sortedFiles])

  // Context menu actions
  const handleContextMenuDownload = useCallback(() => {
    if (!contextMenuFile || contextMenuFile.isDir) return
    void downloadFile(contextMenuFile.path, { filename: contextMenuFile.name })
      .catch((error: unknown) => {
        addToast(getFilesActionErrorToast(error, {
          unavailable: '下载暂不可用',
          failure: '下载失败',
        }))
      })
      .finally(() => {
        contextMenu.hide()
      })
  }, [contextMenuFile, contextMenu])

  const handleContextMenuCopyPath = useCallback(() => {
    if (!contextMenuFile) return
    copyTextToClipboard(contextMenuFile.path)
      .then(() => {
        addToast({ title: '路径已复制', color: 'success' })
      })
      .catch(() => {
        addToast({ title: '复制失败', color: 'danger' })
      })
      .finally(() => {
        contextMenu.hide()
      })
  }, [contextMenuFile, contextMenu])

  // Track created directories to avoid duplicate MKCOL calls
  const createdDirsRef = useRef<Set<string>>(new Set())

  // Ensure a directory path exists (create parent directories recursively)
  const ensureDirectoryExists = useCallback(async (dirPath: string, warningMessages: string[]) => {
    if (dirPath === '/' || createdDirsRef.current.has(dirPath)) return
    
    // Get parent path
    const parentPath = dirPath.substring(0, dirPath.lastIndexOf('/')) || '/'
    
    // Ensure parent exists first
    await ensureDirectoryExists(parentPath, warningMessages)
    
    // Create this directory if not already created
    if (!createdDirsRef.current.has(dirPath)) {
      try {
        const result = await createDirectory(dirPath)
        if (result.warning) {
          warningMessages.push(result.message ?? '')
        }
        createdDirsRef.current.add(dirPath)
      } catch (error) {
        if (isDirectoryAlreadyExistsError(error)) {
          createdDirsRef.current.add(dirPath)
          return
        }
        throw error
      }
    }
  }, [])

  // Enhanced upload handler with queue support and folder support
  const handleUpload = useCallback(async (files: FileList | null) => {
    if (!canWrite) return
    if (!files || files.length === 0) return

    const uploadSession = uploadSessionRef.current + 1
    uploadSessionRef.current = uploadSession
    const isCurrentUploadSession = () => uploadSessionRef.current === uploadSession

    if (uploadClearTimeoutRef.current) {
      clearTimeout(uploadClearTimeoutRef.current)
      uploadClearTimeoutRef.current = null
    }
    
    const fileArray = Array.from(files)
    
    // Check if this is a folder upload (files have webkitRelativePath)
    const isFolderUpload = fileArray.some(f => (f as File & { webkitRelativePath?: string }).webkitRelativePath)
    
    // Reset created directories tracker
    createdDirsRef.current.clear()
    
    const queue = fileArray.map(file => {
      const relativePath = (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name
      const isOversized = file.size > MAX_UPLOAD_FILE_SIZE_BYTES
      return {
        file,
        relativePath,
        progress: 0,
        status: isOversized ? 'error' as const : 'pending' as const,
        error: isOversized ? getUploadSizeError(relativePath, file) : undefined,
      }
    })

    const uploadableEntries = queue
      .map((item, index) => ({ item, index }))
      .filter(({ item }) => item.status === 'pending')
    const rejectedEntries = queue.filter((item) => item.status === 'error')
    
    setUploadQueue(queue)
    setIsUploading(uploadableEntries.length > 0)
    setShowUploadPanel(true)  // Auto show upload panel when upload starts

    if (rejectedEntries.length > 0) {
      addToast({
        title: rejectedEntries.length === queue.length ? '上传失败' : '部分文件未上传',
        description: rejectedEntries.length === 1
          ? rejectedEntries[0].error
          : `${rejectedEntries.length} 个文件超过 ${MAX_UPLOAD_FILE_SIZE_LABEL} 上传限制`,
        color: rejectedEntries.length === queue.length ? 'danger' : 'warning',
      })
    }

    if (uploadableEntries.length === 0) {
      return
    }

    let successCount = 0
    let errorCount = rejectedEntries.length
    const uploadErrors: unknown[] = []
    const uploadWarningMessages: string[] = []
    
    for (const { item, index } of uploadableEntries) {
      const file = item.file
      const relativePath = (file as File & { webkitRelativePath?: string }).webkitRelativePath || ''
      
      if (isCurrentUploadSession()) {
        setUploadQueue(prev => prev.map((item, j) => 
          j === index ? { ...item, status: 'uploading' as const } : item
        ))
      }
      
      try {
        // For folder uploads, create parent directories first
        if (relativePath && relativePath.includes('/')) {
          const relativeDir = relativePath.substring(0, relativePath.lastIndexOf('/'))
          const targetDir = currentPath === '/' ? `/${relativeDir}` : `${currentPath}/${relativeDir}`
          await ensureDirectoryExists(targetDir, uploadWarningMessages)
        }

        // Calculate the target path for the file
        let targetPath = currentPath
        if (relativePath && relativePath.includes('/')) {
          const relativeDir = relativePath.substring(0, relativePath.lastIndexOf('/'))
          targetPath = currentPath === '/' ? `/${relativeDir}` : `${currentPath}/${relativeDir}`
        }
        
        await uploadFile(targetPath, file, (progress) => {
          if (!isCurrentUploadSession()) {
            return
          }
          setUploadQueue(prev => prev.map((item, j) => 
            j === index ? { ...item, progress } : item
          ))
        })
        successCount++
        if (isCurrentUploadSession()) {
          setUploadQueue(prev => prev.map((item, j) => 
            j === index ? { ...item, status: 'done' as const, progress: 100 } : item
          ))
        }
      } catch (error) {
        errorCount++
        uploadErrors.push(error)
        if (isCurrentUploadSession()) {
          setUploadQueue(prev => prev.map((item, j) => 
            j === index ? { ...item, status: 'error' as const, error: getUploadQueueErrorMessage(error) } : item
          ))
        }
      }
    }

    queryClient.invalidateQueries({ queryKey: ['files', currentPath] })

    if (!isCurrentUploadSession()) {
      return
    }

    setIsUploading(false)
    
    // Show summary toast for folder upload
    if (isFolderUpload) {
      addToast(getFolderUploadSummaryToast(successCount, errorCount, uploadErrors, uploadWarningMessages))
    }
    
    // Auto-clear successful uploads after 3 seconds
    uploadClearTimeoutRef.current = setTimeout(() => {
      setUploadQueue(prev => prev.filter(item => item.status === 'error'))
      uploadClearTimeoutRef.current = null
    }, 3000)
  }, [canWrite, currentPath, queryClient, ensureDirectoryExists])

  const handleUploadInputChange = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    void handleUpload(event.target.files)
    event.target.value = ''
  }, [handleUpload])

  useEffect(() => {
    return () => {
      if (uploadClearTimeoutRef.current) {
        clearTimeout(uploadClearTimeoutRef.current)
        uploadClearTimeoutRef.current = null
      }
      if (hintTimeoutRef.current) {
        clearTimeout(hintTimeoutRef.current)
        hintTimeoutRef.current = null
      }
    }
  }, [])

  // Drag and drop handlers
  const handleDragEnter = useCallback((e: React.DragEvent) => {
    if (!canWrite) return
    e.preventDefault()
    e.stopPropagation()
    dragCountRef.current++
    if (e.dataTransfer.types.includes('Files')) {
      setIsDragging(true)
    }
  }, [canWrite])

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    if (!canWrite) return
    e.preventDefault()
    e.stopPropagation()
    dragCountRef.current--
    if (dragCountRef.current === 0) {
      setIsDragging(false)
    }
  }, [canWrite])

  const handleDragOver = useCallback((e: React.DragEvent) => {
    if (!canWrite) return
    e.preventDefault()
    e.stopPropagation()
  }, [canWrite])

  const handleDrop = useCallback((e: React.DragEvent) => {
    if (!canWrite) return
    e.preventDefault()
    e.stopPropagation()
    dragCountRef.current = 0
    setIsDragging(false)
    
    const files = e.dataTransfer.files
    if (files.length > 0) {
      handleUpload(files)
    }
  }, [canWrite, handleUpload])

  // Batch delete handler
  const handleBatchDelete = useCallback(async () => {
    if (!canWrite) return
    setIsBatchDeleting(true)
    const paths = Array.from(selectedFiles)
    let successCount = 0
    let errorCount = 0
    const succeededPaths: string[] = []
    const failedPaths: string[] = []
    const failedErrors: unknown[] = []
    const warningMessages: string[] = []

    try {
      for (const path of paths) {
        try {
          const result = await deleteFileWithMissingSync(path)
          successCount++
          succeededPaths.push(path)
          if (result.warning) {
            warningMessages.push(result.message ?? '')
          }
        } catch (error) {
          errorCount++
          failedPaths.push(path)
          failedErrors.push(error)
        }
      }

      removeFilesFromCache(succeededPaths)
      queryClient.invalidateQueries({ queryKey: ['files', currentPath] })

      if (errorCount === 0) {
        onBatchDeleteClose()
        clearSelection()
        if (warningMessages.length > 0) {
          addToast({
            title: warningMessages[0] || `已删除 ${successCount} 个文件，但存在警告`,
            color: 'warning',
          })
        } else {
          addToast({ title: `成功删除 ${successCount} 个文件`, color: 'success' })
        }
        return
      }

      setSelection(failedPaths)

      if (successCount === 0) {
        if (failedErrors.length > 0 && failedErrors.every(isFilesystemUnavailableError)) {
          addToast({
            title: '批量删除暂不可用',
            description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
            color: 'warning',
          })
        } else {
          addToast({ title: '批量删除失败', description: `共 ${errorCount} 个项目删除失败`, color: 'danger' })
        }
        return
      }

      addToast({ title: '批量删除部分完成', description: `成功 ${successCount} 个，失败 ${errorCount} 个`, color: 'warning' })
    } finally {
      setIsBatchDeleting(false)
    }
  }, [canWrite, selectedFiles, deleteFileWithMissingSync, removeFilesFromCache, queryClient, currentPath, clearSelection, onBatchDeleteClose, setSelection])

  // Batch download handler
  const handleBatchDownload = useCallback(async () => {
    const paths = Array.from(selectedFiles)
    const files = sortedFiles.filter(f => paths.includes(f.path) && !f.isDir)

    if (files.length === 0) {
      addToast({ title: '未选择可下载的文件', color: 'warning' })
      return
    }

    const results = await Promise.allSettled(files.map((file) => downloadFile(file.path, { filename: file.name })))
    const failedErrors = results
      .filter((result): result is PromiseRejectedResult => result.status === 'rejected')
      .map((result) => result.reason)
    const failed = results.filter((result) => result.status === 'rejected').length
    const succeeded = files.length - failed

    if (failed === 0) {
      addToast({ title: `已开始下载 ${succeeded} 个文件`, color: 'success' })
      return
    }

    if (succeeded === 0) {
      if (failedErrors.length > 0 && failedErrors.every(isFilesystemUnavailableError)) {
        addToast({
          title: '批量下载暂不可用',
          description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
          color: 'warning',
        })
      } else {
        addToast({ title: '批量下载失败', description: `共 ${failed} 个文件下载失败`, color: 'danger' })
      }
      return
    }

    addToast({ title: '部分文件开始下载', description: `已开始 ${succeeded} 个，失败 ${failed} 个`, color: 'warning' })
  }, [selectedFiles, sortedFiles])

  // Keyboard shortcuts handlers
  const handleKeyboardCopy = useCallback(() => {
    if (!canWrite) return
    if (selectedFiles.size === 0) return
    clipboard.copy(Array.from(selectedFiles), currentPath)
    addToast({ title: `已复制 ${selectedFiles.size} 个项目`, color: 'success' })
  }, [canWrite, selectedFiles, currentPath, clipboard])

  const handleKeyboardCut = useCallback(() => {
    if (!canWrite) return
    if (selectedFiles.size === 0) return
    clipboard.cut(Array.from(selectedFiles), currentPath)
    addToast({ title: `已剪切 ${selectedFiles.size} 个项目`, color: 'success' })
  }, [canWrite, selectedFiles, currentPath, clipboard])

  const handleKeyboardPaste = useCallback(async () => {
    if (!canWrite) return
    if (!clipboard.hasPaths()) return
    
    const { paths, operation, sourcePath } = clipboard
    if (!operation || !sourcePath) return
    
    let successCount = 0
    let errorCount = 0
    const failedPaths: string[] = []
    const failedErrors: unknown[] = []
    const warningMessages: string[] = []
    
    for (const path of paths) {
      const fileName = path.split('/').pop() || ''
      const destPath = currentPath === '/' ? `/${fileName}` : `${currentPath}/${fileName}`
      
      try {
        if (operation === 'cut') {
          const result = await moveFile(path, destPath)
          if (result.warning) {
            warningMessages.push(result.message ?? '')
          }
        } else {
          const result = await copyFile(path, destPath)
          if (result.warning) {
            warningMessages.push(result.message ?? '')
          }
        }
        successCount++
      } catch (error) {
        errorCount++
        failedPaths.push(path)
        failedErrors.push(error)
      }
    }
    
    if (operation === 'cut') {
      if (failedPaths.length === 0) {
        clipboard.clear()
      } else {
        clipboard.cut(failedPaths, sourcePath)
      }
    }
    
    queryClient.invalidateQueries({ queryKey: ['files', currentPath] })
    if (sourcePath !== currentPath) {
      queryClient.invalidateQueries({ queryKey: ['files', sourcePath] })
    }
    
    if (errorCount === 0) {
          if (warningMessages.length > 0) {
            addToast({
              title: warningMessages[0] || `成功复制 ${successCount} 个文件，但存在警告`,
              color: 'warning',
            })
          } else {
            addToast({ title: `成功${operation === 'cut' ? '移动' : '复制'} ${successCount} 个文件`, color: 'success' })
          }
      return
    }

    if (successCount === 0) {
      if (failedErrors.length > 0 && failedErrors.every(isFilesystemUnavailableError)) {
        addToast({
          title: `${operation === 'cut' ? '批量移动' : '批量复制'}暂不可用`,
          description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
          color: 'warning',
        })
      } else {
        addToast({ title: `${operation === 'cut' ? '批量移动' : '批量复制'}失败`, description: `共 ${errorCount} 个项目失败`, color: 'danger' })
      }
      return
    }

    addToast({ title: `${operation === 'cut' ? '批量移动' : '批量复制'}部分完成`, description: `成功 ${successCount} 个，失败 ${errorCount} 个`, color: 'warning' })
  }, [canWrite, clipboard, currentPath, queryClient])

  const handleKeyboardDelete = useCallback(() => {
    if (!canWrite) return
    if (selectedFiles.size === 0) return
    onBatchDeleteOpen()
  }, [canWrite, selectedFiles.size, onBatchDeleteOpen])

  const handleKeyboardRename = useCallback(() => {
    if (!canWrite) return
    if (selectedFiles.size !== 1) return
    const path = Array.from(selectedFiles)[0]
    const file = sortedFiles.find(f => f.path === path)
    if (file) {
      handleOpenRenameModal(file)
    }
  }, [canWrite, selectedFiles, sortedFiles, handleOpenRenameModal])

  const handleKeyboardEnter = useCallback(() => {
    // If there's a focused file, open it
    if (focusedIndex >= 0 && focusedIndex < sortedFiles.length) {
      const file = sortedFiles[focusedIndex]
      handleFileOpen(file)
      return
    }

    // Otherwise, if single selection, open that file
    if (selectedFiles.size === 1) {
      const path = Array.from(selectedFiles)[0]
      const file = sortedFiles.find(f => f.path === path)
      if (file) {
        handleFileOpen(file)
      }
    }
  }, [focusedIndex, sortedFiles, selectedFiles, handleFileOpen])

  const applyKeyboardSelection = useCallback((nextIndex: number, useRange: boolean) => {
    if (sortedFiles.length === 0) return

    const clampedIndex = Math.max(0, Math.min(nextIndex, sortedFiles.length - 1))
    const file = sortedFiles[clampedIndex]
    if (!file) return

    if (useRange) {
      const anchorIndex = lastSelectedIndexRef.current ?? (focusedIndex >= 0 ? focusedIndex : clampedIndex)
      if (lastSelectedIndexRef.current === null) {
        lastSelectedIndexRef.current = anchorIndex
      }
      const start = Math.min(anchorIndex, clampedIndex)
      const end = Math.max(anchorIndex, clampedIndex)
      const rangePaths = sortedFiles.slice(start, end + 1).map((item) => item.path)
      setSelection(rangePaths)
    } else {
      setSelection([file.path])
      lastSelectedIndexRef.current = clampedIndex
    }

    setFocusedIndex(clampedIndex)
    if (file.isDir) {
      setActiveFilePath(null)
    } else {
      setActiveFilePath(file.path)
    }
  }, [focusedIndex, setFocusedIndex, setSelection, setActiveFilePath, sortedFiles])

  const handleKeyboardArrowDown = useCallback((event?: KeyboardEvent) => {
    if (sortedFiles.length === 0) return
    const newIndex = focusedIndex < 0 ? 0 : Math.min(focusedIndex + 1, sortedFiles.length - 1)
    applyKeyboardSelection(newIndex, Boolean(event?.shiftKey))
  }, [applyKeyboardSelection, focusedIndex, sortedFiles.length])

  const handleKeyboardArrowUp = useCallback((event?: KeyboardEvent) => {
    if (sortedFiles.length === 0) return
    const newIndex = focusedIndex <= 0 ? 0 : focusedIndex - 1
    applyKeyboardSelection(newIndex, Boolean(event?.shiftKey))
  }, [applyKeyboardSelection, focusedIndex, sortedFiles.length])

  const handleKeyboardRefresh = useCallback(() => {
    void refetch().then((result) => {
      if (result.error) {
        addToast(getFilesActionErrorToast(result.error, {
          unavailable: '刷新暂不可用',
          failure: '刷新失败',
        }))
        return
      }

      addToast({ title: '刷新成功', color: 'success' })
    })
  }, [refetch])

  const handleRefreshFavoritesBanner = useCallback(() => {
    void refetchFavorites().then((result) => {
      if (result.error) {
        addToast(getFavoriteRefreshErrorToast(result.error))
        return
      }

      addToast({ title: '收藏状态已刷新', color: 'success' })
    })
  }, [refetchFavorites])

  // Register keyboard shortcuts
  useKeyboardShortcuts({
    onDelete: canWrite ? handleKeyboardDelete : undefined,
    onSelectAll: handleSelectAll,
    onEscape: clearSelection,
    onCopy: canWrite ? handleKeyboardCopy : undefined,
    onCut: canWrite ? handleKeyboardCut : undefined,
    onPaste: canWrite ? handleKeyboardPaste : undefined,
    onRename: canWrite ? handleKeyboardRename : undefined,
    onEnter: handleKeyboardEnter,
    onArrowDown: handleKeyboardArrowDown,
    onArrowUp: handleKeyboardArrowUp,
    onRefresh: handleKeyboardRefresh,
    onNewFolder: canWrite ? handleOpenNewFolderModal : undefined,
  })

  // Determine active file for preview (prioritize activeFilePath, then single selection)
  const activeFile = useMemo(() => {
    if (selectedFiles.size > 1) {
      return null
    }
    if (activeFilePath) {
      return sortedFiles.find(f => f.path === activeFilePath) || null
    }
    if (selectedFiles.size === 1) {
      const path = Array.from(selectedFiles)[0]
      return sortedFiles.find(f => f.path === path) || null
    }
    return null
  }, [activeFilePath, selectedFiles, sortedFiles])

  useEffect(() => {
    if (selectedFiles.size > 1 && activeFilePath) {
      setActiveFilePath(null)
    }
  }, [selectedFiles.size, activeFilePath])

  useEffect(() => {
    if (selectedFiles.size === 0) {
      lastSelectedIndexRef.current = null
    }
  }, [selectedFiles.size])


  // Previewable files for navigation in preview modal (non-directory files)
  const previewFiles = useMemo<PreviewFile[]>(() => {
    return sortedFiles
      .filter(f => !f.isDir)
      .map(f => ({ path: f.path, name: f.name }))
  }, [sortedFiles])

  const selectedFileItems = useMemo(() => {
    if (selectedFiles.size === 0) return []
    return sortedFiles.filter((file) => selectedFiles.has(file.path))
  }, [selectedFiles, sortedFiles])
  const totalCounts = useMemo(() => {
    let files = 0
    let folders = 0
    for (const item of sortedFiles) {
      if (item.isDir) {
        folders++
      } else {
        files++
      }
    }
    return { files, folders }
  }, [sortedFiles])
  const selectedCounts = useMemo(() => {
    let files = 0
    let folders = 0
    for (const item of selectedFileItems) {
      if (item.isDir) {
        folders++
      } else {
        files++
      }
    }
    return { files, folders }
  }, [selectedFileItems])

  const hasSelection = selectedFiles.size > 0
  const isAllSelected = sortedFiles.length > 0 && selectedFiles.size === sortedFiles.length
  const isPartialSelected = selectedFiles.size > 0 && selectedFiles.size < sortedFiles.length
  const hasDownloadableSelection = selectedFiles.size > 0
    && selectedCounts.files > 0
    && selectedCounts.folders === 0
  const hasMultiSelection = selectedFiles.size > 1
  const showMultiSelectHint = hasMultiSelection || multiSelectHintVisible

  useEffect(() => {
    if (!hasMultiSelection) {
      setMultiSelectHintVisible(false)
    }
  }, [hasMultiSelection])

  const handleInvertSelection = useCallback(() => {
    if (sortedFiles.length === 0) return
    const inverted = sortedFiles
      .filter((file) => !selectedFiles.has(file.path))
      .map((file) => file.path)
    setSelection(inverted)
    if (inverted.length === 0) {
      setFocusedIndex(-1)
      setActiveFilePath(null)
    } else {
      const firstIndex = sortedFiles.findIndex((file) => file.path === inverted[0])
      setFocusedIndex(firstIndex)
      const firstFile = sortedFiles[firstIndex]
      if (firstFile?.isDir) {
        setActiveFilePath(null)
      } else {
        setActiveFilePath(firstFile?.path ?? null)
      }
      lastSelectedIndexRef.current = firstIndex
    }
  }, [selectedFiles, setSelection, setFocusedIndex, setActiveFilePath, sortedFiles])

  const handleSelectOnlyFiles = useCallback(() => {
    if (sortedFiles.length === 0) return
    const paths = sortedFiles.filter((file) => !file.isDir).map((file) => file.path)
    setSelection(paths)
    if (paths.length === 0) {
      setFocusedIndex(-1)
      setActiveFilePath(null)
      return
    }
    const firstIndex = sortedFiles.findIndex((file) => file.path === paths[0])
    setFocusedIndex(firstIndex)
    setActiveFilePath(paths[0])
    lastSelectedIndexRef.current = firstIndex
  }, [setSelection, setFocusedIndex, setActiveFilePath, sortedFiles])

  const handleSelectOnlyFolders = useCallback(() => {
    if (sortedFiles.length === 0) return
    const paths = sortedFiles.filter((file) => file.isDir).map((file) => file.path)
    setSelection(paths)
    if (paths.length === 0) {
      setFocusedIndex(-1)
      setActiveFilePath(null)
      return
    }
    const firstIndex = sortedFiles.findIndex((file) => file.path === paths[0])
    setFocusedIndex(firstIndex)
    setActiveFilePath(null)
    lastSelectedIndexRef.current = firstIndex
  }, [setSelection, setFocusedIndex, setActiveFilePath, sortedFiles])

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

  if (hasInvalidHomeDir) {
    return (
      <div className="h-full flex overflow-hidden relative">
        <div className="flex-1 flex flex-col min-w-0 p-7">
          <Breadcrumbs path="/" onNavigate={setCurrentPath} />
          <div className="flex-1 flex items-center justify-center surface-card">
            <EmptyState
              icon={AlertCircle}
              title={invalidHomeDirTitle}
              description={getInvalidHomeDirDescription('浏览文件')}
              className="max-w-md"
            />
          </div>
        </div>
      </div>
    )
  }

  if (error) {
    const errorPresentation = getFilesLoadErrorPresentation(error)
    return (
      <div className="h-full flex overflow-hidden relative">
        <div className="flex-1 flex flex-col min-w-0 p-7">
          <Breadcrumbs path={currentPath} onNavigate={setCurrentPath} />
          <div className="flex-1 flex items-center justify-center surface-card">
            <EmptyState
              icon={AlertCircle}
              title={errorPresentation.title}
              description={errorPresentation.description}
              className="max-w-md"
              action={
                <Button variant="bordered" className="rounded-xl" onPress={handleKeyboardRefresh}>
                  重新加载
                </Button>
              }
            />
          </div>
        </div>
      </div>
    )
  }

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
      {showUploadPanel && uploadQueue.length > 0 && (
        <div className="fixed bottom-6 right-6 z-[100] w-96 bg-content1 border border-divider rounded-xl shadow-xl overflow-hidden">
          <div className="flex items-center justify-between px-4 py-3 bg-content2 border-b border-divider">
            <div className="flex items-center gap-2">
              <Upload size={16} className="text-accent-primary" />
              <span className="font-medium text-sm">
                {isUploading ? `上传中 (${uploadQueue.filter(i => i.status === 'done').length}/${uploadQueue.length})` : '上传完成'}
              </span>
            </div>
            <div className="flex items-center gap-1">
              {!isUploading && (
                <button 
                  onClick={() => setUploadQueue([])}
                  className="px-2 py-1 text-xs text-default-500 hover:text-default-700 hover:bg-content3 rounded transition-colors"
                >
                  清空
                </button>
              )}
              <button 
                onClick={() => setShowUploadPanel(false)}
                className="p-1.5 hover:bg-content3 rounded transition-colors"
              >
                <X size={14} className="text-default-500" />
              </button>
            </div>
          </div>
          <div className="max-h-72 overflow-y-auto">
            {uploadQueue.map((item, i) => (
              <div key={i} className="px-4 py-3 border-b border-divider last:border-b-0 hover:bg-content2/50 transition-colors">
                <div className="flex items-center gap-2.5 mb-1.5">
                  {item.status === 'done' && <CheckCircle2 size={16} className="text-emerald-500 flex-shrink-0" />}
                  {item.status === 'error' && <AlertCircle size={16} className="text-rose flex-shrink-0" />}
                  {(item.status === 'pending' || item.status === 'uploading') && (
                    <div className="w-4 h-4 border-2 border-accent-primary border-t-transparent rounded-full animate-spin flex-shrink-0" />
                  )}
                  <span className="text-sm truncate flex-1" title={item.relativePath || item.file.name}>
                    {item.relativePath || item.file.name}
                  </span>
                  {item.status === 'uploading' && (
                    <span className="text-xs text-default-400">{Math.round(item.progress)}%</span>
                  )}
                </div>
                {item.status === 'uploading' && (
                  <Progress 
                    size="sm" 
                    value={item.progress} 
                    classNames={{ 
                      base: "h-1.5",
                      indicator: "bg-accent-primary"
                    }} 
                  />
                )}
                {item.status === 'error' && (
                  <p className="text-xs text-rose mt-1">{item.error}</p>
                )}
              </div>
            ))}
          </div>
          {/* Summary footer */}
          {!isUploading && uploadQueue.length > 0 && (
            <div className="px-4 py-2.5 bg-content2 border-t border-divider text-xs text-default-500">
              共 {uploadQueue.length} 个文件，
              成功 {uploadQueue.filter(i => i.status === 'done').length} 个
              {uploadQueue.filter(i => i.status === 'error').length > 0 && (
                <span className="text-rose">，失败 {uploadQueue.filter(i => i.status === 'error').length} 个</span>
              )}
            </div>
          )}
        </div>
      )}

      <div className="flex-1 flex flex-col min-w-0 p-7">
        <input ref={fileInputRef} type="file" multiple className="hidden" onChange={handleUploadInputChange} />
        {/* @ts-expect-error - webkitdirectory is a non-standard attribute */}
        <input ref={folderInputRef} type="file" webkitdirectory="" directory="" multiple className="hidden" onChange={handleUploadInputChange} />
        
        {/* Breadcrumbs */}
        <Breadcrumbs path={currentPath} onNavigate={setCurrentPath} />
        
        {/* Toolbar */}
        <div className="flex items-center justify-between mb-6">
          <div className="flex items-center gap-3">
            {hasSelection ? (
              <>
                <div className="flex items-center gap-2 px-3 py-1.5 rounded-xl bg-content2 border border-divider text-sm">
                  <span className="text-default-600">已选</span>
                  <span className="font-semibold text-foreground">{selectedFiles.size}</span>
                  <span className="text-default-600">项</span>
                  <span className="text-default-400">({selectedCounts.files} 文件 / {selectedCounts.folders} 文件夹)</span>
                  <Dropdown>
                    <DropdownTrigger>
                      <button className="ml-1 px-2 py-0.5 text-xs text-default-500 hover:text-default-700 hover:bg-content3 rounded-lg transition-colors">
                        选择工具
                      </button>
                    </DropdownTrigger>
                    <DropdownMenu
                      aria-label="选择工具"
                      classNames={{ base: "bg-content1 border border-divider shadow-lg" }}
                    >
                      <DropdownSection title="快捷键" showDivider>
                        <DropdownItem key="range" isDisabled>
                          Shift: 范围选择
                        </DropdownItem>
                        <DropdownItem key="multi" isDisabled>
                          Ctrl/Cmd: 追加选择
                        </DropdownItem>
                        <DropdownItem key="clear-shortcut" isDisabled>
                          Esc: 清空选择
                        </DropdownItem>
                      </DropdownSection>
                      <DropdownItem key="clear" onPress={clearSelection} startContent={<X size={14} />}>
                        清空选择
                      </DropdownItem>
                      <DropdownItem key="invert" onPress={handleInvertSelection} startContent={<RotateCcw size={14} />}>
                        反选
                      </DropdownItem>
                      <DropdownItem key="only-files" onPress={handleSelectOnlyFiles} isDisabled={totalCounts.files === 0} startContent={<Files size={14} />}>
                        {totalCounts.files === 0 ? '仅文件（无文件）' : '仅文件'}
                      </DropdownItem>
                      <DropdownItem key="only-folders" onPress={handleSelectOnlyFolders} isDisabled={totalCounts.folders === 0} startContent={<Folder size={14} />}>
                        {totalCounts.folders === 0 ? '仅文件夹（无文件夹）' : '仅文件夹'}
                      </DropdownItem>
                    </DropdownMenu>
                  </Dropdown>
                </div>
                <Button 
                  variant="bordered" 
                  className="btn-secondary btn-sm rounded-xl"
                  startContent={<X size={16} />}
                  onPress={clearSelection}
                >
                  取消选择 ({selectedFiles.size})
                </Button>
                <Button 
                  color="primary"
                  variant="flat" 
                  className="rounded-xl"
                  startContent={<Download size={16} />}
                  onPress={handleBatchDownload}
                  isDisabled={!hasDownloadableSelection}
                >
                  批量下载（仅文件）
                </Button>
                <span className="text-xs text-default-500">
                  可下载 {selectedCounts.files} 个
                </span>
                {canWrite && (
                  <Button 
                    variant="bordered" 
                    className="btn-secondary btn-sm rounded-xl text-default-500"
                    startContent={<Move size={16} />}
                    onPress={() => handleOpenMoveModal(selectedFileItems)}
                  >
                    批量移动
                  </Button>
                )}
                {canWrite && (
                  <Button 
                    variant="bordered" 
                    className="btn-secondary btn-sm rounded-xl text-default-500"
                    startContent={<Files size={16} />}
                    onPress={() => handleOpenCopyModal(selectedFileItems)}
                  >
                    批量复制
                  </Button>
                )}
                {canWrite && (
                  <Button 
                    color="danger"
                    variant="flat"
                    className="rounded-xl"
                    startContent={<Trash2 size={16} />}
                    onPress={onBatchDeleteOpen}
                  >
                    批量删除
                  </Button>
                )}
                <div className="flex flex-col gap-0.5">
                  <span className="text-xs text-default-400">删除将进入回收站</span>
                  {!canWrite && (
                    <span className="text-xs text-default-400">访客账户为只读，仅可查看和下载</span>
                  )}
                  {!hasDownloadableSelection && selectedCounts.files > 0 && selectedCounts.folders > 0 && (
                    <span className="text-xs text-default-400 flex items-center gap-1">
                      <Folder size={12} />
                      包含文件夹时无法批量下载
                    </span>
                  )}
                </div>
              </>
            ) : (
              <>
                {canWrite ? (
                  <>
                    <Button 
                      className="btn-primary btn-md border-none font-medium rounded-xl"
                      startContent={<Upload size={16} />}
                      onPress={() => fileInputRef.current?.click()}
                      isLoading={isUploading}
                    >
                      {isUploading ? '上传中...' : '上传文件'}
                    </Button>
                    <Button 
                      variant="bordered" 
                      className="btn-secondary btn-md rounded-xl"
                      startContent={<FolderUp size={16} />}
                      onPress={() => folderInputRef.current?.click()}
                      isLoading={isUploading}
                      isDisabled={isUploading}
                    >
                      上传文件夹
                    </Button>
                    <Button 
                      variant="bordered" 
                      className="btn-secondary btn-md rounded-xl"
                      startContent={<FolderPlus size={16} />}
                      onPress={handleOpenNewFolderModal}
                    >
                      新建空间
                    </Button>
                  </>
                ) : (
                  <div className="rounded-xl border border-divider bg-content1 px-4 py-2 text-sm text-default-500">
                    访客账户为只读，仅可查看、预览和下载文件
                  </div>
                )}
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
          
          {/* Upload history button */}
          {uploadQueue.length > 0 && (
            <button 
              onClick={() => setShowUploadPanel(!showUploadPanel)}
              className={cn(
                "relative p-2.5 rounded-xl border transition-all",
                showUploadPanel 
                  ? "bg-accent-primary text-white border-accent-primary shadow-sm" 
                  : "bg-content1 border-divider text-default-500 hover:text-default-600 hover:border-default-400"
              )}
              title="上传记录"
            >
              <Upload size={16} />
              {isUploading && (
                <span className="absolute -top-1 -right-1 w-2.5 h-2.5 bg-accent-primary rounded-full animate-pulse" />
              )}
              {!isUploading && uploadQueue.filter(i => i.status === 'error').length > 0 && (
                <span className="absolute -top-1 -right-1 w-2.5 h-2.5 bg-rose rounded-full" />
              )}
            </button>
          )}
        </div>

        {/* File List / Grid */}
        {canWrite && favoritesError && (
          <div className="mb-4 flex items-start gap-3 rounded-2xl border border-warning/30 bg-warning/5 px-4 py-3 text-sm text-foreground">
            <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" />
            <div className="flex-1">
              <p className="font-medium">{favoritesBanner?.title ?? '收藏状态加载失败'}</p>
              <p className="text-default-600">{favoritesBanner?.description ?? '请稍后重试'}</p>
            </div>
            <Button size="sm" variant="bordered" className="rounded-xl" onPress={handleRefreshFavoritesBanner}>
              重新加载收藏状态
            </Button>
          </div>
        )}

        {canWrite && shareBanner && (
          <div className="mb-4 flex items-start gap-3 rounded-2xl border border-warning/30 bg-warning/5 px-4 py-3 text-sm text-foreground">
            <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" />
            <div className="flex-1">
              <p className="font-medium">{shareBanner.title}</p>
              <p className="text-default-600">{shareBanner.description}</p>
            </div>
          </div>
        )}

        {viewMode === 'list' ? (
          <div className="flex-1 surface-card overflow-hidden flex flex-col">
            {/* Header */}
            <div className="grid grid-cols-[44px_1fr_100px_150px_120px_40px] gap-4 px-5 py-3 table-head text-[11px] font-semibold">
              <div className="flex items-center justify-center">
                <div 
                  className={cn(
                    "w-5 h-5 border-2 rounded-lg cursor-pointer transition-colors",
                    isAllSelected || isPartialSelected ? "bg-accent-primary border-accent-primary" : "border-default-400 hover:border-accent-primary"
                  )}
                  onClick={handleSelectAll}
                >
                  {isAllSelected && <span className="text-white text-xs font-bold">✓</span>}
                  {isPartialSelected && <span className="text-white text-xs font-bold">-</span>}
                </div>
              </div>
              <div>名称</div>
              <div>大小</div>
              <div>修改时间</div>
              <div>时光印记</div>
              <div className="flex items-center justify-end">
                {showMultiSelectHint && (
                  <div className="flex items-center gap-2 animate-in fade-in duration-150">
                    <span className="text-[10px] text-default-500 bg-content2 border border-divider rounded-full px-2 py-0.5">
                      多选模式已启用
                    </span>
                    {hasSelection && (
                      <span className="text-[10px] text-default-400">
                        Esc 清空选择
                      </span>
                    )}
                  </div>
                )}
              </div>
            </div>

            {/* List Content */}
            <div
              ref={parentRef}
              className="flex-1 overflow-auto custom-scrollbar relative"
              onClick={() => {
                if (selectedFiles.size > 0) {
                  setMultiSelectHintVisible(true)
                  if (hintTimeoutRef.current) {
                    clearTimeout(hintTimeoutRef.current)
                  }
                  hintTimeoutRef.current = setTimeout(() => {
                    setMultiSelectHintVisible(false)
                    hintTimeoutRef.current = null
                  }, 1500)
                }
                clearSelection()
                setActiveFilePath(null)
                setFocusedIndex(-1)
                lastSelectedIndexRef.current = null
              }}
            >
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
                        favoriteActionsAvailable={favoriteActionsAvailable}
                        favoriteUnavailableLabel={favoriteUnavailableLabel}
                        shareActionsAvailable={shareActionsAvailable}
                        shareActionLabel={shareActionLabel}
                        isMultiSelection={hasMultiSelection}
                        canWrite={canWrite}
                        onSelect={(e) => handleFileSelection(file, virtualItem.index, e, 'toggle')}
                        onOpen={() => handleFileOpen(file)}
                        onClick={(e) => handleFileSelection(file, virtualItem.index, e)}
                        onRename={() => handleOpenRenameModal(file)}
                        onDelete={() => handleOpenDeleteModal(file)}
                        onViewVersions={() => handleViewVersions(file)}
                        onShare={() => handleOpenShareModal(file)}
                        onToggleFavorite={() => favoriteMutation.mutate({ 
                          path: file.path, 
                          isFavorited: favoritesData?.[file.path] ?? false 
                        })}
                        onContextMenu={(e) => handleContextMenu(file, e)}
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
          <div
            className="flex-1 overflow-auto custom-scrollbar"
            onClick={() => {
              if (selectedFiles.size > 0) {
                setMultiSelectHintVisible(true)
                if (hintTimeoutRef.current) {
                  clearTimeout(hintTimeoutRef.current)
                }
                hintTimeoutRef.current = setTimeout(() => {
                  setMultiSelectHintVisible(false)
                  hintTimeoutRef.current = null
                }, 1500)
              }
              clearSelection()
              setActiveFilePath(null)
              setFocusedIndex(-1)
              lastSelectedIndexRef.current = null
            }}
          >
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
              <div>
                {showMultiSelectHint && (
                  <div className="mb-3 animate-in fade-in duration-150">
                    <span className="text-[11px] text-default-500 bg-content2 border border-divider rounded-full px-2.5 py-1">
                      多选模式已启用
                    </span>
                  </div>
                )}
                <div className="grid grid-cols-[repeat(auto-fill,minmax(160px,1fr))] gap-4">
                  {sortedFiles.map((file, index) => (
                  <FileCard
                    key={file.path}
                    file={file}
                    isSelected={selectedFiles.has(file.path)}
                    isFavorited={favoritesData?.[file.path] ?? false}
                    favoriteActionsAvailable={favoriteActionsAvailable}
                    favoriteUnavailableLabel={favoriteUnavailableLabel}
                    shareActionsAvailable={shareActionsAvailable}
                    shareActionLabel={shareActionLabel}
                    isMultiSelection={hasMultiSelection}
                    canWrite={canWrite}
                    onSelect={(e) => handleFileSelection(file, index, e, 'toggle')}
                    onOpen={() => handleFileOpen(file)}
                    onClick={(e) => handleFileSelection(file, index, e)}
                    onRename={() => handleOpenRenameModal(file)}
                    onDelete={() => handleOpenDeleteModal(file)}
                    onViewVersions={() => handleViewVersions(file)}
                    onShare={() => handleOpenShareModal(file)}
                    onToggleFavorite={() => favoriteMutation.mutate({ 
                      path: file.path, 
                      isFavorited: favoritesData?.[file.path] ?? false 
                    })}
                    onContextMenu={(e) => handleContextMenu(file, e)}
                  />
                  ))}
                </div>
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
        onClose={handleCloseNewFolderModal}
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
            <Button variant="flat" onPress={handleCloseNewFolderModal} isDisabled={createFolderMutation.isPending} className="text-default-600 rounded-xl">取消</Button>
            <Button color="primary" onPress={handleCreateFolder} isLoading={createFolderMutation.isPending} isDisabled={!newFolderName.trim()} className="rounded-xl">创建</Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      <Modal
        isOpen={isRenameOpen}
        onClose={handleCloseRenameModal}
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
            <Button variant="flat" onPress={handleCloseRenameModal} isDisabled={renameMutation.isPending} className="text-default-600 rounded-xl">取消</Button>
            <Button color="primary" onPress={handleRename} isLoading={renameMutation.isPending} isDisabled={!renameValue.trim() || renameValue.trim() === actionFile?.name} className="rounded-xl">确定</Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      <Modal
        isOpen={isDeleteOpen}
        onClose={handleCloseDeleteModal}
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
            <p className="text-default-600">确定要删除 <strong className="text-foreground">{deleteTarget?.name}</strong> 吗？</p>
            <p className="text-xs text-default-500 mt-2">文件将被移入回收站，可在回收站中恢复。</p>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button variant="flat" onPress={handleCloseDeleteModal} isDisabled={deleteMutation.isPending} className="text-default-600 rounded-xl">取消</Button>
            <Button color="danger" onPress={handleDelete} isLoading={deleteMutation.isPending} className="rounded-xl">删除</Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      <Modal
        isOpen={isBatchDeleteOpen}
        onClose={handleCloseBatchDeleteModal}
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
            <p className="text-xs text-default-500 mt-2">文件将被移入回收站，可在回收站中恢复。</p>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button variant="flat" onPress={handleCloseBatchDeleteModal} isDisabled={isBatchDeleting} className="text-default-600 rounded-xl">取消</Button>
            <Button color="danger" onPress={handleBatchDelete} isLoading={isBatchDeleting} className="rounded-xl">删除全部</Button>
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
        isFolder={shareFile?.isDir}
        featureEnabled={shareActionsAvailable}
        onFeatureDisabled={() => setShareFeatureDisabled(true)}
      />

      {/* Move/Copy Dialog */}
      <MoveDialog
        isOpen={isMoveOpen}
        onClose={() => {
          onMoveClose()
          setMoveFiles([])
        }}
        files={moveFiles}
        currentPath={currentPath}
        mode={moveMode}
      />

      {/* File Preview Modal */}
      <PreviewModal
        isOpen={isPreviewOpen}
        onClose={() => {
          onPreviewClose()
          setPreviewFile(null)
        }}
        file={previewFile}
        files={previewFiles}
        onFileChange={(file) => {
          setPreviewFile(file)
          setActiveFilePath(file.path)
        }}
      />

      {/* Context Menu */}
      <ContextMenu
        isOpen={contextMenu.state.isOpen}
        position={contextMenu.state.position}
        onClose={contextMenu.hide}
      >
        {contextMenuFile && (
          <>
            {hasMultiSelection && selectedFiles.has(contextMenuFile.path) ? (
              <ContextMenuSection title={`已选 ${selectedFiles.size} 项`} showDivider>
                <ContextMenuItem
                  icon={<X size={16} />}
                  onClick={() => {
                    clearSelection()
                    contextMenu.hide()
                  }}
                >
                  清空选择
                </ContextMenuItem>
                <ContextMenuItem
                  icon={<Files size={16} />}
                  onClick={() => {
                    handleInvertSelection()
                    contextMenu.hide()
                  }}
                >
                  反选
                </ContextMenuItem>
                <ContextMenuItem
                  icon={<Download size={16} />}
                  onClick={() => {
                    handleSelectOnlyFiles()
                    contextMenu.hide()
                  }}
                  disabled={totalCounts.files === 0}
                >
                  仅文件（{totalCounts.files}）
                </ContextMenuItem>
                <ContextMenuItem
                  icon={<FolderOpen size={16} />}
                  onClick={() => {
                    handleSelectOnlyFolders()
                    contextMenu.hide()
                  }}
                  disabled={totalCounts.folders === 0}
                >
                  仅文件夹（{totalCounts.folders}）
                </ContextMenuItem>
                <ContextMenuItem
                  icon={<Download size={16} />}
                  onClick={() => {
                    handleBatchDownload()
                    contextMenu.hide()
                  }}
                  disabled={!hasDownloadableSelection}
                >
                  批量下载（仅文件）
                </ContextMenuItem>
                {canWrite && (
                  <ContextMenuItem
                    icon={<Move size={16} />}
                    onClick={() => {
                      handleOpenMoveModal(selectedFileItems)
                      contextMenu.hide()
                    }}
                    disabled={selectedFileItems.length === 0}
                  >
                    批量移动{selectedFileItems.length === 0 ? '（无可移动项）' : ''}
                  </ContextMenuItem>
                )}
                {canWrite && (
                  <ContextMenuItem
                    icon={<Files size={16} />}
                    onClick={() => {
                      handleOpenCopyModal(selectedFileItems)
                      contextMenu.hide()
                    }}
                    disabled={selectedFileItems.length === 0}
                  >
                    批量复制{selectedFileItems.length === 0 ? '（无可复制项）' : ''}
                  </ContextMenuItem>
                )}
                {canWrite && (
                  <ContextMenuItem
                    icon={<Trash2 size={16} />}
                    danger
                    onClick={() => {
                      onBatchDeleteOpen()
                      contextMenu.hide()
                    }}
                  >
                    批量删除（进回收站）
                  </ContextMenuItem>
                )}
              </ContextMenuSection>
            ) : (
              <>
                <ContextMenuSection title="操作" showDivider>
                  {contextMenuFile.isDir ? (
                    <ContextMenuItem
                      icon={<FolderOpen size={16} />}
                      onClick={() => {
                        setCurrentPath(contextMenuFile.path)
                        contextMenu.hide()
                      }}
                    >
                      打开文件夹
                    </ContextMenuItem>
                  ) : (
                    <ContextMenuItem
                      icon={<Download size={16} />}
                      onClick={handleContextMenuDownload}
                    >
                      下载
                    </ContextMenuItem>
                  )}
                  {canWrite && (
                    <ContextMenuItem
                      icon={<Pencil size={16} />}
                      onClick={() => {
                        handleOpenRenameModal(contextMenuFile)
                        contextMenu.hide()
                      }}
                    >
                      重命名
                    </ContextMenuItem>
                  )}
                  {canWrite && (
                    <ContextMenuItem
                      icon={<Move size={16} />}
                      onClick={() => {
                        handleOpenMoveModal([contextMenuFile])
                        contextMenu.hide()
                      }}
                    >
                      移动到...
                    </ContextMenuItem>
                  )}
                  {canWrite && (
                    <ContextMenuItem
                      icon={<Files size={16} />}
                      onClick={() => {
                        handleOpenCopyModal([contextMenuFile])
                        contextMenu.hide()
                      }}
                    >
                      复制到...
                    </ContextMenuItem>
                  )}
                  <ContextMenuItem
                    icon={<Copy size={16} />}
                    onClick={handleContextMenuCopyPath}
                  >
                    复制路径
                  </ContextMenuItem>
                </ContextMenuSection>
                <ContextMenuSection title={canWrite ? '分享' : '历史'} showDivider>
                  {canWrite && (
                    <ContextMenuItem
                      icon={<Star size={16} className={favoritesData?.[contextMenuFile.path] ? "fill-accent-primary text-accent-primary" : ""} />}
                      disabled={!favoriteActionsAvailable}
                      onClick={() => {
                        favoriteMutation.mutate({ 
                          path: contextMenuFile.path, 
                          isFavorited: favoritesData?.[contextMenuFile.path] ?? false 
                        })
                        contextMenu.hide()
                      }}
                    >
                      {favoriteActionsAvailable ? (favoritesData?.[contextMenuFile.path] ? '取消收藏' : '添加收藏') : favoriteUnavailableLabel}
                    </ContextMenuItem>
                  )}
                  {canWrite && (
                    <ContextMenuItem
                      icon={<Link2 size={16} />}
                      disabled={!shareActionsAvailable}
                      onClick={() => {
                        handleOpenShareModal(contextMenuFile)
                        contextMenu.hide()
                      }}
                    >
                      {shareActionLabel}
                    </ContextMenuItem>
                  )}
                  <ContextMenuItem
                    icon={<History size={16} />}
                    onClick={() => {
                      handleViewVersions(contextMenuFile)
                      contextMenu.hide()
                    }}
                    disabled={contextMenuFile.isDir}
                  >
                    查看版本历史
                  </ContextMenuItem>
                </ContextMenuSection>
                {canWrite && (
                  <ContextMenuSection>
                    <ContextMenuItem
                      icon={<Trash2 size={16} />}
                      danger
                      onClick={() => {
                        handleOpenDeleteModal(contextMenuFile)
                        contextMenu.hide()
                      }}
                    >
                      删除
                    </ContextMenuItem>
                  </ContextMenuSection>
                )}
              </>
            )}
          </>
        )}
      </ContextMenu>
    </div>
  )
}
