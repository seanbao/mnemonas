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

run_expect_failure_with_env() {
    local name="$1"
    local domain="$2"
    local email="$3"
    local expected="$4"
    local case_dir="$TMP_ROOT/$name"
    local status
    shift 4
    mkdir -p "$case_dir"

    set +e
    if [[ -n "$email" ]]; then
        env "$@" bash "$REPO_ROOT/scripts/setup-reverse-proxy.sh" "$domain" "$email" > "$case_dir/out.log" 2>&1
    else
        env "$@" bash "$REPO_ROOT/scripts/setup-reverse-proxy.sh" "$domain" > "$case_dir/out.log" 2>&1
    fi
    status=$?
    set -e

    [[ "$status" -ne 0 ]] || fail "$name was accepted"
    assert_file_contains "$case_dir/out.log" "$expected"
}

run_expect_failure() {
    run_expect_failure_with_env "$1" "$2" "$3" "$4"
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

run_port_validation_tests() {
    run_expect_failure_with_env "upstream-public-port" "nas.example.com" "" "MNEMONAS_UPSTREAM_PORT 不能是 80 或 443" \
        MNEMONAS_UPSTREAM_PORT=443
    run_expect_failure_with_env "upstream-public-port-leading-zero" "nas.example.com" "" "MNEMONAS_UPSTREAM_PORT 不能是 80 或 443" \
        MNEMONAS_UPSTREAM_PORT=0443
    run_expect_failure_with_env "dataplane-grpc-public-port" "nas.example.com" "" "MNEMONAS_DATAPLANE_GRPC_PORT 不能是 80 或 443" \
        MNEMONAS_DATAPLANE_GRPC_PORT=443
    run_expect_failure_with_env "dataplane-http-public-port" "nas.example.com" "" "MNEMONAS_DATAPLANE_HTTP_PORT 不能是 80 或 443" \
        MNEMONAS_DATAPLANE_HTTP_PORT=80
    run_expect_failure_with_env "upstream-dataplane-conflict" "nas.example.com" "" "MNEMONAS_UPSTREAM_PORT 不能和 MNEMONAS_DATAPLANE_HTTP_PORT 相同" \
        MNEMONAS_UPSTREAM_PORT=18080 MNEMONAS_DATAPLANE_HTTP_PORT=18080
    run_expect_failure_with_env "upstream-dataplane-conflict-leading-zero" "nas.example.com" "" "MNEMONAS_UPSTREAM_PORT 不能和 MNEMONAS_DATAPLANE_GRPC_PORT 相同" \
        MNEMONAS_UPSTREAM_PORT=08080 MNEMONAS_DATAPLANE_GRPC_PORT=8080
    run_expect_failure_with_env "dataplane-port-conflict" "nas.example.com" "" "MNEMONAS_DATAPLANE_GRPC_PORT 不能和 MNEMONAS_DATAPLANE_HTTP_PORT 相同" \
        MNEMONAS_DATAPLANE_GRPC_PORT=19090 MNEMONAS_DATAPLANE_HTTP_PORT=19090
}

run_upstream_host_validation_tests() {
    run_expect_failure_with_env "upstream-host-url" "nas.example.com" "" "MNEMONAS_UPSTREAM_HOST 主机格式不安全" \
        MNEMONAS_UPSTREAM_HOST=http://127.0.0.1
    run_expect_failure_with_env "upstream-host-command-separator" "nas.example.com" "" "MNEMONAS_UPSTREAM_HOST 主机格式不安全" \
        'MNEMONAS_UPSTREAM_HOST=127.0.0.1;bad'
    run_expect_failure_with_env "upstream-host-invalid-label" "nas.example.com" "" "MNEMONAS_UPSTREAM_HOST 主机格式不安全" \
        MNEMONAS_UPSTREAM_HOST=bad-.example.com
    run_expect_failure_with_env "upstream-host-wildcard" "nas.example.com" "" "MNEMONAS_UPSTREAM_HOST 不能是通配监听地址" \
        'MNEMONAS_UPSTREAM_HOST=*'
}

run_config_path_validation_tests() {
    local target_dir target_config

    target_dir="$TMP_ROOT/config-target"
    mkdir -p "$target_dir"
    ln -s "$target_dir" "$TMP_ROOT/config-link-dir"
    run_expect_failure_with_env "config-path-symlink-directory" "nas.example.com" "" "--config 不能包含符号链接" \
        MNEMONAS_CONFIG_PATH="$TMP_ROOT/config-link-dir/config.toml"

    target_config="$TMP_ROOT/target-config.toml"
    printf '[server]\nport = 8080\n' > "$target_config"
    ln -s "$target_config" "$TMP_ROOT/config-link.toml"
    run_expect_failure_with_env "config-path-symlink-file" "nas.example.com" "" "--config 不能包含符号链接" \
        MNEMONAS_CONFIG_PATH="$TMP_ROOT/config-link.toml"
}

run_config_rewrite_self_test() {
    MNEMONAS_REVERSE_PROXY_SELF_TEST=1 bash "$REPO_ROOT/scripts/setup-reverse-proxy.sh" > "$TMP_ROOT/self-test.log" 2>&1
    assert_file_contains "$TMP_ROOT/self-test.log" "[reverse-proxy-self-test] all checks passed"
}

run_domain_validation_tests
run_email_validation_tests
run_port_validation_tests
run_upstream_host_validation_tests
run_config_path_validation_tests
run_config_rewrite_self_test

printf '[reverse-proxy-test] all checks passed\n'
