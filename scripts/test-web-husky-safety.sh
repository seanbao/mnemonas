#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
  printf '[web-husky-safety-test] ERROR: %s\n' "$*" >&2
  exit 1
}

assert_file_contains() {
  local path="$1"
  local expected="$2"

  grep -Fq -- "$expected" "$path" || fail "$path does not contain: $expected"
}

assert_file_not_contains() {
  local path="$1"
  local unexpected="$2"

  ! grep -Fq -- "$unexpected" "$path" || fail "$path unexpectedly contains: $unexpected"
}

assert_file_contains_before() {
  local path="$1"
  local first="$2"
  local second="$3"
  local first_line
  local second_line

  first_line="$(grep -Fn -- "$first" "$path" | head -n 1 | cut -d: -f1 || true)"
  second_line="$(grep -Fn -- "$second" "$path" | head -n 1 | cut -d: -f1 || true)"

  [[ -n "$first_line" ]] || fail "$path does not contain: $first"
  [[ -n "$second_line" ]] || fail "$path does not contain: $second"
  [[ "$first_line" -lt "$second_line" ]] || fail "$path does not place '$first' before '$second'"
}

assert_file_executable() {
  local path="$1"

  [[ -x "$path" ]] || fail "$path is not executable"
}

write_executable() {
  local path="$1"
  shift

  printf '%s\n' "$@" > "$path"
  chmod +x "$path"
}

read_prepare_script() {
  python3 - "$REPO_ROOT/web/package.json" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    print(json.load(handle)["scripts"]["prepare"])
PY
}

run_frontend_commands_have_node_engine_prechecks() {
  python3 - "$REPO_ROOT/web/package.json" <<'PY'
import json
import sys

required = [
    "dev",
    "build",
    "lint",
    "preview",
    "typecheck",
    "typecheck:e2e",
]
test_commands = [
    "test",
    "test:ui",
    "test:run",
    "test:coverage",
    "test:e2e",
    "test:e2e:navigation",
    "test:e2e:ui",
    "test:e2e:update",
]

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    scripts = json.load(handle)["scripts"]

if scripts.get("check:node") != "node ./scripts/check-node.cjs":
    print("check:node must run the frontend Node engine checker", file=sys.stderr)
    sys.exit(1)

missing = []
unexpected = []
for name in required:
    pre_name = f"pre{name}"
    value = scripts.get(pre_name)
    if value is None:
        missing.append(pre_name)
        continue
    if value != "node ./scripts/check-node.cjs":
        unexpected.append(f"{pre_name}={value!r}")

for name in test_commands:
    pre_name = f"pre{name}"
    value = scripts.get(pre_name)
    if value is None:
        missing.append(pre_name)
        continue
    if value != "node ./scripts/check-node.cjs && npm run check:test-focus":
        unexpected.append(f"{pre_name}={value!r}")

if missing or unexpected:
    for item in missing:
        print(f"missing frontend Node engine precheck: {item}", file=sys.stderr)
    for item in unexpected:
        print(f"unexpected frontend Node engine precheck: {item}", file=sys.stderr)
    sys.exit(1)
PY
}

run_node_engine_checker_reads_package_engine() {
  local case_dir="$TMP_ROOT/node-engine-checker"
  local out="$case_dir/out.log"
  local status
  mkdir -p "$case_dir/web"

  assert_file_contains "$REPO_ROOT/web/scripts/check-node.cjs" "packageJson.engines?.node"
  assert_file_not_contains "$REPO_ROOT/web/scripts/check-node.cjs" "Node.js ^20.19.0 or >=22.12.0 is required"

  printf '%s\n' '{"engines":{"node":">=0.0.0"}}' > "$case_dir/web/package.json"
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-node.cjs" > "$out" 2>&1

  printf '%s\n' '{"engines":{"node":">=999.0.0"}}' > "$case_dir/web/package.json"
  set +e
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-node.cjs" > "$out" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "Node engine checker accepted an impossible package engine"
  assert_file_contains "$out" "Node.js >=999.0.0 is required for web commands"
}

