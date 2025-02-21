import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { authFetch } from '@/api/auth'
import { PdfPreview } from './PdfPreview'

vi.mock('@/api/auth', () => ({
  authFetch: vi.fn(),
}))

vi.mock('@heroui/react', () => ({
  Spinner: ({ size }: { size: string }) => (
    <div data-testid="spinner" data-size={size}>Loading...</div>
  ),
  Button: ({ children, ...props }: any) => (
    <a {...props}>{children}</a>
  ),
}))

describe('PdfPreview', () => {
  const mockAuthFetch = vi.mocked(authFetch)

  beforeAll(() => {
    if (!URL.createObjectURL) {
      URL.createObjectURL = vi.fn(() => 'blob:mock-pdf')
    } else {
      vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:mock-pdf')
    }
    if (!URL.revokeObjectURL) {
      URL.revokeObjectURL = vi.fn()
    } else {
      vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
    }
  })

  beforeEach(() => {
    vi.clearAllMocks()
    mockAuthFetch.mockResolvedValue({
      ok: true,
      blob: () => Promise.resolve(new Blob(['fake'], { type: 'application/pdf' })),
    } as Response)
  })

  it('loads pdf content through authFetch', async () => {
    render(<PdfPreview path="/docs/file.pdf" filename="file.pdf" />)

    await waitFor(() => {
      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/download/docs/file.pdf')
    })

    expect(await screen.findByTitle('file.pdf')).toBeInTheDocument()
  })

  it('shows an error when the authenticated preview request fails', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 401,
      statusText: 'Unauthorized',
    } as Response)

    render(<PdfPreview path="/docs/private.pdf" filename="private.pdf" />)

    await waitFor(() => {
      expect(screen.getByText('无法加载 PDF')).toBeInTheDocument()
    })
  })
})