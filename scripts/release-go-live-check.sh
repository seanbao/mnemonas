#!/usr/bin/env bash
set -euo pipefail

SCRIPT_NAME="$(basename -- "$0")"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

VERSION=""
DOMAIN=""
REPOSITORY="seanbao/mnemonas"
ARTIFACT_DIR=""
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

validate_args() {
	[[ -n "$VERSION" ]] || fail "--version is required"
	[[ -n "$DOMAIN" ]] || fail "--domain is required"
	[[ -n "$REPOSITORY" ]] || fail "--repository must not be empty"

	if [[ "$SKIP_BACKUP_RESTORE_DRILL" == "1" ]]; then
		[[ -z "$BACKUP_API_URL" && -z "$BACKUP_JOB_ID" && -z "$COOKIE_FILE" ]] || fail "backup smoke options cannot be combined with --skip-backup-restore-drill"
		[[ "$KEEP_BACKUP_ARTIFACT" == "0" && "$CURL_INSECURE_VALUE" == "0" ]] || fail "backup smoke flags cannot be combined with --skip-backup-restore-drill"
	else
		[[ -n "$BACKUP_API_URL" && -n "$BACKUP_JOB_ID" ]] || fail "--backup-api-url and --backup-job-id are required, or pass --skip-backup-restore-drill explicitly"
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
