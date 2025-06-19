import { renderHook, act } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useBatchOperation } from './useBatchOperation'

const mockAddToast = vi.fn()

vi.mock('@heroui/react', () => ({
  addToast: (...args: unknown[]) => mockAddToast(...args),
}))

describe('useBatchOperation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('returns failed errors and uses default failure toast', async () => {
    const operation = vi.fn(async (item: string) => {
      if (item === 'b') {
        throw new Error('boom')
      }
    })

    const { result } = renderHook(() => useBatchOperation({
      operation,
      messages: {
        success: '{count} 项成功',
        failure: '{count} 项失败',
        partial: '{succeeded} 项成功，{failed} 项失败',
      },
    }))

    let batchResult: Awaited<ReturnType<typeof result.current.execute>> | undefined
    await act(async () => {
      batchResult = await result.current.execute(['a', 'b'])
    })

    expect(batchResult).toMatchObject({
      succeeded: 1,
      failed: 1,
      failedItems: ['b'],
    })
    expect(batchResult?.failedErrors).toHaveLength(1)
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '1 项成功，1 项失败',
      color: 'warning',
    })
  })

  it('uses custom toast when all failures are unavailable', async () => {
    const operation = vi.fn(async () => {
      const error = new Error('filesystem unavailable') as Error & { status: number; code: string }
      error.status = 503
      error.code = 'SERVICE_UNAVAILABLE'
      throw error
    })

    const { result } = renderHook(() => useBatchOperation({
      operation,
      messages: {
        success: '{count} 项成功',
        failure: '{count} 项失败',
        partial: '{succeeded} 项成功，{failed} 项失败',
      },
      getToast: (batchResult) => {
        if (batchResult.succeeded === 0 && batchResult.failedErrors.every((error) => {
          return !!error && typeof error === 'object' && 'status' in error && error.status === 503
        })) {
          return {
            title: '批量操作暂不可用',
            description: '文件系统当前不可用，请稍后重试。',
            color: 'warning',
          }
        }

        return undefined
      },
    }))

    await act(async () => {
      await result.current.execute(['a', 'b'])
    })

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '批量操作暂不可用',
      description: '文件系统当前不可用，请稍后重试。',
      color: 'warning',
    })
  })

  it('uses a warning toast when successful operations return warnings', async () => {
    const operation = vi.fn(async () => ({
      warning: true,
      message: 'file restored with metadata warning',
    }))

    const { result } = renderHook(() => useBatchOperation({
      operation,
      messages: {
        success: '{count} 项成功',
        failure: '{count} 项失败',
        partial: '{succeeded} 项成功，{failed} 项失败',
      },
    }))

    let batchResult: Awaited<ReturnType<typeof result.current.execute>> | undefined
    await act(async () => {
      batchResult = await result.current.execute(['a'])
    })

    expect(batchResult).toMatchObject({
      succeeded: 1,
      failed: 0,
      warningCount: 1,
      warningMessages: ['file restored with metadata warning'],
    })
    expect(mockAddToast).toHaveBeenCalledWith({
      title: 'file restored with metadata warning',
      color: 'warning',
    })
  })
})