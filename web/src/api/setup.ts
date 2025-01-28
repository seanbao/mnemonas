/**
 * Setup API
 * Endpoints for first-run status and onboarding acknowledgement
 */

import { authFetch } from './auth'

const API_BASE = '/api/v1/setup'

interface SetupErrorResponse {
  success?: boolean
  message?: string
  error?: string | { message?: string }
}

export interface SetupStatusResponse {
  success: boolean
  is_first_run: boolean
  auth_enabled: boolean
  webdav_enabled: boolean
  webdav_auth_type: string
}

/**
 * Get setup status for first run.
 */
export async function getSetupStatus(): Promise<SetupStatusResponse> {
  const response = await fetch(`${API_BASE}/`)
  
  if (!response.ok) {
    try {
      const error = await response.json() as SetupErrorResponse
      const message = typeof error.error === 'string'
        ? error.error
        : error.error?.message || error.message || 'Failed to get setup status'
      throw new Error(message)
    } catch (error) {
      if (error instanceof Error) {
        throw error
      }
      throw new Error('Failed to get setup status')
    }
  }
  
  return response.json()
}

/**
 * Acknowledge setup after an authenticated admin signs in.
 */
export async function acknowledgeSetup(): Promise<{ success: boolean; message: string }> {
  const response = await authFetch(`${API_BASE}/acknowledge`, {
    method: 'POST',
  })
  
  if (!response.ok) {
    try {
      const error = await response.json() as SetupErrorResponse
      const message = typeof error.error === 'string'
        ? error.error
        : error.error?.message || error.message || 'Failed to acknowledge setup'
      throw new Error(message)
    } catch (error) {
      if (error instanceof Error) {
        throw error
      }
      throw new Error('Failed to acknowledge setup')
    }
  }
  
  return response.json()
}
