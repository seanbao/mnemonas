#!/usr/bin/env bash

set -euo pipefail

SCRIPT_NAME="$(basename -- "$0")"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTIFACT_DIR=""
VERSION=""
REPOSITORY="seanbao/mnemonas"
CHECK_IMAGE=0
REQUIRE_TARGETS=0
TMP_ROOT=""
EXPECTED_TARGETS=(linux-amd64 linux-arm64 darwin-amd64 darwin-arm64)
CHECKSUM_ARCHIVES=""
IMAGE_CHECK_RETRIES="${MNEMONAS_RELEASE_IMAGE_CHECK_RETRIES:-6}"
IMAGE_CHECK_SLEEP_SECONDS="${MNEMONAS_RELEASE_IMAGE_CHECK_SLEEP_SECONDS:-5}"

cleanup() {
	if [[ -n "$TMP_ROOT" && -d "$TMP_ROOT" ]]; then
		rm -rf -- "$TMP_ROOT"
	fi
}

trap cleanup EXIT

fail() {
	printf '[release-artifact-verify] ERROR: %s\n' "$*" >&2
	exit 1
}

# shellcheck source=scripts/release-version.sh
. "$SCRIPT_DIR/release-version.sh"

usage() {
	cat <<EOF
Usage: $SCRIPT_NAME [options] <artifact-dir>

Verify downloaded MnemoNAS release archives and checksums.

Options:
  --version VERSION       Expected release tag, for example v1.2.3.
  --repository OWNER/REPO Expected GitHub repository for GHCR image tags.
                          Defaults to seanbao/mnemonas.
  --require-targets      Require linux-amd64, linux-arm64, darwin-amd64,
                          and darwin-arm64 archives.
  --check-image          Verify the matching GHCR image tag with Docker.

Environment:
  MNEMONAS_RELEASE_IMAGE_CHECK_RETRIES        Image manifest check attempts. Defaults to 6.
  MNEMONAS_RELEASE_IMAGE_CHECK_SLEEP_SECONDS  Seconds between image manifest check attempts. Defaults to 5.
  -h, --help             Show this help.
EOF
}

need_tool() {
	local tool="$1"
	command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
}

require_positive_integer() {
	local label="$1"
	local value="$2"

	[[ "$value" =~ ^[0-9]+$ ]] || fail "$label must be a positive integer: $value"
	(( value > 0 )) || fail "$label must be a positive integer: $value"
}

require_non_negative_integer() {
	local label="$1"
	local value="$2"

	[[ "$value" =~ ^[0-9]+$ ]] || fail "$label must be a non-negative integer: $value"
}

assert_file_contains() {
	local path="$1"
	local expected="$2"
	grep -Fq -- "$expected" "$path" || fail "$path does not contain: $expected"
}

assert_regular_file() {
	local path="$1"
	[[ -f "$path" && ! -L "$path" ]] || fail "expected regular file: $path"
}

assert_executable() {
	local path="$1"
	[[ -f "$path" && -x "$path" && ! -L "$path" ]] || fail "expected executable file: $path"
}

assert_manifest_contains() {
	local manifest="$1"
	local expected="$2"
	grep -Fxq -- "$expected" "$manifest" || fail "archive manifest is missing: $expected"
}

contains_control_character() {
	local value="$1"

	LC_ALL=C printf '%s' "$value" | LC_ALL=C grep -q '[[:cntrl:]]'
}

contains_whitespace_character() {
	local value="$1"

	[[ "$value" == *[[:space:]]* ]]
}

format_log_value() {
	local value="$1"
	local quoted

	if contains_control_character "$value"; then
		printf -v quoted '%q' "$value"
		printf '%s\n' "$quoted"
		return
	fi
	printf '%s\n' "$value"
}

