#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECKER="$REPO_ROOT/scripts/check-secret-leaks.sh"

tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT

fail() {
	printf 'test-secret-leaks: %s\n' "$1" >&2
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

run_checker() {
	"$tmp/scripts/check-secret-leaks.sh"
}

private_key_header() {
	printf '%s%s\n' '-----BEGIN ' 'PRIVATE KEY-----'
}

write_private_key_fixture() {
	local path="$1"

	{
		private_key_header
		printf '%s\n' 'not-a-real-key'
		printf '%s' '-----END '
		printf '%s\n' 'PRIVATE KEY-----'
	} >"$path"
}

mkdir -p "$tmp/scripts" "$tmp/docs"
cp "$CHECKER" "$tmp/scripts/check-secret-leaks.sh"
chmod +x "$tmp/scripts/check-secret-leaks.sh"

cd "$tmp"
git init -q
git config user.email "mnemonas@example.invalid"
git config user.name "MnemoNAS Test"

printf '%s\n' 'placeholder token: <secret>' >docs/safe.txt
git add docs/safe.txt scripts/check-secret-leaks.sh

run_checker >"$tmp/safe.out" 2>"$tmp/safe.err" || {
	cat "$tmp/safe.err" >&2
	fail "safe placeholders should pass"
}

write_private_key_fixture "$tmp/private.pem"
if run_checker >"$tmp/private.out" 2>&1; then
	fail "untracked private key fixture should fail"
fi
assert_file_contains "$tmp/private.out" "private key block"
assert_file_not_contains "$tmp/private.out" "not-a-real-key"
assert_file_not_contains "$tmp/private.out" "$(private_key_header)"
rm -f "$tmp/private.pem"

github_token_body="AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
github_token="ghp_${github_token_body}"
printf '%s\n' "$github_token" >docs/github-token.txt
git add docs/github-token.txt
if run_checker >"$tmp/github.out" 2>&1; then
	fail "tracked GitHub token fixture should fail"
fi
assert_file_contains "$tmp/github.out" "GitHub token"
assert_file_not_contains "$tmp/github.out" "$github_token"
git rm -q --cached docs/github-token.txt
rm -f docs/github-token.txt

mkdir -p node_modules
write_private_key_fixture "$tmp/node_modules/leak.pem"
run_checker >"$tmp/skipped.out" 2>"$tmp/skipped.err" || {
	cat "$tmp/skipped.err" >&2
	fail "skipped dependency directories should not fail the scan"
}

printf 'test-secret-leaks: ok\n'
