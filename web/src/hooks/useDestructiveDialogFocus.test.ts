import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useDestructiveDialogFocus } from './useDestructiveDialogFocus'

describe('useDestructiveDialogFocus', () => {
  let nextFrameId: number
  let scheduledFrames: Map<number, FrameRequestCallback>

  const appendButton = (label: string) => {
    const button = document.createElement('button')
    button.textContent = label
    document.body.appendChild(button)
    return button
  }

  const runNextFrame = () => {
    const nextFrame = scheduledFrames.entries().next()
    expect(nextFrame.done).toBe(false)
    const [frameId, callback] = nextFrame.value!
    scheduledFrames.delete(frameId)
    act(() => callback(performance.now()))
  }

  beforeEach(() => {
    nextFrameId = 1
    scheduledFrames = new Map()
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      const frameId = nextFrameId
      nextFrameId += 1
      scheduledFrames.set(frameId, callback)
      return frameId
    })
    vi.spyOn(window, 'cancelAnimationFrame').mockImplementation((frameId) => {
      scheduledFrames.delete(frameId)
    })
  })

  afterEach(() => {
    document.body.innerHTML = ''
    vi.restoreAllMocks()
  })

  it('restores focus to the captured element', () => {
    const returnTarget = appendButton('return target')
    const outsideTarget = appendButton('outside target')
    const { result } = renderHook(() => useDestructiveDialogFocus(false))

    returnTarget.focus()
    act(() => result.current.captureReturnFocus())
    outsideTarget.focus()
    act(() => result.current.restoreReturnFocus())

    expect(scheduledFrames.size).toBe(1)
    runNextFrame()

    expect(returnTarget).toHaveFocus()
    expect(scheduledFrames.size).toBe(0)
  })

  it('uses the fallback when the captured element is disconnected', () => {
    const returnTarget = appendButton('return target')
    const fallbackTarget = appendButton('fallback target')
    const outsideTarget = appendButton('outside target')
    const { result } = renderHook(() => useDestructiveDialogFocus(false))

    returnTarget.focus()
    act(() => {
      result.current.captureReturnFocus()
      result.current.setFallbackReturnFocus(fallbackTarget)
    })
    returnTarget.remove()
    outsideTarget.focus()
    act(() => result.current.restoreReturnFocus())
    runNextFrame()

    expect(fallbackTarget).toHaveFocus()
  })

  it('uses the last-resort element supplied when restoring', () => {
    const lastResort = appendButton('last resort')
    const outsideTarget = appendButton('outside target')
    const { result } = renderHook(() => useDestructiveDialogFocus(false))

    outsideTarget.focus()
    act(() => result.current.restoreReturnFocus(lastResort))
    runNextFrame()

    expect(lastResort).toHaveFocus()
  })

  it('waits for a disabled captured element and restores focus after it is enabled', () => {
    const returnTarget = appendButton('return target')
    const outsideTarget = appendButton('outside target')
    const { result } = renderHook(() => useDestructiveDialogFocus(false))

    returnTarget.focus()
    act(() => result.current.captureReturnFocus())
    outsideTarget.focus()
    returnTarget.disabled = true
    act(() => result.current.restoreReturnFocus())

    runNextFrame()
    expect(outsideTarget).toHaveFocus()
    expect(scheduledFrames.size).toBe(1)

    returnTarget.disabled = false
    runNextFrame()
    expect(returnTarget).toHaveFocus()
    expect(scheduledFrames.size).toBe(0)
  })

  it('cancels a pending restore when focus is captured and restored again', () => {
    const firstTarget = appendButton('first target')
    const secondTarget = appendButton('second target')
    const outsideTarget = appendButton('outside target')
    const cancelAnimationFrame = vi.mocked(window.cancelAnimationFrame)
    const { result } = renderHook(() => useDestructiveDialogFocus(false))

    firstTarget.focus()
    act(() => result.current.captureReturnFocus())
    outsideTarget.focus()
    act(() => result.current.restoreReturnFocus())
    const firstRestoreFrame = scheduledFrames.keys().next().value!

    secondTarget.focus()
    act(() => result.current.captureReturnFocus())
    outsideTarget.focus()
    act(() => result.current.restoreReturnFocus())

    expect(cancelAnimationFrame).toHaveBeenCalledWith(firstRestoreFrame)
    expect(scheduledFrames.size).toBe(1)
    runNextFrame()

    expect(secondTarget).toHaveFocus()
    expect(scheduledFrames.size).toBe(0)
  })

  it('cancels a pending restore and clears captured targets', () => {
    const returnTarget = appendButton('return target')
    const outsideTarget = appendButton('outside target')
    const cancelAnimationFrame = vi.mocked(window.cancelAnimationFrame)
    const { result } = renderHook(() => useDestructiveDialogFocus(false))

    returnTarget.focus()
    act(() => result.current.captureReturnFocus())
    outsideTarget.focus()
    act(() => result.current.restoreReturnFocus())
    const restoreFrame = scheduledFrames.keys().next().value!

    act(() => result.current.clearReturnFocus())

    expect(cancelAnimationFrame).toHaveBeenCalledWith(restoreFrame)
    expect(scheduledFrames.size).toBe(0)

    returnTarget.focus()
    act(() => result.current.captureReturnFocus())
    outsideTarget.focus()
    act(() => result.current.clearReturnFocus())
    act(() => result.current.restoreReturnFocus())
    runNextFrame()

    expect(outsideTarget).toHaveFocus()
    expect(scheduledFrames.size).toBe(0)
  })

  it('cancels a pending animation frame when unmounted', () => {
    const returnTarget = appendButton('return target')
    const outsideTarget = appendButton('outside target')
    const cancelAnimationFrame = vi.mocked(window.cancelAnimationFrame)
    const { result, unmount } = renderHook(() => useDestructiveDialogFocus(true))

    returnTarget.focus()
    act(() => result.current.captureReturnFocus())
    outsideTarget.focus()
    act(() => result.current.restoreReturnFocus())
    const restoreFrame = scheduledFrames.keys().next().value!

    unmount()

    expect(cancelAnimationFrame).toHaveBeenCalledWith(restoreFrame)
    expect(scheduledFrames.size).toBe(0)
    expect(outsideTarget).toHaveFocus()
  })
})
