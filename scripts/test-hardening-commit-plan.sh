#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLANNER="$REPO_ROOT/scripts/plan-hardening-commits.sh"

tmp="$(mktemp -d)"
output_dir="$(mktemp -d)"
trap 'rm -rf -- "$tmp" "$output_dir"' EXIT

fail() {
	printf 'test-hardening-commit-plan: %s\n' "$1" >&2
	exit 1
}

assert_file_contains() {
	local path="$1"
	local expected="$2"

	grep -Fq -- "$expected" "$path" || {
		cat "$path" >&2
		fail "$path does not contain: $expected"
	}
}

assert_file_not_contains() {
	local path="$1"
	local unexpected="$2"

	if grep -Fq -- "$unexpected" "$path"; then
		cat "$path" >&2
		fail "$path contains unexpected text: $unexpected"
	fi
}

mkdir -p "$tmp/scripts"
cp "$PLANNER" "$tmp/scripts/plan-hardening-commits.sh"
chmod +x "$tmp/scripts/plan-hardening-commits.sh"

cd "$tmp"
git init -q
git config user.email "mnemonas@example.invalid"
git config user.name "MnemoNAS Test"

mkdir -p .github/ISSUE_TEMPLATE .github/workflows dataplane/src docs internal/api internal/auth scripts web/e2e web/scripts web/src tools/proto-gen
touch README.md CODE_OF_CONDUCT.md CODE_OF_CONDUCT.zh-CN.md CONTRIBUTING.md CONTRIBUTING.en.md .github/pull_request_template.md .github/ISSUE_TEMPLATE/bug_report.yml Makefile Dockerfile go.mod dataplane/src/lib.rs tools/proto-gen/Cargo.toml
touch docs/testing-strategy.md internal/api/server.go internal/auth/user.go web/package.json web/src/App.tsx web/e2e/files.spec.ts
git add .
git commit -q -m "test: initial"

printf '%s\n' '# docs changed' >README.md
printf '%s\n' '# code of conduct changed' >CODE_OF_CONDUCT.md
printf '%s\n' '# chinese code of conduct changed' >CODE_OF_CONDUCT.zh-CN.md
printf '%s\n' '# contribution docs changed' >CONTRIBUTING.md
printf '%s\n' '# contributing docs changed' >CONTRIBUTING.en.md
printf '%s\n' '# pull request template changed' >.github/pull_request_template.md
printf '%s\n' 'name: Bug report' >.github/ISSUE_TEMPLATE/bug_report.yml
printf '%s\n' 'check: ; @true' >Makefile
printf '%s\n' 'FROM scratch' >Dockerfile
printf '%s\n' 'module example.invalid/mnemonas' >go.mod
printf '%s\n' 'package api' >internal/api/server.go
printf '%s\n' 'package auth' >internal/auth/user.go
printf '%s\n' '{"dependencies":{"left-pad":"1.3.0"}}' >web/package.json
printf '%s\n' 'export const app = true' >web/src/App.tsx
printf '%s\n' 'export const spec = true' >web/e2e/files.spec.ts
printf '%s\n' 'pub fn touched() {}' >dataplane/src/lib.rs
printf '%s\n' '[package]' 'name = "proto-gen"' 'version = "0.1.0"' >tools/proto-gen/Cargo.toml
printf '%s\n' 'console.log("check")' >web/scripts/check-node.cjs
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >scripts/release-readiness.sh
printf '%s\n' 'manual' >misc.txt

./scripts/plan-hardening-commits.sh >"$output_dir/plan.out" 2>"$output_dir/plan.err"

