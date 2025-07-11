#!/usr/bin/env node

const { existsSync, readdirSync, readFileSync } = require('node:fs')
const { join, relative, resolve, sep } = require('node:path')
const ts = require('typescript')

const webRoot = process.env.MNEMONAS_WEB_ROOT ? resolve(process.env.MNEMONAS_WEB_ROOT) : join(__dirname, '..')
const srcRoot = join(webRoot, 'src')
const sourceFilePattern = /\.[cm]?[tj]sx?$/
const testFilePattern = /\.(?:test|spec)\.[cm]?[tj]sx?$/

function shouldSkipFile(file) {
  const parts = relative(srcRoot, file).split(sep)
  return testFilePattern.test(file) || parts.includes('test') || parts.includes('__mocks__')
}

function listSourceFiles(dir) {
  if (!existsSync(dir)) {
    return []
  }

  const entries = readdirSync(dir, { withFileTypes: true })
  const files = []

  for (const entry of entries) {
    const fullPath = join(dir, entry.name)
    if (entry.isDirectory()) {
      files.push(...listSourceFiles(fullPath))
      continue
    }
    if (entry.isFile() && sourceFilePattern.test(entry.name) && !shouldSkipFile(fullPath)) {
      files.push(fullPath)
    }
  }

  return files
}

function lineNumberAt(sourceFile, index) {
  return sourceFile.getLineAndCharacterOfPosition(index).line + 1
}

function isImportMetaEnvDev(node) {
  if (!ts.isPropertyAccessExpression(node) || node.name.text !== 'DEV') {
    return false
  }

  const envAccess = node.expression
  if (!ts.isPropertyAccessExpression(envAccess) || envAccess.name.text !== 'env') {
    return false
  }

  const metaAccess = envAccess.expression
  return ts.isMetaProperty(metaAccess)
    && metaAccess.keywordToken === ts.SyntaxKind.ImportKeyword
    && metaAccess.name.text === 'meta'
}

function conditionRequiresDev(condition) {
  if (ts.isParenthesizedExpression(condition)) {
    return conditionRequiresDev(condition.expression)
  }
  if (isImportMetaEnvDev(condition)) {
    return true
  }
  if (ts.isBinaryExpression(condition) && condition.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken) {
    return conditionRequiresDev(condition.left) || conditionRequiresDev(condition.right)
  }
  return false
}

function nodeIsInside(node, container, sourceFile) {
  const start = node.getStart(sourceFile)
  return start >= container.getStart(sourceFile) && node.end <= container.end
}

function isInsideDevGuard(node, sourceFile) {
  let current = node.parent
  while (current) {
    if (
      ts.isIfStatement(current)
      && conditionRequiresDev(current.expression)
      && nodeIsInside(node, current.thenStatement, sourceFile)
    ) {
      return true
    }
    current = current.parent
  }
  return false
}

function isStringLiteralWithText(node, text) {
  return (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) && node.text === text
}

function isConsoleHostExpression(node) {
  return ts.isIdentifier(node) && ['globalThis', 'self', 'window'].includes(node.text)
}

function isConsoleObjectExpression(node) {
  if (ts.isIdentifier(node)) {
    return node.text === 'console'
  }
  if (ts.isPropertyAccessExpression(node)) {
    return node.name.text === 'console' && isConsoleHostExpression(node.expression)
  }
  if (ts.isElementAccessExpression(node)) {
    return isConsoleHostExpression(node.expression) && isStringLiteralWithText(node.argumentExpression, 'console')
  }
  return false
}

function isConsoleMemberExpression(node) {
  if (ts.isParenthesizedExpression(node)) {
    return isConsoleMemberExpression(node.expression)
  }
  if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.CommaToken) {
    return isConsoleMemberExpression(node.right)
  }
  if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) {
    return isConsoleObjectExpression(node.expression) || isConsoleMemberExpression(node.expression)
  }
  return false
}

function isConsoleCall(node) {
  if (!ts.isCallExpression(node)) {
    return false
  }

  return isConsoleMemberExpression(node.expression)
}

function collectProductionConsoleUsage(sourceFile) {
  const lines = []

  function visit(node) {
    if (ts.isDebuggerStatement(node)) {
      lines.push(lineNumberAt(sourceFile, node.getStart(sourceFile)))
    }

    if (isConsoleCall(node) && !isInsideDevGuard(node, sourceFile)) {
      lines.push(lineNumberAt(sourceFile, node.expression.getStart(sourceFile)))
    }

    ts.forEachChild(node, visit)
  }

  visit(sourceFile)
  return [...new Set(lines)].sort((left, right) => left - right)
}

const failures = []

for (const file of listSourceFiles(srcRoot)) {
  const source = readFileSync(file, 'utf8')
  const sourceFile = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)

  for (const line of collectProductionConsoleUsage(sourceFile)) {
    failures.push(`${relative(webRoot, file)}:${line}`)
  }
}

if (failures.length > 0) {
  console.error('Production source files must not contain unguarded console calls or debugger statements:')
  for (const failure of failures) {
    console.error(`  ${failure}`)
  }
  process.exit(1)
}

console.log('[production-console-check] no unguarded production console or debugger usage found')
