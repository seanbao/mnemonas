#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="${TMPDIR:-/tmp}/mnemonas-webdav-smoke-test-$$"

fail() {
    echo "[webdav-smoke-test] ERROR: $*" >&2
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

assert_not_exists() {
    local path="$1"
    [[ ! -e "$path" ]] || fail "expected $path not to exist"
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

method="GET"
output=""
write_format=""
url=""
upload_file=""
destination=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        -X)
            method="$2"
            shift 2
            ;;
        -o)
            output="$2"
            shift 2
            ;;
        -w)
            write_format="$2"
            shift 2
            ;;
        -T)
            upload_file="$2"
            shift 2
            ;;
        -H)
            if [[ "$2" == Destination:* ]]; then
                destination="${2#Destination: }"
            fi
            shift 2
            ;;
        -I)
            method="HEAD"
            shift
            ;;
        --config|--netrc-file)
            shift 2
            ;;
        --connect-timeout=*|--max-time=*)
            printf '%s\n' "$1" >> "$CURL_INVOKED_LOG"
            shift
            ;;
        --insecure|-s|-S|-sS|-f|-fsS)
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

printf '%s %s\n' "$method" "$url" >> "$CURL_INVOKED_LOG"
if [[ -n "$destination" ]]; then
    printf 'Destination: %s\n' "$destination" >> "$CURL_INVOKED_LOG"
fi

status="200"
body=""
case "$method" in
    OPTIONS)
        status="200"
        ;;
    MKCOL)
        status="201"
        ;;
    PUT)
        status="201"
        ;;
    PROPFIND)
        status="207"
        body="<multistatus><response><href>$url/hello.txt</href></response></multistatus>"
        ;;
    GET)
        status="200"
        if [[ "$url" == */hello.txt ]]; then
            body="mnemonas webdav smoke"$'\n'
        elif [[ "$url" == */moved.txt ]]; then
            body="mnemonas webdav smoke"$'\n'
        elif [[ "$url" == */space%20name.txt ]]; then
            body="mnemonas webdav smoke spaced path"$'\n'
        fi
        ;;
    HEAD)
        if [[ "$url" == */copied.txt ]]; then
            status="404"
        else
            status="200"
        fi
        ;;
    COPY|MOVE)
        status="201"
        ;;
    DELETE)
        status="204"
        ;;
    *)
        status="500"
        ;;
esac

if [[ -n "$upload_file" ]]; then
    printf 'upload:%s\n' "$upload_file" >> "$CURL_INVOKED_LOG"
fi
if [[ -n "$output" ]]; then
    printf '%s' "$body" > "$output"
fi
if [[ "$write_format" == "%{http_code}" ]]; then
    printf '%s' "$status"
fi
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

run_missing_url_test() {
    local case_dir="$TMP_ROOT/missing-url"
    mkdir -p "$case_dir"

    run_expect_failure "$case_dir/out.log" env -u WEBDAV_URL -u MNEMONAS_WEBDAV_USERNAME -u MNEMONAS_WEBDAV_PASSWORD \
        bash "$REPO_ROOT/scripts/webdav-client-smoke.sh"
    assert_file_contains "$case_dir/out.log" "WEBDAV_URL is required"
}

run_partial_credentials_test() {
    local case_dir="$TMP_ROOT/partial-credentials"
    mkdir -p "$case_dir"

    run_expect_failure "$case_dir/out.log" env -u WEBDAV_USERNAME -u WEBDAV_PASSWORD -u MNEMONAS_WEBDAV_PASSWORD \
        WEBDAV_URL="http://127.0.0.1:18080/dav" \
        MNEMONAS_WEBDAV_USERNAME="family-user" \
        bash "$REPO_ROOT/scripts/webdav-client-smoke.sh"
    assert_file_contains "$case_dir/out.log" "both WebDAV username and password are required"
}

run_invalid_timeout_test() {
    local case_dir="$TMP_ROOT/invalid-timeout"
    mkdir -p "$case_dir"

    run_expect_failure "$case_dir/out.log" env -u MNEMONAS_WEBDAV_USERNAME -u MNEMONAS_WEBDAV_PASSWORD \
        WEBDAV_URL="http://127.0.0.1:18080/dav" \
        CURL_CONNECT_TIMEOUT="0" \
        bash "$REPO_ROOT/scripts/webdav-client-smoke.sh"
    assert_file_contains "$case_dir/out.log" "CURL_CONNECT_TIMEOUT must be a positive integer number of seconds"
}

