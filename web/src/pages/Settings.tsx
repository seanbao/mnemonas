import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
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
  Trash2,
  ChevronLeft,
  ChevronDown,
} from 'lucide-react'
import { cn, copyTextToClipboard, parseByteSize, normalizeWebDAVPrefix, isValidWebDAVPrefix, webDAVPrefixOverlapsReservedRoute, formatWebDAVUrl, formatBytes, hasControlCharacter } from '@/lib/utils'
import { GENERIC_LOAD_ERROR_DESCRIPTION, getUserFacingErrorDescription } from '@/lib/apiMessages'
import { getRedactedDiagnosticMessage } from '@/lib/diagnosticMessages'
import { ShareManager, normalizeShareReviewFilter } from '@/components/share'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'
import { SettingsOverview, type SettingsDestination } from '@/components/settings/SettingsOverview'
import { normalizeLogicalPathInput, parseAccessRuleValues } from '@/components/users/userAccessDraft'
import { useAuthStore, useUser } from '@/stores/auth'
import { useSettingsDraftStore } from '@/stores/settingsDraft'
import {
  MAX_VERSIONED_FILE_SIZE_BYTES,
  SettingsError,
  getSecurityCheck,
  getSettings,
  getWebDAVCredentials,
  updateSettings,
  type DirectoryAccessRole,
  type SecurityCheckData,
  type SecurityCheckItem,
  type SecurityCheckStatus,
  type SharePolicyRule,
  type UpdateSettingsRequest,
} from '@/api/settings'

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
const BYTE_SIZE_FORMAT_ERROR_DESCRIPTION = '请使用 1024、1 KB、1.5 MB 之类的格式。'

const getNonBlankToastDescription = getRedactedDiagnosticMessage

function redactSecurityActionToastDescription(description: string): string {
  return getRedactedDiagnosticMessage(description) ?? description
}

const SHARE_POLICY_PRESETS = [
  {
    key: 'family',
    label: '家庭默认',
    description: '7 天有效，最多 20 次下载',
    defaultExpiresIn: '168h',
    defaultMaxAccess: '20',
  },
  {
    key: 'temporary',
    label: '临时协作',
    description: '3 天有效，最多 20 次下载',
    defaultExpiresIn: '72h',
    defaultMaxAccess: '20',
  },
  {
    key: 'public-info',
    label: '资料分发',
    description: '30 天有效，最多 100 次下载',
    defaultExpiresIn: '720h',
    defaultMaxAccess: '100',
  },
] as const

const PUBLIC_ACCESS_MAX_ACCESS_TOKEN_TTL_MS = 60 * 60 * 1000
const PUBLIC_ACCESS_MAX_REFRESH_TOKEN_TTL_MS = 720 * 60 * 60 * 1000
const PUBLIC_ACCESS_MAX_SHARE_DEFAULT_EXPIRES_MS = 720 * 60 * 60 * 1000
const PUBLIC_ACCESS_SNIPPET_COPY_BUTTON_CLASS = 'h-8 w-8 min-w-8 shrink-0'

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

