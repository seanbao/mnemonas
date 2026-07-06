import { useCallback, useEffect, useRef } from 'react'

function isFocusTargetDisabled(target: HTMLElement): boolean {
  return (target instanceof HTMLButtonElement && target.disabled)
    || target.getAttribute('aria-disabled') === 'true'
}

function getConnectedFocusTarget(
  targets: Array<HTMLElement | null | undefined>,
  includeDisabled: boolean
): HTMLElement | null {
  return targets.find((target) => (
    target?.isConnected
    && (includeDisabled || !isFocusTargetDisabled(target))
  )) ?? null
}

export function useDestructiveDialogFocus(isOpen: boolean) {
  const initialFocusRef = useRef<HTMLButtonElement>(null)
  const returnFocusRef = useRef<HTMLElement | null>(null)
  const fallbackReturnFocusRef = useRef<HTMLElement | null>(null)
  const wasOpenRef = useRef(false)
  const animationFrameRef = useRef(0)

  const cancelScheduledFocus = useCallback(() => {
    if (animationFrameRef.current !== 0) {
      window.cancelAnimationFrame(animationFrameRef.current)
      animationFrameRef.current = 0
    }
  }, [])

  const captureReturnFocus = useCallback((fallback?: HTMLElement | null) => {
    cancelScheduledFocus()
    const activeElement = document.activeElement
    returnFocusRef.current = activeElement instanceof HTMLElement && activeElement !== document.body
      ? activeElement
      : null
    fallbackReturnFocusRef.current = fallback ?? null
  }, [cancelScheduledFocus])

  const setFallbackReturnFocus = useCallback((fallback: HTMLElement | null) => {
    fallbackReturnFocusRef.current = fallback
  }, [])

  const clearReturnFocus = useCallback(() => {
    cancelScheduledFocus()
    returnFocusRef.current = null
    fallbackReturnFocusRef.current = null
  }, [cancelScheduledFocus])

  const restoreReturnFocus = useCallback((
    lastResort?: HTMLElement | null,
    preferredOverride?: HTMLElement | null
  ) => {
    cancelScheduledFocus()
    const returnTarget = returnFocusRef.current
    const fallbackTarget = fallbackReturnFocusRef.current
    returnFocusRef.current = null
    fallbackReturnFocusRef.current = null
    const focusTargets = [preferredOverride, returnTarget, fallbackTarget, lastResort]
    const scheduleRestore = (attempt: number) => {
      animationFrameRef.current = window.requestAnimationFrame(() => {
        if (initialFocusRef.current?.isConnected && attempt < 30) {
          scheduleRestore(attempt + 1)
          return
        }

        const preferredTarget = getConnectedFocusTarget(focusTargets, true)
        if (preferredTarget && isFocusTargetDisabled(preferredTarget) && attempt < 30) {
          scheduleRestore(attempt + 1)
          return
        }

        const target = getConnectedFocusTarget(focusTargets, false)
        target?.focus({ preventScroll: true })
        animationFrameRef.current = 0
      })
    }
    scheduleRestore(0)
  }, [cancelScheduledFocus])

  useEffect(() => {
    if (!isOpen && !wasOpenRef.current) {
      return
    }

    cancelScheduledFocus()
    if (isOpen) {
      wasOpenRef.current = true
      animationFrameRef.current = window.requestAnimationFrame(() => {
        animationFrameRef.current = window.requestAnimationFrame(() => {
          initialFocusRef.current?.focus({ preventScroll: true })
          animationFrameRef.current = 0
        })
      })
    } else {
      wasOpenRef.current = false
      restoreReturnFocus()
    }

    return cancelScheduledFocus
  }, [cancelScheduledFocus, isOpen, restoreReturnFocus])

  useEffect(() => cancelScheduledFocus, [cancelScheduledFocus])

  return {
    initialFocusRef,
    captureReturnFocus,
    clearReturnFocus,
    restoreReturnFocus,
    setFallbackReturnFocus,
  }
}