validate_repository_owner() {
	local value="$1"

	[[ -n "$value" ]] || fail "repository owner must not be empty"
	[[ "$value" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || fail "repository owner must use lowercase letters, digits, or hyphens, and must start and end with a letter or digit"
}

validate_repository_name() {
	local value="$1"

	[[ -n "$value" ]] || fail "repository name must not be empty"
	[[ "$value" =~ ^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$ ]] || fail "repository name must use lowercase letters, digits, dots, underscores, or hyphens, and must start and end with a letter or digit"
}

validate_repository() {
	local value="$1"
	local owner
	local repo

	[[ -n "$value" ]] || fail "repository must be in OWNER/REPO form"
	if contains_control_character "$value" || contains_whitespace_character "$value"; then
		fail "repository must not contain whitespace or control characters"
	fi
	[[ "$value" == "${value,,}" ]] || fail "repository must be lowercase OWNER/REPO for GHCR image tags"
	[[ "$value" == */* && "$value" != */*/* ]] || fail "repository must be in OWNER/REPO form"

	owner="${value%%/*}"
	repo="${value#*/}"
	validate_repository_owner "$owner"
	validate_repository_name "$repo"
}

validate_release_version() {
	validate_docker_release_version "$1" "release version" "release version must not be empty" 1
}

tar_is_gnu() {
	tar --version 2>/dev/null | grep -qi 'gnu tar'
}

tar_list_archive() {
	local archive="$1"

	if tar_is_gnu; then
		tar --quoting-style=literal -tzf "$archive"
	else
		tar -tzf "$archive"
	fi
}

tar_list_archive_types() {
	local archive="$1"

	if tar_is_gnu; then
		tar --quoting-style=literal -tvzf "$archive"
	else
		tar -tvzf "$archive"
	fi
}

archive_target() {
	local archive="$1"
	local name
	local target

	name="$(basename -- "$archive")"
	name="${name%.tar.gz}"

	for target in "${EXPECTED_TARGETS[@]}"; do
		case "$name" in
			mnemonas-*-"$target")
				printf '%s\n' "$target"
				return 0
				;;
		esac
	done

	fail "archive name does not end with a supported target: $(basename -- "$archive")"
}

archive_version() {
	local archive="$1"
	local name
	local target
	local prefix

	name="$(basename -- "$archive")"
	name="${name%.tar.gz}"
	target="$(archive_target "$archive")"
	prefix="${name%-"${target}"}"
	printf '%s\n' "${prefix#mnemonas-}"
}

validate_checksums_manifest() {
	local path="$1"
	local line
	local line_number=0
	local hash
	local rest
	local filename
	local normalized

	CHECKSUM_ARCHIVES=""
	while IFS= read -r line || [[ -n "$line" ]]; do
		line_number=$((line_number + 1))
		[[ -n "$line" ]] || fail "checksums.txt contains an empty line"
		[[ "$line" != \\* ]] || fail "checksums.txt contains unsupported escaped filename syntax on line $line_number"
		hash="${line%% *}"
		[[ "$hash" != "$line" ]] || fail "checksums.txt line $line_number is malformed"
		[[ "$hash" =~ ^[[:xdigit:]]{64}$ ]] || fail "checksums.txt line $line_number has an invalid SHA-256 digest"
		rest="${line#"$hash"}"
		case "$rest" in
			"  "*|" *"*)
				filename="${rest:2}"
				;;
			*)
				fail "checksums.txt line $line_number is malformed"
				;;
		esac
		[[ -n "$filename" ]] || fail "checksums.txt line $line_number has an empty filename"
		if contains_control_character "$filename"; then
			fail "checksums.txt contains a control character in file path: $(format_log_value "$filename")"
		fi
		if contains_whitespace_character "$filename"; then
			fail "checksums.txt contains whitespace in file path: $filename"
		fi
		normalized="${filename#./}"
		case "$normalized" in
			mnemonas-*.tar.gz)
				;;
			*)
				fail "checksums.txt lists unsupported file: $filename"
				;;
		esac
		case "$normalized" in
			/*|*/*|.|..|-*)
				fail "checksums.txt contains an unsafe file path: $filename"
				;;
		esac
		case " $CHECKSUM_ARCHIVES " in
			*" $normalized "*)
				fail "checksums.txt lists duplicate archive: $normalized"
				;;
		esac
		CHECKSUM_ARCHIVES+=" $normalized"
	done <"$path"

	[[ -n "$CHECKSUM_ARCHIVES" ]] || fail "checksums.txt does not list any release archives"
}

