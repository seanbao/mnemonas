import { useCallback, useEffect } from 'react'
import { useBeforeUnload, useBlocker, type BlockerFunction } from 'react-router-dom'
import { useSettingsDraftStore } from '@/stores/settingsDraft'
import { confirmDiscardUnsavedChanges } from '@/lib/unsavedChanges'

function isSettingsCategoryNavigation(
  currentPathname: string,
  nextPathname: string,
): boolean {
  return currentPathname === '/settings' && nextPathname === '/settings'
}

export function UnsavedChangesGuard() {
  const hasPendingChanges = useSettingsDraftStore((state) => state.hasPendingChanges)
  const shouldBlock = useCallback<BlockerFunction>(({ currentLocation, nextLocation }) => {
    if (!useSettingsDraftStore.getState().hasPendingChanges) {
      return false
    }

    return !isSettingsCategoryNavigation(currentLocation.pathname, nextLocation.pathname)
  }, [])
  const blocker = useBlocker(shouldBlock)

  useEffect(() => {
    if (blocker.state !== 'blocked') return
    if (confirmDiscardUnsavedChanges()) {
      blocker.proceed()
    } else {
      blocker.reset()
    }
  }, [blocker])

  useBeforeUnload(useCallback((event) => {
    if (!hasPendingChanges) return
    event.preventDefault()
    event.returnValue = ''
  }, [hasPendingChanges]))

  return null
}
