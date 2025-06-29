/**
 * Settings API
 * Admin-only endpoints for system configuration
 */

import { authFetch } from './auth'
import { INVALID_API_RESPONSE_MESSAGE as INVALID_SETTINGS_RESPONSE_MESSAGE } from '@/lib/apiMessages'
import { readStructuredJsonErrorDetails } from '@/lib/jsonErrorResponse'

const API_BASE = '/api/v1/settings'
const SETTINGS_ERROR_MESSAGES = {
  get: '获取设置失败',
  securityCheck: '获取安全检查失败',
  update: '更新设置失败',
  accessCheck: '检查目录访问失败',
  accessReport: '生成目录访问报告失败',
  accessPreview: '预览目录访问变更失败',
  webdavCredentials: '获取 WebDAV 凭据失败',
} as const

export interface SettingsRequestOptions {
  signal?: AbortSignal
}

export interface SettingsData {
  server: {
    host: string
    port: number
    read_timeout: string
    write_timeout: string
    idle_timeout: string
    trusted_proxy_hops: number
    trusted_proxy_cidrs?: string[]
    tls?: {
      enabled: boolean
      cert_file: string
      key_file: string
      auto_generate: boolean
      cert_dir: string
    }
  }
  storage: {
    root: string
    directory_quotas?: DirectoryQuota[]
    directory_access_rules?: DirectoryAccessRule[]
  }
  trash?: {
    enabled: boolean
    retention_days: number
    max_size: number
  }
  retention: {
    max_versions: number
    max_age: string
    min_free_space: number
    gc_interval: string
  }
  versioning?: {
    auto_versioned_extensions: string[]
    auto_versioned_filenames: string[]
    max_versioned_size: number
  }
  webdav: {
    enabled: boolean
    runtime_enabled?: boolean
    prefix: string
    read_only: boolean
    auth_type: string
    username: string
  }
  share: {
    enabled: boolean
    base_url: string
    default_expires_in?: string
    default_max_access?: number
    policy_rules?: SharePolicyRule[]
  }
  favorites?: {
    enabled: boolean
    runtime_available?: boolean
  }
  alerts?: {
    enabled: boolean
    check_interval: string
    threshold_pct: number
    critical_pct: number
    min_free_bytes: number
    cooldown_period: string
    webhook_url: string
    webhook_url_configured?: boolean
    webhook_method: string
    webhook_headers: string[]
    webhook_headers_configured?: boolean
    telegram_enabled?: boolean
    telegram_bot_token_configured?: boolean
    telegram_chat_id?: string
    email_enabled?: boolean
    smtp_host?: string
    smtp_port?: number
    smtp_username?: string
    smtp_password_configured?: boolean
    smtp_from?: string
    smtp_to?: string[]
  }
  disk_health?: {
    enabled: boolean
    check_interval: string
    probe_timeout: string
    cooldown_period: string
    command: string
    temperature_warning_c: number
    temperature_critical_c: number
    media_wear_warning_percent: number
    media_wear_critical_percent: number
    devices: DiskHealthDeviceSettings[]
  }
  maintenance?: {
    scrub?: {
      enabled: boolean
      schedule_interval: string
      retry_interval: string
      max_retries: number
    }
  }
  dataplane: {
    grpc_address: string
    timeout: string
    max_retries: number
  }
  cdc: {
    min_chunk_size: number
    avg_chunk_size: number
    max_chunk_size: number
  }
}

export interface DirectoryQuota {
  path: string
  quota_bytes: number
}

export type DirectoryAccessRole = 'admin' | 'user' | 'guest'

export interface DirectoryAccessRule {
  path: string
  read_users?: string[]
  write_users?: string[]
  read_groups?: string[]
  write_groups?: string[]
  read_roles?: DirectoryAccessRole[]
  write_roles?: DirectoryAccessRole[]
}

export interface SharePolicyRule {
  path: string
  require_password?: boolean
  max_expires_in?: string
  max_access?: number
}

export interface DiskHealthDeviceSettings {
  name?: string
  path: string
  type?: string
  serial?: string
  temperature_warning_c?: number
  temperature_critical_c?: number
}