run_lint_checks_node_tool_scripts_and_button_rule() {
  python3 - "$REPO_ROOT/web/package.json" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    scripts = json.load(handle)["scripts"]

lint = scripts.get("lint", "")
check_scripts = scripts.get("check:scripts", "")
check_identity = scripts.get("check:identity", "")
check_queries = scripts.get("check:queries", "")
check_testids = scripts.get("check:testids", "")
check_ts_suppressions = scripts.get("check:ts-suppressions", "")
check_console = scripts.get("check:console", "")
check_a11y = scripts.get("check:a11y", "")

if "npm run check:scripts" not in lint:
    print("frontend lint must run check:scripts before ESLint", file=sys.stderr)
    sys.exit(1)

if "npm run check:identity" not in lint:
    print("frontend lint must run check:identity before ESLint", file=sys.stderr)
    sys.exit(1)

if "npm run check:queries" not in lint:
    print("frontend lint must reject ambiguous Testing Library queries before ESLint", file=sys.stderr)
    sys.exit(1)

if "npm run check:test-focus" not in lint:
    print("frontend lint must reject focused tests before ESLint", file=sys.stderr)
    sys.exit(1)

if "npm run check:testids" not in lint:
    print("frontend lint must reject production data-testid hooks before ESLint", file=sys.stderr)
    sys.exit(1)

if "npm run check:ts-suppressions" not in lint:
    print("frontend lint must reject production TypeScript suppressions before ESLint", file=sys.stderr)
    sys.exit(1)

if "npm run check:console" not in lint:
    print("frontend lint must reject unguarded production console usage before ESLint", file=sys.stderr)
    sys.exit(1)

if "npm run check:a11y" not in lint:
    print("frontend lint must reject unlabeled icon buttons before ESLint", file=sys.stderr)
    sys.exit(1)

if "node ./scripts/check-eslint-button-rule.mjs" not in lint:
    print("frontend lint must execute the native button type rule self-check", file=sys.stderr)
    sys.exit(1)

if check_scripts != "node ./scripts/check-node-scripts.cjs":
    print("check:scripts must use the auto-discovering Node tool script checker", file=sys.stderr)
    sys.exit(1)

if check_identity != "node ./scripts/check-project-identity.cjs":
    print("check:identity must use the project identity checker", file=sys.stderr)
    sys.exit(1)

if check_queries != "node ./scripts/check-testing-library-queries.cjs":
    print("check:queries must use the Testing Library query checker", file=sys.stderr)
    sys.exit(1)

if check_testids != "node ./scripts/check-production-testids.cjs":
    print("check:testids must use the production data-testid checker", file=sys.stderr)
    sys.exit(1)

if check_ts_suppressions != "node ./scripts/check-production-ts-suppressions.cjs":
    print("check:ts-suppressions must use the production TypeScript suppression checker", file=sys.stderr)
    sys.exit(1)

if check_console != "node ./scripts/check-production-console.cjs":
    print("check:console must use the production console checker", file=sys.stderr)
    sys.exit(1)

if check_a11y != "node ./scripts/check-icon-button-labels.cjs":
    print("check:a11y must use the icon button accessibility checker", file=sys.stderr)
    sys.exit(1)
PY
}

run_production_testid_checker_rejects_source_hooks() {
  local case_dir="$TMP_ROOT/production-testid-checker"
  local out="$case_dir/out.log"
  local status
  mkdir -p "$case_dir/web/src/components" "$case_dir/web/src/test/__mocks__"

  printf '%s\n' \
    'export function Widget() {' \
    "  const dataTestId = 'widget'" \
    '  return <div data-testid="widget" />' \
    '}' > "$case_dir/web/src/components/Widget.tsx"
  printf '%s\n' \
    'it("allows test hooks in tests", () => {' \
    '  expect(<div data-testid="test-hook" />).toBeTruthy()' \
    '})' > "$case_dir/web/src/components/Widget.test.tsx"
  printf '%s\n' \
    'export function MockWidget() {' \
    '  return <div data-testid="mock-hook" />' \
    '}' > "$case_dir/web/src/test/__mocks__/Widget.tsx"

  set +e
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-production-testids.cjs" > "$out" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "production data-testid checker accepted source hooks"
  assert_file_contains "$out" "src/components/Widget.tsx:2"
  assert_file_contains "$out" "src/components/Widget.tsx:3"

  printf '%s\n' \
    'export function Widget() {' \
    '  return <div role="group" aria-label="widget" />' \
    '}' > "$case_dir/web/src/components/Widget.tsx"
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-production-testids.cjs" > "$out" 2>&1
  assert_file_contains "$out" "[production-testid-check] no production data-testid hooks found"
}

run_testing_library_query_checker_rejects_testid_queries() {
  local case_dir="$TMP_ROOT/testing-library-query-checker"
  local out="$case_dir/out.log"
  local status
  mkdir -p "$case_dir/web/src/components" "$case_dir/web/e2e"

  printf '%s\n' \
    'import { screen } from "@testing-library/react"' \
    'it("uses a test id", () => {' \
    '  screen.getByTestId("legacy-hook")' \
    '  screen["queryByTestId"]("legacy-hook")' \
    '  screen.getByTestId.call(screen, "legacy-hook")' \
    '  ;(0, screen.findByTestId)("legacy-hook")' \
    '})' > "$case_dir/web/src/components/Widget.test.tsx"

  printf '%s\n' \
    'import { test } from "@playwright/test"' \
    'test("uses a fragile locator", async ({ page }) => {' \
    '  await page["locator"]("button").click()' \
    '  await page.locator.call(page, "button").click()' \
    '  await page["locator"].apply(page, ["button"]).click()' \
    '})' > "$case_dir/web/e2e/fragile.spec.ts"

  set +e
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-testing-library-queries.cjs" > "$out" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "Testing Library query checker accepted a test id query"
  assert_file_contains "$out" "src/components/Widget.test.tsx:3"
  assert_file_contains "$out" "src/components/Widget.test.tsx:4"
  assert_file_contains "$out" "src/components/Widget.test.tsx:5"
  assert_file_contains "$out" "src/components/Widget.test.tsx:6"
  assert_file_contains "$out" "e2e/fragile.spec.ts:3"
  assert_file_contains "$out" "e2e/fragile.spec.ts:4"
  assert_file_contains "$out" "e2e/fragile.spec.ts:5"

  printf '%s\n' \
    'import { screen } from "@testing-library/react"' \
    'it("uses an accessible role", () => {' \
    '  screen.getByRole("button", { name: "保存" })' \
    '})' > "$case_dir/web/src/components/Widget.test.tsx"
  rm -f "$case_dir/web/e2e/fragile.spec.ts"
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-testing-library-queries.cjs" > "$out" 2>&1
  assert_file_contains "$out" "[test-query-check] no fragile queries found"
}

