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
  const { operation, messages, onComplete } = options
  const [isLoading, setIsLoading] = useState(false)

  const execute = useCallback(
    async (items: T[]): Promise<BatchOperationResult> => {
      if (items.length === 0) {
        return { succeeded: 0, failed: 0, total: 0 }
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

        // Show appropriate toast
        if (failed === 0) {
          addToast({
            title: messages.success.replace('{count}', String(succeeded)),
            color: 'success',
          })
        } else if (succeeded === 0) {
          addToast({
            title: messages.failure.replace('{count}', String(failed)),
            color: 'danger',
          })
        } else {
          addToast({
            title: messages.partial
              .replace('{succeeded}', String(succeeded))
              .replace('{failed}', String(failed)),
            color: 'warning',
          })
        }

        const result = { succeeded, failed, total, succeededItems, failedItems }
        onComplete?.(result)
        return result
      } finally {
        setIsLoading(false)
      }
    },
    [operation, messages, onComplete]
  )

  return { execute, isLoading }
}

export default useBatchOperation
