import { useState, useCallback, useEffect, useLayoutEffect, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Card,
  CardBody,
  CardHeader,
  Button,
  Input,
  Modal,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalFooter,
  useDisclosure,
  Chip,
  Dropdown,
  DropdownTrigger,
  DropdownMenu,
  DropdownSection,
  DropdownItem,
  Select,
  SelectItem,
  Progress,
  addToast,
} from '@heroui/react'
import {
  Users as UsersIcon,
  UserPlus,
  MoreVertical,
  Shield,
  User as UserIcon,
  UserX,
  KeyRound,
  Trash2,
  Mail,
  Calendar,
  HardDrive,
  RefreshCw,
  AlertCircle,
  Pencil,
  FolderOpen,
  Tags,
  LogOut,
  Copy,
  ListChecks,
  Search,
  X,
  ArrowUpDown,
  Download,
  TrendingUp,
} from 'lucide-react'
import { listUsers, createUser, deleteUser, resetUserPassword, revokeUserSessions, toggleUserStatus, updateUser, UsersError, type ListUsersResponse, type User } from '@/api/users'
import { getStoredUser } from '@/api/auth'
import { formatBytes, formatDate, cn, normalizeUserHomeDir } from '@/lib/utils'
import { formatUserAccessReviewReport, getUserAccessContext, summarizeUserAccessReview } from '@/lib/userAccessContext'
import {
  formatUserQuotaSummaryReport,
  createUserQuotaTrendPoint,
  getQuotaStatus,
  getUserQuotaAggregateStatus,
  mergeUserQuotaTrendHistory,
  normalizeUserQuotaTrendHistory,
  quotaBytesToFormValue,
  quotaFormValueToBytes,
  quotaUnits,
  summarizeUserQuotaTrendHistory,
  summarizeUserQuotas,
  userNeedsQuotaAttention,
  type QuotaUnit,
type UserQuotaAggregateStatus,
  type UserQuotaTrendPoint,
} from '@/lib/userQuota'
import { formatUserAccountAttentionReport, summarizeUserAccountAttention } from '@/lib/userAccountAttention'
import { buildUserListViewCsv, getUserListView, userListExportFilename, type UserListFilter, type UserListSort } from '@/lib/userListView'
import { triggerBrowserDownload } from '@/lib/downloadResponse'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'
import { StatCard } from '@/components/ui/StatCard'
import { getUserFacingErrorDescription } from '@/lib/apiMessages'

const usersUnavailableDescription = '用户配置当前不可用，请检查系统配置状态或稍后重试。'
const usersLoadErrorDescription = '用户列表加载失败，请检查网络或稍后重试。'
const clipboardWriteFailureDescription = '请检查浏览器剪贴板权限。'
const maxPasswordBytes = 72
const groupNamePattern = /^[A-Za-z0-9._-]+$/
const userQuotaTrendHistoryLimit = 8
const userQuotaTrendHistoryStoragePrefix = 'mnemonas:user-quota-trend'

type UsersPageListUsersResponse = ListUsersResponse & {
  quotaTrendHistory: UserQuotaTrendPoint[]
}

function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length
}

function parseGroupNames(value: string): { groups: string[]; error?: string } {
  const groups = value
    .split(/[,\s]+/)
    .map((entry) => entry.trim())
    .filter(Boolean)
    .map((entry) => entry.toLowerCase())
  const seen = new Set<string>()
  const normalized: string[] = []

  for (const group of groups) {
    if (!groupNamePattern.test(group)) {
      return { groups: [], error: '用户组只能包含字母、数字、点、短横线和下划线。' }
    }
    if (!seen.has(group)) {
      seen.add(group)
      normalized.push(group)
    }
  }

  normalized.sort()
  return { groups: normalized }
}

function formatGroupNames(groups: string[] | undefined): string {
  return (groups ?? []).join(', ')
}

function normalizesToRootHomeDir(homeDir: string): boolean {
  const normalized = homeDir.trim().replaceAll('\\', '/')
  return normalized.split('/').filter((segment) => segment && segment !== '.').length === 0
}

function isRootHomeDirForNonAdmin(role: User['role'], homeDir: string): boolean {
  return role !== 'admin' && normalizesToRootHomeDir(homeDir)
}

function getUserListEmptyTitle(filter: UserListFilter, isSearchActive: boolean): string {
  if (isSearchActive) {
    return '没有匹配的用户'
  }
  if (filter === 'account-attention') {
    return '暂无账号关注用户'
  }
  if (filter === 'admin') {
    return '暂无管理员'
  }
  if (filter === 'active') {
    return '暂无活跃用户'
  }
  if (filter === 'disabled-account') {
    return '暂无停用账号'
  }
  if (filter === 'never-login') {
    return '暂无从未登录用户'
  }
  if (filter === 'access-review') {
    return '暂无复核提示用户'
  }
  return '暂无配额关注用户'
}

function getUserListEmptyDescription(filter: UserListFilter, isSearchActive: boolean): string {
  if (isSearchActive) {
    return '请调整搜索关键词，或切换用户列表筛选条件。'
  }
  if (filter === 'account-attention') {
    return '所有用户当前均为启用且已有登录记录。'
  }
  if (filter === 'admin') {
    return '当前还没有管理员账号。'
  }
  if (filter === 'active') {
    return '所有用户当前均处于停用状态。'
  }
  if (filter === 'disabled-account') {
    return '所有用户当前均处于启用状态。'
  }
  if (filter === 'never-login') {
    return '所有用户当前均已有登录记录。'
  }
  if (filter === 'access-review') {
    return '所有用户当前暂无账号、权限或配额复核提示。'
  }
  return '所有已设置配额的用户当前都低于关注阈值。'
}

function getUserListEmptyIcon(filter: UserListFilter, isSearchActive: boolean): typeof Search {
  if (isSearchActive) {
    return Search
  }
  if (filter === 'account-attention' || filter === 'disabled-account') {
    return UserX
  }
  if (filter === 'admin') {
    return Shield
  }
  if (filter === 'active') {
    return UserIcon
  }
  if (filter === 'never-login') {
    return LogOut
  }
  if (filter === 'access-review') {
    return ListChecks
  }
  return AlertCircle
}

function getHomeDirValidationIssue(
  role: User['role'],
  homeDir: string,
  options: { allowEmpty?: boolean } = {}
): { title: string; description?: string } | null {
  const trimmed = homeDir.trim()
  if (!trimmed) {
    return options.allowEmpty ? null : { title: '主目录必须以 / 开头' }
  }
  if (!trimmed.startsWith('/')) {
    return { title: '主目录必须以 / 开头' }
  }
  try {
    normalizeUserHomeDir(trimmed)
  } catch {
    return {
      title: '主目录无效',
      description: '主目录不能包含空字符、. 或 .. 路径段。',
    }
  }
  if (isRootHomeDirForNonAdmin(role, trimmed)) {
    return {
      title: '非管理员主目录不能为 /',
      description: '请选择具体目录，例如 /alice。',
    }
  }
  return null
}

function isMissingUserError(error: unknown): boolean {
  return error instanceof UsersError && (error.status === 404 || error.code === 'USER_NOT_FOUND')
}

function isAbortError(error: unknown): boolean {
  return typeof error === 'object'
    && error !== null
    && 'name' in error
    && (error as { name?: unknown }).name === 'AbortError'
}

function syncMissingUserInCache(queryClient: ReturnType<typeof useQueryClient>, userId: string): boolean {
  let removed = false

  queryClient.setQueriesData<ListUsersResponse | undefined>({ queryKey: ['users'] }, (current) => {
    if (!current) {
      return current
    }

    const users = current.users.filter((user) => user.id !== userId)
    const removedFromCurrent = users.length !== current.users.length
    if (!removedFromCurrent) {
      return current
    }
    removed = true

    return {
      ...current,
      users,
      total: users.length,
    }
  })

  return removed
}

function shallowEqualStringRecord<T extends Record<string, string>>(left: T, right: T): boolean {
  const leftKeys = Object.keys(left)
  if (leftKeys.length !== Object.keys(right).length) {
    return false
  }

  return leftKeys.every((key) => left[key] === right[key])
}

function getUsersLoadErrorPresentation(error: unknown): {
  title: string
  description: string
} {
  if (error instanceof UsersError && error.isUnavailable) {
    return {
      title: '用户管理暂不可用',
      description: usersUnavailableDescription,
    }
  }

  return {
    title: '加载用户列表失败',
    description: getUserFacingErrorDescription(error, usersLoadErrorDescription),
  }
}

