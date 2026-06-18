/**
 * Setup API
 * Endpoints for first-run status and onboarding acknowledgement
 */

import { authFetch } from './auth'
import { INVALID_API_RESPONSE_MESSAGE as INVALID_SETUP_RESPONSE_MESSAGE } from '@/lib/apiMessages'
import { getNonBlankJsonString, readStructuredJsonErrorDetails } from '@/lib/jsonErrorResponse'

const API_BASE = '/api/v1/setup'
const SETUP_STATUS_FAILED_MESSAGE = '获取初始化状态失败'
const ACKNOWLEDGE_SETUP_FAILED_MESSAGE = '确认初始化完成失败'

interface SetupErrorResponse {
  success?: boolean
  message?: string
  error?: string | { message?: string }
}

interface SetupSuccessResponse {
  success: boolean
  message?: string
  warning?: boolean
  data?: unknown
}

export interface SetupStatusResponse {
  success: boolean
  is_first_run: boolean
  auth_enabled: boolean
  share_enabled?: boolean
  webdav_enabled: boolean
  webdav_auth_type: string
  allow_unsafe_no_auth?: boolean
}

export interface SetupRequestOptions {
  signal?: AbortSignal
}

export interface SetupActionResult {
  success: boolean
  message: string
  warning: boolean
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value)
}

function hasSetupWarning(response: Response, body: SetupSuccessResponse): boolean {
  return response.headers?.get?.('Warning') != null
    || body.warning === true
    || (isRecord(body.data) && body.data.warning === true)
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

async function readSetupErrorMessage(response: Response, fallback: string): Promise<string> {
  const structuredError = await readStructuredJsonErrorDetails(response, fallback)
  if (structuredError) {
    return structuredError.message
  }

  try {
    const error = await response.json() as SetupErrorResponse
    const legacyErrorMessage = typeof error.error === 'string'
      ? getNonBlankJsonString(error.error)
      : getNonBlankJsonString(error.error?.message)
    return legacyErrorMessage ?? getNonBlankJsonString(error.message) ?? fallback
  } catch {
    return fallback
  }
}

/**
 * Get setup status for first run.
 */
export async function getSetupStatus(options: SetupRequestOptions = {}): Promise<SetupStatusResponse> {
  const response = await fetch(`${API_BASE}/`, options)
  
  if (!response.ok) {
    throw new Error(await readSetupErrorMessage(response, SETUP_STATUS_FAILED_MESSAGE))
  }
  
  const body = await parseSetupSuccess<Partial<SetupStatusResponse> & SetupSuccessResponse>(response, INVALID_SETUP_RESPONSE_MESSAGE)
  if (typeof body.is_first_run !== 'boolean'
    || typeof body.auth_enabled !== 'boolean'
    || (body.share_enabled !== undefined && typeof body.share_enabled !== 'boolean')
    || typeof body.webdav_enabled !== 'boolean'
    || typeof body.webdav_auth_type !== 'string'
    || (body.allow_unsafe_no_auth !== undefined && typeof body.allow_unsafe_no_auth !== 'boolean')) {
    throw new Error(INVALID_SETUP_RESPONSE_MESSAGE)
  }

  return body as SetupStatusResponse
}

/**
 * Acknowledge setup after an authenticated admin signs in.
 */
export async function acknowledgeSetup(options: SetupRequestOptions = {}): Promise<SetupActionResult> {
  const response = await authFetch(`${API_BASE}/acknowledge`, {
    method: 'POST',
    ...(options.signal ? { signal: options.signal } : {}),
  })
  
  if (!response.ok) {
    throw new Error(await readSetupErrorMessage(response, ACKNOWLEDGE_SETUP_FAILED_MESSAGE))
  }
  
  const body = await parseSetupSuccess<SetupSuccessResponse>(response, INVALID_SETUP_RESPONSE_MESSAGE)
  return {
    success: true,
    warning: hasSetupWarning(response, body),
    message: getNonBlankJsonString(body.message) ?? '',
  }
}
