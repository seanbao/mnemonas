import { authFetch } from './auth'
import { copyTextToClipboard, encodePathForUrl, normalizePath } from '@/lib/utils'

const API_BASE = '/api/v1'

// Share types
export type ShareType = 'file' | 'folder'
export type Permission = 'read'

export interface Share {
  id: string
  path: string
  type: ShareType
  created_by: string
  created_at: string
  expires_at?: string
  has_password: boolean
  permission: Permission
  enabled: boolean
  access_count: number
  max_access?: number
  description?: string
  url: string
}

export interface CreateShareRequest {
  path: string
  type?: ShareType
  expires_in?: string  // e.g., "7d", "24h", "30m"
  password?: string
  permission?: Permission
  max_access?: number
  description?: string
}

export interface UpdateShareRequest {
  enabled?: boolean
  expires_in?: string
  password?: string
  permission?: Permission
  max_access?: number
  description?: string
}

export interface PublicShareInfo {
  id: string
  type: ShareType
  has_password: boolean
  permission: Permission
  description?: string
  file_name?: string
  file_size?: number
  folder_items?: number
}

export interface PublicShareItem {
  name: string
  path: string
  is_dir: boolean
  size: number
  mod_time: string
}

export interface PublicShareItemsResponse {
  path: string
  items: PublicShareItem[]
}

export class ShareError extends Error {
  status: number
  code?: string
  
  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = 'ShareError'
    this.status = status
    this.code = code
  }
  
  get isNotFound(): boolean {
    return this.status === 404
  }
  
  get isExpired(): boolean {
    return this.status === 410
  }
  
  get isUnauthorized(): boolean {
    return this.status === 401
  }

  get isRateLimited(): boolean {
    return this.status === 429
  }

  get isFeatureDisabled(): boolean {
    return this.code === 'SHARE_FEATURE_DISABLED'
  }
}

interface ShareApiError {
  code?: string
  message: string
}

interface ShareApiResponse<T> {
  success: boolean
  data?: T
  message?: string
  error?: ShareApiError | string
}

function isShareType(value: unknown): value is ShareType {
  return value === 'file' || value === 'folder'
}

function isPermission(value: unknown): value is Permission {
  return value === 'read'
}