function getUsersActionErrorPresentation(
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
  if (error instanceof UsersError && error.isUnavailable) {
    return {
      title: titles.unavailable,
      description: usersUnavailableDescription,
      color: 'warning',
    }
  }

  if (error instanceof UsersError) {
    if (error.code === 'USER_EXISTS') {
      return {
        title: '用户名已存在',
        description: '该用户名已被占用，请使用其他用户名。',
        color: 'warning',
      }
    }

    if (error.code === 'SELF_DISABLE') {
      return {
        title: '不能禁用当前用户',
        description: '当前登录用户不能禁用自身账号。',
        color: 'warning',
      }
    }

    if (error.code === 'LAST_ADMIN') {
      return {
        title: '不能禁用最后一个管理员',
        description: '系统至少需要保留一个启用中的管理员账号。',
        color: 'warning',
      }
    }

    if (error.code === 'PASSWORD_TOO_LONG') {
      return {
        title: '密码过长',
        description: `密码最多 ${maxPasswordBytes} 字节，请缩短后重试。`,
        color: 'warning',
      }
    }

    if (error.code === 'INVALID_HOME_DIR') {
      return {
        title: '主目录无效',
        description: '主目录必须是站内绝对路径；非管理员不能使用 /。',
        color: 'warning',
      }
    }

    if (error.code === 'INVALID_QUOTA') {
      return {
        title: '配额无效',
        description: '配额必须大于或等于 0，0 表示不限额。',
        color: 'warning',
      }
    }

    if (error.code === 'INVALID_GROUPS') {
      return {
        title: '用户组无效',
        description: '用户组只能包含字母、数字、点、短横线和下划线。',
        color: 'warning',
      }
    }

    if (error.code === 'SELF_ROLE_CHANGE') {
      return {
        title: '不能修改当前管理员角色',
        description: '请使用其他管理员账号调整当前账号角色。',
        color: 'warning',
      }
    }
  }

  return {
    title: titles.failure,
    description: getUserFacingErrorDescription(error),
    color: 'danger',
  }
}

function getUsersRefreshErrorPresentation(error: unknown): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  return getUsersActionErrorPresentation(error, {
    unavailable: '用户管理暂不可用',
    failure: '刷新失败',
  })
}

function getUsersActionSuccessToast(
  action: 'create' | 'update' | 'delete' | 'reset-password' | 'revoke-sessions' | 'toggle-status',
  options?: { warning?: boolean; disabled?: boolean }
): {
  title: string
  description?: string
  color: 'success' | 'warning'
} {
  const warningDescription = '操作已提交，但用户配置保存存在提醒，请检查设备状态。'

  switch (action) {
    case 'create':
      return options?.warning
        ? { title: '用户已创建，但保存存在提醒', description: warningDescription, color: 'warning' }
        : { title: '用户创建成功', color: 'success' }
    case 'update':
      return options?.warning
        ? { title: '用户已更新，但保存存在提醒', description: warningDescription, color: 'warning' }
        : { title: '用户已更新', color: 'success' }
    case 'delete':
      return options?.warning
        ? { title: '用户已删除，但保存存在提醒', description: warningDescription, color: 'warning' }
        : { title: '用户已删除', color: 'success' }
    case 'reset-password':
      return options?.warning
        ? { title: '密码已重置，但保存存在提醒', description: warningDescription, color: 'warning' }
        : { title: '密码已重置', color: 'success' }
    case 'revoke-sessions':
      return options?.warning
        ? { title: '登录已失效，但保存存在提醒', description: warningDescription, color: 'warning' }
        : { title: '现有登录已失效', color: 'success' }
    case 'toggle-status':
      if (options?.warning) {
        return {
          title: options.disabled ? '用户已禁用，但保存存在提醒' : '用户已启用，但保存存在提醒',
          description: warningDescription,
          color: 'warning',
        }
      }
      return { title: options?.disabled ? '用户已禁用' : '用户已启用', color: 'success' }
  }
}

function getUserQuotaTrendHistoryStorageKey(userID: string | undefined): string {
  return `${userQuotaTrendHistoryStoragePrefix}:${userID?.trim() || 'anonymous'}`
}

function loadUserQuotaTrendHistory(storageKey: string): UserQuotaTrendPoint[] {
  if (typeof window === 'undefined') {
    return []
  }
  try {
    const raw = window.localStorage.getItem(storageKey)
    if (!raw) {
      return []
    }
    return normalizeUserQuotaTrendHistory(JSON.parse(raw), userQuotaTrendHistoryLimit)
  } catch {
    return []
  }
}

function saveUserQuotaTrendHistory(storageKey: string, history: UserQuotaTrendPoint[]): boolean {
  if (typeof window === 'undefined') {
    return false
  }
  try {
    window.localStorage.setItem(
      storageKey,
      JSON.stringify(normalizeUserQuotaTrendHistory(history, userQuotaTrendHistoryLimit)),
    )
    return true
  } catch {
    return false
  }
}

function updateUserQuotaTrendHistoryForUsers(storageKey: string, users: User[]): UserQuotaTrendPoint[] {
  const current = loadUserQuotaTrendHistory(storageKey)
  if (users.length === 0) {
    return current
  }

  const next = mergeUserQuotaTrendHistory(
    current,
    createUserQuotaTrendPoint(users),
    userQuotaTrendHistoryLimit,
  )
  saveUserQuotaTrendHistory(storageKey, next)
  return next
}

function formatUserQuotaTrendDeltaBytes(bytes: number): string {
  if (bytes > 0) {
    return `+${formatBytes(bytes)}`
  }
  if (bytes < 0) {
    return `-${formatBytes(Math.abs(bytes))}`
  }
  return '0 B'
}

function formatUserQuotaTrendUsage(point: UserQuotaTrendPoint | null): string {
  if (!point) {
    return '--'
  }
  if (point.quotaBytes <= 0) {
    return `${formatBytes(point.usedBytes)} / 未设总配额`
  }
  return `${formatBytes(point.limitedUsedBytes)} / ${formatBytes(point.quotaBytes)}`
}

function getUserQuotaTrendBarWidth(point: UserQuotaTrendPoint, peakLimitedUsedBytes: number): string {
  if (peakLimitedUsedBytes <= 0 || point.limitedUsedBytes <= 0) {
    return '0%'
  }
  return `${Math.max(6, Math.round((point.limitedUsedBytes / peakLimitedUsedBytes) * 100))}%`
}

function getQuotaAggregateBarClass(tone: UserQuotaAggregateStatus['tone']): string {
  if (tone === 'danger') {
    return 'bg-danger/70'
  }
  if (tone === 'warning') {
    return 'bg-warning/70'
  }
  if (tone === 'success') {
    return 'bg-success/70'
  }
  return 'bg-default-300'
}

// Role badge component
function RoleBadge({ role }: { role: string }) {
  const config = {
    admin: { color: 'danger' as const, icon: Shield, label: '管理员' },
    user: { color: 'primary' as const, icon: UserIcon, label: '用户' },
    guest: { color: 'default' as const, icon: UserX, label: '访客' },
  }[role] || { color: 'default' as const, icon: UserIcon, label: role }

  return (
    <Chip
      size="sm"
      color={config.color}
      variant="flat"
      startContent={<config.icon size={12} />}
    >
      {config.label}
    </Chip>
  )
}

