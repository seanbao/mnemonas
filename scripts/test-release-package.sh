#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKFLOW="$PROJECT_ROOT/.github/workflows/release.yml"
INSTALLER="$PROJECT_ROOT/scripts/install-systemd.sh"

fail() {
	printf '[release-package-test] ERROR: %s\n' "$*" >&2
	exit 1
}

assert_file_contains() {
	local path="$1"
	local expected="$2"
	grep -Fq -- "$expected" "$path" || fail "$path does not contain: $expected"
}

assert_path_exists() {
	local path="$1"
	[[ -e "$path" ]] || fail "missing required release source path: $path"
}

assert_release_workflow_contains() {
	local expected

	for expected in "$@"; do
		assert_file_contains "$WORKFLOW" "$expected"
	done
}

assert_installer_contains() {
	local expected

	for expected in "$@"; do
		assert_file_contains "$INSTALLER" "$expected"
	done
}

for required_path in \
	"$PROJECT_ROOT/scripts/install-systemd.sh" \
	"$PROJECT_ROOT/scripts/mnemonas-dataplane-start.sh" \
	"$PROJECT_ROOT/scripts/mnemonas-doctor.sh" \
	"$PROJECT_ROOT/scripts/docker-quickstart.sh" \
	"$PROJECT_ROOT/scripts/docker-smoke.sh" \
	"$PROJECT_ROOT/scripts/docker-start.sh" \
	"$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" \
	"$PROJECT_ROOT/scripts/setup-reverse-proxy.sh" \
	"$PROJECT_ROOT/scripts/uninstall-systemd.sh" \
	"$PROJECT_ROOT/docker-compose.yml" \
	"$PROJECT_ROOT/.env.example" \
	"$PROJECT_ROOT/mnemonas.example.toml" \
	"$PROJECT_ROOT/README.md" \
	"$PROJECT_ROOT/README.en.md" \
	"$PROJECT_ROOT/CHANGELOG.md" \
	"$PROJECT_ROOT/CHANGELOG.en.md" \
	"$PROJECT_ROOT/SECURITY.md" \
	"$PROJECT_ROOT/SECURITY.zh-CN.md" \
	"$PROJECT_ROOT/SUPPORT.md" \
	"$PROJECT_ROOT/SUPPORT.en.md" \
	"$PROJECT_ROOT/LICENSE" \
	"$PROJECT_ROOT/docs" \
	"$PROJECT_ROOT/deploy"; do
	assert_path_exists "$required_path"
done

assert_release_workflow_contains \
	'mkdir -p "dist/$package_name/web" "dist/$package_name/scripts" "dist/$package_name/docs" "dist/$package_name/deploy"' \
	'install -m 755 dist/nasd-${{ matrix.os }}-${{ matrix.arch }} "dist/$package_name/nasd"' \
	'install -m 755 dist/dataplane-${{ matrix.os }}-${{ matrix.arch }} "dist/$package_name/dataplane"' \
	'cp -R web/dist/. "dist/$package_name/web/"' \
	'install -m 755 scripts/*.sh "dist/$package_name/scripts/"' \
	'cp -R docs/. "dist/$package_name/docs/"' \
	'cp -R deploy/. "dist/$package_name/deploy/"' \
	'install -m 644 docker-compose.yml .env.example "dist/$package_name/"' \
	'install -m 644 mnemonas.example.toml README.md README.en.md CHANGELOG.md CHANGELOG.en.md SECURITY.md SECURITY.zh-CN.md SUPPORT.md SUPPORT.en.md LICENSE "dist/$package_name/"'

assert_file_contains "$WORKFLOW" 'install -m 644 docker-compose.yml .env.example "dist/$package_name/"'
assert_file_contains "$WORKFLOW" 'release_image="ghcr.io/${GITHUB_REPOSITORY}:${VERSION#v}"'
assert_file_contains "$WORKFLOW" 'awk -v image="$release_image" -v version="$VERSION"'
assert_file_contains "$WORKFLOW" '/^MNEMONAS_IMAGE=/ { print "MNEMONAS_IMAGE=" image; next }'
assert_file_contains "$WORKFLOW" '/^MNEMONAS_VERSION=/ { print "MNEMONAS_VERSION=" version; next }'
assert_release_workflow_contains \
	'Build image for smoke test' \
	'platforms: linux/amd64' \
	'push: false' \
	'load: true' \
	'tags: mnemonas:release-smoke' \
	'Smoke test release image' \
	'./scripts/docker-smoke.sh mnemonas:release-smoke' \
	'platforms: linux/amd64,linux/arm64' \
	'push: true'

assert_installer_contains \
	'first_existing_file "$release_root/nasd" "$release_root/bin/nasd" "$PWD/nasd" "$PWD/bin/nasd"' \
	'first_existing_file "$release_root/dataplane" "$release_root/bin/dataplane" "$PWD/dataplane" "$PWD/bin/dataplane"' \
	'first_existing_file "$release_root/scripts/mnemonas-dataplane-start.sh" "$SCRIPT_DIR/mnemonas-dataplane-start.sh" "$PWD/scripts/mnemonas-dataplane-start.sh"' \
	'first_existing_file "$release_root/scripts/mnemonas-doctor.sh" "$SCRIPT_DIR/mnemonas-doctor.sh" "$PWD/scripts/mnemonas-doctor.sh"' \
	'first_existing_file "$release_root/scripts/setup-reverse-proxy.sh" "$SCRIPT_DIR/setup-reverse-proxy.sh" "$PWD/scripts/setup-reverse-proxy.sh"' \
	'first_existing_file "$release_root/scripts/uninstall-systemd.sh" "$SCRIPT_DIR/uninstall-systemd.sh" "$PWD/scripts/uninstall-systemd.sh"' \
	'first_built_web_dir "$release_root/web/dist" "$release_root/web" "$PWD/web/dist" "$PWD/web"' \
	'first_existing_file "$release_root/mnemonas.example.toml" "$PWD/mnemonas.example.toml"' \
	'names=(nasd dataplane mnemonas-dataplane-start)' \
	'names+=(mnemonas-doctor)' \
	'names+=(mnemonas-public-setup)' \
	'names+=(mnemonas-uninstall-systemd)'

printf '[release-package-test] all checks passed\n'
