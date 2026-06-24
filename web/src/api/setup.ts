/**
 * Setup API
 * Endpoints for first-run status and server-derived setup readiness
 */

import { authFetch } from './auth'
import { INVALID_API_RESPONSE_MESSAGE as INVALID_SETUP_RESPONSE_MESSAGE } from '@/lib/apiMessages'
import { getNonBlankJsonString, readStructuredJsonErrorDetails } from '@/lib/jsonErrorResponse'

const API_BASE = '/api/v1/setup'
const SETUP_STATUS_FAILED_MESSAGE = '获取初始化状态失败'
const SETUP_READINESS_FAILED_MESSAGE = '获取设置就绪状态失败'
const ACKNOWLEDGE_SETUP_FAILED_MESSAGE = '完成设置确认失败'
const DEFER_SETUP_FAILED_MESSAGE = '延期设置提醒失败'

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

interface SetupApiResponse<T> {
  success: boolean
  data: T
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

export const SETUP_READINESS_LIFECYCLES = ['pending', 'deferred', 'completed'] as const
export const SETUP_READINESS_OVERALL_STATUSES = ['ready', 'action_required', 'unavailable'] as const
export const SETUP_READINESS_REQUIREMENTS = ['required', 'recommended'] as const
export const SETUP_READINESS_CHECK_STATUSES = ['complete', 'incomplete', 'unavailable', 'not_applicable'] as const
export const SETUP_READINESS_ACTIONS = [
  'change_password',
  'manage_users',
  'create_backup',
  'run_backup',
  'run_restore_drill',
  'review_security',
] as const
export const SETUP_READINESS_CHECK_IDS = [
  'admin_access',
  'bootstrap_credential',
  'initial_password_file',
  'security_baseline',
  'backup_job',
  'backup_success',
  'admin_redundancy',
  'backup_schedule',
  'restore_verification',
  'security_recommendations',
] as const
export const SETUP_DEFER_DAYS = [1, 3, 7, 30] as const

export type SetupReadinessLifecycle = typeof SETUP_READINESS_LIFECYCLES[number]
export type SetupReadinessOverallStatus = typeof SETUP_READINESS_OVERALL_STATUSES[number]
export type SetupReadinessRequirement = typeof SETUP_READINESS_REQUIREMENTS[number]
export type SetupReadinessCheckStatus = typeof SETUP_READINESS_CHECK_STATUSES[number]
export type SetupReadinessAction = typeof SETUP_READINESS_ACTIONS[number]
export type SetupReadinessCheckID = typeof SETUP_READINESS_CHECK_IDS[number]
export type SetupDeferDays = typeof SETUP_DEFER_DAYS[number]

export interface SetupReadinessCheck {
  id: SetupReadinessCheckID
  requirement: SetupReadinessRequirement
  status: SetupReadinessCheckStatus
  deferrable: boolean
  title: string
  message: string
  action: SetupReadinessAction
}

const setupReadinessCheckContracts: ReadonlyArray<{
  id: SetupReadinessCheckID
  requirement: SetupReadinessRequirement
  deferrable: boolean
  actions: readonly SetupReadinessAction[]
}> = [
  { id: 'admin_access', requirement: 'required', deferrable: false, actions: ['manage_users', 'review_security'] },
  { id: 'bootstrap_credential', requirement: 'required', deferrable: false, actions: ['change_password', 'manage_users', 'review_security'] },
  { id: 'initial_password_file', requirement: 'required', deferrable: false, actions: ['change_password'] },
  { id: 'security_baseline', requirement: 'required', deferrable: false, actions: ['review_security'] },
  { id: 'backup_job', requirement: 'required', deferrable: true, actions: ['create_backup'] },
  { id: 'backup_success', requirement: 'required', deferrable: true, actions: ['run_backup'] },
  { id: 'admin_redundancy', requirement: 'recommended', deferrable: false, actions: ['manage_users'] },
  { id: 'backup_schedule', requirement: 'recommended', deferrable: false, actions: ['create_backup'] },
  { id: 'restore_verification', requirement: 'recommended', deferrable: false, actions: ['run_restore_drill'] },
  { id: 'security_recommendations', requirement: 'recommended', deferrable: false, actions: ['review_security'] },
]

export interface SetupReadinessCount {
  completed: number
  total: number
}

export interface SetupReadinessSummary {
  auth_enabled: boolean
  active_admin_count: number
  password_change_required_admin_count: number
  initial_password_file: 'missing' | 'present' | 'unavailable'
  enabled_backup_job_count: number
  latest_backup_success_at?: string
  latest_restore_verification_at?: string
  security_status: 'pass' | 'warning' | 'block' | 'unavailable'
  security_blocking_check_ids: string[]
}

export interface SetupReadiness {
  lifecycle: SetupReadinessLifecycle
  overall_status: SetupReadinessOverallStatus
  prompt: boolean
  generated_at: string
  completed_at?: string
  deferred_until?: string
  can_complete: boolean
  can_defer: boolean
  required: SetupReadinessCount
  recommended: SetupReadinessCount
  checks: SetupReadinessCheck[]
  summary: SetupReadinessSummary
}

export interface DeferSetupRequest {
  remind_in_days: SetupDeferDays
}

export class SetupError extends Error {
  status: number
  code?: string

  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = 'SetupError'
    this.status = status
    this.code = code
  }

