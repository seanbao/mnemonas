import { describe, expect, it } from 'vitest'
import { getFileQueryScopeKey, getFilesQueryKey } from './fileQueryKey'

describe('file query keys', () => {
  it('uses an anonymous root scope when no user is loaded', () => {
    expect(getFileQueryScopeKey(null)).toBe('anonymous:guest:/')
  })

  it('uses the normalized home directory for regular users', () => {
    expect(getFileQueryScopeKey({
      id: 'user-1',
      role: 'user',
      homeDir: 'photos/',
    })).toBe('user-1:user:/photos')
  })

  it('keeps invalid home directories isolated from real paths', () => {
    expect(getFileQueryScopeKey({
      id: 'user-2',
      role: 'user',
      homeDir: ' ',
    })).toBe('user-2:user:__invalid__')
  })

  it('builds stable file list query keys', () => {
    expect(getFilesQueryKey('user-1:user:/photos', '/photos/raw')).toEqual([
      'files',
      'user-1:user:/photos',
      '/photos/raw',
    ])
  })
})
