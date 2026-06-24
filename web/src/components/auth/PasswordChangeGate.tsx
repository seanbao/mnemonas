import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { Button, Card, CardBody, Input, addToast } from '@heroui/react'
import { KeyRound, LogOut, ShieldAlert } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { AuthError, changePassword, logout, type User } from '@/api/auth'
import { useUser } from '@/stores/auth'

interface PasswordChangeGateProps {
  children: ReactNode
}

const minPasswordBytes = 8
const maxPasswordBytes = 72
const passwordChangeFailureMessage = '密码修改失败，请检查网络后重试。'
const logoutFailureMessage = '退出登录失败，请稍后重试。'

function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length
}

function validatePasswordChange(
  currentPassword: string,
  newPassword: string,
  confirmation: string,
): string | null {
  if (!currentPassword) {
    return '请输入当前密码。'
  }
  if (!newPassword.trim()) {
    return '请输入新密码。'
  }

  const passwordBytes = utf8ByteLength(newPassword)
  if (passwordBytes < minPasswordBytes || passwordBytes > maxPasswordBytes) {
    return `新密码长度必须为 ${minPasswordBytes} 至 ${maxPasswordBytes} 个 UTF-8 字节。`
  }
  if (!confirmation) {
    return '请再次输入新密码。'
  }
  if (newPassword !== confirmation) {
    return '两次输入的新密码不一致。'
  }
  if (newPassword === currentPassword) {
    return '新密码不能与当前密码相同。'
  }

  return null
}

function getPasswordChangeFailureMessage(error: unknown): string {
  if (error instanceof AuthError) {
    switch (error.code) {
      case 'INVALID_PASSWORD':
        return '当前密码不正确。'
      case 'PASSWORD_TOO_SHORT':
      case 'PASSWORD_TOO_LONG':
        return `新密码长度必须为 ${minPasswordBytes} 至 ${maxPasswordBytes} 个 UTF-8 字节。`
      case 'PASSWORD_UNCHANGED':
        return '新密码不能与当前密码相同。'
      case 'USER_DISABLED':
        return '当前账户已被禁用，请退出后联系管理员。'
      case 'NOT_AUTHENTICATED':
      case 'TOKEN_EXPIRED':
      case 'TOKEN_REVOKED':
        return '登录会话已失效，请退出后重新登录。'
      default:
        if (error.message === '修改密码响应无效') {
          return error.message
        }
    }
  }

  return passwordChangeFailureMessage
}

export function PasswordChangeGate({ children }: PasswordChangeGateProps) {
  const user = useUser()

  if (!user?.mustChangePassword) {
    return <>{children}</>
  }

  return <PasswordChangeForm key={user.id} user={user} />
}

