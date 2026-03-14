#!/usr/bin/env bash
set -euo pipefail

SCRIPT_NAME="$(basename "$0")"
CURL_BIN="${CURL_BIN:-curl}"
TIMEOUT_BIN="${TIMEOUT_BIN:-timeout}"
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
  TIMEOUT_BIN                    Optional timeout binary path for TCP reachability checks; default timeout.
  CURL_CONNECT_TIMEOUT           Optional curl connection timeout in seconds; default 3.
  CURL_MAX_TIME                  Optional curl per-request timeout in seconds; default 10.
  PUBLIC_SMOKE_BACKEND_TARGETS   Optional space-separated port:path checks; paths must be unambiguous absolute paths; must not be blank; default "8080:/health 9090:/ 9091:/health".
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
    if ! command -v "$TIMEOUT_BIN" >/dev/null 2>&1; then
        fail "timeout is required for backend TCP reachability checks; set TIMEOUT_BIN to a compatible timeout binary"
    fi
}

validate_positive_seconds() {
    local name="$1"
    local value="$2"
    if [[ ! "$value" =~ ^[1-9][0-9]*$ ]]; then
        fail "$name must be a positive integer number of seconds"
    fi
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

validate_backend_target_path() {
    local target="$1"
    local request_path="$2"
    local lower_path segment normalized_segment
    local -a segments

    [[ "$request_path" == /* ]] || fail "backend target path must start with /: $target"
    [[ "$request_path" != *[[:cntrl:][:space:]]* ]] || fail "backend target path must not contain whitespace or control characters: $target"
    [[ "$request_path" != *"?"* && "$request_path" != *"#"* && "$request_path" != *"@"* ]] || fail "backend target path must not contain query strings, fragments, or userinfo: $target"
    [[ "$request_path" != *\\* ]] || fail "backend target path must not contain backslashes: $target"

    lower_path="${request_path,,}"
    [[ "$lower_path" != *"%2f"* && "$lower_path" != *"%5c"* ]] || fail "backend target path must not contain encoded slashes or backslashes: $target"
    [[ "$request_path" != *"//"* ]] || fail "backend target path must not contain empty path segments: $target"

    IFS='/' read -r -a segments <<< "$lower_path"
    for segment in "${segments[@]}"; do
        [[ -n "$segment" ]] || continue
        normalized_segment="${segment//%2e/.}"
        [[ "$normalized_segment" != "." && "$normalized_segment" != ".." ]] || fail "backend target path must not contain dot segments: $target"
    done
}

validate_backend_targets() {
    local target port request_path
    [[ -n "${PUBLIC_SMOKE_BACKEND_TARGETS//[[:space:]]/}" ]] || fail "PUBLIC_SMOKE_BACKEND_TARGETS must include at least one port:path check"

    for target in $PUBLIC_SMOKE_BACKEND_TARGETS; do
        [[ "$target" == *:* ]] || fail "PUBLIC_SMOKE_BACKEND_TARGETS entries must use port:path: $target"
        port="${target%%:*}"
        request_path="${target#*:}"
        [[ "$port" =~ ^[1-9][0-9]{0,4}$ ]] || fail "backend target port must be numeric: $target"
        (( port <= 65535 )) || fail "backend target port is out of range: $target"
        validate_backend_target_path "$target" "$request_path"
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

tcp_connect_succeeds() {
    local domain="$1"
    local port="$2"

    # The inner Bash receives the domain and port as positional arguments.
    # shellcheck disable=SC2016
    "$TIMEOUT_BIN" "$CURL_CONNECT_TIMEOUT" bash -c '
        set -euo pipefail
        exec 3<>"/dev/tcp/$1/$2"
        exec 3<&-
        exec 3>&-
    ' bash "$domain" "$port" >/dev/null 2>&1
}

https_redirect_targets_domain() {
    local domain="$1"
    local redirect_url="$2"

    case "$redirect_url" in
        "https://$domain"|"https://$domain/"*|"https://$domain?"*|"https://$domain#"*)
            return 0
            ;;
        "https://$domain:443"|"https://$domain:443/"*|"https://$domain:443?"*|"https://$domain:443#"*)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
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

    if https_redirect_targets_domain "$domain" "$redirect"; then
        log_ok "HTTP health redirects to HTTPS on the same domain"
    else
        fail "HTTP health redirect target is not same-domain HTTPS: ${redirect:-<empty>}"
    fi
}

check_backend_target_private() {
    local domain="$1"
    local target="$2"
    local port="${target%%:*}"
    local request_path="${target#*:}"
    local url="http://$domain:$port$request_path"
    local status

    if tcp_connect_succeeds "$domain" "$port"; then
        fail "backend target $domain:$port accepted a TCP connection; it must fail to connect or time out from the public internet"
    fi

    if status="$(curl_head_status "$url" "$tmp_dir/backend-$port.out")"; then
        if [[ "$status" =~ ^[1-9][0-9][0-9]$ ]]; then
            fail "backend target $url returned HTTP $status; it must fail to connect or time out from the public internet"
        fi
    fi

    log_ok "backend target $url was not reachable over TCP and did not return an HTTP status"
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
