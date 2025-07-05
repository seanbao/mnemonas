/**
 * User Management API
 * Admin-only endpoints for managing users
 */

import { authFetch } from './auth'
import { INVALID_API_RESPONSE_MESSAGE as INVALID_USERS_RESPONSE_MESSAGE } from '@/lib/apiMessages'
import { readStructuredJsonErrorDetails } from '@/lib/jsonErrorResponse'
import { normalizeUserHomeDir } from '@/lib/utils'

export interface User {
  id: string
  username: string
  email: string
  role: 'admin' | 'user' | 'guest'
  groups?: string[]
  disabled: boolean
  home_dir: string
  created_at: string
  updated_at: string
  last_login_at?: string
  quota_bytes: number
  used_bytes: number
}

export interface CreateUserRequest {
  username: string
  password: string
  email?: string
  role?: 'admin' | 'user' | 'guest'
  groups?: string[]
  home_dir?: string
  quota_bytes?: number
}

export interface ResetPasswordRequest {
  new_password: string
}

export interface UpdateUserRequest {
  email?: string
  role?: User['role']
  groups?: string[]
  home_dir?: string
  quota_bytes?: number
}

export interface ListUsersResponse {
  success: boolean
  users: User[]
  total: number
}

interface UsersActionResult {
  success: boolean
  warning?: boolean
  message?: string
}

export interface UsersRequestOptions {
  signal?: AbortSignal
}

export interface UserResponse {
  success: boolean
  user: User
  warning?: boolean
  message?: string
}

type ApiUser = Omit<User, 'groups'> & {
  groups?: string[] | null
}

interface UsersApiError {
  code?: string
  message: string
}

interface UsersApiResponse<T> {
  success: boolean
  data?: T
  message?: string
  error?: UsersApiError
}

function isUserRole(value: unknown): value is User['role'] {
  return value === 'admin' || value === 'user' || value === 'guest'
}

function isNonNegativeSafeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}

function isCanonicalNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.trim() === value && value.length > 0
}

function isValidUserHomeDir(value: unknown): value is string {
  if (typeof value !== 'string') {
    return false
  }

  try {
    return normalizeUserHomeDir(value) === value
  } catch {
    return false
  }
}

function isValidUser(value: unknown): value is ApiUser {
  if (!value || typeof value !== 'object') {
    return false
  }

  const user = value as Partial<ApiUser>
  return (
    isCanonicalNonEmptyString(user.id) &&
    isCanonicalNonEmptyString(user.username) &&
    typeof user.email === 'string' &&
    isUserRole(user.role) &&
    (user.groups == null || (Array.isArray(user.groups) && user.groups.every((group) => typeof group === 'string'))) &&
    typeof user.disabled === 'boolean' &&
    isValidUserHomeDir(user.home_dir) &&
    typeof user.created_at === 'string' &&
    typeof user.updated_at === 'string' &&
    (user.last_login_at === undefined || typeof user.last_login_at === 'string') &&
    isNonNegativeSafeInteger(user.quota_bytes) &&
    isNonNegativeSafeInteger(user.used_bytes)
  )
}

function normalizeUser(user: ApiUser): User {
  const { groups, ...rest } = user
  if (groups == null) {
    return rest
  }

  return {
    ...rest,
    groups,
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object'
}

function hasUsersWarning(response: Response, data: unknown): boolean {
  const warningHeader = typeof response.headers?.get === 'function'
    ? response.headers.get('Warning')
    : null

  return warningHeader != null || (isRecord(data) && data.warning === true)
}

export class UsersError extends Error {
  status: number
  code?: string

  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = 'UsersError'
    this.status = status
    this.code = code
  }

  get isUnavailable(): boolean {
    return this.status === 503 || this.code === 'SERVICE_UNAVAILABLE'
  }
}

const API_BASE = '/api/v1/admin/users'
const USERS_ERROR_MESSAGES = {
  list: '获取用户列表失败',
  create: '创建用户失败',
  update: '更新用户失败',
  delete: '删除用户失败',
  resetPassword: '重置密码失败',
  revokeSessions: '撤销用户会话失败',
  updateStatus: '更新用户状态失败',
} as const

