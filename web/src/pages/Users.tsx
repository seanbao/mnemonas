import { useState, useCallback, useLayoutEffect, useRef } from 'react'
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
} from 'lucide-react'
import { listUsers, createUser, deleteUser, resetUserPassword, toggleUserStatus, updateUser, UsersError, type ListUsersResponse, type User } from '@/api/users'
import { getStoredUser } from '@/api/auth'
import { formatBytes, formatDate, cn } from '@/lib/utils'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'
import { StatCard } from '@/components/ui/StatCard'

const usersUnavailableDescription = '用户配置当前不可用，请检查系统配置状态或稍后重试。'
const maxPasswordBytes = 72
const quotaUnits = [
  { key: 'B', label: 'B', multiplier: 1 },
  { key: 'MB', label: 'MB', multiplier: 1024 ** 2 },
  { key: 'GB', label: 'GB', multiplier: 1024 ** 3 },
  { key: 'TB', label: 'TB', multiplier: 1024 ** 4 },
] as const

type QuotaUnit = typeof quotaUnits[number]['key']
const groupNamePattern = /^[A-Za-z0-9._-]+$/

function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length
}

function quotaBytesToFormValue(bytes: number): { value: string; unit: QuotaUnit } {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return { value: '0', unit: 'GB' }
  }

  for (const unit of [...quotaUnits].reverse()) {
    const value = bytes / unit.multiplier
    if (value >= 1 && Number.isInteger(value)) {
      return { value: String(value), unit: unit.key }
    }
  }

  return { value: String(bytes), unit: 'B' }
}