  get isUnavailable(): boolean {
    return this.status === 503 || this.code === 'SERVICE_UNAVAILABLE'
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value)
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

async function parseSetupError(response: Response, fallback: string): Promise<SetupError> {
  const structuredError = await readStructuredJsonErrorDetails(response, fallback)
  if (structuredError) {
    return new SetupError(structuredError.message, response.status, structuredError.code)
  }

  try {
    const error = await response.json() as SetupErrorResponse
    const legacyErrorMessage = typeof error.error === 'string'
      ? getNonBlankJsonString(error.error)
      : getNonBlankJsonString(error.error?.message)
    return new SetupError(legacyErrorMessage ?? getNonBlankJsonString(error.message) ?? fallback, response.status)
  } catch {
    return new SetupError(fallback, response.status)
  }
}

function isEnumValue<T extends string>(value: unknown, values: readonly T[]): value is T {
  return typeof value === 'string' && values.includes(value as T)
}

function isSetupDeferDays(value: unknown): value is SetupDeferDays {
  return typeof value === 'number' && (SETUP_DEFER_DAYS as readonly number[]).includes(value)
}

function isNonNegativeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}

function isTimestamp(value: unknown): value is string {
  if (typeof value !== 'string') {
    return false
  }

  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|[+-](\d{2}):(\d{2}))$/.exec(value)
  if (!match) {
    return false
  }

  const [, yearValue, monthValue, dayValue, hourValue, minuteValue, secondValue, offsetHourValue, offsetMinuteValue] = match
  const year = Number(yearValue)
  const month = Number(monthValue)
  const day = Number(dayValue)
  const hour = Number(hourValue)
  const minute = Number(minuteValue)
  const second = Number(secondValue)
  const isLeapYear = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0)
  const daysInMonth = [31, isLeapYear ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]

  return month >= 1
    && month <= 12
    && day >= 1
    && day <= daysInMonth[month - 1]
    && hour <= 23
    && minute <= 59
    && second <= 59
    && (offsetHourValue === undefined || Number(offsetHourValue) <= 23)
    && (offsetMinuteValue === undefined || Number(offsetMinuteValue) <= 59)
}

function isNonBlankString(value: unknown): value is string {
  return getNonBlankJsonString(value) !== undefined
}

function isReadinessCount(value: unknown): value is SetupReadinessCount {
  return isRecord(value)
    && isNonNegativeInteger(value.completed)
    && isNonNegativeInteger(value.total)
    && value.completed <= value.total
}

