import { useState, useCallback, useMemo, useEffect, useRef } from 'react'
import {
  Modal,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalFooter,
  Button,
  Input,
  Select,
  SelectItem,
  Switch,
  addToast,
  Snippet,
} from '@heroui/react'
import {
  Link2,
  AlertCircle,
  Copy,
  Lock,
  Clock,
  Eye,
  Users,
  CheckCircle,
} from 'lucide-react'
import {
  createShare,
  copyShareUrl,
  formatDuration,
  formatShareUrl,
  getSharePolicy,
  ShareError,
  type ShareCreateResult,
  type CreateShareRequest,
  type SharePolicy,
  type SharePolicyRule,
} from '@/api/share'
import { getUserFacingErrorDescription } from '@/lib/apiMessages'
import { hasControlCharacter } from '@/lib/utils'

interface ShareDialogProps {
  isOpen: boolean
  onClose: () => void
  filePath: string
  isFolder?: boolean
  onShareCreated?: (share: ShareCreateResult) => void
  featureEnabled?: boolean
  onFeatureDisabled?: () => void
}

const EXPIRATION_OPTIONS = [
  { value: '', label: '使用系统默认' },
  { value: '1h', label: '1 小时' },
  { value: '24h', label: '24 小时' },
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
  { value: '90d', label: '90 天' },
]

const PERMISSION_OPTIONS = [
  { value: 'read', label: '仅查看', icon: Eye },
]

const maxSharePasswordBytes = 72
const shareCreateWarningTitle = '分享链接已创建，但存在警告'
const shareCreateWarningMessage = '分享链接已创建，但后台记录可能存在延迟，请稍后确认分享列表。'
const shareDurationUnitMs: Record<string, number> = {
  ns: 1 / 1_000_000,
  us: 1 / 1_000,
  'µs': 1 / 1_000,
  ms: 1,
  s: 1_000,
  m: 60_000,
  h: 3_600_000,
  d: 86_400_000,
}

type ShareCreateReviewItem = {
  label: string
  value: string
  tone?: 'default' | 'warning'
}

function formatPolicyDuration(value: string): string {
  const trimmed = value.trim()
  if (isUnlimitedPolicyDuration(trimmed)) {
    return '不过期'
  }
  const hoursMatch = /^(\d+)h(?:0m)?(?:0s)?$/.exec(trimmed)
  if (hoursMatch) {
    const hours = Number(hoursMatch[1])
    if (Number.isInteger(hours) && hours > 0 && hours % 24 === 0) {
      return `${hours / 24} 天`
    }
    return `${hours} 小时`
  }
  const minutesMatch = /^(\d+)m(?:0s)?$/.exec(trimmed)
  if (minutesMatch) {
    return `${Number(minutesMatch[1])} 分钟`
  }
  return formatDuration(trimmed)
}

function isUnlimitedPolicyDuration(value: string): boolean {
  const trimmed = value.trim()
  return !trimmed || trimmed === '0'
}

function parseShareDurationMillis(value: string): number | null {
  const trimmed = value.trim()
  if (!trimmed || trimmed === '0') {
    return null
  }

  let total = 0
  let matched = ''
  const durationPattern = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h|d)/g
  for (const match of trimmed.matchAll(durationPattern)) {
    const unit = match[2]
    const multiplier = shareDurationUnitMs[unit]
    const amount = Number(match[1])
    if (!Number.isFinite(amount) || multiplier === undefined) {
      return null
    }
    total += amount * multiplier
    matched += match[0]
  }

  return matched === trimmed && total > 0 ? total : null
}

function getShareDurationReviewValue(
  explicitDuration: string,
  defaultDuration: string | undefined,
  maxDuration: string | undefined,
): { value: string; capped: boolean } {
  const requestedRaw = explicitDuration || defaultDuration
  if (requestedRaw === undefined) {
    return { value: '系统默认', capped: false }
  }

  const maxMillis = maxDuration ? parseShareDurationMillis(maxDuration) : null
  const requestedMillis = isUnlimitedPolicyDuration(requestedRaw) ? null : parseShareDurationMillis(requestedRaw)
  if (maxDuration && maxMillis !== null && (requestedMillis === null || requestedMillis > maxMillis)) {
    return {
      value: `${formatPolicyDuration(maxDuration)}（路径策略上限）`,
      capped: true,
    }
  }

  return { value: formatPolicyDuration(requestedRaw), capped: false }
}

