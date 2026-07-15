import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ReviewHistory } from './UserAccessHistory'

describe('ReviewHistory', () => {
  it('shows the recorded time using a semantic time element', () => {
    render(
      <ReviewHistory
        entries={[{
          id: 'review-1',
          recordedAt: '2026-07-18T08:00:00Z',
          reviewer: 'admin',
          title: '用户矩阵',
          path: '/team',
          preview: false,
          users: 2,
          readAllowed: 1,
          writeAllowed: 1,
          relatedShares: 0,
          reportText: '目录权限复核记录',
        }]}
        onCopy={vi.fn()}
        onClear={vi.fn()}
      />,
    )

    const recordedAt = screen.getByText((_, element) => element?.tagName === 'TIME')
    expect(recordedAt).toHaveAttribute('dateTime', '2026-07-18T08:00:00Z')
    expect(recordedAt).not.toHaveTextContent('--')
  })
})
