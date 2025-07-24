import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useKeyboardShortcuts, type KeyboardShortcutHandlers, type UseKeyboardShortcutsOptions } from './useKeyboardShortcuts'

describe('useKeyboardShortcuts', () => {
  const mockAddEventListener = vi.spyOn(document, 'addEventListener')
  vi.spyOn(document, 'removeEventListener')

  const triggerKeyDown = (key: string, options: Partial<KeyboardEvent> = {}) => {
    const event = new KeyboardEvent('keydown', {
      key,
      bubbles: true,
      cancelable: true,
      ...options,
    })
    document.dispatchEvent(event)
  }

  const triggerKeyDownOn = (target: HTMLElement, key: string, options: Partial<KeyboardEvent> = {}) => {
    const event = new KeyboardEvent('keydown', {
      key,
      bubbles: true,
      cancelable: true,
      ...options,
    })
    target.dispatchEvent(event)
  }

  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    document.body.innerHTML = ''
    vi.restoreAllMocks()
  })

  describe('initialization', () => {
    it('adds keydown event listener on mount', () => {
      const handlers: KeyboardShortcutHandlers = {
        onDelete: vi.fn(),
      }

      renderHook(() => useKeyboardShortcuts(handlers))

      expect(mockAddEventListener).toHaveBeenCalledWith(
        'keydown',
        expect.any(Function)
      )
    })

    it('does not trigger handlers when disabled', () => {
      const onDelete = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onDelete }
      const options: UseKeyboardShortcutsOptions = { enabled: false }

      renderHook(() => useKeyboardShortcuts(handlers, options))

      triggerKeyDown('Delete')

      expect(onDelete).not.toHaveBeenCalled()
    })

    it('cleans up properly on unmount', () => {
      const handlers: KeyboardShortcutHandlers = {
        onDelete: vi.fn(),
      }

      const { unmount } = renderHook(() => useKeyboardShortcuts(handlers))
      
      // Verify hook can be safely unmounted without throwing
      expect(() => unmount()).not.toThrow()
    })
  })

  describe('Delete shortcut', () => {
    it('calls onDelete when Delete key pressed', () => {
      const onDelete = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onDelete }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('Delete')

      expect(onDelete).toHaveBeenCalled()
    })

    it('calls onDelete when Backspace pressed', () => {
      const onDelete = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onDelete }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('Backspace')

      expect(onDelete).toHaveBeenCalled()
    })
  })

  describe('Escape shortcut', () => {
    it('calls onEscape when Escape key pressed', () => {
      const onEscape = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onEscape }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('Escape')

      expect(onEscape).toHaveBeenCalled()
    })
  })

  describe('Select All (Ctrl+A)', () => {
    it('calls onSelectAll when Ctrl+A pressed', () => {
      const onSelectAll = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onSelectAll }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('a', { ctrlKey: true })

      expect(onSelectAll).toHaveBeenCalled()
    })
  })

  describe('Copy (Ctrl+C)', () => {
    it('calls onCopy when Ctrl+C pressed', () => {
      const onCopy = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onCopy }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('c', { ctrlKey: true })

      expect(onCopy).toHaveBeenCalled()
    })
  })

  describe('Cut (Ctrl+X)', () => {
    it('calls onCut when Ctrl+X pressed', () => {
      const onCut = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onCut }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('x', { ctrlKey: true })

      expect(onCut).toHaveBeenCalled()
    })
  })

  describe('Paste (Ctrl+V)', () => {
    it('calls onPaste when Ctrl+V pressed', () => {
      const onPaste = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onPaste }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('v', { ctrlKey: true })

      expect(onPaste).toHaveBeenCalled()
    })
  })

  describe('Rename (F2)', () => {
    it('calls onRename when F2 pressed', () => {
      const onRename = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onRename }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('F2')

      expect(onRename).toHaveBeenCalled()
    })
  })

  describe('Open (Enter)', () => {
    it('calls onEnter when Enter pressed', () => {
      const onEnter = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onEnter }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('Enter')

      expect(onEnter).toHaveBeenCalled()
    })
  })

  describe('Toggle selection (Space)', () => {
    it('calls onSpace when Space key pressed', () => {
      const onSpace = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onSpace }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown(' ')

      expect(onSpace).toHaveBeenCalled()
    })

    it('calls onSpace when legacy Space key name is used', () => {
      const onSpace = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onSpace }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('Space')

      expect(onSpace).toHaveBeenCalled()
    })
  })

  describe('New folder (Ctrl+Shift+N)', () => {
    it('calls onNewFolder when Ctrl+Shift+N pressed with lowercase key', () => {
      const onNewFolder = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onNewFolder }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('n', { ctrlKey: true, shiftKey: true })

      expect(onNewFolder).toHaveBeenCalled()
    })

    it('calls onNewFolder when Ctrl+Shift+N pressed with uppercase key', () => {
      const onNewFolder = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onNewFolder }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('N', { ctrlKey: true, shiftKey: true })

      expect(onNewFolder).toHaveBeenCalled()
    })
  })

  describe('Refresh (Ctrl+R / F5)', () => {
    it('calls onRefresh when F5 pressed', () => {
      const onRefresh = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onRefresh }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('F5')

      expect(onRefresh).toHaveBeenCalled()
    })

    it('calls onRefresh when Ctrl+R pressed', () => {
      const onRefresh = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onRefresh }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('r', { ctrlKey: true })

      expect(onRefresh).toHaveBeenCalled()
    })
  })

  describe('New Folder (Ctrl+Shift+N)', () => {
    it('calls onNewFolder when Ctrl+Shift+N pressed', () => {
      const onNewFolder = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onNewFolder }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('N', { ctrlKey: true, shiftKey: true })

      expect(onNewFolder).toHaveBeenCalled()
    })
  })

  describe('Navigation (Arrow keys)', () => {
    it('calls onArrowUp when ArrowUp pressed', () => {
      const onArrowUp = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onArrowUp }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('ArrowUp')

      expect(onArrowUp).toHaveBeenCalled()
    })

    it('calls onArrowDown when ArrowDown pressed', () => {
      const onArrowDown = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onArrowDown }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('ArrowDown')

      expect(onArrowDown).toHaveBeenCalled()
    })

    it('calls onArrowLeft when ArrowLeft pressed', () => {
      const onArrowLeft = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onArrowLeft }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('ArrowLeft')

      expect(onArrowLeft).toHaveBeenCalled()
    })

    it('calls onArrowRight when ArrowRight pressed', () => {
      const onArrowRight = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onArrowRight }

      renderHook(() => useKeyboardShortcuts(handlers))

      triggerKeyDown('ArrowRight')

      expect(onArrowRight).toHaveBeenCalled()
    })
  })

  describe('callback not provided', () => {
    it('does not throw when callback is not provided', () => {
      const handlers: KeyboardShortcutHandlers = {}

      renderHook(() => useKeyboardShortcuts(handlers))

      expect(() => {
        triggerKeyDown('Delete')
        triggerKeyDown('c', { ctrlKey: true })
      }).not.toThrow()
    })
  })

  describe('ignored targets', () => {
    it('does not trigger shortcuts from native form fields', () => {
      const onDelete = vi.fn()
      renderHook(() => useKeyboardShortcuts({ onDelete }))

      for (const element of [
        document.createElement('input'),
        document.createElement('textarea'),
        document.createElement('select'),
      ]) {
        document.body.appendChild(element)
        triggerKeyDownOn(element, 'Delete')
      }

      expect(onDelete).not.toHaveBeenCalled()
    })

    it('does not trigger shortcuts from contenteditable elements', () => {
      const onDelete = vi.fn()
      const editor = document.createElement('div')
      editor.contentEditable = 'true'
      document.body.appendChild(editor)

      renderHook(() => useKeyboardShortcuts({ onDelete }))

      triggerKeyDownOn(editor, 'Delete')

      expect(onDelete).not.toHaveBeenCalled()
    })

    it('does not trigger shortcuts from role textbox elements', () => {
      const onDelete = vi.fn()
      const editor = document.createElement('div')
      editor.setAttribute('role', 'textbox')
      document.body.appendChild(editor)

      renderHook(() => useKeyboardShortcuts({ onDelete }))

      triggerKeyDownOn(editor, 'Delete')

      expect(onDelete).not.toHaveBeenCalled()
    })

    it('does not trigger shortcuts inside excluded elements', () => {
      const onDelete = vi.fn()
      const wrapper = document.createElement('div')
      wrapper.className = 'shortcut-ignore'
      const child = document.createElement('button')
      wrapper.appendChild(child)
      document.body.appendChild(wrapper)

      renderHook(() => useKeyboardShortcuts({ onDelete }, { excludeElements: ['.shortcut-ignore'] }))

      triggerKeyDownOn(child, 'Delete')

      expect(onDelete).not.toHaveBeenCalled()
    })
  })

  describe('enabling/disabling', () => {
    it('does not trigger handlers when disabled', () => {
      const onDelete = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onDelete }

      renderHook(() => useKeyboardShortcuts(handlers, { enabled: false }))

      triggerKeyDown('Delete')

      expect(onDelete).not.toHaveBeenCalled()
    })

    it('triggers handlers when enabled is true', () => {
      const onDelete = vi.fn()
      const handlers: KeyboardShortcutHandlers = { onDelete }

      renderHook(() => useKeyboardShortcuts(handlers, { enabled: true }))

      triggerKeyDown('Delete')

      expect(onDelete).toHaveBeenCalled()
    })
  })
})
