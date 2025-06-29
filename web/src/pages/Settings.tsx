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
import { cn, copyTextToClipboard, parseByteSize, normalizeWebDAVPrefix, isValidWebDAVPrefix, webDAVPrefixOverlapsReservedRoute, formatWebDAVUrl, formatBytes } from '@/lib/utils'
import { GENERIC_LOAD_ERROR_DESCRIPTION, getUserFacingErrorDescription } from '@/lib/apiMessages'
import { ShareManager } from '@/components/share'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'
import { useAuthStore, useUser } from '@/stores/auth'
import {
  SettingsError,
  checkDirectoryAccess,
  getSecurityCheck,
  getSettings,
  getWebDAVCredentials,
  previewDirectoryAccess,
  reportDirectoryAccess,
  sendTestAlert,
  updateSettings,
  type DirectoryAccessCheckData,
  type DirectoryAccessCheckRequest,
  type DirectoryAccessDecision,
  type DirectoryAccessReportData,
  type DirectoryAccessReportRequest,
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
const ALERT_CHANNEL_LABELS: Record<string, string> = {
  webhook: 'Webhook',
  telegram: 'Telegram',
  email: 'SMTP 邮件',
}

function formatAlertChannelLabel(channel: string): string {
  const trimmed = channel.trim()
  if (!trimmed) {
    return ''
  }
  return ALERT_CHANNEL_LABELS[trimmed.toLowerCase()] ?? trimmed
}

function formatAlertChannelSummary(channels: string[]): string {
  return channels
    .map(formatAlertChannelLabel)
    .filter(Boolean)
    .join(' / ')
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
    description: '7 天有效，不限制次数',
    defaultExpiresIn: '168h',
    defaultMaxAccess: '0',
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

type SharePolicyRuleDraft = SharePolicyRule & {
  max_access_input?: string
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
    <Card className="card-meridian">
      <CardHeader className="flex min-w-0 gap-4 pb-2">
        <div className="gradient-meridian shrink-0 rounded-lg p-2.5 shadow-sm">
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

  if (error instanceof Error && error.message.includes('webdav.username must not match a non-admin user')) {
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

function getSettingsSaveSuccessToast(message?: string): {
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
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index)
    if (code <= 0x1f || code === 0x7f) {
      return true
    }
  }

  return false
}

function hasInvalidHTTPHeaderValueChar(value: string): boolean {
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index)
    if (code === 0x7f || (code <= 0x1f && code !== 0x09)) {
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

  try {
    const parsed = new URL(trimmed)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return false
    }
    if (parsed.username || parsed.password || trimmed.includes('?') || trimmed.includes('#') || parsed.search || parsed.hash) {
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
  const trimmedPath = pathname.replace(/\/+$/u, '')
  return trimmedPath === '/s' || trimmedPath.endsWith('/s')
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
    parsed.pathname = stripShareRouteSuffix(parsed.pathname)

    if (parsed.pathname === '/' && parsed.search === '') {
      return parsed.origin
    }
    return parsed.toString()
  } catch {
    return ''
  }
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
    .map((quota) => `${quota.path} ${formatBytes(quota.quota_bytes)}`)
    .join('\n')
}

function normalizeDirectoryQuotaPathInput(value: string): string | null {
  const trimmed = value.trim()
  if (!trimmed.startsWith('/') || /[\s\\?#]/.test(trimmed) || hasControlChar(trimmed)) {
    return null
  }
  const withoutTrailingSlash = trimmed === '/' ? '/' : trimmed.replace(/\/+$/, '')
  if (withoutTrailingSlash === '') {
    return null
  }
  const segments = withoutTrailingSlash.split('/').slice(1)
  if (segments.some((segment) => segment === '' || segment === '.' || segment === '..')) {
    return null
  }
  return withoutTrailingSlash
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

    const parts = line.split(/\s+/)
    if (parts.length < 2) {
      return { quotas: [], error: `第 ${index + 1} 行需要填写路径和容量` }
    }

    const quotaPath = normalizeDirectoryQuotaPathInput(parts[0])
    if (!quotaPath) {
      return { quotas: [], error: `第 ${index + 1} 行路径无效` }
    }
    if (seenPaths.has(quotaPath)) {
      return { quotas: [], error: `第 ${index + 1} 行路径重复` }
    }

    const sizeText = parts.slice(1).join(' ')
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
      rule.path,
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

    const parts = line.split(/\s+/)
    const rulePath = normalizeDirectoryQuotaPathInput(parts[0])
    if (!rulePath) {
      return { rules: [], error: `第 ${lineNumber} 行路径无效` }
    }
    if (seenPaths.has(rulePath)) {
      return { rules: [], error: `第 ${lineNumber} 行路径重复` }
    }

    const rule: DirectoryAccessRule = { path: rulePath }
    for (const token of parts.slice(1)) {
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

    if (!inputRule.require_password && !hasMaxExpiresInConstraint && maxAccess === 0) {
      return { rules: [], error: `第 ${lineNumber} 行至少需要一个约束` }
    }

    seenPaths.add(rulePath)
    rules.push({
      path: rulePath,
      require_password: inputRule.require_password || undefined,
      max_expires_in: hasMaxExpiresInConstraint ? maxExpiresIn : undefined,
      max_access: maxAccess > 0 ? maxAccess : undefined,
    })
  }

  return { rules }
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

  if (normalizedMessage === 'directory access rule grants read through a descendant') {
    return '子目录存在读取规则，因此允许查看相关路径。'
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

function DirectoryAccessReportResult({
  report,
  title = '用户矩阵',
  ariaLabel = '目录权限用户矩阵',
}: {
  report: DirectoryAccessReportData
  title?: string
  ariaLabel?: string
}) {
  const shares = report.shares ?? []

  return (
    <div className="rounded-lg border border-divider bg-content2/40 p-3" aria-label={ariaLabel}>
      <div className="mb-2 text-sm font-semibold text-foreground">{title}</div>
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
  const parts = line.trim().split(/\s+/).filter(Boolean)
  draft.path = parts[0] ?? ''
  for (const token of parts.slice(1)) {
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
      const parts = [draft.path.trim()]
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

  const removeDraft = (index: number) => {
    const nextDrafts = drafts.filter((_, draftIndex) => draftIndex !== index)
    commitDrafts(nextDrafts)
  }

  return (
    <div className="space-y-3">
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
  return Boolean(rule.require_password || (maxExpiresIn && !isZeroDurationString(maxExpiresIn)) || (rule.max_access && rule.max_access > 0))
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
      return check.status === 'pass'
        ? '未启用无认证公网暴露例外。'
        : '当前允许无认证服务绑定到非本机地址，公网访问前必须关闭该例外或重新启用认证。'
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
      return check.status === 'pass'
        ? '管理员账号配置满足基本可用性要求。'
        : '建议至少保留一个可用管理员账号，并为家庭或团队配置备用管理员。'
    case 'dataplane_listen':
      return check.status === 'pass'
        ? '数据面 gRPC 仅监听本机地址。'
        : '数据面 gRPC 不应暴露到外网，请绑定到 127.0.0.1 或 ::1。'
    case 'dataplane_http_listen':
      return check.status === 'pass'
        ? '数据面 HTTP 健康接口仅监听本机地址。'
        : '数据面 HTTP 健康接口不应暴露到外网，请绑定到本机地址。'
    case 'webdav_auth':
      if (securityCheckHasWebDAVPasswordRisk(check)) {
        return 'WebDAV Basic Auth 使用弱密码或示例密码，公网访问前应更换为自动生成密码、自定义强密码，或改用 MnemoNAS 用户认证。'
      }
      return check.status === 'pass'
        ? 'WebDAV 暴露面已启用合适的认证方式，或当前未启用 WebDAV。'
        : 'WebDAV 对外访问前必须启用 Basic 认证、MnemoNAS 用户认证或关闭 WebDAV。'
    case 'smb_preview':
      return check.status === 'pass'
        ? 'SMB 预览未启用，不会启动额外的 SMB/Samba 监听器。'
        : '当前版本仍未内置可挂载的 SMB/Samba 运行时；启用前应先收紧监听范围和防火墙。'
    case 'share_base_url':
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
    case 'initial_password_file':
      return check.status === 'pass'
        ? '初始管理员密码文件状态正常。'
        : '初始管理员密码文件需要处理，避免遗留凭据被误用。'
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
    <Card className="card-meridian">
      <CardHeader className="flex min-w-0 flex-col gap-4 pb-2 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 gap-4">
          <div className="gradient-meridian shrink-0 rounded-lg p-2.5 shadow-sm">
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
            {issueChecks.length === 0 && (
              <p className="text-xs text-default-500">
                当前自检项均已通过。公网域名、云防火墙和端口暴露仍建议使用服务器上的 mnemonas-doctor 复核。
              </p>
            )}
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
            }}
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
            }}
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
            }}
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
        description: '请至少配置 Webhook、Telegram 或邮件通道并保存后再发送测试提醒。',
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
  const saveSettingsAbortControllerRef = useRef<AbortController | null>(null)
  const accessCheckAbortControllerRef = useRef<AbortController | null>(null)
  const accessReportAbortControllerRef = useRef<AbortController | null>(null)
  const accessPreviewAbortControllerRef = useRef<AbortController | null>(null)
  const testAlertAbortControllerRef = useRef<AbortController | null>(null)
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
        title: '测试提醒已发送',
        description: channels ? `已发送到 ${channels}` : result.message,
        color: 'success',
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
    accessCheckAbortControllerRef.current?.abort()
    const controller = new AbortController()
    accessCheckAbortControllerRef.current = controller
    accessCheckMutation.mutate({
      request: { username, path: targetPath },
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
    accessReportAbortControllerRef.current?.abort()
    const controller = new AbortController()
    accessReportAbortControllerRef.current = controller
    accessReportMutation.mutate({
      request: { path: targetPath },
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
        path: targetPath,
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
      shareBaseURL: prev.shareEnabled && publicAccessBaseURL ? publicAccessBaseURL : prev.shareBaseURL,
    }))
    addToast({
      title: '已应用公网访问推荐',
      description: '保存设置后生效；监听地址变更需要重启服务。',
      color: 'success',
    })
  }

  const applySecurityCheckFix = (check: SecurityCheckItem) => {
    switch (check.id) {
      case 'https_request':
      case 'public_http_exposure':
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
      case 'unsafe_no_auth_override':
        addToast({
          title: '需要编辑配置文件',
          description: '将 [security].allow_unsafe_no_auth 改为 false，并确认 Web 登录和 WebDAV 认证已启用后重启服务。',
          color: 'warning',
        })
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
      default:
        addToast({ title: '该项需要手动处理', color: 'warning' })
    }
  }

  const getSecurityCheckAction = (check: SecurityCheckItem): SecurityCheckAction | undefined => {
    switch (check.id) {
      case 'https_request':
        return { label: '应用代理推荐', onPress: () => applySecurityCheckFix(check) }
      case 'public_http_exposure':
        return { label: '应用代理推荐', onPress: () => applySecurityCheckFix(check) }
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
        return { label: '查看处理方式', onPress: () => applySecurityCheckFix(check) }
      case 'webdav_auth':
        return { label: securityCheckHasWebDAVPasswordRisk(check) ? '更换密码' : '启用认证', onPress: () => applySecurityCheckFix(check) }
      case 'share_base_url':
        return { label: '使用 HTTPS URL', onPress: () => applySecurityCheckFix(check) }
      case 'unsafe_no_auth_override':
        return { label: '查看处理方式', onPress: () => applySecurityCheckFix(check) }
      case 'admin_accounts':
        return { label: '管理用户', onPress: () => applySecurityCheckFix(check) }
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

      addToast(getSettingsSaveSuccessToast(result.message))
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
    } catch (err) {
      addToast({
        title: '大小格式无效',
        description: err instanceof Error ? err.message : '请使用 1024、1 KB、1.5 MB 之类的格式',
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
        description: '分享基础 URL 必须为空，或使用不含 userinfo、查询参数、片段且主机名有效的 http/https 地址',
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
        title: 'Webhook URL 占位符无效',
        description: '只有服务端已保存的 Webhook URL 才能保留为 <redacted>；新增 Webhook URL 需要填写真实地址。',
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
        title: 'Webhook Header 占位符无效',
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

          <Card className="card-meridian">
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
                title="服务器"
                description="配置服务器网络参数；保存后需重启服务才能影响监听地址、端口和 HTTP 超时"
                icon={Server}
              >
                <div className="space-y-4">
                  <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">监听地址</label>
                      <Input
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
                    placeholder="/team 1 TB"
                    className="input-shell w-full rounded-medium border border-transparent bg-transparent px-3 py-2 font-mono text-sm outline-none focus:border-accent-primary"
                  />
                  <div className="grid gap-2 text-xs text-default-500 sm:grid-cols-2">
                    <div className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
                      每行一个目录，例如 <span className="font-mono text-foreground">/team 1 TB</span>
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
                  <div className="grid gap-2 text-xs text-default-500 sm:grid-cols-2">
                    <div className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
                      可用字段：<span className="font-mono text-foreground">read_users</span>、<span className="font-mono text-foreground">write_users</span>、<span className="font-mono text-foreground">read_groups</span>、<span className="font-mono text-foreground">write_groups</span>
                    </div>
                    <div className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
                      角色字段：<span className="font-mono text-foreground">read_roles</span>、<span className="font-mono text-foreground">write_roles</span>；多个值用英文逗号分隔
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
                    <DirectoryAccessReportResult report={accessReportMutation.data} />
                  )}
                  {accessPreviewMutation.data && (
                    <DirectoryAccessReportResult
                      report={accessPreviewMutation.data}
                      title="变更预览"
                      ariaLabel="目录权限变更预览"
                    />
                  )}
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
                            onPress={() => handleCopy('url', webdavUrl)}
                          >
                            <span className="sr-only">复制 WebDAV 地址</span>
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
                              onPress={() => handleCopy('url', webdavUrl)}
                            >
                              <span className="sr-only">复制 WebDAV 地址</span>
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
                              onPress={() => handleCopy('username', webdavCredentials.username || 'admin')}
                            >
                              <span className="sr-only">复制 WebDAV 用户名</span>
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
                              onPress={() => setShowWebDAVPassword(!showWebDAVPassword)}
                            >
                              <span className="sr-only">{showWebDAVPassword ? '隐藏 WebDAV 密码' : '显示 WebDAV 密码'}</span>
                              {showWebDAVPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                            </Button>
                            <Button
                              isIconOnly
                              size="sm"
                              variant="flat"
                              onPress={() => handleCopy('password', webdavCredentials.password || '')}
                              isDisabled={!webdavCredentials.password}
                            >
                              <span className="sr-only">复制 WebDAV 密码</span>
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
                      value={settings.shareBaseURL}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, shareBaseURL: v }))}
                      placeholder="https://nas.example.com"
                      isDisabled={!settings.shareEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
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
                    description="为指定目录设置更严格的分享约束；更深的路径优先生效"
                  >
                    <SharePolicyRuleEditor
                      rules={settings.sharePolicyRules}
                      isDisabled={!settings.shareEnabled}
                      onChange={(nextRules) => updateDirtySettings(s => ({ ...s, sharePolicyRules: nextRules }))}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>

              <SettingsSection
                title="分享链接"
                description="查看和处理已创建的分享链接"
                icon={Link2}
              >
                <ShareManager featureEnabled={settings.shareEnabled} />
              </SettingsSection>
            </div>
          </Tab>
        </Tabs>
      </div>
    </div>
  )
}
