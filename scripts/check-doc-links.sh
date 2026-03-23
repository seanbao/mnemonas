#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

node <<'NODE'
const cp = require('child_process')
const fs = require('fs')
const path = require('path')

const repoRoot = process.cwd()
const files = [
  ...cp.execFileSync('git', ['ls-files', '--', '*.md'], { encoding: 'utf8' }).split('\n'),
  ...cp.execFileSync('git', ['ls-files', '--others', '--exclude-standard', '--', '*.md'], { encoding: 'utf8' }).split('\n'),
]
  .filter(Boolean)
  .filter((file, index, items) => items.indexOf(file) === index)
const fileSet = new Set(files)

const errors = []
const anchorsByFile = new Map()
const documentationIndexFiles = new Set(['docs/README.md', 'docs/README.en.md'])
const decorativeHeadingEmoji = /[\u2600-\u27BF\u{1F300}-\u{1FAFF}]/u
const statusEmojiMarkers = /(?:\u2705|\u274C|\u26A0\uFE0F?|\u25D0)/u
const legacyFaqMarkers = /^(?:\s{0,3}#{1,6}\s+Q:\s+|\s{0,3}Q:\s+|\s{0,3}\*\*[QA]:\*\*)/
const bannedMarketingPhrases = [
  'Your files. Your control.',
  'Fast deployment',
  'one-click',
  '快速部署',
  '一键',
  '开箱即用',
  '轻松上手',
  '业界最佳',
  '极致性能',
]
const bannedCredentialPlaceholders = [
  'your-secure-password',
  'your-mnemonas-password',
  'your-password',
  'your-email@example.com',
  'very-strong-password-here',
  'change-this-password',
  'change-this-test-password',
  'change-this-webdav-password',
  'changeme',
  'password123',
]
const bannedChineseDocEnglishPhrases = [
  'preview scaffolding',
  'preview gateway',
  'preview config',
  'preview 状态',
  'SMB runtime',
  'SMB share',
  'LAN 挂载',
  'mobile 客户端',
]
const cjkCharacters = /[\u3040-\u30ff\u3400-\u9fff\uf900-\ufaff]/u
const allowedEnglishDocChineseLinkLabels = new Set([
  '简体中文',
  '中文文档索引',
  '中文 README',
  '支持说明',
  '贡献指南',
  '行为准则',
  '安全策略',
])
const shellFenceLanguages = new Set(['bash', 'sh', 'shell', 'console', 'zsh'])
const remoteShellPipePattern = /\b(?:curl|wget)\b[^|]*\|\s*(?:sudo\s+)?(?:sh|bash|zsh)\b/i
const directScriptCommandPattern = /(^|[^\w./-])(\.\/scripts\/([A-Za-z0-9._-]+\.sh))\b/g
const rawAPIPathQueryPattern = /\/api\/v1\/[^"'\s)]*[?&]path=\//
const storageCDCContractDocs = [
  {
    file: 'docs/storage-internals.md',
    required: [
      'BLAKE3 整对象版本',
      '当前版本历史不会按 CDC 分块引用计数',
      'FastCDC API 属于数据面能力',
    ],
    forbidden: [
      '版本历史按 CDC 分块去重',
      '已启用 CDC 分块版本去重',
    ],
  },
  {
    file: 'docs/configuration.md',
    required: [
      'Rust 数据面 FastCDC 文件 API',
      '当前 Go 版本历史路径仍使用整对象 CAS 快照',
      '不表示当前版本历史已启用分块级去重',
    ],
    forbidden: [
      '影响存储效率和去重率',
      '版本历史按 CDC 分块去重',
      '已启用 CDC 分块版本去重',
    ],
  },
  {
    file: 'docs/storage-internals.en.md',
    required: [
      'BLAKE3 whole-object versions',
      'current version history does not reference-count CDC chunks',
      'FastCDC API is a dataplane capability',
    ],
    forbidden: [
      'version history deduplicates CDC chunks',
      'CDC chunk version deduplication is enabled',
    ],
  },
  {
    file: 'docs/configuration.en.md',
    required: [
      'Rust dataplane FastCDC file API',
      'Current Go version history still uses whole-object CAS snapshots',
      'do not mean version history has block-level deduplication enabled',
    ],
    forbidden: [
      'Content-defined chunking settings affect deduplication and metadata overhead',
      'version history deduplicates CDC chunks',
      'CDC chunk version deduplication is enabled',
    ],
  },
]
const requiredDocumentPairs = [
  ['README.md', 'README.en.md', 'English', 'Chinese'],
  ['CHANGELOG.md', 'CHANGELOG.en.md', 'English', 'Chinese'],
  ['CODE_OF_CONDUCT.zh-CN.md', 'CODE_OF_CONDUCT.md', 'English', 'Chinese'],
  ['CONTRIBUTING.md', 'CONTRIBUTING.en.md', 'English', 'Chinese'],
  ['SUPPORT.md', 'SUPPORT.en.md', 'English', 'Chinese'],
  ['SECURITY.zh-CN.md', 'SECURITY.md', 'English', 'Chinese'],
  ['deploy/public-access/README.md', 'deploy/public-access/README.en.md', 'English', 'Chinese'],
  ['web/README.md', 'web/README.en.md', 'English', 'Chinese'],
]

function checkRequiredDocumentPairs() {
  for (const [chineseFile, englishFile, englishLabel, chineseLabel] of requiredDocumentPairs) {
    if (fileSet.has(chineseFile) && !fileSet.has(englishFile)) {
      errors.push(`${chineseFile}: missing ${englishLabel} documentation pair: ${englishFile}`)
    }
    if (fileSet.has(englishFile) && !fileSet.has(chineseFile)) {
      errors.push(`${englishFile}: missing ${chineseLabel} documentation pair: ${chineseFile}`)
    }
  }
}

function checkDocumentationPairs() {
  for (const file of files) {
    const parsed = path.parse(file)
    if (parsed.dir !== 'docs' || parsed.ext !== '.md') {
      continue
    }

    if (parsed.name.endsWith('.en')) {
      const chineseFile = path.join(parsed.dir, `${parsed.name.slice(0, -3)}.md`)
      if (!fileSet.has(chineseFile)) {
        errors.push(`${file}: missing Chinese documentation pair: ${chineseFile}`)
      }
      continue
    }

    const englishFile = path.join(parsed.dir, `${parsed.name}.en.md`)
    if (!fileSet.has(englishFile)) {
      errors.push(`${file}: missing English documentation pair: ${englishFile}`)
    }
  }
}

function checkDocumentationIndexCoverage() {
  const chineseIndex = readOptionalFile('docs/README.md')
  const englishIndex = readOptionalFile('docs/README.en.md')

  for (const file of files) {
    const parsed = path.parse(file)
    if (parsed.dir !== 'docs' || parsed.ext !== '.md' || documentationIndexFiles.has(file) || parsed.name.endsWith('.en')) {
      continue
    }

    const englishFile = path.join(parsed.dir, `${parsed.name}.en.md`)
    if (chineseIndex !== null) {
      if (!containsMarkdownLinkTarget(chineseIndex, file)) {
        errors.push(`docs/README.md: missing documentation index entry: ${file}`)
      }
      if (fileSet.has(englishFile) && !containsMarkdownLinkTarget(chineseIndex, englishFile)) {
        errors.push(`docs/README.md: missing documentation index entry: ${englishFile}`)
      }
    }
    if (englishIndex !== null && fileSet.has(englishFile) && !containsMarkdownLinkTarget(englishIndex, englishFile)) {
      errors.push(`docs/README.en.md: missing documentation index entry: ${englishFile}`)
    }
  }
}

function checkPairedHeadingLevelSequences() {
  for (const [sourceFile, pairedFile] of pairedDocumentationFiles()) {
    const sourceLevels = markdownHeadingLevels(sourceFile)
    const pairedLevels = markdownHeadingLevels(pairedFile)
    if (sourceLevels.join(',') !== pairedLevels.join(',')) {
      errors.push(`${sourceFile} and ${pairedFile}: heading level sequence differs`)
    }
  }
}

function checkPairedLanguageLinks() {
  for (const [chineseFile, englishFile] of pairedDocumentationFiles()) {
    if (!hasMarkdownLinkTo(chineseFile, englishFile)) {
      errors.push(`${chineseFile}: missing language switch link to ${englishFile}`)
    }
    if (!hasMarkdownLinkTo(englishFile, chineseFile)) {
      errors.push(`${englishFile}: missing language switch link to ${chineseFile}`)
    }
  }
}

function checkStorageCDCContract() {
  for (const doc of storageCDCContractDocs) {
    const text = readOptionalFile(doc.file)
    if (text === null) {
      continue
    }

    for (const phrase of doc.required) {
      if (!text.includes(phrase)) {
        errors.push(`${doc.file}: missing storage CDC boundary text: ${phrase}`)
      }
    }
    for (const phrase of doc.forbidden) {
      if (text.includes(phrase)) {
        errors.push(`${doc.file}: avoid implying CDC chunk-level version deduplication: ${phrase}`)
      }
    }
  }
}

function hasMarkdownLinkTo(sourceFile, targetFile) {
  const markdown = readOptionalFile(sourceFile)
  if (markdown === null) {
    return false
  }

  const sourceDir = path.dirname(sourceFile)
  return extractMarkdownLinkTargets(markdown).some((target) => {
    const normalized = normalizeTarget(target)
    if (!normalized?.pathTarget) {
      return false
    }
    return path.normalize(path.join(sourceDir, normalized.pathTarget)) === targetFile
  })
}

function pairedDocumentationFiles() {
  const pairs = []
  const seen = new Set()

  function addPair(sourceFile, pairedFile) {
    if (!fileSet.has(sourceFile) || !fileSet.has(pairedFile)) {
      return
    }
    const key = `${sourceFile}\0${pairedFile}`
    if (seen.has(key)) {
      return
    }
    seen.add(key)
    pairs.push([sourceFile, pairedFile])
  }

  for (const [chineseFile, englishFile] of requiredDocumentPairs) {
    addPair(chineseFile, englishFile)
  }

  for (const file of files) {
    const parsed = path.parse(file)
    if (parsed.dir !== 'docs' || parsed.ext !== '.md' || parsed.name.endsWith('.en')) {
      continue
    }
    addPair(file, path.join(parsed.dir, `${parsed.name}.en.md`))
  }

  return pairs
}

function markdownHeadingLevels(file) {
  const levels = []
  const text = fs.readFileSync(path.join(repoRoot, file), 'utf8')
  let inFence = false

  for (const line of text.split('\n')) {
    if (/^\s{0,3}(```|~~~)/.test(line)) {
      inFence = !inFence
      continue
    }
    if (inFence) {
      continue
    }

    const match = /^\s{0,3}(#{1,6})\s+(.+?)\s*$/.exec(line)
    if (match) {
      levels.push(match[1].length)
    }
  }

  return levels
}

function readOptionalFile(filePath) {
  if (!fileSet.has(filePath)) {
    return null
  }
  return fs.readFileSync(path.join(repoRoot, filePath), 'utf8')
}

function containsMarkdownLinkTarget(markdown, targetFile) {
  const escapedTarget = targetFile.startsWith('docs/') ? targetFile.slice('docs/'.length) : targetFile
  return extractMarkdownLinkTargets(markdown).some((target) => {
    const normalized = normalizeTarget(target)
    return normalized?.pathTarget === escapedTarget
  })
}

function normalizeTarget(rawTarget) {
  let target = rawTarget.trim()
  if (!target) {
    return null
  }
  if (/^[a-z][a-z0-9+.-]*:/i.test(target)) {
    return null
  }
  if (target.startsWith('<') && target.endsWith('>')) {
    target = target.slice(1, -1)
  } else {
    target = target.split(/\s+/, 1)[0]
  }
  const hashIndex = target.indexOf('#')
  const fragment = hashIndex >= 0 ? target.slice(hashIndex + 1) : ''
  const pathTarget = (hashIndex >= 0 ? target.slice(0, hashIndex) : target).split('?', 1)[0]
  if (!pathTarget && !fragment) {
    return null
  }
  try {
    return {
      pathTarget: pathTarget ? decodeURIComponent(pathTarget) : '',
      fragment: fragment ? decodeURIComponent(fragment) : '',
      hasFragment: hashIndex >= 0,
    }
  } catch (error) {
    return { pathTarget, fragment, hasFragment: hashIndex >= 0 }
  }
}

function checkTarget(sourceFile, rawTarget) {
  const link = normalizeTarget(rawTarget)
  if (!link) {
    return
  }

  const sourceDir = path.dirname(path.join(repoRoot, sourceFile))
  const resolved = link.pathTarget
    ? path.normalize(path.join(sourceDir, link.pathTarget))
    : path.join(repoRoot, sourceFile)
  if (resolved !== repoRoot && !resolved.startsWith(repoRoot + path.sep)) {
    errors.push(`${sourceFile}: link escapes repository: ${rawTarget}`)
    return
  }
  if (!fs.existsSync(resolved)) {
    errors.push(`${sourceFile}: missing link target: ${rawTarget}`)
    return
  }
  const relativeTarget = path.relative(repoRoot, resolved).split(path.sep).join('/')
  if (isRepositoryScript(relativeTarget) && !isExecutable(resolved)) {
    errors.push(`${sourceFile}: linked script is not executable: ${rawTarget}`)
  }
  if (link.hasFragment && link.fragment && resolved.endsWith('.md')) {
    const anchors = getMarkdownAnchors(resolved)
    const normalizedAnchor = normalizeAnchor(link.fragment)
    if (!anchors.has(normalizedAnchor)) {
      errors.push(`${sourceFile}: missing heading anchor: ${rawTarget}`)
    }
  }
}

function normalizeAnchor(fragment) {
  return fragment.trim().toLowerCase()
}

function getMarkdownAnchors(filePath) {
  const relativePath = path.relative(repoRoot, filePath)
  if (anchorsByFile.has(relativePath)) {
    return anchorsByFile.get(relativePath)
  }

  const anchors = new Set()
  const seen = new Map()
  let inFence = false
  const text = fs.readFileSync(filePath, 'utf8')

  for (const line of text.split('\n')) {
    if (/^\s{0,3}(```|~~~)/.test(line)) {
      inFence = !inFence
      continue
    }
    if (inFence) {
      continue
    }

    const match = /^\s{0,3}#{1,6}\s+(.+?)\s*$/.exec(line)
    if (!match) {
      continue
    }

    const heading = match[1].replace(/\s+#+\s*$/, '')
    const baseSlug = slugHeading(heading)
    if (!baseSlug) {
      continue
    }
    const count = seen.get(baseSlug) ?? 0
    seen.set(baseSlug, count + 1)
    anchors.add(count === 0 ? baseSlug : `${baseSlug}-${count}`)
  }

  anchorsByFile.set(relativePath, anchors)
  return anchors
}

function slugHeading(heading) {
  return heading
    .trim()
    .toLowerCase()
    .replace(/<[^>]*>/g, '')
    .replace(/[`*_~]/g, '')
    .replace(/[!"#$%&'()*+,./:;<=>?@[\\\]^{}|，。！？、；：（）【】《》“”‘’]/g, '')
    .replace(/\s+/g, '-')
}

checkRequiredDocumentPairs()
checkDocumentationPairs()
checkDocumentationIndexCoverage()
checkPairedHeadingLevelSequences()
checkPairedLanguageLinks()
checkStorageCDCContract()

for (const file of files) {
  const text = fs.readFileSync(path.join(repoRoot, file), 'utf8')

  checkCredentialPlaceholderStyle(file, text)
  checkEnglishDocumentationLanguage(file, text)
  checkDocumentationStyle(file, text)
  checkAPIPathQueryEncoding(file, text)
  checkShellCodeFenceSafety(file, text)
  checkDocumentationScriptReferences(file, text)
  for (const target of extractMarkdownLinkTargets(text)) {
    checkTarget(file, target)
  }
}

function checkCredentialPlaceholderStyle(sourceFile, markdown) {
  const lines = markdown.split('\n')
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index]
    const lineNumber = index + 1
    for (const phrase of bannedCredentialPlaceholders) {
      if (line.includes(phrase)) {
        errors.push(`${sourceFile}:${lineNumber}: avoid copyable placeholder credentials in project documentation: ${phrase}`)
      }
    }
  }
}

function checkEnglishDocumentationLanguage(sourceFile, markdown) {
  if (!isEnglishMarkdown(sourceFile)) {
    return
  }

  const lines = markdown.split('\n')
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index]
    if (!cjkCharacters.test(stripAllowedEnglishDocChineseNavigation(line))) {
      continue
    }
    errors.push(`${sourceFile}:${index + 1}: avoid non-English text outside language-navigation links in English documentation`)
  }
}

function stripAllowedEnglishDocChineseNavigation(line) {
  return line.replace(/\[([^\]\n]+)\]\([^)]+\)/g, (match, label) => (
    allowedEnglishDocChineseLinkLabels.has(label) ? '' : match
  ))
}

function hasBannedMarketingPhrase(line, phrase) {
  if (/[A-Za-z]/.test(phrase)) {
    return line.toLowerCase().includes(phrase.toLowerCase())
  }
  return line.includes(phrase)
}

function checkDocumentationStyle(sourceFile, markdown) {
  const lines = markdown.split('\n')
  let inFence = false
  const chineseMarkdown = isChineseMarkdown(sourceFile)
  const englishSecondPerson = /\b(?:you|your|yours|yourself)\b/i
  const chineseSecondPerson = /[你您](?:的|们)?/

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index]
    const lineNumber = index + 1
    if (/^\s{0,3}(```|~~~)/.test(line)) {
      inFence = !inFence
      continue
    }
    if (inFence) {
      continue
    }

    for (const phrase of bannedMarketingPhrases) {
      if (hasBannedMarketingPhrase(line, phrase)) {
        errors.push(`${sourceFile}:${lineNumber}: avoid promotional wording in project documentation: ${phrase}`)
      }
    }
    if (statusEmojiMarkers.test(line)) {
      errors.push(`${sourceFile}:${lineNumber}: avoid emoji status markers in project documentation`)
    }
    if (legacyFaqMarkers.test(line)) {
      errors.push(`${sourceFile}:${lineNumber}: avoid legacy Q:/A: FAQ markers in project documentation`)
    }
    if (englishSecondPerson.test(line)) {
      errors.push(`${sourceFile}:${lineNumber}: avoid second-person wording in project documentation`)
    }
    if (chineseMarkdown && chineseSecondPerson.test(line)) {
      errors.push(`${sourceFile}:${lineNumber}: avoid second-person wording in Chinese project documentation`)
    }
    if (chineseMarkdown) {
      for (const phrase of bannedChineseDocEnglishPhrases) {
        if (line.includes(phrase)) {
          errors.push(`${sourceFile}:${lineNumber}: avoid English phrasing in Chinese documentation: ${phrase}`)
        }
      }
    }

    const heading = /^\s{0,3}#{1,6}\s+(.+?)\s*$/.exec(line)?.[1]?.replace(/\s+#+\s*$/, '') ?? ''
    if (heading && decorativeHeadingEmoji.test(heading)) {
      errors.push(`${sourceFile}:${lineNumber}: avoid decorative emoji in markdown headings`)
    }
  }
}

function checkAPIPathQueryEncoding(sourceFile, markdown) {
  const lines = markdown.split('\n')
  for (let index = 0; index < lines.length; index += 1) {
    if (rawAPIPathQueryPattern.test(lines[index])) {
      errors.push(`${sourceFile}:${index + 1}: URL-encode API path query values in documentation examples; use path=%2F...`)
    }
  }
}

function isChineseMarkdown(sourceFile) {
  return sourceFile.endsWith('.md') && !sourceFile.endsWith('.en.md') && sourceFile !== 'SECURITY.md'
}

function isEnglishMarkdown(sourceFile) {
  return sourceFile.endsWith('.en.md') || sourceFile === 'SECURITY.md'
}

function checkShellCodeFenceSafety(sourceFile, markdown) {
  const lines = markdown.split('\n')
  let inFence = false
  let fenceChar = ''
  let language = ''

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index]
    const lineNumber = index + 1
    if (!inFence) {
      const match = /^ {0,3}(`{3,}|~{3,})\s*(.*)$/.exec(line)
      if (!match) {
        continue
      }
      inFence = true
      fenceChar = match[1][0]
      language = (match[2] || '').trim().split(/\s+/, 1)[0].toLowerCase()
      continue
    }

    const closePattern = fenceChar === '`' ? /^ {0,3}`{3,}\s*$/ : /^ {0,3}~{3,}\s*$/
    if (closePattern.test(line)) {
      inFence = false
      fenceChar = ''
      language = ''
      continue
    }

    if (shellFenceLanguages.has(language) && remoteShellPipePattern.test(line)) {
      errors.push(`${sourceFile}:${lineNumber}: avoid piping remote install scripts directly to a shell; download and inspect the script first`)
    }
  }
}

function checkDocumentationScriptReferences(sourceFile, markdown) {
  const lines = markdown.split('\n')
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index]
    const lineNumber = index + 1
    for (const match of line.matchAll(directScriptCommandPattern)) {
      const command = match[2]
      const commandStart = (match.index ?? 0) + match[1].length
      if (hasExplicitShellInterpreter(line.slice(0, commandStart))) {
        continue
      }

      const scriptPath = command.slice(2)
      const resolved = path.join(repoRoot, scriptPath)
      if (!fs.existsSync(resolved)) {
        errors.push(`${sourceFile}:${lineNumber}: missing script reference: ${command}`)
        continue
      }
      if (!isExecutable(resolved)) {
        errors.push(`${sourceFile}:${lineNumber}: script reference is not executable: ${command}`)
      }
    }
  }
}

