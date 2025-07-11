#!/usr/bin/env node

import { ESLint } from 'eslint'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const fixturePath = path.join(webRoot, 'src', '__eslint_button_type_fixture.tsx')
const expectedMessage = 'Native button elements must declare type="button", type="submit", or type="reset".'

const eslint = new ESLint({ cwd: webRoot })

async function lintFixture(source) {
  const results = await eslint.lintText(source, { filePath: fixturePath })
  return results.flatMap((result) => result.messages)
}

const invalidMessages = await lintFixture(`
export function MissingButtonType() {
  return <button onClick={() => undefined}>Save</button>
}
`)

if (!invalidMessages.some((message) => message.message === expectedMessage)) {
  console.error('button type lint rule did not reject a native button without an explicit type')
  process.exit(1)
}

const validMessages = await lintFixture(`
export function ExplicitButtonType() {
  return <button type="button" onClick={() => undefined}>Save</button>
}
`)

if (validMessages.some((message) => message.message === expectedMessage)) {
  console.error('button type lint rule rejected a native button with an explicit type')
  process.exit(1)
}

console.log('[eslint-button-rule-check] native button type rule is enforced')
