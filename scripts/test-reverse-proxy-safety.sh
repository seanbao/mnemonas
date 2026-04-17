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

assert_file_not_contains() {
    local path="$1"
    local unexpected="$2"
    if grep -Fq -- "$unexpected" "$path"; then
        fail "$path unexpectedly contains: $unexpected"
    fi
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
        env "$@" bash "$REPO_ROOT/scripts/setup-reverse-proxy.sh" "$domain" "$email" </dev/null > "$case_dir/out.log" 2>&1
    else
        env "$@" bash "$REPO_ROOT/scripts/setup-reverse-proxy.sh" "$domain" </dev/null > "$case_dir/out.log" 2>&1
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
    run_expect_failure_with_env "upstream-host-url-secrets-redacted" "nas.example.com" "" "MNEMONAS_UPSTREAM_HOST 主机格式不安全" \
        'MNEMONAS_UPSTREAM_HOST=https://user:super-secret@example.com?token=also-secret#frag'
    assert_file_not_contains "$TMP_ROOT/upstream-host-url-secrets-redacted/out.log" "https://user"
    assert_file_not_contains "$TMP_ROOT/upstream-host-url-secrets-redacted/out.log" "super-secret"
    assert_file_not_contains "$TMP_ROOT/upstream-host-url-secrets-redacted/out.log" "token=also-secret"
    assert_file_not_contains "$TMP_ROOT/upstream-host-url-secrets-redacted/out.log" "also-secret"
    assert_file_not_contains "$TMP_ROOT/upstream-host-url-secrets-redacted/out.log" "frag"
    run_expect_failure_with_env "upstream-host-command-separator" "nas.example.com" "" "MNEMONAS_UPSTREAM_HOST 主机格式不安全" \
        'MNEMONAS_UPSTREAM_HOST=127.0.0.1;bad'
    run_expect_failure_with_env "upstream-host-invalid-label" "nas.example.com" "" "MNEMONAS_UPSTREAM_HOST 主机格式不安全" \
        MNEMONAS_UPSTREAM_HOST=bad-.example.com
    run_expect_failure_with_env "upstream-host-wildcard" "nas.example.com" "" "MNEMONAS_UPSTREAM_HOST 不能是通配监听地址" \
        'MNEMONAS_UPSTREAM_HOST=*'
}

