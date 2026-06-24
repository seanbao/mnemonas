import { useId, useState } from 'react'
import {
  Button,
  Card,
  CardBody,
  Chip,
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from '@heroui/react'
import {
  AlertCircle,
  CheckCircle2,
  ChevronDown,
  CircleHelp,
  Clock3,
  MinusCircle,
  RefreshCw,
  ShieldCheck,
} from 'lucide-react'
import {
  SETUP_DEFER_DAYS,
  type DeferSetupRequest,
  type SetupDeferDays,
  type SetupReadiness,
  type SetupReadinessAction,
  type SetupReadinessCheck,
  type SetupReadinessCheckStatus,
} from '@/api/setup'
import { cn } from '@/lib/utils'

export interface SetupReadinessCardProps {
  readiness: SetupReadiness
  isRetrying?: boolean
  isCompleting?: boolean
  isDeferring?: boolean
  mutationError?: string | null
  onRetry: () => void
  onAction: (action: SetupReadinessAction) => void
  onComplete: () => void
  onDefer: (request: DeferSetupRequest) => void
}

const actionLabels: Record<SetupReadinessAction, string> = {
  change_password: '修改密码',
  manage_users: '管理用户',
  create_backup: '创建备份',
  run_backup: '运行备份',
  run_restore_drill: '运行恢复演练',
  review_security: '查看安全检查',
}

const checkStatusPresentation: Record<SetupReadinessCheckStatus, {
  label: string
  className: string
  icon: typeof CheckCircle2
  iconClassName: string
}> = {
  complete: {
    label: '已完成',
    className: 'border-success/25 bg-success/10 text-success',
    icon: CheckCircle2,
    iconClassName: 'text-success',
  },
  incomplete: {
    label: '待处理',
    className: 'border-warning/25 bg-warning/10 text-warning',
    icon: AlertCircle,
    iconClassName: 'text-warning',
  },
  unavailable: {
    label: '无法确认',
    className: 'border-divider bg-content2 text-default-600',
    icon: CircleHelp,
    iconClassName: 'text-default-500',
  },
  not_applicable: {
    label: '不适用',
    className: 'border-divider bg-content2 text-default-600',
    icon: MinusCircle,
    iconClassName: 'text-default-500',
  },
}

const deferOptionLabels: Record<SetupDeferDays, string> = {
  1: '1 天后提醒',
  3: '3 天后提醒',
  7: '7 天后提醒',
  30: '30 天后提醒',
}

function formatDateTime(value: string | undefined): string {
  if (!value) {
    return '尚无记录'
  }
  return new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value))
}

function getInitialPasswordFileLabel(status: SetupReadiness['summary']['initial_password_file']): string {
  switch (status) {
    case 'missing':
      return '已移除'
    case 'present':
      return '仍然存在'
    case 'unavailable':
      return '无法确认'
  }
}

function getSecurityStatusLabel(status: SetupReadiness['summary']['security_status']): string {
  switch (status) {
    case 'pass':
      return '已通过'
    case 'warning':
      return '有警告'
    case 'block':
      return '存在阻断项'
    case 'unavailable':
      return '无法确认'
  }
}

function getPromptText(readiness: SetupReadiness): string {
  switch (readiness.overall_status) {
    case 'ready':
      return '必要项目已完成，可以结束首次设置。'
    case 'action_required':
      return '根据自动检测结果完成必要项目。'
    case 'unavailable':
      return '部分自动检测当前不可用，请重新检查。'
  }
}