function isValidShare(value: unknown): value is Share {
  if (!value || typeof value !== 'object') {
    return false
  }

  const share = value as Partial<Share>
  return (
    typeof share.id === 'string' &&
    typeof share.path === 'string' &&
    isShareType(share.type) &&
    typeof share.created_by === 'string' &&
    typeof share.created_at === 'string' &&
    typeof share.has_password === 'boolean' &&
    isPermission(share.permission) &&
    typeof share.enabled === 'boolean' &&
    typeof share.access_count === 'number' &&
    typeof share.url === 'string' &&
    (share.expires_at === undefined || typeof share.expires_at === 'string') &&
    (share.max_access === undefined || typeof share.max_access === 'number') &&
    (share.description === undefined || typeof share.description === 'string')
  )
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

function getShareErrorMessage(body: ShareApiResponse<never> | { error?: string; message?: string }, fallback: string): string {
  if (typeof body.error === 'string' && body.error) {
    return body.error
  }
  if (body.error && typeof body.error === 'object' && 'message' in body.error && body.error.message) {
    return body.error.message
  }
  if (body.message) {
    return body.message
  }
  return fallback
}

function getShareErrorCode(body: ShareApiResponse<never> | { error?: string; message?: string }): string | undefined {
  if (body.error && typeof body.error === 'object' && 'code' in body.error && typeof body.error.code === 'string') {
    return body.error.code
  }

  return undefined
}

async function readShareApiError(response: Response, fallback: string): Promise<ShareError> {
  let message = fallback
  let code: string | undefined

  try {
    const body = await response.json() as ShareApiResponse<never> | { error?: string; message?: string }
    message = getShareErrorMessage(body, message)
    code = getShareErrorCode(body)
  } catch {
    // Keep the fallback when the response body is unavailable.
  }

  return new ShareError(message, response.status, code)
}

async function parseWrappedShareSuccess<T>(response: Response, invalidMessage: string): Promise<T> {
  let body: unknown
  try {
    body = await response.json()
  } catch {
    throw new ShareError(invalidMessage, response.status)
  }

  if (
    !body ||
    typeof body !== 'object' ||
    (body as ShareApiResponse<unknown>).success !== true ||
    !('data' in body)
  ) {
    throw new ShareError(invalidMessage, response.status)
  }

  return body as T
}

async function parsePublicShareSuccess<T>(response: Response, invalidMessage: string): Promise<T> {
  let body: unknown
  try {
    body = await response.json()
  } catch {
    throw new ShareError(invalidMessage, response.status)
  }

  if (!body || typeof body !== 'object') {
    throw new ShareError(invalidMessage, response.status)
  }

  return body as T
}

// === Authenticated Share APIs ===

/**
 * List shares for current user
 * @param all - If true and user is admin, list all shares
 */
export async function listShares(all = false): Promise<Share[]> {
  const url = all ? `${API_BASE}/shares?all=true` : `${API_BASE}/shares`
  const response = await authFetch(url)
  
  if (!response.ok) {
    throw await readShareApiError(response, '获取分享列表失败')
  }

  const body = await parseWrappedShareSuccess<ShareApiResponse<unknown>>(response, '获取分享列表响应无效')
  if (!Array.isArray(body.data) || !body.data.every(isValidShare)) {
    throw new ShareError('获取分享列表响应无效', response.status)
  }
  return body.data
}

/**
 * Create a new share
 */
export async function createShare(req: CreateShareRequest): Promise<Share> {
  const response = await authFetch(`${API_BASE}/shares`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  
  if (!response.ok) {
    throw await readShareApiError(response, '创建分享失败')
  }

  const body = await parseWrappedShareSuccess<ShareApiResponse<unknown>>(response, '创建分享响应无效')
  if (!isValidShare(body.data)) {
    throw new ShareError('创建分享响应无效', response.status)
  }
  return body.data
}

/**
 * Get share details
 */
export async function getShare(id: string): Promise<Share> {
  const response = await authFetch(`${API_BASE}/shares/${id}`)
  
  if (!response.ok) {
    throw await readShareApiError(response, '获取分享详情失败')
  }

  const body = await parseWrappedShareSuccess<ShareApiResponse<unknown>>(response, '获取分享详情响应无效')
  if (!isValidShare(body.data)) {
    throw new ShareError('获取分享详情响应无效', response.status)
  }
  return body.data
}

/**
 * Update share
 */
export async function updateShare(id: string, req: UpdateShareRequest): Promise<Share> {
  const response = await authFetch(`${API_BASE}/shares/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  
  if (!response.ok) {
    throw await readShareApiError(response, '更新分享失败')
  }

  const body = await parseWrappedShareSuccess<ShareApiResponse<unknown>>(response, '更新分享响应无效')
  if (!isValidShare(body.data)) {
    throw new ShareError('更新分享响应无效', response.status)
  }
  return body.data
}

/**
 * Delete share
 */
export async function deleteShare(id: string): Promise<void> {
  const response = await authFetch(`${API_BASE}/shares/${id}`, {
    method: 'DELETE',
  })
  
  if (!response.ok) {
    throw await readShareApiError(response, '删除分享失败')
  }
}

// === Public Share APIs ===

/**
 * Get public share info (no auth required)
 */
export async function getPublicShare(id: string): Promise<PublicShareInfo> {
  const response = await fetch(`/s/${id}`)
  
  if (!response.ok) {
    let message = '分享不存在或已失效'
    if (response.status === 410) {
      message = '分享已过期、已禁用或访问次数已达上限'
    }
    try {
      const body = await response.json() as ShareApiResponse<never> | { error?: string; message?: string }
      message = getShareErrorMessage(body, message)
    } catch { /* ignore */ }
    throw new ShareError(message, response.status)
  }
  
  return parsePublicShareSuccess<PublicShareInfo>(response, '分享信息无效')
}

/**
 * List items in a public shared folder
 */
export async function getPublicShareItems(
  id: string,
  options?: { path?: string }
): Promise<PublicShareItemsResponse> {
  const params = new URLSearchParams()
  if (options?.path) {
    params.set('path', options.path)
  }
  const query = params.toString()
  const url = query ? `/s/${id}/items?${query}` : `/s/${id}/items`
  const response = await fetch(url)

  if (!response.ok) {
    let message = '获取分享文件夹失败'
    let code: string | undefined
    if (response.status === 410) {
      message = '分享已过期、已禁用或访问次数已达上限'
    } else if (response.status === 401) {
      message = '密码错误'
    } else if (response.status === 429) {
      message = '尝试次数过多，请稍后再试'
    }
    try {
      const body = await response.json() as ShareApiResponse<never> | { error?: string; message?: string }
      message = getShareErrorMessage(body, message)
      code = getShareErrorCode(body)
    } catch { /* ignore */ }
    throw new ShareError(message, response.status, code)
  }

  const body = await parsePublicShareSuccess<PublicShareItemsResponse>(response, '分享文件夹响应无效')
  if (!Array.isArray(body.items)) {
    throw new ShareError('分享文件夹响应无效', response.status)
  }
  return body
}

/**
 * Access password-protected share
 */
export async function accessShareWithPassword(id: string, password: string): Promise<PublicShareInfo> {
  const response = await fetch(`/s/${id}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ password }),
    credentials: 'same-origin',
  })
  
  if (!response.ok) {
    let message = '访问失败'
    let code: string | undefined
    if (response.status === 401) {
      message = '密码错误'
    } else if (response.status === 410) {
      message = '分享已过期、已禁用或访问次数已达上限'
    } else if (response.status === 429) {
      message = '尝试次数过多，请稍后再试'
    }
    try {
      const body = await response.json() as ShareApiResponse<never> | { error?: string; message?: string }
      message = getShareErrorMessage(body, message)
      code = getShareErrorCode(body)
    } catch { /* ignore */ }
    throw new ShareError(message, response.status, code)
  }
  
  return parsePublicShareSuccess<PublicShareInfo>(response, '分享信息无效')
}