function userManagementUrl(userId: string): string {
  return `${API_BASE}/${encodeURIComponent(userId)}`
}

async function parseUsersError(response: Response, fallback: string): Promise<UsersError> {
  const structuredError = await readStructuredJsonErrorDetails(response, fallback)
  if (structuredError) {
    return new UsersError(structuredError.message, response.status, structuredError.code)
  }

  try {
    const body = await response.json() as UsersApiResponse<never>
    return new UsersError(body.error?.message || body.message || fallback, response.status, body.error?.code)
  } catch {
    return new UsersError(fallback, response.status)
  }
}

async function parseUsersSuccess<T>(response: Response, invalidMessage: string): Promise<UsersApiResponse<T>> {
  let body: UsersApiResponse<T>
  try {
    body = await response.json() as UsersApiResponse<T>
  } catch {
    throw new Error(invalidMessage)
  }

  if (!body || body.success !== true) {
    throw new Error(invalidMessage)
  }

  return body
}

function validateUserQuotaRequest(quotaBytes: unknown): void {
  if (quotaBytes !== undefined && !isNonNegativeSafeInteger(quotaBytes)) {
    throw new UsersError('用户配额必须是 0 或不超过安全范围的整数', 0, 'INVALID_QUOTA')
  }
}

function normalizeUserHomeDirRequest<T extends { home_dir?: string }>(data: T): T {
  if (data.home_dir === undefined) {
    return data
  }

  try {
    return {
      ...data,
      home_dir: normalizeUserHomeDir(data.home_dir),
    }
  } catch {
    throw new UsersError('用户主目录路径无效', 0, 'INVALID_HOME_DIR')
  }
}

/**
 * List all users (admin only)
 */
export async function listUsers(options: UsersRequestOptions = {}): Promise<ListUsersResponse> {
  const response = await authFetch(`${API_BASE}/`, {
    signal: options.signal,
  })
  
  if (!response.ok) {
    throw await parseUsersError(response, USERS_ERROR_MESSAGES.list)
  }

  const body = await parseUsersSuccess<{ users?: User[]; total?: number }>(response, INVALID_USERS_RESPONSE_MESSAGE)
  if (
    !body.data ||
    !Array.isArray(body.data.users) ||
    body.data.users.some((user) => !isValidUser(user)) ||
    (body.data.total !== undefined && !isNonNegativeSafeInteger(body.data.total)) ||
    (body.data.total !== undefined && body.data.total < body.data.users.length)
  ) {
    throw new Error(INVALID_USERS_RESPONSE_MESSAGE)
  }

  const users = body.data.users.map(normalizeUser)

  return {
    success: body.success,
    users,
    total: body.data.total ?? users.length,
  }
}

/**
 * Create a new user (admin only)
 */
export async function createUser(data: CreateUserRequest, options: UsersRequestOptions = {}): Promise<UserResponse> {
  validateUserQuotaRequest(data.quota_bytes)
  const request = normalizeUserHomeDirRequest(data)

  const response = await authFetch(`${API_BASE}/`, {
    ...options,
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(request),
  })
  
  if (!response.ok) {
    throw await parseUsersError(response, USERS_ERROR_MESSAGES.create)
  }

  const body = await parseUsersSuccess<{ user: User }>(response, INVALID_USERS_RESPONSE_MESSAGE)
  if (!body.data || !isValidUser(body.data.user)) {
    throw new Error(INVALID_USERS_RESPONSE_MESSAGE)
  }

  return {
    success: body.success,
    user: normalizeUser(body.data.user),
    warning: hasUsersWarning(response, body.data),
    message: body.message,
  }
}

/**
 * Update user metadata, role, home directory, or quota (admin only)
 */
