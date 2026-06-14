#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
	printf '[published-release-verify-test] ERROR: %s\n' "$*" >&2
	exit 1
}

write_executable() {
	local path="$1"
	shift
	printf '%s\n' "$@" >"$path"
	chmod +x "$path"
}

assert_file_contains() {
	local path="$1"
	local expected="$2"

	grep -Fq -- "$expected" "$path" || fail "$path does not contain: $expected"
}

assert_file_not_exists() {
	local path="$1"

	[[ ! -e "$path" ]] || fail "$path should not exist"
}

make_fake_gh() {
	local bin_dir="$1"
	local state_dir="$2"
	mkdir -p "$bin_dir" "$state_dir"

	write_executable "$bin_dir/gh" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'if [[ "${1:-}" == "release" && "${2:-}" == "download" ]]; then' \
		'  tag="${3:-}"' \
		'  shift 3' \
		'  repo=""' \
		'  dir=""' \
		'  while [[ "$#" -gt 0 ]]; do' \
		'    case "$1" in' \
		'      --repo) repo="${2:-}"; shift 2 ;;' \
		'      --dir) dir="${2:-}"; shift 2 ;;' \
		'      *) printf "unexpected gh args: %s\n" "$*" >&2; exit 7 ;;' \
		'    esac' \
		'  done' \
		'  [[ -n "$tag" && -n "$repo" && -n "$dir" ]] || exit 8' \
		'  printf "%s\n" "$tag" > "$FAKE_GH_STATE/tag"' \
		'  printf "%s\n" "$repo" > "$FAKE_GH_STATE/repo"' \
		'  printf "%s\n" "$dir" > "$FAKE_GH_STATE/dir"' \
		'  mkdir -p -- "$dir"' \
		'  cp -R "$FAKE_GH_RELEASE_DIR"/. "$dir"/' \
		'  exit 0' \
		'fi' \
		'printf "unexpected gh args: %s\n" "$*" >&2' \
		'exit 7'
}

make_fake_docker() {
	local bin_dir="$1"
	local state_dir="$2"
	mkdir -p "$bin_dir" "$state_dir"

	write_executable "$bin_dir/docker" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'if [[ "${1:-}" == "manifest" && "${2:-}" == "inspect" ]]; then' \
		'  printf "%s\n" "${3:-}" > "$FAKE_DOCKER_STATE/image"' \
		'  printf "{}\n"' \
		'  exit 0' \
		'fi' \
		'printf "unexpected docker args: %s\n" "$*" >&2' \
		'exit 7'
}

make_release_archive() {
	local dist_dir="$1"
	local version="$2"
	local target="$3"
	local repository="$4"
	local package_name="mnemonas-${version}-${target}"
	local release_root="$dist_dir/$package_name"
	local image="ghcr.io/${repository}:${version#v}"

	mkdir -p \
		"$release_root/web" \
		"$release_root/scripts" \
		"$release_root/docs" \
		"$release_root/deploy/public-access/traefik"

	write_executable "$release_root/nasd" '#!/usr/bin/env sh' 'exit 0'
	write_executable "$release_root/dataplane" '#!/usr/bin/env sh' 'exit 0'
	write_executable "$release_root/scripts/install-systemd.sh" '#!/usr/bin/env sh' 'exit 0'
	write_executable "$release_root/scripts/docker-quickstart.sh" '#!/usr/bin/env sh' 'exit 0'
	write_executable "$release_root/scripts/mnemonas-doctor.sh" '#!/usr/bin/env sh' 'exit 0'
	write_executable "$release_root/scripts/verify-release-artifacts.sh" '#!/usr/bin/env sh' 'exit 0'
	write_executable "$release_root/scripts/verify-published-release.sh" '#!/usr/bin/env sh' 'exit 0'

	printf '<!doctype html><title>MnemoNAS</title>\n' >"$release_root/web/index.html"
	printf '# docs\n' >"$release_root/docs/README.md"
	printf '# docs\n' >"$release_root/docs/README.en.md"
	printf '# public access\n' >"$release_root/deploy/public-access/README.md"
	printf 'services:\n  mnemonas:\n    image: mnemonas:local\n' >"$release_root/deploy/public-access/traefik/docker-compose.yml"
	printf 'services:\n  mnemonas:\n    image: ${MNEMONAS_IMAGE:-mnemonas:local}\n' >"$release_root/docker-compose.yml"
	printf 'MNEMONAS_IMAGE=%s\nMNEMONAS_VERSION=%s\n' "$image" "$version" >"$release_root/.env.example"
	printf 'root = "/data"\n' >"$release_root/mnemonas.example.toml"
	printf '# README\n' >"$release_root/README.md"
	printf '# README\n' >"$release_root/README.en.md"
	printf '# CHANGELOG\n' >"$release_root/CHANGELOG.md"
	printf '# CHANGELOG\n' >"$release_root/CHANGELOG.en.md"
	printf '# SECURITY\n' >"$release_root/SECURITY.md"
	printf '# SECURITY\n' >"$release_root/SECURITY.zh-CN.md"
	printf '# SUPPORT\n' >"$release_root/SUPPORT.md"
	printf '# SUPPORT\n' >"$release_root/SUPPORT.en.md"
	printf 'license\n' >"$release_root/LICENSE"

	tar -czf "$dist_dir/${package_name}.tar.gz" -C "$dist_dir" "$package_name"
	rm -rf -- "$release_root"
}

