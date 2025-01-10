import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useContextMenu } from './useContextMenu'

describe('useContextMenu', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // Mock window dimensions
    Object.defineProperty(window, 'innerWidth', { value: 1920, writable: true })
    Object.defineProperty(window, 'innerHeight', { value: 1080, writable: true })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  describe('initial state', () => {
    it('is closed by default', () => {
      const { result } = renderHook(() => useContextMenu())
      expect(result.current.state.isOpen).toBe(false)
    })

    it('has default position of (0, 0)', () => {
      const { result } = renderHook(() => useContextMenu())
      expect(result.current.state.position).toEqual({ x: 0, y: 0 })
    })

    it('has null targetId by default', () => {
      const { result } = renderHook(() => useContextMenu())
      expect(result.current.state.targetId).toBeNull()
    })
  })

  describe('show', () => {
    it('opens context menu at specified position', () => {
      const { result } = renderHook(() => useContextMenu())

      act(() => {
        result.current.show('item-1', 100, 200)
      })

      expect(result.current.state.isOpen).toBe(true)
      expect(result.current.state.position).toEqual({ x: 100, y: 200 })
      expect(result.current.state.targetId).toBe('item-1')
    })

    it('updates position when called multiple times', () => {
      const { result } = renderHook(() => useContextMenu())

      act(() => {
        result.current.show('item-1', 100, 200)
      })

      act(() => {
        result.current.show('item-2', 300, 400)
      })

      expect(result.current.state.position).toEqual({ x: 300, y: 400 })
      expect(result.current.state.targetId).toBe('item-2')
    })

    it('adjusts position when too close to right edge', () => {
      const { result } = renderHook(() => useContextMenu())
      
      // Position near right edge (1920 - 200 - 8 = 1712 max)
      act(() => {
        result.current.show('item', 1850, 100)
      })

      // Should be adjusted to fit within viewport
      expect(result.current.state.position.x).toBeLessThanOrEqual(1920 - 200)
    })

    it('adjusts position when too close to bottom edge', () => {
      const { result } = renderHook(() => useContextMenu())
      
      // Position near bottom edge (1080 - 300 - 8 = 772 max)
      act(() => {
        result.current.show('item', 100, 1050)
      })

      // Should be adjusted to fit within viewport
      expect(result.current.state.position.y).toBeLessThanOrEqual(1080 - 300)
    })

    it('does not adjust position when within bounds', () => {
      const { result } = renderHook(() => useContextMenu())
      
      act(() => {
        result.current.show('item', 500, 500)
      })

      expect(result.current.state.position).toEqual({ x: 500, y: 500 })
    })

    it('calls onOpen callback', () => {
      const onOpen = vi.fn()
      const { result } = renderHook(() => useContextMenu({ onOpen }))

      act(() => {
        result.current.show('item-1', 100, 200)
      })

      expect(onOpen).toHaveBeenCalledWith('item-1', { x: 100, y: 200 })
    })
  })

  describe('hide', () => {
    it('closes the context menu', () => {
      const { result } = renderHook(() => useContextMenu())

      act(() => {
        result.current.show('item', 100, 200)
      })

      expect(result.current.state.isOpen).toBe(true)

      act(() => {
        result.current.hide()
      })

      expect(result.current.state.isOpen).toBe(false)
    })

    it('resets targetId to null', () => {
      const { result } = renderHook(() => useContextMenu())

      act(() => {
        result.current.show('item', 100, 200)
      })

      act(() => {
        result.current.hide()
      })

      expect(result.current.state.targetId).toBeNull()
    })

    it('can be called when already closed', () => {
      const { result } = renderHook(() => useContextMenu())

      act(() => {
        result.current.hide()
      })

      expect(result.current.state.isOpen).toBe(false)
    })

    it('calls onClose callback', () => {
      const onClose = vi.fn()
      const { result } = renderHook(() => useContextMenu({ onClose }))

      act(() => {
        result.current.show('item', 100, 200)
      })

      act(() => {
        result.current.hide()
      })

      expect(onClose).toHaveBeenCalled()
    })
  })

  describe('getContextMenuProps', () => {
    it('returns onContextMenu handler', () => {
      const { result } = renderHook(() => useContextMenu())
      const props = result.current.getContextMenuProps('item-1')

      expect(props.onContextMenu).toBeDefined()
      expect(typeof props.onContextMenu).toBe('function')
    })

    it('onContextMenu prevents default and shows menu', () => {
      const { result } = renderHook(() => useContextMenu())
      const props = result.current.getContextMenuProps('item-1')

      const mockEvent = {
        preventDefault: vi.fn(),
        stopPropagation: vi.fn(),
        clientX: 150,
        clientY: 250,
      }

      act(() => {
        props.onContextMenu(mockEvent as unknown as React.MouseEvent)
      })

      expect(mockEvent.preventDefault).toHaveBeenCalled()
      expect(mockEvent.stopPropagation).toHaveBeenCalled()
      expect(result.current.state.isOpen).toBe(true)
      expect(result.current.state.position).toEqual({ x: 150, y: 250 })
      expect(result.current.state.targetId).toBe('item-1')
    })
  })

  describe('setContainerRef', () => {
    it('is a function', () => {
      const { result } = renderHook(() => useContextMenu())
      expect(typeof result.current.setContainerRef).toBe('function')
    })
  })

  describe('edge cases', () => {
    it('handles zero position', () => {
      const { result } = renderHook(() => useContextMenu())

      act(() => {
        result.current.show('item', 0, 0)
      })

      // Should clamp to minimum 8
      expect(result.current.state.position.x).toBe(8)
      expect(result.current.state.position.y).toBe(8)
    })
  })
})
