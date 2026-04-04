#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="${TMPDIR:-/tmp}/mnemonas-public-go-live-smoke-test-$$"

fail() {
    echo "[public-go-live-smoke-test] ERROR: $*" >&2
    exit 1
}

assert_file_contains() {
    local file="$1"
    local expected="$2"
    if ! grep -Fq -- "$expected" "$file"; then
        echo "Expected to find: $expected" >&2
        echo "--- $file ---" >&2
        cat "$file" >&2
        fail "missing expected text"
    fi
}

assert_file_not_contains() {
    local file="$1"
    local unexpected="$2"
    if [[ -f "$file" ]] && grep -Fq -- "$unexpected" "$file"; then
        echo "Unexpected text: $unexpected" >&2
        echo "--- $file ---" >&2
        cat "$file" >&2
        fail "found unexpected text"
    fi
}

cleanup() {
    rm -rf "$TMP_ROOT"
}

make_fake_timeout() {
    local bin_dir="$1"
    local name="$2"
    mkdir -p "$bin_dir"
    cat > "$bin_dir/$name" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >> "${PUBLIC_SMOKE_TCP_LOG:-/dev/null}"

port="${@: -1}"
case " ${PUBLIC_SMOKE_TCP_OPEN_PORTS:-} " in
    *" $port "*) exit 0 ;;
esac

exit 124
EOF
    chmod +x "$bin_dir/$name"
}

make_fake_curl() {
    local bin_dir="$1"
    mkdir -p "$bin_dir"
    cat > "$bin_dir/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

output=""
write_format=""
url=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        -o)
            output="$2"
            shift 2
            ;;
        -w)
            write_format="$2"
            shift 2
            ;;
        --connect-timeout=*|--max-time=*)
            printf '%s\n' "$1" >> "$PUBLIC_SMOKE_CURL_LOG"
            shift
            ;;
        --connect-timeout|--max-time)
            printf '%s=%s\n' "$1" "$2" >> "$PUBLIC_SMOKE_CURL_LOG"
            shift 2
            ;;
        -I|-s|-S|-sS)
            shift
            ;;
        -*)
            shift
            ;;
        *)
            url="$1"
            shift
            ;;
    esac
done

printf '%s\n' "$url" >> "$PUBLIC_SMOKE_CURL_LOG"

status="000"
redirect=""
case "$url" in
    https://nas.example.com/health)
        status="${PUBLIC_SMOKE_HTTPS_STATUS:-200}"
        ;;
    http://nas.example.com/health)
        status="${PUBLIC_SMOKE_HTTP_STATUS:-308}"
        redirect="${PUBLIC_SMOKE_REDIRECT_URL:-https://nas.example.com/health}"
        ;;
    http://nas.example.com:8080/health|http://nas.example.com:9090/|http://nas.example.com:9091/health)
        if [[ "${PUBLIC_SMOKE_BACKEND_OPEN:-0}" == "1" ]]; then
            status="401"
        else
            exit 7
        fi
        ;;
    *)
        status="404"
        ;;
esac

if [[ -n "$output" ]]; then
    : > "$output"
fi
case "$write_format" in
    "%{http_code}")
        printf '%s' "$status"
        ;;
    "%{http_code} %{redirect_url}")
        printf '%s %s' "$status" "$redirect"
        ;;
    *)
        printf '%s' "$status"
        ;;
esac
EOF
    chmod +x "$bin_dir/curl"

    make_fake_timeout "$bin_dir" "timeout"
}

link_runtime_command() {
    local bin_dir="$1"
    local command_name="$2"
    local command_path

    command_path="$(command -v "$command_name")" || fail "missing runtime command: $command_name"
    ln -sf "$command_path" "$bin_dir/$command_name"
}

run_expect_failure() {
    local output="$1"
    shift
    if "$@" > "$output" 2>&1; then
        cat "$output" >&2
        fail "expected command to fail"
    fi
}

