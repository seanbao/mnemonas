import { describe, it, expect, beforeEach } from 'vitest'
import { useClipboardStore } from './clipboard'

describe('clipboardStore', () => {
  beforeEach(() => {
    // Reset store to initial state
    useClipboardStore.setState({
      paths: [],
      operation: null,
      sourcePath: null,
    })
  })

  describe('initial state', () => {
    it('has correct initial values', () => {
      const state = useClipboardStore.getState()
      expect(state.paths).toEqual([])
      expect(state.operation).toBeNull()
      expect(state.sourcePath).toBeNull()
    })

    it('hasPaths returns false when empty', () => {
      expect(useClipboardStore.getState().hasPaths()).toBe(false)
    })
  })

  describe('copy', () => {
    it('sets paths and operation to copy', () => {
      useClipboardStore.getState().copy(['/file1.txt', '/file2.txt'], '/documents')
      
      const state = useClipboardStore.getState()
      expect(state.paths).toEqual(['/file1.txt', '/file2.txt'])
      expect(state.operation).toBe('copy')
      expect(state.sourcePath).toBe('/documents')
    })

    it('overwrites previous clipboard content', () => {
      useClipboardStore.getState().copy(['/old.txt'], '/old')
      useClipboardStore.getState().copy(['/new.txt'], '/new')
      
      const state = useClipboardStore.getState()
      expect(state.paths).toEqual(['/new.txt'])
      expect(state.sourcePath).toBe('/new')
    })

    it('hasPaths returns true after copy', () => {
      useClipboardStore.getState().copy(['/file.txt'], '/')
      expect(useClipboardStore.getState().hasPaths()).toBe(true)
    })

    it('handles single file', () => {
      useClipboardStore.getState().copy(['/single.txt'], '/root')
      
      const state = useClipboardStore.getState()
      expect(state.paths).toHaveLength(1)
      expect(state.paths[0]).toBe('/single.txt')
    })

    it('handles multiple files', () => {
      const files = ['/a.txt', '/b.txt', '/c.txt', '/d.txt']
      useClipboardStore.getState().copy(files, '/folder')
      
      expect(useClipboardStore.getState().paths).toEqual(files)
    })
  })

  describe('cut', () => {
    it('sets paths and operation to cut', () => {
      useClipboardStore.getState().cut(['/file.txt'], '/documents')
      
      const state = useClipboardStore.getState()
      expect(state.paths).toEqual(['/file.txt'])
      expect(state.operation).toBe('cut')
      expect(state.sourcePath).toBe('/documents')
    })

    it('overwrites copy operation', () => {
      useClipboardStore.getState().copy(['/copied.txt'], '/src')
      useClipboardStore.getState().cut(['/cut.txt'], '/dest')
      
      const state = useClipboardStore.getState()
      expect(state.operation).toBe('cut')
      expect(state.paths).toEqual(['/cut.txt'])
    })

    it('hasPaths returns true after cut', () => {
      useClipboardStore.getState().cut(['/file.txt'], '/')
      expect(useClipboardStore.getState().hasPaths()).toBe(true)
    })
  })

  describe('clear', () => {
    it('clears all clipboard data', () => {
      useClipboardStore.getState().copy(['/file.txt'], '/folder')
      useClipboardStore.getState().clear()
      
      const state = useClipboardStore.getState()
      expect(state.paths).toEqual([])
      expect(state.operation).toBeNull()
      expect(state.sourcePath).toBeNull()
    })

    it('hasPaths returns false after clear', () => {
      useClipboardStore.getState().copy(['/file.txt'], '/')
      expect(useClipboardStore.getState().hasPaths()).toBe(true)
      
      useClipboardStore.getState().clear()
      expect(useClipboardStore.getState().hasPaths()).toBe(false)
    })

    it('can be called multiple times safely', () => {
      useClipboardStore.getState().clear()
      useClipboardStore.getState().clear()
      
      const state = useClipboardStore.getState()
      expect(state.paths).toEqual([])
    })
  })

  describe('hasPaths', () => {
    it('returns false for empty array', () => {
      expect(useClipboardStore.getState().hasPaths()).toBe(false)
    })

    it('returns true for non-empty array', () => {
      useClipboardStore.getState().copy(['/file.txt'], '/')
      expect(useClipboardStore.getState().hasPaths()).toBe(true)
    })

    it('returns true even with empty path strings', () => {
      useClipboardStore.setState({ paths: [''], operation: 'copy', sourcePath: '/' })
      expect(useClipboardStore.getState().hasPaths()).toBe(true)
    })
  })

  describe('edge cases', () => {
    it('handles root path as source', () => {
      useClipboardStore.getState().copy(['/file.txt'], '/')
      expect(useClipboardStore.getState().sourcePath).toBe('/')
    })

    it('handles paths with special characters', () => {
      const specialPaths = ['/文件.txt', '/file with spaces.txt', '/file-name_123.txt']
      useClipboardStore.getState().copy(specialPaths, '/特殊文件夹')
      
      const state = useClipboardStore.getState()
      expect(state.paths).toEqual(specialPaths)
      expect(state.sourcePath).toBe('/特殊文件夹')
    })

    it('handles deeply nested paths', () => {
      const deepPath = '/a/b/c/d/e/f/g/h/i/j/file.txt'
      useClipboardStore.getState().copy([deepPath], '/a/b/c/d/e/f/g/h/i/j')
      
      expect(useClipboardStore.getState().paths[0]).toBe(deepPath)
    })
  })
})