function PasswordChangeForm({ user }: { user: User }) {
  const navigate = useNavigate()
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [formError, setFormError] = useState<string | null>(null)
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [isLoggingOut, setIsLoggingOut] = useState(false)
  const gateRef = useRef<HTMLElement | null>(null)
  const requestControllerRef = useRef<AbortController | null>(null)

  useEffect(() => {
    gateRef.current?.focus()
    return () => {
      requestControllerRef.current?.abort()
      requestControllerRef.current = null
    }
  }, [])

  const isBusy = isSubmitting || isLoggingOut
  const clearFormError = () => {
    if (formError) {
      setFormError(null)
    }
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (isBusy) {
      return
    }

    const validationError = validatePasswordChange(currentPassword, newPassword, confirmation)
    if (validationError) {
      setFormError(validationError)
      return
    }

    setFormError(null)
    setIsSubmitting(true)
    const controller = new AbortController()
    requestControllerRef.current = controller
    try {
      const result = await changePassword({
        old_password: currentPassword,
        new_password: newPassword,
      }, { signal: controller.signal })
      if (controller.signal.aborted) {
        return
      }
      addToast(result.warning
        ? {
            title: '密码已修改，但认证状态持久化未完全确认',
            description: '请使用新密码重新登录验证，并检查设备存储状态或服务日志。',
            color: 'warning',
          }
        : { title: '密码已修改，请重新登录', color: 'success' })
      navigate('/login', { replace: true })
    } catch (error) {
      if (controller.signal.aborted) {
        return
      }
      setFormError(getPasswordChangeFailureMessage(error))
    } finally {
      if (requestControllerRef.current === controller) {
        requestControllerRef.current = null
        setIsSubmitting(false)
      }
    }
  }

  const handleLogout = async () => {
    if (isBusy) {
      return
    }

    setFormError(null)
    setIsLoggingOut(true)
    const controller = new AbortController()
    requestControllerRef.current = controller
    try {
      const result = await logout({ signal: controller.signal })
      if (controller.signal.aborted) {
        return
      }
      addToast(result.warning
        ? {
            title: '已退出登录，但认证状态持久化未完全确认',
            description: '请检查设备存储状态或服务日志，确认会话撤销已持久化。',
            color: 'warning',
          }
        : { title: '已退出登录', color: 'success' })
      navigate('/login', { replace: true })
    } catch {
      if (controller.signal.aborted) {
        return
      }
      setFormError(logoutFailureMessage)
    } finally {
      if (requestControllerRef.current === controller) {
        requestControllerRef.current = null
        setIsLoggingOut(false)
      }
    }
  }

  return (
    <div className="fixed inset-0 z-[100] flex min-h-dvh overflow-y-auto bg-background px-5 py-8 sm:px-8">
      <main
        ref={gateRef}
        aria-labelledby="password-change-gate-title"
        tabIndex={-1}
        className="m-auto w-full max-w-lg"
      >
        <p className="sr-only" role="status" aria-live="assertive">
          账户 {user.username} 必须修改密码，其他功能暂不可访问。
        </p>
        <Card className="border border-divider bg-content1 shadow-[var(--shadow-strong)]">
          <CardBody className="p-6 sm:p-8">
            <div className="mb-6 flex items-start gap-4">
              <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-lg bg-warning/10 text-warning">
                <ShieldAlert className="h-6 w-6" aria-hidden="true" />
              </div>
              <div>
                <h1 id="password-change-gate-title" className="text-xl font-semibold text-foreground">
                  必须修改密码
                </h1>
                <p className="mt-2 text-sm leading-6 text-default-600">
                  账户 {user.username} 当前使用需要更换的密码。完成修改前，文件和管理功能不可访问。
                </p>
              </div>
            </div>

            <div className="mb-6 rounded-lg border border-divider bg-content2/50 px-4 py-3 text-sm leading-6 text-default-600">
              密码修改后，当前会话将结束。请使用新密码重新登录。
            </div>

            {formError && (
              <div role="alert" className="mb-5 rounded-lg border border-danger/20 bg-danger/5 px-4 py-3 text-sm text-danger">
                {formError}
              </div>
            )}

            <form onSubmit={handleSubmit} noValidate className="space-y-5">
              <Input
                type="password"
                label="当前密码"
                aria-label="当前密码"
                value={currentPassword}
                onValueChange={(value) => {
                  setCurrentPassword(value)
                  clearFormError()
                }}
                autoComplete="current-password"
                variant="bordered"
                size="lg"
                isDisabled={isBusy}
                isRequired
              />
              <Input
                type="password"
                label="新密码"
                aria-label="新密码"
                description={`${minPasswordBytes} 至 ${maxPasswordBytes} 个 UTF-8 字节`}
                value={newPassword}
                onValueChange={(value) => {
                  setNewPassword(value)
                  clearFormError()
                }}
                autoComplete="new-password"
                variant="bordered"
                size="lg"
                isDisabled={isBusy}
                isRequired
              />
              <Input
                type="password"
                label="确认新密码"
                aria-label="确认新密码"
                value={confirmation}
                onValueChange={(value) => {
                  setConfirmation(value)
                  clearFormError()
                }}
                autoComplete="new-password"
                variant="bordered"
                size="lg"
                isDisabled={isBusy}
                isRequired
              />

              <Button
                type="submit"
                color="primary"
                size="lg"
                className="w-full rounded-lg"
                isLoading={isSubmitting}
                isDisabled={isLoggingOut}
                startContent={!isSubmitting && <KeyRound className="h-4 w-4" aria-hidden="true" />}
              >
                修改密码并重新登录
              </Button>
              <Button
                type="button"
                variant="flat"
                size="lg"
                className="w-full rounded-lg"
                onPress={handleLogout}
                isLoading={isLoggingOut}
                isDisabled={isSubmitting}
                startContent={!isLoggingOut && <LogOut className="h-4 w-4" aria-hidden="true" />}
              >
                退出登录
              </Button>
            </form>
          </CardBody>
        </Card>
      </main>
    </div>
  )
}