run_production_ts_suppression_checker_rejects_source_suppressions() {
  local case_dir="$TMP_ROOT/production-ts-suppression-checker"
  local out="$case_dir/out.log"
  local status
  mkdir -p "$case_dir/web/src/components" "$case_dir/web/src/test"

  printf '%s\n' \
    '// @ts-nocheck' \
    'export function Widget(value: unknown) {' \
    '  // @ts-expect-error deliberate fixture' \
    '  return value.missing' \
    '}' > "$case_dir/web/src/components/Widget.tsx"
  printf '%s\n' \
    'it("allows test suppressions", () => {' \
    '  // @ts-ignore deliberate fixture' \
    '  expect(globalThis.missing).toBeUndefined()' \
    '})' > "$case_dir/web/src/components/Widget.test.tsx"
  printf '%s\n' \
    'export const value = 1' > "$case_dir/web/src/test/helper.ts"

  set +e
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-production-ts-suppressions.cjs" > "$out" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "production TypeScript suppression checker accepted a source suppression"
  assert_file_contains "$out" "src/components/Widget.tsx:1"
  assert_file_contains "$out" "src/components/Widget.tsx:3"

  printf '%s\n' \
    'export function Widget(value: { missing?: unknown }) {' \
    '  return value.missing' \
    '}' > "$case_dir/web/src/components/Widget.tsx"
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-production-ts-suppressions.cjs" > "$out" 2>&1
  assert_file_contains "$out" "[production-ts-suppression-check] no production TypeScript suppressions found"
}

run_production_console_checker_rejects_unguarded_source_usage() {
  local case_dir="$TMP_ROOT/production-console-checker"
  local out="$case_dir/out.log"
  local status
  mkdir -p "$case_dir/web/src/components" "$case_dir/web/src/test/__mocks__"

  printf '%s\n' \
    'export function Widget() {' \
    '  console.log("visible in production")' \
    '  window.console.warn("visible through window")' \
    '  globalThis.console.error("visible through globalThis")' \
    '  console["debug"]("visible through element access")' \
    '  console.log.call(console, "visible through call")' \
    '  window.console.warn.apply(window.console, ["visible through apply"])' \
    '  ;(console.info)("visible through parenthesized call")' \
    '  ;(0, console.error)("visible through indirect call")' \
    '  debugger' \
    '  return <div />' \
    '}' > "$case_dir/web/src/components/Widget.tsx"
  printf '%s\n' \
    'it("allows test console usage", () => {' \
    '  console.log("test only")' \
    '})' > "$case_dir/web/src/components/Widget.test.tsx"
  printf '%s\n' \
    'export function MockWidget() {' \
    '  console.log("mock only")' \
    '  return <div />' \
    '}' > "$case_dir/web/src/test/__mocks__/Widget.tsx"

  set +e
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-production-console.cjs" > "$out" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "production console checker accepted unguarded source usage"
  assert_file_contains "$out" "src/components/Widget.tsx:2"
  assert_file_contains "$out" "src/components/Widget.tsx:3"
  assert_file_contains "$out" "src/components/Widget.tsx:4"
  assert_file_contains "$out" "src/components/Widget.tsx:5"
  assert_file_contains "$out" "src/components/Widget.tsx:6"
  assert_file_contains "$out" "src/components/Widget.tsx:7"
  assert_file_contains "$out" "src/components/Widget.tsx:8"
  assert_file_contains "$out" "src/components/Widget.tsx:9"
  assert_file_contains "$out" "src/components/Widget.tsx:10"

  printf '%s\n' \
    'export function Widget() {' \
    '  if (import.meta.env.DEV) {' \
    '    console.error("development diagnostics only")' \
    '  }' \
    '  return <div />' \
    '}' > "$case_dir/web/src/components/Widget.tsx"
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-production-console.cjs" > "$out" 2>&1
  assert_file_contains "$out" "[production-console-check] no unguarded production console or debugger usage found"
}

run_playwright_forbids_focused_tests_locally() {
  assert_file_contains "$REPO_ROOT/web/playwright.config.ts" "forbidOnly: true"
  assert_file_not_contains "$REPO_ROOT/web/playwright.config.ts" "forbidOnly: !!process.env.CI"
}

run_vitest_forbids_focused_tests_locally() {
  assert_file_contains "$REPO_ROOT/web/vitest.config.ts" "allowOnly: false"
  assert_file_not_contains "$REPO_ROOT/web/vitest.config.ts" "allowOnly: !process.env.CI"
  assert_file_not_contains "$REPO_ROOT/web/vitest.config.ts" "allowOnly: !!process.env.CI"
}

run_node_tool_script_checker_covers_supported_extensions() {
  python3 - "$REPO_ROOT/web/scripts/check-node-scripts.cjs" <<'PY'
import sys

source = open(sys.argv[1], "r", encoding="utf-8").read()

for extension in ("'.cjs'", "'.js'", "'.mjs'"):
    if extension not in source:
        print(f"Node tool script checker must include {extension}", file=sys.stderr)
        sys.exit(1)

if "readdirSync(scriptsDir" not in source:
    print("Node tool script checker must discover scripts from web/scripts", file=sys.stderr)
    sys.exit(1)
PY
}

