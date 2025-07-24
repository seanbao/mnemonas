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

describe('FileIcon', () => {
  it('uses the largest tile size for large icons', () => {
    mockGetFileIcon.mockReturnValue('image')

    const { container } = render(<FileIcon name="photo.jpg" isDir={false} size={32} />)

    expect(container.firstElementChild).toHaveClass('w-10', 'h-10', 'bg-success/10', 'text-success')
    expect(container.querySelector('svg')).toBeInTheDocument()
  })

  it('falls back to the generic file icon and bare color for unknown icon types', () => {
    mockGetFileIcon.mockReturnValue('unknown')

    const { container } = render(
      <FileIcon name="blob.bin" isDir={false} variant="bare" className="custom-icon" />
    )

    const icon = container.querySelector('svg')
    expect(icon).toBeInTheDocument()
    expect(icon).toHaveClass('text-default-500', 'custom-icon')
  })

  it('falls back to generic tile styling for unknown icon types', () => {
    mockGetFileIcon.mockReturnValue('unknown')

    const { container } = render(<FileIcon name="blob.bin" isDir={false} size={20} />)

    expect(container.firstElementChild).toHaveClass('w-7', 'h-7', 'bg-content2', 'text-default-500')
  })
})