function getShareAccessReviewValue(
  explicitMaxAccess: string,
  parsedMaxAccess: { maxAccess?: number; error?: string },
  defaultMaxAccess: number | undefined,
  policyMaxAccess: number | undefined,
): { value: string; capped: boolean } {
  if (parsedMaxAccess.error) {
    return { value: '格式待修正', capped: false }
  }

  const hasExplicitMaxAccess = explicitMaxAccess.trim() !== ''
  const requestedMaxAccess = hasExplicitMaxAccess ? (parsedMaxAccess.maxAccess ?? 0) : defaultMaxAccess
  if (requestedMaxAccess === undefined) {
    return { value: '系统默认', capped: false }
  }

  if (policyMaxAccess && policyMaxAccess > 0 && (requestedMaxAccess === 0 || requestedMaxAccess > policyMaxAccess)) {
    return {
      value: `${policyMaxAccess} 次（路径策略上限）`,
      capped: true,
    }
  }

  return {
    value: requestedMaxAccess > 0 ? `${requestedMaxAccess} 次` : '不限制',
    capped: false,
  }
}

function getSharePasswordReviewValue(
  usePassword: boolean,
  policyRequiresPassword: boolean,
  password: string,
): ShareCreateReviewItem {
  if (policyRequiresPassword) {
    return {
      label: '密码',
      value: password.trim() ? '已设置，满足路径策略' : '必须设置密码',
      tone: password.trim() ? 'default' : 'warning',
    }
  }

  if (usePassword) {
    return {
      label: '密码',
      value: password.trim() ? '已设置' : '待输入',
      tone: password.trim() ? 'default' : 'warning',
    }
  }

  return { label: '密码', value: '不设置密码', tone: 'warning' }
}

function getSharePathDepth(rawPath: string): number {
  const trimmed = rawPath.trim().replace(/^\/+|\/+$/g, '')
  if (!trimmed) {
    return 0
  }
  return trimmed.split('/').filter(Boolean).length
}

function normalizeSharePolicyPath(value: string): string {
  const trimmed = value.trim()
  if (!trimmed) {
    return '/'
  }
  const withLeadingSlash = trimmed.startsWith('/') ? trimmed : `/${trimmed}`
  const parts: string[] = []
  for (const segment of withLeadingSlash.split('/')) {
    if (!segment || segment === '.') {
      continue
    }
    if (segment === '..') {
      parts.pop()
      continue
    }
    parts.push(segment)
  }
  return parts.length === 0 ? '/' : `/${parts.join('/')}`
}

function cleanSharePolicyRulePath(value: string): string | null {
  const trimmed = value.trim()
  if (!trimmed || !trimmed.startsWith('/')) {
    return null
  }
  if (/[\\?#]/.test(trimmed) || hasControlCharacter(trimmed)) {
    return null
  }
  if (trimmed.split('/').some((segment) => segment === '.' || segment === '..')) {
    return null
  }
  return normalizeSharePolicyPath(trimmed)
}

function sharePathWithinBase(basePath: string, targetPath: string): boolean {
  const normalizedBase = cleanSharePolicyRulePath(basePath)
  if (!normalizedBase) {
    return false
  }
  const normalizedTarget = normalizeSharePolicyPath(targetPath)
  if (normalizedBase === '/') {
    return normalizedTarget.startsWith('/')
  }
  return normalizedTarget === normalizedBase || normalizedTarget.startsWith(`${normalizedBase}/`)
}

function matchSharePolicyRule(rules: SharePolicyRule[] | undefined, targetPath: string): SharePolicyRule | null {
  if (!rules || rules.length === 0) {
    return null
  }
  let matched: SharePolicyRule | null = null
  let matchedPathLength = -1
  for (const rule of rules) {
    const rulePath = cleanSharePolicyRulePath(rule.path)
    if (!rulePath || !sharePathWithinBase(rulePath, targetPath)) {
      continue
    }
    if (rulePath.length > matchedPathLength) {
      matched = { ...rule, path: rulePath }
      matchedPathLength = rulePath.length
    }
  }
  return matched
}

function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length
}

