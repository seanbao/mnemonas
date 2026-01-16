import { authFetch } from './auth'
import { readDownloadJsonErrorDetails, triggerBrowserDownload } from '@/lib/downloadResponse'
import { INVALID_API_RESPONSE_MESSAGE } from '@/lib/apiMessages'
import { getNonBlankJsonString, readStructuredJsonErrorDetails } from '@/lib/jsonErrorResponse'
import { copyTextToClipboard, encodePathForUrl, ensureZipExtension, getFilenameFromContentDisposition, hasControlCharacter, normalizePath } from '@/lib/utils'

const API_BASE = '/api/v1'
const PUBLIC_SHARE_API_BASE = `${API_BASE}/public/shares`

// Share types
export type ShareType = 'file' | 'folder'
export type Permission = 'read'

export interface Share {
  id: string
  path: string
  type: ShareType
  created_by: string
  created_at: string
  expires_at?: string | null
  last_access?: string | null
  has_password: boolean
  permission: Permission
  enabled: boolean
  access_count: number
  max_access?: number
  description?: string
  url: string
  risk?: ShareRisk
}

export type ShareRiskLevel = 'none' | 'low' | 'medium' | 'high'

export interface ShareRiskReason {
  code: string
  level: ShareRiskLevel
  message: string
  resolved?: boolean
}

export interface ShareRisk {
  level: ShareRiskLevel
  reasons?: ShareRiskReason[]
}

export interface SharePolicy {
  default_expires_in: string
  default_max_access: number
  policy_rules?: SharePolicyRule[]
}

export interface SharePolicyRule {
  path: string
  require_password?: boolean
  max_expires_in?: string
  max_access?: number
  allowed_users?: string[]
  allowed_groups?: string[]
  allowed_roles?: Array<'admin' | 'user' | 'guest'>
}

export interface CreateShareRequest {
  path: string
  type?: ShareType
  expires_in?: string  // e.g., "7d", "24h", "30m"
  password?: string
  permission?: Permission
  max_access?: number
  description?: string
}

export interface UpdateShareRequest {
  enabled?: boolean
  expires_in?: string
  password?: string
  permission?: Permission
  max_access?: number
  description?: string
}

export interface PublicShareInfo {
  id: string
  type: ShareType
  has_password: boolean
  permission: Permission
  description?: string
  file_name?: string
  file_size?: number
  folder_items?: number
}

export interface PublicShareItem {
  name: string
  path: string
  is_dir: boolean
  size: number
  mod_time: string
}

export interface PublicShareItemsResponse {
  path: string
  items: PublicShareItem[]
}

export interface ShareActionResult {
  warning: boolean
  message?: string
}

export type ShareCreateResult = Share & ShareActionResult
export type ShareUpdateResult = Share & ShareActionResult

export interface ShareRequestOptions {
  signal?: AbortSignal
}

export interface PublicShareRequestOptions {
  signal?: AbortSignal
}

export interface PublicShareItemsOptions extends PublicShareRequestOptions {
  path?: string
}

export interface PublicShareDownloadOptions extends PublicShareRequestOptions {
  filePath?: string
  filename?: string
  archive?: 'zip'
}

export class ShareError extends Error {
  status: number
  code?: string
  
  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = 'ShareError'
    this.status = status
    this.code = code
  }
  
  get isNotFound(): boolean {
    return this.status === 404
  }

  get isDisabled(): boolean {
    return this.code === 'SHARE_DISABLED'
  }

  get isAccessLimitReached(): boolean {
    return this.code === 'SHARE_ACCESS_LIMIT_REACHED'
  }
  
  get isExpired(): boolean {
    return this.code === 'SHARE_EXPIRED' || (this.status === 410 && !this.code)
  }
  
  get isUnauthorized(): boolean {
    return this.status === 401
  }

  get isRateLimited(): boolean {
    return this.status === 429
  }

  get isFeatureDisabled(): boolean {
    return this.code === 'SHARE_FEATURE_DISABLED'
  }

  get isPolicyPasswordRequired(): boolean {
    return this.code === 'SHARE_POLICY_PASSWORD_REQUIRED'
  }

  get isPolicyPrincipalForbidden(): boolean {
    return this.code === 'SHARE_POLICY_PRINCIPAL_FORBIDDEN'
  }

  get isUnavailable(): boolean {
    return this.status === 503 && !this.isFeatureDisabled
  }
}

