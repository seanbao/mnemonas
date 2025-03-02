import type { FileItem } from '@/stores/files'
import { sanitizeFilename, normalizePath, encodePathForUrl } from '@/lib/utils'
import { authFetch, getStoredToken, refreshAuthSession } from './auth'

export type { FileItem }

const API_BASE = '/api/v1'

export interface FileListResponse {
  files: FileItem[]
  path: string
}

export interface VersionInfo {
  version: number
  hash: string
  size: number
  timestamp: string  // ISO 8601 format from backend
}

// Helper to convert backend version to display format
export function versionToDisplayFormat(v: VersionInfo): { modTime: string; size: number; hash: string } {
  return {
    modTime: v.timestamp,
    size: v.size,
    hash: v.hash,
  }
}

export interface StorageStats {
  totalSize?: number
  totalObjects?: number
  uniqueSize?: number
  dedupRatio?: number
}

export interface HealthStatus {
  status: string
  version: string
  uptime: string
  storage: {
    dataDir: string
    writable: boolean
  }
}

export interface DiagnosticsInfo {
  timestamp: string
  uptime: string
  uptimeSecs?: number
  version: {
    name: string
    version: string
    go: string
  }
  system?: {
    filesystemInitialized?: boolean
    dataplaneConnected?: boolean
    thumbnailServiceReady?: boolean
  }
  memory?: {
    allocMb?: number
    totalAllocMb?: number
    sysMb?: number
    numGc?: number
  }
  goroutines?: number
  filesystem?: {
    trashItems?: number
    trashSize?: number
  }
  storage?: {
    totalChunks?: number
    totalSize?: number
    uniqueSize?: number
    dedupRatio?: number
  }
  dataplane?: {
    healthy?: boolean
    version?: string
    uptimeSec?: number
  }
}

// API Error class for better error handling
export class ApiError extends Error {
  status: number
  statusText: string
  
  constructor(
    message: string,
    status: number,
    statusText: string
  ) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.statusText = statusText
  }
  
  get isNotFound(): boolean {
    return this.status === 404
  }
  
  get isUnauthorized(): boolean {
    return this.status === 401
  }
  
  get isForbidden(): boolean {
    return this.status === 403
  }
  
  get isServerError(): boolean {
    return this.status >= 500
  }
}

// Helper to handle API responses
async function handleResponse<T>(response: Response, errorPrefix: string): Promise<T> {
  if (!response.ok) {
    let message = errorPrefix
    try {
      const body = await response.json()
      if (typeof body.error === 'string') {
        message = body.error
      } else if (body.error?.message) {
        message = body.error.message
      } else if (body.message) {
        message = body.message
      }
    } catch {
      // Use status text if JSON parsing fails
      message = `${errorPrefix}: ${response.statusText}`
    }
    throw new ApiError(message, response.status, response.statusText)
  }
  
  try {
    return await response.json()
  } catch {
    throw new Error('服务器返回了无效的数据')
  }
}

async function handleWrappedResponse<T>(response: Response, errorPrefix: string): Promise<T> {
  const body = await handleResponse<ApiResponseWrapper<T>>(response, errorPrefix)
  if (
    !body ||
    typeof body !== 'object' ||
    body.success !== true ||
    !('data' in body)
  ) {
    throw new Error('服务器返回了无效的数据')
  }
  return body.data
}

async function throwApiErrorFromResponse(response: Response, fallback: string): Promise<never> {
  let message = fallback
  try {
    const body = await response.json() as { error?: string | { message?: string }; message?: string }
    if (typeof body.error === 'string') {
      message = body.error || fallback
    } else if (body.error?.message) {
      message = body.error.message
    } else if (body.message) {
      message = body.message
    }
  } catch {
    // Keep the fallback message when the body is missing or invalid.
  }

  throw new ApiError(message, response.status, response.statusText)
}

// API Response wrapper from backend
interface ApiResponseWrapper<T> {
  success: boolean
  data: T
  message?: string
  timestamp: string
}

// List files in a directory
export async function listFiles(path: string): Promise<FileListResponse> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const response = await authFetch(`${API_BASE}/files${encodedPath}`)
  return handleWrappedResponse<FileListResponse>(response, '获取文件列表失败')
}

// Get file versions
export async function getVersions(path: string): Promise<VersionInfo[]> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const response = await authFetch(`${API_BASE}/versions${encodedPath}`)
  const data = await handleWrappedResponse<{path: string, versions: VersionInfo[]}>(response, '获取版本历史失败')
  return data.versions
}

// Delete a file (soft delete)
export async function deleteFile(path: string): Promise<void> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const response = await authFetch(`${API_BASE}/files${encodedPath}`, {
    method: 'DELETE',
  })
  if (!response.ok) {
    await throwApiErrorFromResponse(response, '删除文件失败')
  }
}

