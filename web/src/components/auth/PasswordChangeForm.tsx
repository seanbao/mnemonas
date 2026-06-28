import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { Button, Input, addToast } from '@heroui/react'
import { Eye, EyeOff, KeyRound } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { AuthError, PASSWORD_CHANGE_UNCONFIRMED_MESSAGE, changePassword } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'

const minPasswordBytes = 8
const maxPasswordBytes = 72
const passwordChangeFailureMessage = '密码修改失败，请稍后重试。'
const terminalAuthErrorCodes = new Set([
  'NOT_AUTHENTICATED',
  'MISSING_AUTH_HEADER',
  'INVALID_AUTH_HEADER',
  'INVALID_TOKEN',
  'TOKEN_EXPIRED',
  'TOKEN_REVOKED',
  'USER_NOT_FOUND',
  'USER_DISABLED',
])

interface PasswordChangeFormProps {
  accountId: string
  autoFocus?: boolean
  isExternallyBusy?: boolean
  onCancel?: () => void
  onSubmittingChange?: (isSubmitting: boolean) => void
  secondaryAction?: (state: { isBusy: boolean; isSubmitting: boolean }) => ReactNode
  actionsClassName?: string
  submitClassName?: string
}

type PasswordField = 'current' | 'new' | 'confirmation'

interface PasswordFormIssue {
  field?: PasswordField
  message: string
}

function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length
}

function validatePasswordChange(
  currentPassword: string,
  newPassword: string,
  confirmation: string,
): PasswordFormIssue | null {
  if (!currentPassword) {
    return { field: 'current', message: '请输入当前密码。' }
  }
  if (!newPassword.trim()) {
    return { field: 'new', message: '请输入新密码。' }
  }

  const passwordBytes = utf8ByteLength(newPassword)
  if (passwordBytes < minPasswordBytes || passwordBytes > maxPasswordBytes) {
    return {
      field: 'new',
      message: `新密码长度必须为 ${minPasswordBytes} 至 ${maxPasswordBytes} 个 UTF-8 字节。`,
    }
  }
  if (!confirmation) {
    return { field: 'confirmation', message: '请再次输入新密码。' }
  }
  if (newPassword !== confirmation) {
    return { field: 'confirmation', message: '两次输入的新密码不一致。' }
  }
  if (newPassword === currentPassword) {
    return { field: 'new', message: '新密码不能与当前密码相同。' }
  }

  return null
}

function getPasswordChangeFailureIssue(error: unknown): PasswordFormIssue {
  if (error instanceof AuthError) {
    switch (error.code) {
      case 'INVALID_PASSWORD':
        return { field: 'current', message: '当前密码不正确。' }
      case 'PASSWORD_TOO_SHORT':
      case 'PASSWORD_TOO_LONG':
        return {
          field: 'new',
          message: `新密码长度必须为 ${minPasswordBytes} 至 ${maxPasswordBytes} 个 UTF-8 字节。`,
        }
      case 'PASSWORD_UNCHANGED':
        return { field: 'new', message: '新密码不能与当前密码相同。' }
    }
  }

  return { message: passwordChangeFailureMessage }
}

function isAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === 'AbortError'
}

function isTerminalAuthError(error: unknown): error is AuthError {
  return error instanceof AuthError && Boolean(error.code && terminalAuthErrorCodes.has(error.code))
}

function PasswordVisibilityButton({
  field,
  label,
  isVisible,
  isDisabled,
  onToggle,
}: {
  field: PasswordField
  label: string
  isVisible: boolean
  isDisabled: boolean
  onToggle: (field: PasswordField) => void
}) {
  return (
    <button
      type="button"
      aria-label={`${isVisible ? '隐藏' : '显示'}${label}`}
      aria-pressed={isVisible}
      className="rounded-md p-1 text-default-400 outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-primary/35 disabled:cursor-not-allowed disabled:opacity-50"
      disabled={isDisabled}
      onClick={() => onToggle(field)}
    >
      {isVisible
        ? <EyeOff className="h-4 w-4" aria-hidden="true" />
        : <Eye className="h-4 w-4" aria-hidden="true" />}
    </button>
  )
}

