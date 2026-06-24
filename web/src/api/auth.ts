const API_BASE = '/api/v1'

import { getNonBlankJsonString, readStructuredJsonErrorDetails } from '@/lib/jsonErrorResponse'
import { normalizeUserHomeDir } from '@/lib/utils'

export interface User {
  id: string
  username: string
  email?: string
  role: 'admin' | 'user' | 'guest'
  homeDir: string
  mustChangePassword: boolean
}

interface ApiUser {
  id: string
  username: string
  email?: string
  role: 'admin' | 'user' | 'guest'
  home_dir: string
  must_change_password: boolean
}

interface StoredUserPayload {
  id?: unknown
  username?: unknown
  email?: unknown
  role?: unknown
  home_dir?: unknown
  homeDir?: unknown
  must_change_password?: unknown
  mustChangePassword?: unknown
}

export interface LoginRequest {
  username: string
  password: string
}

export interface ChangePasswordRequest {
  old_password: string
  new_password: string
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
  reason?: 'expired' | 'disabled' | 'missing' | 'logout' | 'password_changed'
}

export interface AuthSessionUpdatedDetail {
  user: User
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
const INVALID_PASSWORD_CHANGE_RESPONSE_MESSAGE = '修改密码响应无效'
const INVALID_USER_PAYLOAD_MESSAGE = '用户数据无效'
const AUTH_REFRESH_LOCK_NAME = 'mnemonas:auth-refresh'
export const AUTH_CLEARED_EVENT = 'mnemonas:auth-cleared'
export const AUTH_SESSION_UPDATED_EVENT = 'mnemonas:auth-session-updated'
export const AUTH_CROSS_TAB_SYNC_KEY = 'mnemonas:auth-cross-tab-sync'
export const AUTH_CROSS_TAB_CHANNEL_NAME = 'mnemonas:auth-session'
export const AUTH_CROSS_TAB_SOURCE_ID = `${Date.now()}-${Math.random().toString(36).slice(2)}`
interface RefreshOperation {
  generation: number
  controller: AbortController
  promise: Promise<RefreshSessionResult>
}

let authSessionGeneration = 0
let refreshOperation: RefreshOperation | null = null
let isDownloadSessionReady = true
let legacyAuthRequestCount = 0
let authCrossTabSyncSequence = 0
let inMemorySessionUser: User | null = null
let authBroadcastChannel: BroadcastChannel | null = null

interface LocalStorageReadResult {
  available: boolean
  value: string | null
}

function tryReadLocalStorageItem(key: string): LocalStorageReadResult {
  try {
    if (typeof localStorage === 'undefined') {
      return { available: false, value: null }
    }
    return { available: true, value: localStorage.getItem(key) }
  } catch {
    return { available: false, value: null }
  }
}

function readLocalStorageItem(key: string): string | null {
  return tryReadLocalStorageItem(key).value
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

function createSupersededAuthError(): DOMException {
  return new DOMException('身份验证状态已发生变化', 'AbortError')
}

function isAuthGenerationCurrent(generation: number): boolean {
  return generation === authSessionGeneration
}

function assertAuthGenerationCurrent(generation: number): void {
  if (!isAuthGenerationCurrent(generation)) {
    throw createSupersededAuthError()
  }
}

export function invalidateAuthSessionRequests(): number {
  authSessionGeneration += 1
  const pendingRefresh = refreshOperation
  pendingRefresh?.controller.abort()
  if (refreshOperation === pendingRefresh) {
    refreshOperation = null
  }
  return authSessionGeneration
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

function isPasswordChangeSuccessResponse(value: unknown): value is AuthApiResponse<null | { warning: true }> {
  if (!isRecord(value) || value.success !== true || !('data' in value)) {
    return false
  }

  if (
    (value.warning !== undefined && typeof value.warning !== 'boolean') ||
    (value.message !== undefined && typeof value.message !== 'string')
  ) {
    return false
  }

  return value.data === null || (isRecord(value.data) && value.data.warning === true)
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

async function handleUnauthorizedSessionResponse(
  response: Response,
  generation = authSessionGeneration,
): Promise<void> {
  if (response.status !== 401 || !hasStoredAuthState() || !isAuthGenerationCurrent(generation)) {
    return
  }

  let detail: AuthClearedDetail = {
    reason: 'expired',
    message: '登录已过期，请重新登录',
  }

  try {
    const error = await readAuthApiError(response)
    if (!isAuthGenerationCurrent(generation)) {
      return
    }
    if (error?.code === 'USER_NOT_FOUND') {
      detail = {
        reason: 'missing',
        message: getMissingUserMessage(error.message),
      }
    }
  } catch {
    // Keep the generic expired-session detail when the error payload is invalid.
  }

  if (isAuthGenerationCurrent(generation)) {
    clearTokens(detail)
  }
}

async function handleForbiddenSessionResponse(
  response: Response,
  generation = authSessionGeneration,
): Promise<void> {
  if (response.status !== 403 || !isAuthGenerationCurrent(generation)) {
    return
  }

  try {
    const error = await readAuthApiError(response)
    if (!isAuthGenerationCurrent(generation)) {
      return
    }
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
  const storedUser = tryReadLocalStorageItem(USER_KEY)
  if (!storedUser.value) {
    if (storedUser.available) {
      inMemorySessionUser = null
    }
    return null
  }
  try {
    const user = normalizeStoredUser(JSON.parse(storedUser.value) as StoredUserPayload)
    inMemorySessionUser = user
    return user
  } catch {
    removeLocalStorageItem(USER_KEY)
    inMemorySessionUser = null
    return null
  }
}

function getKnownSessionUser(): User | null {
  return inMemorySessionUser ?? getStoredUser()
}

function normalizeUser(user: ApiUser): User {
  if (
    !isCanonicalNonEmptyString(user.id) ||
    !isCanonicalNonEmptyString(user.username) ||
    !isUserRole(user.role) ||
    typeof user.home_dir !== 'string' ||
    user.home_dir.length === 0 ||
    typeof user.must_change_password !== 'boolean' ||
    (user.email !== undefined && typeof user.email !== 'string')
  ) {
    throw new Error(INVALID_USER_PAYLOAD_MESSAGE)
  }

  const normalizedHomeDir = normalizeUserHomeDir(user.home_dir)

  return {
    id: user.id,
    username: user.username,
    email: user.email,
    role: user.role,
    homeDir: normalizedHomeDir,
    mustChangePassword: user.must_change_password,
  }
}

function normalizeStoredUser(user: StoredUserPayload): User {
  const homeDir = user.homeDir ?? user.home_dir
  const mustChangePassword = user.mustChangePassword ?? user.must_change_password

  if (
    !isCanonicalNonEmptyString(user.id) ||
    !isCanonicalNonEmptyString(user.username) ||
    !isUserRole(user.role) ||
    typeof homeDir !== 'string' ||
    homeDir.length === 0 ||
    typeof mustChangePassword !== 'boolean' ||
    (user.email !== undefined && typeof user.email !== 'string')
  ) {
    throw new Error(INVALID_USER_PAYLOAD_MESSAGE)
  }

  return {
    id: user.id,
    username: user.username,
    email: user.email,
    role: user.role,
    homeDir: normalizeUserHomeDir(homeDir),
    mustChangePassword,
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
  inMemorySessionUser = { ...user }
  clearLegacyTokenStorage()
  writeLocalStorageItem(SESSION_MARKER_KEY, '1')
  writeLocalStorageItem(USER_KEY, JSON.stringify(user))
}

function getAuthBroadcastChannel(): BroadcastChannel | null {
  if (authBroadcastChannel) {
    return authBroadcastChannel
  }
  try {
    if (typeof window === 'undefined' || typeof window.BroadcastChannel !== 'function') {
      return null
    }
    authBroadcastChannel = new window.BroadcastChannel(AUTH_CROSS_TAB_CHANNEL_NAME)
    return authBroadcastChannel
  } catch {
    return null
  }
}

function publishAuthCrossTabSync(type: 'session_updated' | 'cleared', reason?: AuthClearedDetail['reason']): void {
  authCrossTabSyncSequence += 1
  const signal = {
    version: 1,
    type,
    ...(reason ? { reason } : {}),
    nonce: `${Date.now()}-${authCrossTabSyncSequence}-${Math.random().toString(36).slice(2)}`,
    source_id: AUTH_CROSS_TAB_SOURCE_ID,
  }
  writeLocalStorageItem(AUTH_CROSS_TAB_SYNC_KEY, JSON.stringify(signal))
  try {
    getAuthBroadcastChannel()?.postMessage(signal)
  } catch {
    authBroadcastChannel?.close()
    authBroadcastChannel = null
  }
}

function notifyAuthSessionUpdated(user: User): void {
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent<AuthSessionUpdatedDetail>(AUTH_SESSION_UPDATED_EVENT, {
      detail: { user },
    }))
  }
  publishAuthCrossTabSync('session_updated')
}

export function clearTokens(detail?: AuthClearedDetail): void {
  invalidateAuthSessionRequests()
  inMemorySessionUser = null
  clearLegacyTokenStorage()
  removeLocalStorageItem(USER_KEY)
  removeLocalStorageItem(SESSION_MARKER_KEY)
  isDownloadSessionReady = true
  publishAuthCrossTabSync('cleared', detail?.reason)

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
  passwordChangeRequired: boolean
  user?: User
}

interface RefreshSessionResult {
  refreshed: boolean
  passwordChangeRequired: boolean
}

type BrowserSessionProbeResult =
  | { status: 'recovered'; user: User }
  | { status: 'refresh_required' }
  | { status: 'unavailable' }

type AuthCrossTabTransition = 'session_updated' | 'cleared'

class AuthRefreshCoordinationError extends Error {
  constructor() {
    super('Unable to coordinate the browser session refresh')
    this.name = 'AuthRefreshCoordinationError'
  }
}

function getAuthRefreshLockManager(): LockManager | null {
  try {
    if (typeof navigator === 'undefined') {
      return null
    }

    const locks = (navigator as unknown as { locks?: LockManager }).locks
    return locks && typeof locks.request === 'function' ? locks : null
  } catch {
    return null
  }
}

function readAuthCrossTabTransition(previousValue: string | null): AuthCrossTabTransition | null {
  const currentValue = readLocalStorageItem(AUTH_CROSS_TAB_SYNC_KEY)
  if (!currentValue || currentValue === previousValue) {
    return null
  }

  try {
    const parsed: unknown = JSON.parse(currentValue)
    if (!isRecord(parsed) || parsed.version !== 1 || !isCanonicalNonEmptyString(parsed.nonce)) {
      return null
    }
    if (parsed.type === 'session_updated' || parsed.type === 'cleared') {
      return parsed.type
    }
  } catch {
    // Ignore malformed cross-tab signals and keep the refresh lock authoritative.
  }

  return null
}

function hasSameAuthSecurityScope(expected: User, current: User): boolean {
  return expected.id === current.id
    && expected.username === current.username
    && expected.role === current.role
    && expected.homeDir === current.homeDir
    && expected.mustChangePassword === current.mustChangePassword
}

async function syncDownloadSession(
  options: AuthRequestOptions = {},
  force = false,
  generation = authSessionGeneration,
): Promise<DownloadSessionResult> {
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
    assertAuthGenerationCurrent(generation)

    if (!response.ok) {
      let message = getDownloadSessionSyncMessage()
      const error = await readAuthApiError(response)
      assertAuthGenerationCurrent(generation)

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

async function syncDownloadSessionForUser(
  user: User,
  options: AuthRequestOptions = {},
  force = false,
  generation = authSessionGeneration,
): Promise<DownloadSessionResult> {
  if (user.mustChangePassword) {
    isDownloadSessionReady = true
    return { ok: true }
  }

  return syncDownloadSession(options, force, generation)
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
  generation: number,
): Promise<boolean> {
  if (!shouldRefreshToken(url, retryCount)) {
    return false
  }

  if (refreshOperation) {
    return true
  }

  if (hadAuthState) {
    return true
  }

  const error = await readAuthApiError(response)
  return isAuthGenerationCurrent(generation) && error?.code === 'TOKEN_EXPIRED'
}

// Fetch with auth
export async function authFetch(url: string, options: RequestInit = {}, retryCount = 0): Promise<Response> {
  const requestGeneration = authSessionGeneration
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

    if (isRequestSignalAborted(options.signal) || !isAuthGenerationCurrent(requestGeneration)) {
      return response
    }

    // If unauthorized, try to refresh token
    const shouldRefresh = response.status === 401
      && await shouldAttemptTokenRefresh(response, url, retryCount, hadAuthStateBeforeRequest, requestGeneration)
    if (!isAuthGenerationCurrent(requestGeneration)) {
      return response
    }
    if (shouldRefresh) {
      const refreshResult = await tryRefreshToken(hadAuthStateBeforeRequest)
      if (refreshResult.refreshed && refreshResult.passwordChangeRequired) {
        return response
      }
      if (refreshResult.refreshed) {
        return authFetch(url, options, retryCount + 1)
      }
      if (hadAuthStateBeforeRequest) {
        return response
      }
    }

    await handleUnauthorizedSessionResponse(response, requestGeneration)

    await handleForbiddenSessionResponse(response, requestGeneration)

    return response
  } finally {
    if (hadLegacyAuthStateBeforeRequest) {
      legacyAuthRequestCount = Math.max(0, legacyAuthRequestCount - 1)
    }
  }
}

export async function refreshAuthSession(): Promise<boolean> {
  const result = await tryRefreshToken()
  return result.refreshed && !result.passwordChangeRequired && isDownloadSessionReady
}

function failedRefreshResult(): RefreshSessionResult {
  return { refreshed: false, passwordChangeRequired: false }
}

async function requestAuthRefreshLock<T>(
  lockManager: LockManager,
  options: LockOptions,
  callback: (lock: Lock | null) => Promise<T> | T,
): Promise<T> {
  let callbackStarted = false
  try {
    return await lockManager.request(AUTH_REFRESH_LOCK_NAME, options, (lock) => {
      callbackStarted = true
      return callback(lock)
    })
  } catch (error) {
    if (!callbackStarted && !isAbortError(error)) {
      throw new AuthRefreshCoordinationError()
    }
    throw error
  }
}

async function performRefreshRequest(
  generation: number,
  signal: AbortSignal,
  hadAuthState: boolean,
): Promise<RefreshSessionResult> {
  const isCurrent = () => isAuthGenerationCurrent(generation) && !signal.aborted
  const response = await fetch(`${API_BASE}/auth/refresh`, {
    method: 'POST',
    headers: { [COOKIE_SESSION_HEADER]: COOKIE_SESSION_VALUE },
    credentials: 'same-origin',
    signal,
  })
  if (!isCurrent()) {
    return failedRefreshResult()
  }

  if (!response.ok) {
    const error = await readAuthApiError(response)
    if (!isCurrent()) {
      return failedRefreshResult()
    }
    if (error?.code === 'TOKEN_REVOKED') {
      const recovery = await recoverBrowserSessionAfterRefreshRevoked(generation, signal)
      if (!isCurrent()) {
        return failedRefreshResult()
      }
      if (recovery.recovered) {
        return {
          refreshed: true,
          passwordChangeRequired: recovery.passwordChangeRequired,
        }
      }
      if (!recovery.terminal) {
        return failedRefreshResult()
      }
    }
    if (hadAuthState && hasStoredAuthState()) {
      clearTokens()
    }
    return failedRefreshResult()
  }

  const body: AuthApiResponse<RefreshResponse> = await response.json()
  if (!isCurrent()) {
    return failedRefreshResult()
  }
  const data = parseAuthSessionData(readAuthSuccessData(body))
  storeSessionUser(data.user)
  notifyAuthSessionUpdated(data.user)
  const downloadSession = await syncDownloadSessionForUser(data.user, { signal }, true, generation)
  if (!isCurrent()) {
    return failedRefreshResult()
  }
  return {
    refreshed: !downloadSession.authCleared,
    passwordChangeRequired: data.user.mustChangePassword && !downloadSession.authCleared,
  }
}

async function probeBrowserSessionAfterLockWait(
  generation: number,
  signal: AbortSignal,
): Promise<BrowserSessionProbeResult> {
  const isCurrent = () => isAuthGenerationCurrent(generation) && !signal.aborted
  try {
    const response = await fetch(`${API_BASE}/auth/me`, {
      credentials: 'same-origin',
      signal,
    })
    if (!isCurrent()) {
      return { status: 'unavailable' }
    }
    if (!response.ok) {
      return response.status === 401 || response.status === 403
        ? { status: 'refresh_required' }
        : { status: 'unavailable' }
    }

    const body: AuthApiResponse<{ user: ApiUser }> = await response.json()
    if (!isCurrent()) {
      return { status: 'unavailable' }
    }
    const data = readAuthSuccessData(body)
    return { status: 'recovered', user: normalizeUser(data.user) }
  } catch {
    return { status: 'unavailable' }
  }
}

async function reuseProbedBrowserSession(
  user: User,
  generation: number,
  signal: AbortSignal,
): Promise<RefreshSessionResult> {
  storeSessionUser(user)
  notifyAuthSessionUpdated(user)
  const downloadSession = await syncDownloadSessionForUser(user, { signal }, true, generation)
  if (!isAuthGenerationCurrent(generation) || signal.aborted) {
    return failedRefreshResult()
  }
  return {
    refreshed: !downloadSession.authCleared,
    passwordChangeRequired: user.mustChangePassword && !downloadSession.authCleared,
  }
}

async function performCoordinatedRefresh(
  lockManager: LockManager,
  generation: number,
  signal: AbortSignal,
  hadAuthState: boolean,
): Promise<RefreshSessionResult> {
  const crossTabSyncBeforeWait = readLocalStorageItem(AUTH_CROSS_TAB_SYNC_KEY)
  const userBeforeWait = getKnownSessionUser()
  const refresh = () => performRefreshRequest(generation, signal, hadAuthState)
  const immediateResult = await requestAuthRefreshLock<RefreshSessionResult | null>(
    lockManager,
    { mode: 'exclusive', ifAvailable: true, signal },
    (lock) => lock ? refresh() : null,
  )
  if (immediateResult) {
    return immediateResult
  }
  if (!isAuthGenerationCurrent(generation) || signal.aborted) {
    return failedRefreshResult()
  }

  return requestAuthRefreshLock(
    lockManager,
    { mode: 'exclusive', signal },
    async (lock) => {
      if (!lock || !isAuthGenerationCurrent(generation) || signal.aborted) {
        return failedRefreshResult()
      }

      const transition = readAuthCrossTabTransition(crossTabSyncBeforeWait)
      if (transition === 'cleared') {
        return failedRefreshResult()
      }

      const probe = await probeBrowserSessionAfterLockWait(generation, signal)
      if (!isAuthGenerationCurrent(generation) || signal.aborted) {
        return failedRefreshResult()
      }
      if (probe.status === 'refresh_required') {
        return refresh()
      }
      if (probe.status === 'unavailable') {
        return failedRefreshResult()
      }
      if (!userBeforeWait || !hasSameAuthSecurityScope(userBeforeWait, probe.user)) {
        invalidateAuthSessionRequests()
        storeSessionUser(probe.user)
        notifyAuthSessionUpdated(probe.user)
        return failedRefreshResult()
      }

      return reuseProbedBrowserSession(probe.user, generation, signal)
    },
  )
}

// Try to refresh the token
async function tryRefreshToken(hadAuthState = hasStoredAuthState()): Promise<RefreshSessionResult> {
  if (refreshOperation && isAuthGenerationCurrent(refreshOperation.generation)) {
    return refreshOperation.promise
  }

  const generation = authSessionGeneration
  const controller = new AbortController()
  const operation: RefreshOperation = {
    generation,
    controller,
    promise: Promise.resolve(failedRefreshResult()),
  }
  const isCurrent = () => isAuthGenerationCurrent(generation) && !controller.signal.aborted

  operation.promise = (async () => {
    try {
      const lockManager = getAuthRefreshLockManager()
      return lockManager
        ? await performCoordinatedRefresh(lockManager, generation, controller.signal, hadAuthState)
        : await performRefreshRequest(generation, controller.signal, hadAuthState)
    } catch (error) {
      if (isCurrent()
        && hadAuthState
        && !isAbortError(error)
        && !(error instanceof AuthRefreshCoordinationError)) {
        clearTokens()
      }
      return failedRefreshResult()
    } finally {
      if (refreshOperation === operation) {
        refreshOperation = null
      }
    }
  })()
  refreshOperation = operation

  return operation.promise
}

async function recoverBrowserSessionAfterRefreshRevoked(
  generation: number,
  signal: AbortSignal,
): Promise<RefreshReplayRecoveryResult> {
  const isCurrent = () => isAuthGenerationCurrent(generation) && !signal.aborted
  try {
    const response = await fetch(`${API_BASE}/auth/me`, {
      credentials: 'same-origin',
      signal,
    })
    if (!isCurrent()) {
      return { recovered: false, terminal: false, passwordChangeRequired: false }
    }

    if (!response.ok) {
      if (response.status === 401 || response.status === 403) {
        await handleUnauthorizedSessionResponse(response, generation)
        await handleForbiddenSessionResponse(response, generation)
        return { recovered: false, terminal: true, passwordChangeRequired: false }
      }
      return { recovered: false, terminal: false, passwordChangeRequired: false }
    }

    const body: AuthApiResponse<{ user: ApiUser }> = await response.json()
    if (!isCurrent()) {
      return { recovered: false, terminal: false, passwordChangeRequired: false }
    }
    const data = readAuthSuccessData(body)
    const user = normalizeUser(data.user)
    storeSessionUser(user)
    notifyAuthSessionUpdated(user)

    const downloadSession = await syncDownloadSessionForUser(user, { signal }, true, generation)
    if (!isCurrent()) {
      return { recovered: false, terminal: false, passwordChangeRequired: false }
    }
    return {
      recovered: !downloadSession.authCleared,
      terminal: Boolean(downloadSession.authCleared),
      passwordChangeRequired: user.mustChangePassword && !downloadSession.authCleared,
      user,
    }
  } catch {
    return { recovered: false, terminal: false, passwordChangeRequired: false }
  }
}

// Login
export async function login(username: string, password: string, options: AuthRequestOptions = {}): Promise<LoginActionResult> {
  const generation = invalidateAuthSessionRequests()
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
  assertAuthGenerationCurrent(generation)
  
  if (!response.ok) {
    let message = '登录失败'
    let code: string | undefined
    const error = await readAuthApiError(response, message)
    assertAuthGenerationCurrent(generation)
    if (error?.message) message = error.message
    if (error?.code) code = error.code
    throw new AuthError(message, response.status, code)
  }
  
  let data: AuthSessionData
  let body: AuthApiResponse<LoginResponse> | undefined
  try {
    body = await response.json()
    assertAuthGenerationCurrent(generation)
    data = parseAuthSessionData(readAuthSuccessData(body))
  } catch (error) {
    if (!isAuthGenerationCurrent(generation) || isAbortError(error)) {
      throw createSupersededAuthError()
    }
    throw new AuthError(INVALID_AUTH_RESPONSE_MESSAGE, response.status)
  }

  storeSessionUser(data.user)
  publishAuthCrossTabSync('session_updated')
  const downloadSession = await syncDownloadSessionForUser(data.user, options, true, generation)
  if (downloadSession.authCleared) {
    throw new AuthError(
      downloadSession.message ?? getMissingBrowserSessionMessage(),
      downloadSession.status ?? 401,
      downloadSession.code,
    )
  }
  assertAuthGenerationCurrent(generation)
  const action = getAuthActionResult(response, body)

  return {
    user: data.user,
    warning: action.warning || !downloadSession.ok,
    message: action.message ?? downloadSession.message,
  }
}

// Logout
export async function logout(options: AuthRequestOptions = {}): Promise<AuthActionResult> {
  const generation = invalidateAuthSessionRequests()
  clearLegacyTokenStorage()

  let response: Response
  try {
    response = await fetch(`${API_BASE}/auth/logout`, {
      method: 'POST',
      credentials: 'same-origin',
      ...(options.signal ? { signal: options.signal } : {}),
    })
  } catch (error) {
    if (!isAuthGenerationCurrent(generation) || options.signal?.aborted || isAbortError(error)) {
      throw createSupersededAuthError()
    }
    throw new AuthError('退出登录失败', 0)
  }
  assertAuthGenerationCurrent(generation)

  if (!response.ok) {
    let message = '退出登录失败'
    let code: string | undefined
    const error = await readAuthApiError(response, message)
    assertAuthGenerationCurrent(generation)
    if (error?.message) message = error.message
    if (error?.code) code = error.code
    throw new AuthError(message, response.status, code)
  }

  let body: AuthApiResponse<null> | undefined
  try {
    body = await response.json()
    assertAuthGenerationCurrent(generation)
  } catch {
    assertAuthGenerationCurrent(generation)
    body = undefined
  }

  const result = getAuthActionResult(response, body)
  clearTokens({ reason: 'logout' })
  return result
}

export async function changePassword(
  request: ChangePasswordRequest,
  options: AuthRequestOptions = {},
): Promise<AuthActionResult> {
  const generation = invalidateAuthSessionRequests()
  const response = await fetch(`${API_BASE}/auth/password`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    credentials: 'same-origin',
    body: JSON.stringify(request),
    ...(options.signal ? { signal: options.signal } : {}),
  })
  assertAuthGenerationCurrent(generation)

  if (!response.ok) {
    let message = '修改密码失败'
    let code: string | undefined
    const error = await readAuthApiError(response, message)
    assertAuthGenerationCurrent(generation)
    if (error?.message) message = error.message
    if (error?.code) code = error.code
    throw new AuthError(message, response.status, code)
  }

  let body: AuthApiResponse<null | { warning: true }>
  try {
    const value: unknown = await response.json()
    assertAuthGenerationCurrent(generation)
    if (!isPasswordChangeSuccessResponse(value)) {
      throw new Error(INVALID_PASSWORD_CHANGE_RESPONSE_MESSAGE)
    }
    body = value
  } catch (error) {
    if (!isAuthGenerationCurrent(generation) || isAbortError(error)) {
      throw createSupersededAuthError()
    }
    throw new AuthError(INVALID_PASSWORD_CHANGE_RESPONSE_MESSAGE, response.status)
  } finally {
    if (isAuthGenerationCurrent(generation)) {
      clearTokens({ reason: 'password_changed' })
    }
  }

  return getAuthActionResult(response, body)
}

// Get current user
export async function getCurrentUser(options: AuthRequestOptions = {}): Promise<User | null> {
  const generation = authSessionGeneration
  const response = await authFetch(
    `${API_BASE}/auth/me`,
    options.signal ? { signal: options.signal } : {},
  )
  if (options.signal?.aborted) {
    throw createSupersededAuthError()
  }
  
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
  assertAuthGenerationCurrent(generation)

  let body: AuthApiResponse<{ user: ApiUser }>
  try {
    body = await response.json()
    if (options.signal?.aborted || !isAuthGenerationCurrent(generation)) {
      throw createSupersededAuthError()
    }
  } catch (error) {
    if (!isAuthGenerationCurrent(generation) || isAbortError(error)) {
      throw createSupersededAuthError()
    }
    clearTokens()
    return null
  }

  let data: { user: ApiUser }
  try {
    data = readAuthSuccessData(body)
  } catch {
    if (isAuthGenerationCurrent(generation)) {
      clearTokens()
    }
    return null
  }

  let user: User
  try {
    user = normalizeUser(data.user)
  } catch {
    if (isAuthGenerationCurrent(generation)) {
      clearTokens()
    }
    return null
  }

  assertAuthGenerationCurrent(generation)
  storeSessionUser(user)
  const downloadSession = await syncDownloadSessionForUser(user, options, true, generation)
  if (options.signal?.aborted) {
    throw createSupersededAuthError()
  }
  if (downloadSession.authCleared) {
    return null
  }
  return user
}
