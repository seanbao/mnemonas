import { normalizePath } from './utils'

export type UploadQueueStatus = 'pending' | 'uploading' | 'done' | 'error' | 'cancelled'

export interface UploadQueueItem {
  file: File
  relativePath?: string
  folderRelativePath?: string
  progress: number
  status: UploadQueueStatus
  error?: string
}

export interface UploadQueueCounts {
  total: number
  done: number
  error: number
  cancelled: number
  finished: number
}

interface BuildUploadQueueOptions {
  maxUploadFileSizeBytes: number
  maxUploadFileSizeLabel: string
}

export const INVALID_FOLDER_UPLOAD_PATH_MESSAGE = '文件夹路径无效，已跳过上传'

export function getUploadSizeError(relativePath: string | undefined, fileName: string, maxUploadFileSizeLabel: string): string {
  return `${relativePath || fileName} 超过 ${maxUploadFileSizeLabel} 上传限制`
}

export function normalizeFolderUploadRelativePath(relativePath: string): string | null {
  const normalized = relativePath.replace(/\\/g, '/')
  const segments = normalized.split('/')
  if (
    !normalized ||
    normalized.startsWith('/') ||
    normalized.includes('\0') ||
    segments.some(segment => segment === '' || segment === '.' || segment === '..')
  ) {
    return null
  }

  try {
    normalizePath(`/${normalized}`)
  } catch {
    return null
  }

  return normalized
}

export function normalizeUploadProgress(progress: number): number {
  if (!Number.isFinite(progress)) {
    return 0
  }
  return Math.min(100, Math.max(0, progress))
}

export function buildUploadQueue(files: File[], options: BuildUploadQueueOptions): UploadQueueItem[] {
  return files.map(file => {
    const rawFolderRelativePath = (file as File & { webkitRelativePath?: string }).webkitRelativePath || ''
    const folderRelativePath = rawFolderRelativePath
      ? normalizeFolderUploadRelativePath(rawFolderRelativePath)
      : undefined
    const relativePath = folderRelativePath ?? file.name
    const pathError = rawFolderRelativePath && !folderRelativePath
      ? INVALID_FOLDER_UPLOAD_PATH_MESSAGE
      : undefined
    const isOversized = file.size > options.maxUploadFileSizeBytes
    const isRejected = isOversized || Boolean(pathError)

    return {
      file,
      relativePath,
      folderRelativePath: folderRelativePath ?? undefined,
      progress: 0,
      status: isRejected ? 'error' : 'pending',
      error: isOversized ? getUploadSizeError(relativePath, file.name, options.maxUploadFileSizeLabel) : pathError,
    }
  })
}

export function getUploadQueueCounts(queue: UploadQueueItem[]): UploadQueueCounts {
  const counts = queue.reduce(
    (acc, item) => {
      if (item.status === 'done') {
        acc.done += 1
      } else if (item.status === 'error') {
        acc.error += 1
      } else if (item.status === 'cancelled') {
        acc.cancelled += 1
      }
      return acc
    },
    { total: queue.length, done: 0, error: 0, cancelled: 0, finished: 0 }
  )
  counts.finished = counts.done + counts.error + counts.cancelled
  return counts
}

export function getUploadPanelTitle(isUploading: boolean, counts: UploadQueueCounts): string {
  if (isUploading) {
    return `上传中 (${counts.finished}/${counts.total})`
  }
  if (counts.cancelled > 0 && counts.done === 0 && counts.error === 0) {
    return '上传已取消'
  }
  if (counts.error > 0 && counts.done === 0 && counts.cancelled === 0) {
    return '上传失败'
  }
  if (counts.error > 0 || counts.cancelled > 0) {
    return '上传部分完成'
  }
  return '上传完成'
}
