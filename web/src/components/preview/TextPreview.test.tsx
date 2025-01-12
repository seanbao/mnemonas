import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { TextPreview } from './TextPreview'

// Mock HeroUI Spinner
vi.mock('@heroui/react', () => ({
  Spinner: ({ size }: { size: string }) => (
    <div data-testid="spinner" data-size={size}>Loading...</div>
  ),
}))

describe('TextPreview', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    global.fetch = vi.fn()
  })

  it('shows loading state initially', () => {
    ;(global.fetch as ReturnType<typeof vi.fn>).mockImplementation(
      () => new Promise(() => {})
    )

    render(<TextPreview path="/test.txt" filename="test.txt" />)

    expect(screen.getByTestId('spinner')).toBeInTheDocument()
    expect(screen.getByText('加载文件内容...')).toBeInTheDocument()
  })

  it('displays text content after loading', async () => {
    const mockContent = 'Hello, World!'
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: true,
      headers: new Headers({ 'content-length': '13' }),
      text: () => Promise.resolve(mockContent),
    })

    render(<TextPreview path="/test.txt" filename="test.txt" />)

    await waitFor(() => {
      expect(screen.getByText('test.txt')).toBeInTheDocument()
    })
  })

  it('shows error when fetch fails', async () => {
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: false,
      statusText: 'Not Found',
    })

    render(<TextPreview path="/test.txt" filename="test.txt" />)

    await waitFor(() => {
      expect(screen.getByText(/加载失败/)).toBeInTheDocument()
    })
  })

  it('shows error for large files', async () => {
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: true,
      headers: new Headers({ 'content-length': '2000000' }), // 2MB
      text: () => Promise.resolve('content'),
    })

    render(<TextPreview path="/large.txt" filename="large.txt" />)

    await waitFor(() => {
      expect(screen.getByText('文件过大，无法预览')).toBeInTheDocument()
    })
  })

  it('builds correct preview URL', async () => {
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: true,
      headers: new Headers(),
      text: () => Promise.resolve('content'),
    })

    render(<TextPreview path="/documents/file.txt" filename="file.txt" />)

    await waitFor(() => {
      expect(global.fetch).toHaveBeenCalledWith(
        '/api/v1/download/documents/file.txt',
        { credentials: 'include' }
      )
    })
  })

  it('shows line numbers', async () => {
    const mockContent = 'line1\nline2\nline3'
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: true,
      headers: new Headers(),
      text: () => Promise.resolve(mockContent),
    })

    render(<TextPreview path="/test.txt" filename="test.txt" />)

    await waitFor(() => {
      expect(screen.getByText('1')).toBeInTheDocument()
      expect(screen.getByText('2')).toBeInTheDocument()
      expect(screen.getByText('3')).toBeInTheDocument()
    })
  })

  it('displays language info for TypeScript files', async () => {
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: true,
      headers: new Headers(),
      text: () => Promise.resolve('const x = 1'),
    })

    render(<TextPreview path="/app.tsx" filename="app.tsx" />)

    await waitFor(() => {
      expect(screen.getByText(/TSX/)).toBeInTheDocument()
    })
  })

  it('applies custom className', async () => {
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: true,
      headers: new Headers(),
      text: () => Promise.resolve('content'),
    })

    const { container } = render(
      <TextPreview path="/test.txt" filename="test.txt" className="custom-class" />
    )

    await waitFor(() => {
      expect(container.querySelector('.custom-class')).toBeInTheDocument()
    })
  })
})