interface ShareApiError {
  code?: string
  message: string
}

interface ShareApiResponse<T> {
  success: boolean
  data?: T
  warning?: boolean
  message?: string
  error?: ShareApiError | string
}

type ShareErrorBody = ShareApiResponse<never> | {
  code?: string
  error?: ShareApiError | string
  message?: string
  success?: boolean
}

const localizedPublicShareErrorMessages: Record<string, string> = {
  INVALID_PASSWORD: '密码错误',
  SHARE_PASSWORD_RATE_LIMITED: '尝试次数过多，请稍后再试',
  SHARE_FEATURE_DISABLED: '分享功能已关闭',
  SHARE_POLICY_PRINCIPAL_FORBIDDEN: '当前账号不允许为该路径创建或维护分享',
  SHARE_NOT_FOUND: '分享不存在或已失效',
  SHARE_DISABLED: '分享已停用',
  SHARE_ACCESS_LIMIT_REACHED: '分享访问次数已用尽',
  SHARE_EXPIRED: '分享已过期',
  FILE_NOT_FOUND: '分享文件不存在或已被移除',
  FILESYSTEM_UNAVAILABLE: '分享内容暂不可用',
  INVALID_SHARE_TYPE: '分享类型不支持',
  INVALID_ARCHIVE_FORMAT: '归档格式不受支持',
  ARCHIVE_TOO_MANY_ENTRIES: '归档包含的条目过多',
  ARCHIVE_TOO_LARGE: '归档内容过大',
  ARCHIVE_DUPLICATE_ENTRY: '归档条目名称冲突，请刷新后重试',
  ARCHIVE_ENTRY_CHANGED: '分享内容已变更，请刷新后重试',
  DOWNLOAD_SHARE_ARCHIVE_FAILED: '生成分享归档失败，请稍后重试',
}

function isShareType(value: unknown): value is ShareType {
  return value === 'file' || value === 'folder'
}

function isPermission(value: unknown): value is Permission {
  return value === 'read'
}

function isShareRiskLevel(value: unknown): value is ShareRiskLevel {
  return value === 'none' || value === 'low' || value === 'medium' || value === 'high'
}

function isShareRiskReason(value: unknown): value is ShareRiskReason {
  if (!value || typeof value !== 'object') {
    return false
  }
  const reason = value as Partial<ShareRiskReason>
  return typeof reason.code === 'string'
    && isShareRiskLevel(reason.level)
    && typeof reason.message === 'string'
    && (reason.resolved === undefined || typeof reason.resolved === 'boolean')
}

function isShareRisk(value: unknown): value is ShareRisk {
  if (!value || typeof value !== 'object') {
    return false
  }
  const risk = value as Partial<ShareRisk>
  return isShareRiskLevel(risk.level)
    && (risk.reasons === undefined || (Array.isArray(risk.reasons) && risk.reasons.every(isShareRiskReason)))
}

function isNonNegativeSafeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}

function validateShareMaxAccessRequest(maxAccess: unknown): void {
  if (maxAccess !== undefined && !isNonNegativeSafeInteger(maxAccess)) {
    throw new ShareError('访问次数必须是 0 或不超过安全范围的正整数', 0, 'INVALID_MAX_ACCESS')
  }
}

function normalizeShareLogicalPath(path: string): string {
  try {
    return normalizePath(path)
  } catch {
    throw new ShareError('分享路径无效', 0, 'INVALID_SHARE_PATH')
  }
}

function isLogicalPathString(value: unknown): value is string {
  if (typeof value !== 'string' || value.length === 0) {
    return false
  }

  try {
    return normalizePath(value) === value
  } catch {
    return false
  }
}

function isCanonicalPublicShareRelativePathString(value: unknown, options: { allowEmpty?: boolean } = {}): value is string {
  if (typeof value !== 'string' || (!options.allowEmpty && value.length === 0)) {
    return false
  }

  try {
    return normalizePublicShareRelativePath(value) === value
  } catch {
    return false
  }
}