export type DirectoryAccessDecisionSource =
  | 'auth_disabled'
  | 'admin'
  | 'user_disabled'
  | 'user_not_found'
  | 'directory_access_rule'
  | 'home_dir'
  | 'invalid_home_dir'

export interface DirectoryAccessDecision {
  mode: 'read' | 'write'
  allowed: boolean
  source: DirectoryAccessDecisionSource
  message?: string
  matched_rule?: DirectoryAccessRule
}

export interface DirectoryAccessCheckData {
  username: string
  user_id: string
  role: DirectoryAccessRole
  groups?: string[]
  home_dir: string
  path: string
  read: DirectoryAccessDecision
  write: DirectoryAccessDecision
}

export interface DirectoryAccessReportSummary {
  users: number
  read_allowed: number
  read_denied: number
  write_allowed: number
  write_denied: number
  related_shares: number
  active_related_shares: number
  password_protected_shares: number
}

export type DirectoryAccessShareRelation = 'exact' | 'covers_path' | 'inside_path'

export interface DirectoryAccessShareImpact {
  id: string
  path: string
  type: 'file' | 'folder'
  created_by: string
  relation: DirectoryAccessShareRelation
  enabled: boolean
  active: boolean
  has_password: boolean
  access_count: number
  max_access: number
  expires_at?: string
  url?: string
}

export interface DirectoryAccessReportData {
  path: string
  preview?: boolean
  summary: DirectoryAccessReportSummary
  users: DirectoryAccessCheckData[]
  shares?: DirectoryAccessShareImpact[]
}

export interface DirectoryAccessCheckRequest {
  username: string
  path: string
}

export interface DirectoryAccessReportRequest {
  path: string
}

export interface DirectoryAccessPreviewRequest extends DirectoryAccessReportRequest {
  directory_access_rules: DirectoryAccessRule[]
}

export interface SettingsResponse {
  success: boolean
  data: SettingsData
}

export type SecurityCheckStatus = 'pass' | 'warning' | 'block'

export interface SecurityCheckItem {
  id: string
  status: SecurityCheckStatus
  title: string
  message: string
  details?: Record<string, unknown>
}

export interface SecurityCheckData {
  status: SecurityCheckStatus
  generated_at: string
  checks: SecurityCheckItem[]
  request: Record<string, unknown>
  config: Record<string, unknown>
}

export interface SecurityCheckResponse {
  success: boolean
  data: SecurityCheckData
}

interface SettingsApiError {
  code?: string
  message: string
}

interface SettingsApiResponse<T> {
  success: boolean
  data?: T
  message?: string
  error?: SettingsApiError
}

export class SettingsError extends Error {
  status: number
  code?: string

  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = 'SettingsError'
    this.status = status
    this.code = code
  }

  get isUnavailable(): boolean {
    return this.status === 503 || this.code === 'SERVICE_UNAVAILABLE'
  }
}

export interface UpdateSettingsRequest {
  server?: {
    host?: string
    port?: number
    read_timeout?: string
    write_timeout?: string
    idle_timeout?: string
    trusted_proxy_hops?: number
    trusted_proxy_cidrs?: string[]
    tls?: {
      enabled?: boolean
      cert_file?: string
      key_file?: string
      auto_generate?: boolean
      cert_dir?: string
    }
  }
  storage?: {
    directory_quotas?: DirectoryQuota[]
    directory_access_rules?: DirectoryAccessRule[]
  }
  trash?: {
    enabled?: boolean
    retention_days?: number
    max_size?: number
  }
  retention?: {
    max_versions?: number
    max_age?: string
    min_free_space?: number
    gc_interval?: string
  }
  versioning?: {
    auto_versioned_extensions?: string[]
    auto_versioned_filenames?: string[]
    max_versioned_size?: number
  }
  dataplane?: {
    grpc_address?: string
    timeout?: string
    max_retries?: number
  }
  cdc?: {
    min_chunk_size?: number
    avg_chunk_size?: number
    max_chunk_size?: number
  }
  share?: {
    enabled?: boolean
    base_url?: string
    default_expires_in?: string
    default_max_access?: number
    policy_rules?: SharePolicyRule[]
  }
  favorites?: {
    enabled?: boolean
  }
  alerts?: {
    enabled?: boolean
    check_interval?: string
    threshold_pct?: number
    critical_pct?: number
    min_free_bytes?: number
    cooldown_period?: string
    webhook_url?: string
    webhook_method?: string
    webhook_headers?: string[]
    telegram_enabled?: boolean
    telegram_bot_token?: string
    telegram_chat_id?: string
    email_enabled?: boolean
    smtp_host?: string
    smtp_port?: number
    smtp_username?: string
    smtp_password?: string
    smtp_from?: string
    smtp_to?: string[]
  }
  disk_health?: {
    enabled?: boolean
    check_interval?: string
    probe_timeout?: string
    cooldown_period?: string
    command?: string
    temperature_warning_c?: number
    temperature_critical_c?: number
    media_wear_warning_percent?: number
    media_wear_critical_percent?: number
    devices?: DiskHealthDeviceSettings[]
  }
  maintenance?: {
    scrub?: {
      enabled?: boolean
      schedule_interval?: string
      retry_interval?: string
      max_retries?: number
    }
  }
  webdav?: {
    enabled?: boolean
    prefix?: string
    read_only?: boolean
    auth_type?: string
    username?: string
    password?: string
  }
}

