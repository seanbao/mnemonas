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

const API_BASE = '/api/v1/admin/users'

async function parseUsersError(response: Response, fallback: string): Promise<Error> {
  try {
    const body = await response.json() as UsersApiResponse<never>
    return new Error(body.error?.message || body.message || fallback)
  } catch {
    return new Error(fallback)
  }
}

/**
 * List all users (admin only)
 */
export async function listUsers(): Promise<ListUsersResponse> {
  const response = await authFetch(`${API_BASE}/`)
  
  if (!response.ok) {
    throw await parseUsersError(response, 'Failed to list users')
  }

  const body = await response.json() as UsersApiResponse<{ users: User[]; total: number }>
  if (!body.data) {
    throw new Error('Invalid users response')
  }

  return {
    success: body.success,
    users: body.data.users,
    total: body.data.total,
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

  const body = await response.json() as UsersApiResponse<{ user: User }>
  if (!body.data) {
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

  const body = await response.json() as UsersApiResponse<null>
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

  const body = await response.json() as UsersApiResponse<null>
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

  const body = await response.json() as UsersApiResponse<{ disabled: boolean }>
  return { success: body.success }
}
