import { useState } from 'react'
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import {
  AccessCoverageSummary,
  AccessRuleChangeReview,
  AccessRuleEditor,
  DirectoryQuotaChangeReview,
} from './UserAccessEditors'

function AccessRuleEditorHarness() {
  const [value, setValue] = useState('')
  return (
    <>
      <AccessRuleEditor value={value} onChange={setValue} />
      <output aria-label="目录权限规则值">{value}</output>
    </>
  )
}

describe('directory policy editors', () => {
  it('shows added, changed, and removed quota entries', () => {
    render(
      <DirectoryQuotaChangeReview
        saved={[
          { path: '/team', quota_bytes: 1073741824 },
          { path: '/archive', quota_bytes: 10737418240 },
        ]}
        draftValue={'/team 2 GB\n/media 512 MB'}
      />,
    )

    const review = within(screen.getByLabelText('目录配额变更复核'))
    expect(review.getByText('新增 1')).toBeTruthy()
    expect(review.getByText('修改 1')).toBeTruthy()
    expect(review.getByText('删除 1')).toBeTruthy()
    expect(review.getByText('/team')).toBeTruthy()
    expect(review.getByText('/media')).toBeTruthy()
    expect(review.getByText('/archive')).toBeTruthy()
    expect(review.getByText('配额从 1 GB 调整为 2 GB')).toBeTruthy()
    expect(review.getByText('容量 512 MB')).toBeTruthy()
    expect(review.getByText('容量 10 GB')).toBeTruthy()
  })

  it('shows added, changed, and removed access rules with changed fields', () => {
    render(
      <AccessRuleChangeReview
        saved={[
          { path: '/team', read_groups: ['family'] },
          { path: '/archive', read_roles: ['admin'], write_roles: ['admin'] },
        ]}
        draftValue={'/team read_groups=family write_groups=family\n/shared read_roles=user'}
      />,
    )

    const review = within(screen.getByLabelText('目录权限变更复核'))
    expect(review.getByText('新增 1')).toBeTruthy()
    expect(review.getByText('修改 1')).toBeTruthy()
    expect(review.getByText('删除 1')).toBeTruthy()
    expect(review.getByText('变更字段：写组')).toBeTruthy()
    expect(review.getByText('/team')).toBeTruthy()
    expect(review.getByText('/shared')).toBeTruthy()
    expect(review.getByText('/archive')).toBeTruthy()
    expect(review.getByText('读角色: user')).toBeTruthy()
    expect(review.getByText('读角色: admin · 写角色: admin')).toBeTruthy()
  })

  it('summarizes broad-access rules as attention items', () => {
    render(
      <AccessCoverageSummary
        draftValue={'/ read_roles=user\n/shared read_roles=user write_roles=user\n/team read_groups=family write_groups=editors'}
      />,
    )

    const summary = within(screen.getByLabelText('目录权限覆盖摘要'))
    expect(summary.getByText('3 条')).toBeTruthy()
    expect(summary.getByText('3 个')).toBeTruthy()
    expect(summary.getAllByText('2 个')).toHaveLength(2)
    expect(summary.getByText('发现 2 条根路径、访客或普通用户宽权限规则，请重点复核。')).toBeTruthy()
  })

  it.each([
    ['全员协作', '/shared', 'user', 'user', '/shared read_roles=user write_roles=user'],
    ['全员只读', '/library', 'user', 'admin', '/library read_roles=user write_roles=admin'],
    ['管理员归档', '/archive', 'admin', 'admin', '/archive read_roles=admin write_roles=admin'],
  ])('applies the %s access-rule preset as a structured draft', async (
    preset,
    path,
    readRole,
    writeRole,
    serialized,
  ) => {
    const user = userEvent.setup()
    render(<AccessRuleEditorHarness />)

    await user.click(screen.getByRole('button', { name: new RegExp(preset) }))

    expect(screen.getByLabelText('目录权限路径 1')).toHaveValue(path)
    expect(screen.getByLabelText('读角色 1')).toHaveValue(readRole)
    expect(screen.getByLabelText('写角色 1')).toHaveValue(writeRole)
    expect(screen.getByLabelText('目录权限规则值')).toHaveTextContent(serialized)
  })

  it('keeps line-syntax quotes as invalid structured path data', async () => {
    render(<AccessRuleEditorHarness />)

    fireEvent.change(screen.getByLabelText('目录权限路径 1'), {
      target: { value: '"/Family Photos' },
    })

    expect(screen.getByLabelText('目录权限路径 1')).toHaveValue('"/Family Photos')
    expect(screen.getByLabelText('目录权限规则值')).toHaveTextContent(
      '"\\"/Family Photos"',
    )
  })
})