run_node_tool_script_checker_rejects_invalid_scripts() {
  local case_dir="$TMP_ROOT/node-script-checker"
  local out="$case_dir/out.log"
  local status
  mkdir -p "$case_dir/web/scripts"

  printf '%s\n' 'const ok = true' > "$case_dir/web/scripts/valid.cjs"
  printf '%s\n' 'console.log("ok")' > "$case_dir/web/scripts/valid.js"
  printf '%s\n' 'export const ok = true' > "$case_dir/web/scripts/valid.mjs"
  printf '%s\n' 'const broken =' > "$case_dir/web/scripts/broken.cjs"
  printf '%s\n' '#!/usr/bin/env bash' 'echo ignored' > "$case_dir/web/scripts/ignored.sh"

  set +e
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-node-scripts.cjs" > "$out" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "Node tool script checker accepted invalid JavaScript"
  assert_file_contains "$out" "broken.cjs"

  rm "$case_dir/web/scripts/broken.cjs"

  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-node-scripts.cjs" > "$out" 2>&1
  assert_file_contains "$out" "[node-script-check] checked 3 Node tool scripts"
}

run_icon_button_label_checker_uses_tsx_ast() {
  local case_dir="$TMP_ROOT/icon-button-label-checker"
  local out="$case_dir/out.log"
  local status
  mkdir -p "$case_dir/web/src" "$case_dir/web/src/test/__mocks__" "$case_dir/web/src/components/__mocks__"

  cat > "$case_dir/web/src/IconButtons.tsx" <<'EOF'
export function InvalidIconButton() {
  return (
    <Button isIconOnly>
      <span />
    </Button>
  )
}
EOF

  cat > "$case_dir/web/src/NativeIconButtons.tsx" <<'EOF'
export function InvalidNativeIconButton() {
  return (
    <button type="button">
      <span />
    </button>
  )
}
EOF

  cat > "$case_dir/web/src/NativeDynamicIconButtons.tsx" <<'EOF'
export function InvalidDynamicNativeIconButton({ icon }: { icon: React.ReactNode }) {
  return (
    <button type="button">{icon}</button>
  )
}
EOF

  cat > "$case_dir/web/src/EmptyAccessibleNames.tsx" <<'EOF'
export function InvalidEmptyAccessibleNames() {
  return (
    <>
      <Button isIconOnly aria-label="   ">
        <span />
      </Button>
      <button type="button" aria-labelledby={''}>
        <span />
      </button>
      <button type="button" aria-label={undefined}>
        <span />
      </button>
      <button type="button" />
    </>
  )
}
EOF

  cat > "$case_dir/web/src/NativeFalsyTextButtons.tsx" <<'EOF'
export function InvalidNativeFalsyTextButtons() {
  return (
    <>
      <button type="button">{false}</button>
      <button type="button">{null}</button>
      <button type="button">{undefined}</button>
      <button type="button">{true}</button>
    </>
  )
}
EOF

  cat > "$case_dir/web/src/test/__mocks__/MockIconButtons.tsx" <<'EOF'
export function IgnoredTestMockIconButton() {
  return (
    <Button isIconOnly>
      <span />
    </Button>
  )
}
EOF

  cat > "$case_dir/web/src/components/__mocks__/MockNativeIconButtons.tsx" <<'EOF'
export function IgnoredComponentMockIconButton() {
  return (
    <button type="button">
      <span />
    </button>
  )
}
EOF

  set +e
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-icon-button-labels.cjs" > "$out" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "icon button label checker accepted an unlabeled icon-only button"
  assert_file_contains "$out" "Icon-only buttons must declare aria-label or aria-labelledby"
  assert_file_contains "$out" "IconButtons.tsx:3"
  assert_file_contains "$out" "NativeIconButtons.tsx:3"
  assert_file_contains "$out" "NativeDynamicIconButtons.tsx:3"
  assert_file_contains "$out" "EmptyAccessibleNames.tsx:"
  assert_file_contains "$out" "NativeFalsyTextButtons.tsx:4"
  assert_file_contains "$out" "NativeFalsyTextButtons.tsx:5"
  assert_file_contains "$out" "NativeFalsyTextButtons.tsx:6"
  assert_file_contains "$out" "NativeFalsyTextButtons.tsx:7"

  cat > "$case_dir/web/src/IconButtons.tsx" <<'EOF'
const documentationExample = '<Button isIconOnly />'

/*
 * <Button isIconOnly />
 */
export function ValidIconButtons({ refreshLabelId }: { refreshLabelId: string }) {
  return (
    <>
      <Button isIconOnly aria-label="Refresh">
        <span />
      </Button>
      <Button isIconOnly aria-labelledby={refreshLabelId}>
        <span />
      </Button>
      <Button isIconOnly={false}>
        <span />
      </Button>
    </>
  )
}
EOF

  cat > "$case_dir/web/src/NativeIconButtons.tsx" <<'EOF'
export function ValidNativeIconButtons({ label }: { label: string }) {
  return (
    <>
      <button type="button" aria-label={label}>
        <span />
      </button>
      <button type="button">
        <span>Open</span>
      </button>
      <button type="button">{label}</button>
      <button type="button">{0}</button>
    </>
  )
}
EOF

  rm "$case_dir/web/src/NativeDynamicIconButtons.tsx"
  rm "$case_dir/web/src/EmptyAccessibleNames.tsx"
  rm "$case_dir/web/src/NativeFalsyTextButtons.tsx"

  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-icon-button-labels.cjs" > "$out" 2>&1
  assert_file_contains "$out" "[icon-button-label-check] all icon-only buttons have accessible labels"
}

