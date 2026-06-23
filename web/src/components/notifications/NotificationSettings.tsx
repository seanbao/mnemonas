import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  addToast,
  Button,
  Card,
  CardBody,
  CardHeader,
  Checkbox,
  Chip,
  Divider,
  Input,
  Switch,
} from '@heroui/react'
import {
  AlertCircle,
  BellRing,
  Bot,
  ChevronDown,
  Globe2,
  Mail,
  MessageCircleMore,
  Plus,
  RefreshCw,
  RotateCcw,
  Save,
  Send,
  Webhook,
} from 'lucide-react'
import {
  getSettings,
  sendTestAlert,
  SettingsError,
  updateSettings,
  type UpdateSettingsRequest,
} from '@/api/settings'
import {
  GENERIC_ACTION_ERROR_DESCRIPTION,
  GENERIC_LOAD_ERROR_DESCRIPTION,
  getUserFacingErrorDescription,
} from '@/lib/apiMessages'
import { getRedactedDiagnosticMessage } from '@/lib/diagnosticMessages'
import { cn, formatBytes, hasControlCharacter, parseByteSize } from '@/lib/utils'

const REDACTED_SETTINGS_SECRET = '<redacted>'
const goDurationPattern = /^(?:\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h))+$/u
const httpHeaderNamePattern = /^[!#$%&'*+\-.^_\x60|~0-9A-Za-z]+$/u

interface NotificationDraft {
  enabled: boolean
  checkInterval: string
  thresholdPct: string
  criticalPct: string
  minFreeSpace: string
  cooldownPeriod: string
  webhookURL: string
  webhookURLConfigured: boolean
  webhookMethod: string
  webhookHeaders: string
  webhookHeadersConfigured: boolean
  telegramEnabled: boolean
  telegramBotToken: string
  telegramBotTokenConfigured: boolean
  telegramBotTokenClear: boolean
  telegramChatID: string
  weComEnabled: boolean
  weComWebhookURL: string
  weComWebhookURLConfigured: boolean
  dingTalkEnabled: boolean
  dingTalkWebhookURL: string
  dingTalkWebhookURLConfigured: boolean
  emailEnabled: boolean
  smtpHost: string
  smtpPort: string
  smtpUsername: string
  smtpPassword: string
  smtpPasswordConfigured: boolean
  smtpPasswordClear: boolean
  smtpFrom: string
  smtpTo: string
}

type NotificationValidationErrors = Partial<Record<
  | 'checkInterval'
  | 'thresholdPct'
  | 'criticalPct'
  | 'minFreeSpace'
  | 'cooldownPeriod'
  | 'webhookURL'
  | 'webhookMethod'
  | 'webhookHeaders'
  | 'telegramBotToken'
  | 'telegramChatID'
  | 'weComWebhookURL'
  | 'dingTalkWebhookURL'
  | 'smtpHost'
  | 'smtpPort'
  | 'smtpFrom'
  | 'smtpTo',
  string
>>

interface PreparedNotificationSettings {
  draft: NotificationDraft
  alerts: NonNullable<UpdateSettingsRequest['alerts']>
}

interface NotificationDestination {
  key: string
  label: string
  enabled: boolean
}

const defaultNotificationDraft: NotificationDraft = {
  enabled: false,
  checkInterval: '1h',
  thresholdPct: '90',
  criticalPct: '95',
  minFreeSpace: '10 GB',
  cooldownPeriod: '4h',
  webhookURL: '',
  webhookURLConfigured: false,
  webhookMethod: 'POST',
  webhookHeaders: '',
  webhookHeadersConfigured: false,
  telegramEnabled: false,
  telegramBotToken: '',
  telegramBotTokenConfigured: false,
  telegramBotTokenClear: false,
  telegramChatID: '',
  weComEnabled: false,
  weComWebhookURL: '',
  weComWebhookURLConfigured: false,
  dingTalkEnabled: false,
  dingTalkWebhookURL: '',
  dingTalkWebhookURLConfigured: false,
  emailEnabled: false,
  smtpHost: '',
  smtpPort: '587',
  smtpUsername: '',
  smtpPassword: '',
  smtpPasswordConfigured: false,
  smtpPasswordClear: false,
  smtpFrom: '',
  smtpTo: '',
}

function isAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === 'AbortError'
}

function isPositiveGoDuration(value: string): boolean {
  const trimmed = value.trim()
  if (!goDurationPattern.test(trimmed)) {
    return false
  }
  return (trimmed.match(/\d+(?:\.\d+)?/gu) ?? []).some((part) => Number(part) > 0)
}

function isValidOptionalHTTPURL(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed) {
    return true
  }
  if (/\s/u.test(trimmed) || hasControlCharacter(trimmed) || /\\|%5c/iu.test(trimmed)) {
    return false
  }
  try {
    const parsed = new URL(trimmed)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:'
  } catch {
    return false
  }
}

function hasInvalidHTTPHeaderValueCharacter(value: string): boolean {
  for (const character of value) {
    if (character !== '\t' && hasControlCharacter(character)) {
      return true
    }
  }
  return false
}

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
  return parts !== null
    && httpHeaderNamePattern.test(parts.name)
    && !hasInvalidHTTPHeaderValueCharacter(parts.value)
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
  const savedCounts = redactedWebhookHeaderNameCounts(savedHeadersText)
  for (const header of headers) {
    const parts = splitWebhookHeaderLine(header)
    if (!parts || parts.value !== REDACTED_SETTINGS_SECRET) {
      continue
    }
    const name = parts.name.toLowerCase()
    const remaining = savedCounts.get(name) ?? 0
    if (remaining <= 0) {
      return parts.name
    }
    savedCounts.set(name, remaining - 1)
  }
  return null
}

function redactWebhookHeaderLine(header: string): string {
  const parts = splitWebhookHeaderLine(header)
  return parts ? parts.name + ': ' + REDACTED_SETTINGS_SECRET : REDACTED_SETTINGS_SECRET
}

function splitNonBlankLines(value: string): string[] {
  return value
    .split('\n')
    .map((entry) => entry.trim())
    .filter(Boolean)
}

function splitSMTPRecipients(value: string): string[] {
  return value
    .split(/[\n,]+/u)
    .map((entry) => entry.trim())
    .filter(Boolean)
}

