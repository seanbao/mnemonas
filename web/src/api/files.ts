import type { FileItem } from '@/stores/files'
import { readDownloadJsonErrorDetails, triggerBrowserDownload } from '@/lib/downloadResponse'
import { INVALID_API_RESPONSE_MESSAGE } from '@/lib/apiMessages'
import { getNonBlankJsonString, readStructuredJsonErrorDetails } from '@/lib/jsonErrorResponse'
import { ensureZipExtension, sanitizeFilename, normalizePath, encodePathForUrl, getFilenameFromContentDisposition, hasControlCharacter } from '@/lib/utils'
import { authFetch, refreshAuthSession } from './auth'

export type { FileItem }

const API_BASE = '/api/v1'

export const MAX_UPLOAD_FILE_SIZE_BYTES = 10 * 1024 * 1024 * 1024
export const MAX_UPLOAD_FILE_SIZE_LABEL = '10 GB'
export const MAX_DELETE_INTENT_TARGETS = 1000
export const MAX_EMPTY_TRASH_IDS = 1000

export type DeleteMode = 'trash' | 'permanent'

interface FileListResponseBase {
  files: FileItem[]
  path: string
  capabilities?: FileItem['capabilities']
}

export type FileListResponse = FileListResponseBase & (
  | {
      deleteMode: DeleteMode
      deletePolicyToken: string
      trashRetentionDays: number
      trashAutoCleanupEnabled: boolean
    }
  | {
      deleteMode: 'unknown'
      deletePolicyToken: null
      trashRetentionDays: null
      trashAutoCleanupEnabled: null
    }
)

export interface ListFilesOptions {
  signal?: AbortSignal
}

export interface RequestOptions {
  signal?: AbortSignal
}

export interface DeleteFileOptions extends RequestOptions {
  expectedDeleteMode: DeleteMode
  expectedDeletePolicyToken: string
  expectedDeleteTargetToken: string
}

export interface ObservedFileDeleteTarget {
  path: string
  observedIdentityToken: string
}

export interface FileDeleteTarget {
  path: string
  name: string
  isDir: boolean
  size: number
  modTime: string
  deleteIdentityToken: string
  deleteTargetToken: string
}

export interface FileDeleteIntent {
  deleteMode: DeleteMode
  deletePolicyToken: string
  trashRetentionDays: number
  trashAutoCleanupEnabled: boolean
  targets: FileDeleteTarget[]
}

export interface UploadFileOptions {
  signal?: AbortSignal
}

export interface VersionInfo {
  version: number
  hash: string
  size: number
  timestamp: string  // ISO 8601 format from backend
}

// Helper to convert backend version to display format
export function versionToDisplayFormat(v: VersionInfo): { modTime: string; size: number; hash: string } {
  return {
    modTime: v.timestamp,
    size: v.size,
    hash: v.hash,
  }
}

export interface StorageStats {
  fileCount?: number
  fileCountAvailable?: boolean
  storageStatsAvailable?: boolean
  diskStatsAvailable?: boolean
  directoryQuotaStatsAvailable?: boolean
  totalSize?: number
  totalObjects?: number
  uniqueSize?: number
  dedupRatio?: number
  diskTotal?: number
  diskFree?: number
  diskAvailable?: number
  diskUsed?: number
  diskUsageRatio?: number
  diskFilesystemType?: string
  diskMountPoint?: string
  diskMountSource?: string
  diskMountOptions?: string
  diskNativeDataChecksumSupport?: boolean
  directoryQuotas?: DirectoryQuotaUsage[]
}

export type DirectoryQuotaUsageStatus = 'normal' | 'warning' | 'exceeded' | 'missing'

export interface DirectoryQuotaUsage {
  path: string
  quotaBytes: number
  usedBytes: number
  availableBytes: number
  usageRatio: number
  exists: boolean
  status: DirectoryQuotaUsageStatus
}

export interface HealthStatus {
  status: string
  uptime: string
  uptimeSecs?: number
  timestamp?: string
  version?: string
  storage?: {
    dataDir?: string
    writable?: boolean
  }
  dataplane?: {
    healthy?: boolean
    version?: string
    uptime?: number
  }
}

export interface DiskHealthDeviceStatus {
  name?: string
  path: string
  type?: string
  expectedSerial?: string
  serial?: string
  model?: string
  present: boolean
  smartAvailable: boolean
  smartPassed?: boolean
  temperatureC?: number
  powerOnHours?: number
  wearPercentUsed?: number
  availableSparePercent?: number
  availableSpareThresholdPercent?: number
  mediaErrors?: number
  nvmeCriticalWarning?: number
  status: string
  message?: string
  temperatureWarningC?: number
  temperatureCriticalC?: number
}

export interface DiskHealthReport {
  enabled: boolean
  status: string
  checkedAt: string
  devices: DiskHealthDeviceStatus[]
  warnings?: string[]
  message?: string
}

export interface DiagnosticsInfo {
  timestamp: string
  uptime: string
  uptimeSecs?: number
  version: AppVersionInfo
  system?: {
    filesystemInitialized?: boolean
    dataplaneConnected?: boolean
    thumbnailServiceReady?: boolean
    maintenanceHistoryReady?: boolean
    backupManagerReady?: boolean
    activityLogReady?: boolean
    favoritesStoreReady?: boolean
    smbRuntimeReady?: boolean
  }
  memory?: {
    allocMb?: number
    totalAllocMb?: number
    sysMb?: number
    numGc?: number
  }
  goroutines?: number
  filesystem?: {
    trashStatsAvailable?: boolean
    trashItems?: number
    trashSize?: number
    diskStatsAvailable?: boolean
    diskTotal?: number
    diskFree?: number
    diskAvailable?: number
    diskUsed?: number
    diskUsageRatio?: number
    diskFilesystemType?: string
    diskMountPoint?: string
    diskMountSource?: string
    diskMountOptions?: string
    diskNativeDataChecksumSupport?: boolean
  }
  alerts?: {
    enabled?: boolean
    runtimeAvailable?: boolean
    checkInterval?: string
    thresholdPct?: number
    criticalPct?: number
    minFreeBytes?: number
    cooldownPeriod?: string
    webhookConfigured?: boolean
    telegramConfigured?: boolean
    wecomConfigured?: boolean
    dingTalkConfigured?: boolean
    emailConfigured?: boolean
    webhookMethod?: string
    lastLevel?: string
    lastCheckedAt?: string
    lastUsedPct?: number
    lastFreeBytes?: number
  }
  maintenance?: {
    historyReady?: boolean
    scrubScheduleEnabled?: boolean
    scrubScheduleInterval?: string
    scrubRetryInterval?: string
    scrubMaxRetries?: number
    lastScrubStatus?: string
    lastScrubAt?: string
    scrubFailureRetries?: number
  }
  diskHealth?: {
    enabled?: boolean
    runtimeAvailable?: boolean
    checkInterval?: string
    probeTimeout?: string
    cooldownPeriod?: string
    temperatureWarningC?: number
    temperatureCriticalC?: number
    mediaWearWarningPercent?: number
    mediaWearCriticalPercent?: number
    deviceCount?: number
    lastStatus?: string
    lastCheckedAt?: string
    lastWarningCount?: number
    lastDeviceCount?: number
    lastCriticalDevices?: number
    lastWarningDevices?: number
    lastUnavailableDevices?: number
  }
  smb?: {
    enabled?: boolean
    runtimeAvailable?: boolean
    implementation?: string
    listen?: string
    serverName?: string
    signingRequired?: boolean
    encryptionRequired?: boolean
    shareCount?: number
    credentialsReady?: boolean
    gatewayConfigured?: boolean
    message?: string
  }
  storage?: {
    totalChunks?: number
    totalSize?: number
    uniqueSize?: number
    dedupRatio?: number
  }
  dataplane?: {
    healthy?: boolean
    version?: string
    uptimeSec?: number
  }
}

export interface AppVersionInfo {
  name: string
  version: string
  go: string
  buildTime?: string
}

interface BackendAppVersionInfo {
  name: string
  version: string
  go: string
  build_time?: string
}

// API Error class for better error handling
export class ApiError extends Error {
  status: number
  statusText: string
  code?: string
  details?: unknown
  
  constructor(
    message: string,
    status: number,
    statusText: string,
    code?: string,
    details?: unknown,
  ) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.statusText = statusText
    this.code = code
    this.details = details
  }
  
  get isNotFound(): boolean {
    return this.status === 404
  }
  
  get isUnauthorized(): boolean {
    return this.status === 401
  }
  
  get isForbidden(): boolean {
    return this.status === 403
  }
  
  get isServerError(): boolean {
    return this.status >= 500
  }

  get isUnavailable(): boolean {
    return this.status === 503 || this.code === 'SERVICE_UNAVAILABLE'
  }
}

// Helper to handle API responses
async function handleResponse<T>(response: Response, errorPrefix: string): Promise<T> {
  if (!response.ok) {
    let details: { message: string; code?: string; details?: unknown }
    try {
      details = extractApiErrorDetails(await response.json(), errorPrefix)
    } catch {
      // Use status text if JSON parsing fails
      throw new ApiError(`${errorPrefix}: ${response.statusText}`, response.status, response.statusText)
    }

    throw new ApiError(details.message, response.status, response.statusText, details.code, details.details)
  }
  
  try {
    return await response.json()
  } catch {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
}

async function handleWrappedResponse<T>(response: Response, errorPrefix: string): Promise<T> {
  const body = await handleResponse<ApiResponseWrapper<T>>(response, errorPrefix)
  if (
    !body ||
    typeof body !== 'object' ||
    body.success !== true ||
    !('data' in body)
  ) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return body.data
}

async function expectWrappedActionResponse<T>(
  response: Response,
  errorPrefix: string,
  isValid: (value: unknown) => value is T,
) {
  const body = await handleResponse<ApiResponseWrapper<unknown>>(response, errorPrefix)
  if (
    !body ||
    typeof body !== 'object' ||
    body.success !== true ||
    !('data' in body)
  ) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }

  const data = body.data
  if (!isValid(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }

  return {
    warning: response.headers?.get?.('Warning') != null
      || body.warning === true
      || (isRecord(data) && data.warning === true),
    message: getNonBlankJsonString(body.message),
  }
}

async function throwApiErrorFromResponse(response: Response, fallback: string): Promise<never> {
  let message = fallback
  let code: string | undefined
  const structuredDetails = await readStructuredJsonErrorDetails(response, fallback)
  if (structuredDetails) {
    throw new ApiError(structuredDetails.message, response.status, response.statusText, structuredDetails.code)
  }

  try {
    const details = extractApiErrorDetails(await response.json(), fallback)
    message = details.message
    code = details.code
  } catch {
    // Keep the fallback message when the body is missing or invalid.
  }

  throw new ApiError(message, response.status, response.statusText, code)
}

async function throwDownloadApiErrorFromJsonResponse(response: Response): Promise<void> {
  const details = await readDownloadJsonErrorDetails(response, '下载文件失败')
  if (!details) {
    return
  }

  const localizedDetails = localizeDownloadApiErrorDetails(details)
  throw new ApiError(localizedDetails.message, response.status, response.statusText, localizedDetails.code)
}

async function throwDownloadApiErrorFromResponse(response: Response): Promise<never> {
  let message = '下载文件失败'
  let code: string | undefined
  const structuredDetails = await readStructuredJsonErrorDetails(response, message)
  if (structuredDetails) {
    const localizedDetails = localizeDownloadApiErrorDetails(structuredDetails)
    throw new ApiError(localizedDetails.message, response.status, response.statusText, localizedDetails.code)
  }

  try {
    const details = localizeDownloadApiErrorDetails(extractApiErrorDetails(await response.json(), message))
    message = details.message
    code = details.code
  } catch {
    // Keep the fallback message when the body is missing or invalid.
  }

  throw new ApiError(message, response.status, response.statusText, code)
}

function localizeDownloadApiErrorDetails(details: { message: string; code?: string }): { message: string; code?: string } {
  const localized = localizedDownloadApiErrorMessage(details.message)
  return localized ? { ...details, message: localized } : details
}

function localizedDownloadApiErrorMessage(message: string): string | undefined {
  switch (message.trim()) {
    case 'archive contains too many entries':
      return '归档包含的条目过多'
    case 'archive content is too large':
      return '归档内容过大'
    case 'archive contains duplicate entries':
      return '归档条目名称冲突，请刷新后重试'
    case 'archive entry changed during download':
      return '文件内容已变更，请刷新后重试'
    default:
      return undefined
  }
}

function extractApiErrorDetails(body: unknown, fallback: string): {
  message: string
  code?: string
  details?: unknown
} {
  if (!isRecord(body)) {
    return { message: fallback }
  }

  const topLevelCode = getNonBlankJsonString(body.code)
  const details = body.details

  const stringError = getNonBlankJsonString(body.error)
  if (stringError !== undefined) {
    return { message: stringError, code: topLevelCode, details }
  }

  if (isRecord(body.error)) {
    const message = getNonBlankJsonString(body.error.message)
    const code = getNonBlankJsonString(body.error.code) ?? topLevelCode

    if (message !== undefined) {
      return { message, code, details }
    }

    const topLevelMessage = getNonBlankJsonString(body.message)
    if (topLevelMessage !== undefined) {
      return { message: topLevelMessage, code, details }
    }

    return { message: fallback, code, details }
  }

  const topLevelMessage = getNonBlankJsonString(body.message)
  if (topLevelMessage !== undefined) {
    return { message: topLevelMessage, code: topLevelCode, details }
  }

  return { message: fallback, code: topLevelCode, details }
}

function createApiErrorFromXhr(xhr: XMLHttpRequest, fallback: string): ApiError {
  let message = fallback
  let code: string | undefined

  if (xhr.responseText) {
    try {
      const details = extractApiErrorDetails(JSON.parse(xhr.responseText), fallback)
      message = details.message
      code = details.code
    } catch {
      // Fall back to the provided message when the body is not valid JSON.
    }
  }

  return new ApiError(message, xhr.status, xhr.statusText, code)
}

function createAbortError(): Error {
  if (typeof DOMException !== 'undefined') {
    return new DOMException('Upload aborted', 'AbortError')
  }
  const error = new Error('Upload aborted')
  error.name = 'AbortError'
  return error
}

function getActionResultFromXhr(xhr: XMLHttpRequest): ActionResult {
  const warningHeader = typeof xhr.getResponseHeader === 'function'
    ? xhr.getResponseHeader('Warning')
    : null

  let message: string | undefined
  let dataWarning = false

  if (xhr.responseText) {
    try {
      const body = JSON.parse(xhr.responseText)
      if (isRecord(body)) {
        message = getNonBlankJsonString(body.message)

        if (body.warning === true) {
          dataWarning = true
        }

        if (isRecord(body.data) && body.data.warning === true) {
          dataWarning = true
        }
      }
    } catch {
      // Ignore malformed success bodies and preserve upload completion state.
    }
  }

  return {
    warning: warningHeader != null || dataWarning,
    message,
  }
}

// API Response wrapper from backend
interface ApiResponseWrapper<T> {
  success: boolean
  data: T
  warning?: boolean
  message?: string
  timestamp: string
}

export interface ActionResult {
  warning: boolean
  message?: string
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value)
}

