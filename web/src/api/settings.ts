/**
 * Settings API
 * Admin-only endpoints for system configuration
 */

import { authFetch } from './auth'

const API_BASE = '/api/v1/settings'

export interface SettingsData {
  server: {
    host: string
    port: number
    read_timeout: string
    write_timeout: string
    idle_timeout: string
    trusted_proxy_hops: number
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
    webhook_method: string
    webhook_headers: string[]
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

export interface DirectoryAccessCheckRequest {
  username: string
  path: string
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
      || typeof value.alerts.webhook_method !== 'string'
      || !isStringArray(value.alerts.webhook_headers)
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
export async function getSettings(): Promise<SettingsResponse> {
  const response = await authFetch(`${API_BASE}/`)
  
  if (!response.ok) {
    throw await parseSettingsError(response, 'Failed to get settings')
  }

  const body = await parseSettingsSuccess<unknown>(response, 'Invalid settings response')
  if (!isValidSettingsData(body.data)) {
    throw new Error('Invalid settings response')
  }
  return {
    success: body.success,
    data: body.data,
  }
}

/**
 * Get public-access security self-check
 */
export async function getSecurityCheck(): Promise<SecurityCheckResponse> {
  const response = await authFetch(`${API_BASE}/security-check`)

  if (!response.ok) {
    throw await parseSettingsError(response, 'Failed to get security check')
  }

  const body = await parseSettingsSuccess<unknown>(response, 'Invalid security check response')
  if (!isValidSecurityCheckData(body.data)) {
    throw new Error('Invalid security check response')
  }
  return {
    success: body.success,
    data: body.data,
  }
}

/**
 * Update settings
 */
export async function updateSettings(data: UpdateSettingsRequest): Promise<{ success: boolean; message: string }> {
  const response = await authFetch(`${API_BASE}/`, {
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(data),
  })
  
  if (!response.ok) {
    throw await parseSettingsError(response, 'Failed to update settings')
  }

  const body = await parseSettingsSuccess<null>(response, 'Invalid update settings response')
  if (!('data' in body)) {
    throw new Error('Invalid update settings response')
  }
  return {
    success: true,
    message: body.message || '',
  }
}

export async function checkDirectoryAccess(data: DirectoryAccessCheckRequest): Promise<DirectoryAccessCheckData> {
  const response = await authFetch(`${API_BASE}/access-check`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(data),
  })

  if (!response.ok) {
    throw await parseSettingsError(response, 'Failed to check directory access')
  }

  const body = await parseSettingsSuccess<unknown>(response, 'Invalid directory access check response')
  if (!isDirectoryAccessCheckData(body.data)) {
    throw new Error('Invalid directory access check response')
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
export async function getWebDAVCredentials(): Promise<WebDAVCredentialsResponse> {
  const response = await authFetch(`${API_BASE}/webdav-credentials`)
  
  if (!response.ok) {
    throw await parseSettingsError(response, 'Failed to get WebDAV credentials')
  }

  const body = await parseSettingsSuccess<unknown>(response, 'Invalid WebDAV credentials response')
  if (!isValidWebDAVCredentials(body.data)) {
    throw new Error('Invalid WebDAV credentials response')
  }
  return body.data
}
