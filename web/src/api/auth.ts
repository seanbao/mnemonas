const API_BASE = '/api/v1'

import { getNonBlankJsonString, readStructuredJsonErrorDetails } from '@/lib/jsonErrorResponse'
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
  message?: string
}

interface AuthApiResponse<T> {
  success: boolean
  data?: T
  warning?: boolean
  message?: string
  code?: string
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

export interface AuthRequestOptions {
  signal?: AbortSignal
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
const INVALID_AUTH_RESPONSE_MESSAGE = '登录响应无效'
const INVALID_USER_PAYLOAD_MESSAGE = '用户数据无效'
export const AUTH_CLEARED_EVENT = 'mnemonas:auth-cleared'
let refreshPromise: Promise<boolean> | null = null
let isDownloadSessionReady = true
let legacyAuthRequestCount = 0

function readLocalStorageItem(key: string): string | null {
  try {
    if (typeof localStorage === 'undefined') {
      return null
    }
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function writeLocalStorageItem(key: string, value: string): void {
  try {
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(key, value)
    }
  } catch {
    // Browser storage may be disabled while the HttpOnly cookie session remains usable.
  }
}

function removeLocalStorageItem(key: string): void {
  try {
    if (typeof localStorage !== 'undefined') {
      localStorage.removeItem(key)
    }
  } catch {
    // Ignore storage access failures so logout and auth recovery can continue.
  }
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

function isRequestSignalAborted(signal: RequestInit['signal']): boolean {
  return signal instanceof AbortSignal && signal.aborted
}

function getSessionEndedMessage(responseMessage?: string): string {
  return getNonBlankJsonString(responseMessage) ?? '账户已被禁用，请联系管理员'
}

function getMissingUserMessage(responseMessage?: string): string {
  return getNonBlankJsonString(responseMessage) ?? '账户不存在或已被删除，请重新登录'
}

function getMissingBrowserSessionMessage(): string {
  return '登录会话未建立，请重新登录'
}

function hasStoredAuthState(): boolean {
  return hasBrowserSessionState() || hasLegacyTokenState()
}

function hasBrowserSessionState(): boolean {
  return Boolean(
    readLocalStorageItem(SESSION_MARKER_KEY) ||
    readLocalStorageItem(USER_KEY)
  )
}

function hasLegacyTokenState(): boolean {
  return legacyAuthRequestCount > 0 || hasLegacyTokenStorage()
}

function hasLegacyTokenStorage(): boolean {
  return Boolean(
    readLocalStorageItem(TOKEN_KEY) ||
    readLocalStorageItem(REFRESH_TOKEN_KEY)
  )
}

function isUserRole(role: unknown): role is User['role'] {
  return role === 'admin' || role === 'user' || role === 'guest'
}

function isCanonicalNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.trim() === value && value.length > 0
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object'
}

function parseAuthSessionData(data: LoginResponse | RefreshResponse | undefined): AuthSessionData {
  if (!data || data.user == null) {
    throw new Error(INVALID_AUTH_RESPONSE_MESSAGE)
  }

  return {
    user: normalizeUser(data.user),
  }
}

function readAuthSuccessData<T>(body: AuthApiResponse<T> | undefined): T {
  if (!body || body.success !== true || body.data === undefined) {
    throw new Error(INVALID_AUTH_RESPONSE_MESSAGE)
  }

  return body.data
}

function getAuthActionResult<T>(response: Response, body: AuthApiResponse<T> | undefined): AuthActionResult {
  return {
    warning: response.headers?.get?.('Warning') != null ||
      body?.warning === true ||
      (isRecord(body?.data) && body.data.warning === true),
    message: getNonBlankJsonString(body?.message),
  }
}

async function readAuthApiError(response: Response, fallback = ''): Promise<AuthApiError | undefined> {
  const structuredError = await readStructuredJsonErrorDetails(response, fallback)
  if (structuredError) {
    return {
      code: structuredError.code,
      message: structuredError.message,
    }
  }

  try {
    const bodySource = typeof response.clone === 'function' ? response.clone() : response
    const body: AuthApiResponse<never> = await bodySource.json()
    const code = getNonBlankJsonString(body.error?.code) ?? getNonBlankJsonString(body.code)
    const message = getNonBlankJsonString(body.error?.message) ?? getNonBlankJsonString(body.message)
    return code === undefined && message === undefined ? undefined : { code, message }
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
  const data = readLocalStorageItem(USER_KEY)
  if (!data) return null
  try {
    return normalizeUser(JSON.parse(data) as ApiUser)
  } catch {
    removeLocalStorageItem(USER_KEY)
    return null
  }
}

function normalizeUser(user: ApiUser): User {
  const homeDir = user.homeDir ?? user.home_dir

  if (
    !isCanonicalNonEmptyString(user.id) ||
    !isCanonicalNonEmptyString(user.username) ||
    !isUserRole(user.role) ||
    typeof homeDir !== 'string' ||
    homeDir.length === 0 ||
    (user.email !== undefined && typeof user.email !== 'string')
  ) {
    throw new Error(INVALID_USER_PAYLOAD_MESSAGE)
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
  removeLocalStorageItem(TOKEN_KEY)
  removeLocalStorageItem(REFRESH_TOKEN_KEY)
}

function storeSessionUser(user: User): void {
  clearLegacyTokenStorage()
  writeLocalStorageItem(SESSION_MARKER_KEY, '1')
  writeLocalStorageItem(USER_KEY, JSON.stringify(user))
}

export function clearTokens(detail?: AuthClearedDetail): void {
  clearLegacyTokenStorage()
  removeLocalStorageItem(USER_KEY)
  removeLocalStorageItem(SESSION_MARKER_KEY)
  isDownloadSessionReady = true

  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent<AuthClearedDetail>(AUTH_CLEARED_EVENT, { detail }))
  }
}

function getDownloadSessionSyncMessage(responseMessage?: string): string {
  return getNonBlankJsonString(responseMessage) ?? '原始预览和下载会话同步失败，请稍后重试'
}

interface DownloadSessionResult {
  ok: boolean
  message?: string
  authCleared?: boolean
  status?: number
  code?: string
}

interface RefreshReplayRecoveryResult {
  recovered: boolean
  terminal: boolean
}

async function syncDownloadSession(options: AuthRequestOptions = {}, force = false): Promise<DownloadSessionResult> {
  const hadBrowserSessionState = force || hasBrowserSessionState()
  clearLegacyTokenStorage()
  if (!hadBrowserSessionState) {
    isDownloadSessionReady = true
    return { ok: true }
  }

  try {
    const response = await fetch(`${API_BASE}/auth/download-session`, {
      method: 'POST',
      credentials: 'same-origin',
      ...(options.signal ? { signal: options.signal } : {}),
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
  } catch (error) {
    if (options.signal?.aborted || isAbortError(error)) {
      throw error
    }
    isDownloadSessionReady = false
    return { ok: false, message: getDownloadSessionSyncMessage() }
  }
}

export async function ensureDownloadSession(options: AuthRequestOptions = {}): Promise<DownloadSessionResult> {
  if (!hasStoredAuthState()) {
    return { ok: true }
  }

  return syncDownloadSession(options)
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

async function shouldAttemptTokenRefresh(
  response: Response,
  url: string,
  retryCount: number,
  hadAuthState: boolean,
): Promise<boolean> {
  if (!shouldRefreshToken(url, retryCount)) {
    return false
  }

  if (refreshPromise) {
    return true
  }

  if (hadAuthState) {
    return true
  }

  const error = await readAuthApiError(response)
  return error?.code === 'TOKEN_EXPIRED'
}

// Fetch with auth
export async function authFetch(url: string, options: RequestInit = {}, retryCount = 0): Promise<Response> {
  const hadLegacyAuthStateBeforeRequest = hasLegacyTokenStorage()
  const hadAuthStateBeforeRequest = hasStoredAuthState()
  if (hadLegacyAuthStateBeforeRequest) {
    legacyAuthRequestCount += 1
  }

  try {
    const headers = mergeAuthHeaders(options.headers)

    const response = await fetch(url, {
      ...options,
      headers,
      credentials: options.credentials ?? 'same-origin',
    })

    if (isRequestSignalAborted(options.signal)) {
      return response
    }

    // If unauthorized, try to refresh token
    const shouldRefresh = response.status === 401
      && await shouldAttemptTokenRefresh(response, url, retryCount, hadAuthStateBeforeRequest)
    if (shouldRefresh) {
      const refreshed = await tryRefreshToken(hadAuthStateBeforeRequest)
      if (refreshed) {
        return authFetch(url, options, retryCount + 1)
      }
      if (hadAuthStateBeforeRequest) {
        return response
      }
    }

    await handleUnauthorizedSessionResponse(response)

    await handleForbiddenSessionResponse(response)

    return response
  } finally {
    if (hadLegacyAuthStateBeforeRequest) {
      legacyAuthRequestCount = Math.max(0, legacyAuthRequestCount - 1)
    }
  }
}

export async function refreshAuthSession(): Promise<boolean> {
  const refreshed = await tryRefreshToken()
  return refreshed && isDownloadSessionReady
}

// Try to refresh the token
async function tryRefreshToken(hadAuthState = hasStoredAuthState()): Promise<boolean> {
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
        const error = await readAuthApiError(response)
        if (error?.code === 'TOKEN_REVOKED') {
          const recovery = await recoverBrowserSessionAfterRefreshRevoked()
          if (recovery.recovered) {
            return true
          }
          if (!recovery.terminal) {
            return false
          }
        }
        if (hadAuthState && hasStoredAuthState()) {
          clearTokens()
        }
        return false
      }

      const body: AuthApiResponse<RefreshResponse> = await response.json()
      const data = parseAuthSessionData(readAuthSuccessData(body))
      storeSessionUser(data.user)
      const downloadSession = await syncDownloadSession({}, true)
      return !downloadSession.authCleared
    } catch {
      if (hadAuthState) {
        clearTokens()
      }
      return false
    } finally {
      refreshPromise = null
    }
  })()

  return refreshPromise
}

async function recoverBrowserSessionAfterRefreshRevoked(): Promise<RefreshReplayRecoveryResult> {
  try {
    const response = await fetch(`${API_BASE}/auth/me`, {
      credentials: 'same-origin',
    })

    if (!response.ok) {
      if (response.status === 401 || response.status === 403) {
        await handleUnauthorizedSessionResponse(response)
        await handleForbiddenSessionResponse(response)
        return { recovered: false, terminal: true }
      }
      return { recovered: false, terminal: false }
    }

    const body: AuthApiResponse<{ user: ApiUser }> = await response.json()
    const data = readAuthSuccessData(body)
    const user = normalizeUser(data.user)
    storeSessionUser(user)

    const downloadSession = await syncDownloadSession({}, true)
    return { recovered: !downloadSession.authCleared, terminal: Boolean(downloadSession.authCleared) }
  } catch {
    return { recovered: false, terminal: false }
  }
}

// Login
export async function login(username: string, password: string, options: AuthRequestOptions = {}): Promise<LoginActionResult> {
  const response = await fetch(`${API_BASE}/auth/login`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      [COOKIE_SESSION_HEADER]: COOKIE_SESSION_VALUE,
    },
    credentials: 'same-origin',
    body: JSON.stringify({ username, password }),
    ...(options.signal ? { signal: options.signal } : {}),
  })
  