assert_file_contains "$output_dir/plan.out" "[hardening-commit-plan] grouped 20 changed file(s)"
assert_file_contains "$output_dir/plan.out" "docs: documentation compaction and bilingual index"
assert_file_contains "$output_dir/plan.out" "README.md"
assert_file_contains "$output_dir/plan.out" "CODE_OF_CONDUCT.md"
assert_file_contains "$output_dir/plan.out" "CODE_OF_CONDUCT.zh-CN.md"
assert_file_contains "$output_dir/plan.out" "CONTRIBUTING.md"
assert_file_contains "$output_dir/plan.out" "CONTRIBUTING.en.md"
assert_file_contains "$output_dir/plan.out" ".github/pull_request_template.md"
assert_file_contains "$output_dir/plan.out" ".github/ISSUE_TEMPLATE/bug_report.yml"
assert_file_contains "$output_dir/plan.out" "build(ci): local and CI gates"
assert_file_contains "$output_dir/plan.out" "Makefile"
assert_file_contains "$output_dir/plan.out" "web/scripts/check-node.cjs"
assert_file_contains "$output_dir/plan.out" "feat(api): path, archive, WebDAV, share, and access boundaries"
assert_file_contains "$output_dir/plan.out" "internal/api/server.go"
assert_file_contains "$output_dir/plan.out" "feat(core): auth, backup, storage, workspace, and runtime boundaries"
assert_file_contains "$output_dir/plan.out" "internal/auth/user.go"
assert_file_contains "$output_dir/plan.out" "go.mod"
assert_file_contains "$output_dir/plan.out" "feat(web): visible frontend experience and client contracts"
assert_file_contains "$output_dir/plan.out" "web/src/App.tsx"
assert_file_contains "$output_dir/plan.out" "web/e2e/files.spec.ts"
assert_file_contains "$output_dir/plan.out" "web/package.json"
assert_file_contains "$output_dir/plan.out" "build(docker-deploy): containers, deployment, and public entry"
assert_file_contains "$output_dir/plan.out" "Dockerfile"
assert_file_contains "$output_dir/plan.out" "scripts/release-readiness.sh"
assert_file_contains "$output_dir/plan.out" "build(dataplane): Rust and proto-generator baseline"
assert_file_contains "$output_dir/plan.out" "dataplane/src/lib.rs"
assert_file_contains "$output_dir/plan.out" "tools/proto-gen/Cargo.toml"
assert_file_contains "$output_dir/plan.out" "review(manual): paths that need manual grouping"
assert_file_contains "$output_dir/plan.out" "misc.txt"
assert_file_contains "$output_dir/plan.err" "review-manual contains 1 path(s)"

./scripts/plan-hardening-commits.sh --commands >"$output_dir/commands.out" 2>"$output_dir/commands.err"
assert_file_contains "$output_dir/commands.out" "git add -- \\"
assert_file_contains "$output_dir/commands.out" "  README.md"
assert_file_contains "$output_dir/commands.out" "# Validation:"

./scripts/plan-hardening-commits.sh --checks >"$output_dir/checks.out" 2>"$output_dir/checks.err"
assert_file_contains "$output_dir/checks.out" "make docs-check"
assert_file_contains "$output_dir/checks.out" "make scripts-check"
assert_file_contains "$output_dir/checks.out" "GOTOOLCHAIN=local CGO_ENABLED=1 bash ./scripts/with-test-dataplane.sh go test -race ./internal/api ./internal/share ./internal/webdav"
assert_file_contains "$output_dir/checks.out" "cd web && npm run test:e2e"
assert_file_contains "$output_dir/checks.out" "make security-check"
assert_file_contains "$output_dir/checks.out" "make security-check NPM_AUDIT=1"
assert_file_contains "$output_dir/checks.out" "git diff --check -- \\"

./scripts/plan-hardening-commits.sh --messages >"$output_dir/messages.out" 2>"$output_dir/messages.err"
assert_file_contains "$output_dir/messages.out" "docs: streamline bilingual documentation"
assert_file_contains "$output_dir/messages.out" "build(ci): harden validation gates"
assert_file_contains "$output_dir/messages.out" "feat(api): harden access and sharing boundaries"
assert_file_contains "$output_dir/messages.out" "feat(core): harden auth and storage boundaries"
assert_file_contains "$output_dir/messages.out" "feat(web): harden visible workflows"
assert_file_contains "$output_dir/messages.out" "build(docker-deploy): harden deployment paths"
assert_file_contains "$output_dir/messages.out" "build(dataplane): harden Rust validation"
assert_file_contains "$output_dir/messages.out" "chore(review): classify remaining hardening paths"