run_invalid_url_test() {
    local case_dir="$TMP_ROOT/invalid-url"
    mkdir -p "$case_dir"

    run_expect_failure "$case_dir/whitespace.log" env -u MNEMONAS_WEBDAV_USERNAME -u MNEMONAS_WEBDAV_PASSWORD \
        WEBDAV_URL="http://127.0.0.1:18080/dav bad" \
        bash "$REPO_ROOT/scripts/webdav-client-smoke.sh"
    assert_file_contains "$case_dir/whitespace.log" "WEBDAV_URL must not contain whitespace"

    run_expect_failure "$case_dir/query.log" env -u MNEMONAS_WEBDAV_USERNAME -u MNEMONAS_WEBDAV_PASSWORD \
        WEBDAV_URL="http://127.0.0.1:18080/dav?token=abc" \
        bash "$REPO_ROOT/scripts/webdav-client-smoke.sh"
    assert_file_contains "$case_dir/query.log" "WEBDAV_URL must not contain query strings or fragments"

    run_expect_failure "$case_dir/userinfo.log" env -u MNEMONAS_WEBDAV_USERNAME -u MNEMONAS_WEBDAV_PASSWORD \
        WEBDAV_URL="http://user:pass@127.0.0.1:18080/dav" \
        bash "$REPO_ROOT/scripts/webdav-client-smoke.sh"
    assert_file_contains "$case_dir/userinfo.log" "WEBDAV_URL must not contain embedded credentials"

    run_expect_failure "$case_dir/backslash.log" env -u MNEMONAS_WEBDAV_USERNAME -u MNEMONAS_WEBDAV_PASSWORD \
        WEBDAV_URL='http://127.0.0.1:18080/dav\root' \
        bash "$REPO_ROOT/scripts/webdav-client-smoke.sh"
    assert_file_contains "$case_dir/backslash.log" "WEBDAV_URL must not contain backslashes"

    run_expect_failure "$case_dir/dot-segment.log" env -u MNEMONAS_WEBDAV_USERNAME -u MNEMONAS_WEBDAV_PASSWORD \
        WEBDAV_URL="http://127.0.0.1:18080/dav/../root" \
        bash "$REPO_ROOT/scripts/webdav-client-smoke.sh"
    assert_file_contains "$case_dir/dot-segment.log" "WEBDAV_URL must not contain dot segments"

    run_expect_failure "$case_dir/encoded-dot-segment.log" env -u MNEMONAS_WEBDAV_USERNAME -u MNEMONAS_WEBDAV_PASSWORD \
        WEBDAV_URL="http://127.0.0.1:18080/dav/%2e%2e/root" \
        bash "$REPO_ROOT/scripts/webdav-client-smoke.sh"
    assert_file_contains "$case_dir/encoded-dot-segment.log" "WEBDAV_URL must not contain dot segments"

    run_expect_failure "$case_dir/encoded-slash.log" env -u MNEMONAS_WEBDAV_USERNAME -u MNEMONAS_WEBDAV_PASSWORD \
        WEBDAV_URL="http://127.0.0.1:18080/dav%2Froot" \
        bash "$REPO_ROOT/scripts/webdav-client-smoke.sh"
    assert_file_contains "$case_dir/encoded-slash.log" "WEBDAV_URL must not contain encoded slashes or backslashes"
}

run_invalid_insecure_flag_test() {
    local case_dir="$TMP_ROOT/invalid-insecure"
    mkdir -p "$case_dir"

    run_expect_failure "$case_dir/out.log" env -u MNEMONAS_WEBDAV_USERNAME -u MNEMONAS_WEBDAV_PASSWORD \
        WEBDAV_URL="http://127.0.0.1:18080/dav" \
        CURL_INSECURE="true" \
        bash "$REPO_ROOT/scripts/webdav-client-smoke.sh"
    assert_file_contains "$case_dir/out.log" "CURL_INSECURE must be 0 or 1"
}