function parseShareMaxAccessInput(value: string): { maxAccess?: number; error?: string } {
  const trimmed = value.trim()
  if (!trimmed) {
    return {}
  }
  if (!/^\d+$/.test(trimmed)) {
    return { error: '访问次数必须是 0 或正整数' }
  }

  const parsed = Number(trimmed)
  if (!Number.isSafeInteger(parsed)) {
    return { error: '访问次数过大' }
  }
  return { maxAccess: parsed }
}

function getShareDialogActionErrorToast(error: unknown): {
  title: string
  description?: string
  color: 'warning' | 'danger'
} {
  if (error instanceof ShareError) {
    if (error.isNotFound) {
      return {
        title: '分享目标已不存在',
        description: '该文件或文件夹可能已被移动或删除，请刷新列表后重试。',
        color: 'warning',
      }
    }

    if (error.isFeatureDisabled) {
      return {
        title: '分享功能已关闭',
        color: 'warning',
      }
    }

    if (error.isPolicyPasswordRequired) {
      return {
        title: '该路径要求设置分享密码',
        description: '请启用密码保护后再创建分享链接。',
        color: 'warning',
      }
    }

    if (error.isPolicyPrincipalForbidden) {
      return {
        title: '当前账号不能分享该路径',
        description: '该路径的分享策略限制了允许创建或维护分享链接的用户、组或角色。',
        color: 'warning',
      }
    }

    if (error.isUnavailable) {
      return {
        title: '创建分享暂不可用',
        description: '分享服务当前不可用，请检查设备状态或稍后重试。',
        color: 'warning',
      }
    }
  }

  return {
    title: '创建分享失败',
    description: getUserFacingErrorDescription(error),
    color: 'danger',
  }
}

function getShareCreateSuccessToast(share: { warning: boolean }): {
  title: string
  color: 'success' | 'warning'
} {
  return share.warning
    ? { title: shareCreateWarningTitle, color: 'warning' }
    : { title: '分享链接已创建', color: 'success' }
}

function isAbortError(error: unknown): boolean {
  return typeof error === 'object'
    && error !== null
    && 'name' in error
    && (error as { name?: unknown }).name === 'AbortError'
}