run_success_test() {
    local case_dir="$TMP_ROOT/success"
    local fake_bin="$case_dir/bin"
    local curl_log="$case_dir/curl.log"
    local tcp_log="$case_dir/tcp.log"
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"

    env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$curl_log" \
        PUBLIC_SMOKE_TCP_LOG="$tcp_log" \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "NAS.EXAMPLE.COM." > "$case_dir/out.log" 2>&1

    assert_file_contains "$case_dir/out.log" "public go-live smoke passed for nas.example.com"
    assert_file_contains "$case_dir/out.log" "HTTPS health returned HTTP 200"
    assert_file_contains "$case_dir/out.log" "was not reachable over TCP and did not return an HTTP status"
    assert_file_contains "$curl_log" "https://nas.example.com/health"
    assert_file_contains "$curl_log" "http://nas.example.com/health"
    assert_file_contains "$curl_log" "http://nas.example.com:8080/health"
    assert_file_contains "$curl_log" "http://nas.example.com:9090/"
    assert_file_contains "$curl_log" "http://nas.example.com:9091/health"
    assert_file_contains "$tcp_log" "nas.example.com 8080"
    assert_file_contains "$tcp_log" "nas.example.com 9090"
    assert_file_contains "$tcp_log" "nas.example.com 9091"
    assert_file_contains "$curl_log" "--connect-timeout=3"
    assert_file_contains "$curl_log" "--max-time=10"
}

run_invalid_domain_test() {
    local case_dir="$TMP_ROOT/invalid-domain"
    mkdir -p "$case_dir"

    run_expect_failure "$case_dir/out.log" bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "https://nas.example.com"
    assert_file_contains "$case_dir/out.log" "public domain must not include a URL scheme"

    run_expect_failure "$case_dir/localhost.log" bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "localhost"
    assert_file_contains "$case_dir/localhost.log" "public domain must be a fully qualified hostname"

    run_expect_failure "$case_dir/ip-address.log" bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "127.0.0.1"
    assert_file_contains "$case_dir/ip-address.log" "public domain must be a hostname, not an IP address"

    run_expect_failure "$case_dir/ipv4-like-overrange.log" bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "999.999.999.999"
    assert_file_contains "$case_dir/ipv4-like-overrange.log" "public domain must be a hostname, not an IP address"

    run_expect_failure "$case_dir/leading-label-hyphen.log" bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.-example.com"
    assert_file_contains "$case_dir/leading-label-hyphen.log" "public domain must be a valid ASCII hostname"

    run_expect_failure "$case_dir/trailing-label-hyphen.log" bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example-.com"
    assert_file_contains "$case_dir/trailing-label-hyphen.log" "public domain must be a valid ASCII hostname"

    run_expect_failure "$case_dir/long-label.log" bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.example.com"
    assert_file_contains "$case_dir/long-label.log" "public domain must be a valid ASCII hostname"

    run_expect_failure "$case_dir/repeated-trailing-dot.log" bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com.."
    assert_file_contains "$case_dir/repeated-trailing-dot.log" "public domain must be a valid ASCII hostname"
}

run_invalid_timeout_test() {
    local case_dir="$TMP_ROOT/invalid-timeout"
    local fake_bin="$case_dir/bin"
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"

    run_expect_failure "$case_dir/out.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/curl.log" \
        CURL_CONNECT_TIMEOUT=0 \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/out.log" "CURL_CONNECT_TIMEOUT must be a positive integer number of seconds"
}

run_gtimeout_fallback_test() {
    local case_dir="$TMP_ROOT/gtimeout-fallback"
    local fake_bin="$case_dir/bin"
    local curl_log="$case_dir/curl.log"
    local tcp_log="$case_dir/tcp.log"
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"
    rm -f "$fake_bin/timeout"
    make_fake_timeout "$fake_bin" "gtimeout"
    link_runtime_command "$fake_bin" "bash"
    link_runtime_command "$fake_bin" "basename"
    link_runtime_command "$fake_bin" "mktemp"
    link_runtime_command "$fake_bin" "rm"

    env \
        PATH="$fake_bin" \
        PUBLIC_SMOKE_CURL_LOG="$curl_log" \
        PUBLIC_SMOKE_TCP_LOG="$tcp_log" \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com" > "$case_dir/out.log" 2>&1

    assert_file_contains "$case_dir/out.log" "public go-live smoke passed for nas.example.com"
    assert_file_contains "$tcp_log" "nas.example.com 8080"
    assert_file_contains "$tcp_log" "nas.example.com 9090"
    assert_file_contains "$tcp_log" "nas.example.com 9091"
}