function mapNotificationSettings(
  response: Awaited<ReturnType<typeof getSettings>>,
): NotificationDraft {
  const alerts = response.data.alerts
  if (!alerts) {
    return { ...defaultNotificationDraft }
  }
  return {
    enabled: alerts.enabled,
    checkInterval: alerts.check_interval,
    thresholdPct: String(alerts.threshold_pct),
    criticalPct: String(alerts.critical_pct),
    minFreeSpace: formatBytes(alerts.min_free_bytes),
    cooldownPeriod: alerts.cooldown_period,
    webhookURL: alerts.webhook_url,
    webhookURLConfigured: alerts.webhook_url_configured
      ?? alerts.webhook_url === REDACTED_SETTINGS_SECRET,
    webhookMethod: alerts.webhook_method,
    webhookHeaders: alerts.webhook_headers.join('\n'),
    webhookHeadersConfigured: alerts.webhook_headers_configured
      ?? alerts.webhook_headers.length > 0,
    telegramEnabled: alerts.telegram_enabled ?? false,
    telegramBotToken: '',
    telegramBotTokenConfigured: alerts.telegram_bot_token_configured ?? false,
    telegramBotTokenClear: false,
    telegramChatID: alerts.telegram_chat_id ?? '',
    weComEnabled: alerts.wecom_enabled ?? false,
    weComWebhookURL: alerts.wecom_webhook_url ?? '',
    weComWebhookURLConfigured: alerts.wecom_webhook_url_configured
      ?? alerts.wecom_webhook_url === REDACTED_SETTINGS_SECRET,
    dingTalkEnabled: alerts.dingtalk_enabled ?? false,
    dingTalkWebhookURL: alerts.dingtalk_webhook_url ?? '',
    dingTalkWebhookURLConfigured: alerts.dingtalk_webhook_url_configured
      ?? alerts.dingtalk_webhook_url === REDACTED_SETTINGS_SECRET,
    emailEnabled: alerts.email_enabled ?? false,
    smtpHost: alerts.smtp_host ?? '',
    smtpPort: String(alerts.smtp_port ?? 587),
    smtpUsername: alerts.smtp_username ?? '',
    smtpPassword: '',
    smtpPasswordConfigured: alerts.smtp_password_configured ?? false,
    smtpPasswordClear: false,
    smtpFrom: alerts.smtp_from ?? '',
    smtpTo: alerts.smtp_to?.join('\n') ?? '',
  }
}

function notificationDraftEqual(left: NotificationDraft, right: NotificationDraft): boolean {
  return Object.keys(left).every((key) => (
    left[key as keyof NotificationDraft] === right[key as keyof NotificationDraft]
  ))
}