function isReadinessCheck(
  value: unknown,
  contract: typeof setupReadinessCheckContracts[number],
): value is SetupReadinessCheck {
  return isRecord(value)
    && value.id === contract.id
    && value.requirement === contract.requirement
    && isEnumValue(value.status, SETUP_READINESS_CHECK_STATUSES)
    && value.deferrable === contract.deferrable
    && isNonBlankString(value.title)
    && isNonBlankString(value.message)
    && isEnumValue(value.action, contract.actions)
}

function hasExactReadinessChecks(value: unknown): value is SetupReadinessCheck[] {
  return Array.isArray(value)
    && value.length === setupReadinessCheckContracts.length
    && value.every((check, index) => isReadinessCheck(check, setupReadinessCheckContracts[index]))
}

function countReadinessChecks(
  checks: SetupReadinessCheck[],
  requirement: SetupReadinessRequirement,
): SetupReadinessCount {
  const matching = checks.filter((check) => check.requirement === requirement)
  return {
    completed: matching.filter((check) => check.status === 'complete' || check.status === 'not_applicable').length,
    total: matching.length,
  }
}

function readinessCountMatches(left: SetupReadinessCount, right: SetupReadinessCount): boolean {
  return left.completed === right.completed && left.total === right.total
}

function isCompletedReadinessStatus(status: SetupReadinessCheckStatus): boolean {
  return status === 'complete' || status === 'not_applicable'
}

function getReadinessCheck(
  readiness: Pick<SetupReadiness, 'checks'>,
  id: SetupReadinessCheckID,
): SetupReadinessCheck {
  return readiness.checks[SETUP_READINESS_CHECK_IDS.indexOf(id)]
}

function hasConsistentReadinessLifecycle(value: SetupReadiness): boolean {
  const requiredUnavailable = value.checks.some((check) => (
    check.requirement === 'required' && check.status === 'unavailable'
  ))
  const allRequiredComplete = value.required.completed === value.required.total
  const lifecycleAvailable = value.lifecycle !== 'pending' || value.prompt
  const expectedOverallStatus: SetupReadinessOverallStatus = !lifecycleAvailable || requiredUnavailable
    ? 'unavailable'
    : allRequiredComplete
      ? 'ready'
      : 'action_required'

  if (value.overall_status !== expectedOverallStatus) {
    return false
  }

  const nonDeferrableRequiredReady = value.checks.every((check) => (
    check.requirement !== 'required'
      || check.deferrable
      || isCompletedReadinessStatus(check.status)
  ))
  const deferrableRequired = value.checks.filter((check) => check.requirement === 'required' && check.deferrable)
  const expectedCanDefer = lifecycleAvailable
    && value.lifecycle !== 'completed'
    && nonDeferrableRequiredReady
    && deferrableRequired.some((check) => check.status === 'incomplete')
    && deferrableRequired.every((check) => check.status !== 'unavailable')
  const expectedCanComplete = value.lifecycle !== 'completed' && expectedOverallStatus === 'ready'

  if (value.can_complete !== expectedCanComplete || value.can_defer !== expectedCanDefer) {
    return false
  }

  if (value.lifecycle === 'completed') {
    return value.completed_at !== undefined && !value.prompt
  }
  if (value.completed_at !== undefined) {
    return false
  }
  if (value.lifecycle === 'deferred') {
    return value.deferred_until !== undefined
      && Date.parse(value.deferred_until) > Date.parse(value.generated_at)
      && !value.prompt
      && value.can_defer
  }

  return value.prompt === lifecycleAvailable
}

