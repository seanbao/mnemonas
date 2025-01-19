import { useEffect, useCallback, useRef } from 'react'

export interface KeyboardShortcutHandlers {
  onDelete?: () => void
  onSelectAll?: () => void
  onEscape?: () => void
  onCopy?: () => void
  onCut?: () => void
  onPaste?: () => void
  onRename?: () => void
  onEnter?: () => void
  onArrowUp?: (event?: KeyboardEvent) => void
  onArrowDown?: (event?: KeyboardEvent) => void
  onArrowLeft?: (event?: KeyboardEvent) => void
  onArrowRight?: (event?: KeyboardEvent) => void
  onRefresh?: () => void
  onNewFolder?: () => void
}

export interface UseKeyboardShortcutsOptions {
  /**
   * Whether the shortcuts are enabled
   */
  enabled?: boolean
  /**
   * Element IDs or class names to exclude (e.g., input fields)
   */
  excludeElements?: string[]
}

/**
 * Check if event target is an input element that should capture keyboard events
 */
function isInputElement(target: EventTarget | null): boolean {
  if (!target || !(target instanceof HTMLElement)) return false
  
  const tagName = target.tagName.toLowerCase()
  if (tagName === 'input' || tagName === 'textarea' || tagName === 'select') {
    return true
  }
  
  // Check for contenteditable
  if (target.isContentEditable) {
    return true
  }
  
  // Check for role="textbox"
  if (target.getAttribute('role') === 'textbox') {
    return true
  }
  
  return false
}

/**
 * Hook for handling keyboard shortcuts in file browser
 * 
 * Usage:
 * ```tsx
 * useKeyboardShortcuts({
 *   onDelete: () => handleDelete(),
 *   onSelectAll: () => selectAll(),
 *   onCopy: () => copyToClipboard(),
 *   onPaste: () => pasteFromClipboard(),
 * })
 * ```
 * 
 * Supported shortcuts:
 * - Delete / Backspace: Delete selected files
 * - Ctrl+A / Cmd+A: Select all files
 * - Escape: Clear selection
 * - Ctrl+C / Cmd+C: Copy selected files
 * - Ctrl+X / Cmd+X: Cut selected files
 * - Ctrl+V / Cmd+V: Paste files
 * - F2: Rename selected file
 * - Enter: Open selected file/folder
 * - Arrow keys: Navigate through files
 * - Ctrl+R / Cmd+R / F5: Refresh
 * - Ctrl+Shift+N / Cmd+Shift+N: New folder
 */
export function useKeyboardShortcuts(
  handlers: KeyboardShortcutHandlers,
  options: UseKeyboardShortcutsOptions = {}
) {
  const { enabled = true, excludeElements = [] } = options
  const handlersRef = useRef(handlers)
  
  // Update handlers ref on each render to avoid stale closures
  useEffect(() => {
    handlersRef.current = handlers
  }, [handlers])

  const handleKeyDown = useCallback((e: KeyboardEvent) => {
    // Skip if disabled
    if (!enabled) return
    
    // Skip if focus is in input element
    if (isInputElement(e.target)) return
    
    // Skip for excluded elements
    if (e.target instanceof HTMLElement) {
      for (const selector of excludeElements) {
        if (e.target.matches(selector) || e.target.closest(selector)) {
          return
        }
      }
    }
    
    const { current: h } = handlersRef
    const isMac = navigator.platform.toUpperCase().indexOf('MAC') >= 0
    const ctrlOrCmd = isMac ? e.metaKey : e.ctrlKey
    
    // Delete / Backspace
    if ((e.key === 'Delete' || e.key === 'Backspace') && !ctrlOrCmd && !e.shiftKey) {
      if (h.onDelete) {
        e.preventDefault()
        h.onDelete()
      }
      return
    }
    
    // Ctrl+A / Cmd+A - Select all
    if (e.key === 'a' && ctrlOrCmd && !e.shiftKey) {
      if (h.onSelectAll) {
        e.preventDefault()
        h.onSelectAll()
      }
      return
    }
    
    // Escape - Clear selection
    if (e.key === 'Escape' && !ctrlOrCmd && !e.shiftKey) {
      if (h.onEscape) {
        e.preventDefault()
        h.onEscape()
      }
      return
    }
    
    // Ctrl+C / Cmd+C - Copy
    if (e.key === 'c' && ctrlOrCmd && !e.shiftKey) {
      if (h.onCopy) {
        e.preventDefault()
        h.onCopy()
      }
      return
    }
    
    // Ctrl+X / Cmd+X - Cut
    if (e.key === 'x' && ctrlOrCmd && !e.shiftKey) {
      if (h.onCut) {
        e.preventDefault()
        h.onCut()
      }
      return
    }
    
    // Ctrl+V / Cmd+V - Paste
    if (e.key === 'v' && ctrlOrCmd && !e.shiftKey) {
      if (h.onPaste) {
        e.preventDefault()
        h.onPaste()
      }
      return
    }
    
    // F2 - Rename
    if (e.key === 'F2' && !ctrlOrCmd && !e.shiftKey) {
      if (h.onRename) {
        e.preventDefault()
        h.onRename()
      }
      return
    }
    
    // Enter - Open
    if (e.key === 'Enter' && !ctrlOrCmd && !e.shiftKey) {
      if (h.onEnter) {
        e.preventDefault()
        h.onEnter()
      }
      return
    }
    
    // Arrow Up
    if (e.key === 'ArrowUp' && !ctrlOrCmd) {
      if (h.onArrowUp) {
        e.preventDefault()
        h.onArrowUp(e)
      }
      return
    }
    
    // Arrow Down
    if (e.key === 'ArrowDown' && !ctrlOrCmd) {
      if (h.onArrowDown) {
        e.preventDefault()
        h.onArrowDown(e)
      }
      return
    }
    
    // Arrow Left
    if (e.key === 'ArrowLeft' && !ctrlOrCmd) {
      if (h.onArrowLeft) {
        e.preventDefault()
        h.onArrowLeft(e)
      }
      return
    }
    
    // Arrow Right
    if (e.key === 'ArrowRight' && !ctrlOrCmd) {
      if (h.onArrowRight) {
        e.preventDefault()
        h.onArrowRight(e)
      }
      return
    }
    
    // Ctrl+R / Cmd+R / F5 - Refresh
    if ((e.key === 'r' && ctrlOrCmd && !e.shiftKey) || e.key === 'F5') {
      if (h.onRefresh) {
        e.preventDefault()
        h.onRefresh()
      }
      return
    }
    
    // Ctrl+Shift+N / Cmd+Shift+N - New folder
    if (e.key.toLowerCase() === 'n' && ctrlOrCmd && e.shiftKey) {
      if (h.onNewFolder) {
        e.preventDefault()
        h.onNewFolder()
      }
      return
    }
  }, [enabled, excludeElements])

  useEffect(() => {
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [handleKeyDown])
}

export default useKeyboardShortcuts
