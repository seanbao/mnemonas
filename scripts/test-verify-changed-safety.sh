#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
  printf '[verify-changed-safety-test] ERROR: %s\n' "$*" >&2
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

  if grep -Fq -- "$unexpected" "$path"; then
    fail "$path contains unexpected text: $unexpected"
  fi
}

assert_docker_build_command_selected() {
  local path="$1"

  assert_file_contains "$path" "Build and smoke test Docker image: if command -v timeout >/dev/null 2>&1;"
  # shellcheck disable=SC2016 # Match the delayed-expansion command literal.
  assert_file_contains "$path" 'timeout "${VERIFY_CHANGED_DOCKER_TIMEOUT:-45m}" make docker-check'
  # shellcheck disable=SC2016 # Match the delayed-expansion command literal.
  assert_file_contains "$path" 'gtimeout "${VERIFY_CHANGED_DOCKER_TIMEOUT:-45m}" make docker-check'
  assert_file_contains "$path" "requires timeout or gtimeout"
}

setup_repo() {
  local repo_dir="$1"
  mkdir -p "$repo_dir/.github/ISSUE_TEMPLATE" "$repo_dir/.github/workflows" "$repo_dir/scripts" "$repo_dir/dataplane/src" "$repo_dir/web/.husky" "$repo_dir/web/e2e" "$repo_dir/web/scripts" "$repo_dir/web/src"
  mkdir -p "$repo_dir/deploy/public-access/traefik/dynamic" "$repo_dir/deploy/public-access/cloudflare-tunnel"
  mkdir -p "$repo_dir/proto"
  mkdir -p "$repo_dir/tools/proto-gen"
  cp "$REPO_ROOT/scripts/verify-changed.sh" "$repo_dir/scripts/verify-changed.sh"
  cp "$REPO_ROOT/scripts/check-yaml-configs.sh" "$repo_dir/scripts/check-yaml-configs.sh"
  cp "$REPO_ROOT/scripts/check-untracked-whitespace.sh" "$repo_dir/scripts/check-untracked-whitespace.sh"
  cp "$REPO_ROOT/scripts/check-toolchain-versions.sh" "$repo_dir/scripts/check-toolchain-versions.sh"
  cp "$REPO_ROOT/scripts/check-secret-leaks.sh" "$repo_dir/scripts/check-secret-leaks.sh"
  chmod +x "$repo_dir/scripts/verify-changed.sh"
  chmod +x "$repo_dir/scripts/check-yaml-configs.sh"
  chmod +x "$repo_dir/scripts/check-untracked-whitespace.sh"
  chmod +x "$repo_dir/scripts/check-toolchain-versions.sh"
  chmod +x "$repo_dir/scripts/check-secret-leaks.sh"
  printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' > "$repo_dir/scripts/tracked-check.sh"
  touch "$repo_dir/go.mod" "$repo_dir/go.sum"
  touch "$repo_dir/dataplane/Cargo.toml" "$repo_dir/dataplane/Cargo.lock" "$repo_dir/dataplane/src/lib.rs"
  touch "$repo_dir/web/package.json" "$repo_dir/web/package-lock.json" "$repo_dir/web/.nvmrc" "$repo_dir/web/.husky/pre-commit" "$repo_dir/web/e2e/auth.setup.ts" "$repo_dir/web/scripts/check-node.cjs" "$repo_dir/web/src/App.tsx" "$repo_dir/web/tsconfig.e2e.json"
  touch "$repo_dir/web/eslint.config.js" "$repo_dir/web/vite.config.ts" "$repo_dir/web/vitest.config.ts" "$repo_dir/web/playwright.config.ts"
  touch "$repo_dir/mnemonas.example.toml"
  touch "$repo_dir/Dockerfile" "$repo_dir/.dockerignore" "$repo_dir/.env.example" "$repo_dir/docker-compose.yml"
  touch "$repo_dir/.go-version" "$repo_dir/.golangci.yml" "$repo_dir/.nvmrc"
  cat > "$repo_dir/Makefile" <<'EOF'
toolchains-check:
	@:

docker:
	@:
EOF
  touch "$repo_dir/.github/dependabot.yml" "$repo_dir/.github/ISSUE_TEMPLATE/bug_report.yml" "$repo_dir/.github/workflows/ci.yml" "$repo_dir/codecov.yml"
  touch "$repo_dir/deploy/public-access/traefik/dynamic/mnemonas.yml"
  touch "$repo_dir/proto/dataplane.proto"
  touch "$repo_dir/tools/proto-gen/Cargo.toml" "$repo_dir/tools/proto-gen/Cargo.lock"

  (
    cd "$repo_dir"
    git init -q
    git config user.email "test@example.invalid"
    git config user.name "Verify Changed Test"
    git add .
    git commit -q -m "test: initial"
  )
}

run_dry_run() {
  local repo_dir="$1"
  local out="$2"

  (
    cd "$repo_dir"
    ./scripts/verify-changed.sh --dry-run > "$out"
  )
}

run_staged_dry_run() {
  local repo_dir="$1"
  local out="$2"

  (
    cd "$repo_dir"
    ./scripts/verify-changed.sh --staged --dry-run > "$out"
  )
}

run_base_dry_run() {
  local repo_dir="$1"
  local base="$2"
  local out="$3"

  (
    cd "$repo_dir"
    ./scripts/verify-changed.sh --base "$base" --dry-run > "$out"
  )
}

