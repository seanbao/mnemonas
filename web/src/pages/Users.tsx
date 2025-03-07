import { useState, useCallback } from 'react'
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
} from 'lucide-react'
import { listUsers, createUser, deleteUser, resetUserPassword, type User } from '@/api/users'
import { getStoredUser } from '@/api/auth'
import { formatBytes, formatDate, cn } from '@/lib/utils'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'
import { StatCard } from '@/components/ui/StatCard'

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
  isCurrentUser,
}: {
  user: User
  onResetPassword: () => void
  onDelete: () => void
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

  const [actionUser, setActionUser] = useState<User | null>(null)
  const [newUsername, setNewUsername] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [newEmail, setNewEmail] = useState('')
  const [newRole, setNewRole] = useState<'admin' | 'user' | 'guest'>('user')
  const [resetPassword, setResetPassword] = useState('')

  const { data, isLoading, refetch } = useQuery({
    queryKey: ['users'],
    queryFn: listUsers,
  })

  const createMutation = useMutation({
    mutationFn: createUser,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      onCreateClose()
      resetCreateForm()
      addToast({ title: '用户创建成功', color: 'success' })
    },
    onError: (error: Error) => {
      addToast({ title: '创建失败', description: error.message, color: 'danger' })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteUser,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      onDeleteClose()
      setActionUser(null)
      addToast({ title: '用户已删除', color: 'success' })
    },
    onError: (error: Error) => {
      addToast({ title: '删除失败', description: error.message, color: 'danger' })
    },
  })

  const resetPasswordMutation = useMutation({
    mutationFn: ({ userId, password }: { userId: string; password: string }) =>
      resetUserPassword(userId, { new_password: password }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      onResetClose()
      setActionUser(null)
      setResetPassword('')
      addToast({ title: '密码已重置', color: 'success' })
    },
    onError: (error: Error) => {
      addToast({ title: '重置失败', description: error.message, color: 'danger' })
    },
  })

  const resetCreateForm = useCallback(() => {
    setNewUsername('')
    setNewPassword('')
    setNewEmail('')
    setNewRole('user')
  }, [])

  const handleCreate = useCallback(() => {
    if (!newUsername.trim() || !newPassword.trim()) return
    createMutation.mutate({
      username: newUsername.trim(),
      password: newPassword,
      email: newEmail.trim() || undefined,
      role: newRole,
    })
  }, [newUsername, newPassword, newEmail, newRole, createMutation])

  const handleDelete = useCallback(() => {
    if (!actionUser) return
    deleteMutation.mutate(actionUser.id)
  }, [actionUser, deleteMutation])

  const handleResetPassword = useCallback(() => {
    if (!actionUser || !resetPassword.trim()) return
    resetPasswordMutation.mutate({ userId: actionUser.id, password: resetPassword })
  }, [actionUser, resetPassword, resetPasswordMutation])

  const handleOpenDeleteModal = useCallback((user: User) => {
    setActionUser(user)
    onDeleteOpen()
  }, [onDeleteOpen])

  const handleOpenResetModal = useCallback((user: User) => {
    setActionUser(user)
    setResetPassword('')
    onResetOpen()
  }, [onResetOpen])

  // Get current user from stored auth state
  const currentUserId = getStoredUser()?.id

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
              onPress={() => refetch()}
              isLoading={isLoading}
              className="text-default-600 rounded-xl"
            >
              刷新
            </Button>
            <Button
              className="bg-accent-primary text-white rounded-xl"
              startContent={<UserPlus size={16} />}
              onPress={onCreateOpen}
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
          value={data?.total || 0}
          icon={UsersIcon}
          tone="primary"
        />
        <StatCard
          title="管理员"
          value={data?.users?.filter(u => u.role === 'admin').length || 0}
          icon={Shield}
          tone="danger"
        />
        <StatCard
          title="活跃用户"
          value={data?.users?.filter(u => !u.disabled).length || 0}
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
          ) : !data?.users?.length ? (
            <div className="flex items-center justify-center h-40">
              <EmptyState icon={UsersIcon} title="暂无用户" />
            </div>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 p-1">
              {data.users.map((user) => (
                <UserCard
                  key={user.id}
                  user={user}
                  isCurrentUser={user.id === currentUserId}
                  onResetPassword={() => handleOpenResetModal(user)}
                  onDelete={() => handleOpenDeleteModal(user)}
                />
              ))}
            </div>
          )}
        </CardBody>
      </Card>

      {/* Create User Modal */}
      <Modal
        isOpen={isCreateOpen}
        onClose={() => {
          onCreateClose()
          resetCreateForm()
        }}
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
              onPress={() => {
                onCreateClose()
                resetCreateForm()
              }}
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
        onClose={() => {
          onDeleteClose()
          setActionUser(null)
        }}
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
              确定要删除用户 <strong className="text-foreground">{actionUser?.username}</strong> 吗？
            </p>
            <p className="text-xs text-default-500 mt-2">
              此操作不可逆，该用户的所有数据将被保留但无法访问。
            </p>
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button
              variant="flat"
              onPress={() => {
                onDeleteClose()
                setActionUser(null)
              }}
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
        onClose={() => {
          onResetClose()
          setActionUser(null)
          setResetPassword('')
        }}
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
              <p className="text-xs text-default-500 font-normal">为用户 <strong>{actionUser?.username}</strong> 设置新密码</p>
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
              onPress={() => {
                onResetClose()
                setActionUser(null)
                setResetPassword('')
              }}
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
