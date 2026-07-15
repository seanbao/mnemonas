import {
  type DirectoryAccessRole,
  type DirectoryAccessRule,
  type DirectoryQuota,
} from '@/api/settings'
import { formatBytes, hasControlCharacter, parseByteSize } from '@/lib/utils'

export const logicalPathInputErrorDescription = '路径必须是站内绝对路径，且不能包含反斜杠、?、#、控制字符、. 或 .. 路径段。'

export function normalizeLogicalPathInput(value: string): string | null {
  const trimmed = value.trim()
  if (!trimmed.startsWith('/') || /[\\?#]/.test(trimmed) || hasControlCharacter(trimmed)) {
    return null
  }
  if (trimmed.split('/').some((segment) => segment === '.' || segment === '..')) {
    return null
  }
  const collapsed = trimmed.replace(/\/+/g, '/')
  return collapsed === '/' ? '/' : collapsed.replace(/\/+$/, '')
}

export function formatLogicalPathLineToken(path: string): string {
  const normalizedPath = normalizeLogicalPathInput(path) ?? path.trim()
  if (!/\s|"/.test(normalizedPath)) {
    return normalizedPath
  }
  return `"${normalizedPath.replaceAll('"', '\\"')}"`
}

function parseLogicalPathLineHead(
  line: string,
  lineNumber: number,
): { path: string; rest: string; error?: string } {
  const trimmed = line.trim()
  if (!trimmed) {
    return { path: '', rest: '' }
  }

  if (!trimmed.startsWith('"')) {
    const [pathToken = '', ...restTokens] = trimmed.split(/\s+/)
    return { path: pathToken, rest: restTokens.join(' ') }
  }

  let pathToken = ''
  let escaping = false
  for (let index = 1; index < trimmed.length; index += 1) {
    const char = trimmed[index]
    if (escaping) {
      pathToken += char === '"' ? '"' : `\\${char}`
      escaping = false
      continue
    }
    if (char === '\\') {
      escaping = true
      continue
    }
    if (char === '"') {
      return {
        path: pathToken,
        rest: trimmed.slice(index + 1).trim(),
      }
    }
    pathToken += char
  }

  if (escaping) {
    pathToken += '\\'
  }
  return { path: pathToken, rest: '', error: `第 ${lineNumber} 行路径引号未闭合` }
}

export function formatDirectoryQuotaLines(quotas: DirectoryQuota[] | undefined): string {
  return (quotas ?? [])
    .map((quota) => `${formatLogicalPathLineToken(quota.path)} ${formatBytes(quota.quota_bytes)}`)
    .join('\n')
}

export function parseDirectoryQuotaLines(value: string): { quotas: DirectoryQuota[]; error?: string } {
  const lines = value.split('\n')
  const quotas: DirectoryQuota[] = []
  const seenPaths = new Set<string>()

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index].trim()
    if (!line) {
      continue
    }

    const parsedLine = parseLogicalPathLineHead(line, index + 1)
    if (parsedLine.error) {
      return { quotas: [], error: parsedLine.error }
    }
    if (!parsedLine.rest) {
      return { quotas: [], error: `第 ${index + 1} 行需要填写路径和容量` }
    }

    const quotaPath = normalizeLogicalPathInput(parsedLine.path)
    if (!quotaPath) {
      return { quotas: [], error: `第 ${index + 1} 行路径无效` }
    }
    if (seenPaths.has(quotaPath)) {
      return { quotas: [], error: `第 ${index + 1} 行路径重复` }
    }

    let quotaBytes: number
    try {
      quotaBytes = parseByteSize(parsedLine.rest)
    } catch {
      return { quotas: [], error: `第 ${index + 1} 行容量格式无效` }
    }
    if (!Number.isSafeInteger(quotaBytes) || quotaBytes <= 0) {
      return { quotas: [], error: `第 ${index + 1} 行容量必须是大于 0 且不超过安全范围的整数` }
    }

    seenPaths.add(quotaPath)
    quotas.push({ path: quotaPath, quota_bytes: quotaBytes })
  }

  return { quotas }
}

const accessRulePrincipalPattern = /^[A-Za-z0-9._-]+$/
const accessRuleKeys = new Set([
  'read_users',
  'write_users',
  'read_groups',
  'write_groups',
  'read_roles',
  'write_roles',
])

function formatAccessRuleList(label: string, values: string[] | undefined): string {
  return values && values.length > 0 ? `${label}=${values.join(',')}` : ''
}

