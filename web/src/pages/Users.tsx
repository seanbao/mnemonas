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
} from 'lucide-react'
import { listUsers, createUser, deleteUser, resetUserPassword, toggleUserStatus, UsersError, type ListUsersResponse, type User } from '@/api/users'
import { getStoredUser } from '@/api/auth'
import { formatBytes, formatDate, cn } from '@/lib/utils'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'
import { StatCard } from '@/components/ui/StatCard'

const usersUnavailableDescription = '用户配置当前不可用，请检查系统配置状态或稍后重试。'

function isMissingUserError(error: unknown): boolean {
  return error instanceof UsersError && (error.status === 404 || error.code === 'USER_NOT_FOUND')
}

function syncMissingUserInCache(queryClient: ReturnType<typeof useQueryClient>, userId: string): boolean {
  let removed = false

  queryClient.setQueryData<ListUsersResponse | undefined>(['users'], (current) => {
    if (!current) {
      return current
    }

    const users = current.users.filter((user) => user.id !== userId)
    removed = users.length !== current.users.length
    if (!removed) {
      return current
    }

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
  action: 'create' | 'delete' | 'reset-password' | 'toggle-status',
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
  onResetPassword,
  onDelete,
  onToggleStatus,
  isCurrentUser,
}: {
  user: User
  onResetPassword: () => void
  onDelete: () => void
  onToggleStatus: () => void
  isCurrentUser: boolean
}) {
  return (
    <Card className="card-meridian">
      <CardBody className="p-4">
        <div className="flex items-start justify-between">
          <div className="flex items-center gap-3">
            <div className={cn(
              "w-10 h-10 rounded-xl flex items-center justify-center",
              user.role === 'admin' 
                ? "bg-rose/15 text-rose" 
                : "bg-accent-primary/15 text-accent-primary"
            )}>
              <span className="font-semibold text-lg text-current">
                {user.username.charAt(0).toUpperCase()}
              </span>
            </div>
            <div>
              <div className="flex items-center gap-2">
                <span className="font-medium text-foreground">{user.username}</span>
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
            </div>
          </div>

          <Dropdown placement="bottom-end">
            <DropdownTrigger>
              <Button
                isIconOnly
                variant="light"
                size="sm"
                aria-label={`${user.username} 用户操作`}
                className="text-default-500 rounded-xl"
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
            <div className="flex items-center gap-2 text-default-500">
              <Mail size={14} />
              <span>{user.email}</span>
            </div>
          )}
          <div className="flex items-center gap-2 text-default-500">
            <Calendar size={14} />
            <span>创建于 {formatDate(user.created_at)}</span>
          </div>
          {user.last_login_at && (
            <div className="flex items-center gap-2 text-default-500">
              <RefreshCw size={14} />
              <span>最后登录 {formatDate(user.last_login_at)}</span>
            </div>
          )}
          <div className="flex items-center gap-2 text-default-500">
            <HardDrive size={14} />
            <span>
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
  const { isOpen: isDeleteOpen, onOpen: onDeleteOpen, onClose: onDeleteClose } = useDisclosure()
  const { isOpen: isResetOpen, onOpen: onResetOpen, onClose: onResetClose } = useDisclosure()

  const [deleteTarget, setDeleteTarget] = useState<User | null>(null)
  const [resetTarget, setResetTarget] = useState<User | null>(null)
  const [newUsername, setNewUsername] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [newEmail, setNewEmail] = useState('')
  const [newRole, setNewRole] = useState<'admin' | 'user' | 'guest'>('user')
  const [resetPassword, setResetPassword] = useState('')
  const createSessionRef = useRef(0)
  const createDraftRef = useRef({ username: '', password: '', email: '', role: 'user' })

  useLayoutEffect(() => {
    createDraftRef.current = {
      username: newUsername,
      password: newPassword,
      email: newEmail,
      role: newRole,
    }
  }, [newEmail, newPassword, newRole, newUsername])

  const { data, isLoading, isRefetching, error, refetch } = useQuery({
    queryKey: ['users'],
    queryFn: listUsers,
  })

  const createMutation = useMutation({
    mutationFn: ({ request }: {
      request: Parameters<typeof createUser>[0]
      submittedDraft: typeof createDraftRef.current
      createSession: number
    }) => createUser(request),
    onSuccess: (result, variables) => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
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

  const deleteMutation = useMutation({
    mutationFn: deleteUser,
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
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
      queryClient.invalidateQueries({ queryKey: ['users'] })
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
      queryClient.invalidateQueries({ queryKey: ['users'] })
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

  // Get current user from stored auth state
  const currentUserId = getStoredUser()?.id

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
    <div className="h-full flex flex-col p-6">
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
              className="text-default-600 rounded-xl"
            >
              刷新
            </Button>
            <Button
              className="bg-accent-primary text-white rounded-xl"
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
      <div className="grid grid-cols-3 gap-4 mb-6">
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
      <Card className="flex-1 card-meridian overflow-hidden">
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
                  <Button variant="bordered" className="rounded-xl" onPress={handleRefreshUsers}>
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
          base: "bg-content1 border border-divider shadow-2xl rounded-2xl",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-xl bg-accent-primary/10 text-accent-primary flex items-center justify-center">
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
                placeholder="请输入密码（至少 8 位）"
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
              className="text-default-600 rounded-xl"
            >
              取消
            </Button>
            <Button
              color="primary"
              onPress={handleCreate}
              isLoading={createMutation.isPending}
              isDisabled={!newUsername.trim() || !newPassword.trim() || newPassword.length < 8}
              className="rounded-xl"
            >
              创建
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
          base: "bg-content1 border border-divider shadow-2xl rounded-2xl",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-xl bg-danger/10 text-danger flex items-center justify-center">
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
              className="text-default-600 rounded-xl"
            >
              取消
            </Button>
            <Button
              color="danger"
              onPress={handleDelete}
              isLoading={deleteMutation.isPending}
              className="rounded-xl"
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
          base: "bg-content1 border border-divider shadow-2xl rounded-2xl",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-xl bg-accent-primary/10 text-accent-primary flex items-center justify-center">
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
                placeholder="请输入新密码（至少 8 位）"
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
              className="text-default-600 rounded-xl"
            >
              取消
            </Button>
            <Button
              color="primary"
              onPress={handleResetPassword}
              isLoading={resetPasswordMutation.isPending}
              isDisabled={!resetPassword.trim() || resetPassword.length < 8}
              className="rounded-xl"
            >
              确认重置
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  )
}
