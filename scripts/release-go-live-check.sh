#!/usr/bin/env bash
set -euo pipefail

SCRIPT_NAME="$(basename -- "$0")"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

VERSION=""
DOMAIN=""
REPOSITORY="seanbao/mnemonas"
ARTIFACT_DIR=""
KEEP_PUBLISHED_ARTIFACTS=0
BACKUP_API_URL=""
BACKUP_JOB_ID=""
COOKIE_FILE=""
KEEP_BACKUP_ARTIFACT=0
CURL_INSECURE_VALUE=0
SKIP_BACKUP_RESTORE_DRILL=0

RELEASE_READINESS_BIN="${MNEMONAS_RELEASE_READINESS_BIN:-$SCRIPT_DIR/release-readiness.sh}"
VERIFY_PUBLISHED_RELEASE_BIN="${MNEMONAS_VERIFY_PUBLISHED_RELEASE_BIN:-$SCRIPT_DIR/verify-published-release.sh}"
DOCTOR_BIN="${MNEMONAS_DOCTOR_BIN:-$SCRIPT_DIR/mnemonas-doctor.sh}"
PUBLIC_GO_LIVE_SMOKE_BIN="${MNEMONAS_PUBLIC_GO_LIVE_SMOKE_BIN:-$SCRIPT_DIR/public-go-live-smoke.sh}"
BACKUP_RESTORE_DRILL_SMOKE_BIN="${MNEMONAS_BACKUP_RESTORE_DRILL_SMOKE_BIN:-$SCRIPT_DIR/backup-restore-drill-smoke.sh}"

fail() {
	printf '[release-go-live-check] ERROR: %s\n' "$*" >&2
	exit 1
}

# shellcheck source=scripts/release-version.sh
. "$SCRIPT_DIR/release-version.sh"

log_info() {
	printf '[release-go-live-check] %s\n' "$*"
}

log_ok() {
	printf '[release-go-live-check] OK: %s\n' "$*"
}

usage() {
	cat <<EOF
Usage:
  $SCRIPT_NAME --version <tag> --domain <domain> [options]

Runs the maintainer post-publication go-live checks in order:
  1. release-readiness summary
  2. GitHub Release and GHCR artifact verification
  3. mnemonas-doctor public-domain checks
  4. external public go-live smoke
  5. backup restore-drill smoke, unless explicitly skipped

Options:
  --version TAG                 Required release tag, for example v1.2.3.
  --domain DOMAIN               Required public domain, for example nas.example.com.
  --repository OWNER/REPO       GitHub repository and GHCR image owner/name.
                                Defaults to seanbao/mnemonas.
  --artifact-dir DIR            Optional directory passed to verify-published-release.
  --keep-published-artifacts    Retain verify-published-release temporary downloads.
  --backup-api-url URL          API root URL for backup restore-drill smoke.
  --backup-job-id ID            Backup job ID for backup restore-drill smoke.
  --cookie-file FILE            Optional curl cookie file for authenticated backup smoke.
  --keep-backup-artifact        Keep the restore-drill artifact during backup smoke.
  --curl-insecure               Pass CURL_INSECURE=1 to backup restore-drill smoke.
  --skip-backup-restore-drill   Explicitly skip backup restore-drill smoke.
  -h, --help                    Show this help.

Environment:
  MNEMONAS_RELEASE_READINESS_BIN, MNEMONAS_VERIFY_PUBLISHED_RELEASE_BIN,
  MNEMONAS_DOCTOR_BIN, MNEMONAS_PUBLIC_GO_LIVE_SMOKE_BIN, and
  MNEMONAS_BACKUP_RESTORE_DRILL_SMOKE_BIN can override helper paths.
EOF
}

need_executable() {
	local label="$1"
	local command_path="$2"

	command -v "$command_path" >/dev/null 2>&1 || fail "$label is required: $command_path"
}

contains_control_character() {
	local value="$1"

	LC_ALL=C printf '%s' "$value" | LC_ALL=C grep -q '[[:cntrl:]]'
}

contains_whitespace_character() {
	local value="$1"

	[[ "$value" == *[[:space:]]* ]]
}

is_ipv4_like_host() {
	local host="$1"
	local octet
	local -a octets

	IFS='.' read -r -a octets <<< "$host"
	[[ "${#octets[@]}" -eq 4 ]] || return 1
	for octet in "${octets[@]}"; do
		[[ "$octet" =~ ^[0-9]+$ ]] || return 1
	done
	return 0
}