// User card component
function UserCard({
  user,
  onEdit,
  onResetPassword,
  onRevokeSessions,
  onDelete,
  onToggleStatus,
  isCurrentUser,
}: {
  user: User
  onEdit: () => void
  onResetPassword: () => void
  onRevokeSessions: () => void
  onDelete: () => void
  onToggleStatus: () => void
  isCurrentUser: boolean
}) {
  const quota = getQuotaStatus(user)
  const accessContext = getUserAccessContext(user)
  const quotaProgressValue = quota.percent === null ? 0 : Math.min(100, Math.max(0, quota.percent))
  const quotaProgressValueText = quota.percent === null
    ? `不限额，已用 ${formatBytes(user.used_bytes)}`
    : `${quota.percent}% 已用，${quota.detail}`

  return (
    <Card className="card-mnemonas">
      <CardBody className="p-4">
        <div className="flex items-start justify-between gap-3">
          <div className="flex min-w-0 items-center gap-3">
            <div className={cn(
              "w-10 h-10 shrink-0 rounded-lg flex items-center justify-center",
              user.role === 'admin' 
                ? "bg-rose/15 text-rose" 
                : "bg-accent-primary/15 text-accent-primary"
            )}>
              <span className="font-semibold text-lg text-current">
                {user.username.charAt(0).toUpperCase()}
              </span>
            </div>
            <div className="min-w-0">
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <span className="truncate font-medium text-foreground">{user.username}</span>
                {isCurrentUser && (
                  <Chip size="sm" variant="flat" color="success">当前用户</Chip>
                )}
                {user.disabled && (
                  <Chip size="sm" variant="flat" color="warning">已禁用</Chip>
                )}
              </div>
              <div className="flex items-center gap-1 mt-0.5">
                <RoleBadge role={user.role} />
              </div>
              {user.groups && user.groups.length > 0 && (
                <div className="mt-2 flex max-w-full flex-wrap gap-1">
                  {user.groups.slice(0, 3).map((group) => (
                    <Chip key={group} size="sm" variant="flat" color="default" className="max-w-full">
                      <span className="max-w-24 truncate">{group}</span>
                    </Chip>
                  ))}
                  {user.groups.length > 3 && (
                    <Chip size="sm" variant="flat" color="default">+{user.groups.length - 3}</Chip>
                  )}
                </div>
              )}
            </div>
          </div>

          <Dropdown placement="bottom-end">
            <DropdownTrigger>
              <Button
                isIconOnly
                variant="light"
                size="sm"
                aria-label={`${user.username} 用户操作`}
                className="text-default-500 rounded-lg"
              >
                <MoreVertical size={16} />
              </Button>
            </DropdownTrigger>
            <DropdownMenu
              aria-label="用户操作"
              classNames={{ base: "bg-content1 border border-divider shadow-lg" }}
            >
              <DropdownSection title="操作">
                <DropdownItem
                  key="edit"
                  startContent={<Pencil size={16} />}
                  onPress={onEdit}
                >
                  编辑用户
                </DropdownItem>
                <DropdownItem
                  key="toggle-status"
                  startContent={user.disabled ? <UserIcon size={16} /> : <UserX size={16} />}
                  onPress={onToggleStatus}
                  isDisabled={isCurrentUser}
                >
                  {user.disabled ? '启用用户' : '禁用用户'}
                </DropdownItem>
                <DropdownItem
                  key="reset-password"
                  startContent={<KeyRound size={16} />}
                  onPress={onResetPassword}
                >
                  重置密码
                </DropdownItem>
                <DropdownItem
                  key="revoke-sessions"
                  startContent={<LogOut size={16} />}
                  onPress={onRevokeSessions}
                  isDisabled={isCurrentUser}
                >
                  让现有登录失效
                </DropdownItem>
              </DropdownSection>
              <DropdownSection>
                <DropdownItem
                  key="delete"
                  startContent={<Trash2 size={16} />}
                  className="text-rose data-[hover=true]:text-rose data-[hover=true]:bg-rose/10"
                  onPress={onDelete}
                  isDisabled={isCurrentUser}
                >
                  删除用户
                </DropdownItem>
              </DropdownSection>
            </DropdownMenu>
          </Dropdown>
        </div>

        <div className="mt-4 space-y-2 text-sm">
          {user.email && (
            <div className="flex min-w-0 items-center gap-2 text-default-500">
              <Mail size={14} className="shrink-0" />
              <span className="truncate">{user.email}</span>
            </div>
          )}
          <div className="flex min-w-0 items-center gap-2 text-default-500">
            <FolderOpen size={14} className="shrink-0" />
            <span className="truncate font-mono">{user.home_dir}</span>
          </div>
          <div className="flex min-w-0 items-start gap-2 text-default-500">
            <Shield size={14} className="mt-0.5 shrink-0" />
            <div className="min-w-0 flex-1">
              <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                <span className="text-xs font-medium text-foreground">{accessContext.scopeLabel}</span>
                <Chip size="sm" variant="flat" color={accessContext.scopeTone}>
                  权限范围
                </Chip>
              </div>
              <p className="mt-1 text-xs text-default-500">{accessContext.scopeDescription}</p>
              {accessContext.reviewHints.length > 0 && (
                <div className="mt-2 flex flex-wrap gap-1" aria-label={`${user.username} 复核提示`}>
                  {accessContext.reviewHints.map((hint) => (
                    <Chip key={hint.key} size="sm" variant="flat" color={hint.tone}>
                      {hint.label}
                    </Chip>
                  ))}
                </div>
              )}
            </div>
          </div>
          <div className="flex min-w-0 items-center gap-2 text-default-500">
            <Calendar size={14} className="shrink-0" />
            <span className="truncate">创建于 {formatDate(user.created_at)}</span>
          </div>
          {user.last_login_at && (
            <div className="flex min-w-0 items-center gap-2 text-default-500">
              <RefreshCw size={14} className="shrink-0" />
              <span className="truncate">最后登录 {formatDate(user.last_login_at)}</span>
            </div>
          )}
          <div className="flex min-w-0 items-center gap-2 text-default-500">
            <HardDrive size={14} className="shrink-0" />
            <span className="truncate">
              已用 {formatBytes(user.used_bytes)}
              {user.quota_bytes > 0 && ` / ${formatBytes(user.quota_bytes)}`}
            </span>
          </div>
          <div className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
            <div className="mb-2 flex min-w-0 items-center justify-between gap-2">
              <div className="min-w-0">
                <div className="text-xs font-medium text-foreground">容量配额状态</div>
                <div className="truncate text-xs text-default-500">{quota.detail}</div>
              </div>
              <Chip size="sm" variant="flat" color={quota.tone}>
                {quota.label}
              </Chip>
            </div>
            {quota.percent === null ? (
              <Progress
                value={0}
                color="default"
                className="h-2 opacity-60"
                aria-label={`${user.username} 未设置用户容量限制`}
                aria-valuetext={quotaProgressValueText}
              />
            ) : (
              <Progress
                value={quotaProgressValue}
                color={quota.tone === 'danger' ? 'danger' : quota.tone === 'warning' ? 'warning' : 'success'}
                className="h-2"
                aria-label={`${user.username} 配额使用率`}
                aria-valuetext={quotaProgressValueText}
              />
            )}
            <div className="mt-1 text-right text-xs text-default-500">
              {quota.percent === null ? '不限额' : `${quota.percent}%`}
            </div>
          </div>
        </div>
      </CardBody>
    </Card>
  )
}

