import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useMutation } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { 
  Card, 
  CardBody, 
  CardHeader,
  Button,
  Input,
  Switch,
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
} from 'lucide-react'
import { cn, copyTextToClipboard, parseByteSize, normalizeWebDAVPrefix, isValidWebDAVPrefix, webDAVPrefixOverlapsReservedRoute, formatWebDAVUrl, formatBytes } from '@/lib/utils'
import { ShareManager } from '@/components/share'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'
import { useAuthStore, useUser } from '@/stores/auth'
import {
  SettingsError,
  getSecurityCheck,
  getSettings,
  getWebDAVCredentials,
  updateSettings,
  type SecurityCheckData,
  type SecurityCheckItem,
  type SecurityCheckStatus,
  type UpdateSettingsRequest,
} from '@/api/settings'

const MIN_CDC_CHUNK_SIZE_BYTES = 64 * 1024
const MAX_CDC_CHUNK_SIZE_BYTES = 64 * 1024 * 1024

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
      description: '系统设置当前不可用，请检查服务健康状态或稍后重试。',
    }
  }

  return {
    title: '加载设置失败',
    description: (error as Error).message,
  }
}

function getWebDAVCredentialsErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof SettingsError && error.isUnavailable) {
    return {
      title: 'WebDAV 凭据暂不可用',
      description: '当前无法读取运行中的 WebDAV 凭据，请检查系统状态或稍后重试。',
    }
  }

  return {
    title: 'WebDAV 凭据加载失败',
    description: (error as Error).message || '请稍后重试',
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
      description: '当前无法读取运行中的 WebDAV 凭据，请检查系统状态或稍后重试。',
      color: 'warning',
    }
  }

  return {
    title: '刷新失败',
    description: error instanceof Error ? error.message : '请稍后重试',
    color: 'danger',
  }
}

function shallowEqualSettingsDraft<T extends Record<string, string | boolean>>(left: T, right: T): boolean {
  const leftKeys = Object.keys(left)
  if (leftKeys.length !== Object.keys(right).length) {
    return false
  }

  return leftKeys.every((key) => left[key] === right[key])
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
      description: '系统设置当前不可用，请检查服务健康状态或稍后重试。',
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
    description: error instanceof Error ? error.message : '请稍后重试',
    color: 'danger',
  }
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

function isSettingsTabKey(value: string): value is SettingsTabKey {
  return SETTINGS_TABS.includes(value as SettingsTabKey)
}