run_husky_hook_change_selects_scripts_check() {
  local case_dir="$TMP_ROOT/husky-hook-change"
  local out="$TMP_ROOT/husky-hook-change-out.log"
  setup_repo "$case_dir"

  printf '%s\n' '#!/usr/bin/env sh' 'npx lint-staged' > "$case_dir/web/.husky/pre-commit"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Check diff whitespace: git diff --check"
  assert_file_contains "$out" "Validate shell scripts: make scripts-check"
  assert_file_not_contains "$out" "Run frontend E2E"
}

run_any_change_selects_secret_leak_check() {
  local case_dir="$TMP_ROOT/any-change-secret-leak-check"
  local out="$TMP_ROOT/any-change-secret-leak-check-out.log"
  setup_repo "$case_dir"

  printf '%s\n' "export const touched = true" > "$case_dir/web/src/App.tsx"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Check obvious secret leaks: ./scripts/check-secret-leaks.sh"
}

run_workflow_change_selects_workflows_check() {
  local case_dir="$TMP_ROOT/workflow-change"
  local out="$TMP_ROOT/workflow-change-out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'name: CI' 'on: [push]' 'jobs: {}' > "$case_dir/.github/workflows/ci.yml"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Check diff whitespace: git diff --check"
  assert_file_contains "$out" "Validate GitHub workflows: make workflows-check"
  assert_file_contains "$out" "Validate toolchain versions: make toolchains-check"
  assert_file_not_contains "$out" "Validate shell scripts"
}

run_ci_workflow_runs_toolchain_check() {
  assert_file_contains "$REPO_ROOT/.github/workflows/ci.yml" "  toolchains:"
  assert_file_contains "$REPO_ROOT/.github/workflows/ci.yml" "    name: Toolchains"
  assert_file_contains "$REPO_ROOT/.github/workflows/ci.yml" "        run: make toolchains-check"
}

run_web_e2e_change_selects_typecheck_and_e2e() {
  local case_dir="$TMP_ROOT/web-e2e-change"
  local out="$TMP_ROOT/web-e2e-change-out.log"
  setup_repo "$case_dir"

  printf '%s\n' "export const touched = true" > "$case_dir/web/e2e/auth.setup.ts"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Check diff whitespace: git diff --check"
  assert_file_contains "$out" "Run frontend lint: cd web && npm run lint"
  assert_file_contains "$out" "Run frontend typecheck: cd web && npm run typecheck"
  assert_file_contains "$out" "Run frontend E2E: cd web && npm run test:e2e"
}

run_untracked_web_e2e_change_selects_typecheck_and_e2e() {
  local case_dir="$TMP_ROOT/untracked-web-e2e-change"
  local out="$TMP_ROOT/untracked-web-e2e-change-out.log"
  setup_repo "$case_dir"

  mkdir -p "$case_dir/web/e2e/helpers"
  printf '%s\n' "export const touched = true" > "$case_dir/web/e2e/helpers/new-helper.ts"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Check untracked file whitespace: ./scripts/check-untracked-whitespace.sh"
  assert_file_contains "$out" "Run frontend lint: cd web && npm run lint"
  assert_file_contains "$out" "Run frontend typecheck: cd web && npm run typecheck"
  assert_file_contains "$out" "Run frontend E2E: cd web && npm run test:e2e"
}

run_web_src_change_selects_typecheck_without_e2e() {
  local case_dir="$TMP_ROOT/web-src-change"
  local out="$TMP_ROOT/web-src-change-out.log"
  setup_repo "$case_dir"

  printf '%s\n' "export const touched = true" > "$case_dir/web/src/App.tsx"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Run frontend typecheck: cd web && npm run typecheck"
  assert_file_not_contains "$out" "Run frontend E2E"
}

run_web_script_change_selects_frontend_and_scripts_checks() {
  local case_dir="$TMP_ROOT/web-script-change"
  local out="$TMP_ROOT/web-script-change-out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'console.log("node check changed")' > "$case_dir/web/scripts/check-node.cjs"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Validate shell scripts: make scripts-check"
  assert_file_contains "$out" "Run frontend lint: cd web && npm run lint"
  assert_file_contains "$out" "Run frontend typecheck: cd web && npm run typecheck"
  assert_file_contains "$out" "Run frontend unit tests: cd web && npm run test:run"
  assert_file_contains "$out" "Build frontend: cd web && npm run build"
  assert_file_not_contains "$out" "Run frontend E2E"
}

run_web_e2e_tsconfig_change_selects_typecheck_and_e2e() {
  local case_dir="$TMP_ROOT/web-e2e-tsconfig-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' '{"compilerOptions":{"strict":true}}' > "$case_dir/web/tsconfig.e2e.json"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Run frontend typecheck: cd web && npm run typecheck"
  assert_file_contains "$out" "Run frontend E2E: cd web && npm run test:e2e"
}

run_web_vitest_config_change_selects_unit_validation() {
  local case_dir="$TMP_ROOT/web-vitest-config-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'export default { test: { allowOnly: false } }' > "$case_dir/web/vitest.config.ts"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Run frontend lint: cd web && npm run lint"
  assert_file_contains "$out" "Run frontend typecheck: cd web && npm run typecheck"
  assert_file_contains "$out" "Run frontend unit tests: cd web && npm run test:run"
  assert_file_contains "$out" "Build frontend: cd web && npm run build"
  assert_file_not_contains "$out" "Run frontend E2E"
}