async function parseSettingsError(response: Response, fallback: string): Promise<SettingsError> {
  const structuredError = await readStructuredJsonErrorDetails(response, fallback)
  if (structuredError) {
    return new SettingsError(structuredError.message, response.status, structuredError.code)
  }

  try {
    const body = await response.json() as SettingsApiResponse<never>
    return new SettingsError(body.error?.message || body.message || fallback, response.status, body.error?.code)
  } catch {
    return new SettingsError(fallback, response.status)
  }
}

async function parseSettingsSuccess<T>(response: Response, invalidMessage: string): Promise<SettingsApiResponse<T>> {
  let body: SettingsApiResponse<T>
  try {
    body = await response.json() as SettingsApiResponse<T>
  } catch {
    throw new Error(invalidMessage)
  }

  if (!body || body.success !== true) {
    throw new Error(invalidMessage)
  }

  return body
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value)
}

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((entry) => typeof entry === 'string')
}

function isDirectoryQuota(value: unknown): value is DirectoryQuota {
  return isRecord(value)
    && typeof value.path === 'string'
    && typeof value.quota_bytes === 'number'
}

function isDirectoryAccessRole(value: unknown): value is DirectoryAccessRole {
  return value === 'admin' || value === 'user' || value === 'guest'
}

function isDirectoryAccessDecisionSource(value: unknown): value is DirectoryAccessDecisionSource {
  return value === 'auth_disabled'
    || value === 'admin'
    || value === 'user_disabled'
    || value === 'user_not_found'
    || value === 'directory_access_rule'
    || value === 'home_dir'
    || value === 'invalid_home_dir'
}

function isDirectoryAccessRule(value: unknown): value is DirectoryAccessRule {
  return isRecord(value)
    && typeof value.path === 'string'
    && (value.read_users === undefined || isStringArray(value.read_users))
    && (value.write_users === undefined || isStringArray(value.write_users))
    && (value.read_groups === undefined || isStringArray(value.read_groups))
    && (value.write_groups === undefined || isStringArray(value.write_groups))
    && (value.read_roles === undefined || (Array.isArray(value.read_roles) && value.read_roles.every(isDirectoryAccessRole)))
    && (value.write_roles === undefined || (Array.isArray(value.write_roles) && value.write_roles.every(isDirectoryAccessRole)))
}

function isSharePolicyRule(value: unknown): value is SharePolicyRule {
  return isRecord(value)
    && typeof value.path === 'string'
    && (value.require_password === undefined || typeof value.require_password === 'boolean')
    && (value.max_expires_in === undefined || typeof value.max_expires_in === 'string')
    && (value.max_access === undefined || typeof value.max_access === 'number')
}

function isDiskHealthDeviceSettings(value: unknown): value is DiskHealthDeviceSettings {
  return isRecord(value)
    && (value.name === undefined || typeof value.name === 'string')
    && typeof value.path === 'string'
    && (value.type === undefined || typeof value.type === 'string')
    && (value.serial === undefined || typeof value.serial === 'string')
    && (value.temperature_warning_c === undefined || typeof value.temperature_warning_c === 'number')
    && (value.temperature_critical_c === undefined || typeof value.temperature_critical_c === 'number')
}

