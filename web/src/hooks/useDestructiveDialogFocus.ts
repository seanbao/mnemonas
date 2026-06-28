import { useCallback, useEffect, useRef } from 'react'

export function useDestructiveDialogFocus(isOpen: boolean) {
  const initialFocusRef = useRef<HTMLButtonElement>(null)
  const returnFocusRef = useRef<HTMLElement | null>(null)
  const fallbackReturnFocusRef = useRef<HTMLElement | null>(null)
  const wasOpenRef = useRef(false)

  const captureReturnFocus = useCallback((fallback?: HTMLElement | null) => {
    const activeElement = document.activeElement
    if (activeElement instanceof HTMLElement && activeElement !== document.body) {
      returnFocusRef.current = activeElement
    }
    if (fallback) {
      fallbackReturnFocusRef.current = fallback
    }
  }, [])

  const setFallbackReturnFocus = useCallback((fallback: HTMLElement | null) => {
    fallbackReturnFocusRef.current = fallback
  }, [])

  useEffect(() => {
    if (!isOpen && !wasOpenRef.current) {
      return
    }

    let animationFrame = 0
    if (isOpen) {
      wasOpenRef.current = true
      animationFrame = window.requestAnimationFrame(() => {
        animationFrame = window.requestAnimationFrame(() => {
          initialFocusRef.current?.focus({ preventScroll: true })
        })
      })
    } else {
      wasOpenRef.current = false
      const returnTarget = returnFocusRef.current
      const fallbackTarget = fallbackReturnFocusRef.current
      const restoreFocus = (attempt: number) => {
        if (initialFocusRef.current?.isConnected && attempt < 30) {
          animationFrame = window.requestAnimationFrame(() => restoreFocus(attempt + 1))
          return
        }

        const target = returnTarget?.isConnected ? returnTarget : fallbackTarget
        if (target?.isConnected) {
          target.focus({ preventScroll: true })
        }
        returnFocusRef.current = null
        fallbackReturnFocusRef.current = null
      }
      animationFrame = window.requestAnimationFrame(() => restoreFocus(0))
    }

    return () => window.cancelAnimationFrame(animationFrame)
  }, [isOpen])

  return { initialFocusRef, captureReturnFocus, setFallbackReturnFocus }
}
