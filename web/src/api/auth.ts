const API_BASE = '/api/v1'

import { normalizeUserHomeDir } from '@/lib/utils'

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

interface AuthSessionData {
  accessToken: string
  refreshToken: string
  user: User
}

export interface AuthActionResult {
  warning: boolean
  message?: string
}

export interface LoginActionResult extends AuthActionResult {
  user: User
}

export interface AuthClearedDetail {
  message?: string
  reason?: 'expired' | 'disabled' | 'missing'
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

function getMissingUserMessage(responseMessage?: string): string {
  return responseMessage || '账户不存在或已被删除，请重新登录'
}

function hasStoredAuthState(): boolean {
  return Boolean(getStoredToken() || getStoredRefreshToken() || localStorage.getItem(USER_KEY))
}

function isUserRole(role: unknown): role is User['role'] {
  return role === 'admin' || role === 'user' || role === 'guest'
}

function isValidAdminListUser(value: unknown): value is UserListResponse['users'][number] {
  if (!value || typeof value !== 'object') {
    return false
  }

  const user = value as Partial<UserListResponse['users'][number]>
  return (
    typeof user.id === 'string' &&
    typeof user.username === 'string' &&
    isUserRole(user.role) &&
    typeof user.disabled === 'boolean' &&
    typeof user.home_dir === 'string' &&
    typeof user.created_at === 'string' &&
    typeof user.updated_at === 'string' &&
    typeof user.quota_bytes === 'number' &&
    typeof user.used_bytes === 'number' &&
    (user.email === undefined || typeof user.email === 'string') &&
    (user.last_login_at === undefined || typeof user.last_login_at === 'string')
  )
}

function parseAuthSessionData(data: LoginResponse | RefreshResponse | undefined): AuthSessionData {
  if (!data || typeof data.access_token !== 'string' || typeof data.refresh_token !== 'string') {
    throw new Error('invalid auth session data')
  }

  return {
    accessToken: data.access_token,
    refreshToken: data.refresh_token,
    user: normalizeUser(data.user),
  }
}

function readAuthSuccessData<T>(body: AuthApiResponse<T> | undefined): T {
  if (!body || body.success !== true || body.data === undefined) {
    throw new Error('invalid auth response data')
  }

  return body.data
}

function getAuthActionResult<T>(response: Response, body: AuthApiResponse<T> | undefined): AuthActionResult {
  return {
    warning: response.headers?.get?.('Warning') != null,
    message: body?.message,
  }
}

async function handleUnauthorizedSessionResponse(response: Response): Promise<void> {
  if (response.status !== 401 || !hasStoredAuthState()) {
    return
  }

  let detail: AuthClearedDetail = {
    reason: 'expired',
    message: '登录已过期，请重新登录',
  }

  try {
    const bodySource = typeof response.clone === 'function' ? response.clone() : response
    const body: AuthApiResponse<never> = await bodySource.json()
    if (body.error?.code === 'USER_NOT_FOUND') {
      detail = {
        reason: 'missing',
        message: getMissingUserMessage(body.error.message),
      }
    }
  } catch {
    // Keep the generic expired-session detail when the error payload is invalid.
  }

  clearTokens(detail)
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
    localStorage.removeItem(USER_KEY)
    return null
  }
}

function normalizeUser(user: ApiUser): User {
  const homeDir = user.homeDir ?? user.home_dir

  if (
    typeof user.id !== 'string' ||
    typeof user.username !== 'string' ||
    !isUserRole(user.role) ||
    typeof homeDir !== 'string' ||
    homeDir.length === 0 ||
    (user.email !== undefined && typeof user.email !== 'string')
  ) {
    throw new Error('invalid user payload')
  }

  const normalizedHomeDir = normalizeUserHomeDir(homeDir)

  return {
    id: user.id,
    username: user.username,
    email: user.email,
    role: user.role,
    homeDir: normalizedHomeDir,
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

  await handleUnauthorizedSessionResponse(response)

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
      const data = parseAuthSessionData(readAuthSuccessData(body))
      storeTokens(data.accessToken, data.refreshToken, data.user)
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
export async function login(username: string, password: string): Promise<LoginActionResult> {
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
  
  let data: AuthSessionData
  let body: AuthApiResponse<LoginResponse> | undefined
  try {
    body = await response.json()
    data = parseAuthSessionData(readAuthSuccessData(body))
  } catch {
    throw new AuthError('登录响应无效', response.status)
  }

  storeTokens(data.accessToken, data.refreshToken, data.user)
  await syncDownloadSession()
  return {
    user: data.user,
    ...getAuthActionResult(response, body),
  }
}

// Logout
export async function logout(): Promise<AuthActionResult> {
  const token = getStoredToken()
  let result: AuthActionResult = { warning: false, message: undefined }

  if (token) {
    try {
      const response = await fetch(`${API_BASE}/auth/logout`, {
        method: 'POST',
        headers: getAuthHeaders(),
      })

      if (response.ok) {
        let body: AuthApiResponse<null> | undefined
        try {
          body = await response.json()
        } catch {
          body = undefined
        }
        result = getAuthActionResult(response, body)
      }
    } catch {
      // Ignore logout errors
    }
  }
  clearTokens()
  return result
}

// Get current user
export async function getCurrentUser(): Promise<User | null> {
  const token = getStoredToken()
  if (!token) return null
  
  const response = await authFetch(`${API_BASE}/auth/me`)
  
  if (!response.ok) {
    if (response.status === 401 || response.status === 403) {
      return null
    }

    let message = '获取当前用户失败'
    let code: string | undefined
    try {
      const body: AuthApiResponse<never> = await response.json()
      if (body.error?.message) {
        message = body.error.message
      }
      if (body.error?.code) {
        code = body.error.code
      }
    } catch {
      // Keep the generic error when the unavailable response body is invalid.
    }

    throw new AuthError(message, response.status, code)
  }

  let body: AuthApiResponse<{ user: ApiUser }>
  try {
    body = await response.json()
  } catch {
    clearTokens()
    return null
  }

  let data: { user: ApiUser }
  try {
    data = readAuthSuccessData(body)
  } catch {
    clearTokens()
    return null
  }

  let user: User
  try {
    user = normalizeUser(data.user)
  } catch {
    clearTokens()
    return null
  }

  await syncDownloadSession()
  return user
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

  try {
    const body: AuthApiResponse<null> = await response.json()
    readAuthSuccessData(body)
  } catch {
    throw new AuthError('修改密码响应无效', response.status)
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
  let data: UserListResponse
  try {
    data = readAuthSuccessData(body)
  } catch {
    throw new AuthError('获取用户列表响应无效', response.status)
  }

  if (!Array.isArray(data.users) || data.users.some((user) => !isValidAdminListUser(user))) {
    throw new AuthError('获取用户列表响应无效', response.status)
  }

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
  
  try {
    const body: AuthApiResponse<{ user: ApiUser }> = await response.json()
    const data = readAuthSuccessData(body)
    return normalizeUser(data.user)
  } catch {
    throw new AuthError('创建用户响应无效', response.status)
  }
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

  try {
    const body: AuthApiResponse<null> = await response.json()
    readAuthSuccessData(body)
  } catch {
    throw new AuthError('删除用户响应无效', response.status)
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

  try {
    const body: AuthApiResponse<null> = await response.json()
    readAuthSuccessData(body)
  } catch {
    throw new AuthError('重置密码响应无效', response.status)
  }
}