function isDiagnosticsVersionShape(value: unknown): value is BackendAppVersionInfo {
  return isRecord(value)
    && typeof value.name === 'string'
    && typeof value.version === 'string'
    && typeof value.go === 'string'
    && isStringOrUndefined(value.build_time)
}

function normalizeAppVersion(value: BackendAppVersionInfo): AppVersionInfo {
  return {
    name: value.name,
    version: value.version,
    go: value.go,
    buildTime: value.build_time,
  }
}

function isNonNegativeSafeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}

function isPositiveSafeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0
}

function isPathActionShape(value: unknown): value is { path: string } {
  return isRecord(value) && isLogicalPathString(value.path)
}

function isFileItemShape(value: unknown): value is FileItem {
  return isRecord(value)
    && typeof value.name === 'string'
    && isLogicalPathString(value.path)
    && typeof value.isDir === 'boolean'
    && isNonNegativeSafeInteger(value.size)
    && typeof value.modTime === 'string'
    && (value.deleteIdentityToken === null || isDeletePolicyToken(value.deleteIdentityToken))
    && isStringOrUndefined(value.etag)
    && (value.capabilities === undefined || isFileCapabilitiesShape(value.capabilities))
}

function isFileCapabilitiesShape(value: unknown): value is NonNullable<FileItem['capabilities']> {
  return isRecord(value)
    && typeof value.read === 'boolean'
    && typeof value.concreteRead === 'boolean'
    && typeof value.write === 'boolean'
}

interface RawFileListResponse extends FileListResponseBase {
  deleteMode?: unknown
  deletePolicyToken?: unknown
  trashRetentionDays?: unknown
  trashAutoCleanupEnabled?: unknown
}

type NormalizedDeletePolicy =
  | {
      deleteMode: DeleteMode
      deletePolicyToken: string
      trashRetentionDays: number
      trashAutoCleanupEnabled: boolean
    }
  | {
      deleteMode: 'unknown'
      deletePolicyToken: null
      trashRetentionDays: null
      trashAutoCleanupEnabled: null
    }

function isFileListResponseShape(value: unknown): value is RawFileListResponse {
  return isRecord(value)
    && isLogicalPathString(value.path)
    && Array.isArray(value.files)
    && value.files.every(isFileItemShape)
    && (value.capabilities === undefined || isFileCapabilitiesShape(value.capabilities))
}

function isDeletePolicyToken(value: unknown): value is string {
  return typeof value === 'string' && /^[0-9a-f]{64}$/.test(value)
}

function isFileDeleteTargetShape(value: unknown): value is FileDeleteTarget {
  if (!isRecord(value)
    || !isLogicalPathString(value.path)
    || value.path === '/'
    || typeof value.name !== 'string'
    || value.name.length === 0
    || typeof value.isDir !== 'boolean'
    || !isNonNegativeSafeInteger(value.size)
    || !isRfc3339Timestamp(value.modTime)
    || !isDeletePolicyToken(value.deleteIdentityToken)
    || !isDeletePolicyToken(value.deleteTargetToken)) {
    return false
  }

  return value.path.slice(value.path.lastIndexOf('/') + 1) === value.name
}

function isFileDeleteIntentShape(value: unknown): value is FileDeleteIntent {
  return isRecord(value)
    && (value.deleteMode === 'trash' || value.deleteMode === 'permanent')
    && isDeletePolicyToken(value.deletePolicyToken)
    && isNonNegativeSafeInteger(value.trashRetentionDays)
    && typeof value.trashAutoCleanupEnabled === 'boolean'
    && Array.isArray(value.targets)
    && value.targets.length > 0
    && value.targets.every(isFileDeleteTargetShape)
}

function normalizeDeletePolicy(data: RawFileListResponse): NormalizedDeletePolicy {
  if (
    (data.deleteMode === 'trash' || data.deleteMode === 'permanent')
    && isDeletePolicyToken(data.deletePolicyToken)
    && isNonNegativeSafeInteger(data.trashRetentionDays)
    && typeof data.trashAutoCleanupEnabled === 'boolean'
  ) {
    return {
      deleteMode: data.deleteMode,
      deletePolicyToken: data.deletePolicyToken,
      trashRetentionDays: data.trashRetentionDays,
      trashAutoCleanupEnabled: data.trashAutoCleanupEnabled,
    }
  }

  return {
    deleteMode: 'unknown',
    deletePolicyToken: null,
    trashRetentionDays: null,
    trashAutoCleanupEnabled: null,
  }
}

function isVersionInfoShape(value: unknown): value is VersionInfo {
  return isRecord(value)
    && isPositiveSafeInteger(value.version)
    && typeof value.hash === 'string'
    && isNonNegativeSafeInteger(value.size)
    && typeof value.timestamp === 'string'
}

function isVersionsResponseShape(value: unknown): value is { path: string, versions: VersionInfo[] } {
  return isRecord(value)
    && isLogicalPathString(value.path)
    && Array.isArray(value.versions)
    && value.versions.every(isVersionInfoShape)
}

function isNonNegativeSafeIntegerOrUndefined(value: unknown): value is number | undefined {
  return value === undefined || isNonNegativeSafeInteger(value)
}

function isNonNegativeFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
}

function isFiniteNumberOrUndefined(value: unknown): value is number | undefined {
  return value === undefined || (typeof value === 'number' && Number.isFinite(value))
}

function getUploadProgressPercent(event: ProgressEvent): number | null {
  if (!event.lengthComputable || !Number.isFinite(event.loaded) || !Number.isFinite(event.total) || event.total <= 0) {
    return null
  }
  return Math.min(100, Math.max(0, (event.loaded / event.total) * 100))
}

function isNonNegativeFiniteNumberOrUndefined(value: unknown): value is number | undefined {
  return value === undefined || isNonNegativeFiniteNumber(value)
}

function isPercentageOrUndefined(value: unknown): value is number | undefined {
  return value === undefined || (isNonNegativeFiniteNumber(value) && value <= 100)
}

function isRatioOrUndefined(value: unknown): value is number | undefined {
  return value === undefined || (isNonNegativeFiniteNumber(value) && value <= 1)
}

function isFiniteRatio(value: unknown): value is number {
  return isNonNegativeFiniteNumber(value) && value <= 1
}

function isStorageStatsShape(value: unknown): value is {
  total_files?: number
  total_files_available?: boolean
  storage_stats_available?: boolean
  disk_stats_available?: boolean
  directory_quota_stats_available?: boolean
  total_size?: number
  total_chunks?: number
  unique_size?: number
  dedup_ratio?: number
  disk_total?: number
  disk_free?: number
  disk_available?: number
  disk_used?: number
  disk_usage_ratio?: number
  disk_filesystem_type?: string
  disk_mount_point?: string
  disk_mount_source?: string
  disk_mount_options?: string
  disk_native_data_checksum_support?: boolean
  directory_quotas?: {
    path: string
    quota_bytes: number
    used_bytes: number
    available_bytes: number
    usage_ratio: number
    exists: boolean
    status: DirectoryQuotaUsageStatus
  }[]
} {
  return isRecord(value)
    && isNonNegativeSafeIntegerOrUndefined(value.total_files)
    && isBooleanOrUndefined(value.total_files_available)
    && isBooleanOrUndefined(value.storage_stats_available)
    && isBooleanOrUndefined(value.disk_stats_available)
    && isBooleanOrUndefined(value.directory_quota_stats_available)
    && isNonNegativeSafeIntegerOrUndefined(value.total_size)
    && isNonNegativeSafeIntegerOrUndefined(value.total_chunks)
    && isNonNegativeSafeIntegerOrUndefined(value.unique_size)
    && isNonNegativeFiniteNumberOrUndefined(value.dedup_ratio)
    && isNonNegativeSafeIntegerOrUndefined(value.disk_total)
    && isNonNegativeSafeIntegerOrUndefined(value.disk_free)
    && isNonNegativeSafeIntegerOrUndefined(value.disk_available)
    && isNonNegativeSafeIntegerOrUndefined(value.disk_used)
    && isRatioOrUndefined(value.disk_usage_ratio)
    && isStringOrUndefined(value.disk_filesystem_type)
    && isStringOrUndefined(value.disk_mount_point)
    && isStringOrUndefined(value.disk_mount_source)
    && isStringOrUndefined(value.disk_mount_options)
    && isBooleanOrUndefined(value.disk_native_data_checksum_support)
    && (value.directory_quotas === undefined || (Array.isArray(value.directory_quotas) && value.directory_quotas.every(isDirectoryQuotaUsageShape)))
}

function isDirectoryQuotaUsageShape(value: unknown): value is {
  path: string
  quota_bytes: number
  used_bytes: number
  available_bytes: number
  usage_ratio: number
  exists: boolean
  status: DirectoryQuotaUsageStatus
} {
  return isRecord(value)
    && isLogicalPathString(value.path)
    && isNonNegativeSafeInteger(value.quota_bytes)
    && isNonNegativeSafeInteger(value.used_bytes)
    && isNonNegativeSafeInteger(value.available_bytes)
    && isNonNegativeFiniteNumber(value.usage_ratio)
    && typeof value.exists === 'boolean'
    && isDirectoryQuotaUsageStatus(value.status)
}

function isDirectoryQuotaUsageStatus(value: unknown): value is DirectoryQuotaUsageStatus {
  return value === 'normal'
    || value === 'warning'
    || value === 'exceeded'
    || value === 'missing'
}

function isMoveCopyActionShape(value: unknown): value is { from: string; to: string } {
  return isRecord(value) && isLogicalPathString(value.from) && isLogicalPathString(value.to)
}

function isRestoreVersionActionShape(value: unknown): value is { path: string; restored: string } {
  return isRecord(value) && isLogicalPathString(value.path) && typeof value.restored === 'string'
}

function isRestoreTrashActionShape(value: unknown): value is { id: string; restored: boolean } {
  return isRecord(value) && typeof value.id === 'string' && typeof value.restored === 'boolean'
}

function isDeleteTrashActionShape(value: unknown): value is { id: string; deleted: boolean } {
  return isRecord(value) && typeof value.id === 'string' && typeof value.deleted === 'boolean'
}

function isTrashItemShape(value: unknown): value is {
  id: string
  originalPath: string
  deletedAt: string
  expiresAt: string
  name: string
  isDir: boolean
  size: number
  hash?: string
  hadVersions?: boolean
} {
  return isRecord(value)
    && typeof value.id === 'string'
    && typeof value.originalPath === 'string'
    && typeof value.deletedAt === 'string'
    && isRfc3339Timestamp(value.expiresAt)
    && typeof value.name === 'string'
    && typeof value.isDir === 'boolean'
    && isNonNegativeSafeInteger(value.size)
    && isStringOrUndefined(value.hash)
    && isBooleanOrUndefined(value.hadVersions)
}

