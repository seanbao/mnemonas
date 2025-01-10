import { useState, useCallback, useEffect, useRef } from 'react'

export interface ContextMenuState {
  isOpen: boolean
  position: { x: number; y: number }
  targetId: string | null
}

export interface UseContextMenuOptions {
  onOpen?: (targetId: string, position: { x: number; y: number }) => void
  onClose?: () => void
}

/**
 * Hook for managing context menu state
 * 
 * Usage:
 * ```tsx
 * const { state, show, hide, getContextMenuProps } = useContextMenu()
 * 
 * <div {...getContextMenuProps('item-1')}>
 *   Item 1
 * </div>
 * 
 * {state.isOpen && (
 *   <ContextMenu position={state.position} onClose={hide}>
 *     ...
 *   </ContextMenu>
 * )}
 * ```
 */
export function useContextMenu(options: UseContextMenuOptions = {}) {
  const { onOpen, onClose } = options
  const [state, setState] = useState<ContextMenuState>({
    isOpen: false,
    position: { x: 0, y: 0 },
    targetId: null,
  })
  
  const containerRef = useRef<HTMLElement | null>(null)

  const show = useCallback((targetId: string, x: number, y: number) => {
    // Adjust position to keep menu within viewport
    const viewportWidth = window.innerWidth
    const viewportHeight = window.innerHeight
    const menuWidth = 200 // Estimated menu width
    const menuHeight = 300 // Estimated menu height
    
    const adjustedX = x + menuWidth > viewportWidth ? viewportWidth - menuWidth - 8 : x
    const adjustedY = y + menuHeight > viewportHeight ? viewportHeight - menuHeight - 8 : y
    
    const position = { x: Math.max(8, adjustedX), y: Math.max(8, adjustedY) }
    
    setState({
      isOpen: true,
      position,
      targetId,
    })
    
    onOpen?.(targetId, position)
  }, [onOpen])

  const hide = useCallback(() => {
    setState(prev => ({
      ...prev,
      isOpen: false,
      targetId: null,
    }))
    onClose?.()
  }, [onClose])

  // Close on click outside
  useEffect(() => {
    if (!state.isOpen) return

    const handleClickOutside = (e: MouseEvent) => {
      // Don't close if clicking inside the menu
      const target = e.target as Element
      if (target.closest('[data-context-menu]')) return
      hide()
    }

    const handleEscape = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        hide()
      }
    }

    const handleScroll = () => {
      hide()
    }

    // Small delay to prevent immediate close
    const timeoutId = setTimeout(() => {
      document.addEventListener('mousedown', handleClickOutside)
      document.addEventListener('keydown', handleEscape)
      document.addEventListener('scroll', handleScroll, true)
    }, 10)

    return () => {
      clearTimeout(timeoutId)
      document.removeEventListener('mousedown', handleClickOutside)
      document.removeEventListener('keydown', handleEscape)
      document.removeEventListener('scroll', handleScroll, true)
    }
  }, [state.isOpen, hide])

  // Generate props for context menu trigger elements
  const getContextMenuProps = useCallback((targetId: string) => ({
    onContextMenu: (e: React.MouseEvent) => {
      e.preventDefault()
      e.stopPropagation()
      show(targetId, e.clientX, e.clientY)
    },
  }), [show])

  // Set container ref for positioning calculations
  const setContainerRef = useCallback((ref: HTMLElement | null) => {
    containerRef.current = ref
  }, [])

  return {
    state,
    show,
    hide,
    getContextMenuProps,
    setContainerRef,
  }
}

export default useContextMenu