function CheckItem({
  check,
  isDisabled,
  onAction,
}: {
  check: SetupReadinessCheck
  isDisabled: boolean
  onAction: (action: SetupReadinessAction) => void
}) {
  const presentation = checkStatusPresentation[check.status]
  const StatusIcon = presentation.icon
  const showAction = check.action !== undefined
    && check.status !== 'complete'
    && check.status !== 'not_applicable'

  return (
    <li className="rounded-lg border border-divider bg-content1/70 p-3 sm:p-4">
      <div className="flex items-start gap-3">
        <StatusIcon
          size={18}
          className={cn('mt-0.5 shrink-0', presentation.iconClassName)}
          aria-hidden="true"
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h4 className="text-sm font-medium text-foreground">{check.title}</h4>
            <span className={cn('rounded-full border px-2 py-0.5 text-xs font-medium', presentation.className)}>
              {presentation.label}
            </span>
            {check.deferrable && check.status === 'incomplete' && (
              <span className="text-xs text-default-500">可延期处理</span>
            )}
          </div>
          <p className="mt-1 text-xs leading-5 text-default-600">{check.message}</p>
          {showAction && (
            <Button
              size="sm"
              variant="flat"
              className="mt-3 min-h-10 w-full rounded-lg sm:w-auto"
              isDisabled={isDisabled}
              onPress={() => onAction(check.action!)}
            >
              {actionLabels[check.action!]}
            </Button>
          )}
        </div>
      </div>
    </li>
  )
}

function CheckGroup({
  id,
  title,
  checks,
  isDisabled,
  onAction,
}: {
  id: string
  title: string
  checks: SetupReadinessCheck[]
  isDisabled: boolean
  onAction: (action: SetupReadinessAction) => void
}) {
  if (checks.length === 0) {
    return null
  }

  return (
    <section aria-labelledby={id}>
      <h3 id={id} className="mb-2 text-xs font-semibold text-default-600">{title}</h3>
      <ul className="space-y-2">
        {checks.map((check) => (
          <CheckItem
            key={check.id}
            check={check}
            isDisabled={isDisabled}
            onAction={onAction}
          />
        ))}
      </ul>
    </section>
  )
}

