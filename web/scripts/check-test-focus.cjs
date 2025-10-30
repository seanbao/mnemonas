#!/usr/bin/env node

const { existsSync, readdirSync, readFileSync } = require('node:fs')
const { join, relative, resolve } = require('node:path')
const ts = require('typescript')

const webRoot = process.env.MNEMONAS_WEB_ROOT ? resolve(process.env.MNEMONAS_WEB_ROOT) : join(__dirname, '..')
const srcRoot = join(webRoot, 'src')
const e2eRoot = join(webRoot, 'e2e')
const testFilePattern = /\.(?:test|spec)\.[cm]?[tj]sx?$/
const typescriptFilePattern = /\.[cm]?[tj]sx?$/
const focusedAliases = new Set(['fdescribe', 'fit'])
const focusedBases = new Set(['bench', 'describe', 'it', 'suite', 'test'])

function listFiles(dir, shouldIncludeFile) {
  if (!existsSync(dir)) {
    return []
  }

  const entries = readdirSync(dir, { withFileTypes: true })
  const files = []

  for (const entry of entries) {
    const fullPath = join(dir, entry.name)
    if (entry.isDirectory()) {
      files.push(...listFiles(fullPath, shouldIncludeFile))
      continue
    }
    if (entry.isFile() && shouldIncludeFile(entry.name)) {
      files.push(fullPath)
    }
  }

  return files
}

function lineNumberAt(sourceFile, index) {
  return sourceFile.getLineAndCharacterOfPosition(index).line + 1
}

function expressionParts(expression) {
  if (ts.isParenthesizedExpression(expression)) {
    return expressionParts(expression.expression)
  }
  if (ts.isBinaryExpression(expression) && expression.operatorToken.kind === ts.SyntaxKind.CommaToken) {
    return expressionParts(expression.right)
  }
  if (ts.isIdentifier(expression)) {
    return [expression.text]
  }
  if (ts.isPropertyAccessExpression(expression)) {
    return [...expressionParts(expression.expression), expression.name.text]
  }
  if (ts.isElementAccessExpression(expression)) {
    return [...expressionParts(expression.expression), stringLiteralValue(expression.argumentExpression) ?? '']
  }
  return []
}

function stringLiteralValue(node) {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
    return node.text
  }
  return null
}

function focusedCallTargetParts(expression) {
  const parts = expressionParts(expression)
  const last = parts.at(-1)
  if ((last === 'call' || last === 'apply') && parts.length > 1) {
    return parts.slice(0, -1)
  }
  return parts
}

function isFocusedParts(parts) {
  if (parts.some((part) => focusedAliases.has(part))) {
    return true
  }

  const onlyIndex = parts.indexOf('only')
  return onlyIndex > 0 && parts.slice(0, onlyIndex).some((part) => focusedBases.has(part))
}

function isFocusedCall(expression) {
  return isFocusedParts(focusedCallTargetParts(expression))
}

function collectFocusedTests(sourceFile) {
  const lines = []

  function visit(node) {
    if (ts.isCallExpression(node) && isFocusedCall(node.expression)) {
      lines.push(lineNumberAt(sourceFile, node.expression.getStart(sourceFile)))
    }

    ts.forEachChild(node, visit)
  }

  visit(sourceFile)
  return lines
}

const failures = []
const scanRoots = [
  { root: srcRoot, shouldIncludeFile: (name) => testFilePattern.test(name) },
  { root: e2eRoot, shouldIncludeFile: (name) => typescriptFilePattern.test(name) },
]

for (const { root, shouldIncludeFile } of scanRoots) {
  for (const file of listFiles(root, shouldIncludeFile)) {
    const source = readFileSync(file, 'utf8')
    const sourceFile = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)

    for (const line of collectFocusedTests(sourceFile)) {
      failures.push(`${relative(webRoot, file)}:${line}`)
    }
  }
}

if (failures.length > 0) {
  console.error('Focused tests are not allowed in committed frontend or E2E test files:')
  for (const failure of failures) {
    console.error(`  ${failure}`)
  }
  process.exit(1)
}

console.log('[test-focus-check] no focused tests found')