run_timeout_override_test() {
    local case_dir="$TMP_ROOT/timeout-override"
    local fake_bin="$case_dir/bin"
    local tcp_log="$case_dir/tcp.log"
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"
    make_fake_timeout "$fake_bin" "gtimeout"

    env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/curl.log" \
        PUBLIC_SMOKE_TCP_LOG="$tcp_log" \
        TIMEOUT_BIN=gtimeout \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com" > "$case_dir/out.log" 2>&1

    assert_file_contains "$case_dir/out.log" "public go-live smoke passed for nas.example.com"
    assert_file_contains "$tcp_log" "nas.example.com 8080"
}

run_missing_timeout_tool_test() {
    local case_dir="$TMP_ROOT/missing-timeout-tool"
    local fake_bin="$case_dir/bin"
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"

    run_expect_failure "$case_dir/out.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/curl.log" \
        TIMEOUT_BIN="$case_dir/missing-timeout" \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/out.log" "TIMEOUT_BIN must point to a timeout-compatible command"
}

run_invalid_backend_targets_test() {
    local case_dir="$TMP_ROOT/invalid-backend-targets"
    local fake_bin="$case_dir/bin"
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"

    run_expect_failure "$case_dir/whitespace.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/whitespace-curl.log" \
        PUBLIC_SMOKE_BACKEND_TARGETS=$' \t ' \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/whitespace.log" "PUBLIC_SMOKE_BACKEND_TARGETS must include at least one port:path check"

    run_expect_failure "$case_dir/missing-colon-secret.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/missing-colon-secret-curl.log" \
        PUBLIC_SMOKE_BACKEND_TARGETS='8080/health?token=secret-token' \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/missing-colon-secret.log" "PUBLIC_SMOKE_BACKEND_TARGETS entries must use port:path: <redacted-target>"
    assert_file_not_contains "$case_dir/missing-colon-secret.log" "secret-token"

    run_expect_failure "$case_dir/bad-port-secret.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/bad-port-secret-curl.log" \
        PUBLIC_SMOKE_BACKEND_TARGETS='admin@secret:/health' \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/bad-port-secret.log" "backend target port must be numeric: <redacted-target>"
    assert_file_not_contains "$case_dir/bad-port-secret.log" "admin@secret"

    run_expect_failure "$case_dir/query.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/query-curl.log" \
        PUBLIC_SMOKE_BACKEND_TARGETS='8080:/health?token=secret-token' \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/query.log" "backend target path must not contain query strings, fragments, or userinfo: 8080:<redacted-path>"
    assert_file_not_contains "$case_dir/query.log" "secret-token"

    run_expect_failure "$case_dir/fragment.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/fragment-curl.log" \
        PUBLIC_SMOKE_BACKEND_TARGETS='8080:/health#secret-fragment' \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/fragment.log" "backend target path must not contain query strings, fragments, or userinfo: 8080:<redacted-path>"
    assert_file_not_contains "$case_dir/fragment.log" "secret-fragment"

    run_expect_failure "$case_dir/userinfo.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/userinfo-curl.log" \
        PUBLIC_SMOKE_BACKEND_TARGETS='8080:/@admin:secret' \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/userinfo.log" "backend target path must not contain query strings, fragments, or userinfo: 8080:<redacted-path>"
    assert_file_not_contains "$case_dir/userinfo.log" "admin:secret"

    run_expect_failure "$case_dir/control.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/control-curl.log" \
        PUBLIC_SMOKE_BACKEND_TARGETS=$'8080:/health\rINJECTED=1' \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/control.log" "backend target path must not contain whitespace or control characters: 8080:<redacted-path>"
    assert_file_not_contains "$case_dir/control.log" $'8080:/health\rINJECTED=1'

    run_expect_failure "$case_dir/backslash.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/backslash-curl.log" \
        PUBLIC_SMOKE_BACKEND_TARGETS='8080:/health\debug' \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/backslash.log" "backend target path must not contain backslashes"

    run_expect_failure "$case_dir/encoded-slash.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/encoded-slash-curl.log" \
        PUBLIC_SMOKE_BACKEND_TARGETS='8080:/api%2Fhealth' \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/encoded-slash.log" "backend target path must not contain encoded slashes or backslashes"

    run_expect_failure "$case_dir/encoded-backslash.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/encoded-backslash-curl.log" \
        PUBLIC_SMOKE_BACKEND_TARGETS='8080:/api%5Chealth' \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/encoded-backslash.log" "backend target path must not contain encoded slashes or backslashes"

    run_expect_failure "$case_dir/dot-segment.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/dot-segment-curl.log" \
        PUBLIC_SMOKE_BACKEND_TARGETS='8080:/../health' \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/dot-segment.log" "backend target path must not contain dot segments"

    run_expect_failure "$case_dir/encoded-dot-segment.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/encoded-dot-segment-curl.log" \
        PUBLIC_SMOKE_BACKEND_TARGETS='8080:/%2e%2e/health' \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/encoded-dot-segment.log" "backend target path must not contain dot segments"

    run_expect_failure "$case_dir/empty-segment.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/empty-segment-curl.log" \
        PUBLIC_SMOKE_BACKEND_TARGETS='8080:/api//health' \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/empty-segment.log" "backend target path must not contain empty path segments"
}