run_web_vite_config_change_selects_build_validation() {
  local case_dir="$TMP_ROOT/web-vite-config-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'export default {}' > "$case_dir/web/vite.config.ts"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Run frontend lint: cd web && npm run lint"
  assert_file_contains "$out" "Run frontend typecheck: cd web && npm run typecheck"
  assert_file_contains "$out" "Run frontend unit tests: cd web && npm run test:run"
  assert_file_contains "$out" "Build frontend: cd web && npm run build"
  assert_file_not_contains "$out" "Run frontend E2E"
}

run_web_playwright_config_change_selects_e2e_validation() {
  local case_dir="$TMP_ROOT/web-playwright-config-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'export default { forbidOnly: true }' > "$case_dir/web/playwright.config.ts"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Run frontend lint: cd web && npm run lint"
  assert_file_contains "$out" "Run frontend typecheck: cd web && npm run typecheck"
  assert_file_contains "$out" "Run frontend E2E: cd web && npm run test:e2e"
}

run_untracked_script_change_selects_scripts_check() {
  local case_dir="$TMP_ROOT/untracked-script-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' > "$case_dir/scripts/new-check.sh"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Check untracked file whitespace: ./scripts/check-untracked-whitespace.sh"
  assert_file_contains "$out" "Validate shell scripts: make scripts-check"
}

run_untracked_newline_script_path_selects_scripts_check() {
  local case_dir="$TMP_ROOT/untracked-newline-script-path"
  local out="$case_dir/out.log"
  local script_path=$'scripts/new\ncheck.sh'
  setup_repo "$case_dir"

  printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' > "$case_dir/$script_path"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Check untracked file whitespace: ./scripts/check-untracked-whitespace.sh"
  assert_file_contains "$out" "Validate shell scripts: make scripts-check"
}

run_script_typechange_selects_scripts_check() {
  local case_dir="$TMP_ROOT/script-typechange"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  rm "$case_dir/scripts/tracked-check.sh"
  ln -s ../Makefile "$case_dir/scripts/tracked-check.sh"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Check diff whitespace: git diff --check"
  assert_file_contains "$out" "Validate shell scripts: make scripts-check"
}

run_renamed_script_to_docs_selects_old_and_new_path_checks() {
  local case_dir="$TMP_ROOT/renamed-script-to-docs"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  mkdir -p "$case_dir/docs"
  git -C "$case_dir" mv scripts/tracked-check.sh docs/tracked-check.md
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Validate shell scripts: make scripts-check"
  assert_file_contains "$out" "Validate documentation links: make docs-check"
}

run_doc_checker_change_selects_docs_and_scripts_checks() {
  local case_dir="$TMP_ROOT/doc-checker-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' '# doc checker changed' >> "$case_dir/scripts/check-doc-links.sh"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Validate shell scripts: make scripts-check"
  assert_file_contains "$out" "Validate documentation links: make docs-check"
}

run_untracked_whitespace_checker_rejects_bad_text() {
  local case_dir="$TMP_ROOT/untracked-whitespace-checker"
  local symlink_target="$TMP_ROOT/untracked-whitespace-symlink-target.ts"
  setup_repo "$case_dir"

  printf '%s\n' 'const ok = true' > "$case_dir/web/src/new-clean.ts"
  printf 'const outsideBad = true  \n' > "$symlink_target"
  ln -s "$symlink_target" "$case_dir/web/src/new-symlink.ts"
  printf 'binary\0with trailing whitespace  \n' > "$case_dir/scripts/new-binary-tool"
  (
    cd "$case_dir"
    ./scripts/check-untracked-whitespace.sh > "$case_dir/untracked-clean.log"
  )
  assert_file_contains "$case_dir/untracked-clean.log" "[untracked-whitespace-check] checked 1 text file(s)"

  printf 'SECRET=value  \n' > "$case_dir/.env"
  set +e
  (
    cd "$case_dir"
    ./scripts/check-untracked-whitespace.sh .env > "$case_dir/untracked-dotfile-bad.log" 2>&1
  )
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "untracked whitespace checker accepted trailing whitespace in dotfile"
  assert_file_contains "$case_dir/untracked-dotfile-bad.log" ".env:1: trailing whitespace"

  printf 'const bad = true  \n' > "$case_dir/web/src/new-bad.ts"
  set +e
  (
    cd "$case_dir"
    ./scripts/check-untracked-whitespace.sh > "$case_dir/untracked-bad.log" 2>&1
  )
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "untracked whitespace checker accepted trailing whitespace"
  assert_file_contains "$case_dir/untracked-bad.log" "web/src/new-bad.ts:1: trailing whitespace"
}

run_untracked_whitespace_checker_rejects_space_before_tab_indent() {
  local case_dir="$TMP_ROOT/untracked-whitespace-indent"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '\tconst ok = true\n' > "$case_dir/web/src/new-clean.ts"
  printf ' \tconst bad = true\n' > "$case_dir/web/src/new-bad.ts"
  set +e
  (
    cd "$case_dir"
    ./scripts/check-untracked-whitespace.sh > "$out" 2>&1
  )
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "untracked whitespace checker accepted space-before-tab indentation"
  assert_file_contains "$out" "web/src/new-bad.ts:1: space before tab in indent"
}

