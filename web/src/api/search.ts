/**
 * Search API
 * Global file search functionality
 */

import { authFetch } from './auth'

interface SearchApiError {
  code?: string
  message?: string
}

interface SearchApiResponse<T> {
  success?: boolean
  data?: T
  message?: string
  error?: string | SearchApiError
}

export interface SearchResult {
  name: string
  path: string
  isDir: boolean
  size: number
  modTime: string
  hash?: string
}

export interface SearchResponse {
  query: string
  results: SearchResult[]
  count: number
}

/**
 * Search for files matching the query
 * @param query - Search query (case-insensitive substring match)
 * @param limit - Maximum results to return (default 50, max 100)
 */
export async function searchFiles(query: string, limit: number = 50): Promise<SearchResponse> {
  const params = new URLSearchParams({ q: query })
  if (limit && limit !== 50) {
    params.set('limit', String(limit))
  }
  
  const response = await authFetch(`/api/v1/search?${params}`)
  
  if (!response.ok) {
    let message = 'Search failed'
    try {
      const body = await response.json() as SearchApiResponse<never>
      message = typeof body.error === 'string'
        ? body.error
        : body.error?.message || body.message || 'Search failed'
    } catch {
      // Fall back to a generic error when the backend did not return valid JSON.
    }
    throw new Error(message)
  }

  const result = await response.json() as SearchApiResponse<SearchResponse> & SearchResponse
  const data = result.data ?? result
  return {
    query: data.query,
    count: data.count,
    results: (data.results || []).map((item: any) => ({
      name: item.name,
      path: item.path,
      isDir: item.isDir ?? item.is_dir,
      size: item.size,
      modTime: item.modTime ?? item.mod_time,
      hash: item.hash,
    })),
  }
}
