#!/usr/bin/env bash
set -euo pipefail

SCRIPT_NAME="$(basename "$0")"
CURL_BIN="${CURL_BIN:-curl}"
WEBDAV_URL="${WEBDAV_URL:-}"
WEBDAV_USERNAME="${WEBDAV_USERNAME:-${MNEMONAS_WEBDAV_USERNAME:-}}"
WEBDAV_PASSWORD="${WEBDAV_PASSWORD:-${MNEMONAS_WEBDAV_PASSWORD:-}}"
WEBDAV_TEST_ROOT="${WEBDAV_TEST_ROOT:-mnemonas-smoke-$(date +%s)-$$}"
CURL_INSECURE="${CURL_INSECURE:-0}"
CURL_CONNECT_TIMEOUT="${CURL_CONNECT_TIMEOUT:-10}"
CURL_MAX_TIME="${CURL_MAX_TIME:-30}"

tmp_dir=""
root_url=""
created_root=0
curl_common_args=()

log_info() {
    printf '[webdav-smoke] %s\n' "$*"
}

log_ok() {
    printf '[webdav-smoke] OK: %s\n' "$*"
}

fail() {
    printf '[webdav-smoke] ERROR: %s\n' "$*" >&2
    exit 1
}

usage() {
    cat <<EOF
Usage:
  WEBDAV_URL=http://127.0.0.1:8080/dav \\
  MNEMONAS_WEBDAV_USERNAME=<mnemonas-or-webdav-username> \\
  MNEMONAS_WEBDAV_PASSWORD=<mnemonas-or-webdav-password> \\
  $SCRIPT_NAME

Environment:
  WEBDAV_URL              Required WebDAV root URL.
  WEBDAV_USERNAME         Optional username; falls back to MNEMONAS_WEBDAV_USERNAME.
  WEBDAV_PASSWORD         Optional password; falls back to MNEMONAS_WEBDAV_PASSWORD.
  WEBDAV_TEST_ROOT        Optional one-segment temporary collection name.
  CURL_BIN                Optional curl binary path.
  CURL_INSECURE=1         Pass --insecure to curl for local TLS smoke tests.
  CURL_CONNECT_TIMEOUT    Optional curl connection timeout in seconds; default 10.
  CURL_MAX_TIME           Optional curl per-request timeout in seconds; default 30.
EOF
}