run_example_config_change_selects_check_config() {
  local case_dir="$TMP_ROOT/example-config-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' '[storage]' 'root = "~/.mnemonas-test"' > "$case_dir/mnemonas.example.toml"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Validate example config: env GOTOOLCHAIN=local go run ./cmd/nasd --check-config --config mnemonas.example.toml"
}

run_env_example_change_selects_docker_template_checks() {
  local case_dir="$TMP_ROOT/env-example-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'MNEMONAS_HTTP_PORT=18080' > "$case_dir/.env.example"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Validate Docker templates: ./scripts/test-docker-start.sh && ./scripts/test-docker-preflight.sh && ./scripts/test-docker-quickstart.sh"
  assert_file_contains "$out" "Validate toolchain versions: make toolchains-check"
  assert_file_not_contains "$out" "Build Docker image"
}

run_docker_compose_change_selects_docker_template_checks() {
  local case_dir="$TMP_ROOT/docker-compose-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'services:' '  mnemonas:' '    image: mnemonas:local' > "$case_dir/docker-compose.yml"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Validate Docker templates: ./scripts/test-docker-start.sh && ./scripts/test-docker-preflight.sh && ./scripts/test-docker-quickstart.sh"
  assert_file_contains "$out" "Validate toolchain versions: make toolchains-check"
  assert_docker_build_command_selected "$out"
}

run_deploy_docker_compose_yaml_change_selects_docker_template_checks() {
  local case_dir="$TMP_ROOT/deploy-docker-compose-yaml-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'services:' '  mnemonas:' '    image: mnemonas:local' > "$case_dir/deploy/public-access/traefik/docker-compose.yaml"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Validate Docker templates: ./scripts/test-docker-start.sh && ./scripts/test-docker-preflight.sh && ./scripts/test-docker-quickstart.sh"
  assert_docker_build_command_selected "$out"
}

run_dockerignore_change_selects_docker_build() {
  local case_dir="$TMP_ROOT/dockerignore-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'web/dist' > "$case_dir/.dockerignore"
  run_dry_run "$case_dir" "$out"

  assert_docker_build_command_selected "$out"
  assert_file_not_contains "$out" "Validate Docker templates"
}

run_docker_build_command_uses_configured_timeout_wrapper() {
  local case_dir="$TMP_ROOT/docker-build-timeout-wrapper"
  local fake_bin="$TMP_ROOT/docker-build-timeout-wrapper-bin"
  local capture_dir="$TMP_ROOT/docker-build-timeout-wrapper-capture"
  local out="$TMP_ROOT/docker-build-timeout-wrapper-out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'FROM scratch' > "$case_dir/Dockerfile"
  mkdir -p "$fake_bin" "$capture_dir"
  cat > "$fake_bin/timeout" <<'FAKE_TIMEOUT'
#!/usr/bin/env bash
printf '%s\n' "$*" > "$CAPTURE_DIR/timeout.args"
FAKE_TIMEOUT
  chmod +x "$fake_bin/timeout"

  (
    cd "$case_dir"
    PATH="$fake_bin:$PATH" CAPTURE_DIR="$capture_dir" VERIFY_CHANGED_DOCKER_TIMEOUT=7s ./scripts/verify-changed.sh > "$out"
  )

  assert_file_contains "$out" "==> Validate toolchain versions"
  assert_file_contains "$out" "==> Build and smoke test Docker image"
  assert_file_contains "$capture_dir/timeout.args" "7s make docker-check"
}

run_docker_build_command_uses_gtimeout_fallback() {
  local case_dir="$TMP_ROOT/docker-build-gtimeout-wrapper"
  local fake_bin="$TMP_ROOT/docker-build-gtimeout-wrapper-bin"
  local capture_dir="$TMP_ROOT/docker-build-gtimeout-wrapper-capture"
  local out="$TMP_ROOT/docker-build-gtimeout-wrapper-out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'FROM scratch' > "$case_dir/Dockerfile"
  mkdir -p "$fake_bin" "$capture_dir"
  ln -s "$(command -v bash)" "$fake_bin/bash"
  ln -s "$(command -v awk)" "$fake_bin/awk"
  ln -s "$(command -v git)" "$fake_bin/git"
  ln -s "$(command -v grep)" "$fake_bin/grep"
  ln -s "$(command -v make)" "$fake_bin/make"
  ln -s "$(command -v stat)" "$fake_bin/stat"
  cat > "$fake_bin/gtimeout" <<'FAKE_GTIMEOUT'
#!/usr/bin/env bash
printf '%s\n' "$*" > "$CAPTURE_DIR/gtimeout.args"
FAKE_GTIMEOUT
  chmod +x "$fake_bin/gtimeout"

  (
    cd "$case_dir"
    PATH="$fake_bin" CAPTURE_DIR="$capture_dir" VERIFY_CHANGED_DOCKER_TIMEOUT=11s "$(command -v bash)" ./scripts/verify-changed.sh > "$out"
  )

  assert_file_contains "$out" "==> Validate toolchain versions"
  assert_file_contains "$out" "==> Build and smoke test Docker image"
  assert_file_contains "$capture_dir/gtimeout.args" "11s make docker-check"
}

