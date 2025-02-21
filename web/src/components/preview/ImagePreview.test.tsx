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
    <div data-testid="spinner" data-size={size}>Loading...</div>
  ),
  Button: ({ children, onPress, title }: { children: React.ReactNode; onPress?: () => void; title?: string }) => (
    <button onClick={onPress} title={title}>{children}</button>
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

    expect(screen.getByTestId('spinner')).toBeInTheDocument()
  })

  it('displays filename in toolbar', async () => {
    await renderImage('/image.png', 'my-image.png')

    expect(screen.getByText('my-image.png')).toBeInTheDocument()
  })

  it('shows zoom controls', async () => {
    await renderImage('/image.png', 'image.png')

    expect(screen.getByTitle('缩小')).toBeInTheDocument()
    expect(screen.getByTitle('放大')).toBeInTheDocument()
    expect(screen.getByTitle('旋转')).toBeInTheDocument()
    expect(screen.getByTitle('重置')).toBeInTheDocument()
  })

  it('displays initial zoom level as 100%', async () => {
    await renderImage('/image.png', 'image.png')

    expect(screen.getByText('100%')).toBeInTheDocument()
  })

  it('builds correct image URL', async () => {
    await renderImage('/documents/photo.jpg', 'photo.jpg')

    await waitFor(() => {
      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/download/documents/photo.jpg')
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

    const zoomIn = screen.getByTitle('放大')
    fireEvent.click(zoomIn)

    expect(screen.getByText('125%')).toBeInTheDocument()
  })

  it('decreases zoom on zoom out button click', async () => {
    await renderImage('/image.png', 'image.png')

    const zoomIn = screen.getByTitle('放大')
    fireEvent.click(zoomIn) // 125%
    
    const zoomOut = screen.getByTitle('缩小')
    fireEvent.click(zoomOut) // Back to 100%

    expect(screen.getByText('100%')).toBeInTheDocument()
  })

  it('resets zoom and rotation on reset button click', async () => {
    await renderImage('/image.png', 'image.png')

    // Zoom in
    const zoomIn = screen.getByTitle('放大')
    fireEvent.click(zoomIn)
    fireEvent.click(zoomIn)
    
    // Rotate
    const rotate = screen.getByTitle('旋转')
    fireEvent.click(rotate)
    
    // Reset
    const reset = screen.getByTitle('重置')
    fireEvent.click(reset)

    expect(screen.getByText('100%')).toBeInTheDocument()
    const img = await screen.findByRole('img')
    expect(img.style.transform).toContain('rotate(0deg)')
  })

  it('rotates image by 90 degrees on rotate button click', async () => {
    await renderImage('/image.png', 'image.png')

    const rotate = screen.getByTitle('旋转')
    fireEvent.click(rotate)

    const img = await screen.findByRole('img')
    expect(img.style.transform).toContain('rotate(90deg)')
  })

  it('wraps rotation after 360 degrees', async () => {
    await renderImage('/image.png', 'image.png')

    const rotate = screen.getByTitle('旋转')
    fireEvent.click(rotate) // 90
    fireEvent.click(rotate) // 180
    fireEvent.click(rotate) // 270
    fireEvent.click(rotate) // 0

    const img = await screen.findByRole('img')
    expect(img.style.transform).toContain('rotate(0deg)')
  })

  it('limits zoom to maximum 5x', async () => {
    await renderImage('/image.png', 'image.png')

    const zoomIn = screen.getByTitle('放大')
    // Click many times
    for (let i = 0; i < 20; i++) {
      fireEvent.click(zoomIn)
    }

    expect(screen.getByText('500%')).toBeInTheDocument()
  })

  it('limits zoom to minimum 10%', async () => {
    await renderImage('/image.png', 'image.png')

    const zoomOut = screen.getByTitle('缩小')
    // Click many times  
    for (let i = 0; i < 20; i++) {
      fireEvent.click(zoomOut)
    }

    expect(screen.getByText('10%')).toBeInTheDocument()
  })

  it('resets state when path changes', async () => {
    const view = await renderImage('/image1.png', 'image1.png')
    const { rerender } = view!

    // Zoom in
    const zoomIn = screen.getByTitle('放大')
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
    const view = await renderImage('/image.png', 'image.png', 'custom-class')

    expect(view?.container.querySelector('.custom-class')).toBeInTheDocument()
  })

  it('sets image as non-draggable', async () => {
    await renderImage('/image.png', 'image.png')

    const img = await screen.findByRole('img')
    expect(img).toHaveAttribute('draggable', 'false')
  })
})
