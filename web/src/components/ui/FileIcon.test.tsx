import type { ReactElement } from 'react'
import { render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { FileIcon } from './FileIcon'

const { mockGetFileIcon } = vi.hoisted(() => ({
  mockGetFileIcon: vi.fn(),
}))

vi.mock('@/lib/utils', async () => {
  const actual = await vi.importActual<typeof import('@/lib/utils')>('@/lib/utils')
  return {
    ...actual,
    getFileIcon: mockGetFileIcon,
  }
})

function renderRoot(ui: ReactElement) {
  const { container } = render(ui)
  const root = container.firstElementChild
  expect(root).not.toBeNull()
  return root as Element
}

describe('FileIcon', () => {
  it('uses the largest tile size for large icons', () => {
    mockGetFileIcon.mockReturnValue('image')

    const icon = renderRoot(<FileIcon name="photo.jpg" isDir={false} size={32} />)

    expect(icon).toHaveClass('w-10', 'h-10', 'bg-success/10', 'text-success')
    expect(icon).toHaveAttribute('aria-hidden', 'true')
    expect(icon.childElementCount).toBe(1)
  })

  it('falls back to the generic file icon and bare color for unknown icon types', () => {
    mockGetFileIcon.mockReturnValue('unknown')

    const icon = renderRoot(
      <FileIcon name="blob.bin" isDir={false} variant="bare" className="custom-icon" />
    )

    expect(icon).toHaveClass('text-default-500', 'custom-icon')
    expect(icon).toHaveAttribute('aria-hidden', 'true')
  })

  it('falls back to generic tile styling for unknown icon types', () => {
    mockGetFileIcon.mockReturnValue('unknown')

    const icon = renderRoot(<FileIcon name="blob.bin" isDir={false} size={20} />)

    expect(icon).toHaveClass('w-7', 'h-7', 'bg-content2', 'text-default-500')
  })
})