run_test_query_checker_uses_typescript_ast() {
  local case_dir="$TMP_ROOT/test-query-checker"
  local out="$case_dir/out.log"
  local status
  mkdir -p "$case_dir/web/src/components" "$case_dir/web/e2e"

  cat > "$case_dir/web/src/components/Fragile.test.tsx" <<'EOF'
import { screen } from '@testing-library/react'

test('rejects fragile queries', async () => {
  screen.getByTitle('Details')
  screen.getAllByRole('button', { name: 'Details' })[0]
  ;(await screen.findAllByRole('button', { name: 'Details' }))[0]
  screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('Details'))
})
EOF

  cat > "$case_dir/web/e2e/fragile.spec.ts" <<'EOF'
test('rejects brittle selectors', async ({ page }) => {
  await page.evaluate(() => document.querySelector('[data-context-menu]'))
  await page.locator('[data-context-menu]').click()
  await page.locator('.activity-log-row').first().isVisible()
  await page.locator('.card-mnemonas').first().isVisible()
  await page.locator('aside, [class*="sidebar"]').first().isVisible()
  await page.locator('[class*="toast"], [class*="alert"], [role="alert"]').isVisible()
  document.body.querySelector('[data-backup-job-id="external-disk"]')
  document.body.closest('[class*="ModalFooter"], footer')
  document.body.closest('button')
  document.body.closest('div')
  document.body.querySelector('audio')
  document.body.querySelector('img')
  document.body.querySelector('svg')
  document.body.querySelector('video')
})
EOF

  set +e
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-testing-library-queries.cjs" > "$out" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "test query checker accepted fragile queries"
  assert_file_contains "$out" "src/components/Fragile.test.tsx:4"
  assert_file_contains "$out" "src/components/Fragile.test.tsx:5"
  assert_file_contains "$out" "src/components/Fragile.test.tsx:6"
  assert_file_contains "$out" "src/components/Fragile.test.tsx:7"
  assert_file_contains "$out" "e2e/fragile.spec.ts:2"
  assert_file_contains "$out" "e2e/fragile.spec.ts:3"
  assert_file_contains "$out" "e2e/fragile.spec.ts:4"
  assert_file_contains "$out" "e2e/fragile.spec.ts:5"
  assert_file_contains "$out" "e2e/fragile.spec.ts:6"
  assert_file_contains "$out" "e2e/fragile.spec.ts:7"
  assert_file_contains "$out" "e2e/fragile.spec.ts:8"
  assert_file_contains "$out" "e2e/fragile.spec.ts:9"
  assert_file_contains "$out" "e2e/fragile.spec.ts:10"
  assert_file_contains "$out" "e2e/fragile.spec.ts:11"
  assert_file_contains "$out" "e2e/fragile.spec.ts:12"
  assert_file_contains "$out" "e2e/fragile.spec.ts:13"
  assert_file_contains "$out" "e2e/fragile.spec.ts:14"
  assert_file_contains "$out" "e2e/fragile.spec.ts:15"

  cat > "$case_dir/web/src/components/Fragile.test.tsx" <<'EOF'
const documentationExample = 'screen.getByTitle("Details")'
const indexedAllQueryExample = "screen.getAllByRole('button')[0]"
const awaitedIndexedAllQueryExample = "(await screen.findAllByRole('button'))[0]"
const findAllQueryExample = "screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('Details'))"

/*
 * document.querySelector('[data-context-menu]')
 * page.locator('[data-context-menu]')
 * page.locator('.activity-log-row')
 * page.locator('.card-mnemonas')
 * page.locator('aside, [class*="sidebar"]')
 * page.locator('[class*="toast"], [class*="alert"], [role="alert"]')
 * document.body.querySelector('[data-backup-job-id="external-disk"]')
 * document.body.closest('[class*="ModalFooter"], footer')
 * document.body.closest('button')
 * document.body.closest('div')
 * document.body.querySelector('audio')
 * document.body.querySelector('img')
 * document.body.querySelector('svg')
 * document.body.querySelector('video')
 */
test('allows accessible queries', () => {
  screen.getByRole('button', { name: 'Details' })
})
EOF

  cat > "$case_dir/web/e2e/fragile.spec.ts" <<'EOF'
test('allows resilient selectors', async ({ page }) => {
  await page.getByRole('button', { name: 'Details' }).click()
})
EOF

  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-testing-library-queries.cjs" > "$out" 2>&1
  assert_file_contains "$out" "[test-query-check] no fragile queries found"
}