function hasConsistentReadinessEvidence(value: SetupReadiness): boolean {
  const adminAccess = getReadinessCheck(value, 'admin_access')
  const bootstrapCredential = getReadinessCheck(value, 'bootstrap_credential')
  const initialPasswordFile = getReadinessCheck(value, 'initial_password_file')
  const securityBaseline = getReadinessCheck(value, 'security_baseline')
  const backupJob = getReadinessCheck(value, 'backup_job')
  const backupSuccess = getReadinessCheck(value, 'backup_success')
  const adminRedundancy = getReadinessCheck(value, 'admin_redundancy')
  const backupSchedule = getReadinessCheck(value, 'backup_schedule')
  const restoreVerification = getReadinessCheck(value, 'restore_verification')
  const securityRecommendations = getReadinessCheck(value, 'security_recommendations')
  const blockingIDs = value.summary.security_blocking_check_ids

  if (!value.summary.auth_enabled) {
    if (adminAccess.status !== 'incomplete' || bootstrapCredential.status !== 'incomplete') {
      return false
    }
  } else if (value.summary.active_admin_count > 0) {
    if (adminAccess.status !== 'complete'
      || bootstrapCredential.status !== (value.summary.password_change_required_admin_count > 0 ? 'incomplete' : 'complete')) {
      return false
    }
  }

  if (value.summary.active_admin_count >= 2 && adminRedundancy.status !== 'complete') {
    return false
  }

  const expectedInitialPasswordStatus: SetupReadinessCheckStatus = value.summary.initial_password_file === 'missing'
    ? 'complete'
    : value.summary.initial_password_file === 'present'
      ? 'incomplete'
      : 'unavailable'
  if (initialPasswordFile.status !== expectedInitialPasswordStatus
    || blockingIDs.includes('initial_password_file') !== (value.summary.initial_password_file === 'present')) {
    return false
  }

  const hasSecurityBlocks = blockingIDs.length > 0
  if ((value.summary.security_status === 'block') !== hasSecurityBlocks) {
    return false
  }
  if (value.summary.security_status === 'unavailable') {
    if (securityBaseline.status !== 'unavailable' || securityRecommendations.status !== 'unavailable') {
      return false
    }
  } else {
    const hasBaselineBlock = blockingIDs.some((id) => id !== 'initial_password_file' && id !== 'admin_accounts')
    if (securityBaseline.status !== (hasBaselineBlock ? 'incomplete' : 'complete')) {
      return false
    }
    const expectedRecommendationStatus = value.summary.security_status === 'pass' ? 'complete' : 'incomplete'
    if (securityRecommendations.status !== expectedRecommendationStatus) {
      return false
    }
  }

  const backupUnavailable = backupJob.status === 'unavailable'
  if ([backupSuccess, backupSchedule, restoreVerification].some((check) => (
    (check.status === 'unavailable') !== backupUnavailable
  ))) {
    return false
  }
  if (!backupUnavailable) {
    const expectedBackupJobStatus = value.summary.enabled_backup_job_count > 0 ? 'complete' : 'incomplete'
    if (backupJob.status !== expectedBackupJobStatus) {
      return false
    }
  }

  return true
}

function isReadinessSummary(value: unknown): value is SetupReadinessSummary {
  return isRecord(value)
    && typeof value.auth_enabled === 'boolean'
    && isNonNegativeInteger(value.active_admin_count)
    && isNonNegativeInteger(value.password_change_required_admin_count)
    && isEnumValue(value.initial_password_file, ['missing', 'present', 'unavailable'] as const)
    && isNonNegativeInteger(value.enabled_backup_job_count)
    && (value.latest_backup_success_at === undefined || isTimestamp(value.latest_backup_success_at))
    && (value.latest_restore_verification_at === undefined || isTimestamp(value.latest_restore_verification_at))
    && isEnumValue(value.security_status, ['pass', 'warning', 'block', 'unavailable'] as const)
    && Array.isArray(value.security_blocking_check_ids)
    && value.security_blocking_check_ids.every(isNonBlankString)
    && new Set(value.security_blocking_check_ids).size === value.security_blocking_check_ids.length
    && value.security_blocking_check_ids.every((id, index, ids) => index === 0 || ids[index - 1] < id)
    && value.password_change_required_admin_count <= value.active_admin_count
}

