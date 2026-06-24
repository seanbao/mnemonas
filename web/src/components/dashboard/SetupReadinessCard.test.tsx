import type { ComponentProps } from 'react'
import { describe, expect, it, vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import { render, screen } from '@/test/utils'
import type { SetupReadiness, SetupReadinessAction } from '@/api/setup'
import { SetupReadinessCard } from './SetupReadinessCard'

function createReadiness(overrides: Partial<SetupReadiness> = {}): SetupReadiness {
  return {
    lifecycle: 'pending',
    overall_status: 'action_required',
    prompt: true,
    generated_at: '2026-07-13T01:02:03Z',
    can_complete: false,
    can_defer: true,
    required: { completed: 1, total: 2 },
    recommended: { completed: 0, total: 1 },
    checks: [
      {
        id: 'bootstrap_credential',
        requirement: 'required',
        status: 'complete',
        deferrable: false,
        title: '修改初始密码',
        message: '所有管理员均已修改初始密码。',
        action: 'change_password',
      },
      {
        id: 'backup_job',
        requirement: 'required',
        status: 'incomplete',
        deferrable: false,
        title: '创建备份',
        message: '尚未创建已启用的备份任务。',
        action: 'create_backup',
      },
      {
        id: 'restore_verification',
        requirement: 'recommended',
        status: 'unavailable',
        deferrable: true,
        title: '验证恢复流程',
        message: '当前没有可用的恢复验证记录。',
        action: 'run_restore_drill',
      },
    ],
    summary: {
      auth_enabled: true,
      active_admin_count: 1,
      password_change_required_admin_count: 0,
      initial_password_file: 'missing',
      enabled_backup_job_count: 0,
      latest_backup_success_at: '2026-07-12T00:00:00Z',
      security_status: 'warning',
      security_blocking_check_ids: ['public_access'],
    },
    ...overrides,
  }
}

function renderCard(
  readinessOverrides: Partial<SetupReadiness> = {},
  propOverrides: Partial<ComponentProps<typeof SetupReadinessCard>> = {},
) {
  const props: ComponentProps<typeof SetupReadinessCard> = {
    readiness: createReadiness(readinessOverrides),
    onRetry: vi.fn(),
    onAction: vi.fn(),
    onComplete: vi.fn(),
    onDefer: vi.fn(),
    ...propOverrides,
  }
  const view = render(<SetupReadinessCard {...props} />)
  return { ...view, props }
}

describe('SetupReadinessCard', () => {
  it('shows a compact server-derived summary and expands required and recommended evidence', async () => {
    const user = userEvent.setup()
    const { props } = renderCard()

    expect(screen.getByRole('region', { name: '首次设置检查' })).toBeInTheDocument()
    expect(screen.getByText('根据自动检测结果完成必要项目。')).toBeInTheDocument()
    expect(screen.getByText('必需 1/2')).toBeInTheDocument()
    expect(screen.getByText('建议 0/1')).toBeInTheDocument()
    expect(screen.queryByText('设备摘要')).not.toBeInTheDocument()

    const expandButton = screen.getByRole('button', { name: '查看检测结果' })
    expect(expandButton).toHaveAttribute('aria-expanded', 'false')
    await user.click(expandButton)

    expect(screen.getByRole('button', { name: '收起检测结果' })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('heading', { name: '必需项目' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '建议项目' })).toBeInTheDocument()
    expect(screen.getByText('所有管理员均已修改初始密码。')).toBeInTheDocument()
    expect(screen.getByText('当前没有可用的恢复验证记录。')).toBeInTheDocument()
    expect(screen.getByText('已移除')).toBeInTheDocument()
    expect(screen.getByText('尚无记录')).toBeInTheDocument()
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '创建备份' }))
    await user.click(screen.getByRole('button', { name: '运行恢复演练' }))
    expect(props.onAction).toHaveBeenNthCalledWith(1, 'create_backup')
    expect(props.onAction).toHaveBeenNthCalledWith(2, 'run_restore_drill')
    expect(screen.queryByRole('button', { name: '修改密码' })).not.toBeInTheDocument()
  })

  it.each<[SetupReadinessAction, string]>([
    ['change_password', '修改密码'],
    ['manage_users', '管理用户'],
    ['create_backup', '创建备份'],
    ['run_backup', '运行备份'],
    ['run_restore_drill', '运行恢复演练'],
    ['review_security', '查看安全检查'],
  ])('sends the %s action enum to the parent callback', async (action, label) => {
    const user = userEvent.setup()
    const { props } = renderCard({
      required: { completed: 0, total: 1 },
      recommended: { completed: 0, total: 0 },
      checks: [{
        id: action,
        requirement: 'required',
        status: 'incomplete',
        deferrable: false,
        title: label,
        message: '自动检测结论。',
        action,
      }],
    })

    await user.click(screen.getByRole('button', { name: '查看检测结果' }))
    await user.click(screen.getByRole('button', { name: label }))
    expect(props.onAction).toHaveBeenCalledWith(action)
  })

  it('renders completion and defer actions only when the server allows them', async () => {
    const user = userEvent.setup()
    const { props, rerender } = renderCard()

    expect(screen.getByRole('button', { name: '稍后提醒' })).toBeEnabled()
    expect(screen.queryByRole('button', { name: '完成设置' })).not.toBeInTheDocument()

    rerender(
      <SetupReadinessCard
        {...props}
        readiness={createReadiness({ can_complete: true, can_defer: false })}
      />,
    )
    expect(screen.queryByRole('button', { name: '稍后提醒' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '完成设置' }))
    expect(props.onComplete).toHaveBeenCalledTimes(1)

    rerender(
      <SetupReadinessCard
        {...props}
        readiness={createReadiness({ can_complete: false, can_defer: false })}
      />,
    )
    expect(screen.queryByRole('button', { name: '稍后提醒' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '完成设置' })).not.toBeInTheDocument()
  })

  it('offers the four bounded defer choices without hiding before the parent updates readiness', async () => {
    const user = userEvent.setup()
    const { props, rerender } = renderCard()

    await user.click(screen.getByRole('button', { name: '稍后提醒' }))
    expect(screen.getByText('设置下次提醒时间')).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: '1 天后提醒' })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: '3 天后提醒' })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: '7 天后提醒' })).toBeChecked()
    expect(screen.getByRole('radio', { name: '30 天后提醒' })).toBeInTheDocument()

    await user.click(screen.getByRole('radio', { name: '30 天后提醒' }))
    await user.click(screen.getByRole('button', { name: '确认延期' }))

    expect(props.onDefer).toHaveBeenCalledWith({ remind_in_days: 30 })
    expect(screen.getByText('设置下次提醒时间')).toBeInTheDocument()

    rerender(
      <SetupReadinessCard
        {...props}
        readiness={createReadiness()}
        mutationError="延期请求未保存，请重试。"
      />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('延期请求未保存，请重试。')
    await user.click(screen.getByRole('button', { name: '取消' }))
    expect(screen.queryByText('设置下次提醒时间')).not.toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent('延期请求未保存，请重试。')
  })

  it('keeps mutation controls unavailable while a parent-owned mutation is pending', async () => {
    const user = userEvent.setup()
    const { props, rerender } = renderCard({ can_complete: true, can_defer: true })

    await user.click(screen.getByRole('button', { name: '稍后提醒' }))

    rerender(
      <SetupReadinessCard
        {...props}
        readiness={createReadiness({ can_complete: true, can_defer: true })}
        isCompleting
      />,
    )
    expect(screen.getByRole('button', { name: '稍后提醒' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '完成设置' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '取消' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '确认延期' })).toBeDisabled()
    expect(screen.getByRole('radio', { name: '7 天后提醒' })).toBeDisabled()

    rerender(
      <SetupReadinessCard
        {...props}
        readiness={createReadiness({ can_complete: true, can_defer: true })}
        isCompleting={false}
        isDeferring
      />,
    )
    expect(screen.getByRole('button', { name: '取消' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '确认延期' })).toBeDisabled()
  })

  it('formats every server summary state and the ready prompt', async () => {
    const user = userEvent.setup()
    const { props, rerender } = renderCard({
      overall_status: 'ready',
      summary: {
        ...createReadiness().summary,
        initial_password_file: 'present',
        security_status: 'pass',
      },
    })

    expect(screen.getByText('必要项目已完成，可以结束首次设置。')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '查看检测结果' }))
    expect(screen.getByText('仍然存在')).toBeInTheDocument()
    expect(screen.getByText('已通过')).toBeInTheDocument()

    rerender(
      <SetupReadinessCard
        {...props}
        readiness={createReadiness({
          summary: {
            ...createReadiness().summary,
            initial_password_file: 'unavailable',
            security_status: 'block',
          },
        })}
      />,
    )
    expect(screen.getAllByText('无法确认')).not.toHaveLength(0)
    expect(screen.getByText('存在阻断项')).toBeInTheDocument()

    rerender(
      <SetupReadinessCard
        {...props}
        readiness={createReadiness({
          summary: {
            ...createReadiness().summary,
            initial_password_file: 'missing',
            security_status: 'unavailable',
          },
        })}
      />,
    )
    expect(screen.getAllByText('无法确认')).not.toHaveLength(0)
  })

  it('shows non-applicable evidence without an action or subjective control', async () => {
    const user = userEvent.setup()
    renderCard({
      required: { completed: 1, total: 1 },
      recommended: { completed: 0, total: 0 },
      checks: [{
        id: 'not_applicable',
        requirement: 'required',
        status: 'not_applicable',
        deferrable: false,
        title: '无需执行',
        message: '此项不适用于当前配置。',
        action: 'review_security',
      }],
    })

    await user.click(screen.getByRole('button', { name: '查看检测结果' }))
    expect(screen.getByText('不适用')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '查看安全检查' })).not.toBeInTheDocument()
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
  })

  it('presents unavailable evidence with an explicit retry action', async () => {
    const user = userEvent.setup()
    const { props, rerender } = renderCard({
      overall_status: 'unavailable',
      can_defer: false,
    })

    expect(screen.getByRole('region', { name: '设置检查暂不可用' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重新检查' }))
    expect(props.onRetry).toHaveBeenCalledTimes(1)

    rerender(
      <SetupReadinessCard
        {...props}
        readiness={createReadiness({ overall_status: 'unavailable', can_defer: false })}
        isRetrying
      />,
    )
    expect(screen.getByRole('button', { name: '重新检查' })).toBeDisabled()

    rerender(
      <SetupReadinessCard
        {...props}
        readiness={createReadiness({ overall_status: 'unavailable', can_defer: false })}
        isRetrying={false}
        isCompleting
      />,
    )
    expect(screen.getByRole('button', { name: '重新检查' })).toBeDisabled()
  })

  it.each([
    ['completed', '首次设置已完成', { completed_at: '2026-07-13T01:03:00Z' }],
    ['deferred', '设置提醒已延期', { deferred_until: '2026-07-20T01:03:00Z' }],
  ] as const)('renders the %s lifecycle as a lightweight status', (lifecycle, label, timestamps) => {
    renderCard({
      lifecycle,
      prompt: false,
      can_complete: false,
      can_defer: false,
      ...timestamps,
    })

    expect(screen.getByRole('region', { name: label })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '查看检测结果' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '完成设置' })).not.toBeInTheDocument()
  })
})