run_docker_build_command_fails_without_timeout_tools() {
  local case_dir="$TMP_ROOT/docker-build-no-timeout"
  local fake_bin="$TMP_ROOT/docker-build-no-timeout-bin"
  local out="$TMP_ROOT/docker-build-no-timeout-out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'FROM scratch' > "$case_dir/Dockerfile"
  mkdir -p "$fake_bin"
  ln -s "$(command -v bash)" "$fake_bin/bash"
  ln -s "$(command -v awk)" "$fake_bin/awk"
  ln -s "$(command -v git)" "$fake_bin/git"
  ln -s "$(command -v grep)" "$fake_bin/grep"
  ln -s "$(command -v make)" "$fake_bin/make"
  ln -s "$(command -v stat)" "$fake_bin/stat"

  set +e
  (
    cd "$case_dir"
    PATH="$fake_bin" "$(command -v bash)" ./scripts/verify-changed.sh > "$out" 2>&1
  )
  status=$?
  set -e

  [[ "$status" -eq 127 ]] || fail "verify-changed did not fail with 127 when timeout and gtimeout were missing"
  assert_file_contains "$out" "verify-changed: Docker build and smoke validation requires timeout or gtimeout"
}

run_go_version_change_selects_quick_check() {
  local case_dir="$TMP_ROOT/go-version-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' '1.25.12' > "$case_dir/.go-version"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Check diff whitespace: git diff --check"
  assert_file_contains "$out" "Validate toolchain versions: make toolchains-check"
  assert_file_contains "$out" "Run quick Go/Rust checks: make quick-check"
}

run_go_dependency_manifest_change_selects_security_check() {
  local case_dir="$TMP_ROOT/go-dependency-manifest-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'module example.invalid/mnemonas-test' 'go 1.25.0' > "$case_dir/go.mod"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Run dependency security checks: make security-check"
  assert_file_contains "$out" "Validate toolchain versions: make toolchains-check"
  assert_file_contains "$out" "Run quick Go/Rust checks: make quick-check"
}

run_web_dependency_manifest_change_selects_security_check() {
  local case_dir="$TMP_ROOT/web-dependency-manifest-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' '{"lockfileVersion":3}' > "$case_dir/web/package-lock.json"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Run dependency security checks: make security-check NPM_AUDIT=1"
  assert_file_contains "$out" "Validate toolchain versions: make toolchains-check"
  assert_file_contains "$out" "Run frontend typecheck: cd web && npm run typecheck"
}

run_rust_dependency_manifest_change_selects_security_check() {
  local case_dir="$TMP_ROOT/rust-dependency-manifest-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' '# lockfile changed' > "$case_dir/dataplane/Cargo.lock"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Run dependency security checks: make security-check"
  assert_file_contains "$out" "Run dataplane tests: cd dataplane && cargo test --locked"
}

run_nvmrc_change_selects_frontend_checks() {
  local case_dir="$TMP_ROOT/nvmrc-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' '24' > "$case_dir/.nvmrc"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Run frontend lint: cd web && npm run lint"
  assert_file_contains "$out" "Run frontend typecheck: cd web && npm run typecheck"
  assert_file_contains "$out" "Build frontend: cd web && npm run build"
  assert_file_contains "$out" "Validate toolchain versions: make toolchains-check"
  assert_file_not_contains "$out" "Run frontend E2E"
}

run_web_nvmrc_change_selects_frontend_and_toolchain_checks() {
  local case_dir="$TMP_ROOT/web-nvmrc-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' '24' > "$case_dir/web/.nvmrc"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Run frontend lint: cd web && npm run lint"
  assert_file_contains "$out" "Run frontend typecheck: cd web && npm run typecheck"
  assert_file_contains "$out" "Build frontend: cd web && npm run build"
  assert_file_contains "$out" "Validate toolchain versions: make toolchains-check"
  assert_file_not_contains "$out" "Run frontend E2E"
}

run_golangci_config_change_selects_lint() {
  local case_dir="$TMP_ROOT/golangci-config-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'version: "2"' > "$case_dir/.golangci.yml"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Run linters: make lint"
}

run_golangci_yaml_config_change_selects_lint() {
  local case_dir="$TMP_ROOT/golangci-yaml-config-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'version: "2"' > "$case_dir/.golangci.yaml"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Run linters: make lint"
}

run_precommit_config_change_selects_yaml_and_precommit_validation() {
  local case_dir="$TMP_ROOT/precommit-config-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'repos: []' > "$case_dir/.pre-commit-config.yaml"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Validate pre-commit config: ./scripts/check-yaml-configs.sh .pre-commit-config.yaml && if [ -f .pre-commit-config.yaml ] && command -v pre-commit >/dev/null 2>&1; then pre-commit validate-config; fi"
}

run_dependabot_config_change_selects_yaml_validation() {
  local case_dir="$TMP_ROOT/dependabot-config-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'version: 2' 'updates: []' > "$case_dir/.github/dependabot.yml"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Validate YAML config: ./scripts/check-yaml-configs.sh .github/dependabot.yml .github/dependabot.yaml .github/ISSUE_TEMPLATE/*.yml .github/ISSUE_TEMPLATE/*.yaml codecov.yml codecov.yaml"
}

