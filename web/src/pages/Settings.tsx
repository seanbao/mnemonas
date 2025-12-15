import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useMutation } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { 
  Card, 
  CardBody, 
  CardHeader,
  Button,
  Input,
  Switch,
  Checkbox,
  Divider,
  Tabs,
  Tab,
  addToast,
  Snippet,
} from '@heroui/react'
import { 
  Server, 
  Shield, 
  HardDrive,
  Clock,
  Plus,
  Save,
  RefreshCw,
  Globe,
  Lock,
  User,
  Folder,
  Zap,
  Link2,
  Eye,
  EyeOff,
  Copy,
  CheckCircle2,
  Key,
  AlertCircle,
  Star,
  Trash2,
  Send,
} from 'lucide-react'
import { cn, copyTextToClipboard, parseByteSize, normalizeWebDAVPrefix, isValidWebDAVPrefix, webDAVPrefixOverlapsReservedRoute, formatWebDAVUrl, formatBytes, hasControlCharacter } from '@/lib/utils'
import { GENERIC_LOAD_ERROR_DESCRIPTION, getUserFacingErrorDescription } from '@/lib/apiMessages'
import { getRedactedDiagnosticMessage } from '@/lib/diagnosticMessages'
import { ShareManager, normalizeShareReviewFilter } from '@/components/share'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'
import { useAuthStore, useUser } from '@/stores/auth'
import {
  SettingsError,
  checkDirectoryAccess,
  clearDirectoryAccessReviewRecords,
  createDirectoryAccessReviewRecord,
  getSecurityCheck,
  getSettings,
  getWebDAVCredentials,
  listDirectoryAccessReviewRecords,
  previewDirectoryAccess,
  reportDirectoryAccess,
  sendTestAlert,
  updateSettings,
  type DirectoryAccessCheckData,
  type DirectoryAccessCheckRequest,
  type DirectoryAccessDecision,
  type DirectoryAccessReportData,
  type DirectoryAccessReportRequest,
  type DirectoryAccessReviewRecord,
  type DirectoryAccessReviewRecordCreateRequest,
  type DirectoryAccessPreviewRequest,
  type DirectoryAccessRule,
  type DirectoryAccessRole,
  type DirectoryQuota,
  type DiskHealthDeviceSettings,
  type SecurityCheckData,
  type SecurityCheckItem,
  type SecurityCheckStatus,
  type SharePolicyRule,
  type UpdateSettingsRequest,
} from '@/api/settings'

const MIN_CDC_CHUNK_SIZE_BYTES = 64 * 1024
const MAX_CDC_CHUNK_SIZE_BYTES = 64 * 1024 * 1024
const DEFAULT_VERSIONING_EXTENSIONS = [
  '.md', '.txt', '.org', '.rst', '.tex',
  '.go', '.rs', '.py', '.ts', '.js', '.tsx', '.jsx',
  '.c', '.cpp', '.h', '.java', '.kt', '.swift',
  '.toml', '.yaml', '.yml', '.json', '.xml',
  '.sh', '.bash', '.zsh', '.fish',
].join('\n')
const DEFAULT_VERSIONING_FILENAMES = [
  'Makefile', 'Dockerfile', 'Vagrantfile',
  'LICENSE', 'README', 'CHANGELOG',
  '.gitignore', '.dockerignore', '.editorconfig',
].join('\n')
const REDACTED_SETTINGS_SECRET = '<redacted>'
const BYTE_SIZE_FORMAT_ERROR_DESCRIPTION = '请使用 1024、1 KB、1.5 MB 之类的格式。'
const ALERT_CHANNEL_LABELS: Record<string, string> = {
  webhook: 'Webhook',
  telegram: 'Telegram',
  wecom: '企业微信',
  dingtalk: '钉钉',
  email: 'SMTP 邮件',
}

function formatAlertChannelLabel(channel: string): string {
  const trimmed = channel.trim()
  if (!trimmed) {
    return ''
  }
  return ALERT_CHANNEL_LABELS[trimmed.toLowerCase()] ?? '未知通道'
}

function formatAlertChannelSummary(channels: string[]): string {
  return channels
    .map(formatAlertChannelLabel)
    .filter(Boolean)
    .join(' / ')
}

const getNonBlankToastDescription = getRedactedDiagnosticMessage

function redactSecurityActionToastDescription(description: string): string {
  return getRedactedDiagnosticMessage(description) ?? description
}

function redactWebhookHeaderLine(header: string): string {
  const separator = header.indexOf(':')
  if (separator <= 0) {
    return REDACTED_SETTINGS_SECRET
  }
  return `${header.slice(0, separator).trim()}: ${REDACTED_SETTINGS_SECRET}`
}

const SHARE_POLICY_PRESETS = [
  {
    key: 'family',
    label: '家庭默认',
    description: '7 天有效，最多 20 次访问',
    defaultExpiresIn: '168h',
    defaultMaxAccess: '20',
  },
  {
    key: 'temporary',
    label: '临时协作',
    description: '3 天有效，最多 20 次访问',
    defaultExpiresIn: '72h',
    defaultMaxAccess: '20',
  },
  {
    key: 'public-info',
    label: '资料分发',
    description: '30 天有效，最多 100 次访问',
    defaultExpiresIn: '720h',
    defaultMaxAccess: '100',
  },
] as const

const PUBLIC_ACCESS_MAX_ACCESS_TOKEN_TTL_MS = 60 * 60 * 1000
const PUBLIC_ACCESS_MAX_REFRESH_TOKEN_TTL_MS = 720 * 60 * 60 * 1000
const PUBLIC_ACCESS_MAX_SHARE_DEFAULT_EXPIRES_MS = 720 * 60 * 60 * 1000
const PUBLIC_ACCESS_SNIPPET_COPY_BUTTON_CLASS = 'h-8 w-8 min-w-8 shrink-0'
const DIRECTORY_ACCESS_REVIEW_HISTORY_LIMIT = 5
const DIRECTORY_ACCESS_REVIEW_HISTORY_STORAGE_PREFIX = 'mnemonas_directory_access_review_history'

type DirectoryAccessReviewSaveResult = 'saved' | 'local' | 'failed'

type SharePolicyRuleDraft = SharePolicyRule & {
  max_access_input?: string
  allowed_users_input?: string
  allowed_groups_input?: string
  allowed_roles_input?: string
}

// Settings section component
function SettingsSection({ 
  title, 
  description, 
  icon: Icon, 
  children 
}: { 
  title: string
  description: string
  icon: React.ComponentType<{ size?: number; className?: string }>
  children: React.ReactNode 
}) {
  return (
    <Card className="card-mnemonas">
      <CardHeader className="flex min-w-0 gap-4 pb-2">
        <div className="gradient-mnemonas shrink-0 rounded-lg p-2.5 shadow-sm">
          <Icon size={20} className="text-white" />
        </div>
        <div className="min-w-0 flex-1">
          <h3 className="break-anywhere text-base font-semibold text-foreground">{title}</h3>
          <p className="break-anywhere mt-0.5 text-xs text-default-500">{description}</p>
        </div>
      </CardHeader>
      <CardBody className="pt-2">
        {children}
      </CardBody>
    </Card>
  )
}

function getSettingsLoadErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof SettingsError && error.isUnavailable) {
    return {
      title: '设置服务暂不可用',
      description: '设置当前不可用，请检查设备状态或稍后重试。',
    }
  }

  return {
    title: '加载设置失败',
    description: getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION),
  }
}

function getWebDAVCredentialsErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof SettingsError && error.isUnavailable) {
    return {
      title: 'WebDAV 凭据暂不可用',
      description: '当前无法读取运行中的 WebDAV 凭据，请检查设备状态或稍后重试。',
    }
  }

  return {
    title: 'WebDAV 凭据加载失败',
    description: getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION),
  }
}

function getWebDAVCredentialsRefreshErrorToast(error: unknown): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  if (error instanceof SettingsError && error.isUnavailable) {
    return {
      title: 'WebDAV 凭据暂不可用',
      description: '当前无法读取运行中的 WebDAV 凭据，请检查设备状态或稍后重试。',
      color: 'warning',
    }
  }

  return {
    title: '刷新失败',
    description: getUserFacingErrorDescription(error),
    color: 'danger',
  }
}

function settingsDraftValueEqual(left: unknown, right: unknown): boolean {
  if (Array.isArray(left) || Array.isArray(right)) {
    return JSON.stringify(left) === JSON.stringify(right)
  }
  return left === right
}

function shallowEqualSettingsDraft<T extends Record<string, unknown>>(left: T, right: T): boolean {
  const leftKeys = Object.keys(left)
  if (leftKeys.length !== Object.keys(right).length) {
    return false
  }

  return leftKeys.every((key) => settingsDraftValueEqual(left[key], right[key]))
}

function normalizeBackendMessage(value: string): string {
  return value.trim().replace(/\s+/g, ' ').toLowerCase()
}

function getSettingsActionErrorToast(
  error: unknown,
  titles: {
    unavailable: string
    failure: string
  }
): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  if (error instanceof SettingsError && error.isUnavailable) {
    return {
      title: titles.unavailable,
      description: '设置当前不可用，请检查设备状态或稍后重试。',
      color: 'warning',
    }
  }

  if (error instanceof Error && normalizeBackendMessage(error.message).includes('webdav.username must not match a non-admin user')) {
    return {
      title: 'WebDAV 用户名不可用',
      description: '当前 WebDAV 用户名与现有非管理员账号冲突，请改用管理员账号或其他专用用户名。',
      color: 'warning',
    }
  }

  return {
    title: titles.failure,
    description: getUserFacingErrorDescription(error),
    color: 'danger',
  }
}

function isAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === 'AbortError'
}

function getSettingsSaveSuccessToast(message?: string, warning = false): {
  title: string
  description?: string
  color: 'success' | 'warning'
} {
  if (typeof message === 'string' && message.includes('require restart')) {
    return {
      title: '设置已保存，部分变更需要重启后生效',
      description: '部分配置项需要重启相关服务后才会生效。',
      color: 'warning',
    }
  }

  if (warning) {
    return {
      title: '设置已保存，但存在警告',
      description: getNonBlankToastDescription(message),
      color: 'warning',
    }
  }

  return {
    title: '设置已保存',
    color: 'success',
  }
}

const SETTINGS_TABS = ['general', 'retention', 'webdav', 'advanced', 'shares'] as const

type SettingsTabKey = (typeof SETTINGS_TABS)[number]
type WebDAVAuthType = 'users' | 'basic' | 'none'

function isSettingsTabKey(value: string): value is SettingsTabKey {
  return SETTINGS_TABS.includes(value as SettingsTabKey)
}

function normalizeSettingsTab(value: string | null): SettingsTabKey {
  if (value && isSettingsTabKey(value)) {
    return value
  }

  return 'general'
}

type PublicProxyKind = 'caddy' | 'nginx'

function isValidPublicDomainHostname(value: string): boolean {
  const hostname = value.endsWith('.') ? value.slice(0, -1) : value
  if (!hostname || hostname.length > 253) {
    return false
  }

  const labels = hostname.split('.')
  return labels.every((label) => {
    return label.length > 0
      && label.length <= 63
      && !label.startsWith('-')
      && !label.endsWith('-')
      && /^[a-z0-9-]+$/i.test(label)
  })
}

function normalizePublicDomainInput(value: string): string {
  let trimmed = value.trim()
  if (trimmed === '') {
    return ''
  }

  if (/^https?:\/\//i.test(trimmed)) {
    try {
      const parsed = new URL(trimmed)
      if (
        parsed.username
        || parsed.password
        || parsed.port
        || (parsed.pathname !== '' && parsed.pathname !== '/')
        || parsed.search
        || parsed.hash
      ) {
        return ''
      }
      trimmed = parsed.hostname
    } catch {
      return ''
    }
  } else if (/[/?#@]/.test(trimmed)) {
    return ''
  }

  trimmed = trimmed.toLowerCase()
  if (trimmed.endsWith('.')) {
    trimmed = trimmed.slice(0, -1)
    if (trimmed.endsWith('.')) {
      return ''
    }
  }
  if (trimmed === '' || /[\s/:]/.test(trimmed)) {
    return ''
  }
  if (!isValidPublicDomainHostname(trimmed)) {
    return ''
  }
  return trimmed
}

function publicDomainErrorMessage(value: string): string | undefined {
  if (value.trim() === '') {
    return undefined
  }
  if (normalizePublicDomainInput(value) === '') {
    if (/^https?:\/\//i.test(value) || /[/?#@:]/.test(value)) {
      return '请输入域名，不要包含路径或端口'
    }
    return '请输入有效域名，域名标签只能包含字母、数字和连字符，且不能以连字符开头或结尾'
  }
  return undefined
}

function hasControlChar(value: string): boolean {
  return hasControlCharacter(value)
}

function hasInvalidHTTPHeaderValueChar(value: string): boolean {
  for (const char of value) {
    if (char !== '\t' && hasControlCharacter(char)) {
      return true
    }
  }

  return false
}

function normalizeListenHost(host: string): string {
  const trimmed = host.trim()
  if (trimmed === '*') {
    return ''
  }
  if (
    trimmed.startsWith('[')
    && trimmed.endsWith(']')
    && trimmed.indexOf('[') === 0
    && trimmed.lastIndexOf(']') === trimmed.length - 1
  ) {
    return trimmed.slice(1, -1)
  }
  return trimmed
}

function listensBeyondLoopback(host: string): boolean {
  const normalized = normalizeListenHost(host).toLowerCase()
  if (normalized === '' || normalized === '*' || normalized === '0.0.0.0' || normalized === '::') {
    return true
  }
  if (normalized === 'localhost' || normalized === 'ip6-localhost' || normalized === '::1') {
    return false
  }
  return !normalized.startsWith('127.')
}

function isValidOptionalHTTPURL(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed) {
    return true
  }
  if (/\s/.test(trimmed) || hasControlChar(trimmed)) {
    return false
  }
  if (pathHasBackslashes(trimmed)) {
    return false
  }

  try {
    const parsed = new URL(trimmed)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:'
  } catch {
    return false
  }
}

function urlHostnameForTCPValidation(hostname: string): string {
  if (
    hostname.startsWith('[')
    && hostname.endsWith(']')
    && hostname.indexOf('[') === 0
    && hostname.lastIndexOf(']') === hostname.length - 1
  ) {
    return hostname.slice(1, -1)
  }
  return hostname
}

function isValidShareBaseURL(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed) {
    return true
  }
  if (/\s/.test(trimmed) || hasControlChar(trimmed)) {
    return false
  }
  if (pathHasBackslashes(trimmed)) {
    return false
  }

  try {
    const parsed = new URL(trimmed)
    const rawPathname = rawHTTPURLPathname(trimmed)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return false
    }
    if (parsed.username || parsed.password || trimmed.includes('?') || trimmed.includes('#') || parsed.search || parsed.hash) {
      return false
    }
    if (
      pathHasBackslashes(rawPathname)
      || pathHasEncodedQueryOrFragmentMarkers(rawPathname)
      || pathHasDuplicateSlashes(rawPathname)
      || pathHasDotSegments(rawPathname)
    ) {
      return false
    }
    return isValidTCPHost(urlHostnameForTCPValidation(parsed.hostname))
  } catch {
    return false
  }
}

function isValidDurationString(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed) {
    return false
  }
  return /^(?:\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h))+$/.test(trimmed)
}

function durationStringToMilliseconds(value: string): number | null {
  const trimmed = value.trim()
  if (!isValidDurationString(trimmed)) {
    return null
  }

  const parts = trimmed.match(/\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h)/g)
  if (!parts || parts.join('') !== trimmed) {
    return null
  }

  const unitMilliseconds: Record<string, number> = {
    ns: 0.000001,
    us: 0.001,
    'µs': 0.001,
    ms: 1,
    s: 1000,
    m: 60 * 1000,
    h: 60 * 60 * 1000,
  }
  let total = 0
  for (const part of parts) {
    const match = part.match(/^(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)$/)
    if (!match) {
      return null
    }
    total += Number.parseFloat(match[1]) * unitMilliseconds[match[2]]
  }

  return Number.isFinite(total) ? total : null
}

function isZeroDurationString(value: string): boolean {
  const trimmed = value.trim()
  if (trimmed === '0') {
    return true
  }
  if (!isValidDurationString(trimmed)) {
    return false
  }

  const parts = trimmed.match(/\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h)/g)
  return parts !== null
    && parts.join('') === trimmed
    && parts.every(part => Number.parseFloat(part) === 0)
}

function parseNonNegativeSafeIntegerInput(value: string): { value: number; valid: boolean } {
  const trimmed = value.trim()
  if (!trimmed) {
    return { value: 0, valid: true }
  }
  if (!/^\d+$/.test(trimmed)) {
    return { value: 0, valid: false }
  }
  const parsed = Number(trimmed)
  if (!Number.isSafeInteger(parsed)) {
    return { value: 0, valid: false }
  }
  return { value: parsed, valid: true }
}

function isSafeByteSize(value: number, allowZero: boolean): boolean {
  return Number.isSafeInteger(value) && (allowZero ? value >= 0 : value > 0)
}

function isPositiveDurationString(value: string): boolean {
  return isValidDurationString(value) && !isZeroDurationString(value)
}

function publicAccessDurationRecommendation(value: string, maxMilliseconds: number, fallback: string): string {
  const trimmed = value.trim()
  const parsed = durationStringToMilliseconds(trimmed)
  if (parsed === null || parsed <= 0 || parsed > maxMilliseconds) {
    return fallback
  }
  return trimmed
}

function publicAccessMaxAccessRecommendation(value: string, fallback: string): string {
  const parsed = parseNonNegativeSafeIntegerInput(value)
  if (!parsed.valid || parsed.value <= 0) {
    return fallback
  }
  return String(parsed.value)
}

function isNonNegativeDurationString(value: string): boolean {
  const trimmed = value.trim()
  return trimmed === '0' || isValidDurationString(trimmed)
}

function isValidTCPHost(host: string): boolean {
  const normalized = host.trim().replace(/\.$/, '')
  if (!normalized || /[[\]\s]/.test(normalized) || hasControlChar(normalized) || normalized.length > 253) {
    return false
  }
  if (normalized.includes(':')) {
    try {
      new URL(`http://[${normalized}]/`)
      return true
    } catch {
      return false
    }
  }

  return normalized.split('.').every((label) => (
    label.length > 0
    && label.length <= 63
    && !label.startsWith('-')
    && !label.endsWith('-')
    && /^[A-Za-z0-9-]+$/.test(label)
  ))
}

function isValidListenHost(host: string): boolean {
  const trimmed = host.trim()
  if (/\s/.test(trimmed) || hasControlChar(trimmed)) {
    return false
  }
  const normalized = normalizeListenHost(trimmed)
  return normalized === '' || isValidTCPHost(normalized)
}

function isValidTCPAddress(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed || /\s/.test(trimmed) || hasControlChar(trimmed)) {
    return false
  }

  const ipv6Match = trimmed.match(/^\[([^\]]+)\]:(\d+)$/)
  const hostPortMatch = ipv6Match ?? trimmed.match(/^([^:]+):(\d+)$/)
  if (!hostPortMatch) {
    return false
  }

  const host = hostPortMatch[1]
  const port = Number(hostPortMatch[2])
  return isValidTCPHost(host) && Number.isInteger(port) && port >= 1 && port <= 65535
}

function isValidIPv4Address(value: string): boolean {
  const parts = value.split('.')
  if (parts.length !== 4) {
    return false
  }

  return parts.every((part) => {
    if (!/^\d+$/.test(part)) {
      return false
    }
    const octet = Number(part)
    return Number.isInteger(octet) && octet >= 0 && octet <= 255
  })
}

function isValidIPv6Address(value: string): boolean {
  if (!value || /[\s[\]%]/.test(value) || hasControlChar(value)) {
    return false
  }

  let address = value
  let embeddedIPv4Groups = 0
  if (address.includes('.')) {
    const lastColon = address.lastIndexOf(':')
    if (lastColon < 0 || !isValidIPv4Address(address.slice(lastColon + 1))) {
      return false
    }
    const prefix = address.slice(0, lastColon)
    if (!prefix) {
      return false
    }
    address = prefix.endsWith(':') ? `${prefix}:` : prefix
    embeddedIPv4Groups = 2
  }

  const compressedParts = address.split('::')
  if (compressedParts.length > 2) {
    return false
  }

  const parseGroups = (part: string): string[] | null => {
    if (!part) {
      return []
    }
    const groups = part.split(':')
    if (groups.some(group => !/^[0-9A-Fa-f]{1,4}$/.test(group))) {
      return null
    }
    return groups
  }

  const leftGroups = parseGroups(compressedParts[0])
  const rightGroups = parseGroups(compressedParts[1] ?? '')
  if (!leftGroups || !rightGroups) {
    return false
  }

  const groupCount = leftGroups.length + rightGroups.length + embeddedIPv4Groups
  if (compressedParts.length === 2) {
    return groupCount < 8
  }
  return groupCount === 8
}

function ipAddressKind(value: string): 'ipv4' | 'ipv6' | null {
  if (isValidIPv4Address(value)) {
    return 'ipv4'
  }
  if (isValidIPv6Address(value)) {
    return 'ipv6'
  }
  return null
}

function ipv4Octets(value: string): number[] | null {
  if (!isValidIPv4Address(value)) {
    return null
  }
  return value.split('.').map((part) => Number(part))
}

function isLoopbackIP(value: string): boolean {
  const kind = ipAddressKind(value)
  if (kind === 'ipv4') {
    return value.startsWith('127.')
  }
  if (kind === 'ipv6') {
    return value.toLowerCase() === '::1'
  }
  return false
}

function isPrivateOrLinkLocalIP(value: string): boolean {
  const octets = ipv4Octets(value)
  if (octets) {
    const [first, second] = octets
    return first === 10
      || (first === 172 && second >= 16 && second <= 31)
      || (first === 192 && second === 168)
      || (first === 169 && second === 254)
  }

  if (!isValidIPv6Address(value)) {
    return false
  }
  const lower = value.toLowerCase()
  return lower.startsWith('fc')
    || lower.startsWith('fd')
    || lower.startsWith('fe8')
    || lower.startsWith('fe9')
    || lower.startsWith('fea')
    || lower.startsWith('feb')
}

function trustedProxySourceFromSecurityCheck(check: SecurityCheckItem): string | undefined {
  const remoteIP = typeof check.details?.remote_ip === 'string' ? check.details.remote_ip.trim() : ''
  if (!remoteIP || ipAddressKind(remoteIP) === null || isLoopbackIP(remoteIP)) {
    return undefined
  }
  return isPrivateOrLinkLocalIP(remoteIP) ? remoteIP : undefined
}

function pathEndsWithShareRoute(pathname: string): boolean {
  const trimmedPath = decodeEscapedPathSlashes(pathname).replace(/\/+$/u, '')
  return trimmedPath === '/s' || trimmedPath.endsWith('/s')
}

function pathHasBackslashes(pathname: string): boolean {
  return /\\|%5c/iu.test(pathname)
}

function pathHasEncodedQueryOrFragmentMarkers(pathname: string): boolean {
  return /%(?:3f|23)/iu.test(pathname)
}

function pathHasDuplicateSlashes(pathname: string): boolean {
  return /\/{2,}/u.test(decodeEscapedPathSlashes(pathname))
}

function pathHasDotSegments(pathname: string): boolean {
  return decodeEscapedPathDots(decodeEscapedPathSlashes(pathname))
    .split('/')
    .some(segment => segment === '.' || segment === '..')
}

function collapseDuplicatePathSlashes(pathname: string): string {
  return decodeEscapedPathSlashes(pathname).replace(/\/{2,}/gu, '/')
}

function stripEncodedQueryFragmentMarkers(pathname: string): string {
  const markerIndex = pathname.search(/%(?:3f|23)/iu)
  if (markerIndex < 0) {
    return pathname
  }
  return pathname.slice(0, markerIndex) || '/'
}

function decodeEscapedPathSlashes(pathname: string): string {
  return pathname.replace(/%2f/giu, '/')
}

function decodeEscapedPathDots(pathname: string): string {
  return pathname.replace(/%2e/giu, '.')
}

function decodeEscapedPathSeparators(pathname: string): string {
  return decodeEscapedPathSlashes(pathname).replace(/\\|%5c/giu, '/')
}