  if (!response.ok) {
    let message = '登录失败'
    let code: string | undefined
    const error = await readAuthApiError(response, message)
    if (error?.message) message = error.message
    if (error?.code) code = error.code
    throw new AuthError(message, response.status, code)
  }
  
  let data: AuthSessionData
  let body: AuthApiResponse<LoginResponse> | undefined
  try {
    body = await response.json()
    data = parseAuthSessionData(readAuthSuccessData(body))
  } catch {
    throw new AuthError(INVALID_AUTH_RESPONSE_MESSAGE, response.status)
  }

  storeSessionUser(data.user)
  const downloadSession = await syncDownloadSession(options, true)
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
    const error = await readAuthApiError(response, message)
    if (error?.message) message = error.message
    if (error?.code) code = error.code
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
export async function getCurrentUser(options: AuthRequestOptions = {}): Promise<User | null> {
  const response = await authFetch(
    `${API_BASE}/auth/me`,
    options.signal ? { signal: options.signal } : {},
  )
  
  if (!response.ok) {
    if (response.status === 401 || response.status === 403) {
      return null
    }

    let message = '获取当前用户失败'
    let code: string | undefined
    const error = await readAuthApiError(response, message)
    if (error?.message) message = error.message
    if (error?.code) code = error.code

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
  const downloadSession = await syncDownloadSession(options, true)
  if (downloadSession.authCleared) {
    return null
  }
  return user
}
