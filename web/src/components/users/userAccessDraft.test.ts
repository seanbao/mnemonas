import { describe, expect, it } from 'vitest'
import {
  formatDirectoryAccessRuleLines,
  formatDirectoryQuotaLines,
  formatLogicalPathLineToken,
  normalizeLogicalPathInput,
  parseDirectoryAccessRuleLines,
  parseDirectoryQuotaLines,
} from './userAccessDraft'

describe('user access draft helpers', () => {
  it('round-trips quota paths with spaces and literal quotes', () => {
    const source = [
      { path: '/Family Photos', quota_bytes: 1073741824 },
      { path: '/Family "Archive"', quota_bytes: 536870912 },
    ]
    const formatted = formatDirectoryQuotaLines(source)

    expect(formatted).toBe('"/Family Photos" 1 GB\n"/Family \\"Archive\\"" 512 MB')
    expect(parseDirectoryQuotaLines(formatted)).toEqual({ quotas: source })
  })

  it('normalizes repeated and trailing path separators', () => {
    expect(normalizeLogicalPathInput('/team//public/')).toBe('/team/public')
    expect(parseDirectoryQuotaLines('/team// 2 GB')).toEqual({
      quotas: [{ path: '/team', quota_bytes: 2147483648 }],
    })
    expect(parseDirectoryAccessRuleLines('/team//public/ read_groups=family')).toEqual({
      rules: [{ path: '/team/public', read_groups: ['family'] }],
    })
  })

  it.each([
    ['relative path', 'team/private'],
    ['backslash', '/team\\private'],
    ['query marker', '/team?private'],
    ['fragment marker', '/team#private'],
    ['C0 control character', '/team\u001fprivate'],
    ['C1 control character', '/team\u0081private'],
    ['current-directory segment', '/team/./private'],
    ['parent-directory segment', '/team/../private'],
  ])('rejects a logical path containing %s', (_label, value) => {
    expect(normalizeLogicalPathInput(value)).toBeNull()
  })

  it.each([
    ['relative path', 'team 1 GB', '第 1 行路径无效'],
    ['dot segment', '/team/../private 1 GB', '第 1 行路径无效'],
    ['Unicode control character', '/team\u0081private 1 GB', '第 1 行路径无效'],
    ['unclosed quote', '"/Family Photos 1 GB', '第 1 行路径引号未闭合'],
    ['unsafe size', '/team 9007199254740992 B', '第 1 行容量必须是大于 0 且不超过安全范围的整数'],
  ])('rejects an invalid quota %s', (_label, value, error) => {
    expect(parseDirectoryQuotaLines(value)).toEqual({ quotas: [], error })
  })

  it('rejects quota paths that become duplicates after normalization', () => {
    expect(parseDirectoryQuotaLines('/team 1 GB\n/team// 2 GB')).toEqual({
      quotas: [],
      error: '第 2 行路径重复',
    })
  })

  it('accepts IEC quota units without changing the binary byte value', () => {
    expect(parseDirectoryQuotaLines('/versions 100 MiB')).toEqual({
      quotas: [{ path: '/versions', quota_bytes: 104857600 }],
    })
  })

  it('normalizes and sorts access rule principals', () => {
    expect(parseDirectoryAccessRuleLines('/team read_users=Bob,alice,bob write_roles=admin')).toEqual({
      rules: [{
        path: '/team',
        read_users: ['alice', 'bob'],
        write_roles: ['admin'],
      }],
    })
  })

  it('round-trips structured access rules with paths containing spaces and quotes', () => {
    const source = [{
      path: '/Family "Photos"',
      read_groups: ['family'],
      write_groups: ['editors'],
    }]
    expect(parseDirectoryAccessRuleLines(formatDirectoryAccessRuleLines(source))).toEqual({ rules: source })
  })

  it('quotes structured paths itself and rejects line-syntax quotes entered as path data', () => {
    expect(formatLogicalPathLineToken('/Family Photos')).toBe('"/Family Photos"')
    expect(formatLogicalPathLineToken('/Family "Photos"')).toBe('"/Family \\"Photos\\""')
    const formatted = `${formatLogicalPathLineToken('"/Family Photos')} read_roles=user`
    expect(parseDirectoryAccessRuleLines(formatted)).toEqual({
      rules: [],
      error: '第 1 行路径无效',
    })
  })

  it.each([
    ['relative path', 'team read_roles=user', '第 1 行路径无效'],
    ['dot segment', '/team/./private read_roles=user', '第 1 行路径无效'],
    ['Unicode control character', '/team\u0081private read_roles=user', '第 1 行路径无效'],
    ['unclosed quoted path', '"/Family Photos read_roles=user', '第 1 行路径引号未闭合'],
    ['invalid role', '/team read_roles=owner', '第 1 行角色只能是 admin、user 或 guest'],
    ['missing principal', '/team', '第 1 行至少需要一个 read 或 write 主体'],
    ['duplicate path', '/team read_roles=user\n/team write_roles=admin', '第 2 行路径重复'],
  ])('rejects an invalid access rule %s', (_label, value, error) => {
    expect(parseDirectoryAccessRuleLines(value)).toEqual({ rules: [], error })
  })
})