function SettingsDisclosure({
  title,
  description,
  children,
}: {
  title: string
  description: string
  children: React.ReactNode
}) {
  return (
    <details className="group">
      <summary className="card-mnemonas flex cursor-pointer list-none items-center gap-4 p-4 marker:hidden focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/35 sm:p-5">
        <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
          <Zap size={19} aria-hidden="true" />
        </span>
        <span className="min-w-0 flex-1">
          <span className="block text-sm font-semibold text-foreground">{title}</span>
          <span className="mt-1 block text-xs leading-5 text-default-500">{description}</span>
        </span>
        <ChevronDown
          size={18}
          aria-hidden="true"
          className="shrink-0 text-default-500 transition-transform duration-200 group-open:rotate-180"
        />
      </summary>
      <div className="mt-6 space-y-6">
        {children}
      </div>
    </details>
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

const SETTINGS_TABS = ['overview', 'general', 'retention', 'webdav', 'shares'] as const

type SettingsTabKey = (typeof SETTINGS_TABS)[number]
type WebDAVAuthType = 'users' | 'basic' | 'none'

function isSettingsTabKey(value: string): value is SettingsTabKey {
  return SETTINGS_TABS.includes(value as SettingsTabKey)
}

function normalizeSettingsTab(value: string | null): SettingsTabKey {
  if (value && isSettingsTabKey(value)) {
    return value
  }

  return 'overview'
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

function dataplaneLoopbackAddressFromSecurityCheck(check: SecurityCheckItem): string {
  const detailAddress = typeof check.details?.grpc_address === 'string' ? check.details.grpc_address : ''
  return loopbackAddressWithOriginalPort(detailAddress, '127.0.0.1:9090')
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

function normalizeSharePolicyRulesForSave(inputRules: SharePolicyRuleDraft[]): { rules: SharePolicyRule[]; error?: string } {
  const rules: SharePolicyRule[] = []
  const seenPaths = new Set<string>()

  for (let index = 0; index < inputRules.length; index += 1) {
    const lineNumber = index + 1
    const inputRule = inputRules[index]
    const rulePath = normalizeLogicalPathInput(inputRule.path)
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
      return { rules: [], error: `第 ${lineNumber} 行下载次数上限必须是 0 或不超过安全范围的正整数` }
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
  { key: 'max_access', label: '最多下载次数' },
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
      label: '新分享默认下载次数',
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
    rule.max_access && rule.max_access > 0 ? `最多下载：${rule.max_access}` : '',
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

function sharePolicyRuleMaxExpiresMilliseconds(rule: SharePolicyRule): number | null {
  const value = rule.max_expires_in?.trim()
  if (!value) {
    return null
  }
  return durationStringToMilliseconds(value)
}

function sharePolicyRuleMaxAccessLimit(rule: SharePolicyRule): number | null {
  return rule.max_access && rule.max_access > 0 ? rule.max_access : null
}

function normalizedSharePolicyPrincipalSet(values: string[] | undefined): Set<string> {
  return new Set((values ?? [])
    .map((value) => value.trim().toLowerCase())
    .filter(Boolean))
}

function sharePolicyRulePrincipalScopeWithinAncestor(rule: SharePolicyRule, ancestor: SharePolicyRule): boolean {
  const principalFields: Array<keyof Pick<SharePolicyRule, 'allowed_users' | 'allowed_groups' | 'allowed_roles'>> = [
    'allowed_users',
    'allowed_groups',
    'allowed_roles',
  ]

  for (const field of principalFields) {
    const ruleValues = normalizedSharePolicyPrincipalSet(rule[field])
    if (ruleValues.size === 0) {
      continue
    }
    const ancestorValues = normalizedSharePolicyPrincipalSet(ancestor[field])
    if (ancestorValues.size === 0) {
      return false
    }
    for (const value of ruleValues) {
      if (!ancestorValues.has(value)) {
        return false
      }
    }
  }

  return true
}

const sharePolicyCleanupConstraintChecks: Array<{
  key: string
  label: string
  hasConstraint: (rule: SharePolicyRule) => boolean
}> = [
  { key: 'password', label: '强制密码约束', hasConstraint: sharePolicyRuleHasPasswordConstraint },
  { key: 'expires', label: '最长有效期约束', hasConstraint: sharePolicyRuleHasExpiresConstraint },
  { key: 'access', label: '下载次数约束', hasConstraint: sharePolicyRuleHasAccessConstraint },
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

    const expiresAncestor = ancestors.find(sharePolicyRuleHasExpiresConstraint)
    const ruleExpiresMilliseconds = sharePolicyRuleMaxExpiresMilliseconds(rule)
    const ancestorExpiresMilliseconds = expiresAncestor
      ? sharePolicyRuleMaxExpiresMilliseconds(expiresAncestor)
      : null
    if (
      expiresAncestor &&
      rule.max_expires_in &&
      expiresAncestor.max_expires_in &&
      ruleExpiresMilliseconds !== null &&
      ancestorExpiresMilliseconds !== null &&
      ruleExpiresMilliseconds > ancestorExpiresMilliseconds
    ) {
      insights.push({
        key: `weaker-expires:${rule.path}:${expiresAncestor.path}`,
        message: `${rule.path} 的最长有效期 ${rule.max_expires_in} 长于上级 ${expiresAncestor.path} 的 ${expiresAncestor.max_expires_in}。`,
        tone: 'warning',
      })
    }

    const accessAncestor = ancestors.find(sharePolicyRuleHasAccessConstraint)
    const ruleMaxAccess = sharePolicyRuleMaxAccessLimit(rule)
    const ancestorMaxAccess = accessAncestor ? sharePolicyRuleMaxAccessLimit(accessAncestor) : null
    if (
      accessAncestor &&
      ruleMaxAccess !== null &&
      ancestorMaxAccess !== null &&
      ruleMaxAccess > ancestorMaxAccess
    ) {
      insights.push({
        key: `weaker-access:${rule.path}:${accessAncestor.path}`,
        message: `${rule.path} 的下载次数 ${ruleMaxAccess} 高于上级 ${accessAncestor.path} 的 ${ancestorMaxAccess}。`,
        tone: 'warning',
      })
    }

    const principalAncestor = ancestors.find(sharePolicyRuleHasPrincipalConstraint)
    if (
      principalAncestor &&
      sharePolicyRuleHasPrincipalConstraint(rule) &&
      !sharePolicyRulePrincipalScopeWithinAncestor(rule, principalAncestor)
    ) {
      insights.push({
        key: `weaker-principal:${rule.path}:${principalAncestor.path}`,
        message: `${rule.path} 的允许创建者范围不在上级 ${principalAncestor.path} 的范围内；请确认是否需要放宽共享创建范围。`,
        tone: 'warning',
      })
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
      defaultMaxAccess === '' ? '新分享默认下载次数不限制，建议为公网分享设置默认下载次数。' : '',
      rules.length === 0 ? '尚未配置路径分享策略，所有路径只受全局默认值约束。' : '',
      rules.length > passwordRuleCount ? `${rules.length - passwordRuleCount} 条路径策略未强制密码。` : '',
      rules.length > expiresRuleCount ? `${rules.length - expiresRuleCount} 条路径策略未限制最长有效期。` : '',
      rules.length > accessRuleCount ? `${rules.length - accessRuleCount} 条路径策略未限制下载次数。` : '',
      rules.length > principalRuleCount ? `${rules.length - principalRuleCount} 条路径策略未限制允许创建者范围。` : '',
    ].filter(Boolean)
    : ['分享功能当前停用；重新启用前应复核默认有效期、下载次数和路径策略。']

  const summaryItems = [
    { label: '功能状态', value: draft.enabled ? '已启用' : '已停用', tone: draft.enabled ? 'warning' : 'success' },
    { label: '默认有效期', value: sharePolicyLimitValueLabel(defaultExpiresIn), tone: draft.enabled && defaultExpiresIn === '' ? 'warning' : 'success' },
    { label: '默认下载次数', value: sharePolicyLimitValueLabel(defaultMaxAccess), tone: draft.enabled && defaultMaxAccess === '' ? 'warning' : 'success' },
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
                    aria-label={`分享策略最多下载次数 ${index + 1}`}
                    label="最多下载次数"
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
        return '新分享默认有效期和默认下载次数处于公网部署建议范围内，或分享功能未启用。'
      }
      if (check.status === 'block') {
        return '分享默认有效期或默认下载次数配置无效，请修复负值后重新检查。'
      }
      if (check.details?.default_expires_in_unlimited === true
        && check.details?.default_max_access_unlimited === true) {
        return '分享功能已启用，但新分享默认不会过期且下载次数不限制；家庭公网分享建议同时设置默认有效期和默认下载次数。'
      }
      if (check.details?.default_expires_in_unlimited === true) {
        return '分享功能已启用，但新分享默认不会过期；家庭公网分享建议设置默认有效期，避免长期公开链接被遗忘。'
      }
      if (check.details?.default_max_access_unlimited === true) {
        return '分享功能已启用，但新分享默认下载次数不限制；家庭公网分享建议设置默认下载次数，避免公开链接被反复下载。'
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
            正在检查安全配置…
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
  const authEnabled = useAuthStore((state) => state.authEnabled)
  const setHasPendingSettingsChanges = useSettingsDraftStore((state) => state.setHasPendingChanges)
  const queryClient = useQueryClient()
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
    trashEnabled: true,
    trashRetentionDays: '30',
    trashMaxSize: '10 GB',
    maxVersions: '50',
    maxAge: '2160h',
    minFreeSpace: '10GB',
    gcInterval: '24h',
    versioningExtensions: DEFAULT_VERSIONING_EXTENSIONS,
    versioningFilenames: DEFAULT_VERSIONING_FILENAMES,
    versioningMaxSize: '100 MiB',
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
    scrubScheduleEnabled: false,
  }
  type SettingsDraft = typeof defaultSettings
  type SaveSettingsVariables = {
    request: UpdateSettingsRequest
    submittedSettings: SettingsDraft
    baseSettingsUpdatedAt: number
    invalidatesFileDeletionQueries: boolean
    signal: AbortSignal
  }
  const sanitizeSavedSettingsOverride = (settings: SettingsDraft): SettingsDraft => ({
      ...settings,
      webdavPassword: '',
      webdavUseGeneratedPassword: false,
    })

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
    return next
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
  const webdavUrl = useMemo(() => {
    return formatWebDAVUrl(window.location.origin, webdavCredentials?.url ?? '')
  }, [webdavCredentials?.url])

  const handleCopy = async (field: string, value: string) => {
    try {
      await copyTextToClipboard(value)
      setCopiedField(field)
      setTimeout(() => setCopiedField(null), 2000)
    } catch {
      addToast({ title: '复制失败', color: 'danger' })
    }
  }

  const saveSettingsAbortControllerRef = useRef<AbortController | null>(null)
  const [draftSettings, setDraftSettings] = useState(defaultSettings)
  const [isDirty, setIsDirty] = useState(false)
  const [savedSettingsOverride, setSavedSettingsOverride] = useState<typeof defaultSettings | null>(null)
  const [savedSettingsOverrideUpdatedAt, setSavedSettingsOverrideUpdatedAt] = useState<number | null>(null)
  const draftSettingsRef = useRef(draftSettings)

  useLayoutEffect(() => {
    draftSettingsRef.current = draftSettings
  }, [draftSettings])

  useEffect(() => () => {
    saveSettingsAbortControllerRef.current?.abort()
    saveSettingsAbortControllerRef.current = null
  }, [user?.id])

  const handleTabSelectionChange = useCallback((key: React.Key) => {
    const nextTab = normalizeSettingsTab(String(key))

    if (nextTab === 'overview') {
      setSearchParams({})
      return
    }

    setSearchParams({ tab: nextTab })
  }, [setSearchParams])

  const handleOverviewNavigate = useCallback((destination: SettingsDestination) => {
    if (destination === 'device-care') {
      navigate('/system-health#notification-settings')
      return
    }
    if (destination === 'users-access') {
      navigate('/users?view=access')
      return
    }
    handleTabSelectionChange(destination)
  }, [handleTabSelectionChange, navigate])

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
      scrubScheduleEnabled: data.maintenance?.scrub?.enabled ?? false,
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
  const webdavUserAuthAvailable = settingsData?.data.auth.enabled !== false
  const webdavUserAuthUnavailable = settings.webdavEnabled && !webdavUserAuthAvailable
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
      description: '保存设置后生效；监听地址变更需要重启服务，会话有效期、新分享默认有效期和默认下载次数会保持在公网建议范围内。',
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
      case 'dataplane_listen': {
        const dataplaneGRPCAddress = dataplaneLoopbackAddressFromSecurityCheck(check)
        addToast({
          title: '需要调整数据面配置',
          description: `在 config.toml 的 [dataplane] 中设置 grpc_address = "${dataplaneGRPCAddress}"；systemd 部署还需将 DATAPLANE_GRPC_ADDR 设为相同地址，随后重启 dataplane 和 MnemoNAS 服务。`,
          color: 'warning',
        })
        return
      }
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
          description: `将 DATAPLANE_HTTP_ADDR 设为 ${dataplaneHTTPAddress}，随后重启 dataplane 和 MnemoNAS 服务。`,
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
        return { label: '查看配置方法', onPress: () => applySecurityCheckFix(check) }
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
      const sanitizedSubmittedSettings = sanitizeSavedSettingsOverride(variables.submittedSettings)
      setSavedSettingsOverride(sanitizedSubmittedSettings)
      setSavedSettingsOverrideUpdatedAt(variables.baseSettingsUpdatedAt)
      useAuthStore.getState().setShareEnabled(variables.submittedSettings.shareEnabled)
      setDraftSettings(current => sanitizeDirtyDraftAfterSave(current, variables.submittedSettings, sanitizedSubmittedSettings))

      if (shallowEqualSettingsDraft(draftSettingsRef.current, variables.submittedSettings)) {
        setIsDirty(false)
      }

      addToast(getSettingsSaveSuccessToast(result.message, result.warning === true))
      if (variables.invalidatesFileDeletionQueries) {
        void queryClient.invalidateQueries({ queryKey: ['files'] })
        void queryClient.invalidateQueries({ queryKey: ['trash'] })
      }
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

  useEffect(() => {
    setHasPendingSettingsChanges(isDirty || saveMutation.isPending)
  }, [isDirty, saveMutation.isPending, setHasPendingSettingsChanges])

  useEffect(() => {
    return () => setHasPendingSettingsChanges(false)
  }, [setHasPendingSettingsChanges])

  const handleSave = () => {
    let minFreeSpaceBytes: number
    let trashMaxSizeBytes: number
    let versioningMaxSizeBytes: number
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
    const trimmedShareBaseURL = settings.shareBaseURL.trim()
    const trimmedShareDefaultExpiresIn = settings.shareDefaultExpiresIn.trim()
    const trimmedShareDefaultMaxAccess = settings.shareDefaultMaxAccess.trim()
    const parsedShareDefaultMaxAccess = parseNonNegativeSafeIntegerInput(trimmedShareDefaultMaxAccess)
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
    const parsedSharePolicyRules = normalizeSharePolicyRulesForSave(settings.sharePolicyRules)
    if (parsedSharePolicyRules.error) {
      addToast({
        title: '分享路径策略格式无效',
        description: parsedSharePolicyRules.error,
        color: 'danger',
      })
      return
    }
    try {
      minFreeSpaceBytes = parseByteSize(settings.minFreeSpace)
      trashMaxSizeBytes = parseByteSize(settings.trashMaxSize)
      versioningMaxSizeBytes = parseByteSize(settings.versioningMaxSize)
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
        valid: isSafeByteSize(trashMaxSizeBytes, false),
        description: '回收站最大容量必须是大于 0 且不超过安全范围的整数',
      },
      {
        valid: isSafeByteSize(versioningMaxSizeBytes, false) && versioningMaxSizeBytes <= MAX_VERSIONED_FILE_SIZE_BYTES,
        description: '最大自动版本化文件大小必须是大于 0 且不超过 100 MiB 的整数',
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
        title: '分享默认下载次数无效',
        description: '默认下载次数必须是 0 或不超过安全范围的正整数',
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
      share: {
        enabled: settings.shareEnabled,
        base_url: trimmedShareBaseURL,
        default_expires_in: trimmedShareDefaultExpiresIn,
        default_max_access: parsedShareDefaultMaxAccess.value,
        policy_rules: parsedSharePolicyRules.rules,
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
    const savedSettings = savedSettingsOverride
      ?? (settingsData?.data ? mapServerSettings(settingsData.data) : null)
    const invalidatesFileDeletionQueries = savedSettings === null
      || req.retention?.max_versions !== Number(savedSettings.maxVersions.trim())
      || req.retention?.max_age !== savedSettings.maxAge.trim()
      || req.retention?.min_free_space !== parseByteSize(savedSettings.minFreeSpace)
      || req.retention?.gc_interval !== savedSettings.gcInterval.trim()
      || req.trash?.enabled !== savedSettings.trashEnabled
      || req.trash?.retention_days !== Number(savedSettings.trashRetentionDays.trim())
      || req.trash?.max_size !== parseByteSize(savedSettings.trashMaxSize)
    saveSettingsAbortControllerRef.current?.abort()
    const controller = new AbortController()
    saveSettingsAbortControllerRef.current = controller
    saveMutation.mutate({
      request: req,
      submittedSettings: { ...settings },
      baseSettingsUpdatedAt: settingsDataUpdatedAt,
      invalidatesFileDeletionQueries,
      signal: controller.signal,
    })
  }

  const handleOpenAccountSecurity = () => {
    if (isDirty || saveMutation.isPending) {
      addToast({
        title: '设置尚未保存',
        description: '请先保存设置或重置当前更改，再修改账户密码。',
        color: 'warning',
      })
      return
    }
    navigate('/account/security', {
      state: { returnTo: '/settings?tab=general' },
    })
  }

  if (isLoading) {
    return (
      <div className="h-full overflow-auto custom-scrollbar">
        <div className="max-w-4xl mx-auto p-4 sm:p-6 lg:p-7">
          <PageHeader
            title="设置"
            subtitle={selectedTab === 'overview' ? '按目标管理访问、保护和设备服务' : '调整网络、访问和数据保留'}
            actions={selectedTab !== 'overview' ? (
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
            ) : undefined}
            className={selectedTab === 'overview' ? 'mb-3' : 'mb-8'}
          />

          <Card className="card-mnemonas">
            <CardBody className="py-16">
              <div className="text-center">
                <div className="w-12 h-12 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-4" />
                <p className="text-default-500">加载设置…</p>
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
          subtitle={selectedTab === 'overview' ? '按目标管理访问、保护和设备服务' : '调整网络、访问和数据保留'}
          actions={selectedTab !== 'overview' ? (
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
          ) : undefined}
          className={selectedTab === 'overview' ? 'mb-3' : 'mb-8'}
        />

        {selectedTab !== 'overview' && (
          <div className="mb-4 flex items-center gap-2 sm:hidden">
            <button
              type="button"
              className="inline-flex h-10 shrink-0 items-center gap-1 rounded-lg border border-divider bg-content1 px-3 text-sm font-medium text-default-600 shadow-[var(--shadow-soft)]"
              onClick={() => handleTabSelectionChange('overview')}
            >
              <ChevronLeft size={16} aria-hidden="true" />
              概览
            </button>
            <label className="sr-only" htmlFor="settings-mobile-category">移动端设置分类</label>
            <select
              id="settings-mobile-category"
              aria-label="移动端设置分类"
              value={selectedTab}
              onChange={(event) => handleTabSelectionChange(event.target.value)}
              className="input-shell h-10 min-w-0 flex-1 rounded-lg border border-divider bg-content1 px-3 text-sm text-foreground shadow-[var(--shadow-soft)] outline-none focus:border-primary"
            >
              <option value="general">账户与远程访问</option>
              <option value="retention">数据保护与权限</option>
              <option value="webdav">设备挂载</option>
              <option value="shares">分享与协作</option>
            </select>
          </div>
        )}

        {/* Tabs */}
        <Tabs
          selectedKey={selectedTab}
          onSelectionChange={handleTabSelectionChange}
          aria-label="设置分类"
          classNames={{
            base: "w-full",
            tabList: cn(
              "w-full max-w-full justify-start gap-1 overflow-visible rounded-lg border border-divider bg-content1 p-1 shadow-[var(--shadow-soft)]",
              selectedTab === 'overview' ? "hidden" : "hidden sm:flex sm:flex-nowrap",
            ),
            tab: "!w-full min-w-0 px-3 py-2 rounded-lg text-default-600 data-[selected=true]:bg-accent-primary data-[selected=true]:text-white data-[selected=true]:shadow-sm whitespace-nowrap sm:!w-auto sm:!flex-none sm:min-w-fit sm:px-4",
            cursor: "hidden",
          }}
        >
          <Tab key="overview" title="概览">
            <SettingsOverview
              trashEnabled={settings.trashEnabled}
              webdavEnabled={settings.webdavEnabled}
              webdavAuthType={settings.webdavAuthType}
              shareEnabled={settings.shareEnabled}
              alertsEnabled={settingsData?.data.alerts?.enabled ?? false}
              diskHealthEnabled={settingsData?.data.disk_health?.enabled ?? false}
              scrubScheduleEnabled={settings.scrubScheduleEnabled}
              onNavigate={handleOverviewNavigate}
            />
          </Tab>

          <Tab key="general" title="账户与访问">
            <div className="space-y-6 mt-6">
              {authEnabled && (
                <SettingsSection
                  title="当前账户"
                  description="查看当前登录身份并修改本人的登录密码"
                  icon={User}
                >
                  <div className="flex flex-col gap-4 rounded-lg border border-divider bg-content2/45 p-4 sm:flex-row sm:items-center sm:justify-between">
                    <div className="min-w-0">
                      <p className="break-anywhere text-sm font-semibold text-foreground">
                        {user?.username ?? '未知账户'}
                      </p>
                      <p id="current-account-password-guidance" className="mt-1 text-xs leading-5 text-default-500">
                        {isDirty || saveMutation.isPending
                          ? '当前设置尚未保存。请先保存或重置当前更改，再修改账户密码。'
                          : '修改密码会单独生效，并让此账户在所有设备上的登录退出；无需使用页面顶部的“保存设置”。'}
                      </p>
                    </div>
                    <Button
                      variant="bordered"
                      className="min-h-10 shrink-0 rounded-lg"
                      startContent={<Key size={16} aria-hidden="true" />}
                      aria-describedby="current-account-password-guidance"
                      isDisabled={isDirty || saveMutation.isPending}
                      onPress={handleOpenAccountSecurity}
                    >
                      修改当前账户密码
                    </Button>
                  </div>
                </SettingsSection>
              )}

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

              <SettingsDisclosure
                title="专业网络参数"
                description="监听地址、会话周期、代理信任和证书路径仅在部署拓扑变化时调整"
              >
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

              </SettingsDisclosure>

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

          <Tab key="retention" title="数据保护">
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
                    description="超过该大小的文件默认不自动创建历史版本；单文件启用仍受 100 MiB 硬上限约束"
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

          <Tab key="webdav" title="设备挂载">
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
                description="日常或生产挂载优先使用 MnemoNAS 用户账号；Basic Auth 仅用于旧客户端或专用服务凭据"
                icon={Shield}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="认证方式"
                    description="保存后会立即作用到运行中的 WebDAV 服务"
                  >
                    <select
                      value={settings.webdavAuthType}
                      onChange={(event) => {
                        const nextWebDAVAuthType = event.target.value as WebDAVAuthType
                        updateDirtySettings((current) => ({
                          ...current,
                          webdavAuthType: nextWebDAVAuthType === 'users' && !webdavUserAuthAvailable
                            ? 'basic'
                            : nextWebDAVAuthType,
                        }))
                      }}
                      disabled={!settings.webdavEnabled}
                      className="input-shell h-9 rounded-lg px-3 text-sm bg-content1 border border-divider min-w-[160px]"
                      aria-label="WebDAV 认证方式"
                    >
                      <option value="users" disabled={!webdavUserAuthAvailable}>MnemoNAS 用户账号</option>
                      <option value="basic">Basic Auth</option>
                      <option value="none">无认证</option>
                    </select>
                  </SettingRow>
                  {webdavUserAuthUnavailable && (
                    <>
                      <Divider className="bg-divider" />
                      <div
                        aria-label="WebDAV 用户账号认证不可用说明"
                        className="flex items-start gap-3 rounded-lg border border-warning/30 bg-warning/5 px-4 py-3 text-sm"
                      >
                        <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" />
                        <div>
                          <div className="font-medium text-foreground">Web 登录认证未启用</div>
                          <div className="text-default-600">
                            WebDAV 用户账号认证需要先启用 Web 登录认证；当前可使用 Basic Auth，或在配置中启用认证后再改用用户账号认证。
                          </div>
                        </div>
                      </div>
                    </>
                  )}
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
                              ? webdavUserAuthAvailable
                                ? '当前监听地址不是 loopback，保存后任何能访问该端口的人都可以读写 WebDAV。建议改用 MnemoNAS 用户账号认证或 Basic Auth，或先把监听地址/端口限制到可信网络。'
                                : '当前监听地址不是 loopback，保存后任何能访问该端口的人都可以读写 WebDAV。建议改用 Basic Auth，或先启用 Web 登录认证后再改用用户账号认证。'
                              : '当前监听地址限制在本机；只有在反向代理、隧道或防火墙已提供外层认证时才建议保持无认证。'}
                          </div>
                        </div>
                      </div>
                    </>
                  )}
                  {settings.webdavAuthType === 'users' && webdavUserAuthAvailable && (
                    <>
                      <Divider className="bg-divider" />
                      <div
                        aria-label="WebDAV 用户账号认证说明"
                        className="flex items-start gap-3 rounded-lg border border-success/20 bg-success/5 px-4 py-3 text-sm"
                      >
                        <CheckCircle2 size={18} className="mt-0.5 shrink-0 text-success" />
                        <div>
                          <div className="font-medium text-foreground">使用 MnemoNAS 用户账号认证</div>
                          <div className="text-default-600">
                            WebDAV 登录会复用已启用用户账号，并继续受用户状态和目录权限限制。
                          </div>
                        </div>
                      </div>
                    </>
                  )}
                  {settings.webdavAuthType === 'basic' && (
                    <>
                      <Divider className="bg-divider" />
                      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                        <div>
                          <label className="text-sm font-medium text-default-600 mb-1.5 block">Basic Auth 用户名</label>
                          <Input
                            aria-label="WebDAV Basic Auth 用户名"
                            placeholder="admin"
                            value={settings.webdavUsername}
                            onValueChange={(v) => updateDirtySettings(s => ({ ...s, webdavUsername: v }))}
                            isDisabled={!settings.webdavEnabled}
                            startContent={<User size={16} className="text-default-500" />}
                            classNames={{
                              inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                            }}
                          />
                        </div>
                        <div>
                          <label className="text-sm font-medium text-default-600 mb-1.5 block">Basic Auth 密码</label>
                          <Input
                            type="password"
                            aria-label="WebDAV Basic Auth 密码"
                            placeholder="••••••••"
                            value={settings.webdavPassword}
                            onValueChange={(v) => updateDirtySettings(s => ({ ...s, webdavPassword: v, webdavUseGeneratedPassword: false }))}
                            isDisabled={!settings.webdavEnabled || settings.webdavUseGeneratedPassword}
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
                        isDisabled={!settings.webdavEnabled}
                      >
                        保存时使用自动生成密码
                      </Checkbox>
                    </>
                  )}
                </div>
              </SettingsSection>
            </div>
          </Tab>

          <Tab key="shares" title="分享与协作">
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
                    description="选择后会填入默认有效期和下载次数，可继续手动调整"
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
                    label="新分享默认下载次数"
                    description="0 表示不限制；只影响之后创建的分享链接"
                  >
                    <Input
                      type="text"
                      aria-label="新分享默认下载次数"
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
