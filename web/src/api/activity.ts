// Activity log API client

import { authFetch } from './auth'

const API_BASE = '/api/v1'

// API error class
export class ApiError extends Error {
  status: number
  statusText: string

  constructor(message: string, status: number, statusText: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.statusText = statusText
  }
}

// API response wrapper
interface ApiResponseWrapper<T> {
  success: boolean
  data: T
  message?: string
  error?: {
    code?: string
    message: string
  }
}

// Handle API response
async function handleResponse<T>(response: Response, errorMessage: string): Promise<T> {
  if (!response.ok) {
    let message = errorMessage
    try {
      const body = await response.json() as ApiResponseWrapper<never> | { error?: string; message?: string }
      if (typeof (body as { error?: string }).error === 'string') {
        message = (body as { error?: string }).error || errorMessage
      } else if ('error' in body && body.error?.message) {
        message = body.error.message
      } else if (body.message) {
        message = body.message
      }
    } catch { /* ignore */ }
    throw new ApiError(message, response.status, response.statusText)
  }
  return response.json()
}

// Activity action types
export type ActionType =
  | 'upload'
  | 'download'
  | 'delete'
  | 'rename'
  | 'move'
  | 'copy'
  | 'create'
  | 'restore'
  | 'share'
  | 'unshare'
  | 'login'
  | 'logout'
  | 'trash_restore'
  | 'trash_delete'
  | 'trash_empty'

// Activity entry
export interface ActivityEntry {
  id: string
  timestamp: string
  action: ActionType
  path?: string
  user?: string
  ip?: string
  details?: Record<string, string>
}

// Activity list response
export interface ActivityListResponse {
  items: ActivityEntry[]
  total: number
  limit: number
  offset: number
}

// Activity statistics
export interface ActivityStats {
  total: number
  today: number
  by_action: Record<ActionType, number>
  by_user: Record<string, number>
}

// List activity entries
export async function listActivity(options?: {
  limit?: number
  offset?: number
  action?: ActionType
  user?: string
}): Promise<ActivityListResponse> {
  const params = new URLSearchParams()
  if (options?.limit) params.set('limit', String(options.limit))
  if (options?.offset) params.set('offset', String(options.offset))
  if (options?.action) params.set('action', options.action)
  if (options?.user) params.set('user', options.user)

  const queryString = params.toString()
  const url = queryString ? `${API_BASE}/activity/?${queryString}` : `${API_BASE}/activity/`

  const response = await authFetch(url)
  const result = await handleResponse<ApiResponseWrapper<{
    items: ActivityEntry[]
    total: number
    limit: number
    offset: number
  }>>(response, '获取活动日志失败')

  return {
    items: result.data.items || [],
    total: result.data.total,
    limit: result.data.limit,
    offset: result.data.offset,
  }
}

// Get activity statistics
export async function getActivityStats(): Promise<ActivityStats> {
  const response = await authFetch(`${API_BASE}/activity/stats`)
  const result = await handleResponse<ApiResponseWrapper<ActivityStats>>(response, '获取活动统计失败')
  return result.data
}

// Clear activity log (admin only)
export async function clearActivity(): Promise<void> {
  const response = await authFetch(`${API_BASE}/activity/`, {
    method: 'DELETE',
  })
  if (!response.ok) {
    await handleResponse(response, '清除活动日志失败')
  }
}

// Get action label in Chinese
export function getActionLabel(action: ActionType): string {
  const labels: Record<ActionType, string> = {
    upload: '上传文件',
    download: '下载文件',
    delete: '删除文件',
    rename: '重命名',
    move: '移动文件',
    copy: '复制文件',
    create: '创建文件夹',
    restore: '恢复版本',
    share: '创建分享',
    unshare: '取消分享',
    login: '登录',
    logout: '登出',
    trash_restore: '从回收站恢复',
    trash_delete: '从回收站删除',
    trash_empty: '清空回收站',
  }
  return labels[action] || action
}

// Get action color for UI
export function getActionColor(action: ActionType): 'default' | 'primary' | 'success' | 'warning' | 'danger' {
  const colors: Record<ActionType, 'default' | 'primary' | 'success' | 'warning' | 'danger'> = {
    upload: 'success',
    download: 'primary',
    delete: 'danger',
    rename: 'warning',
    move: 'warning',
    copy: 'primary',
    create: 'success',
    restore: 'success',
    share: 'primary',
    unshare: 'warning',
    login: 'success',
    logout: 'default',
    trash_restore: 'success',
    trash_delete: 'danger',
    trash_empty: 'danger',
  }
  return colors[action] || 'default'
}