run_project_identity_checker_rejects_stale_identity() {
  local case_dir="$TMP_ROOT/project-identity-checker"
  local out="$case_dir/out.log"
  local status
  local old_project_name
  local old_project_name_lower
  mkdir -p "$case_dir/web/src"

  old_project_name="Meri""dian"
  old_project_name_lower="$(printf '%s' "$old_project_name" | tr '[:upper:]' '[:lower:]')"

  cat > "$case_dir/web/package.json" <<'EOF'
{
  "name": "mnemonas-web"
}
EOF

  cat > "$case_dir/web/package-lock.json" <<'EOF'
{
  "name": "mnemonas-web",
  "lockfileVersion": 3,
  "packages": {
    "": {
      "name": "mnemonas-web"
    }
  }
}
EOF

  printf '%s\n' 'export const currentClass = "card-mnemonas"' > "$case_dir/web/src/Identity.tsx"
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-project-identity.cjs" > "$out" 2>&1
  assert_file_contains "$out" "[project-identity-check] package mnemonas-web and frontend identity terms are consistent"

  printf 'export const staleProjectName = "%s"\n' "$old_project_name" > "$case_dir/web/src/Identity.tsx"
  set +e
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-project-identity.cjs" > "$out" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "project identity checker accepted stale project name"
  assert_file_contains "$out" "src/Identity.tsx:1: $old_project_name"

  printf 'export const staleClass = "%s"\n' "card-$old_project_name_lower" > "$case_dir/web/src/Identity.tsx"
  set +e
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-project-identity.cjs" > "$out" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "project identity checker accepted stale class name"
  assert_file_contains "$out" "src/Identity.tsx:1: card-$old_project_name_lower"

  cat > "$case_dir/web/src/Identity.tsx" <<'EOF'
export const currentClass = "card-mnemonas"
EOF
  cat > "$case_dir/web/package.json" <<'EOF'
{
  "name": "web"
}
EOF

  set +e
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-project-identity.cjs" > "$out" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "project identity checker accepted generic package name"
  assert_file_contains "$out" 'web/package.json name = "web", expected "mnemonas-web"'
}

run_test_focus_checker_uses_typescript_ast() {
  local case_dir="$TMP_ROOT/test-focus-checker"
  local out="$case_dir/out.log"
  local status
  mkdir -p "$case_dir/web/src/components" "$case_dir/web/e2e"

  cat > "$case_dir/web/src/components/Focused.test.tsx" <<'EOF'
import { describe, fit, test } from 'vitest'

fdescribe('focused suite', () => {})
fit('focused alias', () => {})
test.only('focused test', () => {})
describe.concurrent.only('focused concurrent suite', () => {})
;(test.only)('parenthesized focused test', () => {})
test["only"]('indexed focused test', () => {})
;(0, test.only)('indirect focused test', () => {})
test.only.call(test, 'call focused test', () => {})
test["only"].apply(test, ['apply focused test', () => {}])
test.only.each([[1]])('each focused test', () => {})
EOF

  cat > "$case_dir/web/e2e/focused.spec.ts" <<'EOF'
test.only('focused E2E test', async ({ page }) => {
  await page.goto('/')
})
EOF

  set +e
  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-test-focus.cjs" > "$out" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "test focus checker accepted focused tests"
  assert_file_contains "$out" "src/components/Focused.test.tsx:3"
  assert_file_contains "$out" "src/components/Focused.test.tsx:4"
  assert_file_contains "$out" "src/components/Focused.test.tsx:5"
  assert_file_contains "$out" "src/components/Focused.test.tsx:6"
  assert_file_contains "$out" "src/components/Focused.test.tsx:7"
  assert_file_contains "$out" "src/components/Focused.test.tsx:8"
  assert_file_contains "$out" "src/components/Focused.test.tsx:9"
  assert_file_contains "$out" "src/components/Focused.test.tsx:10"
  assert_file_contains "$out" "src/components/Focused.test.tsx:11"
  assert_file_contains "$out" "src/components/Focused.test.tsx:12"
  assert_file_contains "$out" "e2e/focused.spec.ts:1"

  cat > "$case_dir/web/src/components/Focused.test.tsx" <<'EOF'
const documentationExample = 'test.only("example", () => {})'

/*
 * fit('example', () => {})
 */
test('normal test', () => {})
describe.concurrent('normal suite', () => {})
EOF

  cat > "$case_dir/web/e2e/focused.spec.ts" <<'EOF'
test('normal E2E test', async ({ page }) => {
  await page.goto('/')
})
EOF

  MNEMONAS_WEB_ROOT="$case_dir/web" node "$REPO_ROOT/web/scripts/check-test-focus.cjs" > "$out" 2>&1
  assert_file_contains "$out" "[test-focus-check] no focused tests found"
}

run_prepare_installs_web_husky_from_repo_root() {
  local case_dir="$TMP_ROOT/prepare"
  local fake_bin="$case_dir/bin"
  local prepare_script
  mkdir -p "$case_dir/repo/.git" "$case_dir/repo/web/.husky" "$case_dir/repo/web/scripts" "$fake_bin"

  prepare_script="$(read_prepare_script)"
  [[ "$prepare_script" == "node ./scripts/prepare-husky.cjs" ]] || fail "unexpected prepare script: $prepare_script"
  cp "$REPO_ROOT/web/scripts/prepare-husky.cjs" "$case_dir/repo/web/scripts/prepare-husky.cjs"

  write_executable "$fake_bin/husky" \
    '#!/usr/bin/env bash' \
    'printf "%s:%s\n" "$PWD" "$*" > "$HUSKY_INVOKED_LOG"'

  (
    cd "$case_dir/repo/web"
    HUSKY_INVOKED_LOG="$case_dir/husky.log" \
      PATH="$fake_bin:$PATH" \
      sh -c "$prepare_script"
  )

  assert_file_contains "$case_dir/husky.log" "$case_dir/repo:web/.husky"
}

