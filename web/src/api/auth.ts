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

export interface PasswordChangeRequestOptions extends AuthRequestOptions {
  expectedUserId: string
}

export interface AuthClearedDetail {
  message?: string
  reason?: 'expired'
    | 'disabled'
    | 'missing'
    | 'logout'
    | 'password_changed'
    | 'password_change_warning'
    | 'password_change_unconfirmed'
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
export const PASSWORD_CHANGE_UNCONFIRMED_MESSAGE = '密码修改结果无法确认。请先尝试使用新密码登录；若无法登录，再尝试原密码。'
const TERMINAL_PASSWORD_CHANGE_AUTH_CODES = new Set([
  'NOT_AUTHENTICATED',
  'MISSING_AUTH_HEADER',
  'INVALID_AUTH_HEADER',
  'INVALID_TOKEN',
  'TOKEN_EXPIRED',
  'TOKEN_REVOKED',
])
const DEFINITIVE_PASSWORD_CHANGE_FAILURES = new Set([
  '400:INVALID_REQUEST',
  '400:MISSING_EXPECTED_USER_ID',
  '400:MISSING_PASSWORD',
  '400:PASSWORD_TOO_SHORT',
  '400:PASSWORD_TOO_LONG',
  '400:PASSWORD_UNCHANGED',
  '401:NOT_AUTHENTICATED',
  '401:MISSING_AUTH_HEADER',
  '401:INVALID_AUTH_HEADER',
  '401:INVALID_TOKEN',
  '401:TOKEN_EXPIRED',
  '401:TOKEN_REVOKED',
  '401:USER_NOT_FOUND',
  '401:INVALID_PASSWORD',
  '403:USER_DISABLED',
  '403:PASSWORD_CHANGE_REQUIRED',
  '409:AUTH_SCOPE_CHANGED',
  '413:PAYLOAD_TOO_LARGE',
  '500:PASSWORD_ERROR',
  '503:TOKEN_STATE_UNAVAILABLE',
])
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

interface DownloadSessionOperation {
  generation: number
  controller: AbortController
  promise: Promise<DownloadSessionResult>
}

let authSessionGeneration = 0
let refreshOperation: RefreshOperation | null = null
const downloadSessionOperations = new Map<AbortSignal | undefined, DownloadSessionOperation>()
let isDownloadSessionReady = true
let legacyAuthRequestCount = 0
let authCrossTabSyncSequence = 0
let authCrossTabScopeTransitionSequence = 0
const maxAuthCrossTabScopeTransitions = 64
const authCrossTabScopeTransitions: Array<{
  sequence: number
  userId: string | null | undefined
}> = []
let inMemorySessionUser: User | null = null
let inMemorySessionUserWriteFailed = false
let inMemorySessionUserWriteFailureUserSnapshot: LocalStorageReadResult | null = null
let inMemorySessionUserWriteFailureSyncSnapshot: LocalStorageReadResult | null = null
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

function writeLocalStorageItem(key: string, value: string): boolean {
  try {
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(key, value)
      return true
    }
  } catch {
    // Browser storage may be disabled while the HttpOnly cookie session remains usable.
  }
  return false
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
  const pendingDownloadSessions = [...downloadSessionOperations.values()]
  downloadSessionOperations.clear()
  for (const operation of pendingDownloadSessions) {
    operation.controller.abort()
  }
  const pendingRefresh = refreshOperation
  pendingRefresh?.controller.abort()
  if (refreshOperation === pendingRefresh) {
    refreshOperation = null
  }
  return authSessionGeneration
}