function validateAndPrepareNotificationSettings(
  draft: NotificationDraft,
  saved: NotificationDraft,
): { prepared?: PreparedNotificationSettings; errors: NotificationValidationErrors } {
  const errors: NotificationValidationErrors = {}
  const checkInterval = draft.checkInterval.trim()
  const cooldownPeriod = draft.cooldownPeriod.trim()
  const thresholdPctText = draft.thresholdPct.trim()
  const criticalPctText = draft.criticalPct.trim()
  const thresholdPct = Number(thresholdPctText)
  const criticalPct = Number(criticalPctText)
  const webhookURL = draft.webhookURL.trim()
  const webhookMethod = draft.webhookMethod.trim().toUpperCase()
  const webhookHeaders = splitNonBlankLines(draft.webhookHeaders)
  const telegramBotToken = draft.telegramBotToken.trim()
  const telegramChatID = draft.telegramChatID.trim()
  const weComWebhookURL = draft.weComWebhookURL.trim()
  const dingTalkWebhookURL = draft.dingTalkWebhookURL.trim()
  const smtpHost = draft.smtpHost.trim()
  const smtpPortText = draft.smtpPort.trim()
  const smtpPort = Number(smtpPortText)
  const smtpUsername = draft.smtpUsername.trim()
  const smtpPassword = draft.smtpPassword.trim()
  const smtpFrom = draft.smtpFrom.trim()
  const smtpTo = splitSMTPRecipients(draft.smtpTo)

  if (!isPositiveGoDuration(checkInterval)) {
    errors.checkInterval = '检查间隔必须使用 1h、30m 这类 Go duration 格式，且大于 0。'
  }
  if (!isPositiveGoDuration(cooldownPeriod)) {
    errors.cooldownPeriod = '冷却时间必须使用 4h、30m 这类 Go duration 格式，且大于 0。'
  }
  if (!/^\d+$/u.test(thresholdPctText) || !Number.isInteger(thresholdPct) || thresholdPct > 100) {
    errors.thresholdPct = '提醒阈值必须是 0 到 100 之间的整数。'
  }
  if (!/^\d+$/u.test(criticalPctText) || !Number.isInteger(criticalPct) || criticalPct > 100) {
    errors.criticalPct = '严重提醒阈值必须是 0 到 100 之间的整数。'
  } else if (!errors.thresholdPct && criticalPct < thresholdPct) {
    errors.criticalPct = '严重提醒阈值不能小于普通提醒阈值。'
  }

  let minFreeBytes = 0
  try {
    minFreeBytes = parseByteSize(draft.minFreeSpace)
    if (!Number.isSafeInteger(minFreeBytes) || minFreeBytes < 0) {
      errors.minFreeSpace = '最小剩余空间必须是 0 或不超过安全范围的整数。'
    }
  } catch {
    errors.minFreeSpace = '请使用 1024、1 KB、1.5 MB 之类的大小格式。'
  }

  if (webhookURL === REDACTED_SETTINGS_SECRET && !draft.webhookURLConfigured) {
    errors.webhookURL = '当前没有已保存的 Webhook URL，请填写真实地址。'
  } else if (webhookURL !== REDACTED_SETTINGS_SECRET && !isValidOptionalHTTPURL(webhookURL)) {
    errors.webhookURL = 'Webhook URL 必须为空，或使用 http/https 的完整地址。'
  }
  if (webhookMethod !== 'GET' && webhookMethod !== 'POST') {
    errors.webhookMethod = 'Webhook 方法必须是 GET 或 POST。'
  }
  const invalidWebhookHeader = webhookHeaders.find((header) => !isValidWebhookHeaderLine(header))
  if (invalidWebhookHeader) {
    errors.webhookHeaders = '每行必须使用合法的 HTTP Header 名称和值。'
  } else {
    const duplicateHeader = findDuplicateWebhookHeaderName(webhookHeaders)
    const unknownRedactedHeader = findUnknownRedactedWebhookHeader(
      webhookHeaders,
      saved.webhookHeaders,
    )
    if (duplicateHeader) {
      errors.webhookHeaders = 'Header ' + duplicateHeader + ' 重复，每个名称只能配置一次。'
    } else if (unknownRedactedHeader) {
      errors.webhookHeaders = 'Header ' + unknownRedactedHeader + ' 没有可保留的已保存值，请填写真实值。'
    }
  }

  if (draft.telegramEnabled) {
    if (draft.telegramBotTokenClear) {
      errors.telegramBotToken = '启用 Telegram 通知时不能清除 Token。'
    } else if (!telegramBotToken && !draft.telegramBotTokenConfigured) {
      errors.telegramBotToken = '首次启用 Telegram 通知时必须填写 Bot Token。'
    }
    if (!telegramChatID) {
      errors.telegramChatID = '启用 Telegram 通知时必须填写 Chat ID 或频道用户名。'
    }
  }
  if (telegramBotToken && /[\s/?#]/u.test(telegramBotToken)) {
    errors.telegramBotToken = 'Bot Token 不能包含空白、/、? 或 #。'
  }
  if (telegramChatID && /\s/u.test(telegramChatID)) {
    errors.telegramChatID = 'Chat ID 或频道用户名不能包含空白字符。'
  }

  if (weComWebhookURL === REDACTED_SETTINGS_SECRET && !draft.weComWebhookURLConfigured) {
    errors.weComWebhookURL = '当前没有已保存的企业微信 Webhook，请填写真实地址。'
  } else if (weComWebhookURL !== REDACTED_SETTINGS_SECRET && !isValidOptionalHTTPURL(weComWebhookURL)) {
    errors.weComWebhookURL = '企业微信 Webhook 必须为空，或使用 http/https 的完整地址。'
  } else if (draft.weComEnabled && !weComWebhookURL && !draft.weComWebhookURLConfigured) {
    errors.weComWebhookURL = '启用企业微信通知时必须填写群机器人 Webhook。'
  }

  if (dingTalkWebhookURL === REDACTED_SETTINGS_SECRET && !draft.dingTalkWebhookURLConfigured) {
    errors.dingTalkWebhookURL = '当前没有已保存的钉钉 Webhook，请填写真实地址。'
  } else if (dingTalkWebhookURL !== REDACTED_SETTINGS_SECRET && !isValidOptionalHTTPURL(dingTalkWebhookURL)) {
    errors.dingTalkWebhookURL = '钉钉 Webhook 必须为空，或使用 http/https 的完整地址。'
  } else if (draft.dingTalkEnabled && !dingTalkWebhookURL && !draft.dingTalkWebhookURLConfigured) {
    errors.dingTalkWebhookURL = '启用钉钉通知时必须填写群机器人 Webhook。'
  }

  if (!/^\d+$/u.test(smtpPortText) || !Number.isInteger(smtpPort) || smtpPort < 1 || smtpPort > 65535) {
    errors.smtpPort = 'SMTP 端口必须是 1 到 65535 之间的整数。'
  }
  if (draft.emailEnabled) {
    if (!smtpHost) {
      errors.smtpHost = '启用邮件通知时必须填写 SMTP 主机。'
    }
    if (!smtpFrom) {
      errors.smtpFrom = '启用邮件通知时必须填写发件人地址。'
    }
    if (smtpTo.length === 0) {
      errors.smtpTo = '启用邮件通知时至少需要一个收件人。'
    }
  }

  if (Object.keys(errors).length > 0) {
    return { errors }
  }

  const normalizedDraft: NotificationDraft = {
    ...draft,
    checkInterval,
    thresholdPct: thresholdPctText,
    criticalPct: criticalPctText,
    minFreeSpace: draft.minFreeSpace.trim(),
    cooldownPeriod,
    webhookURL,
    webhookMethod,
    webhookHeaders: webhookHeaders.join('\n'),
    telegramBotToken,
    telegramChatID,
    weComWebhookURL,
    dingTalkWebhookURL,
    smtpHost,
    smtpPort: smtpPortText,
    smtpUsername,
    smtpPassword,
    smtpFrom,
    smtpTo: smtpTo.join('\n'),
  }
  const alerts: NonNullable<UpdateSettingsRequest['alerts']> = {
    enabled: normalizedDraft.enabled,
    check_interval: checkInterval,
    threshold_pct: thresholdPct,
    critical_pct: criticalPct,
    min_free_bytes: minFreeBytes,
    cooldown_period: cooldownPeriod,
    webhook_url: webhookURL,
    webhook_method: webhookMethod,
    webhook_headers: webhookHeaders,
    telegram_enabled: normalizedDraft.telegramEnabled,
    telegram_chat_id: telegramChatID,
    wecom_enabled: normalizedDraft.weComEnabled,
    wecom_webhook_url: weComWebhookURL,
    dingtalk_enabled: normalizedDraft.dingTalkEnabled,
    dingtalk_webhook_url: dingTalkWebhookURL,
    email_enabled: normalizedDraft.emailEnabled,
    smtp_host: smtpHost,
    smtp_port: smtpPort,
    smtp_username: smtpUsername,
    smtp_from: smtpFrom,
    smtp_to: smtpTo,
    ...(normalizedDraft.telegramBotTokenClear
      ? { telegram_bot_token: '' }
      : telegramBotToken
        ? { telegram_bot_token: telegramBotToken }
        : {}),
    ...(normalizedDraft.smtpPasswordClear
      ? { smtp_password: '' }
      : smtpPassword
        ? { smtp_password: smtpPassword }
        : {}),
  }
  return { prepared: { draft: normalizedDraft, alerts }, errors }
}

function sanitizeSavedNotificationDraft(
  submitted: NotificationDraft,
  alerts: NonNullable<UpdateSettingsRequest['alerts']>,
): NotificationDraft {
  const webhookURL = alerts.webhook_url?.trim() ?? ''
  const weComWebhookURL = alerts.wecom_webhook_url?.trim() ?? ''
  const dingTalkWebhookURL = alerts.dingtalk_webhook_url?.trim() ?? ''
  return {
    ...submitted,
    webhookURL: webhookURL ? REDACTED_SETTINGS_SECRET : '',
    webhookURLConfigured: webhookURL !== '',
    webhookHeaders: alerts.webhook_headers?.length
      ? alerts.webhook_headers.map(redactWebhookHeaderLine).join('\n')
      : '',
    webhookHeadersConfigured: (alerts.webhook_headers?.length ?? 0) > 0,
    telegramBotToken: '',
    telegramBotTokenConfigured: alerts.telegram_bot_token === ''
      ? false
      : submitted.telegramBotTokenConfigured || (alerts.telegram_bot_token?.trim() ?? '') !== '',
    telegramBotTokenClear: false,
    weComWebhookURL: weComWebhookURL ? REDACTED_SETTINGS_SECRET : '',
    weComWebhookURLConfigured: weComWebhookURL !== '',
    dingTalkWebhookURL: dingTalkWebhookURL ? REDACTED_SETTINGS_SECRET : '',
    dingTalkWebhookURLConfigured: dingTalkWebhookURL !== '',
    smtpPassword: '',
    smtpPasswordConfigured: alerts.smtp_password === ''
      ? false
      : submitted.smtpPasswordConfigured || (alerts.smtp_password?.trim() ?? '') !== '',
    smtpPasswordClear: false,
  }
}

function configuredDestinations(draft: NotificationDraft): NotificationDestination[] {
  const destinations: NotificationDestination[] = []
  if (draft.webhookURLConfigured || draft.webhookURL.trim()) {
    destinations.push({ key: 'webhook', label: 'Webhook', enabled: draft.enabled })
  }
  if (draft.weComWebhookURLConfigured || draft.weComWebhookURL.trim()) {
    destinations.push({
      key: 'wecom',
      label: '企业微信',
      enabled: draft.enabled && draft.weComEnabled,
    })
  }
  if (draft.dingTalkWebhookURLConfigured || draft.dingTalkWebhookURL.trim()) {
    destinations.push({
      key: 'dingtalk',
      label: '钉钉',
      enabled: draft.enabled && draft.dingTalkEnabled,
    })
  }
  if (
    draft.smtpHost.trim()
    && draft.smtpFrom.trim()
    && splitSMTPRecipients(draft.smtpTo).length > 0
  ) {
    destinations.push({
      key: 'email',
      label: '邮件',
      enabled: draft.enabled && draft.emailEnabled,
    })
  }
  if (
    draft.telegramChatID.trim()
    && (draft.telegramBotTokenConfigured || draft.telegramBotToken.trim())
    && !draft.telegramBotTokenClear
  ) {
    destinations.push({
      key: 'telegram',
      label: 'Telegram',
      enabled: draft.enabled && draft.telegramEnabled,
    })
  }
  return destinations
}

function hasSavedNotificationDestination(draft: NotificationDraft): boolean {
  if (draft.webhookURLConfigured || draft.webhookURL.trim()) {
    return true
  }
  if (
    draft.telegramEnabled
    && draft.telegramChatID.trim()
    && (draft.telegramBotTokenConfigured || draft.telegramBotToken.trim())
    && !draft.telegramBotTokenClear
  ) {
    return true
  }
  if (
    draft.weComEnabled
    && (draft.weComWebhookURLConfigured || draft.weComWebhookURL.trim())
  ) {
    return true
  }
  if (
    draft.dingTalkEnabled
    && (draft.dingTalkWebhookURLConfigured || draft.dingTalkWebhookURL.trim())
  ) {
    return true
  }
  const smtpPort = Number(draft.smtpPort.trim())
  return draft.emailEnabled
    && draft.smtpHost.trim() !== ''
    && Number.isInteger(smtpPort)
    && smtpPort >= 1
    && smtpPort <= 65535
    && draft.smtpFrom.trim() !== ''
    && splitSMTPRecipients(draft.smtpTo).length > 0
}

function formatAlertChannelSummary(channels: string[]): string {
  const labels: Record<string, string> = {
    webhook: 'Webhook',
    telegram: 'Telegram',
    wecom: '企业微信',
    dingtalk: '钉钉',
    email: 'SMTP 邮件',
  }
  return channels.map((channel) => labels[channel] ?? '未知通道').join(' / ')
}

function getLoadErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof SettingsError && error.isUnavailable) {
    return {
      title: '通知设置暂不可用',
      description: '设置服务当前不可用，请检查设备状态或稍后重试。',
    }
  }
  return {
    title: '加载通知设置失败',
    description: getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION),
  }
}

