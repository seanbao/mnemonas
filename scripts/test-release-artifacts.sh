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

assert_file_not_contains() {
	local path="$1"
	local unexpected="$2"

	if grep -Fq -- "$unexpected" "$path"; then
		fail "$path contains unexpected text: $unexpected"
	fi
}

make_fake_docker() {
	local bin_dir="$1"
	local state_dir="$2"
	mkdir -p "$bin_dir" "$state_dir"

	write_executable "$bin_dir/docker" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'if [[ "${1:-}" == "manifest" && "${2:-}" == "inspect" ]]; then' \
		'  count_file="$FAKE_DOCKER_STATE/count"' \
		'  count=0' \
		'  [[ -f "$count_file" ]] && read -r count < "$count_file"' \
		'  count=$((count + 1))' \
		'  printf "%s\n" "$count" > "$count_file"' \
		'  printf "%s\n" "${3:-}" > "$FAKE_DOCKER_STATE/image"' \
		'  [[ "${FAKE_DOCKER_IMAGE_FAIL:-0}" == "1" ]] && exit 1' \
		'  failures="${FAKE_DOCKER_IMAGE_FAILS_BEFORE_SUCCESS:-0}"' \
		'  if [[ "$failures" =~ ^[0-9]+$ && "$count" -le "$failures" ]]; then exit 1; fi' \
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

	assert_file_contains "$out" "verified targets: linux-amd64 linux-arm64 darwin-amd64 darwin-arm64"
	assert_file_contains "$out" "verified 4 archive(s) for v1.2.3"
}