run_prepare_skips_husky_outside_git_checkout() {
  local case_dir="$TMP_ROOT/prepare-no-git"
  local fake_bin="$case_dir/bin"
  mkdir -p "$case_dir/repo/web/.husky" "$case_dir/repo/web/scripts" "$fake_bin"
  cp "$REPO_ROOT/web/scripts/prepare-husky.cjs" "$case_dir/repo/web/scripts/prepare-husky.cjs"

  write_executable "$fake_bin/husky" \
    '#!/usr/bin/env bash' \
    'printf "%s:%s\n" "$PWD" "$*" > "$HUSKY_INVOKED_LOG"' \
    'exit 42'

  (
    cd "$case_dir/repo/web"
    HUSKY_INVOKED_LOG="$case_dir/husky.log" \
      MNEMONAS_WEB_ROOT="$case_dir/repo/web" \
      PATH="$fake_bin:$PATH" \
      node ./scripts/prepare-husky.cjs
  )

  [[ ! -e "$case_dir/husky.log" ]] || fail "prepare invoked Husky outside a Git checkout"
}

run_prepare_skips_husky_when_install_context_disables_hooks() {
  local case_dir="$TMP_ROOT/prepare-disabled"
  local fake_bin="$case_dir/bin"
  local env_name
  local env_value
  mkdir -p "$case_dir/repo/.git" "$case_dir/repo/web/.husky" "$case_dir/repo/web/scripts" "$fake_bin"
  cp "$REPO_ROOT/web/scripts/prepare-husky.cjs" "$case_dir/repo/web/scripts/prepare-husky.cjs"

  write_executable "$fake_bin/husky" \
    '#!/usr/bin/env bash' \
    'printf "%s:%s\n" "$PWD" "$*" >> "$HUSKY_INVOKED_LOG"' \
    'exit 42'

  for env_pair in \
    "HUSKY=0" \
    "npm_config_ignore_scripts=true" \
    "npm_config_omit=dev" \
    "npm_config_omit=optional dev" \
    "npm_config_production=true" \
    "NODE_ENV=production"; do
    env_name="${env_pair%%=*}"
    env_value="${env_pair#*=}"
    rm -f "$case_dir/husky.log"

    (
      cd "$case_dir/repo/web"
      env "$env_name=$env_value" \
        HUSKY_INVOKED_LOG="$case_dir/husky.log" \
        MNEMONAS_WEB_ROOT="$case_dir/repo/web" \
        PATH="$fake_bin:$PATH" \
        node ./scripts/prepare-husky.cjs
    )

    [[ ! -e "$case_dir/husky.log" ]] || fail "prepare invoked Husky when $env_pair"
  done
}

run_github_workflows_disable_husky_for_automated_npm_installs() {
  python3 - "$REPO_ROOT/.github/workflows/ci.yml" "$REPO_ROOT/.github/workflows/release.yml" <<'PY'
import re
import sys
from pathlib import Path

failed = False

for path_text in sys.argv[1:]:
    path = Path(path_text)
    text = path.read_text(encoding="utf-8")
    for match in re.finditer(r"(?m)^ {6}- name: .*\n(?:^ {8,}.*\n)*?^ {8}run: (?:\|\n(?:^ {10}.*\n)+|.*npm ci.*$)", text):
        block = match.group(0)
        if "npm ci" not in block:
            continue
        if "HUSKY: '0'" not in block:
            print(f"{path}: automated npm ci step must set HUSKY: '0'", file=sys.stderr)
            failed = True

if failed:
    sys.exit(1)
PY
}

run_dockerfile_copies_prepare_helper_before_npm_ci() {
  local dockerfile="$REPO_ROOT/Dockerfile"
  local helper="$REPO_ROOT/web/scripts/prepare-husky.cjs"

  [[ -f "$helper" ]] || fail "missing frontend Husky prepare helper: $helper"
  assert_file_contains "$dockerfile" "COPY web/package.json web/package-lock.json ./"
  assert_file_contains "$dockerfile" "COPY web/scripts/prepare-husky.cjs ./scripts/prepare-husky.cjs"
  assert_file_contains "$dockerfile" "npm ci --prefer-offline"
  assert_file_contains_before "$dockerfile" "COPY web/package.json web/package-lock.json ./" "COPY web/scripts/prepare-husky.cjs ./scripts/prepare-husky.cjs"
  assert_file_contains_before "$dockerfile" "COPY web/scripts/prepare-husky.cjs ./scripts/prepare-husky.cjs" "npm ci --prefer-offline"
}

