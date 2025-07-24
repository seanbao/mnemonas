import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import {
  ContextMenu,
  ContextMenuSection,
  ContextMenuItem,
  ContextMenuDivider,
} from './ContextMenu'

describe('ContextMenu', () => {
  it('renders nothing when closed', () => {
    render(
      <ContextMenu isOpen={false} position={{ x: 100, y: 100 }} onClose={() => {}}>
        <ContextMenuItem>Test</ContextMenuItem>
      </ContextMenu>
    )

    expect(screen.queryByText('Test')).not.toBeInTheDocument()
  })

  it('renders children when open', () => {
    render(
      <ContextMenu isOpen={true} position={{ x: 100, y: 100 }} onClose={() => {}}>
        <ContextMenuItem>Test Action</ContextMenuItem>
      </ContextMenu>
    )

    expect(screen.getByText('Test Action')).toBeInTheDocument()
  })

  it('positions at specified coordinates', () => {
    render(
      <ContextMenu isOpen={true} position={{ x: 200, y: 300 }} onClose={() => {}}>
        <ContextMenuItem>Test</ContextMenuItem>
      </ContextMenu>
    )

    const menu = document.querySelector('[data-context-menu]')
    expect(menu).toHaveStyle({ left: '200px', top: '300px' })
  })

  it('keeps the menu inside the viewport near the lower right edge', () => {
    const originalInnerWidth = window.innerWidth
    const originalInnerHeight = window.innerHeight
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 180 })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 160 })

    render(
      <ContextMenu isOpen={true} position={{ x: 170, y: 150 }} onClose={() => {}}>
        <ContextMenuItem>Test</ContextMenuItem>
      </ContextMenu>
    )

    const menu = document.querySelector('[data-context-menu]')
    expect(menu).toHaveStyle({ left: '72px', top: '52px' })

    Object.defineProperty(window, 'innerWidth', { configurable: true, value: originalInnerWidth })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: originalInnerHeight })
  })

  it('renders via portal to body', () => {
    render(
      <div id="container">
        <ContextMenu isOpen={true} position={{ x: 100, y: 100 }} onClose={() => {}}>
          <ContextMenuItem>Test</ContextMenuItem>
        </ContextMenu>
      </div>
    )

    const container = document.getElementById('container')
    const menu = document.querySelector('[data-context-menu]')
    
    // Menu should be child of body, not container
    expect(menu?.parentElement).toBe(document.body)
    expect(container?.querySelector('[data-context-menu]')).toBeNull()
  })

  it('applies custom className', () => {
    render(
      <ContextMenu
        isOpen={true}
        position={{ x: 100, y: 100 }}
        onClose={() => {}}
        className="custom-menu-class"
      >
        <ContextMenuItem>Test</ContextMenuItem>
      </ContextMenu>
    )

    const menu = document.querySelector('[data-context-menu]')
    expect(menu).toHaveClass('custom-menu-class')
  })
})

describe('ContextMenuSection', () => {
  it('renders children', () => {
    render(
      <ContextMenuSection>
        <ContextMenuItem>Item 1</ContextMenuItem>
        <ContextMenuItem>Item 2</ContextMenuItem>
      </ContextMenuSection>
    )

    expect(screen.getByText('Item 1')).toBeInTheDocument()
    expect(screen.getByText('Item 2')).toBeInTheDocument()
  })

  it('renders title when provided', () => {
    render(
      <ContextMenuSection title="Section Title">
        <ContextMenuItem>Item</ContextMenuItem>
      </ContextMenuSection>
    )

    expect(screen.getByText('Section Title')).toBeInTheDocument()
  })

  it('does not render title when not provided', () => {
    render(
      <ContextMenuSection>
        <ContextMenuItem>Item</ContextMenuItem>
      </ContextMenuSection>
    )

    // No additional text besides item
    expect(screen.getByRole('button')).toBeInTheDocument()
    expect(screen.queryByText(/Section/)).not.toBeInTheDocument()
  })

  it('shows divider when showDivider is true', () => {
    const { container } = render(
      <ContextMenuSection showDivider>
        <ContextMenuItem>Item</ContextMenuItem>
      </ContextMenuSection>
    )

    expect(container.firstChild).toHaveClass('border-b')
  })
})

describe('ContextMenuItem', () => {
  it('renders children', () => {
    render(<ContextMenuItem>Action Text</ContextMenuItem>)

    expect(screen.getByText('Action Text')).toBeInTheDocument()
  })

  it('renders as a button', () => {
    render(<ContextMenuItem>Action</ContextMenuItem>)

    expect(screen.getByRole('button')).toBeInTheDocument()
  })

  it('calls onClick when clicked', () => {
    const onClick = vi.fn()
    render(<ContextMenuItem onClick={onClick}>Click Me</ContextMenuItem>)

    fireEvent.click(screen.getByRole('button'))

    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('does not call onClick when disabled', () => {
    const onClick = vi.fn()
    render(
      <ContextMenuItem onClick={onClick} disabled>
        Disabled
      </ContextMenuItem>
    )

    fireEvent.click(screen.getByRole('button'))

    expect(onClick).not.toHaveBeenCalled()
  })

  it('applies disabled styles', () => {
    render(<ContextMenuItem disabled>Disabled Item</ContextMenuItem>)

    expect(screen.getByRole('button')).toHaveClass('opacity-50')
    expect(screen.getByRole('button')).toHaveClass('cursor-not-allowed')
  })

  it('applies danger styles', () => {
    render(<ContextMenuItem danger>Delete</ContextMenuItem>)

    expect(screen.getByRole('button')).toHaveClass('text-danger')
  })

  it('renders icon when provided', () => {
    render(
      <ContextMenuItem icon={<span data-testid="icon">📁</span>}>
        With Icon
      </ContextMenuItem>
    )

    expect(screen.getByTestId('icon')).toBeInTheDocument()
  })

  it('applies custom className', () => {
    render(
      <ContextMenuItem className="custom-item-class">Item</ContextMenuItem>
    )

    expect(screen.getByRole('button')).toHaveClass('custom-item-class')
  })
})

describe('ContextMenuDivider', () => {
  it('renders a divider element', () => {
    const { container } = render(<ContextMenuDivider />)

    const divider = container.firstChild
    expect(divider).toHaveClass('border-t')
    expect(divider).toHaveClass('border-divider')
  })

  it('applies custom className', () => {
    const { container } = render(
      <ContextMenuDivider className="custom-divider" />
    )

    expect(container.firstChild).toHaveClass('custom-divider')
  })
})