run_dash_prefixed_artifact_directory_passes() {
	local case_dir="$TMP_ROOT/dash-prefixed-artifact-dir"
	local dist_dir="$case_dir/-artifacts"
	local out="$case_dir/out.log"

	make_complete_release "$dist_dir" "v1.2.3" "seanbao/mnemonas"

	(
		cd "$case_dir"
		bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" \
			--version v1.2.3 \
			--repository seanbao/mnemonas \
			--require-targets \
			-- \
			"-artifacts"
	) >"$out"

	assert_file_contains "$out" "verified targets: linux-amd64 linux-arm64 darwin-amd64 darwin-arm64"
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

run_invalid_repository_fails_before_artifact_checks() {
	local case_dir="$TMP_ROOT/invalid-repository"
	local dist_dir="$case_dir/dist"
	local out
	local status
	local repository
	local expected
	local case_name

	make_complete_release "$dist_dir" "v1.2.3" "seanbao/mnemonas"

	while IFS='|' read -r case_name repository expected; do
		[[ -n "$case_name" ]] || continue
		out="$case_dir/$case_name.log"
		set +e
		bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" \
			--version v1.2.3 \
			--repository "$repository" \
			"$dist_dir" >"$out" 2>&1
		status=$?
		set -e

		[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted invalid repository: $repository"
		assert_file_contains "$out" "$expected"
	done <<'EOF'
uppercase|SeanBao/mnemonas|repository must be lowercase OWNER/REPO for GHCR image tags
extra-slash|seanbao/mnemonas/extra|repository must be in OWNER/REPO form
missing-owner|/mnemonas|repository owner must not be empty
missing-name|seanbao/|repository name must not be empty
whitespace|seanbao/mnemonas test|repository must not contain whitespace or control characters
control|seanbao/mnemonas	test|repository must not contain whitespace or control characters
bad-owner|sean_bao/mnemonas|repository owner must use lowercase letters
bad-name|seanbao/-mnemonas|repository name must use lowercase letters
EOF
}

run_invalid_version_argument_fails_before_artifact_checks() {
	local case_dir="$TMP_ROOT/invalid-version-argument"
	local dist_dir="$case_dir/dist"
	local out
	local status
	local version
	local expected
	local case_name
	local too_long_prerelease

	make_complete_release "$dist_dir" "v1.2.3" "seanbao/mnemonas"

	while IFS='|' read -r case_name version expected; do
		[[ -n "$case_name" ]] || continue
		out="$case_dir/$case_name.log"
		set +e
		bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" \
			--version "$version" \
			--repository seanbao/mnemonas \
			"$dist_dir" >"$out" 2>&1
		status=$?
		set -e

		[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted invalid --version: $version"
		assert_file_contains "$out" "$expected"
	done <<'EOF'
build-metadata|v1.2.3+build.1|release version must not include build metadata
missing-v|1.2.3|release version must match vMAJOR.MINOR.PATCH
leading-zero|v01.2.3|release version numeric components must not contain leading zeroes
prerelease-leading-zero|v1.2.3-rc.01|release version numeric prerelease identifiers must not contain leading zeroes
EOF

	too_long_prerelease="$(printf 'a%.0s' {1..123})"
	out="$case_dir/too-long-docker-tag.log"
	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" \
		--version "v1.2.3-$too_long_prerelease" \
		--repository seanbao/mnemonas \
		"$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted an overlong Docker tag release version"
	assert_file_contains "$out" "release version without the v prefix must be at most 128 characters"
}

run_archive_version_inference_rejects_invalid_release_version() {
	local case_dir="$TMP_ROOT/archive-invalid-version"
	local dist_dir="$case_dir/dist"
	local out="$case_dir/out.log"
	local status

	mkdir -p "$dist_dir"
	make_release_archive "$dist_dir" "v1.2.3+build.1" "linux-amd64" "seanbao/mnemonas"
	write_checksums "$dist_dir"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted an invalid inferred release version"
	assert_file_contains "$out" "release version must not include build metadata"
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

run_checksum_control_character_path_fails_before_checksum() {
	local case_dir="$TMP_ROOT/checksum-control-character-path"
	local dist_dir="$case_dir/dist"
	local out="$case_dir/out.log"
	local archive_name=$'mnemonas-v1.2.3-linux-amd64\t.tar.gz'
	local status

	mkdir -p "$dist_dir"
	printf 'unsafe\n' >"$dist_dir/$archive_name"
	printf '%064d  %s\n' 0 "$archive_name" >"$dist_dir/checksums.txt"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted a checksum path with a control character"
	assert_file_contains "$out" "checksums.txt contains a control character in file path"
	assert_file_not_contains "$out" "$archive_name"
}

run_checksum_whitespace_path_fails_before_checksum() {
	local case_dir="$TMP_ROOT/checksum-whitespace-path"
	local dist_dir="$case_dir/dist"
	local out="$case_dir/out.log"
	local archive_name="mnemonas-v1.2.3 candidate-linux-amd64.tar.gz"
	local status

	mkdir -p "$dist_dir"
	printf 'unsafe\n' >"$dist_dir/$archive_name"
	printf '%064d  %s\n' 0 "$archive_name" >"$dist_dir/checksums.txt"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted a checksum path with whitespace"
	assert_file_contains "$out" "checksums.txt contains whitespace in file path"
}

run_archive_whitespace_filename_fails_before_checksum() {
	local case_dir="$TMP_ROOT/archive-whitespace-filename"
	local dist_dir="$case_dir/dist"
	local out="$case_dir/out.log"
	local archive_name="mnemonas-v1.2.3 candidate-linux-amd64.tar.gz"
	local status

	mkdir -p "$dist_dir"
	make_release_archive "$dist_dir" "v1.2.3" "linux-amd64" "seanbao/mnemonas"
	write_checksums "$dist_dir"
	printf 'unsafe\n' >"$dist_dir/$archive_name"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted an archive filename with whitespace"
	assert_file_contains "$out" "release archive filename contains whitespace"
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

run_unexpected_artifact_entry_fails_before_checksum() {
	local case_dir="$TMP_ROOT/unexpected-artifact-entry"
	local dist_dir="$case_dir/dist"
	local out="$case_dir/out.log"
	local status

	make_complete_release "$dist_dir" "v1.2.3" "seanbao/mnemonas"
	printf 'unexpected\n' >"$dist_dir/release-notes.txt"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted an unexpected artifact directory entry"
	assert_file_contains "$out" "artifact directory contains an unsupported entry: release-notes.txt"
}

run_unexpected_artifact_control_character_entry_escapes_diagnostic() {
	local case_dir="$TMP_ROOT/unexpected-artifact-control-character-entry"
	local dist_dir="$case_dir/dist"
	local out="$case_dir/out.log"
	local entry_name=$'release\tnotes.txt'
	local status

	make_complete_release "$dist_dir" "v1.2.3" "seanbao/mnemonas"
	printf 'unexpected\n' >"$dist_dir/$entry_name"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted an unexpected artifact directory entry with a control character"
	assert_file_contains "$out" "artifact directory contains an unsupported entry"
	assert_file_not_contains "$out" "$entry_name"
}

run_archive_entry_symlink_fails() {
	local case_dir="$TMP_ROOT/archive-entry-symlink"
	local dist_dir="$case_dir/dist"
	local package_name="mnemonas-v1.2.3-linux-amd64"
	local out="$case_dir/out.log"
	local status

	mkdir -p "$dist_dir" "$case_dir/extract"
	make_release_archive "$dist_dir" "v1.2.3" "linux-amd64" "seanbao/mnemonas"
	tar -xzf "$dist_dir/$package_name.tar.gz" -C "$case_dir/extract"
	ln -s README.md "$case_dir/extract/$package_name/README.link"
	rm -f -- "$dist_dir/$package_name.tar.gz"
	tar -czf "$dist_dir/$package_name.tar.gz" -C "$case_dir/extract" "$package_name"
	write_checksums "$dist_dir"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted a symlink entry"
	assert_file_contains "$out" "archive contains unsupported entry type 'l'"
}

run_archive_entry_hardlink_fails() {
	local case_dir="$TMP_ROOT/archive-entry-hardlink"
	local dist_dir="$case_dir/dist"
	local package_name="mnemonas-v1.2.3-linux-amd64"
	local out="$case_dir/out.log"
	local status

	mkdir -p "$dist_dir" "$case_dir/extract"
	make_release_archive "$dist_dir" "v1.2.3" "linux-amd64" "seanbao/mnemonas"
	tar -xzf "$dist_dir/$package_name.tar.gz" -C "$case_dir/extract"
	ln "$case_dir/extract/$package_name/README.md" "$case_dir/extract/$package_name/README.hardlink"
	rm -f -- "$dist_dir/$package_name.tar.gz"
	tar -czf "$dist_dir/$package_name.tar.gz" -C "$case_dir/extract" "$package_name"
	write_checksums "$dist_dir"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted a hardlink entry"
	assert_file_contains "$out" "archive contains unsupported entry type 'h'"
}

run_archive_duplicate_entry_fails() {
	local case_dir="$TMP_ROOT/archive-duplicate-entry"
	local dist_dir="$case_dir/dist"
	local package_name="mnemonas-v1.2.3-linux-amd64"
	local tar_path="$case_dir/$package_name.tar"
	local out="$case_dir/out.log"
	local status

	mkdir -p "$dist_dir" "$case_dir/extract"
	make_release_archive "$dist_dir" "v1.2.3" "linux-amd64" "seanbao/mnemonas"
	tar -xzf "$dist_dir/$package_name.tar.gz" -C "$case_dir/extract"
	rm -f -- "$dist_dir/$package_name.tar.gz"
	tar -cf "$tar_path" -C "$case_dir/extract" "$package_name"
	tar -rf "$tar_path" -C "$case_dir/extract" "$package_name/README.md"
	gzip -c "$tar_path" >"$dist_dir/$package_name.tar.gz"
	write_checksums "$dist_dir"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted a duplicate archive entry"
	assert_file_contains "$out" "archive contains duplicate entry: $package_name/README.md"
}

run_archive_dot_segment_entry_fails() {
	local case_dir="$TMP_ROOT/archive-dot-segment-entry"
	local dist_dir="$case_dir/dist"
	local package_name="mnemonas-v1.2.3-linux-amd64"
	local out="$case_dir/out.log"
	local status

	mkdir -p "$dist_dir" "$case_dir/extract"
	make_release_archive "$dist_dir" "v1.2.3" "linux-amd64" "seanbao/mnemonas"
	tar -xzf "$dist_dir/$package_name.tar.gz" -C "$case_dir/extract"
	rm -f -- "$dist_dir/$package_name.tar.gz"
	tar -czf "$dist_dir/$package_name.tar.gz" \
		-C "$case_dir/extract" "$package_name" \
		-C "$case_dir/extract" "$package_name/./README.md"
	write_checksums "$dist_dir"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted a dot-segment archive entry"
	assert_file_contains "$out" "archive entry has an unsafe path segment: $package_name/./README.md"
}

run_archive_backslash_entry_fails() {
	local case_dir="$TMP_ROOT/archive-backslash-entry"
	local dist_dir="$case_dir/dist"
	local package_name="mnemonas-v1.2.3-linux-amd64"
	local out="$case_dir/out.log"
	local status

	mkdir -p "$dist_dir" "$case_dir/extract"
	make_release_archive "$dist_dir" "v1.2.3" "linux-amd64" "seanbao/mnemonas"
	tar -xzf "$dist_dir/$package_name.tar.gz" -C "$case_dir/extract"
	printf 'unsafe\n' >"$case_dir/extract/$package_name/README\\evil.md"
	rm -f -- "$dist_dir/$package_name.tar.gz"
	tar -czf "$dist_dir/$package_name.tar.gz" -C "$case_dir/extract" "$package_name"
	write_checksums "$dist_dir"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted a backslash archive entry"
	assert_file_contains "$out" "archive entry contains a backslash: $package_name/README"
}

run_archive_control_character_entry_fails() {
	local case_dir="$TMP_ROOT/archive-control-character-entry"
	local dist_dir="$case_dir/dist"
	local package_name="mnemonas-v1.2.3-linux-amd64"
	local out="$case_dir/out.log"
	local entry_name=$'README\tbad.md'
	local status

	mkdir -p "$dist_dir" "$case_dir/extract"
	make_release_archive "$dist_dir" "v1.2.3" "linux-amd64" "seanbao/mnemonas"
	tar -xzf "$dist_dir/$package_name.tar.gz" -C "$case_dir/extract"
	printf 'unsafe\n' >"$case_dir/extract/$package_name/$entry_name"
	rm -f -- "$dist_dir/$package_name.tar.gz"
	tar -czf "$dist_dir/$package_name.tar.gz" -C "$case_dir/extract" "$package_name"
	write_checksums "$dist_dir"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted an archive entry with a control character"
	assert_file_contains "$out" "archive entry contains a control character"
	assert_file_not_contains "$out" "$entry_name"
}

run_archive_whitespace_entry_fails() {
	local case_dir="$TMP_ROOT/archive-whitespace-entry"
	local dist_dir="$case_dir/dist"
	local package_name="mnemonas-v1.2.3-linux-amd64"
	local out="$case_dir/out.log"
	local status

	mkdir -p "$dist_dir" "$case_dir/extract"
	make_release_archive "$dist_dir" "v1.2.3" "linux-amd64" "seanbao/mnemonas"
	tar -xzf "$dist_dir/$package_name.tar.gz" -C "$case_dir/extract"
	printf 'unsafe\n' >"$case_dir/extract/$package_name/README bad.md"
	rm -f -- "$dist_dir/$package_name.tar.gz"
	tar -czf "$dist_dir/$package_name.tar.gz" -C "$case_dir/extract" "$package_name"
	write_checksums "$dist_dir"

	set +e
	bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" "$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted an archive entry with whitespace"
	assert_file_contains "$out" "archive entry contains whitespace: $package_name/README bad.md"
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

run_remote_image_check_retries_transient_manifest_failure() {
	local case_dir="$TMP_ROOT/image-check-retry"
	local dist_dir="$case_dir/dist"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"

	make_complete_release "$dist_dir" "v1.2.3" "seanbao/mnemonas"
	make_fake_docker "$fake_bin" "$state_dir"

	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		FAKE_DOCKER_IMAGE_FAILS_BEFORE_SUCCESS=2 \
		MNEMONAS_RELEASE_IMAGE_CHECK_RETRIES=3 \
		MNEMONAS_RELEASE_IMAGE_CHECK_SLEEP_SECONDS=0 \
		bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" \
			--version v1.2.3 \
			--repository seanbao/mnemonas \
			--check-image \
			"$dist_dir" >"$out" 2>&1

	assert_file_contains "$state_dir/count" "3"
	assert_file_contains "$out" "retrying in 0s (1/3): ghcr.io/seanbao/mnemonas:1.2.3"
	assert_file_contains "$out" "retrying in 0s (2/3): ghcr.io/seanbao/mnemonas:1.2.3"
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
		MNEMONAS_RELEASE_IMAGE_CHECK_RETRIES=1 \
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

run_remote_image_check_invalid_retry_config_fails() {
	local case_dir="$TMP_ROOT/image-check-invalid-retry"
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
		MNEMONAS_RELEASE_IMAGE_CHECK_RETRIES=0 \
		bash "$REPO_ROOT/scripts/verify-release-artifacts.sh" \
			--version v1.2.3 \
			--repository seanbao/mnemonas \
			--check-image \
			"$dist_dir" >"$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release artifact verifier accepted invalid image-check retry count"
	assert_file_contains "$out" "MNEMONAS_RELEASE_IMAGE_CHECK_RETRIES must be a positive integer"
}

run_complete_release_passes
run_dash_prefixed_artifact_directory_passes
run_binary_checksum_marker_passes
run_missing_target_fails_in_strict_mode
run_invalid_repository_fails_before_artifact_checks
run_invalid_version_argument_fails_before_artifact_checks
run_archive_version_inference_rejects_invalid_release_version
run_checksum_mismatch_fails
run_checksum_path_escape_fails_before_checksum
run_checksum_control_character_path_fails_before_checksum
run_checksum_whitespace_path_fails_before_checksum
run_archive_whitespace_filename_fails_before_checksum
run_archive_symlink_fails_before_checksum
run_unexpected_artifact_entry_fails_before_checksum
run_unexpected_artifact_control_character_entry_escapes_diagnostic
run_archive_entry_symlink_fails
run_archive_entry_hardlink_fails
run_archive_duplicate_entry_fails
run_archive_dot_segment_entry_fails
run_archive_backslash_entry_fails
run_archive_control_character_entry_fails
run_archive_whitespace_entry_fails
run_wrong_env_image_fails
run_remote_image_check_uses_docker_manifest
run_remote_image_check_retries_transient_manifest_failure
run_remote_image_check_failure_fails
run_remote_image_check_invalid_retry_config_fails

printf '[release-artifact-verify-test] all checks passed\n'
