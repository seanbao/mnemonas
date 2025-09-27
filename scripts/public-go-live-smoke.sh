#!/usr/bin/env bash
set -euo pipefail

SCRIPT_NAME="$(basename "$0")"
CURL_BIN="${CURL_BIN:-curl}"
CURL_CONNECT_TIMEOUT="${CURL_CONNECT_TIMEOUT:-3}"
CURL_MAX_TIME="${CURL_MAX_TIME:-10}"
PUBLIC_SMOKE_BACKEND_TARGETS="${PUBLIC_SMOKE_BACKEND_TARGETS:-8080:/health 9090:/ 9091:/health}"

tmp_dir=""

log_info() {
    printf '[public-go-live-smoke] %s\n' "$*"
}

log_ok() {
    printf '[public-go-live-smoke] OK: %s\n' "$*"
}

fail() {
    printf '[public-go-live-smoke] ERROR: %s\n' "$*" >&2
    exit 1
}

usage() {
    cat <<EOF
Usage:
  $SCRIPT_NAME nas.example.com

Environment:
  CURL_BIN                       Optional curl binary path.
  CURL_CONNECT_TIMEOUT           Optional curl connection timeout in seconds; default 3.
  CURL_MAX_TIME                  Optional curl per-request timeout in seconds; default 10.
  PUBLIC_SMOKE_BACKEND_TARGETS   Optional space-separated port:path checks; default "8080:/health 9090:/ 9091:/health".
EOF
}

cleanup() {
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

is_ipv4_literal_host() {
    local host="$1"
    local octet
    local -a octets

    IFS='.' read -r -a octets <<< "$host"
    [[ "${#octets[@]}" -eq 4 ]] || return 1
    for octet in "${octets[@]}"; do
        [[ "$octet" =~ ^[0-9]{1,3}$ ]] || return 1
        (( 10#$octet >= 0 && 10#$octet <= 255 )) || return 1
    done
    return 0
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
    [[ "$value" =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$ ]] || fail "public domain must be an ASCII hostname"
    [[ "$value" != *".."* ]] || fail "public domain must not contain empty labels"
    [[ "$value" == *.* ]] || fail "public domain must be a fully qualified hostname"
    [[ "$value" != "localhost" && "$value" != *.localhost ]] || fail "public domain must not be localhost"
    ! is_ipv4_literal_host "$value" || fail "public domain must be a hostname, not an IP address"
}

validate_backend_targets() {
    local target port request_path
    for target in $PUBLIC_SMOKE_BACKEND_TARGETS; do
        [[ "$target" == *:* ]] || fail "PUBLIC_SMOKE_BACKEND_TARGETS entries must use port:path: $target"
        port="${target%%:*}"
        request_path="${target#*:}"
        [[ "$port" =~ ^[1-9][0-9]{0,4}$ ]] || fail "backend target port must be numeric: $target"
        (( port <= 65535 )) || fail "backend target port is out of range: $target"
        [[ "$request_path" == /* ]] || fail "backend target path must start with /: $target"
        [[ "$request_path" != *[[:cntrl:][:space:]]* ]] || fail "backend target path must not contain whitespace or control characters: $target"
    done
}

curl_head_status() {
    local url="$1"
    local output="$2"

    "$CURL_BIN" \
        --connect-timeout="$CURL_CONNECT_TIMEOUT" \
        --max-time="$CURL_MAX_TIME" \
        -sS \
        -o "$output" \
        -w "%{http_code}" \
        -I \
        "$url"
}

curl_head_status_and_redirect() {
    local url="$1"
    local output="$2"

    "$CURL_BIN" \
        --connect-timeout="$CURL_CONNECT_TIMEOUT" \
        --max-time="$CURL_MAX_TIME" \
        -sS \
        -o "$output" \
        -w "%{http_code} %{redirect_url}" \
        -I \
        "$url"
}

check_https_health() {
    local domain="$1"
    local status

    if ! status="$(curl_head_status "https://$domain/health" "$tmp_dir/https-health.out")"; then
        fail "HTTPS health check failed to reach https://$domain/health"
    fi
    [[ "$status" == "200" ]] || fail "HTTPS health returned HTTP $status, expected 200"
    log_ok "HTTPS health returned HTTP 200"
}

check_http_redirect() {
    local domain="$1"
    local status redirect status_and_redirect

    if ! status_and_redirect="$(curl_head_status_and_redirect "http://$domain/health" "$tmp_dir/http-redirect.out")"; then
        fail "HTTP redirect check failed to reach http://$domain/health"
    fi
    status="${status_and_redirect%% *}"
    redirect="${status_and_redirect#* }"

    case "$status" in
        301|302|307|308) ;;
        *) fail "HTTP health returned HTTP $status, expected a redirect to HTTPS on the same domain" ;;
    esac

    case "$redirect" in
        "https://$domain/"*|"https://$domain:443/"*)
            log_ok "HTTP health redirects to HTTPS on the same domain"
            ;;
        *)
            fail "HTTP health redirect target is not same-domain HTTPS: ${redirect:-<empty>}"
            ;;
    esac
}

check_backend_target_private() {
    local domain="$1"
    local target="$2"
    local port="${target%%:*}"
    local request_path="${target#*:}"
    local url="http://$domain:$port$request_path"
    local status

    if status="$(curl_head_status "$url" "$tmp_dir/backend-$port.out")"; then
        if [[ "$status" =~ ^[1-9][0-9][0-9]$ ]]; then
            fail "backend target $url returned HTTP $status; it must fail to connect or time out from the public internet"
        fi
    fi

    log_ok "backend target $url did not return an HTTP status"
}

run_smoke() {
    local raw_domain="$1"
    local domain target

    domain="$(normalize_domain "$raw_domain")"
    validate_domain "$domain"
    validate_positive_seconds "CURL_CONNECT_TIMEOUT" "$CURL_CONNECT_TIMEOUT"
    validate_positive_seconds "CURL_MAX_TIME" "$CURL_MAX_TIME"
    validate_backend_targets

    tmp_dir="$(mktemp -d)"
    trap cleanup EXIT

    log_info "probing public domain $domain"
    check_https_health "$domain"
    check_http_redirect "$domain"

    for target in $PUBLIC_SMOKE_BACKEND_TARGETS; do
        check_backend_target_private "$domain" "$target"
    done

    log_ok "public go-live smoke passed for $domain"
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    usage
    exit 0
fi
[[ "$#" -eq 1 ]] || fail "expected exactly one public domain argument"
require_command
run_smoke "$1"