const RFC3339_TIMESTAMP_PATTERN = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|([+-])(\d{2}):(\d{2}))$/

function isRfc3339Timestamp(value: unknown): value is string {
  if (typeof value !== 'string') {
    return false
  }

  const match = RFC3339_TIMESTAMP_PATTERN.exec(value)
  if (!match) {
    return false
  }

  const year = Number(match[1])
  const month = Number(match[2])
  const day = Number(match[3])
  const hour = Number(match[4])
  const minute = Number(match[5])
  const second = Number(match[6])
  const offsetHour = match[8] === undefined ? 0 : Number(match[8])
  const offsetMinute = match[9] === undefined ? 0 : Number(match[9])
  const leapYear = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0)
  const daysInMonth = [31, leapYear ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]

  return month >= 1
    && month <= 12
    && day >= 1
    && day <= daysInMonth[month - 1]!
    && hour <= 23
    && minute <= 59
    && second <= 59
    && offsetHour <= 23
    && offsetMinute <= 59
}

function isTrashListResponseShape(value: unknown): value is {
  items: Array<{
    id: string
    originalPath: string
    deletedAt: string
    expiresAt: string
    name: string
    isDir: boolean
    size: number
    hash?: string
    hadVersions?: boolean
  }>
  count?: number
  totalSize?: number
  retentionDays?: number
  retentionEnabled?: boolean
  retentionMaxSize?: number
  trashAutoCleanupEnabled: boolean
} {
  return isRecord(value)
    && Array.isArray(value.items)
    && value.items.every(isTrashItemShape)
    && isNonNegativeSafeIntegerOrUndefined(value.count)
    && (value.count === undefined || value.count >= value.items.length)
    && isNonNegativeSafeIntegerOrUndefined(value.totalSize)
    && isNonNegativeSafeIntegerOrUndefined(value.retentionDays)
    && isBooleanOrUndefined(value.retentionEnabled)
    && isNonNegativeSafeIntegerOrUndefined(value.retentionMaxSize)
    && typeof value.trashAutoCleanupEnabled === 'boolean'
}

type RawEmptyTrashResult = {
  deleted: string[]
  remaining: string[]
  skipped: string[]
  deleted_count: number
  remaining_count: number
  skipped_count: number
  partial: boolean
  warning: boolean
}

function isValidTrashSelection(ids: unknown): ids is string[] {
  return Array.isArray(ids)
    && ids.length > 0
    && ids.length <= MAX_EMPTY_TRASH_IDS
    && ids.every((id) => typeof id === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(id))
    && new Set(ids).size === ids.length
}

function isOrderedTrashIdGroup(ids: unknown, requestIndexes: Map<string, number>, seen: Set<string>): ids is string[] {
  if (!Array.isArray(ids)) {
    return false
  }

  let previousIndex = -1
  for (const id of ids) {
    if (typeof id !== 'string') {
      return false
    }
    const requestIndex = requestIndexes.get(id)
    if (requestIndex === undefined || requestIndex <= previousIndex || seen.has(id)) {
      return false
    }
    previousIndex = requestIndex
    seen.add(id)
  }
  return true
}

function isEmptyTrashResultShape(value: unknown, requestedIds: string[]): value is RawEmptyTrashResult {
  const expectedKeys = new Set([
    'deleted',
    'remaining',
    'skipped',
    'deleted_count',
    'remaining_count',
    'skipped_count',
    'partial',
    'warning',
  ])
  if (
    !isRecord(value)
    || Object.keys(value).length !== expectedKeys.size
    || Object.keys(value).some((key) => !expectedKeys.has(key))
    || typeof value.partial !== 'boolean'
    || typeof value.warning !== 'boolean'
    || !isNonNegativeSafeInteger(value.deleted_count)
    || !isNonNegativeSafeInteger(value.remaining_count)
    || !isNonNegativeSafeInteger(value.skipped_count)
  ) {
    return false
  }

  const requestIndexes = new Map(requestedIds.map((id, index) => [id, index]))
  const seen = new Set<string>()
  if (
    !isOrderedTrashIdGroup(value.deleted, requestIndexes, seen)
    || !isOrderedTrashIdGroup(value.remaining, requestIndexes, seen)
    || !isOrderedTrashIdGroup(value.skipped, requestIndexes, seen)
    || seen.size !== requestedIds.length
    || value.deleted_count !== value.deleted.length
    || value.remaining_count !== value.remaining.length
    || value.skipped_count !== value.skipped.length
  ) {
    return false
  }

  return value.partial === (value.remaining.length > 0 || value.skipped.length > 0)
}

function isHealthShape(value: unknown): value is HealthStatus & { uptime_secs?: number } {
  if (!isRecord(value) || typeof value.status !== 'string' || typeof value.uptime !== 'string') {
    return false
  }

  if (!isNonNegativeSafeIntegerOrUndefined(value.uptime_secs) || !isStringOrUndefined(value.timestamp) || !isStringOrUndefined(value.version)) {
    return false
  }

  if (value.storage !== undefined) {
    if (!isRecord(value.storage)
      || !isStringOrUndefined(value.storage.dataDir)
      || !isBooleanOrUndefined(value.storage.writable)) {
      return false
    }
  }

  if (value.dataplane !== undefined) {
    if (!isRecord(value.dataplane)
      || !isBooleanOrUndefined(value.dataplane.healthy)
      || !isStringOrUndefined(value.dataplane.version)
      || !isNonNegativeSafeIntegerOrUndefined(value.dataplane.uptime)) {
      return false
    }
  }

  return true
}

function isDiskHealthDeviceShape(value: unknown): value is {
  name?: string
  path: string
  type?: string
  expected_serial?: string
  serial?: string
  model?: string
  present: boolean
  smart_available: boolean
  smart_passed?: boolean
  temperature_c?: number
  power_on_hours?: number
  wear_percent_used?: number
  available_spare_percent?: number
  available_spare_threshold_percent?: number
  media_errors?: number
  nvme_critical_warning?: number
  status: string
  message?: string
  temperature_warning_c?: number
  temperature_critical_c?: number
} {
  return isRecord(value)
    && isStringOrUndefined(value.name)
    && typeof value.path === 'string'
    && isStringOrUndefined(value.type)
    && isStringOrUndefined(value.expected_serial)
    && isStringOrUndefined(value.serial)
    && isStringOrUndefined(value.model)
    && typeof value.present === 'boolean'
    && typeof value.smart_available === 'boolean'
    && isBooleanOrUndefined(value.smart_passed)
    && isFiniteNumberOrUndefined(value.temperature_c)
    && isNonNegativeSafeIntegerOrUndefined(value.power_on_hours)
    && isNonNegativeFiniteNumberOrUndefined(value.wear_percent_used)
    && isPercentageOrUndefined(value.available_spare_percent)
    && isPercentageOrUndefined(value.available_spare_threshold_percent)
    && isNonNegativeSafeIntegerOrUndefined(value.media_errors)
    && isNonNegativeSafeIntegerOrUndefined(value.nvme_critical_warning)
    && typeof value.status === 'string'
    && isStringOrUndefined(value.message)
    && isFiniteNumberOrUndefined(value.temperature_warning_c)
    && isFiniteNumberOrUndefined(value.temperature_critical_c)
}

function isDiskHealthReportShape(value: unknown): value is {
  enabled: boolean
  status: string
  checked_at: string
  devices: Array<{
    name?: string
    path: string
    type?: string
    expected_serial?: string
    serial?: string
    model?: string
    present: boolean
    smart_available: boolean
    smart_passed?: boolean
    temperature_c?: number
    power_on_hours?: number
    wear_percent_used?: number
    available_spare_percent?: number
    available_spare_threshold_percent?: number
    media_errors?: number
    nvme_critical_warning?: number
    status: string
    message?: string
    temperature_warning_c?: number
    temperature_critical_c?: number
  }>
  warnings?: string[]
  message?: string
} {
  return isRecord(value)
    && typeof value.enabled === 'boolean'
    && typeof value.status === 'string'
    && typeof value.checked_at === 'string'
    && Array.isArray(value.devices)
    && value.devices.every(isDiskHealthDeviceShape)
    && (value.warnings === undefined || (Array.isArray(value.warnings) && value.warnings.every((item) => typeof item === 'string')))
    && isStringOrUndefined(value.message)
}

function isBooleanOrUndefined(value: unknown): value is boolean | undefined {
  return value === undefined || typeof value === 'boolean'
}

function isStringOrUndefined(value: unknown): value is string | undefined {
  return value === undefined || typeof value === 'string'
}

function isLogicalPathString(value: unknown): value is string {
  if (typeof value !== 'string' || value.length === 0) {
    return false
  }

  try {
    return normalizePath(value) === value
  } catch {
    return false
  }
}

function normalizeHostAbsolutePath(value: string): string {
  if (hasControlCharacter(value) || value.includes('\\')) {
    throw new Error('非法路径')
  }

  const trimmed = value.trim()
  if (!trimmed.startsWith('/')) {
    throw new Error('非法路径')
  }

  const segments = trimmed.split('/').filter(Boolean)
  if (segments.length === 0 || segments.some((segment) => segment === '.' || segment === '..')) {
    throw new Error('非法路径')
  }

  return `/${segments.join('/')}`
}

function isCanonicalHostAbsolutePath(value: unknown): value is string {
  if (typeof value !== 'string' || value.length === 0) {
    return false
  }

  try {
    return normalizeHostAbsolutePath(value) === value
  } catch {
    return false
  }
}

function normalizeBackupBatchRestoreItems(items: BackupBatchRestoreItemRequest[]): BackupBatchRestoreItemRequest[] {
  return items.map((item) => ({
    ...item,
    job_id: item.job_id.trim(),
    target_path: normalizeHostAbsolutePath(item.target_path),
  }))
}