is_valid_dns_hostname() {
	local host="$1"
	local label
	local -a labels

	[[ -n "$host" && "${#host}" -le 253 ]] || return 1
	[[ "$host" =~ ^[a-z0-9.-]+$ ]] || return 1
	[[ "$host" != *".."* ]] || return 1

	IFS='.' read -r -a labels <<< "$host"
	for label in "${labels[@]}"; do
		[[ -n "$label" && "${#label}" -le 63 ]] || return 1
		[[ "$label" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || return 1
	done
	return 0
}

validate_backup_api_url_path() {
	local value="$1"
	local after_authority path lower_path segment normalized_segment
	local -a segments

	[[ "$value" != *\\* ]] || fail "backup API URL must not contain backslashes"

	after_authority="${value#*://}"
	if [[ "$after_authority" == */* ]]; then
		path="/${after_authority#*/}"
	else
		path="/"
	fi

	lower_path="${path,,}"
	[[ "$lower_path" != *"%2f"* && "$lower_path" != *"%5c"* ]] || fail "backup API URL must not contain encoded slashes or backslashes"
	if [[ "$path" != "/" ]]; then
		[[ "$path" != *"//"* ]] || fail "backup API URL must not contain empty path segments"
	fi

	IFS='/' read -r -a segments <<< "$lower_path"
	for segment in "${segments[@]}"; do
		[[ -n "$segment" ]] || continue
		normalized_segment="${segment//%2e/.}"
		[[ "$normalized_segment" != "." && "$normalized_segment" != ".." ]] || fail "backup API URL must not contain dot segments"
	done
}

normalize_domain() {
	local value="$1"

	value="${value,,}"
	value="${value%.}"
	printf '%s\n' "$value"
}

validate_domain() {
	local value="$1"

	[[ -n "$value" ]] || fail "public domain is required"
	[[ "$value" != *[[:cntrl:][:space:]]* ]] || fail "public domain must not contain whitespace or control characters"
	[[ "$value" != http://* && "$value" != https://* ]] || fail "public domain must not include a URL scheme"
	[[ "$value" != *"/"* && "$value" != *"?"* && "$value" != *"#"* && "$value" != *"@"* ]] || fail "public domain must not include a path, query, fragment, or userinfo"
	[[ "$value" != *":"* ]] || fail "public domain must not include a port"
	[[ "$value" != *. ]] || fail "public domain must be a valid ASCII hostname"
	is_valid_dns_hostname "$value" || fail "public domain must be a valid ASCII hostname"
	[[ "$value" == *.* ]] || fail "public domain must be a fully qualified hostname"
	[[ "$value" != "localhost" && "$value" != *.localhost ]] || fail "public domain must not be localhost"
	! is_ipv4_like_host "$value" || fail "public domain must be a hostname, not an IP address"
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

validate_backup_smoke_args() {
	[[ -n "$BACKUP_API_URL" && -n "$BACKUP_JOB_ID" ]] || fail "--backup-api-url and --backup-job-id are required, or pass --skip-backup-restore-drill explicitly"

	[[ "$BACKUP_API_URL" == http://* || "$BACKUP_API_URL" == https://* ]] || fail "backup API URL must start with http:// or https://"
	[[ "$BACKUP_API_URL" != *[[:space:]]* ]] || fail "backup API URL must not contain whitespace"
	[[ "$BACKUP_API_URL" != *[[:cntrl:]]* ]] || fail "backup API URL must not contain control characters"
	[[ "$BACKUP_API_URL" != *\?* && "$BACKUP_API_URL" != *#* ]] || fail "backup API URL must not contain query strings or fragments"
	[[ "$BACKUP_API_URL" != *"@"* ]] || fail "backup API URL must not contain embedded credentials"
	validate_backup_api_url_path "$BACKUP_API_URL"

	[[ "${#BACKUP_JOB_ID}" -le 64 ]] || fail "backup job ID must be 64 characters or fewer"
	[[ "$BACKUP_JOB_ID" =~ ^[A-Za-z0-9._-]+$ ]] || fail "backup job ID must be a safe backup job ID"
	[[ "$BACKUP_JOB_ID" != "." && "$BACKUP_JOB_ID" != ".." ]] || fail "backup job ID must not be . or .."

	if [[ -n "$COOKIE_FILE" ]]; then
		[[ "$COOKIE_FILE" != *[[:cntrl:]]* ]] || fail "backup cookie file must not contain control characters"
		[[ -f "$COOKIE_FILE" && -r "$COOKIE_FILE" ]] || fail "backup cookie file must be a readable regular file"
	fi
}

validate_args() {
	[[ -n "$VERSION" ]] || fail "--version is required"
	[[ -n "$DOMAIN" ]] || fail "--domain is required"
	[[ -n "$REPOSITORY" ]] || fail "--repository must not be empty"
	validate_docker_release_version "$VERSION" "release version" "release version must not be empty" 1
	validate_repository "$REPOSITORY"
	DOMAIN="$(normalize_domain "$DOMAIN")"
	validate_domain "$DOMAIN"
	[[ -z "$ARTIFACT_DIR" || "$KEEP_PUBLISHED_ARTIFACTS" == "0" ]] || fail "--keep-published-artifacts cannot be combined with --artifact-dir; explicit artifact directories are already retained"

	if [[ "$SKIP_BACKUP_RESTORE_DRILL" == "1" ]]; then
		[[ -z "$BACKUP_API_URL" && -z "$BACKUP_JOB_ID" && -z "$COOKIE_FILE" ]] || fail "backup smoke options cannot be combined with --skip-backup-restore-drill"
		[[ "$KEEP_BACKUP_ARTIFACT" == "0" && "$CURL_INSECURE_VALUE" == "0" ]] || fail "backup smoke flags cannot be combined with --skip-backup-restore-drill"
	else
		validate_backup_smoke_args
	fi

	need_executable "release-readiness" "$RELEASE_READINESS_BIN"
	need_executable "published release verifier" "$VERIFY_PUBLISHED_RELEASE_BIN"
	need_executable "mnemonas doctor" "$DOCTOR_BIN"
	need_executable "public go-live smoke" "$PUBLIC_GO_LIVE_SMOKE_BIN"
	if [[ "$SKIP_BACKUP_RESTORE_DRILL" == "0" ]]; then
		need_executable "backup restore-drill smoke" "$BACKUP_RESTORE_DRILL_SMOKE_BIN"
	fi
}

run_release_readiness() {
	log_info "running release readiness summary"
	"$RELEASE_READINESS_BIN"
	log_ok "release readiness summary passed"
}

run_published_release_verifier() {
	local -a args

	args=(
		--version "$VERSION"
		--repository "$REPOSITORY"
	)
	if [[ -n "$ARTIFACT_DIR" ]]; then
		args+=(--artifact-dir "$ARTIFACT_DIR")
	fi
	if [[ "$KEEP_PUBLISHED_ARTIFACTS" == "1" ]]; then
		args+=(--keep-artifacts)
	fi

	log_info "verifying published release $VERSION from $REPOSITORY"
	"$VERIFY_PUBLISHED_RELEASE_BIN" "${args[@]}"
	log_ok "published release verification passed"
}

run_public_doctor() {
	log_info "running public-domain doctor for $DOMAIN"
	"$DOCTOR_BIN" --public-domain "$DOMAIN"
	log_ok "public-domain doctor passed"
}

run_public_smoke() {
	log_info "running external public go-live smoke for $DOMAIN"
	"$PUBLIC_GO_LIVE_SMOKE_BIN" "$DOMAIN"
	log_ok "external public go-live smoke passed"
}

run_backup_restore_drill() {
	local -a env_args

	if [[ "$SKIP_BACKUP_RESTORE_DRILL" == "1" ]]; then
		log_info "skipped backup restore-drill smoke by request"
		return
	fi

	env_args=(
		"MNEMONAS_API_URL=$BACKUP_API_URL"
		"MNEMONAS_BACKUP_JOB_ID=$BACKUP_JOB_ID"
		"MNEMONAS_BACKUP_KEEP_ARTIFACT=$KEEP_BACKUP_ARTIFACT"
		"CURL_INSECURE=$CURL_INSECURE_VALUE"
	)
	if [[ -n "$COOKIE_FILE" ]]; then
		env_args+=("MNEMONAS_COOKIE_FILE=$COOKIE_FILE")
	fi

	log_info "running backup restore-drill smoke for job $BACKUP_JOB_ID"
	env "${env_args[@]}" "$BACKUP_RESTORE_DRILL_SMOKE_BIN"
	log_ok "backup restore-drill smoke passed"
}

while [[ "$#" -gt 0 ]]; do
	case "$1" in
		--version)
			[[ "$#" -ge 2 ]] || fail "--version requires a value"
			VERSION="$2"
			shift 2
			;;
		--domain)
			[[ "$#" -ge 2 ]] || fail "--domain requires a value"
			DOMAIN="$2"
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
		--keep-published-artifacts)
			KEEP_PUBLISHED_ARTIFACTS=1
			shift
			;;
		--backup-api-url)
			[[ "$#" -ge 2 ]] || fail "--backup-api-url requires a value"
			BACKUP_API_URL="$2"
			shift 2
			;;
		--backup-job-id)
			[[ "$#" -ge 2 ]] || fail "--backup-job-id requires a value"
			BACKUP_JOB_ID="$2"
			shift 2
			;;
		--cookie-file)
			[[ "$#" -ge 2 ]] || fail "--cookie-file requires a value"
			COOKIE_FILE="$2"
			shift 2
			;;
		--keep-backup-artifact)
			KEEP_BACKUP_ARTIFACT=1
			shift
			;;
		--curl-insecure)
			CURL_INSECURE_VALUE=1
			shift
			;;
		--skip-backup-restore-drill)
			SKIP_BACKUP_RESTORE_DRILL=1
			shift
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			fail "unknown argument: $1"
			;;
	esac
done

validate_args
run_release_readiness
run_published_release_verifier
run_public_doctor
run_public_smoke
run_backup_restore_drill
log_ok "release go-live checks passed for $VERSION on $DOMAIN"
