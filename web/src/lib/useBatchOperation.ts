import { useCallback, useState } from 'react'
import { addToast } from '@heroui/react'

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
}

export interface BatchOperationToast {
  title: string
  description?: string
  color: 'success' | 'warning' | 'danger'
}

/**
 * Hook options
 */
export interface UseBatchOperationOptions<T, R = void> {
  /** The async operation to perform on each item */
  operation: (item: T) => Promise<R>
  /** Message templates for toast notifications */
  messages: {
    /** Message when all succeed. Use {count} placeholder for count */
    success: string
    /** Message when all fail. Use {count} placeholder for count */
    failure: string
    /** Message when partial success. Use {succeeded} and {failed} placeholders */
    partial: string
  }
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
 *     success: '{count} 项删除成功',
 *     failure: '{count} 项删除失败',
 *     partial: '{succeeded} 项删除成功，{failed} 项失败',
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
  const { operation, messages, getToast, onComplete } = options
  const [isLoading, setIsLoading] = useState(false)

  const execute = useCallback(
    async (items: T[]): Promise<BatchOperationResult> => {
      if (items.length === 0) {
        return {
          succeeded: 0,
          failed: 0,
          total: 0,
          succeededItems: [],
          failedItems: [],
          failedErrors: [],
        }
      }

      setIsLoading(true)

      try {
        const results = await Promise.allSettled(
          items.map((item) => operation(item))
        )

        const succeeded = results.filter((r) => r.status === 'fulfilled').length
        const failed = results.filter((r) => r.status === 'rejected').length
        const total = items.length
        const succeededItems = items.filter((_, index) => results[index]?.status === 'fulfilled')
        const failedItems = items.filter((_, index) => results[index]?.status === 'rejected')
        const failedErrors = results
          .filter((result): result is PromiseRejectedResult => result.status === 'rejected')
          .map((result) => result.reason)

        const result = { succeeded, failed, total, succeededItems, failedItems, failedErrors }

        // Show appropriate toast
        const toast = getToast?.(result) ?? (failed === 0
          ? {
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
        setIsLoading(false)
      }
    },
    [operation, messages, getToast, onComplete]
  )

  return { execute, isLoading }
}

export default useBatchOperation
