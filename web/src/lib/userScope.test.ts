import { describe, expect, it } from 'vitest'
import { getInvalidHomeDirDescription, resolveUserHomeScope } from './userScope'

describe('resolveUserHomeScope', () => {
  it('treats admin users as unscoped root access', () => {
    expect(resolveUserHomeScope({ role: 'admin', homeDir: '/' })).toEqual({
      rootPath: '/',
      scopedHomeDir: null,
      hasInvalidHomeDir: false,
    })
  })

  it('returns the normalized home directory for non-admin users', () => {
    expect(resolveUserHomeScope({ role: 'user', homeDir: ' /tester/docs/ ' })).toEqual({
      rootPath: '/tester/docs',
      scopedHomeDir: '/tester/docs',
      hasInvalidHomeDir: false,
    })
  })

  it('treats blank non-admin home directories as invalid', () => {
    expect(resolveUserHomeScope({ role: 'user', homeDir: '   ' })).toEqual({
      rootPath: null,
      scopedHomeDir: null,
      hasInvalidHomeDir: true,
    })
  })

  it('treats traversal-like non-admin home directories as invalid', () => {
    expect(resolveUserHomeScope({ role: 'user', homeDir: '../secret' })).toEqual({
      rootPath: null,
      scopedHomeDir: null,
      hasInvalidHomeDir: true,
    })
  })
})

describe('getInvalidHomeDirDescription', () => {
  it('describes the blocked action', () => {
    expect(getInvalidHomeDirDescription('浏览文件')).toBe('当前账户未配置有效的主目录，无法浏览文件。请联系管理员修复账户 home_dir。')
  })
})