export function PasswordChangeForm({
  accountId,
  autoFocus = false,
  isExternallyBusy = false,
  onCancel,
  onSubmittingChange,
  secondaryAction,
  actionsClassName = 'grid grid-cols-1 gap-2 sm:grid-cols-2',
  submitClassName = 'min-h-11 rounded-lg',
}: PasswordChangeFormProps) {
  const navigate = useNavigate()
  const initializeAuth = useAuthStore((state) => state.initialize)
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [formError, setFormError] = useState<string | null>(null)
  const [fieldError, setFieldError] = useState<PasswordFormIssue | null>(null)
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [visibleFields, setVisibleFields] = useState<Record<PasswordField, boolean>>({
    current: false,
    new: false,
    confirmation: false,
  })
  const requestControllerRef = useRef<AbortController | null>(null)
  const currentPasswordRef = useRef<HTMLInputElement | null>(null)
  const newPasswordRef = useRef<HTMLInputElement | null>(null)
  const confirmationRef = useRef<HTMLInputElement | null>(null)

  useEffect(() => {
    onSubmittingChange?.(isSubmitting)
  }, [isSubmitting, onSubmittingChange])

  useEffect(() => {
    return () => {
      requestControllerRef.current?.abort()
      requestControllerRef.current = null
    }
  }, [])

  const isBusy = isSubmitting || isExternallyBusy
  const clearErrors = () => {
    if (formError) {
      setFormError(null)
    }
    if (fieldError) {
      setFieldError(null)
    }
  }
  const focusField = (field: PasswordField) => {
    const input = field === 'current'
      ? currentPasswordRef.current
      : field === 'new'
        ? newPasswordRef.current
        : confirmationRef.current
    input?.focus()
  }
  const showIssue = (issue: PasswordFormIssue) => {
    if (issue.field) {
      setFormError(null)
      setFieldError(issue)
      focusField(issue.field)
      return
    }
    setFieldError(null)
    setFormError(issue.message)
  }
  const clearSensitiveFields = () => {
    setCurrentPassword('')
    setNewPassword('')
    setConfirmation('')
    setFormError(null)
    setFieldError(null)
    setVisibleFields({ current: false, new: false, confirmation: false })
  }
  const toggleVisibility = (field: PasswordField) => {
    setVisibleFields((current) => ({ ...current, [field]: !current[field] }))
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (isBusy) {
      return
    }

    const validationError = validatePasswordChange(currentPassword, newPassword, confirmation)
    if (validationError) {
      showIssue(validationError)
      return
    }

    setFormError(null)
    setFieldError(null)
    setIsSubmitting(true)
    const controller = new AbortController()
    requestControllerRef.current = controller
    try {
      const result = await changePassword({
        old_password: currentPassword,
        new_password: newPassword,
      }, { expectedUserId: accountId, signal: controller.signal })
      if (controller.signal.aborted) {
        return
      }

      clearSensitiveFields()
      addToast(result.warning
        ? {
            title: '密码已修改，请重新登录',
            description: '设备未确认所有登录的注销状态已保存。请使用新密码重新登录，并检查其他设备是否已退出。',
            color: 'warning',
          }
        : { title: '密码已修改，请重新登录', color: 'success' })
      navigate('/login', { replace: true })
    } catch (error) {
      if (controller.signal.aborted) {
        return
      }
      if (isAbortError(error) || (error instanceof AuthError && error.code === 'AUTH_SCOPE_CHANGED')) {
        clearSensitiveFields()
        addToast({
          title: '登录身份已发生变化',
          description: '密码内容已清除，正在重新确认当前登录会话。',
          color: 'warning',
        })
        void initializeAuth()
        navigate('/', { replace: true })
        return
      }
      if (error instanceof AuthError && error.message === '修改密码响应无效') {
        addToast({
          title: '密码修改结果无法确认',
          description: '服务器已接受请求，但返回结果不完整。请使用新密码重新登录；若无法登录，再尝试原密码。',
          color: 'warning',
        })
        navigate('/login', { replace: true })
        return
      }
      if (error instanceof AuthError && error.code === 'PASSWORD_CHANGE_UNCONFIRMED') {
        addToast({
          title: '密码修改结果无法确认',
          description: PASSWORD_CHANGE_UNCONFIRMED_MESSAGE,
          color: 'warning',
        })
        navigate('/login', { replace: true })
        return
      }
      if (isTerminalAuthError(error)) {
        addToast(error.code === 'USER_DISABLED'
          ? {
              title: '当前账户已被禁用',
              description: '请联系管理员恢复账户后重新登录。',
              color: 'warning',
            }
          : {
              title: '登录会话已失效',
              description: '请重新登录后再修改密码。',
              color: 'warning',
            })
        navigate('/login', { replace: true })
        return
      }
      showIssue(getPasswordChangeFailureIssue(error))
    } finally {
      if (requestControllerRef.current === controller) {
        requestControllerRef.current = null
        setIsSubmitting(false)
      }
    }
  }

  return (
    <form onSubmit={handleSubmit} noValidate className="space-y-5">
      {formError && (
        <div role="alert" className="rounded-lg border border-danger/20 bg-danger/5 px-4 py-3 text-sm text-danger">
          {formError}
        </div>
      )}

      <Input
        ref={currentPasswordRef}
        autoFocus={autoFocus}
        type={visibleFields.current ? 'text' : 'password'}
        label="当前密码"
        aria-label="当前密码"
        value={currentPassword}
        onValueChange={(value) => {
          setCurrentPassword(value)
          clearErrors()
        }}
        autoComplete="current-password"
        variant="bordered"
        size="lg"
        isDisabled={isBusy}
        isRequired
        isInvalid={fieldError?.field === 'current'}
        errorMessage={fieldError?.field === 'current' ? <span role="alert">{fieldError.message}</span> : undefined}
        endContent={(
          <PasswordVisibilityButton
            field="current"
            label="当前密码"
            isVisible={visibleFields.current}
            isDisabled={isBusy}
            onToggle={toggleVisibility}
          />
        )}
      />
      <Input
        ref={newPasswordRef}
        type={visibleFields.new ? 'text' : 'password'}
        label="新密码"
        aria-label="新密码"
        description={newPassword
          ? `${utf8ByteLength(newPassword)} / ${maxPasswordBytes} 个 UTF-8 字节，至少 ${minPasswordBytes} 字节`
          : `${minPasswordBytes} 至 ${maxPasswordBytes} 个 UTF-8 字节；中文和表情会占用多个字节`}
        value={newPassword}
        onValueChange={(value) => {
          setNewPassword(value)
          clearErrors()
        }}
        autoComplete="new-password"
        variant="bordered"
        size="lg"
        isDisabled={isBusy}
        isRequired
        isInvalid={fieldError?.field === 'new'}
        errorMessage={fieldError?.field === 'new' ? <span role="alert">{fieldError.message}</span> : undefined}
        endContent={(
          <PasswordVisibilityButton
            field="new"
            label="新密码"
            isVisible={visibleFields.new}
            isDisabled={isBusy}
            onToggle={toggleVisibility}
          />
        )}
      />
      <Input
        ref={confirmationRef}
        type={visibleFields.confirmation ? 'text' : 'password'}
        label="确认新密码"
        aria-label="确认新密码"
        value={confirmation}
        onValueChange={(value) => {
          setConfirmation(value)
          clearErrors()
        }}
        autoComplete="new-password"
        variant="bordered"
        size="lg"
        isDisabled={isBusy}
        isRequired
        isInvalid={fieldError?.field === 'confirmation'}
        errorMessage={fieldError?.field === 'confirmation' ? <span role="alert">{fieldError.message}</span> : undefined}
        endContent={(
          <PasswordVisibilityButton
            field="confirmation"
            label="确认新密码"
            isVisible={visibleFields.confirmation}
            isDisabled={isBusy}
            onToggle={toggleVisibility}
          />
        )}
      />

      <div className={actionsClassName}>
        {onCancel && (
          <Button
            type="button"
            variant="light"
            size="lg"
            className="min-h-11 rounded-lg"
            onPress={onCancel}
            isDisabled={isBusy}
          >
            取消
          </Button>
        )}
        <Button
          type="submit"
          color="primary"
          size="lg"
          className={submitClassName}
          isLoading={isSubmitting}
          isDisabled={isExternallyBusy}
          startContent={!isSubmitting && <KeyRound className="h-4 w-4" aria-hidden="true" />}
        >
          修改密码并重新登录
        </Button>
        {secondaryAction?.({ isBusy, isSubmitting })}
      </div>
    </form>
  )
}