export function UsersPage() {
  const queryClient = useQueryClient()
  const { isOpen: isCreateOpen, onOpen: onCreateOpen, onClose: onCreateClose } = useDisclosure()
  const { isOpen: isEditOpen, onOpen: onEditOpen, onClose: onEditClose } = useDisclosure()
  const { isOpen: isDeleteOpen, onOpen: onDeleteOpen, onClose: onDeleteClose } = useDisclosure()
  const { isOpen: isResetOpen, onOpen: onResetOpen, onClose: onResetClose } = useDisclosure()

  const [editTarget, setEditTarget] = useState<User | null>(null)
  const [editEmail, setEditEmail] = useState('')
  const [editRole, setEditRole] = useState<User['role']>('user')
  const [editGroups, setEditGroups] = useState('')
  const [editHomeDir, setEditHomeDir] = useState('')
  const [editQuotaValue, setEditQuotaValue] = useState('0')
  const [editQuotaUnit, setEditQuotaUnit] = useState<QuotaUnit>('GB')
  const [deleteTarget, setDeleteTarget] = useState<User | null>(null)
  const [resetTarget, setResetTarget] = useState<User | null>(null)
  const [newUsername, setNewUsername] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [newEmail, setNewEmail] = useState('')
  const [newRole, setNewRole] = useState<'admin' | 'user' | 'guest'>('user')
  const [newGroups, setNewGroups] = useState('')
  const [newHomeDir, setNewHomeDir] = useState('')
  const [newQuotaValue, setNewQuotaValue] = useState('0')
  const [newQuotaUnit, setNewQuotaUnit] = useState<QuotaUnit>('GB')
  const [resetPassword, setResetPassword] = useState('')
  const [userListFilter, setUserListFilter] = useState<UserListFilter>('all')
  const [userSearchQuery, setUserSearchQuery] = useState('')
  const [userListSort, setUserListSort] = useState<UserListSort>('default')
  const currentUserId = getStoredUser()?.id ?? 'anonymous'
  const usersQueryKey = ['users', currentUserId] as const
  const quotaTrendHistoryStorageKey = getUserQuotaTrendHistoryStorageKey(currentUserId)
  const createSessionRef = useRef(0)
  const createDraftRef = useRef({
    username: '',
    password: '',
    email: '',
    role: 'user',
    groups: '',
    homeDir: '',
    quotaValue: '0',
    quotaUnit: 'GB',
  })
  const createAbortControllerRef = useRef<AbortController | null>(null)
  const updateAbortControllerRef = useRef<AbortController | null>(null)
  const deleteAbortControllerRef = useRef<AbortController | null>(null)
  const resetPasswordAbortControllerRef = useRef<AbortController | null>(null)
  const revokeSessionsAbortControllerRef = useRef<AbortController | null>(null)
  const toggleStatusAbortControllerRef = useRef<AbortController | null>(null)
  const newHomeDirValidationIssue = getHomeDirValidationIssue(newRole, newHomeDir, { allowEmpty: true })
  const editHomeDirValidationIssue = getHomeDirValidationIssue(editRole, editHomeDir)

  useEffect(() => {
    return () => {
      createAbortControllerRef.current?.abort()
      createAbortControllerRef.current = null
      updateAbortControllerRef.current?.abort()
      updateAbortControllerRef.current = null
      deleteAbortControllerRef.current?.abort()
      deleteAbortControllerRef.current = null
      resetPasswordAbortControllerRef.current?.abort()
      resetPasswordAbortControllerRef.current = null
      revokeSessionsAbortControllerRef.current?.abort()
      revokeSessionsAbortControllerRef.current = null
      toggleStatusAbortControllerRef.current?.abort()
      toggleStatusAbortControllerRef.current = null
    }
  }, [])

  useLayoutEffect(() => {
    createDraftRef.current = {
      username: newUsername,
      password: newPassword,
      email: newEmail,
      role: newRole,
      groups: newGroups,
      homeDir: newHomeDir,
      quotaValue: newQuotaValue,
      quotaUnit: newQuotaUnit,
    }
  }, [newEmail, newGroups, newHomeDir, newPassword, newQuotaUnit, newQuotaValue, newRole, newUsername])

  const { data, isLoading, isRefetching, error, refetch } = useQuery<UsersPageListUsersResponse>({
    queryKey: usersQueryKey,
    queryFn: async ({ signal }) => {
      const result = await listUsers({ signal })
      return {
        ...result,
        quotaTrendHistory: updateUserQuotaTrendHistoryForUsers(quotaTrendHistoryStorageKey, result.users),
      }
    },
  })

  const createMutation = useMutation({
    mutationFn: ({ request, signal }: {
      request: Parameters<typeof createUser>[0]
      submittedDraft: typeof createDraftRef.current
      createSession: number
      signal: AbortSignal
    }) => createUser(request, { signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }
      queryClient.invalidateQueries({ queryKey: usersQueryKey })
      if (
        createSessionRef.current === variables.createSession
        && shallowEqualStringRecord(createDraftRef.current, variables.submittedDraft)
      ) {
        onCreateClose()
        resetCreateForm()
      }
      addToast(getUsersActionSuccessToast('create', { warning: result.warning }))
    },
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) {
        return
      }
      addToast(getUsersActionErrorPresentation(error, {
        unavailable: '创建用户暂不可用',
        failure: '创建失败',
      }))
    },
    onSettled: (_result, _error, variables) => {
      if (createAbortControllerRef.current?.signal === variables?.signal) {
        createAbortControllerRef.current = null
      }
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({ userId, email, role, groups, homeDir, quotaBytes, signal }: {
      userId: string
      email: string
      role: User['role']
      groups: string[]
      homeDir: string
      quotaBytes: number
      signal: AbortSignal
    }) => updateUser(userId, {
      email,
      role,
      groups,
      home_dir: homeDir,
      quota_bytes: quotaBytes,
    }, { signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }
      queryClient.invalidateQueries({ queryKey: usersQueryKey })
      onEditClose()
      setEditTarget(null)
      addToast(getUsersActionSuccessToast('update', { warning: result.warning }))
    },
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) {
        return
      }
      if (isMissingUserError(error)) {
        syncMissingUserInCache(queryClient, variables.userId)
        onEditClose()
        setEditTarget(null)
        addToast({ title: '用户已不存在，已同步更新', color: 'warning' })
        return
      }

      addToast(getUsersActionErrorPresentation(error, {
        unavailable: '更新用户暂不可用',
        failure: '更新失败',
      }))
    },
    onSettled: (_result, _error, variables) => {
      if (updateAbortControllerRef.current?.signal === variables?.signal) {
        updateAbortControllerRef.current = null
      }
    },
  })

  const deleteMutation = useMutation({
    mutationFn: ({ userId, signal }: { userId: string; signal: AbortSignal }) => deleteUser(userId, { signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }
      queryClient.invalidateQueries({ queryKey: usersQueryKey })
      onDeleteClose()
      setDeleteTarget(null)
      addToast(getUsersActionSuccessToast('delete', { warning: result.warning }))
    },
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) {
        return
      }
      if (isMissingUserError(error)) {
        syncMissingUserInCache(queryClient, variables.userId)
        onDeleteClose()
        setDeleteTarget(null)
        addToast({ title: '用户已不存在，已同步更新', color: 'warning' })
        return
      }

      addToast(getUsersActionErrorPresentation(error, {
        unavailable: '删除用户暂不可用',
        failure: '删除失败',
      }))
    },
    onSettled: (_result, _error, variables) => {
      if (deleteAbortControllerRef.current?.signal === variables?.signal) {
        deleteAbortControllerRef.current = null
      }
    },
  })

  const resetPasswordMutation = useMutation({
    mutationFn: ({ userId, password, signal }: { userId: string; password: string; signal: AbortSignal }) =>
      resetUserPassword(userId, { new_password: password }, { signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }
      queryClient.invalidateQueries({ queryKey: usersQueryKey })
      onResetClose()
      setResetTarget(null)
      setResetPassword('')
      addToast(getUsersActionSuccessToast('reset-password', { warning: result.warning }))
    },
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) {
        return
      }
      if (isMissingUserError(error)) {
        syncMissingUserInCache(queryClient, variables.userId)
        onResetClose()
        setResetTarget(null)
        setResetPassword('')
        addToast({ title: '用户已不存在，已同步更新', color: 'warning' })
        return
      }

      addToast(getUsersActionErrorPresentation(error, {
        unavailable: '重置密码暂不可用',
        failure: '重置失败',
      }))
    },
    onSettled: (_result, _error, variables) => {
      if (resetPasswordAbortControllerRef.current?.signal === variables?.signal) {
        resetPasswordAbortControllerRef.current = null
      }
    },
  })

  const revokeSessionsMutation = useMutation({
    mutationFn: ({ userId, signal }: { userId: string; signal: AbortSignal }) => revokeUserSessions(userId, { signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }
      addToast(getUsersActionSuccessToast('revoke-sessions', { warning: result.warning }))
    },
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) {
        return
      }
      if (isMissingUserError(error)) {
        syncMissingUserInCache(queryClient, variables.userId)
        addToast({ title: '用户已不存在，已同步更新', color: 'warning' })
        return
      }

      addToast(getUsersActionErrorPresentation(error, {
        unavailable: '吊销登录暂不可用',
        failure: '吊销登录失败',
      }))
    },
    onSettled: (_result, _error, variables) => {
      if (revokeSessionsAbortControllerRef.current?.signal === variables?.signal) {
        revokeSessionsAbortControllerRef.current = null
      }
    },
  })

  const toggleStatusMutation = useMutation({
    mutationFn: ({ userId, disabled, signal }: { userId: string; disabled: boolean; signal: AbortSignal }) =>
      toggleUserStatus(userId, disabled, { signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) {
        return
      }
      queryClient.invalidateQueries({ queryKey: usersQueryKey })
      addToast(getUsersActionSuccessToast('toggle-status', {
        warning: result.warning,
        disabled: variables.disabled,
      }))
    },
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) {
        return
      }
      if (isMissingUserError(error)) {
        syncMissingUserInCache(queryClient, variables.userId)
        addToast({ title: '用户已不存在，已同步更新', color: 'warning' })
        return
      }

      addToast(getUsersActionErrorPresentation(error, {
        unavailable: '状态更新暂不可用',
        failure: '状态更新失败',
      }))
    },
    onSettled: (_result, _error, variables) => {
      if (toggleStatusAbortControllerRef.current?.signal === variables?.signal) {
        toggleStatusAbortControllerRef.current = null
      }
    },
  })

  const resetCreateForm = useCallback(() => {
    setNewUsername('')
    setNewPassword('')
    setNewEmail('')
    setNewRole('user')
    setNewGroups('')
    setNewHomeDir('')
    setNewQuotaValue('0')
    setNewQuotaUnit('GB')
  }, [])

  const handleOpenCreateModal = useCallback(() => {
    createSessionRef.current += 1
    onCreateOpen()
  }, [onCreateOpen])

  const handleCloseCreateModal = useCallback(() => {
    if (createMutation.isPending) return
    createSessionRef.current += 1
    onCreateClose()
    resetCreateForm()
  }, [createMutation.isPending, onCreateClose, resetCreateForm])

  const handleCreate = useCallback(() => {
    if (!newUsername.trim() || !newPassword.trim()) {
      addToast({ title: '请输入用户名和密码', color: 'warning' })
      return
    }
    if (newPassword.length < 8) {
      addToast({ title: '密码长度至少为 8 位', color: 'warning' })
      return
    }
    if (utf8ByteLength(newPassword) > maxPasswordBytes) {
      addToast({ title: `密码最多 ${maxPasswordBytes} 字节`, color: 'warning' })
      return
    }
    const parsedGroups = parseGroupNames(newGroups)
    if (parsedGroups.error) {
      addToast({ title: '用户组无效', description: parsedGroups.error, color: 'warning' })
      return
    }
    const homeDir = newHomeDir.trim()
    if (newHomeDirValidationIssue) {
      addToast({ ...newHomeDirValidationIssue, color: 'warning' })
      return
    }
    const quotaBytes = quotaFormValueToBytes(newQuotaValue, newQuotaUnit)
    if (quotaBytes == null) {
      addToast({ title: '配额无效', description: '请输入非负数字，0 表示不限额。', color: 'warning' })
      return
    }
    createAbortControllerRef.current?.abort()
    const controller = new AbortController()
    createAbortControllerRef.current = controller
    createMutation.mutate({
      request: {
        username: newUsername.trim(),
        password: newPassword,
        email: newEmail.trim() || undefined,
        role: newRole,
        groups: parsedGroups.groups,
        home_dir: homeDir || undefined,
        quota_bytes: quotaBytes,
      },
      submittedDraft: { ...createDraftRef.current },
      createSession: createSessionRef.current,
      signal: controller.signal,
    })
  }, [newUsername, newPassword, newEmail, newGroups, newHomeDir, newHomeDirValidationIssue, newQuotaUnit, newQuotaValue, newRole, createMutation])

  const handleOpenEditModal = useCallback((user: User) => {
    const quota = quotaBytesToFormValue(user.quota_bytes)
    setEditTarget(user)
    setEditEmail(user.email ?? '')
    setEditRole(user.role)
    setEditGroups(formatGroupNames(user.groups))
    setEditHomeDir(user.home_dir)
    setEditQuotaValue(quota.value)
    setEditQuotaUnit(quota.unit)
    onEditOpen()
  }, [onEditOpen])

  const handleCloseEditModal = useCallback(() => {
    if (updateMutation.isPending) return
    onEditClose()
    setEditTarget(null)
  }, [onEditClose, updateMutation.isPending])

  const handleUpdateUser = useCallback(() => {
    if (!editTarget) return
    const homeDir = editHomeDir.trim()
    if (editHomeDirValidationIssue) {
      addToast({ ...editHomeDirValidationIssue, color: 'warning' })
      return
    }
    const quotaBytes = quotaFormValueToBytes(editQuotaValue, editQuotaUnit)
    if (quotaBytes == null) {
      addToast({ title: '配额无效', description: '请输入非负数字，0 表示不限额。', color: 'warning' })
      return
    }
    const parsedGroups = parseGroupNames(editGroups)
    if (parsedGroups.error) {
      addToast({ title: '用户组无效', description: parsedGroups.error, color: 'warning' })
      return
    }

    updateAbortControllerRef.current?.abort()
    const controller = new AbortController()
    updateAbortControllerRef.current = controller
    updateMutation.mutate({
      userId: editTarget.id,
      email: editEmail.trim(),
      role: editRole,
      groups: parsedGroups.groups,
      homeDir,
      quotaBytes,
      signal: controller.signal,
    })
  }, [editEmail, editGroups, editHomeDir, editHomeDirValidationIssue, editQuotaUnit, editQuotaValue, editRole, editTarget, updateMutation])

  const handleDelete = useCallback(() => {
    if (!deleteTarget) return
    deleteAbortControllerRef.current?.abort()
    const controller = new AbortController()
    deleteAbortControllerRef.current = controller
    deleteMutation.mutate({ userId: deleteTarget.id, signal: controller.signal })
  }, [deleteTarget, deleteMutation])

  const handleResetPassword = useCallback(() => {
    if (!resetTarget) return
    if (!resetPassword.trim()) {
      addToast({ title: '请输入新密码', color: 'warning' })
      return
    }
    if (resetPassword.length < 8) {
      addToast({ title: '新密码长度至少为 8 位', color: 'warning' })
      return
    }
    if (utf8ByteLength(resetPassword) > maxPasswordBytes) {
      addToast({ title: `新密码最多 ${maxPasswordBytes} 字节`, color: 'warning' })
      return
    }
    resetPasswordAbortControllerRef.current?.abort()
    const controller = new AbortController()
    resetPasswordAbortControllerRef.current = controller
    resetPasswordMutation.mutate({ userId: resetTarget.id, password: resetPassword, signal: controller.signal })
  }, [resetTarget, resetPassword, resetPasswordMutation])

  const handleOpenDeleteModal = useCallback((user: User) => {
    setDeleteTarget(user)
    onDeleteOpen()
  }, [onDeleteOpen])

  const handleCloseDeleteModal = useCallback(() => {
    if (deleteMutation.isPending) return
    onDeleteClose()
    setDeleteTarget(null)
  }, [deleteMutation.isPending, onDeleteClose])

  const handleOpenResetModal = useCallback((user: User) => {
    setResetTarget(user)
    setResetPassword('')
    onResetOpen()
  }, [onResetOpen])

  const handleCloseResetModal = useCallback(() => {
    if (resetPasswordMutation.isPending) return
    onResetClose()
    setResetTarget(null)
    setResetPassword('')
  }, [resetPasswordMutation.isPending, onResetClose])

  const handleToggleStatus = useCallback((user: User) => {
    if (user.id === currentUserId) return
    toggleStatusAbortControllerRef.current?.abort()
    const controller = new AbortController()
    toggleStatusAbortControllerRef.current = controller
    toggleStatusMutation.mutate({ userId: user.id, disabled: !user.disabled, signal: controller.signal })
  }, [currentUserId, toggleStatusMutation])

  const handleRevokeSessions = useCallback((user: User) => {
    if (user.id === currentUserId) return
    revokeSessionsAbortControllerRef.current?.abort()
    const controller = new AbortController()
    revokeSessionsAbortControllerRef.current = controller
    revokeSessionsMutation.mutate({ userId: user.id, signal: controller.signal })
  }, [currentUserId, revokeSessionsMutation])

  const handleRefreshUsers = useCallback(async () => {
    const result = await refetch()
    if (result.error) {
      addToast(getUsersRefreshErrorPresentation(result.error))
      return
    }
    addToast({ title: '用户列表已刷新', color: 'success' })
  }, [refetch])

  const handleClearUserListView = useCallback(() => {
    setUserListFilter('all')
    setUserSearchQuery('')
    setUserListSort('default')
  }, [])

  const handleFocusUserListFilter = useCallback((filter: UserListFilter) => {
    setUserListFilter(filter)
    setUserSearchQuery('')
    setUserListSort('default')
  }, [])

  const users = data?.users ?? []
  const totalUsers = data?.total ?? users.length
  const adminCount = users.filter((user) => user.role === 'admin').length
  const activeUserCount = users.filter((user) => !user.disabled).length
  const accountAttentionSummary = summarizeUserAccountAttention(users)
  const accountAttentionCount = accountAttentionSummary.attentionCount
  const quotaSummary = summarizeUserQuotas(users)
  const quotaAggregate = getUserQuotaAggregateStatus(quotaSummary)
  const quotaAggregateProgressValue = quotaAggregate.percent === null ? 0 : Math.min(100, Math.max(0, quotaAggregate.percent))
  const quotaAggregateProgressText = quotaAggregate.percent === null
    ? `未设置总配额，用户总用量 ${formatBytes(quotaSummary.usedBytes)}`
    : `${quotaAggregate.percent}% 已用，${quotaAggregate.detail}`
  const quotaAttentionCount = users.filter(userNeedsQuotaAttention).length
  const accessReviewSummary = summarizeUserAccessReview(users)
  const accessReviewCount = accessReviewSummary.reviewCount
  const userListView = getUserListView(users, userListFilter, userSearchQuery, userListSort)
  const visibleUsers = userListView.users
  const isUserSearchActive = userListView.isSearchActive
  const userAccountAttentionReport = users.length > 0 ? formatUserAccountAttentionReport(users) : ''
  const userQuotaSummaryReport = users.length > 0 ? formatUserQuotaSummaryReport(users) : ''
  const userAccessReviewReport = users.length > 0 ? formatUserAccessReviewReport(users) : ''
  const usersLoadError = error ? getUsersLoadErrorPresentation(error) : null
  const quotaTrendHistory = data?.quotaTrendHistory ?? []
  const quotaTrendSummary = summarizeUserQuotaTrendHistory(quotaTrendHistory)
  const quotaTrendRecentPoints = normalizeUserQuotaTrendHistory(quotaTrendHistory, 4)

  const handleCopyUserAccountAttention = async () => {
    if (!navigator.clipboard?.writeText) {
      addToast({
        title: '无法复制用户账号摘要',
        description: '当前浏览器不支持剪贴板写入。',
        color: 'warning',
      })
      return
    }

    try {
      await navigator.clipboard.writeText(userAccountAttentionReport)
      addToast({ title: '用户账号摘要已复制', color: 'success' })
    } catch {
      addToast({
        title: '无法复制用户账号摘要',
        description: clipboardWriteFailureDescription,
        color: 'danger',
      })
    }
  }

  const handleCopyUserQuotaSummary = async () => {
    if (!navigator.clipboard?.writeText) {
      addToast({
        title: '无法复制用户配额摘要',
        description: '当前浏览器不支持剪贴板写入。',
        color: 'warning',
      })
      return
    }

    try {
      await navigator.clipboard.writeText(userQuotaSummaryReport)
      addToast({ title: '用户配额摘要已复制', color: 'success' })
    } catch {
      addToast({
        title: '无法复制用户配额摘要',
        description: clipboardWriteFailureDescription,
        color: 'danger',
      })
    }
  }

  const handleCopyUserAccessReview = async () => {
    if (!navigator.clipboard?.writeText) {
      addToast({
        title: '无法复制用户权限摘要',
        description: '当前浏览器不支持剪贴板写入。',
        color: 'warning',
      })
      return
    }

    try {
      await navigator.clipboard.writeText(userAccessReviewReport)
      addToast({ title: '用户权限摘要已复制', color: 'success' })
    } catch {
      addToast({
        title: '无法复制用户权限摘要',
        description: clipboardWriteFailureDescription,
        color: 'danger',
      })
    }
  }

  const handleExportVisibleUserList = () => {
    if (visibleUsers.length === 0) {
      addToast({ title: '没有可导出的用户', color: 'warning' })
      return
    }

    const csv = `\uFEFF${buildUserListViewCsv(visibleUsers, {
      summaryText: userListView.summaryText,
      filterLabel: userListView.filterLabel,
      sortLabel: userListView.sortLabel,
      searchQuery: userSearchQuery,
    })}`
    triggerBrowserDownload(new Blob([csv], { type: 'text/csv;charset=utf-8' }), userListExportFilename())
    addToast({
      title: '用户清单已导出',
      description: `已导出当前视图 ${visibleUsers.length} 个用户。`,
      color: 'success',
    })
  }

  return (
    <div className="flex min-h-full flex-col px-4 pb-28 pt-4 sm:px-6 sm:pt-6 lg:h-full lg:min-h-0 lg:pb-6">
      {/* Header */}
      <PageHeader
        title="用户管理"
        subtitle="管理系统用户、权限和配额"
        icon={UsersIcon}
        actions={
          <>
            <Button
              variant="light"
              startContent={<RefreshCw size={16} />}
              onPress={handleRefreshUsers}
              isLoading={isLoading || isRefetching}
              className="text-default-600 rounded-lg"
            >
              刷新
            </Button>
            <Button
              className="bg-accent-primary text-white rounded-lg"
              startContent={<UserPlus size={16} />}
              onPress={handleOpenCreateModal}
            >
              添加用户
            </Button>
          </>
        }
        className="mb-6"
      />

      {/* Stats */}
      <div className="grid grid-cols-2 gap-2 mb-4 sm:gap-3 xl:grid-cols-6">
        <StatCard
          title="总用户数"
          value={totalUsers}
          icon={UsersIcon}
          tone="primary"
          density="compact"
          onPress={() => handleFocusUserListFilter('all')}
          ariaLabel="查看全部用户"
        />
        <StatCard
          title="管理员"
          value={adminCount}
          icon={Shield}
          tone="danger"
          density="compact"
          onPress={() => handleFocusUserListFilter('admin')}
          ariaLabel="查看管理员"
        />
        <StatCard
          title="活跃用户"
          value={activeUserCount}
          icon={UserIcon}
          tone="success"
          density="compact"
          onPress={() => handleFocusUserListFilter('active')}
          ariaLabel="查看活跃用户"
        />
        <StatCard
          title="账号关注"
          value={accountAttentionCount}
          subtitle={accountAttentionCount > 0
            ? `停用 ${accountAttentionSummary.disabledCount} 个 · 从未登录 ${accountAttentionSummary.neverLoggedInCount} 个`
            : '暂无账号复核项'}
          icon={UserX}
          tone={accountAttentionCount > 0 ? 'warning' : 'success'}
          density="compact"
          onPress={() => handleFocusUserListFilter('account-attention')}
          ariaLabel="查看账号关注用户"
        />
        <StatCard
          title="配额关注"
          value={quotaAttentionCount}
          subtitle={quotaAttentionCount > 0 ? `${quotaAttentionCount} 个用户接近或超过上限` : '暂无接近上限用户'}
          icon={AlertCircle}
          tone={quotaAttentionCount > 0 ? 'warning' : 'success'}
          density="compact"
          onPress={() => handleFocusUserListFilter('quota-attention')}
          ariaLabel="查看配额关注用户"
        />
        <StatCard
          title="复核提示"
          value={accessReviewCount}
          subtitle={accessReviewCount > 0
            ? `严重 ${accessReviewSummary.dangerReviewCount} 个 · 提醒 ${accessReviewSummary.warningReviewCount} 个 · 记录 ${accessReviewSummary.noteReviewCount} 个`
            : '暂无复核提示'}
          icon={ListChecks}
          tone={accessReviewCount > 0 ? 'warning' : 'success'}
          density="compact"
          onPress={() => handleFocusUserListFilter('access-review')}
          ariaLabel="查看复核提示用户"
        />
      </div>

      {users.length > 0 && (
        <section
          className="mb-4 rounded-lg border border-divider bg-content1/70 px-4 py-3"
          aria-label="用户配额总览"
        >
          <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
            <div className="min-w-0">
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <HardDrive size={16} className="shrink-0 text-default-500" />
                <h2 className="text-sm font-semibold text-foreground">用户配额总览</h2>
                <Chip size="sm" variant="flat" color={quotaAggregate.tone}>
                  {quotaAggregate.label}
                </Chip>
              </div>
              <p className="mt-1 text-xs text-default-500">{quotaAggregate.detail}</p>
            </div>
            <div className="grid grid-cols-2 gap-3 text-xs sm:grid-cols-4 lg:min-w-[28rem]">
              <div>
                <div className="text-default-500">已设配额</div>
                <div className="mt-1 font-semibold text-foreground">{quotaSummary.limitedCount} 个</div>
              </div>
              <div>
                <div className="text-default-500">未设配额</div>
                <div className="mt-1 font-semibold text-foreground">{quotaSummary.unlimitedCount} 个</div>
              </div>
              <div>
                <div className="text-default-500">受限用量</div>
                <div className="mt-1 font-semibold text-foreground">
                  {formatBytes(quotaSummary.limitedUsedBytes)}
                  {quotaSummary.quotaBytes > 0 && ` / ${formatBytes(quotaSummary.quotaBytes)}`}
                </div>
              </div>
              <div>
                <div className="text-default-500">需复核</div>
                <div className="mt-1 font-semibold text-foreground">
                  {quotaSummary.attentionCount} 个
                </div>
              </div>
            </div>
          </div>
          <div
            role="progressbar"
            aria-label="用户总配额使用率"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={quotaAggregate.percent === null ? undefined : quotaAggregateProgressValue}
            aria-valuetext={quotaAggregateProgressText}
            className={cn('mt-3 h-2 overflow-hidden rounded-full bg-content2', quotaAggregate.percent === null && 'opacity-60')}
          >
            <div
              className={cn('h-full rounded-full transition-all duration-500', getQuotaAggregateBarClass(quotaAggregate.tone))}
              style={{ width: quotaAggregate.percent === null ? '0%' : `${quotaAggregateProgressValue}%` }}
            />
          </div>
        </section>
      )}

      {users.length > 0 && quotaTrendSummary.latest && (
        <section
          className="mb-4 rounded-lg border border-divider bg-content1/70 px-4 py-3"
          aria-label="用户配额趋势"
        >
          <div className="flex flex-col gap-3 xl:flex-row xl:items-start xl:justify-between">
            <div className="min-w-0">
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <TrendingUp size={16} className="shrink-0 text-default-500" />
                <h2 className="text-sm font-semibold text-foreground">用户配额趋势</h2>
                <Chip size="sm" variant="flat" color={quotaTrendSummary.tone}>
                  {quotaTrendSummary.label}
                </Chip>
              </div>
              <p className="mt-1 text-xs text-default-500">{quotaTrendSummary.detail}</p>
            </div>
            <div className="grid grid-cols-2 gap-3 text-xs sm:grid-cols-4 xl:min-w-[34rem]">
              <div>
                <div className="text-default-500">近期快照</div>
                <div className="mt-1 font-semibold text-foreground">{quotaTrendSummary.sampleCount} 次</div>
              </div>
              <div>
                <div className="text-default-500">当前受限用量</div>
                <div className="mt-1 font-semibold text-foreground">
                  {formatUserQuotaTrendUsage(quotaTrendSummary.latest)}
                </div>
              </div>
              <div>
                <div className="text-default-500">较上一快照</div>
                <div className={cn(
                  'mt-1 font-semibold',
                  quotaTrendSummary.limitedUsedDeltaBytes > 0
                    ? 'text-warning'
                    : quotaTrendSummary.limitedUsedDeltaBytes < 0
                      ? 'text-success'
                      : 'text-foreground',
                )}>
                  {quotaTrendSummary.previous
                    ? formatUserQuotaTrendDeltaBytes(quotaTrendSummary.limitedUsedDeltaBytes)
                    : '等待下一快照'}
                </div>
              </div>
              <div>
                <div className="text-default-500">快照峰值</div>
                <div className="mt-1 font-semibold text-foreground">
                  {formatBytes(quotaTrendSummary.peakLimitedUsedBytes)}
                </div>
              </div>
            </div>
          </div>
          {quotaTrendRecentPoints.length > 0 && (
            <div className="mt-3 space-y-2" aria-label="用户配额趋势快照列表">
              {quotaTrendRecentPoints.map((point) => (
                <div key={point.capturedAt} className="grid gap-2 text-xs sm:grid-cols-[9rem_minmax(0,1fr)_8rem] sm:items-center">
                  <div className="text-default-500">{formatDate(point.capturedAt)}</div>
                  <div className="h-2 overflow-hidden rounded-full bg-content2">
                    <div
                      className="h-full rounded-full bg-accent-primary/70"
                      style={{ width: getUserQuotaTrendBarWidth(point, quotaTrendSummary.peakLimitedUsedBytes) }}
                    />
                  </div>
                  <div className="text-right font-medium text-foreground">
                    {formatBytes(point.limitedUsedBytes)}
                    {point.attentionCount > 0 && (
                      <span className="ml-1 text-warning">· 复核 {point.attentionCount}</span>
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      )}

      {/* User List */}
      <Card className="card-mnemonas flex-none overflow-visible lg:min-h-0 lg:flex-1 lg:overflow-hidden">
        <CardHeader className="flex flex-col items-start gap-3 border-b border-divider sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h2 className="font-semibold text-foreground">用户列表</h2>
            <p className="mt-1 text-xs text-default-500">{userListView.summaryText}</p>
          </div>
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            {users.length > 0 && (
              <div className="flex flex-wrap gap-2">
                <Button
                  size="sm"
                  variant="flat"
                  className="w-fit rounded-lg"
                  startContent={<UserX size={14} />}
                  onPress={handleCopyUserAccountAttention}
                >
                  复制账号摘要
                </Button>
                <Button
                  size="sm"
                  variant="flat"
                  className="w-fit rounded-lg"
                  startContent={<Copy size={14} />}
                  onPress={handleCopyUserQuotaSummary}
                >
                  复制配额摘要
                </Button>
                <Button
                  size="sm"
                  variant="flat"
                  className="w-fit rounded-lg"
                  startContent={<Shield size={14} />}
                  onPress={handleCopyUserAccessReview}
                >
                  复制权限摘要
                </Button>
                <Button
                  size="sm"
                  variant="flat"
                  className="w-fit rounded-lg"
                  startContent={<Download size={14} />}
                  isDisabled={visibleUsers.length === 0}
                  onPress={handleExportVisibleUserList}
                >
                  导出当前清单
                </Button>
              </div>
            )}
            <Dropdown placement="bottom-end">
              <DropdownTrigger>
                <Button
                  size="sm"
                  variant={userListView.isSortActive ? 'flat' : 'bordered'}
                  className="w-fit rounded-lg"
                  startContent={<ArrowUpDown size={14} />}
                  aria-label={`排序：${userListView.sortLabel}`}
                >
                  排序：{userListView.sortLabel}
                </Button>
              </DropdownTrigger>
              <DropdownMenu
                aria-label="用户列表排序"
                classNames={{ base: "bg-content1 border border-divider shadow-lg" }}
              >
                <DropdownItem key="default" onPress={() => setUserListSort('default')}>
                  默认顺序
                </DropdownItem>
                <DropdownItem key="username" onPress={() => setUserListSort('username')}>
                  按用户名
                </DropdownItem>
                <DropdownItem key="role" onPress={() => setUserListSort('role')}>
                  按角色
                </DropdownItem>
                <DropdownItem key="quota-used" onPress={() => setUserListSort('quota-used')}>
                  按容量用量
                </DropdownItem>
                <DropdownItem key="last-login" onPress={() => setUserListSort('last-login')}>
                  按最后登录
                </DropdownItem>
              </DropdownMenu>
            </Dropdown>
            <Input
              aria-label="搜索用户"
              placeholder="搜索用户、邮箱、用户组或主目录"
              value={userSearchQuery}
              onValueChange={setUserSearchQuery}
              size="sm"
              variant="bordered"
              startContent={<Search size={14} className="text-default-400" />}
              className="w-full sm:w-72"
              classNames={{
                inputWrapper: "h-9 bg-content1 border-divider data-[focus=true]:!border-accent-primary",
                input: "text-xs",
              }}
            />
            {userListView.hasActiveControls && (
              <Button
                size="sm"
                variant="light"
                className="w-fit rounded-lg text-default-600"
                startContent={<X size={14} />}
                onPress={handleClearUserListView}
              >
                清除筛选
              </Button>
            )}
            <div className="flex flex-wrap rounded-lg border border-divider bg-content2/50 p-1" role="group" aria-label="用户列表筛选">
              <Button
                size="sm"
                variant={userListFilter === 'all' ? 'solid' : 'light'}
                color={userListFilter === 'all' ? 'primary' : 'default'}
                className="rounded-md"
                onPress={() => setUserListFilter('all')}
              >
                全部用户
              </Button>
              <Button
                size="sm"
                variant={userListFilter === 'admin' ? 'solid' : 'light'}
                color={userListFilter === 'admin' ? 'danger' : 'default'}
                className="rounded-md"
                startContent={<Shield size={14} />}
                onPress={() => setUserListFilter('admin')}
              >
                管理员
              </Button>
              <Button
                size="sm"
                variant={userListFilter === 'active' ? 'solid' : 'light'}
                color={userListFilter === 'active' ? 'success' : 'default'}
                className="rounded-md"
                startContent={<UserIcon size={14} />}
                onPress={() => setUserListFilter('active')}
              >
                活跃用户
              </Button>
              <Button
                size="sm"
                variant={userListFilter === 'account-attention' ? 'solid' : 'light'}
                color={userListFilter === 'account-attention' ? 'warning' : 'default'}
                className="rounded-md"
                startContent={<UserX size={14} />}
                onPress={() => setUserListFilter('account-attention')}
              >
                账号关注
              </Button>
              <Button
                size="sm"
                variant={userListFilter === 'disabled-account' ? 'solid' : 'light'}
                color={userListFilter === 'disabled-account' ? 'warning' : 'default'}
                className="rounded-md"
                startContent={<UserX size={14} />}
                onPress={() => setUserListFilter('disabled-account')}
              >
                停用账号
              </Button>
              <Button
                size="sm"
                variant={userListFilter === 'never-login' ? 'solid' : 'light'}
                color={userListFilter === 'never-login' ? 'warning' : 'default'}
                className="rounded-md"
                startContent={<LogOut size={14} />}
                onPress={() => setUserListFilter('never-login')}
              >
                从未登录
              </Button>
              <Button
                size="sm"
                variant={userListFilter === 'quota-attention' ? 'solid' : 'light'}
                color={userListFilter === 'quota-attention' ? 'warning' : 'default'}
                className="rounded-md"
                startContent={<AlertCircle size={14} />}
                onPress={() => setUserListFilter('quota-attention')}
              >
                配额关注
              </Button>
              <Button
                size="sm"
                variant={userListFilter === 'access-review' ? 'solid' : 'light'}
                color={userListFilter === 'access-review' ? 'warning' : 'default'}
                className="rounded-md"
                startContent={<ListChecks size={14} />}
                onPress={() => setUserListFilter('access-review')}
              >
                复核提示
              </Button>
            </div>
          </div>
        </CardHeader>
        <CardBody className="overflow-visible custom-scrollbar lg:overflow-auto">
          {isLoading ? (
            <div className="flex items-center justify-center h-40">
              <div className="text-center">
                <div className="w-10 h-10 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-3" />
                <p className="text-default-500 text-sm">加载用户列表...</p>
              </div>
            </div>
          ) : error ? (
            <div className="flex items-center justify-center h-40">
              <EmptyState
                icon={AlertCircle}
                title={usersLoadError?.title ?? '加载用户列表失败'}
                description={usersLoadError?.description ?? usersLoadErrorDescription}
                action={
                  <Button variant="bordered" className="rounded-lg" onPress={handleRefreshUsers}>
                    重新加载
                  </Button>
                }
              />
            </div>
          ) : !users.length ? (
            <div className="flex items-center justify-center h-40">
              <EmptyState icon={UsersIcon} title="暂无用户" />
            </div>
          ) : !visibleUsers.length ? (
            <div className="flex items-center justify-center h-40">
              <EmptyState
                icon={getUserListEmptyIcon(userListFilter, isUserSearchActive)}
                title={getUserListEmptyTitle(userListFilter, isUserSearchActive)}
                description={getUserListEmptyDescription(userListFilter, isUserSearchActive)}
                action={userListView.hasActiveControls
                  ? (
                    <Button
                      variant="bordered"
                      aria-label="清除空状态用户筛选"
                      className="rounded-lg"
                      onPress={handleClearUserListView}
                    >
                      清除筛选
                    </Button>
                  )
                  : undefined}
              />
            </div>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 p-1">
              {visibleUsers.map((user) => (
                <UserCard
                  key={user.id}
                  user={user}
                  isCurrentUser={user.id === currentUserId}
                  onEdit={() => handleOpenEditModal(user)}
                  onResetPassword={() => handleOpenResetModal(user)}
                  onDelete={() => handleOpenDeleteModal(user)}
                  onToggleStatus={() => handleToggleStatus(user)}
                  onRevokeSessions={() => handleRevokeSessions(user)}
                />
              ))}
            </div>
          )}
        </CardBody>
      </Card>

      {/* Create User Modal */}
      <Modal
        isOpen={isCreateOpen}
        onClose={handleCloseCreateModal}
        size="lg"
        placement="center"
        classNames={{
          base: "bg-content1 border border-divider shadow-xl rounded-lg",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-lg bg-accent-primary/10 text-accent-primary flex items-center justify-center">
              <UserPlus size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">添加用户</h3>
              <p className="text-xs text-default-500 font-normal">创建新的系统用户</p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4 space-y-4">
            <div>
              <Input
                label="用户名"
                aria-label="用户名"
                placeholder="请输入用户名"
                value={newUsername}
                onValueChange={setNewUsername}
                autoFocus
                size="lg"
                variant="bordered"
                labelPlacement="outside"
                classNames={{
                  inputWrapper: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                  input: "text-sm placeholder:text-default-400",
                }}
              />
            </div>
            <div>
              <Input
                type="password"
                label="密码"
                aria-label="密码"
                placeholder="请输入密码（8-72 字节）"
                value={newPassword}
                onValueChange={setNewPassword}
                size="lg"
                variant="bordered"
                labelPlacement="outside"
                classNames={{
                  inputWrapper: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                  input: "text-sm placeholder:text-default-400",
                }}
              />
            </div>
            <div>
              <Input
                type="email"
                label="邮箱（可选）"
                aria-label="邮箱"
                placeholder="请输入邮箱"
                value={newEmail}
                onValueChange={setNewEmail}
                size="lg"
                variant="bordered"
                labelPlacement="outside"
                classNames={{
                  inputWrapper: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                  input: "text-sm placeholder:text-default-400",
                }}
              />
            </div>
            <div>
              <label className="text-sm font-medium text-foreground mb-2 block">角色</label>
              <Select
                aria-label="角色"
                selectedKeys={[newRole]}
                onSelectionChange={(keys) => {
                  const value = Array.from(keys)[0] as 'admin' | 'user' | 'guest'
                  if (value) setNewRole(value)
                }}
                size="lg"
                variant="bordered"
                classNames={{
                  trigger: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                }}
              >
                <SelectItem key="admin" startContent={<Shield size={16} />}>
                  管理员
                </SelectItem>
                <SelectItem key="user" startContent={<UserIcon size={16} />}>
                  普通用户
                </SelectItem>
                <SelectItem key="guest" startContent={<UserX size={16} />}>
                  访客
                </SelectItem>
              </Select>
            </div>
            <div>
              <Input
                label="用户组（可选）"
                aria-label="用户组"
                placeholder="family, editors"
                value={newGroups}
                onValueChange={setNewGroups}
                size="lg"
                variant="bordered"
                labelPlacement="outside"
                startContent={<Tags size={16} className="text-default-400" />}
                classNames={{
                  inputWrapper: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                  input: "text-sm placeholder:text-default-400",
                }}
              />
            </div>
            <div>
              <Input
                label="主目录"
                aria-label="主目录"
                placeholder="/username"
                value={newHomeDir}
                onValueChange={setNewHomeDir}
                isInvalid={Boolean(newHomeDir.trim() && newHomeDirValidationIssue)}
                errorMessage={
                  newHomeDir.trim() && newHomeDirValidationIssue
                    ? (newHomeDirValidationIssue.description ?? newHomeDirValidationIssue.title)
                    : undefined
                }
                size="lg"
                variant="bordered"
                labelPlacement="outside"
                startContent={<FolderOpen size={16} className="text-default-400" />}
                classNames={{
                  inputWrapper: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                  input: "text-sm placeholder:text-default-400 font-mono",
                }}
              />
            </div>
            <div className="grid grid-cols-[minmax(0,1fr)_8rem] gap-3">
              <Input
                type="number"
                min={0}
                label="容量配额"
                aria-label="容量配额"
                placeholder="0 表示不限额"
                value={newQuotaValue}
                onValueChange={setNewQuotaValue}
                size="lg"
                variant="bordered"
                labelPlacement="outside"
                startContent={<HardDrive size={16} className="text-default-400" />}
                classNames={{
                  inputWrapper: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                  input: "text-sm placeholder:text-default-400",
                }}
              />
              <Select
                label="单位"
                aria-label="容量配额单位"
                selectedKeys={[newQuotaUnit]}
                onSelectionChange={(keys) => {
                  const value = Array.from(keys)[0] as QuotaUnit
                  if (value) setNewQuotaUnit(value)
                }}
                size="lg"
                variant="bordered"
                labelPlacement="outside"
                classNames={{
                  trigger: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                }}
              >
                {quotaUnits.map((unit) => (
                  <SelectItem key={unit.key}>{unit.label}</SelectItem>
                ))}
              </Select>
            </div>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button
              variant="flat"
              onPress={handleCloseCreateModal}
              isDisabled={createMutation.isPending}
              className="text-default-600 rounded-lg"
            >
              取消
            </Button>
            <Button
              color="primary"
              onPress={handleCreate}
              isLoading={createMutation.isPending}
              isDisabled={
                !newUsername.trim()
                || !newPassword.trim()
                || newPassword.length < 8
                || utf8ByteLength(newPassword) > maxPasswordBytes
                || Boolean(newHomeDirValidationIssue)
                || quotaFormValueToBytes(newQuotaValue, newQuotaUnit) == null
              }
              className="rounded-lg"
            >
              创建
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      {/* Edit User Modal */}
      <Modal
        isOpen={isEditOpen}
        onClose={handleCloseEditModal}
        size="lg"
        placement="center"
        classNames={{
          base: "bg-content1 border border-divider shadow-xl rounded-lg",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-lg bg-accent-primary/10 text-accent-primary flex items-center justify-center">
              <Pencil size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">编辑用户</h3>
              <p className="text-xs text-default-500 font-normal">{editTarget?.username ?? ''}</p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4 space-y-4">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Input
                type="email"
                label="邮箱"
                aria-label="邮箱"
                placeholder="可留空"
                value={editEmail}
                onValueChange={setEditEmail}
                size="lg"
                variant="bordered"
                labelPlacement="outside"
                startContent={<Mail size={16} className="text-default-400" />}
                classNames={{
                  inputWrapper: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                  input: "text-sm placeholder:text-default-400",
                }}
              />
              <Select
                label="角色"
                aria-label="角色"
                selectedKeys={[editRole]}
                onSelectionChange={(keys) => {
                  const value = Array.from(keys)[0] as User['role']
                  if (value) setEditRole(value)
                }}
                isDisabled={editTarget?.id === currentUserId}
                size="lg"
                variant="bordered"
                labelPlacement="outside"
                classNames={{
                  trigger: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                }}
              >
                <SelectItem key="admin" startContent={<Shield size={16} />}>
                  管理员
                </SelectItem>
                <SelectItem key="user" startContent={<UserIcon size={16} />}>
                  普通用户
                </SelectItem>
                <SelectItem key="guest" startContent={<UserX size={16} />}>
                  访客
                </SelectItem>
              </Select>
            </div>

            <Input
              label="用户组"
              aria-label="用户组"
              placeholder="family, editors"
              value={editGroups}
              onValueChange={setEditGroups}
              size="lg"
              variant="bordered"
              labelPlacement="outside"
              startContent={<Tags size={16} className="text-default-400" />}
              classNames={{
                inputWrapper: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                input: "text-sm placeholder:text-default-400",
              }}
            />

            <Input
              label="主目录"
              aria-label="主目录"
              placeholder="/username"
              value={editHomeDir}
              onValueChange={setEditHomeDir}
              isInvalid={Boolean(editHomeDirValidationIssue)}
              errorMessage={
                editHomeDirValidationIssue
                  ? (editHomeDirValidationIssue.description ?? editHomeDirValidationIssue.title)
                  : undefined
              }
              size="lg"
              variant="bordered"
              labelPlacement="outside"
              startContent={<FolderOpen size={16} className="text-default-400" />}
              classNames={{
                inputWrapper: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                input: "text-sm placeholder:text-default-400 font-mono",
              }}
            />

            <div className="grid grid-cols-[minmax(0,1fr)_8rem] gap-3">
              <Input
                type="number"
                min={0}
                label="容量配额"
                aria-label="容量配额"
                placeholder="0 表示不限额"
                value={editQuotaValue}
                onValueChange={setEditQuotaValue}
                size="lg"
                variant="bordered"
                labelPlacement="outside"
                startContent={<HardDrive size={16} className="text-default-400" />}
                classNames={{
                  inputWrapper: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                  input: "text-sm placeholder:text-default-400",
                }}
              />
              <Select
                label="单位"
                aria-label="容量配额单位"
                selectedKeys={[editQuotaUnit]}
                onSelectionChange={(keys) => {
                  const value = Array.from(keys)[0] as QuotaUnit
                  if (value) setEditQuotaUnit(value)
                }}
                size="lg"
                variant="bordered"
                labelPlacement="outside"
                classNames={{
                  trigger: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                }}
              >
                {quotaUnits.map((unit) => (
                  <SelectItem key={unit.key}>{unit.label}</SelectItem>
                ))}
              </Select>
            </div>

            <div className="rounded-lg border border-divider bg-default-50 px-4 py-3 text-sm text-default-600">
              当前用量 {formatBytes(editTarget?.used_bytes ?? 0)}
              {editTarget?.quota_bytes && editTarget.quota_bytes > 0 ? ` / ${formatBytes(editTarget.quota_bytes)}` : '，未设置配额'}
            </div>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button
              variant="flat"
              onPress={handleCloseEditModal}
              isDisabled={updateMutation.isPending}
              className="text-default-600 rounded-lg"
            >
              取消
            </Button>
            <Button
              color="primary"
              onPress={handleUpdateUser}
              isLoading={updateMutation.isPending}
              isDisabled={
                !editTarget
                || Boolean(editHomeDirValidationIssue)
                || quotaFormValueToBytes(editQuotaValue, editQuotaUnit) == null
              }
              className="rounded-lg"
            >
              保存
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      {/* Delete Confirmation Modal */}
      <Modal
        isOpen={isDeleteOpen}
        onClose={handleCloseDeleteModal}
        size="md"
        placement="center"
        classNames={{
          base: "bg-content1 border border-divider shadow-xl rounded-lg",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-lg bg-danger/10 text-danger flex items-center justify-center">
              <Trash2 size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">确认删除</h3>
              <p className="text-xs text-default-500 font-normal">此操作不可恢复</p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4">
            <p className="text-default-600">
              确定要删除用户 <strong className="text-foreground">{deleteTarget?.username}</strong> 吗？
            </p>
            <p className="text-xs text-default-500 mt-2">
              此操作不可逆，该用户的所有数据将被保留但无法访问。
            </p>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button
              variant="flat"
              onPress={handleCloseDeleteModal}
              isDisabled={deleteMutation.isPending}
              className="text-default-600 rounded-lg"
            >
              取消
            </Button>
            <Button
              color="danger"
              onPress={handleDelete}
              isLoading={deleteMutation.isPending}
              className="rounded-lg"
            >
              删除
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      {/* Reset Password Modal */}
      <Modal
        isOpen={isResetOpen}
        onClose={handleCloseResetModal}
        size="md"
        placement="center"
        classNames={{
          base: "bg-content1 border border-divider shadow-xl rounded-lg",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-lg bg-accent-primary/10 text-accent-primary flex items-center justify-center">
              <KeyRound size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">重置密码</h3>
              <p className="text-xs text-default-500 font-normal">为用户 <strong>{resetTarget?.username}</strong> 设置新密码</p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4">
            <div>
              <Input
                type="password"
                label="新密码"
                aria-label="新密码"
                placeholder="请输入新密码（8-72 字节）"
                value={resetPassword}
                onValueChange={setResetPassword}
                autoFocus
                size="lg"
                variant="bordered"
                labelPlacement="outside"
                classNames={{
                  inputWrapper: "bg-default-50 border-default-200 hover:border-default-300 data-[focus=true]:!border-accent-primary",
                  input: "text-sm placeholder:text-default-400",
                }}
              />
            </div>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button
              variant="flat"
              onPress={handleCloseResetModal}
              isDisabled={resetPasswordMutation.isPending}
              className="text-default-600 rounded-lg"
            >
              取消
            </Button>
            <Button
              color="primary"
              onPress={handleResetPassword}
              isLoading={resetPasswordMutation.isPending}
              isDisabled={!resetPassword.trim() || resetPassword.length < 8 || utf8ByteLength(resetPassword) > maxPasswordBytes}
              className="rounded-lg"
            >
              确认重置
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  )
}
