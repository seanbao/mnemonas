import { QueryClient } from '@tanstack/react-query'
import { shouldRetryQuery } from '@/lib/queryRetry'

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 1000 * 60,
      retry: shouldRetryQuery,
      retryDelay: (attemptIndex) => Math.min(1000 * 2 ** attemptIndex, 30000),
    },
    mutations: {
      retry: false,
    },
  },
})