function isSafePublicShareName(value: unknown): value is string {
  if (typeof value !== 'string') {
    return false
  }

  const trimmed = value.trim()
  if (trimmed.length === 0 || trimmed === '.' || trimmed === '..') {
    return false
  }

  if (value.includes('/') || value.includes('\\')) {
    return false
  }

  if (hasControlCharacter(value)) {
    return false
  }

  return true
}

function isSharePolicy(value: unknown): value is SharePolicy {
  if (!value || typeof value !== 'object') {
    return false
  }
  const policy = value as Partial<SharePolicy>
  return typeof policy.default_expires_in === 'string'
    && isNonNegativeSafeInteger(policy.default_max_access)
    && (policy.policy_rules === undefined || (Array.isArray(policy.policy_rules) && policy.policy_rules.every(isSharePolicyRule)))
}

function isSharePolicyRule(value: unknown): value is SharePolicyRule {
  if (!value || typeof value !== 'object') {
    return false
  }
  const rule = value as Partial<SharePolicyRule>
  return isLogicalPathString(rule.path)
    && (rule.require_password === undefined || typeof rule.require_password === 'boolean')
    && (rule.max_expires_in === undefined || typeof rule.max_expires_in === 'string')
    && (rule.max_access === undefined || isNonNegativeSafeInteger(rule.max_access))
    && (rule.allowed_users === undefined || (Array.isArray(rule.allowed_users) && rule.allowed_users.every((value) => typeof value === 'string')))
    && (rule.allowed_groups === undefined || (Array.isArray(rule.allowed_groups) && rule.allowed_groups.every((value) => typeof value === 'string')))
    && (rule.allowed_roles === undefined || (Array.isArray(rule.allowed_roles) && rule.allowed_roles.every((value) => value === 'admin' || value === 'user' || value === 'guest')))
}

function isValidShare(value: unknown): value is Share {
  if (!value || typeof value !== 'object') {
    return false
  }

  const share = value as Partial<Share>
  return (
    typeof share.id === 'string' &&
    isLogicalPathString(share.path) &&
    isShareType(share.type) &&
    typeof share.created_by === 'string' &&
    typeof share.created_at === 'string' &&
    typeof share.has_password === 'boolean' &&
    isPermission(share.permission) &&
    typeof share.enabled === 'boolean' &&
    isNonNegativeSafeInteger(share.access_count) &&
    typeof share.url === 'string' &&
    (share.expires_at === undefined || share.expires_at === null || typeof share.expires_at === 'string') &&
    (share.last_access === undefined || share.last_access === null || typeof share.last_access === 'string') &&
    (share.max_access === undefined || isNonNegativeSafeInteger(share.max_access)) &&
    (share.description === undefined || typeof share.description === 'string') &&
    (share.risk === undefined || isShareRisk(share.risk))
  )
}

function isValidPublicShareInfo(value: unknown): value is PublicShareInfo {
  if (!value || typeof value !== 'object') {
    return false
  }

  const share = value as Partial<PublicShareInfo>
  return (
    typeof share.id === 'string' &&
    isShareType(share.type) &&
    typeof share.has_password === 'boolean' &&
    isPermission(share.permission) &&
    (share.description === undefined || typeof share.description === 'string') &&
    (share.file_name === undefined || isSafePublicShareName(share.file_name)) &&
    (share.file_size === undefined || isNonNegativeSafeInteger(share.file_size)) &&
    (share.folder_items === undefined || isNonNegativeSafeInteger(share.folder_items))
  )
}

function isValidPublicShareItem(value: unknown): value is PublicShareItem {
  if (!value || typeof value !== 'object') {
    return false
  }

  const item = value as Partial<PublicShareItem>
  return (
    isSafePublicShareName(item.name) &&
    isCanonicalPublicShareRelativePathString(item.path) &&
    typeof item.is_dir === 'boolean' &&
    isNonNegativeSafeInteger(item.size) &&
    typeof item.mod_time === 'string'
  )
}

