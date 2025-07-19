#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
	printf '[release-artifact-verify-test] ERROR: %s\n' "$*" >&2
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

make_fake_docker() {
	local bin_dir="$1"
	local state_dir="$2"
	mkdir -p "$bin_dir" "$state_dir"

	write_executable "$bin_dir/docker" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'if [[ "${1:-}" == "manifest" && "${2:-}" == "inspect" ]]; then' \
		'  printf "%s\n" "${3:-}" > "$FAKE_DOCKER_STATE/image"' \
		'  [[ "${FAKE_DOCKER_IMAGE_FAIL:-0}" == "1" ]] && exit 1' \
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

write_checksums() {
	local dist_dir="$1"
	(
		cd "$dist_dir"
		sha256sum ./*.tar.gz >checksums.txt
	)
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
	write_checksums "$dist_dir"
}

run_complete_release_passes() {
	local case_dir="$TMP_ROOT/complete"
	local dist_dir="$case_dir/dist"
	local out="$case_dir/out.log"

	make_complete_release "$dist_dir" "v1.2.3" "seanbao/mnemonas"
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" \
		--version v1.2.3 \
		--repository seanbao/mnemonas \
		--require-targets \
		"$dist_dir" >"$out"

	assert_file_contains "$out" "verified 4 archive(s) for v1.2.3"
}

run_binary_checksum_marker_passes() {
	local case_dir="$TMP_ROOT/binary-checksum-marker"
	local dist_dir="$case_dir/dist"
	local out="$case_dir/out.log"

	make_complete_release "$dist_dir" "v1.2.3" "seanbao/mnemonas"
	awk '{ print $1 " *" $2 }' "$dist_dir/checksums.txt" >"$dist_dir/checksums.txt.tmp"
	mv "$dist_dir/checksums.txt.tmp" "$dist_dir/checksums.txt"

	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" \
		--version v1.2.3 \
		--repository seanbao/mnemonas \
		--require-targets \
		"$dist_dir" >"$out"

	assert_file_contains "$out" "verified 4 archive(s) for v1.2.3"
}

run_missing_target_fails_in_strict_mode() {
	local case_dir="$TMP_ROOT/missing-target"
	local dist_dir="$case_dir/dist"
	local out="$case_dir/out.log"
	local status

	mkdir -p "$dist_dir"
	make_release_archive "$dist_dir" "v1.2.3" "linux-amd64" "seanbao/mnemonas"
	write_checksums "$dist_dir"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" \
		--version v1.2.3 \
		--require-targets \
		"$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted a missing target"
	assert_file_contains "$out" "missing required release archive target: linux-arm64"
}

run_checksum_mismatch_fails() {
	local case_dir="$TMP_ROOT/checksum-mismatch"
	local dist_dir="$case_dir/dist"
	local out="$case_dir/out.log"
	local status

	make_complete_release "$dist_dir" "v1.2.3" "seanbao/mnemonas"
	printf 'tamper\n' >>"$dist_dir/mnemonas-v1.2.3-linux-amd64.tar.gz"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted a checksum mismatch"
	assert_file_contains "$out" "sha256 checksum verification failed"
}

run_checksum_path_escape_fails_before_checksum() {
	local case_dir="$TMP_ROOT/checksum-path-escape"
	local dist_dir="$case_dir/dist"
	local out="$case_dir/out.log"
	local status

	mkdir -p "$dist_dir"
	printf '%064d  mnemonas-v1.2.3-linux-amd64.tar.gz/../evil.tar.gz\n' 0 >"$dist_dir/checksums.txt"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted an unsafe checksum path"
	assert_file_contains "$out" "checksums.txt contains an unsafe file path"
}

run_archive_symlink_fails_before_checksum() {
	local case_dir="$TMP_ROOT/archive-symlink"
	local source_dir="$case_dir/source"
	local dist_dir="$case_dir/dist"
	local archive="mnemonas-v1.2.3-linux-amd64.tar.gz"
	local out="$case_dir/out.log"
	local status

	mkdir -p "$source_dir" "$dist_dir"
	make_release_archive "$source_dir" "v1.2.3" "linux-amd64" "seanbao/mnemonas"
	ln -s "$source_dir/$archive" "$dist_dir/$archive"
	printf '%064d  ./%s\n' 0 "$archive" >"$dist_dir/checksums.txt"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted a symlinked archive"
	assert_file_contains "$out" "expected regular file:"
	assert_file_contains "$out" "$archive"
}

run_wrong_env_image_fails() {
	local case_dir="$TMP_ROOT/wrong-image"
	local dist_dir="$case_dir/dist"
	local package_name="mnemonas-v1.2.3-linux-amd64"
	local out="$case_dir/out.log"
	local status

	mkdir -p "$dist_dir"
	make_release_archive "$dist_dir" "v1.2.3" "linux-amd64" "seanbao/mnemonas"
	mkdir -p "$case_dir/extract"
	tar -xzf "$dist_dir/$package_name.tar.gz" -C "$case_dir/extract"
	printf 'MNEMONAS_IMAGE=ghcr.io/example/other:1.2.3\nMNEMONAS_VERSION=v1.2.3\n' >"$case_dir/extract/$package_name/.env.example"
	rm -f -- "$dist_dir/$package_name.tar.gz"
	tar -czf "$dist_dir/$package_name.tar.gz" -C "$case_dir/extract" "$package_name"
	write_checksums "$dist_dir"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" \
		--version v1.2.3 \
		--repository seanbao/mnemonas \
		"$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted the wrong release image"
	assert_file_contains "$out" "MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas:1.2.3"
}

run_remote_image_check_uses_docker_manifest() {
	local case_dir="$TMP_ROOT/image-check"
	local dist_dir="$case_dir/dist"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"

	make_complete_release "$dist_dir" "v1.2.3" "seanbao/mnemonas"
	make_fake_docker "$fake_bin" "$state_dir"

	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" \
			--version v1.2.3 \
			--repository seanbao/mnemonas \
			--check-image \
			"$dist_dir" >"$out"

	assert_file_contains "$state_dir/image" "ghcr.io/seanbao/mnemonas:1.2.3"
	assert_file_contains "$out" "verified container image: ghcr.io/seanbao/mnemonas:1.2.3"
}

run_remote_image_check_failure_fails() {
	local case_dir="$TMP_ROOT/image-check-fails"
	local dist_dir="$case_dir/dist"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"
	local status

	make_complete_release "$dist_dir" "v1.2.3" "seanbao/mnemonas"
	make_fake_docker "$fake_bin" "$state_dir"

	set +e
	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		FAKE_DOCKER_IMAGE_FAIL=1 \
		bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" \
			--version v1.2.3 \
			--repository seanbao/mnemonas \
			--check-image \
			"$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted a missing container image"
	assert_file_contains "$out" "container image tag is not available: ghcr.io/seanbao/mnemonas:1.2.3"
}

run_complete_release_passes
run_binary_checksum_marker_passes
run_missing_target_fails_in_strict_mode
run_checksum_mismatch_fails
run_checksum_path_escape_fails_before_checksum
run_archive_symlink_fails_before_checksum
run_wrong_env_image_fails
run_remote_image_check_uses_docker_manifest
run_remote_image_check_failure_fails

printf '[release-artifact-verify-test] all checks passed\n'