checksum_manifest_has_archive() {
	local archive_name="$1"

	case " $CHECKSUM_ARCHIVES " in
		*" $archive_name "*)
			return 0
			;;
	esac
	return 1
}

validate_artifact_directory_entries() {
	local entry
	local base

	shopt -s nullglob dotglob
	for entry in "$ARTIFACT_DIR"/*; do
		base="$(basename -- "$entry")"
		case "$base" in
			checksums.txt|mnemonas-*.tar.gz)
				;;
			*)
				fail "artifact directory contains an unsupported entry: $(format_log_value "$base")"
				;;
		esac
	done
	shopt -u nullglob dotglob
}

format_seen_targets() {
	local seen="$1"
	local target
	local formatted=""

	for target in "${EXPECTED_TARGETS[@]}"; do
		case " $seen " in
			*" $target "*)
				if [[ -n "$formatted" ]]; then
					formatted+=" "
				fi
				formatted+="$target"
				;;
		esac
	done

	printf '%s\n' "$formatted"
}

validate_manifest_paths() {
	local manifest="$1"
	local expected_top="$2"
	local top=""
	local path
	local root
	declare -A seen_paths=()

	while IFS= read -r path; do
		[[ -n "$path" ]] || continue
		if [[ -n "${seen_paths[$path]+x}" ]]; then
			fail "archive contains duplicate entry: $(format_log_value "$path")"
		fi
		seen_paths["$path"]=1
		case "$path" in
			/*|../*|*/../*|*/..|..)
				fail "archive entry has an unsafe path: $(format_log_value "$path")"
				;;
		esac
		case "$path" in
			*\\*)
				fail "archive entry contains a backslash: $(format_log_value "$path")"
				;;
		esac
		if contains_control_character "$path"; then
			fail "archive entry contains a control character: $(format_log_value "$path")"
		fi
		if contains_whitespace_character "$path"; then
			fail "archive entry contains whitespace: $(format_log_value "$path")"
		fi
		case "$path" in
			*//*|./*|*/./*|*/.|.)
				fail "archive entry has an unsafe path segment: $(format_log_value "$path")"
				;;
		esac
		root="${path%%/*}"
		[[ -n "$root" ]] || fail "archive entry has an empty top-level path: $(format_log_value "$path")"
		if [[ -z "$top" ]]; then
			top="$root"
		fi
		[[ "$root" == "$top" ]] || fail "archive contains multiple top-level directories: $(format_log_value "$top") and $(format_log_value "$root")"
	done <"$manifest"

	[[ "$top" == "$expected_top" ]] || fail "archive top-level directory is $top, expected $expected_top"
}

validate_archive_entry_types() {
	local listing="$1"
	local line
	local entry_type

	while IFS= read -r line; do
		[[ -n "$line" ]] || continue
		entry_type="${line:0:1}"
		case "$entry_type" in
			-|d)
				;;
			*)
				fail "archive contains unsupported entry type '$entry_type': $line"
				;;
		esac
	done <"$listing"
}