run_redirect_variant_test() {
    local case_dir="$TMP_ROOT/redirect-variants"
    local fake_bin="$case_dir/bin"
    local case_name curl_log output redirect
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"

    for redirect in \
        "https://nas.example.com" \
        "https://nas.example.com?from=http" \
        "https://nas.example.com#health" \
        "https://nas.example.com:443" \
        "https://nas.example.com:443?from=http"
    do
        case_name="${redirect//[^a-zA-Z0-9]/_}"
        curl_log="$case_dir/curl-$case_name.log"
        output="$case_dir/out-$case_name.log"

        env \
            PATH="$fake_bin:$PATH" \
            PUBLIC_SMOKE_CURL_LOG="$curl_log" \
            PUBLIC_SMOKE_REDIRECT_URL="$redirect" \
            bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com" > "$output" 2>&1

        assert_file_contains "$output" "HTTP health redirects to HTTPS on the same domain"
        assert_file_contains "$output" "public go-live smoke passed for nas.example.com"
    done
}

run_bad_redirect_test() {
    local case_dir="$TMP_ROOT/bad-redirect"
    local fake_bin="$case_dir/bin"
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"

    run_expect_failure "$case_dir/out.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/curl.log" \
        PUBLIC_SMOKE_REDIRECT_URL="https://other.example.com/health" \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/out.log" "HTTP health redirect target is not same-domain HTTPS"

    run_expect_failure "$case_dir/suffix-domain.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/suffix-domain-curl.log" \
        PUBLIC_SMOKE_REDIRECT_URL="https://nas.example.com.evil.example/health" \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/suffix-domain.log" "HTTP health redirect target is not same-domain HTTPS"

    run_expect_failure "$case_dir/query-secret.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/query-secret-curl.log" \
        PUBLIC_SMOKE_REDIRECT_URL="https://other.example.com/health?token=secret-token#debug" \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/query-secret.log" "HTTP health redirect target is not same-domain HTTPS: https://other.example.com/health?<redacted-query>"
    assert_file_not_contains "$case_dir/query-secret.log" "secret-token"

    run_expect_failure "$case_dir/userinfo-secret.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/userinfo-secret-curl.log" \
        PUBLIC_SMOKE_REDIRECT_URL="https://user:secret-pass@other.example.com/health" \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/userinfo-secret.log" "HTTP health redirect target is not same-domain HTTPS: https://<redacted-userinfo>@other.example.com/health"
    assert_file_not_contains "$case_dir/userinfo-secret.log" "secret-pass"
}

run_open_backend_test() {
    local case_dir="$TMP_ROOT/open-backend"
    local fake_bin="$case_dir/bin"
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"

    run_expect_failure "$case_dir/out.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/curl.log" \
        PUBLIC_SMOKE_TCP_LOG="$case_dir/tcp.log" \
        PUBLIC_SMOKE_TCP_OPEN_PORTS="8080" \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/out.log" "backend target nas.example.com:8080 accepted a TCP connection"
}

run_open_backend_http_status_test() {
    local case_dir="$TMP_ROOT/open-backend-http-status"
    local fake_bin="$case_dir/bin"
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"

    run_expect_failure "$case_dir/out.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/curl.log" \
        PUBLIC_SMOKE_TCP_LOG="$case_dir/tcp.log" \
        PUBLIC_SMOKE_BACKEND_OPEN=1 \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "nas.example.com"
    assert_file_contains "$case_dir/out.log" "http://nas.example.com:8080/health returned HTTP 401"
}

