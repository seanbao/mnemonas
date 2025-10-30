#!/usr/bin/env node

const { existsSync, readdirSync, readFileSync } = require('node:fs')
const { join, relative, resolve, sep } = require('node:path')
const ts = require('typescript')

const webRoot = process.env.MNEMONAS_WEB_ROOT ? resolve(process.env.MNEMONAS_WEB_ROOT) : join(__dirname, '..')
const srcRoot = join(webRoot, 'src')
const sourceFilePattern = /\.[cm]?[tj]sx?$/
const testFilePattern = /\.(?:test|spec)\.[cm]?[tj]sx?$/
const forbiddenNames = new Set(['data-testid', 'dataTestId'])

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

function shouldSkipFile(file) {
  const parts = relative(srcRoot, file).split(sep)
  return testFilePattern.test(file) || parts.includes('test') || parts.includes('__mocks__')
}

function lineNumberAt(sourceFile, index) {
  return sourceFile.getLineAndCharacterOfPosition(index).line + 1
}

function jsxAttributeName(attribute, sourceFile) {
  return attribute.name.getText(sourceFile)
}

function isForbiddenStringLiteral(node) {
  return (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) && forbiddenNames.has(node.text)
}

function collectProductionTestIds(sourceFile) {
  const lines = new Set()

  function add(node) {
    lines.add(lineNumberAt(sourceFile, node.getStart(sourceFile)))
  }

  function visit(node) {
    if (ts.isJsxAttribute(node) && forbiddenNames.has(jsxAttributeName(node, sourceFile))) {
      add(node)
    }

    if (ts.isIdentifier(node) && forbiddenNames.has(node.text)) {
      add(node)
    }

    if (isForbiddenStringLiteral(node)) {
      add(node)
    }

    ts.forEachChild(node, visit)
  }

  visit(sourceFile)
  return [...lines].sort((left, right) => left - right)
}

const failures = []

for (const file of listSourceFiles(srcRoot)) {
  const source = readFileSync(file, 'utf8')
  const sourceFile = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)

  for (const line of collectProductionTestIds(sourceFile)) {
    failures.push(`${relative(webRoot, file)}:${line}`)
  }
}

if (failures.length > 0) {
  console.error('Production source files must not use test-only data-testid hooks:')
  for (const failure of failures) {
    console.error(`  ${failure}`)
  }
  process.exit(1)
}

console.log('[production-testid-check] no production data-testid hooks found')
