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
    <div role="status" aria-label={`加载中 ${size}`}>Loading...</div>
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

    expect(screen.getByRole('status', { name: '加载文本预览' })).toBeInTheDocument()
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

  it('reads preview content from streaming responses', async () => {
    const encoder = new TextEncoder()
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers(),
      body: new ReadableStream({
        start(controller) {
          controller.enqueue(encoder.encode('streamed '))
          controller.enqueue(encoder.encode('content'))
          controller.close()
        },
      }),
    } as Response)

    render(<TextPreview path="/stream.txt" filename="stream.txt" />)

    await waitFor(() => {
      expect(screen.getByText('streamed content')).toBeInTheDocument()
    })
  })

  it('cancels streaming reads when the preview exceeds the byte limit', async () => {
    let cancelled = false
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers(),
      body: new ReadableStream({
        start(controller) {
          controller.enqueue(new Uint8Array(1024 * 1024 + 1))
        },
        cancel() {
          cancelled = true
          return Promise.reject(new Error('cancel failed'))
        },
      }),
    } as Response)

    render(<TextPreview path="/large-stream.txt" filename="large-stream.txt" />)

    await waitFor(() => {
      expect(screen.getByText('文件过大，无法预览')).toBeInTheDocument()
      expect(cancelled).toBe(true)
    })
  })

  it('renders preview content as inert text instead of injecting HTML', async () => {
    const mockContent = '<img src=x onerror=alert(1)>\nconst value = "500"'
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers(),
      text: () => Promise.resolve(mockContent),
    } as Response)

    render(<TextPreview path="/unsafe.ts" filename="unsafe.ts" />)

    await waitFor(() => {
      expect(screen.getByRole('region', { name: 'unsafe.ts 文本预览' })).toHaveTextContent('<img src=x onerror=alert(1)>')
    })
    expect(screen.queryByRole('img')).toBeNull()
  })

  it('shows error when fetch fails', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      statusText: 'Not Found',
    } as Response)

    render(<TextPreview path="/test.txt" filename="test.txt" />)

    await waitFor(() => {
      expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeInTheDocument()
    })
    expect(screen.queryByText('加载失败：Not Found')).not.toBeInTheDocument()
  })

  it('shows missing-file guidance when the preview target no longer exists', async () => {
    mockAuthFetch.mockResolvedValueOnce(new Response(JSON.stringify({
      success: false,
      error: {
        code: 'FILE_NOT_FOUND',
        message: 'file not found',
      },
    }), {
      status: 404,
      headers: { 'Content-Type': 'application/json' },
    }))

    render(<TextPreview path="/missing.txt" filename="missing.txt" />)

    await waitFor(() => {
      expect(screen.getByText('该文件可能已被移动或删除，请刷新列表后重试。')).toBeInTheDocument()
    })
  })

  it('renders successful JSON files even when they resemble API error envelopes', async () => {
    const content = '{\n  "success": false,\n  "error": {\n    "message": "draft import failed"\n  }\n}'
    const json = vi.fn(() => Promise.resolve({
      success: false,
      error: {
        code: 'DRAFT_IMPORT_FAILED',
        message: 'draft import failed',
      },
    }))
    const text = vi.fn(() => Promise.resolve(content))

    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      clone: () => ({ json }),
      text,
    } as unknown as Response)

    render(<TextPreview path="/unavailable.json" filename="unavailable.json" />)

    await waitFor(() => {
      expect(screen.getByText('"success"')).toBeInTheDocument()
      expect(screen.getByText('"error"')).toBeInTheDocument()
      expect(screen.getByText('"message"')).toBeInTheDocument()
      expect(screen.getByText('"draft import failed"')).toBeInTheDocument()
    })
    expect(json).not.toHaveBeenCalled()
    expect(text).toHaveBeenCalled()
  })

  it('renders ordinary JSON files that contain success false without error details', async () => {
    const content = '{\n  "success": false\n}'
    const json = vi.fn(() => Promise.resolve({ success: false }))
    const text = vi.fn(() => Promise.resolve(content))

    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      clone: () => ({ json }),
      text,
    } as unknown as Response)

    render(<TextPreview path="/payload.json" filename="payload.json" />)

    await waitFor(() => {
      expect(screen.getByText('"success"')).toBeInTheDocument()
      expect(screen.getByText('false')).toBeInTheDocument()
    })
    expect(json).not.toHaveBeenCalled()
    expect(text).toHaveBeenCalled()
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
      statusText: 'Service Unavailable',
      headers: new Headers({ 'Content-Type': 'application/json' }),
      clone: () => ({ json }),
    } as unknown as Response)

    render(<TextPreview path="/unavailable.txt" filename="unavailable.txt" />)

    await waitFor(() => {
      expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeInTheDocument()
    })
    expect(json).toHaveBeenCalled()
    expect(screen.queryByText('preview storage unavailable')).not.toBeInTheDocument()
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

  it('rejects large files even when content-length is missing', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers(),
      text: () => Promise.resolve('x'.repeat(1024 * 1024 + 1)),
    } as Response)

    render(<TextPreview path="/large-without-length.txt" filename="large-without-length.txt" />)

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
      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/download/documents/file.txt', expect.anything())
    })
  })

  it('passes an abort signal and cancels the pending text request when unmounted', async () => {
    let signal: AbortSignal | undefined
    mockAuthFetch.mockImplementationOnce((_url, options) => {
      signal = options?.signal
      return new Promise<Response>(() => {})
    })

    const { unmount } = render(<TextPreview path="/test.txt" filename="test.txt" />)

    await waitFor(() => {
      expect(signal).toBeInstanceOf(AbortSignal)
    })

    unmount()

    expect(signal?.aborted).toBe(true)
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

  it('highlights hash comments for scripting languages', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers(),
      text: () => Promise.resolve('# comment\nvalue = 42'),
    } as Response)

    render(<TextPreview path="/script.py" filename="script.py" />)

    await waitFor(() => {
      expect(screen.getByText('# comment')).toHaveClass('text-default-400')
      expect(screen.getByText('42')).toHaveClass('text-amber-500')
    })
  })

  it('applies custom className', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers(),
      text: () => Promise.resolve('content'),
    } as Response)

    render(
      <TextPreview path="/test.txt" filename="test.txt" className="custom-class" />
    )

    await waitFor(() => {
      expect(screen.getByRole('region', { name: 'test.txt 文本预览' })).toHaveClass('custom-class')
    })
  })

  it('uses a stable message after auth retry is exhausted', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 401,
      statusText: 'Unauthorized',
    } as Response)

    render(<TextPreview path="/private.txt" filename="private.txt" />)

    await waitFor(() => {
      expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeInTheDocument()
    })
    expect(screen.queryByText('加载失败：Unauthorized')).not.toBeInTheDocument()
  })

  it('uses a stable message when the text preview request rejects', async () => {
    mockAuthFetch.mockRejectedValueOnce(new Error('preview request failed'))

    render(<TextPreview path="/private.txt" filename="private.txt" />)

    await waitFor(() => {
      expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeInTheDocument()
    })
    expect(screen.queryByText('preview request failed')).not.toBeInTheDocument()
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