function quotaFormValueToBytes(value: string, unitKey: QuotaUnit): number | null {
  const normalized = value.trim()
  if (!normalized) {
    return 0
  }

  const numericValue = Number(normalized)
  const unit = quotaUnits.find((candidate) => candidate.key === unitKey)
  if (!unit || !Number.isFinite(numericValue) || numericValue < 0) {
    return null
  }

  const bytes = Math.round(numericValue * unit.multiplier)
  return Number.isSafeInteger(bytes) ? bytes : null
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

function isMissingUserError(error: unknown): boolean {
  return error instanceof UsersError && (error.status === 404 || error.code === 'USER_NOT_FOUND')
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
    description: error instanceof Error ? error.message : '请稍后重试',
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
        description: '主目录必须是站内绝对路径，例如 /alice。',
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
    description: error instanceof Error ? error.message : '请稍后重试',
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
  action: 'create' | 'update' | 'delete' | 'reset-password' | 'toggle-status',
  options?: { warning?: boolean; disabled?: boolean }
): {
  title: string
  description?: string
  color: 'success' | 'warning'
} {
  const warningDescription = '操作已提交，但用户配置持久化存在告警，请检查系统状态。'

  switch (action) {
    case 'create':
      return options?.warning
        ? { title: '用户已创建，但持久化存在告警', description: warningDescription, color: 'warning' }
        : { title: '用户创建成功', color: 'success' }
    case 'update':
      return options?.warning
        ? { title: '用户已更新，但持久化存在告警', description: warningDescription, color: 'warning' }
        : { title: '用户已更新', color: 'success' }
    case 'delete':
      return options?.warning
        ? { title: '用户已删除，但持久化存在告警', description: warningDescription, color: 'warning' }
        : { title: '用户已删除', color: 'success' }
    case 'reset-password':
      return options?.warning
        ? { title: '密码已重置，但持久化存在告警', description: warningDescription, color: 'warning' }
        : { title: '密码已重置', color: 'success' }
    case 'toggle-status':
      if (options?.warning) {
        return {
          title: options.disabled ? '用户已禁用，但持久化存在告警' : '用户已启用，但持久化存在告警',
          description: warningDescription,
          color: 'warning',
        }
      }
      return { title: options?.disabled ? '用户已禁用' : '用户已启用', color: 'success' }
  }
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
  onDelete,
  onToggleStatus,
  isCurrentUser,
}: {
  user: User
  onEdit: () => void
  onResetPassword: () => void
  onDelete: () => void
  onToggleStatus: () => void
  isCurrentUser: boolean
}) {
  return (
    <Card className="card-meridian">
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
  const [resetPassword, setResetPassword] = useState('')
  const currentUserId = getStoredUser()?.id ?? 'anonymous'
  const usersQueryKey = ['users', currentUserId] as const
  const createSessionRef = useRef(0)
  const createDraftRef = useRef({ username: '', password: '', email: '', role: 'user', groups: '' })

  useLayoutEffect(() => {
    createDraftRef.current = {
      username: newUsername,
      password: newPassword,
      email: newEmail,
      role: newRole,
      groups: newGroups,
    }
  }, [newEmail, newGroups, newPassword, newRole, newUsername])

  const { data, isLoading, isRefetching, error, refetch } = useQuery({
    queryKey: usersQueryKey,
    queryFn: listUsers,
  })

  const createMutation = useMutation({
    mutationFn: ({ request }: {
      request: Parameters<typeof createUser>[0]
      submittedDraft: typeof createDraftRef.current
      createSession: number
    }) => createUser(request),
    onSuccess: (result, variables) => {
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
    onError: (error) => {
      addToast(getUsersActionErrorPresentation(error, {
        unavailable: '创建用户暂不可用',
        failure: '创建失败',
      }))
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({ userId, email, role, groups, homeDir, quotaBytes }: {
      userId: string
      email: string
      role: User['role']
      groups: string[]
      homeDir: string
      quotaBytes: number
    }) => updateUser(userId, {
      email,
      role,
      groups,
      home_dir: homeDir,
      quota_bytes: quotaBytes,
    }),
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: usersQueryKey })
      onEditClose()
      setEditTarget(null)
      addToast(getUsersActionSuccessToast('update', { warning: result.warning }))
    },
    onError: (error, variables) => {
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
  })

  const deleteMutation = useMutation({
    mutationFn: deleteUser,
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: usersQueryKey })
      onDeleteClose()
      setDeleteTarget(null)
      addToast(getUsersActionSuccessToast('delete', { warning: result.warning }))
    },
    onError: (error, userId) => {
      if (isMissingUserError(error)) {
        syncMissingUserInCache(queryClient, userId)
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
  })

  const resetPasswordMutation = useMutation({
    mutationFn: ({ userId, password }: { userId: string; password: string }) =>
      resetUserPassword(userId, { new_password: password }),
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: usersQueryKey })
      onResetClose()
      setResetTarget(null)
      setResetPassword('')
      addToast(getUsersActionSuccessToast('reset-password', { warning: result.warning }))
    },
    onError: (error, variables) => {
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
  })

  const toggleStatusMutation = useMutation({
    mutationFn: ({ userId, disabled }: { userId: string; disabled: boolean }) =>
      toggleUserStatus(userId, disabled),
    onSuccess: (result, variables) => {
      queryClient.invalidateQueries({ queryKey: usersQueryKey })
      addToast(getUsersActionSuccessToast('toggle-status', {
        warning: result.warning,
        disabled: variables.disabled,
      }))
    },
    onError: (error, variables) => {
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
  })

  const resetCreateForm = useCallback(() => {
    setNewUsername('')
    setNewPassword('')
    setNewEmail('')
    setNewRole('user')
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
    createMutation.mutate({
      request: {
        username: newUsername.trim(),
        password: newPassword,
        email: newEmail.trim() || undefined,
        role: newRole,
      },
      submittedDraft: { ...createDraftRef.current },
      createSession: createSessionRef.current,
    })
  }, [newUsername, newPassword, newEmail, newRole, createMutation])

  const handleOpenEditModal = useCallback((user: User) => {
    const quota = quotaBytesToFormValue(user.quota_bytes)
    setEditTarget(user)
    setEditEmail(user.email ?? '')
    setEditRole(user.role)
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
    if (!homeDir.startsWith('/')) {
      addToast({ title: '主目录必须以 / 开头', color: 'warning' })
      return
    }
    const quotaBytes = quotaFormValueToBytes(editQuotaValue, editQuotaUnit)
    if (quotaBytes == null) {
      addToast({ title: '配额无效', description: '请输入非负数字，0 表示不限额。', color: 'warning' })
      return
    }

    updateMutation.mutate({
      userId: editTarget.id,
      email: editEmail.trim(),
      role: editRole,
      homeDir,
      quotaBytes,
    })
  }, [editEmail, editHomeDir, editQuotaUnit, editQuotaValue, editRole, editTarget, updateMutation])

  const handleDelete = useCallback(() => {
    if (!deleteTarget) return
    deleteMutation.mutate(deleteTarget.id)
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
    resetPasswordMutation.mutate({ userId: resetTarget.id, password: resetPassword })
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
    toggleStatusMutation.mutate({ userId: user.id, disabled: !user.disabled })
  }, [currentUserId, toggleStatusMutation])

  const handleRefreshUsers = useCallback(async () => {
	const result = await refetch()
	if (result.error) {
	  addToast(getUsersRefreshErrorPresentation(result.error))
	  return
	}
	addToast({ title: '用户列表已刷新', color: 'success' })
  }, [refetch])

  const users = data?.users ?? []
  const totalUsers = data?.total ?? users.length
  const adminCount = users.filter((user) => user.role === 'admin').length
  const activeUserCount = users.filter((user) => !user.disabled).length
  const usersLoadError = error ? getUsersLoadErrorPresentation(error) : null

  return (
    <div className="flex h-full min-h-0 flex-col p-4 sm:p-6">
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
      <div className="grid grid-cols-1 gap-4 mb-6 sm:grid-cols-3">
        <StatCard
          title="总用户数"
          value={totalUsers}
          icon={UsersIcon}
          tone="primary"
        />
        <StatCard
          title="管理员"
          value={adminCount}
          icon={Shield}
          tone="danger"
        />
        <StatCard
          title="活跃用户"
          value={activeUserCount}
          icon={UserIcon}
          tone="success"
        />
      </div>

      {/* User List */}
      <Card className="card-meridian min-h-0 flex-1 overflow-hidden">
        <CardHeader className="border-b border-divider">
          <h2 className="font-semibold text-foreground">用户列表</h2>
        </CardHeader>
        <CardBody className="overflow-auto custom-scrollbar">
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
                description={usersLoadError?.description ?? '请稍后重试'}
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
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 p-1">
              {users.map((user) => (
                <UserCard
                  key={user.id}
                  user={user}
                  isCurrentUser={user.id === currentUserId}
                  onEdit={() => handleOpenEditModal(user)}
                  onResetPassword={() => handleOpenResetModal(user)}
                  onDelete={() => handleOpenDeleteModal(user)}
                  onToggleStatus={() => handleToggleStatus(user)}
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
              isDisabled={!newUsername.trim() || !newPassword.trim() || newPassword.length < 8 || utf8ByteLength(newPassword) > maxPasswordBytes}
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
              label="主目录"
              aria-label="主目录"
              placeholder="/username"
              value={editHomeDir}
              onValueChange={setEditHomeDir}
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
              isDisabled={!editTarget || !editHomeDir.trim() || quotaFormValueToBytes(editQuotaValue, editQuotaUnit) == null}
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
