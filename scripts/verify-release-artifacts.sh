#!/usr/bin/env bash

set -euo pipefail

SCRIPT_NAME="$(basename "$0")"
ARTIFACT_DIR=""
VERSION=""
REPOSITORY="seanbao/mnemonas"
CHECK_IMAGE=0
REQUIRE_TARGETS=0
TMP_ROOT=""
EXPECTED_TARGETS=(linux-amd64 linux-arm64 darwin-amd64 darwin-arm64)
CHECKSUM_ARCHIVES=""

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
  -h, --help             Show this help.
EOF
}

need_tool() {
	local tool="$1"
	command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
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

archive_target() {
	local archive="$1"
	local name
	local target

	name="$(basename "$archive")"
	name="${name%.tar.gz}"

	for target in "${EXPECTED_TARGETS[@]}"; do
		case "$name" in
			mnemonas-*-"$target")
				printf '%s\n' "$target"
				return 0
				;;
		esac
	done

	fail "archive name does not end with a supported target: $(basename "$archive")"
}

archive_version() {
	local archive="$1"
	local name
	local target
	local prefix

	name="$(basename "$archive")"
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

validate_manifest_paths() {
	local manifest="$1"
	local expected_top="$2"
	local top=""
	local path
	local root

	while IFS= read -r path; do
		[[ -n "$path" ]] || continue
		case "$path" in
			/*|../*|*/../*|*/..|..)
				fail "archive entry has an unsafe path: $path"
				;;
		esac
		root="${path%%/*}"
		[[ -n "$root" ]] || fail "archive entry has an empty top-level path: $path"
		if [[ -z "$top" ]]; then
			top="$root"
		fi
		[[ "$root" == "$top" ]] || fail "archive contains multiple top-level directories: $top and $root"
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

	base="$(basename "$archive")"
	expected_top="${base%.tar.gz}"
	manifest="$TMP_ROOT/${expected_top}.manifest"
	type_listing="$TMP_ROOT/${expected_top}.types"
	extract_parent="$TMP_ROOT/extract-${expected_top}"
	extracted="$extract_parent/$expected_top"
	expected_image="MNEMONAS_IMAGE=ghcr.io/${repository}:${expected_version#v}"

	tar -tzf "$archive" >"$manifest" || fail "cannot list archive: $base"
	validate_manifest_paths "$manifest" "$expected_top"
	tar -tvzf "$archive" >"$type_listing" || fail "cannot inspect archive entry types: $base"
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

	need_tool docker
	docker manifest inspect "$image" >/dev/null 2>&1 || fail "container image tag is not available: $image"
	printf '[release-artifact-verify] verified container image: %s\n' "$image"
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

[[ -n "$ARTIFACT_DIR" ]] || { usage >&2; exit 2; }
[[ -d "$ARTIFACT_DIR" ]] || fail "artifact directory does not exist: $ARTIFACT_DIR"
[[ "$REPOSITORY" == */* ]] || fail "repository must be in OWNER/REPO form"

need_tool sha256sum
need_tool tar

CHECKSUMS="$ARTIFACT_DIR/checksums.txt"
[[ -e "$CHECKSUMS" ]] || fail "missing checksums file: $CHECKSUMS"
assert_regular_file "$CHECKSUMS"
validate_checksums_manifest "$CHECKSUMS"

shopt -s nullglob
archives=("$ARTIFACT_DIR"/mnemonas-*.tar.gz)
shopt -u nullglob
[[ "${#archives[@]}" -gt 0 ]] || fail "no MnemoNAS release archives found in $ARTIFACT_DIR"

for archive in "${archives[@]}"; do
	assert_regular_file "$archive"
done

if ! (cd "$ARTIFACT_DIR" && sha256sum -c checksums.txt); then
	fail "sha256 checksum verification failed"
fi

TMP_ROOT="$(mktemp -d)"
seen_targets=""

for archive in "${archives[@]}"; do
	base="$(basename "$archive")"
	target="$(archive_target "$archive")"
	found_version="$(archive_version "$archive")"

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

printf '[release-artifact-verify] verified %d archive(s) for %s\n' "${#archives[@]}" "$VERSION"
