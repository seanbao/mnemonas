#!/usr/bin/env bash
set -euo pipefail

SCRIPT_NAME="$(basename "$0")"
CURL_BIN="${CURL_BIN:-curl}"
MNEMONAS_API_URL="${MNEMONAS_API_URL:-}"
MNEMONAS_BACKUP_JOB_ID="${MNEMONAS_BACKUP_JOB_ID:-}"
MNEMONAS_COOKIE_FILE="${MNEMONAS_COOKIE_FILE:-}"
MNEMONAS_BACKUP_KEEP_ARTIFACT="${MNEMONAS_BACKUP_KEEP_ARTIFACT:-0}"
CURL_INSECURE="${CURL_INSECURE:-0}"
CURL_CONNECT_TIMEOUT="${CURL_CONNECT_TIMEOUT:-10}"
CURL_MAX_TIME="${CURL_MAX_TIME:-600}"

api_url=""
tmp_dir=""
curl_common_args=()

log_info() {
    printf '[backup-restore-drill-smoke] %s\n' "$*"
}

log_ok() {
    printf '[backup-restore-drill-smoke] OK: %s\n' "$*"
}

fail() {
    printf '[backup-restore-drill-smoke] ERROR: %s\n' "$*" >&2
    exit 1
}

usage() {
    cat <<EOF
Usage:
  MNEMONAS_API_URL=http://127.0.0.1:8080/api/v1 \\
  MNEMONAS_BACKUP_JOB_ID=external-disk \\
  MNEMONAS_COOKIE_FILE=cookies.txt \\
  $SCRIPT_NAME

Environment:
  MNEMONAS_API_URL              Required MnemoNAS API root URL, for example http://127.0.0.1:8080/api/v1.
  MNEMONAS_BACKUP_JOB_ID        Required configured backup job ID to run.
  MNEMONAS_COOKIE_FILE          Optional readable curl cookie file for authenticated API requests.
  MNEMONAS_BACKUP_KEEP_ARTIFACT Optional 0/1 flag passed to restore-drill keep_artifact; default 0.
  CURL_BIN                      Optional curl binary path.
  CURL_INSECURE=1               Pass --insecure to curl for local TLS smoke tests.
  CURL_CONNECT_TIMEOUT          Optional curl connection timeout in seconds; default 10.
  CURL_MAX_TIME                 Optional curl per-request timeout in seconds; default 600.
EOF
}

cleanup() {
    if [[ -n "$tmp_dir" ]]; then
        rm -rf -- "$tmp_dir"
    fi
}

require_command() {
    if ! command -v "$CURL_BIN" >/dev/null 2>&1; then
        fail "curl is required; set CURL_BIN to a compatible curl binary"
    fi
}

validate_positive_seconds() {
    local name="$1"
    local value="$2"
    if [[ ! "$value" =~ ^[1-9][0-9]*$ ]]; then
        fail "$name must be a positive integer number of seconds"
    fi
}

