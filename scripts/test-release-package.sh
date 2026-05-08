#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKFLOW="$PROJECT_ROOT/.github/workflows/release.yml"
INSTALLER="$PROJECT_ROOT/scripts/install-systemd.sh"
TMP_ROOT=""

cleanup() {
	if [[ -n "$TMP_ROOT" && -d "$TMP_ROOT" ]]; then
		rm -rf -- "$TMP_ROOT"
	fi
}

trap cleanup EXIT

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

assert_file_not_contains() {
	local path="$1"
	local unexpected="$2"
	if grep -Fq -- "$unexpected" "$path"; then
		fail "$path unexpectedly contains: $unexpected"
	fi
}

assert_executable() {
	local path="$1"
	[[ -x "$path" ]] || fail "expected executable file: $path"
}

assert_tar_contains() {
	local manifest="$1"
	local expected="$2"
	grep -Fxq -- "$expected" "$manifest" || fail "archive manifest is missing: $expected"
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

build_release_fixture() {
	local version="v9.9.9-test"
	local os="linux"
	local arch="amd64"
	local repository="seanbao/mnemonas"
	local package_name="mnemonas-${version}-${os}-${arch}"
	local dist_dir="$TMP_ROOT/dist"
	local release_root="$dist_dir/$package_name"
	local archive="$dist_dir/${package_name}.tar.gz"
	local manifest="$TMP_ROOT/archive.txt"
	local extract_root="$TMP_ROOT/extract"
	local extracted="$extract_root/$package_name"
	local release_image="ghcr.io/${repository}:${version#v}"

	mkdir -p "$dist_dir" "$release_root/web" "$release_root/scripts" "$release_root/docs" "$release_root/deploy"
	printf '#!/usr/bin/env sh\nexit 0\n' >"$dist_dir/nasd-${os}-${arch}"
	printf '#!/usr/bin/env sh\nexit 0\n' >"$dist_dir/dataplane-${os}-${arch}"
	chmod 755 "$dist_dir/nasd-${os}-${arch}" "$dist_dir/dataplane-${os}-${arch}"
	printf '<!doctype html><title>MnemoNAS fixture</title>\n' >"$release_root/web/index.html"

	install -m 755 "$dist_dir/nasd-${os}-${arch}" "$release_root/nasd"
	install -m 755 "$dist_dir/dataplane-${os}-${arch}" "$release_root/dataplane"
	install -m 755 "$PROJECT_ROOT"/scripts/*.sh "$release_root/scripts/"
	cp -R "$PROJECT_ROOT/docs/." "$release_root/docs/"
	cp -R "$PROJECT_ROOT/deploy/." "$release_root/deploy/"
	install -m 644 "$PROJECT_ROOT/docker-compose.yml" "$PROJECT_ROOT/.env.example" "$release_root/"
	awk -v image="$release_image" -v release_version="$version" '
		/^MNEMONAS_IMAGE=/ { print "MNEMONAS_IMAGE=" image; next }
		/^MNEMONAS_VERSION=/ { print "MNEMONAS_VERSION=" release_version; next }
		{ print }
	' "$release_root/.env.example" >"$release_root/.env.example.tmp"
	mv "$release_root/.env.example.tmp" "$release_root/.env.example"
	install -m 644 \
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
		"$release_root/"

	tar -czf "$archive" -C "$dist_dir" "$package_name"
	tar -tzf "$archive" >"$manifest"
	awk -v prefix="$package_name/" 'NF && $0 != prefix && index($0, prefix) != 1 { exit 1 }' "$manifest" \
		|| fail "archive contains paths outside the top-level package directory"

	assert_tar_contains "$manifest" "$package_name/nasd"
	assert_tar_contains "$manifest" "$package_name/dataplane"
	assert_tar_contains "$manifest" "$package_name/web/index.html"
	assert_tar_contains "$manifest" "$package_name/scripts/install-systemd.sh"
	assert_tar_contains "$manifest" "$package_name/scripts/mnemonas-doctor.sh"
	assert_tar_contains "$manifest" "$package_name/docs/README.md"
	assert_tar_contains "$manifest" "$package_name/deploy/public-access/README.md"
	assert_tar_contains "$manifest" "$package_name/docker-compose.yml"
	assert_tar_contains "$manifest" "$package_name/.env.example"
	assert_tar_contains "$manifest" "$package_name/mnemonas.example.toml"
	assert_tar_contains "$manifest" "$package_name/README.md"
	assert_tar_contains "$manifest" "$package_name/README.en.md"
	assert_tar_contains "$manifest" "$package_name/CHANGELOG.md"
	assert_tar_contains "$manifest" "$package_name/CHANGELOG.en.md"
	assert_tar_contains "$manifest" "$package_name/SECURITY.md"
	assert_tar_contains "$manifest" "$package_name/SECURITY.zh-CN.md"
	assert_tar_contains "$manifest" "$package_name/SUPPORT.md"
	assert_tar_contains "$manifest" "$package_name/SUPPORT.en.md"
	assert_tar_contains "$manifest" "$package_name/LICENSE"
	assert_file_not_contains "$manifest" "$package_name/.env.example.tmp"

	mkdir -p "$extract_root"
	tar -xzf "$archive" -C "$extract_root"
	assert_executable "$extracted/nasd"
	assert_executable "$extracted/dataplane"
	assert_executable "$extracted/scripts/install-systemd.sh"
	assert_executable "$extracted/scripts/docker-quickstart.sh"
	assert_file_contains "$extracted/.env.example" "MNEMONAS_IMAGE=$release_image"
	assert_file_contains "$extracted/.env.example" "MNEMONAS_VERSION=$version"
	assert_file_not_contains "$extracted/.env.example" "MNEMONAS_IMAGE=mnemonas:local"
	assert_path_exists "$extracted/web/index.html"
	assert_path_exists "$extracted/docs/README.en.md"
	assert_path_exists "$extracted/deploy/public-access/traefik/docker-compose.yml"

	(
		cd "$dist_dir"
		sha256sum "$(basename "$archive")" >checksums.txt
		sha256sum -c checksums.txt >/dev/null
	)
	assert_file_contains "$dist_dir/checksums.txt" "$(basename "$archive")"
}

for required_path in \
	"$PROJECT_ROOT/scripts/install-systemd.sh" \
	"$PROJECT_ROOT/scripts/mnemonas-dataplane-start.sh" \
	"$PROJECT_ROOT/scripts/mnemonas-doctor.sh" \
	"$PROJECT_ROOT/scripts/docker-quickstart.sh" \
	"$PROJECT_ROOT/scripts/docker-smoke.sh" \
	"$PROJECT_ROOT/scripts/docker-start.sh" \
	"$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" \
	"$PROJECT_ROOT/scripts/verify-release-artifacts.sh" \
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
	'./scripts/check-release-tag.sh "$GITHUB_REF_NAME"' \
	'Build image for smoke test' \
	'platforms: linux/amd64' \
	'push: false' \
	'load: true' \
	'tags: mnemonas:release-smoke' \
	'Smoke test release image' \
	'./scripts/docker-smoke.sh mnemonas:release-smoke' \
	'platforms: linux/amd64,linux/arm64' \
	'push: true'
assert_release_workflow_contains \
	'sha256sum ./*.tar.gz > checksums.txt' \
	'Verify release artifacts' \
	'packages: read' \
	'Login to GitHub Container Registry' \
	'uses: docker/login-action@v3' \
	'./scripts/verify-release-artifacts.sh' \
	'--version "$GITHUB_REF_NAME"' \
	'--repository "$GITHUB_REPOSITORY"' \
	'--require-targets' \
	'--check-image' \
	'            dist' \
	'dist/checksums.txt'

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

TMP_ROOT="$(mktemp -d)"
build_release_fixture
assert_executable "$TMP_ROOT/extract/mnemonas-v9.9.9-test-linux-amd64/scripts/verify-release-artifacts.sh"

printf '[release-package-test] all checks passed\n'