function rawHTTPURLPathname(value: string): string {
  const withoutScheme = value.replace(/^[a-z][a-z0-9+.-]*:\/\//iu, '')
  const pathStart = withoutScheme.search(/[/?#\\]/u)
  if (pathStart < 0 || (withoutScheme[pathStart] !== '/' && withoutScheme[pathStart] !== '\\')) {
    return '/'
  }
  const rawPath = withoutScheme.slice(pathStart)
  const queryStart = rawPath.search(/[?#]/u)
  return queryStart >= 0 ? rawPath.slice(0, queryStart) || '/' : rawPath || '/'
}

function stripShareRouteSuffix(pathname: string): string {
  const trimmedPath = pathname.replace(/\/+$/u, '')
  if (!pathEndsWithShareRoute(pathname)) {
    return pathname
  }
  const strippedPath = trimmedPath.slice(0, -2)
  return strippedPath === '' ? '/' : `${strippedPath}/`
}

function securityCheckHasWebDAVPasswordRisk(check: SecurityCheckItem): boolean {
  return check.id === 'webdav_auth'
    && typeof check.details?.password_risk === 'string'
    && check.details.password_risk.trim() !== ''
}

function securityCheckUsesGeneratedWebDAVPassword(check: SecurityCheckItem): boolean {
  return check.id === 'webdav_auth'
    && check.details?.password_source === 'generated'
}

function securityCheckHasUnavailableGeneratedWebDAVPassword(check: SecurityCheckItem): boolean {
  return securityCheckUsesGeneratedWebDAVPassword(check)
    && check.details?.generated_password_available === false
}

function securityCheckHasTrustedNonHTTPSForwardedProto(check: SecurityCheckItem): boolean {
  if (check.id !== 'forwarded_proto_trust') {
    return false
  }
  const forwardedProto = typeof check.details?.forwarded_proto === 'string'
    ? check.details.forwarded_proto.trim().toLowerCase()
    : ''
  return forwardedProto !== ''
    && forwardedProto !== 'https'
    && check.details?.trusted_forwarded_source === true
}

function securityCheckStringDetail(check: SecurityCheckItem, key: string): string {
  const value = check.details?.[key]
  return typeof value === 'string' ? value.trim() : ''
}

function securityCheckNumberDetail(check: SecurityCheckItem, key: string): number | undefined {
  const value = check.details?.[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function securityCheckShareBaseURLHasNonDefaultHTTPSPort(check: SecurityCheckItem): boolean {
  const baseURL = securityCheckStringDetail(check, 'base_url')
  if (!baseURL) {
    return false
  }
  try {
    const parsed = new URL(baseURL)
    return parsed.protocol === 'https:' && parsed.port !== '' && parsed.port !== '443'
  } catch {
    return false
  }
}

function getBackupLocalDestinationFixDescription(check: SecurityCheckItem): string {
  const destination = securityCheckStringDetail(check, 'destination')
  const jobID = securityCheckStringDetail(check, 'job_id')
  const subject = destination
    ? `备份作业 ${jobID || '<unknown>'} 的目标目录 ${destination}`
    : '本地备份作业目标目录'
  const symlinkComponent = securityCheckStringDetail(check, 'symlink_component')

  switch (securityCheckStringDetail(check, 'destination_kind')) {
    case 'inside_storage_root':
      return `将${subject}移出 storage.root，改到独立磁盘、独立数据集或远端备份目标；迁移后重新运行安全自检。`
    case 'inside_source':
      return `将${subject}移出备份来源目录，避免源数据和备份一起损坏或被同步删除；迁移后重新运行安全自检。`
    case 'symlink_component':
      return symlinkComponent
        ? `将${subject}改为不经过符号链接组件 ${symlinkComponent} 的普通目录，或改用独立磁盘、独立数据集或远端备份目标。`
        : `将${subject}改为不经过符号链接组件的普通目录，或改用独立磁盘、独立数据集或远端备份目标。`
    case 'symlink':
      return `将${subject}从符号链接改为普通目录，或改用独立磁盘、独立数据集或远端备份目标。`
    case 'not_directory':
      return `将${subject}恢复为普通目录，或改用独立磁盘、独立数据集或远端备份目标。`
    case 'missing':
      return `确认${subject}的父目录已挂载，并且 MnemoNAS 服务账号可以创建目标目录；长期备份目标建议提前创建并监控挂载状态。`
    case 'not_writable':
      return `为${subject}授予 MnemoNAS 服务账号写权限，或改用服务账号可写的独立备份目录；修复后重新运行安全自检。`
    case 'relative':
      return `将${subject}改为绝对路径，并放在 storage.root 和备份来源目录之外的独立位置。`
    default:
      return destination
        ? `在服务器上检查${subject}，确认它不是符号链接、不在主存储或来源目录内，并且 MnemoNAS 服务账号可以写入。`
        : '在服务器上检查本地备份作业目标目录，确认它不是符号链接、不在主存储或来源目录内，并且 MnemoNAS 服务账号可以写入。'
  }
}

function shareDefaultExpiresNeedsSecurityRepair(check: SecurityCheckItem): boolean {
  if (check.details?.default_expires_in_unlimited === true || check.details?.default_expires_in_too_long === true) {
    return true
  }
  const seconds = securityCheckNumberDetail(check, 'default_expires_in_seconds')
  return seconds !== undefined && (seconds <= 0 || seconds > 720 * 60 * 60)
}

function shareDefaultMaxAccessNeedsSecurityRepair(check: SecurityCheckItem): boolean {
  if (check.details?.default_max_access_unlimited === true) {
    return true
  }
  const maxAccess = securityCheckNumberDetail(check, 'default_max_access')
  return maxAccess !== undefined && maxAccess <= 0
}

function httpsShareBaseURLFromSecurityCheck(check: SecurityCheckItem): string {
  const baseURL = typeof check.details?.base_url === 'string' ? check.details.base_url.trim() : ''
  if (!baseURL) {
    return ''
  }

  try {
    const parsed = new URL(baseURL)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return ''
    }
    parsed.protocol = 'https:'
    parsed.username = ''
    parsed.password = ''
    parsed.port = ''
    parsed.search = ''
    parsed.hash = ''
    const requestHost = typeof check.details?.request_host === 'string'
      ? check.details.request_host.trim().toLowerCase()
      : ''
    if (requestHost && !/[\s/:]/.test(requestHost)) {
      parsed.hostname = requestHost
    }
    parsed.pathname = stripShareRouteSuffix(
      collapseDuplicatePathSlashes(
        decodeEscapedPathSeparators(stripEncodedQueryFragmentMarkers(parsed.pathname)),
      ),
    )

    if (parsed.pathname === '/' && parsed.search === '') {
      return parsed.origin
    }
    return parsed.toString()
  } catch {
    return ''
  }
}

function shareBaseURLPublicReviewMessage(value: string): string | undefined {
  const trimmed = value.trim()
  if (!trimmed) {
    return undefined
  }

  try {
    const parsed = new URL(trimmed)
    const rawPathname = rawHTTPURLPathname(trimmed)
    if (parsed.protocol === 'http:') {
      return '公网分享建议使用 HTTPS 基础 URL；HTTP 链接只适用于内网或受控测试环境。'
    }
    if (parsed.protocol === 'https:' && parsed.port && parsed.port !== '443') {
      return 'HTTPS 非标准端口需要额外公网入口和防火墙规则；公网分享建议使用默认 443 端口。'
    }
    if (pathHasBackslashes(rawPathname)) {
      return '路径包含反斜杠；公网部署中代理或浏览器可能规范化为不同的分享地址。'
    }
    if (pathHasEncodedQueryOrFragmentMarkers(rawPathname)) {
      return '路径包含编码后的查询或片段标记；公网部署中代理或浏览器可能规范化为不同的分享地址。'
    }
    if (pathHasDuplicateSlashes(rawPathname)) {
      return '路径包含重复斜杠；公网部署中代理或浏览器可能规范化为不同的分享地址。'
    }
    if (pathHasDotSegments(rawPathname)) {
      return '路径包含 . 或 .. 路径段；公网部署中代理或浏览器可能规范化为不同的分享地址。'
    }
    if (pathEndsWithShareRoute(rawPathname)) {
      return '基础 URL 已包含 /s 分享路由，生成的链接会出现重复的 /s/s。'
    }
  } catch {
    return undefined
  }

  return undefined
}

function loopbackAddressWithOriginalPort(address: string, fallback: string): string {
  const trimmed = address.trim()
  if (!trimmed) {
    return fallback
  }

  try {
    const parsed = new URL(`tcp://${trimmed}`)
    const port = Number(parsed.port)
    if (Number.isInteger(port) && port >= 1 && port <= 65535) {
      return `127.0.0.1:${port}`
    }
  } catch {
    const match = trimmed.match(/^\[[^\]]+\]:(\d+)$/) ?? trimmed.match(/^[^:[\]\s]+:(\d+)$/)
    const port = match ? Number(match[1]) : 0
    if (Number.isInteger(port) && port >= 1 && port <= 65535) {
      return `127.0.0.1:${port}`
    }
  }

  return fallback
}

function dataplaneLoopbackAddressFromSecurityCheck(check: SecurityCheckItem, currentAddress: string): string {
  const detailAddress = typeof check.details?.grpc_address === 'string' ? check.details.grpc_address : ''
  return loopbackAddressWithOriginalPort(detailAddress || currentAddress, '127.0.0.1:9090')
}

function dataplaneHTTPLoopbackAddressFromSecurityCheck(check: SecurityCheckItem): string {
  const detailAddress = typeof check.details?.http_address === 'string' ? check.details.http_address : ''
  return loopbackAddressWithOriginalPort(detailAddress, '127.0.0.1:9091')
}

function appendTrustedProxySourceCIDR(currentValue: string, source: string): string {
  const lines = currentValue.split(/\r?\n/).map((line) => line.trim()).filter(Boolean)
  if (!lines.includes(source)) {
    lines.push(source)
  }
  return lines.join('\n')
}

function isValidTrustedProxyCIDR(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed || trimmed !== value || /\s/.test(trimmed) || hasControlChar(trimmed)) {
    return false
  }

  const parts = trimmed.split('/')
  if (parts.length === 1) {
    return ipAddressKind(trimmed) !== null
  }
  if (parts.length !== 2) {
    return false
  }

  const kind = ipAddressKind(parts[0])
  if (!kind || !/^\d+$/.test(parts[1])) {
    return false
  }

  const prefixLength = Number(parts[1])
  const maxPrefixLength = kind === 'ipv4' ? 32 : 128
  return Number.isInteger(prefixLength) && prefixLength >= 0 && prefixLength <= maxPrefixLength
}

const httpHeaderNamePattern = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/

function splitWebhookHeaderLine(header: string): { name: string; value: string } | null {
  const separator = header.indexOf(':')
  if (separator <= 0 || separator === header.length - 1) {
    return null
  }

  const name = header.slice(0, separator).trim()
  const value = header.slice(separator + 1).trim()
  if (!name || !value) {
    return null
  }

  return { name, value }
}

function isValidWebhookHeaderLine(header: string): boolean {
  const parts = splitWebhookHeaderLine(header)
  if (!parts) {
    return false
  }

  const { name, value } = parts
  return httpHeaderNamePattern.test(name) && value.length > 0 && !hasInvalidHTTPHeaderValueChar(value)
}

function redactedWebhookHeaderNameCounts(headersText: string): Map<string, number> {
  const counts = new Map<string, number>()
  for (const header of headersText.split('\n')) {
    const parts = splitWebhookHeaderLine(header.trim())
    if (!parts || parts.value !== REDACTED_SETTINGS_SECRET) {
      continue
    }
    const name = parts.name.toLowerCase()
    counts.set(name, (counts.get(name) ?? 0) + 1)
  }
  return counts
}

function findUnknownRedactedWebhookHeader(headers: string[], savedHeadersText: string): string | null {
  const savedHeaderCounts = redactedWebhookHeaderNameCounts(savedHeadersText)

  for (const header of headers) {
    const parts = splitWebhookHeaderLine(header)
    if (!parts || parts.value !== REDACTED_SETTINGS_SECRET) {
      continue
    }

    const name = parts.name.toLowerCase()
    const remaining = savedHeaderCounts.get(name) ?? 0
    if (remaining <= 0) {
      return parts.name
    }
    savedHeaderCounts.set(name, remaining - 1)
  }

  return null
}

function findDuplicateWebhookHeaderName(headers: string[]): string | null {
  const seen = new Set<string>()
  for (const header of headers) {
    const parts = splitWebhookHeaderLine(header)
    if (!parts) {
      continue
    }
    const name = parts.name.toLowerCase()
    if (seen.has(name)) {
      return parts.name
    }
    seen.add(name)
  }
  return null
}

function formatDirectoryQuotaLines(quotas: DirectoryQuota[] | undefined): string {
  return (quotas ?? [])
    .map((quota) => `${formatLogicalPathLineToken(quota.path)} ${formatBytes(quota.quota_bytes)}`)
    .join('\n')
}

function normalizeDirectoryQuotaPathInput(value: string): string | null {
  const trimmed = value.trim()
  if (!trimmed.startsWith('/') || /[\\?#]/.test(trimmed) || hasControlChar(trimmed)) {
    return null
  }
  if (trimmed.split('/').some((segment) => segment === '.' || segment === '..')) {
    return null
  }
  const collapsed = trimmed.replace(/\/+/g, '/')
  return collapsed === '/' ? '/' : collapsed.replace(/\/+$/, '')
}

const logicalPathInputErrorDescription = '路径必须是站内绝对路径，且不能包含反斜杠、?、#、控制字符、. 或 .. 路径段。'

function formatLogicalPathLineToken(path: string): string {
  const normalizedPath = normalizeDirectoryQuotaPathInput(path) ?? path.trim()
  if (!/\s|"/.test(normalizedPath)) {
    return normalizedPath
  }
  return `"${normalizedPath.replaceAll('"', '\\"')}"`
}

function parseLogicalPathLineHead(line: string, lineNumber: number): { path: string; rest: string; error?: string } {
  const trimmed = line.trim()
  if (!trimmed) {
    return { path: '', rest: '' }
  }

  if (!trimmed.startsWith('"')) {
    const [pathToken = '', ...restTokens] = trimmed.split(/\s+/)
    return { path: pathToken, rest: restTokens.join(' ') }
  }

  let pathToken = ''
  let escaping = false
  for (let index = 1; index < trimmed.length; index += 1) {
    const char = trimmed[index]
    if (escaping) {
      pathToken += char === '"' ? '"' : `\\${char}`
      escaping = false
      continue
    }
    if (char === '\\') {
      escaping = true
      continue
    }
    if (char === '"') {
      return {
        path: pathToken,
        rest: trimmed.slice(index + 1).trim(),
      }
    }
    pathToken += char
  }

  if (escaping) {
    pathToken += '\\'
  }
  return { path: pathToken, rest: '', error: `第 ${lineNumber} 行路径引号未闭合` }
}

function parseDirectoryQuotaLines(value: string): { quotas: DirectoryQuota[]; error?: string } {
  const lines = value.split('\n')
  const quotas: DirectoryQuota[] = []
  const seenPaths = new Set<string>()

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index].trim()
    if (!line) {
      continue
    }

    const parsedLine = parseLogicalPathLineHead(line, index + 1)
    if (parsedLine.error) {
      return { quotas: [], error: parsedLine.error }
    }
    if (!parsedLine.rest) {
      return { quotas: [], error: `第 ${index + 1} 行需要填写路径和容量` }
    }

    const quotaPath = normalizeDirectoryQuotaPathInput(parsedLine.path)
    if (!quotaPath) {
      return { quotas: [], error: `第 ${index + 1} 行路径无效` }
    }
    if (seenPaths.has(quotaPath)) {
      return { quotas: [], error: `第 ${index + 1} 行路径重复` }
    }

    const sizeText = parsedLine.rest
    let quotaBytes: number
    try {
      quotaBytes = parseByteSize(sizeText)
    } catch {
      return { quotas: [], error: `第 ${index + 1} 行容量格式无效` }
    }
    if (!Number.isSafeInteger(quotaBytes) || quotaBytes <= 0) {
      return { quotas: [], error: `第 ${index + 1} 行容量必须是大于 0 且不超过安全范围的整数` }
    }

    seenPaths.add(quotaPath)
    quotas.push({ path: quotaPath, quota_bytes: quotaBytes })
  }

  return { quotas }
}

type DirectoryQuotaReviewKind = 'added' | 'changed' | 'removed'

type DirectoryQuotaReviewItem = {
  kind: DirectoryQuotaReviewKind
  path: string
  before?: DirectoryQuota
  after?: DirectoryQuota
}

function buildDirectoryQuotaReview(
  savedQuotas: DirectoryQuota[],
  draftQuotas: DirectoryQuota[],
): DirectoryQuotaReviewItem[] {
  const savedByPath = new Map(savedQuotas.map((quota) => [quota.path, quota]))
  const draftByPath = new Map(draftQuotas.map((quota) => [quota.path, quota]))
  const items: DirectoryQuotaReviewItem[] = []

  for (const draftQuota of draftQuotas) {
    const savedQuota = savedByPath.get(draftQuota.path)
    if (!savedQuota) {
      items.push({ kind: 'added', path: draftQuota.path, after: draftQuota })
      continue
    }
    if (savedQuota.quota_bytes !== draftQuota.quota_bytes) {
      items.push({ kind: 'changed', path: draftQuota.path, before: savedQuota, after: draftQuota })
    }
  }

  for (const savedQuota of savedQuotas) {
    if (!draftByPath.has(savedQuota.path)) {
      items.push({ kind: 'removed', path: savedQuota.path, before: savedQuota })
    }
  }

  return items
}

function directoryQuotaReviewKindLabel(kind: DirectoryQuotaReviewKind): string {
  switch (kind) {
    case 'added':
      return '新增'
    case 'changed':
      return '修改'
    case 'removed':
      return '删除'
    default:
      return kind
  }
}

function directoryQuotaReviewTone(kind: DirectoryQuotaReviewKind): string {
  switch (kind) {
    case 'added':
      return 'border-success/30 bg-success/5 text-success'
    case 'changed':
      return 'border-warning/30 bg-warning/5 text-warning'
    case 'removed':
      return 'border-danger/30 bg-danger/5 text-danger'
    default:
      return 'border-divider bg-content2 text-default-600'
  }
}

function directoryQuotaReviewDescription(item: DirectoryQuotaReviewItem): string {
  if (item.kind === 'changed' && item.before && item.after) {
    return `配额从 ${formatBytes(item.before.quota_bytes)} 调整为 ${formatBytes(item.after.quota_bytes)}`
  }
  const quota = item.after ?? item.before
  return quota ? `容量 ${formatBytes(quota.quota_bytes)}` : ''
}

function DirectoryQuotaChangeReview({
  savedQuotas,
  draftValue,
}: {
  savedQuotas: DirectoryQuota[]
  draftValue: string
}) {
  const parsedDraft = parseDirectoryQuotaLines(draftValue)

  if (parsedDraft.error) {
    return (
      <div className="rounded-lg border border-warning/20 bg-warning/10 px-3 py-2 text-xs text-warning">
        目录配额变更复核暂不可用：{parsedDraft.error}
      </div>
    )
  }

  const reviewItems = buildDirectoryQuotaReview(savedQuotas, parsedDraft.quotas)
  const added = reviewItems.filter((item) => item.kind === 'added').length
  const changed = reviewItems.filter((item) => item.kind === 'changed').length
  const removed = reviewItems.filter((item) => item.kind === 'removed').length

  return (
    <div className="rounded-lg border border-divider bg-content1/60 p-3" aria-label="目录配额变更复核">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="text-sm font-semibold text-foreground">目录配额变更复核</div>
          <div className="mt-1 text-xs text-default-500">基于已保存目录配额和当前编辑内容生成。</div>
        </div>
        <div className="flex flex-wrap gap-2 text-xs">
          <span className="rounded-full bg-success/10 px-2 py-1 text-success">新增 {added}</span>
          <span className="rounded-full bg-warning/10 px-2 py-1 text-warning">修改 {changed}</span>
          <span className="rounded-full bg-danger/10 px-2 py-1 text-danger">删除 {removed}</span>
        </div>
      </div>
      {reviewItems.length === 0 ? (
        <div className="mt-3 rounded-lg border border-divider bg-content2/40 px-3 py-2 text-sm text-default-500">
          目录配额与已保存配置一致。
        </div>
      ) : (
        <div className="mt-3 space-y-2">
          {reviewItems.map((item) => (
            <div key={`${item.kind}:${item.path}`} className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
              <div className="flex flex-wrap items-center gap-2">
                <span className={cn('rounded-full border px-2 py-0.5 text-xs font-medium', directoryQuotaReviewTone(item.kind))}>
                  {directoryQuotaReviewKindLabel(item.kind)}
                </span>
                <span className="font-mono text-sm font-semibold text-foreground">{item.path}</span>
              </div>
              <div className="mt-1 text-xs text-default-500">{directoryQuotaReviewDescription(item)}</div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

const accessRulePrincipalPattern = /^[A-Za-z0-9._-]+$/
const accessRuleKeys = new Set([
  'read_users',
  'write_users',
  'read_groups',
  'write_groups',
  'read_roles',
  'write_roles',
])

function formatAccessRuleList(label: string, values: string[] | undefined): string {
  return values && values.length > 0 ? `${label}=${values.join(',')}` : ''
}

function formatDirectoryAccessRuleLines(rules: DirectoryAccessRule[] | undefined): string {
  return (rules ?? [])
    .map((rule) => [
      formatLogicalPathLineToken(rule.path),
      formatAccessRuleList('read_users', rule.read_users),
      formatAccessRuleList('write_users', rule.write_users),
      formatAccessRuleList('read_groups', rule.read_groups),
      formatAccessRuleList('write_groups', rule.write_groups),
      formatAccessRuleList('read_roles', rule.read_roles),
      formatAccessRuleList('write_roles', rule.write_roles),
    ].filter(Boolean).join(' '))
    .join('\n')
}

function parseAccessRuleValues(value: string, lineNumber: number, field: string): { values: string[]; error?: string } {
  const values = value
    .split(',')
    .map((entry) => entry.trim().toLowerCase())
    .filter(Boolean)
  const seen = new Set<string>()
  const normalized: string[] = []

  if (values.length === 0) {
    return { values: [], error: `第 ${lineNumber} 行 ${field} 不能为空` }
  }

  for (const item of values) {
    if (field.endsWith('_roles')) {
      if (item !== 'admin' && item !== 'user' && item !== 'guest') {
        return { values: [], error: `第 ${lineNumber} 行角色只能是 admin、user 或 guest` }
      }
    } else if (!accessRulePrincipalPattern.test(item)) {
      return { values: [], error: `第 ${lineNumber} 行主体只能包含字母、数字、点、短横线和下划线` }
    }
    if (!seen.has(item)) {
      seen.add(item)
      normalized.push(item)
    }
  }

  normalized.sort()
  return { values: normalized }
}

function parseDirectoryAccessRuleLines(value: string): { rules: DirectoryAccessRule[]; error?: string } {
  const lines = value.split('\n')
  const rules: DirectoryAccessRule[] = []
  const seenPaths = new Set<string>()

  for (let index = 0; index < lines.length; index += 1) {
    const lineNumber = index + 1
    const line = lines[index].trim()
    if (!line) {
      continue
    }

    const parsedLine = parseLogicalPathLineHead(line, lineNumber)
    if (parsedLine.error) {
      return { rules: [], error: parsedLine.error }
    }
    const rulePath = normalizeDirectoryQuotaPathInput(parsedLine.path)
    if (!rulePath) {
      return { rules: [], error: `第 ${lineNumber} 行路径无效` }
    }
    if (seenPaths.has(rulePath)) {
      return { rules: [], error: `第 ${lineNumber} 行路径重复` }
    }

    const rule: DirectoryAccessRule = { path: rulePath }
    const tokens = parsedLine.rest ? parsedLine.rest.split(/\s+/) : []
    for (const token of tokens) {
      const separator = token.indexOf('=')
      if (separator <= 0 || separator === token.length - 1) {
        return { rules: [], error: `第 ${lineNumber} 行规则格式无效` }
      }
      const key = token.slice(0, separator)
      const rawValue = token.slice(separator + 1)
      if (!accessRuleKeys.has(key)) {
        return { rules: [], error: `第 ${lineNumber} 行字段 ${key} 不支持` }
      }
      const parsed = parseAccessRuleValues(rawValue, lineNumber, key)
      if (parsed.error) {
        return { rules: [], error: parsed.error }
      }
      switch (key) {
        case 'read_users':
          rule.read_users = parsed.values
          break
        case 'write_users':
          rule.write_users = parsed.values
          break
        case 'read_groups':
          rule.read_groups = parsed.values
          break
        case 'write_groups':
          rule.write_groups = parsed.values
          break
        case 'read_roles':
          rule.read_roles = parsed.values as DirectoryAccessRole[]
          break
        case 'write_roles':
          rule.write_roles = parsed.values as DirectoryAccessRole[]
          break
      }
    }

    const hasPrincipals = Boolean(
      rule.read_users?.length ||
      rule.write_users?.length ||
      rule.read_groups?.length ||
      rule.write_groups?.length ||
      rule.read_roles?.length ||
      rule.write_roles?.length,
    )
    if (!hasPrincipals) {
      return { rules: [], error: `第 ${lineNumber} 行至少需要一个 read 或 write 主体` }
    }

    seenPaths.add(rulePath)
    rules.push(rule)
  }

  return { rules }
}

function formatDiskHealthDeviceLines(devices: DiskHealthDeviceSettings[] | undefined): string {
  return (devices ?? [])
    .map((device) => [
      device.path,
      device.name ?? '',
      device.type ?? '',
      device.serial ?? '',
      device.temperature_warning_c ? String(device.temperature_warning_c) : '',
      device.temperature_critical_c ? String(device.temperature_critical_c) : '',
    ].join(' | '))
    .join('\n')
}

function parseOptionalNonNegativeIntegerCell(value: string, lineNumber: number, field: string): { value?: number; error?: string } {
  const trimmed = value.trim()
  if (!trimmed) {
    return {}
  }
  const parsed = Number(trimmed)
  if (!/^\d+$/.test(trimmed) || !Number.isSafeInteger(parsed)) {
    return { error: `第 ${lineNumber} 行 ${field} 必须是 0 或不超过安全范围的整数` }
  }
  return { value: parsed }
}

function parseDiskHealthDeviceLines(value: string): { devices: DiskHealthDeviceSettings[]; error?: string } {
  const lines = value.split('\n')
  const devices: DiskHealthDeviceSettings[] = []

  for (let index = 0; index < lines.length; index += 1) {
    const lineNumber = index + 1
    const line = lines[index].trim()
    if (!line) {
      continue
    }

    const parts = line.split('|').map((part) => part.trim())
    if (parts.length > 6) {
      return { devices: [], error: `第 ${lineNumber} 行最多包含 6 列` }
    }

    const [path, name = '', type = '', serial = '', warningText = '', criticalText = ''] = parts
    if (!path || !path.startsWith('/') || hasControlChar(path)) {
      return { devices: [], error: `第 ${lineNumber} 行设备路径必须是绝对路径` }
    }
    if ([name, type, serial].some(hasControlChar)) {
      return { devices: [], error: `第 ${lineNumber} 行设备名称、类型和序列号不能包含控制字符` }
    }

    const warning = parseOptionalNonNegativeIntegerCell(warningText, lineNumber, '温度提醒阈值')
    if (warning.error) {
      return { devices: [], error: warning.error }
    }
    const critical = parseOptionalNonNegativeIntegerCell(criticalText, lineNumber, '温度严重阈值')
    if (critical.error) {
      return { devices: [], error: critical.error }
    }
    if (warning.value && critical.value && critical.value < warning.value) {
      return { devices: [], error: `第 ${lineNumber} 行温度严重阈值不能小于提醒阈值` }
    }

    devices.push({
      path,
      ...(name && { name }),
      ...(type && { type }),
      ...(serial && { serial }),
      ...(warning.value !== undefined && { temperature_warning_c: warning.value }),
      ...(critical.value !== undefined && { temperature_critical_c: critical.value }),
    })
  }

  return { devices }
}

function isValidDiskHealthCommand(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed || hasControlChar(trimmed) || /\s/.test(trimmed) || trimmed === '.' || trimmed === '..') {
    return false
  }
  return trimmed.startsWith('/') || !trimmed.includes('/')
}

function normalizeSharePolicyRulesForSave(inputRules: SharePolicyRuleDraft[]): { rules: SharePolicyRule[]; error?: string } {
  const rules: SharePolicyRule[] = []
  const seenPaths = new Set<string>()

  for (let index = 0; index < inputRules.length; index += 1) {
    const lineNumber = index + 1
    const inputRule = inputRules[index]
    const rulePath = normalizeDirectoryQuotaPathInput(inputRule.path)
    if (!rulePath) {
      return { rules: [], error: `第 ${lineNumber} 行路径无效` }
    }
    if (seenPaths.has(rulePath)) {
      return { rules: [], error: `第 ${lineNumber} 行路径重复` }
    }

    const maxExpiresIn = inputRule.max_expires_in?.trim() ?? ''
    const hasMaxExpiresInConstraint = maxExpiresIn !== '' && !isZeroDurationString(maxExpiresIn)
    if (hasMaxExpiresInConstraint && !isValidDurationString(maxExpiresIn)) {
      return { rules: [], error: `第 ${lineNumber} 行有效期上限格式无效` }
    }

    const rawMaxAccess = inputRule.max_access_input
      ?? (inputRule.max_access !== undefined ? String(inputRule.max_access) : '')
    const parsedMaxAccess = parseNonNegativeSafeIntegerInput(rawMaxAccess)
    if (!parsedMaxAccess.valid) {
      return { rules: [], error: `第 ${lineNumber} 行访问次数上限必须是 0 或不超过安全范围的正整数` }
    }
    const maxAccess = parsedMaxAccess.value

    const allowedUsers = parseOptionalSharePolicyPrincipalList(
      inputRule.allowed_users_input ?? (inputRule.allowed_users ?? []).join(', '),
      lineNumber,
      'allowed_users',
    )
    if (allowedUsers.error) {
      return { rules: [], error: allowedUsers.error }
    }
    const allowedGroups = parseOptionalSharePolicyPrincipalList(
      inputRule.allowed_groups_input ?? (inputRule.allowed_groups ?? []).join(', '),
      lineNumber,
      'allowed_groups',
    )
    if (allowedGroups.error) {
      return { rules: [], error: allowedGroups.error }
    }
    const allowedRoles = parseOptionalSharePolicyPrincipalList(
      inputRule.allowed_roles_input ?? (inputRule.allowed_roles ?? []).join(', '),
      lineNumber,
      'allowed_roles',
    )
    if (allowedRoles.error) {
      return { rules: [], error: allowedRoles.error }
    }

    if (!inputRule.require_password && !hasMaxExpiresInConstraint && maxAccess === 0 &&
      allowedUsers.values.length === 0 && allowedGroups.values.length === 0 && allowedRoles.values.length === 0) {
      return { rules: [], error: `第 ${lineNumber} 行至少需要一个约束` }
    }

    seenPaths.add(rulePath)
    rules.push({
      path: rulePath,
      require_password: inputRule.require_password || undefined,
      max_expires_in: hasMaxExpiresInConstraint ? maxExpiresIn : undefined,
      max_access: maxAccess > 0 ? maxAccess : undefined,
      allowed_users: allowedUsers.values.length > 0 ? allowedUsers.values : undefined,
      allowed_groups: allowedGroups.values.length > 0 ? allowedGroups.values : undefined,
      allowed_roles: allowedRoles.values.length > 0 ? allowedRoles.values as DirectoryAccessRole[] : undefined,
    })
  }

  return { rules }
}

function parseOptionalSharePolicyPrincipalList(
  value: string,
  lineNumber: number,
  field: 'allowed_users' | 'allowed_groups' | 'allowed_roles',
): { values: string[]; error?: string } {
  const trimmed = value.trim()
  if (!trimmed) {
    return { values: [] }
  }
  return parseAccessRuleValues(trimmed, lineNumber, field)
}

type SharePolicyReviewInput = {
  enabled: boolean
  baseURL: string
  defaultExpiresIn: string
  defaultMaxAccess: string
  rules: SharePolicyRuleDraft[]
}

type SharePolicyDefaultReviewItem = {
  label: string
  before: string
  after: string
}

type SharePolicyRuleReviewKind = 'added' | 'changed' | 'removed'

type SharePolicyRuleReviewItem = {
  kind: SharePolicyRuleReviewKind
  path: string
  before?: SharePolicyRule
  after?: SharePolicyRule
  changedFields: string[]
}

type SharePolicyCleanupInsight = {
  key: string
  message: string
  tone: 'warning' | 'default'
}

const sharePolicyRuleReviewFields: Array<{
  key: keyof Omit<SharePolicyRule, 'path'>
  label: string
}> = [
  { key: 'require_password', label: '必须设置密码' },
  { key: 'max_expires_in', label: '最长有效期' },
  { key: 'max_access', label: '最多访问次数' },
  { key: 'allowed_users', label: '允许用户' },
  { key: 'allowed_groups', label: '允许组' },
  { key: 'allowed_roles', label: '允许角色' },
]

function normalizeSharePolicyDurationForReview(value: string | undefined): string {
  const trimmed = value?.trim() ?? ''
  return !trimmed || isZeroDurationString(trimmed) ? '' : trimmed
}

function normalizeSharePolicyMaxAccessForReview(value: string | undefined): string {
  const trimmed = value?.trim() ?? ''
  const parsed = parseNonNegativeSafeIntegerInput(trimmed)
  if (!parsed.valid) {
    return trimmed
  }
  return parsed.value > 0 ? String(parsed.value) : ''
}

function sharePolicyOptionalValueLabel(value: string): string {
  return value ? value : '未设置'
}

function sharePolicyLimitValueLabel(value: string): string {
  return value ? value : '不限制'
}

function buildSharePolicyDefaultReview(
  saved: SharePolicyReviewInput,
  draft: SharePolicyReviewInput,
): SharePolicyDefaultReviewItem[] {
  const savedBaseURL = saved.baseURL.trim()
  const draftBaseURL = draft.baseURL.trim()
  const savedDefaultExpiresIn = normalizeSharePolicyDurationForReview(saved.defaultExpiresIn)
  const draftDefaultExpiresIn = normalizeSharePolicyDurationForReview(draft.defaultExpiresIn)
  const savedDefaultMaxAccess = normalizeSharePolicyMaxAccessForReview(saved.defaultMaxAccess)
  const draftDefaultMaxAccess = normalizeSharePolicyMaxAccessForReview(draft.defaultMaxAccess)
  const items: SharePolicyDefaultReviewItem[] = []

  if (saved.enabled !== draft.enabled) {
    items.push({
      label: '分享功能',
      before: saved.enabled ? '启用' : '停用',
      after: draft.enabled ? '启用' : '停用',
    })
  }
  if (savedBaseURL !== draftBaseURL) {
    items.push({
      label: '分享基础 URL',
      before: sharePolicyOptionalValueLabel(savedBaseURL),
      after: sharePolicyOptionalValueLabel(draftBaseURL),
    })
  }
  if (savedDefaultExpiresIn !== draftDefaultExpiresIn) {
    items.push({
      label: '新分享默认有效期',
      before: sharePolicyLimitValueLabel(savedDefaultExpiresIn),
      after: sharePolicyLimitValueLabel(draftDefaultExpiresIn),
    })
  }
  if (savedDefaultMaxAccess !== draftDefaultMaxAccess) {
    items.push({
      label: '新分享默认访问次数',
      before: sharePolicyLimitValueLabel(savedDefaultMaxAccess),
      after: sharePolicyLimitValueLabel(draftDefaultMaxAccess),
    })
  }

  return items
}

function getSharePolicyRuleChangedFields(before: SharePolicyRule, after: SharePolicyRule): string[] {
  return sharePolicyRuleReviewFields
    .filter(({ key }) => !sharePolicyRuleFieldEquals(before[key], after[key]))
    .map(({ label }) => label)
}

function sharePolicyRuleFieldEquals(
  before: SharePolicyRule[keyof Omit<SharePolicyRule, 'path'>],
  after: SharePolicyRule[keyof Omit<SharePolicyRule, 'path'>],
): boolean {
  if (Array.isArray(before) || Array.isArray(after)) {
    const beforeItems = Array.isArray(before) ? before : []
    const afterItems = Array.isArray(after) ? after : []
    return beforeItems.length === afterItems.length && beforeItems.every((item, index) => item === afterItems[index])
  }
  return before === after
}

function buildSharePolicyRuleReview(
  savedRules: SharePolicyRule[],
  draftRules: SharePolicyRule[],
): SharePolicyRuleReviewItem[] {
  const savedByPath = new Map(savedRules.map((rule) => [rule.path, rule]))
  const draftByPath = new Map(draftRules.map((rule) => [rule.path, rule]))
  const items: SharePolicyRuleReviewItem[] = []

  for (const draftRule of draftRules) {
    const savedRule = savedByPath.get(draftRule.path)
    if (!savedRule) {
      items.push({
        kind: 'added',
        path: draftRule.path,
        after: draftRule,
        changedFields: [],
      })
      continue
    }
    const changedFields = getSharePolicyRuleChangedFields(savedRule, draftRule)
    if (changedFields.length > 0) {
      items.push({
        kind: 'changed',
        path: draftRule.path,
        before: savedRule,
        after: draftRule,
        changedFields,
      })
    }
  }

  for (const savedRule of savedRules) {
    if (!draftByPath.has(savedRule.path)) {
      items.push({
        kind: 'removed',
        path: savedRule.path,
        before: savedRule,
        changedFields: [],
      })
    }
  }

  return items
}

function sharePolicyRuleReviewKindLabel(kind: SharePolicyRuleReviewKind): string {
  switch (kind) {
    case 'added':
      return '新增'
    case 'changed':
      return '修改'
    case 'removed':
      return '删除'
    default:
      return kind
  }
}

function sharePolicyRuleReviewTone(kind: SharePolicyRuleReviewKind): string {
  switch (kind) {
    case 'added':
      return 'border-success/30 bg-success/5 text-success'
    case 'changed':
      return 'border-warning/30 bg-warning/5 text-warning'
    case 'removed':
      return 'border-danger/30 bg-danger/5 text-danger'
    default:
      return 'border-divider bg-content2 text-default-600'
  }
}

function sharePolicyRuleSummary(rule: SharePolicyRule): string {
  const parts = [
    rule.require_password ? '必须设置密码' : '',
    rule.max_expires_in ? `最长有效期：${rule.max_expires_in}` : '',
    rule.max_access && rule.max_access > 0 ? `最多访问：${rule.max_access}` : '',
    rule.allowed_users?.length ? `允许用户：${rule.allowed_users.join(', ')}` : '',
    rule.allowed_groups?.length ? `允许组：${rule.allowed_groups.join(', ')}` : '',
    rule.allowed_roles?.length ? `允许角色：${rule.allowed_roles.join(', ')}` : '',
  ].filter(Boolean)

  return parts.length > 0 ? parts.join(' · ') : '未配置约束'
}

function sharePolicyRuleHasPasswordConstraint(rule: SharePolicyRule): boolean {
  return Boolean(rule.require_password)
}

function sharePolicyRuleHasExpiresConstraint(rule: SharePolicyRule): boolean {
  return Boolean(rule.max_expires_in)
}

function sharePolicyRuleHasAccessConstraint(rule: SharePolicyRule): boolean {
  return Boolean(rule.max_access && rule.max_access > 0)
}

function sharePolicyRuleHasPrincipalConstraint(rule: SharePolicyRule): boolean {
  return Boolean(
    rule.allowed_users?.length ||
    rule.allowed_groups?.length ||
    rule.allowed_roles?.length,
  )
}

const sharePolicyCleanupConstraintChecks: Array<{
  key: string
  label: string
  hasConstraint: (rule: SharePolicyRule) => boolean
}> = [
  { key: 'password', label: '强制密码约束', hasConstraint: sharePolicyRuleHasPasswordConstraint },
  { key: 'expires', label: '最长有效期约束', hasConstraint: sharePolicyRuleHasExpiresConstraint },
  { key: 'access', label: '访问次数约束', hasConstraint: sharePolicyRuleHasAccessConstraint },
  { key: 'principal', label: '允许创建者范围', hasConstraint: sharePolicyRuleHasPrincipalConstraint },
]

function sharePolicyRuleConstraintSignature(rule: SharePolicyRule): string {
  return JSON.stringify({
    require_password: Boolean(rule.require_password),
    max_expires_in: rule.max_expires_in ?? '',
    max_access: rule.max_access && rule.max_access > 0 ? rule.max_access : 0,
    allowed_users: [...(rule.allowed_users ?? [])].sort(),
    allowed_groups: [...(rule.allowed_groups ?? [])].sort(),
    allowed_roles: [...(rule.allowed_roles ?? [])].sort(),
  })
}

function sharePolicyRuleCoversPath(parentPath: string, childPath: string): boolean {
  if (parentPath === childPath) {
    return false
  }
  if (parentPath === '/') {
    return childPath.startsWith('/')
  }
  return childPath.startsWith(`${parentPath}/`)
}

function buildSharePolicyCleanupInsights(rules: SharePolicyRule[]): SharePolicyCleanupInsight[] {
  const insights: SharePolicyCleanupInsight[] = []
  const rootRule = rules.find((rule) => rule.path === '/')
  if (rootRule) {
    insights.push({
      key: 'root-wide',
      message: '根路径分享策略会覆盖所有路径；建议确认是否需要为敏感目录设置更具体规则。',
      tone: 'default',
    })
  }

  const ancestorCandidatesByPath = new Map<string, SharePolicyRule[]>()
  for (const rule of rules) {
    const ancestors = rules
      .filter((candidate) => sharePolicyRuleCoversPath(candidate.path, rule.path))
      .sort((left, right) => right.path.length - left.path.length)
    ancestorCandidatesByPath.set(rule.path, ancestors)
  }

  for (const rule of rules) {
    const ancestors = ancestorCandidatesByPath.get(rule.path) ?? []
    if (ancestors.length === 0) {
      continue
    }

    for (const check of sharePolicyCleanupConstraintChecks) {
      if (check.hasConstraint(rule)) {
        continue
      }
      const constrainedAncestor = ancestors.find((ancestor) => check.hasConstraint(ancestor))
      if (constrainedAncestor) {
        insights.push({
          key: `inherit:${rule.path}:${check.key}:${constrainedAncestor.path}`,
          message: `${rule.path} 未继承上级 ${constrainedAncestor.path} 的${check.label}。`,
          tone: 'warning',
        })
      }
    }
  }

  const pathsBySignature = new Map<string, string[]>()
  for (const rule of rules) {
    const signature = sharePolicyRuleConstraintSignature(rule)
    pathsBySignature.set(signature, [...(pathsBySignature.get(signature) ?? []), rule.path])
  }
  for (const paths of pathsBySignature.values()) {
    if (paths.length < 2) {
      continue
    }
    const sortedPaths = [...paths].sort()
    const pathLabel = sortedPaths.length === 2
      ? `${sortedPaths[0]} 与 ${sortedPaths[1]}`
      : `${sortedPaths.slice(0, 3).join('、')} 等 ${sortedPaths.length} 条路径`
    insights.push({
      key: `duplicate:${sortedPaths.join('|')}`,
      message: `${pathLabel} 的约束完全相同，可复核是否改为共同上级策略或保留路径差异。`,
      tone: 'default',
    })
  }

  return insights
}

function SharePolicyChangeReview({
  saved,
  draft,
}: {
  saved: SharePolicyReviewInput
  draft: SharePolicyReviewInput
}) {
  const normalizedSavedRules = normalizeSharePolicyRulesForSave(saved.rules)
  const normalizedDraftRules = normalizeSharePolicyRulesForSave(draft.rules)
  const normalizeError = normalizedSavedRules.error ?? normalizedDraftRules.error

  if (normalizeError) {
    return (
      <div
        className="rounded-lg border border-warning/20 bg-warning/10 px-3 py-2 text-xs text-warning"
        aria-label="分享策略变更复核"
      >
        分享策略变更复核暂不可用：{normalizeError}
      </div>
    )
  }

  const defaultItems = buildSharePolicyDefaultReview(saved, draft)
  const ruleItems = buildSharePolicyRuleReview(normalizedSavedRules.rules, normalizedDraftRules.rules)
  const added = ruleItems.filter((item) => item.kind === 'added').length
  const changed = ruleItems.filter((item) => item.kind === 'changed').length
  const removed = ruleItems.filter((item) => item.kind === 'removed').length

  return (
    <div className="rounded-lg border border-divider bg-content1/60 p-3" aria-label="分享策略变更复核">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="text-sm font-semibold text-foreground">分享策略变更复核</div>
          <div className="mt-1 text-xs text-default-500">基于已保存分享配置和当前编辑内容生成。</div>
        </div>
        <div className="flex flex-wrap gap-2 text-xs">
          <span className="rounded-full bg-content2 px-2 py-1 text-default-600">默认项 {defaultItems.length}</span>
          <span className="rounded-full bg-success/10 px-2 py-1 text-success">新增 {added}</span>
          <span className="rounded-full bg-warning/10 px-2 py-1 text-warning">修改 {changed}</span>
          <span className="rounded-full bg-danger/10 px-2 py-1 text-danger">删除 {removed}</span>
        </div>
      </div>
      {defaultItems.length === 0 && ruleItems.length === 0 ? (
        <div className="mt-3 rounded-lg border border-divider bg-content2/40 px-3 py-2 text-sm text-default-500">
          分享策略与已保存配置一致。
        </div>
      ) : (
        <div className="mt-3 space-y-2">
          {defaultItems.map((item) => (
            <div key={item.label} className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
              <div className="text-sm font-semibold text-foreground">{item.label}</div>
              <div className="mt-1 break-anywhere text-xs text-default-500">
                {item.before} -&gt; {item.after}
              </div>
            </div>
          ))}
          {ruleItems.map((item) => {
            const activeRule = item.after ?? item.before
            return (
              <div key={`${item.kind}:${item.path}`} className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
                <div className="flex flex-wrap items-center gap-2">
                  <span className={cn('rounded-full border px-2 py-0.5 text-xs font-medium', sharePolicyRuleReviewTone(item.kind))}>
                    {sharePolicyRuleReviewKindLabel(item.kind)}
                  </span>
                  <span className="font-mono text-sm font-semibold text-foreground">{item.path}</span>
                  {item.changedFields.length > 0 && (
                    <span className="text-xs text-default-500">变更字段：{item.changedFields.join('、')}</span>
                  )}
                </div>
                {activeRule && (
                  <div className="mt-1 break-anywhere text-xs text-default-500">
                    {sharePolicyRuleSummary(activeRule)}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

function SharePolicyCoverageSummary({ draft }: { draft: SharePolicyReviewInput }) {
  const normalizedRules = normalizeSharePolicyRulesForSave(draft.rules)
  if (normalizedRules.error) {
    return (
      <div
        className="rounded-lg border border-warning/20 bg-warning/10 px-3 py-2 text-xs text-warning"
        aria-label="分享策略覆盖摘要"
      >
        分享策略覆盖摘要暂不可用：{normalizedRules.error}
      </div>
    )
  }

  const rules = normalizedRules.rules
  const defaultExpiresIn = normalizeSharePolicyDurationForReview(draft.defaultExpiresIn)
  const defaultMaxAccess = normalizeSharePolicyMaxAccessForReview(draft.defaultMaxAccess)
  const passwordRuleCount = rules.filter((rule) => rule.require_password).length
  const expiresRuleCount = rules.filter((rule) => Boolean(rule.max_expires_in)).length
  const accessRuleCount = rules.filter((rule) => Boolean(rule.max_access && rule.max_access > 0)).length
  const principalRuleCount = rules.filter((rule) => (
    Boolean(rule.allowed_users?.length) ||
    Boolean(rule.allowed_groups?.length) ||
    Boolean(rule.allowed_roles?.length)
  )).length
  const cleanupInsights = buildSharePolicyCleanupInsights(rules)
  const attentionItems = draft.enabled
    ? [
      draft.baseURL.trim() === '' ? '分享基础 URL 未固定，跨域名或反向代理切换后需复核已生成链接。' : '',
      defaultExpiresIn === '' ? '新分享默认不过期，建议为公网分享设置默认有效期。' : '',
      defaultMaxAccess === '' ? '新分享默认访问次数不限制，建议为公网分享设置默认访问次数。' : '',
      rules.length === 0 ? '尚未配置路径分享策略，所有路径只受全局默认值约束。' : '',
      rules.length > passwordRuleCount ? `${rules.length - passwordRuleCount} 条路径策略未强制密码。` : '',
      rules.length > expiresRuleCount ? `${rules.length - expiresRuleCount} 条路径策略未限制最长有效期。` : '',
      rules.length > accessRuleCount ? `${rules.length - accessRuleCount} 条路径策略未限制访问次数。` : '',
      rules.length > principalRuleCount ? `${rules.length - principalRuleCount} 条路径策略未限制允许创建者范围。` : '',
    ].filter(Boolean)
    : ['分享功能当前停用；重新启用前应复核默认有效期、访问次数和路径策略。']

  const summaryItems = [
    { label: '功能状态', value: draft.enabled ? '已启用' : '已停用', tone: draft.enabled ? 'warning' : 'success' },
    { label: '默认有效期', value: sharePolicyLimitValueLabel(defaultExpiresIn), tone: draft.enabled && defaultExpiresIn === '' ? 'warning' : 'success' },
    { label: '默认访问次数', value: sharePolicyLimitValueLabel(defaultMaxAccess), tone: draft.enabled && defaultMaxAccess === '' ? 'warning' : 'success' },
    { label: '路径策略', value: `${rules.length} 条`, tone: draft.enabled && rules.length === 0 ? 'warning' : 'success' },
    { label: '强制密码路径', value: `${passwordRuleCount} 条`, tone: draft.enabled && rules.length > passwordRuleCount ? 'warning' : 'success' },
    { label: '成员范围路径', value: `${principalRuleCount} 条`, tone: draft.enabled && rules.length > principalRuleCount ? 'warning' : 'success' },
    { label: '完整限制路径', value: `${rules.filter((rule) => (
      rule.require_password &&
      rule.max_expires_in &&
      rule.max_access &&
      rule.max_access > 0 &&
      (rule.allowed_users?.length || rule.allowed_groups?.length || rule.allowed_roles?.length)
    )).length} 条`, tone: 'default' },
  ]

  return (
    <div aria-label="分享策略覆盖摘要" className="rounded-lg border border-divider bg-content1/60 p-3">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="text-sm font-semibold text-foreground">分享策略覆盖摘要</div>
          <div className="mt-1 text-xs text-default-500">基于当前编辑内容估算默认分享限制和路径规则覆盖。</div>
        </div>
        <div className="flex flex-wrap gap-2">
          <span className={cn(
            'w-fit rounded-full px-2 py-1 text-xs font-medium',
            attentionItems.length > 0 ? 'bg-warning/10 text-warning' : 'bg-success/10 text-success',
          )}>
            关注项 {attentionItems.length}
          </span>
          <span className={cn(
            'w-fit rounded-full px-2 py-1 text-xs font-medium',
            cleanupInsights.length > 0 ? 'bg-warning/10 text-warning' : 'bg-success/10 text-success',
          )}>
            整理项 {cleanupInsights.length}
          </span>
        </div>
      </div>
      <div className="mt-3 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
        {summaryItems.map((item) => (
          <div
            key={item.label}
            className={cn(
              'min-w-0 rounded-lg border p-3',
              item.tone === 'success'
                ? 'border-success/20 bg-success/5'
                : item.tone === 'warning'
                  ? 'border-warning/20 bg-warning/10'
                  : 'border-divider bg-content2/60',
            )}
          >
            <div className="text-xs font-medium text-default-500">{item.label}</div>
            <div className="mt-1 break-words text-sm text-default-700">{item.value}</div>
          </div>
        ))}
      </div>
      <div className="mt-3 rounded-lg border border-divider bg-content2/40 px-3 py-2">
        <div className="text-xs font-medium text-default-500">策略关注项</div>
        <ul className="mt-2 list-disc space-y-1 pl-4 text-xs leading-5 text-default-600">
          {attentionItems.length === 0 ? (
            <li>未发现默认策略或路径策略的明显宽松项。</li>
          ) : attentionItems.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
      </div>
      <div className="mt-3 rounded-lg border border-divider bg-content2/40 px-3 py-2">
        <div className="text-xs font-medium text-default-500">策略整理建议</div>
        <ul className="mt-2 list-disc space-y-1 pl-4 text-xs leading-5 text-default-600">
          {cleanupInsights.length === 0 ? (
            <li>当前路径策略没有明显覆盖整理项。</li>
          ) : cleanupInsights.map((item) => (
            <li
              key={item.key}
              className={cn(item.tone === 'warning' ? 'text-warning' : 'text-default-600')}
            >
              {item.message}
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}

function directoryAccessSourceLabel(source: DirectoryAccessDecision['source']): string {
  switch (source) {
    case 'admin':
      return '管理员'
    case 'auth_disabled':
      return '未启用认证'
    case 'directory_access_rule':
      return '目录规则'
    case 'home_dir':
      return '主目录'
    case 'invalid_home_dir':
      return '主目录无效'
    case 'user_disabled':
      return '账号已停用'
    case 'user_not_found':
      return '用户不存在'
    default:
      return source
  }
}

function directoryAccessModeLabel(mode: DirectoryAccessDecision['mode']): string {
  return mode === 'write' ? '写入' : '读取'
}

function getDirectoryAccessDecisionDisplayMessage(decision: DirectoryAccessDecision): string {
  const modeLabel = directoryAccessModeLabel(decision.mode)
  const normalizedMessage = decision.message?.trim().toLowerCase()

  if (normalizedMessage === 'directory access rule grants read through an existing descendant') {
    return '已存在的子目录命中读取规则，因此允许查看相关路径。'
  }

  switch (decision.source) {
    case 'admin':
      return '管理员角色拥有完整访问权限。'
    case 'auth_disabled':
      return '当前未启用认证，此路径对该请求开放。'
    case 'user_not_found':
      return '用户不存在，无法访问该路径。'
    case 'user_disabled':
      return '账号已停用，无法访问该路径。'
    case 'invalid_home_dir':
      return '该用户主目录配置无效，无法判断访问范围。'
    case 'home_dir':
      return decision.allowed
        ? '路径位于该用户主目录内。'
        : '路径位于该用户主目录外。'
    case 'directory_access_rule':
      return decision.allowed
        ? `目录规则允许${modeLabel}该路径。`
        : `目录规则未授予${modeLabel}权限。`
    default:
      return decision.allowed
        ? `已允许${modeLabel}该路径。`
        : `未允许${modeLabel}该路径。`
  }
}

function DirectoryAccessDecisionLine({ label, decision }: { label: string; decision: DirectoryAccessDecision }) {
  const allowedClassName = decision.allowed
    ? 'border-success/30 bg-success/5 text-success'
    : 'border-danger/30 bg-danger/5 text-danger'
  const Icon = decision.allowed ? CheckCircle2 : AlertCircle
  const displayMessage = getDirectoryAccessDecisionDisplayMessage(decision)

  return (
    <div className={cn('rounded-lg border px-3 py-2', allowedClassName)}>
      <div className="flex min-w-0 items-center justify-between gap-3">
        <span className="flex min-w-0 items-center gap-2 text-sm font-semibold">
          <Icon size={16} className="shrink-0" />
          {label}
        </span>
        <span className="shrink-0 rounded-full bg-background/70 px-2 py-0.5 text-xs font-medium text-foreground">
          {decision.allowed ? '允许' : '拒绝'}
        </span>
      </div>
      <div className="mt-1 text-xs text-foreground/70">
        {directoryAccessSourceLabel(decision.source)}
        {decision.matched_rule?.path ? ` · ${decision.matched_rule.path}` : ''}
      </div>
      <div className="mt-1 break-anywhere text-xs text-foreground/60">{displayMessage}</div>
    </div>
  )
}

function DirectoryAccessCheckResult({ result }: { result: DirectoryAccessCheckData }) {
  return (
    <div className="rounded-lg border border-divider bg-content2/40 p-3">
      <div className="mb-3 flex flex-wrap items-center gap-2 text-xs text-default-500">
        <span className="rounded-full bg-content1 px-2 py-1 font-mono text-foreground">{result.username}</span>
        <span className="rounded-full bg-content1 px-2 py-1">{result.role}</span>
        <span className="rounded-full bg-content1 px-2 py-1 font-mono text-foreground">{result.path}</span>
      </div>
      <div className="grid gap-2 sm:grid-cols-2">
        <DirectoryAccessDecisionLine label="读取" decision={result.read} />
        <DirectoryAccessDecisionLine label="写入" decision={result.write} />
      </div>
    </div>
  )
}

function directoryAccessShareRelationLabel(relation: string): string {
  switch (relation) {
    case 'exact':
      return '直接分享'
    case 'covers_path':
      return '父级覆盖'
    case 'inside_path':
      return '子级分享'
    default:
      return relation
  }
}

function formatDirectoryAccessDecisionForReport(decision: DirectoryAccessDecision): string {
  const status = decision.allowed ? '允许' : '拒绝'
  const source = directoryAccessSourceLabel(decision.source)
  const rulePath = decision.matched_rule?.path ? ` · 规则 ${decision.matched_rule.path}` : ''
  return `${status} · ${source}${rulePath}`
}

function formatDirectoryAccessShareForReport(entry: NonNullable<DirectoryAccessReportData['shares']>[number]): string {
  const typeLabel = entry.type === 'folder' ? '文件夹' : '文件'
  const status = entry.active ? '可访问' : '不可访问'
  const password = entry.has_password ? '密码保护' : '无密码'
  const maxAccess = entry.max_access > 0 ? String(entry.max_access) : '不限'
  return `- ${entry.path} (${typeLabel} · ${directoryAccessShareRelationLabel(entry.relation)}): ${status} · ${password} · 访问 ${entry.access_count}/${maxAccess} · 创建者 ${entry.created_by}`
}

function formatDirectoryAccessRuleEffectForReport(entry: NonNullable<DirectoryAccessReportData['rule_effects']>[number]): string {
  const samples = entry.user_samples?.length ? ` · 用户 ${entry.user_samples.join(', ')}` : ''
  return `- 规则 ${entry.index + 1} ${entry.path}: 读允许 ${entry.read_allowed} / 读拒绝 ${entry.read_denied}; 写允许 ${entry.write_allowed} / 写拒绝 ${entry.write_denied}${samples}`
}

function formatDirectoryAccessReportForClipboard(report: DirectoryAccessReportData, title: string): string {
  const lines = [
    '目录权限复核记录',
    `类型: ${report.preview ? '未保存变更预览' : title}`,
    `路径: ${report.path}`,
    `用户: ${report.summary.users}`,
    `读取: 允许 ${report.summary.read_allowed} / 拒绝 ${report.summary.read_denied}`,
    `写入: 允许 ${report.summary.write_allowed} / 拒绝 ${report.summary.write_denied}`,
    `相关分享: ${report.summary.related_shares} (活跃 ${report.summary.active_related_shares}, 密码 ${report.summary.password_protected_shares})`,
    '',
    '用户明细:',
    ...report.users.map((entry) => {
      const groups = entry.groups?.length ? ` · 组 ${entry.groups.join(', ')}` : ''
      return `- ${entry.username} (${entry.role}${groups}, home ${entry.home_dir}): 读 ${formatDirectoryAccessDecisionForReport(entry.read)}; 写 ${formatDirectoryAccessDecisionForReport(entry.write)}`
    }),
    '',
    '规则生效明细:',
  ]

  const ruleEffects = report.rule_effects ?? []
  if (ruleEffects.length === 0) {
    lines.push('- 未命中目录规则')
  } else {
    lines.push(...ruleEffects.map(formatDirectoryAccessRuleEffectForReport))
  }

  lines.push(
    '',
    '分享影响:',
  )

  const shares = report.shares ?? []
  if (shares.length === 0) {
    lines.push('- 无相关分享')
  } else {
    lines.push(...shares.map(formatDirectoryAccessShareForReport))
  }

  return lines.join('\n')
}

type DirectoryAccessReviewHistoryEntry = {
  id: string
  recordedAt: string
  reviewer?: string
  title: string
  path: string
  preview: boolean
  users: number
  readAllowed: number
  writeAllowed: number
  relatedShares: number
  reportText: string
}

function getDirectoryAccessReviewHistoryStorageKey(userID: string | undefined): string {
  return `${DIRECTORY_ACCESS_REVIEW_HISTORY_STORAGE_PREFIX}:${userID?.trim() || 'anonymous'}`
}

function isDirectoryAccessReviewHistoryEntry(value: unknown): value is DirectoryAccessReviewHistoryEntry {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return false
  }
  const entry = value as DirectoryAccessReviewHistoryEntry
  return typeof entry.id === 'string' && entry.id.trim() !== ''
    && typeof entry.recordedAt === 'string' && entry.recordedAt.trim() !== ''
    && (entry.reviewer === undefined || typeof entry.reviewer === 'string')
    && typeof entry.title === 'string' && entry.title.trim() !== ''
    && typeof entry.path === 'string' && entry.path.trim() !== ''
    && typeof entry.preview === 'boolean'
    && Number.isSafeInteger(entry.users) && entry.users >= 0
    && Number.isSafeInteger(entry.readAllowed) && entry.readAllowed >= 0
    && Number.isSafeInteger(entry.writeAllowed) && entry.writeAllowed >= 0
    && Number.isSafeInteger(entry.relatedShares) && entry.relatedShares >= 0
    && typeof entry.reportText === 'string' && entry.reportText.trim() !== ''
}

function loadDirectoryAccessReviewHistory(storageKey: string): DirectoryAccessReviewHistoryEntry[] {
  if (typeof window === 'undefined') {
    return []
  }
  try {
    const raw = window.localStorage.getItem(storageKey)
    if (!raw) {
      return []
    }
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) {
      return []
    }
    return parsed
      .filter(isDirectoryAccessReviewHistoryEntry)
      .slice(0, DIRECTORY_ACCESS_REVIEW_HISTORY_LIMIT)
  } catch {
    return []
  }
}

function saveDirectoryAccessReviewHistory(storageKey: string, entries: DirectoryAccessReviewHistoryEntry[]): boolean {
  if (typeof window === 'undefined') {
    return false
  }
  try {
    window.localStorage.setItem(storageKey, JSON.stringify(entries.slice(0, DIRECTORY_ACCESS_REVIEW_HISTORY_LIMIT)))
    return true
  } catch {
    return false
  }
}

function createDirectoryAccessReviewHistoryEntry(
  report: DirectoryAccessReportData,
  title: string,
  reportText: string,
): DirectoryAccessReviewHistoryEntry {
  const fallbackID = `${Date.now()}-${Math.random().toString(36).slice(2)}`
  return {
    id: typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : fallbackID,
    recordedAt: new Date().toISOString(),
    title,
    path: report.path,
    preview: report.preview === true,
    users: report.summary.users,
    readAllowed: report.summary.read_allowed,
    writeAllowed: report.summary.write_allowed,
    relatedShares: report.summary.related_shares,
    reportText,
  }
}

function createDirectoryAccessReviewRecordRequest(
  report: DirectoryAccessReportData,
  title: string,
  reportText: string,
): DirectoryAccessReviewRecordCreateRequest {
  return {
    title,
    path: report.path,
    preview: report.preview === true,
    users: report.summary.users,
    read_allowed: report.summary.read_allowed,
    read_denied: report.summary.read_denied,
    write_allowed: report.summary.write_allowed,
    write_denied: report.summary.write_denied,
    related_shares: report.summary.related_shares,
    active_related_shares: report.summary.active_related_shares,
    password_protected_shares: report.summary.password_protected_shares,
    report_text: reportText,
  }
}

function directoryAccessReviewRecordToHistoryEntry(record: DirectoryAccessReviewRecord): DirectoryAccessReviewHistoryEntry {
  return {
    id: record.id,
    recordedAt: record.reviewed_at,
    reviewer: record.reviewer,
    title: record.title,
    path: record.path,
    preview: record.preview,
    users: record.users,
    readAllowed: record.read_allowed,
    writeAllowed: record.write_allowed,
    relatedShares: record.related_shares,
    reportText: record.report_text,
  }
}

function directoryAccessReviewHistoryKey(entry: DirectoryAccessReviewHistoryEntry): string {
  return `${entry.path}\u0000${entry.title}\u0000${entry.preview ? 'preview' : 'saved'}`
}

function mergeDirectoryAccessReviewHistory(
  primary: DirectoryAccessReviewHistoryEntry[],
  fallback: DirectoryAccessReviewHistoryEntry[],
): DirectoryAccessReviewHistoryEntry[] {
  const seen = new Set<string>()
  const merged: DirectoryAccessReviewHistoryEntry[] = []
  for (const entry of [...primary, ...fallback]) {
    const key = directoryAccessReviewHistoryKey(entry)
    if (seen.has(key)) {
      continue
    }
    seen.add(key)
    merged.push(entry)
    if (merged.length >= DIRECTORY_ACCESS_REVIEW_HISTORY_LIMIT) {
      break
    }
  }
  return merged
}

function formatDirectoryAccessReviewHistoryTime(value: string): string {
  const timestamp = Date.parse(value)
  if (Number.isNaN(timestamp)) {
    return value
  }
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(timestamp))
}

function DirectoryAccessReviewHistory({
  entries,
  onCopy,
  onClear,
}: {
  entries: DirectoryAccessReviewHistoryEntry[]
  onCopy: (entry: DirectoryAccessReviewHistoryEntry) => void
  onClear: () => void
}) {
  return (
    <div aria-label="目录权限近期复核历史" className="rounded-lg border border-divider bg-content1/60 p-3">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="text-sm font-semibold text-foreground">近期复核历史</div>
          <div className="mt-1 text-xs text-default-500">保留最近 {DIRECTORY_ACCESS_REVIEW_HISTORY_LIMIT} 条目录权限矩阵或变更预览记录；服务端不可用时使用当前浏览器记录。</div>
        </div>
        <Button
          size="sm"
          variant="light"
          className="self-start rounded-lg text-default-500 sm:self-center"
          isDisabled={entries.length === 0}
          onPress={onClear}
        >
          清空近期记录
        </Button>
      </div>
      {entries.length === 0 ? (
        <div className="mt-3 rounded-lg border border-dashed border-divider bg-content2/40 px-3 py-2 text-sm text-default-500">
          暂无近期目录权限复核记录。
        </div>
      ) : (
        <div className="mt-3 space-y-2">
          {entries.map((entry) => (
            <div key={entry.id} className="flex flex-col gap-2 rounded-lg border border-divider bg-content2/50 px-3 py-2 sm:flex-row sm:items-center sm:justify-between">
              <div className="min-w-0">
                <div className="flex min-w-0 flex-wrap items-center gap-2 text-sm font-medium text-foreground">
                  <span className="truncate font-mono">{entry.path}</span>
                  <span className="rounded-full bg-content1 px-2 py-0.5 text-xs text-default-600">{entry.preview ? '变更预览' : entry.title}</span>
                </div>
                <div className="mt-1 flex flex-wrap gap-2 text-xs text-default-500">
                  <span>{formatDirectoryAccessReviewHistoryTime(entry.recordedAt)}</span>
                  {entry.reviewer ? <span>复核人 {entry.reviewer}</span> : null}
                  <span>用户 {entry.users}</span>
                  <span>可读 {entry.readAllowed}</span>
                  <span>可写 {entry.writeAllowed}</span>
                  <span>相关分享 {entry.relatedShares}</span>
                </div>
              </div>
              <Button
                size="sm"
                variant="flat"
                className="self-start rounded-lg sm:self-center"
                startContent={<Copy size={14} />}
                onPress={() => onCopy(entry)}
              >
                复制记录
              </Button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function DirectoryAccessReportResult({
  report,
  title = '用户矩阵',
  ariaLabel = '目录权限用户矩阵',
  onSaveReviewHistory,
}: {
  report: DirectoryAccessReportData
  title?: string
  ariaLabel?: string
  onSaveReviewHistory?: (report: DirectoryAccessReportData, title: string, reportText: string) => DirectoryAccessReviewSaveResult | Promise<DirectoryAccessReviewSaveResult>
}) {
  const shares = report.shares ?? []
  const ruleEffects = report.rule_effects ?? []
  const handleCopyDirectoryAccessReport = async () => {
    try {
      const reportText = formatDirectoryAccessReportForClipboard(report, title)
      await copyTextToClipboard(reportText)
      const saved = await onSaveReviewHistory?.(report, title, reportText)
      if (saved === 'failed') {
        addToast({
          title: '目录权限复核记录已复制',
          description: '近期历史写入失败，报告内容已复制到剪贴板。',
          color: 'warning',
        })
        return
      }
      if (saved === 'local') {
        addToast({
          title: '目录权限复核记录已复制',
          description: '服务端历史暂不可用，记录已保存在当前浏览器。',
          color: 'warning',
        })
        return
      }
      addToast({ title: '目录权限复核记录已复制并保存', color: 'success' })
    } catch {
      addToast({
        title: '复制目录权限复核记录失败',
        description: '请检查浏览器剪贴板权限。',
        color: 'danger',
      })
    }
  }

  return (
    <div className="rounded-lg border border-divider bg-content2/40 p-3" aria-label={ariaLabel}>
      <div className="mb-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="text-sm font-semibold text-foreground">{title}</div>
        <Button
          size="sm"
          variant="flat"
          className="self-start rounded-lg sm:self-auto"
          startContent={<Copy size={14} />}
          onPress={handleCopyDirectoryAccessReport}
        >
          复制复核记录
        </Button>
      </div>
      <div className="mb-3 flex flex-wrap items-center gap-2 text-xs text-default-500">
        <span className="rounded-full bg-content1 px-2 py-1 font-mono text-foreground">{report.path}</span>
        <span className="rounded-full bg-content1 px-2 py-1">用户 {report.summary.users}</span>
        <span className="rounded-full bg-success/10 px-2 py-1 text-success">可读 {report.summary.read_allowed}</span>
        <span className="rounded-full bg-success/10 px-2 py-1 text-success">可写 {report.summary.write_allowed}</span>
        <span className="rounded-full bg-danger/10 px-2 py-1 text-danger">读拒绝 {report.summary.read_denied}</span>
        <span className="rounded-full bg-danger/10 px-2 py-1 text-danger">写拒绝 {report.summary.write_denied}</span>
        <span className="rounded-full bg-warning/10 px-2 py-1 text-warning">相关分享 {report.summary.related_shares}</span>
        <span className="rounded-full bg-warning/10 px-2 py-1 text-warning">活跃分享 {report.summary.active_related_shares}</span>
        <span className="rounded-full bg-content1 px-2 py-1">密码分享 {report.summary.password_protected_shares}</span>
        <span className="rounded-full bg-content1 px-2 py-1">命中规则 {ruleEffects.length}</span>
      </div>
      <div className="max-h-72 overflow-auto rounded-lg border border-divider bg-content1">
        {report.users.map((entry) => (
          <div key={entry.user_id || entry.username} className="grid gap-3 border-b border-divider px-3 py-2 last:border-b-0 sm:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)_minmax(0,1fr)]">
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold text-foreground">{entry.username}</div>
              <div className="truncate text-xs text-default-500">{entry.role} · {entry.home_dir}</div>
            </div>
            <div className="flex items-center gap-2 text-sm">
              <span className={cn('h-2.5 w-2.5 shrink-0 rounded-full', entry.read.allowed ? 'bg-success' : 'bg-danger')} />
              <span className="min-w-0 truncate">读：{entry.read.allowed ? '允许' : '拒绝'} · {directoryAccessSourceLabel(entry.read.source)}</span>
            </div>
            <div className="flex items-center gap-2 text-sm">
              <span className={cn('h-2.5 w-2.5 shrink-0 rounded-full', entry.write.allowed ? 'bg-success' : 'bg-danger')} />
              <span className="min-w-0 truncate">写：{entry.write.allowed ? '允许' : '拒绝'} · {directoryAccessSourceLabel(entry.write.source)}</span>
            </div>
          </div>
        ))}
      </div>
      <div className="mt-3 rounded-lg border border-divider bg-content1" aria-label={`${title}规则生效明细`}>
        {ruleEffects.length === 0 ? (
          <div className="px-3 py-2 text-sm text-default-500">未命中目录规则</div>
        ) : ruleEffects.map((entry) => (
          <div key={`${entry.index}:${entry.path}`} className="grid gap-3 border-b border-divider px-3 py-2 last:border-b-0 sm:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)_minmax(0,1fr)]">
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold text-foreground">规则 {entry.index + 1} · {entry.path}</div>
              {entry.user_samples?.length ? (
                <div className="truncate text-xs text-default-500">用户 {entry.user_samples.join(', ')}</div>
              ) : null}
            </div>
            <div className="flex flex-wrap items-center gap-1 text-xs">
              <span className="rounded-full bg-success/10 px-2 py-0.5 text-success">读允许 {entry.read_allowed}</span>
              <span className="rounded-full bg-danger/10 px-2 py-0.5 text-danger">读拒绝 {entry.read_denied}</span>
            </div>
            <div className="flex flex-wrap items-center gap-1 text-xs">
              <span className="rounded-full bg-success/10 px-2 py-0.5 text-success">写允许 {entry.write_allowed}</span>
              <span className="rounded-full bg-danger/10 px-2 py-0.5 text-danger">写拒绝 {entry.write_denied}</span>
            </div>
          </div>
        ))}
      </div>
      <div className="mt-3 rounded-lg border border-divider bg-content1">
        {shares.length === 0 ? (
          <div className="px-3 py-2 text-sm text-default-500">无相关分享</div>
        ) : shares.map((entry) => (
          <div key={entry.id} className="grid gap-3 border-b border-divider px-3 py-2 last:border-b-0 sm:grid-cols-[minmax(0,1.4fr)_minmax(0,0.8fr)_minmax(0,1fr)]">
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold text-foreground">{entry.path}</div>
              <div className="truncate text-xs text-default-500">{entry.type === 'folder' ? '文件夹' : '文件'} · {directoryAccessShareRelationLabel(entry.relation)}</div>
            </div>
            <div className="flex flex-wrap items-center gap-1 text-xs">
              <span className={cn('rounded-full px-2 py-0.5', entry.active ? 'bg-warning/10 text-warning' : 'bg-content2 text-default-500')}>
                {entry.active ? '可访问' : '不可访问'}
              </span>
              {entry.has_password && <span className="rounded-full bg-content2 px-2 py-0.5 text-default-600">密码</span>}
            </div>
            <div className="min-w-0 truncate text-xs text-default-500">
              访问 {entry.access_count}{entry.max_access > 0 ? ` / ${entry.max_access}` : ''}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

type DirectoryAccessRuleReviewKind = 'added' | 'changed' | 'removed'

type DirectoryAccessRuleReviewItem = {
  kind: DirectoryAccessRuleReviewKind
  path: string
  before?: DirectoryAccessRule
  after?: DirectoryAccessRule
  changedFields: string[]
}

const directoryAccessRuleReviewFields: Array<{
  key: keyof Omit<DirectoryAccessRule, 'path'>
  label: string
}> = [
  { key: 'read_users', label: '读用户' },
  { key: 'write_users', label: '写用户' },
  { key: 'read_groups', label: '读组' },
  { key: 'write_groups', label: '写组' },
  { key: 'read_roles', label: '读角色' },
  { key: 'write_roles', label: '写角色' },
]

function normalizeDirectoryAccessReviewList(values: string[] | undefined): string[] {
  return Array.from(new Set((values ?? []).map((value) => value.trim().toLowerCase()).filter(Boolean))).sort()
}

function directoryAccessReviewListEquals(left: string[] | undefined, right: string[] | undefined): boolean {
  const normalizedLeft = normalizeDirectoryAccessReviewList(left)
  const normalizedRight = normalizeDirectoryAccessReviewList(right)
  return normalizedLeft.length === normalizedRight.length
    && normalizedLeft.every((value, index) => value === normalizedRight[index])
}

function getDirectoryAccessRuleChangedFields(before: DirectoryAccessRule, after: DirectoryAccessRule): string[] {
  return directoryAccessRuleReviewFields
    .filter(({ key }) => !directoryAccessReviewListEquals(before[key], after[key]))
    .map(({ label }) => label)
}

function directoryAccessRulePrincipalSummary(rule: DirectoryAccessRule): string {
  const parts = directoryAccessRuleReviewFields
    .map(({ key, label }) => {
      const values = normalizeDirectoryAccessReviewList(rule[key])
      return values.length > 0 ? `${label}: ${values.join(', ')}` : ''
    })
    .filter(Boolean)

  return parts.length > 0 ? parts.join(' · ') : '未配置主体'
}

function addDirectoryAccessPrincipals(target: Set<string>, type: string, values: string[] | undefined) {
  normalizeDirectoryAccessReviewList(values).forEach((value) => {
    target.add(`${type}:${value}`)
  })
}

function getDirectoryAccessRuleReadPrincipalSet(rule: DirectoryAccessRule): Set<string> {
  const principals = new Set<string>()
  addDirectoryAccessPrincipals(principals, 'user', rule.read_users)
  addDirectoryAccessPrincipals(principals, 'group', rule.read_groups)
  addDirectoryAccessPrincipals(principals, 'role', rule.read_roles)
  addDirectoryAccessPrincipals(principals, 'user', rule.write_users)
  addDirectoryAccessPrincipals(principals, 'group', rule.write_groups)
  addDirectoryAccessPrincipals(principals, 'role', rule.write_roles)
  return principals
}

function getDirectoryAccessRuleWritePrincipalSet(rule: DirectoryAccessRule): Set<string> {
  const principals = new Set<string>()
  addDirectoryAccessPrincipals(principals, 'user', rule.write_users)
  addDirectoryAccessPrincipals(principals, 'group', rule.write_groups)
  addDirectoryAccessPrincipals(principals, 'role', rule.write_roles)
  return principals
}

function directoryAccessRuleHasWriteGrant(rule: DirectoryAccessRule): boolean {
  return getDirectoryAccessRuleWritePrincipalSet(rule).size > 0
}

function getDirectoryAccessRuleAttentionReason(rule: DirectoryAccessRule): string | null {
  const writeRoles = normalizeDirectoryAccessReviewList(rule.write_roles)
  const readRoles = normalizeDirectoryAccessReviewList(rule.read_roles)
  if (rule.path === '/') {
    return '根路径授权'
  }
  if (writeRoles.includes('guest')) {
    return '访客角色可写'
  }
  if (writeRoles.includes('user')) {
    return '普通用户可写'
  }
  if (readRoles.includes('guest')) {
    return '访客角色可读'
  }
  return null
}

function DirectoryAccessCoverageSummary({ draftValue }: { draftValue: string }) {
  const parsedDraft = parseDirectoryAccessRuleLines(draftValue)

  if (parsedDraft.error) {
    return (
      <div className="rounded-lg border border-warning/20 bg-warning/10 px-3 py-2 text-xs text-warning">
        目录权限覆盖摘要暂不可用：{parsedDraft.error}
      </div>
    )
  }

  const rules = parsedDraft.rules
  if (rules.length === 0) {
    return (
      <div aria-label="目录权限覆盖摘要" className="rounded-lg border border-dashed border-divider bg-content2/40 px-3 py-3 text-sm text-default-500">
        暂无目录权限规则。未命中规则的路径会继续按用户 home_dir 边界处理。
      </div>
    )
  }

  const readPrincipals = new Set<string>()
  const writePrincipals = new Set<string>()
  rules.forEach((rule) => {
    getDirectoryAccessRuleReadPrincipalSet(rule).forEach((principal) => readPrincipals.add(principal))
    getDirectoryAccessRuleWritePrincipalSet(rule).forEach((principal) => writePrincipals.add(principal))
  })
  const writeRuleCount = rules.filter(directoryAccessRuleHasWriteGrant).length
  const attentionItems = rules
    .map((rule) => ({ rule, reason: getDirectoryAccessRuleAttentionReason(rule) }))
    .filter((item): item is { rule: DirectoryAccessRule; reason: string } => Boolean(item.reason))
    .slice(0, 5)

  const summaryItems = [
    { label: '规则总数', value: `${rules.length} 条`, detail: '按最具体路径生效' },
    { label: '有效可读主体', value: `${readPrincipals.size} 个`, detail: '写权限同时计入读权限' },
    { label: '可写主体', value: `${writePrincipals.size} 个`, detail: '仅统计显式写授权' },
    { label: '写权限路径', value: `${writeRuleCount} 个`, detail: '需要重点复核写入边界' },
  ]

  return (
    <div aria-label="目录权限覆盖摘要" className="rounded-lg border border-divider bg-content1/60 p-3">
      <div className="flex flex-col gap-1">
        <div className="text-sm font-semibold text-foreground">目录权限覆盖摘要</div>
        <div className="text-xs text-default-500">基于当前编辑内容估算覆盖规模；最终结果仍以保存后的权限检查和用户矩阵为准。</div>
      </div>
      <div className="mt-3 grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
        {summaryItems.map((item) => (
          <div key={item.label} className="rounded-lg border border-default-200 bg-content2/40 px-3 py-2">
            <div className="text-xs text-default-500">{item.label}</div>
            <div className="mt-1 text-base font-semibold text-foreground">{item.value}</div>
            <div className="mt-1 text-xs text-default-500">{item.detail}</div>
          </div>
        ))}
      </div>
      {attentionItems.length > 0 ? (
        <div className="mt-3 rounded-lg border border-warning/25 bg-warning/5 p-3">
          <div className="flex items-center gap-2 text-xs font-medium text-warning">
            <AlertCircle size={14} />
            <span>权限关注项</span>
          </div>
          <div className="mt-2 space-y-2">
            {attentionItems.map(({ rule, reason }) => (
              <div key={`${reason}:${rule.path}`} className="text-xs text-default-600">
                <span className="font-mono font-semibold text-foreground">{rule.path}</span>
                <span className="mx-2 text-default-300">·</span>
                <span className="text-warning">{reason}</span>
                <span className="mx-2 text-default-300">·</span>
                <span>{directoryAccessRulePrincipalSummary(rule)}</span>
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div className="mt-3 rounded-lg border border-success/20 bg-success/5 px-3 py-2 text-xs text-success">
          未发现根路径授权、访客授权或普通用户宽写入规则。
        </div>
      )}
    </div>
  )
}

function buildDirectoryAccessRuleReview(
  savedRules: DirectoryAccessRule[],
  draftRules: DirectoryAccessRule[],
): DirectoryAccessRuleReviewItem[] {
  const savedByPath = new Map(savedRules.map((rule) => [rule.path, rule]))
  const draftByPath = new Map(draftRules.map((rule) => [rule.path, rule]))
  const items: DirectoryAccessRuleReviewItem[] = []

  for (const draftRule of draftRules) {
    const savedRule = savedByPath.get(draftRule.path)
    if (!savedRule) {
      items.push({
        kind: 'added',
        path: draftRule.path,
        after: draftRule,
        changedFields: [],
      })
      continue
    }
    const changedFields = getDirectoryAccessRuleChangedFields(savedRule, draftRule)
    if (changedFields.length > 0) {
      items.push({
        kind: 'changed',
        path: draftRule.path,
        before: savedRule,
        after: draftRule,
        changedFields,
      })
    }
  }

  for (const savedRule of savedRules) {
    if (!draftByPath.has(savedRule.path)) {
      items.push({
        kind: 'removed',
        path: savedRule.path,
        before: savedRule,
        changedFields: [],
      })
    }
  }

  return items
}

function directoryAccessRuleReviewKindLabel(kind: DirectoryAccessRuleReviewKind): string {
  switch (kind) {
    case 'added':
      return '新增'
    case 'changed':
      return '修改'
    case 'removed':
      return '删除'
    default:
      return kind
  }
}

function directoryAccessRuleReviewTone(kind: DirectoryAccessRuleReviewKind): string {
  switch (kind) {
    case 'added':
      return 'border-success/30 bg-success/5 text-success'
    case 'changed':
      return 'border-warning/30 bg-warning/5 text-warning'
    case 'removed':
      return 'border-danger/30 bg-danger/5 text-danger'
    default:
      return 'border-divider bg-content2 text-default-600'
  }
}

function DirectoryAccessRuleChangeReview({
  savedRules,
  draftValue,
}: {
  savedRules: DirectoryAccessRule[]
  draftValue: string
}) {
  const parsedDraft = parseDirectoryAccessRuleLines(draftValue)

  if (parsedDraft.error) {
    return (
      <div className="rounded-lg border border-warning/20 bg-warning/10 px-3 py-2 text-xs text-warning">
        目录权限变更复核暂不可用：{parsedDraft.error}
      </div>
    )
  }

  const reviewItems = buildDirectoryAccessRuleReview(savedRules, parsedDraft.rules)
  const added = reviewItems.filter((item) => item.kind === 'added').length
  const changed = reviewItems.filter((item) => item.kind === 'changed').length
  const removed = reviewItems.filter((item) => item.kind === 'removed').length

  return (
    <div className="rounded-lg border border-divider bg-content1/60 p-3">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="text-sm font-semibold text-foreground">目录权限变更复核</div>
          <div className="mt-1 text-xs text-default-500">基于已保存目录规则和当前编辑内容生成。</div>
        </div>
        <div className="flex flex-wrap gap-2 text-xs">
          <span className="rounded-full bg-success/10 px-2 py-1 text-success">新增 {added}</span>
          <span className="rounded-full bg-warning/10 px-2 py-1 text-warning">修改 {changed}</span>
          <span className="rounded-full bg-danger/10 px-2 py-1 text-danger">删除 {removed}</span>
        </div>
      </div>
      {reviewItems.length === 0 ? (
        <div className="mt-3 rounded-lg border border-divider bg-content2/40 px-3 py-2 text-sm text-default-500">
          目录权限与已保存配置一致。
        </div>
      ) : (
        <div className="mt-3 space-y-2">
          {reviewItems.map((item) => {
            const activeRule = item.after ?? item.before
            return (
              <div key={`${item.kind}:${item.path}`} className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
                <div className="flex flex-wrap items-center gap-2">
                  <span className={cn('rounded-full border px-2 py-0.5 text-xs font-medium', directoryAccessRuleReviewTone(item.kind))}>
                    {directoryAccessRuleReviewKindLabel(item.kind)}
                  </span>
                  <span className="font-mono text-sm font-semibold text-foreground">{item.path}</span>
                  {item.changedFields.length > 0 && (
                    <span className="text-xs text-default-500">变更字段：{item.changedFields.join('、')}</span>
                  )}
                </div>
                {activeRule && (
                  <div className="mt-1 break-anywhere text-xs text-default-500">
                    {directoryAccessRulePrincipalSummary(activeRule)}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

interface DirectoryAccessRuleDraft {
  path: string
  readUsers: string
  writeUsers: string
  readGroups: string
  writeGroups: string
  readRoles: string
  writeRoles: string
}

type DirectoryAccessRuleDraftField = keyof DirectoryAccessRuleDraft

type DirectoryAccessRulePreset = {
  label: string
  description: string
  draft: DirectoryAccessRuleDraft
}

function emptyDirectoryAccessRuleDraft(): DirectoryAccessRuleDraft {
  return {
    path: '',
    readUsers: '',
    writeUsers: '',
    readGroups: '',
    writeGroups: '',
    readRoles: '',
    writeRoles: '',
  }
}

const directoryAccessRulePresets: DirectoryAccessRulePreset[] = [
  {
    label: '全员协作',
    description: '普通用户可读写 /shared，适合家庭共享或小团队资料。',
    draft: {
      ...emptyDirectoryAccessRuleDraft(),
      path: '/shared',
      readRoles: 'user',
      writeRoles: 'user',
    },
  },
  {
    label: '全员只读',
    description: '普通用户可读取 /library，只有管理员可写入。',
    draft: {
      ...emptyDirectoryAccessRuleDraft(),
      path: '/library',
      readRoles: 'user',
      writeRoles: 'admin',
    },
  },
  {
    label: '管理员归档',
    description: '仅管理员可读写 /archive，适合长期归档或敏感资料。',
    draft: {
      ...emptyDirectoryAccessRuleDraft(),
      path: '/archive',
      readRoles: 'admin',
      writeRoles: 'admin',
    },
  },
]

function directoryAccessRuleToDraft(rule: DirectoryAccessRule): DirectoryAccessRuleDraft {
  return {
    path: rule.path,
    readUsers: (rule.read_users ?? []).join(', '),
    writeUsers: (rule.write_users ?? []).join(', '),
    readGroups: (rule.read_groups ?? []).join(', '),
    writeGroups: (rule.write_groups ?? []).join(', '),
    readRoles: (rule.read_roles ?? []).join(', '),
    writeRoles: (rule.write_roles ?? []).join(', '),
  }
}

function rawDirectoryAccessRuleLineToDraft(line: string): DirectoryAccessRuleDraft {
  const draft = emptyDirectoryAccessRuleDraft()
  const parsedLine = parseLogicalPathLineHead(line, 0)
  draft.path = parsedLine.path
  const tokens = parsedLine.rest ? parsedLine.rest.split(/\s+/).filter(Boolean) : []
  for (const token of tokens) {
    const separator = token.indexOf('=')
    if (separator <= 0 || separator === token.length - 1) {
      continue
    }
    const key = token.slice(0, separator)
    const value = token.slice(separator + 1).split(',').join(', ')
    switch (key) {
      case 'read_users':
        draft.readUsers = value
        break
      case 'write_users':
        draft.writeUsers = value
        break
      case 'read_groups':
        draft.readGroups = value
        break
      case 'write_groups':
        draft.writeGroups = value
        break
      case 'read_roles':
        draft.readRoles = value
        break
      case 'write_roles':
        draft.writeRoles = value
        break
    }
  }
  return draft
}

function directoryAccessRuleDraftsFromText(value: string): DirectoryAccessRuleDraft[] {
  const parsed = parseDirectoryAccessRuleLines(value)
  if (!parsed.error) {
    return parsed.rules.length > 0
      ? parsed.rules.map(directoryAccessRuleToDraft)
      : [emptyDirectoryAccessRuleDraft()]
  }

  const drafts = value
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map(rawDirectoryAccessRuleLineToDraft)
  return drafts.length > 0 ? drafts : [emptyDirectoryAccessRuleDraft()]
}

function directoryAccessDraftList(value: string): string[] {
  return value
    .split(',')
    .map((entry) => entry.trim())
    .filter(Boolean)
}

function appendDirectoryAccessDraftList(parts: string[], key: string, value: string) {
  const values = directoryAccessDraftList(value)
  if (values.length > 0) {
    parts.push(`${key}=${values.join(',')}`)
  }
}

function formatDirectoryAccessRuleDrafts(drafts: DirectoryAccessRuleDraft[]): string {
  return drafts
    .map((draft) => {
      const trimmedPath = draft.path.trim()
      const parts = [trimmedPath ? formatLogicalPathLineToken(trimmedPath) : '']
      appendDirectoryAccessDraftList(parts, 'read_users', draft.readUsers)
      appendDirectoryAccessDraftList(parts, 'write_users', draft.writeUsers)
      appendDirectoryAccessDraftList(parts, 'read_groups', draft.readGroups)
      appendDirectoryAccessDraftList(parts, 'write_groups', draft.writeGroups)
      appendDirectoryAccessDraftList(parts, 'read_roles', draft.readRoles)
      appendDirectoryAccessDraftList(parts, 'write_roles', draft.writeRoles)
      return parts.filter(Boolean).join(' ')
    })
    .filter(Boolean)
    .join('\n')
}

function isDirectoryAccessRuleDraftEmpty(draft: DirectoryAccessRuleDraft): boolean {
  return Object.values(draft).every((value) => value.trim() === '')
}

function getDirectoryAccessDraftPathKey(draft: DirectoryAccessRuleDraft): string {
  return normalizeDirectoryQuotaPathInput(draft.path) ?? draft.path.trim()
}

function DirectoryAccessRuleEditor({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const [editorState, setEditorState] = useState(() => ({
    sourceValue: value,
    drafts: directoryAccessRuleDraftsFromText(value),
  }))

  const drafts = editorState.sourceValue === value
    ? editorState.drafts
    : directoryAccessRuleDraftsFromText(value)

  const commitDrafts = (nextDrafts: DirectoryAccessRuleDraft[]) => {
    const renderedDrafts = nextDrafts.length > 0 ? nextDrafts : [emptyDirectoryAccessRuleDraft()]
    const serialized = formatDirectoryAccessRuleDrafts(renderedDrafts)
    setEditorState({ sourceValue: serialized, drafts: renderedDrafts })
    onChange(serialized)
  }

  const updateDraft = (index: number, field: DirectoryAccessRuleDraftField, nextValue: string) => {
    const nextDrafts = drafts.map((draft, draftIndex) => (
      draftIndex === index ? { ...draft, [field]: nextValue } : draft
    ))
    commitDrafts(nextDrafts)
  }

  const addDraft = () => {
    const nextDrafts = [...drafts, emptyDirectoryAccessRuleDraft()]
    commitDrafts(nextDrafts)
  }

  const applyPreset = (preset: DirectoryAccessRulePreset) => {
    const presetDraft = { ...preset.draft }
    const currentDrafts = drafts.length === 1 && isDirectoryAccessRuleDraftEmpty(drafts[0])
      ? []
      : drafts
    const presetPathKey = getDirectoryAccessDraftPathKey(presetDraft)
    const existingIndex = currentDrafts.findIndex((draft) => getDirectoryAccessDraftPathKey(draft) === presetPathKey)
    const nextDrafts = existingIndex >= 0
      ? currentDrafts.map((draft, index) => (index === existingIndex ? presetDraft : draft))
      : [...currentDrafts, presetDraft]
    commitDrafts(nextDrafts)
  }

  const removeDraft = (index: number) => {
    const nextDrafts = drafts.filter((_, draftIndex) => draftIndex !== index)
    commitDrafts(nextDrafts)
  }

  return (
    <div className="space-y-3">
      <div className="rounded-lg border border-divider bg-content1/60 p-3">
        <div className="flex flex-col gap-1">
          <div className="text-sm font-semibold text-foreground">权限策略预设</div>
          <div className="text-xs leading-5 text-default-500">
            从常见家庭和小团队目录策略开始，再按实际用户或用户组调整。
          </div>
        </div>
        <div className="mt-3 grid gap-2 lg:grid-cols-3">
          {directoryAccessRulePresets.map((preset) => (
            <Button
              key={preset.label}
              variant="flat"
              className="h-auto justify-start rounded-lg px-3 py-2 text-left"
              onPress={() => applyPreset(preset)}
            >
              <span className="min-w-0">
                <span className="block text-sm font-medium">{preset.label}</span>
                <span className="mt-1 block whitespace-normal text-xs leading-5 text-default-500">{preset.description}</span>
              </span>
            </Button>
          ))}
        </div>
      </div>
      {drafts.map((draft, index) => {
        const ruleNumber = index + 1
        return (
          <div key={index} className="rounded-lg border border-divider bg-content1/60 p-3">
            <div className="mb-3 flex items-center justify-between gap-3">
              <div className="text-sm font-semibold text-foreground">规则 {ruleNumber}</div>
              <Button
                isIconOnly
                variant="light"
                color="danger"
                className="rounded-lg"
                aria-label={`删除目录权限规则 ${ruleNumber}`}
                onPress={() => removeDraft(index)}
                isDisabled={drafts.length === 1 && !value.trim()}
              >
                <Trash2 size={16} />
              </Button>
            </div>
            <div className="grid gap-3 lg:grid-cols-3">
              <Input
                label="路径"
                aria-label={`目录权限路径 ${ruleNumber}`}
                value={draft.path}
                onValueChange={(nextValue) => updateDraft(index, 'path', nextValue)}
                placeholder="/team"
                className="input-shell lg:col-span-3"
              />
              <Input
                label="读用户"
                aria-label={`读用户 ${ruleNumber}`}
                value={draft.readUsers}
                onValueChange={(nextValue) => updateDraft(index, 'readUsers', nextValue)}
                placeholder="alice,bob"
                className="input-shell"
              />
              <Input
                label="写用户"
                aria-label={`写用户 ${ruleNumber}`}
                value={draft.writeUsers}
                onValueChange={(nextValue) => updateDraft(index, 'writeUsers', nextValue)}
                placeholder="alice"
                className="input-shell"
              />
              <Input
                label="读组"
                aria-label={`读组 ${ruleNumber}`}
                value={draft.readGroups}
                onValueChange={(nextValue) => updateDraft(index, 'readGroups', nextValue)}
                placeholder="family"
                className="input-shell"
              />
              <Input
                label="写组"
                aria-label={`写组 ${ruleNumber}`}
                value={draft.writeGroups}
                onValueChange={(nextValue) => updateDraft(index, 'writeGroups', nextValue)}
                placeholder="editors"
                className="input-shell"
              />
              <Input
                label="读角色"
                aria-label={`读角色 ${ruleNumber}`}
                value={draft.readRoles}
                onValueChange={(nextValue) => updateDraft(index, 'readRoles', nextValue)}
                placeholder="user"
                className="input-shell"
              />
              <Input
                label="写角色"
                aria-label={`写角色 ${ruleNumber}`}
                value={draft.writeRoles}
                onValueChange={(nextValue) => updateDraft(index, 'writeRoles', nextValue)}
                placeholder="admin"
                className="input-shell"
              />
            </div>
          </div>
        )
      })}
      <Button variant="bordered" className="rounded-lg" onPress={addDraft} startContent={<Plus size={16} />}>
        添加规则
      </Button>
    </div>
  )
}

// Setting row component
function SettingRow({ 
  label, 
  description, 
  children 
}: { 
  label: string
  description?: string
  children: React.ReactNode 
}) {
  return (
    <div className="flex flex-col gap-2 py-2.5 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0 flex-1 sm:pr-4">
        <div className="text-sm font-medium text-foreground">{label}</div>
        {description && (
          <div className="text-xs text-default-500 mt-0.5">{description}</div>
        )}
      </div>
      <div className="w-full min-w-0 sm:w-auto sm:shrink-0">
        {children}
      </div>
    </div>
  )
}

function sharePolicyRuleHasConstraint(rule: SharePolicyRuleDraft): boolean {
  const maxExpiresIn = rule.max_expires_in?.trim()
  return Boolean(
    rule.require_password ||
    (maxExpiresIn && !isZeroDurationString(maxExpiresIn)) ||
    (rule.max_access && rule.max_access > 0) ||
    (rule.allowed_users_input?.trim() || rule.allowed_users?.length) ||
    (rule.allowed_groups_input?.trim() || rule.allowed_groups?.length) ||
    (rule.allowed_roles_input?.trim() || rule.allowed_roles?.length),
  )
}

function SharePolicyRuleEditor({
  rules,
  isDisabled,
  onChange,
}: {
  rules: SharePolicyRuleDraft[]
  isDisabled?: boolean
  onChange: (rules: SharePolicyRuleDraft[]) => void
}) {
  const commitRules = (nextRules: SharePolicyRuleDraft[]) => {
    onChange(nextRules)
  }

  const updateRule = (index: number, patch: Partial<SharePolicyRuleDraft>) => {
    const nextRules = rules.map((rule, ruleIndex) => (
      ruleIndex === index
        ? { ...rule, ...patch }
        : rule
    ))
    commitRules(nextRules)
  }

  const addRule = () => {
    commitRules([
      ...rules,
      { path: '', require_password: true },
    ])
  }

  const removeRule = (index: number) => {
    commitRules(rules.filter((_, ruleIndex) => ruleIndex !== index))
  }

  return (
    <div className="w-full space-y-3 sm:w-[42rem]">
      {rules.length === 0 ? (
        <div className="rounded-lg border border-dashed border-divider bg-content2/40 px-4 py-4 text-sm text-default-500">
          暂无路径策略。需要保护某个目录时，添加一条规则即可。
        </div>
      ) : (
        <div className="space-y-3">
          {rules.map((rule, index) => {
            const hasConstraint = sharePolicyRuleHasConstraint(rule)
            return (
              <div key={index} className="rounded-lg border border-divider bg-content2/40 p-3">
                <div className="grid gap-3 lg:grid-cols-[minmax(10rem,1.2fr)_auto_minmax(8rem,0.8fr)_minmax(8rem,0.8fr)_2.5rem] lg:items-center">
                  <Input
                    aria-label={`分享策略路径 ${index + 1}`}
                    label="路径"
                    labelPlacement="outside"
                    value={rule.path}
                    onValueChange={(nextPath) => updateRule(index, { path: nextPath })}
                    placeholder="/Family"
                    isDisabled={isDisabled}
                    classNames={{
                      inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                    }}
                  />
                  <Switch
                    aria-label={`分享策略必须设置密码 ${index + 1}`}
                    isSelected={Boolean(rule.require_password)}
                    isDisabled={isDisabled}
                    onValueChange={(selected) => updateRule(index, { require_password: selected || undefined })}
                    classNames={{
                      wrapper: cn(
                        "group-data-[selected=true]:bg-accent-primary",
                        "bg-content2"
                      ),
                    }}
                  >
                    <span className="text-sm">必须设置密码</span>
                  </Switch>
                  <Input
                    aria-label={`分享策略最长有效期 ${index + 1}`}
                    label="最长有效期"
                    labelPlacement="outside"
                    value={rule.max_expires_in ?? ''}
                    onValueChange={(nextValue) => updateRule(index, { max_expires_in: nextValue.trim() || undefined })}
                    placeholder="例如 24h"
                    isDisabled={isDisabled}
                    classNames={{
                      inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                    }}
                  />
                  <Input
                    aria-label={`分享策略最多访问次数 ${index + 1}`}
                    label="最多访问次数"
                    labelPlacement="outside"
                    type="text"
                    inputMode="numeric"
                    pattern="[0-9]*"
                    value={rule.max_access_input ?? (rule.max_access ? String(rule.max_access) : '')}
                    onValueChange={(nextValue) => {
                      const trimmed = nextValue.trim()
                      const parsed = parseNonNegativeSafeIntegerInput(trimmed)
                      updateRule(index, {
                        max_access_input: nextValue,
                        max_access: trimmed && parsed.valid ? parsed.value : undefined,
                      })
                    }}
                    placeholder="不限制"
                    isDisabled={isDisabled}
                    classNames={{
                      inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                    }}
                  />
                  <Button
                    isIconOnly
                    variant="flat"
                    color="danger"
                    aria-label={`删除分享策略 ${index + 1}`}
                    className="rounded-lg lg:self-end"
                    isDisabled={isDisabled}
                    onPress={() => removeRule(index)}
                  >
                    <Trash2 size={16} />
                  </Button>
                </div>
                <div className="mt-3 grid gap-3 md:grid-cols-3">
                  <Input
                    aria-label={`分享策略允许用户 ${index + 1}`}
                    label="允许用户"
                    labelPlacement="outside"
                    value={rule.allowed_users_input ?? (rule.allowed_users ?? []).join(', ')}
                    onValueChange={(nextValue) => updateRule(index, { allowed_users_input: nextValue })}
                    placeholder="alice,bob"
                    isDisabled={isDisabled}
                    classNames={{
                      inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                    }}
                  />
                  <Input
                    aria-label={`分享策略允许组 ${index + 1}`}
                    label="允许组"
                    labelPlacement="outside"
                    value={rule.allowed_groups_input ?? (rule.allowed_groups ?? []).join(', ')}
                    onValueChange={(nextValue) => updateRule(index, { allowed_groups_input: nextValue })}
                    placeholder="family"
                    isDisabled={isDisabled}
                    classNames={{
                      inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                    }}
                  />
                  <Input
                    aria-label={`分享策略允许角色 ${index + 1}`}
                    label="允许角色"
                    labelPlacement="outside"
                    value={rule.allowed_roles_input ?? (rule.allowed_roles ?? []).join(', ')}
                    onValueChange={(nextValue) => updateRule(index, { allowed_roles_input: nextValue })}
                    placeholder="user,admin"
                    isDisabled={isDisabled}
                    classNames={{
                      inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                    }}
                  />
                </div>
                {!hasConstraint && (
                  <div className="mt-2 text-xs text-warning">
                    至少选择一个限制条件，保存时才会生效。
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

      <Button
        variant="flat"
        size="sm"
        startContent={<Plus size={16} />}
        className="rounded-lg"
        isDisabled={isDisabled}
        onPress={addRule}
      >
        添加路径策略
      </Button>
    </div>
  )
}

function getSecurityStatusMeta(status: SecurityCheckStatus): {
  label: string
  tone: string
  badgeClassName: string
  iconClassName: string
  Icon: React.ComponentType<{ size?: number; className?: string }>
} {
  if (status === 'pass') {
    return {
      label: '通过',
      tone: 'border-success/30 bg-success/5',
      badgeClassName: 'bg-success/10 text-success',
      iconClassName: 'text-success',
      Icon: CheckCircle2,
    }
  }

  if (status === 'block') {
    return {
      label: '需修复',
      tone: 'border-danger/30 bg-danger/5',
      badgeClassName: 'bg-danger/10 text-danger',
      iconClassName: 'text-danger',
      Icon: AlertCircle,
    }
  }

  return {
    label: '需确认',
    tone: 'border-warning/30 bg-warning/5',
    badgeClassName: 'bg-warning/10 text-warning',
    iconClassName: 'text-warning',
    Icon: AlertCircle,
  }
}

function getSecurityCheckFallbackMessage(status: SecurityCheckStatus): string {
  switch (status) {
    case 'pass':
      return '该安全检查已通过。'
    case 'block':
      return '该安全检查需要修复，公网访问前应先处理相关配置。'
    case 'warning':
      return '该安全检查需要确认，请检查相关配置。'
  }
}

function getSecurityCheckDisplayMessage(check: SecurityCheckItem): string {
  switch (check.id) {
    case 'auth_enabled':
      return check.status === 'pass'
        ? '管理界面已启用登录认证。'
        : '管理界面未启用登录认证，公网访问前必须启用认证。'
    case 'unsafe_no_auth_override':
      if (check.status === 'pass') {
        return '未启用无认证公网暴露例外。'
      }
      return check.status === 'block'
        ? '当前允许无认证服务暴露到非本机地址，公网访问前必须关闭该例外或重新启用认证。'
        : '无认证例外已开启；该设置只适合受控网络边界或临时调试，公网访问前请关闭该例外。'
    case 'https_request':
      return check.status === 'pass'
        ? '当前访问已通过 HTTPS 或受信代理转发。'
        : '公网访问前应通过内置 TLS 或受信反向代理提供 HTTPS。'
    case 'public_http_exposure':
      return check.status === 'pass'
        ? '未检测到公网 HTTP 直连风险。'
        : '检测到 HTTP 直连暴露风险，请仅向反向代理开放公网入口。'
    case 'trusted_proxy_or_tls':
      return check.status === 'pass'
        ? 'HTTPS 或受信代理配置已满足公网访问要求。'
        : '如通过反向代理发布公网，请配置实际代理层数或启用 TLS。'
    case 'forwarded_proto_trust': {
      if (check.status === 'pass') {
        return '转发协议头来自受信代理来源。'
      }
      if (securityCheckHasTrustedNonHTTPSForwardedProto(check)) {
        return '受信代理已转发协议头，但当前值不是 https，请检查反向代理的 X-Forwarded-Proto 配置。'
      }
      return '请求携带转发协议头，但来源未被明确标记为受信代理。'
    }
    case 'server_listen':
      return check.status === 'pass'
        ? 'Web 服务仅监听本机地址。'
        : '建议仅监听本机地址，并由受信反向代理对外提供 HTTPS。'
    case 'admin_accounts':
      if (check.status === 'pass') {
        return '管理员账号配置满足基本可用性要求。'
      }
      if (check.status === 'block') {
        return '当前没有可用管理员账号；需要恢复或创建至少一个启用中的管理员。'
      }
      return '建议至少保留两个启用中的管理员账号，避免主账号失效后无法管理。'
    case 'session_token_ttl':
      if (check.status === 'pass') {
        return 'Web UI 访问令牌和刷新令牌有效期处于公网部署建议范围内。'
      }
      if (check.status === 'block') {
        return 'Web UI 会话有效期配置无效，访问令牌和刷新令牌必须为正值。'
      }
      return 'Web UI 会话有效期偏长，公网访问前建议缩短配置以降低会话泄露后的风险窗口。'
    case 'login_rate_limit':
      if (check.status === 'pass') {
        return '连续失败登录会按用户名和客户端 IP 触发短期锁定，并产生登录限速提醒事件。'
      }
      if (check.details?.auth_enabled === false) {
        return 'Web 登录认证未启用，登录失败限速暂不可用。'
      }
      return '登录失败限速策略不可用；公网访问前应先修复登录防护。'
    case 'browser_session_boundary':
      if (check.status === 'pass') {
        return '当前访问会为 Web UI 会话和下载 cookie 设置 Secure 标记，浏览器写请求也会经过同源元数据校验。'
      }
      if (check.details?.auth_enabled === false) {
        return 'Web 登录认证未启用，浏览器会话 cookie 边界暂不可检查。'
      }
      return '当前访问未被识别为 HTTPS，Web UI 会话和下载 cookie 不会带 Secure 标记；公网访问前请修正 TLS 或受信代理配置。'
    case 'public_share_boundary':
      if (check.status === 'pass') {
        return check.details?.share_enabled === false
          ? '公开分享未启用，不会下发公开分享访问 cookie。'
          : '受密码保护的公开分享使用 HttpOnly、SameSite=Strict、Secure 访问 cookie，并设置私有缓存边界。'
      }
      if (check.status === 'block') {
        return '公开分享访问 cookie、失败限速或缓存边界未满足公网安全要求。'
      }
      return '分享功能已启用，但当前访问未被识别为 HTTPS；受密码保护的公开分享访问 cookie 不会带 Secure 标记。'
    case 'config_file_access':
      if (check.status === 'pass') {
        return '配置文件是普通私有文件，权限满足公网部署要求。'
      }
      if (check.status === 'block') {
        if (check.details?.path_kind === 'symlink_component') {
          return '配置文件路径经过符号链接组件；请改为服务账号可读取的普通私有路径。'
        }
        if (check.details?.path_kind === 'symlink') {
          return '配置文件是符号链接；请恢复为服务账号可读取的普通私有文件。'
        }
        if (check.details?.path_kind === 'not_regular') {
          return '配置文件路径存在但不是普通文件；请恢复为服务账号可读取的普通私有文件。'
        }
        return '配置文件路径存在阻断风险；公网访问前应恢复为普通私有文件。'
      }
      if (check.details?.path_kind === 'missing') {
        return '当前配置文件路径不存在；设置保存可能失败，公网部署前应持久化为私有普通文件。'
      }
      if (check.details?.group_or_other_access === true) {
        return '配置文件允许组或其他用户访问；建议将 config.toml 设为 0600。'
      }
      return '配置文件路径或权限需要人工确认；公网部署前应确认 config.toml 是私有普通文件。'
    case 'secrets_file_access':
      if (check.status === 'pass') {
        return check.details?.generated_webdav_password_required === false
          ? '当前 WebDAV 不依赖 secrets.json 中的自动生成 Basic 密码。'
          : '自动 WebDAV 凭据文件是普通私有文件，权限满足公网部署要求。'
      }
      if (check.status === 'block') {
        if (check.details?.path_kind === 'missing') {
          return 'WebDAV 使用自动生成 Basic 密码，但 secrets.json 不存在；请先生成凭据、设置自定义强密码，或改用 MnemoNAS 用户认证。'
        }
        if (check.details?.path_kind === 'symlink_component') {
          return '自动 WebDAV 凭据路径经过符号链接组件；请改为服务账号可读取的普通私有路径。'
        }
        if (check.details?.path_kind === 'symlink') {
          return '自动 WebDAV 凭据文件是符号链接；请恢复为服务账号可读取的普通私有文件。'
        }
        if (check.details?.path_kind === 'not_regular') {
          return '自动 WebDAV 凭据路径存在但不是普通文件；请恢复为服务账号可读取的普通私有文件。'
        }
        return '自动 WebDAV 凭据文件存在阻断风险；公网访问前应恢复为普通私有文件。'
      }
      if (check.details?.group_or_other_access === true) {
        return '自动 WebDAV 凭据文件允许组或其他用户访问；建议将 secrets.json 设为 0600。'
      }
      return '自动 WebDAV 凭据文件路径或权限需要人工确认；公网部署前应确认 secrets.json 是私有普通文件。'
    case 'users_file_access':
      if (check.status === 'pass') {
        return '用户文件是普通私有文件，且目录权限满足公网部署要求。'
      }
      if (check.status === 'block') {
        if (check.details?.dir_kind === 'symlink_component') {
          return '用户文件路径经过符号链接组件；请改为服务账号可读取的普通私有目录路径。'
        }
        return '用户文件缺失、不是普通文件或使用了符号链接；请恢复为服务账号可读取的普通私有文件。'
      }
      return '用户文件或其目录允许组或其他用户访问；建议将目录设为 0700、用户文件设为 0600。'
    case 'dataplane_listen':
      return check.status === 'pass'
        ? '数据面 gRPC 仅监听本机地址。'
        : '数据面 gRPC 不应暴露到外网，请绑定到 127.0.0.1 或 ::1。'
    case 'dataplane_http_listen':
      return check.status === 'pass'
        ? '数据面 HTTP 健康接口仅监听本机地址。'
        : '数据面 HTTP 健康接口不应暴露到外网，请绑定到本机地址。'
    case 'webdav_auth':
      if (securityCheckHasUnavailableGeneratedWebDAVPassword(check)) {
        return 'WebDAV 使用自动生成 Basic Auth，但运行态没有可用密码；请检查 secrets.json，设置自定义强密码，或改用 MnemoNAS 用户认证。'
      }
      if (securityCheckHasWebDAVPasswordRisk(check)) {
        if (securityCheckUsesGeneratedWebDAVPassword(check)) {
          return '当前自动生成的 WebDAV Basic Auth 密码偏弱，公网访问前应设置自定义强密码或改用 MnemoNAS 用户认证。'
        }
        return 'WebDAV Basic Auth 使用弱密码或示例密码，公网访问前应更换为自动生成密码、自定义强密码，或改用 MnemoNAS 用户认证。'
      }
      return check.status === 'pass'
        ? 'WebDAV 暴露面已启用合适的认证方式，或当前未启用 WebDAV。'
        : 'WebDAV 对外访问前必须启用 Basic 认证、MnemoNAS 用户认证或关闭 WebDAV。'
    case 'webdav_prefix':
      if (check.status === 'pass') {
        return 'WebDAV 挂载入口使用独立路径，不会覆盖 Web UI、API、分享或健康检查路由。'
      }
      if (check.details?.prefix_risk === 'invalid_characters') {
        return 'WebDAV 前缀格式无效；只能使用 URL 路径，不能包含反斜杠、查询参数、片段或控制字符。'
      }
      if (check.details?.prefix_risk === 'reserved_route') {
        return 'WebDAV 前缀占用了 /api、/s 或 /health 保留路由；请改为 /dav 或其他独立路径。'
      }
      return 'WebDAV 前缀会覆盖站点根路径或缺少明确挂载点；请改为 /dav 或其他独立路径。'
    case 'smb_preview':
      return check.status === 'pass'
        ? 'SMB 预览未启用，当前构建不会启动额外的 SMB/Samba 监听器。'
        : '当前构建未包含可挂载的 SMB/Samba 运行组件；启用前应先收紧监听范围和防火墙。'
    case 'share_base_url':
      if (
        check.status === 'block'
        && typeof check.details?.base_url_path === 'string'
        && pathHasBackslashes(check.details.base_url_path)
      ) {
        return '分享基础 URL 路径包含反斜杠，继续使用可能被代理或浏览器规范化为不一致的分享地址。'
      }
      if (
        check.status === 'block'
        && typeof check.details?.base_url_path === 'string'
        && pathHasDuplicateSlashes(check.details.base_url_path)
      ) {
        return '分享基础 URL 路径包含重复斜杠，继续使用可能被代理或浏览器规范化为不一致的分享地址。'
      }
      if (
        check.status === 'block'
        && typeof check.details?.base_url_path === 'string'
        && pathHasDotSegments(check.details.base_url_path)
      ) {
        return '分享基础 URL 路径包含 . 或 .. 路径段，继续使用可能被代理或浏览器规范化为不一致的分享地址。'
      }
      if (check.status === 'block' && securityCheckShareBaseURLHasNonDefaultHTTPSPort(check)) {
        return '分享基础 URL 使用非标准 HTTPS 端口，公网分享通常应使用默认 443 端口，避免额外暴露入口。'
      }
      if (
        check.status === 'warning'
        && typeof check.details?.base_url_path === 'string'
        && pathEndsWithShareRoute(check.details.base_url_path)
      ) {
        return '分享基础 URL 已包含 /s 分享路由，继续使用会生成重复的 /s/s 分享链接。'
      }
      if (
        check.status === 'warning'
        && typeof check.details?.base_url_host === 'string'
        && typeof check.details?.request_host === 'string'
        && check.details.base_url_host !== check.details.request_host
      ) {
        return '分享基础 URL 与当前访问域名不同，请确认分享域名同样具备 HTTPS、认证和防火墙保护。'
      }
      return check.status === 'pass'
        ? '公开分享链接使用 HTTPS 基础地址，或分享功能未启用。'
        : '公开分享链接应使用 HTTPS 默认端口且不包含用户信息，避免在公网中暴露不安全链接。'
    case 'share_default_policy':
      if (check.status === 'pass') {
        return '新分享默认有效期和默认访问次数处于公网部署建议范围内，或分享功能未启用。'
      }
      if (check.status === 'block') {
        return '分享默认有效期或默认访问次数配置无效，请修复负值后重新检查。'
      }
      if (check.details?.default_expires_in_unlimited === true
        && check.details?.default_max_access_unlimited === true) {
        return '分享功能已启用，但新分享默认不会过期且访问次数不限制；家庭公网分享建议同时设置默认有效期和默认访问次数。'
      }
      if (check.details?.default_expires_in_unlimited === true) {
        return '分享功能已启用，但新分享默认不会过期；家庭公网分享建议设置默认有效期，避免长期公开链接被遗忘。'
      }
      if (check.details?.default_max_access_unlimited === true) {
        return '分享功能已启用，但新分享默认访问次数不限制；家庭公网分享建议设置默认访问次数，避免公开链接被反复访问。'
      }
      return '新分享默认有效期偏长；家庭公网分享建议缩短默认有效期，降低链接长期暴露风险。'
    case 'backup_local_destinations':
      if (check.status === 'pass') {
        return '启用中的本地备份目标不在主存储或来源目录内，且当前目录状态可用。'
      }
      if (check.status === 'block') {
        if (check.details?.destination_kind === 'inside_storage_root') {
          return '本地备份目标位于 storage.root 内部；请改用独立磁盘、独立数据集或远端备份目标。'
        }
        if (check.details?.destination_kind === 'inside_source') {
          return '本地备份目标位于备份来源目录内；请改用独立磁盘、独立数据集或远端备份目标。'
        }
        if (check.details?.destination_kind === 'symlink_component') {
          return '本地备份目标路径经过符号链接组件；请改为普通目录、独立数据集或远端备份目标。'
        }
        if (check.details?.destination_kind === 'symlink') {
          return '本地备份目标是符号链接；请改为普通目录、独立数据集或远端备份目标。'
        }
        if (check.details?.destination_kind === 'not_directory') {
          return '本地备份目标当前不是目录；请恢复为普通目录、独立数据集或远端备份目标。'
        }
        return '本地备份目标配置存在阻断风险；备份运行前应先修复目标路径。'
      }
      if (check.details?.destination_kind === 'missing') {
        return '本地备份目标目录当前不存在；首次备份可能会创建目录，但长期备份目标建议提前挂载并确认可写。'
      }
      if (check.details?.destination_kind === 'not_writable') {
        return '本地备份目标目录没有写权限位；请确认 MnemoNAS 服务账号可以写入该目录。'
      }
      return '本地备份目标状态需要人工确认；请检查目标目录是否已挂载并可写。'
    case 'initial_password_file':
      if (check.status === 'pass') {
        return '初始管理员密码文件状态正常。'
      }
      if (check.details?.path_kind === 'symlink') {
        return '初始管理员密码路径是符号链接；公网访问前请删除该路径，避免初始凭据指向不受控位置。'
      }
      if (check.details?.path_kind === 'symlink_component') {
        return '初始管理员密码路径经过符号链接组件；公网访问前请改为普通私有目录并确认该文件不存在。'
      }
      if (check.details?.path_kind === 'not_regular') {
        return '初始管理员密码路径存在但不是普通文件；公网访问前请删除该路径。'
      }
      return '初始管理员密码文件需要处理，避免遗留凭据被误用。'
    default:
      return getSecurityCheckFallbackMessage(check.status)
  }
}

type SecurityCheckAction = {
  label: string
  onPress: () => void
}

function SecurityCheckRow({ check, action }: { check: SecurityCheckItem; action?: SecurityCheckAction }) {
  const meta = getSecurityStatusMeta(check.status)
  const Icon = meta.Icon

  return (
    <div className={cn("flex items-start gap-3 rounded-lg border px-3 py-3", meta.tone)}>
      <Icon size={18} className={cn("mt-0.5 shrink-0", meta.iconClassName)} />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="break-anywhere text-sm font-semibold text-foreground">{check.title}</span>
          <span className={cn("rounded-full px-2 py-0.5 text-[11px] font-semibold", meta.badgeClassName)}>
            {meta.label}
          </span>
        </div>
        <p className="break-anywhere mt-1 text-xs leading-relaxed text-default-600">{getSecurityCheckDisplayMessage(check)}</p>
      </div>
      {action && (
        <Button
          size="sm"
          variant="flat"
          className="shrink-0 rounded-lg"
          onPress={action.onPress}
        >
          {action.label}
        </Button>
      )}
    </div>
  )
}

function SecurityCheckCard({
  data,
  error,
  isLoading,
  isRefreshing,
  onRefresh,
  getAction,
}: {
  data?: SecurityCheckData
  error: unknown
  isLoading: boolean
  isRefreshing: boolean
  onRefresh: () => void
  getAction?: (check: SecurityCheckItem) => SecurityCheckAction | undefined
}) {
  const checks = data?.checks ?? []
  const issueChecks = checks.filter((check) => check.status !== 'pass')
  const visibleChecks = issueChecks.length > 0 ? issueChecks : checks.slice(0, 3)
  const counts = {
    block: checks.filter((check) => check.status === 'block').length,
    warning: checks.filter((check) => check.status === 'warning').length,
    pass: checks.filter((check) => check.status === 'pass').length,
  }
  const overallStatus = data?.status ?? (error ? 'warning' : 'pass')
  const meta = getSecurityStatusMeta(overallStatus)
  const Icon = meta.Icon

  return (
    <Card className="card-mnemonas">
      <CardHeader className="flex min-w-0 flex-col gap-4 pb-2 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 gap-4">
          <div className="gradient-mnemonas shrink-0 rounded-lg p-2.5 shadow-sm">
            <Shield size={20} className="text-white" />
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="break-anywhere text-base font-semibold text-foreground">公网访问安全自检</h3>
              {!isLoading && (
                <span className={cn("inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-semibold", meta.badgeClassName)}>
                  <Icon size={13} />
                  {meta.label}
                </span>
              )}
            </div>
            <p className="break-anywhere mt-0.5 text-xs text-default-500">
              检查当前运行态中和公网暴露直接相关的配置。
            </p>
          </div>
        </div>
        <Button
          size="sm"
          variant="bordered"
          className="btn-secondary rounded-lg"
          startContent={<RefreshCw size={14} />}
          isLoading={isRefreshing}
          onPress={onRefresh}
        >
          重新检查
        </Button>
      </CardHeader>
      <CardBody className="pt-2">
        {isLoading && !data ? (
          <div className="flex items-center gap-3 rounded-lg border border-divider bg-content2/40 px-4 py-4 text-sm text-default-500">
            <div className="h-5 w-5 rounded-full border-2 border-accent-primary border-t-transparent animate-spin" />
            正在检查安全配置...
          </div>
        ) : error && !data ? (
          <div className="flex items-start gap-3 rounded-lg border border-warning/30 bg-warning/5 px-4 py-3 text-sm text-foreground">
            <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" />
            <div>
              <div className="font-medium">安全自检暂不可用</div>
              <div className="text-default-600">
                {getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION)}
              </div>
            </div>
          </div>
        ) : (
          <div className="space-y-4">
            <div className="grid grid-cols-3 gap-2 rounded-lg border border-divider bg-content2/40 p-2">
              <div className="rounded-md px-3 py-2">
                <div className="text-xs text-default-500">需修复</div>
                <div className="text-lg font-semibold text-danger">{counts.block}</div>
              </div>
              <div className="rounded-md px-3 py-2">
                <div className="text-xs text-default-500">需确认</div>
                <div className="text-lg font-semibold text-warning">{counts.warning}</div>
              </div>
              <div className="rounded-md px-3 py-2">
                <div className="text-xs text-default-500">通过</div>
                <div className="text-lg font-semibold text-success">{counts.pass}</div>
              </div>
            </div>
            <div className="space-y-2">
              {visibleChecks.map((check) => (
                <SecurityCheckRow
                  key={check.id}
                  check={check}
                  action={check.status === 'pass' ? undefined : getAction?.(check)}
                />
              ))}
            </div>
            <p className="text-xs text-default-500">
              Web 自检只覆盖当前服务可观察到的运行态。公网域名、证书链、云防火墙和端口暴露仍需在服务器上运行 mnemonas-doctor 复核。
            </p>
          </div>
        )}
      </CardBody>
    </Card>
  )
}

function PublicAccessWizard({
  domainInput,
  normalizedDomain,
  domainError,
  proxy,
  shareEnabled,
  shareNeedsDomain,
  isApplyDisabled,
  onDomainChange,
  onProxyChange,
  onApplyRecommendation,
}: {
  domainInput: string
  normalizedDomain: string
  domainError?: string
  proxy: PublicProxyKind
  shareEnabled: boolean
  shareNeedsDomain?: boolean
  isApplyDisabled?: boolean
  onDomainChange: (value: string) => void
  onProxyChange: (value: PublicProxyKind) => void
  onApplyRecommendation: () => void
}) {
  const domainForCommand = normalizedDomain || 'nas.example.com'
  const publicBaseURL = normalizedDomain ? `https://${normalizedDomain}` : ''
  const shareBaseURLPreview = shareEnabled ? (publicBaseURL || '填写公网域名后设置') : '分享功能未启用'
  const setupCommand = `sudo mnemonas-public-setup --proxy ${proxy} ${domainForCommand} admin@example.com`
  const doctorCommand = `sudo mnemonas-doctor --public-domain ${domainForCommand}`
  const renewalCommand = proxy === 'nginx'
    ? 'sudo certbot renew --dry-run'
    : "sudo journalctl -u caddy --since '24 hours ago'"
  const renewalLabel = proxy === 'nginx' ? '续期演练' : '续期日志'
  const setupCopyLabel = '复制公网配置命令'
  const doctorCopyLabel = '复制公网自检命令'
  const renewalCopyLabel = `复制证书${renewalLabel}命令`

  return (
    <SettingsSection
      title="公网访问向导"
      description="生成反向代理配置前，先把 MnemoNAS 调整为适合公网发布的本机监听模式"
      icon={Globe}
    >
      <div className="space-y-4">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-[minmax(0,1fr)_10rem]">
          <div>
            <label className="mb-1.5 block text-sm font-medium text-default-600">公网域名</label>
            <Input
              aria-label="公网域名"
              placeholder="nas.example.com"
              value={domainInput}
              onValueChange={onDomainChange}
              isInvalid={!!domainError}
              errorMessage={domainError}
              startContent={<Globe size={16} className="text-default-500" />}
              classNames={{
                inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
              }}
            />
          </div>
          <div>
            <label className="mb-1.5 block text-sm font-medium text-default-600">反向代理</label>
            <select
              aria-label="反向代理"
              value={proxy}
              onChange={(event) => onProxyChange(event.target.value === 'nginx' ? 'nginx' : 'caddy')}
              className="input-shell h-12 w-full rounded-medium border border-transparent bg-transparent px-3 py-2 text-sm outline-none focus:border-accent-primary"
            >
              <option value="caddy">Caddy</option>
              <option value="nginx">Nginx</option>
            </select>
          </div>
        </div>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <div className="rounded-lg border border-divider bg-content2/40 px-3 py-3">
            <div className="text-xs text-default-500">Web 监听</div>
            <div className="mt-1 font-mono text-sm font-semibold text-foreground">127.0.0.1</div>
          </div>
          <div className="rounded-lg border border-divider bg-content2/40 px-3 py-3">
            <div className="text-xs text-default-500">受信代理层数</div>
            <div className="mt-1 font-mono text-sm font-semibold text-foreground">1</div>
          </div>
          <div className="rounded-lg border border-divider bg-content2/40 px-3 py-3">
            <div className="text-xs text-default-500">分享基础 URL</div>
            <div className={cn(
              "break-anywhere mt-1 font-mono text-sm font-semibold",
              shareNeedsDomain ? 'text-warning-700' : 'text-foreground',
            )}>
              {shareBaseURLPreview}
            </div>
          </div>
        </div>

        <div className="space-y-2">
          <div className="text-xs font-medium text-default-500">服务器命令</div>
          <Snippet
            symbol=""
            variant="flat"
            className="w-full"
            classNames={{
              base: "bg-content1 border border-divider",
              pre: "font-mono text-xs whitespace-pre-wrap break-all",
              copyButton: PUBLIC_ACCESS_SNIPPET_COPY_BUTTON_CLASS,
            }}
            tooltipProps={{ content: setupCopyLabel }}
            copyButtonProps={{ 'aria-label': setupCopyLabel }}
            hideSymbol
          >
            {setupCommand}
          </Snippet>
          <Snippet
            symbol=""
            variant="flat"
            className="w-full"
            classNames={{
              base: "bg-content1 border border-divider",
              pre: "font-mono text-xs whitespace-pre-wrap break-all",
              copyButton: PUBLIC_ACCESS_SNIPPET_COPY_BUTTON_CLASS,
            }}
            tooltipProps={{ content: doctorCopyLabel }}
            copyButtonProps={{ 'aria-label': doctorCopyLabel }}
            hideSymbol
          >
            {doctorCommand}
          </Snippet>
        </div>

        <div className="rounded-lg border border-warning-200 bg-warning-50 px-4 py-3 text-sm text-warning-800">
          <div className="font-medium">证书续期检查</div>
          <div className="mt-1 text-warning-700">
            {proxy === 'nginx'
              ? 'Nginx 路径依赖 certbot 定时任务，公网开放前先执行一次 dry-run。'
              : 'Caddy 会自动续期证书，公网开放后需要确认服务日志里没有 ACME 错误。'}
          </div>
          <Snippet
            symbol=""
            variant="flat"
            className="mt-3 w-full"
            classNames={{
              base: "bg-content1 border border-warning-200",
              pre: "font-mono text-xs whitespace-pre-wrap break-all",
              copyButton: PUBLIC_ACCESS_SNIPPET_COPY_BUTTON_CLASS,
            }}
            tooltipProps={{ content: renewalCopyLabel }}
            copyButtonProps={{ 'aria-label': renewalCopyLabel }}
            hideSymbol
          >
            {renewalCommand}
          </Snippet>
          <div className="mt-2 text-xs text-warning-700">
            {renewalLabel}失败时，先检查 DNS、云防火墙 80/443、ACME challenge 路径和反向代理日志，再重新运行 doctor。
          </div>
        </div>

        <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-xs leading-relaxed text-default-500">
            {shareNeedsDomain
              ? '分享功能已启用，填写公网域名后才能应用完整公网推荐。'
              : '应用推荐只会更新当前表单；点击“保存设置”后，监听地址变更需要重启服务。'}
          </p>
          <Button
            className="btn-primary rounded-lg"
            startContent={<CheckCircle2 size={16} />}
            isDisabled={isApplyDisabled}
            onPress={onApplyRecommendation}
          >
            应用推荐到表单
          </Button>
        </div>
      </div>
    </SettingsSection>
  )
}

export function SettingsPage() {
  const user = useUser()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const selectedTab = normalizeSettingsTab(searchParams.get('tab'))
  const sharePathFilter = selectedTab === 'shares' ? searchParams.get('share_path') ?? '' : ''
  const shareReviewFilter = selectedTab === 'shares' ? normalizeShareReviewFilter(searchParams.get('share_filter')) : 'all'
  const defaultSettings = {
    serverHost: '0.0.0.0',
    serverPort: '8080',
    serverReadTimeout: '30s',
    serverWriteTimeout: '60s',
    serverIdleTimeout: '120s',
    serverTrustedProxyHops: '0',
    serverTrustedProxyCIDRs: '',
    tlsEnabled: false,
    tlsCertFile: '',
    tlsKeyFile: '',
    tlsAutoGenerate: true,
    tlsCertDir: '',
    authAccessTokenTTL: '15m',
    authRefreshTokenTTL: '168h',
    storageRoot: '',
    directoryQuotas: '',
    directoryAccessRules: '',
    trashEnabled: true,
    trashRetentionDays: '30',
    trashMaxSize: '10 GB',
    maxVersions: '50',
    maxAge: '2160h',
    minFreeSpace: '10GB',
    gcInterval: '24h',
    versioningExtensions: DEFAULT_VERSIONING_EXTENSIONS,
    versioningFilenames: DEFAULT_VERSIONING_FILENAMES,
    versioningMaxSize: '100 MB',
    webdavEnabled: true,
    webdavPrefix: '/dav',
    webdavReadOnly: false,
    webdavAuthType: 'basic',
    webdavUsername: 'admin',
    webdavPassword: '',
    webdavUseGeneratedPassword: false,
    shareEnabled: false,
    shareBaseURL: '',
    shareDefaultExpiresIn: '168h',
    shareDefaultMaxAccess: '0',
    sharePolicyRules: [] as SharePolicyRuleDraft[],
    favoritesEnabled: true,
    alertsEnabled: false,
    alertsCheckInterval: '1h',
    alertsThresholdPct: '90',
    alertsCriticalPct: '95',
    alertsMinFreeSpace: '10GB',
    alertsCooldownPeriod: '4h',
    alertsWebhookURL: '',
    alertsWebhookURLConfigured: false,
    alertsWebhookMethod: 'POST',
    alertsWebhookHeaders: '',
    alertsWebhookHeadersConfigured: false,
    alertsTelegramEnabled: false,
    alertsTelegramBotToken: '',
    alertsTelegramBotTokenConfigured: false,
    alertsTelegramBotTokenClear: false,
    alertsTelegramChatID: '',
    alertsWeComEnabled: false,
    alertsWeComWebhookURL: '',
    alertsWeComWebhookURLConfigured: false,
    alertsDingTalkEnabled: false,
    alertsDingTalkWebhookURL: '',
    alertsDingTalkWebhookURLConfigured: false,
    alertsEmailEnabled: false,
    alertsSMTPHost: '',
    alertsSMTPPort: '587',
    alertsSMTPUsername: '',
    alertsSMTPPassword: '',
    alertsSMTPPasswordConfigured: false,
    alertsSMTPPasswordClear: false,
    alertsSMTPFrom: '',
    alertsSMTPTo: '',
    scrubScheduleEnabled: false,
    scrubScheduleInterval: '168h',
    scrubRetryInterval: '1h',
    scrubMaxRetries: '1',
    diskHealthEnabled: false,
    diskHealthCheckInterval: '1h',
    diskHealthProbeTimeout: '15s',
    diskHealthCooldownPeriod: '4h',
    diskHealthCommand: 'smartctl',
    diskHealthTemperatureWarningC: '50',
    diskHealthTemperatureCriticalC: '60',
    diskHealthMediaWearWarningPct: '80',
    diskHealthMediaWearCriticalPct: '100',
    diskHealthDevices: '',
    dataplaneGrpcAddress: '127.0.0.1:9090',
    dataplaneTimeout: '30s',
    dataplaneMaxRetries: '3',
    minChunkSize: '256KB',
    avgChunkSize: '1MB',
    maxChunkSize: '4MB',
  }
  type SettingsDraft = typeof defaultSettings
  type SaveSettingsVariables = {
    request: UpdateSettingsRequest
    submittedSettings: SettingsDraft
    baseSettingsUpdatedAt: number
    signal: AbortSignal
  }
  type DirectoryAccessCheckVariables = {
    request: DirectoryAccessCheckRequest
    signal: AbortSignal
  }
  type DirectoryAccessReportVariables = {
    request: DirectoryAccessReportRequest
    signal: AbortSignal
  }
  type DirectoryAccessPreviewVariables = {
    request: DirectoryAccessPreviewRequest
    signal: AbortSignal
  }
  type TestAlertVariables = {
    signal: AbortSignal
  }

  const sanitizeSavedSettingsOverride = (
    settings: SettingsDraft,
    request: UpdateSettingsRequest,
  ): SettingsDraft => {
    const next: SettingsDraft = {
      ...settings,
      webdavPassword: '',
      webdavUseGeneratedPassword: false,
      alertsTelegramBotToken: '',
      alertsTelegramBotTokenClear: false,
      alertsSMTPPassword: '',
      alertsSMTPPasswordClear: false,
    }

    if (request.alerts) {
      const webhookURL = request.alerts.webhook_url?.trim() ?? ''
      next.alertsWebhookURL = webhookURL ? REDACTED_SETTINGS_SECRET : ''
      next.alertsWebhookURLConfigured = webhookURL !== ''
      next.alertsWebhookHeaders = request.alerts.webhook_headers?.length
        ? request.alerts.webhook_headers.map(redactWebhookHeaderLine).join('\n')
        : ''
      next.alertsWebhookHeadersConfigured = (request.alerts.webhook_headers?.length ?? 0) > 0
      next.alertsTelegramBotTokenConfigured = request.alerts.telegram_bot_token === ''
        ? false
        : settings.alertsTelegramBotTokenConfigured || (request.alerts.telegram_bot_token?.trim() ?? '') !== ''
      const weComWebhookURL = request.alerts.wecom_webhook_url?.trim() ?? ''
      next.alertsWeComWebhookURL = weComWebhookURL ? REDACTED_SETTINGS_SECRET : ''
      next.alertsWeComWebhookURLConfigured = weComWebhookURL !== ''
      const dingTalkWebhookURL = request.alerts.dingtalk_webhook_url?.trim() ?? ''
      next.alertsDingTalkWebhookURL = dingTalkWebhookURL ? REDACTED_SETTINGS_SECRET : ''
      next.alertsDingTalkWebhookURLConfigured = dingTalkWebhookURL !== ''
      next.alertsSMTPPasswordConfigured = request.alerts.smtp_password === ''
        ? false
        : settings.alertsSMTPPasswordConfigured || (request.alerts.smtp_password?.trim() ?? '') !== ''
    }

    return next
  }

  const sanitizeDirtyDraftAfterSave = (
    current: SettingsDraft,
    submitted: SettingsDraft,
    sanitizedSubmitted: SettingsDraft,
  ): SettingsDraft => {
    const next = { ...current }
    if (current.webdavPassword === submitted.webdavPassword) {
      next.webdavPassword = sanitizedSubmitted.webdavPassword
    }
    if (current.webdavUseGeneratedPassword === submitted.webdavUseGeneratedPassword) {
      next.webdavUseGeneratedPassword = sanitizedSubmitted.webdavUseGeneratedPassword
    }
    if (current.alertsWebhookURL === submitted.alertsWebhookURL) {
      next.alertsWebhookURL = sanitizedSubmitted.alertsWebhookURL
      next.alertsWebhookURLConfigured = sanitizedSubmitted.alertsWebhookURLConfigured
    }
    if (current.alertsWebhookHeaders === submitted.alertsWebhookHeaders) {
      next.alertsWebhookHeaders = sanitizedSubmitted.alertsWebhookHeaders
      next.alertsWebhookHeadersConfigured = sanitizedSubmitted.alertsWebhookHeadersConfigured
    }
    if (current.alertsTelegramBotToken === submitted.alertsTelegramBotToken) {
      next.alertsTelegramBotToken = sanitizedSubmitted.alertsTelegramBotToken
      next.alertsTelegramBotTokenConfigured = sanitizedSubmitted.alertsTelegramBotTokenConfigured
    }
    if (current.alertsTelegramBotTokenClear === submitted.alertsTelegramBotTokenClear) {
      next.alertsTelegramBotTokenClear = sanitizedSubmitted.alertsTelegramBotTokenClear
    }
    if (current.alertsWeComWebhookURL === submitted.alertsWeComWebhookURL) {
      next.alertsWeComWebhookURL = sanitizedSubmitted.alertsWeComWebhookURL
      next.alertsWeComWebhookURLConfigured = sanitizedSubmitted.alertsWeComWebhookURLConfigured
    }
    if (current.alertsDingTalkWebhookURL === submitted.alertsDingTalkWebhookURL) {
      next.alertsDingTalkWebhookURL = sanitizedSubmitted.alertsDingTalkWebhookURL
      next.alertsDingTalkWebhookURLConfigured = sanitizedSubmitted.alertsDingTalkWebhookURLConfigured
    }
    if (current.alertsSMTPPassword === submitted.alertsSMTPPassword) {
      next.alertsSMTPPassword = sanitizedSubmitted.alertsSMTPPassword
      next.alertsSMTPPasswordConfigured = sanitizedSubmitted.alertsSMTPPasswordConfigured
    }
    if (current.alertsSMTPPasswordClear === submitted.alertsSMTPPasswordClear) {
      next.alertsSMTPPasswordClear = sanitizedSubmitted.alertsSMTPPasswordClear
    }
    return next
  }

  const hasSavedAlertNotificationChannel = (settings: SettingsDraft): boolean => {
    if (settings.alertsWebhookURLConfigured || settings.alertsWebhookURL.trim() !== '') {
      return true
    }
    if (
      settings.alertsTelegramEnabled
      && settings.alertsTelegramChatID.trim() !== ''
      && (settings.alertsTelegramBotTokenConfigured || settings.alertsTelegramBotToken.trim() !== '')
    ) {
      return true
    }
    if (
      settings.alertsWeComEnabled
      && (settings.alertsWeComWebhookURLConfigured || settings.alertsWeComWebhookURL.trim() !== '')
    ) {
      return true
    }
    if (
      settings.alertsDingTalkEnabled
      && (settings.alertsDingTalkWebhookURLConfigured || settings.alertsDingTalkWebhookURL.trim() !== '')
    ) {
      return true
    }
    const smtpPort = Number(settings.alertsSMTPPort.trim())
    const hasSMTPRecipient = settings.alertsSMTPTo
      .split(/[\n,]+/)
      .some(recipient => recipient.trim() !== '')
    return settings.alertsEmailEnabled
      && settings.alertsSMTPHost.trim() !== ''
      && Number.isInteger(smtpPort)
      && smtpPort > 0
      && smtpPort <= 65535
      && settings.alertsSMTPFrom.trim() !== ''
      && hasSMTPRecipient
  }

  const getTestAlertReadinessWarning = (settings: SettingsDraft): { title: string; description: string } | null => {
    if (!settings.alertsEnabled) {
      return {
        title: '提醒尚未启用',
        description: '测试提醒会使用服务端已保存配置；请先启用提醒并保存。',
      }
    }
    if (!hasSavedAlertNotificationChannel(settings)) {
      return {
        title: '没有可用提醒通道',
        description: '请至少配置 Webhook、Telegram、企业微信、钉钉或邮件通道并保存后再发送测试提醒。',
      }
    }
    return null
  }

  // WebDAV credentials state
  const [showWebDAVPassword, setShowWebDAVPassword] = useState(false)
  const [copiedField, setCopiedField] = useState<string | null>(null)
  const [publicAccessDomain, setPublicAccessDomain] = useState('')
  const [publicAccessProxy, setPublicAccessProxy] = useState<PublicProxyKind>('caddy')

  // Fetch settings from API
  const { data: settingsData, dataUpdatedAt: settingsDataUpdatedAt, isLoading, error, refetch, isRefetching } = useQuery({
    queryKey: ['settings', user?.id ?? 'anonymous'],
    queryFn: ({ signal }) => getSettings({ signal }),
  })
  const settingsLoadErrorPresentation = error ? getSettingsLoadErrorPresentation(error) : null

  const {
    data: securityCheckResponse,
    error: securityCheckError,
    refetch: refetchSecurityCheck,
    isLoading: isLoadingSecurityCheck,
    isRefetching: isRefetchingSecurityCheck,
  } = useQuery({
    queryKey: ['security-check', user?.id ?? 'anonymous'],
    queryFn: ({ signal }) => getSecurityCheck({ signal }),
    enabled: selectedTab === 'general',
  })

  // Fetch WebDAV credentials
  const {
    data: webdavCredentials,
    error: webdavCredentialsError,
    refetch: refetchWebDAVCredentials,
    isRefetching: isRefetchingWebDAVCredentials,
  } = useQuery({
    queryKey: ['webdav-credentials', user?.id ?? 'anonymous'],
    queryFn: ({ signal }) => getWebDAVCredentials({ signal }),
    enabled: selectedTab === 'webdav', // Only fetch when WebDAV tab is selected
  })
  const webdavCredentialsErrorPresentation = webdavCredentialsError
    ? getWebDAVCredentialsErrorPresentation(webdavCredentialsError)
    : null
  const webdavRuntimeUnavailable = settingsData?.data.webdav.enabled === true
    && settingsData.data.webdav.runtime_enabled === false
  const favoritesRuntimeUnavailable = settingsData?.data.favorites?.enabled === true
    && settingsData.data.favorites?.runtime_available === false
  const webdavUrl = useMemo(() => {
    return formatWebDAVUrl(window.location.origin, webdavCredentials?.url ?? '')
  }, [webdavCredentials?.url])

  const [accessCheckUsername, setAccessCheckUsername] = useState('')
  const [accessCheckPath, setAccessCheckPath] = useState('/')
  const directoryAccessReviewHistoryStorageKey = useMemo(
    () => getDirectoryAccessReviewHistoryStorageKey(user?.id),
    [user?.id],
  )
  const {
    data: directoryAccessReviewRecordsData,
    refetch: refetchDirectoryAccessReviewRecords,
  } = useQuery({
    queryKey: ['directory-access-review-records', user?.id ?? 'anonymous'],
    queryFn: ({ signal }) => listDirectoryAccessReviewRecords({
      limit: DIRECTORY_ACCESS_REVIEW_HISTORY_LIMIT,
      signal,
    }),
    enabled: selectedTab === 'retention',
    retry: false,
  })
  const serverDirectoryAccessReviewHistory = useMemo(() => (
    directoryAccessReviewRecordsData?.items.map(directoryAccessReviewRecordToHistoryEntry) ?? []
  ), [directoryAccessReviewRecordsData?.items])
  const [directoryAccessReviewHistory, setDirectoryAccessReviewHistory] = useState<DirectoryAccessReviewHistoryEntry[]>(() => (
    loadDirectoryAccessReviewHistory(getDirectoryAccessReviewHistoryStorageKey(user?.id))
  ))
  const directoryAccessReviewHistoryRef = useRef<DirectoryAccessReviewHistoryEntry[]>(directoryAccessReviewHistory)
  const saveSettingsAbortControllerRef = useRef<AbortController | null>(null)
  const accessCheckAbortControllerRef = useRef<AbortController | null>(null)
  const accessReportAbortControllerRef = useRef<AbortController | null>(null)
  const accessPreviewAbortControllerRef = useRef<AbortController | null>(null)
  const testAlertAbortControllerRef = useRef<AbortController | null>(null)
  useEffect(() => {
    const entries = mergeDirectoryAccessReviewHistory(
      serverDirectoryAccessReviewHistory,
      loadDirectoryAccessReviewHistory(directoryAccessReviewHistoryStorageKey),
    )
    directoryAccessReviewHistoryRef.current = entries
    let cancelled = false
    queueMicrotask(() => {
      if (!cancelled) {
        setDirectoryAccessReviewHistory(entries)
      }
    })
    return () => {
      cancelled = true
    }
  }, [directoryAccessReviewHistoryStorageKey, serverDirectoryAccessReviewHistory])
  useEffect(() => {
    directoryAccessReviewHistoryRef.current = directoryAccessReviewHistory
  }, [directoryAccessReviewHistory])
  useEffect(() => {
    return () => {
      saveSettingsAbortControllerRef.current?.abort()
      accessCheckAbortControllerRef.current?.abort()
      accessReportAbortControllerRef.current?.abort()
      accessPreviewAbortControllerRef.current?.abort()
      testAlertAbortControllerRef.current?.abort()
      saveSettingsAbortControllerRef.current = null
      accessCheckAbortControllerRef.current = null
      accessReportAbortControllerRef.current = null
      accessPreviewAbortControllerRef.current = null
      testAlertAbortControllerRef.current = null
    }
  }, [user?.id])
  const accessCheckMutation = useMutation({
    mutationFn: ({ request, signal }: DirectoryAccessCheckVariables) => checkDirectoryAccess(request, { signal }),
    onError: (err) => {
      if (isAbortError(err)) {
        return
      }
      addToast(getSettingsActionErrorToast(err, {
        unavailable: '权限检查不可用',
        failure: '权限检查失败',
      }))
    },
    onSettled: (_data, _error, variables) => {
      if (accessCheckAbortControllerRef.current?.signal === variables.signal) {
        accessCheckAbortControllerRef.current = null
      }
    },
  })
  const accessReportMutation = useMutation({
    mutationFn: ({ request, signal }: DirectoryAccessReportVariables) => reportDirectoryAccess(request, { signal }),
    onError: (err) => {
      if (isAbortError(err)) {
        return
      }
      addToast(getSettingsActionErrorToast(err, {
        unavailable: '权限矩阵不可用',
        failure: '权限矩阵生成失败',
      }))
    },
    onSettled: (_data, _error, variables) => {
      if (accessReportAbortControllerRef.current?.signal === variables.signal) {
        accessReportAbortControllerRef.current = null
      }
    },
  })
  const accessPreviewMutation = useMutation({
    mutationFn: ({ request, signal }: DirectoryAccessPreviewVariables) => previewDirectoryAccess(request, { signal }),
    onError: (err) => {
      if (isAbortError(err)) {
        return
      }
      addToast(getSettingsActionErrorToast(err, {
        unavailable: '权限预览不可用',
        failure: '权限预览失败',
      }))
    },
    onSettled: (_data, _error, variables) => {
      if (accessPreviewAbortControllerRef.current?.signal === variables.signal) {
        accessPreviewAbortControllerRef.current = null
      }
    },
  })
  const testAlertMutation = useMutation({
    mutationFn: ({ signal }: TestAlertVariables) => sendTestAlert({ signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }
      const channels = formatAlertChannelSummary(result.data.channels)
      addToast({
        title: result.warning === true ? '测试提醒已发送，但存在警告' : '测试提醒已发送',
        description: result.warning === true
          ? getNonBlankToastDescription(result.message) ?? (channels ? `已发送到 ${channels}` : undefined)
          : channels ? `已发送到 ${channels}` : getNonBlankToastDescription(result.message),
        color: result.warning === true ? 'warning' : 'success',
      })
    },
    onError: (err, variables) => {
      if (variables.signal.aborted || isAbortError(err)) {
        return
      }
      addToast(getSettingsActionErrorToast(err, {
        unavailable: '提醒服务暂不可用',
        failure: '测试提醒失败',
      }))
    },
    onSettled: (_data, _error, variables) => {
      if (testAlertAbortControllerRef.current?.signal === variables.signal) {
        testAlertAbortControllerRef.current = null
      }
    },
  })

  const handleCheckDirectoryAccess = () => {
    const username = accessCheckUsername.trim()
    const targetPath = accessCheckPath.trim()
    if (!username || !targetPath) {
      addToast({
        title: '权限检查信息不完整',
        description: '请输入用户名和路径。',
        color: 'warning',
      })
      return
    }
    const normalizedTargetPath = normalizeDirectoryQuotaPathInput(targetPath)
    if (!normalizedTargetPath) {
      addToast({
        title: '权限检查路径无效',
        description: logicalPathInputErrorDescription,
        color: 'warning',
      })
      return
    }
    accessCheckAbortControllerRef.current?.abort()
    const controller = new AbortController()
    accessCheckAbortControllerRef.current = controller
    accessCheckMutation.mutate({
      request: { username, path: normalizedTargetPath },
      signal: controller.signal,
    })
  }

  const handleReportDirectoryAccess = () => {
    const targetPath = accessCheckPath.trim()
    if (!targetPath) {
      addToast({
        title: '权限矩阵路径为空',
        description: '请输入需要检查的路径。',
        color: 'warning',
      })
      return
    }
    const normalizedTargetPath = normalizeDirectoryQuotaPathInput(targetPath)
    if (!normalizedTargetPath) {
      addToast({
        title: '权限矩阵路径无效',
        description: logicalPathInputErrorDescription,
        color: 'warning',
      })
      return
    }
    accessReportAbortControllerRef.current?.abort()
    const controller = new AbortController()
    accessReportAbortControllerRef.current = controller
    accessReportMutation.mutate({
      request: { path: normalizedTargetPath },
      signal: controller.signal,
    })
  }

  const handlePreviewDirectoryAccess = () => {
    const targetPath = accessCheckPath.trim()
    if (!targetPath) {
      addToast({
        title: '权限预览路径为空',
        description: '请输入需要预览的路径。',
        color: 'warning',
      })
      return
    }
    const normalizedTargetPath = normalizeDirectoryQuotaPathInput(targetPath)
    if (!normalizedTargetPath) {
      addToast({
        title: '权限预览路径无效',
        description: logicalPathInputErrorDescription,
        color: 'warning',
      })
      return
    }
    const parsedRules = parseDirectoryAccessRuleLines(settings.directoryAccessRules)
    if (parsedRules.error) {
      addToast({
        title: '目录权限格式无效',
        description: parsedRules.error,
        color: 'danger',
      })
      return
    }
    accessPreviewAbortControllerRef.current?.abort()
    const controller = new AbortController()
    accessPreviewAbortControllerRef.current = controller
    accessPreviewMutation.mutate({
      request: {
        path: normalizedTargetPath,
        directory_access_rules: parsedRules.rules,
      },
      signal: controller.signal,
    })
  }

  const handleSendTestAlert = () => {
    if (isDirty) {
      addToast({
        title: '需要先保存设置',
        description: '测试提醒会使用服务端已保存的提醒配置。',
        color: 'warning',
      })
      return
    }
    const readinessWarning = getTestAlertReadinessWarning(settings)
    if (readinessWarning) {
      addToast({
        ...readinessWarning,
        color: 'warning',
      })
      return
    }
    if (saveMutation.isPending || testAlertMutation.isPending) {
      return
    }
    testAlertAbortControllerRef.current?.abort()
    const controller = new AbortController()
    testAlertAbortControllerRef.current = controller
    testAlertMutation.mutate({ signal: controller.signal })
  }

  const handleCopy = async (field: string, value: string) => {
    try {
      await copyTextToClipboard(value)
      setCopiedField(field)
      setTimeout(() => setCopiedField(null), 2000)
    } catch {
      addToast({ title: '复制失败', color: 'danger' })
    }
  }

  const handleSaveDirectoryAccessReviewHistory = useCallback(async (report: DirectoryAccessReportData, title: string, reportText: string): Promise<DirectoryAccessReviewSaveResult> => {
    const entry = createDirectoryAccessReviewHistoryEntry(report, title, reportText)
    const nextEntries = [
      entry,
      ...directoryAccessReviewHistoryRef.current.filter((current) => (
        current.path !== entry.path || current.title !== entry.title || current.preview !== entry.preview
      )),
    ].slice(0, DIRECTORY_ACCESS_REVIEW_HISTORY_LIMIT)

    if (!saveDirectoryAccessReviewHistory(directoryAccessReviewHistoryStorageKey, nextEntries)) {
      return 'failed'
    }
    directoryAccessReviewHistoryRef.current = nextEntries
    setDirectoryAccessReviewHistory(nextEntries)

    try {
      const record = await createDirectoryAccessReviewRecord(
        createDirectoryAccessReviewRecordRequest(report, title, reportText),
      )
      const merged = mergeDirectoryAccessReviewHistory(
        [directoryAccessReviewRecordToHistoryEntry(record)],
        nextEntries,
      )
      saveDirectoryAccessReviewHistory(directoryAccessReviewHistoryStorageKey, merged)
      directoryAccessReviewHistoryRef.current = merged
      setDirectoryAccessReviewHistory(merged)
      void refetchDirectoryAccessReviewRecords()
      return 'saved'
    } catch {
      return 'local'
    }
  }, [directoryAccessReviewHistoryStorageKey, refetchDirectoryAccessReviewRecords])

  const handleCopyDirectoryAccessReviewHistory = useCallback(async (entry: DirectoryAccessReviewHistoryEntry) => {
    try {
      await copyTextToClipboard(entry.reportText)
      addToast({ title: '目录权限历史记录已复制', color: 'success' })
    } catch {
      addToast({
        title: '复制目录权限历史记录失败',
        description: '请检查浏览器剪贴板权限。',
        color: 'danger',
      })
    }
  }, [])

  const handleClearDirectoryAccessReviewHistory = useCallback(async () => {
    try {
      window.localStorage.removeItem(directoryAccessReviewHistoryStorageKey)
    } catch {
      addToast({
        title: '清空目录权限历史失败',
        description: '请检查浏览器本地存储权限。',
        color: 'danger',
      })
      return
    }
    try {
      await clearDirectoryAccessReviewRecords()
      directoryAccessReviewHistoryRef.current = []
      setDirectoryAccessReviewHistory([])
      void refetchDirectoryAccessReviewRecords()
      addToast({ title: '目录权限近期复核历史已清空', color: 'success' })
    } catch {
      if (serverDirectoryAccessReviewHistory.length > 0) {
        directoryAccessReviewHistoryRef.current = serverDirectoryAccessReviewHistory
        setDirectoryAccessReviewHistory(serverDirectoryAccessReviewHistory)
        addToast({
          title: '本机目录权限历史已清空',
          description: '服务端历史清空失败，仍保留已持久化记录。',
          color: 'warning',
        })
        return
      }
      directoryAccessReviewHistoryRef.current = []
      setDirectoryAccessReviewHistory([])
      addToast({ title: '目录权限近期复核历史已清空', color: 'success' })
    }
  }, [directoryAccessReviewHistoryStorageKey, refetchDirectoryAccessReviewRecords, serverDirectoryAccessReviewHistory])

  const [draftSettings, setDraftSettings] = useState(defaultSettings)
  const [isDirty, setIsDirty] = useState(false)
  const [savedSettingsOverride, setSavedSettingsOverride] = useState<typeof defaultSettings | null>(null)
  const [savedSettingsOverrideUpdatedAt, setSavedSettingsOverrideUpdatedAt] = useState<number | null>(null)
  const draftSettingsRef = useRef(draftSettings)

  useLayoutEffect(() => {
    draftSettingsRef.current = draftSettings
  }, [draftSettings])

  const handleTabSelectionChange = useCallback((key: React.Key) => {
    const nextTab = normalizeSettingsTab(String(key))

    if (nextTab === 'general') {
      setSearchParams({})
      return
    }

    setSearchParams({ tab: nextTab })
  }, [setSearchParams])

  const handleClearSharePathFilter = useCallback(() => {
    const nextParams = new URLSearchParams(searchParams)
    nextParams.set('tab', 'shares')
    nextParams.delete('share_path')
    setSearchParams(nextParams)
  }, [searchParams, setSearchParams])

  const mapServerSettings = useCallback((data: NonNullable<typeof settingsData>['data']) => {
    return {
      serverHost: data.server.host,
      serverPort: String(data.server.port),
      serverReadTimeout: data.server.read_timeout,
      serverWriteTimeout: data.server.write_timeout,
      serverIdleTimeout: data.server.idle_timeout,
      serverTrustedProxyHops: String(data.server.trusted_proxy_hops ?? 0),
      serverTrustedProxyCIDRs: (data.server.trusted_proxy_cidrs ?? []).join('\n'),
      tlsEnabled: data.server.tls?.enabled ?? false,
      tlsCertFile: data.server.tls?.cert_file ?? '',
      tlsKeyFile: data.server.tls?.key_file ?? '',
      tlsAutoGenerate: data.server.tls?.auto_generate ?? true,
      tlsCertDir: data.server.tls?.cert_dir ?? '',
      authAccessTokenTTL: data.auth?.access_token_ttl ?? '15m',
      authRefreshTokenTTL: data.auth?.refresh_token_ttl ?? '168h',
      storageRoot: data.storage.root,
      directoryQuotas: formatDirectoryQuotaLines(data.storage.directory_quotas),
      directoryAccessRules: formatDirectoryAccessRuleLines(data.storage.directory_access_rules),
      trashEnabled: data.trash?.enabled ?? true,
      trashRetentionDays: String(data.trash?.retention_days ?? 30),
      trashMaxSize: formatBytes(data.trash?.max_size ?? 10737418240),
      maxVersions: String(data.retention.max_versions),
      maxAge: data.retention.max_age,
      minFreeSpace: formatBytes(data.retention.min_free_space),
      gcInterval: data.retention.gc_interval,
      versioningExtensions: data.versioning?.auto_versioned_extensions?.join('\n') ?? DEFAULT_VERSIONING_EXTENSIONS,
      versioningFilenames: data.versioning?.auto_versioned_filenames?.join('\n') ?? DEFAULT_VERSIONING_FILENAMES,
      versioningMaxSize: formatBytes(data.versioning?.max_versioned_size ?? 104857600),
      webdavEnabled: data.webdav.enabled,
      webdavPrefix: data.webdav.prefix,
      webdavReadOnly: data.webdav.read_only,
      webdavAuthType: data.webdav.auth_type,
      webdavUsername: data.webdav.username,
      webdavPassword: '',
      webdavUseGeneratedPassword: false,
      shareEnabled: data.share.enabled,
      shareBaseURL: data.share.base_url,
      shareDefaultExpiresIn: data.share.default_expires_in ?? '168h',
      shareDefaultMaxAccess: String(data.share.default_max_access ?? 0),
      sharePolicyRules: (data.share.policy_rules ?? []).map((rule) => ({ ...rule })),
      favoritesEnabled: data.favorites?.enabled ?? true,
      alertsEnabled: data.alerts?.enabled ?? false,
      alertsCheckInterval: data.alerts?.check_interval ?? '1h',
      alertsThresholdPct: String(data.alerts?.threshold_pct ?? 90),
      alertsCriticalPct: String(data.alerts?.critical_pct ?? 95),
      alertsMinFreeSpace: formatBytes(data.alerts?.min_free_bytes ?? 10737418240),
      alertsCooldownPeriod: data.alerts?.cooldown_period ?? '4h',
      alertsWebhookURL: data.alerts?.webhook_url ?? '',
      alertsWebhookURLConfigured: data.alerts?.webhook_url_configured ?? (data.alerts?.webhook_url ?? '') === REDACTED_SETTINGS_SECRET,
      alertsWebhookMethod: data.alerts?.webhook_method ?? 'POST',
      alertsWebhookHeaders: data.alerts?.webhook_headers?.join('\n') ?? '',
      alertsWebhookHeadersConfigured: data.alerts?.webhook_headers_configured ?? ((data.alerts?.webhook_headers?.length ?? 0) > 0),
      alertsTelegramEnabled: data.alerts?.telegram_enabled ?? false,
      alertsTelegramBotToken: '',
      alertsTelegramBotTokenConfigured: data.alerts?.telegram_bot_token_configured ?? false,
      alertsTelegramBotTokenClear: false,
      alertsTelegramChatID: data.alerts?.telegram_chat_id ?? '',
      alertsWeComEnabled: data.alerts?.wecom_enabled ?? false,
      alertsWeComWebhookURL: data.alerts?.wecom_webhook_url ?? '',
      alertsWeComWebhookURLConfigured: data.alerts?.wecom_webhook_url_configured ?? (data.alerts?.wecom_webhook_url ?? '') === REDACTED_SETTINGS_SECRET,
      alertsDingTalkEnabled: data.alerts?.dingtalk_enabled ?? false,
      alertsDingTalkWebhookURL: data.alerts?.dingtalk_webhook_url ?? '',
      alertsDingTalkWebhookURLConfigured: data.alerts?.dingtalk_webhook_url_configured ?? (data.alerts?.dingtalk_webhook_url ?? '') === REDACTED_SETTINGS_SECRET,
      alertsEmailEnabled: data.alerts?.email_enabled ?? false,
      alertsSMTPHost: data.alerts?.smtp_host ?? '',
      alertsSMTPPort: String(data.alerts?.smtp_port ?? 587),
      alertsSMTPUsername: data.alerts?.smtp_username ?? '',
      alertsSMTPPassword: '',
      alertsSMTPPasswordConfigured: data.alerts?.smtp_password_configured ?? false,
      alertsSMTPPasswordClear: false,
      alertsSMTPFrom: data.alerts?.smtp_from ?? '',
      alertsSMTPTo: data.alerts?.smtp_to?.join('\n') ?? '',
      scrubScheduleEnabled: data.maintenance?.scrub?.enabled ?? false,
      scrubScheduleInterval: data.maintenance?.scrub?.schedule_interval ?? '168h',
      scrubRetryInterval: data.maintenance?.scrub?.retry_interval ?? '1h',
      scrubMaxRetries: String(data.maintenance?.scrub?.max_retries ?? 1),
      diskHealthEnabled: data.disk_health?.enabled ?? false,
      diskHealthCheckInterval: data.disk_health?.check_interval ?? '1h',
      diskHealthProbeTimeout: data.disk_health?.probe_timeout ?? '15s',
      diskHealthCooldownPeriod: data.disk_health?.cooldown_period ?? '4h',
      diskHealthCommand: data.disk_health?.command ?? 'smartctl',
      diskHealthTemperatureWarningC: String(data.disk_health?.temperature_warning_c ?? 50),
      diskHealthTemperatureCriticalC: String(data.disk_health?.temperature_critical_c ?? 60),
      diskHealthMediaWearWarningPct: String(data.disk_health?.media_wear_warning_percent ?? 80),
      diskHealthMediaWearCriticalPct: String(data.disk_health?.media_wear_critical_percent ?? 100),
      diskHealthDevices: formatDiskHealthDeviceLines(data.disk_health?.devices),
      dataplaneMaxRetries: String(data.dataplane.max_retries),
      dataplaneGrpcAddress: data.dataplane.grpc_address,
      dataplaneTimeout: data.dataplane.timeout,
      minChunkSize: formatBytes(data.cdc.min_chunk_size),
      avgChunkSize: formatBytes(data.cdc.avg_chunk_size),
      maxChunkSize: formatBytes(data.cdc.max_chunk_size),
    }
  }, [])

  useEffect(() => {
    if (isDirty || !settingsData?.data) {
      return
    }

    if (
      savedSettingsOverride &&
      savedSettingsOverrideUpdatedAt !== null &&
      settingsDataUpdatedAt <= savedSettingsOverrideUpdatedAt
    ) {
      return
    }

    const nextDraftSettings = mapServerSettings(settingsData.data)
    let cancelled = false

    queueMicrotask(() => {
      if (cancelled) {
        return
      }

      setDraftSettings(nextDraftSettings)
      setSavedSettingsOverride(null)
      setSavedSettingsOverrideUpdatedAt(null)
    })

    return () => {
      cancelled = true
    }
  }, [isDirty, mapServerSettings, savedSettingsOverride, savedSettingsOverrideUpdatedAt, settingsData, settingsDataUpdatedAt])

  const settings = !isDirty && savedSettingsOverride
    ? savedSettingsOverride
    : !isDirty && settingsData?.data
      ? mapServerSettings(settingsData.data)
      : draftSettings
  const savedDirectoryQuotas = useMemo(() => {
    const savedSettings = savedSettingsOverride
      ?? (settingsData?.data ? mapServerSettings(settingsData.data) : null)
    if (!savedSettings) {
      return []
    }
    const parsedQuotas = parseDirectoryQuotaLines(savedSettings.directoryQuotas)
    return parsedQuotas.error ? [] : parsedQuotas.quotas
  }, [mapServerSettings, savedSettingsOverride, settingsData])
  const savedDirectoryAccessRules = useMemo(() => {
    const savedSettings = savedSettingsOverride
      ?? (settingsData?.data ? mapServerSettings(settingsData.data) : null)
    if (!savedSettings) {
      return []
    }
    const parsedRules = parseDirectoryAccessRuleLines(savedSettings.directoryAccessRules)
    return parsedRules.error ? [] : parsedRules.rules
  }, [mapServerSettings, savedSettingsOverride, settingsData])
  const savedSharePolicyReviewInput = useMemo<SharePolicyReviewInput>(() => {
    const savedSettings = savedSettingsOverride
      ?? (settingsData?.data ? mapServerSettings(settingsData.data) : null)
    if (!savedSettings) {
      return {
        enabled: false,
        baseURL: '',
        defaultExpiresIn: '168h',
        defaultMaxAccess: '0',
        rules: [],
      }
    }

    return {
      enabled: savedSettings.shareEnabled,
      baseURL: savedSettings.shareBaseURL,
      defaultExpiresIn: savedSettings.shareDefaultExpiresIn,
      defaultMaxAccess: savedSettings.shareDefaultMaxAccess,
      rules: savedSettings.sharePolicyRules,
    }
  }, [mapServerSettings, savedSettingsOverride, settingsData])
  const draftSharePolicyReviewInput: SharePolicyReviewInput = {
    enabled: settings.shareEnabled,
    baseURL: settings.shareBaseURL,
    defaultExpiresIn: settings.shareDefaultExpiresIn,
    defaultMaxAccess: settings.shareDefaultMaxAccess,
    rules: settings.sharePolicyRules,
  }
  const webdavNoAuthSelected = settings.webdavEnabled && settings.webdavAuthType === 'none'
  const serverBeyondLoopback = listensBeyondLoopback(settings.serverHost)
  const normalizedWebDAVPrefixDraft = normalizeWebDAVPrefix(settings.webdavPrefix)
  const webDAVPrefixHasInvalidCharacters = !isValidWebDAVPrefix(normalizedWebDAVPrefixDraft)
  const webDAVPrefixUsesReservedRoute = settings.webdavEnabled && webDAVPrefixOverlapsReservedRoute(normalizedWebDAVPrefixDraft)
  const webDAVPrefixErrorMessage = webDAVPrefixHasInvalidCharacters
    ? '前缀只能是 URL 路径，不能包含反斜杠、?、# 或控制字符'
    : webDAVPrefixUsesReservedRoute
      ? '前缀不能是 /、/api、/s、/health 或它们的子路径'
      : undefined
  const shareBaseURLReviewMessage = settings.shareEnabled
    ? shareBaseURLPublicReviewMessage(settings.shareBaseURL)
    : undefined
  const normalizedPublicAccessDomain = useMemo(() => {
    return normalizePublicDomainInput(publicAccessDomain)
  }, [publicAccessDomain])
  const publicAccessDomainError = publicDomainErrorMessage(publicAccessDomain)
  const publicAccessBaseURL = normalizedPublicAccessDomain ? `https://${normalizedPublicAccessDomain}` : ''
  const publicAccessShareNeedsDomain = settings.shareEnabled && !normalizedPublicAccessDomain && publicAccessDomain.trim() === ''

  const updateDirtySettings = (updater: (prev: typeof draftSettings) => typeof draftSettings) => {
    setIsDirty(true)
    setDraftSettings((prev) => updater(isDirty ? prev : settings))
  }

  const applyPublicAccessRecommendation = () => {
    if (publicAccessDomainError) {
      addToast({
        title: '公网域名无效',
        description: publicAccessDomainError,
        color: 'warning',
      })
      return
    }
    if (settings.shareEnabled && !publicAccessBaseURL) {
      addToast({
        title: '需要公网域名',
        description: '分享功能已启用，填写公网域名后再应用公网访问推荐。',
        color: 'warning',
      })
      return
    }
    updateDirtySettings((prev) => ({
      ...prev,
      serverHost: '127.0.0.1',
      serverTrustedProxyHops: '1',
      authAccessTokenTTL: publicAccessDurationRecommendation(
        prev.authAccessTokenTTL,
        PUBLIC_ACCESS_MAX_ACCESS_TOKEN_TTL_MS,
        '1h',
      ),
      authRefreshTokenTTL: publicAccessDurationRecommendation(
        prev.authRefreshTokenTTL,
        PUBLIC_ACCESS_MAX_REFRESH_TOKEN_TTL_MS,
        '720h',
      ),
      shareDefaultExpiresIn: prev.shareEnabled
        ? publicAccessDurationRecommendation(
          prev.shareDefaultExpiresIn,
          PUBLIC_ACCESS_MAX_SHARE_DEFAULT_EXPIRES_MS,
          '168h',
        )
        : prev.shareDefaultExpiresIn,
      shareDefaultMaxAccess: prev.shareEnabled
        ? publicAccessMaxAccessRecommendation(prev.shareDefaultMaxAccess, '20')
        : prev.shareDefaultMaxAccess,
      shareBaseURL: prev.shareEnabled && publicAccessBaseURL ? publicAccessBaseURL : prev.shareBaseURL,
    }))
    addToast({
      title: '已应用公网访问推荐',
      description: '保存设置后生效；监听地址变更需要重启服务，会话有效期、新分享默认有效期和默认访问次数会保持在公网建议范围内。',
      color: 'success',
    })
  }

  const applySecurityCheckFix = (check: SecurityCheckItem) => {
    switch (check.id) {
      case 'auth_enabled':
      case 'login_rate_limit':
        addToast({
          title: '需要启用 Web 登录认证',
          description: '在配置文件中设置 [auth].enabled = true，确认 jwt_secret 和 users_file 可用后重启服务；首次登录后请修改初始管理员密码。',
          color: 'warning',
        })
        return
      case 'https_request':
      case 'public_http_exposure':
      case 'browser_session_boundary':
        updateDirtySettings((prev) => ({
          ...prev,
          serverHost: '127.0.0.1',
          serverTrustedProxyHops: '1',
        }))
        addToast({ title: '已应用反向代理推荐', description: '保存设置后生效。', color: 'success' })
        return
      case 'public_share_boundary':
        if (check.details?.password_cookie_secure !== false) {
          addToast({
            title: '需要检查公开分享边界',
            description: '公开分享 cookie、失败限速或缓存边界异常；请先升级或检查服务端公开分享响应头后重新运行安全自检。',
            color: 'warning',
          })
          return
        }
        updateDirtySettings((prev) => ({
          ...prev,
          serverHost: '127.0.0.1',
          serverTrustedProxyHops: '1',
        }))
        addToast({ title: '已应用反向代理推荐', description: '保存设置后生效。', color: 'success' })
        return
      case 'trusted_proxy_or_tls':
        updateDirtySettings((prev) => ({
          ...prev,
          serverTrustedProxyHops: '1',
        }))
        addToast({ title: '已设置受信代理层数', description: '保存设置后生效。', color: 'success' })
        return
      case 'forwarded_proto_trust': {
        const proxySource = trustedProxySourceFromSecurityCheck(check)
        if (proxySource) {
          updateDirtySettings((prev) => ({
            ...prev,
            serverTrustedProxyHops: prev.serverTrustedProxyHops.trim() === '' || prev.serverTrustedProxyHops.trim() === '0'
              ? '1'
              : prev.serverTrustedProxyHops,
            serverTrustedProxyCIDRs: appendTrustedProxySourceCIDR(prev.serverTrustedProxyCIDRs, proxySource),
          }))
          addToast({ title: '已加入受信代理来源', description: '保存设置后会信任该代理的转发 header。', color: 'success' })
          return
        }
        updateDirtySettings((prev) => ({
          ...prev,
          serverHost: '127.0.0.1',
          serverTrustedProxyHops: '1',
        }))
        addToast({ title: '已应用反向代理推荐', description: '保存设置后生效。', color: 'success' })
        return
      }
      case 'server_listen':
        updateDirtySettings((prev) => ({ ...prev, serverHost: '127.0.0.1' }))
        addToast({ title: '已改为本机监听', description: '保存设置并重启服务后生效。', color: 'success' })
        return
      case 'dataplane_listen':
        updateDirtySettings((prev) => ({
          ...prev,
          dataplaneGrpcAddress: dataplaneLoopbackAddressFromSecurityCheck(check, prev.dataplaneGrpcAddress),
        }))
        addToast({ title: '已改为本机数据面地址', description: '保存设置后会校验并切换连接。', color: 'success' })
        return
      case 'webdav_auth':
        if (securityCheckUsesGeneratedWebDAVPassword(check) && securityCheckResponse?.data.config.auth_enabled !== false) {
          updateDirtySettings((prev) => ({
            ...prev,
            webdavAuthType: 'users',
            webdavUseGeneratedPassword: false,
          }))
          addToast({ title: '已启用 WebDAV 用户认证', description: '保存后可使用 MnemoNAS 账号挂载。', color: 'success' })
          return
        }
        if (securityCheckHasWebDAVPasswordRisk(check)) {
          updateDirtySettings((prev) => ({
            ...prev,
            webdavAuthType: 'basic',
            webdavPassword: '',
            webdavUseGeneratedPassword: true,
          }))
          addToast({ title: '已改用自动生成 WebDAV 密码', description: '保存后会使用服务端生成的强随机密码。', color: 'success' })
          return
        }
        if (securityCheckResponse?.data.config.auth_enabled === false) {
          updateDirtySettings((prev) => ({
            ...prev,
            webdavAuthType: 'basic',
            webdavUsername: prev.webdavUsername.trim() || 'admin',
            webdavPassword: '',
            webdavUseGeneratedPassword: true,
          }))
          addToast({
            title: '已启用 WebDAV Basic 认证',
            description: '当前 Web 登录认证未启用，已改用 Basic Auth 和自动生成密码；保存后生效。',
            color: 'success',
          })
          return
        }
        updateDirtySettings((prev) => ({
          ...prev,
          webdavAuthType: 'users',
          webdavUseGeneratedPassword: false,
        }))
        addToast({ title: '已启用 WebDAV 用户认证', description: '保存后可使用 MnemoNAS 账号挂载。', color: 'success' })
        return
      case 'webdav_prefix':
        updateDirtySettings((prev) => ({
          ...prev,
          webdavPrefix: '/dav',
        }))
        addToast({ title: '已改回 WebDAV 默认前缀', description: '保存后 WebDAV 挂载入口为 /dav。', color: 'success' })
        return
      case 'share_base_url': {
        const repairedShareBaseURL = publicAccessBaseURL || httpsShareBaseURLFromSecurityCheck(check)
        if (!repairedShareBaseURL) {
          addToast({ title: '需要公网域名', description: '先在公网访问向导中填写域名。', color: 'warning' })
          return
        }
        updateDirtySettings((prev) => ({ ...prev, shareBaseURL: repairedShareBaseURL }))
        addToast({ title: '已更新分享基础 URL', description: '保存设置后影响新创建的分享链接。', color: 'success' })
        return
      }
      case 'share_default_policy':
        updateDirtySettings((prev) => ({
          ...prev,
          shareDefaultExpiresIn: shareDefaultExpiresNeedsSecurityRepair(check) ? '168h' : prev.shareDefaultExpiresIn,
          shareDefaultMaxAccess: shareDefaultMaxAccessNeedsSecurityRepair(check) ? '20' : prev.shareDefaultMaxAccess,
        }))
        addToast({ title: '已应用分享默认策略建议', description: '保存设置后影响新创建的分享链接。', color: 'success' })
        return
      case 'backup_local_destinations':
        {
          const jobID = securityCheckStringDetail(check, 'job_id')
          addToast({
            title: '需要检查本地备份目标',
            description: redactSecurityActionToastDescription(getBackupLocalDestinationFixDescription(check)),
            color: 'warning',
          })
          navigate(jobID ? `/maintenance?backupJob=${encodeURIComponent(jobID)}` : '/maintenance')
        }
        return
      case 'unsafe_no_auth_override':
        addToast({
          title: '需要编辑配置文件',
          description: '将 [security].allow_unsafe_no_auth 改为 false，并确认 Web 登录和 WebDAV 认证已启用后重启服务。',
          color: 'warning',
        })
        return
      case 'config_file_access':
        {
          const configPath = securityCheckStringDetail(check, 'path')
          addToast({
            title: '需要检查配置文件路径',
            description: redactSecurityActionToastDescription(
              configPath
                ? `在服务器上确认 ${configPath} 是普通文件、路径组件不经过符号链接，并将权限设为 0600 后重新检查。`
                : '在服务器上确认 MnemoNAS 使用的 config.toml 是普通文件、路径组件不经过符号链接，并将权限设为 0600 后重新检查。',
            ),
            color: 'warning',
          })
        }
        return
      case 'secrets_file_access':
        {
          const secretsPath = securityCheckStringDetail(check, 'path')
          addToast({
            title: '需要检查自动 WebDAV 凭据',
            description: redactSecurityActionToastDescription(
              secretsPath
                ? `在服务器上确认 ${secretsPath} 是普通文件、路径组件不经过符号链接，并将权限设为 0600；也可以改用 WebDAV 用户认证或设置自定义强密码。`
                : '在服务器上确认 storage.root 下的 secrets.json 是普通文件、路径组件不经过符号链接，并将权限设为 0600；也可以改用 WebDAV 用户认证或设置自定义强密码。',
            ),
            color: 'warning',
          })
        }
        return
      case 'users_file_access':
        {
          const usersDir = securityCheckStringDetail(check, 'dir')
          const usersPath = securityCheckStringDetail(check, 'path')
          const permissionHint = usersDir && usersPath
            ? `在服务器上将用户文件目录 ${usersDir} 设为 0700，将用户文件 ${usersPath} 设为 0600；如果路径是符号链接或非普通文件，请先恢复为普通私有路径。`
            : '在服务器上移除符号链接，并将用户文件目录设为 0700、users.json 设为 0600 后重新检查。'
          addToast({
            title: '需要收紧用户文件权限',
            description: redactSecurityActionToastDescription(permissionHint),
            color: 'warning',
          })
        }
        return
      case 'initial_password_file':
        {
          const initialPasswordPath = securityCheckStringDetail(check, 'path')
          addToast({
            title: '需要移除初始密码路径',
            description: redactSecurityActionToastDescription(
              initialPasswordPath
                ? `完成首次登录并修改密码后，在服务器上删除 ${initialPasswordPath}；如果该路径是符号链接或目录，也一并移除后重新检查。`
                : '完成首次登录并修改密码后，在服务器上删除 initial-password.txt；如果该路径是符号链接或目录，也一并移除后重新检查。',
            ),
            color: 'warning',
          })
        }
        return
      case 'dataplane_http_listen': {
        const dataplaneHTTPAddress = dataplaneHTTPLoopbackAddressFromSecurityCheck(check)
        addToast({
          title: '需要调整启动环境',
          description: `将 DATAPLANE_HTTP_ADDR 设为 ${dataplaneHTTPAddress} 后重启 dataplane 和 MnemoNAS 服务。`,
          color: 'warning',
        })
        return
      }
      case 'admin_accounts':
        navigate('/users')
        return
      case 'session_token_ttl':
        updateDirtySettings((prev) => ({
          ...prev,
          authAccessTokenTTL: '1h',
          authRefreshTokenTTL: '720h',
        }))
        addToast({
          title: '已应用会话有效期建议',
          description: '保存后新签发的 Web UI token 会使用 1h/720h。',
          color: 'success',
        })
        return
      default:
        addToast({ title: '该项需要手动处理', color: 'warning' })
    }
  }

  const getSecurityCheckAction = (check: SecurityCheckItem): SecurityCheckAction | undefined => {
    if (check.status === 'pass') {
      return undefined
    }

    switch (check.id) {
      case 'auth_enabled':
        return { label: '启用认证', onPress: () => applySecurityCheckFix(check) }
      case 'login_rate_limit':
        if (check.details?.auth_enabled === false) {
          return { label: '启用认证', onPress: () => applySecurityCheckFix(check) }
        }
        return undefined
      case 'https_request':
        return { label: '应用代理推荐', onPress: () => applySecurityCheckFix(check) }
      case 'public_http_exposure':
        return { label: '应用代理推荐', onPress: () => applySecurityCheckFix(check) }
      case 'browser_session_boundary':
        return { label: '应用代理推荐', onPress: () => applySecurityCheckFix(check) }
      case 'public_share_boundary':
        if (check.status === 'warning' && check.details?.password_cookie_secure === false) {
          return { label: '应用代理推荐', onPress: () => applySecurityCheckFix(check) }
        }
        return undefined
      case 'trusted_proxy_or_tls':
        return { label: '设置代理层数', onPress: () => applySecurityCheckFix(check) }
      case 'forwarded_proto_trust':
        if (securityCheckHasTrustedNonHTTPSForwardedProto(check)) {
          return undefined
        }
        return { label: '修正代理设置', onPress: () => applySecurityCheckFix(check) }
      case 'server_listen':
        return { label: '改为本机监听', onPress: () => applySecurityCheckFix(check) }
      case 'dataplane_listen':
        return { label: '改为本机地址', onPress: () => applySecurityCheckFix(check) }
      case 'dataplane_http_listen':
        return { label: '查看环境变量', onPress: () => applySecurityCheckFix(check) }
      case 'webdav_auth':
        if (securityCheckUsesGeneratedWebDAVPassword(check)) {
          if (securityCheckResponse?.data.config.auth_enabled === false) {
            return undefined
          }
          return { label: '改用用户认证', onPress: () => applySecurityCheckFix(check) }
        }
        return { label: securityCheckHasWebDAVPasswordRisk(check) ? '更换密码' : '启用认证', onPress: () => applySecurityCheckFix(check) }
      case 'webdav_prefix':
        return { label: '改为 /dav', onPress: () => applySecurityCheckFix(check) }
      case 'share_base_url':
        return { label: '使用 HTTPS URL', onPress: () => applySecurityCheckFix(check) }
      case 'share_default_policy':
        return { label: '应用建议', onPress: () => applySecurityCheckFix(check) }
      case 'backup_local_destinations':
        return { label: '查看备份目标', onPress: () => applySecurityCheckFix(check) }
      case 'unsafe_no_auth_override':
        return { label: '关闭例外', onPress: () => applySecurityCheckFix(check) }
      case 'config_file_access':
        return { label: '查看配置路径', onPress: () => applySecurityCheckFix(check) }
      case 'secrets_file_access':
        return { label: '查看凭据路径', onPress: () => applySecurityCheckFix(check) }
      case 'users_file_access':
        return { label: '查看权限路径', onPress: () => applySecurityCheckFix(check) }
      case 'initial_password_file':
        return { label: '查看文件路径', onPress: () => applySecurityCheckFix(check) }
      case 'admin_accounts':
        return { label: '管理用户', onPress: () => applySecurityCheckFix(check) }
      case 'session_token_ttl':
        return { label: '应用建议', onPress: () => applySecurityCheckFix(check) }
      default:
        return undefined
    }
  }

  const handleReset = async () => {
    if (saveMutation.isPending) {
      return
    }
    const result = await refetch()
    if (result.error) {
      addToast(getSettingsActionErrorToast(result.error, {
        unavailable: '重置暂不可用',
        failure: '重置失败',
      }))
      return
    }

    if (result.data?.data) {
      setDraftSettings(mapServerSettings(result.data.data))
    }
    if (selectedTab === 'general') {
      void refetchSecurityCheck()
    }
    setSavedSettingsOverride(null)
    setSavedSettingsOverrideUpdatedAt(null)
    setIsDirty(false)

    addToast({ title: '已恢复为服务端当前配置', color: 'success' })
  }

  const handleRefreshSettings = async () => {
    const result = await refetch()
    if (result.error) {
      addToast(getSettingsActionErrorToast(result.error, {
        unavailable: '设置服务暂不可用',
        failure: '刷新失败',
      }))
      return
    }

    if (result.data?.data) {
      setDraftSettings(mapServerSettings(result.data.data))
    }
    setSavedSettingsOverride(null)
    setSavedSettingsOverrideUpdatedAt(null)
    setIsDirty(false)
    if (selectedTab === 'general') {
      void refetchSecurityCheck()
    }
    addToast({ title: '设置已刷新', color: 'success' })
  }

  const handleRefreshWebDAVCredentials = async () => {
    const result = await refetchWebDAVCredentials()
    if (result.error) {
      addToast(getWebDAVCredentialsRefreshErrorToast(result.error))
      return
    }

    addToast({ title: 'WebDAV 凭据已刷新', color: 'success' })
  }

  // Save mutation
  const saveMutation = useMutation({
    mutationFn: ({ request, signal }: SaveSettingsVariables) => updateSettings(request, { signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }
      const sanitizedSubmittedSettings = sanitizeSavedSettingsOverride(variables.submittedSettings, variables.request)
      setSavedSettingsOverride(sanitizedSubmittedSettings)
      setSavedSettingsOverrideUpdatedAt(variables.baseSettingsUpdatedAt)
      useAuthStore.getState().setShareEnabled(variables.submittedSettings.shareEnabled)
      setDraftSettings(current => sanitizeDirtyDraftAfterSave(current, variables.submittedSettings, sanitizedSubmittedSettings))

      if (shallowEqualSettingsDraft(draftSettingsRef.current, variables.submittedSettings)) {
        setIsDirty(false)
      }

      addToast(getSettingsSaveSuccessToast(result.message, result.warning === true))
      void refetch()
      if (selectedTab === 'general') {
        void refetchSecurityCheck()
      }
    },
    onError: (err: unknown, variables) => {
      if (variables.signal.aborted || isAbortError(err)) {
        return
      }
      addToast(getSettingsActionErrorToast(err, {
        unavailable: '保存设置暂不可用',
        failure: '保存失败',
      }))
    },
    onSettled: (_result, _error, variables) => {
      if (saveSettingsAbortControllerRef.current?.signal === variables.signal) {
        saveSettingsAbortControllerRef.current = null
      }
    },
  })

  const handleSave = () => {
    let minFreeSpaceBytes: number
    let alertsMinFreeBytes: number
    let trashMaxSizeBytes: number
    let versioningMaxSizeBytes: number
    let minChunkBytes: number
    let avgChunkBytes: number
    let maxChunkBytes: number
    const trimmedPort = settings.serverPort.trim()
    const parsedPort = Number(trimmedPort)
    const trimmedServerHost = settings.serverHost.trim()
    const trimmedReadTimeout = settings.serverReadTimeout.trim()
    const trimmedWriteTimeout = settings.serverWriteTimeout.trim()
    const trimmedIdleTimeout = settings.serverIdleTimeout.trim()
    const trimmedAuthAccessTokenTTL = settings.authAccessTokenTTL.trim()
    const trimmedAuthRefreshTokenTTL = settings.authRefreshTokenTTL.trim()
    const trimmedTrustedProxyHops = settings.serverTrustedProxyHops.trim()
    const parsedTrustedProxyHops = Number(trimmedTrustedProxyHops)
    const trimmedMaxVersions = settings.maxVersions.trim()
    const parsedMaxVersions = Number(trimmedMaxVersions)
    const trimmedMaxAge = settings.maxAge.trim()
    const trimmedGCInterval = settings.gcInterval.trim()
    const trimmedTrashRetentionDays = settings.trashRetentionDays.trim()
    const parsedTrashRetentionDays = Number(trimmedTrashRetentionDays)
    const trimmedDataplaneGrpcAddress = settings.dataplaneGrpcAddress.trim()
    const trimmedDataplaneTimeout = settings.dataplaneTimeout.trim()
    const trimmedMaxRetries = settings.dataplaneMaxRetries.trim()
    const parsedMaxRetries = Number(trimmedMaxRetries)
    const trimmedAlertsCheckInterval = settings.alertsCheckInterval.trim()
    const trimmedAlertsCooldownPeriod = settings.alertsCooldownPeriod.trim()
    const trimmedAlertsThresholdPct = settings.alertsThresholdPct.trim()
    const trimmedAlertsCriticalPct = settings.alertsCriticalPct.trim()
    const trimmedShareBaseURL = settings.shareBaseURL.trim()
    const trimmedShareDefaultExpiresIn = settings.shareDefaultExpiresIn.trim()
    const trimmedShareDefaultMaxAccess = settings.shareDefaultMaxAccess.trim()
    const parsedShareDefaultMaxAccess = parseNonNegativeSafeIntegerInput(trimmedShareDefaultMaxAccess)
    const trimmedAlertsWebhookURL = settings.alertsWebhookURL.trim()
    const trimmedAlertsWebhookMethod = settings.alertsWebhookMethod.trim().toUpperCase()
    const trimmedAlertsTelegramBotToken = settings.alertsTelegramBotToken.trim()
    const trimmedAlertsTelegramChatID = settings.alertsTelegramChatID.trim()
    const trimmedAlertsWeComWebhookURL = settings.alertsWeComWebhookURL.trim()
    const trimmedAlertsDingTalkWebhookURL = settings.alertsDingTalkWebhookURL.trim()
    const trimmedAlertsSMTPHost = settings.alertsSMTPHost.trim()
    const trimmedAlertsSMTPPort = settings.alertsSMTPPort.trim()
    const parsedAlertsSMTPPort = Number(trimmedAlertsSMTPPort)
    const trimmedAlertsSMTPUsername = settings.alertsSMTPUsername.trim()
    const trimmedAlertsSMTPPassword = settings.alertsSMTPPassword.trim()
    const trimmedAlertsSMTPFrom = settings.alertsSMTPFrom.trim()
    const trimmedScrubScheduleInterval = settings.scrubScheduleInterval.trim()
    const trimmedScrubRetryInterval = settings.scrubRetryInterval.trim()
    const trimmedScrubMaxRetries = settings.scrubMaxRetries.trim()
    const parsedScrubMaxRetries = Number(trimmedScrubMaxRetries)
    const trimmedDiskHealthCheckInterval = settings.diskHealthCheckInterval.trim()
    const trimmedDiskHealthProbeTimeout = settings.diskHealthProbeTimeout.trim()
    const trimmedDiskHealthCooldownPeriod = settings.diskHealthCooldownPeriod.trim()
    const trimmedDiskHealthCommand = settings.diskHealthCommand.trim()
    const trimmedDiskHealthTemperatureWarningC = settings.diskHealthTemperatureWarningC.trim()
    const parsedDiskHealthTemperatureWarningC = Number(trimmedDiskHealthTemperatureWarningC)
    const trimmedDiskHealthTemperatureCriticalC = settings.diskHealthTemperatureCriticalC.trim()
    const parsedDiskHealthTemperatureCriticalC = Number(trimmedDiskHealthTemperatureCriticalC)
    const trimmedDiskHealthMediaWearWarningPct = settings.diskHealthMediaWearWarningPct.trim()
    const parsedDiskHealthMediaWearWarningPct = Number(trimmedDiskHealthMediaWearWarningPct)
    const trimmedDiskHealthMediaWearCriticalPct = settings.diskHealthMediaWearCriticalPct.trim()
    const parsedDiskHealthMediaWearCriticalPct = Number(trimmedDiskHealthMediaWearCriticalPct)
    const alertsWebhookHeaders = settings.alertsWebhookHeaders
      .split('\n')
      .map(header => header.trim())
      .filter(Boolean)
    const alertsSMTPTo = settings.alertsSMTPTo
      .split(/[\n,]+/)
      .map(recipient => recipient.trim())
      .filter(Boolean)
    const parsedAlertsThresholdPct = Number(trimmedAlertsThresholdPct)
    const parsedAlertsCriticalPct = Number(trimmedAlertsCriticalPct)
    const savedSettingsForSecretPlaceholders = savedSettingsOverride ?? (settingsData?.data ? mapServerSettings(settingsData.data) : null)
    const versioningExtensions = settings.versioningExtensions
      .split('\n')
      .map(entry => entry.trim())
      .filter(Boolean)
    const versioningFilenames = settings.versioningFilenames
      .split('\n')
      .map(entry => entry.trim())
      .filter(Boolean)
    const trustedProxyCIDRs = settings.serverTrustedProxyCIDRs
      .split('\n')
      .map(entry => entry.trim())
      .filter(Boolean)
    const parsedDirectoryQuotas = parseDirectoryQuotaLines(settings.directoryQuotas)
    if (parsedDirectoryQuotas.error) {
      addToast({
        title: '目录配额格式无效',
        description: parsedDirectoryQuotas.error,
        color: 'danger',
      })
      return
    }
    const parsedDirectoryAccessRules = parseDirectoryAccessRuleLines(settings.directoryAccessRules)
    if (parsedDirectoryAccessRules.error) {
      addToast({
        title: '目录权限格式无效',
        description: parsedDirectoryAccessRules.error,
        color: 'danger',
      })
      return
    }
    const parsedSharePolicyRules = normalizeSharePolicyRulesForSave(settings.sharePolicyRules)
    if (parsedSharePolicyRules.error) {
      addToast({
        title: '分享路径策略格式无效',
        description: parsedSharePolicyRules.error,
        color: 'danger',
      })
      return
    }
    const parsedDiskHealthDevices = parseDiskHealthDeviceLines(settings.diskHealthDevices)
    if (parsedDiskHealthDevices.error) {
      addToast({
        title: '磁盘健康设备格式无效',
        description: parsedDiskHealthDevices.error,
        color: 'danger',
      })
      return
    }

    try {
      minFreeSpaceBytes = parseByteSize(settings.minFreeSpace)
      alertsMinFreeBytes = parseByteSize(settings.alertsMinFreeSpace)
      trashMaxSizeBytes = parseByteSize(settings.trashMaxSize)
      versioningMaxSizeBytes = parseByteSize(settings.versioningMaxSize)
      minChunkBytes = parseByteSize(settings.minChunkSize)
      avgChunkBytes = parseByteSize(settings.avgChunkSize)
      maxChunkBytes = parseByteSize(settings.maxChunkSize)
    } catch {
      addToast({
        title: '大小格式无效',
        description: BYTE_SIZE_FORMAT_ERROR_DESCRIPTION,
        color: 'danger',
      })
      return
    }

    const invalidCapacitySize = [
      {
        valid: isSafeByteSize(minFreeSpaceBytes, true),
        description: '最小空闲空间必须是 0 或不超过安全范围的整数',
      },
      {
        valid: isSafeByteSize(alertsMinFreeBytes, true),
        description: '提醒最小剩余空间必须是 0 或不超过安全范围的整数',
      },
      {
        valid: isSafeByteSize(trashMaxSizeBytes, false),
        description: '回收站最大容量必须是大于 0 且不超过安全范围的整数',
      },
      {
        valid: isSafeByteSize(versioningMaxSizeBytes, false),
        description: '最大自动版本化文件大小必须是大于 0 且不超过安全范围的整数',
      },
    ].find((item) => !item.valid)

    if (invalidCapacitySize) {
      addToast({
        title: '大小格式无效',
        description: invalidCapacitySize.description,
        color: 'danger',
      })
      return
    }

    if (minChunkBytes <= 0 || avgChunkBytes <= 0 || maxChunkBytes <= 0) {
      addToast({
        title: 'CDC 分块参数无效',
        description: '最小、平均和最大块大小都必须大于 0',
        color: 'danger',
      })
      return
    }

    if (minChunkBytes < MIN_CDC_CHUNK_SIZE_BYTES) {
      addToast({
        title: 'CDC 分块参数无效',
        description: '最小块大小不能小于 64 KB',
        color: 'danger',
      })
      return
    }

    if (minChunkBytes >= avgChunkBytes || avgChunkBytes >= maxChunkBytes) {
      addToast({
        title: 'CDC 分块参数无效',
        description: '请保持最小块大小 < 平均块大小 < 最大块大小',
        color: 'danger',
      })
      return
    }

    if (maxChunkBytes > MAX_CDC_CHUNK_SIZE_BYTES) {
      addToast({
        title: 'CDC 分块参数无效',
        description: '最大块大小不能超过 64 MB',
        color: 'danger',
      })
      return
    }

    if (!Number.isInteger(parsedPort) || parsedPort < 1 || parsedPort > 65535) {
      addToast({
        title: '端口格式无效',
        description: '端口必须是 1 到 65535 之间的整数',
        color: 'danger',
      })
      return
    }

    if (!isValidListenHost(trimmedServerHost)) {
      addToast({
        title: '监听地址格式无效',
        description: '监听地址必须为空、*、合法主机名、IPv4 或 IPv6，且不能包含端口、空白或控制字符',
        color: 'danger',
      })
      return
    }

    if (!trimmedReadTimeout) {
      addToast({
        title: '读取超时格式无效',
        description: '读取超时不能为空',
        color: 'danger',
      })
      return
    }

    if (!isPositiveDurationString(trimmedReadTimeout)) {
      addToast({
        title: '读取超时格式无效',
        description: '读取超时必须使用 30s / 1m 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
      return
    }

    if (!trimmedWriteTimeout) {
      addToast({
        title: '写入超时格式无效',
        description: '写入超时不能为空',
        color: 'danger',
      })
      return
    }

    if (!isPositiveDurationString(trimmedWriteTimeout)) {
      addToast({
        title: '写入超时格式无效',
        description: '写入超时必须使用 60s / 1m 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
      return
    }

    if (!trimmedIdleTimeout) {
      addToast({
        title: '空闲超时格式无效',
        description: '空闲超时不能为空',
        color: 'danger',
      })
      return
    }

    if (!isPositiveDurationString(trimmedIdleTimeout)) {
      addToast({
        title: '空闲超时格式无效',
        description: '空闲超时必须使用 120s / 2m 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
      return
    }

    if (!isPositiveDurationString(trimmedAuthAccessTokenTTL)) {
      addToast({
        title: '会话有效期无效',
        description: '访问令牌有效期必须使用 15m / 1h 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
      return
    }

    if (!isPositiveDurationString(trimmedAuthRefreshTokenTTL)) {
      addToast({
        title: '会话有效期无效',
        description: '刷新令牌有效期必须使用 168h / 720h 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
      return
    }

    if (!/^\d+$/.test(trimmedTrustedProxyHops) || !Number.isSafeInteger(parsedTrustedProxyHops)) {
      addToast({
        title: '受信代理层数格式无效',
        description: '受信代理层数必须是 0 或不超过安全范围的整数',
        color: 'danger',
      })
      return
    }

    for (const cidr of trustedProxyCIDRs) {
      if (!isValidTrustedProxyCIDR(cidr)) {
        addToast({
          title: '受信代理来源格式无效',
          description: '每行必须是 IP 地址或 CIDR，例如 10.0.0.0/8、192.168.1.10 或 fd00::/8',
          color: 'danger',
        })
        return
      }
    }

    if (!/^\d+$/.test(trimmedMaxVersions) || !Number.isSafeInteger(parsedMaxVersions)) {
      addToast({
        title: '最大版本数格式无效',
        description: '最大版本数必须是 0 或不超过安全范围的整数',
        color: 'danger',
      })
      return
    }

    if (!isNonNegativeDurationString(trimmedMaxAge)) {
      addToast({
        title: '最大保留时间格式无效',
        description: '最大保留时间必须是 0，或使用 2160h / 30m 这类 Go duration 格式',
        color: 'danger',
      })
      return
    }

    if (!isNonNegativeDurationString(trimmedGCInterval)) {
      addToast({
        title: 'GC 运行间隔格式无效',
        description: 'GC 运行间隔必须是 0，或使用 24h / 30m 这类 Go duration 格式',
        color: 'danger',
      })
      return
    }

    if (!trimmedDataplaneTimeout) {
      addToast({
        title: '数据面超时格式无效',
        description: '连接超时不能为空',
        color: 'danger',
      })
      return
    }

    if (!isPositiveDurationString(trimmedDataplaneTimeout)) {
      addToast({
        title: '数据面超时格式无效',
        description: '连接超时必须使用 30s / 1m 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
      return
    }

    if (!isValidTCPAddress(trimmedDataplaneGrpcAddress)) {
      addToast({
        title: '数据面地址格式无效',
        description: 'gRPC 地址必须是合法的 host:port，端口为 1 到 65535，且不能包含空白或控制字符',
        color: 'danger',
      })
      return
    }

    if (!/^\d+$/.test(trimmedMaxRetries) || !Number.isSafeInteger(parsedMaxRetries)) {
      addToast({
        title: '最大重试次数格式无效',
        description: '最大重试次数必须是 0 或不超过安全范围的整数',
        color: 'danger',
      })
      return
    }

    if (!trimmedAlertsCheckInterval) {
      addToast({
        title: '提醒检查间隔格式无效',
        description: '检查间隔不能为空',
        color: 'danger',
      })
      return
    }

    if (!isPositiveDurationString(trimmedAlertsCheckInterval)) {
      addToast({
        title: '提醒检查间隔格式无效',
        description: '检查间隔必须使用 1h / 30m 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
      return
    }

    if (!trimmedAlertsCooldownPeriod) {
      addToast({
        title: '提醒冷却时间格式无效',
        description: '冷却时间不能为空',
        color: 'danger',
      })
      return
    }

    if (!isPositiveDurationString(trimmedAlertsCooldownPeriod)) {
      addToast({
        title: '提醒冷却时间格式无效',
        description: '冷却时间必须使用 4h / 30m 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
      return
    }

    if (!/^\d+$/.test(trimmedAlertsThresholdPct) || !Number.isInteger(parsedAlertsThresholdPct) || parsedAlertsThresholdPct < 0 || parsedAlertsThresholdPct > 100) {
      addToast({
        title: '提醒阈值格式无效',
        description: '提醒阈值必须是 0 到 100 之间的整数',
        color: 'danger',
      })
      return
    }

    if (!/^\d+$/.test(trimmedAlertsCriticalPct) || !Number.isInteger(parsedAlertsCriticalPct) || parsedAlertsCriticalPct < 0 || parsedAlertsCriticalPct > 100) {
      addToast({
        title: '严重提醒阈值格式无效',
        description: '严重提醒阈值必须是 0 到 100 之间的整数',
        color: 'danger',
      })
      return
    }

    if (parsedAlertsCriticalPct < parsedAlertsThresholdPct) {
      addToast({
        title: '提醒阈值关系无效',
        description: '严重提醒阈值不能小于普通提醒阈值',
        color: 'danger',
      })
      return
    }

    if (!isValidShareBaseURL(trimmedShareBaseURL)) {
      addToast({
        title: '分享基础 URL 无效',
        description: '分享基础 URL 必须为空，或使用不含 userinfo、查询参数、片段、编码后的查询或片段标记、反斜杠、重复路径斜杠、. 或 .. 路径段且主机名有效的 http/https 地址',
        color: 'danger',
      })
      return
    }

    if (trimmedShareDefaultExpiresIn && trimmedShareDefaultExpiresIn !== '0' && !isValidDurationString(trimmedShareDefaultExpiresIn)) {
      addToast({
        title: '分享默认有效期无效',
        description: '默认有效期必须为空、0，或使用 168h / 30m 这类 Go duration 格式',
        color: 'danger',
      })
      return
    }

    if (!parsedShareDefaultMaxAccess.valid) {
      addToast({
        title: '分享默认访问次数无效',
        description: '默认访问次数必须是 0 或不超过安全范围的正整数',
        color: 'danger',
      })
      return
    }

    if (trimmedAlertsWebhookURL === REDACTED_SETTINGS_SECRET && !settings.alertsWebhookURLConfigured) {
      addToast({
        title: 'Webhook URL 无法保留',
        description: '当前没有已保存的 Webhook URL；新增 Webhook URL 需要填写真实地址。',
        color: 'danger',
      })
      return
    }

    if (trimmedAlertsWebhookURL !== REDACTED_SETTINGS_SECRET && !isValidOptionalHTTPURL(trimmedAlertsWebhookURL)) {
      addToast({
        title: 'Webhook URL 无效',
        description: 'Webhook URL 必须为空，或使用 http/https 的完整地址',
        color: 'danger',
      })
      return
    }

    if (trimmedAlertsWebhookMethod !== 'GET' && trimmedAlertsWebhookMethod !== 'POST') {
      addToast({
        title: 'Webhook 方法无效',
        description: 'Webhook 方法必须是 GET 或 POST',
        color: 'danger',
      })
      return
    }

    if (settings.alertsTelegramEnabled) {
      if (settings.alertsTelegramBotTokenClear) {
        addToast({
          title: 'Telegram Bot Token 缺失',
          description: '启用 Telegram 通知时不能清除已保存 Token；请先关闭 Telegram 通知或填写新的 Bot Token。',
          color: 'danger',
        })
        return
      }
      if (!trimmedAlertsTelegramBotToken && !settings.alertsTelegramBotTokenConfigured) {
        addToast({
          title: 'Telegram Bot Token 缺失',
          description: '首次启用 Telegram 通知时必须填写 Bot Token',
          color: 'danger',
        })
        return
      }
      if (!trimmedAlertsTelegramChatID) {
        addToast({
          title: 'Telegram Chat ID 缺失',
          description: '启用 Telegram 通知时必须填写 Chat ID 或频道用户名',
          color: 'danger',
        })
        return
      }
    }

    if (trimmedAlertsTelegramBotToken && /[\s/?#]/.test(trimmedAlertsTelegramBotToken)) {
      addToast({
        title: 'Telegram Bot Token 格式无效',
        description: 'Bot Token 不能包含空白、/、? 或 #',
        color: 'danger',
      })
      return
    }

    if (trimmedAlertsTelegramChatID && /\s/.test(trimmedAlertsTelegramChatID)) {
      addToast({
        title: 'Telegram Chat ID 格式无效',
        description: 'Chat ID 或频道用户名不能包含空白字符',
        color: 'danger',
      })
      return
    }

    if (trimmedAlertsWeComWebhookURL === REDACTED_SETTINGS_SECRET && !settings.alertsWeComWebhookURLConfigured) {
      addToast({
        title: '企业微信 Webhook 无法保留',
        description: '当前没有已保存的企业微信 Webhook URL；新增企业微信 Webhook 需要填写真实地址。',
        color: 'danger',
      })
      return
    }

    if (trimmedAlertsWeComWebhookURL !== REDACTED_SETTINGS_SECRET && !isValidOptionalHTTPURL(trimmedAlertsWeComWebhookURL)) {
      addToast({
        title: '企业微信 Webhook URL 无效',
        description: '企业微信 Webhook URL 必须为空，或使用 http/https 的完整地址',
        color: 'danger',
      })
      return
    }

    if (settings.alertsWeComEnabled && !trimmedAlertsWeComWebhookURL && !settings.alertsWeComWebhookURLConfigured) {
      addToast({
        title: '企业微信 Webhook URL 缺失',
        description: '启用企业微信通知时必须填写群机器人 Webhook URL。',
        color: 'danger',
      })
      return
    }

    if (trimmedAlertsDingTalkWebhookURL === REDACTED_SETTINGS_SECRET && !settings.alertsDingTalkWebhookURLConfigured) {
      addToast({
        title: '钉钉 Webhook 无法保留',
        description: '当前没有已保存的钉钉 Webhook URL；新增钉钉 Webhook 需要填写真实地址。',
        color: 'danger',
      })
      return
    }

    if (trimmedAlertsDingTalkWebhookURL !== REDACTED_SETTINGS_SECRET && !isValidOptionalHTTPURL(trimmedAlertsDingTalkWebhookURL)) {
      addToast({
        title: '钉钉 Webhook URL 无效',
        description: '钉钉 Webhook URL 必须为空，或使用 http/https 的完整地址',
        color: 'danger',
      })
      return
    }

    if (settings.alertsDingTalkEnabled && !trimmedAlertsDingTalkWebhookURL && !settings.alertsDingTalkWebhookURLConfigured) {
      addToast({
        title: '钉钉 Webhook URL 缺失',
        description: '启用钉钉通知时必须填写群机器人 Webhook URL。',
        color: 'danger',
      })
      return
    }

    if (!/^\d+$/.test(trimmedAlertsSMTPPort) || !Number.isInteger(parsedAlertsSMTPPort) || parsedAlertsSMTPPort < 1 || parsedAlertsSMTPPort > 65535) {
      addToast({
        title: 'SMTP 端口格式无效',
        description: 'SMTP 端口必须是 1 到 65535 之间的整数',
        color: 'danger',
      })
      return
    }

    if (settings.alertsEmailEnabled) {
      if (!trimmedAlertsSMTPHost) {
        addToast({
          title: 'SMTP 主机缺失',
          description: '启用邮件通知时必须填写 SMTP 主机。',
          color: 'danger',
        })
        return
      }
      if (!trimmedAlertsSMTPFrom) {
        addToast({
          title: 'SMTP 发件人缺失',
          description: '启用邮件通知时必须填写发件人地址。',
          color: 'danger',
        })
        return
      }
      if (alertsSMTPTo.length === 0) {
        addToast({
          title: 'SMTP 收件人缺失',
          description: '启用邮件通知时至少需要一个收件人。',
          color: 'danger',
        })
        return
      }
    }

    if (!trimmedScrubScheduleInterval) {
      addToast({
        title: 'Scrub 周期间隔格式无效',
        description: '周期 Scrub 的常规间隔不能为空',
        color: 'danger',
      })
      return
    }

    if (!isPositiveDurationString(trimmedScrubScheduleInterval)) {
      addToast({
        title: 'Scrub 周期间隔格式无效',
        description: '周期 Scrub 的常规间隔必须使用 168h / 1h 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
      return
    }

    if (!trimmedScrubRetryInterval) {
      addToast({
        title: 'Scrub 重试间隔格式无效',
        description: '周期 Scrub 的失败重试间隔不能为空',
        color: 'danger',
      })
      return
    }

    if (!isPositiveDurationString(trimmedScrubRetryInterval)) {
      addToast({
        title: 'Scrub 重试间隔格式无效',
        description: '周期 Scrub 的失败重试间隔必须使用 1h / 30m 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
      return
    }

    if (!/^\d+$/.test(trimmedScrubMaxRetries) || !Number.isSafeInteger(parsedScrubMaxRetries)) {
      addToast({
        title: 'Scrub 重试次数格式无效',
        description: '最大重试次数必须是 0 或不超过安全范围的整数',
        color: 'danger',
      })
      return
    }

    if (!trimmedDiskHealthCheckInterval || !isPositiveDurationString(trimmedDiskHealthCheckInterval)) {
      addToast({
        title: '磁盘健康检查间隔格式无效',
        description: '检查间隔必须使用 1h / 30m 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
      return
    }

    if (!trimmedDiskHealthProbeTimeout || !isPositiveDurationString(trimmedDiskHealthProbeTimeout)) {
      addToast({
        title: '磁盘健康探测超时格式无效',
        description: '探测超时必须使用 15s / 1m 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
      return
    }

    if (!trimmedDiskHealthCooldownPeriod || !isPositiveDurationString(trimmedDiskHealthCooldownPeriod)) {
      addToast({
        title: '磁盘健康冷却时间格式无效',
        description: '冷却时间必须使用 4h / 30m 这类 Go duration 格式，且大于 0',
        color: 'danger',
      })
      return
    }

    if (!isValidDiskHealthCommand(trimmedDiskHealthCommand)) {
      addToast({
        title: '磁盘健康命令格式无效',
        description: '命令必须是单个可执行文件名或绝对路径，不能包含空白或控制字符',
        color: 'danger',
      })
      return
    }

    if (!/^\d+$/.test(trimmedDiskHealthTemperatureWarningC) || !Number.isSafeInteger(parsedDiskHealthTemperatureWarningC)) {
      addToast({
        title: '磁盘温度提醒阈值格式无效',
        description: '温度提醒阈值必须是 0 或不超过安全范围的整数',
        color: 'danger',
      })
      return
    }

    if (!/^\d+$/.test(trimmedDiskHealthTemperatureCriticalC) || !Number.isSafeInteger(parsedDiskHealthTemperatureCriticalC)) {
      addToast({
        title: '磁盘温度严重阈值格式无效',
        description: '温度严重阈值必须是 0 或不超过安全范围的整数',
        color: 'danger',
      })
      return
    }

    if (parsedDiskHealthTemperatureWarningC > 0 && parsedDiskHealthTemperatureCriticalC > 0 && parsedDiskHealthTemperatureCriticalC < parsedDiskHealthTemperatureWarningC) {
      addToast({
        title: '磁盘温度阈值关系无效',
        description: '温度严重阈值不能小于提醒阈值',
        color: 'danger',
      })
      return
    }

    if (!/^\d+$/.test(trimmedDiskHealthMediaWearWarningPct) || !Number.isInteger(parsedDiskHealthMediaWearWarningPct) || parsedDiskHealthMediaWearWarningPct > 100) {
      addToast({
        title: '介质磨损提醒阈值格式无效',
        description: '介质磨损提醒阈值必须是 0 到 100 之间的整数',
        color: 'danger',
      })
      return
    }

    if (!/^\d+$/.test(trimmedDiskHealthMediaWearCriticalPct) || !Number.isInteger(parsedDiskHealthMediaWearCriticalPct) || parsedDiskHealthMediaWearCriticalPct > 100) {
      addToast({
        title: '介质磨损严重阈值格式无效',
        description: '介质磨损严重阈值必须是 0 到 100 之间的整数',
        color: 'danger',
      })
      return
    }

    if (parsedDiskHealthMediaWearWarningPct > 0 && parsedDiskHealthMediaWearCriticalPct > 0 && parsedDiskHealthMediaWearCriticalPct < parsedDiskHealthMediaWearWarningPct) {
      addToast({
        title: '介质磨损阈值关系无效',
        description: '介质磨损严重阈值不能小于提醒阈值',
        color: 'danger',
      })
      return
    }

    if (!/^\d+$/.test(trimmedTrashRetentionDays) || !Number.isSafeInteger(parsedTrashRetentionDays)) {
      addToast({
        title: '回收站保留天数格式无效',
        description: '回收站保留天数必须是 0 或不超过安全范围的整数',
        color: 'danger',
      })
      return
    }

    for (const header of alertsWebhookHeaders) {
      if (!isValidWebhookHeaderLine(header)) {
        addToast({
          title: 'Webhook Header 格式无效',
          description: '每行必须使用合法的 HTTP Header 名称和值',
          color: 'danger',
        })
        return
      }
    }
    const duplicateWebhookHeaderName = findDuplicateWebhookHeaderName(alertsWebhookHeaders)
    if (duplicateWebhookHeaderName) {
      addToast({
        title: 'Webhook Header 重复',
        description: `Header ${duplicateWebhookHeaderName} 重复；每个自定义 Header 名称只能配置一次。`,
        color: 'danger',
      })
      return
    }
    const unknownRedactedWebhookHeader = findUnknownRedactedWebhookHeader(
      alertsWebhookHeaders,
      savedSettingsForSecretPlaceholders?.alertsWebhookHeaders ?? '',
    )
    if (unknownRedactedWebhookHeader) {
      addToast({
        title: 'Webhook Header 无法保留',
        description: `Header ${unknownRedactedWebhookHeader} 没有已保存的值；新增或改名的 Header 需要填写真实值。`,
        color: 'danger',
      })
      return
    }

    const normalizedWebDAVPrefix = normalizeWebDAVPrefix(settings.webdavPrefix)
    if (!isValidWebDAVPrefix(normalizedWebDAVPrefix)) {
      addToast({
        title: 'WebDAV 前缀格式无效',
        description: 'WebDAV 前缀只能是 URL 路径，不能包含反斜杠、?、# 或控制字符',
        color: 'danger',
      })
      return
    }
    if (settings.webdavEnabled && webDAVPrefixOverlapsReservedRoute(normalizedWebDAVPrefix)) {
      addToast({
        title: 'WebDAV 前缀不可用',
        description: 'WebDAV 前缀不能是 /、/api、/s、/health 或它们的子路径',
        color: 'danger',
      })
      return
    }

    const tlsCertFile = settings.tlsCertFile.trim()
    const tlsKeyFile = settings.tlsKeyFile.trim()
    const tlsCertDir = settings.tlsCertDir.trim()
    if (settings.tlsEnabled && Boolean(tlsCertFile) !== Boolean(tlsKeyFile)) {
      addToast({
        title: 'TLS 证书配置无效',
        description: '证书文件和私钥文件必须同时设置或同时留空',
        color: 'danger',
      })
      return
    }
    if (settings.tlsEnabled && tlsCertFile !== '' && tlsCertFile === tlsKeyFile) {
      addToast({
        title: 'TLS 证书配置无效',
        description: '证书文件和私钥文件必须指向不同文件',
        color: 'danger',
      })
      return
    }
    if (settings.tlsEnabled && !settings.tlsAutoGenerate && tlsCertFile === '' && tlsKeyFile === '' && tlsCertDir === '') {
      addToast({
        title: 'TLS 证书配置无效',
        description: '禁用自动生成时必须配置证书目录或证书文件对',
        color: 'danger',
      })
      return
    }

    const req: UpdateSettingsRequest = {
      server: {
        host: trimmedServerHost,
        port: parsedPort,
        read_timeout: trimmedReadTimeout,
        write_timeout: trimmedWriteTimeout,
        idle_timeout: trimmedIdleTimeout,
        trusted_proxy_hops: parsedTrustedProxyHops,
        trusted_proxy_cidrs: trustedProxyCIDRs,
        tls: {
          enabled: settings.tlsEnabled,
          cert_file: tlsCertFile,
          key_file: tlsKeyFile,
          auto_generate: settings.tlsAutoGenerate,
          cert_dir: tlsCertDir,
        },
      },
      storage: {
        directory_quotas: parsedDirectoryQuotas.quotas,
        directory_access_rules: parsedDirectoryAccessRules.rules,
      },
      auth: {
        access_token_ttl: trimmedAuthAccessTokenTTL,
        refresh_token_ttl: trimmedAuthRefreshTokenTTL,
      },
      retention: {
        max_versions: parsedMaxVersions,
        max_age: trimmedMaxAge,
        min_free_space: minFreeSpaceBytes,
        gc_interval: trimmedGCInterval,
      },
      versioning: {
        auto_versioned_extensions: versioningExtensions,
        auto_versioned_filenames: versioningFilenames,
        max_versioned_size: versioningMaxSizeBytes,
      },
      trash: {
        enabled: settings.trashEnabled,
        retention_days: parsedTrashRetentionDays,
        max_size: trashMaxSizeBytes,
      },
      dataplane: {
        grpc_address: trimmedDataplaneGrpcAddress,
        timeout: trimmedDataplaneTimeout,
        max_retries: parsedMaxRetries,
      },
      share: {
        enabled: settings.shareEnabled,
        base_url: trimmedShareBaseURL,
        default_expires_in: trimmedShareDefaultExpiresIn,
        default_max_access: parsedShareDefaultMaxAccess.value,
        policy_rules: parsedSharePolicyRules.rules,
      },
      favorites: {
        enabled: settings.favoritesEnabled,
      },
      alerts: {
        enabled: settings.alertsEnabled,
        check_interval: trimmedAlertsCheckInterval,
        threshold_pct: parsedAlertsThresholdPct,
        critical_pct: parsedAlertsCriticalPct,
        min_free_bytes: alertsMinFreeBytes,
        cooldown_period: trimmedAlertsCooldownPeriod,
        webhook_url: trimmedAlertsWebhookURL,
        webhook_method: trimmedAlertsWebhookMethod,
        webhook_headers: alertsWebhookHeaders,
        telegram_enabled: settings.alertsTelegramEnabled,
        telegram_chat_id: trimmedAlertsTelegramChatID,
        wecom_enabled: settings.alertsWeComEnabled,
        wecom_webhook_url: trimmedAlertsWeComWebhookURL,
        dingtalk_enabled: settings.alertsDingTalkEnabled,
        dingtalk_webhook_url: trimmedAlertsDingTalkWebhookURL,
        email_enabled: settings.alertsEmailEnabled,
        smtp_host: trimmedAlertsSMTPHost,
        smtp_port: parsedAlertsSMTPPort,
        smtp_username: trimmedAlertsSMTPUsername,
        smtp_from: trimmedAlertsSMTPFrom,
        smtp_to: alertsSMTPTo,
        ...(settings.alertsTelegramBotTokenClear
          ? { telegram_bot_token: '' }
          : trimmedAlertsTelegramBotToken
            ? { telegram_bot_token: trimmedAlertsTelegramBotToken }
            : {}),
        ...(settings.alertsSMTPPasswordClear
          ? { smtp_password: '' }
          : trimmedAlertsSMTPPassword
            ? { smtp_password: trimmedAlertsSMTPPassword }
            : {}),
      },
      disk_health: {
        enabled: settings.diskHealthEnabled,
        check_interval: trimmedDiskHealthCheckInterval,
        probe_timeout: trimmedDiskHealthProbeTimeout,
        cooldown_period: trimmedDiskHealthCooldownPeriod,
        command: trimmedDiskHealthCommand,
        temperature_warning_c: parsedDiskHealthTemperatureWarningC,
        temperature_critical_c: parsedDiskHealthTemperatureCriticalC,
        media_wear_warning_percent: parsedDiskHealthMediaWearWarningPct,
        media_wear_critical_percent: parsedDiskHealthMediaWearCriticalPct,
        devices: parsedDiskHealthDevices.devices,
      },
      maintenance: {
        scrub: {
          enabled: settings.scrubScheduleEnabled,
          schedule_interval: trimmedScrubScheduleInterval,
          retry_interval: trimmedScrubRetryInterval,
          max_retries: parsedScrubMaxRetries,
        },
      },
      cdc: {
        min_chunk_size: minChunkBytes,
        avg_chunk_size: avgChunkBytes,
        max_chunk_size: maxChunkBytes,
      },
      webdav: {
        enabled: settings.webdavEnabled,
        prefix: normalizedWebDAVPrefix,
        read_only: settings.webdavReadOnly,
        auth_type: settings.webdavAuthType,
        username: settings.webdavUsername,
        ...(settings.webdavAuthType === 'basic' && settings.webdavUseGeneratedPassword
          ? { password: '' }
          : settings.webdavAuthType === 'basic' && settings.webdavPassword
            ? { password: settings.webdavPassword }
            : {}),
      },
    }
    saveSettingsAbortControllerRef.current?.abort()
    const controller = new AbortController()
    saveSettingsAbortControllerRef.current = controller
    saveMutation.mutate({
      request: req,
      submittedSettings: { ...settings },
      baseSettingsUpdatedAt: settingsDataUpdatedAt,
      signal: controller.signal,
    })
  }

  if (isLoading) {
    return (
      <div className="h-full overflow-auto custom-scrollbar">
        <div className="max-w-4xl mx-auto p-4 sm:p-6 lg:p-7">
          <PageHeader
            title="设置"
            subtitle="调整网络、访问和数据保留"
            actions={
              <>
                <Button
                  variant="bordered"
                  className="btn-secondary btn-md rounded-lg"
                  startContent={<RefreshCw size={16} />}
                  isDisabled
                >
                  重置
                </Button>
                <Button
                  className="btn-primary btn-md rounded-lg"
                  startContent={<Save size={16} />}
                  isDisabled
                >
                  保存设置
                </Button>
              </>
            }
            className="mb-8"
          />

          <Card className="card-mnemonas">
            <CardBody className="py-16">
              <div className="text-center">
                <div className="w-12 h-12 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-4" />
                <p className="text-default-500">加载设置...</p>
              </div>
            </CardBody>
          </Card>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="h-full flex items-center justify-center p-6">
        <EmptyState
          icon={AlertCircle}
          title={settingsLoadErrorPresentation!.title}
          description={settingsLoadErrorPresentation!.description}
          action={
            <Button variant="bordered" className="rounded-lg" onPress={handleRefreshSettings} isLoading={isRefetching}>
              重新加载
            </Button>
          }
        />
      </div>
    )
  }

  return (
    <div className="h-full overflow-auto custom-scrollbar">
      <div className="max-w-4xl mx-auto p-4 sm:p-6 lg:p-7">
        {/* Header */}
        <PageHeader
          title="设置"
          subtitle="调整网络、访问和数据保留"
          actions={
            <>
              <Button
                variant="bordered"
                className="btn-secondary btn-md rounded-lg"
                startContent={<RefreshCw size={16} />}
                onPress={handleReset}
                isLoading={isRefetching}
                isDisabled={saveMutation.isPending}
              >
                重置
              </Button>
              <Button
                className="btn-primary btn-md rounded-lg"
                startContent={<Save size={16} />}
                isLoading={saveMutation.isPending}
                onPress={handleSave}
              >
                保存设置
              </Button>
            </>
          }
          className="mb-8"
        />

        {/* Tabs */}
        <Tabs 
          selectedKey={selectedTab}
          onSelectionChange={handleTabSelectionChange}
          aria-label="设置分类"
          classNames={{
            base: "w-full",
            tabList: "grid w-full max-w-full grid-cols-2 justify-start gap-1 overflow-visible rounded-lg border border-divider bg-content1 p-1 shadow-[var(--shadow-soft)] sm:flex sm:flex-nowrap",
            tab: "!w-full min-w-0 px-3 py-2 rounded-lg text-default-600 data-[selected=true]:bg-accent-primary data-[selected=true]:text-white data-[selected=true]:shadow-sm whitespace-nowrap sm:!w-auto sm:!flex-none sm:min-w-fit sm:px-4",
            cursor: "hidden",
          }}
        >
          <Tab key="general" title="常规">
            <div className="space-y-6 mt-6">
              <PublicAccessWizard
                domainInput={publicAccessDomain}
                normalizedDomain={normalizedPublicAccessDomain}
                domainError={publicAccessDomainError}
                proxy={publicAccessProxy}
                shareEnabled={settings.shareEnabled}
                shareNeedsDomain={publicAccessShareNeedsDomain}
                isApplyDisabled={!!publicAccessDomainError || publicAccessShareNeedsDomain}
                onDomainChange={setPublicAccessDomain}
                onProxyChange={setPublicAccessProxy}
                onApplyRecommendation={applyPublicAccessRecommendation}
              />

              <SecurityCheckCard
                data={securityCheckResponse?.data}
                error={securityCheckError}
                isLoading={isLoadingSecurityCheck}
                isRefreshing={isRefetchingSecurityCheck}
                onRefresh={() => { void refetchSecurityCheck() }}
                getAction={getSecurityCheckAction}
              />

              <SettingsSection
                title="认证会话"
                description="配置 Web UI token 有效期；保存后影响新签发的访问令牌和刷新令牌"
                icon={Key}
              >
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">访问令牌有效期</label>
                    <Input
                      aria-label="访问令牌有效期"
                      placeholder="15m"
                      value={settings.authAccessTokenTTL}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, authAccessTokenTTL: v }))}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                      }}
                    />
                  </div>
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">刷新令牌有效期</label>
                    <Input
                      aria-label="刷新令牌有效期"
                      placeholder="168h"
                      value={settings.authRefreshTokenTTL}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, authRefreshTokenTTL: v }))}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                      }}
                    />
                  </div>
                </div>
              </SettingsSection>

              <SettingsSection
                title="服务器"
                description="配置服务器网络参数；保存后需重启服务才能影响监听地址、端口和 HTTP 超时"
                icon={Server}
              >
                <div className="space-y-4">
                  <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">监听地址</label>
                      <Input
                        aria-label="服务器监听地址"
                        placeholder="0.0.0.0"
                        value={settings.serverHost}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, serverHost: v }))}
                        startContent={<Globe size={16} className="text-default-500" />}
                        classNames={{
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                        }}
                      />
                    </div>
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">端口</label>
                      <Input
                        aria-label="服务器端口"
                        placeholder="8080"
                        type="number"
                        min={1}
                        max={65535}
                        inputMode="numeric"
                        value={settings.serverPort}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, serverPort: v }))}
                        classNames={{
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                        }}
                      />
                    </div>
                  </div>
                  <Divider className="bg-divider" />
                  <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">读取超时</label>
                      <Input
                        aria-label="服务器读取超时"
                        placeholder="30s"
                        value={settings.serverReadTimeout}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, serverReadTimeout: v }))}
                        classNames={{
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                        }}
                      />
                    </div>
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">写入超时</label>
                      <Input
                        aria-label="服务器写入超时"
                        placeholder="60s"
                        value={settings.serverWriteTimeout}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, serverWriteTimeout: v }))}
                        classNames={{
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                        }}
                      />
                    </div>
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">空闲超时</label>
                      <Input
                        aria-label="服务器空闲超时"
                        placeholder="120s"
                        value={settings.serverIdleTimeout}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, serverIdleTimeout: v }))}
                        classNames={{
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                        }}
                      />
                    </div>
                  </div>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="受信代理层数"
                    description="默认忽略转发头；仅在受信反向代理后方部署时设置为实际代理层数"
                  >
                    <Input
                      placeholder="0"
                      type="number"
                      min={0}
                      inputMode="numeric"
                      value={settings.serverTrustedProxyHops}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, serverTrustedProxyHops: v }))}
                      className="w-28"
                      aria-label="受信代理层数"
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <SettingRow
                    label="受信代理来源"
                    description="逐行填写非 loopback 代理直连来源的 IP 或 CIDR；为空时仅信任本机代理"
                  >
                    <textarea
                      aria-label="受信代理来源"
                      rows={3}
                      value={settings.serverTrustedProxyCIDRs}
                      onChange={(event) => updateDirtySettings(s => ({ ...s, serverTrustedProxyCIDRs: event.target.value }))}
                      className="input-shell min-h-24 w-full rounded-medium border border-transparent bg-transparent px-3 py-2 font-mono text-sm outline-none focus:border-accent-primary"
                      placeholder={'172.16.0.0/12\n192.168.1.10\nfd00::/8'}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>

              <SettingsSection
                title="TLS / HTTPS"
                description="配置 HTTPS 证书与自动生成策略；保存后需重启服务才能切换运行中的监听器"
                icon={Shield}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="启用 HTTPS"
                    description="启用后服务将使用 TLS 证书提供 HTTPS"
                  >
                    <Switch
                      aria-label="启用 HTTPS"
                      isSelected={settings.tlsEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, tlsEnabled: v }))}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                        label: "sr-only",
                      }}
                    >
                      启用 HTTPS
                    </Switch>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="自动生成证书"
                    description="证书缺失时自动生成自签名证书"
                  >
                    <Switch
                      aria-label="自动生成证书"
                      isSelected={settings.tlsAutoGenerate}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, tlsAutoGenerate: v }))}
                      isDisabled={!settings.tlsEnabled}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                        label: "sr-only",
                      }}
                    >
                      自动生成证书
                    </Switch>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">证书文件</label>
                      <Input
                        aria-label="TLS 证书文件"
                        value={settings.tlsCertFile}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, tlsCertFile: v }))}
                        placeholder="/path/to/server.crt"
                        isDisabled={!settings.tlsEnabled}
                        classNames={{ inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary" }}
                      />
                    </div>
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">私钥文件</label>
                      <Input
                        aria-label="TLS 私钥文件"
                        value={settings.tlsKeyFile}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, tlsKeyFile: v }))}
                        placeholder="/path/to/server.key"
                        isDisabled={!settings.tlsEnabled}
                        classNames={{ inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary" }}
                      />
                    </div>
                  </div>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="证书目录"
                    description="自动生成证书时使用的存放目录"
                  >
                    <Input
                      aria-label="TLS 证书目录"
                      value={settings.tlsCertDir}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, tlsCertDir: v }))}
                      placeholder="<storage.root>/.mnemonas/certs"
                      isDisabled={!settings.tlsEnabled || !settings.tlsAutoGenerate}
                      classNames={{ inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9" }}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>

              <SettingsSection
                title="存储路径"
                description="显示当前数据存储根目录"
                icon={Folder}
              >
                <div className="space-y-4">
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">存储根目录</label>
                    <div className="w-full rounded-lg border border-divider bg-content2/40 px-3 py-3 text-sm text-foreground">
                      {settings.storageRoot || '~/.mnemonas'}
                    </div>
                  </div>
                  <div className="text-xs text-default-500">
                    当前值由服务端配置文件决定，界面中不可直接修改。如需调整，请修改配置文件并重启服务。
                  </div>
                </div>
              </SettingsSection>
            </div>
          </Tab>

          <Tab key="retention" title="版本保留">
            <div className="space-y-6 mt-6">
              <SettingsSection
                title="版本策略"
                description="配置文件历史版本保留规则；保存后会立即更新运行中的保留阈值，gc_interval 设为 0 表示禁用周期清理"
                icon={Clock}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="启用回收站"
                    description="关闭后删除操作将直接永久删除，不再进入回收站"
                  >
                    <Switch
                      aria-label="启用回收站"
                      isSelected={settings.trashEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, trashEnabled: v }))}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                        label: "sr-only",
                      }}
                    >
                      启用回收站
                    </Switch>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="回收站保留天数"
                    description="回收站项目的保留时间；设置为 0 表示进入后立即过期，等待清理任务删除"
                  >
                    <Input
                      aria-label="回收站保留天数"
                      type="number"
                      min={0}
                      step={1}
                      inputMode="numeric"
                      value={settings.trashRetentionDays}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, trashRetentionDays: v }))}
                      className="w-24"
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="回收站最大容量"
                    description="超过该上限时，系统会优先清理最早删除的项目，为最新删除的项目腾出空间"
                  >
                    <Input
                      aria-label="回收站最大容量"
                      value={settings.trashMaxSize}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, trashMaxSize: v }))}
                      className="w-32"
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最大版本数"
                    description="每个文件最多保留的历史版本数量"
                  >
                    <Input
                      aria-label="最大版本数"
                      type="number"
                      min={0}
                      step={1}
                      inputMode="numeric"
                      value={settings.maxVersions}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, maxVersions: v }))}
                      className="w-24"
                        classNames={{
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                        }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最大保留时间"
                    description="历史版本的最长保留期限"
                  >
                    <Input
                      aria-label="最大保留时间"
                      value={settings.maxAge}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, maxAge: v }))}
                      placeholder="2160h"
                      className="w-24"
                        classNames={{
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                        }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最小空闲空间"
                    description="剩余空间低于该阈值时，写入后会强制执行一次全局历史版本清理"
                  >
                    <Input
                      aria-label="最小空闲空间"
                      value={settings.minFreeSpace}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, minFreeSpace: v }))}
                      className="w-24"
                        classNames={{
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                        }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="GC 运行间隔"
                    description="后台历史版本清理任务的执行周期；设为 0 表示禁用周期清理"
                  >
                    <Input
                      aria-label="GC 运行间隔"
                      value={settings.gcInterval}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, gcInterval: v }))}
                      placeholder="24h"
                      className="w-24"
                        classNames={{
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                        }}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>

              <SettingsSection
                title="目录配额"
                description="限制指定逻辑目录的当前文件总量；保存后会立即应用到 Web/API 与 WebDAV 写入"
                icon={HardDrive}
              >
                <div className="space-y-3">
                  <textarea
                    aria-label="目录配额"
                    value={settings.directoryQuotas}
                    onChange={(event) => updateDirtySettings(s => ({ ...s, directoryQuotas: event.target.value }))}
                    rows={4}
                    placeholder={'/team 1 TB\n"/Family Photos" 500 GB'}
                    className="input-shell w-full rounded-medium border border-transparent bg-transparent px-3 py-2 font-mono text-sm outline-none focus:border-accent-primary"
                  />
                  <DirectoryQuotaChangeReview
                    savedQuotas={savedDirectoryQuotas}
                    draftValue={settings.directoryQuotas}
                  />
                  <div className="grid gap-2 text-xs text-default-500 sm:grid-cols-2">
                    <div className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
                      每行一个目录，例如 <span className="font-mono text-foreground">/team 1 TB</span>；路径含空格或双引号时使用双引号，路径内双引号写作 \"
                    </div>
                    <div className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
                      命中上传、复制、移动、回收站恢复、版本恢复和 WebDAV 写入
                    </div>
                  </div>
                </div>
              </SettingsSection>

              <SettingsSection
                title="目录权限"
                description="为共享目录授予读写权限；最具体路径规则优先于父目录规则"
                icon={Shield}
              >
                <div className="space-y-3">
                  <DirectoryAccessRuleEditor
                    value={settings.directoryAccessRules}
                    onChange={(nextValue) => updateDirtySettings(s => ({ ...s, directoryAccessRules: nextValue }))}
                  />
                  <DirectoryAccessRuleChangeReview
                    savedRules={savedDirectoryAccessRules}
                    draftValue={settings.directoryAccessRules}
                  />
                  <DirectoryAccessCoverageSummary draftValue={settings.directoryAccessRules} />
                  <div className="grid gap-2 text-xs text-default-500 sm:grid-cols-2">
                    <div className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
                      路径直接填写 MnemoNAS 逻辑路径；包含空格或双引号时不需要额外加引号
                    </div>
                    <div className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
                      读/写用户、用户组和角色支持多个值，使用英文逗号分隔
                    </div>
                  </div>
                  <div className="rounded-lg border border-divider bg-content1/60 p-3">
                    <div className="grid gap-3 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto_auto_auto]">
                      <Input
                        label="检查用户"
                        value={accessCheckUsername}
                        onValueChange={setAccessCheckUsername}
                        placeholder="alice"
                        className="input-shell"
                      />
                      <Input
                        label="检查路径"
                        value={accessCheckPath}
                        onValueChange={setAccessCheckPath}
                        placeholder="/team/readme.txt"
                        className="input-shell"
                      />
                      <Button
                        color="primary"
                        className="self-end rounded-lg"
                        onPress={handleCheckDirectoryAccess}
                        isLoading={accessCheckMutation.isPending}
                      >
                        检查权限
                      </Button>
                      <Button
                        variant="bordered"
                        className="self-end rounded-lg"
                        onPress={handleReportDirectoryAccess}
                        isLoading={accessReportMutation.isPending}
                      >
                        用户矩阵
                      </Button>
                      <Button
                        variant="bordered"
                        className="self-end rounded-lg"
                        onPress={handlePreviewDirectoryAccess}
                        isLoading={accessPreviewMutation.isPending}
                      >
                        预览变更
                      </Button>
                    </div>
                    <div className="mt-2 text-xs text-default-500">用户矩阵基于已保存配置；预览变更基于当前编辑但尚未保存的规则。</div>
                  </div>
                  {accessCheckMutation.data && (
                    <DirectoryAccessCheckResult result={accessCheckMutation.data} />
                  )}
                  {accessReportMutation.data && (
                    <DirectoryAccessReportResult
                      report={accessReportMutation.data}
                      onSaveReviewHistory={handleSaveDirectoryAccessReviewHistory}
                    />
                  )}
                  {accessPreviewMutation.data && (
                    <DirectoryAccessReportResult
                      report={accessPreviewMutation.data}
                      title="变更预览"
                      ariaLabel="目录权限变更预览"
                      onSaveReviewHistory={handleSaveDirectoryAccessReviewHistory}
                    />
                  )}
                  <DirectoryAccessReviewHistory
                    entries={directoryAccessReviewHistory}
                    onCopy={handleCopyDirectoryAccessReviewHistory}
                    onClear={handleClearDirectoryAccessReviewHistory}
                  />
                </div>
              </SettingsSection>

              <SettingsSection
                title="自动版本化"
                description="配置默认自动版本化规则；保存后会立即影响后续新写入文件的版本策略"
                icon={Folder}
              >
                <div className="space-y-4">
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">自动版本化后缀</label>
                    <textarea
                      aria-label="自动版本化后缀"
                      value={settings.versioningExtensions}
                      onChange={(event) => updateDirtySettings(s => ({ ...s, versioningExtensions: event.target.value }))}
                      rows={4}
                      className="input-shell w-full rounded-medium px-3 py-2 text-sm bg-transparent outline-none border border-transparent focus:border-accent-primary"
                    />
                    <p className="text-xs text-default-500 mt-1">每行一个后缀，例如 `.md`、`.txt`。</p>
                  </div>
                  <Divider className="bg-divider" />
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">自动版本化文件名</label>
                    <textarea
                      aria-label="自动版本化文件名"
                      value={settings.versioningFilenames}
                      onChange={(event) => updateDirtySettings(s => ({ ...s, versioningFilenames: event.target.value }))}
                      rows={4}
                      className="input-shell w-full rounded-medium px-3 py-2 text-sm bg-transparent outline-none border border-transparent focus:border-accent-primary"
                    />
                    <p className="text-xs text-default-500 mt-1">每行一个文件名，例如 `README`、`Dockerfile`。</p>
                  </div>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最大自动版本化文件大小"
                    description="超过该大小的文件默认不再自动创建历史版本"
                  >
                    <Input
                      aria-label="最大自动版本化文件大小"
                      value={settings.versioningMaxSize}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, versioningMaxSize: v }))}
                      className="w-32"
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>
            </div>
          </Tab>

          <Tab key="webdav" title="WebDAV">
            <div className="space-y-6 mt-6">
              {webdavCredentialsError && (
                <div className="flex items-start gap-3 rounded-lg border border-warning/30 bg-warning/5 px-4 py-3 text-sm text-foreground">
                  <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" />
                  <div className="flex-1">
                    <p className="font-medium">{webdavCredentialsErrorPresentation?.title}</p>
                    <p className="text-default-600">{webdavCredentialsErrorPresentation?.description}</p>
                  </div>
                  <Button
                    size="sm"
                    variant="bordered"
                    className="rounded-lg"
                    onPress={handleRefreshWebDAVCredentials}
                    isLoading={isRefetchingWebDAVCredentials}
                  >
                    重新加载凭据
                  </Button>
                </div>
              )}

              {/* WebDAV Credentials Card */}
              {webdavCredentials?.enabled && webdavCredentials?.auth_type === 'users' && (
                <SettingsSection
                  title="WebDAV 挂载信息"
                  description="使用 MnemoNAS 账号登录；普通用户会限制在自己的 home_dir，访客账号只读"
                  icon={Key}
                >
                  <div className="space-y-4">
                    <div className="p-4 rounded-lg bg-content2/50 border border-divider">
                      <div className="space-y-1.5">
                        <label className="text-xs text-default-500">WebDAV 地址</label>
                        <div className="flex items-center gap-2">
                          <Snippet
                            symbol=""
                            variant="flat"
                            className="flex-1"
                            classNames={{
                              base: "bg-content1 border border-divider",
                              pre: "font-mono text-sm",
                            }}
                            hideSymbol
                            hideCopyButton
                          >
                            {webdavUrl}
                          </Snippet>
                          <Button
                            isIconOnly
                            size="sm"
                            variant="flat"
                            aria-label="复制 WebDAV 地址"
                            onPress={() => handleCopy('url', webdavUrl)}
                          >
                            {copiedField === 'url' ? (
                              <CheckCircle2 size={16} className="text-success" />
                            ) : (
                              <Copy size={16} />
                            )}
                          </Button>
                        </div>
                      </div>
                    </div>

                    <div className="text-xs text-default-400">
                      挂载时输入当前 MnemoNAS 用户名和密码；管理员可访问全局目录。
                    </div>
                  </div>
                </SettingsSection>
              )}
              {webdavCredentials?.enabled && webdavCredentials?.auth_type === 'basic' && (
                <SettingsSection
                  title="WebDAV 访问凭据"
                  description="用于挂载当前运行中的 WebDAV 服务；保存成功后这里会显示最新的运行配置"
                  icon={Key}
                >
                  <div className="space-y-4">
                    <div className="p-4 rounded-lg bg-content2/50 border border-divider">
                      <div className="space-y-4">
                        {/* WebDAV URL */}
                        <div className="space-y-1.5">
                          <label className="text-xs text-default-500">WebDAV 地址</label>
                          <div className="flex items-center gap-2">
                            <Snippet
                              symbol=""
                              variant="flat"
                              className="flex-1"
                              classNames={{
                                base: "bg-content1 border border-divider",
                                pre: "font-mono text-sm",
                              }}
                              hideSymbol
                              hideCopyButton
                            >
                              {webdavUrl}
                            </Snippet>
                            <Button
                              isIconOnly
                              size="sm"
                              variant="flat"
                              aria-label="复制 WebDAV 地址"
                              onPress={() => handleCopy('url', webdavUrl)}
                            >
                              {copiedField === 'url' ? (
                                <CheckCircle2 size={16} className="text-success" />
                              ) : (
                                <Copy size={16} />
                              )}
                            </Button>
                          </div>
                        </div>

                        {/* Username */}
                        <div className="space-y-1.5">
                          <label className="text-xs text-default-500">用户名</label>
                          <div className="flex items-center gap-2">
                            <Snippet
                              symbol=""
                              variant="flat"
                              className="flex-1"
                              classNames={{
                                base: "bg-content1 border border-divider",
                                pre: "font-mono",
                              }}
                              hideSymbol
                              hideCopyButton
                            >
                              {webdavCredentials.username || 'admin'}
                            </Snippet>
                            <Button
                              isIconOnly
                              size="sm"
                              variant="flat"
                              aria-label="复制 WebDAV 用户名"
                              onPress={() => handleCopy('username', webdavCredentials.username || 'admin')}
                            >
                              {copiedField === 'username' ? (
                                <CheckCircle2 size={16} className="text-success" />
                              ) : (
                                <Copy size={16} />
                              )}
                            </Button>
                          </div>
                        </div>

                        {/* Password */}
                        <div className="space-y-1.5">
                          <label className="text-xs text-default-500">密码</label>
                          <div className="flex items-center gap-2">
                            <Snippet
                              symbol=""
                              variant="flat"
                              className="flex-1"
                              classNames={{
                                base: "bg-content1 border border-divider",
                                pre: "font-mono",
                              }}
                              hideSymbol
                              hideCopyButton
                            >
                              {showWebDAVPassword
                                ? (webdavCredentials.password || '已设置（不可读取）')
                                : '••••••••••••••••'}
                            </Snippet>
                            <Button
                              isIconOnly
                              size="sm"
                              variant="flat"
                              aria-label={showWebDAVPassword ? '隐藏 WebDAV 密码' : '显示 WebDAV 密码'}
                              onPress={() => setShowWebDAVPassword(!showWebDAVPassword)}
                            >
                              {showWebDAVPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                            </Button>
                            <Button
                              isIconOnly
                              size="sm"
                              variant="flat"
                              aria-label="复制 WebDAV 密码"
                              onPress={() => handleCopy('password', webdavCredentials.password || '')}
                              isDisabled={!webdavCredentials.password}
                            >
                              {copiedField === 'password' ? (
                                <CheckCircle2 size={16} className="text-success" />
                              ) : (
                                <Copy size={16} />
                              )}
                            </Button>
                          </div>
                        </div>
                      </div>
                    </div>

                    <div className="text-xs text-default-400">
                      使用以上凭据在文件管理器中挂载 WebDAV 网络驱动器。
                      Windows: 映射网络驱动器 | macOS: 前往 → 连接服务器
                    </div>
                  </div>
                </SettingsSection>
              )}

              <SettingsSection
                title="WebDAV 服务"
                description="配置 WebDAV 协议接入；保存后会立即更新运行中的 WebDAV 配置"
                icon={Globe}
              >
                <div className="space-y-4">
                  {webdavRuntimeUnavailable && (
                    <div className="flex items-start gap-3 rounded-lg border border-warning/30 bg-warning/5 px-4 py-3 text-sm text-foreground">
                      <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" />
                      <div>
                        <div className="font-medium text-foreground">WebDAV 运行态当前不可用</div>
                        <div className="text-default-600">
                          配置已启用，但运行中的 WebDAV 服务未成功启动；请检查自动生成凭据和内部存储状态。
                        </div>
                      </div>
                    </div>
                  )}
                  <SettingRow
                    label="启用 WebDAV"
                    description="允许通过 WebDAV 协议访问文件"
                  >
                    <Switch
                      aria-label="启用 WebDAV"
                      isSelected={settings.webdavEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, webdavEnabled: v }))}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                        label: "sr-only",
                      }}
                    >
                      启用 WebDAV
                    </Switch>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="URL 前缀"
                    description="WebDAV 挂载点路径"
                  >
                    <Input
                      aria-label="WebDAV URL 前缀"
                      value={settings.webdavPrefix}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, webdavPrefix: v }))}
                      className="w-32"
                      isInvalid={settings.webdavEnabled && Boolean(webDAVPrefixErrorMessage)}
                      errorMessage={settings.webdavEnabled ? webDAVPrefixErrorMessage : undefined}
                      isDisabled={!settings.webdavEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="只读模式"
                    description="启用后仅允许读取操作"
                  >
                    <Switch
                      aria-label="WebDAV 只读模式"
                      isSelected={settings.webdavReadOnly}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, webdavReadOnly: v }))}
                      isDisabled={!settings.webdavEnabled}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                        label: "sr-only",
                      }}
                    >
                      WebDAV 只读模式
                    </Switch>
                  </SettingRow>
                </div>
              </SettingsSection>

              <SettingsSection
                title="WebDAV 认证"
                description="配置访问凭据；保存后会立即作用到运行中的 WebDAV 服务"
                icon={Shield}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="认证方式"
                    description="选择 WebDAV 访问所需的认证方式"
                  >
                    <select
                      value={settings.webdavAuthType}
                      onChange={(event) => updateDirtySettings((current) => ({
                        ...current,
                        webdavAuthType: event.target.value as WebDAVAuthType,
                      }))}
                      disabled={!settings.webdavEnabled}
                      className="input-shell h-9 rounded-lg px-3 text-sm bg-content1 border border-divider min-w-[160px]"
                      aria-label="WebDAV 认证方式"
                    >
                      <option value="users">MnemoNAS 用户账号</option>
                      <option value="basic">Basic Auth</option>
                      <option value="none">无认证</option>
                    </select>
                  </SettingRow>
                  {webdavNoAuthSelected && (
                    <>
                      <Divider className="bg-divider" />
                      <div
                        className={cn(
                          "flex items-start gap-3 rounded-lg px-4 py-3 text-sm text-foreground",
                          serverBeyondLoopback
                            ? "border border-danger/30 bg-danger/5"
                            : "border border-warning/30 bg-warning/5"
                        )}
                      >
                        <AlertCircle
                          size={18}
                          className={cn(
                            "mt-0.5 shrink-0",
                            serverBeyondLoopback ? "text-danger" : "text-warning"
                          )}
                        />
                        <div>
                          <div className="font-medium text-foreground">
                            {serverBeyondLoopback ? 'WebDAV 当前将无认证开放' : 'WebDAV 无认证仅适合本机或可信网络'}
                          </div>
                          <div className="text-default-600">
                            {serverBeyondLoopback
                              ? '当前监听地址不是 loopback，保存后任何能访问该端口的人都可以读写 WebDAV。建议改用 Basic Auth，或先把监听地址/端口限制到可信网络。'
                              : '当前监听地址限制在本机；只有在反向代理、隧道或防火墙已提供外层认证时才建议保持无认证。'}
                          </div>
                        </div>
                      </div>
                    </>
                  )}
                  <Divider className="bg-divider" />
                  <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">用户名</label>
                      <Input
                        aria-label="WebDAV 用户名"
                        placeholder="admin"
                        value={settings.webdavUsername}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, webdavUsername: v }))}
                        isDisabled={!settings.webdavEnabled || settings.webdavAuthType !== 'basic'}
                        startContent={<User size={16} className="text-default-500" />}
                        classNames={{
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                        }}
                      />
                    </div>
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">密码</label>
                      <Input
                        type="password"
                        aria-label="WebDAV 密码"
                        placeholder="••••••••"
                        value={settings.webdavPassword}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, webdavPassword: v, webdavUseGeneratedPassword: false }))}
                        isDisabled={!settings.webdavEnabled || settings.webdavAuthType !== 'basic' || settings.webdavUseGeneratedPassword}
                        startContent={<Lock size={16} className="text-default-500" />}
                        description={settings.webdavUseGeneratedPassword ? '保存后使用 secrets.json 中的自动生成密码' : '留空保留当前密码；勾选下方选项可切回自动生成密码'}
                        classNames={{
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                        }}
                      />
                    </div>
                  </div>
                  <Checkbox
                    isSelected={settings.webdavUseGeneratedPassword}
                    onValueChange={(value) => updateDirtySettings(s => ({
                      ...s,
                      webdavUseGeneratedPassword: value,
                      webdavPassword: value ? '' : s.webdavPassword,
                    }))}
                    isDisabled={!settings.webdavEnabled || settings.webdavAuthType !== 'basic'}
                  >
                    保存时使用自动生成密码
                  </Checkbox>
                </div>
              </SettingsSection>
            </div>
          </Tab>

          <Tab key="advanced" title="高级">
            <div className="space-y-6 mt-6">
              <SettingsSection
                title="CDC 分块参数"
                description="配置 dataplane 文件分块 API；保存后需重启数据面服务"
                icon={Zap}
              >
                <div className="space-y-4">
                  <div className="p-4 rounded-lg bg-content2 border border-divider">
                    <div className="flex items-start gap-3">
                      <div className="w-8 h-8 rounded-lg bg-accent-primary/15 flex items-center justify-center shrink-0 mt-0.5">
                        <HardDrive size={16} className="text-accent-primary" />
                      </div>
                      <div>
                        <div className="text-sm font-medium text-foreground">关于 CDC 分块</div>
                        <div className="text-xs text-default-500 mt-1 leading-relaxed">
                          dataplane 文件 API 会按内容边界切分文件。
                          当前版本历史路径仍使用整对象 CAS；这些参数会影响接入该 API 的新写入。
                        </div>
                      </div>
                    </div>
                  </div>

                  <SettingRow
                    label="最小块大小"
                    description="分块的最小尺寸"
                  >
                    <Input
                      aria-label="最小块大小"
                      value={settings.minChunkSize}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, minChunkSize: v }))}
                      className="w-24"
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="平均块大小"
                    description="分块的目标平均尺寸"
                  >
                    <Input
                      aria-label="平均块大小"
                      value={settings.avgChunkSize}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, avgChunkSize: v }))}
                      className="w-24"
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最大块大小"
                    description="分块的最大尺寸"
                  >
                    <Input
                      aria-label="最大块大小"
                      value={settings.maxChunkSize}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, maxChunkSize: v }))}
                      className="w-24"
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>

              <SettingsSection
                title="数据面连接"
                description="配置与 Rust 数据面的 gRPC 连接；地址变更会立即校验并切换，超时与重试设置用于后续连接建立"
                icon={Zap}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="gRPC 地址"
                    description="Rust 数据面服务地址"
                  >
                    <Input
                      aria-label="数据面 gRPC 地址"
                      value={settings.dataplaneGrpcAddress}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, dataplaneGrpcAddress: v }))}
                      className="w-56"
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="连接超时"
                    description="gRPC 调用超时时间"
                  >
                    <Input
                      aria-label="数据面连接超时"
                      value={settings.dataplaneTimeout}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, dataplaneTimeout: v }))}
                      placeholder="30s"
                      className="w-32"
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最大重试次数"
                    description="失败后重试次数"
                  >
                    <Input
                      aria-label="数据面最大重试次数"
                      type="number"
                      min={0}
                      step={1}
                      inputMode="numeric"
                      value={settings.dataplaneMaxRetries}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, dataplaneMaxRetries: v }))}
                      className="w-24"
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>

              <SettingsSection
                title="磁盘健康监控"
                description="配置 smartctl 周期探测；保存后立即更新运行中的磁盘健康监控"
                icon={HardDrive}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="启用磁盘健康检查"
                    description="启用后按周期检查已配置设备的 SMART、温度和介质健康状态"
                  >
                    <Switch
                      aria-label="启用磁盘健康检查"
                      isSelected={settings.diskHealthEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, diskHealthEnabled: v }))}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                        label: "sr-only",
                      }}
                    >
                      启用磁盘健康检查
                    </Switch>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
                    <Input
                      label="检查间隔"
                      aria-label="磁盘健康检查间隔"
                      value={settings.diskHealthCheckInterval}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, diskHealthCheckInterval: v }))}
                      placeholder="1h"
                      isDisabled={!settings.diskHealthEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                    <Input
                      label="探测超时"
                      aria-label="磁盘健康探测超时"
                      value={settings.diskHealthProbeTimeout}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, diskHealthProbeTimeout: v }))}
                      placeholder="15s"
                      isDisabled={!settings.diskHealthEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                    <Input
                      label="磁盘健康冷却时间"
                      aria-label="磁盘健康冷却时间"
                      value={settings.diskHealthCooldownPeriod}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, diskHealthCooldownPeriod: v }))}
                      placeholder="4h"
                      isDisabled={!settings.diskHealthEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                    <Input
                      label="探测命令"
                      aria-label="磁盘健康探测命令"
                      value={settings.diskHealthCommand}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, diskHealthCommand: v }))}
                      placeholder="smartctl"
                      isDisabled={!settings.diskHealthEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                    <Input
                      label="温度提醒阈值 (C)"
                      aria-label="磁盘温度提醒阈值"
                      type="number"
                      min={0}
                      inputMode="numeric"
                      value={settings.diskHealthTemperatureWarningC}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, diskHealthTemperatureWarningC: v }))}
                      isDisabled={!settings.diskHealthEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                    <Input
                      label="温度严重阈值 (C)"
                      aria-label="磁盘温度严重阈值"
                      type="number"
                      min={0}
                      inputMode="numeric"
                      value={settings.diskHealthTemperatureCriticalC}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, diskHealthTemperatureCriticalC: v }))}
                      isDisabled={!settings.diskHealthEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                    <Input
                      label="介质磨损提醒 (%)"
                      aria-label="介质磨损提醒阈值"
                      type="number"
                      min={0}
                      max={100}
                      step={1}
                      inputMode="numeric"
                      value={settings.diskHealthMediaWearWarningPct}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, diskHealthMediaWearWarningPct: v }))}
                      isDisabled={!settings.diskHealthEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                    <Input
                      label="介质磨损严重 (%)"
                      aria-label="介质磨损严重阈值"
                      type="number"
                      min={0}
                      max={100}
                      step={1}
                      inputMode="numeric"
                      value={settings.diskHealthMediaWearCriticalPct}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, diskHealthMediaWearCriticalPct: v }))}
                      isDisabled={!settings.diskHealthEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </div>
                  <Divider className="bg-divider" />
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">设备列表</label>
                    <textarea
                      aria-label="磁盘健康设备列表"
                      value={settings.diskHealthDevices}
                      onChange={(event) => updateDirtySettings(s => ({ ...s, diskHealthDevices: event.target.value }))}
                      disabled={!settings.diskHealthEnabled}
                      placeholder={"/dev/disk/by-id/ata-data | Data | sat | SER123 | 45 | 55"}
                      rows={4}
                      className={cn(
                        "input-shell w-full rounded-medium px-3 py-2 text-sm bg-transparent outline-none",
                        "border border-transparent focus:border-accent-primary",
                        !settings.diskHealthEnabled && "opacity-60 cursor-not-allowed"
                      )}
                    />
                    <p className="text-xs text-default-500 mt-1">
                      每行格式为：设备路径 | 名称 | 类型 | 期望序列号 | 温度提醒阈值 | 温度严重阈值。后五列可留空。
                    </p>
                  </div>
                </div>
              </SettingsSection>

              <SettingsSection
                title="数据巡检计划"
                description="配置后台周期 Scrub；保存后立即更新调度，失败会按重试策略进入提醒和设备状态"
                icon={Clock}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="启用周期 Scrub"
                    description="启用后按计划校验 CAS 对象完整性"
                  >
                    <Switch
                      aria-label="启用周期 Scrub"
                      isSelected={settings.scrubScheduleEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, scrubScheduleEnabled: v }))}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                        label: "sr-only",
                      }}
                    >
                      启用周期 Scrub
                    </Switch>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="常规间隔"
                    description="两次计划巡检之间的间隔"
                  >
                    <Input
                      aria-label="Scrub 常规间隔"
                      value={settings.scrubScheduleInterval}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, scrubScheduleInterval: v }))}
                      placeholder="168h"
                      className="w-32"
                      isDisabled={!settings.scrubScheduleEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="失败重试间隔"
                    description="巡检失败后等待多久再自动重试"
                  >
                    <Input
                      aria-label="Scrub 失败重试间隔"
                      value={settings.scrubRetryInterval}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, scrubRetryInterval: v }))}
                      placeholder="1h"
                      className="w-32"
                      isDisabled={!settings.scrubScheduleEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最大重试次数"
                    description="单次失败后最多自动重试次数；0 表示不自动重试"
                  >
                    <Input
                      aria-label="Scrub 最大重试次数"
                      type="number"
                      min={0}
                      step={1}
                      inputMode="numeric"
                      value={settings.scrubMaxRetries}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, scrubMaxRetries: v }))}
                      className="w-24"
                      isDisabled={!settings.scrubScheduleEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>

              <SettingsSection
                title="提醒通知"
                description="配置磁盘空间、备份事件和外部通知；保存后立即更新运行态"
                icon={AlertCircle}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="启用提醒"
                    description="启用后定期检查存储空间，并通过已配置通道发送备份、恢复、磁盘健康、Scrub 和登录限流事件通知"
                  >
                    <Switch
                      aria-label="启用提醒"
                      isSelected={settings.alertsEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsEnabled: v }))}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                        label: "sr-only",
                      }}
                    >
                      启用提醒
                    </Switch>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="测试提醒"
                    description="使用已保存的提醒配置发送一次测试事件"
                  >
                    <Button
                      variant="bordered"
                      className="btn-secondary btn-md rounded-lg"
                      startContent={<Send size={16} />}
                      onPress={handleSendTestAlert}
                      isLoading={testAlertMutation.isPending}
                      isDisabled={saveMutation.isPending}
                    >
                      发送测试提醒
                    </Button>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="检查间隔"
                    description="磁盘空间检查频率"
                  >
                    <Input
                      aria-label="提醒检查间隔"
                      value={settings.alertsCheckInterval}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsCheckInterval: v }))}
                      placeholder="1h"
                      className="w-32"
                      isDisabled={!settings.alertsEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">提醒阈值 (%)</label>
                      <Input
                        aria-label="提醒阈值"
                        type="number"
                        min={0}
                        max={100}
                        step={1}
                        inputMode="numeric"
                        value={settings.alertsThresholdPct}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsThresholdPct: v }))}
                        isDisabled={!settings.alertsEnabled}
                        classNames={{ inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary" }}
                      />
                    </div>
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">严重提醒阈值 (%)</label>
                      <Input
                        aria-label="严重提醒阈值"
                        type="number"
                        min={0}
                        max={100}
                        step={1}
                        inputMode="numeric"
                        value={settings.alertsCriticalPct}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsCriticalPct: v }))}
                        isDisabled={!settings.alertsEnabled}
                        classNames={{ inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary" }}
                      />
                    </div>
                  </div>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最小剩余空间"
                    description="剩余空间低于该值时发送提醒"
                  >
                    <Input
                      aria-label="最小剩余空间"
                      value={settings.alertsMinFreeSpace}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsMinFreeSpace: v }))}
                      className="w-32"
                      isDisabled={!settings.alertsEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="冷却时间"
                    description="同级别连续提醒之间的最小间隔"
                  >
                    <Input
                      aria-label="提醒冷却时间"
                      value={settings.alertsCooldownPeriod}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsCooldownPeriod: v }))}
                      placeholder="4h"
                      className="w-32"
                      isDisabled={!settings.alertsEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="Webhook URL"
                    description="发送磁盘空间和备份事件通知的目标地址"
                  >
                    <Input
                      type="url"
                      aria-label="Webhook URL"
                      value={settings.alertsWebhookURL}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsWebhookURL: v }))}
                      placeholder="https://hooks.example.com/alert"
                      isDisabled={!settings.alertsEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="Webhook 方法"
                    description="通知请求使用的 HTTP 方法；GET 会把事件字段编码到 URL query"
                  >
                    <select
                      aria-label="Webhook 方法"
                      value={settings.alertsWebhookMethod}
                      onChange={(event) => updateDirtySettings(s => ({ ...s, alertsWebhookMethod: event.target.value }))}
                      disabled={!settings.alertsEnabled}
                      className="input-shell min-w-[8rem] px-3 py-2 text-sm bg-transparent outline-none"
                    >
                      <option value="POST">POST</option>
                      <option value="GET">GET</option>
                    </select>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">自定义 Header</label>
                    <textarea
                      aria-label="Webhook 自定义 Header"
                      value={settings.alertsWebhookHeaders}
                      onChange={(event) => updateDirtySettings(s => ({ ...s, alertsWebhookHeaders: event.target.value }))}
                      disabled={!settings.alertsEnabled}
                      placeholder={"Authorization: Bearer token\nX-MnemoNAS: alerts"}
                      rows={3}
                      className={cn(
                        "input-shell w-full rounded-medium px-3 py-2 text-sm bg-transparent outline-none",
                        "border border-transparent focus:border-accent-primary",
                        !settings.alertsEnabled && "opacity-60 cursor-not-allowed"
                      )}
                    />
                    <p className="text-xs text-default-500 mt-1">每行一个 Header，使用 Key:Value 格式。</p>
                  </div>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="企业微信通知"
                    description="将同一批提醒事件发送到企业微信/WeCom 群机器人"
                  >
                    <Switch
                      aria-label="启用企业微信通知"
                      isSelected={settings.alertsWeComEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsWeComEnabled: v }))}
                      isDisabled={!settings.alertsEnabled}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                        label: "sr-only",
                      }}
                    >
                      启用企业微信通知
                    </Switch>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="企业微信 Webhook URL"
                    description="企业微信群机器人 Webhook 地址；保存后不会回显完整地址"
                  >
                    <Input
                      type="url"
                      aria-label="企业微信 Webhook URL"
                      value={settings.alertsWeComWebhookURL}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsWeComWebhookURL: v }))}
                      placeholder="https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=..."
                      isDisabled={!settings.alertsEnabled || !settings.alertsWeComEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="钉钉通知"
                    description="将同一批提醒事件发送到钉钉群机器人"
                  >
                    <Switch
                      aria-label="启用钉钉通知"
                      isSelected={settings.alertsDingTalkEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsDingTalkEnabled: v }))}
                      isDisabled={!settings.alertsEnabled}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                        label: "sr-only",
                      }}
                    >
                      启用钉钉通知
                    </Switch>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="钉钉 Webhook URL"
                    description="钉钉群机器人 Webhook 地址；保存后不会回显完整地址"
                  >
                    <Input
                      type="url"
                      aria-label="钉钉 Webhook URL"
                      value={settings.alertsDingTalkWebhookURL}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsDingTalkWebhookURL: v }))}
                      placeholder="https://oapi.dingtalk.com/robot/send?access_token=..."
                      isDisabled={!settings.alertsEnabled || !settings.alertsDingTalkEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="邮件通知"
                    description="将同一批提醒事件发送到 SMTP 收件人"
                  >
                    <Switch
                      aria-label="启用邮件通知"
                      isSelected={settings.alertsEmailEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsEmailEnabled: v }))}
                      isDisabled={!settings.alertsEnabled}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                        label: "sr-only",
                      }}
                    >
                      启用邮件通知
                    </Switch>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
                    <Input
                      label="SMTP 主机"
                      aria-label="SMTP 主机"
                      value={settings.alertsSMTPHost}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsSMTPHost: v }))}
                      placeholder="smtp.example.com"
                      isDisabled={!settings.alertsEnabled || !settings.alertsEmailEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                    <Input
                      label="SMTP 端口"
                      aria-label="SMTP 端口"
                      type="number"
                      min={1}
                      max={65535}
                      inputMode="numeric"
                      value={settings.alertsSMTPPort}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsSMTPPort: v }))}
                      placeholder="587"
                      isDisabled={!settings.alertsEnabled || !settings.alertsEmailEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                    <Input
                      label="SMTP 用户名"
                      aria-label="SMTP 用户名"
                      value={settings.alertsSMTPUsername}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsSMTPUsername: v }))}
                      placeholder="alerts@example.com"
                      isDisabled={!settings.alertsEnabled || !settings.alertsEmailEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                    <div className="space-y-2">
                      <Input
                        label="SMTP 密码"
                        type="password"
                        aria-label="SMTP 密码"
                        value={settings.alertsSMTPPassword}
                        onValueChange={(v) => updateDirtySettings(s => ({
                          ...s,
                          alertsSMTPPassword: v,
                          alertsSMTPPasswordClear: false,
                        }))}
                        placeholder={settings.alertsSMTPPasswordConfigured ? '已配置，留空不变' : '应用专用密码'}
                        isDisabled={!settings.alertsEnabled || !settings.alertsEmailEnabled || settings.alertsSMTPPasswordClear}
                        classNames={{
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                        }}
                      />
                      {settings.alertsSMTPPasswordConfigured && (
                        <Checkbox
                          isSelected={settings.alertsSMTPPasswordClear}
                          onValueChange={(value) => updateDirtySettings(s => ({
                            ...s,
                            alertsSMTPPasswordClear: value,
                            alertsSMTPPassword: value ? '' : s.alertsSMTPPassword,
                          }))}
                          classNames={{ label: "text-xs text-default-600" }}
                        >
                          保存时清除已保存 SMTP 密码
                        </Checkbox>
                      )}
                    </div>
                    <Input
                      label="SMTP 发件人"
                      aria-label="SMTP 发件人"
                      value={settings.alertsSMTPFrom}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsSMTPFrom: v }))}
                      placeholder="MnemoNAS <alerts@example.com>"
                      isDisabled={!settings.alertsEnabled || !settings.alertsEmailEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                    <div className="lg:col-span-2">
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">SMTP 收件人</label>
                      <textarea
                        aria-label="SMTP 收件人"
                        value={settings.alertsSMTPTo}
                        onChange={(event) => updateDirtySettings(s => ({ ...s, alertsSMTPTo: event.target.value }))}
                        disabled={!settings.alertsEnabled || !settings.alertsEmailEnabled}
                        placeholder={"admin@example.com\nops@example.com"}
                        rows={3}
                        className={cn(
                          "input-shell w-full rounded-medium px-3 py-2 text-sm bg-transparent outline-none",
                          "border border-transparent focus:border-accent-primary",
                          (!settings.alertsEnabled || !settings.alertsEmailEnabled) && "opacity-60 cursor-not-allowed"
                        )}
                      />
                      <p className="text-xs text-default-500 mt-1">每行一个收件人，也支持用逗号分隔；启用邮件通知时至少需要一个非空收件人。</p>
                    </div>
                  </div>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="Telegram 通知"
                    description="将同一批提醒事件发送到 Telegram 机器人"
                  >
                    <Switch
                      aria-label="启用 Telegram 通知"
                      isSelected={settings.alertsTelegramEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsTelegramEnabled: v }))}
                      isDisabled={!settings.alertsEnabled}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                        label: "sr-only",
                      }}
                    >
                      启用 Telegram 通知
                    </Switch>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="Telegram Bot Token"
                    description={settings.alertsTelegramBotTokenConfigured ? '留空会保留现有 Token；勾选清除或填写后覆盖' : '从 BotFather 获取的机器人 Token'}
                  >
                    <div className="space-y-2 sm:w-72">
                      <Input
                        type="password"
                        aria-label="Telegram Bot Token"
                        value={settings.alertsTelegramBotToken}
                        onValueChange={(v) => updateDirtySettings(s => ({
                          ...s,
                          alertsTelegramBotToken: v,
                          alertsTelegramBotTokenClear: false,
                        }))}
                        placeholder={settings.alertsTelegramBotTokenConfigured ? '已配置，留空不变' : '123456:ABC...'}
                        isDisabled={!settings.alertsEnabled || !settings.alertsTelegramEnabled || settings.alertsTelegramBotTokenClear}
                        classNames={{
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                        }}
                      />
                      {settings.alertsTelegramBotTokenConfigured && (
                        <Checkbox
                          isSelected={settings.alertsTelegramBotTokenClear}
                          onValueChange={(value) => updateDirtySettings(s => ({
                            ...s,
                            alertsTelegramBotTokenClear: value,
                            alertsTelegramBotToken: value ? '' : s.alertsTelegramBotToken,
                          }))}
                          classNames={{ label: "text-xs text-default-600" }}
                        >
                          保存时清除已保存 Telegram Token
                        </Checkbox>
                      )}
                    </div>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="Telegram Chat ID"
                    description="支持数字 Chat ID 或 @channel 用户名"
                  >
                    <Input
                      aria-label="Telegram Chat ID"
                      value={settings.alertsTelegramChatID}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsTelegramChatID: v }))}
                      placeholder="-1001234567890"
                      isDisabled={!settings.alertsEnabled || !settings.alertsTelegramEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>

              <SettingsSection
                title="收藏功能"
                description="控制文件收藏能力；关闭后收藏接口会立即拒绝请求"
                icon={Star}
              >
                <div className="space-y-4">
                  {favoritesRuntimeUnavailable && (
                    <div className="flex items-start gap-3 rounded-lg border border-warning/30 bg-warning/5 px-4 py-3 text-sm text-foreground">
                      <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" />
                      <div>
                        <div className="font-medium text-foreground">收藏运行态当前不可用</div>
                        <div className="text-default-600">
                          配置已启用，但运行中的收藏存储未就绪；收藏接口会返回不可用，直到服务恢复对收藏存储的访问。
                        </div>
                      </div>
                    </div>
                  )}
                  <SettingRow
                    label="启用收藏功能"
                    description="允许标记收藏、查询收藏状态和维护收藏备注"
                  >
                    <Switch
                      aria-label="启用收藏功能"
                      isSelected={settings.favoritesEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, favoritesEnabled: v }))}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                        label: "sr-only",
                      }}
                    >
                      启用收藏功能
                    </Switch>
                  </SettingRow>
                </div>
              </SettingsSection>
            </div>
          </Tab>

          <Tab key="shares" title="分享">
            <div className="space-y-6 mt-6">
              <SettingsSection
                title="分享功能配置"
                description="控制分享链接功能与默认基础地址；关闭后公开访问会立即失效"
                icon={Link2}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="启用分享功能"
                    description="允许创建和访问公开分享链接"
                  >
                    <Switch
                      aria-label="启用分享功能"
                      isSelected={settings.shareEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, shareEnabled: v }))}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                        label: "sr-only",
                      }}
                    >
                      启用分享功能
                    </Switch>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="分享基础 URL"
                    description="用于生成完整分享链接，可留空使用当前访问地址；保存后会立即影响新创建的分享"
                  >
                    <Input
                      type="url"
                      aria-label="分享基础 URL"
                      value={settings.shareBaseURL}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, shareBaseURL: v }))}
                      placeholder="https://nas.example.com"
                      description={shareBaseURLReviewMessage}
                      color={shareBaseURLReviewMessage ? 'warning' : 'default'}
                      isDisabled={!settings.shareEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                        description: "text-warning",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="新分享策略预设"
                    description="选择后会填入默认有效期和访问次数，可继续手动调整"
                  >
                    <div className="grid w-full gap-2 sm:grid-cols-3">
                      {SHARE_POLICY_PRESETS.map((preset) => {
                        const selected = settings.shareDefaultExpiresIn === preset.defaultExpiresIn
                          && settings.shareDefaultMaxAccess === preset.defaultMaxAccess
                        return (
                          <Button
                            key={preset.key}
                            variant={selected ? 'solid' : 'flat'}
                            color={selected ? 'primary' : 'default'}
                            size="sm"
                            className="h-auto min-h-12 justify-start rounded-lg px-3 py-2"
                            isDisabled={!settings.shareEnabled}
                            onPress={() => updateDirtySettings(s => ({
                              ...s,
                              shareDefaultExpiresIn: preset.defaultExpiresIn,
                              shareDefaultMaxAccess: preset.defaultMaxAccess,
                            }))}
                          >
                            <span className="flex min-w-0 flex-col items-start text-left">
                              <span className="font-medium">{preset.label}</span>
                              <span className="text-xs opacity-75">{preset.description}</span>
                            </span>
                          </Button>
                        )
                      })}
                    </div>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="新分享默认有效期"
                    description="例如 168h；留空或填 0 表示新分享默认不过期"
                  >
                    <Input
                      aria-label="新分享默认有效期"
                      value={settings.shareDefaultExpiresIn}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, shareDefaultExpiresIn: v }))}
                      placeholder="168h"
                      isDisabled={!settings.shareEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="新分享默认访问次数"
                    description="0 表示不限制；只影响之后创建的分享链接"
                  >
                    <Input
                      type="text"
                      aria-label="新分享默认访问次数"
                      inputMode="numeric"
                      pattern="[0-9]*"
                      value={settings.shareDefaultMaxAccess}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, shareDefaultMaxAccess: v }))}
                      placeholder="0"
                      isDisabled={!settings.shareEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="路径分享策略"
                    description="为指定目录设置更严格的分享约束和允许创建者范围；更深的路径优先生效"
                  >
                    <div>
                      <SharePolicyRuleEditor
                        rules={settings.sharePolicyRules}
                        isDisabled={!settings.shareEnabled}
                        onChange={(nextRules) => updateDirtySettings(s => ({ ...s, sharePolicyRules: nextRules }))}
                      />
                      <div className="mt-2 text-xs text-default-500">
                        分享策略路径填写 MnemoNAS 逻辑路径；包含空格或双引号时不需要额外加引号
                      </div>
                    </div>
                  </SettingRow>
                  <SharePolicyChangeReview
                    saved={savedSharePolicyReviewInput}
                    draft={draftSharePolicyReviewInput}
                  />
                  <SharePolicyCoverageSummary draft={draftSharePolicyReviewInput} />
                </div>
              </SettingsSection>

              <SettingsSection
                title="分享链接"
                description="查看和处理已创建的分享链接"
                icon={Link2}
              >
                <ShareManager
                  featureEnabled={settings.shareEnabled}
                  initialReviewFilter={shareReviewFilter}
                  pathFilter={sharePathFilter}
                  onClearPathFilter={handleClearSharePathFilter}
                />
              </SettingsSection>
            </div>
          </Tab>
        </Tabs>
      </div>
    </div>
  )
}
