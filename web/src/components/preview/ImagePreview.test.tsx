import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { ImagePreview } from './ImagePreview'

// Mock HeroUI components
vi.mock('@heroui/react', () => ({
  Spinner: ({ size }: { size: string }) => (
    <div data-testid="spinner" data-size={size}>Loading...</div>
  ),
  Button: ({ children, onPress, title, ...props }: { children: React.ReactNode; onPress?: () => void; title?: string }) => (
    <button onClick={onPress} title={title} {...props}>{children}</button>
  ),
}))

describe('ImagePreview', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders with loading state', () => {
    render(<ImagePreview path="/image.png" filename="image.png" />)

    expect(screen.getByTestId('spinner')).toBeInTheDocument()
  })

  it('displays filename in toolbar', () => {
    render(<ImagePreview path="/image.png" filename="my-image.png" />)

    expect(screen.getByText('my-image.png')).toBeInTheDocument()
  })

  it('shows zoom controls', () => {
    render(<ImagePreview path="/image.png" filename="image.png" />)

    expect(screen.getByTitle('缩小')).toBeInTheDocument()
    expect(screen.getByTitle('放大')).toBeInTheDocument()
    expect(screen.getByTitle('旋转')).toBeInTheDocument()
    expect(screen.getByTitle('重置')).toBeInTheDocument()
  })

  it('displays initial zoom level as 100%', () => {
    render(<ImagePreview path="/image.png" filename="image.png" />)

    expect(screen.getByText('100%')).toBeInTheDocument()
  })

  it('builds correct image URL', () => {
    render(<ImagePreview path="/documents/photo.jpg" filename="photo.jpg" />)

    const img = screen.getByRole('img')
    expect(img).toHaveAttribute(
      'src',
      '/api/v1/download/documents/photo.jpg'
    )
  })

  it('hides spinner after image loads', async () => {
    render(<ImagePreview path="/image.png" filename="image.png" />)

    const img = screen.getByRole('img')
    fireEvent.load(img)

    await waitFor(() => {
      expect(img).toHaveClass('opacity-100')
    })
  })

  it('shows error message on image load failure', async () => {
    render(<ImagePreview path="/broken.png" filename="broken.png" />)

    const img = screen.getByRole('img')
    fireEvent.error(img)

    await waitFor(() => {
      expect(screen.getByText('无法加载图片')).toBeInTheDocument()
    })
  })

  it('increases zoom on zoom in button click', () => {
    render(<ImagePreview path="/image.png" filename="image.png" />)

    const zoomIn = screen.getByTitle('放大')
    fireEvent.click(zoomIn)

    expect(screen.getByText('125%')).toBeInTheDocument()
  })

  it('decreases zoom on zoom out button click', () => {
    render(<ImagePreview path="/image.png" filename="image.png" />)

    const zoomIn = screen.getByTitle('放大')
    fireEvent.click(zoomIn) // 125%
    
    const zoomOut = screen.getByTitle('缩小')
    fireEvent.click(zoomOut) // Back to 100%

    expect(screen.getByText('100%')).toBeInTheDocument()
  })

  it('resets zoom and rotation on reset button click', () => {
    render(<ImagePreview path="/image.png" filename="image.png" />)

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
    const img = screen.getByRole('img')
    expect(img.style.transform).toContain('rotate(0deg)')
  })

  it('rotates image by 90 degrees on rotate button click', () => {
    render(<ImagePreview path="/image.png" filename="image.png" />)

    const rotate = screen.getByTitle('旋转')
    fireEvent.click(rotate)

    const img = screen.getByRole('img')
    expect(img.style.transform).toContain('rotate(90deg)')
  })

  it('wraps rotation after 360 degrees', () => {
    render(<ImagePreview path="/image.png" filename="image.png" />)

    const rotate = screen.getByTitle('旋转')
    fireEvent.click(rotate) // 90
    fireEvent.click(rotate) // 180
    fireEvent.click(rotate) // 270
    fireEvent.click(rotate) // 0

    const img = screen.getByRole('img')
    expect(img.style.transform).toContain('rotate(0deg)')
  })

  it('limits zoom to maximum 5x', () => {
    render(<ImagePreview path="/image.png" filename="image.png" />)

    const zoomIn = screen.getByTitle('放大')
    // Click many times
    for (let i = 0; i < 20; i++) {
      fireEvent.click(zoomIn)
    }

    expect(screen.getByText('500%')).toBeInTheDocument()
  })

  it('limits zoom to minimum 10%', () => {
    render(<ImagePreview path="/image.png" filename="image.png" />)

    const zoomOut = screen.getByTitle('缩小')
    // Click many times  
    for (let i = 0; i < 20; i++) {
      fireEvent.click(zoomOut)
    }

    expect(screen.getByText('10%')).toBeInTheDocument()
  })

  it('resets state when path changes', () => {
    const { rerender } = render(
      <ImagePreview path="/image1.png" filename="image1.png" />
    )

    // Zoom in
    const zoomIn = screen.getByTitle('放大')
    fireEvent.click(zoomIn)
    expect(screen.getByText('125%')).toBeInTheDocument()

    // Change path
    rerender(<ImagePreview path="/image2.png" filename="image2.png" />)

    expect(screen.getByText('100%')).toBeInTheDocument()
  })

  it('applies custom className', () => {
    const { container } = render(
      <ImagePreview path="/image.png" filename="image.png" className="custom-class" />
    )

    expect(container.querySelector('.custom-class')).toBeInTheDocument()
  })

  it('sets image as non-draggable', () => {
    render(<ImagePreview path="/image.png" filename="image.png" />)

    const img = screen.getByRole('img')
    expect(img).toHaveAttribute('draggable', 'false')
  })
})