export async function updateUser(userId: string, data: UpdateUserRequest, options: UsersRequestOptions = {}): Promise<UserResponse> {
  validateUserQuotaRequest(data.quota_bytes)
  const request = normalizeUserHomeDirRequest(data)

  const response = await authFetch(userManagementUrl(userId), {
    ...options,
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(request),
  })

  if (!response.ok) {
    throw await parseUsersError(response, USERS_ERROR_MESSAGES.update)
  }

  const body = await parseUsersSuccess<{ user: User }>(response, INVALID_USERS_RESPONSE_MESSAGE)
  if (!body.data || !isValidUser(body.data.user)) {
    throw new Error(INVALID_USERS_RESPONSE_MESSAGE)
  }

  return {
    success: body.success,
    user: normalizeUser(body.data.user),
    warning: hasUsersWarning(response, body.data),
    message: body.message,
  }
}

/**
 * Delete a user (admin only)
 */
export async function deleteUser(userId: string, options: UsersRequestOptions = {}): Promise<UsersActionResult> {
  const response = await authFetch(userManagementUrl(userId), {
    ...options,
    method: 'DELETE',
  })
  
  if (!response.ok) {
    throw await parseUsersError(response, USERS_ERROR_MESSAGES.delete)
  }

  const body = await parseUsersSuccess<null>(response, INVALID_USERS_RESPONSE_MESSAGE)
  if (!('data' in body)) {
    throw new Error(INVALID_USERS_RESPONSE_MESSAGE)
  }
  return {
    success: body.success,
    warning: hasUsersWarning(response, body.data),
    message: body.message,
  }
}

/**
 * Reset user password (admin only)
 */
export async function resetUserPassword(
  userId: string,
  data: ResetPasswordRequest,
  options: UsersRequestOptions = {}
): Promise<UsersActionResult> {
  const response = await authFetch(`${userManagementUrl(userId)}/reset-password`, {
    ...options,
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(data),
  })
  
  if (!response.ok) {
    throw await parseUsersError(response, USERS_ERROR_MESSAGES.resetPassword)
  }

  const body = await parseUsersSuccess<null>(response, INVALID_USERS_RESPONSE_MESSAGE)
  if (!('data' in body)) {
    throw new Error(INVALID_USERS_RESPONSE_MESSAGE)
  }
  return {
    success: body.success,
    warning: hasUsersWarning(response, body.data),
    message: body.message,
  }
}

/**
 * Revoke a user's active sessions (admin only)
 */
export async function revokeUserSessions(userId: string, options: UsersRequestOptions = {}): Promise<UsersActionResult> {
  const response = await authFetch(`${userManagementUrl(userId)}/revoke-sessions`, {
    ...options,
    method: 'POST',
  })

  if (!response.ok) {
    throw await parseUsersError(response, USERS_ERROR_MESSAGES.revokeSessions)
  }

  const body = await parseUsersSuccess<{ revoked: boolean; warning?: boolean }>(response, INVALID_USERS_RESPONSE_MESSAGE)
  if (!body.data || typeof body.data.revoked !== 'boolean') {
    throw new Error(INVALID_USERS_RESPONSE_MESSAGE)
  }
  return {
    success: body.success,
    warning: hasUsersWarning(response, body.data),
    message: body.message,
  }
}

/**
 * Toggle user enabled/disabled status (admin only)
 */
export async function toggleUserStatus(
  userId: string,
  disabled: boolean,
  options: UsersRequestOptions = {}
): Promise<UsersActionResult> {
  const response = await authFetch(`${userManagementUrl(userId)}/status`, {
    ...options,
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ disabled }),
  })
  
  if (!response.ok) {
    throw await parseUsersError(response, USERS_ERROR_MESSAGES.updateStatus)
  }

  const body = await parseUsersSuccess<{ disabled: boolean }>(response, INVALID_USERS_RESPONSE_MESSAGE)
  if (!body.data || typeof body.data.disabled !== 'boolean') {
    throw new Error(INVALID_USERS_RESPONSE_MESSAGE)
  }
  return {
    success: body.success,
    warning: hasUsersWarning(response, body.data),
    message: body.message,
  }
}