run_dependabot_yaml_config_change_selects_yaml_validation() {
  local case_dir="$TMP_ROOT/dependabot-yaml-config-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'version: 2' 'updates: []' > "$case_dir/.github/dependabot.yaml"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Validate YAML config: ./scripts/check-yaml-configs.sh .github/dependabot.yml .github/dependabot.yaml .github/ISSUE_TEMPLATE/*.yml .github/ISSUE_TEMPLATE/*.yaml codecov.yml codecov.yaml"
}

run_codecov_config_change_selects_yaml_validation() {
  local case_dir="$TMP_ROOT/codecov-config-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'coverage:' '  status: {}' > "$case_dir/codecov.yml"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Validate YAML config: ./scripts/check-yaml-configs.sh .github/dependabot.yml .github/dependabot.yaml .github/ISSUE_TEMPLATE/*.yml .github/ISSUE_TEMPLATE/*.yaml codecov.yml codecov.yaml"
}

run_codecov_yaml_config_change_selects_yaml_validation() {
  local case_dir="$TMP_ROOT/codecov-yaml-config-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'coverage:' '  status: {}' > "$case_dir/codecov.yaml"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Validate YAML config: ./scripts/check-yaml-configs.sh .github/dependabot.yml .github/dependabot.yaml .github/ISSUE_TEMPLATE/*.yml .github/ISSUE_TEMPLATE/*.yaml codecov.yml codecov.yaml"
}

run_issue_template_change_selects_yaml_validation() {
  local case_dir="$TMP_ROOT/issue-template-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'name: Bug report' 'description: Report a defect' 'body: []' > "$case_dir/.github/ISSUE_TEMPLATE/bug_report.yml"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Validate YAML config: ./scripts/check-yaml-configs.sh .github/dependabot.yml .github/dependabot.yaml .github/ISSUE_TEMPLATE/*.yml .github/ISSUE_TEMPLATE/*.yaml codecov.yml codecov.yaml"
}

run_yaml_config_checker_validates_files() {
  local case_dir="$TMP_ROOT/yaml-config-checker"
  setup_repo "$case_dir"

  printf '%s\n' 'version: 2' 'updates: []' > "$case_dir/.github/dependabot.yml"
  (
    cd "$case_dir"
    ./scripts/check-yaml-configs.sh .github/dependabot.yml > "$case_dir/yaml-valid.log"
  )
  assert_file_contains "$case_dir/yaml-valid.log" "[yaml-config-check] checked 1 YAML file(s)"

  printf '%s\n' 'version: [' > "$case_dir/.github/dependabot.yml"
  set +e
  (
    cd "$case_dir"
    ./scripts/check-yaml-configs.sh .github/dependabot.yml > "$case_dir/yaml-invalid.log" 2>&1
  )
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "YAML config checker accepted invalid YAML"
  assert_file_contains "$case_dir/yaml-invalid.log" ".github/dependabot.yml: invalid YAML:"

  printf '%s\n' 'version: 2' 'version: 3' > "$case_dir/.github/dependabot.yml"
  set +e
  (
    cd "$case_dir"
    ./scripts/check-yaml-configs.sh .github/dependabot.yml > "$case_dir/yaml-duplicate-key.log" 2>&1
  )
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "YAML config checker accepted duplicate keys"
  assert_file_contains "$case_dir/yaml-duplicate-key.log" ".github/dependabot.yml: invalid YAML:"
  assert_file_contains "$case_dir/yaml-duplicate-key.log" "found duplicate key 'version'"
}

run_makefile_scripts_check_runs_web_node_script_checker() {
  assert_file_contains "$REPO_ROOT/Makefile" "scripts-check - Validate deployment shell scripts and Web tool scripts"
  assert_file_contains "$REPO_ROOT/Makefile" "cd web && npm run check:scripts"
}

run_makefile_clean_removes_proto_generator_target() {
  assert_file_contains "$REPO_ROOT/Makefile" "cargo clean --manifest-path tools/proto-gen/Cargo.toml"
  assert_file_contains "$REPO_ROOT/Makefile" "cd web && rm -rf dist dist-ssr coverage test-results playwright-report .vite .vitest"
}

