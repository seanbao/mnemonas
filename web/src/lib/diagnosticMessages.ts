const REDACTED_DIAGNOSTIC_SECRET = '<redacted>'
const diagnosticNamePartPattern = String.raw`(?:[A-Za-z0-9_.-]|%[0-9A-Fa-f]{2})*`
const diagnosticKeySeparatorPattern = String.raw`(?:[_-]|%5f|%2d)?`
const sensitiveDiagnosticNamePattern = String.raw`(?:${diagnosticNamePartPattern}(?:password|passwd|secret|token|credential|access${diagnosticKeySeparatorPattern}key|secret${diagnosticKeySeparatorPattern}key|api${diagnosticKeySeparatorPattern}key|authorization|signature)${diagnosticNamePartPattern}|pass|auth|sig|user|username)`
const sensitiveDiagnosticHeaderNamePattern = String.raw`(?:${diagnosticNamePartPattern}(?:password|passwd|secret|token|credential|access${diagnosticKeySeparatorPattern}key|secret${diagnosticKeySeparatorPattern}key|api${diagnosticKeySeparatorPattern}key|signature)${diagnosticNamePartPattern}|pass|sig|user|username)`

const diagnosticURLUserinfoPattern = /\b([a-z][a-z0-9+.-]*:\/\/)([^/@\s]+)@/giu
const diagnosticDoubleQuotedAssignmentPattern = new RegExp(String.raw`(^|[\s?&;,:/\\])(-{0,2}${sensitiveDiagnosticNamePattern}=)"([^"]*)"`, 'giu')
const diagnosticSingleQuotedAssignmentPattern = new RegExp(String.raw`(^|[\s?&;,:/\\])(-{0,2}${sensitiveDiagnosticNamePattern}=)'([^']*)'`, 'giu')
const diagnosticAssignmentPattern = new RegExp(String.raw`(^|[\s?&;,:/\\])(-{0,2}${sensitiveDiagnosticNamePattern}=)([^\s?&;,"'\\]+)`, 'giu')
const diagnosticDoubleQuotedKVPattern = new RegExp(String.raw`(^|[{\s,;])("${sensitiveDiagnosticNamePattern}"\s*:\s*)"([^"]*)"`, 'giu')
const diagnosticSingleQuotedKVPattern = new RegExp(String.raw`(^|[{\s,;])('${sensitiveDiagnosticNamePattern}'\s*:\s*)'([^']*)'`, 'giu')
const diagnosticDoubleQuotedAuthPattern = /\b((?:proxy-)?authorization\s*:\s*(?:bearer|basic|token)\s+)"([^"]*)"/giu
const diagnosticSingleQuotedAuthPattern = /\b((?:proxy-)?authorization\s*:\s*(?:bearer|basic|token)\s+)'([^']*)'/giu
const diagnosticAuthorizationPattern = /\b((?:proxy-)?authorization\s*:\s*(?:bearer|basic|token)\s+)([^\s,;"']+)/giu
const diagnosticDoubleQuotedHeaderPattern = new RegExp(String.raw`\b(${sensitiveDiagnosticHeaderNamePattern}\s*:\s*)"([^"]*)"`, 'giu')
const diagnosticSingleQuotedHeaderPattern = new RegExp(String.raw`\b(${sensitiveDiagnosticHeaderNamePattern}\s*:\s*)'([^']*)'`, 'giu')
const diagnosticHeaderPattern = new RegExp(String.raw`\b(${sensitiveDiagnosticHeaderNamePattern}\s*:\s*)([^\s,;"']+)`, 'giu')
const diagnosticDoubleQuotedFlagPattern = new RegExp(String.raw`(^|[\s,;:])(-{1,2}${sensitiveDiagnosticNamePattern})(\s+)"([^"]*)"`, 'giu')
const diagnosticSingleQuotedFlagPattern = new RegExp(String.raw`(^|[\s,;:])(-{1,2}${sensitiveDiagnosticNamePattern})(\s+)'([^']*)'`, 'giu')
const diagnosticFlagPattern = new RegExp(String.raw`(^|[\s,;:])(-{1,2}${sensitiveDiagnosticNamePattern})(\s+)([^\s,;"']+)`, 'giu')

export function redactDiagnosticSecretFragments(message: string): string {
  return message
    .replace(diagnosticURLUserinfoPattern, (_match, scheme: string) => `${scheme}${REDACTED_DIAGNOSTIC_SECRET}@`)
    .replace(diagnosticDoubleQuotedAssignmentPattern, (_match, prefix: string, name: string) => `${prefix}${name}"${REDACTED_DIAGNOSTIC_SECRET}"`)
    .replace(diagnosticSingleQuotedAssignmentPattern, (_match, prefix: string, name: string) => `${prefix}${name}'${REDACTED_DIAGNOSTIC_SECRET}'`)
    .replace(diagnosticAssignmentPattern, (_match, prefix: string, name: string) => `${prefix}${name}${REDACTED_DIAGNOSTIC_SECRET}`)
    .replace(diagnosticDoubleQuotedKVPattern, (_match, prefix: string, name: string) => `${prefix}${name}"${REDACTED_DIAGNOSTIC_SECRET}"`)
    .replace(diagnosticSingleQuotedKVPattern, (_match, prefix: string, name: string) => `${prefix}${name}'${REDACTED_DIAGNOSTIC_SECRET}'`)
    .replace(diagnosticDoubleQuotedAuthPattern, (_match, prefix: string) => `${prefix}"${REDACTED_DIAGNOSTIC_SECRET}"`)
    .replace(diagnosticSingleQuotedAuthPattern, (_match, prefix: string) => `${prefix}'${REDACTED_DIAGNOSTIC_SECRET}'`)
    .replace(diagnosticAuthorizationPattern, (_match, prefix: string) => `${prefix}${REDACTED_DIAGNOSTIC_SECRET}`)
    .replace(diagnosticDoubleQuotedHeaderPattern, (_match, prefix: string) => `${prefix}"${REDACTED_DIAGNOSTIC_SECRET}"`)
    .replace(diagnosticSingleQuotedHeaderPattern, (_match, prefix: string) => `${prefix}'${REDACTED_DIAGNOSTIC_SECRET}'`)
    .replace(diagnosticHeaderPattern, (_match, prefix: string) => `${prefix}${REDACTED_DIAGNOSTIC_SECRET}`)
    .replace(diagnosticDoubleQuotedFlagPattern, (_match, prefix: string, name: string, separator: string) => `${prefix}${name}${separator}"${REDACTED_DIAGNOSTIC_SECRET}"`)
    .replace(diagnosticSingleQuotedFlagPattern, (_match, prefix: string, name: string, separator: string) => `${prefix}${name}${separator}'${REDACTED_DIAGNOSTIC_SECRET}'`)
    .replace(diagnosticFlagPattern, (_match, prefix: string, name: string, separator: string) => `${prefix}${name}${separator}${REDACTED_DIAGNOSTIC_SECRET}`)
}

export function getRedactedDiagnosticMessage(value: unknown): string | undefined {
  if (typeof value !== 'string') {
    return undefined
  }

  const trimmed = value.trim()
  return trimmed ? redactDiagnosticSecretFragments(trimmed) : undefined
}