validate_api_url_path() {
    local value="$1"
    local after_authority path lower_path segment normalized_segment
    local -a segments

    [[ "$value" != *\\* ]] || fail "MNEMONAS_API_URL must not contain backslashes"

    after_authority="${value#*://}"
    if [[ "$after_authority" == */* ]]; then
        path="/${after_authority#*/}"
    else
        path="/"
    fi

    lower_path="${path,,}"
    [[ "$lower_path" != *"%2f"* && "$lower_path" != *"%5c"* ]] || fail "MNEMONAS_API_URL must not contain encoded slashes or backslashes"
    if [[ "$path" != "/" ]]; then
        [[ "$path" != *"//"* ]] || fail "MNEMONAS_API_URL must not contain empty path segments"
    fi

    IFS='/' read -r -a segments <<< "$lower_path"
    for segment in "${segments[@]}"; do
        [[ -n "$segment" ]] || continue
        normalized_segment="${segment//%2e/.}"
        [[ "$normalized_segment" != "." && "$normalized_segment" != ".." ]] || fail "MNEMONAS_API_URL must not contain dot segments"
    done
}

validate_inputs() {
    if [[ "$#" -gt 0 && ( "${1:-}" == "-h" || "${1:-}" == "--help" ) ]]; then
        usage
        exit 0
    fi
    [[ "$#" -eq 0 ]] || fail "unexpected arguments: $*"

    [[ -n "$MNEMONAS_API_URL" ]] || fail "MNEMONAS_API_URL is required"
    [[ "$MNEMONAS_API_URL" == http://* || "$MNEMONAS_API_URL" == https://* ]] || fail "MNEMONAS_API_URL must start with http:// or https://"
    [[ "$MNEMONAS_API_URL" != *[[:space:]]* ]] || fail "MNEMONAS_API_URL must not contain whitespace"
    [[ "$MNEMONAS_API_URL" != *[[:cntrl:]]* ]] || fail "MNEMONAS_API_URL must not contain control characters"
    [[ "$MNEMONAS_API_URL" != *\?* && "$MNEMONAS_API_URL" != *#* ]] || fail "MNEMONAS_API_URL must not contain query strings or fragments"
    [[ "$MNEMONAS_API_URL" != *"@"* ]] || fail "MNEMONAS_API_URL must not contain embedded credentials"
    validate_api_url_path "$MNEMONAS_API_URL"

    [[ -n "$MNEMONAS_BACKUP_JOB_ID" ]] || fail "MNEMONAS_BACKUP_JOB_ID is required"
    [[ "${#MNEMONAS_BACKUP_JOB_ID}" -le 64 ]] || fail "MNEMONAS_BACKUP_JOB_ID must be 64 characters or fewer"
    [[ "$MNEMONAS_BACKUP_JOB_ID" =~ ^[A-Za-z0-9._-]+$ ]] || fail "MNEMONAS_BACKUP_JOB_ID must be a safe backup job ID"
    [[ "$MNEMONAS_BACKUP_JOB_ID" != "." && "$MNEMONAS_BACKUP_JOB_ID" != ".." ]] || fail "MNEMONAS_BACKUP_JOB_ID must not be . or .."

    if [[ -n "$MNEMONAS_COOKIE_FILE" ]]; then
        [[ "$MNEMONAS_COOKIE_FILE" != *[[:cntrl:]]* ]] || fail "MNEMONAS_COOKIE_FILE must not contain control characters"
        [[ -f "$MNEMONAS_COOKIE_FILE" && -r "$MNEMONAS_COOKIE_FILE" ]] || fail "MNEMONAS_COOKIE_FILE must be a readable regular file"
    fi

    [[ "$MNEMONAS_BACKUP_KEEP_ARTIFACT" == "0" || "$MNEMONAS_BACKUP_KEEP_ARTIFACT" == "1" ]] || fail "MNEMONAS_BACKUP_KEEP_ARTIFACT must be 0 or 1"
    [[ "$CURL_INSECURE" == "0" || "$CURL_INSECURE" == "1" ]] || fail "CURL_INSECURE must be 0 or 1"
    validate_positive_seconds "CURL_CONNECT_TIMEOUT" "$CURL_CONNECT_TIMEOUT"
    validate_positive_seconds "CURL_MAX_TIME" "$CURL_MAX_TIME"
}

curl_request() {
    local label="$1"
    local method="$2"
    local url="$3"
    local output="$4"
    shift 4

    local status
    if ! status="$("$CURL_BIN" "${curl_common_args[@]}" -sS -o "$output" -w "%{http_code}" -X "$method" "$@" "$url")"; then
        fail "$label failed to reach $url"
    fi
    printf '%s\n' "$status"
}

expect_status() {
    local label="$1"
    local actual="$2"
    shift 2

    local expected
    for expected in "$@"; do
        if [[ "$actual" == "$expected" ]]; then
            log_ok "$label returned HTTP $actual"
            return
        fi
    done
    fail "$label returned HTTP $actual, expected one of: $*"
}

compact_json_file() {
    local file="$1"
    LC_ALL=C tr -d '[:space:]' < "$file"
}

expect_compact_json_contains() {
    local label="$1"
    local file="$2"
    local expected="$3"
    local compact

    compact="$(compact_json_file "$file")"
    [[ "$compact" == *"$expected"* ]] || fail "$label response did not contain expected JSON field"
}

expect_success_response() {
    local label="$1"
    local file="$2"
    expect_compact_json_contains "$label" "$file" '"success":true'
}

expect_json_string_field() {
    local label="$1"
    local file="$2"
    local field="$3"
    local expected="$4"
    expect_compact_json_contains "$label" "$file" "\"$field\":\"$expected\""
}

run_smoke() {
    local backups_url job_url list_file job_file run_file retention_file drill_file report_file status keep_artifact_json

    api_url="${MNEMONAS_API_URL%/}"
    backups_url="$api_url/maintenance/backups"
    job_url="$backups_url/$MNEMONAS_BACKUP_JOB_ID"

    if [[ "$CURL_INSECURE" == "1" ]]; then
        curl_common_args+=(--insecure)
    fi
    curl_common_args+=(
        "--connect-timeout=$CURL_CONNECT_TIMEOUT"
        "--max-time=$CURL_MAX_TIME"
        -H "Accept: application/json"
    )
    if [[ -n "$MNEMONAS_COOKIE_FILE" ]]; then
        curl_common_args+=(--cookie "$MNEMONAS_COOKIE_FILE")
    fi

    tmp_dir="$(mktemp -d)"
    trap cleanup EXIT

    list_file="$tmp_dir/list.json"
    job_file="$tmp_dir/job.json"
    run_file="$tmp_dir/run.json"
    retention_file="$tmp_dir/retention.json"
    drill_file="$tmp_dir/drill.json"
    report_file="$tmp_dir/restore-report.json"

    log_info "listing backup jobs"
    status="$(curl_request "list backup jobs" "GET" "$backups_url" "$list_file")"
    expect_status "list backup jobs" "$status" "200"
    expect_success_response "list backup jobs" "$list_file"

    log_info "checking backup job $MNEMONAS_BACKUP_JOB_ID"
    status="$(curl_request "get backup job" "GET" "$job_url" "$job_file")"
    expect_status "get backup job" "$status" "200"
    expect_success_response "get backup job" "$job_file"
    expect_json_string_field "get backup job" "$job_file" "id" "$MNEMONAS_BACKUP_JOB_ID"

    log_info "running backup job $MNEMONAS_BACKUP_JOB_ID"
    status="$(curl_request "run backup job" "POST" "$job_url/run" "$run_file" -H "Content-Type: application/json" --data "{}")"
    expect_status "run backup job" "$status" "200"
    expect_success_response "run backup job" "$run_file"
    expect_json_string_field "run backup job" "$run_file" "job_id" "$MNEMONAS_BACKUP_JOB_ID"

    log_info "checking retention state for $MNEMONAS_BACKUP_JOB_ID"
    status="$(curl_request "retention check" "POST" "$job_url/retention-check" "$retention_file" -H "Content-Type: application/json" --data "{}")"
    expect_status "retention check" "$status" "200"
    expect_success_response "retention check" "$retention_file"

    if [[ "$MNEMONAS_BACKUP_KEEP_ARTIFACT" == "1" ]]; then
        keep_artifact_json='{"keep_artifact":true}'
    else
        keep_artifact_json='{"keep_artifact":false}'
    fi

    log_info "running restore drill for $MNEMONAS_BACKUP_JOB_ID"
    status="$(curl_request "run restore drill" "POST" "$job_url/restore-drill" "$drill_file" -H "Content-Type: application/json" --data "$keep_artifact_json")"
    expect_status "run restore drill" "$status" "200"
    expect_success_response "run restore drill" "$drill_file"
    expect_json_string_field "run restore drill" "$drill_file" "job_id" "$MNEMONAS_BACKUP_JOB_ID"
    expect_json_string_field "run restore drill" "$drill_file" "status" "completed"

    log_info "downloading restore report for $MNEMONAS_BACKUP_JOB_ID"
    status="$(curl_request "download restore report" "GET" "$job_url/restore-report" "$report_file")"
    expect_status "download restore report" "$status" "200"
    expect_json_string_field "download restore report" "$report_file" "id" "$MNEMONAS_BACKUP_JOB_ID"
    expect_compact_json_contains "download restore report" "$report_file" '"findings":'

    log_ok "backup restore drill smoke passed for $api_url job $MNEMONAS_BACKUP_JOB_ID"
}

validate_inputs "$@"
require_command
run_smoke