./scripts/plan-hardening-commits.sh --group feat-web >"$output_dir/group-web.out" 2>"$output_dir/group-web.err"
assert_file_contains "$output_dir/group-web.out" "[hardening-commit-plan] showing group feat-web"
assert_file_contains "$output_dir/group-web.out" "feat(web): visible frontend experience and client contracts"
assert_file_contains "$output_dir/group-web.out" "web/src/App.tsx"
assert_file_contains "$output_dir/group-web.out" "web/e2e/files.spec.ts"
assert_file_not_contains "$output_dir/group-web.out" "docs: documentation compaction and bilingual index"
assert_file_not_contains "$output_dir/group-web.out" "Dockerfile"

./scripts/plan-hardening-commits.sh --group build-ci --commands >"$output_dir/group-ci-commands.out" 2>"$output_dir/group-ci-commands.err"
assert_file_contains "$output_dir/group-ci-commands.out" "[hardening-commit-plan] showing group build-ci"
assert_file_contains "$output_dir/group-ci-commands.out" "# build(ci): local and CI gates"
assert_file_contains "$output_dir/group-ci-commands.out" "  Makefile"
assert_file_not_contains "$output_dir/group-ci-commands.out" "  Dockerfile"

./scripts/plan-hardening-commits.sh --group feat-web --checks >"$output_dir/group-web-checks.out" 2>"$output_dir/group-web-checks.err"
assert_file_contains "$output_dir/group-web-checks.out" "[hardening-commit-plan] showing group feat-web"
assert_file_contains "$output_dir/group-web-checks.out" "cd web && npm run lint"
assert_file_contains "$output_dir/group-web-checks.out" "cd web && npm run test:e2e"
assert_file_contains "$output_dir/group-web-checks.out" "make security-check NPM_AUDIT=1"
assert_file_contains "$output_dir/group-web-checks.out" "web/src/App.tsx"
assert_file_not_contains "$output_dir/group-web-checks.out" "make docs-check"
assert_file_not_contains "$output_dir/group-web-checks.out" "Dockerfile"

./scripts/plan-hardening-commits.sh --group feat-web --messages >"$output_dir/group-web-messages.out" 2>"$output_dir/group-web-messages.err"
assert_file_contains "$output_dir/group-web-messages.out" "[hardening-commit-plan] showing group feat-web"
assert_file_contains "$output_dir/group-web-messages.out" "feat(web): harden visible workflows"
assert_file_not_contains "$output_dir/group-web-messages.out" "docs: streamline bilingual documentation"
assert_file_not_contains "$output_dir/group-web-messages.out" "build(docker-deploy): harden deployment paths"

if ./scripts/plan-hardening-commits.sh --group does-not-exist >"$output_dir/bad-group.out" 2>"$output_dir/bad-group.err"; then
	fail "planner accepted an unknown group"
fi
assert_file_contains "$output_dir/bad-group.err" "unknown group: does-not-exist"

if ./scripts/plan-hardening-commits.sh --fail-on-manual >"$output_dir/fail-manual.out" 2>"$output_dir/fail-manual.err"; then
	fail "planner accepted review(manual) paths in strict mode"
fi
assert_file_contains "$output_dir/fail-manual.err" "review-manual contains 1 path(s)"

rm -f misc.txt
./scripts/plan-hardening-commits.sh --fail-on-manual >"$output_dir/no-manual.out" 2>"$output_dir/no-manual.err" || {
	cat "$output_dir/no-manual.err" >&2
	fail "planner rejected fully classified paths in strict mode"
}
assert_file_contains "$output_dir/no-manual.out" "[hardening-commit-plan] grouped 19 changed file(s)"

./scripts/plan-hardening-commits.sh --group review-manual >"$output_dir/group-manual-empty.out" 2>"$output_dir/group-manual-empty.err"
assert_file_contains "$output_dir/group-manual-empty.out" "group review-manual has no changed files"

./scripts/plan-hardening-commits.sh --group review-manual --messages >"$output_dir/group-manual-empty-messages.out" 2>"$output_dir/group-manual-empty-messages.err"
assert_file_contains "$output_dir/group-manual-empty-messages.out" "group review-manual has no changed files"

printf 'test-hardening-commit-plan: ok\n'