// Get storage stats (direct response, not wrapped)
export async function getStorageStats(): Promise<StorageStats> {
  const response = await authFetch(`${API_BASE}/stats`)
  const data = await handleWrappedResponse<{
    total_size?: number
    total_chunks?: number
    unique_size?: number
    dedup_ratio?: number
  }>(response, '获取存储统计失败')
  return {
    totalSize: data.total_size,
    totalObjects: data.total_chunks,
    uniqueSize: data.unique_size,
    dedupRatio: data.dedup_ratio,
  }
}

// Get health status (direct response, not wrapped)
export async function getHealth(): Promise<HealthStatus> {
  const response = await fetch('/health')
  if (!response.ok) {
    throw new ApiError('获取健康状态失败', response.status, response.statusText)
  }

  let body: unknown
  try {
    body = await response.json()
  } catch {
    throw new Error('服务器返回了无效的数据')
  }

  if (
    !body ||
    typeof body !== 'object' ||
    typeof (body as HealthStatus).status !== 'string' ||
    typeof (body as HealthStatus).version !== 'string' ||
    typeof (body as HealthStatus).uptime !== 'string' ||
    !(body as HealthStatus).storage ||
    typeof (body as HealthStatus).storage.dataDir !== 'string' ||
    typeof (body as HealthStatus).storage.writable !== 'boolean'
  ) {
    throw new Error('服务器返回了无效的数据')
  }

  return body as HealthStatus
}

// Get diagnostics info (direct response, not wrapped)
export async function getDiagnostics(): Promise<DiagnosticsInfo> {
  const response = await authFetch(`${API_BASE}/diagnostics`)
  const data = await handleWrappedResponse<{
    timestamp: string
    uptime: string
    uptime_secs?: number
    version: DiagnosticsInfo['version']
    system?: {
      filesystem_initialized?: boolean
      dataplane_connected?: boolean
      thumbnail_service_ready?: boolean
    }
    memory?: {
      alloc_mb?: number
      total_alloc_mb?: number
      sys_mb?: number
      num_gc?: number
    }
    goroutines?: number
    filesystem?: {
      trash_items?: number
      trash_size?: number
    }
    storage?: {
      total_chunks?: number
      total_size?: number
      unique_size?: number
      dedup_ratio?: number
    }
    dataplane?: {
      healthy?: boolean
      version?: string
      uptime_sec?: number
    }
  }>(response, '获取诊断信息失败')
  return {
    timestamp: data.timestamp,
    uptime: data.uptime,
    uptimeSecs: data.uptime_secs,
    version: data.version,
    system: data.system ? {
      filesystemInitialized: data.system.filesystem_initialized,
      dataplaneConnected: data.system.dataplane_connected,
      thumbnailServiceReady: data.system.thumbnail_service_ready,
    } : undefined,
    memory: data.memory ? {
      allocMb: data.memory.alloc_mb,
      totalAllocMb: data.memory.total_alloc_mb,
      sysMb: data.memory.sys_mb,
      numGc: data.memory.num_gc,
    } : undefined,
    goroutines: data.goroutines,
    filesystem: data.filesystem ? {
      trashItems: data.filesystem.trash_items,
      trashSize: data.filesystem.trash_size,
    } : undefined,
    storage: data.storage ? {
      totalChunks: data.storage.total_chunks,
      totalSize: data.storage.total_size,
      uniqueSize: data.storage.unique_size,
      dedupRatio: data.storage.dedup_ratio,
    } : undefined,
    dataplane: data.dataplane ? {
      healthy: data.dataplane.healthy,
      version: data.dataplane.version,
      uptimeSec: data.dataplane.uptime_sec,
    } : undefined,
  }
}

// Create directory
export async function createDirectory(path: string): Promise<void> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const response = await authFetch(`${API_BASE}/directories${encodedPath}`, {
    method: 'POST',
  })
  if (!response.ok) {
    await throwApiErrorFromResponse(response, '创建文件夹失败')
  }
}

