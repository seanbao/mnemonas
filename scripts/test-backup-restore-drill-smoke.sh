#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="${TMPDIR:-/tmp}/mnemonas-backup-restore-drill-smoke-test-$$"

fail() {
    echo "[backup-restore-drill-smoke-test] ERROR: $*" >&2
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
data=""
cookie_file=""

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
        -H)
            shift 2
            ;;
        --data|--data-raw|--data-binary|-d)
            data="$2"
            shift 2
            ;;
        --cookie|-b)
            cookie_file="$2"
            shift 2
            ;;
        --connect-timeout=*|--max-time=*)
            printf '%s\n' "$1" >> "$CURL_INVOKED_LOG"
            shift
            ;;
        --connect-timeout|--max-time)
            printf '%s=%s\n' "$1" "$2" >> "$CURL_INVOKED_LOG"
            shift 2
            ;;
        --insecure|-s|-S|-sS)
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
if [[ -n "$data" ]]; then
    printf 'data:%s\n' "$data" >> "$CURL_INVOKED_LOG"
fi
if [[ -n "$cookie_file" ]]; then
    printf 'cookie:%s\n' "$cookie_file" >> "$CURL_INVOKED_LOG"
fi

status="404"
body='{"success":false,"message":"not found"}'
case "$method $url" in
    "GET http://127.0.0.1:18080/api/v1/maintenance/backups")
        status="200"
        body='{"success":true,"data":[{"id":"home"}]}'
        ;;
    "GET http://127.0.0.1:18080/api/v1/maintenance/backups/home")
        status="200"
        body='{"success":true,"data":{"id":"home","name":"home backup"}}'
        ;;
    "POST http://127.0.0.1:18080/api/v1/maintenance/backups/home/run")
        status="200"
        body='{"success":true,"message":"backup completed","data":{"job_id":"home","status":"completed"}}'
        ;;
    "POST http://127.0.0.1:18080/api/v1/maintenance/backups/home/retention-check")
        status="200"
        body='{"success":true,"message":"retention check completed","data":{"job_id":"home","status":"completed"}}'
        ;;
    "POST http://127.0.0.1:18080/api/v1/maintenance/backups/home/restore-drill")
        if [[ "${BACKUP_SMOKE_FAIL_DRILL:-0}" == "1" ]]; then
            status="500"
            body='{"success":false,"message":"restore drill failed"}'
        else
            status="200"
            body='{"success":true,"message":"restore drill completed","data":{"id":"drill","job_id":"home","status":"completed","file_count":1,"verified_bytes":24,"artifact_kept":false}}'
        fi
        ;;
    "GET http://127.0.0.1:18080/api/v1/maintenance/backups/home/restore-report")
        status="200"
        body='{"job":{"id":"home"},"findings":[]}'
        ;;
esac

if [[ -n "$output" ]]; then
    printf '%s\n' "$body" > "$output"
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

run_missing_api_url_test() {
    local case_dir="$TMP_ROOT/missing-api-url"
    mkdir -p "$case_dir"

    run_expect_failure "$case_dir/out.log" env -u MNEMONAS_API_URL \
        MNEMONAS_BACKUP_JOB_ID="home" \
        bash "$REPO_ROOT/scripts/backup-restore-drill-smoke.sh"
    assert_file_contains "$case_dir/out.log" "MNEMONAS_API_URL is required"
}

run_missing_job_id_test() {
    local case_dir="$TMP_ROOT/missing-job-id"
    mkdir -p "$case_dir"

    run_expect_failure "$case_dir/out.log" env -u MNEMONAS_BACKUP_JOB_ID \
        MNEMONAS_API_URL="http://127.0.0.1:18080/api/v1" \
        bash "$REPO_ROOT/scripts/backup-restore-drill-smoke.sh"
    assert_file_contains "$case_dir/out.log" "MNEMONAS_BACKUP_JOB_ID is required"
}

