import { describe, it, expect, beforeEach } from 'vitest'
import { useFilesStore } from './files'

describe('filesStore', () => {
  beforeEach(() => {
    // Reset store to initial state
    useFilesStore.setState({
      currentPath: '/',
      selectedFiles: new Set(),
      viewMode: 'list',
      sortBy: 'name',
      sortOrder: 'asc',
    })
  })

  describe('initial state', () => {
    it('has correct initial values', () => {
      const state = useFilesStore.getState()
      expect(state.currentPath).toBe('/')
      expect(state.selectedFiles.size).toBe(0)
      expect(state.viewMode).toBe('list')
      expect(state.sortBy).toBe('name')
      expect(state.sortOrder).toBe('asc')
    })
  })

  describe('setCurrentPath', () => {
    it('updates current path', () => {
      useFilesStore.getState().setCurrentPath('/documents')
      expect(useFilesStore.getState().currentPath).toBe('/documents')
    })

    it('clears selection when path changes', () => {
      // First select some files
      useFilesStore.getState().selectFile('/file1.txt')
      useFilesStore.getState().selectFile('/file2.txt')
      expect(useFilesStore.getState().selectedFiles.size).toBe(2)

      // Change path
      useFilesStore.getState().setCurrentPath('/other')
      
      // Selection should be cleared
      expect(useFilesStore.getState().selectedFiles.size).toBe(0)
    })
  })

  describe('file selection', () => {
    describe('selectFile', () => {
      it('adds file to selection', () => {
        useFilesStore.getState().selectFile('/test.txt')
        expect(useFilesStore.getState().selectedFiles.has('/test.txt')).toBe(true)
      })

      it('can select multiple files', () => {
        useFilesStore.getState().selectFile('/file1.txt')
        useFilesStore.getState().selectFile('/file2.txt')
        expect(useFilesStore.getState().selectedFiles.size).toBe(2)
      })
    })

    describe('deselectFile', () => {
      it('removes file from selection', () => {
        useFilesStore.getState().selectFile('/test.txt')
        useFilesStore.getState().deselectFile('/test.txt')
        expect(useFilesStore.getState().selectedFiles.has('/test.txt')).toBe(false)
      })

      it('does nothing if file not selected', () => {
        useFilesStore.getState().deselectFile('/nonexistent.txt')
        expect(useFilesStore.getState().selectedFiles.size).toBe(0)
      })
    })

    describe('toggleFileSelection', () => {
      it('selects unselected file', () => {
        useFilesStore.getState().toggleFileSelection('/test.txt')
        expect(useFilesStore.getState().selectedFiles.has('/test.txt')).toBe(true)
      })

      it('deselects selected file', () => {
        useFilesStore.getState().selectFile('/test.txt')
        useFilesStore.getState().toggleFileSelection('/test.txt')
        expect(useFilesStore.getState().selectedFiles.has('/test.txt')).toBe(false)
      })
    })

    describe('selectAll', () => {
      it('selects all provided paths', () => {
        useFilesStore.getState().selectAll(['/a.txt', '/b.txt', '/c.txt'])
        const selected = useFilesStore.getState().selectedFiles
        expect(selected.size).toBe(3)
        expect(selected.has('/a.txt')).toBe(true)
        expect(selected.has('/b.txt')).toBe(true)
        expect(selected.has('/c.txt')).toBe(true)
      })

      it('replaces existing selection', () => {
        useFilesStore.getState().selectFile('/old.txt')
        useFilesStore.getState().selectAll(['/new.txt'])
        expect(useFilesStore.getState().selectedFiles.has('/old.txt')).toBe(false)
        expect(useFilesStore.getState().selectedFiles.has('/new.txt')).toBe(true)
      })
    })

    describe('clearSelection', () => {
      it('clears all selected files', () => {
        useFilesStore.getState().selectFile('/a.txt')
        useFilesStore.getState().selectFile('/b.txt')
        useFilesStore.getState().clearSelection()
        expect(useFilesStore.getState().selectedFiles.size).toBe(0)
      })
    })
  })

  describe('view mode', () => {
    it('sets view mode to list', () => {
      useFilesStore.getState().setViewMode('list')
      expect(useFilesStore.getState().viewMode).toBe('list')
    })

    it('sets view mode to grid', () => {
      useFilesStore.getState().setViewMode('grid')
      expect(useFilesStore.getState().viewMode).toBe('grid')
    })

    it('sets view mode to album', () => {
      useFilesStore.getState().setViewMode('album')
      expect(useFilesStore.getState().viewMode).toBe('album')
    })
  })

  describe('sorting', () => {
    describe('setSortBy', () => {
      it('sets sort by name', () => {
        useFilesStore.getState().setSortBy('name')
        expect(useFilesStore.getState().sortBy).toBe('name')
      })

      it('sets sort by size', () => {
        useFilesStore.getState().setSortBy('size')
        expect(useFilesStore.getState().sortBy).toBe('size')
      })

      it('sets sort by modTime', () => {
        useFilesStore.getState().setSortBy('modTime')
        expect(useFilesStore.getState().sortBy).toBe('modTime')
      })
    })

    describe('toggleSortOrder', () => {
      it('toggles from asc to desc', () => {
        expect(useFilesStore.getState().sortOrder).toBe('asc')
        useFilesStore.getState().toggleSortOrder()
        expect(useFilesStore.getState().sortOrder).toBe('desc')
      })

      it('toggles from desc to asc', () => {
        useFilesStore.setState({ sortOrder: 'desc' })
        useFilesStore.getState().toggleSortOrder()
        expect(useFilesStore.getState().sortOrder).toBe('asc')
      })
    })
  })
})
