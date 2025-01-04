import { create } from 'zustand'

export type ClipboardOperation = 'copy' | 'cut'

export interface ClipboardState {
  // Paths of files in clipboard
  paths: string[]
  // Type of operation
  operation: ClipboardOperation | null
  // Source path (parent directory)
  sourcePath: string | null
  
  // Actions
  copy: (paths: string[], sourcePath: string) => void
  cut: (paths: string[], sourcePath: string) => void
  clear: () => void
  hasPaths: () => boolean
}

/**
 * Zustand store for managing clipboard state (copy/cut operations)
 * 
 * Usage:
 * ```tsx
 * const { paths, operation, copy, cut, clear } = useClipboardStore()
 * 
 * // Copy files
 * copy(['/path/to/file1', '/path/to/file2'], '/path/to')
 * 
 * // Cut files
 * cut(['/path/to/file1'], '/path/to')
 * 
 * // Clear clipboard
 * clear()
 * ```
 */
export const useClipboardStore = create<ClipboardState>((set, get) => ({
  paths: [],
  operation: null,
  sourcePath: null,
  
  copy: (paths, sourcePath) => set({
    paths,
    operation: 'copy',
    sourcePath,
  }),
  
  cut: (paths, sourcePath) => set({
    paths,
    operation: 'cut',
    sourcePath,
  }),
  
  clear: () => set({
    paths: [],
    operation: null,
    sourcePath: null,
  }),
  
  hasPaths: () => get().paths.length > 0,
}))

export default useClipboardStore
