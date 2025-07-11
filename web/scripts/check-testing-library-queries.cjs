#!/usr/bin/env node

const { existsSync, readdirSync, readFileSync } = require('node:fs')
const { join, relative, resolve } = require('node:path')
const ts = require('typescript')

const webRoot = process.env.MNEMONAS_WEB_ROOT ? resolve(process.env.MNEMONAS_WEB_ROOT) : join(__dirname, '..')
const srcRoot = join(webRoot, 'src')
const e2eRoot = join(webRoot, 'e2e')
const testFilePattern = /\.(?:test|spec)\.[cm]?[tj]sx?$/
const typescriptFilePattern = /\.[cm]?[tj]sx?$/
const disallowedTestingLibraryQueries = new Set([
  'getByPlaceholderText',
  'getAllByPlaceholderText',
  'findByPlaceholderText',
  'findAllByPlaceholderText',
  'queryByPlaceholderText',
  'queryAllByPlaceholderText',
  'getByDisplayValue',
  'getAllByDisplayValue',
  'findByDisplayValue',
  'findAllByDisplayValue',
  'queryByDisplayValue',
  'queryAllByDisplayValue',
  'getByTitle',
  'getAllByTitle',
  'findByTitle',
  'findAllByTitle',
  'queryByTitle',
  'queryAllByTitle',
  'getByPlaceholder',
  'getByTestId',
  'getAllByTestId',
  'findByTestId',
  'findAllByTestId',
  'queryByTestId',
  'queryAllByTestId',
])
const disallowedSelectors = new Set([
  'input[type="file"]',
  "input[type='file']",
  '.relative.flex.h-full.min-h-0.overflow-hidden',
  '.activity-log-row',
  '.card-mnemonas',
  'aside',
  'aside, [class*="sidebar"]',
  '[class*="ModalFooter"], footer',
  '[class*="toast"], [class*="alert"], [role="alert"]',
  '[data-backup-job-id]',
  '[data-context-menu]',
  'button',
  'div',
  'audio',
  'img',
  'svg',
  'video',
])
const selectorsAllowedOnlyForQuerySelector = new Set([
  '.relative.flex.h-full.min-h-0.overflow-hidden',
  'aside',
])
const disallowedSelectorPrefixes = [
  '[data-backup-job-id=',
]

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

function callExpressionName(expression) {
  const names = callExpressionNames(expression)
  return names.length > 0 ? names[names.length - 1] : ''
}

function callExpressionNames(expression) {
  if (ts.isParenthesizedExpression(expression)) {
    return callExpressionNames(expression.expression)
  }
  if (ts.isBinaryExpression(expression) && expression.operatorToken.kind === ts.SyntaxKind.CommaToken) {
    return callExpressionNames(expression.right)
  }
  if (ts.isIdentifier(expression)) {
    return [expression.text]
  }
  if (ts.isPropertyAccessExpression(expression)) {
    return [...callExpressionNames(expression.expression), expression.name.text]
  }
  if (ts.isElementAccessExpression(expression)) {
    return [...callExpressionNames(expression.expression), stringLiteralValue(expression.argumentExpression) ?? '']
  }
  return []
}

function stringLiteralValue(node) {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
    return node.text
  }
  return null
}

function isDisallowedSelectorCall(name, selector) {
  const disallowedSelector = selector !== null
    && (disallowedSelectors.has(selector) || disallowedSelectorPrefixes.some((prefix) => selector.startsWith(prefix)))
  if (!disallowedSelector) {
    return false
  }
  if (name === 'locator') {
    return true
  }
  if (name === 'querySelector' || name === 'closest') {
    return true
  }
  return name === 'querySelectorAll' && !selectorsAllowedOnlyForQuerySelector.has(selector)
}

function selectorStringArgument(node) {
  const names = callExpressionNames(node.expression)
  const name = names.length > 0 ? names[names.length - 1] : ''
  if (name === 'call' && node.arguments.length > 1) {
    return stringLiteralValue(node.arguments[1])
  }
  if (name === 'apply' && node.arguments.length > 1 && ts.isArrayLiteralExpression(node.arguments[1]) && node.arguments[1].elements.length > 0) {
    return stringLiteralValue(node.arguments[1].elements[0])
  }
  return node.arguments.length > 0 ? stringLiteralValue(node.arguments[0]) : null
}

function selectorCallName(node) {
  const names = callExpressionNames(node.expression)
  if (names.length === 0) {
    return ''
  }
  const last = names[names.length - 1]
  if ((last === 'call' || last === 'apply') && names.length > 1) {
    return names[names.length - 2]
  }
  return last
}

function isAllTestingLibraryQueryName(name) {
  return /^(?:get|query|find)AllBy[A-Z]/.test(name)
}

function unwrapIndexedExpression(node) {
  let current = node
  while (ts.isParenthesizedExpression(current) || ts.isAwaitExpression(current)) {
    current = current.expression
  }
  return current
}

function isDirectIndexedAllQuery(node) {
  if (!ts.isElementAccessExpression(node)) {
    return false
  }

  const expression = unwrapIndexedExpression(node.expression)
  if (!ts.isCallExpression(expression)) {
    return false
  }

  return isAllTestingLibraryQueryName(callExpressionName(expression.expression))
}

function isDirectFindOnAllQuery(node) {
  if (!ts.isCallExpression(node) || !ts.isPropertyAccessExpression(node.expression)) {
    return false
  }
  if (node.expression.name.text !== 'find') {
    return false
  }

  const expression = unwrapIndexedExpression(node.expression.expression)
  if (!ts.isCallExpression(expression)) {
    return false
  }

  return isAllTestingLibraryQueryName(callExpressionName(expression.expression))
}

function collectFragileQueries(sourceFile) {
  const lines = []

  function visit(node) {
    if (isDirectIndexedAllQuery(node)) {
      lines.push(lineNumberAt(sourceFile, node.expression.expression.getStart(sourceFile)))
    }

    if (ts.isCallExpression(node)) {
      if (isDirectFindOnAllQuery(node)) {
        lines.push(lineNumberAt(sourceFile, node.expression.getStart(sourceFile)))
      }

      const names = callExpressionNames(node.expression)
      const name = selectorCallName(node)
      const selector = selectorStringArgument(node)

      if (names.some((candidate) => disallowedTestingLibraryQueries.has(candidate)) || isDisallowedSelectorCall(name, selector)) {
        lines.push(lineNumberAt(sourceFile, node.expression.getStart(sourceFile)))
      }
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

    for (const line of collectFragileQueries(sourceFile)) {
      failures.push(`${relative(webRoot, file)}:${line}`)
    }
  }
}

if (failures.length > 0) {
  console.error('Fragile test queries are not allowed. Use role, label, or text queries instead:')
  for (const failure of failures) {
    console.error(`  ${failure}`)
  }
  process.exit(1)
}

console.log('[test-query-check] no fragile queries found')
