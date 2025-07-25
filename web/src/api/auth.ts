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
  access_token?: string
  refresh_token?: string
  expires_at?: string
  token_type?: string
  user: ApiUser
}

export interface RefreshResponse {
  access_token?: string
  refresh_token?: string
  expires_at?: string
  token_type?: string
  user: ApiUser
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

// Browser sessions use HttpOnly cookies. These legacy keys are only kept so
// upgrades can remove old bearer tokens from localStorage.
const TOKEN_KEY = 'mnemonas_token'
const REFRESH_TOKEN_KEY = 'mnemonas_refresh_token'
const USER_KEY = 'mnemonas_user'
const SESSION_MARKER_KEY = 'mnemonas_session'
const COOKIE_SESSION_HEADER = 'X-MnemoNAS-Session-Mode'
const COOKIE_SESSION_VALUE = 'cookie'
export const AUTH_CLEARED_EVENT = 'mnemonas:auth-cleared'
let refreshPromise: Promise<boolean> | null = null
let isDownloadSessionReady = true

function getSessionEndedMessage(responseMessage?: string): string {
  return responseMessage || '账户已被禁用，请联系管理员'
}

function getMissingUserMessage(responseMessage?: string): string {
  return responseMessage || '账户不存在或已被删除，请重新登录'
}

function getMissingBrowserSessionMessage(): string {
  return '登录会话未建立，请重新登录'
}

function hasStoredAuthState(): boolean {
  return hasBrowserSessionState() || hasLegacyTokenState()
}

function hasBrowserSessionState(): boolean {
  return Boolean(
    localStorage.getItem(SESSION_MARKER_KEY) ||
    localStorage.getItem(USER_KEY)
  )
}

function hasLegacyTokenState(): boolean {
  return Boolean(
    localStorage.getItem(TOKEN_KEY) ||
    localStorage.getItem(REFRESH_TOKEN_KEY)
  )
}

function isUserRole(role: unknown): role is User['role'] {
  return role === 'admin' || role === 'user' || role === 'guest'
}

function parseAuthSessionData(data: LoginResponse | RefreshResponse | undefined): AuthSessionData {
  if (!data || data.user == null) {
    throw new Error('invalid auth session data')
  }

  return {
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

async function readAuthApiError(response: Response): Promise<AuthApiError | undefined> {
  try {
    const bodySource = typeof response.clone === 'function' ? response.clone() : response
    const body: AuthApiResponse<never> = await bodySource.json()
    return body.error
  } catch {
    return undefined
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
    const error = await readAuthApiError(response)
    if (error?.code === 'USER_NOT_FOUND') {
      detail = {
        reason: 'missing',
        message: getMissingUserMessage(error.message),
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
    const error = await readAuthApiError(response)
    if (error?.code === 'USER_DISABLED') {
      clearTokens({
        reason: 'disabled',
        message: getSessionEndedMessage(error.message),
      })
    }
  } catch {
    // Ignore invalid error payloads and let callers handle the response body.
  }
}

export function getStoredToken(): string | null {
  clearLegacyTokenStorage()
  return null
}

export function getStoredRefreshToken(): string | null {
  clearLegacyTokenStorage()
  return null
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
  void accessToken
  void refreshToken
  storeSessionUser(user)
}

function clearLegacyTokenStorage(): void {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(REFRESH_TOKEN_KEY)
}

function storeSessionUser(user: User): void {
  clearLegacyTokenStorage()
  localStorage.setItem(SESSION_MARKER_KEY, '1')
  localStorage.setItem(USER_KEY, JSON.stringify(user))
}

export function clearTokens(detail?: AuthClearedDetail): void {
  clearLegacyTokenStorage()
  localStorage.removeItem(USER_KEY)
  localStorage.removeItem(SESSION_MARKER_KEY)
  isDownloadSessionReady = true

  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent<AuthClearedDetail>(AUTH_CLEARED_EVENT, { detail }))
  }
}

function getDownloadSessionSyncMessage(responseMessage?: string): string {
  return responseMessage || '原始预览和下载会话同步失败，请稍后重试'
}

interface DownloadSessionResult {
  ok: boolean
  message?: string
  authCleared?: boolean
  status?: number
  code?: string
}

async function syncDownloadSession(): Promise<DownloadSessionResult> {
  const hadBrowserSessionState = hasBrowserSessionState()
  clearLegacyTokenStorage()
  if (!hadBrowserSessionState) {
    isDownloadSessionReady = true
    return { ok: true }
  }

  try {
    const response = await fetch(`${API_BASE}/auth/download-session`, {
      method: 'POST',
      credentials: 'same-origin',
    })

    if (!response.ok) {
      let message = getDownloadSessionSyncMessage()
      const error = await readAuthApiError(response)

      if (response.status === 401) {
        message = error?.code === 'USER_NOT_FOUND'
          ? getMissingUserMessage(error.message)
          : error?.code === 'MISSING_AUTH_HEADER' || error?.code === 'NOT_AUTHENTICATED'
            ? getMissingBrowserSessionMessage()
            : '登录已过期，请重新登录'
        clearTokens({
          reason: error?.code === 'USER_NOT_FOUND' ? 'missing' : 'expired',
          message,
        })
        return {
          ok: false,
          authCleared: true,
          status: response.status,
          code: error?.code,
          message,
        }
      }

      if (response.status === 403 && error?.code === 'USER_DISABLED') {
        message = getSessionEndedMessage(error.message)
        clearTokens({
          reason: 'disabled',
          message,
        })
        return {
          ok: false,
          authCleared: true,
          status: response.status,
          code: error.code,
          message,
        }
      }

      if (error?.message) {
        message = getDownloadSessionSyncMessage(error.message)
      }
      isDownloadSessionReady = false
      return { ok: false, status: response.status, code: error?.code, message }
    }

    isDownloadSessionReady = true
    return { ok: true }
  } catch {
    isDownloadSessionReady = false
    return { ok: false, message: getDownloadSessionSyncMessage() }
  }
}

export async function ensureDownloadSession(): Promise<DownloadSessionResult> {
  if (!hasStoredAuthState()) {
    return { ok: true }
  }

  return syncDownloadSession()
}

// Auth header helper
export function getAuthHeaders(): HeadersInit {
  clearLegacyTokenStorage()
  return {}
}

function mergeAuthHeaders(headers?: HeadersInit): Headers {
  clearLegacyTokenStorage()
  return new Headers(headers)
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
  
  const response = await fetch(url, {
    ...options,
    headers,
    credentials: options.credentials ?? 'same-origin',
  })
  
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
  const refreshed = await tryRefreshToken()
  return refreshed && isDownloadSessionReady
}

// Try to refresh the token
async function tryRefreshToken(): Promise<boolean> {
  if (refreshPromise) {
    return refreshPromise
  }

  refreshPromise = (async () => {
    try {
      const response = await fetch(`${API_BASE}/auth/refresh`, {
        method: 'POST',
        headers: { [COOKIE_SESSION_HEADER]: COOKIE_SESSION_VALUE },
        credentials: 'same-origin',
      })

      if (!response.ok) {
        clearTokens()
        return false
      }

      const body: AuthApiResponse<RefreshResponse> = await response.json()
      const data = parseAuthSessionData(readAuthSuccessData(body))
      storeSessionUser(data.user)
      const downloadSession = await syncDownloadSession()
      return !downloadSession.authCleared
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
    headers: {
      'Content-Type': 'application/json',
      [COOKIE_SESSION_HEADER]: COOKIE_SESSION_VALUE,
    },
    credentials: 'same-origin',
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

  storeSessionUser(data.user)
  const downloadSession = await syncDownloadSession()
  if (downloadSession.authCleared) {
    throw new AuthError(
      downloadSession.message ?? getMissingBrowserSessionMessage(),
      downloadSession.status ?? 401,
      downloadSession.code,
    )
  }
  const action = getAuthActionResult(response, body)

  return {
    user: data.user,
    warning: action.warning || !downloadSession.ok,
    message: action.message ?? downloadSession.message,
  }
}

// Logout
export async function logout(): Promise<AuthActionResult> {
  clearLegacyTokenStorage()

  let response: Response
  try {
    response = await fetch(`${API_BASE}/auth/logout`, {
      method: 'POST',
      credentials: 'same-origin',
    })
  } catch {
    throw new AuthError('退出登录失败', 0)
  }

  if (!response.ok) {
    let message = '退出登录失败'
    let code: string | undefined
    try {
      const body: AuthApiResponse<never> = await response.json()
      if (body.error?.message) message = body.error.message
      if (body.error?.code) code = body.error.code
    } catch {
      // Keep the generic logout error when the response body is invalid.
    }
    throw new AuthError(message, response.status, code)
  }

  let body: AuthApiResponse<null> | undefined
  try {
    body = await response.json()
  } catch {
    body = undefined
  }

  const result = getAuthActionResult(response, body)
  clearTokens()
  return result
}

// Get current user
export async function getCurrentUser(): Promise<User | null> {
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

  storeSessionUser(user)
  const downloadSession = await syncDownloadSession()
  if (downloadSession.authCleared) {
    return null
  }
  return user
}