function getSaveErrorToast(error: unknown): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  if (error instanceof SettingsError && error.isUnavailable) {
    return {
      title: '通知设置暂不可用',
      description: '设置服务当前不可用，当前修改尚未保存。',
      color: 'warning',
    }
  }
  return {
    title: '保存通知设置失败',
    description: getUserFacingErrorDescription(error, GENERIC_ACTION_ERROR_DESCRIPTION),
    color: 'danger',
  }
}

function getTestErrorToast(error: unknown): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  if (error instanceof SettingsError && error.isUnavailable) {
    return {
      title: '提醒服务暂不可用',
      description: '提醒服务当前不可用，请检查设备状态或稍后重试。',
      color: 'warning',
    }
  }
  return {
    title: '测试提醒失败',
    description: getUserFacingErrorDescription(error, GENERIC_ACTION_ERROR_DESCRIPTION),
    color: 'danger',
  }
}

function FieldError({ message }: { message?: string }) {
  return message ? <p className="mt-1 text-xs text-danger" role="alert">{message}</p> : null
}

function DestinationSection({
  title,
  description,
  icon: Icon,
  children,
}: {
  title: string
  description: string
  icon: React.ComponentType<{ size?: number; className?: string; 'aria-hidden'?: boolean | 'true' | 'false' }>
  children: React.ReactNode
}) {
  return (
    <section className="rounded-lg border border-divider bg-content1 p-4 shadow-sm sm:p-5">
      <div className="mb-4 flex items-start gap-3">
        <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
          <Icon size={19} aria-hidden="true" />
        </span>
        <div className="min-w-0">
          <h4 className="text-sm font-semibold text-foreground">{title}</h4>
          <p className="mt-1 text-xs leading-5 text-default-500">{description}</p>
        </div>
      </div>
      {children}
    </section>
  )
}

