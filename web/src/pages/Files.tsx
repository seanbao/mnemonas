import { useMemo, useCallback, useEffect, useRef, useState } from 'react'
import type { InputHTMLAttributes } from 'react'
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
  ArrowDownWideNarrow,
  ArrowUpDown,
  ArrowUpNarrowWide,
} from 'lucide-react'
import { ShareDialog } from '@/components/share'
import { FileIcon } from '@/components/ui/FileIcon'
import { EmptyState } from '@/components/ui/EmptyState'
import { ContextMenu, ContextMenuSection, ContextMenuItem } from '@/components/ui/ContextMenu'
import { MoveDialog } from '@/components/file'
import { PreviewModal, type PreviewFile } from '@/components/preview'
import { useLocation, useNavigate } from 'react-router-dom'
import { useFilesStore, type FileCapabilities, type FileItem } from '@/stores/files'
import { useClipboardStore } from '@/stores/clipboard'
import { useCanWrite, useShareEnabled, useUser } from '@/stores/auth'
import { useContextMenu, useKeyboardShortcuts } from '@/hooks'
import { useDestructiveDialogFocus } from '@/hooks/useDestructiveDialogFocus'
import {
  listFiles,
  createFileDeleteIntent,
  deleteFile,
  createDirectory,
  uploadFile,
  moveFile,
  copyFile,
  downloadFile,
  ApiError,
  MAX_UPLOAD_FILE_SIZE_BYTES,
  MAX_UPLOAD_FILE_SIZE_LABEL,
  MAX_DELETE_INTENT_TARGETS,
  type ActionResult,
  type DeleteFileOptions,
  type DeleteMode,
  type FileListResponse,
  type FileDeleteTarget,
} from '@/api/files'
import { checkFavorites, toggleFavorite } from '@/api/favorites'
import { listShares, ShareError } from '@/api/share'
import { getFileQueryScopeKey, getFilesQueryKey } from '@/lib/fileQueryKey'
import { GENERIC_LOAD_ERROR_DESCRIPTION, getUserFacingErrorDescription } from '@/lib/apiMessages'
import {
  buildUploadQueue,
  getUploadPanelTitle,
  getUploadQueueCounts,
  normalizeUploadProgress,
  type UploadQueueItem,
} from '@/lib/uploadQueue'
import {
  getFileDownloadErrorToast,
  getPathConflictErrorToast,
  getQuotaExceededErrorToast,
  getSharedArchiveDownloadErrorToast,
  getSharedMissingFileDownloadErrorToast,
  getSharedPathConflictErrorToast,
  getSharedQuotaExceededErrorToast,
} from '@/lib/fileActionErrors'
import { getPathSegmentNameValidationError, joinPathSegment } from '@/lib/pathSegmentName'
import { copyTextToClipboard, decodePathFromUrl, encodePathForUrl, formatBytes, formatDate, cn, normalizePath } from '@/lib/utils'
import { getInvalidHomeDirDescription, invalidHomeDirTitle, resolveUserHomeScope } from '@/lib/userScope'

type SortKey = 'name' | 'size' | 'modTime'
type DirectoryUploadInputProps = InputHTMLAttributes<HTMLInputElement> & {
  webkitdirectory: string
  directory: string
}

const UPLOAD_HISTORY_SUCCESS_RETENTION_MS = 15000

const directoryUploadInputProps = {
  type: 'file',
  webkitdirectory: '',
  directory: '',
  multiple: true,
  className: 'hidden',
  'aria-label': '选择上传文件夹',
} satisfies DirectoryUploadInputProps

const sortLabels: Record<SortKey, string> = {
  name: '名称',
  size: '大小',
  modTime: '修改时间',
}

function getFileTypeLabel(file: FileItem): string {
  if (file.isDir) {
    return '文件夹'
  }

  const dotIndex = file.name.lastIndexOf('.')
  if (dotIndex <= 0 || dotIndex === file.name.length - 1) {
    return '文件'
  }

  return file.name.slice(dotIndex + 1).toUpperCase()
}

function getDownloadOptions(file: FileItem): Parameters<typeof downloadFile>[1] {
  return file.isDir
    ? { archive: 'zip', filename: `${file.name}.zip` }
    : { filename: file.name }
}

function getDownloadOptionsWithSignal(file: FileItem, signal: AbortSignal): Parameters<typeof downloadFile>[1] {
  return {
    ...getDownloadOptions(file),
    signal,
  }
}

function isDirectoryAlreadyExistsError(error: unknown): boolean {
  return error instanceof ApiError && error.status === 409
}

function getFilesRoutePath(filePath: string): string {
  const normalizedPath = normalizePath(filePath)
  return normalizedPath === '/' ? '/files' : `/files${encodePathForUrl(normalizedPath)}`
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
        description: '收藏存储未成功初始化，请检查设备状态或稍后重试。',
      }
    }
  }

  return {
    title: '收藏状态加载失败',
    description: getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION),
  }
}

function getFilesLoadErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: '当前目录暂不可用',
      description: '文件系统当前不可用，请检查设备状态或稍后重试。',
    }
  }

  return {
    title: '当前目录加载失败',
    description: getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION),
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

function isAbortError(error: unknown): boolean {
  return typeof error === 'object'
    && error !== null
    && 'name' in error
    && (error as { name?: unknown }).name === 'AbortError'
}

type CreateFolderMutationVariables = {
  path: string
  directoryPath: string
  folderName: string
  sessionId: number
  signal: AbortSignal
}

type RenameMutationVariables = {
  from: string
  to: string
  directoryPath: string
  targetPath: string
  submittedName: string
  sessionId: number
  signal: AbortSignal
}

type DeleteMutationVariables = {
  path: string
  expectedDeleteMode: DeleteMode
  expectedDeletePolicyToken: string
  expectedDeleteTargetToken: string
  signal: AbortSignal
}

type KnownDeletePolicy = {
  mode: DeleteMode
  token: string
  trashRetentionDays: number
  trashAutoCleanupEnabled: boolean
}

type SingleDeleteIntent = {
  target: FileDeleteTarget
  policy: KnownDeletePolicy
  directoryPath: string
}

type BatchDeleteIntent = {
  targets: FileDeleteTarget[]
  policy: KnownDeletePolicy
  directoryPath: string
}

type DeleteTargetSnapshot = Pick<FileItem, 'path' | 'name' | 'isDir' | 'size' | 'modTime' | 'deleteIdentityToken'>

function snapshotDeleteTarget(file: FileItem): DeleteTargetSnapshot {
  return {
    path: file.path,
    name: file.name,
    isDir: file.isDir,
    size: file.size,
    modTime: file.modTime,
    deleteIdentityToken: file.deleteIdentityToken,
  }
}

function deleteTargetMatchesSnapshot(target: FileDeleteTarget, snapshot: DeleteTargetSnapshot): boolean {
  return target.path === snapshot.path
    && target.name === snapshot.name
    && target.isDir === snapshot.isDir
    && target.size === snapshot.size
    && target.modTime === snapshot.modTime
    && target.deleteIdentityToken === snapshot.deleteIdentityToken
}

function deleteTargetsMatchSnapshots(targets: FileDeleteTarget[], snapshots: DeleteTargetSnapshot[]): boolean {
  return targets.length === snapshots.length
    && targets.every((target, index) => deleteTargetMatchesSnapshot(target, snapshots[index]!))
}

type BatchDeleteDriftSummary = {
  mode: DeleteMode
  deletedCount: number
  synchronizedMissingCount: number
  remainingCount: number
}

function isDeletePolicyChangedError(error: unknown): boolean {
  return getErrorStatus(error) === 409 && getErrorCode(error) === 'DELETE_POLICY_CHANGED'
}

function isDeleteTargetChangedError(error: unknown): boolean {
  return getErrorStatus(error) === 409 && getErrorCode(error) === 'DELETE_TARGET_CHANGED'
}

function getKnownDeletePolicy(intent: {
  deleteMode: DeleteMode
  deletePolicyToken: string
  trashRetentionDays: number
  trashAutoCleanupEnabled: boolean
}): KnownDeletePolicy {
  return {
    mode: intent.deleteMode,
    token: intent.deletePolicyToken,
    trashRetentionDays: intent.trashRetentionDays,
    trashAutoCleanupEnabled: intent.trashAutoCleanupEnabled,
  }
}

function getDeleteActionLabel(mode: DeleteMode, batch = false): string {
  if (mode === 'permanent') {
    return batch ? '批量永久删除' : '永久删除'
  }
  return batch ? '批量移入回收站' : '移入回收站'
}

function getDeletePolicyDescription(policy: KnownDeletePolicy): string {
  if (policy.mode === 'permanent') {
    return '不会进入回收站，删除后无法恢复。此操作无法撤销。'
  }

  if (!policy.trashAutoCleanupEnabled) {
    return '当前未启用按到期时间自动清理；回收站容量不足时，较早项目仍可能提前清理。'
  }

  if (policy.trashRetentionDays === 0) {
    return '当前策略下，新删除项目会立即到期并等待后台清理；回收站容量不足时也可能提前清理。'
  }

  return `当前策略下，新删除项目将在 ${policy.trashRetentionDays} 天后到期；到期项目由后台清理周期处理，回收站容量不足时也可能提前清理。`
}

function getDeleteConsequence(mode: DeleteMode, batch = false): string {
  if (mode === 'permanent') {
    return batch
      ? '选中项目不会进入回收站，删除后无法恢复。此操作无法撤销。'
      : '文件不会进入回收站，删除后无法恢复。此操作无法撤销。'
  }

  return batch
    ? '选中项目将移入回收站，不会立即永久删除。'
    : '将移入回收站，不会立即永久删除。'
}

function getBatchDeleteDriftDescription(summary: BatchDeleteDriftSummary): string {
  const parts: string[] = []
  if (summary.deletedCount > 0) {
    parts.push(summary.mode === 'trash'
      ? `已移入回收站 ${summary.deletedCount} 项`
      : `已永久删除 ${summary.deletedCount} 项`)
  }
  if (summary.synchronizedMissingCount > 0) {
    parts.push(`已同步移除 ${summary.synchronizedMissingCount} 个不存在项目`)
  }
  parts.push(`${summary.remainingCount} 项未删除`)
  return parts.join('；')
}

type FavoriteMutationVariables = {
  path: string
  isFavorited: boolean
  signal: AbortSignal
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
      description: '文件系统当前不可用，请检查设备状态或稍后重试。',
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
    description: getUserFacingErrorDescription(error),
    color: 'danger',
  }
}

const missingFileSyncWarningTitle = '文件或文件夹已不存在，已同步更新'

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
      title: result.message === missingFileSyncWarningTitle ? missingFileSyncWarningTitle : titles.warning,
      color: 'warning',
    }
  }

  return {
    title: titles.success,
    color: 'success',
  }
}

function isDirectoryAlreadyExistsResult(result: ActionResult): boolean {
  return result.warning !== true && result.message === 'directory already exists'
}

function getCreateFolderSuccessToast(result: ActionResult): {
  title: string
  description?: string
  color: 'success' | 'warning'
} {
  if (isDirectoryAlreadyExistsResult(result)) {
    return {
      title: '文件夹已存在，已同步更新',
      color: 'warning',
    }
  }

  return getFilesActionSuccessToast(result, {
    success: '文件夹创建成功',
    warning: '文件夹创建完成，但存在警告',
  })
}

function getCreateFolderErrorToast(error: unknown): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  return getFilesActionErrorToast(error, {
    unavailable: '创建暂不可用',
    failure: '创建失败',
  })
}

type MissingFileActionResult = ActionResult & { staleMissing: true }

function getMissingFileActionResult(): MissingFileActionResult {
  return {
    warning: true,
    message: missingFileSyncWarningTitle,
    staleMissing: true,
  }
}

function isMissingFileActionResult(result: ActionResult): result is MissingFileActionResult {
  return !!result && typeof result === 'object' && 'staleMissing' in result && result.staleMissing === true
}

function getActionWarningSummary(result: ActionResult): string {
  return isMissingFileActionResult(result) ? missingFileSyncWarningTitle : ''
}

function getSynchronizedWarningTitle(warningMessages: string[]): string | null {
  return warningMessages.find((message) => message === missingFileSyncWarningTitle) ?? null
}

async function withMissingFileActionResult(operation: () => Promise<ActionResult>): Promise<ActionResult> {
  try {
    return await operation()
  } catch (error) {
    if (getErrorStatus(error) === 404) {
      return getMissingFileActionResult()
    }

    throw error
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
      description: '收藏存储未成功初始化，请检查设备状态或稍后重试。',
      color: 'warning',
    }
  }

  return {
    title: '操作失败',
    description: getUserFacingErrorDescription(error),
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
      description: '收藏存储未成功初始化，请检查设备状态或稍后重试。',
      color: 'warning',
    }
  }

  return {
    title: '刷新失败',
    description: getUserFacingErrorDescription(error),
    color: 'danger',
  }
}

function getUploadQueueErrorMessage(error: unknown): string {
  if (isFilesystemUnavailableError(error)) {
    return '文件系统当前不可用，请检查设备状态或稍后重试。'
  }

  const quotaExceededToast = getQuotaExceededErrorToast(error)
  if (quotaExceededToast) {
    return quotaExceededToast.description
  }

  return getUserFacingErrorDescription(error)
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
        title: '文件夹上传完成，但存在警告',
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
        description: '文件系统当前不可用，请检查设备状态或稍后重试。',
        color: 'warning',
      }
    }

    const quotaExceededToast = getSharedQuotaExceededErrorToast(errors)
    if (quotaExceededToast) {
      return quotaExceededToast
    }

    return {
      title: '文件夹上传失败',
      description: `共 ${errorCount} 个文件上传失败`,
      color: 'danger',
    }
  }

  const quotaExceededToast = getSharedQuotaExceededErrorToast(errors)
  const baseDescription = `成功上传 ${successCount} 个文件，失败 ${errorCount} 个`

  return {
    title: getSynchronizedWarningTitle(warningMessages) ?? '文件夹上传部分完成',
    description: quotaExceededToast?.description
      ? `${baseDescription}；${quotaExceededToast.description}`
      : baseDescription,
    color: 'warning',
  }
}

function getUploadWarningSummaryToast(successCount: number, warningMessages: string[]): {
  title: string
  description: string
  color: 'warning'
} {
  return {
    title: getSynchronizedWarningTitle(warningMessages) ?? '上传完成，但存在警告',
    description: `成功上传 ${successCount} 个文件`,
    color: 'warning',
  }
}

function getPartialBatchActionToast(
  title: string,
  successCount: number,
  errorCount: number,
  warningMessages: string[],
  failureDescription?: string
): {
  title: string
  description: string
  color: 'warning'
} {
  const baseDescription = `成功 ${successCount} 个，失败 ${errorCount} 个`

  return {
    title: getSynchronizedWarningTitle(warningMessages) ?? title,
    description: failureDescription ? `${baseDescription}；${failureDescription}` : baseDescription,
    color: 'warning',
  }
}

function getShareBannerContent(): { title: string; description: string } {
  return {
    title: '分享功能已关闭',
    description: '当前服务已关闭分享功能。请在设置中重新启用后再创建分享链接。',
  }
}

