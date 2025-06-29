// Activity log API client

import { authFetch } from './auth'
import { INVALID_API_RESPONSE_MESSAGE } from '@/lib/apiMessages'
import { readStructuredJsonErrorDetails } from '@/lib/jsonErrorResponse'

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
async function handleResponse<T>(response: Response, errorMessage: string, invalidMessage = INVALID_API_RESPONSE_MESSAGE): Promise<T> {
  if (!response.ok) {
    const structuredError = await readStructuredJsonErrorDetails(response, errorMessage)
    if (structuredError) {
      throw new ApiError(structuredError.message, response.status, response.statusText, structuredError.code)
    }

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
export const ACTIVITY_ACTIONS = [
  'upload',
  'download',
  'delete',
  'rename',
  'move',
  'copy',
  'create',
  'restore',
  'share',
  'unshare',
  'favorite',
  'unfavorite',
  'favorite_note_update',
  'login',
  'logout',
  'trash_restore',
  'trash_delete',
  'trash_empty',
  'disk_health',
  'scrub',
] as const

export type ActionType = typeof ACTIVITY_ACTIONS[number]

export const ACTIVITY_ACTION_GROUPS = [
  'risk',
  'share',
] as const

export type ActivityActionGroup = typeof ACTIVITY_ACTION_GROUPS[number]

const activityActionSet = new Set<string>(ACTIVITY_ACTIONS)

function isActionType(value: unknown): value is ActionType {
  return typeof value === 'string' && activityActionSet.has(value)
}

function isStringRecord(value: unknown): value is Record<string, string> {
  return isRecord(value) && Object.values(value).every((entry) => typeof entry === 'string')
}

function isNonNegativeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value >= 0
}

function isNonNegativeIntegerRecord(value: unknown): value is Record<string, number> {
  return isRecord(value) && Object.values(value).every(isNonNegativeInteger)
}

type ActivityActionCountMap = Partial<Record<ActionType, number>>

function isActivityActionNumberRecord(value: unknown): value is ActivityActionCountMap {
  return isRecord(value)
    && Object.entries(value).every(([key, entry]) => isActionType(key) && isNonNegativeInteger(entry))
}

function parseRFC3339Timestamp(value: unknown): number | null {
  if (typeof value !== 'string') {
    return null
  }
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(value)) {
    return null
  }
  const timestamp = Date.parse(value)
  return Number.isNaN(timestamp) ? null : timestamp
}

function isValidActivityRiskWindow(value: Record<string, unknown>): boolean {
  const startedAt = value.max_10m_started_at
  const endedAt = value.max_10m_ended_at

  if (value.max_10m === 0) {
    return startedAt === undefined && endedAt === undefined
  }

  const startedTimestamp = parseRFC3339Timestamp(startedAt)
  const endedTimestamp = parseRFC3339Timestamp(endedAt)
  return startedTimestamp !== null && endedTimestamp !== null && endedTimestamp >= startedTimestamp
}

function isValidActivityRiskSummary(value: unknown): value is ActivityRiskSummary {
  return isRecord(value)
    && isNonNegativeInteger(value.total)
    && isNonNegativeInteger(value.today)
    && isNonNegativeInteger(value.max_10m)
    && isValidActivityRiskWindow(value)
}

function isValidActivityEntry(value: unknown): value is ActivityEntry {
  return isRecord(value)
    && typeof value.id === 'string'
    && value.id.trim() !== ''
    && value.id === value.id.trim()
    && parseRFC3339Timestamp(value.timestamp) !== null
    && isActionType(value.action)
    && (value.path === undefined || typeof value.path === 'string')
    && (value.user === undefined || typeof value.user === 'string')
    && (value.ip === undefined || typeof value.ip === 'string')
    && (value.details === undefined || isStringRecord(value.details))
}

function isValidActivityListPagination(items: ActivityEntry[], total: number, limit: number, offset: number): boolean {
  if (items.length > limit) {
    return false
  }
  if (total < items.length) {
    return false
  }
  if (items.length > 0 && offset + items.length > total) {
    return false
  }
  return true
}