function isDirectoryAccessDecision(value: unknown): value is DirectoryAccessDecision {
  return isRecord(value)
    && (value.mode === 'read' || value.mode === 'write')
    && typeof value.allowed === 'boolean'
    && isDirectoryAccessDecisionSource(value.source)
    && (value.message === undefined || typeof value.message === 'string')
    && (value.matched_rule === undefined || isDirectoryAccessRule(value.matched_rule))
}

function isDirectoryAccessCheckData(value: unknown): value is DirectoryAccessCheckData {
  return isRecord(value)
    && typeof value.username === 'string'
    && typeof value.user_id === 'string'
    && isDirectoryAccessRole(value.role)
    && (value.groups === undefined || isStringArray(value.groups))
    && typeof value.home_dir === 'string'
    && typeof value.path === 'string'
    && isDirectoryAccessDecision(value.read)
    && isDirectoryAccessDecision(value.write)
}

function isDirectoryAccessReportSummary(value: unknown): value is DirectoryAccessReportSummary {
  return isRecord(value)
    && typeof value.users === 'number'
    && typeof value.read_allowed === 'number'
    && typeof value.read_denied === 'number'
    && typeof value.write_allowed === 'number'
    && typeof value.write_denied === 'number'
    && typeof value.related_shares === 'number'
    && typeof value.active_related_shares === 'number'
    && typeof value.password_protected_shares === 'number'
}

function isDirectoryAccessShareRelation(value: unknown): value is DirectoryAccessShareRelation {
  return value === 'exact' || value === 'covers_path' || value === 'inside_path'
}

function isDirectoryAccessShareImpact(value: unknown): value is DirectoryAccessShareImpact {
  return isRecord(value)
    && typeof value.id === 'string'
    && typeof value.path === 'string'
    && (value.type === 'file' || value.type === 'folder')
    && typeof value.created_by === 'string'
    && isDirectoryAccessShareRelation(value.relation)
    && typeof value.enabled === 'boolean'
    && typeof value.active === 'boolean'
    && typeof value.has_password === 'boolean'
    && typeof value.access_count === 'number'
    && typeof value.max_access === 'number'
    && (value.expires_at === undefined || typeof value.expires_at === 'string')
    && (value.url === undefined || typeof value.url === 'string')
}

function isDirectoryAccessReportData(value: unknown): value is DirectoryAccessReportData {
  return isRecord(value)
    && typeof value.path === 'string'
    && (value.preview === undefined || typeof value.preview === 'boolean')
    && isDirectoryAccessReportSummary(value.summary)
    && Array.isArray(value.users)
    && value.users.every(isDirectoryAccessCheckData)
    && (value.shares === undefined || (Array.isArray(value.shares) && value.shares.every(isDirectoryAccessShareImpact)))
}

function isSecurityCheckStatus(value: unknown): value is SecurityCheckStatus {
  return value === 'pass' || value === 'warning' || value === 'block'
}

function isValidSecurityCheckItem(value: unknown): value is SecurityCheckItem {
  return isRecord(value)
    && typeof value.id === 'string'
    && isSecurityCheckStatus(value.status)
    && typeof value.title === 'string'
    && typeof value.message === 'string'
    && (value.details === undefined || isRecord(value.details))
}

function isValidSecurityCheckData(value: unknown): value is SecurityCheckData {
  return isRecord(value)
    && isSecurityCheckStatus(value.status)
    && typeof value.generated_at === 'string'
    && Array.isArray(value.checks)
    && value.checks.every(isValidSecurityCheckItem)
    && isRecord(value.request)
    && isRecord(value.config)
}

function isValidWebDAVCredentials(value: unknown): value is WebDAVCredentialsResponse {
  return isRecord(value)
    && typeof value.enabled === 'boolean'
    && typeof value.url === 'string'
    && typeof value.auth_type === 'string'
    && (value.username === undefined || typeof value.username === 'string')
    && (value.password === undefined || typeof value.password === 'string')
}

