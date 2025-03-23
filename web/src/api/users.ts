/**
 * User Management API
 * Admin-only endpoints for managing users
 */

import { authFetch } from './auth'

export interface User {
  id: string
  username: string
  email: string
  role: 'admin' | 'user' | 'guest'
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
}

export interface ResetPasswordRequest {
  new_password: string
}

export interface ListUsersResponse {
  success: boolean
  users: User[]
  total: number
}

export interface UserResponse {
  success: boolean
  user: User
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

function isValidUser(value: unknown): value is User {
  if (!value || typeof value !== 'object') {
    return false
  }

  const user = value as Partial<User>
  return (
    typeof user.id === 'string' &&
    typeof user.username === 'string' &&
    typeof user.email === 'string' &&
    isUserRole(user.role) &&
    typeof user.disabled === 'boolean' &&
    typeof user.home_dir === 'string' &&
    typeof user.created_at === 'string' &&
    typeof user.updated_at === 'string' &&
    (user.last_login_at === undefined || typeof user.last_login_at === 'string') &&
    typeof user.quota_bytes === 'number' &&
    typeof user.used_bytes === 'number'
  )
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

async function parseUsersError(response: Response, fallback: string): Promise<UsersError> {
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

/**
 * List all users (admin only)
 */
export async function listUsers(): Promise<ListUsersResponse> {
  const response = await authFetch(`${API_BASE}/`)
  
  if (!response.ok) {
    throw await parseUsersError(response, 'Failed to list users')
  }

  const body = await parseUsersSuccess<{ users?: User[]; total?: number }>(response, 'Invalid users response')
  if (
		!body.data ||
		(body.data.users !== undefined && (!Array.isArray(body.data.users) || body.data.users.some((user) => !isValidUser(user)))) ||
		(body.data.total !== undefined && typeof body.data.total !== 'number')
	) {
    throw new Error('Invalid users response')
  }

  const users = Array.isArray(body.data.users) ? body.data.users : []

  return {
    success: body.success,
    users,
    total: body.data.total ?? users.length,
  }
}

/**
 * Create a new user (admin only)
 */
export async function createUser(data: CreateUserRequest): Promise<UserResponse> {
  const response = await authFetch(`${API_BASE}/`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(data),
  })
  
  if (!response.ok) {
    throw await parseUsersError(response, 'Failed to create user')
  }

  const body = await parseUsersSuccess<{ user: User }>(response, 'Invalid create user response')
  if (!body.data || !isValidUser(body.data.user)) {
    throw new Error('Invalid create user response')
  }

  return {
    success: body.success,
    user: body.data.user,
  }
}

/**
 * Delete a user (admin only)
 */
export async function deleteUser(userId: string): Promise<{ success: boolean }> {
  const response = await authFetch(`${API_BASE}/${userId}`, {
    method: 'DELETE',
  })
  
  if (!response.ok) {
    throw await parseUsersError(response, 'Failed to delete user')
  }

  const body = await parseUsersSuccess<null>(response, 'Invalid delete user response')
  if (!('data' in body)) {
    throw new Error('Invalid delete user response')
  }
  return { success: body.success }
}

/**
 * Reset user password (admin only)
 */
export async function resetUserPassword(
  userId: string,
  data: ResetPasswordRequest
): Promise<{ success: boolean }> {
  const response = await authFetch(`${API_BASE}/${userId}/reset-password`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(data),
  })
  
  if (!response.ok) {
    throw await parseUsersError(response, 'Failed to reset password')
  }

  const body = await parseUsersSuccess<null>(response, 'Invalid reset password response')
  if (!('data' in body)) {
    throw new Error('Invalid reset password response')
  }
  return { success: body.success }
}

/**
 * Toggle user enabled/disabled status (admin only)
 */
export async function toggleUserStatus(
  userId: string,
  disabled: boolean
): Promise<{ success: boolean }> {
  const response = await authFetch(`${API_BASE}/${userId}/status`, {
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ disabled }),
  })
  
  if (!response.ok) {
    throw await parseUsersError(response, 'Failed to update user status')
  }

  const body = await parseUsersSuccess<{ disabled: boolean }>(response, 'Invalid update user status response')
  if (!body.data || typeof body.data.disabled !== 'boolean') {
	throw new Error('Invalid update user status response')
	}
  return { success: body.success }
}