function hasExplicitShellInterpreter(prefix) {
  return /(^|\s)(?:bash|sh|zsh|dash)\s*$/.test(prefix.trimEnd())
}

function isRepositoryScript(relativePath) {
  return /^scripts\/[^/]+\.sh$/.test(relativePath)
}

function isExecutable(filePath) {
  return (fs.statSync(filePath).mode & 0o111) !== 0
}

function extractMarkdownLinkTargets(markdown) {
  const targets = []
  let inFence = false
  let fenceChar = ''

  for (const line of markdown.split('\n')) {
    if (!inFence) {
      const fence = /^ {0,3}(`{3,}|~{3,})/.exec(line)
      if (fence) {
        inFence = true
        fenceChar = fence[1][0]
        continue
      }

      targets.push(...extractInlineMarkdownLinkTargets(line))
      const referenceTarget = extractReferenceMarkdownLinkTarget(line)
      if (referenceTarget) {
        targets.push(referenceTarget)
      }
      continue
    }

    const closePattern = fenceChar === '`' ? /^ {0,3}`{3,}\s*$/ : /^ {0,3}~{3,}\s*$/
    if (closePattern.test(line)) {
      inFence = false
      fenceChar = ''
    }
  }

  return targets
}

function extractInlineMarkdownLinkTargets(line) {
  const targets = []
  for (let index = 0; index < line.length; index += 1) {
    if (line[index] !== '[' || isEscaped(line, index)) {
      continue
    }

    const labelEnd = findClosingBracket(line, index + 1)
    if (labelEnd < 0) {
      continue
    }

    if (line[labelEnd + 1] !== '(') {
      index = labelEnd
      continue
    }

    const linkTarget = readParenthesizedLinkTarget(line, labelEnd + 2)
    if (!linkTarget) {
      index = labelEnd
      continue
    }

    targets.push(linkTarget.target)
    index = linkTarget.end
  }
  return targets
}

function findClosingBracket(text, start) {
  let depth = 0
  for (let index = start; index < text.length; index += 1) {
    if (isEscaped(text, index)) {
      continue
    }
    if (text[index] === '[') {
      depth += 1
      continue
    }
    if (text[index] === ']') {
      if (depth === 0) {
        return index
      }
      depth -= 1
    }
  }
  return -1
}

function readParenthesizedLinkTarget(text, start) {
  let depth = 0
  for (let index = start; index < text.length; index += 1) {
    if (isEscaped(text, index)) {
      continue
    }
    if (text[index] === '(') {
      depth += 1
      continue
    }
    if (text[index] === ')') {
      if (depth === 0) {
        return { target: text.slice(start, index), end: index }
      }
      depth -= 1
    }
  }
  return null
}

function extractReferenceMarkdownLinkTarget(line) {
  const match = /^\s*\[[^\]\n]+\]:\s+(\S+)/.exec(line)
  return match ? match[1] : null
}

function isEscaped(text, index) {
  let slashCount = 0
  for (let cursor = index - 1; cursor >= 0 && text[cursor] === '\\'; cursor -= 1) {
    slashCount += 1
  }
  return slashCount % 2 === 1
}

if (errors.length > 0) {
  console.error('Documentation link check failed:')
  for (const error of errors) {
    console.error(`  - ${error}`)
  }
  process.exit(1)
}

console.log(`[docs-link-check] checked ${files.length} markdown files`)
NODE

python3 - <<'PY'
import json
import pathlib
import re
import subprocess
import sys
from collections.abc import Hashable

try:
    import yaml
    from yaml.constructor import ConstructorError
    from yaml.nodes import MappingNode, SequenceNode
except ModuleNotFoundError:
    print(
        "check-doc-links: PyYAML is required for YAML code fences; install python3-yaml or run `python3 -m pip install PyYAML`",
        file=sys.stderr,
    )
    sys.exit(1)


class UniqueKeyLoader(yaml.SafeLoader):
    pass


def construct_mapping_without_duplicates(loader, node, deep=False):
    if not isinstance(node, MappingNode):
        raise ConstructorError(
            None,
            None,
            f"expected a mapping node, but found {node.id}",
            node.start_mark,
        )

    seen = {}
    for key_node, _ in node.value:
        if key_node.tag == "tag:yaml.org,2002:merge":
            continue

        key = loader.construct_object(key_node, deep=deep)
        if not isinstance(key, Hashable):
            raise ConstructorError(
                "while constructing a mapping",
                node.start_mark,
                "found unhashable key",
                key_node.start_mark,
            )
        if key in seen:
            raise ConstructorError(
                "while constructing a mapping",
                node.start_mark,
                f"found duplicate key {key!r}",
                key_node.start_mark,
            )
        seen[key] = key_node.start_mark

    return yaml.SafeLoader.construct_mapping(loader, node, deep=deep)


UniqueKeyLoader.add_constructor(
    yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG,
    construct_mapping_without_duplicates,
)


def construct_unknown_tag(loader, tag_suffix, node):
    if isinstance(node, MappingNode):
        return construct_mapping_without_duplicates(loader, node)
    if isinstance(node, SequenceNode):
        return loader.construct_sequence(node)
    return loader.construct_scalar(node)


UniqueKeyLoader.add_multi_constructor("", construct_unknown_tag)


def construct_json_object_without_duplicates(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"found duplicate key {key!r}")
        result[key] = value
    return result


def git_files(*args):
    output = subprocess.check_output(["git", *args], text=True)
    return [line for line in output.splitlines() if line]


tracked = git_files("ls-files", "--", "*.md")
untracked = git_files("ls-files", "--others", "--exclude-standard", "--", "*.md")
files = list(dict.fromkeys([*tracked, *untracked]))

open_fence = re.compile(r"^ {0,3}(`{3,}|~{3,})\s*(.*)$")
close_backtick = re.compile(r"^ {0,3}`{3,}\s*$")
close_tilde = re.compile(r"^ {0,3}~{3,}\s*$")
errors = []
json_fence_count = 0
yaml_fence_count = 0

for file_name in files:
    path = pathlib.Path(file_name)
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except UnicodeDecodeError as error:
        errors.append(f"{file_name}: invalid UTF-8: {error}")
        continue

    in_fence = False
    fence_char = ""
    language = ""
    start_line = 0
    content = []

    for index, line in enumerate(lines, 1):
        if not in_fence:
            match = open_fence.match(line)
            if not match:
                continue
            in_fence = True
            fence_char = match.group(1)[0]
            language = (match.group(2).strip().split(None, 1)[0].lower() if match.group(2).strip() else "")
            start_line = index
            content = []
            continue

        closed = (fence_char == "`" and close_backtick.match(line)) or (
            fence_char == "~" and close_tilde.match(line)
        )
        if closed:
            if language == "json":
                json_fence_count += 1
                raw = "\n".join(content).strip()
                if raw:
                    try:
                        json.loads(raw, object_pairs_hook=construct_json_object_without_duplicates)
                    except ValueError as error:
                        errors.append(f"{file_name}:{start_line}: invalid json code fence: {error}")
            elif language in {"yaml", "yml"}:
                yaml_fence_count += 1
                raw = "\n".join(content).strip()
                if raw:
                    try:
                        yaml.load(raw, Loader=UniqueKeyLoader)
                    except yaml.YAMLError as error:
                        errors.append(f"{file_name}:{start_line}: invalid yaml code fence: {error}")
            in_fence = False
            fence_char = ""
            language = ""
            start_line = 0
            content = []
            continue

        content.append(line)

if errors:
    print("Documentation structured code fence check failed:", file=sys.stderr)
    for error in errors:
        print(f"  - {error}", file=sys.stderr)
    sys.exit(1)

print(f"[docs-structured-check] checked {json_fence_count} JSON code fences and {yaml_fence_count} YAML code fences")
PY

toml_check_program="$(mktemp "${TMPDIR:-/tmp}/mnemonas-doc-toml-check.XXXXXX.go")"
trap 'rm -f -- "$toml_check_program"' EXIT
cat > "$toml_check_program" <<'GO'
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

func gitFiles(args ...string) ([]string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(out), "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

func main() {
	tracked, err := gitFiles("ls-files", "--", "*.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list tracked markdown files: %v\n", err)
		os.Exit(1)
	}
	untracked, err := gitFiles("ls-files", "--others", "--exclude-standard", "--", "*.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list untracked markdown files: %v\n", err)
		os.Exit(1)
	}

	seen := map[string]bool{}
	files := make([]string, 0, len(tracked)+len(untracked))
	for _, file := range append(tracked, untracked...) {
		if !seen[file] {
			seen[file] = true
			files = append(files, file)
		}
	}

	errors := []string{}
	tomlFenceCount := 0
	openFence := regexp.MustCompile("^ {0,3}(`{3,}|~{3,})\\s*(.*)$")
	closeBacktick := regexp.MustCompile("^ {0,3}`{3,}\\s*$")
	closeTilde := regexp.MustCompile("^ {0,3}~{3,}\\s*$")

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: failed to read file: %v", file, err))
			continue
		}

		inFence := false
		fenceChar := ""
		language := ""
		startLine := 0
		content := []string{}

		for index, line := range strings.Split(string(data), "\n") {
			lineNumber := index + 1
			if !inFence {
				match := openFence.FindStringSubmatch(line)
				if match == nil {
					continue
				}
				inFence = true
				fenceChar = match[1][:1]
				fields := strings.Fields(strings.TrimSpace(match[2]))
				if len(fields) > 0 {
					language = strings.ToLower(fields[0])
				} else {
					language = ""
				}
				startLine = lineNumber
				content = []string{}
				continue
			}

			closed := (fenceChar == "`" && closeBacktick.MatchString(line)) || (fenceChar == "~" && closeTilde.MatchString(line))
			if closed {
				if language == "toml" {
					tomlFenceCount++
					raw := strings.TrimSpace(strings.Join(content, "\n"))
					if raw != "" {
						var decoded map[string]any
						if err := toml.Unmarshal([]byte(raw), &decoded); err != nil {
							errors = append(errors, fmt.Sprintf("%s:%d: invalid toml code fence: %v", file, startLine, err))
						}
					}
				}
				inFence = false
				fenceChar = ""
				language = ""
				startLine = 0
				content = []string{}
				continue
			}

			content = append(content, line)
		}
	}
	checkSecurityCheckDocumentationCoverage(files, &errors)

	if len(errors) > 0 {
		fmt.Fprintln(os.Stderr, "Documentation structured checks failed:")
		for _, err := range errors {
			fmt.Fprintf(os.Stderr, "  - %s\n", err)
		}
		os.Exit(1)
	}

	fmt.Printf("[docs-toml-check] checked %d TOML code fences\n", tomlFenceCount)
}