function hasUniqueActivityEntryIDs(items: ActivityEntry[]): boolean {
  const seen = new Set<string>()
  for (const item of items) {
    if (seen.has(item.id)) {
      return false
    }
    seen.add(item.id)
  }
  return true
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
export interface ActivityRiskSummary {
  total: number
  today: number
  max_10m: number
  max_10m_started_at?: string
  max_10m_ended_at?: string
}

export interface ActivityStats {
  total: number
  today: number
  by_action: ActivityActionCountMap
  by_user: Record<string, number>
  risk_summary?: ActivityRiskSummary
}

export interface ActivityActionResult {
  message?: string
}

export interface ActivityRequestOptions {
  signal?: AbortSignal
}

export interface ActivityFilterOptions extends ActivityRequestOptions {
  action?: ActionType
  actionGroup?: ActivityActionGroup
  user?: string
  path?: string
  since?: string
  until?: string
}

export interface ActivityListOptions extends ActivityFilterOptions {
  limit?: number
  offset?: number
}

function appendActivityFilterParams(params: URLSearchParams, options?: ActivityFilterOptions) {
  if (options?.action) params.set('action', options.action)
  if (options?.actionGroup) params.set('action_group', options.actionGroup)
  if (options?.user) params.set('user', options.user)
  if (options?.path) params.set('path', options.path)
  if (options?.since) params.set('since', options.since)
  if (options?.until) params.set('until', options.until)
}

function buildActivityUrl(path: string, params: URLSearchParams): string {
  const queryString = params.toString()
  return queryString ? `${API_BASE}${path}?${queryString}` : `${API_BASE}${path}`
}

// List activity entries
export async function listActivity(options?: ActivityListOptions): Promise<ActivityListResponse> {
  const params = new URLSearchParams()
  if (options?.limit) params.set('limit', String(options.limit))
  if (options?.offset) params.set('offset', String(options.offset))
  appendActivityFilterParams(params, options)

  const url = buildActivityUrl('/activity/', params)

  const response = options?.signal ? await authFetch(url, { signal: options.signal }) : await authFetch(url)
  const result = await handleResponse<ApiResponseWrapper<{
    items?: ActivityEntry[]
    total?: number
    limit?: number
    offset?: number
  }>>(response, '获取最近操作失败')

  if (
    (result.data.items !== undefined && (!Array.isArray(result.data.items) || result.data.items.some((item) => !isValidActivityEntry(item))))
    || (result.data.total !== undefined && !isNonNegativeInteger(result.data.total))
    || (result.data.limit !== undefined && !isNonNegativeInteger(result.data.limit))
    || (result.data.offset !== undefined && !isNonNegativeInteger(result.data.offset))
  ) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }

  const items = Array.isArray(result.data.items) ? result.data.items : []
  const limit = result.data.limit ?? options?.limit ?? items.length
  const offset = result.data.offset ?? options?.offset ?? 0
  const total = result.data.total ?? offset + items.length

  if (!isValidActivityListPagination(items, total, limit, offset)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  if (!hasUniqueActivityEntryIDs(items)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }

  return {
    items,
    total,
    limit,
    offset,
  }
}

// Get activity statistics
export async function getActivityStats(options?: ActivityFilterOptions): Promise<ActivityStats> {
  const params = new URLSearchParams()
  appendActivityFilterParams(params, options)
  const url = buildActivityUrl('/activity/stats', params)

  const response = options?.signal
    ? await authFetch(url, { signal: options.signal })
    : await authFetch(url)
  const result = await handleResponse<ApiResponseWrapper<ActivityStats>>(response, '获取活动统计失败')

  if (
    !isNonNegativeInteger(result.data.total) ||
    !isNonNegativeInteger(result.data.today) ||
    !isActivityActionNumberRecord(result.data.by_action) ||
    !isNonNegativeIntegerRecord(result.data.by_user) ||
    (result.data.risk_summary !== undefined && !isValidActivityRiskSummary(result.data.risk_summary))
  ) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }

  return result.data
}

// Clear activity log (admin only)
export async function clearActivity(options: ActivityRequestOptions = {}): Promise<ActivityActionResult> {
  const response = await authFetch(`${API_BASE}/activity/`, {
    ...options,
    method: 'DELETE',
  })
  if (!response.ok) {
    await handleResponse(response, '清除最近操作失败')
  }

  const result = await handleResponse<ApiResponseWrapper<{ message?: string }>>(response, '清除最近操作失败')

  if (result.data.message !== undefined && typeof result.data.message !== 'string') {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
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
    disk_health: '磁盘健康异常',
    scrub: '数据校验',
  }
  return labels[action] ?? '未知操作'
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
    disk_health: 'warning',
    scrub: 'warning',
  }
  return colors[action] || 'default'
}
