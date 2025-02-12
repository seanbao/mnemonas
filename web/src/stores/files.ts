import { create } from 'zustand'

export interface FileItem {
  name: string
  path: string
  isDir: boolean
  size: number
  modTime: string
  etag?: string
}

export type ViewMode = 'list' | 'grid' | 'album'

interface FilesState {
  currentPath: string
  selectedFiles: Set<string>
  viewMode: ViewMode
  sortBy: 'name' | 'size' | 'modTime'
  sortOrder: 'asc' | 'desc'
  
  setCurrentPath: (path: string) => void
  selectFile: (path: string) => void
  deselectFile: (path: string) => void
  toggleFileSelection: (path: string) => void
  setSelection: (paths: string[]) => void
  selectAll: (paths: string[]) => void
  clearSelection: () => void
  setViewMode: (mode: ViewMode) => void
  setSortBy: (sortBy: 'name' | 'size' | 'modTime') => void
  toggleSortOrder: () => void
}

export const useFilesStore = create<FilesState>((set) => ({
  currentPath: '/',
  selectedFiles: new Set(),
  viewMode: 'list',
  sortBy: 'name',
  sortOrder: 'asc',
  
  setCurrentPath: (path) => set({ currentPath: path, selectedFiles: new Set() }),
  
  selectFile: (path) => set((state) => ({
    selectedFiles: new Set(state.selectedFiles).add(path)
  })),
  
  deselectFile: (path) => set((state) => {
    const newSet = new Set(state.selectedFiles)
    newSet.delete(path)
    return { selectedFiles: newSet }
  }),
  
  toggleFileSelection: (path) => set((state) => {
    const newSet = new Set(state.selectedFiles)
    if (newSet.has(path)) {
      newSet.delete(path)
    } else {
      newSet.add(path)
    }
    return { selectedFiles: newSet }
  }),

  setSelection: (paths) => set({ selectedFiles: new Set(paths) }),
  
  selectAll: (paths) => set({ selectedFiles: new Set(paths) }),
  
  clearSelection: () => set({ selectedFiles: new Set() }),
  
  setViewMode: (mode) => set({ viewMode: mode }),
  
  setSortBy: (sortBy) => set({ sortBy }),
  
  toggleSortOrder: () => set((state) => ({
    sortOrder: state.sortOrder === 'asc' ? 'desc' : 'asc'
  })),
}))
