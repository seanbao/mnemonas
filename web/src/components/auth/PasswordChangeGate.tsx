import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Button, Card, CardBody, addToast } from '@heroui/react'
import { LogOut, ShieldAlert } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { logout, type User } from '@/api/auth'
import { useUser } from '@/stores/auth'
import { PasswordChangeForm } from './PasswordChangeForm'

interface PasswordChangeGateProps {
  children: ReactNode
}

const logoutFailureMessage = '退出登录失败，请稍后重试。'

export function PasswordChangeGate({ children }: PasswordChangeGateProps) {
  const user = useUser()

  if (!user?.mustChangePassword) {
    return <>{children}</>
  }

  return <RequiredPasswordChangeView key={user.id} user={user} />
}

function RequiredPasswordChangeView({ user }: { user: User }) {
  const navigate = useNavigate()
  const [logoutError, setLogoutError] = useState<string | null>(null)
  const [isChangingPassword, setIsChangingPassword] = useState(false)
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

  const handleLogout = async () => {
    if (isChangingPassword || isLoggingOut) {
      return
    }

    setLogoutError(null)
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
      setLogoutError(logoutFailureMessage)
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
              密码修改后，此账户在所有设备上的登录都将退出。请使用新密码重新登录。
            </div>

            {logoutError && (
              <div role="alert" className="mb-5 rounded-lg border border-danger/20 bg-danger/5 px-4 py-3 text-sm text-danger">
                {logoutError}
              </div>
            )}

            <PasswordChangeForm
              accountId={user.id}
              isExternallyBusy={isLoggingOut}
              onSubmittingChange={setIsChangingPassword}
              actionsClassName="space-y-2"
              submitClassName="w-full rounded-lg"
              secondaryAction={({ isSubmitting }) => (
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
              )}
            />
          </CardBody>
        </Card>
      </main>
    </div>
  )
}
