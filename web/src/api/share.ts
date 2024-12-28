import { authFetch } from './auth'

const API_BASE = '/api/v1'

// Share types
export type ShareType = 'file' | 'folder'
export type Permission = 'read' | 'read_write'

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
    let message = '获取分享列表失败'
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new ShareError(message, response.status)
  }
  
  return response.json()
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
    let message = '创建分享失败'
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new ShareError(message, response.status)
  }
  
  return response.json()
}

/**
 * Get share details
 */
export async function getShare(id: string): Promise<Share> {
  const response = await authFetch(`${API_BASE}/shares/${id}`)
  
  if (!response.ok) {
    let message = '获取分享详情失败'
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new ShareError(message, response.status)
  }
  
  return response.json()
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
    let message = '更新分享失败'
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new ShareError(message, response.status)
  }
  
  return response.json()
}

/**
 * Delete share
 */
export async function deleteShare(id: string): Promise<void> {
  const response = await authFetch(`${API_BASE}/shares/${id}`, {
    method: 'DELETE',
  })
  
  if (!response.ok) {
    let message = '删除分享失败'
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new ShareError(message, response.status)
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
      message = '分享已过期或已禁用'
    }
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new ShareError(message, response.status)
  }
  
  return response.json()
}

/**
 * Access password-protected share
 */
export async function accessShareWithPassword(id: string, password: string): Promise<PublicShareInfo> {
  const response = await fetch(`/s/${id}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ password }),
  })
  
  if (!response.ok) {
    let message = '访问失败'
    if (response.status === 401) {
      message = '密码错误'
    } else if (response.status === 410) {
      message = '分享已过期或已禁用'
    }
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new ShareError(message, response.status)
  }
  
  return response.json()
}

/**
 * Get download URL for shared file
 */
export function getShareDownloadUrl(id: string, password?: string): string {
  const url = `/s/${id}/download`
  if (password) {
    return `${url}?password=${encodeURIComponent(password)}`
  }
  return url
}

/**
 * Get download URL for file in shared folder
 */
export function getShareFileDownloadUrl(id: string, filePath: string, password?: string): string {
  const url = `/s/${id}/download/${encodeURIComponent(filePath)}`
  if (password) {
    return `${url}?password=${encodeURIComponent(password)}`
  }
  return url
}

// === Utility functions ===

/**
 * Copy share URL to clipboard
 */
export async function copyShareUrl(share: Share): Promise<void> {
  const url = share.url.startsWith('http') 
    ? share.url 
    : `${window.location.origin}${share.url}`
  await navigator.clipboard.writeText(url)
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