make_complete_release() {
	local dist_dir="$1"
	local version="$2"
	local repository="$3"
	local target

	mkdir -p "$dist_dir"
	for target in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
		make_release_archive "$dist_dir" "$version" "$target" "$repository"
	done
	(
		cd "$dist_dir"
		sha256sum ./*.tar.gz >checksums.txt
	)
}

run_downloads_and_verifies_published_release() {
	local case_dir="$TMP_ROOT/download-and-verify"
	local release_dir="$case_dir/release"
	local artifact_dir="$case_dir/artifacts"
	local fake_bin="$case_dir/bin"
	local gh_state="$case_dir/gh-state"
	local docker_state="$case_dir/docker-state"
	local out="$case_dir/out.log"

	make_complete_release "$release_dir" "v1.2.3" "seanbao/mnemonas"
	make_fake_gh "$fake_bin" "$gh_state"
	make_fake_docker "$fake_bin" "$docker_state"

	PATH="$fake_bin:$PATH" \
		FAKE_GH_RELEASE_DIR="$release_dir" \
		FAKE_GH_STATE="$gh_state" \
		FAKE_DOCKER_STATE="$docker_state" \
		bash "$REPO_ROOT/scripts/verify-published-release.sh" \
			--version v1.2.3 \
			--repository seanbao/mnemonas \
			--artifact-dir "$artifact_dir" >"$out"

	assert_file_contains "$gh_state/tag" "v1.2.3"
	assert_file_contains "$gh_state/repo" "seanbao/mnemonas"
	assert_file_contains "$gh_state/dir" "$artifact_dir"
	assert_file_contains "$docker_state/image" "ghcr.io/seanbao/mnemonas:1.2.3"
	assert_file_contains "$out" "verified targets: linux-amd64 linux-arm64 darwin-amd64 darwin-arm64"
	assert_file_contains "$out" "verified published release v1.2.3 from seanbao/mnemonas"
}

run_skip_image_check_avoids_docker() {
	local case_dir="$TMP_ROOT/skip-image"
	local release_dir="$case_dir/release"
	local artifact_dir="$case_dir/artifacts"
	local fake_bin="$case_dir/bin"
	local gh_state="$case_dir/gh-state"
	local out="$case_dir/out.log"

	make_complete_release "$release_dir" "v1.2.3" "seanbao/mnemonas"
	make_fake_gh "$fake_bin" "$gh_state"

	PATH="$fake_bin:/usr/bin:/bin" \
		FAKE_GH_RELEASE_DIR="$release_dir" \
		FAKE_GH_STATE="$gh_state" \
		bash "$REPO_ROOT/scripts/verify-published-release.sh" \
			--version v1.2.3 \
			--repository seanbao/mnemonas \
			--skip-image-check \
			--artifact-dir "$artifact_dir" >"$out"

	assert_file_contains "$out" "verified 4 archive(s) for v1.2.3"
	assert_file_contains "$out" "verified published release v1.2.3 from seanbao/mnemonas"
}

run_dash_prefixed_artifact_dir_downloads_and_verifies() {
	local case_dir="$TMP_ROOT/dash-prefixed-artifact-dir"
	local release_dir="$case_dir/release"
	local fake_bin="$case_dir/bin"
	local gh_state="$case_dir/gh-state"
	local docker_state="$case_dir/docker-state"
	local out="$case_dir/out.log"

	make_complete_release "$release_dir" "v1.2.3" "seanbao/mnemonas"
	make_fake_gh "$fake_bin" "$gh_state"
	make_fake_docker "$fake_bin" "$docker_state"

	(
		cd "$case_dir"
		PATH="$fake_bin:$PATH" \
			FAKE_GH_RELEASE_DIR="$release_dir" \
			FAKE_GH_STATE="$gh_state" \
			FAKE_DOCKER_STATE="$docker_state" \
			bash "$REPO_ROOT/scripts/verify-published-release.sh" \
				--version v1.2.3 \
				--repository seanbao/mnemonas \
				--artifact-dir -artifacts >"$out"
	)

	assert_file_contains "$gh_state/dir" "-artifacts"
	assert_file_contains "$docker_state/image" "ghcr.io/seanbao/mnemonas:1.2.3"
	assert_file_contains "$out" "verified targets: linux-amd64 linux-arm64 darwin-amd64 darwin-arm64"
	assert_file_contains "$out" "verified published release v1.2.3 from seanbao/mnemonas"
	[[ -f "$case_dir/-artifacts/checksums.txt" ]] || fail "dash-prefixed artifact directory was not populated"
}

run_non_empty_artifact_dir_fails_before_download() {
	local case_dir="$TMP_ROOT/non-empty-artifact-dir"
	local artifact_dir="$case_dir/artifacts"
	local fake_bin="$case_dir/bin"
	local gh_state="$case_dir/gh-state"
	local out="$case_dir/out.log"
	local status

	mkdir -p "$artifact_dir"
	printf 'stale\n' >"$artifact_dir/stale.txt"
	make_fake_gh "$fake_bin" "$gh_state"

	set +e
	PATH="$fake_bin:$PATH" \
		FAKE_GH_RELEASE_DIR="$case_dir/release" \
		FAKE_GH_STATE="$gh_state" \
		bash "$REPO_ROOT/scripts/verify-published-release.sh" \
			--version v1.2.3 \
			--repository seanbao/mnemonas \
			--artifact-dir "$artifact_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "published release verifier accepted a non-empty artifact directory"
	assert_file_contains "$out" "artifact directory must be empty before download"
	assert_file_not_exists "$gh_state/tag"
}

run_invalid_version_fails_before_download() {
	local case_dir="$TMP_ROOT/invalid-version"
	local fake_bin="$case_dir/bin"
	local gh_state="$case_dir/gh-state"
	local out="$case_dir/out.log"
	local status

	make_fake_gh "$fake_bin" "$gh_state"

	set +e
	PATH="$fake_bin:$PATH" \
		FAKE_GH_RELEASE_DIR="$case_dir/release" \
		FAKE_GH_STATE="$gh_state" \
		bash "$REPO_ROOT/scripts/verify-published-release.sh" \
			--version "v01.2.3" \
			--repository seanbao/mnemonas >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "published release verifier accepted an invalid release version"
	assert_file_contains "$out" "release version numeric components must not contain leading zeroes"
	assert_file_not_exists "$gh_state/tag"
}

run_invalid_repository_fails_before_download() {
	local case_dir="$TMP_ROOT/invalid-repository"
	local fake_bin="$case_dir/bin"
	local gh_state="$case_dir/gh-state"
	local out="$case_dir/out.log"
	local status

	make_fake_gh "$fake_bin" "$gh_state"

	set +e
	PATH="$fake_bin:$PATH" \
		FAKE_GH_RELEASE_DIR="$case_dir/release" \
		FAKE_GH_STATE="$gh_state" \
		bash "$REPO_ROOT/scripts/verify-published-release.sh" \
			--version v1.2.3 \
			--repository SeanBao/mnemonas >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "published release verifier accepted an invalid repository"
	assert_file_contains "$out" "repository must be lowercase OWNER/REPO for GHCR image tags"
	assert_file_not_exists "$gh_state/tag"
}

run_downloads_and_verifies_published_release
run_skip_image_check_avoids_docker
run_dash_prefixed_artifact_dir_downloads_and_verifies
run_non_empty_artifact_dir_fails_before_download
run_invalid_version_fails_before_download
run_invalid_repository_fails_before_download

printf '[published-release-verify-test] all checks passed\n'