function isSetupReadiness(value: unknown): value is SetupReadiness {
  if (!isRecord(value)) {
    return false
  }

  if (!isEnumValue(value.lifecycle, SETUP_READINESS_LIFECYCLES)
    || !isEnumValue(value.overall_status, SETUP_READINESS_OVERALL_STATUSES)
    || typeof value.prompt !== 'boolean'
    || !isTimestamp(value.generated_at)
    || (value.completed_at !== undefined && !isTimestamp(value.completed_at))
    || (value.deferred_until !== undefined && !isTimestamp(value.deferred_until))
    || typeof value.can_complete !== 'boolean'
    || typeof value.can_defer !== 'boolean'
    || !isReadinessCount(value.required)
    || !isReadinessCount(value.recommended)
    || !hasExactReadinessChecks(value.checks)
    || !isReadinessSummary(value.summary)) {
    return false
  }

  const readiness = value as unknown as SetupReadiness
  return readinessCountMatches(readiness.required, countReadinessChecks(readiness.checks, 'required'))
    && readinessCountMatches(readiness.recommended, countReadinessChecks(readiness.checks, 'recommended'))
    && hasConsistentReadinessLifecycle(readiness)
    && hasConsistentReadinessEvidence(readiness)
}

async function parseSetupReadiness(response: Response): Promise<SetupReadiness> {
  let body: unknown
  try {
    body = await response.json()
  } catch {
    throw new Error(INVALID_SETUP_RESPONSE_MESSAGE)
  }

  if (!isRecord(body)
    || body.success !== true
    || !('data' in body)
    || !isSetupReadiness(body.data)) {
    throw new Error(INVALID_SETUP_RESPONSE_MESSAGE)
  }

  return (body as unknown as SetupApiResponse<SetupReadiness>).data
}

/**
 * Get setup status for first run.
 */
export async function getSetupStatus(options: SetupRequestOptions = {}): Promise<SetupStatusResponse> {
  const response = await fetch(`${API_BASE}/`, {
    cache: 'no-store',
    ...(options.signal ? { signal: options.signal } : {}),
  })
  
  if (!response.ok) {
    throw await parseSetupError(response, SETUP_STATUS_FAILED_MESSAGE)
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
 * Get the server-derived setup readiness evidence.
 */
export async function getSetupReadiness(options: SetupRequestOptions = {}): Promise<SetupReadiness> {
  const response = await authFetch(`${API_BASE}/readiness`, {
    cache: 'no-store',
    ...(options.signal ? { signal: options.signal } : {}),
  })

  if (!response.ok) {
    throw await parseSetupError(response, SETUP_READINESS_FAILED_MESSAGE)
  }

  return parseSetupReadiness(response)
}

/**
 * Complete setup after all server-required checks pass.
 */
export async function acknowledgeSetup(options: SetupRequestOptions = {}): Promise<SetupReadiness> {
  const response = await authFetch(`${API_BASE}/acknowledge`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({}),
    ...(options.signal ? { signal: options.signal } : {}),
  })
  
  if (!response.ok) {
    throw await parseSetupError(response, ACKNOWLEDGE_SETUP_FAILED_MESSAGE)
  }

  return parseSetupReadiness(response)
}

/**
 * Defer the setup reminder for a bounded number of days.
 */
export async function deferSetup(request: DeferSetupRequest, options: SetupRequestOptions = {}): Promise<SetupReadiness> {
  if (!isSetupDeferDays(request.remind_in_days)) {
    throw new TypeError('无效的设置提醒延期天数')
  }

  const response = await authFetch(`${API_BASE}/defer`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ remind_in_days: request.remind_in_days }),
    ...(options.signal ? { signal: options.signal } : {}),
  })

  if (!response.ok) {
    throw await parseSetupError(response, DEFER_SETUP_FAILED_MESSAGE)
  }

  return parseSetupReadiness(response)
}
