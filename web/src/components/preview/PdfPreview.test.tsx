import type { AnchorHTMLAttributes, ReactNode } from 'react'
import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { authFetch } from '@/api/auth'
import { PdfPreview } from './PdfPreview'

vi.mock('@/api/auth', () => ({
  authFetch: vi.fn(),
}))

vi.mock('@heroui/react', () => ({
  Spinner: ({ size }: { size: string }) => (
    <div role="status" aria-label={`加载中 ${size}`}>Loading...</div>
  ),
  Button: ({ children, ...props }: { children?: ReactNode } & AnchorHTMLAttributes<HTMLAnchorElement>) => (
    <a {...props}>{children}</a>
  ),
}))

describe('PdfPreview', () => {
  const mockAuthFetch = vi.mocked(authFetch)

  beforeAll(() => {
    if (!URL.createObjectURL) {
      URL.createObjectURL = vi.fn(() => 'about:blank#mock-pdf')
    } else {
      vi.spyOn(URL, 'createObjectURL').mockReturnValue('about:blank#mock-pdf')
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
      headers: new Headers({ 'Content-Type': 'application/pdf; charset=binary' }),
      arrayBuffer: () => Promise.resolve(new TextEncoder().encode('%PDF fake').buffer),
    } as Response)
  })

  it('shows a named loading status while fetching the PDF preview', () => {
    mockAuthFetch.mockImplementationOnce(() => new Promise<Response>(() => {}))

    render(<PdfPreview path="/docs/file.pdf" filename="file.pdf" />)

    expect(screen.getByRole('status', { name: '加载 PDF 预览' })).toBeInTheDocument()
  })

  it('loads pdf content through authFetch', async () => {
    render(<PdfPreview path="/docs/file.pdf" filename="file.pdf" />)

    await waitFor(() => {
      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/download/docs/file.pdf', expect.anything())
    })

    expect(await screen.findByLabelText('file.pdf PDF 预览')).toBeInTheDocument()
  })

  it('passes an abort signal and cancels the pending pdf request when unmounted', async () => {
    let signal: AbortSignal | undefined
    mockAuthFetch.mockImplementationOnce((_url, options) => {
      signal = options?.signal
      return new Promise<Response>(() => {})
    })

    const { unmount } = render(<PdfPreview path="/docs/file.pdf" filename="file.pdf" />)

    await waitFor(() => {
      expect(signal).toBeInstanceOf(AbortSignal)
    })

    unmount()

    expect(signal?.aborted).toBe(true)
  })

  it('forces the preview blob to application/pdf', async () => {
    render(<PdfPreview path="/docs/file.pdf" filename="file.pdf" />)

    await screen.findByLabelText('file.pdf PDF 预览')

    const createObjectURL = vi.mocked(URL.createObjectURL)
    const blob = createObjectURL.mock.calls[0]?.[0] as Blob | undefined
    expect(blob).toBeInstanceOf(Blob)
    expect(blob?.type).toBe('application/pdf')
    await expect(blob?.text()).resolves.toBe('%PDF fake')
  })

  it('rejects non-pdf preview responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers({ 'Content-Type': 'text/html; charset=utf-8' }),
      arrayBuffer: () => Promise.resolve(new TextEncoder().encode('<script>alert(1)</script>').buffer),
    } as Response)

    render(<PdfPreview path="/docs/file.pdf" filename="file.pdf" />)

    await waitFor(() => {
      expect(screen.getByText('无法加载 PDF')).toBeInTheDocument()
    })
    expect(URL.createObjectURL).not.toHaveBeenCalled()
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

  it('rejects successful non-PDF JSON responses without reading them as preview errors', async () => {
    const json = vi.fn(() => Promise.resolve({
      code: 'FILESYSTEM_UNAVAILABLE',
      message: 'user JSON document content',
    }))
    const arrayBuffer = vi.fn(() => Promise.resolve(new ArrayBuffer(0)))

    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      clone: () => ({ json }),
      arrayBuffer,
    } as unknown as Response)

    render(<PdfPreview path="/docs/unavailable.pdf" filename="unavailable.pdf" />)

    await waitFor(() => {
      expect(screen.getByText('无法加载 PDF')).toBeInTheDocument()
    })
    expect(json).not.toHaveBeenCalled()
    expect(arrayBuffer).not.toHaveBeenCalled()
    expect(URL.createObjectURL).not.toHaveBeenCalled()
  })

  it('uses a stable message for structured JSON errors from non-OK preview responses', async () => {
    const json = vi.fn(() => Promise.resolve({
      success: false,
      error: {
        code: 'FILESYSTEM_UNAVAILABLE',
        message: 'preview storage unavailable',
      },
    }))

    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      clone: () => ({ json }),
    } as unknown as Response)

    render(<PdfPreview path="/docs/unavailable.pdf" filename="unavailable.pdf" />)

    await waitFor(() => {
      expect(screen.getByText('无法加载 PDF')).toBeInTheDocument()
    })
    expect(json).toHaveBeenCalled()
    expect(screen.queryByText('preview storage unavailable')).not.toBeInTheDocument()
    expect(URL.createObjectURL).not.toHaveBeenCalled()
  })

  it('shows missing-file guidance for structured not-found preview responses', async () => {
    const json = vi.fn(() => Promise.resolve({
      success: false,
      error: {
        code: 'FILE_NOT_FOUND',
        message: 'file not found',
      },
    }))

    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 404,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      clone: () => ({ json }),
    } as unknown as Response)

    render(<PdfPreview path="/docs/missing.pdf" filename="missing.pdf" />)

    await waitFor(() => {
      expect(screen.getByText('该文件可能已被移动或删除，请刷新列表后重试。')).toBeInTheDocument()
    })
    expect(json).toHaveBeenCalled()
    expect(screen.queryByText('file not found')).not.toBeInTheDocument()
    expect(URL.createObjectURL).not.toHaveBeenCalled()
  })

  it('does not expose raw request errors when PDF loading rejects', async () => {
    mockAuthFetch.mockRejectedValueOnce(new Error('preview storage unavailable'))

    render(<PdfPreview path="/docs/unavailable.pdf" filename="unavailable.pdf" />)

    await waitFor(() => {
      expect(screen.getByText('无法加载 PDF')).toBeInTheDocument()
    })
    expect(screen.queryByText('preview storage unavailable')).not.toBeInTheDocument()
  })
})