function normalizeSettingsTab(value: string | null): SettingsTabKey {
  if (value && isSettingsTabKey(value)) {
    return value
  }

  return 'general'
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

const httpHeaderNamePattern = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/

function isValidWebhookHeaderLine(header: string): boolean {
  const separator = header.indexOf(':')
  if (separator <= 0 || separator === header.length - 1) {
    return false
  }

  const name = header.slice(0, separator).trim()
  const value = header.slice(separator + 1).trim()
  return httpHeaderNamePattern.test(name) && value.length > 0 && !hasInvalidHTTPHeaderValueChar(value)
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

function SecurityCheckRow({ check }: { check: SecurityCheckItem }) {
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
        <p className="break-anywhere mt-1 text-xs leading-relaxed text-default-600">{check.message}</p>
      </div>
    </div>
  )
}

function SecurityCheckCard({
  data,
  error,
  isLoading,
  isRefreshing,
  onRefresh,
}: {
  data?: SecurityCheckData
  error: unknown
  isLoading: boolean
  isRefreshing: boolean
  onRefresh: () => void
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
                {error instanceof Error ? error.message : '请稍后重试。'}
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
                <SecurityCheckRow key={check.id} check={check} />
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

export function SettingsPage() {
  const user = useUser()
  const [searchParams, setSearchParams] = useSearchParams()
  const selectedTab = normalizeSettingsTab(searchParams.get('tab'))
  const defaultSettings = {
    serverHost: '0.0.0.0',
    serverPort: '8080',
    serverReadTimeout: '30s',
    serverWriteTimeout: '60s',
    serverIdleTimeout: '120s',
    serverTrustedProxyHops: '0',
    tlsEnabled: false,
    tlsCertFile: '',
    tlsKeyFile: '',
    tlsAutoGenerate: true,
    tlsCertDir: '',
    storageRoot: '',
    trashEnabled: true,
    trashRetentionDays: '30',
    trashMaxSize: '10 GB',
    maxVersions: '100',
    maxAge: '8760h',
    minFreeSpace: '10GB',
    gcInterval: '24h',
    versioningExtensions: '.md\n.txt\n.go',
    versioningFilenames: 'README\nDockerfile\nMakefile',
    versioningMaxSize: '100 MB',
    webdavEnabled: true,
    webdavPrefix: '/dav',
    webdavReadOnly: false,
    webdavAuthType: 'basic',
    webdavUsername: 'admin',
    webdavPassword: '',
    shareEnabled: false,
    shareBaseURL: '',
    favoritesEnabled: true,
    alertsEnabled: false,
    alertsCheckInterval: '1h',
    alertsThresholdPct: '90',
    alertsCriticalPct: '95',
    alertsMinFreeSpace: '10GB',
    alertsCooldownPeriod: '4h',
    alertsWebhookURL: '',
    alertsWebhookMethod: 'POST',
    alertsWebhookHeaders: '',
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
  }
  
  // WebDAV credentials state
  const [showWebDAVPassword, setShowWebDAVPassword] = useState(false)
  const [copiedField, setCopiedField] = useState<string | null>(null)
  
  // Fetch settings from API
  const { data: settingsData, dataUpdatedAt: settingsDataUpdatedAt, isLoading, error, refetch, isRefetching } = useQuery({
    queryKey: ['settings', user?.id ?? 'anonymous'],
    queryFn: getSettings,
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
    queryFn: getSecurityCheck,
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
    queryFn: getWebDAVCredentials,
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
      tlsEnabled: data.server.tls?.enabled ?? false,
      tlsCertFile: data.server.tls?.cert_file ?? '',
      tlsKeyFile: data.server.tls?.key_file ?? '',
      tlsAutoGenerate: data.server.tls?.auto_generate ?? true,
      tlsCertDir: data.server.tls?.cert_dir ?? '',
      storageRoot: data.storage.root,
      trashEnabled: data.trash?.enabled ?? true,
      trashRetentionDays: String(data.trash?.retention_days ?? 30),
      trashMaxSize: formatBytes(data.trash?.max_size ?? 10737418240),
      maxVersions: String(data.retention.max_versions),
      maxAge: data.retention.max_age,
      minFreeSpace: formatBytes(data.retention.min_free_space),
      gcInterval: data.retention.gc_interval,
      versioningExtensions: data.versioning?.auto_versioned_extensions?.join('\n') ?? '.md\n.txt\n.go',
      versioningFilenames: data.versioning?.auto_versioned_filenames?.join('\n') ?? 'README\nDockerfile\nMakefile',
      versioningMaxSize: formatBytes(data.versioning?.max_versioned_size ?? 104857600),
      webdavEnabled: data.webdav.enabled,
      webdavPrefix: data.webdav.prefix,
      webdavReadOnly: data.webdav.read_only,
      webdavAuthType: data.webdav.auth_type,
      webdavUsername: data.webdav.username,
      webdavPassword: '',
      shareEnabled: data.share.enabled,
      shareBaseURL: data.share.base_url,
      favoritesEnabled: data.favorites?.enabled ?? true,
      alertsEnabled: data.alerts?.enabled ?? false,
      alertsCheckInterval: data.alerts?.check_interval ?? '1h',
      alertsThresholdPct: String(data.alerts?.threshold_pct ?? 90),
      alertsCriticalPct: String(data.alerts?.critical_pct ?? 95),
      alertsMinFreeSpace: formatBytes(data.alerts?.min_free_bytes ?? 10737418240),
      alertsCooldownPeriod: data.alerts?.cooldown_period ?? '4h',
      alertsWebhookURL: data.alerts?.webhook_url ?? '',
      alertsWebhookMethod: data.alerts?.webhook_method ?? 'POST',
      alertsWebhookHeaders: data.alerts?.webhook_headers?.join('\n') ?? '',
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

  const settings = useMemo(() => {
    if (!isDirty && savedSettingsOverride) {
      return savedSettingsOverride
    }
    if (!isDirty && settingsData?.data) {
      return mapServerSettings(settingsData.data)
    }
    return draftSettings
  }, [draftSettings, isDirty, mapServerSettings, savedSettingsOverride, settingsData])
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

  const updateDirtySettings = (updater: (prev: typeof draftSettings) => typeof draftSettings) => {
    setIsDirty(true)
    setDraftSettings((prev) => updater(isDirty ? prev : settings))
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
    mutationFn: ({ request }: SaveSettingsVariables) => updateSettings(request),
    onSuccess: (result, variables) => {
      setSavedSettingsOverride(variables.submittedSettings)
      setSavedSettingsOverrideUpdatedAt(variables.baseSettingsUpdatedAt)
      useAuthStore.getState().setShareEnabled(variables.submittedSettings.shareEnabled)

      if (shallowEqualSettingsDraft(draftSettingsRef.current, variables.submittedSettings)) {
        setIsDirty(false)
      }

      addToast(getSettingsSaveSuccessToast(result.message))
      void refetch()
      if (selectedTab === 'general') {
        void refetchSecurityCheck()
      }
    },
    onError: (err: unknown) => {
      addToast(getSettingsActionErrorToast(err, {
        unavailable: '保存设置暂不可用',
        failure: '保存失败',
      }))
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
    const trimmedAlertsWebhookURL = settings.alertsWebhookURL.trim()
    const trimmedAlertsWebhookMethod = settings.alertsWebhookMethod.trim().toUpperCase()
    const alertsWebhookHeaders = settings.alertsWebhookHeaders
      .split('\n')
      .map(header => header.trim())
      .filter(Boolean)
    const parsedAlertsThresholdPct = Number(trimmedAlertsThresholdPct)
    const parsedAlertsCriticalPct = Number(trimmedAlertsCriticalPct)
    const versioningExtensions = settings.versioningExtensions
      .split('\n')
      .map(entry => entry.trim())
      .filter(Boolean)
    const versioningFilenames = settings.versioningFilenames
      .split('\n')
      .map(entry => entry.trim())
      .filter(Boolean)

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

	if (!trimmedWriteTimeout) {
		addToast({
			title: '写入超时格式无效',
			description: '写入超时不能为空',
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

  if (!/^\d+$/.test(trimmedTrustedProxyHops) || !Number.isInteger(parsedTrustedProxyHops) || parsedTrustedProxyHops < 0) {
    addToast({
      title: '受信代理层数格式无效',
      description: '受信代理层数必须是 0 或正整数',
      color: 'danger',
    })
    return
  }

    if (!/^\d+$/.test(trimmedMaxVersions) || !Number.isInteger(parsedMaxVersions) || parsedMaxVersions < 0) {
      addToast({
        title: '最大版本数格式无效',
        description: '最大版本数必须是 0 或正整数',
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

    if (!isValidTCPAddress(trimmedDataplaneGrpcAddress)) {
      addToast({
        title: '数据面地址格式无效',
        description: 'gRPC 地址必须是合法的 host:port，端口为 1 到 65535，且不能包含空白或控制字符',
        color: 'danger',
      })
      return
    }

    if (!/^\d+$/.test(trimmedMaxRetries) || !Number.isInteger(parsedMaxRetries) || parsedMaxRetries < 0) {
      addToast({
        title: '最大重试次数格式无效',
        description: '最大重试次数必须是 0 或正整数',
        color: 'danger',
      })
      return
    }

    if (!trimmedAlertsCheckInterval) {
      addToast({
        title: '告警检查间隔格式无效',
        description: '检查间隔不能为空',
        color: 'danger',
      })
      return
    }

    if (!trimmedAlertsCooldownPeriod) {
      addToast({
        title: '告警冷却时间格式无效',
        description: '冷却时间不能为空',
        color: 'danger',
      })
      return
    }

    if (!Number.isFinite(parsedAlertsThresholdPct) || parsedAlertsThresholdPct < 0 || parsedAlertsThresholdPct > 100) {
      addToast({
        title: '告警阈值格式无效',
        description: '告警阈值必须在 0 到 100 之间',
        color: 'danger',
      })
      return
    }

    if (!Number.isFinite(parsedAlertsCriticalPct) || parsedAlertsCriticalPct < 0 || parsedAlertsCriticalPct > 100) {
      addToast({
        title: '严重告警阈值格式无效',
        description: '严重告警阈值必须在 0 到 100 之间',
        color: 'danger',
      })
      return
    }

    if (parsedAlertsCriticalPct < parsedAlertsThresholdPct) {
      addToast({
        title: '告警阈值关系无效',
        description: '严重告警阈值不能小于普通告警阈值',
        color: 'danger',
      })
      return
    }

    if (!isValidOptionalHTTPURL(trimmedShareBaseURL)) {
      addToast({
        title: '分享基础 URL 无效',
        description: '分享基础 URL 必须为空，或使用 http/https 的完整地址',
        color: 'danger',
      })
      return
    }

    if (!isValidOptionalHTTPURL(trimmedAlertsWebhookURL)) {
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

  if (!/^\d+$/.test(trimmedTrashRetentionDays) || !Number.isInteger(parsedTrashRetentionDays) || parsedTrashRetentionDays < 0) {
    addToast({
      title: '回收站保留天数格式无效',
      description: '回收站保留天数必须是 0 或正整数',
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

    const req: UpdateSettingsRequest = {
      server: {
        host: trimmedServerHost,
        port: parsedPort,
        read_timeout: trimmedReadTimeout,
        write_timeout: trimmedWriteTimeout,
        idle_timeout: trimmedIdleTimeout,
        trusted_proxy_hops: parsedTrustedProxyHops,
        tls: {
          enabled: settings.tlsEnabled,
          cert_file: settings.tlsCertFile.trim(),
          key_file: settings.tlsKeyFile.trim(),
          auto_generate: settings.tlsAutoGenerate,
          cert_dir: settings.tlsCertDir.trim(),
        },
      },
      retention: {
        max_versions: parsedMaxVersions,
        max_age: settings.maxAge,
        min_free_space: minFreeSpaceBytes,
        gc_interval: settings.gcInterval,
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
        ...(settings.webdavPassword && { password: settings.webdavPassword }),
      },
    }
    saveMutation.mutate({
      request: req,
      submittedSettings: { ...settings },
      baseSettingsUpdatedAt: settingsDataUpdatedAt,
    })
  }

  if (isLoading) {
    return (
      <div className="h-full overflow-auto custom-scrollbar">
        <div className="max-w-4xl mx-auto p-4 sm:p-6 lg:p-7">
          <PageHeader
            title="系统设置"
            subtitle="配置 MnemoNAS 系统参数"
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
          title="系统设置"
          subtitle="配置 MnemoNAS 系统参数"
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
            tabList: "w-full max-w-full justify-start overflow-x-auto bg-content1 border border-divider rounded-lg p-1 gap-1 shadow-[var(--shadow-soft)]",
            tab: "!w-auto !flex-none shrink-0 min-w-fit px-4 py-2 rounded-lg text-default-600 data-[selected=true]:bg-accent-primary data-[selected=true]:text-white data-[selected=true]:shadow-sm whitespace-nowrap",
            cursor: "hidden",
          }}
        >
          <Tab key="general" title="常规">
            <div className="space-y-6 mt-6">
              <SecurityCheckCard
                data={securityCheckResponse?.data}
                error={securityCheckError}
                isLoading={isLoadingSecurityCheck}
                isRefreshing={isRefetchingSecurityCheck}
                onRefresh={() => { void refetchSecurityCheck() }}
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
            placeholder="~/.mnemonas/certs"
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
                      type="number"
                      min={0}
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
                      placeholder="8760h"
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
                        webdavAuthType: event.target.value as 'basic' | 'none',
                      }))}
                      disabled={!settings.webdavEnabled}
                      className="input-shell h-9 rounded-lg px-3 text-sm bg-content1 border border-divider min-w-[160px]"
                      aria-label="WebDAV 认证方式"
                    >
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
                        isDisabled={!settings.webdavEnabled || settings.webdavAuthType === 'none'}
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
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, webdavPassword: v }))}
                        isDisabled={!settings.webdavEnabled || settings.webdavAuthType === 'none'}
                        startContent={<Lock size={16} className="text-default-500" />}
                        classNames={{ 
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                        }}
                      />
                    </div>
                  </div>
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
                      type="number"
                      min={0}
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
                title="存储告警"
                description="配置磁盘空间监控和 Webhook 告警；保存后会立即更新运行中的告警监控"
                icon={AlertCircle}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="启用告警"
                    description="启用后定期检查存储空间并发送告警，保存后立即生效"
                  >
                    <Switch
                      aria-label="启用告警"
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
                      启用告警
                    </Switch>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="检查间隔"
                    description="磁盘空间检查频率"
                  >
                    <Input
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
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">普通告警阈值 (%)</label>
                      <Input
                        type="number"
                        min={0}
                        max={100}
                        inputMode="numeric"
                        value={settings.alertsThresholdPct}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsThresholdPct: v }))}
                        isDisabled={!settings.alertsEnabled}
                        classNames={{ inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary" }}
                      />
                    </div>
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">严重告警阈值 (%)</label>
                      <Input
                        type="number"
                        min={0}
                        max={100}
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
                    description="剩余空间低于该值时发送告警"
                  >
                    <Input
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
                    description="同级别连续告警之间的最小间隔"
                  >
                    <Input
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
                    description="发送告警通知的目标地址"
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
                    description="告警通知请求使用的 HTTP 方法"
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

          <Tab key="shares" title="分享管理">
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
                </div>
              </SettingsSection>

              <SettingsSection
                title="分享链接管理"
                description="管理所有已创建的分享链接"
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