run_config_path_validation_tests() {
    local target_dir target_config

    run_expect_failure_with_env "config-path-parent-segment" "nas.example.com" "" "--config 不能包含父目录段" \
        MNEMONAS_CONFIG_PATH="$TMP_ROOT/config/../config.toml"

    run_expect_failure_with_env "config-path-control-character" "nas.example.com" "" "--config 不能包含控制字符" \
        MNEMONAS_CONFIG_PATH="$TMP_ROOT/config"$'\a'"/config.toml"

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

run_release_layout_nasd_discovery_test() {
    local case_dir="$TMP_ROOT/release-layout"
    mkdir -p "$case_dir/release/scripts"
    cp "$REPO_ROOT/scripts/setup-reverse-proxy.sh" "$case_dir/release/scripts/setup-reverse-proxy.sh"
    cat > "$case_dir/release/nasd" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
    chmod +x "$case_dir/release/nasd"

    MNEMONAS_REVERSE_PROXY_SELF_TEST=1 bash "$case_dir/release/scripts/setup-reverse-proxy.sh" > "$case_dir/self-test.log" 2>&1
    assert_file_contains "$case_dir/self-test.log" "[reverse-proxy-self-test] all checks passed"
}

run_public_setup_help_uses_command_name_test() {
    local case_dir="$TMP_ROOT/public-setup-help"
    local helper
    mkdir -p "$case_dir/bin"

    bash "$REPO_ROOT/scripts/setup-reverse-proxy.sh" --help > "$case_dir/source-help.log"
    assert_file_contains "$case_dir/source-help.log" "用法: sudo setup-reverse-proxy.sh [选项] <域名> [邮箱]"
    assert_file_contains "$case_dir/source-help.log" "sudo setup-reverse-proxy.sh --proxy caddy nas.example.com admin@example.com"
    assert_file_not_contains "$case_dir/source-help.log" "$REPO_ROOT/scripts/setup-reverse-proxy.sh"

    helper="$case_dir/bin/mnemonas-public-setup"
    cp "$REPO_ROOT/scripts/setup-reverse-proxy.sh" "$helper"
    bash "$helper" --help > "$case_dir/installed-help.log"
    assert_file_contains "$case_dir/installed-help.log" "用法: sudo mnemonas-public-setup [选项] <域名> [邮箱]"
    assert_file_contains "$case_dir/installed-help.log" "sudo mnemonas-public-setup --proxy caddy nas.example.com admin@example.com"
    assert_file_not_contains "$case_dir/installed-help.log" "$case_dir"
}

run_nginx_webdav_docs_include_destination_header_test() {
    local doc
    local -a docs=(
        "$REPO_ROOT/docs/reverse-proxy-setup.md"
        "$REPO_ROOT/docs/reverse-proxy-setup.en.md"
        "$REPO_ROOT/docs/security.md"
        "$REPO_ROOT/docs/security.en.md"
        "$REPO_ROOT/docs/docker-deployment.md"
        "$REPO_ROOT/docs/docker-deployment.en.md"
    )

    for doc in "${docs[@]}"; do
        assert_file_contains "$doc" 'proxy_pass_request_headers on;'
        # shellcheck disable=SC2016 # Match the literal nginx variable in docs.
        assert_file_contains "$doc" 'proxy_set_header Destination $http_destination;'
    done
}

run_docker_proxy_docs_include_trusted_proxy_cidrs_test() {
    assert_file_contains "$REPO_ROOT/docs/docker-deployment.md" 'trusted_proxy_cidrs = ["172.18.0.0/16"]'
    assert_file_contains "$REPO_ROOT/docs/docker-deployment.md" 'Docker bridge'
    assert_file_contains "$REPO_ROOT/docs/docker-deployment.en.md" 'trusted_proxy_cidrs = ["172.18.0.0/16"]'
    assert_file_contains "$REPO_ROOT/docs/docker-deployment.en.md" 'Docker bridge'
}

run_reverse_proxy_docs_include_dataplane_port_audit_test() {
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" "ss -tlnp | grep -E '80|443|8080|9090|9091'"
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" "ss -tlnp | grep -E '80|443|8080|9090|9091'"
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" "/proc/net/tcp"
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" "/proc/net/tcp"
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" "公网严格检查要求同时覆盖 IPv4 和 IPv6 监听"
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" "Public strict checks must cover both IPv4 and IPv6 listeners"
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" "公网严格检查还需要 \`curl\`、\`python3\`、\`getent\` 和 \`openssl\`"
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" "Public strict checks also require \`curl\`, \`python3\`, \`getent\`, and \`openssl\`"
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" "如果基础检查确认 Web/API/WebDAV 后端或 dataplane 端口监听在非 loopback 地址"
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" "If the basic checks confirm that the Web/API/WebDAV backend or dataplane ports listen on non-loopback addresses"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "不会输出完成摘要"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "instead of printing the completion summary"
}

run_webdav_docs_avoid_placeholder_password_test() {
    assert_file_not_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" 'change-this-webdav-password'
    assert_file_not_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" 'change-this-webdav-password'
    assert_file_not_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" '/srv/mnemonas/.mnemonas/secrets.json'
    assert_file_not_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" '/srv/mnemonas/.mnemonas/secrets.json'
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" 'WEBDAV_USER="<mnemonas-or-webdav-username>"'
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" 'WEBDAV_PASS="<mnemonas-or-webdav-password>"'
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" '生成密码位于 /srv/mnemonas/secrets.json 的 webdav_password 字段'
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" "curl_auth_config=\"\$(mktemp -t mnemonas-webdav-curl-auth.XXXXXX)\""
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" "curl --config \"\$curl_auth_config\" -X PROPFIND"
    assert_file_not_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" "curl -u \"\$WEBDAV_USER:\$WEBDAV_PASS\""
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" 'WEBDAV_USER="<mnemonas-or-webdav-username>"'
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" 'WEBDAV_PASS="<mnemonas-or-webdav-password>"'
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" 'webdav_password field in /srv/mnemonas/secrets.json'
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" "curl_auth_config=\"\$(mktemp -t mnemonas-webdav-curl-auth.XXXXXX)\""
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" "curl --config \"\$curl_auth_config\" -X PROPFIND"
    assert_file_not_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" "curl -u \"\$WEBDAV_USER:\$WEBDAV_PASS\""
}

run_reverse_proxy_docs_warn_against_insecure_traefik_dashboard_test() {
    # shellcheck disable=SC2016 # Match literal Markdown snippets containing backticks.
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" '不要在公网环境使用 `--api.insecure=true`'
    # shellcheck disable=SC2016 # Match literal Markdown snippets containing backticks.
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" 'Do not use `--api.insecure=true` in public deployments'
}

run_cloudflare_tunnel_docs_keep_backend_ports_private_test() {
    # shellcheck disable=SC2016 # Match literal Markdown snippets containing backticks.
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" '即使使用隧道，也不要把 `8080` 或改过的后端端口暴露到公网'
    # shellcheck disable=SC2016 # Match literal Markdown snippets containing backticks.
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.md" 'dataplane `9090/9091` 或改过的 dataplane 端口也应仅本机或受信私网可达'
    # shellcheck disable=SC2016 # Match literal Markdown snippets containing backticks.
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" 'Even with a tunnel, do not expose `8080` or custom backend ports to the public network'
    # shellcheck disable=SC2016 # Match literal Markdown snippets containing backticks.
    assert_file_contains "$REPO_ROOT/docs/reverse-proxy-setup.en.md" 'Keep dataplane `9090/9091`, or custom dataplane ports, loopback-only or private-network-only'
}

run_domain_validation_tests
run_email_validation_tests
run_port_validation_tests
run_upstream_host_validation_tests
run_config_path_validation_tests
run_config_rewrite_self_test
run_release_layout_nasd_discovery_test
run_public_setup_help_uses_command_name_test
run_nginx_webdav_docs_include_destination_header_test
run_docker_proxy_docs_include_trusted_proxy_cidrs_test
run_reverse_proxy_docs_include_dataplane_port_audit_test
run_webdav_docs_avoid_placeholder_password_test
run_reverse_proxy_docs_warn_against_insecure_traefik_dashboard_test
run_cloudflare_tunnel_docs_keep_backend_ports_private_test

printf '[reverse-proxy-test] all checks passed\n'
