import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { authFetch } from '@/api/auth'
import { ImagePreview } from './ImagePreview'

vi.mock('@/api/auth', () => ({
  authFetch: vi.fn(),
}))

// Mock HeroUI components
vi.mock('@heroui/react', () => ({
  Spinner: ({ size }: { size: string }) => (
    <div role="status" aria-label={`加载中 ${size}`}>Loading...</div>
  ),
  Button: ({ children, onPress, title, 'aria-label': ariaLabel }: { children: React.ReactNode; onPress?: () => void; title?: string; 'aria-label'?: string }) => (
    <button onClick={onPress} title={title} aria-label={ariaLabel}>{children}</button>
  ),
}))

describe('ImagePreview', () => {
  const mockAuthFetch = vi.mocked(authFetch)

  const renderImage = async (path: string, filename: string, className?: string) => {
    let view: ReturnType<typeof render> | null = null
    await act(async () => {
      view = render(<ImagePreview path={path} filename={filename} className={className} />)
      await Promise.resolve()
    })
    return view
  }
  beforeAll(() => {
    if (!URL.createObjectURL) {
      URL.createObjectURL = vi.fn(() => 'blob:mock-image')
    } else {
      vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:mock-image')
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
      blob: () => Promise.resolve(new Blob(['fake'], { type: 'image/png' })),
    } as Response)
  })

  it('renders with loading state', async () => {
    await renderImage('/image.png', 'image.png')

    expect(screen.getByRole('status', { name: '加载图片预览' })).toBeInTheDocument()
  })

  it('displays filename in toolbar', async () => {
    await renderImage('/image.png', 'my-image.png')

    expect(screen.getByText('my-image.png')).toBeInTheDocument()
  })

  it('shows zoom controls', async () => {
    await renderImage('/image.png', 'image.png')

    expect(screen.getByRole('button', { name: '缩小' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '放大' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '旋转' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重置' })).toBeInTheDocument()
  })

  it('displays initial zoom level as 100%', async () => {
    await renderImage('/image.png', 'image.png')

    expect(screen.getByText('100%')).toBeInTheDocument()
  })

  it('builds correct image URL', async () => {
    await renderImage('/documents/photo.jpg', 'photo.jpg')

    await waitFor(() => {
      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/download/documents/photo.jpg', expect.anything())
    })
  })

  it('passes an abort signal and cancels the pending image request when the path changes', async () => {
    let firstSignal: AbortSignal | undefined
    const firstRequest = new Promise<Response>(() => {})
    mockAuthFetch
      .mockImplementationOnce((_url, options) => {
        firstSignal = options?.signal
        return firstRequest
      })
      .mockResolvedValueOnce({
        ok: true,
        blob: () => Promise.resolve(new Blob(['second'], { type: 'image/png' })),
      } as Response)

    const { rerender } = render(<ImagePreview path="/first.png" filename="first.png" />)

    await waitFor(() => {
      expect(firstSignal).toBeInstanceOf(AbortSignal)
    })

    rerender(<ImagePreview path="/second.png" filename="second.png" />)

    await waitFor(() => {
      expect(firstSignal?.aborted).toBe(true)
    })
  })

  it('shows error when authenticated preview request fails', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 401,
      statusText: 'Unauthorized',
    } as Response)

    await renderImage('/private.png', 'private.png')

    await waitFor(() => {
      expect(screen.getByText('无法加载图片')).toBeInTheDocument()
    })
  })

  it('rejects successful non-image JSON responses without reading them as preview errors', async () => {
    const json = vi.fn(() => Promise.resolve({
      success: false,
      error: {
        code: 'FILESYSTEM_UNAVAILABLE',
        message: 'user JSON document content',
      },
    }))
    const blob = vi.fn(() => Promise.resolve(new Blob(['{}'], { type: 'application/json' })))

    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      clone: () => ({ json }),
      blob,
    } as unknown as Response)

    await renderImage('/unavailable.png', 'unavailable.png')

    await waitFor(() => {
      expect(screen.getByText('无法加载图片')).toBeInTheDocument()
    })
    expect(json).not.toHaveBeenCalled()
    expect(blob).not.toHaveBeenCalled()
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

    await renderImage('/unavailable.png', 'unavailable.png')

    await waitFor(() => {
      expect(screen.getByText('无法加载图片')).toBeInTheDocument()
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

    await renderImage('/missing.png', 'missing.png')

    await waitFor(() => {
      expect(screen.getByText('该文件可能已被移动或删除，请刷新列表后重试。')).toBeInTheDocument()
    })
    expect(json).toHaveBeenCalled()
    expect(screen.queryByText('file not found')).not.toBeInTheDocument()
    expect(URL.createObjectURL).not.toHaveBeenCalled()
  })

  it('does not expose raw request errors when image loading rejects', async () => {
    mockAuthFetch.mockRejectedValueOnce(new Error('preview storage unavailable'))

    await renderImage('/unavailable.png', 'unavailable.png')

    await waitFor(() => {
      expect(screen.getByText('无法加载图片')).toBeInTheDocument()
    })
    expect(screen.queryByText('preview storage unavailable')).not.toBeInTheDocument()
  })

  it('hides spinner after image loads', async () => {
    await renderImage('/image.png', 'image.png')

    const img = await screen.findByRole('img')
    fireEvent.load(img)

    await waitFor(() => {
      expect(img).toHaveClass('opacity-100')
    })
  })

  it('shows error message on image load failure', async () => {
    await renderImage('/broken.png', 'broken.png')

    const img = await screen.findByRole('img')
    fireEvent.error(img)

    await waitFor(() => {
      expect(screen.getByText('无法加载图片')).toBeInTheDocument()
    })
  })

  it('increases zoom on zoom in button click', async () => {
    await renderImage('/image.png', 'image.png')

    const zoomIn = screen.getByRole('button', { name: '放大' })
    fireEvent.click(zoomIn)

    expect(screen.getByText('125%')).toBeInTheDocument()
  })

  it('decreases zoom on zoom out button click', async () => {
    await renderImage('/image.png', 'image.png')

    const zoomIn = screen.getByRole('button', { name: '放大' })
    fireEvent.click(zoomIn) // 125%
    
    const zoomOut = screen.getByRole('button', { name: '缩小' })
    fireEvent.click(zoomOut) // Back to 100%

    expect(screen.getByText('100%')).toBeInTheDocument()
  })

  it('resets zoom and rotation on reset button click', async () => {
    await renderImage('/image.png', 'image.png')

    // Zoom in
    const zoomIn = screen.getByRole('button', { name: '放大' })
    fireEvent.click(zoomIn)
    fireEvent.click(zoomIn)
    
    // Rotate
    const rotate = screen.getByRole('button', { name: '旋转' })
    fireEvent.click(rotate)
    
    // Reset
    const reset = screen.getByRole('button', { name: '重置' })
    fireEvent.click(reset)

    expect(screen.getByText('100%')).toBeInTheDocument()
    const img = await screen.findByRole('img')
    expect(img.style.transform).toContain('rotate(0deg)')
  })

  it('rotates image by 90 degrees on rotate button click', async () => {
    await renderImage('/image.png', 'image.png')

    const rotate = screen.getByRole('button', { name: '旋转' })
    fireEvent.click(rotate)

    const img = await screen.findByRole('img')
    expect(img.style.transform).toContain('rotate(90deg)')
  })

  it('wraps rotation after 360 degrees', async () => {
    await renderImage('/image.png', 'image.png')

    const rotate = screen.getByRole('button', { name: '旋转' })
    fireEvent.click(rotate) // 90
    fireEvent.click(rotate) // 180
    fireEvent.click(rotate) // 270
    fireEvent.click(rotate) // 0

    const img = await screen.findByRole('img')
    expect(img.style.transform).toContain('rotate(0deg)')
  })

  it('limits zoom to maximum 5x', async () => {
    await renderImage('/image.png', 'image.png')

    const zoomIn = screen.getByRole('button', { name: '放大' })
    // Click many times
    for (let i = 0; i < 20; i++) {
      fireEvent.click(zoomIn)
    }

    expect(screen.getByText('500%')).toBeInTheDocument()
  })

  it('limits zoom to minimum 10%', async () => {
    await renderImage('/image.png', 'image.png')

    const zoomOut = screen.getByRole('button', { name: '缩小' })
    // Click many times  
    for (let i = 0; i < 20; i++) {
      fireEvent.click(zoomOut)
    }

    expect(screen.getByText('10%')).toBeInTheDocument()
  })

  it('supports wheel zoom in both directions', async () => {
    await renderImage('/image.png', 'image.png')

    await screen.findByRole('img')
    const surface = screen.getByRole('region', { name: 'image.png 图片预览画布' })

    fireEvent.wheel(surface, { deltaY: -100 })
    expect(screen.getByText('110%')).toBeInTheDocument()

    fireEvent.wheel(surface, { deltaY: 100 })
    expect(screen.getByText('100%')).toBeInTheDocument()
  })

  it('drags the image when zoomed in', async () => {
    await renderImage('/image.png', 'image.png')

    fireEvent.click(screen.getByRole('button', { name: '放大' }))
    const img = await screen.findByRole('img')
    const surface = screen.getByRole('region', { name: 'image.png 图片预览画布' })

    fireEvent.mouseDown(surface, { clientX: 10, clientY: 20 })
    fireEvent.mouseMove(surface, { clientX: 30, clientY: 55 })
    fireEvent.mouseUp(surface)

    expect(img.style.transform).toContain('translate(20px, 35px)')
  })

  it('ignores drag gestures while the image is not zoomed', async () => {
    await renderImage('/image.png', 'image.png')

    const img = await screen.findByRole('img')
    const surface = screen.getByRole('region', { name: 'image.png 图片预览画布' })

    fireEvent.mouseDown(surface, { clientX: 10, clientY: 20 })
    fireEvent.mouseMove(surface, { clientX: 30, clientY: 55 })
    fireEvent.mouseUp(surface)

    expect(img.style.transform).toContain('translate(0px, 0px)')
  })

  it('revokes blob URLs when unmounted', async () => {
    const view = await renderImage('/image.png', 'image.png')

    await waitFor(() => {
      expect(URL.createObjectURL).toHaveBeenCalled()
    })

    view?.unmount()

    expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1)
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:mock-image')
  })

  it('revokes the previous blob URL once when the path changes', async () => {
    vi.mocked(URL.createObjectURL)
      .mockReturnValueOnce('blob:first-image')
      .mockReturnValueOnce('blob:second-image')

    const view = await renderImage('/image1.png', 'image1.png')
    const { rerender } = view!

    await waitFor(() => {
      expect(URL.createObjectURL).toHaveBeenCalledTimes(1)
    })

    await act(async () => {
      rerender(<ImagePreview path="/image2.png" filename="image2.png" />)
      await Promise.resolve()
    })

    await waitFor(() => {
      expect(URL.createObjectURL).toHaveBeenCalledTimes(2)
    })

    expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1)
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:first-image')
    expect(await screen.findByRole('img')).toHaveAttribute('src', 'blob:second-image')
  })

  it('resets state when path changes', async () => {
    const view = await renderImage('/image1.png', 'image1.png')
    const { rerender } = view!

    // Zoom in
    const zoomIn = screen.getByRole('button', { name: '放大' })
    fireEvent.click(zoomIn)
    expect(screen.getByText('125%')).toBeInTheDocument()

    // Change path
    await act(async () => {
      rerender(<ImagePreview path="/image2.png" filename="image2.png" />)
      await Promise.resolve()
    })

    expect(screen.getByText('100%')).toBeInTheDocument()
  })

  it('applies custom className', async () => {
    await renderImage('/image.png', 'image.png', 'custom-class')

    expect(screen.getByRole('region', { name: 'image.png 图片预览' })).toHaveClass('custom-class')
  })

  it('sets image as non-draggable', async () => {
    await renderImage('/image.png', 'image.png')

    const img = await screen.findByRole('img')
    expect(img).toHaveAttribute('draggable', 'false')
  })
})