function isDiagnosticsShape(value: unknown): value is {
  timestamp: string
  uptime: string
  uptime_secs?: number
  version: BackendAppVersionInfo
  system?: {
    filesystem_initialized?: boolean
    dataplane_connected?: boolean
    thumbnail_service_ready?: boolean
    maintenance_history_ready?: boolean
    backup_manager_ready?: boolean
    activity_log_ready?: boolean
    favorites_store_ready?: boolean
    smb_runtime_ready?: boolean
  }
  memory?: {
    alloc_mb?: number
    total_alloc_mb?: number
    sys_mb?: number
    num_gc?: number
  }
  goroutines?: number
  filesystem?: {
    trash_stats_available?: boolean
    trash_items?: number
    trash_size?: number
    disk_stats_available?: boolean
    disk_total?: number
    disk_free?: number
    disk_available?: number
    disk_used?: number
    disk_usage_ratio?: number
    disk_filesystem_type?: string
    disk_mount_point?: string
    disk_mount_source?: string
    disk_mount_options?: string
    disk_native_data_checksum_support?: boolean
  }
  alerts?: {
    enabled?: boolean
    runtime_available?: boolean
    check_interval?: string
    threshold_pct?: number
    critical_pct?: number
    min_free_bytes?: number
    cooldown_period?: string
    webhook_configured?: boolean
    telegram_configured?: boolean
    wecom_configured?: boolean
    dingtalk_configured?: boolean
    email_configured?: boolean
    webhook_method?: string
    last_level?: string
    last_checked_at?: string
    last_used_pct?: number
    last_free_bytes?: number
  }
  maintenance?: {
    history_ready?: boolean
    scrub_schedule_enabled?: boolean
    scrub_schedule_interval?: string
    scrub_retry_interval?: string
    scrub_max_retries?: number
    last_scrub_status?: string
    last_scrub_at?: string
    scrub_failure_retries?: number
  }
  disk_health?: {
    enabled?: boolean
    runtime_available?: boolean
    check_interval?: string
    probe_timeout?: string
    cooldown_period?: string
    temperature_warning_c?: number
    temperature_critical_c?: number
    media_wear_warning_percent?: number
    media_wear_critical_percent?: number
    device_count?: number
    last_status?: string
    last_checked_at?: string
    last_warning_count?: number
    last_device_count?: number
    last_critical_devices?: number
    last_warning_devices?: number
    last_unavailable_devices?: number
  }
  smb?: {
    enabled?: boolean
    runtime_available?: boolean
    implementation?: string
    listen?: string
    server_name?: string
    signing_required?: boolean
    encryption_required?: boolean
    share_count?: number
    credentials_ready?: boolean
    gateway_configured?: boolean
    message?: string
  }
  storage?: {
    total_chunks?: number
    total_size?: number
    unique_size?: number
    dedup_ratio?: number
  }
  dataplane?: {
    healthy?: boolean
    version?: string
    uptime_sec?: number
  }
} {
  if (!isRecord(value) || typeof value.timestamp !== 'string' || typeof value.uptime !== 'string' || !isDiagnosticsVersionShape(value.version)) {
    return false
  }

  if (!isNonNegativeSafeIntegerOrUndefined(value.uptime_secs) || !isNonNegativeSafeIntegerOrUndefined(value.goroutines)) {
    return false
  }

  if (value.system !== undefined) {
    if (!isRecord(value.system)
      || !isBooleanOrUndefined(value.system.filesystem_initialized)
      || !isBooleanOrUndefined(value.system.dataplane_connected)
      || !isBooleanOrUndefined(value.system.thumbnail_service_ready)
      || !isBooleanOrUndefined(value.system.maintenance_history_ready)
      || !isBooleanOrUndefined(value.system.backup_manager_ready)
      || !isBooleanOrUndefined(value.system.activity_log_ready)
      || !isBooleanOrUndefined(value.system.favorites_store_ready)
      || !isBooleanOrUndefined(value.system.smb_runtime_ready)) {
      return false
    }
  }

  if (value.memory !== undefined) {
    if (!isRecord(value.memory)
      || !isNonNegativeSafeIntegerOrUndefined(value.memory.alloc_mb)
      || !isNonNegativeSafeIntegerOrUndefined(value.memory.total_alloc_mb)
      || !isNonNegativeSafeIntegerOrUndefined(value.memory.sys_mb)
      || !isNonNegativeSafeIntegerOrUndefined(value.memory.num_gc)) {
      return false
    }
  }

  if (value.filesystem !== undefined) {
    if (!isRecord(value.filesystem)
      || !isBooleanOrUndefined(value.filesystem.trash_stats_available)
      || !isNonNegativeSafeIntegerOrUndefined(value.filesystem.trash_items)
      || !isNonNegativeSafeIntegerOrUndefined(value.filesystem.trash_size)
      || !isBooleanOrUndefined(value.filesystem.disk_stats_available)
      || !isNonNegativeSafeIntegerOrUndefined(value.filesystem.disk_total)
      || !isNonNegativeSafeIntegerOrUndefined(value.filesystem.disk_free)
      || !isNonNegativeSafeIntegerOrUndefined(value.filesystem.disk_available)
      || !isNonNegativeSafeIntegerOrUndefined(value.filesystem.disk_used)
      || !isRatioOrUndefined(value.filesystem.disk_usage_ratio)
      || !isStringOrUndefined(value.filesystem.disk_filesystem_type)
      || !isStringOrUndefined(value.filesystem.disk_mount_point)
      || !isStringOrUndefined(value.filesystem.disk_mount_source)
      || !isStringOrUndefined(value.filesystem.disk_mount_options)
      || !isBooleanOrUndefined(value.filesystem.disk_native_data_checksum_support)) {
      return false
    }
  }

  if (value.alerts !== undefined) {
    if (!isRecord(value.alerts)
      || !isBooleanOrUndefined(value.alerts.enabled)
      || !isBooleanOrUndefined(value.alerts.runtime_available)
      || !isStringOrUndefined(value.alerts.check_interval)
      || !isPercentageOrUndefined(value.alerts.threshold_pct)
      || !isPercentageOrUndefined(value.alerts.critical_pct)
      || !isNonNegativeSafeIntegerOrUndefined(value.alerts.min_free_bytes)
      || !isStringOrUndefined(value.alerts.cooldown_period)
      || !isBooleanOrUndefined(value.alerts.webhook_configured)
      || !isBooleanOrUndefined(value.alerts.telegram_configured)
      || !isBooleanOrUndefined(value.alerts.wecom_configured)
      || !isBooleanOrUndefined(value.alerts.dingtalk_configured)
      || !isBooleanOrUndefined(value.alerts.email_configured)
      || !isStringOrUndefined(value.alerts.webhook_method)
      || !isStringOrUndefined(value.alerts.last_level)
      || !isStringOrUndefined(value.alerts.last_checked_at)
      || !isPercentageOrUndefined(value.alerts.last_used_pct)
      || !isNonNegativeSafeIntegerOrUndefined(value.alerts.last_free_bytes)) {
      return false
    }
  }

  if (value.maintenance !== undefined) {
    if (!isRecord(value.maintenance)
      || !isBooleanOrUndefined(value.maintenance.history_ready)
      || !isBooleanOrUndefined(value.maintenance.scrub_schedule_enabled)
      || !isStringOrUndefined(value.maintenance.scrub_schedule_interval)
      || !isStringOrUndefined(value.maintenance.scrub_retry_interval)
      || !isNonNegativeSafeIntegerOrUndefined(value.maintenance.scrub_max_retries)
      || !isStringOrUndefined(value.maintenance.last_scrub_status)
      || !isStringOrUndefined(value.maintenance.last_scrub_at)
      || !isNonNegativeSafeIntegerOrUndefined(value.maintenance.scrub_failure_retries)) {
      return false
    }
  }

  if (value.disk_health !== undefined) {
    if (!isRecord(value.disk_health)
      || !isBooleanOrUndefined(value.disk_health.enabled)
      || !isBooleanOrUndefined(value.disk_health.runtime_available)
      || !isStringOrUndefined(value.disk_health.check_interval)
      || !isStringOrUndefined(value.disk_health.probe_timeout)
      || !isStringOrUndefined(value.disk_health.cooldown_period)
      || !isFiniteNumberOrUndefined(value.disk_health.temperature_warning_c)
      || !isFiniteNumberOrUndefined(value.disk_health.temperature_critical_c)
      || !isPercentageOrUndefined(value.disk_health.media_wear_warning_percent)
      || !isPercentageOrUndefined(value.disk_health.media_wear_critical_percent)
      || !isNonNegativeSafeIntegerOrUndefined(value.disk_health.device_count)
      || !isStringOrUndefined(value.disk_health.last_status)
      || !isStringOrUndefined(value.disk_health.last_checked_at)
      || !isNonNegativeSafeIntegerOrUndefined(value.disk_health.last_warning_count)
      || !isNonNegativeSafeIntegerOrUndefined(value.disk_health.last_device_count)
      || !isNonNegativeSafeIntegerOrUndefined(value.disk_health.last_critical_devices)
      || !isNonNegativeSafeIntegerOrUndefined(value.disk_health.last_warning_devices)
      || !isNonNegativeSafeIntegerOrUndefined(value.disk_health.last_unavailable_devices)) {
      return false
    }
  }

  if (value.smb !== undefined) {
    if (!isRecord(value.smb)
      || !isBooleanOrUndefined(value.smb.enabled)
      || !isBooleanOrUndefined(value.smb.runtime_available)
      || !isStringOrUndefined(value.smb.implementation)
      || !isStringOrUndefined(value.smb.listen)
      || !isStringOrUndefined(value.smb.server_name)
      || !isBooleanOrUndefined(value.smb.signing_required)
      || !isBooleanOrUndefined(value.smb.encryption_required)
      || !isNonNegativeSafeIntegerOrUndefined(value.smb.share_count)
      || !isBooleanOrUndefined(value.smb.credentials_ready)
      || !isBooleanOrUndefined(value.smb.gateway_configured)
      || !isStringOrUndefined(value.smb.message)) {
      return false
    }
  }

  if (value.storage !== undefined) {
    if (!isRecord(value.storage)
      || !isNonNegativeSafeIntegerOrUndefined(value.storage.total_chunks)
      || !isNonNegativeSafeIntegerOrUndefined(value.storage.total_size)
      || !isNonNegativeSafeIntegerOrUndefined(value.storage.unique_size)
      || !isNonNegativeFiniteNumberOrUndefined(value.storage.dedup_ratio)) {
      return false
    }
  }

  if (value.dataplane !== undefined) {
    if (!isRecord(value.dataplane)
      || !isBooleanOrUndefined(value.dataplane.healthy)
      || !isStringOrUndefined(value.dataplane.version)
      || !isNonNegativeSafeIntegerOrUndefined(value.dataplane.uptime_sec)) {
      return false
    }
  }

  return true
}

function isScrubErrorShape(value: unknown): value is ScrubError {
  return isRecord(value)
    && typeof value.hash === 'string'
    && typeof value.error_type === 'string'
    && typeof value.message === 'string'
}

function isScrubResultShape(value: unknown): value is ScrubResult {
  if (!isRecord(value) || typeof value.has_result !== 'boolean') {
    return false
  }

  if (value.message !== undefined && typeof value.message !== 'string') {
    return false
  }
  if (value.id !== undefined && typeof value.id !== 'string') {
    return false
  }
  if (value.start_time !== undefined && typeof value.start_time !== 'string') {
    return false
  }
  if (value.end_time !== undefined && typeof value.end_time !== 'string') {
    return false
  }
  if (value.status !== undefined && value.status !== 'running' && value.status !== 'completed' && value.status !== 'failed') {
    return false
  }

  const numericKeys = [
    'total_objects',
    'valid_objects',
    'corrupted_objects',
    'missing_objects',
    'total_size',
    'duration_ms',
  ] as const
  for (const key of numericKeys) {
    if (value[key] !== undefined && !isNonNegativeSafeInteger(value[key])) {
      return false
    }
  }

  if (value.error_message !== undefined && typeof value.error_message !== 'string') {
    return false
  }
  if (value.errors !== undefined && (!Array.isArray(value.errors) || !value.errors.every(isScrubErrorShape))) {
    return false
  }

  return true
}

function isRunScrubResponseShape(value: unknown): value is Omit<ScrubResult, 'has_result'> {
  if (!isRecord(value)) {
    return false
  }

  const candidate: ScrubResult = {
    has_result: true,
    ...value,
  }
  return isScrubResultShape(candidate)
}

export type BackupTaskStatus = 'running' | 'completed' | 'failed'

export interface BackupRunResult {
  id: string
  job_id: string
  status: BackupTaskStatus
  started_at: string
  finished_at?: string
  duration_ms: number
  source: string
  destination: string
  snapshot_path?: string
  manifest_path?: string
  file_count: number
  total_bytes: number
  config_included: boolean
  trigger?: string
  warning?: boolean
  warnings?: string[]
  pruned_snapshots?: number
  error_message?: string
}

export interface BackupRestoreDrillResult {
  id: string
  job_id: string
  status: BackupTaskStatus
  started_at: string
  finished_at?: string
  duration_ms: number
  snapshot_path?: string
  manifest_path?: string
  restored_path?: string
  artifact_kept: boolean
  file_count: number
  verified_bytes: number
  warning?: boolean
  warnings?: string[]
  error_message?: string
  failure_category?: string
}

export interface BackupRestoreDrillStats {
  total_runs: number
  successful_runs: number
  failed_runs: number
  success_rate: number
  consecutive_successes?: number
  consecutive_failures?: number
  latest_success_at?: string
  latest_failure_at?: string
  last_failure_message?: string
  last_failure_category?: string
}

export interface BackupRestoreResult {
  id: string
  job_id: string
  status: BackupTaskStatus
  started_at: string
  finished_at?: string
  duration_ms: number
  snapshot_path?: string
  manifest_path?: string
  target_path: string
  config_restored: boolean
  config_path?: string
  file_count: number
  verified_bytes: number
  preflight_checks?: BackupRestorePreflightCheck[]
  warnings?: string[]
  cutover_checklist?: string[]
  rollback_checklist?: string[]
  error_message?: string
}

export type BackupRestorePreflightStatus = 'passed' | 'warning' | 'failed'

export interface BackupRestorePreflightCheck {
  id: string
  status: BackupRestorePreflightStatus
  title: string
  detail?: string
}

export interface BackupRestorePreviewResult {
  id: string
  job_id: string
  status: BackupTaskStatus
  started_at: string
  finished_at?: string
  duration_ms: number
  source: string
  destination: string
  snapshot_path?: string
  manifest_path?: string
  target_path: string
  file_count: number
  total_bytes: number
  config_available: boolean
  config_included: boolean
  sample_paths?: string[]
  preflight_checks?: BackupRestorePreflightCheck[]
  warnings?: string[]
  cutover_checklist?: string[]
  rollback_checklist?: string[]
  error_message?: string
}

export interface BackupRestoreVerifyResult {
  id: string
  job_id: string
  status: BackupTaskStatus
  started_at: string
  finished_at?: string
  duration_ms: number
  source: string
  destination: string
  snapshot_path?: string
  manifest_path?: string
  target_path: string
  file_count: number
  verified_bytes: number
  config_path?: string
  config_found: boolean
  files_dir_found: boolean
  internal_dir_found: boolean
  index_found: boolean
  objects_dir_found: boolean
  looks_like_storage_root: boolean
  warnings?: string[]
  error_message?: string
}

export interface BackupRetentionCheckResult {
  id: string
  job_id: string
  status: BackupTaskStatus
  started_at: string
  finished_at?: string
  duration_ms: number
  target: string
  policy?: string
  snapshot_count?: number
  file_count?: number
  total_bytes?: number
  oldest_snapshot_at?: string
  latest_snapshot_at?: string
  warning?: boolean
  warnings?: string[]
  error_message?: string
}