function getShareErrorMessage(
  body: ShareErrorBody,
  fallback: string,
  options?: { localizeKnownCode?: boolean },
): string {
  const code = getShareErrorCode(body)
  if (options?.localizeKnownCode && code) {
    const localized = localizedPublicShareErrorMessages[code]
    if (localized) {
      return localized
    }
  }

  const stringError = getNonBlankJsonString(body.error)
  if (stringError !== undefined) {
    return stringError
  }
  if (body.error && typeof body.error === 'object' && 'message' in body.error) {
    const errorMessage = getNonBlankJsonString(body.error.message)
    if (errorMessage !== undefined) {
      return errorMessage
    }
  }
  const message = getNonBlankJsonString(body.message)
  if (message !== undefined) {
    return message
  }
  return fallback
}

function getShareErrorCode(body: ShareErrorBody): string | undefined {
  if (body.error && typeof body.error === 'object' && 'code' in body.error) {
    const errorCode = getNonBlankJsonString(body.error.code)
    if (errorCode !== undefined) {
      return errorCode
    }
  }

  if ('code' in body) {
    return getNonBlankJsonString(body.code)
  }

  return undefined
}

function getPublicShareErrorMessage(body: ShareErrorBody, fallback: string): string {
  return getShareErrorMessage(body, fallback, { localizeKnownCode: true })
}

function getPublicShareFetchOptions(signal?: AbortSignal): RequestInit {
  return signal ? { credentials: 'same-origin', signal } : { credentials: 'same-origin' }
}

async function readPublicShareApiError(response: Response, fallback: string): Promise<ShareError> {
  const structuredError = await readStructuredJsonErrorDetails(response, fallback, {
    localizeCode: (code) => localizedPublicShareErrorMessages[code],
  })
  if (structuredError) {
    return new ShareError(structuredError.message, response.status, structuredError.code)
  }

  let message = fallback
  let code: string | undefined
  try {
    const body = await response.json() as ShareErrorBody
    message = getPublicShareErrorMessage(body, message)
    code = getShareErrorCode(body)
  } catch {
    // Keep the fallback when the response body is unavailable.
  }

  return new ShareError(message, response.status, code)
}

async function readShareApiError(response: Response, fallback: string): Promise<ShareError> {
  const structuredError = await readStructuredJsonErrorDetails(response, fallback)
  if (structuredError) {
    return new ShareError(structuredError.message, response.status, structuredError.code)
  }

  let message = fallback
  let code: string | undefined

  try {
    const body = await response.json() as ShareErrorBody
    message = getShareErrorMessage(body, message)
    code = getShareErrorCode(body)
  } catch {
    // Keep the fallback when the response body is unavailable.
  }

  return new ShareError(message, response.status, code)
}

async function throwShareDownloadJsonError(response: Response): Promise<void> {
  const details = await readDownloadJsonErrorDetails(response, '下载分享文件失败', {
    localizeCode: (code) => localizedPublicShareErrorMessages[code],
  })
  if (!details) {
    return
  }

  throw new ShareError(details.message, response.status, details.code)
}

function normalizePublicShareRelativePath(filePath: string): string {
  if (filePath.includes('\\') || hasControlCharacter(filePath)) {
    throw new Error('非法路径')
  }
  return normalizePath(filePath).split('/').filter(Boolean).join('/')
}

function normalizePublicShareDownloadPath(filePath: string): string {
  const normalizedPath = normalizePublicShareRelativePath(filePath)
  if (!normalizedPath) {
    throw new Error('非法路径')
  }
  return normalizedPath
}

function encodeShareIdForUrl(id: string): string {
  return encodeURIComponent(id)
}

function authenticatedShareUrl(id: string): string {
  return `${API_BASE}/shares/${encodeShareIdForUrl(id)}`
}

function publicShareUrl(id: string): string {
  return `${PUBLIC_SHARE_API_BASE}/${encodeShareIdForUrl(id)}`
}

export function formatShareUrl(shareUrl: string, origin = window.location.origin): string {
  const trimmed = shareUrl.trim()
  try {
    const parsed = new URL(trimmed)
    if (parsed.protocol === 'http:' || parsed.protocol === 'https:') {
      if (parsed.username || parsed.password) {
        parsed.username = ''
        parsed.password = ''
        return parsed.toString()
      }
      return trimmed
    }
  } catch {
    // Relative share URLs are resolved against the current origin below.
  }

  const relativePath = trimmed.startsWith('/') ? trimmed : `/${trimmed}`
  const normalizedOrigin = origin.replace(/\/+$/, '')
  return `${normalizedOrigin}${relativePath}`
}

