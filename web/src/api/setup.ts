/**
 * Setup API
 * Endpoints for first-run setup and credential display
 */

const API_BASE = '/api/v1/setup'

export interface SetupStatusResponse {
  success: boolean
  is_first_run: boolean
  auth_enabled: boolean
  web_username?: string
  web_password?: string
  webdav_enabled: boolean
  webdav_auth_type: string
  webdav_username?: string
  webdav_password?: string
}

/**
 * Get setup status and credentials (for first run)
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
 * Acknowledge setup (mark as shown)
 */
export async function acknowledgeSetup(): Promise<{ success: boolean; message: string }> {
  const response = await fetch(`${API_BASE}/acknowledge`, {
    method: 'POST',
  })
  
  if (!response.ok) {
    const error = await response.json()
    throw new Error(error.error || 'Failed to acknowledge setup')
  }
  
  return response.json()
}