validate_archive() {
	local archive="$1"
	local expected_version="$2"
	local repository="$3"
	local base
	local expected_top
	local manifest
	local type_listing
	local extract_parent
	local extracted
	local expected_image

	base="$(basename -- "$archive")"
	expected_top="${base%.tar.gz}"
	manifest="$TMP_ROOT/${expected_top}.manifest"
	type_listing="$TMP_ROOT/${expected_top}.types"
	extract_parent="$TMP_ROOT/extract-${expected_top}"
	extracted="$extract_parent/$expected_top"
	expected_image="MNEMONAS_IMAGE=ghcr.io/${repository}:${expected_version#v}"

	tar_list_archive "$archive" >"$manifest" || fail "cannot list archive: $base"
	validate_manifest_paths "$manifest" "$expected_top"
	tar_list_archive_types "$archive" >"$type_listing" || fail "cannot inspect archive entry types: $base"
	validate_archive_entry_types "$type_listing"

	assert_manifest_contains "$manifest" "$expected_top/nasd"
	assert_manifest_contains "$manifest" "$expected_top/dataplane"
	assert_manifest_contains "$manifest" "$expected_top/web/index.html"
	assert_manifest_contains "$manifest" "$expected_top/scripts/install-systemd.sh"
	assert_manifest_contains "$manifest" "$expected_top/scripts/docker-quickstart.sh"
	assert_manifest_contains "$manifest" "$expected_top/scripts/mnemonas-doctor.sh"
	assert_manifest_contains "$manifest" "$expected_top/scripts/verify-release-artifacts.sh"
	assert_manifest_contains "$manifest" "$expected_top/deploy/public-access/README.md"
	assert_manifest_contains "$manifest" "$expected_top/deploy/public-access/traefik/docker-compose.yml"
	assert_manifest_contains "$manifest" "$expected_top/docs/README.md"
	assert_manifest_contains "$manifest" "$expected_top/docs/README.en.md"
	assert_manifest_contains "$manifest" "$expected_top/docker-compose.yml"
	assert_manifest_contains "$manifest" "$expected_top/.env.example"
	assert_manifest_contains "$manifest" "$expected_top/mnemonas.example.toml"
	assert_manifest_contains "$manifest" "$expected_top/README.md"
	assert_manifest_contains "$manifest" "$expected_top/README.en.md"
	assert_manifest_contains "$manifest" "$expected_top/CHANGELOG.md"
	assert_manifest_contains "$manifest" "$expected_top/CHANGELOG.en.md"
	assert_manifest_contains "$manifest" "$expected_top/SECURITY.md"
	assert_manifest_contains "$manifest" "$expected_top/SECURITY.zh-CN.md"
	assert_manifest_contains "$manifest" "$expected_top/SUPPORT.md"
	assert_manifest_contains "$manifest" "$expected_top/SUPPORT.en.md"
	assert_manifest_contains "$manifest" "$expected_top/LICENSE"

	mkdir -p "$extract_parent"
	tar -xzf "$archive" -C "$extract_parent" || fail "cannot extract archive: $base"
	assert_executable "$extracted/nasd"
	assert_executable "$extracted/dataplane"
	assert_executable "$extracted/scripts/install-systemd.sh"
	assert_executable "$extracted/scripts/docker-quickstart.sh"
	assert_executable "$extracted/scripts/verify-release-artifacts.sh"
	assert_regular_file "$extracted/web/index.html"
	assert_regular_file "$extracted/.env.example"
	assert_file_contains "$extracted/.env.example" "$expected_image"
	assert_file_contains "$extracted/.env.example" "MNEMONAS_VERSION=$expected_version"
	if grep -Fq -- '.env.example.tmp' "$manifest"; then
		fail "archive includes temporary .env.example rewrite file: $base"
	fi
}

check_remote_image() {
	local version="$1"
	local repository="$2"
	local image="ghcr.io/${repository}:${version#v}"
	local attempt

	need_tool docker
	require_positive_integer "MNEMONAS_RELEASE_IMAGE_CHECK_RETRIES" "$IMAGE_CHECK_RETRIES"
	require_non_negative_integer "MNEMONAS_RELEASE_IMAGE_CHECK_SLEEP_SECONDS" "$IMAGE_CHECK_SLEEP_SECONDS"

	for ((attempt = 1; attempt <= IMAGE_CHECK_RETRIES; attempt++)); do
		if docker manifest inspect "$image" >/dev/null 2>&1; then
			printf '[release-artifact-verify] verified container image: %s\n' "$image"
			return 0
		fi
		if (( attempt < IMAGE_CHECK_RETRIES )); then
			printf '[release-artifact-verify] container image not available yet, retrying in %ss (%d/%d): %s\n' \
				"$IMAGE_CHECK_SLEEP_SECONDS" \
				"$attempt" \
				"$IMAGE_CHECK_RETRIES" \
				"$image" >&2
			sleep "$IMAGE_CHECK_SLEEP_SECONDS"
		fi
	done

	fail "container image tag is not available: $image"
}

