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

cleanup() {
    rm -rf "$TMP_ROOT"
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
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"

    env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$curl_log" \
        bash "$REPO_ROOT/scripts/public-go-live-smoke.sh" "NAS.EXAMPLE.COM." > "$case_dir/out.log" 2>&1

    assert_file_contains "$case_dir/out.log" "public go-live smoke passed for nas.example.com"
    assert_file_contains "$case_dir/out.log" "HTTPS health returned HTTP 200"
    assert_file_contains "$curl_log" "https://nas.example.com/health"
    assert_file_contains "$curl_log" "http://nas.example.com/health"
    assert_file_contains "$curl_log" "http://nas.example.com:8080/health"
    assert_file_contains "$curl_log" "http://nas.example.com:9090/"
    assert_file_contains "$curl_log" "http://nas.example.com:9091/health"
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
}

run_open_backend_test() {
    local case_dir="$TMP_ROOT/open-backend"
    local fake_bin="$case_dir/bin"
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"

    run_expect_failure "$case_dir/out.log" env \
        PATH="$fake_bin:$PATH" \
        PUBLIC_SMOKE_CURL_LOG="$case_dir/curl.log" \
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
}

trap cleanup EXIT
mkdir -p "$TMP_ROOT"

run_success_test
run_invalid_domain_test
run_invalid_timeout_test
run_bad_redirect_test
run_open_backend_test
run_docs_contract_test

printf '[public-go-live-smoke-test] all checks passed\n'
