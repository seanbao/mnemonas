import { fireEvent } from '@testing-library/react'
import type { ComponentProps } from 'react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@/test/utils'
import { CreateLocalBackupDialog } from './CreateLocalBackupDialog'

function renderDialog(overrides: Partial<ComponentProps<typeof CreateLocalBackupDialog>> = {}) {
  const props: ComponentProps<typeof CreateLocalBackupDialog> = {
    isOpen: true,
    isSubmitting: false,
    onClose: vi.fn(),
    onSubmit: vi.fn(),
    ...overrides,
  }
  render(<CreateLocalBackupDialog {...props} />)
  return props
}

async function fillDestination(user: ReturnType<typeof userEvent.setup>, value = '/mnt/backup-drive/mnemonas') {
  const destination = screen.getByRole('textbox', { name: '目标目录' })
  await user.clear(destination)
  await user.type(destination, value)
}

describe('CreateLocalBackupDialog', () => {
  it('shows a small consumer-oriented form with safe defaults', () => {
    renderDialog()

    expect(screen.getByText('添加本地备份')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: '备份名称' })).toHaveValue('外置硬盘备份')
    expect(screen.getByRole('textbox', { name: '目标目录' })).toHaveValue('')
    expect(screen.getByRole('checkbox', { name: '每天自动备份' })).toBeChecked()
    expect(screen.getByText('创建后将开始首次备份，之后每天自动运行。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '创建并开始首次备份' })).toBeEnabled()
    expect(screen.getByText(/7 份备份/)).toBeInTheDocument()
  })

  it('submits automatic backups without a schedule override', async () => {
    const user = userEvent.setup()
    const props = renderDialog()
    await fillDestination(user)

    await user.click(screen.getByRole('button', { name: '创建并开始首次备份' }))

    expect(props.onSubmit).toHaveBeenCalledWith({
      name: '外置硬盘备份',
      destination: '/mnt/backup-drive/mnemonas',
    })
  })

  it('submits manual backups with schedule_interval set to zero', async () => {
    const user = userEvent.setup()
    const props = renderDialog()
    await fillDestination(user, '/mnt/manual-backup')
    await user.click(screen.getByRole('checkbox', { name: '每天自动备份' }))

    expect(screen.getByText('仅创建任务；需要备份时可在任务列表中选择“立即备份”。')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '仅创建备份任务' }))

    expect(props.onSubmit).toHaveBeenCalledWith({
      name: '外置硬盘备份',
      destination: '/mnt/manual-backup',
      schedule_interval: '0',
    })
  })

  it('keeps the draft and shows inline validation errors', async () => {
    const user = userEvent.setup()
    const props = renderDialog()
    await user.clear(screen.getByRole('textbox', { name: '备份名称' }))
    await user.click(screen.getByRole('button', { name: '创建并开始首次备份' }))

    expect(props.onSubmit).not.toHaveBeenCalled()
    expect(screen.getByRole('textbox', { name: '备份名称' })).toHaveAttribute('errormessage', '请填写备份名称。')
    expect(screen.getByRole('textbox', { name: '目标目录' })).toHaveAttribute('errormessage', '请填写备份目标目录。')
  })

  it('rejects control characters in the backup name', async () => {
    const user = userEvent.setup()
    const props = renderDialog()
    fireEvent.change(screen.getByRole('textbox', { name: '备份名称' }), { target: { value: 'backup\u0081job' } })
    fireEvent.change(screen.getByRole('textbox', { name: '目标目录' }), { target: { value: '/mnt/backup' } })
    await user.click(screen.getByRole('button', { name: '创建并开始首次备份' }))

    expect(props.onSubmit).not.toHaveBeenCalled()
    expect(screen.getByRole('textbox', { name: '备份名称' })).toHaveAttribute('errormessage', '备份名称不能包含控制字符。')
  })

  it.each([
    ['relative path', 'mnt/backup', '目标目录必须是服务器上的绝对路径。'],
    ['filesystem root', '/', '不能把文件系统根目录作为备份目标。'],
    ['backslash', '/mnt\\backup', '目标目录不能包含反斜杠或控制字符。'],
    ['dot segment', '/mnt/../backup', '目标目录不能包含 . 或 .. 路径段。'],
  ])('rejects an invalid destination: %s', async (_label, value, message) => {
    const user = userEvent.setup()
    const props = renderDialog()
    fireEvent.change(screen.getByRole('textbox', { name: '目标目录' }), { target: { value } })
    await user.click(screen.getByRole('button', { name: '创建并开始首次备份' }))

    expect(props.onSubmit).not.toHaveBeenCalled()
    const destination = screen.getByRole('textbox', { name: '目标目录' })
    expect(destination).toHaveAttribute('errormessage', message)
    expect(destination).toHaveValue(value)
  })

  it('prevents dismissal and duplicate submission while creating', async () => {
    const user = userEvent.setup()
    const props = renderDialog({ isSubmitting: true })

    expect(screen.getByRole('textbox', { name: '备份名称' })).toBeDisabled()
    expect(screen.getByRole('textbox', { name: '目标目录' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '取消' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: '创建并开始首次备份' }))
    const form = screen.getByRole('button', { name: '创建并开始首次备份' }).closest('form')
    expect(form).not.toBeNull()
    fireEvent.submit(form!)
    expect(props.onSubmit).not.toHaveBeenCalled()
  })

  it('closes from the cancel action when idle', async () => {
    const user = userEvent.setup()
    const props = renderDialog()

    await user.click(screen.getByRole('button', { name: '取消' }))
    expect(props.onClose).toHaveBeenCalledTimes(1)
  })
})