function getShareUnavailableBannerContent(): { title: string; description: string } {
  return {
    title: '分享功能暂不可用',
    description: '分享服务当前不可用，请检查设备状态或稍后重试。',
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

function hasWriteCapability(canWrite: boolean, capabilities?: FileCapabilities): boolean {
  return canWrite && (capabilities?.write ?? true)
}

function hasConcreteReadCapability(canWrite: boolean, capabilities?: FileCapabilities): boolean {
  return canWrite && (capabilities?.concreteRead ?? true)
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
    <nav className="mb-4 flex items-center gap-1 overflow-x-auto whitespace-nowrap px-1 pb-1 text-sm custom-scrollbar">
      <button
        type="button"
        onClick={() => onNavigate('/')}
        className={cn(
          "flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg transition-all max-w-[180px] truncate border border-transparent",
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
              type="button"
              onClick={() => onNavigate(segmentPath)}
              className={cn(
                "px-2.5 py-1.5 rounded-lg transition-all max-w-[180px] truncate border border-transparent",
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

function SelectionCheckboxButton({
  isSelected,
  isPartialSelected = false,
  label,
  onClick,
  className,
  visualClassName,
}: {
  isSelected: boolean
  isPartialSelected?: boolean
  label: string
  onClick: (e: React.MouseEvent) => void
  className?: string
  visualClassName?: string
}) {
  const isActive = isSelected || isPartialSelected

  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={isPartialSelected ? 'mixed' : isSelected}
      aria-label={label}
      className={cn(
        "group/selection flex h-9 w-9 cursor-pointer items-center justify-center rounded-lg transition-colors hover:bg-content2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-primary/30",
        className
      )}
      onClick={onClick}
    >
      <span
        className={cn(
          "flex h-5 w-5 items-center justify-center rounded-lg border-2 transition-all duration-150",
          isActive
            ? "border-accent-primary bg-accent-primary"
            : "border-default-400",
          visualClassName
        )}
      >
        {isSelected && <span className="text-xs font-bold text-white">✓</span>}
        {isPartialSelected && <span className="text-xs font-bold text-white">-</span>}
      </span>
    </button>
  )
}

// File row for list view
function FileRow({ 
  file, 
  isSelected, 
  isActive,
  isFavorited,
  favoriteActionsAvailable,
  favoriteUnavailableLabel,
  shareActionsAvailable,
  shareActionLabel,
  isMultiSelection,
  canWrite,
  canDelete,
  deletePreparing,
  canShareFile,
  onSelect, 
  onOpen,
  onActivate,
  onRename,
  onDelete,
  onViewVersions,
  onShare,
  onToggleFavorite,
  onDownload,
  onContextMenu,
}: { 
  file: FileItem
  isSelected: boolean
  isActive: boolean
  isFavorited: boolean
  favoriteActionsAvailable: boolean
  favoriteUnavailableLabel: string
  shareActionsAvailable: boolean
  shareActionLabel: string
  isMultiSelection: boolean
  canWrite: boolean
  canDelete: boolean
  deletePreparing: boolean
  canShareFile: boolean
  onSelect: (e: React.MouseEvent) => void
  onOpen: () => void
  onActivate: (e: React.MouseEvent) => void
  onRename: () => void
  onDelete: () => void
  onViewVersions: () => void
  onShare: () => void
  onToggleFavorite: () => void
  onDownload: () => void
  onContextMenu: (e: React.MouseEvent) => void
}) {
  const detailLabel = isSelected && isMultiSelection
    ? '多选中'
    : getFileTypeLabel(file)
  const handleDownload = useCallback(() => {
    onDownload()
  }, [onDownload])

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
      role="group"
      aria-label={`${file.name} 文件项`}
      className={cn(
        "group grid grid-cols-[36px_minmax(0,1fr)_36px] items-center gap-3 border-b border-divider px-3 py-3 cursor-pointer transition-all duration-150 sm:grid-cols-[44px_minmax(0,1fr)_88px_118px_40px] sm:gap-4 sm:px-5 md:grid-cols-[44px_minmax(0,1fr)_100px_150px_120px_40px]",
        "hover:bg-content2/60",
        isActive && !isSelected && "bg-content2/50",
        isSelected && "bg-accent-primary/10"
      )}
      onClick={(e) => {
        e.stopPropagation()
        onActivate(e)
      }}
      onDoubleClick={onOpen}
      onContextMenu={onContextMenu}
    >
      <div
        role="group"
        aria-label={`${file.name} 选择控制`}
        className="flex items-center justify-center"
        onClick={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
        onContextMenu={(e) => e.stopPropagation()}
      >
        <SelectionCheckboxButton
          isSelected={isSelected}
          label={`选择 ${file.name}`}
          onClick={(e) => {
            e.stopPropagation()
            onSelect(e)
          }}
          visualClassName={!isSelected ? "group-hover:border-accent-primary" : undefined}
        />
      </div>
      
      <div className="flex items-center gap-3.5 min-w-0">
        <FileIcon name={file.name} isDir={file.isDir} size={36} variant="tile" />
        <div className="min-w-0">
          <div className="font-medium text-foreground truncate text-[13px]">{file.name}</div>
          <div className="text-xs text-default-500 mt-0.5 truncate">
            {detailLabel}
          </div>
        </div>
      </div>
      
      <div className="hidden text-sm text-default-600 sm:block">
        {file.isDir ? '—' : formatBytes(file.size)}
      </div>
      
      <div className="hidden text-sm text-default-600 sm:block">
        {formatDate(file.modTime)}
      </div>
      
      <div className="hidden items-center gap-2.5 md:flex">
        <span className="text-xs text-default-400">—</span>
      </div>

      <div
        role="group"
        aria-label={`${file.name} 操作控制`}
        className="flex items-center justify-center"
        onClick={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
        onContextMenu={(e) => e.stopPropagation()}
      >
        {!isMultiSelection && (
          <Dropdown placement="bottom-end">
            <DropdownTrigger>
              <button
                type="button"
                aria-label={`${file.name} 操作菜单`}
                className="flex h-9 w-9 items-center justify-center rounded-lg opacity-100 transition-colors hover:bg-content2 sm:opacity-0 sm:group-hover:opacity-100"
              >
                <MoreVertical size={16} className="text-default-500" />
              </button>
            </DropdownTrigger>
            <DropdownMenu 
              aria-label="文件操作"
              classNames={{ base: "bg-content1 border border-divider shadow-lg" }}
            >
              <DropdownSection title="操作" showDivider>
                {file.isDir ? (
                  <>
                    <DropdownItem
                      key="open"
                      startContent={<FolderOpen size={16} />}
                      onPress={onOpen}
                    >
                      打开文件夹
                    </DropdownItem>
                    <DropdownItem
                      key="download-archive"
                      startContent={<Download size={16} />}
                      onPress={handleDownload}
                    >
                      下载为 ZIP
                    </DropdownItem>
                  </>
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
              {canShareFile ? (
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
              {canDelete ? (
                <DropdownSection>
                  <DropdownItem 
                    key="delete" 
                    startContent={<Trash2 size={16} />}
                    className="text-rose data-[hover=true]:text-rose data-[hover=true]:bg-rose/10"
                    onPress={onDelete}
                    isDisabled={deletePreparing}
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
    <aside aria-label="文件详情" className="relative hidden w-[300px] shrink-0 flex-col gap-5 overflow-hidden border-l border-divider bg-content2/70 p-5 xl:flex">
      <div className="text-center relative z-10">
        <div className="mx-auto mb-3 flex h-20 w-20 items-center justify-center rounded-lg border border-divider bg-content1">
          <FileIcon name={file.name} isDir={file.isDir} size={76} variant="tile" />
        </div>
        <h3 className="font-semibold text-base text-foreground mb-1 truncate px-2">{file.name}</h3>
        <p className="text-[13px] text-default-600">{getFileTypeLabel(file)}</p>
      </div>

      <div className="relative z-10 rounded-lg border border-divider bg-content1 p-4">
        <div className="mb-3 text-[10px] font-semibold uppercase text-default-500">详情</div>
        <div className="space-y-3 text-sm">
          <div className="flex items-center justify-between gap-3">
            <span className="text-default-500">大小</span>
            <span className="font-medium text-foreground">{file.isDir ? '-' : formatBytes(file.size)}</span>
          </div>
          <div className="flex items-center justify-between gap-3">
            <span className="text-default-500">类型</span>
            <span className="font-medium text-foreground">{getFileTypeLabel(file)}</span>
          </div>
        </div>
      </div>

      <div className="flex-1 relative z-10">
        <div className="mb-3.5 text-[10px] font-semibold uppercase text-default-500">时间线</div>
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
  isActive,
  isFavorited,
  favoriteActionsAvailable,
  favoriteUnavailableLabel,
  shareActionsAvailable,
  shareActionLabel,
  isMultiSelection,
  canWrite,
  canDelete,
  deletePreparing,
  canShareFile,
  onSelect,
  onOpen,
  onActivate,
  onRename,
  onDelete,
  onViewVersions,
  onShare,
  onToggleFavorite,
  onDownload,
  onContextMenu,
}: {
  file: FileItem
  isSelected: boolean
  isActive: boolean
  isFavorited: boolean
  favoriteActionsAvailable: boolean
  favoriteUnavailableLabel: string
  shareActionsAvailable: boolean
  shareActionLabel: string
  isMultiSelection: boolean
  canWrite: boolean
  canDelete: boolean
  deletePreparing: boolean
  canShareFile: boolean
  onSelect: (e: React.MouseEvent) => void
  onOpen: () => void
  onActivate: (e: React.MouseEvent) => void
  onRename: () => void
  onDelete: () => void
  onViewVersions: () => void
  onShare: () => void
  onToggleFavorite: () => void
  onDownload: () => void
  onContextMenu: (e: React.MouseEvent) => void
}) {
  const detailLabel = isSelected && isMultiSelection
    ? '多选中'
    : file.isDir
      ? '文件夹'
      : formatBytes(file.size)
  const handleDownload = useCallback(() => {
    onDownload()
  }, [onDownload])

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
      role="group"
      aria-label={`${file.name} 文件项`}
      className={cn(
        "group relative min-h-[168px] bg-content1 border border-divider rounded-lg p-4 cursor-pointer transition-all duration-200",
        "shadow-[var(--shadow-soft)] hover:border-accent-primary/40 hover:shadow-[var(--shadow-medium)]",
        isActive && !isSelected && "border-default-300 bg-content2/50",
        isSelected && "border-accent-primary bg-accent-primary/5"
      )}
      onClick={(e) => {
        e.stopPropagation()
        onActivate(e)
      }}
      onDoubleClick={onOpen}
      onContextMenu={onContextMenu}
    >
      <div
        role="group"
        aria-label={`${file.name} 选择控制`}
        className="absolute top-3 left-3 z-10"
        onClick={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
        onContextMenu={(e) => e.stopPropagation()}
      >
        <SelectionCheckboxButton
          isSelected={isSelected}
          label={`选择 ${file.name}`}
          className={!isSelected ? "opacity-100 sm:opacity-0 sm:group-hover:opacity-100" : undefined}
          visualClassName={isSelected ? "backdrop-blur-sm" : "bg-content1/80 backdrop-blur-sm"}
          onClick={(e) => {
            e.stopPropagation()
            onSelect(e)
          }}
        />
      </div>

      <div
        role="group"
        aria-label={`${file.name} 操作控制`}
        className="absolute top-3 right-3 z-10"
        onClick={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
        onContextMenu={(e) => e.stopPropagation()}
      >
        {!isMultiSelection && (
          <Dropdown placement="bottom-end">
            <DropdownTrigger>
              <button
                type="button"
                aria-label={`${file.name} 操作菜单`}
                className="flex h-9 w-9 items-center justify-center rounded-lg bg-content1/80 opacity-100 backdrop-blur-sm transition-colors hover:bg-content2 sm:opacity-0 sm:group-hover:opacity-100"
              >
                <MoreVertical size={14} className="text-default-500" />
              </button>
            </DropdownTrigger>
            <DropdownMenu 
              aria-label="文件操作"
              classNames={{ base: "bg-content1 border border-divider shadow-lg" }}
            >
              <DropdownSection title="操作" showDivider>
                {file.isDir ? (
                  <>
                    <DropdownItem
                      key="open"
                      startContent={<FolderOpen size={16} />}
                      onPress={onOpen}
                    >
                      打开文件夹
                    </DropdownItem>
                    <DropdownItem
                      key="download-archive"
                      startContent={<Download size={16} />}
                      onPress={handleDownload}
                    >
                      下载为 ZIP
                    </DropdownItem>
                  </>
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
              {canShareFile ? (
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
              {canDelete ? (
                <DropdownSection>
                  <DropdownItem 
                    key="delete" 
                    startContent={<Trash2 size={16} />}
                    className="text-rose data-[hover=true]:text-rose data-[hover=true]:bg-rose/10"
                    onPress={onDelete}
                    isDisabled={deletePreparing}
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
        <div className="w-16 h-16 rounded-lg flex items-center justify-center">
          <FileIcon name={file.name} isDir={file.isDir} size={64} variant="tile" />
        </div>
      </div>

      <div className="text-center">
        <div className="font-medium text-foreground truncate text-sm mb-1">{file.name}</div>
        <div className="text-xs text-default-500">
          {detailLabel}
        </div>
      </div>
    </div>
  )
}

export function FilesPage() {
  'use no memo'

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
  const configuredShareEnabled = useShareEnabled()
  const user = useUser()
  const authScopeKey = user?.id ?? 'anonymous'
  const fileScopeKey = getFileQueryScopeKey(user)
  const { scopedHomeDir, hasInvalidHomeDir } = resolveUserHomeScope(user)
  const favoritesScopeKey = `${authScopeKey}:${hasInvalidHomeDir ? '__invalid__' : (scopedHomeDir ?? '/')}`
  const favoritesListQueryKey = useMemo(() => ['favorites', favoritesScopeKey] as const, [favoritesScopeKey])
  const favoritesCheckQueryKey = useMemo(() => ['favorites-check', favoritesScopeKey] as const, [favoritesScopeKey])
  
  // Track focused file index for keyboard navigation
  const [focusedIndex, setFocusedIndex] = useState<number>(-1)
  
  // Modal states
  const { isOpen: isNewFolderOpen, onOpen: onNewFolderOpen, onClose: onNewFolderClose } = useDisclosure()
  const { isOpen: isRenameOpen, onOpen: onRenameOpen, onClose: onRenameClose } = useDisclosure()
  const { isOpen: isDeleteOpen, onOpen: onDeleteOpen, onClose: onDeleteClose } = useDisclosure()
  const { isOpen: isBatchDeleteOpen, onOpen: onBatchDeleteOpen, onClose: onBatchDeleteClose } = useDisclosure()
  const {
    initialFocusRef: deleteCancelButtonRef,
    captureReturnFocus: captureDeleteReturnFocus,
    setFallbackReturnFocus: setDeleteFallbackReturnFocus,
  } = useDestructiveDialogFocus(isDeleteOpen)
  const {
    initialFocusRef: batchDeleteCancelButtonRef,
    captureReturnFocus: captureBatchDeleteReturnFocus,
    setFallbackReturnFocus: setBatchDeleteFallbackReturnFocus,
  } = useDestructiveDialogFocus(isBatchDeleteOpen)
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
  const newFolderNameValidationError = getPathSegmentNameValidationError(newFolderName, '请输入文件夹名称')
  const displayedNewFolderNameValidationError = newFolderName.trim() ? newFolderNameValidationError : null
  const renameNameValidationError = getPathSegmentNameValidationError(renameValue, '请输入新名称')
  const displayedRenameNameValidationError = renameValue.trim() ? renameNameValidationError : null
  const [actionFile, setActionFile] = useState<FileItem | null>(null)
  const [deleteIntent, setDeleteIntent] = useState<SingleDeleteIntent | null>(null)
  const [batchDeleteIntent, setBatchDeleteIntent] = useState<BatchDeleteIntent | null>(null)
  const [isDeleteIntentPreparing, setIsDeleteIntentPreparing] = useState(false)
  const [isBatchDeleteIntentPreparing, setIsBatchDeleteIntentPreparing] = useState(false)
  const [deletePolicyRefreshRequired, setDeletePolicyRefreshRequired] = useState(false)
  const newFolderSessionRef = useRef(0)
  const currentNewFolderNameRef = useRef('')
  const createFolderAbortControllerRef = useRef<AbortController | null>(null)
  const renameSessionRef = useRef(0)
  const currentRenameValueRef = useRef('')
  const currentRenameFileRef = useRef<FileItem | null>(null)
  const renameAbortControllerRef = useRef<AbortController | null>(null)
  const deleteIntentAbortControllerRef = useRef<AbortController | null>(null)
  const batchDeleteIntentAbortControllerRef = useRef<AbortController | null>(null)
  const deleteAbortControllerRef = useRef<AbortController | null>(null)
  const batchDeleteAbortControllerRef = useRef<AbortController | null>(null)
  const deleteFocusFallbackRef = useRef<HTMLDivElement>(null)
  const isMountedRef = useRef(true)
  const deleteDialogCloseRef = useRef(onDeleteClose)
  const batchDeleteDialogCloseRef = useRef(onBatchDeleteClose)
  const pasteAbortControllerRef = useRef<AbortController | null>(null)
  const favoriteAbortControllerRef = useRef<AbortController | null>(null)
  const lastSelectedIndexRef = useRef<number | null>(null)
  const appliedHighlightedPathRef = useRef<string | null>(null)
  const downloadAbortControllersRef = useRef(new Set<AbortController>())
  const [multiSelectHintVisible, setMultiSelectHintVisible] = useState(false)
  const hintTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  
  // Drag and drop state
  const [isDragging, setIsDragging] = useState(false)
  const dragCountRef = useRef(0)
  
  // Multi-file upload state
  const [uploadQueue, setUploadQueue] = useState<UploadQueueItem[]>([])
  const [isUploading, setIsUploading] = useState(false)
  const [showUploadPanel, setShowUploadPanel] = useState(false)
  const uploadClearTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const uploadSessionRef = useRef(0)
  const uploadAbortControllerRef = useRef<AbortController | null>(null)
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
    setSortBy,
    toggleSortOrder,
  } = useFilesStore()

  const currentPathRef = useRef(currentPath)
  const resetForPathRef = useRef<string | null>(null)
  const selectedFilesRef = useRef(selectedFiles)

  selectedFilesRef.current = selectedFiles

  useEffect(() => {
    deleteDialogCloseRef.current = onDeleteClose
    batchDeleteDialogCloseRef.current = onBatchDeleteClose
  }, [onBatchDeleteClose, onDeleteClose])

  useEffect(() => {
    currentPathRef.current = currentPath
  }, [currentPath])

  useEffect(() => {
    const controllers = downloadAbortControllersRef.current
    return () => {
      controllers.forEach((controller) => controller.abort())
      controllers.clear()
    }
  }, [])

  useEffect(() => {
    return () => {
      isMountedRef.current = false
      createFolderAbortControllerRef.current?.abort()
      createFolderAbortControllerRef.current = null
      renameAbortControllerRef.current?.abort()
      renameAbortControllerRef.current = null
      deleteIntentAbortControllerRef.current?.abort()
      deleteIntentAbortControllerRef.current = null
      batchDeleteIntentAbortControllerRef.current?.abort()
      batchDeleteIntentAbortControllerRef.current = null
      deleteAbortControllerRef.current?.abort()
      deleteAbortControllerRef.current = null
      batchDeleteAbortControllerRef.current?.abort()
      batchDeleteAbortControllerRef.current = null
      pasteAbortControllerRef.current?.abort()
      pasteAbortControllerRef.current = null
      favoriteAbortControllerRef.current?.abort()
      favoriteAbortControllerRef.current = null
    }
  }, [])

  const setFilePathState = useCallback((filePath: string): string => {
    const normalizedPath = normalizePath(filePath)
    if (currentPathRef.current !== normalizedPath) {
      currentPathRef.current = normalizedPath
      setCurrentPath(normalizedPath)
    }
    return normalizedPath
  }, [setCurrentPath])

  const navigateToFilePath = useCallback((filePath: string, options?: { replace?: boolean }) => {
    const normalizedPath = setFilePathState(filePath)
    const routePath = getFilesRoutePath(normalizedPath)
    if (location.pathname !== routePath) {
      navigate(routePath, { replace: options?.replace ?? false })
    }
  }, [location.pathname, navigate, setFilePathState])

  useEffect(() => {
    if (hasInvalidHomeDir) return
    if (!location.pathname.startsWith('/files')) return
    const routePath = location.pathname.replace(/^\/files/, '')
    let finalPath = '/'
    if (routePath) {
      try {
        finalPath = normalizePath(decodePathFromUrl(routePath))
      } catch {
        const fallbackPath = currentPathRef.current || '/'
        const fallbackRoute = getFilesRoutePath(fallbackPath)
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
    setFilePathState(finalPath)
  }, [hasInvalidHomeDir, location.pathname, navigate, setFilePathState])

  useEffect(() => {
    if (!hasInvalidHomeDir) return
    if (currentPath !== '/') {
      setFilePathState('/')
    }
    if (location.pathname !== '/files') {
      navigate('/files', { replace: true })
    }
  }, [hasInvalidHomeDir, currentPath, location.pathname, navigate, setFilePathState])

  const currentPathAllowed = !hasInvalidHomeDir
  const filesQueryKey = getFilesQueryKey(fileScopeKey, currentPath)
  const uploadCounts = useMemo(() => getUploadQueueCounts(uploadQueue), [uploadQueue])

  useEffect(() => {
    if (hasInvalidHomeDir) return
    if (location.pathname.startsWith('/files')) {
      try {
        const routePath = location.pathname.replace(/^\/files/, '')
        const routeFilePath = routePath ? normalizePath(decodePathFromUrl(routePath)) : '/'
        if (routeFilePath !== currentPath) {
          return
        }
      } catch {
        return
      }
    }

    const targetPath = getFilesRoutePath(currentPath)
    if (location.pathname !== targetPath) {
      navigate(targetPath, { replace: true })
    }
  }, [hasInvalidHomeDir, currentPath, location.pathname, navigate])

  useEffect(() => {
    if (resetForPathRef.current === currentPath) {
      return
    }
    resetForPathRef.current = currentPath
    lastSelectedIndexRef.current = null
    deleteIntentAbortControllerRef.current?.abort()
    deleteIntentAbortControllerRef.current = null
    batchDeleteIntentAbortControllerRef.current?.abort()
    batchDeleteIntentAbortControllerRef.current = null
    deleteAbortControllerRef.current?.abort()
    deleteAbortControllerRef.current = null
    batchDeleteAbortControllerRef.current?.abort()
    batchDeleteAbortControllerRef.current = null
    setIsBatchDeleting(false)
    setIsDeleteIntentPreparing(false)
    setIsBatchDeleteIntentPreparing(false)
    setDeleteIntent(null)
    setBatchDeleteIntent(null)
    deleteDialogCloseRef.current()
    batchDeleteDialogCloseRef.current()
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

  const { data, isLoading, isFetching, error, refetch } = useQuery({
    queryKey: filesQueryKey,
    queryFn: ({ signal }) => listFiles(currentPath, { signal }),
    enabled: currentPathAllowed,
  })

  const deletePolicy = useMemo<KnownDeletePolicy | null>(() => {
    if (!data || data.deleteMode === 'unknown') {
      return null
    }

    return {
      mode: data.deleteMode,
      token: data.deletePolicyToken,
      trashRetentionDays: data.trashRetentionDays,
      trashAutoCleanupEnabled: data.trashAutoCleanupEnabled,
    }
  }, [data])
  const deletePolicyAvailable = deletePolicy !== null && !deletePolicyRefreshRequired

  const refreshDeletePolicyAfterDrift = useCallback((summary?: BatchDeleteDriftSummary) => {
    void refetch().then((result) => {
      if (result.error || !result.data || result.data.deleteMode === 'unknown') {
        addToast({
          title: summary ? '删除策略已更改，批量删除已停止' : '删除策略已更改，文件未删除',
          description: summary
            ? `${getBatchDeleteDriftDescription(summary)}；删除策略刷新失败，删除操作保持停用。`
            : '暂时无法确认当前删除策略，删除操作保持停用。请重新加载后再确认。',
          color: 'warning',
        })
        return
      }

      setDeletePolicyRefreshRequired(false)
      addToast({
        title: summary ? '删除策略已更改，批量删除已停止' : '删除策略已更改，文件未删除',
        description: summary
          ? `${getBatchDeleteDriftDescription(summary)}；列表已刷新，请按当前删除策略重新确认。`
          : '列表已刷新，请按当前删除策略重新确认。',
        color: 'warning',
      })
    })
  }, [refetch])

  const refreshFilesAfterTargetDrift = useCallback((summary?: BatchDeleteDriftSummary) => {
    void refetch().then((result) => {
      const title = summary ? '删除目标已更改，批量删除已停止' : '删除目标已更改，文件未删除'
      if (result.error) {
        addToast({
          title,
          description: summary
            ? `${getBatchDeleteDriftDescription(summary)}；列表刷新失败，请重新加载后再次确认。`
            : '列表刷新失败，请重新加载并再次确认删除目标。',
          color: 'warning',
        })
        return
      }

      addToast({
        title,
        description: summary
          ? `${getBatchDeleteDriftDescription(summary)}；列表已刷新，请重新确认剩余删除目标。`
          : '列表已刷新，请重新确认删除目标。',
        color: 'warning',
      })
    })
  }, [refetch])

  const discardReplacedDeleteIntent = useCallback(() => {
    void refetch()
    addToast({
      title: '目标已变化，请重新选择/确认',
      color: 'warning',
    })
  }, [refetch])

  const handleRefreshDeletePolicy = useCallback(() => {
    void refetch().then((result) => {
      if (result.error || !result.data || result.data.deleteMode === 'unknown') {
        addToast({
          title: '删除策略仍不可确认',
          description: result.error
            ? getUserFacingErrorDescription(result.error, GENERIC_LOAD_ERROR_DESCRIPTION)
            : '服务端未返回完整的删除策略。',
          color: 'warning',
        })
        return
      }

      setDeletePolicyRefreshRequired(false)
      addToast({ title: '删除策略已重新加载', color: 'success' })
    })
  }, [refetch])

  const removeFilesFromCache = useCallback((paths: string[]) => {
    if (paths.length === 0) {
      return
    }

    const removedPaths = new Set(paths)
    queryClient.setQueryData<FileListResponse>(filesQueryKey, (current) => {
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
  }, [filesQueryKey, queryClient])

  const deleteFileWithMissingSync = useCallback(async (path: string, options: DeleteFileOptions) => {
    return withMissingFileActionResult(() => deleteFile(path, options))
  }, [])

  const moveFileWithMissingSync = useCallback(async (fromPath: string, toPath: string, options?: { signal?: AbortSignal }) => {
    try {
      return options ? await moveFile(fromPath, toPath, options) : await moveFile(fromPath, toPath)
    } catch (error) {
      if (getErrorStatus(error) === 404) {
        return getMissingFileActionResult()
      }

      throw error
    }
  }, [])

  const copyFileWithMissingSync = useCallback(async (fromPath: string, toPath: string, options?: { signal?: AbortSignal }) => {
    try {
      return options ? await copyFile(fromPath, toPath, options) : await copyFile(fromPath, toPath)
    } catch (error) {
      if (getErrorStatus(error) === 404) {
        return getMissingFileActionResult()
      }

      throw error
    }
  }, [])

  // Mutations (omitted for brevity, same as before)
  const deleteMutation = useMutation({
    mutationFn: ({ path, expectedDeleteMode, expectedDeletePolicyToken, expectedDeleteTargetToken, signal }: DeleteMutationVariables) => deleteFileWithMissingSync(path, {
      expectedDeleteMode,
      expectedDeletePolicyToken,
      expectedDeleteTargetToken,
      signal,
    }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }

      const focusFallback = deleteFocusFallbackRef.current
      if (focusFallback) {
        focusFallback.tabIndex = -1
      }
      setDeleteFallbackReturnFocus(focusFallback)
      removeFilesFromCache([variables.path])
      queryClient.invalidateQueries({ queryKey: filesQueryKey })
      if (variables.expectedDeleteMode === 'trash' && !isMissingFileActionResult(result)) {
        queryClient.invalidateQueries({ queryKey: ['trash'] })
      }
      onDeleteClose()
      setDeleteIntent(null)
      addToast(getFilesActionSuccessToast(result, {
        success: variables.expectedDeleteMode === 'trash' ? '已移入回收站' : '已永久删除',
        warning: variables.expectedDeleteMode === 'trash' ? '已移入回收站，但存在警告' : '已永久删除，但存在警告',
      }))
    },
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) {
        return
      }

      if (isDeletePolicyChangedError(error)) {
        setDeletePolicyRefreshRequired(true)
        onDeleteClose()
        setDeleteIntent(null)
        refreshDeletePolicyAfterDrift()
        return
      }

      if (isDeleteTargetChangedError(error)) {
        onDeleteClose()
        setDeleteIntent(null)
        refreshFilesAfterTargetDrift()
        return
      }

      addToast(getFilesActionErrorToast(error, {
        unavailable: '删除暂不可用',
        failure: '删除失败',
      }))
    },
    onSettled: (_result, _error, variables) => {
      if (deleteAbortControllerRef.current?.signal === variables?.signal) {
        deleteAbortControllerRef.current = null
      }
    },
  })
  
  const createFolderMutation = useMutation({
    mutationFn: ({ path, signal }: CreateFolderMutationVariables) => createDirectory(path, { signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }

      queryClient.invalidateQueries({ queryKey: getFilesQueryKey(fileScopeKey, variables.directoryPath) })
      if (
        newFolderSessionRef.current === variables.sessionId
        && currentNewFolderNameRef.current.trim() === variables.folderName
      ) {
        onNewFolderClose()
        setNewFolderName('')
      }
      addToast(getCreateFolderSuccessToast(result))
    },
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) {
        return
      }

      addToast(getCreateFolderErrorToast(error))
    },
    onSettled: (_result, _error, variables) => {
      if (createFolderAbortControllerRef.current?.signal === variables?.signal) {
        createFolderAbortControllerRef.current = null
      }
    },
  })
  
  const renameMutation = useMutation({
    mutationFn: ({ from, to, signal }: RenameMutationVariables) => moveFileWithMissingSync(from, to, { signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }

      if (isMissingFileActionResult(result)) {
        removeFilesFromCache([variables.from])
      }
      queryClient.invalidateQueries({ queryKey: getFilesQueryKey(fileScopeKey, variables.directoryPath) })
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
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) {
        return
      }

      addToast(getFilesActionErrorToast(error, {
        unavailable: '重命名暂不可用',
        failure: '重命名失败',
      }))
    },
    onSettled: (_result, _error, variables) => {
      if (renameAbortControllerRef.current?.signal === variables?.signal) {
        renameAbortControllerRef.current = null
      }
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

  const currentPathCanWrite = hasWriteCapability(canWrite, data?.capabilities)
  const canWriteFile = useCallback((file: FileItem): boolean => {
    return hasWriteCapability(canWrite, file.capabilities)
  }, [canWrite])
  const canDeleteFile = useCallback((file: FileItem): boolean => {
    return deletePolicyAvailable && file.deleteIdentityToken !== null && canWriteFile(file)
  }, [canWriteFile, deletePolicyAvailable])
  const canUseFileSource = useCallback((file: FileItem): boolean => {
    return hasConcreteReadCapability(canWrite, file.capabilities)
  }, [canWrite])

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
    queryKey: [...favoritesCheckQueryKey, filePaths],
    queryFn: ({ signal }) => checkFavorites(filePaths, { signal }),
    enabled: !hasInvalidHomeDir && filePaths.length > 0,
    staleTime: 30000, // Cache for 30 seconds
  })
  const { error: shareAvailabilityError } = useQuery({
    queryKey: ['shares-availability'],
    queryFn: ({ signal }) => listShares(false, { signal }),
    enabled: canWrite && configuredShareEnabled !== false,
    retry: false,
    staleTime: 30000,
  })
  const favoriteActionsAvailable = !favoritesError
  const favoritesBanner = favoritesError ? getFavoritesBannerContent(favoritesError) : null
  const favoriteUnavailableLabel = favoritesBanner?.title ?? '收藏状态不可用'
  const syncFavoriteStatus = useCallback((path: string, isFavorited: boolean) => {
    queryClient.setQueriesData<Record<string, boolean>>({ queryKey: favoritesCheckQueryKey }, (current) => {
      if (!current) {
        return current
      }

      return { ...current, [path]: isFavorited }
    })
  }, [favoritesCheckQueryKey, queryClient])
  const shareFeatureState = shareFeatureDisabled || configuredShareEnabled === false ? 'disabled' : getShareFeatureState(shareAvailabilityError)
  const shareActionsAvailable = shareFeatureState === null
  const shareActionLabel = getShareActionLabel(shareFeatureState)
  const shareBanner = shareFeatureState === 'disabled'
    ? getShareBannerContent()
    : shareFeatureState === 'unavailable'
      ? getShareUnavailableBannerContent()
      : null

  const favoriteMutation = useMutation({
    mutationFn: ({ path, isFavorited, signal }: FavoriteMutationVariables) =>
      toggleFavorite(path, isFavorited, { signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }

      queryClient.invalidateQueries({ queryKey: favoritesCheckQueryKey })
      queryClient.invalidateQueries({ queryKey: favoritesListQueryKey })
      addToast({
        title: result.isFavorited
          ? result.warning ? '已添加收藏，但存在警告' : '已添加收藏'
          : result.warning ? '已取消收藏，但存在警告' : '已取消收藏',
        color: result.warning ? 'warning' : 'success',
      })
    },
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) {
        return
      }

      if (getErrorCode(error) === 'FAVORITE_ALREADY_EXISTS' || getErrorStatus(error) === 409) {
        syncFavoriteStatus(variables.path, true)
        queryClient.invalidateQueries({ queryKey: favoritesCheckQueryKey })
        queryClient.invalidateQueries({ queryKey: favoritesListQueryKey })
        addToast({
          title: '已在收藏夹中',
          description: '该文件已被其他操作加入收藏，状态已同步。',
          color: 'warning',
        })
        return
      }

      if (getErrorCode(error) === 'FAVORITE_NOT_FOUND' || getErrorStatus(error) === 404) {
        syncFavoriteStatus(variables.path, false)
        queryClient.invalidateQueries({ queryKey: favoritesCheckQueryKey })
        queryClient.invalidateQueries({ queryKey: favoritesListQueryKey })
        addToast({
          title: '收藏已移除',
          description: '该文件已不在收藏夹中，状态已同步。',
          color: 'warning',
        })
        return
      }

      addToast(getFavoriteActionErrorToast(error))
    },
    onSettled: (_result, _error, variables) => {
      if (favoriteAbortControllerRef.current?.signal === variables?.signal) {
        favoriteAbortControllerRef.current = null
      }
    },
  })

  const handleToggleFavorite = (path: string, isFavorited: boolean) => {
    favoriteAbortControllerRef.current?.abort()
    const controller = new AbortController()
    favoriteAbortControllerRef.current = controller
    favoriteMutation.mutate({
      path,
      isFavorited,
      signal: controller.signal,
    })
  }

  const virtualizer = useVirtualizer({
    count: sortedFiles.length,
    getScrollElement: () => parentRef.current,
    getItemKey: (index) => sortedFiles[index]?.path ?? index,
    estimateSize: () => 72, // Increased row height
    overscan: 10,
  })

  // Active file for preview panel (not selection)
  const [activeFilePath, setActiveFilePath] = useState<string | null>(null)

  const getFocusedOrActiveFile = useCallback((): { file: FileItem; index: number } | null => {
    if (focusedIndex >= 0 && focusedIndex < sortedFiles.length) {
      return { file: sortedFiles[focusedIndex], index: focusedIndex }
    }

    if (activeFilePath) {
      const activeIndex = sortedFiles.findIndex((file) => file.path === activeFilePath)
      if (activeIndex >= 0) {
        return { file: sortedFiles[activeIndex], index: activeIndex }
      }
    }

    if (selectedFiles.size === 1) {
      const selectedPath = Array.from(selectedFiles)[0]
      const selectedIndex = sortedFiles.findIndex((file) => file.path === selectedPath)
      if (selectedIndex >= 0) {
        return { file: sortedFiles[selectedIndex], index: selectedIndex }
      }
    }

    return null
  }, [activeFilePath, focusedIndex, selectedFiles, sortedFiles])

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

  const handleFileActivate = useCallback((file: FileItem, index: number, event: React.MouseEvent) => {
    if (event.shiftKey || event.metaKey || event.ctrlKey) {
      handleFileSelection(file, index, event, event.metaKey || event.ctrlKey ? 'toggle' : 'primary')
      return
    }

    if (selectedFiles.size > 0) {
      clearSelection()
    }

    setActiveFilePath(file.path)
    setFocusedIndex(index)
    lastSelectedIndexRef.current = index
  }, [clearSelection, handleFileSelection, selectedFiles.size, setFocusedIndex])

  // Handle double click - open folder or preview file
  const handleFileOpen = useCallback((file: FileItem) => {
    if (file.isDir) {
      navigateToFilePath(file.path)
      setActiveFilePath(null)
      return
    }
    setPreviewFile({ path: file.path, name: file.name })
    onPreviewOpen()
  }, [navigateToFilePath, onPreviewOpen])

  const handleSelectAll = useCallback(() => {
    if (selectedFiles.size === sortedFiles.length) {
      clearSelection()
    } else {
      selectAll(sortedFiles.map(f => f.path))
    }
  }, [sortedFiles, selectedFiles.size, selectAll, clearSelection])

  const handleCreateFolder = useCallback(() => {
    if (!currentPathCanWrite || createFolderMutation.isPending) return
    const validationError = getPathSegmentNameValidationError(newFolderName, '请输入文件夹名称')
    if (validationError) {
      addToast({
        title: '文件夹名称无效',
        description: validationError,
        color: 'warning',
      })
      return
    }
    const trimmedFolderName = newFolderName.trim()
    const path = joinPathSegment(currentPath, trimmedFolderName)
    const controller = new AbortController()
    createFolderAbortControllerRef.current?.abort()
    createFolderAbortControllerRef.current = controller
    createFolderMutation.mutate({
      path,
      directoryPath: currentPath,
      folderName: trimmedFolderName,
      sessionId: newFolderSessionRef.current,
      signal: controller.signal,
    })
  }, [currentPathCanWrite, createFolderMutation, newFolderName, currentPath])

  const handleRename = useCallback(() => {
    if (renameMutation.isPending) return
    const trimmedRenameValue = renameValue.trim()
    const validationError = getPathSegmentNameValidationError(renameValue, '请输入新名称')
    if (validationError) {
      addToast({
        title: '名称无效',
        description: validationError,
        color: 'warning',
      })
      return
    }
    if (!actionFile) return
    if (!canWriteFile(actionFile)) return
    if (trimmedRenameValue === actionFile.name) return
    const parentPath = actionFile.path.substring(0, actionFile.path.lastIndexOf('/')) || '/'
    const newPath = joinPathSegment(parentPath, trimmedRenameValue)

    const controller = new AbortController()
    renameAbortControllerRef.current?.abort()
    renameAbortControllerRef.current = controller
    renameMutation.mutate({
      from: actionFile.path,
      to: newPath,
      directoryPath: currentPath,
      targetPath: actionFile.path,
      submittedName: trimmedRenameValue,
      sessionId: renameSessionRef.current,
      signal: controller.signal,
    })
  }, [renameMutation, renameValue, actionFile, canWriteFile, currentPath])

  const handleDelete = useCallback(() => {
    if (deleteMutation.isPending) return
    if (!deleteIntent) return
    if (currentPathRef.current !== deleteIntent.directoryPath) {
      onDeleteClose()
      setDeleteIntent(null)
      return
    }
    const controller = new AbortController()
    deleteAbortControllerRef.current?.abort()
    deleteAbortControllerRef.current = controller
    deleteMutation.mutate({
      path: deleteIntent.target.path,
      expectedDeleteMode: deleteIntent.policy.mode,
      expectedDeletePolicyToken: deleteIntent.policy.token,
      expectedDeleteTargetToken: deleteIntent.target.deleteTargetToken,
      signal: controller.signal,
    })
  }, [deleteIntent, deleteMutation, onDeleteClose])

  const handleOpenNewFolderModal = useCallback(() => {
    if (!currentPathCanWrite) return
    newFolderSessionRef.current += 1
    onNewFolderOpen()
  }, [currentPathCanWrite, onNewFolderOpen])

  const handleCloseNewFolderModal = useCallback(() => {
    if (createFolderMutation.isPending) return
    onNewFolderClose()
  }, [createFolderMutation.isPending, onNewFolderClose])

  // Action handlers for context menu
  const handleOpenRenameModal = useCallback((file: FileItem) => {
    if (!canWriteFile(file)) return
    renameSessionRef.current += 1
    setActionFile(file)
    setRenameValue(file.name)
    onRenameOpen()
  }, [canWriteFile, onRenameOpen])

  const handleOpenDeleteModal = useCallback(async (file: FileItem) => {
    const observedIdentityToken = file.deleteIdentityToken
    if (!canDeleteFile(file) || observedIdentityToken === null) return
    if (deleteIntentAbortControllerRef.current) return

    deleteFocusFallbackRef.current?.removeAttribute('tabindex')
    const menuTrigger = Array.from(document.querySelectorAll<HTMLElement>('[aria-label]'))
      .find((element) => element.getAttribute('aria-label') === `${file.name} 操作菜单`)
    captureDeleteReturnFocus(menuTrigger)
    const directoryPath = currentPathRef.current
    const targetSnapshot = snapshotDeleteTarget(file)
    const controller = new AbortController()
    deleteIntentAbortControllerRef.current = controller
    setIsDeleteIntentPreparing(true)
    try {
      const intent = await createFileDeleteIntent([{
        path: targetSnapshot.path,
        observedIdentityToken,
      }], { signal: controller.signal })
      if (controller.signal.aborted || currentPathRef.current !== directoryPath) return
      if (!deleteTargetMatchesSnapshot(intent.targets[0]!, targetSnapshot)) {
        discardReplacedDeleteIntent()
        return
      }
      setDeleteIntent({
        target: intent.targets[0]!,
        policy: getKnownDeletePolicy(intent),
        directoryPath,
      })
      captureDeleteReturnFocus(menuTrigger)
      onDeleteOpen()
    } catch (error) {
      if (controller.signal.aborted || isAbortError(error)) return
      if (getErrorStatus(error) === 404) {
        queryClient.invalidateQueries({ queryKey: filesQueryKey })
        addToast({ title: '文件或文件夹已不存在，列表已刷新', color: 'warning' })
        return
      }
      if (isDeleteTargetChangedError(error)) {
        refreshFilesAfterTargetDrift()
        return
      }
      addToast(getFilesActionErrorToast(error, {
        unavailable: '删除确认暂不可用',
        failure: '无法确认删除目标',
      }))
    } finally {
      if (deleteIntentAbortControllerRef.current === controller) {
        deleteIntentAbortControllerRef.current = null
        setIsDeleteIntentPreparing(false)
      }
    }
  }, [canDeleteFile, captureDeleteReturnFocus, discardReplacedDeleteIntent, filesQueryKey, onDeleteOpen, queryClient, refreshFilesAfterTargetDrift])

  const handleOpenBatchDeleteModal = useCallback(async (returnFocusTarget?: Element) => {
    if (!deletePolicyAvailable) {
      addToast({ title: '删除策略不可确认，删除操作已停用', color: 'warning' })
      return
    }

    const targets = sortedFiles.filter((file) => selectedFiles.has(file.path) && canDeleteFile(file))
    if (targets.length === 0 || targets.length !== selectedFiles.size) {
      addToast({ title: '所选项目不可删除', color: 'warning' })
      return
    }
    if (targets.length > MAX_DELETE_INTENT_TARGETS) {
      addToast({
        title: '所选项目过多',
        description: `单次最多确认 ${MAX_DELETE_INTENT_TARGETS} 个删除目标，请减少选择后重试。`,
        color: 'warning',
      })
      return
    }
    if (batchDeleteIntentAbortControllerRef.current) return

    deleteFocusFallbackRef.current?.removeAttribute('tabindex')
    const returnFocusElement = returnFocusTarget instanceof HTMLElement ? returnFocusTarget : undefined
    captureBatchDeleteReturnFocus(returnFocusElement)
    const directoryPath = currentPathRef.current
    const controller = new AbortController()
    batchDeleteIntentAbortControllerRef.current = controller
    setIsBatchDeleteIntentPreparing(true)
    try {
      const targetSnapshots = targets.map(snapshotDeleteTarget)
      const requestedPaths = targets.map((file) => file.path)
      const requestedTargets = targetSnapshots.map((target) => ({
        path: target.path,
        observedIdentityToken: target.deleteIdentityToken!,
      }))
      const intent = await createFileDeleteIntent(requestedTargets, { signal: controller.signal })
      if (controller.signal.aborted || currentPathRef.current !== directoryPath) return
      const currentSelection = selectedFilesRef.current
      if (
        currentSelection.size !== requestedPaths.length
        || requestedPaths.some((path) => !currentSelection.has(path))
      ) {
        addToast({
          title: '所选项目已更改',
          description: '请按当前选择重新确认批量删除。',
          color: 'warning',
        })
        return
      }
      if (!deleteTargetsMatchSnapshots(intent.targets, targetSnapshots)) {
        discardReplacedDeleteIntent()
        return
      }
      setBatchDeleteIntent({
        targets: intent.targets,
        policy: getKnownDeletePolicy(intent),
        directoryPath,
      })
      captureBatchDeleteReturnFocus(returnFocusElement)
      onBatchDeleteOpen()
    } catch (error) {
      if (controller.signal.aborted || isAbortError(error)) return
      if (getErrorStatus(error) === 404) {
        queryClient.invalidateQueries({ queryKey: filesQueryKey })
        addToast({ title: '部分所选项目已不存在，列表已刷新', color: 'warning' })
        return
      }
      if (isDeleteTargetChangedError(error)) {
        refreshFilesAfterTargetDrift({
          mode: deletePolicy!.mode,
          deletedCount: 0,
          synchronizedMissingCount: 0,
          remainingCount: targets.length,
        })
        return
      }
      addToast(getFilesActionErrorToast(error, {
        unavailable: '批量删除确认暂不可用',
        failure: '无法确认批量删除目标',
      }))
    } finally {
      if (batchDeleteIntentAbortControllerRef.current === controller) {
        batchDeleteIntentAbortControllerRef.current = null
        setIsBatchDeleteIntentPreparing(false)
      }
    }
  }, [canDeleteFile, captureBatchDeleteReturnFocus, deletePolicy, deletePolicyAvailable, discardReplacedDeleteIntent, filesQueryKey, onBatchDeleteOpen, queryClient, refreshFilesAfterTargetDrift, selectedFiles, sortedFiles])

  const handleCloseRenameModal = useCallback(() => {
    if (renameMutation.isPending) return
    onRenameClose()
  }, [renameMutation.isPending, onRenameClose])

  const handleCloseDeleteModal = useCallback(() => {
    if (deleteMutation.isPending) return
    onDeleteClose()
    setDeleteIntent(null)
  }, [deleteMutation.isPending, onDeleteClose])

  const handleCloseBatchDeleteModal = useCallback(() => {
    if (isBatchDeleting) return
    onBatchDeleteClose()
    setBatchDeleteIntent(null)
  }, [isBatchDeleting, onBatchDeleteClose])

  const handleViewVersions = useCallback((file: FileItem) => {
    if (file.isDir) return
    navigate(`/versions?path=${encodeURIComponent(file.path)}`)
  }, [navigate])

  const handleOpenShareModal = useCallback((file: FileItem) => {
    if (!canUseFileSource(file)) return
    if (!shareActionsAvailable) {
      addToast({ title: shareActionLabel, color: 'warning' })
      return
    }
    setShareFile(file)
    onShareOpen()
  }, [canUseFileSource, onShareOpen, shareActionLabel, shareActionsAvailable])

  // Move/Copy handlers
  const handleOpenMoveModal = useCallback((files: FileItem[]) => {
    if (files.length === 0 || !files.every(canWriteFile)) {
      addToast({ title: '所选项目不可移动', color: 'warning' })
      return
    }
    setMoveFiles(files)
    setMoveMode('move')
    onMoveOpen()
  }, [canWriteFile, onMoveOpen])

  const handleOpenCopyModal = useCallback((files: FileItem[]) => {
    if (files.length === 0 || !files.every(canUseFileSource)) {
      addToast({ title: '所选项目不可复制', color: 'warning' })
      return
    }
    setMoveFiles(files)
    setMoveMode('copy')
    onMoveOpen()
  }, [canUseFileSource, onMoveOpen])

  const handleFileDownload = useCallback((file: FileItem, options?: { onSettled?: () => void }) => {
    const controller = new AbortController()
    downloadAbortControllersRef.current.add(controller)

    void downloadFile(file.path, getDownloadOptionsWithSignal(file, controller.signal))
      .catch((error: unknown) => {
        if (controller.signal.aborted || isAbortError(error)) {
          return
        }

        addToast(getFileDownloadErrorToast(error))
      })
      .finally(() => {
        downloadAbortControllersRef.current.delete(controller)
        options?.onSettled?.()
      })
  }, [])

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
    if (!contextMenuFile) return
    handleFileDownload(contextMenuFile, { onSettled: contextMenu.hide })
  }, [contextMenuFile, contextMenu, handleFileDownload])

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
  const ensureDirectoryExists = useCallback(async (dirPath: string, warningMessages: string[], signal?: AbortSignal) => {
    if (dirPath === '/' || createdDirsRef.current.has(dirPath)) return
    if (signal?.aborted) {
      throw new DOMException('directory creation aborted', 'AbortError')
    }
    
    // Get parent path
    const parentPath = dirPath.substring(0, dirPath.lastIndexOf('/')) || '/'
    
    // Ensure parent exists first
    await ensureDirectoryExists(parentPath, warningMessages, signal)
    if (signal?.aborted) {
      throw new DOMException('directory creation aborted', 'AbortError')
    }
    
    // Create this directory if not already created
    if (!createdDirsRef.current.has(dirPath)) {
      try {
        const result = await createDirectory(dirPath, signal ? { signal } : {})
        if (result.warning) {
          warningMessages.push(getActionWarningSummary(result))
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

  const handleCancelUpload = useCallback(() => {
    const uploadController = uploadAbortControllerRef.current
    if (!uploadController || uploadController.signal.aborted) {
      return
    }

    uploadSessionRef.current += 1
    uploadController.abort()
    uploadAbortControllerRef.current = null
    setIsUploading(false)
    setShowUploadPanel(true)
    setUploadQueue(prev => prev.map(item =>
      item.status === 'pending' || item.status === 'uploading'
        ? { ...item, status: 'cancelled' as const, error: '上传已取消' }
        : item
    ))
    void queryClient.invalidateQueries({ queryKey: filesQueryKey })
    addToast({
      title: '上传已取消',
      color: 'warning',
    })
  }, [filesQueryKey, queryClient])

  // Enhanced upload handler with queue support and folder support
  const handleUpload = useCallback(async (files: FileList | null) => {
    if (!currentPathCanWrite) return
    if (!files || files.length === 0) return

    const previousUploadController = uploadAbortControllerRef.current
    if (previousUploadController && !previousUploadController.signal.aborted) {
      previousUploadController.abort()
      void queryClient.invalidateQueries({ queryKey: filesQueryKey })
    }
    const uploadSession = uploadSessionRef.current + 1
    uploadSessionRef.current = uploadSession
    const uploadController = new AbortController()
    uploadAbortControllerRef.current = uploadController
    const isCurrentUploadSession = () => uploadSessionRef.current === uploadSession
      && uploadAbortControllerRef.current === uploadController
      && !uploadController.signal.aborted

    if (uploadClearTimeoutRef.current) {
      clearTimeout(uploadClearTimeoutRef.current)
      uploadClearTimeoutRef.current = null
    }
    
    const fileArray = Array.from(files)
    
    // Reset created directories tracker
    createdDirsRef.current.clear()
    
    const queue = buildUploadQueue(fileArray, {
      maxUploadFileSizeBytes: MAX_UPLOAD_FILE_SIZE_BYTES,
      maxUploadFileSizeLabel: MAX_UPLOAD_FILE_SIZE_LABEL,
    })
    const isFolderUpload = queue.some(item => item.folderRelativePath)

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
          : `${rejectedEntries.length} 个文件未进入上传队列，请查看上传记录。`,
        color: rejectedEntries.length === queue.length ? 'danger' : 'warning',
      })
    }

    if (uploadableEntries.length === 0) {
      if (uploadAbortControllerRef.current === uploadController) {
        uploadAbortControllerRef.current = null
      }
      return
    }

    let successCount = 0
    let errorCount = rejectedEntries.length
    const uploadErrors: unknown[] = []
    const uploadWarningMessages: string[] = []

    try {
      for (const { item, index } of uploadableEntries) {
        const file = item.file
        const relativePath = item.folderRelativePath || ''

        if (!isCurrentUploadSession()) {
          return
        }
        setUploadQueue(prev => prev.map((item, j) =>
          j === index ? { ...item, status: 'uploading' as const } : item
        ))

        try {
          // For folder uploads, create parent directories first
          if (relativePath && relativePath.includes('/')) {
            const relativeDir = relativePath.substring(0, relativePath.lastIndexOf('/'))
            const targetDir = currentPath === '/' ? `/${relativeDir}` : `${currentPath}/${relativeDir}`
            await ensureDirectoryExists(targetDir, uploadWarningMessages, uploadController.signal)
          }
          if (!isCurrentUploadSession()) {
            return
          }

          // Calculate the target path for the file
          let targetPath = currentPath
          if (relativePath && relativePath.includes('/')) {
            const relativeDir = relativePath.substring(0, relativePath.lastIndexOf('/'))
            targetPath = currentPath === '/' ? `/${relativeDir}` : `${currentPath}/${relativeDir}`
          }

          const result = await uploadFile(targetPath, file, (progress) => {
            if (!isCurrentUploadSession()) {
              return
            }
            const normalizedProgress = normalizeUploadProgress(progress)
            setUploadQueue(prev => prev.map((item, j) =>
              j === index ? { ...item, progress: normalizedProgress } : item
            ))
          }, { signal: uploadController.signal })
          if (result.warning) {
            uploadWarningMessages.push(getActionWarningSummary(result))
          }
          successCount++
          if (isCurrentUploadSession()) {
            setUploadQueue(prev => prev.map((item, j) =>
              j === index ? { ...item, status: 'done' as const, progress: 100 } : item
            ))
          }
        } catch (error) {
          if (uploadController.signal.aborted || isAbortError(error)) {
            return
          }
          errorCount++
          uploadErrors.push(error)
          if (isCurrentUploadSession()) {
            setUploadQueue(prev => prev.map((item, j) =>
              j === index ? { ...item, status: 'error' as const, error: getUploadQueueErrorMessage(error) } : item
            ))
          }
        }
      }

      if (!isCurrentUploadSession()) {
        return
      }

      queryClient.invalidateQueries({ queryKey: filesQueryKey })

      if (!isCurrentUploadSession()) {
        return
      }

      setIsUploading(false)

      // Show summary toast for folder upload
      if (isFolderUpload) {
        const summaryToast = getFolderUploadSummaryToast(successCount, errorCount, uploadErrors, uploadWarningMessages)
        if (summaryToast.color !== 'success') {
          addToast(summaryToast)
        }
      } else if (errorCount === 0 && uploadWarningMessages.length > 0) {
        addToast(getUploadWarningSummaryToast(successCount, uploadWarningMessages))
      }

      // Keep completed uploads visible long enough to review, then remove successes.
      uploadClearTimeoutRef.current = setTimeout(() => {
        setUploadQueue(prev => prev.filter(item => item.status === 'error'))
        uploadClearTimeoutRef.current = null
      }, UPLOAD_HISTORY_SUCCESS_RETENTION_MS)
    } finally {
      if (uploadAbortControllerRef.current === uploadController) {
        uploadAbortControllerRef.current = null
      }
    }
  }, [currentPathCanWrite, currentPath, ensureDirectoryExists, filesQueryKey, queryClient])

  const handleUploadInputChange = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    void handleUpload(event.target.files)
    event.target.value = ''
  }, [handleUpload])

  useEffect(() => {
    return () => {
      uploadSessionRef.current += 1
      uploadAbortControllerRef.current?.abort()
      uploadAbortControllerRef.current = null
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
    if (!currentPathCanWrite) return
    e.preventDefault()
    e.stopPropagation()
    dragCountRef.current++
    if (e.dataTransfer.types.includes('Files')) {
      setIsDragging(true)
    }
  }, [currentPathCanWrite])

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    if (!currentPathCanWrite) return
    e.preventDefault()
    e.stopPropagation()
    dragCountRef.current--
    if (dragCountRef.current === 0) {
      setIsDragging(false)
    }
  }, [currentPathCanWrite])

  const handleDragOver = useCallback((e: React.DragEvent) => {
    if (!currentPathCanWrite) return
    e.preventDefault()
    e.stopPropagation()
  }, [currentPathCanWrite])

  const handleDrop = useCallback((e: React.DragEvent) => {
    if (!currentPathCanWrite) return
    e.preventDefault()
    e.stopPropagation()
    dragCountRef.current = 0
    setIsDragging(false)
    
    const files = e.dataTransfer.files
    if (files.length > 0) {
      handleUpload(files)
    }
  }, [currentPathCanWrite, handleUpload])

  // Batch delete handler
  const handleBatchDelete = useCallback(async () => {
    if (isBatchDeleting) return
    if (!batchDeleteIntent) {
      addToast({ title: '删除策略不可确认，删除操作已停用', color: 'warning' })
      return
    }
    if (currentPathRef.current !== batchDeleteIntent.directoryPath) {
      onBatchDeleteClose()
      setBatchDeleteIntent(null)
      return
    }

    batchDeleteAbortControllerRef.current?.abort()
    const controller = new AbortController()
    batchDeleteAbortControllerRef.current = controller
    const { signal } = controller
    setIsBatchDeleting(true)
    const targets = batchDeleteIntent.targets
    const paths = targets.map((target) => target.path)
    let deletedCount = 0
    let synchronizedMissingCount = 0
    let errorCount = 0
    const succeededPaths: string[] = []
    const failedPaths: string[] = []
    const failedErrors: unknown[] = []
    let serverWarningCount = 0
    let succeededPathsSynchronized = false

    const synchronizeSucceededPaths = () => {
      if (succeededPathsSynchronized || succeededPaths.length === 0) {
        return
      }

      succeededPathsSynchronized = true
      const focusFallback = deleteFocusFallbackRef.current
      if (focusFallback) {
        focusFallback.tabIndex = -1
      }
      setBatchDeleteFallbackReturnFocus(focusFallback)
      removeFilesFromCache(succeededPaths)
      queryClient.invalidateQueries({ queryKey: filesQueryKey })
      if (batchDeleteIntent.policy.mode === 'trash' && deletedCount > 0) {
        queryClient.invalidateQueries({ queryKey: ['trash'] })
      }
    }

    try {
      for (let index = 0; index < paths.length; index++) {
        const target = targets[index]!
        const path = target.path
        if (signal.aborted) {
          return
        }

        try {
          const result = await deleteFileWithMissingSync(path, {
            expectedDeleteMode: batchDeleteIntent.policy.mode,
            expectedDeletePolicyToken: batchDeleteIntent.policy.token,
            expectedDeleteTargetToken: target.deleteTargetToken,
            signal,
          })
          succeededPaths.push(path)
          if (isMissingFileActionResult(result)) {
            synchronizedMissingCount++
          } else {
            deletedCount++
            if (result.warning) {
              serverWarningCount++
            }
          }
          if (signal.aborted) {
            return
          }
        } catch (error) {
          if (signal.aborted || isAbortError(error)) {
            return
          }

          if (isDeletePolicyChangedError(error)) {
            const preservedPaths = Array.from(new Set([
              ...failedPaths,
              ...paths.slice(index),
            ]))
            synchronizeSucceededPaths()
            setSelection(preservedPaths)
            setDeletePolicyRefreshRequired(true)
            onBatchDeleteClose()
            setBatchDeleteIntent(null)
            refreshDeletePolicyAfterDrift({
              mode: batchDeleteIntent.policy.mode,
              deletedCount,
              synchronizedMissingCount,
              remainingCount: preservedPaths.length,
            })
            return
          }

          if (isDeleteTargetChangedError(error)) {
            const preservedPaths = Array.from(new Set([
              ...failedPaths,
              ...paths.slice(index),
            ]))
            synchronizeSucceededPaths()
            setSelection(preservedPaths)
            onBatchDeleteClose()
            setBatchDeleteIntent(null)
            refreshFilesAfterTargetDrift({
              mode: batchDeleteIntent.policy.mode,
              deletedCount,
              synchronizedMissingCount,
              remainingCount: preservedPaths.length,
            })
            return
          }

          errorCount++
          failedPaths.push(path)
          failedErrors.push(error)
        }
      }

      if (signal.aborted) {
        return
      }

      synchronizeSucceededPaths()

      if (errorCount === 0) {
        onBatchDeleteClose()
        setBatchDeleteIntent(null)
        clearSelection()
        const completedParts: string[] = []
        if (deletedCount > 0) {
          completedParts.push(batchDeleteIntent.policy.mode === 'trash'
            ? `已将 ${deletedCount} 个项目移入回收站`
            : `已永久删除 ${deletedCount} 个项目`)
        }
        if (synchronizedMissingCount > 0) {
          completedParts.push(`已同步移除 ${synchronizedMissingCount} 个不存在项目`)
        }
        addToast({
          title: completedParts.join('，') || '批量删除已完成',
          description: serverWarningCount > 0 ? `${serverWarningCount} 项返回了服务端警告` : undefined,
          color: synchronizedMissingCount > 0 || serverWarningCount > 0 ? 'warning' : 'success',
        })
        return
      }

      setSelection(failedPaths)
      setBatchDeleteIntent((current) => current
        ? {
            ...current,
            targets: current.targets.filter((target) => failedPaths.includes(target.path)),
          }
        : null)

      if (deletedCount === 0 && synchronizedMissingCount === 0) {
        if (failedErrors.length > 0 && failedErrors.every(isFilesystemUnavailableError)) {
          addToast({
            title: '批量删除暂不可用',
            description: '文件系统当前不可用，请检查设备状态或稍后重试。',
            color: 'warning',
          })
        } else {
          addToast({ title: '批量删除失败', description: `共 ${errorCount} 个项目删除失败`, color: 'danger' })
        }
        return
      }

      const partialParts: string[] = []
      if (deletedCount > 0) {
        partialParts.push(batchDeleteIntent.policy.mode === 'trash'
          ? `已移入回收站 ${deletedCount} 项`
          : `已永久删除 ${deletedCount} 项`)
      }
      if (synchronizedMissingCount > 0) {
        partialParts.push(`已同步移除 ${synchronizedMissingCount} 个不存在项目`)
      }
      partialParts.push(`失败 ${errorCount} 项`)
      if (serverWarningCount > 0) {
        partialParts.push(`${serverWarningCount} 项带服务端警告`)
      }
      addToast({
        title: '批量删除部分完成',
        description: partialParts.join('；'),
        color: 'warning',
      })
    } finally {
      if (!succeededPathsSynchronized && succeededPaths.length > 0) {
        synchronizeSucceededPaths()
        if (isMountedRef.current && currentPathRef.current === batchDeleteIntent.directoryPath) {
          const succeededPathSet = new Set(succeededPaths)
          setSelection(Array.from(selectedFilesRef.current).filter((path) => !succeededPathSet.has(path)))
        }
      }

      const ownsController = batchDeleteAbortControllerRef.current === controller
      if (ownsController) {
        batchDeleteAbortControllerRef.current = null
      }
      if (isMountedRef.current && (ownsController || batchDeleteAbortControllerRef.current === null)) {
        setIsBatchDeleting(false)
      }
    }
  }, [batchDeleteIntent, clearSelection, deleteFileWithMissingSync, filesQueryKey, isBatchDeleting, onBatchDeleteClose, queryClient, refreshDeletePolicyAfterDrift, refreshFilesAfterTargetDrift, removeFilesFromCache, setBatchDeleteFallbackReturnFocus, setSelection])

  // Batch download handler
  const handleBatchDownload = useCallback(async () => {
    const paths = Array.from(selectedFiles)
    const items = sortedFiles.filter(f => paths.includes(f.path))

    if (items.length === 0) {
      addToast({ title: '未选择可下载的文件', color: 'warning' })
      return
    }

    const controller = new AbortController()
    downloadAbortControllersRef.current.add(controller)

    try {
      const results = await Promise.allSettled(
        items.map((file) => downloadFile(file.path, getDownloadOptionsWithSignal(file, controller.signal)))
      )

      if (controller.signal.aborted) {
        return
      }

      const failedErrors = results
        .filter((result): result is PromiseRejectedResult => result.status === 'rejected')
        .map((result) => result.reason)
      const failed = results.filter((result) => result.status === 'rejected').length
      const succeeded = items.length - failed
      const archiveErrorToast = getSharedArchiveDownloadErrorToast(failedErrors)
      const missingFileErrorToast = getSharedMissingFileDownloadErrorToast(failedErrors)
      const sharedDownloadErrorToast = archiveErrorToast ?? missingFileErrorToast

      if (failed === 0) {
        addToast({ title: `已开始下载 ${succeeded} 项`, color: 'success' })
        return
      }

      if (succeeded === 0) {
        if (failedErrors.length > 0 && failedErrors.every(isFilesystemUnavailableError)) {
          addToast({
            title: '批量下载暂不可用',
            description: '文件系统当前不可用，请检查设备状态或稍后重试。',
            color: 'warning',
          })
        } else if (sharedDownloadErrorToast) {
          addToast(sharedDownloadErrorToast)
        } else {
          addToast({ title: '批量下载失败', description: `共 ${failed} 个项目下载失败`, color: 'danger' })
        }
        return
      }

      addToast({
        title: '部分项目开始下载',
        description: sharedDownloadErrorToast
          ? `已开始 ${succeeded} 项，失败 ${failed} 项；${sharedDownloadErrorToast.description}`
          : `已开始 ${succeeded} 项，失败 ${failed} 项`,
        color: 'warning',
      })
    } finally {
      downloadAbortControllersRef.current.delete(controller)
    }
  }, [selectedFiles, sortedFiles])

  // Keyboard shortcuts handlers
  const handleKeyboardCopy = useCallback(() => {
    if (selectedFiles.size === 0) return
    const copyableSelected = sortedFiles.filter((file) => selectedFiles.has(file.path) && canUseFileSource(file))
    if (copyableSelected.length === 0 || copyableSelected.length !== selectedFiles.size) {
      addToast({ title: '所选项目不可复制', color: 'warning' })
      return
    }
    clipboard.copy(copyableSelected.map((file) => file.path), currentPath)
    addToast({ title: `已复制 ${copyableSelected.length} 个项目`, color: 'success' })
  }, [canUseFileSource, selectedFiles, sortedFiles, currentPath, clipboard])

  const handleKeyboardCut = useCallback(() => {
    if (selectedFiles.size === 0) return
    const writableSelected = sortedFiles.filter((file) => selectedFiles.has(file.path) && canWriteFile(file))
    if (writableSelected.length === 0 || writableSelected.length !== selectedFiles.size) {
      addToast({ title: '所选项目不可移动', color: 'warning' })
      return
    }
    clipboard.cut(writableSelected.map((file) => file.path), currentPath)
    addToast({ title: `已剪切 ${writableSelected.length} 个项目`, color: 'success' })
  }, [canWriteFile, selectedFiles, sortedFiles, currentPath, clipboard])

  const handleKeyboardPaste = useCallback(async () => {
    if (!currentPathCanWrite) return
    if (!clipboard.hasPaths()) return
    
    const { paths, operation, sourcePath } = clipboard
    if (!operation || !sourcePath) return

    const normalizedCurrentPath = normalizePath(currentPath)
    const normalizedSourcePath = normalizePath(sourcePath)
    if (normalizedSourcePath === normalizedCurrentPath) {
      addToast({
        title: operation === 'cut' ? '无法移动到原目录' : '无法复制到原目录',
        description: '目标目录与源目录相同，请选择其他文件夹。',
        color: 'warning',
      })
      return
    }

    pasteAbortControllerRef.current?.abort()
    const controller = new AbortController()
    pasteAbortControllerRef.current = controller
    const { signal } = controller
    
    let successCount = 0
    let errorCount = 0
    const failedPaths: string[] = []
    const stalePaths: string[] = []
    const failedErrors: unknown[] = []
    const warningMessages: string[] = []

    try {
      for (const path of paths) {
        if (signal.aborted) {
          return
        }

        const fileName = path.split('/').pop() || ''
        const destPath = normalizedCurrentPath === '/' ? `/${fileName}` : `${normalizedCurrentPath}/${fileName}`

        try {
          if (operation === 'cut') {
            const result = await moveFileWithMissingSync(path, destPath, { signal })
            if (signal.aborted) {
              return
            }
            if (isMissingFileActionResult(result)) {
              stalePaths.push(path)
            }
            if (result.warning) {
              warningMessages.push(getActionWarningSummary(result))
            }
          } else {
            const result = await copyFileWithMissingSync(path, destPath, { signal })
            if (signal.aborted) {
              return
            }
            if (isMissingFileActionResult(result)) {
              stalePaths.push(path)
            }
            if (result.warning) {
              warningMessages.push(getActionWarningSummary(result))
            }
          }
          successCount++
        } catch (error) {
          if (signal.aborted || isAbortError(error)) {
            return
          }

          errorCount++
          failedPaths.push(path)
          failedErrors.push(error)
        }
      }

      if (signal.aborted) {
        return
      }

      if (operation === 'cut') {
        if (failedPaths.length === 0) {
          clipboard.clear()
        } else {
          clipboard.cut(failedPaths, sourcePath)
        }
      } else if (stalePaths.length > 0) {
        const staleSet = new Set(stalePaths)
        const remainingPaths = paths.filter((path) => !staleSet.has(path))
        if (remainingPaths.length === 0) {
          clipboard.clear()
        } else {
          clipboard.copy(remainingPaths, sourcePath)
        }
      }

      queryClient.invalidateQueries({ queryKey: filesQueryKey })
      if (normalizedSourcePath !== normalizedCurrentPath) {
        queryClient.invalidateQueries({ queryKey: getFilesQueryKey(fileScopeKey, normalizedSourcePath) })
      }

      if (errorCount === 0) {
        const pasteActionLabel = operation === 'cut' ? '移动' : '复制'
        if (warningMessages.length > 0) {
          addToast({
            title: getSynchronizedWarningTitle(warningMessages) ?? `成功${pasteActionLabel} ${successCount} 个文件，但存在警告`,
            color: 'warning',
          })
        } else {
          addToast({ title: `成功${pasteActionLabel} ${successCount} 个文件`, color: 'success' })
        }
        return
      }

      if (successCount === 0) {
        if (failedErrors.length > 0 && failedErrors.every(isFilesystemUnavailableError)) {
          addToast({
            title: `${operation === 'cut' ? '批量移动' : '批量复制'}暂不可用`,
            description: '文件系统当前不可用，请检查设备状态或稍后重试。',
            color: 'warning',
          })
        } else {
          addToast(
            getSharedPathConflictErrorToast(failedErrors)
            ?? getSharedQuotaExceededErrorToast(failedErrors)
            ?? { title: `${operation === 'cut' ? '批量移动' : '批量复制'}失败`, description: `共 ${errorCount} 个项目失败`, color: 'danger' }
          )
        }
        return
      }

      const sharedFailureToast =
        getSharedPathConflictErrorToast(failedErrors)
        ?? getSharedQuotaExceededErrorToast(failedErrors)
      addToast(getPartialBatchActionToast(
        `${operation === 'cut' ? '批量移动' : '批量复制'}部分完成`,
        successCount,
        errorCount,
        warningMessages,
        sharedFailureToast?.description
      ))
    } finally {
      if (pasteAbortControllerRef.current === controller) {
        pasteAbortControllerRef.current = null
      }
    }
  }, [currentPathCanWrite, clipboard, currentPath, moveFileWithMissingSync, copyFileWithMissingSync, filesQueryKey, fileScopeKey, queryClient])

  const handleKeyboardDelete = useCallback(() => {
    if (selectedFiles.size > 0) {
      handleOpenBatchDeleteModal()
      return
    }

    const target = getFocusedOrActiveFile()
    if (!target) return
    handleOpenDeleteModal(target.file)
  }, [getFocusedOrActiveFile, handleOpenBatchDeleteModal, handleOpenDeleteModal, selectedFiles.size])

  const handleKeyboardRename = useCallback(() => {
    const target = selectedFiles.size === 1
      ? sortedFiles.find(f => f.path === Array.from(selectedFiles)[0])
      : getFocusedOrActiveFile()?.file
    if (target && canWriteFile(target)) {
      handleOpenRenameModal(target)
    }
  }, [canWriteFile, getFocusedOrActiveFile, handleOpenRenameModal, selectedFiles, sortedFiles])

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

  const focusFileByKeyboard = useCallback((nextIndex: number) => {
    if (sortedFiles.length === 0) return

    const clampedIndex = Math.max(0, Math.min(nextIndex, sortedFiles.length - 1))
    const file = sortedFiles[clampedIndex]
    if (!file) return

    if (selectedFiles.size > 0) {
      clearSelection()
    }
    setFocusedIndex(clampedIndex)
    setActiveFilePath(file.path)
    lastSelectedIndexRef.current = clampedIndex
  }, [clearSelection, selectedFiles.size, setFocusedIndex, sortedFiles])

  const handleKeyboardArrowDown = useCallback((event?: KeyboardEvent) => {
    if (sortedFiles.length === 0) return
    const newIndex = focusedIndex < 0 ? 0 : Math.min(focusedIndex + 1, sortedFiles.length - 1)
    if (event?.shiftKey) {
      applyKeyboardSelection(newIndex, true)
      return
    }
    focusFileByKeyboard(newIndex)
  }, [applyKeyboardSelection, focusFileByKeyboard, focusedIndex, sortedFiles.length])

  const handleKeyboardArrowUp = useCallback((event?: KeyboardEvent) => {
    if (sortedFiles.length === 0) return
    const newIndex = focusedIndex <= 0 ? 0 : focusedIndex - 1
    if (event?.shiftKey) {
      applyKeyboardSelection(newIndex, true)
      return
    }
    focusFileByKeyboard(newIndex)
  }, [applyKeyboardSelection, focusFileByKeyboard, focusedIndex, sortedFiles.length])

  const handleKeyboardToggleFocusedSelection = useCallback(() => {
    const target = getFocusedOrActiveFile() ?? (sortedFiles[0] ? { file: sortedFiles[0], index: 0 } : null)
    if (!target) return

    toggleFileSelection(target.file.path)
    setFocusedIndex(target.index)
    lastSelectedIndexRef.current = target.index

    if (target.file.isDir) {
      setActiveFilePath(null)
    } else {
      setActiveFilePath(target.file.path)
    }
  }, [getFocusedOrActiveFile, setFocusedIndex, sortedFiles, toggleFileSelection])

  const handleKeyboardEscape = useCallback(() => {
    clearSelection()
    setActiveFilePath(null)
    setFocusedIndex(-1)
    lastSelectedIndexRef.current = null
  }, [clearSelection, setFocusedIndex])

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

  const fileShortcutsEnabled = !(
    isNewFolderOpen
    || isRenameOpen
    || isDeleteOpen
    || isBatchDeleteOpen
    || isShareOpen
    || isMoveOpen
    || isPreviewOpen
    || contextMenu.state.isOpen
  )

  // Register keyboard shortcuts
  useKeyboardShortcuts({
    onDelete: canWrite && deletePolicyAvailable ? handleKeyboardDelete : undefined,
    onSelectAll: handleSelectAll,
    onEscape: handleKeyboardEscape,
    onCopy: canWrite ? handleKeyboardCopy : undefined,
    onCut: canWrite ? handleKeyboardCut : undefined,
    onPaste: currentPathCanWrite ? handleKeyboardPaste : undefined,
    onRename: canWrite ? handleKeyboardRename : undefined,
    onEnter: handleKeyboardEnter,
    onSpace: handleKeyboardToggleFocusedSelection,
    onArrowDown: handleKeyboardArrowDown,
    onArrowUp: handleKeyboardArrowUp,
    onRefresh: handleKeyboardRefresh,
    onNewFolder: currentPathCanWrite ? handleOpenNewFolderModal : undefined,
  }, {
    enabled: fileShortcutsEnabled,
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
  const canMoveSelectedItems = selectedFileItems.length > 0 && selectedFileItems.every(canWriteFile)
  const canCopySelectedItems = selectedFileItems.length > 0 && selectedFileItems.every(canUseFileSource)
  const canDeleteSelectedItems = selectedFileItems.length > 0
    && selectedFileItems.length === selectedFiles.size
    && selectedFileItems.every(canDeleteFile)
  const deleteSelectionHint = !deletePolicyAvailable || !deletePolicy
    ? '删除方式未知，删除操作已停用'
    : deletePolicy.mode === 'trash'
      ? '删除将移入回收站'
      : '删除将永久移除，无法恢复'
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
          <p className="text-default-500">加载记忆中…</p>
        </div>
      </div>
    )
  }

  if (hasInvalidHomeDir) {
    return (
      <div className="relative flex h-full min-h-0 overflow-hidden">
        <div className="flex min-h-0 flex-1 flex-col p-4 sm:p-5 lg:p-7">
          <Breadcrumbs path="/" onNavigate={navigateToFilePath} />
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

  if (error && !data) {
    const errorPresentation = getFilesLoadErrorPresentation(error)
    return (
      <div className="relative flex h-full min-h-0 overflow-hidden">
        <div className="flex min-h-0 flex-1 flex-col p-4 sm:p-5 lg:p-7">
          <Breadcrumbs path={currentPath} onNavigate={navigateToFilePath} />
          <div className="flex-1 flex items-center justify-center surface-card">
            <EmptyState
              icon={AlertCircle}
              title={errorPresentation.title}
              description={errorPresentation.description}
              className="max-w-md"
              action={
                <Button variant="bordered" className="rounded-lg" onPress={handleKeyboardRefresh}>
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
      ref={deleteFocusFallbackRef}
      role="region"
      aria-label="文件上传区域"
      className="relative flex h-full min-h-0 overflow-hidden"
      onDragEnter={handleDragEnter}
      onDragLeave={handleDragLeave}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
      {/* Drag overlay */}
      {isDragging && (
        <div className="absolute inset-0 z-50 bg-content1/95 backdrop-blur-sm flex items-center justify-center border-2 border-dashed border-accent-primary rounded-lg m-4">
          <div className="text-center">
            <div className="w-16 h-16 mx-auto mb-4 rounded-lg bg-accent-primary flex items-center justify-center shadow-[var(--shadow-soft)]">
              <Upload size={32} className="text-white" />
            </div>
            <h3 className="text-xl font-semibold text-foreground mb-2">释放以上传</h3>
            <p className="text-default-500">文件将上传到当前目录</p>
          </div>
        </div>
      )}

      {/* Upload queue panel */}
      {showUploadPanel && uploadQueue.length > 0 && (
        <div className="fixed inset-x-4 bottom-[calc(env(safe-area-inset-bottom)+5.25rem)] z-30 overflow-hidden rounded-lg border border-divider bg-content1 shadow-xl sm:inset-x-auto sm:right-6 sm:bottom-6 sm:w-96">
          <div className="flex items-center justify-between px-4 py-3 bg-content2 border-b border-divider">
            <div className="flex items-center gap-2">
              <Upload size={16} className="text-accent-primary" />
              <span className="font-medium text-sm">
                {getUploadPanelTitle(isUploading, uploadCounts)}
              </span>
            </div>
            <div className="flex items-center gap-1">
              {isUploading && (
                <button
                  type="button"
                  onClick={handleCancelUpload}
                  className="flex items-center gap-1 rounded px-2 py-1 text-xs text-default-500 transition-colors hover:bg-content3 hover:text-default-700"
                  aria-label="取消上传"
                >
                  <X size={12} />
                  取消
                </button>
              )}
              {!isUploading && (
                <button
                  type="button"
                  onClick={() => setUploadQueue([])}
                  className="px-2 py-1 text-xs text-default-500 hover:text-default-700 hover:bg-content3 rounded transition-colors"
                  aria-label="清空上传记录"
                >
                  清空
                </button>
              )}
              <button
                type="button"
                onClick={() => setShowUploadPanel(false)}
                className="p-1.5 hover:bg-content3 rounded transition-colors"
                aria-label="隐藏上传记录"
              >
                <X size={14} className="text-default-500" />
              </button>
            </div>
          </div>
          <div className="max-h-72 overflow-y-auto">
            {uploadQueue.map((item, i) => {
              const uploadDisplayName = item.relativePath || item.file.name
              const uploadProgressPercent = Math.round(item.progress)
              return (
                <div key={i} className="px-4 py-3 border-b border-divider last:border-b-0 hover:bg-content2/50 transition-colors">
                  <div className="flex items-center gap-2.5 mb-1.5">
                    {item.status === 'done' && <CheckCircle2 size={16} className="text-emerald-500 flex-shrink-0" />}
                    {item.status === 'error' && <AlertCircle size={16} className="text-rose flex-shrink-0" />}
                    {item.status === 'cancelled' && <X size={16} className="text-default-400 flex-shrink-0" />}
                    {(item.status === 'pending' || item.status === 'uploading') && (
                      <div className="w-4 h-4 border-2 border-accent-primary border-t-transparent rounded-full animate-spin flex-shrink-0" />
                    )}
                    <span className="text-sm truncate flex-1" title={uploadDisplayName}>
                      {uploadDisplayName}
                    </span>
                    {item.status === 'uploading' && (
                      <span className="text-xs text-default-400">{uploadProgressPercent}%</span>
                    )}
                  </div>
                  {item.status === 'uploading' && (
                    <Progress
                      size="sm"
                      value={item.progress}
                      aria-label={`${uploadDisplayName} 上传进度`}
                      aria-valuetext={`${uploadProgressPercent}% 已上传`}
                      classNames={{
                        base: "h-1.5",
                        indicator: "bg-accent-primary"
                      }}
                    />
                  )}
                  {item.status === 'error' && (
                    <p className="text-xs text-rose mt-1">{item.error}</p>
                  )}
                  {item.status === 'cancelled' && (
                    <p className="text-xs text-default-500 mt-1">{item.error}</p>
                  )}
                </div>
              )
            })}
          </div>
          {/* Summary footer */}
          {!isUploading && uploadQueue.length > 0 && (
            <div className="px-4 py-2.5 bg-content2 border-t border-divider text-xs text-default-500">
              共 {uploadCounts.total} 个文件，
              成功 {uploadCounts.done} 个
              {uploadCounts.error > 0 && (
                <span className="text-rose">，失败 {uploadCounts.error} 个</span>
              )}
              {uploadCounts.cancelled > 0 && (
                <span>，取消 {uploadCounts.cancelled} 个</span>
              )}
            </div>
          )}
        </div>
      )}

      <div className="flex min-h-0 flex-1 flex-col p-4 sm:p-5 lg:p-7">
        <input ref={fileInputRef} type="file" multiple className="hidden" aria-label="选择上传文件" onChange={handleUploadInputChange} />
        <input ref={folderInputRef} {...directoryUploadInputProps} onChange={handleUploadInputChange} />
        
        {/* Breadcrumbs */}
        <Breadcrumbs path={currentPath} onNavigate={navigateToFilePath} />

        {canWrite && data && !deletePolicyAvailable && (
          <div role="alert" className="mb-3 flex items-center gap-3 rounded-lg border border-warning/30 bg-warning/10 px-3 py-2 text-sm text-default-700">
            <AlertCircle size={16} className="shrink-0 text-warning" />
            <div className="min-w-0 flex-1">
              <p className="font-medium">删除操作已停用</p>
              <p className="text-xs text-default-600">
                {deletePolicyRefreshRequired
                  ? '检测到删除策略已更改，正在重新确认。为避免文件被永久删除，删除操作已停用。'
                  : '无法确认当前删除策略。为避免文件被永久删除，删除操作已停用。'}
              </p>
            </div>
            <Button
              size="sm"
              variant="bordered"
              className="rounded-lg"
              onPress={handleRefreshDeletePolicy}
              isLoading={isFetching}
            >
              重新加载删除策略
            </Button>
          </div>
        )}
        {(isDeleteIntentPreparing || isBatchDeleteIntentPreparing) && (
          <div
            role="status"
            aria-live="polite"
            className="mb-4 flex items-center gap-2 rounded-lg border border-divider bg-content1 px-4 py-3 text-sm text-default-600"
          >
            <span className="h-4 w-4 animate-spin rounded-full border-2 border-accent-primary border-t-transparent" aria-hidden="true" />
            正在确认删除目标…
          </div>
        )}
        
        {/* Toolbar */}
        <div className="mb-4 rounded-lg border border-divider bg-content1/95 shadow-[var(--shadow-soft)]">
          <div className="flex flex-col gap-3 p-3 xl:flex-row xl:items-center xl:justify-between">
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              {hasSelection ? (
                <>
                  <div className="flex h-10 min-w-0 items-center gap-2 rounded-lg bg-content2 px-3 text-sm">
                    <span className="text-default-600">已选</span>
                    <span className="font-semibold text-foreground">{selectedFiles.size}</span>
                    <span className="text-default-600">项</span>
                    <span className="hidden text-default-400 sm:inline">
                      {selectedCounts.files} 文件 / {selectedCounts.folders} 文件夹
                    </span>
                  </div>
                  <Button
                    variant="bordered"
                    className="btn-secondary btn-sm rounded-lg"
                    startContent={<X size={16} />}
                    onPress={clearSelection}
                  >
                    取消选择
                  </Button>
                  <Button
                    color="primary"
                    variant="flat"
                    className="rounded-lg"
                    startContent={<Download size={16} />}
                    onPress={handleBatchDownload}
                  >
                    批量下载
                  </Button>
                  {canWrite && (
                    <Button
                      variant="bordered"
                      className="btn-secondary btn-sm rounded-lg text-default-600"
                      startContent={<Move size={16} />}
                      onPress={() => handleOpenMoveModal(selectedFileItems)}
                      isDisabled={!canMoveSelectedItems}
                    >
                      批量移动
                    </Button>
                  )}
                  {canWrite && (
                    <Button
                      variant="bordered"
                      className="btn-secondary btn-sm rounded-lg text-default-600"
                      startContent={<Files size={16} />}
                      onPress={() => handleOpenCopyModal(selectedFileItems)}
                      isDisabled={!canCopySelectedItems}
                    >
                      批量复制
                    </Button>
                  )}
                  {canWrite && (
                    <Button
                      color="danger"
                      variant="flat"
                      className="rounded-lg"
                      startContent={<Trash2 size={16} />}
                      onPress={(event) => handleOpenBatchDeleteModal(event.target)}
                      isDisabled={!canDeleteSelectedItems || isBatchDeleteIntentPreparing}
                      isLoading={isBatchDeleteIntentPreparing}
                    >
                      批量删除
                    </Button>
                  )}
                  {!canWrite && (
                    <span className="text-sm text-default-500">
                      只读账户可查看、预览和下载文件
                    </span>
                  )}
                  <Dropdown>
                    <DropdownTrigger>
                      <button type="button" className="h-9 rounded-lg px-2.5 text-sm text-default-500 transition-colors hover:bg-content2 hover:text-foreground">
                        选择工具
                      </button>
                    </DropdownTrigger>
                    <DropdownMenu
                      aria-label="选择工具"
                      classNames={{ base: "bg-content1 border border-divider shadow-lg" }}
                    >
                      <DropdownSection title="选择" showDivider>
                        <DropdownItem key="clear" onPress={clearSelection} startContent={<X size={14} />}>
                          清空选择
                        </DropdownItem>
                        <DropdownItem key="invert" onPress={handleInvertSelection} startContent={<RotateCcw size={14} />}>
                          反选
                        </DropdownItem>
                      </DropdownSection>
                      <DropdownItem key="only-files" onPress={handleSelectOnlyFiles} isDisabled={totalCounts.files === 0} startContent={<Files size={14} />}>
                        {totalCounts.files === 0 ? '仅文件（无文件）' : '仅文件'}
                      </DropdownItem>
                      <DropdownItem key="only-folders" onPress={handleSelectOnlyFolders} isDisabled={totalCounts.folders === 0} startContent={<Folder size={14} />}>
                        {totalCounts.folders === 0 ? '仅文件夹（无文件夹）' : '仅文件夹'}
                      </DropdownItem>
                    </DropdownMenu>
                  </Dropdown>
                </>
              ) : (
                <>
                  {currentPathCanWrite ? (
                    <>
                      <Button
                        className="btn-primary btn-md border-none font-medium rounded-lg"
                        startContent={<Upload size={16} />}
                        onPress={() => fileInputRef.current?.click()}
                        isLoading={isUploading}
                      >
                        {isUploading ? '上传中…' : '上传文件'}
                      </Button>
                      <Button
                        variant="bordered"
                        className="btn-secondary btn-md rounded-lg"
                        startContent={<FolderUp size={16} />}
                        onPress={() => folderInputRef.current?.click()}
                        isLoading={isUploading}
                        isDisabled={isUploading}
                      >
                        上传文件夹
                      </Button>
                      <Button
                        variant="bordered"
                        className="btn-secondary btn-md rounded-lg"
                        startContent={<FolderPlus size={16} />}
                        onPress={handleOpenNewFolderModal}
                      >
                        新建文件夹
                      </Button>
                    </>
                  ) : (
                    <div className="rounded-lg border border-divider bg-content2/50 px-3 py-2 text-sm text-default-500">
                      {canWrite ? '当前目录为只读，可查看、预览和下载文件' : '只读账户可查看、预览和下载文件'}
                    </div>
                  )}
                </>
              )}
            </div>

            <div className="flex shrink-0 items-center gap-2 self-start xl:self-auto">
              <div className="flex items-center gap-1 rounded-lg border border-divider bg-content1 p-0.5 shadow-[var(--shadow-soft)]">
                <Dropdown placement="bottom-end">
                  <DropdownTrigger>
                    <button
                      type="button"
                      className="flex h-9 items-center gap-2 rounded-lg px-2.5 text-sm text-default-600 transition-colors hover:bg-content2 hover:text-foreground"
                      aria-label={`排序：${sortLabels[sortBy as SortKey]}`}
                    >
                      <ArrowUpDown size={15} />
                      <span className="hidden sm:inline">排序：{sortLabels[sortBy as SortKey]}</span>
                    </button>
                  </DropdownTrigger>
                  <DropdownMenu
                    aria-label="排序字段"
                    classNames={{ base: "bg-content1 border border-divider shadow-lg" }}
                  >
                    <DropdownItem key="name" onPress={() => setSortBy('name')}>
                      按名称
                    </DropdownItem>
                    <DropdownItem key="size" onPress={() => setSortBy('size')}>
                      按大小
                    </DropdownItem>
                    <DropdownItem key="modTime" onPress={() => setSortBy('modTime')}>
                      按修改时间
                    </DropdownItem>
                  </DropdownMenu>
                </Dropdown>
                <button
                  type="button"
                  className="flex h-9 w-9 items-center justify-center rounded-lg text-xs font-semibold text-default-600 transition-colors hover:bg-content2 hover:text-foreground"
                  onClick={toggleSortOrder}
                  aria-label={sortOrder === 'asc' ? '切换为降序' : '切换为升序'}
                  title={sortOrder === 'asc' ? '升序' : '降序'}
                >
                  {sortOrder === 'asc' ? (
                    <ArrowUpNarrowWide size={16} aria-hidden="true" />
                  ) : (
                    <ArrowDownWideNarrow size={16} aria-hidden="true" />
                  )}
                </button>
              </div>

              <div className="flex bg-content1 border border-divider rounded-lg p-0.5 shadow-[var(--shadow-soft)]">
                <button
                  type="button"
                  className={cn("flex h-9 w-9 items-center justify-center rounded-lg transition-all", viewMode === 'list' ? "bg-accent-primary text-white shadow-sm" : "text-default-500 hover:text-default-600")}
                  onClick={() => setViewMode('list')}
                  aria-label="列表视图"
                  aria-pressed={viewMode === 'list'}
                >
                  <List size={16} />
                </button>
                <button
                  type="button"
                  className={cn("flex h-9 w-9 items-center justify-center rounded-lg transition-all", viewMode === 'grid' ? "bg-accent-primary text-white shadow-sm" : "text-default-500 hover:text-default-600")}
                  onClick={() => setViewMode('grid')}
                  aria-label="网格视图"
                  aria-pressed={viewMode === 'grid'}
                >
                  <Grid size={16} />
                </button>
              </div>
          
              {uploadQueue.length > 0 && (
                <button
                  type="button"
                  onClick={() => setShowUploadPanel(!showUploadPanel)}
                  className={cn(
                    "relative p-2.5 rounded-lg border transition-all",
                    showUploadPanel 
                      ? "bg-accent-primary text-white border-accent-primary shadow-sm" 
                      : "bg-content1 border-divider text-default-500 hover:text-default-600 hover:border-default-400"
                  )}
                  title="上传记录"
                  aria-label="上传记录"
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
          </div>

          {hasSelection && (
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-divider px-3 pb-3 text-xs text-default-500">
              <span>下载将处理 {selectedCounts.files + selectedCounts.folders} 项</span>
              <span>{deleteSelectionHint}</span>
              {selectedCounts.folders > 0 && <span>文件夹会打包为 ZIP</span>}
            </div>
          )}
        </div>

        {/* File List / Grid */}
        {canWrite && favoritesError && (
          <div className="mb-3 flex items-center gap-3 rounded-lg border border-divider bg-content1 px-3 py-2 text-sm text-default-600">
            <AlertCircle size={16} className="shrink-0 text-warning" />
            <div className="min-w-0 flex-1">
              <p className="font-medium">{favoritesBanner?.title ?? '收藏状态加载失败'}</p>
              <p className="truncate text-xs text-default-500">{favoritesBanner?.description ?? GENERIC_LOAD_ERROR_DESCRIPTION}</p>
            </div>
            <Button size="sm" variant="bordered" className="rounded-lg" onPress={handleRefreshFavoritesBanner}>
              重新加载收藏状态
            </Button>
          </div>
        )}

        {canWrite && shareBanner && (
          <div className="mb-3 flex items-center gap-3 rounded-lg border border-divider bg-content1 px-3 py-2 text-sm text-default-600">
            <AlertCircle size={16} className="shrink-0 text-warning" />
            <div className="min-w-0 flex-1">
              <p className="font-medium">{shareBanner.title}</p>
              <p className="truncate text-xs text-default-500">{shareBanner.description}</p>
            </div>
          </div>
        )}

        {viewMode === 'list' ? (
          <div className="surface-card flex min-h-0 flex-1 flex-col overflow-hidden">
            {/* Header */}
            <div className="grid grid-cols-[36px_minmax(0,1fr)_36px] gap-3 px-3 py-3 table-head text-[11px] font-semibold sm:grid-cols-[44px_minmax(0,1fr)_88px_118px_40px] sm:gap-4 sm:px-5 md:grid-cols-[44px_minmax(0,1fr)_100px_150px_120px_40px]">
              <div className="flex items-center justify-center">
                <SelectionCheckboxButton
                  isSelected={isAllSelected}
                  isPartialSelected={isPartialSelected && !isAllSelected}
                  label={isAllSelected ? '取消全选' : '全选当前目录'}
                  onClick={handleSelectAll}
                  visualClassName={!isAllSelected && !isPartialSelected ? "group-hover/selection:border-accent-primary" : undefined}
                />
              </div>
              <div>名称</div>
              <div className="hidden sm:block">大小</div>
              <div className="hidden sm:block">修改时间</div>
              <div className="hidden md:block">时光印记</div>
              <div className="hidden items-center justify-end md:flex">
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
              role="region"
              aria-label="文件列表内容"
              className="relative min-h-0 flex-1 overflow-auto custom-scrollbar"
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
                      key={file.path}
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
                        key={file.path}
                        file={file}
                        isSelected={selectedFiles.has(file.path)}
                        isActive={activeFilePath === file.path}
                        isFavorited={favoritesData?.[file.path] ?? false}
                        favoriteActionsAvailable={favoriteActionsAvailable}
                        favoriteUnavailableLabel={favoriteUnavailableLabel}
                        shareActionsAvailable={shareActionsAvailable}
                        shareActionLabel={shareActionLabel}
                        isMultiSelection={hasMultiSelection}
                        canWrite={canWriteFile(file)}
                        canDelete={canDeleteFile(file)}
                        deletePreparing={isDeleteIntentPreparing}
                        canShareFile={canUseFileSource(file)}
                        onSelect={(e) => handleFileSelection(file, virtualItem.index, e, 'toggle')}
                        onOpen={() => handleFileOpen(file)}
                        onActivate={(e) => handleFileActivate(file, virtualItem.index, e)}
                        onRename={() => handleOpenRenameModal(file)}
                        onDelete={() => handleOpenDeleteModal(file)}
                        onViewVersions={() => handleViewVersions(file)}
                        onShare={() => handleOpenShareModal(file)}
                        onToggleFavorite={() => handleToggleFavorite(
                          file.path,
                          favoritesData?.[file.path] ?? false
                        )}
                        onDownload={() => handleFileDownload(file)}
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
                    description="上传文件后会显示在这里"
                    className="max-w-md"
                  />
                </div>
              )}
            </div>
          </div>
        ) : (
          /* Grid View */
          <div
            role="region"
            aria-label="文件网格内容"
            className="min-h-0 flex-1 overflow-auto custom-scrollbar"
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
                  description="上传文件后会显示在这里"
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
                <div className="grid grid-cols-[repeat(auto-fill,minmax(140px,1fr))] gap-3 sm:grid-cols-[repeat(auto-fill,minmax(160px,1fr))] sm:gap-4">
                  {sortedFiles.map((file, index) => (
                    <FileCard
                      key={file.path}
                      file={file}
                      isSelected={selectedFiles.has(file.path)}
                      isActive={activeFilePath === file.path}
                      isFavorited={favoritesData?.[file.path] ?? false}
                      favoriteActionsAvailable={favoriteActionsAvailable}
                      favoriteUnavailableLabel={favoriteUnavailableLabel}
                      shareActionsAvailable={shareActionsAvailable}
                      shareActionLabel={shareActionLabel}
                      isMultiSelection={hasMultiSelection}
                      canWrite={canWriteFile(file)}
                      canDelete={canDeleteFile(file)}
                      deletePreparing={isDeleteIntentPreparing}
                      canShareFile={canUseFileSource(file)}
                      onSelect={(e) => handleFileSelection(file, index, e, 'toggle')}
                      onOpen={() => handleFileOpen(file)}
                      onActivate={(e) => handleFileActivate(file, index, e)}
                      onRename={() => handleOpenRenameModal(file)}
                      onDelete={() => handleOpenDeleteModal(file)}
                      onViewVersions={() => handleViewVersions(file)}
                      onShare={() => handleOpenShareModal(file)}
                      onToggleFavorite={() => handleToggleFavorite(
                        file.path,
                        favoritesData?.[file.path] ?? false
                      )}
                      onDownload={() => handleFileDownload(file)}
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
          base: "bg-content1 border border-divider shadow-xl rounded-lg",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-lg bg-accent-primary/10 text-accent-primary flex items-center justify-center">
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
                aria-label="文件夹名称"
                placeholder="请输入文件夹名称"
                value={newFolderName}
                onValueChange={setNewFolderName}
                autoFocus
                size="lg"
                variant="bordered"
                isInvalid={Boolean(displayedNewFolderNameValidationError)}
                errorMessage={displayedNewFolderNameValidationError ?? undefined}
                classNames={{
                  inputWrapper: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                  input: "text-sm placeholder:text-default-400",
                }}
              />
              {displayedNewFolderNameValidationError && (
                <p className="mt-2 text-xs text-warning">
                  {displayedNewFolderNameValidationError}
                </p>
              )}
              <div className="flex items-center justify-between text-xs mt-2">
                <span className="text-default-500">支持中文与英文名称</span>
                <span className="text-default-400">建议 2-24 个字符</span>
              </div>
            </div>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button variant="flat" onPress={handleCloseNewFolderModal} isDisabled={createFolderMutation.isPending} className="text-default-600 rounded-lg">取消</Button>
            <Button color="primary" onPress={handleCreateFolder} isLoading={createFolderMutation.isPending} isDisabled={Boolean(newFolderNameValidationError)} className="rounded-lg">创建</Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      <Modal
        isOpen={isRenameOpen}
        onClose={handleCloseRenameModal}
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
                aria-label="新名称"
                placeholder="请输入新名称"
                value={renameValue}
                onValueChange={setRenameValue}
                autoFocus
                size="lg"
                variant="bordered"
                isInvalid={Boolean(displayedRenameNameValidationError)}
                errorMessage={displayedRenameNameValidationError ?? undefined}
                classNames={{
                  inputWrapper: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                  input: "text-sm placeholder:text-default-400",
                }}
              />
              {displayedRenameNameValidationError && (
                <p className="mt-2 text-xs text-warning">
                  {displayedRenameNameValidationError}
                </p>
              )}
            </div>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button variant="flat" onPress={handleCloseRenameModal} isDisabled={renameMutation.isPending} className="text-default-600 rounded-lg">取消</Button>
            <Button color="primary" onPress={handleRename} isLoading={renameMutation.isPending} isDisabled={Boolean(renameNameValidationError) || renameValue.trim() === actionFile?.name} className="rounded-lg">确定</Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

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
          aria-labelledby="single-delete-dialog-title"
          aria-describedby="single-delete-dialog-description"
          aria-busy={deleteMutation.isPending}
        >
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-lg bg-danger/10 text-danger flex items-center justify-center">
              <AlertCircle size={20} />
            </div>
            <div>
              <h3 id="single-delete-dialog-title" className="text-lg font-semibold text-foreground">
                {deleteIntent ? getDeleteActionLabel(deleteIntent.policy.mode) : '确认删除'}
              </h3>
              <p className="text-xs text-default-500 font-normal">
                {deleteIntent?.policy.mode === 'permanent' ? '项目将被永久移除' : '项目将进入回收站'}
              </p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4">
            <div id="single-delete-dialog-description">
              <p className="text-default-600">
                确定要{deleteIntent?.policy.mode === 'permanent' ? '永久删除' : '将'}{' '}
                <strong className="text-foreground">{deleteIntent?.target.name}</strong>
                {deleteIntent?.policy.mode === 'trash' ? ' 移入回收站' : ''}吗？
              </p>
              {deleteIntent && (
                <>
                  <p className="text-xs text-default-500 mt-2">{getDeleteConsequence(deleteIntent.policy.mode)}</p>
                  {deleteIntent.policy.mode === 'trash' && (
                    <p className="text-xs text-default-500 mt-2">{getDeletePolicyDescription(deleteIntent.policy)}</p>
                  )}
                </>
              )}
            </div>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button ref={deleteCancelButtonRef} autoFocus variant="flat" onPress={handleCloseDeleteModal} isDisabled={deleteMutation.isPending} className="text-default-600 rounded-lg">取消</Button>
            <Button
              color="danger"
              aria-label={deleteIntent
                ? `${getDeleteActionLabel(deleteIntent.policy.mode)} ${deleteIntent.target.name}`
                : '确认删除'}
              onPress={handleDelete}
              isLoading={deleteMutation.isPending}
              className="rounded-lg"
            >
              {deleteIntent ? getDeleteActionLabel(deleteIntent.policy.mode) : '删除'}
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

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
          aria-labelledby="batch-delete-dialog-title"
          aria-describedby="batch-delete-dialog-description"
          aria-busy={isBatchDeleting}
        >
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-lg bg-danger/10 text-danger flex items-center justify-center">
              <Trash2 size={20} />
            </div>
            <div>
              <h3 id="batch-delete-dialog-title" className="text-lg font-semibold text-foreground">
                {batchDeleteIntent ? getDeleteActionLabel(batchDeleteIntent.policy.mode, true) : '批量删除'}
              </h3>
              <p className="text-xs text-default-500 font-normal">
                {batchDeleteIntent?.policy.mode === 'permanent' ? '选中项目将被永久移除' : '选中项目将进入回收站'}
              </p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4">
            <div id="batch-delete-dialog-description">
              <p className="text-default-600">
                确定要处理选中的 <strong className="text-foreground">{batchDeleteIntent?.targets.length ?? 0}</strong> 个项目吗？
              </p>
              {batchDeleteIntent && (
                <>
                  <p className="text-xs text-default-500 mt-2">{getDeleteConsequence(batchDeleteIntent.policy.mode, true)}</p>
                  {batchDeleteIntent.policy.mode === 'trash' && (
                    <p className="text-xs text-default-500 mt-2">{getDeletePolicyDescription(batchDeleteIntent.policy)}</p>
                  )}
                </>
              )}
            </div>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button ref={batchDeleteCancelButtonRef} autoFocus variant="flat" onPress={handleCloseBatchDeleteModal} isDisabled={isBatchDeleting} className="text-default-600 rounded-lg">取消</Button>
            <Button
              color="danger"
              aria-label={batchDeleteIntent
                ? `${getDeleteActionLabel(batchDeleteIntent.policy.mode, true)} ${batchDeleteIntent.targets.length} 个项目`
                : '批量删除'}
              onPress={handleBatchDelete}
              isLoading={isBatchDeleting}
              className="rounded-lg"
            >
              {batchDeleteIntent ? getDeleteActionLabel(batchDeleteIntent.policy.mode, true) : '批量删除'}
            </Button>
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
                >
                  批量下载
                </ContextMenuItem>
                {canWrite && (
                  <ContextMenuItem
                    icon={<Move size={16} />}
                    onClick={() => {
                      handleOpenMoveModal(selectedFileItems)
                      contextMenu.hide()
                    }}
                    disabled={!canMoveSelectedItems}
                  >
                    批量移动{!canMoveSelectedItems ? '（无可移动项）' : ''}
                  </ContextMenuItem>
                )}
                {canWrite && (
                  <ContextMenuItem
                    icon={<Files size={16} />}
                    onClick={() => {
                      handleOpenCopyModal(selectedFileItems)
                      contextMenu.hide()
                    }}
                    disabled={!canCopySelectedItems}
                  >
                    批量复制{!canCopySelectedItems ? '（无可复制项）' : ''}
                  </ContextMenuItem>
                )}
                {canWrite && (
                  <ContextMenuItem
                    icon={<Trash2 size={16} />}
                    danger
                    onClick={() => {
                      handleOpenBatchDeleteModal()
                      contextMenu.hide()
                    }}
                    disabled={!canDeleteSelectedItems || isBatchDeleteIntentPreparing}
                  >
                    {deletePolicy?.mode === 'permanent'
                      ? '批量永久删除'
                      : deletePolicy?.mode === 'trash'
                        ? '批量移入回收站'
                        : '批量删除（策略未知）'}
                  </ContextMenuItem>
                )}
              </ContextMenuSection>
            ) : (
              <>
                <ContextMenuSection title="操作" showDivider>
                  {contextMenuFile.isDir ? (
                    <>
                      <ContextMenuItem
                        icon={<FolderOpen size={16} />}
                        onClick={() => {
                          navigateToFilePath(contextMenuFile.path)
                          contextMenu.hide()
                        }}
                      >
                        打开文件夹
                      </ContextMenuItem>
                      <ContextMenuItem
                        icon={<Download size={16} />}
                        onClick={handleContextMenuDownload}
                      >
                        下载为 ZIP
                      </ContextMenuItem>
                    </>
                  ) : (
                    <ContextMenuItem
                      icon={<Download size={16} />}
                      onClick={handleContextMenuDownload}
                    >
                      下载
                    </ContextMenuItem>
                  )}
                  {canWriteFile(contextMenuFile) && (
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
                  {canWriteFile(contextMenuFile) && (
                    <ContextMenuItem
                      icon={<Move size={16} />}
                      onClick={() => {
                        handleOpenMoveModal([contextMenuFile])
                        contextMenu.hide()
                      }}
                    >
                      移动到…
                    </ContextMenuItem>
                  )}
                  {canUseFileSource(contextMenuFile) && (
                    <ContextMenuItem
                      icon={<Files size={16} />}
                      onClick={() => {
                        handleOpenCopyModal([contextMenuFile])
                        contextMenu.hide()
                      }}
                    >
                      复制到…
                    </ContextMenuItem>
                  )}
                  <ContextMenuItem
                    icon={<Copy size={16} />}
                    onClick={handleContextMenuCopyPath}
                  >
                    复制路径
                  </ContextMenuItem>
                </ContextMenuSection>
                <ContextMenuSection title={canUseFileSource(contextMenuFile) ? '分享' : '历史'} showDivider>
                  {canUseFileSource(contextMenuFile) && (
                    <ContextMenuItem
                      icon={<Star size={16} className={favoritesData?.[contextMenuFile.path] ? "fill-accent-primary text-accent-primary" : ""} />}
                      disabled={!favoriteActionsAvailable}
                      onClick={() => {
                        handleToggleFavorite(
                          contextMenuFile.path,
                          favoritesData?.[contextMenuFile.path] ?? false
                        )
                        contextMenu.hide()
                      }}
                    >
                      {favoriteActionsAvailable ? (favoritesData?.[contextMenuFile.path] ? '取消收藏' : '添加收藏') : favoriteUnavailableLabel}
                    </ContextMenuItem>
                  )}
                  {canUseFileSource(contextMenuFile) && (
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
                {canDeleteFile(contextMenuFile) && (
                  <ContextMenuSection>
                    <ContextMenuItem
                      icon={<Trash2 size={16} />}
                      danger
                      onClick={() => {
                        handleOpenDeleteModal(contextMenuFile)
                        contextMenu.hide()
                      }}
                      disabled={isDeleteIntentPreparing}
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