export interface BackupBatchRestoreItemRequest {
  job_id: string
  target_path: string
  include_config?: boolean
}

export interface BackupBatchRestorePreviewItemResult {
  index: number
  job_id: string
  target_path: string
  include_config: boolean
  status: BackupTaskStatus
  preview?: BackupRestorePreviewResult
  error_message?: string
}

export interface BackupBatchRestorePreviewResult {
  id: string
  status: BackupTaskStatus
  started_at: string
  finished_at?: string
  duration_ms: number
  items: BackupBatchRestorePreviewItemResult[]
  total_files: number
  total_bytes: number
  warning?: boolean
  warnings?: string[]
  error_message?: string
}

export interface BackupBatchRestoreItemResult {
  index: number
  job_id: string
  target_path: string
  include_config: boolean
  status: BackupTaskStatus
  restore?: BackupRestoreResult
  verify?: BackupRestoreVerifyResult
  warnings?: string[]
  error_message?: string
}

export interface BackupBatchRestoreResult {
  id: string
  status: BackupTaskStatus
  started_at: string
  finished_at?: string
  duration_ms: number
  items: BackupBatchRestoreItemResult[]
  total_files: number
  verified_bytes: number
  warning?: boolean
  warnings?: string[]
  error_message?: string
}

export interface BackupJob {
  id: string
  name: string
  type: string
  source: string
  destination: string
  repository?: string
  remote?: string
  command?: string
  disabled: boolean
  schedule_interval?: string
  schedule_window_start?: string
  schedule_window_end?: string
  next_run_at?: string
  stale_after?: string
  restore_drill_stale_after?: string
  max_snapshots?: number
  max_age?: string
  retention_policy?: string
  retention_status: string
  retention_message?: string
  health_status: string
  health_message?: string
  restore_drill_status: string
  restore_drill_message?: string
  last_restore_drill_reminder_at?: string
  restore_drill_stats?: BackupRestoreDrillStats
  include_config: boolean
  verify_after_backup: boolean
  exclude: string[]
  running: boolean
  last_run?: BackupRunResult
  last_successful_run?: BackupRunResult
  last_restore_drill?: BackupRestoreDrillResult
  restore_drill_history?: BackupRestoreDrillResult[]
  last_restore?: BackupRestoreResult
  last_restore_verify?: BackupRestoreVerifyResult
  last_matching_restore_verify?: BackupRestoreVerifyResult
  restore_report_findings?: string[]
  restore_history?: BackupRestoreResult[]
  last_retention_check?: BackupRetentionCheckResult
}

export interface CreateLocalBackupJobRequest {
  name: string
  destination: string
  schedule_interval?: '0'
}

function isBackupStatus(value: unknown): value is BackupTaskStatus {
  return value === 'running' || value === 'completed' || value === 'failed'
}

function isBackupRestorePreflightStatus(value: unknown): value is BackupRestorePreflightStatus {
  return value === 'passed' || value === 'warning' || value === 'failed'
}

function isStringArrayOrUndefined(value: unknown): value is string[] | undefined {
  return value === undefined || (Array.isArray(value) && value.every((entry) => typeof entry === 'string'))
}

function isBackupRestorePreflightCheckShape(value: unknown): value is BackupRestorePreflightCheck {
  return isRecord(value)
    && typeof value.id === 'string'
    && isBackupRestorePreflightStatus(value.status)
    && typeof value.title === 'string'
    && isStringOrUndefined(value.detail)
}

function isBackupRestorePreflightChecksOrUndefined(value: unknown): value is BackupRestorePreflightCheck[] | undefined {
  return value === undefined || (Array.isArray(value) && value.every(isBackupRestorePreflightCheckShape))
}

function isBackupRunResultShape(value: unknown): value is BackupRunResult {
  return isRecord(value)
    && typeof value.id === 'string'
    && typeof value.job_id === 'string'
    && isBackupStatus(value.status)
    && typeof value.started_at === 'string'
    && isStringOrUndefined(value.finished_at)
    && isNonNegativeSafeInteger(value.duration_ms)
    && typeof value.source === 'string'
    && typeof value.destination === 'string'
    && isStringOrUndefined(value.snapshot_path)
    && isStringOrUndefined(value.manifest_path)
    && isNonNegativeSafeInteger(value.file_count)
    && isNonNegativeSafeInteger(value.total_bytes)
    && typeof value.config_included === 'boolean'
    && isStringOrUndefined(value.trigger)
    && isBooleanOrUndefined(value.warning)
    && (value.warnings === undefined || (Array.isArray(value.warnings) && value.warnings.every((entry) => typeof entry === 'string')))
    && isNonNegativeSafeIntegerOrUndefined(value.pruned_snapshots)
    && isStringOrUndefined(value.error_message)
}

function isBackupRestoreDrillResultShape(value: unknown): value is BackupRestoreDrillResult {
  return isRecord(value)
    && typeof value.id === 'string'
    && typeof value.job_id === 'string'
    && isBackupStatus(value.status)
    && typeof value.started_at === 'string'
    && isStringOrUndefined(value.finished_at)
    && isNonNegativeSafeInteger(value.duration_ms)
    && isStringOrUndefined(value.snapshot_path)
    && isStringOrUndefined(value.manifest_path)
    && (value.restored_path === undefined || isCanonicalHostAbsolutePath(value.restored_path))
    && typeof value.artifact_kept === 'boolean'
    && isNonNegativeSafeInteger(value.file_count)
    && isNonNegativeSafeInteger(value.verified_bytes)
    && isBooleanOrUndefined(value.warning)
    && (value.warnings === undefined || (Array.isArray(value.warnings) && value.warnings.every((entry) => typeof entry === 'string')))
    && isStringOrUndefined(value.error_message)
    && isStringOrUndefined(value.failure_category)
}

function isBackupRestoreDrillStatsShape(value: unknown): value is BackupRestoreDrillStats {
  return isRecord(value)
    && isNonNegativeSafeInteger(value.total_runs)
    && isNonNegativeSafeInteger(value.successful_runs)
    && isNonNegativeSafeInteger(value.failed_runs)
    && isFiniteRatio(value.success_rate)
    && isNonNegativeSafeIntegerOrUndefined(value.consecutive_successes)
    && isNonNegativeSafeIntegerOrUndefined(value.consecutive_failures)
    && isStringOrUndefined(value.latest_success_at)
    && isStringOrUndefined(value.latest_failure_at)
    && isStringOrUndefined(value.last_failure_message)
    && isStringOrUndefined(value.last_failure_category)
}

function isBackupRestoreResultShape(value: unknown): value is BackupRestoreResult {
  return isRecord(value)
    && typeof value.id === 'string'
    && typeof value.job_id === 'string'
    && isBackupStatus(value.status)
    && typeof value.started_at === 'string'
    && isStringOrUndefined(value.finished_at)
    && isNonNegativeSafeInteger(value.duration_ms)
    && isStringOrUndefined(value.snapshot_path)
    && isStringOrUndefined(value.manifest_path)
    && isCanonicalHostAbsolutePath(value.target_path)
    && typeof value.config_restored === 'boolean'
    && (value.config_path === undefined || isCanonicalHostAbsolutePath(value.config_path))
    && isNonNegativeSafeInteger(value.file_count)
    && isNonNegativeSafeInteger(value.verified_bytes)
    && isBackupRestorePreflightChecksOrUndefined(value.preflight_checks)
    && isStringArrayOrUndefined(value.warnings)
    && isStringArrayOrUndefined(value.cutover_checklist)
    && isStringArrayOrUndefined(value.rollback_checklist)
    && isStringOrUndefined(value.error_message)
}

function isBackupRestorePreviewResultShape(value: unknown): value is BackupRestorePreviewResult {
  return isRecord(value)
    && typeof value.id === 'string'
    && typeof value.job_id === 'string'
    && isBackupStatus(value.status)
    && typeof value.started_at === 'string'
    && isStringOrUndefined(value.finished_at)
    && isNonNegativeSafeInteger(value.duration_ms)
    && typeof value.source === 'string'
    && typeof value.destination === 'string'
    && isStringOrUndefined(value.snapshot_path)
    && isStringOrUndefined(value.manifest_path)
    && isCanonicalHostAbsolutePath(value.target_path)
    && isNonNegativeSafeInteger(value.file_count)
    && isNonNegativeSafeInteger(value.total_bytes)
    && typeof value.config_available === 'boolean'
    && typeof value.config_included === 'boolean'
    && (value.sample_paths === undefined || (Array.isArray(value.sample_paths) && value.sample_paths.every((entry) => typeof entry === 'string')))
    && isBackupRestorePreflightChecksOrUndefined(value.preflight_checks)
    && isStringArrayOrUndefined(value.warnings)
    && isStringArrayOrUndefined(value.cutover_checklist)
    && isStringArrayOrUndefined(value.rollback_checklist)
    && isStringOrUndefined(value.error_message)
}

function isBackupRestoreVerifyResultShape(value: unknown): value is BackupRestoreVerifyResult {
  return isRecord(value)
    && typeof value.id === 'string'
    && typeof value.job_id === 'string'
    && isBackupStatus(value.status)
    && typeof value.started_at === 'string'
    && isStringOrUndefined(value.finished_at)
    && isNonNegativeSafeInteger(value.duration_ms)
    && typeof value.source === 'string'
    && typeof value.destination === 'string'
    && isStringOrUndefined(value.snapshot_path)
    && isStringOrUndefined(value.manifest_path)
    && isCanonicalHostAbsolutePath(value.target_path)
    && isNonNegativeSafeInteger(value.file_count)
    && isNonNegativeSafeInteger(value.verified_bytes)
    && (value.config_path === undefined || isCanonicalHostAbsolutePath(value.config_path))
    && typeof value.config_found === 'boolean'
    && typeof value.files_dir_found === 'boolean'
    && typeof value.internal_dir_found === 'boolean'
    && typeof value.index_found === 'boolean'
    && typeof value.objects_dir_found === 'boolean'
    && typeof value.looks_like_storage_root === 'boolean'
    && (value.warnings === undefined || (Array.isArray(value.warnings) && value.warnings.every((entry) => typeof entry === 'string')))
    && isStringOrUndefined(value.error_message)
}

function isBackupRetentionCheckResultShape(value: unknown): value is BackupRetentionCheckResult {
  return isRecord(value)
    && typeof value.id === 'string'
    && typeof value.job_id === 'string'
    && isBackupStatus(value.status)
    && typeof value.started_at === 'string'
    && isStringOrUndefined(value.finished_at)
    && isNonNegativeSafeInteger(value.duration_ms)
    && typeof value.target === 'string'
    && isStringOrUndefined(value.policy)
    && isNonNegativeSafeIntegerOrUndefined(value.snapshot_count)
    && isNonNegativeSafeIntegerOrUndefined(value.file_count)
    && isNonNegativeSafeIntegerOrUndefined(value.total_bytes)
    && isStringOrUndefined(value.oldest_snapshot_at)
    && isStringOrUndefined(value.latest_snapshot_at)
    && isBooleanOrUndefined(value.warning)
    && (value.warnings === undefined || (Array.isArray(value.warnings) && value.warnings.every((entry) => typeof entry === 'string')))
    && isStringOrUndefined(value.error_message)
}

function isBackupBatchRestorePreviewItemResultShape(value: unknown): value is BackupBatchRestorePreviewItemResult {
  return isRecord(value)
    && isNonNegativeSafeInteger(value.index)
    && typeof value.job_id === 'string'
    && isCanonicalHostAbsolutePath(value.target_path)
    && typeof value.include_config === 'boolean'
    && isBackupStatus(value.status)
    && (value.preview === undefined || isBackupRestorePreviewResultShape(value.preview))
    && isStringOrUndefined(value.error_message)
}

function isBackupBatchRestorePreviewResultShape(value: unknown): value is BackupBatchRestorePreviewResult {
  return isRecord(value)
    && typeof value.id === 'string'
    && isBackupStatus(value.status)
    && typeof value.started_at === 'string'
    && isStringOrUndefined(value.finished_at)
    && isNonNegativeSafeInteger(value.duration_ms)
    && Array.isArray(value.items)
    && value.items.every(isBackupBatchRestorePreviewItemResultShape)
    && isNonNegativeSafeInteger(value.total_files)
    && isNonNegativeSafeInteger(value.total_bytes)
    && isBooleanOrUndefined(value.warning)
    && isStringArrayOrUndefined(value.warnings)
    && isStringOrUndefined(value.error_message)
}