func checkSecurityCheckDocumentationCoverage(files []string, errors *[]string) {
	if _, err := os.Stat("internal/api/server.go"); err != nil {
		if os.IsNotExist(err) {
			return
		}
		*errors = append(*errors, fmt.Sprintf("internal/api/server.go: failed to stat server file: %v", err))
		return
	}

	serverIDs, err := securityCheckIDsFromServer("internal/api/server.go")
	if err != nil {
		*errors = append(*errors, fmt.Sprintf("internal/api/server.go: failed to extract security-check IDs: %v", err))
		return
	}
	if len(serverIDs) == 0 {
		return
	}

	fileSet := map[string]bool{}
	for _, file := range files {
		fileSet[file] = true
	}

	for _, docFile := range []string{"docs/api-reference.md", "docs/api-reference.en.md"} {
		if !fileSet[docFile] {
			*errors = append(*errors, fmt.Sprintf("%s: missing security-check API documentation", docFile))
			continue
		}

		data, err := os.ReadFile(docFile)
		if err != nil {
			*errors = append(*errors, fmt.Sprintf("%s: failed to read security-check API documentation: %v", docFile, err))
			continue
		}
		docIDs := securityCheckIDsFromDoc(string(data))
		docIDSet := map[string]bool{}
		for _, id := range docIDs {
			docIDSet[id] = true
		}
		for _, id := range serverIDs {
			if !docIDSet[id] {
				*errors = append(*errors, fmt.Sprintf("%s: security-check documentation is missing ID: %s", docFile, id))
			}
		}

		serverIDSet := map[string]bool{}
		for _, id := range serverIDs {
			serverIDSet[id] = true
		}
		for _, id := range docIDs {
			if !serverIDSet[id] {
				*errors = append(*errors, fmt.Sprintf("%s: security-check documentation lists unknown ID: %s", docFile, id))
			}
		}
	}
}

