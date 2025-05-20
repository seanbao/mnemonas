import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import { authFetch } from '@/api/auth'
import { TextPreview } from './TextPreview'

vi.mock('@/api/auth', () => ({
  authFetch: vi.fn(),
}))

// Mock HeroUI Spinner
vi.mock('@heroui/react', () => ({
  Spinner: ({ size }: { size: string }) => (
    <div data-testid="spinner" data-size={size}>Loading...</div>
  ),
}))

describe('TextPreview', () => {
  const mockAuthFetch = vi.mocked(authFetch)

  function createDeferred<T>() {
    let resolve!: (value: T) => void
    const promise = new Promise<T>((res) => {
      resolve = res
    })
    return { promise, resolve }
  }

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows loading state initially', () => {
    mockAuthFetch.mockImplementation(
      () => new Promise(() => {})
    )

    render(<TextPreview path="/test.txt" filename="test.txt" />)

    expect(screen.getByTestId('spinner')).toBeInTheDocument()
    expect(screen.getByText('加载文件内容...')).toBeInTheDocument()
  })

  it('displays text content after loading', async () => {
    const mockContent = 'Hello, World!'
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers({ 'content-length': '13' }),
      text: () => Promise.resolve(mockContent),
    } as Response)

    render(<TextPreview path="/test.txt" filename="test.txt" />)

    await waitFor(() => {
      expect(screen.getByText('test.txt')).toBeInTheDocument()
    })
  })

  it('shows error when fetch fails', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      statusText: 'Not Found',
    } as Response)

    render(<TextPreview path="/test.txt" filename="test.txt" />)

    await waitFor(() => {
      expect(screen.getByText(/加载失败/)).toBeInTheDocument()
    })
  })

  it('shows error for large files', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers({ 'content-length': '2000000' }), // 2MB
      text: () => Promise.resolve('content'),
    } as Response)

    render(<TextPreview path="/large.txt" filename="large.txt" />)

    await waitFor(() => {
      expect(screen.getByText('文件过大，无法预览')).toBeInTheDocument()
    })
  })

  it('builds correct preview URL', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers(),
      text: () => Promise.resolve('content'),
    } as Response)

    render(<TextPreview path="/documents/file.txt" filename="file.txt" />)

    await waitFor(() => {
      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/download/documents/file.txt')
    })
  })

  it('shows line numbers', async () => {
    const mockContent = 'line1\nline2\nline3'
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers(),
      text: () => Promise.resolve(mockContent),
    } as Response)

    render(<TextPreview path="/test.txt" filename="test.txt" />)

    await waitFor(() => {
      expect(screen.getByText('1')).toBeInTheDocument()
      expect(screen.getByText('2')).toBeInTheDocument()
      expect(screen.getByText('3')).toBeInTheDocument()
    })
  })

  it('displays language info for TypeScript files', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers(),
      text: () => Promise.resolve('const x = 1'),
    } as Response)

    render(<TextPreview path="/app.tsx" filename="app.tsx" />)

    await waitFor(() => {
      expect(screen.getByText(/TSX/)).toBeInTheDocument()
    })
  })

  it('applies custom className', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers(),
      text: () => Promise.resolve('content'),
    } as Response)

    const { container } = render(
      <TextPreview path="/test.txt" filename="test.txt" className="custom-class" />
    )

    await waitFor(() => {
      expect(container.querySelector('.custom-class')).toBeInTheDocument()
    })
  })

  it('surfaces unauthorized preview failures after auth retry is exhausted', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 401,
      statusText: 'Unauthorized',
    } as Response)

    render(<TextPreview path="/private.txt" filename="private.txt" />)

    await waitFor(() => {
      expect(screen.getByText('加载失败: Unauthorized')).toBeInTheDocument()
    })
  })

  it('ignores stale content when the preview path changes during loading', async () => {
    const firstLoad = createDeferred<string>()

    mockAuthFetch
      .mockResolvedValueOnce({
        ok: true,
        headers: new Headers(),
        text: () => firstLoad.promise,
      } as Response)
      .mockResolvedValueOnce({
        ok: true,
        headers: new Headers(),
        text: () => Promise.resolve('second content'),
      } as Response)

    const { rerender } = render(<TextPreview path="/first.txt" filename="first.txt" />)
    rerender(<TextPreview path="/second.txt" filename="second.txt" />)

    await waitFor(() => {
      expect(screen.getByText('second content')).toBeInTheDocument()
    })

    await act(async () => {
      firstLoad.resolve('first content')
      await firstLoad.promise
    })

    expect(screen.getByText('second content')).toBeInTheDocument()
    expect(screen.queryByText('first content')).not.toBeInTheDocument()
  })

})