cleanup() {
    if [[ "$created_root" == "1" && -n "$root_url" ]]; then
        "$CURL_BIN" "${curl_common_args[@]}" -sS -o /dev/null -w "%{http_code}" -X DELETE "$root_url/" >/dev/null 2>&1 || true
    fi
    if [[ -n "$tmp_dir" ]]; then
        rm -rf "$tmp_dir"
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

validate_inputs() {
    if [[ "$#" -gt 0 && ( "${1:-}" == "-h" || "${1:-}" == "--help" ) ]]; then
        usage
        exit 0
    fi
    [[ "$#" -eq 0 ]] || fail "unexpected arguments: $*"

    [[ -n "$WEBDAV_URL" ]] || fail "WEBDAV_URL is required"
    [[ "$WEBDAV_URL" == http://* || "$WEBDAV_URL" == https://* ]] || fail "WEBDAV_URL must start with http:// or https://"
    [[ "$WEBDAV_URL" != *[[:cntrl:]]* ]] || fail "WEBDAV_URL must not contain control characters"
    [[ "$WEBDAV_URL" != *\?* && "$WEBDAV_URL" != *#* ]] || fail "WEBDAV_URL must not contain query strings or fragments"
    [[ "$WEBDAV_URL" != *"@"* ]] || fail "WEBDAV_URL must not contain embedded credentials"

    [[ "$WEBDAV_TEST_ROOT" != *[[:cntrl:]]* ]] || fail "WEBDAV_TEST_ROOT must not contain control characters"
    [[ "$WEBDAV_TEST_ROOT" =~ ^[A-Za-z0-9._-]+$ ]] || fail "WEBDAV_TEST_ROOT must be a single safe path segment"
    [[ "$WEBDAV_TEST_ROOT" != "." && "$WEBDAV_TEST_ROOT" != ".." ]] || fail "WEBDAV_TEST_ROOT must not be . or .."

    if [[ -n "$WEBDAV_USERNAME" || -n "$WEBDAV_PASSWORD" ]]; then
        [[ -n "$WEBDAV_USERNAME" && -n "$WEBDAV_PASSWORD" ]] || fail "both WebDAV username and password are required when authentication is used"
        [[ "$WEBDAV_USERNAME" != *[[:cntrl:]]* && "$WEBDAV_PASSWORD" != *[[:cntrl:]]* ]] || fail "WebDAV credentials must not contain control characters"
    fi

    validate_positive_seconds "CURL_CONNECT_TIMEOUT" "$CURL_CONNECT_TIMEOUT"
    validate_positive_seconds "CURL_MAX_TIME" "$CURL_MAX_TIME"
}

escape_curl_config_value() {
    local value="$1"
    value="${value//\\/\\\\}"
    value="${value//\"/\\\"}"
    printf '%s' "$value"
}

write_curl_auth_config() {
    if [[ -z "$WEBDAV_USERNAME" && -z "$WEBDAV_PASSWORD" ]]; then
        return
    fi

    local escaped_username escaped_password
    escaped_username="$(escape_curl_config_value "$WEBDAV_USERNAME")"
    escaped_password="$(escape_curl_config_value "$WEBDAV_PASSWORD")"
    curl_auth_config="$tmp_dir/curl-auth.conf"
    printf 'user = "%s:%s"\n' "$escaped_username" "$escaped_password" > "$curl_auth_config"
    chmod 0600 "$curl_auth_config"
    curl_common_args+=(--config "$curl_auth_config")
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

curl_head_status() {
    local label="$1"
    local url="$2"
    local output="$3"

    local status
    if ! status="$("$CURL_BIN" "${curl_common_args[@]}" -sS -o "$output" -w "%{http_code}" -I "$url")"; then
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

run_smoke() {
    local base_url file_url spaced_file_url copied_url moved_url
    local upload_file download_file spaced_upload_file spaced_download_file moved_download_file status

    base_url="${WEBDAV_URL%/}"
    root_url="$base_url/$WEBDAV_TEST_ROOT"
    file_url="$root_url/hello.txt"
    spaced_file_url="$root_url/space%20name.txt"
    copied_url="$root_url/copied.txt"
    moved_url="$root_url/moved.txt"

    if [[ "$CURL_INSECURE" == "1" ]]; then
        curl_common_args+=(--insecure)
    fi
    curl_common_args+=(
        "--connect-timeout=$CURL_CONNECT_TIMEOUT"
        "--max-time=$CURL_MAX_TIME"
    )

    tmp_dir="$(mktemp -d)"
    trap cleanup EXIT
    write_curl_auth_config

    upload_file="$tmp_dir/upload.txt"
    download_file="$tmp_dir/download.txt"
    spaced_upload_file="$tmp_dir/spaced-upload.txt"
    spaced_download_file="$tmp_dir/spaced-download.txt"
    moved_download_file="$tmp_dir/moved-download.txt"
    printf 'mnemonas webdav smoke\n' > "$upload_file"
    printf 'mnemonas webdav smoke spaced path\n' > "$spaced_upload_file"

    log_info "probing $base_url"
    status="$(curl_request "OPTIONS" OPTIONS "$base_url/" "$tmp_dir/options.out")"
    expect_status "OPTIONS" "$status" 200 204

    status="$(curl_request "MKCOL" MKCOL "$root_url/" "$tmp_dir/mkcol.out")"
    expect_status "MKCOL" "$status" 201
    created_root=1

    status="$(curl_request "PUT" PUT "$file_url" "$tmp_dir/put.out" -T "$upload_file")"
    expect_status "PUT" "$status" 200 201 204

    status="$(curl_request "PROPFIND" PROPFIND "$root_url/" "$tmp_dir/propfind.out" -H "Depth: 1")"
    expect_status "PROPFIND" "$status" 207

    status="$(curl_request "GET" GET "$file_url" "$download_file")"
    expect_status "GET" "$status" 200
    if ! cmp -s "$upload_file" "$download_file"; then
        fail "downloaded file content did not match uploaded content"
    fi
    log_ok "GET content matches uploaded content"

    status="$(curl_head_status "HEAD" "$file_url" "$tmp_dir/head.out")"
    expect_status "HEAD" "$status" 200

    status="$(curl_request "PUT URL-encoded space path" PUT "$spaced_file_url" "$tmp_dir/put-spaced.out" -T "$spaced_upload_file")"
    expect_status "PUT URL-encoded space path" "$status" 200 201 204

    status="$(curl_request "GET URL-encoded space path" GET "$spaced_file_url" "$spaced_download_file")"
    expect_status "GET URL-encoded space path" "$status" 200
    if ! cmp -s "$spaced_upload_file" "$spaced_download_file"; then
        fail "URL-encoded space path download content did not match uploaded content"
    fi
    log_ok "URL-encoded space path content matches uploaded content"

    status="$(curl_request "COPY" COPY "$file_url" "$tmp_dir/copy.out" -H "Destination: $copied_url")"
    expect_status "COPY" "$status" 201 204

    status="$(curl_request "MOVE" MOVE "$copied_url" "$tmp_dir/move.out" -H "Destination: $moved_url")"
    expect_status "MOVE" "$status" 201 204

    status="$(curl_request "GET moved file" GET "$moved_url" "$moved_download_file")"
    expect_status "GET moved file" "$status" 200
    if ! cmp -s "$upload_file" "$moved_download_file"; then
        fail "moved file content did not match uploaded content"
    fi
    log_ok "MOVE content matches uploaded content"

    status="$(curl_head_status "HEAD moved source" "$copied_url" "$tmp_dir/head-moved-source.out")"
    expect_status "HEAD moved source" "$status" 404 410
    log_ok "MOVE source no longer exists"

    status="$(curl_request "DELETE original file" DELETE "$file_url" "$tmp_dir/delete-file.out")"
    expect_status "DELETE original file" "$status" 200 204

    status="$(curl_request "DELETE moved file" DELETE "$moved_url" "$tmp_dir/delete-moved.out")"
    expect_status "DELETE moved file" "$status" 200 204

    status="$(curl_request "DELETE URL-encoded space path" DELETE "$spaced_file_url" "$tmp_dir/delete-spaced.out")"
    expect_status "DELETE URL-encoded space path" "$status" 200 204

    status="$(curl_request "DELETE collection" DELETE "$root_url/" "$tmp_dir/delete-root.out")"
    expect_status "DELETE collection" "$status" 200 204
    created_root=0

    log_ok "WebDAV smoke passed for $base_url"
}

validate_inputs "$@"
require_command
run_smoke
