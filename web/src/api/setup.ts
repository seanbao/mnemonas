/**
 * Setup API
 * Endpoints for first-run status and onboarding acknowledgement
 */

import { authFetch } from './auth'

const API_BASE = '/api/v1/setup'

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
    const error = await response.json()
    throw new Error(error.error || 'Failed to get setup status')
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
    const error = await response.json()
    throw new Error(error.error || 'Failed to acknowledge setup')
  }
  
  return response.json()
}