run_invalid_url_test() {
    local case_dir="$TMP_ROOT/invalid-url"
    mkdir -p "$case_dir"

    run_expect_failure "$case_dir/whitespace.log" \
        env MNEMONAS_API_URL="http://127.0.0.1:18080/api/v1 bad" MNEMONAS_BACKUP_JOB_ID="home" \
        bash "$REPO_ROOT/scripts/backup-restore-drill-smoke.sh"
    assert_file_contains "$case_dir/whitespace.log" "MNEMONAS_API_URL must not contain whitespace"

    run_expect_failure "$case_dir/query.log" \
        env MNEMONAS_API_URL="http://127.0.0.1:18080/api/v1?token=abc" MNEMONAS_BACKUP_JOB_ID="home" \
        bash "$REPO_ROOT/scripts/backup-restore-drill-smoke.sh"
    assert_file_contains "$case_dir/query.log" "MNEMONAS_API_URL must not contain query strings or fragments"

    run_expect_failure "$case_dir/userinfo.log" \
        env MNEMONAS_API_URL="http://user:pass@127.0.0.1:18080/api/v1" MNEMONAS_BACKUP_JOB_ID="home" \
        bash "$REPO_ROOT/scripts/backup-restore-drill-smoke.sh"
    assert_file_contains "$case_dir/userinfo.log" "MNEMONAS_API_URL must not contain embedded credentials"

    run_expect_failure "$case_dir/dot-segment.log" \
        env MNEMONAS_API_URL="http://127.0.0.1:18080/api/../v1" MNEMONAS_BACKUP_JOB_ID="home" \
        bash "$REPO_ROOT/scripts/backup-restore-drill-smoke.sh"
    assert_file_contains "$case_dir/dot-segment.log" "MNEMONAS_API_URL must not contain dot segments"

    run_expect_failure "$case_dir/encoded-slash.log" \
        env MNEMONAS_API_URL="http://127.0.0.1:18080/api%2Fv1" MNEMONAS_BACKUP_JOB_ID="home" \
        bash "$REPO_ROOT/scripts/backup-restore-drill-smoke.sh"
    assert_file_contains "$case_dir/encoded-slash.log" "MNEMONAS_API_URL must not contain encoded slashes or backslashes"
}

run_invalid_job_id_test() {
    local case_dir="$TMP_ROOT/invalid-job-id"
    mkdir -p "$case_dir"

    run_expect_failure "$case_dir/out.log" \
        env MNEMONAS_API_URL="http://127.0.0.1:18080/api/v1" MNEMONAS_BACKUP_JOB_ID="home/primary" \
        bash "$REPO_ROOT/scripts/backup-restore-drill-smoke.sh"
    assert_file_contains "$case_dir/out.log" "MNEMONAS_BACKUP_JOB_ID must be a safe backup job ID"
}

run_invalid_cookie_file_test() {
    local case_dir="$TMP_ROOT/invalid-cookie-file"
    mkdir -p "$case_dir"

    run_expect_failure "$case_dir/out.log" \
        env MNEMONAS_API_URL="http://127.0.0.1:18080/api/v1" MNEMONAS_BACKUP_JOB_ID="home" MNEMONAS_COOKIE_FILE="$case_dir/missing-cookie.txt" \
        bash "$REPO_ROOT/scripts/backup-restore-drill-smoke.sh"
    assert_file_contains "$case_dir/out.log" "MNEMONAS_COOKIE_FILE must be a readable regular file"
}

run_invalid_timeout_test() {
    local case_dir="$TMP_ROOT/invalid-timeout"
    mkdir -p "$case_dir"

    run_expect_failure "$case_dir/out.log" \
        env MNEMONAS_API_URL="http://127.0.0.1:18080/api/v1" MNEMONAS_BACKUP_JOB_ID="home" CURL_MAX_TIME="0" \
        bash "$REPO_ROOT/scripts/backup-restore-drill-smoke.sh"
    assert_file_contains "$case_dir/out.log" "CURL_MAX_TIME must be a positive integer number of seconds"
}

run_invalid_flags_test() {
    local case_dir="$TMP_ROOT/invalid-flags"
    mkdir -p "$case_dir"

    run_expect_failure "$case_dir/keep-artifact.log" \
        env MNEMONAS_API_URL="http://127.0.0.1:18080/api/v1" MNEMONAS_BACKUP_JOB_ID="home" MNEMONAS_BACKUP_KEEP_ARTIFACT="true" \
        bash "$REPO_ROOT/scripts/backup-restore-drill-smoke.sh"
    assert_file_contains "$case_dir/keep-artifact.log" "MNEMONAS_BACKUP_KEEP_ARTIFACT must be 0 or 1"

    run_expect_failure "$case_dir/insecure.log" \
        env MNEMONAS_API_URL="http://127.0.0.1:18080/api/v1" MNEMONAS_BACKUP_JOB_ID="home" CURL_INSECURE="yes" \
        bash "$REPO_ROOT/scripts/backup-restore-drill-smoke.sh"
    assert_file_contains "$case_dir/insecure.log" "CURL_INSECURE must be 0 or 1"
}

