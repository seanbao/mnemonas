#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKFLOW="$PROJECT_ROOT/.github/workflows/release.yml"

fail() {
	printf '[release-package-test] ERROR: %s\n' "$*" >&2
	exit 1
}

assert_file_contains() {
	local path="$1"
	local expected="$2"
	grep -Fq -- "$expected" "$path" || fail "$path does not contain: $expected"
}

assert_file_contains "$WORKFLOW" 'install -m 644 docker-compose.yml .env.example "dist/$package_name/"'
assert_file_contains "$WORKFLOW" 'release_image="ghcr.io/${GITHUB_REPOSITORY}:${VERSION#v}"'
assert_file_contains "$WORKFLOW" 'awk -v image="$release_image" -v version="$VERSION"'
assert_file_contains "$WORKFLOW" '/^MNEMONAS_IMAGE=/ { print "MNEMONAS_IMAGE=" image; next }'
assert_file_contains "$WORKFLOW" '/^MNEMONAS_VERSION=/ { print "MNEMONAS_VERSION=" version; next }'
assert_file_contains "$WORKFLOW" 'install -m 755 scripts/*.sh "dist/$package_name/scripts/"'
assert_file_contains "$WORKFLOW" 'cp -R docs/. "dist/$package_name/docs/"'

printf '[release-package-test] all checks passed\n'