function isBackupBatchRestoreItemResultShape(value: unknown): value is BackupBatchRestoreItemResult {
  return isRecord(value)
    && isNonNegativeSafeInteger(value.index)
    && typeof value.job_id === 'string'
    && isCanonicalHostAbsolutePath(value.target_path)
    && typeof value.include_config === 'boolean'
    && isBackupStatus(value.status)
    && (value.restore === undefined || isBackupRestoreResultShape(value.restore))
    && (value.verify === undefined || isBackupRestoreVerifyResultShape(value.verify))
    && isStringArrayOrUndefined(value.warnings)
    && isStringOrUndefined(value.error_message)
}

function isBackupBatchRestoreResultShape(value: unknown): value is BackupBatchRestoreResult {
  return isRecord(value)
    && typeof value.id === 'string'
    && isBackupStatus(value.status)
    && typeof value.started_at === 'string'
    && isStringOrUndefined(value.finished_at)
    && isNonNegativeSafeInteger(value.duration_ms)
    && Array.isArray(value.items)
    && value.items.every(isBackupBatchRestoreItemResultShape)
    && isNonNegativeSafeInteger(value.total_files)
    && isNonNegativeSafeInteger(value.verified_bytes)
    && isBooleanOrUndefined(value.warning)
    && isStringArrayOrUndefined(value.warnings)
    && isStringOrUndefined(value.error_message)
}

function isBackupJobShape(value: unknown): value is BackupJob {
  return isRecord(value)
    && typeof value.id === 'string'
    && typeof value.name === 'string'
    && typeof value.type === 'string'
    && typeof value.source === 'string'
    && typeof value.destination === 'string'
    && isStringOrUndefined(value.repository)
    && isStringOrUndefined(value.remote)
    && isStringOrUndefined(value.command)
    && typeof value.disabled === 'boolean'
    && isStringOrUndefined(value.schedule_interval)
    && isStringOrUndefined(value.schedule_window_start)
    && isStringOrUndefined(value.schedule_window_end)
    && isStringOrUndefined(value.next_run_at)
    && isStringOrUndefined(value.stale_after)
    && isStringOrUndefined(value.restore_drill_stale_after)
    && isNonNegativeSafeIntegerOrUndefined(value.max_snapshots)
    && isStringOrUndefined(value.max_age)
    && isStringOrUndefined(value.retention_policy)
    && typeof value.retention_status === 'string'
    && isStringOrUndefined(value.retention_message)
    && typeof value.health_status === 'string'
    && isStringOrUndefined(value.health_message)
    && typeof value.restore_drill_status === 'string'
    && isStringOrUndefined(value.restore_drill_message)
    && isStringOrUndefined(value.last_restore_drill_reminder_at)
    && (value.restore_drill_stats === undefined || isBackupRestoreDrillStatsShape(value.restore_drill_stats))
    && typeof value.include_config === 'boolean'
    && typeof value.verify_after_backup === 'boolean'
    && Array.isArray(value.exclude)
    && value.exclude.every((entry) => typeof entry === 'string')
    && typeof value.running === 'boolean'
    && (value.last_run === undefined || isBackupRunResultShape(value.last_run))
    && (value.last_successful_run === undefined || isBackupRunResultShape(value.last_successful_run))
    && (value.last_restore_drill === undefined || isBackupRestoreDrillResultShape(value.last_restore_drill))
    && (value.restore_drill_history === undefined || (Array.isArray(value.restore_drill_history) && value.restore_drill_history.every(isBackupRestoreDrillResultShape)))
    && (value.last_restore === undefined || isBackupRestoreResultShape(value.last_restore))
    && (value.last_restore_verify === undefined || isBackupRestoreVerifyResultShape(value.last_restore_verify))
    && (value.last_matching_restore_verify === undefined || isBackupRestoreVerifyResultShape(value.last_matching_restore_verify))
    && isStringArrayOrUndefined(value.restore_report_findings)
    && (value.restore_history === undefined || (Array.isArray(value.restore_history) && value.restore_history.every(isBackupRestoreResultShape)))
    && (value.last_retention_check === undefined || isBackupRetentionCheckResultShape(value.last_retention_check))
}

// List files in a directory
export async function listFiles(path: string, options: ListFilesOptions = {}): Promise<FileListResponse> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const response = await authFetch(`${API_BASE}/files${encodedPath}`, options)
  const data = await handleWrappedResponse<unknown>(response, '获取文件列表失败')
  if (!isFileListResponseShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return {
    ...data,
    ...normalizeDeletePolicy(data),
  }
}

// Get file versions
export async function getVersions(path: string, options: { signal?: AbortSignal } = {}): Promise<VersionInfo[]> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const response = await authFetch(`${API_BASE}/versions${encodedPath}`, options)
  const data = await handleWrappedResponse<unknown>(response, '获取版本历史失败')
  if (!isVersionsResponseShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return data.versions
}

// Create an immutable delete confirmation snapshot for the requested targets.
export async function createFileDeleteIntent(targets: ObservedFileDeleteTarget[], options: RequestOptions = {}): Promise<FileDeleteIntent> {
  if (!Array.isArray(targets) || targets.length === 0) {
    throw new Error('删除目标不能为空')
  }
  if (targets.length > MAX_DELETE_INTENT_TARGETS) {
    throw new Error(`单次最多确认 ${MAX_DELETE_INTENT_TARGETS} 个删除目标`)
  }

  if (targets.some((target) => !isRecord(target)
    || typeof target.path !== 'string'
    || !isDeletePolicyToken(target.observedIdentityToken))) {
    throw new Error('删除目标无效')
  }

  const normalizedTargets = targets.map((target) => ({
    path: normalizePath(target.path),
    observedIdentityToken: target.observedIdentityToken,
  }))
  const normalizedPaths = normalizedTargets.map((target) => target.path)
  if (
    normalizedPaths.some((path) => path === '/')
    || new Set(normalizedPaths).size !== normalizedPaths.length
  ) {
    throw new Error('删除目标无效')
  }

  const response = await authFetch(`${API_BASE}/files-delete-intents`, {
    ...options,
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ targets: normalizedTargets }),
  })
  const data = await handleWrappedResponse<unknown>(response, '获取删除确认信息失败')
  if (!isFileDeleteIntentShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }

  const targetsByPath = new Map(data.targets.map((target) => [target.path, target]))
  if (
    data.targets.length !== normalizedPaths.length
    || targetsByPath.size !== normalizedPaths.length
    || normalizedPaths.some((path) => !targetsByPath.has(path))
    || normalizedTargets.some((target) => targetsByPath.get(target.path)?.deleteIdentityToken !== target.observedIdentityToken)
  ) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }

  return {
    ...data,
    targets: normalizedPaths.map((path) => targetsByPath.get(path)!),
  }
}

// Delete a file using the policy observed when the user confirmed the action.
export async function deleteFile(path: string, options: DeleteFileOptions): Promise<ActionResult> {
  if (options.expectedDeleteMode !== 'trash' && options.expectedDeleteMode !== 'permanent') {
    throw new Error('删除方式无效')
  }
  if (!isDeletePolicyToken(options.expectedDeletePolicyToken)) {
    throw new Error('删除策略令牌无效')
  }
  if (!isDeletePolicyToken(options.expectedDeleteTargetToken)) {
    throw new Error('删除目标令牌无效')
  }
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const query = new URLSearchParams({
    expected_delete_mode: options.expectedDeleteMode,
    expected_delete_policy_token: options.expectedDeletePolicyToken,
    expected_delete_target_token: options.expectedDeleteTargetToken,
  })
  const response = await authFetch(`${API_BASE}/files${encodedPath}?${query.toString()}`, {
    method: 'DELETE',
    signal: options.signal,
  })
  return expectWrappedActionResponse(
    response,
    '删除文件失败',
    (value): value is { path: string } => isPathActionShape(value) && value.path === normalizedPath,
  )
}

