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

  it('returns an empty result without calling the operation or showing a toast', async () => {
    const operation = vi.fn(async () => undefined)
    const onComplete = vi.fn()

    const { result } = renderHook(() => useBatchOperation({
      operation,
      messages: {
        success: '{count} 项成功',
        failure: '{count} 项失败',
        partial: '{succeeded} 项成功，{failed} 项失败',
      },
      onComplete,
    }))

    let batchResult: Awaited<ReturnType<typeof result.current.execute>> | undefined
    await act(async () => {
      batchResult = await result.current.execute([])
    })

    expect(batchResult).toEqual({
      succeeded: 0,
      failed: 0,
      total: 0,
      succeededItems: [],
      failedItems: [],
      failedErrors: [],
      warningCount: 0,
      warningMessages: [],
    })
    expect(operation).not.toHaveBeenCalled()
    expect(onComplete).not.toHaveBeenCalled()
    expect(mockAddToast).not.toHaveBeenCalled()
    expect(result.current.isLoading).toBe(false)
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

  it('passes an abort signal to each operation', async () => {
    const controller = new AbortController()
    const operation = vi.fn(async () => undefined)

    const { result } = renderHook(() => useBatchOperation({
      operation,
      messages: {
        success: '{count} 项成功',
        failure: '{count} 项失败',
        partial: '{succeeded} 项成功，{failed} 项失败',
      },
    }))

    await act(async () => {
      await result.current.execute(['a', 'b'], { signal: controller.signal })
    })

    expect(operation).toHaveBeenCalledWith('a', { signal: controller.signal })
    expect(operation).toHaveBeenCalledWith('b', { signal: controller.signal })
  })

  it('suppresses toast and completion callbacks after abort', async () => {
    const controller = new AbortController()
    const onComplete = vi.fn()
    const operation = vi.fn(async () => {
      controller.abort()
      throw new DOMException('batch aborted', 'AbortError')
    })

    const { result } = renderHook(() => useBatchOperation({
      operation,
      messages: {
        success: '{count} 项成功',
        failure: '{count} 项失败',
        partial: '{succeeded} 项成功，{failed} 项失败',
      },
      onComplete,
    }))

    await act(async () => {
      await result.current.execute(['a'], { signal: controller.signal })
    })

    expect(mockAddToast).not.toHaveBeenCalled()
    expect(onComplete).not.toHaveBeenCalled()
    expect(result.current.isLoading).toBe(false)
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

  it('uses a generic warning toast without exposing backend warning messages', async () => {
    const operation = vi.fn(async () => ({
      warning: true,
      message: 'token=backend-secret',
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
      warningMessages: [],
    })
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '1 项成功，但存在警告',
      color: 'warning',
    })
  })

  it('preserves warning messages only through an explicit safe mapper', async () => {
    const operation = vi.fn(async () => ({
      warning: true,
      message: 'item already missing',
    }))

    const { result } = renderHook(() => useBatchOperation({
      operation,
      messages: {
        success: '{count} 项成功',
        failure: '{count} 项失败',
        partial: '{succeeded} 项成功，{failed} 项失败',
      },
      getWarningMessage: (operationResult) => {
        return operationResult.message === 'item already missing' ? '项目已不存在，已同步更新' : undefined
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
      warningMessages: ['项目已不存在，已同步更新'],
    })
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '1 项成功，但存在警告',
      color: 'warning',
    })
  })

  it('uses the default success toast when every operation succeeds', async () => {
    const operation = vi.fn(async () => undefined)

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
      succeeded: 2,
      failed: 0,
      warningCount: 0,
      warningMessages: [],
    })
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '2 项成功',
      color: 'success',
    })
  })

  it('counts warning results without a message', async () => {
    const operation = vi.fn(async () => ({
      warning: true,
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
      warningMessages: [],
    })
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '1 项成功，但存在警告',
      color: 'warning',
    })
  })

  it('counts warning results with blank messages without exposing them', async () => {
    const operation = vi.fn(async () => ({
      warning: true,
      message: '   ',
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
      warningMessages: [],
    })
    expect(mockAddToast).toHaveBeenCalledWith({
      title: '1 项成功，但存在警告',
      color: 'warning',
    })
  })
})