run_success_test() {
    local case_dir="$TMP_ROOT/success"
    local fake_bin="$case_dir/bin"
    local curl_log="$case_dir/curl.log"
    local cookie_file="$case_dir/cookies.txt"
    local secret="mnemonas-session-secret"
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"
    printf 'mnemonas_session=%s\n' "$secret" > "$cookie_file"

    env \
        PATH="$fake_bin:$PATH" \
        CURL_INVOKED_LOG="$curl_log" \
        MNEMONAS_API_URL="http://127.0.0.1:18080/api/v1" \
        MNEMONAS_BACKUP_JOB_ID="home" \
        MNEMONAS_COOKIE_FILE="$cookie_file" \
        bash "$REPO_ROOT/scripts/backup-restore-drill-smoke.sh" > "$case_dir/out.log" 2>&1

    assert_file_contains "$case_dir/out.log" "backup restore drill smoke passed for http://127.0.0.1:18080/api/v1 job home"
    assert_file_contains "$curl_log" "--connect-timeout=10"
    assert_file_contains "$curl_log" "--max-time=600"
    assert_file_contains "$curl_log" "cookie:$cookie_file"
    assert_file_contains "$curl_log" "GET http://127.0.0.1:18080/api/v1/maintenance/backups"
    assert_file_contains "$curl_log" "GET http://127.0.0.1:18080/api/v1/maintenance/backups/home"
    assert_file_contains "$curl_log" "POST http://127.0.0.1:18080/api/v1/maintenance/backups/home/run"
    assert_file_contains "$curl_log" "POST http://127.0.0.1:18080/api/v1/maintenance/backups/home/retention-check"
    assert_file_contains "$curl_log" "POST http://127.0.0.1:18080/api/v1/maintenance/backups/home/restore-drill"
    assert_file_contains "$curl_log" 'data:{"keep_artifact":false}'
    assert_file_contains "$curl_log" "GET http://127.0.0.1:18080/api/v1/maintenance/backups/home/restore-report"
    assert_file_not_contains "$curl_log" "$secret"
    assert_file_not_contains "$case_dir/out.log" "$secret"
}

run_keep_artifact_test() {
    local case_dir="$TMP_ROOT/keep-artifact"
    local fake_bin="$case_dir/bin"
    local curl_log="$case_dir/curl.log"
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"

    env \
        PATH="$fake_bin:$PATH" \
        CURL_INVOKED_LOG="$curl_log" \
        MNEMONAS_API_URL="http://127.0.0.1:18080/api/v1/" \
        MNEMONAS_BACKUP_JOB_ID="home" \
        MNEMONAS_BACKUP_KEEP_ARTIFACT="1" \
        bash "$REPO_ROOT/scripts/backup-restore-drill-smoke.sh" > "$case_dir/out.log" 2>&1

    assert_file_contains "$curl_log" 'data:{"keep_artifact":true}'
    assert_file_contains "$case_dir/out.log" "backup restore drill smoke passed for http://127.0.0.1:18080/api/v1 job home"
}

run_api_failure_test() {
    local case_dir="$TMP_ROOT/api-failure"
    local fake_bin="$case_dir/bin"
    local curl_log="$case_dir/curl.log"
    mkdir -p "$case_dir"
    make_fake_curl "$fake_bin"

    run_expect_failure "$case_dir/out.log" \
        env PATH="$fake_bin:$PATH" CURL_INVOKED_LOG="$curl_log" BACKUP_SMOKE_FAIL_DRILL="1" \
        MNEMONAS_API_URL="http://127.0.0.1:18080/api/v1" MNEMONAS_BACKUP_JOB_ID="home" \
        bash "$REPO_ROOT/scripts/backup-restore-drill-smoke.sh"
    assert_file_contains "$case_dir/out.log" "run restore drill returned HTTP 500, expected one of: 200"
}

run_docs_contract_test() {
    assert_file_contains "$REPO_ROOT/docs/backup-guide.md" './scripts/backup-restore-drill-smoke.sh'
    assert_file_contains "$REPO_ROOT/docs/backup-guide.en.md" './scripts/backup-restore-drill-smoke.sh'
    assert_file_contains "$REPO_ROOT/docs/development.md" './scripts/backup-restore-drill-smoke.sh'
    assert_file_contains "$REPO_ROOT/docs/development.en.md" './scripts/backup-restore-drill-smoke.sh'
    assert_file_contains "$REPO_ROOT/docs/testing-strategy.md" './scripts/backup-restore-drill-smoke.sh'
    assert_file_contains "$REPO_ROOT/docs/testing-strategy.en.md" './scripts/backup-restore-drill-smoke.sh'
    assert_file_contains "$REPO_ROOT/docs/backup-guide.md" 'MNEMONAS_BACKUP_JOB_ID=external-disk'
    assert_file_contains "$REPO_ROOT/docs/backup-guide.en.md" 'MNEMONAS_BACKUP_JOB_ID=external-disk'
    assert_file_contains "$REPO_ROOT/docs/development.md" 'MNEMONAS_BACKUP_JOB_ID=external-disk'
    assert_file_contains "$REPO_ROOT/docs/development.en.md" 'MNEMONAS_BACKUP_JOB_ID=external-disk'
    assert_file_contains "$REPO_ROOT/docs/testing-strategy.md" 'MNEMONAS_BACKUP_JOB_ID=external-disk'
    assert_file_contains "$REPO_ROOT/docs/testing-strategy.en.md" 'MNEMONAS_BACKUP_JOB_ID=external-disk'
}

trap cleanup EXIT
mkdir -p "$TMP_ROOT"

run_missing_api_url_test
run_missing_job_id_test
run_invalid_url_test
run_invalid_job_id_test
run_invalid_cookie_file_test
run_invalid_timeout_test
run_invalid_flags_test
run_success_test
run_keep_artifact_test
run_api_failure_test
run_docs_contract_test

printf '[backup-restore-drill-smoke-test] all checks passed\n'
