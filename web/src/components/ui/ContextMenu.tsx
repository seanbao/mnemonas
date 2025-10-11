import { useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import { cn } from '@/lib/utils'

export interface ContextMenuProps {
  isOpen: boolean
  position: { x: number; y: number }
  onClose: () => void
  children: React.ReactNode
  className?: string
}

/**
 * Positioned context menu component that renders via portal
 * 
 * Usage:
 * ```tsx
 * <ContextMenu
 *   isOpen={state.isOpen}
 *   position={state.position}
 *   onClose={hide}
 * >
 *   <ContextMenuItem onClick={handleAction}>Action</ContextMenuItem>
 * </ContextMenu>
 * ```
 */
export function ContextMenu({ isOpen, position, children, className }: ContextMenuProps) {
  const menuRef = useRef<HTMLDivElement>(null)

  // Adjust position if menu would overflow viewport
  useEffect(() => {
    if (!isOpen || !menuRef.current) return
    
    const menu = menuRef.current
    const rect = menu.getBoundingClientRect()
    const viewportWidth = window.innerWidth
    const viewportHeight = window.innerHeight
    
    let adjustedX = position.x
    let adjustedY = position.y
    
    if (position.x + rect.width > viewportWidth) {
      adjustedX = viewportWidth - rect.width - 8
    }
    if (position.y + rect.height > viewportHeight) {
      adjustedY = viewportHeight - rect.height - 8
    }
    
    menu.style.left = `${Math.max(8, adjustedX)}px`
    menu.style.top = `${Math.max(8, adjustedY)}px`
  }, [isOpen, position])

  if (!isOpen) return null

  return createPortal(
    <div
      ref={menuRef}
      data-context-menu
      className={cn(
        "fixed z-[100] min-w-[180px] rounded-lg py-1",
        "bg-content1 border border-divider shadow-lg",
        "animate-in fade-in-0 zoom-in-95 duration-100",
        className
      )}
      style={{
        left: position.x,
        top: position.y,
      }}
    >
      {children}
    </div>,
    document.body
  )
}

export interface ContextMenuSectionProps {
  title?: string
  children: React.ReactNode
  showDivider?: boolean
}

export function ContextMenuSection({ title, children, showDivider = false }: ContextMenuSectionProps) {
  return (
    <div className={cn(showDivider && "border-b border-divider pb-1 mb-1")}>
      {title && (
        <div className="px-3 py-1.5 text-xs font-semibold uppercase text-default-500">
          {title}
        </div>
      )}
      {children}
    </div>
  )
}

export interface ContextMenuItemProps {
  icon?: React.ReactNode
  children: React.ReactNode
  onClick?: () => void
  disabled?: boolean
  danger?: boolean
  className?: string
}

export function ContextMenuItem({ 
  icon, 
  children, 
  onClick, 
  disabled = false, 
  danger = false,
  className 
}: ContextMenuItemProps) {
  return (
    <button
      type="button"
      className={cn(
        "w-full flex items-center gap-2.5 px-3 py-2 text-sm text-left transition-colors",
        "hover:bg-content2 focus:bg-content2 focus:outline-none",
        disabled && "opacity-50 cursor-not-allowed",
        danger && "text-danger hover:bg-danger/10",
        className
      )}
      onClick={() => {
        if (!disabled) {
          onClick?.()
        }
      }}
      disabled={disabled}
    >
      {icon && <span className="w-4 h-4 flex items-center justify-center">{icon}</span>}
      <span className="flex-1">{children}</span>
    </button>
  )
}

export interface ContextMenuDividerProps {
  className?: string
}

export function ContextMenuDivider({ className }: ContextMenuDividerProps) {
  return <div className={cn("my-1 border-t border-divider", className)} />
}