async function parseWrappedShareSuccess<T>(response: Response, invalidMessage: string): Promise<T> {
  let body: unknown
  try {
    body = await response.json()
  } catch {
    throw new ShareError(invalidMessage, response.status)
  }

  if (
    !body ||
    typeof body !== 'object' ||
    (body as ShareApiResponse<unknown>).success !== true ||
    !('data' in body)
  ) {
    throw new ShareError(invalidMessage, response.status)
  }

  return body as T
}

function getShareActionResult(response: Response, body: ShareApiResponse<unknown>): ShareActionResult {
  return {
    warning: response.headers?.get?.('Warning') != null
      || body.warning === true
      || (!!body.data && typeof body.data === 'object' && 'warning' in body.data && body.data.warning === true),
    message: getNonBlankJsonString(body.message),
  }
}

async function parsePublicShareSuccess<T>(response: Response, invalidMessage: string): Promise<T> {
  let body: unknown
  try {
    body = await response.json()
  } catch {
    throw new ShareError(invalidMessage, response.status)
  }

  if (!body || typeof body !== 'object') {
    throw new ShareError(invalidMessage, response.status)
  }

  return body as T
}

// === Authenticated Share APIs ===

/**
 * List shares for current user
 * @param all - If true and user is admin, list all shares
 */
export async function listShares(all = false, options: ShareRequestOptions = {}): Promise<Share[]> {
  const url = all ? `${API_BASE}/shares?all=true` : `${API_BASE}/shares`
  const response = await authFetch(url, options)
  
  if (!response.ok) {
    throw await readShareApiError(response, '获取分享列表失败')
  }

  const body = await parseWrappedShareSuccess<ShareApiResponse<unknown>>(response, INVALID_API_RESPONSE_MESSAGE)
  if (!Array.isArray(body.data) || !body.data.every(isValidShare)) {
    throw new ShareError(INVALID_API_RESPONSE_MESSAGE, response.status)
  }
  return body.data
}

export async function getSharePolicy(options: ShareRequestOptions = {}): Promise<SharePolicy> {
  const response = await authFetch(`${API_BASE}/shares/policy`, options)

  if (!response.ok) {
    throw await readShareApiError(response, '获取分享默认策略失败')
  }

  const body = await parseWrappedShareSuccess<ShareApiResponse<unknown>>(response, INVALID_API_RESPONSE_MESSAGE)
  if (!isSharePolicy(body.data)) {
    throw new ShareError(INVALID_API_RESPONSE_MESSAGE, response.status)
  }
  return body.data
}

/**
 * Create a new share
 */
export async function createShare(req: CreateShareRequest, options: ShareRequestOptions = {}): Promise<ShareCreateResult> {
  validateShareMaxAccessRequest(req.max_access)
  const request = {
    ...req,
    path: normalizeShareLogicalPath(req.path),
  }

  const response = await authFetch(`${API_BASE}/shares`, {
    ...options,
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  })
  
  if (!response.ok) {
    throw await readShareApiError(response, '创建分享失败')
  }

  const body = await parseWrappedShareSuccess<ShareApiResponse<unknown>>(response, INVALID_API_RESPONSE_MESSAGE)
  if (!isValidShare(body.data)) {
    throw new ShareError(INVALID_API_RESPONSE_MESSAGE, response.status)
  }
  return {
    ...body.data,
    ...getShareActionResult(response, body),
  }
}

/**
 * Get share details
 */
export async function getShare(id: string, options: ShareRequestOptions = {}): Promise<Share> {
  const response = await authFetch(authenticatedShareUrl(id), options)
  
  if (!response.ok) {
    throw await readShareApiError(response, '获取分享详情失败')
  }

  const body = await parseWrappedShareSuccess<ShareApiResponse<unknown>>(response, INVALID_API_RESPONSE_MESSAGE)
  if (!isValidShare(body.data)) {
    throw new ShareError(INVALID_API_RESPONSE_MESSAGE, response.status)
  }
  return body.data
}

/**
 * Update share
 */