function isValidSettingsData(value: unknown): value is SettingsData {
  if (!isRecord(value)
    || !isRecord(value.server)
    || typeof value.server.host !== 'string'
    || typeof value.server.port !== 'number'
    || typeof value.server.read_timeout !== 'string'
    || typeof value.server.write_timeout !== 'string'
    || typeof value.server.idle_timeout !== 'string'
    || typeof value.server.trusted_proxy_hops !== 'number'
    || (value.server.trusted_proxy_cidrs !== undefined && !isStringArray(value.server.trusted_proxy_cidrs))
    || !isRecord(value.storage)
    || typeof value.storage.root !== 'string'
    || !isRecord(value.retention)
    || typeof value.retention.max_versions !== 'number'
    || typeof value.retention.max_age !== 'string'
    || typeof value.retention.min_free_space !== 'number'
    || typeof value.retention.gc_interval !== 'string'
    || !isRecord(value.webdav)
    || typeof value.webdav.enabled !== 'boolean'
    || (value.webdav.runtime_enabled !== undefined && typeof value.webdav.runtime_enabled !== 'boolean')
    || typeof value.webdav.prefix !== 'string'
    || typeof value.webdav.read_only !== 'boolean'
    || typeof value.webdav.auth_type !== 'string'
    || typeof value.webdav.username !== 'string'
    || !isRecord(value.share)
    || typeof value.share.enabled !== 'boolean'
    || typeof value.share.base_url !== 'string'
    || (value.share.default_expires_in !== undefined && typeof value.share.default_expires_in !== 'string')
    || (value.share.default_max_access !== undefined && typeof value.share.default_max_access !== 'number')
    || (value.share.policy_rules !== undefined && (!Array.isArray(value.share.policy_rules) || !value.share.policy_rules.every(isSharePolicyRule)))
    || !isRecord(value.dataplane)
    || typeof value.dataplane.grpc_address !== 'string'
    || typeof value.dataplane.timeout !== 'string'
    || typeof value.dataplane.max_retries !== 'number'
    || !isRecord(value.cdc)
    || typeof value.cdc.min_chunk_size !== 'number'
    || typeof value.cdc.avg_chunk_size !== 'number'
    || typeof value.cdc.max_chunk_size !== 'number') {
    return false
  }

  if (value.server.tls !== undefined) {
    if (!isRecord(value.server.tls)
      || typeof value.server.tls.enabled !== 'boolean'
      || typeof value.server.tls.cert_file !== 'string'
      || typeof value.server.tls.key_file !== 'string'
      || typeof value.server.tls.auto_generate !== 'boolean'
      || typeof value.server.tls.cert_dir !== 'string') {
      return false
    }
  }

  if (value.storage.directory_quotas !== undefined) {
    if (!Array.isArray(value.storage.directory_quotas) || !value.storage.directory_quotas.every(isDirectoryQuota)) {
      return false
    }
  }

  if (value.storage.directory_access_rules !== undefined) {
    if (!Array.isArray(value.storage.directory_access_rules) || !value.storage.directory_access_rules.every(isDirectoryAccessRule)) {
      return false
    }
  }

  if (value.trash !== undefined) {
    if (!isRecord(value.trash)
      || typeof value.trash.enabled !== 'boolean'
      || typeof value.trash.retention_days !== 'number'
      || typeof value.trash.max_size !== 'number') {
      return false
    }
  }

  if (value.versioning !== undefined) {
    if (!isRecord(value.versioning)
      || !isStringArray(value.versioning.auto_versioned_extensions)
      || !isStringArray(value.versioning.auto_versioned_filenames)
      || typeof value.versioning.max_versioned_size !== 'number') {
      return false
    }
  }

  if (value.favorites !== undefined) {
    if (!isRecord(value.favorites)
      || typeof value.favorites.enabled !== 'boolean'
      || (value.favorites.runtime_available !== undefined && typeof value.favorites.runtime_available !== 'boolean')) {
      return false
    }
  }

  if (value.alerts !== undefined) {
    if (!isRecord(value.alerts)
      || typeof value.alerts.enabled !== 'boolean'
      || typeof value.alerts.check_interval !== 'string'
      || typeof value.alerts.threshold_pct !== 'number'
      || typeof value.alerts.critical_pct !== 'number'
      || typeof value.alerts.min_free_bytes !== 'number'
      || typeof value.alerts.cooldown_period !== 'string'
      || typeof value.alerts.webhook_url !== 'string'
      || (value.alerts.webhook_url_configured !== undefined && typeof value.alerts.webhook_url_configured !== 'boolean')
      || typeof value.alerts.webhook_method !== 'string'
      || !isStringArray(value.alerts.webhook_headers)
      || (value.alerts.webhook_headers_configured !== undefined && typeof value.alerts.webhook_headers_configured !== 'boolean')
      || (value.alerts.telegram_enabled !== undefined && typeof value.alerts.telegram_enabled !== 'boolean')
      || (value.alerts.telegram_bot_token_configured !== undefined && typeof value.alerts.telegram_bot_token_configured !== 'boolean')
      || (value.alerts.telegram_chat_id !== undefined && typeof value.alerts.telegram_chat_id !== 'string')
      || (value.alerts.email_enabled !== undefined && typeof value.alerts.email_enabled !== 'boolean')
      || (value.alerts.smtp_host !== undefined && typeof value.alerts.smtp_host !== 'string')
      || (value.alerts.smtp_port !== undefined && typeof value.alerts.smtp_port !== 'number')
      || (value.alerts.smtp_username !== undefined && typeof value.alerts.smtp_username !== 'string')
      || (value.alerts.smtp_password_configured !== undefined && typeof value.alerts.smtp_password_configured !== 'boolean')
      || (value.alerts.smtp_from !== undefined && typeof value.alerts.smtp_from !== 'string')
      || (value.alerts.smtp_to !== undefined && !isStringArray(value.alerts.smtp_to))) {
      return false
    }
  }

  if (value.disk_health !== undefined) {
    if (!isRecord(value.disk_health)
      || typeof value.disk_health.enabled !== 'boolean'
      || typeof value.disk_health.check_interval !== 'string'
      || typeof value.disk_health.probe_timeout !== 'string'
      || typeof value.disk_health.cooldown_period !== 'string'
      || typeof value.disk_health.command !== 'string'
      || typeof value.disk_health.temperature_warning_c !== 'number'
      || typeof value.disk_health.temperature_critical_c !== 'number'
      || typeof value.disk_health.media_wear_warning_percent !== 'number'
      || typeof value.disk_health.media_wear_critical_percent !== 'number'
      || !Array.isArray(value.disk_health.devices)
      || !value.disk_health.devices.every(isDiskHealthDeviceSettings)) {
      return false
    }
  }

  if (value.maintenance !== undefined) {
    if (!isRecord(value.maintenance)) {
      return false
    }
    if (value.maintenance.scrub !== undefined) {
      if (!isRecord(value.maintenance.scrub)
        || typeof value.maintenance.scrub.enabled !== 'boolean'
        || typeof value.maintenance.scrub.schedule_interval !== 'string'
        || typeof value.maintenance.scrub.retry_interval !== 'string'
        || typeof value.maintenance.scrub.max_retries !== 'number') {
        return false
      }
    }
  }

  return true
}

