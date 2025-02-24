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
  }
  storage: {
    data_dir: string
    metadata_dir: string
    temp_dir: string
  }
  retention: {
    max_versions: number
    max_age: string
    min_free_space: number
    gc_interval: string
  }
  webdav: {
    enabled: boolean
    prefix: string
    read_only: boolean
    auth_type: string
    username: string
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

export interface UpdateSettingsRequest {
  server?: {
    host?: string
    port?: number
  }
  retention?: {
    max_versions?: number
    max_age?: string
    min_free_space?: number
    gc_interval?: string
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

/**
 * Get current settings
 */
export async function getSettings(): Promise<SettingsResponse> {
  const response = await authFetch(`${API_BASE}/`)
  
  if (!response.ok) {
    const error = await response.json()
    throw new Error(error.error || 'Failed to get settings')
  }
  
  return response.json()
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
    const error = await response.json()
    throw new Error(error.error || 'Failed to update settings')
  }
  
  return response.json()
}

/**
 * WebDAV credentials response
 */
export interface WebDAVCredentialsResponse {
  success: boolean
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
    const error = await response.json()
    throw new Error(error.error || 'Failed to get WebDAV credentials')
  }
  
  return response.json()
}