// Upload file
export async function uploadFile(
  path: string,
  file: File,
  onProgress?: (progress: number) => void
): Promise<void> {
  // Sanitize filename to prevent path traversal
  const safeFilename = sanitizeFilename(file.name)
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const encodedFilename = encodeURIComponent(safeFilename)
  
  const url = `${API_BASE}/files${encodedPath}/${encodedFilename}`

  const sendUpload = (retryCount: number): Promise<void> => new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    const token = getStoredToken()

    xhr.upload.addEventListener('progress', (e) => {
      if (e.lengthComputable && onProgress) {
        onProgress((e.loaded / e.total) * 100)
      }
    })

    xhr.addEventListener('load', async () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve()
        return
      }

      if (xhr.status === 401 && retryCount === 0) {
        const refreshed = await refreshAuthSession()
        if (refreshed) {
          try {
            await sendUpload(retryCount + 1)
            resolve()
          } catch (error) {
            reject(error)
          }
          return
        }
      }

      reject(new ApiError('上传失败', xhr.status, xhr.statusText))
    })

    xhr.addEventListener('error', () => {
      reject(new Error('网络错误，上传失败'))
    })

    xhr.addEventListener('timeout', () => {
      reject(new Error('上传超时'))
    })

    // Use REST API instead of WebDAV to avoid Basic Auth popup
    xhr.open('POST', url)
    if (token) {
      xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    }
    xhr.send(file)
  })

  return sendUpload(0)
}

// Download file URL
export function getDownloadUrl(path?: string): string {
  return buildDownloadUrl(path)
}

function getFilenameFromContentDisposition(contentDisposition: string | null, fallback: string): string {
  if (!contentDisposition) {
    return fallback
  }

  const utf8Match = contentDisposition.match(/filename\*=UTF-8''([^;]+)/i)
  if (utf8Match?.[1]) {
    try {
      return decodeURIComponent(utf8Match[1])
    } catch {
      return utf8Match[1]
    }
  }

  const filenameMatch = contentDisposition.match(/filename="?([^";]+)"?/i)
  return filenameMatch?.[1] ?? fallback
}

function triggerBrowserDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  URL.revokeObjectURL(url)
}

export function buildDownloadUrl(
  path?: string,
  options?: { version?: string; download?: boolean }
): string {
  if (!path) return ''
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const params = new URLSearchParams()
  if (options?.version) {
    params.set('version', options.version)
  }
  if (options?.download) {
    params.set('download', 'true')
  }
  const query = params.toString()
  return query ? `${API_BASE}/download${encodedPath}?${query}` : `${API_BASE}/download${encodedPath}`
}

export async function downloadFile(
  path: string,
  options?: { version?: string; download?: boolean; filename?: string }
): Promise<void> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const params = new URLSearchParams()
  if (options?.version) {
    params.set('version', options.version)
  }
  if (options?.download !== false) {
    params.set('download', 'true')
  }

  const query = params.toString()
  const url = query ? `${API_BASE}/download${encodedPath}?${query}` : `${API_BASE}/download${encodedPath}`
  const response = await authFetch(url)

  if (!response.ok) {
    throw new ApiError('下载文件失败', response.status, response.statusText)
  }

  const fallbackFilename = options?.filename ?? normalizedPath.split('/').filter(Boolean).pop() ?? 'download'
  const filename = getFilenameFromContentDisposition(response.headers.get('Content-Disposition'), fallbackFilename)
  const blob = await response.blob()
  triggerBrowserDownload(blob, filename)
}

// Thumbnail URL
export type ThumbnailSize = 'small' | 'medium' | 'large'

export function getThumbnailUrl(path?: string, size: ThumbnailSize = 'medium'): string {
  if (!path) return ''
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  return `${API_BASE}/thumbnails${encodedPath}?size=${size}`
}

// Rename/Move file
export async function moveFile(fromPath: string, toPath: string): Promise<void> {
  const normalizedFrom = normalizePath(fromPath)
  const normalizedTo = normalizePath(toPath)
  
  const response = await authFetch(`${API_BASE}/files-move`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      from: normalizedFrom,
      to: normalizedTo,
    }),
  })
  if (!response.ok) {
    await throwApiErrorFromResponse(response, '移动文件失败')
  }
}

// Copy file
export async function copyFile(fromPath: string, toPath: string): Promise<void> {
  const normalizedFrom = normalizePath(fromPath)
  const normalizedTo = normalizePath(toPath)
  
  const response = await authFetch(`${API_BASE}/files-copy`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      from: normalizedFrom,
      to: normalizedTo,
    }),
  })
  if (!response.ok) {
    await throwApiErrorFromResponse(response, '复制文件失败')
  }
}

// Restore file to a specific version
export async function restoreVersion(path: string, hash: string): Promise<void> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodeURIComponent(normalizedPath)
  const response = await authFetch(`${API_BASE}/versions/${hash}/restore?path=${encodedPath}`, {
    method: 'POST',
  })
  if (!response.ok) {
    await throwApiErrorFromResponse(response, '恢复版本失败')
  }
}

// === Trash/Recycle Bin API ===

export interface TrashItem {
  id: string
  originalPath: string
  deletedAt: string  // ISO 8601 format
  name: string
  isDir: boolean
  size: number
  hash?: string
  versions?: number
}