export async function updateShare(id: string, req: UpdateShareRequest, options: ShareRequestOptions = {}): Promise<ShareUpdateResult> {
  validateShareMaxAccessRequest(req.max_access)

  const response = await authFetch(authenticatedShareUrl(id), {
    ...options,
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  
  if (!response.ok) {
    throw await readShareApiError(response, '更新分享失败')
  }

  const body = await parseWrappedShareSuccess<ShareApiResponse<unknown>>(response, INVALID_API_RESPONSE_MESSAGE)
  if (!isValidShare(body.data)) {
    throw new ShareError(INVALID_API_RESPONSE_MESSAGE, response.status)
  }
  return {
    ...body.data,
    ...getShareActionResult(response, body),
  }
}

/**
 * Delete share
 */
export async function deleteShare(id: string, options: ShareRequestOptions = {}): Promise<ShareActionResult> {
  const response = await authFetch(authenticatedShareUrl(id), {
    ...options,
    method: 'DELETE',
  })
  
  if (!response.ok) {
    throw await readShareApiError(response, '删除分享失败')
  }

  const body = await parseWrappedShareSuccess<ShareApiResponse<null>>(response, INVALID_API_RESPONSE_MESSAGE)
  return getShareActionResult(response, body)
}

// === Public Share APIs ===

/**
 * Get public share info (no auth required)
 */
export async function getPublicShare(id: string, options: PublicShareRequestOptions = {}): Promise<PublicShareInfo> {
  const response = await fetch(publicShareUrl(id), getPublicShareFetchOptions(options.signal))
  
  if (!response.ok) {
    let message = '分享不存在或已失效'
    if (response.status === 410) {
      message = '分享已过期、已禁用或访问次数已达上限'
    }
    throw await readPublicShareApiError(response, message)
  }
  
  const body = await parsePublicShareSuccess<unknown>(response, INVALID_API_RESPONSE_MESSAGE)
  if (!isValidPublicShareInfo(body)) {
    throw new ShareError(INVALID_API_RESPONSE_MESSAGE, response.status)
  }
  return body
}

/**
 * List items in a public shared folder
 */
export async function getPublicShareItems(
  id: string,
  options: PublicShareItemsOptions = {}
): Promise<PublicShareItemsResponse> {
  const params = new URLSearchParams()
  if (options?.path) {
    const normalizedPath = normalizePublicShareRelativePath(options.path)
    if (normalizedPath) {
      params.set('path', normalizedPath)
    }
  }
  const query = params.toString()
  const baseURL = `${publicShareUrl(id)}/items`
  const url = query ? `${baseURL}?${query}` : baseURL
  const response = await fetch(url, getPublicShareFetchOptions(options.signal))

  if (!response.ok) {
    let message = '获取分享文件夹失败'
    if (response.status === 410) {
      message = '分享已过期、已禁用或访问次数已达上限'
    } else if (response.status === 401) {
      message = '密码错误'
    } else if (response.status === 429) {
      message = '尝试次数过多，请稍后再试'
    }
    throw await readPublicShareApiError(response, message)
  }

  const body = await parsePublicShareSuccess<PublicShareItemsResponse>(response, INVALID_API_RESPONSE_MESSAGE)
  if (!isCanonicalPublicShareRelativePathString(body.path, { allowEmpty: true }) || !Array.isArray(body.items) || !body.items.every(isValidPublicShareItem)) {
    throw new ShareError(INVALID_API_RESPONSE_MESSAGE, response.status)
  }
  return body
}

/**
 * Access password-protected share
 */
export async function accessShareWithPassword(
  id: string,
  password: string,
  options: PublicShareRequestOptions = {},
): Promise<PublicShareInfo> {
  const response = await fetch(`${publicShareUrl(id)}/access`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ password }),
    ...getPublicShareFetchOptions(options.signal),
  })
  
  if (!response.ok) {
    let message = '访问失败'
    if (response.status === 401) {
      message = '密码错误'
    } else if (response.status === 410) {
      message = '分享已过期、已禁用或访问次数已达上限'
    } else if (response.status === 429) {
      message = '尝试次数过多，请稍后再试'
    }
    throw await readPublicShareApiError(response, message)
  }
  
  const body = await parsePublicShareSuccess<unknown>(response, INVALID_API_RESPONSE_MESSAGE)
  if (!isValidPublicShareInfo(body)) {
    throw new ShareError(INVALID_API_RESPONSE_MESSAGE, response.status)
  }
  return body
}

