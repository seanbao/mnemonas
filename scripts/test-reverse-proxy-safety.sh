#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
    printf '[reverse-proxy-test] ERROR: %s\n' "$*" >&2
    exit 1
}

assert_file_contains() {
    local path="$1"
    local expected="$2"
    grep -Fq -- "$expected" "$path" || fail "$path does not contain: $expected"
}

run_expect_failure() {
    local name="$1"
    local domain="$2"
    local email="$3"
    local expected="$4"
    local case_dir="$TMP_ROOT/$name"
    local status
    mkdir -p "$case_dir"

    set +e
    if [[ -n "$email" ]]; then
        bash "$REPO_ROOT/scripts/setup-reverse-proxy.sh" "$domain" "$email" > "$case_dir/out.log" 2>&1
    else
        bash "$REPO_ROOT/scripts/setup-reverse-proxy.sh" "$domain" > "$case_dir/out.log" 2>&1
    fi
    status=$?
    set -e

    [[ "$status" -ne 0 ]] || fail "$name was accepted"
    assert_file_contains "$case_dir/out.log" "$expected"
}

run_domain_validation_tests() {
    local long_label
    long_label="$(printf 'a%.0s' {1..64})"

    run_expect_failure "domain-label-trailing-hyphen" "bad-.example.com" "" "域名格式不安全"
    run_expect_failure "domain-label-leading-hyphen" "bad.-example.com" "" "域名格式不安全"
    run_expect_failure "domain-label-too-long" "${long_label}.example.com" "" "域名格式不安全"
}

run_email_validation_tests() {
    run_expect_failure "email-quote" "nas.example.com" 'admin@example.com"' "邮箱格式不安全"
    run_expect_failure "email-caddy-brace" "nas.example.com" 'admin@example.com{' "邮箱格式不安全"
    run_expect_failure "email-domain-invalid-label" "nas.example.com" "admin@bad-.example.com" "邮箱格式不安全"
}

run_domain_validation_tests
run_email_validation_tests

printf '[reverse-proxy-test] all checks passed\n'