setup_toolchain_check_repo() {
  local repo_dir="$1"
  mkdir -p "$repo_dir/scripts" "$repo_dir/web" "$repo_dir/.github/workflows" "$repo_dir/dataplane" "$repo_dir/tools/proto-gen" "$repo_dir/docs"

  cp "$REPO_ROOT/scripts/check-toolchain-versions.sh" "$repo_dir/scripts/check-toolchain-versions.sh"
  chmod +x "$repo_dir/scripts/check-toolchain-versions.sh"
  cp "$REPO_ROOT/.go-version" "$repo_dir/.go-version"
  cp "$REPO_ROOT/.nvmrc" "$repo_dir/.nvmrc"
  cp "$REPO_ROOT/web/.nvmrc" "$repo_dir/web/.nvmrc"
  cp "$REPO_ROOT/go.mod" "$repo_dir/go.mod"
  cp "$REPO_ROOT/dataplane/Cargo.toml" "$repo_dir/dataplane/Cargo.toml"
  cp "$REPO_ROOT/tools/proto-gen/Cargo.toml" "$repo_dir/tools/proto-gen/Cargo.toml"
  cp "$REPO_ROOT/.github/workflows/ci.yml" "$repo_dir/.github/workflows/ci.yml"
  cp "$REPO_ROOT/.github/workflows/release.yml" "$repo_dir/.github/workflows/release.yml"
  cp "$REPO_ROOT/.github/workflows/torture.yml" "$repo_dir/.github/workflows/torture.yml"
  cp "$REPO_ROOT/Dockerfile" "$repo_dir/Dockerfile"
  cp "$REPO_ROOT/.env.example" "$repo_dir/.env.example"
  cp "$REPO_ROOT/docker-compose.yml" "$repo_dir/docker-compose.yml"
  cp "$REPO_ROOT/web/package.json" "$repo_dir/web/package.json"
  cp "$REPO_ROOT/web/package-lock.json" "$repo_dir/web/package-lock.json"
  cp "$REPO_ROOT/README.md" "$repo_dir/README.md"
  cp "$REPO_ROOT/README.en.md" "$repo_dir/README.en.md"
  cp "$REPO_ROOT/docs/development.md" "$repo_dir/docs/development.md"
  cp "$REPO_ROOT/docs/development.en.md" "$repo_dir/docs/development.en.md"
  cp "$REPO_ROOT/docs/docker-deployment.md" "$repo_dir/docs/docker-deployment.md"
  cp "$REPO_ROOT/docs/docker-deployment.en.md" "$repo_dir/docs/docker-deployment.en.md"

  git -C "$repo_dir" init -q
}

run_toolchain_checker_rejects_web_lock_manifest_drift() {
  local case_dir="$TMP_ROOT/toolchain-web-lock-drift"
  local out="$case_dir/out.log"
  local status
  setup_toolchain_check_repo "$case_dir"

  (
    cd "$case_dir"
    ./scripts/check-toolchain-versions.sh > "$out"
  )
  assert_file_contains "$out" "[toolchain-version-check]"

  python3 - "$case_dir/web/package-lock.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, "r", encoding="utf-8") as handle:
    data = json.load(handle)

data["packages"][""].setdefault("dependencies", {})["mnemonas-lock-drift"] = "1.0.0"

with open(path, "w", encoding="utf-8") as handle:
    json.dump(data, handle, indent=2)
    handle.write("\n")
PY

  set +e
  (
    cd "$case_dir"
    ./scripts/check-toolchain-versions.sh > "$out" 2>&1
  )
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "toolchain checker accepted package-lock dependency drift"
  assert_file_contains "$out" "web/package-lock.json root dependencies does not match web/package.json"
  assert_file_contains "$out" "extra keys ['mnemonas-lock-drift']"
}

run_toolchain_checker_rejects_web_package_identity_drift() {
  local case_dir="$TMP_ROOT/toolchain-web-package-identity-drift"
  local out="$case_dir/out.log"
  local status
  setup_toolchain_check_repo "$case_dir"

  (
    cd "$case_dir"
    ./scripts/check-toolchain-versions.sh > "$out"
  )
  assert_file_contains "$out" "Web package mnemonas-web"

  python3 - "$case_dir/web/package.json" "$case_dir/web/package-lock.json" <<'PY'
import json
import sys

package_path = sys.argv[1]
lock_path = sys.argv[2]

with open(package_path, "r", encoding="utf-8") as handle:
    package = json.load(handle)
with open(lock_path, "r", encoding="utf-8") as handle:
    lockfile = json.load(handle)

package["name"] = "web"
lockfile["name"] = "web"
lockfile["packages"][""]["name"] = "web"

with open(package_path, "w", encoding="utf-8") as handle:
    json.dump(package, handle, indent=2)
    handle.write("\n")
with open(lock_path, "w", encoding="utf-8") as handle:
    json.dump(lockfile, handle, indent=2)
    handle.write("\n")
PY

  set +e
  (
    cd "$case_dir"
    ./scripts/check-toolchain-versions.sh > "$out" 2>&1
  )
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "toolchain checker accepted web package identity drift"
  assert_file_contains "$out" "web/package.json name = 'web', expected 'mnemonas-web'"
}

run_public_access_template_change_selects_template_check() {
  local case_dir="$TMP_ROOT/public-access-template-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'http:' '  routers: {}' > "$case_dir/deploy/public-access/traefik/dynamic/mnemonas.yml"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Validate public access templates: ./scripts/test-public-access-templates.sh"
  assert_file_not_contains "$out" "Build Docker image"
}

run_makefile_change_selects_full_project_check() {
  local case_dir="$TMP_ROOT/makefile-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'check:' '	@true' > "$case_dir/Makefile"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Run full project check: make check"
  assert_file_not_contains "$out" "Run quick Go/Rust checks: make quick-check"
  assert_file_not_contains "$out" "Validate documentation links: make docs-check"
}

run_proto_change_selects_regen_and_quick_check() {
  local case_dir="$TMP_ROOT/proto-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'syntax = "proto3";' 'package mnemonas.dataplane.v1;' > "$case_dir/proto/dataplane.proto"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Regenerate protobuf and check generated output stability"
  assert_file_contains "$out" "Run quick Go/Rust checks: make quick-check"
  assert_file_not_contains "$out" "Run frontend E2E"
}

