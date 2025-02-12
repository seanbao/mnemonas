/**
 * Search API
 * Global file search functionality
 */

import { authFetch } from './auth'

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
    const error = await response.json()
    throw new Error(error.error || 'Search failed')
  }

  const result = await response.json()
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
