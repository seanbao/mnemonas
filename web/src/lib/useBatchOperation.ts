import { useCallback, useEffect, useRef, useState } from 'react'
import { addToast } from '@heroui/react'
import { getNonBlankJsonString } from './jsonErrorResponse'

/**
 * Result of a batch operation
 */
export interface BatchOperationResult {
  succeeded: number
  failed: number
  total: number
  succeededItems: unknown[]
  failedItems: unknown[]
  failedErrors: unknown[]
  warningCount: number
  warningMessages: string[]
}

export interface BatchOperationToast {
  title: string
  description?: string
  color: 'success' | 'warning' | 'danger'
}

export interface BatchOperationMessages {
  /** Message when all succeed. Use {count} placeholder for count */
  success: string
  /** Message when all fail. Use {count} placeholder for count */
  failure: string
  /** Message when partial success. Use {succeeded} and {failed} placeholders */
  partial: string
}

interface WarningAwareActionResult {
  warning?: boolean
  message?: string
}

function isWarningAwareActionResult(value: unknown): value is WarningAwareActionResult {
  return !!value && typeof value === 'object' && !Array.isArray(value)
}

function getDefaultWarningSuccessTitle(messages: BatchOperationMessages, succeeded: number): string {
  return `${messages.success.replace('{count}', String(succeeded))}，但存在警告`
}

export interface BatchOperationContext {
  signal?: AbortSignal
}

export interface BatchOperationExecuteOptions {
  signal?: AbortSignal
}

/**
 * Hook options
 */
export interface UseBatchOperationOptions<T, R = void> {
  /** The async operation to perform on each item */
  operation: (item: T, context: BatchOperationContext) => Promise<R>
  /** Message templates for toast notifications */
  messages: BatchOperationMessages
  /** Optional mapper for locally safe warning messages. Backend messages are not exposed by default. */
  getWarningMessage?: (result: R) => string | undefined
  /** Optional custom toast builder for batch results */
  getToast?: (result: BatchOperationResult) => BatchOperationToast | null | undefined
  /** Optional callback when operation completes */
  onComplete?: (result: BatchOperationResult) => void
}

/**
 * Custom hook for handling batch operations with proper error handling
 * and toast notifications.
 * 
 * @example
 * ```tsx
 * const { execute, isLoading } = useBatchOperation({
 *   operation: deleteFile,
 *   messages: {
 *     success: '{count} files deleted',
 *     failure: '{count} files failed',
 *     partial: '{succeeded} files deleted, {failed} failed',
 *   },
 *   onComplete: () => clearSelection(),
 * })
 * 
 * // Execute on multiple items
 * await execute(selectedItems)
 * ```
 */
export function useBatchOperation<T, R = void>(
  options: UseBatchOperationOptions<T, R>
) {
  const { operation, messages, getWarningMessage, getToast, onComplete } = options
  const [isLoading, setIsLoading] = useState(false)
  const mountedRef = useRef(true)

  useEffect(() => () => {
    mountedRef.current = false
  }, [])

  const execute = useCallback(
    async (items: T[], executeOptions: BatchOperationExecuteOptions = {}): Promise<BatchOperationResult> => {
      if (items.length === 0) {
        return {
          succeeded: 0,
          failed: 0,
          total: 0,
          succeededItems: [],
          failedItems: [],
          failedErrors: [],
          warningCount: 0,
          warningMessages: [],
        }
      }

      setIsLoading(true)
      const { signal } = executeOptions

      try {
        const results = await Promise.allSettled(
          items.map((item) => operation(item, { signal }))
        )

        const succeeded = results.filter((r) => r.status === 'fulfilled').length
        const failed = results.filter((r) => r.status === 'rejected').length
        const total = items.length
        const succeededItems = items.filter((_, index) => results[index]?.status === 'fulfilled')
        const failedItems = items.filter((_, index) => results[index]?.status === 'rejected')
        const failedErrors = results
          .filter((result): result is PromiseRejectedResult => result.status === 'rejected')
          .map((result) => result.reason)
        const warningResults = results.flatMap((result) => {
          if (result.status !== 'fulfilled') {
            return []
          }
          const value: unknown = result.value
          if (!isWarningAwareActionResult(value) || value.warning !== true) {
            return []
          }
          return [result.value]
        })
        const warningMessages = warningResults.flatMap((value) => {
          const message = getNonBlankJsonString(getWarningMessage?.(value))
          return message === undefined ? [] : [message]
        })

        const result = {
          succeeded,
          failed,
          total,
          succeededItems,
          failedItems,
          failedErrors,
          warningCount: warningResults.length,
          warningMessages,
        }

        if (signal?.aborted) {
          return result
        }

        // Show appropriate toast
        const toast = getToast?.(result) ?? (failed === 0
          ? result.warningCount > 0
            ? {
              title: getDefaultWarningSuccessTitle(messages, succeeded),
              color: 'warning' as const,
            }
            : {
              title: messages.success.replace('{count}', String(succeeded)),
              color: 'success' as const,
            }
          : succeeded === 0
            ? {
                title: messages.failure.replace('{count}', String(failed)),
                color: 'danger' as const,
              }
            : {
                title: messages.partial
                  .replace('{succeeded}', String(succeeded))
                  .replace('{failed}', String(failed)),
                color: 'warning' as const,
              })

        if (toast) {
          addToast(toast)
        }

        onComplete?.(result)
        return result
      } finally {
        if (mountedRef.current) {
          setIsLoading(false)
        }
      }
    },
    [operation, messages, getWarningMessage, getToast, onComplete]
  )

  return { execute, isLoading }
}

export default useBatchOperation