func securityCheckIDsFromServer(path string) ([]string, error) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}

	ids := map[string]bool{}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if fn.Name.Name != "handleGetSecurityCheck" && !strings.HasPrefix(fn.Name.Name, "security") {
			continue
		}

		constants := map[string]string{}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			decl, ok := node.(*ast.GenDecl)
			if !ok || decl.Tok != token.CONST {
				return true
			}
			for _, spec := range decl.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, name := range valueSpec.Names {
					if name.Name != "checkID" || index >= len(valueSpec.Values) {
						continue
					}
					if id, ok := stringLiteralValue(valueSpec.Values[index]); ok {
						constants[name.Name] = id
					}
				}
			}
			return false
		})

		ast.Inspect(fn.Body, func(node ast.Node) bool {
			composite, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			collectSecurityCheckItemIDs(composite, constants, ids)
			return true
		})
	}

	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func collectSecurityCheckItemIDs(composite *ast.CompositeLit, constants map[string]string, ids map[string]bool) {
	switch typ := composite.Type.(type) {
	case *ast.Ident:
		if typ.Name == "securityCheckItem" {
			collectSecurityCheckItemIDFields(composite.Elts, constants, ids)
		}
	case *ast.ArrayType:
		if ident, ok := typ.Elt.(*ast.Ident); ok {
			if ident.Name != "securityCheckItem" {
				return
			}
			for _, element := range composite.Elts {
				if child, ok := element.(*ast.CompositeLit); ok {
					collectSecurityCheckItemIDFields(child.Elts, constants, ids)
				}
			}
		}
	}
}

