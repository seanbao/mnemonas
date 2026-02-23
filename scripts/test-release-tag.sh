#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECK="$REPO_ROOT/scripts/check-release-tag.sh"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
	printf '[release-tag-test] ERROR: %s\n' "$*" >&2
	exit 1
}

assert_file_contains() {
	local path="$1"
	local expected="$2"

	grep -Fq -- "$expected" "$path" || fail "$path does not contain: $expected"
}

assert_passes() {
	local tag="$1"
	local out="$TMP_ROOT/pass-${tag//[^A-Za-z0-9_.-]/_}.log"

	bash "$CHECK" "$tag" >"$out"
	assert_file_contains "$out" "valid release tag: $tag"
}

assert_fails() {
	local tag="$1"
	local expected="$2"
	local out="$TMP_ROOT/fail-${tag//[^A-Za-z0-9_.-]/_}.log"
	local status

	set +e
	bash "$CHECK" "$tag" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "accepted invalid release tag: $tag"
	assert_file_contains "$out" "$expected"
}

assert_passes "v0.1.0"
assert_passes "v1.2.3"
assert_passes "v1.2.3-rc.1"
assert_passes "v1.2.3-alpha.7"
max_docker_prerelease="$(printf 'a%.0s' {1..122})"
assert_passes "v1.2.3-$max_docker_prerelease"

GITHUB_REF_NAME="v2.0.0-beta.1" bash "$CHECK" >"$TMP_ROOT/env.log"
assert_file_contains "$TMP_ROOT/env.log" "valid release tag: v2.0.0-beta.1"

assert_fails "" "release tag is required"
assert_fails "1.2.3" "release tag must match"
assert_fails "v1" "release tag must match"
assert_fails "v1.2" "release tag must match"
assert_fails "v1.2.3+build.1" "must not include build metadata"
assert_fails "v1.2.3 rc.1" "must not contain whitespace or control characters"
assert_fails $'v1.2.3\nrc.1' "must not contain whitespace or control characters"
assert_fails "v01.2.3" "numeric components must not contain leading zeroes"
assert_fails "v1.02.3" "numeric components must not contain leading zeroes"
assert_fails "v1.2.03" "numeric components must not contain leading zeroes"
assert_fails "v1.2.3-rc.01" "numeric prerelease identifiers must not contain leading zeroes"
assert_fails "v1.2.3-rc..1" "release tag must match"
assert_fails "v1.2.3-rc_1" "release tag must match"
too_long_docker_prerelease="$(printf 'a%.0s' {1..123})"
assert_fails "v1.2.3-$too_long_docker_prerelease" "without the v prefix must be at most 128 characters"

printf '[release-tag-test] all checks passed\n'