export interface EmptyTrashResult {
  deletedCount: number
  partial: boolean
}

export interface TrashListResponse {
  items: TrashItem[]
  count: number
  totalSize: number
  retentionDays?: number
  retentionEnabled?: boolean
  retentionMaxSize?: number
}

// List trash items
export async function listTrash(): Promise<TrashListResponse> {
  const response = await authFetch(`${API_BASE}/trash/`)
  const data = await handleWrappedResponse<{
    items?: Array<{
      id: string
      originalPath: string
      deletedAt: string
      name: string
      isDir: boolean
      size: number
      hash?: string
      hadVersions?: boolean
    }>
    count?: number
    totalSize?: number
    retentionDays?: number
    retentionEnabled?: boolean
    retentionMaxSize?: number
  }>(response, '获取回收站列表失败')

  const items = Array.isArray(data.items)
    ? data.items.map(item => ({
      id: item.id,
      originalPath: item.originalPath,
      deletedAt: item.deletedAt,
      name: item.name,
      isDir: item.isDir,
      size: item.size,
      hash: item.hash,
      versions: item.hadVersions ? 1 : 0,
    }))
    : []
  
  return {
    items,
    count: data.count ?? items.length,
    totalSize: data.totalSize ?? items.reduce((sum, item) => sum + item.size, 0),
    retentionDays: data.retentionDays,
    retentionEnabled: data.retentionEnabled,
    retentionMaxSize: data.retentionMaxSize,
  }
}

// Restore item from trash
export async function restoreFromTrash(id: string, newPath?: string): Promise<void> {
  const url = newPath 
    ? `${API_BASE}/trash/${id}/restore?path=${encodeURIComponent(newPath)}`
    : `${API_BASE}/trash/${id}/restore`
  
  const response = await authFetch(url, {
    method: 'POST',
  })
  if (!response.ok) {
    await throwApiErrorFromResponse(response, '恢复文件失败')
  }
}

// Permanently delete item from trash
export async function deleteFromTrash(id: string): Promise<void> {
  const response = await authFetch(`${API_BASE}/trash/${id}`, {
    method: 'DELETE',
  })
  if (!response.ok) {
    await throwApiErrorFromResponse(response, '永久删除失败')
  }
}

// Empty trash (delete all items permanently)
export async function emptyTrash(): Promise<EmptyTrashResult> {
  const response = await authFetch(`${API_BASE}/trash/`, {
    method: 'DELETE',
  })
  const data = await handleWrappedResponse<{deleted_count: number, partial?: boolean}>(response, '清空回收站失败')
  return {
    deletedCount: data.deleted_count,
    partial: !!data.partial,
  }
}

// === Maintenance / Scrub APIs ===

export interface ScrubError {
  hash: string
  error_type: string
  message: string
}

export interface ScrubResult {
  has_result: boolean
  message?: string
  id?: string
  start_time?: string
  end_time?: string
  status?: 'running' | 'completed' | 'failed'
  total_objects?: number
  valid_objects?: number
  corrupted_objects?: number
  missing_objects?: number
  total_size?: number
  duration_ms?: number
  errors?: ScrubError[]
  error_message?: string
}

// Get last scrub result
export async function getScrubResult(): Promise<ScrubResult> {
  const response = await authFetch(`${API_BASE}/maintenance/scrub`)
  return handleWrappedResponse<ScrubResult>(response, '获取校验结果失败')
}

// Run scrub operation
export async function runScrub(hashes?: string[]): Promise<ScrubResult> {
  const response = await authFetch(`${API_BASE}/maintenance/scrub`, {
    method: 'POST',
    headers: hashes?.length ? { 'Content-Type': 'application/json' } : {},
    body: hashes?.length ? JSON.stringify({ hashes }) : undefined,
  })
  const data = await handleWrappedResponse<Omit<ScrubResult, 'has_result'>>(response, '执行数据校验失败')
  return {
    has_result: true,
    ...data,
  }
}

// Download diagnostics export
export async function downloadDiagnosticsExport(): Promise<void> {
  const response = await authFetch(`${API_BASE}/diagnostics-export`)
  if (!response.ok) {
    throw new ApiError('导出诊断信息失败', response.status, response.statusText)
  }
  
  // Get filename from Content-Disposition header or generate one
  const contentDisposition = response.headers.get('Content-Disposition')
  let filename = `mnemonas-diagnostics-${new Date().toISOString().slice(0, 10)}.json`
  if (contentDisposition) {
    const match = contentDisposition.match(/filename=(.+)/)
    if (match) filename = match[1]
  }
  
  const blob = await response.blob()
  triggerBrowserDownload(blob, filename)
}
