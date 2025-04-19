// Activity log API client

import { authFetch } from './auth'

const API_BASE = '/api/v1'

// API error class
export class ApiError extends Error {
  status: number
  statusText: string
  code?: string

  constructor(message: string, status: number, statusText: string, code?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.statusText = statusText
    this.code = code
  }

  get isUnavailable(): boolean {
    return this.status === 503 || this.code === 'SERVICE_UNAVAILABLE'
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
async function handleResponse<T>(response: Response, errorMessage: string, invalidMessage = '服务器返回了无效的数据'): Promise<T> {
  if (!response.ok) {
    let message = errorMessage
    let code: string | undefined
    try {
      const body: unknown = await response.json()
      if (isRecord(body)) {
        const topLevelCode = typeof body.code === 'string' ? body.code : undefined
        if (typeof body.error === 'string') {
          message = body.error || errorMessage
        } else if (isRecord(body.error) && typeof body.error.message === 'string') {
          message = body.error.message
          if (typeof body.error.code === 'string') {
            code = body.error.code
          }
        } else if (typeof body.message === 'string' && body.message) {
          message = body.message
        }
        if (!code && topLevelCode) {
          code = topLevelCode
        }
      }
    } catch { /* ignore */ }
    throw new ApiError(message, response.status, response.statusText, code)
  }

  let body: unknown
  try {
    body = await response.json()
  } catch {
    throw new Error(invalidMessage)
  }

  if (!body || typeof body !== 'object' || (body as ApiResponseWrapper<T>).success !== true || !('data' in body)) {
    throw new Error(invalidMessage)
  }

  return body as T
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value)
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
  | 'favorite'
  | 'unfavorite'
  | 'favorite_note_update'
  | 'login'
  | 'logout'
  | 'trash_restore'
  | 'trash_delete'
  | 'trash_empty'

function isActionType(value: unknown): value is ActionType {
  return value === 'upload'
    || value === 'download'
    || value === 'delete'
    || value === 'rename'
    || value === 'move'
    || value === 'copy'
    || value === 'create'
    || value === 'restore'
    || value === 'share'
    || value === 'unshare'
    || value === 'favorite'
    || value === 'unfavorite'
    || value === 'favorite_note_update'
    || value === 'login'
    || value === 'logout'
    || value === 'trash_restore'
    || value === 'trash_delete'
    || value === 'trash_empty'
}

function isStringRecord(value: unknown): value is Record<string, string> {
  return isRecord(value) && Object.values(value).every((entry) => typeof entry === 'string')
}

function isNumberRecord(value: unknown): value is Record<string, number> {
  return isRecord(value) && Object.values(value).every((entry) => typeof entry === 'number')
}

function isValidActivityEntry(value: unknown): value is ActivityEntry {
  return isRecord(value)
    && typeof value.id === 'string'
    && typeof value.timestamp === 'string'
    && isActionType(value.action)
    && (value.path === undefined || typeof value.path === 'string')
    && (value.user === undefined || typeof value.user === 'string')
    && (value.ip === undefined || typeof value.ip === 'string')
    && (value.details === undefined || isStringRecord(value.details))
}

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

export interface ActivityActionResult {
  message?: string
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
    items?: ActivityEntry[]
    total?: number
    limit?: number
    offset?: number
  }>>(response, '获取活动日志失败')

  if (
    (result.data.items !== undefined && (!Array.isArray(result.data.items) || result.data.items.some((item) => !isValidActivityEntry(item))))
    || (result.data.total !== undefined && typeof result.data.total !== 'number')
    || (result.data.limit !== undefined && typeof result.data.limit !== 'number')
    || (result.data.offset !== undefined && typeof result.data.offset !== 'number')
  ) {
    throw new Error('服务器返回了无效的数据')
  }

  const items = Array.isArray(result.data.items) ? result.data.items : []
  const limit = result.data.limit ?? options?.limit ?? items.length
  const offset = result.data.offset ?? options?.offset ?? 0

  return {
    items,
    total: result.data.total ?? items.length,
    limit,
    offset,
  }
}

// Get activity statistics
export async function getActivityStats(): Promise<ActivityStats> {
  const response = await authFetch(`${API_BASE}/activity/stats`)
  const result = await handleResponse<ApiResponseWrapper<ActivityStats>>(response, '获取活动统计失败')

  if (
    typeof result.data.total !== 'number' ||
    typeof result.data.today !== 'number' ||
    !isNumberRecord(result.data.by_action) ||
    !isNumberRecord(result.data.by_user)
  ) {
    throw new Error('服务器返回了无效的数据')
  }

  return result.data
}

// Clear activity log (admin only)
export async function clearActivity(): Promise<ActivityActionResult> {
  const response = await authFetch(`${API_BASE}/activity/`, {
    method: 'DELETE',
  })
  if (!response.ok) {
    await handleResponse(response, '清除活动日志失败')
  }

  const result = await handleResponse<ApiResponseWrapper<{ message?: string }>>(response, '清除活动日志失败')

  if (result.data.message !== undefined && typeof result.data.message !== 'string') {
    throw new Error('服务器返回了无效的数据')
  }

  return {
    message: result.data.message,
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
    favorite: '添加收藏',
    unfavorite: '取消收藏',
    favorite_note_update: '更新收藏备注',
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
    favorite: 'primary',
    unfavorite: 'warning',
    favorite_note_update: 'primary',
    login: 'success',
    logout: 'default',
    trash_restore: 'success',
    trash_delete: 'danger',
    trash_empty: 'danger',
  }
  return colors[action] || 'default'
}