/**
 * Get current settings
 */
export async function getSettings(options: SettingsRequestOptions = {}): Promise<SettingsResponse> {
  const response = await authFetch(`${API_BASE}/`, options)
  
  if (!response.ok) {
    throw await parseSettingsError(response, SETTINGS_ERROR_MESSAGES.get)
  }

  const body = await parseSettingsSuccess<unknown>(response, INVALID_SETTINGS_RESPONSE_MESSAGE)
  if (!isValidSettingsData(body.data)) {
    throw new Error(INVALID_SETTINGS_RESPONSE_MESSAGE)
  }
  return {
    success: body.success,
    data: body.data,
  }
}

/**
 * Get public-access security self-check
 */
export async function getSecurityCheck(options: SettingsRequestOptions = {}): Promise<SecurityCheckResponse> {
  const response = await authFetch(`${API_BASE}/security-check`, options)

  if (!response.ok) {
    throw await parseSettingsError(response, SETTINGS_ERROR_MESSAGES.securityCheck)
  }

  const body = await parseSettingsSuccess<unknown>(response, INVALID_SETTINGS_RESPONSE_MESSAGE)
  if (!isValidSecurityCheckData(body.data)) {
    throw new Error(INVALID_SETTINGS_RESPONSE_MESSAGE)
  }
  return {
    success: body.success,
    data: body.data,
  }
}

