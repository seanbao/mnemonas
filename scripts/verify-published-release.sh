#!/usr/bin/env bash

set -euo pipefail

SCRIPT_NAME="$(basename -- "$0")"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSION=""
REPOSITORY="seanbao/mnemonas"
ARTIFACT_DIR=""
DOWNLOAD_DIR=""
CHECK_IMAGE=1
KEEP_ARTIFACTS=0
TMP_ROOT=""
RETAINED_ARTIFACTS_REPORTED=0

cleanup() {
	local status=$?

	if [[ "$KEEP_ARTIFACTS" == "1" ]]; then
		report_retained_artifacts
		return "$status"
	fi
	if [[ -n "$TMP_ROOT" && -d "$TMP_ROOT" && "$KEEP_ARTIFACTS" == "0" ]]; then
		rm -rf -- "$TMP_ROOT"
	fi
	return "$status"
}

trap cleanup EXIT

fail() {
	printf '[published-release-verify] ERROR: %s\n' "$*" >&2
	exit 1
}

report_retained_artifacts() {
	if [[ "$RETAINED_ARTIFACTS_REPORTED" == "1" ]]; then
		return
	fi
	if [[ -n "$TMP_ROOT" && -n "$DOWNLOAD_DIR" && -d "$DOWNLOAD_DIR" ]]; then
		printf '[published-release-verify] retained artifacts at %s\n' "$DOWNLOAD_DIR"
		RETAINED_ARTIFACTS_REPORTED=1
	fi
}

# shellcheck source=scripts/release-version.sh
. "$SCRIPT_DIR/release-version.sh"

usage() {
	cat <<EOF
Usage: $SCRIPT_NAME --version VERSION [options]

Download and verify a published MnemoNAS GitHub Release.

Options:
  --version VERSION       Release tag to download, for example v1.2.3.
  --repository OWNER/REPO GitHub repository and GHCR image owner/name.
                          Defaults to seanbao/mnemonas.
  --artifact-dir DIR      Download artifacts into DIR. DIR must be empty or
                          absent. Defaults to a temporary directory.
                          Dash-prefixed relative paths are supported.
  --skip-image-check      Verify archives and checksums without checking GHCR.
  --keep-artifacts        Keep the temporary download directory after exit.
  -h, --help              Show this help.

Environment:
  MNEMONAS_RELEASE_IMAGE_CHECK_RETRIES and
  MNEMONAS_RELEASE_IMAGE_CHECK_SLEEP_SECONDS are passed to the artifact
  verifier when image checks are enabled.
EOF
}

need_tool() {
	local tool="$1"
	command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
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

prepare_download_dir() {
	local artifact_dir_for_ops

	if [[ -z "$ARTIFACT_DIR" ]]; then
		TMP_ROOT="$(mktemp -d)"
		DOWNLOAD_DIR="$TMP_ROOT/artifacts"
		mkdir -p -- "$DOWNLOAD_DIR"
		return
	fi

	if contains_control_character "$ARTIFACT_DIR"; then
		fail "artifact directory must not contain control characters: $(format_log_value "$ARTIFACT_DIR")"
	fi
	artifact_dir_for_ops="$ARTIFACT_DIR"
	case "$artifact_dir_for_ops" in
		-*) artifact_dir_for_ops="./$artifact_dir_for_ops" ;;
	esac
	if [[ -e "$artifact_dir_for_ops" && ! -d "$artifact_dir_for_ops" ]]; then
		fail "artifact directory exists but is not a directory: $ARTIFACT_DIR"
	fi
	mkdir -p -- "$artifact_dir_for_ops"
	if [[ -n "$(find "$artifact_dir_for_ops" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
		fail "artifact directory must be empty before download: $ARTIFACT_DIR"
	fi
	DOWNLOAD_DIR="$artifact_dir_for_ops"
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
		--artifact-dir)
			[[ "$#" -ge 2 ]] || fail "--artifact-dir requires a value"
			ARTIFACT_DIR="$2"
			shift 2
			;;
		--skip-image-check)
			CHECK_IMAGE=0
			shift
			;;
		--keep-artifacts)
			KEEP_ARTIFACTS=1
			shift
			;;
		-h|--help)
			usage
			exit 0
			;;
		-*)
			fail "unknown option: $1"
			;;
		*)
			fail "unexpected argument: $1"
			;;
	esac
done

[[ -n "$VERSION" ]] || { usage >&2; exit 2; }
validate_docker_release_version "$VERSION" "release version" "release version must not be empty" 1
validate_repository "$REPOSITORY"

need_tool gh
prepare_download_dir

printf '[published-release-verify] downloading %s from %s into %s\n' "$VERSION" "$REPOSITORY" "$DOWNLOAD_DIR"
gh release download "$VERSION" --repo "$REPOSITORY" --dir "$DOWNLOAD_DIR"

verifier_args=(
	--version "$VERSION"
	--repository "$REPOSITORY"
	--require-targets
)
if [[ "$CHECK_IMAGE" == "1" ]]; then
	verifier_args+=(--check-image)
fi

bash "$SCRIPT_DIR/verify-release-artifacts.sh" "${verifier_args[@]}" -- "$DOWNLOAD_DIR"

printf '[published-release-verify] verified published release %s from %s\n' "$VERSION" "$REPOSITORY"
if [[ "$KEEP_ARTIFACTS" == "1" ]]; then
	report_retained_artifacts
fi