export function ShareDialog({ 
  isOpen, 
  onClose, 
  filePath, 
  isFolder = false,
  onShareCreated,
  featureEnabled = true,
  onFeatureDisabled,
}: ShareDialogProps) {
  const [isLoading, setIsLoading] = useState(false)
  const [createdShare, setCreatedShare] = useState<ShareCreateResult | null>(null)
  const [featureDisabled, setFeatureDisabled] = useState(false)
  const [sharePolicy, setSharePolicy] = useState<SharePolicy | null>(null)
  const [isPolicyLoading, setIsPolicyLoading] = useState(false)
  const createSessionRef = useRef(0)
  const currentFilePathRef = useRef(filePath)
  const currentOpenRef = useRef(isOpen)
  const createAbortControllerRef = useRef<AbortController | null>(null)
  
  // Form state
  const [usePassword, setUsePassword] = useState(false)
  const [password, setPassword] = useState('')
  const [expiresIn, setExpiresIn] = useState('')
  const [permission, setPermission] = useState<'read'>('read')
  const [maxAccess, setMaxAccess] = useState('')
  const [description, setDescription] = useState('')

  const matchedPolicyRule = useMemo(() => (
    matchSharePolicyRule(sharePolicy?.policy_rules, filePath)
  ), [filePath, sharePolicy?.policy_rules])
  const policyRequiresPassword = Boolean(matchedPolicyRule?.require_password)
  const passwordRequired = usePassword || policyRequiresPassword
  const passwordRequiredButEmpty = passwordRequired && password.trim() === ''
  const passwordTooLong = passwordRequired && utf8ByteLength(password) > maxSharePasswordBytes
  const passwordErrorMessage = passwordRequiredButEmpty
    ? policyRequiresPassword
      ? '当前路径要求设置分享密码'
      : '启用密码保护后必须输入密码'
    : passwordTooLong
      ? '分享密码最多 72 字节'
      : undefined
  const parsedMaxAccess = useMemo(() => parseShareMaxAccessInput(maxAccess), [maxAccess])
  const maxAccessInvalid = Boolean(parsedMaxAccess.error)

  const shareUrl = useMemo(() => {
    if (!createdShare) return ''
    return formatShareUrl(createdShare.url)
  }, [createdShare])

  const resetForm = useCallback(() => {
    setUsePassword(false)
    setPassword('')
    setExpiresIn('')
    setPermission('read')
    setMaxAccess('')
    setDescription('')
    setCreatedShare(null)
    setFeatureDisabled(false)
    setSharePolicy(null)
    setIsPolicyLoading(false)
  }, [])

  useEffect(() => () => {
    createAbortControllerRef.current?.abort()
    createAbortControllerRef.current = null
  }, [])

  const handleClose = useCallback(() => {
    if (isLoading) {
      return
    }
    resetForm()
    onClose()
  }, [isLoading, onClose, resetForm])

  useEffect(() => {
    const wasOpen = currentOpenRef.current
    const previousFilePath = currentFilePathRef.current

    currentOpenRef.current = isOpen
    if (!isOpen) {
      createAbortControllerRef.current?.abort()
      createAbortControllerRef.current = null
      return
    }

    currentFilePathRef.current = filePath
    if (wasOpen && previousFilePath === filePath) {
      return
    }

    createSessionRef.current += 1
    createAbortControllerRef.current?.abort()
    createAbortControllerRef.current = null
    let cancelled = false
    queueMicrotask(() => {
      if (cancelled) return
      resetForm()
      setIsLoading(false)
    })

    return () => {
      cancelled = true
    }
  }, [filePath, isOpen, resetForm])

  useEffect(() => {
    if (!isOpen || !featureEnabled || featureDisabled) {
      return
    }

    const sessionId = createSessionRef.current
    const controller = new AbortController()
    let cancelled = false
    queueMicrotask(() => {
      if (!cancelled && currentOpenRef.current && createSessionRef.current === sessionId) {
        setIsPolicyLoading(true)
      }
    })
    getSharePolicy({ signal: controller.signal })
      .then((policy) => {
        if (!cancelled && currentOpenRef.current && createSessionRef.current === sessionId) {
          setSharePolicy(policy)
        }
      })
      .catch(() => {
        if (!cancelled && currentOpenRef.current && createSessionRef.current === sessionId) {
          setSharePolicy(null)
        }
      })
      .finally(() => {
        if (!cancelled && currentOpenRef.current && createSessionRef.current === sessionId) {
          setIsPolicyLoading(false)
        }
      })

    return () => {
      cancelled = true
      controller.abort()
    }
  }, [featureDisabled, featureEnabled, filePath, isOpen])

  const effectivePolicyText = useMemo(() => {
    if (isPolicyLoading && !sharePolicy) {
      return {
        expiresIn: '读取中',
        maxAccess: '读取中',
      }
    }
    return {
      expiresIn: formatPolicyDuration(sharePolicy?.default_expires_in ?? ''),
      maxAccess: sharePolicy && sharePolicy.default_max_access > 0
        ? `${sharePolicy.default_max_access} 次`
        : '不限制',
    }
  }, [isPolicyLoading, sharePolicy])

  const riskWarnings = useMemo(() => {
    const warnings: string[] = []
    if (!passwordRequired) {
      warnings.push('未设置密码，拿到链接的人都能访问。')
    }
    if (expiresIn === '' && sharePolicy && isUnlimitedPolicyDuration(sharePolicy.default_expires_in)) {
      warnings.push('系统默认不设置过期时间。')
    }
    if (maxAccess.trim() === '0') {
      warnings.push('已选择不限制访问次数。')
    } else if (maxAccess === '' && sharePolicy && sharePolicy.default_max_access === 0) {
      warnings.push('系统默认不限制访问次数。')
    }
    if (isFolder && getSharePathDepth(filePath) <= 1) {
      warnings.push(filePath.trim() === '/' ? '根目录分享会公开整个文件空间。' : '顶层文件夹分享可能覆盖较多内容。')
    }
    return warnings
  }, [expiresIn, filePath, isFolder, maxAccess, passwordRequired, sharePolicy])

  const matchedPolicyDescriptions = useMemo(() => {
    if (!matchedPolicyRule) {
      return []
    }
    const descriptions: string[] = []
    if (matchedPolicyRule.require_password) {
      descriptions.push('此路径要求设置分享密码。')
    }
    if (matchedPolicyRule.max_expires_in) {
      descriptions.push(`有效期最多 ${formatPolicyDuration(matchedPolicyRule.max_expires_in)}。`)
    }
    if (matchedPolicyRule.max_access && matchedPolicyRule.max_access > 0) {
      descriptions.push(`访问次数最多 ${matchedPolicyRule.max_access} 次。`)
    }
    return descriptions
  }, [matchedPolicyRule])
  const createReviewItems = useMemo<ShareCreateReviewItem[]>(() => {
    const durationReview = getShareDurationReviewValue(
      expiresIn,
      sharePolicy?.default_expires_in,
      matchedPolicyRule?.max_expires_in,
    )
    const accessReview = getShareAccessReviewValue(
      maxAccess,
      parsedMaxAccess,
      sharePolicy?.default_max_access,
      matchedPolicyRule?.max_access,
    )

    return [
      {
        label: '策略来源',
        value: matchedPolicyRule ? `路径策略 ${matchedPolicyRule.path}` : '系统默认',
      },
      getSharePasswordReviewValue(usePassword, policyRequiresPassword, password),
      {
        label: '有效期',
        value: durationReview.value,
        tone: durationReview.capped ? 'warning' : 'default',
      },
      {
        label: '访问次数',
        value: accessReview.value,
        tone: accessReview.capped ? 'warning' : 'default',
      },
    ]
  }, [
    expiresIn,
    matchedPolicyRule,
    maxAccess,
    parsedMaxAccess,
    password,
    policyRequiresPassword,
    sharePolicy,
    usePassword,
  ])

  const handleCreate = async () => {
    if (featureDisabled || !featureEnabled) return
    if (passwordRequiredButEmpty) {
      addToast({
        title: policyRequiresPassword ? '该路径要求设置分享密码' : '请输入分享密码',
        description: policyRequiresPassword ? '当前目录的分享策略要求设置访问密码。' : '启用密码保护后，必须设置访问密码。',
        color: 'warning',
      })
      return
    }
    if (passwordTooLong) {
      addToast({
        title: '分享密码过长',
        description: '分享密码最多 72 字节。',
        color: 'warning',
      })
      return
    }
    if (parsedMaxAccess.error) {
      addToast({
        title: '访问次数格式无效',
        description: parsedMaxAccess.error,
        color: 'warning',
      })
      return
    }

    const sessionId = createSessionRef.current
    const requestPath = filePath
    const isCurrentCreateRequest = () => (
      createSessionRef.current === sessionId
      && currentOpenRef.current
      && currentFilePathRef.current === requestPath
    )
    createAbortControllerRef.current?.abort()
    const controller = new AbortController()
    createAbortControllerRef.current = controller

    setIsLoading(true)
    try {
      const req: CreateShareRequest = {
        path: filePath,
        type: isFolder ? 'folder' : 'file',
        permission,
      }
      
      if (passwordRequired && password) {
        req.password = password
      }
      if (expiresIn) {
        req.expires_in = expiresIn
      }
      if (parsedMaxAccess.maxAccess !== undefined) {
        req.max_access = parsedMaxAccess.maxAccess
      }
      if (description.trim()) {
        req.description = description.trim()
      }

      const share = await createShare(req, { signal: controller.signal })
      if (controller.signal.aborted || !isCurrentCreateRequest()) {
        return
      }
      setCreatedShare(share)
      onShareCreated?.(share)
      addToast(getShareCreateSuccessToast(share))
    } catch (err) {
      if (controller.signal.aborted || isAbortError(err) || !isCurrentCreateRequest()) {
        return
      }

      if (err instanceof ShareError && err.isFeatureDisabled) {
        setFeatureDisabled(true)
        onFeatureDisabled?.()
        addToast(getShareDialogActionErrorToast(err))
        return
      }
      addToast(getShareDialogActionErrorToast(err))
    } finally {
      if (createAbortControllerRef.current === controller) {
        createAbortControllerRef.current = null
      }
      if (isCurrentCreateRequest()) {
        setIsLoading(false)
      }
    }
  }

  const handleCopy = async () => {
    if (!createdShare) return
    try {
      await copyShareUrl(createdShare)
      addToast({ title: '链接已复制', color: 'success' })
    } catch {
      addToast({ title: '复制失败', color: 'danger' })
    }
  }

  return (
    <Modal 
      isOpen={isOpen} 
      onClose={handleClose}
      placement="center"
      size="lg"
      classNames={{
        base: "bg-content1 border border-divider shadow-xl rounded-lg",
        backdrop: "bg-black/60 backdrop-blur-md",
        closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        header: "border-b border-divider",
        footer: "border-t border-divider",
      }}
    >
      <ModalContent>
        <ModalHeader className="flex items-center gap-2">
          <Link2 size={20} className="text-accent-primary" />
          <span>{isFolder ? '分享文件夹' : '分享文件'}</span>
        </ModalHeader>
        
        <ModalBody className="py-6">
          {/* File info */}
          <div className="p-3 bg-content2 rounded-lg border border-divider mb-4">
            <div className="text-sm text-default-500">分享路径</div>
            <div className="font-medium text-foreground truncate">{filePath}</div>
          </div>

          {createdShare ? (
            /* Share created - show link */
            <div className="space-y-4">
              <div className={`flex items-center gap-2 ${createdShare.warning ? 'text-warning' : 'text-success'}`}>
                <CheckCircle size={20} />
                <span className="font-medium">{createdShare.warning ? shareCreateWarningTitle : '分享链接已创建'}</span>
              </div>

              {createdShare.warning && (
                <div className="rounded-lg border border-warning/30 bg-warning/10 p-3 text-sm text-warning">
                  {shareCreateWarningMessage}
                </div>
              )}
              
              <Snippet 
                symbol="" 
                variant="bordered"
                classNames={{
                  base: "w-full bg-content2 border-divider",
                  pre: "text-foreground",
                }}
              >
                {shareUrl}
              </Snippet>

              <Button
                className="w-full rounded-lg"
                color="primary"
                startContent={<Copy size={16} />}
                onPress={handleCopy}
              >
                复制链接
              </Button>

              {createdShare.has_password && (
                <div className="rounded-lg border border-warning/30 bg-warning/10 p-3">
                  <div className="flex items-center gap-2 text-sm text-warning">
                    <Lock size={16} />
                    <span>此链接需要密码才能访问</span>
                  </div>
                </div>
              )}
            </div>
          ) : featureDisabled || !featureEnabled ? (
            <div className="space-y-4">
              <div className="flex items-center gap-2 text-warning">
                <Lock size={20} />
                <span className="font-medium">分享功能已关闭</span>
              </div>

              <div className="p-4 bg-warning/10 border border-warning/30 rounded-lg text-sm text-default-700">
                当前服务已关闭分享功能。重新启用后，才能为文件或文件夹创建分享链接。
              </div>
            </div>
          ) : (
            /* Share form */
            <div className="space-y-6">
              {matchedPolicyDescriptions.length > 0 && (
                <div className="rounded-lg border border-primary/30 bg-primary/10 p-3 text-sm text-default-700">
                  <div className="mb-2 flex items-center gap-2 font-medium text-primary">
                    <AlertCircle size={16} />
                    <span>当前路径分享规则</span>
                  </div>
                  <ul className="space-y-1">
                    {matchedPolicyDescriptions.map((description) => (
                      <li key={description}>{description}</li>
                    ))}
                  </ul>
                </div>
              )}

              {/* Password protection */}
              <div className="space-y-3">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Lock size={16} className="text-default-500" />
                    <span className="text-sm font-medium">密码保护</span>
                  </div>
                  <Switch
                    aria-label="启用密码保护"
                    isSelected={usePassword || policyRequiresPassword}
                    isDisabled={policyRequiresPassword}
                    onValueChange={setUsePassword}
                    size="sm"
                  />
                </div>
                {(usePassword || policyRequiresPassword) && (
                  <div className="space-y-2">
                    <Input
                      aria-label="分享访问密码"
                      type="password"
                      placeholder="设置访问密码（最多 72 字节）"
                      value={password}
                      onValueChange={setPassword}
                      isInvalid={passwordRequiredButEmpty || passwordTooLong}
                      errorMessage={passwordErrorMessage}
                      classNames={{
                        inputWrapper: "bg-content2 border-divider",
                      }}
                    />
                    <p className="text-xs text-default-500">启用后，访问此分享链接必须先输入密码。</p>
                  </div>
                )}
              </div>

              {/* Expiration */}
              <div className="space-y-3">
                <div className="flex items-center gap-2">
                  <Clock size={16} className="text-default-500" />
                  <span className="text-sm font-medium">有效期</span>
                </div>
                <Select
                  aria-label="分享有效期"
                  selectedKeys={[expiresIn]}
                  onSelectionChange={(keys) => setExpiresIn([...keys][0] as string || '')}
                  classNames={{
                    trigger: "bg-content2 border-divider",
                  }}
                >
                  {EXPIRATION_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value}>{opt.label}</SelectItem>
                  ))}
                </Select>
                <p className="text-xs text-default-500">
                  系统默认：{effectivePolicyText.expiresIn}
                </p>
              </div>

              {/* Permission */}
              <div className="space-y-3">
                <div className="flex items-center gap-2">
                  <Eye size={16} className="text-default-500" />
                  <span className="text-sm font-medium">权限</span>
                </div>
                <Select
                  aria-label="分享权限"
                  selectedKeys={[permission]}
                  onSelectionChange={(keys) => setPermission([...keys][0] as 'read' || 'read')}
                  classNames={{
                    trigger: "bg-content2 border-divider",
                  }}
                >
                  {PERMISSION_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value} startContent={<opt.icon size={14} />}>
                      {opt.label}
                    </SelectItem>
                  ))}
                </Select>
              </div>

              {/* Max access count */}
              <div className="space-y-3">
                <div className="flex items-center gap-2">
                  <Users size={16} className="text-default-500" />
                  <span className="text-sm font-medium">访问次数限制</span>
                </div>
                <Input
                  aria-label="分享访问次数限制"
                  type="text"
                  placeholder="使用系统默认"
                  inputMode="numeric"
                  pattern="[0-9]*"
                  value={maxAccess}
                  onValueChange={setMaxAccess}
                  isInvalid={maxAccessInvalid}
                  errorMessage={parsedMaxAccess.error}
                  classNames={{
                    inputWrapper: "bg-content2 border-divider",
                  }}
                />
                <p className="text-xs text-default-500">
                  系统默认：{effectivePolicyText.maxAccess}；0 表示不限制
                </p>
              </div>

              {riskWarnings.length > 0 && (
                <div className="rounded-lg border border-warning/30 bg-warning/10 p-3 text-sm text-default-700">
                  <div className="mb-2 flex items-center gap-2 font-medium text-warning">
                    <AlertCircle size={16} />
                    <span>分享安全提醒</span>
                  </div>
                  <ul className="space-y-1">
                    {riskWarnings.map((warning) => (
                      <li key={warning}>{warning}</li>
                    ))}
                  </ul>
                </div>
              )}

              <div className="rounded-lg border border-divider bg-content2/60 p-3" aria-label="分享创建前复核">
                <div className="mb-2 flex items-center gap-2 text-sm font-medium text-foreground">
                  <CheckCircle size={16} className="text-accent-primary" />
                  <span>创建前复核</span>
                </div>
                <div className="grid gap-2 sm:grid-cols-2">
                  {createReviewItems.map((item) => (
                    <div key={item.label} className="rounded-lg border border-divider bg-content1 px-3 py-2">
                      <div className="text-xs text-default-500">{item.label}</div>
                      <div className={item.tone === 'warning' ? 'mt-1 text-sm font-medium text-warning' : 'mt-1 text-sm font-medium text-foreground'}>
                        {item.value}
                      </div>
                    </div>
                  ))}
                </div>
              </div>

              {/* Description */}
              <div className="space-y-3">
                <span className="text-sm font-medium text-default-600">备注（可选）</span>
                <Input
                  aria-label="分享备注"
                  placeholder="添加备注信息"
                  value={description}
                  onValueChange={setDescription}
                  classNames={{
                    inputWrapper: "bg-content2 border-divider",
                  }}
                />
              </div>
            </div>
          )}
        </ModalBody>

        <ModalFooter>
          {createdShare || featureDisabled || !featureEnabled ? (
            <Button onPress={handleClose} className="rounded-lg">
              关闭
            </Button>
          ) : (
            <>
              <Button variant="flat" onPress={handleClose} isDisabled={isLoading} className="rounded-lg">
                取消
              </Button>
              <Button 
                color="primary" 
                onPress={handleCreate}
                isDisabled={passwordRequiredButEmpty || passwordTooLong || maxAccessInvalid}
                isLoading={isLoading}
                startContent={!isLoading && <Link2 size={16} />}
                className="rounded-lg"
              >
                创建分享链接
              </Button>
            </>
          )}
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}
