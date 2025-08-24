#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
	printf '[commit-message-test] ERROR: %s\n' "$*" >&2
	exit 1
}

assert_file_contains() {
	local path="$1"
	local expected="$2"
	grep -Fq -- "$expected" "$path" || fail "$path does not contain: $expected"
}

write_message() {
	local path="$1"
	local subject="$2"
	printf '# ignored comment\n\n%s\n\nbody\n' "$subject" > "$path"
}

run_accepts() {
	local name="$1"
	local subject="$2"
	local message_file="$TMP_ROOT/$name.txt"
	write_message "$message_file" "$subject"
	"$REPO_ROOT/scripts/check-commit-message.sh" "$message_file" > "$TMP_ROOT/$name.log" 2>&1
}

run_rejects() {
	local name="$1"
	local subject="$2"
	local expected="$3"
	local message_file="$TMP_ROOT/$name.txt"
	local status
	write_message "$message_file" "$subject"

	set +e
	"$REPO_ROOT/scripts/check-commit-message.sh" "$message_file" > "$TMP_ROOT/$name.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "$name was accepted"
	assert_file_contains "$TMP_ROOT/$name.log" "$expected"
}

run_accepts "conventional" "fix(api): reject invalid share passwords"
run_accepts "breaking" "feat!: drop legacy session storage"
run_accepts "chinese" "docs: 更新公网部署说明"
run_accepts "merge" "Merge branch 'main'"
run_accepts "revert" 'Revert "fix(api): reject invalid share passwords"'

run_rejects "unknown-type" "bug(api): reject invalid share passwords" "subject must use Conventional Commits"
run_rejects "uppercase" "fix(api): Reject invalid share passwords" "subject description must start lowercase"
run_rejects "period" "fix(api): reject invalid share passwords." "subject must not end with a period"
run_rejects "long-subject" "fix(api): reject invalid share passwords because the generated link can leak credentials" "subject exceeds 72 characters"

printf '[commit-message-test] all checks passed\n'