run_pre_commit_enters_web_before_lint_staged() {
  local case_dir="$TMP_ROOT/pre-commit"
  local fake_bin="$case_dir/bin"
  mkdir -p "$case_dir/repo/web/.husky" "$case_dir/repo/web/scripts" "$case_dir/repo/nested" "$fake_bin"
  assert_file_executable "$REPO_ROOT/web/.husky/pre-commit"
  cp "$REPO_ROOT/web/.husky/pre-commit" "$case_dir/repo/web/.husky/pre-commit"
  assert_file_executable "$case_dir/repo/web/.husky/pre-commit"

  write_executable "$fake_bin/npm" \
    '#!/usr/bin/env bash' \
    'printf "npm:%s:%s\n" "$PWD" "$*" >> "$HOOK_INVOKED_LOG"'

  write_executable "$fake_bin/node" \
    '#!/usr/bin/env bash' \
    'printf "node:%s:%s\n" "$PWD" "$*" >> "$HOOK_INVOKED_LOG"'

  write_executable "$fake_bin/npx" \
    '#!/usr/bin/env bash' \
    'printf "npx:%s:%s\n" "$PWD" "$*" >> "$HOOK_INVOKED_LOG"'

  (
    cd "$case_dir/repo"
    git init -q
    cd nested
    HOOK_INVOKED_LOG="$case_dir/hook.log" \
      PATH="$fake_bin:$PATH" \
      sh "$case_dir/repo/web/.husky/pre-commit"
  )

  assert_file_contains "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:node"
  assert_file_contains "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:scripts"
  assert_file_contains "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:identity"
  assert_file_contains "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:queries"
  assert_file_contains "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:testids"
  assert_file_contains "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:ts-suppressions"
  assert_file_contains "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:console"
  assert_file_contains "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:test-focus"
  assert_file_contains "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:a11y"
  assert_file_contains "$case_dir/hook.log" "node:$case_dir/repo/web:./scripts/check-eslint-button-rule.mjs"
  assert_file_contains "$case_dir/hook.log" "npx:$case_dir/repo/web:lint-staged --config package.json"
  assert_file_contains_before "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:node" "npm:$case_dir/repo/web:run check:scripts"
  assert_file_contains_before "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:scripts" "npm:$case_dir/repo/web:run check:identity"
  assert_file_contains_before "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:identity" "npm:$case_dir/repo/web:run check:queries"
}

run_pre_commit_stops_after_failed_check() {
  local case_dir="$TMP_ROOT/pre-commit-failure"
  local fake_bin="$case_dir/bin"
  local status
  mkdir -p "$case_dir/repo/web/.husky" "$case_dir/repo/nested" "$fake_bin"
  cp "$REPO_ROOT/web/.husky/pre-commit" "$case_dir/repo/web/.husky/pre-commit"
  chmod +x "$case_dir/repo/web/.husky/pre-commit"

  write_executable "$fake_bin/npm" \
    '#!/usr/bin/env bash' \
    'printf "npm:%s:%s\n" "$PWD" "$*" >> "$HOOK_INVOKED_LOG"' \
    'if [[ "$*" == "run check:scripts" ]]; then exit 42; fi'

  write_executable "$fake_bin/node" \
    '#!/usr/bin/env bash' \
    'printf "node:%s:%s\n" "$PWD" "$*" >> "$HOOK_INVOKED_LOG"'

  write_executable "$fake_bin/npx" \
    '#!/usr/bin/env bash' \
    'printf "npx:%s:%s\n" "$PWD" "$*" >> "$HOOK_INVOKED_LOG"'

  set +e
  (
    cd "$case_dir/repo"
    git init -q
    cd nested
    HOOK_INVOKED_LOG="$case_dir/hook.log" \
      PATH="$fake_bin:$PATH" \
      sh "$case_dir/repo/web/.husky/pre-commit"
  )
  status=$?
  set -e

  [[ "$status" -eq 42 ]] || fail "pre-commit returned $status instead of the failing check status"
  assert_file_contains "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:node"
  assert_file_contains "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:scripts"
  assert_file_not_contains "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:identity"
  assert_file_not_contains "$case_dir/hook.log" "npm:$case_dir/repo/web:run check:queries"
  assert_file_not_contains "$case_dir/hook.log" "npx:$case_dir/repo/web:lint-staged --config package.json"
}

run_frontend_commands_have_node_engine_prechecks
run_node_engine_checker_reads_package_engine
run_lint_checks_node_tool_scripts_and_button_rule
run_production_testid_checker_rejects_source_hooks
run_testing_library_query_checker_rejects_testid_queries
run_production_ts_suppression_checker_rejects_source_suppressions
run_production_console_checker_rejects_unguarded_source_usage
run_playwright_forbids_focused_tests_locally
run_vitest_forbids_focused_tests_locally
run_node_tool_script_checker_covers_supported_extensions
run_node_tool_script_checker_rejects_invalid_scripts
run_icon_button_label_checker_uses_tsx_ast
run_test_query_checker_uses_typescript_ast
run_project_identity_checker_rejects_stale_identity
run_test_focus_checker_uses_typescript_ast
run_prepare_installs_web_husky_from_repo_root
run_prepare_skips_husky_outside_git_checkout
run_prepare_skips_husky_when_install_context_disables_hooks
run_github_workflows_disable_husky_for_automated_npm_installs
run_dockerfile_copies_prepare_helper_before_npm_ci
run_pre_commit_enters_web_before_lint_staged
run_pre_commit_stops_after_failed_check

printf '[web-husky-safety-test] all checks passed\n'
