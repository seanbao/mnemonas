const API_BASE = '/api/v1'

export interface User {
  id: string
  username: string
  email?: string
  role: 'admin' | 'user' | 'guest'
  homeDir: string
}

interface ApiUser {
  id: string
  username: string
  email?: string
  role: 'admin' | 'user' | 'guest'
  home_dir?: string
  homeDir?: string
}

export interface LoginRequest {
  username: string
  password: string
}

export interface LoginResponse {
  access_token: string
  refresh_token: string
  expires_at: string
  token_type: string
  user: ApiUser
}

export interface RefreshResponse {
  access_token: string
  refresh_token: string
  expires_at: string
  token_type: string
  user: ApiUser
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

interface AuthApiError {
  code?: string
  message: string
}

interface AuthApiResponse<T> {
  success: boolean
  data?: T
  message?: string
  error?: AuthApiError
}

export interface AuthClearedDetail {
  message?: string
  reason?: 'expired' | 'disabled'
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
export const AUTH_CLEARED_EVENT = 'mnemonas:auth-cleared'
let refreshPromise: Promise<boolean> | null = null

function getSessionEndedMessage(responseMessage?: string): string {
  return responseMessage || '账户已被禁用，请联系管理员'
}

async function handleForbiddenSessionResponse(response: Response): Promise<void> {
  if (response.status !== 403) {
    return
  }

  try {
    const bodySource = typeof response.clone === 'function' ? response.clone() : response
    const body: AuthApiResponse<never> = await bodySource.json()
    if (body.error?.code === 'USER_DISABLED') {
      clearTokens({
        reason: 'disabled',
        message: getSessionEndedMessage(body.error.message),
      })
    }
  } catch {
    // Ignore invalid error payloads and let callers handle the response body.
  }
}

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
    return normalizeUser(JSON.parse(data) as ApiUser)
  } catch {
    return null
  }
}

function normalizeUser(user: ApiUser): User {
  return {
    id: user.id,
    username: user.username,
    email: user.email,
    role: user.role,
    homeDir: user.homeDir ?? user.home_dir ?? '/',
  }
}

export function storeTokens(accessToken: string, refreshToken: string, user: User): void {
  localStorage.setItem(TOKEN_KEY, accessToken)
  localStorage.setItem(REFRESH_TOKEN_KEY, refreshToken)
  localStorage.setItem(USER_KEY, JSON.stringify(user))
}

export function clearTokens(detail?: AuthClearedDetail): void {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(REFRESH_TOKEN_KEY)
  localStorage.removeItem(USER_KEY)

  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent<AuthClearedDetail>(AUTH_CLEARED_EVENT, { detail }))
  }
}

async function syncDownloadSession(): Promise<void> {
  const token = getStoredToken()
  if (!token) return

  try {
    await fetch(`${API_BASE}/auth/download-session`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${token}`,
      },
    })
  } catch {
    // Ignore download session sync failures; header-based fetch remains available.
  }
}

// Auth header helper
export function getAuthHeaders(): HeadersInit {
  const token = getStoredToken()
  if (!token) return {}
  return {
    'Authorization': `Bearer ${token}`,
  }
}

function mergeAuthHeaders(headers?: HeadersInit): Headers {
  const merged = new Headers(headers)
  const token = getStoredToken()
  if (token && !merged.has('Authorization')) {
    merged.set('Authorization', `Bearer ${token}`)
  }
  return merged
}

function getRequestPath(url: string): string {
  try {
    return new URL(url, 'http://localhost').pathname
  } catch {
    return url
  }
}

function shouldRefreshToken(url: string, retryCount: number): boolean {
  if (retryCount > 0) {
    return false
  }

  const pathname = getRequestPath(url)
  return pathname !== `${API_BASE}/auth/login` && pathname !== `${API_BASE}/auth/refresh`
}

// Fetch with auth
export async function authFetch(url: string, options: RequestInit = {}, retryCount = 0): Promise<Response> {
  const headers = mergeAuthHeaders(options.headers)
  
  const response = await fetch(url, { ...options, headers })
  
  // If unauthorized, try to refresh token
  if (response.status === 401 && shouldRefreshToken(url, retryCount)) {
    const refreshed = await tryRefreshToken()
    if (refreshed) {
      return authFetch(url, options, retryCount + 1)
    }
  }

  await handleForbiddenSessionResponse(response)
  
  return response
}

export async function refreshAuthSession(): Promise<boolean> {
  return tryRefreshToken()
}

// Try to refresh the token
async function tryRefreshToken(): Promise<boolean> {
  if (refreshPromise) {
    return refreshPromise
  }

  const refreshToken = getStoredRefreshToken()
  if (!refreshToken) return false

  refreshPromise = (async () => {
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

      const body: AuthApiResponse<RefreshResponse> = await response.json()
      if (!body.data) {
        clearTokens()
        return false
      }
      const data = body.data
      storeTokens(data.access_token, data.refresh_token, normalizeUser(data.user))
      await syncDownloadSession()
      return true
    } catch {
      clearTokens()
      return false
    } finally {
      refreshPromise = null
    }
  })()

  return refreshPromise
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
      const body: AuthApiResponse<never> = await response.json()
      if (body.error?.message) message = body.error.message
      if (body.error?.code) code = body.error.code
    } catch { /* ignore */ }
    throw new AuthError(message, response.status, code)
  }
  
  const body: AuthApiResponse<LoginResponse> = await response.json()
  if (!body.data) {
    throw new AuthError('登录响应无效', response.status)
  }
  const data = body.data
  const user = normalizeUser(data.user)
  storeTokens(data.access_token, data.refresh_token, user)
  await syncDownloadSession()
  return user
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
  
  const body: AuthApiResponse<{ user: ApiUser }> = await response.json()
  if (!body.data) {
    return null
  }
  await syncDownloadSession()
  return normalizeUser(body.data.user)
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
      const body: AuthApiResponse<never> = await response.json()
      if (body.error?.message) message = body.error.message
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
      const body: AuthApiResponse<never> = await response.json()
      if (body.error?.message) message = body.error.message
    } catch { /* ignore */ }
    throw new AuthError(message, response.status)
  }
  
  const body: AuthApiResponse<UserListResponse> = await response.json()
  if (!body.data) {
    throw new AuthError('获取用户列表响应无效', response.status)
  }
  const data = body.data
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
      const body: AuthApiResponse<never> = await response.json()
      if (body.error?.message) message = body.error.message
    } catch { /* ignore */ }
    throw new AuthError(message, response.status)
  }
  
  const body: AuthApiResponse<{ user: ApiUser }> = await response.json()
  if (!body.data) {
    throw new AuthError('创建用户响应无效', response.status)
  }
  return normalizeUser(body.data.user)
}

// Delete user (admin only)
export async function deleteUser(userId: string): Promise<void> {
  const response = await authFetch(`${API_BASE}/admin/users/${userId}`, {
    method: 'DELETE',
  })
  
  if (!response.ok) {
    let message = '删除用户失败'
    try {
      const body: AuthApiResponse<never> = await response.json()
      if (body.error?.message) message = body.error.message
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
      const body: AuthApiResponse<never> = await response.json()
      if (body.error?.message) message = body.error.message
    } catch { /* ignore */ }
    throw new AuthError(message, response.status)
  }
}