/**
 * Get download URL for shared file
 */
export function getShareDownloadUrl(id: string): string {
  return `/s/${id}/download`
}

/**
 * Get download URL for file in shared folder
 */
export function getShareFileDownloadUrl(id: string, filePath: string): string {
  const normalizedPath = normalizePath(filePath)
  const encodedPath = encodePathForUrl(normalizedPath)
  const trimmedPath = encodedPath.startsWith('/') ? encodedPath.slice(1) : encodedPath
  return `/s/${id}/download/${trimmedPath}`
}

export async function downloadShare(id: string, options?: { filePath?: string; filename?: string }): Promise<void> {
  const url = options?.filePath ? getShareFileDownloadUrl(id, options.filePath) : getShareDownloadUrl(id)
  const response = await fetch(url, { credentials: 'same-origin' })

  if (!response.ok) {
    let message = '下载分享文件失败'
    let code: string | undefined
    if (response.status === 401) {
      message = '访问凭证已失效，请重新输入密码'
    } else if (response.status === 410) {
      message = '分享已过期、已禁用或访问次数已达上限'
    } else if (response.status === 429) {
      message = '尝试次数过多，请稍后再试'
    }
    try {
      const body = await response.json() as ShareApiResponse<never> | { error?: string; message?: string }
      message = getShareErrorMessage(body, message)
      code = getShareErrorCode(body)
    } catch { /* ignore */ }
    throw new ShareError(message, response.status, code)
  }

  const fallbackFilename = options?.filename
    ?? (options?.filePath ? normalizePath(options.filePath).split('/').filter(Boolean).pop() : undefined)
    ?? 'download'
  const filename = getFilenameFromContentDisposition(response.headers.get('Content-Disposition'), fallbackFilename)
  const blob = await response.blob()
  triggerBrowserDownload(blob, filename)
}

// === Utility functions ===

/**
 * Copy share URL to clipboard
 */
export async function copyShareUrl(share: Share): Promise<void> {
  const url = share.url.startsWith('http') 
    ? share.url 
    : `${window.location.origin}${share.url}`
  await copyTextToClipboard(url)
}

/**
 * Format expiration time
 */
export function formatExpiration(expiresAt?: string): string {
  if (!expiresAt) return '永不过期'
  
  const expires = new Date(expiresAt)
  const now = new Date()
  const diff = expires.getTime() - now.getTime()
  
  if (diff < 0) return '已过期'
  
  const days = Math.floor(diff / (1000 * 60 * 60 * 24))
  const hours = Math.floor((diff % (1000 * 60 * 60 * 24)) / (1000 * 60 * 60))
  
  if (days > 0) return `${days} 天后过期`
  if (hours > 0) return `${hours} 小时后过期`
  return '即将过期'
}

/**
 * Parse duration string to display
 */
export function formatDuration(duration: string): string {
  if (duration.endsWith('d')) {
    const days = parseInt(duration)
    return `${days} 天`
  }
  if (duration.endsWith('h')) {
    const hours = parseInt(duration)
    return `${hours} 小时`
  }
  if (duration.endsWith('m')) {
    const mins = parseInt(duration)
    return `${mins} 分钟`
  }
  return duration
}