func collectSecurityCheckItemIDFields(elements []ast.Expr, constants map[string]string, ids map[string]bool) {
	for _, element := range elements {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok || key.Name != "ID" {
			continue
		}
		if id, ok := stringLiteralValue(pair.Value); ok {
			ids[id] = true
			continue
		}
		if ident, ok := pair.Value.(*ast.Ident); ok {
			if id, ok := constants[ident.Name]; ok {
				ids[id] = true
			}
		}
	}
}

func stringLiteralValue(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

func securityCheckIDsFromDoc(markdown string) []string {
	ids := map[string]bool{}
	codeSpan := regexp.MustCompile("`([a-z][a-z0-9_]+)`")
	for _, source := range securityCheckIDDocSources(markdown) {
		for _, match := range codeSpan.FindAllStringSubmatch(source, -1) {
			ids[match[1]] = true
		}
	}

	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func securityCheckIDDocSources(markdown string) []string {
	lines := strings.Split(markdown, "\n")
	sources := []string{}
	idListMarkers := []string{
		"Current check IDs include",
		"当前检查项 ID 包括",
	}
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		switch {
		case strings.Contains(line, "checks[].id"):
			block := []string{line}
			for next := index + 1; next < len(lines); next++ {
				nextLine := strings.TrimSpace(lines[next])
				if nextLine == "" || strings.HasPrefix(nextLine, "- ") {
					break
				}
				block = append(block, lines[next])
				index = next
			}
			sources = append(sources, strings.Join(block, " "))
		default:
			markerStart := -1
			for _, marker := range idListMarkers {
				if start := strings.Index(line, marker); start >= 0 {
					markerStart = start
					break
				}
			}
			if markerStart >= 0 {
				block := []string{line[markerStart:]}
				for next := index + 1; next < len(lines); next++ {
					nextLine := strings.TrimSpace(lines[next])
					if nextLine == "" {
						break
					}
					block = append(block, lines[next])
					index = next
					if strings.ContainsAny(lines[next], ".。") {
						break
					}
				}
				source := strings.Join(block, " ")
				if stop := strings.IndexAny(source, ".。"); stop >= 0 {
					source = source[:stop]
				}
				sources = append(sources, source)
			}
		}
	}
	return sources
}
GO
GOTOOLCHAIN=local go run "$toml_check_program"
