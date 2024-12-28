/**
 * HeroUI mock components for jsdom testing environment.
 * 
 * HeroUI uses React Aria Collections which has compatibility issues with jsdom.
 * This mock provides simplified implementations that work in tests while
 * preserving the component structure for proper testing.
 */
import * as React from 'react'

// Re-export everything from actual HeroUI
export * from '@heroui/react'

// Mock Table components
interface TableProps {
  children: React.ReactNode
  'aria-label'?: string
  removeWrapper?: boolean
  classNames?: Record<string, string>
  selectionMode?: string
  selectedKeys?: Set<string>
  onSelectionChange?: (keys: Set<string>) => void
}

export function Table({ children, 'aria-label': ariaLabel, ...props }: TableProps) {
  return (
    <div data-testid="heroui-table" role="table" aria-label={ariaLabel} {...props}>
      {children}
    </div>
  )
}

interface TableHeaderProps {
  children: React.ReactNode
  columns?: unknown[]
}

export function TableHeader({ children }: TableHeaderProps) {
  return (
    <div data-testid="table-header" role="rowgroup">
      <div role="row">{children}</div>
    </div>
  )
}

interface TableColumnProps {
  children: React.ReactNode
  key?: string
}

export function TableColumn({ children }: TableColumnProps) {
  return <div role="columnheader">{children}</div>
}

interface TableBodyProps<T> {
  children: React.ReactNode | ((item: T) => React.ReactNode)
  items?: T[]
  emptyContent?: React.ReactNode
  isLoading?: boolean
  loadingContent?: React.ReactNode
}

export function TableBody<T>({ children, items, emptyContent, isLoading, loadingContent }: TableBodyProps<T>) {
  if (isLoading && loadingContent) {
    return <div data-testid="table-body" role="rowgroup">{loadingContent}</div>
  }
  
  if (typeof children === 'function' && items) {
    if (items.length === 0) {
      return <div data-testid="table-body" role="rowgroup">{emptyContent}</div>
    }
    return (
      <div data-testid="table-body" role="rowgroup">
        {items.map((item, index) => (
          <React.Fragment key={index}>{children(item)}</React.Fragment>
        ))}
      </div>
    )
  }
  
  // When children is not a function, it's ReactNode
  const childrenNode = typeof children === 'function' ? null : children
  return <div data-testid="table-body" role="rowgroup">{childrenNode}</div>
}

interface TableRowProps {
  children: React.ReactNode
  key?: string
  className?: string
}

export function TableRow({ children, className }: TableRowProps) {
  return <div role="row" className={className}>{children}</div>
}

interface TableCellProps {
  children: React.ReactNode
}

export function TableCell({ children }: TableCellProps) {
  return <div role="cell">{children}</div>
}

// Mock Dropdown components for proper testing
interface DropdownProps {
  children: React.ReactNode
  isOpen?: boolean
  onOpenChange?: (open: boolean) => void
}

export function Dropdown({ children, ...props }: DropdownProps) {
  const [isOpen, setIsOpen] = React.useState(false)
  
  return (
    <div data-testid="heroui-dropdown" data-open={isOpen} {...props}>
      {React.Children.map(children, child => {
        if (React.isValidElement(child)) {
          if (child.type === DropdownTrigger) {
            return React.cloneElement(child as React.ReactElement<{ onClick: () => void }>, {
              onClick: () => setIsOpen(!isOpen)
            })
          }
          if (child.type === DropdownMenu && isOpen) {
            return child
          }
        }
        return child
      })}
    </div>
  )
}

interface DropdownTriggerProps {
  children: React.ReactNode
  onClick?: () => void
}

export function DropdownTrigger({ children, onClick }: DropdownTriggerProps) {
  return (
    <div data-testid="dropdown-trigger" onClick={onClick}>
      {children}
    </div>
  )
}

interface DropdownMenuProps {
  children: React.ReactNode
  'aria-label'?: string
  onAction?: (key: string) => void
}

export function DropdownMenu({ children, 'aria-label': ariaLabel, onAction }: DropdownMenuProps) {
  return (
    <div data-testid="dropdown-menu" role="menu" aria-label={ariaLabel}>
      {React.Children.map(children, child => {
        if (React.isValidElement(child) && onAction) {
          return React.cloneElement(child as React.ReactElement<{ onAction: typeof onAction }>, { onAction })
        }
        return child
      })}
    </div>
  )
}

interface DropdownItemProps {
  children: React.ReactNode
  key?: string
  className?: string
  color?: string
  startContent?: React.ReactNode
  onAction?: (key: string) => void
}

export function DropdownItem({ children, key, className, startContent, onAction }: DropdownItemProps) {
  return (
    <div
      role="menuitem"
      className={className}
      onClick={() => onAction?.(key || '')}
      data-key={key}
    >
      {startContent}
      {children}
    </div>
  )
}

interface DropdownSectionProps {
  children: React.ReactNode
  title?: string
  showDivider?: boolean
}

export function DropdownSection({ children, title }: DropdownSectionProps) {
  return (
    <div data-testid="dropdown-section">
      {title && <div className="dropdown-section-title">{title}</div>}
      {children}
    </div>
  )
}

// Export mock useDisclosure for modals
export const useDisclosure = () => {
  const [isOpen, setIsOpen] = React.useState(false)
  return {
    isOpen,
    onOpen: () => setIsOpen(true),
    onClose: () => setIsOpen(false),
    onOpenChange: setIsOpen,
  }
}