run_docs_contract_test() {
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" './scripts/public-go-live-smoke.sh'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" './scripts/public-go-live-smoke.sh'
    assert_file_contains "$REPO_ROOT/docs/cloud-firewall-checklist.md" './scripts/public-go-live-smoke.sh'
    assert_file_contains "$REPO_ROOT/docs/cloud-firewall-checklist.en.md" './scripts/public-go-live-smoke.sh'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" '外部网络'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" 'external network'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "公网检查需要公网完整域名，不接受 \`localhost\` 或 IP 地址"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "Public checks require a fully qualified public hostname, not \`localhost\` or an IP address"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "path 必须是不含 query、fragment、userinfo、反斜杠、编码斜杠、编码反斜杠、空路径段或 \`.\`/\`..\` 路径段的明确绝对路径"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "path must be an unambiguous absolute path without query strings, fragments, userinfo, backslashes, encoded slashes, encoded backslashes, empty path segments, or \`.\`/\`..\` path segments"
    assert_file_contains "$REPO_ROOT/docs/cloud-firewall-checklist.md" "path 必须是不含 query、fragment、userinfo、反斜杠、编码斜杠、编码反斜杠、空路径段或 \`.\`/\`..\` 路径段的明确绝对路径"
    assert_file_contains "$REPO_ROOT/docs/cloud-firewall-checklist.en.md" "path must be an unambiguous absolute path without query strings, fragments, userinfo, backslashes, encoded slashes, encoded backslashes, empty path segments, or \`.\`/\`..\` path segments"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "无效自定义目标或错误跳转的诊断只保留目标形状，不回显 query、fragment、userinfo 或控制字符路径内容"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "Diagnostics for invalid custom targets or bad redirects keep only the target shape and do not echo query strings, fragments, userinfo, or control-character path content"
    assert_file_contains "$REPO_ROOT/docs/cloud-firewall-checklist.md" "无效自定义目标或错误跳转的诊断只保留目标形状，不回显 query、fragment、userinfo 或控制字符路径内容"
    assert_file_contains "$REPO_ROOT/docs/cloud-firewall-checklist.en.md" "Diagnostics for invalid custom targets or bad redirects keep only the target shape and do not echo query strings, fragments, userinfo, or control-character path content"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "只要 TCP 可连接，即使没有 HTTP 状态码，也表示后端端口仍可从公网访问"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "Any successful TCP connection means the backend port is still publicly reachable, even when no HTTP status is returned"
    assert_file_contains "$REPO_ROOT/docs/cloud-firewall-checklist.md" "只要 TCP 可连接，即使没有 HTTP 状态码，也表示后端端口仍可从公网访问"
    assert_file_contains "$REPO_ROOT/docs/cloud-firewall-checklist.en.md" "Any successful TCP connection means the backend port is still publicly reachable, even when no HTTP status is returned"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "脚本会按 \`timeout\`、\`gtimeout\` 顺序自动选择"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "The script auto-selects \`timeout\` then \`gtimeout\`"
    assert_file_contains "$REPO_ROOT/docs/cloud-firewall-checklist.md" "脚本会按 \`timeout\`、\`gtimeout\` 顺序自动选择"
    assert_file_contains "$REPO_ROOT/docs/cloud-firewall-checklist.en.md" "The script auto-selects \`timeout\` then \`gtimeout\`"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "curl --connect-timeout 3 --max-time 10 http://nas.example.com:8080/health"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "curl --connect-timeout 3 --max-time 10 http://nas.example.com:8080/health"
    assert_file_contains "$REPO_ROOT/docs/cloud-firewall-checklist.md" "curl --connect-timeout 3 --max-time 10 http://nas.example.com:8080/health"
    assert_file_contains "$REPO_ROOT/docs/cloud-firewall-checklist.en.md" "curl --connect-timeout 3 --max-time 10 http://nas.example.com:8080/health"
}

trap cleanup EXIT
mkdir -p "$TMP_ROOT"

run_success_test
run_invalid_domain_test
run_invalid_timeout_test
run_gtimeout_fallback_test
run_timeout_override_test
run_missing_timeout_tool_test
run_invalid_backend_targets_test
run_redirect_variant_test
run_bad_redirect_test
run_open_backend_test
run_open_backend_http_status_test
run_docs_contract_test

printf '[public-go-live-smoke-test] all checks passed\n'