/**
 * Update settings
 */
export async function updateSettings(
  data: UpdateSettingsRequest,
  options: SettingsRequestOptions = {},
): Promise<{ success: boolean; message: string }> {
  const response = await authFetch(`${API_BASE}/`, {
    ...options,
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(data),
  })
  
  if (!response.ok) {
    throw await parseSettingsError(response, SETTINGS_ERROR_MESSAGES.update)
  }

  const body = await parseSettingsSuccess<null>(response, INVALID_SETTINGS_RESPONSE_MESSAGE)
  if (!('data' in body)) {
    throw new Error(INVALID_SETTINGS_RESPONSE_MESSAGE)
  }
  return {
    success: true,
    message: body.message || '',
  }
}

export async function checkDirectoryAccess(
  data: DirectoryAccessCheckRequest,
  options: SettingsRequestOptions = {},
): Promise<DirectoryAccessCheckData> {
  const response = await authFetch(`${API_BASE}/access-check`, {
    ...options,
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(data),
  })

  if (!response.ok) {
    throw await parseSettingsError(response, SETTINGS_ERROR_MESSAGES.accessCheck)
  }

  const body = await parseSettingsSuccess<unknown>(response, INVALID_SETTINGS_RESPONSE_MESSAGE)
  if (!isDirectoryAccessCheckData(body.data)) {
    throw new Error(INVALID_SETTINGS_RESPONSE_MESSAGE)
  }
  return body.data
}

export async function reportDirectoryAccess(
  data: DirectoryAccessReportRequest,
  options: SettingsRequestOptions = {},
): Promise<DirectoryAccessReportData> {
  const response = await authFetch(`${API_BASE}/access-report`, {
    ...options,
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(data),
  })

  if (!response.ok) {
    throw await parseSettingsError(response, SETTINGS_ERROR_MESSAGES.accessReport)
  }

  const body = await parseSettingsSuccess<unknown>(response, INVALID_SETTINGS_RESPONSE_MESSAGE)
  if (!isDirectoryAccessReportData(body.data)) {
    throw new Error(INVALID_SETTINGS_RESPONSE_MESSAGE)
  }
  return body.data
}

export async function previewDirectoryAccess(
  data: DirectoryAccessPreviewRequest,
  options: SettingsRequestOptions = {},
): Promise<DirectoryAccessReportData> {
  const response = await authFetch(`${API_BASE}/access-preview`, {
    ...options,
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(data),
  })

  if (!response.ok) {
    throw await parseSettingsError(response, SETTINGS_ERROR_MESSAGES.accessPreview)
  }

  const body = await parseSettingsSuccess<unknown>(response, INVALID_SETTINGS_RESPONSE_MESSAGE)
  if (!isDirectoryAccessReportData(body.data)) {
    throw new Error(INVALID_SETTINGS_RESPONSE_MESSAGE)
  }
  return body.data
}

/**
 * WebDAV credentials response
 */
export interface WebDAVCredentialsResponse {
  enabled: boolean
  url: string
  auth_type: string
  username?: string
  password?: string
}

/**
 * Get WebDAV credentials for authenticated users
 */
export async function getWebDAVCredentials(options: SettingsRequestOptions = {}): Promise<WebDAVCredentialsResponse> {
  const response = await authFetch(`${API_BASE}/webdav-credentials`, options)
  
  if (!response.ok) {
    throw await parseSettingsError(response, SETTINGS_ERROR_MESSAGES.webdavCredentials)
  }

  const body = await parseSettingsSuccess<unknown>(response, INVALID_SETTINGS_RESPONSE_MESSAGE)
  if (!isValidWebDAVCredentials(body.data)) {
    throw new Error(INVALID_SETTINGS_RESPONSE_MESSAGE)
  }
  return body.data
}