export function NotificationSettings() {
  const loadAbortControllerRef = useRef<AbortController | null>(null)
  const saveAbortControllerRef = useRef<AbortController | null>(null)
  const testAbortControllerRef = useRef<AbortController | null>(null)
  const destinationsRef = useRef<HTMLDivElement | null>(null)
  const advancedRef = useRef<HTMLDetailsElement | null>(null)
  const [saved, setSaved] = useState<NotificationDraft | null>(null)
  const [draft, setDraft] = useState<NotificationDraft>(defaultNotificationDraft)
  const [validationErrors, setValidationErrors] = useState<NotificationValidationErrors>({})
  const [loadError, setLoadError] = useState<unknown | null>(null)
  const [isLoading, setIsLoading] = useState(true)
  const [isSaving, setIsSaving] = useState(false)
  const [isTesting, setIsTesting] = useState(false)

  const isDirty = saved !== null && !notificationDraftEqual(saved, draft)
  const destinations = useMemo(() => configuredDestinations(draft), [draft])

  const loadSettings = useCallback(async () => {
    loadAbortControllerRef.current?.abort()
    const controller = new AbortController()
    loadAbortControllerRef.current = controller
    setIsLoading(true)
    setLoadError(null)
    try {
      const response = await getSettings({ signal: controller.signal })
      if (controller.signal.aborted || loadAbortControllerRef.current !== controller) {
        return
      }
      const next = mapNotificationSettings(response)
      setSaved(next)
      setDraft(next)
      setValidationErrors({})
    } catch (error) {
      if (controller.signal.aborted || isAbortError(error) || loadAbortControllerRef.current !== controller) {
        return
      }
      setLoadError(error)
    } finally {
      if (loadAbortControllerRef.current === controller) {
        loadAbortControllerRef.current = null
        setIsLoading(false)
      }
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    queueMicrotask(() => {
      if (!cancelled) {
        void loadSettings()
      }
    })
    return () => {
      cancelled = true
      loadAbortControllerRef.current?.abort()
      loadAbortControllerRef.current = null
      saveAbortControllerRef.current?.abort()
      saveAbortControllerRef.current = null
      testAbortControllerRef.current?.abort()
      testAbortControllerRef.current = null
    }
  }, [loadSettings])

  const updateDraft = (update: Partial<NotificationDraft>) => {
    setDraft((current) => ({ ...current, ...update }))
    setValidationErrors({})
  }

  const handleReset = () => {
    if (!saved || isSaving) {
      return
    }
    setDraft(saved)
    setValidationErrors({})
  }

  const handleSave = async () => {
    if (!saved || !isDirty || isSaving) {
      return
    }
    const { prepared, errors } = validateAndPrepareNotificationSettings(draft, saved)
    setValidationErrors(errors)
    if (!prepared) {
      if (
        errors.checkInterval
        || errors.thresholdPct
        || errors.criticalPct
        || errors.minFreeSpace
        || errors.cooldownPeriod
      ) {
        if (advancedRef.current) {
          advancedRef.current.open = true
        }
      }
      addToast({
        title: '通知设置格式无效',
        description: '请修正标记的字段后再保存。',
        color: 'danger',
      })
      return
    }

    saveAbortControllerRef.current?.abort()
    const controller = new AbortController()
    saveAbortControllerRef.current = controller
    setIsSaving(true)
    try {
      const result = await updateSettings({ alerts: prepared.alerts }, { signal: controller.signal })
      if (controller.signal.aborted || saveAbortControllerRef.current !== controller) {
        return
      }
      const sanitized = sanitizeSavedNotificationDraft(prepared.draft, prepared.alerts)
      setSaved(sanitized)
      setDraft(sanitized)
      setValidationErrors({})
      addToast({
        title: result.warning ? '通知设置已保存，但存在警告' : '通知设置已保存',
        description: result.warning && result.message.trim() ? result.message.trim() : undefined,
        color: result.warning ? 'warning' : 'success',
      })
    } catch (error) {
      if (controller.signal.aborted || isAbortError(error) || saveAbortControllerRef.current !== controller) {
        return
      }
      addToast(getSaveErrorToast(error))
    } finally {
      if (saveAbortControllerRef.current === controller) {
        saveAbortControllerRef.current = null
        setIsSaving(false)
      }
    }
  }

  const handleTest = async () => {
    if (!saved || isTesting || isSaving) {
      return
    }
    if (isDirty) {
      addToast({
        title: '需要先保存通知设置',
        description: '测试提醒使用服务端已保存配置，请先保存当前更改。',
        color: 'warning',
      })
      return
    }
    if (!saved.enabled) {
      addToast({
        title: '提醒尚未启用',
        description: '测试提醒会使用服务端已保存配置；请先启用提醒并保存。',
        color: 'warning',
      })
      return
    }
    if (!hasSavedNotificationDestination(saved)) {
      addToast({
        title: '没有可用提醒通道',
        description: '请至少配置 Webhook、Telegram、企业微信、钉钉或邮件通道并保存后再发送测试提醒。',
        color: 'warning',
      })
      return
    }

    testAbortControllerRef.current?.abort()
    const controller = new AbortController()
    testAbortControllerRef.current = controller
    setIsTesting(true)
    try {
      const result = await sendTestAlert({ signal: controller.signal })
      if (controller.signal.aborted || testAbortControllerRef.current !== controller) {
        return
      }
      const channels = formatAlertChannelSummary(result.data.channels)
      const warningDescription = getRedactedDiagnosticMessage(result.message)
      addToast({
        title: result.warning ? '测试提醒已发送，但存在警告' : '测试提醒已发送',
        description: result.warning
          ? warningDescription ?? (channels ? `已发送到 ${channels}` : undefined)
          : channels ? `已发送到 ${channels}` : warningDescription,
        color: result.warning ? 'warning' : 'success',
      })
    } catch (error) {
      if (controller.signal.aborted || isAbortError(error) || testAbortControllerRef.current !== controller) {
        return
      }
      addToast(getTestErrorToast(error))
    } finally {
      if (testAbortControllerRef.current === controller) {
        testAbortControllerRef.current = null
        setIsTesting(false)
      }
    }
  }

  const scrollToDestinations = () => {
    destinationsRef.current?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
    destinationsRef.current?.querySelector<HTMLElement>('input, select, textarea, button')?.focus()
  }

  const loadErrorPresentation = loadError ? getLoadErrorPresentation(loadError) : null

  return (
    <Card id="notification-settings" className="card-mnemonas" aria-label="通知设置">
      <CardHeader className="flex flex-col items-start gap-3 pb-4 lg:flex-row lg:items-center lg:justify-between">
        <div className="flex min-w-0 items-center gap-3">
          <span className="flex h-11 w-11 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
            <BellRing size={21} aria-hidden="true" />
          </span>
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="text-base font-semibold text-foreground">通知与提醒</h3>
              {isDirty && <Chip size="sm" variant="flat" color="warning">有未保存更改</Chip>}
            </div>
            <p className="mt-1 text-xs leading-5 text-default-500">
              选择接收重要设备事件的目的地，并按需调整提醒条件
            </p>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="flat"
            className="rounded-lg"
            startContent={<Send size={16} aria-hidden="true" />}
            isDisabled={isLoading || isSaving}
            isLoading={isTesting}
            onPress={() => { void handleTest() }}
          >
            发送测试提醒
          </Button>
          <Button
            variant="bordered"
            className="rounded-lg"
            startContent={<RotateCcw size={16} aria-hidden="true" />}
            isDisabled={!isDirty || isSaving || isTesting}
            onPress={handleReset}
          >
            重置更改
          </Button>
          <Button
            className="btn-primary rounded-lg"
            startContent={<Save size={16} aria-hidden="true" />}
            isDisabled={!isDirty || isLoading}
            isLoading={isSaving}
            onPress={() => { void handleSave() }}
          >
            保存通知设置
          </Button>
        </div>
      </CardHeader>
      <Divider />
      <CardBody className="gap-6 pt-6">
        {isLoading ? (
          <div className="flex items-center justify-center gap-2 py-10 text-sm text-default-500" role="status">
            <RefreshCw size={20} className="animate-spin" aria-hidden="true" />
            加载通知设置…
          </div>
        ) : loadErrorPresentation ? (
          <div className="flex flex-col items-start gap-4 rounded-lg border border-warning/30 bg-warning/5 p-4 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-start gap-3">
              <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" aria-hidden="true" />
              <div>
                <p className="text-sm font-medium text-foreground">{loadErrorPresentation.title}</p>
                <p className="mt-1 text-xs text-default-600">{loadErrorPresentation.description}</p>
              </div>
            </div>
            <Button variant="bordered" className="rounded-lg" onPress={() => { void loadSettings() }}>
              重新加载
            </Button>
          </div>
        ) : (
          <>
            <section className="rounded-lg border border-primary/20 bg-content2/35 p-5 sm:p-6">
              <div className="flex flex-col gap-5 lg:flex-row lg:items-start lg:justify-between">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                    <div>
                      <h4 className="text-base font-semibold text-foreground">启用提醒</h4>
                      <p className="mt-1 text-xs leading-5 text-default-600">
                        开启后，重要的容量、备份、恢复和磁盘健康事件会发送到已启用目的地。
                      </p>
                    </div>
                    <Switch
                      aria-label="启用提醒"
                      isSelected={draft.enabled}
                      isDisabled={isSaving}
                      onValueChange={(enabled) => updateDraft({ enabled })}
                    >
                      {draft.enabled ? '已启用' : '已关闭'}
                    </Switch>
                  </div>
                  <div className="mt-5 border-t border-divider/70 pt-4">
                    {destinations.length > 0 ? (
                      <>
                        <p className="text-xs font-medium text-default-600">已配置目的地</p>
                        <div className="mt-2 flex flex-wrap gap-2">
                          {destinations.map((destination) => (
                            <Chip
                              key={destination.key}
                              size="sm"
                              variant="flat"
                              color={destination.enabled ? 'success' : 'default'}
                            >
                              {destination.label}{destination.enabled ? '' : ' · 未启用'}
                            </Chip>
                          ))}
                        </div>
                      </>
                    ) : (
                      <>
                        <p className="text-sm font-medium text-foreground">尚未添加目的地</p>
                        <p className="mt-1 text-xs text-default-500">至少配置一种接收方式，提醒才会送达。</p>
                      </>
                    )}
                  </div>
                </div>
                <Button
                  className="btn-primary shrink-0 rounded-lg"
                  startContent={<Plus size={16} aria-hidden="true" />}
                  onPress={scrollToDestinations}
                >
                  添加目的地
                </Button>
              </div>
            </section>

            <div ref={destinationsRef} className="space-y-4 scroll-mt-6" aria-labelledby="notification-destinations-title">
              <div>
                <h4 id="notification-destinations-title" className="text-sm font-semibold text-foreground">通知目的地</h4>
                <p className="mt-1 text-xs text-default-500">每种接收方式独立配置，关闭某一通道不会删除已保存信息。</p>
              </div>

              <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
                <DestinationSection
                  title="Webhook"
                  description="向自建服务或自动化流程发送标准 HTTP 请求"
                  icon={Webhook}
                >
                  <div className="space-y-4">
                    <Input
                      type="url"
                      label="目标 URL"
                      aria-label="Webhook URL"
                      value={draft.webhookURL}
                      placeholder="https://hooks.example.com/alert"
                      isDisabled={isSaving}
                      aria-invalid={validationErrors.webhookURL ? 'true' : undefined}
                      onValueChange={(webhookURL) => updateDraft({ webhookURL })}
                      classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                    />
                    <FieldError message={validationErrors.webhookURL} />
                    <div className="grid grid-cols-1 gap-4 sm:grid-cols-[9rem_1fr]">
                      <div>
                        <label htmlFor="notification-webhook-method" className="mb-1.5 block text-sm text-default-600">请求方法</label>
                        <select
                          id="notification-webhook-method"
                          aria-label="Webhook 方法"
                          value={draft.webhookMethod}
                          disabled={isSaving}
                          onChange={(event) => updateDraft({ webhookMethod: event.target.value })}
                          className="input-shell h-10 w-full rounded-medium bg-transparent px-3 text-sm outline-none"
                        >
                          <option value="POST">POST</option>
                          <option value="GET">GET</option>
                        </select>
                        <FieldError message={validationErrors.webhookMethod} />
                      </div>
                      <div>
                        <label htmlFor="notification-webhook-headers" className="mb-1.5 block text-sm text-default-600">自定义 Header</label>
                        <textarea
                          id="notification-webhook-headers"
                          aria-label="Webhook 自定义 Header"
                          value={draft.webhookHeaders}
                          disabled={isSaving}
                          rows={3}
                          placeholder={'Authorization: Bearer token\nX-MnemoNAS: alerts'}
                          onChange={(event) => updateDraft({ webhookHeaders: event.target.value })}
                          className={cn(
                            'input-shell w-full rounded-medium border border-transparent bg-transparent px-3 py-2 text-sm outline-none focus:border-accent-primary',
                            validationErrors.webhookHeaders && 'border-danger',
                          )}
                        />
                        <p className="mt-1 text-xs text-default-500">每行一个，使用 Key: Value 格式。</p>
                        <FieldError message={validationErrors.webhookHeaders} />
                      </div>
                    </div>
                  </div>
                </DestinationSection>

                <DestinationSection
                  title="企业微信"
                  description="通过企业微信群机器人接收设备提醒"
                  icon={MessageCircleMore}
                >
                  <div className="space-y-4">
                    <Switch
                      aria-label="启用企业微信通知"
                      isSelected={draft.weComEnabled}
                      isDisabled={isSaving}
                      onValueChange={(weComEnabled) => updateDraft({ weComEnabled })}
                    >
                      启用企业微信通知
                    </Switch>
                    <Input
                      type="url"
                      label="群机器人 Webhook"
                      aria-label="企业微信 Webhook URL"
                      value={draft.weComWebhookURL}
                      placeholder="https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=..."
                      isDisabled={isSaving || !draft.weComEnabled}
                      aria-invalid={validationErrors.weComWebhookURL ? 'true' : undefined}
                      onValueChange={(weComWebhookURL) => updateDraft({ weComWebhookURL })}
                      classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                    />
                    <FieldError message={validationErrors.weComWebhookURL} />
                    <p className="text-xs text-default-500">地址保存后只显示脱敏值。</p>
                  </div>
                </DestinationSection>

                <DestinationSection
                  title="钉钉"
                  description="通过钉钉群机器人接收设备提醒"
                  icon={MessageCircleMore}
                >
                  <div className="space-y-4">
                    <Switch
                      aria-label="启用钉钉通知"
                      isSelected={draft.dingTalkEnabled}
                      isDisabled={isSaving}
                      onValueChange={(dingTalkEnabled) => updateDraft({ dingTalkEnabled })}
                    >
                      启用钉钉通知
                    </Switch>
                    <Input
                      type="url"
                      label="群机器人 Webhook"
                      aria-label="钉钉 Webhook URL"
                      value={draft.dingTalkWebhookURL}
                      placeholder="https://oapi.dingtalk.com/robot/send?access_token=..."
                      isDisabled={isSaving || !draft.dingTalkEnabled}
                      aria-invalid={validationErrors.dingTalkWebhookURL ? 'true' : undefined}
                      onValueChange={(dingTalkWebhookURL) => updateDraft({ dingTalkWebhookURL })}
                      classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                    />
                    <FieldError message={validationErrors.dingTalkWebhookURL} />
                    <p className="text-xs text-default-500">地址保存后只显示脱敏值。</p>
                  </div>
                </DestinationSection>

                <DestinationSection
                  title="邮件"
                  description="通过 SMTP 向一个或多个邮箱发送提醒"
                  icon={Mail}
                >
                  <div className="space-y-4">
                    <Switch
                      aria-label="启用邮件通知"
                      isSelected={draft.emailEnabled}
                      isDisabled={isSaving}
                      onValueChange={(emailEnabled) => updateDraft({ emailEnabled })}
                    >
                      启用邮件通知
                    </Switch>
                    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                      <div>
                        <Input
                          label="SMTP 主机"
                          aria-label="SMTP 主机"
                          value={draft.smtpHost}
                          placeholder="smtp.example.com"
                          isDisabled={isSaving || !draft.emailEnabled}
                          aria-invalid={validationErrors.smtpHost ? 'true' : undefined}
                          onValueChange={(smtpHost) => updateDraft({ smtpHost })}
                          classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                        />
                        <FieldError message={validationErrors.smtpHost} />
                      </div>
                      <div>
                        <Input
                          type="number"
                          min={1}
                          max={65535}
                          inputMode="numeric"
                          label="SMTP 端口"
                          aria-label="SMTP 端口"
                          value={draft.smtpPort}
                          isDisabled={isSaving || !draft.emailEnabled}
                          aria-invalid={validationErrors.smtpPort ? 'true' : undefined}
                          onValueChange={(smtpPort) => updateDraft({ smtpPort })}
                          classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                        />
                        <FieldError message={validationErrors.smtpPort} />
                      </div>
                      <Input
                        label="SMTP 用户名"
                        aria-label="SMTP 用户名"
                        value={draft.smtpUsername}
                        placeholder="alerts@example.com"
                        isDisabled={isSaving || !draft.emailEnabled}
                        onValueChange={(smtpUsername) => updateDraft({ smtpUsername })}
                        classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                      />
                      <div>
                        <Input
                          type="password"
                          label="SMTP 密码"
                          aria-label="SMTP 密码"
                          value={draft.smtpPassword}
                          placeholder={draft.smtpPasswordConfigured ? '已配置，留空不变' : '应用专用密码'}
                          isDisabled={isSaving || !draft.emailEnabled || draft.smtpPasswordClear}
                          onValueChange={(smtpPassword) => updateDraft({
                            smtpPassword,
                            smtpPasswordClear: false,
                          })}
                          classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                        />
                        {draft.smtpPasswordConfigured && (
                          <Checkbox
                            isSelected={draft.smtpPasswordClear}
                            isDisabled={isSaving}
                            onValueChange={(smtpPasswordClear) => updateDraft({
                              smtpPasswordClear,
                              smtpPassword: smtpPasswordClear ? '' : draft.smtpPassword,
                            })}
                            classNames={{ label: 'text-xs text-default-600' }}
                          >
                            保存时清除已保存 SMTP 密码
                          </Checkbox>
                        )}
                      </div>
                      <div>
                        <Input
                          label="发件人"
                          aria-label="SMTP 发件人"
                          value={draft.smtpFrom}
                          placeholder="MnemoNAS <alerts@example.com>"
                          isDisabled={isSaving || !draft.emailEnabled}
                          aria-invalid={validationErrors.smtpFrom ? 'true' : undefined}
                          onValueChange={(smtpFrom) => updateDraft({ smtpFrom })}
                          classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                        />
                        <FieldError message={validationErrors.smtpFrom} />
                      </div>
                    </div>
                    <div>
                      <label htmlFor="notification-smtp-to" className="mb-1.5 block text-sm text-default-600">收件人</label>
                      <textarea
                        id="notification-smtp-to"
                        aria-label="SMTP 收件人"
                        value={draft.smtpTo}
                        disabled={isSaving || !draft.emailEnabled}
                        rows={3}
                        placeholder={'admin@example.com\nops@example.com'}
                        onChange={(event) => updateDraft({ smtpTo: event.target.value })}
                        className={cn(
                          'input-shell w-full rounded-medium border border-transparent bg-transparent px-3 py-2 text-sm outline-none focus:border-accent-primary',
                          validationErrors.smtpTo && 'border-danger',
                          !draft.emailEnabled && 'cursor-not-allowed opacity-60',
                        )}
                      />
                      <p className="mt-1 text-xs text-default-500">每行一个，也支持使用逗号分隔。</p>
                      <FieldError message={validationErrors.smtpTo} />
                    </div>
                  </div>
                </DestinationSection>

                <DestinationSection
                  title="Telegram"
                  description="通过 Telegram Bot 向聊天或频道发送提醒"
                  icon={Bot}
                >
                  <div className="space-y-4">
                    <Switch
                      aria-label="启用 Telegram 通知"
                      isSelected={draft.telegramEnabled}
                      isDisabled={isSaving}
                      onValueChange={(telegramEnabled) => updateDraft({ telegramEnabled })}
                    >
                      启用 Telegram 通知
                    </Switch>
                    <div>
                      <Input
                        type="password"
                        label="Bot Token"
                        aria-label="Telegram Bot Token"
                        value={draft.telegramBotToken}
                        placeholder={draft.telegramBotTokenConfigured ? '已配置，留空不变' : '123456:token'}
                        isDisabled={isSaving || !draft.telegramEnabled || draft.telegramBotTokenClear}
                        aria-invalid={validationErrors.telegramBotToken ? 'true' : undefined}
                        onValueChange={(telegramBotToken) => updateDraft({
                          telegramBotToken,
                          telegramBotTokenClear: false,
                        })}
                        classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                      />
                      <FieldError message={validationErrors.telegramBotToken} />
                      {draft.telegramBotTokenConfigured && (
                        <Checkbox
                          isSelected={draft.telegramBotTokenClear}
                          isDisabled={isSaving}
                          onValueChange={(telegramBotTokenClear) => updateDraft({
                            telegramBotTokenClear,
                            telegramBotToken: telegramBotTokenClear ? '' : draft.telegramBotToken,
                          })}
                          classNames={{ label: 'text-xs text-default-600' }}
                        >
                          保存时清除已保存 Telegram Token
                        </Checkbox>
                      )}
                    </div>
                    <div>
                      <Input
                        label="Chat ID 或频道用户名"
                        aria-label="Telegram Chat ID"
                        value={draft.telegramChatID}
                        placeholder="-1001234567890"
                        isDisabled={isSaving || !draft.telegramEnabled}
                        aria-invalid={validationErrors.telegramChatID ? 'true' : undefined}
                        onValueChange={(telegramChatID) => updateDraft({ telegramChatID })}
                        classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                      />
                      <FieldError message={validationErrors.telegramChatID} />
                    </div>
                  </div>
                </DestinationSection>
              </div>
            </div>

            <details
              ref={advancedRef}
              className="group rounded-lg border border-divider bg-content2/25"
            >
              <summary className="flex cursor-pointer list-none items-center gap-3 p-4 marker:hidden focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/35 sm:p-5">
                <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-default-100 text-default-600">
                  <Globe2 size={18} aria-hidden="true" />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block text-sm font-semibold text-foreground">专业参数</span>
                  <span className="mt-1 block text-xs text-default-500">容量阈值、检查频率与重复提醒间隔</span>
                </span>
                <ChevronDown
                  size={18}
                  aria-hidden="true"
                  className="shrink-0 text-default-500 transition-transform group-open:rotate-180"
                />
              </summary>
              <div className="grid grid-cols-1 gap-4 border-t border-divider p-4 sm:grid-cols-2 sm:p-5 lg:grid-cols-5">
                <div>
                  <Input
                    type="number"
                    min={0}
                    max={100}
                    step={1}
                    inputMode="numeric"
                    label="提醒阈值 (%)"
                    aria-label="提醒阈值"
                    value={draft.thresholdPct}
                    isDisabled={isSaving}
                    aria-invalid={validationErrors.thresholdPct ? 'true' : undefined}
                    onValueChange={(thresholdPct) => updateDraft({ thresholdPct })}
                    classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                  />
                  <FieldError message={validationErrors.thresholdPct} />
                </div>
                <div>
                  <Input
                    type="number"
                    min={0}
                    max={100}
                    step={1}
                    inputMode="numeric"
                    label="严重阈值 (%)"
                    aria-label="严重提醒阈值"
                    value={draft.criticalPct}
                    isDisabled={isSaving}
                    aria-invalid={validationErrors.criticalPct ? 'true' : undefined}
                    onValueChange={(criticalPct) => updateDraft({ criticalPct })}
                    classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                  />
                  <FieldError message={validationErrors.criticalPct} />
                </div>
                <div>
                  <Input
                    label="最小剩余空间"
                    aria-label="最小剩余空间"
                    value={draft.minFreeSpace}
                    isDisabled={isSaving}
                    aria-invalid={validationErrors.minFreeSpace ? 'true' : undefined}
                    onValueChange={(minFreeSpace) => updateDraft({ minFreeSpace })}
                    classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                  />
                  <FieldError message={validationErrors.minFreeSpace} />
                </div>
                <div>
                  <Input
                    label="检查间隔"
                    aria-label="提醒检查间隔"
                    value={draft.checkInterval}
                    placeholder="1h"
                    isDisabled={isSaving}
                    aria-invalid={validationErrors.checkInterval ? 'true' : undefined}
                    onValueChange={(checkInterval) => updateDraft({ checkInterval })}
                    classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                  />
                  <FieldError message={validationErrors.checkInterval} />
                </div>
                <div>
                  <Input
                    label="冷却时间"
                    aria-label="提醒冷却时间"
                    value={draft.cooldownPeriod}
                    placeholder="4h"
                    isDisabled={isSaving}
                    aria-invalid={validationErrors.cooldownPeriod ? 'true' : undefined}
                    onValueChange={(cooldownPeriod) => updateDraft({ cooldownPeriod })}
                    classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                  />
                  <FieldError message={validationErrors.cooldownPeriod} />
                </div>
              </div>
            </details>
          </>
        )}
      </CardBody>
    </Card>
  )
}