function ReadinessSummary({ readiness, titleId }: { readiness: SetupReadiness; titleId: string }) {
  const items = [
    ['登录认证', readiness.summary.auth_enabled ? '已启用' : '未启用'],
    ['可用管理员', `${readiness.summary.active_admin_count} 个`],
    ['需要修改密码的管理员', `${readiness.summary.password_change_required_admin_count} 个`],
    ['初始密码文件', getInitialPasswordFileLabel(readiness.summary.initial_password_file)],
    ['已启用备份任务', `${readiness.summary.enabled_backup_job_count} 个`],
    ['最近成功备份', formatDateTime(readiness.summary.latest_backup_success_at)],
    ['最近恢复验证', formatDateTime(readiness.summary.latest_restore_verification_at)],
    ['安全检查', getSecurityStatusLabel(readiness.summary.security_status)],
  ] as const

  return (
    <section aria-labelledby={titleId}>
      <h3 id={titleId} className="mb-2 text-xs font-semibold text-default-600">设备摘要</h3>
      <dl className="grid grid-cols-1 gap-px overflow-hidden rounded-lg border border-divider bg-divider sm:grid-cols-2">
        {items.map(([label, value]) => (
          <div key={label} className="flex items-center justify-between gap-4 bg-content1 px-3 py-2.5">
            <dt className="text-xs text-default-500">{label}</dt>
            <dd className="text-right text-xs font-medium text-foreground">{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  )
}

function LifecycleCard({ readiness }: { readiness: SetupReadiness }) {
  const isCompleted = readiness.lifecycle === 'completed'

  return (
    <Card
      className="border border-divider bg-content1 shadow-none"
      role="region"
      aria-label={isCompleted ? '首次设置已完成' : '设置提醒已延期'}
    >
      <CardBody className="flex flex-row items-start gap-3 p-4 sm:items-center">
        {isCompleted ? (
          <CheckCircle2 size={18} className="mt-0.5 shrink-0 text-success sm:mt-0" aria-hidden="true" />
        ) : (
          <Clock3 size={18} className="mt-0.5 shrink-0 text-default-500 sm:mt-0" aria-hidden="true" />
        )}
        <div className="min-w-0">
          <p className="text-sm font-medium text-foreground">
            {isCompleted ? '首次设置已完成' : '设置提醒已延期'}
          </p>
          <p className="mt-0.5 text-xs text-default-500">
            {isCompleted
              ? `完成时间：${formatDateTime(readiness.completed_at)}`
              : `下次提醒时间：${formatDateTime(readiness.deferred_until)}`}
          </p>
        </div>
      </CardBody>
    </Card>
  )
}

export function SetupReadinessCard({
  readiness,
  isRetrying = false,
  isCompleting = false,
  isDeferring = false,
  mutationError,
  onRetry,
  onAction,
  onComplete,
  onDefer,
}: SetupReadinessCardProps) {
  const [isExpanded, setIsExpanded] = useState(false)
  const [isDeferOpen, setIsDeferOpen] = useState(false)
  const [deferDays, setDeferDays] = useState<SetupDeferDays>(7)
  const instanceId = useId()

  if (readiness.lifecycle !== 'pending') {
    return <LifecycleCard readiness={readiness} />
  }

  const requiredChecks = readiness.checks.filter((check) => check.requirement === 'required')
  const recommendedChecks = readiness.checks.filter((check) => check.requirement === 'recommended')
  const mutationPending = isCompleting || isDeferring
  const isUnavailable = readiness.overall_status === 'unavailable'
  const titleId = `${instanceId}-title`
  const detailsId = `${instanceId}-details`
  const summaryTitleId = `${instanceId}-summary-title`
  const requiredTitleId = `${instanceId}-required-title`
  const recommendedTitleId = `${instanceId}-recommended-title`

  const handleDeferOpenChange = (open: boolean) => {
    if (!mutationPending) {
      setIsDeferOpen(open)
    }
  }

  return (
    <>
      <Card
        className={cn(
          'border shadow-none',
          isUnavailable ? 'border-warning/30 bg-warning/5' : 'border-primary/20 bg-primary/5',
        )}
        role="region"
        aria-labelledby={titleId}
      >
        <CardBody className="p-0">
          <div className="flex flex-col gap-4 p-4 sm:p-5">
            <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
              <div className="flex min-w-0 items-start gap-3">
                <span className={cn(
                  'flex h-9 w-9 shrink-0 items-center justify-center rounded-lg',
                  isUnavailable ? 'bg-warning/15 text-warning' : 'bg-primary/10 text-primary',
                )}>
                  {isUnavailable
                    ? <CircleHelp size={19} aria-hidden="true" />
                    : <ShieldCheck size={19} aria-hidden="true" />}
                </span>
                <div className="min-w-0">
                  <h2 id={titleId} className="text-sm font-semibold text-foreground">
                    {isUnavailable ? '设置检查暂不可用' : '首次设置检查'}
                  </h2>
                  <p className="mt-1 text-xs leading-5 text-default-600">{getPromptText(readiness)}</p>
                  <div className="mt-2 flex flex-wrap gap-2" aria-label="设置检查进度">
                    <Chip size="sm" variant="flat" color={readiness.can_complete ? 'success' : 'primary'}>
                      必需 {readiness.required.completed}/{readiness.required.total}
                    </Chip>
                    <Chip size="sm" variant="flat" color="default">
                      建议 {readiness.recommended.completed}/{readiness.recommended.total}
                    </Chip>
                  </div>
                </div>
              </div>
              <div className="grid w-full grid-cols-1 gap-2 sm:w-auto sm:grid-cols-2">
                {isUnavailable && (
                  <Button
                    size="sm"
                    variant="flat"
                    className="min-h-11 w-full rounded-lg sm:w-auto"
                    startContent={!isRetrying && <RefreshCw size={15} aria-hidden="true" />}
                    isLoading={isRetrying}
                    isDisabled={mutationPending}
                    onPress={onRetry}
                  >
                    重新检查
                  </Button>
                )}
                <Button
                  size="sm"
                  variant="light"
                  className="min-h-11 w-full rounded-lg sm:w-auto"
                  aria-expanded={isExpanded}
                  aria-controls={detailsId}
                  endContent={(
                    <ChevronDown
                      size={16}
                      className={cn('transition-transform', isExpanded && 'rotate-180')}
                      aria-hidden="true"
                    />
                  )}
                  onPress={() => setIsExpanded((current) => !current)}
                >
                  {isExpanded ? '收起检测结果' : '查看检测结果'}
                </Button>
              </div>
            </div>

            {mutationError && !isDeferOpen && (
              <div role="alert" className="rounded-lg border border-danger/25 bg-danger/10 px-3 py-2 text-xs text-danger">
                {mutationError}
              </div>
            )}

            {isExpanded && (
              <div id={detailsId} className="space-y-4 border-t border-divider pt-4">
                <ReadinessSummary readiness={readiness} titleId={summaryTitleId} />
                <CheckGroup
                  id={requiredTitleId}
                  title="必需项目"
                  checks={requiredChecks}
                  isDisabled={mutationPending}
                  onAction={onAction}
                />
                <CheckGroup
                  id={recommendedTitleId}
                  title="建议项目"
                  checks={recommendedChecks}
                  isDisabled={mutationPending}
                  onAction={onAction}
                />
                <p className="text-xs text-default-500">检测时间：{formatDateTime(readiness.generated_at)}</p>
              </div>
            )}

            {(readiness.can_complete || readiness.can_defer) && (
              <div className="grid grid-cols-1 gap-2 border-t border-divider pt-4 sm:flex sm:justify-end">
                {readiness.can_defer && (
                  <Button
                    size="sm"
                    variant="flat"
                    className="min-h-11 w-full rounded-lg sm:w-auto"
                    isDisabled={mutationPending}
                    onPress={() => handleDeferOpenChange(true)}
                  >
                    稍后提醒
                  </Button>
                )}
                {readiness.can_complete && (
                  <Button
                    size="sm"
                    color="primary"
                    className="min-h-11 w-full rounded-lg sm:w-auto"
                    isLoading={isCompleting}
                    isDisabled={isDeferring}
                    onPress={onComplete}
                  >
                    完成设置
                  </Button>
                )}
              </div>
            )}
          </div>
        </CardBody>
      </Card>

      <Modal
        isOpen={isDeferOpen}
        onOpenChange={handleDeferOpenChange}
        isDismissable={!mutationPending}
        hideCloseButton={mutationPending}
        placement="center"
        classNames={{ base: 'border border-divider bg-content1' }}
      >
        <ModalContent>
          <ModalHeader className="flex flex-col gap-1">
            <span className="text-base font-semibold">设置下次提醒时间</span>
            <span className="text-xs font-normal text-default-500">延期不会改变当前检测结果。</span>
          </ModalHeader>
          <ModalBody>
            <fieldset disabled={mutationPending}>
              <legend className="mb-2 text-sm font-medium text-foreground">提醒时间</legend>
              <div className="space-y-2">
              {SETUP_DEFER_DAYS.map((days) => (
                <label
                  key={days}
                  className="flex min-h-11 cursor-pointer items-center gap-3 rounded-lg border border-divider px-3 py-2 text-sm text-foreground has-[:checked]:border-primary/40 has-[:checked]:bg-primary/5"
                >
                  <input
                    type="radio"
                    name="setup-reminder-delay"
                    value={days}
                    checked={deferDays === days}
                    onChange={() => setDeferDays(days)}
                    className="h-4 w-4 accent-primary"
                  />
                  {deferOptionLabels[days]}
                </label>
              ))}
              </div>
            </fieldset>
            {mutationError && (
              <div role="alert" className="rounded-lg border border-danger/25 bg-danger/10 px-3 py-2 text-xs text-danger">
                {mutationError}
              </div>
            )}
          </ModalBody>
          <ModalFooter className="grid grid-cols-1 gap-2 sm:flex sm:justify-end">
            <Button
              variant="light"
              className="min-h-11 rounded-lg"
              isDisabled={mutationPending}
              onPress={() => handleDeferOpenChange(false)}
            >
              取消
            </Button>
            <Button
              color="primary"
              className="min-h-11 rounded-lg"
              isLoading={isDeferring}
              isDisabled={isCompleting}
              onPress={() => onDefer({ remind_in_days: deferDays })}
            >
              确认延期
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </>
  )
}