run_dataplane_rust_change_selects_fmt_tests_and_clippy() {
  local case_dir="$TMP_ROOT/dataplane-rust-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'pub fn touched() -> bool { true }' > "$case_dir/dataplane/src/lib.rs"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Check dataplane Rust formatting: cd dataplane && cargo fmt --check"
  assert_file_contains "$out" "Run dataplane tests: cd dataplane && cargo test --locked"
  assert_file_contains "$out" "Run dataplane clippy: cd dataplane && cargo clippy --all-targets --locked -- -D warnings"
  assert_file_not_contains "$out" "Run frontend E2E"
}

run_proto_generator_change_selects_regen_and_generator_checks() {
  local case_dir="$TMP_ROOT/proto-generator-change"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' '[package]' 'name = "mnemonas-proto-gen"' 'version = "0.1.0"' > "$case_dir/tools/proto-gen/Cargo.toml"
  run_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Regenerate protobuf and check generated output stability"
  assert_file_contains "$out" "Check proto generator Rust formatting: cargo fmt --manifest-path tools/proto-gen/Cargo.toml --check"
  assert_file_contains "$out" "Run proto generator tests: cargo test --manifest-path tools/proto-gen/Cargo.toml --locked"
  assert_file_contains "$out" "Run proto generator clippy: cargo clippy --manifest-path tools/proto-gen/Cargo.toml --locked -- -D warnings"
  assert_file_not_contains "$out" "Run frontend E2E"
}

run_staged_change_uses_cached_diff_check() {
  local case_dir="$TMP_ROOT/staged-diff-check"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' > "$case_dir/scripts/staged-check.sh"
  (
    cd "$case_dir"
    git add scripts/staged-check.sh
  )
  run_staged_dry_run "$case_dir" "$out"

  assert_file_contains "$out" "Check diff whitespace: git diff --cached --check"
  assert_file_contains "$out" "Validate shell scripts: make scripts-check"
}

run_base_change_uses_base_diff_check() {
  local case_dir="$TMP_ROOT/base-diff-check"
  local out="$case_dir/out.log"
  setup_repo "$case_dir"

  printf '%s\n' 'export const baseChanged = true' > "$case_dir/web/src/App.tsx"
  (
    cd "$case_dir"
    git add web/src/App.tsx
    git commit -q -m "test: base mode change"
  )
  run_base_dry_run "$case_dir" "HEAD~1" "$out"

  assert_file_contains "$out" "Check diff whitespace: git diff --check HEAD~1...HEAD"
  assert_file_contains "$out" "Run frontend typecheck: cd web && npm run typecheck"
}

run_husky_hook_change_selects_scripts_check
run_any_change_selects_secret_leak_check
run_workflow_change_selects_workflows_check
run_ci_workflow_runs_toolchain_check
run_web_e2e_change_selects_typecheck_and_e2e
run_untracked_web_e2e_change_selects_typecheck_and_e2e
run_web_src_change_selects_typecheck_without_e2e
run_web_script_change_selects_frontend_and_scripts_checks
run_web_e2e_tsconfig_change_selects_typecheck_and_e2e
run_web_vitest_config_change_selects_unit_validation
run_web_vite_config_change_selects_build_validation
run_web_playwright_config_change_selects_e2e_validation
run_untracked_script_change_selects_scripts_check
run_untracked_newline_script_path_selects_scripts_check
run_script_typechange_selects_scripts_check
run_renamed_script_to_docs_selects_old_and_new_path_checks
run_doc_checker_change_selects_docs_and_scripts_checks
run_untracked_whitespace_checker_rejects_bad_text
run_untracked_whitespace_checker_rejects_space_before_tab_indent
run_example_config_change_selects_check_config
run_env_example_change_selects_docker_template_checks
run_docker_compose_change_selects_docker_template_checks
run_deploy_docker_compose_yaml_change_selects_docker_template_checks
run_dockerignore_change_selects_docker_build
run_docker_build_command_uses_configured_timeout_wrapper
run_docker_build_command_uses_gtimeout_fallback
run_docker_build_command_fails_without_timeout_tools
run_go_version_change_selects_quick_check
run_nvmrc_change_selects_frontend_checks
run_web_nvmrc_change_selects_frontend_and_toolchain_checks
run_golangci_config_change_selects_lint
run_golangci_yaml_config_change_selects_lint
run_precommit_config_change_selects_yaml_and_precommit_validation
run_dependabot_config_change_selects_yaml_validation
run_dependabot_yaml_config_change_selects_yaml_validation
run_codecov_config_change_selects_yaml_validation
run_codecov_yaml_config_change_selects_yaml_validation
run_issue_template_change_selects_yaml_validation
run_yaml_config_checker_validates_files
run_makefile_scripts_check_runs_web_node_script_checker
run_makefile_clean_removes_proto_generator_target
run_toolchain_checker_rejects_web_lock_manifest_drift
run_toolchain_checker_rejects_web_package_identity_drift
run_public_access_template_change_selects_template_check
run_makefile_change_selects_full_project_check
run_go_dependency_manifest_change_selects_security_check
run_web_dependency_manifest_change_selects_security_check
run_rust_dependency_manifest_change_selects_security_check
run_proto_change_selects_regen_and_quick_check
run_dataplane_rust_change_selects_fmt_tests_and_clippy
run_proto_generator_change_selects_regen_and_generator_checks
run_staged_change_uses_cached_diff_check
run_base_change_uses_base_diff_check

printf '[verify-changed-safety-test] all checks passed\n'