export function formatDirectoryAccessRuleLines(rules: DirectoryAccessRule[] | undefined): string {
  return (rules ?? [])
    .map((rule) => [
      formatLogicalPathLineToken(rule.path),
      formatAccessRuleList('read_users', rule.read_users),
      formatAccessRuleList('write_users', rule.write_users),
      formatAccessRuleList('read_groups', rule.read_groups),
      formatAccessRuleList('write_groups', rule.write_groups),
      formatAccessRuleList('read_roles', rule.read_roles),
      formatAccessRuleList('write_roles', rule.write_roles),
    ].filter(Boolean).join(' '))
    .join('\n')
}

export function parseAccessRuleValues(
  value: string,
  lineNumber: number,
  field: string,
): { values: string[]; error?: string } {
  const values = value
    .split(',')
    .map((entry) => entry.trim().toLowerCase())
    .filter(Boolean)
  const seen = new Set<string>()
  const normalized: string[] = []

  if (values.length === 0) {
    return { values: [], error: `第 ${lineNumber} 行 ${field} 不能为空` }
  }

  for (const item of values) {
    if (field.endsWith('_roles')) {
      if (item !== 'admin' && item !== 'user' && item !== 'guest') {
        return { values: [], error: `第 ${lineNumber} 行角色只能是 admin、user 或 guest` }
      }
    } else if (!accessRulePrincipalPattern.test(item)) {
      return { values: [], error: `第 ${lineNumber} 行主体只能包含字母、数字、点、短横线和下划线` }
    }
    if (!seen.has(item)) {
      seen.add(item)
      normalized.push(item)
    }
  }

  normalized.sort()
  return { values: normalized }
}

export function parseDirectoryAccessRuleLines(
  value: string,
): { rules: DirectoryAccessRule[]; error?: string } {
  const lines = value.split('\n')
  const rules: DirectoryAccessRule[] = []
  const seenPaths = new Set<string>()

  for (let index = 0; index < lines.length; index += 1) {
    const lineNumber = index + 1
    const line = lines[index].trim()
    if (!line) {
      continue
    }

    const parsedLine = parseLogicalPathLineHead(line, lineNumber)
    if (parsedLine.error) {
      return { rules: [], error: parsedLine.error }
    }
    const rulePath = normalizeLogicalPathInput(parsedLine.path)
    if (!rulePath) {
      return { rules: [], error: `第 ${lineNumber} 行路径无效` }
    }
    if (seenPaths.has(rulePath)) {
      return { rules: [], error: `第 ${lineNumber} 行路径重复` }
    }

    const rule: DirectoryAccessRule = { path: rulePath }
    const tokens = parsedLine.rest ? parsedLine.rest.split(/\s+/) : []
    for (const token of tokens) {
      const separator = token.indexOf('=')
      if (separator <= 0 || separator === token.length - 1) {
        return { rules: [], error: `第 ${lineNumber} 行规则格式无效` }
      }
      const key = token.slice(0, separator)
      const rawValue = token.slice(separator + 1)
      if (!accessRuleKeys.has(key)) {
        return { rules: [], error: `第 ${lineNumber} 行字段 ${key} 不支持` }
      }
      const parsed = parseAccessRuleValues(rawValue, lineNumber, key)
      if (parsed.error) {
        return { rules: [], error: parsed.error }
      }
      switch (key) {
        case 'read_users':
          rule.read_users = parsed.values
          break
        case 'write_users':
          rule.write_users = parsed.values
          break
        case 'read_groups':
          rule.read_groups = parsed.values
          break
        case 'write_groups':
          rule.write_groups = parsed.values
          break
        case 'read_roles':
          rule.read_roles = parsed.values as DirectoryAccessRole[]
          break
        case 'write_roles':
          rule.write_roles = parsed.values as DirectoryAccessRole[]
          break
      }
    }

    const hasPrincipals = Boolean(
      rule.read_users?.length
      || rule.write_users?.length
      || rule.read_groups?.length
      || rule.write_groups?.length
      || rule.read_roles?.length
      || rule.write_roles?.length
    )
    if (!hasPrincipals) {
      return { rules: [], error: `第 ${lineNumber} 行至少需要一个 read 或 write 主体` }
    }

    seenPaths.add(rulePath)
    rules.push(rule)
  }

  return { rules }
}

export function serializeDirectoryPolicies(
  quotas: DirectoryQuota[],
  rules: DirectoryAccessRule[],
): string {
  return JSON.stringify({ quotas, rules })
}