while [[ "$#" -gt 0 ]]; do
	case "$1" in
		--version)
			[[ "$#" -ge 2 ]] || fail "--version requires a value"
			VERSION="$2"
			shift 2
			;;
		--repository)
			[[ "$#" -ge 2 ]] || fail "--repository requires a value"
			REPOSITORY="$2"
			shift 2
			;;
		--require-targets)
			REQUIRE_TARGETS=1
			shift
			;;
		--check-image)
			CHECK_IMAGE=1
			shift
			;;
		-h|--help)
			usage
			exit 0
			;;
		--)
			shift
			break
			;;
		-*)
			fail "unknown option: $1"
			;;
		*)
			[[ -z "$ARTIFACT_DIR" ]] || fail "unexpected argument: $1"
			ARTIFACT_DIR="$1"
			shift
			;;
	esac
done

if [[ "$#" -gt 0 ]]; then
	[[ -z "$ARTIFACT_DIR" ]] || fail "unexpected argument: $1"
	[[ "$#" -eq 1 ]] || fail "unexpected argument: $2"
	ARTIFACT_DIR="$1"
	shift
fi

[[ -n "$ARTIFACT_DIR" ]] || { usage >&2; exit 2; }
if [[ -n "$VERSION" ]]; then
	validate_release_version "$VERSION"
fi
[[ -d "$ARTIFACT_DIR" ]] || fail "artifact directory does not exist: $ARTIFACT_DIR"
validate_repository "$REPOSITORY"

need_tool sha256sum
need_tool tar

CHECKSUMS="$ARTIFACT_DIR/checksums.txt"
[[ -e "$CHECKSUMS" ]] || fail "missing checksums file: $CHECKSUMS"
assert_regular_file "$CHECKSUMS"
validate_artifact_directory_entries
validate_checksums_manifest "$CHECKSUMS"

shopt -s nullglob
archives=("$ARTIFACT_DIR"/mnemonas-*.tar.gz)
shopt -u nullglob
[[ "${#archives[@]}" -gt 0 ]] || fail "no MnemoNAS release archives found in $ARTIFACT_DIR"

for archive in "${archives[@]}"; do
	assert_regular_file "$archive"
	base="$(basename -- "$archive")"
	if contains_control_character "$base"; then
		fail "release archive filename contains a control character: $(format_log_value "$base")"
	fi
	if contains_whitespace_character "$base"; then
		fail "release archive filename contains whitespace: $base"
	fi
done

if ! (cd -- "$ARTIFACT_DIR" && sha256sum -c checksums.txt); then
	fail "sha256 checksum verification failed"
fi

TMP_ROOT="$(mktemp -d)"
seen_targets=""

for archive in "${archives[@]}"; do
	base="$(basename -- "$archive")"
	target="$(archive_target "$archive")"
	found_version="$(archive_version "$archive")"
	validate_release_version "$found_version"

	checksum_manifest_has_archive "$base" || fail "checksums.txt does not list $base"
	if [[ -n "$VERSION" ]]; then
		[[ "$found_version" == "$VERSION" ]] || fail "$base has version $found_version, expected $VERSION"
	else
		VERSION="$found_version"
	fi
	[[ "$found_version" == "$VERSION" ]] || fail "$base has version $found_version, expected $VERSION"
	case " $seen_targets " in
		*" $target "*)
			fail "duplicate release archive target: $target"
			;;
	esac
	seen_targets+=" $target"
	validate_archive "$archive" "$VERSION" "$REPOSITORY"
done

if [[ "$REQUIRE_TARGETS" == "1" ]]; then
	for target in "${EXPECTED_TARGETS[@]}"; do
		case " $seen_targets " in
			*" $target "*)
				;;
			*)
				fail "missing required release archive target: $target"
				;;
		esac
	done
fi

if [[ "$CHECK_IMAGE" == "1" ]]; then
	check_remote_image "$VERSION" "$REPOSITORY"
fi

printf '[release-artifact-verify] verified targets: %s\n' "$(format_seen_targets "$seen_targets")"
printf '[release-artifact-verify] verified %d archive(s) for %s\n' "${#archives[@]}" "$VERSION"
