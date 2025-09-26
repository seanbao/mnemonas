#!/usr/bin/env node

const { existsSync, readdirSync, readFileSync } = require('node:fs')
const { join, relative, resolve, sep } = require('node:path')
const ts = require('typescript')

const webRoot = process.env.MNEMONAS_WEB_ROOT ? resolve(process.env.MNEMONAS_WEB_ROOT) : join(__dirname, '..')
const srcRoot = join(webRoot, 'src')
const sourceFilePattern = /\.tsx$/
const testFilePattern = /\.(?:test|spec)\.tsx$/

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

function jsxAttributeName(attribute, sourceFile) {
  return attribute.name.getText(sourceFile)
}

function findJsxAttribute(attributes, sourceFile, name) {
  for (const attribute of attributes.properties) {
    if (!ts.isJsxAttribute(attribute)) {
      continue
    }
    if (jsxAttributeName(attribute, sourceFile) === name) {
      return attribute
    }
  }
  return null
}

function attributeHasNonEmptyValue(attribute) {
  if (!attribute) {
    return false
  }
  if (!attribute.initializer) {
    return true
  }
  if (ts.isStringLiteral(attribute.initializer)) {
    return attribute.initializer.text.trim().length > 0
  }
  if (!ts.isJsxExpression(attribute.initializer) || !attribute.initializer.expression) {
    return false
  }
  const expression = attribute.initializer.expression
  if (ts.isStringLiteral(expression) || ts.isNoSubstitutionTemplateLiteral(expression)) {
    return expression.text.trim().length > 0
  }
  if (expression.kind === ts.SyntaxKind.FalseKeyword || expression.kind === ts.SyntaxKind.NullKeyword) {
    return false
  }
  if (ts.isIdentifier(expression) && expression.text === 'undefined') {
    return false
  }
  return true
}

function attributeIsTruthy(attribute) {
  if (!attribute) {
    return false
  }
  if (!attribute.initializer) {
    return true
  }
  if (ts.isStringLiteral(attribute.initializer)) {
    return attribute.initializer.text.toLowerCase() !== 'false'
  }
  if (!ts.isJsxExpression(attribute.initializer) || !attribute.initializer.expression) {
    return true
  }
  const expression = attribute.initializer.expression
  if (expression.kind === ts.SyntaxKind.FalseKeyword) {
    return false
  }
  return true
}

function isButtonElement(node) {
  return ts.isIdentifier(node.tagName) && node.tagName.text === 'Button'
}

function isNativeButtonElement(node) {
  return ts.isIdentifier(node.tagName) && node.tagName.text === 'button'
}

function hasAccessibleName(attributes, sourceFile) {
  return (
    attributeHasNonEmptyValue(findJsxAttribute(attributes, sourceFile, 'aria-label')) ||
    attributeHasNonEmptyValue(findJsxAttribute(attributes, sourceFile, 'aria-labelledby'))
  )
}

function isLikelyIconExpressionName(name) {
  return /^(?:icon|startContent|endContent)$/i.test(name) || /Icon$/.test(name)
}

function expressionMayRenderText(expression) {
  if (ts.isParenthesizedExpression(expression)) {
    return expressionMayRenderText(expression.expression)
  }
  if (ts.isStringLiteral(expression) || ts.isNoSubstitutionTemplateLiteral(expression)) {
    return expression.text.trim().length > 0
  }
  if (
    expression.kind === ts.SyntaxKind.FalseKeyword ||
    expression.kind === ts.SyntaxKind.TrueKeyword ||
    expression.kind === ts.SyntaxKind.NullKeyword
  ) {
    return false
  }
  if (ts.isIdentifier(expression)) {
    if (expression.text === 'undefined') {
      return false
    }
    if (isLikelyIconExpressionName(expression.text)) {
      return false
    }
    return true
  }
  if (ts.isPropertyAccessExpression(expression)) {
    return !isLikelyIconExpressionName(expression.name.text)
  }
  if (ts.isJsxElement(expression) || ts.isJsxSelfClosingElement(expression) || ts.isJsxFragment(expression)) {
    return jsxNodeContainsText(expression)
  }
  return true
}

function jsxChildrenContainText(children) {
  for (const child of children) {
    if (ts.isJsxText(child) && child.getText().trim().length > 0) {
      return true
    }
    if (ts.isJsxExpression(child)) {
      if (!child.expression) {
        continue
      }
      if (expressionMayRenderText(child.expression)) {
        return true
      }
      continue
    }
    if ((ts.isJsxElement(child) || ts.isJsxFragment(child)) && jsxNodeContainsText(child)) {
      return true
    }
  }
  return false
}

function jsxNodeContainsText(node) {
  if (ts.isJsxElement(node)) {
    return jsxChildrenContainText(node.children)
  }
  if (ts.isJsxFragment(node)) {
    return jsxChildrenContainText(node.children)
  }
  return false
}

function collectMissingIconButtonLabels(sourceFile) {
  const missing = []

  function visit(node) {
    const isButtonOpening = (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) && isButtonElement(node)
    if (isButtonOpening) {
      const attributes = node.attributes
      const isIconOnly = attributeIsTruthy(findJsxAttribute(attributes, sourceFile, 'isIconOnly'))
      if (isIconOnly && !hasAccessibleName(attributes, sourceFile)) {
        missing.push(lineNumberAt(sourceFile, node.getStart(sourceFile)))
      }
    }

    if (ts.isJsxElement(node) && isNativeButtonElement(node.openingElement)) {
      const attributes = node.openingElement.attributes
      if (!hasAccessibleName(attributes, sourceFile) && !jsxChildrenContainText(node.children)) {
        missing.push(lineNumberAt(sourceFile, node.openingElement.getStart(sourceFile)))
      }
    }

    if (ts.isJsxSelfClosingElement(node) && isNativeButtonElement(node)) {
      if (!hasAccessibleName(node.attributes, sourceFile)) {
        missing.push(lineNumberAt(sourceFile, node.getStart(sourceFile)))
      }
    }

    ts.forEachChild(node, visit)
  }

  visit(sourceFile)
  return missing
}

const failures = []

for (const file of listSourceFiles(srcRoot)) {
  const source = readFileSync(file, 'utf8')
  const sourceFile = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)

  for (const line of collectMissingIconButtonLabels(sourceFile)) {
    failures.push(`${relative(webRoot, file)}:${line}`)
  }
}

if (failures.length > 0) {
  console.error('Icon-only buttons must declare aria-label or aria-labelledby:')
  for (const failure of failures) {
    console.error(`  ${failure}`)
  }
  process.exit(1)
}

console.log('[icon-button-label-check] all icon-only buttons have accessible labels')