/**
 * Get download URL for shared file
 */
export function getShareDownloadUrl(id: string, options?: { archive?: 'zip' }): string {
  return withShareDownloadArchiveParam(`${publicShareUrl(id)}/download`, options?.archive)
}

/**
 * Get download URL for file in shared folder
 */
export function getShareFileDownloadUrl(id: string, filePath: string, options?: { archive?: 'zip' }): string {
  const normalizedPath = normalizePublicShareDownloadPath(filePath)
  const encodedPath = encodePathForUrl(`/${normalizedPath}`)
  const trimmedPath = encodedPath.startsWith('/') ? encodedPath.slice(1) : encodedPath
  return withShareDownloadArchiveParam(`${publicShareUrl(id)}/download/${trimmedPath}`, options?.archive)
}

function withShareDownloadArchiveParam(url: string, archive?: 'zip'): string {
  if (!archive) {
    return url
  }
  const params = new URLSearchParams({ archive })
  return `${url}?${params.toString()}`
}

export async function downloadShare(id: string, options: PublicShareDownloadOptions = {}): Promise<void> {
  const normalizedFilePath = options?.filePath ? normalizePublicShareDownloadPath(options.filePath) : undefined
  const url = normalizedFilePath
    ? getShareFileDownloadUrl(id, normalizedFilePath, { archive: options.archive })
    : getShareDownloadUrl(id, { archive: options?.archive })
  const response = await fetch(url, getPublicShareFetchOptions(options.signal))

  if (!response.ok) {
    let message = '下载分享文件失败'
    if (response.status === 401) {
      message = '访问凭证已失效，请重新输入密码'
    } else if (response.status === 410) {
      message = '分享已过期、已禁用或访问次数已达上限'
    } else if (response.status === 429) {
      message = '尝试次数过多，请稍后再试'
    }
    throw await readPublicShareApiError(response, message)
  }

  const pathFilename = normalizedFilePath ? normalizedFilePath.split('/').filter(Boolean).pop() : undefined
  const baseFilename = options?.filename ?? pathFilename ?? 'download'
  const fallbackFilename = options?.archive === 'zip' ? ensureZipExtension(baseFilename) : baseFilename
  const contentDisposition = response.headers.get('Content-Disposition')
  await throwShareDownloadJsonError(response)
  const filename = getFilenameFromContentDisposition(contentDisposition, fallbackFilename)
  const blob = await response.blob()
  triggerBrowserDownload(blob, filename)
}

// === Utility functions ===

/**
 * Copy share URL to clipboard
 */
export async function copyShareUrl(share: Share): Promise<void> {
  await copyTextToClipboard(formatShareUrl(share.url))
}

/**
 * Format expiration time
 */
export function formatExpiration(expiresAt?: string | null): string {
  if (!expiresAt) return '永不过期'
  
  const expires = new Date(expiresAt)
  if (Number.isNaN(expires.getTime())) {
    return '过期时间无效'
  }
  const now = new Date()
  const diff = expires.getTime() - now.getTime()
  
  if (diff <= 0) return '已过期'
  
  const days = Math.floor(diff / (1000 * 60 * 60 * 24))
  const hours = Math.floor((diff % (1000 * 60 * 60 * 24)) / (1000 * 60 * 60))
  
  if (days > 0) return `${days} 天后过期`
  if (hours > 0) return `${hours} 小时后过期`
  return '即将过期'
}

/**
 * Parse duration string to display
 */
export function formatDuration(duration: string): string {
  if (duration.endsWith('d')) {
    const days = parseInt(duration)
    if (Number.isNaN(days)) return duration
    return `${days} 天`
  }
  if (duration.endsWith('h')) {
    const hours = parseInt(duration)
    if (Number.isNaN(hours)) return duration
    return `${hours} 小时`
  }
  if (duration.endsWith('m')) {
    const mins = parseInt(duration)
    if (Number.isNaN(mins)) return duration
    return `${mins} 分钟`
  }
  return duration
}