// Get storage stats (direct response, not wrapped)
export async function getStorageStats(options: RequestOptions = {}): Promise<StorageStats> {
  const response = await authFetch(`${API_BASE}/stats`, options)
  const data = await handleWrappedResponse<unknown>(response, '获取存储统计失败')
  if (!isStorageStatsShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return {
    fileCount: data.total_files,
    fileCountAvailable: data.total_files_available ?? data.total_files !== undefined,
    storageStatsAvailable: data.storage_stats_available ?? (
      data.total_size !== undefined
      || data.total_chunks !== undefined
      || data.unique_size !== undefined
      || data.dedup_ratio !== undefined
    ),
    diskStatsAvailable: data.disk_stats_available ?? (
      data.disk_total !== undefined
      || data.disk_free !== undefined
      || data.disk_available !== undefined
      || data.disk_used !== undefined
      || data.disk_usage_ratio !== undefined
      || data.disk_filesystem_type !== undefined
      || data.disk_mount_point !== undefined
      || data.disk_mount_source !== undefined
    ),
    directoryQuotaStatsAvailable: data.directory_quota_stats_available ?? data.directory_quotas !== undefined,
    totalSize: data.total_size,
    totalObjects: data.total_chunks,
    uniqueSize: data.unique_size,
    dedupRatio: data.dedup_ratio,
    diskTotal: data.disk_total,
    diskFree: data.disk_free,
    diskAvailable: data.disk_available,
    diskUsed: data.disk_used,
    diskUsageRatio: data.disk_usage_ratio,
    diskFilesystemType: data.disk_filesystem_type,
    diskMountPoint: data.disk_mount_point,
    diskMountSource: data.disk_mount_source,
    diskMountOptions: data.disk_mount_options,
    diskNativeDataChecksumSupport: data.disk_native_data_checksum_support,
    directoryQuotas: data.directory_quotas?.map((quota) => ({
      path: quota.path,
      quotaBytes: quota.quota_bytes,
      usedBytes: quota.used_bytes,
      availableBytes: quota.available_bytes,
      usageRatio: quota.usage_ratio,
      exists: quota.exists,
      status: quota.status,
    })),
  }
}

// Get health status (direct response, not wrapped)
export async function getHealth(options: RequestOptions = {}): Promise<HealthStatus> {
  const response = await fetch('/health', options)
  if (!response.ok) {
    await throwApiErrorFromResponse(response, '获取健康状态失败')
  }

  let body: unknown
  try {
    body = await response.json()
  } catch {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }

  if (!isHealthShape(body)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }

  return {
    status: body.status,
    uptime: body.uptime,
    uptimeSecs: body.uptime_secs,
    timestamp: body.timestamp,
    version: body.version,
    storage: body.storage,
    dataplane: body.dataplane,
  }
}

function normalizeDiskHealthReport(data: {
  enabled: boolean
  status: string
  checked_at: string
  devices: Array<{
    name?: string
    path: string
    type?: string
    expected_serial?: string
    serial?: string
    model?: string
    present: boolean
    smart_available: boolean
    smart_passed?: boolean
    temperature_c?: number
    power_on_hours?: number
    wear_percent_used?: number
    available_spare_percent?: number
    available_spare_threshold_percent?: number
    media_errors?: number
    nvme_critical_warning?: number
    status: string
    message?: string
    temperature_warning_c?: number
    temperature_critical_c?: number
  }>
  warnings?: string[]
  message?: string
}): DiskHealthReport {
  return {
    enabled: data.enabled,
    status: data.status,
    checkedAt: data.checked_at,
    devices: data.devices.map((device) => ({
      name: device.name,
      path: device.path,
      type: device.type,
      expectedSerial: device.expected_serial,
      serial: device.serial,
      model: device.model,
      present: device.present,
      smartAvailable: device.smart_available,
      smartPassed: device.smart_passed,
      temperatureC: device.temperature_c,
      powerOnHours: device.power_on_hours,
      wearPercentUsed: device.wear_percent_used,
      availableSparePercent: device.available_spare_percent,
      availableSpareThresholdPercent: device.available_spare_threshold_percent,
      mediaErrors: device.media_errors,
      nvmeCriticalWarning: device.nvme_critical_warning,
      status: device.status,
      message: getNonBlankJsonString(device.message),
      temperatureWarningC: device.temperature_warning_c,
      temperatureCriticalC: device.temperature_critical_c,
    })),
    warnings: data.warnings,
    message: getNonBlankJsonString(data.message),
  }
}

export async function getDiskHealth(options?: { signal?: AbortSignal }): Promise<DiskHealthReport> {
  const response = await authFetch(`${API_BASE}/maintenance/disk-health`, {
    signal: options?.signal,
  })
  const data = await handleWrappedResponse<unknown>(response, '获取磁盘健康失败')
  if (!isDiskHealthReportShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return normalizeDiskHealthReport(data)
}

export async function getAppVersion(options: RequestOptions = {}): Promise<AppVersionInfo> {
  const response = await authFetch(`${API_BASE}/version`, options)
  const data = await handleWrappedResponse<unknown>(response, '获取版本信息失败')
  if (!isDiagnosticsVersionShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return normalizeAppVersion(data)
}

// Get diagnostics info (direct response, not wrapped)
export async function getDiagnostics(options: RequestOptions = {}): Promise<DiagnosticsInfo> {
  const response = await authFetch(`${API_BASE}/diagnostics`, options)
  const data = await handleWrappedResponse<unknown>(response, '获取诊断信息失败')
  if (!isDiagnosticsShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return {
    timestamp: data.timestamp,
    uptime: data.uptime,
    uptimeSecs: data.uptime_secs,
    version: normalizeAppVersion(data.version),
    system: data.system ? {
      filesystemInitialized: data.system.filesystem_initialized,
      dataplaneConnected: data.system.dataplane_connected,
      thumbnailServiceReady: data.system.thumbnail_service_ready,
      maintenanceHistoryReady: data.system.maintenance_history_ready,
      backupManagerReady: data.system.backup_manager_ready,
      activityLogReady: data.system.activity_log_ready,
      favoritesStoreReady: data.system.favorites_store_ready,
      smbRuntimeReady: data.system.smb_runtime_ready,
    } : undefined,
    memory: data.memory ? {
      allocMb: data.memory.alloc_mb,
      totalAllocMb: data.memory.total_alloc_mb,
      sysMb: data.memory.sys_mb,
      numGc: data.memory.num_gc,
    } : undefined,
    goroutines: data.goroutines,
    filesystem: data.filesystem ? {
      trashStatsAvailable: data.filesystem.trash_stats_available,
      trashItems: data.filesystem.trash_items,
      trashSize: data.filesystem.trash_size,
      diskStatsAvailable: data.filesystem.disk_stats_available,
      diskTotal: data.filesystem.disk_total,
      diskFree: data.filesystem.disk_free,
      diskAvailable: data.filesystem.disk_available,
      diskUsed: data.filesystem.disk_used,
      diskUsageRatio: data.filesystem.disk_usage_ratio,
      diskFilesystemType: data.filesystem.disk_filesystem_type,
      diskMountPoint: data.filesystem.disk_mount_point,
      diskMountSource: data.filesystem.disk_mount_source,
      diskMountOptions: data.filesystem.disk_mount_options,
      diskNativeDataChecksumSupport: data.filesystem.disk_native_data_checksum_support,
    } : undefined,
    alerts: data.alerts ? {
      enabled: data.alerts.enabled,
      runtimeAvailable: data.alerts.runtime_available,
      checkInterval: data.alerts.check_interval,
      thresholdPct: data.alerts.threshold_pct,
      criticalPct: data.alerts.critical_pct,
      minFreeBytes: data.alerts.min_free_bytes,
      cooldownPeriod: data.alerts.cooldown_period,
      webhookConfigured: data.alerts.webhook_configured,
      telegramConfigured: data.alerts.telegram_configured,
      wecomConfigured: data.alerts.wecom_configured,
      dingTalkConfigured: data.alerts.dingtalk_configured,
      emailConfigured: data.alerts.email_configured,
      webhookMethod: data.alerts.webhook_method,
      lastLevel: data.alerts.last_level,
      lastCheckedAt: data.alerts.last_checked_at,
      lastUsedPct: data.alerts.last_used_pct,
      lastFreeBytes: data.alerts.last_free_bytes,
    } : undefined,
    maintenance: data.maintenance ? {
      historyReady: data.maintenance.history_ready,
      scrubScheduleEnabled: data.maintenance.scrub_schedule_enabled,
      scrubScheduleInterval: data.maintenance.scrub_schedule_interval,
      scrubRetryInterval: data.maintenance.scrub_retry_interval,
      scrubMaxRetries: data.maintenance.scrub_max_retries,
      lastScrubStatus: data.maintenance.last_scrub_status,
      lastScrubAt: data.maintenance.last_scrub_at,
      scrubFailureRetries: data.maintenance.scrub_failure_retries,
    } : undefined,
    diskHealth: data.disk_health ? {
      enabled: data.disk_health.enabled,
      runtimeAvailable: data.disk_health.runtime_available,
      checkInterval: data.disk_health.check_interval,
      probeTimeout: data.disk_health.probe_timeout,
      cooldownPeriod: data.disk_health.cooldown_period,
      temperatureWarningC: data.disk_health.temperature_warning_c,
      temperatureCriticalC: data.disk_health.temperature_critical_c,
      mediaWearWarningPercent: data.disk_health.media_wear_warning_percent,
      mediaWearCriticalPercent: data.disk_health.media_wear_critical_percent,
      deviceCount: data.disk_health.device_count,
      lastStatus: data.disk_health.last_status,
      lastCheckedAt: data.disk_health.last_checked_at,
      lastWarningCount: data.disk_health.last_warning_count,
      lastDeviceCount: data.disk_health.last_device_count,
      lastCriticalDevices: data.disk_health.last_critical_devices,
      lastWarningDevices: data.disk_health.last_warning_devices,
      lastUnavailableDevices: data.disk_health.last_unavailable_devices,
    } : undefined,
    smb: data.smb ? {
      enabled: data.smb.enabled,
      runtimeAvailable: data.smb.runtime_available,
      implementation: data.smb.implementation,
      listen: data.smb.listen,
      serverName: data.smb.server_name,
      signingRequired: data.smb.signing_required,
      encryptionRequired: data.smb.encryption_required,
      shareCount: data.smb.share_count,
      credentialsReady: data.smb.credentials_ready,
      gatewayConfigured: data.smb.gateway_configured,
      message: getNonBlankJsonString(data.smb.message),
    } : undefined,
    storage: data.storage ? {
      totalChunks: data.storage.total_chunks,
      totalSize: data.storage.total_size,
      uniqueSize: data.storage.unique_size,
      dedupRatio: data.storage.dedup_ratio,
    } : undefined,
    dataplane: data.dataplane ? {
      healthy: data.dataplane.healthy,
      version: data.dataplane.version,
      uptimeSec: data.dataplane.uptime_sec,
    } : undefined,
  }
}

// Create directory
export async function createDirectory(path: string, options: RequestOptions = {}): Promise<ActionResult> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const response = await authFetch(`${API_BASE}/directories${encodedPath}`, {
    ...options,
    method: 'POST',
  })
  return expectWrappedActionResponse(response, '创建文件夹失败', isPathActionShape)
}

// Upload file
export async function uploadFile(
  path: string,
  file: File,
  onProgress?: (progress: number) => void,
  options: UploadFileOptions = {}
): Promise<ActionResult> {
  // Sanitize filename to prevent path traversal
  const safeFilename = sanitizeFilename(file.name)
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const encodedFilename = encodeURIComponent(safeFilename)
  
  const uploadBase = encodedPath === '/' ? `${API_BASE}/files` : `${API_BASE}/files${encodedPath}`
  const url = `${uploadBase}/${encodedFilename}`

  const sendUpload = (retryCount: number): Promise<ActionResult> => new Promise((resolve, reject) => {
    if (options.signal?.aborted) {
      reject(createAbortError())
      return
    }

    const xhr = new XMLHttpRequest()
    let settled = false

    const cleanup = () => {
      options.signal?.removeEventListener('abort', abortUpload)
    }

    function resolveOnce(result: ActionResult) {
      if (settled) {
        return
      }
      settled = true
      cleanup()
      resolve(result)
    }

    function rejectOnce(error: unknown) {
      if (settled) {
        return
      }
      settled = true
      cleanup()
      reject(error)
    }

    function abortUpload() {
      xhr.abort()
      rejectOnce(createAbortError())
    }

    options.signal?.addEventListener('abort', abortUpload, { once: true })

    xhr.upload.addEventListener('progress', (e) => {
      if (onProgress) {
        const progress = getUploadProgressPercent(e)
        if (progress !== null) {
          onProgress(progress)
        }
      }
    })

    xhr.addEventListener('load', async () => {
      if (options.signal?.aborted) {
        rejectOnce(createAbortError())
        return
      }

      if (xhr.status >= 200 && xhr.status < 300) {
        resolveOnce(getActionResultFromXhr(xhr))
        return
      }

      if (xhr.status === 401 && retryCount === 0) {
        let refreshed = false
        try {
          refreshed = await refreshAuthSession()
        } catch (error) {
          rejectOnce(error)
          return
        }
        if (options.signal?.aborted) {
          rejectOnce(createAbortError())
          return
        }
        if (refreshed) {
          try {
            const retryResult = await sendUpload(retryCount + 1)
            resolveOnce(retryResult)
          } catch (error) {
            rejectOnce(error)
          }
          return
        }
      }

      if (xhr.status === 413) {
        rejectOnce(createApiErrorFromXhr(xhr, `文件超过 ${MAX_UPLOAD_FILE_SIZE_LABEL} 上传限制`))
        return
      }

      rejectOnce(createApiErrorFromXhr(xhr, '上传失败'))
    })

    xhr.addEventListener('error', () => {
      rejectOnce(new Error('网络错误，上传失败'))
    })

    xhr.addEventListener('timeout', () => {
      rejectOnce(new Error('上传超时'))
    })

    xhr.addEventListener('abort', () => {
      rejectOnce(createAbortError())
    })

    // Use REST API instead of WebDAV to avoid Basic Auth popup
    xhr.open('POST', url)
    xhr.withCredentials = true
    xhr.send(file)
  })

  return sendUpload(0)
}

// Download file URL
export function getDownloadUrl(path?: string): string {
  return buildDownloadUrl(path)
}

export function buildDownloadUrl(
  path?: string,
  options?: { version?: string; download?: boolean; archive?: 'zip' }
): string {
  if (!path) return ''
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const params = new URLSearchParams()
  if (options?.version) {
    params.set('version', options.version)
  }
  if (options?.download) {
    params.set('download', 'true')
  }
  if (options?.archive) {
    params.set('archive', options.archive)
  }
  const query = params.toString()
  return query ? `${API_BASE}/download${encodedPath}?${query}` : `${API_BASE}/download${encodedPath}`
}

export async function downloadFile(
  path: string,
  options?: { version?: string; download?: boolean; filename?: string; archive?: 'zip'; signal?: AbortSignal }
): Promise<void> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const params = new URLSearchParams()
  if (options?.version) {
    params.set('version', options.version)
  }
  if (options?.download !== false) {
    params.set('download', 'true')
  }
  if (options?.archive) {
    params.set('archive', options.archive)
  }

  const query = params.toString()
  const url = query ? `${API_BASE}/download${encodedPath}?${query}` : `${API_BASE}/download${encodedPath}`
  const response = await authFetch(url, options?.signal ? { signal: options.signal } : {})

  if (!response.ok) {
    await throwDownloadApiErrorFromResponse(response)
  }

  const pathFilename = normalizedPath.split('/').filter(Boolean).pop() ?? 'download'
  const baseFilename = options?.filename ?? pathFilename
  const fallbackFilename = options?.archive === 'zip' ? ensureZipExtension(baseFilename) : baseFilename
  const contentDisposition = response.headers.get('Content-Disposition')
  await throwDownloadApiErrorFromJsonResponse(response)
  const filename = getFilenameFromContentDisposition(contentDisposition, fallbackFilename)
  const blob = await response.blob()
  triggerBrowserDownload(blob, filename)
}

// Thumbnail URL
export type ThumbnailSize = 'small' | 'medium' | 'large'

export function getThumbnailUrl(path?: string, size: ThumbnailSize = 'medium'): string {
  if (!path) return ''
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const params = new URLSearchParams()
  params.set('size', size)
  return `${API_BASE}/thumbnails${encodedPath}?${params.toString()}`
}

// Rename/Move file
export async function moveFile(fromPath: string, toPath: string, options: RequestOptions = {}): Promise<ActionResult> {
  const normalizedFrom = normalizePath(fromPath)
  const normalizedTo = normalizePath(toPath)
  
  const response = await authFetch(`${API_BASE}/files-move`, {
    ...options,
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      from: normalizedFrom,
      to: normalizedTo,
    }),
  })
  return expectWrappedActionResponse(response, '移动文件失败', isMoveCopyActionShape)
}

// Copy file
export async function copyFile(fromPath: string, toPath: string, options: RequestOptions = {}): Promise<ActionResult> {
  const normalizedFrom = normalizePath(fromPath)
  const normalizedTo = normalizePath(toPath)
  
  const response = await authFetch(`${API_BASE}/files-copy`, {
    ...options,
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      from: normalizedFrom,
      to: normalizedTo,
    }),
  })
  return expectWrappedActionResponse(response, '复制文件失败', isMoveCopyActionShape)
}

// Restore file to a specific version
export async function restoreVersion(path: string, hash: string, options: RequestOptions = {}): Promise<ActionResult> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodeURIComponent(normalizedPath)
  const encodedHash = encodeURIComponent(hash)
  const response = await authFetch(`${API_BASE}/versions/${encodedHash}/restore?path=${encodedPath}`, {
    ...options,
    method: 'POST',
  })
  return expectWrappedActionResponse(response, '恢复版本失败', isRestoreVersionActionShape)
}

// === Trash/Recycle Bin API ===