export function recordAuthCrossTabScopeTransition(userId?: string | null): void {
  authCrossTabScopeTransitionSequence += 1
  authCrossTabScopeTransitions.push({
    sequence: authCrossTabScopeTransitionSequence,
    userId,
  })
  if (authCrossTabScopeTransitions.length > maxAuthCrossTabScopeTransitions) {
    authCrossTabScopeTransitions.shift()
  }
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

function isDefinitivePasswordChangeFailure(status: number, code?: string): boolean {
  return code !== undefined && DEFINITIVE_PASSWORD_CHANGE_FAILURES.has(`${status}:${code}`)
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
): Promise<boolean> {
  if (response.status !== 401 || !hasStoredAuthState() || !isAuthGenerationCurrent(generation)) {
    return false
  }

  let detail: AuthClearedDetail = {
    reason: 'expired',
    message: '登录已过期，请重新登录',
  }

  try {
    const error = await readAuthApiError(response)
    if (!isAuthGenerationCurrent(generation)) {
      return false
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
    return true
  }
  return false
}

async function handleForbiddenSessionResponse(
  response: Response,
  generation = authSessionGeneration,
): Promise<boolean> {
  if (response.status !== 403 || !isAuthGenerationCurrent(generation)) {
    return false
  }

  try {
    const error = await readAuthApiError(response)
    if (!isAuthGenerationCurrent(generation)) {
      return false
    }
    if (error?.code === 'USER_DISABLED') {
      clearTokens({
        reason: 'disabled',
        message: getSessionEndedMessage(error.message),
      })
      return true
    }
  } catch {
    // Ignore invalid error payloads and let callers handle the response body.
  }
  return false
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
  if (inMemorySessionUserWriteFailed && inMemorySessionUser) {
    return inMemorySessionUser
  }

  const storedUser = tryReadLocalStorageItem(USER_KEY)
  if (!storedUser.value) {
    if (storedUser.available) {
      inMemorySessionUser = null
      inMemorySessionUserWriteFailed = false
    }
    return null
  }
  try {
    const user = normalizeStoredUser(JSON.parse(storedUser.value) as StoredUserPayload)
    inMemorySessionUser = user
    inMemorySessionUserWriteFailed = false
    return user
  } catch {
    removeLocalStorageItem(USER_KEY)
    inMemorySessionUser = null
    inMemorySessionUserWriteFailed = false
    return null
  }
}

function getKnownSessionUser(): User | null {
  return inMemorySessionUser ?? getStoredUser()
}

function hasStorageSnapshotChanged(
  snapshot: LocalStorageReadResult | null,
  current: LocalStorageReadResult,
): boolean {
  if (!current.available) {
    return false
  }
  if (!snapshot?.available) {
    return current.value !== null
  }
  return current.value !== snapshot.value
}

function getPasswordChangeScopeUser(): User | null {
  if (!inMemorySessionUserWriteFailed) {
    return getStoredUser() ?? inMemorySessionUser
  }
  if (!inMemorySessionUser) {
    return null
  }

  const storedUser = tryReadLocalStorageItem(USER_KEY)
  const crossTabSync = tryReadLocalStorageItem(AUTH_CROSS_TAB_SYNC_KEY)
  if (
    hasStorageSnapshotChanged(inMemorySessionUserWriteFailureUserSnapshot, storedUser)
    || hasStorageSnapshotChanged(inMemorySessionUserWriteFailureSyncSnapshot, crossTabSync)
  ) {
    return null
  }
  return inMemorySessionUser
}

interface PasswordChangeScopeObservation {
  crossTabSync: LocalStorageReadResult
  transitionSequence: number
}

function getCrossTabScopeUserId(value: string | null): string | undefined {
  if (!value) {
    return undefined
  }
  try {
    const signal: unknown = JSON.parse(value)
    if (!isRecord(signal) || signal.version !== 1) {
      return undefined
    }
    if (signal.type === 'cleared' || signal.type === 'session_updated') {
      return isCanonicalNonEmptyString(signal.user_id) ? signal.user_id : undefined
    }
  } catch {
    // Invalid synchronization records cannot identify an authentication scope.
  }
  return undefined
}

function doObservedCrossTabScopesMatch(
  expectedUserId: string,
  observation: PasswordChangeScopeObservation,
): boolean {
  if (authCrossTabScopeTransitionSequence === observation.transitionSequence) {
    return true
  }

  const transitions = authCrossTabScopeTransitions.filter(
    (transition) => transition.sequence > observation.transitionSequence,
  )
  if (
    transitions.length === 0
    || transitions[0]?.sequence !== observation.transitionSequence + 1
    || transitions.at(-1)?.sequence !== authCrossTabScopeTransitionSequence
  ) {
    return false
  }

  return transitions.every((transition) => transition.userId === expectedUserId)
}

function getPasswordChangeObservedUserId(
  expectedUserId: string,
  observation: PasswordChangeScopeObservation,
): string | null {
  if (!doObservedCrossTabScopesMatch(expectedUserId, observation)) {
    return null
  }

  const crossTabSync = tryReadLocalStorageItem(AUTH_CROSS_TAB_SYNC_KEY)
  if (observation.crossTabSync.available && !crossTabSync.available) {
    return null
  }
  if (hasStorageSnapshotChanged(observation.crossTabSync, crossTabSync)) {
    if (getCrossTabScopeUserId(crossTabSync.value) !== expectedUserId) {
      return null
    }
  }

  if (!inMemorySessionUserWriteFailed) {
    return (getStoredUser() ?? inMemorySessionUser)?.id === expectedUserId
      ? expectedUserId
      : null
  }
  if (!inMemorySessionUser) {
    return null
  }

  const storedUser = tryReadLocalStorageItem(USER_KEY)
  if (!hasStorageSnapshotChanged(inMemorySessionUserWriteFailureUserSnapshot, storedUser)) {
    return inMemorySessionUser.id === expectedUserId ? expectedUserId : null
  }
  if (!storedUser.available || !storedUser.value) {
    return null
  }

  try {
    const observedUser = normalizeStoredUser(JSON.parse(storedUser.value) as StoredUserPayload)
    return observedUser.id === inMemorySessionUser.id && observedUser.id === expectedUserId
      ? expectedUserId
      : null
  } catch {
    return null
  }
}

function assertPasswordChangeAccountCurrent(
  expectedUserId: string,
  observation: PasswordChangeScopeObservation,
): void {
  if (getPasswordChangeObservedUserId(expectedUserId, observation) !== expectedUserId) {
    throw createSupersededAuthError()
  }
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
  const storedUserBeforeWrite = tryReadLocalStorageItem(USER_KEY)
  const crossTabSyncBeforeWrite = tryReadLocalStorageItem(AUTH_CROSS_TAB_SYNC_KEY)
  inMemorySessionUserWriteFailed = !writeLocalStorageItem(USER_KEY, JSON.stringify(user))
  inMemorySessionUserWriteFailureUserSnapshot = inMemorySessionUserWriteFailed
    ? storedUserBeforeWrite
    : null
  inMemorySessionUserWriteFailureSyncSnapshot = inMemorySessionUserWriteFailed
    ? crossTabSyncBeforeWrite
    : null
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

function publishAuthCrossTabSync(
  type: 'session_updated' | 'cleared',
  reason?: AuthClearedDetail['reason'],
  scopeUserId?: string,
): void {
  authCrossTabSyncSequence += 1
  const signal = {
    version: 1,
    type,
    ...(reason ? { reason } : {}),
    ...(scopeUserId ? { user_id: scopeUserId } : {}),
    nonce: `${Date.now()}-${authCrossTabSyncSequence}-${Math.random().toString(36).slice(2)}`,
    source_id: AUTH_CROSS_TAB_SOURCE_ID,
  }
  const serializedSignal = JSON.stringify(signal)
  const signalWasStored = writeLocalStorageItem(AUTH_CROSS_TAB_SYNC_KEY, serializedSignal)
  if (type === 'session_updated' && inMemorySessionUserWriteFailed && signalWasStored) {
    inMemorySessionUserWriteFailureSyncSnapshot = { available: true, value: serializedSignal }
  }
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
  publishAuthCrossTabSync('session_updated', undefined, user.id)
}

export function clearTokens(detail?: AuthClearedDetail): void {
  const clearedUserId = getKnownSessionUser()?.id
  invalidateAuthSessionRequests()
  inMemorySessionUser = null
  inMemorySessionUserWriteFailed = false
  inMemorySessionUserWriteFailureUserSnapshot = null
  inMemorySessionUserWriteFailureSyncSnapshot = null
  clearLegacyTokenStorage()
  removeLocalStorageItem(USER_KEY)
  removeLocalStorageItem(SESSION_MARKER_KEY)
  isDownloadSessionReady = true
  publishAuthCrossTabSync('cleared', detail?.reason, clearedUserId)

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
  authCleared: boolean
  user?: User
  downloadSession?: DownloadSessionResult
}

interface RefreshSessionResult {
  refreshed: boolean
  passwordChangeRequired: boolean
  authCleared: boolean
  downloadSession?: DownloadSessionResult
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

const DOWNLOAD_SESSION_REFRESHABLE_AUTH_CODES = new Set([
  'INVALID_TOKEN',
  'TOKEN_EXPIRED',
  'TOKEN_REVOKED',
])

type DownloadSessionUnauthorizedHandling = 'clear' | 'preserve-refreshable'

function assertRequestSignalActive(signal?: AbortSignal): void {
  if (!signal?.aborted) {
    return
  }
  throw signal.reason ?? new DOMException('The operation was aborted', 'AbortError')
}

async function syncDownloadSession(
  options: AuthRequestOptions = {},
  force = false,
  generation = authSessionGeneration,
  unauthorizedHandling: DownloadSessionUnauthorizedHandling = 'clear',
): Promise<DownloadSessionResult> {
  const hadBrowserSessionState = force || hasBrowserSessionState()
  clearLegacyTokenStorage()
  assertRequestSignalActive(options.signal)
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
    assertRequestSignalActive(options.signal)

    if (!response.ok) {
      let message = getDownloadSessionSyncMessage()
      const error = await readAuthApiError(response)
      assertAuthGenerationCurrent(generation)
      assertRequestSignalActive(options.signal)

      if (response.status === 401) {
        message = error?.code === 'USER_NOT_FOUND'
          ? getMissingUserMessage(error.message)
          : error?.code === 'MISSING_AUTH_HEADER' || error?.code === 'NOT_AUTHENTICATED'
            ? getMissingBrowserSessionMessage()
            : '登录已过期，请重新登录'
        const preserveForRefresh = unauthorizedHandling === 'preserve-refreshable'
          && error?.code !== undefined
          && DOWNLOAD_SESSION_REFRESHABLE_AUTH_CODES.has(error.code)
        if (!preserveForRefresh) {
          clearTokens({
            reason: error?.code === 'USER_NOT_FOUND' ? 'missing' : 'expired',
            message,
          })
        }
        return {
          ok: false,
          authCleared: !preserveForRefresh,
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
    if (!isAuthGenerationCurrent(generation)) {
      throw createSupersededAuthError()
    }
    if (options.signal?.aborted) {
      assertRequestSignalActive(options.signal)
    }
    if (isAbortError(error)) {
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
  assertAuthGenerationCurrent(generation)
  assertRequestSignalActive(options.signal)
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

  const generation = authSessionGeneration
  const signal = options.signal
  const existingOperation = downloadSessionOperations.get(signal)
  if (existingOperation && existingOperation.generation === generation) {
    return existingOperation.promise
  }

  const controller = new AbortController()
  const unlinkCallerSignal = linkAbortSignal(signal, controller)
  const operation: DownloadSessionOperation = {
    generation,
    controller,
    promise: Promise.resolve({ ok: false }),
  }
  operation.promise = (async () => {
    const initialSync = await syncDownloadSession(
      { signal: controller.signal },
      false,
      generation,
      'preserve-refreshable',
    )
    if (initialSync.ok || initialSync.status !== 401 || initialSync.authCleared) {
      return initialSync
    }

    assertAuthGenerationCurrent(generation)
    assertRequestSignalActive(signal)
    const refreshResult = await waitForAuthRefresh(tryRefreshToken(true), signal)
    if (refreshResult.authCleared) {
      return refreshResult.downloadSession ?? { ...initialSync, authCleared: true }
    }
    assertAuthGenerationCurrent(generation)
    if (controller.signal.aborted) {
      throw new DOMException('The operation was aborted', 'AbortError')
    }
    if (!refreshResult.refreshed || refreshResult.passwordChangeRequired) {
      return { ...initialSync, authCleared: false }
    }

    return refreshResult.downloadSession ?? (isDownloadSessionReady
      ? { ok: true }
      : { ok: false, message: getDownloadSessionSyncMessage() })
  })().finally(() => {
    unlinkCallerSignal()
    if (downloadSessionOperations.get(signal) === operation) {
      downloadSessionOperations.delete(signal)
    }
  })
  downloadSessionOperations.set(signal, operation)
  return operation.promise
}

function linkAbortSignal(source: AbortSignal | undefined, controller: AbortController): () => void {
  if (!source) {
    return () => undefined
  }
  if (source.aborted) {
    controller.abort(source.reason)
    return () => undefined
  }

  const abort = () => controller.abort(source.reason)
  source.addEventListener('abort', abort, { once: true })
  return () => source.removeEventListener('abort', abort)
}

function waitForAuthRefresh(
  refresh: Promise<RefreshSessionResult>,
  signal?: AbortSignal,
): Promise<RefreshSessionResult> {
  if (!signal) {
    return refresh
  }
  if (signal.aborted) {
    return Promise.reject(new DOMException('The operation was aborted', 'AbortError'))
  }

  return new Promise((resolve, reject) => {
    const abort = () => {
      reject(new DOMException('The operation was aborted', 'AbortError'))
    }
    signal.addEventListener('abort', abort, { once: true })
    void refresh.then(
      (result) => {
        signal.removeEventListener('abort', abort)
        resolve(result)
      },
      (error: unknown) => {
        signal.removeEventListener('abort', abort)
        reject(error)
      },
    )
  })
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

function failedRefreshResult(authCleared = false): RefreshSessionResult {
  return { refreshed: false, passwordChangeRequired: false, authCleared }
}

const DEFINITIVE_REFRESH_FAILURES = new Set([
  '400:MISSING_TOKEN',
  '401:INVALID_TOKEN',
  '401:TOKEN_EXPIRED',
  '401:TOKEN_REVOKED',
  '401:USER_NOT_FOUND',
  '403:USER_DISABLED',
])

function isDefinitiveRefreshFailure(status: number, code?: string): boolean {
  return code !== undefined && DEFINITIVE_REFRESH_FAILURES.has(`${status}:${code}`)
}

function clearStoredAuthAfterDefinitiveRefreshFailure(hadAuthState: boolean): boolean {
  if (!hadAuthState || !hasStoredAuthState()) {
    return false
  }
  clearTokens()
  return true
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
      if (recovery.authCleared) {
        return {
          ...failedRefreshResult(true),
          ...(recovery.downloadSession ? { downloadSession: recovery.downloadSession } : {}),
        }
      }
      if (!isCurrent()) {
        return failedRefreshResult()
      }
      if (recovery.recovered) {
        return {
          refreshed: true,
          passwordChangeRequired: recovery.passwordChangeRequired,
          authCleared: false,
          ...(recovery.downloadSession ? { downloadSession: recovery.downloadSession } : {}),
        }
      }
      if (!recovery.terminal) {
        return failedRefreshResult()
      }
      return failedRefreshResult()
    }
    if (isDefinitiveRefreshFailure(response.status, error?.code)) {
      return failedRefreshResult(clearStoredAuthAfterDefinitiveRefreshFailure(hadAuthState))
    }
    return failedRefreshResult()
  }

  let body: AuthApiResponse<RefreshResponse>
  try {
    body = await response.json()
  } catch {
    return failedRefreshResult()
  }
  if (!isCurrent()) {
    return failedRefreshResult()
  }
  let data: AuthSessionData
  try {
    data = parseAuthSessionData(readAuthSuccessData(body))
  } catch {
    return failedRefreshResult()
  }
  storeSessionUser(data.user)
  notifyAuthSessionUpdated(data.user)
  const downloadSession = await syncDownloadSessionForUser(data.user, { signal }, true, generation)
  if (downloadSession.authCleared) {
    return {
      refreshed: false,
      passwordChangeRequired: false,
      authCleared: true,
      downloadSession,
    }
  }
  if (!isCurrent()) {
    return failedRefreshResult()
  }
  return {
    refreshed: !downloadSession.authCleared,
    passwordChangeRequired: data.user.mustChangePassword && !downloadSession.authCleared,
    authCleared: Boolean(downloadSession.authCleared),
    downloadSession,
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
  if (downloadSession.authCleared) {
    return {
      refreshed: false,
      passwordChangeRequired: false,
      authCleared: true,
      downloadSession,
    }
  }
  if (!isAuthGenerationCurrent(generation) || signal.aborted) {
    return failedRefreshResult()
  }
  return {
    refreshed: !downloadSession.authCleared,
    passwordChangeRequired: user.mustChangePassword && !downloadSession.authCleared,
    authCleared: Boolean(downloadSession.authCleared),
    downloadSession,
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

  operation.promise = (async () => {
    try {
      const lockManager = getAuthRefreshLockManager()
      return lockManager
        ? await performCoordinatedRefresh(lockManager, generation, controller.signal, hadAuthState)
        : await performRefreshRequest(generation, controller.signal, hadAuthState)
    } catch {
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
      return { recovered: false, terminal: false, passwordChangeRequired: false, authCleared: false }
    }

    if (!response.ok) {
      if (response.status === 401 || response.status === 403) {
        const unauthorizedCleared = await handleUnauthorizedSessionResponse(response, generation)
        const forbiddenCleared = await handleForbiddenSessionResponse(response, generation)
        return {
          recovered: false,
          terminal: true,
          passwordChangeRequired: false,
          authCleared: unauthorizedCleared || forbiddenCleared,
        }
      }
      return { recovered: false, terminal: false, passwordChangeRequired: false, authCleared: false }
    }

    const body: AuthApiResponse<{ user: ApiUser }> = await response.json()
    if (!isCurrent()) {
      return { recovered: false, terminal: false, passwordChangeRequired: false, authCleared: false }
    }
    const data = readAuthSuccessData(body)
    const user = normalizeUser(data.user)
    storeSessionUser(user)
    notifyAuthSessionUpdated(user)

    const downloadSession = await syncDownloadSessionForUser(user, { signal }, true, generation)
    if (downloadSession.authCleared) {
      return {
        recovered: false,
        terminal: true,
        passwordChangeRequired: false,
        authCleared: true,
        downloadSession,
      }
    }
    if (!isCurrent()) {
      return { recovered: false, terminal: false, passwordChangeRequired: false, authCleared: false }
    }
    return {
      recovered: !downloadSession.authCleared,
      terminal: Boolean(downloadSession.authCleared),
      passwordChangeRequired: user.mustChangePassword && !downloadSession.authCleared,
      authCleared: Boolean(downloadSession.authCleared),
      user,
      downloadSession,
    }
  } catch {
    return { recovered: false, terminal: false, passwordChangeRequired: false, authCleared: false }
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
  publishAuthCrossTabSync('session_updated', undefined, data.user.id)
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
  options: PasswordChangeRequestOptions,
): Promise<AuthActionResult> {
  if (!options?.expectedUserId || getPasswordChangeScopeUser()?.id !== options.expectedUserId) {
    throw new AuthError('登录会话已发生变化，请重新打开账户安全页面', 409, 'AUTH_SCOPE_CHANGED')
  }

  const scopeObservation: PasswordChangeScopeObservation = {
    crossTabSync: tryReadLocalStorageItem(AUTH_CROSS_TAB_SYNC_KEY),
    transitionSequence: authCrossTabScopeTransitionSequence,
  }
  invalidateAuthSessionRequests()
  let response: Response
  try {
    response = await fetch(`${API_BASE}/auth/password`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      credentials: 'same-origin',
      body: JSON.stringify({
        ...request,
        expected_user_id: options.expectedUserId,
      }),
      ...(options.signal ? { signal: options.signal } : {}),
    })
  } catch {
    if (getPasswordChangeObservedUserId(options.expectedUserId, scopeObservation) !== options.expectedUserId) {
      throw createSupersededAuthError()
    }
    clearTokens({
      reason: 'password_change_unconfirmed',
      message: PASSWORD_CHANGE_UNCONFIRMED_MESSAGE,
    })
    throw new AuthError(PASSWORD_CHANGE_UNCONFIRMED_MESSAGE, 0, 'PASSWORD_CHANGE_UNCONFIRMED')
  }
  assertPasswordChangeAccountCurrent(options.expectedUserId, scopeObservation)

  if (!response.ok) {
    let message = '修改密码失败'
    let code: string | undefined
    const error = await readAuthApiError(response, message)
    assertPasswordChangeAccountCurrent(options.expectedUserId, scopeObservation)
    if (error?.message) message = error.message
    if (error?.code) code = error.code

    if (!isDefinitivePasswordChangeFailure(response.status, code)) {
      clearTokens({
        reason: 'password_change_unconfirmed',
        message: PASSWORD_CHANGE_UNCONFIRMED_MESSAGE,
      })
      throw new AuthError(
        PASSWORD_CHANGE_UNCONFIRMED_MESSAGE,
        response.status,
        'PASSWORD_CHANGE_UNCONFIRMED',
      )
    }

    if (response.status === 401 && code === 'USER_NOT_FOUND') {
      clearTokens({ reason: 'missing', message: getMissingUserMessage(message) })
    } else if (response.status === 401 && code && TERMINAL_PASSWORD_CHANGE_AUTH_CODES.has(code)) {
      clearTokens({ reason: 'expired', message: '登录已过期，请重新登录' })
    } else if (response.status === 403 && code === 'USER_DISABLED') {
      clearTokens({ reason: 'disabled', message: getSessionEndedMessage(message) })
    }
    throw new AuthError(message, response.status, code)
  }

  let body: AuthApiResponse<null | { warning: true }>
  try {
    const value: unknown = await response.json()
    assertPasswordChangeAccountCurrent(options.expectedUserId, scopeObservation)
    if (!isPasswordChangeSuccessResponse(value)) {
      throw new Error(INVALID_PASSWORD_CHANGE_RESPONSE_MESSAGE)
    }
    body = value
  } catch (error) {
    if (getPasswordChangeObservedUserId(options.expectedUserId, scopeObservation) !== options.expectedUserId) {
      throw createSupersededAuthError()
    }
    if (isAbortError(error)) {
      clearTokens({
        reason: 'password_change_unconfirmed',
        message: PASSWORD_CHANGE_UNCONFIRMED_MESSAGE,
      })
      throw new AuthError(PASSWORD_CHANGE_UNCONFIRMED_MESSAGE, 0, 'PASSWORD_CHANGE_UNCONFIRMED')
    }
    clearTokens({
      reason: 'password_change_unconfirmed',
      message: PASSWORD_CHANGE_UNCONFIRMED_MESSAGE,
    })
    throw new AuthError(INVALID_PASSWORD_CHANGE_RESPONSE_MESSAGE, response.status)
  }

  const result = getAuthActionResult(response, body)
  clearTokens({ reason: result.warning ? 'password_change_warning' : 'password_changed' })
  return result
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
