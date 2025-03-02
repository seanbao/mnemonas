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

export interface SettingsResponse {
  success: boolean
  data: SettingsData
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

export interface UpdateSettingsRequest {
  server?: {
    host?: string
    port?: number
    read_timeout?: string
    write_timeout?: string
    idle_timeout?: string
    tls?: {
      enabled?: boolean
      cert_file?: string
      key_file?: string
      auto_generate?: boolean
      cert_dir?: string
    }
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

async function parseSettingsError(response: Response, fallback: string): Promise<Error> {
  try {
    const body = await response.json() as SettingsApiResponse<never>
    return new Error(body.error?.message || body.message || fallback)
  } catch {
    return new Error(fallback)
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

/**
 * Get current settings
 */
export async function getSettings(): Promise<SettingsResponse> {
  const response = await authFetch(`${API_BASE}/`)
  
  if (!response.ok) {
    throw await parseSettingsError(response, 'Failed to get settings')
  }

  const body = await parseSettingsSuccess<SettingsData>(response, 'Invalid settings response')
  if (!body.data) {
    throw new Error('Invalid settings response')
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
  return {
    success: true,
    message: body.message || '',
  }
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

  const body = await parseSettingsSuccess<WebDAVCredentialsResponse>(response, 'Invalid WebDAV credentials response')
  if (!body.data) {
    throw new Error('Invalid WebDAV credentials response')
  }
  return body.data
}
