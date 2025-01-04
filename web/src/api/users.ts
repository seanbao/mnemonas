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

const API_BASE = '/api/v1/admin/users'

/**
 * List all users (admin only)
 */
export async function listUsers(): Promise<ListUsersResponse> {
  const response = await authFetch(`${API_BASE}/`)
  
  if (!response.ok) {
    const error = await response.json()
    throw new Error(error.error || 'Failed to list users')
  }
  
  return response.json()
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
    const error = await response.json()
    throw new Error(error.error || 'Failed to create user')
  }
  
  return response.json()
}

/**
 * Delete a user (admin only)
 */
export async function deleteUser(userId: string): Promise<{ success: boolean }> {
  const response = await authFetch(`${API_BASE}/${userId}`, {
    method: 'DELETE',
  })
  
  if (!response.ok) {
    const error = await response.json()
    throw new Error(error.error || 'Failed to delete user')
  }
  
  return response.json()
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
    const error = await response.json()
    throw new Error(error.error || 'Failed to reset password')
  }
  
  return response.json()
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
    const error = await response.json()
    throw new Error(error.error || 'Failed to update user status')
  }
  
  return response.json()
}
