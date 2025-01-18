const API_BASE = '/api/v1'

export interface User {
  id: string
  username: string
  email?: string
  role: 'admin' | 'user' | 'guest'
  homeDir: string
}

export interface LoginRequest {
  username: string
  password: string
}

export interface LoginResponse {
  success: boolean
  access_token: string
  refresh_token: string
  expires_at: string
  token_type: string
  user: User
}

export interface RefreshResponse {
  success: boolean
  access_token: string
  refresh_token: string
  expires_at: string
  token_type: string
  user: User
}

export interface ChangePasswordRequest {
  old_password: string
  new_password: string
}

export interface CreateUserRequest {
  username: string
  password: string
  email?: string
  role?: string
}

export interface UserListResponse {
  success: boolean
  users: Array<{
    id: string
    username: string
    email?: string
    role: string
    disabled: boolean
    home_dir: string
    created_at: string
    updated_at: string
    quota_bytes: number
    used_bytes: number
    last_login_at?: string
  }>
  total: number
}

export class AuthError extends Error {
  status: number
  code?: string
  
  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = 'AuthError'
    this.status = status
    this.code = code
  }
  
  get isUnauthorized(): boolean {
    return this.status === 401
  }
  
  get isForbidden(): boolean {
    return this.status === 403
  }
}

// Token storage
const TOKEN_KEY = 'mnemonas_token'
const REFRESH_TOKEN_KEY = 'mnemonas_refresh_token'
const USER_KEY = 'mnemonas_user'

export function getStoredToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function getStoredRefreshToken(): string | null {
  return localStorage.getItem(REFRESH_TOKEN_KEY)
}

export function getStoredUser(): User | null {
  const data = localStorage.getItem(USER_KEY)
  if (!data) return null
  try {
    return JSON.parse(data)
  } catch {
    return null
  }
}

export function storeTokens(accessToken: string, refreshToken: string, user: User): void {
  localStorage.setItem(TOKEN_KEY, accessToken)
  localStorage.setItem(REFRESH_TOKEN_KEY, refreshToken)
  localStorage.setItem(USER_KEY, JSON.stringify(user))
}

export function clearTokens(): void {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(REFRESH_TOKEN_KEY)
  localStorage.removeItem(USER_KEY)
}

// Auth header helper
export function getAuthHeaders(): HeadersInit {
  const token = getStoredToken()
  if (!token) return {}
  return {
    'Authorization': `Bearer ${token}`,
  }
}

// Fetch with auth
export async function authFetch(url: string, options: RequestInit = {}): Promise<Response> {
  const headers = {
    ...getAuthHeaders(),
    ...options.headers,
  }
  
  const response = await fetch(url, { ...options, headers })
  
  // If unauthorized, try to refresh token
  if (response.status === 401) {
    const refreshed = await tryRefreshToken()
    if (refreshed) {
      // Retry with new token
      const newHeaders = {
        ...getAuthHeaders(),
        ...options.headers,
      }
      return fetch(url, { ...options, headers: newHeaders })
    }
  }
  
  return response
}

// Try to refresh the token
async function tryRefreshToken(): Promise<boolean> {
  const refreshToken = getStoredRefreshToken()
  if (!refreshToken) return false
  
  try {
    const response = await fetch(`${API_BASE}/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    })
    
    if (!response.ok) {
      clearTokens()
      return false
    }
    
    const data: RefreshResponse = await response.json()
    storeTokens(data.access_token, data.refresh_token, data.user)
    return true
  } catch {
    clearTokens()
    return false
  }
}

// Login
export async function login(username: string, password: string): Promise<User> {
  const response = await fetch(`${API_BASE}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  
  if (!response.ok) {
    let message = '登录失败'
    let code: string | undefined
    try {
      const body = await response.json()
      if (body.error) message = body.error
      if (body.code) code = body.code
    } catch { /* ignore */ }
    throw new AuthError(message, response.status, code)
  }
  
  const data: LoginResponse = await response.json()
  storeTokens(data.access_token, data.refresh_token, data.user)
  return data.user
}

// Logout
export async function logout(): Promise<void> {
  const token = getStoredToken()
  if (token) {
    try {
      await fetch(`${API_BASE}/auth/logout`, {
        method: 'POST',
        headers: getAuthHeaders(),
      })
    } catch {
      // Ignore logout errors
    }
  }
  clearTokens()
}

// Get current user
export async function getCurrentUser(): Promise<User | null> {
  const token = getStoredToken()
  if (!token) return null
  
  const response = await authFetch(`${API_BASE}/auth/me`)
  
  if (!response.ok) {
    if (response.status === 401) {
      clearTokens()
    }
    return null
  }
  
  const data = await response.json()
  return data.user
}

// Change password
export async function changePassword(oldPassword: string, newPassword: string): Promise<void> {
  const response = await authFetch(`${API_BASE}/auth/password`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      old_password: oldPassword,
      new_password: newPassword,
    }),
  })
  
  if (!response.ok) {
    let message = '修改密码失败'
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new AuthError(message, response.status)
  }
}

// === Admin APIs ===

// List users (admin only)
export async function listUsers(): Promise<UserListResponse['users']> {
  const response = await authFetch(`${API_BASE}/admin/users`)
  
  if (!response.ok) {
    let message = '获取用户列表失败'
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new AuthError(message, response.status)
  }
  
  const data: UserListResponse = await response.json()
  return data.users
}

// Create user (admin only)
export async function createUser(req: CreateUserRequest): Promise<User> {
  const response = await authFetch(`${API_BASE}/admin/users`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  
  if (!response.ok) {
    let message = '创建用户失败'
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new AuthError(message, response.status)
  }
  
  const data = await response.json()
  return data.user
}

// Delete user (admin only)
export async function deleteUser(userId: string): Promise<void> {
  const response = await authFetch(`${API_BASE}/admin/users/${userId}`, {
    method: 'DELETE',
  })
  
  if (!response.ok) {
    let message = '删除用户失败'
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new AuthError(message, response.status)
  }
}

// Reset user password (admin only)
export async function resetUserPassword(userId: string, newPassword: string): Promise<void> {
  const response = await authFetch(`${API_BASE}/admin/users/${userId}/reset-password`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ new_password: newPassword }),
  })
  
  if (!response.ok) {
    let message = '重置密码失败'
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new AuthError(message, response.status)
  }
}