run_success_test() {
    local case_dir="$TMP_ROOT/success"
    local fake_bin="$case_dir/bin"
    local curl_log="$case_dir/curl.log"
    local secret="mnemonas-user-secret"
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"

    env \
        PATH="$fake_bin:$PATH" \
        CURL_INVOKED_LOG="$curl_log" \
        WEBDAV_URL="http://127.0.0.1:18080/dav" \
        MNEMONAS_WEBDAV_USERNAME="family-user" \
        MNEMONAS_WEBDAV_PASSWORD="$secret" \
        WEBDAV_TEST_ROOT="smoke-test" \
        bash "$REPO_ROOT/scripts/webdav-client-smoke.sh" > "$case_dir/out.log" 2>&1

    assert_file_contains "$case_dir/out.log" "WebDAV smoke passed for http://127.0.0.1:18080/dav"
    assert_file_contains "$curl_log" "--connect-timeout=10"
    assert_file_contains "$curl_log" "--max-time=30"
    assert_file_contains "$curl_log" "OPTIONS http://127.0.0.1:18080/dav/"
    assert_file_contains "$curl_log" "MKCOL http://127.0.0.1:18080/dav/smoke-test/"
    assert_file_contains "$curl_log" "PUT http://127.0.0.1:18080/dav/smoke-test/hello.txt"
    assert_file_contains "$curl_log" "PROPFIND http://127.0.0.1:18080/dav/smoke-test/"
    assert_file_contains "$curl_log" "GET http://127.0.0.1:18080/dav/smoke-test/hello.txt"
    assert_file_contains "$curl_log" "HEAD http://127.0.0.1:18080/dav/smoke-test/hello.txt"
    assert_file_contains "$curl_log" "PUT http://127.0.0.1:18080/dav/smoke-test/space%20name.txt"
    assert_file_contains "$curl_log" "GET http://127.0.0.1:18080/dav/smoke-test/space%20name.txt"
    assert_file_contains "$curl_log" "COPY http://127.0.0.1:18080/dav/smoke-test/hello.txt"
    assert_file_contains "$curl_log" "Destination: http://127.0.0.1:18080/dav/smoke-test/copied.txt"
    assert_file_contains "$curl_log" "MOVE http://127.0.0.1:18080/dav/smoke-test/copied.txt"
    assert_file_contains "$curl_log" "Destination: http://127.0.0.1:18080/dav/smoke-test/moved.txt"
    assert_file_contains "$curl_log" "GET http://127.0.0.1:18080/dav/smoke-test/moved.txt"
    assert_file_contains "$case_dir/out.log" "MOVE content matches uploaded content"
    assert_file_contains "$curl_log" "HEAD http://127.0.0.1:18080/dav/smoke-test/copied.txt"
    assert_file_contains "$case_dir/out.log" "MOVE source no longer exists"
    assert_file_contains "$curl_log" "DELETE http://127.0.0.1:18080/dav/smoke-test/space%20name.txt"
    assert_file_contains "$curl_log" "DELETE http://127.0.0.1:18080/dav/smoke-test/"
    assert_file_not_contains "$curl_log" "$secret"
}

run_docs_contract_test() {
    assert_file_contains "$REPO_ROOT/docs/development.md" './scripts/webdav-client-smoke.sh'
    assert_file_contains "$REPO_ROOT/docs/development.en.md" './scripts/webdav-client-smoke.sh'
    assert_file_contains "$REPO_ROOT/docs/testing-strategy.md" 'Standalone WebDAV smoke'
    assert_file_contains "$REPO_ROOT/docs/testing-strategy.en.md" 'Standalone WebDAV smoke'
    assert_file_contains "$REPO_ROOT/docs/webdav-compatibility.md" 'curl 协议 smoke'
    assert_file_contains "$REPO_ROOT/docs/webdav-compatibility.en.md" 'curl protocol smoke'
    assert_file_contains "$REPO_ROOT/docs/webdav-compatibility.md" "不包含空白、query、fragment、内嵌凭据、反斜杠、编码斜杠或编码反斜杠，也不包含 \`.\`/\`..\` 路径段"
    assert_file_contains "$REPO_ROOT/docs/webdav-compatibility.en.md" "without whitespace, query strings, fragments, embedded credentials, backslashes, encoded slashes, encoded backslashes, or \`.\`/\`..\` path segments"
    assert_file_contains "$REPO_ROOT/docs/webdav-compatibility.md" 'URL 编码空格路径'
    assert_file_contains "$REPO_ROOT/docs/webdav-compatibility.en.md" 'URL-encoded space paths'
    assert_file_contains "$REPO_ROOT/docs/webdav-compatibility.md" 'CURL_MAX_TIME'
    assert_file_contains "$REPO_ROOT/docs/webdav-compatibility.en.md" 'CURL_MAX_TIME'
}

trap cleanup EXIT
mkdir -p "$TMP_ROOT"

run_missing_url_test
run_partial_credentials_test
run_invalid_timeout_test
run_invalid_url_test
run_invalid_insecure_flag_test
run_success_test
run_docs_contract_test

printf '[webdav-smoke-test] all checks passed\n'
