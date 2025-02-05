import { authFetch } from './auth'
import { encodePathForUrl, normalizePath } from '@/lib/utils'

const API_BASE = '/api/v1'
let batchCheckSupported: boolean | null = null

export interface Favorite {
  path: string
  user_id: string
  created_at: string
  note?: string
}

export interface FavoritesResponse {
  favorites: Favorite[]
  count: number
}

export interface CheckPathsResponse {
  favorites: Record<string, boolean>
}

export class FavoritesError extends Error {
  status: number
  
  constructor(message: string, status: number) {
    super(message)
    this.name = 'FavoritesError'
    this.status = status
  }
  
  get isNotFound(): boolean {
    return this.status === 404
  }
  
  get isConflict(): boolean {
    return this.status === 409
  }
}

/**
 * List user's favorites
 */
export async function listFavorites(): Promise<Favorite[]> {
  const response = await authFetch(`${API_BASE}/favorites`)
  
  if (!response.ok) {
    let message = '获取收藏列表失败'
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new FavoritesError(message, response.status)
  }
  
  const data: FavoritesResponse = await response.json()
  return data.favorites
}

/**
 * Add path to favorites
 */
export async function addFavorite(path: string, note = ''): Promise<Favorite> {
  const normalizedPath = normalizePath(path)
  const response = await authFetch(`${API_BASE}/favorites`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: normalizedPath, note }),
  })
  
  if (!response.ok) {
    let message = '添加收藏失败'
    if (response.status === 409) {
      message = '已经收藏过了'
    }
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new FavoritesError(message, response.status)
  }
  
  return response.json()
}

/**
 * Remove path from favorites
 */
export async function removeFavorite(path: string): Promise<void> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const response = await authFetch(`${API_BASE}/favorites${encodedPath}`, {
    method: 'DELETE',
  })
  
  if (!response.ok) {
    let message = '移除收藏失败'
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new FavoritesError(message, response.status)
  }
}

/**
 * Check if a path is favorited
 */
export async function checkFavorite(path: string): Promise<boolean> {
  const normalizedPath = normalizePath(path)
  const response = await authFetch(`${API_BASE}/favorites/check?path=${encodeURIComponent(normalizedPath)}`)
  
  if (!response.ok) {
    return false
  }
  
  const data = await response.json()
  return data.is_favorite
}

/**
 * Check multiple paths at once
 */
export async function checkFavorites(paths: string[]): Promise<Record<string, boolean>> {
  if (batchCheckSupported === false) {
    return Object.fromEntries(paths.map(p => [p, false]))
  }
  const normalizedMap = new Map<string, string>()
  const normalizedPaths = paths.map((path) => {
    const normalized = normalizePath(path)
    normalizedMap.set(normalized, path)
    return normalized
  })
  const response = await authFetch(`${API_BASE}/favorites/check-batch`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ paths: normalizedPaths }),
  })
  
  if (!response.ok) {
    if (response.status === 404) {
      batchCheckSupported = false
    }
    // Return all false on error
    return Object.fromEntries(paths.map(p => [p, false]))
  }
  batchCheckSupported = true
  const data: CheckPathsResponse = await response.json()
  const mapped: Record<string, boolean> = {}
  for (const [normalized, isFavorite] of Object.entries(data.favorites)) {
    const original = normalizedMap.get(normalized)
    if (original) {
      mapped[original] = isFavorite
    }
  }
  return mapped
}

/**
 * Update note for a favorite
 */
export async function updateFavoriteNote(path: string, note: string): Promise<void> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const response = await authFetch(`${API_BASE}/favorites${encodedPath}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ note }),
  })
  
  if (!response.ok) {
    let message = '更新备注失败'
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch { /* ignore */ }
    throw new FavoritesError(message, response.status)
  }
}

/**
 * Toggle favorite status
 */
export async function toggleFavorite(path: string, isFavorited: boolean): Promise<boolean> {
  if (isFavorited) {
    await removeFavorite(path)
    return false
  } else {
    await addFavorite(path)
    return true
  }
}
