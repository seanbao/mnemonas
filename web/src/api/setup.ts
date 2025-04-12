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

interface SetupSuccessResponse {
  success: boolean
}

export interface SetupStatusResponse {
  success: boolean
  is_first_run: boolean
  auth_enabled: boolean
  webdav_enabled: boolean
  webdav_auth_type: string
}

async function parseSetupSuccess<T extends SetupSuccessResponse>(response: Response, invalidMessage: string): Promise<T> {
  let body: unknown
  try {
    body = await response.json()
  } catch {
    throw new Error(invalidMessage)
  }

  if (!body || typeof body !== 'object' || (body as SetupSuccessResponse).success !== true) {
    throw new Error(invalidMessage)
  }

  return body as T
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
  
  const body = await parseSetupSuccess<Partial<SetupStatusResponse> & SetupSuccessResponse>(response, 'Invalid setup status response')
  if (typeof body.is_first_run !== 'boolean' || typeof body.auth_enabled !== 'boolean' || typeof body.webdav_enabled !== 'boolean' || typeof body.webdav_auth_type !== 'string') {
    throw new Error('Invalid setup status response')
  }

  return body as SetupStatusResponse
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
  
  const body = await parseSetupSuccess<SetupSuccessResponse & { message?: string }>(response, 'Invalid acknowledge setup response')
  return {
    success: true,
    message: body.message ?? '',
  }
}