export interface TrashItem {
  id: string
  originalPath: string
  deletedAt: string  // ISO 8601 format
  expiresAt: string  // RFC 3339 timestamp persisted when the item entered trash
  name: string
  isDir: boolean
  size: number
  hash?: string
  versions?: number
}

export interface EmptyTrashResult {
  deleted: string[]
  remaining: string[]
  skipped: string[]
  partial: boolean
  warning: boolean
  auditWarning: boolean
  message?: string
}

export interface TrashListResponse {
  items: TrashItem[]
  count: number
  totalSize: number
  retentionDays?: number
  retentionEnabled?: boolean
  retentionMaxSize?: number
  trashAutoCleanupEnabled: boolean
}

function trashItemUrl(id: string): string {
  return `${API_BASE}/trash/${encodeURIComponent(id)}`
}

// List trash items
export async function listTrash(options: RequestOptions = {}): Promise<TrashListResponse> {
  const response = await authFetch(`${API_BASE}/trash/`, options)
  const data = await handleWrappedResponse<unknown>(response, '获取回收站列表失败')
  if (!isTrashListResponseShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }

  const items = data.items.map(item => ({
      id: item.id,
      originalPath: item.originalPath,
      deletedAt: item.deletedAt,
      expiresAt: item.expiresAt,
      name: item.name,
      isDir: item.isDir,
      size: item.size,
      hash: item.hash,
      versions: item.hadVersions ? 1 : 0,
    }))
  
  return {
    items,
    count: data.count ?? items.length,
    totalSize: data.totalSize ?? items.reduce((sum, item) => sum + item.size, 0),
    retentionDays: data.retentionDays,
    retentionEnabled: data.retentionEnabled,
    retentionMaxSize: data.retentionMaxSize,
    trashAutoCleanupEnabled: data.trashAutoCleanupEnabled,
  }
}

// Restore item from trash
export async function restoreFromTrash(id: string, newPath?: string, options: RequestOptions = {}): Promise<ActionResult> {
  const baseURL = `${trashItemUrl(id)}/restore`
  const url = newPath 
    ? `${baseURL}?path=${encodeURIComponent(newPath)}`
    : baseURL
  
  const response = await authFetch(url, {
    ...options,
    method: 'POST',
  })
  return expectWrappedActionResponse(response, '恢复文件失败', isRestoreTrashActionShape)
}

// Permanently delete item from trash
export async function deleteFromTrash(id: string, options: RequestOptions = {}): Promise<ActionResult> {
  const response = await authFetch(trashItemUrl(id), {
    ...options,
    method: 'DELETE',
  })
  return expectWrappedActionResponse(
    response,
    '永久删除失败',
    (value): value is { id: string; deleted: true } => (
      isDeleteTrashActionShape(value) && value.id === id && value.deleted === true
    ),
  )
}

// Permanently delete the exact trash items confirmed by the caller.
export async function emptyTrash(ids: string[], options: RequestOptions = {}): Promise<EmptyTrashResult> {
  if (!isValidTrashSelection(ids)) {
    throw new Error('回收站选择无效')
  }

  const response = await authFetch(`${API_BASE}/trash/empty`, {
    ...options,
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids }),
  })
  const body = await handleResponse<ApiResponseWrapper<unknown>>(response, '清空回收站失败')
  if (
    !body
    || typeof body !== 'object'
    || body.success !== true
    || !('data' in body)
  ) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }

  const data = body.data
  if (!isEmptyTrashResultShape(data, ids)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return {
    deleted: [...data.deleted],
    remaining: [...data.remaining],
    skipped: [...data.skipped],
    partial: data.partial,
    warning: data.warning,
    auditWarning: response.headers?.get?.('X-Mnemonas-Audit-Status')?.trim().toLowerCase() === 'failed',
    message: getNonBlankJsonString(body.message),
  }
}

// === Maintenance / Scrub APIs ===

export interface ScrubError {
  hash: string
  error_type: string
  message: string
}

export interface ScrubResult {
  has_result: boolean
  message?: string
  warning?: boolean
  id?: string
  start_time?: string
  end_time?: string
  status?: 'running' | 'completed' | 'failed'
  total_objects?: number
  valid_objects?: number
  corrupted_objects?: number
  missing_objects?: number
  total_size?: number
  duration_ms?: number
  errors?: ScrubError[]
  error_message?: string
}

// Get last scrub result
export async function getScrubResult(options: RequestOptions = {}): Promise<ScrubResult> {
  const response = await authFetch(`${API_BASE}/maintenance/scrub`, options)
  const data = await handleWrappedResponse<unknown>(response, '获取校验结果失败')
  if (!isScrubResultShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return data
}

// Run scrub operation
export async function runScrub(hashes?: string[], options: RequestOptions = {}): Promise<ScrubResult> {
  const response = await authFetch(`${API_BASE}/maintenance/scrub`, {
    method: 'POST',
    headers: hashes?.length ? { 'Content-Type': 'application/json' } : {},
    body: hashes?.length ? JSON.stringify({ hashes }) : undefined,
    signal: options.signal,
  })
  const body = await handleResponse<ApiResponseWrapper<unknown>>(response, '执行数据校验失败')
  if (
    !body
    || typeof body !== 'object'
    || body.success !== true
    || !('data' in body)
  ) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }

  const data = body.data
  if (!isRunScrubResponseShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }

  const result: ScrubResult = {
    has_result: true,
    ...data,
    warning: response.headers?.get?.('Warning') != null
      || body.warning === true
      || (isRecord(data) && data.warning === true),
    message: getNonBlankJsonString(body.message),
  }
  if (!result.status) {
    result.status = 'completed'
  }
  return result
}

// List configured backup jobs
export async function listBackupJobs(options: RequestOptions = {}): Promise<BackupJob[]> {
  const response = await authFetch(`${API_BASE}/maintenance/backups`, options)
  const data = await handleWrappedResponse<unknown>(response, '获取备份任务失败')
  if (!Array.isArray(data) || !data.every(isBackupJobShape)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return data
}

// Create a local backup job using server-managed safety defaults.
export async function createLocalBackupJob(request: CreateLocalBackupJobRequest, options: RequestOptions = {}): Promise<BackupJob> {
  const name = request.name.trim()
  if (!name || hasControlCharacter(name)) {
    throw new Error('非法备份名称')
  }
  if (request.schedule_interval !== undefined && request.schedule_interval !== '0') {
    throw new Error('非法备份计划')
  }

  const normalizedRequest: CreateLocalBackupJobRequest = {
    name,
    destination: normalizeHostAbsolutePath(request.destination),
    ...(request.schedule_interval === '0' ? { schedule_interval: '0' as const } : {}),
  }
  const response = await authFetch(`${API_BASE}/maintenance/backups`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(normalizedRequest),
    signal: options.signal,
  })
  const data = await handleWrappedResponse<unknown>(response, '创建备份任务失败')
  if (!isBackupJobShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return data
}

// Run a backup job immediately
export async function runBackupJob(id: string, options: RequestOptions = {}): Promise<BackupRunResult> {
  const response = await authFetch(`${API_BASE}/maintenance/backups/${encodeURIComponent(id)}/run`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: '{}',
    signal: options.signal,
  })
  const data = await handleWrappedResponse<unknown>(response, '执行备份任务失败')
  if (!isBackupRunResultShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return data
}

// Run a restore drill against the latest completed backup snapshot
export async function runBackupRestoreDrill(id: string, keepArtifact = false, options: RequestOptions = {}): Promise<BackupRestoreDrillResult> {
  const response = await authFetch(`${API_BASE}/maintenance/backups/${encodeURIComponent(id)}/restore-drill`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ keep_artifact: keepArtifact }),
    signal: options.signal,
  })
  const data = await handleWrappedResponse<unknown>(response, '执行恢复演练失败')
  if (!isBackupRestoreDrillResultShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return data
}

// Check backup retention policy and remote/local snapshot visibility
export async function checkBackupRetentionJob(id: string, options: RequestOptions = {}): Promise<BackupRetentionCheckResult> {
  const response = await authFetch(`${API_BASE}/maintenance/backups/${encodeURIComponent(id)}/retention-check`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: '{}',
    signal: options.signal,
  })
  const data = await handleWrappedResponse<unknown>(response, '检查备份保留策略失败')
  if (!isBackupRetentionCheckResultShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return data
}

// Preview a supported backup restore without writing target data
export async function previewBackupRestoreJob(id: string, targetPath: string, includeConfig = false, options: RequestOptions = {}): Promise<BackupRestorePreviewResult> {
  const normalizedTargetPath = normalizeHostAbsolutePath(targetPath)
  const response = await authFetch(`${API_BASE}/maintenance/backups/${encodeURIComponent(id)}/restore-preview`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ target_path: normalizedTargetPath, include_config: includeConfig }),
    signal: options.signal,
  })
  const data = await handleWrappedResponse<unknown>(response, '生成恢复预览失败')
  if (!isBackupRestorePreviewResultShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return data
}

// Preview multiple backup restores without writing target data
export async function previewBatchBackupRestore(items: BackupBatchRestoreItemRequest[], options: RequestOptions = {}): Promise<BackupBatchRestorePreviewResult> {
  const normalizedItems = normalizeBackupBatchRestoreItems(items)
  const response = await authFetch(`${API_BASE}/maintenance/backups/batch-restore-preview`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ items: normalizedItems }),
    signal: options.signal,
  })
  const data = await handleWrappedResponse<unknown>(response, '生成批量恢复预览失败')
  if (!isBackupBatchRestorePreviewResultShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return data
}

// Restore a supported backup job to a safe target directory
export async function restoreBackupJob(id: string, targetPath: string, includeConfig = false, options: RequestOptions = {}): Promise<BackupRestoreResult> {
  const normalizedTargetPath = normalizeHostAbsolutePath(targetPath)
  const response = await authFetch(`${API_BASE}/maintenance/backups/${encodeURIComponent(id)}/restore`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ target_path: normalizedTargetPath, include_config: includeConfig }),
    signal: options.signal,
  })
  const data = await handleWrappedResponse<unknown>(response, '恢复备份失败')
  if (!isBackupRestoreResultShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return data
}

// Restore multiple backup jobs sequentially to safe target directories
export async function runBatchBackupRestore(items: BackupBatchRestoreItemRequest[], options: RequestOptions = {}): Promise<BackupBatchRestoreResult> {
  const normalizedItems = normalizeBackupBatchRestoreItems(items)
  const response = await authFetch(`${API_BASE}/maintenance/backups/batch-restore`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ items: normalizedItems }),
    signal: options.signal,
  })
  const data = await handleWrappedResponse<unknown>(response, '执行批量恢复失败')
  if (!isBackupBatchRestoreResultShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return data
}

// Verify a restored backup target without modifying it
export async function verifyBackupRestoreJob(id: string, targetPath: string, options: RequestOptions = {}): Promise<BackupRestoreVerifyResult> {
  const normalizedTargetPath = normalizeHostAbsolutePath(targetPath)
  const response = await authFetch(`${API_BASE}/maintenance/backups/${encodeURIComponent(id)}/restore-verify`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ target_path: normalizedTargetPath }),
    signal: options.signal,
  })
  const data = await handleWrappedResponse<unknown>(response, '校验恢复目录失败')
  if (!isBackupRestoreVerifyResultShape(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }
  return data
}

// Download a restore summary for one backup job restore state.
export async function downloadBackupRestoreReport(id: string, options?: { signal?: AbortSignal }): Promise<void> {
  const response = await authFetch(
    `${API_BASE}/maintenance/backups/${encodeURIComponent(id)}/restore-report`,
    options?.signal ? { signal: options.signal } : {},
  )
  if (!response.ok) {
    await throwApiErrorFromResponse(response, '导出恢复摘要失败')
  }

  const fallbackFilename = `mnemonas-restore-summary-${id}-${new Date().toISOString().slice(0, 10)}.json`
  const contentDisposition = response.headers.get('Content-Disposition')
  await throwDownloadApiErrorFromJsonResponse(response)
  const filename = getFilenameFromContentDisposition(contentDisposition, fallbackFilename)

  const blob = await response.blob()
  triggerBrowserDownload(blob, filename)
}

// Download diagnostics export
export async function downloadDiagnosticsExport(options?: { signal?: AbortSignal }): Promise<void> {
  const response = await authFetch(`${API_BASE}/diagnostics-export`, options?.signal ? { signal: options.signal } : {})
  if (!response.ok) {
    await throwApiErrorFromResponse(response, '导出诊断信息失败')
  }

  const fallbackFilename = `mnemonas-diagnostics-${new Date().toISOString().slice(0, 10)}.json`
  const contentDisposition = response.headers.get('Content-Disposition')
  await throwDownloadApiErrorFromJsonResponse(response)
  const filename = getFilenameFromContentDisposition(contentDisposition, fallbackFilename)

  const blob = await response.blob()
  triggerBrowserDownload(blob, filename)
}
